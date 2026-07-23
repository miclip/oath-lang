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
| `i64` | 8 bytes, big-endian two's complement |
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

`Term` — one tag byte, then fields:

| tag | kind | fields |
|---|---|---|
| 0x10 | var | `u32` de Bruijn index (0 = innermost) |
| 0x11 | int | `i64` |
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

`Def` — one tag byte after the magic:

| tag | kind | fields |
|---|---|---|
| 0x01 | data | `u32` tyvars, `u32` ctor count, then per ctor: `list<Ty>` fields |
| 0x02 | func | `u32` tyvars, Ty declared type, Term body, `u32` prop count, then per prop: `list<Ty>` binders, Term body |

Decoders MUST be strict: unknown tags, malformed bool bytes, record names
not strictly ascending, and trailing bytes are all rejected — so decode
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
  or not`. There are NO string primitives: strings are the ordinary `Str`
  datatype (§3), and every string operation is a definition.

### 1.4 Surface syntax and elaboration

The surface syntax is not identity, but the examples corpus is a conformance
input. A conforming surface elaborator MUST therefore match these rules:

- Whitespace is space, tab, carriage return, newline. `;` starts a comment
  through the next newline. Delimiters are `()`, `[]`, and `{}`.
- Atoms are decimal int64 literals, string literals, or symbols. A token that
  parses as int64 is an integer; otherwise it is a symbol.
- String literals are delimited by `"`. Supported escapes are `\n`, `\t`,
  `\"`, and `\\`; any other backslash escape is rejected. Newlines inside
  strings are accepted and count for later line numbers.
- Lists produce `list`, square brackets `brack`, and braces `brace`.
- Top-level forms are `(data Name [tyvars] ctor...)` and `(defn name [tyvars]
  [(param ty) ...] ret body prop...)`.
- Type syntax: `Int`, `Bool`, `Str`, type variables, data names, `(Data arg
  ...)`, right-associated `(-> a b c)`, and record types `{field Ty ...}`.
  Record fields are sorted ascending by raw symbol bytes during elaboration;
  duplicate fields are rejected.
- Term syntax: ints, strings, `true`, `false`, variables, `(fn [(x ty) ...]
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
  scalar value (a decimal int64) of the literal's `i`-th codepoint in order;
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
reference; empty lists (a bare zero count); and a record whose encoding
witnesses the name-then-value pair layout in ascending name order.

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
- PRIMITIVES with fixed operand types (`+ - * / % neg < <= and or not`) CHECK
  each operand against that fixed type. `==` is polymorphic in
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
- Primitive arities and types are fixed: arithmetic and comparisons are over
  `Int`, `and`/`or`/`not` over `Bool`, and `==` over equal first-order types
  only. There are no string primitives.

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

Runtime values are erased: `int`, `bool`, `closure`, `data`, `record`,
and generated `native` functions. Closures store an environment, lambda term,
and enclosing self hash. Native functions are used only by deterministic
generation: identity, affine, constant, and finite table.

Value printing is normative for counterexamples and conformance:

- Int and Bool print as decimal and `true`/`false`.
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
  rle-encode goldens arrives through non-total `rle-decode`'s body). Additionally, a property already
  refuted by deterministic testing (§4) is never recorded as proven even if the
  solver reports it valid — the concrete counterexample governs.
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
  invalid has no valid negative verdict, and the kernel MUST invalidate
  the run there instead of recording. (Empirical origin of the
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
(`accepted`|`falsified`|`rejected`|`blocked` (repoint refused by store policy; object stored, name unchanged)), `hash`, `prev` (on repoint), `error`,
`guarantee`, `termination`, `context`, `chain`. Every submission attempt MUST
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
```

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
the stored object hash; `prev` is the previous hash for the submitted name, or
omitted when the name was new or already pointed at the same hash.

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
  prove/outcomes.json       solver version, outcome, method, detail
  api/*.txt                 stable CLI/MCP text outputs
```

No second implementation is trusted until it passes the fixtures without
consulting the Go source.
