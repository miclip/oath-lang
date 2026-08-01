package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"testing"
)

func signDel(t *testing.T, priv ed25519.PrivateKey, op, ns, subject string, rev int64) ([]byte, string) {
	t.Helper()
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	env := delEnvelope{Op: op, Namespace: ns, Subject: subject, Authority: pub,
		AuthorityRev: big.NewInt(rev), Pubkey: pub}
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
		Authority: bobHex, AuthorityRev: big.NewInt(1), Pubkey: aliceHex}).validate(); err == nil {
		t.Error("accepted a grant whose authority is not the signer")
	}
	if err := (delEnvelope{Op: opDelegate, Namespace: "alice/*", Subject: aliceHex,
		Authority: aliceHex, AuthorityRev: big.NewInt(1), Pubkey: aliceHex}).validate(); err == nil {
		t.Error("accepted a self-delegation")
	}
	oct, _ := signDel(t, alice, opDelegate, "alice/*", bobHex, 1)
	if _, err := parseDelegateEnvelope(append(oct, []byte("extra=1\n")...)); err == nil {
		t.Error("accepted an envelope with an extra member")
	}
}
