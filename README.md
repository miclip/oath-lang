# Oath

An experiment: what would a programming language look like if it were designed
**only for AI authors** — no human ergonomics, no files, no style, just
verifiability and locality?

Oath is the v0 kernel of that idea, ~2,000 lines of dependency-free Go.
Definitions are content-addressed (identity = SHA-256 of the canonical AST),
carry machine-checkable properties as part of their signature, and live in an
immutable object database instead of source files. Names are metadata. The
kernel refuses ill-typed code at the gate, runs every property with
deterministic inputs before a name is trusted, and records an **honest
guarantee level** on every definition:

```
asserted  →  tested (N cases)  →  PROVEN (all inputs, Z3)
                      ↘  FALSIFIED (with counterexample)
```

`proven` is real: `oath prove <name>` translates properties to SMT-LIB and
asks Z3 (`brew install z3`) to hold them for *all* inputs — including
**recursive functions, by induction**: datatypes become SMT algebraic
datatypes, recursive functions become quantified defining equations, matches
become tester/selector chains, records become single-constructor datatypes,
and function values are array-encoded (so higher-order properties quantify
over *all functions* and capability properties over *all worlds*). Structural
and lexicographic induction reach recursion that shrinks a datatype;
**recursion induction** reaches functions that recurse on an integer COUNTER
(`replicate`, `range`, `fib`, even a counter inside a datatype field), proving
their measure laws like `length (replicate n x) == n`. Proven properties become
a **lemma library**: they are asserted as axioms in later proofs, composing
bottom-up through the hash graph like every other verdict. 99 definitions are
fully proven (299 properties proven overall), including `reverse (reverse xs) == xs` (via the append laws
and its own antidistribution lemma), insertion sort's complete correctness —
`output-is-sorted`, `preserves-counts` (the permutation oath), `idempotent`,
and `reverse-invariant`, the last two through a four-lemma plan (insert
commutativity, the sorted-head no-op, snoc-is-insert, and the sorted-fixpoint
theorem `is-sorted xs ⟹ sort xs == xs`) — `map.preserves-length` with a
quantified higher-order induction hypothesis, and `greet`'s capability
properties for every possible network.

`examples/undertested.oath` is the exhibit for why the rungs differ: its
property passes all 200 test cases and is refuted by Z3 at x = -401, outside
anything the generator draws. Z3 reasons over unbounded integers and the
evaluator matches it — `Int` is arbitrary precision, so a proof carries no
overflow caveat. Integer division stays outside the fragment on purpose
(kernel truncates, SMT-LIB is Euclidean — a "proof" would certify the wrong
theorem); rational division does not — `Rat` is exact ℚ over the `Real` sort,
so `(a/b)*b == a` is proven.

Two more dimensions ride alongside: **termination** (a structural checker
proves totality where recursion visibly descends, including lexicographic
descent — `merge` alternates which argument shrinks and is still `total` — and
a Z3-verified **ranking function** proves it for integer-counter recursion,
where the counter is not a shrinking datatype: `replicate` on `n → n-1`, `range`
on `lo → lo+1` bounded by `hi`, the count inside `rle-expand`'s `Run`; genuinely
non-terminating or unanalyzable recursion stays honestly `termination unproven`
and fuel-bounded) and **spec strength** (mutation-tested: do the properties
notice when the body changes?).

See [DESIGN.md](DESIGN.md) for the full rationale and roadmap,
[docs/SPEC.md](docs/SPEC.md) for the normative kernel specification
(canonical encoding, exact hash bytes, semantics, generation, proof
boundaries — the conformance target for any second implementation), and
[docs/effects.md](docs/effects.md) for the capability model.

## Quickstart

```sh
cd oath && go build -o oath . && cd ..

./oath/oath put examples/list.oath        # elaborate → typecheck → store → verify
./oath/oath ls                            # names, hashes, guarantees
./oath/oath get reverse                   # human projection of a definition
./oath/oath eval '(reverse [Int] (Cons [Int] 1 (Cons [Int] 2 (Nil [Int]))))'
./oath/oath put examples/bad_reverse.oath # watch a wrong definition get FALSIFIED
```

The codebase database lives in `./codebase` (override with `OATH_STORE`).

## What the demo shows

- `put examples/list.oath` — five list functions, eight properties, all
  verified with 200 deterministic cases each. Re-running `put` produces
  byte-identical hashes: the store is idempotent.
- `put examples/bad_reverse.oath` — a `reverse` that returns its input
  unchanged. Its `involution` property **passes** (a lesson about weak specs);
  the append law falsifies it and the kernel prints a counterexample and
  records the definition as `FALSIFIED`.
- Renaming every variable in a definition produces the **same hash** —
  binders are de Bruijn indices, so code is alpha-canonical by construction
  and formatting/naming diffs cannot exist.

## Surface syntax

The s-expression syntax is an *input format*, not the language — it elaborates
to the canonical AST and is thrown away. `oath get` prints a projection back
out for human auditors.

```lisp
(data List [a]
  (Nil)
  (Cons a (List a)))

(defn length [a] [(xs (List a))] Int
  (match xs
    ((Nil) 0)
    ((Cons h t) (+ 1 (length [a] t))))
  (prop non-negative [(xs (List Int))]
    (<= 0 (length [Int] xs))))
```

Binders are explicitly annotated; type arguments may be omitted and are inferred
(`length t`). Checking is bidirectional local synthesis — no full inference and
no unification of two unknowns — small enough to audit.

Three numeric primitives, chosen so the solver is always strong. `Int` is ℤ
(arbitrary precision — no overflow); `Rat` is ℚ (arbitrary-precision exact
rationals); `Float` is IEEE-754 binary64 (opt in with an `f` suffix: `0.1f`).
A bare decimal like `0.1` is a `Rat`, so `0.1 + 0.2` is exactly `3/10` — no
rounding — and `examples/rat.oath` proves rational associativity, distributivity,
and exact division-inverse `(a/b)*b == a`. The same sum as `Float` is the honest
`0.1f + 0.2f = 0.30000000000000004f`. Both theories (Z3's reals and floats) are
complete, so both prove: `examples/float.oath` proves `x*1.0 == x` and `x+x ==
x*2` for *every* float (including NaN/±inf/±0), while `0.1f + 0.2f == 0.3f` is
correctly *falsified*. (`Float` identity is bitwise — `NaN == NaN`, `+0.0 ≠
-0.0`; IEEE `fp.eq` is a separate primitive. See `docs/floats.md`.)

The three interconvert explicitly — `to-rat`, `to-float`, `floor` (overloaded by
source; `Float → Rat`/`Int` errors on NaN/inf, like division by zero). The exact
directions prove: `examples/convert.oath` proves that embedding an `Int` in ℚ and
flooring back is the identity, that `floor x ≤ x`, and that the exact rational
`1/10` rounds to precisely the `0.1f` float literal — the tower meets where it
should.

Strings are the ordinary `Str` datatype (a codepoint sequence, not a primitive —
see `docs/structural-strings.md`); with structural records (`examples/records.oath`):

```lisp
(defn full-name [] [(p {first Str last Str})] Str
  (str-append (str-append (. p first) " ") (. p last))
  (prop starts-from-parts [(a Str) (b Str)]
    (== (full-name {first a last b}) (str-append (str-append a " ") b))))
```

Record field names are part of the type (and the hash); field *order* is
canonicalized away — `{last "b" first "a"}` and `{first "a" last "b"}` are
the same term.

## The agent interface (phase 3, v0)

Three verbs turn the store into what an AI author actually consumes —
queries and transactions instead of files:

```sh
./oath/oath context sort append --budget 500   # spec-only slice: signatures, props,
                                               # guarantees — never bodies; anything
                                               # omitted for budget is named explicitly
./oath/oath put --json examples/sort.oath      # machine-readable verdicts: accepted /
                                               # rejected / falsified + counterexamples
./oath/oath dependents append                  # reverse dependency query
./oath/oath mutate length                      # spec strength: do the properties
                                               # notice mutations of the body?
./oath/oath cross sabs sumsq                   # N-version: run each spec against
                                               # the other's body — AGREE / DISAGREE
```

`mutate` is the answer to "who verifies the specs?" — survivors are printed
with their bodies, and the killed/total score sits next to the guarantee.
`length`'s original spec scored 1/5; two added anchor properties took it to 5/5.

`cross` is the answer to "who verifies the spec is of the *right* function?"
Mutation kills a weak spec; it is blind to a spec tightly written around the
wrong function (sum-of-squares graded as sum-of-absolute-values passes every
axis). Cross-checking two independently-authored specs of one brief makes them
collide: their defining equations cannot both hold on one body, and the kernel
names the counterexample. Detection is mechanical; only adjudication is human.

`examples/sort.oath` was authored this way: written against the `context`
output for `List`/`length`/`append` — their specs, never their bodies — and
accepted with 12 properties verified, including the strong permutation oath
(`count x (sort xs) == count x xs`).

## Effects, and the audit journal

Effects use capability passing, not an effect system (rationale and roadmap
in [docs/effects.md](docs/effects.md)): a capability is a record of functions
passed as an ordinary parameter, so the signature is the authority audit and
purity is visible as the absence of capability parameters. Properties
quantify over generated simulated worlds — see `examples/service.oath`:

```lisp
(defn greet [] [(net {fetch (-> Str Str)}) (id Str)] Str
  (str-append "Hello, " (str-append ((. net fetch) id) "!"))
  (prop same-world-same-answer [(net {fetch (-> Str Str)}) (id Str)]
    (== (greet net id) (greet net id))))
```

The kernel also proves capability **confinement** where it can: a
higher-order parameter that is only exercised — never returned, stored, or
captured — is verdicted `confined` in metadata (`net: confined` on `greet`;
`net: ESCAPES` on `examples/leaky.oath`'s capability-hoarders), and verdicts
compose bottom-up through the dependency graph like totality does.

Every `put` attempt — accepted, falsified, or rejected — is retained in an
append-only journal (`oath log`) with principal attribution (`--author` /
`OATH_AUTHOR`), timestamp, and verifier version. Rejections store no object
but leave a permanent record.

## Publishing and importing: trust by reproduction

The registry layer needs no trusted server, because verdicts are
reproducible. `oath export sort` packs a definition's transitive closure —
canonical object bytes, naming metadata, publisher guarantees as clearly
UNVERIFIED hints — into a single file publishable on any dumb host.
`oath import <path|url>` refuses any byte that doesn't hash to its name,
strict-decodes, gate-checks in dependency order, **re-verifies every
function locally**, journals each admission with the bundle source, and
binds names through the ordinary repoint policy — foreign code obeys the
same rules as local code. Proofs are re-earned with `oath prove`, never
imported. A registry is just a directory of bundles; all trust lives in
the importer.

## Discovery: find proven code by what it does

Every other lookup is by *name*, and names are the one non-authoritative layer
(a label anyone can repoint). So discovery keys on **meaning** instead. The
realization is that *properties are content-addressed too*: a property is stored
as `(binders, body)` with the function as `self` and de Bruijn binders, so a
pure law like commutativity — `(== (self a b) (self b a))` — has one canonical
hash wherever it appears. "Which proven definitions satisfy this spec?" becomes a
hash lookup, not a search.

```
$ oath find rat-add                        # by example: who shares a law?
  · commutes [proven here]  #f230af55f94f
      rat-mul   (proven as "commutes")  ← proven on both: interchangeable

$ oath find --spec spec.oath               # by a fresh spec: who proved it?
      rat-add   (proven as "commutes")  ← a proven implementation of this spec

$ oath find --implies flipped.oath         # by PROOF: who provably satisfies it?
      rat-add   ← provably satisfies it (direct)
```

Four modes, all name-free: by example, by a spec you write (`self` is the sought
function), matched *up to operand types* (Int and Rat commutativity match), and —
because a property is portable — by **proof-implication**: append your spec to
each same-signature definition and prove it, so commutativity written
`(== (self b a) (self a b))` still finds `+` even though its AST differs. This is
the layer that makes the commons real: pull proven code by property, rebuild
nothing. Full rationale in [docs/discovery.md](docs/discovery.md).

## Compiling to executables

`oath build <name> [-o out]` compiles a definition's dependency closure to
a standalone native binary (stage 1 of the backend: emit Go, `go build`
does native codegen). Entry protocol: `main : (-> (List Str) Str)` — argv
in, stdout out. The provenance gate is the point:

```
$ oath build main-echo -o echo && ./echo a bb ccc
a bb ccc  [sorted by length]
$ oath build bad-reverse
error: bad-reverse is FALSIFIED (antidistributes-over-append) — refusing
to build an executable from a broken oath
```

Compiled programs shed fuel/depth bounds (those are verification
semantics); what they keep is provenance — an executable is a
proof-carrying artifact, or it isn't built. Differentially tested against
`oath eval`. Capability entry points (real IO wired once at the program
boundary) are the next rung.

## MCP server

`oath serve` speaks MCP over stdio, so any agent session can mount the
substrate as native tools — `context`, `put` (source text in, verdicts out),
`get`, `ls`, `find`/`find_spec`/`find_implies` (discovery by property), `eval`,
`verify`, `mutate`, `dependents`, `log`. The repo's
`.mcp.json` registers it for Claude Code automatically; other clients point
at `./oath/oath serve`.

Hosting model (git's, in three layers): the stdio server is local-first, one
store per project. The **team store is real** (`oath serve --http <addr>
--tokens <file>`, see [docs/teamstore.md](docs/teamstore.md)): the same MCP
tools over HTTP with authenticated principals — journal authorship derives
from the bearer token, spoofed author fields are ignored — and a repoint
policy (`<store>/policy.json`) that can require spec/body authorship
separation, proven termination, and spec-strength floors before a *name*
moves. Objects always store (content addressing); policy governs only what
names point at, so a blocked submission leaves the previous version live. A
public registry of shared verified definitions is the eventual third layer;
content addressing means no namespace wars — names are local, hashes are
universal.

## Layout

```
oath/          the kernel + CLI (Go, no dependencies)
  ast.go       the language: canonical AST, de Bruijn binders, defs & props
  canon.go     hashing (identity = SHA-256 of canonical encoding)
  store.go     content-addressed object DB + mutable name index
  check.go     the trusted typechecker (bidirectional synthesis; no unification of two unknowns)
  eval.go      fuel-bounded interpreter
  gen.go       deterministic test-input generation (seeded by def hash)
  verify.go    property runner + guarantee tracking
  surface.go   s-expr reader + elaborator (names → indices/hashes)
  pretty.go    projection printer for human auditors
  main.go      put / ls / get / verify / eval
examples/      demo modules
codebase/      the object store (created on first put)
```
