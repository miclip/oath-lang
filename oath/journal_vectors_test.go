package main

import (
	"bytes"
	"testing"
)

// The journal corpus's COVERAGE is asserted directly on the produced bytes, not
// on the committed file, so a regeneration cannot quietly narrow what
// conformance check 9 reaches. Fixture integrity proves the committed tree IS
// the generator's output; it cannot notice that the generator started producing
// a weaker corpus. Those are different claims and both are needed.
func TestJournalVectorsExerciseTheEscapingRules(t *testing.T) {
	b, err := buildJournalVectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`\u0000`, `\u001f`, `\u0001`, // C0 controls, LOWERCASE hex
		`\u2028`, `\u2029`, // escaped although JSON permits them raw
		`\"`, `\\`, `\b`, `\f`, `\n`, `\r`, `\t`, // the short forms
		"lt=< gt=> amp=& slash=/", // MUST NOT be escaped
		"e-acute=\u00e9",          // literal UTF-8, not an escape
		"</script>",
		`"time":"","author":"","verifier":"","name":"","status":""`, // the omission exception
	} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("the corpus does not exercise %q -- check 9 would be blind to it", want)
		}
	}
	for _, forbidden := range []string{`\u003c`, `\u003e`, `\u0026`, `\u002f`, `\u00e9`} {
		if bytes.Contains(b, []byte(forbidden)) {
			t.Errorf("the corpus contains %q, which SPEC 8.2.1 forbids emitting", forbidden)
		}
	}
	// EVERY member must appear, derived from the normative order rather than from
	// a list written here: a hand-kept list is exactly what let six signing
	// members go uncovered while a comment claimed the corpus populated them all.
	for _, m := range journalFieldOrder {
		if !bytes.Contains(b, []byte(`"`+m+`":`)) {
			t.Errorf("member %q never appears, so its ORDER and encoding are unwitnessed", m)
		}
	}
	// The pre-chain prefix must exist, or the legacy-anchor path never runs.
	if n := bytes.Count(b, []byte(`"chain"`)); n != 7 {
		t.Errorf("expected 7 chained entries after a 2-entry legacy prefix, found %d", n)
	}
}

// The pinned digests must be what the digest command actually emits, or check 9
// compares oathrs against an answer the reference kernel no longer gives.
func TestJournalVectorDigestsMatchTheCorpus(t *testing.T) {
	corpus, err := buildJournalVectors()
	if err != nil {
		t.Fatal(err)
	}
	d, err := journalVectorDigests(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(d, []byte("\n")); n != 9 {
		t.Fatalf("pinned digests cover %d entries, want 9", n)
	}
}
