# Semantic canonicalization (the e-graph) — design note

**Status:** shipped (2026-07) — commutativity + type-directed associativity. The last rung of the discovery
ladder (docs/discovery.md): find definitions that are the SAME FUNCTION even when
their bodies differ — body-equivalence, not just same-property (rung 2) or
provably-satisfies-my-spec (proof-implication).

## What it is, and the honest ceiling

Two definitions can compute the same function with different bodies: `(+ a b)`
and `(+ b a)`; a def and its eta-expansion; two different `sort`s. We'd like the
commons to treat these as one — so a fresh implementation dedups onto an existing
proven one, and discovery finds it.

The honest ceiling: **full extensional equivalence is undecidable.** No engine
collapses *all* equal functions. What an e-graph does is collapse things equal
**under a rewrite rule set** — congruence closure over the rules, saturated to a
fixpoint. A useful, bounded class, and the standard tool (egg). Anything outside
the rules stays distinct; that's a soundness feature, not a gap to apologize for.

There are two complementary mechanisms for body-equivalence, and we already have
one:

- **Proof-based** (`oath find --implies` today, extendable to `--equiv`): prove
  `∀ args. f(args) == g(args)` via Z3. General for the *decidable fragment*
  (non-recursive Int/Bool/Rat/Float), but pairwise (O(n) solver calls) and blind
  to recursion.
- **Rewriting-based** (this note): normalize a body to a **canonical form**, hash
  it. Two bodies with the same canonical hash are equal under the rules. The win
  is that it is a *canonical form* — one normalize + hash, then an O(1) lookup
  across the whole store, which is exactly what commons dedup needs. Bounded to
  the rule set, but decidable and cheap.

The e-graph is the rewriting mechanism, and its payoff for the commons is the
canonical form, not a pairwise oracle.

## The load-bearing invariant (unchanged from discovery.md)

**Canonicalization never touches identity.** A definition's identity stays the
O1 hash of its *actual* canonical AST (SPEC §1). The e-graph computes a SEPARATE
key — call it the `eHash` — over a *rewritten* form, used only to draw
equivalence edges between existing objects. `(+ a b)` and `(+ b a)` remain two
distinct objects with two distinct identities; the e-graph just records that they
land in the same equivalence class. Semantics is a view over the hash graph,
never a redefinition of it. This is what keeps the e-graph from destabilizing the
foundation everything else stands on — and why it can be built additively.

## The first slice: AC-normalization

The first, soundest rules are the algebraic ones for the built-in operators:

- **Commutativity** — sort the operands of a commutative primitive into a
  canonical order (by their own encoded bytes). Sound for EVERY operand type:
  `+`, `*`, `==` (structural equality is symmetric), `and`, `or` all commute for
  all their types (`+`/`*` commute even for `Float` — only associativity fails
  there). `(+ a b)` and `(+ b a)` normalize to one form. **This slice.**
- **Associativity** — flatten and re-sort `+`/`*` chains (and `and`/`or`), so
  `(+ (+ a b) c)`, `(+ a (+ b c))`, and `(+ c (+ b a))` all normalize to one
  form. This is **type-directed**: sound for `Int`/`Rat` (and `and`/`or` over
  `Bool`), but NOT for `Float` — float addition isn't associative, which is
  exactly the law `examples/float.oath` falsifies, so a `Float` chain is only
  commutatively sorted, never flattened. `eNormalize` therefore threads the
  checker and de Bruijn context and synthesizes the operator's operand type to
  decide (`isACPrim`). **This slice** — the numeric tower told the e-graph where
  each rule is allowed to fire.
- Unit/identity laws (`x + 0 = x`, `x * 1 = x`), and eventually a real
  saturating e-graph with a growable rule set and equality-saturation extraction,
  are the deeper versions still to come — tracked, with the other discovery
  rungs, in issue #65.

`eNormalize` walks a term applying the confluent rules directly to a normal form
(a full e-graph data structure isn't needed while the rules are confluent);
`eHash(def)` = hash of the definition's signature plus its normalized body.
`oath find --equiv <name>` returns the definitions sharing an `eHash` —
different identities, same equivalence class.

## Why this is the right shape

Everything here draws edges over the hash graph and leaves identity alone; the
rule set is a knob that only ever *adds* recognized equivalences; and each rule
is sound by construction (we apply a law only where it holds — hence the
type-direction for associativity, learned straight from the numeric tower). The
commons gets a canonical-form dedup key that starts modest (AC) and grows toward
the full saturating engine, without ever forking reality.
