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

- **Annotations on every binder, no *full* inference.** Type arguments may be
  omitted and are inferred by one-sided matching (never unification of two
  unknowns), so the checker stays tiny, fast, and auditable.
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

## Spec strength: who verifies the specs?

The system's load-bearing weakness, named plainly: Oath relocates trust from
implementations to specs — so what stops an author (especially an AI author
writing both sides) from swearing weak oaths? A tautological property passes
trivially; the phase-4 "verification is an unfakeable reward signal" claim is
only as good as the specs, or it becomes reward hacking moved up a level.

The kernel's partial answer is **mutation testing** (`oath mutate`): generate
type-preserving mutations of the implementation and check whether the
properties notice. The killed/total score is recorded next to the guarantee
— `tested` says the promises held; spec strength says whether the promises
say anything. The `length` case study is instructive: its original
`non-negative` property scored 1/5 (mutants returning `length+1`, `length·0`
and `const 0` all survived); adding a base-case anchor and a step law took it
to 5/5. Survivors demand judgment, not automation: `insert`'s `<= → <`
mutant survives because on bare Ints the output list is literally identical —
the classic equivalent-mutant problem.

This is a mitigation, not a solution. The remaining defenses are structural:
separating spec-authorship from implementation-authorship (different agents,
adversarial spec review), human-owned specs at trust boundaries, and treating
low spec-strength scores as publication blockers. Fully closing the loop is
an open problem, and DESIGN.md will keep saying so until it isn't.

## Two axes, not one ladder (2026-07-14)

The experiments forced a correction to the guarantee ladder's framing. A
definition can be fully PROVEN and still have surviving mutants — the
corpus's own `is-sorted` was proven 2/2 while scoring 0/5, and the BST spec
passed testing *and* proof while a duplicate-placement mutant sailed
through. That is not a contradiction; it is two independent measurements:

- **Depth** (the ladder): does each property hold — sampled (`tested`), or
  for all inputs (`PROVEN`)?
- **Completeness** (spec strength): do the properties *collectively pin the
  function down* — does changing the body make some promise fail?

`PROVEN` is the top of the depth axis only. A definition is trusted in the
sense a reader assumes only when it scores on both. The two axes also have
distinct failure modes with distinct defenses, established adversarially:

- **Weakness** (tight code↔spec link, promises that say nothing) is killed
  by mutation testing — mechanically, no judge required. The controlled
  rematch ran as a 2×2: specs for eight corpus functions re-authored from
  briefs by a model, with and without the mutation scorer available during
  authoring, attached to the original bodies (identical mutant catalogs),
  against the founding-corpus baseline. Founding specs: 33/50. Model with
  the scorer in the loop: 41/50. Model blind — no scorer, no store, no
  validation: 41/50, function-for-function identical. Two successive
  framings of this result were refuted by measurement: "models beat
  humans" fell to the loop confound, then "closing the loop raises spec
  strength" fell to the blind control. What the loop actually bought was
  epistemic, not optimizational: the loop-condition author predicted its
  scores accurately, validated its survivor-equivalence claims against a
  reference, and shipped waiver justifications; the blind author produced
  equally strong specs with no verified claims about them at all.
  Verification did not make the artifact better — it made claims about
  the artifact trustworthy, which is the substrate's own thesis applied
  to its own experiment. (Survivor equivalence arguments remain
  self-authored: waived legibly, machine-proven only where an SMT
  artifact exists.)
- **Misalignment** (a spec internally tight around the *wrong* function)
  is invisible to mutation by construction: an adversary instructed to
  cheat delivered sum-of-squares for a sum-of-absolute-values brief at the
  top of both axes — 7/7 PROVEN, 5/5 mutants killed. The brief is not an
  object in the system; no checker can read it. What mutation *did* force
  is legibility: maxing the kill score required signing the wrong defining
  equation into the spec, in the open. The audit surface this leaves for
  humans is small and namable — check the defining equations against
  intent — instead of "read everything."
- The structural defense against misalignment is **independent redundancy**
  (N-version specification): specs for the same brief authored by disjoint
  processes collide mechanically when they disagree — no implementation
  can satisfy both sum-abs's and sum-of-squares' defining equations, and
  the kernel falsifies one within a case or two, naming which author the
  body followed. Detection is free; only adjudication needs the trusted
  party. This has its own honest regress: two authors misaligned to the
  same wrong function still pass, and intent always enters the system from
  outside, as an axiom, supplied by whoever is trusted. The entry point
  can be moved; it cannot be deleted. This is now a first-class analysis
  (`oath cross`, SPEC §6.4, #20): given two identically-signed definitions
  it evaluates each one's properties against the other's body and returns
  `AGREE`/`DISAGREE` with the falsifying counterexample, optionally sealing
  the verdict into the journal as `kind=cross` provenance. The manual
  demonstration became a verb; the honest regress above is unchanged and is
  recorded normatively alongside it.

## The split-agent workflow experiment (2026-07-13)

External review proposed treating separated spec/implementation authorship as
central, and proving the workflow rather than asserting it. Ran, with two
independent agents against this repo:

**Setup.** Agent A (implementer) was given the language cheat sheet, four
contracts with seed properties (max2, drop, take, contains), and `oath
context` output only — reading any implementation body was forbidden. Agent B
(spec adversary) then mutation-scored A's work and strengthened the specs,
allowed to touch only `prop` forms.

**Results.**
- A implemented all four correctly with no falsified submissions (the
  immutable store proves this: exactly one object per name, no branded
  predecessors). Spec-only context was sufficient; its slice cost ~700 tokens
  vs ~1,500 for the then-full source corpus. The ratio is unremarkable at 17
  definitions — the claim that matters is asymptotic: slices grow with the
  task's dependency frontier, repo-reading grows with the codebase.
- B's baseline scores exposed real under-specification the verdicts had
  called done: drop 5/9, take 3/9. After strengthening: 9/9 and 9/9, with
  max2's one survivor correctly classified as an equivalent mutant.
- B found no implementation bugs — the gate plus seed properties had already
  forced correctness.

**Two findings worth keeping:**
1. *Witness reliability ≠ logical strength.* `take-then-drop-rebuilds` is a
   correct, general law, yet six mutants survived under it — uniform random
   ints rarely produce the boundary n ∈ {0,1,2} that distinguishes off-by-one
   mutants. Deterministic ground anchors killed them instantly. Kernel fix
   applied: the generator now biases toward boundary values.
2. *Mutation scores are a floor, not a target.* B's two most valuable
   strengthenings — max2's "result is one of the inputs" (the original
   lower-bound-only spec would bless a `max+1` implementation) and contains'
   count-characterization — moved no score at all: no available single
   mutation expresses those gaps. Spec quality above the floor still needs an
   adversarial reader.

**Positioning consequence.** Two independent reviews converged on the same
reframe, and the experiment supports it: Oath's value is not as a
general-purpose language but as an AI-facing verified codebase kernel — a
substrate where agents submit small units, get deterministic verdicts,
retrieve dependency contracts sized to a token budget, and regenerate safely.
The syntax is disposable; the substrate is the product.

## Prior art

Oath is a synthesis, not an invention; the pieces have owners:

- **Unison** — content-addressed definitions, names-as-metadata, and the
  codebase-as-database. Oath's store is directly in its lineage. Unison's
  hard lesson is also inherited and unsolved here: the cost is rebuilding the
  entire tooling ecosystem (diff, blame, review, refactoring) from scratch.
- **Lean / Agda** — the small-trusted-kernel architecture, and structural
  termination checking (Agda's Foetus) planned for the next kernel rung.
- **QuickCheck** — property-based testing with generated inputs.
- **Mutation testing** (DeMillo/Lipton/Sayward) — spec-strength scoring.
- **Dafny / Liquid Haskell** — specs carried in signatures and checked
  mechanically.
- **egg / e-graphs** — the future answer to canonicalizing semantically
  (not just alpha-) equivalent forms.

## What v0 deliberately is not

- **Proven only for a fragment.** `oath prove` discharges properties in the
  non-recursive Int/Bool fragment via Z3 (unbounded-int semantics, stated on
  every proof). Recursion needs induction — the road there is a Lean-style
  kernel. Everything outside the fragment bails with a reason and stays
  `tested`; `examples/undertested.oath` shows why the distinction matters
  (200 cases passed, refuted at x = -401).
- **Termination is proven only structurally** — a Foetus-lite check (after
  Agda): total iff some fixed argument position strictly descends at every
  self-call and all callees are total (hash-acyclicity makes this compose
  bottom-up for free). Non-structural recursion is labeled `termination
  unproven`, never rejected; fuel and a recursion-depth bound contain it at
  runtime. This was the first `proven` fact to enter the metadata.
- **No canonical binary encoding** — v0 hashes Go's deterministic JSON; a
  real spec must define encoding independent of any host language.
- **No mutual recursion, floats, or effects.** The effect/capability system
  is the next major design piece. (Structural records landed after v0; strings
  are now the ordinary `Str` datatype, not a primitive — see
  docs/structural-strings.md. Record field names are semantic — part of the type
  and hash —
  but field order is canonicalized away, like variable names before it.)
- **e-graph canonicalization** (collapsing semantically-equivalent forms,
  not just alpha-equivalent ones) is future work.

## Roadmap — status as of 2026-07-15

Phases 1–3 are COMPLETE, beyond the original ambitions:

- **Phase 1 ✓** — kernel calculus, content-addressed store, property
  verification, projections. Then far past it: structural termination,
  capability confinement, mutation-scored spec strength with justified
  waivers, and an O1 binary identity encoding that inherits nothing from
  any host language (SPEC §1; store migrated wholesale, mappings journaled).
- **Phase 2 ✓ (mostly)** — SMT proofs are real INCLUDING structural
  induction with a relevance-filtered lemma library (§7.2): 99 definitions
  fully PROVEN, insertion sort 7/7. The Rust kernel exists — built BLIND
  from docs/SPEC.md + fixtures by an agent that never saw the Go source,
  conforming byte-for-byte on all six checks, wasm32-ready. Effects
  resolved by capability passing + state-as-data (docs/effects.md); floats
  and mutual recursion remain out.
- **Phase 3 ✓** — MCP over stdio and over HTTP with authenticated
  principals (the team store), spec-only context slices by token budget,
  and a repoint policy that makes authorship separation enforcement, not
  procedure (docs/teamstore.md). Cross-kernel CI guards it all on every
  push.
- **Phase 4 (open)** — the flywheel: verification as an unfakeable reward
  signal. Scoped experiments ran (docs/experiments): the split-agent
  workflow validated spec-blind implementation; the 2×2 rematch showed the
  verification loop buys trustworthy CLAIMS about artifacts rather than
  better artifacts; the misalignment adversary marked the boundary (briefs
  are not objects the system can check). The full self-play training loop
  remains future work, as do the compiler backend (#13) and the public
  registry (#14).

The conformance saga is its own result: two kernels, zero shared code,
kept in byte-level agreement by CI — and the blind implementation found
two spec bugs, a fixture bug, a migration bug, and a latent analysis bug,
including refusing to force a stale fixture green. N-version validation
works, and the spec, not either implementation, now carries the semantics.

## Why the kernel is written in a human language

The kernel is the root of trust — the one component that cannot be verified
by itself and therefore must be audited by humans. It belongs in a boring,
readable human language and should stay small enough to read in an afternoon.
Everything *above* the kernel — stdlib, tooling, application code, the
millions of lines nobody can hand-audit — is what belongs in Oath.
