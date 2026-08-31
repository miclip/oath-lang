package main

import "testing"

// license-consumer demand 2: `oath license <file> --assert <SPDX>` previews the
// composition verdict a PROPOSED assertion would yield, before publishing. The core
// is evaluateLicensingSubject, which takes the subject's license explicitly instead
// of reading it from the store.

// The proposed subject license drives the verdict — it is NOT read from the store
// (the subject is unpublished and asserts nothing there).
func TestLicensePreviewUsesProposedSubjectLicense(t *testing.T) {
	st := newMemStoreForTest(t)
	reps, err := apiPut(st, `(defn thing [] [] Int 7)`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("put: %v %+v", err, reps[0])
	}
	h := reps[0].Hash

	// The published-path evaluation reads assertedLicense == "" (never published with
	// terms) and is UNSTATED.
	if pub := evaluateLicensing(st, "thing", nil); pub.Result.Commercial != triUnstated {
		t.Fatalf("an unpublished subject should evaluate UNSTATED, got %v", pub.Result.Commercial)
	}

	// The preview supplies Apache-2.0 as the PROPOSED term, and the verdict is the
	// model's grants for Apache — commercial/modify YES, share-alike NO.
	prev := evaluateLicensingSubject(st, "thing", h, "Apache-2.0", "", nil)
	if prev.Result.Commercial != triYes || prev.Result.Modify != triYes || prev.Result.ShareAlike != triNo {
		t.Fatalf("the proposed Apache-2.0 must drive the verdict: %+v", prev.Result)
	}
	if prev.Subject != h {
		t.Errorf("preview subject artifact = %q, want the elaborated hash %q", prev.Subject, h)
	}
}

// Dependency contagion still applies in a preview: one unlicensed dependency makes
// the whole composition UNSTATED, even when the subject proposes a permissive term.
func TestLicensePreviewContagionFromUnlicensedDep(t *testing.T) {
	st := newMemStoreForTest(t)
	dep, err := apiPut(st, `(defn dep [] [] Int 1)`, "t", "") // never published with terms
	if err != nil || dep[0].Status != "accepted" {
		t.Fatalf("dep put: %v %+v", err, dep[0])
	}
	app, err := apiPut(st, `(defn app [] [] Int 2)`, "t", "")
	if err != nil || app[0].Status != "accepted" {
		t.Fatalf("app put: %v %+v", err, app[0])
	}

	prev := evaluateLicensingSubject(st, "app", app[0].Hash, "Apache-2.0", "", []string{dep[0].Hash})
	if prev.Result.Commercial != triUnstated || prev.Result.Modify != triUnstated {
		t.Errorf("an unlicensed dependency must make the proposed composition UNSTATED: %+v", prev.Result)
	}
}

// A preview subject carries the empty publication it was given — never an existing
// one — so its digest is not bound to a publication that asserted different terms.
func TestLicensePreviewSubjectHasNoPublication(t *testing.T) {
	st := newMemStoreForTest(t)
	reps, err := apiPut(st, `(defn thing [] [] Int 7)`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("put: %v %+v", err, reps[0])
	}
	h := reps[0].Hash
	ev := evaluateLicensingSubject(st, "thing", h, "GPL-3.0-only", "", nil)
	for _, in := range ev.Inputs {
		if in.Artifact == h && in.Publication != "" {
			t.Errorf("a preview subject must carry the empty publication, got %q", in.Publication)
		}
	}
}

// A composition verdict must cover the TRANSITIVE closure: aay -> bee -> cee. A
// direct-only closure would drop cee, and a restrictive cee would be silently
// ignored. The subject itself is excluded (evaluated separately).
func TestLicensingClosureIsTransitive(t *testing.T) {
	st := newMemStoreForTest(t)
	if _, err := apiPut(st, `(defn cee [] [(n Int)] Int (+ n 1))`, "t", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := apiPut(st, `(defn bee [] [(n Int)] Int (cee n))`, "t", ""); err != nil {
		t.Fatal(err)
	}
	reps, err := apiPut(st, `(defn aay [] [(n Int)] Int (bee n))`, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	aayH := reps[0].Hash
	aay, _ := st.GetDef(aayH)
	ceeH, _ := st.Resolve("cee")
	beeH, _ := st.Resolve("bee")

	in := map[string]bool{}
	for _, h := range licensingClosure(st, aay) {
		in[h] = true
	}
	if !in[beeH] || !in[ceeH] {
		t.Errorf("closure must include the direct dep bee AND the transitive dep cee, got bee=%v cee=%v", in[beeH], in[ceeH])
	}
	if in[aayH] {
		t.Error("closure must exclude the subject itself")
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"Apache-2.0":        "Apache-2.0",          // simple: no quoting needed
		"MIT OR Apache-2.0": "'MIT OR Apache-2.0'", // spaces: must quote
		"":                  "''",
		"a'b":               `'a'\''b'`, // an embedded quote is escaped
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
