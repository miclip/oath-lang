# IEEE-754 floats (`Float`) — design note

**Status:** committed direction (2026-07). Adds `Float` (IEEE-754 binary64) as
a primitive numeric type alongside `Int` (ℤ) and `Rat` (ℚ). This note fixes the
one decision that touches the substrate — how a content-addressed kernel gives
floats an identity — before any kernel code.

## Why floats at all

`Rat` closed exact arithmetic (money, decimals, provable algebra). It does not
close **IEEE interop**: hardware floats, ML/scientific data, existing float
formats. A program that must match what a GPU or numpy computes needs the same
bit-level semantics, not an exact rational. `Float` is that bridge. It is a
*different* need from `Rat`, not a better `Rat` — you reach for `Float` when
bit-compatibility with the outside world is the requirement.

## The fork: identity for a type where value ≠ representation

Every other Oath type has a clean canonical form (reduced fraction, sorted
record fields, de Bruijn indices), so identity is obvious. IEEE-754 breaks that
at two points:

- **NaN** has ~2²³ bit patterns, and IEEE `NaN ≠ NaN`.
- **±0.0** are equal as IEEE numbers but have different bits and are
  distinguishable (`1.0 / -0.0 = -inf ≠ +inf`).

Content-addressing requires: one canonical encoding per value, and **same hash
⇒ interchangeable in every context** (Leibniz). The resolution:

### Decision 1 — `Float` values are bit patterns, identity is bit-identity

A `Float` term *is* its 64-bit IEEE-754 pattern; two `Float` terms are the same
object iff they have the same bits. This is the only choice that preserves
interchangeability: identical bits, under a fixed rounding mode and canonical
NaN, produce identical results under every IEEE operation. Numeric equality
(`fp.eq`) would make `+0.0` and `-0.0` "the same" while `1/x` distinguishes
them — breaking substitution. So identity is **not** numeric equality.

### Decision 2 — structural `==` is SMT `=` (Leibniz), not IEEE `fp.eq`

SMT-LIB already separates the two float equalities, and they line up exactly
with what Oath needs:

| | IEEE `fp.eq` | SMT `=` (Leibniz) |
|---|---|---|
| `NaN = NaN` | false | **true** (one NaN value) |
| `+0.0 = -0.0` | true | **false** (distinct values) |
| reflexive / a congruence | no | **yes** |

Oath's `==` is Leibniz equality for every other type; `Float` is no exception.
`==` on floats maps to SMT `=`. IEEE's `fp.eq` (with its NaN/±0 quirks) is
available as a *separate, optional* named primitive `fp-eq` for the rare code
that wants NaN-aware comparison — it does not pollute structural equality.

### Decision 3 — one canonical NaN

SMT-LIB FP has exactly **one** NaN value. To keep kernel-value ↔ Z3-value a
bijection (and to kill NaN-payload nondeterminism, which is platform-defined),
the kernel canonicalizes every NaN to a single quiet NaN,
`0x7FF8000000000000`, at construction and after every operation. The strict
decoder **rejects** any other NaN bit pattern — exactly as it rejects a
non-reduced rational. `-0.0` is a normal, accepted, distinct value.

### Decision 4 — fixed rounding mode: round-nearest-ties-even

All ops round RNE (the IEEE default, and Z3's assumption). No per-op rounding
argument; it is part of the semantics. Can be revisited if a real need appears.

### Decision 5 — float ops are total (no runtime error)

Unlike `Int`/`Rat` division, IEEE division by zero is defined: `1.0/0.0 = +inf`,
`-1.0/0.0 = -inf`, `0.0/0.0 = NaN`. So `Float` `+ - * / neg` never error — they
are total functions of their inputs. (This is a semantic *gain* for totality.)

## Encoding

- Ty tag `0x0A` (float), no fields.
- Term tag `0x20` (float): **8 raw bytes**, the IEEE-754 binary64 in
  big-endian, with NaN canonicalized. One value ⇒ one encoding.

## What proves — floats reach PROVEN

Z3's float theory (FPA) is decidable, and Oath's method (assert the negated
property with binders as constants, check-sat) puts non-recursive float
arithmetic inside the provable fragment. `Float` definitions earn real `proven`
verdicts — the same tier as `Int`/`Rat`, **not** a consolation `tested`.

The distinction is *which properties are true*, not whether truth is provable:

| property | verdict | why |
|---|---|---|
| `0.5 + 0.5 == 1.0` | PROVEN | true; FPA discharges it |
| `x * 1.0 == x` (all x) | PROVEN | true |
| "no NaN out on finite in" | PROVEN | true safety property |
| monotonicity / bounds / sign | PROVEN | true numerical facts |
| `(a+b)+c == a+(b+c)` | FALSIFIED | *genuinely false* for floats |
| `0.1 + 0.2 == 0.3` | FALSIFIED | *genuinely false* (`…04`) |

The bottom two do not bail to `tested` for weakness — they are **false**, and
the kernel correctly refuses to certify a false thing. That is the prover being
right, and it is categorically different from the recursion fragment (where a
*true* property bails with "couldn't reason about this"). Nothing about floats
bails that way.

This gives the corpus its sharpest exhibit — one property, two substrates:

```
0.1 + 0.2 == 0.3   →  PROVEN    for Rat   (exact 3/10)
0.1f + 0.2f == 0.3f →  FALSIFIED for Float (0.30000000000000004)
```

## Surface syntax

Decimal literals stay `Rat` (exactness is the safe default). `Float` literals
opt in with an `f` suffix: `0.1f`, `1.0f`, `1f`, `3.14f`, `1e9f`, optionally
signed. A token is a float iff stripping a trailing `f` leaves a
`strconv.ParseFloat`-able string (so a bare symbol like `f` or `fold` is
unaffected). `inf`/`nan` are not literals — they arise from operations
(`1.0f / 0.0f`) and print as `inf` / `-inf` / `nan`.

## Prover mapping

- `Float` → Z3 sort `(_ FloatingPoint 11 53)`.
- literal → uniformly `(fp (_ bvS 1) (_ bvE 11) (_ bvM 52))` from the exact
  canonicalized bits, for EVERY value including NaN / ±inf / ±0 (no special
  `(_ NaN …)` / `(_ +oo …)` / `(_ +zero …)` forms — one code path, one
  representation per value). This is the normative rule; see SPEC §7.1.
- `+ - * / neg` → `fp.add`/`fp.sub`/`fp.mul`/`fp.div`/`fp.neg` at `RNE`.
- `< <=` → `fp.lt` / `fp.leq` (IEEE ordered: NaN unordered ⇒ false).
- `==` → SMT `=` (Leibniz), matching kernel identity.
- optional `fp-eq` → `fp.eq` (IEEE equality) for NaN-aware code.

## What Float is not

Not a replacement for `Rat`; not implicitly convertible to/from `Int` or `Rat`
(explicit conversions are a later, separate story). No per-op rounding modes,
no decimal (base-10) float, no `Float32` — binary64 only for v1.
