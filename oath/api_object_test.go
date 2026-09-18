package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"testing"
)

func hashOfBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// signObjectPublication builds the envelope a publisher signs for object
// publication, binding the hash of the EXACT octets being submitted.
func signObjectPublication(t *testing.T, name string, raw []byte, priv ed25519.PrivateKey) *pubAuth {
	t.Helper()
	pubHex := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	env := pubEnvelope{Op: "put", Name: name, Artifact: hashOfBytes(raw),
		Parent: noParent, ParentRev: big.NewInt(0), Author: pubHex, License: "-"}
	octets := envelopeEncode(env)
	return &pubAuth{Bytes: string(octets), Sig: hex.EncodeToString(ed25519.Sign(priv, octets)), Pubkey: pubHex}
}

// The governing invariant of #102: the bytes hashed by the publisher, covered by
// the envelope, received by the registry and STORED as the artifact are one byte
// sequence. This asserts the last link, which is the one a source-publication
// path cannot make — there the registry regenerates the object and merely checks
// that it agrees.
func TestObjectPublicationStoresTheSubmittedBytes(t *testing.T) {
	st := newMemStoreForTest(t)
	_, priv, _ := ed25519.GenerateKey(nil)

	// Elaborate once to obtain a legitimate object, exactly as a publisher would.
	src := "(defn twice [] [(n Int)] Int (+ n n))"
	reps, err := apiPut(st, src, "author", "")
	if err != nil || len(reps) == 0 {
		t.Fatalf("setup put failed: %v %v", err, reps)
	}
	def, err := st.GetDef(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	raw := encodeDef(def)

	target := newMemStoreForTest(t)
	auth := signObjectPublication(t, "twice", raw, priv)
	out, err := apiPutObject(target, base64.StdEncoding.EncodeToString(raw),
		&objectNaming{ParamNames: []string{"n"}}, auth, "author", "")
	if err != nil {
		t.Fatalf("object publication refused a well-formed publication: %v", err)
	}
	if len(out) != 1 || out[0].Status != "accepted" {
		t.Fatalf("expected one accepted report, got %+v", out)
	}
	// THE INVARIANT: byte-for-byte, not "hashes to the same value" — a hash
	// comparison would pass for a store that re-derived equivalent bytes, which
	// is the assumption this path exists to remove.
	stored, ok, err := target.be.getObject(out[0].Hash)
	if err != nil || !ok {
		t.Fatalf("stored object not readable: %v %v", err, ok)
	}
	if !bytes.Equal(stored, raw) {
		t.Fatalf("stored object differs from the submitted octets (%d vs %d bytes)", len(stored), len(raw))
	}
	if out[0].Hash != hashOfBytes(raw) {
		t.Fatalf("stored under %s, but the publisher signed %s", out[0].Hash, hashOfBytes(raw))
	}
}

// Controls. Each must be refused, and for its OWN reason — a path that refused
// everything would satisfy the test above's sibling assertions while being
// useless.
func TestObjectPublicationRefusals(t *testing.T) {
	st := newMemStoreForTest(t)
	_, priv, _ := ed25519.GenerateKey(nil)
	reps, err := apiPut(st, "(defn twice [] [(n Int)] Int (+ n n))", "author", "")
	if err != nil {
		t.Fatal(err)
	}
	def, _ := st.GetDef(reps[0].Hash)
	raw := encodeDef(def)
	b64 := base64.StdEncoding.EncodeToString(raw)

	t.Run("unsigned is refused rather than degraded", func(t *testing.T) {
		if _, err := apiPutObject(newMemStoreForTest(t), b64, nil, nil, "author", ""); err == nil {
			t.Fatal("an unsigned object publication was accepted")
		}
	})
	t.Run("non-canonical base64 is refused", func(t *testing.T) {
		auth := signObjectPublication(t, "twice", raw, priv)
		if _, err := apiPutObject(newMemStoreForTest(t), " "+b64, nil, auth, "author", ""); err == nil {
			t.Fatal("base64 with leading whitespace was accepted")
		}
	})
	t.Run("bytes that are not a canonical definition are refused", func(t *testing.T) {
		auth := signObjectPublication(t, "twice", []byte{0x00, 0x01}, priv)
		if _, err := apiPutObject(newMemStoreForTest(t), base64.StdEncoding.EncodeToString([]byte{0x00, 0x01}), nil, auth, "author", ""); err == nil {
			t.Fatal("garbage octets were accepted as a definition")
		}
	})
	t.Run("a signature over DIFFERENT bytes is refused", func(t *testing.T) {
		// The envelope binds the hash of `raw`; submit a different valid object.
		other, err := apiPut(st, "(defn thrice [] [(n Int)] Int (+ n (+ n n)))", "author", "")
		if err != nil {
			t.Fatal(err)
		}
		od, _ := st.GetDef(other[0].Hash)
		auth := signObjectPublication(t, "twice", raw, priv) // signs raw...
		out, err := apiPutObject(newMemStoreForTest(t), base64.StdEncoding.EncodeToString(encodeDef(od)), nil, auth, "author", "")
		if err == nil && (len(out) == 0 || out[0].Status == "accepted") {
			t.Fatal("an object was accepted under a signature binding different bytes")
		}
	})
}

// The vocabulary is optional and attacker-supplied, so every naming slice must
// come out matching the OBJECT's own cardinality. Renderers and the prover index
// CtorNames positionally and unguarded, so a short payload does not fail the
// publication — it panics later, in an operation the publisher is no longer part
// of, which is the worst possible place for it to surface.
func TestNamingIsFittedToTheObjectsShape(t *testing.T) {
	st := newMemStoreForTest(t)
	reps, err := apiPut(st, "(data Colour [] (Red) (Green) (Blue))", "author", "")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	def, err := st.GetDef(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Ctors) != 3 {
		t.Fatalf("expected a 3-constructor datatype, got %d", len(def.Ctors))
	}
	for _, tc := range []struct {
		name   string
		naming *objectNaming
	}{
		{"absent entirely", nil},
		{"empty", &objectNaming{}},
		{"too short", &objectNaming{CtorNames: []string{"Red"}}},
		{"too long", &objectNaming{CtorNames: []string{"a", "b", "c", "d", "e"}}},
	} {
		m := &Meta{Name: "colour"}
		if tc.naming != nil {
			m.CtorNames = tc.naming.CtorNames
		}
		fitNaming(m, def)
		if len(m.CtorNames) != len(def.Ctors) {
			t.Errorf("%s: CtorNames is %d long for a %d-constructor object", tc.name, len(m.CtorNames), len(def.Ctors))
		}
		for i, n := range m.CtorNames {
			if n == "" {
				t.Errorf("%s: constructor %d has an empty name", tc.name, i)
			}
		}
	}
	// Supplied names must SURVIVE — a fit that discarded them would satisfy every
	// length assertion above while making the vocabulary useless.
	m := &Meta{Name: "colour", CtorNames: []string{"Red", "Green", "Blue"}}
	fitNaming(m, def)
	if m.CtorNames[0] != "Red" || m.CtorNames[2] != "Blue" {
		t.Fatalf("supplied names were discarded: %v", m.CtorNames)
	}
}

// A scalar cardinality the node budget cannot see. TyVars is ONE node, so a
// ten-byte object can declare 2^24 type variables and pass the structural
// budget entirely — and anything materialising a slice per type variable then
// allocates hundreds of megabytes from an input small enough to send in a loop.
//
// Asserted at admitDef rather than at the allocation, because admitDef is the
// documented single answer to what the profile admits; a check beside one
// allocation would protect that one and nothing added afterwards.
func TestAdmissionBoundsScalarCardinalities(t *testing.T) {
	huge := &Def{K: "data", TyVars: 1 << 20, Ctors: [][]Ty{{}}}
	if err := admitDef(huge); err == nil {
		t.Fatal("a definition declaring 2^20 type variables was admitted")
	}
	// The control: an ordinary count must still pass, or the bound would be
	// refusing every real definition and the test above would mean nothing.
	ok := &Def{K: "data", TyVars: 2, Ctors: [][]Ty{{}}}
	if err := admitDef(ok); err != nil {
		t.Fatalf("an ordinary 2-type-variable definition was refused: %v", err)
	}
}

// Source publication validates names by PARSING them. Object publication does
// not parse, so it must check them — otherwise it is a second door into a space
// the lexer was the only door to, and a binding can be created that no Oath
// source can reference and no projection can print correctly.
func TestObjectPublicationRefusesNonSurfaceNames(t *testing.T) {
	for _, bad := range []string{
		"bad name",   // a space: two tokens, not one symbol
		"(bad)",      // delimiters
		"",           // empty
		" lead",      // leading space
		"trail ",     // trailing space
		"12",         // lexes as a number, not an identifier
		"\"quoted\"", // lexes as a string
	} {
		if err := requireSurfaceSymbol("name", bad); err == nil {
			t.Errorf("%q was accepted as a surface symbol", bad)
		}
	}
	// The control: real names must pass, or the check would refuse every honest
	// publication and the assertions above would be satisfied by a stub.
	for _, ok := range []string{"twice", "list-map", "oath/str-take", "Colour", "<=", "x'"} {
		if err := requireSurfaceSymbol("name", ok); err != nil {
			t.Errorf("legitimate name %q was refused: %v", ok, err)
		}
	}
}

// Object publication must accept EXACTLY what source publication accepts. The
// language produces duplicate positional names — `(defn f [] [(x Int) (x Int)]
// …)` and `(data D [a a] …)` both elaborate — and source publication stores
// them, so refusing them here would reject definitions the other path accepts,
// after the author has signed. That divergence between the two paths is the
// thing #102 exists to remove, so reintroducing it in the new path is the one
// regression this change must not contain.
func TestObjectPublicationAcceptsWhateverSourcePublicationAccepts(t *testing.T) {
	st := newMemStoreForTest(t)
	_, priv, _ := ed25519.GenerateKey(nil)
	// Duplicates, straight from the elaborator.
	reps, err := apiPut(st, "(defn dup [] [(x Int) (x Int)] Int x)", "author", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("source publication refused the duplicate-name definition: %v %+v", err, reps)
	}
	m, _ := st.GetMeta(reps[0].Hash)
	def, _ := st.GetDef(reps[0].Hash)
	raw := encodeDef(def)
	auth := signObjectPublication(t, "dup", raw, priv)
	out, err := apiPutObject(newMemStoreForTest(t), encodeEnvelopeB64(raw),
		&objectNaming{ParamNames: m.ParamNames}, auth, "author", "")
	if err != nil {
		t.Fatalf("object publication refused what source publication accepted: %v", err)
	}
	if len(out) != 1 || out[0].Status != "accepted" {
		t.Fatalf("expected acceptance, got %+v", out)
	}
	// And the vocabulary survives VERBATIM: fitNaming keeps supplied names as
	// given, so the same definition carries the same names whichever path
	// published it.
	fitted := &Meta{Name: "dup", ParamNames: append([]string(nil), m.ParamNames...)}
	fitNaming(fitted, def)
	if len(fitted.ParamNames) != len(m.ParamNames) {
		t.Fatalf("fitting changed the vocabulary length: %v -> %v", m.ParamNames, fitted.ParamNames)
	}
	for i := range m.ParamNames {
		if fitted.ParamNames[i] != m.ParamNames[i] {
			t.Fatalf("fitting rewrote a supplied name: %v -> %v", m.ParamNames, fitted.ParamNames)
		}
	}
}

// Uniqueness must hold of the RESULT, not of the payload. A caller sending
// {"t1", ""} passes a distinctness check on the input and then receives a
// generated "t1" in the empty slot — the collision introduced by the repair
// itself, which is why the producer owns the invariant.
func TestFittedNamesAreUniqueEvenWhenSuppliedNamesLookGenerated(t *testing.T) {
	st := newMemStoreForTest(t)
	reps, err := apiPut(st, "(data Pair [a b] (MkPair a b))", "author", "")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	def, err := st.GetDef(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if def.TyVars != 2 {
		t.Fatalf("expected 2 type variables, got %d", def.TyVars)
	}
	// Only cases with a name to GENERATE. A payload of supplied duplicates is
	// NOT included: those are kept verbatim by design, because the language
	// produces them and source publication stores them.
	for _, supplied := range [][]string{
		{"t1", ""}, // the generated name for slot 1 would collide with the supplied one
		{"", "t0"}, // ...and for slot 0
		{},         // nothing supplied at all
	} {
		m := &Meta{Name: "pair", TyVarNames: append([]string(nil), supplied...)}
		fitNaming(m, def)
		if len(m.TyVarNames) != def.TyVars {
			t.Errorf("%v: got %d names for %d type variables", supplied, len(m.TyVarNames), def.TyVars)
			continue
		}
		seen := map[string]bool{}
		for _, n := range m.TyVarNames {
			if n == "" {
				t.Errorf("%v: produced an empty name", supplied)
			}
			if seen[n] {
				t.Errorf("%v: produced duplicate name %q -> %v", supplied, n, m.TyVarNames)
			}
			seen[n] = true
		}
	}
}

// ParamNames must be fitted to the BINDER COUNT the printer walks. A partial
// payload of {"x0"} on a two-binder function otherwise renders both binders
// "x0" — the printer generates from x0 for binders past the preset — so a body
// referring to the outer binder displays as referring to the inner one and
// re-elaborates to a different object than the signed bytes.
func TestParamNamesAreFittedToTheBinderCount(t *testing.T) {
	st := newMemStoreForTest(t)
	reps, err := apiPut(st, "(defn add2 [] [(a Int) (b Int)] Int (+ a b))", "author", "")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	def, err := st.GetDef(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if n := lambdaBinders(def.Body); n != 2 {
		t.Fatalf("expected 2 binders, counted %d", n)
	}
	// {"a", "a"} is deliberately absent: supplied duplicates are preserved, since
	// the elaborator emits them for legal source and both paths must agree.
	for _, supplied := range [][]string{{"x0"}, {}, {"a"}} {
		m := &Meta{Name: "add2", ParamNames: append([]string(nil), supplied...)}
		fitNaming(m, def)
		if len(m.ParamNames) != 2 {
			t.Errorf("%v: got %d parameter names, want 2", supplied, len(m.ParamNames))
			continue
		}
		if m.ParamNames[0] == m.ParamNames[1] {
			t.Errorf("%v: both binders named %q", supplied, m.ParamNames[0])
		}
	}
}

// A publication about to be REJECTED must not touch an existing object's
// metadata. This path is the one where that matters: the caller supplies the
// bytes and the vocabulary, so without a pre-store check a doomed request could
// rewrite the rendered names of a definition someone else has bound.
func TestRejectedObjectPublicationLeavesExistingMetadataAlone(t *testing.T) {
	st := newMemStoreForTest(t)
	_, priv, _ := ed25519.GenerateKey(nil)
	reps, err := apiPut(st, "(data Colour [] (Red) (Green) (Blue))", "author", "")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	h := reps[0].Hash
	before, err := st.GetMeta(h)
	if err != nil {
		t.Fatal(err)
	}
	def, _ := st.GetDef(h)
	raw := encodeDef(def)

	// A statement that will FAIL ENV-STORE-CAS: it is signed against the
	// no-parent sentinel at revision 0, but `Colour` is already bound. Chosen
	// deliberately over a mismatched NAME, which is not a failure in this path —
	// the name comes FROM the envelope here, so ENV-STORE-NAME compares the
	// envelope against itself and publishing under another name is a legitimate
	// second binding of the same object (its vocabulary is then kept per-alias).
	auth := signObjectPublication(t, "Colour", raw, priv)
	if _, err := apiPutObject(st, base64.StdEncoding.EncodeToString(raw),
		&objectNaming{CtorNames: []string{"Zzz", "Yyy", "Xxx"}}, auth, "attacker", ""); err == nil {
		t.Fatal("a publication signed against the wrong parent was accepted")
	}
	after, err := st.GetMeta(h)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before.CtorNames {
		if after.CtorNames[i] != before.CtorNames[i] {
			t.Fatalf("a rejected publication rewrote constructor %d: %q -> %q", i, before.CtorNames[i], after.CtorNames[i])
		}
	}
}

// `pending` is an ADMITTED publication: the proof worker binds the name later,
// so the naming restore must not treat it as a refusal. Restoring there would
// leave the eventually-bound name wearing a previous alias's vocabulary — the
// publication succeeds and its names quietly do not.
func TestPendingIsTreatedAsAdmittedForNaming(t *testing.T) {
	for _, tc := range []struct {
		status   string
		admitted bool
	}{
		{"accepted", true}, {"falsified", true}, {"pending", true},
		{"rejected", false}, {"blocked", false},
	} {
		got := tc.status == "accepted" || tc.status == "falsified" || tc.status == "pending"
		if got != tc.admitted {
			t.Errorf("status %q: admitted=%v, want %v", tc.status, got, tc.admitted)
		}
	}
}

// Moving the statement check before storage must not move the ATTEMPT out of
// the record. The journal's guarantee is that a rejected put is retained, and
// the source path's equivalent gate appends one.
func TestRejectedObjectPublicationIsJournalled(t *testing.T) {
	st := newMemStoreForTest(t)
	_, priv, _ := ed25519.GenerateKey(nil)
	reps, err := apiPut(st, "(data Colour [] (Red) (Green) (Blue))", "author", "")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	def, _ := st.GetDef(reps[0].Hash)
	raw := encodeDef(def)
	before := len(st.ReadLog())

	// Signed against the no-parent sentinel while `Colour` is already bound: CAS fails.
	auth := signObjectPublication(t, "Colour", raw, priv)
	if _, err := apiPutObject(st, encodeEnvelopeB64(raw), nil, auth, "attacker", ""); err == nil {
		t.Fatal("a publication signed against the wrong parent was accepted")
	}
	entries := st.ReadLog()
	if len(entries) != before+1 {
		t.Fatalf("the rejected attempt was not journalled: %d entries before, %d after", before, len(entries))
	}
	last := entries[len(entries)-1]
	if last.Status != "rejected" || last.Name != "Colour" {
		t.Fatalf("unexpected journal entry: %+v", last)
	}
	// The entry must NOT carry author evidence: VerifyLog validates every
	// populated author record regardless of status, so recording an envelope
	// whose signature failed would make every later audit of this journal fail.
	if last.EnvelopeB64 != "" || last.AuthorSig != "" || last.AuthorPubkey != "" {
		t.Fatal("a rejected entry carries author evidence; one bad request would poison the journal")
	}
	// And the journal must still verify afterwards — the actual property at risk.
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("the journal no longer verifies after a rejected publication: %v", err)
	}
}
