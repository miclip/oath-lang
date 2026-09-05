// `prove` (hence `--shard` and `--cost-out`) is behind the `prove` feature; in a
// solver-free build it is compiled out. Gate the file so
// `cargo test --no-default-features` stays green.
#![cfg(feature = "prove")]

//! SPEC §7.5 — PER-ATTEMPT COST EMISSION, end to end through the real binary.
//!
//! The unit tests in `src/cost.rs` pin the RECORD (its wire format, its
//! null-handling, its valid-prefix reading) without a solver. These pin the
//! EMISSION: that a real sharded run produces one record per §7.2
//! property-proof attempt, with this attempt's budget and this attempt's
//! consumed counter, written as it goes — and that attaching the sink changes
//! NOTHING about the run, which is §7.5's hardest requirement to satisfy by
//! accident.
//!
//! WHAT THESE TESTS DO NOT ASSERT. §7.5 fixes the FRAMING — the encoding, the
//! line discipline, the member names, the types — and leaves the VALUES of
//! `strategy` and `detail` opaque and kernel-local, with §10 not comparing the
//! emission across kernels at all. So no test here asserts that a particular
//! label is correct, only that the labels DISTINGUISH a property's attempts and
//! are STABLE within this kernel, which is the whole of what the section
//! requires of them. An assertion naming a label would be pinning a word this
//! kernel chose as though it were portable.
//!
//! Every test states what would make it fail.

use oathrs::cost::{read_prefix, CostRecord};
use std::io::Write;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};

static TMP_SEQ: AtomicU64 = AtomicU64::new(0);

fn tmp(name: &str, body: &str) -> std::path::PathBuf {
    let seq = TMP_SEQ.fetch_add(1, Ordering::Relaxed);
    let mut p = std::env::temp_dir();
    p.push(format!("oathrs-cost-test-{}-{}-{}", std::process::id(), seq, name));
    let mut f = std::fs::File::create(&p).expect("create temp file");
    f.write_all(body.as_bytes()).expect("write temp file");
    p
}

fn tmp_path(name: &str) -> std::path::PathBuf {
    let seq = TMP_SEQ.fetch_add(1, Ordering::Relaxed);
    let mut p = std::env::temp_dir();
    p.push(format!("oathrs-cost-out-{}-{}-{}", std::process::id(), seq, name));
    let _ = std::fs::remove_file(&p);
    p
}

fn have_z3() -> bool {
    Command::new("z3").arg("--version").output().map(|o| o.status.success()).unwrap_or(false)
}

/// A three-definition arithmetic corpus, one property each, all z3-provable and
/// fast. No datatypes, so every attempt is a direct one.
const FLAT: &str = r#"
(defn twice [] [(n Int)] Int
  (* 2 n)
  (prop doubles [(n Int)] (== (twice n) (+ n n))))

(defn unrelated [] [(n Int)] Int
  (+ n 1)
  (prop succ [(n Int)] (== (unrelated n) (+ n 1))))

(defn quad [] [(n Int)] Int
  (twice (twice n))
  (prop unfolds [(n Int)] (== (quad n) (twice (twice n)))))
"#;

/// A list corpus whose later properties do NOT discharge under an empty seed, so
/// the strategy sequence runs to its end: lemma-free, direct, deterministic
/// instantiation, structural induction, lexicographic induction, and the
/// full-budget direct fallback. Run at a small rlimit so the failures are cheap.
const LISTS: &str = r#"
(data List [a]
  (Nil)
  (Cons a (List a)))

(defn length [a] [(xs (List a))] Int
  (match xs
    ((Nil) 0)
    ((Cons h t) (+ 1 (length [a] t))))
  (prop non-negative [(xs (List Int))]
    (<= 0 (length [Int] xs))))

(defn append [a] [(xs (List a)) (ys (List a))] (List a)
  (match xs
    ((Nil) ys)
    ((Cons h t) (Cons [a] h (append [a] t ys))))
  (prop length-adds [(xs (List Int)) (ys (List Int))]
    (== (length [Int] (append [Int] xs ys))
        (+ (length [Int] xs) (length [Int] ys)))))

(defn reverse [a] [(xs (List a))] (List a)
  (match xs
    ((Nil) (Nil [a]))
    ((Cons h t) (append [a] (reverse [a] t) (Cons [a] h (Nil [a])))))
  (prop involution [(xs (List Int))]
    (== (reverse [Int] (reverse [Int] xs)) xs))
  (prop antidistributes-over-append [(xs (List Int)) (ys (List Int))]
    (== (reverse [Int] (append [Int] xs ys))
        (append [Int] (reverse [Int] ys) (reverse [Int] xs)))))
"#;

fn oathrs_env(args: &[String], envs: &[(&str, &str)]) -> std::process::Output {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_oathrs"));
    cmd.args(args);
    for (k, v) in envs {
        cmd.env(k, v);
    }
    cmd.output().expect("run oathrs")
}

/// A seed naming every definition with NO proven properties — a legitimate `S`
/// (the empty proven set) that leaves the lemma library empty, so the strategy
/// sequence has to work for its verdicts.
fn empty_seed(corpus: &std::path::Path) -> String {
    let out = Command::new(env!("CARGO_BIN_EXE_oathrs"))
        .args(["hash", corpus.to_str().unwrap()])
        .output()
        .expect("run oathrs hash");
    assert!(out.status.success(), "oathrs hash: {}", String::from_utf8_lossy(&out.stderr));
    let defs: Vec<String> = String::from_utf8_lossy(&out.stdout)
        .lines()
        .filter_map(|l| {
            let mut it = l.split('\t');
            match (it.next(), it.next()) {
                (Some(n), Some(h)) => {
                    Some(format!("{{\"name\":\"{}\",\"hash\":\"{}\",\"props\":[]}}", n, h))
                }
                _ => None,
            }
        })
        .collect();
    format!("{{\"definitions\":[{}]}}", defs.join(","))
}

fn shard_args(seed: &std::path::Path, corpus: &std::path::Path, cost: Option<&std::path::Path>) -> Vec<String> {
    let mut a: Vec<String> = vec![
        "prove".into(),
        "--shard".into(),
        "0/1".into(),
        "--hints".into(),
        seed.to_str().unwrap().into(),
    ];
    if let Some(c) = cost {
        a.push("--cost-out".into());
        a.push(c.to_str().unwrap().into());
    }
    a.push(corpus.to_str().unwrap().into());
    a
}

/// Read the emission and require it to be COMPLETE (the run finished, so no
/// trailing partial line is expected) and non-empty.
fn records(path: &std::path::Path) -> Vec<CostRecord> {
    let text = std::fs::read_to_string(path).expect("cost emission exists");
    assert!(text.ends_with('\n'), "a completed run leaves no partial line: {:?}", text);
    assert!(!text.contains('\r'), "LF only, never CRLF");
    let p = read_prefix(&text).expect("every complete line is a complete record");
    assert!(!p.truncated);
    assert!(!p.records.is_empty(), "a run with solver attempts emits records");
    p.records
}

// ===========================================================================

/// §7.5: "no verdict, campaign identity, merge result or conformance outcome may
/// depend on the emission or its absence."
///
/// The shard result on stdout must be BYTE-IDENTICAL with and without
/// `--cost-out` — which covers the verdicts, the partition and the campaign
/// identity in one comparison, since the emission carries all three.
///
/// FAILS IF: the cost path perturbs the run — a different script, a different
/// budget, an attempt made or skipped because a sink is attached, or the sink's
/// own failure being allowed to change a verdict.
#[test]
fn attaching_the_sink_changes_nothing_the_run_reports() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("flat.oath", FLAT);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let cost = tmp_path("flat.jsonl");

    let without = oathrs_env(&shard_args(&seed, &corpus, None), &[]);
    let with = oathrs_env(&shard_args(&seed, &corpus, Some(&cost)), &[]);

    assert!(without.status.success() && with.status.success());
    assert_eq!(
        without.stdout, with.stdout,
        "the shard result must not depend on the emission:\n{}\n---\n{}",
        String::from_utf8_lossy(&without.stdout),
        String::from_utf8_lossy(&with.stdout)
    );
    // Control: the run WITH the flag really did emit, so the equality above is
    // not the equality of two runs that both emitted nothing.
    assert!(!records(&cost).is_empty());

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
    let _ = std::fs::remove_file(&cost);
}

/// §7.5 WIRE FORMAT, on a real emission: UTF-8, one JSON object per line, each
/// terminated by a single LF, each carrying at least the eight members, and each
/// keyed by a definition hash and property index the run actually attempted.
///
/// FAILS IF: the producer writes a JSON array, a pretty-printed object spanning
/// lines, a CSV, a record missing a member, or a record keyed by a display name
/// instead of the identity hash.
#[test]
fn a_real_emission_is_one_complete_record_per_line_keyed_by_identity() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("flat.oath", FLAT);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let cost = tmp_path("shape.jsonl");
    let out = oathrs_env(&shard_args(&seed, &corpus, Some(&cost)), &[]);
    assert!(out.status.success());

    let text = std::fs::read_to_string(&cost).expect("emission");
    // One object per line: as many complete lines as records, and every line
    // independently parseable — nothing is reassembled across lines.
    let recs = records(&cost);
    assert_eq!(recs.len(), text.lines().count(), "one record per line");
    for line in text.lines() {
        assert!(line.starts_with('{') && line.ends_with('}'), "each line is one object: {}", line);
        oathrs::cost::parse_record(line).expect("each line parses alone");
    }

    // The verdict lines of the shard result name (hash, prop); every cost record
    // must be keyed by one of those pairs.
    let stdout = String::from_utf8_lossy(&out.stdout);
    let attempted: Vec<(String, usize)> = stdout
        .lines()
        .filter_map(|l| {
            let mut f = l.split('\t');
            match (f.next(), f.next()) {
                (Some(h), Some(p)) if h.len() == 64 => Some((h.to_string(), p.parse().ok()?)),
                _ => None,
            }
        })
        .collect();
    assert!(!attempted.is_empty(), "the shard attempted something: {}", stdout);
    for r in &recs {
        assert_eq!(r.hash.len(), 64, "keyed by the O1 identity hash, not a name: {:?}", r);
        assert!(
            attempted.contains(&(r.hash.clone(), r.prop)),
            "record {:?} names a (hash, prop) the shard did not attempt",
            r
        );
        assert!(r.well_formed(), "invalid/verdict must agree: {:?}", r);
        assert!(!r.strategy.is_empty(), "every attempt names its strategy: {:?}", r);
    }
    // And every attempted property that reached the solver is accounted for.
    for (h, p) in &attempted {
        assert!(
            recs.iter().any(|r| &r.hash == h && &r.prop == p),
            "no cost record for attempted property {} {}",
            h,
            p
        );
    }

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
    let _ = std::fs::remove_file(&cost);
}

/// §7.5 `budget`: "the effective rlimit for THIS attempt, which is not always
/// the run's nominal budget — §7.2's reduced-budget attempts run at their own."
///
/// The list corpus drives the whole strategy sequence, whose attempts run at two
/// different rlimits (the reduced direct/lemma-free/instantiation budget, and
/// the full budget for induction and the fallback). With the run's nominal
/// budget set ABOVE the reduced constant, both must appear, the maximum must be
/// the nominal one, and some record must be strictly below it.
///
/// FAILS IF: `budget` is written as `z3_rlimit()` (the run's nominal budget) for
/// every attempt — the single most natural way to get this field wrong.
#[test]
fn budget_is_this_attempts_rlimit_not_the_runs_nominal_one() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("lists.oath", LISTS);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let cost = tmp_path("budgets.jsonl");
    const NOMINAL: u64 = 5_000_000; // above the reduced-attempt constant
    let out = oathrs_env(
        &shard_args(&seed, &corpus, Some(&cost)),
        &[("OATHRS_Z3_RLIMIT", &NOMINAL.to_string())],
    );
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));

    let recs = records(&cost);
    let mut budgets: Vec<u64> = recs.iter().map(|r| r.budget).collect();
    budgets.sort_unstable();
    budgets.dedup();
    assert!(budgets.len() >= 2, "the sequence runs at more than one budget, got {:?}", budgets);
    assert_eq!(*budgets.last().unwrap(), NOMINAL, "the largest budget is the run's nominal one");
    assert!(budgets[0] < NOMINAL, "some attempt runs at a REDUCED budget: {:?}", budgets);

    // The budget spread must be BETWEEN strategies, not one strategy varying —
    // otherwise `budget` could be some per-run noise rather than this attempt's
    // rlimit. Stated WITHOUT naming a label: §7.5 makes the values opaque, so
    // the assertion is about the partition they induce, not about which words
    // this kernel picked.
    let mut by_strategy: std::collections::BTreeMap<&str, std::collections::BTreeSet<u64>> =
        Default::default();
    for r in &recs {
        by_strategy.entry(r.strategy.as_str()).or_default().insert(r.budget);
    }
    assert!(by_strategy.len() >= 2, "more than one strategy ran: {:?}", by_strategy.keys());
    for (s, b) in &by_strategy {
        assert_eq!(b.len(), 1, "strategy {:?} runs at ONE budget, got {:?}", s, b);
    }

    // `consumed` is the SOLVER'S counter, not the budget: a proof that
    // discharges cheaply must report far less than it was allowed.
    assert!(
        recs.iter().any(|r| matches!(r.consumed, Some(c) if c < r.budget / 10)),
        "some attempt discharges well under budget; consumed is the solver's own counter"
    );

    // §7.5: `consumed` is INDEPENDENT of `invalid`, and a PROVED goal records
    // its cost like any other attempt — the case a `consumed = invalid ? null :
    // n` producer gets right and a `verdict-scan-returns-early` producer gets
    // wrong, reporting nothing for exactly the attempts that succeeded.
    let proved: Vec<&CostRecord> = recs.iter().filter(|r| r.verdict.as_deref() == Some("unsat")).collect();
    assert!(!proved.is_empty(), "this corpus proves something: {:?}", recs);
    for r in &proved {
        assert!(!r.invalid, "a solver answer is not an abort: {:?}", r);
        assert!(r.consumed.is_some(), "a PROVED goal records its cost: {:?}", r);
    }

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
    let _ = std::fs::remove_file(&cost);
}

/// §7.5 `invalid` / `verdict`: "true iff the attempt was an abort (§7.2 #72) …
/// An abort is NOT an outcome" and "a producer MUST NOT substitute `"unknown"`,
/// which is a real solver answer".
///
/// A one-millisecond wall cap makes every attempt an abort: z3 is killed before
/// it reports anything, so the record must be `invalid: true`, `verdict: null`,
/// and — because the kill takes the telemetry with it — `consumed: null` rather
/// than an invented number.
///
/// The `consumed: null` assertion below is about THIS abort class and nothing
/// wider. §7.5 makes `consumed` and `invalid` independent in both directions,
/// and `memout` / below-budget `canceled` are aborts that exit normally WITH a
/// counter; neither is producible on demand from a test, so that direction is
/// asserted where it can be — over the pure `cost_record` in `src/prove.rs`.
/// Reading this test as "an abort never carries a counter" would be exactly the
/// derivation the section forbids.
///
/// FAILS IF: an abort is written with `"unknown"`, with `invalid: false`, or
/// with a fabricated consumed counter.
#[test]
fn an_aborted_attempt_is_invalid_with_no_verdict_and_no_invented_counter() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("lists.oath", LISTS);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    // A budget large enough that several attempts run for far longer than the
    // 1ms cap below — the cap must be what ends them, not the solver finishing.
    const RLIMIT: &str = "1000000";

    // CONTROL first: the SAME corpus at the SAME budget under the NORMAL wall
    // cap records NO aborts, so the assertions below are about the cap.
    let ok_cost = tmp_path("noabort.jsonl");
    let ok = oathrs_env(
        &shard_args(&seed, &corpus, Some(&ok_cost)),
        &[("OATHRS_Z3_RLIMIT", RLIMIT)],
    );
    assert!(ok.status.success());
    let ok_recs = records(&ok_cost);
    assert!(ok_recs.iter().all(|r| !r.invalid), "control: no aborts at the normal cap");
    assert!(ok_recs.iter().all(|r| r.verdict.is_some() && r.consumed.is_some()));

    let cost = tmp_path("abort.jsonl");
    let out = oathrs_env(
        &shard_args(&seed, &corpus, Some(&cost)),
        &[("OATHRS_Z3_RLIMIT", RLIMIT), ("OATHRS_Z3_WALL_CAP_MS", "1")],
    );
    assert!(out.status.success(), "a shard emission still exits 0: {}", String::from_utf8_lossy(&out.stderr));
    let recs = records(&cost);
    let aborts: Vec<&CostRecord> = recs.iter().filter(|r| r.invalid).collect();
    assert!(!aborts.is_empty(), "a 1ms wall cap aborts attempts, got {:?}", recs);
    for r in aborts {
        assert_eq!(r.verdict, None, "an abort has NO valid verdict: {:?}", r);
        assert_eq!(r.consumed, None, "a killed process reported no counter: {:?}", r);
    }
    // ...and the string "unknown" is never used for one.
    let text = std::fs::read_to_string(&cost).unwrap();
    for line in text.lines() {
        let r = oathrs::cost::parse_record(line).unwrap();
        assert!(
            !(r.invalid && r.verdict.as_deref() == Some("unknown")),
            "an abort must not be written as unknown: {}",
            line
        );
    }

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
    let _ = std::fs::remove_file(&cost);
    let _ = std::fs::remove_file(&ok_cost);
}

/// §7.5: "Records MUST be emitted as they are produced, not assembled at the end
/// … a consumer MUST be able to read a valid prefix of the emission from a run
/// that was killed, and a kernel MUST NOT buffer records to the end of a shard."
///
/// This is the requirement the section says the emission most needs to satisfy,
/// so it is tested the way it will actually be met: start a long shard, wait
/// until the file has records, KILL the process, and read what survived.
///
/// FAILS IF: records are accumulated and written on completion (the file would
/// still be empty at the kill), or are written without flushing (the same),
/// or a truncated tail makes the file unreadable.
#[test]
fn a_killed_shard_leaves_a_readable_prefix() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("lists.oath", LISTS);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let cost = tmp_path("killed.jsonl");

    let mut child = Command::new(env!("CARGO_BIN_EXE_oathrs"))
        .args(shard_args(&seed, &corpus, Some(&cost)))
        // Large enough that the corpus takes many seconds — the run must still
        // be going when we kill it.
        .env("OATHRS_Z3_RLIMIT", "40000000")
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
        .expect("spawn oathrs");

    // Wait for the first COMPLETE record to appear while the child is still
    // running. If the kernel buffered to the end this loop times out.
    let mut saw_records = false;
    for _ in 0..600 {
        std::thread::sleep(std::time::Duration::from_millis(50));
        if let Ok(t) = std::fs::read_to_string(&cost) {
            if t.contains('\n') {
                saw_records = true;
                break;
            }
        }
        if let Ok(Some(_)) = child.try_wait() {
            break; // the run finished on its own; the assertion below reports it
        }
    }
    let finished_early = matches!(child.try_wait(), Ok(Some(_)));
    let _ = child.kill();
    let _ = child.wait();
    assert!(
        saw_records,
        "records must appear WHILE the shard runs, not at the end (run finished early: {})",
        finished_early
    );
    assert!(!finished_early, "the shard must still have been running when it was killed");

    // What survives is a valid prefix: every complete line is a complete record,
    // and a partial tail (if any) is absent rather than corrupt.
    let text = std::fs::read_to_string(&cost).expect("the killed run left a file");
    let p = read_prefix(&text).expect("a killed run leaves a READABLE prefix");
    assert!(!p.records.is_empty(), "the prefix carries the records it had written");
    for r in &p.records {
        assert!(r.well_formed(), "a surviving record is complete: {:?}", r);
    }

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
    let _ = std::fs::remove_file(&cost);
}

/// A merge makes no solver attempts, so there is no per-attempt cost to emit.
/// Writing an empty file there would hand a consumer a campaign that appears to
/// have cost nothing.
///
/// FAILS IF: `--cost-out --merge-shards` is accepted and produces an empty
/// emission.
#[test]
fn cost_out_is_refused_where_no_attempt_is_made() {
    let corpus = tmp("flat.oath", FLAT);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let cost = tmp_path("merge.jsonl");
    let out = oathrs_env(
        &vec![
            "prove".to_string(),
            "--merge-shards".into(),
            "1".into(),
            "--hints".into(),
            seed.to_str().unwrap().into(),
            "--cost-out".into(),
            cost.to_str().unwrap().into(),
            corpus.to_str().unwrap().into(),
        ],
        &[],
    );
    assert!(!out.status.success(), "a merge with --cost-out must be refused");
    assert!(
        String::from_utf8_lossy(&out.stderr).contains("makes none"),
        "and must say why: {}",
        String::from_utf8_lossy(&out.stderr)
    );
    assert!(!cost.exists(), "nothing is created for a refused run");

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}

/// A `--cost-out` destination that cannot be opened fails the run at SETUP,
/// before any attempt: an operator who asked for the emission and silently got
/// none has no way to notice.
///
/// FAILS IF: an unopenable destination is ignored and the run proceeds.
#[test]
fn an_unopenable_destination_fails_before_any_attempt() {
    let corpus = tmp("flat.oath", FLAT);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let mut bad = std::env::temp_dir();
    bad.push("oathrs-cost-no-such-directory");
    bad.push("out.jsonl");
    let out = oathrs_env(&shard_args(&seed, &corpus, Some(&bad)), &[]);
    assert!(!out.status.success(), "an unopenable --cost-out is a setup error");
    assert!(
        String::from_utf8_lossy(&out.stderr).contains("--cost-out"),
        "and names the flag: {}",
        String::from_utf8_lossy(&out.stderr)
    );
    // Control: the SAME invocation with a writable destination succeeds, so the
    // failure above is the destination and not the corpus or the seed.
    if have_z3() {
        let good = tmp_path("good.jsonl");
        let ok = oathrs_env(&shard_args(&seed, &corpus, Some(&good)), &[]);
        assert!(ok.status.success(), "{}", String::from_utf8_lossy(&ok.stderr));
        let _ = std::fs::remove_file(&good);
    }

    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}

/// §7.5, `strategy` and `detail` are OPAQUE and kernel-chosen: "The only
/// requirements are that the values DISTINGUISH a property's attempts from one
/// another and be STABLE within a kernel."
///
/// This test asserts EXACTLY those two and nothing more. It never names a label,
/// because §7.5 says "Two kernels' labels are NOT comparable and a consumer MUST
/// NOT join on them; the portable key is `(hash, prop)`" — an assertion on the
/// words this kernel picked would be claiming portability the section withholds.
///
/// DISTINGUISH is checked in BOTH drivers, because they fail it differently. In
/// sharded mode a property is attempted once and the strategy sequence's own
/// labels separate its attempts. The unsharded iterating driver re-attempts a
/// property under a GROWN candidate set — different script bytes, same strategy,
/// same subgoal — and there the pass index in `detail` is what separates them;
/// the control below strips it and shows the collisions it prevents.
///
/// FAILS IF: two attempts of one property carry the same `(strategy, detail)`,
/// or the labels are not a function of the inputs (a pointer, a timestamp, a
/// counter over the whole run, an unordered iteration).
#[test]
fn strategy_and_detail_distinguish_a_propertys_attempts_and_are_stable() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("lists.oath", LISTS);
    let seed = tmp("seed.json", &empty_seed(&corpus));
    let rlimit = ("OATHRS_Z3_RLIMIT", "5000000");

    let labels = |recs: &[CostRecord]| -> Vec<(String, usize, String, String)> {
        recs.iter()
            .map(|r| (r.hash.clone(), r.prop, r.strategy.clone(), r.detail.clone()))
            .collect()
    };
    let distinct = |recs: &[CostRecord]| -> bool {
        let s: std::collections::BTreeSet<_> = labels(recs).into_iter().collect();
        s.len() == recs.len()
    };

    // --- DISTINGUISH, sharded: one pass per property.
    let a = tmp_path("dist-shard-a.jsonl");
    assert!(oathrs_env(&shard_args(&seed, &corpus, Some(&a)), &[rlimit]).status.success());
    let ra = records(&a);
    assert!(ra.len() >= 8, "the sequence really ran: {} record(s)", ra.len());
    assert!(distinct(&ra), "a property's attempts are distinguishable: {:?}", labels(&ra));

    // --- STABLE: the same inputs produce the same labels, in the same order.
    let b = tmp_path("dist-shard-b.jsonl");
    assert!(oathrs_env(&shard_args(&seed, &corpus, Some(&b)), &[rlimit]).status.success());
    assert_eq!(labels(&ra), labels(&records(&b)), "labels are stable across runs");
    // ...and so is `budget`, which is a function of the attempt too. `consumed`
    // is deliberately NOT compared: §7.5 says it is "REPORTED, never compared".
    assert_eq!(
        ra.iter().map(|r| r.budget).collect::<Vec<_>>(),
        records(&b).iter().map(|r| r.budget).collect::<Vec<_>>()
    );

    // --- DISTINGUISH, unsharded: the driver re-attempts a property whose
    // candidate set grew, repeating the whole sequence over different bytes.
    let c = tmp_path("dist-plain.jsonl");
    let plain = oathrs_env(
        &vec![
            "prove".to_string(),
            "--cost-out".into(),
            c.to_str().unwrap().into(),
            corpus.to_str().unwrap().into(),
        ],
        &[rlimit],
    );
    assert!(plain.status.success(), "{}", String::from_utf8_lossy(&plain.stderr));
    let rc = records(&c);
    assert!(distinct(&rc), "an iterating run's attempts stay distinguishable: {:?}", labels(&rc));

    // CONTROL: without the pass index the same run COLLIDES, so the assertion
    // above is testing a live mechanism rather than an accident of this corpus.
    let stripped: Vec<(String, usize, String, String)> = rc
        .iter()
        .map(|r| {
            let d = r.detail.split(',').filter(|p| !p.starts_with("retry=")).collect::<Vec<_>>().join(",");
            (r.hash.clone(), r.prop, r.strategy.clone(), d)
        })
        .collect();
    let uniq: std::collections::BTreeSet<_> = stripped.iter().cloned().collect();
    assert!(
        uniq.len() < stripped.len(),
        "control: the iterating run must re-attempt something, else this test is vacuous"
    );

    for p in [&corpus, &seed, &a, &b, &c] {
        let _ = std::fs::remove_file(p);
    }
}

/// §7.5 SCOPE: "A kernel offering sharded mode MAY additionally emit a COST
/// RECORD per §7.2 PROPERTY-PROOF attempt. That scope is narrower than 'per
/// solver call' and deliberately so: §6.1.1's termination-measure search also
/// reaches the solver, per DEFINITION, with no property index to key a record
/// to. Such calls are OUT of this emission."
///
/// `countdown` is not structurally recursive, so §6.1.1's integer-ranking search
/// decides its termination — which means the solver IS called for it — and it
/// carries no property. The control is its analysis verdict: `measure` is
/// reachable only through that search, so a corpus where it holds is a corpus
/// where the excluded solver calls really happened.
///
/// FAILS IF: the measure search is routed through the emitting seam — a record
/// would appear keyed to a definition with no property, or with a fabricated
/// property index.
#[test]
fn the_emission_is_scoped_to_property_proof_attempts() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    const MEASURE: &str = r#"
(defn countdown [] [(n Int)] Int
  (if (<= n 0) 0 (countdown (- n 1))))

(defn uses-countdown [] [(n Int)] Int
  (+ n (countdown n))
  (prop adds [(n Int)] (== (uses-countdown n) (+ n (countdown n)))))
"#;
    let corpus = tmp("measure.oath", MEASURE);
    let seed = tmp("seed.json", &empty_seed(&corpus));

    // CONTROL: `countdown` really does reach the solver through §6.1.1.
    let analysis = oathrs_env(&["analyze".to_string(), corpus.to_str().unwrap().into()], &[]);
    assert!(analysis.status.success());
    let text = String::from_utf8_lossy(&analysis.stdout);
    assert!(
        text.contains("\"termination\": \"measure\""),
        "control: countdown's termination must come from the §6.1.1 SOLVER search: {}",
        text
    );

    // The hash of the definition that has NO properties.
    let hashes = oathrs_env(&["hash".to_string(), corpus.to_str().unwrap().into()], &[]);
    let measure_only: String = String::from_utf8_lossy(&hashes.stdout)
        .lines()
        .find(|l| l.starts_with("countdown\t"))
        .and_then(|l| l.split('\t').nth(1))
        .expect("countdown is in the corpus")
        .to_string();

    let cost = tmp_path("scope.jsonl");
    assert!(oathrs_env(&shard_args(&seed, &corpus, Some(&cost)), &[]).status.success());
    let recs = records(&cost);
    for r in &recs {
        assert_ne!(
            r.hash, measure_only,
            "§6.1.1's measure search is OUT of this emission; got {:?}",
            r
        );
    }
    // Control the other way: the property-bearing definition IS emitted, so the
    // absence above is a scope decision and not an empty run.
    assert!(recs.iter().any(|r| r.hash != measure_only), "the property attempt was emitted");

    for p in [&corpus, &seed, &cost] {
        let _ = std::fs::remove_file(p);
    }
}
