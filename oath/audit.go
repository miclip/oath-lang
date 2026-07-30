package main

// `oath audit` — the auditor's entry point (#83).
//
// VerifyLog has existed since #14 and was reachable only from tests, which meant
// the tamper-evidence the project claims was real but not RUNNABLE by the people
// it exists for. An audit trail nobody outside the test suite can check is a
// promise, not a guarantee.
//
// The four independent checks an auditor performs, and what each one buys:
//
//  1. AUTHOR SIGNATURE over the persisted envelope bytes — "this key requested
//     this exact publication". Verified against the stored bytes, never a
//     re-encoding, so an old entry is checked under the format it was written
//     with.
//  2. ENVELOPE vs ENTRY agreement — the name, artifact and parent the author
//     signed are the transition the registry actually recorded. Catches a registry
//     that accepted something its author did not sign.
//  3. REGISTRY CUSTODY — the append-only chain, each entry sealing the previous,
//     so entries cannot be reordered, edited or removed from the middle.
//  4. FINDINGS ARE RE-DERIVABLE — status, guarantee and termination are functions
//     of content bytes, so they are not taken on trust from this record at all.
//     Nothing to verify here; that is the point, and `oath explain` shows them.
//
// Checks 1-3 are VerifyLog's job. What this command adds is coverage reporting,
// because "the journal verifies" and "the journal proves who authored what" are
// different statements: an entry with no author record verifies perfectly and
// attests to nothing. Reporting unsigned coverage as a headline number keeps that
// distinction visible rather than letting a green check imply attribution the
// record does not carry.

import (
	"fmt"
	"sort"
)

func cmdAudit(st *Store) {
	// Integrity first: coverage figures computed over a tampered journal would be
	// describing a file that has already failed.
	if err := st.VerifyLog(); err != nil {
		fmt.Printf("JOURNAL: FAILED\n  %v\n", err)
		fmt.Printf("\nThe chain, a signature, or an envelope/entry agreement check did not\nhold. Treat every attribution below as unusable until this is resolved.\n")
		fail(fmt.Errorf("journal verification failed"))
	}

	entries := st.ReadLog()
	var accepted, authored, unsigned int
	byKey := map[string]int{}
	labels := map[string]int{}
	for _, e := range entries {
		if e.Status != "accepted" {
			continue
		}
		accepted++
		if e.EnvelopeB64 != "" {
			authored++
			byKey[e.AuthorPubkey]++
			continue
		}
		unsigned++
		who := e.Author
		if who == "" {
			who = "unattributed"
		}
		labels[who]++
	}

	fmt.Printf("JOURNAL: VERIFIED — %d entries, chain intact\n", len(entries))
	fmt.Printf("  every author signature present verifies against its PERSISTED envelope bytes,\n")
	fmt.Printf("  and each envelope agrees with the transition the entry records.\n\n")

	fmt.Printf("AUTHORSHIP COVERAGE (%d accepted publications):\n", accepted)
	fmt.Printf("  %6d  cryptographically authored — a key signed the exact publication\n", authored)
	fmt.Printf("  %6d  UNSIGNED — the recorded author is a label, not evidence\n", unsigned)

	if len(byKey) > 0 {
		fmt.Printf("\nSIGNING KEYS:\n")
		for _, k := range sortedByCount(byKey) {
			fmt.Printf("  %6d  %s\n", byKey[k], k)
		}
	}
	if len(labels) > 0 {
		fmt.Printf("\nUNSIGNED, BY RECORDED LABEL (these are claims, not attestations):\n")
		for _, k := range sortedByCount(labels) {
			fmt.Printf("  %6d  %s\n", labels[k], k)
		}
	}

	if unsigned > 0 {
		fmt.Printf("\nWHAT THE UNSIGNED ENTRIES DO AND DO NOT ESTABLISH:\n")
		fmt.Printf("  The chain proves they have not been altered since they were written.\n")
		fmt.Printf("  It does NOT establish who wrote them: the author field is whatever the\n")
		fmt.Printf("  registry recorded, so a third party must trust the registry rather than\n")
		fmt.Printf("  check a signature. Publishing with `oath put --remote --key` produces\n")
		fmt.Printf("  entries that carry their own proof.\n")
	}
}

func sortedByCount(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if m[ks[i]] != m[ks[j]] {
			return m[ks[i]] > m[ks[j]]
		}
		return ks[i] < ks[j]
	})
	return ks
}

// cmdAuditEntry prints the full verifiable record for one publication — the
// author's signed statement and the registry's findings side by side, so a reader
// can see which half is which. Selected by artifact hash or name.
func cmdAuditEntry(st *Store, ref string) {
	entries := st.ReadLog()
	var hits []LogEntry
	for _, e := range entries {
		if e.Hash == ref || e.Name == ref {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		fail(fmt.Errorf("no journal entry for %q (try a name or a full artifact hash)", ref))
	}
	for _, e := range hits {
		fmt.Printf("── seq %d  %s  %s  %s\n", e.Seq, e.Time, e.Status, e.Name)
		fmt.Printf("   artifact %s\n", orNone(e.Hash))
		if e.EnvelopeB64 == "" {
			fmt.Printf("   AUTHOR STATEMENT: none — author=%q is a label the registry recorded,\n", e.Author)
			fmt.Printf("                     with no signature behind it.\n")
		} else {
			fmt.Printf("   AUTHOR STATEMENT (exact signed bytes, verified):\n")
			for _, l := range splitLines(envelopeTextOf(e)) {
				fmt.Printf("     | %s\n", l)
			}
			fmt.Printf("   signed by %s\n", e.AuthorPubkey)
		}
		fmt.Printf("   REGISTRY FINDINGS (re-derivable from the artifact, not taken on trust):\n")
		fmt.Printf("     guarantee=%s termination=%s\n", orNone(e.Guarantee), orNone(e.Termination))
		fmt.Printf("   REGISTRY CUSTODY: chain=%s\n\n", shortHash(e.Chain))
	}
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// envelopeTextOf recovers the author's statement for DISPLAY. Decode failures are
// shown rather than hidden: VerifyLog has already rejected the journal if this
// cannot decode, so reaching here with bad data means something is very wrong.
func envelopeTextOf(e LogEntry) string {
	octets, err := decodeEnvelopeB64(e.EnvelopeB64)
	if err != nil {
		return "<undecodable envelope: " + err.Error() + ">"
	}
	return string(octets)
}
