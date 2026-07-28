package main

import "testing"

// Evidence without reproducible campaign identity is an assertion with numbers
// attached. The campaign hash is the identity of a MEASUREMENT — not of a result
// — so anything that can change the outcome must change the hash, or a score
// stays MEASURED while describing a campaign that no longer exists.
func TestCampaignHashCoversWhatChangesTheOutcome(t *testing.T) {
	base := campaignHash("artifact-a", nil)

	// Different artifact: a score is about ONE object.
	if campaignHash("artifact-b", nil) == base {
		t.Fatal("campaign hash ignores the artifact")
	}

	// Waivers count toward the score, so adding one changes the number without
	// changing the code — the case most likely to go unnoticed.
	withWaiver := campaignHash("artifact-a", []WaivedMutant{{Hash: "m1"}})
	if withWaiver == base {
		t.Fatal("campaign hash ignores the waiver set")
	}

	// The waiver SET is what matters, not the order it was recorded in.
	a := campaignHash("artifact-a", []WaivedMutant{{Hash: "m1"}, {Hash: "m2"}})
	b := campaignHash("artifact-a", []WaivedMutant{{Hash: "m2"}, {Hash: "m1"}})
	if a != b {
		t.Fatal("campaign hash depends on waiver ORDER — it must identify the set")
	}

	// Reproducible: the same description must hash the same, or an independent
	// implementation could never audit the evidence.
	if campaignHash("artifact-a", nil) != base {
		t.Fatal("campaign hash is not reproducible")
	}
}

// A score recorded under a superseded campaign must read STALE, never MEASURED.
// The failure mode this guards is silent: the number is real, it is simply
// evidence about a campaign that no longer exists.
func TestSupersededCampaignReadsStale(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn scored [] [(x Int)] Int (+ x 1)
		(prop adds [(x Int)] (== (scored x) (+ x 1))))`)
	if _, err := apiMutate(st, "scored"); err != nil {
		t.Fatal(err)
	}
	pkg, _ := buildExplain(st, "scored")
	if pkg.SpecStrength == nil || pkg.SpecStrength.State != "MEASURED" {
		t.Fatalf("fresh measurement not MEASURED: %+v", pkg.SpecStrength)
	}

	// Simulate a superseded campaign by rewriting the recorded identity.
	h, _ := st.Resolve("scored")
	m, _ := st.GetMeta(h)
	m.MutationCampaign = "some-older-campaign"
	if err := st.SetMeta(h, m); err != nil {
		t.Fatal(err)
	}
	pkg2, _ := buildExplain(st, "scored")
	if pkg2.SpecStrength.State != "STALE" {
		t.Fatalf("superseded campaign reported as %q, want STALE", pkg2.SpecStrength.State)
	}
	found := false
	for _, l := range pkg2.Limitations {
		if contains(l, "STALE") {
			found = true
		}
	}
	if !found {
		t.Fatalf("staleness not disclosed in limitations: %v", pkg2.Limitations)
	}
}
