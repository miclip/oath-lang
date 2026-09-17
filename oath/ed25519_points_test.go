package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"testing"
)

// yEqualsP is a NON-CANONICAL encoding: p reduces to 0, so these bytes name the
// same point as the all-zero encoding -- which is the ORDER-FOUR point y = 0, not
// the identity (y = 1) -- while differing from it byte for byte. The
// superseded blocklist held canonical encodings only, so this passed straight
// through it — the forgery then failed for an unrelated reason, because
// crypto/ed25519 happens to refuse non-canonical point encodings. This is the
// case that makes the cofactor condition worth computing rather than tabulating.
func yEqualsP() []byte {
	b := make([]byte, 32)
	b[0] = 0xed
	for i := 1; i < 31; i++ {
		b[i] = 0xff
	}
	b[31] = 0x7f
	return b
}

func TestRejectsNonCanonicalPointEncodings(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  []byte
	}{
		{"y = p (a second encoding of the order-4 point y = 0)", yEqualsP()},
		{"y = p+1", func() []byte {
			b := yEqualsP()
			b[0] = 0xee
			return b
		}()},
		{"y = 2^255-1 (all bits set below the sign)", func() []byte {
			b := make([]byte, 32)
			for i := range b {
				b[i] = 0xff
			}
			b[31] = 0x7f
			return b
		}()},
	} {
		if err := rejectWeakKey(tc.key); err == nil {
			t.Errorf("%s was ACCEPTED as a key", tc.name)
		}
	}
}

// The control that makes the test above mean something: an ordinary key must
// still decode and be admitted. A check that refused everything would pass every
// rejection assertion while breaking the system.
func TestOrdinaryKeysStillDecodeAndAreAdmitted(t *testing.T) {
	for i := 0; i < 16; i++ {
		pub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok := edDecode(pub); !ok {
			t.Fatalf("a freshly generated key failed to decode: %s", hex.EncodeToString(pub))
		}
		if err := rejectWeakKey(pub); err != nil {
			t.Fatalf("a freshly generated key was refused: %v", err)
		}
	}
}

// The decoder must round-trip: the point it recovers must satisfy the curve
// equation -x^2 + y^2 = 1 + d x^2 y^2. Without this the "on the curve" clause is
// asserted by the code that is supposed to be establishing it.
func TestDecodedPointsSatisfyTheCurveEquation(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	x, y, ok := edDecode(pub)
	if !ok {
		t.Fatal("decode failed for a valid key")
	}
	x2, y2 := edMul(x, x), edMul(y, y)
	lhs := new(big.Int).Mod(new(big.Int).Sub(y2, x2), edP)
	rhs := new(big.Int).Mod(new(big.Int).Add(big.NewInt(1), edMul(edD, edMul(x2, y2))), edP)
	if lhs.Cmp(rhs) != 0 {
		t.Fatalf("decoded point is not on the curve:\n lhs %s\n rhs %s", lhs, rhs)
	}
}

// [8]A == identity must hold for exactly the 8-torsion points and no others.
// Checked in BOTH directions, because a predicate that answers "true" for
// everything satisfies the first direction perfectly.
func TestCofactorConditionAgreesWithTheKnownSubgroup(t *testing.T) {
	for i, k := range smallOrderCanonicalEncodings {
		x, y, ok := edDecode(k[:])
		if !ok {
			t.Errorf("control point %d does not decode, so the computation never sees it", i)
			continue
		}
		if !edIsSmallOrder(x, y) {
			t.Errorf("control point %d (%x) is small-order but [8]A was not the identity", i, k[:6])
		}
	}
	for i := 0; i < 16; i++ {
		pub, _, _ := ed25519.GenerateKey(nil)
		x, y, ok := edDecode(pub)
		if !ok {
			continue
		}
		if edIsSmallOrder(x, y) {
			t.Fatalf("an ordinary key was reported small-order: %s", hex.EncodeToString(pub))
		}
	}
}

// The identity must be caught by the COMPUTATION, not by its presence in a list,
// so this asserts the arithmetic path directly rather than going through
// rejectWeakKey, which would pass either way.
//
// The identity is y = 1, encoded 0x01 followed by zeros. The ALL-ZERO encoding
// is y = 0, a different point — it has order 4 — and the two are easy to confuse
// because both look like "empty" bytes. Getting that backwards is what this
// test caught on its first run, in the test rather than in the decoder.
func TestIdentityAndTheOrderFourPointAreBothSmallOrder(t *testing.T) {
	identity := make([]byte, 32)
	identity[0] = 0x01
	x, y, ok := edDecode(identity)
	if !ok {
		t.Fatal("the identity encoding failed to decode")
	}
	if x.Sign() != 0 || y.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("the identity encoding decoded to (%s, %s), want (0, 1)", x, y)
	}
	if !edIsSmallOrder(x, y) {
		t.Fatal("[8]identity was not the identity")
	}
	// y = 0: order 4, and NOT the identity. Asserted so the two stay
	// distinguishable in this file rather than only in the blocklist comments.
	x0, y0, ok := edDecode(make([]byte, 32))
	if !ok {
		t.Fatal("the all-zero encoding failed to decode")
	}
	if y0.Sign() != 0 || x0.Sign() == 0 {
		t.Fatalf("the all-zero encoding decoded to (%s, %s); it is y = 0, not the identity", x0, y0)
	}
	if !edIsSmallOrder(x0, y0) {
		t.Fatal("the order-4 point was not reported small-order")
	}
}
