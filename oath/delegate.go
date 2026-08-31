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

// delegateVersion is what this kernel EMITS. /1 is still READ: three accepted
// delegations exist on the live registry under it, and a kernel that stopped
// reading them would invalidate correct signatures rather than reject bad ones.
const delegateVersion = "oath-delegate/2"

// delegateVersion3 adds an optional SCOPE narrower than the whole prefix (#66): a
// holder may grant a key permission to bind one exact name (`michael/item-report`)
// or one sub-prefix (`michael/tools/*`) rather than every name under the reservation.
// A /2 grant remains the WHOLE-prefix grant, unchanged; /3 is written only when a
// scope is given, so old signatures keep verifying and the common case is byte-for-
// byte what it was.
const delegateVersion3 = "oath-delegate/3"

// delegateVersionV1 predates delegation_rev. Its statements remain valid and are
// counted by replay; what they cannot do is state which permission-state they
// replace, which is exactly the gap /2 closes.
const delegateVersionV1 = "oath-delegate/1"

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
	Subject string
	// Scope narrows a grant below the whole prefix (/3 only, #66): an exact name
	// (`michael/item-report`) or a sub-prefix (`michael/tools/*`), and it MUST lie
	// strictly under Namespace. Empty means the whole prefix — the /1//2 grant, whose
	// bytes are unchanged. A delegate may bind a name iff its scope covers it
	// (patternSpecificity ≥ 0). One scope per (prefix, subject): a later grant to the
	// same subject replaces the earlier, and a revoke removes it whatever the scope.
	Scope        string
	Authority    string   // the holder as of signing; must be Pubkey
	AuthorityRev *big.Int // the prefix's authority revision at signing
	// DelegationRev versions the DELEGATED-PERMISSION state of this prefix,
	// separately from AuthorityRev which versions who HOLDS it. They are different
	// states and one counter cannot version both: giving delegation events the
	// holder's counter would invalidate any statement signed against the prefix's
	// authority every time a delegate changed.
	//
	// Every ACCEPTED grant or revocation advances it exactly once. Refused
	// statements do NOT — they are preserved as history and confer nothing
	// (AUTH-ACCEPTANCE-IS-THE-BOUNDARY), so advancing on refusal would let a
	// rejected submission invalidate a valid pending one.
	//
	// This is what makes revocation DURABLE. Without it, a grant signed before a
	// revocation stayed submittable after it — nothing in the envelope recorded
	// which permission-state it was written against, so resubmitting the original
	// bytes silently re-activated a revoked delegate. Demonstrated, not theorised.
	DelegationRev *big.Int
	Pubkey        string // the holder making the grant
	// version is the format these bytes were WRITTEN in. Verification must use it
	// rather than whatever this kernel currently emits: re-encoding a /1 statement
	// as /2 and checking its signature against those bytes invalidates a correct
	// signature instead of rejecting a bad one. Unexported — it is a property of
	// the octets, never of the statement's content.
	version string
}

func delEncode(e delEnvelope) []byte {
	// A scoped grant is /3; an unscoped one stays /2, byte-identical to before.
	if e.Scope != "" {
		return delEncodeAs(e, delegateVersion3)
	}
	return delEncodeAs(e, delegateVersion)
}

// delEncodeAs renders under a SPECIFIC format version, so a historical statement
// can be reproduced for verification rather than only re-signed under the current
// shape — the same discipline §8.6.1 requires of publication envelopes.
func delEncodeAs(e delEnvelope, version string) []byte {
	if err := e.validate(); err != nil {
		panic("delEncode on an invalid envelope: " + err.Error())
	}
	var b strings.Builder
	b.WriteString(version)
	b.WriteByte('\n')
	kvs := [][2]string{
		{"op", e.Op},
		{"namespace", e.Namespace},
		{"subject", e.Subject},
	}
	// /3 carries the scope, positioned right after the subject it narrows. /1 and /2
	// have no such line, so their bytes are exactly what they always were.
	if version == delegateVersion3 {
		kvs = append(kvs, [2]string{"scope", e.Scope})
	}
	kvs = append(kvs,
		[2]string{"authority", e.Authority},
		[2]string{"authority_rev", e.AuthorityRev.String()},
	)
	if version != delegateVersionV1 {
		kvs = append(kvs, [2]string{"delegation_rev", e.DelegationRev.String()})
	}
	kvs = append(kvs, [2]string{"pubkey", e.Pubkey})
	for _, kv := range kvs {
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
	if e.DelegationRev == nil || e.DelegationRev.Sign() < 0 {
		return fmt.Errorf("delegation_rev is unset or negative")
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
	if e.Scope != "" {
		if err := validScopeUnder(e.Scope, e.Namespace); err != nil {
			return err
		}
		// A revocation removes the subject's grant whatever its scope, so a scope on
		// a revoke would attest to nothing and invite a false expectation of
		// per-scope withdrawal, which this version does not have.
		if e.Op != opDelegate {
			return fmt.Errorf("a scope may only accompany a delegate: a revoke removes the subject's grant whatever its scope")
		}
	}
	return nil
}

// validScopeUnder checks a /3 delegation scope: an exact name or a sub-prefix that
// lies STRICTLY under the reserved namespace. Strictly, because a scope equal to the
// whole prefix is the /2 grant and must be written as one (there is exactly one
// encoding of "the whole prefix"), and a scope outside the prefix would purport to
// grant what the holder does not govern.
func validScopeUnder(scope, namespace string) error {
	if !envelopeSafe(scope) {
		return fmt.Errorf("scope contains a control character")
	}
	nsBase, ok := strings.CutSuffix(namespace, "/*")
	if !ok || nsBase == "" {
		return fmt.Errorf("namespace %q is not a prefix pattern", namespace)
	}
	if sub, isPrefix := strings.CutSuffix(scope, "/*"); isPrefix {
		// A sub-prefix scope must itself be a well-formed prefix pattern.
		if err := validNamespacePattern(scope); err != nil {
			return fmt.Errorf("scope %q is not a valid sub-prefix: %w", scope, err)
		}
		if !strings.HasPrefix(sub, nsBase+"/") {
			return fmt.Errorf("scope %q must lie strictly under the reserved namespace %q", scope, namespace)
		}
		return nil
	}
	// An exact-name scope: no glob, non-empty, strictly under the prefix.
	if scope == "" || strings.Contains(scope, "*") {
		return fmt.Errorf("scope %q must be an exact name or a sub-prefix ending in \"/*\"", scope)
	}
	if !strings.HasPrefix(scope, nsBase+"/") {
		return fmt.Errorf("scope %q must lie strictly under the reserved namespace %q", scope, namespace)
	}
	return nil
}

func parseDelegateEnvelope(b []byte) (delEnvelope, error) {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	// /1 is READ but never emitted: accepted delegations exist under it, and their
	// signatures must keep verifying. A /1 statement has no delegation_rev — it
	// could not state which permission-state it replaced, which is the gap /2
	// closes — so replay counts it while nothing may be SUBMITTED under it.
	var want []string
	switch lines[0] {
	case delegateVersion3:
		want = []string{"op", "namespace", "subject", "scope", "authority", "authority_rev", "delegation_rev", "pubkey"}
	case delegateVersion:
		want = []string{"op", "namespace", "subject", "authority", "authority_rev", "delegation_rev", "pubkey"}
	case delegateVersionV1:
		want = []string{"op", "namespace", "subject", "authority", "authority_rev", "pubkey"}
	default:
		return delEnvelope{}, fmt.Errorf("unknown delegation format %q", lines[0])
	}
	if len(lines) != len(want)+1 {
		return delEnvelope{}, fmt.Errorf("delegation envelope has %d line(s), want %d for %s",
			len(lines), len(want)+1, lines[0])
	}
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
	drev := big.NewInt(0)
	if v, present := vals["delegation_rev"]; present {
		if drev, ok = new(big.Int).SetString(v, 10); !ok {
			return delEnvelope{}, fmt.Errorf("delegation_rev %q is not a base-10 integer", v)
		}
	}
	// /3 carries a scope; /1 and /2 have no such key, so vals["scope"] is "" for them
	// — exactly the whole-prefix meaning. A /3 with an empty scope is malformed: the
	// whole prefix has one encoding, and it is /2.
	if lines[0] == delegateVersion3 && vals["scope"] == "" {
		return delEnvelope{}, fmt.Errorf("oath-delegate/3 requires a non-empty scope; the whole prefix is a /2 grant")
	}
	e := delEnvelope{Op: vals["op"], Namespace: vals["namespace"], Subject: vals["subject"],
		Scope: vals["scope"], Authority: vals["authority"], AuthorityRev: rev, DelegationRev: drev,
		Pubkey: vals["pubkey"], version: lines[0]}
	if err := e.validate(); err != nil {
		return delEnvelope{}, err
	}
	if lines[0] == delegateVersionV1 {
		// Round-trip under /1's shape, since delEncode emits /2.
		if string(delEncodeAs(e, delegateVersionV1)) != string(b) {
			return delEnvelope{}, fmt.Errorf("delegation envelope does not re-encode to itself")
		}
		return e, nil
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
	// Verified against the format the statement was WRITTEN in (§8.6.1's rule for
	// publication envelopes, and for the same reason).
	ver := e.version
	if ver == "" {
		ver = delegateVersion
	}
	if ruleOn("ENV-VERIFY-SIGNATURE") && !ed25519.Verify(ed25519.PublicKey(pub), delEncodeAs(e, ver), sig) {
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
// The value is each subject's SCOPE: the name pattern it may bind under the prefix —
// the whole prefix (the reservation namespace) for a /1//2 grant, or the narrower
// exact name / sub-prefix a /3 grant carried. A subject present in the map is a
// current delegate; its scope decides which names it may bind (mayBindUnder).
func delegates(st *Store) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, e := range st.ReadLog() {
		if e.Status != "accepted" || e.EnvelopeB64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(e.EnvelopeB64)
		if err != nil {
			continue
		}
		// A TRANSFER CLEARS EVERY DELEGATION UNDER THE PREFIX, explicitly rather
		// than as a side effect. The grants were made by the OLD holder; carrying
		// them across would hand the recipient publishers it never authorised —
		// authority by inheritance, which is exactly what reservation exists to
		// prevent.
		//
		// Stated here even though the holder test below would drop them anyway.
		// That test compares against the FINAL derived holder, so the clearing
		// would be a consequence of evaluation order rather than a rule, and a
		// later refactor to a point-in-time holder check would silently restore
		// the delegations without any test noticing.
		if authorStatementKind(raw) == "transfer" {
			xe, xerr := parseTransferEnvelope(raw)
			if xerr == nil && xferVerify(xe, e.AuthorSig, e.RecipientSig) == nil {
				delete(out, xe.Namespace)
			}
			continue
		}
		if authorStatementKind(raw) != "delegation" {
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
			set = map[string]string{}
			out[env.Namespace] = set
		}
		switch env.Op {
		case opDelegate:
			// A /1//2 grant carries no scope and means the whole prefix; a /3 grant
			// carries the narrower one. A later grant to the same subject replaces the
			// earlier — one scope per (prefix, subject).
			scope := env.Scope
			if scope == "" {
				scope = env.Namespace
			}
			set[env.Subject] = scope
		case opRevoke:
			delete(set, env.Subject)
		}
	}
	return out
}

// delegationRev is the current permission-state version of a prefix: how many
// ACCEPTED grants or revocations it has undergone.
//
// Derived by replay like everything else, and counted from ACCEPTED entries only.
// A refused statement is preserved as history and confers nothing, so counting it
// would let a rejected submission invalidate a valid pending one.
func delegationRev(st *Store, namespace string) *big.Int {
	n := big.NewInt(0)
	for _, e := range st.ReadLog() {
		if e.Status != "accepted" || e.EnvelopeB64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(e.EnvelopeB64)
		if err != nil {
			continue
		}
		// A TRANSFER ADVANCES THE PERMISSION STATE. It clears every delegation, so
		// it changes exactly what this revision versions — and without the advance,
		// a grant envelope written before the handover would still state the
		// current value and could be replayed against the new holder's prefix.
		if authorStatementKind(raw) == "transfer" {
			xe, xerr := parseTransferEnvelope(raw)
			if xerr == nil && xe.Namespace == namespace && xferVerify(xe, e.AuthorSig, e.RecipientSig) == nil {
				n.Add(n, big.NewInt(1))
			}
			continue
		}
		if authorStatementKind(raw) != "delegation" {
			continue
		}
		env, perr := parseDelegateEnvelope(raw)
		if perr != nil || env.Namespace != namespace {
			continue
		}
		if delVerify(env, e.AuthorSig) != nil || e.AuthorPubkey != env.Pubkey {
			continue
		}
		n.Add(n, big.NewInt(1))
	}
	return n
}

// mayBindUnder reports whether `key` may bind `name` under the reservation `r`
// governing it — as the holder (any name under the prefix), or as a current delegate
// whose SCOPE covers this particular name. A delegate's scope is the whole prefix for
// a /1//2 grant, or the narrower exact name / sub-prefix a /3 grant carried;
// patternSpecificity ≥ 0 means the scope covers the name, and it handles both an exact
// name and a `prefix/*` pattern.
func mayBindUnder(st *Store, r reservation, key, name string) bool {
	if key == r.Pubkey {
		return true
	}
	scope, ok := delegates(st)[r.Namespace][key]
	return ok && patternSpecificity(scope, name) >= 0
}

// delegateReport is what a delegation attempt returns to its caller.
type delegateReport struct {
	Op        string
	Namespace string
	Subject   string
	Holder    string
	Rev       *big.Int
	Active    []string          // the delegate set AFTER this operation, keys sorted
	Scopes    map[string]string // each active key -> its scope (the namespace when whole-prefix)
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

	// REFUSALS ARE PRESERVED, NOT DISCARDED. Past this point the statement is
	// authenticated: the signature verifies and the caller holds the signing key.
	// A refusal is therefore a real thing a real principal said, and the journal's
	// job is to keep what was said — the same reason a blocked publication stores
	// its object and journals `blocked` rather than vanishing.
	//
	// Preserving it costs nothing in authority, because replay counts only entries
	// recorded as accepted (AUTH-ACCEPTANCE-IS-THE-BOUNDARY). What it buys is that
	// "someone tried to delegate to this key and was refused" survives, which is
	// exactly the record an incident review needs and exactly the record a
	// discarding implementation destroys.
	refuse := func(format string, a ...any) (delegateReport, error) {
		err := fmt.Errorf(format, a...)
		_ = st.AppendLog(&LogEntry{
			Author: env.Pubkey, Name: env.Namespace, Kind: kindDelegate, Status: "rejected",
			Error: err.Error(), EnvelopeB64: encodeEnvelopeB64(octets),
			AuthorPubkey: env.Pubkey, AuthorSig: sigHex,
		})
		return delegateReport{}, err
	}

	holder, rev := reservationRev(st, env.Namespace)
	if holder == noAuthority {
		return refuse("namespace %q is not reserved: there is no authority to delegate from", env.Namespace)
	}
	if holder != env.Pubkey {
		return refuse("namespace %q is held by %s, not by the signer %s: only the current holder may grant or revoke",
			env.Namespace, shortHash(holder), shortHash(env.Pubkey))
	}
	if env.Authority != holder || env.AuthorityRev.Cmp(rev) != 0 {
		return refuse("stale authority state: signed against authority=%s rev=%s, but %q is held by %s at rev=%s — re-read and sign again",
			shortHash(env.Authority), env.AuthorityRev, env.Namespace, shortHash(holder), rev)
	}

	// THE PERMISSION-STATE COMPARE-AND-SWAP. This is what makes revocation
	// durable: a grant signed before a revocation names the permission-state it
	// replaced, so resubmitting those bytes afterwards no longer matches.
	//
	// /1 statements carry no delegation_rev and are accepted only when the prefix
	// has never had one — otherwise a historical envelope would be replayable by
	// the very gap this closes.
	curDRev := delegationRev(st, env.Namespace)
	if env.DelegationRev.Cmp(curDRev) != 0 {
		return refuse("stale delegation state: signed against delegation_rev=%s, but %q is at %s — "+
			"a grant or revocation has happened since, so this statement describes a permission "+
			"state that no longer exists. Re-read and sign again",
			env.DelegationRev, env.Namespace, curDRev)
	}

	active := delegates(st)[env.Namespace]
	switch env.Op {
	case opDelegate:
		// The stored scope is the grant's scope, or the whole prefix when unscoped.
		// Re-granting the SAME subject at the SAME scope changes nothing and is
		// refused; re-granting it at a DIFFERENT scope is a deliberate re-scoping and
		// is allowed (one scope per subject — the new grant replaces the old).
		newScope := env.Scope
		if newScope == "" {
			newScope = env.Namespace
		}
		if cur, ok := active[env.Subject]; ok && cur == newScope {
			return refuse("%s is already a delegate of %q at scope %q: re-granting would journal a record that changes nothing",
				shortHash(env.Subject), env.Namespace, newScope)
		}
	case opRevoke:
		if _, ok := active[env.Subject]; !ok {
			// Refused rather than treated as a no-op. A revocation that appears to
			// succeed against a key that was never granted tells an operator they have
			// removed access they never gave — which is exactly the wrong thing to
			// believe during an incident.
			return refuse("%s is not a current delegate of %q: there is nothing to revoke",
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
	scopes := map[string]string{}
	for k, s := range now {
		out = append(out, k)
		scopes[k] = s
	}
	sort.Strings(out)
	return delegateReport{Op: env.Op, Namespace: env.Namespace, Subject: env.Subject,
		Holder: holder, Rev: new(big.Int).Set(rev), Active: out, Scopes: scopes}, nil
}

// scopeDescription renders a stored scope for a human: the whole prefix reads as
// exactly that, a narrower scope names what it admits.
func scopeDescription(scope, namespace string) string {
	if scope == namespace || scope == "" {
		return "the whole prefix"
	}
	if strings.HasSuffix(scope, "/*") {
		return "names under " + scope
	}
	return "only " + scope
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
		fmt.Fprintf(&b, "  %s  (%s)\n", k, scopeDescription(r.Scopes[k], r.Namespace))
	}
	fmt.Fprintf(&b, "\nEach may bind the names shown above and nothing else. None may reserve,\n")
	fmt.Fprintf(&b, "delegate onward, or revoke. The holder may withdraw any of them at any time.\n")
	return b.String()
}

// cmdDelegate grants or withdraws publication rights, locally or against a registry.
func cmdDelegate(local *Store, endpoint, keyPath, kmsKey, namespace, subject, op, scope string, dryRun, assumeYes bool) {
	if namespace == "" || subject == "" {
		// The flag differs by operation: you delegate TO a key and revoke FROM one.
		// Both are accepted by the parser, but the usage message must name the one
		// that reads correctly, or it teaches the wrong idiom for half its uses.
		flag := "--to"
		if op == opRevoke {
			flag = "--from"
		}
		fail(fmt.Errorf("usage: oath %s <namespace>/* %s <pubkey> [--scope <name|sub/*>] [--key <file> | --kms-key <res>] [--remote <url>]", op, flag))
	}
	if err := validNamespacePattern(namespace); err != nil {
		fail(err)
	}
	if scope != "" {
		if op == opRevoke {
			fail(fmt.Errorf("--scope is not used with revoke: a revoke removes the delegate's grant whatever its scope"))
		}
		if err := validScopeUnder(scope, namespace); err != nil {
			fail(err)
		}
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

	// The CURRENT permission-state, read from the same place the acceptance path
	// will check it against.
	drev := delegationRev(local, namespace)
	if endpoint != "" {
		drev = remoteDelegationRev(ctx, endpoint, signer, namespace)
	}
	env := delEnvelope{Op: op, Namespace: namespace, Subject: subject, Scope: scope,
		Authority: holder, AuthorityRev: rev, DelegationRev: drev, Pubkey: pubHex}
	octets := delEncode(env)
	fmt.Printf("EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):\n")
	for _, line := range strings.Split(strings.TrimSuffix(string(octets), "\n"), "\n") {
		fmt.Printf("  | %s\n", line)
	}
	if op == opDelegate {
		where := namespace
		if scope != "" {
			where = scope + " (only)"
		}
		fmt.Printf("\nThis lets %s… publish %s. It does NOT make them the owner:\n", shortHash(subject), where)
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
