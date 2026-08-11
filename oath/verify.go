package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const propCases = 200
const propFuel = 2_000_000

// PropOutcome is the three-way result of checking one property.
//
// THE DISTINCTION IS THE POINT, AND IT IS THE ONE THIS PROJECT KEEPS NAMING:
// an implementation limit is not a semantic fact. A property that EVALUATES TO
// FALSE is refuted — a real defect, and the strongest thing the tester can say.
// A property whose cases could not be EVALUATED AT ALL — fuel exhausted, depth
// exceeded, an input the generator could not build — has produced no verdict
// whatsoever, and recording that as refutation asserts something about the
// program that nothing observed.
//
// The kernel already had this concept one layer up: §7.2 requires an aborted
// solver attempt be reported "environmentally inconclusive rather than as a
// divergence", because "no valid verdict exists" is a different claim from
// "not proven". Property testing had no such state, so every unevaluable case
// was funnelled into `falsified` by fiat. This is that same rule, applied to
// the tester rather than the prover.
type PropOutcome string

const (
	PropPassed        PropOutcome = "passed"
	PropFalsified     PropOutcome = "falsified"
	PropIndeterminate PropOutcome = "indeterminate"
)

// PropReport is the outcome of checking one property.
type PropReport struct {
	Name    string
	Outcome PropOutcome
	Passed  int    // cases that evaluated to true
	Indet   int    // cases that could not be evaluated at all
	Counter string // rendered counterexample inputs — ONLY when falsified
	Err     string // why no verdict was reached — ONLY when indeterminate
}

// Falsified reports whether the property was REFUTED — an evaluated boolean
// false. It is deliberately a method rather than a field: the old `Failed bool`
// was set for refutation AND for evaluation errors, and every consumer that
// read it inherited the conflation. A method cannot be assigned to, so no
// caller can quietly re-widen it.
func (r PropReport) Falsified() bool { return r.Outcome == PropFalsified }

// Indeterminate reports whether the tester reached no verdict.
func (r PropReport) Indeterminate() bool { return r.Outcome == PropIndeterminate }

// Established reports whether this property's cases all passed — the only
// outcome that may contribute to a `tested` guarantee.
func (r PropReport) Established() bool { return r.Outcome == PropPassed }

// Marker, Headline and Detail are the ONE rendering vocabulary for a property
// outcome. Four surfaces print these lines — `verify` (which is also the
// conformance transcript), `put`, `cross` and the MCP tools — and before this
// each re-derived the distinction from two booleans with its own spelling. A
// renderer that disagreed with the ladder about what "falsified" looks like
// would be invisible from inside either one, so the words live with the type
// and each surface supplies only its own indentation.
func (r PropReport) Marker() string {
	switch r.Outcome {
	case PropFalsified:
		return "✗"
	case PropIndeterminate:
		return "?"
	default:
		return "✓"
	}
}

func (r PropReport) Headline() string {
	switch r.Outcome {
	case PropFalsified:
		return fmt.Sprintf("FALSIFIED after %d cases", r.Passed)
	case PropIndeterminate:
		return fmt.Sprintf("INDETERMINATE (%d passed, %d unevaluable)", r.Passed, r.Indet)
	default:
		return fmt.Sprintf("passed %d cases", r.Passed)
	}
}

// Detail returns the labelled second line, if the outcome has one. A falsified
// property carries a COUNTEREXAMPLE — an input that refutes it. An
// indeterminate one carries a REASON and deliberately NO counterexample: the
// inputs it could not evaluate refute nothing, and printing them under that
// label is what made a fuel exhaustion read as a defect.
// A refutation ALWAYS prints its counterexample line, even when the rendered
// inputs are empty — a property with no binders is refuted by the empty
// binding, and that line is part of the committed conformance transcripts.
// Suppressing it when the text is empty would move a fixture for a reason
// unrelated to this change.
func (r PropReport) Detail() (label, text string, ok bool) {
	switch r.Outcome {
	case PropFalsified:
		return "counterexample", r.Counter, true
	case PropIndeterminate:
		return "no verdict", r.Err, r.Err != ""
	default:
		return "", "", false
	}
}

// verifyDef runs every property of a function definition and records the
// resulting guarantee in its metadata. The guarantee is honest by
// construction: `tested` only ever means "these exact deterministic cases
// passed", and a falsified property downgrades the definition loudly rather
// than hiding it.
// verifyReports runs every property deterministically and returns the reports
// WITHOUT touching the store. Read-only consumers (e.g. fixture generation)
// use this; verifyDef wraps it and persists the resulting guarantee.
func verifyReports(st *Store, h string) ([]PropReport, *Def, *Meta, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return nil, nil, nil, err
	}
	if d.K != "func" {
		return nil, nil, nil, fmt.Errorf("only function definitions have properties to verify")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return nil, nil, nil, err
	}
	// A schedule that failed to resolve is NOT an error here: §4.1 says the
	// affected cases are `no-verdict`, and the schedule carries the failure so
	// each case reports it individually. Returning the error would collapse a
	// per-case disposition into a whole-definition failure.
	sch, _ := newGenSchedule(st, h)
	var reports []PropReport
	for pi := range d.Props {
		name := fmt.Sprintf("prop%d", pi)
		if pi < len(m.PropNames) {
			name = m.PropNames[pi]
		}
		reports = append(reports, runProp(st, h, &d.Props[pi], name, sch, pi, propCases, propFuel))
	}
	return reports, d, m, nil
}

func verifyDef(st *Store, h string) ([]PropReport, error) {
	reports, d, mp, err := verifyReports(st, h)
	if err != nil {
		return nil, err
	}
	m := *mp

	prevLevel := m.Guarantee.Level
	g := guaranteeFromReports(d, reports)
	m.Guarantee = g
	allProven := len(d.Props) > 0 && len(m.ProvenProps) == len(d.Props)
	m.Guarantee.Level = resolveLevel(g.Level, prevLevel, allProven)
	if m.Guarantee.Level == "falsified" {
		// A refuted definition retains no proofs — leaving ProvenProps set
		// would be a self-contradictory record (falsified AND proven).
		m.ProvenProps = nil
	}
	// Keep the proven count consistent with the retained proof set, so a
	// partially-proven `tested` def (e.g. 3 of 5) still reports "3 proven"
	// instead of dropping to 0 on re-verify.
	if m.Guarantee.Level != "falsified" {
		m.Guarantee.Proven = len(m.ProvenProps)
	}
	if err := st.SetMeta(h, &m); err != nil {
		return nil, err
	}
	return reports, nil
}

// guaranteeFromReports maps property outcomes to a guarantee level. It is a
// PURE function of (does the def have properties, what did each report say) so
// every outcome can be asserted directly rather than inferred from a store
// round-trip — the shape this repo's gate discipline asks for.
//
// The ladder, in priority order:
//
//	any FALSIFIED        -> falsified, naming the refuted properties.
//	                        A refutation is the strongest thing the tester can
//	                        say and it dominates any indeterminacy elsewhere.
//	any INDETERMINATE    -> asserted. NOT `tested`: `tested` means "all
//	                        properties passed all cases", and a property whose
//	                        cases could not be evaluated has not passed them.
//	                        Recording it as `tested` would claim evidence that
//	                        does not exist; recording it as `falsified` would
//	                        claim a refutation that does not exist. `asserted`
//	                        — the property is stated, testing established
//	                        nothing — is exactly the available truth, and it
//	                        needs no new level and no stored-data migration.
//	all PASSED           -> tested.
//	no properties        -> asserted.
func guaranteeFromReports(d *Def, reports []PropReport) Guarantee {
	if len(d.Props) == 0 {
		return Guarantee{Level: "asserted"}
	}
	var falsified, indeterminate []string
	for _, r := range reports {
		switch r.Outcome {
		case PropFalsified:
			falsified = append(falsified, r.Name)
		case PropIndeterminate:
			indeterminate = append(indeterminate, r.Name)
		}
	}
	switch {
	case len(falsified) > 0:
		// The indeterminate names are carried ALONGSIDE the falsified ones, not
		// dropped. A refutation elsewhere does not turn an unevaluable property
		// into a checked one, and dropping the names here would let `explain`
		// fall through to reporting it as `tested` — reintroducing the exact
		// claim this change removes, on a definition that is already known bad.
		return Guarantee{Level: "falsified", Falsified: falsified, Indeterminate: indeterminate}
	case len(indeterminate) > 0:
		// `asserted`, but NAMING the properties that reached no verdict. The
		// level alone cannot distinguish this from a definition that swears
		// nothing, and a reader who cannot tell those apart is being told a
		// definition has no properties when it has properties nobody could
		// evaluate.
		return Guarantee{Level: "asserted", Indeterminate: indeterminate}
	default:
		return Guarantee{Level: "tested", Cases: propCases}
	}
}

// resolveLevel decides the level actually recorded, given the level the current
// run computed, the level the object already carried, and whether every
// property carries a standing SMT proof. Pure, so every combination can be
// asserted rather than inferred from a store round-trip.
//
// IT EXISTS TO PROTECT PROOFS FROM THE TESTER'S BUDGET. A proof quantifies over
// ALL inputs; a generated case that ran out of fuel is a fact about 2,000,000
// fuel units and says nothing about the program. So a `proven` definition whose
// properties all still carry proofs stays proven when a run comes back
// `tested` (nothing new was learned) or `asserted` (nothing was learned at
// all). Only a REFUTATION — an evaluated boolean false, which contradicts the
// proof outright and means one of the two is wrong — takes a proof away.
func resolveLevel(computed, prevLevel string, allProven bool) string {
	if computed == "falsified" {
		return "falsified"
	}
	if prevLevel == "proven" && allProven {
		return "proven"
	}
	return computed
}

// caseSeedBase derives a definition's case-seed base from its hash. It is the
// SOLE authority on that derivation: a hand-repeated copy drifts, and anything
// reading it would then describe a different draw than the verdict it checks.
func caseSeedBase(hash string) uint64 {
	seedB, _ := hex.DecodeString(hash[:16])
	return binary.BigEndian.Uint64(seedB)
}

// genPropCase produces the binder values for ONE case of a property, through
// the deterministic tester's own seed and size schedule.
//
// It is the SOLE authority on that schedule, and the reason it is a function
// rather than three matching loops is the one this repo keeps arriving at: a
// duplicate is correct exactly once, and nothing announces when it stops being.
// `runProp` binds what this returns, so anything asking "what does the tester
// actually generate?" — a cross-check claiming an identical input stream, a
// measurement of the generator's reach — must ASK here rather than reproduce
// the derivation. Reproducing it yields a population that looks like the
// tester's and silently stops being it the moment the schedule changes.
// genSchedule is everything one case's draws depend on beyond (pi, c): the
// seed base and, per SPEC §4, the literal set L(D) of the definition under
// test. Resolved ONCE per property run rather than per case.
//
// IT REPLACED A `base uint64` PARAMETER, and that is a consolidation rather
// than plumbing. Every caller derived that base as caseSeedBase(owner) and then
// passed the two independently; §4 adds a second thing derived from the same
// owner, so passing three values that must agree gives three ways to disagree.
// One constructor, one owner, no way to seed from one definition and weight
// from another.
type genSchedule struct {
	owner string      // the definition under test — §4's D
	base  uint64      // caseSeedBase(owner)
	lits  *strWeights // §4's L(D); nil when empty, which means NO extra draw

	// err records a closure that could not be resolved. §4 makes an
	// unresolvable dependency an ERROR rather than a skip — a truncated L(D) is
	// a different distribution, not a smaller one — and §4.1 gives the
	// disposition: every case is `no-verdict`. Carrying it HERE rather than
	// failing the whole run is what makes that true, because runProp already
	// turns a generator failure into exactly one no-verdict case.
	err error
}

// newGenSchedule resolves the schedule for a definition in the store.
func newGenSchedule(st *Store, owner string) (*genSchedule, error) {
	lits, err := strLiterals(st, owner)
	if err != nil {
		return &genSchedule{owner: owner, base: caseSeedBase(owner), err: err}, err
	}
	return &genSchedule{owner: owner, base: caseSeedBase(owner), lits: lits}, nil
}

// newGenScheduleFor resolves the schedule for a definition whose canonical
// bytes are in hand rather than in the store — §6.3 mutation scoring, where §4
// makes the mutant the definition under test.
func newGenScheduleFor(st *Store, owner string, d *Def) (*genSchedule, error) {
	lits, err := strLiteralsOf(st, owner, d)
	if err != nil {
		return &genSchedule{owner: owner, base: caseSeedBase(owner), err: err}, err
	}
	return &genSchedule{owner: owner, base: caseSeedBase(owner), lits: lits}, nil
}

func genPropCase(st *Store, p *Prop, sch *genSchedule, pi, c int) ([]Value, error) {
	if sch.err != nil {
		return nil, sch.err
	}
	r := &rng{s: sch.base ^ (uint64(pi) << 32) ^ uint64(c)*0xD1B54A32D192ED03, lits: sch.lits}
	size := c % 8
	env := make([]Value, 0, len(p.Binders))
	for bi := range p.Binders {
		v, err := genValue(st, &p.Binders[bi], size, r)
		if err != nil {
			return nil, err
		}
		env = append(env, v)
	}
	return env, nil
}

// runProp checks one property over `cases` generated cases.
//
// A REFUTATION DOMINATES AN INDETERMINACY, which is why an unevaluable case
// does NOT stop the run. If some case evaluates to false the property IS false,
// and that is a positive finding about the program; a case that ran out of fuel
// is only a fact about the budget. Stopping at the first indeterminate case
// would let a fuel exhaustion at case 3 hide a genuine counterexample at case
// 4, downgrading a real defect into "no verdict". Only a refutation stops the
// run, because after one there is nothing left to learn.
func runProp(st *Store, h string, p *Prop, name string, sch *genSchedule, pi int, cases int, fuel int64) PropReport {
	rep := PropReport{Name: name, Outcome: PropPassed}
	// The first reason no verdict was reached, kept so the report can say WHY
	// it is indeterminate. Later reasons are not collected: one is enough to
	// act on, and the count is reported separately.
	noVerdict := func(reason string) {
		rep.Indet++
		if rep.Err == "" {
			rep.Err = reason
		}
	}
	for c := 0; c < cases; c++ {
		env, err := genPropCase(st, p, sch, pi, c)
		if err != nil {
			// The generator could not build an input. No case was run, so
			// nothing was observed about the property.
			noVerdict(err.Error())
			continue
		}
		inputs := make([]string, len(env))
		for i, v := range env {
			inputs[i] = printValue(st, v)
		}

		ev := &evaluator{st: st, fuel: fuel}
		out, err := ev.eval(env, h, &p.Body)
		if err != nil {
			// Fuel exhausted, depth exceeded, or any other runtime error. The
			// property did not evaluate, so it was neither confirmed nor
			// refuted on this case.
			noVerdict(err.Error())
			continue
		}
		if out.K != "bool" {
			// Not a boolean verdict, so not a refutation. The checker should
			// make this unreachable; if it happens, "no verdict" is the honest
			// report rather than a refutation nothing observed.
			noVerdict("property did not evaluate to a bool")
			continue
		}
		if !out.Bool {
			// AN EVALUATED BOOLEAN FALSE — the only thing that refutes.
			rep.Outcome = PropFalsified
			rep.Counter = strings.Join(inputs, ", ")
			// Any no-verdict reason collected from an earlier case is
			// DISCARDED. The report is now a refutation, and Err means "why no
			// verdict was reached" — carrying both would emit a counterexample
			// AND an error on the same property, which is the conflation this
			// change exists to remove, reappearing inside a single record.
			rep.Err = ""
			return rep
		}
		rep.Passed++
	}
	if rep.Indet > 0 {
		rep.Outcome = PropIndeterminate
	}
	return rep
}
