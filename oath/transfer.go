package main

// Namespace transfer: the operation that closes a protocol dead end.
//
// THE DEFECT IT REMOVES. Two valid principals could agree on a change of
// authority and the protocol had no way to express it. A holder willing to hand a
// prefix over, a squatter who had been persuaded, a personal key that should
// become an organisation key — all of them were refused by a rule that existed to
// stop a HOSTILE seizure and caught every consensual one with it. A design that
// refuses a transaction all parties want is wrong independently of any adversary.
//
// WHAT IT IS NOT. It is not recovery. Transfer requires the current holder's
// signature, so a LOST key cannot be transferred and nothing here helps. Rotation
// has the same shape and the same limit. The only mechanism that survives key
// loss is one registered in advance that can act without the original key, and
// that is deliberately not built: such a key is as powerful as the one it
// replaces, so it doubles the theft surface to solve a problem nobody has had.
// `docs/publishing.md` continues to say plainly that a lost key is terminal.
//
// BOTH SIDES SIGN, over the SAME canonical bytes. The holder authorises surrender
// and the recipient accepts custody, because custody carries obligations — a
// reservation counts against the recipient's cap, and a prefix cannot be pushed
// onto a key that never asked for it. A separate acknowledgement would be a
// second document that could be detached from this one and replayed against a
// different transfer; one statement with two signatures cannot be.
//
// WHAT TRANSFER DOES, exactly:
//
//	holder             A → B
//	authority_rev      n → n+1
//	delegates          CLEARED
//	delegation_rev     advances
//	exact-name owners  unchanged
//	authorship         unchanged
//
// DELEGATIONS DO NOT SURVIVE. They were granted by the OLD holder, and carrying
// them across would make the recipient inherit publishers it never authorised —
// authority arriving by inheritance rather than by consent, which is the thing
// the whole reservation model exists to prevent. Clearing them also has to be
// replay-proof: transfer advances the delegation revision, so a grant envelope
// written before the handover states a permission state that no longer exists.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
)

const transferVersion = "oath-transfer/1"

const opTransfer = "transfer"

// xferEnvelope is the signed statement. No `pubkey` field: FromAuthority and
// ToAuthority already name both signers, so a separate signer field could only
// agree or disagree with them, and a field that can disagree with the thing it
// duplicates is a defect waiting to be found.
type xferEnvelope struct {
	Op            string
	Namespace     string
	FromAuthority string
	ToAuthority   string
	AuthorityRev  *big.Int
}

func xferEncode(e xferEnvelope) []byte {
	var b strings.Builder
	b.WriteString(transferVersion)
	b.WriteByte('\n')
	for _, kv := range [][2]string{
		{"op", e.Op},
		{"namespace", e.Namespace},
		{"from_authority", e.FromAuthority},
		{"to_authority", e.ToAuthority},
		{"authority_rev", e.AuthorityRev.String()},
	} {
		b.WriteString(kv[0])
		b.WriteByte('=')
		b.WriteString(kv[1])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (e xferEnvelope) validate() error {
	if e.Op != opTransfer {
		return fmt.Errorf("op must be %q, got %q", opTransfer, e.Op)
	}
	if e.AuthorityRev == nil || e.AuthorityRev.Sign() < 0 {
		return fmt.Errorf("authority_rev is unset or negative")
	}
	for _, f := range []struct{ k, v string }{
		{"namespace", e.Namespace}, {"from_authority", e.FromAuthority}, {"to_authority", e.ToAuthority},
	} {
		if f.v == "" {
			return fmt.Errorf("%s is empty", f.k)
		}
	}
	if e.FromAuthority == e.ToAuthority {
		return fmt.Errorf("from_authority and to_authority are the same key: a transfer to yourself changes nothing and would advance the revision for no reason")
	}
	if err := validNamespacePattern(e.Namespace); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	return nil
}

func parseTransferEnvelope(b []byte) (xferEnvelope, error) {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 6 || lines[0] != transferVersion {
		return xferEnvelope{}, fmt.Errorf("not an %s envelope", transferVersion)
	}
	vals := map[string]string{}
	for i, key := range []string{"op", "namespace", "from_authority", "to_authority", "authority_rev"} {
		k, v, ok := strings.Cut(lines[i+1], "=")
		if !ok || k != key {
			return xferEnvelope{}, fmt.Errorf("line %d: want key %q, got %q", i+2, key, lines[i+1])
		}
		vals[k] = v
	}
	rev, ok := new(big.Int).SetString(vals["authority_rev"], 10)
	if !ok {
		return xferEnvelope{}, fmt.Errorf("authority_rev %q is not a base-10 integer", vals["authority_rev"])
	}
	e := xferEnvelope{Op: vals["op"], Namespace: vals["namespace"], FromAuthority: vals["from_authority"],
		ToAuthority: vals["to_authority"], AuthorityRev: rev}
	if err := e.validate(); err != nil {
		return xferEnvelope{}, err
	}
	if string(xferEncode(e)) != string(b) {
		return xferEnvelope{}, fmt.Errorf("transfer envelope does not re-encode to itself")
	}
	return e, nil
}

// xferVerify checks BOTH signatures over the SAME bytes (XFER-SIGNED-BOTH).
func xferVerify(e xferEnvelope, holderSig, recipientSig string) error {
	if err := e.validate(); err != nil {
		return err
	}
	octets := xferEncode(e)
	for _, side := range []struct{ who, pub, sig string }{
		{"holder", e.FromAuthority, holderSig},
		{"recipient", e.ToAuthority, recipientSig},
	} {
		pub, err := hex.DecodeString(side.pub)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("%s key is not a usable public key", side.who)
		}
		if ruleOn("SIG-SMALL-ORDER") {
			if err := rejectWeakKey(pub); err != nil {
				return fmt.Errorf("%s key: %w", side.who, err)
			}
		}
		sig, err := hex.DecodeString(side.sig)
		if err != nil || len(sig) != ed25519.SignatureSize {
			return fmt.Errorf("%s signature is not a %d-byte hex signature — a transfer needs consent from BOTH sides", side.who, ed25519.SignatureSize)
		}
		if ruleOn("ENV-VERIFY-SIGNATURE") && !ed25519.Verify(ed25519.PublicKey(pub), octets, sig) {
			return fmt.Errorf("%s signature does not verify over the transfer statement", side.who)
		}
	}
	return nil
}

const kindTransfer = "transfer"

// transfers replays accepted transfers for a namespace, most recent last. Used by
// the delegation replay to know where a prefix changed hands.
func acceptedTransfers(st *Store) []xferEnvelope {
	var out []xferEnvelope
	for _, e := range st.ReadLog() {
		if e.Status != "accepted" || e.EnvelopeB64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(e.EnvelopeB64)
		if err != nil || authorStatementKind(raw) != "transfer" {
			continue
		}
		env, perr := parseTransferEnvelope(raw)
		if perr != nil {
			continue
		}
		if xferVerify(env, e.AuthorSig, e.RecipientSig) != nil {
			continue
		}
		out = append(out, env)
	}
	return out
}

// apiTransfer is the single acceptance path, as apiReserve and apiDelegate are.
func apiTransfer(st *Store, octets []byte, holderSig, recipientSig, principal string) (transferReport, error) {
	env, err := parseTransferEnvelope(octets)
	if err != nil {
		return transferReport{}, fmt.Errorf("malformed transfer: %w", err)
	}
	if err := xferVerify(env, holderSig, recipientSig); err != nil {
		return transferReport{}, err
	}
	// EITHER party may submit — both signed the same bytes, so both consented and
	// neither can be surprised by the other carrying it. A third party may not,
	// for the same reason a delegation may not be relayed: an observed statement
	// resubmitted by a stranger is a replay channel.
	if principal != "" && principal != env.FromAuthority && principal != env.ToAuthority {
		return transferReport{}, fmt.Errorf("transfer is between %s and %s but the authenticated caller is %s: only a party to the transfer may submit it",
			shortHash(env.FromAuthority), shortHash(env.ToAuthority), shortHash(principal))
	}

	// XFER-REFUSALS-ARE-PRESERVED. Past this point both signatures verify and the
	// caller is a party, so a refusal is a real thing real principals said. It is
	// preserved and confers nothing — the same boundary reservation and delegation
	// already draw.
	refuse := func(format string, a ...any) (transferReport, error) {
		rerr := fmt.Errorf(format, a...)
		_ = st.AppendLog(&LogEntry{
			Author: env.FromAuthority, Name: env.Namespace, Kind: kindTransfer, Status: "rejected",
			Error: rerr.Error(), EnvelopeB64: encodeEnvelopeB64(octets),
			AuthorPubkey: env.FromAuthority, AuthorSig: holderSig, RecipientSig: recipientSig,
		})
		return transferReport{}, rerr
	}

	if isProtocolRoot(env.Namespace) {
		return refuse("%q is a protocol root: its meaning is assigned by the kernel and no principal may hold or receive it", env.Namespace)
	}

	holder, rev := reservationRev(st, env.Namespace)
	if holder == noAuthority {
		return refuse("namespace %q is not reserved: there is no authority to transfer", env.Namespace)
	}
	// XFER-HOLDER-CURRENT.
	if holder != env.FromAuthority {
		return refuse("namespace %q is held by %s, not by %s: only the current holder may transfer it",
			env.Namespace, shortHash(holder), shortHash(env.FromAuthority))
	}
	// XFER-AUTHORITY-CURRENT.
	if env.AuthorityRev.Cmp(rev) != 0 {
		return refuse("stale authority state: signed against authority_rev=%s, but %q is at %s — authority changed since this was signed, so re-read and sign again",
			env.AuthorityRev, env.Namespace, rev)
	}
	// XFER-RESERVATION-LIMIT. Transfer must not be a way around the cap.
	held := 0
	for _, r := range reservations(st) {
		if r.Pubkey == env.ToAuthority {
			held++
		}
	}
	if held >= maxReservationsPerPrincipal {
		return refuse("recipient %s already holds %d namespaces, which is the limit (%d): accepting %q would exceed it, and a transfer must not be a way around the cap",
			shortHash(env.ToAuthority), held, maxReservationsPerPrincipal, env.Namespace)
	}

	// XFER-NO-CAPTURE. Report retained names, never seize them. A transfer moves
	// the PREFIX; exact-name ownership beneath it belongs to whoever established
	// it, or acquiring ground would become a way of acquiring other people's names.
	var retained []string
	for name := range st.Names() {
		if !namespaceCovers(env.Namespace, name) {
			continue
		}
		if owner, _ := nameOwner(st, name); owner != "" && owner != env.FromAuthority && owner != env.ToAuthority {
			retained = append(retained, name)
		}
	}

	cleared := len(delegates(st)[env.Namespace])

	if err := st.AppendLog(&LogEntry{
		Author: env.FromAuthority, Name: env.Namespace, Kind: kindTransfer, Status: "accepted",
		EnvelopeB64:  encodeEnvelopeB64(octets),
		AuthorPubkey: env.FromAuthority, AuthorSig: holderSig, RecipientSig: recipientSig,
	}); err != nil {
		return transferReport{}, err
	}
	newHolder, newRev := reservationRev(st, env.Namespace)
	return transferReport{
		Namespace: env.Namespace, From: env.FromAuthority, To: newHolder,
		AuthorityRev: newRev, DelegatesCleared: cleared, Retained: retained,
	}, nil
}

type transferReport struct {
	Namespace        string
	From             string
	To               string
	AuthorityRev     *big.Int
	DelegatesCleared int
	Retained         []string
}

// cmdTransfer is the client side: the holder signs, the recipient signs the SAME
// bytes, and either submits.
func cmdTransfer(local *Store, endpoint, keyPath, kmsKey, recipientKeyPath, namespace, toPub string, dryRun, assumeYes bool) {
	if namespace == "" || toPub == "" {
		fail(fmt.Errorf("usage: oath transfer <namespace>/* --to <pubkey> [--recipient-key <file>] [--key <file>] [--remote <url>] [--dry-run] [-y]"))
	}
	if err := validNamespacePattern(namespace); err != nil {
		fail(err)
	}
	signer, serr := resolveSigner(keyPath, kmsKey)
	if serr != nil {
		fail(fmt.Errorf("transfer must be signed by the current holder: %w", serr))
	}
	ctx := context.Background()
	fromRaw, perr := signer.PublicKey(ctx)
	if perr != nil {
		fail(perr)
	}
	fromHex := hex.EncodeToString(fromRaw)

	holder, rev := noAuthority, big.NewInt(0)
	if endpoint == "" {
		holder, rev = reservationRev(local, namespace)
	} else {
		h, r, err := remoteAuthority(ctx, endpoint, signer, namespace)
		if err != nil {
			fail(fmt.Errorf("cannot read authority state from %s: %w", endpoint, err))
		}
		holder, rev = h, r
	}
	if holder != fromHex {
		fail(fmt.Errorf("%q is held by %s, not by your key %s: only the holder may transfer it",
			namespace, shortHash(holder), shortHash(fromHex)))
	}

	env := xferEnvelope{Op: opTransfer, Namespace: namespace, FromAuthority: fromHex,
		ToAuthority: toPub, AuthorityRev: rev}
	octets := xferEncode(env)

	fmt.Printf("EXACT BYTES BOTH PARTIES SIGN (one statement, two signatures):\n")
	for _, l := range strings.Split(strings.TrimRight(string(octets), "\n"), "\n") {
		fmt.Printf("  | %s\n", l)
	}
	fmt.Printf("\nThis hands %s to %s. It is PERMANENT and there is no undo.\n", namespace, shortHash(toPub))
	fmt.Printf("  authority revision:  %s -> %s\n", rev, new(big.Int).Add(rev, big.NewInt(1)))
	fmt.Printf("  delegations:         ALL CLEARED — the recipient does not inherit publishers it never authorized\n")
	fmt.Printf("  names already bound: RETAINED by their owners; a transfer moves the prefix, not other people's names\n")
	fmt.Printf("  authorship:          UNCHANGED — the journal keeps saying who published what\n")
	if dryRun {
		fmt.Printf("\n--dry-run: nothing was signed and nothing was sent.\n")
		return
	}
	if !assumeYes && !confirm("Sign and submit?") {
		fmt.Println("aborted; nothing signed.")
		return
	}
	holderSig, herr := signStatement(ctx, signer, octets, fromHex, false)
	if herr != nil {
		fail(herr)
	}
	// The recipient's signature is over the SAME octets. In this client both keys
	// are local; a real handover exchanges the statement out of band and the
	// recipient signs it with `oath transfer --countersign`.
	rsigner, rerr := resolveSigner(recipientKeyPath, "")
	if rerr != nil {
		fail(fmt.Errorf("the recipient must countersign the same statement (--recipient-key), because custody carries obligations and cannot be pushed onto a key that never accepted it: %w", rerr))
	}
	rRaw, _ := rsigner.PublicKey(ctx)
	if hex.EncodeToString(rRaw) != toPub {
		fail(fmt.Errorf("--recipient-key is %s but --to names %s: the countersignature must come from the key receiving custody",
			shortHash(hex.EncodeToString(rRaw)), shortHash(toPub)))
	}
	recipientSig, rserr := signStatement(ctx, rsigner, octets, toPub, true)
	if rserr != nil {
		fail(rserr)
	}

	var rep transferReport
	var aerr error
	if endpoint == "" {
		rep, aerr = apiTransfer(local, octets, holderSig, recipientSig, fromHex)
	} else {
		rep, aerr = remoteTransfer(ctx, endpoint, signer, octets, holderSig, recipientSig)
	}
	if aerr != nil {
		fail(aerr)
	}
	fmt.Printf("\nTRANSFERRED %s\n", rep.Namespace)
	fmt.Printf("  from: %s\n", rep.From)
	fmt.Printf("  to:   %s\n", rep.To)
	fmt.Printf("  authority revision: %s\n", rep.AuthorityRev)
	if rep.DelegatesCleared > 0 {
		fmt.Printf("  %d delegation(s) CLEARED — the recipient must re-grant any that should continue\n", rep.DelegatesCleared)
	}
	for _, n := range rep.Retained {
		fmt.Printf("  retained by its existing owner: %s\n", n)
	}
	_ = os.Stdout.Sync()
}
