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
			"description": "Spec-query (discovery by property, not by name): given a definition, find every OTHER definition that satisfies the same property, matched by the property's CONTENT HASH (up to operand types). A law shared and PROVEN on both sides means the two are interchangeable for it — this is how you reuse proven code without trusting a name. Query by example: point at a def whose property you want, get back who else satisfies it. Call `explain` on candidates before choosing — matching a property is not evidence that an artifact is trustworthy.",
			"inputSchema": obj(map[string]any{"name": str("definition name whose properties to query by")}, "name"),
		},
		{
			"name":        "find_spec",
			"description": "AGENT DISCOVERY FROM INTENT — the entry point to the discovery workflow, and the step where YOU do the translation. Take the user's natural-language requirement and project it LOCALLY into a fresh Oath (defn ...) whose (prop ...) clauses state the behaviour required: the sought function is `self`, and the body may be any trivial well-typed expression. Send only that SPEC — never the user's original intent, task context, or prose — and you get back every PROVEN definition satisfying it, matched by content hash, no name and no example needed. Sending the spec rather than the sentence is not a formality: the registry cannot leak intent it never receives, which is why the projection belongs on your side and not in a hosted service. Then call `explain` on each candidate BEFORE choosing one — satisfying the query is not the same as being trustworthy.",
			"inputSchema": obj(map[string]any{"source": str("an Oath (defn ...) whose properties are the spec to search for")}, "source"),
		},
		{
			"name":        "find_implies",
			"description": "Spec-query by PROOF-IMPLICATION: like find_spec, but finds every definition that PROVABLY satisfies the spec (via Z3), not just the ones whose stated law matches by shape. Catches semantic matches the content-hash surface misses — e.g. commutativity written `(== (self b a) (self a b))` still proves against `+`. Slower (a proof per same-signature candidate) but the real reuse question: 'who can I prove satisfies this, however they wrote their own specs?'. As with find_spec, project the user's intent into the spec LOCALLY and send only the spec, then call `explain` on each candidate before selecting.",
			"inputSchema": obj(map[string]any{"source": str("an Oath (defn ...) whose properties are the spec to prove against candidates")}, "source"),
		},
		{
			"name":        "find_equiv",
			"description": "Discovery by BODY-EQUIVALENCE (the e-graph): find every definition that is the SAME FUNCTION as this one up to the rewrite rules (currently commutativity) — a different implementation that normalizes to the same canonical form. The matched definitions keep DISTINCT identities; this draws an equivalence edge, it never merges objects. This is how a fresh implementation finds the proven one it's equal to.",
			"inputSchema": obj(map[string]any{"name": str("definition name to find equivalents of")}, "name"),
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
			"name":        "explain",
			"description": "DECISION PACKAGE — the SELECTION step for a candidate returned by find, find_spec or find_implies. Do NOT choose from search results alone: those tools tell you what SATISFIES a query, and this tells you whether the artifact is trustworthy and appropriate, which is a different question. Compare candidates on per-property proof status, spec strength and its freshness (MEASURED vs STALE vs UNMEASURED), provenance including whether spec and body had independent authors, the exact dependency closure, and — most usefully — the LIMITATIONS: the recorded reasons NOT to use it. Everything is derived from recorded state, so a definition cannot look better than its evidence: `tested` is distinguished from `proven`, absent mutation evidence is reported as UNMEASURED rather than as a zero score, and waived mutants are listed with their justifications so you judge the reasoning rather than the number. A definition that satisfies your spec may still be falsified, unproven, weakly specified, or measured under a superseded campaign.",
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
func mcpCallTool(st *Store, name string, args json.RawMessage, principal string, canWrite bool) (string, error) {
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
	// Capability gate: state-changing tools require write. `put` authors objects
	// and moves names; `cross --record` writes the journal. A read-only bearer
	// token can still read, discover, and re-verify — just not author. Sign the
	// request or use a write-scoped token. (#14)
	if (name == "put" || (name == "cross" && a.Record)) && !canWrite {
		return "", fmt.Errorf("principal %q is read-only: %q needs write capability — sign the request (X-Oath-Signature) or use a token with \"write\": true", principal, name)
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
	case "find_spec":
		return apiFindSpec(st, a.Source)
	case "find_implies":
		return apiFindImplies(st, a.Source)
	case "find_equiv":
		return apiFindEquiv(st, a.Name)
	case "ls":
		return apiLs(st), nil
	case "eval":
		return apiEval(st, a.Expr)
	case "verify":
		return apiVerify(st, a.Name)
	case "mutate":
		return apiMutate(st, a.Name)
	case "explain":
		pkg, err := buildExplain(st, a.Name)
		if err != nil {
			return "", err
		}
		// JSON over MCP: the consumer is an agent choosing between candidates,
		// and a decision it has to parse out of prose is a decision it will get
		// wrong. The CLI keeps the human rendering.
		b, err := json.MarshalIndent(pkg, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
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
		// Same freshness rule as the HTTP surface (#70): this loop is long-lived
		// too, and a `prove`/`prove-worker` running beside it writes verdicts out
		// of band. An agent session holding a stdio server open would otherwise
		// keep reporting the guarantees it saw at startup.
		st.RefreshMutable()
		resp := handleRPC(st, &req, "", true) // local stdio: the invoking user owns the store
		reply(req.ID, resp.Result, resp.Error)
	}
}
