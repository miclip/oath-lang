# Semantic canonicalization (the e-graph) — design note

**Status:** shipped (2026-07) — commutativity + type-directed associativity;
extended (2026-08, "rung 1a") with identity elements, Bool idempotence, `neg`
involution, and the control-flow and binding rules. The last rung of the discovery
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
`eNormalize` walks a term applying the confluent rules directly to a normal form
(a full e-graph data structure isn't needed while the rules are confluent);
`eHash(def)` = hash of the definition's signature plus its normalized body.
`oath find --equiv <name>` returns the definitions sharing an `eHash` —
different identities, same equivalence class.

## Rung 1a: the structure-removing rules

Commutativity and associativity REORDER a chain. These REMOVE structure from it,
so a body carrying an identity element, a duplicated `Bool` operand, or a doubled
`neg` lands in the same class as the body it equals. Each rule is stated with the
types it fires on and the types it is EXCLUDED from, because the exclusions are
where the soundness lives:

| rule | fires on | excluded from, and why |
| --- | --- | --- |
| `x + 0` ≡ `x` | `Int`, `Rat` | **`Float`** — `-0.0 + 0.0` is `+0.0`, a distinct value under the kernel's Leibniz `==`, so this would merge definitions that disagree on an input |
| `x * 1` ≡ `x` | `Int`, `Rat`, `Float` | nothing — `x * 1.0` is `x` for every IEEE value: `±0.0` keeps its sign, `±inf` is unchanged, and NaN stays NaN, which is an identity rather than a near-miss only because the kernel canonicalizes every NaN to one bit pattern |
| `x and true` ≡ `x`, `x or false` ≡ `x` | `Bool` | — |
| `x and x` ≡ `x`, `x or x` ≡ `x` | `Bool` | **`+` and `*`** — they are not idempotent (`a + a` is `2a`), so a rule keyed on "the same operand twice" rather than on which operators are idempotent would be unsound |
| `neg (neg x)` ≡ `x` | `Int`, `Rat`, `Float` | nothing — `neg`'s typing rule admits exactly those three, and flipping a sign twice restores the exact value, including `±0.0`, `±inf` and (canonical) NaN |

Type direction is carried by the leaf's own kind, and that is exact rather than a
shortcut: the checker refuses operands of mixed numeric type and the language has
no numeric coercion, so an `int`-kinded literal inside a `+` proves the whole
chain is `Int`-typed. A `Float` chain cannot smuggle an `int` `0` past the test,
because such a term does not typecheck.

The unit and idempotence rules are applied where a whole chain is visible — once
per maximal associative-commutative chain, not per level — so they compose with
the flattening rather than fighting it.

## Rung 1a: control flow and binding

These are restricted by Oath's **strict evaluation order** and by **binding
structure**, not by any type's algebra. So unlike the table above they carry no
`Float` carve-out and hold at every well-formed type.

- **`if true`/`if false` select their branch.** Unconditional: the other branch
  was never going to be evaluated, so discarding it removes no work whatever it
  contains. The branches themselves carry no side condition — a non-total live
  branch is preserved and a dead branch is discarded unevaluated.
- **`(if c x x)` ≡ `x` only when `c` is ALREADY A VALUE** — a `var`, which
  call-by-value has already bound to a value, or a `Bool` literal. The evaluator
  evaluates an `if` condition before selecting a branch, so a condition that is a
  COMPUTATION is part of what the term does; dropping a divergent one turns a
  non-terminating term into a terminating one, which is removing divergence
  rather than preserving meaning.
- **`fn x. (f x)` ≡ `f`** when the argument is exactly the removed binder and the
  head is a value. The left side is a value whatever `f` is, while the right side
  evaluates `f` immediately, so a head that diverges would MOVE divergence from
  application time to construction time. The binder must also not occur free in
  the head: removing it shifts every free index, and an occurrence of the binder
  itself has no index to shift to.

**The admitted eta heads are `var` and `ref`, and that boundary is a COST one as
much as a semantic one.**

- A `var` head is one node. Its index *is* the freeness question — index 0 is the
  removed binder itself, and is rejected — and the shift is one decrement.
- A `ref` head is one node carrying a hash and TYPE arguments, so it contains no
  term de Bruijn index at all: the binder cannot occur free in it and the shift
  is vacuous. It is not a value by KIND, because evaluating a `ref` evaluates the
  referenced body and a nullary definition's body is not a lambda — so it is
  admitted only when a store lookup shows that body BEGINS WITH a lambda, which
  makes evaluating it a closure construction.
- **A `lam` head is equally sound and is deliberately NOT admitted.** It is
  unbounded, so deciding freeness and shifting indices inside it means walking a
  subtree containing the rest of the term — quadratic on the tower
  `fn x. ((fn y. …) x)`, on a term the portable profile admits and
  `find --equiv` normalizes. Re-admitting it requires making freeness and
  shifting O(1) first (a min-free-index attribute computed during normalization,
  and shifts accumulated through a chain of etas rather than applied per level),
  not merely re-establishing that the rewrite is sound, which it already is. An
  allocation-scaling regression holds that line.

### The lam-head refusal, measured (#155)

**The falsifier came back ZERO: admitting `lam` heads discovers nothing on the
committed corpus.** The sound rule — freeness and shifting decided by traversal,
which is what the deferred attribute would make O(1) — moves no definition's
normal form and creates no new equivalence:

    committed corpus, codebase/ at 9f7cbf3 (2026-08-07) — a ONE-SHOT
    experiment, like the tower timings above: the lam-head normalizer exists
    only in the harness, so nothing in the tree regenerates these.

    unique function digests                194   210 live names, 15 non-function,
                                                 deduped by hash
    pairs compared                      18,721
    definitions whose eHash MOVED             0
    NEWLY equivalent pairs                    0
    equivalent pairs at baseline              0   nothing was equivalent before,
                                                  either

The census says why, and says it more strongly than the pair count can: those
194 bodies normalize to **322 `lam` nodes, of which 3 are eta-shaped
`fn x. (H x)` — and their heads are `app` (2) and `self` (1).** Bodies only,
because `eHash` is signature plus normalized BODY and nothing else; the corpus's
property bodies carry 2 further `lam` nodes and no eta shapes at all. Not one
lam-headed redex exists in the corpus, and **none of the three that do exist is
excluded by the cost boundary:**

- `json-string-value` and `parse-nat` head theirs with an `app`, a COMPUTATION,
  refused by the head-is-a-value condition.
- `spin` is `fn x. (self x)`, outside the admitted `var`/`ref` set — and `self`
  is not the cheap addition it resembles. The `ref` rule's licence is that the
  referenced body BEGINS WITH A LAMBDA, and for a `self` head this rewrite
  destroys its own premise: `spin`'s body would become `self`, which evaluates
  by evaluating `spin`'s body again. That is the same divergence-moved-to-
  construction failure the value condition exists to prevent, arriving through
  the head's own definition.

**Method, and the two controls the zero rests on.** The universe is
`apiFindEquiv`'s own — every live name resolved, kept when `K == "func"`,
deduped by hash — because a walk of `meta/` would answer a question about the
store's HISTORY while looking like a question about the corpus. `eHash` was then
computed for every definition twice, once under the shipped rule and once under
the lam-head rule, and the two partitions compared pairwise. **An instrument
that measures nothing also reports zero**, and the baseline row above shows this
corpus offers no positive instance of its own, so the discrimination was
established synthetically first: the rule fires on `fn x. ((fn y. y) x)`,
refuses when the binder occurs free in the head, decrements outer free indices —
and end-to-end, a definition whose body is a lam-head redex takes the same
`eHash` as its already-reduced twin only when the rule is on. The corpus zero is
therefore a fact about the corpus rather than about the harness.

**What it does not say.** It is a statement about THIS corpus, not about Oath
programs in general: `examples/` and `apps/` are the exhibits this project
chose, and the eta tower is a term the portable profile admits whether or not
anyone here has written one. It also does not make the rewrite unsound — it is
sound and unadmitted, which was already the position. What it removes is the
reason to PAY for it. On this evidence the recommendation is that **#155 closes
DECLINED**: the admitted-head boundary stands, now on measurement rather than on
caution.

## What rung 1a is NOT

Named explicitly, because "the e-graph" invites all four:

- **No distributivity.** `a * (b + c)` and `(a * b) + (a * c)` remain distinct.
  Distribution is not confluent as a rewrite in either direction — it needs a
  cost function to choose a direction, which is an extraction problem, not a
  normalization one.
- **No e-class data structure.** There is no union-find over terms; `eNormalize`
  rewrites directly to a normal form, which is all a confluent rule set needs.
- **No equality saturation.** Rules are applied once, bottom-up, to a fixed
  normal form. Nothing runs to a fixpoint over a growing set of equalities.
- **No extraction.** With no e-classes and no cost function there is nothing to
  extract a best representative from; the normal form IS the representative.

Those four are what a real saturating engine (egg-style) buys, and they are the
deeper version still to come — tracked, with the other discovery rungs, in issue
#65. Adding a non-confluent rule such as distributivity is precisely the point at
which the direct-normalization shortcut stops working and the data structure has
to arrive.

## Why this is the right shape

Everything here draws edges over the hash graph and leaves identity alone; the
rule set is a knob that only ever *adds* recognized equivalences; and each rule
is sound by construction (we apply a law only where it holds — hence the
type-direction for associativity, learned straight from the numeric tower). The
commons gets a canonical-form dedup key that starts modest (AC) and grows toward
the full saturating engine, without ever forking reality.
