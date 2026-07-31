package main

import (
	"bytes"
	"fmt"
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
				rep.Props = append(rep.Props, propJSON{
					Name: r.Name, Passed: r.Passed, Failed: r.Failed,
					Counterexample: r.Counter, Error: r.Err,
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
				if r.Failed {
					fmt.Fprintf(&b, "    prop %-24s FALSIFIED after %d cases\n", r.Name, r.Passed)
					fmt.Fprintf(&b, "      counterexample: %s\n", r.Counterexample)
				} else if r.Error != "" {
					fmt.Fprintf(&b, "    prop %-24s ERROR: %s\n", r.Name, r.Error)
				} else {
					fmt.Fprintf(&b, "    prop %-24s passed %d cases\n", r.Name, r.Passed)
				}
			}
		}
	}
	return b.String()
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
		fmt.Fprintf(&b, "%-16s #%s  %-5s %s\n", k, shortHash(h), kind, g)
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
		name   string
		hash   string
		proven bool
	}
	var queries []qprop
	qidx := map[string]int{}
	for i := range qd.Props {
		ph := propHashGeneral(&qd.Props[i])
		if _, seen := qidx[ph]; seen {
			continue
		}
		qidx[ph] = len(queries)
		queries = append(queries, qprop{propNameOf(qm, i), ph, provenContains(qm, i)})
	}

	type match struct {
		def      string
		propName string
		proven   bool
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
				matches[j] = append(matches[j], match{k, propNameOf(m, i), provenContains(m, i)})
			}
		}
	}

	mark := func(proven bool) string {
		if proven {
			return "proven"
		}
		return "tested"
	}
	var b strings.Builder
	b.WriteString(header)
	for _, q := range queries {
		j := qidx[q.hash]
		fmt.Fprintf(&b, "\n  · %s [%s]  #%s\n", q.name, mark(q.proven)+" here", shortHash(q.hash))
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
			if near := signatureNeighbours(st, qd, excludeHash); len(near) > 0 {
				fmt.Fprintf(&b, "      %d definition(s) have a COMPATIBLE SIGNATURE — the law may be stated differently, or your\n", len(near))
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
		for _, m := range matches[j] {
			flag := ""
			if m.proven && q.proven {
				flag = "  ← proven on both: interchangeable for this law"
			} else if m.proven {
				flag = "  ← a proven implementation of this spec"
			}
			fmt.Fprintf(&b, "      %-18s (%s as %q)%s\n", m.def, mark(m.proven), m.propName, flag)
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

// apiFindImplies is spec-query by PROOF-IMPLICATION: find every definition that
// PROVABLY satisfies a fresh spec — not just the ones whose stated law matches
// by shape. Because a property is `self`-referential and de Bruijn, it is
// portable: for each definition of the same signature, we append the query
// property and try to prove it (reusing the full prover, including that
// definition's own proven properties as lemmas and its body). If it proves, the
// definition satisfies the spec — even when the spec is written differently from
// any law that definition happens to have stated. This catches semantic matches
// that the content-hash surface misses (e.g. commutativity written `(== (self b
// a) (self a b))` still proves against `+`).
func apiFindImplies(st *Store, src string) (string, error) {
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
	qd, qm, err := elabFunc(st, forms[0])
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

	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "spec query %q — which definitions PROVABLY satisfy it (proof-implication, not shape match):\n", qm.Name)
	for pqi := range qd.Props {
		fmt.Fprintf(&b, "\n  · %s\n", propNameOf(qm, pqi))
		found := false
		for _, k := range keys {
			h := names[k]
			d, err := st.GetDef(h)
			if err != nil || d.K != "func" || !bytes.Equal(tyBytes(d.Ty), qsig) {
				continue
			}
			m, err := st.GetMeta(h)
			if err != nil {
				continue
			}
			// The query property is portable (self + de Bruijn): append it to
			// this same-signature definition and prove it about that def.
			aug := *d
			aug.Props = append(append([]Prop{}, d.Props...), qd.Props[pqi])
			if err := checkDef(st, &aug); err != nil {
				continue
			}
			pi := len(d.Props)
			c := newSmtCtx(st, &aug, h)
			// -1: the goal here is a SYNTHETIC query property appended past the
			// definition's own props, not one the author hinted — its hints (keyed
			// by real prop index) must not leak into a discovery query.
			loadLemmaLibrary(c, st, &aug, h, m, -1)
			if o := c.proveOne(&aug, h, m, &aug.Props[pi], pi); o.status == "proven" {
				fmt.Fprintf(&b, "      %-18s ← provably satisfies it (%s)\n", k, o.method)
				found = true
			}
		}
		if !found {
			b.WriteString("      (no definition provably satisfies this — in the same-signature, provable set)\n")
		}
	}
	return b.String(), nil
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

func apiEval(st *Store, src string) (string, error) {
	forms, err := parseForms(src)
	if err != nil {
		return "", err
	}
	if len(forms) != 1 {
		return "", fmt.Errorf("eval expects exactly one expression")
	}
	e := &elab{st: st}
	term, err := e.elabTerm(forms[0])
	if err != nil {
		return "", err
	}
	c := &checker{st: st}
	ty, err := c.synth(nil, term)
	if err != nil {
		return "", err
	}
	ev := &evaluator{st: st, fuel: propFuel}
	v, err := ev.eval(nil, "", term)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s : %s", printValue(st, v), printTy(st, ty, nil)), nil
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
		if r.Failed {
			fmt.Fprintf(&b, "✗ prop %-24s FALSIFIED after %d cases\n    counterexample: %s\n", r.Name, r.Passed, r.Counter)
		} else if r.Err != "" {
			fmt.Fprintf(&b, "✗ prop %-24s ERROR: %s\n", r.Name, r.Err)
		} else {
			fmt.Fprintf(&b, "✓ prop %-24s passed %d cases\n", r.Name, r.Passed)
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
	if qd == nil || qd.Ty == nil {
		return nil
	}
	want := debugTy(&generalizeTypes([]Ty{*qd.Ty})[0])
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
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
		out = append(out, fmt.Sprintf("%-18s %s", k, guaranteeString(m.Guarantee)))
		if len(out) >= 8 {
			break
		}
	}
	return out
}

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
