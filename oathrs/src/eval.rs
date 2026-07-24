//! Strict, left-to-right evaluator (SPEC §3) with both resource bounds
//! (§3.1). Fuel: 1 per term-node evaluation and 1 per function application.
//! Depth: nested evaluation deeper than 100,000 is an error.

use crate::elaborate::Store;
use crate::ir::{Def, Term};
use crate::value::{struct_eq, Native, Value};
use num_bigint::{BigInt, BigUint, Sign};

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
            Term::Float(bits) => Ok(Value::Float(*bits)),
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
    // `fp-eq` is the Float-only IEEE-754 equality (SPEC §3): `NaN != NaN`,
    // `+0.0 == -0.0` — distinct from structural `==` (which is bitwise Leibniz).
    if op == "fp-eq" {
        return match (&vs[0], &vs[1]) {
            (Value::Float(a), Value::Float(b)) => {
                Ok(Value::Bool(f64::from_bits(*a) == f64::from_bits(*b)))
            }
            _ => Err("fp-eq expects float operands".into()),
        };
    }
    // Numeric conversions (SPEC §3): unary, dispatch on the operand's runtime
    // kind. The NaN/inf faults on a `Float` source follow the division-by-zero
    // idiom — the narrowing has no answer, so it is a runtime error.
    match op {
        // to-rat: Int -> Rat exact (n ↦ n/1); Float -> Rat exact for a finite
        // binary64 (a dyadic rational), a runtime error on NaN/±inf.
        "to-rat" => {
            return match &vs[0] {
                Value::Int(n) => Ok(Value::Rat(n.clone(), BigInt::from(1))),
                Value::Float(bits) => {
                    let (num, den) = float_to_exact(*bits)
                        .ok_or("to-rat of a non-finite Float")?;
                    // den is a power of two (> 0); reduce to canonical form.
                    match crate::ir::reduce_rat(num, den) {
                        Some((n, d)) => Ok(Value::Rat(n, d)),
                        None => Err("to-rat produced an invalid rational".into()),
                    }
                }
                _ => Err("to-rat expects an Int or Float operand".into()),
            };
        }
        // to-float: Int|Rat -> Float, round to nearest binary64, ties to even.
        "to-float" => {
            return match &vs[0] {
                Value::Int(n) => Ok(mk_float(rat_to_f64(n, &BigInt::from(1)))),
                Value::Rat(n, d) => Ok(mk_float(rat_to_f64(n, d))),
                _ => Err("to-float expects an Int or Rat operand".into()),
            };
        }
        // floor: Rat|Float -> Int, rounding toward −∞. Rat is total; Float is a
        // runtime error on NaN/±inf.
        "floor" => {
            return match &vs[0] {
                Value::Rat(n, d) => Ok(Value::Int(floor_div(n, d))),
                Value::Float(bits) => {
                    let (num, den) = float_to_exact(*bits)
                        .ok_or("floor of a non-finite Float")?;
                    Ok(Value::Int(floor_div(&num, &den)))
                }
                _ => Err("floor expects a Rat or Float operand".into()),
            };
        }
        _ => {}
    }
    // Numeric-overloaded primitives (SPEC §3): `+ - * / neg < <=` dispatch on the
    // runtime kind of the (first) operand. The gate guarantees both operands
    // share one kind, so a `Rat` first operand means a rational operation and a
    // `Float` first operand means an IEEE-754 operation.
    if matches!(vs.first(), Some(Value::Rat(_, _)))
        && matches!(op, "+" | "-" | "*" | "/" | "neg" | "<" | "<=")
    {
        return eval_rat_prim(op, vs);
    }
    if matches!(vs.first(), Some(Value::Float(_)))
        && matches!(op, "+" | "-" | "*" | "/" | "neg" | "<" | "<=")
    {
        return eval_float_prim(op, vs);
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

/// IEEE-754 binary64 arithmetic (SPEC §3). Operands are canonicalized bit
/// patterns. `+ - * / neg` round nearest-ties-even (Rust's default f64 rounding)
/// and are TOTAL — `/` by zero yields `±inf` (`0.0/0.0` yields NaN), never an
/// error — with NaN canonicalized on every result. `< <=` are IEEE ordered (a
/// NaN operand yields `false`, which Rust's `<`/`<=` on `f64` already give).
fn eval_float_prim(op: &str, vs: &[Value]) -> Result<Value, String> {
    let fl = |v: &Value| -> Result<f64, String> {
        match v {
            Value::Float(b) => Ok(f64::from_bits(*b)),
            _ => Err("expected float operand".into()),
        }
    };
    // Canonicalize NaN on every produced value (SPEC §1.3/§3).
    let mk = |r: f64| Value::Float(crate::ir::canon_f64_bits(r.to_bits()));
    match op {
        "neg" => Ok(mk(-fl(&vs[0])?)),
        "+" => Ok(mk(fl(&vs[0])? + fl(&vs[1])?)),
        "-" => Ok(mk(fl(&vs[0])? - fl(&vs[1])?)),
        "*" => Ok(mk(fl(&vs[0])? * fl(&vs[1])?)),
        "/" => Ok(mk(fl(&vs[0])? / fl(&vs[1])?)),
        "<" => Ok(Value::Bool(fl(&vs[0])? < fl(&vs[1])?)),
        "<=" => Ok(Value::Bool(fl(&vs[0])? <= fl(&vs[1])?)),
        _ => Err(format!("unknown float primitive {}", op)),
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

/// Canonicalize a finite `f64` into a `Float` value (NaN never arises here, but
/// the canonicalization keeps the construction identical to every other float).
fn mk_float(r: f64) -> Value {
    Value::Float(crate::ir::canon_f64_bits(r.to_bits()))
}

/// Floor division toward −∞ (SPEC §3), `d > 0`. num-bigint `/` truncates toward
/// zero and `%` takes the dividend's sign, so a negative remainder means the
/// truncated quotient overshot upward by one — e.g. `floor(-7/2) = -4`.
fn floor_div(n: &BigInt, d: &BigInt) -> BigInt {
    let q = n / d;
    let r = n % d;
    if r.sign() == Sign::Minus {
        q - 1
    } else {
        q
    }
}

/// Decompose a finite binary64 into the EXACT dyadic rational `(num, den)` it
/// denotes, with `den` a positive power of two (SPEC §3: every finite float is a
/// dyadic rational). Returns `None` for NaN or ±inf — the callers turn that into
/// the division-by-zero-style runtime error.
fn float_to_exact(bits: u64) -> Option<(BigInt, BigInt)> {
    let exp_field = ((bits >> 52) & 0x7FF) as i64;
    let frac = bits & 0x000F_FFFF_FFFF_FFFF;
    if exp_field == 0x7FF {
        // NaN or ±inf — no rational.
        return None;
    }
    let neg = (bits >> 63) & 1 == 1;
    // Significand and unbiased power of two: normal numbers carry the implicit
    // leading bit and bias 1075 (= 1023 + 52); subnormals (exp_field == 0) have
    // no implicit bit and a fixed exponent of −1074.
    let (mant, e) = if exp_field == 0 {
        (BigUint::from(frac), -1074i64)
    } else {
        (BigUint::from((1u64 << 52) | frac), exp_field - 1075)
    };
    let mut num = BigInt::from_biguint(if neg { Sign::Minus } else { Sign::Plus }, mant);
    if num.sign() == Sign::NoSign {
        return Some((BigInt::from(0), BigInt::from(1)));
    }
    // value = num · 2^e.
    let (num, den) = if e >= 0 {
        num <<= e as u64;
        (num, BigInt::from(1))
    } else {
        (num, BigInt::from(1) << ((-e) as u64))
    };
    Some((num, den))
}

/// Round an exact rational `num/den` (`den > 0`, any sign on `num`) to the
/// nearest binary64, ties to even (SPEC §3). Correctly rounded across the whole
/// normal range, subnormals, and overflow (→ ±inf) — a genuine reading of "round
/// to nearest binary64", not a double-rounding shortcut through `f64` division.
fn rat_to_f64(num: &BigInt, den: &BigInt) -> f64 {
    if num.sign() == Sign::NoSign {
        return 0.0;
    }
    let neg = num.sign() == Sign::Minus;
    let n: BigUint = num.magnitude().clone();
    let d: BigUint = den.magnitude().clone();
    // Estimate the binary exponent of n/d; `bits()` gives the highest set bit
    // position, so bits(n) − bits(d) is within ±1 of floor(log2(n/d)).
    let e2 = n.bits() as i64 - d.bits() as i64;
    // Scale so the floored quotient q has at least 54 bits (53 significand + a
    // guard bit). Aiming high guarantees q ≥ 2^53, so the only normalization is
    // lossless right-shifting (accumulating a sticky bit) — never a lossy left
    // shift after flooring.
    let shift = 54 - e2;
    let (scaled_num, scaled_den) = if shift >= 0 {
        (n << (shift as u64), d)
    } else {
        (n, d << ((-shift) as u64))
    };
    let mut q = &scaled_num / &scaled_den;
    let rem = &scaled_num % &scaled_den;
    let mut sticky = rem != BigUint::from(0u32);
    // value ≈ q · 2^base_exp.
    let mut base_exp = -shift;
    // Normalize down to q ∈ [2^53, 2^54): bit 0 becomes the guard bit.
    let top = BigUint::from(1u32) << 54;
    let one = BigUint::from(1u32);
    while q >= top {
        if (&q & &one) != BigUint::from(0u32) {
            sticky = true;
        }
        q >>= 1;
        base_exp += 1;
    }
    // Split guard bit off; `m` is the 53-bit significand candidate.
    let guard = (&q & &one) != BigUint::from(0u32);
    let mut m: u64 = (&q >> 1u32)
        .to_u64_digits()
        .first()
        .copied()
        .unwrap_or(0);
    // Reconstruct the low 64 bits precisely (m is < 2^53, so one digit suffices).
    let mut exp = base_exp + 1; // value ≈ m · 2^exp, m ∈ [2^52, 2^53)
    // Round to nearest, ties to even.
    if guard && (sticky || (m & 1) == 1) {
        m += 1;
        if m == (1u64 << 53) {
            m = 1u64 << 52;
            exp += 1;
        }
    }
    // value = m · 2^exp with m ∈ [2^52, 2^53): the unbiased exponent of the
    // leading bit is exp + 52.
    let unbiased = exp + 52;
    let result = if unbiased > 1023 {
        // Overflow → ±inf.
        f64::INFINITY
    } else if unbiased >= -1022 {
        // Normal: drop the implicit leading bit, bias the exponent.
        let biased = (unbiased + 1023) as u64;
        let mantissa = m - (1u64 << 52);
        f64::from_bits((biased << 52) | mantissa)
    } else {
        // Subnormal / underflow: re-round the significand to the reduced
        // precision available at 2^−1074. `m · 2^exp` = `m · 2^(unbiased−52)`;
        // shift the significand right by the shortfall, rounding ties to even.
        let drop = (-1022 - unbiased) as u32; // ≥ 1 extra bits to remove
        if drop >= 53 {
            // Rounds to 0 (or the smallest subnormal on a boundary tie).
            let boundary = drop == 53 && m == (1u64 << 52) && !guard && !sticky;
            if boundary {
                0.0 // exact half of the smallest subnormal → ties to even → 0
            } else if drop == 53 {
                // more than half of the smallest subnormal rounds up to it
                f64::from_bits(1)
            } else {
                0.0
            }
        } else {
            let lost_mask = (1u64 << drop) - 1;
            let lost = m & lost_mask;
            let mut sub = m >> drop;
            let half = 1u64 << (drop - 1);
            if lost > half || (lost == half && ((sub & 1) == 1 || sticky || guard)) {
                sub += 1;
            }
            f64::from_bits(sub)
        }
    };
    if neg {
        -result
    } else {
        result
    }
}
