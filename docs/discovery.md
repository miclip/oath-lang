# Discovery: finding proven code by what it does, not what it's called

**Status:** first rung shipped (2026-07) — `oath find`; the rest is roadmap.
This is the layer that turns the content-addressed store from "a place your
code lives" into "a commons you can *draw from*" — you find an existing proven
definition by the **property it satisfies**, with no name trusted.

## Why this exists

Every other discovery path is name-keyed: `ls`, `get`, `dependents`, `context`
all take a name. But names are the one mutable, non-authoritative, collision-
prone layer (`docs/teamstore.md`, and the naming discussion generally). So
"reuse the proven `validate`" secretly means "reuse whatever *my store's* label
`validate` points at" — which is exactly the trust we removed everywhere else.
Discovery needs to key on *meaning*, not on a label.

## The key realization: properties are content-addressed too

A property is stored as `(binders, body)`, where the function under proof is
`self` (not a ref) and the binders are de Bruijn indices. So a **pure algebraic
law carries no names and no specific hashes** — commutativity is literally
`(== (self a b) (self b a))` — and its canonical encoding has **one hash**
wherever it appears, on any definition, of the matching operand types.

That means "which proven definitions satisfy this spec?" is a **hash lookup,
not a search**. `propHash` (canon.go) is the content address of a property;
`oath find` indexes it. Content-addressing, which already gave code a
name-independent identity, gives *specs* one too.

## The three-rung ladder

Discovery by meaning has three strengths of "the same," each looser than the
last and each further from name-dependence:

1. **Syntactic** — identical AST → same code hash. (Already how identity works.)
2. **Spec-equivalent** — different implementations that satisfy the **same
   property**. *This is the rung `oath find` implements*, and it was already
   latent in the proofs we store — it just had no query surface. If two defs
   share a law and both **prove** it, they are interchangeable *for that law*.
3. **Rewriting-equivalent (e-graph)** — different *bodies* that are provably
   equal under a rule set, collapsed to one canonical form. The deepest dedup;
   the biggest build; roadmap (DESIGN.md flags egg/e-graphs). Its hard
   constraint is below.

## `oath find` (rung 2, today)

Two front doors, both matching on the generalized property content hash
(`propHashGeneral` — matched *up to operand types*):

**Query by example** — point at a def whose property you want:
```
$ oath find rat-add
  · commutes [proven here]  #f230af55f94f
      rat-mul   (proven as "commutes")  ← proven on both: interchangeable for this law
  · assoc [proven here]  #59b248e21d01
      (no definition in the store satisfies this)
```

**Query by fresh spec** — write the property you want; the sought function is
`self`, the body is any trivial placeholder of the right type:
```
$ cat spec.oath
(defn wanted [] [(a Int) (b Int)] Int (+ a b)
  (prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))
$ oath find --spec spec.oath
spec query "wanted" — which proven definitions satisfy it (by content hash, no name, no example):
  · commutative [tested here]  #f230af55f94f
      rat-add   (proven as "commutes")  ← a proven implementation of this spec
      rat-mul   (proven as "commutes")  ← a proven implementation of this spec
```

Both find `rat-add`/`rat-mul` with **no name trusted** and (here) across types
— an `Int` spec matched the `Rat` implementations. Both are exposed over MCP
(`find` and `find_spec`), since agents are the intended consumers: generate a
spec, ask the commons who already proved it, reuse instead of rebuild.

## The invariant that protects the substrate

**The discovery layer never touches identity.** Code hashes and prop hashes are
*syntactic* and stay that way; `find` (and, later, the e-graph) only draw
*edges* between existing hashes — "these satisfy the same law," "these bodies
rewrite-equal." They never merge two objects into one identity or redefine a
hash. Identity is the O1 encoding (SPEC §1) and remains so. Semantics is a
*view over* the hash graph, never a *change to* it. This is what keeps the
e-graph from destabilizing the foundation when it lands.

## Honest limits (and the roadmap they imply)

`oath find` matches a property up to its operand types (`propHashGeneral`
generalizes the primitive leaf types in the binders to positional type
variables, so commutativity over `Int` and over `Rat` both become `[t0, t0]`
and match). What remains:

- **Body-embedded types aren't generalized yet.** Generalization today covers
  binder types, which is complete for the pure algebraic laws whose bodies
  carry no types (commutativity, associativity, idempotence). A law whose *body*
  mentions a type — a generic callee's type arguments, a `(Nil [Int])` in the
  statement — still matches only same-type (it's safe, never a false match,
  just not yet cross-type). *Next:* thread the same type-generalization through
  the body's `ctor`/`ref`/`self` type arguments.
- **Query by fresh spec** is now supported alongside query-by-example:
  `oath find --spec <file>` (and the `find_spec` MCP tool) elaborate a `(defn
  ...)` whose `(prop ...)` clauses are the query — the sought function is
  `self`, the body is any trivial well-typed placeholder — and return every
  proven definition that satisfies them. This is "I have a spec; who has proven
  an implementation?", the core commons interaction, with no name and no example
  needed. The dummy body is the one wart (you must write a well-typed
  placeholder to give `self` a signature); auto-synthesizing an inhabitant of
  the return type would remove it.
- **It's exact-shape, not implication.** It finds defs with the *same* law, not
  defs whose (possibly stronger) law *implies* yours. *Next:* proof-based
  implication — "does this def's spec entail mine?" — which needs the prover.
- **It's spec-equivalence, not body-equivalence.** Two different proven `sort`s
  with differently-shaped specs won't link. *That* is rung 3, the e-graph.

Each limit is a rung, and they compose: normalize types, add a fresh-spec front
door, then proof-implication, then the e-graph — every step loosening
name-dependence further, none of them touching identity.
