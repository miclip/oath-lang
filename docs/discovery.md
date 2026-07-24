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

## `oath find` (rung 2, today) — query by example

```
oath find <name>
```
Given a definition, returns every *other* definition that satisfies the same
property, matched by `propHash`. A law proven on both sides is flagged
`interchangeable for this law`. Example:

```
$ oath find rat-add
  · commutes [proven here]  #b5c36a6c0677
      rat-mul   (proven as "commutes")  ← proven on both: interchangeable for this law
  · assoc [proven here]  #c4280ba37ac5
      (no other definition shares this law)
```

`rat-mul` was found with **no name trusted** — purely because its commutativity
property hashes to the same value as `rat-add`'s. Available over MCP too (the
`find` tool), since agents are the intended consumers: generate a spec, ask the
commons who already proved it, reuse instead of rebuild.

## The invariant that protects the substrate

**The discovery layer never touches identity.** Code hashes and prop hashes are
*syntactic* and stay that way; `find` (and, later, the e-graph) only draw
*edges* between existing hashes — "these satisfy the same law," "these bodies
rewrite-equal." They never merge two objects into one identity or redefine a
hash. Identity is the O1 encoding (SPEC §1) and remains so. Semantics is a
*view over* the hash graph, never a *change to* it. This is what keeps the
e-graph from destabilizing the foundation when it lands.

## Honest limits (and the roadmap they imply)

`oath find` today is an **exact** match on the property's canonical form,
including binder types. So:

- **Cross-type laws don't match yet.** Commutativity over `Int` and over `Rat`
  are different hashes (different binder types), even though they're "the same
  law." *Next:* normalize binder types to variables so a law matches across the
  types it's polymorphic in.
- **It's query-by-example, not query-by-fresh-spec.** You point at a def that
  already has the property. *Next:* write a standalone spec (a prop over `self`)
  and query it directly — the same `propHash` lookup, just a new front door.
- **It's exact-shape, not implication.** It finds defs with the *same* law, not
  defs whose (possibly stronger) law *implies* yours. *Next:* proof-based
  implication — "does this def's spec entail mine?" — which needs the prover.
- **It's spec-equivalence, not body-equivalence.** Two different proven `sort`s
  with differently-shaped specs won't link. *That* is rung 3, the e-graph.

Each limit is a rung, and they compose: normalize types, add a fresh-spec front
door, then proof-implication, then the e-graph — every step loosening
name-dependence further, none of them touching identity.
