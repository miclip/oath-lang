# Oath: a language designed for AI authors

## Premise

Human languages optimize for being **easy to write**: forgiving syntax,
inference, dynamic typing, files you can skim. An AI author has different
economics — generation is nearly free and **verification is the entire
bottleneck**. Its scarce resource is not typing effort or RAM but *context*:
what must be held in attention to reason about a unit of code. So an
AI-native language should maximize two things, and judge every feature by
them:

1. **Verifiability** — the kernel can *prove* (or at least mechanically
   check) what the author would otherwise have to believe about its own
   output. Confidently-wrong code becomes a rejection, not a bug.
2. **Locality** — everything needed to reason about a definition is in the
   definition (plus its dependencies' *specs*, never their bodies). No global
   state, no spooky action at a distance, nothing that requires having "lived
   in" a codebase.

Corollaries that fall out of these:

- **Annotations everywhere, inference nowhere.** Tedium costs a machine
  nothing; a checker without unification is tiny, fast, and auditable.
- **Content addressing.** A definition's identity is the hash of its
  canonical AST. Names are metadata. Merge conflicts, rename breakage, and
  formatting diffs stop existing. De Bruijn binders make code
  alpha-canonical: there is exactly one way to write a given program.
- **Immutability.** A "change" is a new object plus a repointed name.
  Dependents reference hashes, so nothing can break underneath them.
- **Specs in the signature.** Properties are part of the definition (and its
  hash). Changing the spec *is* changing the definition.
- **Honest guarantees.** Every definition carries its verification status:
  `asserted` → `tested (N deterministic cases)` → `proven` (reserved), or
  `FALSIFIED` with a counterexample. The system never conflates "I ran some
  tests" with "this is true", and never hides a falsified oath.
- **Determinism as a kernel invariant.** No wall clocks, no OS entropy.
  Test inputs are seeded by the definition's hash, so verification is
  reproducible on any machine, forever.
- **Regeneration over editing.** Small, pure, spec-carrying units are meant
  to be rewritten wholesale and swapped in when the checks still pass —
  editing is a human workflow born from typing being expensive.

## What v0 is

A working kernel (~2k lines of dependency-free Go):

- **Calculus**: pure functional terms (lam/app/let/if/match), Int/Bool,
  parametric ADTs, explicit rank-1 polymorphism, recursion via explicit
  self-reference (`self` in terms, `rec` in types — the standard escape
  hatch that makes recursion compatible with content addressing).
- **Kernel gate**: structural typechecker; nothing enters the store without
  passing it.
- **Verification**: property testing with deterministic, hash-seeded input
  generation, including generated higher-order inputs (identity / affine /
  constant functions). Fuel-bounded evaluation, so non-termination is an
  error, not a hang.
- **Store**: content-addressed objects + mutable name index + metadata
  (names, guarantee levels).
- **Projection**: `oath get` renders canonical definitions back to readable
  text for human auditors — the read-only view, like a query plan.

The `bad_reverse` example is the thesis in miniature: a wrong implementation
whose first property *passes* (weak oath) and whose second property is
falsified with a concrete counterexample. Specs are the unit of trust;
redundant, diverse specs are the defense.

## What v0 deliberately is not

- **Not proven.** `proven` is reserved; v0's strongest oath is deterministic
  property testing. The road to real proofs is SMT-discharged obligations
  (Z3) and a Lean-style kernel.
- **No termination checking** — fuel bounds stand in for it.
- **No canonical binary encoding** — v0 hashes Go's deterministic JSON; a
  real spec must define encoding independent of any host language.
- **No mutual recursion, strings, floats, records, or effects.** The effect
  /capability system is the next major design piece.
- **e-graph canonicalization** (collapsing semantically-equivalent forms,
  not just alpha-equivalent ones) is future work.

## Roadmap

- **Phase 1 (this)** — kernel calculus, content-addressed store, property
  verification, projections. *Done in prototype form.*
- **Phase 2** — richer guarantees: SMT-backed proof obligations, effect
  types, termination checking; canonical binary encoding; port kernel to
  Rust once the spec stabilizes.
- **Phase 3** — the agent interface: replace the CLI with a transactional
  API over the codebase graph ("fetch this def + dependency *specs*, sized
  to N tokens"; submit accepted iff the kernel accepts). This is where the
  context-window economics pay off.
- **Phase 4** — the flywheel: verification is an unfakeable reward signal,
  so spec→implementation self-play generates unlimited perfect training
  data. The language that is easiest to verify is the language easiest to
  learn to write.

## Why the kernel is written in a human language

The kernel is the root of trust — the one component that cannot be verified
by itself and therefore must be audited by humans. It belongs in a boring,
readable human language and should stay small enough to read in an afternoon.
Everything *above* the kernel — stdlib, tooling, application code, the
millions of lines nobody can hand-audit — is what belongs in Oath.
