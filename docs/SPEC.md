# Oath Kernel Specification

Version: `oath-kernel/0.7` · Status: normative, extracted from the Go
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
hex. `canonical-bytes` is the "O1" binary encoding of the `Def` defined
below — the exact bytes the store persists in `objects/<hash>.bin`. Only
`Def` is ever hashed: names, guarantees, verdicts, and all other metadata
are outside identity (§9).

### 1.1 O1 primitives (normative)

O1 is a tag-length-value tree with no optional fields and no escaping.
Every field is always written; there is exactly one encoding per definition.

| primitive | encoding |
|---|---|
| `u8` | one byte |
| `u32` | 4 bytes, big-endian unsigned (counts, indices) |
| `i64` | 8 bytes, big-endian two's complement (used only for magnitudes, if ever) |
| `bigint` | `u8` sign (`0x00` ≥0, `0x01` <0) ++ `u32` magnitude byte-length ++ minimal big-endian magnitude bytes (no leading zeros; zero is sign `0x00`, length 0 — sign `0x01` with length 0 is a NEGATIVE ZERO and MUST be rejected) |
| `str` | `u32` byte-length ++ raw UTF-8 bytes (NO escaping of any kind) |
| `hash` | 32 raw bytes (the referenced definition's SHA-256) |
| `list<X>` | `u32` count ++ that many `X` |
| `bool byte` | `0x00` false / `0x01` true; any other value is malformed |

Canonical bytes begin with the 2-byte magic `0x4F 0x31` ("O1").

### 1.2 Tags and structure

`Ty` — one tag byte, then fields:

| tag | kind | fields |
|---|---|---|
| 0x01 | int | — |
| 0x02 | bool | — |
| 0x03 | *(reserved — was the `str` primitive type; strings are now the `Str` datatype)* |
| 0x04 | var | `u32` index |
| 0x05 | fun | Ty domain, Ty codomain |
| 0x06 | data | `hash`, `list<Ty>` args |
| 0x07 | rec | `list<Ty>` args |
| 0x08 | record | `u32` count, then per field: `str` name, Ty — names strictly ascending bytewise |
| 0x09 | rat | — |
| 0x0A | float | — |

`Term` — one tag byte, then fields:

| tag | kind | fields |
|---|---|---|
| 0x10 | var | `u32` de Bruijn index (0 = innermost) |
| 0x11 | int | `bigint` (arbitrary precision — `Int` is ℤ) |
| 0x12 | bool | bool byte |
| 0x13 | *(reserved — was the string-literal term; `"…"` now elaborates to an `Str` constructor chain)* |
| 0x14 | lam | Ty param, Term body |
| 0x15 | app | Term fn, Term arg |
| 0x16 | let | Ty, Term bound, Term body |
| 0x17 | if | Term, Term, Term |
| 0x18 | prim | `str` op, `list<Term>` args |
| 0x19 | ref | `hash`, `list<Ty>` tyargs |
| 0x1A | self | `list<Ty>` tyargs |
| 0x1B | ctor | `hash`, `u32` ctor index, `list<Ty>` tyargs, `list<Term>` args |
| 0x1C | match | `hash`, Term scrutinee, `list<Term>` arms (constructor order; arm *i* binds ctor *i*'s fields, first field outermost) |
| 0x1D | record | `u32` count, then per field: `str` name, Term — names strictly ascending |
| 0x1E | field | Term record, `str` field name |
| 0x1F | rat | `bigint` numerator ++ `bigint` denominator (reduced form: `gcd(|num|, den) = 1`, `den ≥ 1`) |
| 0x20 | float | 8 raw bytes: IEEE-754 binary64, big-endian, NaN canonicalized to `0x7FF8000000000000` |

`Def` — one tag byte after the magic:

| tag | kind | fields |
|---|---|---|
| 0x01 | data | `u32` tyvars, `u32` ctor count, then per ctor: `list<Ty>` fields |
| 0x02 | func | `u32` tyvars, Ty declared type, Term body, `u32` prop count, then per prop: `list<Ty>` binders, Term body |

Decoders MUST be strict: unknown tags, malformed bool bytes, record names
not strictly ascending, non-canonical integers (a leading-zero magnitude, or a
NEGATIVE ZERO — sign `0x01` with a zero-length magnitude, which would be a second
encoding of `0`), and trailing bytes are all rejected — so decode
followed by encode is the identity on valid objects, and no second encoding
of any definition exists.

(Kernels ≤0.6 hashed a canonical-JSON encoding; stores were migrated
wholesale by `oath migrate-encoding`, which rewrote embedded references
topologically and journaled every object's old→new mapping. The journal's
pre-migration entries retain v0 hashes.)

### 1.3 Canonicalization obligations

Producers (elaborators) MUST emit, and checkers MUST enforce:

- Variables are de Bruijn indices; binder names never appear in a `Def`.
  Alpha-equivalent programs are byte-identical.
- Record fields (types and literals) sorted ascending by bytewise string
  comparison; duplicate names rejected.
- Match arms in constructor-declaration order, exhaustive, with the ADT
  hash recorded in the term.
- Primitive operators are the literal strings: `+ - * / % neg == < <= and
  or not fp-eq to-rat to-float floor hmac-sha256 bytes-eq-ct`. There are NO
  string primitives: strings are the ordinary `Str` datatype (§3), and every
  string operation is a definition. `fp-eq` is the IEEE-754 equality on `Float`, distinct from
  structural `==` (§3). `to-rat`, `to-float`, and `floor` are the numeric
  conversions (§2, §3), overloaded by source type.
- **Crypto over byte lists (normative, #78).** `hmac-sha256` and `bytes-eq-ct`
  are the only primitives whose arguments are an ADT rather than a scalar. Both
  take two `(List Int)` arguments — a BYTE list, one `Int` per byte in `0..255`.
  Byte strings are deliberately NOT `Str`: `Str` is a codepoint list (§3) that a
  kernel may represent as native text, and a signed payload is bytes which need
  not be valid UTF-8. Routing it through `Str` would corrupt any byte above
  `0x7F` inside the TYPE, where nothing downstream can recover it.
  - `hmac-sha256 key msg : (List Int)` — HMAC-SHA256 (RFC 2104 with SHA-256),
    returning the 32-byte digest as a byte list.
  - `bytes-eq-ct a b : Bool` — equality that MUST examine every byte regardless
    of where the operands first differ. Structural `==` on a byte list
    short-circuits and so leaks the position of the first difference; comparing
    a digest with `==` is a timing oracle, which is why this exists separately.
  Operands of DIFFERING LENGTH compare unequal — they are never an error. The
  corpus's fail-closed behaviour depends on this: malformed hex decodes to the
  empty list, which must simply not match a 32-byte digest.
  An element outside `0..255` is a RUNTIME ERROR in both operations. A kernel
  MUST NOT truncate or reduce modulo 256: a digest computed over silently
  altered input would verify against a message nobody sent, so the failure mode
  of being permissive here is accepting a forged payload.
  Note the pair is deliberately digest-plus-compare rather than a single
  `verify` predicate. A `verify` cannot be exercised on its ACCEPTING path by
  property testing, because the generator cannot forge a valid signature; with
  the digest exposed it can compute one, so both branches are testable.
  **Both are OUTSIDE THE PROVABLE FRAGMENT (§7).** They are the trusted boundary:
  modelling SHA-256 in SMT is not useful even where possible, and an
  axiomatization invented for the purpose would establish facts about the
  axiomatization rather than about the algorithm. A property whose goal reaches
  either operation is recorded `tested`, never `proven` — which is the honest
  guarantee level for a trusted primitive, and leaves the surrounding logic
  provable as usual.
- Rational (`rat`) terms are stored in reduced form: the denominator is
  positive (`≥ 1`) and coprime to the numerator (`gcd(|num|, den) = 1`); the
  sign lives on the numerator. `0` is `0/1`. Decoders MUST reject a `rat`
  whose denominator is `0` or `< 0`, or whose numerator and denominator share
  a common factor — so equal rationals have one identity.
- Float (`float`) terms are stored as the 8 IEEE-754 binary64 bytes with NaN
  canonicalized: every NaN (any payload, signaling or quiet) is the one
  pattern `0x7FF8000000000000`. Decoders MUST reject any other NaN bit pattern.
  `-0.0` and `+0.0` are distinct values (distinct bits, distinct identity), as
  are `±inf`. Producers canonicalize NaN at construction and after every
  operation — so a float value has exactly one encoding.

### 1.4 Surface syntax and elaboration

The surface syntax is not identity, but the examples corpus is a conformance
input. A conforming surface elaborator MUST therefore match these rules:

- Whitespace is space, tab, carriage return, newline. `;` starts a comment
  through the next newline. Delimiters are `()`, `[]`, and `{}`.
- Atoms are integer literals, float literals, rational literals, string
  literals, or symbols. Lexing tries, in order: (1) a decimal integer
  (arbitrary precision, `big.Int` syntax — optional leading `-`, then digits)
  → `int`; (2) a **float** — a token ending in `f` whose prefix matches the
  PORTABLE decimal float grammar `[+-]?( D+ (. D*)? | . D+ )([eE][+-]?D+)?` or a
  case-insensitive `[+-]?(inf|infinity|nan)` (`0.1f`, `1f`, `3.14f`, `1e9f`,
  `-2.5f`) → `float`. Hex floats (`0x1p4`) and digit-separator underscores
  (`1_000`) are NOT accepted, even where a host `ParseFloat` would — so a second
  kernel with a plain decimal parser agrees byte-for-byte; (3) a
  **rational** (`big.Rat` syntax: a decimal like `3.14`/`0.1`, or a fraction
  `num/den`, optionally signed) → `rat`, reduced; (4) otherwise a symbol.
  Integer is tried first (so `3` is `int`, not `3/1`); the `f` suffix is tried
  before rational (so `0.1f` is `float`, `0.1` is `rat`); a bare symbol like
  `f` or `fold` has no numeric prefix and is unaffected.
- String literals are delimited by `"`. Supported escapes are `\n`, `\t`,
  `\"`, and `\\`; any other backslash escape is rejected. Newlines inside
  strings are accepted and count for later line numbers.
- Lists produce `list`, square brackets `brack`, and braces `brace`.
- Top-level forms are `(data Name [tyvars] ctor...)` and `(defn name [tyvars]
  [(param ty) ...] ret body prop...)`.
- Type syntax: `Int`, `Rat`, `Float`, `Bool`, `Str`, type variables, data
  names, `(Data arg ...)`, right-associated `(-> a b c)`, and record types
  `{field Ty ...}`.
  Record fields are sorted ascending by raw symbol bytes during elaboration;
  duplicate fields are rejected.
- Term syntax: ints, rationals, floats, strings, `true`, `false`, variables, `(fn [(x ty) ...]
  body)`, `(let (x ty expr) body)`, `(if c t e)`, `(match scrut ((Ctor x ...)
  body) ...)`, record literals `{field expr ...}`, field access `(. expr
  field)`, list literals `(list e0 e1 ...)`, primitives, and named application
  `(name [tyargs] arg ...)`. The `[tyargs]` group MAY be omitted and inferred
  (§2.1).
- LIST-LITERAL SUGAR: `(list e0 e1 … eₙ)` elaborates to
  `(Cons e0 (Cons e1 … (Cons eₙ (Nil))))` with the constructors' type arguments
  omitted (inferred, §2.1); `(list)` is `(Nil)`. `list` is a reserved head — the
  `Nil` and `Cons` constructors must be in scope. Because it desugars to the same
  constructor chain the author would write, identity is unchanged.
- STRING-LITERAL SUGAR: a `"…"` literal elaborates to the codepoint chain
  `(SCons c0 (SCons c1 … (SCons cₙ (SNil))))`, where each `cᵢ` is the Unicode
  scalar value (a decimal `Int`) of the literal's `i`-th codepoint in order;
  `""` is `(SNil)`. `Str` is an ordinary datatype (§3) and the `SNil`/`SCons`
  constructors must be in scope. There is no string-literal term and no string
  primitive — a literal is byte-identical to the constructor chain an author
  would write by hand.
- Name resolution for a bare term name is, in order: local variable, the
  function currently being defined (emits `self`), constructor, stored function.
  Constructor lookup scans the current name index in ascending name order and
  chooses the first ADT whose metadata contains the constructor name.
- A constructor term is saturated by all remaining arguments in its surface
  application. Other applications elaborate to left-associated `app` chains.
- A `defn` body is wrapped in one `lam` per parameter, from last parameter to
  first, so the first surface parameter is the outermost lambda. Properties are
  elaborated in a separate term scope containing only their binders; they may
  refer to the function under proof via `self` by using the function's name.
- Match elaboration resolves constructor names, requires all arms belong to one
  ADT, rejects duplicates, orders arms by constructor index, and rejects
  non-exhaustive matches before hashing.

Errors are reported with best-effort source line numbers, but exact diagnostic
wording is not part of kernel identity.

### 1.5 Golden encoding fixtures

The conformance suite MUST include raw canonical-byte fixtures for at least:
raw (unescaped) strings containing quotes, backslashes, newlines, `<>&`, and
U+2028/U+2029; a negative `i64`; the bool byte; a 32-raw-byte hash
reference; empty lists (a bare zero count); a record whose encoding
witnesses the name-then-value pair layout in ascending name order; a
`rat` term whose encoding witnesses the reduced numerator/denominator
`bigint` pair (e.g. a negative, non-integer rational); and a `float` term
whose encoding witnesses the 8 canonical IEEE-754 bytes (e.g. a negative
non-integer value, or `-0.0` to witness that it is distinct from `+0.0`).

A second implementation should first pass these byte fixtures, then the full
examples corpus.

## 2. Static semantics (the gate)

Typechecking is bidirectional local synthesis. Every binder is annotated. A
reference to a generic definition or constructor MAY carry explicit type
arguments matching the target's `tyvars` count, OR omit them entirely, in which
case the typechecker INFERS them (§2.1) and BACKFILLS the solved arguments into
the AST before it is hashed — so an inferred call is byte-identical to the
explicit one and identity is unchanged. The only unknowns ever solved are these
omitted type arguments, by ONE-SIDED matching against argument and expected
types (never unification of two unknowns). `==` requires both operands the same
type, which must not contain a function type. `rec` types are legal only inside
`data` definitions. Prop binders must be concrete (no `var`/`rec`) and prop
bodies must be `Bool`. A definition failing any check MUST NOT be stored.

### 2.1 Type-argument inference (normative)

Checking runs in two modes: SYNTHESIZE a term's type, or CHECK a term against an
expected type `E`. The definition body is CHECKED against its declared type, and
every prop body against `Bool`; CHECK threads `E` through `if`/`let`/`match`/`lam`
(each branch/arm/body/codomain checked against the corresponding expected type)
and defaults, on any other form, to synthesize-then-compare. A generic reference
or constructor with OMITTED type arguments is solved as follows, over a solution
vector `S` of length `tyvars` (all initially unsolved):

- ONE-SIDED MATCH of a pattern type `P` (whose variable indices `< len(S)` are
  the unknowns) against a concrete type `G`: if `P` is one of those variables,
  bind it to `G` (or fail if already bound to a different type); otherwise `P`
  and `G` must have the same shape and their components match componentwise. A
  variable in `G` (an enclosing definition's type parameter) is an opaque
  constant a pattern variable may bind to.
- APPLICATION `(f a₁…aₖ)` with `f` a generic `ref`/`self`: peel one parameter
  type per applied argument from `f`'s type. In CHECK mode, match the result
  type (after peeling) against `E`. Then, for each argument that SYNTHESIZES a
  type, match its parameter type against it. If any `S` entry is still unsolved,
  the definition is rejected ("cannot infer type argument"). Backfill `f`'s type
  arguments from `S`; then CHECK each argument against its now-concrete parameter
  type (so an argument that could not synthesize — e.g. a bare `(Nil)` — is
  inferred from context).
- CONSTRUCTOR `(C a₁…aₖ)`: in CHECK mode, match the constructor's data type
  against `E`; then match each field type against every argument that
  synthesizes. Reject if any `S` entry is unsolved. Backfill and CHECK each
  argument against its now-concrete field type.
- PRIMITIVES split into groups. `and`/`or`/`not` have fixed `Bool` operands and
  CHECK against them. `%` is `Int`-only (it truncates). `fp-eq` is `Float`-only
  and returns `Bool` (IEEE equality, §3). The arithmetic and ordering
  primitives `+ - * / neg < <=` are NUMERIC-OVERLOADED over `Int`, `Rat`, or
  `Float`: SYNTHESIZE the first operand to fix the numeric kind, reject if it is
  none of the three, then CHECK every operand against that same kind (operands
  may not mix kinds). `+ - * neg` return the operand kind; `< <=` return `Bool`.
  `/` is admitted for all three but means different things: truncating integer
  division over `Int`, exact real division over `Rat`, IEEE division over
  `Float` (§3). `==` is polymorphic in
  its operand type: SYNTHESIZE whichever operand can be, then CHECK the other
  against it (so `(== xs (Nil))` infers the `(Nil)`); both must be the same
  non-function type, and — since the operands end at the same type — the
  backfilled arguments do not depend on which operand is synthesized first. An
  `==` where neither operand synthesizes (`(== (Nil) (Nil))`) is rejected.

Because matching is one-sided and every solution is checked structurally
afterward, inference never accepts an ill-typed term; and because the solved
arguments are the same the author would have written, the hash is unchanged. A
term whose type arguments cannot be determined this way (a bare `(Nil)` in
synthesize mode, a polymorphic reference passed as a value with no expected type)
is rejected, and the author writes them explicitly.

Detailed synthesis obligations:

- Type well-formedness checks type-variable bounds, data-reference existence,
  data arity, record field ordering/uniqueness, and `rec` placement. `rec` is
  legal only while checking an ADT's constructor fields and must be applied to
  exactly the ADT's type parameters.
- **Strict positivity.** A `data` definition MUST NOT place `rec` in a negative
  position: to the left of a function arrow, or inside the type arguments of a
  container that is not transitively arrow-free. Polarity flips on each function
  domain and is preserved through the codomain; a `rec` argument to an
  arrow-free (hence covariant) datatype keeps its polarity, otherwise it is
  treated as negative. This is required for soundness, not ergonomics: a
  negative datatype such as `data D = C (D -> D)` encodes nontermination with no
  syntactic self-recursion, which the structural termination checker (§6.1)
  cannot see and would wrongly certify `total`. The check conservatively
  over-rejects the (unusual) covariant-through-an-arrow container.
- Type substitution replaces `var i` with the `i`th type argument and recurses
  structurally through functions, data, records, and `rec`.
- Constructor field types are instantiated by substituting ADT type arguments
  and resolving `rec` to the ADT's concrete `data` type.
- Contexts are de Bruijn stacks: `ctx[len-1]` is `var 0`.
- `lam` checks its annotation is well formed, synthesizes the body under the
  extended context, and returns `fun(annotation, bodyType)`.
- `app` requires the function side synthesize to `fun(a,b)` and the argument
  synthesize exactly to `a`.
- `let` checks its annotation, requires the bound expression synthesize to that
  annotation, then synthesizes the body under the extended context.
- `if` requires a `Bool` condition and identical branch types.
- `record` terms synthesize record types from fields in canonical order.
  `field` requires a record type containing the requested field name.
- `ref`, `self`, and `ctor` require exactly the target's `tyvars` count in
  explicit type arguments; all type arguments must be well formed.
- `ctor` requires a data target, valid constructor index, and argument types
  exactly matching instantiated constructor fields.
- `match` requires a data scrutinee, the recorded match hash to match the
  scrutinee hash when present, exactly one arm per constructor, and all arm
  result types equal. Constructor fields are pushed into the arm context in
  declaration order; the first field is outermost and the last field is
  `var 0`.
- Primitive arities are fixed. Arithmetic and comparisons (`+ - * / neg < <=`)
  are numeric-overloaded over `Int`, `Rat`, or `Float` (operands share one
  kind); `%` is `Int`-only; `fp-eq` is `Float`-only → `Bool`; `and`/`or`/`not`
  are over `Bool`; and `==` is over equal first-order types only. There are no
  string primitives.
- The numeric conversions are unary and overloaded by SOURCE type, with a fixed
  result type: `to-rat` synthesizes an `Int` or `Float` operand → `Rat`;
  `to-float` an `Int` or `Rat` → `Float`; `floor` a `Rat` or `Float` → `Int`
  (an operand of any other type is a type error). Their runtime meaning is in
  §3 and their SMT translation in §7.

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
- **Integers** are ℤ — arbitrary precision. `+ - *` never overflow; `/`
  truncates toward zero; `%` takes the dividend's sign; division or modulo
  by zero is a runtime error. This matches the proof model exactly (the solver
  already reasons over unbounded integers), so there is no overflow caveat.
- **Rationals** (`Rat`) are ℚ — exact, arbitrary precision (numerator and
  denominator are unbounded integers, always kept in reduced form). `+ - *`
  are exact; `/` is exact real division (no truncation), so `(/ a b)` for a
  nonzero `b` is the true rational `a/b` and `(* (/ a b) b) = a`; division by
  zero is a runtime error. `neg`, `<`, `<=`, and `==` are the exact rational
  operations. Because ℚ is a field and Z3's real theory is complete, the
  algebraic laws IEEE floating point violates — associativity, distributivity,
  exact division-inverse — are provable (§7), and a decimal literal like `0.1`
  denotes exactly `1/10`, never a binary approximation. `Rat` and `Int` do not
  implicitly convert.
- **Floats** (`Float`) are IEEE-754 binary64. A value IS its 64-bit pattern;
  identity is bit-identity with every NaN canonicalized to one quiet pattern,
  so a value has one encoding. `+ - * / neg` are the IEEE operations rounding
  nearest-ties-even, and are TOTAL — division by zero yields `±inf` (`0.0/0.0`
  yields NaN), never a runtime error — with NaN canonicalized on every result.
  `< <=` are the IEEE ordered comparisons (a NaN operand ⇒ `false`). Structural
  `==` is **Leibniz** (bitwise on canonicalized values): `NaN == NaN` is
  **true** and `+0.0 == -0.0` is **false** — deliberately NOT IEEE equality.
  IEEE equality (`fp.eq`: `NaN ≠ NaN`, `+0.0 == -0.0`) is the separate opt-in
  primitive `fp-eq`. Because Z3's float theory is complete (§7), true float
  properties are provable; the algebraic laws IEEE floats break (associativity,
  `0.1f + 0.2f == 0.3f`) are falsified, correctly. `Float` does not implicitly
  convert to/from `Int` or `Rat`; a decimal without the `f` suffix is a `Rat`.
- **Numeric conversions** are explicit (there is no implicit coercion):
  - `to-rat` — `Int → Rat` is exact (`n ↦ n/1`); `Float → Rat` is exact for a
    finite float (every finite binary64 is a dyadic rational) and a RUNTIME
    ERROR on NaN or ±inf.
  - `to-float` — `Int → Float` and `Rat → Float` round to the nearest binary64,
    ties to even (total).
  - `floor` — rounds toward −∞ to an `Int`. `Rat → Int` is total; `Float → Int`
    is a RUNTIME ERROR on NaN or ±inf. (`floor` names its rounding rather than
    hiding a convention; other roundings are definitions built from it.)
  The NaN/inf errors follow the division-by-zero idiom: the narrowing has no
  answer, so it faults rather than inventing one. The total, exact directions
  (`Int→Rat`, `Int/Rat→Float`, `Rat→Int`) are provable (§7); the partial
  `Float→{Rat,Int}` directions are outside the proof fragment, like `Int` `/`.
- **Strings** are NOT primitive. A string is a value of the ordinary datatype
  `(data Str [] (SNil) (SCons Int Str))` — a sequence of Unicode scalar values
  (each an `Int` codepoint), built with the `SNil`/`SCons` constructors. It
  evaluates, matches, and compares (`==` structural on constructor index and
  fields) exactly like any other datatype; there are no string operators.
  Every string operation (length, concatenation, prefix test, substring,
  split, …) is a definition over `Str`, proven like any other. A `"…"` literal
  is surface sugar for the corresponding `SCons` codepoint chain (§1.4). The
  codepoint convention is: `SCons` carries a Unicode scalar value; enforcing
  its range is a future refinement, not part of this datatype.
- **Structural equality** (`==`) on data and records compares constructor
  index and fields recursively; applying it to function values is a runtime
  error (statically prevented).

### 3.1 Resource bounds

Two independent bounds, both normative for verdict reproducibility:

- **Fuel**: each term-node evaluation and each function application
  consumes 1 unit. Exhaustion is a runtime error.
- **Depth**: nested evaluation deeper than 100,000 is a runtime error
  (the fuel bound alone does not prevent host-stack exhaustion).

### 3.2 Runtime values and printing

Runtime values are erased: `int`, `rat`, `float`, `bool`, `closure`, `data`,
`record`, and generated `native` functions. Closures store an environment, lambda term,
and enclosing self hash. Native functions are used only by deterministic
generation: identity, affine, constant, and finite table.

Value printing is normative for counterexamples and conformance:

- Int and Bool print as decimal and `true`/`false`. Rat prints in lowest
  terms: an integer-valued rational as a bare integer (`3`, `-2`), otherwise
  `num/den` with the sign on the numerator (`1/2`, `-7/4`). Float prints with
  an `f` suffix so it round-trips and is distinct from a Rat: a finite value as
  its shortest round-tripping decimal + `f` (`0.5f`, `1f`, `-0f`,
  `0.30000000000000004f`), and the specials as `inff`, `-inff`, `nanf`. The
  finite form is the shortest decimal that parses back to the same binary64,
  formatted as Go's `strconv.FormatFloat(f, 'g', -1, 64)` (fixed-point within a
  bounded decimal-exponent range, else scientific with a lowercase `e`); this
  is a conformance-sensitive detail, like SMT string escaping.
- Strings, being `Str` datatype values, print as data (see below) — a chain of
  `(SCons <codepoint> …)` ending in `SNil`.
- Records print `{name value ...}` in canonical field order.
- Data values print `Ctor` for nullary constructors and `(Ctor field ...)`
  otherwise, using current metadata names where available.
- Closures print `<fn>`.
- Native functions print `<fn x. x>`, `<fn x. A*x + B>`, `<fn _. V>`, or
  `<fn {K→V ...} else D>` with table entries in generation order.
- A property runtime error counterexample appends two spaces and `(runtime
  error: MESSAGE)` after the comma-separated printed inputs.

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

Size is a recursion budget and is clamped to a minimum of 0 on entry to every
generation call. Data fields recurse at `size - 1` (below), so without the
clamp a `Str` nested inside a data value could be reached at size `-1` and draw
`below(0)` — a division by zero. The clamp is normative: it fixes the draw count
(and therefore the generated value) for the previously-undefined negative-size
case.

Generation by type — draw order is normative:

- `Int`: draw `below(4)`; on 0, draw `below(5)` into boundary table
  `[-2,-1,0,1,2]`; otherwise draw `intIn(-20,20)`.
- `Rat`: draw the numerator `intIn(-8,8)`, then the denominator `intIn(1,5)`
  (numerator first), and reduce to lowest terms. The denominator range starts
  at 1 so integer-valued rationals occur, and a zero numerator yields `0/1`;
  the reduction makes the resulting value canonical.
- `Float`: draw `below(4)`; on 0, draw `below(9)` into the boundary/special
  table `[+0.0, -0.0, 1.0, -1.0, 0.5, 2.0, +inf, -inf, NaN]` (so ±0.0, the
  infinities, and NaN are all exercised); otherwise draw the numerator
  `intIn(-8,8)`, then the denominator `intIn(1,4)` (numerator first), and take
  `numerator / denominator` as a binary64. Every generated NaN is canonical.
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
  field (error if none); else uniformly among all constructors — and the selection ALWAYS consumes exactly one below(k) draw, including when k = 1 (single-candidate selection is not skipped, in either size branch). Fields
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
  `nonrecursive` if no self-calls and all callees total; `measure` if a
  Z3-verified integer ranking function bounds the recursion (§6.1) when
  structural descent fails; else `unknown`. `structural`, `nonrecursive`, and
  `measure` are the total verdicts.
- **Confinement**: a higher-order parameter is `confined` if every
  occurrence is applied, projected-and-applied, passed at the same position
  in a self-call, or passed whole to a callee position already `confined`;
  any other use (returned, stored, captured under an inner `lam`, projected
  without application) makes it `escapes`.
- **Spec strength**: mutation catalog = type-preserving operator swaps
  (`+↔-`, `*→+`, `/→*`, `%→/`, `<↔<=`, `and↔or`), operand swaps of
  non-commutative binary prims (`- / % < <=`), integer literal ±1 and →0,
  if-branch swap. Score = killed/total. Spec strength is
  computed for every function definition independent of its termination verdict:
  a `measure`-total function is mutated exactly like a `structural` one.
- **Cross-check (N-version)**: for two definitions with identical signatures,
  each one's properties are evaluated against the other's body. Mutation scores
  a spec's tightness around ITS OWN body and is blind to misalignment (a spec
  tight around the wrong function); the cross-check is the disagreement between
  two independently-authored specs of one brief. Verdict `AGREE`/`DISAGREE`.

### 6.1 Termination algorithm

The termination checker analyzes only the function body after stripping the
top-level lambda spine. Let there be `n` parameters. The initial relation
environment maps each parameter binder to `eq(i)` for its parameter position;
new binders with unknown size relation receive `nil`.

- A relation is a map `parameter-index -> eq|lt`; `ltOf(r)` turns every entry
  of `r` into `lt`.
- `argRel(term, env)` returns the relation of a variable term, accounting for
  de Bruijn indexing, or `nil` for any non-variable.
- The walker carries an application spine. In `app`, it walks the function
  with `argRel(argument)` prepended to the spine, and separately walks the
  argument with an empty spine.
- Seeing `self` records one call site with the current spine. Seeing `self`
  without a full application spine is therefore conservative and will fail the
  descent test.
- `lam` and `let` push `nil`. `match` first walks the scrutinee. If the
  scrutinee is a variable, every constructor field binder in every arm gets
  `ltOf(scrutineeRelation)`; otherwise fields get `nil`.
- Other terms recursively walk subterms with an empty spine.

The verdict is:

- `unknown` if any body-referenced function has missing metadata or a
  non-total termination verdict;
- `nonrecursive` if no self-call sites are recorded;
- `structural` if a LEXICOGRAPHIC order of parameter positions discharges
  every recorded self-call site (below);
- `measure` if the integer-ranking-function check (§6.1.1) succeeds;
- `unknown` otherwise.

The lexicographic search considers, per site and position `j`, only the
diagonal relation — how the argument passed at position `j` relates to
parameter `j` itself: `lt` (strict subterm), `eq` (the parameter unchanged),
or unknown. A position may head an order iff every remaining site is `lt` or
`eq` there and at least one is `lt`; sites at `lt` are discharged, sites at
`eq` must be discharged recursively by the remaining positions, and the
search backtracks over head choices. A single always-descending position is
the length-1 case, so this strictly extends the earlier rule: previously
`structural` verdicts are unchanged, and merge-style functions that
alternate descent between two arguments now verify. Soundness: each
self-call strictly decreases the tuple of parameters in the chosen order
under the well-founded subterm relation. Nested recursion whose outer
argument is a constructor application (Ackermann-style) is still `unknown`:
relations track variables only, and a non-variable argument has no relation.

Only refs in the body count as callee obligations; properties are not
production code and do not affect totality.

#### 6.1.1 Integer ranking functions

When structural descent fails, the checker attempts a well-founded integer
MEASURE: an integer expression over the parameters that strictly decreases and
stays non-negative at every self-call, given the path guards. This reaches
functions that recurse on an integer COUNTER rather than a shrinking datatype
(`replicate n x` on `n → n-1` guarded by `n>0`; `range lo hi` on `lo → lo+1`
guarded by `lo<hi`). Because the counter is unbounded, the guard is essential
and the check is discharged by the same solver host as §7.2. A kernel with no
solver host available takes the conservative branch (no `measure` verdict).

1. Strip the parameter lambdas. Declare each parameter as an SMT constant at its
   REAL sort — `Int` as `Int`, a datatype/record over its declared sort (so its
   field selectors are well-formed for a field measure, #57) — except a
   type-variable parameter, which has no ground sort and is declared over a fresh
   uninterpreted sort so translations mentioning it stay well-formed. The check
   fails only if step 3 yields NO candidate measure (a function with no `Int`
   parameter, `Int` datatype field, or `Str` parameter cannot be ranked here).
2. Walk the body collecting self-call SITES. Each site records the path guards
   reaching it and the SMT expression passed at each parameter position:
   - `if c t e`: translate `c`; walk `t` with guard `c` added and `e` with
     `(not c)` added. If `c` is untranslatable, both branches are POISONED.
   - `let`: bind the variable to its translated value (or, on failure, a fresh
     constant of the bound type) and walk the body.
   - `match`: walk the scrutinee; for each arm, bind the constructor's fields to
     the scrutinee's SELECTORS applied to the scrutinee's translated expression —
     so a measure over a datatype field (#57) stays connected to the counter the
     body reads (`(match r ((Run n v) …))` binds `n` to `(cnt r)`) — and add the
     constructor tester `((_ is Ctor) scrut)` as a path guard, omitted for a
     single-constructor datatype where the tester is always true. Walk the arm.
     If the field sorts or selectors cannot be determined, the arm is poisoned.
   - `lam` inside the body: bind its parameter to a fresh constant and walk.
   - an application spine bottoming at `self`: record a site with the spine's
     argument terms. A `self` reached with fewer arguments than parameters, on
     a poisoned path, or with an untranslatable argument sets a hard-fail flag.
   - all other terms: walk every subterm.
   If the hard-fail flag is set or no site is recorded, the check fails. (This
   COMPLETENESS is the soundness condition: every self-call must be discharged,
   so any call the walk cannot fully analyze forfeits the whole attempt.)
3. Candidate measures, in order: each `Int` parameter `p_i` by itself, then each
   ordered difference `p_i - p_j` of two distinct `Int` parameters, then each
   `Int`-typed FIELD of a single-constructor datatype parameter `p_i` as its
   selector applied to the parameter, `(selector p_i)` (#57 — the counter inside
   rle-expand's `Run`). (Recursion over a string needs no special measure:
   `Str` is a datatype, so a string-shrinking recursion is ordinary structural
   descent.)
4. A candidate μ succeeds iff, for EVERY site, the solver returns `unsat` for
   `(assert (and <site guards> (not (and (< μ(args) μ(params)) (>= μ(params) 0)))))`
   — i.e. `guards ⟹ (μ(args) < μ(params) ∧ μ(params) ≥ 0)` is valid. μ(params)
   substitutes each `p_i`; μ(args) substitutes the site's argument expressions.
   The obligation carries ONLY the parameter/binder declarations, the site
   guards, and the negated decrease — NO callee defining-equation axioms.
   Including a quantified defining axiom would make the query undecidable and let
   the solver answer `unknown`, so the verdict would stop being a pure function
   of the definition; omitting them only weakens the premises (it can never yield
   a false `measure`) and keeps the obligation decidable linear-integer
   arithmetic. The first candidate that clears every site yields `measure`.

   Translation of guards and arguments (per §7's term→SMT rules) runs on the
   UNINSTANTIATED definition, so it may meet a polymorphic type variable (a
   constructor type-argument, or a `let`/`match`/`lam` binder sort). Resolve
   every type variable to a fixed placeholder sort so translation stays total;
   this is sound because measures are built only from `Int` parameters, so a
   subterm of type-variable type is either irrelevant to the chosen μ or makes
   the site's obligation non-`unsat` — the candidate is then rejected, never
   spuriously accepted.

The verdict is a pure function of the definition and the solver's linear-integer
decision procedure (which is complete, so it never reports the solver's
`unknown`): no wall clock or rlimit participates, so `measure` is deterministic
and reproducible across kernels. Soundness: a strictly decreasing sequence of
non-negative integers is finite, so a μ clearing every site witnesses that no
infinite self-call chain exists. The measure ranges over parameters and, for a
single-constructor datatype parameter, its `Int`-typed fields (#57) — reaching a
counter extracted by `match`, as in rle-expand's `Run`.

### 6.2 Confinement algorithm

Confinement is computed for each top-level parameter whose type contains a
function. First-order parameters receive an empty verdict.

For a parameter at surface position `i`, the variable has de Bruijn index
`nparams-1-i` in the stripped body. The checker walks the body with that target
index and an `inLam` flag initially false:

- A bare occurrence of the target variable is an escape.
- Direct application `(f ...)` of the target variable is allowed only when not
  under an inner lambda AND the application result is data: walking the
  parameter's declared type through one arrow per applied argument must leave
  a type free of function types. A partial application of a curried
  capability — or a capability whose result is function-valued — is a closure
  DERIVED from the capability, and letting it flow as data would smuggle the
  capability out. A type variable in result position counts as data: what a
  caller instantiates it with is that caller's own closure to analyze.
- Projection-and-application `((. f field) ...)` is allowed under the same
  result rule, using the projected field's declared type.
- Passing the whole parameter as an argument to `self` is allowed only at the
  same parameter position currently being checked.
- Passing the whole parameter as an argument to a referenced callee is allowed
  only when the callee metadata says that argument position is `confined`.
- A LAMBDA literal passed (not under an inner lambda) to a callee position
  whose metadata says `confined` is a blessed closure: the callee only ever
  invokes it during the call and never keeps it, so the walk continues into
  the closure body with the inner-lambda flag RESET (and the target index
  shifted by the closure's binders). Every smuggling route out of the closure
  is still caught by the ordinary rules — returning the capability bare,
  capturing it in a further unblessed lambda, or escaping a partial
  application. This admits the wrapper idiom
  `(map (fn [u] ((. net fetch) u)) urls)`.
- Any other occurrence under an inner lambda, stored in a constructor or
  record, returned as a value, let-bound and later used bare, or projected
  without application is an escape.
- `let` and `match` adjust the target de Bruijn index by the number of binders
  they introduce.

If all occurrences are allowed the verdict is `confined`; otherwise `escapes`.

### 6.3 Mutation algorithm

Mutation scoring traverses the body pre-order over `A`, `B`, `C`, `Args`, then
`Arms`. For each single-node mutation, the whole definition is deep-copied,
typechecked, hashed, and skipped if its hash was already seen. The original
hash is pre-marked as seen. Mutants are not stored in the object database, but
are cached in memory under their candidate hash while properties run.

Mutations are attempted in this order at each node:

1. Primitive operator substitutions listed above, in the per-operator order
   shown by this document's catalog.
2. Operand swap for swappable binary primitives.
3. Integer literal to `old+1`, `old-1`, and `0`, skipping unchanged values.
4. If-branch swap.
5. At an `app` node whose function side is an `app`: swap the two adjacent
   call arguments.
6. At an `app` chain whose head is `self`: replace the whole chain by each
   spine argument in turn ("forgot to recurse").
7. At a `ctor` node: swap each adjacent argument pair.
8. At a `match` node: swap each arm-body pair `(i, j)`, `i < j`, in index
   order (de Bruijn policing makes cross-arity swaps fail the gate); then
   replace the whole match by the body of each arm whose constructor binds
   zero fields ("always take the base case").

Structural mutants rely on the typecheck gate as their filter — generators
are liberal, and only candidates that still typecheck count toward the total.
Surviving mutants can be semantically equivalent to the original (e.g. `<=`
vs `<` where equal elements are indistinguishable); the score's denominator
honestly includes such unkillable mutants rather than special-casing them.

Each mutant runs properties in property order with `mutantCases=60` and
`mutantFuel=500000`, seeded by the mutant hash using §4. The first property
that fails or errors kills the mutant. Survivors are rendered with the
projection printer.

### 6.4 Cross-check algorithm (N-version specification)

Mutation (§6.3) measures a spec's tightness around its own body; it cannot see
a spec that is internally tight around the WRONG function (a sum-of-squares
body whose every property is a true statement about sum-of-squares proves and
resists mutation exactly as a correct one would). The brief is not an object in
the system, so no single-spec analysis can read intent. The cross-check is the
defense with the right shape: independent redundancy. Two definitions authored
by disjoint processes for the same brief are made to collide.

Given two names resolving to hashes `hA` and `hB` with definitions `dA`, `dB`:

- **Preconditions (the analysis is REFUSED, not scored, if any fail).** Both
  MUST be `func`. `hA` and `hB` MUST differ (a definition cross-checked against
  itself is vacuous — identical structure content-addresses to one object).
  `dA` and `dB` MUST have identical signatures: equal `TyVars` and type-equal
  `Ty`. Cross-binding relies on the property-input generators of one side
  producing values admissible to the other body, which identical signatures
  guarantee.
- **Procedure.** For each property `i` of `dA`, run §4 generation seeded from
  `hA` exactly as `verify` would (base = first 8 bytes of `hA`; per-property
  and per-case seeding per §4), but evaluate the property body with the
  `self` reference bound to `hB` (i.e. `dB`'s body). Symmetrically, each
  property of `dB` is seeded from `hB` and evaluated with `self` bound to `hA`.
  Each property runs `propCases` (200) cases with `propFuel`. Seeding from the
  property OWNER's hash — not the body's — means the cross-run and an ordinary
  `verify` of that owner draw the identical input stream, so a falsifying
  counterexample is directly comparable across the two bodies.
- **Verdict.** `AGREE` iff no property of either side is falsified against the
  other's body; `DISAGREE` otherwise. Because `self` is name-free (§1), the
  rebinding is purely mechanical. The verdict is a pure function of
  `(hA, hB, store)` and is therefore reproducible and cross-kernel comparable.
  On `DISAGREE`, the falsifying `(side, property, counterexample)` localizes the
  divergence; WHICH property falsifies names which author's function the other
  body implements. Detection is mechanical; a human adjudicates which body
  matches the brief.
- **Effect.** Read-only: the cross-check never rejects or mutates a stored
  definition. It MAY append a journal entry (§8) of kind `cross` with status
  `accepted` (AGREE) or `falsified` (DISAGREE) naming both hashes, as durable
  provenance that the two specs were reconciled.

Honest limits, normative to record: two authors misaligned to the SAME wrong
function still agree; intent enters as an axiom from a trusted party regardless.
Redundancy lowers the probability of undetected misalignment the way mutation
lowers the probability of undetected weakness; neither reaches zero.

## 7. Proof obligations (SMT boundary)

The provable fragment and its translation, normative for outcome
reproducibility (given the same solver):

- Sorts: `Int`, `Bool`; monomorphic data instances as algebraic datatypes
  (names derived from metadata per §7.1 — semantically inert for outcomes, but
  BYTE-significant: script identity is fixtured, §7.2); records as
  single-constructor datatypes; function types as `(Array dom cod)` applied via
  `select`. `Str` is not special — it is a monomorphic datatype instance
  (`SNil`/`SCons`) and translates as an ADT like any other, so Z3's sequence
  theory is not used at all.
- Primitive translation: arithmetic and comparison operators map to their
  SMT-LIB counterparts. There are no string primitives to translate — string
  operations are ordinary recursive definitions over the `Str` datatype, so
  their properties discharge by the structural / measure induction below, never
  by a string decision procedure.
- Non-recursive callees are inlined (beta-reduction); recursive callees are
  declared uninterpreted. Their defining equation is asserted as a universally
  quantified axiom with the application as pattern **only when the callee is
  proven total** (termination `structural` or `nonrecursive`, §6.1). This gate
  is a soundness requirement: the defining equation of a non-terminating
  function can be inconsistent (e.g. `f x = f x + 1` ⟹ `∀x. f(x) = f(x)+1`,
  which is UNSAT), and an inconsistent axiom lets the solver discharge every
  goal by ex falso — certifying false properties and, through the lemma
  library, poisoning every dependent. A non-total recursive callee is therefore
  left uninterpreted with no defining equation: sound, merely weaker (goals that
  needed its definition return `unknown`). Registration is EAGER and the
  totality gate governs only the ASSERT: every recursive callee is declared
  and its body translated at first touch regardless of totality — so a
  non-total callee's own callees get declared too. This is byte-visible in
  the script fixtures as orphan `declare-fun`s (`fn_rle_expand` in the
  rle-encode goldens arrives through non-total `rle-decode`'s body). If this
  eager body translation reaches an operator EXCLUDED from translation (see the
  exclusion list below), the callee's body cannot be fully registered, and the
  direct-attempt script is NOT emitted for any property whose goal triggers it:
  the property is recorded unprovable with no script, and its line is ABSENT
  from `prove/scripts.txt`. The verdict is unchanged — the callee is
  uninterpreted, so the goal would return `unknown` from an emitted script
  anyway. (Until #71 this rule's canonical witness was a decimal `show` over
  `Int`, which recurses on `n / 10`. That example is now OBSOLETE: `/` and `%`
  over `Int` translate, and `show-nat` consequently GAINS a script and proves.
  The rule still governs the operators that remain excluded, and it is
  witnessed again: `excluded-witness.reaches-excluded-op`
  (examples/exclusion.oath) reaches a PARTIAL APPLICATION through a non-total
  callee's body, so its line is ABSENT from `prove/scripts.txt` while its
  sibling `independent-of-exclusion` emits one — pinning both the suppression
  and the fact that it is PER GOAL, not per definition. Do not delete that
  definition to tidy the corpus; it looks pointless precisely when it works.) Additionally, a property already
  refuted by deterministic testing (§4) is never recorded as proven even if the
  solver reports it valid — the concrete counterexample governs.
- `match` translates to tester/selector ite-chains.
- **`/` and `%` over `Int` (normative, #71).** The kernel's `/` truncates toward
  zero and its `%` takes the DIVIDEND's sign (Go `big.Int` `Quo`/`Rem`), whereas
  SMT-LIB's `div`/`mod` are Euclidean (`0 ≤ (mod a b) < |b|`). They agree when
  the dividend is non-negative and differ otherwise, so `div`/`mod` MUST NOT be
  emitted directly — that would prove a different theorem. They are instead
  DEFINED in terms of them. A kernel translating an `Int` `/` or `%` MUST emit
  these two definitions, verbatim and exactly once each, before first use, and
  translate `(/ a b)` to `(oath_tquo A B)` and `(% a b)` to `(oath_trem A B)`:

  ```
  (define-fun oath_tquo ((a Int) (b Int)) Int
    (ite (>= a 0) (div a b)
      (ite (= (mod a b) 0) (div a b)
        (+ (div a b) (ite (> b 0) 1 (- 1))))))
  (define-fun oath_trem ((a Int) (b Int)) Int
    (ite (>= a 0) (mod a b)
      (ite (= (mod a b) 0) 0
        (- (mod a b) (ite (> b 0) b (- b))))))
  ```

  For `b ≠ 0` this is exactly the kernel's semantics: it is the unique `(q, r)`
  with `a = b·q + r`, `|r| < |b|`, and `r` sharing `a`'s sign or zero — which is
  machine-checkable, and `oathrs`'s conformance run is expected to reproduce the
  emitted bytes rather than re-derive the algebra.

  **Division by zero is deliberately left unconstrained.** In the kernel `/` and
  `%` by zero are ERRORS — the operations are partial — so there is no value to
  model. SMT-LIB leaves `div`/`mod` by zero unspecified, and the definitions
  above inherit that, making the translated term an arbitrary fixed value there.
  This is the SOUND direction: a property whose truth depends on the result of
  division by zero simply will not prove, because the solver may choose any
  value. A kernel MUST NOT "helpfully" pin a zero-divisor result (e.g. to 0);
  doing so would prove properties the kernel's own evaluator cannot run.

  Emit the definitions ONLY when an `Int` `/` or `%` is actually translated, so
  scripts for goals that use neither are byte-identical to those emitted before
  this rule existed. Two placement details are BYTE-significant, hence normative:
  - **Both, together, on first use of either.** Translating an `Int` `/` emits
    `oath_trem` as well, and vice versa. A kernel MUST NOT emit only the operator
    it happens to need.
  - **At the point of first use, not hoisted.** The pair is appended to the
    DECLARATION stream at the moment the first `Int` `/` or `%` is translated, so
    it sits after whatever declarations were already accumulated and before those
    that follow. A kernel MUST NOT float them to the top of the script.
  - **Operands first.** Within the triggering application itself, the OPERANDS are
    translated before the pair is appended — so any declaration first touched by
    an operand precedes the two definitions. (This is forced for `/`, which is
    numerically overloaded: the `Int` case is not known until operand 0's sort
    is. `%` is statically `Int`-only per §2.1 and could in principle be
    pre-scanned, so the ordering is stated rather than left to inference.)
  - **The code fence above is INDENTED for markdown; "verbatim" means the
    DEDENTED text.** Each definition is `\n`-terminated with no blank line
    between them. A kernel that copies the leading two spaces literally will fail
    every affected hash, and because `prove/scripts.txt` stores only sha256 there
    is no expected text to diff against — so this is called out explicitly.
- **Excluded, permanently or pending**: partial application; lambda values in
  argument position; `hmac-sha256` and `bytes-eq-ct` (§1, the trusted crypto
  boundary — permanently, by design rather than pending). Note the exclusion
  bites on two distinct paths: eager registration of a non-total recursive
  callee (above), and — for a NONRECURSIVE definition, which is inlined — the
  excluded operator landing directly in the goal. Both yield the same outcome:
  no direct-attempt script, the property recorded `tested`, its line absent from
  `prove/scripts.txt`. Note `/` over `Rat` (exact real division) and `/` over
  `Float` (IEEE `fp.div`) are NOT excluded — both translate faithfully
  (§7.1), so rational division-inverse and float division laws are provable.
- Proof search: direct (assert negation, check-sat), then structural
  induction on each datatype-typed binder in order — one subgoal per
  constructor, induction hypotheses for datatype-recursive fields with all
  other binders universally generalized.
- Lemma library: proven properties of transitively referenced definitions,
  and previously proven properties of the definition itself, are asserted
  as axioms — a property is never an axiom in its own proof.
- `sat` on a quantifier-free direct attempt is a refutation (report the
  model); `sat`/`unknown` otherwise is merely "unproven".
- Integers are unbounded on both sides — the solver reasons over ℤ and the
  kernel's `Int` is arbitrary precision — so proofs hold without an overflow
  caveat.

### 7.1 SMT-LIB generation details

- Z3 is invoked as `z3 -in` with a 15 second timeout per solver call. Timeout,
  process failure, stderr-only output, or output not beginning with `sat` or
  `unsat` is `unknown`, never proof.
- SMT identifiers are produced by replacing every character outside
  `[A-Za-z0-9]` with `_`. This is not guaranteed collision-free; fixture
  coverage must include collision-prone metadata names before relying on such
  names in a public store.
- Negative integer literals render as `(- N)`. Non-negative integers render in
  decimal.
- `Rat` translates to the SMT sort `Real` (Z3's linear real arithmetic is a
  complete, decidable theory — the reason `Rat` is a primitive rather than a
  structural datatype). A rat literal `num/den` renders as `(/ NUM DEN)` with
  a negative numerator rendered `(- N)` as above; `0/1` is `(/ 0 1)`. The
  numeric-overloaded prims propagate the operand sort: `+ - * neg` over `Real`
  stay `Real`, `< <=` yield `Bool`, and `/` over `Real` is admitted (exact
  real division) — in contrast to `/` over `Int`, which is excluded below.
- `Float` translates to the SMT sort `Float64` = `(_ FloatingPoint 11 53)`
  (Z3's float theory, FPA, is decidable — the reason `Float` is a primitive).
  A float literal renders as `(fp (_ bvS 1) (_ bvE 11) (_ bvM 52))` from its
  exact canonicalized bits (sign, 11-bit exponent, 52-bit mantissa) — no
  rounding, one literal per value. Over `Float` operands the prims translate to
  FPA: `+ - * /` to `(fp.add RNE …)`/`(fp.sub RNE …)`/`(fp.mul RNE …)`/
  `(fp.div RNE …)` (round-nearest-even), `neg` to `(fp.neg …)`, `< <=` to
  `(fp.lt …)`/`(fp.leq …)`. Structural `==` over `Float` is SMT `=` (Leibniz —
  `NaN = NaN`, `+0.0 ≠ -0.0`), matching kernel identity; the `fp-eq` primitive
  is IEEE `(fp.eq …)`. `/` over `Float` is admitted (total IEEE division), in
  contrast to `/` over `Int`.
- Numeric conversions translate on their total, exact directions: `to-rat` of
  an `Int` is `(to_real N)` → `Real`; `to-float` of an `Int` is
  `((_ to_fp 11 53) RNE (to_real N))` and of a `Rat` is `((_ to_fp 11 53) RNE
  R)` → `Float64`; `floor` of a `Rat` is `(to_int R)` → `Int` (SMT `to_int` is
  floor). The `Float`-source conversions `to-rat`/`floor` are PARTIAL (NaN/inf
  have no rational/integer) and are excluded from the fragment, like `/` over
  `Int`.
- SMT string literals double `"` characters inside the SMT string. Other
  string escaping must match SMT-LIB accepted literal syntax and the examples
  corpus.
- Function types translate to array sorts `(Array dom cod)` and application of
  function values translates to nested `select`.
- Record sorts are named `Rec_<field>_<sort>...` in canonical field order and
  declared as single-constructor datatypes. Field selectors are
  `mk_<recordSort>_<field>`.
- Data sort names start with sanitized metadata definition name plus sanitized
  type-argument sorts. Constructor names are sanitized constructor metadata
  plus `_` plus the data sort name. Selector names are `<constructor>_<fieldIndex>`.
- Declared function symbols are `fn_` plus the sanitized metadata definition
  name plus sanitized type-argument sorts, `_`-joined (`fn_length_Int`;
  monomorphic `fn_spin`). The prefix keeps function symbols from colliding
  with sort or constructor names under sanitization.
- Byte form of a constructor declaration: `(<ctor> <selectors...>)` with
  selectors space-joined after a leading space — a NULLARY constructor
  therefore renders with a trailing space, `(Nil_List_Int )`. Scripts are
  byte-fixtured (§7.2), so this detail is normative.

### 7.2 Calls, lemmas, and induction

- Calls must be fully applied. Partial application and over-application are
  outside the provable fragment.
- Non-recursive callees are inlined by beta-reducing through their top-level
  lambda spine after type substitution.
- Recursive callees are declared with `declare-fun`; their defining equation is
  asserted as a quantified axiom over the top-level parameters. The pattern is
  the full function application, EXCEPT for a callee whose termination verdict is
  `measure` (integer-counter recursion, §6.1.1): its axiom is asserted with NO
  pattern. An integer-recursive pattern would E-match without bound — `f(n)`
  instantiates `f(n-1)` instantiates `f(n-2)`…, with none of the datatype
  acyclicity that halts the descent for structural recursion — so any goal
  mentioning the function would diverge. Pattern-free, the solver falls back to
  model-based instantiation, which terminates and discharges both the function's
  direct laws and the recursion-induction obligations (§7.2). This is part of the
  current proof fragment and must be treated as trusted proof-kernel behavior by
  a conforming implementation.
- `self` inside a property means the definition under proof and is translated
  the same way as a call to the definition's hash.
- Candidate-lemma closure: the transitive dependency closure of the
  definition under proof, where a definition's dependencies are EVERYTHING
  its body AND its properties reference — uniformly, at the seed and at
  every traversal step (a dependency's own properties extend the closure).
  Traversal order is irrelevant: only MEMBERSHIP matters, because
  candidates are collected first and then translated in ascending
  (definition-hash, property-index) order. For every function member,
  metadata `proven_props` indices contribute candidates.
- A candidate lemma whose formula fails to translate is skipped, but
  declarations and axioms already accumulated by the partial translation
  REMAIN in the context — there is no rollback. This is deterministic
  (canonical candidate order) and byte-visible in the script fixtures: a
  script may declare sorts or functions that no surviving assertion
  mentions (`Option_Int` in the q-drop goldens arrives this way).
- Previously proven properties of the definition itself are also lemmas, tagged
  by property index. A property's own lemma is excluded while proving that
  property. A property proven earlier in the same `prove` run becomes a lemma
  for later properties.
- **Self-lemma availability is a TWO-LEVEL fixpoint, not a single ordered
  pass.** Inner level (growth): within a run, iterate proving (each
  property's lemma set = every *other* proven property of the definition,
  whether proven earlier in this pass or in a prior one) until no new
  property proves. Attempts proceed in ascending property-index order, and
  an in-run proof joins the lemma set IMMEDIATELY (available to the next
  attempt of the same pass, not deferred to the next pass) — this
  Gauss-Seidel path is normative, not just the stability criterion: when
  several self-consistent states exist, the path from the start state
  selects which one is reached, so two kernels must walk the same path. A single declaration-order pass is insufficient — the
  canonical witness is `reverse`: `involution` (index 0) is provable only
  with `antidistributes-over-append` (index 1) as a lemma, so it proves on
  the second iteration. (Found by independent implementation: a literal
  one-pass reading yields 188/189 conformance.)
  Outer level (RUN STABILITY, normative): the recorded `proven_props` MUST
  be a fixpoint of the whole-run map F(S) = the proven set produced by
  running the inner fixpoint with S as the recorded state. Growth alone is
  not enough, because a budget-limited solver is NON-MONOTONE in its axiom
  set: a property proven early in a cold run (small lemma set) is not
  necessarily re-provable from the final state — extra lemmas can divert
  the search into budget exhaustion. The corpus witness is `q-drop`:
  `drop-back-only` proves from `{drop-front-nonempty}` alone but NOT once
  `drop-empty` is also asserted, so a single cold pass records a proof the
  recorded state cannot reproduce, and the next run silently drops it —
  warm and cold runs land on different verdicts. A kernel MUST therefore
  iterate F until S = F(S) before recording (the reference bounds this at
  8 rounds and has never seen more than 3): every recorded proof is then
  re-derivable from exactly the state the store records, and warm/cold
  runs converge to the same self-consistent verdicts. Conformance
  outcomes (`prove/outcomes.json`) are the limit of F from the empty
  state.
- **Lemma relevance (normative).** A goal's *footprint* is the smallest set
  of definition hashes containing the definition under proof and every
  definition referenced by the property's binders and body, closed
  transitively through definition BODIES (a member's body references are
  members; props do not extend the footprint). Lemma admissibility: a lemma
  belonging to the definition under proof is admissible unconditionally
  (sibling lemmas are the self-lemma fixpoint's foundation — their proof
  chains may route through symbols the goal never mentions); a DEPENDENCY
  lemma is admissible iff its own definition and every definition its
  binders/body reference lie inside the footprint. Footprint membership
  and the admissibility test cover DATA definitions as first-class
  members, not only functions: a lemma whose only out-of-footprint
  reference is a data type is inadmissible (it would drag an unrelated
  datatype's declarations into the problem — the noise the filter
  exists to remove). This bounds each proof's
  axiom set by what the goal can reach instead of by the library's size
  (#25).
- **Author-supplied hints (normative, #67).** A definition's metadata MAY
  carry, per property index `pi`, a list of *hints*: references `(defHash,
  propIdx)` to proven properties of OTHER definitions. Hints are metadata
  only — they are NOT part of the object's canonical encoding (§1) and do not
  affect its hash; two objects differing only in hints are the same object,
  and hints merge on re-put like other verdict-adjacent facts. For the goal
  that is property `pi` of the definition under proof, a hinted lemma
  `(defHash, propIdx)` is admissible EVEN WHEN the footprint relevance test
  above would exclude it, PROVIDED `propIdx` is a currently-proven property of
  `defHash` (present in that definition's proven-property set). A hint whose
  target is unproven, falsified, or missing is INERT — never admitted. A
  hinted lemma MAY belong to a definition outside the goal's footprint (and
  outside the dependency closure); a kernel collects it as an additional lemma
  candidate, and its declarations enter the problem as any lemma's do.
  Admission is purely ADDITIVE: the admitted set is still the sibling and
  footprint-admissible lemmas, PLUS the proven hinted lemmas, emitted in the
  same canonical (defHash, propIdx) order; a hint never removes a lemma and
  never affects the lemma-free attempt (which admits none). Soundness is by
  construction — only already-proven facts are ever asserted, so a hint can
  make a true goal reachable within budget but can never make a false goal
  `unsat`; hints are a reachability lever over the same non-monotone solver,
  not a change to what counts as proved. A property proven with at least one
  hinted lemma in its admitted set, by a strategy other than the lemma-free
  attempt, is REPORTED as hinted (the method string carries a `(hinted)`
  suffix); this is a legibility annotation and does not affect the verdict.
  Two consequences of "additive" that a kernel MUST NOT get wrong:
  - **Set union, not concatenation.** If a hinted lemma is ALREADY an admissible
    candidate for the goal, the hint makes no difference: the lemma is asserted
    exactly ONCE, and the emitted bytes are identical to the unhinted script. A
    redundant hint is a no-op, never a duplicated assertion.
    CORPUS WITNESS (do not remove): `q-push.push-length` carries a hint naming
    `append.length-adds`, which that goal's footprint ALREADY admits. It exists
    solely to pin this rule in `prove/scripts.txt` — the hint is carried to every
    kernel through `prove/outcomes.json` (§10), and a kernel that concatenated
    rather than unioned would emit a second assertion and fail the byte oracle on
    that line. Without it the rule would rest on each kernel's own unit tests,
    which cannot catch the two disagreeing.
  - **A property is never its own lemma (soundness).** The rule that a goal's own
    property is excluded from its lemma set is absolute and is applied BEFORE
    hints: a kernel MUST discard any hinted lemma whose (definition hash,
    property index) IS the goal being proven, whatever the metadata says. Without
    this a hint could assert the goal as an axiom and "prove" anything, so this is
    a soundness requirement, not a preference. Hints naming the definition under
    proof are in any case redundant — sibling lemmas are already admissible
    unconditionally — and authoring tools SHOULD refuse to record one.
- **Lexicographic induction (normative).** When single-binder induction
  fails, kernels MUST attempt lexicographic induction on each ordered pair
  (i, j) of distinct datatype-sorted binders, in ascending (i, j) order,
  accepting the first pair whose subgoals all discharge. For each
  constructor c of binder i's datatype: if c has no recursive fields, one
  subgoal with i := c(fresh fields) and every other binder at its goal
  constant, no hypotheses; otherwise, for each constructor c' of binder
  j's datatype, one subgoal with i := c(fresh), j := c'(fresh), under
  hypotheses (a) for each recursive field x of c: the property with
  i := x and every other binder universally generalized, and (b) for each
  recursive field y of c': the property with i pinned to the SAME
  c(fresh) value, j := y, and remaining binders generalized. Sound by the
  lexicographic subterm order. (The corpus witness is merge, whose
  recursion shrinks either argument.)
- **Recursion induction (normative, #56).** When structural and lexicographic
  induction fail and the definition UNDER PROOF is `measure`-total (§6.1.1), a
  kernel MUST attempt induction along that function's OWN recursion. First map
  the property's binders to the function's inputs, choosing the FIRST applicable:
  - CONSTRUCTOR case (#57): the function has ONE parameter of a single-constructor
    datatype whose field sorts equal the property's binder sorts (the law applies
    the function to `(ctor b0 b1 …)`, as `length (rle-expand (Run n v)) = n`).
    Bind that parameter to `(ctor b0 b1 …)`; a site's recursive argument is then a
    datatype value, and IH binder `j` is substituted by `(selector_j A_s)` where
    `A_s` is that single argument.
  - POSITIONAL case: otherwise, if the property has at least `dParams` binders
    (`dParams` = the function's arity), the leading binders ARE the parameters;
    IH binder `j` (`j < dParams`) is substituted by the argument `A_s[j]` passed
    at position `j`, and binders at index `≥ dParams` are left generalized.
  If neither applies, skip. Walk the function body — exactly as §6.1.1 collects
  self-call sites, over the mapped binder constants — to recover every self-call
  SITE: its path guard `G_s` (the conjunction of `if`-conditions reaching it,
  `true` if none) and its recursive argument(s). Skip if the walk cannot fully
  analyze the body or finds no site. Then discharge, all `unsat`:
  (a) BASE — the goal under `(assert (not G_s))` for EVERY site (the complement
  of the recursive region — where no self-call fires);
  (b) STEP — GROUP the sites by their guard `G_s` (in first-seen order), and for
  each group prove the goal under `(assert G_s)` together with the induction
  hypothesis `(assert IH_s)` of EVERY site in the group, where `IH_s` is the
  property with each binder substituted per the mapping above (the property at
  that recursive call's arguments — a point of strictly smaller measure). A
  function with several recursive calls on one path (`fib`'s `fib(n-1)` and
  `fib(n-2)`) thereby gets all their hypotheses at once; a single-recursive-call
  function has one site per guard, so its obligation is unchanged.
  Sound by well-founded induction on the measure the function is total by: the
  recursive arguments strictly decrease it, so a false property fails either the
  base (false off the recursion) or some step (false where its smaller-measure
  hypothesis holds). This subsumes single-counter induction and also reaches a
  counter that INCREASES toward a bound. Each obligation runs at the reduced
  budget `(set-option :rlimit 4000000)` under model-based instantiation on the
  pattern-free `measure` axiom. Corpus witnesses: `replicate.length-is-n`
  (`length (replicate n x) = n`, decreasing counter) and `range.length-is-span`
  (`length (range lo hi) = hi − lo`, increasing counter, measure `hi − lo`).
- **Deterministic proof budget (normative).** The per-goal budget is z3's
  resource limit — `(set-option :rlimit 400000000)` as the script's first
  command — not wall-clock time: same script + same solver version + same
  rlimit yields the same outcome on any machine. A wall-clock safety cap
  (600s) exists only to contain pathological environments; a kernel whose
  wall cap fires before rlimit exhausts MUST treat the run as
  non-conformant rather than record an outcome — recording "unknown" on a
  cap hit smuggles machine-dependence back in (a goal that proves at
  minute four on one machine would be silently unproven on a slower one).
  The cap must sit far above any legitimate budget exhaust: burning the
  full rlimit on quantifier-heavy goals takes minutes of wall time, which
  is why the cap is 600s and not lower. (History: the cap was first set at
  180s, and the reference kernel recorded cap hits as unknown; the blind
  Rust kernel implemented invalidation correctly from this text and its
  exit-on-cap fired on hardware where the reference had been quietly
  recording — cross-kernel conformance catching the reference violating
  its own spec.)
- **Lemma-free first attempt (normative, #53).** Before any other strategy, a
  kernel MUST attempt the goal with its declarations and defining-equation
  axioms but **no lemma library**, at the reduced budget
  `(set-option :rlimit 4000000)`. Only `unsat` is accepted from this attempt —
  it proves the goal from strictly fewer premises, which is sound — and it
  records the method as direct. Any other result (including `sat`) is
  DISCARDED and the goal proceeds through the unchanged strategies below, so
  the recorded outcome is the UNION of lemma-free and the existing search: no
  property provable before can regress. Rationale: a budget-limited solver is
  non-monotone in its axiom set, and the effect is severe enough to decide
  verdicts, not merely speed. The corpus witness is `q-peek.peek-is-head`,
  which discharges at 2,294 rlimit with no lemmas and does NOT terminate
  within the full 400000000 once its twelve *legitimately relevant* lemmas are
  admitted — the relevance filter is not at fault; the axiom COUNT is.
  Lemma-free successes cost milliseconds, so the reduced budget catches them
  while failing fast for goals that genuinely need their chain (`sort` does
  not prove lemma-free and reaches its verdict through the strategies below).
  Because the lemma-free script omits the lemma block, it is NOT the script
  pinned by `prove/scripts.txt`; the canonical with-lemmas direct-attempt
  script is unchanged and is still what the byte oracle hashes. To be precise
  about WHAT is omitted: only the lemma `(assert …)` lines are dropped. The
  declaration and defining-equation-axiom streams are emitted UNCHANGED — a
  kernel MUST still perform lemma translation for its declaration side effects,
  so a lemma-free script may legitimately carry orphan declarations for sorts
  or functions that only a now-omitted lemma mentioned. Shrinking the
  declaration stream to the goal's own footprint is NOT conformant: it changes
  which symbols exist and, under a budget, can change outcomes.
- **Direct-attempt budget on inductive-eligible goals (normative, #50, #56).** A
  goal is INDUCTIVE-ELIGIBLE if it has at least one datatype-typed binder (a
  candidate for structural/lexicographic induction) OR at least one `Int`-typed
  binder (a candidate for recursion induction, #56). Such a goal runs its
  DIRECT attempt at the reduced budget `(set-option :rlimit 4000000)`; its
  structural and lexicographic induction and the fallback below use the full
  `400000000` — except the recursion-induction obligations themselves, which are
  reduced-budget (above). A goal with no datatype-typed and no `Int`-typed binder
  runs its single direct attempt at the full budget.
  The direct attempt on an inductive-eligible goal is almost always futile
  (the goal needs induction) yet at the full budget burns minutes of wall
  time before failing; every direct proof that SUCCEEDS in the corpus consumes
  under ~3K rlimit, so the reduced budget cannot change a direct success, only
  fail a futile attempt ~100x faster. To preserve the budget-part-of-identity
  invariant, after structural and lexicographic induction both fail the kernel
  MUST retry the direct attempt at the FULL budget (the fallback); a goal
  provable only by heavy direct search thereby keeps its verdict, and the
  recorded outcome is identical to a kernel running a single full-budget
  direct attempt. The direct-attempt SCRIPT is byte-identical at either budget
  (the rlimit is a runner option outside the hashed core, above), so
  `prove/scripts.txt` and the emitted script texts are unaffected.
- **Attempt validity (normative, #29).** The wall cap is one instance of a
  general rule: a non-verdict (anything other than `unsat`/`sat`) is an
  OUTCOME only when the solver's own telemetry proves the attempt was
  deterministic. The runner appends `(get-info :rlimit)` and
  `(get-info :reason-unknown)` after the core script (outside the hashed
  bytes, like the prepended options), and an attempt is VALID only when
  BOTH infos parse and the reason is
  deterministic: either a genuine budget exhaust (`"canceled"` with
  consumed rlimit ≥ the budget; z3 overshoots by a few units) or a solver
  incompleteness give-up (any NON-EMPTY, non-canceled, non-memout
  reason — a pure function of the script). A BLANK reason on a
  non-verdict invalidates: the rule demands positive telemetry, and an
  empty string is absence of evidence. The opt-in memory bound is passed
  to `memory_max_size` verbatim in MEGABYTES (z3's unit). Note the two
  distinct memory failure modes: a bound below z3's arena reservation
  kills the process before any telemetry (caught by missing-telemetry),
  while a bound tripped mid-search yields a clean `memout` reason —
  both invalidate. Missing telemetry means the process died
  mid-attempt (crash, kill); `memout` means the memory bound fired; and
  `"canceled"` below budget means something external canceled — all three
  are the ENVIRONMENT talking, and recording them as unproven would make
  verdicts depend on RAM and signals. (Empirical origin: during the #17
  settles, z3 segfaulted under memory pressure at ~21GB per attempt and
  the reference recorded the deaths as unproven — found via macOS crash
  reports, not via any in-band signal, which is exactly why the rule
  demands positive telemetry.) A solver memory bound
  (`memory_max_size`) is OPT-IN environment policy, never a default and
  never outcome-determining: z3 counts its upfront arena RESERVATIONS
  (tens of GB on quantifier-heavy goals) against the bound, so any value
  below the reservation instantly memouts attempts that would have run
  fine. Environments that prefer a clean memout invalidation over an
  OS-level death may set it (reference: `OATH_PROVE_MEMORY_MB`); the
  missing-telemetry clause catches the OS-death case regardless.
  GRANULARITY: an invalid attempt yields NO EVIDENCE — it does not by
  itself end the run. `unsat` (and a quantifier-free `sat`) is positive
  evidence no environment can fake, so a property proven or refuted by
  any valid attempt keeps its verdict regardless of other attempts'
  invalidity. Invalidity taints only the NEGATIVE case: a property that
  would be recorded UNPROVEN while any of its strategy attempts was
  invalid has no valid negative verdict, and the kernel MUST NOT record
  an outcome for it. **This is PER PROPERTY, not per run (normative,
  #72).** For such a property its STANDING VERDICT stands UNCHANGED — if it
  was proven, it stays proven, since demoting it would turn an
  environmental abort into a verdict, the very thing this rule forbids —
  and it is reported DISTINCTLY as aborted, never as unproven,
  because "no valid verdict exists" is a different claim from "not
  proven".
  REPORTING (normative, #72): the recorded state of a property and whether
  its attempt aborted are INDEPENDENT — a property can be recorded proven
  (by carry-forward) AND have aborted this run — so a kernel MUST satisfy
  three claims and is otherwise free in how it renders them. (1) It MUST
  NOT describe an aborted property as unproven, since "attempted validly,
  not proven" is a claim the run cannot support. (2) A carried-forward
  proof MUST still count as proven everywhere a verdict is aggregated —
  proven counts, guarantee level, `prove/outcomes.json` — because it IS
  the recorded state; the abort withholds new evidence, it does not
  withdraw old. (3) The abort MUST be discoverable, together with whether
  a standing verdict was retained. The SURFACE is deliberately NOT pinned:
  a one-character-per-property rendering cannot carry both dimensions and
  may put the recorded state in the character and the abort alongside it,
  while a line-per-property rendering may lead with the abort and name the
  retained verdict inline. Both satisfy the claims above; the reference
  and the Rust kernel legitimately differ here, and conformance compares
  recorded verdicts rather than presentation. Sibling properties are unaffected: a property whose own
  attempts were all valid records its verdict normally, and the run as a
  whole SUCCEEDS with partial results rather than failing. An aborted
  property contributes no new lemma (it gains nothing this run), which is
  the conservative direction for the run-stability fixpoint below.
  (Earlier this invalidated the ENTIRE run, so one intractable property
  hid every sibling's status and made the definition permanently
  unrecordable — the `rot*` flywheel arms, 28 properties across four
  definitions, were stuck exactly this way once #71 made their goals
  translatable: one goal exceeds the wall cap at any practical budget, and
  the other six could never be recorded.)
  STANDING VERDICT means the proven set the kernel holds for the object AT
  THE START OF THE CURRENT ROUND of the run-stability fixpoint below —
  i.e. the recorded state as it stands, INCLUDING proofs this run
  established in an earlier round. It is NOT a snapshot taken before the
  run: a property proven in round 0 and aborted in round 1 keeps its
  proof, because that proof was derived from a valid attempt and losing it
  is the same environmental demotion this rule forbids. A kernel with no
  recorded state (a cold `prove` that reads no stored verdicts, §10) simply
  has an empty standing set in round 0 and carries nothing — the same rule,
  a different input. This referent is spelled out because it is otherwise
  ambiguous between "the store's pre-run metadata" and "the fixpoint's
  current set", and the two disagree observably on the round-0-proven,
  round-1-aborted case while being INVISIBLE to the byte oracle.
  A carried-forward proof REMAINS admissible as a lemma: withdrawing it
  would make sibling verdicts depend on which attempts happened to abort,
  i.e. on the environment. Only a property that has never been proven
  contributes no lemma.
  CARRY-FORWARD WEAKENS THE FIXPOINT, and consumers must know it. This is
  not a soundness hole — a carried verdict is backed by a valid `unsat`
  over lemmas themselves recorded proven, and truth does not expire; an
  abort never CREATES a verdict, it only declines to overwrite one. But the
  run-stability fixpoint below otherwise makes the settled state
  SELF-CERTIFYING (every recorded proof re-derivable from it), and a
  carried-forward proof was derived from an earlier round's state, not
  re-derived from the final one. The settled state is therefore a fixpoint
  of F only MODULO aborted properties. Two consequences a kernel or
  consumer MUST respect: a conformance re-derivation cannot certify an
  aborted property at all and MUST report it as environmentally
  inconclusive rather than as a divergence; and provenance consumers
  (§8) MUST treat an aborted property's verdict as CARRIED, not
  re-derived, since a lemma it leaned on may since have been dropped —
  the verdict stays true but is not replayable from the recorded state
  alone.
  (Empirical origin of the
  granularity: z3 crashes with empty output — deterministically — on
  t-insert.insert-length's direct-attempt script, while structural
  induction proves the goal; run-level invalidation would have made the
  definition permanently unprovable on affected z3 builds.) The budget
  value is part of outcome identity. (Calibration: the heaviest
  successful proof in the corpus consumes ~115M; the budget allows 3.5x
  that.)
- **Script stability (normative).** A goal's emitted SMT script is a pure
  function of (goal, recorded lemma state): byte-identical across attempt
  histories, warm/cold runs, and independent kernels. Three rules deliver
  this: (1) STRUCTURAL NAMING — no counters exist; symbols are named by
  intrinsic position: property binders `b<i>`, universally generalized
  binders in lemma/hypothesis formulas `q<i>` (binder index),
  single-binder induction field constants `f<fi>` (field index),
  lexicographic field constants `g<fi>` (first binder) and `h<fj>`
  (second), axiomatized-function parameters `p<i>` (parameter index).
  (2) FRESH CONTEXT PER ATTEMPT — declarations and axioms are accumulated
  from scratch for each property attempt (library lemmas first, then goal
  translation), never shared across attempts. (3) CANONICAL EMISSION —
  lemma assertions carry no comments and are emitted in ascending
  (definition-hash, property-index) order; script layout is: datatype and
  function declarations in first-touch order of the canonical build,
  defining-equation axioms, admissible lemmas, binder declarations,
  negated goal, `(check-sat)`, and `(get-model)` iff NO quantifier was
  introduced anywhere during context construction — by a defining-equation
  axiom or by translating ANY candidate lemma with binders, including
  candidates later excluded from emission (footprint-inadmissible or the
  property's own lemma). The flag is a property of the build, not of the
  emitted bytes: a script can contain no quantifier and still omit
  `(get-model)`. An uninterpreted (non-total, axiom-less) callee introduces
  no quantifier. The conformance suite fixtures `prove/scripts.txt`:
  sha256 of every property's direct-attempt script under the recorded
  lemma state — a conforming kernel MUST reproduce these hashes, which
  pins the scheme without prose ambiguity. `prove/scripts/` holds full
  golden script texts for a curated structural sample (recursive axioms,
  uninterpreted callee, lemma-heavy interleaving, lexicographic-fragment
  recursion) so a hash divergence is debuggable byte-by-byte.
- **Lexicographic induction (normative).** When single-binder induction
  fails, kernels MUST attempt lexicographic induction on each ordered pair
  (i, j) of distinct datatype-sorted binders, in ascending (i, j) order,
  accepting the first pair whose subgoals all discharge. For each
  constructor c of binder i's datatype: if c has no recursive fields, one
  subgoal with i := c(fresh fields) and every other binder at its goal
  constant, no hypotheses; otherwise, for each constructor c' of binder
  j's datatype, one subgoal with i := c(fresh), j := c'(fresh), under
  hypotheses (a) for each recursive field x of c: the property with
  i := x and every other binder universally generalized, and (b) for each
  recursive field y of c': the property with i pinned to the SAME
  c(fresh) value, j := y, and remaining binders generalized. Sound by the
  lexicographic subterm order. (The corpus witness is merge, whose
  recursion shrinks either argument.)
- **Script stability (normative).** A goal's emitted SMT script must be a
  function of the goal and its admissible lemma SET only — independent of
  attempt history and lemma acquisition history. Two rules deliver this:
  fresh-variable numbering resets at each property attempt, and lemma
  assertions are emitted in ascending (definition-hash, property-index)
  order regardless of whether a lemma was loaded from prior-run metadata
  or proven earlier in the same run. Solver heuristics are sensitive to
  both names and assertion order: without these rules, unrelated prover
  changes or warm-vs-cold differences flip borderline goals —
  nondeterminism masquerading as regression. (The corpus witness is
  q-drop.drop-back-only, which sat exactly at the budget edge and flipped
  between warm and cold runs until the order was canonicalized; it is now
  stably unproven at the normative budget.)
- Kernels MAY gate fixpoint re-attempts on lemma-set growth: a goal whose
  available lemma set has not changed since its last failed attempt need
  not be re-attempted — with a deterministic solver and fixed budget the
  outcome is identical, and the full-budget timeout burns on genuinely
  unprovable goals happen once instead of once per iteration (#24).
- Direct proof declares property binders as constants, translates the property,
  asserts its negation, and checks satisfiability. `unsat` proves it. `sat`
  refutes only when the formula is quantifier-free; otherwise `sat` is
  reported as unproven.
- Induction is attempted after direct proof, over datatype-typed property
  binders in binder order. For each constructor, fresh constants are declared
  for fields. For every field whose sort is the induction sort, an induction
  hypothesis is asserted by substituting that field for the induction binder
  and universally quantifying all other property binders. The constructor
  subgoal substitutes the constructed value for the induction binder and keeps
  other binders as the direct-attempt constants. All constructor subgoals must
  be `unsat` under negation for the property to be proven.

### 7.3 Proof metadata

`prove` updates `Guarantee.proven` with the number of proven properties and
`Meta.proven_props` with their indices. If all properties are proven, no
property was refuted, and the prior guarantee level is `tested`, the guarantee
level becomes `proven`.

Conformance fixtures SHOULD record, for every proof attempt: kernel version,
solver name/version, property index, method (`direct` or induction binder),
outcome, and a hash of the emitted SMT-LIB obligation. The current metadata
stores only the proven count and property indices, so these proof-provenance
fixtures are external to the hashed definition.

## 8. The journal

Append-only, one JSON object per line: `seq`, `time` (RFC3339 UTC),
`author` (principal string, self-reported in local mode), `verifier`
(kernel version string), `name`, `kind`, `status`
(`accepted`|`falsified`|`rejected`|`blocked` (repoint refused by store policy; object stored, name unchanged)|`pending` (repoint DEFERRED awaiting an out-of-band proof under a `require_proven` policy (§8.5); object stored, name unchanged until a verification worker resolves it to `accepted` or `blocked`)), `hash`, `prev` (previous binding; §8.2), `error`,
`guarantee`, `termination`, `context`, `pubkey` (optional; §8.4), `sig`
(optional; §8.4), `envelope_b64`, `author_pubkey`, `author_sig` (the author's
publication statement; optional, all-or-none; §8.6.3), `recipient_sig` (the
SECOND signature over the same octets, transfer only; §8.7), `name_transition`
(`applied`|`unchanged`|`none`; §8.6.2), `chain`.

The exact member ORDER, the omission rule, and string escaping are normative — see
§8.2.1. This list names the members; §8.2.1 fixes their bytes.

Note that `prev` is the previous binding on EVERY publication of an existing name,
including one that re-publishes the hash already bound; it is omitted only when the
name did not previously exist (§8.2). Whether the binding changed is
`name_transition`, and MUST NOT be inferred from `prev`.

Every submission attempt MUST
be journaled, including gate rejections (which store no object). A cross-check
(§6.4) recorded to the journal uses `kind` = `cross`, status `accepted` (AGREE)
or `falsified` (DISAGREE), with the two cross-checked object hashes carried in
`hash` and `prev` — here `prev` names the OTHER operand, not a repoint origin.

`context` is the author-supplied hash of the context slice the submission was
built against: SHA-256 (lowercase hex) of the newline-joined, byte-sorted
definition hashes actually served by a `context` query. The slice output ends
with a `-- context-hash: <hex>` line; a submitter passes it back via
`put --context` (CLI) or the `context` argument (MCP). It hashes the served
identity set, not the rendered text, so presentation changes cannot alter what
"built against these specs" means. Self-reported and optional in local mode —
like `author`, it becomes trustworthy only under a hosted store that
associates served slices with sessions — but once journaled it is sealed by
the chain, making implemented-against-stale-specs detectable after the fact. The journal is
metadata: never part of definition identity, wall-clock timestamps permitted
(the kernel's no-clock rule protects verification semantics only).

`chain` makes the journal tamper-evident. For each appended entry:
`chain = SHA-256(anchor + "\n" + entry-bytes)` rendered as lowercase hex,
where `entry-bytes` is the entry's compact JSON with `chain` empty (omitted),
and `anchor` is the `chain` of the most recent chained entry — or, when no
chained entry exists yet (a journal predating this field), the SHA-256 of the
entire byte prefix before this entry, which retroactively seals legacy lines.
A verifier MUST reject a journal containing an unparseable line, a `seq` gap
or reorder, an unchained entry after a chained one, or a `chain` mismatch.
One limitation is inherent and disclosed rather than papered over: deleting
entries from the TAIL leaves a self-consistent file; an external anchor (the
version-control history of the store) is required to detect it.

### 8.1 Store layout

The reference filesystem store layout is normative for local conformance:

```
codebase/
  objects/<hash>.bin   compact canonical Def JSON
  meta/<hash>.json      indented metadata JSON, two-space indent
  names.json            indented object mapping name -> current hash
  log.jsonl             append-only compact JSON log entries
  proofq/<hash>.job     pending verification-worker jobs (§8.5); .lease when claimed
```

`proofq/` is an optional work queue, not part of identity or the conformance
corpus; a kernel without the async proof gate (§8.5) need not create it.

Object filenames MUST match their hash, and a conforming reader MUST reject or
at least report an object whose canonical bytes do not hash to the filename.
A conforming reader MUST ALSO re-validate a loaded object against the static
semantics (§2) before use, rejecting it on failure: content addressing proves
the bytes are intact, not that they encode a well-formed definition, and the
typechecker and evaluator are not total on malformed `Def`s (a nil type or body
would fault them). Objects written directly into the store — the team/hosted
threat model — never passed the gate, so the store is trusted because it is
checked, not merely because it is content-addressed. Validation MAY be lazy
(on first access) and cached, since objects are immutable.
Names are mutable local aliases. Repointing a name never deletes or mutates the
old object.

`names.json` is metadata and may be rendered with ordinary JSON object key
ordering for the host implementation; hash identity never depends on it. For
deterministic API output, commands that list names sort name keys ascending.

Writes to `names.json`, `meta/`, and `objects/` MUST be atomic (write to a
temporary file in the same directory, sync, rename): the name index and the
journal are not reconstructible from `objects/`, so a crash-truncated write is
unrecoverable outside version control. A store opener MUST refuse to open a
store whose `names.json` exists but does not parse, rather than treating it as
an empty index — silently losing every name is strictly worse than failing.

`meta/<hash>.json` contains `Meta`: definition name, type-variable names,
constructor names, property names, guarantee, mutation score and waived
mutants, termination, author, parameter names, confinement verdicts, and
proven property indices. Metadata may change without changing the object
hash.

Metadata splits along a naming/verdict boundary. NAMING fields (definition,
type-variable, constructor, property, and parameter names) belong to an
ALIAS: structurally identical definitions content-address to the same
object, so one object may be bound by several names, each with its own
vocabulary. The top-level naming fields hold the most recent put's naming;
prior aliases are preserved in an `aliases` map keyed by name. Constructor
resolution (§1.4) resolves each name through its own naming block — the
alias entry when the name is not the object's most recent one. VERDICT
fields (guarantee, termination, confinement, proven property indices,
mutation score, waived mutants) belong to the HASH: a proof of an object is
a fact about the object regardless of which name submitted it. A put of an
already-present object therefore MERGES metadata rather than clobbering:
verdict fields carry over, and when the incoming name differs, the previous
name's naming block moves into `aliases`.

`waived_mutants` records surviving mutants judged semantically equivalent to
the original, keyed by the mutant's content hash, each carrying a
justification, the judging principal, and optionally a reference to a
machine-checkable equivalence artifact. Waivers are annotations, never
kills: mutation scoring reports them separately and the killed/total score
is unaffected. A waiver may only be recorded for a mutant that currently
survives every property.

### 8.2 Journal sequencing

`seq` is one plus the number of existing newline-terminated log records. Local
time format is `time.Now().UTC().Format(time.RFC3339)` equivalent: UTC with
`Z`, no subsecond field. Hosted stores may use stronger sequencing, but a
conformance fixture must specify exact journal bytes if journal replay is under
test.

Gate rejection logs contain no object hash. Accepted and falsified logs contain
the stored object hash; `prev` is the hash the submitted name pointed at BEFORE
this entry — **always**, including when it already pointed at the same hash. It
is omitted only when the name did not previously exist.

> **Amended.** This previously also omitted `prev` "when the name already
> pointed at the same hash". That was harmless while nothing signed the
> transition, and became a contradiction once §8.6 bound the parent: a same-hash
> re-publication journalled an absent `prev`, while its envelope named the real
> parent, so §8.6.4(5) compared `-` against a hash and MUST fail. A correctly
> signed, correctly accepted publication produced a journal that failed its own
> verifier. Collapsing an unchanged binding to "absent" destroyed the distinction
> between *there was no previous value* and *the previous value was the same
> one* — and the second is exactly what a parent commitment needs to express.
>
> This defect was found by an independent implementation reading only the
> normative text, which derived the contradiction before any code was compared.

Whether the binding CHANGED is recorded separately and MUST NOT be inferred from
`prev` — see `name_transition` in §8.6.3.

### 8.2.1 Canonical entry encoding

A journal entry is a compact JSON object whose members appear in exactly this
order:

```
seq, time, author, verifier, name, kind, status, hash, prev, error,
guarantee, termination, context, pubkey, sig,
envelope_b64, author_pubkey, author_sig, recipient_sig, parent_rev,
name_transition, chain
```

Field order is NORMATIVE, not a formatting preference. `chain` (§8) and the
entry signature (§8.4) are both computed over "the entry's compact JSON", so two
implementations ordering these members differently compute different chain values
and different signatures for the same logical entry — and each then rejects the
other's journal wholesale. An undefined order is a byte-level fork waiting for a
second implementation.

Members whose value is empty (or zero, for `seq`) are omitted, except `seq`,
`time`, `author`, `verifier`, `name`, and `status`, which are always present even
when empty.

> `verifier` was missing from this list in the first draft of §8.2.1. Because the
> conformance vectors carry `"verifier":""`, a strict reader implementing the prose
> literally REFUSED the fixture's own journal line — so the prose and the vectors
> described two different chains, signatures and entry digests for one entry, which
> is the exact divergence §8.2.1 exists to prevent, occurring inside §8.2.1. Found
> by an independent implementation running the vectors against the text.

String values are escaped MINIMALLY: only `"`, `\`, and characters below U+0020
are escaped, using JSON's short forms where they exist (`\"`, `\\`, `\b`, `\f`,
`\n`, `\r`, `\t`) and `\u00XX` otherwise. All other characters, INCLUDING
non-ASCII, appear as literal UTF-8. `<`, `>`, `&` and `/` MUST NOT be escaped.

Two exceptions, both required: U+2028 and U+2029 MUST be escaped as `\u2028` and
`\u2029`. They are Unicode line terminators and the journal is a line-delimited
format, so a literal one invites a reader splitting on Unicode line boundaries to
see two records where there is one.

Member order alone is not sufficient for byte agreement while string values still
have several valid spellings. `error` and `guarantee` are free-form, so a rejection
message containing `<` is enough to fork two implementations — and since chain
hashes, entry signatures and entry digests are all computed over these bytes, that
fork means each rejects the other's journal wholesale. Note that at least one widely
used encoder escapes `<`, `>` and `&` by default for HTML safety; that is a
language-specific habit, not an RFC 8259 requirement, and it is not canonical here.

The canonical line is the JSON object bytes ALONE. The record separator (a single
LF between entries) is a framing concern and is NOT part of entry identity: it does
not enter the chain, the entry signature, or the entry digest.

A verifier MUST read entries STRICTLY: parse the line, re-encode it canonically,
and reject the journal unless the result is byte-identical to the stored line.
Unknown members, duplicated members, reordered members, added whitespace, and
trailing content after the object are all errors.

Canonical writing with lenient reading protects nothing: a normative order would
then bind writers while readers accepted several spellings of one entry, and
since `chain` and signatures are over these bytes, accepting a second spelling
means accepting two identities for one entry. It follows that a tool MUST NOT
parse a non-canonical line, normalize it, and verify the normalized form — that
verifies a statement other than the one the journal contains.

### 8.2.2 Publication identity

An artifact hash identifies CONTENT, not a publication of that content. The same
artifact may be published more than once — a re-publication of the hash already
bound is a valid recorded no-op (§8.6.2) — so an artifact hash does not address a
publication, and a reader that selects the first or last matching entry is
guessing which was meant.

The identity of one publication is its **entry digest**: SHA-256 over the exact
canonical entry line of §8.2.1, lowercase hex — the object bytes only, excluding the
record separator.

Where the entry is CHAINED, the digest also fixes its position, because `chain` is
part of the line and two entries identical in content at different positions have
different chains. Where it is NOT chained, the digest identifies content only: an
unchained entry duplicated verbatim at another position has the same digest.

> An earlier draft claimed the digest fixes position unconditionally. It does not:
> `chain` is not an always-present member, so a digest cannot establish position
> without a chain or a signed external index. An implementation relying on
> positional uniqueness MUST therefore require the entry to be chained.

An ordinal (`seq`) is a convenience for humans and does not survive copying
between stores; the entry digest does. An implementation SHOULD return both when
accepting a publication, so a client can verify the exact accepted transition
rather than searching for it.

### 8.3 API and MCP result shape

The CLI and MCP server share one API layer. Tool results are text unless a
command explicitly documents JSON mode.

- `put` accepts source text plus an author, stops at the first elaboration error
  or gate rejection, journals every attempted top-level form, and returns one
  report per processed form. CLI exit code is `0` for all accepted, `1` for a
  rejection or command error, and `2` when at least one processed definition is
  falsified and no rejection occurred.
- `put --json` returns an array of reports with fields `name`, `hash`, `kind`,
  `status`, `guarantee`, `termination`, `confinement`, `prev`, `ctors`,
  `error`, and `props`.
- `context` prints spec projections in breadth-first dependency order from the
  requested names. Dependencies are sorted by hash before enqueuing. Each
  section's token estimate is `len(section)/4 + 1`; sections that would exceed
  a positive budget are omitted and named in the footer.
- `dependents` scans all object hashes in ascending order and prints current
  names or `name@shortHash` for superseded objects.
- MCP uses JSON-RPC 2.0 over newline-delimited stdio. Supported methods are
  `initialize`, `ping`, `tools/list`, and `tools/call`; notifications get no
  response; unknown methods return `-32601`. Tool call failures are encoded as
  successful JSON-RPC responses with `isError: true` and a text body beginning
  `error: `.

### 8.4 Signed entries (authorship)

A journal entry MAY be signed, making authorship unforgeable and verifiable
offline — the host is not the root of trust. A principal IS an Ed25519 keypair;
its public key is its identity. The chain (§8) seals ORDERING; a signature seals
AUTHORSHIP, and the two are independent (re-chaining a forged entry does not make
it verify).

- `pubkey`: lowercase hex of the 32-byte Ed25519 public key of the signer.
  Absent (or empty) means the entry is **unattributed** — signing is opt-in and
  a store of unsigned entries is conformant.
- `sig`: lowercase hex of the 64-byte Ed25519 signature over the entry's
  *signed content*.

The **signed content** is the entry's compact JSON with the store-assigned
fields `seq`, `time`, `verifier`, and `chain`, and the `sig` field itself, all
set empty/omitted — i.e. the signature covers exactly the authored fields
(including `pubkey`), independent of where the entry lands in the log. A signer
sets `pubkey` and `sig` before the `chain` is computed, so the chain covers the
signature too.

A verifier MUST, for every entry whose `pubkey` or `sig` is non-empty, reject
the journal unless `pubkey` is a well-formed 32-byte key and `sig` is a valid
Ed25519 signature under it over the signed content. An entry carrying a `pubkey`
with no valid signature (or vice versa) is a forged attribution and MUST be
rejected, not treated as unattributed. Entries with neither field are
unattributed and impose no signature obligation. This is metadata (never part of
definition identity); the reference kernel key format is hex of the 64-byte
Ed25519 private key (a 32-byte seed is also accepted) in a `.key` file.

### 8.5 The async proof gate (require_proven)

Every guarantee a repoint policy (§8.3 / the store policy model) enforces —
`forbid_falsified`, `require_total`, `min_mutation_score` — is recomputed
synchronously in the put path: the kernel re-runs the tests, termination, and (on
demand) the mutation engine against the submitted object before deciding the
name. **Proof is the exception.** SMT proving is unbounded work (§7.2) and cannot
run inside a submission, so a `require_proven` rule cannot be decided
synchronously.

A `require_proven` rule therefore splits the repoint decision in two:

1. At put time, after the synchronous policy checks pass, the object is stored
   and its verdicts recorded, but if it is not already fully proven the name is
   **not** moved. The attempt is journaled `pending` and the object hash is
   enqueued for out-of-band proving. A def that can never be proven — one that is
   `falsified`, or one that swears no properties — is journaled `blocked`
   instead, not `pending`.
2. A verification worker proves the queued object (deterministically — seeds
   derive from the content hash and the solver is pinned, so any worker yields
   the identical verdict). When every property proves, the worker re-checks the
   synchronous policy and repoints the name, journaling `accepted` (a `put`-kind
   entry with `prev`); the worker MAY sign this entry (§8.4), making the bound
   `proven` badge an authenticated "the verifier re-proved this" rather than a
   publisher's claim. If the object does not fully prove, the name stays put and
   the attempt is journaled `blocked`. The worker also records the proof itself
   as a `prove`-kind `accepted` entry.

This is metadata mechanism, not identity: the queue, the `pending` state, and the
worker are all outside the hashed `Def`. A kernel MAY omit the gate entirely
(treating `require_proven` as unsupported); one that implements it MUST NOT bind a
`require_proven` name to an object that is not fully proven. Because proofs are
re-earned by whoever consumes the object, a consumer never has to trust that the
worker actually proved anything — the gate is a convenience and a policy-
enforcement tier, not a root of trust.

### 8.6 Signed publication (the author's statement)

§8.4 signs a journal ENTRY, which seals custody: the entry has not been altered
since it was written. It does not establish authorship to a third party, because
the entry is composed by the store — a store could sign an entry naming anyone.
A **publication envelope** is the author's own statement, signed before submission
and persisted verbatim, so authorship becomes checkable without trusting the store.

The two are independent and MAY both be present. They answer different questions:

| | question answered | signer |
|---|---|---|
| entry signature (§8.4) | has this record been altered? | whoever holds the store's key |
| publication envelope | who requested this exact transition? | the author |

An envelope covers only what the author controls: the name, the artifact, and the
transition. It MUST NOT cover store-derived verdicts (`status`, `guarantee`,
`termination`, policy outcome) — those are re-derivable from content bytes (§7),
and an author cannot predict them, so requiring a signature over them would make a
policy decision indistinguishable from a forged statement.

#### 8.6.1 Canonical encoding

An envelope is a byte string: a version line, then a fixed number of `key=value` lines,
in the order below, each terminated by one LF (`0x0A`). There is no other
whitespace, no trailing content, and no optional field (**ENV-FIELD-COUNT**,
**ENV-FIELD-ORDER**, **ENV-LINE-LF**).

```
oath-publish/2
op=put
name=<name>
artifact=<64 lowercase hex>
parent=<64 lowercase hex | ->
parent_rev=<canonical decimal>
author=<64 lowercase hex>
license=<SPDX expression | ->
```

`license` is the terms the publisher ASSERTS for this publication (§12), and `-`
asserts none. It is deliberately NOT validated here: this layer is the notary for
what the author signed, and checking expression syntax would drift publication
into policy enforcement.

`oath-publish/1` is the same shape WITHOUT the `license` line — six lines, not
seven. Implementations MUST still read it: §8.6.4 requires an entry to be
verified under the format it was recorded with, and a kernel that read only its
newest shape would reject correct historical signatures, destroying evidence
rather than admitting it. A `/1` envelope asserts no terms, and MUST be reported
as that explicit no-assertion state rather than as an empty field — "the format
had no licence line" and "the publisher chose to say nothing" are different
historical facts.

> This section documented only `/1` for several commits after the kernel began
> emitting `/2`, so the normative text described an envelope the reference no
> longer produced and the vectors no longer contained. A blind implementation
> reading it would have built `/1` and failed every canonical vector — which is
> precisely how it was found.

The version line is inside the signed bytes, so a signature under one format
version can never be mistaken for another.

The octets are UTF-8. (An implementation reading them as bytes never needs to
decode, but a name may contain non-ASCII, and two encodings of one name would be
two different statements.)

A conformant implementation MUST reject an envelope, on both encoding and
verification, unless all of (**ENV-REENCODE** additionally requires that parsed
octets re-encode to themselves byte-for-byte):

- **ENV-TAG.** the first line is a format tag this version DEFINES — `oath-publish/2`, or
  `oath-publish/1` for historical verification (§8.6.1). Any OTHER tag is not a
  newer envelope to be interpreted — it is an envelope this version cannot verify, and accepting it
  under these rules would read a signature made under one format as though it
  were made under another;
- **ENV-VALUE-CHARS.** every value is free of LF, CR, and any character below `0x20` or equal to
  `0x7F`. A value containing LF would inject a line, and the encoding would stop
  being uniquely decodable. Note this rule is reachable only on the ENCODING
  side: an injected LF in stored octets presents as a line-count error, never as
  "a value contained an LF";
- **ENV-OP-PUT.** `op` is `put` (the only operation defined in this version);
- **ENV-HEX-LOWERCASE.** `artifact` is 64 **lowercase** hex characters. Uppercase MUST be rejected: the
  encoding is compared as bytes, so `ABAB…` and `abab…` would be different
  statements about one artifact;
- **ENV-PARENT-FORM.** `parent` is 64 lowercase hex, or exactly `-` meaning the name had no previous
  value;
- **ENV-REV-CANONICAL.** `parent_rev` is a non-negative integer in canonical decimal — no leading zeros,
  no sign, no spaces. `03` and `+3` MUST be rejected, since they would be distinct
  bytes for one revision. It is **unbounded**: an implementation MUST NOT impose a
  machine-word limit, because doing so would declare a valid historical statement
  malformed once a name had been repointed often enough;
- **ENV-NAME-NONEMPTY.** `name` is non-empty and otherwise unconstrained by this section beyond the
  character rule above. In particular a name MAY contain `=`; parsing splits each
  line at its FIRST `=`, so `name=a=b` names `a=b` and is not ambiguous;
- **ENV-PARENT-CONSISTENT.** `parent` is `-` **if and only if** `parent_rev` is `0`. A first publication has
  both or neither; allowing disagreement would let one envelope describe two
  different states;
- **ENV-AUTHOR-HEX.** `author` is 64 lowercase hex.

> This bullet read "exactly `oath-publish/2`" while the paragraph above it said
> "Implementations MUST still read `/1`". Both were added by repairs, in
> different rounds, and neither round re-read the other's text — so the section
> simultaneously REQUIRED and FORBADE accepting a `/1` envelope. Nothing
> witnessed either reading: every `/1` vector in the corpus is a `reject` record
> refused on other grounds, so a conformant implementation could take either side
> and pass. A contradiction no fixture can see is invisible to conformance and
> visible only to someone implementing from the prose.

Parsing MUST be strict: unknown keys, missing keys, duplicated keys, reordered
keys, a missing trailing LF, and trailing bytes are all errors. An implementation
MUST additionally verify that re-encoding the parsed value reproduces the input
bytes exactly. Canonical encoding without canonical parsing would protect identity
at creation and discard it at verification.

#### 8.6.2 The name revision

`parent_rev` is the number of journal entries that have **applied a transition** to
that name. It versions the name BINDING; it is not a publication counter, and the
journal already counts and orders events.

A revision therefore counts distinct name STATES:

| before → after | transition | revision |
|---|---|---|
| (none) → A | `applied` | increments |
| A → B | `applied` | increments |
| A → A | `unchanged` | **unchanged** |
| no name operation | `none` | unchanged |

A re-publication of the hash already bound is a recorded **no-op**: valid,
journalled, and not a new version of the binding. If it advanced the revision, a
harmless no-op would invalidate every envelope prepared against a state that never
changed. ABA protection is unaffected: A → B → A increments twice, so an envelope
naming the first A carries the wrong revision even though the hash matches again.

An entry has applied a transition if and only if its `name_transition` member is
`applied`. This MUST NOT be inferred from `status`, `kind`, `prev`, or artifact
equality for any entry that carries the member.

For LEGACY entries written before `name_transition` existed, an implementation
MUST derive it as follows, and MUST NOT extend this derivation to entries that
carry the member:

- entries whose `kind` is neither `data` nor `func` (nor absent) applied nothing.
  A `prove` (§8.5) or `cross` entry concerns an ARTIFACT and touches no name, so
  counting it would inflate a name's revision;
- otherwise, `accepted` and `falsified` applied a transition, EXCEPT where the
  entry's `hash` already equals what the name is bound to at that point in the log,
  which is a no-op (`unchanged`);
- every other status applied nothing.

The derivation is a FOLD over the name's entries, not a per-entry predicate. It
must track what the name is bound to as it goes, and compare each entry's `hash`
against that running value.

> A per-entry test — "`prev` equals `hash`, so it was a no-op" — cannot work, and
> the first draft of this section specified exactly that. The rule legacy entries
> were written under (§8.2, before amendment) omitted `prev` whenever the name
> already pointed at the same hash, so a legacy no-op carries NO `prev`, and an
> absent `prev` is irrecoverably ambiguous between "the name was new" and "the name
> was already here". The test therefore misses not some no-ops but ALL of them.
>
> Measured on the reference corpus: 169 of 187 names would receive an inflated
> revision, one counting 8 revisions for a single distinct state, with 526 entries
> in the ambiguous state. That turns the revision back into the publication counter
> this section opens by saying it is not, and quietly falsifies the replay-protection
> property for every pre-amendment name. Found by an independent implementation,
> which proved it from the pre-amendment §8.2 text quoted in §8.2's own amendment
> note.

A revision MUST be monotonic per name and MUST NOT be reused. This is what makes
replay protection survive an A→B→A cycle, where a `parent` hash alone becomes valid
again. Names in this version are only ever repointed, never deleted, so an
unresolvable name is one that was never published (`parent=-`, `parent_rev=0`). An
implementation that adds deletion MUST NOT allow a revision to reset, or every
historical envelope for a recreated name becomes replayable; a tombstone retaining
the revision, never reusing a deleted name, or a permanent name epoch inside the
envelope are the safe designs.

#### 8.6.3 Journal fields

- `envelope_b64`: the exact envelope octets, base64-encoded. A store MUST persist
  the octets verbatim and MUST NOT re-encode, normalize, or reformat them at any
  point. They are the historical statement; the duplicated fields below are its
  interpretation. Reconstructing them later from those fields would let an encoder
  change silently invalidate old signatures — the signature would remain correct
  and stop verifying.

  **Base64 is a storage representation, not a transformation of the statement.**
  Envelope octets contain LFs and a journal is one JSON object per line (§8.1), so
  they cannot appear literally. "Verbatim" therefore means the exact octets are
  recoverable as the DECODED value of this member.

  The dialect is pinned, because "base64" alone admits several spellings of one
  byte string and this member is compared and re-encoded (§8.2.1): RFC 4648 §4
  **standard** alphabet (`+` and `/`), padding **required**, no line breaks, no
  whitespace anywhere, no URL-safe substitutions (**ENV-B64-DIALECT**). **ENV-B64-CANONICAL**: a verifier MUST additionally
  reject the member unless re-encoding the decoded octets reproduces it exactly,
  so alternate spellings that decode to the same octets from different stored
  bytes are refused.

- `author_pubkey`: lowercase hex of the key that signed the envelope octets.
- `parent_rev`: the revision the author SIGNED AGAINST, as a decimal STRING. It is
  the author's own claim about what they were publishing over, preserved verbatim
  rather than recomputed. A JSON string and never a number: §8.6.1 makes the
  revision unbounded, and a float64 reader corrupts values past 2^53.
- `author_sig`: lowercase hex of the 64-byte signature over the **decoded**
  envelope octets. Never over the base64 text, and never over the JSON string
  bytes that carry it.
- `name_transition`: `applied`, `unchanged`, or `none` (§8.6.2). This records what
  happened to the NAME, which is a different dimension from `status`, which records
  what the store concluded about the ARTIFACT. A definition may be `falsified` and
  still bind its name; a request may be `rejected` and belong in the journal
  without moving anything; a `prove` or `cross` entry concerns an artifact and
  touches no name. Deriving either dimension from the other is not reliable, so
  both are recorded.

These are metadata and never part of definition identity (§9). Their position in
the entry's member order is normative — see §8.2.1.

#### 8.6.4 Verification obligations

A verifier MUST, for every entry where any of `envelope_b64`, `author_pubkey`,
`author_sig` is non-empty, reject the journal unless ALL of:

1. **ENV-VERIFY-PRESENT.** all three are present. Any one alone attests to nothing;
2. **ENV-VERIFY-DECODE.** `envelope_b64` decodes under §8.6.3's pinned dialect, and the decoded octets
   parse under §8.6.1 and re-encode to themselves;
3. **ENV-VERIFY-AUTHOR.** the envelope's `author` equals `author_pubkey`;
4. **ENV-VERIFY-SIGNATURE.** `author_sig` is a valid signature under `author_pubkey` over the octets
   **decoded from** `envelope_b64` — not over a re-encoding of the parsed
   envelope, and not over the base64 text;
5. **ENV-VERIFY-AGREES.** for an entry whose transition is `applied` — DERIVED by
   the verifier from journal history, never read from the entry — the envelope's `name`,
   `artifact`, and `parent` equal the entry's `name`, `hash`, and `prev`
   respectively, where an empty `prev` corresponds to `-`.

> Clause 5 was previously UNSCOPED, which made it impossible to journal a refused
> publication honestly. §8 requires every submission attempt to be journalled and
> §8.6.4 says a failed attempt SHOULD be, but a refused attempt records the state
> that caused the refusal — a `prev` naming the current binding while the envelope
> names the stale one it was signed against. Unscoped, clause 5 then rejects the
> whole journal. Gate rejections were worse: they carry no `hash` at all, while
> `artifact` must be 64 hex, so the clause could never hold.
>
> The scope follows from the clause's own rationale — "the store recorded a
> transition its author did not sign" is a statement about an entry that RECORDED a
> transition. An entry that applied nothing makes no such claim, and its envelope is
> a record of what was attempted rather than of what happened. Found by an
> independent implementation, which noted this is the round-one §8.2 defect one layer
> up: a rule written for the accepted case, applied to every case.

> This section previously named a member `envelope`, which §8.6.3 had renamed to
> `envelope_b64`. That reading FAILS OPEN, which is why it matters more than a
> typo: an implementation checking presence member-by-member finds `envelope`
> always absent, concludes no obligation applies, and accepts every forged
> publication while passing every vector. Found by an independent implementation
> reasoning about the failure direction rather than the wording.

Failing (5) means the store recorded a transition its author did not sign, which
MUST be a hard failure rather than a warning.

**ENV-VERIFY-REVISION.** For an entry carrying `parent_rev`, the value MUST equal
the envelope's `parent_rev`, and MUST equal the name's revision derived from the
journal history preceding the entry.

> Without a persisted revision, ABA replay survives offline verification
> entirely: after `n` → A → B → A, an envelope signed against the FIRST A carries
> a stale revision, but clause 5 compares only name, artifact and parent — all of
> which are valid again after the cycle — and no entry member records the
> revision that would expose it. §8.6.2 claims "ABA protection is unaffected"
> because the envelope carries the revision; the journal then discarded it at
> admission, so the protection held only inside a live store. An offline auditor
> could not check the one property the revision exists to provide.
>
> Entries predating this member are verified without the check, and a verifier
> SHOULD report that the revision was unavailable rather than that it matched.

**ENV-VERIFY-DERIVED-TRANSITION.** A verifier MUST DERIVE the transition from the
journal history preceding the entry (§8.6.2), and MUST NOT take it from the
entry's `name_transition` member. Where a stored `name_transition` disagrees with
the derived value, the entry MUST fail verification.

> **What the journal preserves: everything the publisher SIGNED, and nothing the
> registry merely COMPUTED.** That single line decides both of this section's
> record-model rules and would have predicted them. A revision the author signed
> against is evidence and is kept verbatim; a transition the registry calculated
> is a computation and is re-derived. The journal had it exactly backwards —
> storing the computed transition and discarding the signed revision.
>
> Offline verification derives facts; it does not consume them. `name_transition`
> is not a publisher claim — it is a property of journal history, and it is
> written by the store. Reading it inverts the trust model for exactly the field
> that decides whether clause 5 runs at all: a store could label a transition
> `unchanged`, attach a genuine signature over an unrelated envelope, and the
> entry would pass every clause. Demonstrated by an independent implementation of
> this section.
>
> The consequence is that entry verification is a WHOLE-JOURNAL operation, not a
> per-entry one. That is not a new cost. Chain integrity, replay protection,
> revision evolution and ownership history are already journal properties — an
> isolated entry has never been independently meaningful, and `name_transition`
> was the one field pretending otherwise.
>
> For entries recorded before the field existed, the transition is derived by
> historical reconstruction under the KIND restriction of §8.6.2, and a verifier
> SHOULD report it as reconstructed rather than as stated. History is not
> rewritten; how it was interpreted is disclosed. Entries with none of the three fields
impose no obligation and are conformant.

A store accepting a publication MUST, **before** the name moves:

- **ENV-STORE-PRINCIPAL.** verify the signing key is the **authenticated principal**, not the key the
  envelope names. Verifying against the envelope's own `author` would always
  succeed, so a caller could submit any valid statement made by anyone;
- **ENV-STORE-ARTIFACT.** recompute the artifact hash from the submitted content and require it to equal
  the signed `artifact`. Because identity is a pure function of content (§1), this
  makes agreement between the client's and the store's elaboration an enforced
  precondition;
- **ENV-STORE-NAME.** require the signed `name` to be the name being published;
- **ENV-STORE-CAS.** require the signed `parent` to be the name's current binding;
- **ENV-STORE-REV.** require the signed `parent_rev` to be the name's current revision. This is what makes ABA replay detectable, since a hash alone is not monotonic.

A statement failing any check MUST NOT move the name. The object itself MAY still
be stored: storage is idempotent under content addressing and an unreferenced
object is inert. A failed attempt SHOULD be journalled, so the log records what
happened rather than only what succeeded.

#### 8.6.4a The signature convention

"A valid Ed25519 signature" is not a single predicate, and the choices are
observable between implementations: a signature one kernel accepts another may
reject. This version pins them.

**SIG-COFACTORLESS.** Verification MUST follow RFC 8032 §5.1.7 with the **cofactorless** equation
(`[S]B = R + [k]A`), and MUST reject a signature unless:

- **SIG-S-CANONICAL.** the encoded `S` is canonical, i.e. strictly less than the group order `L`.
  Non-canonical `S` values admit a second signature over the same message under
  the same key, which contradicts an envelope being *the author's statement*;
- **SIG-POINTS-CANONICAL.** `R` and `A` are canonical point encodings.

**SIG-SMALL-ORDER.** A small-order `A` MUST be **rejected**.

> This was previously a MAY, which is not a tenable option in this position: if two
> conforming verifiers may disagree about whether one signature is valid, then
> journal validity is not portable, and "is this journal well-formed" stops having a
> single answer. The argument for permitting it — that no real key is small-order —
> is probabilistic, and conformance is not. MUST-reject is chosen over MUST-accept
> because a small-order key cannot make a meaningful authorship claim: signatures
> under it verify for parties who do not hold it.

Signing is deterministic (RFC 8032), so a signature is a function of the private
key and the message with no randomness to record. Conformance vectors therefore
pin a seed rather than a nonce.

#### 8.6.5 What this does not guarantee

The envelope expresses a compare-and-swap; it does not by itself make application
atomic. Where compare and update are separate operations, two correctly signed
publications naming the same parent can both verify before one overwrites the
other. Historical **replay** is prevented — an old envelope fails the parent or
revision check — but concurrent same-parent publication is not. A store claiming
atomic signed transitions MUST enforce the comparison and the update in a single
atomic operation; an implementation that cannot MUST NOT claim it.

Distinct signing keys for spec and body are also not evidence of independent
authorship: one process holding both keys produces an identical record. Custody
separation is a separate, stronger claim and is not established by any mechanism in
this version.

### 8.7 Namespace reservation (prefix authority)

Two questions about a name look alike and are not the same:

| question | answered by | established |
|---|---|---|
| who first bound `alice/foo`? | exact-name ownership | INFERRED from a publication |
| who may govern `alice/*`? | prefix authority | DECLARED by a signed act |

The first may be inferred, because binding a name IS the act. The second MUST NOT
be: publishing one child would otherwise capture a whole namespace its publisher
never reasoned about.

**RES-NEVER-INFERRED.** An implementation MUST NOT derive prefix authority from
any publication, however many names beneath the prefix a key has published.
Prefix authority arises ONLY from a reservation recorded under this section.

#### 8.7.0 The objects

This section reasons about five objects. Each is defined HERE, before any rule
uses it, because a rule that compares, orders, or replays an undefined object is
an algorithm without an alphabet.

**AUTHORITY.** The authority over a prefix is the PUBLIC KEY currently governing
it, or the sentinel `-` when no key does. It is a principal, not a record and not
a hash.

This follows the division the rest of this specification already makes: FACTS are
identified by hashes, CLAIMS AND AUTHORITY by principals. A reservation is an
authority act BY a principal, so the value a reservation compares against is the
principal it expects to find. Were it a hash, a further rule would immediately be
needed to say WHICH hash, since one authority may issue many reservations, and
that rule would carry no additional meaning.

**AUTHORITY REVISION.** The authority revision of a prefix is the VERSION OF ITS
AUTHORITY STATE: a non-negative integer, `0` for a prefix no key has ever held.

**RES-REV-SUCCESSOR.** An accepted reservation carrying `authority_rev = n`
advances the prefix's authority revision TO `n + 1`. It is stated as a
destination rather than as an increment deliberately: under the concurrency
§8.7.6 admits, two reservations may both be accepted carrying the same `n`, and
an increment rule would then demand `n + 2` while the derived state is `n + 1`.

The first draft said "advances by exactly one" and forbade accepting a
reservation that left the revision unchanged — which the tie case violates, since
the second acceptance moves nothing. A rule contradicted by a state the same
section admits is reachable is a defect regardless of which reading an
implementer takes.

The successor relation is not an implementation detail; it is what gives the
revision meaning. The pair `(authority, authority_rev)` is a compare-and-swap, and
if acceptance did not advance the revision the pair could not distinguish the state
before an accepted reservation from the state after it — which is the only thing it
exists to do.

An authority revision is unrelated to the `parent_rev` of any name beneath the
prefix (§8.6.1). They count different things about different objects.

**ORDER.** Where this section says FIRST, it means first in the registry's
SERIALIZATION OF ACCEPTED AUTHORITY TRANSITIONS — the order in which the registry
committed the acceptances, as recorded in the journal.

**RES-ORDER-RECORDED.** A verifier MUST read that serialization as THE ORDER OF
ENTRIES IN THE JOURNAL. The journal is the registry's record of what it committed
and in what sequence; it carries no separate member naming a commit order, so any
other reading would make this rule unimplementable by a third party holding only
the journal.

The distinction between the two is therefore about what a registry MAY DO, not
about what a verifier reads. A registry that replicates, shards, or runs its
journal transactionally MUST record entries in the order it committed them — the
constraint falls on the writer. A verifier never has to reconstruct a commit
order; it reads the recorded one.

The wording is deliberate. An earlier draft said the order "deliberately does NOT
mean physical position in a file" and stopped there, which defined the operative
term as the one thing no member carries: §8.2.1 has no commit-order member, so a
verifier could not implement the rule that makes §8.7.4 total. Pinning a choice
before it becomes observable is legitimate; defining it as unreadable is not.

**RES-FIRST-DEFINED.** Where two accepted reservations of the same prefix carry the
same authority revision — the concurrent state §8.7.6 admits is reachable — the
EARLIER one in that serialization governs. This makes §8.7.4's resolution total; a
"highest revision" rule alone is not an order, because it ties.

**EXACT-NAME OWNERSHIP.** The owner of an exact name is the principal named as
`author` in the signed publication envelope (§8.6.1) of the FIRST accepted
publication that bound that name, in the serialization defined above.

Three consequences the dependent rules need and could not otherwise obtain:

- It is the key named IN THE SIGNED STATEMENT, not the entry's recorded author
  field. §8.6.4 already requires those to agree for an applied transition, so
  where both exist they are the same key — but only one of them is signed, and a
  rule about ownership must rest on the signed one.
- It does NOT move on re-publication. A later publication by another key does not
  become the owner, or ownership would be a moving target and the rules depending
  on it would say nothing.
- A name whose first binding publication carried NO signed envelope has no
  cryptographic owner. An implementation MUST NOT synthesise one from an unsigned
  record; it may enforce an unsigned recorded principal, but MUST NOT report that
  as cryptographic ownership.

An ACCEPTED PUBLICATION, for the purposes of this definition, is an entry whose
signed statement parses as a publication envelope (§8.6.1), whose signature
verifies against the key that envelope names as `author`, and which the registry
recorded with the status `accepted`. The publication envelope has no `pubkey`
member, so the key verified against is the one the statement itself names.

**ACCEPTED.** Replay (RES-DERIVED) counts an entry as an accepted reservation if
and only if ALL of the following hold. Anything else is not a reservation for the
purposes of this section.

1. its signed statement parses as a reservation envelope under §8.7.2;
2. that statement's signature verifies (RES-SIGNED);
3. the registry recorded the entry with the status `accepted`.

**AUTH-VERSION-IS-SIGNED-DATA.** A statement's format version is part of the
signed octets, not ambient information supplied by the verifying implementation.
A verifier MUST dispatch from the version recorded in the parsed envelope,
reproduce that version's canonical encoding when checking the signature, and
recognise every statement kind it has ever accepted.

Re-encoding a historical statement under the current emitter INVALIDATES CORRECT
SIGNATURES rather than rejecting bad ones — the failure is silent in the
dangerous direction, since a verifier that cannot read a statement reports the
history as broken rather than reporting its own gap.

Two instances, both from one format bump: verification re-encoded `/1`
statements as `/2` before checking them, and the kind-dispatch matched only the
CURRENT version constant, so older statements stopped being recognised as that
kind at all.

Both were caught against the LIVE journal and neither against the committed
corpus, which held no statements of the affected kind. **A compatibility corpus
must contain the history it exists to protect**; fixtures that predate a feature
cannot defend it.

**AUTH-REFUSALS-ARE-PRESERVED.** An authority submission path MUST journal every
AUTHENTICATED signed attempt, including refusals, before returning the refusal.

The boundary is whether there is a trustworthy signed assertion to preserve:

- MALFORMED bytes, or a signature that does not verify, MAY be refused before
  journaling. They assert nothing, and recording them would let anyone write into
  the journal by submitting garbage.
- A statement relayed by someone other than its signer MAY be refused before
  journaling. It is somebody else's assertion being replayed, and recording it
  would let any observer of a valid envelope append at will.
- A STRUCTURALLY VALID statement with a VERIFIED signature, submitted by its
  signer, MUST be journaled with the refusal status and the reason, even when it
  is refused.

A refused entry MUST produce no authority transition and no revision change; the
API MAY return an error after the append.

This is the shape a blocked publication already has — the object is stored and the
attempt journaled while the name does not move — and the inconsistency was real:
an implementation refused authenticated delegations as API errors and discarded
them, so "someone tried to delegate to this key and was refused" did not survive.
That is precisely the record an incident review needs, and a discarding
implementation destroys it while looking correct, because authority is right and
only history is gone.

**AUTH-ACCEPTANCE-IS-THE-BOUNDARY.** Protocol effects derive ONLY from accepted
journal entries. A signed but unaccepted statement is EVIDENCE, NOT STATE.

Three things exist and are routinely conflated:

| | what it is | what it creates |
|---|---|---|
| a signed statement | "I intend to reserve / delegate / revoke" | evidence of intent |
| an accepted entry | a registry validated it against current authority and journaled it | history |
| replay of accepted entries | derivation over that history | authority |

Only the second creates history, and only the third creates authority. Holding a
correctly signed envelope confers nothing, however valid its signature: a verifier
MUST NOT grant authority on the strength of a statement no registry accepted, and
an implementation MUST NOT treat possession of a signed statement as equivalent to
its having taken effect.

This governs every authority mutation — reservation, delegation, revocation, and
any operation added later. It is stated here rather than per-operation because the
per-operation reading is the one that goes wrong: each rule looks obviously
correct in isolation while the boundary it shares with the others goes unstated.

The failure is observable. A validly signed delegation submitted to a registry
that refused it — for any real reason, an unknown operation, a stale revision, a
rejected submission — grants nothing, appears in no replay, and leaves the subject
unable to publish. The signature remains valid and remains evidence of what its
signer intended; it is not state.

**RES-ACCEPTED-CLOSED.** The status test is a WHITELIST. An entry whose status is
absent, empty, unrecognised, or any value other than `accepted` MUST NOT be
counted. An implementation that instead excludes a list of known rejection words
and counts the remainder FAILS OPEN: a status it has never seen would grant
authority, and the set of statuses a registry may record is not closed by this
specification.

#### 8.7.1 What a reservation means

A reservation establishes AUTHORITY OVER A PREFIX STRING in one registry. It is
not identity, affiliation, endorsement, or authorship.

**RES-NOT-ENDORSEMENT.** A reservation of `openai/*` establishes only that a key
won this registry's first-come rule for the octets `openai`. An implementation
MUST NOT present it as evidence of any relationship to an external organisation
of that name, and MUST NOT treat it as authenticating the holder.

This is a consequence of the model, not a caveat about it. Authorship remains
what the publication envelope (§8.6) says; identity remains the artifact hash
(§1). A reservation adds a governance fact and nothing else.

#### 8.7.2 The reservation envelope

The canonical signed octets, in this exact order and count:

```
oath-reserve/1
op=reserve
namespace=<prefix pattern>
authority=<64 hex | ->
authority_rev=<decimal>
pubkey=<64 hex>
```

Terminated by LF; no trailing blank line. `-` in `authority` is the explicit
sentinel for a prefix no key holds — distinct from an empty field, because "I
checked and it was unclaimed" and "nobody populated this" are different facts.

`pubkey` names the key making the claim and is INSIDE the signed octets. A
detached signature would establish that some key signed these bytes; only this
member makes the bytes say whose claim it is.

`authority` and `authority_rev` are the authority state the reservation replaces,
as defined in §8.7.0: a PRINCIPAL and a version, not a record and not a hash.
Together they form a compare-and-swap, so a reservation is a REVISION OF
AUTHORITY rather than a standalone assertion.

They are positionally analogous to `parent`/`parent_rev` in §8.6.1 and are NOT
the same kind of value: `parent` identifies a fact and is a hash, `authority`
identifies who governs and is a public key. Read as an analogy rather than as the
definition in §8.7.0, this member has no determinable domain — which is the
defect this paragraph now exists to prevent.

**RES-AUTHORITY-REV-DISTINCT.** `authority_rev` counts AUTHORITY transitions of
the prefix. It is unrelated to, and MUST NOT be conflated with, the `parent_rev`
of any name beneath that prefix. Publishing a thousand names under `alice/*`
leaves its authority revision unchanged.

`authority_rev` is an arbitrary-precision non-negative decimal in canonical form
(no leading zeros, no sign). A verifier MUST NOT impose a machine-word bound.

**RES-HEX-LOWERCASE.** `authority` and `pubkey` MUST be lowercase hexadecimal.
The reasoning §8.6.1 gives for the same rule applies with more force here: these
values are compared as bytes against derived state in RES-AUTHORITY-CURRENT and
identify principals during replay, so `ABAB…` and `abab…` would be two different
statements about one key.

**RES-AUTHORITY-CONSISTENT.** `authority = -` MUST be accompanied by
`authority_rev = 0`, and a non-sentinel `authority` MUST NOT be. An unheld prefix
at a non-zero revision, or a held one at revision zero, describes no state this
section can produce.

**RES-PARSE-STRICT.** A parser MUST reject any octet sequence that does not
re-encode to itself under this encoding — reordered members, absent or extra
members, non-canonical integers, or an unknown format line. Signatures cover
bytes; a decoder accepting variant spellings would let two byte strings verify as
one statement.

#### 8.7.3 Acceptance rules

A registry MUST refuse a reservation failing any of these.

**RES-SIGNED.** The signature MUST verify against `pubkey` over the canonical
octets, AND the authenticated principal MUST equal `pubkey`. Both halves are
required: the first proves the key signed the statement, the second proves the
submitter holds that key.

**RES-PATTERN.** `namespace` MUST be of the form `<segments>/*` with a non-empty
prefix and no other `*`. An exact name MUST be refused — exact-name ownership is
established by publication, and a second, stronger path to it would give one name
two answers. `*` MUST be refused: a claim to the whole registry is a takeover, not
a reservation, and no first-come rule may grant it.

**RES-AUTHORITY-CURRENT.** `authority` and `authority_rev` MUST equal the
registry's currently derived state for the prefix (`-` and `0` when unheld). This
is the replay defence: a reservation signed against a state the registry has
since left cannot be applied later.

It is deliberately NOT claimed as an ABA defence. An earlier draft asserted that
"a revision never repeats even if a prefix returns to a key that held it before",
and neither half holds in this version: no operation releases, transfers or
expires a prefix, so a prefix cannot return to anyone, and under §8.7.6's
concurrency two distinct accepted transitions can carry the same revision. A
rationale must not defend against an attack the version cannot express.

**RES-FIRST-COME.** A reservation MUST be refused when a prefix held by a
DIFFERENT key OVERLAPS it in either direction — that is, when either prefix is a
segment-wise ancestor of the other. `alice/*` and `alice/sub/*` are one claim
spelled two ways, not two independent claims.

#### Transfer (`oath-transfer/1`)

Two valid principals could agree on a change of authority and earlier versions
could not express it. The rule forbidding transfer existed to stop a HOSTILE
seizure and caught every CONSENSUAL handover with it; a design that refuses a
transaction all parties want is wrong independently of any adversary.

The statement is:

```
oath-transfer/1
op=transfer
namespace=<pattern>
from_authority=<hex>
to_authority=<hex>
authority_rev=<decimal>
attempt=<64 lowercase hex characters>
```

**XFER-SCOPE.** `namespace` MUST be a prefix pattern, the same form a reservation
takes. An exact name is not transferable: exact-name ownership is inferred from a
name's first binding, not held, so there is no authority record to move.
`from_authority` MUST differ from `to_authority` — a self-transfer changes nothing
while advancing both revisions and clearing every delegation, which would be a way
to revoke wholesale without going through the delegation path.

**XFER-SIGNED-BOTH.** A transfer carries TWO signatures over the SAME canonical
octets: one by `from_authority`, one by `to_authority`. A verifier MUST check
both. A separate acknowledgement MUST NOT be accepted in place of the second
signature — a detached document can be replayed against a different transfer, and
one statement with two signatures cannot.

Neither signature is part of the signed octets; both are carried beside them. A
verifier MUST match each signature against the key it is claimed for
(`from_authority`, `to_authority`) rather than by position, so the order in which
an implementation transmits or stores them carries no meaning.

**XFER-RECIPIENT-CONSENTS.** Custody carries obligations: a reservation counts
against the recipient's cap and confers duties over everything beneath the prefix.
A namespace MUST NOT be pushed onto a key that did not countersign.

**XFER-HOLDER-CURRENT.** The signer named by `from_authority` MUST be the current
holder.

**XFER-AUTHORITY-CURRENT.** `authority_rev` MUST equal the prefix's current
authority revision; a transfer signed against any other value MUST be refused.

**XFER-RESERVATION-LIMIT.** A transfer that would take the recipient past a
registry's reservation cap MUST be refused at ADMISSION. Otherwise transfer is a
way around the cap.

A namespace held by transfer counts toward the recipient's cap exactly as a
reserved one does, and stops counting toward the sender's — the cap counts what a
key HOLDS, not how it came to hold it.

REPLAY MUST NOT RE-VALIDATE THE CAP. The cap is registry-local configuration and
its value MAY differ between registries; re-checking it during derivation would
make a journal replay to a different holder depending on the verifier's
configuration. Derivation trusts the recorded acceptance, which is what
AUTH-ACCEPTANCE-IS-THE-BOUNDARY already requires. The consequence is stated rather
than hidden: a third party cannot verify from the journal alone that a registry
applied its own cap correctly.

**XFER-NO-CAPTURE.** A transfer moves the PREFIX. Exact-name ownership beneath it
is unchanged, and retained names MUST be reported. Acquiring ground must never be
a way of acquiring other parties' names.

**XFER-ADVANCES-AUTHORITY-REV.** An accepted transfer advances the authority
revision by exactly one. A REFUSED transfer MUST NOT advance it.

**XFER-AUTHORSHIP-UNCHANGED.** Transfer changes who CONTROLS a prefix and never
who WROTE anything. Journal authorship is untouched, as it is by revocation.

**XFER-CLEARS-DELEGATIONS.** An accepted transfer revokes every delegation under
the prefix, and advances the delegation revision by EXACTLY ONE — unconditionally,
including when the prefix had no delegations, and regardless of how many it had.
Advancing per cleared delegation, or not advancing when the set was empty, would
put two conformant implementations at different delegation revisions after the
same transfer, so the next grant would be accepted by one and refused by the other
under DEL-CAS. The grants were made by the OLD
holder; carrying them across would give the recipient publishers it never
authorised — authority by inheritance, which is what reservation exists to
prevent. The revision advance is what stops a grant envelope written before the
handover from being replayed after it.

**XFER-ATTEMPT-ONE-SHOT.** A validly signed transfer attempt is SINGLE-USE. Once
the registry preserves it — as accepted OR as refused — those signed bytes can
never produce a later authority transition. A replay of a consumed attempt MUST be
refused, and MUST NOT itself be journaled: the original is already recorded, and
preserving every replay would let anyone holding the bytes grow the journal
without bound.

Consumption is keyed on `attempt`, not on the whole statement. Keying on the
octets would let a party alter one field and resubmit under the same consent,
which is the resurrection this closes. Only the two parties can consume their own
nonce, since an entry is journaled only when both signatures verify.

**XFER-FRESH-CONSENT.** A later attempt at the same transfer requires a DISTINCT
`attempt`, covered by both signatures. The nonce is required because Ed25519 is
deterministic: without a fresh signed field, two parties re-signing the same
logical transfer would produce byte-identical octets and signatures, so consuming
the bytes would make a legitimate retry impossible forever.

Together these give a refusal the meaning it appeared to have when it was
recorded — *that transaction did not happen* — rather than *it may happen
automatically once its blocking condition disappears*. A transfer refused because
the recipient was at their cap does not become effective a month later when the
cap frees; both parties must sign again.

The guarantee is deliberately narrow, and MUST NOT be described as revocable
consent. It closes resurrection AFTER a submission. It does not let a recipient
withdraw a countersignature the holder has never submitted — that would require a
separate signed cancellation, or an expiry model, and this version has neither.

**XFER-REFUSALS-ARE-PRESERVED.** Once both signatures verify and the caller is a
party, a refusal is a statement real principals made: it is journaled and confers
nothing. When EITHER signature fails to verify, the statement is not authenticated
and MUST NOT be journaled — a half-signed document attests to nothing, and
preserving it would let anyone write into the journal by pairing a real signature
with a forged one. A transfer submitted by anyone other than the two parties MUST be
refused, and such a relay is not preserved.

The net effect of an accepted transfer:

```
holder             A → B
authority_rev      n → n+1
delegates          cleared
delegation_rev     advances
exact-name owners  unchanged
authorship         unchanged
```

Transfer is NOT recovery. It requires the current holder's signature, so a lost
key cannot be transferred and this version offers no mechanism that survives key
loss.

**RES-RESERVATION-LIMIT.** A registry MAY cap how many namespaces one principal
holds; the reference registry caps it at five. Only ACCEPTED reservations count
toward the cap — a refused statement confers nothing and so consumes nothing.

The cap is a mistake guard rather than a squatting defence: keys are free, so a
determined party generates more of them. Namespaces nest, so a held prefix covers
everything beneath it and additional top-level claims are rarely necessary.

**RES-NO-CAPTURE.** Names already published beneath the prefix whose exact-name
owner is another key MUST be RETAINED by that owner. The reservation MUST NOT be
refused on their account, and MUST NOT transfer them.

RES-NO-CAPTURE is the rule most likely to be implemented backwards, so its
reasoning is normative context rather than commentary. Refusing a reservation
because some name beneath it is already owned makes every namespace DENIABLE:
anyone could publish one name into `alice/*` and permanently block Alice from
reserving it. Retention has no such vector, preserves the historical fact instead
of seizing it, and is simply most-specific-wins (§8.7.4) applied consistently — an
exact name is more specific than a prefix, so its owner keeps it. A registry
SHOULD report the retained names to the reserver at reservation time.

**RES-PROSPECTIVE.** A reservation MUST NOT alter the authorship, ownership, or
transition of any entry recorded before it. Authority changes what MAY happen
next; it never rewrites what happened.

#### 8.7.3a What authority DOES

The rules above say how authority is established. This says what it is FOR, and
without it a conformant registry could accept every reservation, resolve every
name, and let authority govern nothing.

**RES-GOVERNS-BINDING.** Where a reservation governs a name (§8.7.4), a registry
MUST refuse to bind that name to an artifact on behalf of any principal other
than the prefix's current holder.

**RES-EXACT-OWNER-PREVAILS.** Where the name additionally has an exact-name owner
(§8.7.0) who is not the prefix holder, that owner MAY continue to bind it and the
prefix holder MAY NOT. This is RES-NO-CAPTURE at binding time: the reservation
promised not to seize the name, and a promise kept only at reservation time and
broken at every subsequent publication is not kept.

**RES-GOVERNS-INDEPENDENT-OF-CONFIGURATION.** This refusal MUST NOT depend on any
operator configuration. A reservation is an authority record in the journal, and
a registry that enforced it only when separately configured to would leave a
publisher's namespace unprotected on exactly the registries where no operator has
acted on their behalf — which is the case the operation exists for.

Nothing here grants the holder authority OUTSIDE the prefix, and nothing changes
what §8.7.1 says a reservation means: refusing a binding is an access decision by
one registry, not a statement about who anyone is.

#### 8.7.7 Delegation

A holder MAY grant another key permission to bind names under their prefix,
WITHOUT granting authority over it. The grant and its withdrawal are signed acts
recorded in the journal.

This exists so that automating publication does not require handing over the
namespace. Since this version has no transfer, release or expiry, a key that
holds a prefix holds it permanently — so a deployment that gave its automation
the holder's key would be choosing between never automating and accepting a risk
it could never undo. Delegation makes that choice unnecessary.

**DEL-PERMISSION-NOT-AUTHORITY.** A delegate MAY bind names under the prefix. A
delegate MUST NOT reserve any prefix, MUST NOT delegate onward, and MUST NOT
revoke. Only the holder may grant or withdraw, and authority never moves.

**DEL-GRANTOR-IS-HOLDER.** A delegation is counted only if its `authority` equals
its `pubkey` AND that key is the prefix's holder as derived from the entries
preceding it. A key that briefly held a prefix MUST NOT leave behind delegations
that outlive its authority.

**DEL-NO-SELF.** A grant whose subject equals its grantor MUST be refused. It
conveys nothing, and it would create a record whose revocation would appear to
remove the holder's own rights.

**DEL-REV-DISTINCT.** A prefix carries a DELEGATION REVISION versioning its
delegated-permission state, separate from the authority revision versioning who
HOLDS it. Every accepted grant or revocation advances it by exactly one; a
REFUSED statement MUST NOT advance it.

**DEL-CAS.** A grant or revocation MUST state the delegation revision it
replaces, and MUST be refused when that value does not equal the registry's
derived state.

Without it, revocation is not durable: a grant signed BEFORE a revocation stays
submittable AFTER it, because nothing in the envelope records which permission
state it was written against — so resubmitting the original bytes silently
re-activates a revoked delegate. That was demonstrated, not theorised, and the
realistic replayer is not an attacker but a retry loop or a redeploy holding an
old envelope.

The two revisions MUST NOT be merged into one. They version different states, and
sharing a counter would make every delegation event invalidate any pending
statement signed against the prefix's authority.

**DEL-REVOCABLE.** A withdrawal takes effect from the point it is recorded.

**DEL-REVOCATION-RECOVERS.** After withdrawal, the former delegate MUST NOT be
able to bind or repoint any name it published under the prefix. Those names are
governed by the holder again.

This is the rule that makes "revocable" mean anything, and it does not follow
from DEL-REVOCABLE alone. Exact-name ownership is established by first
publication, so a delegate acquires ownership of everything it binds — and
RES-EXACT-OWNER-PREVAILS would then protect that ownership FROM THE HOLDER. A
revoked key would keep every name it had published and could go on repointing
them, so revocation would stop new names and recover nothing.

The resolution is that RES-NO-CAPTURE's retention is scoped to names that PREDATE
the reservation, which is what it was always for: a reservation must not seize a
name somebody already held. A name bound under the prefix afterwards belongs to
the namespace, whoever signed the publication.

AUTHORSHIP IS UNAFFECTED. The journal continues to record which key signed each
publication, and revocation does not and must not alter that. What returns to the
holder is CONTROL of the name, not credit for the work.

A compromised delegate can therefore publish until it is revoked, can do nothing
to prevent the revocation, and retains nothing afterwards but the historical fact
that it signed.

**DEL-ACCEPTANCE.** A registry MUST validate a submitted delegation before
journaling it: the signature verifies, the authenticated caller is the signer, the
signer holds the prefix NOW, and `(authority, authority_rev)` match the derived
state. A grant that would change nothing (re-granting a current delegate) and a
withdrawal of a key that is not a delegate MUST both be refused rather than
recorded — a revocation that appears to succeed against a key that was never
granted tells an operator they removed access they never gave, which is the wrong
thing to believe during an incident.

Replay remains the verifier and MUST NOT rely on this having happened: it ignores
any grant it cannot justify from the journal alone. The duplication is deliberate.
A registry that only checked at submission would be trusting its own past
acceptance; a verifier that only checked at replay could not explain a refusal.

**DEL-DERIVED.** The current delegate set MUST be derived by replaying the
journal, never read from a stored table. A stored permission table could be
edited to un-revoke a key; a replayed one cannot, because the withdrawal remains
in the history.

The envelope is `oath-delegate/1`, carrying `op` (`delegate` or `revoke`),
`namespace`, `subject`, and the same `(authority, authority_rev)` compare-and-swap
a reservation carries — a grant signed against an authority state the prefix has
since left is a grant its signer would not make today.

#### 8.7.4 Authority replay and resolution

**RES-DERIVED.** Authority state MUST be derived by replaying the journal,
counting exactly the entries §8.7.0 defines as ACCEPTED. An
implementation MUST NOT maintain a mutable ownership table as the authority: a
stored summary can drift from the history it summarises, and the journal is what
a third party actually holds. The current holder of a prefix is the `pubkey` of
the accepted reservation with the highest authority revision for that exact
prefix string, and where revisions tie, the earlier one in the serialization
(RES-FIRST-DEFINED). Highest-revision alone is not an order.

**RES-MOST-SPECIFIC.** Where several reservations cover a name, the one with the
LONGEST prefix governs. Where a reservation and an exact-name owner both apply,
the EXACT NAME governs (this is RES-NO-CAPTURE at resolution time).

**RES-SEGMENT-MATCH.** A prefix `p/*` covers a name `n` if and only if `n` begins
with `p` followed by the separator `/`. Matching is by SEGMENT, never by
substring: `alice/*` covers `alice/foo` and `alice/sub/foo`, and does NOT cover
`alice2/x` or the bare name `alice`. A namespace and a definition sharing leading
characters are different things.

#### 8.7.5 Protocol roots

A small fixed set of first segments is not reservable. This version defines:

```
key
sys
```

These are INTRINSIC to the protocol: their meaning is assigned by the kernel and
no publisher governs them. A namespace that is merely important to the protocol —
including one holding a standard library — is NOT intrinsic; it has a governing
party and therefore MUST remain reservable, or the protocol could never represent
who governs it.

**RES-PROTOCOL-ROOT.** A reservation whose FIRST SEGMENT equals a protocol root
MUST be refused. Compared on the first segment only: `oathkeeper/*` and `keys/*`
are ordinary first-come prefixes.

**RES-ROOT-CONSTRAINS-RESERVATION-ONLY.** A protocol root constrains RESERVATION
and nothing else. A name such as `key/foo` that exists, or is later published,
retains its exact-name ownership and its history unchanged; an implementation
MUST NOT refuse, seize, or reinterpret it merely because its prefix cannot be
reserved. Unreservable is not forbidden.

**RES-ROOTS-NOT-CONFIGURABLE.** The protocol-root set MUST be fixed by the
implementation. It MUST NOT be operator-configurable, since an editable reserved
list reintroduces the allocation power reservation exists to remove.

#### 8.7.6 Known limits

These are properties of this version, not defects to be worked around.

- **Squatting is permitted, deliberately.** Human-readable prefixes are
  first-come. Holding a string confers no ability to impersonate anyone
  (RES-NOT-ENDORSEMENT): every artifact is signed, every publication attributable,
  every authority chain public. The alternative — allocating attractive names by
  some other mechanism — would be registry configuration outside the protocol, and
  would reintroduce the operator this section removes.
- **Transfer is a distinct operation.** A reservation naming a prefix another key
  holds MUST still be refused rather than treated as a transfer. Changing hands is
  `oath-transfer/1`, below, and requires both parties' signatures.
  DELEGATION exists (§8.7.7) and is a different act: it grants permission to
  publish without granting authority.
- **First reservation is not atomic** where compare and append are separate
  operations, exactly as §8.6.5 describes for publication. Two first reservations
  of the same prefix can both verify. A registry claiming atomic first-come MUST
  enforce the check and the append in one atomic operation.
- **Authority is per-registry.** A reservation is a fact about one journal.
  Nothing in this version relates reservations across registries.
- **A protocol root is not a protected root.** Because it cannot be reserved, a
  name beneath one is governed only by whatever exact-name ownership the registry
  has enabled — whereas an ordinary prefix can be defended by a reservation with
  no operator configuration at all. "Unreservable" therefore means LESS
  registry-enforced protection, not more. This is acceptable only because protocol
  roots exist to hold kernel-defined names; an implementation MUST NOT rely on
  protocol-root status to protect a published name.

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

The conformance suite SHOULD be materialized as fixtures, not only prose:

```
fixtures/
  canonical/*.bin           raw canonical O1 Def bytes
  hashes.txt                name/hash table from elaborating examples
  gate/*.oath               accepted and rejected examples
  verify/*.txt              property verdicts and counterexamples
  analyses/*.json           termination, confinement, mutation scores
  prove/*.smt2              emitted obligations or obligation hashes
  prove/outcomes.json       solver version, outcome, method, detail;
                            per-property author hints (#67)
  api/*.txt                 stable CLI/MCP text outputs
```

**Hints in the fixture channel (normative, #67).** Author hints are store
METADATA, not source: a kernel that reads only `.oath` text cannot know them, so
a hinted goal's script would be irreproducible from source alone. `prove/
outcomes.json` therefore carries them, per property, alongside the recorded
proof outcome — it is already the channel by which recorded proof state reaches
an independent kernel, and a hint is proof state in the same sense. Each entry
names its target as `{"def": <definition hash>, "prop": <index>}`; targets are
HASHES, never names, so they resolve identically in any kernel (each computes
hashes itself). A kernel reproducing `prove/scripts.txt` MUST apply the hints
given for a property when assembling that property's lemma set, and MUST NOT
apply them to any other property. The field is absent when a property has no
hints, so a hint-free corpus produces byte-identical fixtures to one from a
kernel predating the feature.

### 10.0a Fixture filenames

A fixture derived from a definition NAME is stored under an ENCODED filename. The
encoding is applied to EACH CHARACTER of the name in turn, and the results
concatenated:

| character | output |
|---|---|
| `_` | `__` |
| an ASCII uppercase letter, `A`–`Z` | `_` followed by that letter's ASCII lowercase form |
| anything else | unchanged |

`Map` becomes `_map`, `map` stays `map`, `_map` becomes `__map`, and `KV`
becomes `_k_v` — the per-character rule applied twice.

**CONF-FIXTURE-FILENAME.** Implementations MUST use this encoding for every
fixture named after a definition — `canonical/`, `analyses/`, `verify/`. It does
NOT apply to fixtures named after anything else: §1.5's golden encoding cases are
case names, not definition names, and encoding them would rename `bool_bytes.bin`
to `bool__bytes.bin`.

The encoding is an INJECTION, not a bijection. Distinct names always produce
distinct filenames, so nothing collides — but the image is a proper subset of all
strings, so DECODING IS PARTIAL. `Map`, `_1` and `m_` are not in the image and
have no pre-image. A decoder MUST reject them rather than guess:

- read left to right; a character other than `_` is itself;
- `__` is `_`;
- `_` followed by an ASCII lowercase letter is that letter's uppercase form;
- `_` followed by anything else, or by nothing, is NOT a valid encoded filename.

> Earlier text asserted "the encoding is a bijection, so a name is recoverable
> from its filename". That is false, and load-bearing: recoverability is real but
> partial, and a decoder written to the claim would have no rule for input outside
> the image. Found by an independent implementation, which checked the property
> rather than accepting the assertion.

**KNOWN LIMITATION — the rule does not fully achieve its purpose.** Only ASCII
`A`–`Z` are escaped, so names differing only by NON-ASCII case still collide on a
case-insensitive filesystem: `Élan` and `élan` both encode to themselves, and one
silently overwrites the other. This is the `map`/`Map` defect the encoding was
introduced to fix, surviving for names outside ASCII.

Extending the escape to full Unicode is not a drop-in repair — Unicode case
mapping is not injective (U+212A KELVIN SIGN and `K` both lower-case to `k`) and
is not one-to-one in length (U+0130 maps to two code points), so the encoding
would stop being an injection at all. The current corpus is entirely ASCII, which
is why the gap is latent rather than active. It is stated here rather than left
for the next reader to rediscover.

**CONF-COVERAGE-FROM-CORPUS.** A conformance check over a fixture family MUST
derive its work list from the CORPUS — the set of names bound in the store,
enumerated in `hashes.txt` — and never from the fixture directory.

Where a family emits a fixture for every definition (`canonical/`, `analyses/`),
the check MUST require one per definition. Where a family is SPARSE by
construction, it MUST instead require presence AGREEMENT: a fixture exists if and
only if the definition emits one. `verify/` is sparse — a data declaration or a
property-free function produces no verdict text, so the corpus of 187 yields 168
verify fixtures, and demanding one per definition would require 19 files that
must not exist.

Directory enumeration defines coverage as "whatever files exist", so a missing
fixture is not compared, not counted, and cannot fail. The check reported "186
canonical/*.bin byte-identical" — true, and useless, because the number described
the test rather than the thing tested.

**CONF-FILENAME-EXACT.** A check comparing fixture filenames MUST match names
EXACTLY, against a directory listing. It MUST NOT rely on filesystem lookup
(`test -f`, `open`), because on a case-insensitive filesystem a lookup for
`_Map.bin` succeeds when only `_map.bin` exists — and a byte comparison then
compares a file with itself and passes.

> This made CONF-FIXTURE-FILENAME unfalsifiable on macOS, the platform it was
> written for. Measured: an implementation emitting `_X` instead of `_x` produced
> 0 failures under lookup-based checking and 15 under exact-name matching. A rule
> that cannot fail on the developer's own machine is not being tested there.

### 10.1 Signed-publication vectors

`fixtures/envelope/vectors.jsonl` is the conformance surface for §8.6. It is JSONL,
and every octet string is carried as canonical base64 (§8.6.3) — deliberately, so
reading the fixtures requires no knowledge of any implementation's string-literal
syntax. A fixture format that presumes the reference language is not a cross-kernel
fixture.

Four record kinds, each keyed by `kind`:

- `canonical` — a structured `envelope` and the `octets_b64` it MUST encode to.
  These octets are what a signature is computed over, so one differing byte makes
  every signature that implementation produces unverifiable elsewhere. The failure
  is silent: the signature stays correct and stops verifying.
- `reject` — `octets_b64` (or `envelope_b64`) a conformant implementation MUST
  refuse, with the reason.
- `signature` — the whole path: `envelope`, `octets_b64`, `envelope_b64` as stored,
  `author_pubkey`, `author_sig`, `journal_line_b64`, and the expected `verdict`.
  Tamper records carry `tampered_field` and MUST fail verification; a signed field
  that still verifies after tampering is not bound by the signature.

Keys are derived from `seed_b64`. Signing is deterministic (§8.6.4a), so a
signature is a function of seed and octets.

Coverage is stated here because a vector suite that silently under-covers reads as
conformance it has not established. Round two of the blind-implementation exercise
passed every vector while three defects survived in the prose, and the three rules
§8 had most recently added were exactly the three nothing exercised — prose written
in response to an audit is the most likely to lack a fixture, because nothing has
ever run against it.

> **This paragraph previously overstated coverage on four of its five clauses.** An
> independent implementation measured the suite by disabling each normative rule in
> turn and re-running it: **15 of 17 rules could be deleted with the suite still
> passing.** A conformance claim is a claim like any other, and this one was not
> derived — it was asserted from having written the vectors. The corrected statement
> follows, and the method is the one this project already uses on specifications:
> mutate the thing under test and see whether anything notices.

Witnessed, and measured to be so: §8.6.1's encoding and its rejection rules;
§8.6.3's base64 dialect including non-canonical spellings; §8.2.1's member order;
§8.6.4a's canonical-`S` rule and its small-order rule (the latter only since a vector
carrying an actual universal forgery replaced one that any rule-less verifier passed).

NOT witnessed, and each is a real gap rather than an oversight:

- **§8.6.2's revision arithmetic, entirely.** The vectors carry revision *values*, not
  arithmetic. The transition table, the three-valued member, the FOLD, the KIND
  restriction, the A→A no-op and ABA are all unexercised.
- **Three of §8.6.4's five clauses.** Clause 1 needs an entry with a PARTIAL field set;
  clause 3 is only distinguishable from clause 4 when a signature is valid under
  `author_pubkey` while the envelope names someone else; clause 5 needs an entry
  disagreeing with its envelope, and the file contains exactly one journal line — the
  honest one.
- **§8.2.1's escaping, §8.2.2's entry digest, §8.4's signed content, and §8's chain.**
  There is no journal fixture family and no expected digest or chain value anywhere in
  the tree, so two implementations can compute different publication identities and
  both conform.
- **The reachable majority of §8.6.1's value-character rule.** LF is unreachable from
  the parse side, but CR, DEL and NUL are not: `name=a\rb` is seven well-formed lines
  whose value contains a forbidden character.
- **Cofactorless versus cofactored verification, and small-order `R`.** §8.6.4a names
  only `A`, and no vector distinguishes the two equations.

Also witnessed, via a fourth record kind: §8.6.4's three store-side MUSTs. A `store`
record carries a `state` (what the store currently believes about the name) and a
`request` (the signed octets, the signature, the authenticated principal, and the
artifact hash the store recomputed from submitted content), with the expected
`verdict`. Expressing these needs state-plus-request rather than a single record,
which is why they were unwitnessed until now — and half of §8.6's normative weight is
store-side, so a kernel could previously pass every vector without implementing any of
it.

Currently NOT witnessed: the LF-in-a-value rule of §8.6.1, which is unreachable from
the parse side by construction (an injected LF presents as a line-count error), so it
is an encoder obligation a fixture cannot express; and full §8.6.4a small-order
coverage beyond the identity point, which needs curve arithmetic.

## 11. Campaign identity (normative, #74)

A mutation score is evidence, and evidence is only trustworthy when the
computation that produced it has a REPRODUCIBLE IDENTITY. A bare killed/total
answers "how many" and never "out of which mutants, under which policy" — so the
score is attached to the digest of a CAMPAIGN DESCRIPTION, and a consumer decides
whether the score is current by comparing digests rather than by reasoning about
version strings and dates.

This section specifies **only the identity**: the description, its encoding, and
the digest. It does NOT specify the mutation engine, the worker, scheduling, or
storage — those are registry concerns and may differ between implementations. The
obligation on a kernel is a pure function:

    campaignEncode : CampaignDescription → bytes
    campaignHash   : CampaignDescription → Hash

Both are obligations: §11.2 and §11.4 pin the ENCODING, not merely the digest, so
a kernel must expose the bytes to be checkable.

A kernel need not run mutation testing. It must be able to reproduce the identity
of the computation being CLAIMED, which is what lets an auditor reconstruct a
description and verify the digest instead of trusting the registry's assertion
that some hex string is the right one. Specifying it is what stops "independently
auditable evidence, except for the function that decides which evidence counts".

### 11.1 The description

Every field is included because it can change the outcome:

| field | why it is identity-bearing |
|---|---|
| `artifact` | a score is about ONE object (its content hash) |
| `kernel` | evaluation semantics decide whether a mutant is caught |
| `engine` | the mutant generator revision; a new mutation kind is a different campaign |
| `cases` | a survivor at 60 cases may be a kill at 600 — the budget is part of the claim |
| `fuel` | evaluation budget per case |
| `waiver-policy` | how waivers affect the score |
| `waivers` | the waived-mutant SET. Waivers count toward the score, so adding one changes the number WITHOUT changing the code — the drift this digest exists to expose |

### 11.2 Canonical encoding (BYTE-significant)

A domain separator line, then one `key=value` line per field in exactly this
order, each terminated by a single LF (`0x0A`). No spaces around `=`, no trailing
blank line, UTF-8 throughout:

```
oath-campaign/1
artifact=<64 lowercase hex>
kernel=<kernel version string>
engine=<engine id>
cases=<decimal>
fuel=<decimal>
waiver-policy=<policy id>
waivers=<hex,hex,...>
```

**The encoding is CANONICAL BY DEFINITION**: it emits exactly one byte
representation for every semantically identical description. Normalization
therefore belongs here and nowhere else, and the digest is simply
`SHA-256(campaignEncode(d))` — hash the bytes, interpret nothing. Splitting the
job (encoder serializes a sequence, hasher reinterprets it as a set) would leave
a conforming implementation free to hash the encoded bytes directly and get a
different answer while believing it followed this section.

`waivers` is a genuine SET — **order and multiplicity are both non-semantic**.
Waivers are keyed by mutant hash and a mutant can be waived once, so a repeat
carries no information. The encoder therefore MUST, in this order: collapse
duplicates, then sort ASCENDING as byte strings, then join with `,`. Duplicates
COLLAPSE rather than being rejected — a description naming the same waiver twice
is meaningless, not invalid.

When there are none the value is EMPTY and the line is still present (`waivers=`
followed by LF): omitting the line would make "no waivers" and "field absent" the
same bytes.

**Terminology is load-bearing here.** A field may be sorted and deduplicated ONLY
where order and multiplicity carry no meaning. An ordered execution phase, a
precedence rule, or any staged mutation sequence is a SEQUENCE and MUST NOT be
normalized this way — doing so would erase meaning rather than canonicalize it.
`waivers` is the only set-valued field in this description.

**Field values MUST NOT contain LF (`0x0A`) or CR (`0x0D`).** A producer given
such a value MUST reject the description rather than encode it. This is a
DECODABILITY requirement and NOT a collision-resistance one; the distinction is
worth keeping exact rather than overstating a cryptographic risk. No LF-based
digest collision exists: the encoder always writes exactly these seven keys in
this order, so an embedded LF ADDS a line rather than replacing a field and the
byte streams differ.

Unique decodability is required for a different reason — **an independent auditor
must be able to verify that the committed bytes correspond to the claimed
structured description.** Auditing means parsing the bytes back into fields and
re-encoding them; a stream that parses more than one way cannot be reconstructed
with confidence, so the commitment stops being checkable by anyone who did not
produce it. That is the property this rule protects.

DECODING: split each line at the FIRST `=`; everything after it is the value,
verbatim. A value may therefore contain `=` safely, and a vector pins this. (An
earlier draft justified line framing by pointing at `=` — true, irrelevant, and
the wrong character. `=` is harmless under first-match splitting; LF was the
hazard.)

`artifact` and the `waivers` members are **opaque, case-SENSITIVE byte strings
written in a restricted textual alphabet — they are NOT hexadecimal numbers whose
case is presentation trivia.** This deliberately overrides the usual convention
that hex case is insignificant, and it overrides it here even though these values
are called hashes elsewhere in this document.

Consequences, stated so no implementation "helpfully" repairs them:
- `abab…` ≠ `ABAB…`. They are DIFFERENT descriptions denoting different
  campaigns, not one description with two spellings — which is what keeps
  canonicality literal, since no hidden decoding or folding exists to disagree
  about.
- An implementation MUST NOT case-fold, normalize, or hex-decode these values at
  ANY point — not while encoding, and not after parsing. Deduplication compares
  these same literal bytes, before any decoding.
- Producers MUST emit lowercase. A description carrying uppercase is well-formed
  and simply denotes something else.

Integers are rendered as SHORTEST BARE DECIMAL: no sign, no leading zeros, no
padding, no separators. `0` encodes as `0`.

The digest is `SHA-256` over those bytes, rendered lowercase hex.

Changing this encoding changes every campaign identity, which is a fork of what
"current evidence" means and must be treated as such — not as a refactor.

### 11.3 Freshness

Given a recorded campaign digest and a description reconstructed from the
CURRENT engine, kernel, configuration and waiver set for the same artifact:

- digests equal → **MEASURED**: the score was produced under the campaign now in
  force.
- digests differ → **STALE**: authentic evidence, for a reproducible campaign
  other than the one currently recognised. STALE does NOT mean false, corrupted
  or expired, and a kernel MUST NOT present a stale score as current.
- no recorded score → **UNMEASURED**: no campaign exists. This is distinct from a
  score of zero, and MUST NOT be reported as one.

Timestamps and signatures are deliberately absent from the description. They
record WHEN and BY WHOM a result was captured, not WHAT was measured, and they
belong in the journal (§8) — which is already append-only and signed. Admitting
them here would make the identity non-reproducible and break byte-for-byte
agreement between kernels for a property nothing needs.

### 11.4 Fixtures

`fixtures/campaign/vectors.txt` pins description → digest pairs. A kernel
reproduces every digest without consulting another implementation. Three vector
classes carry the canonicality claim:

1. **Order-independence** — a two-waiver description in both recording orders
   MUST produce identical encoding and digest.
2. **Duplicate collapse** — a description naming one waiver twice MUST produce
   the SAME digest as naming it once.
3. **Member sensitivity** — changing one set member MUST change both encoding and
   digest.

Plus: the empty-waiver line; a description differing only in case budget (a
survivor at 60 cases may be a kill at 600, so the budget is part of the claim);
UPPERCASE hex (a different description, since nothing folds case); single-digit
and zero integers (pinning shortest bare decimal); a THREE-member set given out
of order (a two-member vector cannot distinguish a sort from a swap); and a value
containing `=` (harmless under first-match splitting — pinned rather than argued
about).

The file's own format: each block is the canonical encoding followed by a
`digest=<hex>` line, blocks separated by a blank line. The `digest=` line is not
part of the encoding — it is distinguishable because §11.2's key set is closed.

No second implementation is trusted until it passes the fixtures without
consulting the Go source.

## 12. License evaluation (normative)

A publication may ASSERT licensing terms in its signed envelope (§8.6.1,
`license`). This section defines what a registry may DERIVE from those
assertions across a dependency closure, and how such a derivation is
identified.

The two are different claims and MUST NOT be reported as one. An assertion is
signed, historical and the publisher's. An evaluation is computed, will be
recomputed differently as the model changes, and is the registry's.

**This section does not define legal meaning.** SPDX supplies identifiers, not
semantics; the mapping from an identifier to the grants below is a MODEL, is
versioned, and is fallible. An evaluation reproduces what a particular model
concluded from particular signed assertions — nothing more. It is not advice,
and it MUST NOT be reported as a proof (§7): compatibility over a finite lattice
is decided by evaluation, not proved over unbounded inputs.

Each normative rule below carries a STABLE IDENTIFIER. Identifiers exist so a
conformance report can say which obligation a vector witnesses, and so a reader
can tell a specification defect from a missing fixture — without them, "the
vectors constrain something the prose does not" and "the prose states something
no vector witnesses" are indistinguishable.

### 12.1 Three-valued grants

An evaluation reports, per dimension, one of `YES`, `NO`, `UNSTATED`.

`UNSTATED` is not a synonym for `NO`. It means the model has no basis to answer,
and the distinction is what stops an absence becoming a permission.

The dimensions are of two KINDS, and the kind determines how values combine:

- **permissions** — `commercial`, `redistribute`, `modify`, `patent_grant`.
  "May I?"
- **obligations** — `share_alike`. "Must I?"

### 12.2 Combination

For a composition — an artifact together with its transitive dependency closure
— each dimension is folded across every member's asserted terms.

**LICENSE-PERMISSION-NO / LICENSE-PERMISSION-UNKNOWN.** Permissions combine
conservatively toward denial:

| inputs contain | result |
|---|---|
| any `NO` | `NO` |
| otherwise, any `UNSTATED` | `UNSTATED` |
| otherwise | `YES` |

**LICENSE-OBLIGATION-YES / LICENSE-OBLIGATION-UNKNOWN.** Obligations combine
conservatively toward the obligation:

| inputs contain | result |
|---|---|
| any `YES` | `YES` |
| otherwise, any `UNSTATED` | `UNSTATED` |
| otherwise | `NO` |

The two orders are OPPOSITE and a conformant implementation MUST NOT use one
operator for both. A prohibition anywhere defeats a permission; a reciprocal
requirement anywhere binds the whole composition. Folding an obligation with the
permission rule reports "no reciprocal obligation" about terms nobody has read.

`UNSTATED` is CONTAGIOUS in both directions. Ignorance can establish neither a
permission nor the absence of an obligation, so one unknown input makes the
composition unknown however many others were definite.

Equivalently, over the total order `NO < UNSTATED < YES`, permissions fold as
MINIMUM and obligations as MAXIMUM. Contagion is then not a separate rule but a
consequence of `UNSTATED` sitting between the two definite values. An
implementation MAY use either formulation; they agree on every input.

**LICENSE-FOLD-NONEMPTY.** The fold is NEVER applied to an empty input set. A
composition always contains at least the artifact itself, so an implementation
that reaches an empty fold has mis-assembled the closure, and MUST yield
`UNSTATED` in every dimension rather than the operator's identity element.

> This matters because the identity for permissions is `YES` — the tables above,
> read literally over zero inputs, fall through both rows and grant everything.
> A false `YES` produced by reading the specification CORRECTLY is a
> specification defect, not an implementation defect, and it is exactly the
> permissive direction the note below demands be checked. Found by a blind
> implementation working from this section alone.

> The asymmetry that matters: a false `UNSTATED` is inconvenient, a false `YES`
> is harmful. An implementation MUST be checked in the permissive direction —
> for each dimension, mutating the combiner toward permissiveness must be
> detectable. A suite that catches false denials while missing false permissions
> is pointed the wrong way for this domain.

### 12.3 Model lookup

An asserted expression resolves to a set of grants, or to none.

**LICENSE-LOOKUP-UNKNOWN.** An implementation MUST yield all-`UNSTATED`, never a
grant. The tests are applied IN THIS ORDER, and the order is normative:

1. the expression is absent, or is the explicit no-assertion sentinel `-`;
2. the expression is COMPOUND — containing ` OR `, ` AND `, or ` WITH `;
3. the identifier is not in the model.

**LICENSE-LOOKUP-PRECEDENCE.** The compound test precedes the model lookup.
Since a model MAY contain any set of identifiers, one containing a compound key
would otherwise let a lookup-first implementation RESOLVE a compound expression,
which `LICENSE-LOOKUP-COMPOUND` forbids. Unordered, the two rules contradict each
other on an input the specification permits to exist.

**LICENSE-LOOKUP-EXACT.** The identifier is matched by EXACT OCTET EQUALITY. An
implementation MUST NOT case-fold, trim whitespace, strip punctuation, or
otherwise normalise the expression before lookup. An unmatched identifier is
already safe — it yields `UNSTATED` — so normalisation buys nothing and risks
everything: it converts a value the publisher did not write into a grant.

> This was the largest unconstrained surface in this section. With matching
> undefined, `mit`, `MIT ` and `(MIT)` each yield `UNSTATED` from one conformant
> registry and a FULL COMMERCIAL GRANT from another that helpfully normalises,
> with both passing every vector. §12.2's fold was checked hard in the permissive
> direction while the lookup one layer down was not checked at all — and the fold
> cannot protect against a wrong answer handed to it.

**LICENSE-LOOKUP-COMPOUND.** Compound expressions MUST NOT be resolved by the
registry. `MIT OR Apache-2.0`
requires choosing a disjunct, which is a decision with legal consequence and
belongs to the consumer.

A model MAY contain any set of identifiers. A missing entry is safe: it yields
`UNSTATED`. A wrong entry is not, because it yields a confident falsehood.

**LICENSE-ASSERTED-BY-PUBLICATION.** A licence is asserted by a PUBLICATION, not
by a name transition. Every ACCEPTED publication of a name carries its author's
asserted terms, whether or not the name moved — an entry whose transition is
`unchanged` asserts terms exactly as one whose transition is `applied` does. Only
an entry that published nothing (`none`) asserts nothing.

Scoping the assertion to `applied` discards the terms on a re-publication of
identical content, which is precisely how RELICENSING works: the code does not
change, the terms do. It would also make dual licensing unexpressible, since a
second publication of the same artifact under different terms would be silently
ignored rather than recorded.

> Found by the first real signed publication to a registry, after the fixture
> family reported 22/22 obligations witnessed. No vector could have caught it:
> vectors supply assertions directly to the evaluator and never travel through a
> name transition, so the entire coupling between publication and transition was
> outside what the witness family could express. A conformance suite cannot
> witness an interaction its fixtures do not model.

**LICENSE-MODEL-SCHEMA.** The published model is a JSON object with exactly
these members:

| member | type | meaning |
|---|---|---|
| `model` | string | the model version bound as `model=` by §12.4 |
| `note` | string | informational; carries no normative weight |
| `licenses` | object | identifier → row |

A ROW is a JSON object with exactly the five dimension members of §12.1 —
`commercial`, `redistribute`, `modify`, `patent_grant`, `share_alike` — each
whose value is exactly one of the strings `YES`, `NO`, `UNSTATED`.

An implementation MUST treat as `UNSTATED`, for that dimension:

- a row member that is absent;
- a value outside `{YES, NO, UNSTATED}`, including one differing only in case.

Both are safe by the argument §12.3 already makes — a missing entry yields
`UNSTATED`, a wrong entry yields a confident falsehood — and BOTH readings had to
be stated, because the permissive alternatives are the natural ones. Defaulting
an absent `share_alike` to `NO` reports "no reciprocal obligation" about a row
that never said so, and case-folding `"yes"` to `YES` grants terms nobody wrote.

The model MUST NOT carry an `engine` member. The engine is the evaluator and the
model is the lattice it consults (§12.3, LICENSE-ENGINE-DEFINED); carrying one
inside the model's bytes made `engine=` and `model-digest=` non-independent
components of the same identity.

> None of this was specified. A blind implementation could read the supplied
> file but could not know its shape was fixed, what an out-of-vocabulary value
> meant, or which member was the version — so a schema change would silently
> break every independent implementation while every gate stayed green.

**LICENSE-MODEL-PUBLISHED.** The model is NOT part of this specification, and
this section deliberately does not fix its contents: a lattice mapping
identifiers to grants is a policy artifact with legal consequence, it is
expected to be corrected and superseded, and §12.4 already makes each version
distinguishable. Freezing it here would make every correction a specification
fork.

It MUST NEVERTHELESS BE PUBLISHED. Conformance is always relative to a NAMED
model, and the model this corpus is scored against is supplied as a fixture at
`fixtures/license/model.json`, carrying the same version string the digest
binds. An implementation reproduces verdicts by reading that file, not by
inferring the table from the vectors.

> Before this was stated, §12.3 described a mechanism whose inputs appeared
> nowhere: a reader with the prose and no fixtures could not derive a single
> verdict, and an independent implementation reverse-engineered rows from the
> vectors instead. That silently promotes the fixtures to normative text —
> whereupon a row no vector exercises is unconstrained, and any values at all
> for it pass the suite. The model must be an input the specification POINTS AT,
> not one it implies.

### 12.4 Evaluation identity

An evaluation MUST record the ENGINE, the MODEL version, the POLICY, and a
DIGEST over the assertions consumed. Without them a verdict is a claim whose
method can change invisibly: alter the lattice and every historical result
silently means something else. This is the requirement of §11 applied one layer
out.

The digest is SHA-256, lowercase hex, over a canonical encoding: the domain
separator `oath-license-eval/1` and one LF-terminated line each, in this order:

```
oath-license-eval/1
engine=<engine>
model=<model version>
model-digest=<64 lowercase hex>
policy=<policy>
subject=<64 lowercase hex>
input-artifact=<64 lowercase hex>       (one TRIPLE per closure member)
input-publication=<64 lowercase hex>
input-license=<asserted expression>
```

All lines are UTF-8, with no space around `=` and no trailing blank line.

**LICENSE-IDENTITY-MODEL-CONTENT.** `model-digest` is SHA-256 over the published
model's exact bytes (§12.3), and a verifier MUST RECOMPUTE it from the model it
consulted rather than accepting the field as given.

Without recomputation the field is an assertion, and the attack it exists to stop
survives intact: a registry can serve lattice `M` publicly, evaluate against `M'`
with one `NO` flipped to `YES`, and publish `model-digest = SHA-256(M)`. Every
identity then verifies against bytes nobody used. Binding the version STRING alone is not sufficient
and MUST NOT be relied on: a version string is an assertion by whoever edits the
lattice, so a table can be changed while the string stays fixed, and every
historical digest then still verifies while meaning the opposite of what it
meant. That is verbatim the harm this section opens by naming — "alter the
lattice and every historical result silently means something else" — so the rule
was stated and the encoding permitted precisely it.

> §11.2 hashes the waiver SET rather than a waiver-set version, for exactly this
> reason. §12.4 claimed to be §11 applied one layer out and then bound a name
> where §11.2 binds content. A blind implementation demonstrated the swap: flip
> one `NO` to `YES` in the lattice, leave `spdx-lattice/1` untouched, and every
> published evaluation identity still checks out.

> **A licence evaluation is a verdict about exact artifacts under exact
> publication terms. Names are discovery paths and MUST NOT contribute to
> evaluation identity.**

**LICENSE-IDENTITY-SUBJECT.** `subject` is the artifact hash the evaluation is
ABOUT — the artifact §12.2's composition is formed around. It is bound in
addition to the closure, never in place of it.

Without it an evaluation identity names a method and a set of members but never
what it is an evaluation OF, and two consequences follow that the identity is
supposed to prevent. Evaluating `A` over closure `{A, B}` and evaluating `B` over
closure `{B, A}` — two entry points into one component, or a dependency cycle —
produce the SAME digest, so a verdict cannot be attributed to a subject. And
every mis-assembled closure collapses: with no members there is nothing to
distinguish them, so "we failed to assemble `app`" and "we failed to assemble
`payments`" are one published evaluation. `LICENSE-FOLD-NONEMPTY` fixes the
VERDICT for an empty closure and says nothing about its identity.

> §12.2 defines a composition as "an artifact TOGETHER WITH its transitive
> dependency closure". The distinguished artifact was in the definition from the
> start and absent from the encoding — a rule and its encoding disagreeing again,
> which is the defect class §12.4's notes already record twice.

**LICENSE-IDENTITY-ARTIFACT.** An input triple identifies its member by ARTIFACT
HASH, never by name. Names are PROVENANCE — they record how the closure was
located and MUST be reported alongside an evaluation — but they are not part of
its identity.

§9 states that names are mutable without changing identity, so binding them here
would make an evaluation change when nothing about the evaluated software did:
rename `app` to `service`, leave every artifact and every asserted expression
untouched, and the identity would move. That is the same defect as making
artifact identity depend on a repository path. It is also inconsistent with the
rest of this specification — §11.2 binds `artifact=<64 hex>`, and every verdict
the kernel records is a hash-keyed fact — so the name-bound form was the outlier
rather than the pattern.

The two claims are different and both are wanted:

| claim | answers | bound by |
|---|---|---|
| the EVALUATION | these exact artifacts, under this exact model and policy, produced this verdict | §12.4 |
| the PUBLICATION | this name pointed at this artifact when the evaluation was requested | §8.2, §8.6 |

**LICENSE-IDENTITY-PUBLICATION.** Each triple ALSO binds the PUBLICATION
IDENTITY — the entry digest of §8.2.2 — of the publication whose assertion was
consumed. The artifact hash identifies the code being evaluated; the publication
identity identifies WHOSE grant is being relied on.

Both are required, and neither substitutes for the other. Licensing is a
publication claim rather than a property of the code (§8.6.1), so the same
artifact hash can legitimately carry different assertions under different
publications — and, just as importantly, the SAME expression asserted by two
different principals is two different grants over the same bytes. Binding the
artifact and the expression alone would collapse those into one identity, which
is the same class of error as binding the name: an identity that fails to
distinguish claims that differ in who made them.

The three components answer three separate questions, and an evaluation needs
all three to be reproducible:

| component | answers |
|---|---|
| `input-artifact` | which code was evaluated |
| `input-publication` | whose grant was consumed |
| `input-license` | what terms that grant asserted |

**LICENSE-PUBLICATION-SENTINEL.** A closure member whose publication identity
cannot be determined MUST encode `input-publication=-`, the same no-assertion
sentinel §8.6.1 uses. It MUST NOT be omitted, and MUST NOT be given a
substituted value.

Omitting the line would collapse the triple into a pair and let an
unidentifiable member borrow an identifiable one's encoding, which is the same
collision LICENSE-INPUT-COMPLETE forbids one paragraph down. Substituting some
other value — a name, a placeholder string, the artifact hash again — is worse:
the digest would then attest to a publication identity that does not exist.

> The reference kernel emitted a literal `unpublished` here, a magic value
> appearing nowhere in this document. Two published vectors were consequently
> IRREPRODUCIBLE from the normative text: an independent implementation searched
> more than 160,000 candidate encodings for them and correctly refused to tune
> its implementation to match. An undocumented sentinel in the reference is
> indistinguishable, to a blind reader, from a specification that cannot be
> implemented.

**LICENSE-IDENTITY-UNAMBIGUOUS.** Each component of a triple occupies its own
LINE, and every value MUST exclude LF, CR, any octet below `0x20` or equal to
`0x7F`, and the code points U+2028 (LINE SEPARATOR) and U+2029 (PARAGRAPH
SEPARATOR). A producer given such a value MUST REJECT the evaluation rather than
encode it.

> §11.2 and §8.6.1 both state the rejection obligation explicitly; §12.4 stated
> only the exclusion, so the obligation was carried by a FIXTURE rather than by
> the prose — a vector asserting a rule the specification does not contain, which
> is the exact inversion §13 exists to detect, occurring inside the section §13
> is about.

> U+2028/U+2029 are not control octets, so an exclusion phrased only in terms of
> `0x20`/`0x7F` admits them — and a Unicode-aware line splitter (ECMAScript's
> `split(/\r?\n|\u2028|\u2029/)`, ICU, several text pipelines) then reads a
> ONE-member evaluation as a multi-member composition carrying a grant nobody
> published. §8.2.1 escapes both by name for precisely this hazard; §12.4 again
> inherited a rule's letter without its purpose. Both rules are load-bearing, and neither is decorative:

- a single `input=<member>=<expression>` line has no sound split, because a value
  may itself contain `=` — so `"a=b"` with `"MIT"` and `"a"` with `"b=MIT"`
  encode to identical bytes;
- without the character rule an expression containing an LF injects a line, so
  ONE assertion can reproduce the digest of a DIFFERENT composition — a forgery
  surface in the one value whose purpose is to make a change of method visible.

Both collisions were demonstrated against the previous encoding, and both
violated LICENSE-IDENTITY-INPUT below: the rule was already stated and the
encoding did not satisfy it. §8.6.1 had established the same character
discipline for the same reason; §12.4 inherited none of it. A rule and the
encoding that must implement it are separate obligations, and stating the
rule does not discharge the encoding.

**LICENSE-ENGINE-DEFINED.** `engine` names the EVALUATOR that produced the
verdict — the implementation of §12 itself, versioned independently of the model
it consults. The engine of this specification is `oath-license/1`. An
implementation that changes how §12's rules are applied MUST advance this
identifier rather than reuse it.

`engine` is NOT a property of the model, even where a published model file
happens to carry one for convenience: the model is the lattice, the engine is
what consults it, and conflating them makes the two components of the digest
non-independent.

> This identifier had no definition, no vocabulary and no stated source — the
> identical defect the paragraph below records for `policy`, one line further up
> and left unfixed when `policy` was repaired. Fixing one instance of a defect
> class and not looking for its siblings is how the class survives.

**LICENSE-POLICY-DEFINED.** `policy` names the rule by which the input set was
SELECTED from the store. Exactly one value is defined by this specification:

| policy | meaning |
|---|---|
| `composition` | the artifact together with its exact transitive dependency closure (§12.2) |

An implementation MUST NOT invent a policy value, and MUST treat an evaluation
carrying an unrecognised policy as one it cannot reproduce, rather than
evaluating it under `composition` and reporting agreement.

Concretely, on an unrecognised policy an implementation MUST:

- ENCODE the policy verbatim and produce the digest normally — the identity
  records what was claimed, and an evaluation nobody can reproduce still has to
  be nameable in order to be reported as unreproducible;
- WITHHOLD the verdict, yielding all-`UNSTATED`, rather than folding under
  `composition`.

> The rule previously said what an implementation must NOT do and never what to
> emit, so refusing outright and encoding-then-withholding were both conformant —
> and they disagree on the digest, which is where identity lives. A blind
> implementation chose the first, left three vectors failing rather than tune to
> them, and reported the gap. Naming a prohibition is not the same as defining
> the behaviour.

> Before this table existed, `policy` was a REQUIRED component of a normative
> identity with no vocabulary, no default, no semantics and no stated source —
> derivable only by reading it out of a fixture. It was the single hole that made
> "implementable from the prose alone" false: without the vectors an
> implementation could not produce any digest at all. A field that selects the
> input set also silently changes the verdict, so leaving it open was not a
> documentation gap but an unbounded one.

**LICENSE-INPUT-COMPLETE.** EVERY member of the selected closure contributes
exactly one input TRIPLE, INCLUDING a member that asserts nothing — which
contributes its no-assertion sentinel `-`. Members are never skipped, never
deduplicated, and a member appearing twice in the closure contributes twice.

> Skipping non-asserting members is the reading that collides. Under it
> `{a:MIT, b:(none)}` and `{a:MIT}` encode identically while their verdicts
> differ — all-`UNSTATED` against a commercial grant — so one evaluation identity
> would cover two compositions that disagree. Demonstrated, and both readings
> passed every vector.

**LICENSE-ORDER-INDEPENDENT.** Input TRIPLES are sorted before hashing by
artifact hash, then publication identity, then asserted expression, comparing by
OCTET value so ordering never depends on locale, language, or a Unicode
collation. Artifact hash alone is not a total order — the same artifact may
appear in a closure under several publications — so the full triple is what
determines the order. The same assertions evaluated in
a different order are the SAME evaluation, and MUST produce the same digest —
otherwise fixture or traversal order would make one evaluation appear to be two.

**LICENSE-IDENTITY-INPUT.** Changing the engine, the model version, the model
digest, the policy, or any consumed artifact hash, publication identity or
asserted expression MUST change the digest. Changing a NAME MUST NOT.

> This rule previously named "any consumed name" and omitted the model digest,
> the artifact hash and the publication identity — so it simultaneously
> CONTRADICTED LICENSE-IDENTITY-ARTIFACT above and under-described the encoding
> below it. A rule that enumerates the digest's components must be re-checked
> against the encoding whenever either moves; nothing else notices.

**LICENSE-CLOSURE-EXCLUSIVE.** ONLY assertions belonging to the artifact and its
exact transitive dependency closure are consumed. An artifact outside the closure
MUST NOT affect either the verdict or the digest — otherwise an unrelated
publication elsewhere in the registry could change what a composition is reported
to permit.

**LICENSE-MODEL-VERSIONED.** Changing the model MUST produce evaluations
distinguishable from earlier ones rather than reinterpreting them. A historical
verdict remains a statement about the model that produced it.

### 12.5 What an evaluation does not establish

- It is not a legal opinion, and MUST be presented as derived.
- It does not verify that an asserted expression is valid SPDX. The publication
  layer is the notary for what the author signed, not an authority on licence
  syntax; validating there would drift publication into policy enforcement.
- It does not resolve compound expressions on a consumer's behalf.
- `UNSTATED` is not permission. A consumer may adopt "treat `UNSTATED` as deny"
  or "require explicit grants"; a registry MUST NOT choose that for them.

## 13. Independent implementability (normative, measurement)

Conformance asks: does this implementation agree with the vectors? This section
asks a different question, about the SPECIFICATION rather than about any
implementation:

> Could an independent implementer build this honestly from the published
> surface alone, without hidden knowledge?

The two are not the same question, and passing the first tells you almost
nothing about the second. Two implementations can agree perfectly with each
other and with every vector while both disagree with the document — or while the
document alone determines neither of them. A specification that requires its
reader to infer rules from fixtures has ALREADY FAILED, however green the suite
is, because the next implementer is not guaranteed to infer the same rules.

### 13.1 The measurement

**IMPL-MEASURED-BY-CONSTRUCTION.** Independent implementability is measured by
an INDEPENDENT IMPLEMENTATION ATTEMPT and by nothing else. It cannot be measured
by review, by checklist, by counting rule identifiers, or by an author's
judgement that the text reads completely. The property is precisely that
somebody who does not already know the answers can reconstruct them, and only
somebody who does not already know the answers can test it.

**IMPL-SCORE-IS-NOT-EVIDENCE.** A passing vector score is NOT evidence of
independent implementability, and MUST NOT be reported as though it were. An
implementer who resolves an ambiguity by trying a reading and keeping whichever
one passes has demonstrated that the FIXTURES are sufficient — which was never
in question — while saying nothing about the prose. This inverts the usual
reading of a green suite, deliberately.

**IMPL-OBSERVATION-LABELLED.** A recorded fact that is neither asserted by a
participant nor derivable from history is an OBSERVATION — known only to the
actor that performed the event. It MUST be preserved, because nobody else can
recreate it, and it MUST be presented as an observation rather than as an
assertion or a derived fact.

An observation is authoritative about the observer and about nothing else. A
journal's acceptance timestamp is not "the publication time"; it is the time that
registry claims it accepted the publication. No verifier can check it — a
registry can backdate an entry and nothing in the record contradicts it — so a
reader MUST NOT be invited to treat it as checked. The checkable ordering
evidence is `seq` and `chain`.

Labelling is the whole obligation. An unlabelled observation is read as an
assertion by the next person, which silently converts an unverifiable claim into
apparent evidence.

**IMPL-EQUIVALENCE-PINNED.** Wherever this specification depends on two things
being "the same" — two signatures, two encodings of one value, two byte sequences
naming one publication — the EQUIVALENCE RELATION MUST be stated explicitly,
versioned, and applied consistently. An implementation MUST NOT leave it to a
library's default or to what is conventional.

Such a choice is CONVENTIONAL: there is no mathematically correct answer to "when
are two signatures the same signature", and reasonable systems differ. That is
precisely why it must be pinned rather than reasoned about. An unstated
equivalence is the defect that makes two correct implementations disagree
permanently, each certain the other is wrong, with no fixture able to arbitrate
because both satisfy every stated rule.

> §8.6.4a is the model: it selects the cofactorless equation, requires canonical
> `S`, requires canonical point encodings, and rejects small-order keys — then
> says in its own words that this version pins them, and explains why a MAY was
> untenable. That is what every equivalence decision in this document should look
> like. Where one is still open, it is open BECAUSE the convention has not been
> chosen, not because the question is unclear.

> **Three questions for any normative identity.** The rules below are stated
> separately because each is independently checkable, but they are one review
> discipline and apply to anything that hashes a semantic object — publication
> identity, campaign identity, evaluation identity, and whatever comes next:
>
> - **SOURCE** — does every byte contributing to this identity have a normative
>   source? (IMPL-NORMATIVE-SOURCE)
> - **SUBJECT** — what is this identity a name FOR? (IMPL-IDENTITY-SUBJECT)
> - **SURFACE** — is every normative input it depends on explicitly declared?
>   (IMPL-SURFACE-DECLARED, §13.1a)
> - **SESSION** — does the implementer's environment contribute knowledge the
>   surface does not? (IMPL-ISOLATED-SESSION, below)
>
> Each was learned from a defect that field-level review did not catch, and each
> is now enforced mechanically rather than by recollection.

**IMPL-REPRODUCIBLE-INSTRUMENT.** Every artifact an implementability claim
depends on — the exported surface, the tool that produced it, and the record that
verifies it — MUST be reproducible from the historical commit that produced it. A
claim whose surface can only be rebuilt from current sources is not bound to
anything.

This is measurement identity, not harness sophistication. A digest is a
measurement, and a measurement is meaningless without its instrument; an
instrument that has since been repaired cannot reproduce what it measured before.
The failure is self-inflicted and recurring in a specific way: ACTING ON A
ROUND'S FINDINGS IS WHAT BREAKS THE ROUND'S OWN EVIDENCE. A round finds the
harness leaked, the harness is fixed, and the round's digest stops reproducing. A
round finds a section under-defined, the section is repaired, and the kit built
from it stops reproducing.

A ledger that fails in those situations punishes exactly the behaviour it exists
to provoke, and the only way to keep it green would be to leave findings unfixed.
Three separate instruments needed this before it was stated as a rule rather than
applied as three repairs.

**IMPL-FROZEN-APPARATUS.** Once a round is dispatched, three things are FROZEN
until it returns and its outcome is recorded: the SURFACE that was supplied, the
INSTRUMENT that produced it, and the PRE-REGISTRATION stating what was expected
and how the result would be read. None may be edited, reinterpreted, or improved
in flight, including in ways that would make the round easier to pass or its
findings easier to accept.

This is the append-only discipline the journal already has, applied to
experimental evidence — and for the same reason. A record that can be revised
after the fact by the party it judges is not evidence about that party.

The prohibition covers changes that look like improvements. Repairing the section
under test mid-round makes the subject's findings describe a document that no
longer exists. Sharpening a prediction after seeing partial results converts a
falsifiable claim into a narration. Fixing the harness invalidates the surface
digest. Each is locally correct and destroys the round.

The corollary is that a limitation, once declared, STAYS declared. It is not
removed from the record when the tooling later improves; a subsequent round
records that it was satisfied, and the earlier round keeps saying it was not.

**IMPL-REPRODUCIBILITY-CLASS.** A claim MUST state which of two classes it
belongs to, because they are not the same guarantee and reporting them together
overstates the weaker one:

- **FULLY REPRODUCIBLE** — the surface, the tool that produced it, and the record
  that verifies it are all pinned to commits, so the measurement can be
  re-derived exactly by anyone holding the repository. Nothing outside the
  repository is depended on.
- **ENVIRONMENT-CONSTRAINED** — reproduction additionally depends on properties
  of the implementer's execution environment that the repository cannot pin or
  verify, IMPL-ISOLATED-SESSION being the current instance.

The distinction is not a caveat about rigour; it is a boundary on what the
experiment can measure at all. An environment-constrained claim may be perfectly
executed and still cannot be re-run identically by a third party, because the
thing that differs is not in the artifact.

Where a required property cannot be enforced by the available tooling, a round
MUST declare that BEFORE it runs. A limitation discovered afterwards is
indistinguishable, in the record, from one discovered because the result was
disappointing.

**IMPL-UNOBSERVABLE-PINNED.** A specification MAY normatively fix a distinction
that no current implementation or vector can observe, and such a rule is NOT
under-witnessed merely because nothing distinguishes its alternatives today.

THREE STATES, which a single word "unwitnessed" used to collapse into one. They
have different remedies, and confusing them either manufactures defects or hides
them:

| state | the specification | a witness | remedy |
|---|---|---|---|
| UNWITNESSED | makes an observable claim | none constrains it | write the vector |
| UNOBSERVABLE BUT PINNED | chooses one alternative | none CAN distinguish them under any currently permitted implementation | say so; the choice is the point |
| UNDEFINED | chooses nothing | nothing to witness | define the object |

Round 7 found several instances of UNDEFINED and, for a time, they were reported
as if they were UNWITNESSED — which suggested writing vectors for rules that did
not yet have meanings. A vector cannot witness an undefined object; it can only
freeze one implementer's guess about it.

**RES-PINNED-RECOVERABLE** (prerequisite). A pinned choice MUST be recoverable
from the protocol state. Otherwise it is not pinned — it is merely asserted.

This separates two things that both look like "unobservable" and are not the same:

- **CURRENTLY OBSERVATIONALLY EQUIVALENT** — every implementation the model
  permits today behaves identically under either alternative, and the value is
  still reconstructible from what the protocol records. Pinning it early is
  correct and is what stops divergence when the distinction becomes visible.
- **FUNDAMENTALLY UNREADABLE** — the chosen value cannot be reconstructed from
  protocol state at all. This is not a pinned choice. It is an UNDEFINED OBJECT
  wearing the vocabulary of a decision, and it must be repaired as one.

Only the first qualifies. The failure is easy to make from inside, because both
produce the same symptom — no vector distinguishes the alternatives — while only
one of them is a specification a third party can implement.

Such a rule MUST say which is the case. The present instance is §8.7.0's ORDER:
committed serialization and physical journal position coincide for a
single-writer append-only journal, so no fixture can separate them, and the
specification still has to pick one because a replicated or transactional
registry would separate them.

**IMPL-ISOLATED-SESSION.** An implementability claim MUST NOT be made from a run
whose execution environment carried project-specific normative knowledge outside
the exported surface. Where such knowledge was present, the run MUST disclose it
and identify every value it may have supplied.

An implementation is a function of `(surface, session)`, not of `surface` alone.
A clean archive establishes only that the archive is clean; if the implementer's
environment separately describes the system — project instructions, retained
memory, a prior conversation — then the measured quantity is the surface PLUS
that environment, and the claim overstates the surface by exactly the difference.

This contamination is strictly harder to detect than an artefact leak, because it
is INVISIBLE FROM THE EXPORTED BYTES. A preflight over the archive cannot see it
by construction, and no digest of the surface changes when it is present.

It was found by a run whose archive passed preflight and whose environment
carried a project description the subject had not chosen to load. The subject
disclosed it unprompted and identified the single value it supplied — a status
token the excerpt never publishes. That disclosure did not weaken the round's
finding; the finding WAS that the token is unspecified, and being able to supply
one from memory is precisely what would have made the round look more successful
than the text warranted.

**IMPL-IDENTITY-SUBJECT.** Every normative identity MUST explicitly identify its
SUBJECT — the object the identity is an identity OF. Binding the identity's
inputs is not sufficient and does not imply it.

The question a review must ask of any identity encoding is not "are all the
fields bound?" but "identity of WHAT?". Those come apart precisely when the
subject is implied by context that the encoding does not carry. §12.4 bound a
method and a multiset of members and never the artifact the evaluation was
about — so two entry points into one dependency component produced the same
identity, and every empty closure produced one identity for every possible
subject. Each field was correct; the encoding still did not say what it
identified.

The failure is invisible to field-level review because nothing is missing from
the list of inputs. It is only visible by asking what the resulting value is a
name for, and then checking that the encoding contains an answer.

> **Four questions for any conformance witness.** The SOURCE / SUBJECT / SURFACE
> triad above interrogates a normative IDENTITY. A witness needs its own, because
> a witness can be wrong in ways the specification is not:
>
> - **CLAIM** — which exact normative clause does this witness say it exercises,
>   and is that clause named in the PROSE? (§8.6's vectors cited a vocabulary
>   present only in the fixtures and the reference kernel.)
> - **CARRIER** — can its representation preserve every value needed to exercise
>   that clause? (A bignum witness carried as a JSON number cannot.)
> - **CAUSALITY** — does disabling the clause change the result? (This is what the
>   §10 mutation scorer measures, and it is the only one of the four that was
>   already enforced.)
> - **SELF-REPRODUCTION** — can the witness regenerate any bytes, digest,
>   signature or record it claims to contain?
>
> CAUSALITY alone is not sufficient and was long assumed to be. It answers whether
> a rule matters to the outcome; it cannot show that the fixture faithfully
> represents the input it says it does.

**IMPL-WITNESS-FAITHFUL.** A conformance witness MUST be able to produce what it
witnesses, and MUST NOT be carried in a form that defeats the rule it exists to
demonstrate.

A witness is not merely a test case; it is a claim about the specification, and
it can be wrong in ways the specification is not. Both failure modes were found
in one section:

- an envelope `canonical` record pairs a structured envelope with the octets it
  claims to encode to. For months the structured half carried the SIX keys of
  `oath-publish/1` while the octets carried `/2`'s seven — so no record could
  produce its own bytes, while §10.1 and the fixture manifest both asserted that
  they could;
- the record labelled "arbitrary precision, not a machine word" carried its
  revision as a JSON NUMBER. A float64 reader decodes `2^128 + 1` OFF BY ONE, so
  the witness for the unbounded-integer rule was defeated by its own carrier —
  and the file was internally inconsistent, since other records already used a
  string.

Neither is visible to the gates that compare committed fixtures against generated
ones: both halves were equally wrong, so nothing drifted. A witness has to be
checked against the property it claims, not only against its producer.

**IMPL-NORMATIVE-SOURCE.** Every byte contributing to a normative identity MUST
have a normative source. A constant, sentinel, separator or key that exists only
in an implementation makes every value containing it irreproducible from the
specification — and to a blind reader that is indistinguishable from a
specification which cannot be implemented at all.

This is stated as a general obligation because the defect is not tied to any one
value. A reference kernel encoded an undeterminable publication as the literal
`unpublished`; had the constant been `none` or `missing` the defect would have
been identical. What went wrong was that the IMPLEMENTATION KNEW SOMETHING THE
SPECIFICATION DID NOT, and an identity encoding is the one place where that is
fatal rather than untidy, because every byte of it is load-bearing for a value
others must reproduce exactly.

### 13.1a The normative surface

A specification is not only prose. Real protocols incorporate grammars,
registries, Unicode tables, opcode assignments — data that determines behaviour
and lives outside the sentences. This document does the same, and the
implementability measurement is only well defined once the boundary is explicit.

Material divides into three kinds, and conflating any two makes a verdict
meaningless:

| kind | what it is | may an implementation consume it? |
|---|---|---|
| NORMATIVE PROSE | algorithms, semantics, identities, obligations | yes — it is the specification |
| NORMATIVE DATA | versioned artifacts this document incorporates BY REFERENCE, whose schema and interpretation it defines | yes — it is part of the specification |
| CONFORMANCE WITNESSES | fixtures that demonstrate the rules, and tooling that measures coverage | for SELF-CHECKING only; never as a source of rules |

**IMPL-NORMATIVE-DATA.** An implementation MAY consume any artifact whose SCHEMA
and INTERPRETATION are normatively defined by this document. It MUST NOT infer
either. An artifact incorporated by reference without its schema specified is not
normative data — it is a fixture the prose gestures at, and reading rules out of
it is inference (§13.2).

**IMPL-SURFACE-DECLARED.** The normative surface is the union of this document
and the NORMATIVE DATA listed in §13.1b — not "whatever files a particular export
happened to contain". A `surface_digest` covering more than the declared surface
measures a different and easier experiment than the one being claimed.

> The distinction matters in the permissive direction. Round three's subject
> disclosed that a coverage-measuring SCRIPT, supplied in its dispatch surface,
> carried a label that nudged one of its pre-boundary assumptions. That script is
> neither prose nor data — it is our measurement apparatus, describing our
> intent — so a rule taken from it is inferred, not derived, and its presence
> made the surface easier than the specification. Witnesses and tooling are for
> checking an implementation, never for building one.

### 13.1b Normative inputs

The following artifacts form part of this specification. Each is incorporated by
reference, versioned, and has its schema and interpretation defined in the
section named:

| artifact | schema and interpretation | version binding |
|---|---|---|
| `fixtures/license/model.json` | §12.3 (LICENSE-MODEL-SCHEMA, LICENSE-MODEL-PUBLISHED) | `model` member; bound into every evaluation identity as `model=` and `model-digest=` (§12.4) |
| **RFC 8032** (Ed25519) | §8.6.4a selects the variant and the rejection rules; RFC 8032 supplies the curve parameters, point encoding, hash and key expansion | cited by RFC number; §8.6.4a pins the choices RFC 8032 leaves open |

RFC 8032 is an EXTERNAL normative reference: incorporated by reference like any
other normative data, but retrieved from its publisher rather than from this
distribution, so IMPL-DATA-RETRIEVABLE is satisfied by the RFC number.

> It was not declared at all until an independent implementation of §8.6 pointed
> out that a reader holding only this document cannot obtain `p`, `L`, `d`, the
> base point, the hash, or the clamping rule — and therefore cannot implement
> §8.6.4a. Every one of those is a byte contributing to a normative identity with
> no source in the declared surface (IMPL-NORMATIVE-SOURCE), and the omission was
> invisible because a competent implementer supplies them from habit. That is
> exactly the substitution §13.2 classes as INFERENCE: a correct implementation
> built from undocumented knowledge is still evidence the specification was
> insufficient.

The following are CONFORMANCE WITNESSES ONLY. They demonstrate the rules and a
candidate implementation may check itself against them, but they define nothing
and no rule may be read out of them:

| artifact | witnesses |
|---|---|
| `fixtures/license/vectors.jsonl` | §12 |
| `fixtures/envelope/vectors.jsonl` | §8.6 |
| `fixtures/campaign/vectors.txt` | §11 |
| `fixtures/canonical/`, `fixtures/encoding/` | §1 identity encoding |
| `fixtures/verify/`, `fixtures/analyses/`, `fixtures/prove/`, `fixtures/gate/` | §2–§7 verdicts |
| `scripts/rule-matrix.py` | coverage measurement; NOT part of any dispatch surface |

**IMPL-DATA-RETRIEVABLE.** Normative data MUST be retrievable from its declared
path in this specification's distribution, and a consumer MUST verify the bytes
it retrieves against the content digest the identity binds.

Publication alone is not enough for an identity to be auditable by a third party.
An evaluation naming `model=spdx-lattice/1` and `model-digest=a3aa…` gives an
outside auditor a content digest and, absent this rule, no stated way to resolve
it — so the reproducibility §11 exists to establish would hold only for readers
who already have the repository.

**IMPL-DATA-SUPERSEDED.** Normative data is versioned, never edited in place. A
future `model-v2.json` does not invalidate earlier evaluations: their identities
name the artifact they consumed, by version and by content digest, so old
evaluations stay reproducible while new ones bind the new artifact. This is
§11's campaign-identity discipline applied to the specification's own inputs.

### 13.2 The three verdicts

| verdict | meaning |
|---|---|
| `PASS` | every rule and encoding detail was stated in the normative text or supplied as a NAMED input the text points at. Nothing was inferred, guessed, or confirmed against a fixture. |
| `PASS-WITH-INFERENCE` | a working implementation was produced, but at least one rule had to be inferred rather than derived. The implementation is evidence; the specification is not yet sufficient. |
| `FAIL` | no working implementation was produced from the supplied surface. |

**IMPL-INFERENCE-DEFINED.** A rule is INFERRED, not derived, if the implementer:

- worked it out by observing what a vector expected;
- guessed a value, encoding detail, separator, ordering, or default and then
  confirmed it against a fixture;
- resolved an ambiguity by selecting whichever reading made a vector pass; or
- supplied it from prior knowledge of this project rather than from the text.

The distinction is about the ORDER OF DISCOVERY, not the final correctness. A
rule that happens to be right is still inferred if the document did not
determine it — and it is exactly those rules that a second independent
implementer may get differently.

**IMPL-PRE-REGISTERED.** A run's HYPOTHESIS SHOULD be recorded before dispatch,
and MUST NOT be edited after the outcome is known. A hypothesis written
afterwards is a narration of the result: it converts every round into a
confirmation and makes the measurement unfalsifiable, which is the one failure
this ledger cannot detect from its own contents.

**IMPL-CONSTRUCTIVE-FAILURE.** A run MAY be annotated CONSTRUCTIVE when the
implementer, on failing to reproduce a published fixture, declined to infer the
missing rule and reported the fixture as irreproducible instead of adjusting the
implementation until it matched.

The annotation records something a score cannot. A suite passed by adaptation
demonstrates that the fixtures are reachable; a suite deliberately LEFT FAILING,
with the gap reported, demonstrates that the specification was insufficient AND
that the measurement resisted teaching-to-the-test. The second is stronger
evidence, and a lower vector count carrying it is a better result than a higher
one without.

**IMPL-VERDICT-BY-SUBJECT.** The verdict is reported by the implementer, not by
the specification's authors. An author is the one party who cannot measure this,
having the answers already. A claim of `PASS` MUST carry the implementer's own
statement that nothing was inferred; absent that statement the verdict is
`PASS-WITH-INFERENCE` at best.

### 13.3 What a claim binds

**IMPL-SURFACE-BOUND.** "This specification is implementable" is never a
statement about a document; it is a statement about an exact set of supplied
bytes. A claim MUST therefore record:

| field | why |
|---|---|
| `source` | the pinned commit the surface was exported from |
| `surface_digest` | SHA-256 over the manifest of every supplied file and its digest |
| `verdict` | §13.2 |
| `inferred` | each rule that had to be inferred, or an empty list |
| `contamination` | anything the subject saw beyond the supplied surface, including prior knowledge of the project |

The commit alone is NOT sufficient to identify a surface: the same commit can be
exported with a different file selection, and adding one file can turn
`PASS-WITH-INFERENCE` into `PASS` without the document changing at all. The
surface digest is recomputable from the source, so this half of every claim is
checkable rather than asserted.

**IMPL-CONTAMINATION-RECORDED.** Anything the subject saw beyond the supplied
surface MUST be recorded, including partial exposure and prior familiarity with
the project, and MUST NOT be discounted on the grounds that it probably did not
matter. Whether it mattered is a judgement; whether it happened is a fact, and
only the second belongs in the record.

### 13.4 Scope

**IMPL-VERDICT-SCOPED.** A verdict applies to the SECTIONS a run actually
attempted, on the SURFACE it was given, and MUST be reported in those terms. The
form is:

> §12, on normative surface `e0ee5df5…`, was independently implemented without
> inference.

and NEVER "the specification is independently implementable". The narrower
sentence is the stronger one, because it is exactly true: it names what was
attempted and the bytes it was attempted against, both of which a reader can
check. The broad sentence is unfalsifiable and, on any document with sections
nobody has attempted, false.

This constraint was written while a run was IN FLIGHT and its outcome unknown.
That timing is the point — deciding how a `PASS` may be phrased after obtaining
one is indistinguishable from shaping the claim to fit the result, which is the
same defect IMPL-PRE-REGISTERED addresses one paragraph up. Implementability is not a property the specification has
uniformly: one section can be fully reconstructible while its neighbour is
determined only by fixtures.

> Why this section exists. Three consecutive blind runs of this specification
> produced the same shape of result: the implementation passed, the vectors
> passed, and the implementer independently reported that it could not have
> built the thing honestly from the prose. The defects that surfaced were almost
> never algorithmic — they were fields participating in identity with no stated
> meaning, encodings contradicting rules the same section had already stated, and
> a lattice that determined every verdict while living outside the normative
> surface entirely. None of those is visible to a conformance suite, because a
> conformance suite is written by someone who already knows the answers.

## 14. The handler protocol: the Request model (normative, #122)

### 14.0 What this section binds, and what it does not

Sections 1–13 bind a KERNEL: identity, semantics, verdicts, the journal, and the
evidence layers built on them. This section binds a BACKEND — an implementation
that compiles an Oath handler entry point and connects it to a network. The
distinction is load-bearing and is stated here rather than inferred:

- A kernel that does not compile programs conforms to this specification without
  implementing §14 at all. §10's five conformance points do not reach it.
- A backend that compiles `(-> Request Response)` MUST implement §14. Two
  backends satisfying it produce the SAME `Request` value from the same HTTP
  request, and that is the entire obligation.

A §14 claim is scoped exactly as §13.4 requires of any other: name the backend
and the surface, never "this implementation is conformant" unqualified.

### 14.1 The governing principle

The prior rule was *normalize as little as possible*. It is withdrawn. It read as
a prohibition on all transformation, and under it the reference backend both
canonicalized header-name case and imposed an ordering — because the rule
described a disposition rather than an obligation, and because part of what it
demanded is not obtainable from the transport at all.

**HDR-PRINCIPLE.** A backend PRESERVES every semantically meaningful distinction
the transport supplies, and CANONICALIZES distinctions the transport does not
define or explicitly declares insignificant.

Both halves are obligations. Preserving is not permission to pass through
whatever a host library happens to expose, and canonicalizing is not permission
to discard information the transport carried.

The classification is a matter of HTTP, not of taste:

| distinction | HTTP's position | §14 |
|---|---|---|
| repeated field lines with one name | significant; order is meaningful (RFC 9110 §5.3) | PRESERVE |
| the field value's octets | significant | PRESERVE |
| method | case-sensitive (RFC 9110 §9.1) | PRESERVE |
| request target, with query | significant, and signed schemes rely on the raw form | PRESERVE |
| body octets | significant | PRESERVE |
| field-name case | insignificant (RFC 9110 §5.1); HTTP/2 and HTTP/3 mandate lowercase on the wire (RFC 9113 §8.2.1), so the sender's case does not survive the transport | CANONICALIZE |
| order ACROSS distinct field names | not significant (RFC 9110 §5.3) | CANONICALIZE |

Field-name case is the decisive row. A rule that preserved it would be
unsatisfiable over HTTP/2, where the case never arrives — so making it normative
would make backend agreement depend on which transport a client negotiated.

### 14.2 The Request value

A backend constructs `(Req method path headers body received-at)` with the
following obligations. The type is the one bound to the name `Request` in the
store; §14 constrains its CONTENTS.

**REQ-HEADER-NAMES-LOWERCASE.** Field names in `headers` are lowercase. The
mapping is ASCII-only: the 26 octets `A`–`Z` (0x41–0x5A) map to `a`–`z`
(0x61–0x7A); every other octet is unchanged. This is NOT Unicode case folding —
a field name is an HTTP token and is ASCII by construction, and applying a
locale- or Unicode-aware transformation would make the result depend on a table
outside this specification.

**REQ-HEADER-ORDER-LEXICOGRAPHIC.** Entries are ordered ascending by the
lowercase field name, compared as a sequence of unsigned octets. Shorter names
that are a prefix of a longer one sort first.

**REQ-HEADER-REPEATS-PRESERVED.** Entries sharing a field name appear in the
order the transport delivered them, and the sort of REQ-HEADER-ORDER-LEXICOGRAPHIC
is stable with respect to that order. A backend MUST NOT reorder, deduplicate, or
drop repeats.

**REQ-HEADER-VALUES-NOT-JOINED.** Two field lines with the same name produce TWO
entries. A backend MUST NOT combine them into one comma-separated value, even
where RFC 9110 §5.3 would permit a recipient to do so. Combining is irreversible
and destroys a repetition structure the adapter still holds; a handler verifying
a signature or diagnosing a duplicated header can then no longer see what
arrived.

**REQ-HEADER-VALUE-OCTETS.** A field value crosses unchanged. The optional
whitespace surrounding a field value is transport framing and is not part of the
value (RFC 9110 §5.5); removing it is parsing, not normalization. A backend MUST
NOT otherwise trim, decode, re-encode, or case-map a value, and MUST NOT deliver
obsolete line folding as an embedded newline.

**REQ-HOST-IS-A-HEADER.** The request authority appears in `headers` under the
name `host`, ordered and compared like any other entry. Host libraries routinely
lift it out of the header collection into a dedicated field — Go's `net/http`
promotes it to `Request.Host` and REMOVES it from the header map — and a backend
that passes its library's collection through unexamined therefore drops it. It is
a header field, it is mandatory in HTTP/1.1, and a handler inspecting the
authority must not have to know which backend compiled it.

This also covers the HTTP/2 and HTTP/3 spelling: the authority arrives as the
`:authority` pseudo-header rather than as a field line, and it appears here as
`host` regardless. Pseudo-headers are otherwise NOT header entries — `:method`,
`:path` and `:scheme` are the transport's encoding of information this section
already places in `method` and `path`, and repeating them as entries would make
the header list depend on the HTTP version. When the transport supplies no
authority at all, no `host` entry appears; a backend MUST NOT synthesize one.

**REQ-FRAMING-FIELDS-EXCLUDED.** These names do NOT appear in `headers`:

    connection            keep-alive            proxy-authenticate
    proxy-authorization   te                    trailer
    transfer-encoding     upgrade               content-length

They describe how this message was TRANSMITTED, not what was requested. Most are
connection-specific and hop-by-hop (RFC 9110 §7.6.1) — meaningful only between
one pair of endpoints, and meaningless to a handler that receives an already
reassembled request. By the time `body` is a list of octets the framing has been
consumed, so `transfer-encoding: chunked` describes an encoding that no longer
exists in the value.

`content-length` is excluded for a sharper reason: `body` is the authority on how
many octets arrived, and a handler that trusted a header disagreeing with it
would be reproducing the disagreement that request smuggling is built on. There
is nothing a handler can soundly do with the field that inspecting `body` does
not do better.

Enumerating these is not tidiness — it is the difference between a model and a
coincidence. Host libraries lift exactly these fields out of their header
collections at unpredictable points (Go's `net/http` deletes `Transfer-Encoding`
and `Trailer` into dedicated struct fields, and deduplicates repeated identical
`Content-Length` lines), so a backend that simply forwarded its library's
collection would agree with another backend only by luck. Excluding them by NAME
means every backend produces the same list whether or not its library kept them.

**REQ-METHOD-VERBATIM.** `method` is the request method as received, unchanged
and case-sensitive.

**REQ-PATH-IS-RAW.** `path` is the request target as received, including the
query string when one is present, separated by `?`. It is NOT percent-decoded,
NOT dot-segment-normalized, and NOT otherwise rewritten. When no query string is
present the `?` is absent.

**REQ-BODY-IS-OCTETS.** `body` is `(List Int)`, one element per octet of the
request body, each in `0..255`, in order. It is not decoded as text. A body is
signed as octets and may not be valid UTF-8; routing it through a codepoint type
would place the corruption in the type rather than in the adapter.

**REQ-TIME-IS-DATA.** `received-at` is the backend's observation of when the
request was received, in seconds since the Unix epoch. It is an OBSERVATION in
the sense of the journal's four categories: authoritative about the observer and
checkable by nobody. It is a field rather than an ambient clock so that a handler
stays a deterministic function of its argument.

### 14.3 Worked vector

A request arriving as:

```
POST /hook?attempt=2 HTTP/1.1
X-GitHub-Event: push
Accept: */*
X-Example: first
X-Example: second
Content-Type: application/json
```

produces `method = "POST"`, `path = "/hook?attempt=2"`, and exactly this
`headers` list:

```
[ ("accept",         "*/*")
, ("content-type",   "application/json")
, ("x-example",      "first")
, ("x-example",      "second")
, ("x-github-event", "push")
]
```

Three obligations are jointly witnessed here and each fails a distinguishable
way: `x-github-event` witnesses REQ-HEADER-NAMES-LOWERCASE; the ordering
`accept` < `content-type` < `x-example` < `x-github-event` witnesses
REQ-HEADER-ORDER-LEXICOGRAPHIC and is not the arrival order; and `first`
preceding `second` under one repeated name witnesses both
REQ-HEADER-REPEATS-PRESERVED and REQ-HEADER-VALUES-NOT-JOINED, which a
comma-joining backend would collapse to a single `x-example` entry.

### 14.4 What this makes true, and what it does not

A handler may look a field up by its lowercase name and get the answer on every
conformant backend and every HTTP version. Case-insensitive lookup is therefore
unnecessary rather than merely inconvenient, and `header-first`'s case-sensitive
comparison becomes correct by construction rather than a hazard the author must
remember.

This does NOT make a handler portable in any larger sense. It fixes the value a
backend constructs; it says nothing about TLS termination, proxies that add or
rewrite headers before the backend sees them, request smuggling, or limits on
header count and size. A rewrite performed upstream of the backend is invisible
to this section by construction — §14 binds the adapter, and the adapter's input
is whatever reached it.

**The RESPONSE is deliberately not constrained.** A `Response`'s header names are
the handler's own choice, HTTP compares them case-insensitively, and a backend
MAY transmit them in whatever case its library produces. The asymmetry is not an
omission: inbound needs one canonical form because agreement BETWEEN BACKENDS
about the same request depends on it, and nothing analogous is at stake on the
way out. A handler that needs an exact outbound spelling is depending on
something HTTP does not guarantee, from any implementation.
