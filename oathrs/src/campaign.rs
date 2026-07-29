//! Campaign identity (SPEC §11).
//!
//! A mutation score is only trustworthy if the computation that produced it has
//! a reproducible identity, so the score is attached to the digest of a
//! CAMPAIGN DESCRIPTION and a consumer decides freshness by comparing digests
//! (§11.3: MEASURED / STALE / UNMEASURED).
//!
//! This module implements ONLY the identity — the pure pair
//!
//! ```text
//! campaignEncode : CampaignDescription -> bytes
//! campaignHash   : CampaignDescription -> Hash
//! ```
//!
//! It does not implement a mutation engine, a worker, scheduling or storage:
//! §11 says those are registry concerns and are deliberately unspecified. Like
//! the rest of the library this is pure computation (no fs, no process), so it
//! cross-compiles to wasm unchanged.
//!
//! The split between the two functions is load-bearing. §11.2 states the
//! encoding is CANONICAL BY DEFINITION — it emits exactly one byte
//! representation for every semantically identical description — so ALL
//! normalization lives in `campaign_encode`, and `campaign_hash` is a thin
//! wrapper that hashes the bytes and interprets nothing. If the hash had to
//! re-normalize, an auditor who hashed the encoded bytes directly would get a
//! different answer while believing they had followed §11.

use crate::hash::{sha256_hex, sha256};

/// The domain separator line (§11.2). It is a LINE: LF-terminated like every
/// other, and it sits FIRST, before any field.
pub const CAMPAIGN_DOMAIN: &str = "oath-campaign/1";

/// A campaign description (§11.1). Every field is identity-bearing: each one
/// can change the outcome the score reports.
///
/// Field ORDER in this struct is the encoding order fixed by §11.2 and is not
/// free to change — reordering forks every campaign identity.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CampaignDescription {
    /// Content hash of the ONE object the score is about — 64 lowercase hex.
    pub artifact: String,
    /// Kernel version string: evaluation semantics decide whether a mutant is
    /// caught.
    pub kernel: String,
    /// Mutant-generator revision. A new mutation kind is a different campaign.
    pub engine: String,
    /// Generated cases per mutant. A survivor at 60 may be a kill at 600, so
    /// the budget is part of the claim.
    pub cases: u64,
    /// Evaluation budget per case.
    pub fuel: u64,
    /// How waivers affect the score.
    pub waiver_policy: String,
    /// The waived-mutant SET, keyed by mutant hash (64 lowercase hex each).
    ///
    /// §11.2: order and multiplicity are BOTH non-semantic here, and this is
    /// the ONLY set-valued field in the description. It is stored as a `Vec`
    /// because a description is whatever an auditor reconstructs — possibly
    /// unsorted, possibly repeating — and the encoder is what makes it
    /// canonical. Do not generalize this treatment: an ordered execution
    /// phase or a staged mutation sequence would be a SEQUENCE, and sorting or
    /// deduplicating it would erase meaning rather than canonicalize it.
    pub waivers: Vec<String>,
}

/// `campaignEncode` (§11.2) — the canonical byte encoding.
///
/// Domain separator line, then one `key=value` line per field in exactly the
/// §11.2 order, each terminated by a single LF (0x0A). No spaces around `=`, no
/// trailing blank line, UTF-8 throughout.
///
/// This function is TOTAL, matching the signature §11 states
/// (`CampaignDescription -> bytes`): §11 gives `campaignEncode` no error path.
/// Shape checking is available separately as [`CampaignDescription::validate`]
/// and is deliberately not on the encoding path.
pub fn campaign_encode(d: &CampaignDescription) -> Vec<u8> {
    let mut out = String::new();

    out.push_str(CAMPAIGN_DOMAIN);
    out.push('\n');

    // The field order below is normative (§11.2).
    push_field(&mut out, "artifact", &d.artifact);
    push_field(&mut out, "kernel", &d.kernel);
    push_field(&mut out, "engine", &d.engine);
    push_field(&mut out, "cases", &d.cases.to_string());
    push_field(&mut out, "fuel", &d.fuel.to_string());
    push_field(&mut out, "waiver-policy", &d.waiver_policy);
    // §11.2: when there are no waivers the value is EMPTY and the line is
    // STILL PRESENT — omitting it would make "no waivers" and "field absent"
    // the same bytes.
    push_field(&mut out, "waivers", &canonical_waivers(&d.waivers).join(","));

    out.into_bytes()
}

/// `campaignHash` (§11.3) — `SHA-256(campaignEncode(d))`, lowercase hex.
///
/// Deliberately a thin wrapper: it hashes the encoded bytes and interprets
/// nothing. Every normalization the identity depends on already happened in
/// [`campaign_encode`], so hashing that function's output by hand yields the
/// same digest.
pub fn campaign_hash(d: &CampaignDescription) -> String {
    sha256_hex(&campaign_encode(d))
}

/// The raw 32-byte digest, for callers that want bytes rather than the §11
/// lowercase-hex rendering.
pub fn campaign_hash_bytes(d: &CampaignDescription) -> [u8; 32] {
    sha256(&campaign_encode(d))
}

/// One `key=value` line: no spaces around `=`, single LF terminator.
fn push_field(out: &mut String, key: &str, value: &str) {
    out.push_str(key);
    out.push('=');
    out.push_str(value);
    out.push('\n');
}

/// Canonicalize the `waivers` SET (§11.2), in the order the spec states:
/// collapse duplicates, THEN sort ascending as byte strings.
///
/// Duplicates COLLAPSE rather than being rejected — a description naming the
/// same waiver twice is meaningless, not invalid. (The two steps commute, but
/// they are written in the specified order rather than fused into a sorted set
/// so the code reads as §11.2 does.)
fn canonical_waivers(waivers: &[String]) -> Vec<&str> {
    let mut uniq: Vec<&str> = Vec::with_capacity(waivers.len());
    for w in waivers {
        if !uniq.iter().any(|seen| *seen == w.as_str()) {
            uniq.push(w.as_str());
        }
    }
    // "ASCENDING as byte strings" — compare the UTF-8 bytes, not chars and not
    // any decoded numeric value.
    uniq.sort_by(|a, b| a.as_bytes().cmp(b.as_bytes()));
    uniq
}

impl CampaignDescription {
    /// Shape check for the §11.2 grammar, kept OFF the encoding path.
    ///
    /// §11.2 writes `artifact=<64 lowercase hex>` and keys waivers by mutant
    /// hash, but gives `campaignEncode` no error path, so this is offered as a
    /// separate obligation a caller may discharge. It also rejects LF and CR in
    /// the free-form values (`kernel`, `engine`, `waiver-policy`): §11.2
    /// justifies line framing by noting a value containing `=` cannot shift the
    /// parse, but says nothing about a value containing the line terminator
    /// itself — see DIVERGENCES.md.
    pub fn validate(&self) -> Result<(), String> {
        if !is_hash_hex(&self.artifact) {
            return Err(format!(
                "artifact must be 64 lowercase hex digits, got {:?}",
                self.artifact
            ));
        }
        for w in &self.waivers {
            if !is_hash_hex(w) {
                return Err(format!(
                    "waiver must be 64 lowercase hex digits, got {:?}",
                    w
                ));
            }
        }
        for (name, v) in [
            ("kernel", &self.kernel),
            ("engine", &self.engine),
            ("waiver-policy", &self.waiver_policy),
        ] {
            if v.contains('\n') || v.contains('\r') {
                return Err(format!("{} must not contain a line terminator", name));
            }
        }
        Ok(())
    }
}

fn is_hash_hex(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    /// The pinned vectors (§11.4). `include_str!` rather than `std::fs` so the
    /// test module stays as host-free as the library.
    const VECTORS: &str = include_str!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../fixtures/campaign/vectors.txt"
    ));

    /// A vector: the canonical encoding as it appears in the fixture, and the
    /// digest recorded beneath it.
    struct Vector {
        desc: CampaignDescription,
        encoded: String,
        digest: String,
    }

    /// Parse the fixture into vectors. Each block is the canonical encoding
    /// followed by a `digest=` line that is NOT part of the encoding; blocks
    /// are blank-line separated and `#` lines are comments.
    fn vectors() -> Vec<Vector> {
        let mut out = Vec::new();
        let mut block: Vec<&str> = Vec::new();
        let mut flush = |block: &mut Vec<&str>| {
            if !block.is_empty() {
                out.push(parse_block(block));
                block.clear();
            }
        };
        for line in VECTORS.lines() {
            if line.starts_with('#') {
                continue;
            }
            if line.is_empty() {
                flush(&mut block);
            } else {
                block.push(line);
            }
        }
        flush(&mut block);
        out
    }

    fn parse_block(block: &[&str]) -> Vector {
        assert_eq!(block[0], CAMPAIGN_DOMAIN, "block must open with the domain separator");
        let mut fields: Vec<(&str, &str)> = Vec::new();
        for line in &block[1..] {
            // Split on the FIRST '=' — §11.2 promises a value containing '='
            // cannot shift the parse.
            let (k, v) = line.split_once('=').expect("field line must contain '='");
            fields.push((k, v));
        }
        let get = |k: &str| -> &str {
            fields
                .iter()
                .find(|(fk, _)| *fk == k)
                .unwrap_or_else(|| panic!("missing field {}", k))
                .1
        };
        let waivers_raw = get("waivers");
        let waivers: Vec<String> = if waivers_raw.is_empty() {
            Vec::new()
        } else {
            waivers_raw.split(',').map(str::to_string).collect()
        };
        let desc = CampaignDescription {
            artifact: get("artifact").to_string(),
            kernel: get("kernel").to_string(),
            engine: get("engine").to_string(),
            cases: get("cases").parse().expect("cases must be decimal"),
            fuel: get("fuel").parse().expect("fuel must be decimal"),
            waiver_policy: get("waiver-policy").to_string(),
            waivers,
        };
        // The encoding is every line of the block except the trailing digest,
        // each LF-terminated, with no trailing blank line.
        let encoded: String = block
            .iter()
            .filter(|l| !l.starts_with("digest="))
            .map(|l| format!("{}\n", l))
            .collect();
        Vector {
            desc,
            encoded,
            digest: get("digest").to_string(),
        }
    }

    /// A description with plausible non-identity-bearing values, so a test can
    /// vary exactly one field.
    fn base() -> CampaignDescription {
        CampaignDescription {
            artifact: "ab".repeat(32),
            kernel: "oath-kernel/0.7".into(),
            engine: "mutants-1".into(),
            cases: 60,
            fuel: 500_000,
            waiver_policy: "waived-count-as-killed".into(),
            waivers: Vec::new(),
        }
    }

    fn enc(d: &CampaignDescription) -> String {
        String::from_utf8(campaign_encode(d)).expect("encoding is UTF-8")
    }

    // -- the fixtures (§11.4) ------------------------------------------------

    #[test]
    fn every_vector_reproduces_encoding_and_digest() {
        let vs = vectors();
        assert!(vs.len() >= 7, "expected the pinned vector set, got {}", vs.len());
        for (i, v) in vs.iter().enumerate() {
            assert_eq!(
                enc(&v.desc),
                v.encoded,
                "vector {}: canonical encoding differs byte-for-byte",
                i
            );
            assert_eq!(
                campaign_hash(&v.desc),
                v.digest,
                "vector {}: digest differs",
                i
            );
        }
    }

    #[test]
    fn hash_is_a_thin_wrapper_over_the_encoding() {
        // §11.2: hash the bytes, interpret nothing. An auditor who hashes
        // campaignEncode's output by hand must get the recorded digest.
        for v in vectors() {
            assert_eq!(campaign_hash(&v.desc), sha256_hex(v.encoded.as_bytes()));
        }
    }

    // -- the three canonicality claims, at the ENCODING level -----------------

    #[test]
    fn set_valued_field_is_order_independent() {
        let a = "11".repeat(32);
        let b = "22".repeat(32);
        let c = "33".repeat(32);

        let mut one = base();
        one.waivers = vec![a.clone(), b.clone(), c.clone()];
        let mut other = base();
        other.waivers = vec![c.clone(), a.clone(), b.clone()];
        let mut reversed = base();
        reversed.waivers = vec![c, b.clone(), a.clone()];

        assert_eq!(enc(&one), enc(&other), "recording order must not reach the bytes");
        assert_eq!(enc(&one), enc(&reversed));
        assert_eq!(campaign_hash(&one), campaign_hash(&reversed));

        // ...because the encoder SORTS ascending as byte strings.
        assert!(enc(&one).contains(&format!("waivers={},{},{}\n", a, b, "33".repeat(32))));
    }

    #[test]
    fn duplicates_collapse_rather_than_being_rejected() {
        let a = "11".repeat(32);
        let b = "22".repeat(32);

        let mut once = base();
        once.waivers = vec![a.clone()];
        let mut twice = base();
        twice.waivers = vec![a.clone(), a.clone()];
        let mut thrice_interleaved = base();
        thrice_interleaved.waivers = vec![b.clone(), a.clone(), b.clone(), a.clone(), b.clone()];

        assert_eq!(enc(&once), enc(&twice), "a repeat carries no information");
        assert_eq!(campaign_hash(&once), campaign_hash(&twice));
        assert!(enc(&once).contains(&format!("waivers={}\n", a)));

        let mut both = base();
        both.waivers = vec![a.clone(), b.clone()];
        assert_eq!(enc(&thrice_interleaved), enc(&both));
    }

    #[test]
    fn set_membership_is_sensitive() {
        let mut one = base();
        one.waivers = vec!["11".repeat(32)];
        let mut other = base();
        other.waivers = vec!["33".repeat(32)];
        assert_ne!(enc(&one), enc(&other), "changing a member must change the bytes");
        assert_ne!(campaign_hash(&one), campaign_hash(&other));

        // Adding a member changes it too — the drift §11 exists to expose.
        let mut grown = one.clone();
        grown.waivers.push("22".repeat(32));
        assert_ne!(enc(&one), enc(&grown));
        assert_ne!(campaign_hash(&one), campaign_hash(&grown));
    }

    #[test]
    fn empty_set_still_emits_the_line() {
        let d = base();
        let e = enc(&d);
        assert!(
            e.ends_with("waivers=\n"),
            "no waivers is `waivers=` + LF, never an omitted line: {:?}",
            e
        );
        // "no waivers" and "field absent" must not be the same bytes.
        let absent: String = e.lines().filter(|l| !l.starts_with("waivers"))
            .map(|l| format!("{}\n", l)).collect();
        assert_ne!(e, absent);
        assert_ne!(sha256_hex(e.as_bytes()), sha256_hex(absent.as_bytes()));

        // An empty vec and a vec of nothing-but-duplicates-of-nothing agree;
        // an empty value is distinct from a single member.
        let mut nonempty = base();
        nonempty.waivers = vec!["00".repeat(32)];
        assert_ne!(enc(&d), enc(&nonempty));
    }

    // -- framing and the remaining fields -------------------------------------

    #[test]
    fn framing_is_domain_line_then_seven_fields_no_trailing_blank() {
        let e = enc(&base());
        assert!(e.ends_with('\n'));
        assert!(!e.ends_with("\n\n"), "no trailing blank line");
        assert!(!e.contains('\r'), "LF only, never CRLF");
        let lines: Vec<&str> = e.trim_end_matches('\n').split('\n').collect();
        assert_eq!(lines.len(), 8);
        assert_eq!(lines[0], "oath-campaign/1");
        let keys: Vec<&str> = lines[1..]
            .iter()
            .map(|l| l.split_once('=').unwrap().0)
            .collect();
        assert_eq!(
            keys,
            vec![
                "artifact",
                "kernel",
                "engine",
                "cases",
                "fuel",
                "waiver-policy",
                "waivers"
            ]
        );
    }

    #[test]
    fn every_field_is_identity_bearing() {
        let b = base();
        let h = campaign_hash(&b);
        let mut variants = Vec::new();

        let mut v = b.clone();
        v.artifact = "cd".repeat(32);
        variants.push(v);
        let mut v = b.clone();
        v.kernel = "oath-kernel/0.8".into();
        variants.push(v);
        let mut v = b.clone();
        v.engine = "mutants-2".into();
        variants.push(v);
        let mut v = b.clone();
        v.cases = 600;
        variants.push(v);
        let mut v = b.clone();
        v.fuel = 500_001;
        variants.push(v);
        let mut v = b.clone();
        v.waiver_policy = "waived-count-as-survived".into();
        variants.push(v);
        let mut v = b.clone();
        v.waivers = vec!["11".repeat(32)];
        variants.push(v);

        for v in &variants {
            assert_ne!(campaign_hash(v), h, "field change did not move the digest: {:?}", v);
        }
    }

    #[test]
    fn integers_are_bare_decimal() {
        let mut d = base();
        d.cases = 0;
        d.fuel = 1;
        assert!(enc(&d).contains("cases=0\n"));
        assert!(enc(&d).contains("fuel=1\n"));
        d.cases = u64::MAX;
        assert!(enc(&d).contains("cases=18446744073709551615\n"));
    }

    #[test]
    fn validate_is_off_the_encoding_path() {
        // encode is total (§11's signature has no error case) even for a
        // description validate rejects.
        let mut d = base();
        d.artifact = "not-a-hash".into();
        assert!(d.validate().is_err());
        assert!(enc(&d).contains("artifact=not-a-hash\n"));
        assert_eq!(base().validate(), Ok(()));

        let mut lf = base();
        lf.kernel = "oath-kernel/0.7\nengine=evil".into();
        assert!(lf.validate().is_err(), "a value with an LF forges a field");
    }
}
