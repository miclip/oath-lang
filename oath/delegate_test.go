package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

func signDel(t *testing.T, priv ed25519.PrivateKey, op, ns, subject string, rev int64, drev ...int64) ([]byte, string) {
	t.Helper()
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	d := int64(0)
	if len(drev) > 0 {
		d = drev[0]
	}
	env := delEnvelope{Op: op, Namespace: ns, Subject: subject, Authority: pub,
		AuthorityRev: big.NewInt(rev), DelegationRev: big.NewInt(d), Pubkey: pub}
	oct := delEncode(env)
	return oct, hex.EncodeToString(ed25519.Sign(priv, oct))
}

func appendDel(t *testing.T, st *Store, priv ed25519.PrivateKey, op, ns, subject string, rev int64) {
	t.Helper()
	oct, sig := signDel(t, priv, op, ns, subject, rev)
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	if err := st.AppendLog(&LogEntry{Author: pub, Name: ns, Kind: kindDelegate, Status: "accepted",
		EnvelopeB64: encodeEnvelopeB64(oct), AuthorPubkey: pub, AuthorSig: sig}); err != nil {
		t.Fatal(err)
	}
}

// THE POINT OF THE WHOLE MECHANISM: a delegate may publish, and the holder can
// take that back. Without revocation, automating publication would mean handing
// over the namespace permanently, since this version has no transfer.
func TestDelegateMayPublishAndRevocationTakesItBack(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)

	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	// Before delegation, CI is refused.
	reps, _ := apiPut(st, `(defn alice/a [] [] Int 1)`, ciHex, "")
	if reps[0].Status != "blocked" {
		t.Fatalf("CI bound a name before being delegated (status=%s)", reps[0].Status)
	}

	appendDel(t, st, alice, opDelegate, "alice/*", ciHex, 1)
	reps, err := apiPut(st, `(defn alice/b [] [] Int 2)`, ciHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("delegate could not publish: %v %+v", err, reps[0])
	}

	appendDel(t, st, alice, opRevoke, "alice/*", ciHex, 1)
	reps, _ = apiPut(st, `(defn alice/c [] [] Int 3)`, ciHex, "")
	if reps[0].Status != "blocked" {
		t.Fatalf("a REVOKED delegate still published (status=%s) — revocation is the property that makes delegation safe", reps[0].Status)
	}

	// The holder is unaffected throughout.
	reps, err = apiPut(st, `(defn alice/d [] [] Int 4)`, aliceHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("the holder was blocked by their own delegation record: %v %+v", err, reps[0])
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal verification broke on delegation entries: %v", err)
	}
}

// A delegate holds PERMISSION, never AUTHORITY. It must not be able to reserve,
// nor to delegate onward — otherwise a stolen release key could entrench itself
// and the revocation above would be worthless.
func TestDelegateHoldsNoAuthority(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, ci := newKey(t)

	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendDel(t, st, alice, opDelegate, "alice/*", ciHex, 1)

	// Cannot reserve a nested prefix.
	oct, sig = signRes(t, ci, "alice/sub/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, ciHex); err == nil {
		t.Error("a delegate reserved a prefix under the namespace it may only publish into")
	}
	// Cannot delegate onward: its own grant names itself as authority, which it is not.
	otherHex, _ := newKey(t)
	appendDel(t, st, ci, opDelegate, "alice/*", otherHex, 1)
	if delegates(st)["alice/*"][otherHex] {
		t.Error("a delegate granted publication rights onward — permission became authority")
	}
}

// A grant is refused unless the grantor IS the holder, and self-delegation is
// refused because it grants nothing and could not be meaningfully revoked.
func TestDelegationEnvelopeRules(t *testing.T) {
	_, alice := newKey(t)
	aliceHex := hex.EncodeToString(alice.Public().(ed25519.PublicKey))
	bobHex, _ := newKey(t)

	if err := (delEnvelope{Op: opDelegate, Namespace: "alice/*", Subject: bobHex,
		Authority: bobHex, AuthorityRev: big.NewInt(1), DelegationRev: big.NewInt(0), Pubkey: aliceHex}).validate(); err == nil {
		t.Error("accepted a grant whose authority is not the signer")
	}
	if err := (delEnvelope{Op: opDelegate, Namespace: "alice/*", Subject: aliceHex,
		Authority: aliceHex, AuthorityRev: big.NewInt(1), DelegationRev: big.NewInt(0), Pubkey: aliceHex}).validate(); err == nil {
		t.Error("accepted a self-delegation")
	}
	oct, _ := signDel(t, alice, opDelegate, "alice/*", bobHex, 1)
	if _, err := parseDelegateEnvelope(append(oct, []byte("extra=1\n")...)); err == nil {
		t.Error("accepted an envelope with an extra member")
	}
}

// REGRESSION: a revoked delegate must not keep the names it published.
//
// Shipped broken. Exact-name TOFU made the delegate the owner of everything it
// bound, and RES-EXACT-OWNER-PREVAILS then protected that ownership from the
// holder — so revocation stopped new names and recovered nothing, while the
// revoked key went on repointing what it already had. "Revocable" cannot be
// allowed to mean that.
//
// Retention is scoped to names that PREDATE the reservation, which is what
// RES-NO-CAPTURE was always for.
func TestRevokedDelegateLosesNamesItPublished(t *testing.T) {
	st := newMemStoreForTest(t)
	holderHex, holder := newKey(t)
	ciHex, _ := newKey(t)
	bobHex, _ := newKey(t)

	// Bob binds a name BEFORE the reservation exists.
	if reps, err := apiPut(st, `(defn zoo/pre [] [] Int 1)`, bobHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("setup: %v %+v", err, reps[0])
	}
	oct, sig := signRes(t, holder, "zoo/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, holderHex); err != nil {
		t.Fatal(err)
	}
	appendDel(t, st, holder, opDelegate, "zoo/*", ciHex, 1)
	if reps, err := apiPut(st, `(defn zoo/post [] [] Int 1)`, ciHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("delegate could not publish: %v %+v", err, reps[0])
	}
	appendDel(t, st, holder, opRevoke, "zoo/*", ciHex, 1)

	// The revoked key must not keep what it published under the reservation.
	reps, _ := apiPut(st, `(defn zoo/post [] [] Int 2)`, ciHex, "")
	if reps[0].Status != "blocked" {
		t.Errorf("a REVOKED delegate repointed a name it had published — revocation recovered nothing")
	}
	// And the holder must be able to take it over.
	reps, err := apiPut(st, `(defn zoo/post [] [] Int 3)`, holderHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Errorf("the holder could not bind a name in their own namespace: %v %+v", err, reps[0])
	}
	// Retention still holds for the name that predates the reservation.
	reps, err = apiPut(st, `(defn zoo/pre [] [] Int 2)`, bobHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Errorf("RES-NO-CAPTURE broke: a name predating the reservation was seized: %v %+v", err, reps[0])
	}
}

// AUTH-ACCEPTANCE-IS-THE-BOUNDARY, exercised end to end.
//
// The envelope here is PERFECT: correct format, correct signer, correct authority
// state, verifying signature. It confers nothing, because no registry accepted
// it. That is a stronger claim than "the delegate cannot publish" — it proves the
// protocol boundary is ACCEPTANCE, not possession of a signed statement, and it
// is the rule an implementation is most likely to get wrong by treating a valid
// signature as sufficient.
func TestRejectedSignedStatementIsAuthorityNeutral(t *testing.T) {
	st := newMemStoreForTest(t)
	holderHex, holder := newKey(t)
	ciHex, _ := newKey(t)

	oct, sig := signRes(t, holder, "n8/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, holderHex); err != nil {
		t.Fatal(err)
	}

	// A valid grant, journaled as a REFUSAL — what a registry records when it
	// declines a submission.
	doct, dsig := signDel(t, holder, opDelegate, "n8/*", ciHex, 1)
	if err := st.AppendLog(&LogEntry{Author: holderHex, Name: "n8/*", Kind: kindDelegate,
		Status: "rejected", EnvelopeB64: encodeEnvelopeB64(doct), AuthorPubkey: holderHex,
		AuthorSig: dsig}); err != nil {
		t.Fatal(err)
	}

	// The signature is genuinely valid — the point is that validity is not enough.
	env, err := parseDelegateEnvelope(doct)
	if err != nil {
		t.Fatalf("the test's own envelope is malformed: %v", err)
	}
	if err := delVerify(env, dsig); err != nil {
		t.Fatalf("the test's own signature does not verify, so it proves nothing: %v", err)
	}

	// 1. it grants no authority, and 2. replay ignores it
	if delegates(st)["n8/*"][ciHex] {
		t.Error("a REJECTED entry granted authority — replay counted a statement no registry accepted")
	}
	// 3. explain reports no active delegation
	if reps, err := apiPut(st, `(defn n8/x [] [] Int 1)`, holderHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("setup: %v %+v", err, reps[0])
	}
	pkg, err := buildExplain(st, "n8/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Provenance.NamespaceDelegates) != 0 {
		t.Errorf("explain reported %v as delegates from a rejected statement", pkg.Provenance.NamespaceDelegates)
	}
	// 4. publication using that key is refused
	reps, _ := apiPut(st, `(defn n8/y [] [] Int 2)`, ciHex, "")
	if reps[0].Status != "blocked" {
		t.Error("the subject of a REJECTED grant published — possessing a signed envelope is not the same as it having taken effect")
	}
	// And the journal is still intact with the refusal recorded in it.
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal verification broke on a rejected delegation entry: %v", err)
	}
}

// A refused delegation must be PRESERVED, not discarded. "The registry preserves
// rejected signed intent while deriving no authority from it" is one claim with
// two halves, and the first half was missing: apiDelegate returned an error and
// appended nothing, so the fact that someone tried and was refused vanished.
//
// That is the record an incident review needs, and a discarding implementation
// destroys it while looking correct — authority is right, history is gone.
func TestRefusedDelegationIsPreservedInTheJournal(t *testing.T) {
	st := newMemStoreForTest(t)
	holderHex, holder := newKey(t)
	ciHex, _ := newKey(t)

	oct, sig := signRes(t, holder, "p1/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, holderHex); err != nil {
		t.Fatal(err)
	}
	doct, dsig := signDel(t, holder, opDelegate, "p1/*", ciHex, 1)
	if _, err := apiDelegate(st, doct, dsig, holderHex); err != nil {
		t.Fatal(err)
	}
	before := len(st.ReadLog())

	// Re-granting an active delegate: a real refusal, on an authenticated statement.
	if _, err := apiDelegate(st, doct, dsig, holderHex); err == nil {
		t.Fatal("a duplicate grant was accepted")
	}
	entries := st.ReadLog()
	if len(entries) != before+1 {
		t.Fatalf("the refusal was DISCARDED: %d entries before, %d after", before, len(entries))
	}
	last := entries[len(entries)-1]
	if last.Status != "rejected" || last.Kind != kindDelegate {
		t.Errorf("refusal journalled as kind=%q status=%q", last.Kind, last.Status)
	}
	if last.EnvelopeB64 == "" || last.AuthorSig == "" {
		t.Error("the refusal record does not carry the statement that was refused")
	}
	if last.Error == "" {
		t.Error("the refusal record does not say why it was refused")
	}
	// And it grants nothing: the delegate set is unchanged, one entry only.
	if n := len(delegates(st)["p1/*"]); n != 1 {
		t.Errorf("delegate set is %d after a refused duplicate; the refusal affected authority", n)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal verification broke on a preserved refusal: %v", err)
	}
}

// THE JOURNALING BOUNDARY, pinned in both directions.
//
// A refusal is preserved once there is a trustworthy signed assertion to
// preserve, and not before. Malformed bytes and bad signatures assert nothing, so
// journaling them would record noise as history and hand anyone a way to write
// into the journal by submitting garbage.
//
// The relay case is subtler and is deliberately NOT journalled: the signature is
// valid but the caller is not the signer, so it is somebody else's statement being
// replayed. Recording it would let any observer of a valid envelope append to the
// journal at will, and it is not the signer's attempt in any case.
func TestDelegationJournalingBoundary(t *testing.T) {
	mk := func(t *testing.T) (*Store, string, ed25519.PrivateKey, string) {
		st := newMemStoreForTest(t)
		hHex, h := newKey(t)
		cHex, _ := newKey(t)
		oct, sig := signRes(t, h, "b1/*", noAuthority, 0)
		if _, err := apiReserve(st, oct, sig, hHex); err != nil {
			t.Fatal(err)
		}
		return st, hHex, h, cHex
	}
	count := func(st *Store) int {
		n := 0
		for _, e := range st.ReadLog() {
			if e.Kind == kindDelegate {
				n++
			}
		}
		return n
	}

	for _, c := range []struct {
		name       string
		journalled bool
		run        func(st *Store, hHex string, h ed25519.PrivateKey, cHex string)
	}{
		{"valid signature + stale revision", true, func(st *Store, hHex string, h ed25519.PrivateKey, cHex string) {
			o, s := signDel(t, h, opDelegate, "b1/*", cHex, 99)
			_, _ = apiDelegate(st, o, s, hHex)
		}},
		{"valid signature + non-holder signer", true, func(st *Store, _ string, _ ed25519.PrivateKey, cHex string) {
			oHex, o := newKey(t)
			oct, sg := signDel(t, o, opDelegate, "b1/*", cHex, 1)
			_, _ = apiDelegate(st, oct, sg, oHex)
		}},
		{"malformed envelope", false, func(st *Store, hHex string, _ ed25519.PrivateKey, _ string) {
			_, _ = apiDelegate(st, []byte("not an envelope\n"), "00", hHex)
		}},
		{"invalid signature", false, func(st *Store, hHex string, h ed25519.PrivateKey, cHex string) {
			o, _ := signDel(t, h, opDelegate, "b1/*", cHex, 1)
			_, _ = apiDelegate(st, o, strings.Repeat("00", 64), hHex)
		}},
		{"relayed by someone other than the signer", false, func(st *Store, _ string, h ed25519.PrivateKey, cHex string) {
			relayHex, _ := newKey(t)
			o, s := signDel(t, h, opDelegate, "b1/*", cHex, 1)
			_, _ = apiDelegate(st, o, s, relayHex)
		}},
		{"accepted delegation", true, func(st *Store, hHex string, h ed25519.PrivateKey, cHex string) {
			o, s := signDel(t, h, opDelegate, "b1/*", cHex, 1)
			if _, err := apiDelegate(st, o, s, hHex); err != nil {
				t.Errorf("accepted case was refused: %v", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, hHex, h, cHex := mk(t)
			before := count(st)
			c.run(st, hHex, h, cHex)
			after := count(st)
			if c.journalled && after != before+1 {
				t.Errorf("expected the attempt to be PRESERVED; entries went %d → %d", before, after)
			}
			if !c.journalled && after != before {
				t.Errorf("expected NO journal entry; entries went %d → %d", before, after)
			}
			if err := st.VerifyLog(); err != nil {
				t.Errorf("journal verification broke: %v", err)
			}
		})
	}
}

// #106: a grant signed BEFORE a revocation must not be replayable after it.
//
// This failed before delegation_rev existed. Revocation removed the delegate,
// and resubmitting the ORIGINAL GRANT BYTES — unchanged, validly signed, by the
// holder — re-activated them. "Revocation removes delegated control" held only
// until someone replayed a file they still had, and the realistic replayer is
// not an attacker but a retry loop or a redeploy.
func TestPreRevocationGrantIsNotReplayable(t *testing.T) {
	st := newMemStoreForTest(t)
	hHex, h := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signRes(t, h, "r6/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, hHex); err != nil {
		t.Fatal(err)
	}
	// Grant at permission-state 0, keeping the bytes.
	goct, gsig := signDel(t, h, opDelegate, "r6/*", ciHex, 1, 0)
	if _, err := apiDelegate(st, goct, gsig, hHex); err != nil {
		t.Fatal(err)
	}
	// Revoke at 1.
	roct, rsig := signDel(t, h, opRevoke, "r6/*", ciHex, 1, 1)
	if _, err := apiDelegate(st, roct, rsig, hHex); err != nil {
		t.Fatal(err)
	}
	if delegates(st)["r6/*"][ciHex] {
		t.Fatal("revocation did not take effect")
	}

	// REPLAY the original grant, byte for byte.
	_, err := apiDelegate(st, goct, gsig, hHex)
	if err == nil {
		t.Fatal("the pre-revocation grant was ACCEPTED again — revocation is not durable")
	}
	if delegates(st)["r6/*"][ciHex] {
		t.Fatal("the replayed grant re-activated the delegate")
	}
	// And the refusal is preserved, conferring nothing.
	last := st.ReadLog()[len(st.ReadLog())-1]
	if last.Status != "rejected" || last.EnvelopeB64 == "" {
		t.Errorf("the replay was not preserved as a refusal: status=%q", last.Status)
	}
	// A FRESH grant at the current permission-state still works — the holder is
	// not locked out, only the stale bytes are.
	foct, fsig := signDel(t, h, opDelegate, "r6/*", ciHex, 1, delegationRev(st, "r6/*").Int64())
	if _, err := apiDelegate(st, foct, fsig, hHex); err != nil {
		t.Fatalf("a fresh grant at the current state was refused: %v", err)
	}
	if !delegates(st)["r6/*"][ciHex] {
		t.Error("the fresh grant did not take effect")
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal verification broke: %v", err)
	}
}
