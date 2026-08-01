package main

// The signing seam: publication and reservation depend on WHO SIGNS, never on
// WHERE THE KEY LIVES.
//
// Before this existed, `--key <file>` was not a flag but an assumption baked
// through every signing path — which meant moving the oath/* authority key into
// a managed signer could not remove the local private copy, because nothing
// could sign without one. A temporary custody exception would have become the
// permanent operating model by default.
//
// WHY KMSSigner SHELLS OUT TO gcloud RATHER THAN IMPORTING A CLOUD SDK. The
// default build of this kernel has no dependencies, and that is a property worth
// more than convenience here. It also means THE KERNEL NEVER HANDLES
// CREDENTIALS: it asks an already-authenticated tool for a signature and
// receives 64 bytes. There is no token, no service-account file, and no refresh
// logic anywhere in this package.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Signer produces Oath signatures. Deliberately three methods: a signing path
// that cannot state WHOSE key it is using cannot check that the key matches the
// statement, and one that cannot describe itself cannot tell an operator what is
// about to sign on their behalf.
type Signer interface {
	// PublicKey returns the raw 32-byte Ed25519 public key.
	PublicKey(ctx context.Context) (ed25519.PublicKey, error)
	// Sign returns a signature over the EXACT bytes given. Implementations MUST
	// NOT hash, pad, length-prefix, or otherwise transform the message: the Oath
	// signature convention is PureEdDSA over canonical octets, and a signer that
	// pre-hashed would produce signatures that verify under its own scheme and
	// nowhere else.
	Sign(ctx context.Context, message []byte) ([]byte, error)
	// Description is shown to a human before anything is signed.
	Description() string
}

// FileSigner signs with a private key held on disk.
type FileSigner struct {
	priv ed25519.PrivateKey
	path string
}

func NewFileSigner(path string) (*FileSigner, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading signing key %s: %w", path, err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("signing key %s is not valid hex: %w", path, err)
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	default:
		return nil, fmt.Errorf("signing key %s has wrong length %d", path, len(raw))
	}
	return &FileSigner{priv: priv, path: path}, nil
}

func (f *FileSigner) PublicKey(context.Context) (ed25519.PublicKey, error) {
	return f.priv.Public().(ed25519.PublicKey), nil
}

func (f *FileSigner) Sign(_ context.Context, msg []byte) ([]byte, error) {
	return ed25519.Sign(f.priv, msg), nil
}

func (f *FileSigner) Description() string {
	return fmt.Sprintf("local private key file %s", f.path)
}

// KMSSigner signs through Cloud KMS. The private key is non-exportable and never
// reaches this process.
type KMSSigner struct {
	// Resource is the FULL resource name INCLUDING the key version:
	//   projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoKeyVersions/N
	//
	// The version is required rather than optional. Naming only the crypto key
	// would let a future primary-version rotation silently change the signing
	// principal — and since a namespace is bound to a public key permanently, a
	// silently rotated signer would simply stop being able to act for it, at some
	// later moment, for reasons invisible at the call site.
	Resource string
}

func NewKMSSigner(resource string) (*KMSSigner, error) {
	if !strings.Contains(resource, "/cryptoKeyVersions/") {
		return nil, fmt.Errorf("KMS resource %q names no key VERSION: pass the full "+
			"projects/.../cryptoKeyVersions/N path. Naming only the crypto key would let a "+
			"primary-version rotation silently change which principal signs", resource)
	}
	return &KMSSigner{Resource: resource}, nil
}

// kmsParts splits the version resource into the flags gcloud wants.
func (k *KMSSigner) kmsParts() (version, key, keyring, location string, err error) {
	p := strings.Split(k.Resource, "/")
	if len(p) != 10 || p[0] != "projects" || p[2] != "locations" || p[4] != "keyRings" ||
		p[6] != "cryptoKeys" || p[8] != "cryptoKeyVersions" {
		return "", "", "", "", fmt.Errorf("malformed KMS resource %q", k.Resource)
	}
	return p[9], p[7], p[5], p[3], nil
}

// kmsTimeout bounds every subprocess. A hung gcloud must fail CLOSED rather than
// block a publication indefinitely — an operator who walks away from a stalled
// signing prompt should return to a refusal, not to an open one.
const kmsTimeout = 60 * time.Second

func (k *KMSSigner) run(parent context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, kmsTimeout)
	defer cancel()
	// An ARGUMENT ARRAY, never `sh -c`: a resource name or path must never be able
	// to become shell syntax.
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		// NO FALLBACK. A KMS failure is an abort, never a quiet retreat to a local
		// key: silently signing with a different key than the operator asked for is
		// the failure this seam exists to prevent.
		return nil, fmt.Errorf("gcloud kms failed (no fallback to a local key is attempted): %v: %s",
			err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func (k *KMSSigner) PublicKey(ctx context.Context) (ed25519.PublicKey, error) {
	v, key, ring, loc, err := k.kmsParts()
	if err != nil {
		return nil, err
	}
	pem, err := k.run(ctx, "kms", "keys", "versions", "get-public-key", v,
		"--location", loc, "--keyring", ring, "--key", key)
	if err != nil {
		return nil, err
	}
	return ed25519PubFromPEM(pem)
}

func (k *KMSSigner) Sign(ctx context.Context, msg []byte) ([]byte, error) {
	v, key, ring, loc, err := k.kmsParts()
	if err != nil {
		return nil, err
	}
	// gcloud takes files, not stdin, for these. The MESSAGE is written — never any
	// key material — and both files are removed before returning.
	in, err := os.CreateTemp("", "oath-sign-msg-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(msg); err != nil {
		return nil, err
	}
	in.Close()
	sigf, err := os.CreateTemp("", "oath-sign-sig-")
	if err != nil {
		return nil, err
	}
	sigf.Close()
	defer os.Remove(sigf.Name())

	if _, err := k.run(ctx, "kms", "asymmetric-sign", "--version", v,
		"--location", loc, "--keyring", ring, "--key", key,
		"--input-file", in.Name(), "--signature-file", sigf.Name()); err != nil {
		return nil, err
	}
	sig, rerr := os.ReadFile(sigf.Name())
	if rerr != nil {
		return nil, rerr
	}
	// Verified HERE as well as in signStatement: this adapter must be safe to use
	// on its own, and a signature that does not verify must never leave it.
	pub, perr := k.PublicKey(ctx)
	if perr != nil {
		return nil, perr
	}
	if !ed25519.Verify(pub, msg, sig) {
		return nil, fmt.Errorf("KMS returned a signature that does not verify over the exact bytes signed — refusing to return it")
	}
	return sig, nil
}

func (k *KMSSigner) Description() string {
	return fmt.Sprintf("google-kms via gcloud subprocess:\n    %s\n    (non-exportable; the private key never reaches this process)", k.Resource)
}

// ed25519PubFromPEM extracts the raw 32-byte key from a SubjectPublicKeyInfo PEM.
//
// Hand-rolled rather than via x/crypto: the DER suffix of an Ed25519 SPKI is the
// raw key, and the structure is fixed (12-byte prefix, 32-byte key). Parsing it
// this way keeps the default build dependency-free, and the length check below is
// what makes it safe — anything not exactly 44 bytes is not an Ed25519 SPKI and
// is refused rather than truncated into something plausible.
func ed25519PubFromPEM(pemBytes []byte) (ed25519.PublicKey, error) {
	s := string(pemBytes)
	i := strings.Index(s, "-----BEGIN PUBLIC KEY-----")
	j := strings.Index(s, "-----END PUBLIC KEY-----")
	if i < 0 || j < 0 || j <= i {
		return nil, fmt.Errorf("no PEM public key in KMS response")
	}
	b64 := strings.Join(strings.Fields(s[i+len("-----BEGIN PUBLIC KEY-----"):j]), "")
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("KMS public key is not valid base64: %w", err)
	}
	if len(der) != 44 {
		return nil, fmt.Errorf("KMS returned a %d-byte SubjectPublicKeyInfo; an Ed25519 one is 44 bytes — "+
			"this key is not Ed25519 and must not be used to sign Oath statements", len(der))
	}
	return ed25519.PublicKey(der[12:]), nil
}

// signStatement is the ONE path every signed Oath statement goes through.
//
// It enforces the invariants that make a signature evidence rather than a blob,
// and it enforces them BEFORE anything is transmitted:
//
//   - the signer's public key must equal the principal the statement names, or
//     the statement is about a key that did not sign it;
//   - the signature must pass this kernel's own verifier, so a signer that
//     pre-hashes or length-prefixes is caught here rather than by a third party
//     months later;
//   - the operator sees WHICH signer and WHICH key before it is used.
//
// wantPub is the principal encoded in the statement. Passing it in rather than
// reading it back out of the octets keeps this function independent of which
// envelope format is being signed.
func signStatement(ctx context.Context, s Signer, msg []byte, wantPub string, quiet bool) (string, error) {
	pub, err := s.PublicKey(ctx)
	if err != nil {
		return "", fmt.Errorf("could not obtain the signer's public key: %w", err)
	}
	gotHex := hex.EncodeToString(pub)
	if wantPub != "" && gotHex != wantPub {
		return "", fmt.Errorf("SIGNER MISMATCH: the statement names principal %s… but the signer holds %s…. "+
			"Signing would produce a statement about a key that did not sign it", shortHash(wantPub), shortHash(gotHex))
	}
	if !quiet {
		fp := sha256.Sum256([]byte(gotHex))
		fmt.Printf("\nSIGNER: %s\n  public key:  %s\n  fingerprint: %s\n",
			s.Description(), gotHex, hex.EncodeToString(fp[:])[:32])
	}
	sig, err := s.Sign(ctx, msg)
	if err != nil {
		return "", err
	}
	if len(sig) != ed25519.SignatureSize {
		return "", fmt.Errorf("signer returned %d bytes; an Ed25519 signature is %d", len(sig), ed25519.SignatureSize)
	}
	// Verify with the kernel's own primitive before anything leaves this process.
	if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
		return "", fmt.Errorf("the signature this signer produced does NOT verify against its own public key over the exact bytes signed — " +
			"the signer is transforming the message (hashing, padding or length-prefixing) and is not producing Oath signatures. Nothing was sent")
	}
	return hex.EncodeToString(sig), nil
}

// resolveSigner turns the mutually exclusive flags into one Signer.
func resolveSigner(keyPath, kmsResource string) (Signer, error) {
	switch {
	case keyPath != "" && kmsResource != "":
		return nil, fmt.Errorf("--key and --kms-key are mutually exclusive: pass exactly one, so which key signs is never ambiguous")
	case kmsResource != "":
		return NewKMSSigner(kmsResource)
	case keyPath != "":
		return NewFileSigner(keyPath)
	}
	return nil, fmt.Errorf("no signing key: pass --key <file> or --kms-key <full resource name including /cryptoKeyVersions/N>")
}
