package main

import (
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
	if tokensPath == "" {
		fail(fmt.Errorf("--http requires --tokens <file>: an unauthenticated network store would make every journal entry a lie"))
	}
	tokens, err := loadTokens(tokensPath)
	if err != nil {
		fail(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only (stateless streamable-HTTP subset)", http.StatusMethodNotAllowed)
			return
		}
		auth := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		entry, ok := tokens[token]
		if !ok {
			http.Error(w, "unknown token", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
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
		resp := handleRPC(st, &req, entry.Principal)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	fmt.Printf("oath team store: http://%s/mcp (%d principals; store %s)\n", addr, len(tokens), st.Root)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fail(err)
	}
}

// handleRPC serves one JSON-RPC request. principal, when non-empty, is the
// AUTHENTICATED identity and overrides any client-supplied author.
func handleRPC(st *Store, req *rpcRequest, principal string) rpcResponse {
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
		text, err := mcpCallTool(st, p.Name, p.Arguments, principal)
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
