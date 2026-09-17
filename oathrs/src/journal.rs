//! The journal — SPEC §8, §8.2.1 (canonical entry encoding), §8.2.2 (the entry
//! digest), §8.4 (entry signatures), §8.6.2 (the name revision) and §8.6.4
//! (verification obligations for a signed publication).
//!
//! §8.6 is the section this module exists for, but none of it can be checked
//! per entry. **ENV-VERIFY-DERIVED-TRANSITION** makes entry verification a
//! WHOLE-JOURNAL operation: the transition must be DERIVED from the history
//! preceding an entry and never read from the entry, because the member that
//! decides whether §8.6.4 clause 5 runs at all is written by the store — and a
//! store could label a transition `unchanged`, attach a genuine signature over
//! an unrelated envelope, and pass every clause. So the fold comes first and
//! the obligations are stated against its output.
//!
//! EMPTY IS ABSENT, ON THE WIRE. §8.2.1 omits every member whose value is
//! empty, so an absent member and an empty one have one encoding and cannot be
//! told apart by a reader. §8.6.3 nevertheless distinguishes them for
//! `applied_via` ("Absent is a fourth state and is not `none`") — and it can,
//! because `none` is a non-empty value. The representation here is therefore a
//! plain `String` per member with the empty string meaning absent; that is the
//! wire's own distinction, and inventing a richer one in memory would let this
//! kernel hold a state it could not write down.

use crate::base64;
use crate::ed25519;
use crate::envelope::{hex_lower, unhex_lower, Envelope, Parent};
use crate::hash::sha256_hex;
use num_bigint::BigUint;
use std::collections::HashMap;

/// §8.2.1's member order. **NORMATIVE, not a formatting preference**: `chain`
/// and the entry signature are both computed over "the entry's compact JSON",
/// so two implementations ordering these differently compute different chains
/// and different signatures for one logical entry, and each then rejects the
/// other's journal wholesale.
pub const MEMBER_ORDER: [&str; 23] = [
    "seq",
    "time",
    "author",
    "verifier",
    "name",
    "kind",
    "status",
    "hash",
    "prev",
    "error",
    "guarantee",
    "termination",
    "context",
    "pubkey",
    "sig",
    "envelope_b64",
    "author_pubkey",
    "author_sig",
    "recipient_sig",
    "parent_rev",
    "name_transition",
    "applied_via",
    "chain",
];

/// The members §8.2.1 keeps even when empty. `verifier` is in this list because
/// §8.2.1 says so — a note in that section records that omitting it made a
/// strict reader refuse the conformance fixture's own journal line.
const ALWAYS_PRESENT: [&str; 6] = ["seq", "time", "author", "verifier", "name", "status"];

/// One journal entry (§8).
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Entry {
    pub seq: u64,
    pub time: String,
    pub author: String,
    pub verifier: String,
    pub name: String,
    pub kind: String,
    pub status: String,
    pub hash: String,
    pub prev: String,
    pub error: String,
    pub guarantee: String,
    pub termination: String,
    pub context: String,
    pub pubkey: String,
    pub sig: String,
    pub envelope_b64: String,
    pub author_pubkey: String,
    pub author_sig: String,
    pub recipient_sig: String,
    /// The revision the author SIGNED AGAINST, as a decimal STRING — never a
    /// number, since §8.6.1 makes the revision unbounded and a float64 reader
    /// corrupts values past 2^53 (§8.6.3).
    pub parent_rev: String,
    pub name_transition: String,
    pub applied_via: String,
    pub chain: String,
}

impl Entry {
    fn member(&self, key: &str) -> &str {
        match key {
            "time" => &self.time,
            "author" => &self.author,
            "verifier" => &self.verifier,
            "name" => &self.name,
            "kind" => &self.kind,
            "status" => &self.status,
            "hash" => &self.hash,
            "prev" => &self.prev,
            "error" => &self.error,
            "guarantee" => &self.guarantee,
            "termination" => &self.termination,
            "context" => &self.context,
            "pubkey" => &self.pubkey,
            "sig" => &self.sig,
            "envelope_b64" => &self.envelope_b64,
            "author_pubkey" => &self.author_pubkey,
            "author_sig" => &self.author_sig,
            "recipient_sig" => &self.recipient_sig,
            "parent_rev" => &self.parent_rev,
            "name_transition" => &self.name_transition,
            "applied_via" => &self.applied_via,
            "chain" => &self.chain,
            other => panic!("not a journal member: {other}"),
        }
    }

    fn set_member(&mut self, key: &str, value: String) {
        let slot = match key {
            "time" => &mut self.time,
            "author" => &mut self.author,
            "verifier" => &mut self.verifier,
            "name" => &mut self.name,
            "kind" => &mut self.kind,
            "status" => &mut self.status,
            "hash" => &mut self.hash,
            "prev" => &mut self.prev,
            "error" => &mut self.error,
            "guarantee" => &mut self.guarantee,
            "termination" => &mut self.termination,
            "context" => &mut self.context,
            "pubkey" => &mut self.pubkey,
            "sig" => &mut self.sig,
            "envelope_b64" => &mut self.envelope_b64,
            "author_pubkey" => &mut self.author_pubkey,
            "author_sig" => &mut self.author_sig,
            "recipient_sig" => &mut self.recipient_sig,
            "parent_rev" => &mut self.parent_rev,
            "name_transition" => &mut self.name_transition,
            "applied_via" => &mut self.applied_via,
            "chain" => &mut self.chain,
            other => panic!("not a journal member: {other}"),
        };
        *slot = value;
    }

    /// The canonical line of §8.2.1: the JSON object bytes ALONE. The record
    /// separator is framing and is NOT part of entry identity — it does not
    /// enter the chain, the entry signature, or the entry digest.
    pub fn encode(&self) -> String {
        self.encode_clearing(&[])
    }

    /// The canonical line with the named members forced empty (and therefore
    /// omitted, unless §8.2.1 keeps them). This is the one primitive under
    /// [`Entry::encode`], [`Entry::signed_content`] and [`Entry::chain_input`]:
    /// all three are "the entry's compact JSON" with a different set blanked,
    /// and writing them as one function is what stops the three drifting apart.
    fn encode_clearing(&self, clear: &[&str]) -> String {
        let mut out = String::with_capacity(512);
        out.push('{');
        let mut first = true;
        for key in MEMBER_ORDER {
            let cleared = clear.contains(&key);
            let always = ALWAYS_PRESENT.contains(&key);
            if key == "seq" {
                let v = if cleared { 0 } else { self.seq };
                if v == 0 && !always {
                    continue;
                }
                if !first {
                    out.push(',');
                }
                first = false;
                out.push_str("\"seq\":");
                out.push_str(&v.to_string());
                continue;
            }
            let value = if cleared { "" } else { self.member(key) };
            if value.is_empty() && !always {
                continue;
            }
            if !first {
                out.push(',');
            }
            first = false;
            out.push('"');
            out.push_str(key);
            out.push_str("\":");
            push_json_string(&mut out, value);
        }
        out.push('}');
        out
    }

    /// §8.4's **signed content**: the entry's compact JSON with the
    /// store-assigned fields `seq`, `time`, `verifier`, `applied_via` and
    /// `chain`, and `sig` itself, all empty or omitted.
    ///
    /// The exclusion list is exactly the set the STORE assigns rather than the
    /// author, so the signature covers the authored fields (including `pubkey`)
    /// independent of where the entry lands in the log.
    pub fn signed_content(&self) -> String {
        self.encode_clearing(&["seq", "time", "verifier", "applied_via", "chain", "sig"])
    }

    /// The `entry-bytes` of §8's chain rule: the entry's compact JSON with
    /// `chain` empty (omitted).
    pub fn chain_input(&self) -> String {
        self.encode_clearing(&["chain"])
    }

    /// §8.2.2's **entry digest** — SHA-256 over the exact canonical entry line,
    /// lowercase hex. This, not the artifact hash, is the identity of one
    /// PUBLICATION: an artifact hash identifies content, and the same content
    /// may be published more than once.
    ///
    /// Where the entry is CHAINED the digest also fixes its position, because
    /// `chain` is part of the line. Where it is NOT chained it identifies
    /// content only — see [`Entry::digest_fixes_position`].
    pub fn digest(&self) -> String {
        sha256_hex(self.encode().as_bytes())
    }

    /// Whether this entry's digest establishes its POSITION. §8.2.2: an
    /// implementation relying on positional uniqueness MUST require the entry
    /// to be chained, because an unchained entry duplicated verbatim elsewhere
    /// has the same digest.
    pub fn digest_fixes_position(&self) -> bool {
        !self.chain.is_empty()
    }
}

/// The serialization mechanism a store declares it wrote an entry under
/// (§8.6.3, §8.6.5).
///
/// **Unrecorded is a FOURTH state and is not `None_`.** Absence is the absence
/// of a record; `none` is a positive statement that no coordination mechanism
/// was used. Collapsing them would retroactively make a claim on behalf of
/// every entry written before the member existed — and the fabricated claim
/// would be the WEAKEST one, describing a store's own history as uncoordinated
/// when the journal simply never asked.
///
/// **It is a SELF-DECLARATION, not re-derivable evidence.** Unlike a guarantee
/// or a termination verdict, the mechanism is not a function of any bytes the
/// artifact carries; it is unobservable after the fact. A surface reporting it
/// MUST keep it separate from re-derived evidence and MUST NOT present it as
/// verified, attested, signed or authenticated — not even on an entry whose
/// `sig` verifies, because §8.4 excludes the member from the signed content.
/// What the CHAIN adds is only that the store said this at write time and has
/// not since changed its story; that is strictly weaker than attestation and
/// holds only for a verifier that has actually checked the chain.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AppliedVia {
    /// The member is absent. The store claims nothing.
    Unrecorded,
    /// No serialization mechanism was used (a single-process local store).
    None_,
    /// A lock that narrows the shared-writer window without closing it.
    AdvisoryLock,
    /// The parent/revision comparison, the name update AND the journal append
    /// all participate in ONE atomic operation. All three are required: a
    /// transaction around the name update alone leaves exactly the lost-update
    /// window this value exists to rule out.
    TransactionalCas,
    /// A value §8.6.3 does not define. Kept distinct rather than folded into
    /// `Unrecorded` or `None_`, for the same reason those two are distinct: a
    /// reader must not manufacture a claim the store did not make.
    Unrecognised(String),
}

impl Entry {
    /// Read the entry's `applied_via` claim. See [`AppliedVia`] before
    /// displaying the result anywhere.
    pub fn applied_via_claim(&self) -> AppliedVia {
        match self.applied_via.as_str() {
            "" => AppliedVia::Unrecorded,
            "none" => AppliedVia::None_,
            "advisory-lock" => AppliedVia::AdvisoryLock,
            "transactional-cas" => AppliedVia::TransactionalCas,
            other => AppliedVia::Unrecognised(other.to_string()),
        }
    }
}

/// §8.2.1's minimal escaping.
///
/// Only `"`, `\` and characters below U+0020 are escaped, using JSON's short
/// forms where they exist and `\u00XX` otherwise. `<`, `>`, `&` and `/` MUST
/// NOT be escaped — at least one widely used encoder escapes the first three by
/// default for HTML safety, and that habit forks two implementations over a
/// rejection message containing `<`.
///
/// U+2028 and U+2029 are the two required exceptions: they are Unicode line
/// terminators and the journal is line-delimited, so a literal one invites a
/// reader splitting on Unicode line boundaries to see two records where there
/// is one.
///
/// UNDETERMINED BY §8.2.1: the CASE of the hex digits in `\u00XX`. Lowercase is
/// used here. Nothing in the section fixes it, and the two spellings are
/// different bytes for one entry — so this is a genuine fork risk, chosen by
/// convention rather than derived.
fn push_json_string(out: &mut String, s: &str) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            c if (c as u32) < 0x20 => {
                out.push_str(&format!("\\u{:04x}", c as u32));
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

// ---------------------------------------------------------------------------
// Strict reading (§8.2.1)
// ---------------------------------------------------------------------------

/// Parse one journal line STRICTLY.
///
/// §8.2.1: "parse the line, re-encode it canonically, and reject the journal
/// unless the result is byte-identical to the stored line. Unknown members,
/// duplicated members, reordered members, added whitespace, and trailing
/// content after the object are all errors."
///
/// Each of those is refused explicitly AND the re-encode is compared. The
/// redundancy is deliberate: the re-encode alone would catch them all, but only
/// as "does not round-trip", and a verifier that cannot say WHICH rule a line
/// broke is hard to trust. Canonical writing with lenient reading protects
/// nothing — accepting a second spelling means accepting two identities for one
/// entry.
pub fn parse_line(line: &str) -> Result<Entry, String> {
    let b: Vec<char> = line.chars().collect();
    let mut i = 0usize;
    if b.first() != Some(&'{') {
        return Err("a journal entry is a JSON object".into());
    }
    i += 1;
    let mut entry = Entry::default();
    let mut seen: Vec<String> = Vec::new();
    if b.get(i) != Some(&'}') {
        loop {
            let key = read_string(&b, &mut i)?;
            if !MEMBER_ORDER.contains(&key.as_str()) {
                return Err(format!("unknown member {key:?}"));
            }
            if seen.iter().any(|k| *k == key) {
                return Err(format!("duplicated member {key:?}"));
            }
            // Reordering: the member sequence must follow §8.2.1's order.
            if let (Some(prev), Some(pos)) = (
                seen.last().and_then(|p| MEMBER_ORDER.iter().position(|m| m == p)),
                MEMBER_ORDER.iter().position(|m| *m == key),
            ) {
                if pos <= prev {
                    return Err(format!("member {key:?} is out of §8.2.1 order"));
                }
            }
            if b.get(i) != Some(&':') {
                return Err(format!("expected `:` after {key:?}"));
            }
            i += 1;
            if key == "seq" {
                entry.seq = read_uint(&b, &mut i)?;
            } else {
                let v = read_string(&b, &mut i)?;
                entry.set_member(&key, v);
            }
            seen.push(key);
            match b.get(i) {
                Some(',') => i += 1,
                Some('}') => break,
                _ => return Err("expected `,` or `}`".into()),
            }
        }
    }
    if b.get(i) != Some(&'}') {
        return Err("unterminated object".into());
    }
    i += 1;
    if i != b.len() {
        return Err("trailing content after the object".into());
    }
    let re = entry.encode();
    if re != line {
        return Err("the line is not canonical: it does not re-encode to itself".into());
    }
    Ok(entry)
}

fn read_uint(b: &[char], i: &mut usize) -> Result<u64, String> {
    let start = *i;
    while b.get(*i).map(|c| c.is_ascii_digit()).unwrap_or(false) {
        *i += 1;
    }
    if *i == start {
        return Err("expected a number".into());
    }
    let text: String = b[start..*i].iter().collect();
    // Canonical decimal: a leading zero would be a second spelling, and the
    // re-encode below would catch it, but naming it here is cheaper to read.
    if text.len() > 1 && text.starts_with('0') {
        return Err("seq has a leading zero".into());
    }
    text.parse::<u64>().map_err(|e| format!("seq: {e}"))
}

fn read_string(b: &[char], i: &mut usize) -> Result<String, String> {
    if b.get(*i) != Some(&'"') {
        return Err("expected a string".into());
    }
    *i += 1;
    let mut s = String::new();
    loop {
        match b.get(*i) {
            None => return Err("unterminated string".into()),
            Some('"') => {
                *i += 1;
                return Ok(s);
            }
            Some('\\') => {
                *i += 1;
                let hex4 = |i: &mut usize| -> Result<u32, String> {
                    let mut code = 0u32;
                    for _ in 0..4 {
                        *i += 1;
                        let d = b.get(*i).and_then(|c| c.to_digit(16)).ok_or("bad \\u escape")?;
                        code = code * 16 + d;
                    }
                    Ok(code)
                };
                match b.get(*i) {
                    Some('"') => s.push('"'),
                    Some('\\') => s.push('\\'),
                    Some('/') => s.push('/'),
                    Some('b') => s.push('\u{8}'),
                    Some('f') => s.push('\u{c}'),
                    Some('n') => s.push('\n'),
                    Some('r') => s.push('\r'),
                    Some('t') => s.push('\t'),
                    Some('u') => {
                        let hi = hex4(i)?;
                        let ch = if (0xD800..0xDC00).contains(&hi) {
                            if b.get(*i + 1) != Some(&'\\') || b.get(*i + 2) != Some(&'u') {
                                return Err("high surrogate not followed by \\u".into());
                            }
                            *i += 2;
                            let lo = hex4(i)?;
                            if !(0xDC00..0xE000).contains(&lo) {
                                return Err("high surrogate not followed by a low surrogate".into());
                            }
                            char::from_u32(0x10000 + ((hi - 0xD800) << 10) + (lo - 0xDC00))
                                .ok_or("bad surrogate pair")?
                        } else {
                            char::from_u32(hi).ok_or("bad \\u escape")?
                        };
                        s.push(ch);
                    }
                    _ => return Err("bad escape".into()),
                }
                *i += 1;
            }
            Some(&c) => {
                s.push(c);
                *i += 1;
            }
        }
    }
}

// ---------------------------------------------------------------------------
// §8.6.2 — the name revision
// ---------------------------------------------------------------------------

/// What an entry did to the NAME (§8.6.3). A different dimension from `status`,
/// which records what the store concluded about the ARTIFACT: a definition may
/// be `falsified` and still bind its name, and a `prove` or `cross` entry
/// concerns an artifact and touches no name.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Transition {
    Applied,
    Unchanged,
    None,
}

impl Transition {
    pub fn text(self) -> &'static str {
        match self {
            Transition::Applied => "applied",
            Transition::Unchanged => "unchanged",
            Transition::None => "none",
        }
    }

    pub fn parse(s: &str) -> Option<Transition> {
        match s {
            "applied" => Some(Transition::Applied),
            "unchanged" => Some(Transition::Unchanged),
            "none" => Some(Transition::None),
            _ => None,
        }
    }
}

/// What the fold established about one entry.
#[derive(Debug, Clone)]
pub struct Derived {
    /// The transition, derived from history — never read from the entry.
    pub transition: Transition,
    /// The name's revision as of the moment BEFORE this entry, which is what
    /// **ENV-VERIFY-REVISION** compares an author's signed claim against.
    pub revision_before: BigUint,
    /// True when the entry carried no `name_transition` member, so the value
    /// above is a HISTORICAL RECONSTRUCTION. §8.6.4 asks a verifier to report
    /// it as reconstructed rather than as stated: history is not rewritten, but
    /// how it was interpreted is disclosed.
    pub reconstructed: bool,
}

/// Derive every entry's transition and the revision it ran against (§8.6.2).
///
/// **The derivation is a FOLD, not a per-entry predicate.** It tracks what each
/// name is bound to as it goes and compares each entry's `hash` against that
/// running value. A per-entry test — "`prev` equals `hash`, so it was a no-op" —
/// cannot work: the rule legacy entries were written under omitted `prev`
/// whenever the name already pointed at the same hash, so a legacy no-op
/// carries NO `prev`, and an absent `prev` is irrecoverably ambiguous between
/// "the name was new" and "the name was already here". That test misses not
/// some no-ops but ALL of them.
///
/// SPEC CONTRADICTION, resolved here and reported rather than hidden. §8.6.2
/// says an entry has applied a transition "if and only if its `name_transition`
/// member is `applied`", and that the reconstruction below MUST NOT be extended
/// to entries that carry the member. §8.6.4's **ENV-VERIFY-DERIVED-TRANSITION**
/// says a verifier MUST derive the transition from history and MUST NOT take it
/// from the member, failing the entry where the two disagree. Those cannot both
/// be obeyed, because the reconstruction is the ONLY derivation the
/// specification defines. This function follows §8.6.4: it folds over every
/// entry, and [`verify`] compares the stored member where one is present. That
/// side is chosen because it is the side with a stated threat model — reading
/// the member inverts the trust model for exactly the field that decides
/// whether clause 5 runs at all.
pub fn derive(entries: &[Entry]) -> Vec<Derived> {
    let mut bound: HashMap<String, String> = HashMap::new();
    let mut revision: HashMap<String, BigUint> = HashMap::new();
    let mut out = Vec::with_capacity(entries.len());
    for e in entries {
        let before = revision.get(&e.name).cloned().unwrap_or_else(|| BigUint::from(0u32));
        let transition = classify(e, bound.get(&e.name).map(|s| s.as_str()));
        if transition == Transition::Applied {
            bound.insert(e.name.clone(), e.hash.clone());
            revision.insert(e.name.clone(), before.clone() + BigUint::from(1u32));
        }
        out.push(Derived {
            transition,
            revision_before: before,
            reconstructed: e.name_transition.is_empty(),
        });
    }
    out
}

/// One step of the fold, given what the name is bound to at this point.
fn classify(e: &Entry, current: Option<&str>) -> Transition {
    // "entries whose `kind` is none of `data`, `func`, `put` (nor absent) applied
    // nothing. A `prove` or `cross` entry concerns an ARTIFACT and touches no
    // name, so counting it would inflate a name's revision."
    if !(e.kind.is_empty() || e.kind == "data" || e.kind == "func" || e.kind == "put") {
        return Transition::None;
    }
    // "otherwise, `accepted` and `falsified` applied a transition, EXCEPT where
    // the entry's `hash` already equals what the name is bound to at that point
    // in the log, which is a no-op."
    if e.status != "accepted" && e.status != "falsified" {
        return Transition::None;
    }
    match current {
        Some(h) if h == e.hash => Transition::Unchanged,
        _ => Transition::Applied,
    }
}

// ---------------------------------------------------------------------------
// §8.4 + §8.6.4 — verification
// ---------------------------------------------------------------------------

/// What verification established about one entry's publication statement.
#[derive(Debug, Clone)]
pub struct EntryReport {
    /// The transition the verifier DERIVED, and whether it had to reconstruct
    /// it (§8.6.4).
    pub transition: Transition,
    pub transition_reconstructed: bool,
    /// The envelope, where the entry carried one.
    pub envelope: Option<Envelope>,
    /// The revision checked under **ENV-VERIFY-REVISION**, or `None` when the
    /// entry predates the member. §8.6.4: a verifier SHOULD report that the
    /// revision was UNAVAILABLE rather than that it matched — the two are
    /// different facts and only one of them is evidence.
    pub revision_checked: Option<BigUint>,
    /// Whether an entry signature (§8.4) was present and verified.
    pub entry_signature_verified: bool,
}

/// Verify a whole journal: §8's chain and sequencing, §8.4's entry signatures,
/// and §8.6.4's publication obligations, in that order.
///
/// Whole-journal because it has to be. §8.6.4's note says this plainly: chain
/// integrity, replay protection, revision evolution and ownership history are
/// already journal properties, an isolated entry has never been independently
/// meaningful, and `name_transition` was the one field pretending otherwise.
///
/// `lines` are the canonical entry lines WITHOUT their separators.
pub fn verify_journal(lines: &[String]) -> Result<Vec<EntryReport>, String> {
    let mut entries = Vec::with_capacity(lines.len());
    for (i, line) in lines.iter().enumerate() {
        entries.push(parse_line(line).map_err(|e| format!("entry {}: {e}", i + 1))?);
    }
    check_sequencing(&entries)?;
    check_chain(&entries, lines)?;

    let derived = derive(&entries);
    let mut reports = Vec::with_capacity(entries.len());
    for (i, (entry, d)) in entries.iter().zip(derived.iter()).enumerate() {
        reports.push(
            verify_entry(entry, d).map_err(|e| format!("entry {} ({}): {e}", i + 1, entry.name))?,
        );
    }
    Ok(reports)
}

/// §8.2's sequencing: `seq` is one plus the number of existing records, so a
/// complete journal numbers its entries 1..n. §8 requires a verifier to reject
/// "a `seq` gap or reorder", and over a whole file those two statements give
/// the same test.
fn check_sequencing(entries: &[Entry]) -> Result<(), String> {
    for (i, e) in entries.iter().enumerate() {
        let want = i as u64 + 1;
        if e.seq != want {
            return Err(format!("entry {} carries seq {} (a gap or reorder)", i + 1, e.seq));
        }
    }
    Ok(())
}

/// §8's chain. `chain = SHA-256(anchor + "\n" + entry-bytes)`, lowercase hex,
/// where `entry-bytes` is the entry's compact JSON with `chain` omitted and
/// `anchor` is the `chain` of the most recent chained entry — or, when no
/// chained entry exists yet, the SHA-256 of the entire byte prefix before this
/// entry, which retroactively seals legacy lines.
///
/// UNDETERMINED BY §8: whether `anchor` is the hex TEXT of those digests or
/// their 32 raw bytes. Hex text is used here, because `chain` is defined as a
/// rendered lowercase-hex value and the rule concatenates it with a `"\n"`
/// separator, which is a text operation. Nothing in the tree witnesses this —
/// §10.1 records that there is no chain fixture anywhere — so two kernels can
/// differ here and both pass conformance.
///
/// The byte prefix includes the separators of the preceding entries, since it
/// is "the entire byte prefix", i.e. the file as far as this entry's first
/// byte.
fn check_chain(entries: &[Entry], lines: &[String]) -> Result<(), String> {
    let mut anchor: Option<String> = None;
    // The bytes of the file so far, INCLUDING each entry's separator — "the
    // entire byte prefix before this entry".
    let mut prefix = String::new();
    for (i, e) in entries.iter().enumerate() {
        if e.chain.is_empty() {
            if anchor.is_some() {
                return Err(format!("entry {} is unchained after a chained entry", i + 1));
            }
        } else {
            let want = chain_value(e, anchor.as_deref(), prefix.as_bytes());
            if want != e.chain {
                return Err(format!("entry {}: chain mismatch", i + 1));
            }
            anchor = Some(e.chain.clone());
        }
        prefix.push_str(&lines[i]);
        prefix.push('\n');
    }
    Ok(())
}

/// The §8.4 and §8.6.4 obligations for ONE entry, given what the fold derived
/// for it. Exposed separately from [`verify_journal`] because the derived facts
/// are the interesting input and a caller may hold them from elsewhere — but
/// note that obtaining them requires the history, which is the whole point of
/// **ENV-VERIFY-DERIVED-TRANSITION**.
pub fn verify_entry(entry: &Entry, derived: &Derived) -> Result<EntryReport, String> {
    // §8.4. "A verifier MUST, for every entry whose `pubkey` or `sig` is
    // non-empty, reject the journal unless `pubkey` is a well-formed 32-byte
    // key and `sig` is a valid Ed25519 signature under it over the signed
    // content." An entry carrying one without the other is a FORGED
    // ATTRIBUTION and must be rejected, not treated as unattributed.
    let mut entry_signature_verified = false;
    if !entry.pubkey.is_empty() || !entry.sig.is_empty() {
        let key = unhex_lower(&entry.pubkey)
            .filter(|k| k.len() == 32)
            .ok_or("§8.4: `pubkey` is not a well-formed 32-byte key")?;
        let sig = unhex_lower(&entry.sig)
            .filter(|s| s.len() == 64)
            .ok_or("§8.4: `sig` is not a well-formed 64-byte signature")?;
        ed25519::verify(&key, entry.signed_content().as_bytes(), &sig)
            .map_err(|e| format!("§8.4: the entry signature does not verify ({e})"))?;
        entry_signature_verified = true;
    }

    // ENV-VERIFY-DERIVED-TRANSITION. Where a stored `name_transition`
    // disagrees with the derived value, the entry MUST fail.
    if !entry.name_transition.is_empty() {
        let stated = Transition::parse(&entry.name_transition)
            .ok_or_else(|| format!("`name_transition` is {:?}", entry.name_transition))?;
        if stated != derived.transition {
            return Err(format!(
                "ENV-VERIFY-DERIVED-TRANSITION: the entry states `{}` but history derives `{}`",
                stated.text(),
                derived.transition.text()
            ));
        }
    }

    let any = !entry.envelope_b64.is_empty()
        || !entry.author_pubkey.is_empty()
        || !entry.author_sig.is_empty();
    if !any {
        // "Entries with none of the three fields impose no obligation and are
        // conformant."
        return Ok(EntryReport {
            transition: derived.transition,
            transition_reconstructed: derived.reconstructed,
            envelope: None,
            revision_checked: None,
            entry_signature_verified,
        });
    }

    // 1. ENV-VERIFY-PRESENT — all three. Any one alone attests to nothing.
    //
    // Checked over the three members BY NAME. §8.6.4 once named a member
    // `envelope`, which §8.6.3 had renamed to `envelope_b64`; an implementation
    // checking presence member-by-member found `envelope` always absent,
    // concluded no obligation applied, and accepted every forged publication
    // while passing every vector. That is why this reads the same names §8.6.3
    // defines and why the failure direction is stated at the site.
    if entry.envelope_b64.is_empty() || entry.author_pubkey.is_empty() || entry.author_sig.is_empty()
    {
        return Err("ENV-VERIFY-PRESENT: a partial envelope field set attests to nothing".into());
    }

    // 2. ENV-VERIFY-DECODE — the pinned base64 dialect, then §8.6.1 parsing,
    //    which itself requires the octets to re-encode to themselves.
    let octets = base64::decode_canonical(&entry.envelope_b64)
        .map_err(|e| format!("ENV-VERIFY-DECODE: `envelope_b64` is not canonical base64 ({e})"))?;
    let env = Envelope::parse(&octets)
        .map_err(|e| format!("ENV-VERIFY-DECODE: the envelope octets are malformed ({e})"))?;

    // 3. ENV-VERIFY-AUTHOR.
    if env.author != entry.author_pubkey {
        return Err(format!(
            "ENV-VERIFY-AUTHOR: the envelope names {} but the entry records {}",
            env.author, entry.author_pubkey
        ));
    }

    // 4. ENV-VERIFY-SIGNATURE — over the octets DECODED FROM `envelope_b64`,
    //    never over a re-encoding of the parsed envelope and never over the
    //    base64 text.
    let key = unhex_lower(&entry.author_pubkey)
        .filter(|k| k.len() == 32)
        .ok_or("ENV-VERIFY-SIGNATURE: `author_pubkey` is not 32 bytes of lowercase hex")?;
    let sig = unhex_lower(&entry.author_sig)
        .filter(|s| s.len() == 64)
        .ok_or("ENV-VERIFY-SIGNATURE: `author_sig` is not 64 bytes of lowercase hex")?;
    ed25519::verify(&key, &octets, &sig)
        .map_err(|e| format!("ENV-VERIFY-SIGNATURE: {e}"))?;

    // 5. ENV-VERIFY-AGREES — SCOPED to an entry whose transition is `applied`.
    //    An entry that applied nothing makes no claim to have moved a name, and
    //    its envelope is a record of what was ATTEMPTED rather than of what
    //    happened: a refused publication legitimately records a `prev` naming
    //    the current binding while its envelope names the stale one it was
    //    signed against, and a gate rejection carries no `hash` at all while
    //    `artifact` must be 64 hex.
    if derived.transition == Transition::Applied {
        if env.name != entry.name {
            return Err(format!(
                "ENV-VERIFY-AGREES: the envelope names {:?} but the entry moved {:?}",
                env.name, entry.name
            ));
        }
        if env.artifact != entry.hash {
            return Err(format!(
                "ENV-VERIFY-AGREES: the envelope signs artifact {} but the entry bound {}",
                env.artifact, entry.hash
            ));
        }
        let entry_parent = if entry.prev.is_empty() { "-" } else { &entry.prev };
        if env.parent.text() != entry_parent {
            return Err(format!(
                "ENV-VERIFY-AGREES: the envelope signs parent {} but the entry's prev is {}",
                env.parent.text(),
                entry_parent
            ));
        }
    }

    // ENV-VERIFY-REVISION. For an entry CARRYING `parent_rev`, the value must
    // equal the envelope's and the revision derived from the history preceding
    // the entry. Entries predating the member are verified without the check.
    let mut revision_checked = None;
    if !entry.parent_rev.is_empty() {
        // The member is the author's claim PRESERVED VERBATIM (§8.6.3), so it is
        // compared as TEXT against the envelope's canonical decimal, not parsed
        // and compared numerically. A numeric compare accepts `01` beside an
        // envelope signing `1`: both parse to the same integer, so every check
        // passes while the journal carries a spelling the author never signed —
        // and re-encoding it would then produce different bytes.
        if entry.parent_rev != crate::envelope::revision_text(&env.parent_rev) {
            return Err(format!(
                "ENV-VERIFY-REVISION: the entry records `parent_rev` {:?} but the envelope signs {:?} — the member is preserved verbatim, so a differing spelling of the same number is still a disagreement",
                entry.parent_rev, crate::envelope::revision_text(&env.parent_rev)
            ));
        }
        let recorded = BigUint::parse_bytes(entry.parent_rev.as_bytes(), 10).ok_or_else(|| {
            format!("ENV-VERIFY-REVISION: `parent_rev` {:?} is not decimal", entry.parent_rev)
        })?;
        if recorded != env.parent_rev {
            return Err(format!(
                "ENV-VERIFY-REVISION: the entry records {} but the envelope signs {}",
                recorded, env.parent_rev
            ));
        }
        if recorded != derived.revision_before {
            return Err(format!(
                "ENV-VERIFY-REVISION: the entry records {} but history derives {}",
                recorded, derived.revision_before
            ));
        }
        revision_checked = Some(recorded);
    }

    Ok(EntryReport {
        transition: derived.transition,
        transition_reconstructed: derived.reconstructed,
        envelope: Some(env),
        revision_checked,
        entry_signature_verified,
    })
}

/// Build the `chain` value for an entry appended after `anchor` (§8). `anchor`
/// is the previous chained entry's `chain`, or `None` for the first chained
/// entry in a journal, in which case `prefix` is the file's bytes so far.
pub fn chain_value(entry: &Entry, anchor: Option<&str>, prefix: &[u8]) -> String {
    let a = match anchor {
        Some(a) => a.to_string(),
        None => sha256_hex(prefix),
    };
    sha256_hex(format!("{a}\n{}", entry.chain_input()).as_bytes())
}

/// Encode an entry's `envelope_b64` member from octets (§8.6.3). A store MUST
/// persist the octets VERBATIM and MUST NOT re-encode, normalize or reformat
/// them: base64 is a storage representation, not a transformation of the
/// statement, and reconstructing the octets later from the duplicated fields
/// would let an encoder change silently invalidate old signatures — the
/// signature would remain correct and stop verifying.
pub fn envelope_member(octets: &[u8]) -> String {
    base64::encode(octets)
}

/// The hex spelling §8.6.3 requires for `author_pubkey` and `author_sig`.
pub fn hex_member(bytes: &[u8]) -> String {
    hex_lower(bytes)
}

/// Whether an envelope's parent agrees with an entry's `prev`, where an empty
/// `prev` corresponds to `-` (§8.6.4 clause 5). Exposed because the mapping is
/// easy to get backwards and §8.2's amendment note exists precisely because an
/// earlier rule made a same-hash re-publication journal an absent `prev` while
/// its envelope named the real parent.
pub fn parent_matches(env_parent: &Parent, prev: &str) -> bool {
    env_parent.text() == if prev.is_empty() { "-" } else { prev }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry() -> Entry {
        Entry {
            seq: 1,
            time: "2026-08-01T00:00:00Z".into(),
            author: "claude-main".into(),
            verifier: "oath-kernel/0.7".into(),
            name: "double".into(),
            kind: "func".into(),
            status: "accepted".into(),
            hash: "5".repeat(64),
            ..Entry::default()
        }
    }

    #[test]
    fn the_member_order_is_the_one_82_1_fixes() {
        let e = entry();
        let line = e.encode();
        let mut last = 0usize;
        for m in MEMBER_ORDER {
            if let Some(pos) = line.find(&format!("\"{m}\":")) {
                assert!(pos >= last, "{m} appears out of order");
                last = pos;
            }
        }
        // The always-present six survive being empty; nothing else does.
        let bare = Entry { seq: 0, ..Entry::default() }.encode();
        assert_eq!(bare, r#"{"seq":0,"time":"","author":"","verifier":"","name":"","status":""}"#);
    }

    /// §8.2.1's escaping, in the direction that forks implementations: an
    /// encoder escaping `<`, `>` or `&` for HTML safety writes different bytes
    /// for one entry, and chain hashes and signatures are over those bytes.
    #[test]
    fn html_unsafe_characters_are_left_alone_and_line_terminators_are_not() {
        let e = Entry {
            error: "a < b && c > d, path /x".into(),
            guarantee: "tab\there\nnewline\u{2028}ls\u{2029}ps\u{1}nul-ish".into(),
            ..entry()
        };
        let line = e.encode();
        assert!(line.contains("a < b && c > d, path /x"), "no HTML escaping");
        assert!(line.contains("tab\\there"), "tab uses its short form");
        assert!(line.contains("newline\\n") || line.contains("\\nnewline"));
        assert!(line.contains("\\u2028") && line.contains("\\u2029"), "line terminators escape");
        assert!(line.contains("\\u0001"), "other C0 controls use \\u00XX");
        assert_eq!(parse_line(&line).unwrap(), e, "and it round-trips");
    }

    /// Strict reading, item by item from §8.2.1's own list. What would make
    /// this fail: accepting any second spelling of one entry — which is worse
    /// than refusing a good one, because `chain` and signatures are over these
    /// bytes and a second spelling is a second identity.
    #[test]
    fn non_canonical_lines_are_refused() {
        let good = entry().encode();
        assert!(parse_line(&good).is_ok(), "the control must parse");
        let cases = [
            good.replace("{\"seq\"", "{ \"seq\""),               // added whitespace
            good.replace("\"name\":", "\"nombre\":"),            // unknown member
            format!("{good} "),                                  // trailing content
            format!("{good}{{}}"),                               // trailing object
            good.replace("\"author\":\"claude-main\"", "\"author\":\"claude-main\",\"author\":\"x\""),
            good.replace("\"kind\":\"func\",\"status\"", "\"status\":\"accepted\",\"kind\"").replace(":\"accepted\",\"hash\"", ":\"func\",\"hash\""),
            good.replace("\"seq\":1", "\"seq\":01"),
            good.replace("/", "\\/"),                            // escaped solidus
        ];
        for bad in cases {
            assert!(parse_line(&bad).is_err(), "must refuse: {bad}");
        }
    }

    /// §8.4's signed content excludes exactly the store-assigned fields. The
    /// `applied_via` exclusion is the one with teeth: an author cannot know
    /// which mechanism a store will use, so including it would make every
    /// honest signed entry fail against its own record.
    #[test]
    fn the_signed_content_excludes_what_the_store_assigns() {
        let base = Entry { pubkey: "ab".repeat(32), ..entry() };
        let with_store_fields = Entry {
            seq: 99,
            time: "2026-09-09T09:09:09Z".into(),
            verifier: "other-kernel/9".into(),
            applied_via: "transactional-cas".into(),
            chain: "cd".repeat(32),
            sig: "ef".repeat(64),
            ..base.clone()
        };
        assert_eq!(
            base.signed_content(),
            with_store_fields.signed_content(),
            "none of the store-assigned fields may enter the signed content"
        );
        // The control: an AUTHORED field must change it, or the signature binds
        // nothing.
        let moved = Entry { name: "other".into(), ..base.clone() };
        assert_ne!(base.signed_content(), moved.signed_content());
        assert!(base.signed_content().contains("\"pubkey\""), "pubkey IS signed");
    }

    /// §8.6.3/§8.6.5: `applied_via`'s four states, and that no signature covers
    /// it. The failure this guards is specific — reading absence as `none`
    /// fabricates a claim of weakness on behalf of every entry written before
    /// the member existed.
    #[test]
    fn applied_via_has_four_states_and_is_never_signed() {
        assert_eq!(Entry::default().applied_via_claim(), AppliedVia::Unrecorded);
        for (text, want) in [
            ("none", AppliedVia::None_),
            ("advisory-lock", AppliedVia::AdvisoryLock),
            ("transactional-cas", AppliedVia::TransactionalCas),
            ("some-future-thing", AppliedVia::Unrecognised("some-future-thing".into())),
        ] {
            let e = Entry { applied_via: text.into(), ..entry() };
            assert_eq!(e.applied_via_claim(), want);
            // §8.6.5: "No signature ever covers it, on any entry."
            assert!(!e.signed_content().contains("applied_via"), "{text} leaked into the signature");
            // The chain DOES cover it, since the chain is over the whole entry.
            assert!(e.chain_input().contains("applied_via"), "{text} must be chained");
        }
        assert_ne!(AppliedVia::Unrecorded, AppliedVia::None_);
    }

    /// §8.2.2: the digest identifies a PUBLICATION, and fixes position only
    /// where the entry is chained.
    #[test]
    fn the_entry_digest_fixes_position_only_when_chained() {
        let a = entry();
        let b = entry();
        assert_eq!(a.digest(), b.digest(), "identical unchained entries share a digest");
        assert!(!a.digest_fixes_position());
        let chained = Entry { chain: "ab".repeat(32), ..entry() };
        assert!(chained.digest_fixes_position());
        assert_ne!(chained.digest(), a.digest());
    }

    // -- §8.6.2 ------------------------------------------------------------

    fn put(seq: u64, name: &str, hash: &str, status: &str) -> Entry {
        Entry {
            seq,
            name: name.into(),
            kind: "func".into(),
            status: status.into(),
            hash: hash.into(),
            ..Entry::default()
        }
    }

    /// §8.6.2's transition table, row by row.
    #[test]
    fn the_revision_counts_states_and_not_publications() {
        let a = "a".repeat(64);
        let b = "b".repeat(64);
        let log = vec![
            put(1, "n", &a, "accepted"),   // (none) -> A : applied, 0 -> 1
            put(2, "n", &a, "accepted"),   // A -> A      : unchanged, stays 1
            put(3, "n", &b, "accepted"),   // A -> B      : applied, 1 -> 2
            put(4, "n", &a, "accepted"),   // B -> A      : applied, 2 -> 3
            Entry { kind: "prove".into(), ..put(5, "n", &a, "accepted") }, // no name op
            put(6, "n", &b, "rejected"),   // not accepted/falsified
        ];
        let d = derive(&log);
        let want = [
            Transition::Applied,
            Transition::Unchanged,
            Transition::Applied,
            Transition::Applied,
            Transition::None,
            Transition::None,
        ];
        for (i, w) in want.iter().enumerate() {
            assert_eq!(d[i].transition, *w, "entry {}", i + 1);
        }
        let revs: Vec<u32> = d.iter().map(|x| x.revision_before.to_string().parse().unwrap()).collect();
        assert_eq!(revs, vec![0, 1, 1, 2, 3, 3]);
        // ABA: the second A is revision 2, not 0 — so an envelope signed
        // against the FIRST A carries the wrong revision even though the hash
        // matches again. That is the whole of the replay protection.
        assert_eq!(revs[3], 2);
    }

    /// A `falsified` definition still binds its name; a `prove` or `cross`
    /// entry never does, whatever its status.
    #[test]
    fn status_and_kind_are_different_dimensions() {
        let a = "a".repeat(64);
        let log = vec![
            put(1, "n", &a, "falsified"),
            Entry { kind: "cross".into(), ..put(2, "n", &"c".repeat(64), "accepted") },
        ];
        let d = derive(&log);
        assert_eq!(d[0].transition, Transition::Applied, "falsified still binds");
        assert_eq!(d[1].transition, Transition::None, "a cross entry touches no name");
    }

    /// The fold's reason for existing: a legacy no-op carries NO `prev`, so the
    /// per-entry test "`prev` equals `hash`" misses every one of them. Here the
    /// fold catches the no-op with no `prev` present at all.
    #[test]
    fn a_legacy_no_op_has_no_prev_and_is_still_found() {
        let a = "a".repeat(64);
        let log = vec![put(1, "n", &a, "accepted"), put(2, "n", &a, "accepted")];
        assert!(log[1].prev.is_empty(), "the premise: a legacy no-op carries no prev");
        let d = derive(&log);
        assert_eq!(d[1].transition, Transition::Unchanged);
        assert_eq!(d[1].revision_before.to_string(), "1", "a no-op does not advance it");
    }

    #[test]
    fn revisions_are_per_name() {
        let a = "a".repeat(64);
        let log = vec![
            put(1, "x", &a, "accepted"),
            put(2, "y", &a, "accepted"),
            put(3, "x", &"b".repeat(64), "accepted"),
        ];
        let d = derive(&log);
        assert_eq!(d[2].revision_before.to_string(), "1", "y's publication is not x's revision");
    }

    // -- §8 chain ----------------------------------------------------------

    #[test]
    fn the_chain_seals_ordering() {
        let mut e1 = Entry { seq: 1, ..entry() };
        e1.chain = chain_value(&e1, None, b"");
        let mut e2 = Entry { seq: 2, name: "triple".into(), ..entry() };
        e2.chain = chain_value(&e2, Some(&e1.chain), b"");
        let lines = vec![e1.encode(), e2.encode()];
        assert!(verify_journal(&lines).is_ok(), "an honestly chained journal verifies");

        // Re-chaining is not enough to hide a reorder: swapping the two lines
        // breaks both the sequence and the chain.
        let swapped = vec![lines[1].clone(), lines[0].clone()];
        assert!(verify_journal(&swapped).is_err());

        // A tampered field breaks the chain even with seq intact.
        let mut tampered = e2.clone();
        tampered.hash = "9".repeat(64);
        assert!(verify_journal(&[lines[0].clone(), tampered.encode()]).is_err());
    }

    #[test]
    fn an_unchained_entry_after_a_chained_one_is_refused() {
        let mut e1 = Entry { seq: 1, ..entry() };
        e1.chain = chain_value(&e1, None, b"");
        let e2 = Entry { seq: 2, ..entry() };
        assert!(verify_journal(&[e1.encode(), e2.encode()]).is_err());
    }

    #[test]
    fn a_seq_gap_or_reorder_is_refused() {
        let ok = [Entry { seq: 1, ..entry() }.encode(), Entry { seq: 2, ..entry() }.encode()];
        assert!(verify_journal(&ok).is_ok(), "the control");
        for bad in [(1, 3), (2, 1), (0, 1)] {
            let lines = [
                Entry { seq: bad.0, ..entry() }.encode(),
                Entry { seq: bad.1, ..entry() }.encode(),
            ];
            assert!(verify_journal(&lines).is_err(), "seq {bad:?} must be refused");
        }
    }

    // -- §8.6.4, the three clauses §10.1 records as unwitnessed -------------
    //
    // §10.1 is explicit that the vector suite cannot reach these: clause 1
    // needs an entry with a PARTIAL field set, clause 3 is distinguishable from
    // clause 4 only when a signature is valid under `author_pubkey` while the
    // envelope names someone else, and the file holds exactly one journal line.
    // These are constructed here instead. That is weaker evidence in the way
    // §10.1 means — a witness built by the implementation it guards — and it is
    // strictly better than the alternative, which was measured: each of these
    // rules could be DELETED with the whole suite still passing.

    fn signed_entry(seed: [u8; 32], env: crate::envelope::Envelope) -> Entry {
        let octets = env.encode().expect("a well-formed envelope");
        let sig = crate::ed25519::sign(&seed, &octets);
        Entry {
            hash: env.artifact.clone(),
            prev: match &env.parent {
                Parent::Absent => String::new(),
                Parent::Hash(h) => h.clone(),
            },
            envelope_b64: envelope_member(&octets),
            author_pubkey: env.author.clone(),
            author_sig: hex_member(&sig),
            name_transition: "applied".into(),
            name: env.name.clone(),
            ..entry()
        }
    }

    fn an_envelope(author_hex: &str, name: &str) -> crate::envelope::Envelope {
        crate::envelope::Envelope {
            version: crate::envelope::Version::V2,
            op: "put".into(),
            name: name.into(),
            artifact: "5".repeat(64),
            parent: Parent::Absent,
            parent_rev: BigUint::from(0u32),
            author: author_hex.into(),
            license: crate::envelope::License::NoneAsserted,
        }
    }

    fn applied() -> Derived {
        Derived {
            transition: Transition::Applied,
            revision_before: BigUint::from(0u32),
            reconstructed: false,
        }
    }

    /// **ENV-VERIFY-PRESENT.** Any one of the three alone attests to nothing.
    /// The failure direction is what makes this matter: a presence check that
    /// looks for the wrong member name finds it always absent, concludes no
    /// obligation applies, and accepts every forged publication.
    #[test]
    fn a_partial_envelope_field_set_is_refused() {
        let seed = [3u8; 32];
        let pk = hex_member(&crate::ed25519::public_key(&seed));
        let full = signed_entry(seed, an_envelope(&pk, "double"));
        assert!(verify_entry(&full, &applied()).is_ok(), "the control must verify");

        for drop in ["envelope_b64", "author_pubkey", "author_sig"] {
            let mut partial = full.clone();
            partial.set_member(drop, String::new());
            let err = verify_entry(&partial, &applied())
                .expect_err(&format!("dropping {drop} must fail, not silently excuse the entry"));
            assert!(err.contains("ENV-VERIFY-PRESENT"), "{err}");
        }
    }

    /// **ENV-VERIFY-AUTHOR**, distinguished from ENV-VERIFY-SIGNATURE. The
    /// signature here IS valid under `author_pubkey`; what fails is that the
    /// envelope names somebody else. A verifier checking only clause 4 accepts
    /// it, which is how a statement made by one party gets recorded as
    /// another's.
    #[test]
    fn a_signature_valid_under_the_wrong_author_is_refused() {
        let signer = [4u8; 32];
        let named = [5u8; 32];
        let signer_hex = hex_member(&crate::ed25519::public_key(&signer));
        let named_hex = hex_member(&crate::ed25519::public_key(&named));
        assert_ne!(signer_hex, named_hex);

        // The envelope NAMES `named`, but `signer` signed it, and the entry
        // records `signer` as the key.
        let env = an_envelope(&named_hex, "double");
        let octets = env.encode().unwrap();
        let entry = Entry {
            author_pubkey: signer_hex.clone(),
            ..signed_entry(signer, env)
        };
        // The control: the signature really is valid under the recorded key, so
        // clause 4 alone would pass this entry.
        let key = unhex_lower(&signer_hex).unwrap();
        let sig = unhex_lower(&entry.author_sig).unwrap();
        assert!(crate::ed25519::verify(&key, &octets, &sig).is_ok(), "the control");

        let err = verify_entry(&entry, &applied()).expect_err("clause 3 must refuse this");
        assert!(err.contains("ENV-VERIFY-AUTHOR"), "{err}");
    }

    /// **ENV-VERIFY-DERIVED-TRANSITION.** The stored member is the store's
    /// computation, not the publisher's claim; where it disagrees with history
    /// the entry fails. The envelope here AGREES with the entry, so clause 5 is
    /// satisfied and the disagreement is the only thing left to catch it.
    #[test]
    fn a_stored_transition_disagreeing_with_history_is_refused() {
        let seed = [6u8; 32];
        let pk = hex_member(&crate::ed25519::public_key(&seed));
        let honest = signed_entry(seed, an_envelope(&pk, "double"));
        assert!(verify_entry(&honest, &applied()).is_ok(), "the control");

        for stated in ["unchanged", "none"] {
            let mislabelled = Entry { name_transition: stated.into(), ..honest.clone() };
            let err = verify_entry(&mislabelled, &applied())
                .expect_err("a mislabelled transition must fail verification");
            assert!(err.contains("ENV-VERIFY-DERIVED-TRANSITION"), "{err}");
        }
    }

    /// **ENV-VERIFY-REVISION**, both halves: the entry must agree with its
    /// envelope AND with the revision history derives. An entry that predates
    /// the member is verified WITHOUT the check and reported as unavailable —
    /// which is a different fact from "it matched".
    #[test]
    fn the_persisted_revision_is_checked_against_the_envelope_and_history() {
        let seed = [8u8; 32];
        let pk = hex_member(&crate::ed25519::public_key(&seed));
        let mut env = an_envelope(&pk, "double");
        env.parent = Parent::Hash("6".repeat(64));
        env.parent_rev = BigUint::from(7u32);
        let entry = Entry { parent_rev: "7".into(), ..signed_entry(seed, env) };

        let at_seven = Derived { revision_before: BigUint::from(7u32), ..applied() };
        let report = verify_entry(&entry, &at_seven).expect("the control");
        assert_eq!(report.revision_checked, Some(BigUint::from(7u32)));

        // History says 3: this is the ABA case, where parent, name and artifact
        // are all valid again and only the revision exposes the replay.
        let at_three = Derived { revision_before: BigUint::from(3u32), ..applied() };
        let err = verify_entry(&entry, &at_three).expect_err("a stale revision must fail");
        assert!(err.contains("ENV-VERIFY-REVISION"), "{err}");

        // The entry disagreeing with its own envelope.
        let lying = Entry { parent_rev: "8".into(), ..entry.clone() };
        assert!(verify_entry(&lying, &Derived { revision_before: BigUint::from(8u32), ..applied() })
            .is_err());

        // Absent: verified without the check, and REPORTED as unavailable.
        let legacy = Entry { parent_rev: String::new(), ..entry };
        let report = verify_entry(&legacy, &at_three).expect("no member, no obligation");
        assert_eq!(report.revision_checked, None, "unavailable is not `it matched`");
    }

    /// **ENV-VERIFY-DECODE** and **ENV-VERIFY-SIGNATURE** as they apply to an
    /// ENTRY, which is the path the vectors never take: their `signature`
    /// records carry the three members loose, so a verifier could check them
    /// there and check nothing on the journal.
    ///
    /// The signature is over the octets DECODED FROM `envelope_b64` — never
    /// over the base64 text and never over a re-encoding of the parsed
    /// envelope. The last is the one that fails silently: a re-encoding is
    /// byte-identical for every well-formed envelope, so signing over it works
    /// until the day it does not.
    #[test]
    fn the_stored_envelope_must_be_canonical_base64_and_its_signature_must_verify() {
        let seed = [11u8; 32];
        let pk = hex_member(&crate::ed25519::public_key(&seed));
        let honest = signed_entry(seed, an_envelope(&pk, "double"));
        assert!(verify_entry(&honest, &applied()).is_ok(), "the control");

        // A second spelling of the same octets: §8.6.3 refuses it even though
        // it decodes to exactly the right bytes.
        let padded = Entry {
            envelope_b64: format!(" {}", honest.envelope_b64),
            ..honest.clone()
        };
        let err = verify_entry(&padded, &applied()).expect_err("non-canonical base64 must fail");
        assert!(err.contains("ENV-VERIFY-DECODE"), "{err}");

        // A signature over the right message but from the wrong key.
        let other = crate::ed25519::sign(&[12u8; 32], &base64::decode_canonical(&honest.envelope_b64).unwrap());
        let forged = Entry { author_sig: hex_member(&other), ..honest.clone() };
        let err = verify_entry(&forged, &applied()).expect_err("a bad signature must fail");
        assert!(err.contains("ENV-VERIFY-SIGNATURE"), "{err}");

        // A signature over the BASE64 TEXT rather than the octets — the exact
        // mistake clause 4 names.
        let wrong_message = crate::ed25519::sign(&seed, honest.envelope_b64.as_bytes());
        let over_text = Entry { author_sig: hex_member(&wrong_message), ..honest };
        assert!(
            verify_entry(&over_text, &applied()).is_err(),
            "a signature over the base64 text is not a signature over the statement"
        );
    }

    /// §8.4: an entry carrying a `pubkey` with no valid signature — or the
    /// reverse — is a FORGED ATTRIBUTION and must be rejected, never treated as
    /// unattributed.
    #[test]
    fn a_half_signed_entry_is_a_forgery_and_not_an_unattributed_one() {
        let seed = [2u8; 32];
        let pk = crate::ed25519::public_key(&seed);
        let mut e = Entry { pubkey: hex_member(&pk), ..entry() };
        e.sig = hex_member(&crate::ed25519::sign(&seed, e.signed_content().as_bytes()));
        let d = Derived {
            transition: Transition::Applied,
            revision_before: BigUint::from(0u32),
            reconstructed: false,
        };
        assert!(verify_entry(&e, &d).expect("the control").entry_signature_verified);

        let no_sig = Entry { sig: String::new(), ..e.clone() };
        assert!(verify_entry(&no_sig, &d).is_err(), "a pubkey with no signature is a forgery");
        let no_key = Entry { pubkey: String::new(), ..e.clone() };
        assert!(verify_entry(&no_key, &d).is_err(), "a signature with no key is a forgery");
        let unattributed = Entry { pubkey: String::new(), sig: String::new(), ..e };
        assert!(
            !verify_entry(&unattributed, &d).expect("unattributed is conformant").entry_signature_verified
        );
    }
}
