//! Property verification (SPEC §4, §4.1, §5) and byte-exact result rendering.
//! 200 cases per property, 2,000,000 fuel per case.
//!
//! SPEC §4.1 gives each case exactly one outcome — `pass`, `refute`, or
//! `no-verdict` — and derives the property outcome from the cases. A case the
//! kernel could not evaluate (generator failure, runtime error including fuel
//! and depth exhaustion, or a non-boolean body) is `no-verdict`: it is a fact
//! about this kernel's budget, not about the program, so it is NOT a
//! refutation.

use crate::elaborate::Store;
use crate::eval::Machine;
use crate::gen::{generate, seed_base, seed_for};
use crate::ir::{Def, Prop};
use crate::value::{print_value, Value};

pub const VERIFY_CASES: u64 = 200;
pub const VERIFY_FUEL: i64 = 2_000_000;

/// A property's outcome, derived from its cases (SPEC §4.1).
pub enum PropResult {
    /// Every case passed.
    Passed,
    /// At least one case refuted. `after` is the index of the first refuting
    /// case and `inputs` its printed inputs — the counterexample.
    Falsified { after: u64, inputs: Vec<String> },
    /// No case refuted and at least one reached `no-verdict`. There is no
    /// counterexample; `reason` is why the FIRST no-verdict case reached none.
    Indeterminate { passed: u64, indet: u64, reason: String },
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
    // A REFUTATION DOMINATES A NO-VERDICT (SPEC §4.1): a no-verdict case never
    // stops the run, so a later refuting case is still found and reported —
    // the same input a kernel with unlimited fuel would report. Only a refute
    // returns early.
    let mut passed = 0u64;
    let mut indet = 0u64;
    let mut first_reason: Option<String> = None;
    for c in 0..cases {
        let mut rng = seed_for(base, pi, c);
        let size = (c % 8) as i64;
        let mut env: Vec<Value> = Vec::new();
        let mut inputs: Vec<String> = Vec::new();
        let mut gen_err: Option<String> = None;
        for bty in &prop.binders {
            match generate(store, bty, size, &mut rng) {
                Ok(v) => {
                    inputs.push(print_value(store, &v));
                    env.push(v);
                }
                Err(e) => {
                    gen_err = Some(e);
                    break;
                }
            }
        }
        if let Some(e) = gen_err {
            // the generator failed to build an input — no verdict on this case
            indet += 1;
            first_reason.get_or_insert(e);
            continue;
        }
        let mut m = Machine::new(store, fuel);
        match m.eval(&prop.body, &mut env, def_hash) {
            Ok(Value::Bool(true)) => passed += 1,
            // `after` is the number of PASSED cases, not the number attempted
            // (SPEC §4.1). The two differ only once a no-verdict case precedes
            // a refutation, and reporting attempts there would emit a different
            // transcript from a kernel reporting passes, for the same seed.
            Ok(Value::Bool(false)) => return PropResult::Falsified { after: passed, inputs },
            Ok(_) => {
                indet += 1;
                first_reason.get_or_insert_with(|| "property did not evaluate to a bool".into());
            }
            Err(e) => {
                indet += 1;
                first_reason.get_or_insert(e);
            }
        }
    }
    match first_reason {
        Some(reason) => PropResult::Indeterminate { passed, indet, reason },
        None => PropResult::Passed,
    }
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
            PropResult::Falsified { after, inputs } => {
                out.push_str(&format!(
                    "\u{2717} prop {:<24} FALSIFIED after {} cases\n",
                    pname, after
                ));
                // SPEC §3.2: the counterexample is the comma-separated printed
                // inputs of the first refuting case. The line is always emitted,
                // even for a nullary property whose inputs render empty.
                out.push_str("    counterexample: ");
                out.push_str(&inputs.join(", "));
                out.push('\n');
            }
            PropResult::Indeterminate { passed, indet, reason } => {
                // An indeterminate property has NO counterexample (SPEC §3.2,
                // §4.1) — no input refuted it — so it reports the reason its
                // first no-verdict case reached no verdict, unadorned.
                out.push_str(&format!(
                    "? prop {:<24} INDETERMINATE ({} passed, {} unevaluable)\n",
                    pname, passed, indet
                ));
                out.push_str(&format!("    no verdict: {}\n", reason));
            }
        }
    }
    Some(out)
}
