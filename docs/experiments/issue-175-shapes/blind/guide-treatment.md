# Phrasing a query for `oath find`

`oath find` discovers proven code by what it PROVES, not by name. Four modes:

    oath find <name>              by example — needs a name
    oath find --spec <file>       does any definition state this law?
    oath find --implies <file>    does any definition PROVABLY satisfy this law?
    oath find --equiv <name>      body-equivalence — needs a name

A query file is an ordinary definition with a dummy body and one or more `prop`
laws. `self` is the definition being queried, so the law is portable:

    (defn wanted [] [(x Int)] Int 0
      (prop some-law [(x Int)] (== (wanted x) x)))

## When a query returns nothing: check the SHAPE before concluding absence

**An empty result and an empty corpus are the same output.** The two modes that
take a LAW both require you to guess a *shape* — arity, parameter types, return
type, polymorphism — and neither bridges a wrong guess.

- `--implies` PREFILTERS on signature compatibility: a definition whose
  signature does not match yours up to primitive leaves is never considered,
  whatever its body proves.
- `--spec` applies no signature test, but compares property hashes — and a law
  that applies `self` embeds that application in its hash, so a differently
  shaped query hashes differently and misses.

Primitive leaves generalize only in the property's BINDERS (an `Int` law reaches
its `Rat` counterpart). A primitive written into the property BODY — in a type
application, a constructor, an annotation — does not generalize at all.

### Three axes to vary when a query finds nothing

These are the ones that have been observed to matter. They are a place to start,
not a complete list, and exhausting them does not establish that the corpus has
nothing.

1. **RETURN.** Does the operation hand back the whole collection, or ONE element
   of it? A query asking for `(List T)` does not reach a definition returning
   `T`, and nothing bridges that — it is not a primitive-leaf difference.

2. **ABSTRACTION.** Your intent may name a fixed VALUE where the corpus
   generalizes it to a TEST — an extra `(-> a Bool)` parameter the query does
   not have. The mismatch is an extra parameter, not a leaf type, so no mode
   bridges it. Ask whether the thing you are looking for might be expressed as a
   higher-order combinator applied to a predicate.

3. **POLYMORPHISM.** A monomorphic query does not reach a `forall a` definition.
   Declare the query definition polymorphic — `(defn wanted [a] ...)` — and note
   that a definition's type variables are NOT in scope in its property binders:
   state the law at a concrete type, as the corpus's own polymorphic definitions
   do. The type application inside the law is inferred, so writing `[Int]`
   explicitly changes nothing.

### Procedure

1. State the law in your own words. Do not try to guess how a target states it:
   `--implies` proves rather than matches.
2. Run `--spec` first — it is fast, and its fallback list of
   **signature-compatible** definitions is the cheapest signal you have. If it
   names candidates, your signature is close and the law is worded differently;
   `oath get <name>` shows how they state it.
3. If it names nothing, try `--implies` before changing anything. An empty
   fallback list does not mean no signature matched: the neighbour list is built
   only from definitions carrying properties of their own.
4. If that also finds nothing, vary the SHAPE using the three axes above.
5. On any shape that surfaces candidates, run `--implies`.
6. **`NO VERDICT` is not absence.** It reports a limit of the prover, not a fact
   about the definition — a body outside the provable fragment yields no goal to
   solve.
7. When you see one, re-run with `--details`: it NAMES each unsettled candidate
   and says why. That is a name you can `oath get` and read.
8. Only if that leaves nothing, `--equiv` is the remaining route — at a price:
   it takes an IMPLEMENTATION where the others take a LAW.
