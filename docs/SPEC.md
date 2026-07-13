# Oath Kernel Specification

Version: `oath-kernel/0.6` · Status: normative, extracted from the Go
reference implementation (`oath/`).

This document defines what an Oath kernel **is**, independent of any
implementation. A conforming kernel MUST produce byte-identical definition
hashes, identical verdicts, identical counterexamples, and identical proof
outcomes on the same store. Where the Go implementation's behavior is an
accident of its host language, this spec promotes that behavior to normative
(and says so) — because the hash is identity, there is no such thing as an
innocent encoding difference.

## 1. Identity and canonical encoding

A definition's identity is `SHA-256(canonical-bytes)`, rendered as lowercase
hex. `canonical-bytes` is the compact JSON encoding of the `Def` structure
defined below. Only `Def` is ever hashed: names, guarantees, verdicts, and
all other metadata are outside identity (§9).

### 1.1 JSON encoding rules (normative)

- UTF-8, no insignificant whitespace (single-line, no spaces after `:`/`,`).
- Object keys appear in exactly the field order given in §1.2 — never
  alphabetized, never reordered.
- A field is **omitted entirely** when it holds its zero value: `0`, `false`,
  `""`, or an empty/absent array or object pointer — *except* fields marked
  "always present" below.
- String escaping follows Go `encoding/json` defaults, promoted to
  normative: `"` becomes `\"`, backslash doubles, control characters
  become `\uXXXX` (with `\n`, `\r`, `\t` shortcuts), and — the trap for
  conformance implementers — the HTML-safety escapes: `<` becomes
  `\u003c`, `>` becomes `\u003e`, `&` becomes `\u0026`, U+2028 becomes
  `\u2028`, U+2029 becomes `\u2029`. A conforming kernel MUST reproduce
  these: a string literal containing `<` hashes differently if encoded
  naively.
- Integers are decimal, no exponent form, range int64.

### 1.2 Structures and field order

`Ty` — a type. Fields in order: `k`, `var`, `a`, `b`, `hash`, `args`,
`names`.

| k | meaning | fields used |
|---|---|---|
| `int`, `bool`, `str` | base types | — |
| `var` | type variable | `var` (index into the def's type params) |
| `fun` | function | `a` (domain), `b` (codomain) |
| `data` | ADT instance | `hash` (defining ADT), `args` (type arguments) |
| `rec` | the ADT being defined | `args` (only inside `data` defs) |
| `record` | structural record | `names` (field names, sorted ascending bytewise, unique), `args` (field types, parallel) |

`Term` — an expression. Fields in order: `k`, `idx`, `int`, `bool`, `str`,
`ty`, `a`, `b`, `c`, `op`, `hash`, `tyargs`, `args`, `names`, `arms`.

| k | fields used |
|---|---|
| `var` | `idx` (de Bruijn: 0 = innermost binder) |
| `int`, `bool`, `str` | `int` / `bool` / `str` |
| `lam` | `ty` (param type), `a` (body) |
| `app` | `a` (function), `b` (argument) |
| `let` | `ty` (bound type), `a` (bound), `b` (body; binds one) |
| `if` | `a`, `b`, `c` |
| `prim` | `op`, `args` |
| `ref` | `hash`, `tyargs` |
| `self` | `tyargs` (the def being defined) |
| `ctor` | `hash` (ADT), `idx` (constructor), `tyargs`, `args` |
| `match` | `hash` (ADT), `a` (scrutinee), `arms` (one per constructor, in constructor order; arm *i* binds constructor *i*'s fields as consecutive binders, first field outermost) |
| `record` | `names` (sorted, unique), `args` (values, parallel) |
| `field` | `a` (record), `op` (field name) |

`Prop`: `binders` (**always present**, array of `Ty`, possibly empty),
`body` (`Term`, always present).

`Def`: `k` (`data`|`func`, always present), `tyvars` (int, **always
present** even when 0), then `ctors` (data: array of arrays of `Ty`), `ty`,
`body`, `props` (func).

`Guarantee` (metadata, §9): `level` always present; `cases`, `proven`,
`falsified` omitted when zero/empty.

### 1.3 Canonicalization obligations

Producers (elaborators) MUST emit, and checkers MUST enforce:

- Variables are de Bruijn indices; binder names never appear in a `Def`.
  Alpha-equivalent programs are byte-identical.
- Record fields (types and literals) sorted ascending by bytewise string
  comparison; duplicate names rejected.
- Match arms in constructor-declaration order, exhaustive, with the ADT
  hash recorded in the term.
- Primitive operators are the literal strings: `+ - * / % neg == < <= and
  or not ++ str-len`.

## 2. Static semantics (the gate)

Typechecking is structural synthesis; there is no inference or unification.
Every binder is annotated; every reference to a generic definition or
constructor carries explicit type arguments matching the target's `tyvars`
count. `==` requires both operands the same type, which must not contain a
function type. `rec` types are legal only inside `data` definitions. Prop
binders must be concrete (no `var`/`rec`) and prop bodies must be `Bool`.
A definition failing any check MUST NOT be stored.

## 3. Dynamic semantics

- **Strict, left-to-right.** `app` evaluates function then argument;
  `prim`, `ctor`, and `record` (in canonical field order) evaluate all
  arguments left-to-right; `let` evaluates bound then body; `match`
  evaluates scrutinee then exactly the selected arm; `if` evaluates only the
  taken branch.
- **`and`/`or` are NOT short-circuiting** — all primitive arguments are
  evaluated before the operator applies. `(and false (diverge))` diverges.
- Closures capture their environment by value at `lam` evaluation.
- `ref`/`self` re-evaluate the referenced definition's body at each use
  (bodies are almost always `lam`, so this constructs a closure).
- **Integers** are two's-complement int64; `+ - *` wrap on overflow; `/`
  truncates toward zero; `%` takes the dividend's sign; division or modulo
  by zero is a runtime error.
- **Strings** are byte sequences (UTF-8 by convention): `++` is byte
  concatenation, `==` is byte equality, `str-len` counts *Unicode code
  points* (not bytes). This asymmetry is normative.
- **Structural equality** (`==`) on data and records compares constructor
  index and fields recursively; applying it to function values is a runtime
  error (statically prevented).

### 3.1 Resource bounds

Two independent bounds, both normative for verdict reproducibility:

- **Fuel**: each term-node evaluation and each function application
  consumes 1 unit. Exhaustion is a runtime error.
- **Depth**: nested evaluation deeper than 100,000 is a runtime error
  (the fuel bound alone does not prevent host-stack exhaustion).

## 4. Deterministic generation

All random values derive from `splitmix64`:

```
next(s): s += 0x9E3779B97F4A7C15
         z = s; z ^= z>>30; z *= 0xBF58476D1CE4E5B9
         z ^= z>>27; z *= 0x94D049BB133111EB
         return z ^ (z>>31)
below(n)    = next() mod n            (modulo bias is normative)
intIn(lo,hi)= lo + next() mod (hi-lo+1)
```

Seed for property *pi*, case *c* of a definition with hash *h*:
`base = big-endian-uint64(first 8 bytes of hex-decoded h)`;
`s = base XOR (pi << 32) XOR (c * 0xD1B54A32D192ED03)`. Size = `c mod 8`.

Generation by type — draw order is normative:

- `Int`: draw `below(4)`; on 0, draw `below(5)` into boundary table
  `[-2,-1,0,1,2]`; otherwise draw `intIn(-20,20)`.
- `Bool`: `below(2) == 0`.
- `Str`: length `below(size+1)`, then that many draws of `below(7)` into
  alphabet `"ab xyz!"` (bytes, in that order).
- `Int -> Int`: draw `below(4)`: 0 → identity; 1,2 → affine with
  `NA = intIn(-3,3)`, `NB = intIn(-10,10)`; 3 → table (below).
- Any other function type: table with `n = 1 + below(3)` entries — for each,
  generate key (domain type, same size) then value (codomain, same size) —
  then a default value (codomain). Application: first key structurally equal
  to the argument wins, else default.
- Record: fields in canonical order, same size.
- Data: if size ≤ 0, choose uniformly among constructors with no recursive
  field (error if none); else uniformly among all constructors. Fields
  generated left-to-right at size−1.

Verification runs **200 cases per property** with **2,000,000 fuel** per
case. Mutation testing runs 60 cases with 500,000 fuel. A runtime error
(including fuel/depth exhaustion) during a case is a failure of that case.

## 5. Verdicts

Guarantee levels: `asserted` (no properties), `tested` (all properties
passed all cases), `falsified` (some property failed — the failing property
names are recorded, with the first counterexample), `proven` (§7 succeeded
for all properties). Falsified definitions ARE stored; rejection happens
only at the typecheck gate. `put` reporting (JSON mode) and the journal
(§8) carry: name, hash, kind, status, guarantee, termination, confinement,
prior hash on repoint, and per-property results with counterexamples
rendered by the value printer.

## 6. Auxiliary analyses (metadata verdicts, never rejections)

- **Termination**: `structural` if some fixed argument position receives a
  strict subterm (obtained via match binders, transitively) at every
  self-call and every body-referenced function is itself total;
  `nonrecursive` if no self-calls and all callees total; else `unknown`.
- **Confinement**: a higher-order parameter is `confined` if every
  occurrence is applied, projected-and-applied, passed at the same position
  in a self-call, or passed whole to a callee position already `confined`;
  any other use (returned, stored, captured under an inner `lam`, projected
  without application) makes it `escapes`.
- **Spec strength**: mutation catalog = type-preserving operator swaps
  (`+↔-`, `*→+`, `/→*`, `%→/`, `<↔<=`, `and↔or`), operand swaps of
  non-commutative binary prims (`- / % < <= ++`), integer literal ±1 and →0,
  string literal → `""`, if-branch swap. Score = killed/total.

## 7. Proof obligations (SMT boundary)

The provable fragment and its translation, normative for outcome
reproducibility (given the same solver):

- Sorts: `Int`, `Bool`, `Str`→`String`; monomorphic data instances as
  algebraic datatypes (names derived from metadata but NOT semantically
  significant — only structure is); records as single-constructor
  datatypes; function types as `(Array dom cod)` applied via `select`.
- Non-recursive callees are inlined (beta-reduction); recursive callees are
  declared uninterpreted with their defining equation asserted as a
  universally quantified axiom with the application as pattern.
- `match` translates to tester/selector ite-chains.
- **Excluded, permanently or pending**: `/` and `%` (kernel truncates,
  SMT-LIB is Euclidean — translation would prove the wrong theorem);
  partial application; lambda values in argument position.
- Proof search: direct (assert negation, check-sat), then structural
  induction on each datatype-typed binder in order — one subgoal per
  constructor, induction hypotheses for datatype-recursive fields with all
  other binders universally generalized.
- Lemma library: proven properties of transitively referenced definitions,
  and previously proven properties of the definition itself, are asserted
  as axioms — a property is never an axiom in its own proof.
- `sat` on a quantifier-free direct attempt is a refutation (report the
  model); `sat`/`unknown` otherwise is merely "unproven".
- Standing caveat attached to every proof: solver integers are unbounded;
  kernel integers are int64. A proof is valid modulo overflow.

## 8. The journal

Append-only, one JSON object per line: `seq`, `time` (RFC3339 UTC),
`author` (principal string, self-reported in local mode), `verifier`
(kernel version string), `name`, `kind`, `status`
(`accepted`|`falsified`|`rejected`), `hash`, `prev` (on repoint), `error`,
`guarantee`, `termination`. Every submission attempt MUST be journaled,
including gate rejections (which store no object). The journal is metadata:
never hashed, wall-clock timestamps permitted (the kernel's no-clock rule
protects verification semantics only).

## 9. The hashed/metadata boundary

Hashed (identity): the `Def` — structure, types, bodies, properties.
Everything else is metadata, mutable without changing identity: all names
(definition, type-variable, constructor, property, parameter), guarantee
level and history, termination/confinement verdicts, spec strength, proven
property indices, author, the name→hash index, and the journal.

## 10. Conformance

A candidate kernel conforms if, against a reference store:

1. It computes byte-identical hashes for every definition in the store
   (equivalently: re-elaborating `examples/*.oath` reproduces every hash).
2. Given the same definition hash, it reproduces every property verdict,
   pass count, and counterexample string byte-for-byte.
3. Its gate accepts and rejects exactly the same definitions.
4. Termination and confinement verdicts match.
5. Proof outcomes match, given the same solver version (proof *methods* —
   direct vs induction binder — should match but MAY differ where multiple
   proofs exist).

The `examples/` corpus plus the journal of a reference store constitutes
the initial conformance suite. Cross-kernel agreement on all five points is
the intended CI gate for any second implementation.
