package main

import (
	"fmt"
	"os"
)

// cmdJournalDigest emits one line per journal entry: its seq, its §8.2.2 entry
// digest, and its chain RECOMPUTED from the preceding bytes.
//
// It exists for conformance check 9, and the choice of what to emit is the whole
// point. A second kernel agreeing on these three columns has agreed on §8.2.1's
// member order and omission rule, on its string escaping over whatever the input
// contains, and on §8's chain construction — including the two things that had
// no fixture at all until this check: whether the anchor is the chain's HEX TEXT
// or the bytes it renders, and whether the legacy prefix includes the preceding
// entries' LF separators. Both determine every downstream chain value, so a
// disagreement on either shows up here on the first affected entry.
//
// The chain is RECOMPUTED rather than read back. Printing the stored value would
// compare what each kernel can copy, not what each kernel computes — the two
// look identical in the output and only one of them is a claim about the kernel.
func cmdJournalDigest(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	pos, line := 0, 0
	for pos < len(b) {
		end := pos
		for end < len(b) && b[end] != '\n' {
			end++
		}
		line++
		// STRICT reading (§8.2.1): parse, re-encode, and reject unless the bytes
		// are reproduced exactly. A lenient read here would let each kernel
		// normalize a non-canonical line into agreement, which is the one result
		// this check must never be able to produce.
		e, perr := strictJournalLine(b[pos:end])
		if perr != nil {
			fail(fmt.Errorf("journal line %d: %w", line, perr))
		}
		d, derr := entryDigest(e)
		if derr != nil {
			fail(fmt.Errorf("journal line %d: %w", line, derr))
		}
		anchor := chainAnchor(b[:pos])
		saved := e.Chain
		e.Chain = ""
		body, berr := canonicalJournalLine(e)
		if berr != nil {
			fail(fmt.Errorf("journal line %d: %w", line, berr))
		}
		e.Chain = saved
		fmt.Printf("%d %s %s\n", e.Seq, d, chainHash(anchor, body))
		pos = end + 1
	}
	if line == 0 {
		fail(fmt.Errorf("%s holds no journal entries; check 9 would compare two empty streams", path))
	}
}
