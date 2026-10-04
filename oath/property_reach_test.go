package main

import (
	"fmt"
	"sort"
	"testing"
)

// ownIfPaths maps each `if` node lexically inside one term to a stable path.
//
// The universe is the ifs of the PROPERTY'S OWN body, not every if the cases
// evaluated. A first version recorded both and the data said so at once:
// `fib`'s `base-zero` reported a one-sided if that lives inside `fib`, reached
// through the call. A guard is a property-level construct; a callee's branch is
// not. `ref` is deliberately not followed, which is what draws that line.
//
// The PATH, rather than a bare name, is what the ratchet keys on: two guards in
// one property are two obligations, and keying by property alone lets a second
// one appear without the baseline noticing.
func ownIfPaths(t *Term, path string, out map[*Term]string) {
	if t == nil {
		return
	}
	if t.K == "if" {
		out[t] = path
	}
	for lbl, c := range map[string]*Term{"a": t.A, "b": t.B, "c": t.C} {
		ownIfPaths(c, path+"."+lbl, out)
	}
	for i := range t.Args {
		ownIfPaths(&t.Args[i], fmt.Sprintf("%s.arg%d", path, i), out)
	}
	for i := range t.Arms {
		ownIfPaths(&t.Arms[i], fmt.Sprintf("%s.arm%d", path, i), out)
	}
}

// VACUITY RATCHET. A guarded property is vacuously true whenever its guard
// fails, and `passed 200 cases` says the same thing whether 200 cases tested
// the conclusion or none did. Nothing else in this repository can see it:
// mutation scoring mutates the guard along with the body, and review found the
// original instance by accident (docs/experiments/webhook-friction.md entry 7).
//
// VACUOUS means every case that reached a guard LANDED ON A CONSTANT `true` —
// not that one branch went untaken. `(if G conclusion true)` with G always true
// is one-sided and fully covered; the same shape with G never true is one-sided
// and tests nothing. Only where the case lands separates them.
//
// PROVEN properties are not ratcheted. A proof covers every input, so a guard
// the generator never satisfies costs nothing there — and treating it as a
// defect would repeat the mutation-score error of reporting the GENERATOR'S
// REACH as the specification's strength.
var vacuousTestedOnly = map[string]bool{
	// The four the corpus carried when this ratchet was written. Listed rather
	// than repaired because each needs its generator to reach the guard or the
	// input constructed in the property, which is per-property work and a
	// separate decision — but none of them may multiply quietly meanwhile.
	//
	// `gh-webhook/unsigned-is-401` is the one worth reading twice: it claims the
	// receiver answers 401 to an unsigned delivery, and it tests nothing.
	"gh-line-ok/an-accepted-line-carries-the-tag":   true,
	"gh-line-ok/an-accepted-line-has-five-fields":   true,
	"gh-webhook/unsigned-is-401":                    true,
	"gh-webhook/unusable-secret-refuses-everything": true,
}

func TestPropertyVacuityRatchet(t *testing.T) {
	// THE FILESYSTEM BACKEND EXPLICITLY, not OpenStore, which consults
	// OATH_BACKEND and under `cloud` ignores the path and opens the remote
	// registry — so this ratchet would describe a different corpus while its
	// baseline still compared cleanly. The claim is about the committed store
	// at this path, so the authority is pinned to the claim rather than left to
	// the environment. Same reason as corpus_census_test.go.
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	type finding struct {
		key               string
		substantive, triv int
		proven            bool
	}
	var vacuous []finding
	props, guarded := 0, 0

	seen := map[string]bool{}
	names := st.Names()
	keys := make([]string, 0, len(names))
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)

	for _, name := range keys {
		h := names[name]
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			continue
		}
		// PER-ALIAS property names. Naming is per-alias — structurally identical
		// definitions are one object with several names, each keeping its own
		// vocabulary — so `storedPropName`, which reads the object's canonical
		// list, would pair this name with another's spelling and build a key
		// naming no actual property. Same idiom as corpus_census_test.go.
		propNames := m.PropNames
		if n := name; n != m.Name {
			if an, ok := m.Aliases[n]; ok && an != nil {
				propNames = an.PropNames
			}
		}
		propName := func(pi int) string {
			if pi < len(propNames) && propNames[pi] != "" {
				return propNames[pi]
			}
			return fmt.Sprintf("prop%d", pi)
		}
		base := caseSeedBase(h)
		for pi := range d.Props {
			p := &d.Props[pi]
			props++
			own := map[*Term]string{}
			ownIfPaths(&p.Body, "", own)
			if len(own) == 0 {
				continue
			}
			guarded++
			acc := map[*Term]*branchCount{}
			evaluated := 0
			for c := 0; c < propCases; c++ {
				env, err := genPropCase(st, p, base, pi, c)
				if err != nil {
					continue
				}
				// PER CASE, merged only on success: the accumulator is written
				// as evaluation proceeds, so a case that enters a branch and
				// then exhausts fuel would otherwise contribute coverage the
				// verifier itself discards as indeterminate — and could make a
				// genuinely unreached path look reached.
				one := map[*Term]*branchCount{}
				ev := &evaluator{st: st, fuel: propFuel, branch: one}
				if _, err := ev.eval(env, h, &p.Body); err != nil {
					continue
				}
				evaluated++
				for node, b := range one {
					got := acc[node]
					if got == nil {
						got = &branchCount{}
						acc[node] = got
					}
					got.Substantive += b.Substantive
					got.Trivial += b.Trivial
				}
			}
			if evaluated == 0 {
				// NOT vacuous — INDETERMINATE. No case produced a verdict, so
				// the guarantee is `asserted` and nothing was skipped because
				// nothing ran. Collapsing the two would reintroduce exactly the
				// conflation `PropOutcome` was split three ways to remove.
				continue
			}
			// Over `own`, not `acc`: a guard NO case reaches gets no counter at
			// all, and iterating the counters would miss it entirely — a nested
			// guard behind an outer branch nothing takes is untested in the
			// strongest sense, not absent.
			for node, path := range own {
				b := acc[node]
				if b == nil {
					b = &branchCount{}
				}
				if b.Substantive == 0 {
					vacuous = append(vacuous, finding{
						fmt.Sprintf("%s/%s%s", name, propName(pi), path),
						b.Substantive, b.Trivial, provenContains(m, pi)})
				}
			}
		}
	}

	sort.Slice(vacuous, func(i, j int) bool { return vacuous[i].key < vacuous[j].key })
	fmt.Printf("\n=== property vacuity over the committed corpus ===\n")
	fmt.Printf("properties examined : %d\n", props)
	fmt.Printf("with a guard of own : %d\n", guarded)
	fmt.Printf("VACUOUS guards      : %d\n\n", len(vacuous))
	for _, f := range vacuous {
		status := "TESTED-ONLY"
		if f.proven {
			status = "proven"
		}
		fmt.Printf("  %-11s %-64s (reached 0 of %d cases)\n", status, f.key, f.triv)
	}
	fmt.Println()

	var added, gone []string
	live := map[string]bool{}
	for _, f := range vacuous {
		if f.proven {
			continue
		}
		live[f.key] = true
		if !vacuousTestedOnly[f.key] {
			added = append(added, f.key)
		}
	}
	for k := range vacuousTestedOnly {
		if !live[k] {
			gone = append(gone, k)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)
	if len(added) > 0 {
		t.Errorf("NEW vacuous tested-only guard(s) — no generated case reached the conclusion, so "+
			"`passed %d cases` establishes nothing:\n  %v\n"+
			"Make the generator reach the guard, construct the input in the property "+
			"(webhook-friction.md entry 7), or prove it.", propCases, added)
	}
	if len(gone) > 0 {
		t.Errorf("no longer vacuous — remove from vacuousTestedOnly so the ratchet keeps its grip:\n  %v", gone)
	}
}
