package main

import (
	"bytes"
	"encoding/json"
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
		EnvelopeB64: "ZQ==", AuthorPubkey: "apk", AuthorSig: "as",
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
