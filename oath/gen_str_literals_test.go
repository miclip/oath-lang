package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// SPEC §4's literal-weighted Str generation: the rule, its blast radius, and
// its stability.
//
// WHY THE RULE EXISTS, stated once so the measurements below are readable.
// Generated Str codepoints came from the generic Int arm alone, whose range
// contains no printable character at all — so a property quantifying over a Str
// key could never see the delimiter its own dependency branches on, and two
// committed properties carried a false `tested` guarantee for exactly that
// reason. Widening the Int arm uniformly was measured and made the codepoint
// REACHABLE without making it FINDABLE; widening further made detection worse,
// because a larger alphabet dilutes each individual codepoint. Weighting toward
// the definition's own literals is what closed it.
//
// EVERY POPULATION AND BUDGET IS NAMED WHERE IT IS USED. A blast-radius number
// is unreadable without knowing what it ranged over:
//
//	CORPUS       unique OBJECTS reached by a live name in a COPIED store,
//	             K=func, at least one property. Objects, not names: a verdict is
//	             a fact about a hash, and names alias.
//	UNRELATED    the subset of CORPUS whose transitive closure yields ZERO
//	             literals — where §4 requires the rule to be a no-op.
//	MUTANTS      every mutant genMutants produces for every CORPUS object,
//	             generated ONCE and scored by both arms.
//	CHAIN        a three-link scratch corpus in a temp store, for the stability
//	             controls.
//
// THE UNWEIGHTED ARM IS A MEASUREMENT DEVICE, NOT A MODE. `unweightedSchedule`
// reconstructs the pre-§4 stream so a comparison can produce a difference; no
// kernel path builds one, because §4 makes L(D) a function of the definition.

const (
	// The delimiter examples/config.oath splits on. Spelled out in a
	// MEASUREMENT of the generator and never in a property: the properties
	// derive their domain by round-tripping through config-key, so the constant
	// has exactly one normative home and this is not it.
	litDelimiter = 61

	// The two SUPERSEDED objects whose unguarded properties this rule was built
	// to find. Addressed by hash because no name resolves to them any more —
	// that is what makes them pre-repair.
	preRepairFindsHead = "026c7502118d609a3106c199e9fb6ea854415f7a6c5f6b0c7aab719435e3bdb1"
	preRepairComplete  = "8c9e095b0d72fe2dd3338908839c2f89353f2585efb362b8d298a6f562b779a6"
)

// ---------------------------------------------------------------------------
// Stores and populations.

// copiedStore copies the committed store into a temp directory and opens the
// copy. TestCopiedStoreIsFaithful is what makes the copy usable as evidence.
func copiedStore(t *testing.T) (*Store, string) {
	t.Helper()
	dst := t.TempDir()
	if err := copyTree("../codebase", dst); err != nil {
		t.Fatalf("copying the committed store: %v", err)
	}
	be, err := openFSBackend(dst)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	st, err := newStoreWithBackend(be, dst)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	return st, dst
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func litCorpus(t *testing.T, st *Store) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		out = append(out, h)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("the CORPUS population is empty; every measurement below would be vacuous")
	}
	return out
}

func TestCopiedStoreIsFaithful(t *testing.T) {
	st, dir := copiedStore(t)
	live := openCommittedStore(t)
	corpus := litCorpus(t, st)
	if len(corpus) != len(litCorpus(t, live)) {
		t.Fatalf("the copy at %s holds a different CORPUS than the committed store", dir)
	}
	for _, h := range corpus {
		a, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("copy is missing %s: %v", shortHash(h), err)
		}
		if b, err := live.GetDef(h); err != nil || hashDef(a) != hashDef(b) {
			t.Fatalf("%s differs between the copy and the committed store", shortHash(h))
		}
	}
	t.Logf("CORPUS: %d unique live func objects with properties, copy verified against the committed store", len(corpus))
}

// ---------------------------------------------------------------------------
// The rule's own preconditions.

func TestCanonicalStrHashMatchesTheCorpus(t *testing.T) {
	st, _ := copiedStore(t)
	byName, ok := st.Resolve("Str")
	if !ok {
		t.Fatal("Str is not bound in the copied store")
	}
	if got := canonicalStrHash(); got != byName {
		t.Fatalf("the derived Str identity %s is not what the corpus binds as Str (%s); the rule is "+
			"weighting a datatype the corpus does not use", shortHash(got), shortHash(byName))
	}
	// CONTROL: the derivation must be sensitive to the declaration it encodes,
	// or "it matches" is a fact about hashDef rather than about Str.
	if wrong := hashDef(&Def{K: "data", Ctors: [][]Ty{{}, {{K: "rat"}, {K: "rec"}}}}); wrong == byName {
		t.Fatal("a Str-shaped declaration over Rat hashes the same as Str; the derivation does not " +
			"discriminate and the match above is meaningless")
	}
}

// TestNoStrTermsSurviveCanonicalization checks the premise §4 rests on when it
// says surface Str literals contribute THROUGH the Int rule rather than beside
// it. If a `str` term ever survived into canonical bytes, extraction would miss
// its codepoints and the two kernels could still agree — both blind the same
// way — so this is checked against the corpus rather than assumed.
func TestNoStrTermsSurviveCanonicalization(t *testing.T) {
	st, _ := copiedStore(t)
	strTerms, ints := 0, 0
	var walk func(*Term)
	walk = func(tm *Term) {
		if tm == nil {
			return
		}
		switch tm.K {
		case "str":
			strTerms++
		case "int":
			ints++
		}
		walk(tm.A)
		walk(tm.B)
		walk(tm.C)
		for i := range tm.Args {
			walk(&tm.Args[i])
		}
		for i := range tm.Arms {
			walk(&tm.Arms[i])
		}
	}
	seen := map[string]bool{}
	for _, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		walk(d.Body)
		for pi := range d.Props {
			walk(&d.Props[pi].Body)
		}
	}
	if ints == 0 {
		t.Fatal("the walk found no Int literals at all; it is not reaching canonical bodies and a " +
			"zero str-term count would mean nothing")
	}
	if strTerms != 0 {
		t.Errorf("%d `str` terms survive canonicalization; §4 assumes surface Str literals elaborate "+
			"to SCons chains of Int literals, and extraction would miss these", strTerms)
	}
	t.Logf("PREMISE: %d Int literals and %d surviving `str` terms across the corpus", ints, strTerms)
}

// ---------------------------------------------------------------------------
// Extraction: the five behaviours cross-kernel conformance also covers.

// litChain builds the CHAIN in a fresh temp store: three links whose only
// literal lives at the far end.
//
//	delimiter-caller -> delimiter-relay -> delimiter-source, which holds `lit`
//
// Three links, not two, so a NON-transitive extraction provably fails here: the
// caller's direct dependency carries no literal at all. That makes the chain a
// firing control for transitivity itself rather than only for propagation.
func litChain(t *testing.T, body string, extra string) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	src := fmt.Sprintf(`
(defn delimiter-source [] [(n Int)] Int %s)
(defn delimiter-relay [] [(n Int)] Int (delimiter-source n))
(defn delimiter-caller [] [(n Int)] Int %s)
`, body, extra)
	if _, err := apiPut(st, src, "spec4-control", ""); err != nil {
		t.Fatalf("building the CHAIN: %v", err)
	}
	h, ok := st.Resolve("delimiter-caller")
	if !ok {
		t.Fatal("delimiter-caller is not bound in the CHAIN store")
	}
	return st, h, dir
}

func litsOf(t *testing.T, st *Store, h string) []string {
	t.Helper()
	w, err := strLiterals(st, h)
	if err != nil {
		t.Fatalf("extraction at %s: %v", shortHash(h), err)
	}
	if w == nil {
		return nil
	}
	out := make([]string, len(w.lits))
	for i, v := range w.lits {
		out[i] = v.String()
	}
	return out
}

// TestExtractionIsTransitive: the literal is two hops from the owner, so only a
// transitive closure finds it.
func TestExtractionIsTransitive(t *testing.T) {
	st, h, _ := litChain(t, "(+ n 61)", "(delimiter-relay n)")
	got := litsOf(t, st, h)
	if len(got) != 1 || got[0] != "61" {
		t.Fatalf("transitive extraction gave %v, want [61] — the literal two hops out was not reached", got)
	}
	// CONTROL: the owner's DIRECT dependency really does carry no literal, so
	// the result above is transitivity and not a one-hop lookup succeeding.
	relay, ok := st.Resolve("delimiter-relay")
	if !ok {
		t.Fatal("delimiter-relay is not bound")
	}
	rd, err := st.GetDef(relay)
	if err != nil {
		t.Fatalf("reading the relay: %v", err)
	}
	direct := map[string]*big.Int{}
	collectDefLiterals(rd, direct)
	if len(direct) != 0 {
		t.Fatalf("the relay carries literals %v, so the chain does not isolate transitivity", direct)
	}
	t.Logf("TRANSITIVE: owner population %v with a literal-free direct dependency", got)
}

// TestExtractionSortsNumericallyAndDeduplicates pins the two properties a
// lexicographic or set-less implementation would get wrong. §4 requires
// ascending NUMERIC order, and the index draw is into that order — so a kernel
// sorting decimal strings would draw a different codepoint for the same seed.
func TestExtractionSortsNumericallyAndDeduplicates(t *testing.T) {
	// 9 and 10 are the pair lexicographic order inverts; 7 appears three times.
	st, h, _ := litChain(t, "(+ (+ (+ n 10) 9) 7)", "(+ (+ (delimiter-relay n) 7) 7)")
	got := litsOf(t, st, h)
	want := []string{"7", "9", "10"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("extraction gave %v, want %v — numeric ascending order, deduplicated by value", got, want)
	}
	// CONTROL: the inversion is real. If 9 and 10 sorted the same way under
	// both orders this would prove nothing.
	lex := append([]string{}, want...)
	sort.Strings(lex)
	if strings.Join(lex, ",") == strings.Join(want, ",") {
		t.Fatal("the chosen literals sort identically lexicographically and numerically; this control " +
			"cannot distinguish the two orders")
	}
	t.Logf("SORT/DEDUP: %v (lexicographic order would be %v)", got, lex)
}

// TestExtractionKeepsArbitraryPrecision: Int is ℤ, and a literal wider than a
// machine word must contribute its VALUE. Nothing in the committed corpus
// exercises this — the largest literal there is far inside int64 — so the
// coverage has to be synthetic, and this is why it is worth having.
func TestExtractionKeepsArbitraryPrecision(t *testing.T) {
	huge := new(big.Int).Lsh(big.NewInt(1), 200)     // 2^200
	wrapped := new(big.Int).Add(huge, big.NewInt(1)) // 2^200 + 1, same low 64 bits as 1
	st, h, _ := litChain(t, fmt.Sprintf("(+ n %s)", huge), fmt.Sprintf("(+ (delimiter-relay n) %s)", wrapped))
	got := litsOf(t, st, h)
	want := []string{huge.String(), wrapped.String()}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("extraction gave %v, want %v", got, want)
	}
	// CONTROL: these two really are distinct only ABOVE 64 bits. A kernel
	// narrowing to int64 would collapse or mangle them, and would also have
	// collapsed the pair to one entry.
	if len(got) != 2 {
		t.Errorf("the two 200-bit literals collapsed to %d entries; precision was lost in extraction", len(got))
	}
	if huge.Uint64() == wrapped.Uint64() == false {
		t.Log("note: the low words differ, so this control does not depend on wrap-around collision")
	}
	t.Logf("ARBITRARY PRECISION: population holds %d-bit and %d-bit literals intact",
		huge.BitLen(), wrapped.BitLen())
}

// TestZeroLiteralClosureConsumesNoExtraDraw is §4's empty-set path: with no
// literals the generator must follow Data EXACTLY, taking no extra draw. A
// kernel that drew below(4) and then ignored it would still produce plausible
// strings while desynchronising every later value in the case.
func TestZeroLiteralClosureConsumesNoExtraDraw(t *testing.T) {
	st, h, _ := litChain(t, "n", "(delimiter-relay n)")
	if got := litsOf(t, st, h); len(got) != 0 {
		t.Fatalf("the literal-free CHAIN drew population %v; this control cannot test the empty path", got)
	}
	sch, err := newGenSchedule(st, h)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if sch.lits != nil {
		t.Fatal("an empty literal set produced a non-nil weighting; the no-extra-draw path is unreachable")
	}

	// Generate Str values directly off pairs of identically-seeded rngs — one
	// carrying the empty schedule's (nil) weighting, one carrying none at all —
	// and require the same value AND the same rng state, i.e. the same number
	// of draws consumed.
	//
	// SWEPT OVER SEEDS RATHER THAN PINNED TO ONE. At most seeds the spine ends
	// immediately and the value is the empty string, which is identical under
	// every weighting and would satisfy the comparison vacuously — the first
	// draft picked one seed, drew "SNil", and its own two-way control caught
	// it. The sweep requires a population of NON-EMPTY strings before either
	// direction counts.
	strTy := &Ty{K: "data", Hash: canonicalStrHash()}
	corpus, _ := copiedStore(t)
	one := &strWeights{strHash: canonicalStrHash(), lits: []*big.Int{big.NewInt(9999)}}

	nonEmpty, differed := 0, 0
	for seed := uint64(1); seed <= 400; seed++ {
		a := &rng{s: seed, lits: sch.lits} // empty set -> nil weighting
		b := &rng{s: seed}                 // no weighting at all
		va, err := genValue(corpus, strTy, 5, a)
		if err != nil {
			continue
		}
		vb, err := genValue(corpus, strTy, 5, b)
		if err != nil {
			t.Fatalf("seed %d: unweighted generation failed where the empty schedule succeeded: %v", seed, err)
		}
		if printValue(corpus, va) != printValue(corpus, vb) || a.s != b.s {
			t.Fatalf("seed %d: an EMPTY literal set changed generation: %q vs %q, rng state %d vs %d — "+
				"§4 requires the Data rule exactly, with no extra draw",
				seed, printValue(corpus, va), printValue(corpus, vb), a.s, b.s)
		}
		if cps, ok := strCodepoints(va); ok && len(cps) > 0 {
			nonEmpty++
		}
		// TWO-WAY CONTROL, same seed: a NON-empty set must change the value or
		// the draw count, or the comparison above cannot see a weighting.
		c := &rng{s: seed, lits: one}
		vc, err := genValue(corpus, strTy, 5, c)
		if err != nil {
			continue
		}
		if printValue(corpus, vc) != printValue(corpus, va) || c.s != a.s {
			differed++
		}
	}
	if nonEmpty == 0 {
		t.Fatal("the sweep produced no non-empty Str at all; identical empty strings would satisfy " +
			"the equality above vacuously")
	}
	if differed == 0 {
		t.Error("CONTROL FAILED: a one-literal set never changed the value or the draw count over the " +
			"whole sweep, so this comparison cannot see a weighting at all")
	}
	t.Logf("EMPTY SET: 400 seeds, %d producing a non-empty Str, all byte-identical with an identical "+
		"rng state; a one-literal set differs at %d of them", nonEmpty, differed)
}

// TestWeightingReachesNestedStr is §4's "at any depth, including binders whose
// declared type is not Str". A Str inside a (List Str) must be weighted too.
func TestWeightingReachesNestedStr(t *testing.T) {
	st, _ := copiedStore(t)
	listHash, ok := st.Resolve("List")
	if !ok {
		t.Fatal("List is not bound in the copied store")
	}
	strTy := Ty{K: "data", Hash: canonicalStrHash()}
	listOfStr := &Ty{K: "data", Hash: listHash, Args: []Ty{strTy}}
	w := &strWeights{strHash: canonicalStrHash(), lits: []*big.Int{big.NewInt(9999)}}

	found, cases := false, 400
	for seed := 0; seed < cases && !found; seed++ {
		v, err := genValue(st, listOfStr, 4, &rng{s: uint64(seed), lits: w})
		if err != nil {
			continue
		}
		for _, elem := range flattenListValues(v) {
			cps, ok := strCodepoints(elem)
			if !ok {
				continue
			}
			for _, cp := range cps {
				if cp == 9999 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("the literal 9999 never appeared inside a (List Str) element over %d seeds; nested "+
			"Str values are not being weighted", cases)
	}
	// CONTROL: it must NOT appear without the weighting, or the finding above
	// is the generic Int arm rather than the rule.
	for seed := 0; seed < cases; seed++ {
		v, err := genValue(st, listOfStr, 4, &rng{s: uint64(seed)})
		if err != nil {
			continue
		}
		for _, elem := range flattenListValues(v) {
			cps, _ := strCodepoints(elem)
			for _, cp := range cps {
				if cp == 9999 {
					t.Fatalf("9999 appears WITHOUT weighting at seed %d; the control literal is "+
						"reachable from the generic Int arm and proves nothing", seed)
				}
			}
		}
	}
	t.Logf("NESTED: the weighted literal reaches (List Str) elements and is unreachable without weighting")
}

// flattenListValues walks a List spine and returns its elements.
func flattenListValues(v Value) []Value {
	var out []Value
	cur := v
	for cur.K == "data" && len(cur.Fields) == 2 {
		out = append(out, cur.Fields[0])
		cur = cur.Fields[1]
	}
	return out
}

// ---------------------------------------------------------------------------
// The rule's reason for existing: detection at the two pre-repair sites.

type litSite struct {
	hash, defName, propName string
	binder                  int
	falseKey, trueKey       string
}

func litSites() []litSite {
	return []litSite{
		{preRepairFindsHead, "config-has-key", "finds-head", 1, "a=b", "a"},
		{preRepairComplete, "config-missing", "complete-config-reports-nothing", 0, "a=b", "a"},
	}
}

func litProp(t *testing.T, st *Store, s litSite) (*Def, int) {
	t.Helper()
	d, err := st.GetDef(s.hash)
	if err != nil {
		t.Fatalf("%s: reading the pre-repair object: %v", s.propName, err)
	}
	live, ok := st.Resolve(s.defName)
	if !ok {
		t.Fatalf("%s is not live in the store; the site map is stale", s.defName)
	}
	if live == s.hash {
		t.Fatalf("%s still resolves to %s — this object is NOT pre-repair, and every claim here about "+
			"measuring the unguarded property is false", s.defName, shortHash(s.hash))
	}
	m, err := st.GetMeta(s.hash)
	if err != nil {
		t.Fatalf("%s: reading pre-repair metadata: %v", s.propName, err)
	}
	for i := range d.Props {
		if metaPropName(m, i) == s.propName {
			return d, i
		}
	}
	t.Fatalf("%s carries no property named %s", shortHash(s.hash), s.propName)
	return nil, 0
}

// TestRuleDetectsThePreRepairDefects is the measurement the whole rule was
// built for, and it carries four controls because a detection result alone
// would not be evidence — a generator that falsified everything would also
// "detect" these two.
//
//	BASELINE     the pre-§4 stream still PASSES all 200 cases. Reproduces the
//	             miss the rule exists to fix; without it detection could be an
//	             artifact of any change at all.
//	FIRING       the unguarded claim is genuinely FALSE at a key holding '='.
//	NON-FIRING   it is TRUE at an ordinary key, so it is not false everywhere
//	             and finding it took aim rather than luck.
//	POPULATION   the weighted stream actually puts '=' in the guarded binder,
//	             and the unweighted one never does.
//
// The objects are the SUPERSEDED ones the guards replaced, still in the store
// because objects are immutable and repointing deletes nothing. Nothing here
// says anything about the live corpus's verdicts.
func TestRuleDetectsThePreRepairDefects(t *testing.T) {
	st, _ := copiedStore(t)
	strHash := canonicalStrHash()

	for _, s := range litSites() {
		t.Run(s.propName, func(t *testing.T) {
			d, pi := litProp(t, st, s)
			p := &d.Props[pi]
			if s.binder >= len(p.Binders) || !isStrTy(strHash, &p.Binders[s.binder]) {
				t.Fatalf("%s binder %d is not Str; the positional map is stale", s.propName, s.binder)
			}
			weighted := mustSchedule(st, s.hash)
			plain := unweightedSchedule(s.hash)
			if weighted.lits == nil {
				t.Fatalf("%s draws no literal population; this site cannot measure the rule", s.propName)
			}

			// FIRING / NON-FIRING, evaluated against the pre-repair object
			// itself rather than proxied through live names: the live
			// `config-missing` body differs from the historical one, because it
			// holds a ref to `config-has-key` whose hash moved when its
			// properties were guarded. Content addressing propagates.
			if litEvalProp(t, st, s.hash, p, litWitnessEnv(t, strHash, p, s.binder, s.falseKey)) {
				t.Errorf("FIRING control failed: the pre-repair %s claim is TRUE at key %q, so there "+
					"is no defect here for any generator to find", s.propName, s.falseKey)
			}
			if !litEvalProp(t, st, s.hash, p, litWitnessEnv(t, strHash, p, s.binder, s.trueKey)) {
				t.Errorf("NON-FIRING control failed: the pre-repair %s claim is FALSE at ordinary key "+
					"%q, so it is broken everywhere and detecting it measures nothing", s.propName, s.trueKey)
			}

			hasDelim := false
			for _, l := range weighted.lits.lits {
				if l.Cmp(big.NewInt(litDelimiter)) == 0 {
					hasDelim = true
				}
			}
			if !hasDelim {
				t.Fatalf("%s: the population excludes %d, so a miss would be a fact about the "+
					"population rather than about the rule", s.propName, litDelimiter)
			}
			hitsW := litDelimiterHits(t, st, p, weighted, pi, s.binder)
			hitsP := litDelimiterHits(t, st, p, plain, pi, s.binder)
			t.Logf("POPULATION %s: %d literals, budget %d cases / %d fuel; binder %d holds %d in "+
				"%d/%d weighted cases and %d/%d unweighted",
				s.propName, len(weighted.lits.lits), propCases, propFuel, s.binder,
				litDelimiter, hitsW, propCases, hitsP, propCases)
			if hitsP != 0 {
				t.Errorf("the UNWEIGHTED stream already reaches %d in %d/%d cases; the baseline this "+
					"rests on has changed", litDelimiter, hitsP, propCases)
			}
			if hitsW == 0 {
				t.Errorf("the WEIGHTED stream never puts %d in binder %d; any detection below comes "+
					"from somewhere else", litDelimiter, s.binder)
			}

			if got := runProp(st, s.hash, p, s.propName, plain, pi, propCases, propFuel).Outcome; got != PropPassed {
				t.Fatalf("BASELINE broken: the pre-§4 stream reports %q, not `passed`; detection would "+
					"not be attributable to the rule", got)
			}
			rep := runProp(st, s.hash, p, s.propName, weighted, pi, propCases, propFuel)
			if rep.Outcome != PropFalsified {
				t.Fatalf("§4's rule does NOT detect %s within its own %d-case schedule (outcome %q, "+
					"%d indeterminate)", s.propName, propCases, rep.Outcome, rep.Indet)
			}
			at := litFirstFalsifyingCase(st, s.hash, p, s.propName, weighted, pi)
			if at < 0 {
				t.Fatalf("%s: falsified over %d cases but no single case reproduces it", s.propName, propCases)
			}
			if at > 0 && runProp(st, s.hash, p, s.propName, weighted, pi, at, propFuel).Outcome == PropFalsified {
				t.Errorf("%s: a budget of %d cases already falsifies, so the first hit is earlier", s.propName, at)
			}
			t.Logf("DETECTED %s: first falsifying case %d of %d (counterexample: %s)",
				s.propName, at, propCases, rep.Counter)
		})
	}
}

// litFirstFalsifyingCase asks runProp rather than re-running the cases beside
// it, because "what counts as a refutation" is that function's rule.
// Falsification is monotone in the case budget, so a binary search is exact;
// the caller's boundary assertion is what checks that.
func litFirstFalsifyingCase(st *Store, h string, p *Prop, name string, sch *genSchedule, pi int) int {
	within := func(cases int) bool {
		return runProp(st, h, p, name, sch, pi, cases, propFuel).Outcome == PropFalsified
	}
	if !within(propCases) {
		return -1
	}
	lo, hi := 1, propCases
	for lo < hi {
		mid := (lo + hi) / 2
		if within(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo - 1
}

func litDelimiterHits(t *testing.T, st *Store, p *Prop, sch *genSchedule, pi, binder int) int {
	t.Helper()
	hits := 0
	for c := 0; c < propCases; c++ {
		env, err := genPropCase(st, p, sch, pi, c)
		if err != nil {
			t.Fatalf("case %d: %v", c, err)
		}
		cps, ok := strCodepoints(env[binder])
		if !ok {
			t.Fatalf("case %d binder %d: not a Str spine", c, binder)
		}
		for _, cp := range cps {
			if cp == litDelimiter {
				hits++
				break
			}
		}
	}
	return hits
}

func litEvalProp(t *testing.T, st *Store, h string, p *Prop, env []Value) bool {
	t.Helper()
	ev := &evaluator{st: st, fuel: propFuel}
	out, err := ev.eval(env, h, &p.Body)
	if err != nil {
		t.Fatalf("evaluating the pre-repair property at %s: %v", shortHash(h), err)
	}
	if out.K != "bool" {
		t.Fatalf("the pre-repair property at %s did not evaluate to a Bool (%q)", shortHash(h), out.K)
	}
	return out.Bool
}

func litWitnessEnv(t *testing.T, strHash string, p *Prop, binder int, key string) []Value {
	t.Helper()
	env := make([]Value, len(p.Binders))
	for bi := range p.Binders {
		switch {
		case bi == binder:
			env[bi] = litStrValue(strHash, key)
		case isStrTy(strHash, &p.Binders[bi]):
			env[bi] = litStrValue(strHash, "")
		default:
			if p.Binders[bi].K != "data" {
				t.Fatalf("binder %d has kind %q; this witness builder handles Str and one datatype's "+
					"empty constructor only", bi, p.Binders[bi].K)
			}
			env[bi] = Value{K: "data", Hash: p.Binders[bi].Hash}
		}
	}
	return env
}

func litStrValue(strHash, s string) Value {
	v := Value{K: "data", Hash: strHash}
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		v = Value{K: "data", Hash: strHash, Idx: 1, Fields: []Value{
			{K: "int", Int: big.NewInt(int64(runes[i]))}, v,
		}}
	}
	return v
}

func TestLitStrValueRoundTrips(t *testing.T) {
	strHash := canonicalStrHash()
	for _, s := range []string{"", "a", "a=b"} {
		cps, ok := strCodepoints(litStrValue(strHash, s))
		if !ok {
			t.Fatalf("litStrValue(%q) is not a Str spine", s)
		}
		want := []rune(s)
		if len(cps) != len(want) {
			t.Fatalf("litStrValue(%q): %d codepoints, want %d", s, len(cps), len(want))
		}
		for i := range want {
			if cps[i] != int64(want[i]) {
				t.Fatalf("litStrValue(%q) codepoint %d = %d, want %d", s, i, cps[i], want[i])
			}
		}
	}
	if cps, _ := strCodepoints(litStrValue(strHash, "=")); len(cps) != 1 || cps[0] != litDelimiter {
		t.Fatalf(`"=" builds to %v, but the measurement uses %d`, cps, litDelimiter)
	}
}

// ---------------------------------------------------------------------------
// Blast radius.

func TestCorpusVerdictTransitions(t *testing.T) {
	st, _ := copiedStore(t)
	corpus := litCorpus(t, st)

	start := time.Now()
	counts := map[string]int{}
	var moved []string
	props, weightedObjects, movedStreams := 0, 0, 0

	for _, h := range corpus {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", shortHash(h), err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s metadata: %v", shortHash(h), err)
		}
		weighted := mustSchedule(st, h)
		plain := unweightedSchedule(h)
		if weighted.lits != nil {
			weightedObjects++
		}
		for pi := range d.Props {
			props++
			name := metaPropName(m, pi)
			// Did the INPUTS move? Without this, a zero verdict count is
			// equally consistent with the rule doing nothing at all, and the
			// two readings have completely different consequences.
			for c := 0; c < propCases; c++ {
				x, e1 := genPropCase(st, &d.Props[pi], plain, pi, c)
				y, e2 := genPropCase(st, &d.Props[pi], weighted, pi, c)
				if e1 == nil && e2 == nil && !sameEnv(st, x, y) {
					movedStreams++
					break
				}
			}
			b := runProp(st, h, &d.Props[pi], name, plain, pi, propCases, propFuel).Outcome
			a := runProp(st, h, &d.Props[pi], name, weighted, pi, propCases, propFuel).Outcome
			counts[fmt.Sprintf("%s -> %s", b, a)]++
			if a != b {
				moved = append(moved, fmt.Sprintf("%s.%s: %s -> %s", shortHash(h), name, b, a))
			}
		}
	}

	t.Logf("CORPUS SWEEP: %d objects (%d with a non-empty literal set), %d properties, "+
		"budget %d cases / %d fuel per arm, %s",
		len(corpus), weightedObjects, props, propCases, propFuel, time.Since(start).Round(time.Millisecond))
	t.Logf("  generated INPUTS moved for %d of %d properties", movedStreams, props)
	// THE INPUTS MUST MOVE SOMEWHERE. A zero-verdict-movement result is only
	// interesting if the rule actually changed what was generated; otherwise it
	// reports that a no-op is harmless.
	if movedStreams == 0 {
		t.Error("NOT ONE property's generated inputs moved, so the zero verdict movement below is a " +
			"fact about the rule being inert rather than about it being safe")
	}
	for _, k := range sortedCountKeys(counts) {
		t.Logf("  %-32s %d", k, counts[k])
	}
	for _, s := range moved {
		t.Logf("  MOVED %s", s)
	}
	if len(moved) == 0 {
		t.Logf("  NO VERDICT MOVED across the whole CORPUS")
	}
	if weightedObjects == 0 {
		t.Fatal("no CORPUS object drew a literal set, so the two arms are the same arm")
	}
	// CONTROL: the comparison must be able to observe a move where one exists.
	d, err := st.GetDef(preRepairFindsHead)
	if err != nil {
		t.Fatalf("control site: %v", err)
	}
	b := runProp(st, preRepairFindsHead, &d.Props[1], "finds-head", unweightedSchedule(preRepairFindsHead), 1, propCases, propFuel).Outcome
	a := runProp(st, preRepairFindsHead, &d.Props[1], "finds-head", mustSchedule(st, preRepairFindsHead), 1, propCases, propFuel).Outcome
	if !(b == PropPassed && a == PropFalsified) {
		t.Fatalf("the known-moving property reports %s -> %s; this sweep cannot observe a move", b, a)
	}
}

func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestUnrelatedPopulationIsUntouched is §4's empty-set guarantee at corpus
// scale. "CONSERVATIVE" is load-bearing: this is not "definitions that look
// unrelated to strings", it is the set where the rule provably cannot act,
// which makes the no-op result correspondingly stronger.
func TestUnrelatedPopulationIsUntouched(t *testing.T) {
	st, _ := copiedStore(t)
	corpus := litCorpus(t, st)

	var unrelated []string
	for _, h := range corpus {
		if mustSchedule(st, h).lits == nil {
			unrelated = append(unrelated, h)
		}
	}
	if len(unrelated) == 0 {
		t.Skip("no CORPUS object has a literal-free transitive closure")
	}

	streams, verdicts, cases := 0, 0, 0
	for _, h := range unrelated {
		d, _ := st.GetDef(h)
		m, _ := st.GetMeta(h)
		weighted, plain := mustSchedule(st, h), unweightedSchedule(h)
		for pi := range d.Props {
			for c := 0; c < propCases; c++ {
				cases++
				a, err := genPropCase(st, &d.Props[pi], plain, pi, c)
				if err != nil {
					continue
				}
				b, err := genPropCase(st, &d.Props[pi], weighted, pi, c)
				if err != nil {
					t.Errorf("%s prop %d case %d: the weighted arm failed where the plain one did not: %v",
						shortHash(h), pi, c, err)
					continue
				}
				if !sameEnv(st, a, b) {
					streams++
					if streams <= 5 {
						t.Errorf("UNRELATED %s prop %d case %d: the stream MOVED where the literal set "+
							"is empty", shortHash(h), pi, c)
					}
				}
			}
			name := metaPropName(m, pi)
			bo := runProp(st, h, &d.Props[pi], name, plain, pi, propCases, propFuel).Outcome
			ao := runProp(st, h, &d.Props[pi], name, weighted, pi, propCases, propFuel).Outcome
			if ao != bo {
				verdicts++
				t.Errorf("UNRELATED %s.%s: verdict moved %s -> %s", shortHash(h), name, bo, ao)
			}
		}
	}
	t.Logf("UNRELATED: %d of %d CORPUS objects have a literal-free transitive closure; %d generated "+
		"cases compared; %d stream differences, %d verdict moves",
		len(unrelated), len(corpus), cases, streams, verdicts)

	// TWO-WAY CONTROL: byte-identical streams are also what a blind comparison
	// reports, so the same comparison must find a difference where the set is
	// non-empty.
	d, _ := st.GetDef(preRepairFindsHead)
	differs := false
	for c := 0; c < propCases && !differs; c++ {
		a, _ := genPropCase(st, &d.Props[1], unweightedSchedule(preRepairFindsHead), 1, c)
		b, _ := genPropCase(st, &d.Props[1], mustSchedule(st, preRepairFindsHead), 1, c)
		differs = !sameEnv(st, a, b)
	}
	if !differs {
		t.Error("CONTROL FAILED: the stream comparison finds no difference at a site with a non-empty " +
			"literal set, so the byte-identical result above establishes nothing")
	}
}

func sameEnv(st *Store, a, b []Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if printValue(st, a[i]) != printValue(st, b[i]) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Diversity: the mutation campaign.

type mutantState string

const (
	mutantKilled    mutantState = "killed"
	mutantLived     mutantState = "survived"
	mutantNoVerdict mutantState = "indeterminate"
)

func mutantStateOf(killer string, indet bool) mutantState {
	switch {
	case killer != "":
		return mutantKilled
	case indet:
		return mutantNoVerdict
	default:
		return mutantLived
	}
}

// mutantKillerUnweighted is mutantKiller on the pre-§4 stream — the campaign's
// baseline arm, and a measurement device only.
func mutantKillerUnweighted(st *Store, m *Meta, mu mutantDef) (string, bool) {
	sch := unweightedSchedule(mu.hash)
	indeterminate := false
	for pi := range mu.def.Props {
		rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi), sch, pi, mutantCases, mutantFuel)
		if rep.Falsified() {
			return rep.Name, false
		}
		if rep.Indeterminate() {
			indeterminate = true
		}
	}
	return "", indeterminate
}

// TestMutationCampaignDiversity runs the complete campaign twice over ONE
// mutant population. A mutant the specification previously distinguished and no
// longer does is a measured loss of coverage, so any killed -> not-killed
// transition fails. The reverse is reported and does not fail.
func TestMutationCampaignDiversity(t *testing.T) {
	if os.Getenv("OATH_SPEC4_CAMPAIGN") != "1" {
		t.Skip("set OATH_SPEC4_CAMPAIGN=1 to run the full two-arm mutation campaign (~4 min); " +
			"skipped, so nothing here is currently evidence about diversity")
	}
	st, _ := copiedStore(t)
	corpus := litCorpus(t, st)

	// The budgets are the campaign's own, read from the engine rather than
	// restated: a hand-copied 60/500000 would stop being the campaign's budget
	// the day either constant moved.
	if mutantCases != 60 || mutantFuel != 500_000 {
		t.Fatalf("the campaign budget has moved to %d cases / %d fuel; the recorded figures are "+
			"against 60 / 500000 and must be re-measured", mutantCases, mutantFuel)
	}

	start := time.Now()
	counts := map[string]int{}
	var regressions []string
	total, weightedPops := 0, 0

	for _, h := range corpus {
		d, _ := st.GetDef(h)
		m, _ := st.GetMeta(h)
		mutants := genMutants(st, d)
		for _, mu := range mutants {
			total++
			// The engine's own staging step, not an optimisation: a mutant is
			// evaluated by hash, so without it `self` resolves to nothing and
			// every property reports no verdict.
			st.CacheDef(mu.hash, mu.def)
			before := mutantStateOf(mutantKillerUnweighted(st, m, mu))
			sch, err := newGenScheduleFor(st, mu.hash, mu.def)
			if err != nil {
				t.Fatalf("%s mutant %s schedule: %v", shortHash(h), mu.desc, err)
			}
			if sch.lits != nil {
				weightedPops++
			}
			after := mutantStateOf(mutantKiller(st, m, mu))
			counts[fmt.Sprintf("%s -> %s", before, after)]++
			if before == mutantKilled && after != mutantKilled {
				regressions = append(regressions, fmt.Sprintf("%s [%s] %s -> %s", shortHash(h), mu.desc, before, after))
			}
		}
	}

	t.Logf("MUTANTS: %d mutants over %d CORPUS objects (%d drew a non-empty literal set), "+
		"budget %d cases / %d fuel per arm, %s",
		total, len(corpus), weightedPops, mutantCases, mutantFuel, time.Since(start).Round(time.Millisecond))
	for _, k := range sortedCountKeys(counts) {
		t.Logf("  %-36s %d", k, counts[k])
	}

	killedBefore := 0
	for k, v := range counts {
		if strings.HasPrefix(k, string(mutantKilled)+" ->") {
			killedBefore += v
		}
	}
	if killedBefore == 0 {
		t.Fatal("the baseline campaign killed no mutant at all; a zero-regression result would be vacuous")
	}
	if weightedPops == 0 {
		t.Fatal("no mutant drew a literal set, so both arms ran the same generator")
	}
	for _, r := range regressions {
		t.Errorf("DIVERSITY REGRESSION: %s", r)
	}
	if len(regressions) == 0 {
		t.Logf("  NO mutant killed by the baseline survives the weighted arm (%d killed baseline)", killedBefore)
	}
}

// ---------------------------------------------------------------------------
// Stability: L(D) cannot change without D's hash changing.

// recordingBackend counts what a Store actually asks its backend for. It embeds
// the interface so an unlisted method keeps working, and intercepts only the
// three READ paths that could carry non-canonical state.
type recordingBackend struct {
	backend
	mu      sync.Mutex
	objects []string
	metas   []string
	names   int
}

func (r *recordingBackend) getObject(h string) ([]byte, bool, error) {
	r.mu.Lock()
	r.objects = append(r.objects, h)
	r.mu.Unlock()
	return r.backend.getObject(h)
}

func (r *recordingBackend) getMeta(h string) ([]byte, bool, error) {
	r.mu.Lock()
	r.metas = append(r.metas, h)
	r.mu.Unlock()
	return r.backend.getMeta(h)
}

func (r *recordingBackend) readNames() ([]byte, bool, error) {
	r.mu.Lock()
	r.names++
	r.mu.Unlock()
	return r.backend.readNames()
}

func (r *recordingBackend) reset() {
	r.mu.Lock()
	r.objects, r.metas, r.names = nil, nil, 0
	r.mu.Unlock()
}

// TestExtractionReadsOnlyCanonicalBytes audits the READ half of §4's stability
// claim.
//
// THE AUDIT IS OF THE READ LOG, NOT OF A SECOND TRAVERSAL. Re-deriving the
// expected closure here would be a hand-written copy of the thing under test,
// agreeing with itself by construction. Each read is instead checked against
// the canonical dependency edges of the objects already read: legitimate iff it
// is the owner, or some previously-read definition NAMES it via
// sortedDepHashes. That is "hash-addressed dependencies, recursively" stated as
// a property of the log.
func TestExtractionReadsOnlyCanonicalBytes(t *testing.T) {
	_, dir := copiedStore(t)
	fs, err := openFSBackend(dir)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	rec := &recordingBackend{backend: fs}
	// A FRESH store, so nothing is cached: an audit over a warm cache would
	// observe no reads and pass for the wrong reason.
	st, err := newStoreWithBackend(rec, dir)
	if err != nil {
		t.Fatalf("opening the recording store: %v", err)
	}

	owner := preRepairFindsHead
	rec.reset()
	w, err := strLiterals(st, owner)
	if err != nil {
		t.Fatalf("extraction: %v", err)
	}
	if w == nil {
		t.Fatal("the audited extraction produced no literal set; there is nothing to audit")
	}
	objects, metas, names := rec.objects, rec.metas, rec.names
	t.Logf("EXTRACTION READS at %s: %d objects, %d metadata, %d name-index reads",
		shortHash(owner), len(objects), len(metas), names)

	if names != 0 {
		t.Errorf("extraction read the NAME INDEX %d times; names are mutable, so L(D) could change "+
			"with no hash changing", names)
	}
	if len(metas) != 0 {
		t.Errorf("extraction read METADATA for %v; metadata is not part of identity", metas)
	}
	if len(objects) == 0 {
		t.Fatal("extraction read no objects; the recorder is not wired to the store under test")
	}
	reachable := map[string]bool{owner: true}
	for _, h := range objects {
		if !reachable[h] {
			t.Errorf("extraction read %s, which is neither the owner nor named by any object read "+
				"before it — that read is not a hash-addressed dependency", shortHash(h))
			continue
		}
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("re-reading %s: %v", shortHash(h), err)
		}
		for _, dep := range sortedDepHashes(d) {
			reachable[dep] = true
		}
	}

	// TWO-WAY CONTROL: the recorder must be able to SEE a name or metadata
	// read, or three zeros are a fact about the recorder.
	rec.reset()
	if _, ok := st.Resolve("Str"); !ok {
		t.Fatal("Str is not bound in the copied store")
	}
	if _, err := st.GetMeta(owner); err != nil {
		t.Fatalf("reading metadata: %v", err)
	}
	if rec.names == 0 && len(rec.metas) == 0 {
		t.Error("CONTROL FAILED: a deliberate name lookup and metadata read registered nothing")
	}
	t.Logf("CONTROL: a deliberate lookup registers %d name reads, %d metadata reads", rec.names, len(rec.metas))
}

func TestLiteralSetTracksIdentity(t *testing.T) {
	baseStore, baseHash, _ := litChain(t, "(+ n 61)", "(delimiter-relay n)")
	baseLits := litsOf(t, baseStore, baseHash)
	if strings.Join(baseLits, ",") != "61" {
		t.Fatalf("CHAIN baseline is %v, want [61]", baseLits)
	}

	// FIRING A: a literal in the OWNER.
	ownerStore, ownerHash, _ := litChain(t, "(+ n 61)", "(+ (delimiter-relay n) 7)")
	ownerLits := litsOf(t, ownerStore, ownerHash)
	if ownerHash == baseHash {
		t.Error("FIRING A: adding a literal to the owner did not change the owner's hash")
	}
	if strings.Join(ownerLits, ",") == strings.Join(baseLits, ",") {
		t.Errorf("FIRING A: adding literal 7 left the set at %v", ownerLits)
	}
	t.Logf("FIRING A (owner literal): hash %s -> %s, set %v -> %v",
		shortHash(baseHash), shortHash(ownerHash), baseLits, ownerLits)

	// FIRING B: a literal in a TRANSITIVE dependency. The owner's own source
	// text is character-for-character identical to the baseline's; both the set
	// and the owner's hash must move anyway, because content addressing
	// propagates a dependency's identity into its callers — which is the
	// mechanism §4's stability claim rests on.
	depStore, depHash, _ := litChain(t, "(+ n 62)", "(delimiter-relay n)")
	depLits := litsOf(t, depStore, depHash)
	if depHash == baseHash {
		t.Error("FIRING B: changing a literal two hops away did not change the owner's hash, so L(D) " +
			"CAN change without identity changing")
	}
	if strings.Join(depLits, ",") == strings.Join(baseLits, ",") {
		t.Errorf("FIRING B: changing the transitive literal left the set at %v", depLits)
	}
	t.Logf("FIRING B (transitive dependency literal): hash %s -> %s, set %v -> %v",
		shortHash(baseHash), shortHash(depHash), baseLits, depLits)

	// NON-FIRING: metadata only.
	m, err := baseStore.GetMeta(baseHash)
	if err != nil {
		t.Fatalf("reading CHAIN metadata: %v", err)
	}
	before := *m
	m.Name = m.Name + "-perturbed"
	m.PropNames = append(m.PropNames, "invented-name")
	if err := baseStore.SetMeta(baseHash, m); err != nil {
		t.Fatalf("writing CHAIN metadata: %v", err)
	}
	after, err := baseStore.GetMeta(baseHash)
	if err != nil {
		t.Fatalf("re-reading CHAIN metadata: %v", err)
	}
	if after.Name == before.Name && len(after.PropNames) == len(before.PropNames) {
		t.Fatal("NON-FIRING control is inert: the metadata perturbation did not take")
	}
	if lits := litsOf(t, baseStore, baseHash); strings.Join(lits, ",") != strings.Join(baseLits, ",") {
		t.Errorf("NON-FIRING: a metadata-only change moved the set from %v to %v", baseLits, lits)
	}
	if d, err := baseStore.GetDef(baseHash); err != nil || hashDef(d) != baseHash {
		t.Errorf("NON-FIRING: a metadata-only change moved the owner's identity (err %v)", err)
	}
	t.Logf("NON-FIRING (metadata only): set %v and hash %s both unchanged", baseLits, shortHash(baseHash))
}

// TestNameRepointCannotMoveTheLiteralSet is the name half of the non-firing
// control, and the direct reason §4 forbids identifying Str by name.
func TestNameRepointCannotMoveTheLiteralSet(t *testing.T) {
	clean, dir := copiedStore(t)
	owner := preRepairFindsHead
	wantLits := litsOf(t, clean, owner)
	if len(wantLits) == 0 {
		t.Fatal("the control owner has no literal set; a no-change result would be vacuous")
	}
	d, err := clean.GetDef(owner)
	if err != nil {
		t.Fatalf("reading the owner: %v", err)
	}
	wantStream := litStreamDigest(t, clean, d, mustSchedule(clean, owner), 1)

	namesPath := filepath.Join(dir, "names.json")
	raw, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatalf("reading names.json: %v", err)
	}
	var names map[string]string
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatalf("parsing names.json: %v", err)
	}
	other, ok := names["List"]
	if !ok || other == names["Str"] {
		t.Fatal("cannot find a distinct datatype to repoint Str at")
	}
	original := names["Str"]
	names["Str"] = other
	out, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		t.Fatalf("encoding names.json: %v", err)
	}
	if err := os.WriteFile(namesPath, out, 0o644); err != nil {
		t.Fatalf("writing names.json: %v", err)
	}

	be, err := openFSBackend(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	moved, err := newStoreWithBackend(be, dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	// CONTROL that the perturbation is real: a name-based lookup must now
	// return something else.
	if got := strTypeHash(moved); got == original {
		t.Fatal("the repoint did not take; the no-change result below establishes nothing")
	}
	if canonicalStrHash() != original {
		t.Fatal("the derived Str identity moved with the name; the rule is still name-dependent")
	}
	if lits := litsOf(t, moved, owner); strings.Join(lits, ",") != strings.Join(wantLits, ",") {
		t.Errorf("repointing the name Str moved the set from %v to %v", wantLits, lits)
	}
	d2, err := moved.GetDef(owner)
	if err != nil {
		t.Fatalf("reading the owner from the repointed store: %v", err)
	}
	if got := litStreamDigest(t, moved, d2, mustSchedule(moved, owner), 1); got != wantStream {
		t.Error("repointing the name Str changed the GENERATED STREAM; the rule is still a function " +
			"of mutable name state rather than of identity")
	}
	t.Logf("NON-FIRING (name repoint): Str rebound to %s, set %v and the %d-case stream both unchanged",
		shortHash(other), wantLits, propCases)
}

func litStreamDigest(t *testing.T, st *Store, d *Def, sch *genSchedule, pi int) string {
	t.Helper()
	var b strings.Builder
	for c := 0; c < propCases; c++ {
		env, err := genPropCase(st, &d.Props[pi], sch, pi, c)
		if err != nil {
			fmt.Fprintf(&b, "%d:err\n", c)
			continue
		}
		for _, v := range env {
			b.WriteString(printValue(st, v))
			b.WriteByte('\x1f')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestUnreadableClosureFails: an extraction that cannot read its whole closure
// must FAIL rather than return a smaller set. A truncated closure is a
// DIFFERENT distribution, not a partial one.
func TestUnreadableClosureFails(t *testing.T) {
	st, owner, dir := litChain(t, "(+ n 61)", "(delimiter-relay n)")
	if lits := litsOf(t, st, owner); len(lits) == 0 {
		t.Fatal("the CHAIN has no literals before damage; this control cannot distinguish failure " +
			"from an already-empty result")
	}
	src, ok := st.Resolve("delimiter-source")
	if !ok {
		t.Fatal("delimiter-source is not bound")
	}
	if err := os.Remove(filepath.Join(dir, "objects", src+".bin")); err != nil {
		t.Fatalf("removing the dependency: %v", err)
	}
	be, err := openFSBackend(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	damaged, err := newStoreWithBackend(be, dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}

	// PATH 1: entry by hash. The STORE refuses first — GetDef re-validates on
	// load and checkDef resolves dependencies transitively, so the owner's own
	// load fails before extraction walks anything. Stronger enforcement, but it
	// is not this code, and an earlier draft reported success while testing
	// exactly that prerequisite.
	if w, err := strLiterals(damaged, owner); err == nil {
		t.Fatalf("extraction SUCCEEDED over an unreadable closure and returned %v", w)
	} else if !strings.Contains(err.Error(), shortHash(src)) {
		t.Errorf("the failure does not name the unreadable dependency %s: %v", shortHash(src), err)
	} else {
		t.Logf("UNREADABLE, by hash: refused at load by the store — %v", err)
	}

	// PATH 2: entry with the def IN HAND — the mutation path, where no store
	// check sits in front and the traversal's own guard is the only one there
	// is. Reached by decoding the owner's raw canonical bytes.
	raw, err := os.ReadFile(filepath.Join(dir, "objects", owner+".bin"))
	if err != nil {
		t.Fatalf("reading the owner's canonical bytes: %v", err)
	}
	inHand, err := decodeDefRaw(raw)
	if err != nil {
		t.Fatalf("decoding the owner's canonical bytes: %v", err)
	}
	w2, err := strLiteralsOf(damaged, owner, inHand)
	if err == nil {
		t.Fatalf("extraction with the def in hand SUCCEEDED over an unreadable closure and returned %v", w2)
	}
	// Match this function's OWN message: the point of path 2 is to establish
	// WHICH code refused.
	if !strings.Contains(err.Error(), "an incomplete closure is a DIFFERENT distribution") {
		t.Errorf("the def-in-hand path failed, but not in the traversal's guard, so that guard is "+
			"still unwitnessed: %v", err)
	}
	t.Logf("UNREADABLE, def in hand: refused by the traversal — %v", err)
}
