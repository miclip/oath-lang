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
        println!("{}\t{}", name, sha256_hex(bytes.as_bytes()));
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
            let path = format!("{}/{}.json", dir, name);
            if let Err(e) = fs::write(&path, bytes.as_bytes()) {
                eprintln!("error: {}: {}", path, e);
                return 1;
            }
        }
    } else {
        let mut names: Vec<&String> = store.def_by_name.keys().collect();
        names.sort();
        for name in names {
            let def = store.def_by_name.get(name).unwrap();
            println!("{}\t{}", name, canonical_bytes(def));
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

/// Build the SPEC §1.5 golden encoding Defs by hand and compare against the
/// fixture directory (byte-identity and manifest hash).
fn cmd_enctest(dir: &str) -> i32 {
    let esc = "\"\\\n<>&\u{2028}\u{2029}".to_string();
    let cases: Vec<(&str, Def)> = vec![
        (
            "empty_props_omitted",
            Def::Func { tyvars: 0, ty: Ty::Int, body: Term::Int(0), props: vec![] },
        ),
        (
            "prop_body_always_present",
            Def::Func {
                tyvars: 0,
                ty: Ty::Bool,
                body: Term::Bool(true),
                props: vec![Prop { binders: vec![], body: Term::Bool(true) }],
            },
        ),
        (
            "string_escapes",
            Def::Func { tyvars: 0, ty: Ty::Str, body: Term::Str(esc), props: vec![] },
        ),
        (
            "zero_ctor_idx_omitted",
            Def::Func { tyvars: 0, ty: Ty::Bool, body: Term::Bool(false), props: vec![] },
        ),
        (
            "zero_var_omitted",
            Def::Func { tyvars: 0, ty: Ty::Int, body: Term::Var(0), props: vec![] },
        ),
    ];
    let mut ok = true;
    for (name, def) in &cases {
        let got = canonical_bytes(def);
        let path = format!("{}/{}.json", dir, name);
        let want = match fs::read_to_string(&path) {
            Ok(w) => w,
            Err(e) => {
                eprintln!("enctest {}: cannot read {}: {}", name, path, e);
                ok = false;
                continue;
            }
        };
        if got != want {
            eprintln!("enctest {}: BYTES DIFFER", name);
            eprintln!("  got:  {}", got);
            eprintln!("  want: {}", want);
            ok = false;
        } else {
            println!("enctest {}: ok ({})", name, sha256_hex(got.as_bytes()));
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
