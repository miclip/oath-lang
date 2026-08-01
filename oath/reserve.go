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
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
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
// The test is INTRINSIC TO THE PROTOCOL, not merely IMPORTANT TO IT. `key` and
// `sys` are intrinsic: their meaning is assigned by the kernel, no publisher
// should ever control them, and they need room for kernel-defined names
// (key-derived prefixes, system objects).
//
// `oath` was here in the first draft and was WRONG. It is a publication
// namespace for the Oath project and its standard library — which means it has a
// governing party, a history, and eventually delegations. An unreservable root
// can never have an owner, so reserving it would have permanently prevented the
// protocol from representing who governs its own standard library. Being
// important to the protocol is not the same as being part of it; `oath/*` is an
// ordinary first-come root that the project simply reserves before open
// admission, like any other publisher.
var protocolRoots = []string{"key", "sys"}

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
		// SPEC §8.7.0 ACCEPTED, and RES-ACCEPTED-CLOSED. Three conditions, all
		// required, and the status test is a WHITELIST: an entry whose status is
		// absent, empty or unrecognised is NOT counted. Excluding known rejection
		// words and counting the remainder FAILS OPEN — a status this kernel has
		// never seen would grant authority.
		//
		// Deliberately NOT gated on e.Kind. Kind is a label the registry wrote and
		// nobody signed; dispatching authority on it would let a mislabelled entry
		// decide who governs a namespace. The signed envelope is what says this is a
		// reservation — the same reasoning VerifyLog's format dispatch already uses.
		if e.Status != "accepted" {
			continue
		}
		env, err := decodeReserveEnvelope(e.EnvelopeB64)
		if err != nil {
			// An entry that cannot be decoded is not authority. It is recorded
			// history that this kernel cannot interpret, and inventing an
			// interpretation would manufacture authority from a parse failure.
			continue
		}
		// The signature must verify HERE, not only in VerifyLog. Replay may be run
		// against a journal this process did not verify, and authority derived from
		// an unchecked statement is authority asserted by whoever held the file.
		if resVerify(env, e.AuthorSig) != nil || e.AuthorPubkey != env.Pubkey {
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
	case delegateVersion, delegateVersionV1:
		return "delegation"
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

	// RES-RESERVATION-LIMIT. A mistake guard, not a squatting defence: keys are
	// free, so a determined party generates more of them. It stops one key
	// accumulating namespaces it does not need, and nothing beyond that.
	//
	// Counts only ACCEPTED reservations, for the same reason the delegation
	// revision does — a refused statement confers nothing, so it must not consume
	// anything either.
	if holder != env.Pubkey {
		held := 0
		for _, r := range reservations(st) {
			if r.Pubkey == env.Pubkey {
				held++
			}
		}
		if held >= maxReservationsPerPrincipal {
			return reserveReport{}, fmt.Errorf("%s already holds %d namespaces, which is the limit (%d): reserving %q would be the %d%s.\n"+
				"  Namespaces NEST — a prefix you already hold covers everything beneath it, so `%s/thing/*` needs no separate claim.\n"+
				"  Reservations are permanent, so this limit exists to stop one key accumulating ground it will never use",
				shortHash(env.Pubkey), held, maxReservationsPerPrincipal, env.Namespace, held+1, ordinalSuffix(held+1),
				strings.TrimSuffix(firstNamespaceOf(st, env.Pubkey), "/*"))
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

// renderReserveReport is what a successful reservation tells its holder.
//
// The RETAINED list is the part that must not be silently omitted. A reserver
// who is not told which names beneath their prefix belong to somebody else will
// discover it at their first refused publication, and will reasonably read that
// refusal as a bug in the reservation they just paid for.
func renderReserveReport(r reserveReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RESERVED %s\n", r.Namespace)
	fmt.Fprintf(&b, "  holder:   %s\n", r.Pubkey)
	fmt.Fprintf(&b, "  revision: %s\n", r.Rev)
	if len(r.Retained) == 0 {
		fmt.Fprintf(&b, "\nNo name beneath this prefix was already owned by another key.\n")
	} else {
		fmt.Fprintf(&b, "\nRETAINED BY THEIR EXISTING OWNERS — these are NOT yours (§8.7 RES-NO-CAPTURE):\n")
		for _, n := range r.Retained {
			fmt.Fprintf(&b, "  %s\n", n)
		}
		fmt.Fprintf(&b, "\nThey were published before this reservation and keep their owners. You\n")
		fmt.Fprintf(&b, "govern every OTHER name under %s, including names not yet published.\n", r.Namespace)
	}
	fmt.Fprintf(&b, "\nThis establishes authority over the prefix string in THIS registry. It is\n")
	fmt.Fprintf(&b, "not identity, affiliation, or endorsement (§8.7.1).\n")
	return b.String()
}

// cmdReserve claims a namespace prefix, locally or against a registry.
//
// It shows the EXACT BYTES before signing, for the reason `publish` does: the
// octets are the statement, and a summary of them is not what gets signed. A
// reservation is also permanent in a way a publication is not — this version has
// no transfer, release or expiry — so the confirmation matters more here, not
// less.
func cmdReserve(local *Store, endpoint, keyPath, kmsKey, namespace string, dryRun, assumeYes bool) {
	if namespace == "" {
		fail(fmt.Errorf("usage: oath reserve <namespace>/* [--key <file>] [--remote <url>] [--dry-run] [-y]"))
	}
	if err := validNamespacePattern(namespace); err != nil {
		fail(err)
	}
	signer, serr := resolveSigner(keyPath, kmsKey)
	if serr != nil {
		fail(fmt.Errorf("reserve needs a signing key — prefix authority is established BY a key, so there is nothing to record without one: %w", serr))
	}
	ctx := context.Background()
	pubRaw, perr := signer.PublicKey(ctx)
	if perr != nil {
		fail(perr)
	}
	pubHex := hex.EncodeToString(pubRaw)

	// The state being replaced must come from the registry the claim is FOR. A
	// local reading would sign against a state the target has never had, and the
	// compare-and-swap would refuse it — correctly, and confusingly.
	holder, rev := noAuthority, big.NewInt(0)
	if endpoint == "" {
		holder, rev = reservationRev(local, namespace)
	} else {
		h, r, err := remoteAuthority(ctx, endpoint, signer, namespace)
		if err != nil {
			fail(fmt.Errorf("reading current authority for %q from %s: %w", namespace, endpoint, err))
		}
		holder, rev = h, r
	}
	if holder != noAuthority && holder != pubHex {
		fail(fmt.Errorf("%q is already held by %s… at revision %s. Taking it would be a TRANSFER, which is signed by the CURRENT holder and is not implemented in this version", namespace, shortHash(holder), rev))
	}

	env := resEnvelope{Op: opReserve, Namespace: namespace, Authority: holder,
		AuthorityRev: rev, Pubkey: pubHex}
	octets := resEncode(env)

	fmt.Printf("EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):\n")
	for _, line := range strings.Split(strings.TrimSuffix(string(octets), "\n"), "\n") {
		fmt.Printf("  | %s\n", line)
	}
	fmt.Printf("\nThis claims authority over %s in %s.\n", namespace, orWord(endpoint, "the local store"))
	fmt.Printf("Signer: %s\n", signer.Description())
	fmt.Printf("It is not identity, affiliation, or endorsement, and this version has no\n")
	fmt.Printf("transfer, release or expiry — a prefix granted here cannot be given back.\n")
	if dryRun {
		fmt.Printf("\n--dry-run: nothing was signed and nothing was sent.\n")
		return
	}
	if !assumeYes && !confirm("\nSign and submit this reservation?") {
		fmt.Printf("aborted; nothing signed.\n")
		return
	}
	sig, sgerr := signStatement(ctx, signer, octets, pubHex, false)
	if sgerr != nil {
		fail(sgerr)
	}

	if endpoint == "" {
		rep, err := apiReserve(local, octets, sig, pubHex)
		if err != nil {
			fail(err)
		}
		fmt.Print("\n" + renderReserveReport(rep))
		return
	}
	out, err := remoteReserve(ctx, endpoint, signer, pubHex, octets, sig)
	if err != nil {
		fail(err)
	}
	fmt.Print("\n" + out)
}

// cmdAuthority answers "who governs this prefix, and may I claim it" — and it is
// the command the documentation tells people to run BEFORE the one irreversible
// act in the protocol.
//
// THE CONTRACT (#104). Reservation advice MUST NOT be given unless the authority
// state being reported is the same authority state a subsequent reservation would
// be evaluated against. `cmdReserve` reads local when no registry is configured
// and the registry otherwise; this resolves the view identically, from the same
// environment, flags and config, so the two cannot drift apart.
//
// It failed that contract completely. With a registry configured it read the
// LOCAL store and printed
//
//	oath/* is UNCLAIMED (authority revision 0)
//	  `oath reserve 'oath/*'` would claim it. This is PERMANENT: …
//
// about a prefix held on that registry at revision 1. Not stale — confidently
// wrong about the only question it exists to answer, and then recommending the
// permanent act. A stale `ls` wastes a moment; this spends a namespace.
//
// So when the authoritative state cannot be read, this reports the local view
// under an explicit NOT AUTHORITATIVE banner, gives NO advice, and exits nonzero:
// the question was not answered, and a script that reads a confident "UNCLAIMED"
// out of a failure is precisely the accident being prevented.
// Returns the process exit code. THE DISTINCTION IT ENCODES: a negative answer
// and no answer are different results, and only one of them is an answer.
//
//	authoritative, held       0   the question was answered
//	authoritative, unclaimed  0   the question was answered
//	authoritative unavailable 1   the question was NOT answered
//
// Exit 0 with a printed warning would let a script treat "warning emitted" as
// success and reserve anyway, which is the accident this command exists to
// prevent. The exit code is returned rather than taken directly so the contract
// is testable; main() is what exits.
func cmdAuthority(local *Store, endpoint, keyPath, kmsKey, query string) int {
	if query == "" {
		fail(fmt.Errorf("usage: oath authority <prefix>/* | <name>  [--remote <url>] [--key <file>|--kms-key <res>]"))
	}
	localSource := os.Getenv("OATH_STORE")
	if localSource == "" {
		localSource = "./codebase"
	}
	isPrefix := validNamespacePattern(query) == nil

	localView := func() authorityView {
		h, r := reservationRev(local, query)
		ds := []string{}
		for k := range delegates(local)[query] {
			ds = append(ds, k)
		}
		sort.Strings(ds)
		return authorityView{Holder: h, Rev: r, DelegationRev: delegationRev(local, query),
			Delegates: ds, Source: localSource, Authoritative: endpoint == ""}
	}

	// No registry configured: a reservation would be recorded locally, so the local
	// store IS the state it would be evaluated against. The view is authoritative
	// for that act — which is a narrow claim, and the banner says which store.
	if endpoint == "" {
		renderAuthority(local, query, localView(), isPrefix)
		return 0
	}
	signer, serr := resolveSigner(keyPath, kmsKey)
	var view authorityView
	var rerr error
	if serr != nil {
		rerr = fmt.Errorf("no signing key, and this registry authenticates reads: %w", serr)
	} else if isPrefix {
		view, rerr = remoteAuthorityView(context.Background(), endpoint, signer, query)
	} else {
		// An exact name: the registry's authority record covers the governing
		// reservation, which is what the advice would turn on.
		view, rerr = remoteAuthorityView(context.Background(), endpoint, signer, query)
	}
	if rerr != nil {
		lv := localView()
		lv.Authoritative = false
		fmt.Printf("NOT ANSWERED: a registry is configured (%s) but its authority state\n", endpoint)
		fmt.Printf("could not be read: %v\n\n", rerr)
		fmt.Printf("Showing the LOCAL store only. It is NOT the state a reservation would be\n")
		fmt.Printf("evaluated against, and it will report a prefix held on the registry as free:\n\n")
		renderAuthority(local, query, lv, isPrefix)
		fmt.Printf("\nNo reservation advice is given, because none can be given from this view.\n")
		fmt.Printf("Pass --key <file> or --kms-key <resource> to read %s.\n", endpoint)
		return 1
	}
	renderAuthority(local, query, view, isPrefix)
	return 0
}

// renderAuthority prints a governance record and, only when the view is
// authoritative, the advice that depends on it.
func renderAuthority(local *Store, query string, v authorityView, isPrefix bool) {
	banner := func() {
		if v.Authoritative {
			fmt.Printf("  view: %s — this IS the state a reservation would be evaluated against\n", v.Source)
			return
		}
		fmt.Printf("  view: %s — LOCAL STORE, NOT AUTHORITATIVE\n", v.Source)
	}
	if !isPrefix {
		// Exact names: governed by a reservation, and possibly owned outright.
		if v.Holder != noAuthority && v.Holder != "" {
			fmt.Printf("%s is governed by a reservation held by %s\n", query, v.Holder)
		} else {
			fmt.Printf("%s is governed by no reservation\n", query)
		}
		if owner, src := nameOwner(local, query); owner != "" {
			fmt.Printf("  the NAME itself is owned by %s (%s)\n", owner, src)
		}
		banner()
		return
	}
	if v.Holder == noAuthority || v.Holder == "" {
		fmt.Printf("%s is UNCLAIMED (authority revision %s)\n", query, v.Rev)
		banner()
		if v.Authoritative {
			fmt.Printf("  `oath reserve '%s'` would claim it. This is PERMANENT:\n", query)
			fmt.Printf("  there is no transfer, no release and no expiry.\n")
		}
		return
	}
	fmt.Printf("%s is HELD by %s (authority revision %s)\n", query, v.Holder, v.Rev)
	for _, k := range v.Delegates {
		fmt.Printf("  publication delegated to %s\n", k)
	}
	if v.DelegationRev != nil {
		fmt.Printf("  delegation revision %s — a grant or revocation must state this value\n", v.DelegationRev)
	}
	banner()
}

// maxReservationsPerPrincipal caps how many namespaces one key may hold.
const maxReservationsPerPrincipal = 5

// firstNamespaceOf returns a namespace this key already holds, for an error that
// can point at the nesting they should be using instead. Falls back to a
// placeholder when the key holds none.
func firstNamespaceOf(st *Store, pubkey string) string {
	for _, r := range reservations(st) {
		if r.Pubkey == pubkey {
			return r.Namespace
		}
	}
	return "yours/*"
}

func ordinalSuffix(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}
