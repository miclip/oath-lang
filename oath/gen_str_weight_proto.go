package main

// PROTOTYPE — #162 Step 1. THROWAWAY, and deliberately so.
//
// #162 proposes weighting generated `Str` codepoints toward the literals in a
// definition's canonical dependency closure. That is a NORMATIVE change: SPEC
// §4 pins generation including draw order, `oathrs/src/gen.rs` implements it
// independently, and every `tested` verdict in the corpus re-derives if it
// lands. None of that is done here. This file exists to answer ONE question
// ahead of any of it — falsifier 1 on the issue:
//
//	does weighted generation DETECT the two `config.oath` defects inside the
//	real 200-case schedule, where uniform widening measurably did not?
//
// So nothing here is wired into the default path. `strWeights` reaches the
// generator only through an explicitly-passed argument, and a nil argument —
// which is every caller outside the Step 1 measurement — leaves the existing
// stream byte-identical. No SPEC text, no Rust, and no fixture moves with it.
//
// WHAT IS PRE-REGISTERED, recorded before the measurement was run so the
// distribution cannot be fitted to the answer it produces:
//
//   - POPULATION: the unique, numerically sorted Int literals, plus the
//     codepoints of Str literals, taken from the owner definition and from
//     every definition named by `sortedDepHashes` — the same canonical
//     dependency set the hash already rests on.
//   - WEIGHT: one codepoint in four is drawn from that set; the other three
//     go to the existing generic Int arm unchanged. 1/4 is the kernel's
//     established boundary-arm weight (Int, Float), reused rather than tuned.
//   - APPLICABILITY: the weighting applies to every Str binder of the owner's
//     properties when the closure carries literals, and is switched off
//     entirely when it does not — an empty set preserves today's schedule
//     rather than degenerating into it.

import (
	"fmt"
	"math/big"
	"sort"
)

// strWeights is the literal alphabet a weighted run draws from, together with
// the identity of the Str ADT it applies to. It is never constructed empty:
// strLiteralClosure returns nil in that case, so "no literals in the closure"
// and "weighting off" are one state rather than two that can disagree.
type strWeights struct {
	strHash string  // the Str datatype's hash; the structural site of the weighting
	lits    []int64 // unique, numerically sorted, non-empty

	// binderOnly restricts the weighting to binders whose type IS Str,
	// leaving Str values nested inside another datatype — a `(List Str)`
	// element, say — on the unweighted arm. See forBinder.
	binderOnly bool
}

// forBinder decides whether this weighting applies while generating one
// binder. Nil-receiver-safe, so the production path (`w == nil`) needs no
// special case at the call site.
//
// WHICH READING IS RIGHT IS NOT SETTLED HERE, AND THE DIFFERENCE IS MEASURED.
// "Apply the weighting to Str binders" can mean every Str VALUE generated, or
// only a binder whose declared type is Str. They are not equivalent and not
// merely a matter of coverage: `finds-head` binds `(rest (List Str))` BEFORE
// `(key Str)`, so weighting the list's elements consumes draws and moves the
// key's whole schedule. A result that held under one reading and not the other
// would be a fact about draw alignment wearing the costume of a design result.
//
// The default is the wider reading — every Str value — because #162 is about
// Str CODEPOINTS reaching the literals a definition branches on, and a
// delimiter matters exactly as much inside a list element as at the top level.
// The narrow reading is measured beside it rather than argued away.
func (w *strWeights) forBinder(t *Ty) *strWeights {
	if w == nil || !w.binderOnly {
		return w
	}
	if isStrTy(w.strHash, t) {
		return w
	}
	return nil
}

// narrowedToBinders returns the same population under the narrow reading.
func (w *strWeights) narrowedToBinders() *strWeights {
	if w == nil {
		return nil
	}
	n := *w
	n.binderOnly = true
	return &n
}

// genStrCodepoint is the weighted arm. The 1/4 draw happens FIRST and
// unconditionally, so the literal and generic branches consume a decidable
// prefix of the stream rather than an alphabet-dependent one.
func genStrCodepoint(r *rng) Value {
	if r.below(4) == 0 {
		lits := r.strW.lits
		return Value{K: "int", Int: big.NewInt(lits[r.below(len(lits))])}
	}
	return genInt(r)
}

// TWO READINGS OF "THE CANONICAL DEPENDENCY CLOSURE", AND BOTH ARE MEASURED.
//
// #162 names `sortedDepHashes` as the source. That function returns a
// definition's DIRECT references — the set `collectDeps` walks out of one
// definition's own terms and types — so "the set sortedDepHashes names" and
// "the transitive closure" are two different populations, and the issue's
// wording does not separate them. They differ materially here rather than
// pedantically: `config-missing` directly references `config-has-key`, but the
// delimiter 61 originates in `config-key`, one hop further out, so the direct
// population is small and dense while the transitive one is large and dilute.
// The delimiter's weight per codepoint moves by an order of magnitude between
// them.
//
// So the choice is not made here. `strLiteralClosure` is the pre-registered
// reading, taken literally, and it is what the detection criterion is judged
// on; `strLiteralClosureTransitive` is measured beside it and reported, so the
// sensitivity of the result to the ambiguity is visible rather than resolved by
// whichever reading was implemented first.
//
// The universe is derived from the CLAIM either way — "the literals this
// definition's behaviour can branch on" — and its structural owner is the
// canonical dependency set rather than a hand-listed set of callees:
// `sortedDepHashes` is already what identity rests on, so a literal set built
// from it cannot drift from the definition's hash. #162's own counterexample is
// why the owner alone will not do: the literal 61 lives in `config-key`'s body,
// while the false properties belong to definitions that only CALL it.
func strLiteralClosure(st *Store, ownerHash string) (*strWeights, error) {
	return strLiteralPopulation(st, ownerHash, false)
}

// strLiteralClosureTransitive is the wider reading: every definition
// transitively reachable from the owner, not only those it names directly.
func strLiteralClosureTransitive(st *Store, ownerHash string) (*strWeights, error) {
	return strLiteralPopulation(st, ownerHash, true)
}

// strLiteralPopulation builds the literal set for one owner, or returns nil
// when it is empty — so "no literals in the closure" and "weighting off" stay
// one state rather than two that can disagree.
//
// AN UNREADABLE DEPENDENCY IS FATAL, not skipped. The first draft skipped it,
// on the reasoning that a missing literal can only cost detection — and that
// is false in the direction that matters. Dropping a dependency SHRINKS the
// alphabet, and a smaller alphabet raises every surviving literal's share of
// the 1/4 arm: lose fifteen of `finds-head`'s seventeen literals and the
// delimiter's weight goes from 1/68 to 1/8. A partial read can therefore
// manufacture detection the complete population would not produce. For a
// measurement that is a false positive, so an incomplete population must
// invalidate the run rather than quietly become a different experiment.
func strLiteralPopulation(st *Store, ownerHash string, transitive bool) (*strWeights, error) {
	strHash := strTypeHash(st)
	if strHash == "" {
		return nil, nil
	}
	owner, err := st.GetDef(ownerHash)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	collectDefLiterals(owner, seen)

	// The store's reference graph is acyclic by construction — a definition
	// cannot name a hash that does not exist yet — but the visited set is kept
	// anyway: it bounds the walk on a malformed store and makes the traversal
	// cost linear rather than exponential in a diamond.
	visited := map[string]bool{ownerHash: true}
	queue := sortedDepHashes(owner)
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if visited[h] {
			continue
		}
		visited[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			return nil, fmt.Errorf("literal population for %s: dependency %s is unreadable (%w); "+
				"an incomplete population is a DIFFERENT distribution, not a smaller one",
				shortHash(ownerHash), shortHash(h), err)
		}
		collectDefLiterals(d, seen)
		if transitive {
			queue = append(queue, sortedDepHashes(d)...)
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	lits := make([]int64, 0, len(seen))
	for v := range seen {
		lits = append(lits, v)
	}
	sort.Slice(lits, func(i, j int) bool { return lits[i] < lits[j] })
	return &strWeights{strHash: strHash, lits: lits}, nil
}

// collectDefLiterals accumulates one definition's Int literals and Str-literal
// codepoints. Both sources are collected because either encoding may carry a
// delimiter: `(str-split 61 line)` writes it as an Int, `(str-append k "=")`
// writes it inside a Str. Which of the two survives canonicalization is an
// encoding detail, and a population that depended on the answer would be
// measuring the encoding.
//
// The whole definition is walked — body AND property bodies. A property's own
// literals are part of what the definition is about, and excluding them would
// need an argument this prototype does not have.
func collectDefLiterals(d *Def, out map[int64]bool) {
	if d == nil {
		return
	}
	var walk func(t *Term)
	walk = func(t *Term) {
		if t == nil {
			return
		}
		switch t.K {
		case "int":
			// Int is ℤ, so a literal need not fit an int64. One that does not
			// is not a plausible codepoint either, and IsInt64 is the exact
			// test for both.
			if t.Int != nil && t.Int.IsInt64() {
				out[t.Int.Int64()] = true
			}
		case "str":
			for _, cp := range t.Str {
				out[int64(cp)] = true
			}
		}
		walk(t.A)
		walk(t.B)
		walk(t.C)
		for i := range t.Args {
			walk(&t.Args[i])
		}
		for i := range t.Arms {
			walk(&t.Arms[i])
		}
	}
	walk(d.Body)
	for pi := range d.Props {
		walk(&d.Props[pi].Body)
	}
}
