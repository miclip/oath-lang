//! Base64 in the dialect SPEC §8.6.3 pins (**ENV-B64-DIALECT**,
//! **ENV-B64-CANONICAL**).
//!
//! "Base64" alone admits several spellings of one byte string, and
//! `envelope_b64` is COMPARED and RE-ENCODED (§8.2.1), so the dialect is
//! nailed down: RFC 4648 §4 standard alphabet (`+` and `/`), padding REQUIRED,
//! no line breaks, no whitespace anywhere, no URL-safe substitutions.
//!
//! §8.6.3 further requires a verifier to reject the member unless re-encoding
//! the decoded octets reproduces it exactly. [`decode_canonical`] enforces that
//! directly rather than by a re-encode comparison at every call site: the only
//! way two distinct standard-alphabet strings decode to one byte string is
//! through the unused low bits of the final quantum, so rejecting a non-zero
//! remainder there IS the canonicality rule. The unit tests assert both
//! formulations agree.

const ALPHABET: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/// Encode to the pinned dialect: standard alphabet, padded, unbroken.
pub fn encode(bytes: &[u8]) -> String {
    let mut out = String::with_capacity((bytes.len() + 2) / 3 * 4);
    for chunk in bytes.chunks(3) {
        let b0 = chunk[0] as u32;
        let b1 = *chunk.get(1).unwrap_or(&0) as u32;
        let b2 = *chunk.get(2).unwrap_or(&0) as u32;
        let n = (b0 << 16) | (b1 << 8) | b2;
        out.push(ALPHABET[(n >> 18) as usize & 0x3f] as char);
        out.push(ALPHABET[(n >> 12) as usize & 0x3f] as char);
        if chunk.len() > 1 {
            out.push(ALPHABET[(n >> 6) as usize & 0x3f] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(ALPHABET[n as usize & 0x3f] as char);
        } else {
            out.push('=');
        }
    }
    out
}

fn sextet(c: u8) -> Option<u32> {
    match c {
        b'A'..=b'Z' => Some((c - b'A') as u32),
        b'a'..=b'z' => Some((c - b'a') as u32 + 26),
        b'0'..=b'9' => Some((c - b'0') as u32 + 52),
        b'+' => Some(62),
        b'/' => Some(63),
        _ => None,
    }
}

/// Decode under the pinned dialect, refusing every non-canonical spelling.
///
/// Rejected, each because accepting it would give one byte string two stored
/// representations that compare unequal: any character outside the standard
/// alphabet (so `-`/`_` of the URL-safe alphabet are refused, not translated),
/// whitespace ANYWHERE including a leading or trailing byte, a length that is
/// not a multiple of four, `=` anywhere but the final quantum's tail, and a
/// final quantum whose unused low bits are not zero.
pub fn decode_canonical(text: &str) -> Result<Vec<u8>, String> {
    let b = text.as_bytes();
    if b.is_empty() {
        // The empty string is the encoding of the empty byte string. It is
        // canonical, and an envelope is never empty, so its rejection belongs
        // to the envelope parser rather than here.
        return Ok(Vec::new());
    }
    if b.len() % 4 != 0 {
        return Err(format!(
            "base64 length {} is not a multiple of 4 (padding is required)",
            b.len()
        ));
    }
    // Padding may only be the last one or two characters of the last quantum.
    let pad = b.iter().rev().take_while(|c| **c == b'=').count();
    if pad > 2 {
        return Err("more than two padding characters".into());
    }
    if b[..b.len() - pad].iter().any(|c| *c == b'=') {
        return Err("padding before the end of the input".into());
    }

    let mut out = Vec::with_capacity(b.len() / 4 * 3);
    let data = &b[..b.len() - pad];
    let mut acc: u32 = 0;
    let mut bits = 0u32;
    for (i, c) in data.iter().enumerate() {
        let v = sextet(*c).ok_or_else(|| {
            format!("byte {} is {:?}, not a standard base64 character", i, *c as char)
        })?;
        acc = (acc << 6) | v;
        bits += 6;
        if bits >= 8 {
            bits -= 8;
            out.push((acc >> bits) as u8);
        }
    }
    // ENV-B64-CANONICAL: the leftover bits of the final quantum are not part of
    // any output byte. If they are non-zero, some OTHER spelling decodes to the
    // same octets, and this one is not the canonical representation of them.
    if bits > 0 && (acc & ((1 << bits) - 1)) != 0 {
        return Err("non-canonical final quantum: unused bits are not zero".into());
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip() {
        for n in 0..40usize {
            let bytes: Vec<u8> = (0..n).map(|i| (i * 37 + 11) as u8).collect();
            assert_eq!(decode_canonical(&encode(&bytes)).as_deref(), Ok(&bytes[..]));
        }
    }

    #[test]
    fn rfc4648_test_vectors() {
        for (plain, enc) in [
            ("", ""),
            ("f", "Zg=="),
            ("fo", "Zm8="),
            ("foo", "Zm9v"),
            ("foob", "Zm9vYg=="),
            ("fooba", "Zm9vYmE="),
            ("foobar", "Zm9vYmFy"),
        ] {
            assert_eq!(encode(plain.as_bytes()), enc);
            assert_eq!(decode_canonical(enc).unwrap(), plain.as_bytes());
        }
    }

    /// The dialect's whole point: these all decode to `foo`-ish octets under a
    /// permissive reader, and each is a SECOND stored spelling of one statement.
    #[test]
    fn non_canonical_spellings_are_refused() {
        for bad in [
            "Zm9vYg",      // unpadded
            " Zm9vYg==",   // leading whitespace
            "Zm9vYg== ",   // trailing whitespace
            "Zm9v\nYg==",  // line break
            "Zm9vYg=",     // wrong padding length
            "Zm9vYg===",   // over-padded
            "Zm=9vYg==",   // padding in the middle
            "Zm9vYh==",    // non-zero unused bits (decodes to the same "fob")
            "Zm9-Yg==",    // URL-safe alphabet
        ] {
            assert!(decode_canonical(bad).is_err(), "must refuse {bad:?}");
        }
    }

    /// §8.6.3 states ENV-B64-CANONICAL as a RE-ENCODE test; this module
    /// implements it as an unused-bits test. Those are two formulations, and if
    /// they disagreed on any input the module would be enforcing a rule the
    /// specification does not state. Checked by exhaustion over every
    /// well-formed padded quantum built from a spanning character subset, with
    /// a deliberately permissive reference decoder as the control.
    #[test]
    fn the_unused_bits_rule_is_exactly_the_re_encode_rule() {
        // A subset that spans the sextet space enough to produce both zero and
        // non-zero trailing bits: `A`=0, `B`=1, `P`=15, `/`=63.
        let alpha = ['A', 'B', 'P', '/'];
        // Permissive control: decode ignoring canonicality entirely.
        fn permissive(text: &str) -> Vec<u8> {
            let data: Vec<u8> = text.bytes().filter(|c| *c != b'=').collect();
            let mut out = Vec::new();
            let (mut acc, mut bits) = (0u32, 0u32);
            for c in data {
                acc = (acc << 6) | sextet(c).unwrap();
                bits += 6;
                if bits >= 8 {
                    bits -= 8;
                    out.push((acc >> bits) as u8);
                }
            }
            out
        }
        let mut checked = 0usize;
        for a in alpha {
            for b in alpha {
                for (s, _shape) in [
                    (format!("{a}{b}=="), 1),
                    (format!("{a}{b}{a}="), 2),
                    (format!("{a}{b}{a}{b}"), 3),
                ] {
                    let ours = decode_canonical(&s);
                    let want_ok = encode(&permissive(&s)) == s;
                    assert_eq!(ours.is_ok(), want_ok, "{s:?}: the two formulations disagree");
                    if let Ok(bytes) = ours {
                        assert_eq!(bytes, permissive(&s));
                    }
                    checked += 1;
                }
            }
        }
        assert_eq!(checked, 48, "the exhaustion did not cover what it claims");
    }
}
