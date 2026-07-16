use oathrs::analyze;
use oathrs::check;
use oathrs::elaborate::elaborate_corpus;
use oathrs::hash::sha256_hex;
use oathrs::ir::*;
use oathrs::verify;
use std::fs;
use std::process::exit;

fn read_files(paths: &[String]) -> Result<Vec<(String, String)>, String> {
    let mut out = Vec::new();
    for p in paths {
        let src = fs::read_to_string(p).map_err(|e| format!("{}: {}", p, e))?;
        out.push((p.clone(), src));
    }
    Ok(out)
}

fn cmd_hash(paths: &[String]) -> i32 {
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    // gate every definition
    for name in &store.order {
        let def = store.def_by_name.get(name).unwrap();
        if let Err(e) = check::check_def(&store, def) {
            eprintln!("gate rejected {}: {}", name, e);
            return 1;
        }
    }
    let mut names: Vec<&String> = store.def_by_name.keys().collect();
    names.sort();
    for name in names {
        let def = store.def_by_name.get(name).unwrap();
        let bytes = canonical_bytes(def);
        println!("{}\t{}", name, sha256_hex(&bytes));
    }
    0
}

fn cmd_canon(paths: &[String], out_dir: Option<&str>) -> i32 {
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    if let Some(dir) = out_dir {
        if let Err(e) = fs::create_dir_all(dir) {
            eprintln!("error: {}: {}", dir, e);
            return 1;
        }
        for (name, def) in &store.def_by_name {
            let bytes = canonical_bytes(def);
            let path = format!("{}/{}.bin", dir, name);
            if let Err(e) = fs::write(&path, &bytes) {
                eprintln!("error: {}: {}", path, e);
                return 1;
            }
        }
    } else {
        let mut names: Vec<&String> = store.def_by_name.keys().collect();
        names.sort();
        for name in names {
            let def = store.def_by_name.get(name).unwrap();
            println!("{}\t{}", name, sha256_hex(&canonical_bytes(def)));
        }
    }
    0
}

fn cmd_verify(paths: &[String], out_dir: Option<&str>) -> i32 {
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    if let Some(dir) = out_dir {
        if let Err(e) = fs::create_dir_all(dir) {
            eprintln!("error: {}: {}", dir, e);
            return 1;
        }
    }
    let names: Vec<String> = store.def_by_name.keys().cloned().collect();
    for name in &names {
        if let Some(text) = verify::verify_text(&store, name) {
            match out_dir {
                Some(dir) => {
                    let path = format!("{}/{}.txt", dir, name);
                    if let Err(e) = fs::write(&path, text.as_bytes()) {
                        eprintln!("error: {}: {}", path, e);
                        return 1;
                    }
                }
                None => {
                    println!("==== {} ====", name);
                    print!("{}", text);
                }
            }
        }
    }
    0
}

fn parse_proofs(path: &str) -> std::collections::BTreeMap<String, Vec<bool>> {
    let mut m = std::collections::BTreeMap::new();
    if let Ok(s) = fs::read_to_string(path) {
        for line in s.lines() {
            let parts: Vec<&str> = line.split('\t').collect();
            if parts.len() == 3 {
                let flags = parts[2].chars().map(|c| c == '+').collect();
                m.insert(parts[0].to_string(), flags);
            }
        }
    }
    m
}

fn cmd_analyze(paths: &[String], out_dir: Option<&str>, proofs_path: Option<&str>) -> i32 {
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let proofs = proofs_path.map(parse_proofs);
    if let Some(dir) = out_dir {
        if let Err(e) = fs::create_dir_all(dir) {
            eprintln!("error: {}: {}", dir, e);
            return 1;
        }
    }
    let names: Vec<String> = store.def_by_name.keys().cloned().collect();
    for name in &names {
        let pf = proofs.as_ref().and_then(|m| m.get(name)).map(|v| v.as_slice());
        let a = analyze::analyze(&store, name, pf);
        let json = analyze::to_json(&a);
        match out_dir {
            Some(dir) => {
                let path = format!("{}/{}.json", dir, name);
                if let Err(e) = fs::write(&path, json.as_bytes()) {
                    eprintln!("error: {}: {}", path, e);
                    return 1;
                }
            }
            None => {
                print!("{}", json);
            }
        }
    }
    0
}

#[cfg(feature = "prove")]
fn cmd_prove(paths: &[String]) -> i32 {
    use oathrs::prove;
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    // falsified set: any prop falsified under testing
    let mut falsified = std::collections::BTreeSet::new();
    for (name, def) in &store.def_by_name {
        if let Def::Func { props, .. } = def {
            if props.is_empty() {
                continue;
            }
            let hash = &store.func_by_name.get(name).unwrap().hash;
            for (pi, prop) in props.iter().enumerate() {
                if let verify::PropResult::Falsified { .. } = verify::run_prop(
                    &store,
                    hash,
                    pi as u64,
                    prop,
                    verify::VERIFY_CASES,
                    verify::VERIFY_FUEL,
                ) {
                    falsified.insert(hash.clone());
                    break;
                }
            }
        }
    }
    let results = prove::prove_all(&store, &falsified);
    // print keyed by name, sorted
    let mut by_name: Vec<(&String, &String)> = store
        .func_by_name
        .iter()
        .map(|(n, fi)| (n, &fi.hash))
        .collect();
    by_name.sort();
    for (name, hash) in by_name {
        if let Some(r) = results.get(hash) {
            let count = r.proven.iter().filter(|b| **b).count();
            let flags: String = r
                .proven
                .iter()
                .map(|b| if *b { '+' } else { '-' })
                .collect();
            println!("{}\t{}/{}\t{}", name, count, r.proven.len(), flags);
        }
    }
    0
}

/// Byte oracle (SPEC §7.2): emit `name\tprop\tsha256` for every property's
/// DIRECT-attempt core script, under the recorded proven (final lemma) state
/// read from an outcomes.json. No solver is run — this is a pure function of
/// the corpus + recorded lemma state, for comparison against
/// fixtures/prove/scripts.txt.
#[cfg(feature = "prove")]
fn cmd_scripts(paths: &[String], outcomes_path: &str) -> i32 {
    use oathrs::prove;
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let text = match std::fs::read_to_string(outcomes_path) {
        Ok(t) => t,
        Err(e) => {
            eprintln!("error reading {}: {}", outcomes_path, e);
            return 1;
        }
    };
    // Recorded proven state: (def hash, zero-based prop index) that are proven.
    // The pretty-printed outcomes.json puts one field per line; a def's "hash"
    // opens it (resetting the prop counter), and each prop's "proven" advances it.
    let mut proven = std::collections::BTreeSet::new();
    let mut cur = String::new();
    let mut pi = 0usize;
    for line in text.lines() {
        let t = line.trim();
        if let Some(rest) = t.strip_prefix("\"hash\":") {
            let h: String = rest.chars().filter(|c| c.is_ascii_hexdigit()).collect();
            if h.len() == 64 {
                cur = h;
                pi = 0;
            }
        } else if t.starts_with("\"proven\":") {
            if t.contains("true") {
                proven.insert((cur.clone(), pi));
            }
            pi += 1;
        }
    }
    println!("# name\tprop\tsha256(direct-attempt script)");
    for (name, idx, sha) in prove::scripts_for(&store, &proven) {
        println!("{}\t{}\t{}", name, idx, sha);
    }
    0
}

/// Build the SPEC §1.5 golden O1 encoding Defs by hand and compare against the
/// fixture .bin files (byte-identity + manifest hash), then round-trip each
/// through the strict decoder.
fn cmd_enctest(dir: &str) -> i32 {
    // raw (unescaped) string: quote, backslash, newline, <>&, U+2028, U+2029
    let raw = "\"\\\n<>&\u{2028}\u{2029}".to_string();
    // a fixed 32-byte hash reference: 00 11 22 .. ff repeated twice
    let href = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff".to_string();
    let cases: Vec<(&str, Def)> = vec![
        ("bool_bytes", Def::Func { tyvars: 0, ty: Ty::Bool, body: Term::Bool(false), props: vec![] }),
        (
            "empty_lists",
            Def::Func {
                tyvars: 0,
                ty: Ty::Int,
                body: Term::Int(0),
                props: vec![Prop { binders: vec![], body: Term::Bool(true) }],
            },
        ),
        (
            "hash_reference",
            Def::Func {
                tyvars: 0,
                ty: Ty::Bool,
                body: Term::Ctor { hash: href, idx: 0, tyargs: vec![], args: vec![Term::Bool(false)] },
                props: vec![],
            },
        ),
        ("negative_int", Def::Func { tyvars: 0, ty: Ty::Int, body: Term::Int(-401), props: vec![] }),
        ("raw_strings", Def::Func { tyvars: 0, ty: Ty::Str, body: Term::Str(raw), props: vec![] }),
        (
            "record_order",
            Def::Func {
                tyvars: 0,
                ty: Ty::Record { names: vec!["a".into(), "b".into()], args: vec![Ty::Int, Ty::Str] },
                body: Term::Record {
                    names: vec!["a".into(), "b".into()],
                    args: vec![Term::Int(1), Term::Str("x".into())],
                },
                props: vec![],
            },
        ),
    ];
    let mut ok = true;
    for (name, def) in &cases {
        let got = canonical_bytes(def);
        let path = format!("{}/{}.bin", dir, name);
        let want = match fs::read(&path) {
            Ok(w) => w,
            Err(e) => {
                eprintln!("enctest {}: cannot read {}: {}", name, path, e);
                ok = false;
                continue;
            }
        };
        if got != want {
            eprintln!("enctest {}: BYTES DIFFER (got {} bytes, want {})", name, got.len(), want.len());
            ok = false;
            continue;
        }
        // strict decode + re-encode round-trip must be the identity
        match oathrs::ir::decode(&got) {
            Ok(back) if canonical_bytes(&back) == got && back == *def => {
                println!("enctest {}: ok ({})", name, sha256_hex(&got));
            }
            Ok(_) => {
                eprintln!("enctest {}: round-trip mismatch", name);
                ok = false;
            }
            Err(e) => {
                eprintln!("enctest {}: strict decode failed: {}", name, e);
                ok = false;
            }
        }
    }
    // strict-decoder negative checks (SPEC §1.2): each must be rejected
    let base = canonical_bytes(&Def::Func { tyvars: 0, ty: Ty::Bool, body: Term::Bool(false), props: vec![] });
    let mut trailing = base.clone();
    trailing.push(0x00);
    let mut badbool = base.clone();
    *badbool.last_mut().unwrap() = 0xFF; // the prop-count last byte is 0; flip a bool? use a targeted case below
    let neg: Vec<(&str, Vec<u8>)> = vec![
        ("bad-magic", { let mut v = base.clone(); v[0] = 0x00; v }),
        ("unknown-tag", { let mut v = base.clone(); v[2] = 0x7F; v }),
        ("trailing-bytes", trailing),
        ("malformed-bool", vec![0x4F, 0x31, 0x02, 0, 0, 0, 0, 0x02, 0x12, 0x02, 0, 0, 0, 0]),
        ("unsorted-record", vec![
            0x4F, 0x31, 0x02, 0, 0, 0, 0, // func, tyvars 0
            0x08, 0, 0, 0, 2, 0, 0, 0, 1, b'b', 0x01, 0, 0, 0, 1, b'a', 0x01, // record ty with b,a (descending)
            0x12, 0x00, 0, 0, 0, 0, // body bool false, 0 props
        ]),
    ];
    let _ = badbool;
    for (name, bytes) in &neg {
        match oathrs::ir::decode(bytes) {
            Err(_) => println!("enctest reject {}: ok", name),
            Ok(_) => {
                eprintln!("enctest reject {}: WRONGLY ACCEPTED", name);
                ok = false;
            }
        }
    }
    if ok {
        0
    } else {
        1
    }
}

#[cfg(not(target_arch = "wasm32"))]
fn main() {
    // The evaluator recurses one host stack frame per nested Oath evaluation;
    // the §3.1 depth bound is 100,000, which overflows the default 8 MiB main
    // stack. Run on a worker thread with a large stack (the reference host, Go,
    // grows stacks automatically). wasm32 has no threads — see the wasm main
    // below and DIVERGENCES.md for the depth-bound consequence.
    let child = std::thread::Builder::new()
        .stack_size(2 * 1024 * 1024 * 1024)
        .spawn(run)
        .expect("spawn worker thread");
    let code = child.join().unwrap_or(1);
    exit(code);
}

#[cfg(target_arch = "wasm32")]
fn main() {
    // No threads on wasm32: run on the module's own stack. Deep evaluations
    // (e.g. the non-terminating `spin`, which walks to the 100,000 depth bound)
    // require a correspondingly large wasm stack — configure it at link time
    // (`-C link-arg=-zstack-size=...`) or via the runtime. Terminating examples
    // used by the demo stay well within the default stack.
    exit(run());
}

fn run() -> i32 {
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 2 {
        eprintln!("usage: oathrs <hash|canon|verify|analyze|enctest> ...");
        return 1;
    }
    match args[1].as_str() {
        "hash" => cmd_hash(&args[2..]),
        "canon" => {
            // optional --out DIR
            if args.len() >= 4 && args[2] == "--out" {
                cmd_canon(&args[4..], Some(&args[3]))
            } else {
                cmd_canon(&args[2..], None)
            }
        }
        "verify" => {
            if args.len() >= 4 && args[2] == "--out" {
                cmd_verify(&args[4..], Some(&args[3]))
            } else {
                cmd_verify(&args[2..], None)
            }
        }
        "analyze" => {
            let mut out_dir: Option<String> = None;
            let mut proofs: Option<String> = None;
            let mut files: Vec<String> = Vec::new();
            let mut i = 2;
            while i < args.len() {
                match args[i].as_str() {
                    "--out" => {
                        out_dir = args.get(i + 1).cloned();
                        i += 2;
                    }
                    "--proofs" => {
                        proofs = args.get(i + 1).cloned();
                        i += 2;
                    }
                    _ => {
                        files.push(args[i].clone());
                        i += 1;
                    }
                }
            }
            cmd_analyze(&files, out_dir.as_deref(), proofs.as_deref())
        }
        #[cfg(feature = "prove")]
        "prove" => cmd_prove(&args[2..]),
        #[cfg(feature = "prove")]
        "scripts" => {
            let mut outcomes: Option<String> = None;
            let mut files: Vec<String> = Vec::new();
            let mut i = 2;
            while i < args.len() {
                match args[i].as_str() {
                    "--outcomes" => {
                        outcomes = args.get(i + 1).cloned();
                        i += 2;
                    }
                    _ => {
                        files.push(args[i].clone());
                        i += 1;
                    }
                }
            }
            match outcomes {
                Some(o) => cmd_scripts(&files, &o),
                None => {
                    eprintln!("usage: oathrs scripts --outcomes <outcomes.json> <files>");
                    1
                }
            }
        }
        "enctest" => {
            if args.len() < 3 {
                eprintln!("usage: oathrs enctest <encoding-dir>");
                exit(1);
            }
            cmd_enctest(&args[2])
        }
        other => {
            eprintln!("unknown command: {}", other);
            1
        }
    }
}
