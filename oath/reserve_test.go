package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// signRes builds and signs a reservation the way a client would.
func signRes(t *testing.T, priv ed25519.PrivateKey, ns, authority string, rev int64) ([]byte, string) {
	t.Helper()
	pubHex := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	env := resEnvelope{Op: opReserve, Namespace: ns, Authority: authority,
		AuthorityRev: big.NewInt(rev), Pubkey: pubHex}
	octets := resEncode(env)
	return octets, hex.EncodeToString(ed25519.Sign(priv, octets))
}

func newKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), priv
}

// The encoding must round-trip and must REFUSE anything it would not itself
// emit. A decoder that accepted variant spellings would let two different byte
// strings verify as the same statement, and the signature covers bytes.
func TestReserveEnvelopeRoundTripAndVariants(t *testing.T) {
	pubHex, priv := newKey(t)
	octets, _ := signRes(t, priv, "alice/*", noAuthority, 0)

	got, err := parseReserveEnvelope(octets)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got.Namespace != "alice/*" || got.Pubkey != pubHex || got.AuthorityRev.Sign() != 0 {
		t.Fatalf("round trip lost data: %+v", got)
	}

	text := string(octets)
	for _, variant := range []struct{ name, bytes string }{
		{"reordered members", strings.Replace(text, "op=reserve\nnamespace=alice/*", "namespace=alice/*\nop=reserve", 1)},
		{"trailing space in a value", strings.Replace(text, "namespace=alice/*", "namespace=alice/* ", 1)},
		{"leading zero on the revision", strings.Replace(text, "authority_rev=0", "authority_rev=00", 1)},
		{"unknown format line", strings.Replace(text, reserveVersion, "oath-reserve/9", 1)},
		{"extra member", text + "extra=1\n"},
	} {
		if _, err := parseReserveEnvelope([]byte(variant.bytes)); err == nil {
			t.Errorf("%s: accepted, but only bytes the encoder emits are the statement", variant.name)
		}
	}
}

// RES-PATTERN. Reservation is deliberately narrower than the policy pattern
// language: exact names are answered by publication, and "*" is a takeover.
func TestReservePatternIsPrefixOnly(t *testing.T) {
	for _, bad := range []string{"alice", "*", "/*", "alice/*/x", "al*ce/*", ""} {
		if err := validNamespacePattern(bad); err == nil {
			t.Errorf("pattern %q accepted; reservation must claim a <segments>/* prefix", bad)
		}
	}
	for _, good := range []string{"alice/*", "alice/sub/*"} {
		if err := validNamespacePattern(good); err != nil {
			t.Errorf("pattern %q rejected: %v", good, err)
		}
	}
}

// Segment-awareness is the property that stops a prefix capturing names its
// holder never reasoned about.
func TestNamespaceCoversIsSegmentAware(t *testing.T) {
	for _, c := range []struct {
		pat, name string
		want      bool
	}{
		{"alice/*", "alice/foo", true},
		{"alice/*", "alice/sub/foo", true},
		{"alice/*", "alice2/x", false}, // the whole point: not a raw string prefix
		{"alice/*", "alice", false},    // a namespace is not the bare name
		{"alice/*", "bob/foo", false},
	} {
		if got := namespaceCovers(c.pat, c.name); got != c.want {
			t.Errorf("namespaceCovers(%q,%q)=%v want %v", c.pat, c.name, got, c.want)
		}
	}
	if !namespaceOverlaps("alice/*", "alice/sub/*") || !namespaceOverlaps("alice/sub/*", "alice/*") {
		t.Error("nested prefixes must overlap in both directions")
	}
	if namespaceOverlaps("alice/*", "alice2/*") {
		t.Error("alice/* and alice2/* are independent claims")
	}
}

func TestReserveAcceptsAndIsDerivable(t *testing.T) {
	st := newMemStoreForTest(t)
	pubHex, priv := newKey(t)
	octets, sig := signRes(t, priv, "alice/*", noAuthority, 0)

	rep, err := apiReserve(st, octets, sig, pubHex)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if rep.Rev.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("first reservation should land at rev 1, got %s", rep.Rev)
	}
	// Derived from the journal, never from a stored table.
	holder, rev := reservationRev(st, "alice/*")
	if holder != pubHex || rev.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("derived state = (%s, %s), want (%s, 1)", shortHash(holder), rev, shortHash(pubHex))
	}
	// A reservation entry must not break journal verification: it is a different
	// signed format in the same three fields, routed by its format line.
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal verification broke on an authority entry: %v", err)
	}
}

// RES-SIGNED, both halves.
func TestReserveRejectsUnsignedAndImpersonated(t *testing.T) {
	st := newMemStoreForTest(t)
	pubHex, priv := newKey(t)
	otherHex, _ := newKey(t)
	octets, sig := signRes(t, priv, "alice/*", noAuthority, 0)

	bad := strings.Repeat("00", ed25519.SignatureSize)
	if _, err := apiReserve(st, octets, bad, pubHex); err == nil {
		t.Error("accepted a reservation whose signature does not verify")
	}
	// A relayer must not be able to submit somebody else's claim as their own.
	if _, err := apiReserve(st, octets, sig, otherHex); err == nil {
		t.Error("accepted a reservation signed by one key from a caller authenticated as another")
	}
}

// RES-AUTHORITY-CURRENT: a reservation is a revision of authority, so a stale
// claim is refused rather than silently overwriting state the signer never saw.
func TestReserveRejectsStaleAuthorityState(t *testing.T) {
	st := newMemStoreForTest(t)
	pubHex, priv := newKey(t)
	octets, sig := signRes(t, priv, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, pubHex); err != nil {
		t.Fatal(err)
	}
	// Replaying the very same bytes must fail: rev has moved to 1.
	if _, err := apiReserve(st, octets, sig, pubHex); err == nil {
		t.Error("a replayed reservation was accepted; authority_rev is the replay defence")
	}
}

// RES-FIRST-COME across overlapping spellings.
func TestReserveRejectsOverlappingPrefixFromAnotherKey(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	bobHex, bob := newKey(t)

	octets, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	// A nested prefix is the same claim spelled differently.
	octets, sig = signRes(t, bob, "alice/sub/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, bobHex); err == nil {
		t.Error("bob took alice/sub/* while alice holds alice/*")
	}
	// An independent namespace is unaffected — first-come must not become a veto.
	octets, sig = signRes(t, bob, "alice2/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, bobHex); err != nil {
		t.Errorf("alice2/* is an independent claim but was refused: %v", err)
	}
}

// Taking a held prefix is a TRANSFER, which is a different signed operation and
// is not implemented. It must refuse rather than silently succeed.
func TestReserveRefusesTakeoverOfHeldPrefix(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	bobHex, bob := newKey(t)

	octets, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	// Bob signs against the CURRENT state honestly, and must still be refused.
	octets, sig = signRes(t, bob, "alice/*", aliceHex, 1)
	if _, err := apiReserve(st, octets, sig, bobHex); err == nil {
		t.Error("bob took a prefix alice holds, with no transfer signed by alice")
	}
}

// THE DECISIVE TEST for #66: a key that no operator ever named reserves a
// namespace and publishes into it, and a DIFFERENT key is refused.
//
// The refusal must come from the POLICY layer, not from authentication. Both
// principals here are equally authenticated — apiPut is called directly, with no
// allowlist and no token in sight — so a refusal can only be authorization. A
// test that proved the second key was merely unauthenticated would prove the
// wrong thing: that is the gate that already existed and the one the milestone
// requires removing.
//
// There is also NO policy.json in this store. That is the case the whole
// operation exists for — a developer on a registry whose operator never edited
// anything on their behalf — and it is why the reservation check sits above
// evalPolicy's no-rule early return.
func TestReservedNamespaceIsEnforcedWithoutAnyPolicyFile(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	bobHex, _ := newKey(t)

	if pol, err := LoadPolicy(st.Root); err != nil || pol != nil {
		t.Fatalf("this test is only meaningful with no policy.json (got pol=%v err=%v)", pol, err)
	}

	octets, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, aliceHex); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// The holder may bind names in her namespace.
	reps, err := apiPut(st, `(defn alice/greet [] [] Int 1)`, aliceHex, "")
	if err != nil {
		t.Fatalf("holder refused in her own namespace: %v", err)
	}
	if reps[0].Status != "accepted" {
		t.Fatalf("holder refused in her own namespace: %s", reps[0].Error)
	}

	// A different key may not, and the reason must name the authority record.
	reps, _ = apiPut(st, `(defn alice/intruder [] [] Int 2)`, bobHex, "")
	if reps[0].Status != "blocked" {
		t.Fatalf("bob bound a name inside alice's reserved namespace (status=%s)", reps[0].Status)
	}
	if !strings.Contains(reps[0].Error, "reserved to key") {
		t.Errorf("refusal does not cite namespace authority, so it may be the wrong gate: %q", reps[0].Error)
	}

	// Outside the reservation, bob is unaffected: this grants authority over a
	// namespace, not over the registry.
	reps, err = apiPut(st, `(defn bob/own [] [] Int 3)`, bobHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("bob refused OUTSIDE alice's namespace — the reservation over-reached: %v %+v", err, reps[0])
	}
}

// RES-NO-CAPTURE, enforced rather than merely reported: a name published before
// the reservation keeps its owner. Retention (not refusal) is what stops a
// namespace being deniable by anyone willing to publish one name into it.
func TestReservationRetainsPriorExactNameOwners(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	bobHex, _ := newKey(t)

	// Bob gets there first with one name.
	if reps, err := apiPut(st, `(defn alice/early [] [] Int 1)`, bobHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("setup: %v %+v", err, reps[0])
	}

	// Alice can still reserve the namespace — otherwise one name would deny it
	// forever — and is TOLD what she did not get.
	octets, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	rep, err := apiReserve(st, octets, sig, aliceHex)
	if err != nil {
		t.Fatalf("one prior name denied the whole namespace: %v", err)
	}
	if len(rep.Retained) != 1 || rep.Retained[0] != "alice/early" {
		t.Errorf("retained set = %v, want [alice/early] reported at reservation time", rep.Retained)
	}

	// And the reservation did not seize it: bob still governs his own name.
	if owner, _ := nameOwner(st, "alice/early"); owner != bobHex {
		t.Errorf("prior owner of alice/early became %s; a reservation is prospective", shortHash(owner))
	}
	reps, err := apiPut(st, `(defn alice/early [] [] Int 2)`, bobHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("bob lost a name he owned when alice reserved the prefix: %v %+v", err, reps[0])
	}
}
