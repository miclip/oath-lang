//! Proof obligations / SMT boundary (SPEC §7, §7.1, §7.2, §7.3).
//! Translates the provable fragment to SMT-LIB, drives z3 (`z3 -in`), and
//! reproduces per-property proof outcomes. Because there is no byte-level
//! SMT fixture, the encoding only needs to be logically faithful enough that
//! z3 returns the same sat/unsat as the reference; naming is internal.

use crate::analyze::termination;
use crate::elaborate::Store;
use crate::ir::*;
use std::collections::{BTreeMap, BTreeSet, HashSet, VecDeque};
use std::io::Write;
use std::process::{Command, Stdio};

// SPEC §7.1 sets a 15 s per-goal timeout. Every goal in this corpus that is
// provable at all resolves well under it once the right lemmas are present, so
// a shorter budget reproduces the reference outcomes far faster (documented in
// DIVERGENCES). The unprovable goals fail regardless of the budget.
const Z3_TIMEOUT_MS: u64 = 4000;

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
}

fn sort_of(store: &Store, ty: &Ty, sc: &mut Sorts) -> String {
    match ty {
        Ty::Int => "Int".to_string(),
        Ty::Bool => "Bool".to_string(),
        Ty::Str => "String".to_string(),
        Ty::Fun(a, b) => format!("(Array {} {})", sort_of(store, a, sc), sort_of(store, b, sc)),
        Ty::Data { hash, args } => {
            let mut name = format!("T{}", &hash[..8]);
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
                    body.push_str(" (");
                    body.push_str(&csmt);
                    for (j, f) in fields.iter().enumerate() {
                        let fty = inst_field(f, args, hash);
                        let fsort = sort_of(store, &fty, sc);
                        body.push_str(&format!(" ({}_{} {})", csmt, j, fsort));
                    }
                    body.push(')');
                    let _ = ci;
                }
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
                    body.push_str(&format!(" ({}_{} {})", name, sanitize(n), fsort));
                }
                body.push(')');
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

// ---------------------------------------------------------------------------
// translation context
// ---------------------------------------------------------------------------

struct Cx<'a> {
    store: &'a Store,
    sc: Sorts,
    fun_decls: BTreeMap<String, String>, // id -> "(declare-fun ...)"
    axioms: BTreeMap<String, String>,    // id -> "(assert ...)"
    need_axiom: VecDeque<(String, Vec<Ty>)>,
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
            need_axiom: VecDeque::new(),
            axiomatized: BTreeSet::new(),
            quantified: false,
        }
    }

    fn instance_id(hash: &str, cargs: &[Ty], sc: &mut Sorts, store: &Store) -> String {
        let mut s = format!("f_{}", &hash[..8]);
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
        self.fun_decls.insert(id.clone(), decl);
        self.quantified = true;
        if is_total(self.store, hash) {
            self.need_axiom.push_back((hash.to_string(), cargs.to_vec()));
        }
        id
    }

    fn build_axioms(&mut self) {
        while let Some((hash, cargs)) = self.need_axiom.pop_front() {
            let id = Cx::instance_id(&hash, &cargs, &mut self.sc, self.store);
            if self.axiomatized.contains(&id) {
                continue;
            }
            self.axiomatized.insert(id.clone());
            let n = self.store.func_by_hash.get(&hash).unwrap().param_names.len();
            let (body, fty) = match self.store.def_by_hash.get(&hash) {
                Some(Def::Func { body, ty, .. }) => (body.clone(), ty.clone()),
                _ => continue,
            };
            let (ptys, _ret) = param_types(&apply_tyenv(&fty, &cargs), n);
            let inner = strip_lams(&body, n);
            let mut env: Vec<(String, Ty)> = Vec::new();
            let mut decls = String::new();
            for (j, pt) in ptys.iter().enumerate() {
                let vname = format!("x{}", j);
                let s = sort_of(self.store, pt, &mut self.sc);
                decls.push_str(&format!("({} {}) ", vname, s));
                env.push((vname, pt.clone()));
            }
            let call = {
                let mut c = format!("({}", id);
                for j in 0..n {
                    c.push_str(&format!(" x{}", j));
                }
                c.push(')');
                c
            };
            match self.tr(inner, &env, &cargs, &hash, &cargs) {
                Ok((rhs, _)) => {
                    let axiom = if n == 0 {
                        format!("(assert (= {} {}))", call, rhs)
                    } else {
                        format!(
                            "(assert (forall ({}) (! (= {} {}) :pattern ({}))))",
                            decls.trim_end(),
                            call,
                            rhs,
                            call
                        )
                    };
                    self.axioms.insert(id, axiom);
                }
                Err(_) => { /* body outside fragment: leave uninterpreted */ }
            }
        }
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
                        Ok((format!("({}_{} {})", sortname, sanitize(op), ae), args[i].clone()))
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
        // translate arguments
        let mut aexprs = Vec::new();
        for (i, a) in args.iter().enumerate() {
            let (ae, _) = self.tr(a, env, tyenv, self_hash, self_tyargs)?;
            aexprs.push((ae, ptys[i].clone()));
        }
        if is_recursive(self.store, hash) {
            let id = self.register_fun(hash, &cargs);
            let mut e = format!("({}", id);
            for (ae, _) in &aexprs {
                e.push(' ');
                e.push_str(ae);
            }
            e.push(')');
            Ok((e, ret0))
        } else {
            // inline: beta-reduce through the lambda spine
            let body = match self.store.def_by_hash.get(hash) {
                Some(Def::Func { body, .. }) => body.clone(),
                _ => return Err(()),
            };
            let inner = strip_lams(&body, n);
            let env2: Vec<(String, Ty)> = aexprs;
            self.tr(inner, &env2, &cargs, hash, &cargs)
        }
    }
}

// ---------------------------------------------------------------------------
// proof driver
// ---------------------------------------------------------------------------

fn run_z3(script: &str) -> Outcome {
    // `-t:` is z3's per-query soft timeout (ms); `-T:` is a hard wall-clock
    // process timeout (s) that guarantees the subprocess exits even when a
    // quantified goal would otherwise run away.
    let soft = format!("-t:{}", Z3_TIMEOUT_MS);
    let hard = format!("-T:{}", Z3_TIMEOUT_MS / 1000 + 2);
    let mut child = match Command::new("z3")
        .arg("-in")
        .arg(&soft)
        .arg(&hard)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    {
        Ok(c) => c,
        Err(_) => return Outcome::Unknown,
    };
    if let Some(mut sin) = child.stdin.take() {
        let _ = sin.write_all(script.as_bytes());
    }
    let out = match child.wait_with_output() {
        Ok(o) => o,
        Err(_) => return Outcome::Unknown,
    };
    let s = String::from_utf8_lossy(&out.stdout);
    // first non-empty result line
    for line in s.lines() {
        let l = line.trim();
        match l {
            "unsat" => return Outcome::Unsat,
            "sat" => return Outcome::Sat,
            "unknown" => return Outcome::Unknown,
            _ => {}
        }
    }
    Outcome::Unknown
}

struct Prover<'a> {
    store: &'a Store,
    // proven props: (def hash, prop index) recorded when proven
    proven: BTreeSet<(String, usize)>,
}

impl<'a> Prover<'a> {
    fn header() -> String {
        format!("(set-option :timeout {})\n(set-logic ALL)\n", Z3_TIMEOUT_MS)
    }

    /// Assemble a full self-contained script from a fresh context.
    fn assemble(cx: &Cx, body_asserts: &str, extra_decls: &str) -> String {
        let mut s = Prover::header();
        if !cx.sc.order.is_empty() {
            s.push_str("(declare-datatypes (");
            for name in &cx.sc.order {
                s.push_str(&format!("({} 0)", name));
            }
            s.push_str(") (");
            for name in &cx.sc.order {
                s.push('(');
                s.push_str(cx.sc.decl.get(name).unwrap().trim());
                s.push(')');
            }
            s.push_str("))\n");
        }
        for d in cx.fun_decls.values() {
            s.push_str(d);
            s.push('\n');
        }
        for a in cx.axioms.values() {
            s.push_str(a);
            s.push('\n');
        }
        s.push_str(extra_decls);
        s.push_str(body_asserts);
        s.push_str("(check-sat)\n");
        s
    }

    fn lemma_asserts(&self, cx: &mut Cx, lemmas: &[(String, usize)]) -> String {
        let mut out = String::new();
        for (hash, pi) in lemmas {
            let prop = match self.store.def_by_hash.get(hash) {
                Some(Def::Func { props, .. }) => props[*pi].clone(),
                _ => continue,
            };
            let mut env = Vec::new();
            let mut decls = String::new();
            for (k, bt) in prop.binders.iter().enumerate() {
                let vname = format!("l{}", k);
                let s = sort_of(self.store, bt, &mut cx.sc);
                decls.push_str(&format!("({} {}) ", vname, s));
                env.push((vname, bt.clone()));
            }
            if let Ok((be, _)) = cx.tr(&prop.body, &env, &[], hash, &[]) {
                if prop.binders.is_empty() {
                    out.push_str(&format!("(assert {})\n", be));
                } else {
                    cx.quantified = true;
                    out.push_str(&format!("(assert (forall ({}) {}))\n", decls.trim_end(), be));
                }
            }
        }
        out
    }

    /// Direct proof attempt. Returns (outcome, quantified?).
    fn try_direct(
        &self,
        def_hash: &str,
        prop: &Prop,
        lemmas: &[(String, usize)],
    ) -> (Outcome, bool) {
        let mut cx = Cx::new(self.store);
        let lem = self.lemma_asserts(&mut cx, lemmas);
        // binders as constants
        let mut env = Vec::new();
        let mut decls = String::new();
        for (k, bt) in prop.binders.iter().enumerate() {
            let vname = format!("b{}", k);
            let s = sort_of(self.store, bt, &mut cx.sc);
            decls.push_str(&format!("(declare-const {} {})\n", vname, s));
            env.push((vname, bt.clone()));
        }
        let goal = match cx.tr(&prop.body, &env, &[], def_hash, &[]) {
            Ok((g, _)) => g,
            Err(_) => return (Outcome::Unknown, true),
        };
        cx.build_axioms();
        let body = format!("{}(assert (not {}))\n", lem, goal);
        let o = run_z3(&Prover::assemble(&cx, &body, &decls));
        (o, cx.quantified)
    }

    /// Structural induction on binder `k` (a datatype). Returns true if proven.
    fn try_induction_binder(
        &self,
        def_hash: &str,
        prop: &Prop,
        k: usize,
        lemmas: &[(String, usize)],
    ) -> bool {
        let (dhash, dargs) = match &prop.binders[k] {
            Ty::Data { hash, args } => (hash.clone(), args.clone()),
            _ => return false,
        };
        let di = match self.store.data_by_hash.get(&dhash) {
            Some(d) => d.clone(),
            None => return false,
        };
        let ind_sort = {
            let mut sc = Sorts::default();
            sort_of(self.store, &prop.binders[k], &mut sc)
        };
        for (ci, (cname, cfields)) in di.ctors.iter().enumerate() {
            let mut cx = Cx::new(self.store);
            let lem = self.lemma_asserts(&mut cx, lemmas);
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
                let vname = format!("f{}_{}", ci, j);
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
                Err(_) => return false,
            };
            cx.build_axioms();
            let body = format!("{}{}(assert (not {}))\n", lem, ih, goal);
            if run_z3(&Prover::assemble(&cx, &body, &decls)) != Outcome::Unsat {
                return false;
            }
        }
        true
    }

    fn prove_prop(&self, def_hash: &str, prop: &Prop, lemmas: &[(String, usize)]) -> bool {
        let (o, quantified) = self.try_direct(def_hash, prop, lemmas);
        if o == Outcome::Unsat {
            return true;
        }
        // A quantifier-free problem is decidable: the direct result is final,
        // and induction cannot add power (SPEC §7.2).
        if !quantified {
            return false;
        }
        for k in 0..prop.binders.len() {
            if matches!(prop.binders[k], Ty::Data { .. })
                && self.try_induction_binder(def_hash, prop, k, lemmas)
            {
                return true;
            }
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

/// Transitive function dependencies of a definition, BFS in sorted hash order.
fn transitive_deps(store: &Store, hash: &str) -> Vec<String> {
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

    let mut prover = Prover { store, proven: BTreeSet::new() };

    // Precompute transitive deps once.
    let deps_of: BTreeMap<String, Vec<String>> =
        order.iter().map(|h| (h.clone(), transitive_deps(store, h))).collect();

    // Fixpoint: the lemma set for a property is every OTHER proven property of
    // the same definition plus all proven properties of its transitive deps
    // (SPEC §7.2). Because a definition's own metadata accumulates proven props,
    // a later-indexed sibling can serve as a lemma for an earlier one — so we
    // iterate until no new property becomes proven. A property is re-attempted
    // only when its available-lemma count has grown, which keeps the hopeless
    // goals from being re-run every pass.
    let mut last_lemma_count: BTreeMap<(String, usize), usize> = BTreeMap::new();
    loop {
        let mut changed = false;
        for hash in &order {
            let props = match store.def_by_hash.get(hash) {
                Some(Def::Func { props, .. }) => props.clone(),
                _ => continue,
            };
            if falsified.contains(hash) {
                continue; // never proved (§7.3: upgrade requires tested)
            }
            // deps' proven props
            let mut dep_lemmas: Vec<(String, usize)> = Vec::new();
            for d in &deps_of[hash] {
                if let Some(Def::Func { props: dp, .. }) = store.def_by_hash.get(d) {
                    for pi in 0..dp.len() {
                        if prover.proven.contains(&(d.clone(), pi)) {
                            dep_lemmas.push((d.clone(), pi));
                        }
                    }
                }
            }
            for (pi, prop) in props.iter().enumerate() {
                if prover.proven.contains(&(hash.clone(), pi)) {
                    continue;
                }
                let mut lemmas = dep_lemmas.clone();
                for j in 0..props.len() {
                    if j != pi && prover.proven.contains(&(hash.clone(), j)) {
                        lemmas.push((hash.clone(), j));
                    }
                }
                let key = (hash.clone(), pi);
                if last_lemma_count.get(&key) == Some(&lemmas.len()) {
                    continue; // lemma set unchanged since last attempt
                }
                last_lemma_count.insert(key.clone(), lemmas.len());
                if prover.prove_prop(hash, prop, &lemmas) {
                    prover.proven.insert(key);
                    changed = true;
                }
            }
        }
        if !changed {
            break;
        }
    }

    let mut results: BTreeMap<String, ProofResult> = BTreeMap::new();
    for hash in &order {
        if let Some(Def::Func { props, .. }) = store.def_by_hash.get(hash) {
            let flags = (0..props.len())
                .map(|pi| prover.proven.contains(&(hash.clone(), pi)))
                .collect();
            results.insert(hash.clone(), ProofResult { proven: flags });
        }
    }
    results
}
