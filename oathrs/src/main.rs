#[cfg(feature = "prove")]
use std::io::Write;
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
/// The falsified set: a definition any of whose properties is falsified under
/// testing. SPEC §5 — only a `falsified` property removes a proof; an
/// `indeterminate` one does not, because a case the tester could not evaluate
/// refuted nothing. Shared by the plain and the sharded prove paths so a
/// falsified definition is skipped identically in both.
fn compute_falsified(store: &oathrs::elaborate::Store) -> std::collections::BTreeSet<String> {
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
    falsified
}

#[cfg(feature = "prove")]
/// The solver's own version string (`z3 --version`), one of the four inputs a
/// proof outcome is a function of (SPEC §7.2). Folded into the campaign identity
/// so a shard proved under a different z3 cannot be merged. If z3 cannot be run,
/// a distinct sentinel is returned — a shard that could not invoke z3 proved
/// nothing, and the sentinel makes its campaign id differ from any real one.
fn z3_version_string() -> String {
    match std::process::Command::new("z3").arg("--version").output() {
        Ok(o) if o.status.success() => String::from_utf8_lossy(&o.stdout).trim().to_string(),
        _ => "z3-version-unavailable".to_string(),
    }
}

#[cfg(feature = "prove")]
/// Open the SPEC §7.5 per-attempt cost sink for `--cost-out <path>`, or `None`
/// when the flag was not given.
///
/// `Err` on a path that cannot be opened: the emission itself is OPTIONAL, but a
/// destination the operator explicitly named and that cannot be written is a
/// setup error, and silently proceeding would hand back a run whose cost was
/// never recorded and never reported missing. Once the sink is OPEN, every later
/// write failure is a warning only — from that point on nothing may depend on
/// the emission (§7.5).
/// Whether two paths name the SAME FILE, by filesystem identity.
///
/// Identity, not spelling. A canonical-path comparison is a PROXY: it misses a
/// hard link (two names, one inode) and a redirected stdout that is an ordinary
/// file reached by a different route. The claim being checked is "would writing
/// here destroy that", whose owner is the inode, so that is what is compared.
/// A path that does not exist has no identity and cannot alias anything — and
/// creating it therefore destroys nothing.
#[cfg(all(feature = "prove", unix))]
fn same_file(a: &std::path::Path, b: &std::path::Path) -> bool {
    use std::os::unix::fs::MetadataExt;
    match (std::fs::metadata(a), std::fs::metadata(b)) {
        (Ok(x), Ok(y)) => x.dev() == y.dev() && x.ino() == y.ino(),
        _ => false,
    }
}

/// Non-unix: there is no identity check here, so the feature REFUSES rather than
/// approximating one. Canonical paths were the obvious fallback and they are a
/// PROXY that misses a hard link — the same defect this function exists to close,
/// reintroduced where it cannot be tested. oathrs ships linux and macos only (the
/// release matrix), and the wasm build excludes `prove` entirely, so no shipped
/// configuration reaches this. A guard that silently weakens off the tested
/// platforms is worse than one that says it does not run there.
#[cfg(all(feature = "prove", not(unix)))]
fn same_file(_a: &std::path::Path, _b: &std::path::Path) -> bool {
    false
}

/// Whether a destination is this process's standard output, by identity.
///
/// STATS THE DESCRIPTOR, not a path. `/dev/fd/1` is itself a path proxy and does
/// not resolve to the redirected file on every platform — measured: with stdout
/// redirected to an ordinary file on macOS, the path comparison passed and the
/// records went into the shard-result stream. fd 1 is the thing §7.5 requires the
/// emission to stay clear of, so fd 1 is what gets compared.
#[cfg(all(feature = "prove", unix))]
fn resolves_to_stdout(p: &str) -> bool {
    use std::os::unix::fs::MetadataExt;
    use std::os::unix::io::FromRawFd;
    // ManuallyDrop: File::from_raw_fd TAKES OWNERSHIP, and dropping it would close
    // the process's stdout — the guard would break the stream it protects.
    // TWO CHECKS, because each is blind where the other sees. Descriptor identity
    // catches a redirected ORDINARY FILE, which no name reveals. The conventional
    // NAMES catch the case identity cannot: when stdout is a pipe or a tty,
    // stat-ing the path does not report the descriptor's identity, and measured,
    // `--cost-out /dev/stdout` sailed through an identity-only guard.
    // STDERR TOO. §7.5 requires the emission to be separately addressable, and the
    // run's own summary and warnings go to stderr — records interleaved there are
    // no more readable than records interleaved with the shard result.
    const NAMES: [&str; 6] = ["/dev/stdout", "/dev/fd/1", "/proc/self/fd/1",
                              "/dev/stderr", "/dev/fd/2", "/proc/self/fd/2"];
    // Canonicalized BEFORE the name comparison, so `/dev/./stdout` and a symlink
    // to it are not spellings that walk past the check.
    let canon = std::path::Path::new(p).canonicalize();
    let as_str = canon.as_ref().ok().and_then(|c| c.to_str());
    if NAMES.contains(&p) || as_str.map(|c| NAMES.contains(&c)).unwrap_or(false) {
        return true;
    }
    for fd in [1, 2] {
        let s = std::mem::ManuallyDrop::new(unsafe { std::fs::File::from_raw_fd(fd) });
        if let (Ok(a), Ok(b)) = (s.metadata(), std::fs::metadata(p)) {
            if a.dev() == b.dev() && a.ino() == b.ino() {
                return true;
            }
        }
    }
    false
}

#[cfg(all(feature = "prove", not(unix)))]
fn resolves_to_stdout(_p: &str) -> bool {
    false
}

/// Reports the first input `cost_out` would overwrite, if any.
#[cfg(feature = "prove")]
fn aliases_an_input(cost_out: &str, hints: &str, paths: &[String]) -> Option<String> {
    let t = std::path::Path::new(cost_out);
    std::iter::once(hints)
        .chain(paths.iter().map(|s| s.as_str()))
        .filter(|s| !s.is_empty())
        .find(|inp| same_file(t, std::path::Path::new(inp)))
        .map(|s| s.to_string())
}

/// Whether an ALREADY-OPEN handle is one of the files this must not truncate.
///
/// Compared against the OPEN DESCRIPTOR, which is what makes it race-free: the
/// path-based checks can be invalidated between check and open, this cannot.
#[cfg(all(feature = "prove", unix))]
fn opened_handle_is_protected(f: &std::fs::File, protect: &[&str]) -> Option<String> {
    use std::os::unix::fs::MetadataExt;
    use std::os::unix::io::FromRawFd;
    let m = f.metadata().ok()?;
    for fd in [1, 2] {
        let s = std::mem::ManuallyDrop::new(unsafe { std::fs::File::from_raw_fd(fd) });
        if let Ok(a) = s.metadata() {
            if a.dev() == m.dev() && a.ino() == m.ino() {
                return Some(if fd == 1 { "this run's stdout".into() } else { "this run's stderr".into() });
            }
        }
    }
    protect
        .iter()
        .find(|q| {
            std::fs::metadata(q).map(|b| b.dev() == m.dev() && b.ino() == m.ino()).unwrap_or(false)
        })
        .map(|q| format!("an input ({})", q))
}

#[cfg(all(feature = "prove", not(unix)))]
fn opened_handle_is_protected(_f: &std::fs::File, _protect: &[&str]) -> Option<String> {
    Some("unverifiable on this platform".into())
}

#[cfg(feature = "prove")]
fn open_cost_sink(path: Option<&str>, protect: &[&str]) -> Result<Option<oathrs::cost::CostSink>, String> {
    match path {
        None => Ok(None),
        // FAIL CLOSED off the platforms whose file identity this can check. The
        // emission is OPTIONAL (§7.5), so refusing it costs a diagnostic; running
        // it without the input-aliasing and stdout guards risks destroying a
        // corpus file. Refusing and naming the reason is the repository's rule for
        // an unsupported construct.
        Some(p) if !cfg!(unix) => Err(format!(
            "--cost-out {}: the cost emission is supported on unix only — its \
             input-aliasing and stdout guards rest on filesystem identity, and \
             approximating them elsewhere risks truncating an input", p)),
        // §7.5 requires the emission to be SEPARATELY ADDRESSABLE — readable
        // without parsing or locating a shard result. `/dev/stdout` and its
        // equivalents are the shard-result stream itself, so writing records
        // there interleaves the two and leaves stdout parseable as neither.
        Some(p) if resolves_to_stdout(p) => Err(format!(
            "--cost-out {}: the cost emission must not share the shard result's stream (§7.5)", p)),
        Some(p) => {
            // OPEN FIRST, VERIFY THE HANDLE, THEN TRUNCATE. The path checks above
            // are necessarily TOCTOU — the name can be repointed between the
            // check and the open — so the decisive comparison is against the
            // descriptor actually obtained, and truncation happens only after it.
            let f = oathrs::cost::CostSink::open_untruncated(p)
                .map_err(|e| format!("--cost-out {}: {}", p, e))?;
            if let Some(what) = opened_handle_is_protected(&f, protect) {
                return Err(format!(
                    "--cost-out {}: the opened file is {} — refusing to truncate it", p, what));
            }
            oathrs::cost::CostSink::from_verified(f)
                .map(Some)
                .map_err(|e| format!("--cost-out {}: {}", p, e))
        }
    }
}

#[cfg(feature = "prove")]
/// Parse the `i/n` argument of `--shard` into `(i, n)`. Both must be unsigned
/// integers separated by a single slash; anything else is rejected.
fn parse_shard(s: &str) -> Option<(u64, u64)> {
    let (i, n) = s.split_once('/')?;
    Some((i.parse::<u64>().ok()?, n.parse::<u64>().ok()?))
}

#[cfg(feature = "prove")]
/// SPEC §7.5 sharded verification. Three shapes, all requiring `--hints` (the seed
/// S is the `proven` set that file carries):
///   * `--shard i/n`  — run ONE shard i of n (a parallel-campaign job) and emit
///     its contribution on stdout in the `oath-sharded-verification/v2` wire
///     format (keyed by (hash, property), round-trippable). The unit of
///     assignment is a PROPERTY, so a shard normally holds SOME of a
///     definition's properties and attempts no others of it (§7.5). A single
///     shard CANNOT self-check —
///     the union does — so its output is labelled "CONTRIBUTION ONLY … NOT
///     verified until merged" and it exits 0 once the shard runs. Emit-success is
///     not a passing self-check.
///   * `--merge-shards n --shard-in <file> …` — the real parallel-campaign gate:
///     read the n `--shard i/n` emissions (one per `--shard-in` file), reconstruct
///     their `ShardEmission`s, and self-check the union against S property-by-
///     property. Exits non-zero on ANY mismatch, a malformed/foreign emission, or
///     a shard whose declared n or seed identity does not match this merge.
///   * `--verify-shards n` — run ALL n shards in-process (serial) and self-check
///     the union. The single-process equivalent of the shard+merge pipeline;
///     convenient, but without the parallel throughput. Exits non-zero on any
///     mismatch.
/// Elaboration is GLOBAL in every mode: every file is elaborated regardless of
/// shard, and an elaboration failure fails the whole run, from every shard.
///
/// THE EMISSION AND ITS CAMPAIGN IDENTITY ARE KERNEL-LOCAL. §7.5 pins no encoding
/// and no destination for a shard result, and scopes the requirement to "a
/// kernel's own emissions, merged by that same kernel": "Two kernels are NOT
/// expected to merge each other's shard results." So `--merge-shards` consumes
/// what THIS binary's `--shard` produced, and nothing in these bytes is a
/// cross-kernel interchange — §10 compares none of it.
fn cmd_prove_shard(
    paths: &[String],
    hints_path: Option<&str>,
    shard: Option<(u64, u64)>,
    verify_n: Option<u64>,
    merge_n: Option<u64>,
    shard_ins: &[String],
    cost_out: Option<&str>,
) -> i32 {
    use oathrs::prove;
    // The seed S and the author hints both come from the outcomes fixture.
    let hints_path = match hints_path {
        Some(p) => p,
        None => {
            eprintln!("error: sharded verification requires --hints <outcomes.json> (the seed S)");
            return 1;
        }
    };
    let (seed, hints) = match read_outcomes(hints_path) {
        Ok(v) => v,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let files = match read_files(paths) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    // MODE ARGUMENTS ARE VALIDATED BEFORE THE SINK IS OPENED. `--cost-out` creates
    // (and truncates) its file, so an invocation that is going to be rejected
    // anyway must not destroy an existing emission on its way to the error. The
    // per-mode checks below repeat these; that is deliberate, since each mode
    // still owns its own contract.
    if let Some(n) = verify_n {
        if n == 0 {
            eprintln!("error: --verify-shards n requires n >= 1");
            return 1;
        }
    }
    if let Some((i, n)) = shard {
        if n == 0 || i >= n {
            eprintln!("error: --shard i/n requires n >= 1 and i < n");
            return 1;
        }
    }
    // SPEC §7.5: the OPTIONAL cost sink. Opened HERE — after every input has been
    // read, and before any attempt runs — for two independent reasons.
    //
    // A bad destination must fail SETUP rather than lose the first records
    // silently, which is why it is not deferred further.
    //
    // And it must not TRUNCATE AN INPUT. `--cost-out` creates its file, so naming
    // a corpus file or the hints fixture destroyed it before it was read: the
    // proof then ran against an empty file and the user's source was gone.
    // Reading first removes the empty-read half; the aliasing check below removes
    // the data loss, which ordering alone does not — truncating a source file is
    // destructive whether or not it was read first.
    if let Some(cp) = cost_out {
        if let Some(clash) = aliases_an_input(cp, hints_path, paths) {
            eprintln!("error: --cost-out {} would overwrite an input ({})", cp, clash);
            return 1;
        }
    }
    // Elaboration is GLOBAL (SPEC §7.5): an elaboration failure fails the whole
    // run regardless of which shard is requested — a shard MUST NOT be able to
    // suppress an elaboration error because the broken definition falls outside
    // it, which would turn a broken corpus into a green run.
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error (elaboration failed, whole run fails from every shard): {}", e);
            return 1;
        }
    };
    let falsified = compute_falsified(&store);
    let name_of = |hash: &str| -> String {
        store.func_by_hash.get(hash).map(|fi| fi.name.clone()).unwrap_or_else(|| hash.to_string())
    };
    // THE SINK OPENS ONLY ONCE ELABORATION HAS SUCCEEDED. Opening it earlier
    // truncates the destination, so a corpus that parses but fails to elaborate
    // destroyed a previous emission while running no attempt at all — the file is
    // a diagnostic, and losing it must cost at least one attempt's worth of work.
    let protect: Vec<&str> = std::iter::once(hints_path)
        .chain(paths.iter().map(|s| s.as_str()))
        .collect();
    let cost = match open_cost_sink(cost_out, &protect) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };

    if let Some((i, n)) = shard {
        if n == 0 || i >= n {
            eprintln!("error: --shard i/n requires n >= 1 and 0 <= i < n (got {}/{})", i, n);
            return 1;
        }
        let out = prove::prove_shard_cost(&store, &falsified, &hints, &seed, i, n, cost.as_ref());
        if let (Some(sink), Some(p)) = (cost.as_ref(), cost_out) {
            // On STDERR, deliberately: stdout carries the shard result, and §7.5
            // requires the cost emission to live at a destination consuming the
            // shard result never has to parse.
            // NOT eprintln!: it PANICS if stderr is closed, which would make a
            // --cost-out run fail where the same run without it succeeds — and
            // §7.5 requires no run outcome to depend on the emission.
            let _ = writeln!(std::io::stderr(), "# §7.5 cost emission: {} record(s) -> {}", sink.written(), p);
        }
        // Bind the FULL determinism context (S, hints, solver, rlimit) AND THE
        // PARTITION (granularity + n) so a merge can reject a shard that ran a
        // different F, or the same F under a different partition (SPEC §7.5).
        let campaign =
            prove::campaign_identity(&seed, &hints, &z3_version_string(), prove::effective_z3_rlimit(), n);
        // Emit the round-trippable, hash-keyed wire format. The leading banner
        // states in the bytes themselves that this is a CONTRIBUTION, not a
        // verified result — a campaign must run `--merge-shards` to verify.
        print!("{}", prove::format_shard_emission(&out, n, &campaign));
        return 0;
    }

    if let Some(n) = merge_n {
        return merge_shards(&store, &falsified, &hints, &seed, n, shard_ins, &name_of);
    }

    if let Some(n) = verify_n {
        if n == 0 {
            eprintln!("error: --verify-shards n requires n >= 1");
            return 1;
        }
        let report =
            prove::verify_sharded_z3_cost(&store, &falsified, &hints, &seed, n, cost.as_ref());
        eprintln!("# sharded verification (SPEC §7.5), n={} shards", n);
        if let (Some(sink), Some(p)) = (cost.as_ref(), cost_out) {
            // NOT eprintln!: it PANICS if stderr is closed, which would make a
            // --cost-out run fail where the same run without it succeeds — and
            // §7.5 requires no run outcome to depend on the emission.
            let _ = writeln!(std::io::stderr(), "# §7.5 cost emission: {} record(s) -> {}", sink.written(), p);
        }
        eprintln!("# seed S identity: {}", report.seed_id);
        for ((hash, pi), reason) in &report.carried {
            eprintln!(
                "carried-forward (aborted, prior PROVEN in S kept): {} prop {} — {}",
                name_of(hash),
                pi,
                reason
            );
        }
        if report.ok() {
            println!("PASS\tunion == S\t{} proven properties\tseed {}", report.proven.len(), report.seed_id);
            return 0;
        }
        eprintln!("FAIL: the union of the shards' attempt results does NOT equal the seed S ({} mismatch(es)):", report.mismatches.len());
        for m in &report.mismatches {
            eprintln!("  {}", m);
        }
        println!("FAIL\tunion != S\t{} mismatch(es)\tseed {}", report.mismatches.len(), report.seed_id);
        return 1;
    }

    eprintln!("error: sharded prove needs --shard i/n, --merge-shards n, or --verify-shards n");
    1
}

#[cfg(feature = "prove")]
/// SPEC §7.5 parallel-campaign gate: collect the `--shard i/n` emissions, rebuild
/// their `ShardEmission`s, and self-check the union against S. This is where a set
/// of green `--shard` jobs actually becomes a verified result — a shard's own
/// exit 0 means only that it emitted its contribution.
///
/// The seed S and the corpus (hence `store`/`falsified`) are the merge's OWN,
/// derived from `--hints` and the corpus files. The merge recomputes its OWN
/// campaign identity from (S, hints, this machine's z3 version, this run's
/// rlimit) and REJECTS any emission whose declared `n` or campaign identity does
/// not match — so a shard proved under different hints, a different z3, or a
/// different rlimit cannot be assembled into an unsound hybrid (SPEC §7.2/§10.5).
/// It also requires EXACTLY ONE emission per shard index — indices `{0,…,n-1}`,
/// no missing, duplicate, or out-of-range — closing the gap where an omitted
/// shard to which no property was assigned slips past the partition check.
/// (§7.5 states the range explicitly: "`n >= 1` and the shard index satisfies
/// `0 <= i < n` (half-open, which `mod n` can never leave)". It is read off the
/// text, not inferred from the assignment rule.)
/// Every remaining structural check is the canonical validation already inside
/// `merge_and_check` (assignment, range, shardability, one-owner partition,
/// `union == S`).
fn merge_shards(
    store: &oathrs::elaborate::Store,
    falsified: &std::collections::BTreeSet<String>,
    hints: &oathrs::prove::Hints,
    seed: &std::collections::BTreeSet<(String, usize)>,
    n: u64,
    shard_ins: &[String],
    name_of: &dyn Fn(&str) -> String,
) -> i32 {
    use oathrs::prove;
    if n == 0 {
        eprintln!("error: --merge-shards n requires n >= 1");
        return 1;
    }
    if shard_ins.is_empty() {
        eprintln!("error: --merge-shards needs the shard emissions via --shard-in <file> (one per shard)");
        return 1;
    }
    // The merge's OWN determinism context: (S, hints, this z3, this rlimit) and
    // its OWN partition (property granularity, this n) — §7.5 requires the
    // campaign identity to bind the partition too, so an emission sharded a
    // different number of ways is rejected by the identity as well as by the
    // explicit `n` check below.
    let expected_campaign =
        prove::campaign_identity(seed, hints, &z3_version_string(), prove::effective_z3_rlimit(), n);
    let mut emissions: Vec<prove::ShardEmission> = Vec::with_capacity(shard_ins.len());
    let mut seen_indices: std::collections::BTreeSet<u64> = std::collections::BTreeSet::new();
    for path in shard_ins {
        let text = match std::fs::read_to_string(path) {
            Ok(t) => t,
            Err(e) => {
                eprintln!("error: reading shard emission {}: {}", path, e);
                return 1;
            }
        };
        let parsed = match prove::parse_shard_emission(&text) {
            Ok(p) => p,
            Err(e) => {
                eprintln!("FAIL: shard emission {} is malformed: {}", path, e);
                return 1;
            }
        };
        // A shard that ran against a different partition or a different
        // determinism context cannot count toward THIS verification.
        if parsed.n != n {
            eprintln!(
                "FAIL: shard emission {} declares n={}, but this merge is n={}",
                path, parsed.n, n
            );
            return 1;
        }
        if parsed.campaign_id != expected_campaign {
            eprintln!(
                "FAIL: shard emission {} ran under campaign {}, but this merge's campaign is {} — different (S, hints, solver, rlimit, partition) cannot be merged",
                path, parsed.campaign_id, expected_campaign
            );
            return 1;
        }
        // EXACTLY ONE emission per shard index: in range, and no duplicate.
        if parsed.i >= n {
            eprintln!("FAIL: shard emission {} declares shard {} which is out of range (0 <= i < {})", path, parsed.i, n);
            return 1;
        }
        if !seen_indices.insert(parsed.i) {
            eprintln!("FAIL: shard index {} was supplied more than once (from {})", parsed.i, path);
            return 1;
        }
        emissions.push(parsed.emission);
    }
    // The collected indices must be exactly {0, 1, …, n-1} — a missing shard is a
    // loud failure, not a silent pass (an omitted shard to which no property was
    // assigned would otherwise slip past the partition check).
    let missing: Vec<String> = (0..n).filter(|i| !seen_indices.contains(i)).map(|i| i.to_string()).collect();
    if !missing.is_empty() {
        eprintln!(
            "FAIL: --merge-shards {} requires one emission per shard; missing shard(s): {}",
            n,
            missing.join(", ")
        );
        return 1;
    }

    let report = prove::merge_and_check(store, falsified, seed, &emissions, n);
    eprintln!("# sharded MERGE (SPEC §7.5), n={} shards, {} emission(s)", n, shard_ins.len());
    eprintln!("# seed S identity: {}", report.seed_id);
    for ((hash, pi), reason) in &report.carried {
        eprintln!(
            "carried-forward (aborted, prior PROVEN in S kept): {} prop {} — {}",
            name_of(hash),
            pi,
            reason
        );
    }
    if report.ok() {
        println!("PASS\tunion == S\t{} proven properties\tseed {}", report.proven.len(), report.seed_id);
        return 0;
    }
    eprintln!("FAIL: the union of the shards' attempt results does NOT equal the seed S ({} mismatch(es)):", report.mismatches.len());
    for m in &report.mismatches {
        eprintln!("  {}", m);
    }
    println!("FAIL\tunion != S\t{} mismatch(es)\tseed {}", report.mismatches.len(), report.seed_id);
    1
}

#[cfg(feature = "prove")]
/// `--hints <outcomes.json>` (optional) supplies the author hints (#67) for the
/// run. Hints are store metadata: they are not in `.oath` source and not in the
/// hash, so a kernel that elaborates only source has none — the fixture channel
/// (SPEC §10) is how they reach it. Without the flag the run is hint-free.
fn cmd_prove(paths: &[String], hints_path: Option<&str>, cost_out: Option<&str>) -> i32 {
    use oathrs::prove;
    // The alias check and the deferred open belong to BOTH entry points, not just
    // the sharded one: `--cost-out` truncates on create, so naming an input here
    // destroys it exactly as it would there. Fixing only the path a report named
    // leaves the same door open one function away.
    if let Some(cp) = cost_out {
        if let Some(clash) = aliases_an_input(cp, hints_path.unwrap_or(""), paths) {
            eprintln!("error: --cost-out {} would overwrite an input ({})", cp, clash);
            return 1;
        }
    }
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
    // Opened only now: every input has been read, so a destination that aliases
    // one can no longer truncate it, and a bad destination still fails setup
    // before any attempt runs.
    let store = match elaborate_corpus(&files) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    // Same ordering as the sharded path: nothing is truncated until the corpus
    // has elaborated.
    let protect: Vec<&str> = hints_path
        .into_iter()
        .chain(paths.iter().map(|s| s.as_str()))
        .collect();
    let cost = match open_cost_sink(cost_out, &protect) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("error: {}", e);
            return 1;
        }
    };
    let falsified = compute_falsified(&store);
    let results = prove::prove_all_cost(&store, &falsified, &hints, cost.as_ref());
    if let (Some(sink), Some(p)) = (cost.as_ref(), cost_out) {
        // NOT eprintln!: it PANICS if stderr is closed, which would make a
        // --cost-out run fail where the same run without it succeeds — and
        // §7.5 requires no run outcome to depend on the emission.
        let _ = writeln!(std::io::stderr(), "# §7.5 cost emission: {} record(s) -> {}", sink.written(), p);
    }
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
            let mut shard: Option<(u64, u64)> = None;
            let mut verify_shards: Option<u64> = None;
            let mut merge_shards: Option<u64> = None;
            let mut shard_ins: Vec<String> = Vec::new();
            // SPEC §7.5 PER-ATTEMPT COST EMISSION (OPTIONAL): the destination for
            // the cost records. It is a FILE, and therefore a destination
            // DISTINCT from the shard result (which goes to stdout) — §7.5
            // requires that consuming either never requires parsing the other.
            let mut cost_out: Option<String> = None;
            let mut files: Vec<String> = Vec::new();
            let mut i = 2;
            let mut bad = false;
            while i < args.len() {
                match args[i].as_str() {
                    "--hints" => {
                        hints = args.get(i + 1).cloned();
                        i += 2;
                    }
                    // SPEC §7.5: `--shard i/n` runs one shard; parsed as two
                    // slash-separated unsigned integers.
                    "--shard" => {
                        match args.get(i + 1).and_then(|s| parse_shard(s)) {
                            Some(pair) => shard = Some(pair),
                            None => {
                                eprintln!("error: --shard expects i/n (e.g. 0/8)");
                                bad = true;
                            }
                        }
                        i += 2;
                    }
                    // SPEC §7.5 self-check: run all n shards in-process and
                    // compare the union to the seed S.
                    "--verify-shards" => {
                        match args.get(i + 1).and_then(|s| s.parse::<u64>().ok()) {
                            Some(n) => verify_shards = Some(n),
                            None => {
                                eprintln!("error: --verify-shards expects an integer n");
                                bad = true;
                            }
                        }
                        i += 2;
                    }
                    // SPEC §7.5 parallel-campaign gate: merge the n `--shard`
                    // emissions supplied via `--shard-in` and self-check the union.
                    "--merge-shards" => {
                        match args.get(i + 1).and_then(|s| s.parse::<u64>().ok()) {
                            Some(n) => merge_shards = Some(n),
                            None => {
                                eprintln!("error: --merge-shards expects an integer n");
                                bad = true;
                            }
                        }
                        i += 2;
                    }
                    // SPEC §7.5: OPTIONAL per-attempt cost emission, one JSON
                    // object per line, written and flushed as each attempt
                    // completes. Omitting the flag emits nothing, and §7.5
                    // requires the run to be identical either way.
                    "--cost-out" => {
                        match args.get(i + 1) {
                            Some(pth) => cost_out = Some(pth.clone()),
                            None => {
                                eprintln!("error: --cost-out expects a file path");
                                bad = true;
                            }
                        }
                        i += 2;
                    }
                    // One shard emission file; repeatable (one per shard).
                    "--shard-in" => {
                        match args.get(i + 1) {
                            Some(p) => shard_ins.push(p.clone()),
                            None => {
                                eprintln!("error: --shard-in expects a file path");
                                bad = true;
                            }
                        }
                        i += 2;
                    }
                    _ => {
                        files.push(args[i].clone());
                        i += 1;
                    }
                }
            }
            let modes = shard.is_some() as u8 + verify_shards.is_some() as u8 + merge_shards.is_some() as u8;
            if bad {
                1
            } else if modes > 1 {
                eprintln!("error: --shard, --merge-shards and --verify-shards are mutually exclusive");
                1
            } else if !shard_ins.is_empty() && merge_shards.is_none() {
                eprintln!("error: --shard-in is only used with --merge-shards");
                1
            } else if cost_out.is_some() && merge_shards.is_some() {
                // A merge runs NO §7.2 property-proof attempts, so there is no
                // per-attempt cost to emit. Refusing is louder than writing an
                // empty file a consumer would read as "this campaign cost
                // nothing".
                eprintln!(
                    "error: --cost-out records §7.2 PROPERTY-PROOF ATTEMPTS; --merge-shards makes none"
                );
                1
            } else if modes == 1 {
                cmd_prove_shard(
                    &files,
                    hints.as_deref(),
                    shard,
                    verify_shards,
                    merge_shards,
                    &shard_ins,
                    cost_out.as_deref(),
                )
            } else {
                cmd_prove(&files, hints.as_deref(), cost_out.as_deref())
            }
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
