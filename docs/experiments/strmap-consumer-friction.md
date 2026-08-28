# str-map consumer friction — a wordcount CLI

A demand list produced by BUILDING a real consumer of the #184 native Str-keyed
map, the way `webhook-friction.md` was produced by building the webhook. The
program is `wordcount.oath` (in this directory): it tallies command-line words
into a `StrMap` and prints `word count` lines in `str-lt`-sorted key order.

**It works, and the container itself is frictionless.** The program compiles on
both backends and agrees three ways:

- `oath eval` and the Go binary and the LLVM binary produce identical output on
  scrambled, duplicate-heavy, and prefix-adjacent inputs (`the cat the dog the`
  → `cat 1 / dog 1 / the 3`; `banana apple cherry apple banana apple date banana`
  identical Go vs LLVM).
- Native lowering fires on both: the emitted Go carries 8 `smap*` helper calls
  (`smapInsert`/`smapLookup`/`smapKeys`/`smapEmpty`) and zero structural
  assoc-list fallback; LLVM lowers to the `T_STRMAP` persistent tree.

So `str-map` is a real, usable native container. Everything below is friction at
the EDGES — composition and tooling — not in the map. Ranked by value.

## 1. Native-container recognition does not compose with a namespaced registry dependency

**This is the sharpest finding, because it undercuts the point of publishing
str-map to the registry.** The recognizer that turns `str-map-*` calls into native
map operations has two halves that key on different things:

- **Recognition** (emit time) is by OBJECT HASH:
  `oath/compile.go` `recognizeSetOp` does `op, ok := e.nc.Ops[head.Hash]`. The
  NAME used to reference the operation is irrelevant — content-addressing working
  as designed.
- **Discovery** (building `nc.Ops`) is by BARE NAME:
  `oath/program.go` `validateFamily` does `st.Resolve(name)` for each bare name in
  `strMapOpNames` (`"str-map-insert"`, …). It only registers a family's hashes if
  the BARE names resolve in the build store.

Consequence for a consumer who depends on the published copy: the registry carries
str-map under `michael/oath/str-map-*` (and NOT under bare names — `oath ls`
confirms bare `smi-lookup`/`str-map-*` are absent on the live registry). A program
built against a store populated only from that namespace has no bare
`str-map-insert` to resolve, so `validateFamily` never records the family,
`nc.Ops` is empty, and every `str-map-*` call falls back to its STRUCTURAL
assoc-list body — correct, but O(N) per lookup, O(N²) for the tally. Silently. The
consumer gets none of the native container they thought they were importing.

This is the same class as the standing note that "the capability vocabulary is
global and name-based (#117)": a global, bare-name-keyed vocabulary does not
survive namespacing. The missing artefact is discovery keyed on the operation's
IDENTITY (hash / structural signature) rather than its bare spelling — then a
namespaced alias of the same object is discovered exactly as the bare name is,
and recognition (already hash-based) composes end to end.

Scope, stated precisely so it is not overread: native lowering is lost only when
the build store lacks the bare names. A consumer who ALSO has the corpus (bare
names present) gets native lowering even while referencing the namespaced copy,
because recognition is by hash. The failure is specific to a pure-registry
consumer — which is exactly the consumer the registry exists to serve.

## 2. There is no way to see the emitted backend source

`oath build` writes `main.go` (or the LLVM IR + C runtime) into an
`os.MkdirTemp` directory and `defer`s its removal (`oath/compile.go`). There is no
`--emit-source` flag and `-o foo.go` still produces a binary. A consumer asking
the obvious question — "did my `str-map` actually lower to a native map, or the
structural fallback?" — has nothing to inspect. Confirming finding #1's native
path at all required racing the temp directory to copy `main.go` before cleanup.
The missing artefact is a one-line flag to emit the lowered source next to, or
instead of, the binary.

## 3. `oath eval` prints a `Str` as codepoints, and there is no lightweight "run"

`oath eval '(wordcount …)'` returns the result as the raw `Str` datatype —
`(SCons 99 (SCons 97 (SCons 116 …)))` — not `cat`. The only way to see a program's
string output as text is to `oath build` a native binary (invoking `go`/`clang`)
and run it. There is no `oath run <name> -- args` that interprets a program and
renders its `Str` result as text. For a consumer iterating on a string-producing
program this is a real tax: every "what does it print?" is a full native build.
Two small artefacts would remove it — a `Str` renderer in `eval`'s output, and/or
an interpreted `oath run`.

## 4. No module/import system; dependency provenance is folklore

`wordcount` depends on `show-int` (defined in `circle.oath`), `str-append` /
`str-join` (`str.oath`), and `str-map-*` (`strmap.oath`) — three example files a
consumer must already KNOW to have loaded into the store, with nothing in the
program stating what it needs or where each name comes from. The store is one flat
namespace and a program simply assumes every name it uses is already present. This
is livable at corpus scale and won't be past it; the missing artefact is a
per-program dependency manifest (which names, at which hashes) that a build could
resolve — from a store or a registry — rather than requiring the whole world to be
pre-loaded.

## What this says about ranking

Finding #1 is the one worth acting on: it is not a rough edge, it is the published
artefact failing to deliver its headline property to the consumer it was published
for, and the fix (discovery by identity, not spelling) is a known shape the
project already has vocabulary for. #2 and #3 are cheap tooling wins that would
have paid for themselves during this very build. #4 is real but not yet urgent —
it is the friction that arrives with the FIRST external program the corpus does
not already contain.
