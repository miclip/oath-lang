package main

import (
	"strings"
	"testing"
)

// UNSTATED IS CONTAGIOUS. One unknown input makes the composition unknown, however many
// others granted. Deriving YES from missing data would turn absence of a prohibition
// into a grant, which is the silent overclaim this system refuses everywhere else.
func TestUnstatedIsContagious(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []tri
		want tri
	}{
		{"all yes", []tri{triYes, triYes, triYes}, triYes},
		{"one unstated poisons the result", []tri{triYes, triYes, triUnstated}, triUnstated},
		{"unstated first", []tri{triUnstated, triYes}, triUnstated},
		{"a prohibition binds the whole", []tri{triYes, triNo, triYes}, triNo},
		{"NO beats UNSTATED — a known prohibition is stronger than ignorance", []tri{triUnstated, triNo}, triNo},
	} {
		acc := tc.in[0]
		for _, v := range tc.in[1:] {
			acc = combine(acc, v)
		}
		if acc != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, acc, tc.want)
		}
	}
}

// A licence the model does not know must contribute NOTHING, not something optimistic.
// A missing entry is safe; a wrong entry is a confident falsehood.
func TestUnknownLicensesYieldUnstated(t *testing.T) {
	for _, expr := range []string{
		"MIT OR Apache-2.0",                    // compound: choosing a disjunct is the consumer's call
		"GPL-3.0 WITH Classpath-exception-2.0", // compound
		"NotARealLicense-1.0",                  // unknown identifier
		"",                                     // nothing recorded
		noLicense,                              // publisher explicitly asserted none
	} {
		g, reason := modelLookup(expr)
		if reason == "" {
			t.Fatalf("%q was interpreted by the model without explanation", expr)
		}
		if g.Commercial != triUnstated || g.Redistribute != triUnstated || g.ShareAlike != triUnstated {
			t.Fatalf("%q produced a grant despite not being modelled: %+v", expr, g)
		}
	}
	// A known identifier must still resolve, or the model is inert.
	g, reason := modelLookup("Apache-2.0")
	if reason != "" || g.Commercial != triYes || g.PatentGrant != triYes {
		t.Fatalf("Apache-2.0 did not resolve: %+v (%s)", g, reason)
	}
}

// The evaluation has an IDENTITY. Changing the engine, the model, the policy, or any
// consumed assertion must change the digest — otherwise altering the lattice next year
// would silently reinterpret every historical verdict, the defect campaign identity
// exists to prevent.
func TestEvaluationDigestBindsMethodAndInputs(t *testing.T) {
	base := licenseEvaluation{Policy: "composition", Engine: licenseEngine, Model: licenseModelVersion,
		Inputs: []licenseInput{{Name: "a", License: "MIT"}, {Name: "b", License: "Apache-2.0"}}}
	d := evaluationDigest(base)

	for _, tc := range []struct {
		name   string
		mutate func(*licenseEvaluation)
	}{
		{"engine", func(e *licenseEvaluation) { e.Engine = "other/1" }},
		{"model", func(e *licenseEvaluation) { e.Model = "other-lattice/2" }},
		{"policy", func(e *licenseEvaluation) { e.Policy = "redistribution" }},
		{"an input licence", func(e *licenseEvaluation) { e.Inputs[1].License = "GPL-3.0-only" }},
		{"an input name", func(e *licenseEvaluation) { e.Inputs[0].Name = "z" }},
	} {
		m := base
		m.Inputs = append([]licenseInput(nil), base.Inputs...)
		tc.mutate(&m)
		if evaluationDigest(m) == d {
			t.Fatalf("changing %s did not change the digest — a stale verdict would be undetectable", tc.name)
		}
	}

	// Input ORDER must not matter: the same assertions evaluated in a different order
	// are the same evaluation, so the digest sorts before hashing.
	rev := base
	rev.Inputs = []licenseInput{base.Inputs[1], base.Inputs[0]}
	if evaluationDigest(rev) != d {
		t.Fatal("input order changed the digest; the same evaluation would appear to be two")
	}
}

// The rendering must state that it is derived, name its model, and never say PROVEN —
// compatibility over a finite lattice is decided by evaluation, not proved over
// unbounded inputs, and reusing that word would overload the strongest claim the system
// makes.
func TestRenderingDoesNotClaimProof(t *testing.T) {
	ev := licenseEvaluation{Policy: "composition", Engine: licenseEngine, Model: licenseModelVersion,
		Inputs: []licenseInput{{Name: "x", License: "MIT"}}, Result: grants{Commercial: triYes}}
	ev.Digest = evaluationDigest(ev)
	out := ev.render()
	for _, want := range []string{"DERIVED", licenseEngine, licenseModelVersion, "not a legal opinion"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendering omits %q — fallibility must be visible before the result is", want)
		}
	}
	if strings.Contains(out, "PROVEN —") || strings.Contains(out, "Result: PROVEN") {
		t.Fatal("the rendering claims proof for a computed verdict")
	}
}
