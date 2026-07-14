//! Property verification (SPEC §4, §5) and byte-exact result rendering.
//! 200 cases per property, 2,000,000 fuel per case. A runtime error during a
//! case is a failure of that case.

use crate::elaborate::Store;
use crate::eval::Machine;
use crate::gen::{generate, seed_base, seed_for};
use crate::ir::{Def, Prop};
use crate::value::{print_value, Value};

pub const VERIFY_CASES: u64 = 200;
pub const VERIFY_FUEL: i64 = 2_000_000;

pub enum PropResult {
    Passed,
    Falsified { after: u64, inputs: Vec<String>, error: Option<String> },
}

pub fn run_prop(
    store: &Store,
    def_hash: &str,
    pi: u64,
    prop: &Prop,
    cases: u64,
    fuel: i64,
) -> PropResult {
    let base = seed_base(def_hash);
    for c in 0..cases {
        let mut rng = seed_for(base, pi, c);
        let size = (c % 8) as i64;
        let mut env: Vec<Value> = Vec::new();
        let mut inputs: Vec<String> = Vec::new();
        for bty in &prop.binders {
            match generate(store, bty, size, &mut rng) {
                Ok(v) => {
                    inputs.push(print_value(store, &v));
                    env.push(v);
                }
                Err(e) => {
                    return PropResult::Falsified { after: c, inputs, error: Some(e) };
                }
            }
        }
        let mut m = Machine::new(store, fuel);
        match m.eval(&prop.body, &mut env, def_hash) {
            Ok(Value::Bool(true)) => continue,
            Ok(Value::Bool(false)) => {
                return PropResult::Falsified { after: c, inputs, error: None }
            }
            Ok(_) => {
                return PropResult::Falsified {
                    after: c,
                    inputs,
                    error: Some("property did not evaluate to a bool".into()),
                }
            }
            Err(e) => return PropResult::Falsified { after: c, inputs, error: Some(e) },
        }
    }
    PropResult::Passed
}

/// Render the verify report for one function definition, or None if it has no
/// properties (data definitions and prop-less functions produce no file).
pub fn verify_text(store: &Store, name: &str) -> Option<String> {
    let info = store.func_by_name.get(name)?;
    let def = store.def_by_name.get(name)?;
    let props = match def {
        Def::Func { props, .. } => props,
        _ => return None,
    };
    if props.is_empty() {
        return None;
    }
    let mut out = String::new();
    for (pi, prop) in props.iter().enumerate() {
        let pname = &info.prop_names[pi];
        match run_prop(store, &info.hash, pi as u64, prop, VERIFY_CASES, VERIFY_FUEL) {
            PropResult::Passed => {
                out.push_str(&format!("\u{2713} prop {:<24} passed {} cases\n", pname, VERIFY_CASES));
            }
            PropResult::Falsified { after, inputs, error } => {
                out.push_str(&format!(
                    "\u{2717} prop {:<24} FALSIFIED after {} cases\n",
                    pname, after
                ));
                out.push_str("    counterexample: ");
                out.push_str(&inputs.join(", "));
                if let Some(e) = error {
                    out.push_str(&format!("  (runtime error: {})", e));
                }
                out.push('\n');
            }
        }
    }
    Some(out)
}
