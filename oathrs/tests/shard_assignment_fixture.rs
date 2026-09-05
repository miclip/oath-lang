#![cfg(feature = "prove")]
//! Gated on `prove`: `oathrs::prove` is compiled out without it, and the
//! solver-free build is supported (`cargo test --no-default-features`).

//! SPEC §7.5 shard assignment, checked against `fixtures/prove/shards.txt`.
//!
//! WHY A FIXTURE AND NOT A PROPERTY TEST. Every other check in this repository
//! was blind to the assignment rule's BYTES. The partition is deterministic, the
//! merge recomputes it with the same function it was produced by, and the
//! self-check compares a campaign against itself — so a kernel that hashed the
//! 32 raw bytes of the identity instead of its 64-character hex spelling, or
//! separated with ':' instead of '#', or read the digest little-endian, would
//! produce a perfectly self-consistent campaign and pass everything. It would
//! simply partition the corpus differently from every other kernel, and nothing
//! would say so.
//!
//! The fixture was generated INDEPENDENTLY of this kernel, from the
//! specification text. That is the whole of its value: a fixture produced by the
//! implementation it guards agrees with that implementation by construction.

use std::fs;

fn rows() -> Vec<(String, String, usize, Vec<u64>)> {
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../fixtures/prove/shards.txt");
    // FAIL on a missing fixture rather than skip. Both the fixture and this test
    // are committed, so absence is a defect and never a legitimate environment —
    // and a skip here would let deleting the file turn this gate green.
    let text = fs::read_to_string(path)
        .unwrap_or_else(|e| panic!("fixtures/prove/shards.txt is committed and must be readable: {e}"));
    let mut out = Vec::new();
    for line in text.lines() {
        if line.starts_with('#') || line.trim().is_empty() {
            continue;
        }
        let f: Vec<&str> = line.split('\t').collect();
        assert!(f.len() >= 4, "malformed row: {line}");
        out.push((
            f[0].to_string(),
            f[1].to_string(),
            f[2].parse().expect("prop index"),
            f[3..].iter().map(|s| s.parse().expect("shard")).collect(),
        ));
    }
    assert!(!out.is_empty(), "the fixture parsed to zero rows — this test would assert nothing");
    out
}

/// Mirrors the fixture's columns. NON-POWERS OF TWO are load-bearing: with only
/// powers of two, `v & (n-1)` and even `digest[7] % n` reproduce every row while
/// partitioning wrongly at other counts, so the witness would not witness the
/// modulo or the 64-bit read at all.
const NS: [u64; 7] = [1, 2, 3, 7, 8, 32, 100];

#[test]
fn the_kernel_reproduces_every_pinned_assignment() {
    let rows = rows();
    let mut checked = 0usize;
    for (name, hash, prop, want) in &rows {
        assert_eq!(want.len(), NS.len(), "{name} prop {prop}: column count");
        for (k, n) in NS.iter().enumerate() {
            let got = oathrs::prove::shard_of_prop(hash, *prop, *n);
            assert_eq!(
                got, want[k],
                "SPEC §7.5 assignment diverges: {name} prop {prop} at n={n} -> {got}, fixture pins {}\n\
                 the rule is first_64_bits(SHA-256(hash ++ \"#\" ++ decimal(prop))) mod n, over the \
                 64-char lowercase hex spelling of the hash",
                want[k]
            );
            checked += 1;
        }
    }
    assert_eq!(checked, rows.len() * NS.len());
    eprintln!("§7.5: {} assignments reproduced across n in {:?}", checked, NS);
}

/// n = 1 is the unsharded verifier. Its correct value is known WITHOUT computing
/// any digest, so this is the one column that catches a fixture regenerated from
/// a broken rule — the failure mode a self-generated fixture cannot have.
#[test]
fn n_of_one_sends_everything_to_shard_zero() {
    for (name, hash, prop, want) in &rows() {
        assert_eq!(want[0], 0, "fixture claims {name} prop {prop} is not in shard 0 at n=1");
        assert_eq!(oathrs::prove::shard_of_prop(hash, *prop, 1), 0, "{name} prop {prop}");
    }
}

/// The fixture must DISCRIMINATE: if every property landed in one shard, the
/// comparison above would pass against almost any rule.
#[test]
fn the_fixture_actually_spreads() {
    let rows = rows();
    let last = NS.len() - 1;
    let mut seen = std::collections::BTreeSet::new();
    for (_, _, _, want) in &rows {
        seen.insert(want[last]);
    }
    assert_eq!(
        seen.len() as u64, NS[last],
        "the pinned assignments use only {} of {} shards at n={} — a fixture this \
         degenerate would not distinguish a correct rule from a broken one",
        seen.len(), NS[last], NS[last]
    );
}

/// The fixture claims to pin EVERY property of the corpus. Nothing above checks
/// that: iterating the rows present means a deleted row, a duplicated one, or a
/// newly added corpus property simply is not looked at, and the file goes on
/// passing while covering less than it says. A witness that cannot notice its own
/// gaps is the failure mode this whole fixture exists to remove, one level up.
///
/// The expected set is read from `prove/outcomes.json` — a DIFFERENT fixture,
/// produced by the same run but describing the corpus rather than the partition,
/// so a row lost from one is not lost from the other.
#[test]
fn the_fixture_covers_every_corpus_property_exactly_once() {
    use std::collections::{BTreeMap, BTreeSet};

    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../fixtures/prove/outcomes.json");
    let text = std::fs::read_to_string(path)
        .unwrap_or_else(|e| panic!("fixtures/prove/outcomes.json is committed and must be readable: {e}"));

    // A deliberately small scan: `"hash": "<64 hex>"` followed by its
    // `"prop_count": N`, in document order. Anything unparseable is a hard error
    // rather than a skipped entry — a scanner that silently drops entries would
    // shrink the expected set to match whatever the fixture happens to contain.
    let mut want: BTreeSet<(String, usize)> = BTreeSet::new();
    let mut cur: Option<String> = None;
    let mut defs = 0usize;
    for raw in text.split('"').collect::<Vec<_>>().windows(4) {
        if raw[0] == "hash" && raw[1] == ": " && raw[2].len() == 64
            && raw[2].chars().all(|c| c.is_ascii_hexdigit()) {
            cur = Some(raw[2].to_string());
        }
        if raw[0] == "prop_count" {
            if let (Some(h), Some(n)) = (cur.clone(), raw[1].trim_matches(|c: char| !c.is_ascii_digit()).parse::<usize>().ok()) {
                for p in 0..n { want.insert((h.clone(), p)); }
                if n > 0 { defs += 1; }
                cur = None;
            }
        }
    }
    assert!(defs > 100, "the outcomes scan found only {defs} property-bearing definitions — \
                        it is not reading the file, and every assertion below would be vacuous");

    let mut seen: BTreeMap<(String, usize), usize> = BTreeMap::new();
    for (_, hash, prop, _) in &rows() {
        *seen.entry((hash.clone(), *prop)).or_insert(0) += 1;
    }
    let dupes: Vec<_> = seen.iter().filter(|(_, c)| **c > 1).map(|(k, c)| format!("{}#{} x{c}", &k.0[..12], k.1)).collect();
    assert!(dupes.is_empty(), "shards.txt pins the same property more than once: {dupes:?}");

    let have: BTreeSet<_> = seen.keys().cloned().collect();
    let missing: Vec<_> = want.difference(&have).map(|k| format!("{}#{}", &k.0[..12], k.1)).collect();
    let extra: Vec<_> = have.difference(&want).map(|k| format!("{}#{}", &k.0[..12], k.1)).collect();
    assert!(missing.is_empty(), "shards.txt omits {} corpus propert(ies), e.g. {:?} — \
                                 regenerate it (`oath fixtures <dir>`)", missing.len(), &missing[..missing.len().min(5)]);
    assert!(extra.is_empty(), "shards.txt pins {} propert(ies) the corpus does not have: {:?}",
            extra.len(), &extra[..extra.len().min(5)]);
}
