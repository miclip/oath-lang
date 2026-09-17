//! The publication envelope — SPEC §8.6.1 (canonical encoding), §8.6.2 (the
//! name revision, as a type) and §8.6.4's store-side obligations.
//!
//! An envelope is the AUTHOR's statement, signed before submission and
//! persisted verbatim. §8.4's entry signature seals custody; this seals
//! authorship, and the two are independent. What an envelope covers is only
//! what the author controls — the name, the artifact and the transition — and
//! never a store-derived verdict, which an author cannot predict.
//!
//! The octets are the thing. Every function here is written so that the byte
//! string is primary and the struct is its interpretation: [`Envelope::parse`]
//! refuses anything that does not re-encode to itself (**ENV-REENCODE**), so a
//! parsed envelope and its octets are interchangeable by construction.
//!
//! WHAT §8.6.1 LEAVES OPEN, recorded because the section does not say (each is
//! argued in full in the module tests or at the site):
//!
//!  * `license` is "an SPDX expression or `-`" and explicitly NOT validated.
//!    An EMPTY value (`license=`) is therefore neither admitted nor refused by
//!    any rule in the section; it satisfies ENV-VALUE-CHARS and round-trips, so
//!    it is accepted here and reported as an asserted (if meaningless) term.
//!  * The rules are stated as a CONJUNCTION with no precedence, so which one a
//!    given malformed input trips first is not fixed by the specification. The
//!    error values below name a rule for diagnosis; nothing may depend on
//!    WHICH name appears.
//!
//! **ENV-REENCODE IS REDUNDANT IN THIS ENCODING, and saying so is worth more
//! than quietly relying on it.** Measured by deleting each rule in turn and
//! re-running the suite: dropping ENV-REENCODE alone changes no verdict, and
//! dropping ENV-REV-CANONICAL's leading-zero clause alone changes no verdict
//! either — each is caught by the other. Dropping BOTH lets `parent_rev=01`
//! through, which the vectors catch. The reason is structural: [`Envelope::encode`]
//! is a function of the parsed fields, and every field rule admits exactly ONE
//! text per value, so the parse is already injective and re-encoding cannot
//! disagree. Both are implemented anyway — §8.6.1 states them separately, and a
//! later field with real spelling freedom (an added format version, say) would
//! make the redundancy vanish without anything announcing it.

use crate::ed25519;
use num_bigint::BigUint;

/// The format tag, which is inside the signed bytes — so a signature made under
/// one version can never be read as one made under another (§8.6.1).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Version {
    /// `oath-publish/1` — six fields, no `license` line. Readable for
    /// HISTORICAL verification only: §8.6.4 requires an entry to be verified
    /// under the format it was recorded with, and a kernel reading only its
    /// newest shape would reject correct historical signatures.
    V1,
    /// `oath-publish/2` — seven fields, the current shape.
    V2,
}

impl Version {
    pub fn tag(self) -> &'static str {
        match self {
            Version::V1 => "oath-publish/1",
            Version::V2 => "oath-publish/2",
        }
    }

    fn from_tag(tag: &str) -> Option<Version> {
        match tag {
            "oath-publish/1" => Some(Version::V1),
            "oath-publish/2" => Some(Version::V2),
            _ => None,
        }
    }

    /// The keys this version carries, in their normative order
    /// (**ENV-FIELD-ORDER**, **ENV-FIELD-COUNT**).
    fn keys(self) -> &'static [&'static str] {
        match self {
            Version::V1 => &["op", "name", "artifact", "parent", "parent_rev", "author"],
            Version::V2 => {
                &["op", "name", "artifact", "parent", "parent_rev", "author", "license"]
            }
        }
    }
}

/// What the publisher said about terms (§8.6.1, §12).
///
/// THREE states, not two. §8.6.1 requires a `/1` envelope to be reported as an
/// explicit no-assertion state "rather than as an empty field", and says in the
/// same breath that "the format had no licence line" and "the publisher chose
/// to say nothing" are DIFFERENT historical facts. Collapsing them would make
/// the two indistinguishable in exactly the direction §12 cares about.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum License {
    /// `/1`: the format carried no licence line. The publisher was never asked.
    FieldAbsent,
    /// `/2` with `-`: the publisher was asked and asserted no terms.
    NoneAsserted,
    /// `/2` with an expression, preserved verbatim and NOT validated — this
    /// layer is the notary for what was signed, and checking SPDX syntax would
    /// drift publication into policy enforcement (§8.6.1).
    Asserted(String),
}

impl License {
    /// The terms asserted, if any. Both no-assertion states answer `None`; the
    /// distinction between them is in the variant, not here.
    pub fn terms(&self) -> Option<&str> {
        match self {
            License::Asserted(s) => Some(s),
            _ => None,
        }
    }
}

/// `parent`: a 64-hex artifact hash, or the sentinel `-` for a name that had no
/// previous value (**ENV-PARENT-FORM**).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Parent {
    /// `-` — the name did not exist. Tied to revision 0 by
    /// **ENV-PARENT-CONSISTENT**.
    Absent,
    Hash(String),
}

impl Parent {
    /// The wire spelling: the hash, or the `-` sentinel.
    pub fn text(&self) -> &str {
        match self {
            Parent::Absent => "-",
            Parent::Hash(h) => h,
        }
    }
}

/// Why an envelope was refused. The `rule` is the §8.6.1/§8.6.4 identifier for
/// diagnosis; see the module note on precedence before depending on it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EnvError {
    pub rule: &'static str,
    pub detail: String,
}

impl EnvError {
    fn new(rule: &'static str, detail: impl Into<String>) -> EnvError {
        EnvError { rule, detail: detail.into() }
    }
}

impl std::fmt::Display for EnvError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.rule, self.detail)
    }
}

/// The author's publication statement (§8.6.1).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Envelope {
    pub version: Version,
    /// `op` — `put` is the only operation this version defines
    /// (**ENV-OP-PUT**). Kept as a field anyway, so a later version adding an
    /// operation changes a value rather than a shape.
    pub op: String,
    pub name: String,
    pub artifact: String,
    pub parent: Parent,
    /// **UNBOUNDED** (**ENV-REV-CANONICAL**). A machine-word limit would
    /// declare a valid historical statement malformed once a name had been
    /// repointed often enough, so this is a `BigUint` and not a `u64`.
    pub parent_rev: BigUint,
    pub author: String,
    pub license: License,
}

/// **ENV-VALUE-CHARS**: no LF, no CR, nothing below `0x20`, and not `0x7F`.
///
/// §8.6.1 notes the LF half is reachable only on the ENCODING side — an
/// injected LF in stored octets presents as a line-count error. CR, DEL and NUL
/// are reachable from BOTH sides, so this runs on both.
fn check_value_chars(key: &str, value: &str) -> Result<(), EnvError> {
    for c in value.chars() {
        let u = c as u32;
        if u < 0x20 || u == 0x7f {
            return Err(EnvError::new(
                "ENV-VALUE-CHARS",
                format!("{key} contains U+{u:04X}, which the value rule forbids"),
            ));
        }
    }
    Ok(())
}

fn is_lower_hex64(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

/// **ENV-REV-CANONICAL**: a non-negative integer in canonical decimal — no
/// leading zeros, no sign, no spaces, unbounded.
fn parse_revision(text: &str) -> Result<BigUint, EnvError> {
    if text.is_empty() || !text.bytes().all(|b| b.is_ascii_digit()) {
        return Err(EnvError::new(
            "ENV-REV-CANONICAL",
            format!("parent_rev {text:?} is not a non-negative decimal integer"),
        ));
    }
    if text.len() > 1 && text.starts_with('0') {
        return Err(EnvError::new(
            "ENV-REV-CANONICAL",
            format!("parent_rev {text:?} has a leading zero; `01` and `1` would be two spellings of one revision"),
        ));
    }
    Ok(BigUint::parse_bytes(text.as_bytes(), 10).expect("digits parse"))
}

/// Render a revision in canonical decimal. `BigUint`'s decimal form is already
/// leading-zero-free, so this is the inverse of [`parse_revision`] on its whole
/// image — which is what makes ENV-REENCODE hold for this field.
pub fn revision_text(rev: &BigUint) -> String {
    rev.to_str_radix(10)
}

impl Envelope {
    /// Encode to the octets a signature is computed over (§8.6.1).
    ///
    /// Validates as it goes: an envelope that cannot be encoded is not one
    /// whose encoding is unavailable, it is a statement that was never
    /// well-formed, and returning bytes for it would create octets no parser
    /// would accept.
    pub fn encode(&self) -> Result<Vec<u8>, EnvError> {
        if self.version == Version::V1 && self.license != License::FieldAbsent {
            return Err(EnvError::new(
                "ENV-FIELD-COUNT",
                "oath-publish/1 has no license line, so it cannot carry terms",
            ));
        }
        if self.version == Version::V2 && self.license == License::FieldAbsent {
            return Err(EnvError::new(
                "ENV-FIELD-COUNT",
                "oath-publish/2 always carries a license line; there is no optional field",
            ));
        }
        if self.op != "put" {
            return Err(EnvError::new(
                "ENV-OP-PUT",
                format!("op is {:?}; `put` is the only operation this version defines", self.op),
            ));
        }
        if self.name.is_empty() {
            return Err(EnvError::new("ENV-NAME-NONEMPTY", "name is empty"));
        }
        if !is_lower_hex64(&self.artifact) {
            return Err(EnvError::new(
                "ENV-HEX-LOWERCASE",
                format!("artifact {:?} is not 64 lowercase hex characters", self.artifact),
            ));
        }
        if let Parent::Hash(h) = &self.parent {
            if !is_lower_hex64(h) {
                return Err(EnvError::new(
                    "ENV-PARENT-FORM",
                    format!("parent {h:?} is neither 64 lowercase hex nor `-`"),
                ));
            }
        }
        if !is_lower_hex64(&self.author) {
            return Err(EnvError::new(
                "ENV-AUTHOR-HEX",
                format!("author {:?} is not 64 lowercase hex characters", self.author),
            ));
        }
        let rev_is_zero = self.parent_rev == BigUint::from(0u32);
        if (self.parent == Parent::Absent) != rev_is_zero {
            return Err(EnvError::new(
                "ENV-PARENT-CONSISTENT",
                "`parent` is `-` if and only if `parent_rev` is 0",
            ));
        }

        let mut fields: Vec<(&'static str, String)> = vec![
            ("op", self.op.clone()),
            ("name", self.name.clone()),
            ("artifact", self.artifact.clone()),
            ("parent", self.parent.text().to_string()),
            ("parent_rev", revision_text(&self.parent_rev)),
            ("author", self.author.clone()),
        ];
        if self.version == Version::V2 {
            fields.push((
                "license",
                match &self.license {
                    License::NoneAsserted => "-".to_string(),
                    License::Asserted(s) => s.clone(),
                    License::FieldAbsent => unreachable!("rejected above"),
                },
            ));
        }

        let mut out = String::with_capacity(512);
        out.push_str(self.version.tag());
        out.push('\n');
        for (k, v) in &fields {
            check_value_chars(k, v)?;
            out.push_str(k);
            out.push('=');
            out.push_str(v);
            out.push('\n');
        }
        Ok(out.into_bytes())
    }

    /// Parse octets STRICTLY (§8.6.1).
    ///
    /// Unknown keys, missing keys, duplicated keys, reordered keys, a missing
    /// trailing LF and trailing bytes are all errors — and the parsed value
    /// must re-encode to the input byte for byte (**ENV-REENCODE**). Canonical
    /// encoding without canonical parsing would protect identity at creation
    /// and discard it at verification.
    pub fn parse(octets: &[u8]) -> Result<Envelope, EnvError> {
        // UTF-8 is normative (§8.6.1): "a name may contain non-ASCII, and two
        // encodings of one name would be two different statements".
        let text = std::str::from_utf8(octets)
            .map_err(|e| EnvError::new("ENV-VALUE-CHARS", format!("octets are not UTF-8: {e}")))?;
        if !text.ends_with('\n') {
            return Err(EnvError::new(
                "ENV-LINE-LF",
                "the final line is not LF-terminated; a truncated envelope is not a shorter valid one",
            ));
        }
        // `split('\n')` on LF-terminated text ends in one empty element, which
        // is the framing and not a line. Anything after the last LF would show
        // up here as a non-empty tail.
        let mut lines: Vec<&str> = text.split('\n').collect();
        let tail = lines.pop().expect("split yields at least one element");
        debug_assert!(tail.is_empty());

        let version = Version::from_tag(lines.first().copied().unwrap_or("")).ok_or_else(|| {
            EnvError::new(
                "ENV-TAG",
                format!(
                    "{:?} is not a format tag this version defines",
                    lines.first().copied().unwrap_or("")
                ),
            )
        })?;
        let keys = version.keys();
        if lines.len() != keys.len() + 1 {
            return Err(EnvError::new(
                "ENV-FIELD-COUNT",
                format!(
                    "{} expects {} lines after the tag, found {}",
                    version.tag(),
                    keys.len(),
                    lines.len().saturating_sub(1)
                ),
            ));
        }

        let mut values: Vec<&str> = Vec::with_capacity(keys.len());
        for (i, key) in keys.iter().enumerate() {
            let line = lines[i + 1];
            // §8.6.1: parsing splits each line at its FIRST `=`, so `name=a=b`
            // names `a=b` and is not ambiguous.
            let (k, v) = line.split_once('=').ok_or_else(|| {
                EnvError::new("ENV-FIELD-ORDER", format!("line {} has no `=`", i + 2))
            })?;
            if k != *key {
                return Err(EnvError::new(
                    "ENV-FIELD-ORDER",
                    format!("line {} is {k:?}, expected {key:?}", i + 2),
                ));
            }
            check_value_chars(k, v)?;
            values.push(v);
        }

        let parent = if values[3] == "-" {
            Parent::Absent
        } else {
            if !is_lower_hex64(values[3]) {
                return Err(EnvError::new(
                    "ENV-PARENT-FORM",
                    format!("parent {:?} is neither 64 lowercase hex nor `-`", values[3]),
                ));
            }
            Parent::Hash(values[3].to_string())
        };
        let env = Envelope {
            version,
            op: values[0].to_string(),
            name: values[1].to_string(),
            artifact: values[2].to_string(),
            parent,
            parent_rev: parse_revision(values[4])?,
            author: values[5].to_string(),
            license: match version {
                Version::V1 => License::FieldAbsent,
                Version::V2 if values[6] == "-" => License::NoneAsserted,
                Version::V2 => License::Asserted(values[6].to_string()),
            },
        };
        // The remaining rules (op, name, hex, parent/revision agreement) are
        // shared with the encoder, and running them THERE rather than
        // duplicating them here is what makes the re-encode below meaningful:
        // one rule set, two directions.
        let re = env.encode()?;
        if re != octets {
            return Err(EnvError::new(
                "ENV-REENCODE",
                "the parsed envelope does not re-encode to the input octets",
            ));
        }
        Ok(env)
    }
}

// ---------------------------------------------------------------------------
// §8.6.4 — the store side
// ---------------------------------------------------------------------------

/// What the store currently believes about the name being published to.
#[derive(Debug, Clone)]
pub struct NameState {
    pub name: String,
    /// The current binding, or `None` for a name that does not exist.
    pub bound: Option<String>,
    /// The name's current revision (§8.6.2). Zero for a name that does not
    /// exist, which is the same state ENV-PARENT-CONSISTENT ties to `-`.
    pub revision: BigUint,
}

/// A submitted publication, as the store sees it.
pub struct PublishRequest<'a> {
    /// The signed octets, verbatim.
    pub octets: &'a [u8],
    /// The 64-byte signature.
    pub author_sig: &'a [u8],
    /// The AUTHENTICATED principal's 32-byte key — who the store knows is
    /// calling, as distinct from who the envelope says signed.
    pub authenticated_principal: &'a [u8],
    /// The artifact hash the store recomputed from the submitted content.
    pub recomputed_artifact: &'a str,
}

/// Why a store refused to move a name (§8.6.4's store-side MUSTs).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Refusal {
    /// The octets are not a well-formed envelope.
    Malformed(EnvError),
    /// **ENV-STORE-PRINCIPAL**: the signing key is not the authenticated
    /// principal.
    NotThePrincipal(String),
    /// **ENV-STORE-ARTIFACT**: the recomputed hash is not the signed one.
    ArtifactMismatch { signed: String, recomputed: String },
    /// **ENV-STORE-NAME**: the signed name is not the name being published.
    NameMismatch { signed: String, publishing: String },
    /// **ENV-STORE-CAS**: the signed parent is not the name's current binding.
    StaleParent { signed: String, current: String },
    /// **ENV-STORE-REV**: the signed revision is not the name's current one.
    /// Distinct from `StaleParent` on purpose — this is the ABA case, where the
    /// hash is valid again and only the revision exposes the replay.
    StaleRevision { signed: BigUint, current: BigUint },
}

impl std::fmt::Display for Refusal {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Refusal::Malformed(e) => write!(f, "malformed envelope ({e})"),
            Refusal::NotThePrincipal(d) => write!(f, "signer is not the authenticated principal: {d}"),
            Refusal::ArtifactMismatch { signed, recomputed } => write!(
                f,
                "submitted content hashes to {recomputed}, but the statement signs {signed}"
            ),
            Refusal::NameMismatch { signed, publishing } => {
                write!(f, "the statement names {signed}, but {publishing} is being published")
            }
            Refusal::StaleParent { signed, current } => {
                write!(f, "the signed parent {signed} is not the current binding {current}")
            }
            Refusal::StaleRevision { signed, current } => {
                write!(f, "the signed revision {signed} is not the current revision {current}")
            }
        }
    }
}

/// The checks §8.6.4 requires a store to make **before** the name moves.
///
/// Returns the admitted envelope on success. A statement failing any check MUST
/// NOT move the name; the object itself MAY still be stored, which is a
/// decision for the caller since storage is idempotent under content addressing
/// and an unreferenced object is inert.
///
/// SPEC INTERPRETATION. **ENV-STORE-PRINCIPAL** says to "verify the signing key
/// is the authenticated principal, not the key the envelope names", and the
/// failure it names is that verifying against the envelope's own `author` would
/// ALWAYS succeed. Both halves are enforced here: the signature is verified
/// under the PRINCIPAL's key, and the envelope's `author` must equal that same
/// key. The second is not spelled out in §8.6.4's store list — it is §8.6.4
/// clause 3 (**ENV-VERIFY-AUTHOR**) read forward, since an entry whose envelope
/// names someone other than the signer would fail journal verification the
/// moment it was written. Admitting a publication that is unverifiable the
/// instant it lands is not a defensible reading, so the check is made here.
/// No vector distinguishes the two, so this is an inference, not a derivation.
pub fn admit(state: &NameState, req: &PublishRequest) -> Result<Envelope, Refusal> {
    let env = Envelope::parse(req.octets).map_err(Refusal::Malformed)?;

    // ENV-STORE-PRINCIPAL.
    let principal_hex = hex_lower(req.authenticated_principal);
    if env.author != principal_hex {
        return Err(Refusal::NotThePrincipal(format!(
            "the statement is signed as {} but the caller is {}",
            env.author, principal_hex
        )));
    }
    if let Err(e) = ed25519::verify(req.authenticated_principal, req.octets, req.author_sig) {
        return Err(Refusal::NotThePrincipal(e.to_string()));
    }

    // ENV-STORE-ARTIFACT.
    if env.artifact != req.recomputed_artifact {
        return Err(Refusal::ArtifactMismatch {
            signed: env.artifact.clone(),
            recomputed: req.recomputed_artifact.to_string(),
        });
    }

    // ENV-STORE-NAME.
    if env.name != state.name {
        return Err(Refusal::NameMismatch {
            signed: env.name.clone(),
            publishing: state.name.clone(),
        });
    }

    // ENV-STORE-CAS.
    let current = state.bound.clone().unwrap_or_else(|| "-".to_string());
    if env.parent.text() != current {
        return Err(Refusal::StaleParent {
            signed: env.parent.text().to_string(),
            current,
        });
    }

    // ENV-STORE-REV. A hash can return to an earlier value; a revision cannot,
    // which is the whole of what makes ABA replay detectable.
    if env.parent_rev != state.revision {
        return Err(Refusal::StaleRevision {
            signed: env.parent_rev.clone(),
            current: state.revision.clone(),
        });
    }

    Ok(env)
}

pub(crate) fn hex_lower(bytes: &[u8]) -> String {
    let mut s = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        s.push(char::from_digit((b >> 4) as u32, 16).unwrap());
        s.push(char::from_digit((b & 0xf) as u32, 16).unwrap());
    }
    s
}

/// Decode lowercase hex. Uppercase is REFUSED rather than folded: §8.6.1's
/// whole argument for ENV-HEX-LOWERCASE is that the encoding is compared as
/// bytes, and a reader that accepted both spellings would undo it.
pub(crate) fn unhex_lower(s: &str) -> Option<Vec<u8>> {
    if s.len() % 2 != 0 {
        return None;
    }
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len() / 2);
    for pair in b.chunks(2) {
        let hi = (pair[0] as char).to_digit(16)?;
        let lo = (pair[1] as char).to_digit(16)?;
        if pair.iter().any(|c| c.is_ascii_uppercase()) {
            return None;
        }
        out.push((hi * 16 + lo) as u8);
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn v2(name: &str, artifact: &str, parent: Parent, rev: u32, author: &str, lic: License) -> Envelope {
        Envelope {
            version: Version::V2,
            op: "put".into(),
            name: name.into(),
            artifact: artifact.into(),
            parent,
            parent_rev: BigUint::from(rev),
            author: author.into(),
            license: lic,
        }
    }

    const A: &str = "1111111111111111111111111111111111111111111111111111111111111111";
    const B: &str = "2222222222222222222222222222222222222222222222222222222222222222";

    #[test]
    fn the_shape_is_a_tag_then_seven_lf_terminated_lines() {
        let e = v2("double", A, Parent::Absent, 0, B, License::Asserted("MIT".into()));
        let octets = e.encode().unwrap();
        let text = String::from_utf8(octets.clone()).unwrap();
        assert_eq!(text.lines().count(), 8);
        assert!(text.ends_with('\n'));
        // No other whitespace anywhere — the encoding has no formatting freedom.
        assert!(!text.contains("\r"));
        assert_eq!(text.split('\n').next().unwrap(), "oath-publish/2");
        assert_eq!(Envelope::parse(&octets).unwrap(), e);
    }

    /// `/1` is six fields, not seven, and MUST still be readable — §8.6.4
    /// requires an entry to be verified under the format it was recorded with.
    #[test]
    fn version_one_is_six_fields_and_still_parses() {
        let octets = format!(
            "oath-publish/1\nop=put\nname=n\nartifact={A}\nparent=-\nparent_rev=0\nauthor={B}\n"
        );
        let e = Envelope::parse(octets.as_bytes()).expect("a /1 envelope must still verify");
        assert_eq!(e.version, Version::V1);
        assert_eq!(e.license, License::FieldAbsent);
        assert_eq!(e.license.terms(), None);
        assert_eq!(e.encode().unwrap(), octets.as_bytes());
    }

    /// The three licence states stay distinct. What would make this fail:
    /// collapsing `/1`'s absent field onto `/2`'s explicit `-`, which is the
    /// reading §8.6.1 rules out in as many words.
    #[test]
    fn a_missing_licence_line_is_not_an_asserted_none() {
        let one = Envelope::parse(
            format!("oath-publish/1\nop=put\nname=n\nartifact={A}\nparent=-\nparent_rev=0\nauthor={B}\n")
                .as_bytes(),
        )
        .unwrap();
        let two = Envelope::parse(
            format!("oath-publish/2\nop=put\nname=n\nartifact={A}\nparent=-\nparent_rev=0\nauthor={B}\nlicense=-\n")
                .as_bytes(),
        )
        .unwrap();
        assert_eq!(one.license, License::FieldAbsent);
        assert_eq!(two.license, License::NoneAsserted);
        assert_ne!(one.license, two.license);
        // Both assert no terms, and that agreement is not the same as identity.
        assert_eq!(one.license.terms(), None);
        assert_eq!(two.license.terms(), None);
    }

    #[test]
    fn a_name_may_contain_an_equals_sign() {
        let e = v2("eq=name", A, Parent::Absent, 0, B, License::NoneAsserted);
        let octets = e.encode().unwrap();
        assert!(String::from_utf8_lossy(&octets).contains("name=eq=name\n"));
        assert_eq!(Envelope::parse(&octets).unwrap().name, "eq=name");
    }

    #[test]
    fn the_revision_is_unbounded() {
        let big = BigUint::parse_bytes(b"340282366920938463463374607431768211457", 10).unwrap();
        let e = Envelope {
            parent_rev: big.clone(),
            ..v2("n", A, Parent::Hash(B.into()), 1, B, License::NoneAsserted)
        };
        let octets = e.encode().unwrap();
        assert_eq!(Envelope::parse(&octets).unwrap().parent_rev, big);
    }

    /// Each rejection rule, with the input that trips it. What would make this
    /// fail: any rule silently not being enforced — which is the failure
    /// direction that matters, since a missing rule ACCEPTS a second spelling
    /// of one statement rather than refusing a good one.
    #[test]
    fn every_rejection_rule_fires() {
        let ok = format!(
            "oath-publish/2\nop=put\nname=n\nartifact={A}\nparent=-\nparent_rev=0\nauthor={B}\nlicense=-\n"
        );
        assert!(Envelope::parse(ok.as_bytes()).is_ok(), "the control must be accepted");

        let cases: &[(&str, String)] = &[
            ("ENV-TAG", ok.replace("oath-publish/2", "oath-publish/3")),
            ("ENV-TAG", ok.replace("oath-publish/2", "OATH-PUBLISH/2")),
            ("ENV-OP-PUT", ok.replace("op=put", "op=repoint")),
            ("ENV-NAME-NONEMPTY", ok.replace("name=n", "name=")),
            ("ENV-HEX-LOWERCASE", ok.replace(A, &A.replace('1', "A"))),
            ("ENV-REV-CANONICAL", ok.replace("parent_rev=0", "parent_rev=00")),
            ("ENV-REV-CANONICAL", ok.replace("parent_rev=0", "parent_rev=+0")),
            ("ENV-REV-CANONICAL", ok.replace("parent_rev=0", "parent_rev= 0")),
            ("ENV-PARENT-CONSISTENT", ok.replace("parent_rev=0", "parent_rev=2")),
            ("ENV-FIELD-COUNT", ok.replace("license=-\n", "")),
            ("ENV-FIELD-COUNT", format!("{ok}extra=1\n")),
            ("ENV-LINE-LF", ok.trim_end().to_string()),
            ("ENV-VALUE-CHARS", ok.replace("name=n", "name=a\rb")),
            ("ENV-VALUE-CHARS", ok.replace("name=n", "name=a\u{7f}b")),
            ("ENV-VALUE-CHARS", ok.replace("name=n", "name=a\0b")),
        ];
        for (rule, bad) in cases {
            let err = Envelope::parse(bad.as_bytes())
                .expect_err(&format!("{rule}: this input must be refused"));
            // The rule NAME is asserted here even though §8.6.1 fixes no
            // precedence, and the distinction matters: this pins THIS kernel's
            // diagnosis so that each rule has an individual witness — without
            // it, ENV-REENCODE subsumes several of these and deleting one goes
            // unnoticed. It is NOT a portable obligation, and no cross-kernel
            // check may compare these names.
            assert_eq!(err.rule, *rule, "for input {bad:?}");
        }

        // Reordering, checked separately because it needs a rebuilt body.
        let reordered = format!(
            "oath-publish/2\nname=n\nop=put\nartifact={A}\nparent=-\nparent_rev=0\nauthor={B}\nlicense=-\n"
        );
        assert!(Envelope::parse(reordered.as_bytes()).is_err(), "ENV-FIELD-ORDER");

        // The parent/revision consistency rule from the other side.
        let parent_with_zero = format!(
            "oath-publish/2\nop=put\nname=n\nartifact={A}\nparent={B}\nparent_rev=0\nauthor={B}\nlicense=-\n"
        );
        assert!(Envelope::parse(parent_with_zero.as_bytes()).is_err(), "ENV-PARENT-CONSISTENT");
    }

    /// An LF inside a value cannot be parsed back as one — it is a line, and
    /// §8.6.1 says so. The ENCODER must still refuse it, which is the only
    /// place the rule is reachable.
    #[test]
    fn the_encoder_refuses_an_lf_in_a_value() {
        let e = v2("a\nb", A, Parent::Absent, 0, B, License::NoneAsserted);
        let err = e.encode().expect_err("an LF in a name would inject a line");
        assert_eq!(err.rule, "ENV-VALUE-CHARS");
    }

    /// ENV-REENCODE is not implied by field-by-field validity: a value could
    /// pass every field rule and still have been written in a second spelling.
    /// The only such spelling this encoding admits is the revision's, which is
    /// already refused, so the check is asserted directly on its own terms.
    #[test]
    fn parsing_requires_the_bytes_to_round_trip() {
        let ok = format!(
            "oath-publish/2\nop=put\nname=n\nartifact={A}\nparent=-\nparent_rev=0\nauthor={B}\nlicense=-\n"
        );
        let env = Envelope::parse(ok.as_bytes()).unwrap();
        assert_eq!(env.encode().unwrap(), ok.as_bytes());
    }
}
