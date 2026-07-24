//! Strict, left-to-right evaluator (SPEC §3) with both resource bounds
//! (§3.1). Fuel: 1 per term-node evaluation and 1 per function application.
//! Depth: nested evaluation deeper than 100,000 is an error.

use crate::elaborate::Store;
use crate::ir::{Def, Term};
use crate::value::{struct_eq, Native, Value};
use num_bigint::BigInt;

pub const DEPTH_MSG: &str = "recursion too deep (likely non-termination)";
pub const FUEL_MSG: &str = "out of fuel";
const MAX_DEPTH: i32 = 100_000;

pub struct Machine<'a> {
    pub store: &'a Store,
    pub fuel: i64,
    pub depth: i32,
}

impl<'a> Machine<'a> {
    pub fn new(store: &'a Store, fuel: i64) -> Self {
        Machine { store, fuel, depth: 0 }
    }

    fn burn(&mut self) -> Result<(), String> {
        self.fuel -= 1;
        if self.fuel < 0 {
            return Err(FUEL_MSG.to_string());
        }
        Ok(())
    }

    pub fn eval(&mut self, t: &Term, env: &mut Vec<Value>, self_hash: &str) -> Result<Value, String> {
        self.burn()?;
        self.depth += 1;
        if self.depth > MAX_DEPTH {
            self.depth -= 1;
            return Err(DEPTH_MSG.to_string());
        }
        let r = self.eval_inner(t, env, self_hash);
        self.depth -= 1;
        r
    }

    fn eval_body_of(&mut self, hash: &str) -> Result<Value, String> {
        // ref/self re-evaluate the referenced definition's body at each use.
        let body = match self.store.def_by_hash.get(hash) {
            Some(Def::Func { body, .. }) => body.clone(),
            _ => return Err(format!("no function body for {}", hash)),
        };
        let mut fresh: Vec<Value> = Vec::new();
        self.eval(&body, &mut fresh, hash)
    }

    fn apply(&mut self, f: Value, x: Value) -> Result<Value, String> {
        self.fuel -= 1;
        if self.fuel < 0 {
            return Err(FUEL_MSG.to_string());
        }
        match f {
            Value::Closure { env, body, self_hash } => {
                let mut new_env = env;
                new_env.push(x);
                self.eval(&body, &mut new_env, &self_hash)
            }
            Value::Native(n) => self.apply_native(&n, &x),
            _ => Err("applying a non-function value".into()),
        }
    }

    fn apply_native(&mut self, n: &Native, x: &Value) -> Result<Value, String> {
        match n {
            Native::Identity => Ok(x.clone()),
            Native::Affine(a, b) => match x {
                // `Int` is ℤ: a*x + b in arbitrary precision (no wrapping).
                Value::Int(v) => Ok(Value::Int(BigInt::from(*a) * v + BigInt::from(*b))),
                _ => Err("affine applied to non-int".into()),
            },
            Native::Const(v) => Ok((**v).clone()),
            Native::Table(entries, default) => {
                for (k, val) in entries {
                    if struct_eq(k, x)? {
                        return Ok(val.clone());
                    }
                }
                Ok((**default).clone())
            }
        }
    }

    fn eval_inner(
        &mut self,
        t: &Term,
        env: &mut Vec<Value>,
        self_hash: &str,
    ) -> Result<Value, String> {
        match t {
            Term::Var(i) => {
                let n = env.len();
                let idx = n
                    .checked_sub(1 + *i as usize)
                    .ok_or("variable out of range at runtime")?;
                Ok(env[idx].clone())
            }
            Term::Int(v) => Ok(Value::Int(v.clone())),
            Term::Rat { num, den } => Ok(Value::Rat(num.clone(), den.clone())),
            Term::Bool(b) => Ok(Value::Bool(*b)),
            Term::Lam { a, .. } => Ok(Value::Closure {
                env: env.clone(),
                body: (**a).clone(),
                self_hash: self_hash.to_string(),
            }),
            Term::App { a, b } => {
                let f = self.eval(a, env, self_hash)?;
                let x = self.eval(b, env, self_hash)?;
                self.apply(f, x)
            }
            Term::Let { a, b, .. } => {
                let v = self.eval(a, env, self_hash)?;
                env.push(v);
                let r = self.eval(b, env, self_hash);
                env.pop();
                r
            }
            Term::If { a, b, c } => {
                match self.eval(a, env, self_hash)? {
                    Value::Bool(true) => self.eval(b, env, self_hash),
                    Value::Bool(false) => self.eval(c, env, self_hash),
                    _ => Err("if condition not a bool".into()),
                }
            }
            Term::Prim { op, args } => {
                let mut vs = Vec::with_capacity(args.len());
                for a in args {
                    vs.push(self.eval(a, env, self_hash)?);
                }
                eval_prim(op, &vs)
            }
            Term::Ref { hash, .. } => self.eval_body_of(hash),
            Term::SelfRef { .. } => self.eval_body_of(self_hash),
            Term::Ctor { hash, idx, args, .. } => {
                let mut fields = Vec::with_capacity(args.len());
                for a in args {
                    fields.push(self.eval(a, env, self_hash)?);
                }
                Ok(Value::Data { hash: hash.clone(), idx: *idx, fields })
            }
            Term::Match { a, arms, .. } => {
                let scrut = self.eval(a, env, self_hash)?;
                match scrut {
                    Value::Data { idx, fields, .. } => {
                        let arm = &arms[idx as usize];
                        let pushed = fields.len();
                        for f in fields {
                            env.push(f);
                        }
                        let r = self.eval(arm, env, self_hash);
                        for _ in 0..pushed {
                            env.pop();
                        }
                        r
                    }
                    _ => Err("match on a non-data value".into()),
                }
            }
            Term::Record { names, args } => {
                let mut vals = Vec::with_capacity(args.len());
                for a in args {
                    vals.push(self.eval(a, env, self_hash)?);
                }
                Ok(Value::Record { names: names.clone(), vals })
            }
            Term::Field { a, op } => {
                let v = self.eval(a, env, self_hash)?;
                match v {
                    Value::Record { names, vals } => {
                        match names.iter().position(|n| n == op) {
                            Some(i) => Ok(vals[i].clone()),
                            None => Err("record missing field at runtime".into()),
                        }
                    }
                    _ => Err("field access on non-record".into()),
                }
            }
        }
    }
}

fn eval_prim(op: &str, vs: &[Value]) -> Result<Value, String> {
    // Integers are ℤ (SPEC §3): arbitrary precision, `+ - *` never overflow.
    let int = |v: &Value| -> Result<BigInt, String> {
        match v {
            Value::Int(n) => Ok(n.clone()),
            _ => Err("expected int operand".into()),
        }
    };
    let boolean = |v: &Value| -> Result<bool, String> {
        match v {
            Value::Bool(b) => Ok(*b),
            _ => Err("expected bool operand".into()),
        }
    };
    let zero = BigInt::from(0);
    // Numeric-overloaded primitives (SPEC §3): `+ - * / neg < <=` dispatch on the
    // runtime kind of the (first) operand. The gate guarantees both operands
    // share one kind, so a `Rat` first operand means a rational operation.
    if matches!(vs.first(), Some(Value::Rat(_, _)))
        && matches!(op, "+" | "-" | "*" | "/" | "neg" | "<" | "<=")
    {
        return eval_rat_prim(op, vs);
    }
    match op {
        "+" => Ok(Value::Int(int(&vs[0])? + int(&vs[1])?)),
        "-" => Ok(Value::Int(int(&vs[0])? - int(&vs[1])?)),
        "*" => Ok(Value::Int(int(&vs[0])? * int(&vs[1])?)),
        "/" => {
            let d = int(&vs[1])?;
            if d == zero {
                return Err("division by zero".into());
            }
            // num-bigint `/` truncates toward zero (Quo), matching the spec.
            Ok(Value::Int(int(&vs[0])? / d))
        }
        "%" => {
            let d = int(&vs[1])?;
            if d == zero {
                return Err("modulo by zero".into());
            }
            // num-bigint `%` takes the dividend's sign (Rem), matching the spec.
            Ok(Value::Int(int(&vs[0])? % d))
        }
        "neg" => Ok(Value::Int(-int(&vs[0])?)),
        "==" => Ok(Value::Bool(struct_eq(&vs[0], &vs[1])?)),
        "<" => Ok(Value::Bool(int(&vs[0])? < int(&vs[1])?)),
        "<=" => Ok(Value::Bool(int(&vs[0])? <= int(&vs[1])?)),
        "and" => Ok(Value::Bool(boolean(&vs[0])? && boolean(&vs[1])?)),
        "or" => Ok(Value::Bool(boolean(&vs[0])? || boolean(&vs[1])?)),
        "not" => Ok(Value::Bool(!boolean(&vs[0])?)),
        _ => Err(format!("unknown primitive {}", op)),
    }
}

/// Exact rational arithmetic (SPEC §3). Operands are reduced (num, den) pairs
/// with `den ≥ 1`. Results are re-reduced to canonical form. `/` is exact real
/// division (no truncation); division by zero is a runtime error.
fn eval_rat_prim(op: &str, vs: &[Value]) -> Result<Value, String> {
    let rat = |v: &Value| -> Result<(BigInt, BigInt), String> {
        match v {
            Value::Rat(n, d) => Ok((n.clone(), d.clone())),
            _ => Err("expected rat operand".into()),
        }
    };
    let reduce = |n: BigInt, d: BigInt| -> Result<Value, String> {
        match crate::ir::reduce_rat(n, d) {
            Some((n, d)) => Ok(Value::Rat(n, d)),
            None => Err("division by zero".into()),
        }
    };
    match op {
        "neg" => {
            let (n, d) = rat(&vs[0])?;
            Ok(Value::Rat(-n, d))
        }
        _ => {
            let (a, b) = rat(&vs[0])?; // a/b
            let (c, d) = rat(&vs[1])?; // c/d
            match op {
                "+" => reduce(&a * &d + &c * &b, &b * &d),
                "-" => reduce(&a * &d - &c * &b, &b * &d),
                "*" => reduce(&a * &c, &b * &d),
                // exact real division: (a/b)/(c/d) = (a*d)/(b*c)
                "/" => reduce(&a * &d, &b * &c),
                // b, d > 0, so ordering is a*d vs c*b
                "<" => Ok(Value::Bool(&a * &d < &c * &b)),
                "<=" => Ok(Value::Bool(&a * &d <= &c * &b)),
                _ => Err(format!("unknown rational primitive {}", op)),
            }
        }
    }
}
