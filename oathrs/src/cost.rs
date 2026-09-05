//! SPEC §7.5 — PER-ATTEMPT COST EMISSION (OPTIONAL, pinned once offered).
//!
//! A kernel offering sharded verification MAY additionally emit a COST RECORD
//! per §7.2 PROPERTY-PROOF attempt. This module is that emission: the record
//! shape, its one-JSON-object-per-line wire format, and the sink that writes it.
//!
//! SCOPE. The emission covers §7.2 property-proof attempts and nothing else.
//! §6.1.1's termination-measure search also reaches the solver, per DEFINITION
//! and with no property index to key a record to; §7.5 puts those calls OUT of
//! the emission, so this is the cost of PROVING PROPERTIES and not the whole
//! solver cost of a run. `prove.rs` enforces that structurally: every attempt
//! that emits goes through `Prover::attempt`, and the measure search calls
//! `run_z3` directly.
//!
//! WHAT IS PORTABLE, AND WHAT IS NOT (§7.5's own division). The FRAMING is
//! fixed, so any consumer can mechanically read any kernel's emission: the
//! encoding, the line discipline, the member names, the types. The VALUES of
//! `strategy` and `detail` are KERNEL-LOCAL and OPAQUE; §10 does not compare
//! this emission across kernels at all. The portable key is `(hash, prop)`, and
//! nothing here — code, test or comment — may join on a label.
//!
//! WHAT THE SECTION BINDS, and how each obligation is discharged here:
//!
//!   * "UTF-8 text, ONE JSON object per line, each terminated by a single LF
//!     (`0x0A`)" — [`encode_record`] returns exactly one line, LF-terminated,
//!     with no CR anywhere and no interior LF (every string member is JSON-
//!     escaped, so a newline inside a value becomes `\n` and cannot split the
//!     record across lines).
//!   * "Records are written to a destination DISTINCT from the shard result" —
//!     the sink is a separate stream, opened by the caller. The shard result
//!     goes to stdout; the CLI writes cost records to the `--cost-out` file.
//!     Neither consumer ever parses the other's bytes.
//!   * "Each object MUST carry AT LEAST these members, and MAY carry others" —
//!     the eight members are always present, in §7.5's own listing order. The
//!     OPTIONAL `wall_ms` is emitted last and is CLEARLY DISTINGUISHED by name;
//!     §7.5 permits reporting wall-clock additionally but forbids requiring it,
//!     so no consumer here reads it and its absence changes nothing.
//!   * "Unknown members MUST be ignored by a consumer" — [`parse_record`] reads
//!     the eight it knows and silently skips every other member.
//!   * "A record MUST be complete on its line: a consumer reads whole lines and
//!     never reassembles an object across them" — [`read_prefix`] parses line by
//!     line and never joins two lines.
//!   * "A record MUST be written and flushed BEFORE the attempt that follows it
//!     begins" — [`CostSink::record`] writes and flushes in the same call,
//!     which is strictly stronger: the flush happens before the caller returns,
//!     hence before any later attempt starts. Nothing is buffered to the end of
//!     a shard.
//!   * "a consumer MUST treat a trailing partial line as absent rather than as
//!     corrupt" — [`read_prefix`] drops a final line with no LF terminator and
//!     reports it as `truncated`, not as an error.
//!   * "Consumed resource is REPORTED, never compared … A kernel MUST NOT admit
//!     it into a campaign identity, a proof outcome, or a merge decision" — this
//!     module has no path into any of those: it is write-only from the prover's
//!     side, and `prove.rs` discards the record after handing it to the sink.
//!
//! WHERE §7.5 DID NOT DETERMINE THE ANSWER, the choice is recorded at the point
//! it is made (see also REPORT.md at the root of this tree). The largest is the
//! DESTINATION, which §7.5 constrains only by requiring it to be distinct from
//! the shard result. The `strategy`/`detail` VOCABULARY is NOT one of them: §7.5
//! declares those values opaque and kernel-chosen, so inventing them is what the
//! section asks for rather than a gap it left — see [`strategy`].

use std::io::Write;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;

/// The §7.5 strategy labels THIS kernel uses.
///
/// §7.5 makes `strategy` "an OPAQUE, kernel-chosen label" and `detail` "an
/// OPAQUE, kernel-chosen discriminator within that strategy". There is no shared
/// vocabulary to match and the section says why it declines to create one: §7.2
/// keeps its labels deliberately unspecified, and "a shared vocabulary here
/// would contradict that directly".
///
/// So these names are LOCAL. §7.5 imposes exactly two requirements on them, and
/// nothing else is claimed for the constants below:
///
///   * DISTINGUISH — the values separate a property's attempts from one
///     another. Held here by giving each script-emitting site of the strategy
///     sequence its own name, by discriminating a site's subgoals in `detail`,
///     and (in the iterating driver only) by the `retry=<n>` pass index.
///   * STABLE within the kernel — the same inputs produce the same labels, which
///     is what makes this kernel's emission joinable to this kernel's scripts.
///
/// "Two kernels' labels are NOT comparable and a consumer MUST NOT join on
/// them; the portable key is `(hash, prop)`." Nothing outside this kernel should
/// read the constants below, and no test here asserts that another kernel would
/// choose them.
///
/// One name per distinct script-emitting site of §7.2's strategy sequence, in
/// the order the sequence attempts them.
pub mod strategy {
    /// §7.2 #53, the lemma-free first attempt. Detail: `""`.
    pub const LEMMA_FREE: &str = "lemma-free";
    /// Direct proof. Detail: `""`.
    pub const DIRECT: &str = "direct";
    /// §7.2 #50, the full-budget direct retry after induction failed. A separate
    /// strategy name rather than a `direct` record with a different `budget`,
    /// because the two attempts are distinguishable in the sequence and a
    /// consumer that keyed only on (strategy, detail) would otherwise merge
    /// them.
    pub const DIRECT_FALLBACK: &str = "direct-fallback";
    /// §7.2 deterministic instantiation, one constructor subgoal of structural
    /// induction with ground defining-equation instances.
    /// Detail: `binder=<k>,ctor=<c>`.
    pub const INSTANTIATION: &str = "instantiation";
    /// Single-binder structural induction, one constructor subgoal.
    /// Detail: `binder=<k>,ctor=<c>`.
    pub const INDUCTION: &str = "induction";
    /// Lexicographic induction, one subgoal.
    /// Detail: `i=<i>,j=<j>,ctor=<c>` (base case) or
    /// `i=<i>,j=<j>,ctor=<c>,ctor2=<c'>` (split case).
    pub const LEX: &str = "lex";
    /// Recursion induction (§7.2 #56/#57), the BASE obligation. Detail: `""`.
    pub const RECURSION_BASE: &str = "recursion-base";
    /// Recursion induction, one guard-group STEP obligation.
    /// Detail: `group=<index>`.
    pub const RECURSION_STEP: &str = "recursion-step";
}

/// One §7.5 cost record: the diagnostic account of a single solver attempt.
///
/// The field ORDER is §7.5's listing order and is the order [`encode_record`]
/// emits. Nothing downstream depends on member order (JSON objects are
/// unordered), but keeping it makes the record readable against the section.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CostRecord {
    /// "the definition's identity hash" (§1, 64 lowercase hex).
    pub hash: String,
    /// "the property index within that definition".
    pub prop: usize,
    /// "an OPAQUE, kernel-chosen label for the §7.2 strategy that emitted the
    /// attempt" — see [`strategy`]. Not comparable to another kernel's labels.
    pub strategy: String,
    /// "an OPAQUE, kernel-chosen discriminator within that strategy, `""` when
    /// it has none". Also not comparable across kernels.
    pub detail: String,
    /// "the effective rlimit for THIS attempt, which is not always the run's
    /// nominal budget" — the reduced-budget attempts carry their own.
    pub budget: u64,
    /// "the solver's reported resource counter, or NULL when and only when the
    /// solver reported none."
    ///
    /// §7.5: "This is INDEPENDENT of `invalid` in both directions … A producer
    /// MUST NOT derive either member from the other." Both crossings are real
    /// here: `memout` and a below-budget `canceled` are ABORTS that exit
    /// normally WITH a counter, and a PROVED goal (`unsat`) carries its cost
    /// like any other attempt. `None` means only that no counter was reported —
    /// a spawn failure, a wall-cap kill, a crash mid-attempt.
    pub consumed: Option<u64>,
    /// "true iff the attempt was an abort (§7.2 #72)". An abort is NOT an
    /// outcome.
    pub invalid: bool,
    /// `"unsat" | "sat" | "unknown"` when `invalid` is false, and NULL when it
    /// is true. §7.5 forbids substituting `"unknown"` for an abort, which is a
    /// real solver answer; the type makes that substitution impossible to write
    /// by accident.
    pub verdict: Option<String>,
    /// OPTIONAL extra member (§7.5: "Wall-clock time MUST NOT be required. A
    /// kernel MAY report it additionally, clearly distinguished"). Emitted as
    /// `wall_ms` when present, omitted otherwise. No consumer obligation
    /// attaches to it.
    pub wall_ms: Option<u64>,
}

impl CostRecord {
    /// §7.5's own consistency rule between `invalid` and `verdict`, checkable:
    /// a valid attempt carries exactly one of the three solver answers, and an
    /// aborted one carries none.
    ///
    /// It says NOTHING about `consumed`, deliberately. §7.5 couples `invalid`
    /// to `verdict` and declares `invalid` and `consumed` INDEPENDENT in both
    /// directions, so a well-formedness check that also constrained `consumed`
    /// would reject records the section requires a producer to write.
    pub fn well_formed(&self) -> bool {
        match (self.invalid, self.verdict.as_deref()) {
            (false, Some("unsat")) | (false, Some("sat")) | (false, Some("unknown")) => true,
            (true, None) => true,
            _ => false,
        }
    }
}

/// Encode one record as §7.5's wire format: a single JSON object, UTF-8,
/// terminated by exactly one LF.
///
/// Every string member is escaped, so no value can inject a line break or an
/// unbalanced quote and split the record — which is what makes "a record MUST be
/// complete on its line" hold for arbitrary strategy details and abort reasons.
pub fn encode_record(r: &CostRecord) -> String {
    let mut s = String::with_capacity(192);
    s.push('{');
    push_str_member(&mut s, "hash", &r.hash, true);
    s.push_str(&format!(",\"prop\":{}", r.prop));
    push_str_member(&mut s, "strategy", &r.strategy, false);
    push_str_member(&mut s, "detail", &r.detail, false);
    s.push_str(&format!(",\"budget\":{}", r.budget));
    match r.consumed {
        Some(c) => s.push_str(&format!(",\"consumed\":{}", c)),
        None => s.push_str(",\"consumed\":null"),
    }
    s.push_str(&format!(",\"invalid\":{}", r.invalid));
    match &r.verdict {
        Some(v) => push_str_member(&mut s, "verdict", v, false),
        None => s.push_str(",\"verdict\":null"),
    }
    // The OPTIONAL wall-clock member, last and clearly named (§7.5).
    if let Some(ms) = r.wall_ms {
        s.push_str(&format!(",\"wall_ms\":{}", ms));
    }
    s.push('}');
    s.push('\n');
    s
}

fn push_str_member(out: &mut String, key: &str, value: &str, first: bool) {
    if !first {
        out.push(',');
    }
    out.push('"');
    out.push_str(key);
    out.push_str("\":");
    push_json_string(out, value);
}

/// A JSON string literal. Escapes `"`, `\` and every C0 control character —
/// notably LF and CR, which is the escape the line framing depends on.
fn push_json_string(out: &mut String, s: &str) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{08}' => out.push_str("\\b"),
            '\u{0c}' => out.push_str("\\f"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
}

/// The §7.5 emission sink: an open destination, DISTINCT from the shard result,
/// that a record is written and flushed to as soon as it is produced.
///
/// "Records MUST be emitted as they are produced, not assembled at the end … a
/// kernel MUST NOT buffer records to the end of a shard." Hence no internal
/// buffer: [`record`](CostSink::record) writes the line and flushes before it
/// returns.
///
/// The `Mutex` is not for parallelism inside this kernel (the shard driver is
/// serial); it makes the sink shareable behind `&` so the prover can hold a
/// borrow, and it keeps a line indivisible if a future driver runs shards in
/// threads of one process.
pub struct CostSink {
    out: Mutex<Box<dyn Write + Send>>,
    /// Records handed to this sink. Diagnostic only — nothing in the verifier
    /// reads it.
    written: Mutex<u64>,
    /// POISONED once any write fails. A failed `write_all` may have written PART
    /// of a line, and §7.5's guarantee is that every complete line in the file is
    /// a complete record. Continuing would let a later record's LF close the
    /// partial one into a single malformed line that `read_prefix` accepts —
    /// turning a recoverable truncation into a corrupt file, which is the exact
    /// property the format exists to provide. Stopping leaves a trailing partial
    /// line, which a consumer is required to treat as absent.
    poisoned: AtomicBool,
}

impl CostSink {
    /// A sink over any writable destination.
    pub fn new(out: Box<dyn Write + Send>) -> CostSink {
        CostSink { out: Mutex::new(out), written: Mutex::new(0), poisoned: AtomicBool::new(false) }
    }

    /// A sink over a file, created or TRUNCATED. Truncation is deliberate: a
    /// consumer reading a valid prefix of a killed run's emission must not find
    /// a previous run's records appended below it.
    /// Opens WITHOUT truncating, so the caller can verify what it actually got
    /// before any data is destroyed. `File::create` truncates as it opens, which
    /// makes every guard checked beforehand a TOCTOU: the path can be replaced,
    /// or a symlink repointed, between the check and the open, and the truncation
    /// has already happened by the time anything could notice. Truncation is the
    /// caller's separate, later step — see `truncate`.
    pub fn open_untruncated(path: &str) -> std::io::Result<std::fs::File> {
        std::fs::OpenOptions::new().read(true).write(true).create(true).open(path)
    }

    /// Takes an ALREADY-VERIFIED handle, truncates it, and wraps it.
    ///
    /// Truncating the descriptor rather than re-opening the path is what closes
    /// the race: whatever the name resolves to now, this empties the file the
    /// caller inspected.
    pub fn from_verified(f: std::fs::File) -> std::io::Result<CostSink> {
        f.set_len(0)?;
        Ok(CostSink::new(Box::new(f)))
    }

    /// Write one record and FLUSH it (§7.5). Emission is a diagnostic: an I/O
    /// failure is reported to stderr and otherwise ignored, because "no verdict,
    /// campaign identity, merge result or conformance outcome may depend on the
    /// emission or its absence" — failing the run here would make the verdict
    /// depend on it.
    pub fn record(&self, r: &CostRecord) {
        debug_assert!(r.well_formed(), "§7.5: invalid/verdict disagree: {:?}", r);
        if self.poisoned.load(Ordering::Relaxed) {
            return; // a previous write may have left a partial line; see `poisoned`
        }
        let line = encode_record(r);
        if let Ok(mut w) = self.out.lock() {
            if let Err(e) = w.write_all(line.as_bytes()).and_then(|()| w.flush()) {
                self.poisoned.store(true, Ordering::Relaxed);
                // NOT eprintln!: it panics on a closed stderr, which would turn a
                // failed DIAGNOSTIC into a failed run — the one thing §7.5 says
                // the emission must never do.
                let _ = writeln!(std::io::stderr(),
                    "warning: §7.5 cost emission could not be written ({}) — no further records will be emitted, and the run is unaffected", e);
                return;
            }
        }
        if let Ok(mut n) = self.written.lock() {
            *n += 1;
        }
    }

    /// How many records this sink has written. For tests and human reporting.
    pub fn written(&self) -> u64 {
        self.written.lock().map(|n| *n).unwrap_or(0)
    }
}

/// The result of reading an emission: the records of every COMPLETE line, plus
/// whether a trailing PARTIAL line was dropped.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Prefix {
    pub records: Vec<CostRecord>,
    /// True iff the input ended without an LF, i.e. the last line was partial
    /// and was treated as ABSENT (§7.5), not as corrupt.
    pub truncated: bool,
}

/// Read a valid PREFIX of an emission (§7.5): every complete, LF-terminated line
/// is parsed as a record; a trailing partial line is DROPPED and reported. This
/// is the consumer side the section describes — a run killed at any instant
/// leaves a file this function reads without error.
///
/// A complete line that does not parse IS an error: the flush rule makes a
/// complete line a complete record, so a malformed one is a real defect and must
/// not be silently skipped.
/// The BYTES entry point, and the one a consumer of a possibly-killed run wants.
///
/// A process killed mid-write can stop inside a MULTIBYTE CHARACTER, leaving a
/// file that is not valid UTF-8 as a whole. A `&str` API cannot express that: the
/// caller's `read_to_string` fails before any prefix logic runs, so the promised
/// recovery does not hold at arbitrary byte offsets — which is precisely the
/// case the guarantee exists for. Finding the last LF in the RAW BYTES and
/// decoding only up to it keeps the promise, because every complete line is
/// whole by construction and the undecodable tail is exactly the part a consumer
/// is required to treat as absent.
pub fn read_prefix_bytes(bytes: &[u8]) -> Result<Prefix, String> {
    let truncated = !bytes.is_empty() && bytes.last() != Some(&b'\n');
    let end = match bytes.iter().rposition(|b| *b == b'\n') {
        Some(i) => i + 1,
        None => return Ok(Prefix { records: Vec::new(), truncated }),
    };
    let text = std::str::from_utf8(&bytes[..end])
        .map_err(|e| format!("complete lines are not valid UTF-8: {}", e))?;
    let mut p = read_prefix(text)?;
    p.truncated = truncated;
    Ok(p)
}

pub fn read_prefix(text: &str) -> Result<Prefix, String> {
    let truncated = !text.is_empty() && !text.ends_with('\n');
    let mut records = Vec::new();
    // Only LF-terminated lines are complete. Split on LF and drop the tail after
    // the last one; never reassemble an object across lines.
    let complete: Vec<&str> = match text.rfind('\n') {
        Some(i) => text[..=i].lines().collect(),
        None => Vec::new(),
    };
    for (k, line) in complete.iter().enumerate() {
        // A BLANK LF-TERMINATED LINE IS A COMPLETE LINE, and §7.5 requires every
        // complete line to be one record. Skipping it would accept
        // `record\n\nrecord\n` as a clean prefix while this function promises to
        // reject malformed complete lines — so it is parsed like any other and
        // fails there.
        records.push(parse_record(line).map_err(|e| format!("line {}: {}", k + 1, e))?);
    }
    Ok(Prefix { records, truncated })
}

/// Parse one record line. UNKNOWN MEMBERS ARE IGNORED (§7.5), so a producer may
/// add fields without breaking this consumer.
///
/// A deliberately small JSON reader: the record is a flat object of strings,
/// integers, booleans and nulls, so nothing here recurses into arrays or nested
/// objects — it rejects them instead of half-understanding them.
pub fn parse_record(line: &str) -> Result<CostRecord, String> {
    let b: Vec<char> = line.trim().chars().collect();
    let mut i = 0usize;
    skip_ws(&b, &mut i);
    if b.get(i) != Some(&'{') {
        return Err("record must be a JSON object".into());
    }
    i += 1;
    let mut hash: Option<String> = None;
    let mut prop: Option<usize> = None;
    let mut strategy: Option<String> = None;
    let mut detail: Option<String> = None;
    let mut budget: Option<u64> = None;
    let mut consumed: Option<Option<u64>> = None;
    let mut invalid: Option<bool> = None;
    let mut verdict: Option<Option<String>> = None;
    let mut wall_ms: Option<u64> = None;
    loop {
        skip_ws(&b, &mut i);
        if b.get(i) == Some(&'}') {
            i += 1;
            break;
        }
        let key = read_string(&b, &mut i)?;
        skip_ws(&b, &mut i);
        if b.get(i) != Some(&':') {
            return Err(format!("member {:?} is missing its `:`", key));
        }
        i += 1;
        skip_ws(&b, &mut i);
        // UNKNOWN MEMBERS ARE SKIPPED WITHOUT BEING PARSED AS A RECORD VALUE.
        // §7.5 requires a consumer to ignore them, and the record's own value
        // grammar is narrow (strings, unsigned integers, booleans, null). A future
        // producer's extra member may hold an object, an array, a negative number
        // or a float — all legal JSON that read_value rejects — so deciding the
        // key is unknown BEFORE parsing is what makes the forward-compatibility
        // requirement real rather than nominal.
        const KNOWN: [&str; 9] = ["hash", "prop", "strategy", "detail", "budget",
                                  "consumed", "invalid", "verdict", "wall_ms"];
        if !KNOWN.contains(&key.as_str()) {
            skip_value(&b, &mut i)?;
            skip_ws(&b, &mut i);
            match b.get(i) {
                Some(',') => {
                    i += 1;
                    continue;
                }
                Some('}') => continue,
                _ => return Err("expected `,` or `}` after a member".into()),
            }
        }
        let value = read_value(&b, &mut i)?;
        match key.as_str() {
            "hash" => hash = Some(value.as_string()?),
            "prop" => prop = Some(value.as_u64()? as usize),
            "strategy" => strategy = Some(value.as_string()?),
            "detail" => detail = Some(value.as_string()?),
            "budget" => budget = Some(value.as_u64()?),
            "consumed" => consumed = Some(value.as_opt_u64()?),
            "invalid" => invalid = Some(value.as_bool()?),
            "verdict" => verdict = Some(value.as_opt_string()?),
            "wall_ms" => wall_ms = Some(value.as_u64()?),
            // §7.5: "Unknown members MUST be ignored by a consumer."
            _ => {}
        }
        skip_ws(&b, &mut i);
        match b.get(i) {
            Some(',') => {
                i += 1;
                // A comma PROMISES another member. Accepting `}` next would admit
                // `{"a":1,}`, which is not JSON — and §7.5 requires every complete
                // line to be one JSON object, so a malformed line must fail rather
                // than be handed on as cost data.
                skip_ws(&b, &mut i);
                if b.get(i) == Some(&'}') {
                    return Err("trailing comma before `}`".into());
                }
            }
            Some('}') => {}
            _ => return Err("expected `,` or `}` after a member".into()),
        }
    }
    skip_ws(&b, &mut i);
    if i != b.len() {
        return Err("trailing text after the record object".into());
    }
    let miss = |m: &str| format!("record is missing the required member `{}`", m);
    let rec = CostRecord {
        hash: hash.ok_or_else(|| miss("hash"))?,
        prop: prop.ok_or_else(|| miss("prop"))?,
        strategy: strategy.ok_or_else(|| miss("strategy"))?,
        detail: detail.ok_or_else(|| miss("detail"))?,
        budget: budget.ok_or_else(|| miss("budget"))?,
        consumed: consumed.ok_or_else(|| miss("consumed"))?,
        invalid: invalid.ok_or_else(|| miss("invalid"))?,
        verdict: verdict.ok_or_else(|| miss("verdict"))?,
        wall_ms,
    };
    // THE CONTRACT IS CHECKED HERE, not only when producing. §7.5 fixes `verdict`
    // to one of three strings when `invalid` is false and to null when it is true,
    // so `invalid:true, verdict:"unsat"` — or an arbitrary verdict string — is not
    // a record this consumer may hand on as valid cost data. A parser that only
    // checks SHAPE lets a malformed emission through as measurement.
    if !rec.well_formed() {
        return Err(format!(
            "§7.5: invalid={} with verdict={:?} — verdict MUST be null when invalid, and one of \
             \"unsat\"/\"sat\"/\"unknown\" otherwise",
            rec.invalid, rec.verdict));
    }
    Ok(rec)
}

enum JVal {
    Str(String),
    Num(u64),
    Bool(bool),
    Null,
}

impl JVal {
    fn as_string(self) -> Result<String, String> {
        match self {
            JVal::Str(s) => Ok(s),
            _ => Err("expected a string".into()),
        }
    }
    fn as_opt_string(self) -> Result<Option<String>, String> {
        match self {
            JVal::Str(s) => Ok(Some(s)),
            JVal::Null => Ok(None),
            _ => Err("expected a string or null".into()),
        }
    }
    fn as_u64(self) -> Result<u64, String> {
        match self {
            JVal::Num(n) => Ok(n),
            _ => Err("expected an integer".into()),
        }
    }
    fn as_opt_u64(self) -> Result<Option<u64>, String> {
        match self {
            JVal::Num(n) => Ok(Some(n)),
            JVal::Null => Ok(None),
            _ => Err("expected an integer or null".into()),
        }
    }
    fn as_bool(self) -> Result<bool, String> {
        match self {
            JVal::Bool(v) => Ok(v),
            _ => Err("expected a boolean".into()),
        }
    }
}

fn skip_ws(b: &[char], i: &mut usize) {
    while matches!(b.get(*i), Some(' ') | Some('\t')) {
        *i += 1;
    }
}

fn read_value(b: &[char], i: &mut usize) -> Result<JVal, String> {
    match b.get(*i) {
        Some('"') => Ok(JVal::Str(read_string(b, i)?)),
        Some('t') => {
            expect_word(b, i, "true")?;
            Ok(JVal::Bool(true))
        }
        Some('f') => {
            expect_word(b, i, "false")?;
            Ok(JVal::Bool(false))
        }
        Some('n') => {
            expect_word(b, i, "null")?;
            Ok(JVal::Null)
        }
        Some(c) if c.is_ascii_digit() => {
            let mut n: u64 = 0;
            while let Some(d) = b.get(*i).and_then(|c| c.to_digit(10)) {
                n = n.checked_mul(10).and_then(|x| x.checked_add(d as u64)).ok_or("integer overflows u64")?;
                *i += 1;
            }
            Ok(JVal::Num(n))
        }
        _ => Err("unsupported value (this record shape has only strings, unsigned integers, booleans and null)".into()),
    }
}

/// Skips any JSON value, for an UNKNOWN member only. Deliberately permissive
/// where read_value is strict: it accepts nested objects and arrays, negative and
/// fractional numbers, and exponents — everything §7.5's own record shape does not
/// use but a later producer's extra member legitimately might.
fn skip_value(b: &[char], i: &mut usize) -> Result<(), String> {
    skip_ws(b, i);
    match b.get(*i) {
        Some('"') => {
            read_string(b, i)?;
            Ok(())
        }
        Some('t') => expect_word(b, i, "true"),
        Some('f') => expect_word(b, i, "false"),
        Some('n') => expect_word(b, i, "null"),
        Some('{') | Some('[') => {
            // Depth-counted, and STRINGS ARE CONSUMED WHOLE: a brace or bracket
            // inside a string must not move the depth, or `{"k":"}"}` would end
            // the object early.
            let mut depth = 0usize;
            loop {
                match b.get(*i) {
                    Some('"') => {
                        read_string(b, i)?;
                        continue;
                    }
                    Some('{') | Some('[') => depth += 1,
                    Some('}') | Some(']') => {
                        depth -= 1;
                        if depth == 0 {
                            *i += 1;
                            return Ok(());
                        }
                    }
                    Some(_) => {}
                    None => return Err("unterminated object or array in an unknown member".into()),
                }
                *i += 1;
            }
        }
        Some(c) if c.is_ascii_digit() || *c == '-' => {
            *i += 1;
            let mut digits = c.is_ascii_digit();
            while let Some(c) = b.get(*i) {
                if c.is_ascii_digit() {
                    digits = true;
                    *i += 1;
                } else if matches!(c, '.' | 'e' | 'E' | '+' | '-') {
                    *i += 1;
                } else {
                    break;
                }
            }
            // IGNORING a member's MEANING is not the same as accepting arbitrary
            // bytes for it. §7.5 requires each complete line to be a JSON object,
            // so `"x":-` must fail the line rather than pass as an unknown value —
            // otherwise a corrupt emission is handed on as valid cost data.
            if digits { Ok(()) } else { Err("a number with no digits in an unknown member".into()) }
        }
        _ => Err("unsupported value in an unknown member".into()),
    }
}

fn expect_word(b: &[char], i: &mut usize, w: &str) -> Result<(), String> {
    for c in w.chars() {
        if b.get(*i) != Some(&c) {
            return Err(format!("expected `{}`", w));
        }
        *i += 1;
    }
    Ok(())
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
                match b.get(*i) {
                    Some('"') => s.push('"'),
                    Some('\\') => s.push('\\'),
                    Some('/') => s.push('/'),
                    Some('n') => s.push('\n'),
                    Some('r') => s.push('\r'),
                    Some('t') => s.push('\t'),
                    Some('b') => s.push('\u{08}'),
                    Some('f') => s.push('\u{0c}'),
                    Some('u') => {
                        let hex4 = |i: &mut usize| -> Result<u32, String> {
                            let mut code = 0u32;
                            for _ in 0..4 {
                                *i += 1;
                                let d = b.get(*i).and_then(|c| c.to_digit(16))
                                    .ok_or("bad \\u escape")?;
                                code = code * 16 + d;
                            }
                            Ok(code)
                        };
                        let hi = hex4(i)?;
                        // SURROGATE PAIRS. A char above U+FFFF is written as two
                        // \u escapes in JSON, and each half alone is not a scalar
                        // value — char::from_u32 rejects it. Decoding only the
                        // first would reject legal JSON that any other producer
                        // may emit in an unknown member or a future field.
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

#[cfg(test)]
mod tests {
    /// Ignoring an unknown member's MEANING does not license accepting non-JSON
    /// for it: the line must still be a JSON object.
    #[test]
    fn malformed_unknown_members_still_fail_the_line() {
        for bad in [r#""x":-"#, r#""x":{"#, r#""x":[1,"#] {
            let line = format!(
                r#"{{"hash":"h","prop":0,"strategy":"s","detail":"","budget":1,"consumed":1,"invalid":false,"verdict":"unsat",{}}}"#,
                bad);
            assert!(parse_record(&line).is_err(), "must reject malformed unknown member {}", bad);
        }
    }

    /// A kill inside a multibyte character must still leave a readable prefix.
    /// The `&str` API cannot express this case at all — the caller cannot even
    /// construct the argument — which is why the bytes entry point exists.
    #[test]
    fn a_prefix_survives_truncation_inside_a_multibyte_character() {
        let rec = format!(
            "{{\"hash\":\"h\",\"prop\":0,\"strategy\":\"s\",\"detail\":\"{}\",\"budget\":1,\"consumed\":1,\"invalid\":false,\"verdict\":\"unsat\"}}\n",
            "ok");
        let mut bytes = rec.clone().into_bytes();
        // A second, partial record cut inside the UTF-8 encoding of 'é'.
        bytes.extend_from_slice(b"{\"hash\":\"h\",\"detail\":\"\xc3");
        assert!(std::str::from_utf8(&bytes).is_err(), "the control: these bytes are not valid UTF-8");
        let p = read_prefix_bytes(&bytes).expect("the complete prefix must still parse");
        assert_eq!(p.records.len(), 1, "the one complete record must be recovered");
        assert!(p.truncated, "the partial tail must be reported as truncation");
    }

    /// A trailing comma is not JSON, and §7.5 requires every complete line to be
    /// one JSON object. Accepting it would hand a malformed line on as cost data.
    #[test]
    fn trailing_comma_is_rejected() {
        let good = r#"{"hash":"h","prop":0,"strategy":"s","detail":"","budget":1,"consumed":1,"invalid":false,"verdict":"unsat"}"#;
        assert!(parse_record(good).is_ok(), "the control line must parse");
        let bad = r#"{"hash":"h","prop":0,"strategy":"s","detail":"","budget":1,"consumed":1,"invalid":false,"verdict":"unsat",}"#;
        assert!(parse_record(bad).is_err(), "a trailing comma must be rejected");
    }

    /// §7.5 requires a record's members to be readable when a producer adds one
    /// this consumer does not know — including values outside the record's own
    /// narrow grammar.
    #[test]
    fn unknown_members_of_any_json_type_are_ignored() {
        for extra in [r#""x":{"a":[1,-2.5e3,null]}"#, r#""x":[-1,{"k":"}"}]"#, r#""x":-4.5"#] {
            let line = format!(
                r#"{{"hash":"h","prop":0,"strategy":"s","detail":"","budget":1,"consumed":1,"invalid":false,"verdict":"unsat",{}}}"#,
                extra);
            assert!(parse_record(&line).is_ok(), "unknown member {} must be ignored", extra);
        }
    }

    /// The contract, not just the shape: §7.5 fixes `verdict` to null exactly when
    /// `invalid` is true.
    #[test]
    fn contract_violations_are_rejected_at_parse() {
        let bad = [
            r#"{"hash":"h","prop":0,"strategy":"s","detail":"","budget":1,"consumed":1,"invalid":true,"verdict":"unsat"}"#,
            r#"{"hash":"h","prop":0,"strategy":"s","detail":"","budget":1,"consumed":null,"invalid":false,"verdict":null}"#,
        ];
        for line in bad {
            assert!(parse_record(line).is_err(), "must reject: {}", line);
        }
    }

    use super::*;

    fn rec() -> CostRecord {
        CostRecord {
            hash: "ab".repeat(32),
            prop: 3,
            strategy: strategy::INDUCTION.into(),
            detail: "binder=0,ctor=cons".into(),
            budget: 4_000_000,
            consumed: Some(2_294),
            invalid: false,
            verdict: Some("unsat".into()),
            wall_ms: None,
        }
    }

    /// §7.5 WIRE FORMAT: one JSON object, one LF, no CR, all eight members.
    /// FAILS IF: the encoder omits a member, emits more than one line, or drops
    /// the terminator.
    #[test]
    fn a_record_is_one_lf_terminated_json_object_carrying_the_eight_members() {
        let line = encode_record(&rec());
        assert!(line.ends_with('\n'), "single LF terminator: {:?}", line);
        assert_eq!(line.matches('\n').count(), 1, "exactly one line: {:?}", line);
        assert!(!line.contains('\r'), "no CR: {:?}", line);
        assert!(line.starts_with('{') && line.trim_end().ends_with('}'));
        for m in ["hash", "prop", "strategy", "detail", "budget", "consumed", "invalid", "verdict"] {
            assert!(line.contains(&format!("\"{}\":", m)), "member {} present in {}", m, line);
        }
    }

    /// §7.5: `consumed` is NULL when the solver reported no counter, and
    /// `verdict` is NULL exactly when `invalid` is true — never the string
    /// "unknown", which is a real solver answer.
    /// FAILS IF: an abort is encoded with a "unknown" verdict, or a missing
    /// counter is encoded as 0.
    #[test]
    fn an_abort_carries_a_null_verdict_and_may_carry_a_null_counter() {
        let mut r = rec();
        r.invalid = true;
        r.verdict = None;
        r.consumed = None;
        let line = encode_record(&r);
        assert!(line.contains("\"invalid\":true"));
        assert!(line.contains("\"verdict\":null"), "{}", line);
        assert!(line.contains("\"consumed\":null"), "{}", line);
        assert!(!line.contains("\"unknown\""), "an abort must NOT be written as unknown: {}", line);

        // The two members are independent: an abort that DID report a counter
        // (a cancel below budget) keeps it.
        r.consumed = Some(17);
        assert!(encode_record(&r).contains("\"consumed\":17"));

        assert!(!CostRecord { verdict: Some("unknown".into()), ..r.clone() }.well_formed());
        assert!(CostRecord { invalid: false, verdict: Some("unknown".into()), ..r.clone() }.well_formed());
        assert!(!CostRecord { invalid: false, verdict: None, ..r }.well_formed());
    }

    /// §7.5: "A record MUST be complete on its line." A strategy detail or an
    /// abort reason containing a newline must NOT be able to split the record.
    /// FAILS IF: the encoder stops escaping control characters.
    #[test]
    fn a_value_containing_a_newline_cannot_split_the_record() {
        let mut r = rec();
        r.detail = "a\nb\r\"c\\d\te".into();
        let line = encode_record(&r);
        assert_eq!(line.matches('\n').count(), 1, "still one line: {:?}", line);
        assert!(!line.contains('\r'));
        // ...and it round-trips, so the escaping is faithful and not lossy.
        let back = parse_record(line.trim_end()).expect("parses");
        assert_eq!(back.detail, "a\nb\r\"c\\d\te");
    }

    /// §7.5 round trip: encode then parse recovers every member.
    /// FAILS IF: any member is dropped or mistyped on either side.
    #[test]
    fn encode_then_parse_is_the_identity() {
        for r in [
            rec(),
            CostRecord { consumed: None, ..rec() },
            CostRecord { invalid: true, verdict: None, ..rec() },
            CostRecord { verdict: Some("sat".into()), ..rec() },
            CostRecord { verdict: Some("unknown".into()), detail: String::new(), ..rec() },
            CostRecord { wall_ms: Some(1234), ..rec() },
        ] {
            let line = encode_record(&r);
            assert_eq!(parse_record(line.trim_end()).expect("parses"), r, "round trip: {}", line);
        }
    }

    /// §7.5: "Unknown members MUST be ignored by a consumer, so the format can
    /// gain fields without breaking one."
    /// FAILS IF: the parser rejects (or chokes on) a member it does not know.
    #[test]
    fn an_unknown_member_is_ignored_not_rejected() {
        let base = rec();
        let line = encode_record(&base);
        let grown = line.trim_end().replace(
            "\"invalid\":",
            "\"solver_seed\":\"deadbeef\",\"attempt_no\":9,\"experimental\":null,\"flag\":true,\"invalid\":",
        );
        assert_ne!(grown, line.trim_end(), "the fixture actually injected the new members");
        assert_eq!(parse_record(&grown).expect("unknown members are ignored"), base);
    }

    /// §7.5: "a consumer MUST treat a trailing partial line as absent rather
    /// than as corrupt" — the valid-prefix guarantee of a killed run.
    /// FAILS IF: the reader errors on a truncated tail, or reassembles it.
    #[test]
    fn a_trailing_partial_line_is_absent_not_corrupt() {
        let whole: String = [rec(), CostRecord { prop: 4, ..rec() }, CostRecord { prop: 5, ..rec() }]
            .iter()
            .map(encode_record)
            .collect();

        // Every truncation point: the prefix must read cleanly, and must contain
        // exactly the records whose LF the truncation kept.
        for cut in 0..whole.len() {
            if !whole.is_char_boundary(cut) {
                continue;
            }
            let part = &whole[..cut];
            let p = read_prefix(part).unwrap_or_else(|e| panic!("prefix at {} must read: {}", cut, e));
            let complete = part.matches('\n').count();
            assert_eq!(p.records.len(), complete, "prefix at {} keeps its complete lines only", cut);
            assert_eq!(p.truncated, !part.is_empty() && !part.ends_with('\n'));
        }
        let full = read_prefix(&whole).expect("full file reads");
        assert_eq!(full.records.len(), 3);
        assert!(!full.truncated);
    }

    /// A COMPLETE line that does not parse is an error, not a silent skip — the
    /// flush rule makes a complete line a complete record.
    /// FAILS IF: the reader swallows malformed complete lines (which would make
    /// the truncation test above pass vacuously).
    #[test]
    fn a_malformed_complete_line_is_an_error() {
        assert!(read_prefix("{not json}\n").is_err());
        assert!(read_prefix("{\"hash\":\"aa\"}\n").is_err(), "a record missing members is malformed");
        // Control: the same file with a well-formed line reads.
        assert!(read_prefix(&encode_record(&rec())).is_ok());
    }

    /// §7.5: records are written AND FLUSHED as they are produced, never
    /// buffered to the end of a shard. The sink is handed a writer that records
    /// the order of writes and flushes, so a buffering implementation is visible.
    /// FAILS IF: `record` stops flushing, or accumulates lines internally.
    #[test]
    fn every_record_is_written_and_flushed_before_the_next_one() {
        use std::sync::{Arc, Mutex};
        #[derive(Default)]
        struct Trace {
            events: Vec<String>,
        }
        struct W(Arc<Mutex<Trace>>);
        impl Write for W {
            fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
                self.0.lock().unwrap().events.push(format!("write:{}", String::from_utf8_lossy(buf).trim_end()));
                Ok(buf.len())
            }
            fn flush(&mut self) -> std::io::Result<()> {
                self.0.lock().unwrap().events.push("flush".into());
                Ok(())
            }
        }
        let trace = Arc::new(Mutex::new(Trace::default()));
        let sink = CostSink::new(Box::new(W(trace.clone())));
        sink.record(&rec());
        let after_first = trace.lock().unwrap().events.clone();
        assert_eq!(after_first.len(), 2, "one write then one flush: {:?}", after_first);
        assert!(after_first[0].starts_with("write:{"), "{:?}", after_first);
        assert_eq!(after_first[1], "flush", "the record is FLUSHED before control returns: {:?}", after_first);

        sink.record(&CostRecord { prop: 9, ..rec() });
        let all = trace.lock().unwrap().events.clone();
        assert_eq!(all.len(), 4, "the second record is a second write+flush, not a batch: {:?}", all);
        assert_eq!(sink.written(), 2);
    }
}
