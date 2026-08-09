package main

import (
	"fmt"
	"math/big"
	"sort"
	"testing"
)

// SPEC §4's DECLARED `Str` ALPHABET IS UNREACHED — the witness for #163.
//
// docs/SPEC.md §4 declares, as normative draw order:
//
//	- `Str`: length `below(size+1)`, then that many draws of `below(7)` into
//	  alphabet `"ab xyz!"` (bytes, in that order).
//
// `Str` stopped being a primitive at #59: it is the inductive datatype
// `(data Str [] (SNil) (SCons Int Str))`, so generation dispatches through
// genValue's `data` arm and every codepoint comes from its `int` arm — the
// boundary table one draw in four, `intIn(-20, 20)` otherwise. Neither kernel
// dispatches §4's Str rule: the Go generator handles
// int/rat/float/bool/record/fun/data and the Rust one the same set, and neither
// special-cases the Str datatype's identity (which is how a kernel COULD honour
// the rule — so this is a divergence from §4, not an impossibility).
//
// THE CLAIM this witnesses:
//
//	the codepoints the deterministic tester emits into a `Str` are DISJOINT
//	from the alphabet SPEC §4 declares for `Str`.
//
// TWO ARMS, BECAUSE THE CLAIM'S UNIVERSE AND A STABLE RATCHET ARE NOT THE SAME
// SET. Conflating them is how a witness ends up measuring neither well:
//
//	ARM A — THE REAL POPULATION. Every `Str` binder of every property of every
//	        live definition in the committed store, drawn at that definition's
//	        OWN seed base and the property's OWN index. This is the universe the
//	        claim quantifies over: the `Str` values verification actually binds.
//	        Its size changes whenever the corpus does, so it carries no
//	        equality-pinned totals — only the zeros, a containment bound, and
//	        the vacuity controls that make a zero mean something.
//	        REPORTED IN TWO WINDOWS, and they are not interchangeable: the REAL
//	        window is `propCases`, exactly what `runProp` runs, and is the only
//	        one that may be described as what verification binds; the EXTENDED
//	        window continues the same seed path further, because a zero over 200
//	        cases is consistent with a rare event and a zero over 2000 is not.
//
//	ARM B — THE PINNED INSTRUMENT. One synthetic single-`Str`-binder property at
//	        a fixed seed base, so the population is FIXED and every figure can be
//	        pinned by equality. It is explicitly a synthetic RNG stream and is
//	        labelled as such wherever its numbers are quoted: it establishes what
//	        the generator's alphabet IS, not what any committed property samples.
//
// Both call `genPropCase` — the function `runProp` binds its cases from — so
// neither reproduces a schedule that could drift from the one the kernel runs.
//
// WHAT THIS DOES NOT CLAIM. It samples; it does not prove that no execution
// anywhere can emit 'a'. It does not say what §4 ought to say instead — that is
// #163's repair, and this file is only its evidence.
//
// WHY IT IS NOT gen_str_reach_test.go's MEASUREMENT. That file asks whether one
// codepoint ('=', 61) the corpus branches on is reachable — #161's question,
// about a gap in what `tested` can mean. This asks whether the SEVEN codepoints
// §4 names are reachable — #163's question, about normative text that neither
// kernel implements. Same cause, different claims.

// specStrAlphabet is SPEC §4's declared `Str` alphabet, "ab xyz!", as bytes in
// the order the rule gives them. Written as character literals so a reader can
// check it against the specification sentence without decoding numbers.
var specStrAlphabet = []int64{'a', 'b', ' ', 'x', 'y', 'z', '!'}

func inSpecStrAlphabet(cp int64) bool {
	for _, a := range specStrAlphabet {
		if a == cp {
			return true
		}
	}
	return false
}

// strSpine identifies the committed `Str` datatype by IDENTITY, not by shape.
// A nil/cons-shaped walker that checks only constructor arities accepts
// `(List Int)` as happily as `Str`, which would turn the promised "a binder
// that stopped being Str fails loudly" control into no control at all.
type strSpine struct {
	hash     string
	snilIdx  int
	sconsIdx int
}

func newStrSpine(st *Store) (strSpine, error) {
	h := strTypeHash(st)
	if h == "" {
		return strSpine{}, fmt.Errorf("Str is not bound in the committed store")
	}
	d, err := st.GetDef(h)
	if err != nil {
		return strSpine{}, fmt.Errorf("reading the Str datatype: %w", err)
	}
	s := strSpine{hash: h, snilIdx: -1, sconsIdx: -1}
	for i, c := range d.Ctors {
		switch len(c) {
		case 0:
			s.snilIdx = i
		case 2:
			s.sconsIdx = i
		}
	}
	if s.snilIdx < 0 || s.sconsIdx < 0 {
		return strSpine{}, fmt.Errorf("Str does not have the expected nil/cons constructor shape: %v", d.Ctors)
	}
	return s, nil
}

// codepoints flattens a `Str` value. Every node must carry the committed Str
// datatype's hash and one of its two constructor indices; ok=false otherwise.
func (s strSpine) codepoints(v Value) ([]int64, bool) {
	var out []int64
	cur := v
	for {
		if cur.K != "data" || cur.Hash != s.hash {
			return nil, false
		}
		switch {
		case cur.Idx == s.snilIdx && len(cur.Fields) == 0:
			return out, true
		case cur.Idx == s.sconsIdx && len(cur.Fields) == 2:
			if cur.Fields[0].K != "int" || cur.Fields[0].Int == nil {
				return nil, false
			}
			out = append(out, cur.Fields[0].Int.Int64())
			cur = cur.Fields[1]
		default:
			return nil, false
		}
	}
}

func (s strSpine) build(cps []int64) Value {
	v := Value{K: "data", Hash: s.hash, Idx: s.snilIdx}
	for i := len(cps) - 1; i >= 0; i-- {
		v = Value{K: "data", Hash: s.hash, Idx: s.sconsIdx, Fields: []Value{
			{K: "int", Int: big.NewInt(cps[i])},
			v,
		}}
	}
	return v
}

// specAlphabetHits reports the codepoints of v that lie in SPEC §4's declared
// alphabet, and ok=false for anything that is not a `Str` spine.
func (s strSpine) specAlphabetHits(v Value) ([]int64, bool) {
	cps, ok := s.codepoints(v)
	if !ok {
		return nil, false
	}
	var hits []int64
	for _, cp := range cps {
		if inSpecStrAlphabet(cp) {
			hits = append(hits, cp)
		}
	}
	return hits, true
}

// strTally accumulates one arm's emissions.
type strTally struct {
	nonEmpty int
	total    int
	seen     map[int64]int
	spec     map[int64]int
	minCp    int64
	maxCp    int64
	haveCp   bool
}

func newStrTally() *strTally {
	return &strTally{seen: map[int64]int{}, spec: map[int64]int{}}
}

func (a *strTally) add(cps []int64) {
	if len(cps) > 0 {
		a.nonEmpty++
	}
	for _, cp := range cps {
		a.total++
		a.seen[cp]++
		if inSpecStrAlphabet(cp) {
			a.spec[cp]++
		}
		if !a.haveCp || cp > a.maxCp {
			a.maxCp = cp
		}
		if !a.haveCp || cp < a.minCp {
			a.minCp = cp
		}
		a.haveCp = true
	}
}

func (a *strTally) distinct() int { return len(a.seen) }

func (a *strTally) report(label string, values int) {
	fmt.Printf("  %s: %d values drawn   non-empty: %d   codepoints emitted: %d   distinct: %d   observed range: [%d, %d]\n",
		label, values, a.nonEmpty, a.total, a.distinct(), a.minCp, a.maxCp)
	fmt.Printf("    occurrences of each codepoint SPEC §4 declares:")
	for _, cp := range specStrAlphabet {
		fmt.Printf("  %q=%d", rune(cp), a.spec[cp])
	}
	fmt.Printf("\n    VERDICT: the generated alphabet and SPEC §4's declared alphabet are %s\n",
		map[bool]string{true: "DISJOINT", false: "NOT disjoint"}[len(a.spec) == 0])
}

// checkDeclaredCodepointsUnreached is the assertion both arms share. It does
// NOT demand that the generator stay unable to emit "ab xyz!"; it demands that
// changing that fact be LOUD, so this file and docs/experiments/issue-163.md
// are re-read together rather than one of them silently becoming a lie.
func (a *strTally) checkDeclaredCodepointsUnreached(t *testing.T, arm string, draws int) {
	t.Helper()
	for _, cp := range specStrAlphabet {
		if a.spec[cp] != 0 {
			t.Errorf("RECORDED MEASUREMENT IS STALE (%s) — codepoint %d (%q) from SPEC §4's declared "+
				"Str alphabet now occurs %d times over %d draws, where this file records 0. Re-read "+
				"docs/experiments/issue-163.md and #163 before trusting either.",
				arm, cp, rune(cp), a.spec[cp], draws)
		}
	}
}

// strBinderSite is one (property, binder) position in the committed corpus at
// which the tester generates a `Str`.
type strBinderSite struct {
	defName string
	hash    string
	pi      int
	binders []int // indices of the Str binders of that property
}

// liveStrBinderSites derives ARM A's population from the STORE rather than from
// a list of definitions someone remembered: every live name, every property,
// every binder whose type is the committed `Str`.
func liveStrBinderSites(t *testing.T, st *Store, spine strSpine) []strBinderSite {
	t.Helper()
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sites []strBinderSite
	seenHash := map[string]bool{}
	for _, name := range keys {
		h := names[name]
		// Names alias — several names can resolve to one object. Generation is
		// a fact about the OBJECT (the seed base is its hash), so measuring an
		// aliased object twice would double-count one population.
		if seenHash[h] {
			continue
		}
		seenHash[h] = true
		// FATAL, not skipped. Arm A claims to cover every live definition; a
		// name that silently drops out would shrink the population while the
		// verdict still read as complete — the exact failure this witness
		// exists to expose one layer up.
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("live name %q resolves to %s, which could not be read: %v — "+
				"ARM A cannot claim to cover every live definition", name, shortHash(h), err)
		}
		for pi := range d.Props {
			var bs []int
			for bi := range d.Props[pi].Binders {
				if isStrTy(spine.hash, &d.Props[pi].Binders[bi]) {
					bs = append(bs, bi)
				}
			}
			if len(bs) > 0 {
				sites = append(sites, strBinderSite{defName: name, hash: h, pi: pi, binders: bs})
			}
		}
	}
	return sites
}

func TestSpecStrAlphabetIsUnreachedByTheGenerator(t *testing.T) {
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	spine, err := newStrSpine(st)
	if err != nil {
		t.Fatalf("%v; the measurement has no subject", err)
	}

	// --- CONTROLS ON THE SCAN ------------------------------------------------
	// A "disjoint" verdict from a scan that can never report a hit is not
	// evidence, and neither is one from a scan that accepts any nil/cons value.
	// These run before the measurement so a broken scan fails first.
	for _, cp := range specStrAlphabet {
		if !inSpecStrAlphabet(cp) {
			t.Fatalf("predicate POSITIVE control failed: %d is in \"ab xyz!\" and was not recognised", cp)
		}
	}
	if inSpecStrAlphabet(61) {
		t.Fatal("predicate NEGATIVE control failed: 61 ('=') is not in \"ab xyz!\"")
	}
	if hits, ok := spine.specAlphabetHits(spine.build([]int64{97, 61, 98})); !ok ||
		len(hits) != 2 || hits[0] != 97 || hits[1] != 98 {
		t.Fatalf("scan POSITIVE control failed: \"a=b\" gave hits %v (ok=%v), want [97 98]", hits, ok)
	}
	if hits, ok := spine.specAlphabetHits(spine.build([]int64{1, 2, 3})); !ok || len(hits) != 0 {
		t.Fatalf("scan NEGATIVE control failed: codepoints [1 2 3] gave hits %v (ok=%v), want none", hits, ok)
	}
	if _, ok := spine.specAlphabetHits(Value{K: "int", Int: big.NewInt(97)}); ok {
		t.Fatal("scan TYPE control failed: a non-Str value was scored instead of rejected")
	}
	// THE IDENTITY CONTROL. `(List Int)` has exactly the nil/cons shape `Str`
	// has. A walker that checks arities alone accepts it, and the "this binder
	// stopped being Str" control it promises is then vacuous.
	if listH, ok := st.Resolve("List"); ok {
		nilH, nilIdx, ok1 := st.FindCtor("Nil")
		consH, consIdx, ok2 := st.FindCtor("Cons")
		if ok1 && ok2 && nilH == listH && consH == listH {
			listVal := Value{K: "data", Hash: listH, Idx: consIdx, Fields: []Value{
				{K: "int", Int: big.NewInt(97)},
				{K: "data", Hash: listH, Idx: nilIdx},
			}}
			if _, ok := spine.specAlphabetHits(listVal); ok {
				t.Fatal("scan IDENTITY control failed: a (List Int) value was scored as a Str")
			}
		} else {
			t.Fatal("scan IDENTITY control could not be set up: List's Nil/Cons constructors did not resolve")
		}
	} else {
		t.Fatal("scan IDENTITY control could not be set up: List is not live in the committed store")
	}

	// --- ARM A: THE REAL POPULATION ------------------------------------------
	sites := liveStrBinderSites(t, st, spine)
	if len(sites) == 0 {
		t.Fatal("no live property in the committed store has a Str binder; ARM A has no population")
	}
	binderPositions := 0
	for _, site := range sites {
		binderPositions += len(site.binders)
	}

	// TWO WINDOWS, REPORTED SEPARATELY, because only one of them is the
	// population verification actually binds. `propCases` is what `runProp`
	// runs; the extended window continues the SAME seed path past it, and a
	// zero over 200 cases is consistent with a rare event where a zero over
	// 2000 is not. Reporting the extended count as "what verification binds"
	// would be the mislabel this whole file is about, one layer up.
	const armAExtCases = 2000
	if armAExtCases <= propCases {
		t.Fatalf("the extended window (%d) must exceed propCases (%d) or it adds nothing",
			armAExtCases, propCases)
	}
	armAReal := newStrTally() // c < propCases — the cases verification runs
	armAExt := newStrTally()  // c < armAExtCases — the same seed path, continued
	armARealValues, armAExtValues := 0, 0
	exhibits := 0

	fmt.Printf("\n=== SPEC §4 Str alphabet \"ab xyz!\" vs the generator ===\n")
	fmt.Printf("ARM A — real population: %d live properties carrying %d (property, Str binder) positions,\n",
		len(sites), binderPositions)
	fmt.Printf("        each drawn at its own definition's seed base and property index.\n")
	fmt.Printf("        REAL window = %d cases (what runProp runs); EXTENDED window = %d cases (same seed path).\n",
		propCases, armAExtCases)

	for _, site := range sites {
		d, err := st.GetDef(site.hash)
		if err != nil {
			t.Fatalf("%s: reading the definition: %v", site.defName, err)
		}
		p := &d.Props[site.pi]
		base := caseSeedBase(site.hash)
		for c := 0; c < armAExtCases; c++ {
			env, err := genPropCase(st, p, base, site.pi, c)
			if err != nil {
				// Generation can legitimately fail for a type the generator
				// cannot build; that case contributes nothing and is not a
				// fact about the alphabet.
				continue
			}
			if len(env) != len(p.Binders) {
				t.Fatalf("%s prop %d case %d: generated %d values for %d binders",
					site.defName, site.pi, c, len(env), len(p.Binders))
			}
			for _, bi := range site.binders {
				cps, ok := spine.codepoints(env[bi])
				if !ok {
					t.Fatalf("%s prop %d case %d binder %d: value is not a Str spine",
						site.defName, site.pi, c, bi)
				}
				armAExt.add(cps)
				armAExtValues++
				if c < propCases {
					armAReal.add(cps)
					armARealValues++
				}
				// THE EXHIBIT: values a committed property actually binds, taken
				// from inside the real window so it is not a counterfactual case.
				if len(cps) > 0 && c < propCases && exhibits < 3 {
					exhibits++
					hits, _ := spine.specAlphabetHits(env[bi])
					fmt.Printf("  EXHIBIT %d: %s prop %d binder %d, case c=%d (size %d) -> Str codepoints %v\n",
						exhibits, site.defName, site.pi, bi, c, c%8, cps)
					fmt.Printf("             codepoints in SPEC §4's alphabet \"ab xyz!\": %v (want none)\n", hits)
				}
			}
		}
	}

	// VACUITY CONTROLS. Arm A's population is corpus-dependent, so its zeros are
	// only evidence once something was actually drawn and emitted.
	if armARealValues == 0 {
		t.Fatal("ARM A drew no values inside the real window; its zeros are not evidence")
	}
	if armAReal.total == 0 {
		t.Fatalf("ARM A emitted no codepoints over %d values in the real window; a zero would be a "+
			"fact about string LENGTH, not about the alphabet", armARealValues)
	}
	if exhibits == 0 {
		t.Fatal("ARM A produced no non-empty Str inside the real window; there is no exhibit")
	}
	armAReal.report(fmt.Sprintf("ARM A (real, %d cases/prop)", propCases), armARealValues)
	armAExt.report(fmt.Sprintf("ARM A (extended, %d cases/prop)", armAExtCases), armAExtValues)
	armAReal.checkDeclaredCodepointsUnreached(t, "ARM A real", armARealValues)
	armAExt.checkDeclaredCodepointsUnreached(t, "ARM A extended", armAExtValues)
	// Containment, not equality: this population changes with the corpus, so an
	// exact range would red the build on any new Str property. Arm B carries the
	// equality pin. The bound still fires on the direction that matters — a
	// widened integer range is what would put "ab xyz!" in reach.
	if armAExt.minCp < -20 || armAExt.maxCp > 20 {
		t.Errorf("RECORDED MEASUREMENT IS STALE (ARM A) — observed codepoints in [%d, %d], outside the "+
			"recorded [-20, 20]. The Str alphabet has moved; re-read docs/experiments/issue-163.md.",
			armAExt.minCp, armAExt.maxCp)
	}

	// --- ARM B: THE PINNED INSTRUMENT ----------------------------------------
	// A synthetic single-Str-binder property at seed base 0. SYNTHETIC IS THE
	// POINT: the population is fixed, so every figure below can be pinned by
	// equality and any change to the generator is loud. It is not, and must not
	// be quoted as, the stream any committed property samples.
	strProp := &Prop{Binders: []Ty{{K: "data", Hash: spine.hash}}}
	const armBDraws = 200000
	armB := newStrTally()
	for c := 0; c < armBDraws; c++ {
		env, err := genPropCase(st, strProp, 0, 0, c)
		if err != nil {
			t.Fatalf("ARM B draw %d: generation failed: %v", c, err)
		}
		cps, ok := spine.codepoints(env[0])
		if !ok {
			t.Fatalf("ARM B draw %d: generated value is not a Str spine", c)
		}
		armB.add(cps)
	}
	fmt.Printf("ARM B — synthetic single-Str-binder property, seed base 0, size schedule c%%8\n")
	armB.report("ARM B", armBDraws)
	fmt.Println()

	if armB.total == 0 {
		t.Fatalf("ARM B emitted no codepoints over %d draws; a zero would be a fact about string "+
			"LENGTH, not about the alphabet", armBDraws)
	}
	armB.checkDeclaredCodepointsUnreached(t, "ARM B", armBDraws)
	// Pinned by EQUALITY, not as a bound: a NARROWED range leaves every count
	// above at zero, so a one-sided check would pass while the range recorded
	// here and in docs/experiments/issue-163.md had silently stopped being true.
	if armB.minCp != -20 || armB.maxCp != 20 || armB.distinct() != 41 {
		t.Errorf("RECORDED MEASUREMENT IS STALE (ARM B) — observed codepoints in [%d, %d] with %d "+
			"distinct, where this file records [-20, 20] with 41. The Str alphabet has moved; re-read "+
			"the recorded figures in docs/experiments/issue-163.md before trusting them.",
			armB.minCp, armB.maxCp, armB.distinct())
	}
	// The emission totals pin the LENGTH schedule, a dimension the range cannot
	// see: both are quoted in docs/experiments/issue-163.md, and a quoted number
	// with nothing asserting it is the drift this ratchet exists to make loud.
	if armB.nonEmpty != 87186 || armB.total != 149449 {
		t.Errorf("RECORDED MEASUREMENT IS STALE (ARM B) — %d/%d non-empty strings and %d codepoints "+
			"emitted, where this file records 87186/%d and 149449. The generator's size or length "+
			"schedule has moved; re-read docs/experiments/issue-163.md.",
			armB.nonEmpty, armBDraws, armB.total, armBDraws)
	}
}
