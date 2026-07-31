package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// The normative field-order list must match what the encoder actually emits.
// A drift here is invisible until a second implementation computes different chain
// hashes from the same logical entry.
func TestJournalFieldOrderIsNormative(t *testing.T) {
	// Every field populated, so omitempty hides nothing.
	e := &LogEntry{Seq: 1, Time: "t", Author: "a", Verifier: "v", Name: "n", Kind: "func",
		Status: "accepted", Hash: "h", Prev: "p", Error: "e", Guarantee: "g",
		Termination: "structural", Context: "c", Pubkey: "pk", Sig: "s",
		EnvelopeB64: "ZQ==", AuthorPubkey: "apk", AuthorSig: "as", ParentRev: "37",
		NameTransition: transitionApplied, Chain: "ch"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, _ := dec.Token() // '{'
	_ = tok
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, k.(string))
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != len(journalFieldOrder) {
		t.Fatalf("emitted %d fields, journalFieldOrder lists %d:\n emitted %v\n listed  %v", len(got), len(journalFieldOrder), got, journalFieldOrder)
	}
	for i := range got {
		if got[i] != journalFieldOrder[i] {
			t.Fatalf("field %d: emitted %q, normative order says %q\n emitted %v", i, got[i], journalFieldOrder[i], got)
		}
	}
}

// Strict reading: a re-ordered or re-spaced entry must be rejected even though it
// parses to the same values, because chain hashes and signatures are over the bytes.
func TestStrictJournalLineRejectsNonCanonical(t *testing.T) {
	e := &LogEntry{Seq: 1, Name: "n", Status: "accepted", Hash: "h"}
	good, _ := canonicalJournalLine(e)
	if _, err := strictJournalLine(good); err != nil {
		t.Fatalf("canonical line rejected: %v", err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"reordered keys", `{"name":"n","seq":1,"status":"accepted","hash":"h"}`},
		{"extra whitespace", `{"seq": 1,"name":"n","status":"accepted","hash":"h"}`},
		{"unknown field", `{"seq":1,"name":"n","status":"accepted","hash":"h","bogus":1}`},
		{"trailing content", string(good) + `{}`},
	} {
		if _, err := strictJournalLine([]byte(tc.raw)); err == nil {
			t.Fatalf("%s: accepted — a second byte spelling of one entry means two identities for it", tc.name)
		}
	}
}

// String escaping is pinned, and the writer must agree with the canonical reader.
//
// This is the case that would have forked two kernels silently: a rejection message
// containing "<" is ordinary, Go's default encoder escapes it as \u003c for HTML
// safety, and no independent implementer reading RFC 8259 would do the same. Since
// chain hashes, entry signatures and entry digests are all over these bytes, the two
// would compute different identities for one entry and reject each other's journals.
func TestCanonicalEscapingIsPinned(t *testing.T) {
	e := &LogEntry{Seq: 1, Time: "t", Author: "a", Name: "n", Status: "rejected",
		Error: `a<b>c&d/e"f\g`}
	line, err := canonicalJournalLine(e)
	if err != nil {
		t.Fatal(err)
	}
	got := string(line)

	// Only what JSON requires: '"' and '\'. NOT <, >, &, /.
	for _, bad := range []string{`\u003c`, `\u003e`, `\u0026`, `\/`} {
		if strings.Contains(got, bad) {
			t.Fatalf("canonical form contains %s — that is a language-specific escaping habit, not an RFC 8259 requirement:\n%s", bad, got)
		}
	}
	for _, want := range []string{`a<b>c&d/e`, `\"`, `\\`} {
		if !strings.Contains(got, want) {
			t.Fatalf("canonical form is missing %s:\n%s", want, got)
		}
	}

	// And the writer's bytes must survive the strict reader, or the journal would be
	// unreadable by its own verifier the first time an error message contained "<".
	if _, err := strictJournalLine(line); err != nil {
		t.Fatalf("the canonical encoder produced a line its own strict reader rejects: %v", err)
	}
}

// A journal written through AppendLog must be readable by the strict reader, for an
// entry whose free-form text exercises the escaping rules.
func TestAppendedEntryIsCanonical(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{Author: "a", Name: "n", Status: "rejected",
		Error: `unexpected "<" in <input> & more`}); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("an appended entry with escape-sensitive text broke verification: %v", err)
	}
	raw, err := st.be.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	line := bytes.TrimSuffix(raw, []byte("\n"))
	if _, err := strictJournalLine(line); err != nil {
		t.Fatalf("the stored line is not canonical per the strict reader: %v\n%s", err, line)
	}
}

// The record separator must NOT be part of entry identity: framing is a transport
// concern, and folding it in would make the digest depend on how records are joined.
func TestEntryDigestExcludesRecordSeparator(t *testing.T) {
	e := &LogEntry{Seq: 1, Time: "t", Author: "a", Name: "n", Status: "accepted", Hash: "h"}
	line, err := canonicalJournalLine(e)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		t.Fatal("the canonical line includes a trailing newline: the separator would enter the chain, the signature and the digest")
	}
	d1, err := entryDigest(e)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(line)
	if d1 != hex.EncodeToString(sum[:]) {
		t.Fatal("entryDigest is not SHA-256 over the canonical line alone")
	}
}
