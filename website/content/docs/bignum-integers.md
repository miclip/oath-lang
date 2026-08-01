# Arbitrary-precision integers (substrate change)

**Status:** committed direction (2026-07). Closes the "valid modulo overflow"
caveat that sat on every proof. No back-compat: the corpus re-forks (every int
literal re-encodes).

## Decision

`Int` becomes **ℤ — arbitrary precision**, not `int64`. The runtime and the O1
encoding are brought into line with the proof model, which was *already*
unbounded.

### Why this is the correct solution

Proofs discharge over Z3's **unbounded** integer theory (LIA) — the language
already committed to "`Int` = ℤ". The `int64` runtime was the pragmatic shortcut
that created the mismatch, giving every proof the caveat *"valid modulo
overflow"*: a proven-correct function could return a silently-wrong wrapped value
(`fib 93` overflows int64 today). 

Overflow is **not** like division by zero. Division by zero has no answer, so
erroring is correct. Overflow has a **well-defined answer**; int64 just can't
hold it. Trapping (checked arithmetic) discards a real result because the
*representation* is too small; representing it (bignum) computes the answer that
exists. Given the proof model is already ℤ, the correct completion is to make the
runtime ℤ too. `total` goes back to meaning total.

**Governing principle (same as strings/maps):** prove over the clean model, run
over an optimized representation. The clean model here is ℤ; the small-int
representation optimization (tagged int64 fast path) is a compiler concern (#13),
not an inherent cost — it does not affect the semantics or the proofs.

## What changes / what doesn't

**Unchanged:**
- **Proofs.** Z3 `Int` is already unbounded ℤ; SMT literals already render in
  decimal. Not a single proof, script, or verdict changes in meaning.
- Test verdicts: generated inputs are small; nothing overflowed before, so
  nothing traps/changes.

**Changed:**
- **AST:** `Term.Int` and the runtime `Value.Int` go from `int64` to
  `*big.Int` (Go `math/big`); oathrs uses its arbitrary-precision type.
- **Eval:** `+ - * / %`, `neg`, and comparisons use big-integer arithmetic.
  `/`/`%` by zero stay runtime errors; overflow no longer exists.
- **O1 encoding (identity):** the `int` term (tag `0x11`) stops being a fixed
  8-byte `i64`. New canonical form: a **sign byte** (`0x00` for ≥0, `0x01` for
  <0) + `u32` magnitude-byte-length + big-endian minimal magnitude bytes (no
  leading zeros; `0` is sign `0x00`, length `0`). Canonical and unbounded. This
  re-encodes every int literal, so the whole corpus re-forks.
- **Parse:** integer literals parse at arbitrary precision (not `strconv.ParseInt
  ..64`).
- **SPEC:** §1.1 (the `i64` row / int-term encoding), §3 (drop "two's-complement
  int64; `+ - *` wrap on overflow" → "`Int` is ℤ, arbitrary precision"), and the
  §7 standing caveat ("valid modulo overflow") is **deleted**.

## Staging

0. **This note.**
1. **Kernel:** `Term.Int`/`Value.Int` → `*big.Int`; parse, eval, canon
   encode/decode, gen, compile, prove-format updated. Green build; a big
   computation that overflowed int64 (`fib 93`, `2^100`) now evaluates exactly.
2. **Corpus re-fork:** re-put everything (new int encoding → new hashes),
   restore verdicts (proofs unchanged; only hashes move), regenerate fixtures.
3. **SPEC rewrite** (the encoding + the deleted overflow caveat).
4. **Second kernel:** blind `oathrs` re-derives from the SPEC; byte-oracle green.
5. **Fixtures, website, CI** regenerated; merge.
