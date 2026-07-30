//go:build harness

package main

// The conformance-scoring machinery, compiled ONLY under `-tags harness`.
//
// Everything here can weaken verification, which is why it is not in a normal build.
// See rules.go for the boundary this establishes.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// disabledRules is the set of rules currently switched off. A SET rather than a
// single id because identifying what ELSE rejects an input requires disabling two
// rules at once: the one a vector claims, and a candidate for the check that is
// actually catching it.
//
// Unexported and never read from the environment — see the file comment. Empty is the
// only state any real invocation ever sees.
var disabledRules = map[string]bool{}

// ruleOn reports whether a normative rule is currently enforced. Enforcement sites
// read this; production always gets true.
func ruleOn(id string) bool { return !disabledRules[id] }

// withRulesDisabled runs fn with exactly the named rules off, restoring the previous
// set afterwards even if fn panics — a mutated verifier is deliberately weaker and may
// reach paths the enforced one never does, and leaking a disabled rule into later
// measurements would corrupt every subsequent verdict.
func withRulesDisabled(ids []string, fn func()) {
	prev := disabledRules
	disabledRules = map[string]bool{}
	for k := range prev {
		disabledRules[k] = true
	}
	for _, id := range ids {
		disabledRules[id] = true
	}
	defer func() { disabledRules = prev }()
	fn()
}

// harnessNoopRule is consulted by NOTHING. The scorer asserts it comes back
// UNWITNESSED: if it ever reads as witnessed, the aggregation is crediting rules for
// failures they did not cause, and every other number in the report is suspect.
const harnessNoopRule = "harness/known-noop"

// harnessWitnessRule is a real rule known to be load-bearing. The scorer asserts it
// comes back WITNESSED. Together these two catch the failure that actually happened
// while building this: a polarity inversion reported every rule as unwitnessed, and
// the same mistake in the other direction would have silently inflated the score.
const harnessWitnessRule = "8.6.4a/signature-valid"

// conformanceReport is the machine-readable evidence §10.1 references.
//
// PINNED, for the reason campaign identity exists (SPEC §11): a bare score whose
// denominator or method can change invisibly is a number that cannot be compared with
// itself. Adding a rule to the inventory would silently lower every historical score
// while looking like a regression, and changing the mutation operator would change
// what the number means without changing the number.
type conformanceReport struct {
	Suite           string          `json:"suite"`
	Family          string          `json:"family"`
	InventoryDigest string          `json:"rule_inventory_digest"`
	FixtureDigest   string          `json:"fixture_digest"`
	RunnerVersion   string          `json:"runner_version"`
	MutationOp      string          `json:"mutation_operator"`
	RulesTotal      int             `json:"rules_total"`
	RulesWitnessed  int             `json:"rules_witnessed"`
	Rules           []reportedRule  `json:"rules"`
	VectorClaims    []reportedClaim `json:"vector_claims,omitempty"`
}

type reportedRule struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	What    string `json:"what"`
	Verdict string `json:"verdict"`
	Vectors int    `json:"failing_vectors,omitempty"`
	// Baseline and Disabled record the OUTCOME either side of the mutation, so a reader
	// sees the differential rather than only the verdict derived from it.
	Baseline string `json:"baseline,omitempty"`
	Disabled string `json:"disabled,omitempty"`
}

type reportedClaim struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Witnesses string `json:"witnesses"`
	Verdict   string `json:"verdict"`
	// SubsumedBy names the rule that rejects this input once the claimed one is
	// removed. Its presence turns AMBIGUOUS into a decision: write a more isolated
	// vector, or accept that only the composite guarantee is measurable.
	SubsumedBy string `json:"subsumed_by,omitempty"`
}

// mutationOperator names WHAT was mutated. Recorded because "2 of 17" under
// rule-disabling means something different from "2 of 17" under any other operator.
const mutationOperator = "disable-one-normative-rule/1"

// cmdConformanceScore disables each normative rule in turn, re-runs the vector suite,
// and reports whether anything noticed.
//
// A rule is WITNESSED when removing it makes some vector fail. UNWITNESSED does not
// mean the rule is wrong or the kernel untested — it means nothing in THIS suite would
// notice its removal, so the suite cannot claim to cover it.
func cmdConformanceScore(vectorPath string, jsonOut bool) {
	vs, err := loadVectors(vectorPath)
	if err != nil {
		fail(err)
	}
	if failures := runVectors(vs); len(failures) > 0 {
		fmt.Printf("BASELINE FAILED — %d vector(s) fail with every rule enabled:\n", len(failures))
		for _, f := range failures {
			fmt.Printf("  %s\n", f)
		}
		fail(fmt.Errorf("cannot measure coverage against a failing baseline"))
	}

	// HARNESS SELF-TEST, before any number is reported. The scorer is now evidence in
	// its own right, and a polarity inversion or an aggregation slip produces a
	// plausible-looking score rather than an obvious failure — which is exactly what
	// happened while building it. A rule known to be load-bearing must read WITNESSED,
	// and a rule nothing consults must read UNWITNESSED.
	if err := harnessSelfTest(vs); err != nil {
		fmt.Printf("HARNESS SELF-TEST FAILED: %v\n\n", err)
		fmt.Printf("The measurement mechanism is not behaving, so no score derived from it can\n")
		fmt.Printf("be trusted. Refusing to report one.\n")
		fail(fmt.Errorf("harness self-test failed"))
	}

	fam := familyEnvelope
	rules := rulesInFamily(fam)
	rep := conformanceReport{
		Suite: "signed-publication", Family: fam,
		InventoryDigest: ruleInventoryDigest(fam),
		FixtureDigest:   fileDigest(vectorPath),
		RunnerVersion:   kernelVersion,
		MutationOp:      mutationOperator,
		RulesTotal:      len(rules),
	}

	for _, r := range rules {
		var caught []string
		withRulesDisabled([]string{r.ID}, func() { caught = runVectors(vs) })
		verdict := obligationAmbiguous
		if len(caught) > 0 {
			verdict = obligationWitnessed
			rep.RulesWitnessed++
		} else {
			verdict = "UNWITNESSED"
		}
		rr := reportedRule{ID: r.ID, Section: r.Sec, What: r.What, Verdict: verdict,
			Vectors: len(caught), Baseline: "reject", Disabled: "reject"}
		if verdict == obligationWitnessed {
			rr.Disabled = "accept"
		}
		rep.Rules = append(rep.Rules, rr)
	}
	sort.Slice(rep.Rules, func(i, j int) bool { return rep.Rules[i].ID < rep.Rules[j].ID })

	for _, v := range vs {
		if out := witnessOutcome(v); out != "" && out != obligationWitnessed {
			c := reportedClaim{Kind: v.Kind, Label: v.Label, Witnesses: v.Witnesses, Verdict: out}
			if out == obligationAmbiguous {
				c.SubsumedBy = alternateRejector(v, v.Witnesses)
			}
			rep.VectorClaims = append(rep.VectorClaims, c)
		}
	}

	if jsonOut {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return
	}

	fmt.Printf("ENVELOPE CONFORMANCE: %d/%d obligations witnessed\n", rep.RulesWitnessed, rep.RulesTotal)
	fmt.Printf("  suite %s · operator %s · runner %s\n", rep.Suite, rep.MutationOp, rep.RunnerVersion)
	fmt.Printf("  inventory %s…  fixtures %s…\n\n", rep.InventoryDigest[:12], rep.FixtureDigest[:12])
	fmt.Printf("Each rule is disabled in turn and the suite re-run. WITNESSED means its\n")
	fmt.Printf("removal makes a vector fail. UNWITNESSED does not mean the rule is wrong —\n")
	fmt.Printf("it means nothing here would notice if a second implementation omitted it.\n\n")
	for _, r := range rep.Rules {
		fmt.Printf("  [%-11s] %-32s %s\n", r.Verdict, r.ID, r.What)
	}
	if len(rep.VectorClaims) > 0 {
		fmt.Printf("\nVECTORS NOT DEMONSTRATING THEIR DECLARED OBLIGATION (%d):\n", len(rep.VectorClaims))
		for _, c := range rep.VectorClaims {
			sub := c.SubsumedBy
			if sub == "" {
				sub = "(nothing else accounts for it)"
			}
			fmt.Printf("  [%s] %-44s claims %-26s subsumed by %s\n", c.Verdict, trunc(c.Label, 44), c.Witnesses, sub)
		}
		fmt.Printf("\nAMBIGUOUS means removing the named rule does not change the outcome, so some\n")
		fmt.Printf("other check decides it. The vector may be perfectly valid and still not be\n")
		fmt.Printf("creditable to the rule it names.\n")
	}
	if n := len(rulesInFamily(familyIntegration)); n > 0 {
		fmt.Printf("\n%d further normative rule(s) are EXCLUDED from this denominator: they need a\n", n)
		fmt.Printf("journal or a live store and cannot be reached by envelope vectors at all.\n")
		fmt.Printf("Counting them here would make a low score read as \"untested\" when it means\n")
		fmt.Printf("\"this family cannot witness it\".\n")
	}
}

func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// harnessSelfTest proves the measurement mechanism works before any score derived
// from it is reported.
func harnessSelfTest(vs []vectorRecord) error {
	var caught []string
	withRulesDisabled([]string{harnessWitnessRule}, func() { caught = runVectors(vs) })
	if len(caught) == 0 {
		return fmt.Errorf("%s is known to be load-bearing, but disabling it broke nothing — the harness is not detecting failures at all (a polarity inversion reports every rule as unwitnessed)", harnessWitnessRule)
	}
	withRulesDisabled([]string{harnessNoopRule}, func() { caught = runVectors(vs) })
	if len(caught) != 0 {
		return fmt.Errorf("%s is consulted by nothing, yet disabling it produced %d failure(s) — the aggregation is crediting rules for failures they did not cause", harnessNoopRule, len(caught))
	}
	return nil
}

// Obligation verdicts.
const (
	obligationWitnessed = "WITNESSED"
	// AMBIGUOUS: removing the named rule does not change the outcome, so something
	// else decides it. Not a synonym for broken — it means this vector cannot be
	// CREDITED to this rule, and a suite counting it would overstate its coverage.
	obligationAmbiguous = "AMBIGUOUS"
	// BASELINE-FAIL: the vector does not hold even with every rule enabled, so the
	// suite is broken and no coverage statement derived from it means anything.
	obligationBaselineFail = "BASELINE-FAIL"
)

// alternateRejector finds WHICH other rule rejects a vector whose claimed rule has
// been removed. That answer turns the report from a verdict into a work queue: an
// AMBIGUOUS rule with a named subsumer is a decision (write a more isolated vector, or
// accept that only the composite guarantee is measurable), while one with no named
// subsumer is a mystery worth investigating.
//
// Returns "" when nothing else accounts for it.
func alternateRejector(v vectorRecord, claimed string) string {
	for _, r := range normativeRules {
		if r.ID == claimed || r.Family != familyEnvelope {
			continue
		}
		var stillFails bool
		withRulesDisabled([]string{claimed, r.ID}, func() {
			stillFails = len(runVectors([]vectorRecord{v})) == 0
		})
		// With BOTH off the vector's expectation is violated -> this rule was the one
		// doing the rejecting once the claimed rule was gone.
		var violated bool
		withRulesDisabled([]string{claimed, r.ID}, func() {
			violated = len(runVectors([]vectorRecord{v})) > 0
		})
		_ = stillFails
		if violated {
			return r.ID
		}
	}
	return ""
}

// witnessOutcome measures ONE vector's claim: does it fail because of the rule it
// names, or for some other reason?
//
// The test is differential rather than observational. A negative vector that simply
// fails proves nothing — it may be malformed in several ways at once and be caught by
// whichever check runs first. So the rule it claims is switched OFF and the vector
// re-run:
//
//	fails with the rule on, PASSES with it off  -> WITNESSED   (the rule is load-bearing)
//	fails with the rule on, FAILS with it off   -> AMBIGUOUS   (something else rejects it)
//	passes with the rule on                     -> UNWITNESSED (it constrains nothing)
//
// AMBIGUOUS is not a synonym for broken. It means this vector cannot be credited to
// this rule, and a suite that counted it would overstate its own coverage — the exact
// failure §10.1 exists to prevent.
func witnessOutcome(v vectorRecord) string {
	if v.Witnesses == "" {
		return ""
	}
	// runVectors reports EXPECTATION VIOLATED, not "the input was rejected". A healthy
	// negative vector therefore produces no failure at baseline — getting this polarity
	// wrong reported every vector as unwitnessed, which was implausible enough to be
	// obvious, but the same mistake in the other direction would have silently inflated
	// the score instead.
	if len(runVectors([]vectorRecord{v})) > 0 {
		return obligationBaselineFail
	}
	violated := false
	withRulesDisabled([]string{v.Witnesses}, func() {
		violated = len(runVectors([]vectorRecord{v})) > 0
	})
	if violated {
		return obligationWitnessed
	}
	// The outcome did not change when the rule was removed, so this vector does not
	// depend on it. For a NEGATIVE vector that means some other check rejects the input
	// first — the "newline vector" failure, where a fixture is credited for being
	// rejected at all rather than for the reason it names.
	return obligationAmbiguous
}

// reportVectorClaims prints vectors that do not demonstrate the obligation they name.
func reportVectorClaims(vs []vectorRecord) {
	var bad []string
	for _, v := range vs {
		if out := witnessOutcome(v); out == obligationAmbiguous || out == obligationBaselineFail {
			bad = append(bad, fmt.Sprintf("%s %q claims to witness %s but is %s", v.Kind, v.Label, v.Witnesses, out))
		}
	}
	if len(bad) == 0 {
		return
	}
	fmt.Printf("VECTOR CLAIMS: %d record(s) do not demonstrate the obligation they name\n", len(bad))
	for _, b := range bad {
		fmt.Printf("  %s\n", b)
	}
	fmt.Println()
}

func init() {
	harnessCommands["conformance-score"] = func(rest []string) {
		path, jsonOut := "fixtures/envelope/vectors.jsonl", false
		for _, a := range rest {
			if a == "--json" {
				jsonOut = true
			} else {
				path = a
			}
		}
		cmdConformanceScore(path, jsonOut)
	}
}

var harnessCommands = map[string]func([]string){}
