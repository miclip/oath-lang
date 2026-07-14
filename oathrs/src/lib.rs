//! Oath conformance kernel — library crate.
//!
//! Everything here is pure computation (collections, strings, sha2) and builds
//! for wasm32 unchanged. The `prove` module (SMT via a z3 subprocess) is
//! native-only, behind the default `prove` feature, and is the sole part that
//! depends on `std::process`. See DIVERGENCES.md for the host assumptions the
//! wasm port surfaced (notably the evaluator's host-stack recursion, §3.1).

pub mod analyze;
pub mod check;
pub mod elaborate;
pub mod eval;
pub mod gen;
pub mod hash;
pub mod ir;
pub mod sexpr;
pub mod value;
pub mod verify;

#[cfg(feature = "prove")]
pub mod prove;
