// The `prove` subcommand (and thus `--shard`) is behind the `prove` feature; in
// a solver-free build it is compiled out and the binary answers "unknown
// command: prove". Gate the whole test file so `cargo test --no-default-features`
// keeps a green suite rather than failing on a command that does not exist.
#![cfg(feature = "prove")]

//! SPEC §7.5 test 4 — an injected elaboration error fails the sharded run
//! GLOBALLY, from a shard that does NOT contain the broken definition.
//!
//! This is a CLI-level property: `oathrs prove --shard i/n` elaborates the WHOLE
//! corpus before it selects a shard, so an elaboration failure fails every shard
//! regardless of where the broken definition would have landed. A shard that
//! suppressed the error because the broken def fell outside it would turn a
//! broken corpus into a green run — the single most important §7.5 invariant.
//! It runs the real binary so the global-elaboration gate is exercised end to
//! end, not a library stand-in for it.

use std::io::Write;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};

// Tests run in parallel threads within ONE process, so a temp name keyed only by
// pid would collide across tests. A per-call counter keeps every file unique.
static TMP_SEQ: AtomicU64 = AtomicU64::new(0);

fn tmp(name: &str, body: &str) -> std::path::PathBuf {
    let seq = TMP_SEQ.fetch_add(1, Ordering::Relaxed);
    let mut p = std::env::temp_dir();
    p.push(format!("oathrs-shard-test-{}-{}-{}", std::process::id(), seq, name));
    let mut f = std::fs::File::create(&p).expect("create temp file");
    f.write_all(body.as_bytes()).expect("write temp file");
    p
}

/// A corpus whose second definition has a dangling call (`nonexistent`), so
/// elaboration must fail. `good` is a well-formed definition; whichever shard it
/// is assigned to, the run must still fail because elaboration is global.
const BROKEN: &str = r#"
(defn good [] [(n Int)] Int
  (+ n 1)
  (prop succ [(n Int)] (== (good n) (+ n 1))))

(defn broken [] [(n Int)] Int
  (nonexistent n)
  (prop p [(n Int)] (== (broken n) n)))
"#;

const EMPTY_SEED: &str = r#"{"definitions":[]}"#;

fn run_shard(seed: &std::path::Path, corpus: &std::path::Path, shard: &str) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_oathrs"))
        .args(["prove", "--shard", shard, "--hints"])
        .arg(seed)
        .arg(corpus)
        .output()
        .expect("run oathrs")
}

#[test]
fn elaboration_error_fails_the_sharded_run_from_every_shard() {
    let seed = tmp("seed.json", EMPTY_SEED);
    let corpus = tmp("broken.oath", BROKEN);

    // Every shard of a 2-shard split must fail — including the one that does not
    // contain `broken`. We do not need to know which shard `broken` lands in:
    // BOTH must fail, which is exactly the point.
    for shard in ["0/2", "1/2"] {
        let out = run_shard(&seed, &corpus, shard);
        assert!(
            !out.status.success(),
            "shard {} must FAIL on a corpus that does not elaborate",
            shard
        );
        let stderr = String::from_utf8_lossy(&out.stderr);
        assert!(
            stderr.contains("elaboration failed"),
            "shard {} must fail specifically because elaboration is global, got: {}",
            shard,
            stderr
        );
    }

    // Control: a corpus that DOES elaborate runs (exit 0), so the failures above
    // are the elaboration error and not the harness. Property-free so the control
    // needs no solver — the elaboration gate is what test 4 is about.
    let fixed = tmp(
        "fixed.oath",
        "(defn good [] [(n Int)] Int (+ n 1))\n",
    );
    let out = run_shard(&seed, &fixed, "0/2");
    assert!(
        out.status.success(),
        "the elaborating corpus runs: {}",
        String::from_utf8_lossy(&out.stderr)
    );

    let _ = std::fs::remove_file(&seed);
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&fixed);
}

// ===========================================================================
// SPEC §7.5 — the PARALLEL CAMPAIGN pipeline: `--shard i/n` emits a wire-format
// contribution, `--merge-shards n` collects the emissions and self-checks the
// union. These exercise the real binary end to end, so they need z3 (the shards
// actually prove); they SKIP cleanly when z3 is absent so a solver-free
// `cargo test` stays green. The z3-free coverage of the same round-trip lives in
// the library test `emit_parse_merge_equals_in_process_verify`.
// ===========================================================================

/// A 3-definition arithmetic corpus, one property each, all z3-provable.
const CORPUS: &str = r#"
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

fn have_z3() -> bool {
    Command::new("z3").arg("--version").output().map(|o| o.status.success()).unwrap_or(false)
}

fn oathrs(args: &[&str]) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_oathrs")).args(args).output().expect("run oathrs")
}

/// Build a `prove/outcomes.json` seed marking each named def's single property
/// proven, using the corpus's real hashes (from `oathrs hash`). `omit` names a
/// definition to leave OUT of the seed entirely (for the non-self-consistent
/// case).
fn seed_json(corpus: &std::path::Path, omit: Option<&str>) -> String {
    let out = oathrs(&["hash", corpus.to_str().unwrap()]);
    assert!(out.status.success(), "oathrs hash failed: {}", String::from_utf8_lossy(&out.stderr));
    let text = String::from_utf8_lossy(&out.stdout);
    let mut defs = Vec::new();
    for line in text.lines() {
        let mut it = line.split('\t');
        let (name, hash) = match (it.next(), it.next()) {
            (Some(n), Some(h)) => (n, h),
            _ => continue,
        };
        if omit == Some(name) {
            continue;
        }
        defs.push(format!(
            "{{\"name\":\"{}\",\"hash\":\"{}\",\"props\":[{{\"name\":\"p\",\"proven\":true}}]}}",
            name, hash
        ));
    }
    format!("{{\"definitions\":[{}]}}", defs.join(","))
}

/// Emit shard `i/n` to a file; returns the file path.
fn emit_shard(seed: &std::path::Path, corpus: &std::path::Path, i: u64, n: u64) -> std::path::PathBuf {
    let out = oathrs(&[
        "prove",
        "--shard",
        &format!("{}/{}", i, n),
        "--hints",
        seed.to_str().unwrap(),
        corpus.to_str().unwrap(),
    ]);
    assert!(out.status.success(), "--shard {}/{} must exit 0 (contribution): {}", i, n, String::from_utf8_lossy(&out.stderr));
    let body = String::from_utf8_lossy(&out.stdout);
    assert!(body.contains("CONTRIBUTION ONLY"), "the emission is labelled a contribution: {}", body);
    tmp(&format!("shard-{}-of-{}.txt", i, n), &body)
}

fn merge_args(seed: &std::path::Path, corpus: &std::path::Path, n: u64, files: &[std::path::PathBuf]) -> Vec<String> {
    let mut args: Vec<String> = vec![
        "prove".into(),
        "--merge-shards".into(),
        n.to_string(),
        "--hints".into(),
        seed.to_str().unwrap().into(),
    ];
    for f in files {
        args.push("--shard-in".into());
        args.push(f.to_str().unwrap().into());
    }
    args.push(corpus.to_str().unwrap().into());
    args
}

fn merge(seed: &std::path::Path, corpus: &std::path::Path, n: u64, files: &[std::path::PathBuf]) -> std::process::Output {
    let args = merge_args(seed, corpus, n, files);
    let refs: Vec<&str> = args.iter().map(|s| s.as_str()).collect();
    oathrs(&refs)
}

/// The corpus's (name, hash) pairs, from `oathrs hash`.
fn corpus_hashes(corpus: &std::path::Path) -> Vec<(String, String)> {
    let out = oathrs(&["hash", corpus.to_str().unwrap()]);
    assert!(out.status.success(), "oathrs hash failed");
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .filter_map(|l| {
            let mut it = l.split('\t');
            match (it.next(), it.next()) {
                (Some(n), Some(h)) => Some((n.to_string(), h.to_string())),
                _ => None,
            }
        })
        .collect()
}

/// A seed with the SAME proven set as `seed_json(corpus, None)` but carrying one
/// author hint on `from_name`'s property (targeting `to_name` prop 0). Only the
/// hints differ, so the campaign identity differs while S is identical.
fn seed_json_hinted(corpus: &std::path::Path, from_name: &str, to_name: &str) -> String {
    let hs = corpus_hashes(corpus);
    let to_hash = &hs.iter().find(|(n, _)| n == to_name).expect("target def").1;
    let defs: Vec<String> = hs
        .iter()
        .map(|(name, hash)| {
            let prop = if name == from_name {
                format!("{{\"name\":\"p\",\"proven\":true,\"hints\":[{{\"def\":\"{}\",\"prop\":0}}]}}", to_hash)
            } else {
                "{\"name\":\"p\",\"proven\":true}".to_string()
            };
            format!("{{\"name\":\"{}\",\"hash\":\"{}\",\"props\":[{}]}}", name, hash, prop)
        })
        .collect();
    format!("{{\"definitions\":[{}]}}", defs.join(","))
}

/// Run oathrs with extra environment variables set.
fn oathrs_env(args: &[String], envs: &[(&str, &str)]) -> std::process::Output {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_oathrs"));
    cmd.args(args);
    for (k, v) in envs {
        cmd.env(k, v);
    }
    cmd.output().expect("run oathrs")
}

/// (a) ROUND-TRIP — emit every shard, merge them, and get the SAME PASS/proven
/// set as the in-process `--verify-shards`.
#[test]
fn parallel_emit_merge_matches_in_process_verify() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("corpus.oath", CORPUS);
    let seed = tmp("seed.json", &seed_json(&corpus, None));
    let n = 3u64;
    let files: Vec<_> = (0..n).map(|i| emit_shard(&seed, &corpus, i, n)).collect();

    let merged = merge(&seed, &corpus, n, &files);
    let m_out = String::from_utf8_lossy(&merged.stdout);
    assert!(merged.status.success(), "merge must PASS: {}\n{}", m_out, String::from_utf8_lossy(&merged.stderr));
    assert!(m_out.contains("PASS\tunion == S"), "merge prints PASS: {}", m_out);

    let verify = oathrs(&["prove", "--verify-shards", &n.to_string(), "--hints", seed.to_str().unwrap(), corpus.to_str().unwrap()]);
    let v_out = String::from_utf8_lossy(&verify.stdout);
    assert!(verify.status.success(), "in-process verify must PASS: {}", v_out);
    // Both report the same "<N> proven properties" and the same seed identity.
    let proven_field = |s: &str| s.lines().find(|l| l.starts_with("PASS")).map(|l| l.to_string());
    assert_eq!(proven_field(&m_out), proven_field(&v_out), "merge and in-process verify agree exactly");

    for f in &files {
        let _ = std::fs::remove_file(f);
    }
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}

/// (b) A TAMPERED or MISSING shard emission fed to `--merge-shards` FAILS —
/// the canonical validation and partition check still apply to external input.
#[test]
fn a_tampered_or_missing_shard_emission_fails_the_merge() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("corpus.oath", CORPUS);
    let seed = tmp("seed.json", &seed_json(&corpus, None));
    let n = 3u64;
    let files: Vec<_> = (0..n).map(|i| emit_shard(&seed, &corpus, i, n)).collect();

    // A verdict line is one whose first tab-field is a 64-char hash (not a `#`
    // comment, nor the `shard`/`campaign` control lines).
    let has_verdict = |f: &std::path::PathBuf| {
        std::fs::read_to_string(f).unwrap().lines().any(|l| {
            l.split('\t').next().map(|h| h.len() == 64).unwrap_or(false)
        })
    };

    // MISSING: drop one shard entirely — `--merge-shards` requires exactly one
    // emission per index, so a missing index is a loud failure (on stderr) before
    // the union is even checked. Drop a shard that owns a definition, so this
    // would ALSO have failed the partition check had the index rule not caught it.
    let non_empty = files.iter().find(|f| has_verdict(f)).expect("some shard attempted a def").clone();
    let remaining: Vec<_> = files.iter().filter(|f| **f != non_empty).cloned().collect();
    let missing = merge(&seed, &corpus, n, &remaining);
    assert!(!missing.status.success(), "a missing shard must FAIL the merge");
    assert!(
        String::from_utf8_lossy(&missing.stderr).contains("missing shard"),
        "the missing shard index is reported: {}",
        String::from_utf8_lossy(&missing.stderr)
    );

    // TAMPERED: replace one shard's emission with a copy whose `proven` verdict is
    // flipped to `unproven`. All n indices are still present and the campaign id
    // is unchanged, so it reaches the union check and fails there.
    let tampered_text = std::fs::read_to_string(&non_empty).unwrap().replacen("\tproven", "\tunproven", 1);
    let tampered_file = tmp("tampered.txt", &tampered_text);
    let mut with_tamper: Vec<_> = files.iter().filter(|f| **f != non_empty).cloned().collect();
    with_tamper.push(tampered_file.clone());
    let tampered = merge(&seed, &corpus, n, &with_tamper);
    assert!(!tampered.status.success(), "a tampered verdict must FAIL the merge");
    assert!(
        String::from_utf8_lossy(&tampered.stdout).contains("FAIL"),
        "the tampered union is reported FAIL: {}",
        String::from_utf8_lossy(&tampered.stdout)
    );

    for f in files.iter().chain([tampered_file].iter()) {
        let _ = std::fs::remove_file(f);
    }
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}

/// (c) A NON-SELF-CONSISTENT seed makes the merged campaign FAIL even though
/// every individual `--shard` job "succeeded" (exit 0). The seed omits `twice`,
/// which is provable on its own — so the shards re-prove it, and the union
/// records a proof the seed does not (F(S) ⊋ S).
#[test]
fn a_non_self_consistent_seed_fails_the_merged_campaign() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("corpus.oath", CORPUS);
    let seed = tmp("seed-bad.json", &seed_json(&corpus, Some("twice")));
    let n = 2u64;
    // Each shard job exits 0 (emit_shard asserts success) — a green set of jobs.
    let files: Vec<_> = (0..n).map(|i| emit_shard(&seed, &corpus, i, n)).collect();

    let merged = merge(&seed, &corpus, n, &files);
    assert!(
        !merged.status.success(),
        "a non-self-consistent seed must FAIL the merged campaign despite green shard jobs"
    );
    assert!(
        String::from_utf8_lossy(&merged.stdout).contains("FAIL"),
        "the campaign is reported FAIL: {}",
        String::from_utf8_lossy(&merged.stdout)
    );

    for f in &files {
        let _ = std::fs::remove_file(f);
    }
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}

/// (1a) Two emissions with the SAME proven set S but DIFFERENT hints must be
/// REJECTED on campaign-identity mismatch — a proof outcome is a function of the
/// hints (they enter the script bytes, §7.2), so a differently-hinted run
/// computed a different F. Shards are emitted with a hinted seed; the merge uses
/// the un-hinted seed (identical S).
#[test]
fn a_different_hints_context_is_rejected_by_the_merge() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("corpus.oath", CORPUS);
    let hinted = tmp("seed-hinted.json", &seed_json_hinted(&corpus, "quad", "unrelated"));
    let plain = tmp("seed-plain.json", &seed_json(&corpus, None));
    let n = 2u64;
    let files: Vec<_> = (0..n).map(|i| emit_shard(&hinted, &corpus, i, n)).collect();

    let merged = merge(&plain, &corpus, n, &files);
    assert!(!merged.status.success(), "same S but different hints must be rejected");
    assert!(
        String::from_utf8_lossy(&merged.stderr).contains("campaign"),
        "rejected on campaign-identity mismatch: {}",
        String::from_utf8_lossy(&merged.stderr)
    );

    for f in &files {
        let _ = std::fs::remove_file(f);
    }
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&hinted);
    let _ = std::fs::remove_file(&plain);
}

/// (1b) An emission produced under a different RLIMIT (a proxy for any
/// solver/rlimit change, since all fold into the one campaign id) is rejected.
/// Shards are emitted under one explicit rlimit and merged under another.
#[test]
fn a_different_rlimit_context_is_rejected_by_the_merge() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("corpus.oath", CORPUS);
    let seed = tmp("seed.json", &seed_json(&corpus, None));
    let n = 2u64;
    // Emit under an explicit rlimit.
    let files: Vec<_> = (0..n)
        .map(|i| {
            let out = oathrs_env(
                &[
                    "prove".into(),
                    "--shard".into(),
                    format!("{}/{}", i, n),
                    "--hints".into(),
                    seed.to_str().unwrap().into(),
                    corpus.to_str().unwrap().into(),
                ],
                &[("OATHRS_Z3_RLIMIT", "400000000")],
            );
            assert!(out.status.success(), "emit failed: {}", String::from_utf8_lossy(&out.stderr));
            tmp(&format!("shard-{}.txt", i), &String::from_utf8_lossy(&out.stdout))
        })
        .collect();

    // Merge under a DIFFERENT rlimit — the recomputed campaign id differs.
    let merged = oathrs_env(&merge_args(&seed, &corpus, n, &files), &[("OATHRS_Z3_RLIMIT", "400000001")]);
    assert!(!merged.status.success(), "a different rlimit must be rejected");
    assert!(
        String::from_utf8_lossy(&merged.stderr).contains("campaign"),
        "rejected on campaign-identity mismatch: {}",
        String::from_utf8_lossy(&merged.stderr)
    );

    for f in &files {
        let _ = std::fs::remove_file(f);
    }
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}

/// (2) `--merge-shards n` requires EXACTLY ONE emission per shard index. A
/// DUPLICATE index fails loudly (the missing-index case is covered above).
#[test]
fn a_duplicate_shard_index_fails_the_merge() {
    if !have_z3() {
        eprintln!("skipping: z3 not on PATH");
        return;
    }
    let corpus = tmp("corpus.oath", CORPUS);
    let seed = tmp("seed.json", &seed_json(&corpus, None));
    let n = 2u64;
    let shard0 = emit_shard(&seed, &corpus, 0, n);
    let shard1 = emit_shard(&seed, &corpus, 1, n);

    // Supply shard 0 twice — a duplicate index.
    let dup = merge(&seed, &corpus, n, &[shard0.clone(), shard0.clone(), shard1.clone()]);
    assert!(!dup.status.success(), "a duplicate shard index must FAIL");
    assert!(
        String::from_utf8_lossy(&dup.stderr).contains("more than once"),
        "the duplicate index is reported: {}",
        String::from_utf8_lossy(&dup.stderr)
    );

    let _ = std::fs::remove_file(&shard0);
    let _ = std::fs::remove_file(&shard1);
    let _ = std::fs::remove_file(&corpus);
    let _ = std::fs::remove_file(&seed);
}
