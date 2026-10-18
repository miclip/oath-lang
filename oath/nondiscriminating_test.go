package main

import (
	"fmt"
	"sort"
	"testing"
)

// MEASUREMENT: which properties never distinguish ANY mutant of their own
// subject.
//
// A guarded property can fail to be evidence in two opposite ways. The vacuity
// ratchet catches the first — no case reaches the conclusion. This measures the
// second: every case reaches it and the property is true regardless, so nothing
// a mutation does to the body can refute it. `gh-line-ok` carried two of those,
// written `(if (gh-line-ok line) (== (gh-field 0 line) (gh-schema)) true)` —
// `(A and B) -> A`, true by logic — and they were found by hand, not by any
// instrument.
//
// ALL properties are evaluated per mutant, deliberately. `mutantKiller` returns
// the FIRST falsifier and stops, which is right for reporting one reason and
// wrong for this question: a property later in declaration order would never be
// credited, and "never kills" would be measuring property ORDER.
//
// NEVER-KILLS IS NOT WORTHLESS. Mutation reach is a fact about the GENERATOR,
// not about the specification — `hex-nibble` is proven for all inputs and scores
// among the worst in the corpus because generation draws Int from [-20,20] while
// its guards sit at 48, 97 and 65. So the signal is never-kills AND NOT PROVEN,
// the same split that made the vacuity measurement readable.
// WHAT A LISTING HERE MEANS, because reading it as a defect list is the mistake
// its own author made first. Three of these were investigated and none was a
// hole:
//
//	record-under/clean-values-are-well-formed  REDUNDANT. Its sibling
//	  `clean-values-reach-the-record` takes all 182 of that definition's 220
//	  kills, so this one restates a claim already witnessed.
//	no-field-can-inject (all four)             A PURE WITNESS. Its body
//	  arranges calls to a sanitiser that returns true for every arrangement, so
//	  there is no logic in it to break — deleting two of its three checks, and
//	  then a whole dispatch branch, left every property passing. The safety it
//	  describes lives in `record-under`, which scores 182/220.
//
// So an entry is a PROMPT to read the property, not a verdict about it. The
// reasons seen so far: redundant with a sibling, a witness over an
// already-witnessed mechanism, a base case no mutant's path touches — and
// genuine tautologies, which is what `gh-line-ok` carried until they were
// replaced.
//
// NEVER-KILLS IS NOT WORTHLESS either: reach is a fact about the GENERATOR, not
// the specification. `hex-nibble` is proven for all inputs and among the worst
// scorers in the corpus. That is why 119 of the 148 found are not listed — they
// are proven, and a proof covers what generation cannot reach.
var nonDiscriminating = map[string]bool{
	"04241aba6e93#4": true, // webhook/never-leaks-a-body  (176 mutants)
	"11dc5883ba4a#2": true, // gh-record/never-empty  (201 mutants)
	"16ba49acd70c#4": true, // gh-field/past-the-end-is-empty  (3 mutants)
	"16d57ea2081f#0": true, // str-nub/nothing-in-nothing-out  (4 mutants)
	"16d57ea2081f#1": true, // str-nub/it-never-grows  (4 mutants)
	"3a8dba7ca427#4": true, // gh-line-ok/a-blank-line-is-refused  (9 mutants)
	"3b04f218be82#0": true, // gh-counts/an-empty-log-changes-nothing  (9 mutants)
	"7bf00db3bddb#0": true, // gh-refusal/the-headline-count-matches-the-listing  (141 mutants)
	"82a31da3b637#0": true, // gh-dedup/nothing-in-nothing-out  (8 mutants)
	"82a31da3b637#1": true, // gh-dedup/it-never-grows  (8 mutants)
	"8a059a2f7b56#0": true, // set-inter/empty-left  (1 mutants)
	"99ae46cb0869#1": true, // gh-webhook/never-leaks-a-body  (208 mutants)
	"9ac9fa7c0e1a#2": true, // gh-summary/one-line-per-distinct-deduplicated-group  (74 mutants)
	"b15aeb2bf3c3#1": true, // json-scoped-string/value-has-no-quote  (30 mutants)
	"b15aeb2bf3c3#2": true, // json-scoped-string/value-has-no-tab  (30 mutants)
	"bafdf7773694#0": true, // record-under/clean-values-are-well-formed  (220 mutants)
	"c2b36fe59543#1": true, // set-union/union-has-left  (1 mutants)
	"d497b8473178#1": true, // hmac-kat-rfc4231-2/arguments-are-not-symmetric  (288 mutants)
	"d497b8473178#2": true, // hmac-kat-rfc4231-2/length-mismatch-is-not-equal  (288 mutants)
	"e102d9df757d#1": true, // gh-report-main/the-answer-is-always-one-of-three  (89 mutants)
	"e84c7faa721c#0": true, // json-string-value/no-quote  (9 mutants)
	"ea6b2442b4cb#1": true, // gh-group-labels/nothing-in-nothing-out  (2 mutants)
	"eddf3a14cdda#1": true, // gh-refused-lines/a-clean-log-lists-nothing  (11 mutants)
	"ef4ac723d36c#2": true, // secret-tail/empty-maps-to-empty  (10 mutants)
	"f397905be1c1#0": true, // no-field-can-inject/tab-cannot-inject  (147 mutants)
	"f397905be1c1#1": true, // no-field-can-inject/newline-cannot-inject  (147 mutants)
	"f397905be1c1#2": true, // no-field-can-inject/carriage-return-cannot-inject  (147 mutants)
	"f397905be1c1#3": true, // no-field-can-inject/no-control-byte-can-inject  (147 mutants)
	"f55335261c55#1": true, // excluded-witness/reaches-excluded-op  (3 mutants)
}

func TestNonDiscriminatingRatchet(t *testing.T) {
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}

	type row struct {
		key        string // hash#propIndex — the only alias-independent identity
		name, prop string // for reading; both move when an alias is added
		proven     bool
		mutants    int
	}
	var never []row
	defs, props, withMutants := 0, 0, 0

	names := st.Names()
	keys := make([]string, 0, len(names))
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	seen := map[string]bool{}

	for _, name := range keys {
		h := names[name]
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			continue
		}
		mutants := genMutants(st, d)
		if len(mutants) == 0 {
			continue
		}
		defs++
		withMutants += len(mutants)
		// SHORT-CIRCUIT PER PROPERTY, not per mutant. The question is a boolean
		// for each property — "did it ever refute anything?" — so once one has
		// killed, evaluating it against the remaining mutants cannot change the
		// answer. Only the never-killers pay the full price, which is the right
		// place for the cost to land.
		killed := make([]bool, len(d.Props))
		outstanding := len(d.Props)
		for _, mu := range mutants {
			if outstanding == 0 {
				break
			}
			// CACHED FIRST, or `self` resolves to nothing and every property
			// comes back INDETERMINATE rather than falsified. Omitting this
			// reported that all 634 properties killed nothing — a 100% result,
			// which is the shape of a broken instrument and not of a finding.
			// Mutants live in the cache only; they are never admitted.
			st.CacheDef(mu.hash, mu.def)
			base := mutantSeed(mu.hash)
			for pi := range mu.def.Props {
				if pi >= len(killed) || killed[pi] {
					continue
				}
				rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi),
					base, pi, mutantCases, mutantFuel)
				if rep.Falsified() {
					killed[pi] = true
					outstanding--
				}
			}
		}
		fmt.Printf("  %-24s %4d mutants  %2d props  %2d never killed\n",
			name, len(mutants), len(d.Props), outstanding)
		for pi := range d.Props {
			props++
			if !killed[pi] {
				// THE CANONICAL NAME, not `name`. The traversal is sorted and
				// deduplicated by hash, so `name` is whichever live alias sorts
				// first — and adding a lexicographically earlier alias would
				// report every entry here as both gone and added while no
				// property changed behaviour. `metaPropName` reads the object's
				// canonical PropNames, so the two halves of the key agree.
				// KEYED BY HASH AND PROPERTY INDEX. Every NAME here moves:
				// `name` is whichever live alias sorts first, and `m.Name` is
				// whichever alias published most recently, since StoreObject
				// makes the incoming one canonical and demotes the rest. Keying
				// on either churns the baseline when an alias is added and no
				// property changed behaviour. The hash does not move for an
				// unchanged object — and when the object DOES change, churn is
				// correct: different bytes deserve a fresh look.
				never = append(never, row{fmt.Sprintf("%s#%d", h[:12], pi),
					m.Name, metaPropName(m, pi), provenContains(m, pi), len(mutants)})
			}
		}
	}

	// THE INSTRUMENT'S OWN CONTROL. "Nothing kills anything" is what a
	// misconfigured harness reports, so a measurement that cannot tell that from
	// a corpus-wide finding is not reporting either. If no property anywhere
	// refuted a single mutant, the harness is wrong and the numbers below mean
	// nothing.
	if len(never) == props {
		t.Fatalf("every one of %d properties killed no mutant across %d mutants — "+
			"that is a broken harness, not a result", props, withMutants)
	}

	sort.Slice(never, func(i, j int) bool { return never[i].key < never[j].key })
	unproven := 0
	for _, r := range never {
		if !r.proven {
			unproven++
		}
	}
	fmt.Printf("\n=== properties that distinguish no mutant of their subject ===\n")
	fmt.Printf("definitions measured : %d  (%d mutants)\n", defs, withMutants)
	fmt.Printf("properties measured  : %d\n", props)
	fmt.Printf("NEVER kill a mutant  : %d\n", len(never))
	fmt.Printf("  of those, NOT proven: %d   <- candidates for carrying no evidence\n", unproven)
	fmt.Printf("  of those, proven    : %d   <- reach is a generator fact; harmless\n\n", len(never)-unproven)
	for _, r := range never {
		status := "TESTED-ONLY"
		if r.proven {
			status = "proven"
		}
		fmt.Printf("  %-11s %-16s %-22s %-34s (%d mutants, none refuted)\n",
			status, r.key, r.name, r.prop, r.mutants)
	}
	fmt.Println()

	var added, gone []string
	live := map[string]bool{}
	for _, r := range never {
		if r.proven {
			continue
		}
		live[r.key] = true
		if !nonDiscriminating[r.key] {
			added = append(added, fmt.Sprintf("%s (%s/%s)", r.key, r.name, r.prop))
		}
	}
	for k := range nonDiscriminating {
		if !live[k] {
			gone = append(gone, k)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)
	if len(added) > 0 {
		t.Errorf("NEW propert%s distinguishing no mutant of its subject:\n  %v\n"+
			"Read it before listing it: it may be redundant with a sibling, a witness over a "+
			"mechanism tested elsewhere, or a tautology like gh-line-ok's. Prove it, give it "+
			"something to refute, or add it here with the reason.",
			map[bool]string{true: "y", false: "ies"}[len(added) == 1], added)
	}
	if len(gone) > 0 {
		t.Errorf("these now distinguish something — remove them from nonDiscriminating so the "+
			"ratchet keeps its grip:\n  %v", gone)
	}
}
