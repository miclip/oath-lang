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
  field)`, primitives, and named application `(name [tyargs] arg ...)`.
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

- zero values omitted: `false`, integer `0`, `var 0`, constructor index `0`,
  empty `tyargs`, and empty function `props`;
- always-present fields: `Def.tyvars`, `Prop.binders`, and `Prop.body`;
- strings containing `"`, `\`, newline, `<`, `>`, `&`, U+2028, and U+2029;
- records whose source field order differs from canonical order;
- data constructors with and without fields, and matches whose first arm binds
  zero fields.

A second implementation should first pass these byte fixtures, then the full
examples corpus.

## 2. Static semantics (the gate)

Typechecking is structural synthesis; there is no inference or unification.
Every binder is annotated; every reference to a generic definition or
constructor carries explicit type arguments matching the target's `tyvars`
count. `==` requires both operands the same type, which must not contain a
function type. `rec` types are legal only inside `data` definitions. Prop
binders must be concrete (no `var`/`rec`) and prop bodies must be `Bool`.
A definition failing any check MUST NOT be stored.

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
  `Int`, `and`/`or`/`not` over `Bool`, `++`/`str-len` over `Str`, and `==`
  over equal first-order types only.

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

### 3.2 Runtime values and printing

Runtime values are erased: `int`, `bool`, `str`, `closure`, `data`, `record`,
and generated `native` functions. Closures store an environment, lambda term,
and enclosing self hash. Native functions are used only by deterministic
generation: identity, affine, constant, and finite table.

Value printing is normative for counterexamples and conformance:

- Int and Bool print as decimal and `true`/`false`.
- Str prints using Go `strconv.Quote` behavior, promoted to normative.
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
- `structural` if there exists one parameter index `j` such that every
  recorded self-call has an argument at position `j` whose relation contains
  `j -> lt`;
- `unknown` otherwise.

Only refs in the body count as callee obligations; properties are not
production code and do not affect totality.

### 6.2 Confinement algorithm

Confinement is computed for each top-level parameter whose type contains a
function. First-order parameters receive an empty verdict.

For a parameter at surface position `i`, the variable has de Bruijn index
`nparams-1-i` in the stripped body. The checker walks the body with that target
index and an `inLam` flag initially false:

- A bare occurrence of the target variable is an escape.
- Direct application `(f ...)` of the target variable is allowed only when not
  under an inner lambda.
- Projection-and-application `((. f field) ...)` is allowed only when not under
  an inner lambda.
- Passing the whole parameter as an argument to `self` is allowed only at the
  same parameter position currently being checked.
- Passing the whole parameter as an argument to a referenced callee is allowed
  only when the callee metadata says that argument position is `confined`.
- Any occurrence under an inner lambda, stored in a constructor or record,
  returned as a value, let-bound and later used bare, or projected without
  application is an escape.
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
3. String literal to `""`, only when non-empty.
4. Integer literal to `old+1`, `old-1`, and `0`, skipping unchanged values.
5. If-branch swap.

Each mutant runs properties in property order with `mutantCases=60` and
`mutantFuel=500000`, seeded by the mutant hash using §4. The first property
that fails or errors kills the mutant. Survivors are rendered with the
projection printer.

## 7. Proof obligations (SMT boundary)

The provable fragment and its translation, normative for outcome
reproducibility (given the same solver):

- Sorts: `Int`, `Bool`, `Str`→`String`; monomorphic data instances as
  algebraic datatypes (names derived from metadata but NOT semantically
  significant — only structure is); records as single-constructor
  datatypes; function types as `(Array dom cod)` applied via `select`.
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
  needed its definition return `unknown`). Additionally, a property already
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

### 7.2 Calls, lemmas, and induction

- Calls must be fully applied. Partial application and over-application are
  outside the provable fragment.
- Non-recursive callees are inlined by beta-reducing through their top-level
  lambda spine after type substitution.
- Recursive callees are declared with `declare-fun`; their defining equation is
  asserted as a quantified axiom over the top-level parameters. The pattern is
  the full function application. This is part of the current proof fragment and
  must be treated as trusted proof-kernel behavior by a conforming
  implementation.
- `self` inside a property means the definition under proof and is translated
  the same way as a call to the definition's hash.
- The lemma queue starts with all dependencies of the definition under proof,
  then traverses transitive dependencies breadth-first in sorted hash order.
  For every function dependency, metadata `proven_props` indices in stored
  order become lemma axioms when their properties translate.
- Previously proven properties of the definition itself are also lemmas, tagged
  by property index. A property's own lemma is excluded while proving that
  property. A property proven earlier in the same `prove` run becomes a lemma
  for later properties.
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
(`accepted`|`falsified`|`rejected`), `hash`, `prev` (on repoint), `error`,
`guarantee`, `termination`, `chain`. Every submission attempt MUST be
journaled, including gate rejections (which store no object). The journal is
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
  objects/<hash>.json   compact canonical Def JSON
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
constructor names, property names, guarantee, mutation score, termination,
author, parameter names, confinement verdicts, and proven property indices.
Metadata may change without changing the object hash.

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
  canonical/*.json          raw canonical Def bytes
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
