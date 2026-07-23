//! Deterministic value generation (SPEC §4). splitmix64, seed derivation from
//! the definition hash, and the exact per-type draw order and distributions.
//! One draw out of order diverges every downstream case.

use crate::elaborate::Store;
use crate::ir::Ty;
use crate::value::{Native, Value};

pub struct Rng {
    s: u64,
}

impl Rng {
    pub fn next(&mut self) -> u64 {
        self.s = self.s.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = self.s;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^ (z >> 31)
    }

    pub fn below(&mut self, n: u64) -> u64 {
        // modulo bias is normative
        self.next() % n
    }

    pub fn int_in(&mut self, lo: i64, hi: i64) -> i64 {
        let range = (hi - lo + 1) as u64;
        lo.wrapping_add((self.next() % range) as i64)
    }
}

/// base = big-endian uint64 of the first 8 bytes of the hex-decoded hash.
pub fn seed_base(hash: &str) -> u64 {
    let bytes = hex_decode(hash);
    let mut b = 0u64;
    for i in 0..8 {
        b = (b << 8) | bytes[i] as u64;
    }
    b
}

/// s = base XOR (pi<<32) XOR (c * 0xD1B54A32D192ED03)
pub fn seed_for(base: u64, pi: u64, c: u64) -> Rng {
    let s = base ^ (pi << 32) ^ c.wrapping_mul(0xD1B54A32D192ED03);
    Rng { s }
}

fn hex_decode(h: &str) -> Vec<u8> {
    let bytes = h.as_bytes();
    let mut out = Vec::with_capacity(bytes.len() / 2);
    let mut i = 0;
    while i + 1 < bytes.len() {
        let hi = (bytes[i] as char).to_digit(16).unwrap_or(0);
        let lo = (bytes[i + 1] as char).to_digit(16).unwrap_or(0);
        out.push(((hi << 4) | lo) as u8);
        i += 2;
    }
    out
}

const INT_BOUNDARY: [i64; 5] = [-2, -1, 0, 1, 2];

pub fn generate(store: &Store, ty: &Ty, size: i64, rng: &mut Rng) -> Result<Value, String> {
    // Size is clamped to a minimum of 0 on entry to every generation call.
    let size = size.max(0);
    match ty {
        Ty::Int => {
            // `Int` is ℤ, but the §4 generator's distribution is unchanged: small
            // boundary/uniform values, wrapped in arbitrary-precision integers.
            if rng.below(4) == 0 {
                let k = rng.below(5) as usize;
                Ok(Value::Int(num_bigint::BigInt::from(INT_BOUNDARY[k])))
            } else {
                Ok(Value::Int(num_bigint::BigInt::from(rng.int_in(-20, 20))))
            }
        }
        Ty::Bool => Ok(Value::Bool(rng.below(2) == 0)),
        Ty::Fun(a, b) => {
            if matches!(**a, Ty::Int) && matches!(**b, Ty::Int) {
                match rng.below(4) {
                    0 => Ok(Value::Native(Native::Identity)),
                    1 | 2 => {
                        let na = rng.int_in(-3, 3);
                        let nb = rng.int_in(-10, 10);
                        Ok(Value::Native(Native::Affine(na, nb)))
                    }
                    _ => gen_table(store, a, b, size, rng),
                }
            } else {
                gen_table(store, a, b, size, rng)
            }
        }
        Ty::Record { names, args } => {
            let mut vals = Vec::with_capacity(args.len());
            for a in args {
                vals.push(generate(store, a, size, rng)?);
            }
            Ok(Value::Record { names: names.clone(), vals })
        }
        Ty::Data { hash, args } => {
            let di = store
                .data_by_hash
                .get(hash)
                .ok_or_else(|| format!("generate: unknown data {}", hash))?;
            let nctors = di.ctors.len();
            // choose the constructor. Selection ALWAYS consumes exactly one
            // below(k) draw, including k == 1, in both size branches (SPEC §4).
            // See DIVERGENCES #59.
            let idx = if size <= 0 {
                let cands: Vec<usize> = (0..nctors)
                    .filter(|&i| !ctor_is_recursive(&di.ctors[i].1))
                    .collect();
                if cands.is_empty() {
                    return Err(format!("generate: data {} has no base constructor", hash));
                }
                cands[rng.below(cands.len() as u64) as usize]
            } else {
                rng.below(nctors as u64) as usize
            };
            let field_tys: Vec<Ty> = di.ctors[idx]
                .1
                .iter()
                .map(|f| inst_field(f, args, hash))
                .collect();
            let mut fields = Vec::with_capacity(field_tys.len());
            for ft in &field_tys {
                fields.push(generate(store, ft, size - 1, rng)?);
            }
            Ok(Value::Data { hash: hash.clone(), idx: idx as u32, fields })
        }
        Ty::Rec { .. } | Ty::Var(_) => {
            Err("generate: non-concrete type encountered".into())
        }
    }
}

fn gen_table(store: &Store, dom: &Ty, cod: &Ty, size: i64, rng: &mut Rng) -> Result<Value, String> {
    let n = 1 + rng.below(3);
    let mut entries = Vec::new();
    for _ in 0..n {
        let key = generate(store, dom, size, rng)?;
        let value = generate(store, cod, size, rng)?;
        entries.push((key, value));
    }
    let default = generate(store, cod, size, rng)?;
    Ok(Value::Native(Native::Table(entries, Box::new(default))))
}

fn ctor_is_recursive(fields: &[Ty]) -> bool {
    fields.iter().any(contains_rec)
}

fn contains_rec(ty: &Ty) -> bool {
    match ty {
        Ty::Rec { .. } => true,
        Ty::Fun(a, b) => contains_rec(a) || contains_rec(b),
        Ty::Data { args, .. } | Ty::Record { args, .. } => args.iter().any(contains_rec),
        _ => false,
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
