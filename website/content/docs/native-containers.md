# Native containers — Set and Map (compiler)

**Status:** shipped (2026-07), part of the compiler backend (#13). Extends the
"prove over the structural model, run over a native representation" refinement
from `Str` (a codepoint datatype proven inductively, compiled to a Go string) to
the finite containers `Set` and `Map`.

## The idea

`Set` and `Map` are ordinary Oath datatypes — the proof model and the
interpreter both use them directly:

```
(data Set []  (MkSet (List Int)))                 ; strictly-sorted, dedup'd
(data Map []  (MkMap (List (Pair Int Int))))      ; sorted by key, dedup'd
```

The sorted list is canonical (#37): a set/map has one representation regardless
of insertion order, so identity is semantic. Properties are proven against this
model (`si-*` / `mi-*` helpers carry the #37 laws — sorted-insert commutes and
is idempotent, lookup-after-insert finds, and so on).

At **compile time only**, `oath build` refines the representation: a `Set`
becomes a native Go hash map (`oset`), a `Map` becomes a native Go hash map
(`omap`). Membership, lookup, `has`, and `size` are **O(1)**; the recognized
`set-*` / `map-*` operations lower directly to native map access. Because Oath
values are immutable, the pure updates (`add`, `insert`, `union`, `merge`) are
copy-on-write (O(n)) — true persistent maps (HAMTs) are a later refinement, so
today the win is on reads, not writes.

## Why a distinct type is required

Str compiles natively because a string *is* a codepoint cons-list — same shape,
so the constructors swap directly. A Go map is a *different* shape from a sorted
list, and the operations aren't constructors — they're recursive definitions.
So native backing needs two things a bare `List Int` can't give:

1. **A type the compiler can recognize.** An arbitrary `List Int` is not sorted,
   and programs use lists as lists. Only a distinct `Set`/`Map` type lets the
   compiler safely pick the map representation — otherwise representation
   selection is unsound.
2. **A recognized operation vocabulary.** The `set-*` / `map-*` operations are
   lowered by name to native helpers at their saturated call sites; their
   sorted-list bodies (and the `si-*`/`mi-*` helpers) are not emitted at all.

`MkSet`/`MkMap` and `match` on a `Set`/`Map` are the **List boundary**: building
a set from a list fills the native map; matching one materializes its
**sorted** contents. So a value is uniformly a native map inside compiled code,
and every crossing back to a list is sorted — which is exactly what the
structural model would produce.

## Correctness — the differential gate

None of this rests on the refinement being *obviously* sound. It rests on the
differential gate that already guards the compiler: a compiled program must
produce byte-identical output to `oath eval` (the interpreter's sorted-list
model). `TestCompileNativeSetDifferential` and `TestCompileNativeMapDifferential`
exercise it — a set built out of order with duplicates answers membership
exactly as the model does; a union reports the dedup'd size and its sorted
minimum; a map with a repeated key looks up the latest value and misses an
absent one. If the native map and the structural model ever disagreed on an
observable output, the gate would fail.

## Structure in the corpus

- `examples/set.oath` — `Set`, the `si-*` sorted-list core (proven), and the
  `set-*` wrappers (the recognized vocabulary): empty, member, add, union,
  inter, size, elems.
- `examples/map.oath` — `Map`, the `mi-*` core, and the `map-*` wrappers:
  empty, insert, lookup, has, keys, values, size, merge.

The recursion (and thus the termination proofs and the deep induction) lives in
the `si-*`/`mi-*` helpers, which descend on `List` subterms and are total; the
`set-*`/`map-*` wrappers are non-recursive over the newtype.

## Not yet

Persistent (HAMT) maps for O(log n) functional updates; generic element/value
types (today `Set` is of `Int`, `Map` is `Int → Int`, matching #37); and the
native representations participating in the fast MLIR/LLVM execution path.
