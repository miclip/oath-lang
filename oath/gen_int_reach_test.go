package main

// A READ-ONLY census of how far the Int generator reaches into the corpus's
// property binders (#170), and the assertion that makes it a measurement rather
// than a printout.
//
// WHY THIS EXISTS. #170 asks whether `genValue`'s `int` arm — `intIn(-20, 20)`
// with a boundary bias — leaves a class of properties undischargeable by
// testing. Answering that needs the affected UNIVERSE first, and the tempting
// universe is wrong: the motivating case was `Str`, so it is easy to measure
// `Str` and `(List Int)` binders and report a number that describes the
// implementation's decomposition rather than the claim's. Every `Int` in a
// generated value comes from that one arm whatever wraps it, so the claim
// quantifies over every binder whose value can transitively CONTAIN an Int —
// through record fields, ADT constructor fields, and the keys, values and
// defaults of generated function tables.
//
// Two questions are asked of that population and deliberately kept apart:
//
//	STATIC   can a value of this binder's type contain an Int under genValue's
//	         own rules — computed by walking the type
//	OBSERVED what integers the tester actually draws into that binder over the
//	         propCases cases verification runs
//
// The observed half goes through genPropCase, which the kernel names as the SOLE
// authority on the seed and size schedule. Reproducing that derivation here
// would yield a population that looks like the tester's and would silently stop
// being it the moment the schedule changed.
//
// WHAT IT ASSERTS. Only what cannot rot: that the static walker is SOUND
// (nothing draws an Int into a binder the walker called unreachable), that every
// property could be generated at all, and that the census is not empty. It
// hardcodes NO counts — a pinned 833 would be the duplicated-authority defect
// this repo keeps paying for, and the numbers move whenever the corpus does.
// The figures are OUTPUT. The record that interprets them, against a named
// corpus state, is docs/experiments/issue-170.md.
//
// RELATION TO gen_str_reach_test.go. That file measures the same generator
// through the same seam and is NOT superseded by this one: it asks a narrow,
// pre-registered question — how often codepoint 61 reaches the `key`/`k` binder
// of two named properties (#161/#162) — with a threshold fixed before the first
// run. This asks a corpus-wide one with no threshold at all. Neither answers the
// other's question, and the overlap is deliberately only the seam.
//
// Nothing here writes: the store is opened, read, and closed.
//
//	cd oath && go test -run TestIntReachCensus -count=1 -v
//	cd oath && INT_REACH_OUT=/tmp/binders.json go test -run TestIntReachCensus -count=1

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"
	"testing"
)

type binderReach struct {
	Def         string `json:"def"`  // representative live name for the object
	Hash        string `json:"hash"` // the object; verdicts are hash-keyed
	Prop        string `json:"prop"`
	PropIndex   int    `json:"prop_index"`
	Binder      int    `json:"binder"`
	Ty          string `json:"ty"`
	StaticReach bool   `json:"static_int_reach"`
	Draws       int    `json:"observed_int_arm_draws"`
	// Bounds are *big.Int, and the reason is the whole subject of this file:
	// Oath's Int is ℤ, so a widened generator — the change #170 exists to judge —
	// is exactly the thing that could draw a value outside int64. An instrument
	// that narrowed to int64 would silently discard the inputs it was pointed at,
	// and would go on reporting a clean [-20, 20] while doing it. Serialised as
	// decimal strings so the JSON does not reintroduce the same ceiling.
	Min *bigDec `json:"min"`
	Max *bigDec `json:"max"`
	// Kept apart from the above, never summed with it: a native affine's
	// coefficients are integers in the value that did NOT come from the `int`
	// arm. int64 is exact here rather than a narrowing — gen.go stores NA and NB
	// as int64 fields, so no wider value can reach them. See collectInts.
	AffineDraws int   `json:"observed_affine_coefficients"`
	AffineMin   int64 `json:"affine_min"`
	AffineMax   int64 `json:"affine_max"`
}

// intReachable reports whether genValue, generating a value of ty, can place an
// Int anywhere inside it. Binders are monomorphic, so no type variable occurs.
//
// The `fun` arm is the one a caller-enumerating version would miss: a generated
// function is a finite table over its domain and codomain — or, for Int -> Int,
// an identity/affine native whose coefficients are Int draws — so a binder of
// type (-> Str Str) reaches the Int arm twice over, through the codepoints of
// both the keys and the values.
// bigDec is a *big.Int that marshals as a decimal STRING. encoding/json writes a
// *big.Int as a JSON NUMBER, so the comment above promised a ceiling-free wire
// form that the struct tags did not deliver: any consumer parsing JSON numbers
// as float64 — every JavaScript one — silently rounds past 2^53, which is
// exactly the narrowing this field exists to prevent. Found by review; the
// comment asserted a correspondence the code did not implement.
type bigDec struct{ *big.Int }

func (b bigDec) MarshalJSON() ([]byte, error) {
	if b.Int == nil {
		return []byte("null"), nil
	}
	return []byte(`"` + b.Int.String() + `"`), nil
}

func newBigDec(i *big.Int) *bigDec {
	if i == nil {
		return nil
	}
	return &bigDec{i}
}

func intReachable(st *Store, ty *Ty, memo map[string]int8, errs *[]error) bool {
	if ty == nil {
		return false
	}
	switch ty.K {
	case "int":
		return true
	case "rat", "float", "bool":
		return false
	case "record":
		for i := range ty.Args {
			if intReachable(st, &ty.Args[i], memo, errs) {
				return true
			}
		}
		return false
	case "fun":
		return intReachable(st, ty.A, memo, errs) || intReachable(st, ty.B, memo, errs)
	case "data":
		// The memo key is a FINITE ABSTRACTION of the instantiation, not the
		// instantiation itself. Keying on the printed type is correct for every
		// regular datatype and does not terminate for a nested-recursive one: a
		// declaration whose constructor mentions `(Bush (Bush a))` expands to a
		// strictly larger type at every step, so no key ever repeats and the
		// walk recurses until the stack ends.
		//
		// What decides the answer is the declaration plus, for each type
		// argument, only WHETHER it reaches Int — which is why (List Int) and
		// (List (List Int)) share a key and an answer. At most 2^arity keys per
		// declaration, so the walk is bounded whatever the corpus contains.
		//
		// RESULTS ARE CACHED, not merely marked visited, and the distinction is
		// load-bearing: computing the argument flags for (List (List Int))
		// visits the key (List, [reaches Int]) on the way in, and a
		// visited-means-false marker would then report the OUTER type Int-free.
		// That is not hypothetical — it is what the first version of this
		// function did, and the soundness assertion below caught it on eleven
		// binders. In-progress re-entry still returns false: a cycle contributes
		// no new Int, which is the least fixed point.
		const inProgress, isFalse, isTrue = int8(1), int8(2), int8(3)
		key := ty.Hash + "|"
		for i := range ty.Args {
			if intReachable(st, &ty.Args[i], memo, errs) {
				key += "1"
			} else {
				key += "0"
			}
		}
		switch memo[key] {
		case inProgress, isFalse:
			return false
		case isTrue:
			return true
		}
		memo[key] = inProgress
		d, err := st.GetDef(ty.Hash)
		if err != nil {
			// NOT classified Int-free. A datatype this census cannot read is a
			// BROKEN CENSUS, not a type without Ints — and the difference is
			// invisible downstream, because generation can still complete for
			// every deterministic case while the static population is silently
			// undercounted. Recorded and surfaced by the caller.
			*errs = append(*errs, fmt.Errorf("datatype %s unreadable, so its Int-reachability is unknown: %w", ty.Hash, err))
			memo[key] = isFalse
			return false
		}
		for i := range d.Ctors {
			for _, f := range instCtorFields(d, ty.Hash, ty.Args, i) {
				if intReachable(st, f, memo, errs) {
					memo[key] = isTrue
					return true
				}
			}
		}
		memo[key] = isFalse
		return false
	}
	return false
}

// collectInts gathers the integers the generator drew into v, in TWO BUCKETS
// that must not be added together.
//
//	arm     values produced by genValue's `int` arm — the draw #170 is about.
//	        A native table's keys, values and default belong here: they are
//	        generated by recursive genValue calls, so a plain Fields walk that
//	        stopped at the native would undercount them.
//	affine  the NA/NB coefficients of a native affine function. These are
//	        integers in a generated value, and they are NOT the `int` arm —
//	        gen.go draws them from its own intIn(-3,3) and intIn(-10,10). Folding
//	        them into the headline figure would report a measurement of one draw
//	        while summing three, and would go on looking right if the `int` arm's
//	        range changed underneath.
func collectInts(v Value, arm, affine *[]*big.Int) {
	switch v.K {
	case "int":
		*arm = append(*arm, v.Int)
	case "record", "data":
		for _, f := range v.Fields {
			collectInts(f, arm, affine)
		}
	case "native":
		switch v.Native {
		case "affine":
			*affine = append(*affine, big.NewInt(v.NA), big.NewInt(v.NB))
		case "table":
			for _, k := range v.Fields {
				collectInts(k, arm, affine)
			}
			for _, t := range v.TVals {
				collectInts(t, arm, affine)
			}
			if v.NVal != nil {
				collectInts(*v.NVal, arm, affine)
			}
		}
	}
}

func TestIntReachCensus(t *testing.T) {
	// Collected rather than swallowed: an unreadable datatype makes the STATIC
	// half of this census wrong while every generated case still succeeds, so it
	// has to reach the verdict rather than stop at a memo entry.
	var reachErrs []error
	errs := &reachErrs

	// The FILESYSTEM backend explicitly: OpenStore consults OATH_BACKEND and,
	// when it is `cloud`, ignores the path it was handed — the census would then
	// describe a different corpus while every assertion still passed.
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}

	// One representative per unique hash. Two names resolving to one object
	// share every property and every draw, so counting per NAME would double
	// the aliased ones — the same join hazard corpus_census_test.go documents.
	byHash := map[string][]string{}
	for n, h := range st.Names() {
		byHash[h] = append(byHash[h], n)
	}
	hashes := make([]string, 0, len(byHash))
	for h := range byHash {
		sort.Strings(byHash[h])
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool { return byHash[hashes[i]][0] < byHash[hashes[j]][0] })

	var stats []binderReach
	objects, props := 0, 0
	for _, h := range hashes {
		// An UNREADABLE object and an object with nothing to measure are
		// different outcomes and must not share a branch: folding them together
		// drops a live definition from the population while every non-emptiness
		// assertion below still passes, which is a complete-looking census over
		// a silently smaller corpus.
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s (%s): reading the definition: %v", byHash[h][0], shortHash(h), err)
		}
		if d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s: reading meta: %v", byHash[h][0], err)
		}
		objects++
		base := caseSeedBase(h)
		for pi := range d.Props {
			props++
			p := &d.Props[pi]
			row := make([]binderReach, len(p.Binders))
			for bi := range p.Binders {
				row[bi] = binderReach{
					Def: byHash[h][0], Hash: h, Prop: metaPropName(m, pi),
					PropIndex: pi, Binder: bi, Ty: debugTy(&p.Binders[bi]),
					StaticReach: intReachable(st, &p.Binders[bi], map[string]int8{}, errs),
				}
			}
			for c := 0; c < propCases; c++ {
				env, err := genPropCase(st, p, base, pi, c)
				if err != nil {
					// A binder the tester cannot generate is not a quiet gap:
					// the property is untestable and `oath verify` says so.
					t.Fatalf("%s prop %s case %d: %v", byHash[h][0], metaPropName(m, pi), c, err)
				}
				for bi, v := range env {
					var arm, affine []*big.Int
					collectInts(v, &arm, &affine)
					for _, x := range arm {
						// Every draw counts, at any magnitude. No int64 filter:
						// see the comment on Min/Max.
						if row[bi].Draws == 0 {
							row[bi].Min = newBigDec(new(big.Int).Set(x))
							row[bi].Max = newBigDec(new(big.Int).Set(x))
						}
						if x.Cmp(row[bi].Min.Int) < 0 {
							row[bi].Min = newBigDec(new(big.Int).Set(x))
						}
						if x.Cmp(row[bi].Max.Int) > 0 {
							row[bi].Max = newBigDec(new(big.Int).Set(x))
						}
						row[bi].Draws++
					}
					for _, x := range affine {
						// Exact by construction: these came from int64 fields.
						n := x.Int64()
						if row[bi].AffineDraws == 0 {
							row[bi].AffineMin, row[bi].AffineMax = n, n
						}
						if n < row[bi].AffineMin {
							row[bi].AffineMin = n
						}
						if n > row[bi].AffineMax {
							row[bi].AffineMax = n
						}
						row[bi].AffineDraws++
					}
				}
			}
			stats = append(stats, row...)
		}
	}

	// --- the assertions ------------------------------------------------------
	// A census over an empty population passes every check it makes, which is
	// the failure this repo has paid for more than once.
	if objects == 0 || props == 0 || len(stats) == 0 {
		t.Fatalf("census measured nothing: %d objects, %d properties, %d binders",
			objects, props, len(stats))
	}
	// SOUNDNESS of the static walker, which is the only claim that cannot rot:
	// if an Int was drawn into a binder, the walk must have said it could be.
	// The converse is reported below rather than asserted — a constructor that
	// carries an Int but is never selected would make it false without anything
	// being wrong.
	for _, s := range stats {
		if s.Draws > 0 && !s.StaticReach {
			t.Errorf("%s prop %s binder %d (%s): drew %d integers but the static walk called it Int-free",
				s.Def, s.Prop, s.Binder, s.Ty, s.Draws)
		}
	}

	// --- the census ----------------------------------------------------------
	reach, unseen, draws, aDraws := 0, 0, 0, 0
	var lo, hi *big.Int
	aLo, aHi := int64(0), int64(0)
	aFirst := true
	byTy := map[string]int{}
	nonTy := map[string]int{}
	for _, s := range stats {
		if s.StaticReach {
			reach++
			byTy[s.Ty]++
			if s.Draws == 0 {
				unseen++
			}
		} else {
			nonTy[s.Ty]++
		}
		draws += s.Draws
		if s.Draws > 0 {
			if lo == nil {
				lo, hi = new(big.Int).Set(s.Min.Int), new(big.Int).Set(s.Max.Int)
			}
			if s.Min.Int.Cmp(lo) < 0 {
				lo = new(big.Int).Set(s.Min.Int)
			}
			if s.Max.Int.Cmp(hi) > 0 {
				hi = new(big.Int).Set(s.Max.Int)
			}
		}
		aDraws += s.AffineDraws
		if s.AffineDraws > 0 {
			if aFirst {
				aLo, aHi, aFirst = s.AffineMin, s.AffineMax, false
			}
			if s.AffineMin < aLo {
				aLo = s.AffineMin
			}
			if s.AffineMax > aHi {
				aHi = s.AffineMax
			}
		}
	}
	if len(reachErrs) > 0 {
		t.Errorf("the static Int-reachability census could not read %d datatype(s), so its "+
			"population is undercounted and the range claim below is over a set this test "+
			"does not know: %v", len(reachErrs), reachErrs)
	}
	t.Logf("live objects with properties : %d", objects)
	t.Logf("properties                   : %d", props)
	t.Logf("property binders             : %d", len(stats))
	t.Logf("  Int-reaching (static)      : %d", reach)
	t.Logf("    of which drew no Int     : %d", unseen)
	t.Logf("  not Int-reaching           : %d", len(stats)-reach)
	t.Logf("`int`-arm draws (%d cases)  : %d", propCases, draws)
	if lo == nil {
		// Unreachable while the assertions above hold; stated so a future change
		// that empties the draw set cannot print "[0, 0]" and look measured.
		t.Fatal("no integers were drawn at all: the census has no range to report")
	}
	t.Logf("observed `int`-arm range     : [%s, %s]", lo, hi)
	// Reported beside the headline figure and never added to it: separate draws,
	// separate ranges, and only the first is what #170 is about.
	t.Logf("affine coefficients (NOT the `int` arm): %d in [%d, %d]", aDraws, aLo, aHi)
	t.Log(tallied("Int-reaching binder types", byTy))
	t.Log(tallied("non-Int-reaching binder types", nonTy))

	if out := os.Getenv("INT_REACH_OUT"); out != "" {
		b, err := json.MarshalIndent(stats, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("per-binder records written to %s", out)
	}
}

func tallied(title string, m map[string]int) string {
	type kv struct {
		k string
		n int
	}
	var es []kv
	for k, n := range m {
		es = append(es, kv{k, n})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].n != es[j].n {
			return es[i].n > es[j].n
		}
		return es[i].k < es[j].k
	})
	s := title + ":"
	for _, e := range es {
		s += fmt.Sprintf("\n  %4d  %s", e.n, e.k)
	}
	return s
}
