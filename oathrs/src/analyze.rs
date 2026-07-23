//! Auxiliary analyses (SPEC §6): termination (§6.1, lexicographic descent),
//! confinement (§6.2, closure tracking), mutation (§6.3), and the testing
//! guarantee. Proof-derived fields (`proven`, the tested->proven upgrade) are
//! SMT (§7, stage 3) and are intentionally NOT produced here.

use crate::check::check_def;
use crate::elaborate::Store;
use crate::hash::sha256_hex;
use crate::ir::*;
use crate::verify::{run_prop, PropResult};
use std::collections::HashSet;

const MUT_CASES: u64 = 60;
const MUT_FUEL: i64 = 500_000;

// ===========================================================================
// Termination (§6.1)
// ===========================================================================

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Term5 {
    Structural,
    Nonrecursive,
    // SPEC §6.1.1: a Z3-verified integer ranking function bounds the recursion.
    // A TOTAL verdict alongside structural/nonrecursive.
    Measure,
    Unknown,
}

impl Term5 {
    pub fn total(self) -> bool {
        matches!(self, Term5::Structural | Term5::Nonrecursive | Term5::Measure)
    }
    fn as_str(self) -> &'static str {
        match self {
            Term5::Structural => "structural",
            Term5::Nonrecursive => "nonrecursive",
            Term5::Measure => "measure",
            Term5::Unknown => "unknown",
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum RelKind {
    Eq,
    Lt,
}

// A relation maps parameter-index -> Eq|Lt, stored as a fixed-length vector.
type Relation = Vec<Option<RelKind>>;

fn eq_at(i: usize, n: usize) -> Relation {
    let mut r = vec![None; n];
    r[i] = Some(RelKind::Eq);
    r
}

fn lt_of(r: &Relation) -> Relation {
    r.iter().map(|e| e.map(|_| RelKind::Lt)).collect()
}

fn arg_rel(t: &Term, env: &[Option<Relation>]) -> Option<Relation> {
    match t {
        Term::Var(k) => {
            let idx = env.len().checked_sub(1 + *k as usize)?;
            env[idx].clone()
        }
        _ => None,
    }
}

struct Site {
    spine: Vec<Option<Relation>>,
}

struct TermWalk<'a> {
    store: &'a Store,
    sites: Vec<Site>,
    callees: Vec<String>,
}

impl<'a> TermWalk<'a> {
    fn walk(&mut self, t: &Term, env: &mut Vec<Option<Relation>>, spine: Vec<Option<Relation>>) {
        match t {
            Term::App { a, b } => {
                let mut new_spine = Vec::with_capacity(spine.len() + 1);
                new_spine.push(arg_rel(b, env));
                new_spine.extend(spine.iter().cloned());
                self.walk(a, env, new_spine);
                self.walk(b, env, Vec::new());
            }
            Term::SelfRef { .. } => {
                self.sites.push(Site { spine });
            }
            Term::Ref { hash, .. } => {
                self.callees.push(hash.clone());
            }
            Term::Lam { a, .. } => {
                env.push(None);
                self.walk(a, env, Vec::new());
                env.pop();
            }
            Term::Let { a, b, .. } => {
                self.walk(a, env, Vec::new());
                env.push(None);
                self.walk(b, env, Vec::new());
                env.pop();
            }
            Term::If { a, b, c } => {
                self.walk(a, env, Vec::new());
                self.walk(b, env, Vec::new());
                self.walk(c, env, Vec::new());
            }
            Term::Prim { args, .. } => {
                for a in args {
                    self.walk(a, env, Vec::new());
                }
            }
            Term::Ctor { args, .. } => {
                for a in args {
                    self.walk(a, env, Vec::new());
                }
            }
            Term::Record { args, .. } => {
                for a in args {
                    self.walk(a, env, Vec::new());
                }
            }
            Term::Field { a, .. } => {
                self.walk(a, env, Vec::new());
            }
            Term::Match { hash, a, arms } => {
                self.walk(a, env, Vec::new());
                let scrut_rel = if matches!(**a, Term::Var(_)) {
                    arg_rel(a, env)
                } else {
                    None
                };
                let arities = ctor_arities(self.store, hash);
                for (i, arm) in arms.iter().enumerate() {
                    let k = arities.get(i).copied().unwrap_or(0);
                    let field_rel = scrut_rel.as_ref().map(lt_of);
                    for _ in 0..k {
                        env.push(field_rel.clone());
                    }
                    self.walk(arm, env, Vec::new());
                    for _ in 0..k {
                        env.pop();
                    }
                }
            }
            _ => {}
        }
    }
}

fn ctor_arities(store: &Store, hash: &str) -> Vec<usize> {
    match store.data_by_hash.get(hash) {
        Some(di) => di.ctors.iter().map(|(_, f)| f.len()).collect(),
        None => Vec::new(),
    }
}

fn diag(site: &Site, j: usize) -> Option<RelKind> {
    site.spine.get(j).and_then(|opt| opt.as_ref()).and_then(|rel| rel.get(j).copied().flatten())
}

fn discharges(sites: &[&Site], positions: &[usize]) -> bool {
    if sites.is_empty() {
        return true;
    }
    for (idx, &p) in positions.iter().enumerate() {
        let mut all_ok = true;
        let mut any_lt = false;
        for s in sites {
            match diag(s, p) {
                Some(RelKind::Lt) => any_lt = true,
                Some(RelKind::Eq) => {}
                None => all_ok = false,
            }
        }
        if all_ok && any_lt {
            let remaining: Vec<&Site> = sites
                .iter()
                .copied()
                .filter(|s| diag(s, p) == Some(RelKind::Eq))
                .collect();
            let mut rempos = positions.to_vec();
            rempos.remove(idx);
            if discharges(&remaining, &rempos) {
                return true;
            }
        }
    }
    false
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

pub fn termination(store: &Store, hash: &str, visiting: &mut HashSet<String>) -> Term5 {
    if visiting.contains(hash) {
        return Term5::Unknown; // conservative on cycles
    }
    let def = match store.def_by_hash.get(hash) {
        Some(Def::Func { .. }) => store.def_by_hash.get(hash).unwrap(),
        _ => return Term5::Unknown,
    };
    let (body, n) = match def {
        Def::Func { body, .. } => (body, store.func_by_hash.get(hash).unwrap().param_names.len()),
        _ => unreachable!(),
    };
    let inner = strip_lams(body, n);
    let mut w = TermWalk { store, sites: Vec::new(), callees: Vec::new() };
    let mut env: Vec<Option<Relation>> = (0..n).map(|i| Some(eq_at(i, n))).collect();
    w.walk(inner, &mut env, Vec::new());

    visiting.insert(hash.to_string());
    for c in &w.callees {
        if !termination(store, c, visiting).total() {
            visiting.remove(hash);
            return Term5::Unknown;
        }
    }
    visiting.remove(hash);

    if w.sites.is_empty() {
        return Term5::Nonrecursive;
    }
    let site_refs: Vec<&Site> = w.sites.iter().collect();
    let positions: Vec<usize> = (0..n).collect();
    if discharges(&site_refs, &positions) {
        return Term5::Structural;
    }
    // SPEC §6.1.1: structural descent failed — attempt a Z3-verified integer
    // ranking function. Succeeds only when a candidate integer measure strictly
    // decreases and stays >= 0 at every self-call under the path guards. With no
    // solver host available this conservatively yields `unknown` — the `prove`
    // feature (the Z3 host) is excluded from the wasm build, so the check is
    // cfg-gated and falls back to `unknown` there.
    #[cfg(feature = "prove")]
    {
        if crate::prove::measure_terminates(store, hash) {
            Term5::Measure
        } else {
            Term5::Unknown
        }
    }
    #[cfg(not(feature = "prove"))]
    {
        let _ = (store, hash);
        Term5::Unknown
    }
}

// ===========================================================================
// Confinement (§6.2)
// ===========================================================================

fn param_types(ty: &Ty, n: usize) -> Vec<Ty> {
    let mut out = Vec::new();
    let mut t = ty;
    for _ in 0..n {
        match t {
            Ty::Fun(a, b) => {
                out.push((**a).clone());
                t = b;
            }
            _ => break,
        }
    }
    out
}

fn contains_fun(ty: &Ty) -> bool {
    match ty {
        Ty::Fun(_, _) => true,
        Ty::Data { args, .. } | Ty::Rec { args } | Ty::Record { args, .. } => {
            args.iter().any(contains_fun)
        }
        _ => false,
    }
}

fn result_is_data(ty: &Ty, nargs: usize) -> bool {
    let mut t = ty;
    for _ in 0..nargs {
        match t {
            Ty::Fun(_, cod) => t = cod,
            _ => return false,
        }
    }
    !contains_fun(t)
}

fn field_type<'a>(ty: &'a Ty, field: &str) -> Option<&'a Ty> {
    match ty {
        Ty::Record { names, args } => names.iter().position(|n| n == field).map(|i| &args[i]),
        _ => None,
    }
}

fn unwind(t: &Term) -> (&Term, Vec<&Term>) {
    let mut args = Vec::new();
    let mut cur = t;
    while let Term::App { a, b } = cur {
        args.push(&**b);
        cur = a;
    }
    args.reverse();
    (cur, args)
}

struct ConfWalk<'a> {
    store: &'a Store,
    param_i: usize,
    param_ty: &'a Ty,
}

impl<'a> ConfWalk<'a> {
    // returns false as soon as an escape is detected
    fn walk(&self, t: &Term, target: u32, in_lam: bool) -> bool {
        match t {
            Term::Var(k) => *k != target,
            Term::Int(_) | Term::Bool(_) => true,
            Term::Ref { .. } | Term::SelfRef { .. } => true,
            Term::Lam { a, .. } => self.walk(a, target + 1, true),
            Term::Let { a, b, .. } => {
                self.walk(a, target, in_lam) && self.walk(b, target + 1, in_lam)
            }
            Term::If { a, b, c } => {
                self.walk(a, target, in_lam)
                    && self.walk(b, target, in_lam)
                    && self.walk(c, target, in_lam)
            }
            Term::Prim { args, .. } | Term::Record { args, .. } => {
                args.iter().all(|x| self.walk(x, target, in_lam))
            }
            Term::Ctor { args, .. } => args.iter().all(|x| self.walk(x, target, in_lam)),
            Term::Field { a, .. } => {
                // projection without application is an escape
                if matches!(**a, Term::Var(k) if k == target) {
                    false
                } else {
                    self.walk(a, target, in_lam)
                }
            }
            Term::Match { hash, a, arms } => {
                if !self.walk(a, target, in_lam) {
                    return false;
                }
                let arities = ctor_arities(self.store, hash);
                for (i, arm) in arms.iter().enumerate() {
                    let k = arities.get(i).copied().unwrap_or(0) as u32;
                    if !self.walk(arm, target + k, in_lam) {
                        return false;
                    }
                }
                true
            }
            Term::App { .. } => self.walk_app(t, target, in_lam),
        }
    }

    fn walk_app(&self, t: &Term, target: u32, in_lam: bool) -> bool {
        let (head, args) = unwind(t);
        match head {
            Term::Var(k) if *k == target => {
                // direct application of the capability
                if !in_lam && result_is_data(self.param_ty, args.len()) {
                    args.iter().all(|x| self.walk(x, target, in_lam))
                } else {
                    false
                }
            }
            Term::Field { a, op } if matches!(**a, Term::Var(k) if k == target) => {
                match field_type(self.param_ty, op) {
                    Some(fty) if !in_lam && result_is_data(fty, args.len()) => {
                        args.iter().all(|x| self.walk(x, target, in_lam))
                    }
                    _ => false,
                }
            }
            Term::SelfRef { .. } => {
                for (p, arg) in args.iter().enumerate() {
                    if matches!(arg, Term::Var(k) if *k == target) {
                        if p != self.param_i {
                            return false;
                        }
                    } else if !self.walk(arg, target, in_lam) {
                        return false;
                    }
                }
                true
            }
            Term::Ref { hash, .. } => {
                let cc = confinement_of(self.store, hash);
                for (p, arg) in args.iter().enumerate() {
                    let pos_confined = cc.get(p).map(|s| s == "confined").unwrap_or(false);
                    if matches!(arg, Term::Var(k) if *k == target) {
                        if !pos_confined {
                            return false;
                        }
                    } else if matches!(arg, Term::Lam { .. }) && !in_lam && pos_confined {
                        // blessed closure: walk into body, reset in_lam, shift target
                        if !self.walk_blessed(arg, target) {
                            return false;
                        }
                    } else if !self.walk(arg, target, in_lam) {
                        return false;
                    }
                }
                true
            }
            _ => {
                if !self.walk(head, target, in_lam) {
                    return false;
                }
                args.iter().all(|x| self.walk(x, target, in_lam))
            }
        }
    }

    fn walk_blessed(&self, lam: &Term, target: u32) -> bool {
        let mut t = lam;
        let mut shift = 0u32;
        while let Term::Lam { a, .. } = t {
            shift += 1;
            t = a;
        }
        self.walk(t, target + shift, false)
    }
}

pub fn confinement_of(store: &Store, hash: &str) -> Vec<String> {
    let (ty, body, n) = match store.def_by_hash.get(hash) {
        Some(Def::Func { ty, body, .. }) => {
            (ty, body, store.func_by_hash.get(hash).unwrap().param_names.len())
        }
        _ => return Vec::new(),
    };
    let ptys = param_types(ty, n);
    let inner = strip_lams(body, n);
    let mut out = Vec::with_capacity(n);
    for i in 0..n {
        if !contains_fun(&ptys[i]) {
            out.push(String::new());
            continue;
        }
        let target = (n - 1 - i) as u32;
        let w = ConfWalk { store, param_i: i, param_ty: &ptys[i] };
        if w.walk(inner, target, false) {
            out.push("confined".to_string());
        } else {
            out.push("escapes".to_string());
        }
    }
    out
}

// ===========================================================================
// Mutation (§6.3)
// ===========================================================================

fn op_subs(op: &str) -> Vec<&'static str> {
    match op {
        "+" => vec!["-"],
        "-" => vec!["+"],
        "*" => vec!["+"],
        "/" => vec!["*"],
        "%" => vec!["/"],
        "<" => vec!["<="],
        "<=" => vec!["<"],
        "and" => vec!["or"],
        "or" => vec!["and"],
        _ => vec![],
    }
}

fn swappable(op: &str) -> bool {
    matches!(op, "-" | "/" | "%" | "<" | "<=")
}

fn head_is_self(t: &Term) -> bool {
    let (head, _) = unwind(t);
    matches!(head, Term::SelfRef { .. })
}

/// Node-local mutations for `t` (children handled by the caller).
fn mutate_here(store: &Store, t: &Term, is_func_child: bool) -> Vec<Term> {
    let mut out = Vec::new();
    match t {
        Term::Prim { op, args } => {
            for newop in op_subs(op) {
                out.push(Term::Prim { op: newop.to_string(), args: args.clone() });
            }
            if swappable(op) && args.len() == 2 {
                out.push(Term::Prim {
                    op: op.clone(),
                    args: vec![args[1].clone(), args[0].clone()],
                });
            }
        }
        Term::Int(n) => {
            let cands = [n.wrapping_add(1), n.wrapping_sub(1), 0];
            for c in cands {
                if c != *n {
                    out.push(Term::Int(c));
                }
            }
        }
        Term::If { a, b, c } => {
            out.push(Term::If { a: a.clone(), b: c.clone(), c: b.clone() });
        }
        Term::App { a, b } => {
            // rule 6: function side is an app -> swap the two adjacent call args
            if let Term::App { a: inner_f, b: arg1 } = &**a {
                out.push(Term::App {
                    a: Box::new(Term::App { a: inner_f.clone(), b: b.clone() }),
                    b: arg1.clone(),
                });
            }
            // rule 7: maximal self-headed chain -> replace whole chain by each spine arg
            if !is_func_child && head_is_self(t) {
                let (_, args) = unwind(t);
                for arg in args {
                    out.push(arg.clone());
                }
            }
        }
        Term::Ctor { hash, idx, tyargs, args } => {
            for i in 0..args.len().saturating_sub(1) {
                let mut a2 = args.clone();
                a2.swap(i, i + 1);
                out.push(Term::Ctor {
                    hash: hash.clone(),
                    idx: *idx,
                    tyargs: tyargs.clone(),
                    args: a2,
                });
            }
        }
        Term::Match { hash, a, arms } => {
            // swap each arm-body pair (i, j), i < j
            for i in 0..arms.len() {
                for j in (i + 1)..arms.len() {
                    let mut arms2 = arms.clone();
                    arms2.swap(i, j);
                    out.push(Term::Match { hash: hash.clone(), a: a.clone(), arms: arms2 });
                }
            }
            // replace whole match by the body of each zero-field-binding arm
            let arities = ctor_arities(store, hash);
            for (i, arm) in arms.iter().enumerate() {
                if arities.get(i).copied().unwrap_or(0) == 0 {
                    out.push(arm.clone());
                }
            }
        }
        _ => {}
    }
    out
}

/// All single-node mutations of the subtree rooted at `t`.
fn all_mutations(store: &Store, t: &Term, is_func_child: bool) -> Vec<Term> {
    let mut out = mutate_here(store, t, is_func_child);
    match t {
        Term::Lam { ty, a } => {
            for m in all_mutations(store, a, false) {
                out.push(Term::Lam { ty: ty.clone(), a: Box::new(m) });
            }
        }
        Term::App { a, b } => {
            for m in all_mutations(store, a, true) {
                out.push(Term::App { a: Box::new(m), b: b.clone() });
            }
            for m in all_mutations(store, b, false) {
                out.push(Term::App { a: a.clone(), b: Box::new(m) });
            }
        }
        Term::Let { ty, a, b } => {
            for m in all_mutations(store, a, false) {
                out.push(Term::Let { ty: ty.clone(), a: Box::new(m), b: b.clone() });
            }
            for m in all_mutations(store, b, false) {
                out.push(Term::Let { ty: ty.clone(), a: a.clone(), b: Box::new(m) });
            }
        }
        Term::If { a, b, c } => {
            for m in all_mutations(store, a, false) {
                out.push(Term::If { a: Box::new(m), b: b.clone(), c: c.clone() });
            }
            for m in all_mutations(store, b, false) {
                out.push(Term::If { a: a.clone(), b: Box::new(m), c: c.clone() });
            }
            for m in all_mutations(store, c, false) {
                out.push(Term::If { a: a.clone(), b: b.clone(), c: Box::new(m) });
            }
        }
        Term::Prim { op, args } => {
            for (i, arg) in args.iter().enumerate() {
                for m in all_mutations(store, arg, false) {
                    let mut a2 = args.clone();
                    a2[i] = m;
                    out.push(Term::Prim { op: op.clone(), args: a2 });
                }
            }
        }
        Term::Ctor { hash, idx, tyargs, args } => {
            for (i, arg) in args.iter().enumerate() {
                for m in all_mutations(store, arg, false) {
                    let mut a2 = args.clone();
                    a2[i] = m;
                    out.push(Term::Ctor {
                        hash: hash.clone(),
                        idx: *idx,
                        tyargs: tyargs.clone(),
                        args: a2,
                    });
                }
            }
        }
        Term::Record { names, args } => {
            for (i, arg) in args.iter().enumerate() {
                for m in all_mutations(store, arg, false) {
                    let mut a2 = args.clone();
                    a2[i] = m;
                    out.push(Term::Record { names: names.clone(), args: a2 });
                }
            }
        }
        Term::Field { a, op } => {
            for m in all_mutations(store, a, false) {
                out.push(Term::Field { a: Box::new(m), op: op.clone() });
            }
        }
        Term::Match { hash, a, arms } => {
            for m in all_mutations(store, a, false) {
                out.push(Term::Match { hash: hash.clone(), a: Box::new(m), arms: arms.clone() });
            }
            for (i, arm) in arms.iter().enumerate() {
                for m in all_mutations(store, arm, false) {
                    let mut arms2 = arms.clone();
                    arms2[i] = m;
                    out.push(Term::Match { hash: hash.clone(), a: a.clone(), arms: arms2 });
                }
            }
        }
        _ => {}
    }
    out
}

/// Returns (killed, total) or None if the definition has no properties.
pub fn mutation_score(store: &Store, hash: &str) -> Option<(u32, u32)> {
    let (tyvars, ty, body, props) = match store.def_by_hash.get(hash) {
        Some(Def::Func { tyvars, ty, body, props }) if !props.is_empty() => {
            (*tyvars, ty.clone(), body.clone(), props.clone())
        }
        _ => return None,
    };
    let mut seen: HashSet<String> = HashSet::new();
    seen.insert(hash.to_string());
    let mut total = 0u32;
    let mut killed = 0u32;
    let candidates = all_mutations(store, &body, false);
    for mbody in candidates {
        let mdef = Def::Func {
            tyvars,
            ty: ty.clone(),
            body: mbody,
            props: props.clone(),
        };
        if check_def(store, &mdef).is_err() {
            continue;
        }
        let mhash = sha256_hex(&canonical_bytes(&mdef));
        if seen.contains(&mhash) {
            continue;
        }
        seen.insert(mhash.clone());
        total += 1;
        // run properties against the mutant, cached under its candidate hash
        let mut s = store.clone();
        s.def_by_hash.insert(mhash.clone(), mdef);
        let mut is_killed = false;
        for (pi, prop) in props.iter().enumerate() {
            if let PropResult::Falsified { .. } =
                run_prop(&s, &mhash, pi as u64, prop, MUT_CASES, MUT_FUEL)
            {
                is_killed = true;
                break;
            }
        }
        if is_killed {
            killed += 1;
        }
    }
    Some((killed, total))
}

// ===========================================================================
// Assembly + JSON emission (matches the fixture layout; no proof fields)
// ===========================================================================

pub struct Analysis {
    pub name: String,
    pub hash: String,
    pub is_data: bool,
    pub termination: Term5,
    pub confinement: Vec<String>,
    pub mutants: Option<(u32, u32)>,
    pub level: String,
    pub cases: Option<u64>,
    pub proven: Option<usize>,
}

pub fn analyze(store: &Store, name: &str, proofs: Option<&[bool]>) -> Analysis {
    let hash = store
        .def_by_name
        .get(name)
        .map(|d| sha256_hex(&canonical_bytes(d)))
        .unwrap_or_default();
    let def = store.def_by_name.get(name).unwrap();
    match def {
        Def::Data { .. } => Analysis {
            name: name.to_string(),
            hash,
            is_data: true,
            termination: Term5::Unknown,
            confinement: Vec::new(),
            mutants: None,
            level: "asserted".to_string(),
            cases: None,
            proven: None,
        },
        Def::Func { props, .. } => {
            let mut visiting = HashSet::new();
            let termination = termination(store, &hash, &mut visiting);
            let confinement = confinement_of(store, &hash);
            // testing-based guarantee level (proof upgrade is stage 3)
            let (level, cases, mutants) = if props.is_empty() {
                ("asserted".to_string(), None, None)
            } else {
                let mut falsified = false;
                let info = store.func_by_hash.get(&hash).unwrap();
                for (pi, prop) in props.iter().enumerate() {
                    let _ = &info.prop_names[pi];
                    if let PropResult::Falsified { .. } = run_prop(
                        store,
                        &hash,
                        pi as u64,
                        prop,
                        crate::verify::VERIFY_CASES,
                        crate::verify::VERIFY_FUEL,
                    ) {
                        falsified = true;
                        break;
                    }
                }
                // SPEC §6 (Spec strength): spec strength is computed for every
                // function definition independent of its termination verdict — a
                // `measure`-total function is mutated exactly like a `structural`
                // one.
                let m = match mutation_score(store, &hash) {
                    Some((_, 0)) => None,
                    other => other,
                };
                if falsified {
                    ("falsified".to_string(), None, m)
                } else {
                    ("tested".to_string(), Some(200), m)
                }
            };
            // proof-derived upgrade (SPEC §7.3): if all properties are proven,
            // none refuted, and the prior level is tested, become proven.
            let (level, proven) = match proofs {
                Some(flags) if !props.is_empty() => {
                    let count = flags.iter().filter(|b| **b).count();
                    let lvl = if level == "tested" && count == props.len() {
                        "proven".to_string()
                    } else {
                        level
                    };
                    let pv = if count > 0 { Some(count) } else { None };
                    (lvl, pv)
                }
                _ => (level, None),
            };
            Analysis {
                name: name.to_string(),
                hash,
                is_data: false,
                termination,
                confinement,
                mutants,
                level,
                cases,
                proven,
            }
        }
    }
}

pub fn to_json(a: &Analysis) -> String {
    let mut o = String::new();
    o.push_str("{\n");
    o.push_str(&format!("  \"name\": \"{}\",\n", a.name));
    o.push_str(&format!("  \"hash\": \"{}\",\n", a.hash));
    let kind = if a.is_data { "data" } else { "func" };
    o.push_str(&format!("  \"kind\": \"{}\"", kind));
    if !a.is_data {
        o.push_str(",\n");
        o.push_str(&format!("  \"termination\": \"{}\"", a.termination.as_str()));
        // A parameterless function has an empty confinement verdict; the field is
        // omitted entirely (matching the fixtures) rather than emitted as `[]`.
        if !a.confinement.is_empty() {
            o.push_str(",\n");
            o.push_str("  \"confinement\": [\n");
            for (i, c) in a.confinement.iter().enumerate() {
                o.push_str(&format!("    \"{}\"", c));
                if i + 1 < a.confinement.len() {
                    o.push(',');
                }
                o.push('\n');
            }
            o.push_str("  ]");
        }
        if let Some((killed, total)) = a.mutants {
            o.push_str(",\n");
            o.push_str(&format!("  \"mutants_killed\": {},\n", killed));
            o.push_str(&format!("  \"mutants_total\": {}", total));
        }
        o.push_str(",\n");
        o.push_str(&format!("  \"level\": \"{}\"", a.level));
        if let Some(c) = a.cases {
            o.push_str(",\n");
            o.push_str(&format!("  \"cases\": {}", c));
        }
        if let Some(p) = a.proven {
            o.push_str(",\n");
            o.push_str(&format!("  \"proven\": {}", p));
        }
        o.push('\n');
    } else {
        o.push_str(",\n");
        o.push_str(&format!("  \"level\": \"{}\"\n", a.level));
    }
    o.push('}');
    o
}
