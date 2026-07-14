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
**recursive functions, by structural induction**: datatypes become SMT
algebraic datatypes, recursive functions become quantified defining
equations, matches become tester/selector chains, records become
single-constructor datatypes, and function values are array-encoded (so
higher-order properties quantify over *all functions* and capability
properties over *all worlds*). Proven properties become a **lemma library**:
they are asserted as axioms in later proofs, composing bottom-up through
the hash graph like every other verdict. 29 functions / 97 properties are
fully proven (116 proven overall), including `reverse (reverse xs) == xs` (via the append laws
and its own antidistribution lemma), insertion sort's complete correctness —
`output-is-sorted`, `preserves-counts` (the permutation oath), `idempotent`,
and `reverse-invariant`, the last two through a four-lemma plan (insert
commutativity, the sorted-head no-op, snoc-is-insert, and the sorted-fixpoint
theorem `is-sorted xs ⟹ sort xs == xs`) — `map.preserves-length` with a
quantified higher-order induction hypothesis, and `greet`'s capability
properties for every possible network.

`examples/undertested.oath` is the exhibit for why the rungs differ: its
property passes all 200 test cases and is refuted by Z3 at x = -401, outside
anything the generator draws. The standing caveat on every proof: Z3 reasons
over unbounded integers; the evaluator uses int64. Division stays outside
the fragment on purpose (kernel truncates, SMT-LIB is Euclidean — a
"proof" would certify the wrong theorem).

Two more dimensions ride alongside: **termination** (a structural checker
proves totality where recursion visibly descends, including lexicographic
descent — `merge` alternates which argument shrinks and is still `total`;
everything else is honestly `termination unproven` and fuel-bounded) and
**spec strength** (mutation-tested: do the properties notice when the body
changes?).

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

Everything is explicitly annotated — type arguments included (`length [a] t`).
Annotations are cheap for a machine author, and they keep the kernel free of
inference: checking is pure structural synthesis, small enough to audit.

Strings and structural records are in (`examples/records.oath`):

```lisp
(defn full-name [] [(p {first Str last Str})] Str
  (++ (++ (. p first) " ") (. p last))
  (prop starts-from-parts [(a Str) (b Str)]
    (== (full-name {first a last b}) (++ (++ a " ") b))))
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
```

`mutate` is the answer to "who verifies the specs?" — survivors are printed
with their bodies, and the killed/total score sits next to the guarantee.
`length`'s original spec scored 1/5; two added anchor properties took it to 5/5.

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
  (++ "Hello, " (++ ((. net fetch) id) "!"))
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

## MCP server

`oath serve` speaks MCP over stdio, so any agent session can mount the
substrate as native tools — `context`, `put` (source text in, verdicts out),
`get`, `ls`, `eval`, `verify`, `mutate`, `dependents`, `log`. The repo's
`.mcp.json` registers it for Claude Code automatically; other clients point
at `./oath/oath serve`.

Hosting model (git's, in three layers): the stdio server is local-first, one
store per project. A team store is the same protocol over HTTP with real
principal auth — that's where policy (authorship separation, repoint gates)
becomes enforceable rather than self-reported. A public registry of shared
verified definitions is the eventual third layer; content addressing means
no namespace wars — names are local, hashes are universal.

## Layout

```
oath/          the kernel + CLI (Go, no dependencies)
  ast.go       the language: canonical AST, de Bruijn binders, defs & props
  canon.go     hashing (identity = SHA-256 of canonical encoding)
  store.go     content-addressed object DB + mutable name index
  check.go     the trusted typechecker (no inference, no unification)
  eval.go      fuel-bounded interpreter
  gen.go       deterministic test-input generation (seeded by def hash)
  verify.go    property runner + guarantee tracking
  surface.go   s-expr reader + elaborator (names → indices/hashes)
  pretty.go    projection printer for human auditors
  main.go      put / ls / get / verify / eval
examples/      demo modules
codebase/      the object store (created on first put)
```
