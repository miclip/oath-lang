package main

// NORMATIVE RULE INSTRUMENTATION, and the conformance mutation score.
//
// The project already measures how much of an IMPLEMENTATION's behaviour a
// specification constrains: mutate the code, see whether the properties notice, and
// report the score as evidence rather than as a claim (SPEC §11). A blind
// implementation pointed out that the same question applies one level up, and that
// nobody had asked it:
//
//   implementation mutation score — how much of the implementation is constrained
//                                   by its specification
//   conformance mutation score    — how much of the NORMATIVE SPECIFICATION is
//                                   actually witnessed by the conformance vectors
//
// They are analogous and answer different questions. The second was measured by hand
// once, by disabling each normative rule in turn and re-running the vectors: 15 of 17
// rules could be deleted with the suite still passing. That measurement should not
// depend on someone repeating it by hand, so it is automated here — a coverage figure
// that is DERIVED, like every other verdict in this system, rather than asserted by
// whoever wrote the vectors.
//
// WHY THE SWITCH IS NOT AN ENVIRONMENT VARIABLE. This mechanism can turn off
// signature verification. Reading it from the environment would mean a deployment
// could be silently stripped of its guarantees by setting a variable — the scoring
// harness would have created a production vulnerability in order to measure test
// coverage. It is an unexported package variable, written only by the scorer, and
// nothing parses it from outside the process.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// normativeRule is one obligation the specification states and the kernel enforces.
type normativeRule struct {
	ID   string // stable identifier, "<section>/<slug>"
	Sec  string // the section that states it
	What string // what it obliges, in one line
	// Family is the fixture family that can execute this rule. The denominator of a
	// score must contain only rules the suite being measured is CAPABLE of witnessing;
	// counting a rule that needs integration fixtures against the envelope suite would
	// make a low score read as "the rule is untested" when it means "this family cannot
	// reach it". A low score must mean "this suite does not witness the rule" and
	// nothing else.
	Family string
}

const (
	familyEnvelope    = "envelope"    // reachable through vectors.jsonl
	familyIntegration = "integration" // needs a journal or a live store; no fixture family yet
)

// normativeRules is the population any conformance score is measured over. A rule
// belongs here when the spec states it as a MUST, the kernel has a single place that
// enforces it, and it can be disabled without changing unrelated semantics — the
// third condition matters, because a switch that perturbs two obligations at once
// cannot attribute a failure to either.
var normativeRules = []normativeRule{
	{"8.6.1/version-tag", "§8.6.1", "the first line must be exactly the format tag", familyEnvelope},
	{"8.6.1/lowercase-hex", "§8.6.1", "hashes and keys must be lowercase hex", familyEnvelope},
	{"8.6.1/canonical-decimal", "§8.6.1", "parent_rev must be canonical decimal", familyEnvelope},
	{"8.6.1/parent-consistency", "§8.6.1", "the parent sentinel and revision 0 hold iff each other", familyEnvelope},
	{"8.6.1/value-characters", "§8.6.1", "values must exclude LF, CR and control characters", familyEnvelope},
	{"8.6.1/field-order", "§8.6.1", "envelope members appear in a fixed order", familyEnvelope},
	{"8.6.1/field-count", "§8.6.1", "the envelope has exactly six members", familyEnvelope},
	{"8.6.1/trailing-lf", "§8.6.1", "the envelope ends with a newline", familyEnvelope},
	{"8.6.1/reencode", "§8.6.1", "parsed octets must re-encode to themselves", familyEnvelope},
	{"8.6.3/base64-dialect", "§8.6.3", "envelope_b64 is standard padded base64", familyEnvelope},
	{"8.6.3/base64-canonical", "§8.6.3", "envelope_b64 must re-encode to itself", familyEnvelope},
	{"8.6.4/principal-binding", "§8.6.4", "the signer must be the authenticated principal", familyEnvelope},
	{"8.6.4/artifact-recompute", "§8.6.4", "the signed artifact must equal the recomputed hash", familyEnvelope},
	{"8.6.4/name-match", "§8.6.4", "the signed name must be the name being published", familyEnvelope},
	{"8.6.4/parent-current", "§8.6.4", "the signed parent must be the current binding", familyEnvelope},
	{"8.6.4/revision-current", "§8.6.4", "the signed revision must be current (ABA)", familyEnvelope},
	{"8.6.4a/signature-valid", "§8.6.4a", "the signature must verify over the decoded octets", familyEnvelope},
	{"8.6.4a/small-order", "§8.6.4a", "a small-order author key must be rejected", familyEnvelope},

	// Reachable only with a journal or a live store. Listed so the inventory is
	// complete and the gap is visible, but EXCLUDED from the envelope suite's
	// denominator — see Family.
	{"8.6.2/revision-fold", "§8.6.2", "the legacy transition derivation folds a name's history", familyIntegration},
	{"8.6.2/no-op-revision", "§8.6.2", "a same-hash re-publication does not advance the revision", familyIntegration},
	{"8.2.1/member-order", "§8.2.1", "journal members appear in the normative order", familyIntegration},
	{"8.2.1/escaping", "§8.2.1", "journal strings use minimal escaping", familyIntegration},
	{"8.2.2/entry-digest", "§8.2.2", "an entry digest is SHA-256 over the canonical line", familyIntegration},
}

// rulesInFamily returns the denominator for one suite.
func rulesInFamily(fam string) []normativeRule {
	var out []normativeRule
	for _, r := range normativeRules {
		if r.Family == fam {
			out = append(out, r)
		}
	}
	return out
}

// ruleInventoryDigest identifies the POPULATION a score was measured over.
//
// Campaign identity taught this lesson once already (SPEC §11): a bare score whose
// denominator or method can change invisibly is a number that cannot be compared with
// itself. "2 of 17" means nothing without knowing which 17 — and a rule added to the
// inventory silently lowers every historical score while looking like a regression.
func ruleInventoryDigest(fam string) string {
	var b strings.Builder
	b.WriteString("oath-rule-inventory/1\n")
	b.WriteString("family=" + fam + "\n")
	rs := rulesInFamily(fam)
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	for _, r := range rs {
		b.WriteString("rule=" + r.ID + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

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

// ruleKnown guards against the score silently measuring a rule that no enforcement
// site consults — a typo in an ID would otherwise show up as an "unwitnessed" rule
// and be indistinguishable from a genuine coverage gap.
func ruleKnown(id string) bool {
	for _, r := range normativeRules {
		if r.ID == id {
			return true
		}
	}
	return false
}

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
