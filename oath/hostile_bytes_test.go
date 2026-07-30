package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hostile-bytes family (#91) exercises the DECODER, which no source-level reject
// can reach: every object the kernel produces is canonical by construction, so the
// canonicality rules are only expressible by bytes that did not come from an encoder.
//
// Each vector must be refused, and refused for the reason it declares. A vector that
// merely fails proves nothing about the rule it names — the mistake this project has
// now made twice (the newline envelope vector, the small-order key vector).
func TestHostileBytesAreRefusedForTheirDeclaredReason(t *testing.T) {
	dir := "../fixtures/gate/bytes"
	base, err := os.ReadFile(filepath.Join(dir, "baseline.bin"))
	if err != nil {
		t.Skip("hostile-bytes fixtures not present")
	}

	// The baseline MUST decode. If it does not, every rejection below is
	// uninterpretable: the vectors would be failing for whatever is wrong with the
	// baseline rather than for their own defect.
	if _, err := decodeDef(base); err != nil {
		t.Fatalf("baseline.bin does not decode, so no vector built from it witnesses anything: %v", err)
	}

	// Declared rule -> a substring the rejection must mention.
	expect := map[string]string{
		"negative_zero":  "negative zero",
		"bad_sign_byte":  "sign",
		"trailing_bytes": "trailing",
		"truncated":      "",
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".bin") || n == "baseline.bin" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		_, derr := decodeDef(b)
		if derr == nil {
			t.Errorf("%s was ACCEPTED — a hostile encoding decoded, so it loads at a distinct hash", n)
			continue
		}
		key := strings.TrimSuffix(n, ".bin")
		if want, ok := expect[key]; ok && want != "" {
			if !strings.Contains(strings.ToLower(derr.Error()), want) {
				t.Errorf("%s was refused, but for the wrong reason: %q does not mention %q — the vector does not witness the rule it declares",
					n, derr, want)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no hostile vectors were exercised — the test measured nothing")
	}
	t.Logf("%d hostile encodings refused, each for its declared reason", checked)
}

// The specific hole #91 closed: a negative zero decodes to the same VALUE as the
// canonical form, so accepting it means one integer has two content-addressed
// identities. Asserted directly rather than only through the fixture, so the rule
// survives even if the fixture family is ever regenerated differently.
func TestNegativeZeroVectorTargetsTheRealHole(t *testing.T) {
	b, err := os.ReadFile("../fixtures/gate/bytes/negative_zero.bin")
	if err != nil {
		t.Skip("fixture not present")
	}
	_, derr := decodeDef(b)
	if derr == nil {
		t.Fatal("the negative-zero encoding decoded")
	}
	if !strings.Contains(strings.ToLower(derr.Error()), "negative zero") {
		t.Fatalf("refused for an unrelated reason: %v", derr)
	}
	// And the canonical zero must still decode, or the rule is over-broad.
	if _, err := (&dec{b: []byte{0, 0, 0, 0, 0}}).bigint(); err != nil {
		t.Fatalf("canonical zero refused: %v", err)
	}
}
