package main

import (
	"strings"
	"testing"
)

// TestNonOperatorKeyReservesPublishesLicensedAndIsThirdPartyVerifiable is the
// integrated def-of-done for #66's onboarding milestone. The reservation rules,
// signed publication, licensing and journal verification are each unit-tested
// elsewhere; the milestone's own bar is explicitly NOT "the rules are
// implemented" but the whole chain in one flow:
//
//	a key that has NEVER been named by the operator reserves a namespace,
//	publishes a LICENSED artifact into it, and a THIRD PARTY verifies the
//	whole chain from the journal alone — with an out-of-scope write refused
//	at the POLICY layer, not the authentication one.
//
// THIS IS A MECHANISM TEST, NOT THE SOCIAL WALKTHROUGH. It proves the code path
// admits a second principal and enforces the scope; it does not stand in for a
// real external contributor, whose walkthrough is deliberately not a scheduled
// step (docs/milestones.md). What it converts is docs/milestones.md's untested
// assertion that the finish line is "probably reachable today" into a checked
// fact about the mechanism.
func TestNonOperatorKeyReservesPublishesLicensedAndIsThirdPartyVerifiable(t *testing.T) {
	st := newMemStoreForTest(t)

	// No operator configuration: no policy.json, no authorized-keys allowlist.
	// The keys below are self-generated and were never named by anyone.
	if pol, err := LoadPolicy(st.Root); err != nil || pol != nil {
		t.Fatalf("this milestone is only meaningful with no operator policy (pol=%v err=%v)", pol, err)
	}
	aliceHex, alice := newKey(t)
	bobHex, bob := newKey(t)

	// 1. Alice reserves her namespace with a signed oath-reserve/1 statement.
	resOctets, resSig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, resOctets, resSig, aliceHex); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// 2. Alice publishes a LICENSED artifact into her namespace with a signed
	//    publication envelope (a bare put would be refused under the ownership
	//    freeze; the milestone requires the publication path).
	const src = `(defn alice/two [] [] Int 2
	  (prop is-two [] (== (alice/two) 2)))`
	h := artifactHashOf(t, st, src)
	env := pubEnvelope{Op: "put", Name: "alice/two", Artifact: h,
		Parent: noParent, ParentRev: firstRev(), Author: aliceHex, License: "Apache-2.0"}
	sig, err := envelopeSign(alice, env)
	if err != nil {
		t.Fatal(err)
	}
	reps, err := apiPutSigned(st, src, aliceHex, "",
		&pubAuth{Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: aliceHex})
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("licensed publication into own namespace failed: %v %+v", err, reps[0])
	}

	// 3. THE POLICY-LAYER NEGATIVE. Bob, a different self-generated key, is
	//    refused inside alice's namespace — and the refusal must cite namespace
	//    AUTHORITY, so it is the authorization gate rather than authentication
	//    (bob authenticates fine; his own signed publication is well-formed).
	const bsrc = `(defn alice/intruder [] [] Int 9
	  (prop is-nine [] (== (alice/intruder) 9)))`
	bh := artifactHashOf(t, st, bsrc)
	benv := pubEnvelope{Op: "put", Name: "alice/intruder", Artifact: bh,
		Parent: noParent, ParentRev: firstRev(), Author: bobHex, License: "Apache-2.0"}
	bsig, err := envelopeSign(bob, benv)
	if err != nil {
		t.Fatal(err)
	}
	reps, _ = apiPutSigned(st, bsrc, bobHex, "",
		&pubAuth{Bytes: string(envelopeEncode(benv)), Sig: bsig, Pubkey: bobHex})
	if reps[0].Status == "accepted" {
		t.Fatalf("bob published inside alice's reserved namespace")
	}
	if !strings.Contains(strings.ToLower(reps[0].Error), "reserved to key") {
		t.Errorf("refusal does not cite namespace authority, so it may be the WRONG gate "+
			"(an auth failure would leave the milestone unproven): %q", reps[0].Error)
	}

	// 4. THE THIRD PARTY, holding only the PERSISTED store, re-derives the whole
	//    chain. Reopened fresh from disk so nothing rides on the publisher's warm
	//    caches — GetDef would otherwise return a cached def without re-reading or
	//    hash-checking the stored object.
	third, err := OpenStore(st.Root)
	if err != nil {
		t.Fatalf("third party could not open the store: %v", err)
	}
	//    (a) ownership from the journal alone, not a stored table.
	holder, rev := reservationRev(third, "alice/*")
	if holder != aliceHex || rev.Sign() == 0 {
		t.Errorf("third party derived owner (%s, rev %s), want alice at rev>=1", shortHash(holder), rev)
	}
	//    (b) the journal itself verifies — the reservation and the signed
	//        publication are both in the tamper-evident chain.
	if err := third.VerifyLog(); err != nil {
		t.Fatalf("journal does not verify, so a third party cannot trust the chain: %v", err)
	}
	//    (c) DISCOVERY by meaning, not by name. A third party who does not know
	//        "alice/two" asks which proven definition states its law and gets it
	//        back — the discovery path the finish line requires, keyed on the
	//        property rather than a name it was never told.
	found, err := apiFindSpec(third, `(defn q [] [] Int 0
	  (prop is-two [] (== (q) 2)))`)
	if err != nil {
		t.Fatalf("discovery query failed: %v", err)
	}
	// A REAL match, not a near-neighbour. apiFindSpec lists signature-compatible
	// suggestions on a MISS, and alice/two would appear there too — so the name
	// alone does not prove discovery. A hash match prints the definition with a
	// "(tested as ...)"/"(proven as ...)" marker and no "no definition states"
	// line; assert that shape.
	if strings.Contains(found, "no definition states this law as written") ||
		!strings.Contains(found, `alice/two`) ||
		!(strings.Contains(found, "(tested as") || strings.Contains(found, "(proven as")) {
		t.Errorf("a third party could not DISCOVER the artifact by its law (got a "+
			"miss or only a signature suggestion):\n%s", found)
	}
	//    identity is the content hash (SPEC §1), so the name a third party
	//    followed resolves to the exact bytes alice published — no trust in the
	//    host. (The proof re-earns by hash too; that path is the prove tests', and
	//    running Z3 here would only add a toolchain dependency.)
	if got, ok := third.Resolve("alice/two"); !ok || got != h {
		t.Errorf("alice/two resolves to %s, want the published artifact %s", shortHash(got), shortHash(h))
	}
	//    (d) the ASSERTED license is derivable and correctly interpreted, so a
	//        consumer deciding whether it may ship gets the real terms. Policy is
	//        a constant, so assert the CONSUMED assertion instead: the input must
	//        carry Apache-2.0, and the derived grants must show its distinctive
	//        PATENT grant (triYes) — an unlicensed artifact would leave it
	//        unstated, so this fails if the envelope's License were ignored.
	ev := evaluateLicensing(third, "alice/two", nil)
	sawApache := false
	for _, in := range ev.Inputs {
		if in.License == "Apache-2.0" {
			sawApache = true
		}
	}
	if !sawApache {
		t.Errorf("the published Apache-2.0 assertion was not consumed; inputs=%+v", ev.Inputs)
	}
	if ev.Result.PatentGrant != triYes {
		t.Errorf("derived patent grant = %v, want triYes (Apache-2.0's distinctive grant); "+
			"the license was not interpreted", ev.Result.PatentGrant)
	}
}
