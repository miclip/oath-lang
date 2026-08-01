# Structural strings (substrate change)

**Status:** committed direction (2026-07). Supersedes the SMT-sequence string
model. No back-compat: the corpus re-forks wholesale.

## Decision

`Str` stops being a kernel primitive backed by Z3's sequence theory and becomes a
**distinct inductive datatype of Unicode codepoints**. Every string operation
becomes an ordinary proven definition. The kernel's trusted string surface drops
to **zero** — strings are derived from `List`/`Int`-grade machinery that is
already trusted.

```
(data Str (SNil) (SCons Int Str))     ; a codepoint sequence; SCons carries one scalar
```

`"hello"` is **surface sugar** for `(SCons 104 (SCons 101 (SCons 108 (SCons 108
(SCons 111 (SNil))))))`, resolved by constructor name at elaboration exactly like
the `(list …)` sugar.

### Why this is the correct solution, not a taste

The goal is a substrate whose guarantees are maximally trustworthy and minimally
trusted. Structural strings advance it three ways at once:

1. **Smaller trusted base.** The seven string primitives (`++`, `str-len`,
   `starts-with`, `ends-with`, `str-contains`, `substring`, `str-index-of`) —
   each a *trusted, unproven* assumption spanning check/eval/SMT/compile/both
   kernels — collapse into proven library defs over the `Str` datatype.
2. **No model↔eval bridge.** One definition per operation; eval runs it and the
   prover reasons about it. They cannot disagree.
3. **Full provability of the real type.** `split`'s round-trip and its whole
   class of laws prove by structural / measure induction on `Str`, on the actual
   type literals and I/O produce — verified this direction with prototypes
   (`is-prefix sep ys ⟹ sep ++ drop |sep| ys == ys` proves; a list `split/join`
   round-trip proves), the exact laws Z3's seq theory cannot reach.

### The governing principle

**Keep a type primitive where the solver is complete; make it structural where the
solver is incomplete.** Z3's linear-integer arithmetic is strong and complete →
`Int` stays primitive. Z3's sequence theory is incomplete → strings go
structural. This also says explicitly *not* to structuralize `Int`.

## Kernel surface: removed vs kept

**Removed (deleted from the trusted core):**
- The `str` **type** primitive (O1 `Ty` tag `0x03`; `{K:"str"}`/`tStr()`).
- The `str` **value literal** term (O1 `Term` tag `0x13`).
- All seven string **primitives** and every site that handles them: `surface.go`
  arities, `check.go` typing, `eval.go` evaluation, `prove.go` SMT translation +
  `smtPrimOps`/`Sorts`/`Swap`, `compile.go` codegen + helpers, `ranking.go`
  str-len measure, `mutate.go` swap catalog.
- The `Str`→`String` SMT sort mapping and all `str.*` seq-theory translation.

**Kept (unchanged — these are host/metadata strings, not language values):**
- The `str` **encoding primitive** (u32 len + UTF-8 bytes) used for *metadata*:
  definition/alias names, constructor names, record field names, primitive op
  names, in O1 tags `0x08`/`0x18`/`0x1D`/`0x1E`. These are Go-level strings, never
  `Str` values, and are orthogonal to this change.

So a "string type" in a signature is no longer tag `0x03`; it is a `data`
reference (tag `0x06`) to the `Str` datatype's hash, exactly like `List` or
`Tree`. `Str` itself is a **library datatype** (a `data` def), not a kernel
builtin — the kernel ends up with *no* string concept at all.

## Layers

- **Identity/encoding:** `Str` values are `SCons`/`SNil` ctor chains; a literal's
  canonical bytes are the nested-ctor encoding. Verbose but uniform; correctness
  over compactness. (A compact literal *representation* is a possible future
  optimization that does not touch the semantic/proof base.)
- **Eval:** structural `Str` values. A native-string *representation* optimization
  (pack codepoints as bytes under the hood while preserving datatype semantics) is
  deferred and optional — it changes representation, never semantics.
- **Prover:** `Str` is declared as an ADT like any other; all string laws prove by
  the existing structural / measure / recursion induction. No seq theory.
- **I/O boundary:** capabilities (`fetch`/`env`/`readfile`) and `argv` produce
  `Str` **datatype** values; the host decodes incoming bytes (UTF-8) into codepoint
  `SCons` chains and encodes back at the edge. Native `compile` does the same.
- **Codepoints:** `Int` scalar values (0…0x10FFFF). Range/surrogate validity is a
  convention for now; enforcing it is a later refinement, not part of this change.

## Corpus

All string-using examples are rewritten over the `Str` datatype and re-proved:
the string library (`strings.oath`) becomes append/length/take/drop/is-prefix/
index-of/contains/substring/split with their laws — now fully **proven**,
including `split`'s round-trip — plus the string-content defs in `cli.oath`,
`records.oath`, `service.oath`, and the opaque-`Str` carriers in `netcli`,
`stateful`, `leaky` (whose signatures just move from the `str` tag to the `Str`
data ref). Fixtures regenerate wholesale.

## Staging

0. **This note** — plan of record.
1. **Kernel cutover.** Introduce the `Str` datatype + `"…"` sugar in the
   elaborator; make the typechecker/eval/prover treat `Str` as an ADT; delete the
   `str` type/literal primitives and all seven string primitives. Green build with
   one hand-written string def proving end-to-end.
2. **String library.** Rebuild `strings.oath` over `Str`; prove every law incl.
   `split`'s round-trip; mutate.
3. **Corpus port.** Move the remaining string-using examples; wire the
   capability/`argv` I/O boundary to construct `Str` values.
4. **SPEC rewrite (normative).** §1 (drop the `str` type/literal, keep the `str`
   metadata encoding), §2/§3 (no string primitives; `Str` is an ADT), §5/§6/§7
   (no seq theory; string laws are ordinary induction). Add the `"…"` sugar and
   the codepoint convention.
5. **Second kernel.** Blind `oathrs` re-derives from the rewritten SPEC; six-check
   conformance green.
6. **Fixtures, website playground, CI** regenerated; full re-prove.
