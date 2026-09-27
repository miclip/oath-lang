package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// End to end through buildExplain (#82): the authorship rung is decided by the
// JOURNAL's lineage evidence, not by the labels alone. Two keys that each
// validly signed the write establishing their lineage reach
// DISTINCT_KEYS_CUSTODY_UNVERIFIED; two bearer labels, or one signed lineage
// and one unrecoverable one, stay at DISTINCT_PRINCIPALS_CUSTODY_UNVERIFIED.

// Same props, different bodies: a body-only repoint changes the body lineage
// and INHERITS the props lineage, which is exactly the split under test.
const (
	sepSpec  = `(defn sep [] [(n Int)] Int (+ n n) (prop even [(n Int)] (== (sep n) (* 2 n))))`
	sepBody2 = `(defn sep [] [(n Int)] Int (* 2 n) (prop even [(n Int)] (== (sep n) (* 2 n))))`
)

func sepObject(t *testing.T, src string) []byte {
	t.Helper()
	scratch := newMemStoreForTest(t)
	reps, err := apiPut(scratch, src, "scratch", "")
	if err != nil || len(reps) != 1 {
		t.Fatalf("elaborating %q: %v %+v", src, err, reps)
	}
	d, err := scratch.GetDef(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	return encodeDef(d)
}

// publishSigned is object publication by a key-holder: the authenticated
// principal IS the key, as under signature auth.
func publishSigned(t *testing.T, st *Store, raw []byte, priv ed25519.PrivateKey, parent string, rev int64) {
	t.Helper()
	publishSignedAs(t, st, raw, priv, parent, rev, pubOf(priv))
}

// publishSignedAs records `label` as the principal while the envelope is signed
// by priv — the shape where attribution and signature can disagree.
func publishSignedAs(t *testing.T, st *Store, raw []byte, priv ed25519.PrivateKey, parent string, rev int64, label string) {
	t.Helper()
	publishSignedNamed(t, st, "sep", raw, priv, parent, rev, label)
}

func publishSignedNamed(t *testing.T, st *Store, name string, raw []byte, priv ed25519.PrivateKey, parent string, rev int64, label string) {
	t.Helper()
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	env := pubEnvelope{Op: "put", Name: name, Artifact: hashOfBytes(raw),
		Parent: parent, ParentRev: big.NewInt(rev), Author: pub, License: noLicense}
	octets := envelopeEncode(env)
	auth := &pubAuth{Bytes: string(octets), Sig: hex.EncodeToString(ed25519.Sign(priv, octets)), Pubkey: pub}
	out, err := apiPutObject(st, base64.StdEncoding.EncodeToString(raw),
		&objectNaming{ParamNames: []string{"n"}, PropNames: []string{"even"}}, auth, label, "")
	if err != nil || len(out) != 1 || out[0].Status != "accepted" {
		t.Fatalf("signed publication refused: %v %+v", err, out)
	}
}

// publishBearer is source publication under a server-vouched label: no key.
func publishBearer(t *testing.T, st *Store, src, label string) {
	t.Helper()
	reps, err := apiPut(st, src, label, "")
	if err != nil || len(reps) != 1 || reps[0].Status != "accepted" {
		t.Fatalf("bearer publication refused: %v %+v", err, reps)
	}
}

func explainSep(t *testing.T, st *Store) *explainPkg {
	t.Helper()
	pkg, err := buildExplain(st, "sep")
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestExplainDistinctSigningKeysReachDistinctKeys(t *testing.T) {
	st := newMemStoreForTest(t)
	kSpec, kBody := genKey(t), genKey(t)
	raw1, raw2 := sepObject(t, sepSpec), sepObject(t, sepBody2)
	publishSigned(t, st, raw1, kSpec, noParent, 0)
	publishSigned(t, st, raw2, kBody, hashOfBytes(raw1), 1)

	pkg := explainSep(t, st)
	pSpec, pBody := pubOf(kSpec), pubOf(kBody)
	if pkg.Provenance.SpecAuthor != pSpec || pkg.Provenance.BodyAuthor != pBody {
		t.Fatalf("setup: labels spec=%s body=%s, want the two keys", pkg.Provenance.SpecAuthor, pkg.Provenance.BodyAuthor)
	}
	if pkg.Provenance.Authorship != authDistinctKeys {
		t.Fatalf("authorship = %s, want %s", pkg.Provenance.Authorship, authDistinctKeys)
	}
	wantTier(t, "spec lineage", pkg.Provenance.SpecLineage, lineageKeySigned, 1, pSpec, "")
	wantTier(t, "body lineage", pkg.Provenance.BodyLineage, lineageKeySigned, 2, pBody, "")
	joined := strings.Join(pkg.Limitations, " | ")
	// The new rung keeps its caveat: two keys are not two custodians.
	if !strings.Contains(joined, "DISTINCT KEY") || !strings.Contains(joined, "custody and independent control were NOT verified") {
		t.Fatalf("distinct-keys limitation missing: %q", joined)
	}
	if strings.Contains(joined, "UNKNOWN") {
		t.Fatalf("fully signed lineages disclosed as unknown: %q", joined)
	}
	// The evidence reaches the JSON surface, named.
	b, _ := json.Marshal(pkg)
	for _, want := range []string{`"authorship":"DISTINCT_KEYS_CUSTODY_UNVERIFIED"`, `"spec_lineage":{"tier":"KEY_SIGNED","establishing_seq":1`, `"body_lineage":{"tier":"KEY_SIGNED","establishing_seq":2`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("JSON missing %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), authSeparateCustody) {
		t.Fatal("SEPARATE_CUSTODY_ATTESTED emitted")
	}
}

func TestExplainTwoBearerLabelsStayDistinctPrincipals(t *testing.T) {
	st := newMemStoreForTest(t)
	publishBearer(t, st, sepSpec, "alice")
	publishBearer(t, st, sepBody2, "bob")

	pkg := explainSep(t, st)
	if pkg.Provenance.SpecAuthor != "alice" || pkg.Provenance.BodyAuthor != "bob" {
		t.Fatalf("setup: labels spec=%s body=%s", pkg.Provenance.SpecAuthor, pkg.Provenance.BodyAuthor)
	}
	if pkg.Provenance.Authorship != authDistinctPrincipals {
		t.Fatalf("authorship = %s, want %s", pkg.Provenance.Authorship, authDistinctPrincipals)
	}
	wantTier(t, "spec lineage", pkg.Provenance.SpecLineage, lineageUnknown, 1, "", lineageReasonNoEnvelope)
	wantTier(t, "body lineage", pkg.Provenance.BodyLineage, lineageUnknown, 2, "", lineageReasonNoEnvelope)
	joined := strings.Join(pkg.Limitations, " | ")
	if !strings.Contains(joined, "held a signing key is UNKNOWN") {
		t.Fatalf("distinct-principals limitation must say key possession is unknown: %q", joined)
	}
	for _, lineage := range []string{"spec lineage is UNKNOWN", "body lineage is UNKNOWN"} {
		if !strings.Contains(joined, "key possession for the "+lineage) {
			t.Fatalf("%s not disclosed: %q", lineage, joined)
		}
	}
	// UNKNOWN is disclosed as unknown, never as a negative.
	if strings.Contains(joined, "carry no signature") || strings.Contains(joined, "NOT signed") {
		t.Fatalf("unknown lineage reported as unsigned: %q", joined)
	}
}

func TestExplainOneSignedOneUnknownStaysDistinctPrincipals(t *testing.T) {
	st := newMemStoreForTest(t)
	kSpec := genKey(t)
	publishSigned(t, st, sepObject(t, sepSpec), kSpec, noParent, 0)
	publishBearer(t, st, sepBody2, "bob")

	pkg := explainSep(t, st)
	if pkg.Provenance.SpecAuthor != pubOf(kSpec) || pkg.Provenance.BodyAuthor != "bob" {
		t.Fatalf("setup: labels spec=%s body=%s", pkg.Provenance.SpecAuthor, pkg.Provenance.BodyAuthor)
	}
	if pkg.Provenance.Authorship != authDistinctPrincipals {
		t.Fatalf("authorship = %s, want %s", pkg.Provenance.Authorship, authDistinctPrincipals)
	}
	wantTier(t, "spec lineage", pkg.Provenance.SpecLineage, lineageKeySigned, 1, pubOf(kSpec), "")
	wantTier(t, "body lineage", pkg.Provenance.BodyLineage, lineageUnknown, 2, "", lineageReasonNoEnvelope)
	joined := strings.Join(pkg.Limitations, " | ")
	if !strings.Contains(joined, "key possession for the body lineage is UNKNOWN") {
		t.Fatalf("unknown body lineage not disclosed: %q", joined)
	}
	// The rung's own caveat must not discard the spec lineage's real evidence.
	if strings.Contains(joined, "whether either held a signing key is UNKNOWN") {
		t.Fatalf("mixed history described as if no key were evidenced: %q", joined)
	}
	if !strings.Contains(joined, "does NOT show each lineage signed by its own principal's key") {
		t.Fatalf("mixed-history caveat missing: %q", joined)
	}
	if strings.Contains(joined, "key possession for the spec lineage") {
		t.Fatalf("signed spec lineage disclosed as unknown: %q", joined)
	}
}

// A lineage validly signed by a key that is NOT its recorded principal is
// evidence for that key only: the rung stays at distinct principals, and the
// disagreement is disclosed rather than read as either signed or unknown.
func TestExplainSignatureByAnotherKeyIsDisclosed(t *testing.T) {
	st := newMemStoreForTest(t)
	kSpec, kBody := genKey(t), genKey(t)
	raw1, raw2 := sepObject(t, sepSpec), sepObject(t, sepBody2)
	publishSigned(t, st, raw1, kSpec, noParent, 0)
	publishSignedAs(t, st, raw2, kBody, hashOfBytes(raw1), 1, "carol")

	pkg := explainSep(t, st)
	if pkg.Provenance.BodyAuthor != "carol" {
		t.Fatalf("setup: body label %q, want carol", pkg.Provenance.BodyAuthor)
	}
	if pkg.Provenance.Authorship != authDistinctPrincipals {
		t.Fatalf("authorship = %s, want %s", pkg.Provenance.Authorship, authDistinctPrincipals)
	}
	wantTier(t, "body lineage", pkg.Provenance.BodyLineage, lineageKeySigned, 2, pubOf(kBody), "")
	joined := strings.Join(pkg.Limitations, " | ")
	if !strings.Contains(joined, `but that entry attributes it to "carol"`) {
		t.Fatalf("label/key disagreement not disclosed: %q", joined)
	}
}

// An ALIAS publication of the same object rewrites the per-hash meta labels.
// Here "sep" has its props signed by kSpec and its body signed by kBody but
// recorded as "carol". An alias then publishes the identical objects so that
// meta ends up labelled kSpec/kBody. The name-scoped labels still say carol, so
// "sep" must not be promoted on labels it never recorded.
func TestExplainAliasCannotPromoteThroughSharedMeta(t *testing.T) {
	st := newMemStoreForTest(t)
	kSpec, kBody := genKey(t), genKey(t)
	raw1, raw2 := sepObject(t, sepSpec), sepObject(t, sepBody2)
	publishSigned(t, st, raw1, kSpec, noParent, 0)
	publishSignedAs(t, st, raw2, kBody, hashOfBytes(raw1), 1, "carol")
	// The alias: same two objects, same props-then-body order, honest labels.
	publishSignedNamed(t, st, "alias", raw1, kSpec, noParent, 0, pubOf(kSpec))
	publishSignedNamed(t, st, "alias", raw2, kBody, hashOfBytes(raw1), 1, pubOf(kBody))

	m, _ := st.GetMeta(hashOfBytes(raw2))
	if m.SpecAuthor != pubOf(kSpec) || m.BodyAuthor != pubOf(kBody) {
		t.Fatalf("setup: alias did not rewrite the shared labels (spec=%s body=%s)", m.SpecAuthor, m.BodyAuthor)
	}
	pkg := explainSep(t, st)
	if pkg.Provenance.BodyLineage.Principal != "carol" {
		t.Fatalf("name-scoped body principal = %q, want carol", pkg.Provenance.BodyLineage.Principal)
	}
	if pkg.Provenance.Authorship == authDistinctKeys {
		t.Fatalf("sep promoted to %s on labels only its alias recorded", authDistinctKeys)
	}
	// The reason for the lower rung is visible in the text, not only in JSON:
	// the key/principal mismatch against THIS name's record, and the fact that
	// the shared label shown above is not this name's.
	joined := strings.Join(pkg.Limitations, " | ")
	if !strings.Contains(joined, `but that entry attributes it to "carol"`) {
		t.Fatalf("name-scoped key/principal mismatch not disclosed: %q", joined)
	}
	if !strings.Contains(joined, `under THIS name the establishing entry (journal seq 2) records "carol"`) {
		t.Fatalf("shared-label rewrite not disclosed: %q", joined)
	}
	// Control: the alias itself, whose own record is honest, does reach it.
	alias, err := buildExplain(st, "alias")
	if err != nil {
		t.Fatal(err)
	}
	if alias.Provenance.Authorship != authDistinctKeys {
		t.Fatalf("alias authorship = %s, want %s", alias.Provenance.Authorship, authDistinctKeys)
	}
}

// Signedness never lifts SAME_PRINCIPAL: one key signing both lineages is one
// author, however well-evidenced.
func TestExplainOneKeyBothLineagesStaysSamePrincipal(t *testing.T) {
	st := newMemStoreForTest(t)
	k := genKey(t)
	raw1, raw2 := sepObject(t, sepSpec), sepObject(t, sepBody2)
	publishSigned(t, st, raw1, k, noParent, 0)
	publishSigned(t, st, raw2, k, hashOfBytes(raw1), 1)
	if got := explainSep(t, st).Provenance.Authorship; got != authSamePrincipal {
		t.Fatalf("authorship = %s, want %s", got, authSamePrincipal)
	}
}

// The rung is decided by the lineage's KEY matching its LABEL. Pure, so the
// case the publish path cannot produce — a valid signature by a key other than
// the recorded principal — is still pinned.
func TestAuthorshipLevelRequiresEachLabelsOwnKey(t *testing.T) {
	signed := func(k string) lineageTier {
		return lineageTier{Tier: lineageKeySigned, Seq: 1, Pubkey: k, Principal: k}
	}
	unknown := lineageTier{Tier: lineageUnknown, Reason: lineageReasonNoEnvelope}
	for _, c := range []struct {
		name       string
		spec, body lineageTier
		want       string
	}{
		{"both signed by their own labels", signed("A"), signed("B"), authDistinctKeys},
		{"spec signed by the other label", signed("B"), signed("B"), authDistinctPrincipals},
		{"body signed by a third key", signed("A"), signed("C"), authDistinctPrincipals},
		{"spec unknown", unknown, signed("B"), authDistinctPrincipals},
		{"body unknown", signed("A"), unknown, authDistinctPrincipals},
		// Meta labels match the keys, but THIS name recorded another principal
		// for the body: an alias rewrote the per-hash labels.
		{"name-scoped body label differs", signed("A"),
			lineageTier{Tier: lineageKeySigned, Seq: 2, Pubkey: "B", Principal: "carol"}, authDistinctPrincipals},
	} {
		if got := authorshipLevel("A", "B", c.spec, c.body); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	if got := authorshipLevel("A", "A", signed("A"), signed("A")); got != authSamePrincipal {
		t.Errorf("same label, both signed: %s", got)
	}
	if got := authorshipLevel("", "B", signed("A"), signed("B")); got != authUnattributed {
		t.Errorf("missing label: %s", got)
	}
}

func genKey(t *testing.T) ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func pubOf(k ed25519.PrivateKey) string { return hex.EncodeToString(k.Public().(ed25519.PublicKey)) }

// The OPPOSITE direction, and the one a promotion test cannot see: an alias
// rewriting the shared labels so both read as ONE key, which would collapse a
// genuinely distinct pair to SAME_PRINCIPAL. A first publication of the FINAL
// object under a new name establishes BOTH of that name's lineages at once, so
// one write by kSpec sets the hash-shared SpecAuthor AND BodyAuthor to kSpec --
// and "sep", whose own journal shows two keys, would be demoted by a write made
// under a name it has nothing to do with.
func TestExplainAliasCannotCollapseToSamePrincipal(t *testing.T) {
	st := newMemStoreForTest(t)
	kSpec, kBody := genKey(t), genKey(t)
	raw1, raw2 := sepObject(t, sepSpec), sepObject(t, sepBody2)
	publishSigned(t, st, raw1, kSpec, noParent, 0)
	publishSigned(t, st, raw2, kBody, hashOfBytes(raw1), 1)

	before := explainSep(t, st).Provenance.Authorship
	if before == authSamePrincipal {
		t.Fatalf("setup: sep is already %s, so the collapse below proves nothing", authSamePrincipal)
	}

	// The alias: ONE first publication of the final object, by kSpec alone.
	publishSignedNamed(t, st, "solo", raw2, kSpec, noParent, 0, pubOf(kSpec))

	m, _ := st.GetMeta(hashOfBytes(raw2))
	if m.SpecAuthor != pubOf(kSpec) || m.BodyAuthor != pubOf(kSpec) {
		t.Fatalf("setup: the alias did not collapse the shared labels (spec=%s body=%s)", m.SpecAuthor, m.BodyAuthor)
	}

	pkg := explainSep(t, st)
	if pkg.Provenance.Authorship == authSamePrincipal {
		t.Fatalf("sep collapsed to %s because another name published the same object; "+
			"this name's journal shows two keys", authSamePrincipal)
	}
	if pkg.Provenance.Authorship != before {
		t.Fatalf("sep authorship moved from %s to %s on a write under another name",
			before, pkg.Provenance.Authorship)
	}
	// Control: the alias's OWN record honestly has one author, so it reads that way.
	solo, err := buildExplain(st, "solo")
	if err != nil {
		t.Fatal(err)
	}
	if solo.Provenance.Authorship != authSamePrincipal {
		t.Fatalf("solo authorship = %s, want %s -- one key established both its lineages",
			solo.Provenance.Authorship, authSamePrincipal)
	}
}
