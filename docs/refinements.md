# Refinement types — design note (#69)

**Status:** design only. Nothing is implemented, and nothing here should be
implemented until the identity decision below is accepted, because that decision
is not a thing you can revise later — it determines what a hash means.

## What this is

A refinement type attaches a proposition to a type: `{x: Int | x > 0}` is the
Ints that are positive. The proposition is discharged by the same SMT machinery
that already discharges properties (§7), so this needs no new solver, no new
translation fragment, and no new guarantee vocabulary.

It is the natural next axis for Oath, and the reason is structural rather than
fashionable. The thesis is *properties in identity*: machine-checked promises are
part of what a definition IS. Today those promises live at the **property** layer
— claims *about* a definition, attached to it. Refinements move the same idea into
the **type** layer, where they become claims the type system enforces
compositionally at every use site, not just facts recorded about one definition.

Concretely, what it buys:

- **Invariants become structural.** A function taking `{n: Int | n > 0}` cannot be
  applied to a possibly-zero `Int` without discharging an obligation. The
  guarantee moves from a proven property *about* the function into the *type of*
  the function.
- **Partial operations become total on a refined domain.** `head` on a non-empty
  list, division by a non-zero denominator: no `Result` wrapper, the caller proves
  the precondition. This is directly relevant to #71's division work, where `/` and
  `%` are *partial* (they error on zero) and the SMT bridge deliberately leaves
  division-by-zero unconstrained. A refined divisor type would let the kernel
  reject the bad call instead of leaving the goal unprovable.
- **It composes with what exists.** Termination, confinement, and the property
  ladder are unchanged; refinements add obligations, they do not reinterpret
  verdicts.

## THE IDENTITY DECISION

This is the fork. Everything else is engineering; this is not.

A refinement is a proposition, and propositions have many syntactically different
forms with the same meaning: `{x | x > 0}`, `{x | 0 < x}`, `{x | x >= 1}`. Since
refinements are part of the type, and types are part of the canonical O1 encoding
(§1), the question is unavoidable:

> Do logically-equivalent refinements produce the same hash?

**Decision: NO. Refinement identity is SYNTACTIC, exactly like every other part of
the O1 encoding. `{x | x > 0}` and `{x | 0 < x}` are different types with
different hashes.**

### Why

The project has already answered this question twice, in the same direction, and
written the answer down as an invariant:

- `docs/discovery.md`: *"The discovery layer never touches identity. … They never
  merge two objects into one identity or redefine a hash."*
- `docs/egraph.md`: *"Canonicalization never touches identity."*

Semantic equivalence in Oath is a relation drawn **over** the hash graph, never
into it. Adopting semantic refinement identity would reverse that for types
specifically, and the consequences are not cosmetic:

1. **Identity would become undecidable.** Proposition equivalence over the theories
   Oath admits (unbounded Int, ℚ, IEEE floats, ADTs, quantifiers) is not decidable
   in general. A hash function that must decide it is not a hash function. Every
   `put` would depend on a solver call that may not terminate.
2. **Identity would become solver-dependent.** If two refinements hash the same
   because Z3 4.16.0 proved them equivalent, then the identity of a definition
   depends on a solver version. `docs/SPEC.md` §1 already treats encoding changes
   as forking reality; making them depend on a *prover* makes reality fork on a
   dependency upgrade. The whole point of the deterministic rlimit budget (§7.2) is
   that verdicts are machine-independent — but verdicts are *metadata*. Identity
   has a stricter standard, not a looser one.
3. **The N-version experiment would collapse.** `oathrs` reproduces hashes
   byte-for-byte from the spec. It could not do so if hashing required agreeing
   with another solver's equivalence judgements. The strongest evidence this
   project has that its spec is real is that a blind kernel reproduces its hashes;
   semantic identity would trade that away.
4. **It buys less than it appears to.** The thing users actually want is not "these
   are the same object" but "I can pass this value where that type is expected."
   That is *subtyping*, and subtyping is where the SMT belongs (below). Syntactic
   identity plus semantic subtyping gets the ergonomics without putting a solver
   in the hash.

### What this costs, honestly

Two definitions whose refinements differ only in spelling are different objects,
even though they are interchangeable in use. That is a real duplication cost in
the commons: the store may hold `{x | x > 0}` and `{x | 0 < x}` versions of the
same idea.

This is the *same* cost the project already accepts for bodies — `(+ a b)` and
`(+ b a)` are different objects — and the answer is the same: the **discovery
layer** relates them. `oath find` already has the machinery (spec-query by
property hash, proof-implication, e-graph canonicalization), and refinements
should plug into it rather than into hashing. A `find --equiv` over refinements is
future work, not a blocker.

### Consequence for the spec

If refinements are added to `Ty`, §1's encoding gains a case and **every existing
hash is unaffected** only if the encoding is extended in a way that leaves
unrefined types byte-identical. That must be a hard requirement: a refinement
feature that re-hashes the existing corpus would fork reality for no reason. The
natural shape is a new `Ty.K` value (`"refine"`) wrapping a base type plus a
proposition, so unrefined types encode exactly as they do today.

## Subtyping: where the SMT actually goes

`{x: T | P} <: {x: T | Q}` iff `P ⟹ Q`. That implication is an SMT query, and it
is where every interesting refinement obligation is discharged:

- Passing an argument: the actual type must be a subtype of the declared one.
- Returning a value: the body's type must be a subtype of the declared result.
- `{x: T | P} <: T` always (forgetting a refinement is free).
- `T <: {x: T | P}` only with a discharged obligation.

Decidability follows the base theory, which is a known quantity here: Int/Rat/Bool
are good, Float is decidable via FPA, `Str` is the weak spot (see #68), and
quantified or recursive refinements will sometimes not discharge.

**Obligations get the guarantee ladder, not a binary.** This is the design's
sharpest fit with the rest of Oath. An undischargeable obligation must NOT be a
hard type error — that would make the type system's usefulness hostage to solver
completeness. Instead an obligation should carry the same vocabulary properties
already have:

- **proven** — SMT discharged it.
- **tested** — the deterministic tester (§4) found no counterexample.
- **falsified** — a counterexample exists; this IS a hard error, because the
  program is definitely wrong.
- **asserted** — neither proved nor tested; recorded honestly.

That makes refinements *strictly additive* to the guarantee story rather than a
second, stricter gate that can reject working programs. It also means `oath hint`
(#67) applies unchanged: a stuck obligation can be given a stepping-stone lemma.

## Interaction with what exists

- **Termination (§6).** Refinements do not affect the measure. But a refined
  domain can make a measure provable that was not — `{n: Int | n >= 0}` removes
  the negative case that defeats many ranking functions. Worth exploiting, not
  required.
- **Attempt validity (§7.2, #72).** Obligation discharge is a solver attempt like
  any other, so it inherits the per-property validity rule: an environmentally
  aborted obligation has NO verdict and must not be recorded as failed. Getting
  this wrong would make builds fail on slow machines.
- **Generics (docs/generics.md).** Dictionary-passing is unaffected, but refined
  type parameters raise the question of whether a dictionary can be required to
  satisfy a refinement. Defer: B2/B3 are already deferred.
- **Confinement.** Unaffected — refinements constrain values, not escape.
- **The e-graph.** The obvious temptation is to reuse AC-normalization to reduce
  spurious refinement distinctions. Resist it *in identity* for the reasons above;
  use it in discovery.

## Staging

1. **This document, accepted.** The identity decision is load-bearing for
   everything after it.
2. **SPEC §1 encoding** for a `refine` type constructor, with the hard requirement
   that unrefined types stay byte-identical, plus fixtures proving the corpus
   hashes do not move.
3. **Checker**: bidirectional checking with annotations at binders; subtyping as
   an SMT query; obligations recorded with the ladder above.
4. **Blind-kernel implementation** from the spec, as with #67/#71/#72 — expected
   to surface ambiguities, which is the point.
5. **Discovery integration** (relating logically-equal refinements) — last, and
   explicitly not part of identity.

## Open questions, not settled here

- **Inference.** Full refinement inference is undecidable; the assumption is
  bidirectional checking with annotations, but where exactly annotations are
  required is unspecified.
- **Measure interaction.** Whether a refined domain should feed the termination
  checker automatically, or only when asked.
- **Str refinements** are largely useless until #68, since the base theory cannot
  discharge them.
- **Error surface.** An undischarged obligation needs to be legible — which
  obligation, at which call site, and what the ladder recorded. The #72 reporting
  rule (claims pinned, surface free) is the precedent to follow.
