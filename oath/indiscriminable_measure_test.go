package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// MEASUREMENT for #77: can a caller choosing on EVIDENCE tell two artifacts
// apart?
//
// #77's premise is two proven `sort` artifacts, neither exposing stability,
// complexity or allocation, so a caller diverges from another caller not because
// ranking is wrong and not because the corpus lacks an artifact, but because the
// artifacts do not expose enough structure to decide between them. That is a
// claim about THIS corpus and is measurable rather than arguable.
//
// Two levels, because they answer different questions:
//
//	SAME SIGNATURE      a caller searching by type gets more than one candidate.
//	                    Ordinary, and not itself a defect.
//	SAME SIGNATURE AND  nothing in the recorded evidence separates them. This is
//	SAME PROPERTY SET   the state #77 describes.
//
// Keyed by canonical bytes, never by name: `propHash` is what the discovery
// layer matches on, and the type key is built with the same encoder so the two
// halves agree.
// tyKeyOf keys a SIGNATURE, which is the type tree AND the type-parameter
// arity. Hashing the `Ty` alone would group a monomorphic `Int -> Int` with one
// declaring an unused type variable: identical type bytes, different arity, and
// a caller must supply different type arguments.
func tyKeyOf(d *Def) string {
	e := &enc{}
	e.tys([]Ty{*d.Ty})
	// The arity is appended as text, not one byte: `TyVars` is permitted well
	// past 255, and truncating it would merge signatures 256 apart.
	s := sha256.Sum256(append(e.b, []byte(fmt.Sprintf("|tv%d", d.TyVars))...))
	return hex.EncodeToString(s[:])[:12]
}

// A MEASUREMENT, not a gate: four indiscriminable groups is not a defect count.
// Four rotation implementations sharing a spec may be exactly what the corpus
// wants; what #77 asks is whether a caller can CHOOSE, and the answer is a fact
// about the corpus that changes as specs are strengthened. Gated so it costs CI
// nothing: OATH_MEASURE=1 go test -run TestMeasureIndiscriminableArtifacts -v
func TestMeasureIndiscriminableArtifacts(t *testing.T) {
	if os.Getenv("OATH_MEASURE") == "" {
		t.Skip("measurement; set OATH_MEASURE=1")
	}
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}

	type art struct {
		name, hash, tyKey, propKey, bareKey, level string
		props, proven                              int
	}
	var arts []art
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
		// A STORE THAT WILL NOT READ IS A FAILURE, NOT AN ABSENCE. Skipping an
		// unreadable object would shrink the population while the totals below
		// still printed as if they covered the named corpus — a smaller
		// universe wearing an authoritative number, which is the defect this
		// measurement would otherwise be reporting about other people's.
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s (%s) does not load: %v", name, h[:12], err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s (%s) has no readable metadata: %v", name, h[:12], err)
		}
		if d.K != "func" || d.Ty == nil {
			continue // not a signature-bearing artifact; nothing to compare
		}
		// EACH PROPERTY HASH CARRIES ITS PROOF STATUS. Comparing only the
		// aggregate `Guarantee.Level` reported the whole `rot` family as one
		// indiscriminable group — but `rot` has properties [0,1,2] proven and
		// `rot-h3` has [0,1,2,6], so a caller reading the evidence CAN choose.
		// The unit of evidence is a property's verdict, not a definition's.
		// EACH PROPERTY HASH CARRIES ITS OWN VERDICT, all three of them.
		// Comparing only the aggregate `Guarantee.Level` reported the whole
		// `rot` family as one group, though `rot` has [0,1,2] proven and
		// `rot-h3` has [0,1,2,6]. And collapsing every non-proof to one mark
		// would lose REFUTED: `bad-reverse` carries reverse's two property
		// hashes with one tested and one falsified, which is precisely what a
		// caller reads to tell them apart. The unit of evidence is a property's
		// verdict, never a definition's.
		indet := map[string]bool{}
		for _, n := range m.Guarantee.Indeterminate {
			indet[n] = true
		}
		ph := make([]string, 0, len(d.Props))
		bare := make([]string, 0, len(d.Props))
		proven := 0
		for pi := range d.Props {
			mark := "~tested"
			switch {
			// INDETERMINATE IS ITS OWN VERDICT. A property with no verdict is
			// not a tested one; `guaranteeString` shows the difference, so two
			// `asserted` artifacts whose no-verdict subsets differ are not
			// indiscriminable.
			case indet[metaPropName(m, pi)]:
				mark = "?noverdict"
			case provenContains(m, pi):
				mark = "!proven"
				proven++
			// refutedContains, not a name-keyed set of my own: duplicate property
			// names are legal, and that helper already preserves the ambiguity
			// by weighing falsified-name multiplicity against the property
			// count. A set keyed by name would mark every occurrence refuted
			// when one was.
			case refutedContains(m, pi, len(d.Props)):
				mark = "Xrefuted"
			}
			ph = append(ph, propHash(&d.Props[pi])[:12]+mark)
			bare = append(bare, propHash(&d.Props[pi])[:12])
		}
		sort.Strings(ph)
		sort.Strings(bare)
		// THE CASE COUNT IS PART OF THE EVIDENCE. `guaranteeString` shows a
		// caller "tested (200 cases per property)", so two otherwise-identical
		// tested artifacts at different case counts are not indiscriminable.
		arts = append(arts, art{m.Name, h[:12], tyKeyOf(d),
			// THE MUTATION SCORE IS SURFACED EVIDENCE TOO. `specStrengthString`
			// shows it, and in this corpus `rot` reports 20/22 against
			// `rot-h2`'s 9/9 — a caller can choose on that, so omitting it
			// overstated the count this measurement exists to establish.
			strings.Join(ph, ",") + fmt.Sprintf("@%dcases/%dof%d",
				m.Guarantee.Cases, m.MutantsKilled, m.MutantsTotal),
			strings.Join(bare, ","), m.Guarantee.Level, len(d.Props), proven})
	}

	byTy := map[string][]art{}
	for _, a := range arts {
		byTy[a.tyKey] = append(byTy[a.tyKey], a)
	}
	sharedSig, sharedSpec, emptySpec, levelSplit, evidenceSplit := 0, 0, 0, 0, 0
	var collisions, empties [][]art
	for _, group := range byTy {
		if len(group) < 2 {
			continue
		}
		sharedSig++
		// COUNTED FROM THE BARE SPEC, because `propKey` already carries every
		// verdict — so a refutation puts two artifacts in different groups and
		// a split could never be observed from it. Printing a metric that
		// cannot be non-zero, beside a comment naming `bad-reverse` as its
		// case, would be the instrument asserting something it cannot measure.
		byBare := map[string][]art{}
		for _, a := range group {
			byBare[a.bareKey] = append(byBare[a.bareKey], a)
		}
		for _, same := range byBare {
			seenEv := map[string]bool{}
			for _, a := range same {
				seenEv[a.propKey] = true
			}
			if len(same) >= 2 && len(seenEv) > 1 {
				evidenceSplit++
			}
		}

		byProp := map[string][]art{}
		for _, a := range group {
			byProp[a.propKey] = append(byProp[a.propKey], a)
		}
		for _, same := range byProp {
			if len(same) < 2 {
				continue
			}
			// ZERO PROPERTIES IS A DIFFERENT PROBLEM. Two definitions that
			// assert nothing share a spec trivially; a caller cannot choose
			// because there is no evidence, not because the evidence coincides.
			// #77 is about artifacts that each carry a spec and still cannot be
			// told apart, so an empty spec is counted separately.
			if same[0].props == 0 {
				emptySpec++
				empties = append(empties, same)
				continue
			}
			// A DIFFERENT GUARANTEE LEVEL DISCRIMINATES. `bad-reverse` carries
			// reverse's spec and is FALSIFIED — a caller reading evidence sees
			// that immediately, so sharing the property set does not make them
			// indiscriminable.
			// PARTITIONED BY LEVEL, not discarded. Skipping a mixed group would
			// drop the indiscriminable pair inside it: two `proven` artifacts
			// beside one `tested` are still a pair a caller cannot choose
			// between, and under-reporting them is the error this measurement
			// exists to avoid.
			byLevel := map[string][]art{}
			for _, a := range same {
				byLevel[a.level] = append(byLevel[a.level], a)
			}
			if len(byLevel) > 1 {
				levelSplit++
			}
			for _, lv := range byLevel {
				if len(lv) >= 2 {
					sharedSpec++
					collisions = append(collisions, lv)
				}
			}
		}
	}

	fmt.Printf("\n=== can a caller tell two artifacts apart on evidence? ===\n")
	fmt.Printf("artifacts measured            : %d\n", len(arts))
	fmt.Printf("distinct signatures           : %d\n", len(byTy))
	fmt.Printf("signatures with >1 artifact   : %d\n", sharedSig)
	fmt.Printf("  of those, same spec but one is EMPTY   : %d   <- no evidence, not equal evidence\n", emptySpec)
	fmt.Printf("  of those, same bare spec but EVIDENCE differs: %d   <- the ladder discriminating\n", evidenceSplit)
	fmt.Printf("  of those, partitioned by aggregate level     : %d\n", levelSplit)
	fmt.Printf("INDISCRIMINABLE: same signature, same non-empty spec, same level: %d   <- #77's state\n\n", sharedSpec)

	sort.Slice(collisions, func(i, j int) bool { return collisions[i][0].name < collisions[j][0].name })
	for _, c := range collisions {
		ns := make([]string, 0, len(c))
		for _, a := range c {
			ns = append(ns, fmt.Sprintf("%s(%s)", a.name, a.hash))
		}
		fmt.Printf("  %d props, %d proven : %s\n", c[0].props, c[0].proven, strings.Join(ns, "  "))
	}
	fmt.Println()
}
