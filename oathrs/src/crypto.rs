//! The trusted crypto boundary (SPEC §1.3, "Crypto over byte lists", #78).
//!
//! Two primitives operate on BYTE LISTS — `(List Int)` with every element in
//! `0..255` — rather than on scalars:
//!
//!   * `hmac-sha256 key msg : (List Int)` — RFC 2104 keyed hashing with
//!     SHA-256, returning the 32-byte digest.
//!   * `bytes-eq-ct a b : Bool` — equality that examines EVERY byte, so it
//!     does not leak the position of the first difference through timing.
//!
//! Both are deliberately OUTSIDE the provable fragment (SPEC §7): a property
//! whose goal reaches either is recorded `tested`, never `proven`. That is
//! enforced in `prove.rs`, not here.
//!
//! Implemented by hand on top of the SHA-256 already used for object identity,
//! so no HMAC crate is pulled in and the wasm build keeps its dependency
//! surface (pure computation only — see lib.rs).

use crate::hash::sha256;

/// SHA-256's block size in bytes (RFC 2104's `B`).
const BLOCK: usize = 64;

/// HMAC-SHA256 (RFC 2104 with SHA-256): `H((K ⊕ opad) ‖ H((K ⊕ ipad) ‖ msg))`,
/// where `K` is `key` hashed down if longer than one block and zero-padded up
/// to one block otherwise. Returns the 32-byte digest.
pub fn hmac_sha256(key: &[u8], msg: &[u8]) -> Vec<u8> {
    // RFC 2104 §2: keys longer than the block size are replaced by their own
    // digest; shorter keys are right-padded with zeros to the block size.
    let mut k = [0u8; BLOCK];
    if key.len() > BLOCK {
        k[..32].copy_from_slice(&sha256(key));
    } else {
        k[..key.len()].copy_from_slice(key);
    }

    let mut inner = Vec::with_capacity(BLOCK + msg.len());
    for b in k.iter() {
        inner.push(b ^ 0x36); // ipad
    }
    inner.extend_from_slice(msg);
    let inner_digest = sha256(&inner);

    let mut outer = Vec::with_capacity(BLOCK + 32);
    for b in k.iter() {
        outer.push(b ^ 0x5c); // opad
    }
    outer.extend_from_slice(&inner_digest);
    sha256(&outer).to_vec()
}

/// Constant-time byte-list equality (SPEC §1.3). Every byte of the longer
/// operand is examined regardless of where the two first differ — there is no
/// early exit — so the running time is a function of the LENGTHS only, which
/// are not the secret. Differing lengths compare unequal; the missing tail is
/// folded in as zero bytes purely to keep the loop shape uniform, and the
/// length mismatch is what decides the result.
pub fn bytes_eq_ct(a: &[u8], b: &[u8]) -> bool {
    let n = if a.len() > b.len() { a.len() } else { b.len() };
    let mut diff: u8 = 0;
    for i in 0..n {
        let x = if i < a.len() { a[i] } else { 0 };
        let y = if i < b.len() { b[i] } else { 0 };
        diff |= x ^ y;
    }
    // `&`, not `&&`: no short-circuit, so neither operand's evaluation is
    // skipped on the basis of the other.
    (diff == 0) & (a.len() == b.len())
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
            .map(|p| {
                u8::from_str_radix(std::str::from_utf8(p).unwrap(), 16).unwrap()
            })
            .collect()
    }

    // RFC 4231 test vectors for HMAC-SHA-256.
    #[test]
    fn rfc4231_case1() {
        let key = vec![0x0bu8; 20];
        assert_eq!(
            hex(&hmac_sha256(&key, b"Hi There")),
            "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
        );
    }

    #[test]
    fn rfc4231_case2() {
        assert_eq!(
            hex(&hmac_sha256(b"Jefe", b"what do ya want for nothing?")),
            "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
        );
    }

    #[test]
    fn rfc4231_case3() {
        let key = vec![0xaau8; 20];
        let msg = vec![0xddu8; 50];
        assert_eq!(
            hex(&hmac_sha256(&key, &msg)),
            "773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe"
        );
    }

    #[test]
    fn rfc4231_case4() {
        let key = unhex("0102030405060708090a0b0c0d0e0f10111213141516171819");
        let msg = vec![0xcdu8; 50];
        assert_eq!(
            hex(&hmac_sha256(&key, &msg)),
            "82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b"
        );
    }

    // Case 6: key longer than one block (131 bytes) — exercises the key-hashing
    // branch of RFC 2104 §2.
    #[test]
    fn rfc4231_case6_long_key() {
        let key = vec![0xaau8; 131];
        assert_eq!(
            hex(&hmac_sha256(
                &key,
                b"Test Using Larger Than Block-Size Key - Hash Key First"
            )),
            "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54"
        );
    }

    #[test]
    fn rfc4231_case7_long_key_and_msg() {
        let key = vec![0xaau8; 131];
        let msg: &[u8] = b"This is a test using a larger than block-size key and a larger than block-size data. The key needs to be hashed before being used by the HMAC algorithm.";
        assert_eq!(
            hex(&hmac_sha256(&key, msg)),
            "9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2"
        );
    }

    #[test]
    fn empty_key_and_message() {
        assert_eq!(
            hex(&hmac_sha256(b"", b"")),
            "b613679a0814d9ec772f95d778c35fc5ff1697c493715653c6c712144292c5ad"
        );
    }

    #[test]
    fn ct_equality_agrees_with_plain_equality() {
        let cases: &[(&[u8], &[u8])] = &[
            (b"", b""),
            (b"", b"a"),
            (b"a", b""),
            (b"abc", b"abc"),
            (b"abc", b"abd"),
            (b"abc", b"bbc"),
            (b"abc", b"abcd"),
            (b"\x00", b""),
        ];
        for (a, b) in cases {
            assert_eq!(bytes_eq_ct(a, b), a == b, "{:?} vs {:?}", a, b);
        }
    }
}
