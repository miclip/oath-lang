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
  non-recursive Int/Bool fragment via Z3 (unbounded-int semantics — now the
  kernel's actual `Int`, which is arbitrary precision, so proofs hold with no
  overflow caveat). Recursion needs induction — the road there is a Lean-style
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
- **No mutual recursion or effects.** The effect/capability system is the next
  major design piece. Numbers, by contrast, are settled — three primitives,
  chosen by the same lens: put a type where the solver is strong. `Int` is ℤ
  (arbitrary precision); `Rat` is ℚ (arbitrary-precision exact rationals); and
  `Float` is IEEE-754 binary64, for bit-level interop with the outside world.
  The lens is completeness: Z3's sequence theory is INCOMPLETE, so `Str` became
  a *structural* datatype proven by induction — while Z3's real (LRA) and float
  (FPA) theories are both COMPLETE, so `Rat` and `Float` stay *primitive* and
  translate straight to the `Real` and `Float64` sorts. Structure where the
  solver is weak, primitive where it is strong. `Rat` and `Float` split the
  numeric world cleanly: `0.1 + 0.2` is exactly `3/10` as a `Rat` (the clean
  algebraic laws — associativity, distributivity, `(a/b)*b == a` — are proven),
  while `0.1f + 0.2f` is the honest `0.30000000000000004f` as a `Float` (that
  law is falsified, correctly — floats reach `proven` for their *true*
  properties, like `x + x == x*2`, since FPA is decidable). The one subtlety
  `Float` forces is identity: a content-addressed kernel needs one canonical
  form, so a `Float` value IS its bit pattern (NaN canonicalized to one),
  structural `==` is Leibniz equality (`NaN == NaN`, `+0.0 ≠ -0.0` — SMT `=`),
  and IEEE's `fp.eq` is a separate opt-in primitive. See docs/floats.md.
  (Structural records landed after v0; strings are now the ordinary `Str`
  datatype, not a primitive — see docs/structural-strings.md. Record field
  names are semantic — part of the type and hash — but field order is
  canonicalized away, like variable names before it.)
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
  induction with a relevance-filtered lemma library (§7.2): 123 definitions
  fully PROVEN, insertion sort 7/7. The Rust kernel exists — built BLIND
  from docs/SPEC.md + fixtures by an agent that never saw the Go source,
  conforming byte-for-byte on all six checks, wasm32-ready. Effects
  resolved by capability passing + state-as-data (docs/effects.md); the numeric
  tower is complete — `Int` (ℤ), `Rat` (ℚ), and `Float` (IEEE-754); mutual
  recursion remains out.
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
  remains future work, as does the public registry (#14). The compiler backend
  (#13) has a working first stage: `oath build` lowers a definition's closure to
  a standalone native binary, refusing anything not proof-carrying, and shows the
  "prove over the structural model, run over a native representation" pattern —
  `Str` datatype values compile to native Go strings, and `Set`/`Map` (distinct
  types over the #37 sorted-list model) compile to native Go hash maps with O(1)
  membership/lookup — all verified by a differential gate (compiled output ==
  interpreter output; see docs/native-containers.md). The fast execution path
  (MLIR/LLVM) and persistent (HAMT) maps for O(log n) functional updates remain.
- **Discovery — the commons made usable.** The public registry (#14) is more
  than storage; its point is that you draw *proven* code from it instead of
  rebuilding. That needs discovery keyed on MEANING, not on names (the one
  non-authoritative layer). `oath find` is the first rung, and it fell out of
  content-addressing: a property is stored `(binders, body)` with the function
  as `self` and de Bruijn binders, so a pure law has one canonical hash wherever
  it appears — "who satisfies this spec?" is a hash lookup. Four modes shipped
  (docs/discovery.md): by example, by a fresh spec you write, matched up to
  operand types, and — because a property is *portable* — by proof-implication
  (append your spec to each same-signature definition and prove it, so a law
  written differently from any stated one is still found). A load-bearing
  invariant runs through all of it and guards the eventual e-graph: the
  discovery layer draws edges *over* the hash graph, it never touches identity.
  Semantics is a view, not a redefinition — the e-graph (body-equivalence) is
  the last open rung.

The conformance saga is its own result: two kernels, zero shared code,
kept in byte-level agreement by CI — and the blind implementation found
two spec bugs, a fixture bug, a migration bug, and a latent analysis bug,
including refusing to force a stale fixture green. N-version validation
works, and the spec, not either implementation, now carries the semantics.

## The applicability inversion (2026-07-28)

A decision record, not an observation. It reorders the next three milestones.

### Thesis

Oath's long-term value is **proof-carrying composition**: an agent assembles a
program out of artifacts whose claims come with evidence, instead of out of
packages whose claims come with a README. That requires three things we do not
yet have together — real-world artifacts, discoverability by intent, and
executability in a real environment.

**The current system proves the model, but does not yet support the workflow.**

The workflow an autonomous agent actually runs is `search → inspect evidence →
compose → execute`. It does not begin with authoring, and it does not begin with
proving. Framed that way the competition is not "Python versus proving an Oath
program" — it is **package discovery versus proof-carrying artifact discovery**,
which is a far stronger position. A conventional registry tells you what code
*claims* to do. Oath can tell you what the artifact **is**, what it **claims**,
and what **evidence** backs the claim.

Which yields the sharper form: *AI does not need to prove everything it uses. It
needs a reliable way to discover and compose things that have already been
proven.*

### Current state — the foundation is not the problem

- The **evidence layer** is real and differentiated: per-property proof status,
  mutation-scored spec strength with justified waivers, termination and
  confinement verdicts, an append-only signed provenance journal.
- **Content addressing solves versioning structurally** rather than by policy.
  Dependents pin hashes, names are metadata, dependency closure is exact by
  construction. "Evolve dependencies without destabilising everything" is not a
  feature here; it is a consequence of identity.
- **Cross-kernel validation is unusually strong.** Verdicts are reproduced by a
  second kernel implemented blind from the spec. Almost no registry can offer
  evidence of that kind about its own tooling.

### Hard limits — the blocking gaps

**Coverage.** 187 definitions, deep but narrow: list, sort, tree, queue,
interval, map, set, string, rational, plus honest exhibits. Nothing corresponds
to a task an agent is actually given.

**Discovery.** `oath find` is spec-native, not intent-native. Its four modes all
require the caller to already think in algebraic properties — powerful once you
know the law you want, useless at the moment you only know the goal.

**Environment fit.** *(Corrected 2026-07-28 after reading the code rather than
the roadmap — the first draft of this section said real programs are
"inexpressible", which is wrong and worth correcting precisely, because the
inaccuracy pointed the work at research when most of it is not.)*

The MECHANISM is shipped and works end to end: a capability-first entry point is
verified against every simulated world, and `oath build` wires genuine
implementations exactly once at the program boundary — refusing to wire an entry
that is falsified, unverified, or whose capability the confinement checker marks
ESCAPES. The corpus witness `main-fetch` is PROVEN 3/3 over all worlds and then
runs against a live HTTP server. Stateful worlds are shipped too, as a pattern:
state is data, transitions are code, and the world's laws are ordinary proven
properties.

What is thin is the capability **VOCABULARY**. Exactly three capabilities are
wireable, and all three are `(-> Str Str)`: `fetch` (HTTP GET), `env`,
`readfile`. Every one is read-only and outbound. There is no ingress (nothing
receives a request), no write of any kind, no crypto, and no clock.

Take the canonical example — parse a signed webhook payload and return a
validated event. It needs HTTP *ingress* rather than `fetch`, a signature
primitive, a response or a store to write to, and a timestamp window. Only the
last of those is the genuinely open research question. The rest is vocabulary:
ordinary work of adding wireable capabilities and the primitives they need.

So the accurate statement of the gap is **not** "effects are unresolved". It is:
the effect discipline is real and proven, the vocabulary it speaks is three
read-only string functions, and time/interleaving remains open. That is a far
more tractable position than the first draft claimed, and it re-scopes #38 from
research into mostly-engineering plus one hard question.

**Escape hatches.** No FFI, no interop path. Adoption is all-or-nothing, which is
the opposite of what an agent needs when one piece of a task is missing.

### The structural tension

This is the part that determines the next phase.

> Proofs are cheapest exactly where code is easiest and least risky — pure
> algorithms. They are hardest where agents need the most help — I/O,
> integration, concurrency, external APIs.

A proof system left to its own momentum therefore grows along the **pure** axis,
because that is where the work succeeds. Demand sits on the **impure** axis.
Without deliberate intervention the system optimises the wrong frontier, and it
looks like progress the entire time: more definitions, more proofs, deeper
corpus — in the region of least need.

Stated at its most uncomfortable: Oath is currently a very good system for
verifying things agents do not yet need. The next phase is making it usable for
things agents already do.

### The re-prioritisation

**Priority is not expressiveness. Priority is applicability.**

The roadmap language corrects with it: not *"effects must be designed"* but
**"the effect discipline is shipped; the capability vocabulary must be extended
under demand."** That moves this work from language research to product
completion.

**Primary path** — completes the end-to-end loop (express → compile → execute):

1. **#38 — effects, re-scoped.** NOT "solve effects" — the discipline is shipped
   and proven. Two separable pieces: (a) **capability vocabulary** — ingress,
   writes, crypto, and the primitives they need; ordinary engineering, and the
   larger share of the value; (b) **time and interleaving** — the one genuinely
   open research question, and the only part that deserves the word "blocker".
2. **#13 — compiler backend.** Makes those artifacts executable in an agent's
   environment.
3. **#65 / #74 — discovery, full.** Makes them findable from intent.

**Parallel track, starting now** — *discovery v0 over the current corpus*.

Sequencing discovery strictly third was wrong, for a reason that took an argument
to see. There are two loops, not one. The **end-to-end loop** above is blocked on
effects. But the **credibility loop** an agent experiences first — search →
inspect → compare → trust — requires neither effects nor execution. It can run
against the 187 primitives we already have, and it should, because:

- It **validates the registry thesis while it is still cheap to be wrong.** "The
  registry is the product" is only true if querying and result-shaping work. Even
  a narrow corpus answers: does intent → spec mapping work at all? What does a
  decision package actually need to contain? How do you rank competing artifacts
  by evidence? That is design that cannot be deferred without being redone.
- It **forces the intent-versus-law gap into the open early.** Discovery v0 will
  show where translation fails, where specs are pitched too low-level to match
  intent, and where naming and structure are wrong. Build the richer system first
  and you risk building it on a misaligned discovery model.
- It **produces real value on a narrow corpus.** `oath find "stable sort of
  integers"` returning candidate implementations with their properties, mutation
  scores and proof coverage is already strictly better than a conventional
  registry *for that domain* — which proves the idea before the system is
  complete.

The constraint that keeps this honest: **discovery v0 must not outpace the
substance of the registry.** Leading with discovery alone would optimise UX over
substance and produce fancy search over trivial artifacts. It runs in parallel to
shape the primary path, not in place of it.

**And discovery v0 is the demand-sensing instrument.** This roadmap originally
assumed coverage would follow once effects existed. That is not guaranteed and
probably not true: coverage follows DEMAND signals, not capability. Discovery v0
is what records the queries agents make and fail to satisfy — which is the input
#75 needs to be demand-led rather than a guess. Without it, the coverage
programme repeats the very error this section exists to name, one level down:
building what is buildable rather than what is needed.

That loop — discovery → telemetry → coverage → discovery — has one failure mode
that must be handled or it never turns: **cold-start starvation.** Early on the
corpus is small, so discovery results are thin, so telemetry is sparse, so
coverage does not grow, so discovery stays thin. A rule that forbids guessing is
correct and can also refuse to bootstrap.

**Telemetry lives in SPEC-SPACE, not in natural language.** The obvious design —
capture intent text, aggregate it, apply a retention policy — makes privacy a
matter of policy, and policy is exactly what this project refuses to rely on
everywhere else. Intent is not neutral: an agent pastes whatever its task
contains, which can carry customer names, unreleased product direction, or
strategy. A registry whose entire proposition is "evidence you can check" cannot
also be a thing that watches what you were trying to do.

So the projection happens on the CLIENT. The agent turns intent into a partial
spec — a type shape plus property fragments — and what the registry receives is
the spec shape that failed to resolve, never the sentence that produced it:

    failed:  map<Str, Bytes> -> VerifiedEvent
             properties: signature-valid, timestamp-within-window

No customer names, no product context, and strictly MORE actionable than the
sentence: a spec shape is already a coverage request in the form the corpus is
indexed by. Privacy here is structural rather than promised — the registry cannot
leak raw intent it never received.

The honest cost, which must be stated because it is not free: the intent → spec
projection is the fuzzy step, it now runs where we cannot observe it, and so the
registry loses the signal that would tell us the MAPPING is failing rather than
the corpus. Diagnosing that needs a different, explicitly opt-in channel; it
cannot be smuggled in as a side effect of search.

The bootstrap clause, which weakens nothing: coverage is demand-led by discovery
telemetry; in the absence of sufficient telemetry, seed coverage is derived from
PROXY demand — canonical agent tasks, known integration surfaces, repeated
failure patterns — and every such seed is recorded as **provisional**, never
counted as observed demand. A provisional seed that never attracts real signal
once telemetry exists is evidence against itself and should be retired rather
than grandfathered, or "provisional" quietly becomes "permanent" and the guessing
this rule exists to prevent returns wearing a label.

And explicitly: **#69 refinement types remain correct and are not on the critical
path to adoption.** The design note (docs/refinements.md) settles a question that
had to be settled — refinement identity is syntactic, semantics layers above it —
and that decision stands. It is a language feature that deepens expressiveness
without moving applicability, so it sequences after the three above.

This is a deliberate reordering, not drift. The original vision is unchanged;
what changed is the recognition that sequencing decides whether the vision is
reachable.

### Consequence

Oath is not yet a general-purpose system, and the next phase is bridging into
reality rather than deepening theory. Success for this phase is measured by
firsts, not by counts:

- the first real integration with an external system;
- the first end-to-end executable artifact an agent could actually deploy;
- the first intent-driven discovery loop that returns a decision package —
  candidates, exact specs, provenance, dependency closure, proof status, known
  limitations — rather than a list of names.

Coverage becomes a deliberate, demand-led programme (#75) rather than a
by-product of whatever was easiest to prove. Tracking issues: #73 (this
reframing), #74 (discovery from intent), #75 (coverage).

## What belongs inside identity (2026-07-30)

Licensing forced a question the project had answered implicitly and never
stated: **which facts about an artifact belong inside its identity, and which
belong outside it?**

The decision is that licensing lives on the PUBLICATION layer, not in the
artifact graph. The general principle it settles is broader than licensing, and
is the reason this is an architectural record rather than a feature design.

### The principle

> **Content identity must not change when only human claims change.**

Structure determines identity; publications make claims about that identity.
Licenses, authorship, descriptions, repository URLs, support contacts,
deprecation notices and security advisories are all claims ABOUT an artifact or
a publication. None may change the artifact hash.

This is the same line already drawn for refinement types (identity is SYNTACTIC,
because semantic identity would make hashing undecidable) and for campaign
identity (a measurement is identified by its description, not folded into the
thing measured). Licensing is the third case, and stating the rule once is
better than deriving it a fourth time.

### Three layers that look similar and are not

| layer | made by | means |
|---|---|---|
| **artifact** | the kernel | "this is the content" |
| **publication** | the publisher | "I publish this artifact under this name, on these terms" |
| **registry evidence** | the registry | "these are facts I independently derived" |

A license belongs on the middle layer — not because it is "metadata", but
because it is an ASSERTION MADE DURING PUBLICATION. That is exactly where
authorship already lives, and the signed publication envelope (SPEC §8.6) is
already the mechanism for binding an author's claim to a specific transition.

### Why not an artifact the implementation depends on

The obvious design is a `license` artifact that code depends on, so the
dependency graph carries it. It was rejected, and the identity argument is
decisive: **if licensing changes the artifact hash, then legal assertions become
part of structural identity.**

Three consequences follow immediately, each of which is wrong:

- **Relicensing would invalidate downstream content hashes.** Apache-2.0 → BSL →
  Apache-2.0 would fork identity three times while the code never changed, and
  every downstream pin would break on a legal decision.
- **Dual licensing would be inexpressible.** "MIT OR Apache-2.0" is one artifact
  with two grants; as a dependency it becomes two different objects with
  identical code, contradicting the invariant that structurally identical
  definitions content-address to the SAME object.
- **Identical code would stop being identical code**, depending on how someone
  later chose to publish it.

Licenses are also many-to-many in ways code is not: `MIT OR Apache-2.0`,
`GPL-3.0 WITH Classpath-exception`. Those are publication TERMS, not properties
of the bytes. Keeping them outside identity makes that natural rather than
something the type system has to be bent around.

### Licensing itself has two layers, and they are the ClaimedGuarantee split

**Publisher assertion** — "I license this publication under Apache-2.0." Signed,
historical, immutable. The legal intent stays with the author.

**Registry evaluation** — "given these dependency licenses, this publication
satisfies policy X." Derived, recomputed, and versioned as the policy engine
improves.

This is precisely the split the registry already makes between a claimed
guarantee and a re-derived one. The publisher asserts; the registry derives
compatibility, inheritance through the dependency closure, conflicts, and
whether a composition satisfies a requested policy.

### The mechanism already exists

Nothing here needs new machinery, which is a good sign the layering is right:

- the **signed envelope** already binds author, name, artifact and parent, so a
  license assertion is signed publication evidence by construction;
- **license history** is a sequence of name transitions. Apache → BSL → Apache is
  unambiguous because `parent` and `parent_rev` distinguish the second Apache
  publication from the first — the ABA protection built for replay defence turns
  out to be exactly what a licensing history needs;
- the **dependency closure is already exact and by hash**, which is what makes
  transitive license evaluation possible at all. This is the part a
  repository-centric platform cannot do: it stores code and attaches a license,
  but has no verified dependency graph to reason over.

### Vocabulary: not PROVEN

License compatibility over a finite lattice of grants is **decidable by
evaluation**, not proved over unbounded inputs. It is a derived verdict of the
same kind as `termination` or `confinement`, and it MUST NOT reuse `PROVEN`,
which in this system means machine-checked by an SMT solver over all inputs.
Overloading that word here would be the same overclaim the authorship ladder
exists to prevent. A separate vocabulary — LICENSE EVALUATION: compatible /
incompatible / undetermined — keeps the two apart.

### The failure mode that is different in kind

A wrong `tested` verdict costs a reader a bug. A wrong "commercial use: yes"
costs them a lawsuit.

So the machine semantics must be visibly a MODEL — contributed, reviewable,
versioned and fallible — never an authority. SPDX supplies identifiers, not
semantics; the semantics layer is Oath's own and carries Oath's own
fallibility. That belongs on the face of any surface that reports it, not in a
footnote, and it is the one place in this system where the honest-limitations
discipline protects against legal harm rather than technical error.

The corollary, and the answer to most of the policy questions in #97: a
signature is provenance, a proof is evidence, and a registry entry is
publication — not a licence, and not a transfer. Each is a different claim, and
the whole discipline of this project is not conflating claims.

## Independent implementability as a measured property (2026-07-30)

Conformance testing asks whether an implementation agrees with the vectors.
After three blind implementation rounds we are also measuring something else,
and it turned out to be the more useful question:

> Could an independent implementer build this honestly from the published
> surface alone, without hidden knowledge?

The two are not the same, and passing the first says almost nothing about the
second. Every blind round so far has followed the identical progression: the
implementation passes, the vectors pass, and the implementer independently
reports that it could not have built the thing honestly from the prose. A
specification whose reader must infer rules from fixtures has already failed —
however green the suite — because the NEXT implementer is not guaranteed to
infer the same rules.

**The fixes this finds are almost never algorithmic.** Across three rounds they
were: a field participating in identity with no stated meaning (`policy`); an
encoding contradicting a rule the same section had already stated; a lattice
determining every verdict while living outside the normative surface entirely;
and an empty-input case inheriting an unsafe algebraic identity. None is a
missing algorithm. Each is a place where the protocol was not independently
reconstructible, and none is visible to a conformance suite — because a
conformance suite is written by someone who already has the answers.

**Why it is a property of the SPECIFICATION, not of either implementation.**
Two implementations can agree perfectly with each other and with every vector
while both disagree with the document, or while the document alone determines
neither of them. That is the class `check-spec-vs-fixtures` closes for formats;
§13 closes it for meaning.

SPEC §13 defines the measurement, three verdicts (`PASS` /
`PASS-WITH-INFERENCE` / `FAIL`), and what a claim binds. Two design points carry
most of the weight:

- **A passing score is not evidence, and is stated normatively not to be.** An
  implementer who tries a reading and keeps whichever one passes has shown the
  FIXTURES are sufficient, which was never in question. This deliberately
  inverts the usual reading of a green suite.
- **A claim binds a SURFACE, not a commit.** "Implementable" is a statement
  about an exact set of supplied bytes; the same commit exported with one extra
  file can turn `PASS-WITH-INFERENCE` into `PASS` without the document changing.
  The surface digest is recomputable from the source, so that half of every
  claim is machine-checked rather than asserted — the same
  published-and-pointed-at discipline §12.3 needed one layer down.

The verdict itself is attested by the implementer and cannot be re-derived by
CI: re-running the experiment is the only reproduction, and a subject who has
seen the answers is no longer a valid subject. The gate is therefore split
explicitly into machine-checked, structurally-enforced, and attested parts.
Claiming to verify the verdict would be the same error the ledger exists to
catch.

**Current state, recorded rather than advertised: no run has reached `PASS`.**
`docs/implementability.json` is the ledger; the gate prints that fact on every
successful run.

The granularity keeps refining under measurement, and that is the measurement
working rather than failing. We began scoring RULES; a rule turned out to
contain several independent obligations, so we now score CLAUSES. Expect clauses
to contain independent semantic obligations next. Each refinement is the
measurement revealing that the specification's granularity was too coarse to
express what it was promising.

## Why the kernel is written in a human language

The kernel is the root of trust — the one component that cannot be verified
by itself and therefore must be audited by humans. It belongs in a boring,
readable human language and should stay small enough to read in an afternoon.
Everything *above* the kernel — stdlib, tooling, application code, the
millions of lines nobody can hand-audit — is what belongs in Oath.
