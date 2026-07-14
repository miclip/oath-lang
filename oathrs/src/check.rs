//! The gate: structural type synthesis (SPEC §2). No inference, no
//! unification. A definition failing any check MUST NOT be stored.

use crate::elaborate::Store;
use crate::ir::*;
use std::collections::HashSet;

pub fn check_def(store: &Store, def: &Def) -> Result<(), String> {
    match def {
        Def::Data { tyvars, ctors } => {
            for (ci, fields) in ctors.iter().enumerate() {
                for f in fields {
                    wf(store, f, *tyvars, true, *tyvars)
                        .map_err(|e| format!("constructor {}: {}", ci, e))?;
                }
                for f in fields {
                    check_pos(store, f, true)?;
                }
            }
            Ok(())
        }
        Def::Func { tyvars, ty, body, props } => {
            wf(store, ty, *tyvars, false, 0)?;
            let c = Checker { store, self_ty: ty, self_tyvars: *tyvars, nvars: *tyvars };
            let mut ctx: Vec<Ty> = Vec::new();
            let bt = c.synth(body, &mut ctx)?;
            if bt != *ty {
                return Err("function body type does not match declared type".into());
            }
            for (pi, p) in props.iter().enumerate() {
                for b in &p.binders {
                    if !concrete(b) {
                        return Err(format!(
                            "property {}: binder types must be concrete (no type variables or rec)",
                            pi
                        ));
                    }
                    wf(store, b, 0, false, 0)
                        .map_err(|e| format!("property {} binder: {}", pi, e))?;
                }
                let cp = Checker { store, self_ty: ty, self_tyvars: *tyvars, nvars: 0 };
                let mut pctx: Vec<Ty> = p.binders.clone();
                let bt = cp.synth(&p.body, &mut pctx)?;
                if bt != Ty::Bool {
                    return Err(format!("property {}: body must be Bool", pi));
                }
            }
            Ok(())
        }
    }
}

struct Checker<'a> {
    store: &'a Store,
    self_ty: &'a Ty,
    self_tyvars: u32,
    nvars: u32,
}

impl<'a> Checker<'a> {
    fn synth(&self, t: &Term, ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        match t {
            Term::Var(i) => {
                let n = ctx.len();
                if (*i as usize) >= n {
                    return Err(format!("variable index {} out of range", i));
                }
                Ok(ctx[n - 1 - *i as usize].clone())
            }
            Term::Int(_) => Ok(Ty::Int),
            Term::Bool(_) => Ok(Ty::Bool),
            Term::Str(_) => Ok(Ty::Str),
            Term::Lam { ty, a } => {
                wf(self.store, ty, self.nvars, false, 0)?;
                ctx.push(ty.clone());
                let bt = self.synth(a, ctx);
                ctx.pop();
                Ok(Ty::Fun(Box::new(ty.clone()), Box::new(bt?)))
            }
            Term::App { a, b } => {
                let ft = self.synth(a, ctx)?;
                match ft {
                    Ty::Fun(d, c) => {
                        let at = self.synth(b, ctx)?;
                        if at != *d {
                            return Err("application argument type mismatch".into());
                        }
                        Ok(*c)
                    }
                    _ => Err("applying a non-function".into()),
                }
            }
            Term::Let { ty, a, b } => {
                wf(self.store, ty, self.nvars, false, 0)?;
                let at = self.synth(a, ctx)?;
                if at != *ty {
                    return Err("let bound expression type mismatch".into());
                }
                ctx.push(ty.clone());
                let bt = self.synth(b, ctx);
                ctx.pop();
                bt
            }
            Term::If { a, b, c } => {
                if self.synth(a, ctx)? != Ty::Bool {
                    return Err("if condition must be Bool".into());
                }
                let bt = self.synth(b, ctx)?;
                let ct = self.synth(c, ctx)?;
                if bt != ct {
                    return Err("if branches have different types".into());
                }
                Ok(bt)
            }
            Term::Prim { op, args } => self.synth_prim(op, args, ctx),
            Term::Ref { hash, tyargs } => {
                let fi = self
                    .store
                    .func_by_hash
                    .get(hash)
                    .ok_or_else(|| format!("ref to unknown function {}", hash))?;
                if tyargs.len() as u32 != fi.tyvars {
                    return Err("ref type-argument count mismatch".into());
                }
                for ta in tyargs {
                    wf(self.store, ta, self.nvars, false, 0)?;
                }
                Ok(subst(&fi.ty, tyargs))
            }
            Term::SelfRef { tyargs } => {
                if tyargs.len() as u32 != self.self_tyvars {
                    return Err("self type-argument count mismatch".into());
                }
                for ta in tyargs {
                    wf(self.store, ta, self.nvars, false, 0)?;
                }
                Ok(subst(self.self_ty, tyargs))
            }
            Term::Ctor { hash, idx, tyargs, args } => {
                let di = self
                    .store
                    .data_by_hash
                    .get(hash)
                    .ok_or_else(|| format!("ctor of unknown data {}", hash))?;
                if tyargs.len() as u32 != di.tyvars {
                    return Err("ctor type-argument count mismatch".into());
                }
                for ta in tyargs {
                    wf(self.store, ta, self.nvars, false, 0)?;
                }
                if *idx as usize >= di.ctors.len() {
                    return Err("ctor index out of range".into());
                }
                let fields = instantiate_ctor(self.store, hash, *idx as usize, tyargs);
                if args.len() != fields.len() {
                    return Err(format!(
                        "constructor applied to {} arguments, expected {}",
                        args.len(),
                        fields.len()
                    ));
                }
                for (a, f) in args.iter().zip(fields.iter()) {
                    let at = self.synth(a, ctx)?;
                    if at != *f {
                        return Err("constructor argument type mismatch".into());
                    }
                }
                Ok(Ty::Data { hash: hash.clone(), args: tyargs.clone() })
            }
            Term::Match { hash, a, arms } => {
                let st = self.synth(a, ctx)?;
                let scrut_args = match &st {
                    Ty::Data { hash: h, args } => {
                        if h != hash {
                            return Err("match hash does not match scrutinee".into());
                        }
                        args.clone()
                    }
                    _ => return Err("match scrutinee is not a data type".into()),
                };
                let di = self
                    .store
                    .data_by_hash
                    .get(hash)
                    .ok_or_else(|| format!("match on unknown data {}", hash))?;
                if arms.len() != di.ctors.len() {
                    return Err("match is not exhaustive".into());
                }
                let mut result: Option<Ty> = None;
                for (i, arm) in arms.iter().enumerate() {
                    let fields = instantiate_ctor(self.store, hash, i, &scrut_args);
                    let pushed = fields.len();
                    for f in &fields {
                        ctx.push(f.clone());
                    }
                    let at = self.synth(arm, ctx);
                    for _ in 0..pushed {
                        ctx.pop();
                    }
                    let at = at?;
                    match &result {
                        None => result = Some(at),
                        Some(r) => {
                            if *r != at {
                                return Err("match arms have different result types".into());
                            }
                        }
                    }
                }
                Ok(result.unwrap())
            }
            Term::Record { names, args } => {
                check_record_names(names)?;
                if names.len() != args.len() {
                    return Err("record names/values length mismatch".into());
                }
                let mut tys = Vec::new();
                for a in args {
                    tys.push(self.synth(a, ctx)?);
                }
                Ok(Ty::Record { names: names.clone(), args: tys })
            }
            Term::Field { a, op } => {
                let rt = self.synth(a, ctx)?;
                match rt {
                    Ty::Record { names, args } => {
                        match names.iter().position(|n| n == op) {
                            Some(idx) => Ok(args[idx].clone()),
                            None => Err(format!("record has no field {}", op)),
                        }
                    }
                    _ => Err("field access on a non-record".into()),
                }
            }
        }
    }

    fn synth_prim(&self, op: &str, args: &[Term], ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        let mut at = Vec::new();
        for a in args {
            at.push(self.synth(a, ctx)?);
        }
        let expect = |ts: &[Ty], want: &Ty| -> Result<(), String> {
            for t in ts {
                if t != want {
                    return Err(format!("primitive {} operand type mismatch", op));
                }
            }
            Ok(())
        };
        match op {
            "+" | "-" | "*" | "/" | "%" => {
                if at.len() != 2 {
                    return Err(format!("{} takes 2 operands", op));
                }
                expect(&at, &Ty::Int)?;
                Ok(Ty::Int)
            }
            "neg" => {
                if at.len() != 1 {
                    return Err("neg takes 1 operand".into());
                }
                expect(&at, &Ty::Int)?;
                Ok(Ty::Int)
            }
            "<" | "<=" => {
                if at.len() != 2 {
                    return Err(format!("{} takes 2 operands", op));
                }
                expect(&at, &Ty::Int)?;
                Ok(Ty::Bool)
            }
            "==" => {
                if at.len() != 2 {
                    return Err("== takes 2 operands".into());
                }
                if at[0] != at[1] {
                    return Err("== operands have different types".into());
                }
                if contains_fun(&at[0]) {
                    return Err("== is not defined on function types".into());
                }
                Ok(Ty::Bool)
            }
            "and" | "or" => {
                if at.len() != 2 {
                    return Err(format!("{} takes 2 operands", op));
                }
                expect(&at, &Ty::Bool)?;
                Ok(Ty::Bool)
            }
            "not" => {
                if at.len() != 1 {
                    return Err("not takes 1 operand".into());
                }
                expect(&at, &Ty::Bool)?;
                Ok(Ty::Bool)
            }
            "++" => {
                if at.len() != 2 {
                    return Err("++ takes 2 operands".into());
                }
                expect(&at, &Ty::Str)?;
                Ok(Ty::Str)
            }
            "str-len" => {
                if at.len() != 1 {
                    return Err("str-len takes 1 operand".into());
                }
                expect(&at, &Ty::Str)?;
                Ok(Ty::Int)
            }
            _ => Err(format!("unknown primitive {}", op)),
        }
    }
}

// ---------------------------------------------------------------------------
// Type helpers
// ---------------------------------------------------------------------------

fn check_record_names(names: &[String]) -> Result<(), String> {
    for w in names.windows(2) {
        if w[0].as_bytes() >= w[1].as_bytes() {
            return Err("record field names must be sorted ascending and unique".into());
        }
    }
    Ok(())
}

fn wf(
    store: &Store,
    ty: &Ty,
    nvars: u32,
    allow_rec: bool,
    rec_arity: u32,
) -> Result<(), String> {
    match ty {
        Ty::Int | Ty::Bool | Ty::Str => Ok(()),
        Ty::Var(i) => {
            if *i < nvars {
                Ok(())
            } else {
                Err(format!("type variable {} out of scope", i))
            }
        }
        Ty::Fun(a, b) => {
            wf(store, a, nvars, allow_rec, rec_arity)?;
            wf(store, b, nvars, allow_rec, rec_arity)
        }
        Ty::Data { hash, args } => {
            let di = store
                .data_by_hash
                .get(hash)
                .ok_or_else(|| format!("reference to unknown data {}", hash))?;
            if di.tyvars as usize != args.len() {
                return Err("data type applied to wrong number of arguments".into());
            }
            for a in args {
                wf(store, a, nvars, allow_rec, rec_arity)?;
            }
            Ok(())
        }
        Ty::Rec { args } => {
            if !allow_rec {
                return Err("rec type outside a data definition".into());
            }
            if args.len() as u32 != rec_arity {
                return Err("rec applied to wrong number of type parameters".into());
            }
            for (i, a) in args.iter().enumerate() {
                match a {
                    Ty::Var(v) if *v as usize == i => {}
                    _ => return Err("rec must be applied to exactly the ADT's parameters".into()),
                }
                wf(store, a, nvars, allow_rec, rec_arity)?;
            }
            Ok(())
        }
        Ty::Record { names, args } => {
            check_record_names(names)?;
            if names.len() != args.len() {
                return Err("record names/args length mismatch".into());
            }
            for a in args {
                wf(store, a, nvars, allow_rec, rec_arity)?;
            }
            Ok(())
        }
    }
}

fn concrete(ty: &Ty) -> bool {
    match ty {
        Ty::Int | Ty::Bool | Ty::Str => true,
        Ty::Var(_) | Ty::Rec { .. } => false,
        Ty::Fun(a, b) => concrete(a) && concrete(b),
        Ty::Data { args, .. } => args.iter().all(concrete),
        Ty::Record { args, .. } => args.iter().all(concrete),
    }
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

fn subst(ty: &Ty, args: &[Ty]) -> Ty {
    match ty {
        Ty::Var(i) => args[*i as usize].clone(),
        Ty::Fun(a, b) => Ty::Fun(Box::new(subst(a, args)), Box::new(subst(b, args))),
        Ty::Data { hash, args: as_ } => Ty::Data {
            hash: hash.clone(),
            args: as_.iter().map(|a| subst(a, args)).collect(),
        },
        Ty::Rec { args: as_ } => Ty::Rec { args: as_.iter().map(|a| subst(a, args)).collect() },
        Ty::Record { names, args: as_ } => Ty::Record {
            names: names.clone(),
            args: as_.iter().map(|a| subst(a, args)).collect(),
        },
        other => other.clone(),
    }
}

fn instantiate_ctor(store: &Store, hash: &str, idx: usize, tyargs: &[Ty]) -> Vec<Ty> {
    let di = store.data_by_hash.get(hash).expect("data present");
    di.ctors[idx]
        .1
        .iter()
        .map(|f| inst_field(f, tyargs, hash))
        .collect()
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

// ---------------------------------------------------------------------------
// Strict positivity (SPEC §2)
// ---------------------------------------------------------------------------

fn check_pos(store: &Store, ty: &Ty, positive: bool) -> Result<(), String> {
    match ty {
        Ty::Rec { .. } => {
            if positive {
                Ok(())
            } else {
                Err("strict positivity: rec in a negative position".into())
            }
        }
        Ty::Fun(a, b) => {
            check_pos(store, a, !positive)?;
            check_pos(store, b, positive)
        }
        Ty::Data { hash, args } => {
            let mut vis = HashSet::new();
            let af = data_arrow_free(store, hash, &mut vis);
            let np = if af { positive } else { false };
            for a in args {
                check_pos(store, a, np)?;
            }
            Ok(())
        }
        Ty::Record { args, .. } => {
            let mut vis = HashSet::new();
            let af = args.iter().all(|a| !contains_arrow(store, a, &mut vis));
            let np = if af { positive } else { false };
            for a in args {
                check_pos(store, a, np)?;
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

fn contains_arrow(store: &Store, ty: &Ty, vis: &mut HashSet<String>) -> bool {
    match ty {
        Ty::Fun(_, _) => true,
        Ty::Data { hash, args } => {
            !data_arrow_free(store, hash, vis) || args.iter().any(|a| contains_arrow(store, a, vis))
        }
        Ty::Rec { .. } => false,
        Ty::Record { args, .. } => args.iter().any(|a| contains_arrow(store, a, vis)),
        _ => false,
    }
}

fn data_arrow_free(store: &Store, hash: &str, vis: &mut HashSet<String>) -> bool {
    if vis.contains(hash) {
        return true;
    }
    let di = match store.data_by_hash.get(hash) {
        Some(d) => d,
        None => return true,
    };
    vis.insert(hash.to_string());
    let free = di
        .ctors
        .iter()
        .all(|(_, fields)| fields.iter().all(|f| !contains_arrow(store, f, vis)));
    vis.remove(hash);
    free
}
