package main

import "testing"

// Negative zero is a SECOND byte encoding of 0, and identity is the hash of the
// bytes — so accepting it means two content-addressed identities for one value.
//
// Unreachable from the encoder, which is why it survived: only bytes that did not
// come from an encoder can express it. The store's load path checks
// sha256(bytes)==name and typechecks, deliberately not encode(decode(b))==b, so a
// hand-crafted object loaded at a distinct hash in exactly the hostile-store threat
// model that check exists for.
func TestBigintRejectsNegativeZero(t *testing.T) {
	// sign=1, magnitude length 0.
	if _, err := (&dec{b: []byte{1, 0, 0, 0, 0}}).bigint(); err == nil {
		t.Fatal("negative zero decoded: two byte encodings now denote 0, so one value has two identities")
	}

	// The canonical zero must still decode, or the fix has broken every artifact
	// containing a literal 0.
	v, err := (&dec{b: []byte{0, 0, 0, 0, 0}}).bigint()
	if err != nil {
		t.Fatalf("canonical zero was rejected: %v", err)
	}
	if v.Sign() != 0 {
		t.Fatalf("canonical zero decoded to %v", v)
	}

	// Ordinary negatives must be unaffected: sign=1 is only invalid with an EMPTY
	// magnitude. A fix that rejected all negatives would pass the test above.
	neg, err := (&dec{b: []byte{1, 0, 0, 0, 1, 7}}).bigint()
	if err != nil {
		t.Fatalf("a genuine negative was rejected: %v", err)
	}
	if neg.Int64() != -7 {
		t.Fatalf("negative decoded to %v, want -7", neg)
	}
	pos, err := (&dec{b: []byte{0, 0, 0, 0, 1, 7}}).bigint()
	if err != nil || pos.Int64() != 7 {
		t.Fatalf("positive decoded to %v (err %v), want 7", pos, err)
	}

	// And the neighbouring canonicality rule must still hold.
	if _, err := (&dec{b: []byte{0, 0, 0, 0, 2, 0, 7}}).bigint(); err == nil {
		t.Fatal("leading-zero magnitude accepted")
	}
}
