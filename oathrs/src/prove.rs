//! Proof obligations / SMT boundary (SPEC §7, §7.1, §7.2, §7.3).
//! Translates the provable fragment to SMT-LIB, drives z3 (`z3 -in`), and
//! reproduces per-property proof outcomes. Scripts are fully canonical (SPEC
//! §7.2 script stability): a goal's byte content is a function of the goal and
//! its admissible lemma set, pinned by the byte oracle in prove/scripts.txt.

use crate::analyze::termination;
use crate::elaborate::Store;
use crate::hash::sha256_hex;
use crate::ir::*;
use std::collections::{BTreeMap, BTreeSet, HashSet, VecDeque};
use std::io::{Read, Write};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

// SPEC §7.1/§7.2: the per-goal budget is z3's machine-independent `rlimit`
// resource counter, NOT wall-clock — the outcome is a function of (script bytes,
// solver version, rlimit) and reproduces bit-for-bit across hardware. The
// normative default is 400,000,000 (≈3.5x the heaviest successful proof). A
// wall-clock cap (600s, far above any legitimate rlimit exhaust) survives only
// as a SAFETY net: if it fires before the rlimit is reached, the run is
// invalidated (no outcome is ever recorded), never treated as a timeout verdict.
const DEFAULT_Z3_RLIMIT: u64 = 400_000_000;
const DEFAULT_WALL_CAP_MS: u64 = 600_000;

fn z3_rlimit() -> u64 {
    std::env::var("OATHRS_Z3_RLIMIT")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .filter(|&v| v > 0)
        .unwrap_or(DEFAULT_Z3_RLIMIT)
}

fn z3_wall_cap_ms() -> u64 {
    std::env::var("OATHRS_Z3_WALL_CAP_MS")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .filter(|&v| v > 0)
        .unwrap_or(DEFAULT_WALL_CAP_MS)
}

// SPEC §7.2 attempt validity: z3's `memory_max_size` (megabytes) is OPT-IN
// environment policy — never a default, never outcome-determining. It only
// converts an OS-level death under memory pressure into a clean `memout`
// invalidation; the missing-telemetry clause already covers the OS-death case.
// The spec WARNS that z3 counts its multi-GB upfront arena RESERVATIONS against
// this bound, so any value below the reservation instantly memouts attempts that
// would otherwise run fine — hence no default. Reference env: OATH_PROVE_MEMORY_MB;
// oathrs mirrors the OATHRS_Z3_* convention.
fn z3_memory_mb() -> Option<u64> {
    std::env::var("OATHRS_Z3_MEMORY_MB")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .filter(|&v| v > 0)
}

/// SPEC §7.2 attempt-validity invalidation: the run is non-conformant, so we
/// record NOTHING and abort the whole prove with exit code 3. Called only from
/// `prove_prop`, once a property is decided UNPROVEN while a strategy attempt was
/// invalid (per-attempt GRANULARITY) — never from an individual z3 attempt,
/// which merely reports invalidity and lets a valid strategy win. `msg` names
/// the failing condition.
fn invalidate(msg: &str) -> ! {
    eprintln!("FATAL: {}", msg);
    std::process::exit(3);
}

/// Extract the unsigned integer following `key` in a z3 get-info response
/// (`(:rlimit 400000042)` → the number after `:rlimit`). None if the key is
/// absent or the following token is not an integer.
fn info_int(out: &str, key: &str) -> Option<u64> {
    let after = &out[out.find(key)? + key.len()..];
    let tok: String = after
        .chars()
        .skip_while(|c| c.is_whitespace())
        .take_while(|c| c.is_ascii_digit())
        .collect();
    tok.parse::<u64>().ok()
}

/// Extract the value token following `key` in a get-info response, handling the
/// three forms z3 emits for `:reason-unknown`: a quoted string (`"canceled"`,
/// `"memout"`), a balanced s-expr (`(incomplete (theory arithmetic))`), or a
/// bare word. Quotes are stripped; the s-expr/bare forms are returned verbatim.
fn info_value(out: &str, key: &str) -> Option<String> {
    let rest = out[out.find(key)? + key.len()..].trim_start();
    match rest.chars().next()? {
        '"' => Some(rest[1..].chars().take_while(|&c| c != '"').collect()),
        '(' => {
            let mut depth = 0i32;
            let mut v = String::new();
            for c in rest.chars() {
                v.push(c);
                match c {
                    '(' => depth += 1,
                    ')' => {
                        depth -= 1;
                        if depth == 0 {
                            break;
                        }
                    }
                    _ => {}
                }
            }
            Some(v)
        }
        _ => {
            let v: String = rest.chars().take_while(|&c| !c.is_whitespace() && c != ')').collect();
            (!v.is_empty()).then_some(v)
        }
    }
}

/// SPEC §7.2 attempt validity: classify a non-verdict (z3 returned neither
/// `unsat` nor `sat`) from the appended telemetry. `Ok(Unknown)` is a VALID,
/// reproducible non-proof (recordable as unproven) — a genuine budget exhaust
/// (`canceled` with consumed rlimit ≥ the budget; z3 overshoots by a few units)
/// or a solver incompleteness give-up (any NON-EMPTY, non-`canceled`,
/// non-`memout` reason, a pure function of the script). `Err(reason)` is an
/// INVALID attempt yielding NO EVIDENCE — the caller decides whether it taints:
/// missing telemetry (process died mid-attempt), a BLANK reason (absence of
/// positive evidence — #29 adjudication), `memout` (memory bound fired), and
/// `canceled`-below-budget (external cancel) are all the ENVIRONMENT talking.
fn classify_nonverdict(out: &str) -> Result<Outcome, String> {
    let (rlimit, reason) = match (info_int(out, ":rlimit"), info_value(out, ":reason-unknown")) {
        (Some(r), Some(reason)) => (r, reason),
        _ => {
            return Err("z3 produced a non-verdict but its (get-info) telemetry did not parse — \
                        the process likely died mid-attempt (crash/kill) (SPEC §7.2 attempt \
                        validity: missing telemetry)"
                .to_string())
        }
    };
    let reason = reason.trim();
    if reason.is_empty() {
        // Positive-telemetry rule (§7.2, #29): a blank reason is the ABSENCE of
        // evidence that the attempt was deterministic, not evidence of it.
        return Err("z3 produced a non-verdict with a blank :reason-unknown — no positive \
                    evidence the attempt was deterministic (SPEC §7.2 attempt validity: \
                    blank reason)"
            .to_string());
    }
    if reason == "memout" {
        return Err("z3 hit its memory bound (:reason-unknown = memout) — an environment fact, \
                    not a deterministic outcome (SPEC §7.2 attempt validity: memout)"
            .to_string());
    }
    if reason == "canceled" {
        let budget = z3_rlimit();
        if rlimit >= budget {
            // Genuine deterministic budget exhaust: a valid recordable non-proof.
            return Ok(Outcome::Unknown);
        }
        return Err(format!(
            "z3 was canceled below budget (consumed rlimit {} < {}) — something external \
             canceled the attempt, not the deterministic budget (SPEC §7.2 attempt \
             validity: canceled-below-budget)",
            rlimit, budget
        ));
    }
    // Any non-empty, non-canceled, non-memout reason is a solver incompleteness
    // give-up — a pure function of the script, so a valid recordable non-proof.
    Ok(Outcome::Unknown)
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Outcome {
    Unsat,
    Sat,
    Unknown,
}

// ---------------------------------------------------------------------------
// small type utilities
// ---------------------------------------------------------------------------

fn apply_tyenv(ty: &Ty, tyenv: &[Ty]) -> Ty {
    match ty {
        Ty::Var(i) => tyenv[*i as usize].clone(),
        Ty::Fun(a, b) => Ty::Fun(Box::new(apply_tyenv(a, tyenv)), Box::new(apply_tyenv(b, tyenv))),
        Ty::Data { hash, args } => Ty::Data {
            hash: hash.clone(),
            args: args.iter().map(|a| apply_tyenv(a, tyenv)).collect(),
        },
        Ty::Rec { args } => Ty::Rec { args: args.iter().map(|a| apply_tyenv(a, tyenv)).collect() },
        Ty::Record { names, args } => Ty::Record {
            names: names.clone(),
            args: args.iter().map(|a| apply_tyenv(a, tyenv)).collect(),
        },
        other => other.clone(),
    }
}

fn inst_field(ty: &Ty, tyargs: &[Ty], selfhash: &str) -> Ty {
    match ty {
        Ty::Var(i) => tyargs[*i as usize].clone(),
        Ty::Rec { args } => Ty::Data {
            hash: selfhash.to_string(),
            args: args.iter().map(|a| inst_field(a, tyargs, selfhash)).collect(),
        },
        Ty::Fun(a, b) => Ty::Fun(
            Box::new(inst_field(a, tyargs, selfhash)),
            Box::new(inst_field(b, tyargs, selfhash)),
        ),
        Ty::Data { hash, args } => Ty::Data {
            hash: hash.clone(),
            args: args.iter().map(|a| inst_field(a, tyargs, selfhash)).collect(),
        },
        Ty::Record { names, args } => Ty::Record {
            names: names.clone(),
            args: args.iter().map(|a| inst_field(a, tyargs, selfhash)).collect(),
        },
        other => other.clone(),
    }
}

fn param_types(ty: &Ty, n: usize) -> (Vec<Ty>, Ty) {
    let mut out = Vec::new();
    let mut t = ty.clone();
    for _ in 0..n {
        match t {
            Ty::Fun(a, b) => {
                out.push(*a);
                t = *b;
            }
            _ => break,
        }
    }
    (out, t)
}

fn sanitize(s: &str) -> String {
    s.chars().map(|c| if c.is_ascii_alphanumeric() { c } else { '_' }).collect()
}

fn has_self(t: &Term) -> bool {
    match t {
        Term::SelfRef { .. } => true,
        Term::Lam { a, .. } | Term::Field { a, .. } => has_self(a),
        Term::App { a, b } | Term::Let { a, b, .. } => has_self(a) || has_self(b),
        Term::If { a, b, c } => has_self(a) || has_self(b) || has_self(c),
        Term::Prim { args, .. } | Term::Ctor { args, .. } | Term::Record { args, .. } => {
            args.iter().any(has_self)
        }
        Term::Match { a, arms, .. } => has_self(a) || arms.iter().any(has_self),
        _ => false,
    }
}

fn is_recursive(store: &Store, hash: &str) -> bool {
    matches!(store.def_by_hash.get(hash), Some(Def::Func { body, .. }) if has_self(body))
}

fn is_total(store: &Store, hash: &str) -> bool {
    let mut v = HashSet::new();
    termination(store, hash, &mut v).total()
}

fn strip_lams(body: &Term, n: usize) -> &Term {
    let mut t = body;
    for _ in 0..n {
        match t {
            Term::Lam { a, .. } => t = a,
            _ => break,
        }
    }
    t
}

fn fmt_int(n: i64) -> String {
    if n >= 0 {
        n.to_string()
    } else {
        format!("(- {})", (n as i128).unsigned_abs())
    }
}

// ---------------------------------------------------------------------------
// sort collection
// ---------------------------------------------------------------------------

#[derive(Default)]
struct Sorts {
    order: Vec<String>,
    seen: BTreeSet<String>,
    // name -> constructor-list body for declare-datatypes
    decl: BTreeMap<String, String>,
    // the unified declaration stream (SPEC §7.2): each datatype's own
    // `(declare-datatypes ...)` line AND (appended by register_fun) each
    // `(declare-fun ...)` line, interleaved in first-touch order. \n-terminated.
    decls: Vec<String>,
}

fn sort_of(store: &Store, ty: &Ty, sc: &mut Sorts) -> String {
    match ty {
        Ty::Int => "Int".to_string(),
        Ty::Bool => "Bool".to_string(),
        Ty::Str => "String".to_string(),
        Ty::Fun(a, b) => format!("(Array {} {})", sort_of(store, a, sc), sort_of(store, b, sc)),
        Ty::Data { hash, args } => {
            // SPEC §7.1: a data sort name is the sanitized metadata definition
            // name followed by its sanitized type-argument sorts.
            let di = store.data_by_hash.get(hash).expect("data present");
            let mut name = sanitize(&di.name);
            for a in args {
                name.push('_');
                name.push_str(&sanitize(&sort_of(store, a, sc)));
            }
            if !sc.seen.contains(&name) {
                sc.seen.insert(name.clone());
                sc.order.push(name.clone());
                let di = store.data_by_hash.get(hash).expect("data present");
                let mut body = String::new();
                for (ci, (cname, fields)) in di.ctors.iter().enumerate() {
                    let csmt = format!("{}_{}", sanitize(cname), name);
                    // `(<ctor> <selectors...>)`: a space always follows the ctor
                    // name, so a nullary constructor renders as `(<ctor> )`.
                    body.push_str(" (");
                    body.push_str(&csmt);
                    body.push(' ');
                    let mut sels = Vec::new();
                    for (j, f) in fields.iter().enumerate() {
                        let fty = inst_field(f, args, hash);
                        let fsort = sort_of(store, &fty, sc);
                        sels.push(format!("({}_{} {})", csmt, j, fsort));
                    }
                    body.push_str(&sels.join(" "));
                    body.push(')');
                    let _ = ci;
                }
                sc.decls
                    .push(format!("(declare-datatypes (({} 0)) (({})))\n", name, body.trim()));
                sc.decl.insert(name.clone(), body);
            }
            name
        }
        Ty::Record { names, args } => {
            let mut name = "Rec".to_string();
            for (n, a) in names.iter().zip(args.iter()) {
                name.push('_');
                name.push_str(&sanitize(n));
                name.push('_');
                name.push_str(&sanitize(&sort_of(store, a, sc)));
            }
            if !sc.seen.contains(&name) {
                sc.seen.insert(name.clone());
                sc.order.push(name.clone());
                let mut body = format!(" (mk_{}", name);
                for (n, a) in names.iter().zip(args.iter()) {
                    let fsort = sort_of(store, a, sc);
                    // Field selectors are mk_<recordSort>_<field> (SPEC §7.1).
                    body.push_str(&format!(" (mk_{}_{} {})", name, sanitize(n), fsort));
                }
                body.push(')');
                sc.decls
                    .push(format!("(declare-datatypes (({} 0)) (({})))\n", name, body.trim()));
                sc.decl.insert(name.clone(), body);
            }
            name
        }
        Ty::Rec { .. } | Ty::Var(_) => "Int".to_string(), // unreachable for concrete props
    }
}

fn ctor_smt(cname: &str, sortname: &str) -> String {
    format!("{}_{}", sanitize(cname), sortname)
}

/// `(cname field0 field1 ...)`, or bare `cname` for a nullary constructor.
fn build_ctor(csmt: &str, fields: &[(String, Ty)]) -> String {
    if fields.is_empty() {
        return csmt.to_string();
    }
    let mut e = format!("({}", csmt);
    for (v, _) in fields {
        e.push(' ');
        e.push_str(v);
    }
    e.push(')');
    e
}

// ---------------------------------------------------------------------------
// translation context
// ---------------------------------------------------------------------------

struct Cx<'a> {
    store: &'a Store,
    sc: Sorts,
    fun_decls: BTreeMap<String, String>, // id -> "(declare-fun ...)" (dedup only)
    axioms: BTreeMap<String, String>,    // id -> "(assert ...)"
    axiom_order: Vec<String>,            // axiom ids in build (first-touch) order
    axiomatized: BTreeSet<String>,
    // true once the problem contains quantifiers (recursive fn decls or
    // quantified lemmas); a quantifier-free `sat` is a genuine refutation
    // (SPEC §7.2) and induction cannot add power.
    quantified: bool,
}

impl<'a> Cx<'a> {
    fn new(store: &'a Store) -> Self {
        Cx {
            store,
            sc: Sorts::default(),
            fun_decls: BTreeMap::new(),
            axioms: BTreeMap::new(),
            axiom_order: Vec::new(),
            axiomatized: BTreeSet::new(),
            quantified: false,
        }
    }

    fn instance_id(hash: &str, cargs: &[Ty], sc: &mut Sorts, store: &Store) -> String {
        // SPEC §7.1: a function symbol is its sanitized metadata name followed by
        // its sanitized type-argument sorts (the instance's monomorphisation).
        let fname = store.func_by_hash.get(hash).map(|fi| fi.name.as_str()).unwrap_or("");
        let mut s = format!("fn_{}", sanitize(fname));
        for a in cargs {
            s.push('_');
            s.push_str(&sanitize(&sort_of(store, a, sc)));
        }
        s
    }

    fn register_fun(&mut self, hash: &str, cargs: &[Ty]) -> String {
        let id = Cx::instance_id(hash, cargs, &mut self.sc, self.store);
        if self.fun_decls.contains_key(&id) {
            return id;
        }
        let n = self.store.func_by_hash.get(hash).unwrap().param_names.len();
        let fty = match self.store.def_by_hash.get(hash) {
            Some(Def::Func { ty, .. }) => ty.clone(),
            _ => unreachable!(),
        };
        let (ptys, ret) = param_types(&apply_tyenv(&fty, cargs), n);
        let psorts: Vec<String> = ptys.iter().map(|t| sort_of(self.store, t, &mut self.sc)).collect();
        let retsort = sort_of(self.store, &ret, &mut self.sc);
        let decl = format!("(declare-fun {} ({}) {})", id, psorts.join(" "), retsort);
        // Append to the unified first-touch declaration stream (signature sorts,
        // touched just above, precede this line). A bare declaration introduces
        // NO quantifier — `quantified` is set only when a ∀ defining axiom is
        // actually emitted (build_axioms) or a binder-carrying lemma is
        // translated (build_lemmas), so a non-total callee stays quantifier-free.
        self.sc.decls.push(format!("{}\n", decl));
        self.fun_decls.insert(id.clone(), decl);
        // A bare declaration introduces NO quantifier — `quantified` is set only
        // when a ∀ defining axiom is emitted or a binder-carrying lemma is
        // translated, so a non-total callee stays quantifier-free. Total callees
        // get their defining axiom built EAGERLY here (first-touch order of the
        // axiom's own callees follows immediately after this declaration).
        // Eagerly translate the callee's body (SPEC §7.2 build sequence). This
        // registers further callees — declarations/axioms interleave in
        // call-graph first-touch order — and its side-effect declarations remain
        // even for a NON-total callee whose axiom is ultimately not asserted
        // (no rollback): only the ∀ equation is gated on totality, not the
        // body translation that discovers referenced symbols.
        self.build_axiom(hash, cargs, &id);
        id
    }

    /// Translate one function's defining equation eagerly and, only if the
    /// function is proven total, assert it as an axiom. The body translation
    /// runs regardless (registering further callees); the `axiomatized` guard
    /// keeps self/mutual recursion finite.
    fn build_axiom(&mut self, hash: &str, cargs: &[Ty], id: &str) {
        if self.axiomatized.contains(id) {
            return;
        }
        self.axiomatized.insert(id.to_string());
        let n = self.store.func_by_hash.get(hash).unwrap().param_names.len();
        let (body, fty) = match self.store.def_by_hash.get(hash) {
            Some(Def::Func { body, ty, .. }) => (body.clone(), ty.clone()),
            _ => return,
        };
        let (ptys, _ret) = param_types(&apply_tyenv(&fty, cargs), n);
        let inner = strip_lams(&body, n);
        let mut env: Vec<(String, Ty)> = Vec::new();
        let mut decls = String::new();
        for (j, pt) in ptys.iter().enumerate() {
            let vname = format!("p{}", j);
            let s = sort_of(self.store, pt, &mut self.sc);
            decls.push_str(&format!("({} {}) ", vname, s));
            env.push((vname, pt.clone()));
        }
        let call = {
            let mut c = format!("({}", id);
            for j in 0..n {
                c.push_str(&format!(" p{}", j));
            }
            c.push(')');
            c
        };
        // Translate the body regardless of totality (registers callees, whose
        // declarations remain even when this axiom is not asserted). The axiom
        // itself is asserted ONLY for a proven-total function (§7): a non-total
        // recursive definition can be inconsistent, so it stays uninterpreted.
        if let Ok((rhs, _)) = self.tr(inner, &env, cargs, hash, cargs) {
            if is_total(self.store, hash) {
                let axiom = if n == 0 {
                    format!("(assert (= {} {}))", call, rhs)
                } else {
                    // A ∀ defining axiom introduces a quantifier.
                    self.quantified = true;
                    format!(
                        "(assert (forall ({}) (! (= {} {}) :pattern ({}))))",
                        decls.trim_end(),
                        call,
                        rhs,
                        call
                    )
                };
                self.axiom_order.push(id.to_string());
                self.axioms.insert(id.to_string(), axiom);
            }
        }
        // else: body outside fragment — leave the callee uninterpreted.
    }

    /// Translate a term to (smt-expr, concrete-type). Err => outside fragment.
    fn tr(
        &mut self,
        t: &Term,
        env: &[(String, Ty)],
        tyenv: &[Ty],
        self_hash: &str,
        self_tyargs: &[Ty],
    ) -> Result<(String, Ty), ()> {
        match t {
            Term::Var(i) => {
                let idx = env.len().checked_sub(1 + *i as usize).ok_or(())?;
                Ok(env[idx].clone())
            }
            Term::Int(n) => Ok((fmt_int(*n), Ty::Int)),
            Term::Bool(b) => Ok(((if *b { "true" } else { "false" }).to_string(), Ty::Bool)),
            Term::Str(s) => Ok((format!("\"{}\"", s.replace('"', "\"\"")), Ty::Str)),
            Term::Lam { .. } => Err(()),
            Term::Prim { op, args } => self.tr_prim(op, args, env, tyenv, self_hash, self_tyargs),
            Term::Ctor { hash, idx, tyargs, args } => {
                let cargs: Vec<Ty> = tyargs.iter().map(|t| apply_tyenv(t, tyenv)).collect();
                let dty = Ty::Data { hash: hash.clone(), args: cargs.clone() };
                let sortname = sort_of(self.store, &dty, &mut self.sc);
                let di = self.store.data_by_hash.get(hash).unwrap();
                let cname = ctor_smt(&di.ctors[*idx as usize].0, &sortname);
                if args.is_empty() {
                    Ok((cname, dty))
                } else {
                    let mut e = format!("({}", cname);
                    for a in args {
                        let (ae, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                        e.push(' ');
                        e.push_str(&ae);
                    }
                    e.push(')');
                    Ok((e, dty))
                }
            }
            Term::Record { names, args } => {
                let mut argtys = Vec::new();
                let mut aexprs = Vec::new();
                for a in args {
                    let (ae, at) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                    aexprs.push(ae);
                    argtys.push(at);
                }
                let rty = Ty::Record { names: names.clone(), args: argtys };
                let sortname = sort_of(self.store, &rty, &mut self.sc);
                let mut e = format!("(mk_{}", sortname);
                for ae in &aexprs {
                    e.push(' ');
                    e.push_str(ae);
                }
                e.push(')');
                Ok((e, rty))
            }
            Term::Field { a, op } => {
                let (ae, at) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                match &at {
                    Ty::Record { names, args } => {
                        let i = names.iter().position(|n| n == op).ok_or(())?;
                        let sortname = sort_of(self.store, &at, &mut self.sc);
                        // Field selector: mk_<recordSort>_<field> (SPEC §7.1).
                        Ok((format!("(mk_{}_{} {})", sortname, sanitize(op), ae), args[i].clone()))
                    }
                    _ => Err(()),
                }
            }
            Term::If { a, b, c } => {
                let (ea, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                let (eb, tb) = self.tr(b, env, tyenv, self_hash, self_tyargs)?;
                let (ec, _) = self.tr(c, env, tyenv, self_hash, self_tyargs)?;
                Ok((format!("(ite {} {} {})", ea, eb, ec), tb))
            }
            Term::Let { ty, a, b } => {
                let (ea, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                let lty = apply_tyenv(ty, tyenv);
                let mut env2 = env.to_vec();
                env2.push((ea, lty));
                self.tr(b, &env2, tyenv, self_hash, self_tyargs)
            }
            Term::Match { hash, a, arms } => {
                let (se, sty) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                let sargs = match &sty {
                    Ty::Data { hash: h, args } if h == hash => args.clone(),
                    _ => return Err(()),
                };
                let sortname = sort_of(self.store, &sty, &mut self.sc);
                let di = self.store.data_by_hash.get(hash).unwrap().clone();
                let mut arm_exprs = Vec::new();
                let mut result_ty = None;
                for (i, arm) in arms.iter().enumerate() {
                    let cname = ctor_smt(&di.ctors[i].0, &sortname);
                    let fields: Vec<Ty> =
                        di.ctors[i].1.iter().map(|f| inst_field(f, &sargs, hash)).collect();
                    let mut env2 = env.to_vec();
                    for (j, ft) in fields.iter().enumerate() {
                        env2.push((format!("({}_{} {})", cname, j, se), ft.clone()));
                    }
                    let (ae, at) = self.tr(arm, &env2, tyenv, self_hash, self_tyargs)?;
                    if result_ty.is_none() {
                        result_ty = Some(at);
                    }
                    arm_exprs.push((cname, ae));
                }
                // build ite chain: last arm is the else
                let n = arm_exprs.len();
                let mut expr = arm_exprs[n - 1].1.clone();
                for i in (0..n - 1).rev() {
                    let (cname, ae) = &arm_exprs[i];
                    expr = format!("(ite ((_ is {}) {}) {} {})", cname, se, ae, expr);
                }
                Ok((expr, result_ty.unwrap()))
            }
            Term::App { .. } => self.tr_app(t, env, tyenv, self_hash, self_tyargs),
            Term::Ref { .. } | Term::SelfRef { .. } => Err(()), // bare function value
        }
    }

    fn tr_prim(
        &mut self,
        op: &str,
        args: &[Term],
        env: &[(String, Ty)],
        tyenv: &[Ty],
        self_hash: &str,
        self_tyargs: &[Ty],
    ) -> Result<(String, Ty), ()> {
        if op == "/" || op == "%" {
            return Err(()); // excluded: kernel truncates, SMT-LIB is Euclidean
        }
        let mut e = Vec::new();
        for a in args {
            e.push(self.tr(a, env, tyenv, self_hash, self_tyargs)?.0);
        }
        let (sexpr, ty) = match op {
            "+" => (format!("(+ {} {})", e[0], e[1]), Ty::Int),
            "-" => (format!("(- {} {})", e[0], e[1]), Ty::Int),
            "*" => (format!("(* {} {})", e[0], e[1]), Ty::Int),
            "neg" => (format!("(- {})", e[0]), Ty::Int),
            "<" => (format!("(< {} {})", e[0], e[1]), Ty::Bool),
            "<=" => (format!("(<= {} {})", e[0], e[1]), Ty::Bool),
            "==" => (format!("(= {} {})", e[0], e[1]), Ty::Bool),
            "and" => (format!("(and {} {})", e[0], e[1]), Ty::Bool),
            "or" => (format!("(or {} {})", e[0], e[1]), Ty::Bool),
            "not" => (format!("(not {})", e[0]), Ty::Bool),
            "++" => (format!("(str.++ {} {})", e[0], e[1]), Ty::Str),
            "str-len" => (format!("(str.len {})", e[0]), Ty::Int),
            _ => return Err(()),
        };
        Ok((sexpr, ty))
    }

    fn tr_app(
        &mut self,
        t: &Term,
        env: &[(String, Ty)],
        tyenv: &[Ty],
        self_hash: &str,
        self_tyargs: &[Ty],
    ) -> Result<(String, Ty), ()> {
        // unwind application spine
        let mut args: Vec<&Term> = Vec::new();
        let mut cur = t;
        while let Term::App { a, b } = cur {
            args.push(b);
            cur = a;
        }
        args.reverse();
        let head = cur;
        match head {
            Term::Ref { hash, tyargs } => {
                self.tr_call(hash, tyargs, &args, env, tyenv, self_hash, self_tyargs)
            }
            Term::SelfRef { tyargs } => {
                let sh = self_hash.to_string();
                self.tr_call(&sh, tyargs, &args, env, tyenv, self_hash, self_tyargs)
            }
            _ => {
                // application of a function value: nested select
                let (mut he, mut hty) = self.tr(head, env, tyenv, self_hash, self_tyargs)?;
                for a in &args {
                    let (ae, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                    let cod = match hty {
                        Ty::Fun(_, b) => *b,
                        _ => return Err(()),
                    };
                    he = format!("(select {} {})", he, ae);
                    hty = cod;
                }
                Ok((he, hty))
            }
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn tr_call(
        &mut self,
        hash: &str,
        tyargs: &[Ty],
        args: &[&Term],
        env: &[(String, Ty)],
        tyenv: &[Ty],
        self_hash: &str,
        self_tyargs: &[Ty],
    ) -> Result<(String, Ty), ()> {
        let cargs: Vec<Ty> = tyargs.iter().map(|t| apply_tyenv(t, tyenv)).collect();
        let n = self.store.func_by_hash.get(hash).ok_or(())?.param_names.len();
        if args.len() != n {
            return Err(()); // partial or over-application: outside fragment
        }
        let fty = match self.store.def_by_hash.get(hash) {
            Some(Def::Func { ty, .. }) => ty.clone(),
            _ => return Err(()),
        };
        let (ptys, ret0) = param_types(&apply_tyenv(&fty, &cargs), n);
        if is_recursive(self.store, hash) {
            // Register the callee (declare-fun) BEFORE translating its arguments,
            // so first-touch declaration order follows the call structure head-first.
            let id = self.register_fun(hash, &cargs);
            let mut e = format!("({}", id);
            for a in args.iter() {
                let (ae, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                e.push(' ');
                e.push_str(&ae);
            }
            e.push(')');
            Ok((e, ret0))
        } else {
            // inline: translate arguments, then beta-reduce through the lambda spine
            let mut aexprs = Vec::new();
            for (i, a) in args.iter().enumerate() {
                let (ae, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
                aexprs.push((ae, ptys[i].clone()));
            }
            let body = match self.store.def_by_hash.get(hash) {
                Some(Def::Func { body, .. }) => body.clone(),
                _ => return Err(()),
            };
            let inner = strip_lams(&body, n);
            self.tr(inner, &aexprs, &cargs, hash, &cargs)
        }
    }
}

// ---------------------------------------------------------------------------
// proof driver
// ---------------------------------------------------------------------------

/// One z3 attempt. `Ok(outcome)` is a VALID, reproducible result (`Unsat`/`Sat`
/// verdicts, or a telemetry-backed `Unknown` non-proof). `Err(reason)` is an
/// INVALID attempt yielding NO EVIDENCE (SPEC §7.2 GRANULARITY) — the caller
/// decides whether it taints the run; run_z3 itself NEVER ends the run.
fn run_z3(script: &str) -> Result<Outcome, String> {
    // The budget is the `(set-option :rlimit ...)` inside `script` (deterministic,
    // machine-independent). z3 returns `unknown` when the rlimit is reached — a
    // legitimate "unproven" verdict. We add NO wall-clock timeout to z3 itself
    // (that would make outcomes hardware-dependent); instead we enforce a wall
    // cap ourselves purely as a safety net. The cap is one instance of the
    // general attempt-validity rule (§7.2): if it fires, the attempt yielded no
    // evidence (`Err`), which taints the run only if the property is otherwise
    // unproven — it is not itself a recorded verdict.
    let mut child = match Command::new("z3")
        .arg("-in")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    {
        Ok(c) => c,
        Err(e) => {
            return Err(format!(
                "z3 failed to spawn ({}) — no attempt telemetry exists (SPEC §7.2 attempt \
                 validity: missing telemetry)",
                e
            ))
        }
    };
    if let Some(mut sin) = child.stdin.take() {
        // Runner wrapping (outside the byte-oracle hash): deterministic options
        // are PREPENDED — an OPT-IN memory bound first (only when set; never a
        // default, per §7.2), then the deterministic rlimit budget, then the core
        // script. The attempt-validity telemetry (§7.2) is APPENDED after the
        // script: the consumed rlimit and z3's own reason for any non-verdict.
        // All of this lies outside the bytes the byte oracle hashes.
        if let Some(mb) = z3_memory_mb() {
            let _ = sin.write_all(format!("(set-option :memory_max_size {})\n", mb).as_bytes());
        }
        let _ = sin.write_all(format!("(set-option :rlimit {})\n", z3_rlimit()).as_bytes());
        let _ = sin.write_all(script.as_bytes());
        let _ = sin.write_all(b"(get-info :rlimit)\n(get-info :reason-unknown)\n");
        // stdin dropped here -> EOF, so z3 processes and exits.
    }
    let mut sout = child.stdout.take();
    let cap = Duration::from_millis(z3_wall_cap_ms());
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(_)) => break,
            Ok(None) => {
                if start.elapsed() > cap {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(format!(
                        "z3 exceeded the {}ms wall-clock safety cap before its rlimit (SPEC §7.2 \
                         attempt validity: the wall cap is one instance of the general \
                         no-evidence rule)",
                        z3_wall_cap_ms()
                    ));
                }
                std::thread::sleep(Duration::from_millis(20));
            }
            Err(e) => {
                return Err(format!(
                    "z3 could not be waited on ({}) — the attempt produced no reliable telemetry \
                     (SPEC §7.2 attempt validity: missing telemetry)",
                    e
                ))
            }
        }
    }
    let mut s = String::new();
    if let Some(ref mut o) = sout {
        let _ = o.read_to_string(&mut s);
    }
    // A verdict (`unsat`/`sat`) is an outcome unconditionally. The check-sat
    // result precedes the appended telemetry in z3's output, so the first such
    // line is the verdict.
    for line in s.lines() {
        match line.trim() {
            "unsat" => return Ok(Outcome::Unsat),
            "sat" => return Ok(Outcome::Sat),
            _ => {}
        }
    }
    // No verdict: `Ok(Unknown)` if the appended telemetry proves the attempt was
    // deterministic, else `Err` (an invalid, no-evidence attempt) (§7.2).
    classify_nonverdict(&s)
}

struct Prover<'a> {
    store: &'a Store,
}

impl<'a> Prover<'a> {
    /// Assemble the CORE self-contained script (the bytes the byte oracle
    /// hashes, SPEC §7.2): datatype declarations, then function declarations and
    /// defining-equation axioms in FIRST-TOUCH order of the canonical build,
    /// then `tail` (lemma asserts, binder decls, goal). There is no set-logic
    /// line and no rlimit option — the `(set-option :rlimit …)` is runner
    /// wrapping added in `run_z3`, outside the hashed bytes.
    fn assemble(cx: &Cx, tail: &str) -> String {
        let mut s = String::new();
        // Interleaved first-touch declaration stream: each datatype's own
        // declare-datatypes and each declare-fun, in the order first touched.
        for line in &cx.sc.decls {
            s.push_str(line);
        }
        // All defining-equation axioms, wholesale, in build (first-touch) order.
        for id in &cx.axiom_order {
            s.push_str(cx.axioms.get(id).unwrap());
            s.push('\n');
        }
        s.push_str(tail);
        s
    }

    /// Load the lemma library (SPEC §7.2 construction sequence): translate EVERY
    /// candidate `(def-hash, prop-index, admissible)` in ascending (def-hash,
    /// prop-index) order — even inadmissible ones, so their declarations/axioms
    /// and the `quantified` flag reflect the full candidate set — but emit an
    /// `(assert …)` only for admissible candidates. Returns the concatenated
    /// admissible asserts (already in canonical order).
    fn build_lemmas(&self, cx: &mut Cx, candidates: &[(String, usize, bool)]) -> String {
        let mut ordered: Vec<(String, usize, bool)> = candidates.to_vec();
        ordered.sort_by(|a, b| (a.0.as_str(), a.1).cmp(&(b.0.as_str(), b.1)));
        let mut out = String::new();
        for (hash, pi, adm) in &ordered {
            let prop = match self.store.def_by_hash.get(hash) {
                Some(Def::Func { props, .. }) => props[*pi].clone(),
                _ => continue,
            };
            let mut env = Vec::new();
            let mut qdecls = String::new();
            for (k, bt) in prop.binders.iter().enumerate() {
                let vname = format!("q{}", k);
                let s = sort_of(self.store, bt, &mut cx.sc);
                qdecls.push_str(&format!("({} {}) ", vname, s));
                env.push((vname, bt.clone()));
            }
            // Translate regardless of admissibility (touches decls/axioms).
            let translated = cx.tr(&prop.body, &env, &[], hash, &[]);
            // A binder-carrying candidate contributes a ∀ wrapper → quantified,
            // even when filtered from emission.
            if !prop.binders.is_empty() {
                cx.quantified = true;
            }
            if !*adm {
                continue;
            }
            if let Ok((be, _)) = translated {
                if prop.binders.is_empty() {
                    out.push_str(&format!("(assert {})\n", be));
                } else {
                    out.push_str(&format!("(assert (forall ({}) {}))\n", qdecls.trim_end(), be));
                }
            }
        }
        out
    }

    /// Build the CORE direct-attempt script for a property under `lemmas`
    /// (the exact bytes the byte oracle hashes, SPEC §7.2). Returns (script,
    /// quantified?), or None if the goal is outside the provable fragment.
    fn direct_script(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
    ) -> Option<(String, bool)> {
        let mut cx = Cx::new(self.store);
        // Canonical build order: lemmas first, then the goal (binders, then body).
        let lem = self.build_lemmas(&mut cx, candidates);
        let mut env = Vec::new();
        let mut binder_decls = String::new();
        for (k, bt) in prop.binders.iter().enumerate() {
            let vname = format!("b{}", k);
            let s = sort_of(self.store, bt, &mut cx.sc);
            binder_decls.push_str(&format!("(declare-const {} {})\n", vname, s));
            env.push((vname, bt.clone()));
        }
        let goal = cx.tr(&prop.body, &env, &[], def_hash, &[]).ok()?.0;
        // Script layout: lemma asserts, binder declarations, then the goal.
        let mut tail = String::new();
        tail.push_str(&lem);
        tail.push_str(&binder_decls);
        tail.push_str(&format!("(assert (not {}))\n(check-sat)\n", goal));
        if !cx.quantified {
            tail.push_str("(get-model)\n");
        }
        Some((Prover::assemble(&cx, &tail), cx.quantified))
    }

    /// Direct proof attempt. Returns (attempt, quantified?), where the attempt is
    /// `Ok(outcome)` for a valid result or `Err(reason)` for an invalid attempt
    /// (SPEC §7.2). An outside-fragment goal (no script) is a valid, deterministic
    /// non-proof: `Ok(Unknown)`.
    fn try_direct(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
    ) -> (Result<Outcome, String>, bool) {
        match self.direct_script(def_hash, prop, candidates) {
            Some((script, quantified)) => (run_z3(&script), quantified),
            None => (Ok(Outcome::Unknown), true),
        }
    }

    /// Structural induction on binder `k` (a datatype). `Ok(true)` = proven,
    /// `Ok(false)` = validly failed (no proof), `Err(reason)` = an attempt was
    /// invalid and this strategy is tainted (SPEC §7.2 GRANULARITY).
    fn try_induction_binder(
        &self,
        def_hash: &str,
        prop: &Prop,
        k: usize,
        candidates: &[(String, usize, bool)],
    ) -> Result<bool, String> {
        let (dhash, dargs) = match &prop.binders[k] {
            Ty::Data { hash, args } => (hash.clone(), args.clone()),
            _ => return Ok(false),
        };
        let di = match self.store.data_by_hash.get(&dhash) {
            Some(d) => d.clone(),
            None => return Ok(false),
        };
        let ind_sort = {
            let mut sc = Sorts::default();
            sort_of(self.store, &prop.binders[k], &mut sc)
        };
        for (cname, cfields) in di.ctors.iter() {
            let mut cx = Cx::new(self.store);
            let lem = self.build_lemmas(&mut cx, candidates);
            let sortname = {
                let s = sort_of(self.store, &prop.binders[k], &mut cx.sc);
                s
            };
            let csmt = ctor_smt(cname, &sortname);
            let fields: Vec<Ty> =
                cfields.iter().map(|f| inst_field(f, &dargs, &dhash)).collect();

            let mut decls = String::new();
            // other binders -> constants
            let mut base_env: Vec<Option<(String, Ty)>> = vec![None; prop.binders.len()];
            for (j, bt) in prop.binders.iter().enumerate() {
                if j == k {
                    continue;
                }
                let vname = format!("b{}", j);
                let s = sort_of(self.store, bt, &mut cx.sc);
                decls.push_str(&format!("(declare-const {} {})\n", vname, s));
                base_env[j] = Some((vname, bt.clone()));
            }
            // constructor field constants
            let mut field_consts = Vec::new();
            for (j, ft) in fields.iter().enumerate() {
                let vname = format!("f{}", j);
                let s = sort_of(self.store, ft, &mut cx.sc);
                decls.push_str(&format!("(declare-const {} {})\n", vname, s));
                field_consts.push((vname, ft.clone()));
            }
            let constructed = if fields.is_empty() {
                csmt.clone()
            } else {
                let mut e = format!("({}", csmt);
                for (v, _) in &field_consts {
                    e.push(' ');
                    e.push_str(v);
                }
                e.push(')');
                e
            };

            // induction hypotheses: for each recursive field (same sort), assert
            // the property with the induction binder replaced by that field,
            // other binders universally generalized.
            let mut ih = String::new();
            for (fi, ft) in fields.iter().enumerate() {
                let fsort = {
                    let mut sc = Sorts::default();
                    sort_of(self.store, ft, &mut sc)
                };
                if fsort != ind_sort {
                    continue;
                }
                let mut env = base_env.clone();
                env[k] = Some((field_consts[fi].0.clone(), ft.clone()));
                // universally quantify the other binders
                let mut qdecls = String::new();
                let mut qenv: Vec<(String, Ty)> = Vec::new();
                for (j, slot) in env.iter().enumerate() {
                    if j == k {
                        qenv.push(slot.clone().unwrap());
                        continue;
                    }
                    let bt = &prop.binders[j];
                    let vname = format!("q{}", j);
                    let s = sort_of(self.store, bt, &mut cx.sc);
                    qdecls.push_str(&format!("({} {}) ", vname, s));
                    qenv.push((vname, bt.clone()));
                }
                if let Ok((be, _)) = cx.tr(&prop.body, &qenv, &[], def_hash, &[]) {
                    if qdecls.is_empty() {
                        ih.push_str(&format!("(assert {})\n", be));
                    } else {
                        ih.push_str(&format!("(assert (forall ({}) {}))\n", qdecls.trim_end(), be));
                    }
                }
            }

            // subgoal: property with induction binder = constructed value
            let mut senv: Vec<(String, Ty)> = Vec::new();
            for (j, slot) in base_env.iter().enumerate() {
                if j == k {
                    senv.push((constructed.clone(), prop.binders[k].clone()));
                } else {
                    senv.push(slot.clone().unwrap());
                }
            }
            let goal = match cx.tr(&prop.body, &senv, &[], def_hash, &[]) {
                Ok((g, _)) => g,
                Err(_) => return Ok(false),
            };
                let mut tail = String::new();
            tail.push_str(&lem);
            tail.push_str(&decls);
            tail.push_str(&ih);
            tail.push_str(&format!("(assert (not {}))\n(check-sat)\n", goal));
            // A subgoal must discharge (`unsat`). A valid non-unsat validly fails
            // the strategy; an invalid attempt taints it (SPEC §7.2 GRANULARITY).
            match run_z3(&Prover::assemble(&cx, &tail)) {
                Ok(Outcome::Unsat) => {}
                Ok(_) => return Ok(false),
                Err(reason) => return Err(reason),
            }
        }
        Ok(true)
    }

    /// Translate `prop` with the binders named in `fixed` bound to given SMT
    /// expressions and every other binder universally quantified with a fresh
    /// `q{index}` variable. Returns the (possibly quantified) formula, or None
    /// if the body is outside the fragment.
    fn forall_prop(
        &self,
        cx: &mut Cx,
        prop: &Prop,
        def_hash: &str,
        fixed: &BTreeMap<usize, (String, Ty)>,
    ) -> Option<String> {
        let mut qdecls = String::new();
        let mut env: Vec<(String, Ty)> = Vec::with_capacity(prop.binders.len());
        for (m, bt) in prop.binders.iter().enumerate() {
            if let Some((e, ty)) = fixed.get(&m) {
                env.push((e.clone(), ty.clone()));
            } else {
                let vname = format!("q{}", m);
                let s = sort_of(self.store, bt, &mut cx.sc);
                qdecls.push_str(&format!("({} {}) ", vname, s));
                env.push((vname, bt.clone()));
            }
        }
        match cx.tr(&prop.body, &env, &[], def_hash, &[]) {
            Ok((be, _)) => Some(if qdecls.is_empty() {
                be
            } else {
                format!("(forall ({}) {})", qdecls.trim_end(), be)
            }),
            Err(_) => None,
        }
    }

    /// Lexicographic induction on the ordered binder pair `(i, j)` (SPEC §7.2).
    /// `Ok(true)` iff every subgoal discharges (`unsat`); `Ok(false)` = validly
    /// failed; `Err(reason)` = a subgoal attempt was invalid (taint, §7.2).
    fn try_induction_lex(
        &self,
        def_hash: &str,
        prop: &Prop,
        i: usize,
        j: usize,
        candidates: &[(String, usize, bool)],
    ) -> Result<bool, String> {
        let (dhash_i, dargs_i) = match &prop.binders[i] {
            Ty::Data { hash, args } => (hash.clone(), args.clone()),
            _ => return Ok(false),
        };
        let (dhash_j, dargs_j) = match &prop.binders[j] {
            Ty::Data { hash, args } => (hash.clone(), args.clone()),
            _ => return Ok(false),
        };
        let di = match self.store.data_by_hash.get(&dhash_i) {
            Some(d) => d.clone(),
            None => return Ok(false),
        };
        let dj = match self.store.data_by_hash.get(&dhash_j) {
            Some(d) => d.clone(),
            None => return Ok(false),
        };
        let ind_sort_i = {
            let mut sc = Sorts::default();
            sort_of(self.store, &prop.binders[i], &mut sc)
        };
        let ind_sort_j = {
            let mut sc = Sorts::default();
            sort_of(self.store, &prop.binders[j], &mut sc)
        };
        let is_rec = |ft: &Ty, sort: &str| {
            let mut sc = Sorts::default();
            sort_of(self.store, ft, &mut sc) == *sort
        };

        for (ci, (cname_i, cfields_i)) in di.ctors.iter().enumerate() {
            let fields_i: Vec<Ty> =
                cfields_i.iter().map(|f| inst_field(f, &dargs_i, &dhash_i)).collect();
            let rec_i: Vec<usize> = (0..fields_i.len())
                .filter(|&f| is_rec(&fields_i[f], &ind_sort_i))
                .collect();
            if rec_i.is_empty() {
                // Base case: i := c(fresh), other binders at goal constants, no
                // hypotheses. j is NOT split.
                match self.lex_subgoal(
                    def_hash, prop, candidates, i, j, ci, cname_i, &fields_i, &rec_i, None,
                    &ind_sort_j,
                ) {
                    Ok(true) => {}
                    Ok(false) => return Ok(false),
                    Err(reason) => return Err(reason),
                }
            } else {
                for (cj, (cname_j, cfields_j)) in dj.ctors.iter().enumerate() {
                    let fields_j: Vec<Ty> =
                        cfields_j.iter().map(|f| inst_field(f, &dargs_j, &dhash_j)).collect();
                    match self.lex_subgoal(
                        def_hash,
                        prop,
                        candidates,
                        i,
                        j,
                        ci,
                        cname_i,
                        &fields_i,
                        &rec_i,
                        Some((cj, cname_j.as_str(), &fields_j)),
                        &ind_sort_j,
                    ) {
                        Ok(true) => {}
                        Ok(false) => return Ok(false),
                        Err(reason) => return Err(reason),
                    }
                }
            }
        }
        Ok(true)
    }

    /// One lexicographic subgoal. `jsplit` = Some((cj, cname_j, fields_j)) for a
    /// doubly-split recursive case, None for an i-base case. `Ok(true)` = this
    /// subgoal discharged (`unsat`); `Ok(false)` = valid non-unsat; `Err(reason)`
    /// = the attempt was invalid (SPEC §7.2 GRANULARITY).
    #[allow(clippy::too_many_arguments)]
    fn lex_subgoal(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
        i: usize,
        j: usize,
        _ci: usize,
        cname_i: &str,
        fields_i: &[Ty],
        rec_i: &[usize],
        jsplit: Option<(usize, &str, &[Ty])>,
        ind_sort_j: &str,
    ) -> Result<bool, String> {
        let mut cx = Cx::new(self.store);
        let lem = self.build_lemmas(&mut cx, candidates);
        let mut decls = String::new();

        let sort_i = sort_of(self.store, &prop.binders[i], &mut cx.sc);
        let csmt_i = ctor_smt(cname_i, &sort_i);

        // Other binders (not i, and not j when j is split) become goal constants.
        let mut base_env: Vec<Option<(String, Ty)>> = vec![None; prop.binders.len()];
        for (m, bt) in prop.binders.iter().enumerate() {
            if m == i || (jsplit.is_some() && m == j) {
                continue;
            }
            let vname = format!("b{}", m);
            let s = sort_of(self.store, bt, &mut cx.sc);
            decls.push_str(&format!("(declare-const {} {})\n", vname, s));
            base_env[m] = Some((vname, bt.clone()));
        }

        // Constructor field constants for i.
        let mut fi_consts: Vec<(String, Ty)> = Vec::new();
        for (f, ft) in fields_i.iter().enumerate() {
            let vname = format!("g{}", f);
            let s = sort_of(self.store, ft, &mut cx.sc);
            decls.push_str(&format!("(declare-const {} {})\n", vname, s));
            fi_consts.push((vname, ft.clone()));
        }
        let constructed_i = build_ctor(&csmt_i, &fi_consts);

        // Constructor field constants for j (split case only).
        let mut constructed_j: Option<String> = None;
        let mut rec_j: Vec<usize> = Vec::new();
        let mut fj_consts: Vec<(String, Ty)> = Vec::new();
        if let Some((_cj, cname_j, fields_j)) = jsplit {
            let sort_j = sort_of(self.store, &prop.binders[j], &mut cx.sc);
            let csmt_j = ctor_smt(cname_j, &sort_j);
            for (f, ft) in fields_j.iter().enumerate() {
                let vname = format!("h{}", f);
                let s = sort_of(self.store, ft, &mut cx.sc);
                decls.push_str(&format!("(declare-const {} {})\n", vname, s));
                fj_consts.push((vname, ft.clone()));
                let mut sc = Sorts::default();
                if sort_of(self.store, ft, &mut sc) == *ind_sort_j {
                    rec_j.push(f);
                }
            }
            constructed_j = Some(build_ctor(&csmt_j, &fj_consts));
        }

        // Hypotheses.
        let mut hyps = String::new();
        // (a) i shrinks: for each recursive field of c_i, prop with i := that
        //     field and every other binder universally generalized.
        for &f in rec_i {
            let mut fixed: BTreeMap<usize, (String, Ty)> = BTreeMap::new();
            fixed.insert(i, fi_consts[f].clone());
            if let Some(h) = self.forall_prop(&mut cx, prop, def_hash, &fixed) {
                hyps.push_str(&format!("(assert {})\n", h));
            }
        }
        // (b) j shrinks with i pinned: for each recursive field of c_j, prop with
        //     i pinned to the constructed value, j := that field, rest generalized.
        for &y in &rec_j {
            let mut fixed: BTreeMap<usize, (String, Ty)> = BTreeMap::new();
            fixed.insert(i, (constructed_i.clone(), prop.binders[i].clone()));
            fixed.insert(j, fj_consts[y].clone());
            if let Some(h) = self.forall_prop(&mut cx, prop, def_hash, &fixed) {
                hyps.push_str(&format!("(assert {})\n", h));
            }
        }

        // Subgoal: property with i (and j when split) at the constructed values,
        // other binders at their goal constants.
        let mut senv: Vec<(String, Ty)> = Vec::with_capacity(prop.binders.len());
        for m in 0..prop.binders.len() {
            if m == i {
                senv.push((constructed_i.clone(), prop.binders[i].clone()));
            } else if jsplit.is_some() && m == j {
                senv.push((constructed_j.clone().unwrap(), prop.binders[j].clone()));
            } else {
                senv.push(base_env[m].clone().unwrap());
            }
        }
        let goal = match cx.tr(&prop.body, &senv, &[], def_hash, &[]) {
            Ok((g, _)) => g,
            Err(_) => return Ok(false),
        };
        let mut tail = String::new();
        tail.push_str(&lem);
        tail.push_str(&decls);
        tail.push_str(&hyps);
        tail.push_str(&format!("(assert (not {}))\n(check-sat)\n", goal));
        match run_z3(&Prover::assemble(&cx, &tail)) {
            Ok(Outcome::Unsat) => Ok(true),
            Ok(_) => Ok(false),
            Err(reason) => Err(reason),
        }
    }

    /// Prove one property. SPEC §7.2 GRANULARITY: attempt validity is per-attempt.
    /// A valid `unsat` (or quantifier-free `sat` refutation) from ANY strategy is
    /// positive evidence no environment can fake, so it decides the property
    /// regardless of other attempts' invalidity. An invalid attempt yields NO
    /// evidence — it does not end the run; it only TAINTS the negative case. If no
    /// strategy proves the property and any attempt along the way was invalid, the
    /// property has no valid negative verdict and the whole run is INVALIDATED
    /// here (exit 3); if no attempt was invalid, the property records unproven.
    fn prove_prop(&self, def_hash: &str, prop: &Prop, candidates: &[(String, usize, bool)]) -> bool {
        // First invalid-attempt reason seen while trying to prove this property.
        let mut taint: Option<String> = None;

        let (direct, quantified) = self.try_direct(def_hash, prop, candidates);
        match direct {
            Ok(Outcome::Unsat) => return true, // proven; a valid attempt wins
            Err(reason) => {
                // Invalid direct attempt: no evidence. Fall through to induction —
                // a valid strategy may still prove it (the t-insert case).
                taint.get_or_insert(reason);
            }
            // A valid non-unsat (Unknown, or a quantified Sat): not proven here.
            // For a quantifier-free problem the direct result is final and
            // induction cannot add power — a QF `sat` is a valid refutation.
            Ok(_) => {}
        }

        if quantified {
            for k in 0..prop.binders.len() {
                if !matches!(prop.binders[k], Ty::Data { .. }) {
                    continue;
                }
                match self.try_induction_binder(def_hash, prop, k, candidates) {
                    Ok(true) => return true,
                    Ok(false) => {}
                    Err(reason) => {
                        taint.get_or_insert(reason);
                    }
                }
            }
            // Lexicographic induction on ordered pairs of distinct datatype
            // binders, ascending (i, j); accept the first pair that discharges.
            for i in 0..prop.binders.len() {
                if !matches!(prop.binders[i], Ty::Data { .. }) {
                    continue;
                }
                for j in 0..prop.binders.len() {
                    if i == j || !matches!(prop.binders[j], Ty::Data { .. }) {
                        continue;
                    }
                    match self.try_induction_lex(def_hash, prop, i, j, candidates) {
                        Ok(true) => return true,
                        Ok(false) => {}
                        Err(reason) => {
                            taint.get_or_insert(reason);
                        }
                    }
                }
            }
        }

        // Not proven by any strategy. A tainted negative has no valid verdict:
        // invalidate the run here (SPEC §7.2 GRANULARITY). Otherwise it is an
        // honest, reproducible unproven.
        if let Some(reason) = taint {
            invalidate(&format!(
                "attempt-validity: no strategy proved a property of def {} and an attempt was \
                 invalid, so it has no valid negative verdict — {} — run invalidated, no outcome \
                 recorded (SPEC §7.2 attempt validity: GRANULARITY)",
                &def_hash[..def_hash.len().min(12)],
                reason
            ));
        }
        false
    }
}

// ---------------------------------------------------------------------------
// dependency ordering + public entry
// ---------------------------------------------------------------------------

fn body_and_prop_refs(def: &Def) -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    if let Def::Func { body, props, .. } = def {
        collect_refs(body, &mut out);
        for p in props {
            collect_refs(&p.body, &mut out);
        }
    }
    out
}

fn collect_refs(t: &Term, out: &mut BTreeSet<String>) {
    match t {
        Term::Ref { hash, .. } => {
            out.insert(hash.clone());
        }
        Term::Lam { a, .. } | Term::Field { a, .. } => collect_refs(a, out),
        Term::App { a, b } | Term::Let { a, b, .. } => {
            collect_refs(a, out);
            collect_refs(b, out);
        }
        Term::If { a, b, c } => {
            collect_refs(a, out);
            collect_refs(b, out);
            collect_refs(c, out);
        }
        Term::Prim { args, .. } | Term::Ctor { args, .. } | Term::Record { args, .. } => {
            for a in args {
                collect_refs(a, out);
            }
        }
        Term::Match { a, arms, .. } => {
            collect_refs(a, out);
            for arm in arms {
                collect_refs(arm, out);
            }
        }
        _ => {}
    }
}


/// Every definition hash a type mentions — data instances and (recursively)
/// their type arguments. `Rec` is the self-reference of the datatype being
/// defined and carries no hash of its own.
fn collect_ty_refs(ty: &Ty, out: &mut BTreeSet<String>) {
    match ty {
        Ty::Data { hash, args } => {
            out.insert(hash.clone());
            for a in args {
                collect_ty_refs(a, out);
            }
        }
        Ty::Fun(a, b) => {
            collect_ty_refs(a, out);
            collect_ty_refs(b, out);
        }
        Ty::Rec { args } | Ty::Record { args, .. } => {
            for a in args {
                collect_ty_refs(a, out);
            }
        }
        _ => {}
    }
}

/// Every definition hash a term references — functions (`ref`), datatypes named
/// by constructors/matches/annotations, and datatypes named by instantiation
/// type arguments (SPEC §7.2: data definitions are first-class references).
fn collect_all_refs(t: &Term, out: &mut BTreeSet<String>) {
    match t {
        Term::Ref { hash, tyargs } => {
            out.insert(hash.clone());
            for ty in tyargs {
                collect_ty_refs(ty, out);
            }
        }
        Term::SelfRef { tyargs } => {
            for ty in tyargs {
                collect_ty_refs(ty, out);
            }
        }
        Term::Ctor { hash, tyargs, args, .. } => {
            out.insert(hash.clone());
            for ty in tyargs {
                collect_ty_refs(ty, out);
            }
            for a in args {
                collect_all_refs(a, out);
            }
        }
        Term::Match { hash, a, arms } => {
            out.insert(hash.clone());
            collect_all_refs(a, out);
            for arm in arms {
                collect_all_refs(arm, out);
            }
        }
        Term::Lam { ty, a } => {
            collect_ty_refs(ty, out);
            collect_all_refs(a, out);
        }
        Term::Let { ty, a, b } => {
            collect_ty_refs(ty, out);
            collect_all_refs(a, out);
            collect_all_refs(b, out);
        }
        Term::App { a, b } => {
            collect_all_refs(a, out);
            collect_all_refs(b, out);
        }
        Term::If { a, b, c } => {
            collect_all_refs(a, out);
            collect_all_refs(b, out);
            collect_all_refs(c, out);
        }
        Term::Prim { args, .. } | Term::Record { args, .. } => {
            for a in args {
                collect_all_refs(a, out);
            }
        }
        Term::Field { a, .. } => collect_all_refs(a, out),
        _ => {}
    }
}

/// The definition hashes a member contributes to the footprint closure — its
/// BODY references (SPEC §7.2: props never extend the footprint). A function's
/// body is its term; a datatype's "body" is its constructor field types, so a
/// member datatype's referenced datatypes are members too.
fn body_refs(def: &Def, out: &mut BTreeSet<String>) {
    match def {
        Def::Func { body, .. } => collect_all_refs(body, out),
        Def::Data { ctors, .. } => {
            for fields in ctors {
                for f in fields {
                    collect_ty_refs(f, out);
                }
            }
        }
    }
}

/// A goal's footprint (SPEC §7.2 "lemma relevance"): the definition under proof
/// plus every definition referenced by the property's binders and body, closed
/// transitively through definition bodies (functions through their term,
/// datatypes through their constructor fields). Data and function definitions
/// are both first-class members.
fn footprint(store: &Store, def_hash: &str, prop: &Prop) -> BTreeSet<String> {
    let mut fp = BTreeSet::new();
    let mut queue: VecDeque<String> = VecDeque::new();
    fp.insert(def_hash.to_string());
    queue.push_back(def_hash.to_string());
    let mut seed = BTreeSet::new();
    for bty in &prop.binders {
        collect_ty_refs(bty, &mut seed);
    }
    collect_all_refs(&prop.body, &mut seed);
    for h in seed {
        if fp.insert(h.clone()) {
            queue.push_back(h);
        }
    }
    while let Some(h) = queue.pop_front() {
        if let Some(def) = store.def_by_hash.get(&h) {
            let mut r = BTreeSet::new();
            body_refs(def, &mut r);
            for d in r {
                if fp.insert(d.clone()) {
                    queue.push_back(d);
                }
            }
        }
    }
    fp
}

/// A dependency lemma (a proven property `pi` of `e_hash`) is admissible for a
/// goal iff its definition and every definition its binders/body reference lie
/// inside the goal's footprint (SPEC §7.2). Sibling lemmas bypass this entirely.
fn lemma_admissible(store: &Store, fp: &BTreeSet<String>, e_hash: &str, pi: usize) -> bool {
    if !fp.contains(e_hash) {
        return false;
    }
    match store.def_by_hash.get(e_hash) {
        Some(Def::Func { props, .. }) => {
            let mut r = BTreeSet::new();
            for bty in &props[pi].binders {
                collect_ty_refs(bty, &mut r);
            }
            collect_all_refs(&props[pi].body, &mut r);
            r.iter().all(|d| fp.contains(d))
        }
        _ => false,
    }
}

/// The dependency closure for a definition's lemma candidates (SPEC §7.2):
/// UNIFORM body+props at every level — the seed is the definition's body and
/// property references, and each traversal step likewise adds a dependency's
/// body AND property references (a dependency's own props DO extend the closure).
/// BFS, deduplicated.
fn dep_closure(store: &Store, hash: &str) -> Vec<String> {
    let mut seen = BTreeSet::new();
    let mut order = Vec::new();
    let mut queue: VecDeque<String> = VecDeque::new();
    if let Some(def) = store.def_by_hash.get(hash) {
        for d in body_and_prop_refs(def) {
            queue.push_back(d);
        }
    }
    while let Some(h) = queue.pop_front() {
        if !seen.insert(h.clone()) {
            continue;
        }
        order.push(h.clone());
        if let Some(def) = store.def_by_hash.get(&h) {
            for d in body_and_prop_refs(def) {
                if !seen.contains(&d) {
                    queue.push_back(d);
                }
            }
        }
    }
    order
}

/// The candidate lemma set for property `pi` of `def_hash` given a proven set
/// (SPEC §7.2 construction): every proven property of the transitive dependency
/// closure, plus every recorded-proven property of the definition itself
/// (including `pi` if it is recorded proven). Each is tagged with whether it is
/// ADMISSIBLE for emission — a dependency by the footprint test, an own property
/// by `index != pi` (siblings admissible, the own lemma excluded). All are
/// translated (touching declarations/axioms); only admissible ones are asserted.
/// Sorted by (definition-hash, property-index).
fn candidate_lemmas(
    store: &Store,
    def_hash: &str,
    pi: usize,
    prop: &Prop,
    proven: &BTreeSet<(String, usize)>,
) -> Vec<(String, usize, bool)> {
    let fp = footprint(store, def_hash, prop);
    let mut cands = Vec::new();
    for d in dep_closure(store, def_hash) {
        if let Some(Def::Func { props, .. }) = store.def_by_hash.get(&d) {
            for j in 0..props.len() {
                if proven.contains(&(d.clone(), j)) {
                    cands.push((d.clone(), j, lemma_admissible(store, &fp, &d, j)));
                }
            }
        }
    }
    if let Some(Def::Func { props, .. }) = store.def_by_hash.get(def_hash) {
        for j in 0..props.len() {
            if proven.contains(&(def_hash.to_string(), j)) {
                cands.push((def_hash.to_string(), j, j != pi));
            }
        }
    }
    cands.sort_by(|a, b| (a.0.as_str(), a.1).cmp(&(b.0.as_str(), b.1)));
    cands
}

/// Byte oracle (SPEC §7.2): for every function definition with properties, and
/// every property whose direct goal translates, the sha256 of its DIRECT-attempt
/// core script under the given proven (final lemma) state. Returned as
/// (def name, zero-based prop index, sha256-hex), sorted by name, for comparison
/// against fixtures/prove/scripts.txt. Outside-fragment properties are omitted.
pub fn scripts_for(
    store: &Store,
    proven: &BTreeSet<(String, usize)>,
) -> Vec<(String, usize, String)> {
    let prover = Prover { store };
    let mut by_name: Vec<(String, String)> = store
        .func_by_name
        .iter()
        .map(|(n, fi)| (n.clone(), fi.hash.clone()))
        .collect();
    by_name.sort();
    let mut out = Vec::new();
    for (name, hash) in by_name {
        let props = match store.def_by_hash.get(&hash) {
            Some(Def::Func { props, .. }) => props.clone(),
            _ => continue,
        };
        for (pi, prop) in props.iter().enumerate() {
            let cands = candidate_lemmas(store, &hash, pi, prop, proven);
            if let Some((script, _)) = prover.direct_script(&hash, prop, &cands) {
                out.push((name.clone(), pi, sha256_hex(script.as_bytes())));
            }
        }
    }
    out
}

pub struct ProofResult {
    pub proven: Vec<bool>, // per prop index
}

/// Prove all properties of every func definition; returns per-def results and
/// records proven props so later definitions can use them as lemmas.
pub fn prove_all(store: &Store, falsified: &BTreeSet<String>) -> BTreeMap<String, ProofResult> {
    // process definitions in dependency order (deps first)
    let func_hashes: Vec<String> = store
        .def_by_hash
        .iter()
        .filter(|(_, d)| matches!(d, Def::Func { .. }))
        .map(|(h, _)| h.clone())
        .collect();
    // topological-ish: repeatedly take defs whose deps are all processed
    let mut processed: BTreeSet<String> = BTreeSet::new();
    let mut order: Vec<String> = Vec::new();
    let dep_map: BTreeMap<String, BTreeSet<String>> = func_hashes
        .iter()
        .map(|h| {
            let deps = store
                .def_by_hash
                .get(h)
                .map(|d| body_and_prop_refs(d))
                .unwrap_or_default();
            (h.clone(), deps)
        })
        .collect();
    while order.len() < func_hashes.len() {
        let mut progressed = false;
        for h in &func_hashes {
            if processed.contains(h) {
                continue;
            }
            let deps = &dep_map[h];
            if deps.iter().all(|d| processed.contains(d) || !dep_map.contains_key(d)) {
                order.push(h.clone());
                processed.insert(h.clone());
                progressed = true;
            }
        }
        if !progressed {
            // cycle (none expected); append the rest in hash order
            for h in &func_hashes {
                if !processed.contains(h) {
                    order.push(h.clone());
                    processed.insert(h.clone());
                }
            }
        }
    }

    let prover = Prover { store };

    // TWO-LEVEL proof fixpoint (SPEC §7.2). A budget-limited solver is NON-
    // MONOTONE in its axiom set — a goal that proves from a small lemma set can
    // fail once more (irrelevant) lemmas are asserted and divert the search into
    // rlimit exhaustion — so a proof earned against a partial in-run lemma state
    // may not survive re-derivation from the FINAL recorded state. The recorded
    // verdicts must therefore be RUN-STABLE: S = F(S), where F(S) attempts every
    // non-falsified property once with candidate lemmas drawn from S (fixed for
    // the pass). We iterate F from the empty state to a fixpoint (the conformance
    // outcome is defined as this limit), bounded at 8 rounds; without it a cold
    // run can record a proof its own recorded state cannot reproduce.
    //
    // F(S) itself is the INNER GROWTH FIXPOINT (Gauss-Seidel, NOT a single
    // pass): within a round a property proven in-run immediately joins the
    // candidate pool for later attempts, definitions are visited in dependency
    // order, and within a definition properties are attempted in ascending index
    // order, re-iterating until none newly proves. The candidate state for each
    // attempt is `recorded ∪ in-run` (minus the property's own lemma). Pinning
    // the scheme — not just the stability criterion — is what makes the limit
    // deterministic when more than one self-consistent state exists.
    //
    // Per-property caching keys on the actual candidate lemma set, so an attempt
    // whose candidate set is unchanged (within or across rounds) reuses its prior
    // verdict instead of re-running the solver.
    // Opt-in progress to STDERR (env OATHRS_PROVE_PROGRESS): a "proving <name>"
    // line before each definition (so a reap/crash leaves the in-flight def named
    // in the log — "where it died") and a "done <name> c/m flags" line after its
    // inner-growth loop settles each round (so provisional verdicts land mid-log
    // even if the tail is eaten). Pure output timing on stderr — NOT the stdout
    // conformance surface, never in any fixture, zero effect on verdicts/bytes.
    let progress = std::env::var("OATHRS_PROVE_PROGRESS").is_ok();
    let total_defs = order.len();
    let name_of = |hash: &str| -> String {
        store.func_by_hash.get(hash).map(|fi| fi.name.clone()).unwrap_or_else(|| hash[..8.min(hash.len())].to_string())
    };

    let mut recorded: BTreeSet<(String, usize)> = BTreeSet::new();
    let mut cache: BTreeMap<(String, usize), (Vec<(String, usize, bool)>, bool)> = BTreeMap::new();
    for round in 0..8 {
        if progress {
            eprintln!("[prove] === round {} start ({} defs) ===", round, total_defs);
        }
        let mut in_run: BTreeSet<(String, usize)> = BTreeSet::new();
        // combined = recorded ∪ in_run, kept in sync as in_run grows this round.
        let mut combined = recorded.clone();
        for (di, hash) in order.iter().enumerate() {
            let props = match store.def_by_hash.get(hash) {
                Some(Def::Func { props, .. }) => props.clone(),
                _ => continue,
            };
            if falsified.contains(hash) {
                continue; // never proved (§7.3: upgrade requires tested)
            }
            if progress && !props.is_empty() {
                eprintln!("[prove] r{} proving {} ({}/{})", round, name_of(hash), di + 1, total_defs);
            }
            // Local growth fixpoint over this definition's properties.
            loop {
                let mut changed = false;
                for (pi, prop) in props.iter().enumerate() {
                    let key = (hash.clone(), pi);
                    if in_run.contains(&key) {
                        continue;
                    }
                    let cands = candidate_lemmas(store, hash, pi, prop, &combined);
                    let proved = match cache.get(&key) {
                        Some((c, r)) if *c == cands => *r,
                        _ => {
                            let r = prover.prove_prop(hash, prop, &cands);
                            cache.insert(key.clone(), (cands, r));
                            r
                        }
                    };
                    if proved {
                        in_run.insert(key.clone());
                        combined.insert(key);
                        changed = true;
                    }
                }
                if !changed {
                    break;
                }
            }
            if progress && !props.is_empty() {
                // Provisional verdict = this round's in_run for the def (what will
                // become `recorded` at round end).
                let flags: String = (0..props.len())
                    .map(|pi| if in_run.contains(&(hash.clone(), pi)) { '+' } else { '-' })
                    .collect();
                let cnt = flags.chars().filter(|&c| c == '+').count();
                eprintln!("[prove] r{} done {} {}/{} {}", round, name_of(hash), cnt, props.len(), flags);
            }
        }
        if in_run == recorded {
            if progress {
                eprintln!("[prove] converged after round {}", round);
            }
            break;
        }
        recorded = in_run;
    }

    let mut results: BTreeMap<String, ProofResult> = BTreeMap::new();
    for hash in &order {
        if let Some(Def::Func { props, .. }) = store.def_by_hash.get(hash) {
            let flags = (0..props.len())
                .map(|pi| recorded.contains(&(hash.clone(), pi)))
                .collect();
            results.insert(hash.clone(), ProofResult { proven: flags });
        }
    }
    results
}
