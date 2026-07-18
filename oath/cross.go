package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// N-version specification (#20). Mutation testing kills spec WEAKNESS but is
// structurally blind to spec MISALIGNMENT: a spec internally tight around the
// WRONG function maxes every axis (sum-of-squares graded as sum-abs proves all
// its props and kills all its mutants). The brief is not an object the kernel
// can read, so no single-spec checker can catch it.
//
// The defense with the right shape is independent redundancy. Two authors,
// disjoint processes, one brief: each writes a reference body AND a property
// set. Cross-binding is mechanical because properties reference `self`, which
// is name-free — running author A's properties with `self` bound to author B's
// body asks "does B's implementation satisfy A's spec?". If the two authors
// converged on the same function, every cross-property passes; if they diverge,
// a cross-property falsifies and its counterexample localizes exactly where.
//
// Detection is free (the deterministic generator, no human). Adjudication —
// WHICH author matches the brief — is the human's remaining job, and the
// counterexample is the evidence they adjudicate on. Honest limit: two authors
// misaligned to the SAME wrong function still pass. Redundancy lowers the
// probability of undetected misalignment the way mutation lowers the
// probability of undetected weakness; neither reaches zero, and intent still
// enters as an axiom from a trusted party. The entry point moves; it is not
// deleted.

// crossDir is the result of running one definition's properties against the
// other's body.
type crossDir struct {
	spec, body string       // display names: whose props, whose body
	reports    []PropReport // one per property of the spec side
}

func (d crossDir) contradictions() []PropReport {
	var out []PropReport
	for _, r := range d.reports {
		if r.Failed || r.Err != "" {
			out = append(out, r)
		}
	}
	return out
}

// crossProp runs one property with the deterministic 200-case generator, seeded
// from the property owner's hash (so the input stream is identical to an
// ordinary `verify` of the owner — a cross-failure and a self-pass then share
// inputs, making the distinguishing counterexample directly comparable), but
// with `self` bound to a DIFFERENT body's hash for evaluation.
func crossProp(st *Store, ownerHash, bodyHash string, p *Prop, name string, pi int) PropReport {
	seedBytes, _ := hex.DecodeString(ownerHash[:16])
	base := binary.BigEndian.Uint64(seedBytes)
	rep := PropReport{Name: name}
	for c := 0; c < propCases; c++ {
		r := &rng{s: base ^ (uint64(pi) << 32) ^ uint64(c)*0xD1B54A32D192ED03}
		size := c % 8
		var env []Value
		var inputs []string
		genFailed := false
		for bi := range p.Binders {
			v, err := genValue(st, &p.Binders[bi], size, r)
			if err != nil {
				rep.Err = err.Error()
				genFailed = true
				break
			}
			env = append(env, v)
			inputs = append(inputs, printValue(st, v))
		}
		if genFailed {
			return rep
		}
		ev := &evaluator{st: st, fuel: propFuel}
		out, err := ev.eval(env, bodyHash, &p.Body)
		if err != nil {
			rep.Failed = true
			rep.Counter = strings.Join(inputs, ", ") + "  (runtime error: " + err.Error() + ")"
			return rep
		}
		if out.K != "bool" || !out.Bool {
			rep.Failed = true
			rep.Counter = strings.Join(inputs, ", ")
			return rep
		}
		rep.Passed++
	}
	return rep
}

// CrossResult is the full outcome of cross-checking two definitions.
type CrossResult struct {
	nameA, nameB string
	hashA, hashB string
	aOnB, bOnA   crossDir
}

// Agree reports whether the two independently-authored definitions are
// observationally equivalent on the deterministic domain: every property of
// each holds against the other's body.
func (r CrossResult) Agree() bool {
	return len(r.aOnB.contradictions()) == 0 && len(r.bOnA.contradictions()) == 0
}

// crossCheck binds each definition's properties to the other's body. It
// requires identical signatures (a cross-check between different signatures is
// meaningless — the generated inputs would not typecheck against the other
// body). It does NOT mutate the store.
func crossCheck(st *Store, nameA, nameB string) (*CrossResult, error) {
	hA, ok := st.Resolve(nameA)
	if !ok {
		return nil, fmt.Errorf("no definition named %q", nameA)
	}
	hB, ok := st.Resolve(nameB)
	if !ok {
		return nil, fmt.Errorf("no definition named %q", nameB)
	}
	if hA == hB {
		return nil, fmt.Errorf("%q and %q are the same object (%s) — cross-checking a definition against itself is vacuous; N-version redundancy needs INDEPENDENT authorship", nameA, nameB, shortHash(hA))
	}
	dA, err := st.GetDef(hA)
	if err != nil {
		return nil, err
	}
	dB, err := st.GetDef(hB)
	if err != nil {
		return nil, err
	}
	if dA.K != "func" || dB.K != "func" {
		return nil, fmt.Errorf("cross requires two function definitions")
	}
	if dA.TyVars != dB.TyVars || !tyEq(dA.Ty, dB.Ty) {
		return nil, fmt.Errorf("signatures differ: %q and %q must have identical types to cross-check (their properties generate inputs for a shared signature)", nameA, nameB)
	}

	res := &CrossResult{nameA: nameA, nameB: nameB, hashA: hA, hashB: hB}
	res.aOnB = crossDir{spec: nameA, body: nameB}
	res.bOnA = crossDir{spec: nameB, body: nameA}
	mA, _ := st.GetMeta(hA)
	mB, _ := st.GetMeta(hB)
	for pi := range dA.Props {
		res.aOnB.reports = append(res.aOnB.reports, crossProp(st, hA, hB, &dA.Props[pi], propName(mA, pi), pi))
	}
	for pi := range dB.Props {
		res.bOnA.reports = append(res.bOnA.reports, crossProp(st, hB, hA, &dB.Props[pi], propName(mB, pi), pi))
	}
	return res, nil
}

func propName(m *Meta, pi int) string {
	if m != nil && pi < len(m.PropNames) {
		return m.PropNames[pi]
	}
	return fmt.Sprintf("prop%d", pi)
}

func renderCross(r *CrossResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cross-check: %s (#%s)  vs  %s (#%s)\n\n",
		r.nameA, shortHash(r.hashA), r.nameB, shortHash(r.hashB))

	renderDir := func(d crossDir) {
		if len(d.reports) == 0 {
			fmt.Fprintf(&b, "  %s's spec on %s's body: no properties — imposes no constraint\n", d.spec, d.body)
			return
		}
		fmt.Fprintf(&b, "  %s's spec on %s's body:\n", d.spec, d.body)
		for _, rp := range d.reports {
			switch {
			case rp.Failed:
				fmt.Fprintf(&b, "    ✗ %-24s FALSIFIED after %d cases\n        counterexample: %s\n", rp.Name, rp.Passed, rp.Counter)
			case rp.Err != "":
				fmt.Fprintf(&b, "    ✗ %-24s ERROR: %s\n", rp.Name, rp.Err)
			default:
				fmt.Fprintf(&b, "    ✓ %-24s passed %d cases\n", rp.Name, rp.Passed)
			}
		}
	}
	renderDir(r.aOnB)
	renderDir(r.bOnA)
	b.WriteString("\n")

	if r.Agree() {
		fmt.Fprintf(&b, "AGREE — every property of each holds against the other's body.\n")
		b.WriteString("Two independent processes converged on the same function over the\n")
		b.WriteString("deterministic domain; no misalignment flagged. (Honest limit: two\n")
		b.WriteString("authors misaligned to the SAME wrong function would also agree.)\n")
		return b.String()
	}

	fmt.Fprintf(&b, "DISAGREE — the two specs describe different functions.\n")
	for _, d := range []crossDir{r.aOnB, r.bOnA} {
		for _, rp := range d.contradictions() {
			fmt.Fprintf(&b, "  %s's `%s` is violated by %s's body.\n", d.spec, rp.Name, d.body)
		}
	}
	b.WriteString("A human adjudicates which body matches the brief; the counterexamples\n")
	b.WriteString("above are the evidence. Detection was mechanical.\n")
	return b.String()
}

// apiCross runs the cross-check and, when record is set, stamps the verdict
// into the journal as provenance (kind=cross) against both objects — a durable,
// tamper-evident record that these two specs were reconciled and how.
func apiCross(st *Store, nameA, nameB string, record bool, author string) (string, error) {
	r, err := crossCheck(st, nameA, nameB)
	if err != nil {
		return "", err
	}
	out := renderCross(r)
	if record {
		status := "falsified"
		if r.Agree() {
			status = "accepted"
		}
		if err := st.AppendLog(&LogEntry{
			Author: author,
			Name:   r.nameA + " ×cross× " + r.nameB,
			Kind:   "cross",
			Status: status,
			Hash:   r.hashA,
			Prev:   r.hashB,
		}); err != nil {
			return out, fmt.Errorf("cross-check ran but journaling failed: %w", err)
		}
		out += fmt.Sprintf("\nrecorded to journal (kind=cross, status=%s).\n", status)
	}
	return out, nil
}
