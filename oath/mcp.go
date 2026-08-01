package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
			"name":        "reserve",
			"description": "Claim a namespace prefix (SPEC §8.7). Submit the EXACT canonical octets of a signed oath-reserve/1 envelope, base64-encoded, plus its hex signature. Prefix authority is never inferred from publishing — this explicit signed act is the only way to obtain it. First-come: an unclaimed prefix goes to the first accepted claim. Returns the granted namespace, the new authority revision, and any names beneath the prefix that were already owned by somebody else and are RETAINED by them.",
			"inputSchema": obj(map[string]any{
				"envelope":  str("base64 of the exact canonical reservation octets that were signed"),
				"signature": str("hex Ed25519 signature over those octets"),
			}, "envelope", "signature"),
		},
		{
			"name":        "delegate",
			"description": "Grant or withdraw permission to publish under a namespace you hold (SPEC §8.7.7). Submit the exact canonical octets of a signed oath-delegate/1 envelope, base64-encoded, plus its hex signature. This grants PERMISSION, never AUTHORITY: a delegate may bind names under the prefix and may not reserve, delegate onward, or revoke, and the holder may withdraw them at any time. Requires a signed request by the current holder.",
			"inputSchema": obj(map[string]any{
				"envelope":  str("base64 of the exact canonical delegation octets that were signed"),
				"signature": str("hex Ed25519 signature over those octets"),
			}, "envelope", "signature"),
		},
		{
			"name":        "authority",
			"description": "Read the CURRENT authority state of a namespace prefix, or of the prefix governing a name (SPEC §8.7). Structured JSON, because a signing client needs the exact (authority, authority_rev) pair to build a reservation that will be accepted — a claim signed against a stale state is refused by the compare-and-swap. Read-only.",
			"inputSchema": obj(map[string]any{"name": str("a prefix pattern like \"alice/*\", or a definition name to resolve")}, "name"),
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
			"description": "AGENT DISCOVERY FROM INTENT — the entry point to the discovery workflow, and the step where YOU do the translation. Take the user's natural-language requirement and project it LOCALLY into a fresh Oath (defn ...) whose (prop ...) clauses state the behaviour required. Refer to the sought function BY THE NAME YOU GIVE IT in your defn — e.g. `(defn q ...)` then `(q (q xs))` — NOT by the literal token `self`, which does not elaborate. The body may be any trivial well-typed expression of the right type. MATCH THE SHAPE you expect the implementation to have: properties are matched by content hash, so a monomorphic query `(defn q [] [(xs (List Int))] ...)` will NOT match a polymorphic definition's law; if you are looking for something like list reversal, write `(defn q [a] [(xs (List a))] (List a) ...)` with the property over a concrete type. Send only that SPEC — never the user's original intent, task context, or prose — and you get back every PROVEN definition satisfying it, matched by content hash, no name and no example needed. Sending the spec rather than the sentence is not a formality: the registry cannot leak intent it never receives, which is why the projection belongs on your side and not in a hosted service. Then call `explain` on each candidate BEFORE choosing one — satisfying the query is not the same as being trustworthy.",
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
			"name": "license",
			"description": "DERIVED licensing verdict for a definition and its exact transitive " +
				"dependency closure (SPEC §12). Returns what the composition PERMITS in five " +
				"dimensions — commercial use, redistribution, modification, patent grant, and the " +
				"share-alike OBLIGATION — together with the evaluation's identity: engine, model " +
				"version, model content digest, policy, and a digest binding every consumed " +
				"assertion. Distinct from `explain`, which reports the publisher's raw ASSERTION " +
				"and explicitly declines to evaluate it. Read the result the way it is written: " +
				"UNSTATED is NOT permission, it is absence of evidence, and it is CONTAGIOUS — one " +
				"unknown or unmodelled dependency makes the whole composition unknown however many " +
				"others granted. This is not legal advice and is not PROVEN: it is a reproducible " +
				"derivation under a NAMED, versioned model, and the digest exists so a verdict can " +
				"be re-derived and compared rather than trusted.",
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
func mcpCallTool(st *Store, name string, args json.RawMessage, principal string, canWrite, signed, hosted bool) (string, error) {
	var a struct {
		Names   []string `json:"names"`
		Budget  int      `json:"budget"`
		Source  string   `json:"source"`
		Author  string   `json:"author"`
		Context string   `json:"context"`
		// The author's signed publication statement (#83). Envelope carries the
		// EXACT canonical bytes that were signed; the server must not normalise,
		// re-encode or pretty-print them at any point on the way to the journal.
		Envelope  string `json:"envelope"`
		Signature string `json:"signature"`
		Name      string `json:"name"`
		Hash      string `json:"hash"`
		NameB     string `json:"name_b"`
		Record    bool   `json:"record"`
		Expr      string `json:"expr"`
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
	if (name == "put" || name == "reserve" || name == "delegate" || (name == "cross" && a.Record)) && !canWrite {
		// The remedy depends on HOW the caller authenticated, and getting this
		// wrong wastes real time: telling someone who already signed to "sign the
		// request" hides the actual cause. It cannot be inferred from the
		// principal string either — a bearer token's principal may legitimately BE
		// a pubkey hex, so a signed-looking principal is not proof of a signature.
		if signed {
			return "", fmt.Errorf("principal %q is read-only: %q needs write capability. The request WAS validly signed, so the signature is not the problem — this key is absent from the server's authorized-keys allowlist. Add it there (the server reads the file at startup) and redeploy", principal, name)
		}
		return "", fmt.Errorf("principal %q is read-only: %q needs write capability — sign the request (X-Oath-Signature) with an authorized key, or use a token with \"write\": true", principal, name)
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
		// An author statement is only meaningful from a SIGNED request: the signing
		// key must be the authenticated principal, so a bearer-token caller cannot
		// present a statement attributed to somebody else's key.
		var auth *pubAuth
		if a.Envelope != "" || a.Signature != "" {
			if !signed {
				return "", fmt.Errorf("an author statement (envelope/signature) requires a SIGNED request: with a bearer token the principal is server-vouched, so a statement naming a key could not be tied to this caller")
			}
			if a.Envelope == "" || a.Signature == "" {
				return "", fmt.Errorf("author statement is incomplete: both envelope and signature are required, since either alone attests to nothing")
			}
			auth = &pubAuth{Bytes: a.Envelope, Sig: a.Signature, Pubkey: principal}
		}
		// THE FREEZE (legacy.go). On a HOSTED registry, creating a name is creating
		// permanent authority state, and it must begin with a verifiable authority
		// event rather than a server-vouched label. `hosted` rather than `!signed`:
		// local stdio serve is the invoking user's own store, where there is no
		// principal to establish and nothing to spoof.
		//
		// Narrow on purpose. This refuses CREATION, not publication — an existing
		// legacy name may still be updated under operator policy, which is what
		// "preserve legacy ambiguity" means. A name that already has a cryptographic
		// owner is governed by the ownership rules, not by this one.
		if hosted && auth == nil {
			for _, n := range sourceNames(a.Source) {
				if !nameExists(st, n) {
					return "", fmt.Errorf(bearerRefusal, n)
				}
				if !isLegacyUnowned(st, n) {
					return "", fmt.Errorf("%q is not in the frozen legacy set, so an unsigned request may not "+
						"repoint it: it was created by a signed publication and only a signed request from an "+
						"authorized principal may move it", n)
				}
			}
		}
		results, err := apiPutSigned(st, a.Source, a.Author, a.Context, auth)
		out := renderPutReports(results)
		if err != nil {
			return "", fmt.Errorf("%s%w", out, err)
		}
		return out, nil
	case "authority":
		// Structured on purpose: a client parsing prose would break on rewording,
		// and this is the value a signature is computed against.
		resp := map[string]any{"name": a.Name}
		if validNamespacePattern(a.Name) == nil {
			holder, rev := reservationRev(st, a.Name)
			resp["namespace"], resp["authority"], resp["authority_rev"] = a.Name, holder, rev.String()
			resp["delegation_rev"] = delegationRev(st, a.Name).String()
			// The keys permitted to publish, not merely how many times that set
			// has changed. A client asking who governs a prefix is asking about
			// both, and a delegation revision without the delegates tells it
			// something has changed while withholding what.
			ds := []string{}
			for k := range delegates(st)[a.Name] {
				ds = append(ds, k)
			}
			sort.Strings(ds)
			resp["delegates"] = ds
		} else if r, ok := governingReservation(st, a.Name); ok {
			resp["namespace"], resp["authority"], resp["authority_rev"] = r.Namespace, r.Pubkey, r.Rev.String()
		} else {
			resp["namespace"], resp["authority"], resp["authority_rev"] = "", noAuthority, "0"
		}
		if owner, src := nameOwner(st, a.Name); owner != "" {
			resp["exact_name_owner"], resp["owner_source"] = owner, src
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
	case "delegate":
		// Signed-only, for the same reason reserve is: RES/DEL rules require the
		// authenticated principal to BE the holder, and a bearer principal is
		// server-vouched rather than proved.
		if !signed {
			return "", fmt.Errorf("delegate requires a SIGNED request (X-Oath-Signature): only the key that holds a namespace may grant or revoke, and a bearer token's principal is vouched by the server rather than proved by a signature")
		}
		if a.Envelope == "" || a.Signature == "" {
			return "", fmt.Errorf("delegate needs both `envelope` and `signature`")
		}
		doct, derr2 := decodeEnvelopeB64(a.Envelope)
		if derr2 != nil {
			return "", fmt.Errorf("envelope: %w", derr2)
		}
		drep, rerr2 := apiDelegate(st, doct, a.Signature, principal)
		if rerr2 != nil {
			return "", rerr2
		}
		return renderDelegateReport(drep), nil
	case "reserve":
		// A reservation MUST be signed, never bearer-authenticated. RES-SIGNED
		// requires the authenticated principal to EQUAL the key named in the
		// envelope, and a bearer principal is server-vouched — it asserts who the
		// registry believes you are, not which key you hold. Accepting one here
		// would let the registry grant a namespace to a key that never signed for
		// it, which is the one thing this operation exists to make impossible.
		if !signed {
			return "", fmt.Errorf("reserve requires a SIGNED request (X-Oath-Signature): the claim must be made by the key it names, and a bearer token's principal is vouched by the server rather than proved by a signature")
		}
		if a.Envelope == "" || a.Signature == "" {
			return "", fmt.Errorf("reserve needs both `envelope` (base64 of the exact signed octets) and `signature`: either alone attests to nothing")
		}
		octets, derr := decodeEnvelopeB64(a.Envelope)
		if derr != nil {
			return "", fmt.Errorf("envelope: %w", derr)
		}
		rep, rerr := apiReserve(st, octets, a.Signature, principal)
		if rerr != nil {
			return "", rerr
		}
		return renderReserveReport(rep), nil
	case "head":
		// What a signing client needs before it can build an envelope: the parent it
		// would replace, that name's revision, and (for verification after publishing)
		// the envelope bytes the registry has recorded for an artifact. Structured
		// output on purpose — a client parsing `log` prose would break on rewording.
		parent, rev := nameRevision(st, a.Name)
		resp := map[string]any{"name": a.Name, "parent": parent, "parent_rev": rev}
		if a.Hash != "" {
			// LAST match, not first: the same artifact may be published more than once
			// (a same-hash re-publication is a valid recorded no-op), and a client
			// checking what was persisted means the publication it just made.
			for _, e := range st.ReadLog() {
				if e.Hash == a.Hash && e.EnvelopeB64 != "" {
					resp["envelope_b64"] = e.EnvelopeB64
					resp["author_pubkey"] = e.AuthorPubkey
				}
			}
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
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
	case "license":
		pkg, err := buildExplain(st, a.Name)
		if err != nil {
			return "", err
		}
		ev := evaluateLicensing(st, a.Name, pkg.depHashes)
		// JSON, for the same reason explain is: the consumer is an agent deciding
		// whether it may ship something, and the dimensions plus the evaluation
		// identity are the decision — prose it has to parse is a decision it gets
		// wrong. The CLI keeps the human rendering.
		b, err := json.MarshalIndent(struct {
			Name         string         `json:"name"`
			Engine       string         `json:"engine"`
			Model        string         `json:"model"`
			ModelDigest  string         `json:"model_digest"`
			Policy       string         `json:"policy"`
			Digest       string         `json:"evaluation_digest"`
			Commercial   string         `json:"commercial_use"`
			Redistribute string         `json:"redistribution"`
			Modify       string         `json:"modification"`
			PatentGrant  string         `json:"patent_grant"`
			ShareAlike   string         `json:"share_alike_obligation"`
			Unmodelled   int            `json:"unmodelled_inputs"`
			Inputs       []licenseInput `json:"inputs"`
			Caveat       string         `json:"caveat"`
		}{
			Name: a.Name, Engine: ev.Engine, Model: ev.Model, ModelDigest: ev.ModelDigest,
			Policy: ev.Policy, Digest: ev.Digest,
			Commercial:   ev.Result.Commercial.String(),
			Redistribute: ev.Result.Redistribute.String(),
			Modify:       ev.Result.Modify.String(),
			PatentGrant:  ev.Result.PatentGrant.String(),
			ShareAlike:   ev.Result.ShareAlike.String(),
			Unmodelled:   ev.Unmodeled, Inputs: ev.Inputs,
			Caveat: "DERIVED under a named, versioned model — not legal advice and not PROVEN. " +
				"UNSTATED is absence of evidence, never permission, and it is contagious.",
		}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
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
		resp := handleRPC(st, &req, "", true, false, false) // local stdio: the invoking user owns the store, and hosted=false leaves the name-creation freeze to the hosted surface
		reply(req.ID, resp.Result, resp.Error)
	}
}
