package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"os"
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

// RES-PROTOCOL-ROOT: the only prefixes that are not first-come.
func TestProtocolRootsAreNotReservable(t *testing.T) {
	for _, bad := range []string{"key/*", "sys/*", "key/sub/*"} {
		if err := validNamespacePattern(bad); err == nil {
			t.Errorf("%q was reservable; protocol roots must not be claimable", bad)
		}
	}
	// Compared on the FIRST SEGMENT, not by string prefix. "oathkeeper" is an
	// ordinary name and must stay claimable, or the reserved list would silently
	// consume every name that happens to start with the same letters.
	// `oath/*` is deliberately in this list: it is a first-come root the project
	// reserves like any other publisher, NOT a kernel namespace.
	for _, good := range []string{"oath/*", "oathkeeper/*", "keys/*", "system/*", "alice/*"} {
		if err := validNamespacePattern(good); err != nil {
			t.Errorf("%q must remain first-come, got: %v", good, err)
		}
	}
}

// The squatting decision, made explicit as a test so it cannot be reversed by
// accident: an arbitrary key may take an attractive human-readable root, and the
// SECOND key is refused. This is intended behaviour for v1 (see the rationale on
// protocolRoots), not an oversight.
func TestAttractiveRootsAreFirstComeIncludingSquatting(t *testing.T) {
	st := newMemStoreForTest(t)
	squatterHex, squatter := newKey(t)
	realHex, real := newKey(t)

	octets, sig := signRes(t, squatter, "openai/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, squatterHex); err != nil {
		t.Fatalf("first-come must grant the first signed claim: %v", err)
	}
	octets, sig = signRes(t, real, "openai/*", noAuthority, 0)
	if _, err := apiReserve(st, octets, sig, realHex); err == nil {
		t.Error("a second key took a held root; first-come must be first-come")
	}
}

// RES-ROOT-CONSTRAINS-RESERVATION-ONLY (§8.7.5). A protocol root blocks
// RESERVATION and nothing else. A name beneath one is published, owned, and
// governed exactly like any other — "unreservable" is not "forbidden".
//
// Worth a test because the instinct pulls the other way: a reserved prefix reads
// like a closed prefix, and an implementation that refused key/foo would look
// defensible while silently making existing history unpublishable.
func TestProtocolRootsDoNotForbidPublication(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, _ := newKey(t)
	bobHex, _ := newKey(t)

	reps, err := apiPut(st, `(defn key/foo [] [] Int 1)`, aliceHex, "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("publication under a protocol root was refused: %v %+v", err, reps[0])
	}
	// And it is owned normally: exact-name TOFU applies beneath a protocol root.
	if owner, _ := nameOwner(st, "key/foo"); owner != aliceHex {
		t.Errorf("key/foo owner = %s, want the first publisher", shortHash(owner))
	}
	// A second key CAN repoint it here, and that is correct: exact-name TOFU is
	// opt-in per policy rule (#84), and this store has no policy.json. The
	// asymmetry is worth pinning — a RESERVATION enforces with no policy at all,
	// so a protocol root, being unreservable, is protected ONLY by operator
	// policy. That is acceptable because protocol roots hold kernel-defined names,
	// but it is the opposite of what "reserved" suggests.
	reps, _ = apiPut(st, `(defn key/foo [] [] Int 2)`, bobHex, "")
	if reps[0].Status != "accepted" {
		t.Errorf("unexpected refusal: TOFU is opt-in, so with no policy this repoint is allowed (%s)", reps[0].Error)
	}
	// The PREFIX remains unreservable, which is the only thing the root controls.
	if err := validNamespacePattern("key/*"); err == nil {
		t.Error("key/* became reservable")
	}
}

// A reservation must never be accepted from a bearer-authenticated caller.
//
// RES-SIGNED requires the authenticated principal to EQUAL the key named in the
// envelope. A bearer principal is server-vouched — it says who the registry
// believes you are, not which key you hold — so accepting one would let the
// registry grant a namespace to a key that never signed for it. That is the one
// thing this operation exists to make impossible, and it is a capability gap a
// reader of mcpCallTool could easily not notice, since `reserve` is otherwise
// shaped exactly like `put`.
func TestReserveRequiresASignedRequest(t *testing.T) {
	st := newMemStoreForTest(t)
	pubHex, priv := newKey(t)
	octets, sig := signRes(t, priv, "alice/*", noAuthority, 0)
	args, _ := json.Marshal(map[string]any{
		"envelope": encodeEnvelopeB64(octets), "signature": sig,
	})

	// canWrite=true, signed=FALSE: a write-scoped bearer token.
	if _, err := mcpCallTool(st, "reserve", args, pubHex, true, false, false); err == nil {
		t.Fatal("a bearer-authenticated caller reserved a namespace")
	}
	// The same call, signed, succeeds.
	if _, err := mcpCallTool(st, "reserve", args, pubHex, true, true, false); err != nil {
		t.Fatalf("a signed reservation was refused: %v", err)
	}
	if holder, _ := reservationRev(st, "alice/*"); holder != pubHex {
		t.Errorf("holder = %s, want %s", shortHash(holder), shortHash(pubHex))
	}
}

// captureStdout runs f and returns what it printed.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var b bytes.Buffer
	if _, err := io.Copy(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// THE #104 CONTRACT. Reservation advice must never be rendered from a view that is
// not the one a reservation would be evaluated against — because the failure is not
// a stale answer but a confident wrong one, recommending the single irreversible
// act in the protocol against a prefix somebody already holds.
func TestNoReservationAdviceFromNonAuthoritativeView(t *testing.T) {
	st := newMemStoreForTest(t)
	unclaimed := authorityView{Holder: noAuthority, Rev: big.NewInt(0),
		Source: "./codebase", Authoritative: false}

	out := captureStdout(t, func() { renderAuthority(st, "oath/*", unclaimed, true) })
	for _, forbidden := range []string{"would claim it", "oath reserve", "PERMANENT"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a NON-AUTHORITATIVE view offered reservation advice (%q):\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "NOT AUTHORITATIVE") {
		t.Errorf("a non-authoritative view did not say so:\n%s", out)
	}

	// The same state, authoritatively read, SHOULD advise — the contract restricts
	// where advice may come from, and must not suppress it where it is sound.
	unclaimed.Authoritative = true
	unclaimed.Source = "https://registry.example"
	out = captureStdout(t, func() { renderAuthority(st, "oath/*", unclaimed, true) })
	if !strings.Contains(out, "oath reserve") || !strings.Contains(out, "PERMANENT") {
		t.Errorf("an AUTHORITATIVE view withheld sound advice:\n%s", out)
	}
	if strings.Contains(out, "NOT AUTHORITATIVE") {
		t.Errorf("an authoritative view was labelled otherwise:\n%s", out)
	}
}

// A held prefix reports its delegates and its delegation revision, so that the
// value a grant must state is readable without parsing the journal by hand.
func TestAuthorityRendersDelegationState(t *testing.T) {
	st := newMemStoreForTest(t)
	v := authorityView{Holder: "aa" + strings.Repeat("0", 62), Rev: big.NewInt(1),
		DelegationRev: big.NewInt(5), Delegates: []string{"bb" + strings.Repeat("0", 62)},
		Source: "https://registry.example", Authoritative: true}
	out := captureStdout(t, func() { renderAuthority(st, "oath/*", v, true) })
	for _, want := range []string{"is HELD by", "publication delegated to bb", "delegation revision 5"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "oath reserve") {
		t.Errorf("a HELD prefix was advertised as reservable:\n%s", out)
	}
}

// THE EXIT-CODE CONTRACT (#104). A negative answer and no answer are different
// results, and a script must be able to tell them apart without parsing prose.
//
//	authoritative, held        → 0, no advice (there is nothing to claim)
//	authoritative, unclaimed   → 0, advice given
//	authoritative unavailable  → 1, NO advice
//
// Exit 0 on the third would let a caller read "warning printed" as success and
// reserve anyway — reserving a prefix somebody already holds is exactly the
// permanent mistake this command exists to prevent.
func TestAuthorityExitCodeDistinguishesNoAnswerFromNegativeAnswer(t *testing.T) {
	st := newMemStoreForTest(t)

	// No registry configured: the local store IS where a reservation would land,
	// so the question is answered.
	var rc int
	out := captureStdout(t, func() { rc = cmdAuthority(st, "", "", "", "free/*") })
	if rc != 0 {
		t.Errorf("an ANSWERED question exited %d, want 0", rc)
	}
	if !strings.Contains(out, "oath reserve") {
		t.Errorf("an authoritative unclaimed view withheld advice:\n%s", out)
	}

	// A registry is configured but unreachable — no key, and the registry
	// authenticates reads. The question is NOT answered.
	out = captureStdout(t, func() {
		rc = cmdAuthority(st, "https://registry.invalid", "", "", "free/*")
	})
	if rc != 1 {
		t.Errorf("an UNANSWERED question exited %d, want 1 — a caller cannot distinguish it from a negative answer", rc)
	}
	for _, forbidden := range []string{"would claim it", "oath reserve '", "PERMANENT"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("advice was given without authoritative state (%q):\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "NOT ANSWERED") {
		t.Errorf("a failure to answer did not say so:\n%s", out)
	}
}
