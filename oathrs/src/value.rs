//! Runtime values (SPEC §3.2) and the value printer. The printer is
//! byte-critical for counterexamples, so it is treated like the canonical
//! encoder: implemented by hand from the spec.

use crate::elaborate::Store;
use num_bigint::BigInt;

#[derive(Clone, Debug)]
pub enum Value {
    // `Int` is ℤ — arbitrary precision (SPEC §3).
    Int(BigInt),
    // `Rat` is ℚ — exact, arbitrary precision, always kept in reduced form
    // (den ≥ 1, gcd(|num|,den) = 1, sign on the numerator) (SPEC §3).
    Rat(BigInt, BigInt),
    // `Float` is IEEE-754 binary64 (SPEC §3). The value IS its canonicalized
    // 64-bit pattern; identity/`==` is bit-identity (Leibniz), held as `u64` so
    // equality is exactly bitwise (`NaN == NaN`, `+0.0 != -0.0`).
    Float(u64),
    Bool(bool),
    Data { hash: String, idx: u32, fields: Vec<Value> },
    Record { names: Vec<String>, vals: Vec<Value> },
    Closure { env: Vec<Value>, body: crate::ir::Term, self_hash: String },
    Native(Native),
}

#[derive(Clone, Debug)]
pub enum Native {
    Identity,
    Affine(i64, i64),
    // Per SPEC §3.2; not produced by the §4 generators, kept for the printer.
    #[allow(dead_code)]
    Const(Box<Value>),
    // finite table: (key, value) pairs in generation order, plus default
    Table(Vec<(Value, Value)>, Box<Value>),
}

/// Structural equality (SPEC §3): constructor index and fields recursively;
/// applying it to function values is a runtime error.
pub fn struct_eq(a: &Value, b: &Value) -> Result<bool, String> {
    match (a, b) {
        (Value::Int(x), Value::Int(y)) => Ok(x == y),
        // Rationals are reduced, so structural pair equality is value equality.
        (Value::Rat(nx, dx), Value::Rat(ny, dy)) => Ok(nx == ny && dx == dy),
        // Structural `==` on `Float` is Leibniz (bitwise on canonicalized bits):
        // `NaN == NaN` is true, `+0.0 == -0.0` is false (SPEC §3). This is NOT
        // IEEE equality — that is the separate `fp-eq` primitive.
        (Value::Float(x), Value::Float(y)) => Ok(x == y),
        (Value::Bool(x), Value::Bool(y)) => Ok(x == y),
        (Value::Data { idx: i, fields: fa, .. }, Value::Data { idx: j, fields: fb, .. }) => {
            if i != j || fa.len() != fb.len() {
                return Ok(false);
            }
            for (x, y) in fa.iter().zip(fb.iter()) {
                if !struct_eq(x, y)? {
                    return Ok(false);
                }
            }
            Ok(true)
        }
        (Value::Record { vals: va, .. }, Value::Record { vals: vb, .. }) => {
            if va.len() != vb.len() {
                return Ok(false);
            }
            for (x, y) in va.iter().zip(vb.iter()) {
                if !struct_eq(x, y)? {
                    return Ok(false);
                }
            }
            Ok(true)
        }
        (Value::Closure { .. }, _)
        | (_, Value::Closure { .. })
        | (Value::Native(_), _)
        | (_, Value::Native(_)) => Err("equality on a function value".into()),
        _ => Ok(false),
    }
}

pub fn print_value(store: &Store, v: &Value) -> String {
    let mut s = String::new();
    print_into(store, v, &mut s);
    s
}

fn print_into(store: &Store, v: &Value, out: &mut String) {
    match v {
        Value::Int(n) => out.push_str(&n.to_string()),
        // Rat prints in lowest terms (SPEC §3.2): an integer-valued rational as
        // a bare integer, otherwise `num/den` with the sign on the numerator.
        Value::Rat(num, den) => {
            if *den == BigInt::from(1) {
                out.push_str(&num.to_string());
            } else {
                out.push_str(&num.to_string());
                out.push('/');
                out.push_str(&den.to_string());
            }
        }
        // Float prints with an `f` suffix so it round-trips and is distinct from
        // a Rat (SPEC §3.2): a finite value as its shortest round-tripping decimal
        // (Go's `strconv.FormatFloat(f,'g',-1,64)`) + `f`, specials as
        // `inff`/`-inff`/`nanf`.
        Value::Float(bits) => out.push_str(&format_float(*bits)),
        Value::Bool(b) => out.push_str(if *b { "true" } else { "false" }),
        Value::Record { names, vals } => {
            out.push('{');
            for (i, (n, val)) in names.iter().zip(vals.iter()).enumerate() {
                if i > 0 {
                    out.push(' ');
                }
                out.push_str(n);
                out.push(' ');
                print_into(store, val, out);
            }
            out.push('}');
        }
        Value::Data { hash, idx, fields } => {
            let name = ctor_name(store, hash, *idx);
            if fields.is_empty() {
                out.push_str(&name);
            } else {
                out.push('(');
                out.push_str(&name);
                for f in fields {
                    out.push(' ');
                    print_into(store, f, out);
                }
                out.push(')');
            }
        }
        Value::Closure { .. } => out.push_str("<fn>"),
        Value::Native(n) => match n {
            Native::Identity => out.push_str("<fn x. x>"),
            Native::Affine(a, b) => {
                out.push_str(&format!("<fn x. {}*x + {}>", a, b));
            }
            Native::Const(v) => {
                out.push_str("<fn _. ");
                print_into(store, v, out);
                out.push('>');
            }
            Native::Table(entries, default) => {
                out.push_str("<fn {");
                for (i, (k, val)) in entries.iter().enumerate() {
                    if i > 0 {
                        out.push(' ');
                    }
                    print_into(store, k, out);
                    out.push('\u{2192}');
                    print_into(store, val, out);
                }
                out.push_str("} else ");
                print_into(store, default, out);
                out.push('>');
            }
        },
    }
}

/// Print a `Float` (SPEC §3.2): specials as `inff`/`-inff`/`nanf`, finite as
/// the shortest round-tripping decimal + `f`, formatted as Go's
/// `strconv.FormatFloat(f, 'g', -1, 64)` (fixed-point within a bounded
/// decimal-exponent range, else scientific with a lowercase `e`). NOTE: the
/// examples corpus never prints a `Float` value (the one float counterexample,
/// f-tenths, is a NULLARY prop whose printed-inputs list is empty), so this Go
/// `'g'` reproduction is asserted from the spec but not byte-pinned by a
/// fixture — see DIVERGENCES.md.
pub fn format_float(bits: u64) -> String {
    let f = f64::from_bits(bits);
    if f.is_nan() {
        return "nanf".to_string();
    }
    if f.is_infinite() {
        return if f < 0.0 { "-inff".to_string() } else { "inff".to_string() };
    }
    let neg = (bits >> 63) & 1 == 1; // captures -0.0 too
    if f == 0.0 {
        return if neg { "-0f".to_string() } else { "0f".to_string() };
    }
    // Shortest significant digits and decimal-point position from Rust's shortest
    // scientific formatter: "<mant>e<exp>" where the first digit has place value
    // 10^exp, so the point sits after digit index (exp + 1).
    let abs = f.abs();
    let sci = format!("{:e}", abs);
    let (mant, exp) = sci.split_once('e').expect("LowerExp has an 'e'");
    let e: i32 = exp.parse().expect("integer exponent");
    let digits: String = mant.chars().filter(|c| *c != '.').collect();
    let dp = e + 1; // number of digits to the left of the decimal point
    let nd = digits.len() as i32;
    // Go 'g' with shortest precision: %e iff exp10 < -4 || exp10 >= 21, where
    // exp10 = dp - 1; otherwise %f.
    let exp10 = dp - 1;
    let body = if exp10 < -4 || exp10 >= 21 {
        // scientific: d[0] ('.' d[1..])? 'e' sign exp(≥2 digits)
        let mut s = String::new();
        s.push_str(&digits[..1]);
        if nd > 1 {
            s.push('.');
            s.push_str(&digits[1..]);
        }
        s.push('e');
        if exp10 < 0 {
            s.push('-');
        } else {
            s.push('+');
        }
        let mag = exp10.unsigned_abs();
        if mag < 10 {
            s.push('0');
        }
        s.push_str(&mag.to_string());
        s
    } else if dp <= 0 {
        // 0 . (zeros) digits
        let mut s = String::from("0.");
        for _ in 0..(-dp) {
            s.push('0');
        }
        s.push_str(&digits);
        s
    } else if dp >= nd {
        // digits (trailing zeros)
        let mut s = digits.clone();
        for _ in 0..(dp - nd) {
            s.push('0');
        }
        s
    } else {
        // digits split by the decimal point
        let d = dp as usize;
        format!("{}.{}", &digits[..d], &digits[d..])
    };
    if neg {
        format!("-{}f", body)
    } else {
        format!("{}f", body)
    }
}

fn ctor_name(store: &Store, hash: &str, idx: u32) -> String {
    if let Some(di) = store.data_by_hash.get(hash) {
        if let Some((n, _)) = di.ctors.get(idx as usize) {
            return n.clone();
        }
    }
    format!("Ctor{}", idx)
}
