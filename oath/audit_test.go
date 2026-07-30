package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

// newMemStoreForTest gives each case its own empty journal, so a rejection is
// attributable to the perturbation under test rather than to a shared prefix.
func newMemStoreForTest(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return st
}

// Build a store whose journal carries one signed publication, then perturb the
// author record in each way that must be rejected. These are the invariants that
// make the record evidence rather than decoration: a partial record attests to
// nothing, and an envelope disagreeing with its entry means the registry recorded
// a transition its author did not sign.
func TestVerifyLogAuthorRecord(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	artifact := strings.Repeat("a", 64)

	// A well-formed signed entry, used as the baseline every case perturbs.
	base := func() *LogEntry {
		env := pubEnvelope{Op: "put", Name: "n", Artifact: artifact,
			Parent: noParent, ParentRev: firstRev(), Author: pubHex}
		raw := envelopeEncode(env)
		sig, err := envelopeSign(priv, env)
		if err != nil {
			t.Fatal(err)
		}
		return &LogEntry{
			Author: pubHex, Name: "n", Kind: "func", Status: "accepted",
			Hash: artifact, EnvelopeB64: encodeEnvelopeB64(raw), AuthorPubkey: pubHex, AuthorSig: sig,
		}
	}

	// Baseline must verify, or the negative cases below prove nothing.
	st := newMemStoreForTest(t)
	if err := st.AppendLog(base()); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("an honestly signed publication failed to verify: %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*LogEntry)
		wantErr string
	}{
		{"envelope without signature", func(e *LogEntry) { e.AuthorSig = "" }, "partial author record"},
		{"signature without envelope", func(e *LogEntry) { e.EnvelopeB64 = "" }, "partial author record"},
		{"key disagrees with envelope", func(e *LogEntry) { e.AuthorPubkey = strings.Repeat("b", 64) }, "envelope names author"},
		{"entry name differs from signed name", func(e *LogEntry) { e.Name = "other" }, "author signed name"},
		{"entry artifact differs from signed artifact", func(e *LogEntry) { e.Hash = strings.Repeat("c", 64) }, "author signed artifact"},
		{"entry claims a parent the author did not sign", func(e *LogEntry) { e.Prev = strings.Repeat("d", 64) }, "author signed parent"},
		{"envelope octets altered", func(e *LogEntry) {
			oct, _ := decodeEnvelopeB64(e.EnvelopeB64)
			e.EnvelopeB64 = encodeEnvelopeB64([]byte(strings.Replace(string(oct), "name=n", "name=z", 1)))
		}, "does not verify"},
		{"non-canonical base64 (whitespace)", func(e *LogEntry) { e.EnvelopeB64 = " " + e.EnvelopeB64 }, "not standard padded base64"},
	} {
		st := newMemStoreForTest(t)
		e := base()
		tc.mutate(e)
		if err := st.AppendLog(e); err != nil {
			t.Fatal(err)
		}
		err := st.VerifyLog()
		if err == nil {
			t.Fatalf("%s: VerifyLog accepted it — the author record would be decoration, not evidence", tc.name)
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: got %q, wanted it to mention %q", tc.name, err, tc.wantErr)
		}
	}
}

// An entry with NO author record at all must still verify: the corpus is full of
// them, and they are honestly unattributed rather than invalid.
func TestVerifyLogAllowsUnauthoredEntries(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{
		Author: "claude-main", Name: "n", Kind: "func", Status: "accepted",
		Hash: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("an unsigned entry must verify (it attests to nothing, but it is not corrupt): %v", err)
	}
}
