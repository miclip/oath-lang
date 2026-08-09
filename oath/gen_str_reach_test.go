package main

import (
	"fmt"
	"math/big"
	"sort"
	"testing"
)

// STR GENERATOR REACH — the measurement half of #161, taken BEFORE either
// property was repaired. The figures below are a historical record of the
// generator at that moment, and the ratchets keep them honest.
//
// #161 found two committed properties in examples/config.oath that WERE FALSE
// while recorded `tested` at 200 cases with `falsified: null`. BOTH HAVE SINCE
// BEEN REPAIRED — each is now guarded by `(== (config-key key) key)` and PROVEN
// over unbounded ints; `config_guard_test.go` carries the firing and non-firing
// controls. Repairing them left the question underneath untouched, which is why
// this measurement was taken first and is kept afterwards:
//
//	can the deterministic tester's Str generator produce the one codepoint
//	these definitions branch on at all?
//
// THAT QUESTION IS STILL OPEN AND IS NOW #162. The answer measured here is no,
// and the repair above did not change it: guarding two properties removed two
// false claims, and left every future Str definition exposed to the same gap.
// Later measurements, recorded on #162, showed that widening the generator's
// uniform Int arm makes '=' REACHABLE without making it FINDABLE. TWO DISTINCT
// PROBES, and they must not be quoted as one — the second is the range actually
// proposed, and it detects WORSE than the first because 256 values dilute each
// codepoint further than 148 do:
//
//	                          hit rate, key binder   detection in 200 cases
//	  [-20,127]  (throwaway    0.36% / 0.34%          51.4% / 49.4%
//	              probe below)
//	  [-128,127] (the widening 0.14% / 0.30%          24.4% / 45.2%
//	              actually run)
//
// Under [-128,127] BOTH properties took zero hits inside the first 200 cases —
// the schedule verification really runs — and neither was falsified. So "reach
// is not detection" is not a coin flip at the range proposed; it is worse than
// one, and the coin-flip figures belong to the throwaway probe alone.
//
// THE CLAIM, and its universe, derived from the claim rather than from the
// generator's decomposition:
//
//	POPULATION  the values the deterministic tester actually binds to the
//	            relevant Str binder of each affected property. It is DERIVED,
//	            not reproduced: this calls `caseSeedBase` and `genPropCase` —
//	            the same two functions `runProp` binds its cases from — so the
//	            population is the tester's by construction. A copied seed/size
//	            schedule would be correct exactly once and would then silently
//	            describe a generator this repo no longer runs, which is the
//	            failure this measurement exists to expose one layer down.
//
// PRE-REGISTERED DEFINITIONS (fixed before the first run, so the threshold is
// not chosen after seeing the number):
//
//	"structurally significant"  a tester-generated value bound to the Str
//	                            binder named `key` (finds-head) or `k`
//	                            (complete-config-reports-nothing) that CONTAINS
//	                            codepoint 61, '='. 61 is not an arbitrary pick:
//	                            it is the delimiter config-key splits on, so it
//	                            is the one byte that discriminates the two
//	                            properties' truth.
//	"reasonable reach"          at least 1% of 10,000 cases — i.e. >= 100 —
//	                            produce such a value.
//
// 10,000 extends the tester's seed path (c = 0..9999) past the 200 cases
// verification actually runs. That is deliberate: 200 cases finding nothing is
// consistent with a rare event, and only a longer run of the SAME schedule
// distinguishes "rare" from "unreachable". Both counts are reported.
//
// THE FALSIFIERS AS THEY STOOD, recorded here because the measurement was only
// interesting if the properties really were false. These bindings are now
// EXCLUDED BY THE GUARDS rather than falsifying — they are reproduced verbatim
// as the historical evidence, and `config_guard_test.go` asserts every one of
// them against the current store. Run against the committed store with
// `oath eval`; no solver, no store write:
//
//	(config-has-key (Cons [Str] (str-append "a=b" "=v") (Nil [Str])) "a=b")
//	  -> false     FALSIFIER   (finds-head asserts true)
//	(config-has-key (Cons [Str] (str-append "a"   "=v") (Nil [Str])) "a")
//	  -> true      CONTROL     (no '=' in the key)
//
//	(config-missing (Cons [Str] (str-append "a=b" (str-append "=" "v")) (Nil [Str]))
//	                (Cons [Str] "a=b" (Nil [Str])))
//	  -> (SCons 97 (SCons 61 (SCons 98 SNil)))   i.e. "a=b"
//	                FALSIFIER   (complete-config-reports-nothing asserts SNil)
//	  with k = "a"          -> SNil              CONTROL
//	  with k = "a", v="x=y" -> SNil              CONTROL: '=' in the VALUE is
//	                                             harmless, isolating the cause
//	                                             to the KEY exactly.
//
// THE INSTRUMENT'S REVERT-CHECK. A zero that a broken counter would also
// report is not evidence. Beyond the four counter controls asserted below, the
// generator itself was mutated in a throwaway worktree — gen.go's int case
// `r.intIn(-20, 20)` widened to `r.intIn(-20, 127)`, nothing else changed —
// and re-measured:
//
//	                       committed          mutated to (-20,127)
//	  finds-head   key     0/10000            36/10000  (0.360%)
//	  complete...  k       0/10000            34/10000  (0.340%)
//	  alphabet, cp 61      0 occurrences      709 occurrences
//	  observed range       [-20, 20]          [-20, 127]
//
// So the zero is a property of the generator's integer range, not of the
// counter or of the seed reconstruction. Note the second column also stays
// BELOW the pre-registered 1% threshold: widening the range alone does not buy
// reasonable reach for a particular delimiter. That is a measured observation
// about this generator under that one mutation, not a recommendation.
//
// Under that same mutation the RATCHET assertions below fail with
// "RECORDED MEASUREMENT IS STALE", naming the new figures — so the revert-check
// is reproducible rather than a claim about a run nobody can repeat.
//
// THE ZERO IS NOT NEWS TO THE REPO, ONLY UNQUANTIFIED. mutate.go's header
// already records that `genValue` draws Int from [-20,20] while `hex-nibble`'s
// guards sit at 48/97/65, and calls the resulting score a measure of REACH.
// What was missing is that the same limit reaches Str through its Int fields,
// and therefore decides what `tested` can mean for any definition that branches
// on a printable delimiter. This file measures that consequence; it does not
// discover the cause.
//
// WHAT THIS TEST DOES NOT CLAIM. Every number here is about THIS generator and
// THESE two committed properties. It says nothing about how many other corpus
// properties have this shape, and nothing about Str generation in any other
// implementation. It measures REACH — whether the input exists in the
// generator's range — and not whether reaching it would be a good design.

const strReachCases = 10000 // the extended run
// DERIVED from propCases, never restated: this window is "what verification
// actually runs", and a copied 200 would keep that label after the verifier
// stopped running 200.
const strReachActualCases = propCases
const strReachCodepoint = 61     // '=' — the delimiter config-key splits on
const strReachControlPoint = 10  // '\n' — reachable by construction; the counter's live control
const strReachThresholdPct = 1.0 // pre-registered "reasonable reach"

// strCodepoints flattens a Str value into its codepoints. Str is an inductive
// datatype (SNil | SCons Int Str), so this walks the spine rather than reading
// a host string. Returns ok=false for anything that is not a data value, which
// is how a binder that stopped being Str is caught instead of silently scoring
// zero.
func strCodepoints(v Value) ([]int64, bool) {
	var out []int64
	cur := v
	for {
		if cur.K != "data" {
			return nil, false
		}
		switch len(cur.Fields) {
		case 0: // SNil
			return out, true
		case 2: // SCons cp rest
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

func strContains(v Value, cp int64) (bool, bool) {
	cps, ok := strCodepoints(v)
	if !ok {
		return false, false
	}
	for _, c := range cps {
		if c == cp {
			return true, true
		}
	}
	return false, true
}

// mkStr builds a Str value from codepoints, for the counter's controls. It
// needs the Str datatype's hash and constructor indices from the store rather
// than assuming them.
func mkStr(strHash string, snilIdx, sconsIdx int, cps []int64) Value {
	v := Value{K: "data", Hash: strHash, Idx: snilIdx}
	for i := len(cps) - 1; i >= 0; i-- {
		v = Value{K: "data", Hash: strHash, Idx: sconsIdx, Fields: []Value{
			{K: "int", Int: big.NewInt(cps[i])},
			v,
		}}
	}
	return v
}

type strBinderStat struct {
	idx        int
	name       string // from the source declaration, pinned by the binder-shape assertion
	hits       int    // cases whose value contained strReachCodepoint
	hitsFirst  int    // ... within the first strReachActualCases
	ctrlHits   int    // cases whose value contained strReachControlPoint
	nonEmpty   int
	maxCp      int64
	minCp      int64
	seenAny    bool
	totalCps   int
	distinctCp map[int64]int
}

// target names the property under measurement and pins the binder shape that
// makes the positional lookup meaningful.
type strReachTarget struct {
	defName   string
	propName  string
	binderKs  []string // expected Ty.K per binder, in declaration order
	strNames  map[int]string
	relevant  int // the binder the claim is about
	sourceRef string
}

func TestStrGeneratorReachOfEqualsCodepoint(t *testing.T) {
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}

	strHash := strTypeHash(st)
	if strHash == "" {
		t.Fatal("Str is not bound in the committed store; the measurement has no subject")
	}
	strDef, err := st.GetDef(strHash)
	if err != nil {
		t.Fatalf("reading the Str datatype: %v", err)
	}
	strMeta, err := st.GetMeta(strHash)
	if err != nil {
		t.Fatalf("reading Str metadata: %v", err)
	}
	snilIdx, sconsIdx := -1, -1
	for i, c := range strDef.Ctors {
		switch len(c) {
		case 0:
			snilIdx = i
		case 2:
			sconsIdx = i
		}
		_ = strMeta
	}
	if snilIdx < 0 || sconsIdx < 0 {
		t.Fatalf("Str does not have the expected nil/cons constructor shape: %v", strDef.Ctors)
	}

	// --- CONTROLS ON THE COUNTER ---------------------------------------------
	// A zero is only evidence if the thing reporting it can report non-zero.
	// These run before the measurement so a broken counter fails first.
	if got, ok := strContains(mkStr(strHash, snilIdx, sconsIdx, []int64{97, 61, 98}), strReachCodepoint); !ok || !got {
		t.Fatalf("counter POSITIVE control failed: \"a=b\" not reported as containing %d (ok=%v)", strReachCodepoint, ok)
	}
	if got, ok := strContains(mkStr(strHash, snilIdx, sconsIdx, []int64{97, 98}), strReachCodepoint); !ok || got {
		t.Fatalf("counter NEGATIVE control failed: \"ab\" reported as containing %d (ok=%v)", strReachCodepoint, ok)
	}
	if got, ok := strContains(mkStr(strHash, snilIdx, sconsIdx, nil), strReachCodepoint); !ok || got {
		t.Fatalf("counter EMPTY control failed: SNil mishandled (ok=%v got=%v)", ok, got)
	}
	if _, ok := strContains(Value{K: "int", Int: big.NewInt(1)}, strReachCodepoint); ok {
		t.Fatal("counter TYPE control failed: a non-Str value was scored instead of rejected")
	}

	targets := []strReachTarget{
		{
			defName:  "config-has-key",
			propName: "finds-head",
			// (prop finds-head [(rest (List Str)) (key Str)] ...)
			binderKs:  []string{"data", "data"},
			strNames:  map[int]string{1: "key"},
			relevant:  1,
			sourceRef: "examples/config.oath:35-37",
		},
		{
			defName:  "config-missing",
			propName: "complete-config-reports-nothing",
			// (prop complete-config-reports-nothing [(k Str) (v Str)] ...)
			binderKs:  []string{"data", "data"},
			strNames:  map[int]string{0: "k", 1: "v"},
			relevant:  0,
			sourceRef: "examples/config.oath:55-57",
		},
	}

	fmt.Printf("\n=== Str generator reach: codepoint %d ('='), committed store ../codebase ===\n", strReachCodepoint)
	fmt.Printf("pre-registered threshold: >= %.0f%% of %d cases (>= %d cases)\n\n",
		strReachThresholdPct, strReachCases, int(strReachThresholdPct/100*strReachCases))

	anyMeasured := false
	for _, tg := range targets {
		h, ok := st.Resolve(tg.defName)
		if !ok {
			t.Fatalf("%s is not live in the committed store", tg.defName)
		}
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("reading %s: %v", tg.defName, err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("reading %s metadata: %v", tg.defName, err)
		}
		pi := -1
		for i, n := range m.PropNames {
			if n == tg.propName {
				pi = i
				break
			}
		}
		if pi < 0 || pi >= len(d.Props) {
			t.Fatalf("%s has no property named %s (names: %v)", tg.defName, tg.propName, m.PropNames)
		}
		p := &d.Props[pi]

		// Pin the binder shape. Binder NAMES are not stored in the object, so
		// the relevant binder is identified positionally from the source
		// declaration quoted above. If the signature ever changes, this fails
		// loudly rather than measuring a different binder under the old name.
		if len(p.Binders) != len(tg.binderKs) {
			t.Fatalf("%s.%s binder arity changed: got %d, source declares %d — the positional binder map is stale",
				tg.defName, tg.propName, len(p.Binders), len(tg.binderKs))
		}
		for i, want := range tg.binderKs {
			if p.Binders[i].K != want {
				t.Fatalf("%s.%s binder %d kind changed: got %q want %q", tg.defName, tg.propName, i, p.Binders[i].K, want)
			}
		}
		for i := range tg.strNames {
			if !isStrTy(strHash, &p.Binders[i]) {
				t.Fatalf("%s.%s binder %d (%s) is no longer Str", tg.defName, tg.propName, i, tg.strNames[i])
			}
		}

		// --- the tester's own seed/size path ---------------------------------
		// DERIVED, not reproduced: caseSeedBase and genPropCase are the same
		// functions runProp binds its cases from, so this measures the
		// tester's population by construction and cannot drift from it.
		base := caseSeedBase(h)

		stats := map[int]*strBinderStat{}
		for i, n := range tg.strNames {
			stats[i] = &strBinderStat{idx: i, name: n, minCp: 1 << 62, distinctCp: map[int64]int{}}
		}

		for c := 0; c < strReachCases; c++ {
			// genPropCase generates EVERY binder, in order, off one rng —
			// which is exactly why it is called rather than partially
			// reproduced: scoring only the binders of interest would
			// desynchronise the stream and measure a generator the tester
			// never runs.
			env, err := genPropCase(st, p, base, pi, c)
			if err != nil {
				t.Fatalf("%s.%s case %d: generation failed: %v", tg.defName, tg.propName, c, err)
			}
			if len(env) != len(p.Binders) {
				t.Fatalf("%s.%s case %d: generated %d values for %d binders", tg.defName, tg.propName, c, len(env), len(p.Binders))
			}
			for bi, v := range env {
				s, scored := stats[bi]
				if !scored {
					continue
				}
				cps, ok := strCodepoints(v)
				if !ok {
					t.Fatalf("%s.%s case %d binder %d: value is not a Str spine", tg.defName, tg.propName, c, bi)
				}
				if len(cps) > 0 {
					s.nonEmpty++
				}
				// SCAN THE WHOLE STRING, THEN DECIDE. Accumulating the
				// aggregates and short-circuiting on the first hit in one loop
				// truncates totalCps, distinctCp and the observed range at that
				// hit — and it does so only once '=' becomes reachable, which is
				// precisely the change this instrument exists to diagnose. The
				// aggregates would then be wrong exactly when they are read.
				for _, cp := range cps {
					s.totalCps++
					s.distinctCp[cp]++
					if !s.seenAny || cp > s.maxCp {
						s.maxCp = cp
					}
					if !s.seenAny || cp < s.minCp {
						s.minCp = cp
					}
					s.seenAny = true
				}
				// Per-CASE membership, counted once however many times the
				// codepoint occurs in the value.
				if hit, ok := strContains(v, strReachCodepoint); ok && hit {
					s.hits++
					if c < strReachActualCases {
						s.hitsFirst++
					}
				}
				if hit, ok := strContains(v, strReachControlPoint); ok && hit {
					s.ctrlHits++
				}
			}
		}

		fmt.Printf("%s.%s   (%s, prop index %d, seed base %016x)\n", tg.defName, tg.propName, tg.sourceRef, pi, base)
		idxs := []int{}
		for i := range stats {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		for _, i := range idxs {
			s := stats[i]
			rel := ""
			if i == tg.relevant {
				rel = "  <- the binder the claim is about"
			}
			fmt.Printf("  binder %d %-4s  hits(cp %d): %d/%d (%.3f%%)   in first %d cases: %d\n",
				s.idx, s.name, strReachCodepoint, s.hits, strReachCases,
				100*float64(s.hits)/float64(strReachCases), strReachActualCases, s.hitsFirst)
			fmt.Printf("            non-empty values: %d/%d   codepoints emitted: %d   observed range: [%d, %d]   distinct: %d\n",
				s.nonEmpty, strReachCases, s.totalCps, s.minCp, s.maxCp, len(s.distinctCp))
			fmt.Printf("            LIVE CONTROL hits(cp %d, '\\n'): %d/%d (%.3f%%)%s\n",
				strReachControlPoint, s.ctrlHits, strReachCases,
				100*float64(s.ctrlHits)/float64(strReachCases), rel)

			// --- CONTROL ON THE GENERATOR RUN --------------------------------
			// If no codepoints were emitted at all, a zero for 61 would be a
			// fact about string LENGTH, not about the codepoint alphabet.
			if s.totalCps == 0 {
				t.Fatalf("%s.%s binder %d (%s): the generator emitted no codepoints at all over %d cases; "+
					"a zero for %d would not be evidence about the alphabet",
					tg.defName, tg.propName, s.idx, s.name, strReachCases, strReachCodepoint)
			}
			if s.ctrlHits == 0 {
				t.Fatalf("%s.%s binder %d (%s): LIVE CONTROL failed — codepoint %d is inside the generator's "+
					"own int range and was still never produced in %d cases; the measurement path is suspect",
					tg.defName, tg.propName, s.idx, s.name, strReachControlPoint, strReachCases)
			}

			if i == tg.relevant {
				anyMeasured = true
				pct := 100 * float64(s.hits) / float64(strReachCases)
				if pct >= strReachThresholdPct {
					fmt.Printf("            VERDICT: reach ACHIEVED (%.3f%% >= %.0f%%)\n", pct, strReachThresholdPct)
				} else {
					fmt.Printf("            VERDICT: reach NOT achieved (%.3f%% < %.0f%%)\n", pct, strReachThresholdPct)
				}
				// --- THE RECORDED MEASUREMENT, AS A RATCHET ------------------
				// A printed verdict rots: `go test` hides the output of a
				// passing test, so the 0/10000 written into this file's header
				// could stop being true with no signal at all. Asserting it does
				// NOT demand the generator stay unable to produce '='. It
				// demands that CHANGING that fact be loud, so the recorded
				// number and the prose quoting it are re-read together rather
				// than one of them silently becoming a lie.
				if s.hits != 0 {
					t.Errorf("RECORDED MEASUREMENT IS STALE — %s.%s binder %d (%s) now produces codepoint %d "+
						"in %d/%d cases (%.3f%%), where this file records 0. That is not a regression: it means "+
						"the Str generator's reach has changed. Re-read the header block, the #161 write-up and "+
						"anything quoting these figures, then update them together.",
						tg.defName, tg.propName, s.idx, s.name, strReachCodepoint,
						s.hits, strReachCases, pct)
				}
				// The generator's alphabet is what the zero is a consequence
				// of; pin the bound so a widened range cannot pass unnoticed
				// even if '=' itself happens to stay unreached.
				if s.maxCp > 20 || s.minCp < -20 {
					t.Errorf("RECORDED MEASUREMENT IS STALE — %s.%s binder %d (%s) observed codepoints in [%d, %d], "+
						"where this file records [-20, 20]. The Str alphabet has moved; re-read the recorded figures.",
						tg.defName, tg.propName, s.idx, s.name, s.minCp, s.maxCp)
				}
			}
		}
		fmt.Println()
	}
	if !anyMeasured {
		t.Fatal("no relevant binder was measured; the instrument reported nothing")
	}
}

// TestStrGeneratorCodepointAlphabet measures the alphabet directly, one layer
// below the two properties: what codepoints can genValue put inside a Str AT
// ALL, over the same size schedule the tester uses? This is the diagnosis the
// per-property counts on their own cannot supply — a zero for '=' in two
// properties is consistent with bad luck; an alphabet that excludes it is not.
func TestStrGeneratorCodepointAlphabet(t *testing.T) {
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	strHash := strTypeHash(st)
	if strHash == "" {
		t.Fatal("Str is not bound in the committed store")
	}
	// A synthetic single-Str-binder property, drawn through genPropCase so this
	// samples the TESTER's schedule rather than a lookalike. Sampling
	// `genValue` directly with a hand-rolled rng and size would measure a
	// generator the tester does not run the moment either changes — the same
	// defect this file measures one layer down.
	strProp := &Prop{Binders: []Ty{{K: "data", Hash: strHash}}}

	const draws = 200000
	seen := map[int64]int{}
	total := 0
	nonEmpty := 0
	for c := 0; c < draws; c++ {
		env, err := genPropCase(st, strProp, 0, 0, c)
		if err != nil {
			t.Fatalf("draw %d: %v", c, err)
		}
		cps, ok := strCodepoints(env[0])
		if !ok {
			t.Fatalf("draw %d: not a Str spine", c)
		}
		if len(cps) > 0 {
			nonEmpty++
		}
		for _, cp := range cps {
			seen[cp]++
			total++
		}
	}
	if total == 0 {
		t.Fatalf("no codepoints emitted over %d draws; the instrument measured nothing", draws)
	}
	var keys []int64
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	fmt.Printf("\n=== Str codepoint alphabet, %d draws, size schedule c%%8 ===\n", draws)
	fmt.Printf("non-empty strings: %d/%d   codepoints emitted: %d   distinct: %d   range: [%d, %d]\n",
		nonEmpty, draws, total, len(keys), keys[0], keys[len(keys)-1])
	fmt.Printf("codepoint 61 ('='): %d occurrences\n", seen[61])
	fmt.Printf("codepoint 10 ('\\n'): %d occurrences\n", seen[10])
	fmt.Printf("codepoint 44 (','): %d occurrences\n", seen[44])
	// A compact histogram: the alphabet is small enough to print whole.
	fmt.Printf("alphabet: ")
	for _, k := range keys {
		fmt.Printf("%d ", k)
	}
	fmt.Printf("\n")
	negatives := 0
	for _, k := range keys {
		if k < 0 {
			negatives += seen[k]
		}
	}
	fmt.Printf("occurrences of NEGATIVE codepoints (outside any scalar range): %d\n\n", negatives)

	// --- THE RECORDED ALPHABET, AS A RATCHET ---------------------------------
	// Same reasoning as the per-property ratchet above: these assertions do not
	// say the alphabet OUGHT to be [-20, 20]. They say that if it moves, the
	// figures recorded in this file and quoted in the #161 write-up must be
	// re-read rather than left standing.
	if keys[0] != -20 || keys[len(keys)-1] != 20 || len(keys) != 41 {
		t.Errorf("RECORDED MEASUREMENT IS STALE — Str alphabet is now [%d, %d] with %d distinct codepoints, "+
			"where this file records [-20, 20] with 41. Re-read the recorded figures before trusting them.",
			keys[0], keys[len(keys)-1], len(keys))
	}
	for _, cp := range []int64{61, 44} {
		if seen[cp] != 0 {
			t.Errorf("RECORDED MEASUREMENT IS STALE — codepoint %d now occurs %d times in %d draws, "+
				"where this file records 0. The Str generator's reach has changed; update the recorded figures.",
				cp, seen[cp], draws)
		}
	}
	// The discriminating control: a codepoint inside the generator's own range
	// must be produced, or the two zeros above are facts about the instrument.
	if seen[10] == 0 {
		t.Errorf("CONTROL FAILED — codepoint 10 is inside the generator's int range and was never produced "+
			"in %d draws; the zeros recorded for 61 and 44 are not evidence about the alphabet.", draws)
	}
}
