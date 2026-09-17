//! Ed25519 under the convention SPEC §8.6.4a pins.
//!
//! "A valid Ed25519 signature" is not one predicate, and §8.6.4a says so: the
//! choices are observable between implementations, so the section fixes them
//! and this module implements exactly that fixing.
//!
//!   * **SIG-COFACTORLESS** — RFC 8032 §5.1.7 with `[S]B = R + [k]A`. The
//!     cofactored equation `[8S]B = [8]R + [8k]A` is NOT used; it accepts
//!     signatures this one refuses.
//!   * **SIG-S-CANONICAL** — the encoded `S` must be strictly less than the
//!     group order `L`. `S + L` is a second encoding of one signature, which
//!     contradicts an envelope being *the author's statement*.
//!   * **SIG-POINTS-CANONICAL** — `R` and `A` must be canonical point
//!     encodings: the y coordinate, read little-endian with bit 255 removed,
//!     must be less than `p`, and the encoding must name a curve point.
//!   * **SIG-SMALL-ORDER** — a small-order `A` is REJECTED. §8.6.4a settled
//!     this as a MUST rather than a MAY because two conforming verifiers
//!     disagreeing about one signature would make journal validity
//!     unportable.
//!
//! §8.6.4a says nothing about small-order `R`, and §10.1 lists that gap
//! explicitly; this module therefore imposes no order condition on `R`. That is
//! the text implemented as written, not a judgement that `R`'s order is
//! harmless.
//!
//! IMPLEMENTATION NOTE. The arithmetic is plain `BigUint` modular arithmetic in
//! extended twisted-Edwards coordinates — the reference formulas of RFC 8032
//! §5.1.4, transcribed. It is not constant-time and makes no attempt to be:
//! everything here operates on PUBLIC data (a published key, a published
//! signature, published octets), and the one secret-bearing operation, signing,
//! exists to generate test material rather than to hold a production key. A
//! kernel wanting to sign with a real key should say so and use a hardened
//! implementation; nothing in §8.6 requires this one to.

use num_bigint::BigUint;
use sha2::{Digest, Sha512};

/// `0` and `1` as field elements. Spelled out rather than pulled from
/// `num_traits`: that crate is a transitive dependency of `num-bigint`, not a
/// declared one, and depending on it here would add a dependency edge the
/// manifest does not state.
fn zero() -> BigUint {
    BigUint::from(0u32)
}

fn one() -> BigUint {
    BigUint::from(1u32)
}

// ---------------------------------------------------------------------------
// Curve constants
// ---------------------------------------------------------------------------

/// `p = 2^255 - 19`, the field characteristic.
fn p() -> BigUint {
    (one() << 255u32) - BigUint::from(19u32)
}

/// `L = 2^252 + 27742317777372353535851937790883648493`, the group order —
/// the bound SIG-S-CANONICAL compares against.
fn order_l() -> BigUint {
    (one() << 252u32)
        + BigUint::parse_bytes(b"27742317777372353535851937790883648493", 10).unwrap()
}

/// `d = -121665/121666 mod p`, the curve parameter.
fn curve_d() -> BigUint {
    let p = p();
    let num = &p - BigUint::from(121665u32);
    let den = BigUint::from(121666u32);
    fmul(&num, &finv(&den, &p), &p)
}

/// `sqrt(-1) mod p = 2^((p-1)/4)`, used to recover `x` from `y`.
fn sqrt_m1() -> BigUint {
    let p = p();
    BigUint::from(2u32).modpow(&((&p - one()) >> 2u32), &p)
}

// ---------------------------------------------------------------------------
// Field arithmetic
// ---------------------------------------------------------------------------

fn fadd(a: &BigUint, b: &BigUint, p: &BigUint) -> BigUint {
    (a + b) % p
}

fn fsub(a: &BigUint, b: &BigUint, p: &BigUint) -> BigUint {
    (a + p - b) % p
}

fn fmul(a: &BigUint, b: &BigUint, p: &BigUint) -> BigUint {
    (a * b) % p
}

/// `a^(p-2) mod p` — inversion by Fermat, correct for every non-zero `a` and
/// returning zero for zero, which the callers never rely on.
fn finv(a: &BigUint, p: &BigUint) -> BigUint {
    a.modpow(&(p - BigUint::from(2u32)), p)
}

// ---------------------------------------------------------------------------
// Points, in extended coordinates (X:Y:Z:T) with x = X/Z, y = Y/Z, xy = T/Z
// ---------------------------------------------------------------------------

#[derive(Clone, Debug)]
struct Point {
    x: BigUint,
    y: BigUint,
    z: BigUint,
    t: BigUint,
}

impl Point {
    fn identity() -> Point {
        Point { x: zero(), y: one(), z: one(), t: zero() }
    }

    fn is_identity(&self, p: &BigUint) -> bool {
        // x == 0 and y == z (i.e. affine (0,1)).
        self.x == zero() && fsub(&self.y, &self.z, p) == zero()
    }

    fn eq_point(&self, other: &Point, p: &BigUint) -> bool {
        fmul(&self.x, &other.z, p) == fmul(&other.x, &self.z, p)
            && fmul(&self.y, &other.z, p) == fmul(&other.y, &self.z, p)
    }
}

/// RFC 8032 §5.1.4 addition in extended coordinates. It is unified: the same
/// formula doubles a point, so the ladder below needs no separate doubling
/// case and no exceptional-point handling.
fn point_add(q: &Point, r: &Point, p: &BigUint, d2: &BigUint) -> Point {
    let a = fmul(&fsub(&q.y, &q.x, p), &fsub(&r.y, &r.x, p), p);
    let b = fmul(&fadd(&q.y, &q.x, p), &fadd(&r.y, &r.x, p), p);
    let c = fmul(&fmul(&q.t, d2, p), &r.t, p);
    let dd = fmul(&fmul(&q.z, &BigUint::from(2u32), p), &r.z, p);
    let e = fsub(&b, &a, p);
    let f = fsub(&dd, &c, p);
    let g = fadd(&dd, &c, p);
    let h = fadd(&b, &a, p);
    Point {
        x: fmul(&e, &f, p),
        y: fmul(&g, &h, p),
        t: fmul(&e, &h, p),
        z: fmul(&f, &g, p),
    }
}

/// `[n]q` by double-and-add over the bits of `n`, most significant first.
fn scalar_mul(q: &Point, n: &BigUint, p: &BigUint, d2: &BigUint) -> Point {
    let mut acc = Point::identity();
    let bits = n.bits();
    if bits == 0 {
        return acc;
    }
    for i in (0..bits).rev() {
        acc = point_add(&acc, &acc, p, d2);
        if n.bit(i) {
            acc = point_add(&acc, q, p, d2);
        }
    }
    acc
}

/// The base point `B`: `y = 4/5`, `x` the even root (RFC 8032 §5.1).
fn base_point(p: &BigUint) -> Point {
    let y = fmul(&BigUint::from(4u32), &finv(&BigUint::from(5u32), p), p);
    let x = recover_x(&y, false, p).expect("the base point's y has a square root");
    Point { t: fmul(&x, &y, p), x, y, z: one() }
}

/// Recover `x` from `y` and the sign bit, per RFC 8032 §5.1.3. Returns `None`
/// when no square root exists (the encoding names no curve point) or when the
/// encoding is the non-canonical `x = 0` with the sign bit set — two spellings
/// of one point, which SIG-POINTS-CANONICAL refuses.
fn recover_x(y: &BigUint, x_odd: bool, p: &BigUint) -> Option<BigUint> {
    let d = curve_d();
    let y2 = fmul(y, y, p);
    let u = fsub(&y2, &one(), p);
    let v = fadd(&fmul(&d, &y2, p), &one(), p);
    if v == zero() {
        return None;
    }
    // x = (u/v)^((p+3)/8), then corrected by sqrt(-1) if needed.
    let uv = fmul(&u, &finv(&v, p), p);
    let exp = (p + BigUint::from(3u32)) >> 3u32;
    let mut x = uv.modpow(&exp, p);
    if fmul(&x, &x, p) != uv {
        x = fmul(&x, &sqrt_m1(), p);
        if fmul(&x, &x, p) != uv {
            return None;
        }
    }
    if x == zero() && x_odd {
        // x = 0 has one square root; asking for the odd one names nothing.
        return None;
    }
    if x.bit(0) != x_odd {
        x = fsub(p, &x, p);
    }
    Some(x)
}

/// Decode a 32-byte compressed point under SIG-POINTS-CANONICAL.
fn decode_point(bytes: &[u8], p: &BigUint) -> Option<Point> {
    if bytes.len() != 32 {
        return None;
    }
    let mut le = bytes.to_vec();
    let x_odd = le[31] & 0x80 != 0;
    le[31] &= 0x7f;
    let y = BigUint::from_bytes_le(&le);
    // CANONICAL ENCODING: y is read as a 255-bit little-endian integer and MUST
    // be a reduced field element. `y >= p` encodes the same point as `y - p`
    // and is the classic second spelling.
    if y >= *p {
        return None;
    }
    let x = recover_x(&y, x_odd, p)?;
    Some(Point { t: fmul(&x, &y, p), x, y, z: one() })
}

/// Encode a point to its 32-byte compressed form.
fn encode_point(q: &Point, p: &BigUint) -> [u8; 32] {
    let zi = finv(&q.z, p);
    let x = fmul(&q.x, &zi, p);
    let y = fmul(&q.y, &zi, p);
    let mut out = [0u8; 32];
    let le = y.to_bytes_le();
    out[..le.len()].copy_from_slice(&le);
    if x.bit(0) {
        out[31] |= 0x80;
    }
    out
}

// ---------------------------------------------------------------------------
// The §8.6.4a predicate
// ---------------------------------------------------------------------------

/// Why a signature was refused. Returned rather than a bare `false` so a
/// surface can say WHICH of §8.6.4a's rules fired — "invalid signature" and
/// "the key is small-order" are different facts about a publication.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SigError {
    /// The public key is not 32 bytes, or the signature is not 64.
    BadLength,
    /// The public key is not a canonical point encoding (SIG-POINTS-CANONICAL).
    NonCanonicalKey,
    /// `R` is not a canonical point encoding (SIG-POINTS-CANONICAL).
    NonCanonicalR,
    /// `S >= L` (SIG-S-CANONICAL).
    NonCanonicalS,
    /// `A` has order dividing 8 (SIG-SMALL-ORDER).
    SmallOrderKey,
    /// The cofactorless equation does not hold (SIG-COFACTORLESS).
    EquationFailed,
}

impl std::fmt::Display for SigError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let s = match self {
            SigError::BadLength => "key or signature has the wrong length",
            SigError::NonCanonicalKey => "public key is not a canonical point encoding",
            SigError::NonCanonicalR => "signature R is not a canonical point encoding",
            SigError::NonCanonicalS => "signature S is not canonical (S >= L)",
            SigError::SmallOrderKey => "public key is small-order",
            SigError::EquationFailed => "the verification equation does not hold",
        };
        f.write_str(s)
    }
}

/// §8.6.4a verification: `[S]B = R + [k]A`, cofactorless, with the canonicality
/// and small-order rules the section pins.
pub fn verify(public_key: &[u8], message: &[u8], signature: &[u8]) -> Result<(), SigError> {
    if public_key.len() != 32 || signature.len() != 64 {
        return Err(SigError::BadLength);
    }
    let p = p();
    let d2 = fmul(&curve_d(), &BigUint::from(2u32), &p);
    let l = order_l();

    let a = decode_point(public_key, &p).ok_or(SigError::NonCanonicalKey)?;
    // SIG-SMALL-ORDER. A point of order dividing 8 is annihilated by [8]; the
    // check is stated that way rather than by enumerating the eight points,
    // because the enumeration is a list someone wrote down and this is the set
    // the rule names. The identity-point forgery in the conformance vectors is
    // caught here and nowhere else.
    if scalar_mul(&a, &BigUint::from(8u32), &p, &d2).is_identity(&p) {
        return Err(SigError::SmallOrderKey);
    }
    let r = decode_point(&signature[..32], &p).ok_or(SigError::NonCanonicalR)?;
    let s = BigUint::from_bytes_le(&signature[32..]);
    if s >= l {
        return Err(SigError::NonCanonicalS);
    }

    let mut h = Sha512::new();
    h.update(&signature[..32]);
    h.update(public_key);
    h.update(message);
    let k = BigUint::from_bytes_le(&h.finalize()) % &l;

    let b = base_point(&p);
    let lhs = scalar_mul(&b, &s, &p, &d2);
    let rhs = point_add(&r, &scalar_mul(&a, &k, &p, &d2), &p, &d2);
    if lhs.eq_point(&rhs, &p) {
        Ok(())
    } else {
        Err(SigError::EquationFailed)
    }
}

// ---------------------------------------------------------------------------
// Signing — deterministic, per RFC 8032 §5.1.6
// ---------------------------------------------------------------------------

/// The 32-byte public key for a 32-byte seed.
pub fn public_key(seed: &[u8; 32]) -> [u8; 32] {
    let p = p();
    let d2 = fmul(&curve_d(), &BigUint::from(2u32), &p);
    let a = clamped_scalar(seed);
    encode_point(&scalar_mul(&base_point(&p), &a, &p, &d2), &p)
}

fn clamped_scalar(seed: &[u8; 32]) -> BigUint {
    let h = Sha512::digest(seed);
    let mut a = [0u8; 32];
    a.copy_from_slice(&h[..32]);
    a[0] &= 248;
    a[31] &= 127;
    a[31] |= 64;
    BigUint::from_bytes_le(&a)
}

/// Sign `message` with the key derived from `seed`. Deterministic (RFC 8032),
/// which is why §8.6.4a can say conformance vectors pin a seed and not a nonce.
pub fn sign(seed: &[u8; 32], message: &[u8]) -> [u8; 64] {
    let p = p();
    let d2 = fmul(&curve_d(), &BigUint::from(2u32), &p);
    let l = order_l();
    let b = base_point(&p);

    let h = Sha512::digest(seed);
    let a = clamped_scalar(seed);
    let pubkey = encode_point(&scalar_mul(&b, &a, &p, &d2), &p);

    let mut hr = Sha512::new();
    hr.update(&h[32..]);
    hr.update(message);
    let r = BigUint::from_bytes_le(&hr.finalize()) % &l;
    let r_point = encode_point(&scalar_mul(&b, &r, &p, &d2), &p);

    let mut hk = Sha512::new();
    hk.update(r_point);
    hk.update(pubkey);
    hk.update(message);
    let k = BigUint::from_bytes_le(&hk.finalize()) % &l;

    let s = (r + k * a) % &l;
    let mut out = [0u8; 64];
    out[..32].copy_from_slice(&r_point);
    let sle = s.to_bytes_le();
    out[32..32 + sle.len()].copy_from_slice(&sle);
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn hex(bytes: &[u8]) -> String {
        let mut s = String::new();
        for b in bytes {
            s.push(char::from_digit((b >> 4) as u32, 16).unwrap());
            s.push(char::from_digit((b & 0xf) as u32, 16).unwrap());
        }
        s
    }

    fn unhex(s: &str) -> Vec<u8> {
        s.as_bytes()
            .chunks(2)
            .map(|p| u8::from_str_radix(std::str::from_utf8(p).unwrap(), 16).unwrap())
            .collect()
    }

    /// RFC 8032 §7.1 TEST 1: the key derivation this module's signing rests on.
    #[test]
    fn rfc8032_test1_key_derivation() {
        let mut seed = [0u8; 32];
        seed.copy_from_slice(&unhex(
            "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60",
        ));
        assert_eq!(
            hex(&public_key(&seed)),
            "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
        );
    }

    /// Sign-then-verify over messages of several lengths, including the empty
    /// one. What would make this fail: any error in the ladder, the hash
    /// ordering, or the scalar reduction.
    #[test]
    fn signatures_this_module_makes_verify_under_this_module() {
        let seed = [7u8; 32];
        let pk = public_key(&seed);
        for msg in [&b""[..], b"a", b"oath-publish/2\nop=put\n", &[0xffu8; 200][..]] {
            let sig = sign(&seed, msg);
            assert_eq!(verify(&pk, msg, &sig), Ok(()), "own signature must verify");
            // The control: one flipped message byte must break it, or the
            // signature is not binding the message at all.
            let mut tampered = msg.to_vec();
            tampered.push(b'x');
            assert!(verify(&pk, &tampered, &sig).is_err(), "a changed message must fail");
        }
    }

    /// SIG-S-CANONICAL: `S + L` is a second encoding of one signature and MUST
    /// be refused even though the cofactorless equation still holds for it.
    #[test]
    fn s_plus_l_is_refused() {
        let seed = [9u8; 32];
        let pk = public_key(&seed);
        let msg = b"statement";
        let sig = sign(&seed, msg);
        assert_eq!(verify(&pk, msg, &sig), Ok(()));

        let s = BigUint::from_bytes_le(&sig[32..]);
        let malleable = s + order_l();
        let le = malleable.to_bytes_le();
        // Only usable as a 32-byte S; S + L fits for the S values in range here.
        if le.len() <= 32 {
            let mut alt = sig;
            alt[32..].fill(0);
            alt[32..32 + le.len()].copy_from_slice(&le);
            assert_eq!(
                verify(&pk, msg, &alt),
                Err(SigError::NonCanonicalS),
                "S + L must be refused by the canonical-S rule"
            );
        }
    }

    /// SIG-SMALL-ORDER, with the universal forgery it exists to stop: `A` and
    /// `R` the identity point with `S = 0` satisfies `[0]B = R + [k]A` for ANY
    /// message, under no private key at all.
    #[test]
    fn the_identity_point_forgery_is_refused() {
        let mut pk = [0u8; 32];
        pk[0] = 1; // the identity, y = 1
        let sig = {
            let mut s = [0u8; 64];
            s[0] = 1; // R = identity, S = 0
            s
        };
        // The control: without the small-order rule this WOULD pass, so assert
        // the equation really does hold for it before asserting the refusal.
        let p = p();
        let d2 = fmul(&curve_d(), &BigUint::from(2u32), &p);
        let a = decode_point(&pk, &p).expect("the identity is a canonical encoding");
        let r = decode_point(&sig[..32], &p).expect("R decodes");
        let lhs = scalar_mul(&base_point(&p), &zero(), &p, &d2);
        let k = BigUint::from(12345u32);
        let rhs = point_add(&r, &scalar_mul(&a, &k, &p, &d2), &p, &d2);
        assert!(lhs.eq_point(&rhs, &p), "the control: this IS a universal forgery");

        assert_eq!(
            verify(&pk, b"anything at all", &sig),
            Err(SigError::SmallOrderKey)
        );
    }

    /// SIG-SMALL-ORDER again, for a small-order point that is not the identity:
    /// `y = 0` has order 4.
    #[test]
    fn an_order_four_key_is_refused() {
        let pk = [0u8; 32]; // y = 0
        let sig = [0x11u8; 64];
        assert_eq!(verify(&pk, b"m", &sig), Err(SigError::SmallOrderKey));
    }

    /// SIG-POINTS-CANONICAL: `y >= p` is a second spelling of a point.
    #[test]
    fn non_canonical_y_is_refused() {
        // y = p + 1, little-endian, high bit clear.
        let y = p() + one();
        let le = y.to_bytes_le();
        let mut pk = [0u8; 32];
        pk[..le.len().min(32)].copy_from_slice(&le[..le.len().min(32)]);
        pk[31] &= 0x7f;
        assert_eq!(verify(&pk, b"m", &[0u8; 64]), Err(SigError::NonCanonicalKey));
    }

    /// A y with no corresponding x names no point at all.
    #[test]
    fn a_non_point_is_refused() {
        let mut pk = [0u8; 32];
        pk[0] = 2; // y = 2 is not on the curve
        assert_eq!(verify(&pk, b"m", &[0u8; 64]), Err(SigError::NonCanonicalKey));
    }
}
