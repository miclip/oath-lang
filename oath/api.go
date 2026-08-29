package main

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// String-returning implementations of every verb, shared by the CLI (which
// prints them) and the MCP server (which returns them as tool results).
// None of these exit the process; errors come back as errors.

// apiPut elaborates, gates, stores, verifies, and journals every form in
// src. It stops at the first rejection or elaboration error; results
// accumulated so far are returned alongside any error. ctxHash, when the
// author supplies one, is the context-slice hash it built against (#4) and
// is stamped on every journal entry this submission produces.
func apiPut(st *Store, src string, author string, ctxHash string) ([]putReport, error) {
	return apiPutSigned(st, src, author, ctxHash, nil)
}

// pubAuth is an author's signed publication statement, as received: the EXACT
// bytes and the signature over them. The bytes are never re-serialized between
// arrival and journalling — they are the author's historical statement, and
// normalising them would destroy the very thing being recorded.
type pubAuth struct {
	Bytes  string // exact canonical envelope bytes as received
	Sig    string // hex signature over Bytes
	Pubkey string // authenticated principal's key (NOT taken from the envelope)
}

// apiPutSigned is apiPut with an optional author statement. When auth is non-nil
// the statement is verified BEFORE the name moves, so an invalid signature can
// never appear as an accepted publication (#83). Verification order matters: each
// check below is cheap relative to the next, and none of them mutate the name.
func apiPutSigned(st *Store, src string, author string, ctxHash string, auth *pubAuth) ([]putReport, error) {
	if author == "" {
		author = "unattributed"
	}
	forms, err := parseForms(src)
	if err != nil {
		return nil, err
	}
	var results []putReport
	for _, f := range forms {
		if f.K != "list" || len(f.Kids) == 0 || f.Kids[0].K != "sym" {
			return results, fmt.Errorf("line %d: top-level forms must be (data ...) or (defn ...)", f.Line)
		}
		formName := "?"
		if len(f.Kids) >= 2 && f.Kids[1].K == "sym" {
			formName = f.Kids[1].Sym
		}
		var def *Def
		var meta *Meta
		switch f.Kids[0].Sym {
		case "data":
			def, meta, err = elabData(st, f)
		case "defn":
			def, meta, err = elabFunc(st, f)
		default:
			err = fmt.Errorf("line %d: unknown top-level form %q", f.Line, f.Kids[0].Sym)
		}
		if err != nil {
			_ = st.AppendLog(&LogEntry{Author: author, Name: formName, Status: "rejected", Error: err.Error(), Context: ctxHash})
			return results, err
		}
		meta.Author = author

		// The kernel gate: nothing enters the codebase without typechecking.
		// Rejections store no object, but the journal retains the attempt.
		if err := checkDef(st, def); err != nil {
			_ = st.AppendLog(&LogEntry{Author: author, Name: meta.Name, Kind: def.K, Status: "rejected", Error: err.Error(), Context: ctxHash})
			results = append(results, putReport{Name: meta.Name, Kind: def.K, Status: "rejected", Error: err.Error()})
			return results, nil
		}

		// Storage is unconditional past the gate (content addressing); the
		// NAME only moves if repoint policy passes, after verdicts exist.
		h, err := st.StoreObject(def, meta)
		if err != nil {
			return results, err
		}

		rep := putReport{Name: meta.Name, Hash: h, Kind: def.K, Status: "accepted", Ctors: len(def.Ctors)}
		if def.K == "func" {
			reports, err := verifyDef(st, h)
			if err != nil {
				return results, err
			}
			m, _ := st.GetMeta(h)
			m.Termination = terminationOf(st, def, h)
			m.Confinement = confinementOf(st, def)
			if err := st.SetMeta(h, m); err != nil {
				return results, err
			}
			rep.Guarantee = guaranteeString(m.Guarantee)
			rep.Termination = m.Termination
			rep.Confinement = confinementString(m)
			if m.Guarantee.Level == "falsified" {
				rep.Status = "falsified"
			}
			for _, r := range reports {
				label, text, hasDetail := r.Detail()
				rep.Props = append(rep.Props, propJSON{
					Name: r.Name, Passed: r.Passed, Indeterminate: r.Indet,
					Outcome: string(r.Outcome), Failed: r.Falsified(),
					Counterexample: r.Counter, Error: r.Err,
					Headline: r.Headline(), DetailLabel: label, Detail: text, HasDetail: hasDetail,
				})
			}
		}

		// THE AUTHOR-STATEMENT GATE. Everything here happens after elaboration (so
		// the artifact hash is known) and before Repoint (so a failure leaves the
		// name exactly where it was). The object itself is already stored, which is
		// correct and harmless: content addressing makes storage idempotent, and an
		// unreferenced object is inert. What must not happen is a NAME moving on an
		// unverified statement.
		if auth != nil {
			env, perr := envelopeParse([]byte(auth.Bytes))
			var gerr error
			if perr != nil {
				gerr = fmt.Errorf("author envelope is not canonical: %v", perr)
			} else {
				curParent, curRev := nameRevision(st, meta.Name)
				gerr = checkPublication(env, auth.Sig, auth.Pubkey, meta.Name, h, curParent, curRev)
			}
			if gerr != nil {
				rep.Status = "rejected"
				rep.Error = gerr.Error()
				results = append(results, rep)
				_ = st.AppendLog(&LogEntry{Author: author, Name: meta.Name, Kind: def.K,
					Status: "rejected", Hash: h, Error: rep.Error,
					NameTransition: transitionNone})
				continue
			}
		}

		specAuthor, bodyAuthor := attributeAuthorship(st, meta.Name, def, author)
		pol, err := LoadPolicy(st.Root)
		if err != nil {
			return results, err
		}
		if ok, reason := evalPolicy(st, pol, meta.Name, h, def, specAuthor, bodyAuthor); !ok {
			rep.Status = "blocked"
			rep.Error = reason
			_ = st.AppendLog(&LogEntry{
				Author: author, Name: meta.Name, Kind: def.K, Status: "blocked",
				Hash: h, Error: reason, Guarantee: rep.Guarantee, Termination: rep.Termination,
				Context: ctxHash,
			})
			results = append(results, rep)
			continue
		}

		// The asynchronous half: a require_proven name cannot bind until the
		// object is SMT-proven, and proving is too heavy to run here. Defer the
		// bind — store stays put, object is queued for the worker (#14).
		gm, _ := st.GetMeta(h)
		switch state, greason := provenGate(pol, meta.Name, gm, def); state {
		case "blocked":
			rep.Status = "blocked"
			rep.Error = greason
			_ = st.AppendLog(&LogEntry{
				Author: author, Name: meta.Name, Kind: def.K, Status: "blocked",
				Hash: h, Error: greason, Guarantee: rep.Guarantee, Termination: rep.Termination,
				Context: ctxHash,
			})
			results = append(results, rep)
			continue
		case "pending":
			rep.Status = "pending"
			rep.Error = greason
			if err := st.EnqueueProof(ProofJob{Hash: h, Name: meta.Name, Submitter: author, Gate: true}); err != nil {
				return results, err
			}
			_ = st.AppendLog(&LogEntry{
				Author: author, Name: meta.Name, Kind: def.K, Status: "pending",
				Hash: h, Error: greason, Guarantee: rep.Guarantee, Termination: rep.Termination,
				Context: ctxHash,
			})
			results = append(results, rep)
			continue
		}

		prev, err := st.Repoint(meta.Name, h)
		if err != nil {
			return results, err
		}
		rep.Prev = prev
		if m, err := st.GetMeta(h); err == nil {
			m.SpecAuthor, m.BodyAuthor = specAuthor, bodyAuthor
			_ = st.SetMeta(h, m)
		}
		le := &LogEntry{
			Author: author, Name: meta.Name, Kind: def.K, Status: rep.Status,
			Hash: h, Prev: prev, Guarantee: rep.Guarantee, Termination: rep.Termination,
			Context: ctxHash,
			// Reached only after Repoint succeeded, so a name operation happened —
			// but not necessarily a state CHANGE. A publication of the hash already
			// bound is a recorded no-op: valid, journalled, and not a new version of
			// the binding. Distinguishing them here is what keeps parent_rev a state
			// version rather than a publication counter.
			NameTransition: nameTransition(prev, h),
		}
		if auth != nil {
			// Verbatim. Not re-encoded from the parsed envelope: the bytes ARE the
			// statement, and a round-trip through the encoder would substitute this
			// kernel's rendering for the author's.
			le.EnvelopeB64, le.AuthorPubkey, le.AuthorSig = encodeEnvelopeB64([]byte(auth.Bytes)), auth.Pubkey, auth.Sig
			// The revision the author SIGNED AGAINST, preserved as their claim
			// rather than recomputed later (§8.2.1). The journal keeps everything
			// the publisher signed and nothing the registry merely computed.
			if env, perr := envelopeParse([]byte(auth.Bytes)); perr == nil {
				le.ParentRev = env.ParentRev.String()
			}
		}
		if err := st.AppendLog(le); err == nil {
			// The publication's own identity, so a client can address and verify the
			// exact accepted transition rather than searching by artifact hash.
			rep.JournalPosition = le.Seq
			if d, derr := entryDigest(le); derr == nil {
				rep.JournalEntry = d
			}
		}
		results = append(results, rep)
	}
	return results, nil
}

// renderPutReports formats put results the way the CLI prints them.
func renderPutReports(results []putReport) string {
	var b strings.Builder
	for _, rep := range results {
		status := ""
		switch {
		case rep.Prev == "":
			// First publication of this name; nothing was displaced.
		case rep.Prev == rep.Hash:
			// A no-op. Saying "repointed" here, with the SAME hash presented as the
			// "old version", reads as a change that did not happen.
			status = "  (no-op: the name already pointed at this version)"
		default:
			status = fmt.Sprintf("  (name repointed; old version %s remains immutable)", shortHash(rep.Prev))
		}
		switch {
		case rep.Status == "rejected":
			fmt.Fprintf(&b, "✗ %-16s REJECTED: %s\n", rep.Name, rep.Error)
		case rep.Status == "blocked":
			fmt.Fprintf(&b, "⛔ %-16s BLOCKED: %s\n", rep.Name, rep.Error)
			fmt.Fprintf(&b, "    object stored as #%s (%s); the name still points at its previous version\n", shortHash(rep.Hash), rep.Guarantee)
		case rep.Status == "pending":
			fmt.Fprintf(&b, "⏳ %-16s PENDING PROOF: %s\n", rep.Name, rep.Error)
			fmt.Fprintf(&b, "    object stored as #%s (%s); queued for `oath prove-worker` — the name binds once every property is proven\n", shortHash(rep.Hash), rep.Guarantee)
		case rep.Kind == "data":
			fmt.Fprintf(&b, "✓ %-16s #%s  data (%d constructors)%s\n", rep.Name, shortHash(rep.Hash), rep.Ctors, status)
		default:
			mark := "✓"
			if rep.Status == "falsified" {
				mark = "✗"
			}
			suffix := ""
			switch {
			case isTotal(rep.Termination):
				suffix = " · total"
			case rep.Termination == "unknown":
				suffix = " · termination unproven"
			}
			fmt.Fprintf(&b, "%s %-16s #%s  %s%s%s\n", mark, rep.Name, shortHash(rep.Hash), rep.Guarantee, suffix, status)
			if rep.Confinement != "" {
				fmt.Fprintf(&b, "    capabilities: %s\n", rep.Confinement)
			}
			for _, r := range rep.Props {
				fmt.Fprintf(&b, "    prop %-24s %s\n", r.Name, r.Headline)
				if r.HasDetail {
					fmt.Fprintf(&b, "      %s: %s\n", r.DetailLabel, r.Detail)
				}
			}
		}
	}
	return b.String()
}

// aliasTyVarNames returns the type-variable names for the name k AS IT RESOLVES:
// the object's top-level names when k is its most recent name, else k's own alias
// naming block (#19 per-alias vocabulary — the same selection store.go makes for
// constructor names). A polymorphic object published under two names with
// different type-variable spellings must render each row with ITS name's spelling,
// not the newest alias's.
func aliasTyVarNames(m *Meta, k string) []string {
	if m == nil {
		return nil
	}
	if m.Name != k {
		if a, ok := m.Aliases[k]; ok {
			return a.TyVarNames
		}
	}
	return m.TyVarNames
}

func apiLs(st *Store) string {
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		h := names[k]
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			continue
		}
		kind := "func"
		g := guaranteeString(m.Guarantee) + termSuffix(m)
		if d.K == "data" {
			kind = "data"
			g = fmt.Sprintf("%d constructors", len(d.Ctors))
		}
		// A function's SIGNATURE, so `oath ls` answers "what does this corpus hold
		// at this shape?" directly — `oath ls | grep '(-> (List'` finds the list
		// operations without writing a probe query. It trails the guarantee (rather
		// than sitting between the aligned columns) so the existing name/hash/kind/
		// guarantee layout is unchanged; the `::` marks it. Rendered with the alias's
		// own type-variable names, so a polymorphic type reads `(List a)`, not `t0`.
		line := fmt.Sprintf("%-16s #%s  %-5s %s", k, shortHash(h), kind, g)
		if d.K == "func" && d.Ty != nil {
			line += "  ::  " + printTy(st, d.Ty, aliasTyVarNames(m, k))
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	return b.String()
}

func apiGet(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	return printDef(st, h)
}

// provenContains reports whether property index i was SMT-proven for this def.
func provenContains(m *Meta, i int) bool {
	if m == nil {
		return false
	}
	for _, p := range m.ProvenProps {
		if p == i {
			return true
		}
	}
	return false
}

// storedPropName is the name the verifier and prover RECORD for property index i:
// the surface name when present, else a positional fallback. Guarantee.Falsified
// is keyed on exactly this spelling, so anything matching against it (the
// spec-query refutation renderer) MUST derive the name here rather than
// re-inventing the fallback — the two differ from propNameOf's human-readable
// "prop %d", and that space was exactly the drift that let a refuted property
// with a short PropNames list render as "tested". One function, no drift.
func storedPropName(m *Meta, i int) string {
	if m != nil && i < len(m.PropNames) {
		return m.PropNames[i]
	}
	return fmt.Sprintf("prop%d", i)
}

// refutedContains reports whether property index i was REFUTED for this def — a
// concrete countermodel exists. Guarantee level "falsified" records the NAMES of
// the refuted properties (the same field `oath ls` prints), not their indices, so
// attribution to a specific index is by name. This is the third verdict the
// spec-query renderer must keep distinct from a bare "tested": a definition
// DISPROVED against a law is the one a caller must not pick for it, and a
// two-valued mark hid it as an ordinary pass (finding #1 of the discovery-consumer
// experiment).
//
// Property names are NOT guaranteed unique within a def (elaboration does not
// enforce it), so a name-only match would wrongly mark a PASSING property refuted
// when it shares a name with a falsified sibling. Guard against that: attribute a
// refutation to index i only when EVERY property sharing its name is falsified —
// the name appears at least as often in the falsified list as among the def's
// properties. That covers the ordinary unique-name case exactly, and in an
// ambiguous partial collision falls back to "tested" rather than ever labelling a
// passing property REFUTED. The caller additionally excludes proven indices, so
// the sort and flag below are never driven by a stale refuted flag.
//
// nProps is the def's TRUE property count, not len(PropNames): the two differ for
// short/absent metadata, where later indices take a positional fallback name that
// can collide with an explicit earlier name — counting only PropNames would miss
// those and re-open the false-positive. A verdict/PropNames desync across a re-put
// cannot arise: a put re-verifies and rewrites Falsified under the current names in
// the same pipeline that records them, so the two are always consistent (measured).
func refutedContains(m *Meta, i, nProps int) bool {
	if m == nil || len(m.Guarantee.Falsified) == 0 {
		return false
	}
	name := storedPropName(m, i)
	inFalsified := 0
	for _, n := range m.Guarantee.Falsified {
		if n == name {
			inFalsified++
		}
	}
	if inFalsified == 0 {
		return false
	}
	inProps := 0
	for j := 0; j < nProps; j++ {
		if storedPropName(m, j) == name {
			inProps++
		}
	}
	if inProps == 0 { // nProps unknown (0): treat the single index as its own
		inProps = 1
	}
	return inFalsified >= inProps
}

func propNameOf(m *Meta, i int) string {
	if m != nil && i < len(m.PropNames) && m.PropNames[i] != "" {
		return m.PropNames[i]
	}
	return fmt.Sprintf("prop %d", i)
}

// findFromDef is the shared spec-query lookup: given a definition's properties
// (a stored def for query-by-example, or an ephemeral one elaborated from a
// fresh spec), find every OTHER definition in the store that satisfies each,
// matched by the property's generalized content hash — no name trusted.
// excludeHash omits the query def itself (empty for an ephemeral spec).
func findFromDef(st *Store, qd *Def, qm *Meta, excludeHash, header string) string {
	type qprop struct {
		name    string
		hash    string
		proven  bool
		refuted bool
	}
	var queries []qprop
	qidx := map[string]int{}
	for i := range qd.Props {
		ph := propHashGeneral(&qd.Props[i])
		if _, seen := qidx[ph]; seen {
			continue
		}
		qidx[ph] = len(queries)
		pv := provenContains(qm, i)
		queries = append(queries, qprop{propNameOf(qm, i), ph, pv, !pv && refutedContains(qm, i, len(qd.Props))})
	}

	type match struct {
		def      string
		propName string
		proven   bool
		refuted  bool
	}
	matches := make([][]match, len(queries))
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := names[k]
		if h == excludeHash {
			continue
		}
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" {
			continue
		}
		m, _ := st.GetMeta(h)
		for i := range d.Props {
			if j, ok := qidx[propHashGeneral(&d.Props[i])]; ok {
				pv := provenContains(m, i)
				matches[j] = append(matches[j], match{k, propNameOf(m, i), pv, !pv && refutedContains(m, i, len(d.Props))})
			}
		}
	}

	// A THREE-VALUED verdict, not two. "tested" and "REFUTED" are opposite
	// findings — a law that PASSED generated cases versus one DISPROVED by a
	// countermodel — and collapsing them to a single "tested" tells a caller a
	// falsified candidate is a viable choice. --implies already keeps refutation
	// distinct; --spec is the cheaper surface reached first, and must too.
	mark := func(proven, refuted bool) string {
		switch {
		case proven:
			return "proven"
		case refuted:
			return "REFUTED"
		default:
			return "tested"
		}
	}
	var b strings.Builder
	b.WriteString(header)
	for _, q := range queries {
		j := qidx[q.hash]
		fmt.Fprintf(&b, "\n  · %s [%s]  #%s\n", q.name, mark(q.proven, q.refuted)+" here", shortHash(q.hash))
		if len(matches[j]) == 0 {
			// An empty result is the most dangerous output this tool has. It reads
			// as "the registry has nothing", and a caller that believes it will
			// hand-roll something unverified when a proven implementation existed.
			// Matching is by property content hash, so a query that is correct-
			// looking but shaped differently — most often a call written without
			// the explicit type application a polymorphic definition requires —
			// misses SILENTLY. So say what is actually known: nothing states this
			// law AS WRITTEN, and here are the definitions with a compatible
			// signature that are worth re-querying against.
			// DEMAND SIGNAL (#75). A miss is the coverage request — someone
			// tried and failed to find something. Only the structural
			// fingerprint and signature are retained; see demand.go for what is
			// deliberately dropped.
			recordMiss(st, q.hash, querySignature(st, qd), time.Now())
			b.WriteString("      no definition states this law as written (matched by property content hash)\n")
			if near, total := signatureNeighboursN(st, qd, excludeHash); len(near) > 0 {
				fmt.Fprintf(&b, "      %d definition(s) have a COMPATIBLE SIGNATURE — the law may be stated differently, or your\n", total)
				b.WriteString("      query may be shaped differently (a call to a POLYMORPHIC definition needs explicit\n")
				b.WriteString("      type application, e.g. (q [Int] (q [Int] xs)) rather than (q (q xs))):\n")
				for _, n := range near {
					fmt.Fprintf(&b, "        %s\n", n)
				}
				b.WriteString("      Try `get <name>` to read how each states its properties, or find --implies to\n")
				b.WriteString("      search by PROOF rather than by shape.\n")
			}
			continue
		}
		// Refuted candidates render LOUDLY and sort LAST: a definition disproved
		// against this law is the one a caller must not pick, so it belongs at the
		// bottom of the group flagged, not interleaved as an ordinary "tested".
		sort.SliceStable(matches[j], func(a, b int) bool {
			return !matches[j][a].refuted && matches[j][b].refuted
		})
		for _, m := range matches[j] {
			flag := ""
			switch {
			case m.refuted:
				flag = "  ← DISPROVED for this law: a countermodel exists (find --implies --details shows it)"
			case m.proven && q.proven:
				flag = "  ← proven on both: interchangeable for this law"
			case m.proven:
				flag = "  ← a proven implementation of this spec"
			}
			fmt.Fprintf(&b, "      %-18s (%s as %q)%s\n", m.def, mark(m.proven, m.refuted), m.propName, flag)
		}
	}
	return b.String()
}

// apiFind is spec-query by example: given a definition, find OTHER definitions
// that satisfy the SAME property, matched by the property's generalized content
// hash, NOT by name. A property proven on both sides means the two are
// interchangeable for that law — discovery by meaning, no name trusted.
func apiFind(st *Store, name string) (string, error) {
	qh, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	qd, err := st.GetDef(qh)
	if err != nil {
		return "", err
	}
	if qd.K != "func" || len(qd.Props) == 0 {
		return "", fmt.Errorf("%q has no properties to query on", name)
	}
	qm, _ := st.GetMeta(qh)
	header := fmt.Sprintf("properties of %s, and which other definitions satisfy each (matched by content hash up to operand types, not name):\n", name)
	return findFromDef(st, qd, qm, qh, header), nil
}

// apiFindSpec is spec-query by a FRESH spec: elaborate a (defn ...) whose
// PROPERTIES are the query (the sought function is `self`; the body can be any
// trivial expression of the right type), then find every definition that
// satisfies them. This is "I have a spec — who has proven an implementation?",
// the core commons interaction, with no name and no example needed.
// reparsesAsLiteral reports whether a symbol is read as a value literal before
// local/self lookup — the only such symbols are the two Booleans (every other
// literal is a distinct token kind, so no binder can be named one).
func reparsesAsLiteral(s string) bool { return s == "true" || s == "false" }

// isPropForm reports whether x has the SHAPE of a property clause —
// (prop <name> [<params>] <body>) — not merely a list headed by `prop`. Function
// names are not reserved against `prop`, so an explicit body that CALLS a function
// named prop (e.g. (prop x)) must not be mistaken for a property clause and treated
// as an omitted body. A clause has a symbol name and a bracketed parameter list; a
// call does not.
func isPropForm(x sx) bool {
	return x.K == "list" && len(x.Kids) >= 4 && x.Kids[0].isSym("prop") &&
		x.Kids[1].K == "sym" && x.Kids[2].K == "brack"
}

// ensureQueryBody gives a spec-query (defn ...) a synthesized body when it omits
// one, so a query can be written as JUST its properties — the point of a SPEC query
// is that you have no implementation to write. The find matches on the property's
// CONTENT HASH, never the body, so the body only has to TYPE-CHECK.
//
// The synthesized body is a PARAMETER whose type is the return type, referenced by
// name. This is deliberately not a self-call: a bare parameter reference is never in
// head position, so it never routes through special-form or primitive dispatch and
// never mentions the function's own name — the two things that make a synthesized
// self-call collide with a query named `if`/`+`, a parameter that shadows it, and so
// on. Algebraic-law queries almost always have such a parameter (a commutative law
// over Int takes Int parameters; an involution over (List a) takes a (List a)), and
// this also handles a POLYMORPHIC return, which no inhabitant synthesizer could. When
// no parameter has the return type — a query returning Bool from Int parameters, a
// nullary query — a body genuinely cannot be reused, and an explicit one is required.
//
// A body is ABSENT iff the element after the return type is a property clause: a real
// body is an expression, which is never one (isPropForm matches the clause SHAPE, so
// a body that merely CALLS a function named prop is not mistaken for one).
func ensureQueryBody(st *Store, f sx) (sx, error) {
	if f.K != "list" || len(f.Kids) < 5 || !f.Kids[0].isSym("defn") || f.Kids[1].K != "sym" ||
		f.Kids[2].K != "brack" || f.Kids[3].K != "brack" {
		return f, nil // not a well-formed query defn; let elabFunc report it
	}
	if len(f.Kids) >= 6 && !isPropForm(f.Kids[5]) {
		return f, nil // a body is already present
	}
	// Match a parameter to the return type by CANONICAL IDENTITY, not surface
	// spelling: record fields are canonicalized independent of author order, a name
	// and its alias resolve to one data hash, and arrow nestings that curry
	// differently are the same type — all of which sxEqual-style syntax comparison
	// would miss. So elaborate the return type and each parameter type and compare
	// tyBytes, the same canonical encoding the rest of discovery uses. A type that
	// does not elaborate is left for elabFunc to report.
	tvs, err := tyvarNames(f.Kids[2])
	if err != nil {
		return f, nil
	}
	e := &elab{st: st, tyvars: tvs}
	retTy, err := e.parseTy(f.Kids[4])
	if err != nil {
		return f, nil
	}
	want := tyBytes(retTy)
	params := f.Kids[3].Kids
	// A parameter is LEXICALLY VISIBLE iff no later parameter reuses its name — a
	// bare reference resolves to the LAST binding of a name. Record each name's last
	// index, then take the FIRST visible parameter whose type is the return type.
	lastIdx := map[string]int{}
	for i, p := range params {
		if p.K == "list" && len(p.Kids) > 0 && p.Kids[0].K == "sym" {
			lastIdx[p.Kids[0].Sym] = i
		}
	}
	var body sx
	found := false
	for i, p := range params {
		if p.K != "list" || len(p.Kids) < 2 || p.Kids[0].K != "sym" {
			continue
		}
		pn := p.Kids[0].Sym
		if lastIdx[pn] != i {
			continue // shadowed by a later parameter of the same name
		}
		// A bare `true`/`false` reparses as a Boolean literal rather than the
		// parameter, so it cannot be used as the reference.
		if reparsesAsLiteral(pn) {
			continue
		}
		pty, perr := e.parseTy(p.Kids[1])
		if perr != nil {
			continue
		}
		if bytes.Equal(tyBytes(pty), want) {
			body, found = p.Kids[0], true
			break
		}
	}
	if !found {
		return f, fmt.Errorf("spec query %q needs an explicit body: no parameter has the return type to reuse as a placeholder — write any well-typed expression of the return type", f.Kids[1].Sym)
	}
	out := f
	out.Kids = make([]sx, 0, len(f.Kids)+1)
	out.Kids = append(out.Kids, f.Kids[:5]...)
	out.Kids = append(out.Kids, body)
	out.Kids = append(out.Kids, f.Kids[5:]...) // the properties
	return out, nil
}

func apiFindSpec(st *Store, src string) (string, error) {
	forms, err := parseForms(src)
	if err != nil {
		return "", err
	}
	if len(forms) == 0 {
		return "", fmt.Errorf("spec query is empty")
	}
	var b strings.Builder
	for _, f := range forms {
		if f.K != "list" || len(f.Kids) == 0 || !f.Kids[0].isSym("defn") {
			return "", fmt.Errorf("a spec query must be a (defn ...) whose properties are the query")
		}
		f, err = ensureQueryBody(st, f)
		if err != nil {
			return "", err
		}
		def, meta, err := elabFunc(st, f)
		if err != nil {
			return "", err
		}
		if err := checkDef(st, def); err != nil {
			return "", fmt.Errorf("spec query does not typecheck: %w", err)
		}
		if len(def.Props) == 0 {
			return "", fmt.Errorf("spec query %q has no properties (the query lives in the (prop ...) clauses)", meta.Name)
		}
		header := fmt.Sprintf("spec query %q — which proven definitions satisfy it (by content hash, no name, no example):\n", meta.Name)
		b.WriteString(findFromDef(st, def, meta, "", header))
	}
	return b.String(), nil
}

// crossTypeSub is the leaf-type substitution taking a query signature to a
// candidate signature: query primitive KIND -> candidate type. It is keyed by
// kind because generalizeTypes assigns one type variable per distinct primitive
// kind, shared across every occurrence — so the mapping is per-kind by
// construction, not per-position.
type crossTypeSub map[string]Ty

// crossTypeCompatible reports whether two signatures are equal up to primitive
// leaves, and if so returns the witnessing substitution. Compatibility is
// decided by generalizeTypes + tyBytes — the same anti-unification propHashGeneral
// already uses for the cross-type SHAPE surface — rather than by a hand-written
// structural walk: the generalization is the authority for what "same up to
// operand types" means, and a second walker deciding it independently could
// disagree with the hash surface. The walk below runs only AFTER equality is
// established, and only to read the witnessing map off two shapes already known
// to coincide.
//
// Note this admits (Int,Int)->Int against (Bool,Bool)->Bool: both generalize to
// (t0,t0)->t0. That is intended. Admission is not a claim — the PROOF is the
// filter, and a law that does not hold over the candidate's types simply fails
// to prove.
func crossTypeCompatible(qTy, cTy *Ty) (crossTypeSub, bool) {
	gq := generalizeTypes([]Ty{*qTy})
	gc := generalizeTypes([]Ty{*cTy})
	if !bytes.Equal(tyBytes(&gq[0]), tyBytes(&gc[0])) {
		return nil, false
	}
	sub := crossTypeSub{}
	var walk func(a, b *Ty) bool
	walk = func(a, b *Ty) bool {
		switch a.K {
		case "int", "rat", "float", "bool":
			if prev, ok := sub[a.K]; ok {
				return bytes.Equal(tyBytes(&prev), tyBytes(b))
			}
			sub[a.K] = *b
			return true
		case "fun":
			return b.K == "fun" && walk(a.A, b.A) && walk(a.B, b.B)
		case "data", "rec", "record":
			if b.K != a.K || len(a.Args) != len(b.Args) {
				return false
			}
			for i := range a.Args {
				if !walk(&a.Args[i], &b.Args[i]) {
					return false
				}
			}
			return true
		default:
			return bytes.Equal(tyBytes(a), tyBytes(b))
		}
	}
	if !walk(qTy, cTy) {
		return nil, false
	}
	return sub, true
}

// typeSubsumes reports whether the candidate type cTy, with its type PARAMETERS
// (k="var") free to be instantiated, can be made structurally equal to qTy — i.e.
// cTy is at least as general as qTy, so a query at qTy's exact types would reach a
// polymorphic definition of type cTy if only it were phrased polymorphically. It
// is DIAGNOSTIC ONLY: `find --implies` lines up types up to primitive leaves, so a
// monomorphic query silently skips these more-general candidates, and this lets
// the report say how many were skipped and why (finding #3). It changes no verdict.
//
// This is a one-sided match (candidate vars bind to query subterms; query vars, if
// any, are treated as ordinary leaves), NOT crossTypeCompatible's symmetric
// primitive-leaf equality — a different question, so a different walk. subst
// accumulates the instantiation and enforces that one candidate var maps to one
// query type.
func typeSubsumes(cTy, qTy *Ty, subst map[int]*Ty) bool {
	if cTy == nil || qTy == nil {
		return cTy == qTy
	}
	if cTy.K == "var" {
		if prev, ok := subst[cTy.Var]; ok {
			return bytes.Equal(tyBytes(prev), tyBytes(qTy))
		}
		subst[cTy.Var] = qTy
		return true
	}
	if cTy.K != qTy.K {
		return false
	}
	switch cTy.K {
	case "fun":
		return typeSubsumes(cTy.A, qTy.A, subst) && typeSubsumes(cTy.B, qTy.B, subst)
	case "data", "rec", "record":
		if cTy.Hash != qTy.Hash || len(cTy.Args) != len(qTy.Args) {
			return false
		}
		if cTy.K == "record" {
			if len(cTy.Names) != len(qTy.Names) {
				return false
			}
			for i := range cTy.Names {
				if cTy.Names[i] != qTy.Names[i] {
					return false
				}
			}
		}
		for i := range cTy.Args {
			if !typeSubsumes(&cTy.Args[i], &qTy.Args[i], subst) {
				return false
			}
		}
		return true
	default: // int, rat, float, bool, str, and any other leaf: equal iff same kind
		return bytes.Equal(tyBytes(cTy), tyBytes(qTy))
	}
}

// crossTypeRetypeBinders applies the substitution to a query property's BINDER
// types. Binder kinds absent from the signature are left unchanged (there is
// nothing to map them to), which is sound: an unmapped binder either still
// typechecks against the candidate or is rejected by checkDef.
//
// SCOPE, deliberately: only Prop.Binders are re-typed. Term.TyArgs (ref/self/ctor
// instantiations) and Term.Ty (lam/let annotations) inside a property BODY are
// left alone. The consequence is not unsoundness but REJECTION — a body-embedded
// type that disagrees with the re-typed binders fails checkDef and the candidate
// is dropped, which is why checkDef on the augmented definition is a required
// gate here and not an optimisation. Threading the substitution through bodies
// is a separate, larger change.
// retypeTyBySub rewrites a type under a primitive-leaf substitution — the shared
// core of cross-type re-typing, applied to BOTH a property's binders and (rung
// 3) the types embedded in its body.
func retypeTyBySub(sub crossTypeSub, root *Ty) Ty {
	// ITERATIVE, not recursive: TyArgs can carry types INFERRED by checkDef from a
	// stored/imported definition, which are bounded by the admission cap
	// (maxCanonicalNodes) rather than by source nesting, so descending one on the
	// host stack would be a #149-class overflow reachable from find --implies.
	out := new(Ty)
	type item struct{ src, dst *Ty }
	stack := []item{{root, out}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s, d := it.src, it.dst
		switch s.K {
		case "int", "rat", "float", "bool":
			if to, ok := sub[s.K]; ok {
				*d = to
			} else {
				*d = *s
			}
		case "fun":
			*d = *s
			d.A = new(Ty)
			d.B = new(Ty)
			stack = append(stack, item{s.A, d.A}, item{s.B, d.B})
		case "data", "rec", "record":
			*d = *s // K, Hash, and (for record) Names; Args rebuilt below
			if len(s.Args) > 0 {
				d.Args = make([]Ty, len(s.Args))
				for k := range s.Args {
					stack = append(stack, item{&s.Args[k], &d.Args[k]})
				}
			}
		default:
			*d = *s
		}
	}
	return *out
}

func crossTypeRetypeBinders(binders []Ty, sub crossTypeSub) []Ty {
	out := make([]Ty, len(binders))
	for i := range binders {
		out[i] = retypeTyBySub(sub, &binders[i])
	}
	return out
}

// crossTypeRetypeBody is RUNG 3 (#65). A cross-type candidate used to be admitted
// by re-typing the query property's BINDERS alone; a body carrying its own type
// arguments — a `(Nil [Int])`, a polymorphic callee's `[Int]` — kept those, so
// the re-typed property was ill-typed against the candidate and checkDef dropped
// it before the prover. Threading the SAME substitution through the body's type
// annotations and type-application arguments removes that asymmetry, so a law
// whose body mentions a type reaches the prover cross-type exactly as one whose
// only types are in its binders always has.
//
// checkDef remains the filter: this makes more augmentations WELL-TYPED, never
// more PROVEN. A substitution that produced something ill-typed still fails
// checkDef, precisely as before rung 3.
func crossTypeRetypeBody(root *Term, sub crossTypeSub) *Term {
	// Deep-copies the term, applying the cross-type substitution to every embedded
	// type (the lam/let annotation t.Ty and the ref/self/ctor type arguments
	// t.TyArgs) and copying structure verbatim. ITERATIVE on an explicit work
	// stack, not recursive: a query property may carry a deep canonical term — a
	// long string literal is a chain of SCons ctors, each with TyArgs — and
	// descending it on the host stack would reintroduce the #149 overflow that
	// admitted 5,000-node terms trigger. retypeTyBySub is likewise iterative,
	// since inferred TyArgs can carry admitted (not source-bounded) type depth.
	if root == nil {
		return nil
	}
	type item struct{ src, dst *Term }
	out := new(Term)
	stack := []item{{root, out}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s, d := it.src, it.dst
		*d = *s // scalars, K, Op, Hash, Idx, Names, Bool, Int, Rat, Float, and the
		// child pointers/slices — the ones with children are overwritten below.
		if s.Ty != nil {
			ty := retypeTyBySub(sub, s.Ty)
			d.Ty = &ty
		}
		if len(s.TyArgs) > 0 {
			d.TyArgs = make([]Ty, len(s.TyArgs))
			for i := range s.TyArgs {
				d.TyArgs[i] = retypeTyBySub(sub, &s.TyArgs[i])
			}
		}
		if s.A != nil {
			d.A = new(Term)
			stack = append(stack, item{s.A, d.A})
		}
		if s.B != nil {
			d.B = new(Term)
			stack = append(stack, item{s.B, d.B})
		}
		if s.C != nil {
			d.C = new(Term)
			stack = append(stack, item{s.C, d.C})
		}
		if len(s.Args) > 0 {
			d.Args = make([]Term, len(s.Args))
			for i := range s.Args {
				stack = append(stack, item{&s.Args[i], &d.Args[i]})
			}
		}
		if len(s.Arms) > 0 {
			d.Arms = make([]Term, len(s.Arms))
			for i := range s.Arms {
				stack = append(stack, item{&s.Arms[i], &d.Arms[i]})
			}
		}
	}
	return out
}

// apiFindImplies is spec-query by PROOF-IMPLICATION: find every definition that
// PROVABLY satisfies a fresh spec — not just the ones whose stated law matches
// by shape. Because a property is `self`-referential and de Bruijn, it is
// portable: for each candidate definition we append the query property and try
// to prove it (reusing the full prover, including that definition's own proven
// properties as lemmas and its body). If it proves, the definition satisfies the
// spec — even when the spec is written differently from any law that definition
// happens to have stated. This catches semantic matches that the content-hash
// surface misses (e.g. commutativity written `(== (self b a) (self a b))` still
// proves against `+`).
//
// The candidate set is signature-compatible, NOT same-signature: a definition
// whose signature equals the query's up to primitive leaves is admitted with the
// query PROPERTY re-typed to it — its binders AND the types embedded in its body
// (lam/let annotations, ref/self/ctor type arguments) — so an Int law finds its
// Rat, Float and Bool counterparts whether or not its body mentions a type.
// Exact matches are unaffected — they are proved with the query property
// verbatim.
//
// COST, measured on the committed corpus before this was widened (one real
// query, alternating order, three repetitions per mode): candidates 6 -> 9
// (1.500x), z3 subprocesses 12 -> 16 (1.333x), median wall 5m44.257s ->
// 5m44.981s (1.002x). The candidate set grows by half while wall clock does not
// move, because runtime here is dominated by goals that never prove, and the
// admitted cross-type candidates are not those.
// ---------------------------------------------------------------------------
// The pre-solver concrete pass (#80).
// ---------------------------------------------------------------------------

// ceOutcome is the TYPED result of the concrete search that runs before the
// solver in apiFindImplies.
//
// It is a type rather than a rendered string deliberately. The obvious
// implementation reuses runProp and reads its Counter text for the substring
// "runtime error:" to tell an implementation limit from a falsehood — which
// works, and makes the discrimination this pass depends on for SOUNDNESS one
// reworded message away from silently inverting. A caller that cannot tell "the
// property is false" from "the evaluator gave up" will eventually report the
// second as the first, and that is the exact error class this repo keeps
// catching.
type ceOutcome int

const (
	// ceNone: no sampled environment falsified the goal. Decides nothing — the
	// goal goes to the solver, which is the only thing that can prove it.
	ceNone ceOutcome = iota
	// ceFalsified: a concrete environment evaluated the goal to Boolean FALSE.
	ceFalsified
	// ceIndeterminate: at least one sample hit an IMPLEMENTATION LIMIT — value
	// generation failed, the evaluator errored, or fuel ran out — and none was
	// false. Never a reason to reject. Distinguished from ceNone only so a test
	// can witness that the two are not being conflated.
	ceIndeterminate
)

// ceResult is the typed result of the concrete pre-solver search: the OUTCOME,
// together with — for ceFalsified alone — the environment that witnessed it.
//
// Retaining the environment rather than only the verdict is what makes a
// refutation REPORTABLE. A verdict alone says only that SOME goal was false and
// never which values made it so, and a candidate reported that way is
// indistinguishable from one that was never a candidate. The countermodel is
// already in hand at the moment of the decision; keeping it is what lets the
// decision be shown rather than merely acted on.
//
// THE INVARIANT: env is non-nil exactly when outcome == ceFalsified. ceNone and
// ceIndeterminate carry no witness because neither HAS one — "no sample was
// false" and "a sample never finished" are both the ABSENCE of a countermodel,
// and attaching the last sampled environment to either would dress an
// implementation limit up as evidence. That is the conflation ceOutcome exists
// to prevent, arriving one layer out: the verdict would still be honest while
// the attached values quietly implied a refutation that never happened.
type ceResult struct {
	outcome ceOutcome
	env     []Value // the falsifying environment; non-nil iff outcome == ceFalsified
}

// witness renders the falsifying environment, and renders NOTHING for the two
// outcomes that have none — the invariant above, enforced at the one place a
// caller can read the witness rather than merely documented on the field.
//
// DETERMINISM is a property of both halves and neither is incidental. The search
// is seeded (findImpliesProbeSeed) and returns at the FIRST falsifying sample, so
// the retained environment is a function of (store, hash, prop) alone; and
// printValue is structural, with no map iteration anywhere in Value. The same
// query against the same corpus therefore renders the same witness on every run
// and every host — the same guarantee findImpliesProbeSeed exists to give the
// skip decision, extended to the evidence for it.
//
// It renders with printValue, which is what runProp already renders a
// counterexample with, so a witness reported here reads exactly like one
// reported by verify rather than introducing a second spelling of the same fact.
func (r ceResult) witness(st *Store) string {
	if r.outcome != ceFalsified {
		return ""
	}
	parts := make([]string, 0, len(r.env))
	for _, v := range r.env {
		parts = append(parts, printValue(st, v))
	}
	return strings.Join(parts, ", ")
}

// findImpliesProbeSeed fixes the sample sequence so the pass is DETERMINISTIC:
// the same query against the same corpus refutes exactly the same candidates on
// every run and on every host, AND reports the same countermodel for each. A
// discovery result that varied between runs would be a worse failure than a slow
// one, and that now covers the evidence as well as the verdict.
const findImpliesProbeSeed uint64 = 0x9E3779B97F4A7C15

const (
	findImpliesProbeCases = 64
	findImpliesProbeFuel  = 200_000
)

// concreteProbe looks for a concrete environment that makes the goal evaluate to
// Boolean false, and RETAINS the one it finds.
//
// WHY THIS IS SOUND AHEAD OF THE PROVER, where a structural or shape-based
// filter is not: evaluation is the reference semantics of the language, so a
// goal that evaluates to false under some concrete environment IS false, and no
// valid proof of it exists. Not calling the solver for such a goal therefore
// cannot remove a definition that would otherwise have been returned — the
// refutation and the proof would contradict each other. The argument is about the
// goal, not about any particular corpus, which is what makes it general.
//
// It is also why the outcome is reported as a REFUTATION rather than as a
// candidate that was passed over: the same argument that licenses skipping the
// solver is the argument that the goal is false. Calling this a skip understates
// what was established.
//
// A shape filter has no such argument, and `find --implies` exists precisely to
// see THROUGH shape: filtering on shape would reinstate the miss the prover is
// called to fix.
//
// WHAT IT MUST NEVER DO is promote an IMPLEMENTATION LIMIT to a semantic fact.
// Generation failure, evaluator errors and fuel exhaustion are all
// ceIndeterminate — "the evaluator could not finish" is not "the property is
// false", and a non-terminating candidate is the case that makes the difference
// visible. A non-Bool result is likewise not a falsehood; it is a surprise, and
// it defers to the solver.
func concreteProbe(st *Store, h string, p *Prop) ceResult {
	sawIndeterminate := false
	for c := 0; c < findImpliesProbeCases; c++ {
		// A budgeted `find --implies` bounds this sampling loop too, not only the
		// solver: 64 cases at 200k fuel is finite but not free, and leaving it
		// uncapped lets one candidate overrun --timeout in the non-solver stage.
		// Stopping early yields ceIndeterminate — no refutation was found and none
		// is claimed — and the candidate then meets the (capped) solver or the
		// abort. Checked every 8 cases so the check itself costs nothing.
		if c%8 == 0 && searchDeadlinePassed() {
			return ceResult{outcome: ceIndeterminate}
		}
		r := &rng{s: findImpliesProbeSeed ^ uint64(c)*0xD1B54A32D192ED03}
		size := c % 8
		env := make([]Value, 0, len(p.Binders))
		generated := true
		for bi := range p.Binders {
			v, err := genValue(st, &p.Binders[bi], size, r)
			if err != nil {
				generated = false
				break
			}
			env = append(env, v)
		}
		if !generated {
			sawIndeterminate = true
			continue
		}
		ev := &evaluator{st: st, fuel: findImpliesProbeFuel}
		out, err := ev.eval(env, h, &p.Body)
		if err != nil {
			sawIndeterminate = true
			continue
		}
		if out.K != "bool" {
			sawIndeterminate = true
			continue
		}
		if !out.Bool {
			// env is allocated fresh per case, so retaining it here cannot be
			// overwritten by a later sample — and there is no later sample, because
			// the first falsifying environment ends the search.
			return ceResult{outcome: ceFalsified, env: env}
		}
	}
	if sawIndeterminate {
		return ceResult{outcome: ceIndeterminate}
	}
	return ceResult{outcome: ceNone}
}

// ---------------------------------------------------------------------------
// What `find --implies` did NOT find (#156).
// ---------------------------------------------------------------------------

// implyStatus is what `find --implies` established about ONE admitted candidate
// against ONE query property. It is the four-way classification proveOne already
// makes, carried out to the report instead of collapsed at the boundary.
//
// The collapse it replaces printed a candidate only when it PROVED, so three
// materially different results rendered as the same silence: nothing in the
// corpus was close, something was REFUTED with a countermodel, and the prover
// declined. The first is a fact about the corpus, the second is a POSITIVE
// finding about the corpus, and the third is a fact about this implementation.
// Summing them states an implementation limit as a semantic one.
type implyStatus int

const (
	// implyProven: the query property was PROVED of this definition. The answer
	// to the query.
	implyProven implyStatus = iota
	// implyRefuted: the query property was proved FALSE of this definition, by a
	// concrete countermodel or by the solver. This is a result, not a residue —
	// "this definition provably does NOT satisfy your law, here is why" is
	// something established about the corpus, and burying it under "did not
	// prove" reports a proof as an absence.
	implyRefuted
	// implyUnknown: no verdict. The solver declined, the goal was untranslatable,
	// or the strategy ladder ran out. It says NOTHING about the definition, and
	// must never be read as "does not satisfy".
	implyUnknown
	// implyInvalidated: a strategy attempt was environmentally aborted, so no
	// negative verdict is valid at all (SPEC §7.2). Distinct from implyUnknown:
	// there the prover finished and had nothing; here the prover's own run is not
	// a basis for any verdict.
	implyInvalidated
)

// classifyProofStatus maps proveOne's status string onto the reported
// classification. It is TOTAL, and the second return says whether the status was
// RECOGNISED.
//
// proveOne returns a string, so an unrecognised value is a live possibility
// whenever prove.go grows a status. Degrading it to implyUnknown is the safe
// direction — implyUnknown is the classification that claims nothing about the
// definition — but it must be VISIBLE that the degradation happened, which is
// what the bool is for. Silently folding an unknown status into "did not prove"
// would be the same erasure this whole mechanism exists to undo, one level up.
func classifyProofStatus(s string) (implyStatus, bool) {
	switch s {
	case "proven":
		return implyProven, true
	case "refuted":
		return implyRefuted, true
	case "unknown":
		return implyUnknown, true
	case "invalidated":
		return implyInvalidated, true
	}
	return implyUnknown, false
}

// implyResult is one admitted candidate's classification against one query
// property, with the evidence for it.
type implyResult struct {
	name     string      // the candidate's name
	status   implyStatus // what was established about it
	method   string      // implyProven: how it was proved
	crossSig string      // the candidate's own signature, when admitted cross-type
	evidence string      // the countermodel, or the prover's reason for having none
	byEval   bool        // the verdict came from EVALUATION, not from the solver
}

// findImpliesMode selects how much of the classification is rendered.
//
// The default is counts because the candidate set grows with the corpus while
// the ANSWER — what proved — does not: on a large store, naming every refuted
// and unsettled candidate buries the hits under the misses. Detail is the
// explicit ask, and it is where the evidence lives.
type findImpliesMode int

const (
	findImpliesSummary  findImpliesMode = iota // per-status counts
	findImpliesDetailed                        // per-candidate names and evidence
)

// implyStatusRows fixes the RENDER ORDER and the wording of the three non-proven
// classifications. Proven candidates are not here: they are named individually in
// both modes, because they are the query's answer and a count of them would be
// strictly less information.
//
// The wording is load-bearing. Two rows say "NO VERDICT" and they say it for
// different reasons that are never summed — the same separation renderAdjudication
// keeps between a settled survivor and an unresolved one, and for the same reason:
// one of these is what a better prover would move, and the other is not.
var implyStatusRows = []struct {
	status implyStatus
	label  string
}{
	{implyRefuted, "REFUTED — proved NOT to satisfy it (a countermodel exists)"},
	{implyUnknown, "NO VERDICT — the prover did not settle it (a limit of this prover, NOT a fact about the definition)"},
	{implyInvalidated, "NO VERDICT — a strategy attempt was environmentally aborted, so no negative verdict is valid (SPEC §7.2)"},
}

// implySatisfiesMarker is what a line reporting a HIT says, and it is a named
// constant because tests read the rendered report to decide whether a candidate
// was returned as satisfying the query. Spelled out at both the writer and the
// reader, a reworded marker would leave those tests matching nothing and passing
// — a check that quietly stops looking is worse than no check.
const implySatisfiesMarker = "← provably satisfies it"

// renderImplyResults writes one query property's classification.
//
// It is a PURE function of the results and the mode so that every outcome can be
// asserted directly, rather than only through a query that happens to produce
// one. That matters most for the statuses a synthetic store cannot cheaply
// provoke: an unreachable rendering branch is indistinguishable from a correct
// one when the only witness is an end-to-end run.
func renderImplyResults(b *strings.Builder, results []implyResult, mode findImpliesMode, complete bool) {
	byStatus := map[implyStatus][]implyResult{}
	for _, r := range results {
		byStatus[r.status] = append(byStatus[r.status], r)
	}
	// The answer first, named in both modes and in the format it has always had.
	for _, r := range byStatus[implyProven] {
		if r.crossSig != "" {
			fmt.Fprintf(b, "      %-18s %s at %s (%s, cross-type: query property re-typed)\n", r.name, implySatisfiesMarker, r.crossSig, r.method)
		} else {
			fmt.Fprintf(b, "      %-18s %s (%s)\n", r.name, implySatisfiesMarker, r.method)
		}
	}
	for _, row := range implyStatusRows {
		rs := byStatus[row.status]
		if len(rs) == 0 {
			continue
		}
		fmt.Fprintf(b, "      %d %s\n", len(rs), row.label)
		// REFUTATIONS ARE NAMED IN BOTH MODES; the residue is counted in summary.
		// The asymmetry is the point of calling a refutation a RESULT: "1 REFUTED"
		// is not actionable, because the finding IS which definition was refuted.
		// An unresolved count is actionable at a glance — it tells you the answer
		// is incomplete without needing to know which candidates were involved.
		// So the volume rule applies to the residue and not to results.
		if mode != findImpliesDetailed && row.status != implyRefuted {
			continue
		}
		for _, r := range rs {
			fmt.Fprintf(b, "          %-18s %s\n", r.name, implyEvidenceLine(r))
		}
	}
	// THE FALLBACK FIRES ONLY WHEN NOTHING WAS CLASSIFIED AT ALL. Its sentence is
	// about the WORLD — no definition satisfies this — and it is supportable only
	// when the signature-compatible set was empty AND the search actually finished.
	// A budget that aborted the scan leaves the same empty result, but the sentence
	// would then be a claim about the corpus made from an unfinished search — the
	// exact overclaim this mechanism exists to prevent — so the abort banner speaks
	// for that case instead.
	if len(results) == 0 && complete {
		b.WriteString("      (no definition provably satisfies this — in the signature-compatible, provable set)\n")
	}
}

// implyEvidenceLine renders one candidate's evidence on a single line.
//
// It names the SOURCE of a refutation, because the two are different kinds of
// fact and only one of them involves the solver: a concrete countermodel is an
// EVALUATION of the reference semantics, which is why the pre-solver pass may
// act on it at all. Evidence is collapsed to one line — solver models arrive as
// multi-line s-expressions — so a hundred candidates stay readable as a list.
func implyEvidenceLine(r implyResult) string {
	raw := r.evidence
	// ONLY a solver refutation's evidence is z3's raw response, so only it can
	// carry the telemetry. Evaluation countermodels are this kernel's own
	// rendering of an environment, and a non-refuted candidate's evidence is a
	// prose reason — neither is parsed, let alone cut.
	if r.status == implyRefuted && !r.byEval {
		raw = stripSolverTelemetry(raw)
	}
	ev := strings.Join(strings.Fields(raw), " ")
	sig := ""
	if r.crossSig != "" {
		sig = " at " + r.crossSig
	}
	if r.status == implyRefuted {
		src := "solver"
		if r.byEval {
			src = "by evaluation"
		}
		if ev == "" {
			return fmt.Sprintf("refuted (%s)%s", src, sig)
		}
		return fmt.Sprintf("countermodel (%s): %s%s", src, ev, sig)
	}
	if ev == "" {
		return "(no reason recorded)" + sig
	}
	return ev + sig
}

// solverTelemetryKeys names the `(get-info :...)` responses that follow every
// solver answer. They are ATTEMPT-VALIDITY TELEMETRY — how much work the run
// spent and why it stopped — and they bind nothing, so under the words
// "countermodel (solver):" they are not merely noise: `(:reason-unknown "")`
// reads as the prover declining to give a reason, on a line that has just
// reported a refutation.
//
// THE AUTHORITY IS `runZ3Budget` IN prove.go, which appends the get-info
// commands; this list is a duplicate of that call site's vocabulary, and a
// duplicate is correct exactly once. It is guarded rather than trusted:
// TestSolverTelemetryKeysMatchProveGo derives the set from that call site and
// fails if the two ever drift, so a third telemetry line cannot be added
// without this list following it.
var solverTelemetryKeys = []string{"rlimit", "reason-unknown"}

// stripSolverTelemetry removes the telemetry forms trailing a solver
// refutation's evidence, leaving the countermodel.
//
// IT IS CONSERVATIVE IN ONE DIRECTION ONLY, and that asymmetry is the whole
// design: a countermodel that still carries telemetry is untidy, while a
// countermodel with a binding silently removed is WRONG and the reader cannot
// tell — the line still says "countermodel (solver)" and still looks complete.
// So every uncertainty returns the input UNCHANGED. Concretely it cuts a form
// only when all four hold:
//
//	the whole text tokenizes as balanced s-expressions (strings and |symbols|
//	  opaque, so a paren inside a reason-unknown payload cannot desynchronise it)
//	the form is at the very END — a telemetry-shaped form in the middle of a
//	  model is not something this function claims to understand
//	its head is `:key` for a key this kernel knows it ASKED for, not any
//	  keyword-headed form
//	something is left afterwards — if every form were telemetry there is no
//	  countermodel here and the shape is not the one modelled
func stripSolverTelemetry(detail string) string {
	forms, ok := smtTopLevelForms(detail)
	if !ok {
		return detail
	}
	cut := len(forms)
	for cut > 0 && isSolverTelemetryForm(detail[forms[cut-1].start:forms[cut-1].end]) {
		cut--
	}
	if cut == len(forms) || cut == 0 {
		return detail
	}
	return strings.TrimRight(detail[:forms[cut-1].end], " \t\r\n")
}

// smtSpan is one top-level form's extent in the response text. Spans rather
// than substrings so the caller can cut the ORIGINAL bytes: re-serializing
// parsed forms would rewrite the model's own formatting, which is a change this
// function has no business making.
type smtSpan struct{ start, end int }

// smtTopLevelForms splits an SMT-LIB response into its top-level forms, or
// reports that it could not. `ok == false` means the text did not tokenize —
// unbalanced parens, an unterminated string or quoted symbol — and the caller
// must then leave it alone rather than guess where a form ends.
func smtTopLevelForms(s string) ([]smtSpan, bool) {
	var forms []smtSpan
	depth, start := 0, -1
	flush := func(end int) {
		if depth == 0 && start >= 0 {
			forms = append(forms, smtSpan{start, end})
			start = -1
		}
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ';': // a comment runs to end of line
			flush(i)
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			flush(i)
			i++
		case c == '"' || c == '|':
			// OPAQUE. An SMT string may contain parentheses — z3 puts them in
			// reason-unknown payloads — so a tokenizer that did not skip these
			// would mis-nest and cut in the wrong place.
			if start < 0 {
				start = i
			}
			i++
			closed := false
			for i < len(s) {
				if s[i] == c {
					if c == '"' && i+1 < len(s) && s[i+1] == '"' { // "" escapes a quote
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			flush(i)
		case c == '(':
			flush(i)
			if depth == 0 {
				start = i
			}
			depth++
			i++
		case c == ')':
			depth--
			if depth < 0 {
				return nil, false
			}
			i++
			flush(i)
		default:
			if start < 0 {
				start = i
			}
			i++
		}
	}
	if depth != 0 {
		return nil, false
	}
	flush(len(s))
	return forms, true
}

// isSolverTelemetryForm reports whether one top-level form is a response to a
// get-info command this kernel issued. It matches on the KEY, never on the
// payload: a filter keyed on the empty `(:reason-unknown "")` seen in one run
// would leave a populated one through, and the populated one is the case that
// most looks like part of a model.
func isSolverTelemetryForm(f string) bool {
	if len(f) < 2 || f[0] != '(' || f[len(f)-1] != ')' {
		return false
	}
	i := 1
	for i < len(f) && (f[i] == ' ' || f[i] == '\t' || f[i] == '\r' || f[i] == '\n') {
		i++
	}
	if i >= len(f) || f[i] != ':' {
		return false
	}
	i++
	j := i
	for j < len(f) && !strings.ContainsRune(" \t\r\n()", rune(f[j])) {
		j++
	}
	for _, k := range solverTelemetryKeys {
		if f[i:j] == k {
			return true
		}
	}
	return false
}

func apiFindImplies(st *Store, src string, mode findImpliesMode) (string, error) {
	return apiFindImpliesOpts(st, src, mode, 0, nil)
}

// apiFindImpliesOpts is find --implies with two operational controls that change
// NEITHER a verdict nor which candidates are DETERMINISTICALLY reachable:
//
//   - progress (non-nil): a writer receiving a live, carriage-return-updated line
//     per candidate, so a slow search reports movement instead of appearing to
//     hang. It never touches the returned report — the CLI directs it to a
//     terminal stderr only, so a piped or captured run is unchanged.
//   - budget (>0): a WALL-CLOCK budget on the search, enforced at candidate
//     boundaries and on the solver — the two places the cost actually lives. The
//     scan stops before starting a new candidate (or building its solver context)
//     once the deadline passes; those unreached candidates are NO VERDICT (search
//     aborted). The dominant cost, a single z3 proof, is capped by
//     setSearchWallDeadline, and the concrete-probe sampling loop checks the
//     deadline too. What is NOT interrupted mid-operation is a candidate's finite
//     preprocessing (type-check, context build, lemma load) once it has begun — so
//     the bound is "roughly the budget", overrunning by at most one admitted
//     candidate's preprocessing, not a hard sub-second ceiling. Every over-budget
//     cut is an ENVIRONMENTAL ABORT (NO VERDICT), never a verdict — a budget can
//     never turn a proof into a false negative — and `find` records nothing to the
//     store, so nothing about identity or reproducibility depends on wall-clock.
//     budget == 0 leaves the solver cap at the host safety net, so the unbounded
//     default that conformance and every scripted run take is byte-identical to
//     before this option existed.
//
//     SCOPE: the budget bounds the SEARCH — candidate scanning and proving. It
//     does NOT bound the initial store-index read (st.Names()): that is a
//     store-layer operation shared by every command, fast and local for the fs
//     store the CLI uses, and uncancellable from here for a remote backend just as
//     it is for verify/prove/ls. The deadline starts before it so its time counts,
//     but a backend that STALLS is an infrastructure fault this flag does not
//     claim to interrupt.
func apiFindImpliesOpts(st *Store, src string, mode findImpliesMode, budget time.Duration, progress io.Writer) (string, error) {
	if err := z3Available(); err != nil {
		return "", err
	}
	forms, err := parseForms(src)
	if err != nil {
		return "", err
	}
	if len(forms) != 1 || forms[0].K != "list" || len(forms[0].Kids) == 0 || !forms[0].Kids[0].isSym("defn") {
		return "", fmt.Errorf("a proof-implication query must be a single (defn ...) whose properties are the query")
	}
	qf, err := ensureQueryBody(st, forms[0])
	if err != nil {
		return "", err
	}
	qd, qm, err := elabFunc(st, qf)
	if err != nil {
		return "", err
	}
	if err := checkDef(st, qd); err != nil {
		return "", fmt.Errorf("spec query does not typecheck: %w", err)
	}
	if len(qd.Props) == 0 {
		return "", fmt.Errorf("spec query %q has no properties", qm.Name)
	}
	qsig := tyBytes(qd.Ty)

	// START THE BUDGET BEFORE READING THE NAME INDEX: on a cloud-backed store
	// st.Names() itself hits the backend, and the wall-clock ceiling must cover it.
	// Also caps the in-flight proof (setSearchWallDeadline) and short-circuits z3
	// launches once elapsed (execZ3) — a single slow attempt would otherwise
	// overrun --timeout; a cap hit is an environmental abort (NO VERDICT), never a
	// verdict, and `find` stores nothing, so this is identity-neutral.
	var deadline time.Time
	if budget > 0 {
		deadline = time.Now().Add(budget)
		setSearchWallDeadline(deadline)
		defer clearSearchWallDeadline()
	}

	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// examined is a RUNNING count of candidate checks, shown as-is. There is no
	// up-front total: computing one means a GetDef over every name, which on a
	// cloud-backed store is itself expensive and, if bounded by the budget, yields
	// a partial number that would misreport as the corpus size. A running count
	// needs no denominator and cannot lie about a total it never claimed.
	examined, aborted := 0, false
	// excludedPolyHashes is the SET of polymorphic definitions a MONOMORPHIC query
	// skipped (see the diagnostic below), keyed by content HASH so aliases of one
	// object count once, and accumulated across ALL query properties: whether a
	// candidate is skipped can depend on the property (a self-free law typechecks
	// against a poly candidate; a self-referencing one does not), so counting only
	// the first property would make the diagnostic depend on property order.
	// anyProven records whether the query succeeded
	// anywhere, so the diagnostic fires only when the query came up empty and the
	// round trip it warns about is real.
	excludedPolyHashes := map[string]bool{}
	anyProven := false

	var b strings.Builder
	fmt.Fprintf(&b, "spec query %q — which definitions PROVABLY satisfy it (proof-implication, not shape match):\n", qm.Name)
	for pqi := range qd.Props {
		fmt.Fprintf(&b, "\n  · %s\n", propNameOf(qm, pqi))
		var results []implyResult
		for _, k := range keys {
			// Checked BEFORE the fetch: st.GetDef is a remote read on a cloud store,
			// so once the deadline passes the scan must stop rather than fetch every
			// remaining entry. A scan cut short here is genuinely INCOMPLETE — we did
			// not finish looking — so aborted is set and the report says so rather
			// than claiming "nothing satisfies". A candidate-less store with a budget
			// large enough to finish never reaches this and falls through normally.
			if budget > 0 && time.Now().After(deadline) {
				aborted = true
				break
			}
			h := names[k]
			d, err := st.GetDef(h)
			if err != nil || d.K != "func" {
				continue
			}
			examined++
			reportImpliesProgress(progress, examined, len(qd.Props), pqi, k)
			// Exact signature: the query property is portable (self + de Bruijn)
			// and is appended verbatim. Otherwise admit the candidate only if its
			// signature is equal up to primitive leaves, and re-type the query
			// property to it — binders and body-embedded types alike.
			qp, crossSig := qd.Props[pqi], ""
			if !bytes.Equal(tyBytes(d.Ty), qsig) {
				sub, ok := crossTypeCompatible(qd.Ty, d.Ty)
				if !ok {
					// finding #3: a monomorphic query silently skips a polymorphic
					// definition whose shape SUBSUMES it (reverse : forall a. (List a)
					// is never reached by a (List Str) query). Record it so an empty
					// result can say WHY and how to fix it; the set dedups across props.
					if qd.TyVars == 0 && d.TyVars > 0 && typeSubsumes(d.Ty, qd.Ty, map[int]*Ty{}) {
						excludedPolyHashes[h] = true
					}
					continue
				}
				qp = Prop{Binders: crossTypeRetypeBinders(qp.Binders, sub), Body: *crossTypeRetypeBody(&qp.Body, sub)}
				crossSig = printTy(st, d.Ty, nil)
			}
			m, err := st.GetMeta(h)
			if err != nil {
				continue
			}
			aug := *d
			aug.Props = append(append([]Prop{}, d.Props...), qp)
			// REQUIRED GATE, UNCONDITIONAL, and the unconditional part is the
			// load-bearing half. The substitution rewrites the primitive leaves of
			// the query's SIGNATURE and nothing else, so a re-typed property can
			// still be ill-typed against the candidate — a body calling a
			// monomorphic Int-typed definition carries no type argument for the
			// substitution to reach, and now sees a Rat argument. That
			// disagreement must reject the candidate rather than reach the prover.
			//
			// It must also run on the EXACT path, which is not obvious. `tyBytes`
			// excludes `Def.TyVars`, so a candidate declaring an unused type
			// parameter still matches exactly, and the appended query's `self`
			// nodes then carry the wrong number of type arguments. Gating only
			// the retyped path leaves that augmentation ill-typed on the way to
			// `proveOne`, which can inline it and report a FALSE PROOF.
			//
			// Such a candidate is dropped, and that is correct rather than a
			// regression: it does not typecheck. Before this rung there was no
			// gate at all, so the case reached the prover unchecked — widening
			// the search is what made the hole visible, not what created it.
			if err := checkDef(st, &aug); err != nil {
				// finding #3, the primitive-typed twin of the count above: a mono
				// query over a PRIMITIVE (Int->Int) passes crossTypeCompatible against
				// a polymorphic (a->a) — both generalize to var->var — but the appended
				// self then carries no type argument the candidate needs, so checkDef
				// rejects it here rather than at admission. Same mismatch, same fix, so
				// count it the same way.
				if qd.TyVars == 0 && d.TyVars > 0 && typeSubsumes(d.Ty, qd.Ty, map[int]*Ty{}) {
					excludedPolyHashes[h] = true
				}
				continue
			}
			// #80/#156. A goal with a concrete countermodel cannot be proven, so
			// the solver has nothing to contribute to it — and the countermodel
			// SETTLES the candidate rather than setting it aside. It is recorded
			// as REFUTED, with the environment that falsified it, because
			// evaluation is the reference semantics: this is not a candidate the
			// search failed to reach a verdict on, it is one it reached a negative
			// verdict on without needing z3.
			//
			// The returned SATISFYING set is therefore unchanged by this branch,
			// which is what makes the latency argument sound: it can only divert
			// goals that are FALSE.
			//
			// It must run BEFORE newSmtCtx, not merely before proveOne: building
			// the context is itself work, and the point is to reach the solver
			// layer as rarely as possible.
			//
			// This is NOT a timeout and NOT a cap. Every goal that survives it
			// still gets the full unmodified proof search, so no verdict is
			// weakened and no coverage is traded away. Unlike a budget, what it
			// leaves behind is a RESULT rather than an unexamined remainder —
			// which is why the report can name it instead of merely counting what
			// it did not get to.
			if probe := concreteProbe(st, h, &qp); probe.outcome == ceFalsified {
				results = append(results, implyResult{
					name: k, status: implyRefuted, crossSig: crossSig,
					evidence: probe.witness(st), byEval: true,
				})
				continue
			}
			// Mid-candidate budget check: the probe above may have consumed the
			// remaining time. Stop BEFORE building the solver context and loading the
			// lemma library (both uncapped work) for a proof that would be capped to
			// nothing anyway. This candidate was examined (counted, shown in progress),
			// so record it as NO VERDICT (environmentally aborted) rather than dropping
			// it silently — the banner then speaks only for the candidates AFTER it.
			if budget > 0 && time.Now().After(deadline) {
				results = append(results, implyResult{
					name: k, status: implyInvalidated, crossSig: crossSig,
					evidence: "the --timeout elapsed before this candidate's proof began",
				})
				aborted = true
				break
			}
			pi := len(d.Props)
			c := newSmtCtx(st, &aug, h)
			// -1: the goal here is a SYNTHETIC query property appended past the
			// definition's own props, not one the author hinted — its hints (keyed
			// by real prop index) must not leak into a discovery query.
			loadLemmaLibrary(c, st, &aug, h, m, -1)
			o := c.proveOne(&aug, h, m, &aug.Props[pi], pi)
			status, known := classifyProofStatus(o.status)
			detail := o.detail
			if !known {
				// Do not let an unrecognised status disappear into the residue it
				// is being degraded to. It is reported as a no-verdict — the
				// classification that claims nothing — but the reason names the
				// status, so the degradation is legible rather than inferred.
				detail = fmt.Sprintf("unrecognised prover status %q: %s", o.status, o.detail)
			}
			if status == implyProven {
				anyProven = true
			}
			results = append(results, implyResult{
				name: k, status: status, method: o.method,
				crossSig: crossSig, evidence: detail,
			})
		}
		// No post-loop deadline re-check: `aborted` is set ONLY where work was left
		// UNREACHED — the pre-candidate break (a new candidate not started) and the
		// mid-candidate break (the solver context not built). A candidate whose proof
		// the cap cut short is REACHED, not unreached, and is reported honestly as
		// NO VERDICT (invalidated) in the results — so the banner would be wrong for
		// it, and a bare time check here cannot tell that case from a final candidate
		// that completed a nanosecond before the deadline.
		renderImplyResults(&b, results, mode, !aborted)
		if aborted {
			break
		}
	}
	clearImpliesProgress(progress)
	if aborted {
		writeImpliesAbort(&b, budget, examined)
	}
	// finding #3: when a MONOMORPHIC query proved nothing anywhere, say WHY the
	// obvious candidates were missed. A polymorphic definition whose shape subsumes
	// the query is never admitted — the query fixes a type it ranges over — so the
	// report reads as "the corpus has nothing" when the real issue is the query's
	// shape. Only on a complete, empty search: an abort or a hit speaks for itself.
	if !aborted && !anyProven && len(excludedPolyHashes) > 0 {
		fmt.Fprintf(&b, "\n  %d polymorphic definition(s) share this shape but could not be lined up: proof-implication\n", len(excludedPolyHashes))
		b.WriteString("  matches types, and THIS query is monomorphic while they range over one or more type\n")
		b.WriteString("  parameters. Restate the query polymorphically — declare the SAME type parameters they\n")
		b.WriteString("  have and pass them explicitly in the property (e.g. `[a]` in the signature and\n")
		b.WriteString("  `(wanted [a] xs)` in the law) — to reach them.\n")
	}
	return b.String(), nil
}

// reportImpliesProgress writes one carriage-return-updated line so a long search
// shows movement. Best-effort: progress is nil unless the CLI is on a terminal,
// and a write error is ignored — progress must never fail the search it reports.
func reportImpliesProgress(w io.Writer, examined, nprops, pqi int, name string) {
	if w == nil {
		return
	}
	prop := ""
	if nprops > 1 {
		prop = fmt.Sprintf(" · property %d/%d", pqi+1, nprops)
	}
	fmt.Fprintf(w, "\r  proving %d%s  %-28s", examined, prop, truncateForProgress(name, 28))
}

// truncateForProgress keeps the live line from jittering as candidate names change
// length. Cosmetic only; it never affects a name used for matching or display. n
// is a RUNE budget: it slices []rune, never bytes, so a multibyte codepoint is
// never split into invalid UTF-8 on the terminal stream.
func truncateForProgress(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// clearImpliesProgress erases the in-place line so it does not collide with the
// report that follows (both share the terminal, report on stdout, line on stderr).
func clearImpliesProgress(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "\r%*s\r", 64, "")
}

// writeImpliesAbort states IN THE REPORT that a wall-clock budget cut the search
// short, so "we ran out of time" cannot be read as "the corpus has nothing" or
// "these are refuted". The unreached candidates got NO VERDICT because they were
// never examined — a fact about the budget, not about them.
func writeImpliesAbort(b *strings.Builder, budget time.Duration, examined int) {
	fmt.Fprintf(b, "\n  SEARCH INCOMPLETE — the --timeout of %s elapsed after %d candidate checks.\n", budget, examined)
	b.WriteString("  The candidates not yet reached are NO VERDICT (search aborted) — never refuted\n")
	b.WriteString("  and never absent, only unexamined. Re-run without --timeout (or with a larger\n")
	b.WriteString("  one) for the complete, deterministic answer.\n")
}

// apiFindEquiv is spec-query by BODY-EQUIVALENCE (the e-graph rung): find every
// definition that is the SAME FUNCTION as this one up to the rewrite rules
// (currently commutativity) — a different implementation that normalizes to the
// same canonical form. Matched by eHash (signature + e-normalized body), which
// is a discovery key ONLY: the matched definitions keep their distinct
// identities; the e-graph draws an equivalence edge, it never merges objects.
func apiFindEquiv(st *Store, name string) (string, error) {
	qh, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	qd, err := st.GetDef(qh)
	if err != nil {
		return "", err
	}
	if qd.K != "func" {
		return "", fmt.Errorf("%q is not a function definition", name)
	}
	target := eHash(st, qd)

	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var matches []string
	seen := map[string]bool{}
	for _, k := range keys {
		h := names[k]
		if h == qh {
			continue
		}
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || seen[h] {
			continue
		}
		if eHash(st, d) == target {
			seen[h] = true
			m, _ := st.GetMeta(h)
			g := "asserted"
			if m != nil {
				g = guaranteeString(m.Guarantee)
			}
			matches = append(matches, fmt.Sprintf("      %-18s #%s  (%s)", k, shortHash(h), g))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "definitions equivalent to %s (#%s) — same function up to the rewrite rules, distinct identities:\n  eHash %s\n", name, shortHash(qh), shortHash(target))
	if len(matches) == 0 {
		b.WriteString("  (no other definition normalizes to the same form)\n")
	} else {
		b.WriteString("\n" + strings.Join(matches, "\n") + "\n")
	}
	return b.String(), nil
}

// apiEval renders a value STRUCTURALLY — a Str as its SCons/SNil tower. This is a
// programmatic contract: the differential-test oracle (evalDenotation) and the MCP
// eval tool parse this text, so it must not change. The CLI `oath eval` renders a
// Str as text instead, via cmdEval + printValueEval; both share evalExpr.
func apiEval(st *Store, src string) (string, error) {
	v, tyStr, err := evalExpr(st, src)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s : %s", printValue(st, v), tyStr), nil
}

// evalDisplay is the CLI rendering of `oath eval`: like apiEval, but a Str value
// reads as text ("cat") rather than its codepoint tower. It is kept distinct from
// apiEval, whose structural output is a parsed contract (the differential oracle
// and the MCP tool).
func evalDisplay(st *Store, src string) (string, error) {
	v, tyStr, err := evalExpr(st, src)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s : %s", printValueEval(st, v), tyStr), nil
}

// evalExpr elaborates, checks and evaluates a single expression, returning the
// value and its rendered type. The value is left unrendered so the CLI can render
// a Str as text while apiEval renders it structurally.
func evalExpr(st *Store, src string) (Value, string, error) {
	forms, err := parseForms(src)
	if err != nil {
		return Value{}, "", err
	}
	if len(forms) != 1 {
		return Value{}, "", fmt.Errorf("eval expects exactly one expression")
	}
	e := &elab{st: st}
	term, err := e.elabTerm(forms[0])
	if err != nil {
		return Value{}, "", err
	}
	// `eval` builds a bare Term rather than a Def, so it needs admission of its
	// own — the same budget, applied to the same kind of structure.
	//
	// NO SYNTHETIC TYPE. An earlier version wrapped the term in
	// Def{Ty: tInt(), Body: term}, and that tInt() counted as a node, making
	// eval's effective limit 65,535 against a profile documented as exactly
	// 65,536. A budget stated as an exact boundary has to be exact at the
	// boundary, or the number in the documentation is not the number enforced.
	if err := admitTerm(term); err != nil {
		return Value{}, "", err
	}
	// The explicit machine (#149), not the recursive checker. NOTE the narrow
	// scope: this makes the CHECKING step stack-safe, and eval still reaches
	// elabTerm and printValue, which recurse. `oath eval` is not yet stack-safe
	// end to end — only its checker call is migrated.
	c := &checkerMachine{st: st}
	ty, err := c.run(checkerStep{mode: modeSynth, term: term})
	if err != nil {
		return Value{}, "", err
	}
	ev := &evaluator{st: st, fuel: propFuel}
	v, err := ev.eval(nil, "", term)
	if err != nil {
		return Value{}, "", err
	}
	return v, printTy(st, ty, nil), nil
}

func apiVerify(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	reports, err := verifyDef(st, h)
	if err != nil {
		return "", err
	}
	if len(reports) == 0 {
		return fmt.Sprintf("%s has no properties; guarantee remains: asserted", name), nil
	}
	return renderVerifyReports(reports), nil
}

// renderVerifyReports formats property verdicts identically for the CLI and for
// conformance fixtures.
func renderVerifyReports(reports []PropReport) string {
	var b strings.Builder
	for _, r := range reports {
		fmt.Fprintf(&b, "%s prop %-24s %s\n", r.Marker(), r.Name, r.Headline())
		if label, text, ok := r.Detail(); ok {
			fmt.Fprintf(&b, "    %s: %s\n", label, text)
		}
	}
	return b.String()
}

func apiLog(st *Store, filter string) string {
	entries := st.ReadLog()
	if len(entries) == 0 {
		return "journal is empty"
	}
	var b strings.Builder
	for _, e := range entries {
		if filter != "" && e.Name != filter {
			continue
		}
		mark := "✓"
		detail := e.Guarantee
		if isTotal(e.Termination) {
			detail += " · total"
		}
		switch e.Status {
		case "rejected":
			mark = "✗"
			detail = e.Error
		case "falsified":
			mark = "✗"
		case "blocked":
			mark = "⛔"
		}
		h := ""
		if e.Hash != "" {
			h = "#" + shortHash(e.Hash)
		}
		if e.Prev != "" {
			// A cross-check references two independent objects, not a repoint;
			// "vs" is the honest relation, "was" would imply a rename.
			rel := " (was #"
			if e.Kind == "cross" {
				rel = " (vs #"
			}
			h += rel + shortHash(e.Prev) + ")"
		}
		fmt.Fprintf(&b, "%-4d %s  %-20s %s %-10s %-16s %s  %s\n",
			e.Seq, e.Time, e.Author, mark, e.Status, e.Name, h, detail)
	}
	return b.String()
}

// signatureNeighbours lists definitions whose type signature is compatible with a
// query definition's, up to the same generalization the property matcher uses.
// They are not matches — they are where to look next when a spec query comes back
// empty, which is otherwise indistinguishable from an empty registry.
func signatureNeighbours(st *Store, qd *Def, excludeHash string) []string {
	out, _ := signatureNeighboursN(st, qd, excludeHash)
	return out
}

// signatureNeighboursN also returns how many definitions MATCHED, which is not
// len(rows): the rows are capped, and the last one may be the "and N more"
// notice rather than a definition. A caller rendering a count must use this,
// or a truncated list reports its own length as the population — which is the
// silent cap again, one layer up.
func signatureNeighboursN(st *Store, qd *Def, excludeHash string) ([]string, int) {
	if qd == nil || qd.Ty == nil {
		return nil, 0
	}
	want := debugTy(&generalizeTypes([]Ty{*qd.Ty})[0])
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	matched := 0
	for _, k := range keys {
		h := names[k]
		if h == excludeHash {
			continue
		}
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || d.Ty == nil || len(d.Props) == 0 {
			continue
		}
		if debugTy(&generalizeTypes([]Ty{*d.Ty})[0]) != want {
			continue
		}
		m, _ := st.GetMeta(h)
		matched++
		if len(out) < neighbourCap {
			out = append(out, fmt.Sprintf("%-18s %s", k, guaranteeString(m.Guarantee)))
		}
	}
	// SAY WHAT WAS DROPPED. This list used to stop at the cap and print nothing
	// about the rest, so a caller reading eight names could not tell a complete
	// answer from a truncated one — and the definition they wanted might be the
	// ninth. Silent truncation reads as "that is all there is", which is the one
	// thing an empty-or-short discovery result must never be allowed to imply.
	if matched > len(out) {
		out = append(out, fmt.Sprintf("... and %d more at this signature (this list is capped at %d)",
			matched-len(out), neighbourCap))
	}
	return out, matched
}

// neighbourCap bounds how many signature-compatible names are PRINTED. The
// count of matches is not bounded, so the caller is always told how many were
// withheld.
const neighbourCap = 8

// querySignature renders the sought function's type, generalized the same way
// the property matcher generalizes, so a recorded demand signal reads as a
// coverage request rather than as one caller's exact phrasing.
func querySignature(st *Store, qd *Def) string {
	if qd == nil || qd.Ty == nil {
		return "?"
	}
	// Rendered with datatype NAMES rather than hashes: a demand record has to be
	// readable as a coverage request ("something (List a) -> (List a)"), not as
	// an opaque fingerprint. Type names are corpus vocabulary, not caller prose,
	// so this adds legibility without adding anything the caller authored.
	g := generalizeTypes([]Ty{*qd.Ty})[0]
	return printTy(st, &g, nil)
}

// nameRevision returns the transition a publication of `name` would replace: the
// hash the name currently points at (or noParent) and its per-name revision — the
// number of accepted publications it has already had.
//
// Both halves are DERIVED, so a client and the registry compute the same values
// from the same journal without a shared counter to drift. The revision is what
// makes the parent check survive ABA: a hash can return to an earlier value, a
// count cannot.
//
// A name that does not resolve is treated as never published (noParent, 0). That
// is sound here specifically because names are only ever REPOINTED, never deleted
// — so unresolvable and never-published are the same state. If deletion is ever
// added, this must change: a deleted-then-recreated name whose revision reset to
// zero would make every historical envelope for it replayable.
func nameRevision(st *Store, name string) (string, int) {
	rev := 0
	// Folded, not filtered: a legacy no-op is undetectable from one entry (see
	// nameTransitions), so a per-entry test counts every re-publication as a state
	// change and inflates the revision.
	for _, t := range nameTransitions(st.ReadLog(), name) {
		if t.Transition == transitionApplied {
			rev++
		}
	}
	h, ok := st.Resolve(name)
	if !ok || rev == 0 {
		return noParent, 0
	}
	return h, rev
}

// nameTransition classifies what a successful publication did to the binding.
// `prev` is the hash the name pointed at before (empty only for a first
// publication), `h` the hash it points at now.
func nameTransition(prev, h string) string {
	if prev == h {
		return transitionUnchanged
	}
	return transitionApplied
}
