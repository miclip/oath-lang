package main

import (
	"math/big"
	"sort"
	"testing"
)

// #162 STEP 1 — DOES WEIGHTED GENERATION DETECT, ON THE REAL SCHEDULE?
//
// #162's falsifier 1 is the only question these tests answer: weighted Str
// generation is REJECTED if it is run against the actual 200-case schedules
// and both `config.oath` defects are still missed. Uniform widening already
// failed exactly that bar — it made '=' reachable (461 draws in 200,000) and
// still took zero hits inside either first-200 window, so reach is not the
// measure and never was.
//
// WHAT IS UNDER TEST IS THE PRE-REPAIR PROPERTY, NOT THE LIVE ONE. #161 has
// already landed: `config-has-key.finds-head` and
// `config-missing.complete-config-reports-nothing` both carry a
// `(== (config-key key) key)` round-trip guard today, both are PROVEN, and
// neither is falsifiable by any generator. The two definitions measured here
// are the SUPERSEDED objects those repairs replaced, still in the store
// because objects are immutable and repointing deletes nothing. They are
// reconstructed pre-repair properties: the historical bytes, addressed by
// hash, carrying the unguarded claims as they stood when the generator missed
// them. Nothing here says anything about the live corpus's verdicts, and
// TestProtoWeightedLeavesTheGuardedPropertiesPassing is the control that keeps
// the two apart.
//
// A DETECTION RESULT ALONE WOULD NOT BE EVIDENCE. A generator that falsified
// everything would also "detect" these two, so each site carries four
// controls, and the measurement is only readable with all four:
//
//	BASELINE     unweighted, same 200 cases -> PASSES. Reproduces the miss
//	             this change exists to fix. Without it, detection could be an
//	             artifact of any change at all.
//	FIRING       the unguarded claim is genuinely FALSE at a key holding '=',
//	             so what the weighted run finds is a real counterexample.
//	NON-FIRING   the unguarded claim is TRUE at an ordinary key, so it is not
//	             false everywhere and finding it required aim, not luck.
//	POPULATION   the weighted stream actually puts '=' into the guarded binder,
//	             and the unweighted one never does.
const (
	// The delimiter config-key splits on. Spelled out in a MEASUREMENT of the
	// generator, never in a property: the properties derive their domain by
	// round-tripping through config-key, so the constant has exactly one
	// normative home and this is not it.
	protoDelimiter = 61

	// Pre-repair objects, addressed by hash rather than by name because no
	// name resolves to them any more — that is what makes them pre-repair.
	protoFindsHeadHash = "026c7502118d609a3106c199e9fb6ea854415f7a6c5f6b0c7aab719435e3bdb1"
	protoCompleteHash  = "8c9e095b0d72fe2dd3338908839c2f89353f2585efb362b8d298a6f562b779a6"
)

type protoSite struct {
	hash     string
	defName  string // the name whose LIVE object superseded this one
	propName string
	binder   int // the Str binder the defect lives in, positionally
	// FIRING and NON-FIRING witnesses for the UNGUARDED claim: the key that
	// breaks it and an ordinary key that does not. The remaining binders are
	// filled with empty values, which the controls' own outcomes validate.
	falseKey string
	trueKey  string
}

func protoSites() []protoSite {
	return []protoSite{
		{
			hash: protoFindsHeadHash, defName: "config-has-key", propName: "finds-head", binder: 1,
			falseKey: "a=b", trueKey: "a",
		},
		{
			hash: protoCompleteHash, defName: "config-missing", propName: "complete-config-reports-nothing", binder: 0,
			falseKey: "a=b", trueKey: "a",
		},
	}
}

// protoEvalProp evaluates a property body at a hand-built environment, through
// the same evaluator and fuel budget runProp uses.
//
// THE WITNESSES RUN AGAINST THE PRE-REPAIR OBJECT ITSELF, not against the live
// names. The first draft of this file proxied them through `config-has-key` /
// `config-missing` in source, on the assumption that #161 changed only the
// properties — and the canonical hash said otherwise: `config-missing`'s body
// moved too, because it holds a `ref` to `config-has-key`, whose hash changed
// when its properties were guarded. Nothing semantic changed (that definition's
// own body is byte-identical), but "nothing semantic changed" is a judgment,
// and evaluating the historical bytes directly needs no such judgment.
func protoEvalProp(t *testing.T, st *Store, h string, p *Prop, env []Value) bool {
	t.Helper()
	ev := &evaluator{st: st, fuel: propFuel}
	out, err := ev.eval(env, h, &p.Body)
	if err != nil {
		t.Fatalf("evaluating the pre-repair property at %s: %v", shortHash(h), err)
	}
	if out.K != "bool" {
		t.Fatalf("the pre-repair property at %s did not evaluate to a Bool (got %q); this control is "+
			"not measuring a proposition", shortHash(h), out.K)
	}
	return out.Bool
}

// protoWitnessEnv builds one case by hand: the named key in the binder under
// test, and the type's empty value everywhere else.
func protoWitnessEnv(t *testing.T, st *Store, strHash string, p *Prop, binder int, key string) []Value {
	t.Helper()
	env := make([]Value, len(p.Binders))
	for bi := range p.Binders {
		switch {
		case bi == binder:
			env[bi] = protoStrValue(strHash, key)
		case isStrTy(strHash, &p.Binders[bi]):
			env[bi] = protoStrValue(strHash, "")
		default:
			// The only other binder across these two properties is a
			// (List Str), whose empty value is constructor 0 with no fields.
			// Anything else means the site map has drifted.
			if p.Binders[bi].K != "data" {
				t.Fatalf("binder %d has kind %q; this witness builder handles Str and one datatype's "+
					"empty constructor only", bi, p.Binders[bi].K)
			}
			env[bi] = Value{K: "data", Hash: p.Binders[bi].Hash}
		}
	}
	return env
}

// protoStrValue builds a Str spine from a Go string: SCons of each codepoint,
// terminated by SNil. Constructor indices are Str's own — 0 is SNil, 1 is
// SCons — and TestProtoStrValueRoundTrips is what checks that against the
// store rather than against this comment.
func protoStrValue(strHash, s string) Value {
	v := Value{K: "data", Hash: strHash} // SNil
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		v = Value{K: "data", Hash: strHash, Idx: 1, Fields: []Value{
			{K: "int", Int: big.NewInt(int64(runes[i]))},
			v,
		}}
	}
	return v
}

// TestProtoStrValueRoundTrips verifies the witness builder before any control
// leans on it. A builder that silently produced SNil for every input would
// make every firing control below report the wrong thing while looking fine.
func TestProtoStrValueRoundTrips(t *testing.T) {
	st := openCommittedStore(t)
	strHash := strTypeHash(st)
	if strHash == "" {
		t.Fatal("Str is not bound in the store")
	}
	for _, s := range []string{"", "a", "a=b"} {
		cps, ok := strCodepoints(protoStrValue(strHash, s))
		if !ok {
			t.Fatalf("protoStrValue(%q) is not a Str spine", s)
		}
		want := []rune(s)
		if len(cps) != len(want) {
			t.Fatalf("protoStrValue(%q): %d codepoints, want %d", s, len(cps), len(want))
		}
		for i := range want {
			if cps[i] != int64(want[i]) {
				t.Fatalf("protoStrValue(%q) codepoint %d = %d, want %d", s, i, cps[i], want[i])
			}
		}
	}
	// The delimiter this file measures must be exactly what "=" builds to, or
	// the firing witnesses and the reach count are talking about two things.
	cps, _ := strCodepoints(protoStrValue(strHash, "="))
	if len(cps) != 1 || cps[0] != protoDelimiter {
		t.Fatalf(`"=" builds to %v, but the measurement uses %d`, cps, protoDelimiter)
	}
}

// protoProp resolves a property inside a SUPERSEDED object and proves it is
// superseded. Both halves matter: reading the object by hash is what makes
// this the pre-repair claim, and asserting that no live name reaches it is
// what stops the measurement silently retargeting the guarded property if a
// future repoint ever made these hashes live again.
func protoProp(t *testing.T, st *Store, s protoSite) (*Def, int) {
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
		t.Fatalf("%s still resolves to %s — this object is NOT pre-repair, and every claim "+
			"in this file about measuring the unguarded property is false", s.defName, shortHash(s.hash))
	}
	m, err := st.GetMeta(s.hash)
	if err != nil {
		t.Fatalf("%s: reading pre-repair metadata: %v", s.propName, err)
	}
	pi := -1
	for i := range d.Props {
		if metaPropName(m, i) == s.propName {
			pi = i
			break
		}
	}
	if pi < 0 {
		t.Fatalf("%s carries no property named %s", shortHash(s.hash), s.propName)
	}
	return d, pi
}

// firstFalsifyingCase returns the index of the first case at which the
// property refutes under `w`, or -1 if it survives all of `propCases`.
//
// It asks runPropWeighted rather than re-running the cases beside it, because
// "what counts as a refutation" is that function's rule — a refutation
// dominates an indeterminacy, and a lookalike loop here could report a
// different verdict than verification would for identical inputs. Falsification
// is monotone in the case budget (runProp stops at the first refutation and
// scans c ascending), so a binary search over the budget is exact; the two
// assertions the caller makes on the boundary are what check that.
func firstFalsifyingCase(st *Store, h string, p *Prop, name string, base uint64, pi int, w *strWeights) int {
	falsifiesWithin := func(cases int) bool {
		return runPropWeighted(st, h, p, name, base, pi, cases, propFuel, w).Outcome == PropFalsified
	}
	if !falsifiesWithin(propCases) {
		return -1
	}
	lo, hi := 1, propCases // falsifiesWithin(hi) is true; lo is the smallest candidate budget
	for lo < hi {
		mid := (lo + hi) / 2
		if falsifiesWithin(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo - 1 // smallest budget lo means the refutation is at case lo-1
}

func TestProtoWeightedGenerationDetectsTheConfigDefects(t *testing.T) {
	st := openCommittedStore(t)
	strHash := strTypeHash(st)
	if strHash == "" {
		t.Fatal("Str is not bound in the store")
	}

	for _, s := range protoSites() {
		t.Run(s.propName, func(t *testing.T) {
			d, pi := protoProp(t, st, s)
			p := &d.Props[pi]
			if s.binder >= len(p.Binders) || !isStrTy(strHash, &p.Binders[s.binder]) {
				t.Fatalf("%s binder %d is not Str; the positional map is stale", s.propName, s.binder)
			}
			base := caseSeedBase(s.hash)

			// --- FIRING and NON-FIRING controls ---------------------------
			if protoEvalProp(t, st, s.hash, p, protoWitnessEnv(t, st, strHash, p, s.binder, s.falseKey)) {
				t.Errorf("FIRING control failed: the pre-repair %s claim is TRUE at key %q, "+
					"so there is no defect here for any generator to find", s.propName, s.falseKey)
			}
			if !protoEvalProp(t, st, s.hash, p, protoWitnessEnv(t, st, strHash, p, s.binder, s.trueKey)) {
				t.Errorf("NON-FIRING control failed: the pre-repair %s claim is FALSE at ordinary key %q, "+
					"so it is broken everywhere and detecting it measures nothing about aim", s.propName, s.trueKey)
			}

			t.Logf("BUDGET %s: %d cases, %d fuel, seed base from %s",
				s.propName, propCases, propFuel, shortHash(s.hash))

			// --- BASELINE: today's generator misses it --------------------
			// Population-independent, so it runs once and gates everything
			// after it. Without it, detection below could be an artifact of
			// any change at all rather than of weighting.
			if plain := protoDelimiterHits(t, st, p, base, pi, s.binder, nil); plain != 0 {
				t.Errorf("the UNWEIGHTED generator already reaches %d in %d/%d cases; the baseline this "+
					"measurement rests on has changed", protoDelimiter, plain, propCases)
			}
			if got := runProp(st, s.hash, p, s.propName, base, pi, propCases, propFuel).Outcome; got != PropPassed {
				t.Fatalf("BASELINE broken: the unweighted run reports %q, but #162 measured this "+
					"property passing all %d cases. Detection below would not be attributable to weighting.",
					got, propCases)
			}

			// --- THE MEASUREMENT, under BOTH readings of "closure" --------
			// The pre-registered reading decides the verdict; the transitive
			// one is measured beside it so the result's sensitivity to that
			// ambiguity is a reported number rather than an implementation
			// accident. See gen_str_weight_proto.go for why they differ.
			narrow := func(b func(*Store, string) (*strWeights, error)) func(*Store, string) (*strWeights, error) {
				return func(st *Store, h string) (*strWeights, error) {
					w, err := b(st, h)
					return w.narrowedToBinders(), err
				}
			}
			for _, pop := range []struct {
				label      string
				build      func(*Store, string) (*strWeights, error)
				preregistd bool
			}{
				{"DIRECT (pre-registered: owner + sortedDepHashes)", strLiteralClosure, true},
				{"TRANSITIVE (secondary: owner + full reachable set)", strLiteralClosureTransitive, false},
				// The narrow reading of "Str binders" — nested Str values, such
				// as `(List Str)` elements, stay on the unweighted arm. Measured
				// because it moves the draw alignment of every later binder, so
				// a result holding under only one reading would be a fact about
				// alignment rather than about weighting.
				{"DIRECT, BINDER-ONLY (secondary: nested Str unweighted)", narrow(strLiteralClosure), false},
			} {
				w, err := pop.build(st, s.hash)
				if err != nil {
					t.Fatalf("%s literal closure: %v", pop.label, err)
				}
				if w == nil {
					t.Fatalf("%s / %s: the population is empty, so the weighting is off and this site "+
						"cannot measure the design", s.propName, pop.label)
				}
				hasDelim := false
				for _, l := range w.lits {
					if l == protoDelimiter {
						hasDelim = true
					}
				}
				if !hasDelim {
					t.Fatalf("%s / %s: the population %v excludes %d, so weighting cannot reach the "+
						"defect and a miss would be a fact about the population, not the design",
						s.propName, pop.label, w.lits, protoDelimiter)
				}
				hits := protoDelimiterHits(t, st, p, base, pi, s.binder, w)
				t.Logf("POPULATION %s / %s: %d literals %v; binder %d holds %d in %d/%d cases",
					s.propName, pop.label, len(w.lits), w.lits, s.binder, protoDelimiter, hits, propCases)
				if hits == 0 {
					t.Errorf("%s / %s: the weighted generator never puts %d in binder %d over %d cases; "+
						"any detection would be coming from somewhere else",
						s.propName, pop.label, protoDelimiter, s.binder, propCases)
				}

				rep := runPropWeighted(st, s.hash, p, s.propName, base, pi, propCases, propFuel, w)
				if rep.Outcome != PropFalsified {
					msg := "%s / %s: weighted generation does NOT detect the defect within its own %d-case " +
						"schedule (outcome %q, %d indeterminate). Reach without detection is what uniform " +
						"widening already bought."
					if pop.preregistd {
						t.Fatalf("FALSIFIER 1 FIRES — "+msg, s.propName, pop.label, propCases, rep.Outcome, rep.Indet)
					}
					// The secondary reading is REPORTED, never fatal: it is not
					// the criterion Step 1 pre-registered, and quietly failing
					// the run on it would be choosing the reading after seeing
					// the answer, which the pre-registration exists to prevent.
					t.Logf("NOT DETECTED (secondary reading, not the criterion) — "+msg,
						s.propName, pop.label, propCases, rep.Outcome, rep.Indet)
					continue
				}
				at := firstFalsifyingCase(st, s.hash, p, s.propName, base, pi, w)
				if at < 0 {
					t.Fatalf("%s / %s: falsified over %d cases but no single case reproduces it — the "+
						"search and the run disagree, so one of them is not measuring the schedule",
						s.propName, pop.label, propCases)
				}
				// Pin the boundary exactly, which is also what validates the
				// monotone search: the budget one short of it must NOT falsify.
				if at > 0 && runPropWeighted(st, s.hash, p, s.propName, base, pi, at, propFuel, w).Outcome == PropFalsified {
					t.Errorf("%s / %s: a budget of %d cases already falsifies, so the first hit is earlier than %d",
						s.propName, pop.label, at, at)
				}
				t.Logf("DETECTED %s / %s: first falsifying case %d of %d (counterexample: %s)",
					s.propName, pop.label, at, propCases, rep.Counter)
			}
		})
	}
}

// protoDelimiterHits counts, over the real schedule, how many cases put the
// delimiter into the binder the defect lives in.
func protoDelimiterHits(t *testing.T, st *Store, p *Prop, base uint64, pi, binder int, w *strWeights) int {
	t.Helper()
	hits := 0
	for c := 0; c < propCases; c++ {
		env, err := genPropCaseWeighted(st, p, base, pi, c, w)
		if err != nil {
			t.Fatalf("case %d: %v", c, err)
		}
		cps, ok := strCodepoints(env[binder])
		if !ok {
			t.Fatalf("case %d binder %d: not a Str spine", c, binder)
		}
		for _, cp := range cps {
			if cp == protoDelimiter {
				hits++
				break
			}
		}
	}
	return hits
}

// TestProtoWeightedLeavesTheGuardedPropertiesPassing is the non-firing control
// for the DESIGN rather than for one site: the weighted generator must not
// falsify the LIVE, guarded properties. If it did, either the #161 guard fails
// to cover the region weighting newly reaches, or weighting breaks properties
// indiscriminately and the detection above says nothing.
//
// It also pins the claim the sibling test leans on — that #161 changed the
// properties and not the bodies — by canonical hash rather than by reading the
// two definitions and judging them similar.
func TestProtoWeightedLeavesTheGuardedPropertiesPassing(t *testing.T) {
	st := openCommittedStore(t)

	for _, s := range protoSites() {
		t.Run(s.defName, func(t *testing.T) {
			oldDef, _ := protoProp(t, st, s)
			liveHash, _ := st.Resolve(s.defName)
			liveDef, err := st.GetDef(liveHash)
			if err != nil {
				t.Fatalf("reading live %s: %v", s.defName, err)
			}

			// The two objects must genuinely differ, or there is no repair for
			// this control to be on the far side of. By canonical hash: in a
			// content-addressed language structural equality IS hash equality,
			// and a hand-written comparison would check the fields its author
			// remembered.
			//
			// NOTE what is NOT asserted here. An earlier draft required the
			// BODIES to be equal, on the reasoning that #161 guarded properties
			// and nothing else. That is false as stated and the hash caught it:
			// `config-missing`'s body holds a `ref` to `config-has-key`, so
			// guarding the callee's properties moved the caller's body hash
			// too. Content addressing propagates through references, and a
			// "the body did not change" assertion is wrong for every caller of
			// a repaired definition. The sibling test needs no such claim now —
			// it evaluates the pre-repair object directly.
			if hashDef(oldDef) == hashDef(liveDef) {
				t.Fatalf("%s: the pre-repair and live objects are identical; there is no repair to be "+
					"on either side of", s.defName)
			}

			w, err := strLiteralClosure(st, liveHash)
			if err != nil {
				t.Fatalf("literal closure: %v", err)
			}
			if w == nil {
				t.Fatalf("%s: the live object's closure carries no literals, so this control runs the "+
					"unweighted generator and controls nothing", s.defName)
			}
			m, err := st.GetMeta(liveHash)
			if err != nil {
				t.Fatalf("reading live metadata: %v", err)
			}
			base := caseSeedBase(liveHash)
			for pi := range liveDef.Props {
				name := metaPropName(m, pi)
				rep := runPropWeighted(st, liveHash, &liveDef.Props[pi], name, base, pi, propCases, propFuel, w)
				switch rep.Outcome {
				case PropFalsified:
					t.Errorf("weighted generation FALSIFIES the live guarded %s.%s at %s — the guard does "+
						"not cover the region weighting reaches, or the weighting is unsound",
						s.defName, name, rep.Counter)
				case PropPassed:
					// survived, with a verdict on every case — the only
					// outcome this control may read as survival.
				default:
					// AN INDETERMINACY IS NOT SURVIVAL. A weighted input that
					// exhausts fuel or defeats the generator yields no verdict,
					// and accepting it here would let the control report "the
					// guard holds" when what actually happened is that nothing
					// was checked. #162's falsifier 2 — diversity regressing —
					// would arrive looking exactly like this.
					t.Errorf("weighted generation leaves the live guarded %s.%s WITHOUT A VERDICT "+
						"(outcome %q, %d of %d cases indeterminate: %s) — this control cannot read that "+
						"as the guard holding", s.defName, name, rep.Outcome, rep.Indet, propCases, rep.Err)
				}
			}
			t.Logf("%s: all %d live properties survive weighted generation over %d cases (population %v)",
				s.defName, len(liveDef.Props), propCases, w.lits)
		})
	}
}

// TestProtoWeightingIsOffWithoutLiterals checks the applicability clause: a
// definition whose closure carries no literals must see the EXISTING schedule,
// not a degenerate version of the new one. Byte-identical streams, with a
// two-way control — a site that DOES carry literals must produce a different
// stream, or "identical" would just mean the weighting never runs.
func TestProtoWeightingIsOffWithoutLiterals(t *testing.T) {
	st := openCommittedStore(t)

	sameStream := func(h string, d *Def, pi int, w *strWeights) bool {
		base := caseSeedBase(h)
		for c := 0; c < propCases; c++ {
			a, err := genPropCase(st, &d.Props[pi], base, pi, c)
			if err != nil {
				t.Fatalf("unweighted case %d: %v", c, err)
			}
			b, err := genPropCaseWeighted(st, &d.Props[pi], base, pi, c, w)
			if err != nil {
				t.Fatalf("weighted case %d: %v", c, err)
			}
			if len(a) != len(b) {
				return false
			}
			for i := range a {
				if printValue(st, a[i]) != printValue(st, b[i]) {
					return false
				}
			}
		}
		return true
	}

	// A definition with no literals anywhere in its closure. Found by asking
	// the store rather than by naming one: a hand-picked name would silently
	// stop being literal-free the day someone edited it.
	var emptyName, emptyHash string
	var emptyDef *Def
	names := make([]string, 0, len(st.Names()))
	for name := range st.Names() {
		names = append(names, name)
	}
	sort.Strings(names) // a deterministic pick, so the control names the same definition every run
	for _, name := range names {
		h, ok := st.Resolve(name)
		if !ok {
			continue
		}
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 || !propHasStrBinder(st, d) {
			continue
		}
		w, err := strLiteralClosure(st, h)
		if err != nil || w != nil {
			continue
		}
		emptyName, emptyHash, emptyDef = name, h, d
		break
	}
	if emptyDef == nil {
		t.Skip("no live definition has a Str-bindered property and a literal-free closure; " +
			"the applicability clause cannot be measured against the corpus")
	}
	w, err := strLiteralClosure(st, emptyHash)
	if err != nil {
		t.Fatalf("closure: %v", err)
	}
	if w != nil {
		t.Fatalf("%s: closure is non-nil after all", emptyName)
	}
	if !sameStream(emptyHash, emptyDef, 0, w) {
		t.Errorf("%s has a literal-free closure but its case stream MOVED; the existing schedule is "+
			"not preserved when the weighting is off", emptyName)
	}
	t.Logf("SCHEDULE PRESERVED: %s (literal-free closure) generates an identical %d-case stream", emptyName, propCases)

	// The two-way control. Same comparison, at a site that DOES carry
	// literals, must come out different — otherwise the check above passes
	// because the comparison cannot see a change.
	d, pi := protoProp(t, st, protoSites()[0])
	lw, err := strLiteralClosure(st, protoFindsHeadHash)
	if err != nil || lw == nil {
		t.Fatalf("the literal-carrying control site has no population (%v)", err)
	}
	if sameStream(protoFindsHeadHash, d, pi, lw) {
		t.Error("CONTROL FAILED: a site with a 17-literal population generates an unchanged stream, " +
			"so the stream comparison above cannot detect weighting at all")
	}
}

// propHasStrBinder reports whether the definition's first property binds a Str
// — the applicability clause is about Str binders, so a definition with none
// would satisfy it vacuously.
func propHasStrBinder(st *Store, d *Def) bool {
	strHash := strTypeHash(st)
	if strHash == "" || len(d.Props) == 0 {
		return false
	}
	for bi := range d.Props[0].Binders {
		if isStrTy(strHash, &d.Props[0].Binders[bi]) {
			return true
		}
	}
	return false
}

// TestProtoBinderOnlyActuallyNarrows is the control for the third reading
// measured in the detection table.
//
// It exists because that reading reported the SAME first falsifying case as the
// wide one at `finds-head`, which is exactly what a silently inert flag would
// also report. The two are genuinely different experiments — weighting the
// `(List Str)` elements consumes draws and moves everything after them — so the
// streams must diverge, and a coincidence at one case index is only readable as
// a coincidence once that is established.
func TestProtoBinderOnlyActuallyNarrows(t *testing.T) {
	st := openCommittedStore(t)
	s := protoSites()[0] // finds-head: a (List Str) binder ahead of the Str key
	d, pi := protoProp(t, st, s)
	p := &d.Props[pi]
	wide, err := strLiteralClosure(st, s.hash)
	if err != nil || wide == nil {
		t.Fatalf("population: %v", err)
	}
	narrow := wide.narrowedToBinders()
	if narrow.binderOnly == wide.binderOnly {
		t.Fatal("narrowedToBinders did not set the flag, so the third reading is the first one")
	}

	base := caseSeedBase(s.hash)
	diff := make([]int, len(p.Binders))
	for c := 0; c < propCases; c++ {
		a, err := genPropCaseWeighted(st, p, base, pi, c, wide)
		if err != nil {
			t.Fatalf("wide case %d: %v", c, err)
		}
		b, err := genPropCaseWeighted(st, p, base, pi, c, narrow)
		if err != nil {
			t.Fatalf("narrow case %d: %v", c, err)
		}
		for bi := range a {
			if printValue(st, a[bi]) != printValue(st, b[bi]) {
				diff[bi]++
			}
		}
	}
	t.Logf("%s: wide vs binder-only streams differ per binder over %d cases: %v", s.propName, propCases, diff)

	// The nested-Str binder MUST move: it is the one the narrow reading stops
	// weighting. If it does not, the flag is inert.
	if diff[0] == 0 {
		t.Errorf("the (List Str) binder is identical under both readings, so binder-only weighting is "+
			"a no-op and the third row of the detection table measures nothing new (per-binder diffs %v)", diff)
	}
	// And the Str binder must move too, or the readings differ only in a
	// binder the defect does not live in and the comparison is not probing
	// what the reviewer's concern was about — draw alignment reaching the key.
	if diff[s.binder] == 0 {
		t.Errorf("binder %d (the key the defect lives in) is identical under both readings, so the "+
			"draw-alignment concern is untested here (per-binder diffs %v)", s.binder, diff)
	}
}
