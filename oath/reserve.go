package main

// Namespace reservation: the explicit signed act that establishes PREFIX
// authority (#66).
//
// WHY THIS EXISTS AS A SEPARATE OPERATION. Two different questions were being
// answered by one mechanism, and they are not the same question:
//
//	who first bound alice/foo?          — exact-name TOFU, INFERRED from a publication
//	who is entitled to govern alice/*?  — prefix authority, which must be DECLARED
//
// The first may be inferred from an applied publication, because binding a name
// IS the act. The second may not: publishing one child would silently capture a
// whole namespace, so #84 refused to infer it at all and left prefix authority
// obtainable only by an operator editing policy.json.
//
// That refusal was right and this does not weaken it. #84's rule already named
// the way out — "prefix authority comes only from an explicit rule, RESERVATION,
// or adoption, never from inference" — and a signed reservation is the explicit
// act, not the inference. The invariant is preserved verbatim:
//
//	PREFIX AUTHORITY IS NEVER INFERRED FROM USE. IT IS ESTABLISHED BY AN
//	EXPLICIT SIGNED ACT.
//
// WHY IT IS JOURNALED RATHER THAN CONFIGURED. The authorized-keys allowlist and
// policy.json are both read at startup, so a file-based reservation would still
// need an operator edit and a redeploy — which is the operator intervention this
// operation exists to remove. The journal is already the authority for ownership,
// so authority events belong in it, where a third party re-derives them from
// history alone rather than trusting a config file they cannot see.
//
// WHAT IT DELIBERATELY IS NOT: an ownership TABLE. Reservations are derived by
// replaying the journal, exactly as nameOwner is, so there is no stored summary
// that can drift from the history it summarises.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// reserveVersion is the format this kernel emits and reads. Like the publication
// envelope, the version is INSIDE the signed bytes, so a signature made under one
// shape can never be reinterpreted under another.
const reserveVersion = "oath-reserve/1"

// Authority operations. Only opReserve is implemented; the others are named here
// because the encoding must be able to express them without a version bump —
// release, transfer and delegation are separate SIGNED OPERATIONS, never edits to
// policy.json, and a format that could not carry them would force exactly the
// operator-edit model this replaces.
const (
	opReserve = "reserve"
)

// noAuthority marks a prefix that no key holds. Explicit rather than empty, for
// the same reason noParent is: "I checked, and the prefix was unclaimed" and "a
// field nobody populated" are different facts, and only the first is a statement.
const noAuthority = "-"

// resEnvelope is a key's claim to govern a namespace.
//
// The shape deliberately mirrors pubEnvelope: an operation, a subject, the state
// it replaces, a monotonic revision of that state, and the key making the claim.
// A reservation is a REVISION OF AUTHORITY, not a standalone assertion — without
// (Authority, AuthorityRev) a later transfer could not say what it replaced, and
// the whole authority history would be a set of unordered claims.
type resEnvelope struct {
	Op        string // opReserve
	Namespace string // the prefix pattern, e.g. "alice/*"
	// Authority is the pubkey that holds this prefix NOW, or noAuthority when it is
	// unclaimed. Checked against the registry's derived state, so a stale claim is
	// refused rather than silently overwriting a reservation the signer never saw.
	Authority string
	// AuthorityRev is how many authority transitions this prefix has ALREADY
	// undergone. It is what makes the replay defence survive ABA — a revision never
	// repeats even if a prefix returns to a key that held it before.
	//
	// Arbitrary precision on the wire for the same reason ParentRev is: an envelope
	// is a permanent record, and a bounded parser would declare a valid historical
	// statement MALFORMED rather than merely unsupported.
	AuthorityRev *big.Int
	// Pubkey is the key claiming the namespace. It is INSIDE the signed bytes
	// deliberately: a statement must name its own subject, or it does not say whose
	// claim it is. Verifying a detached signature would establish that SOMEONE
	// signed these bytes; only naming the key here makes the bytes say who.
	// (The §13 IMPL-IDENTITY-SUBJECT discipline, applied to an authority act.)
	Pubkey string
}

// resEncode renders the canonical signed bytes. Panics on an invalid envelope
// rather than emitting bytes that cannot be uniquely decoded — a silently-corrupt
// canonical encoding is the failure discovered years later by someone unable to
// verify a signature they should have been able to trust.
func resEncode(e resEnvelope) []byte {
	if err := e.validate(); err != nil {
		panic("resEncode on an invalid envelope: " + err.Error())
	}
	var b strings.Builder
	b.WriteString(reserveVersion)
	b.WriteByte('\n')
	// Fixed order, fixed count. Do not sort, do not omit, do not add.
	for _, kv := range [][2]string{
		{"op", e.Op},
		{"namespace", e.Namespace},
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

func (e resEnvelope) validate() error {
	if e.AuthorityRev == nil {
		return fmt.Errorf("authority_rev is unset: a reservation must state which authority revision it replaces")
	}
	if e.AuthorityRev.Sign() < 0 {
		return fmt.Errorf("authority_rev is negative")
	}
	for _, f := range []struct{ k, v string }{
		{"op", e.Op}, {"namespace", e.Namespace},
		{"authority", e.Authority}, {"pubkey", e.Pubkey},
	} {
		if f.v == "" {
			return fmt.Errorf("%s is empty", f.k)
		}
		// Same unique-decodability rule as the publication envelope: an LF would
		// inject a line, a CR may be normalised into one by some readers.
		if !envelopeSafe(f.v) {
			return fmt.Errorf("%s contains a control character, which would break unique decodability", f.k)
		}
	}
	if e.Op != opReserve {
		return fmt.Errorf("unknown authority op %q", e.Op)
	}
	if err := validNamespacePattern(e.Namespace); err != nil {
		return err
	}
	if _, err := hex.DecodeString(e.Pubkey); err != nil || len(e.Pubkey) != ed25519.PublicKeySize*2 {
		return fmt.Errorf("pubkey %q is not a 32-byte hex Ed25519 key", e.Pubkey)
	}
	if e.Authority != noAuthority {
		if _, err := hex.DecodeString(e.Authority); err != nil || len(e.Authority) != ed25519.PublicKeySize*2 {
			return fmt.Errorf("authority %q is neither %q nor a 32-byte hex Ed25519 key", e.Authority, noAuthority)
		}
	}
	return nil
}

// protocolRoots are the only prefixes no key may reserve.
//
// They are a TINY IMMUTABLE LIST compiled into the kernel, deliberately not
// policy.json: policy.json is operator state, and an operator-editable reserved
// list would reintroduce exactly the allocation power this operation removes.
// Every OTHER prefix is first-come, first-served.
//
// These are protocol namespaces — room for kernel-defined names (key-derived
// prefixes, system objects) that must not be claimable by whoever asks first.
// Reserving the space now costs nothing; recovering it later would need a
// transfer operation and a cooperative holder.
var protocolRoots = []string{"key", "sys", "oath"}

// WHY FIRST-COME EVERYWHERE ELSE, SQUATTING INCLUDED (settled 2026-08-01).
//
// The alternative considered was key-derived roots — "key/<pubkey>/*" always
// self-service, human-readable roots allocated separately. It was rejected, and
// the reason is the load-bearing one: it leaks the CRYPTOGRAPHIC layer into the
// DISCOVERY layer, in a system whose entire shape is that hashes are not names,
// names are not identity, signatures establish authority, and authority is
// derived from history. A new publisher's first artifact being
// key/65ea5701.../reverse would contradict that on their first interaction.
//
// First-come keeps the invariant that matters: EVERY AUTHORITY CHANGE IS A
// SIGNED JOURNAL OPERATION. No hidden registry state, no operator allocation, no
// special bootstrap path. A second allocation mechanism for "good" names would
// have been registry configuration wearing a protocol's clothes.
//
// Squatting is therefore permitted, and is judged acceptable for v1 because Oath
// is not DNS. Holding the string "openai" does not let anyone impersonate
// OpenAI: every artifact is signed, every publication is attributable, and every
// authority chain is public. It is annoying, not a trust failure — the same
// property github.com/<name> has. If it becomes a real problem, transfer and
// delegation are signed protocol operations to be built, not a naming scheme to
// be retrofitted.

// validNamespacePattern accepts exactly the prefix form "<segments>/*".
//
// Reservation is deliberately NARROWER than the policy pattern language, which
// also accepts exact names and "*". Neither belongs here:
//
//   - an exact name is already answered by TOFU, and offering a second, stronger
//     path to the same thing would create two answers to "who owns alice/foo";
//   - "*" is a claim to the entire registry, which is not a namespace reservation
//     but a takeover, and no first-come rule should be able to grant it.
func validNamespacePattern(p string) error {
	prefix, ok := strings.CutSuffix(p, "/*")
	if !ok {
		return fmt.Errorf("namespace %q must end in \"/*\": a reservation claims a PREFIX (exact names are established by publication, not by reservation)", p)
	}
	if prefix == "" {
		return fmt.Errorf("namespace \"/*\" has an empty prefix: a namespace with an empty name is not a thing anyone means")
	}
	if strings.Contains(prefix, "*") {
		return fmt.Errorf("namespace %q: \"*\" is only meaningful as the trailing \"/*\" — it is not a glob", p)
	}
	// RES-PROTOCOL-ROOT. Compared on the FIRST SEGMENT, not by string prefix:
	// "oath" is reserved but "oathkeeper" is an ordinary name somebody may claim.
	root, _, _ := strings.Cut(prefix, "/")
	for _, r := range protocolRoots {
		if root == r {
			return fmt.Errorf("namespace %q is under the protocol root %q, which is not reservable: these are kernel namespaces, and every other prefix is first-come", p, r+"/*")
		}
	}
	return nil
}

// namespaceCovers reports whether reservation pattern `pat` governs `name`.
//
// SEGMENT-AWARE, and this is the whole point of routing through the existing
// matcher rather than a fresh strings.HasPrefix: "alice/*" governs "alice/foo"
// but NOT "alice2/x", and not the bare name "alice" either. A prefix check that
// forgot the separator would let a reservation capture every name sharing its
// first characters, which is a capture its holder never reasoned about.
func namespaceCovers(pat, name string) bool { return patternSpecificity(pat, name) >= 0 }

// namespaceOverlaps reports whether two reservation prefixes are nested in either
// direction. Overlap is what makes first-come meaningful: "alice/*" and
// "alice/sub/*" are not independent claims, so the second must not be grantable
// to a different key merely because it is spelled differently.
func namespaceOverlaps(a, b string) bool {
	pa := strings.TrimSuffix(a, "/*") + "/"
	pb := strings.TrimSuffix(b, "/*") + "/"
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

// reservation is one prefix's authority state, derived from the journal.
type reservation struct {
	Namespace string
	Pubkey    string   // the key that holds it
	Rev       *big.Int // authority transitions applied SO FAR (0 = never reserved)
	Seq       int      // journal seq of the entry that established it
}

// reservations replays the journal into current authority state.
//
// DERIVED, never stored — the same discipline as nameOwner. A stored table would
// be a summary that can drift from the history it summarises, and the journal is
// the thing a third party actually holds.
func reservations(st *Store) map[string]reservation {
	out := map[string]reservation{}
	for _, e := range st.ReadLog() {
		if e.Kind != kindReserve || e.Status != "accepted" {
			continue
		}
		env, err := decodeReserveEnvelope(e.EnvelopeB64)
		if err != nil {
			// An entry that cannot be decoded is not authority. It is recorded
			// history that this kernel cannot interpret, and inventing an
			// interpretation would manufacture authority from a parse failure.
			continue
		}
		cur := out[env.Namespace]
		next := new(big.Int).Add(env.AuthorityRev, big.NewInt(1))
		if cur.Rev != nil && next.Cmp(cur.Rev) <= 0 {
			continue // stale or replayed; the accepted chain already moved past it
		}
		out[env.Namespace] = reservation{Namespace: env.Namespace, Pubkey: env.Pubkey, Rev: next, Seq: e.Seq}
	}
	return out
}

// reservationRev reports the current authority revision of a prefix, and who
// holds it. A prefix never reserved is (noAuthority, 0) — which is exactly what a
// first reservation must state, so a signer can construct it without guessing.
func reservationRev(st *Store, namespace string) (holder string, rev *big.Int) {
	if r, ok := reservations(st)[namespace]; ok {
		return r.Pubkey, new(big.Int).Set(r.Rev)
	}
	return noAuthority, big.NewInt(0)
}

// governingReservation returns the MOST SPECIFIC reservation covering `name`.
//
// Most-specific-wins, resolved by the same patternSpecificity used for policy
// rules, so authority resolution has one definition rather than two that could
// diverge. A key holding "alice/sub/*" governs alice/sub/x even when a different
// key holds "alice/*" — which cannot arise from a first-come grant, but CAN arise
// from a delegation, and the resolution rule must already be right when it does.
func governingReservation(st *Store, name string) (reservation, bool) {
	best, bestScore, found := reservation{}, -1, false
	for _, r := range reservations(st) {
		if s := patternSpecificity(r.Namespace, name); s > bestScore {
			best, bestScore, found = r, s, true
		}
	}
	return best, found
}

func decodeReserveEnvelope(b64 string) (resEnvelope, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return resEnvelope{}, fmt.Errorf("envelope is not valid base64: %w", err)
	}
	return parseReserveEnvelope(raw)
}

// parseReserveEnvelope decodes canonical bytes and REJECTS anything that does not
// re-encode to itself.
//
// The round-trip is the parser's real contract: a decoder that accepted variant
// spellings would let two different byte strings verify as the same statement,
// and the signature covers BYTES. Anything the encoder would not emit is
// malformed, not lenient input.
func parseReserveEnvelope(b []byte) (resEnvelope, error) {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 6 {
		return resEnvelope{}, fmt.Errorf("reservation envelope has %d line(s), want 6", len(lines))
	}
	if lines[0] != reserveVersion {
		return resEnvelope{}, fmt.Errorf("unknown reservation format %q (this kernel emits and reads %q)", lines[0], reserveVersion)
	}
	want := []string{"op", "namespace", "authority", "authority_rev", "pubkey"}
	vals := map[string]string{}
	for i, key := range want {
		k, v, ok := strings.Cut(lines[i+1], "=")
		if !ok || k != key {
			return resEnvelope{}, fmt.Errorf("line %d: want key %q, got %q", i+2, key, lines[i+1])
		}
		vals[k] = v
	}
	rev, ok := new(big.Int).SetString(vals["authority_rev"], 10)
	if !ok {
		return resEnvelope{}, fmt.Errorf("authority_rev %q is not a base-10 integer", vals["authority_rev"])
	}
	e := resEnvelope{
		Op: vals["op"], Namespace: vals["namespace"], Authority: vals["authority"],
		AuthorityRev: rev, Pubkey: vals["pubkey"],
	}
	if err := e.validate(); err != nil {
		return resEnvelope{}, err
	}
	if string(resEncode(e)) != string(b) {
		return resEnvelope{}, fmt.Errorf("reservation envelope does not re-encode to itself: a variant spelling would let two byte strings verify as one statement")
	}
	return e, nil
}

// kindReserve marks a journal entry as an AUTHORITY event rather than a
// publication. `Kind` is already overloaded across the journal (definition kinds,
// "prove", "cross"), so this follows the established convention.
//
// It is a LABEL, not the authority: the entry's meaning comes from the signed
// envelope, and nothing dispatches verification on this field (see
// authorStatementKind). A kind is written by the registry; only the envelope is
// signed by the author.
const kindReserve = "reserve"

// resVerify checks the author's signature over the canonical reservation bytes.
//
// Deliberately parallel to envelopeVerify, INCLUDING the weak-key rejection: a
// small-order key that could not sign a valid publication must not be able to
// hold a namespace either, or the weaker path becomes the one an attacker uses.
func resVerify(e resEnvelope, sigHex string) error {
	if err := e.validate(); err != nil {
		return err
	}
	pub, err := hex.DecodeString(e.Pubkey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("reservation pubkey is not a usable public key")
	}
	if ruleOn("SIG-SMALL-ORDER") {
		if err := rejectWeakKey(pub); err != nil {
			return err
		}
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("reservation signature is not a %d-byte hex signature", ed25519.SignatureSize)
	}
	if ruleOn("ENV-VERIFY-SIGNATURE") && !ed25519.Verify(ed25519.PublicKey(pub), resEncode(e), sig) {
		return fmt.Errorf("reservation signature does not verify: the envelope was altered in transit, or it was not signed by %s", e.Pubkey)
	}
	return nil
}

// authorStatementKind reports which signed format a recorded statement is in, by
// reading its FORMAT LINE.
//
// DISPATCH ON THE BYTES, NEVER ON THE ENTRY'S KIND FIELD. The version line is
// inside the signed octets; `Kind` is a label the registry wrote and nobody
// signed. Routing verification by the label would let a mislabeled entry be
// checked under the wrong format — and "verified under a format its author never
// used" is indistinguishable, to anyone reading the journal, from "verified".
//
// Unknown formats return "" and are NOT silently accepted: a statement this
// kernel cannot verify must fail loudly, because the alternative is a journal
// that reports unverifiable entries as intact.
func authorStatementKind(octets []byte) string {
	line, _, _ := strings.Cut(string(octets), "\n")
	switch line {
	case envelopeVersion, envelopeVersionV1:
		return "publication"
	case reserveVersion:
		return "reservation"
	}
	return ""
}

// reserveReport is what a reservation attempt returns to its caller.
type reserveReport struct {
	Namespace string
	Pubkey    string
	Rev       *big.Int
	// Retained lists names ALREADY published beneath the prefix whose exact-name
	// owner is somebody else. They are not captured (see RES-NO-CAPTURE) and are
	// surfaced so the reserver learns it at reservation time rather than at their
	// first refused repoint.
	Retained []string
}

// apiReserve applies a signed namespace reservation, or refuses it.
//
// FIVE RULES, and each exists because of a specific way this could go wrong:
//
//	RES-SIGNED             the signature must verify AND the authenticated caller
//	                       must be the key named in the envelope — otherwise a
//	                       relayer could submit somebody else's claim as their own.
//	RES-PATTERN            a prefix "<segments>/*" only (validNamespacePattern).
//	RES-AUTHORITY-CURRENT  the claimed (authority, authority_rev) must match the
//	                       registry's derived state — replay and ABA defence,
//	                       identical in role to parent_rev on a publication.
//	RES-FIRST-COME         an overlapping prefix held by a DIFFERENT key refuses.
//	RES-NO-CAPTURE         existing exact-name owners beneath the prefix keep their
//	                       names; the reservation is prospective.
//
// WHY RES-NO-CAPTURE RETAINS RATHER THAN REFUSES. The obvious rule — refuse a
// reservation if any name beneath it is owned by someone else — makes the
// namespace DENIABLE: anyone could publish one name into alice/* and block Alice
// from ever reserving it. Retention has no such vector, preserves the historical
// fact rather than seizing it, and is what most-specific-wins already says: the
// exact-name owner is more specific than the prefix, so they keep it.
func apiReserve(st *Store, octets []byte, sigHex, principal string) (reserveReport, error) {
	env, err := parseReserveEnvelope(octets)
	if err != nil {
		return reserveReport{}, fmt.Errorf("malformed reservation: %w", err)
	}
	// RES-SIGNED. Both halves are required: the signature proves the key signed
	// these bytes, and the principal check proves the CALLER holds that key. With
	// only the first, anyone who observed a valid reservation could resubmit it —
	// harmless here (it names its own signer) but the same laxity on a transfer
	// would let a relayer redirect authority.
	if err := resVerify(env, sigHex); err != nil {
		return reserveReport{}, err
	}
	if principal != "" && principal != env.Pubkey {
		return reserveReport{}, fmt.Errorf("reservation is signed by %s but the authenticated caller is %s: a key may only claim a namespace for itself", shortHash(env.Pubkey), shortHash(principal))
	}

	// RES-AUTHORITY-CURRENT.
	holder, rev := reservationRev(st, env.Namespace)
	if env.Authority != holder || env.AuthorityRev.Cmp(rev) != 0 {
		return reserveReport{}, fmt.Errorf("stale authority state: the reservation was signed against authority=%s rev=%s, but %q is currently held by %s at rev=%s — re-read the current state and sign again",
			shortHash(env.Authority), env.AuthorityRev, env.Namespace, shortHash(holder), rev)
	}
	// A prefix already held by ANOTHER key cannot be taken by reserving it again;
	// that is a transfer, which is a different signed operation and is not
	// implemented. Held by the SAME key, a re-reservation is a no-op that would
	// have been caught by RES-AUTHORITY-CURRENT above (the rev has moved).
	if holder != noAuthority && holder != env.Pubkey {
		return reserveReport{}, fmt.Errorf("namespace %q is already held by %s: taking it is a TRANSFER, which must be signed by the current holder", env.Namespace, shortHash(holder))
	}

	// RES-FIRST-COME. Overlap in EITHER direction: "alice/*" and "alice/sub/*" are
	// not independent claims, so a different spelling must not grant a second key
	// authority over ground the first already governs.
	for _, r := range reservations(st) {
		if r.Namespace == env.Namespace || r.Pubkey == env.Pubkey {
			continue
		}
		if namespaceOverlaps(r.Namespace, env.Namespace) {
			return reserveReport{}, fmt.Errorf("namespace %q overlaps %q, held by %s: overlapping prefixes are one claim, not two", env.Namespace, r.Namespace, shortHash(r.Pubkey))
		}
	}

	// RES-NO-CAPTURE. Report, never seize.
	var retained []string
	for name := range st.Names() {
		if !namespaceCovers(env.Namespace, name) {
			continue
		}
		if owner, _ := nameOwner(st, name); owner != "" && owner != env.Pubkey {
			retained = append(retained, name)
		}
	}
	sort.Strings(retained)

	next := new(big.Int).Add(env.AuthorityRev, big.NewInt(1))
	entry := &LogEntry{
		Author: env.Pubkey, Name: env.Namespace, Kind: kindReserve, Status: "accepted",
		EnvelopeB64: encodeEnvelopeB64(octets), AuthorPubkey: env.Pubkey, AuthorSig: sigHex,
	}
	if err := st.AppendLog(entry); err != nil {
		return reserveReport{}, fmt.Errorf("reservation verified but could not be journaled: %w", err)
	}
	return reserveReport{Namespace: env.Namespace, Pubkey: env.Pubkey, Rev: next, Retained: retained}, nil
}
