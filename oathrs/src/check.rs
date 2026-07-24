//! The gate: bidirectional type checking with type-argument inference
//! (SPEC §2, §2.1). Checking runs in two modes — SYNTHESIZE a term's type, or
//! CHECK a term against an expected type. A generic reference or constructor MAY
//! omit its `[types]` bracket, in which case the checker INFERS the arguments by
//! one-sided matching and BACKFILLS them into the AST (so an inferred call is
//! byte-identical to the explicit one). A definition failing any check MUST NOT
//! be stored.

use crate::elaborate::Store;
use crate::ir::*;
use std::collections::HashSet;

/// Validate a (possibly already-complete) definition. Runs the bidirectional
/// checker on a CLONE — inference/backfill is a no-op on a fully-annotated
/// definition — and discards the backfill. This is the gate used after
/// elaboration and for mutation candidates.
pub fn check_def(store: &Store, def: &Def) -> Result<(), String> {
    let mut d = def.clone();
    check_and_backfill(store, &mut d)
}

/// Bidirectional check of `def`, BACKFILLING every omitted type argument into the
/// AST in place (SPEC §2.1). Called during elaboration BEFORE the definition is
/// hashed, so identity is that of the fully-annotated form.
pub fn check_and_backfill(store: &Store, def: &mut Def) -> Result<(), String> {
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
            let tyvars = *tyvars;
            wf(store, ty, tyvars, false, 0)?;
            let self_ty = ty.clone();
            let c = Checker { store, self_ty: self_ty.clone(), self_tyvars: tyvars, nvars: tyvars };
            let mut ctx: Vec<Ty> = Vec::new();
            // The body is CHECKED against its declared type.
            c.check(body, &self_ty, &mut ctx)?;
            for (pi, p) in props.iter_mut().enumerate() {
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
                let cp = Checker { store, self_ty: self_ty.clone(), self_tyvars: tyvars, nvars: 0 };
                let mut pctx: Vec<Ty> = p.binders.clone();
                // Each prop body is CHECKED against Bool.
                cp.check(&mut p.body, &Ty::Bool, &mut pctx)?;
            }
            Ok(())
        }
    }
}

struct Checker<'a> {
    store: &'a Store,
    self_ty: Ty,
    self_tyvars: u32,
    nvars: u32,
}

impl<'a> Checker<'a> {
    // -----------------------------------------------------------------------
    // SYNTHESIZE
    // -----------------------------------------------------------------------
    fn synth(&self, t: &mut Term, ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        match t {
            Term::Var(i) => {
                let n = ctx.len();
                if (*i as usize) >= n {
                    return Err(format!("variable index {} out of range", i));
                }
                Ok(ctx[n - 1 - *i as usize].clone())
            }
            Term::Int(_) => Ok(Ty::Int),
            Term::Rat { .. } => Ok(Ty::Rat),
            Term::Float(_) => Ok(Ty::Float),
            Term::Bool(_) => Ok(Ty::Bool),
            Term::Lam { ty, a } => {
                wf(self.store, ty, self.nvars, false, 0)?;
                let pty = ty.clone();
                ctx.push(pty.clone());
                let bt = self.synth(a, ctx);
                ctx.pop();
                Ok(Ty::Fun(Box::new(pty), Box::new(bt?)))
            }
            Term::App { .. } => self.app(t, None, ctx),
            Term::Let { ty, a, b } => {
                wf(self.store, ty, self.nvars, false, 0)?;
                let lty = ty.clone();
                self.check(a, &lty, ctx)?;
                ctx.push(lty);
                let bt = self.synth(b, ctx);
                ctx.pop();
                bt
            }
            Term::If { a, b, c } => {
                self.check(a, &Ty::Bool, ctx)?;
                let bt = self.synth(b, ctx)?;
                self.check(c, &bt, ctx)?;
                Ok(bt)
            }
            Term::Prim { op, args } => self.synth_prim(op, args, ctx),
            Term::Ref { hash, tyargs } => {
                let fi = self
                    .store
                    .func_by_hash
                    .get(hash)
                    .ok_or_else(|| format!("ref to unknown function {}", hash))?;
                if tyargs.is_empty() && fi.tyvars > 0 {
                    return Err(
                        "cannot infer type arguments for a bare polymorphic reference".into(),
                    );
                }
                if tyargs.len() as u32 != fi.tyvars {
                    return Err("ref type-argument count mismatch".into());
                }
                for ta in tyargs.iter() {
                    wf(self.store, ta, self.nvars, false, 0)?;
                }
                Ok(subst(&fi.ty, tyargs))
            }
            Term::SelfRef { tyargs } => {
                if tyargs.is_empty() && self.self_tyvars > 0 {
                    return Err("cannot infer type arguments for a bare self reference".into());
                }
                if tyargs.len() as u32 != self.self_tyvars {
                    return Err("self type-argument count mismatch".into());
                }
                for ta in tyargs.iter() {
                    wf(self.store, ta, self.nvars, false, 0)?;
                }
                Ok(subst(&self.self_ty, tyargs))
            }
            Term::Ctor { .. } => self.ctor(t, None, ctx),
            Term::Match { .. } => self.tc_match(t, None, ctx),
            Term::Record { names, args } => {
                check_record_names(names)?;
                let names2 = names.clone();
                if names2.len() != args.len() {
                    return Err("record names/values length mismatch".into());
                }
                let mut tys = Vec::new();
                for a in args.iter_mut() {
                    tys.push(self.synth(a, ctx)?);
                }
                Ok(Ty::Record { names: names2, args: tys })
            }
            Term::Field { a, op } => {
                let rt = self.synth(a, ctx)?;
                match rt {
                    Ty::Record { names, args } => match names.iter().position(|n| n == op) {
                        Some(idx) => Ok(args[idx].clone()),
                        None => Err(format!("record has no field {}", op)),
                    },
                    _ => Err("field access on a non-record".into()),
                }
            }
        }
    }

    // -----------------------------------------------------------------------
    // CHECK against an expected type (threads `e` through if/let/match/lam)
    // -----------------------------------------------------------------------
    fn check(&self, t: &mut Term, e: &Ty, ctx: &mut Vec<Ty>) -> Result<(), String> {
        match t {
            Term::If { a, b, c } => {
                self.check(a, &Ty::Bool, ctx)?;
                self.check(b, e, ctx)?;
                self.check(c, e, ctx)?;
                Ok(())
            }
            Term::Let { ty, a, b } => {
                wf(self.store, ty, self.nvars, false, 0)?;
                let lty = ty.clone();
                self.check(a, &lty, ctx)?;
                ctx.push(lty);
                let r = self.check(b, e, ctx);
                ctx.pop();
                r
            }
            Term::Match { .. } => {
                self.tc_match(t, Some(e), ctx)?;
                Ok(())
            }
            Term::Lam { ty, a } => match e {
                Ty::Fun(d, cod) => {
                    wf(self.store, ty, self.nvars, false, 0)?;
                    if *ty != **d {
                        return Err("lambda parameter type does not match expected".into());
                    }
                    let pty = ty.clone();
                    ctx.push(pty);
                    let r = self.check(a, cod, ctx);
                    ctx.pop();
                    r
                }
                _ => {
                    let got = self.synth(t, ctx)?;
                    if got != *e {
                        return Err("lambda where a non-function type was expected".into());
                    }
                    Ok(())
                }
            },
            Term::App { .. } => {
                let got = self.app(t, Some(e), ctx)?;
                if got != *e {
                    return Err("application result type does not match expected".into());
                }
                Ok(())
            }
            Term::Ctor { .. } => {
                let got = self.ctor(t, Some(e), ctx)?;
                if got != *e {
                    return Err("constructor result type does not match expected".into());
                }
                Ok(())
            }
            // Any other form: synthesize then compare (SPEC §2.1 default).
            _ => {
                let got = self.synth(t, ctx)?;
                if got != *e {
                    return Err("type mismatch (expected and actual differ)".into());
                }
                Ok(())
            }
        }
    }

    // -----------------------------------------------------------------------
    // APPLICATION `(f a1 … ak)` — solves an omitted generic head, then CHECKS
    // each argument against its (now concrete) parameter type.
    // -----------------------------------------------------------------------
    fn app(&self, t: &mut Term, expected: Option<&Ty>, ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        let owned = std::mem::replace(t, Term::Bool(false));
        let (mut head, mut args) = unwind_app(owned);
        let r = self.process_call(&mut head, &mut args, expected, ctx);
        *t = rebuild_app(head, args);
        r
    }

    fn process_call(
        &self,
        head: &mut Term,
        args: &mut [Term],
        expected: Option<&Ty>,
        ctx: &mut Vec<Ty>,
    ) -> Result<Ty, String> {
        // A generic ref/self head with an OMITTED bracket (empty tyargs) is the
        // only thing to solve; anything else synthesizes normally.
        let generic: Option<(u32, Ty)> = match &*head {
            Term::Ref { hash, tyargs } if tyargs.is_empty() => {
                let fi = self
                    .store
                    .func_by_hash
                    .get(hash)
                    .ok_or_else(|| format!("ref to unknown function {}", hash))?;
                if fi.tyvars > 0 {
                    Some((fi.tyvars, fi.ty.clone()))
                } else {
                    None
                }
            }
            Term::SelfRef { tyargs } if tyargs.is_empty() => {
                if self.self_tyvars > 0 {
                    Some((self.self_tyvars, self.self_ty.clone()))
                } else {
                    None
                }
            }
            _ => None,
        };

        let head_ty = if let Some((tyvars, gen_ty)) = generic {
            let (param_pats, result_pat) = peel(&gen_ty, args.len())
                .ok_or_else(|| "call applied to too many arguments".to_string())?;
            let mut s: Vec<Option<Ty>> = vec![None; tyvars as usize];
            // In CHECK mode, match the peeled result against the expected type.
            if let Some(e) = expected {
                match_ty(&result_pat, e, &mut s)?;
            }
            // Match each SYNTHESIZABLE argument's parameter type against it.
            for (pat, arg) in param_pats.iter().zip(args.iter()) {
                let mut probe = arg.clone();
                let mut pctx = ctx.clone();
                if let Ok(ti) = self.synth(&mut probe, &mut pctx) {
                    match_ty(pat, &ti, &mut s)?;
                }
            }
            let mut solved = Vec::with_capacity(tyvars as usize);
            for (i, opt) in s.into_iter().enumerate() {
                match opt {
                    Some(ty) => solved.push(ty),
                    None => return Err(format!("cannot infer type argument {} of call", i)),
                }
            }
            for ta in &solved {
                wf(self.store, ta, self.nvars, false, 0)?;
            }
            match head {
                Term::Ref { tyargs, .. } | Term::SelfRef { tyargs } => *tyargs = solved.clone(),
                _ => unreachable!(),
            }
            subst(&gen_ty, &solved)
        } else {
            self.synth(head, ctx)?
        };

        // Peel one parameter per argument and CHECK the argument against it (so a
        // bare `(Nil)` argument is inferred from its parameter type).
        let mut cur = head_ty;
        for arg in args.iter_mut() {
            match cur {
                Ty::Fun(d, c) => {
                    self.check(arg, &d, ctx)?;
                    cur = *c;
                }
                _ => return Err("applying a non-function to an argument".into()),
            }
        }
        Ok(cur)
    }

    // -----------------------------------------------------------------------
    // CONSTRUCTOR `(C a1 … ak)` — solves omitted type args from the expected
    // data type and synthesizable fields, then CHECKS each field argument.
    // -----------------------------------------------------------------------
    fn ctor(&self, t: &mut Term, expected: Option<&Ty>, ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        let (hash, idx) = match &*t {
            Term::Ctor { hash, idx, .. } => (hash.clone(), *idx),
            _ => unreachable!(),
        };
        let di = self
            .store
            .data_by_hash
            .get(&hash)
            .ok_or_else(|| format!("ctor of unknown data {}", hash))?
            .clone();
        if idx as usize >= di.ctors.len() {
            return Err("ctor index out of range".into());
        }
        let dtyvars = di.tyvars;

        let (tyargs, args) = match t {
            Term::Ctor { tyargs, args, .. } => (tyargs, args),
            _ => unreachable!(),
        };

        if tyargs.is_empty() && dtyvars > 0 {
            // Solve the omitted arguments.
            let mut s: Vec<Option<Ty>> = vec![None; dtyvars as usize];
            if let Some(e) = expected {
                let pat = Ty::Data {
                    hash: hash.clone(),
                    args: (0..dtyvars).map(Ty::Var).collect(),
                };
                match_ty(&pat, e, &mut s)?;
            }
            let field_pats: Vec<Ty> =
                di.ctors[idx as usize].1.iter().map(|f| field_pattern(f, &hash)).collect();
            if args.len() != field_pats.len() {
                return Err(format!(
                    "constructor applied to {} arguments, expected {}",
                    args.len(),
                    field_pats.len()
                ));
            }
            for (pat, arg) in field_pats.iter().zip(args.iter()) {
                let mut probe = arg.clone();
                let mut pctx = ctx.clone();
                if let Ok(ti) = self.synth(&mut probe, &mut pctx) {
                    match_ty(pat, &ti, &mut s)?;
                }
            }
            let mut solved = Vec::with_capacity(dtyvars as usize);
            for (i, opt) in s.into_iter().enumerate() {
                match opt {
                    Some(ty) => solved.push(ty),
                    None => return Err(format!("cannot infer constructor type argument {}", i)),
                }
            }
            for ta in &solved {
                wf(self.store, ta, self.nvars, false, 0)?;
            }
            *tyargs = solved;
        } else {
            if tyargs.len() as u32 != dtyvars {
                return Err("ctor type-argument count mismatch".into());
            }
            for ta in tyargs.iter() {
                wf(self.store, ta, self.nvars, false, 0)?;
            }
        }

        // CHECK each argument against its now-concrete field type.
        let ret_args = tyargs.clone();
        let fields = instantiate_ctor(self.store, &hash, idx as usize, &ret_args);
        if args.len() != fields.len() {
            return Err(format!(
                "constructor applied to {} arguments, expected {}",
                args.len(),
                fields.len()
            ));
        }
        for (a, f) in args.iter_mut().zip(fields.iter()) {
            self.check(a, f, ctx)?;
        }
        Ok(Ty::Data { hash, args: ret_args })
    }

    // -----------------------------------------------------------------------
    // MATCH — scrutinee synthesizes; each arm is checked/synthesized.
    // -----------------------------------------------------------------------
    fn tc_match(
        &self,
        t: &mut Term,
        expected: Option<&Ty>,
        ctx: &mut Vec<Ty>,
    ) -> Result<Ty, String> {
        let (hash, a, arms) = match t {
            Term::Match { hash, a, arms } => (hash.clone(), a, arms),
            _ => unreachable!(),
        };
        let st = self.synth(a, ctx)?;
        let scrut_args = match &st {
            Ty::Data { hash: h, args } => {
                if *h != hash {
                    return Err("match hash does not match scrutinee".into());
                }
                args.clone()
            }
            _ => return Err("match scrutinee is not a data type".into()),
        };
        let di = self
            .store
            .data_by_hash
            .get(&hash)
            .ok_or_else(|| format!("match on unknown data {}", hash))?
            .clone();
        if arms.len() != di.ctors.len() {
            return Err("match is not exhaustive".into());
        }
        let mut result: Option<Ty> = None;
        for (i, arm) in arms.iter_mut().enumerate() {
            let fields = instantiate_ctor(self.store, &hash, i, &scrut_args);
            let nf = fields.len();
            for f in fields {
                ctx.push(f);
            }
            let at: Result<Ty, String> = match expected {
                Some(e) => self.check(arm, e, ctx).map(|_| e.clone()),
                None => self.synth(arm, ctx),
            };
            for _ in 0..nf {
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

    // -----------------------------------------------------------------------
    // Primitives — operands are CHECKED against their known operand type; `==`
    // synthesizes one operand and checks the other against it.
    // -----------------------------------------------------------------------
    /// Fix the numeric kind of an overloaded arithmetic/ordering primitive
    /// (SPEC §2.1) by SYNTHESIZING its first operand on a probe: the result must
    /// be `Int`, `Rat`, or `Float`, otherwise the primitive is rejected.
    fn numeric_kind(&self, first: &Term, ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        let mut probe = first.clone();
        let mut pctx = ctx.clone();
        match self.synth(&mut probe, &mut pctx)? {
            Ty::Int => Ok(Ty::Int),
            Ty::Rat => Ok(Ty::Rat),
            Ty::Float => Ok(Ty::Float),
            _ => Err("arithmetic operand is neither Int, Rat, nor Float".into()),
        }
    }

    fn synth_prim(&self, op: &str, args: &mut [Term], ctx: &mut Vec<Ty>) -> Result<Ty, String> {
        let arity_err = |n: usize| format!("primitive {} takes {} operand(s)", op, n);
        match op {
            // `%` is Int-only (it truncates); operands CHECK against `Int`.
            "%" => {
                if args.len() != 2 {
                    return Err(arity_err(2));
                }
                for a in args.iter_mut() {
                    self.check(a, &Ty::Int, ctx)?;
                }
                Ok(Ty::Int)
            }
            // `+ - * /` are NUMERIC-OVERLOADED over `Int` or `Rat` (SPEC §2.1):
            // SYNTHESIZE the first operand to fix the numeric kind, reject if it
            // is neither, then CHECK every operand against that same kind. The
            // result is the operand kind (`/` means truncating int division over
            // `Int`, exact real division over `Rat`).
            "+" | "-" | "*" | "/" => {
                if args.len() != 2 {
                    return Err(arity_err(2));
                }
                let k = self.numeric_kind(&args[0], ctx)?;
                for a in args.iter_mut() {
                    self.check(a, &k, ctx)?;
                }
                Ok(k)
            }
            "neg" => {
                if args.len() != 1 {
                    return Err(arity_err(1));
                }
                let k = self.numeric_kind(&args[0], ctx)?;
                self.check(&mut args[0], &k, ctx)?;
                Ok(k)
            }
            // `< <=` are numeric-overloaded over `Int`|`Rat` but return `Bool`.
            "<" | "<=" => {
                if args.len() != 2 {
                    return Err(arity_err(2));
                }
                let k = self.numeric_kind(&args[0], ctx)?;
                for a in args.iter_mut() {
                    self.check(a, &k, ctx)?;
                }
                Ok(Ty::Bool)
            }
            "==" => {
                if args.len() != 2 {
                    return Err(arity_err(2));
                }
                // Synthesize whichever operand can be typed, check the other.
                let mut probe0 = args[0].clone();
                let mut pctx0 = ctx.clone();
                let t = match self.synth(&mut probe0, &mut pctx0) {
                    Ok(t0) => t0,
                    Err(_) => {
                        let mut probe1 = args[1].clone();
                        let mut pctx1 = ctx.clone();
                        self.synth(&mut probe1, &mut pctx1)
                            .map_err(|_| "== operands cannot be typed".to_string())?
                    }
                };
                self.check(&mut args[0], &t, ctx)?;
                self.check(&mut args[1], &t, ctx)?;
                if contains_fun(&t) {
                    return Err("== is not defined on function types".into());
                }
                Ok(Ty::Bool)
            }
            // `fp-eq` is `Float`-only (IEEE-754 equality, SPEC §2/§3): both
            // operands CHECK against `Float`, result is `Bool`.
            "fp-eq" => {
                if args.len() != 2 {
                    return Err(arity_err(2));
                }
                for a in args.iter_mut() {
                    self.check(a, &Ty::Float, ctx)?;
                }
                Ok(Ty::Bool)
            }
            // Numeric conversions (SPEC §2/§3): unary, overloaded by SOURCE type
            // with a fixed result type. SYNTHESIZE the operand, admit only the
            // two source kinds, then CHECK the operand against that kind; any
            // other operand type is a type error.
            "to-rat" | "to-float" | "floor" => {
                if args.len() != 1 {
                    return Err(arity_err(1));
                }
                let src = {
                    let mut probe = args[0].clone();
                    let mut pctx = ctx.clone();
                    self.synth(&mut probe, &mut pctx)?
                };
                // (allowed source kinds, fixed result type) per conversion
                let (ok, result) = match op {
                    // Int|Float -> Rat
                    "to-rat" => (matches!(src, Ty::Int | Ty::Float), Ty::Rat),
                    // Int|Rat -> Float
                    "to-float" => (matches!(src, Ty::Int | Ty::Rat), Ty::Float),
                    // Rat|Float -> Int
                    _ => (matches!(src, Ty::Rat | Ty::Float), Ty::Int),
                };
                if !ok {
                    return Err(format!("{} applied to an unsupported operand type", op));
                }
                self.check(&mut args[0], &src, ctx)?;
                Ok(result)
            }
            "and" | "or" => {
                if args.len() != 2 {
                    return Err(arity_err(2));
                }
                for a in args.iter_mut() {
                    self.check(a, &Ty::Bool, ctx)?;
                }
                Ok(Ty::Bool)
            }
            "not" => {
                if args.len() != 1 {
                    return Err(arity_err(1));
                }
                self.check(&mut args[0], &Ty::Bool, ctx)?;
                Ok(Ty::Bool)
            }
            _ => Err(format!("unknown primitive {}", op)),
        }
    }
}

// ---------------------------------------------------------------------------
// One-sided matching (SPEC §2.1)
// ---------------------------------------------------------------------------

/// One-sided match of pattern `pat` (variable indices `< s.len()` are unknowns)
/// against concrete `g`. Binds an unknown to `g` (failing on a conflicting bind);
/// otherwise `pat` and `g` must have the same shape and match componentwise. A
/// variable in `g` is an opaque constant a pattern variable may bind to. Two
/// unknowns are never unified.
fn match_ty(pat: &Ty, g: &Ty, s: &mut [Option<Ty>]) -> Result<(), String> {
    if let Ty::Var(i) = pat {
        if (*i as usize) < s.len() {
            match &s[*i as usize] {
                None => {
                    s[*i as usize] = Some(g.clone());
                    return Ok(());
                }
                Some(bound) => {
                    return if bound == g {
                        Ok(())
                    } else {
                        Err("conflicting type-argument inference".into())
                    };
                }
            }
        }
    }
    match (pat, g) {
        (Ty::Int, Ty::Int) | (Ty::Bool, Ty::Bool) | (Ty::Rat, Ty::Rat) | (Ty::Float, Ty::Float) => {
            Ok(())
        }
        (Ty::Var(i), Ty::Var(j)) if i == j => Ok(()),
        (Ty::Fun(pa, pb), Ty::Fun(ga, gb)) => {
            match_ty(pa, ga, s)?;
            match_ty(pb, gb, s)
        }
        (Ty::Data { hash: ph, args: pa }, Ty::Data { hash: gh, args: ga })
            if ph == gh && pa.len() == ga.len() =>
        {
            for (p, q) in pa.iter().zip(ga.iter()) {
                match_ty(p, q, s)?;
            }
            Ok(())
        }
        (Ty::Record { names: pn, args: pa }, Ty::Record { names: gn, args: ga })
            if pn == gn && pa.len() == ga.len() =>
        {
            for (p, q) in pa.iter().zip(ga.iter()) {
                match_ty(p, q, s)?;
            }
            Ok(())
        }
        (Ty::Rec { args: pa }, Ty::Rec { args: ga }) if pa.len() == ga.len() => {
            for (p, q) in pa.iter().zip(ga.iter()) {
                match_ty(p, q, s)?;
            }
            Ok(())
        }
        _ => Err("type mismatch during inference".into()),
    }
}

/// Peel `k` parameter types off a function type, returning them and the result.
fn peel(ty: &Ty, k: usize) -> Option<(Vec<Ty>, Ty)> {
    let mut params = Vec::with_capacity(k);
    let mut cur = ty.clone();
    for _ in 0..k {
        match cur {
            Ty::Fun(d, c) => {
                params.push(*d);
                cur = *c;
            }
            _ => return None,
        }
    }
    Some((params, cur))
}

/// A constructor field type as an inference pattern: `rec` becomes the data type
/// being constructed (`Data{hash, args}`); type variables (the data's parameters)
/// are the unknowns and stay put.
fn field_pattern(f: &Ty, selfhash: &str) -> Ty {
    match f {
        Ty::Rec { args } => Ty::Data {
            hash: selfhash.to_string(),
            args: args.iter().map(|a| field_pattern(a, selfhash)).collect(),
        },
        Ty::Fun(a, b) => Ty::Fun(
            Box::new(field_pattern(a, selfhash)),
            Box::new(field_pattern(b, selfhash)),
        ),
        Ty::Data { hash, args } => Ty::Data {
            hash: hash.clone(),
            args: args.iter().map(|a| field_pattern(a, selfhash)).collect(),
        },
        Ty::Record { names, args } => Ty::Record {
            names: names.clone(),
            args: args.iter().map(|a| field_pattern(a, selfhash)).collect(),
        },
        other => other.clone(),
    }
}

fn unwind_app(t: Term) -> (Term, Vec<Term>) {
    let mut args = Vec::new();
    let mut cur = t;
    while let Term::App { a, b } = cur {
        args.push(*b);
        cur = *a;
    }
    args.reverse();
    (cur, args)
}

fn rebuild_app(head: Term, args: Vec<Term>) -> Term {
    let mut acc = head;
    for a in args {
        acc = Term::App { a: Box::new(acc), b: Box::new(a) };
    }
    acc
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

fn wf(store: &Store, ty: &Ty, nvars: u32, allow_rec: bool, rec_arity: u32) -> Result<(), String> {
    match ty {
        Ty::Int | Ty::Bool | Ty::Rat | Ty::Float => Ok(()),
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
        Ty::Int | Ty::Bool | Ty::Rat | Ty::Float => true,
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
    di.ctors[idx].1.iter().map(|f| inst_field(f, tyargs, hash)).collect()
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
