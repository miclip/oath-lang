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
// never delegate onward, never stop the holder from revoking it, and — see
// DEL-REVOCATION-RECOVERS — retains nothing afterwards except the historical fact
// that it signed. That last property did not hold when this first shipped.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
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

// delegateReport is what a delegation attempt returns to its caller.
type delegateReport struct {
	Op        string
	Namespace string
	Subject   string
	Holder    string
	Rev       *big.Int
	Active    []string // the delegate set AFTER this operation
}

// apiDelegate applies a signed delegation or revocation, or refuses it.
//
// THE SUBMISSION INTERFACE, which replay deliberately is not. Replay IGNORES a
// grant it cannot justify — that is correct for a verifier, which must never
// invent authority from a malformed record. It is wrong for a registry, which
// should refuse at submission and say why, rather than silently recording
// something that will never count and leaving the submitter to discover it from
// behaviour.
//
// Every rule here is also enforced by replay. That duplication is deliberate: a
// registry that only checked here would be trusting its own past acceptance, and
// a verifier that only checked at replay could not explain a refusal.
func apiDelegate(st *Store, octets []byte, sigHex, principal string) (delegateReport, error) {
	env, err := parseDelegateEnvelope(octets)
	if err != nil {
		return delegateReport{}, fmt.Errorf("malformed delegation: %w", err)
	}
	if err := delVerify(env, sigHex); err != nil {
		return delegateReport{}, err
	}
	// Both halves, as for a reservation: the signature proves the key signed these
	// bytes, the principal check proves the CALLER holds it. Without the second, an
	// observed grant could be resubmitted by anyone — harmless for a grant that
	// names its own signer, and not harmless once revocation exists, since replaying
	// a stale REVOKE is a denial-of-service against a delegate.
	if principal != "" && principal != env.Pubkey {
		return delegateReport{}, fmt.Errorf("delegation is signed by %s but the authenticated caller is %s: only the holder may grant or revoke",
			shortHash(env.Pubkey), shortHash(principal))
	}

	holder, rev := reservationRev(st, env.Namespace)
	if holder == noAuthority {
		return delegateReport{}, fmt.Errorf("namespace %q is not reserved: there is no authority to delegate from", env.Namespace)
	}
	if holder != env.Pubkey {
		return delegateReport{}, fmt.Errorf("namespace %q is held by %s, not by the signer %s: only the current holder may grant or revoke",
			env.Namespace, shortHash(holder), shortHash(env.Pubkey))
	}
	if env.Authority != holder || env.AuthorityRev.Cmp(rev) != 0 {
		return delegateReport{}, fmt.Errorf("stale authority state: signed against authority=%s rev=%s, but %q is held by %s at rev=%s — re-read and sign again",
			shortHash(env.Authority), env.AuthorityRev, env.Namespace, shortHash(holder), rev)
	}

	active := delegates(st)[env.Namespace]
	switch env.Op {
	case opDelegate:
		if active[env.Subject] {
			return delegateReport{}, fmt.Errorf("%s is already a delegate of %q: re-granting would journal a record that changes nothing",
				shortHash(env.Subject), env.Namespace)
		}
	case opRevoke:
		if !active[env.Subject] {
			// Refused rather than treated as a no-op. A revocation that appears to
			// succeed against a key that was never granted tells an operator they have
			// removed access they never gave — which is exactly the wrong thing to
			// believe during an incident.
			return delegateReport{}, fmt.Errorf("%s is not a current delegate of %q: there is nothing to revoke",
				shortHash(env.Subject), env.Namespace)
		}
	}

	entry := &LogEntry{
		Author: env.Pubkey, Name: env.Namespace, Kind: kindDelegate, Status: "accepted",
		EnvelopeB64: encodeEnvelopeB64(octets), AuthorPubkey: env.Pubkey, AuthorSig: sigHex,
	}
	if err := st.AppendLog(entry); err != nil {
		return delegateReport{}, fmt.Errorf("delegation verified but could not be journaled: %w", err)
	}

	now := delegates(st)[env.Namespace]
	var out []string
	for k := range now {
		out = append(out, k)
	}
	sort.Strings(out)
	return delegateReport{Op: env.Op, Namespace: env.Namespace, Subject: env.Subject,
		Holder: holder, Rev: new(big.Int).Set(rev), Active: out}, nil
}

func renderDelegateReport(r delegateReport) string {
	var b strings.Builder
	verb := "DELEGATED"
	if r.Op == opRevoke {
		verb = "REVOKED"
	}
	fmt.Fprintf(&b, "%s %s\n", verb, r.Namespace)
	fmt.Fprintf(&b, "  subject:  %s\n", r.Subject)
	fmt.Fprintf(&b, "  holder:   %s  (authority is UNCHANGED — this grants permission, not ownership)\n", r.Holder)
	fmt.Fprintf(&b, "  at authority revision: %s\n\n", r.Rev)
	if len(r.Active) == 0 {
		fmt.Fprintf(&b, "No key may now publish under %s except the holder.\n", r.Namespace)
		return b.String()
	}
	fmt.Fprintf(&b, "Keys that may now publish under %s (besides the holder):\n", r.Namespace)
	for _, k := range r.Active {
		fmt.Fprintf(&b, "  %s\n", k)
	}
	fmt.Fprintf(&b, "\nEach may bind names under this prefix and nothing else. None may reserve,\n")
	fmt.Fprintf(&b, "delegate onward, or revoke. The holder may withdraw any of them at any time.\n")
	return b.String()
}

// cmdDelegate grants or withdraws publication rights, locally or against a registry.
func cmdDelegate(local *Store, endpoint, keyPath, kmsKey, namespace, subject, op string, dryRun, assumeYes bool) {
	if namespace == "" || subject == "" {
		fail(fmt.Errorf("usage: oath %s <namespace>/* --to <pubkey> [--key <file> | --kms-key <res>] [--remote <url>]", op))
	}
	if err := validNamespacePattern(namespace); err != nil {
		fail(err)
	}
	if _, err := hex.DecodeString(subject); err != nil || len(subject) != ed25519.PublicKeySize*2 {
		fail(fmt.Errorf("subject %q is not a 32-byte hex Ed25519 public key", subject))
	}
	signer, serr := resolveSigner(keyPath, kmsKey)
	if serr != nil {
		fail(serr)
	}
	ctx := context.Background()
	pubRaw, perr := signer.PublicKey(ctx)
	if perr != nil {
		fail(perr)
	}
	pubHex := hex.EncodeToString(pubRaw)

	holder, rev := noAuthority, big.NewInt(0)
	if endpoint == "" {
		holder, rev = reservationRev(local, namespace)
	} else {
		h, r, err := remoteAuthority(ctx, endpoint, signer, namespace)
		if err != nil {
			fail(fmt.Errorf("reading current authority for %q: %w", namespace, err))
		}
		holder, rev = h, r
	}
	if holder != pubHex {
		fail(fmt.Errorf("%q is held by %s, and you are signing as %s: only the current holder may grant or revoke",
			namespace, shortHash(holder), shortHash(pubHex)))
	}

	env := delEnvelope{Op: op, Namespace: namespace, Subject: subject,
		Authority: holder, AuthorityRev: rev, Pubkey: pubHex}
	octets := delEncode(env)
	fmt.Printf("EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):\n")
	for _, line := range strings.Split(strings.TrimSuffix(string(octets), "\n"), "\n") {
		fmt.Printf("  | %s\n", line)
	}
	if op == opDelegate {
		fmt.Printf("\nThis lets %s… publish under %s. It does NOT make them the owner:\n", shortHash(subject), namespace)
		fmt.Printf("they cannot reserve, cannot delegate onward, and you can revoke at any time.\n")
	} else {
		fmt.Printf("\nThis stops %s… publishing under %s from the moment it is recorded.\n", shortHash(subject), namespace)
	}
	fmt.Printf("Signer: %s\n", signer.Description())
	if dryRun {
		fmt.Printf("\n--dry-run: nothing was signed and nothing was sent.\n")
		return
	}
	if !assumeYes && !confirm("\nSign and submit?") {
		fmt.Printf("aborted; nothing signed.\n")
		return
	}
	sig, sgerr := signStatement(ctx, signer, octets, pubHex, false)
	if sgerr != nil {
		fail(sgerr)
	}
	if endpoint == "" {
		rep, err := apiDelegate(local, octets, sig, pubHex)
		if err != nil {
			fail(err)
		}
		fmt.Print("\n" + renderDelegateReport(rep))
		return
	}
	out, err := mcpCallSignedBy(ctx, endpoint, signer, "delegate", map[string]any{
		"envelope": encodeEnvelopeB64(octets), "signature": sig,
	})
	if err != nil {
		fail(err)
	}
	fmt.Print("\n" + out)
}

func sortStringsInPlace(s []string) { sort.Strings(s) }
