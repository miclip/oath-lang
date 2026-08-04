package main

import (
	"fmt"
	"strings"
)

// Survivor adjudication (#130).
//
// EXECUTION MEASURES REACH. PROOF MEASURES EXCLUSION. The two answer different
// questions and must never be averaged into one confidence number:
//
//	generated mutation score  did generated executions distinguish the mutant?
//	survivor adjudication     is a surviving mutant incompatible with a PROVEN property?
//
// The defect this repairs is not the score, and it is not the measurement — it is
// the CLAIM attached to the measurement. `oath mutate` faithfully measures
// whether GENERATED EXECUTIONS witness a property distinguishing the mutant.
// That is a real and useful quantity; what over-scoped was reporting it as
// "spec strength", which is a claim about what the SPECIFICATION excludes. The
// two coincide only when the harness can exhibit the distinction.
//
// Stating it as "the tool answers a different question than it asks" would point
// the next repair at the wrong place — at the generator, or the case budget.
// Nothing about execution needed fixing. Measured on the committed corpus: 112 of 148
// scored definitions are `proven`, 15 of those score below 50%, and eight kill
// zero mutants while carrying proofs over all inputs. 220 of the corpus's 616
// survivors sit on proven definitions.
//
// The worked instance: `hex-nibble` is PROVEN, scores 11/53 (worst in the
// corpus), and its surviving mutant `48 → 49` is REFUTED by the prover with
// counterexample c = 48 in milliseconds. `genValue` draws Int from [-20,20], so
// no generated case ever reaches 48. The specification excludes the mutant; the
// harness could not show it.
//
// A proof-refuted survivor is STILL A REAL SURVIVOR. It is evidence about the
// executable harness — the generator did not exercise the distinguishing input —
// and that is worth keeping visible, which is why this never folds into
// killed/total. Keeping the score untouched also keeps `analyses/*.json`
// byte-identical across kernels (oathrs/conformance.sh:350) and SPEC §6.3
// unchanged, so this adds a reading, not a normative obligation.

// survivorVerdict is the disposition of one mutant that survived testing.
//
// There are deliberately only three. "the mutant satisfies every proven
// property" is NOT reported as equivalence: the proven set is a SUBSET of the
// property set mutation scoring runs, so silence from it is not a claim about
// the rest. A failed or inconclusive proof attempt must never become
// "equivalent" — that is the direction in which this instrument would lie.
type survivorVerdict struct {
	kind   string // "proof-refuted" | "equivalent" | "unadjudicated"
	prop   string // the proven property that refuted it, when kind == "proof-refuted"
	reason string // why it is unadjudicated, or the waiver's justification
}

// adjudicateSurvivor asks the prover whether a mutant that survived generated
// testing is nonetheless excluded by the specification.
//
// It appeals ONLY to properties recorded PROVEN for the ORIGINAL object, so the
// claim it supports is exactly:
//
//	the original provably satisfies P, and the mutant provably violates P,
//	therefore the mutant is semantically distinct — under the same property set,
//	the same kernel semantics, and the same solver budget as the original proof.
//
// Appealing to an unproven property would be weaker and would mean something
// else: a refutation there says the mutant violates a property we never
// established the ORIGINAL satisfies, which is not a statement about the mutant
// being distinct from it.
func adjudicateSurvivor(st *Store, origMeta *Meta, mutHash string, mutDef *Def) survivorVerdict {
	v, _ := adjudicateSurvivorErr(st, origMeta, mutHash, mutDef)
	return v
}

// adjudicateSurvivorErr is the same computation, reporting a MISSING SOLVER as
// an error rather than as a disposition. `--prove` asks a question; "no z3"
// is a failure to answer it, not an answer. The check lives here — at the one
// point that actually runs a solver — because every earlier place that could
// ask "will a solver be needed?" would be a second copy of this function's
// own control flow, and would be wrong the moment either changed.
func adjudicateSurvivorErr(st *Store, origMeta *Meta, mutHash string, mutDef *Def) (survivorVerdict, error) {
	if len(origMeta.ProvenProps) == 0 {
		return survivorVerdict{kind: "unadjudicated", reason: "no proven property to appeal to"}, nil
	}
	mm := mutantMeta(st, origMeta, mutDef, mutHash)
	// FAILS CLOSED ON NON-TOTAL MUTANTS, and this is the second half of the
	// uninterpreted-function hazard rather than a separate concern. Supplying the
	// classification is not enough: prove.go asserts a defining equation only for
	// a function classified TOTAL, so a mutant that DESTROYS termination is still
	// left uninterpreted — and z3 may then "refute" a property using arbitrary
	// values for it. The case is real and present: `str-take` recurses on
	// `(- n 1)`, and the catalogue's `- → +` mutant recurses on `(+ n 1)`, which
	// does not terminate. It survives generation (the divergence is outside the
	// draw) and was reported proof-refuted.
	//
	// There is no weaker honest answer here. A countermodel over an uninterpreted
	// function is not evidence about this mutant, so the only truthful verdict is
	// that nothing was established. Caught by review, after the first repair had
	// already fixed the total case and looked complete.
	if !isTotal(mm.Termination) {
		return survivorVerdict{kind: "unadjudicated",
			reason: fmt.Sprintf("mutant is not provably total (%s), so its defining equation cannot be asserted — any refutation would be against an uninterpreted function",
				orNone(mm.Termination))}, nil
	}
	// Past every early return: a solver WILL run below.
	if err := z3Available(); err != nil {
		return survivorVerdict{}, err
	}
	inconclusive := 0
	for _, pi := range origMeta.ProvenProps {
		if pi < 0 || pi >= len(mutDef.Props) {
			continue
		}
		// A FRESH context per attempt, as the prover itself requires: the script
		// must be a function of (goal, lemma set) alone, never of the order
		// candidates were translated in.
		c := newSmtCtx(st, mutDef, mutHash)
		// The prover reads metadata through metaOf, and the store holds none for
		// a mutant. Without this the mutant reaches it as an UNINTERPRETED
		// function — which is not a weaker proof but a CORRUPT one: with no
		// defining equation asserted, z3 may choose any values for the function,
		// so almost any property becomes "refutable". Measured before this line
		// existed, `str-take` reported 6 proof-refuted survivors and every one
		// was false. The defect looked like extra power and was its opposite.
		c.metaOverride = map[string]*Meta{mutHash: mm}
		loadLemmaLibrary(c, st, mutDef, mutHash, mm, pi)
		// FAILS CLOSED, and the default branch is the point. `proveOne` returns
		// proven | refuted | unknown | invalidated, and only an explicit "proven"
		// licenses the claim that this property does not separate the mutant.
		// Enumerating the failure statuses instead would mean a fifth status
		// added later silently counts as a clean result — which is precisely the
		// direction this instrument must never lie in. Caught by review: an
		// earlier version handled "unknown" by name and let "invalidated" (the
		// solver's wall-clock safety cap) fall through to "still holds".
		switch outcome := c.proveOne(mutDef, mutHash, mm, &mutDef.Props[pi], pi).status; outcome {
		case "refuted":
			return survivorVerdict{kind: "proof-refuted", prop: metaPropName(origMeta, pi)}, nil
		case "proven":
			// This property genuinely does not separate the mutant. Keep looking.
		default:
			inconclusive++
		}
	}
	// Honest outcomes, distinguished. Both are "unadjudicated", but a reader
	// deciding whether to spend effort here needs to know which: an inconclusive
	// attempt may be worth retrying, while "every proven property still holds"
	// says the proven set simply does not separate this mutant and never will.
	if inconclusive > 0 {
		return survivorVerdict{kind: "unadjudicated",
			reason: fmt.Sprintf("%d proven propert%s did not reach a verdict (timeout, resource cap, or untranslatable)",
				inconclusive, pluralize(inconclusive, "y", "ies"))}, nil
	}
	return survivorVerdict{kind: "unadjudicated", reason: "every proven property still holds on the mutant"}, nil
}

// mutantMeta builds the metadata the prover must see for a mutant under
// interrogation. Extracted so the two claims it makes are directly assertable
// rather than only observable through a proof outcome.
//
// It makes exactly two decisions, and each is load-bearing in a different way:
//
//  1. NO PROVEN PROPERTIES. The original's self-lemmas are assertions about the
//     ORIGINAL BODY, and the mutant occupies the same self-reference position,
//     so admitting them would axiomatize precisely the thing under test. With
//     them admitted the axiom set is contradictory and every refutation is
//     suppressed — measured: all 11 of `hex-nibble`'s refutations vanish.
//
//  2. TERMINATION RECLASSIFIED, never inherited. A mutation can destroy
//     structural descent — `(f (tail xs))` → `(f xs)` is in the catalog — and
//     inheriting "structural" would assert a defining equation for a function
//     that may not terminate. prove.go's gate exists because an inconsistent
//     axiom lets Z3 discharge ANY goal; inheriting would walk straight into it.
func mutantMeta(st *Store, origMeta *Meta, mutDef *Def, mutHash string) *Meta {
	return &Meta{
		Name:        origMeta.Name,
		PropNames:   origMeta.PropNames,
		ParamNames:  origMeta.ParamNames,
		TyVarNames:  origMeta.TyVarNames,
		CtorNames:   origMeta.CtorNames,
		Termination: terminationOf(st, mutDef, mutHash),
	}
}

// pluralize picks between two full word forms. The existing `plural` only
// appends "s", which cannot render "property"/"properties".
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renderAdjudication formats the survivor disposition block. Reported beneath
// the score and never merged into it.
func renderAdjudication(b *strings.Builder, verdicts []survivorVerdict, descs []string, adjudicated bool) {
	if len(verdicts) == 0 {
		return
	}
	byKind := map[string]int{}
	for _, v := range verdicts {
		byKind[v.kind]++
	}
	fmt.Fprintf(b, "%d survivor%s:\n", len(verdicts), plural(len(verdicts)))
	if !adjudicated {
		// Without the prover pass the only disposition on record is the waiver
		// set. Say so rather than implying the rest were examined and cleared.
		fmt.Fprintf(b, "  %3d equivalent (waived, justification on record)\n", byKind["equivalent"])
		fmt.Fprintf(b, "  %3d unadjudicated — run `oath mutate <name> --prove` to classify against proven properties\n",
			len(verdicts)-byKind["equivalent"])
		return
	}
	for _, k := range []string{"proof-refuted", "equivalent", "unadjudicated"} {
		if byKind[k] == 0 {
			continue
		}
		fmt.Fprintf(b, "  %3d %s\n", byKind[k], k)
	}
	// Per-survivor detail, so the summary can be checked rather than trusted.
	for i, v := range verdicts {
		switch v.kind {
		case "proof-refuted":
			fmt.Fprintf(b, "    ⊨ %-22s refuted by proven property %s\n", descs[i], v.prop)
		case "equivalent":
			fmt.Fprintf(b, "    ○ %-22s waived: %s\n", descs[i], v.reason)
		default:
			fmt.Fprintf(b, "    ? %-22s %s\n", descs[i], v.reason)
		}
	}
}
