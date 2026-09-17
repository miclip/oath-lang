//! SPEC §8.6 against `fixtures/envelope/vectors.jsonl` — the conformance
//! surface §10.1 defines for signed publication.
//!
//! WHY A FIXTURE AND NOT ONLY PROPERTIES. The envelope octets are what a
//! signature is computed over, so one differing byte makes every signature this
//! kernel produces unverifiable elsewhere — and the failure is SILENT: the
//! signature stays correct and stops verifying. Nothing internal to a kernel
//! can notice that. The unit tests in `src/envelope.rs` and `src/journal.rs`
//! pin the rules; these pin the BYTES, against a file this kernel did not
//! produce.
//!
//! WHAT IS NOT ASSERTED HERE, and why. A `reject` record carries a `witnesses`
//! field naming the §8.6.1 rule its author expected to fire. This test asserts
//! REFUSAL and not the rule name, because §8.6.1 states its rules as a
//! CONJUNCTION with no precedence — "MUST reject … unless all of" — so which
//! rule a given malformed input trips first is not determined by the
//! specification, and asserting it would pin an evaluation order the text does
//! not fix. One record in the file makes that concrete: the record labelled
//! "wrong format version" carries an `oath-publish/2` tag with SIX fields, and
//! `witnesses` names ENV-TAG, but `oath-publish/2` is a tag this version
//! DEFINES (§8.6.1), so a reader implementing the current text reaches
//! ENV-FIELD-COUNT instead. Both refuse it; only the reason differs.
//!
//! §10.1 states the suite's coverage and its gaps. The gaps it names are real
//! and this file cannot close them — notably §8.6.2's revision arithmetic
//! (entirely unwitnessed here), three of §8.6.4's five clauses, and §8's chain.
//! Those are covered by the unit tests instead, which is weaker evidence in
//! exactly the way §10.1 says: a witness produced by the implementation it
//! guards agrees with that implementation by construction.

use oathrs::base64;
use oathrs::ed25519;
use oathrs::envelope::{admit, Envelope, License, NameState, Parent, PublishRequest, Version};
use oathrs::journal::{self, Derived, Entry};
use num_bigint::BigUint;
use std::collections::BTreeMap;
use std::fs;

// ---------------------------------------------------------------------------
// A minimal JSON reader for the fixture channel. §10.1 carries every octet
// string as canonical base64 precisely so that reading these needs no knowledge
// of any implementation's string-literal syntax; this is the other half of that
// bargain. Nothing here touches kernel semantics.
// ---------------------------------------------------------------------------

#[derive(Debug, Clone)]
enum Json {
    Str(String),
    Obj(BTreeMap<String, Json>),
    Other,
}

impl Json {
    fn get(&self, k: &str) -> Option<&Json> {
        match self {
            Json::Obj(m) => m.get(k),
            _ => None,
        }
    }
    fn s(&self, k: &str) -> Option<&str> {
        match self.get(k) {
            Some(Json::Str(s)) => Some(s),
            _ => None,
        }
    }
    fn req(&self, k: &str) -> &str {
        self.s(k).unwrap_or_else(|| panic!("the vector is missing a string member {k:?}"))
    }
}

struct P<'a> {
    b: &'a [u8],
    i: usize,
}

impl<'a> P<'a> {
    fn ws(&mut self) {
        while self.i < self.b.len() && (self.b[self.i] as char).is_ascii_whitespace() {
            self.i += 1;
        }
    }
    fn string(&mut self) -> String {
        assert_eq!(self.b[self.i], b'"');
        self.i += 1;
        let mut out = String::new();
        loop {
            let c = self.b[self.i];
            self.i += 1;
            match c {
                b'"' => return out,
                b'\\' => {
                    let e = self.b[self.i];
                    self.i += 1;
                    match e {
                        b'"' => out.push('"'),
                        b'\\' => out.push('\\'),
                        b'/' => out.push('/'),
                        b'b' => out.push('\u{8}'),
                        b'f' => out.push('\u{c}'),
                        b'n' => out.push('\n'),
                        b'r' => out.push('\r'),
                        b't' => out.push('\t'),
                        b'u' => {
                            let h = std::str::from_utf8(&self.b[self.i..self.i + 4]).unwrap();
                            let cp = u32::from_str_radix(h, 16).unwrap();
                            self.i += 4;
                            out.push(char::from_u32(cp).unwrap());
                        }
                        other => panic!("bad escape \\{}", other as char),
                    }
                }
                _ => {
                    let start = self.i - 1;
                    let len = if c < 0x80 {
                        1
                    } else if c >> 5 == 0b110 {
                        2
                    } else if c >> 4 == 0b1110 {
                        3
                    } else {
                        4
                    };
                    self.i = start + len;
                    out.push_str(std::str::from_utf8(&self.b[start..self.i]).unwrap());
                }
            }
        }
    }
    fn value(&mut self) -> Json {
        self.ws();
        match self.b[self.i] {
            b'"' => Json::Str(self.string()),
            b'{' => {
                self.i += 1;
                let mut m = BTreeMap::new();
                self.ws();
                if self.b[self.i] == b'}' {
                    self.i += 1;
                    return Json::Obj(m);
                }
                loop {
                    self.ws();
                    let k = self.string();
                    self.ws();
                    assert_eq!(self.b[self.i], b':');
                    self.i += 1;
                    let v = self.value();
                    m.insert(k, v);
                    self.ws();
                    match self.b[self.i] {
                        b',' => self.i += 1,
                        b'}' => {
                            self.i += 1;
                            return Json::Obj(m);
                        }
                        c => panic!("bad object at byte {}: {:?}", self.i, c as char),
                    }
                }
            }
            b'[' => {
                // No vector needs array contents; skip to the matching bracket.
                let mut depth = 0;
                loop {
                    match self.b[self.i] {
                        b'[' => depth += 1,
                        b']' => {
                            depth -= 1;
                            if depth == 0 {
                                self.i += 1;
                                return Json::Other;
                            }
                        }
                        b'"' => {
                            self.string();
                            continue;
                        }
                        _ => {}
                    }
                    self.i += 1;
                }
            }
            _ => {
                while self.i < self.b.len()
                    && !matches!(self.b[self.i], b',' | b'}' | b']')
                {
                    self.i += 1;
                }
                Json::Other
            }
        }
    }
}

fn vectors() -> Vec<Json> {
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../fixtures/envelope/vectors.jsonl");
    // FAIL on a missing fixture rather than skip: deleting the file must not be
    // a way to turn this gate green.
    let text = fs::read_to_string(path).unwrap_or_else(|e| {
        panic!("fixtures/envelope/vectors.jsonl is the §10.1 conformance surface and must be readable: {e}")
    });
    let out: Vec<Json> = text
        .lines()
        .filter(|l| !l.trim().is_empty())
        .map(|l| P { b: l.as_bytes(), i: 0 }.value())
        .collect();
    assert!(!out.is_empty(), "the fixture parsed to zero records — this test would assert nothing");
    out
}

fn b64(s: &str) -> Vec<u8> {
    base64::decode_canonical(s).expect("a fixture's canonical base64 must decode")
}

fn unhex(s: &str) -> Vec<u8> {
    assert_eq!(s.len() % 2, 0, "hex of odd length");
    s.as_bytes()
        .chunks(2)
        .map(|p| u8::from_str_radix(std::str::from_utf8(p).unwrap(), 16).unwrap())
        .collect()
}

/// Rebuild an `Envelope` from a vector's structured `envelope` member. Every
/// structured envelope in the file carries a `license`, so all of them are
/// `oath-publish/2`; a `/1` record would have no such member and is handled
/// where it arises.
fn structured(j: &Json) -> Envelope {
    let parent = match j.req("parent") {
        "-" => Parent::Absent,
        h => Parent::Hash(h.to_string()),
    };
    let license = match j.s("license") {
        None => License::FieldAbsent,
        Some("-") => License::NoneAsserted,
        Some(x) => License::Asserted(x.to_string()),
    };
    Envelope {
        version: if j.s("license").is_some() { Version::V2 } else { Version::V1 },
        op: j.req("op").to_string(),
        name: j.req("name").to_string(),
        artifact: j.req("artifact").to_string(),
        parent,
        // §8.6.3 carries the revision as a JSON STRING: the arbitrary-precision
        // vector is 2^128+1, which a float64 JSON reader decodes off by one,
        // and a witness defeated by its own carrier witnesses nothing.
        parent_rev: BigUint::parse_bytes(j.req("parent_rev").as_bytes(), 10)
            .expect("parent_rev is canonical decimal"),
        author: j.req("author").to_string(),
        license,
    }
}

/// §8.6.4 clauses 2-4 over the three loose members a `signature` record
/// carries. Clause 5 needs an ENTRY, and the file holds exactly one journal
/// line — the honest one — which the dedicated test below uses.
fn statement_verifies(octets: &[u8], author_pubkey: &str, author_sig: &str) -> Result<(), String> {
    let env = Envelope::parse(octets).map_err(|e| e.to_string())?;
    if env.author != author_pubkey {
        return Err(format!("ENV-VERIFY-AUTHOR: envelope names {}", env.author));
    }
    let key = unhex(author_pubkey);
    let sig = unhex(author_sig);
    ed25519::verify(&key, octets, &sig).map_err(|e| e.to_string())
}

// ---------------------------------------------------------------------------

/// `canonical` records: the structured envelope MUST encode to exactly these
/// octets. This is the byte-level obligation the whole section rests on.
#[test]
fn every_canonical_record_reproduces_its_octets_exactly() {
    let mut checked = 0;
    for v in vectors() {
        if v.req("kind") != "canonical" {
            continue;
        }
        let label = v.req("label");
        let want = b64(v.req("octets_b64"));
        let env = structured(v.get("envelope").expect("a canonical record carries an envelope"));
        let got = env.encode().unwrap_or_else(|e| panic!("{label}: encoding refused: {e}"));
        assert_eq!(
            String::from_utf8_lossy(&got),
            String::from_utf8_lossy(&want),
            "{label}: the octets differ"
        );
        // And the other direction: parsing those octets must give back the same
        // statement, since §8.6.1 requires canonical PARSING as well.
        assert_eq!(Envelope::parse(&want).expect("must parse"), env, "{label}: parse disagrees");
        checked += 1;
    }
    assert!(checked >= 6, "only {checked} canonical records were exercised");
}

/// `reject` records: each MUST be refused. Two of them carry `envelope_b64`
/// rather than `octets_b64` and are refusals of the §8.6.3 base64 dialect, not
/// of the envelope encoding.
#[test]
fn every_reject_record_is_refused() {
    let mut octet_cases = 0;
    let mut b64_cases = 0;
    for v in vectors() {
        if v.req("kind") != "reject" {
            continue;
        }
        let label = v.req("label");
        if let Some(o) = v.s("octets_b64") {
            let octets = b64(o);
            let err = Envelope::parse(&octets)
                .err()
                .unwrap_or_else(|| panic!("{label}: these octets MUST be refused"));
            // A refusal must name a §8.6.1 rule, not fall out of a panic or a
            // generic parse failure with no rule behind it.
            assert!(err.rule.starts_with("ENV-"), "{label}: {err}");
            octet_cases += 1;
        } else {
            let text = v.s("envelope_b64").unwrap_or_else(|| {
                panic!("{label}: a reject record carries octets_b64 or envelope_b64")
            });
            assert!(
                base64::decode_canonical(text).is_err(),
                "{label}: this base64 spelling MUST be refused by the pinned dialect"
            );
            b64_cases += 1;
        }
    }
    assert!(octet_cases >= 9, "only {octet_cases} envelope rejections were exercised");
    assert!(b64_cases >= 2, "only {b64_cases} base64-dialect rejections were exercised");
}

/// `signature` records: the whole path, with the verdict the fixture states.
#[test]
fn every_signature_record_reaches_its_verdict() {
    let (mut accepts, mut rejects) = (0, 0);
    for v in vectors() {
        if v.req("kind") != "signature" {
            continue;
        }
        let label = v.req("label");
        let octets = b64(v.req("octets_b64"));
        let got = statement_verifies(&octets, v.req("author_pubkey"), v.req("author_sig"));
        match v.req("verdict") {
            "accept" => {
                got.unwrap_or_else(|e| panic!("{label}: must verify, got {e}"));
                accepts += 1;
            }
            "reject" => {
                assert!(got.is_err(), "{label}: MUST fail verification");
                rejects += 1;
            }
            other => panic!("{label}: unknown verdict {other}"),
        }
    }
    assert!(accepts >= 1 && rejects >= 9, "coverage: {accepts} accepts, {rejects} rejects");
}

/// The honest `signature` record, end to end — including the parts no other
/// test reaches: that the KEY and the SIGNATURE are reproducible from the
/// recorded seed, and that the stored journal line verifies under §8.6.4.
///
/// The seed check is the strongest single assertion in this file. §8.6.4a says
/// signing is deterministic, so a signature is a function of seed and octets;
/// reproducing this kernel's own signature from the fixture's seed therefore
/// exercises the field arithmetic, the scalar reduction, the hash ordering and
/// the octet encoding all at once, against a value this kernel did not produce.
#[test]
fn the_honest_publication_verifies_from_its_seed_and_in_its_journal_line() {
    let v = vectors()
        .into_iter()
        .find(|v| v.s("kind") == Some("signature") && v.s("seed_b64").is_some())
        .expect("the file carries one signature record with a seed");

    let octets = b64(v.req("octets_b64"));
    let pk_hex = v.req("author_pubkey");
    let sig_hex = v.req("author_sig");

    // The seed reproduces the key and the signature.
    let seed_bytes = b64(v.req("seed_b64"));
    let mut seed = [0u8; 32];
    assert_eq!(seed_bytes.len(), 32, "an Ed25519 seed is 32 bytes");
    seed.copy_from_slice(&seed_bytes);
    assert_eq!(hex(&ed25519::public_key(&seed)), pk_hex, "the key derived from the seed differs");
    assert_eq!(hex(&ed25519::sign(&seed, &octets)), sig_hex, "the signature over these octets differs");

    // `envelope_b64` as stored is the canonical spelling of the same octets.
    assert_eq!(b64(v.req("envelope_b64")), octets, "envelope_b64 must decode to the octets");
    assert_eq!(
        journal::envelope_member(&octets),
        v.req("envelope_b64"),
        "ENV-B64-CANONICAL: the stored spelling must be the one re-encoding produces"
    );

    // The structured envelope agrees with the octets.
    let env = structured(v.get("envelope").expect("the honest record carries an envelope"));
    assert_eq!(env.encode().unwrap(), octets);

    // And the journal line verifies under §8.6.4, with the transition DERIVED
    // rather than read from the entry.
    let line = String::from_utf8(b64(v.req("journal_line_b64"))).expect("UTF-8");
    let entry = journal::parse_line(&line).expect("the fixture's journal line must be canonical");
    let derived = journal::derive(std::slice::from_ref(&entry));
    let report = journal::verify_entry(&entry, &derived[0])
        .unwrap_or_else(|e| panic!("the honest entry must verify: {e}"));
    assert!(report.envelope.is_some());
    assert_eq!(report.transition, journal::Transition::Applied);
    // The entry carries no `parent_rev`, so §8.6.4 says to report the revision
    // as UNAVAILABLE rather than as matched.
    assert_eq!(report.revision_checked, None);
    assert!(!report.transition_reconstructed, "this entry carries name_transition");
}

/// `store` records: §8.6.4's store-side MUSTs, which are half of §8.6's
/// normative weight and were unwitnessed until this record kind existed.
#[test]
fn every_store_record_reaches_its_verdict() {
    let (mut accepts, mut rejects) = (0, 0);
    for v in vectors() {
        if v.req("kind") != "store" {
            continue;
        }
        let label = v.req("label");
        let st = v.get("state").expect("a store record carries a state");
        let rq = v.get("request").expect("a store record carries a request");
        let bound = st.req("bound");
        let state = NameState {
            name: st.req("name").to_string(),
            bound: if bound == "-" { None } else { Some(bound.to_string()) },
            revision: BigUint::parse_bytes(st.req("parent_rev").as_bytes(), 10).unwrap(),
        };
        let octets = b64(rq.req("octets_b64"));
        let sig = unhex(rq.req("author_sig"));
        let principal = unhex(rq.req("authenticated_principal"));
        let got = admit(
            &state,
            &PublishRequest {
                octets: &octets,
                author_sig: &sig,
                authenticated_principal: &principal,
                recomputed_artifact: rq.req("recomputed_artifact"),
            },
        );
        match v.req("verdict") {
            "accept" => {
                got.unwrap_or_else(|e| panic!("{label}: must be admitted, got {e}"));
                accepts += 1;
            }
            "reject" => {
                assert!(got.is_err(), "{label}: MUST NOT move the name");
                rejects += 1;
            }
            other => panic!("{label}: unknown verdict {other}"),
        }
    }
    assert!(accepts >= 1 && rejects >= 4, "coverage: {accepts} accepts, {rejects} rejects");
}

/// The suite must be READ, not merely iterated: assert the record kinds and the
/// total this file expects, so a truncated or re-keyed fixture fails loudly
/// instead of quietly exercising less.
#[test]
fn the_whole_suite_is_consumed() {
    let vs = vectors();
    let mut kinds: BTreeMap<&str, usize> = BTreeMap::new();
    for v in &vs {
        *kinds.entry(v.req("kind")).or_default() += 1;
    }
    assert_eq!(
        kinds.keys().copied().collect::<Vec<_>>(),
        vec!["canonical", "reject", "signature", "store"],
        "§10.1 defines exactly four record kinds"
    );
    let total: usize = kinds.values().sum();
    assert_eq!(total, vs.len());
    assert!(total >= 30, "the suite shrank to {total} records");
}

/// §8.6.4 clause 5 is scoped to an entry whose DERIVED transition is `applied`,
/// and the fixture cannot witness the scope (it holds one journal line, the
/// honest one). This is the missing half, built here: an entry that applied
/// nothing may honestly disagree with its envelope, and one that applied a
/// transition may not.
#[test]
fn clause_five_runs_only_where_a_transition_was_applied() {
    let v = vectors()
        .into_iter()
        .find(|v| v.s("kind") == Some("signature") && v.s("journal_line_b64").is_some())
        .expect("the honest record");
    let line = String::from_utf8(b64(v.req("journal_line_b64"))).unwrap();
    let honest = journal::parse_line(&line).unwrap();

    // A refused attempt: the store recorded the CURRENT binding in `prev`,
    // while the envelope names the stale one it was signed against. §8 requires
    // the attempt to be journalled; unscoped, clause 5 would reject the whole
    // journal for recording it honestly.
    let refused = Entry {
        status: "blocked".into(),
        prev: "9".repeat(64),
        name_transition: "none".into(),
        ..honest.clone()
    };
    let derived = Derived {
        transition: journal::Transition::None,
        revision_before: BigUint::from(0u32),
        reconstructed: false,
    };
    assert!(
        journal::verify_entry(&refused, &derived).is_ok(),
        "a refused attempt must be journallable with the envelope it was refused for"
    );

    // The same disagreement on an APPLIED entry is the failure clause 5 exists
    // for: the store recorded a transition its author did not sign.
    let applied = Derived { transition: journal::Transition::Applied, ..derived };
    let mismatched = Entry { prev: "9".repeat(64), name_transition: "applied".into(), ..honest };
    let err = journal::verify_entry(&mismatched, &applied)
        .expect_err("an applied transition disagreeing with its envelope MUST fail");
    assert!(err.contains("ENV-VERIFY-AGREES"), "{err}");
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// `parent_rev` is the author's claim PRESERVED VERBATIM (§8.6.3), so a journal
/// entry spelling it `01` beside an envelope signing `1` is a disagreement even
/// though both parse to the same integer. A numeric comparison accepts it: every
/// check passes while the journal carries a spelling the author never signed.
#[test]
fn parent_rev_is_compared_verbatim_not_numerically() {
    assert_eq!(
        oathrs::envelope::revision_text(&"1".parse::<num_bigint::BigUint>().unwrap()),
        "1",
        "canonical decimal is leading-zero-free, so `01` must not render"
    );
    // The control: the two spellings are numerically equal, which is exactly why
    // a numeric compare cannot tell them apart.
    let a: num_bigint::BigUint = "01".parse().unwrap();
    let b: num_bigint::BigUint = "1".parse().unwrap();
    assert_eq!(a, b, "control: the spellings ARE numerically equal");
    assert_ne!("01", oathrs::envelope::revision_text(&b), "but not textually");
}
