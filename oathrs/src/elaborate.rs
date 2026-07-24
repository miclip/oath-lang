//! Surface elaboration (SPEC §1.4). Resolves names lazily across the whole
//! corpus to a fixpoint so cross-file, definition-before-use references work.

use crate::hash::sha256_hex;
use crate::ir::*;
use crate::sexpr::{Reader, Sexpr};
use std::collections::{BTreeMap, HashSet};

const PRIMS: &[&str] = &[
    "+", "-", "*", "/", "%", "neg", "==", "<", "<=", "and", "or", "not", "fp-eq",
    // Numeric conversions (SPEC §1.3, §2, §3): unary, overloaded by SOURCE type.
    "to-rat", "to-float", "floor",
];

#[derive(Clone)]
pub struct DataInfo {
    pub hash: String,
    pub name: String,
    pub tyvars: u32,
    pub ctors: Vec<(String, Vec<Ty>)>,
}

#[derive(Clone)]
pub struct FuncInfo {
    pub hash: String,
    pub name: String,
    pub tyvars: u32,
    pub ty: Ty,
    pub prop_names: Vec<String>,
    pub param_names: Vec<String>,
}

#[derive(Default, Clone)]
pub struct Store {
    pub data_by_name: BTreeMap<String, DataInfo>,
    pub func_by_name: BTreeMap<String, FuncInfo>,
    pub data_by_hash: BTreeMap<String, DataInfo>,
    pub func_by_hash: BTreeMap<String, FuncInfo>,
    pub def_by_name: BTreeMap<String, Def>,
    pub def_by_hash: BTreeMap<String, Def>,
    pub order: Vec<String>,
    all_data_names: HashSet<String>,
    all_func_names: HashSet<String>,
    all_ctor_names: HashSet<String>,
}

enum EErr {
    Defer(String),
    Hard(String),
}
type ER<T> = Result<T, EErr>;

fn hard<T>(m: String) -> ER<T> {
    Err(EErr::Hard(m))
}

pub enum Elaborated {
    Data { name: String, def: Def, info: DataInfo },
    Func { name: String, def: Def, info: FuncInfo },
}

impl Store {
    fn lookup_var(ctx: &[String], name: &str) -> Option<u32> {
        for (p, n) in ctx.iter().enumerate().rev() {
            if n == name {
                return Some((ctx.len() - 1 - p) as u32);
            }
        }
        None
    }

    /// Constructor lookup: scan the current name index in ascending name order
    /// and choose the first ADT whose metadata contains the constructor name.
    fn lookup_ctor(&self, name: &str) -> ER<Option<(String, u32, u32)>> {
        for (_, di) in self.data_by_name.iter() {
            if let Some(idx) = di.ctors.iter().position(|(cn, _)| cn == name) {
                return Ok(Some((di.hash.clone(), idx as u32, di.tyvars)));
            }
        }
        if self.all_ctor_names.contains(name) {
            return Err(EErr::Defer(name.to_string()));
        }
        Ok(None)
    }

    // ---- type elaboration ----

    fn elab_type(&self, s: &Sexpr, tyvars: &[String], self_data: Option<&str>) -> ER<Ty> {
        match s {
            Sexpr::Sym(name) => match name.as_str() {
                "Int" => Ok(Ty::Int),
                "Bool" => Ok(Ty::Bool),
                "Rat" => Ok(Ty::Rat),
                "Float" => Ok(Ty::Float),
                // `Str` is no longer a primitive type — it resolves to the `Str`
                // datatype like any other ADT (via the data lookup below).
                _ => {
                    if let Some(i) = tyvars.iter().position(|v| v == name) {
                        return Ok(Ty::Var(i as u32));
                    }
                    if self_data == Some(name.as_str()) {
                        return Ok(Ty::Rec { args: vec![] });
                    }
                    if let Some(di) = self.data_by_name.get(name) {
                        return Ok(Ty::Data { hash: di.hash.clone(), args: vec![] });
                    }
                    if self.all_data_names.contains(name) {
                        return Err(EErr::Defer(name.clone()));
                    }
                    hard(format!("unknown type name: {}", name))
                }
            },
            Sexpr::List(items) => {
                if items.is_empty() {
                    return hard("empty type list".into());
                }
                let head = match &items[0] {
                    Sexpr::Sym(h) => h.clone(),
                    _ => return hard("type list head must be a symbol".into()),
                };
                if head == "->" {
                    let parts = &items[1..];
                    if parts.len() < 2 {
                        return hard("(-> ...) needs at least two types".into());
                    }
                    let mut tys = Vec::new();
                    for p in parts {
                        tys.push(self.elab_type(p, tyvars, self_data)?);
                    }
                    let mut acc = tys.pop().unwrap();
                    while let Some(t) = tys.pop() {
                        acc = Ty::Fun(Box::new(t), Box::new(acc));
                    }
                    return Ok(acc);
                }
                // data application (Data arg ...)
                let mut args = Vec::new();
                for a in &items[1..] {
                    args.push(self.elab_type(a, tyvars, self_data)?);
                }
                if self_data == Some(head.as_str()) {
                    return Ok(Ty::Rec { args });
                }
                if let Some(di) = self.data_by_name.get(&head) {
                    return Ok(Ty::Data { hash: di.hash.clone(), args });
                }
                if self.all_data_names.contains(&head) {
                    return Err(EErr::Defer(head));
                }
                hard(format!("unknown type constructor: {}", head))
            }
            Sexpr::Brace(items) => {
                if items.len() % 2 != 0 {
                    return hard("record type needs name/type pairs".into());
                }
                let mut pairs: Vec<(String, Ty)> = Vec::new();
                let mut i = 0;
                while i < items.len() {
                    let fname = match &items[i] {
                        Sexpr::Sym(n) => n.clone(),
                        _ => return hard("record field name must be a symbol".into()),
                    };
                    let fty = self.elab_type(&items[i + 1], tyvars, self_data)?;
                    pairs.push((fname, fty));
                    i += 2;
                }
                pairs.sort_by(|a, b| a.0.as_bytes().cmp(b.0.as_bytes()));
                for w in pairs.windows(2) {
                    if w[0].0 == w[1].0 {
                        return hard(format!("duplicate record field: {}", w[0].0));
                    }
                }
                let names = pairs.iter().map(|p| p.0.clone()).collect();
                let args = pairs.into_iter().map(|p| p.1).collect();
                Ok(Ty::Record { names, args })
            }
            _ => hard("invalid type syntax".into()),
        }
    }

    // ---- term elaboration ----

    fn elab_term(
        &self,
        s: &Sexpr,
        tyvars: &[String],
        self_name: &str,
        self_tyvars: u32,
        ctx: &mut Vec<String>,
    ) -> ER<Term> {
        match s {
            Sexpr::Int(n) => Ok(Term::Int(n.clone())),
            // Rational literal (SPEC §1.4): already reduced by the reader.
            Sexpr::Rat(num, den) => Ok(Term::Rat { num: num.clone(), den: den.clone() }),
            // Float literal (SPEC §1.4): the reader already canonicalized the bits.
            Sexpr::Float(bits) => Ok(Term::Float(*bits)),
            // STRING-LITERAL SUGAR (SPEC §1.4): `"…"` desugars to the codepoint
            // chain `(SCons c0 (SCons c1 … (SNil)))`, byte-identical to the ctor
            // form. There is no string-literal term anymore.
            Sexpr::Str(v) => self.elab_string_literal(v),
            Sexpr::Sym(name) => match name.as_str() {
                "true" => Ok(Term::Bool(true)),
                "false" => Ok(Term::Bool(false)),
                _ => {
                    // Bare name resolution (SPEC §1.4), in order: local variable,
                    // the function being defined (emits `self`), constructor,
                    // stored function. A generic reference used bare carries no
                    // type arguments (omitted); the checker infers or rejects.
                    if let Some(i) = Self::lookup_var(ctx, name) {
                        return Ok(Term::Var(i));
                    }
                    if name == self_name {
                        return Ok(Term::SelfRef { tyargs: vec![] });
                    }
                    if let Some((hash, idx, _)) = self.lookup_ctor(name)? {
                        return Ok(Term::Ctor { hash, idx, tyargs: vec![], args: vec![] });
                    }
                    if let Some(fi) = self.func_by_name.get(name) {
                        return Ok(Term::Ref { hash: fi.hash.clone(), tyargs: vec![] });
                    }
                    if self.all_func_names.contains(name) {
                        return Err(EErr::Defer(name.to_string()));
                    }
                    hard(format!("unbound variable: {}", name))
                }
            },
            Sexpr::Brace(items) => {
                // record literal {field expr ...}
                if items.len() % 2 != 0 {
                    return hard("record literal needs name/value pairs".into());
                }
                let mut pairs: Vec<(String, Term)> = Vec::new();
                let mut i = 0;
                while i < items.len() {
                    let fname = match &items[i] {
                        Sexpr::Sym(n) => n.clone(),
                        _ => return hard("record field name must be a symbol".into()),
                    };
                    let v = self.elab_term(&items[i + 1], tyvars, self_name, self_tyvars, ctx)?;
                    pairs.push((fname, v));
                    i += 2;
                }
                pairs.sort_by(|a, b| a.0.as_bytes().cmp(b.0.as_bytes()));
                for w in pairs.windows(2) {
                    if w[0].0 == w[1].0 {
                        return hard(format!("duplicate record field: {}", w[0].0));
                    }
                }
                let names = pairs.iter().map(|p| p.0.clone()).collect();
                let args = pairs.into_iter().map(|p| p.1).collect();
                Ok(Term::Record { names, args })
            }
            Sexpr::List(items) => {
                if items.is_empty() {
                    return hard("empty application".into());
                }
                if let Sexpr::Sym(head) = &items[0] {
                    let head = head.as_str();
                    if PRIMS.contains(&head) {
                        let mut args = Vec::new();
                        for a in &items[1..] {
                            args.push(self.elab_term(a, tyvars, self_name, self_tyvars, ctx)?);
                        }
                        return Ok(Term::Prim { op: head.to_string(), args });
                    }
                    match head {
                        "fn" => return self.elab_fn(items, tyvars, self_name, self_tyvars, ctx),
                        "let" => return self.elab_let(items, tyvars, self_name, self_tyvars, ctx),
                        "if" => {
                            if items.len() != 4 {
                                return hard("if needs three arguments".into());
                            }
                            let a = self.elab_term(&items[1], tyvars, self_name, self_tyvars, ctx)?;
                            let b = self.elab_term(&items[2], tyvars, self_name, self_tyvars, ctx)?;
                            let c = self.elab_term(&items[3], tyvars, self_name, self_tyvars, ctx)?;
                            return Ok(Term::If {
                                a: Box::new(a),
                                b: Box::new(b),
                                c: Box::new(c),
                            });
                        }
                        "match" => {
                            return self.elab_match(items, tyvars, self_name, self_tyvars, ctx)
                        }
                        "list" => {
                            // LIST-LITERAL SUGAR (SPEC §1.4): `(list e0 … en)`
                            // desugars to `(Cons e0 (Cons e1 … (Cons en (Nil))))`
                            // with omitted (inferred) type arguments.
                            return self.elab_list(
                                &items[1..],
                                tyvars,
                                self_name,
                                self_tyvars,
                                ctx,
                            );
                        }
                        "." => {
                            if items.len() != 3 {
                                return hard("(. expr field) needs two arguments".into());
                            }
                            let a = self.elab_term(&items[1], tyvars, self_name, self_tyvars, ctx)?;
                            let field = match &items[2] {
                                Sexpr::Sym(n) => n.clone(),
                                _ => return hard("field name must be a symbol".into()),
                            };
                            return Ok(Term::Field { a: Box::new(a), op: field });
                        }
                        _ => {}
                    }
                    // named application. `tyarg_opt` is None when the `[types]`
                    // bracket was OMITTED (deferred to the checker's inference);
                    // Some(v) when present (its count is validated here).
                    let (tyarg_opt, arg_start) = self.parse_tyargs(items, tyvars)?;
                    let arg_exprs = &items[arg_start..];
                    // A present bracket with a WRONG count is an error; an absent
                    // bracket yields empty type arguments the checker will infer.
                    let checked = |present: Option<Vec<Ty>>, want: u32, what: &str, nm: &str| -> ER<Vec<Ty>> {
                        match present {
                            Some(v) => {
                                if v.len() as u32 != want {
                                    return hard(format!(
                                        "{} {} expects {} type arguments, got {}",
                                        what, nm, want, v.len()
                                    ));
                                }
                                Ok(v)
                            }
                            None => Ok(Vec::new()),
                        }
                    };
                    // resolution order: local var, self, constructor, stored fn
                    if let Some(i) = Self::lookup_var(ctx, head) {
                        if tyarg_opt.is_some() {
                            return hard("type arguments applied to a variable".into());
                        }
                        let mut acc = Term::Var(i);
                        for a in arg_exprs {
                            let at =
                                self.elab_term(a, tyvars, self_name, self_tyvars, ctx)?;
                            acc = Term::App { a: Box::new(acc), b: Box::new(at) };
                        }
                        return Ok(acc);
                    }
                    if head == self_name {
                        let tyargs = checked(tyarg_opt, self_tyvars, "self reference", head)?;
                        let mut acc = Term::SelfRef { tyargs };
                        for a in arg_exprs {
                            let at =
                                self.elab_term(a, tyvars, self_name, self_tyvars, ctx)?;
                            acc = Term::App { a: Box::new(acc), b: Box::new(at) };
                        }
                        return Ok(acc);
                    }
                    if let Some((hash, idx, dtyvars)) = self.lookup_ctor(head)? {
                        let tyargs = checked(tyarg_opt, dtyvars, "constructor", head)?;
                        let mut args = Vec::new();
                        for a in arg_exprs {
                            args.push(self.elab_term(a, tyvars, self_name, self_tyvars, ctx)?);
                        }
                        return Ok(Term::Ctor { hash, idx, tyargs, args });
                    }
                    if let Some(fi) = self.func_by_name.get(head) {
                        let tyargs = checked(tyarg_opt, fi.tyvars, "function", head)?;
                        let mut acc = Term::Ref { hash: fi.hash.clone(), tyargs };
                        for a in arg_exprs {
                            let at =
                                self.elab_term(a, tyvars, self_name, self_tyvars, ctx)?;
                            acc = Term::App { a: Box::new(acc), b: Box::new(at) };
                        }
                        return Ok(acc);
                    }
                    if self.all_func_names.contains(head) {
                        return Err(EErr::Defer(head.to_string()));
                    }
                    hard(format!("unknown name in application: {}", head))
                } else {
                    // head is a compound term (e.g. field projection)
                    let mut acc =
                        self.elab_term(&items[0], tyvars, self_name, self_tyvars, ctx)?;
                    for a in &items[1..] {
                        let at = self.elab_term(a, tyvars, self_name, self_tyvars, ctx)?;
                        acc = Term::App { a: Box::new(acc), b: Box::new(at) };
                    }
                    Ok(acc)
                }
            }
            _ => hard("invalid term syntax".into()),
        }
    }

    /// STRING-LITERAL SUGAR (SPEC §1.4). `"…"` becomes the `Str` codepoint chain
    /// `(SCons c0 (SCons c1 … (SNil)))`, where each `cᵢ` is the Unicode scalar
    /// value of the i-th codepoint; `""` is `(SNil)`. `SNil`/`SCons` must be in
    /// scope. `Str` has no type parameters, so the constructors carry no tyargs.
    fn elab_string_literal(&self, v: &str) -> ER<Term> {
        let (snil_hash, snil_idx, _) = match self.lookup_ctor("SNil")? {
            Some(x) => x,
            None => return hard("string literal requires the `SNil` constructor in scope".into()),
        };
        let (scons_hash, scons_idx, _) = match self.lookup_ctor("SCons")? {
            Some(x) => x,
            None => {
                return hard("string literal requires the `SCons` constructor in scope".into())
            }
        };
        let mut acc = Term::Ctor { hash: snil_hash, idx: snil_idx, tyargs: vec![], args: vec![] };
        for c in v.chars().rev() {
            acc = Term::Ctor {
                hash: scons_hash.clone(),
                idx: scons_idx,
                tyargs: vec![],
                args: vec![Term::Int(num_bigint::BigInt::from(c as u32)), acc],
            };
        }
        Ok(acc)
    }

    /// LIST-LITERAL SUGAR (SPEC §1.4). `(list e0 … en)` becomes the `Cons`/`Nil`
    /// chain with every constructor's type arguments OMITTED (inferred, §2.1);
    /// `(list)` is `(Nil)`. `Nil` and `Cons` must be in scope.
    fn elab_list(
        &self,
        elems: &[Sexpr],
        tyvars: &[String],
        self_name: &str,
        self_tyvars: u32,
        ctx: &mut Vec<String>,
    ) -> ER<Term> {
        let (nil_hash, nil_idx, _) = match self.lookup_ctor("Nil")? {
            Some(x) => x,
            None => return hard("list literal requires the `Nil` constructor in scope".into()),
        };
        let (cons_hash, cons_idx, _) = match self.lookup_ctor("Cons")? {
            Some(x) => x,
            None => return hard("list literal requires the `Cons` constructor in scope".into()),
        };
        // Elaborate elements in source order, then fold into the chain from the
        // tail so `e0` is the outermost `Cons`.
        let mut terms = Vec::with_capacity(elems.len());
        for e in elems {
            terms.push(self.elab_term(e, tyvars, self_name, self_tyvars, ctx)?);
        }
        let mut acc = Term::Ctor { hash: nil_hash, idx: nil_idx, tyargs: vec![], args: vec![] };
        for et in terms.into_iter().rev() {
            acc = Term::Ctor {
                hash: cons_hash.clone(),
                idx: cons_idx,
                tyargs: vec![],
                args: vec![et, acc],
            };
        }
        Ok(acc)
    }

    /// Detect a leading `[...]` type-argument bracket in an application. Returns
    /// `(Some(types), 2)` when the bracket is PRESENT (its count is validated by
    /// the caller against the target's `tyvars`), or `(None, 1)` when it is
    /// ABSENT — in which case the type arguments are OMITTED and left for the
    /// checker to INFER and BACKFILL (SPEC §2.1). An empty bracket `[]` is still a
    /// present bracket of count 0.
    fn parse_tyargs(&self, items: &[Sexpr], tyvars: &[String]) -> ER<(Option<Vec<Ty>>, usize)> {
        if items.len() >= 2 {
            if let Sexpr::Brack(ts) = &items[1] {
                let mut out = Vec::new();
                for t in ts {
                    out.push(self.elab_type(t, tyvars, None)?);
                }
                return Ok((Some(out), 2));
            }
        }
        Ok((None, 1))
    }

    fn elab_fn(
        &self,
        items: &[Sexpr],
        tyvars: &[String],
        self_name: &str,
        self_tyvars: u32,
        ctx: &mut Vec<String>,
    ) -> ER<Term> {
        if items.len() != 3 {
            return hard("(fn [params] body) needs a param list and a body".into());
        }
        let params = match &items[1] {
            Sexpr::Brack(p) => p,
            _ => return hard("fn parameters must be in []".into()),
        };
        let mut ptys = Vec::new();
        let mut pnames = Vec::new();
        for p in params {
            let (n, t) = self.parse_binder(p, tyvars)?;
            pnames.push(n);
            ptys.push(t);
        }
        let pushed = pnames.len();
        for n in &pnames {
            ctx.push(n.clone());
        }
        let body = self.elab_term(&items[2], tyvars, self_name, self_tyvars, ctx);
        for _ in 0..pushed {
            ctx.pop();
        }
        let mut acc = body?;
        for t in ptys.into_iter().rev() {
            acc = Term::Lam { ty: t, a: Box::new(acc) };
        }
        Ok(acc)
    }

    fn elab_let(
        &self,
        items: &[Sexpr],
        tyvars: &[String],
        self_name: &str,
        self_tyvars: u32,
        ctx: &mut Vec<String>,
    ) -> ER<Term> {
        if items.len() != 3 {
            return hard("(let (x ty expr) body) malformed".into());
        }
        let bind = match &items[1] {
            Sexpr::List(b) if b.len() == 3 => b,
            _ => return hard("let binding must be (name type expr)".into()),
        };
        let name = match &bind[0] {
            Sexpr::Sym(n) => n.clone(),
            _ => return hard("let variable must be a symbol".into()),
        };
        let ty = self.elab_type(&bind[1], tyvars, None)?;
        let a = self.elab_term(&bind[2], tyvars, self_name, self_tyvars, ctx)?;
        ctx.push(name);
        let b = self.elab_term(&items[2], tyvars, self_name, self_tyvars, ctx);
        ctx.pop();
        let b = b?;
        Ok(Term::Let { ty, a: Box::new(a), b: Box::new(b) })
    }

    fn elab_match(
        &self,
        items: &[Sexpr],
        tyvars: &[String],
        self_name: &str,
        self_tyvars: u32,
        ctx: &mut Vec<String>,
    ) -> ER<Term> {
        if items.len() < 2 {
            return hard("match needs a scrutinee".into());
        }
        let scrut = self.elab_term(&items[1], tyvars, self_name, self_tyvars, ctx)?;
        // parse arm clauses
        struct Arm {
            idx: u32,
            fields: Vec<String>,
            body: Sexpr,
        }
        let mut data_hash: Option<String> = None;
        let mut arms: Vec<Arm> = Vec::new();
        for clause in &items[2..] {
            let parts = match clause {
                Sexpr::List(p) if p.len() == 2 => p,
                _ => return hard("match arm must be (pattern body)".into()),
            };
            let pat = match &parts[0] {
                Sexpr::List(p) if !p.is_empty() => p,
                _ => return hard("match pattern must be (Ctor fields...)".into()),
            };
            let cname = match &pat[0] {
                Sexpr::Sym(n) => n.clone(),
                _ => return hard("constructor name must be a symbol".into()),
            };
            let mut fields = Vec::new();
            for f in &pat[1..] {
                match f {
                    Sexpr::Sym(n) => fields.push(n.clone()),
                    _ => return hard("pattern field must be a symbol".into()),
                }
            }
            let (hash, idx, _) = match self.lookup_ctor(&cname)? {
                Some(c) => c,
                None => return hard(format!("unknown constructor in match: {}", cname)),
            };
            match &data_hash {
                None => data_hash = Some(hash.clone()),
                Some(h) if *h != hash => {
                    return hard("match arms span more than one ADT".into())
                }
                _ => {}
            }
            if arms.iter().any(|a| a.idx == idx) {
                return hard(format!("duplicate match arm for constructor {}", cname));
            }
            arms.push(Arm { idx, fields, body: parts[1].clone() });
        }
        let hash = match data_hash {
            Some(h) => h,
            None => return hard("match has no arms".into()),
        };
        let di = self
            .data_by_hash
            .get(&hash)
            .expect("resolved ctor implies resolved data");
        // exhaustiveness: exactly one arm per constructor
        if arms.len() != di.ctors.len() {
            return hard("non-exhaustive match".into());
        }
        for k in 0..di.ctors.len() as u32 {
            if !arms.iter().any(|a| a.idx == k) {
                return hard("non-exhaustive match".into());
            }
        }
        // order by constructor index
        arms.sort_by_key(|a| a.idx);
        let mut out_arms = Vec::new();
        for arm in &arms {
            let arity = di.ctors[arm.idx as usize].1.len();
            if arm.fields.len() != arity {
                return hard(format!(
                    "constructor {} bound with {} fields, expected {}",
                    di.ctors[arm.idx as usize].0,
                    arm.fields.len(),
                    arity
                ));
            }
            let pushed = arm.fields.len();
            for f in &arm.fields {
                ctx.push(f.clone());
            }
            let body = self.elab_term(&arm.body, tyvars, self_name, self_tyvars, ctx);
            for _ in 0..pushed {
                ctx.pop();
            }
            out_arms.push(body?);
        }
        Ok(Term::Match { hash, a: Box::new(scrut), arms: out_arms })
    }

    fn parse_binder(&self, s: &Sexpr, tyvars: &[String]) -> ER<(String, Ty)> {
        let parts = match s {
            Sexpr::List(p) if p.len() == 2 => p,
            _ => return hard("binder must be (name type)".into()),
        };
        let name = match &parts[0] {
            Sexpr::Sym(n) => n.clone(),
            _ => return hard("binder name must be a symbol".into()),
        };
        let ty = self.elab_type(&parts[1], tyvars, None)?;
        Ok((name, ty))
    }

    // ---- top-level forms ----

    fn try_elab_form(&self, form: &Sexpr) -> ER<Elaborated> {
        let items = match form {
            Sexpr::List(i) if !i.is_empty() => i,
            _ => return hard("top-level form must be a list".into()),
        };
        let head = match &items[0] {
            Sexpr::Sym(h) => h.as_str(),
            _ => return hard("top-level head must be a symbol".into()),
        };
        match head {
            "data" => self.elab_data(items),
            "defn" => self.elab_defn(items),
            _ => hard(format!("unknown top-level form: {}", head)),
        }
    }

    fn elab_data(&self, items: &[Sexpr]) -> ER<Elaborated> {
        // (data Name [tyvars] ctor...)
        if items.len() < 3 {
            return hard("data needs a name, tyvars, and constructors".into());
        }
        let name = match &items[1] {
            Sexpr::Sym(n) => n.clone(),
            _ => return hard("data name must be a symbol".into()),
        };
        let tyvar_names = match &items[2] {
            Sexpr::Brack(v) => symbols(v)?,
            _ => return hard("data tyvars must be a []".into()),
        };
        let mut ctors: Vec<(String, Vec<Ty>)> = Vec::new();
        for c in &items[3..] {
            let parts = match c {
                Sexpr::List(p) if !p.is_empty() => p,
                _ => return hard("constructor must be (Name fields...)".into()),
            };
            let cname = match &parts[0] {
                Sexpr::Sym(n) => n.clone(),
                _ => return hard("constructor name must be a symbol".into()),
            };
            let mut fields = Vec::new();
            for f in &parts[1..] {
                fields.push(self.elab_type(f, &tyvar_names, Some(name.as_str()))?);
            }
            ctors.push((cname, fields));
        }
        let def = Def::Data {
            tyvars: tyvar_names.len() as u32,
            ctors: ctors.iter().map(|(_, f)| f.clone()).collect(),
        };
        let hash = sha256_hex(&canonical_bytes(&def));
        let info =
            DataInfo { hash: hash.clone(), name: name.clone(), tyvars: tyvar_names.len() as u32, ctors };
        Ok(Elaborated::Data { name, def, info })
    }

    fn elab_defn(&self, items: &[Sexpr]) -> ER<Elaborated> {
        // (defn name [tyvars] [(param ty)...] ret body prop...)
        if items.len() < 6 {
            return hard("defn needs name, tyvars, params, return type, body".into());
        }
        let name = match &items[1] {
            Sexpr::Sym(n) => n.clone(),
            _ => return hard("defn name must be a symbol".into()),
        };
        let tyvar_names = match &items[2] {
            Sexpr::Brack(v) => symbols(v)?,
            _ => return hard("defn tyvars must be a []".into()),
        };
        let params = match &items[3] {
            Sexpr::Brack(v) => v,
            _ => return hard("defn parameters must be a []".into()),
        };
        let mut pnames = Vec::new();
        let mut ptys = Vec::new();
        for p in params {
            let (n, t) = self.parse_binder(p, &tyvar_names)?;
            pnames.push(n);
            ptys.push(t);
        }
        let ret = self.elab_type(&items[4], &tyvar_names, None)?;
        // def type: fold params into arrows over return type
        let mut ty = ret;
        for t in ptys.iter().rev() {
            ty = Ty::Fun(Box::new(t.clone()), Box::new(ty));
        }
        let tyvars_count = tyvar_names.len() as u32;
        // body
        let mut ctx: Vec<String> = pnames.clone();
        let body = self.elab_term(&items[5], &tyvar_names, &name, tyvars_count, &mut ctx)?;
        let mut fun_body = body;
        for t in ptys.iter().rev() {
            fun_body = Term::Lam { ty: t.clone(), a: Box::new(fun_body) };
        }
        // props
        let mut props = Vec::new();
        let mut prop_names = Vec::new();
        for pform in &items[6..] {
            prop_names.push(prop_name(pform)?);
            props.push(self.elab_prop(pform, &tyvar_names, &name, tyvars_count)?);
        }
        let mut def = Def::Func { tyvars: tyvars_count, ty: ty.clone(), body: fun_body, props };
        // Type-check and BACKFILL inferred type arguments (SPEC §2.1) BEFORE
        // hashing, so an inferred call has the identity of its explicit form. A
        // type error rejects the definition (it must not be stored).
        crate::check::check_and_backfill(self, &mut def).map_err(EErr::Hard)?;
        let hash = sha256_hex(&canonical_bytes(&def));
        let info = FuncInfo {
            hash: hash.clone(),
            name: name.clone(),
            tyvars: tyvars_count,
            ty,
            prop_names,
            param_names: pnames.clone(),
        };
        Ok(Elaborated::Func { name, def, info })
    }

    fn elab_prop(
        &self,
        form: &Sexpr,
        tyvars: &[String],
        self_name: &str,
        self_tyvars: u32,
    ) -> ER<Prop> {
        let items = match form {
            Sexpr::List(i) if i.len() == 4 => i,
            _ => return hard("prop must be (prop name [binders] body)".into()),
        };
        match &items[0] {
            Sexpr::Sym(k) if k == "prop" => {}
            _ => return hard("expected prop".into()),
        }
        let binder_forms = match &items[2] {
            Sexpr::Brack(v) => v,
            _ => return hard("prop binders must be a []".into()),
        };
        let mut binders = Vec::new();
        let mut names = Vec::new();
        for b in binder_forms {
            let (n, t) = self.parse_binder(b, tyvars)?;
            names.push(n);
            binders.push(t);
        }
        let mut ctx = names;
        let body = self.elab_term(&items[3], tyvars, self_name, self_tyvars, &mut ctx)?;
        Ok(Prop { binders, body })
    }

    fn register(&mut self, e: Elaborated) {
        match e {
            Elaborated::Data { name, def, info } => {
                self.data_by_name.insert(name.clone(), info.clone());
                self.data_by_hash.insert(info.hash.clone(), info.clone());
                self.def_by_hash.insert(info.hash.clone(), def.clone());
                self.def_by_name.insert(name.clone(), def);
                self.order.push(name);
            }
            Elaborated::Func { name, def, info } => {
                self.func_by_name.insert(name.clone(), info.clone());
                self.func_by_hash.insert(info.hash.clone(), info.clone());
                self.def_by_hash.insert(info.hash.clone(), def.clone());
                self.def_by_name.insert(name.clone(), def);
                self.order.push(name);
            }
        }
    }
}

fn prop_name(form: &Sexpr) -> ER<String> {
    match form {
        Sexpr::List(i) if i.len() == 4 => match &i[1] {
            Sexpr::Sym(n) => Ok(n.clone()),
            _ => hard("prop name must be a symbol".into()),
        },
        _ => hard("prop must be (prop name [binders] body)".into()),
    }
}

fn symbols(items: &[Sexpr]) -> ER<Vec<String>> {
    let mut out = Vec::new();
    for i in items {
        match i {
            Sexpr::Sym(s) => out.push(s.clone()),
            _ => return hard("expected a symbol".into()),
        }
    }
    Ok(out)
}

/// Parse every file, then elaborate all forms to a fixpoint.
pub fn elaborate_corpus(files: &[(String, String)]) -> Result<Store, String> {
    let mut forms: Vec<Sexpr> = Vec::new();
    for (fname, src) in files {
        let mut r = Reader::new(src);
        let fs = r
            .read_all()
            .map_err(|e| format!("{}: {}", fname, e))?;
        forms.extend(fs);
    }

    let mut store = Store::default();
    // Pre-scan names for deferral detection.
    for f in &forms {
        if let Sexpr::List(items) = f {
            if items.len() >= 2 {
                if let (Sexpr::Sym(head), Sexpr::Sym(nm)) = (&items[0], &items[1]) {
                    match head.as_str() {
                        "data" => {
                            store.all_data_names.insert(nm.clone());
                            for c in &items[3.min(items.len())..] {
                                if let Sexpr::List(cp) = c {
                                    if let Some(Sexpr::Sym(cn)) = cp.first() {
                                        store.all_ctor_names.insert(cn.clone());
                                    }
                                }
                            }
                        }
                        "defn" => {
                            store.all_func_names.insert(nm.clone());
                        }
                        _ => {}
                    }
                }
            }
        }
    }

    let mut pending: Vec<usize> = (0..forms.len()).collect();
    loop {
        let mut progressed = false;
        let mut still: Vec<usize> = Vec::new();
        let mut last_defer: Option<String> = None;
        for &i in &pending {
            match store.try_elab_form(&forms[i]) {
                Ok(e) => {
                    store.register(e);
                    progressed = true;
                }
                Err(EErr::Defer(n)) => {
                    last_defer = Some(n);
                    still.push(i);
                }
                Err(EErr::Hard(m)) => return Err(m),
            }
        }
        if still.is_empty() {
            break;
        }
        if !progressed {
            return Err(format!(
                "cannot resolve {} definition(s); last blocked on '{}'",
                still.len(),
                last_defer.unwrap_or_default()
            ));
        }
        pending = still;
    }
    Ok(store)
}
