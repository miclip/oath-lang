package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// hasDel reports whether key holds a live grant under ns (membership only, scope
// ignored) — the boolean question the pre-scope tests asked of delegates().
func hasDel(st *Store, ns, key string) bool {
	_, ok := delegates(st)[ns][key]
	return ok
}

// signScopedDel is signDel with a /3 scope.
func signScopedDel(t *testing.T, priv ed25519.PrivateKey, op, ns, subject, scope string, rev, drev int64) ([]byte, string) {
	t.Helper()
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	env := delEnvelope{Op: op, Namespace: ns, Subject: subject, Scope: scope, Authority: pub,
		AuthorityRev: big.NewInt(rev), DelegationRev: big.NewInt(drev), Pubkey: pub}
	oct := delEncode(env)
	return oct, hex.EncodeToString(ed25519.Sign(priv, oct))
}

// A single fresh reservation holds at authority rev 1, which every test here signs
// against; drev is the delegation-state CAS (0 for the first grant, then climbing).
func appendScopedDel(t *testing.T, st *Store, priv ed25519.PrivateKey, op, ns, subject, scope string, drev int64) {
	t.Helper()
	oct, sig := signScopedDel(t, priv, op, ns, subject, scope, 1, drev)
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	if _, err := apiDelegate(st, oct, sig, pub); err != nil {
		t.Fatalf("apiDelegate(%s scope=%q): %v", op, scope, err)
	}
}

// #66 item 1: a grant narrower than the whole prefix. A delegate scoped to one name
// may bind exactly that name and nothing else; the holder is unaffected.
func TestScopedDelegateBindsOnlyItsScope(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)

	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendScopedDel(t, st, alice, opDelegate, "alice/*", ciHex, "alice/report", 0)

	// The one scoped name: accepted.
	if reps, err := apiPut(st, `(defn alice/report [] [] Int 1)`, ciHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("scoped delegate could not bind its own name: %v %+v", err, reps[0])
	}
	// Any other name under the prefix: blocked — the scope does not cover it.
	if reps, _ := apiPut(st, `(defn alice/other [] [] Int 2)`, ciHex, ""); reps[0].Status != "blocked" {
		t.Fatalf("a name-scoped delegate bound a name outside its scope (status=%s)", reps[0].Status)
	}
	// The holder still binds anything.
	if reps, err := apiPut(st, `(defn alice/anything [] [] Int 3)`, aliceHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("the holder was constrained by a delegate's scope: %v %+v", err, reps[0])
	}
}

// A sub-prefix scope admits every name under it, and nothing beside it.
func TestSubPrefixScope(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendScopedDel(t, st, alice, opDelegate, "alice/*", ciHex, "alice/tools/*", 0)

	if reps, err := apiPut(st, `(defn alice/tools/fmt [] [] Int 1)`, ciHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("sub-prefix delegate could not bind under its scope: %v %+v", err, reps[0])
	}
	if reps, _ := apiPut(st, `(defn alice/lib [] [] Int 2)`, ciHex, ""); reps[0].Status != "blocked" {
		t.Fatalf("a sub-prefix delegate bound a name outside its scope (status=%s)", reps[0].Status)
	}
}

// A /2 grant (no scope) is unchanged: the whole prefix.
func TestWholePrefixDelegateUnchanged(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendDel(t, st, alice, opDelegate, "alice/*", ciHex, 1) // unscoped /2
	for _, src := range []string{`(defn alice/a [] [] Int 1)`, `(defn alice/deep/b [] [] Int 2)`} {
		if reps, err := apiPut(st, src, ciHex, ""); err != nil || reps[0].Status != "accepted" {
			t.Fatalf("an unscoped (whole-prefix) delegate was blocked: %v %+v", err, reps[0])
		}
	}
}

// Re-scoping the same subject replaces the old scope (one scope per subject).
func TestRescopeReplaces(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendScopedDel(t, st, alice, opDelegate, "alice/*", ciHex, "alice/first", 0)
	appendScopedDel(t, st, alice, opDelegate, "alice/*", ciHex, "alice/second", 1) // re-scope
	// The new scope governs; the old name is no longer permitted.
	if reps, err := apiPut(st, `(defn alice/second [] [] Int 1)`, ciHex, ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("the re-scoped name was not permitted: %v %+v", err, reps[0])
	}
	if reps, _ := apiPut(st, `(defn alice/first [] [] Int 2)`, ciHex, ""); reps[0].Status != "blocked" {
		t.Fatalf("the superseded scope still permitted its name (status=%s)", reps[0].Status)
	}
}

// A revoke removes the grant whatever its scope.
func TestScopedRevoke(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendScopedDel(t, st, alice, opDelegate, "alice/*", ciHex, "alice/report", 0)
	appendDel(t, st, alice, opRevoke, "alice/*", ciHex, 1)
	if reps, _ := apiPut(st, `(defn alice/report [] [] Int 1)`, ciHex, ""); reps[0].Status != "blocked" {
		t.Fatalf("a revoked scoped delegate still published (status=%s)", reps[0].Status)
	}
}

// explain reports a scoped delegate only for names its scope covers — a delegate
// scoped to one name must not appear as able to publish a different name under the
// prefix (the misreport codex caught).
func TestExplainListsOnlyCoveringDelegates(t *testing.T) {
	st := newMemStoreForTest(t)
	aliceHex, alice := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signRes(t, alice, "alice/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, aliceHex); err != nil {
		t.Fatal(err)
	}
	appendScopedDel(t, st, alice, opDelegate, "alice/*", ciHex, "alice/report", 0)
	// The holder publishes both names (the delegate's scope covers only one).
	for _, src := range []string{`(defn alice/report [] [] Int 1)`, `(defn alice/other [] [] Int 2)`} {
		if reps, err := apiPut(st, src, aliceHex, ""); err != nil || reps[0].Status != "accepted" {
			t.Fatalf("holder setup publish failed: %v %+v", err, reps[0])
		}
	}
	covered, err := buildExplain(st, "alice/report")
	if err != nil {
		t.Fatal(err)
	}
	if len(covered.Provenance.NamespaceDelegates) != 1 || covered.Provenance.NamespaceDelegates[0] != ciHex {
		t.Errorf("explain of the scoped name should list the delegate, got %v", covered.Provenance.NamespaceDelegates)
	}
	uncovered, err := buildExplain(st, "alice/other")
	if err != nil {
		t.Fatal(err)
	}
	if len(uncovered.Provenance.NamespaceDelegates) != 0 {
		t.Errorf("explain of a name outside the scope must not list the delegate as able to publish it, got %v",
			uncovered.Provenance.NamespaceDelegates)
	}
}

// The /3 envelope round-trips and its signature verifies; an empty scope is malformed.
func TestScopedEnvelopeRoundTrip(t *testing.T) {
	_, alice := newKey(t)
	ciHex, _ := newKey(t)
	oct, sig := signScopedDel(t, alice, opDelegate, "alice/*", ciHex, "alice/report", 0, 0)
	if !strings.HasPrefix(string(oct), delegateVersion3+"\n") {
		t.Fatalf("a scoped grant must be written under /3, got:\n%s", oct)
	}
	env, err := parseDelegateEnvelope(oct)
	if err != nil {
		t.Fatalf("scoped envelope did not parse: %v", err)
	}
	if env.Scope != "alice/report" {
		t.Fatalf("scope did not round-trip: %q", env.Scope)
	}
	if err := delVerify(env, sig); err != nil {
		t.Fatalf("scoped envelope signature did not verify: %v", err)
	}
	// A /3 line-shape with an empty scope value is refused.
	bad := strings.Replace(string(oct), "scope=alice/report", "scope=", 1)
	if _, err := parseDelegateEnvelope([]byte(bad)); err == nil {
		t.Fatal("a /3 envelope with an empty scope must be refused")
	}
}

func TestValidScopeUnder(t *testing.T) {
	ok := []string{"alice/report", "alice/tools/*", "alice/a/b/c", "alice/a/b/*"}
	for _, s := range ok {
		if err := validScopeUnder(s, "alice/*"); err != nil {
			t.Errorf("valid scope %q under alice/* was rejected: %v", s, err)
		}
	}
	bad := []string{
		"bob/report",   // outside the namespace
		"alicex/report", // prefix is a segment, not a string prefix
		"alice/*",      // equals the whole prefix — must be a /2 grant, not narrower
		"alice",        // not under the prefix
		"alice/a*b",    // stray glob
		"alice/re\tport", // control character
	}
	for _, s := range bad {
		if err := validScopeUnder(s, "alice/*"); err == nil {
			t.Errorf("invalid scope %q under alice/* was accepted", s)
		}
	}
}
