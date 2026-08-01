package main

import (
	"context"
	"crypto/ed25519"
	"os"
	"testing"
)

// OPT-IN. Requires live KMS credentials, so it is skipped unless OATH_KMS_KEY is
// set — ordinary CI must not depend on a cloud round-trip, and a test that
// silently passes because it was skipped would be worse than no test.
//
//	OATH_KMS_KEY=projects/.../cryptoKeyVersions/1 \
//	OATH_KMS_TEST_FILE_KEY=~/.oath/keys/oath-project.key \
//	go test -run TestKMSSignerMatchesFileSigner ./...
//
// THE ASSERTION IS BYTE-IDENTICAL OUTPUT, not "both verify". Ed25519 is
// deterministic, so two signers over the same message must produce the same 64
// bytes. A signer that pre-hashed, padded, or length-prefixed would still
// produce something that verifies UNDER ITS OWN SCHEME — only equality against a
// known-good signer proves it signs what the kernel signs.
func TestKMSSignerMatchesFileSigner(t *testing.T) {
	res := os.Getenv("OATH_KMS_KEY")
	keyFile := os.Getenv("OATH_KMS_TEST_FILE_KEY")
	if res == "" || keyFile == "" {
		t.Skip("set OATH_KMS_KEY and OATH_KMS_TEST_FILE_KEY to run the live KMS equivalence test")
	}
	ctx := context.Background()

	kms, err := NewKMSSigner(res)
	if err != nil {
		t.Fatal(err)
	}
	file, err := NewFileSigner(keyFile)
	if err != nil {
		t.Fatal(err)
	}

	kpub, err := kms.PublicKey(ctx)
	if err != nil {
		t.Fatalf("KMS public key: %v", err)
	}
	fpub, _ := file.PublicKey(ctx)
	if string(kpub) != string(fpub) {
		t.Fatalf("the two signers hold DIFFERENT keys; they cannot act for the same principal")
	}

	// A real canonical Oath statement, not a random blob — the bytes this key
	// actually signs in production.
	msg := resEncode(resEnvelope{Op: opReserve, Namespace: "oath/*",
		Authority: noAuthority, AuthorityRev: firstRev(), Pubkey: hexOf(kpub)})

	ksig, err := kms.Sign(ctx, msg)
	if err != nil {
		t.Fatalf("KMS sign: %v", err)
	}
	fsig, _ := file.Sign(ctx, msg)
	if string(ksig) != string(fsig) {
		t.Errorf("signatures DIFFER over identical bytes:\n  kms  %x\n  file %x\nEd25519 is deterministic, so the KMS signer is transforming the message", ksig, fsig)
	}
	if !ed25519.Verify(kpub, msg, ksig) {
		t.Error("the kernel's own verifier rejects the KMS signature")
	}
}

// A resource naming no key VERSION must be refused: a movable primary alias would
// let a rotation silently change which principal signs.
func TestKMSSignerRequiresAnExactKeyVersion(t *testing.T) {
	if _, err := NewKMSSigner("projects/p/locations/l/keyRings/r/cryptoKeys/k"); err == nil {
		t.Error("accepted a crypto-key alias with no version")
	}
	if _, err := NewKMSSigner("projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"); err != nil {
		t.Errorf("refused a well-formed version resource: %v", err)
	}
}

// --key and --kms-key must never both apply: which key signs cannot be ambiguous.
func TestSignerFlagsAreMutuallyExclusive(t *testing.T) {
	if _, err := resolveSigner("some.key", "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"); err == nil {
		t.Error("accepted both --key and --kms-key")
	}
	if _, err := resolveSigner("", ""); err == nil {
		t.Error("accepted neither")
	}
}

func hexOf(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, h[c>>4], h[c&15])
	}
	return string(out)
}
