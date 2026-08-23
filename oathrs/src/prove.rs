//! Proof obligations / SMT boundary (SPEC §7, §7.1, §7.2, §7.3).
//! Translates the provable fragment to SMT-LIB, drives z3 (`z3 -in`), and
//! reproduces per-property proof outcomes. Scripts are fully canonical (SPEC
//! §7.2 script stability): a goal's byte content is a function of the goal and
//! its admissible lemma set, pinned by the byte oracle in prove/scripts.txt.

use crate::analyze::{termination, Term5};
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
// SPEC §7.2 (#50): a goal with at least one datatype-typed binder — a candidate
// for structural induction — runs its DIRECT attempt at this REDUCED budget. The
// direct attempt on such a goal is almost always futile, yet at the full budget
// burns minutes before failing; every direct proof that SUCCEEDS in the corpus
// consumes under ~3K rlimit, so the reduced budget cannot change a direct success
// (unsat is sound at any rlimit and is reached far below it), only fail a futile
// attempt ~100x faster. Structural/lexicographic induction and the full-budget
// FALLBACK (prove_prop) still use DEFAULT_Z3_RLIMIT, so the recorded outcome is
// identical to a kernel running a single full-budget direct attempt.
const DIRECT_Z3_RLIMIT: u64 = 4_000_000;
// SPEC §7.2 (#53): budget for the LEMMA-FREE first attempt, which runs BEFORE any
// other strategy with the declarations and defining-equation axioms but NO lemma
// library. Rationale: a budget-limited solver is NON-MONOTONE in its axiom set —
// admitting legitimately-relevant lemmas can divert the search into budget
// exhaustion on a goal that discharges instantly from strictly fewer premises.
// The corpus witness is `q-peek.peek-is-head`: it discharges at 2,294 rlimit with
// no lemmas, and does NOT terminate within the full 400,000,000 once its twelve
// relevant lemmas are admitted. Only `unsat` is accepted from this attempt (a
// proof from strictly fewer premises is sound); every other result is DISCARDED
// and the goal proceeds through the unchanged strategies, so the recorded outcome
// is the UNION of lemma-free and the existing search — no regression is possible.
const LEMMA_FREE_Z3_RLIMIT: u64 = 4_000_000;
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

/// SPEC §7.2 attempt validity, PER PROPERTY (#72). The result of attempting one
/// property. `Aborted` is the third verdict the rule demands: not a claim that
/// the property is unproven, but the claim that NO VALID VERDICT EXISTS for it
/// this run because one of its strategy attempts was an environmental abort
/// (wall cap, missing telemetry, memout, canceled-below-budget). It is reported
/// distinctly, records nothing of its own, and leaves whatever the store already
/// recorded standing unchanged — in particular a prior PROVEN is carried forward
/// and MUST NOT be demoted, since demoting it would turn an environmental abort
/// into a verdict, exactly what the rule forbids.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PropVerdict {
    Proven,
    Unproven,
    /// No valid verdict; the string names the first invalidating condition seen.
    Aborted(String),
}

/// SPEC §7.2 (#72): the final composition rule of `prove_prop`, factored out so
/// the soundness-critical direction is testable without a solver. A property
/// PROVEN by any valid attempt is proven regardless of taint (`unsat` is
/// positive evidence no environment can fake). A property NOT proven is
/// `Unproven` only when every attempt was valid; if any attempt was invalid the
/// negative has no valid verdict and the property is `Aborted` — never
/// `Unproven`, because those are different claims.
fn compose_verdict(proved: bool, taint: Option<String>) -> PropVerdict {
    match (proved, taint) {
        (true, _) => PropVerdict::Proven,
        (false, None) => PropVerdict::Unproven,
        (false, Some(reason)) => PropVerdict::Aborted(reason),
    }
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
fn classify_nonverdict(out: &str, budget: u64) -> Result<Outcome, String> {
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
        // The budget-exhaust test uses the ACTUAL rlimit this attempt ran at
        // (the direct attempt on an inductive-eligible goal runs at the reduced
        // DIRECT_Z3_RLIMIT), NOT the full default — a reduced attempt that
        // exhausts at ~4M is a genuine deterministic non-proof, not an external
        // cancel below the (full) budget.
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

/// SPEC §7.2/§6.1.1: is this callee total specifically via an integer ranking
/// function (`measure`)? Such a callee's defining axiom is asserted pattern-free.
fn is_measure(store: &Store, hash: &str) -> bool {
    let mut v = HashSet::new();
    termination(store, hash, &mut v) == Term5::Measure
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

fn fmt_int(n: &num_bigint::BigInt) -> String {
    // SMT-LIB has no negative literals: render a negative as `(- <magnitude>)`.
    // The decimal magnitude is unchanged whatever the precision.
    if n.sign() == num_bigint::Sign::Minus {
        format!("(- {})", n.magnitude())
    } else {
        n.to_string()
    }
}

/// Render a canonicalized binary64 bit pattern as the SMT-LIB FP literal
/// `(fp (_ bvSIGN 1) (_ bvEXP 11) (_ bvMANT 52))` (SPEC §7.1): the 1-bit sign,
/// 11-bit exponent field, and 52-bit mantissa field as decimal `bv` constants.
/// This is exact (no rounding) and gives one literal per value.
fn fmt_float(bits: u64) -> String {
    let sign = (bits >> 63) & 0x1;
    let exp = (bits >> 52) & 0x7FF;
    let mant = bits & 0x000F_FFFF_FFFF_FFFF;
    format!("(fp (_ bv{} 1) (_ bv{} 11) (_ bv{} 52))", sign, exp, mant)
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
        // `Rat` translates to the SMT sort `Real` (SPEC §7.1) — Z3's linear real
        // arithmetic is a complete decidable theory.
        Ty::Rat => "Real".to_string(),
        // `Float` translates to the SMT sort `Float64` = `(_ FloatingPoint 11 53)`
        // (SPEC §7.1). Z3's built-in `Float64` abbreviation is used.
        Ty::Float => "Float64".to_string(),
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
    // SPEC §7.1 (#71): set once the truncating-division bridge (`oath_tquo` /
    // `oath_trem`) has been appended to the declaration stream. The pair is
    // emitted TOGETHER on first use of EITHER `Int` `/` or `%`, exactly once,
    // at the point of first use — not hoisted — so it sits after whatever
    // declarations had already accumulated and before those that follow.
    tdiv_defined: bool,
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
            tdiv_defined: false,
        }
    }

    /// SPEC §7.1 (#71): the kernel's `/` truncates toward zero and its `%` takes
    /// the DIVIDEND's sign, while SMT-LIB's `div`/`mod` are Euclidean. Emitting
    /// `div`/`mod` directly would prove a different theorem, so both operators
    /// are bridged through these two definitions, emitted VERBATIM and exactly
    /// once each. Division by zero is deliberately left unconstrained — SMT-LIB
    /// leaves `div`/`mod` by zero unspecified and these inherit that, which is
    /// the sound direction (the kernel's own `/` and `%` by zero are errors).
    fn ensure_tdiv(&mut self) {
        if self.tdiv_defined {
            return;
        }
        self.tdiv_defined = true;
        self.sc.decls.push(
            "(define-fun oath_tquo ((a Int) (b Int)) Int\n  \
             (ite (>= a 0) (div a b)\n    \
             (ite (= (mod a b) 0) (div a b)\n      \
             (+ (div a b) (ite (> b 0) 1 (- 1))))))\n"
                .to_string(),
        );
        self.sc.decls.push(
            "(define-fun oath_trem ((a Int) (b Int)) Int\n  \
             (ite (>= a 0) (mod a b)\n    \
             (ite (= (mod a b) 0) 0\n      \
             (- (mod a b) (ite (> b 0) b (- b))))))\n"
                .to_string(),
        );
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
                    // SPEC §7.2 (#56): a callee whose termination verdict is
                    // `measure` (integer-counter recursion, §6.1.1) is asserted
                    // with NO pattern — an integer-recursive pattern E-matches
                    // without bound (f(n) → f(n-1) → f(n-2) …), diverging any goal
                    // that mentions it. Pattern-free, z3 falls back to model-based
                    // instantiation, which terminates and discharges the function's
                    // direct laws and the integer-induction obligations. All other
                    // total callees keep the full-application pattern.
                    if is_measure(self.store, hash) {
                        format!("(assert (forall ({}) (= {} {})))", decls.trim_end(), call, rhs)
                    } else {
                        format!(
                            "(assert (forall ({}) (! (= {} {}) :pattern ({}))))",
                            decls.trim_end(),
                            call,
                            rhs,
                            call
                        )
                    }
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
            Term::Int(n) => Ok((fmt_int(n), Ty::Int)),
            // A rat literal `num/den` renders as `(/ NUM DEN)` over `Real`, with
            // a negative numerator rendered `(- N)`; `0/1` is `(/ 0 1)` (SPEC §7.1).
            Term::Rat { num, den } => {
                Ok((format!("(/ {} {})", fmt_int(num), fmt_int(den)), Ty::Rat))
            }
            // A float literal renders as `(fp (_ bvSIGN 1) (_ bvEXP 11) (_ bvMANT
            // 52))` from its exact canonicalized bits — sign, 11-bit exponent,
            // 52-bit mantissa in decimal — no rounding, one literal per value
            // (SPEC §7.1).
            Term::Float(bits) => Ok((fmt_float(*bits), Ty::Float)),
            Term::Bool(b) => Ok(((if *b { "true" } else { "false" }).to_string(), Ty::Bool)),
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
            // A bare reference to a NULLARY function is a complete 0-argument call
            // (e.g. `one-two-three`); anything else is a bare function value,
            // outside the fragment.
            Term::Ref { hash, tyargs } => {
                let n = self.store.func_by_hash.get(hash).map(|fi| fi.param_names.len());
                if n == Some(0) {
                    self.tr_call(hash, tyargs, &[], env, tyenv, self_hash, self_tyargs)
                } else {
                    Err(())
                }
            }
            Term::SelfRef { tyargs } => {
                let n = self.store.func_by_hash.get(self_hash).map(|fi| fi.param_names.len());
                if n == Some(0) {
                    let sh = self_hash.to_string();
                    self.tr_call(&sh, tyargs, &[], env, tyenv, self_hash, self_tyargs)
                } else {
                    Err(())
                }
            }
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
        // THE TRUSTED CRYPTO BOUNDARY (SPEC §1.3, §7, #78). `hmac-sha256` and
        // `bytes-eq-ct` are OUTSIDE THE PROVABLE FRAGMENT — permanently, not
        // pending. Modelling SHA-256 in SMT would establish facts about an
        // invented axiomatization rather than about the algorithm, so a goal
        // reaching either operation gets NO script and its property is recorded
        // `tested`, never `proven` — exactly like partial application.
        //
        // The bail is BEFORE the operands are translated: translating them would
        // append declarations to the script for a translation that cannot
        // succeed, and declaration order is byte-significant (§7.1).
        if op == "hmac-sha256" || op == "bytes-eq-ct" {
            return Err(());
        }
        // Translate operands, keeping each operand's SMT type. The numeric-
        // overloaded prims propagate the operand kind: `Int` stays `Int`, `Rat`
        // (sort `Real`) stays `Rat`, `Float` (sort `Float64`) stays `Float`
        // (SPEC §7.1).
        let mut e = Vec::new();
        let mut opnd_ty = Ty::Int;
        for (i, a) in args.iter().enumerate() {
            let (ae, at) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
            if i == 0 {
                opnd_ty = at;
            }
            e.push(ae);
        }
        let is_rat = matches!(opnd_ty, Ty::Rat);
        let is_float = matches!(opnd_ty, Ty::Float);
        // Numeric conversions (SPEC §7): translated only on their total, exact
        // directions. `to_int` is SMT floor. The partial `Float`-source
        // conversions (`to-rat`/`floor` on a Float) have no rational/integer for
        // NaN/inf and are outside the fragment, like `/` over `Int`.
        match op {
            // to-rat: Int -> Real is `(to_real N)`; Float -> Rat is partial.
            "to-rat" => {
                return match &opnd_ty {
                    Ty::Int => Ok((format!("(to_real {})", e[0]), Ty::Rat)),
                    _ => Err(()),
                };
            }
            // to-float: Int -> `((_ to_fp 11 53) RNE (to_real N))`;
            // Rat -> `((_ to_fp 11 53) RNE R)` — both round nearest-ties-even.
            "to-float" => {
                return match &opnd_ty {
                    Ty::Int => Ok((
                        format!("((_ to_fp 11 53) RNE (to_real {}))", e[0]),
                        Ty::Float,
                    )),
                    Ty::Rat => {
                        Ok((format!("((_ to_fp 11 53) RNE {})", e[0]), Ty::Float))
                    }
                    _ => Err(()),
                };
            }
            // floor: Rat -> `(to_int R)` (SMT to_int is floor); Float is partial.
            "floor" => {
                return match &opnd_ty {
                    Ty::Rat => Ok((format!("(to_int {})", e[0]), Ty::Int)),
                    _ => Err(()),
                };
            }
            _ => {}
        }
        // Over `Float` operands the arithmetic/ordering prims translate to FPA
        // (SPEC §7.1): `+ - * /` at `RNE`, `neg` to `fp.neg`, `< <=` to
        // `fp.lt`/`fp.leq` (IEEE ordered). `/` over `Float` is admitted (total
        // IEEE division). Structural `==` over `Float` is SMT `=` (Leibniz),
        // handled by the shared `==` arm below; `fp-eq` is IEEE `fp.eq`.
        if is_float {
            let (sexpr, ty) = match op {
                "+" => (format!("(fp.add RNE {} {})", e[0], e[1]), Ty::Float),
                "-" => (format!("(fp.sub RNE {} {})", e[0], e[1]), Ty::Float),
                "*" => (format!("(fp.mul RNE {} {})", e[0], e[1]), Ty::Float),
                "/" => (format!("(fp.div RNE {} {})", e[0], e[1]), Ty::Float),
                "neg" => (format!("(fp.neg {})", e[0]), Ty::Float),
                "<" => (format!("(fp.lt {} {})", e[0], e[1]), Ty::Bool),
                "<=" => (format!("(fp.leq {} {})", e[0], e[1]), Ty::Bool),
                "==" => (format!("(= {} {})", e[0], e[1]), Ty::Bool),
                "fp-eq" => (format!("(fp.eq {} {})", e[0], e[1]), Ty::Bool),
                _ => return Err(()),
            };
            return Ok((sexpr, ty));
        }
        // `fp-eq` is Float-only; on a non-float operand it is outside the fragment.
        if op == "fp-eq" {
            return Err(());
        }
        // `/` and `%` over `Int` are TRUNCATING (SPEC §7.1, #71): they bridge to
        // SMT-LIB's Euclidean `div`/`mod` through `oath_tquo`/`oath_trem`, whose
        // definitions are appended to the declaration stream here, at the point of
        // first use. `/` over `Rat` is exact real division and stays `(/ …)`.
        // `%` is Int-only (§2.1), so a `%` reaching here is always the Int case.
        if (op == "/" || op == "%") && !is_rat {
            self.ensure_tdiv();
            let f = if op == "/" { "oath_tquo" } else { "oath_trem" };
            return Ok((format!("({} {} {})", f, e[0], e[1]), Ty::Int));
        }
        let num_ty = if is_rat { Ty::Rat } else { Ty::Int };
        let (sexpr, ty) = match op {
            "+" => (format!("(+ {} {})", e[0], e[1]), num_ty),
            "-" => (format!("(- {} {})", e[0], e[1]), num_ty),
            "*" => (format!("(* {} {})", e[0], e[1]), num_ty),
            "/" => (format!("(/ {} {})", e[0], e[1]), num_ty),
            "neg" => (format!("(- {})", e[0]), num_ty),
            "<" => (format!("(< {} {})", e[0], e[1]), Ty::Bool),
            "<=" => (format!("(<= {} {})", e[0], e[1]), Ty::Bool),
            "==" => (format!("(= {} {})", e[0], e[1]), Ty::Bool),
            "and" => (format!("(and {} {})", e[0], e[1]), Ty::Bool),
            "or" => (format!("(or {} {})", e[0], e[1]), Ty::Bool),
            "not" => (format!("(not {})", e[0]), Ty::Bool),
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
///
/// `budget` is the deterministic z3 rlimit for THIS attempt (SPEC §7.2): the full
/// `DEFAULT_Z3_RLIMIT` (via `z3_rlimit()`) for induction/lex and the full-budget
/// direct fallback, the reduced `DIRECT_Z3_RLIMIT` for the direct attempt on an
/// inductive-eligible goal. It is a RUNNER option (`(set-option :rlimit …)`)
/// prepended OUTSIDE the byte-oracle-hashed core script — the script bytes are
/// byte-identical at either budget.
fn run_z3(script: &str, budget: u64) -> Result<Outcome, String> {
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
        let _ = sin.write_all(format!("(set-option :rlimit {})\n", budget).as_bytes());
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
    classify_nonverdict(&s, budget)
}

struct Prover<'a> {
    store: &'a Store,
}

/// How recursion induction (§7.2, #56/#57) maps a property's binders to the
/// measure-total function's inputs.
enum RecCase {
    /// The leading `dparams` binders ARE the parameters.
    Positional { dparams: usize },
    /// A single parameter of a single-constructor datatype `csmt` with `nfields`
    /// fields; the property binders map onto the fields.
    Constructor { csmt: String, nfields: usize },
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
        self.direct_script_opts(def_hash, prop, candidates, true)
    }

    /// As `direct_script`, but with the lemma ASSERT block optionally omitted
    /// (SPEC §7.2 #53, the lemma-free first attempt). Everything else is
    /// byte-identical: `build_lemmas` is still run for its context side effects,
    /// so the declaration/axiom stream, the `quantified` flag (hence the
    /// `(get-model)` line), the binder declarations and the negated goal are the
    /// same bytes as the canonical with-lemmas script — only the `(assert …)`
    /// lines the lemma library would contribute are dropped. `include_lemmas =
    /// true` reproduces the canonical script pinned by `prove/scripts.txt`
    /// exactly; the lemma-free variant is deliberately NOT that script and is
    /// never hashed by the byte oracle.
    fn direct_script_opts(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
        include_lemmas: bool,
    ) -> Option<(String, bool)> {
        let mut cx = Cx::new(self.store);
        // Canonical build order: lemmas first, then the goal (binders, then body).
        let lem = self.build_lemmas(&mut cx, candidates);
        let lem = if include_lemmas { lem } else { String::new() };
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

    /// Direct proof attempt at the given rlimit `budget`. Returns (attempt,
    /// quantified?), where the attempt is `Ok(outcome)` for a valid result or
    /// `Err(reason)` for an invalid attempt (SPEC §7.2). An outside-fragment goal
    /// (no script) is a valid, deterministic non-proof: `Ok(Unknown)`. The script
    /// bytes are identical at either budget (the rlimit is a runner option); only
    /// the deterministic resource ceiling differs.
    fn try_direct(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
        budget: u64,
    ) -> (Result<Outcome, String>, bool) {
        match self.direct_script(def_hash, prop, candidates) {
            Some((script, quantified)) => (run_z3(&script, budget), quantified),
            None => (Ok(Outcome::Unknown), true),
        }
    }

    /// SPEC §7.2 (#53) LEMMA-FREE FIRST ATTEMPT. Runs the direct script with the
    /// lemma block omitted at `LEMMA_FREE_Z3_RLIMIT`. Returns `true` ONLY on
    /// `unsat` — a proof from strictly fewer premises, which is sound and records
    /// as a direct proof. Every other outcome (`sat`, unknown, an outside-fragment
    /// goal, or an INVALID/aborted attempt) is discarded and returns `false`: this
    /// is an optional extra attempt whose failure costs nothing, so it must never
    /// refute the property and must never taint the run.
    fn try_direct_lemma_free(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
    ) -> bool {
        let budget = LEMMA_FREE_Z3_RLIMIT.min(z3_rlimit());
        match self.direct_script_opts(def_hash, prop, candidates, false) {
            Some((script, _)) => matches!(run_z3(&script, budget), Ok(Outcome::Unsat)),
            None => false,
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
            // Structural induction runs at the FULL budget (SPEC §7.2 #50).
            match run_z3(&Prover::assemble(&cx, &tail), z3_rlimit()) {
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
        // Lexicographic induction runs at the FULL budget (SPEC §7.2 #50).
        match run_z3(&Prover::assemble(&cx, &tail), z3_rlimit()) {
            Ok(Outcome::Unsat) => Ok(true),
            Ok(_) => Ok(false),
            Err(reason) => Err(reason),
        }
    }

    /// SPEC §7.2 (#56, #57): RECURSION INDUCTION. Applies only when the definition
    /// under proof is `measure`-total (§6.1.1): induct along that function's OWN
    /// recursion. Map the property's binders to the function's inputs, first
    /// applicable of:
    ///   CONSTRUCTOR (#57) — one parameter of a single-constructor datatype whose
    ///     field sorts equal the property's binder sorts; bind it to
    ///     `(ctor b0 b1 …)`, and IH binder `j` := `(selector_j A_s)` of the single
    ///     recursive argument `A_s`.
    ///   POSITIONAL — otherwise, if the property has at least `dparams` binders, the
    ///     leading binders ARE the parameters; IH binder `j` (`j < dparams`) :=
    ///     `A_s[j]`, binders at index `>= dparams` left generalized.
    /// Walk the body exactly as §6.1.1 collects self-call sites (over the mapped
    /// binder constants) to recover each site's guard `G_s` and recursive
    /// argument(s). Then discharge, all `unsat` at the reduced budget:
    ///   BASE — the goal under `(assert (not G_s))` for EVERY site;
    ///   STEP — per site, the goal under `(assert G_s)` and `(assert IH_s)`.
    /// Sound by well-founded induction on the measure the function is total by.
    fn try_recursion_induction(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
    ) -> Result<bool, String> {
        if !is_measure(self.store, def_hash) {
            return Ok(false);
        }
        let n = self.store.func_by_hash.get(def_hash).map(|fi| fi.param_names.len()).unwrap_or(0);
        let fty = match self.store.def_by_hash.get(def_hash) {
            Some(Def::Func { ty, .. }) => ty.clone(),
            _ => return Ok(false),
        };
        let ptys = param_types(&fty, n).0;
        let budget = DIRECT_Z3_RLIMIT.min(z3_rlimit());
        let sort_str = |ty: &Ty| -> String {
            let mut sc = Sorts::default();
            sort_of(self.store, ty, &mut sc)
        };

        // Map the property's binders to the function's inputs (constructor case
        // first, positional as fallback).
        let rec_case: RecCase = {
            let mut chosen: Option<RecCase> = None;
            // CONSTRUCTOR case (#57).
            if n == 1 {
                if let Ty::Data { hash: dh, args } = &ptys[0] {
                    if let Some(di) = self.store.data_by_hash.get(dh) {
                        if di.ctors.len() == 1 {
                            let ftys: Vec<Ty> =
                                di.ctors[0].1.iter().map(|f| inst_field(f, args, dh)).collect();
                            if ftys.len() == prop.binders.len()
                                && ftys
                                    .iter()
                                    .zip(prop.binders.iter())
                                    .all(|(ft, bt)| sort_str(ft) == sort_str(bt))
                            {
                                let csmt = ctor_smt(&di.ctors[0].0, &sort_str(&ptys[0]));
                                chosen = Some(RecCase::Constructor { csmt, nfields: ftys.len() });
                            }
                        }
                    }
                }
            }
            // POSITIONAL case.
            if chosen.is_none() && n > 0 && prop.binders.len() >= n {
                chosen = Some(RecCase::Positional { dparams: n });
            }
            match chosen {
                Some(c) => c,
                None => return Ok(false),
            }
        };

        // Parameter environment for the site walk, per the chosen mapping.
        let param_env: Vec<(String, Ty)> = match &rec_case {
            RecCase::Positional { dparams } => (0..*dparams)
                .map(|j| (format!("b{}", j), prop.binders[j].clone()))
                .collect(),
            RecCase::Constructor { csmt, nfields } => {
                let expr = if *nfields == 0 {
                    csmt.clone()
                } else {
                    let mut e = format!("({}", csmt);
                    for j in 0..*nfields {
                        e.push_str(&format!(" b{}", j));
                    }
                    e.push(')');
                    e
                };
                vec![(expr, ptys[0].clone())]
            }
        };

        // Per-site induction-hypothesis substitution, per the chosen mapping.
        let ih_fixed = |args: &[String]| -> BTreeMap<usize, (String, Ty)> {
            let mut fixed = BTreeMap::new();
            match &rec_case {
                RecCase::Positional { dparams } => {
                    for j in 0..*dparams {
                        fixed.insert(j, (args[j].clone(), prop.binders[j].clone()));
                    }
                }
                RecCase::Constructor { csmt, nfields } => {
                    // The single recursive argument A_s is deconstructed by selectors.
                    for j in 0..*nfields {
                        fixed.insert(
                            j,
                            (format!("({}_{} {})", csmt, j, args[0]), prop.binders[j].clone()),
                        );
                    }
                }
            }
            fixed
        };

        // Declare every property binder as a `b{m}` constant and translate the
        // goal in that environment. Shared by every obligation.
        let build_goal = |cx: &mut Cx| -> Option<(String, String)> {
            let mut decls = String::new();
            let mut env: Vec<(String, Ty)> = Vec::with_capacity(prop.binders.len());
            for (m, bt) in prop.binders.iter().enumerate() {
                let vname = format!("b{}", m);
                let s = sort_of(self.store, bt, &mut cx.sc);
                decls.push_str(&format!("(declare-const {} {})\n", vname, s));
                env.push((vname, bt.clone()));
            }
            let goal = cx.tr(&prop.body, &env, &[], def_hash, &[]).ok()?.0;
            Some((decls, goal))
        };

        // ---- BASE: (not G_s) for every site, then (not goal). Also derive the
        // guard GROUPS (site indices sharing a guard, in first-seen order) that
        // STEP discharges together. ----
        let groups: Vec<Vec<usize>>;
        {
            let mut cx = Cx::new(self.store);
            let lem = self.build_lemmas(&mut cx, candidates);
            let (sites, fresh_decls) = match collect_self_sites(&mut cx, def_hash, &param_env) {
                Some(x) => x,
                None => return Ok(false),
            };
            // Group site indices by guard string, preserving first-seen order.
            let mut gs: Vec<(String, Vec<usize>)> = Vec::new();
            for (idx, s) in sites.iter().enumerate() {
                let g = guard_conj(&s.guards);
                if let Some(e) = gs.iter_mut().find(|(gg, _)| *gg == g) {
                    e.1.push(idx);
                } else {
                    gs.push((g, vec![idx]));
                }
            }
            groups = gs.into_iter().map(|(_, v)| v).collect();
            let (decls, goal) = match build_goal(&mut cx) {
                Some(x) => x,
                None => return Ok(false),
            };
            let mut tail = String::new();
            tail.push_str(&lem);
            tail.push_str(&fresh_decls);
            tail.push_str(&decls);
            for s in &sites {
                tail.push_str(&format!("(assert (not {}))\n", guard_conj(&s.guards)));
            }
            tail.push_str(&format!("(assert (not {}))\n(check-sat)\n", goal));
            match run_z3(&Prover::assemble(&cx, &tail), budget) {
                Ok(Outcome::Unsat) => {}
                Ok(_) => return Ok(false),
                Err(reason) => return Err(reason),
            }
        }

        // ---- STEP (SPEC §7.2, grouped): per guard-group, assert `G_s` ONCE plus
        // the induction hypothesis of EVERY site in the group, then `(not goal)`.
        // A function with several recursive calls on one path (fib) thereby gets
        // all their hypotheses at once; single-call functions have one site per
        // guard, so their obligation is unchanged. ----
        for group in &groups {
            let mut cx = Cx::new(self.store);
            let lem = self.build_lemmas(&mut cx, candidates);
            let (sites, fresh_decls) = match collect_self_sites(&mut cx, def_hash, &param_env) {
                Some(x) => x,
                None => return Ok(false),
            };
            // All sites in a group share the guard; assert it once.
            let guard = guard_conj(&sites[group[0]].guards);
            let mut ihs = String::new();
            for &si in group {
                let fixed = ih_fixed(&sites[si].args);
                let ih = match self.forall_prop(&mut cx, prop, def_hash, &fixed) {
                    Some(h) => h,
                    None => return Ok(false),
                };
                ihs.push_str(&format!("(assert {})\n", ih));
            }
            let (decls, goal) = match build_goal(&mut cx) {
                Some(x) => x,
                None => return Ok(false),
            };
            let mut tail = String::new();
            tail.push_str(&lem);
            tail.push_str(&fresh_decls);
            tail.push_str(&decls);
            tail.push_str(&format!("(assert {})\n", guard));
            tail.push_str(&ihs);
            tail.push_str(&format!("(assert (not {}))\n(check-sat)\n", goal));
            match run_z3(&Prover::assemble(&cx, &tail), budget) {
                Ok(Outcome::Unsat) => {}
                Ok(_) => return Ok(false),
                Err(reason) => return Err(reason),
            }
        }

        Ok(true)
    }

    /// Prove one property. SPEC §7.2 GRANULARITY: attempt validity is per-attempt.
    /// A valid `unsat` (or quantifier-free `sat` refutation) from ANY strategy is
    /// positive evidence no environment can fake, so it decides the property
    /// regardless of other attempts' invalidity. An invalid attempt yields NO
    /// evidence — it does not end the run; it only TAINTS the negative case. If no
    /// strategy proves the property and any attempt along the way was invalid, the
    /// property has no valid negative verdict: it returns `Aborted` (SPEC §7.2
    /// #72, PER PROPERTY — the run continues and its siblings record normally),
    /// never `Unproven`. If no attempt was invalid, it records `Unproven`.
    fn prove_prop(
        &self,
        def_hash: &str,
        prop: &Prop,
        candidates: &[(String, usize, bool)],
    ) -> PropVerdict {
        // First invalid-attempt reason seen while trying to prove this property.
        let mut taint: Option<String> = None;

        // SPEC §7.2 (#53): LEMMA-FREE FIRST ATTEMPT, before any other strategy.
        // Only `unsat` is accepted (sound: strictly fewer premises); it records as
        // a direct proof — oathrs records no method string, a proven property is
        // just `true`. Anything else is discarded WITHOUT taint and the goal
        // proceeds through the unchanged strategies below, so the outcome is the
        // union of this attempt and the existing search.
        if self.try_direct_lemma_free(def_hash, prop, candidates) {
            return PropVerdict::Proven;
        }

        // SPEC §7.2 (#50, #56): a goal is INDUCTIVE-ELIGIBLE if it has at least
        // one datatype-typed binder (structural/lexicographic induction) OR at
        // least one Int-typed binder (Peano integer induction, #56). Its DIRECT
        // attempt runs at the reduced budget (the futile-but-slow full-budget
        // direct is skipped in favour of the full-budget fallback below). A goal
        // with neither runs its single direct attempt at the full budget.
        let inductive_eligible =
            prop.binders.iter().any(|b| matches!(b, Ty::Data { .. } | Ty::Int));
        // Never exceed the full budget (defensive against an OATHRS_Z3_RLIMIT
        // override that lowers it below the reduced constant): the reduced
        // attempt must be no stronger than the full-budget fallback.
        let direct_budget = if inductive_eligible {
            DIRECT_Z3_RLIMIT.min(z3_rlimit())
        } else {
            z3_rlimit()
        };

        let (direct, quantified) = self.try_direct(def_hash, prop, candidates, direct_budget);
        match direct {
            Ok(Outcome::Unsat) => return PropVerdict::Proven, // proven; a valid attempt wins
            Err(reason) => {
                // Invalid direct attempt: no evidence. Fall through to induction —
                // a valid strategy may still prove it (the t-insert case).
                taint.get_or_insert(reason);
            }
            // A valid non-unsat (Unknown, or a Sat). For a full-budget direct
            // attempt on a NON-inductive goal this is final: a quantifier-free
            // `sat` is a valid refutation and induction cannot add power. For an
            // inductive-eligible goal the reduced-budget result is NOT
            // authoritative — the full-budget fallback below re-decides it.
            Ok(_) => {}
        }

        if quantified {
            for k in 0..prop.binders.len() {
                if !matches!(prop.binders[k], Ty::Data { .. }) {
                    continue;
                }
                match self.try_induction_binder(def_hash, prop, k, candidates) {
                    Ok(true) => return PropVerdict::Proven,
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
                        Ok(true) => return PropVerdict::Proven,
                        Ok(false) => {}
                        Err(reason) => {
                            taint.get_or_insert(reason);
                        }
                    }
                }
            }
            // SPEC §7.2 (#56): recursion induction, after structural and
            // lexicographic induction have failed. Fires only when the definition
            // under proof is `measure`-total, inducting along its OWN recursion.
            match self.try_recursion_induction(def_hash, prop, candidates) {
                Ok(true) => return PropVerdict::Proven,
                Ok(false) => {}
                Err(reason) => {
                    taint.get_or_insert(reason);
                }
            }
        }

        // FALLBACK (SPEC §7.2 #50). An inductive-eligible goal ran its DIRECT
        // attempt at the REDUCED budget above; if structural and lexicographic
        // induction have now both failed to discharge it, retry the SAME direct
        // script at the FULL budget. This preserves the budget-part-of-identity
        // invariant: a goal provable only by heavy direct search keeps its
        // verdict, and the recorded outcome is identical to a kernel running a
        // single full-budget direct attempt. `unsat` proves it; a quantifier-free
        // `sat` refutes it (not proven); an invalid attempt taints the negative.
        // For a non-inductive goal the direct attempt already ran at the full
        // budget, so no fallback is needed.
        if inductive_eligible {
            let (fb, _) = self.try_direct(def_hash, prop, candidates, z3_rlimit());
            match fb {
                Ok(Outcome::Unsat) => return PropVerdict::Proven,
                Err(reason) => {
                    taint.get_or_insert(reason);
                }
                Ok(_) => {}
            }
        }

        // Not proven by any strategy. A tainted negative has no valid verdict, so
        // THIS PROPERTY is aborted (SPEC §7.2 GRANULARITY, PER PROPERTY #72): it
        // records nothing, its previously recorded state stands, and its siblings
        // and the run are unaffected. Untainted, it is an honest, reproducible
        // unproven.
        compose_verdict(
            false,
            taint.map(|reason| {
                format!(
                    "no strategy proved it (def {}) and an attempt was invalid, so it has no \
                     valid negative verdict — {}",
                    &def_hash[..def_hash.len().min(12)],
                    reason
                )
            }),
        )
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

/// Author-supplied proof hints (SPEC §7.2 "Author-supplied hints", §10): per
/// goal `(definition hash, property index)`, a list of `(definition hash,
/// property index)` references to proven properties of OTHER definitions.
/// Hints are METADATA — they never enter the canonical encoding and never
/// change a hash — so an independent kernel receives them through the fixture
/// channel (`prove/outcomes.json`), not through `.oath` source.
pub type Hints = BTreeMap<(String, usize), Vec<(String, usize)>>;

/// The candidate lemma set for property `pi` of `def_hash` given a proven set
/// (SPEC §7.2 construction): every proven property of the transitive dependency
/// closure, plus every recorded-proven property of the definition itself
/// (including `pi` if it is recorded proven). Each is tagged with whether it is
/// ADMISSIBLE for emission — a dependency by the footprint test, an own property
/// by `index != pi` (siblings admissible, the own lemma excluded). All are
/// translated (touching declarations/axioms); only admissible ones are asserted.
/// Sorted by (definition-hash, property-index).
///
/// Author hints for THIS goal (SPEC §7.2, #67) are applied last and are purely
/// ADDITIVE: a hinted `(defHash, propIdx)` that is currently proven becomes
/// admissible even when the footprint filter excluded it, and — if it lies
/// outside the dependency closure entirely — joins the candidate list as a new
/// member. A hint whose target is unproven/missing is INERT. The canonical
/// (defHash, propIdx) emission order is unchanged, and nothing is ever removed.
/// The two MUSTs §7.2 spells out under "consequences of additive":
///   * SET UNION, NOT CONCATENATION — a hint naming a lemma that is already an
///     admissible candidate is a NO-OP: the lemma is asserted exactly once and
///     the script bytes equal the unhinted script's. Enforced by looking the
///     candidate up and (at most) raising its admissibility, never pushing a
///     second entry for the same key.
///   * A PROPERTY IS NEVER ITS OWN LEMMA — the own-property exclusion is
///     absolute and applies BEFORE hints, so a hint whose target IS the goal
///     `(def_hash, pi)` is DISCARDED whatever the metadata says. This is a
///     soundness requirement: admitting it would assert the goal as its own
///     axiom and make any property "provable".
fn candidate_lemmas(
    store: &Store,
    def_hash: &str,
    pi: usize,
    prop: &Prop,
    proven: &BTreeSet<(String, usize)>,
    hints: &Hints,
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
    if let Some(hs) = hints.get(&(def_hash.to_string(), pi)) {
        for (h, j) in hs {
            // SOUNDNESS (§7.2): the own-property exclusion is applied BEFORE
            // hints and is absolute — a hint at the goal itself is discarded, so
            // no hint can ever assert the goal as its own axiom. Checked first,
            // ahead of every other test, because no later condition may rescue it.
            if h == def_hash && *j == pi {
                continue;
            }
            // INERT unless the target property is currently proven, and unless
            // it actually exists as a property of a function definition.
            if !proven.contains(&(h.clone(), *j)) {
                continue;
            }
            match store.def_by_hash.get(h) {
                Some(Def::Func { props, .. }) if *j < props.len() => {}
                _ => continue,
            }
            // SET UNION (§7.2): at most one entry per (defHash, propIdx) — an
            // already-present candidate is raised to admissible (a no-op when it
            // already was), never duplicated, so a redundant hint cannot change
            // the emitted bytes.
            match cands.iter_mut().find(|c| c.0 == *h && c.1 == *j) {
                Some(c) => c.2 = true,
                // Outside the closure: an extra candidate, admitted.
                None => cands.push((h.clone(), *j, true)),
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
    hints: &Hints,
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
            let cands = candidate_lemmas(store, &hash, pi, prop, proven, hints);
            if let Some((script, _)) = prover.direct_script(&hash, prop, &cands) {
                out.push((name.clone(), pi, sha256_hex(script.as_bytes())));
            }
        }
    }
    out
}

// ===========================================================================
// SPEC §7.5 — Sharded verification (OPTIONAL capability, #98)
//
// A THROUGHPUT feature that changes NO verdict. It verifies ONLY the
// run-stability property F(S) = S of a GIVEN seed set S (the recorded proven
// set carried by prove/outcomes.json), with the single application of F
// partitioned across shards. It does NOT re-establish §7.2's from-empty limit.
//
// Why a single pass with no rounds and no within-shard ordering suffices: with
// the proven state held FIXED at S, `candidate_lemmas(goal)` is a pure function
// of (S, corpus, hints) — it never reads this run's own progress — so a goal's
// candidate set (hence its script bytes, hence its verdict) is identical in any
// order, in any shard. F(S) is therefore one independent attempt per property.
//
// The self-check IS the mode: the union of the shards' verdicts is compared to
// S property-by-property and the run FAILS LOUDLY on any mismatch (a valid
// verdict differing from S, a definition proved by no shard, or one attempted by
// more than one). A sharded run that merely completes is not a pass.
// ===========================================================================

/// SPEC §7.5 shard-assignment key: `first_64_bits(definition_hash) mod n`, where
/// `first_64_bits` is the leading 8 bytes of the O1 identity hash read
/// big-endian. The hash is 64 lowercase hex chars (§1), so the leading 8 bytes
/// are its first 16 hex characters. Assignment depends ONLY on identity — never
/// on input-file position or elaboration order, which are unstable across file
/// moves and unreproducible by an independent runner. `n = 1` sends every
/// definition to shard 0 (the unsharded seeded verifier).
pub fn shard_of(def_hash: &str, n: u64) -> u64 {
    assert!(n >= 1, "shard count n must be >= 1");
    // A well-formed identity hash is 64 hex chars; anything shorter is not a
    // real O1 hash. Fall back to 0 rather than panicking on malformed input —
    // the partition self-check would surface any resulting anomaly loudly.
    // `get(..16)` is None if the string is shorter than 16 bytes OR byte 16 is
    // not a char boundary (a malformed non-ASCII hash), so this never slices
    // mid-character — it falls back to 0 as the comment promises.
    let lead = def_hash
        .get(..16)
        .and_then(|h| u64::from_str_radix(h, 16).ok())
        .unwrap_or(0);
    lead % n
}

/// The raw outcome of ONE shard of the seeded single-pass verifier (SPEC §7.5).
/// `verdicts` are the untouched per-property attempt results — carry-forward is
/// NOT applied here so the record stays a faithful account of what the attempt
/// returned; the merge applies it against S. `defs` is the set of
/// property-bearing, non-falsified function definitions this shard attempted,
/// carried so the merge can verify the partition (exactly one shard per def).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ShardOutcome {
    pub shard: u64,
    pub defs: BTreeSet<String>,
    pub verdicts: BTreeMap<(String, usize), PropVerdict>,
}

/// The result of merging every shard's outcome and self-checking it against the
/// seed `S` (SPEC §7.5). `ok()` is the pass; `mismatches` is empty exactly when
/// the run passes. `seed_id` makes the seed's identity visible so two runs
/// cannot claim the same verification from different seeds.
#[derive(Debug, Clone)]
pub struct MergeReport {
    /// The effective proven set after carry-forward — what the union established.
    pub proven: BTreeSet<(String, usize)>,
    /// Properties whose PRIOR proof in S was carried forward through an
    /// environmental abort this run (not a mismatch), with the reason.
    pub carried: BTreeMap<(String, usize), String>,
    /// Loud, human-readable descriptions of every mismatch. Empty iff the run
    /// passes.
    pub mismatches: Vec<String>,
    /// Campaign identity of the seed S (a hash of the proven set).
    pub seed_id: String,
}

impl MergeReport {
    pub fn ok(&self) -> bool {
        self.mismatches.is_empty()
    }
}

/// Identity of a seed set S alone: a hash of the proven set, reported for humans
/// so a `MergeReport` names which S it checked. The set is emitted in its
/// canonical sorted order (`BTreeSet` iteration) as `hash\tpi\n` lines. NOTE: this
/// binds ONLY S — it is NOT sufficient to gate a distributed merge, because a
/// proof outcome (hence F(S)) is a function of (S, hints, solver, rlimit) per
/// §7.2/§10.5. Use `campaign_identity` for the merge admission check.
pub fn seed_identity(seed: &BTreeSet<(String, usize)>) -> String {
    let mut buf = String::new();
    for (h, pi) in seed {
        buf.push_str(h);
        buf.push('\t');
        buf.push_str(&pi.to_string());
        buf.push('\n');
    }
    sha256_hex(buf.as_bytes())
}

/// The FULL determinism context of a sharded campaign (SPEC §7.2/§10.5): a hash
/// over (proven set S, the author hints, the solver version string, the effective
/// rlimit). §7.2 pins a proof outcome as `f(script bytes, solver version, rlimit)`
/// and §7.2's candidate construction folds the hints into the script bytes, so
/// `F(S)` — the whole thing a sharded merge reconstructs — is a function of these
/// four. Two shards that agree on S but ran under different hints, a different z3,
/// or a different rlimit did NOT compute the same `F`, and their union is an
/// unsound hybrid no single application of `F` ever produced. `--merge-shards`
/// therefore admits an emission only when its campaign identity equals the merge's
/// own recomputed one; the seed hash alone cannot see the other three inputs.
///
/// Canonical serialization: a versioned header, then `solver` and `rlimit` lines,
/// then S (sorted), then the hints (sorted, each target list sorted) — so
/// logically equal contexts hash equal and any difference in any input changes the
/// id.
pub fn campaign_identity(
    seed: &BTreeSet<(String, usize)>,
    hints: &Hints,
    solver_version: &str,
    rlimit: u64,
) -> String {
    let mut buf = String::new();
    buf.push_str("oath-campaign/v1\n");
    buf.push_str("solver\t");
    buf.push_str(solver_version);
    buf.push('\n');
    buf.push_str(&format!("rlimit\t{}\n", rlimit));
    buf.push_str("seed\n");
    for (h, pi) in seed {
        buf.push_str(&format!("{}\t{}\n", h, pi));
    }
    buf.push_str("hints\n");
    // `hints` is a BTreeMap, so keys iterate sorted; sort each target list too so
    // the identity does not depend on the order the fixture happened to list them.
    for ((h, pi), targets) in hints {
        let mut ts = targets.clone();
        ts.sort();
        buf.push_str(&format!("{}\t{}", h, pi));
        for (th, tpi) in &ts {
            buf.push_str(&format!("\t{}:{}", th, tpi));
        }
        buf.push('\n');
    }
    sha256_hex(buf.as_bytes())
}

/// The effective z3 rlimit (SPEC §7.2 budget), reading `OATHRS_Z3_RLIMIT`. Public
/// so the CLI can fold it into the campaign identity.
pub fn effective_z3_rlimit() -> u64 {
    z3_rlimit()
}

/// A short, UTF-8-SAFE prefix of a hash for diagnostics. Never byte-slice a hash:
/// an untrusted emission may carry a multibyte string, and `&h[..8]` panics at a
/// non-char boundary. `chars().take(8)` fails cleanly on anything.
fn short_hash(h: &str) -> String {
    h.chars().take(8).collect()
}

/// The property-bearing, non-falsified function definitions of a store — the
/// universe the §7.5 partition quantifies over. Derived from the store (the
/// claim's owner), NOT from any shard's membership.
fn shardable_defs(store: &Store, falsified: &BTreeSet<String>) -> BTreeSet<String> {
    store
        .def_by_hash
        .iter()
        .filter(|(h, d)| match d {
            Def::Func { props, .. } => !props.is_empty() && !falsified.contains(*h),
            _ => false,
        })
        .map(|(h, _)| h.clone())
        .collect()
}

/// Run ONE shard of the seeded single-pass verifier (SPEC §7.5). Attempts each
/// property of every non-falsified, property-bearing function definition
/// assigned to this shard EXACTLY ONCE, with candidate lemmas drawn from the
/// FIXED seed `S`. No rounds and no within-shard dependency ordering: with the
/// proven state fixed at S each goal is independent.
///
/// `assign(hash)` decides shard membership; passing an arbitrary deterministic
/// `assign` lets a second assignment function be exercised (the normative one is
/// `|h| shard_of(h, n)`). The real driver passes `Prover::prove_prop` as
/// `attempt`; tests pass a deterministic oracle, the only way to exercise the
/// abort/carry-forward semantics without a solver or a clock.
pub fn prove_shard_with<A, F>(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
    seed: &BTreeSet<(String, usize)>,
    assign: A,
    i: u64,
    mut attempt: F,
) -> ShardOutcome
where
    A: Fn(&str) -> u64,
    F: FnMut(&str, usize, &Prop, &[(String, usize, bool)]) -> PropVerdict,
{
    let mut defs = BTreeSet::new();
    let mut verdicts = BTreeMap::new();
    // Iterate in a stable (definition-hash) order; the partition is by `assign`,
    // never by this order, but a deterministic walk keeps output reproducible.
    for (hash, def) in &store.def_by_hash {
        let props = match def {
            Def::Func { props, .. } if !props.is_empty() => props,
            _ => continue,
        };
        // §7.3: a falsified definition is never proved (upgrade requires tested),
        // exactly as the from-empty driver skips it — so the seeded verifier must
        // skip it too, or `n = 1` would diverge from the unsharded seeded run.
        if falsified.contains(hash) {
            continue;
        }
        if assign(hash) != i {
            continue;
        }
        defs.insert(hash.clone());
        for (pi, prop) in props.iter().enumerate() {
            let cands = candidate_lemmas(store, hash, pi, prop, seed, hints);
            let verdict = attempt(hash, pi, prop, &cands);
            verdicts.insert((hash.clone(), pi), verdict);
        }
    }
    ShardOutcome { shard: i, defs, verdicts }
}

/// The unsharded seeded verifier (SPEC §7.5, the `n = 1` reference): one
/// application of §7.2's F with the recorded proven state held FIXED at the seed
/// S — every non-falsified property attempted once, candidates drawn from S.
/// This is NOT `prove_all_with`'s from-empty iteration; it is a single pass, and
/// it is written as an INDEPENDENT loop (not a call to `prove_shard_with`) so
/// that "`n = 1` sharded == the unsharded seeded verifier" is a real cross-check
/// of two code paths rather than a tautology.
pub fn seeded_verify_all<F>(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
    seed: &BTreeSet<(String, usize)>,
    mut attempt: F,
) -> BTreeMap<(String, usize), PropVerdict>
where
    F: FnMut(&str, usize, &Prop, &[(String, usize, bool)]) -> PropVerdict,
{
    let mut verdicts = BTreeMap::new();
    for (hash, def) in &store.def_by_hash {
        let props = match def {
            Def::Func { props, .. } if !props.is_empty() => props,
            _ => continue,
        };
        if falsified.contains(hash) {
            continue;
        }
        for (pi, prop) in props.iter().enumerate() {
            let cands = candidate_lemmas(store, hash, pi, prop, seed, hints);
            verdicts.insert((hash.clone(), pi), attempt(hash, pi, prop, &cands));
        }
    }
    verdicts
}

/// The self-check (SPEC §7.5): given the raw per-property verdicts from a run and
/// which shard attempted each definition, compare the union against the seed `S`
/// property-by-property and report every mismatch loudly. This is the whole
/// verifier — a run that merely completes is not a pass, the equality is.
///
/// `union == S` is checked in BOTH directions, so neither side can hide a member
/// the other lacks: the universe loop covers every property the run should have
/// attempted (catching S ⊋ union and union ⊋ S over the corpus, and any assigned
/// property left unattempted), and the trailing seed loop covers every member of
/// S the universe loop could not reach — a proof S records for a definition this
/// run cannot even pose (absent, falsified, non-shardable, or an out-of-range
/// index). Skipping either direction would verify `union == S ∩ universe`, which
/// is not the property.
///
/// `owners` maps each definition hash to the shard indices that attempted it;
/// exactly one is required. `raw` is the union of the shards' raw verdicts.
fn check_against_seed(
    store: &Store,
    falsified: &BTreeSet<String>,
    seed: &BTreeSet<(String, usize)>,
    raw: &BTreeMap<(String, usize), PropVerdict>,
    owners: &BTreeMap<String, Vec<u64>>,
) -> MergeReport {
    let universe = shardable_defs(store, falsified);
    let mut proven = BTreeSet::new();
    let mut carried = BTreeMap::new();
    let mut mismatches = Vec::new();
    let name_of = |hash: &str| -> String {
        store
            .func_by_hash
            .get(hash)
            .map(|fi| fi.name.clone())
            .unwrap_or_else(|| short_hash(hash))
    };

    // Partition self-check (§7.5): every definition in the universe must have
    // been attempted by EXACTLY ONE shard. Zero is "proved by no shard"; more
    // than one is "attempted by more than one shard".
    for hash in &universe {
        match owners.get(hash).map(|v| v.len()).unwrap_or(0) {
            1 => {}
            0 => mismatches.push(format!(
                "PARTITION: definition {} ({}) was attempted by NO shard",
                name_of(hash),
                short_hash(hash)
            )),
            k => mismatches.push(format!(
                "PARTITION: definition {} ({}) was attempted by {} shards {:?} — must be exactly one",
                name_of(hash),
                short_hash(hash),
                k,
                owners.get(hash).unwrap()
            )),
        }
    }
    // A shard that attempted a definition OUTSIDE the universe (falsified, or not
    // a property-bearing func) is itself an error worth surfacing.
    for hash in owners.keys() {
        if !universe.contains(hash) {
            mismatches.push(format!(
                "PARTITION: a shard attempted {} ({}), which is not a shardable definition",
                name_of(hash),
                short_hash(hash)
            ));
        }
    }

    // Per-property verdict check (§7.5) over the universe's properties.
    for hash in &universe {
        let props = match store.def_by_hash.get(hash) {
            Some(Def::Func { props, .. }) => props.len(),
            _ => 0,
        };
        for pi in 0..props {
            let key = (hash.clone(), pi);
            let in_s = seed.contains(&key);
            match raw.get(&key) {
                Some(PropVerdict::Proven) => {
                    proven.insert(key.clone());
                    if !in_s {
                        // F(S) proved a property S does not record: S is not a
                        // fixpoint (F(S) ⊋ S).
                        mismatches.push(format!(
                            "VERDICT: {} prop {} was PROVEN by the seeded re-derivation but is NOT in the seed S",
                            name_of(hash),
                            pi
                        ));
                    }
                }
                Some(PropVerdict::Unproven) => {
                    if in_s {
                        // A VALID verdict differing from S (§7.5): S is not
                        // run-stable — a member it records proven cannot be
                        // re-derived from S. This is the only mechanism in the
                        // system that catches a non-self-consistent seed.
                        mismatches.push(format!(
                            "VERDICT: {} prop {} is in the seed S but the seeded re-derivation returned UNPROVEN (S is not run-stable)",
                            name_of(hash),
                            pi
                        ));
                    }
                }
                Some(PropVerdict::Aborted(reason)) => {
                    if in_s {
                        // Carry-forward on abort (§7.5): a property that aborts
                        // environmentally AND is in S carries S's verdict — NOT a
                        // mismatch. Mirrors §7.2's run-stability carry-forward.
                        proven.insert(key.clone());
                        carried.insert(key.clone(), reason.clone());
                    }
                    // An abort on a NON-member is not a valid verdict differing
                    // from S (S records it unproven, and an abort is "no verdict"
                    // — consistent with non-membership), so it is not a mismatch.
                }
                None => {
                    // No shard produced a verdict for this property of an OWNED
                    // (universe) definition. §7.5 requires every assigned property
                    // to be attempted EXACTLY ONCE, so a missing verdict is ALWAYS
                    // a mismatch — regardless of seed membership. A def attempted
                    // by exactly one shard always yields a verdict for every one of
                    // its properties (the shard attempts them all), so a `None`
                    // here means the def went unattempted (also flagged by the
                    // partition check) or the shard outcome was truncated/malformed
                    // — a property never attempted, which a PASS must never hide.
                    mismatches.push(format!(
                        "VERDICT: {} prop {} was NOT attempted — no shard produced a verdict for this assigned property{}",
                        name_of(hash),
                        pi,
                        if in_s { " (and it is recorded proven in the seed S)" } else { "" }
                    ));
                }
            }
        }
    }

    // §7.5 "the union … MUST be compared to S property by property": the loop
    // above visits every property of the UNIVERSE, which is `union == S ∩
    // universe`, not `union == S`. Every member of S must be accounted for, so a
    // seed member the run cannot attempt — its definition absent from the corpus,
    // newly falsified, or otherwise non-shardable, or a property index the
    // definition does not have — is a MISMATCH: S records a proof this run cannot
    // even pose, so it cannot have verified `union == S`.
    for (h, pi) in seed {
        let props = match store.def_by_hash.get(h) {
            Some(Def::Func { props, .. }) => props.len(),
            _ => 0,
        };
        if !universe.contains(h) {
            let why = if store.def_by_hash.get(h).is_none() {
                "its definition is not in this run (absent from the corpus)"
            } else if falsified.contains(h) {
                "its definition is FALSIFIED, so it is never proved (§7.3)"
            } else {
                "its definition is not a shardable definition (no properties)"
            };
            mismatches.push(format!(
                "SEED: {} prop {} is recorded proven in the seed S, but {} — the run cannot attempt it",
                name_of(h),
                pi,
                why
            ));
        } else if *pi >= props {
            // In the universe but the index is out of range — a malformed seed
            // claiming a property the definition does not have.
            mismatches.push(format!(
                "SEED: {} prop {} is recorded proven in the seed S, but the definition has only {} propert{} — the run cannot attempt it",
                name_of(h),
                pi,
                props,
                if props == 1 { "y" } else { "ies" }
            ));
        }
    }

    MergeReport { proven, carried, mismatches, seed_id: seed_identity(seed) }
}

/// Merge every shard's outcome and self-check it against the seed `S`
/// (SPEC §7.5). Returns a `MergeReport` whose `ok()` is the pass.
///
/// The outcomes are UNTRUSTED input — this is the external-merge path, fed the
/// `--shard i/n` emissions collected from separate processes — so this trusts
/// NOTHING in an outcome's self-reported metadata. Every verdict `(def_hash, pi)`
/// an outcome contributes is validated against the CANONICAL source (`shard_of`
/// plus the elaborated store), not against the outcome's own `defs` list, and is
/// unioned ONLY when all three hold; any violation is a loud mismatch and the
/// verdict is never unioned:
///   1. `pi < property_count(def_hash)` in the store — an out-of-range index is a
///      mismatch, not a silently-ignored extra key.
///   2. `shard_of(def_hash, n) == out.shard` — the definition is genuinely
///      assigned to the emitting outcome's shard, so a swapped/relabelled shard,
///      an arbitrary reassignment, or a verdict for a def that hashes elsewhere
///      all fail. (Ownership for the partition check is taken from these VALID
///      contributions, keyed by outcome POSITION so two outcomes sharing a shard
///      label are still two owners.)
///   3. `def_hash` is a shardable definition in the store (exists, property-
///      bearing, non-falsified) — a verdict for an absent/falsified/non-shardable
///      def is a mismatch.
/// It also owns SHARD-INDEX COMPLETENESS: the outcomes' declared indices must be
/// EXACTLY `{0,…,n-1}` — no missing, duplicate, or out-of-range shard. This lives
/// HERE, not only in the CLI wrapper, so a direct caller cannot get a vacuous PASS
/// from an empty or incomplete campaign (`outcomes = []`, or a missing shard that
/// owns no property-bearing def, which the per-def partition check alone cannot
/// see).
/// This closes the whole class of malformed-outcome vectors at the structural
/// owner: after it, every invariant §7.5 pins on a shard outcome is checked
/// against the canonical source, so the external-merge path cannot false-PASS on
/// any malformed input. The in-process `verify_sharded` path is unchanged: its
/// outcomes satisfy all of this by construction.
pub fn merge_and_check(
    store: &Store,
    falsified: &BTreeSet<String>,
    seed: &BTreeSet<(String, usize)>,
    outcomes: &[ShardOutcome],
    n: u64,
) -> MergeReport {
    // §7.5: `n >= 1`. `shard_of`'s `mod n` cannot be evaluated at n = 0, and a
    // zero-shard partition defines nothing — fail up front rather than merge a
    // vacuous PASS.
    if n == 0 {
        return MergeReport {
            proven: BTreeSet::new(),
            carried: BTreeMap::new(),
            mismatches: vec![
                "SHARDING: shard count n must be >= 1 (SPEC §7.5); n = 0 defines no partition".to_string(),
            ],
            seed_id: seed_identity(seed),
        };
    }

    // SHARD-INDEX COMPLETENESS (§7.5): exactly one outcome per index {0,…,n-1}.
    // Owned here so no caller can bypass it — an empty or incomplete campaign is a
    // FAIL, never a vacuous PASS. Counted from each outcome's DECLARED index; the
    // per-verdict `shard_of == out.shard` check below separately proves that index
    // is not a lie, so the two together pin both "every shard is present exactly
    // once" and "every shard carries the definitions it should".
    let mut index_mismatches: Vec<String> = Vec::new();
    let mut index_counts: BTreeMap<u64, usize> = BTreeMap::new();
    for out in outcomes {
        *index_counts.entry(out.shard).or_insert(0) += 1;
        if out.shard >= n {
            index_mismatches.push(format!(
                "SHARD-INDEX: an outcome declares shard {}, out of range for n={} (valid 0..{})",
                out.shard, n, n
            ));
        }
    }
    for (idx, count) in &index_counts {
        if *idx < n && *count > 1 {
            index_mismatches.push(format!(
                "SHARD-INDEX: shard {} was supplied {} times — exactly one emission per index is required",
                idx, count
            ));
        }
    }
    for i in 0..n {
        if !index_counts.contains_key(&i) {
            index_mismatches.push(format!(
                "SHARD-INDEX: shard {} is missing — one emission per index {{0..{}}} is required",
                i,
                n - 1
            ));
        }
    }

    let universe = shardable_defs(store, falsified);
    let prop_count = |hash: &str| -> usize {
        match store.def_by_hash.get(hash) {
            Some(Def::Func { props, .. }) => props.len(),
            _ => 0,
        }
    };
    let name_of = |hash: &str| -> String {
        store
            .func_by_hash
            .get(hash)
            .map(|fi| fi.name.clone())
            .unwrap_or_else(|| short_hash(hash))
    };

    let mut raw: BTreeMap<(String, usize), PropVerdict> = BTreeMap::new();
    // owners: def -> the shard labels of the outcomes that VALIDLY contributed a
    // verdict for it. Recorded once per (def, outcome position) so a def is not
    // double-counted for its several properties, but two DISTINCT outcomes wearing
    // the same shard label both count — that is a real double attempt.
    let mut owners: BTreeMap<String, Vec<u64>> = BTreeMap::new();
    let mut provenance: Vec<String> = Vec::new();

    for out in outcomes {
        let mut counted: BTreeSet<&String> = BTreeSet::new();
        for (key, v) in &out.verdicts {
            let (h, pi) = (&key.0, key.1);
            // (3) shardable & present in the store.
            if !universe.contains(h) {
                provenance.push(format!(
                    "PROVENANCE: shard {} contributed a verdict for {} prop {}, which is not a shardable definition in the store (absent, falsified, or property-free)",
                    out.shard,
                    name_of(h),
                    pi
                ));
                continue;
            }
            // (1) property index in range.
            let pc = prop_count(h);
            if pi >= pc {
                provenance.push(format!(
                    "PROVENANCE: shard {} contributed a verdict for {} prop {}, but that definition has only {} propert{} (index out of range)",
                    out.shard,
                    name_of(h),
                    pi,
                    pc,
                    if pc == 1 { "y" } else { "ies" }
                ));
                continue;
            }
            // (2) canonical assignment: the def must hash to THIS outcome's shard.
            let canonical = shard_of(h, n);
            if canonical != out.shard {
                provenance.push(format!(
                    "PROVENANCE: shard {} contributed a verdict for {} prop {}, but that definition is canonically assigned to shard {} (shard_of), not {}",
                    out.shard,
                    name_of(h),
                    pi,
                    canonical,
                    out.shard
                ));
                continue;
            }
            // Valid contribution: union it and record ownership once per outcome.
            raw.entry(key.clone()).or_insert_with(|| v.clone());
            if counted.insert(h) {
                owners.entry(h.clone()).or_default().push(out.shard);
            }
        }
    }
    // Sort each def's owner list so the >1 diagnostic is deterministic.
    for v in owners.values_mut() {
        v.sort_unstable();
    }
    let mut report = check_against_seed(store, falsified, seed, &raw, &owners);
    report.mismatches.extend(index_mismatches);
    report.mismatches.extend(provenance);
    report
}

/// Run every shard 0..n of the seeded verifier and self-check the union against
/// S (SPEC §7.5). The complete verifier as a single call: it partitions with the
/// normative `shard_of` key, attempts each property once, merges, and returns a
/// `MergeReport` whose `ok()` is the pass. `attempt` is the per-property oracle
/// (z3 in production, a deterministic oracle in tests).
pub fn verify_sharded<F>(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
    seed: &BTreeSet<(String, usize)>,
    n: u64,
    mut attempt: F,
) -> MergeReport
where
    F: FnMut(&str, usize, &Prop, &[(String, usize, bool)]) -> PropVerdict,
{
    // §7.5: `n >= 1`. Reject `n = 0` up front, BEFORE constructing any outcome —
    // otherwise the `0..0` loop attempts nothing and merges to a vacuous "success"
    // on an empty corpus, and `shard_of`'s `mod n` would panic on a non-empty one.
    // A verifier that reports PASS for an impossible shard count is the exact class
    // of silent-pass this self-check exists to prevent, so it must fail loudly.
    if n == 0 {
        return MergeReport {
            proven: BTreeSet::new(),
            carried: BTreeMap::new(),
            mismatches: vec![
                "SHARDING: shard count n must be >= 1 (SPEC §7.5); n = 0 defines no partition".to_string(),
            ],
            seed_id: seed_identity(seed),
        };
    }
    let mut outcomes = Vec::with_capacity(n as usize);
    for i in 0..n {
        outcomes.push(prove_shard_with(
            store,
            falsified,
            hints,
            seed,
            |h| shard_of(h, n),
            i,
            &mut attempt,
        ));
    }
    merge_and_check(store, falsified, seed, &outcomes, n)
}

/// Run ONE shard i of n with the real z3 driver (SPEC §7.5). The z3 counterpart
/// of `prove_shard_with`, building the `Prover` internally exactly as `prove_all`
/// wraps `prove_all_with`, so callers need no access to the private `Prover`.
pub fn prove_shard(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
    seed: &BTreeSet<(String, usize)>,
    i: u64,
    n: u64,
) -> ShardOutcome {
    let prover = Prover { store };
    prove_shard_with(store, falsified, hints, seed, |h| shard_of(h, n), i, |hash, _pi, prop, cands| {
        prover.prove_prop(hash, prop, cands)
    })
}

/// Run all n shards with the real z3 driver and self-check the union against S
/// (SPEC §7.5) — the z3 counterpart of `verify_sharded`.
pub fn verify_sharded_z3(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
    seed: &BTreeSet<(String, usize)>,
    n: u64,
) -> MergeReport {
    let prover = Prover { store };
    verify_sharded(store, falsified, hints, seed, n, |hash, _pi, prop, cands| {
        prover.prove_prop(hash, prop, cands)
    })
}

// ===========================================================================
// SPEC §7.5 — the shard-emission WIRE FORMAT (#98 parallel campaign).
//
// A parallel campaign runs `--shard 0/n … (n-1)/n` as independent jobs, collects
// their stdout, and self-checks the UNION with `--merge-shards`. Each job's
// stdout must therefore round-trip: what one shard emits is exactly what the
// merge needs to reconstruct that shard's `ShardOutcome`, keyed by definition
// HASH (identity, reproducible across runners) rather than display name.
//
// Format (`oath-sharded-verification/v1`), line-based, tab-separated, UTF-8:
//
//   # oath-sharded-verification/v1 — CONTRIBUTION ONLY (shard i of n) …
//   shard<TAB>{i}<TAB>{n}
//   campaign<TAB>{campaign_identity}
//   {def_hash}<TAB>{prop_index}<TAB>proven
//   {def_hash}<TAB>{prop_index}<TAB>unproven
//   {def_hash}<TAB>{prop_index}<TAB>aborted<TAB>{reason...}
//
// Lines beginning `#` are comments (the human "not verified until merged"
// banner is one). The `shard` and `campaign` control lines appear once each. The
// `campaign` id binds the FULL determinism context (S, hints, solver, rlimit) —
// see `campaign_identity` — so a merge admits only emissions that ran the same F.
// Every other line is one attempted property's verdict, emitted sorted by
// (def_hash, prop_index); the `def_hash` is 64 lowercase hex characters (an O1
// identity hash, §1) and is validated as such on parse. For an `aborted` verdict
// the reason is the remainder of the line after the fourth field, so a reason
// containing tabs survives; a reason must not contain a newline (abort reasons
// are single-line by construction). The parser recovers the shard's `defs` as the
// set of definition hashes carrying a verdict — every shardable def has ≥1
// property, so this reconstructs the attempted-definition set exactly.
// ===========================================================================

/// The parsed content of one `--shard i/n` emission: enough to reconstruct the
/// `ShardOutcome`, plus the `i`/`n`/campaign-identity the shard declared. The
/// merge validates all three against its own (`n`, and the campaign identity it
/// recomputes from its hints/solver/rlimit) so a shard computed against a
/// different partition or a different determinism context cannot be merged in.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedShard {
    pub i: u64,
    pub n: u64,
    pub campaign_id: String,
    pub outcome: ShardOutcome,
}

/// True iff `s` is a well-formed O1 identity hash: exactly 64 lowercase hex
/// characters (SPEC §1). Used to reject a malformed emission on parse — before any
/// untrusted string reaches code that indexes or hashes it.
fn is_identity_hash(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

/// Render one shard's `ShardOutcome` as the `oath-sharded-verification/v1` wire
/// format (see the module comment above). `campaign_id` is the caller-computed
/// `campaign_identity` (which binds S, hints, solver and rlimit); it is emitted so
/// a merge can reject a shard that ran a different `F`. The leading `#` banner
/// states, in the bytes themselves, that a single shard is a CONTRIBUTION and not
/// a verified result until merged.
pub fn format_shard_emission(out: &ShardOutcome, n: u64, campaign_id: &str) -> String {
    let mut s = String::new();
    s.push_str(&format!(
        "# oath-sharded-verification/v1 — CONTRIBUTION ONLY (shard {} of {}); NOT verified until merged with `oath prove --merge-shards`\n",
        out.shard, n
    ));
    s.push_str(&format!("shard\t{}\t{}\n", out.shard, n));
    s.push_str(&format!("campaign\t{}\n", campaign_id));
    // `verdicts` is a BTreeMap, so iteration is already sorted by (hash, pi).
    for ((hash, pi), v) in &out.verdicts {
        match v {
            PropVerdict::Proven => s.push_str(&format!("{}\t{}\tproven\n", hash, pi)),
            PropVerdict::Unproven => s.push_str(&format!("{}\t{}\tunproven\n", hash, pi)),
            PropVerdict::Aborted(reason) => {
                s.push_str(&format!("{}\t{}\taborted\t{}\n", hash, pi, reason))
            }
        }
    }
    s
}

/// Parse one `oath-sharded-verification/v1` emission back into a `ParsedShard`.
/// The inverse of `format_shard_emission`: `parse_shard_emission(format(out, n,
/// campaign_id))` recovers `i`, `n`, the campaign identity, and a `ShardOutcome`
/// equal to `out`. Malformed input — including a `def_hash` that is not a 64-char
/// lowercase-hex identity hash — is a hard error (the merge treats a shard it
/// cannot parse as a failed campaign, never as an empty-but-fine one).
pub fn parse_shard_emission(text: &str) -> Result<ParsedShard, String> {
    let mut shard_hdr: Option<(u64, u64)> = None;
    let mut campaign_id: Option<String> = None;
    let mut verdicts: BTreeMap<(String, usize), PropVerdict> = BTreeMap::new();
    let mut defs: BTreeSet<String> = BTreeSet::new();
    for (lineno, raw) in text.lines().enumerate() {
        let line = raw.strip_suffix('\r').unwrap_or(raw); // tolerate CRLF
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let mut fields = line.split('\t');
        let tag = fields.next().unwrap_or("");
        match tag {
            "shard" => {
                let i = fields
                    .next()
                    .and_then(|s| s.parse::<u64>().ok())
                    .ok_or_else(|| format!("line {}: malformed `shard` index", lineno + 1))?;
                let n = fields
                    .next()
                    .and_then(|s| s.parse::<u64>().ok())
                    .ok_or_else(|| format!("line {}: malformed `shard` count", lineno + 1))?;
                shard_hdr = Some((i, n));
            }
            "campaign" => {
                let id = fields
                    .next()
                    .ok_or_else(|| format!("line {}: missing campaign identity", lineno + 1))?;
                campaign_id = Some(id.to_string());
            }
            hash => {
                // A verdict line: {def_hash}\t{pi}\t{verdict}[\t{reason}]. The
                // def_hash is UNTRUSTED — validate it as an O1 identity hash
                // BEFORE it is stored, so nothing downstream ever indexes or
                // slices a non-hex/multibyte string.
                if !is_identity_hash(hash) {
                    return Err(format!(
                        "line {}: `{}` is not a 64-char lowercase-hex identity hash",
                        lineno + 1,
                        short_hash(hash)
                    ));
                }
                let pi = fields
                    .next()
                    .and_then(|s| s.parse::<usize>().ok())
                    .ok_or_else(|| format!("line {}: malformed property index", lineno + 1))?;
                let verdict = fields
                    .next()
                    .ok_or_else(|| format!("line {}: missing verdict", lineno + 1))?;
                let v = match verdict {
                    "proven" => PropVerdict::Proven,
                    "unproven" => PropVerdict::Unproven,
                    "aborted" => {
                        // The reason is the remainder of the line, tabs and all.
                        // `splitn(4, …)` on the ORIGINAL line yields it as the 4th
                        // part; an aborted verdict with no reason is tolerated.
                        let reason = line.splitn(4, '\t').nth(3).unwrap_or("").to_string();
                        PropVerdict::Aborted(reason)
                    }
                    other => {
                        return Err(format!(
                            "line {}: unknown verdict `{}` (want proven|unproven|aborted)",
                            lineno + 1,
                            other
                        ))
                    }
                };
                defs.insert(hash.to_string());
                if verdicts.insert((hash.to_string(), pi), v).is_some() {
                    return Err(format!(
                        "line {}: duplicate verdict for {} prop {}",
                        lineno + 1,
                        short_hash(hash),
                        pi
                    ));
                }
            }
        }
    }
    let (i, n) = shard_hdr.ok_or("missing `shard i n` header line")?;
    let campaign_id = campaign_id.ok_or("missing `campaign <identity>` header line")?;
    Ok(ParsedShard { i, n, campaign_id, outcome: ShardOutcome { shard: i, defs, verdicts } })
}

pub struct ProofResult {
    pub proven: Vec<bool>, // per prop index
    /// SPEC §7.2 (#72): per prop index, whether the property ABORTED — an
    /// attempt was environmentally invalid and the property therefore has no
    /// valid verdict this run. Reported DISTINCTLY from `proven == false`, which
    /// is the positive claim "attempted validly and not proven". A property may
    /// be both `proven` and `aborted`: its prior recorded PROVEN was carried
    /// forward unchanged (never demoted) while this run could not re-derive it.
    pub aborted: Vec<bool>,
}

/// Prove all properties of every func definition; returns per-def results and
/// records proven props so later definitions can use them as lemmas.
pub fn prove_all(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
) -> BTreeMap<String, ProofResult> {
    let prover = Prover { store };
    prove_all_with(store, falsified, hints, |hash, _pi, prop, cands| {
        prover.prove_prop(hash, prop, cands)
    })
}

/// The recording layer of `prove_all`, with the per-property attempt supplied by
/// the caller. The real driver passes `Prover::prove_prop` (z3); tests pass a
/// deterministic oracle, which is the only way to exercise the #72 abort
/// semantics without depending on timing or on a solver that happens to die.
pub fn prove_all_with<F>(
    store: &Store,
    falsified: &BTreeSet<String>,
    hints: &Hints,
    mut attempt: F,
) -> BTreeMap<String, ProofResult>
where
    F: FnMut(&str, usize, &Prop, &[(String, usize, bool)]) -> PropVerdict,
{
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
        store.func_by_hash.get(hash).map(|fi| fi.name.clone()).unwrap_or_else(|| short_hash(hash))
    };

    let mut recorded: BTreeSet<(String, usize)> = BTreeSet::new();
    // SPEC §7.2 (#72): properties with no valid verdict this round, and why.
    let mut aborted: BTreeMap<(String, usize), String> = BTreeMap::new();
    // The per-property cache keys on the actual candidate set. An ABORTED verdict
    // is cached like any other: re-attempting the same script under the same
    // lemma state would burn the wall cap again per round, and an abort records
    // nothing either way, so caching cannot change what is recorded — only how
    // long the run takes.
    let mut cache: BTreeMap<(String, usize), (Vec<(String, usize, bool)>, PropVerdict)> =
        BTreeMap::new();
    for round in 0..8 {
        if progress {
            eprintln!("[prove] === round {} start ({} defs) ===", round, total_defs);
        }
        let mut in_run: BTreeSet<(String, usize)> = BTreeSet::new();
        // Properties ABORTED this round (SPEC §7.2 #72): no valid verdict exists
        // for them. Reset per round — only the settling round's aborts are
        // reported, exactly as only its verdicts are recorded.
        aborted.clear();
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
                    let cands = candidate_lemmas(store, hash, pi, prop, &combined, hints);
                    let verdict = match cache.get(&key) {
                        Some((c, r)) if *c == cands => r.clone(),
                        _ => {
                            let r = attempt(hash, pi, prop, &cands);
                            cache.insert(key.clone(), (cands, r.clone()));
                            r
                        }
                    };
                    match verdict {
                        PropVerdict::Proven => {
                            in_run.insert(key.clone());
                            combined.insert(key.clone());
                            // A property may abort on one pass of the inner growth
                            // loop and PROVE on a later pass of the same round
                            // (its candidate set grew when a sibling proved). The
                            // proof is a valid verdict, so the earlier abort is no
                            // longer this property's state and must not be
                            // reported: "aborted" claims no valid verdict EXISTS.
                            aborted.remove(&key);
                            changed = true;
                        }
                        PropVerdict::Unproven => {}
                        PropVerdict::Aborted(reason) => {
                            // SPEC §7.2 (#72), PER PROPERTY. No valid verdict for
                            // THIS property: it records nothing of its own, its
                            // siblings are untouched, and the run continues. Its
                            // STANDING VERDICT stands — and the spec pins that
                            // referent: "the proven set the kernel holds for the
                            // object AT THE START OF THE CURRENT ROUND of the
                            // run-stability fixpoint … INCLUDING proofs this run
                            // established in an earlier round … NOT a snapshot
                            // taken before the run". `recorded` is exactly that
                            // set: it is assigned only at round end, so throughout
                            // a round it holds what the previous round settled. So
                            // a property proven in round 0 and aborted in round 1
                            // keeps its proof; a cold kernel's round-0 standing set
                            // is empty and carries nothing.
                            //
                            // The carry-forward is not a NEW lemma (the property
                            // was already in `recorded`, hence already in
                            // `combined` at round start, and §7.2: "a carried-
                            // forward proof REMAINS admissible as a lemma"), so it
                            // does not set `changed`: an aborted property gains
                            // nothing this run, the conservative direction for the
                            // run-stability fixpoint.
                            aborted.insert(key.clone(), reason);
                            if recorded.contains(&key) {
                                in_run.insert(key);
                            }
                        }
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
                    .map(|pi| {
                        let key = (hash.clone(), pi);
                        if in_run.contains(&key) {
                            '+'
                        } else if aborted.contains_key(&key) {
                            '!' // no valid verdict (#72), NOT "unproven"
                        } else {
                            '-'
                        }
                    })
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
            let aborts = (0..props.len())
                .map(|pi| aborted.contains_key(&(hash.clone(), pi)))
                .collect();
            results.insert(hash.clone(), ProofResult { proven: flags, aborted: aborts });
        }
    }
    // SPEC §7.2 (#72): aborted properties are reported DISTINCTLY, on stderr,
    // naming the invalidating condition — "no valid verdict exists" is a
    // different claim from "not proven" and must never be silently rendered as
    // the latter. The run itself SUCCEEDS with partial results.
    for ((hash, pi), reason) in &aborted {
        eprintln!(
            "ABORTED (no valid verdict, SPEC §7.2 #72): {} prop {}{} — {}",
            name_of(hash),
            pi,
            if recorded.contains(&(hash.clone(), *pi)) {
                " [prior PROVEN carried forward unchanged]"
            } else {
                ""
            },
            reason
        );
    }
    results
}

// ===========================================================================
// SPEC §6.1.1: integer ranking functions (the `measure` termination verdict)
// ===========================================================================

use std::cell::RefCell;

thread_local! {
    // Re-entrancy guard: translating a NESTED self-call inside the ranking walk
    // recurses back into termination()->measure_terminates for the SAME hash
    // (via register_fun -> build_axiom -> is_total). Break that cycle
    // conservatively — a definition we cannot finish analyzing is not `measure`.
    static MEASURE_ACTIVE: RefCell<HashSet<String>> = RefCell::new(HashSet::new());
    // Result memo (content-addressed hash -> verdict). Pure per definition, so
    // safe to cache for the lifetime of a process that operates over one store.
    static MEASURE_CACHE: RefCell<BTreeMap<String, bool>> = RefCell::new(BTreeMap::new());
}

/// A candidate integer measure (SPEC §6.1.1 step 3): a single Int parameter, an
/// ordered difference of two distinct Int parameters, or (#57) an Int-typed field
/// of a single-constructor datatype parameter, addressed by its selector.
enum Cand {
    Single(usize),
    Diff(usize, usize),
    /// `(selector p_i)` — the Int field `sel` of single-constructor datatype
    /// parameter `pi`.
    Field { pi: usize, sel: String },
}

impl Cand {
    /// μ(params): substitutes each `p_i` by the parameter constant.
    fn params(&self) -> String {
        match self {
            Cand::Single(i) => format!("p{}", i),
            Cand::Diff(i, j) => format!("(- p{} p{})", i, j),
            Cand::Field { pi, sel } => format!("({} p{})", sel, pi),
        }
    }
    /// μ(args): substitutes each `p_i` by the SMT term passed at position i.
    fn args(&self, args: &[String]) -> String {
        match self {
            Cand::Single(i) => args[*i].clone(),
            Cand::Diff(i, j) => format!("(- {} {})", args[*i], args[*j]),
            Cand::Field { pi, sel } => format!("({} {})", sel, args[*pi]),
        }
    }
}

/// One recorded self-call site: the path guards reaching it and the translated
/// SMT term passed at each parameter position.
struct MSite {
    guards: Vec<String>,
    args: Vec<String>,
}

/// Conjunction of a site's reaching guards as one SMT boolean: `true` if none,
/// the sole guard if one, `(and …)` otherwise.
fn guard_conj(guards: &[String]) -> String {
    if guards.is_empty() {
        "true".to_string()
    } else if guards.len() == 1 {
        guards[0].clone()
    } else {
        format!("(and {})", guards.join(" "))
    }
}

/// Walk `def_hash`'s body with the §6.1.1 self-call site collector, rendering
/// guards and recursive-argument expressions over `param_env` (the SMT constants
/// standing for the function's parameters). Side effects on `cx`: any function or
/// sort a guard/argument references is declared into `cx`. Returns
/// `(sites, fresh_decls)` — `fresh_decls` being declarations for any fresh
/// constants the walk introduced for let/match/lam binders — or `None` if the
/// walk cannot fully analyze the body (hard-fail) or records no self-call.
fn collect_self_sites(
    cx: &mut Cx,
    def_hash: &str,
    param_env: &[(String, Ty)],
) -> Option<(Vec<MSite>, String)> {
    let store: &Store = cx.store;
    let (body, tyvars, n) = match store.def_by_hash.get(def_hash) {
        Some(Def::Func { body, tyvars, .. }) => {
            let n = store.func_by_hash.get(def_hash).map(|fi| fi.param_names.len()).unwrap_or(0);
            (body.clone(), *tyvars, n)
        }
        _ => return None,
    };
    if param_env.len() != n {
        return None;
    }
    let tyenv: Vec<Ty> = vec![Ty::Int; tyvars as usize];
    let inner = strip_lams(&body, n);
    let mut w = MeasureWalk {
        cx,
        self_hash: def_hash.to_string(),
        tyenv,
        n,
        sites: Vec::new(),
        hard_fail: false,
        fresh: 0,
        fresh_decls: String::new(),
    };
    let mut env = param_env.to_vec();
    w.walk(inner, &mut env, &[], false);
    if w.hard_fail || w.sites.is_empty() {
        return None;
    }
    Some((w.sites, w.fresh_decls))
}

struct MeasureWalk<'a, 'c> {
    cx: &'c mut Cx<'a>,
    self_hash: String,
    tyenv: Vec<Ty>,
    n: usize,
    sites: Vec<MSite>,
    hard_fail: bool,
    fresh: usize,
    // declarations for fresh constants introduced by let/match/lam binders.
    fresh_decls: String,
}

impl<'a, 'c> MeasureWalk<'a, 'c> {
    fn tr_term(&mut self, t: &Term, env: &[(String, Ty)]) -> Result<(String, Ty), ()> {
        let tyenv = self.tyenv.clone();
        let sh = self.self_hash.clone();
        self.cx.tr(t, env, &tyenv, &sh, &tyenv)
    }

    fn fresh_const(&mut self, ty: &Ty) -> String {
        let sort = {
            let tyenv = self.tyenv.clone();
            let applied = apply_tyenv(ty, &tyenv);
            sort_of(self.cx.store, &applied, &mut self.cx.sc)
        };
        let name = format!("mfresh{}", self.fresh);
        self.fresh += 1;
        self.fresh_decls.push_str(&format!("(declare-const {} {})\n", name, sort));
        name
    }

    fn walk(&mut self, t: &Term, env: &mut Vec<(String, Ty)>, guards: &[String], poisoned: bool) {
        match t {
            Term::If { a, b, c } => {
                // Translate the condition; on failure both branches are poisoned.
                match self.tr_term(a, env) {
                    Ok((cstr, _)) => {
                        let mut gt = guards.to_vec();
                        gt.push(cstr.clone());
                        self.walk(b, env, &gt, poisoned);
                        let mut ge = guards.to_vec();
                        ge.push(format!("(not {})", cstr));
                        self.walk(c, env, &ge, poisoned);
                    }
                    Err(()) => {
                        self.walk(b, env, guards, true);
                        self.walk(c, env, guards, true);
                    }
                }
            }
            Term::Let { ty, a, b } => {
                // Bind the variable to its translated value, or a fresh constant
                // of the bound type on failure; then walk the body.
                let binding = match self.tr_term(a, env) {
                    Ok((astr, aty)) => (astr, aty),
                    Err(()) => {
                        let lty = apply_tyenv(ty, &self.tyenv.clone());
                        (self.fresh_const(ty), lty)
                    }
                };
                env.push(binding);
                self.walk(b, env, guards, poisoned);
                env.pop();
            }
            Term::Match { hash, a, arms } => {
                // Walk the scrutinee for self-calls.
                self.walk(a, env, guards, poisoned);
                // SPEC §6.1.1 (#57): translate the scrutinee to its SMT expression
                // and bind each arm's constructor fields to the scrutinee's
                // SELECTORS applied to it (NOT fresh constants), so a measure over
                // a datatype field stays connected to the counter the body reads.
                let scrut = self.tr_term(a, env).ok();
                let di = self.cx.store.data_by_hash.get(hash).cloned();
                let single_ctor = di.as_ref().map(|d| d.ctors.len() == 1).unwrap_or(false);
                for (i, arm) in arms.iter().enumerate() {
                    let arity =
                        di.as_ref().and_then(|d| d.ctors.get(i)).map(|c| c.1.len()).unwrap_or(0);
                    let mut binds: Vec<(String, Ty)> = Vec::new();
                    let mut extra_guard: Option<String> = None;
                    let mut arm_poison = poisoned;
                    if let (Some((se, Ty::Data { hash: h, args })), Some(d)) = (&scrut, &di) {
                        if h == hash && i < d.ctors.len() {
                            let dty = Ty::Data { hash: h.clone(), args: args.clone() };
                            let sortname = {
                                let mut sc = Sorts::default();
                                sort_of(self.cx.store, &dty, &mut sc)
                            };
                            let csmt = ctor_smt(&d.ctors[i].0, &sortname);
                            for (j, f) in d.ctors[i].1.iter().enumerate() {
                                binds.push((
                                    format!("({}_{} {})", csmt, j, se),
                                    inst_field(f, args, hash),
                                ));
                            }
                            // Add the constructor tester as a path guard, OMITTED
                            // for a single-constructor datatype (always true).
                            if !single_ctor {
                                extra_guard = Some(format!("((_ is {}) {})", csmt, se));
                            }
                        }
                    }
                    if binds.len() != arity {
                        // Selectors/field sorts undeterminable: poison, bind fresh.
                        arm_poison = true;
                        binds.clear();
                        for _ in 0..arity {
                            binds.push((self.fresh_const(&Ty::Int), Ty::Int));
                        }
                    }
                    for (name, ty) in &binds {
                        env.push((name.clone(), ty.clone()));
                    }
                    let mut arm_guards = guards.to_vec();
                    if let Some(g) = extra_guard {
                        arm_guards.push(g);
                    }
                    self.walk(arm, env, &arm_guards, arm_poison);
                    for _ in 0..binds.len() {
                        env.pop();
                    }
                }
            }
            Term::Lam { ty, a } => {
                let c = self.fresh_const(ty);
                env.push((c, apply_tyenv(ty, &self.tyenv.clone())));
                self.walk(a, env, guards, poisoned);
                env.pop();
            }
            Term::App { .. } => {
                // Unwind the application spine.
                let mut args: Vec<&Term> = Vec::new();
                let mut cur = t;
                while let Term::App { a, b } = cur {
                    args.push(b);
                    cur = a;
                }
                args.reverse();
                if let Term::SelfRef { .. } = cur {
                    self.record_self(&args, env, guards, poisoned);
                } else {
                    // all other terms: walk every subterm.
                    self.walk(cur, env, guards, poisoned);
                    for a in args {
                        self.walk(a, env, guards, poisoned);
                    }
                }
            }
            Term::SelfRef { .. } => {
                // self with no application spine: fewer args than parameters.
                self.hard_fail = true;
            }
            Term::Prim { args, .. } | Term::Ctor { args, .. } | Term::Record { args, .. } => {
                for a in args {
                    self.walk(a, env, guards, poisoned);
                }
            }
            Term::Field { a, .. } => self.walk(a, env, guards, poisoned),
            Term::Ref { .. }
            | Term::Var(_)
            | Term::Int(_)
            | Term::Rat { .. }
            | Term::Float(_)
            | Term::Bool(_) => {}
        }
    }

    fn record_self(
        &mut self,
        args: &[&Term],
        env: &[(String, Ty)],
        guards: &[String],
        poisoned: bool,
    ) {
        // A self reached with fewer arguments than parameters, on a poisoned
        // path, or with an untranslatable argument forfeits the whole attempt.
        if poisoned || args.len() != self.n {
            self.hard_fail = true;
            return;
        }
        let mut arg_strs = Vec::with_capacity(self.n);
        for a in args {
            match self.tr_term(a, env) {
                Ok((s, _)) => arg_strs.push(s),
                Err(()) => {
                    self.hard_fail = true;
                    return;
                }
            }
        }
        self.sites.push(MSite { guards: guards.to_vec(), args: arg_strs });
    }
}

/// SPEC §6.1.1: attempt a Z3-verified integer ranking function. Returns true iff
/// some candidate integer measure strictly decreases and stays >= 0 at every
/// self-call given the path guards. A missing solver, the absence of any Int
/// parameter, an incompletely-analyzable self-call, or no self-call at all all
/// yield false — never a spurious `measure`.
pub fn measure_terminates(store: &Store, hash: &str) -> bool {
    if let Some(v) = MEASURE_CACHE.with(|c| c.borrow().get(hash).copied()) {
        return v;
    }
    let reenter = MEASURE_ACTIVE.with(|s| !s.borrow_mut().insert(hash.to_string()));
    if reenter {
        return false; // do not cache: a re-entrant probe, not the real verdict.
    }
    let r = measure_terminates_inner(store, hash);
    MEASURE_ACTIVE.with(|s| {
        s.borrow_mut().remove(hash);
    });
    MEASURE_CACHE.with(|c| {
        c.borrow_mut().insert(hash.to_string(), r);
    });
    r
}

fn measure_terminates_inner(store: &Store, hash: &str) -> bool {
    let (body, fty, tyvars, n) = match store.def_by_hash.get(hash) {
        Some(Def::Func { body, ty, tyvars, .. }) => {
            let n = store.func_by_hash.get(hash).map(|fi| fi.param_names.len()).unwrap_or(0);
            (body.clone(), ty.clone(), *tyvars, n)
        }
        _ => return false,
    };
    let (ptys, _ret) = param_types(&fty, n);
    if ptys.len() != n {
        return false;
    }
    // A placeholder type environment keeps `tr`'s `apply_tyenv` total; type
    // variables never enter an integer measure (a translation that structurally
    // uses one only fails the site's obligation, never passes it spuriously).
    let tyenv: Vec<Ty> = vec![Ty::Int; tyvars as usize];

    let mut cx = Cx::new(store);
    // Parameter declarations (SPEC §6.1.1 step 1): Int parameters as `Int`; a type
    // variable over a fresh uninterpreted sort (it never enters a measure); every
    // other parameter (datatype/record/…) over its REAL sort, so a
    // single-constructor datatype parameter's field selectors (#57) and any match
    // on it stay well-formed.
    let mut sort_decls = String::new();
    let mut param_decls = String::new();
    let mut env: Vec<(String, Ty)> = Vec::with_capacity(n);
    for i in 0..n {
        let name = format!("p{}", i);
        match &ptys[i] {
            Ty::Int => {
                param_decls.push_str(&format!("(declare-const {} Int)\n", name));
            }
            Ty::Var(_) => {
                let sort = format!("MSort{}", i);
                sort_decls.push_str(&format!("(declare-sort {} 0)\n", sort));
                param_decls.push_str(&format!("(declare-const {} {})\n", name, sort));
            }
            other => {
                let applied = apply_tyenv(other, &tyenv);
                let sort = sort_of(store, &applied, &mut cx.sc);
                param_decls.push_str(&format!("(declare-const {} {})\n", name, sort));
            }
        }
        env.push((name, ptys[i].clone()));
    }

    // SPEC §6.1.1 step 3: candidates in order — each Int parameter, then each
    // ordered difference of two distinct Int parameters, then (#57) each Int-typed
    // field of a single-constructor datatype parameter, addressed by its selector.
    let int_params: Vec<usize> = (0..n).filter(|&i| matches!(ptys[i], Ty::Int)).collect();
    let mut cands: Vec<Cand> = int_params.iter().map(|&i| Cand::Single(i)).collect();
    for &i in &int_params {
        for &j in &int_params {
            if i != j {
                cands.push(Cand::Diff(i, j));
            }
        }
    }
    for i in 0..n {
        if let Ty::Data { hash: dh, args } = &ptys[i] {
            if let Some(di) = store.data_by_hash.get(dh) {
                if di.ctors.len() == 1 {
                    let sortname = {
                        let mut sc = Sorts::default();
                        sort_of(store, &ptys[i], &mut sc)
                    };
                    let csmt = ctor_smt(&di.ctors[0].0, &sortname);
                    for (j, f) in di.ctors[0].1.iter().enumerate() {
                        if matches!(inst_field(f, args, dh), Ty::Int) {
                            cands.push(Cand::Field { pi: i, sel: format!("{}_{}", csmt, j) });
                        }
                    }
                }
            }
        }
    }
    // With no candidate measure at all (no Int parameter and no single-constructor
    // datatype Int field), the check fails.
    if cands.is_empty() {
        return false;
    }

    let inner = strip_lams(&body, n);
    let mut w = MeasureWalk {
        cx: &mut cx,
        self_hash: hash.to_string(),
        tyenv,
        n,
        sites: Vec::new(),
        hard_fail: false,
        fresh: 0,
        fresh_decls: String::new(),
    };
    let mut walk_env = env.clone();
    w.walk(inner, &mut walk_env, &[], false);

    // SPEC §6.1.1 step 2 completeness: any self-call the walk cannot fully
    // analyze, or the absence of any self-call, forfeits the attempt.
    if w.hard_fail || w.sites.is_empty() {
        return false;
    }
    let sites = w.sites;
    let fresh_decls = w.fresh_decls;
    // All declarations discovered while declaring parameters and translating
    // guards/arguments (datatypes, callee function symbols) must precede the
    // obligation. NOTE: only the guard predicate and decrease are asserted —
    // callee DEFINING axioms are NOT added (SPEC §6.1.1 step 4).
    let aux_decls: String = cx.sc.decls.join("");
    let preamble = format!("{}{}{}{}", sort_decls, aux_decls, fresh_decls, param_decls);

    // SPEC §6.1.1 step 4: the first candidate that clears EVERY site wins.
    for cand in &cands {
        if sites.iter().all(|site| measure_site_unsat(&preamble, cand, site)) {
            return true;
        }
    }
    false
}

/// The per-site obligation (SPEC §6.1.1 step 4): `guards => (μ(args) < μ(params)
/// && μ(params) >= 0)` is valid iff its negation is UNSAT. Linear integer
/// arithmetic is decidable, so the solver never reports `unknown`; any non-`unsat`
/// (including a missing solver) means the candidate fails this site.
fn measure_site_unsat(preamble: &str, cand: &Cand, site: &MSite) -> bool {
    let mu_params = cand.params();
    let mu_args = cand.args(&site.args);
    let neg = format!("(not (and (< {} {}) (>= {} 0)))", mu_args, mu_params, mu_params);
    let body = if site.guards.is_empty() {
        neg
    } else {
        format!("(and {} {})", site.guards.join(" "), neg)
    };
    let script = format!("{}(assert {})\n(check-sat)\n", preamble, body);
    matches!(run_z3(&script, z3_rlimit()), Ok(Outcome::Unsat))
}

#[cfg(test)]
mod tests {
    //! Unit tests for the #67 author-hint rules of SPEC §7.2. These are pure —
    //! `candidate_lemmas` and `direct_script` build the lemma set and the script
    //! BYTES without running z3 — so they need no solver on PATH.
    use super::*;
    use crate::elaborate::elaborate_corpus;

    /// A three-definition corpus. `quad`'s body calls `twice`, so `twice` is in
    /// `quad`'s footprint AND dependency closure (its lemma is admissible with no
    /// hint at all). `unrelated` is reachable from nothing, so its lemma is
    /// neither a candidate nor admissible for a `quad` goal — the case a hint
    /// exists to override.
    const SRC: &str = r#"
(defn twice [] [(n Int)] Int
  (* 2 n)
  (prop doubles [(n Int)] (== (twice n) (+ n n))))

(defn unrelated [] [(n Int)] Int
  (+ n 1)
  (prop succ [(n Int)] (== (unrelated n) (+ n 1))))

(defn quad [] [(n Int)] Int
  (twice (twice n))
  (prop unfolds [(n Int)] (== (quad n) (twice (twice n))))
  (prop is-four-n [(n Int)] (== (quad n) (* 4 n))))
"#;

    struct Fix {
        store: Store,
        twice: String,
        unrelated: String,
        quad: String,
        proven: BTreeSet<(String, usize)>,
    }

    fn fixture() -> Fix {
        let store = elaborate_corpus(&[("t.oath".to_string(), SRC.to_string())])
            .expect("test corpus elaborates");
        let h = |n: &str| store.func_by_name.get(n).expect("defined").hash.clone();
        let (twice, unrelated, quad) = (h("twice"), h("unrelated"), h("quad"));
        // Everything proven, so nothing is INERT for the wrong reason.
        let proven: BTreeSet<(String, usize)> = [
            (twice.clone(), 0),
            (unrelated.clone(), 0),
            (quad.clone(), 0),
            (quad.clone(), 1),
        ]
        .into_iter()
        .collect();
        Fix { store, twice, unrelated, quad, proven }
    }

    impl Fix {
        fn prop(&self, hash: &str, pi: usize) -> Prop {
            match self.store.def_by_hash.get(hash) {
                Some(Def::Func { props, .. }) => props[pi].clone(),
                _ => panic!("not a func"),
            }
        }
        fn cands(&self, pi: usize, hints: &Hints) -> Vec<(String, usize, bool)> {
            candidate_lemmas(&self.store, &self.quad, pi, &self.prop(&self.quad, pi), &self.proven, hints)
        }
        fn script(&self, pi: usize, hints: &Hints) -> String {
            let prover = Prover { store: &self.store };
            prover
                .direct_script(&self.quad, &self.prop(&self.quad, pi), &self.cands(pi, hints))
                .expect("goal is inside the provable fragment")
                .0
        }
        fn hint(&self, pi: usize, target: (&str, usize)) -> Hints {
            let mut hs = Hints::new();
            hs.insert((self.quad.clone(), pi), vec![(target.0.to_string(), target.1)]);
            hs
        }
    }

    fn admissible(cands: &[(String, usize, bool)]) -> Vec<(String, usize)> {
        cands.iter().filter(|c| c.2).map(|c| (c.0.clone(), c.1)).collect()
    }

    /// Baseline: with no hints, the in-closure lemma is admitted, the sibling is
    /// admitted, the goal's OWN property is not, and the unrelated definition is
    /// not even a candidate. Everything below is measured against this.
    #[test]
    fn baseline_lemma_set() {
        let f = fixture();
        let c = f.cands(1, &Hints::new());
        let mut want = vec![(f.quad.clone(), 0), (f.twice.clone(), 0)];
        want.sort();
        assert_eq!(
            admissible(&c),
            want,
            "sibling + in-footprint dependency are the admitted lemmas"
        );
        assert!(!c.iter().any(|x| x.0 == f.unrelated), "out-of-closure def is no candidate");
        assert!(
            c.iter().any(|x| x.0 == f.quad && x.1 == 1 && !x.2),
            "the goal's own property is a candidate but NOT admissible"
        );
    }

    /// §7.2 "a hinted lemma MAY belong to a definition outside the goal's
    /// footprint (and outside the dependency closure)": the hint must ADD it.
    /// This is the feature working at all — the other tests bound it.
    #[test]
    fn hint_admits_out_of_footprint_lemma() {
        let f = fixture();
        let base = f.cands(1, &Hints::new());
        let hinted = f.cands(1, &f.hint(1, (&f.unrelated, 0)));
        assert_eq!(hinted.len(), base.len() + 1, "exactly one candidate added");
        assert!(
            hinted.iter().any(|x| x.0 == f.unrelated && x.1 == 0 && x.2),
            "the hinted out-of-footprint lemma is admitted"
        );
        // Canonical (defHash, propIdx) order is preserved, additively.
        let mut sorted = hinted.clone();
        sorted.sort_by(|a, b| (a.0.as_str(), a.1).cmp(&(b.0.as_str(), b.1)));
        assert_eq!(hinted, sorted);
        assert_ne!(f.script(1, &Hints::new()), f.script(1, &f.hint(1, (&f.unrelated, 0))));
    }

    /// §7.2 MUST — SET UNION, NOT CONCATENATION. `twice` prop 0 is already an
    /// admissible candidate; hinting it must be a total no-op, down to the bytes.
    /// Under a concatenating implementation the lemma would be asserted twice.
    #[test]
    fn redundant_hint_on_admissible_dependency_is_a_noop() {
        let f = fixture();
        assert_eq!(f.cands(1, &Hints::new()), f.cands(1, &f.hint(1, (&f.twice, 0))));
        assert_eq!(f.script(1, &Hints::new()), f.script(1, &f.hint(1, (&f.twice, 0))));
    }

    /// §7.2 MUST — same union rule for a SIBLING hint (a property of the
    /// definition under proof other than the goal): siblings are admissible
    /// unconditionally, so the hint changes nothing.
    #[test]
    fn redundant_hint_on_sibling_is_a_noop() {
        let f = fixture();
        assert_eq!(f.cands(1, &Hints::new()), f.cands(1, &f.hint(1, (&f.quad, 0))));
        assert_eq!(f.script(1, &Hints::new()), f.script(1, &f.hint(1, (&f.quad, 0))));
    }

    /// §7.2 MUST — SOUNDNESS: a property is never its own lemma, and the
    /// exclusion is applied BEFORE hints. A hint naming the goal itself must be
    /// discarded even though its target is recorded proven.
    ///
    /// This test FAILS if the guard in `candidate_lemmas` is removed: without it
    /// the hint flips the goal's own candidate to admissible and the goal is
    /// asserted as its own axiom (`(assert (forall ((q0 Int)) (= (fn_quad_Int q0)
    /// (* 4 q0))))` appears in the script), which would let anything "prove".
    #[test]
    fn self_hint_at_the_goal_is_discarded() {
        let f = fixture();
        let hinted = f.cands(1, &f.hint(1, (&f.quad, 1)));
        assert_eq!(hinted, f.cands(1, &Hints::new()), "candidate set unchanged");
        assert!(
            hinted.iter().any(|x| x.0 == f.quad && x.1 == 1 && !x.2),
            "the goal's own property stays INADMISSIBLE despite the hint"
        );
        let base = f.script(1, &Hints::new());
        assert_eq!(base, f.script(1, &f.hint(1, (&f.quad, 1))), "script bytes unchanged");
        // The equality above is not vacuous: force-admitting the goal's own
        // property (what an unguarded self-hint would do) DOES change the bytes —
        // the goal's formula appears as a positive lemma assert. So the test
        // genuinely fails if the guard is removed.
        let mut unsound = f.cands(1, &Hints::new());
        for c in unsound.iter_mut() {
            if c.0 == f.quad && c.1 == 1 {
                c.2 = true;
            }
        }
        let prover = Prover { store: &f.store };
        let unsound_script = prover
            .direct_script(&f.quad, &f.prop(&f.quad, 1), &unsound)
            .expect("translates")
            .0;
        assert_ne!(base, unsound_script, "admitting the self-lemma would change the script");
        assert_eq!(
            unsound_script.matches("(assert").count(),
            base.matches("(assert").count() + 1,
            "the extra assert is exactly the goal asserted as its own axiom"
        );
    }

    /// §7.2: a hint whose target is not currently proven is INERT.
    #[test]
    fn hint_at_an_unproven_target_is_inert() {
        let f = fixture();
        let mut thin = f.proven.clone();
        thin.remove(&(f.unrelated.clone(), 0));
        let hints = f.hint(1, (&f.unrelated, 0));
        let base = candidate_lemmas(&f.store, &f.quad, 1, &f.prop(&f.quad, 1), &thin, &Hints::new());
        let hinted = candidate_lemmas(&f.store, &f.quad, 1, &f.prop(&f.quad, 1), &thin, &hints);
        assert_eq!(base, hinted, "an unproven hint target is never admitted");
    }

    /// §7.2: "Hints apply ONLY to the property they are attached to" — a hint on
    /// prop 1 must not touch prop 0's script.
    #[test]
    fn hints_do_not_leak_to_sibling_properties() {
        let f = fixture();
        let hints = f.hint(1, (&f.unrelated, 0));
        assert_eq!(f.cands(0, &Hints::new()), f.cands(0, &hints));
        assert_eq!(f.script(0, &Hints::new()), f.script(0, &hints));
    }

    // =======================================================================
    // #72 — PER-PROPERTY attempt validity (SPEC §7.2 "Attempt validity").
    //
    // These are invisible to the byte oracle: the rule governs verdict
    // RECORDING under environmental aborts, and every fixture was generated
    // from a clean run, so no fixture exercises it. They are also unreachable
    // through timing without being flaky. So the abort is injected
    // DETERMINISTICALLY: `prove_all_with` takes the per-property attempt as a
    // parameter, and these tests pass an oracle that returns
    // `PropVerdict::Aborted` for a designated property — no z3, no clock, same
    // answer every run. Everything below the injection point (the round loop,
    // the growth fixpoint, the carry-forward, the reporting) is the real code.
    // =======================================================================

    /// The oracle: `abort` names properties whose attempts are environmental
    /// aborts; every other property proves. `after` delays aborting until the
    /// property has been attempted that many times (so a property can be PROVEN
    /// in round 0 and abort in round 1 — the demotion case). Records every
    /// attempt's candidate set for the no-new-lemma check.
    struct Oracle {
        abort: Vec<(String, usize)>,
        after: usize,
        until: usize,
        attempts: std::cell::RefCell<Vec<((String, usize), Vec<(String, usize, bool)>)>>,
    }

    impl Oracle {
        fn new(abort: Vec<(String, usize)>, after: usize) -> Self {
            Oracle { abort, after, until: usize::MAX, attempts: Default::default() }
        }
        /// Abort only the attempts numbered `after..until` for the named
        /// properties; every other attempt proves.
        fn window(abort: Vec<(String, usize)>, after: usize, until: usize) -> Self {
            Oracle { abort, after, until, attempts: Default::default() }
        }
        fn verdict(&self, hash: &str, pi: usize, cands: &[(String, usize, bool)]) -> PropVerdict {
            let key = (hash.to_string(), pi);
            let seen = self.attempts.borrow().iter().filter(|(k, _)| *k == key).count();
            self.attempts.borrow_mut().push((key.clone(), cands.to_vec()));
            if self.abort.contains(&key) && seen >= self.after && seen < self.until {
                PropVerdict::Aborted("simulated environmental abort (wall cap)".to_string())
            } else {
                PropVerdict::Proven
            }
        }
        /// Every candidate set any attempt was given, flattened.
        fn all_candidates(&self) -> Vec<(String, usize, bool)> {
            self.attempts.borrow().iter().flat_map(|(_, c)| c.clone()).collect()
        }
    }

    fn prove_with(f: &Fix, o: &Oracle) -> BTreeMap<String, ProofResult> {
        prove_all_with(&f.store, &BTreeSet::new(), &Hints::new(), |h, pi, _p, c| {
            o.verdict(h, pi, c)
        })
    }

    /// §7.2 #72 composition, the soundness direction: a property NOT proven while
    /// an attempt was invalid is ABORTED, never UNPROVEN — "no valid verdict
    /// exists" and "attempted validly, not proven" are different claims. A valid
    /// proof still wins over a taint (`unsat` is evidence no environment fakes).
    #[test]
    fn a_tainted_negative_is_aborted_never_unproven() {
        let taint = || Some("z3 exceeded the wall cap".to_string());
        assert_eq!(
            compose_verdict(false, taint()),
            PropVerdict::Aborted("z3 exceeded the wall cap".to_string())
        );
        assert_ne!(compose_verdict(false, taint()), PropVerdict::Unproven);
        assert_eq!(compose_verdict(false, None), PropVerdict::Unproven);
        // A valid proof is unaffected by another attempt's invalidity.
        assert_eq!(compose_verdict(true, taint()), PropVerdict::Proven);
        assert_eq!(compose_verdict(true, None), PropVerdict::Proven);
    }

    /// §7.2 #72: "Sibling properties are unaffected ... the run as a whole
    /// SUCCEEDS with partial results". One aborted property must not prevent any
    /// other property — of its own definition or of any other — from recording.
    /// (Before #72 this invalidated the ENTIRE run and recorded nothing.)
    #[test]
    fn an_aborted_property_does_not_block_its_siblings() {
        let f = fixture();
        let o = Oracle::new(vec![(f.quad.clone(), 1)], 0);
        let r = prove_with(&f, &o);

        let quad = &r[&f.quad];
        assert_eq!(quad.proven, vec![true, false], "the sibling records its proof");
        assert_eq!(quad.aborted, vec![false, true], "only prop 1 aborted");
        // Other definitions record normally: the run did not end.
        assert_eq!(r[&f.twice].proven, vec![true]);
        assert_eq!(r[&f.unrelated].proven, vec![true]);
        assert!(r.values().all(|p| p.aborted.iter().filter(|a| **a).count() <= 1));
    }

    /// §7.2 #72: an aborted property "is reported DISTINCTLY as aborted, never as
    /// unproven". The recorded state must therefore keep the two apart — an
    /// aborted property is NOT merely `proven == false`, which is the positive
    /// claim that it was attempted validly and failed.
    #[test]
    fn an_aborted_property_is_not_recorded_as_unproven() {
        let f = fixture();
        // quad prop 0 is a genuine, validly-attempted non-proof; quad prop 1
        // aborts. Both end up NOT recorded — the result must still tell them
        // apart, since only one of them supports the claim "not proven".
        let r = prove_all_with(&f.store, &BTreeSet::new(), &Hints::new(), |h, pi, _p, _c| {
            if h == f.quad && pi == 0 {
                PropVerdict::Unproven
            } else if h == f.quad && pi == 1 {
                PropVerdict::Aborted("simulated environmental abort".to_string())
            } else {
                PropVerdict::Proven
            }
        });
        let quad = &r[&f.quad];
        assert_eq!(quad.proven, vec![false, false], "neither is recorded proven");
        assert_eq!(
            quad.aborted,
            vec![false, true],
            "the aborted property is flagged distinctly from the honest unproven"
        );
    }

    /// §7.2 #72, the soundness core, and the EXACT case on which the two possible
    /// readings of "standing verdict" disagree — now pinned normatively: "the
    /// proven set the kernel holds for the object AT THE START OF THE CURRENT
    /// ROUND … INCLUDING proofs this run established in an earlier round. It is
    /// NOT a snapshot taken before the run: a property proven in round 0 and
    /// aborted in round 1 keeps its proof."
    ///
    /// The oracle proves every property in round 0 and aborts quad prop 1 on
    /// every later attempt (round 1 re-attempts it under the now-larger recorded
    /// lemma state, so the abort really is reached). Under the pre-run-snapshot
    /// reading the cold run's snapshot is EMPTY and prop 1 would silently lose its
    /// round-0 proof; under the pinned reading it keeps it. This test fails on the
    /// former and passes on the latter — it is the whole difference between the
    /// two, and it is invisible to the byte oracle.
    #[test]
    fn a_previously_proven_property_is_never_demoted_by_an_abort() {
        let f = fixture();
        let o = Oracle::new(vec![(f.quad.clone(), 1)], 1);
        let r = prove_with(&f, &o);
        let quad = &r[&f.quad];
        assert!(
            o.attempts.borrow().iter().filter(|(k, _)| *k == (f.quad.clone(), 1)).count() >= 2,
            "the test is vacuous unless the property was re-attempted after being proven"
        );
        assert_eq!(quad.proven, vec![true, true], "the prior proof is carried forward");
        assert_eq!(
            quad.aborted,
            vec![false, true],
            "the abort is still reported, on top of the standing proof"
        );
    }

    /// §7.2 #72: "An aborted property contributes no new lemma (it gains nothing
    /// this run)". `twice`'s only property aborts from the first attempt, so it is
    /// never recorded — and must never appear in any other goal's candidate set,
    /// admissible or not.
    #[test]
    fn an_aborted_property_contributes_no_new_lemma() {
        let f = fixture();
        let o = Oracle::new(vec![(f.twice.clone(), 0)], 0);
        let r = prove_with(&f, &o);
        assert_eq!(r[&f.twice].proven, vec![false], "nothing recorded for the aborted property");
        assert_eq!(r[&f.twice].aborted, vec![true]);
        assert!(
            !o.all_candidates().iter().any(|(h, pi, _)| *h == f.twice && *pi == 0),
            "an aborted property is never offered as a lemma to any goal"
        );
        // Its dependents still record their own verdicts (partial results).
        assert_eq!(r[&f.quad].proven, vec![true, true]);
    }

    /// §7.2 #72 (spec amendment): "A carried-forward proof REMAINS admissible as a
    /// lemma: withdrawing it would make sibling verdicts depend on which attempts
    /// happened to abort, i.e. on the environment. Only a property that has never
    /// been proven contributes no lemma."
    ///
    /// `twice` prop 0 proves in round 0 and aborts from round 1 on. Its standing
    /// proof must keep reaching `quad`'s goals as a lemma in round 1 — the round
    /// in which its own attempt aborted.
    #[test]
    fn a_carried_forward_proof_is_still_admissible_as_a_lemma() {
        let f = fixture();
        let o = Oracle::new(vec![(f.twice.clone(), 0)], 1);
        let r = prove_with(&f, &o);
        assert_eq!(r[&f.twice].proven, vec![true], "the standing proof is carried forward");
        assert_eq!(r[&f.twice].aborted, vec![true]);
        // Attempts are recorded in order; find where round 1 starts (the second
        // attempt of twice prop 0 — the aborted one) and check that quad's goals
        // AFTER it were still offered twice.doubles as an admissible lemma.
        let attempts = o.attempts.borrow();
        let abort_at = attempts
            .iter()
            .enumerate()
            .filter(|(_, (k, _))| *k == (f.twice.clone(), 0))
            .nth(1)
            .expect("twice prop 0 is re-attempted in round 1")
            .0;
        let later_quad_goals: Vec<_> = attempts[abort_at + 1..]
            .iter()
            .filter(|(k, _)| k.0 == f.quad)
            .collect();
        assert!(!later_quad_goals.is_empty(), "quad is attempted after the abort");
        assert!(
            later_quad_goals
                .iter()
                .all(|(_, c)| c.iter().any(|(h, pi, adm)| *h == f.twice && *pi == 0 && *adm)),
            "the carried-forward proof is still an ADMISSIBLE lemma for sibling goals"
        );
    }

    /// §7.2 #72: "aborted" is the claim that NO VALID VERDICT EXISTS. A property
    /// that aborts on one pass of the inner growth loop and then PROVES on a later
    /// pass of the same round has a valid verdict, so the earlier abort must not
    /// be reported — otherwise the run would announce an abort for a property it
    /// proved outright this run. quad prop 0 aborts on its first attempt only; the
    /// sibling's proof grows its candidate set and it proves on the retry.
    ///
    /// HONEST LIMITATION, mutation-checked: this test does NOT currently
    /// discriminate the `aborted.remove(&key)` that implements the rule — deleting
    /// that line leaves it passing. The per-round `aborted.clear()` masks it: a
    /// supersession makes `in_run` grow, so that round cannot be the settling
    /// round, and the next round's clear wipes the stale entry anyway. The single
    /// path where the entry would survive is exhaustion of the 8-round cap, which
    /// this three-definition fixture cannot reach (the candidate-set cache freezes
    /// each property's verdict once its lemma set stops changing, so the run
    /// converges in a handful of rounds). The `remove` is kept as a defensive
    /// invariant — "reported aborted" must mean "no valid verdict exists" at every
    /// point, not merely at the settling round — and this test pins the observable
    /// half of it: a superseded abort never reaches the recorded state.
    #[test]
    fn an_abort_superseded_by_a_proof_in_the_same_round_is_not_reported() {
        let f = fixture();
        let o = Oracle::window(vec![(f.quad.clone(), 0)], 0, 1);
        let r = prove_with(&f, &o);
        assert!(
            o.attempts.borrow().iter().filter(|(k, _)| *k == (f.quad.clone(), 0)).count() >= 2,
            "the test is vacuous unless the aborted property was re-attempted"
        );
        assert_eq!(r[&f.quad].proven, vec![true, true]);
        assert_eq!(
            r[&f.quad].aborted,
            vec![false, false],
            "an abort superseded by a valid proof is no longer the property's state"
        );
    }

    // =======================================================================
    // SPEC §7.5 — Sharded verification (#98).
    //
    // These test the seeded single-pass verifier and its self-check with a
    // DETERMINISTIC oracle — no z3, no clock — exactly as the #72 abort tests
    // do, and for the same reason: the rules govern verdict RECORDING and the
    // union/self-check, which no fixture generated from a clean run exercises.
    // Everything below the injection point (assignment, the single pass, the
    // carry-forward merge, the loud self-check) is the real code.
    // =======================================================================

    /// An oracle that models LEMMA DEPENDENCE, so the self-check can be exercised
    /// against a seed that is (or is not) run-stable. A property proves iff every
    /// lemma it `requires` is present AND admissible in its candidate set (which
    /// is drawn from the fixed seed S) — remove that lemma from S and the
    /// dependent can no longer prove. A property in `abort` returns an
    /// environmental abort regardless. Everything else proves.
    struct DepOracle {
        requires: BTreeMap<(String, usize), Vec<(String, usize)>>,
        abort: BTreeSet<(String, usize)>,
    }
    impl DepOracle {
        fn new() -> Self {
            DepOracle { requires: BTreeMap::new(), abort: BTreeSet::new() }
        }
        fn require(mut self, goal: (&str, usize), lemma: (&str, usize)) -> Self {
            self.requires
                .entry((goal.0.to_string(), goal.1))
                .or_default()
                .push((lemma.0.to_string(), lemma.1));
            self
        }
        fn aborting(mut self, goal: (&str, usize)) -> Self {
            self.abort.insert((goal.0.to_string(), goal.1));
            self
        }
        fn verdict(&self, hash: &str, pi: usize, cands: &[(String, usize, bool)]) -> PropVerdict {
            let key = (hash.to_string(), pi);
            if self.abort.contains(&key) {
                return PropVerdict::Aborted("simulated environmental abort (wall cap)".to_string());
            }
            if let Some(reqs) = self.requires.get(&key) {
                for (rh, rj) in reqs {
                    let admitted = cands.iter().any(|(h, j, adm)| h == rh && j == rj && *adm);
                    if !admitted {
                        return PropVerdict::Unproven;
                    }
                }
            }
            PropVerdict::Proven
        }
    }

    /// A second, DETERMINISTIC assignment function distinct from the normative
    /// `shard_of` (it reads the TRAILING 16 hex digits, not the leading): the
    /// union over any partition must equal the unsharded seeded result, so a
    /// different partition is a genuine independent check of that invariant.
    fn alt_shard(def_hash: &str, n: u64) -> u64 {
        let tail = &def_hash[def_hash.len() - 16..];
        u64::from_str_radix(tail, 16).unwrap_or(0) % n
    }

    /// Run every shard 0..n with a given assignment function and oracle.
    fn shards_with(
        store: &Store,
        seed: &BTreeSet<(String, usize)>,
        n: u64,
        assign: fn(&str, u64) -> u64,
        o: &DepOracle,
    ) -> Vec<ShardOutcome> {
        let falsified = BTreeSet::new();
        (0..n)
            .map(|i| {
                prove_shard_with(store, &falsified, &Hints::new(), seed, |h| assign(h, n), i, |h, pi, _p, c| {
                    o.verdict(h, pi, c)
                })
            })
            .collect()
    }

    /// The union of shard verdicts, per property (with diagnostics).
    fn union_raw(outs: &[ShardOutcome]) -> BTreeMap<(String, usize), PropVerdict> {
        let mut m = BTreeMap::new();
        for o in outs {
            for (k, v) in &o.verdicts {
                m.insert(k.clone(), v.clone());
            }
        }
        m
    }

    /// A mixed oracle producing all three verdict kinds against the full seed, so
    /// a per-property equality is not vacuously over one verdict: quad prop 0
    /// requires the (admissible) `twice` lemma → PROVEN; `unrelated` aborts →
    /// ABORTED; quad prop 1 requires `unrelated` prop 0, which is never a
    /// candidate for a quad goal → UNPROVEN.
    fn mixed_oracle(f: &Fix) -> DepOracle {
        DepOracle::new()
            .require((&f.quad, 0), (&f.twice, 0))
            .require((&f.quad, 1), (&f.unrelated, 0))
            .aborting((&f.unrelated, 0))
    }

    /// §7.5 test 1 — assignment is DETERMINISTIC and PARTITIONS every definition
    /// exactly once across shards, for several n. `shard_of` reads only the
    /// identity hash, so it cannot depend on file position or elaboration order.
    #[test]
    fn assignment_is_deterministic_and_partitions_every_definition() {
        let f = fixture();
        let defs: Vec<String> = shardable_defs(&f.store, &BTreeSet::new()).into_iter().collect();
        assert_eq!(defs.len(), 3, "twice, unrelated, quad are the shardable defs");
        for n in 1..=6u64 {
            for h in &defs {
                // Deterministic: same input, same output, every call.
                assert_eq!(shard_of(h, n), shard_of(h, n));
                // In range 0..n.
                assert!(shard_of(h, n) < n);
            }
            // Partition: run all shards and confirm each def is attempted by
            // exactly one, and every def is covered.
            let seed: BTreeSet<(String, usize)> = f.proven.clone();
            let outs = shards_with(&f.store, &seed, n, shard_of, &DepOracle::new());
            let mut owner_count: BTreeMap<&String, usize> = defs.iter().map(|d| (d, 0)).collect();
            for o in &outs {
                for d in &o.defs {
                    *owner_count.get_mut(d).unwrap() += 1;
                }
            }
            assert!(
                owner_count.values().all(|&c| c == 1),
                "n={}: every definition is attempted by exactly one shard, got {:?}",
                n,
                owner_count
            );
            let covered: BTreeSet<String> = outs.iter().flat_map(|o| o.defs.iter().cloned()).collect();
            assert_eq!(
                covered,
                defs.iter().cloned().collect::<BTreeSet<_>>(),
                "n={}: the shards cover every definition",
                n
            );
        }
    }

    /// §7.5 test 2 — `n = 1` sharded equals the unsharded seeded verifier, per
    /// property including diagnostics. The two are computed by DIFFERENT code
    /// paths (`prove_shard_with` vs the independent `seeded_verify_all`), so the
    /// equality is a real cross-check, not a tautology. The mixed oracle makes it
    /// non-vacuous: PROVEN, UNPROVEN and ABORTED all appear.
    #[test]
    fn n1_sharded_equals_the_unsharded_seeded_verifier() {
        let f = fixture();
        let o = mixed_oracle(&f);
        let seed = f.proven.clone();
        let seeded = seeded_verify_all(&f.store, &BTreeSet::new(), &Hints::new(), &seed, |h, pi, _p, c| {
            o.verdict(h, pi, c)
        });
        let shard_union = union_raw(&shards_with(&f.store, &seed, 1, shard_of, &o));
        assert_eq!(shard_union, seeded, "n=1 union equals the unsharded seeded verdicts, diagnostics included");
        // Sanity: the mixed oracle really did produce all three kinds.
        let kinds: BTreeSet<&str> = seeded
            .values()
            .map(|v| match v {
                PropVerdict::Proven => "p",
                PropVerdict::Unproven => "u",
                PropVerdict::Aborted(_) => "a",
            })
            .collect();
        assert_eq!(kinds.len(), 3, "the fixture exercises all three verdict kinds");
    }

    /// §7.5 test 3 — the union over several n AND a second assignment function
    /// equals the seeded unsharded result, per property including diagnostics.
    /// This is the acceptance test's core invariant: HOW the definitions are
    /// partitioned never changes what F(S) computes.
    #[test]
    fn union_over_n_and_a_second_assignment_equals_the_seeded_result() {
        let f = fixture();
        let o = mixed_oracle(&f);
        let seed = f.proven.clone();
        let seeded = seeded_verify_all(&f.store, &BTreeSet::new(), &Hints::new(), &seed, |h, pi, _p, c| {
            o.verdict(h, pi, c)
        });
        for n in [1u64, 2, 3] {
            let by_shard_of = union_raw(&shards_with(&f.store, &seed, n, shard_of, &o));
            assert_eq!(by_shard_of, seeded, "n={} (shard_of) union equals the seeded result", n);
            let by_alt = union_raw(&shards_with(&f.store, &seed, n, alt_shard, &o));
            assert_eq!(by_alt, seeded, "n={} (second assignment) union equals the seeded result", n);
        }
    }

    /// §7.5 test 5 — a NON-self-consistent seed makes the self-check FAIL LOUDLY.
    /// `quad` prop 0 depends on `twice` prop 0 for its proof; removing `twice`
    /// prop 0 from S leaves an S that is not run-stable. A verifier that ASSUMED
    /// S rather than recomputing F(S) would pass this; the self-check must not.
    /// The control (full S) passes with the same oracle, proving the failure is
    /// the seed's, not the harness's.
    #[test]
    fn a_non_self_consistent_seed_fails_the_self_check_loudly() {
        let f = fixture();
        let o = DepOracle::new().require((&f.quad, 0), (&f.twice, 0));
        // Control: the full seed IS run-stable under this oracle.
        let full = f.proven.clone();
        let ok_report =
            verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &full, 3, |h, pi, _p, c| o.verdict(h, pi, c));
        assert!(ok_report.ok(), "the full seed is self-consistent: {:?}", ok_report.mismatches);
        assert_eq!(ok_report.proven, full, "the union equals S");

        // Remove the depended-on member. Now `quad` prop 0 cannot re-derive.
        let mut broken = f.proven.clone();
        broken.remove(&(f.twice.clone(), 0));
        let bad =
            verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &broken, 3, |h, pi, _p, c| o.verdict(h, pi, c));
        assert!(!bad.ok(), "a non-run-stable seed must FAIL the self-check");
        assert!(
            bad.mismatches.iter().any(|m| m.contains("quad") && m.contains("UNPROVEN")),
            "the dependent's failure to re-derive is reported loudly: {:?}",
            bad.mismatches
        );
        assert!(
            bad.mismatches.iter().any(|m| m.contains("twice") && m.contains("NOT in the seed")),
            "and the removed member re-proving itself is flagged too: {:?}",
            bad.mismatches
        );
    }

    /// §7.5 test 6 — an environmental abort on an S-member is CARRIED FORWARD and
    /// is NOT a mismatch (mirrors §7.2's run-stability carry-forward). `unrelated`
    /// prop 0 is in S and aborts this run; the union must still equal S, the run
    /// must pass, and the property must be reported as carried, never as a
    /// verdict mismatch.
    #[test]
    fn an_environmental_abort_on_an_s_member_carries_forward() {
        let f = fixture();
        let o = DepOracle::new()
            .require((&f.quad, 0), (&f.twice, 0))
            .aborting((&f.unrelated, 0));
        let seed = f.proven.clone();
        let report =
            verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &seed, 2, |h, pi, _p, c| o.verdict(h, pi, c));
        assert!(report.ok(), "an abort on an S-member is not a mismatch: {:?}", report.mismatches);
        assert_eq!(report.proven, seed, "the carried-forward member keeps its place in the union");
        assert!(
            report.carried.contains_key(&(f.unrelated.clone(), 0)),
            "the aborted S-member is reported as carried forward"
        );
        assert!(
            !report.mismatches.iter().any(|m| m.contains("unrelated")),
            "the carried-forward member is never reported as a mismatch: {:?}",
            report.mismatches
        );
    }

    /// §7.5 partition self-check — a definition attempted by MORE THAN ONE shard
    /// is caught. (The complement, attempted by NO shard, is caught by the same
    /// code path; both are the load-bearing partition guard.) Built by DUPLICATING
    /// a canonical outcome under its own (valid) shard label, so a definition is
    /// validly contributed by two distinct outcomes — both pass the canonical
    /// assignment check, and the partition count is 2.
    #[test]
    fn a_definition_attempted_by_two_shards_is_caught() {
        let f = fixture();
        let seed = f.proven.clone();
        let o = DepOracle::new();
        let outs = shards_with(&f.store, &seed, 2, shard_of, &o);
        let victim = outs
            .iter()
            .find(|out| !out.defs.is_empty())
            .expect("some shard owns a definition")
            .clone();
        let mut all = outs.clone();
        all.push(victim); // a second outcome wearing the same, valid shard label
        let report = merge_and_check(&f.store, &BTreeSet::new(), &seed, &all, 2);
        assert!(!report.ok(), "a def attempted by two shards must fail the partition check");
        assert!(
            report.mismatches.iter().any(|m| m.contains("PARTITION") && m.contains("must be exactly one")),
            "the double-attempt is reported: {:?}",
            report.mismatches
        );
    }

    /// §7.5 — the seed's identity is a function of S, so mutating S changes the
    /// reported campaign identity (acceptance test 7). Two runs cannot claim the
    /// same verification from different seeds.
    #[test]
    fn mutating_the_seed_changes_the_reported_identity() {
        let f = fixture();
        let full = f.proven.clone();
        let mut reduced = full.clone();
        reduced.remove(&(f.twice.clone(), 0));
        assert_ne!(seed_identity(&full), seed_identity(&reduced));
        // And it is stable for an unchanged seed.
        assert_eq!(seed_identity(&full), seed_identity(&f.proven));
    }

    /// §7.5 completeness [P1] — a seed member OUTSIDE the shard universe must fail
    /// loudly, not be silently ignored. The universe loop alone verifies
    /// `union == S ∩ universe`; a proven seed entry whose definition the run
    /// cannot attempt (absent from the corpus, falsified, non-shardable, or an
    /// out-of-range index) is a genuine `union != S` and must be reported.
    #[test]
    fn seed_members_outside_the_universe_fail_loudly() {
        // (a) A NONEMPTY seed over an EMPTY corpus: the exact concrete regression —
        // it used to report `PASS union == S` with zero proven properties.
        let empty = elaborate_corpus(&[]).expect("empty corpus elaborates to an empty store");
        let mut seed = BTreeSet::new();
        seed.insert(("00".repeat(32), 0)); // a fabricated, absent definition hash
        let r = verify_sharded(&empty, &BTreeSet::new(), &Hints::new(), &seed, 1, |_h, _pi, _p, _c| {
            PropVerdict::Proven
        });
        assert!(!r.ok(), "a nonempty seed over an empty corpus cannot have verified union == S");
        assert!(
            r.mismatches.iter().any(|m| m.contains("SEED") && m.contains("absent from the corpus")),
            "the unaccounted seed member is flagged loudly: {:?}",
            r.mismatches
        );
        assert!(r.proven.is_empty(), "and nothing was actually proven");

        // (b) A seed member whose definition is FALSIFIED (so never proved, §7.3)
        // is outside the universe and must fail — the real corpus still runs.
        let f = fixture();
        let falsified: BTreeSet<String> = [f.twice.clone()].into_iter().collect();
        let o = DepOracle::new();
        let r = verify_sharded(&f.store, &falsified, &Hints::new(), &f.proven, 2, |h, pi, _p, c| {
            o.verdict(h, pi, c)
        });
        assert!(!r.ok(), "a seed member for a falsified definition must FAIL");
        assert!(
            r.mismatches.iter().any(|m| m.contains("twice") && m.contains("FALSIFIED")),
            "the falsified seed member is flagged: {:?}",
            r.mismatches
        );

        // (c) An ABSENT definition in an otherwise-valid, otherwise-passing run.
        let mut seed = f.proven.clone();
        seed.insert(("11".repeat(32), 0));
        let r = verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &seed, 3, |h, pi, _p, c| {
            o.verdict(h, pi, c)
        });
        assert!(!r.ok(), "an absent seed member must FAIL even when every real member checks out");
        assert!(
            r.mismatches.iter().any(|m| m.contains("absent from the corpus")),
            "the absent member is flagged: {:?}",
            r.mismatches
        );

        // (d) A malformed seed naming a property index the definition lacks
        // (`unrelated` has exactly one property).
        let mut seed = f.proven.clone();
        seed.insert((f.unrelated.clone(), 5));
        let r = verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &seed, 1, |h, pi, _p, c| {
            o.verdict(h, pi, c)
        });
        assert!(!r.ok(), "an out-of-range seed index must FAIL");
        assert!(
            r.mismatches.iter().any(|m| m.contains("unrelated") && m.contains("only 1 propert")),
            "the out-of-range index is flagged: {:?}",
            r.mismatches
        );
    }

    /// §7.5 completeness [P2] — a missing verdict for an OWNED (assigned)
    /// property must fail even when that property is NOT in S. §7.5 requires every
    /// assigned property attempted exactly once; a truncated/malformed shard
    /// outcome that drops a property verdict left it un-attempted, which a PASS
    /// must never hide — regardless of seed membership.
    #[test]
    fn a_missing_verdict_for_an_owned_property_fails_even_when_not_in_s() {
        let f = fixture();
        // Put `quad` prop 1 OUTSIDE the seed and make it (validly) UNPROVEN, so a
        // complete run PASSES — quad1 unproven and not in S is no mismatch. This
        // isolates the P2 fix: only the MISSING verdict can flip the result.
        let mut seed = f.proven.clone();
        seed.remove(&(f.quad.clone(), 1));
        let o = DepOracle::new().require((&f.quad, 1), (&f.unrelated, 0)); // quad1 -> Unproven
        let outs = shards_with(&f.store, &seed, 2, shard_of, &o);

        // Control: with every assigned property attempted, the run passes.
        let control = merge_and_check(&f.store, &BTreeSet::new(), &seed, &outs, 2);
        assert!(control.ok(), "control passes: quad1 is unproven and not in S: {:?}", control.mismatches);
        assert!(!seed.contains(&(f.quad.clone(), 1)), "quad1 is deliberately NOT in the seed");

        // Truncate the owning shard's outcome: drop quad prop 1's verdict.
        let mut truncated = outs.clone();
        for out in truncated.iter_mut() {
            out.verdicts.remove(&(f.quad.clone(), 1));
        }
        let r = merge_and_check(&f.store, &BTreeSet::new(), &seed, &truncated, 2);
        assert!(
            !r.ok(),
            "a truncated shard outcome must FAIL — an assigned property went un-attempted"
        );
        assert!(
            r.mismatches.iter().any(|m| m.contains("quad") && m.contains("NOT attempted")),
            "the un-attempted assigned property is flagged even though it is not in S: {:?}",
            r.mismatches
        );
    }

    /// §7.5 provenance — `merge_and_check` treats its outcomes as UNTRUSTED
    /// (the external-merge path). An outcome may only contribute verdicts for the
    /// definitions it CLAIMS to own; a verdict smuggled in for a definition another
    /// outcome legitimately owns must FAIL the self-check, not slip through with an
    /// ownership count of exactly one. Without the provenance guard the foreign
    /// verdict would be unioned and the partition check would read a false PASS.
    #[test]
    fn a_foreign_verdict_from_an_unowning_outcome_fails_the_merge() {
        let f = fixture();
        let seed = f.proven.clone();
        let o = DepOracle::new();
        // Legitimate split: shard A (i=0) owns whatever `shard_of` assigns to 0,
        // shard B (i=1) the rest. Whichever owns `quad`, we make the OTHER outcome
        // smuggle a verdict for `quad` — an ownership it never lists in `defs`.
        let outs = shards_with(&f.store, &seed, 2, shard_of, &o);
        // Control: the honest merge passes.
        let control = merge_and_check(&f.store, &BTreeSet::new(), &seed, &outs, 2);
        assert!(control.ok(), "the honest merge passes: {:?}", control.mismatches);

        // Find the outcome that does NOT own `quad`, and smuggle a verdict for
        // `quad` prop 0 into it (a verdict from the wrong shard).
        let quad_key = (f.quad.clone(), 0usize);
        let mut tampered = outs.clone();
        let non_owner = tampered
            .iter_mut()
            .find(|out| !out.defs.contains(&f.quad))
            .expect("with 2 shards some outcome does not own quad");
        non_owner.verdicts.insert(quad_key.clone(), PropVerdict::Proven);
        assert!(
            !non_owner.defs.contains(&f.quad),
            "the smuggling outcome does NOT list quad in its defs — that is the point"
        );

        let r = merge_and_check(&f.store, &BTreeSet::new(), &seed, &tampered, 2);
        assert!(!r.ok(), "a foreign verdict for an unowned definition must FAIL the merge");
        assert!(
            r.mismatches.iter().any(|m| m.contains("PROVENANCE") && m.contains("quad") && m.contains("canonically assigned")),
            "the foreign verdict is rejected with a provenance mismatch: {:?}",
            r.mismatches
        );
    }

    /// §7.5 provenance vector (a) — an OUT-OF-RANGE property index in an OWNED
    /// outcome fails. `quad` has exactly two properties; a verdict for `quad`
    /// prop 5 is inserted into the outcome that legitimately owns `quad` (so the
    /// canonical-assignment check passes and ONLY the range check can fire),
    /// validated against `property_count` in the store rather than trusted.
    #[test]
    fn an_out_of_range_property_index_in_an_owned_outcome_fails() {
        let f = fixture();
        let seed = f.proven.clone();
        let o = DepOracle::new();
        let mut outs = shards_with(&f.store, &seed, 2, shard_of, &o);
        let owner = outs
            .iter_mut()
            .find(|out| out.defs.contains(&f.quad))
            .expect("some outcome owns quad");
        owner.verdicts.insert((f.quad.clone(), 5), PropVerdict::Proven);
        let r = merge_and_check(&f.store, &BTreeSet::new(), &seed, &outs, 2);
        assert!(!r.ok(), "an out-of-range property index must FAIL the merge");
        assert!(
            r.mismatches.iter().any(|m| m.contains("PROVENANCE") && m.contains("quad") && m.contains("out of range")),
            "the out-of-range index is rejected against the store's property count: {:?}",
            r.mismatches
        );
    }

    /// §7.5 provenance vector (b) — an outcome whose declared `shard` does not
    /// equal `shard_of(def, n)` for a definition it lists fails. A canonical
    /// outcome's label is flipped to the wrong index; every def it carries now
    /// hashes to its OLD shard, not the new label, so each verdict fails the
    /// canonical-assignment check. The truth comes from `shard_of`, never from the
    /// outcome's self-reported `shard`.
    #[test]
    fn an_outcome_with_a_mislabelled_shard_fails() {
        let f = fixture();
        let seed = f.proven.clone();
        let o = DepOracle::new();
        let mut outs = shards_with(&f.store, &seed, 2, shard_of, &o);
        let victim = outs
            .iter_mut()
            .find(|out| !out.defs.is_empty())
            .expect("some shard owns a definition");
        victim.shard = 1 - victim.shard; // n = 2: flip the label to the wrong shard
        let r = merge_and_check(&f.store, &BTreeSet::new(), &seed, &outs, 2);
        assert!(!r.ok(), "a mislabelled shard must FAIL the merge");
        assert!(
            r.mismatches.iter().any(|m| m.contains("PROVENANCE") && m.contains("canonically assigned")),
            "the relabelled shard's verdicts are rejected against shard_of: {:?}",
            r.mismatches
        );
    }

    /// §7.5 wire format — `format_shard_emission` and `parse_shard_emission` are
    /// exact inverses: every shard's emission round-trips to a `ShardOutcome`
    /// equal to the original, carrying `i`, `n`, and the campaign identity. The
    /// emission is keyed by definition HASH and includes an ABORT reason string.
    #[test]
    fn shard_emission_round_trips() {
        let f = fixture();
        let seed = f.proven.clone();
        let cid = campaign_identity(&seed, &Hints::new(), "Z3 version 4.16.0 - 64 bit", 400_000_000);
        // An oracle producing all three verdict kinds (so the round-trip covers
        // proven, unproven, AND an aborted reason string with spaces).
        let o = mixed_oracle(&f);
        let n = 3u64;
        for i in 0..n {
            let out = prove_shard_with(&f.store, &BTreeSet::new(), &Hints::new(), &seed, |h| shard_of(h, n), i, |h, pi, _p, c| {
                o.verdict(h, pi, c)
            });
            let text = format_shard_emission(&out, n, &cid);
            let parsed = parse_shard_emission(&text).expect("emission parses");
            assert_eq!(parsed.i, i, "shard index round-trips");
            assert_eq!(parsed.n, n, "shard count round-trips");
            assert_eq!(parsed.campaign_id, cid, "campaign identity round-trips");
            assert_eq!(parsed.outcome, out, "the ShardOutcome round-trips exactly (defs + verdicts)");
        }
        // The banner names the emission as a contribution, not a verified result.
        let out0 = prove_shard_with(&f.store, &BTreeSet::new(), &Hints::new(), &seed, |h| shard_of(h, n), 0, |h, pi, _p, c| o.verdict(h, pi, c));
        let text = format_shard_emission(&out0, n, &cid);
        assert!(
            text.lines().next().unwrap().starts_with('#') && text.contains("CONTRIBUTION ONLY"),
            "the emission states it is a contribution, not verified: {}",
            text.lines().next().unwrap()
        );
    }

    /// §7.2/§10.5 — the campaign identity binds the FULL determinism context. Any
    /// change to S, the hints, the solver version, or the rlimit changes it; an
    /// unchanged context reproduces it. This is what makes a merge able to reject
    /// an emission that ran a different `F`.
    #[test]
    fn campaign_identity_binds_s_hints_solver_and_rlimit() {
        let f = fixture();
        let seed = f.proven.clone();
        let hints0 = Hints::new();
        let base = campaign_identity(&seed, &hints0, "Z3 4.16.0", 400_000_000);
        // Stable for an identical context.
        assert_eq!(base, campaign_identity(&seed, &hints0, "Z3 4.16.0", 400_000_000));
        // Different S.
        let mut seed2 = seed.clone();
        seed2.remove(&(f.twice.clone(), 0));
        assert_ne!(base, campaign_identity(&seed2, &hints0, "Z3 4.16.0", 400_000_000), "S changes the id");
        // Different hints.
        let mut hints1 = Hints::new();
        hints1.insert((f.quad.clone(), 1), vec![(f.unrelated.clone(), 0)]);
        assert_ne!(base, campaign_identity(&seed, &hints1, "Z3 4.16.0", 400_000_000), "hints change the id");
        // Different solver version.
        assert_ne!(base, campaign_identity(&seed, &hints0, "Z3 4.15.0", 400_000_000), "solver version changes the id");
        // Different rlimit.
        assert_ne!(base, campaign_identity(&seed, &hints0, "Z3 4.16.0", 4_000_000), "rlimit changes the id");
    }

    /// §7.5 — a shard's emission fed through the wire format and merged equals the
    /// in-process `verify_sharded` result. This is the ROUND-TRIP acceptance at the
    /// library level: emit every shard, parse each back, `merge_and_check` the
    /// reconstructed outcomes, and assert the same proven set and PASS as running
    /// all shards in-process.
    #[test]
    fn emit_parse_merge_equals_in_process_verify() {
        let f = fixture();
        let seed = f.proven.clone();
        // A self-consistent oracle: quad prop 0 needs the twice lemma (present in
        // the full seed), everything proves — so both paths PASS.
        let o = DepOracle::new().require((&f.quad, 0), (&f.twice, 0));
        let n = 3u64;

        // In-process reference.
        let reference = verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &seed, n, |h, pi, _p, c| {
            o.verdict(h, pi, c)
        });
        assert!(reference.ok(), "the in-process verify passes: {:?}", reference.mismatches);

        // Emit -> parse -> merge.
        let cid = campaign_identity(&seed, &Hints::new(), "Z3 test", 400_000_000);
        let mut reconstructed = Vec::new();
        for i in 0..n {
            let out = prove_shard_with(&f.store, &BTreeSet::new(), &Hints::new(), &seed, |h| shard_of(h, n), i, |h, pi, _p, c| {
                o.verdict(h, pi, c)
            });
            let text = format_shard_emission(&out, n, &cid);
            reconstructed.push(parse_shard_emission(&text).expect("parses").outcome);
        }
        let merged = merge_and_check(&f.store, &BTreeSet::new(), &seed, &reconstructed, n);
        assert!(merged.ok(), "the emit->parse->merge pipeline passes: {:?}", merged.mismatches);
        assert_eq!(merged.proven, reference.proven, "the merged proven set equals the in-process one");
        assert_eq!(merged.seed_id, reference.seed_id, "and reports the same seed identity");
    }

    /// §7.5 issue 3 — a non-hex or MULTIBYTE "hash" in an emission fails to parse
    /// CLEANLY (an error, never a panic). Untrusted hashes are validated as
    /// 64-char lowercase hex before anything stores or slices them, and the error
    /// path builds its prefix with a char-safe helper.
    #[test]
    fn a_malformed_hash_in_an_emission_fails_cleanly() {
        // A multibyte, non-hex "hash" — byte-slicing this at index 8 would panic.
        let text = "shard\t0\t2\ncampaign\tabc\nnötaöreally0hash\t0\tproven\n";
        assert!(parse_shard_emission(text).is_err(), "a multibyte non-hex hash is rejected, not panicked on");
        // Hex but wrong length.
        let short = "shard\t0\t2\ncampaign\tabc\ndeadbeef\t0\tproven\n";
        assert!(parse_shard_emission(short).is_err(), "a too-short hash is rejected");
        // Uppercase hex is not the canonical lowercase identity form.
        let upper = format!("shard\t0\t2\ncampaign\tabc\n{}\t0\tproven\n", "A".repeat(64));
        assert!(parse_shard_emission(&upper).is_err(), "uppercase hex is rejected");
        // A well-formed 64-char lowercase-hex hash parses.
        let good = format!("shard\t0\t2\ncampaign\tabc\n{}\t0\tproven\n", "a".repeat(64));
        assert!(parse_shard_emission(&good).is_ok(), "a well-formed identity hash parses");
    }

    /// §7.5 — `n >= 1`. `verify_sharded` must reject `n = 0` rather than merge an
    /// empty run to a vacuous PASS (a partition into zero shards defines nothing),
    /// keeping the public API consistent with the CLI's own `n >= 1` check.
    #[test]
    fn n_zero_does_not_report_success() {
        let f = fixture();
        let r = verify_sharded(&f.store, &BTreeSet::new(), &Hints::new(), &f.proven, 0, |_h, _pi, _p, _c| {
            PropVerdict::Proven
        });
        assert!(!r.ok(), "n = 0 must NOT report success");
        assert!(
            r.mismatches.iter().any(|m| m.contains("n must be >= 1")),
            "n = 0 is rejected with a clear reason: {:?}",
            r.mismatches
        );
        assert!(r.proven.is_empty(), "nothing is proven under an impossible shard count");
        // The seed identity is still reported, so the failing run names its seed.
        assert_eq!(r.seed_id, seed_identity(&f.proven));
    }

    /// §7.5 — SHARD-INDEX completeness is owned by `merge_and_check` ITSELF, not
    /// the CLI wrapper, so a direct caller cannot get a vacuous PASS from an empty
    /// or incomplete campaign. Called directly, without the CLI.
    #[test]
    fn merge_and_check_owns_shard_index_completeness() {
        let f = fixture();
        let seed = f.proven.clone();
        let o = DepOracle::new();

        // (a) EMPTY outcomes with n >= 1 must FAIL, not vacuously pass.
        let empty = merge_and_check(&f.store, &BTreeSet::new(), &seed, &[], 2);
        assert!(!empty.ok(), "an empty campaign must FAIL, not vacuously pass");
        assert!(
            empty.mismatches.iter().any(|m| m.contains("SHARD-INDEX") && m.contains("missing")),
            "every missing shard index is reported: {:?}",
            empty.mismatches
        );

        // Canonical n=2 outcomes (indices {0,1}) — the complete, valid baseline.
        let outs = shards_with(&f.store, &seed, 2, shard_of, &o);
        assert!(
            merge_and_check(&f.store, &BTreeSet::new(), &seed, &outs, 2).ok(),
            "the complete, valid campaign passes"
        );

        // (b) MISSING an index: drop shard 1.
        let only0: Vec<_> = outs.iter().filter(|out| out.shard == 0).cloned().collect();
        let missing = merge_and_check(&f.store, &BTreeSet::new(), &seed, &only0, 2);
        assert!(!missing.ok(), "a missing shard index must FAIL");
        assert!(
            missing.mismatches.iter().any(|m| m.contains("SHARD-INDEX") && m.contains("shard 1 is missing")),
            "the missing index is named: {:?}",
            missing.mismatches
        );

        // (c) DUPLICATE index: two outcomes both labelled 0.
        let s0 = outs.iter().find(|out| out.shard == 0).expect("shard 0").clone();
        let dup = vec![s0.clone(), s0, outs.iter().find(|out| out.shard == 1).unwrap().clone()];
        let duped = merge_and_check(&f.store, &BTreeSet::new(), &seed, &dup, 2);
        assert!(!duped.ok(), "a duplicate shard index must FAIL");
        assert!(
            duped.mismatches.iter().any(|m| m.contains("SHARD-INDEX") && m.contains("supplied 2 times")),
            "the duplicate index is named: {:?}",
            duped.mismatches
        );

        // (d) OUT-OF-RANGE index: an outcome declaring shard 5 for n=2.
        let mut oor = outs.clone();
        oor[0].shard = 5;
        let ranged = merge_and_check(&f.store, &BTreeSet::new(), &seed, &oor, 2);
        assert!(!ranged.ok(), "an out-of-range shard index must FAIL");
        assert!(
            ranged.mismatches.iter().any(|m| m.contains("SHARD-INDEX") && m.contains("out of range")),
            "the out-of-range index is named: {:?}",
            ranged.mismatches
        );
    }
}
