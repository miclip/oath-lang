//! Runtime values (SPEC §3.2) and the value printer. The printer is
//! byte-critical for counterexamples, so it is treated like the canonical
//! encoder: implemented by hand from the spec.

use crate::elaborate::Store;
use num_bigint::BigInt;

#[derive(Clone, Debug)]
pub enum Value {
    // `Int` is ℤ — arbitrary precision (SPEC §3).
    Int(BigInt),
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

fn ctor_name(store: &Store, hash: &str, idx: u32) -> String {
    if let Some(di) = store.data_by_hash.get(hash) {
        if let Some((n, _)) = di.ctors.get(idx as usize) {
            return n.clone();
        }
    }
    format!("Ctor{}", idx)
}
