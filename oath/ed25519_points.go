package main

import "math/big"

// Edwards25519 point decoding and the small-order test, in terms of the
// CONDITIONS SPEC §8.6.4a states rather than a table of encodings.
//
// WHY ARITHMETIC RATHER THAN A LIST. The previous implementation compared a key
// against eight hard-coded encodings. That is a set someone wrote down, and the
// rule is what it was written FROM — so the list can only ever be as complete as
// its author's recall, and nothing announces when it is not. It was in fact
// canonical-only: y = p (a second encoding of the ORDER-FOUR point y = 0, not
// of the identity, which is y = 1) passed straight through it, and the forgery
// failed anyway because `crypto/ed25519` happens to refuse non-canonical point
// encodings. A defence that rests on an undocumented property of a dependency is
// not a defence this kernel has made.
//
// Correctness over speed, deliberately: math/big rather than field arithmetic in
// limbs. This runs once per signature verification, on public data, and there is
// no secret to leak through timing — the operand is an attacker-supplied public
// key. Optimising it would trade the one property that matters here (being
// obviously the condition the specification states) for a speed nobody needs.
var (
	// p = 2^255 - 19
	edP, _ = new(big.Int).SetString("57896044618658097711785492504343953926634992332820282019728792003956564819949", 10)
	// d = -121665/121666 mod p
	edD, _ = new(big.Int).SetString("37095705934669439343138083508754565189542113879843219016388785533085940283555", 10)
	// sqrt(-1) mod p, for recovering x when the first candidate is off by a factor
	edSqrtM1, _ = new(big.Int).SetString("19681161376707505956807079304988542015446066515923890162744021073123829784752", 10)
)

func edMul(a, b *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Mul(a, b), edP)
}

func edInv(a *big.Int) *big.Int {
	return new(big.Int).ModInverse(a, edP)
}

// edDecode recovers the affine point from a 32-byte encoding, enforcing
// SIG-POINTS-CANONICAL: y read as a 255-bit little-endian integer MUST be less
// than p, the encoding MUST name a point on the curve, and x = 0 with the sign
// bit set is refused. Returns ok=false for anything else.
func edDecode(b []byte) (x, y *big.Int, ok bool) { return edDecodeAs(b, true) }

// edDecodePermissive reduces y mod p instead of refusing y >= p — what a kernel
// WITHOUT SIG-POINTS-CANONICAL does. It exists so the two rules stay independently
// switchable: with canonicity disabled, the order test must still run on the point
// the permissive reading yields, or disabling one rule would silently disable the
// other and the mutation harness would attribute the failure to the wrong id.
func edDecodePermissive(b []byte) (x, y *big.Int, ok bool) { return edDecodeAs(b, false) }

func edDecodeAs(b []byte, strict bool) (x, y *big.Int, ok bool) {
	if len(b) != 32 {
		return nil, nil, false
	}
	le := make([]byte, 32)
	for i := range le {
		le[i] = b[31-i] // big.Int wants big-endian
	}
	sign := le[0] >> 7
	le[0] &= 0x7f
	y = new(big.Int).SetBytes(le)
	// CANONICAL: y >= p is a second encoding of a point that already has one.
	// Rejecting it is what stops a non-canonical small-order key being admitted.
	if y.Cmp(edP) >= 0 {
		if strict {
			return nil, nil, false
		}
		y = new(big.Int).Mod(y, edP)
	}
	// x^2 = (y^2 - 1) / (d y^2 + 1)
	y2 := edMul(y, y)
	u := new(big.Int).Mod(new(big.Int).Sub(y2, big.NewInt(1)), edP)
	v := new(big.Int).Mod(new(big.Int).Add(edMul(edD, y2), big.NewInt(1)), edP)
	vInv := edInv(v)
	if vInv == nil {
		return nil, nil, false
	}
	x2 := edMul(u, vInv)
	// Candidate root: x = x2^((p+3)/8).
	e := new(big.Int).Rsh(new(big.Int).Add(edP, big.NewInt(3)), 3)
	x = new(big.Int).Exp(x2, e, edP)
	if edMul(x, x).Cmp(x2) != 0 {
		x = edMul(x, edSqrtM1)
		if edMul(x, x).Cmp(x2) != 0 {
			return nil, nil, false // y names no point on the curve
		}
	}
	if x.Sign() == 0 && sign == 1 && strict {
		return nil, nil, false // x = 0 has one encoding, not two
	}
	if byte(x.Bit(0)) != sign {
		x = new(big.Int).Sub(edP, x)
	}
	return x, y, true
}

// edAdd is the COMPLETE affine addition law for twisted Edwards a = -1. Complete
// because d is a non-square mod p, so the denominators never vanish — no special
// case for doubling, for the identity, or for a point plus its negative, and
// therefore no branch that a crafted input can steer.
func edAdd(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	x1x2, y1y2 := edMul(x1, x2), edMul(y1, y2)
	dxy := edMul(edD, edMul(x1x2, y1y2))
	one := big.NewInt(1)
	nx := new(big.Int).Mod(new(big.Int).Add(edMul(x1, y2), edMul(y1, x2)), edP)
	dx := new(big.Int).Mod(new(big.Int).Add(one, dxy), edP)
	ny := new(big.Int).Mod(new(big.Int).Add(y1y2, x1x2), edP) // a = -1
	dy := new(big.Int).Mod(new(big.Int).Sub(one, dxy), edP)
	dxi, dyi := edInv(dx), edInv(dy)
	if dxi == nil || dyi == nil {
		return nil, nil
	}
	return edMul(nx, dxi), edMul(ny, dyi)
}

// edIsSmallOrder reports whether [8]A is the identity — the cofactor condition
// SPEC §8.6.4a states. Written as the condition rather than as the eight points
// it admits, so the set is COMPUTED and cannot be short by one.
func edIsSmallOrder(x, y *big.Int) bool {
	for i := 0; i < 3; i++ { // three doublings = [8]A
		x, y = edAdd(x, y, x, y)
		if x == nil {
			return false
		}
	}
	return x.Sign() == 0 && y.Cmp(big.NewInt(1)) == 0
}
