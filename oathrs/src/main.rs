mod check;
mod elaborate;
mod hash;
mod ir;
mod sexpr;

use elaborate::elaborate_corpus;
use hash::sha256_hex;
use ir::*;
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

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 2 {
        eprintln!("usage: oathrs <hash|canon|enctest> ...");
        exit(1);
    }
    let code = match args[1].as_str() {
        "hash" => cmd_hash(&args[2..]),
        "canon" => {
            // optional --out DIR
            if args.len() >= 4 && args[2] == "--out" {
                cmd_canon(&args[4..], Some(&args[3]))
            } else {
                cmd_canon(&args[2..], None)
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
    };
    exit(code);
}
