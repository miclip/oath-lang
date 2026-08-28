# Semantic canonicalization (the e-graph) — design note

**Status:** shipped (2026-07) — commutativity + type-directed associativity;
extended (2026-08, "rung 1a") with identity elements, Bool idempotence, `neg`
involution, and the control-flow and binding rules; extended again (2026-08,
"rung 1b") with a REAL E-GRAPH — hash-consed e-nodes, union-find e-classes,
congruence closure, bidirectional distributivity over `Int`/`Rat` saturated to a
fixpoint under explicit budgets, and cost-based extraction. The last rung of the
discovery ladder (docs/discovery.md): find definitions that are the SAME FUNCTION
even when their bodies differ — body-equivalence, not just same-property (rung 2)
or provably-satisfies-my-spec (proof-implication).

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
`eCanonicalArith` then runs the NON-confluent rules through a real e-graph and
extracts a representative (see "Rung 1b" below); `eHash(def)` = hash of the
definition's signature plus that representative.
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

## What witnesses the normal form, and what does not (#152)

`eNormalize`'s output IS discovery: `eHash` is signature plus normalized body,
and `find --equiv` groups by it. A change to the normal form does not degrade
performance or break a proof — it **may silently redefine equivalence classes**,
and the two ways it can go are worth separating. A RULE change moves only
definitions containing an applicable redex, so it repartitions some classes and
leaves the rest alone. A change to canonical BYTES can move every `eHash` while
leaving the partition identical. Neither is visible as a failure anywhere
obvious, which is why it is worth stating plainly what would notice.

**Measured, by mutating SIX of rung 1a's rules, one at a time.** Not all of
them: eta reduction, the Boolean unit rules, `and` idempotence and several type
variants were not mutated, so no witness is claimed or denied for those.

    rule mutated                    goldens      caught by
    ---------------------------------------------------------------
    `+ 0` Int/Rat                   SURVIVED     find_test.go
    `* 1.0` Float                   SURVIVED     find_test.go
    `or` idempotence                SURVIVED     find_test.go
    `negInvolution`                 SURVIVED     find_test.go x3
    `ifSelect` const-cond           SURVIVED     find_test.go x4
    `ifSelect` identical-branches   SURVIVED     find_test.go x5

**Six of six survived the recorded digests. Six of six were caught by the
discovery-behaviour tests.**

**THE WITNESS FOR THESE SIX IS `find_test.go`, AND IT WORKS ON `eHash`.** The
relevant cases compare equivalence keys directly; they do not call
`apiFindEquiv` or assert its printed output. That is the right level — `eHash`
is exactly what `find --equiv` groups by — but it is a claim about the KEY, not
about the command's returned text, and nothing here covers the latter.

**THE RECORDED GOLDENS ARE NOT A SYSTEMATIC WITNESS, WHICH IS NARROWER THAN
SAYING THEY WITNESS NOTHING.** They do catch some rules: disabling Int `* 1`
moves the `int-times-duplicates` digest, and the Boolean unit rules moved
`bool-and-duplicates` and `bool-or-duplicates` when rung 1a landed. What the
measurement establishes is that they catch **none of the six mutated above** —
`+ 0` Int/Rat, Float `* 1`, `or` idempotence, `negInvolution`, and both
`ifSelect` forms. So they cover part of the rule set by accident of which shapes
were chosen, and no argument says which part. Do not call them the witness, and
do not call them useless.

**A GOLDEN CASE CAN BE TOPICALLY EXACT AND STILL INCAPABLE OF WITNESSING ITS
RULE**, which is the finding worth carrying past this issue.
`bool-or-duplicates` looks like it covers `or` idempotence and does not: the
unit pass consumes the duplicates before idempotence can act. `int-plus-*` and
`+ 0` are the same shape. **Coverage cannot be read off case names** — it has to
be measured by mutation, which is why this was settled that way rather than by
inspection, and why any future fixture needs its own coverage argument rather
than a plausible-looking list.

**THE ACCEPTED RESIDUAL GAP, stated so nobody has to rediscover it.** These
tests compare INLINE SYNTHETIC PAIRS, not equivalence matches among the
committed corpus. So the gap is not "a change that leaves corpus matches
identical" — it is narrower and differently shaped: **a normal-form change that
does not alter the `eHash` relation between any of the specific pairs those
tests construct would pass**, whatever it does to the corpus. Conversely, a
change that repartitions real corpus definitions is not guaranteed to fail them.
That is accepted deliberately, not overlooked:

- `eNormalizeRecursive` cannot close it — it calls the same `acFlatten` and
  `acRebuild` as the subject, so both sides of that differential move together.
- `oathrs` implements no `eNormalize`, so the normal form sits outside the
  cross-kernel conformance surface. A byte-exact cross-kernel witness would
  require implementing it there, BLIND from `docs/SPEC.md` — never by copying
  from `oath/`.

**Rung 1b did not close it either, and chose a different witness.** Equality
saturation and extraction moved the normal form far more than rung 1a did, so
the gap above would have widened. What was added instead of a golden is a
CORPUS-WIDE INERTNESS check (`TestEgraphIsInertWithoutArithRulesOnTheCorpus`):
the e-graph is forced to run on every committed body, and wherever no rule fires
its extraction must reproduce the e-normalized bytes exactly. That witnesses the
extractor against the real population rather than against synthetic pairs — but
it is silent about bodies where a rule DOES fire, and on this corpus none does.

## Rung 1b: the real e-graph

Rung 1a's four bullets — no distributivity, no e-classes, no saturation, no
extraction — described exactly what a CONFLUENT rule set does not need. All four
are now present, because distributivity is the rule that ends confluence:
`a*(b+c)` and `a*b + a*c` are equal, neither is canonically smaller in the sense
a rewriter needs, and expanding one can enable a factoring that re-creates the
other. There is no orientation to pick, so the class cannot be decided by
rewriting a term in place.

`eHash` therefore runs TWO passes, and they are different kinds of thing:

    eNormalize        the confluent rules, applied directly to a normal form
                      (everything above; unchanged)
    eCanonicalArith   the non-confluent rules, run through an e-graph and
                      resolved by extraction (oath/egraph.go)

### The structure

- **e-node** — an operator symbol plus the e-CLASSES of its children, never
  child terms. The symbol is the canonical encoding of the node with every child
  slot replaced by one fixed placeholder, so two nodes have one symbol exactly
  when the canonical encoder does not distinguish them. It is derived from the
  real encoder rather than from a hand-written field comparison, for the reason
  that rule exists: a hand-written key compares the fields its author
  remembered.
- **e-class** — a set of e-nodes asserted equal, with union-find owning the
  relation. The SMALLER class id always survives a merge, so a class's
  representative is a function of which classes exist and never of the order
  they were merged in.
- **congruence closure** — after a union, two nodes with the same symbol whose
  children are now in the same classes denote the same value, so their classes
  merge too. Restored to a fixpoint after every saturation round; a merge can
  make a further pair congruent one level up.
- **AC nodes** — `+` and `*` over `Int`/`Rat` enter as ONE n-ary node over the
  flattened chain, with children held as a sorted MULTISET (sorted for
  hash-consing, never deduplicated: `a + a` is `2a`, and neither operator is
  idempotent). Every other term node is an ordinary structural node. `and`/`or`
  are deliberately left binary: no rule here touches them, and the narrower
  representation cannot perturb what rung 1a already decided.

### The rules, and where they are allowed to fire

All three are confined to `+` and `*` over `Int` and `Rat`. Both types are
EXACT — `Int` is ℤ and `Rat` is ℚ — so there is no overflow and no rounding for
a re-association or a re-distribution to expose.

| rule | fires on | note |
| --- | --- | --- |
| `x*(y+z)` ≡ `x*y + x*z` | `Int`, `Rat` | products and sums of ANY arity, since both are flattened |
| `x*y + x*z + w` ≡ `x*(y+z) + w` | `Int`, `Rat` | the inverse direction; the factor comes out of every addend that can supply it |
| `op(x, op(y, z))` ≡ `op(x, y, z)` | `Int`, `Rat` | associativity ACROSS A MERGED CLASS — see below |

**`Float` is excluded, and it is the same exclusion associativity already
carries.** `a*(b+c)` and `a*b + a*c` differ in binary64 for real inputs, so
distributing over `Float` would merge definitions that disagree on an input.
The type direction is read from `isACPrim` — the existing authority for "may
this operator be re-associated at this operand type" — narrowed to the two exact
numeric kinds, rather than from a second list that could drift from it. Operand
types are synthesized against a THROWAWAY COPY of the body, because the checker
publishes inferred type arguments into the term it is given and `eHash` must not
move as a side effect of type inference.

**Why associativity is a RULE here when the representation is already flat.**
Flattening happens at INSERTION, on syntax. Once a rewrite has merged classes, a
child class can come to CONTAIN a same-operator node without any term ever
having been written that way, and the flattened form of that is a node nothing
else would create. It takes TWO nested sums to observe: with one, the nested
chain sorts last and right-nesting reproduces the flat chain by accident.

### Extraction: which term becomes the `eHash`

Saturation produces a set of equal terms; extraction picks one.

- **Cost is TREE SIZE of the REBUILT AST** — summed over children, so the
  extractor prefers `a*(b+c)` (5 nodes) to `a*b + a*c` (7). The most-factored
  form is the canonical one. An n-ary AC node charges **`n-1`**, not 1, because
  that is how many `prim` nodes it rebuilds into: a flattened 4-way sum is three
  `+` nodes in the extracted term. Charging it as one made the cost function
  disagree with the AST it names, and since extraction MINIMISES that cost the
  disagreement decides which representative a class settles on.
- **Ties break on CANONICAL BYTES, never on class ids.** Ids are assigned in
  insertion order, so a tie broken on them would make `eHash` depend on how a
  definition happened to be written rather than on what it means — and equal
  costs are not a corner case: `a*b + a*c + d*b` has two different cheapest
  factorings.
- **AC chains are rebuilt by `acRebuild`**, the same function `eNormalize` uses,
  so the ordering, the right-nesting and the unit rules cannot drift into a
  second version of themselves.
- The relaxation is iterated to a fixpoint and is cycle-safe; a class whose best
  term is not yet known simply contributes nothing that round.

### Budgets, and what happens at the cap

Equality saturation is unbounded in general — a product of `n` sums expands
exponentially — so three explicit limits apply, all in `oath/egraph.go`:

| limit | value | what happens when it is reached |
| --- | --- | --- |
| term size | 2048 nodes | the e-graph is not built; the e-normalized term is hashed exactly as before |
| e-nodes | 8192 | saturation stops; extraction runs on the graph as it stands |
| saturation rounds | 12 | the same |

**Exceeding a limit is not an error and never produces a wrong answer.** Every
node in a class is equal to every other, so any representative is a correct one:
what a budget costs is COMPLETENESS — two definitions that would have been found
equivalent under a larger budget may not be. Stopping is also DETERMINISTIC: rule
application, unions and extraction ties are all decided without reading a Go
map's iteration order, so the same input under the same budget produces the same
bytes every time.

**The bound is regression-tested by NODE COUNT, not by timing or by scaling.**
`TestEgraphEngineCostIsBoundedBelowTheCap` runs a term dense with distributivity
redexes and asserts the graph never exceeds the declared node budget — and that
some row actually REACHES it, so the assertion is satisfied by the guard rather
than by arithmetic. A scaling ratio was tried first and does not witness this:
re-run under a 25x budget the cost-per-doubling ratios stay in the same band
while absolute cost goes from 60 MB to 2.7 GB, so a missing bound is invisible
to a scaling test and obvious to a node count.

A **syntactic pre-check** runs first and skips the whole machine for a body with
no distributivity or factoring match. It is **conservative, and only one
direction of that matters**: it has no FALSE NEGATIVES — it never skips a term a
rule could fire on, because at insertion every class is a singleton, so a rule
can only match a shape the term already has, and only a rule firing creates new
matches.

It does admit FALSE POSITIVES, deliberately. The factoring test asks whether a
sum has two PRODUCTS under it, not whether those products share a factor —
deciding that here would be a second copy of the factoring rule's own matching,
free to drift from it — and operand types are not consulted, so a `Float` chain
passes the pre-check and is stopped later by the annotation. Measured on the
committed corpus: **34 bodies are admitted and 0 fire a rule.**

**It sizes the term BEFORE it matches, and each maximal AC chain is flattened
once.** Both are load-bearing rather than tidy: matching walks chains, so a
survey that flattened at every level was quadratic in chain depth, and running
it before consulting the node cap meant the cap could not protect the case it
exists for — a 65,536-node arithmetic chain that the portable profile admits and
`find --equiv` reaches. Measured at 4 GB allocated for a 16,000-node chain
before the split, 47 MB after; `TestEgraphSurveyScalesOnDeepChainsAboveTheCap`
holds the line by scaling rather than by a wall-clock bound. A body with no
arithmetic redex therefore hashes precisely the bytes it hashed before rung 1b —
measured over the committed corpus, where the pass fires on nothing and moves no
`eHash`.

### Completeness limits, stated so nobody re-derives them

- **Factoring takes the MAXIMAL support**: a factor comes out of every addend
  that can supply it, not out of each subset of them. Subsets are exponential;
  the maximal choice is the one that inverts distribution.
- **A budget truncation is silent by design.** It loses equivalences, never
  invents them.
- **The rule set is the ceiling.** Full extensional equivalence is undecidable;
  this collapses what the rules reach and nothing else. `--implies` remains the
  complementary mechanism — general over the decidable fragment, pairwise, and
  blind to recursion.

### The invariant, now regression-tested rather than argued

`oath/discovery_identity_test.go` snapshots the canonical ENCODING, the
IDENTITY, every property's `propHash` and `propHashGeneral`, and the store's
name resolution — over the committed corpus and over a constructed store
carrying the polymorphic and arithmetic shapes the corpus lacks — and re-checks
them after EVERY `eHash` and `find --equiv` call, then again through a freshly
opened store.

Per-call rather than end-to-end for a measured reason: an endpoint comparison
cannot see a write that undoes itself over an even number of calls, and a mutant
that swapped a commutative primitive's operands on every `eHash` passed the
endpoint form of that test.

The hazard it guards is real, not decorative. `Store.GetDef` CACHES, so every
call for one hash returns the same `*Def` pointer — a mutation made while
hashing would be published to every later consumer in the process — and
`eNormalize` calls `chk.synth` on the original subterm, which is a live write
path into the structure whose bytes ARE the identity.

## Why this is the right shape

Everything here draws edges over the hash graph and leaves identity alone; the
rule set is a knob that only ever *adds* recognized equivalences; and each rule
is sound by construction (we apply a law only where it holds — hence the
type-direction for associativity, learned straight from the numeric tower). The
commons gets a canonical-form dedup key that started modest (AC), reached the
saturating engine, and never forked reality on the way.
