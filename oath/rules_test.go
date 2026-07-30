package main

import "testing"

// The harness is now evidence in its own right, so it needs its own. A polarity
// inversion or an aggregation slip produces a plausible-looking SCORE rather than an
// obvious failure — which is what happened while building it, and what these two
// synthetic anchors exist to catch.
func TestHarnessSelfTest(t *testing.T) {
	vs, err := loadVectors("../fixtures/envelope/vectors.jsonl")
	if err != nil {
		t.Skip("vectors not present")
	}
	if err := harnessSelfTest(vs); err != nil {
		t.Fatalf("harness self-test failed, so no score it produces can be trusted: %v", err)
	}
}

// A rule nothing consults must never read as witnessed. If it does, the aggregation
// is attributing failures to whichever rule happened to be disabled.
func TestNoopRuleWitnessesNothing(t *testing.T) {
	vs, err := loadVectors("../fixtures/envelope/vectors.jsonl")
	if err != nil {
		t.Skip("vectors not present")
	}
	var caught []string
	withRulesDisabled([]string{harnessNoopRule}, func() { caught = runVectors(vs) })
	if len(caught) != 0 {
		t.Fatalf("disabling a rule consulted by nothing produced %d failure(s)", len(caught))
	}
	// And the no-op must not be in the DENOMINATOR, or it would depress every score
	// with an obligation that does not exist.
	if ruleKnown(harnessNoopRule) {
		t.Fatal("the harness no-op rule is in the normative inventory; it would count against the score")
	}
}

// The disable switch must restore itself, or one measurement leaks into the next and
// every subsequent verdict is wrong in a way nothing reports.
func TestRuleSwitchRestores(t *testing.T) {
	if len(disabledRules) != 0 {
		t.Fatal("rules are disabled outside a measurement")
	}
	withRulesDisabled([]string{"8.6.1/lowercase-hex"}, func() {
		if ruleOn("8.6.1/lowercase-hex") {
			t.Fatal("rule was not disabled inside the closure")
		}
	})
	if !ruleOn("8.6.1/lowercase-hex") {
		t.Fatal("rule stayed disabled after the closure returned")
	}
	// Restores even when the mutated verifier panics — a weaker verifier may reach
	// paths the enforced one never does.
	func() {
		defer func() { _ = recover() }()
		withRulesDisabled([]string{"8.6.1/field-count"}, func() { panic("boom") })
	}()
	if !ruleOn("8.6.1/field-count") {
		t.Fatal("a panic inside a measurement leaked a disabled rule into later ones")
	}
}

// Every rule a vector claims must exist in the inventory, or a typo would appear as a
// genuine coverage gap.
func TestVectorClaimsNameKnownRules(t *testing.T) {
	vs, err := loadVectors("../fixtures/envelope/vectors.jsonl")
	if err != nil {
		t.Skip("vectors not present")
	}
	for _, v := range vs {
		if v.Witnesses != "" && !ruleKnown(v.Witnesses) {
			t.Fatalf("vector %q claims unknown rule %q", v.Label, v.Witnesses)
		}
	}
}
