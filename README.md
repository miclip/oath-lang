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
asserted  →  tested (N cases)  →  proven (reserved for v1)
                      ↘  FALSIFIED (with counterexample)
```

See [DESIGN.md](DESIGN.md) for the full rationale and roadmap.

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
