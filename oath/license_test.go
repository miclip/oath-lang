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
	base := licenseEvaluation{Policy: licensePolicyComposition, Engine: licenseEngine,
		Model: licenseModelVersion, ModelDigest: licenseModelDigest(),
		Inputs: []licenseInput{
			{Artifact: "11", Publication: "aa", Name: "a", License: "MIT"},
			{Artifact: "22", Publication: "bb", Name: "b", License: "Apache-2.0"}}}
	d := evaluationDigest(base)

	for _, tc := range []struct {
		name   string
		mutate func(*licenseEvaluation)
	}{
		{"engine", func(e *licenseEvaluation) { e.Engine = "other/1" }},
		{"model", func(e *licenseEvaluation) { e.Model = "other-lattice/2" }},
		{"policy", func(e *licenseEvaluation) { e.Policy = "redistribution" }},
		{"the model content", func(e *licenseEvaluation) { e.ModelDigest = "deadbeef" }},
		{"an input licence", func(e *licenseEvaluation) { e.Inputs[1].License = "GPL-3.0-only" }},
		{"an input artifact", func(e *licenseEvaluation) { e.Inputs[0].Artifact = "99" }},
		{"an input publication", func(e *licenseEvaluation) { e.Inputs[0].Publication = "zz" }},
	} {
		m := base
		m.Inputs = append([]licenseInput(nil), base.Inputs...)
		tc.mutate(&m)
		if evaluationDigest(m) == d {
			t.Fatalf("changing %s did not change the digest — a stale verdict would be undetectable", tc.name)
		}
	}

	// A NAME must NOT change the digest (§12.4 LICENSE-IDENTITY-ARTIFACT). Names are
	// discovery paths: renaming changes how the closure was found, never what was
	// evaluated, and §9 makes them mutable without changing identity.
	renamed := base
	renamed.Inputs = append([]licenseInput(nil), base.Inputs...)
	renamed.Inputs[0].Name = "service"
	renamed.Inputs[1].Name = "vendored-lib"
	if evaluationDigest(renamed) != d {
		t.Fatal("renaming a member changed the evaluation digest; identity is following the discovery path rather than the artifacts")
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

// TestUnchangedPublicationAssertsLicense pins the defect the first real signed
// publication exposed (SPEC §12.3 LICENSE-ASSERTED-BY-PUBLICATION). A publisher
// re-publishing IDENTICAL content with terms is relicensing; scoping the
// assertion to `applied` discarded it, and no vector could catch that because
// vectors hand assertions straight to the evaluator and never pass through a
// name transition.
func TestUnchangedPublicationAssertsLicense(t *testing.T) {
	env := pubEnvelope{Op: "put", Name: "n", Artifact: strings.Repeat("a", 64),
		Parent: strings.Repeat("a", 64), ParentRev: revOf(1),
		Author: strings.Repeat("b", 64), License: "Apache-2.0"}
	b64 := encodeEnvelopeB64(envelopeEncode(env))

	st := newMemStoreForTest(t)
	for _, e := range []LogEntry{
		{Name: "n", Kind: "func", Status: "accepted", Hash: "hA"},
		{Name: "n", Kind: "func", Status: "accepted", Hash: "hA",
			EnvelopeB64: b64, AuthorPubkey: env.Author, AuthorSig: "s"},
	} {
		le := e
		if err := st.AppendLog(&le); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}
	trs := nameTransitions(st.ReadLog(), "n")
	if trs[1].Transition != transitionUnchanged {
		t.Fatalf("setup: transition = %q, want unchanged", trs[1].Transition)
	}
	if got := assertedLicense(st, "n"); got != "Apache-2.0" {
		t.Fatalf("assertedLicense = %q, want Apache-2.0 — an unchanged publication "+
			"still asserts its author's terms", got)
	}
}

// TestEvaluationConsumesRawHashes pins the defect the first real multi-dependency
// evaluation exposed. explainPkg.Dependencies is a DISPLAY form ("append
// #78d23e27" — name plus a SHORT hash); passing it to evaluateLicensing made
// every dependency resolve to an empty name, report as unmodelled, and bind a
// display string into the §12.4 digest where the spec requires 64 lowercase hex.
// Single-dependency and dependency-free definitions hid it completely.
func TestEvaluationConsumesRawHashes(t *testing.T) {
	st := newMemStoreForTest(t)
	display := []string{"append #78d23e27"}  // explainPkg.Dependencies form
	raw := []string{strings.Repeat("7", 64)} // explainPkg.depHashes form

	// The RAW form yields a well-formed §12.4 triple.
	for _, in := range evaluateLicensing(st, "root", raw).Inputs {
		if in.Artifact != "" && len(in.Artifact) != 64 {
			t.Fatalf("raw hashes produced artifact %q, want 64 hex", in.Artifact)
		}
	}
	// The DISPLAY form does not, and this is the assertion that matters: it
	// documents WHY the two must not be confused. A display string binds into the
	// evaluation digest where §12.4 requires a 64-hex artifact hash, and every
	// dependency resolves to an empty name and reports as unmodelled — a verdict
	// that looks like honest UNSTATED and is actually a lookup failure.
	bad := false
	for _, in := range evaluateLicensing(st, "root", display).Inputs {
		if in.Artifact != "" && len(in.Artifact) != 64 {
			bad = true
		}
	}
	if !bad {
		t.Fatal("display-form dependencies produced valid artifact hashes — the two forms " +
			"are no longer distinguishable, so nothing stops a caller passing the wrong one")
	}
}
