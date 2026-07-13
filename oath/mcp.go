package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// MCP server over stdio: newline-delimited JSON-RPC 2.0. This is the local
// ("git binary") layer — an agent session spawns `oath serve` per project
// and gets the substrate as tools. A hosted team store is the same protocol
// over HTTP with real principal auth; nothing here precludes it.
//
// Implemented by hand in the kernel's zero-dependency spirit: initialize,
// tools/list, tools/call, ping. Notifications are consumed silently.

type rpcRequest struct {
	Jsonrpc string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"` // nil => notification
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Jsonrpc string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

func mcpTools() []map[string]any {
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []map[string]any{
		{
			"name":        "context",
			"description": "Spec-only slice of named definitions plus their transitive dependencies (signatures, properties, guarantees — never bodies), greedily fitted to a token budget. The primary way to learn what exists before building on it.",
			"inputSchema": obj(map[string]any{
				"names":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "definition names to slice from"},
				"budget": map[string]any{"type": "integer", "description": "approximate token budget; 0 or omitted = unlimited"},
			}, "names"),
		},
		{
			"name":        "put",
			"description": "Submit Oath source (one or more (data ...) / (defn ...) forms). Typechecks at the gate, stores content-addressed, runs every property with deterministic inputs, checks termination and capability confinement, and journals the attempt. Returns per-definition verdicts with counterexamples on falsification.",
			"inputSchema": obj(map[string]any{
				"source": str("Oath source text"),
				"author": str("principal id for the journal (defaults to unattributed)"),
			}, "source"),
		},
		{
			"name":        "get",
			"description": "Full human projection of one definition: body, properties, hash, guarantee, termination, confinement, deps.",
			"inputSchema": obj(map[string]any{"name": str("definition name")}, "name"),
		},
		{
			"name":        "ls",
			"description": "List every named definition with hash, kind, and guarantee.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name":        "eval",
			"description": "Typecheck and evaluate a single Oath expression, e.g. (sort (Cons [Int] 2 (Cons [Int] 1 (Nil [Int])))).",
			"inputSchema": obj(map[string]any{"expr": str("Oath expression")}, "expr"),
		},
		{
			"name":        "verify",
			"description": "Re-run a definition's properties (200 deterministic cases each).",
			"inputSchema": obj(map[string]any{"name": str("definition name")}, "name"),
		},
		{
			"name":        "mutate",
			"description": "Score spec strength: generate type-preserving mutants of the body and check whether the properties notice. Survivors are printed with their bodies.",
			"inputSchema": obj(map[string]any{"name": str("definition name")}, "name"),
		},
		{
			"name":        "dependents",
			"description": "Reverse dependency query: which definitions reference this one.",
			"inputSchema": obj(map[string]any{"name": str("definition name")}, "name"),
		},
		{
			"name":        "log",
			"description": "The append-only submission journal: every put attempt (accepted, falsified, rejected) with principal, timestamp, and verifier version. Optionally filtered by name.",
			"inputSchema": obj(map[string]any{"name": str("filter to one definition name")}),
		},
	}
}

func mcpCallTool(st *Store, name string, args json.RawMessage) (string, error) {
	var a struct {
		Names  []string `json:"names"`
		Budget int      `json:"budget"`
		Source string   `json:"source"`
		Author string   `json:"author"`
		Name   string   `json:"name"`
		Expr   string   `json:"expr"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	switch name {
	case "context":
		if len(a.Names) == 0 {
			return "", fmt.Errorf("context needs at least one name")
		}
		return apiContext(st, a.Names, a.Budget)
	case "put":
		results, err := apiPut(st, a.Source, a.Author)
		out := renderPutReports(results)
		if err != nil {
			return "", fmt.Errorf("%s%w", out, err)
		}
		return out, nil
	case "get":
		return apiGet(st, a.Name)
	case "ls":
		return apiLs(st), nil
	case "eval":
		return apiEval(st, a.Expr)
	case "verify":
		return apiVerify(st, a.Name)
	case "mutate":
		return apiMutate(st, a.Name)
	case "dependents":
		return apiDependents(st, a.Name)
	case "log":
		return apiLog(st, a.Name), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func cmdServe(st *Store) {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	reply := func(id *json.RawMessage, result any, rerr *rpcError) {
		if id == nil {
			return // notification: no response
		}
		_ = out.Encode(rpcResponse{Jsonrpc: "2.0", ID: id, Result: result, Error: rerr})
	}
	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			reply(req.ID, map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "oath", "version": kernelVersion},
			}, nil)
		case "ping":
			reply(req.ID, map[string]any{}, nil)
		case "tools/list":
			reply(req.ID, map[string]any{"tools": mcpTools()}, nil)
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				reply(req.ID, nil, &rpcError{Code: -32602, Message: "invalid params"})
				continue
			}
			text, err := mcpCallTool(st, p.Name, p.Arguments)
			isErr := err != nil
			if isErr {
				text = "error: " + err.Error()
			}
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": isErr,
			}, nil)
		default:
			if req.ID != nil {
				reply(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
			}
		}
	}
}
