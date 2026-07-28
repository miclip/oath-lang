//! SHA-256, lowercase hex (SPEC §1).

use sha2::{Digest, Sha256};

/// Raw SHA-256 digest (32 bytes). Shared by object identity (§1) and the
/// `hmac-sha256` primitive (§1.3), which is RFC 2104 over this compression.
pub fn sha256(bytes: &[u8]) -> [u8; 32] {
    let mut h = Sha256::new();
    h.update(bytes);
    let digest = h.finalize();
    let mut out = [0u8; 32];
    out.copy_from_slice(&digest);
    out
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    let digest = sha256(bytes);
    let mut s = String::with_capacity(64);
    for b in digest.iter() {
        s.push(char::from_digit((b >> 4) as u32, 16).unwrap());
        s.push(char::from_digit((b & 0xf) as u32, 16).unwrap());
    }
    s
}
