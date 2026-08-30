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

const registryBanner = `Oath — public verified-codebase registry
=========================================

This is an MCP-over-HTTP endpoint, not a web page.

  POST /mcp    JSON-RPC 2.0 — initialize, tools/list, tools/call
               tools: context, put, get, ls, find[/_spec/_implies/_equiv],
                      eval, verify, mutate, prove, explain, license, cross,
                      dependents, log
               (tools/list is authoritative; this line is prose and can drift)

Authentication (one required):
  X-Oath-Pubkey + X-Oath-Signature    Ed25519 over the raw request body;
                                       the principal IS the key (full capability)
  Authorization: Bearer <token>        read-only by default

Every definition is content-addressed (identity = hash of its canonical form)
and every proof is re-earned by whoever consumes it — the host is not a root of
trust. The store is public, re-verifiable bytes.

Docs & source: https://github.com/miclip/oath-lang
`

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

// loadAuthorizedKeys reads a JSON array of hex Ed25519 pubkeys — the write
// allowlist (#66). Empty path → nil, meaning open contribution.
func loadAuthorizedKeys(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(b, &keys); err != nil {
		return nil, fmt.Errorf("corrupt authorized-keys file: %w", err)
	}
	set := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if _, err := hex.DecodeString(k); err != nil || len(k) != ed25519.PublicKeySize*2 {
			return nil, fmt.Errorf("authorized-keys: %q is not a 32-byte hex pubkey", k)
		}
		set[k] = true
	}
	return set, nil
}

// anonymousReadEligible reports whether an UNAUTHENTICATED request may proceed as an
// anonymous, read-only principal. Two conditions, both required: public reads are
// enabled, AND the request carried NO credentials at all. A request that presented a
// signature or a token which then FAILED to validate is NOT eligible — it is a hard
// 401 — so a forged key or a bad token can never be laundered into anonymous access.
func anonymousReadEligible(publicReads bool, pubkey, signature, authorization string) bool {
	return publicReads && pubkey == "" && signature == "" && authorization == ""
}

func cmdServeHTTP(st *Store, addr, tokensPath, authKeysPath string, publicReads bool) {
	// Demand telemetry is a REGISTRY-side aggregate: it only means anything across many
	// callers, so the serving process is the only thing that should record it. A local
	// `oath find` is a read, and a read that writes into a git-tracked store would make
	// two clones diverge by who searched what (#94).
	EnableDemandRecording()

	// Two ways to authenticate a principal, and a request needs exactly one:
	//   - SIGNATURE (the real one, #14): X-Oath-Pubkey + X-Oath-Signature, an
	//     Ed25519 signature over the raw request body. The principal IS the key —
	//     unforgeable, no shared secret, and the server holds nothing. This is
	//     always available, so tokens are now optional.
	//   - BEARER TOKEN (the transport shim): a server-vouched principal, for
	//     clients that cannot sign. Only active when --tokens is given.
	// An unauthenticated store is still impossible: with no token file and no
	// valid signature, every request 401s.
	//
	// --authorized-keys is the OPTIONAL registration gate (#66): a JSON array of
	// hex pubkeys allowed to WRITE. Empty/absent → open contribution (any signer
	// may write). When set, an unlisted key still authenticates and READS, but its
	// writes are refused — reads stay open (the store is public, re-verifiable).
	var tokens map[string]tokenEntry
	if tokensPath != "" {
		var err error
		if tokens, err = loadTokens(tokensPath); err != nil {
			fail(err)
		}
	}
	authKeys, err := loadAuthorizedKeys(authKeysPath)
	if err != nil {
		fail(err)
	}
	mux := http.NewServeMux()
	// A human hitting the root gets a plain-text banner, not a 404 — the URL is
	// shareable and self-documenting even though the real surface is /mcp.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, registryBanner)
	})
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
		principal, canWrite, signed, ok := authenticatePrincipal(r, body, tokens, authKeys)
		if !ok {
			// With --public-reads, a request carrying NO credentials at all is served
			// as an anonymous, read-only principal: the store is public and
			// re-verifiable, so reads need no identity. Writes stay gated — mcpCallTool
			// refuses put/reserve/delegate/transfer for canWrite=false, signed=false.
			// A request with credentials that FAILED to validate is never laundered
			// into anonymous access (see anonymousReadEligible), so a forged key cannot
			// quietly downgrade to an anonymous read.
			if anonymousReadEligible(publicReads, r.Header.Get("X-Oath-Pubkey"), r.Header.Get("X-Oath-Signature"), r.Header.Get("Authorization")) {
				principal, canWrite, signed = "anonymous", false, false
			} else {
				http.Error(w, "unauthenticated: present a valid X-Oath-Signature over the body, or a known bearer token", http.StatusUnauthorized)
				return
			}
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
		// Observe the store as it is NOW, not as it was when this process
		// started (#70). The prove-worker writes verdicts out of band, so a
		// cached view served stale — and stale verdicts from a registry whose
		// whole claim is "these were re-derived here" are false verdicts, not
		// slow ones. Per REQUEST, not per tool call: one request is one
		// consistent snapshot, and the caching still pays for itself within a
		// heavy call (a prove touches the same metadata many times).
		st.RefreshMutable()
		resp := handleRPC(st, &req, principal, canWrite, signed, true) // hosted: name creation requires a signed principal
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	gate := "open contribution"
	if authKeys != nil {
		gate = fmt.Sprintf("%d authorized writer key(s)", len(authKeys))
	}
	reads := "reads require auth"
	if publicReads {
		reads = "PUBLIC reads (anonymous, read-only)"
	}
	fmt.Printf("oath team store: http://%s/mcp (writes signed; %s; %d bearer principals; %s; store %s)\n", addr, reads, len(tokens), gate, st.Root)
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
func authenticatePrincipal(r *http.Request, body []byte, tokens map[string]tokenEntry, authKeys map[string]bool) (principal string, canWrite, signed, ok bool) {
	pubHex := r.Header.Get("X-Oath-Pubkey")
	sigHex := r.Header.Get("X-Oath-Signature")
	if pubHex != "" || sigHex != "" {
		pub, perr := hex.DecodeString(pubHex)
		sig, serr := hex.DecodeString(sigHex)
		if perr != nil || serr != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), body, sig) {
			return "", false, false, false
		}
		// The principal IS the key (docs/registry-auth.md). Writes are open unless
		// a registration allowlist is set, in which case only listed keys may write
		// (#66); an unlisted key still authenticates and reads.
		canWrite := authKeys == nil || authKeys[pubHex]
		return pubHex, canWrite, true, true
	}
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if entry, ok := tokens[token]; ok {
			return entry.Principal, entry.Write, false, true
		}
	}
	return "", false, false, false
}

// handleRPC serves one JSON-RPC request. principal, when non-empty, is the
// AUTHENTICATED identity and overrides any client-supplied author. canWrite
// gates the state-changing tools (local stdio callers pass true — local trust).
func handleRPC(st *Store, req *rpcRequest, principal string, canWrite, signed, hosted bool) rpcResponse {
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
		text, err := mcpCallTool(st, p.Name, p.Arguments, principal, canWrite, signed, hosted)
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
