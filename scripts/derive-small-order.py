#!/usr/bin/env python3
"""Derive the Ed25519 small-order point encodings from the curve definition.

WHY THIS EXISTS. SPEC §8.6.4a requires rejecting a small-order public key: signatures
under one verify for parties who do not hold it, so it cannot carry an authorship
claim. Detecting them needs either curve arithmetic in the kernel or a blocklist of
their encodings. The kernel takes the blocklist, because a byte comparison needs no
arithmetic and no new dependency — the default build is dependency-free by design.

A blocklist is only as trustworthy as its constants, and constants recalled from
memory are exactly the failure this project has already been bitten by: a blind
implementation's first RFC 8032 test vector was misremembered, and the correct
response was to build an independent oracle rather than adjust the code to match.

So nothing here is transcribed. Everything is computed from p, d and L, and every
point is CHECKED — on the curve, [8]P = identity, order dividing 8 — before it is
printed. The base point is checked NOT to be among them, so the derivation cannot
pass vacuously by producing an empty or degenerate set.

Run: python3 scripts/derive-small-order.py
Exits non-zero if any check fails, and prints nothing usable in that case.
"""

p = 2**255 - 19
d = (-121665 * pow(121666, p - 2, p)) % p
L = 2**252 + 27742317777372353535851937790883648493

IDENT = (0, 1)


def inv(a):
    return pow(a, p - 2, p)


def on_curve(P):
    x, y = P
    return (-x * x + y * y - 1 - d * x * x * y * y) % p == 0


def add(P, Q):
    x1, y1 = P
    x2, y2 = Q
    prod = d * x1 * x2 * y1 * y2 % p
    xd, yd = (1 + prod) % p, (1 - prod) % p
    if xd == 0 or yd == 0:
        raise ValueError("exceptional case in affine addition")
    return ((x1 * y2 + y1 * x2) * inv(xd) % p, (y1 * y2 + x1 * x2) * inv(yd) % p)


def mul(k, P):
    R, Q = IDENT, P
    while k:
        if k & 1:
            R = add(R, Q)
        Q = add(Q, Q)
        k >>= 1
    return R


def sqrt_mod(a):
    """Square root mod p for p == 5 (mod 8); None when a is not a residue."""
    a %= p
    if a == 0:
        return 0
    c = pow(a, (p + 3) // 8, p)
    if c * c % p == a:
        return c
    c = c * pow(2, (p - 1) // 4, p) % p
    return c if c * c % p == a else None


def point_from_y(y):
    """x^2 = (y^2 - 1) / (d y^2 + 1)."""
    den = (d * y * y + 1) % p
    if den == 0:
        return None
    x = sqrt_mod((y * y - 1) * inv(den) % p)
    return None if x is None else (x, y)


def encode(P):
    """RFC 8032: y little-endian, most significant bit carries the sign of x."""
    x, y = P
    b = bytearray(y.to_bytes(32, "little"))
    b[31] |= (x & 1) << 7
    return bytes(b)


def order_of(P, bound=16):
    for k in range(1, bound + 1):
        if mul(k, P) == IDENT:
            return k
    return None


def main():
    fail = []

    # Derive a torsion generator without assuming any coordinates: a generic point R
    # has order 8L, so [L]R has order 8. Scan y values until one yields such a point.
    gen = None
    for y in range(2, 1000):
        R = point_from_y(y)
        if R is None or not on_curve(R):
            continue
        S = mul(L, R)
        if S != IDENT and order_of(S) == 8:
            gen = S
            break
    if gen is None:
        print("FAIL: could not derive a torsion generator")
        return 1

    points = [mul(k, gen) for k in range(1, 9)]  # <gen> = the full 8-torsion
    if len(set(points)) != 8:
        fail.append("torsion subgroup has %d distinct elements, expected 8" % len(set(points)))
    if IDENT not in points:
        fail.append("the identity is not in <gen>: the generator's order is wrong")

    for P in points:
        if not on_curve(P):
            fail.append("not on the curve: %r" % (P,))
        if mul(8, P) != IDENT:
            fail.append("[8]P != identity: %r" % (P,))
        o = order_of(P)
        if o is None or 8 % o != 0:
            fail.append("order %r does not divide 8" % (o,))

    # Non-vacuity: the base point must exist, satisfy [L]B = identity, and NOT be
    # small-order. Without this, a broken derivation could "pass" by emitting garbage.
    B = point_from_y(4 * inv(5) % p)
    if B is None:
        fail.append("could not reconstruct the base point")
    else:
        if B[0] & 1:
            B = ((-B[0]) % p, B[1])
        if not on_curve(B):
            fail.append("base point is not on the curve")
        if mul(L, B) != IDENT:
            fail.append("[L]B != identity: curve parameters are wrong")
        if B in points:
            fail.append("the base point was classified as small-order")

    if fail:
        for f in fail:
            print("FAIL:", f)
        return 1

    ordered = sorted(points, key=lambda P: encode(P).hex())
    print("// DERIVED by scripts/derive-small-order.py from p, d and L — NOT transcribed.")
    print("// Every entry verified on-curve with [8]P = identity and order dividing 8;")
    print("// the base point is verified NOT to be among them, so the derivation cannot")
    print("// pass vacuously. Re-run the script to reproduce this list.")
    print("var smallOrderEncodings = [][32]byte{")
    for P in ordered:
        print("\t{" + ", ".join("0x%02x" % c for c in encode(P)) + "}, // order %d" % order_of(P))
    print("}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
