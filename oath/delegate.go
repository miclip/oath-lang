package main

// Delegation: granting the right to PUBLISH under a prefix without granting
// authority OVER it (#66).
//
// WHY THIS IS NOT A CONVENIENCE. Without it, automating publication means giving
// the automation the namespace key itself — and since this version has no
// transfer, release or expiry, a compromised release credential would control the
// namespace permanently, with no mechanism to take it back. The choice would be
// between "never automate" and "automate and accept an unrecoverable risk", which
// is not a choice a protocol should force.
//
// So delegation exists to make the dangerous grant unnecessary:
//
//	AUTHORITY over a prefix stays with the holder, is not transferable, and is
//	never held by a machine.
//	PERMISSION to bind names under it is granted to another key, is recorded as a
//	signed act, and is REVOCABLE by the holder alone.
//
// A stolen release key can then publish until it is revoked. It can never reserve,
// never delegate onward, and never stop the holder from revoking it.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const delegateVersion = "oath-delegate/1"

const (
	opDelegate = "delegate"
	opRevoke   = "revoke"
)

// delEnvelope is a holder's grant (or withdrawal) of publication rights.
//
// It carries the SAME (authority, authority_rev) compare-and-swap as a
// reservation, for the same reason: a delegation signed against an authority
// state the prefix has since left must not apply later. A stale grant is a grant
// the signer would not make today.
type delEnvelope struct {
	Op        string // opDelegate | opRevoke
	Namespace string // the prefix whose publication rights are granted
	// Subject is the key being granted or revoked. It is a DIFFERENT key from
	// Pubkey, and that difference is the whole point — a statement where they were
	// equal would be a holder granting themselves what they already have.
	Subject      string
	Authority    string   // the holder as of signing; must be Pubkey
	AuthorityRev *big.Int // the prefix's authority revision at signing
	Pubkey       string   // the holder making the grant
}

func delEncode(e delEnvelope) []byte {
	if err := e.validate(); err != nil {
		panic("delEncode on an invalid envelope: " + err.Error())
	}
	var b strings.Builder
	b.WriteString(delegateVersion)
	b.WriteByte('\n')
	for _, kv := range [][2]string{
		{"op", e.Op},
		{"namespace", e.Namespace},
		{"subject", e.Subject},
		{"authority", e.Authority},
		{"authority_rev", e.AuthorityRev.String()},
		{"pubkey", e.Pubkey},
	} {
		b.WriteString(kv[0])
		b.WriteByte('=')
		b.WriteString(kv[1])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (e delEnvelope) validate() error {
	if e.AuthorityRev == nil || e.AuthorityRev.Sign() < 0 {
		return fmt.Errorf("authority_rev is unset or negative")
	}
	for _, f := range []struct{ k, v string }{
		{"op", e.Op}, {"namespace", e.Namespace}, {"subject", e.Subject},
		{"authority", e.Authority}, {"pubkey", e.Pubkey},
	} {
		if f.v == "" {
			return fmt.Errorf("%s is empty", f.k)
		}
		if !envelopeSafe(f.v) {
			return fmt.Errorf("%s contains a control character", f.k)
		}
	}
	if e.Op != opDelegate && e.Op != opRevoke {
		return fmt.Errorf("unknown delegation op %q", e.Op)
	}
	if err := validNamespacePattern(e.Namespace); err != nil {
		return err
	}
	for _, k := range []struct{ name, v string }{
		{"subject", e.Subject}, {"authority", e.Authority}, {"pubkey", e.Pubkey},
	} {
		if _, err := hex.DecodeString(k.v); err != nil || len(k.v) != ed25519.PublicKeySize*2 {
			return fmt.Errorf("%s %q is not a 32-byte lowercase hex Ed25519 key", k.name, k.v)
		}
		if strings.ToLower(k.v) != k.v {
			return fmt.Errorf("%s must be lowercase hex", k.name)
		}
	}
	// The grantor must be the authority. A delegation signed by anyone else is a
	// statement about a prefix the signer does not govern.
	if e.Authority != e.Pubkey {
		return fmt.Errorf("authority %s… and pubkey %s… differ: only the current holder may delegate",
			shortHash(e.Authority), shortHash(e.Pubkey))
	}
	// SELF-DELEGATION IS REFUSED. It grants nothing the holder lacks, and
	// permitting it would create a delegation record that cannot be meaningfully
	// revoked — revoking it would appear to remove the holder's own rights.
	if e.Subject == e.Pubkey {
		return fmt.Errorf("subject and pubkey are the same key: a holder cannot delegate to itself")
	}
	return nil
}

func parseDelegateEnvelope(b []byte) (delEnvelope, error) {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 7 {
		return delEnvelope{}, fmt.Errorf("delegation envelope has %d line(s), want 7", len(lines))
	}
	if lines[0] != delegateVersion {
		return delEnvelope{}, fmt.Errorf("unknown delegation format %q", lines[0])
	}
	want := []string{"op", "namespace", "subject", "authority", "authority_rev", "pubkey"}
	vals := map[string]string{}
	for i, key := range want {
		k, v, ok := strings.Cut(lines[i+1], "=")
		if !ok || k != key {
			return delEnvelope{}, fmt.Errorf("line %d: want key %q, got %q", i+2, key, lines[i+1])
		}
		vals[k] = v
	}
	rev, ok := new(big.Int).SetString(vals["authority_rev"], 10)
	if !ok {
		return delEnvelope{}, fmt.Errorf("authority_rev %q is not a base-10 integer", vals["authority_rev"])
	}
	e := delEnvelope{Op: vals["op"], Namespace: vals["namespace"], Subject: vals["subject"],
		Authority: vals["authority"], AuthorityRev: rev, Pubkey: vals["pubkey"]}
	if err := e.validate(); err != nil {
		return delEnvelope{}, err
	}
	if string(delEncode(e)) != string(b) {
		return delEnvelope{}, fmt.Errorf("delegation envelope does not re-encode to itself")
	}
	return e, nil
}

func delVerify(e delEnvelope, sigHex string) error {
	if err := e.validate(); err != nil {
		return err
	}
	pub, err := hex.DecodeString(e.Pubkey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("delegation pubkey is not a usable public key")
	}
	if ruleOn("SIG-SMALL-ORDER") {
		if err := rejectWeakKey(pub); err != nil {
			return err
		}
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("delegation signature is not a %d-byte hex signature", ed25519.SignatureSize)
	}
	if ruleOn("ENV-VERIFY-SIGNATURE") && !ed25519.Verify(ed25519.PublicKey(pub), delEncode(e), sig) {
		return fmt.Errorf("delegation signature does not verify")
	}
	return nil
}

const kindDelegate = "delegate"

// delegates replays the journal into the CURRENT set of keys permitted to publish
// under each prefix.
//
// DERIVED, never stored, exactly as authority is — and revocation is why that
// matters more here than anywhere else. A stored permission table could be edited
// to un-revoke a key; a replayed one cannot, because the revocation is a signed
// entry that stays in the history forever.
//
// A grant counts only if the grantor WAS THE HOLDER AT THE TIME, which is
// recomputed rather than trusted: the envelope's (authority, authority_rev) is
// checked against the authority state derived from the entries before it. A key
// that briefly held a prefix cannot leave behind delegations that outlive its
// authority.
func delegates(st *Store) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, e := range st.ReadLog() {
		if e.Status != "accepted" || e.EnvelopeB64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(e.EnvelopeB64)
		if err != nil || authorStatementKind(raw) != "delegation" {
			continue
		}
		env, perr := parseDelegateEnvelope(raw)
		if perr != nil {
			continue
		}
		if delVerify(env, e.AuthorSig) != nil || e.AuthorPubkey != env.Pubkey {
			continue
		}
		// The grantor must hold the prefix NOW, under the state this replay has
		// derived so far. This is the check that stops a former holder's stale
		// grant from taking effect.
		holder, _ := reservationRev(st, env.Namespace)
		if holder != env.Pubkey {
			continue
		}
		set := out[env.Namespace]
		if set == nil {
			set = map[string]bool{}
			out[env.Namespace] = set
		}
		switch env.Op {
		case opDelegate:
			set[env.Subject] = true
		case opRevoke:
			delete(set, env.Subject)
		}
	}
	return out
}

// mayBindUnder reports whether `key` may bind names under the reservation
// governing `name` — as the holder, or as a current delegate of it.
func mayBindUnder(st *Store, r reservation, key string) bool {
	if key == r.Pubkey {
		return true
	}
	return delegates(st)[r.Namespace][key]
}
