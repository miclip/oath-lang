package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
				"source":  str("Oath source text"),
				"author":  str("principal id for the journal (defaults to unattributed)"),
				"context": str("the context-hash line from the `context` tool output this code was authored against; journaled for stale-spec audits"),
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
			"name":        "find",
			"description": "Spec-query (discovery by property, not by name): given a definition, find every OTHER definition that satisfies the same property, matched by the property's CONTENT HASH. A law shared and PROVEN on both sides means the two are interchangeable for it — this is how you reuse proven code without trusting a name. Query by example: point at a def whose property you want, get back who else satisfies it.",
			"inputSchema": obj(map[string]any{"name": str("definition name whose properties to query by")}, "name"),
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
			"name":        "prove",
			"description": "SMT-prove a definition's properties for ALL inputs (Z3, unbounded-int semantics). Works on the non-recursive Int/Bool fragment; properties outside it stay tested with the bail reason explained. Refutations return a concrete counterexample model.",
			"inputSchema": obj(map[string]any{"name": str("definition name")}, "name"),
		},
		{
			"name":        "cross",
			"description": "N-version cross-check (misalignment detection): given two INDEPENDENTLY-authored definitions with identical signatures, run each one's properties against the other's body. AGREE means they compute the same function on the deterministic domain; DISAGREE returns the falsifying property and counterexample. Mutation kills spec weakness; this kills spec misalignment (a spec tight around the wrong function). Set record=true to journal the verdict.",
			"inputSchema": obj(map[string]any{"name": str("first definition name"), "name_b": str("second definition name"), "record": map[string]any{"type": "boolean", "description": "journal the verdict as provenance"}}, "name", "name_b"),
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

// mcpCallTool dispatches one tool call. principal, when non-empty, is an
// AUTHENTICATED identity (HTTP transport) and overrides any client-supplied
// author; the stdio transport passes "" (local trust, self-reported author).
func mcpCallTool(st *Store, name string, args json.RawMessage, principal string) (string, error) {
	var a struct {
		Names   []string `json:"names"`
		Budget  int      `json:"budget"`
		Source  string   `json:"source"`
		Author  string   `json:"author"`
		Context string   `json:"context"`
		Name    string   `json:"name"`
		NameB   string   `json:"name_b"`
		Record  bool     `json:"record"`
		Expr    string   `json:"expr"`
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
		if principal != "" {
			a.Author = principal
		}
		results, err := apiPut(st, a.Source, a.Author, a.Context)
		out := renderPutReports(results)
		if err != nil {
			return "", fmt.Errorf("%s%w", out, err)
		}
		return out, nil
	case "get":
		return apiGet(st, a.Name)
	case "find":
		return apiFind(st, a.Name)
	case "ls":
		return apiLs(st), nil
	case "eval":
		return apiEval(st, a.Expr)
	case "verify":
		return apiVerify(st, a.Name)
	case "mutate":
		return apiMutate(st, a.Name)
	case "cross":
		author := a.Author
		if principal != "" {
			author = principal
		}
		if author == "" {
			author = "unattributed"
		}
		return apiCross(st, a.Name, a.NameB, a.Record, author)
	case "prove":
		return apiProve(st, a.Name)
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
		if req.ID == nil {
			continue // notification
		}
		if strings.HasPrefix(req.Method, "notifications/") {
			continue
		}
		resp := handleRPC(st, &req, "")
		reply(req.ID, resp.Result, resp.Error)
	}
}
