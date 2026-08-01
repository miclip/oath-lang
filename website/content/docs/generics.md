# Generics via dictionary passing (#33)

Status: B1 shipped (convention + generic combinators, all PROVEN).
B2 (verified laws) and B3 (resolution sugar) deliberately deferred.

## The move

Oath's typing is bidirectional local synthesis: no full inference, no unification of two unknowns, and a
monomorphic `==`. You cannot say "for any T with an ordering" — until you
notice the language already says it about the *world*. A capability is a
record of functions passed as an ordinary parameter; a type-class instance
is exactly the same object with a different name on the door:

```lisp
(net {fetch (-> Str Str)})   ; a world you can query   (docs/effects.md)
(ord {lt    (-> a a Bool)})  ; an ordering you can ask  (this document)
```

So ad-hoc polymorphism lands with **zero new rules in the trusted core** —
the argument that chose capability passing over effect rows decides this
too. An instance is a record literal at the call site; a constrained
function is a function with a dictionary parameter; and properties
quantify over dictionaries exactly as capability properties quantify over
worlds: the generator supplies table-function dictionaries, and the prover
binds them as array-encoded record fields — so `PROVEN` means *for every
dictionary*, lawful or not.

## The B1 convention

- **Ord** is `{lt (-> a a Bool)}`. One field, no new `Ordering` ADT, and
  "x ≤ y" is spelled `(not (lt y x))`. Three-way `cmp` waits for B2, where
  verified laws make the richer shape worth carrying.
- **Eq** is `{eq (-> a a Bool)}`. Container equality is *relative to* an
  element dictionary (`list-eq-by`), because kernel `==` cannot cross
  function types.
- Instances are ordinary values. Nothing resolves them for you (that is
  B3); you pass them, and the signature shows exactly which comparisons a
  definition may perform — the same audit purity gives capabilities. The
  confinement checker verdicts dictionaries like any capability record:
  every B1 combinator is `ord: confined` / `eqd: confined`.

`examples/generic.oath` ships the worked set, every property PROVEN over
all dictionaries: `count-by`, `list-eq-by`, `min-by`, `max-by`,
`insert-by`, `sort-by` — including the generic permutation oath,

```lisp
(prop preserves-counts [(ord {lt (-> Int Int Bool)}) (eqd {eq (-> Int Int Bool)}) (x Int) (xs (List Int))]
  (== (count-by [Int] eqd x (sort-by [Int] ord xs))
      (count-by [Int] eqd x xs)))
```

— `sort-by` moves nothing in or out, for every ordering dictionary and
every equality dictionary at once.

## What lawless dictionaries refuse to grant (the case for B2)

Two properties of the monomorphic `sort` do NOT survive generalization,
and the kernel proved it the honest way:

- `is-sorted-by ord (sort-by ord xs)` is **false** for arbitrary `ord` —
  a dictionary with `(lt x y)` and `(lt y x)` both true breaks it. Never
  sworn; documented in the source.
- `snoc-is-insert` — the monomorphic sort's own proof lemma — was tried
  and **FALSIFIED in 2 cases** by a generated table dictionary: it
  secretly rides on insert commutativity, which needs the ordering laws.

That is the precise shape of B2, *verified type classes*: a dictionary
whose laws (totality, transitivity, antisymmetry) are themselves proven
properties, so downstream proofs may assume them and the two theorems
above become provable relative to a lawful dictionary. The gap between
"what structure alone grants" (length, counts, membership arithmetic) and
"what laws grant" (sortedness, rewrite lemmas) is now demonstrated inside
the store rather than argued in prose.

## Deferred

- **B2 — verified laws**: prover work; how properties reference dictionary
  parameters' own properties. The novel piece.
- **B3 — resolution sugar**: surface-level instance resolution so callers
  don't thread dictionaries by hand. Touches SPEC §1.4 + both kernels +
  conformance; its own epic. Until then, where instances live is a naming
  convention, not a mechanism.
