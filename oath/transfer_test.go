package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

func signXfer(t *testing.T, holder, recipient ed25519.PrivateKey, ns string, rev int64, attempt ...string) ([]byte, string, string) {
	t.Helper()
	from := hex.EncodeToString(holder.Public().(ed25519.PublicKey))
	to := hex.EncodeToString(recipient.Public().(ed25519.PublicKey))
	a := strings.Repeat("a", 64)
	if len(attempt) > 0 {
		a = attempt[0]
	}
	env := xferEnvelope{Op: opTransfer, Namespace: ns, FromAuthority: from, ToAuthority: to,
		AuthorityRev: big.NewInt(rev), Attempt: a}
	oct := xferEncode(env)
	return oct, hex.EncodeToString(ed25519.Sign(holder, oct)), hex.EncodeToString(ed25519.Sign(recipient, oct))
}

func reserveFor(t *testing.T, st *Store, pubHex string, priv ed25519.PrivateKey, ns string) {
	t.Helper()
	oct, sig := signRes(t, priv, ns, noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, pubHex); err != nil {
		t.Fatal(err)
	}
}

// The dead end this closes: two valid principals agree, and the protocol can
// finally express it.
func TestTransferMovesAuthority(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	bHex, b := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")

	oct, hs, rs := signXfer(t, a, b, "co/*", 1)
	rep, err := apiTransfer(st, oct, hs, rs, aHex)
	if err != nil {
		t.Fatalf("a consensual transfer was refused: %v", err)
	}
	holder, rev := reservationRev(st, "co/*")
	if holder != bHex {
		t.Errorf("holder is %s, want the recipient %s", shortHash(holder), shortHash(bHex))
	}
	if rev.Int64() != 2 { // XFER-ADVANCES-AUTHORITY-REV
		t.Errorf("authority_rev = %s, want 2", rev)
	}
	if rep.To != bHex {
		t.Errorf("report says the new holder is %s", shortHash(rep.To))
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal verification broke: %v", err)
	}
}

// XFER-SIGNED-BOTH / XFER-RECIPIENT-CONSENTS. A holder must not be able to push a
// namespace onto an unwilling key.
func TestTransferRequiresBothSignatures(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	_, b := newKey(t)
	_, c := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")

	oct, hs, rs := signXfer(t, a, b, "co/*", 1)

	if _, err := apiTransfer(st, oct, hs, "", aHex); err == nil {
		t.Error("a transfer with NO recipient signature was accepted: custody was forced onto a key that never consented")
	}
	if _, err := apiTransfer(st, oct, "", rs, aHex); err == nil {
		t.Error("a transfer with no HOLDER signature was accepted")
	}
	// A signature from the wrong key is not consent, however valid it is.
	wrong := hex.EncodeToString(ed25519.Sign(c, oct))
	if _, err := apiTransfer(st, oct, hs, wrong, aHex); err == nil {
		t.Error("a third party's signature was accepted as the recipient's consent")
	}
	if h, _ := reservationRev(st, "co/*"); h != aHex {
		t.Error("a refused transfer moved authority")
	}
}

// XFER-RESERVATION-LIMIT: transfer must not be a way around the cap.
func TestTransferRespectsReservationLimit(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	bHex, b := newKey(t)
	reserveFor(t, st, aHex, a, "extra/*")
	for i := 0; i < maxReservationsPerPrincipal; i++ {
		reserveFor(t, st, bHex, b, string(rune('m'+i))+"full/*")
	}
	oct, hs, rs := signXfer(t, a, b, "extra/*", 1)
	_, err := apiTransfer(st, oct, hs, rs, aHex)
	if err == nil {
		t.Fatal("transfer let a key exceed the reservation cap — the limit is bypassable")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("refusal does not mention the limit: %v", err)
	}
}

// XFER-NO-CAPTURE: the prefix moves, other people's names do not.
func TestTransferDoesNotCaptureExactNames(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	_, b := newKey(t)
	cHex, c := newKey(t)

	// A third party owns a name FIRST, then the prefix is reserved and transferred.
	if err := st.AppendLog(&LogEntry{Author: cHex, Name: "cofoo", Status: "accepted",
		Hash: "h1", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	_ = c
	reserveFor(t, st, aHex, a, "co/*")
	oct, hs, rs := signXfer(t, a, b, "co/*", 1)
	rep, err := apiTransfer(st, oct, hs, rs, aHex)
	if err != nil {
		t.Fatal(err)
	}
	if owner, _ := nameOwner(st, "cofoo"); owner != cHex {
		t.Errorf("a transfer changed the owner of an existing name to %s", shortHash(owner))
	}
	_ = rep
}

// XFER-AUTHORSHIP-UNCHANGED and delegation clearing.
func TestTransferClearsDelegationsAndKeepsAuthorship(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	_, b := newKey(t)
	dHex, _ := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")

	goct, gsig := signDel(t, a, opDelegate, "co/*", dHex, 1, 0)
	if _, err := apiDelegate(st, goct, gsig, aHex); err != nil {
		t.Fatal(err)
	}
	if !hasDel(st, "co/*", dHex) {
		t.Fatal("the delegation did not take effect")
	}
	drevBefore := delegationRev(st, "co/*").Int64()

	oct, hs, rs := signXfer(t, a, b, "co/*", 1)
	if _, err := apiTransfer(st, oct, hs, rs, aHex); err != nil {
		t.Fatal(err)
	}

	if hasDel(st, "co/*", dHex) {
		t.Error("a delegation SURVIVED the transfer: the recipient inherited a publisher it never authorized")
	}
	if d := delegationRev(st, "co/*").Int64(); d <= drevBefore {
		t.Errorf("delegation_rev did not advance across the transfer (%d -> %d): the old grant envelope is replayable", drevBefore, d)
	}
	// And the old grant cannot be replayed to restore itself.
	if _, err := apiDelegate(st, goct, gsig, aHex); err == nil {
		t.Error("the pre-transfer grant was replayable after the handover")
	}
	if hasDel(st, "co/*", dHex) {
		t.Error("the replayed grant re-activated a delegate under the new holder")
	}
}

// XFER-AUTHORITY-CURRENT plus refusal preservation.
func TestStaleTransferIsRefusedAndPreserved(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	_, b := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")

	oct, hs, rs := signXfer(t, a, b, "co/*", 0) // wrong revision: it is at 1
	if _, err := apiTransfer(st, oct, hs, rs, aHex); err == nil {
		t.Fatal("a transfer signed against a stale authority revision was accepted")
	}
	last := st.ReadLog()[len(st.ReadLog())-1]
	if last.Status != "rejected" || last.EnvelopeB64 == "" {
		t.Errorf("the refusal was not preserved: status=%q", last.Status)
	}
	if h, r := reservationRev(st, "co/*"); h != aHex || r.Int64() != 1 {
		t.Error("a refused transfer changed authority state")
	}
}

// Non-holder, protocol root, and self-transfer.
func TestTransferRefusesIllegitimateScopes(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	bHex, b := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")

	// A key that does not hold it cannot transfer it, even with both signatures.
	oct, hs, rs := signXfer(t, b, a, "co/*", 1)
	if _, err := apiTransfer(st, oct, hs, rs, bHex); err == nil {
		t.Error("a non-holder transferred a namespace")
	}
	// Protocol roots are not receivable.
	oct, hs, rs = signXfer(t, a, b, "key/*", 1)
	if _, err := apiTransfer(st, oct, hs, rs, aHex); err == nil {
		t.Error("a protocol root was transferred")
	}
	// Self-transfer is meaningless and must not advance the revision.
	env := xferEnvelope{Op: opTransfer, Namespace: "co/*", FromAuthority: aHex, ToAuthority: aHex,
		AuthorityRev: big.NewInt(1), Attempt: strings.Repeat("b", 64)}
	if err := env.validate(); err == nil {
		t.Error("a self-transfer validated")
	}
	_ = bHex
}

// A stranger may not relay an observed transfer.
func TestTransferCannotBeRelayedByAThirdParty(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	_, b := newKey(t)
	cHex, _ := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")
	oct, hs, rs := signXfer(t, a, b, "co/*", 1)
	if _, err := apiTransfer(st, oct, hs, rs, cHex); err == nil {
		t.Error("a third party relayed a transfer between two other keys")
	}
}

// XFER-ATTEMPT-ONE-SHOT / XFER-FRESH-CONSENT.
//
// The resurrection this closes: B countersigns, the submission is refused for a
// transient reason, and a month later — with nothing re-signed — the same bytes
// become effective. A refusal must mean "that transaction did not happen", not
// "it may happen automatically once its blocking condition disappears".
func TestRefusedTransferCannotBeResurrected(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	bHex, b := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")
	// Fill the recipient to the cap so the first attempt is refused for a
	// TRANSIENT reason — the case where resurrection is tempting.
	for i := 0; i < maxReservationsPerPrincipal; i++ {
		reserveFor(t, st, bHex, b, string(rune('p'+i))+"cap/*")
	}
	n1 := strings.Repeat("1", 64)
	oct, hs, rs := signXfer(t, a, b, "co/*", 1, n1)
	if _, err := apiTransfer(st, oct, hs, rs, aHex); err == nil {
		t.Fatal("the at-cap transfer was accepted")
	}
	if consumedAttempts(st)[n1] != "rejected" {
		t.Fatal("the refused attempt was not journaled as consumed")
	}

	// The blocking condition disappears. In this kernel a namespace cannot be
	// released, so simulate the general case directly: replay must be refused
	// because the ATTEMPT is spent, not because the cap is still full.
	_, err := apiTransfer(st, oct, hs, rs, aHex)
	if err == nil {
		t.Fatal("a refused transfer was resurrected by replaying the same bytes")
	}
	if !strings.Contains(err.Error(), "consumed") {
		t.Errorf("refused for the wrong reason — resurrection would still be possible once the cap frees: %v", err)
	}
	// And the replay is NOT journaled: the original refusal is already recorded,
	// so preserving every replay would let anyone holding the bytes grow the journal.
	before := len(st.ReadLog())
	_, _ = apiTransfer(st, oct, hs, rs, aHex)
	if len(st.ReadLog()) != before {
		t.Error("a replay of a consumed attempt was journaled — the journal is growable by anyone with the bytes")
	}
}

// An ACCEPTED attempt is consumed too, so a transfer cannot be re-applied.
func TestAcceptedAttemptIsAlsoConsumed(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	_, b := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")
	n := strings.Repeat("2", 64)
	oct, hs, rs := signXfer(t, a, b, "co/*", 1, n)
	if _, err := apiTransfer(st, oct, hs, rs, aHex); err != nil {
		t.Fatal(err)
	}
	if _, err := apiTransfer(st, oct, hs, rs, aHex); err == nil {
		t.Error("an accepted transfer was re-applied from the same bytes")
	}
}

// XFER-FRESH-CONSENT: a distinct nonce, signed by both, works.
func TestFreshAttemptSucceedsAfterARefusal(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	bHex, b := newKey(t)
	reserveFor(t, st, aHex, a, "co/*")
	stale, hs, rs := signXfer(t, a, b, "co/*", 0, strings.Repeat("3", 64)) // wrong rev
	if _, err := apiTransfer(st, stale, hs, rs, aHex); err == nil {
		t.Fatal("a stale transfer was accepted")
	}
	fresh, hs2, rs2 := signXfer(t, a, b, "co/*", 1, strings.Repeat("4", 64))
	if _, err := apiTransfer(st, fresh, hs2, rs2, aHex); err != nil {
		t.Fatalf("a freshly signed attempt was refused after an earlier refusal: %v", err)
	}
	if h, _ := reservationRev(st, "co/*"); h != bHex {
		t.Error("the fresh attempt did not take effect")
	}
}
