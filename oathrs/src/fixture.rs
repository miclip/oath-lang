//! Fixture filenames (SPEC §10.0a, CONF-FIXTURE-FILENAME).
//!
//! A fixture derived from a definition NAME is stored under an ENCODED
//! filename. The encoding is per character, in order:
//!
//! | input                     | output                                    |
//! |---------------------------|-------------------------------------------|
//! | `_`                       | `__`                                      |
//! | an uppercase letter `X`   | `_x` (underscore, then the lowercase one) |
//! | anything else             | unchanged                                 |
//!
//! So `Map` becomes `_map`, `map` stays `map`, `_map` becomes `__map`.
//!
//! WHY: the image contains no uppercase letter at all, so two names that differ
//! only by case cannot collide on a case-insensitive filesystem. Before the
//! rule they did — `map` and `Map` folded onto one inode and one definition's
//! canonical bytes were absent from the tree entirely, and the same generator
//! emitted a different file count on Linux and on macOS.
//!
//! SPEC INTERPRETATION (recorded because §10.0a does not say):
//!
//!  * "an uppercase letter" is taken as ASCII `A`..=`Z`, lowercased by `+0x20`.
//!    Full Unicode case mapping would falsify the injectivity the section
//!    claims (U+212A KELVIN SIGN and `K` both lowercase to `k`) and is not
//!    always single-character (U+0130 lowercases to two codepoints), so the
//!    only reading under which the section's own claim holds is the ASCII one.
//!  * The suffix (`.bin`, `.json`, `.txt`) is appended AFTER encoding. §10.0a
//!    does not say which side of the encoding it falls on; here it makes no
//!    difference, since every character of every suffix is a fixed point.
//!  * "anything else unchanged" is applied literally, including characters a
//!    filesystem would object to (`/`, `.`, NUL). Names are lexer symbols
//!    (§1.4) with no stated character class, so such a name is well formed and
//!    would escape its directory; inventing extra escaping here would diverge
//!    from every other kernel, so the rule is implemented exactly as written.

/// §10.0a: encode a definition name for use as a fixture filename stem.
pub fn encode_name(name: &str) -> String {
    let mut out = String::with_capacity(name.len() + 8);
    for c in name.chars() {
        if c == '_' {
            out.push('_');
            out.push('_');
        } else if c.is_ascii_uppercase() {
            out.push('_');
            out.push(c.to_ascii_lowercase());
        } else {
            out.push(c);
        }
    }
    out
}

/// §10.0a inverse: recover a definition name from its fixture filename stem.
///
/// The section says "the encoding is a bijection, so a name is recoverable
/// from its filename". Recoverable it is — the encoding is INJECTIVE and this
/// decode is its left inverse. It is NOT a bijection onto strings, so this
/// function is partial and returns `None` on the strings outside the image:
/// anything containing an uppercase letter (never emitted), and `_` followed
/// by anything that is neither `_` nor an ASCII lowercase letter (including a
/// trailing lone `_`).
pub fn decode_name(file: &str) -> Option<String> {
    let cs: Vec<char> = file.chars().collect();
    let mut out = String::with_capacity(cs.len());
    let mut i = 0;
    while i < cs.len() {
        let c = cs[i];
        if c == '_' {
            let n = *cs.get(i + 1)?;
            if n == '_' {
                out.push('_');
            } else if n.is_ascii_lowercase() {
                out.push(n.to_ascii_uppercase());
            } else {
                return None;
            }
            i += 2;
        } else if c.is_ascii_uppercase() {
            return None;
        } else {
            out.push(c);
            i += 1;
        }
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn spec_worked_examples() {
        // The three §10.0a states verbatim.
        assert_eq!(encode_name("Map"), "_map");
        assert_eq!(encode_name("map"), "map");
        assert_eq!(encode_name("_map"), "__map");
    }

    #[test]
    fn case_collision_is_impossible() {
        // The property the rule exists for: no encoded name contains an
        // uppercase letter, so no two encodings can differ only by case.
        for n in ["Map", "map", "mAp", "MAP", "_Map", "set-of_Map"] {
            assert!(!encode_name(n).chars().any(|c| c.is_ascii_uppercase()));
        }
        assert_ne!(encode_name("Map"), encode_name("map"));
    }

    #[test]
    fn round_trip() {
        for n in [
            "map", "Map", "_map", "__", "_", "", "list->set", "Set-of-Int",
            "x_1", "MAP", "int/rat", "naïve-Élan",
        ] {
            assert_eq!(decode_name(&encode_name(n)).as_deref(), Some(n));
        }
    }

    #[test]
    fn outside_the_image() {
        // Not a bijection onto strings; these have no preimage.
        assert_eq!(decode_name("Map"), None); // uppercase never emitted
        assert_eq!(decode_name("_1"), None); // `_` before a non-letter
        assert_eq!(decode_name("m_"), None); // dangling escape
    }

    #[test]
    fn non_ascii_is_left_alone() {
        // "an uppercase letter" is read as ASCII (see the module note): a
        // Unicode uppercase letter is "anything else" and passes through.
        assert_eq!(encode_name("Élan"), "Élan");
    }
}
