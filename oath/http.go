package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// The hosted team store (#2): the same MCP tool surface as stdio, over
// HTTP (streamable-HTTP's stateless subset: one JSON-RPC message per POST).
// The difference that matters is not the transport — it is that principals
// become AUTHENTICATED: the journal author derives from the bearer token's
// identity, and any client-supplied author field is ignored. Combined with
// the repoint policy, this is where authorship separation stops being
// discipline and becomes enforcement.
//
// Tokens file (never committed): {"<token>": {"principal": "name"}, ...}

type tokenEntry struct {
	Principal string `json:"principal"`
	// Write grants state-changing tools (put, cross --record). Default false: a
	// bearer token is READ-ONLY — it can read, discover, and re-verify, but a
	// shared secret should not be able to author objects or move names. Grant it
	// explicitly per token, or require a signature for writes. (#14)
	Write bool `json:"write,omitempty"`
}

func loadTokens(path string) (map[string]tokenEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]tokenEntry
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("corrupt tokens file: %w", err)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("tokens file has no entries")
	}
	for t, e := range m {
		if len(t) < 16 {
			return nil, fmt.Errorf("token for %q is too short (16+ chars)", e.Principal)
		}
		if e.Principal == "" {
			return nil, fmt.Errorf("token %s... has no principal", t[:4])
		}
	}
	return m, nil
}

func cmdServeHTTP(st *Store, addr, tokensPath string) {
	// Two ways to authenticate a principal, and a request needs exactly one:
	//   - SIGNATURE (the real one, #14): X-Oath-Pubkey + X-Oath-Signature, an
	//     Ed25519 signature over the raw request body. The principal IS the key —
	//     unforgeable, no shared secret, and the server holds nothing. This is
	//     always available, so tokens are now optional.
	//   - BEARER TOKEN (the transport shim): a server-vouched principal, for
	//     clients that cannot sign. Only active when --tokens is given.
	// An unauthenticated store is still impossible: with no token file and no
	// valid signature, every request 401s.
	var tokens map[string]tokenEntry
	if tokensPath != "" {
		var err error
		if tokens, err = loadTokens(tokensPath); err != nil {
			fail(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only (stateless streamable-HTTP subset)", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		principal, canWrite, ok := authenticatePrincipal(r, body, tokens)
		if !ok {
			http.Error(w, "unauthenticated: present a valid X-Oath-Signature over the body, or a known bearer token", http.StatusUnauthorized)
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json-rpc", http.StatusBadRequest)
			return
		}
		if req.ID == nil { // notification: acknowledge, no body
			w.WriteHeader(http.StatusAccepted)
			return
		}
		resp := handleRPC(st, &req, principal, canWrite)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	fmt.Printf("oath team store: http://%s/mcp (signature auth always on; %d bearer principals; store %s)\n", addr, len(tokens), st.Root)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fail(err)
	}
}

// authenticatePrincipal resolves the caller to a principal, or (,false). A
// signature (if the headers are present) is checked first and MUST be valid —
// present-but-invalid is a rejection, never a silent fall-through to tokens, so
// a forged pubkey can't be laundered into a token principal. The signed message
// is the exact request body; replay protection (nonce/timestamp) is a later
// addition, no weaker than a replayable bearer token today.
// authenticatePrincipal returns (principal, canWrite, ok). A key-holder who
// signs gets full capability; a bearer token gets only what it was granted
// (read-only by default). A present-but-invalid signature is a hard rejection.
func authenticatePrincipal(r *http.Request, body []byte, tokens map[string]tokenEntry) (string, bool, bool) {
	pubHex := r.Header.Get("X-Oath-Pubkey")
	sigHex := r.Header.Get("X-Oath-Signature")
	if pubHex != "" || sigHex != "" {
		pub, perr := hex.DecodeString(pubHex)
		sig, serr := hex.DecodeString(sigHex)
		if perr != nil || serr != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), body, sig) {
			return "", false, false
		}
		// The principal IS the key (docs/registry-auth.md); key-holders may write.
		return pubHex, true, true
	}
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if entry, ok := tokens[token]; ok {
			return entry.Principal, entry.Write, true
		}
	}
	return "", false, false
}

// handleRPC serves one JSON-RPC request. principal, when non-empty, is the
// AUTHENTICATED identity and overrides any client-supplied author. canWrite
// gates the state-changing tools (local stdio callers pass true — local trust).
func handleRPC(st *Store, req *rpcRequest, principal string, canWrite bool) rpcResponse {
	resp := rpcResponse{Jsonrpc: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		resp.Result = map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "oath", "version": kernelVersion},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return resp
		}
		text, err := mcpCallTool(st, p.Name, p.Arguments, principal, canWrite)
		isErr := err != nil
		if isErr {
			text = "error: " + err.Error()
		}
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}
