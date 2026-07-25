package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A put made with a signer attaches the signer's pubkey and a valid Ed25519
// signature to every journal entry, and VerifyLog accepts them. Authorship is
// the signature, not the label: the host stores signed bytes and holds no
// secret (docs/registry-auth.md).
func TestSignedPutIsVerifiable(t *testing.T) {
	st := newStore(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSigner(priv)
	put(t, st, `(defn one [] [] Int 1)`)
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("signed journal failed verification: %v", err)
	}
	entries := st.ReadLog()
	if len(entries) == 0 {
		t.Fatal("no journal entries")
	}
	wantPub := hex.EncodeToString(pub)
	for _, e := range entries {
		if e.Pubkey != wantPub {
			t.Fatalf("entry %d pubkey = %q, want %q", e.Seq, e.Pubkey, wantPub)
		}
		if e.Sig == "" {
			t.Fatalf("entry %d is unsigned", e.Seq)
		}
	}
}

// Tampering with a signed entry's authored fields (here, the author label)
// breaks signature verification independently of the hash chain — re-chaining a
// forged entry does not make it verify, because the forger cannot produce a
// signature under the victim's pubkey.
func TestSignedPutTamperDetected(t *testing.T) {
	st := newStore(t)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSigner(priv)
	put(t, st, `(defn one [] [] Int 1)`)

	logPath := filepath.Join(st.Root, "log.jsonl")
	pristine, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimRight(string(pristine), "\n"), "\n")
	var e LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatal(err)
	}
	// Forge the author but re-chain so the hash chain still checks out. Only the
	// signature can catch this.
	e.Author = "mallory"
	e.Chain = ""
	body, _ := json.Marshal(e)
	e.Chain = chainHash(chainAnchor(nil), body)
	edited, _ := json.Marshal(e)
	os.WriteFile(logPath, []byte(string(edited)+"\n"), 0o644)

	if err := st.VerifyLog(); err == nil {
		t.Fatal("tampered signed entry passed verification")
	}
}

// A pubkey without a matching signature is a forged attribution: claiming a
// principal you cannot sign for must fail, not pass as attributed.
func TestForgedPubkeyRejected(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn one [] [] Int 1)`) // unsigned to start

	logPath := filepath.Join(st.Root, "log.jsonl")
	pristine, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimRight(string(pristine), "\n"), "\n")
	var e LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(nil)
	e.Pubkey = hex.EncodeToString(pub) // claim a principal, no valid signature
	e.Chain = ""
	body, _ := json.Marshal(e)
	e.Chain = chainHash(chainAnchor(nil), body)
	edited, _ := json.Marshal(e)
	os.WriteFile(logPath, []byte(string(edited)+"\n"), 0o644)

	if err := st.VerifyLog(); err == nil {
		t.Fatal("entry with a pubkey but no valid signature passed verification")
	}
}

// Unsigned puts remain valid — they are unattributed, not rejected. Signature
// is opt-in; the substrate keeps working without keys.
func TestUnsignedPutStillVerifies(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn one [] [] Int 1)`)
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("unsigned journal failed verification: %v", err)
	}
	for _, e := range st.ReadLog() {
		if e.Pubkey != "" || e.Sig != "" {
			t.Fatalf("unsigned entry %d carries signature material", e.Seq)
		}
	}
}

// loadSigningKey round-trips both the 64-byte key form and the 32-byte seed
// form to the same principal, and the seed form is written by NewKeyFromSeed.
func TestLoadSigningKeyFormats(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	wantPub := hex.EncodeToString(pub)
	dir := t.TempDir()

	keyPath := filepath.Join(dir, "full.key")
	os.WriteFile(keyPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600)
	if _, gotPub := loadSigningKey(keyPath); gotPub != wantPub {
		t.Fatalf("64-byte key: pubkey %q, want %q", gotPub, wantPub)
	}

	seedPath := filepath.Join(dir, "seed.key")
	os.WriteFile(seedPath, []byte(hex.EncodeToString(priv.Seed())), 0o600)
	if _, gotPub := loadSigningKey(seedPath); gotPub != wantPub {
		t.Fatalf("32-byte seed: pubkey %q, want %q", gotPub, wantPub)
	}
}
