use oathrs::analyze;
use oathrs::bridge;
use oathrs::check;
use oathrs::elaborate::elaborate_corpus;
use oathrs::fixture::encode_name;
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
            // SPEC §10.0a CONF-FIXTURE-FILENAME: the name is ENCODED.
            let path = format!("{}/{}.bin", dir, encode_name(name));
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
                    // SPEC §10.0a CONF-FIXTURE-FILENAME: the name is ENCODED.
                    let path = format!("{}/{}.txt", dir, encode_name(name));
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
                // SPEC §10.0a CONF-FIXTURE-FILENAME: the name is ENCODED.
                let path = format!("{}/{}.json", dir, encode_name(name));
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
/// `--hints <outcomes.json>` (optional) supplies the author hints (#67) for the
/// run. Hints are store metadata: they are not in `.oath` source and not in the
/// hash, so a kernel that elaborates only source has none — the fixture channel
/// (SPEC §10) is how they reach it. Without the flag the run is hint-free.
fn cmd_prove(paths: &[String], hints_path: Option<&str>) -> i32 {
    use oathrs::prove;
    let hints = match hints_path {
        Some(p) => match read_outcomes(p) {
            Ok((_, h)) => h,
            Err(e) => {
                eprintln!("error: {}", e);
                return 1;
            }
        },
        None => prove::Hints::new(),
    };
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
    // falsified set: any prop falsified under testing. SPEC §5 — only a
    // `falsified` property removes a proof; an `indeterminate` one does not,
    // because a case the tester could not evaluate refuted nothing.
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
    let results = prove::prove_all(&store, &falsified, &hints);
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
            // SPEC §7.2 (#72): three distinct states per property. '+' PROVEN (a
            // fresh proof, or a prior proof carried forward through an abort —
            // never demoted), '-' UNPROVEN (attempted validly, not proven), '!'
            // ABORTED (an attempt was environmentally invalid, so no valid
            // verdict exists and nothing was recorded). '!' is emitted only for
            // an abort with no standing proof: rendering an aborted property as
            // '-' would state "not proven", a claim this run cannot make. The
            // per-property detail (which condition, whether a prior proof was
            // carried) goes to stderr, so this stdout surface stays the pinned
            // conformance channel.
            let flags: String = r
                .proven
                .iter()
                .zip(r.aborted.iter())
                .map(|(p, a)| if *p { '+' } else if *a { '!' } else { '-' })
                .collect();
            println!("{}\t{}/{}\t{}", name, count, r.proven.len(), flags);
        }
    }
    0
}

// ---------------------------------------------------------------------------
// A minimal JSON reader for the fixture channel (SPEC §10). Only
// `prove/outcomes.json` is read this way — the recorded proof state, and (#67)
// the per-property author hints, which are store METADATA and therefore cannot
// be recovered from `.oath` source. Nothing here touches identity; it is CLI
// plumbing, not kernel semantics, which is why it lives in the binary rather
// than the library.
// ---------------------------------------------------------------------------

#[cfg(feature = "prove")]
#[derive(Debug, Clone)]
enum Json {
    Null,
    Bool(bool),
    Num(String),
    Str(String),
    Arr(Vec<Json>),
    Obj(Vec<(String, Json)>),
}

#[cfg(feature = "prove")]
impl Json {
    fn get(&self, key: &str) -> Option<&Json> {
        match self {
            Json::Obj(kvs) => kvs.iter().find(|(k, _)| k == key).map(|(_, v)| v),
            _ => None,
        }
    }
    fn arr(&self) -> &[Json] {
        match self {
            Json::Arr(v) => v,
            _ => &[],
        }
    }
    fn str(&self) -> Option<&str> {
        match self {
            Json::Str(s) => Some(s),
            _ => None,
        }
    }
    fn usize(&self) -> Option<usize> {
        match self {
            Json::Num(n) => n.parse().ok(),
            _ => None,
        }
    }
    fn is_true(&self) -> bool {
        matches!(self, Json::Bool(true))
    }
}

#[cfg(feature = "prove")]
struct JsonParser<'a> {
    b: &'a [u8],
    i: usize,
}

#[cfg(feature = "prove")]
impl<'a> JsonParser<'a> {
    fn ws(&mut self) {
        while self.i < self.b.len() && (self.b[self.i] as char).is_ascii_whitespace() {
            self.i += 1;
        }
    }
    fn lit(&mut self, s: &str) -> Result<(), String> {
        if self.b[self.i..].starts_with(s.as_bytes()) {
            self.i += s.len();
            Ok(())
        } else {
            Err(format!("expected {} at byte {}", s, self.i))
        }
    }
    fn string(&mut self) -> Result<String, String> {
        self.lit("\"")?;
        let mut out = String::new();
        while self.i < self.b.len() {
            let c = self.b[self.i];
            self.i += 1;
            match c {
                b'"' => return Ok(out),
                b'\\' => {
                    let e = *self.b.get(self.i).ok_or("truncated escape")?;
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
                            let h = std::str::from_utf8(
                                self.b.get(self.i..self.i + 4).ok_or("truncated \\u")?,
                            )
                            .map_err(|e| e.to_string())?;
                            let cp = u32::from_str_radix(h, 16).map_err(|e| e.to_string())?;
                            self.i += 4;
                            out.push(char::from_u32(cp).unwrap_or('\u{fffd}'));
                        }
                        _ => return Err(format!("bad escape \\{}", e as char)),
                    }
                }
                _ => {
                    // Copy the raw UTF-8 byte run for this character.
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
                    out.push_str(
                        std::str::from_utf8(self.b.get(start..self.i).ok_or("truncated utf8")?)
                            .map_err(|e| e.to_string())?,
                    );
                }
            }
        }
        Err("unterminated string".into())
    }
    fn value(&mut self) -> Result<Json, String> {
        self.ws();
        match *self.b.get(self.i).ok_or("unexpected end of JSON")? {
            b'{' => {
                self.i += 1;
                let mut kvs = Vec::new();
                self.ws();
                if self.b.get(self.i) == Some(&b'}') {
                    self.i += 1;
                    return Ok(Json::Obj(kvs));
                }
                loop {
                    self.ws();
                    let k = self.string()?;
                    self.ws();
                    self.lit(":")?;
                    let v = self.value()?;
                    kvs.push((k, v));
                    self.ws();
                    match self.b.get(self.i) {
                        Some(b',') => self.i += 1,
                        Some(b'}') => {
                            self.i += 1;
                            return Ok(Json::Obj(kvs));
                        }
                        _ => return Err(format!("bad object at byte {}", self.i)),
                    }
                }
            }
            b'[' => {
                self.i += 1;
                let mut items = Vec::new();
                self.ws();
                if self.b.get(self.i) == Some(&b']') {
                    self.i += 1;
                    return Ok(Json::Arr(items));
                }
                loop {
                    items.push(self.value()?);
                    self.ws();
                    match self.b.get(self.i) {
                        Some(b',') => self.i += 1,
                        Some(b']') => {
                            self.i += 1;
                            return Ok(Json::Arr(items));
                        }
                        _ => return Err(format!("bad array at byte {}", self.i)),
                    }
                }
            }
            b'"' => Ok(Json::Str(self.string()?)),
            b't' => {
                self.lit("true")?;
                Ok(Json::Bool(true))
            }
            b'f' => {
                self.lit("false")?;
                Ok(Json::Bool(false))
            }
            b'n' => {
                self.lit("null")?;
                Ok(Json::Null)
            }
            _ => {
                let start = self.i;
                while self.i < self.b.len()
                    && matches!(self.b[self.i], b'-' | b'+' | b'.' | b'e' | b'E' | b'0'..=b'9')
                {
                    self.i += 1;
                }
                if start == self.i {
                    return Err(format!("bad value at byte {}", self.i));
                }
                Ok(Json::Num(
                    String::from_utf8_lossy(&self.b[start..self.i]).into_owned(),
                ))
            }
        }
    }
}

#[cfg(feature = "prove")]
fn json_parse(text: &str) -> Result<Json, String> {
    let mut p = JsonParser { b: text.as_bytes(), i: 0 };
    let v = p.value()?;
    p.ws();
    Ok(v)
}

/// Read the recorded proof state out of a `prove/outcomes.json` (SPEC §10): the
/// set of proven `(definition hash, property index)` pairs, plus the per-goal
/// author hints (#67). A hint entry names its target as
/// `{"def": <hash>, "prop": <index>}` — hashes, never names, so it resolves
/// identically in any kernel. The `hints` field is absent when a property has
/// none, so a hint-free corpus reads exactly as it did before the feature.
#[cfg(feature = "prove")]
fn read_outcomes(
    path: &str,
) -> Result<(std::collections::BTreeSet<(String, usize)>, oathrs::prove::Hints), String> {
    let text = std::fs::read_to_string(path).map_err(|e| format!("reading {}: {}", path, e))?;
    let doc = json_parse(&text).map_err(|e| format!("parsing {}: {}", path, e))?;
    let mut proven = std::collections::BTreeSet::new();
    let mut hints = oathrs::prove::Hints::new();
    for d in doc.get("definitions").map(|v| v.arr()).unwrap_or(&[]) {
        let hash = match d.get("hash").and_then(|v| v.str()) {
            Some(h) => h.to_string(),
            None => continue,
        };
        for (pi, p) in d.get("props").map(|v| v.arr()).unwrap_or(&[]).iter().enumerate() {
            if p.get("proven").map(|v| v.is_true()).unwrap_or(false) {
                proven.insert((hash.clone(), pi));
            }
            let hs: Vec<(String, usize)> = p
                .get("hints")
                .map(|v| v.arr())
                .unwrap_or(&[])
                .iter()
                .filter_map(|h| {
                    Some((h.get("def")?.str()?.to_string(), h.get("prop")?.usize()?))
                })
                .collect();
            if !hs.is_empty() {
                hints.insert((hash.clone(), pi), hs);
            }
        }
    }
    Ok((proven, hints))
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
    // Recorded proof state + author hints (#67) from the fixture channel.
    let (proven, hints) = match read_outcomes(outcomes_path) {
        Ok(v) => v,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    println!("# name\tprop\tsha256(direct-attempt script)");
    for (name, idx, sha) in prove::scripts_for(&store, &proven, &hints) {
        println!("{}\t{}\t{}", name, idx, sha);
    }
    0
}

/// SPEC §7.4 bridge obligations. With no argument, print the §7.4.9 manifest;
/// with `--emit <id>`, print that obligation's complete script bytes and
/// nothing else.
///
/// "Complete" depends on the obligation's FAMILY, and the two families do not
/// share a preamble: for a CARRIER obligation (§7.4.2, §7.4.3) it is the §7.4.1
/// core plus the subgoal, and for a TRANSPORT obligation (§7.4.5-§7.4.8) it is
/// the §7.4.4 preamble plus the bridged function's declaration block plus the
/// subgoal.
///
/// This produces TEXT only — no solver is invoked and none need be installed —
/// so it is not behind the `prove` feature. §7.4.9 fixes no solver budget: a
/// caller that RUNS one of these scripts chooses and must state its rlimit,
/// since §7.2 already makes an outcome a function of (script bytes, solver
/// version, rlimit).
fn cmd_bridge_obligation(args: &[String]) -> i32 {
    match args.first().map(|s| s.as_str()) {
        None => {
            print!("{}", bridge::manifest());
            0
        }
        // Exactly `--emit <id>`, nothing after it. A trailing token used to be
        // ignored, so `--emit <id> --prove` printed a script and exited 0 —
        // automation reading that status would record work that never happened.
        // The Go kernel refuses the same shape for the same reason.
        Some("--emit") if args.len() > 2 => {
            eprintln!("unexpected argument after the id: {}", args[2]);
            eprintln!("usage: oathrs bridge-obligation [--emit <id>]");
            1
        }
        Some("--emit") => match args.get(1) {
            None => {
                eprintln!("usage: oathrs bridge-obligation [--emit <id>]");
                1
            }
            Some(id) => match bridge::script(id) {
                Some(s) => {
                    print!("{}", s);
                    0
                }
                None => {
                    eprintln!(
                        "unknown bridge obligation: {} (known: {})",
                        id,
                        bridge::ids().join(", ")
                    );
                    1
                }
            },
        },
        Some(other) => {
            eprintln!("unknown argument: {}", other);
            eprintln!("usage: oathrs bridge-obligation [--emit <id>]");
            1
        }
    }
}

/// Build the SPEC §1.5 golden O1 encoding Defs by hand and compare against the
/// fixture .bin files (byte-identity + manifest hash), then round-trip each
/// through the strict decoder.
fn cmd_enctest(dir: &str) -> i32 {
    // a fixed 32-byte hash reference: 00 11 22 .. ff repeated twice
    let href = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff".to_string();
    let cases: Vec<(&str, Def)> = vec![
        ("bool_bytes", Def::Func { tyvars: 0, ty: Ty::Bool, body: Term::Bool(false), props: vec![] }),
        (
            "empty_lists",
            Def::Func {
                tyvars: 0,
                ty: Ty::Int,
                body: Term::Int(num_bigint::BigInt::from(0)),
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
        ("negative_int", Def::Func { tyvars: 0, ty: Ty::Int, body: Term::Int(num_bigint::BigInt::from(-401)), props: vec![] }),
        // §1.5: a `rat` term whose encoding witnesses the reduced numerator/
        // denominator `bigint` pair (a negative, non-integer rational, -7/4).
        (
            "negative_rat",
            Def::Func {
                tyvars: 0,
                ty: Ty::Rat,
                body: Term::Rat {
                    num: num_bigint::BigInt::from(-7),
                    den: num_bigint::BigInt::from(4),
                },
                props: vec![],
            },
        ),
        // §1.5: a `float` term whose encoding witnesses the 8 big-endian
        // IEEE-754 binary64 bytes (here -2.5 = 0xC004000000000000).
        (
            "negative_float",
            Def::Func {
                tyvars: 0,
                ty: Ty::Float,
                body: Term::Float((-2.5f64).to_bits()),
                props: vec![],
            },
        ),
        (
            "record_order",
            Def::Func {
                tyvars: 0,
                ty: Ty::Record { names: vec!["a".into(), "b".into()], args: vec![Ty::Int, Ty::Bool] },
                body: Term::Record {
                    names: vec!["a".into(), "b".into()],
                    args: vec![Term::Int(num_bigint::BigInt::from(1)), Term::Bool(true)],
                },
                props: vec![],
            },
        ),
    ];
    let mut ok = true;
    for (name, def) in &cases {
        let got = canonical_bytes(def);
        // NOT §10.0a-encoded, deliberately: these are §1.5 golden-encoding CASE
        // names (`bool_bytes`, `record_order`), not definition names, and §10.0a
        // scopes the encoding to "a fixture derived from a definition NAME".
        // Encoding them would rename `bool_bytes.bin` to `bool__bytes.bin`.
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
        // A non-canonical NaN float term must be rejected (SPEC §1.3): every NaN
        // encodes as the one quiet pattern 0x7FF8000000000000.
        ("non-canonical-nan", vec![
            0x4F, 0x31, 0x02, 0, 0, 0, 0, // func, tyvars 0
            0x0A, // ty Float
            0x20, 0x7F, 0xF8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, // float body: NaN with payload 1
            0, 0, 0, 0, // 0 props
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
        eprintln!("usage: oathrs <hash|canon|verify|analyze|bridge-obligation|enctest> ...");
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
        "prove" => {
            let mut hints: Option<String> = None;
            let mut files: Vec<String> = Vec::new();
            let mut i = 2;
            while i < args.len() {
                match args[i].as_str() {
                    "--hints" => {
                        hints = args.get(i + 1).cloned();
                        i += 2;
                    }
                    _ => {
                        files.push(args[i].clone());
                        i += 1;
                    }
                }
            }
            cmd_prove(&files, hints.as_deref())
        }
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
        "bridge-obligation" => cmd_bridge_obligation(&args[2..]),
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
