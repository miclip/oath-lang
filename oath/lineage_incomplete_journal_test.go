package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A journal with an UNPARSEABLE line must not yield a positive verdict.
//
// ReadLog drops a malformed line silently, and this derivation replays
// POSITIONS -- so a hole let a later signed entry look like a first
// transition and produced KEY_SIGNED for a history that was never seen.
// Evidence absent from the record cannot support a claim about the record.
//
// The control is the point: the SAME journal without the corrupt line does
// reach KEY_SIGNED, so what the test observes is the hole changing the
// verdict, not a derivation that returns UNKNOWN for unrelated reasons.
func TestLineageRefusesAnUnparseableJournal(t *testing.T) {
	build := func(t *testing.T, corrupt string) (props, body lineageTier) {
		t.Helper()
		dir := t.TempDir()
		st, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		// StoreObject + Repoint, NOT apiPut: apiPut journals its own unsigned
		// entry, which would become the establishing one and make the signed
		// entry below irrelevant. The journal here is entirely this test's.
		// ldef carries no Ty (its own callers never store), so spell the def out.
		d := &Def{K: "func", Ty: tFun(tInt(), tInt()),
			Body: &Term{K: "lam", Ty: tInt(), A: &Term{K: "var", Idx: 0}}}
		h, err := st.StoreObject(d, &Meta{Name: "f"})
		if err != nil {
			t.Fatal(err)
		}
		pub, priv, _ := ed25519.GenerateKey(nil)
		pubHex := hex.EncodeToString(pub)
		env := pubEnvelope{Op: "put", Name: "f", Artifact: h, Parent: noParent,
			ParentRev: big.NewInt(0), Author: pubHex, License: noLicense}
		octets := envelopeEncode(env)
		sig := hex.EncodeToString(ed25519.Sign(priv, octets))
		if err := st.AppendLog(&LogEntry{
			Author: pubHex, Name: "f", Kind: "func", Status: "accepted", Hash: h,
			EnvelopeB64: encodeEnvelopeB64(octets), AuthorPubkey: pubHex, AuthorSig: sig,
		}); err != nil {
			t.Fatal(err)
		}
		if corrupt != "" {
			p := filepath.Join(dir, "log.jsonl")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, append([]byte(corrupt), b...), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, whole := st.readLogWhole(); whole {
				t.Fatalf("the damage %q read as a whole journal, so this case sees nothing", corrupt)
			}
		}
		if _, err := st.Repoint("f", h); err != nil {
			t.Fatal(err)
		}
		return lineageEvidenceAt(st, "f", h)
	}

	// CONTROL first: without the hole, this journal reaches KEY_SIGNED.
	p, b := build(t, "")
	if p.Tier != lineageKeySigned || b.Tier != lineageKeySigned {
		t.Fatalf("control did not reach KEY_SIGNED (props=%+v body=%+v); the corrupt cases below would prove nothing", p, b)
	}

	// Two shapes of damage. The BLANK line is the one that looks like
	// whitespace: a truncated entry reduced to its newline leaves exactly this,
	// and skipping it as empty would renumber every later position while still
	// reporting a whole journal.
	// The blank-line shape needs a line BEFORE it, or TrimSpace removes it as
	// leading whitespace and no interior hole exists — which is why the prefix
	// carries a valid entry for an unrelated name. That entry also puts the
	// signed entry at position 2, so a reader that skipped the blank would
	// renumber it to 1: exactly the renumbering this refuses.
	damages := []string{
		"{\"seq\":1,\"na\n",
		"{\"seq\":1,\"name\":\"other\",\"status\":\"refused\"}\n\n",
		// A LEADING blank line. Trimming arbitrary edge whitespace would delete
		// this hole before the check saw it, so the edges must be treated like
		// the middle — only the single record terminator comes off.
		"\n",
		// JSON `null`. This one DECODES: it unmarshals into a zero LogEntry, so
		// a check asking only "did decoding fail?" reports a whole journal. It
		// is why the condition requires an OBJECT rather than a successful
		// decode, and it is the case that stopped the enumeration.
		"null\n",
		// Valid JSON of the wrong SHAPE, which the same condition covers
		// without anyone having enumerated it.
		"[1,2,3]\n",
		"\"a string\"\n",
	}
	for _, damage := range damages {
		p, b := build(t, damage)
		for _, got := range []lineageTier{p, b} {
			if got.Tier != lineageUnknown {
				t.Fatalf("damage %q yielded %q; a hole must not support a positive verdict", damage, got.Tier)
			}
			if !strings.Contains(got.Reason, "does not parse") {
				t.Fatalf("damage %q gave reason %q, which does not name the unreadable line", damage, got.Reason)
			}
		}
	}
}

// Presence is a claim about the STORE, not about what this process remembers.
// GetDef answers from an in-memory cache, so a definition loaded before its
// object was deleted still reads as present -- and a derivation rested on that
// would report KEY_SIGNED for a store that no longer holds the object.
func TestLineagePresenceIsNotDecidedByTheCache(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &Def{K: "func", Ty: tFun(tInt(), tInt()),
		Body: &Term{K: "lam", Ty: tInt(), A: &Term{K: "var", Idx: 0}}}
	h, err := st.StoreObject(d, &Meta{Name: "f"})
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	env := pubEnvelope{Op: "put", Name: "f", Artifact: h, Parent: noParent,
		ParentRev: big.NewInt(0), Author: pubHex, License: noLicense}
	octets := envelopeEncode(env)
	if err := st.AppendLog(&LogEntry{
		Author: pubHex, Name: "f", Kind: "func", Status: "accepted", Hash: h,
		EnvelopeB64: encodeEnvelopeB64(octets), AuthorPubkey: pubHex,
		AuthorSig: hex.EncodeToString(ed25519.Sign(priv, octets)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repoint("f", h); err != nil {
		t.Fatal(err)
	}

	// CONTROL: with the object on disk this reaches KEY_SIGNED.
	if p, _ := lineageEvidenceAt(st, "f", h); p.Tier != lineageKeySigned {
		t.Fatalf("control did not reach KEY_SIGNED (%+v); the deletion below proves nothing", p)
	}

	// Warm the cache, then delete the object from under it.
	if _, err := st.GetDef(h); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "objects", h+".bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDef(h); err != nil {
		t.Fatalf("setup: GetDef stopped answering from cache, so this test sees nothing: %v", err)
	}

	p, b := lineageEvidenceAt(st, "f", h)
	for _, got := range []lineageTier{p, b} {
		if got.Tier != lineageUnknown {
			t.Fatalf("a deleted object still yielded %q; presence was decided by the cache", got.Tier)
		}
	}
}
