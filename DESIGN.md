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

## Examples are witnesses, not proofs (2026-08-01)

> Examples CONSTRAIN. They do not CHARACTERISE.

§10.0a defined a filename encoding and demonstrated it on `Map`, `map`, `_map`.
Every example was correct. The rule still failed to achieve its stated goal,
because the examples covered ASCII and the goal was stated over all names — so
`Élan` and `élan` still collide, which is the exact defect the encoding was
introduced to fix.

The worked example is a witness of the rule on three inputs. It says nothing
about the domain the prose claims. That is the same relationship as a fixture to
a rule, a vector to a clause, or a campaign to an invariant: each demonstrates an
instance and none establishes the property.

### The sharper form: goal ≠ construction

The section stated a GOAL — portable injective filenames on case-insensitive
filesystems — and shipped a CONSTRUCTION that delivered something narrower:
injectivity over ASCII case distinctions. Both were correct in isolation. Nothing
compared them, because the examples matched the construction and the prose
described the goal.

**A specification that states a goal must say which construction achieves it and
over what domain, or the reader will assume the construction reaches the goal.**
The honest repair was not to solve Unicode — case folding is neither injective
(U+212A and `K` both lower to `k`) nor length-preserving, and filesystems disagree
about normalisation — but to state what is guaranteed, over which alphabet, and
why the gap exists. A specification that says "injective for this alphabet" is
stronger than one claiming universal portability it cannot deliver.

### What this makes blind rounds for

Round 6 reduced OVERCLAIM, not bugs. The implementation was complete and reached
187/187; what it found was that a mathematical word ("bijection") was stronger
than the construction justified, and that a stated purpose exceeded what the rule
delivered. Those are the hardest defects to find from inside — an author reading
their own text supplies the intended meaning for free — and in a specification
they are the ones that matter most, because every downstream reader inherits the
overclaim.

## A specification must not claim a stronger property than its construction establishes (2026-08-01)

The shortest form of what six blind rounds found, and the one to keep.

| the claim | what was actually established |
|---|---|
| "the encoding is a bijection" | an injection; decode is partial |
| "portable filenames on case-insensitive filesystems" | injectivity over ASCII case distinctions only |
| "186 canonical fixtures byte-identical" | 186 of the files that survived generation agreed |
| "the client derives byte-identical identity to the server" | it shared the elaboration and not the step after it |
| an evaluation identity | bound a method and its members, never the subject |

None of these is an implementation bug. Each is a place where the PROSE OUTRAN
THE EVIDENCE — and each was invisible to the author, because reading your own
text supplies the intended meaning for free.

### The rounds changed what they were finding

The progression is the maturation, and it is worth stating because it predicts
what a later round is for:

- **rounds 1–2** — the specification did not say enough
- **rounds 3–4** — it said enough, but not consistently
- **rounds 5–6** — it said MORE than it had established

The target stopped being completeness and became CALIBRATION. Missing information
is easy to find by trying to implement; excess confidence is not, because a
complete implementation is compatible with an overclaiming specification. Round 6
reached 187/187 and still disproved the hypothesis.

### Why no round has reached PASS, and why that is the honest reading

The ledger records six rounds and no PASS. That is not the methodology failing to
converge — it is the bar staying fixed while the document improved. A PASS
appearing in round two or three, while the specification was still changing this
fast, would have been evidence the experiment had become accommodating.

The PASS to expect is not "nothing more can be found". It is the point where
findings stop changing what the specification MEANS and become editorial. We are
not there, and the ledger says so.

**But that is the wrong stopping criterion for the ROUNDS**, and it took an
outside reading to see why: it can be satisfied by ever-finer wording, so it
never terminates. The criterion that does:

> **Blind rounds stop when they stop changing what users can do.**

By that measure the last three rounds had already stopped. Round 4 found
contradictions introduced by repairs; round 5 found a new defect class in
witnesses; round 6 found overclaim in a rule written the same day. All real, all
valuable — and all improvements to the METHODOLOGY rather than to what a
publisher, verifier or registry operator can accomplish. The rounds had begun
discovering their own unknowns rather than the project's, which is what it looks
like when a research subject becomes infrastructure.

Licensing becoming deployable, ownership becoming cryptographic, and the registry
becoming signed rather than an unsigned catalogue are the changes that met the
bar. §10.0a's calibration did not.



### The discipline that made it work: metrics were allowed to get worse

Repeatedly, a number moved the wrong way because the measurement improved —
witness coverage gaining a larger denominator, the envelope inventory growing
while witnessed obligations stayed flat, the fixture matrix getting stricter,
§10.0a now claiming less than it did. Optimising the score and optimising what
the score MEANS are different objectives, and only the second survives contact
with an independent reader.

### What all of it reduces to

**The project learned to distinguish evidence from explanation.** Every principle
here follows from that one distinction: history stores evidence rather than
derived facts; identities bind their subject rather than their intended meaning;
witnesses do not define their own coverage; examples constrain but do not prove;
and a specification claims only what its construction establishes.

## What eight rounds actually found, in order

The findings changed KIND as the specification matured, and the progression is
the clearest evidence that the rounds were measuring something real rather than
generating work:

| rounds | the defect class | what was wrong |
|---|---|---|
| 1-3 | OMISSION | the specification left information out |
| 4-6 | OVERSTATEMENT | the specification claimed more than it established |
| 7-8 | INCOMPLETE ONTOLOGY | every object was present; not every RELATIONSHIP was |

By the end the work was not fixing wording. It was completing a conceptual model:
round 7 found objects used but never defined, and round 8 found the last missing
EDGE — authority was fully specified as a thing and never connected to the act it
governs.

That progression is also why stopping is correct. Not because a ninth round would
find nothing; it almost certainly would. But the remaining work has shifted from
DISCOVERING the protocol to BUILDING AROUND a protocol whose conceptual model is
now stable, and those need different effort.

## Tests cannot define semantics

The sharpest thing round 7 taught, stated so it survives outside the round record:

> **A vector cannot witness an undefined object. It can only freeze one
> implementer's guess about it.**

This is the specification analogue of a classic testing mistake. Tests constrain
an already-defined semantic object; they cannot BE the definition. When a test
becomes the first place a behaviour is decided, it stops being a witness and
becomes executable prose — and executable prose is the worst kind, because it is
read as evidence rather than as a claim.

The practical consequence is an ordering rule. On finding a rule with no witness,
the reflex is to write the vector. That is right only when the object the rule
operates on is DEFINED. Where it is not, writing a vector actively harms the
specification: it manufactures agreement between the reference implementation and
a fixture generated from it, while the prose that a third party must read remains
silent. Both then pass, and the gap is now invisible.

Define the object, then witness the rule. Never the reverse.

## The methodology became subordinate to the protocol

Early in the §8.7 work the blind rounds drove the project: the question being
answered was "what else can the methodology find?", and every answer generated
more methodology. By the end of it the relationship had inverted, and the
inversion is visible in a list of things that were REFUSED rather than a list of
things that were built:

- the section under test was not edited while a round was reading it;
- the pre-registration was not edited once dispatched;
- the prediction was not reinterpreted after partial results;
- the supplied surface was not changed;
- the harness was not improved in ways that would invalidate historical evidence;
- a limitation that could not be fixed was DECLARED rather than worked around.

Each refusal costs something concrete — a better document, a cleaner harness, a
more favourable result — and each is the correct trade, because the alternative is
a measurement that flatters whoever is holding the instrument.

That is the stopping condition, arriving in a form that was not anticipated when
it was written. The criterion recorded earlier was behavioural: rounds stop when
they stop changing what users can do. The signal that actually appeared is
POSTURAL: the methodology stopped seeking work for itself and started acting as a
release gate — refusing to move, refusing to reinterpret, refusing to grade its
own homework.

A research programme asks what else it can discover. A gate asks whether this
change is ready. The second question has an answer that can be reached, which is
the entire difference.

The intended use was written into CLAUDE.md long before it was achieved: blind
implementation as a DEFINITION OF DONE for new normative text, not an activity.
Round 8 is the first time it was actually used that way — one round, scoped to
one section, against text written for a shipping feature, with the outcome
deciding whether to wire it rather than whether to keep looking.

## Acting on a finding must not destroy the finding

Three instruments needed the same repair before the pattern was named: the blind
exporter, the protocol kit, and the ledger that checks both. Each time the shape
was identical, and each time it looked like a one-off.

A round finds that the harness leaked; the harness is fixed; the round's recorded
surface digest stops reproducing. A round finds a section used objects it never
defined; the section is repaired; the kit built from that section stops
reproducing. In both cases the evidence was invalidated BY THE ACT THE EVIDENCE
EXISTED TO PROVOKE.

A gate that fails in that situation is worse than no gate. It makes the cheapest
way to stay green "do not fix what you found", which inverts the entire purpose,
and it does so quietly — the failure looks like a integrity violation rather than
like a design error in the check.

The rule, now normative as IMPL-REPRODUCIBLE-INSTRUMENT: every experimental
artifact must be reproducible from the historical commit that produced it. Not
from current sources, and not from current tooling. The generalisation is that a
digest is a measurement, and a measurement is only meaningful relative to its
instrument — so pinning the surface is not enough if the thing that computes the
surface can move.

The practical form is small: every tool that produces evidence takes a commit,
and every claim records which tool and which commit. The cost of adding that up
front is minutes. The cost of discovering it three times is that two intervening
rounds recorded evidence nobody could re-derive.

## A fifth defect class: objects with no ontology

Round 7 found five defects in one section, and their value is not that there were
five. It is that every one is the same question:

> **What IS this thing?**

- what is AUTHORITY — a public key, a record, a publication, a hash? The
  compare-and-swap compares it; nothing defined its domain.
- what is an AUTHORITY REVISION — a counter, a journal position, a version, a
  logical clock?
- what is the ORDERING? "First reservation wins" is not a total order until
  "first" is defined.
- what is the state machine REPLAYING? The algorithm existed before the alphabet.
- what is EXACT-NAME OWNERSHIP? Two rules depended on it; it was never an object.

This is a distinct class, and it is not the one round 5 found. Round 5 asked
whether an identifier had a SUBJECT — identity of what? Round 7 asks whether the
subject EXISTS AT ALL. An identity with no subject names something unspecified; an
object with no ontology is a term the specification uses fluently and never
introduces.

The failure mode is characteristic and is why field-level review misses it: the
prose reads as complete. Every rule mentioning the object is well-formed, the
rules are mutually consistent, and each sentence is individually checkable. What
is missing is not a sentence but a DEFINITION, and prose does not have a hole
where a definition should be — it simply proceeds.

The structural fix is equally characteristic. Five scattered defects did not get
five scattered patches; they got ONE new subsection that introduces the objects
before any rule uses them. If repairing a defect of this class does not produce a
definition, the repair has not addressed the class.

Two diagnostics, both cheap:

- **Can a reader enumerate the value space of every member an identity or a
  comparison COMPARES?** If not, the comparison is undefined regardless of how
  precisely the comparing is described.
- **Does an algorithm mention a vocabulary the document never enumerates?** A
  replay rule over unenumerated statuses is an algorithm without an alphabet, and
  the safe reading is never the obvious one — excluding known failures and
  counting the remainder FAILS OPEN.

## Evidence must never define itself (2026-08-01)

One sentence, three levels:

> **Identity** cannot derive its own subject.
> **History** cannot trust facts it exists to verify.
> **Witnesses** cannot define their own coverage.

Nearly every defect found this month is a violation of it, across subsystems that
share no code:

| violation | where |
|---|---|
| a campaign validating itself | namespace migration: 8 sound publications of the wrong objects |
| a conformance inventory defining its own denominator | rule scoring counted rules it knew about |
| fixtures defining their own completeness | 186 files for 187 definitions, all checks green |
| a stored transition defining the history it describes | `name_transition` read rather than derived |
| an identity omitting the subject it identifies | §12.4 bound a method and members, never the artifact |
| coverage measured from surviving files | conformance check 1 enumerated the directory |
| client and server each elaborating, assuming agreement | #101, signed artifact ≠ stored artifact |
| a test asking the filesystem the question it was testing | `[ -f _Map.bin ]` succeeds when only `_map.bin` exists |

They look like seven unrelated bugs. They are one structural mistake: **letting
the thing being measured supply part of the measurement.**

That framing is worth more than any individual repair, because it predicts where
to look. Any place an artefact reports on itself is a candidate — a count derived
from the writer rather than the result, a verdict trusted from the party being
verified, a check whose scope comes from the material it is checking.

### Why the discipline is now a tool rather than a subject

The implementability methodology (§13) was worth building and is worth stopping
work on. It generalised past the section it was developed against, produced a
defect class nobody was looking for, and documented its own boundary — and it now
yields protocol and implementation improvements rather than better methodology.
That is the sign it has done its job. Use it when changing normative text; do not
keep sharpening it.

### The one thing to protect

**The Rust kernel must stay genuinely blind.** It has found protocol defects,
specification defects and witness defects, and #103 will test whether a normative
encoding written this week is implementable from prose at all. Every time the
temptation to "help" it by copying obvious logic was refused, the result was a
lesson about the SPECIFICATION instead — which is the entire return on having a
second implementation. Copying into `oathrs/` converts an independent oracle into
an expensive mirror.

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

- **Proven, but not for everything.** `oath prove` discharges properties via Z3
  over unbounded integers — now the kernel's actual `Int`, which is arbitrary
  precision, so proofs hold with no overflow caveat. Recursion is INSIDE that
  reach rather than beyond it: a recursive function PROVEN TOTAL enters the SMT
  problem as a quantified defining equation — one whose termination is unproven
  is deliberately left uninterpreted, because asserting an equation for a
  possibly-diverging function can make the context inconsistent and discharge
  anything. A goal that no direct attempt settles is
  tried by structural induction, by lexicographic induction on a pair of
  binders, and by recursion induction for functions driven by an integer
  counter. Proven properties then serve as lemmas in later proofs. What stays
  outside is named rather than hidden: a construct the translator does not
  support, or a goal no strategy discharges, bails with a reason and the
  definition remains `tested`; `examples/undertested.oath` shows why the
  distinction matters (200 cases passed, still unproven in both kernels — the
  README carries the per-kernel detail).
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
  induction with a relevance-filtered lemma library (§7.2): 129 definitions
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

**Coverage.** 238 definitions, deep but narrow: list, sort, tree, queue,
interval, map, set, string, rational, plus honest exhibits — and, since #120,
one APPLICATION (`apps/github-webhook`) that a system outside the project
actually calls. Those twenty-one definitions are the first entries in this
corpus that correspond to a task someone would be given rather than to a
structure someone would teach, and what they cost is recorded in
`docs/experiments/webhook-friction.md` rather than smoothed away.

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

## What a licence evaluation is about (2026-07-30)

Evaluation identity binds the ARTIFACT CLOSURE, not the names that located it.
Names are provenance and presentation.

The question was whether an evaluation is about publications or about software.
Oath had already answered it everywhere else: `explain` carries a name but
explains the artifact reached at that hash, and every derived verdict —
guarantee, termination, properties, spec strength — is a hash-keyed fact.
Licensing was the lone exception, so this is not a special identity rule for
§12; it is §12 rejoining the pattern. Binding names produced a property with no
defensible reading: rename `app` to `service`, change no artifact and no
assertion, and the evaluation identity moved — the same defect as making artifact
identity depend on a repository path.

But artifact alone over-corrects, because licensing is a PUBLICATION claim rather
than a property of the code. The same artifact can carry different assertions
under different publications, and the same expression asserted by two principals
is two different grants over the same bytes. So an input is a triple: artifact
hash (which code), publication identity (whose grant — the §8.2.2 entry digest),
asserted expression (what terms). The mutable name stays outside identity as
recorded provenance.

This is the same separation that keeps paying out across the project — identity
vs publication, publisher assertion vs registry derivation, authorship vs
custody, implementation vs evidence — rather than a new principle.

## Two review disciplines the blind rounds produced (2026-07-31)

Neither is about licensing. Both came out of §12 and both generalise to any
specification work here.

### Search the class, not the occurrence

When a blind round names a defect, the follow-up must not be "fix this
occurrence". It must be "search the entire normative surface for siblings".

Round two found `policy` was a required component of a normative identity with
no vocabulary, no default and no stated source. It was repaired. Round three
found `engine` — one line above, in the same encoding, with the identical
defect. Nobody had looked. Fixing one instance of a defect class without
searching for the rest is how the class survives, and a checklist derived from
the first instance will not find the second because it was written from the
first.

The mechanical half of this one is now `check-normative-source`, which asserts
every literal in every identity encoder appears in the spec — the general form of
the undocumented-`unpublished` defect. Building it immediately found that the
gate itself was under-measuring: it understood `WriteString` but not `Fprintf`,
so seven of `campaignEncode`'s eight keys were unchecked while it reported "ok".
The discipline applies to the tools that enforce it.

### Copy the reason, not the rule

Three rounds in a row, §12 inherited the LETTER of a rule from §8.6.1 or §11.2
without its RATIONALE:

- §8.6.1 excluded control characters from envelope values because an embedded LF
  injects a line. §12.4 got the character rule later, and only after a
  demonstrated forgery.
- §8.2.1 escapes U+2028/U+2029 BY NAME because a Unicode-aware line splitter
  treats them as line terminators. §12.4's exclusion was phrased in terms of
  `0x20`/`0x7F`, which admits both — the same forgery, one code point up.
- §11.2 hashes the waiver SET rather than a waiver-set version, so the content
  cannot change beneath a fixed name. §12.4 bound `model=<version string>` and
  let the lattice be edited under it.

Reviewing an inherited rule by asking "did we copy the rule?" passes every one of
these. Asking "did we copy the REASON this rule exists?" fails all three
immediately. The second question is the one to ask, because a rule transplanted
without its rationale is re-derived by the next reader from its wording alone —
and its wording is exactly what was tuned to the original hazard.

## OPEN: what an evaluation is over (2026-07-31)

DESIGN ONLY — no code, deliberately. Raised by round three and not patched,
because it decides what a licence evaluation IS rather than fixing an omission.

§12.4 binds `input-publication` (an entry digest) beside `input-license` (an
expression), and nothing requires the second to be what the first actually
asserted. A registry can name a real publication next to a false expression; the
identity verifies perfectly on any conformant kernel and reports whatever grant
it likes. The digest proves the registry hashed what it said it hashed. It does
not prove those were the publishers' terms.

Two models, and they are not equivalent:

**A — the evaluation is over CLAIMED TERMS.** The triple is what the evaluator
consumed, self-attested. The digest makes an evaluation reproducible and
comparable; whether the inputs were faithful is a separate audit against the
journal. Cheap, local, and honest about what it proves — but "whose grant" is
then a reference the identity never checks.

**B — the evaluation is over CONTAINED TERMS.** `input-license` MUST equal the
`license` field inside the envelope whose entry digest is `input-publication`,
and an evaluation that cannot demonstrate that is not conformant. This makes the
publication binding mean what §12.4's prose already claims it means — but it
requires the evaluator to read the journal, turns evaluation into a verification
of publication history, and raises what a conformant kernel does when the
referenced entry is unavailable.

The tell that this is unresolved rather than merely unimplemented: §12.4 argues
that binding the publication captures "whose grant is being relied on", which is
model B's claim, while the encoding implements model A. That gap is the same
shape as every defect these rounds have found — the prose asserting a property
the encoding does not deliver — so it should be settled in text before anything
is written.

## Where the methodology stops: specification engineering vs protocol design (2026-07-31)

Five blind rounds produced two kinds of finding, and only one of them is
something the method can fix.

| SPECIFICATION ENGINEERING | PROTOCOL DESIGN |
|---|---|
| missing meaning | what is a transition? |
| hidden normative inputs | what constitutes publication identity? |
| internal inconsistency | what exactly is signed? |
| incompletely defined objects | what is a durable historical fact? |
| defective witnesses | |
| *the method systematically finds and closes these* | *someone has to choose the semantics* |

The distinction matters because it narrows what the implementability process
claims. It does not tell you how to design a protocol. It tells you WHERE the
protocol has not yet made a decision — which is a smaller and far more useful
role, and one that stops the method from being driven past its competence.

The tell is uniform: every question round five left open asks *what belongs in
the immutable historical record*, not *what should the implementation do*. Should
a transition be trusted or derived? Must `parent_rev` be persisted? What
constitutes an Ed25519 publication? Is publication identity stable under
alternate base64 spellings? §8.6 stopped being about envelopes and signatures
somewhere along the way and became a question about what makes a historical fact
durable.

**Consequently §8.6 will not be driven to green by further blind rounds, and
should not be.** The remaining blockers are not implementability failures. A
section whose semantics are undecided will fail an implementability round for a
reason the round cannot repair, and running one anyway would measure the same
four open decisions repeatedly while looking like progress.

## The architecture as a decision procedure — and a gap it exposed (2026-07-31)

The three layers are more useful as a PROCEDURE than as a description. When
someone proposes adding a field, the first question stops being "is it useful?"
and becomes "which layer does it belong to?":

- an author ASSERTION → preserve it, because nothing can reconstruct it;
- DERIVABLE from history → do not store it, and re-derive on every verification;
- affects IDENTITY → pin the equivalence relation, versioned and explicit.

### Testing the procedure found a fourth category

Applied to `time`, the procedure gave the wrong answer, and forcing `time` into
either existing bucket was the wrong repair. `time` is written by the registry
(`time.Now()`), is NOT inside the signed envelope, and history cannot reconstruct
it. It is neither an author assertion nor a derived fact. It is an OBSERVATION.

> An observation is a fact known only by the actor performing the event, asserted
> by no participant and reconstructible by nobody afterwards.

That makes the test sharper than "can history reconstruct this?":

> **Who was the only party capable of knowing this at the moment it happened?**

| field | who knew it | verdict |
|---|---|---|
| `parent_rev` | the author (and the registry) | preserve the AUTHOR'S SIGNED statement |
| `name_transition` | nobody intrinsically — it is computed | DERIVE |
| `time` | only the registry, by performing the acceptance | preserve as a REGISTRY OBSERVATION |

Crucially this does not reintroduce self-certification. The registry is not
saying "the transition was applied", which history can check and therefore must.
It is saying "I observed this request at this instant", which history cannot
check and nobody else could ever supply.

### The record model, in four categories

| category | contract | why |
|---|---|---|
| ASSERTIONS | preserve | nobody else can recreate a participant's claim |
| OBSERVATIONS | preserve | nobody else can recreate the event |
| DERIVED FACTS | never preserve | everybody can recreate them |
| EQUIVALENCE | pin | nobody can derive a convention |

Assertions: `artifact`, `parent`, `parent_rev`, `license`, the signed author
identity. Observations: the acceptance timestamp, the authenticated principal.
Derived: transition, revision, ownership, guarantees. Equivalence: Ed25519
validity, canonical encoding, base64 identity.

### An observation MUST be labelled as one

This follows directly from the distinction and is the part most likely to be lost
later. An unlabelled observation gets read as an assertion by the next person.

The registry timestamp is not "the publication time". It is *the time this
registry claims it accepted the publication*. The registry is authoritative about
its own observation and about nothing else — not about universal time, and not
about anything a third party could contradict. A verifier cannot check it, so a
reader must not be invited to treat it as checked.

The same distinction already exists in the corpus without having been named: an
unsigned entry's `author` is the registry's OBSERVATION of who it authenticated,
while a signed entry's author is an ASSERTION inside the envelope. `explain`
already says as much for the first case — "a principal string the registry
recorded on an unsigned entry, so who may repoint this name is NOT independently
checkable" — which is exactly an observation being labelled as one. The model was
being applied before it was written down.

## Four concerns that now evolve independently (2026-07-31)

A stable protocol means these stop moving together, and keeping them apart is a
deliberate act rather than an emergent property:

| concern | example | changes when |
|---|---|---|
| PROTOCOL | signed licensing, the journal record model | the normative document changes — and only with §13's guardrails applied |
| REGISTRY implementation | background verification workers, store backends | operations change; no normative consequence |
| AGENT WORKFLOW | blind dispatch, subagent orchestration | how work gets done changes |
| UI / rendering | `explain` output, the website | presentation changes |

Earlier in the project these evolved together, which is why "the registry needs
feature X" was once a sufficient argument for changing the protocol. It no longer
is. The question for any proposal is which of the four it belongs to, and only
the first one alters what a conformant implementation must do.

That the deployed registry now LAGS the protocol — it still writes the
pre-`parent_rev` format and consumes the transition it should derive — is
evidence the separation is real. Updating it will be implementing the protocol
rather than redefining it, which is the first time that direction has held.

## What humans are left reviewing (2026-07-31)

The project started from: if AI writes most software, what should humans review?
The answer got sharper than "evidence over code", and it mirrors the three
layers:

- **assertions** — what is being claimed, and by whom?
- **evidence** — what can be independently re-derived?
- **conventions** — what definitions of identity and equivalence did we choose?

Implementation is not on the list. It becomes an optimisation problem, checkable
by machine against the three above. What remains irreducibly human is deciding
what may be claimed, what counts as proof of it, and what counts as the same
thing — which are the same three questions the protocol layers answer, arrived at
from the opposite direction.

## The evidence does not define its own denominator (2026-08-01)

> Coverage must be enumerated from the SUBJECT being covered, never from the
> witnesses that happened to survive generation.

The fixture corpus shipped 186 canonical files for 187 definitions, and every
check passed. Two defects attacked one assumption from opposite sides — that the
fixture DIRECTORY was the source of truth for what existed.

**Representation.** `map` and `Map` folded onto one inode on a case-insensitive
filesystem, so one definition's bytes were absent and the survivor had silently
overwritten the other. Fixed by an encoding that is injective and reversible
(§10.0a), so one logical name maps to one portable filename and Linux and macOS
generate the same set.

**Measurement.** Both the generator's report and conformance check 1 enumerated
`canonical/*.bin`. A missing fixture was therefore not compared, not counted, and
could not fail. "186 byte-identical" was true and useless: *all files present
agree* had been allowed to masquerade as *all corpus objects are covered*.

The second is the reusable half. A denominator taken from the witnesses can only
ever report that the surviving witnesses agree. The corpus is the subject; the
fixture tree is a derived representation, and it must prove it holds one distinct
canonical file per corpus object.

A related distinction the fix turns on: the generator now counts what LANDED ON
DISK, not how many writes it issued. **A write counter measures operations; the
question was about resulting state**, and one file overwriting another is
invisible to the first and obvious to the second.

This is the fixture analogue of the rest of the session's findings — a
conformance score computed from its own rule inventory, a census that could never
be all-PASS, a campaign checked only by its own output. In each, the artefact
under test supplied the measure of its own completeness.

## A transformation campaign needs an EXTERNAL equivalence invariant (2026-08-01)

> Internal success only proves the output is self-consistent.

The namespace migration rewrote 187 definitions and published them signed. The
campaign reported zero failures. Every publication succeeded, every property
passed, every signature verified, every artifact satisfied its own specification.

Eight of them were the wrong objects.

A paren-anchored rename missed self-references to NULLARY constants, which are
written as bare tokens rather than calls — `(== (map-lookup k map-empty) …)`. The
namespaced definition's property therefore referenced the BARE definition instead
of itself, turning a self-reference into an external dependency and changing the
artifact. Sound publications of objects nobody intended.

Nothing internal could detect this. The output was valid by construction: it
elaborated, it verified, it was signed for. What caught it was an invariant
checked from OUTSIDE the operation, derived from what the transformation CLAIMED
to be:

    hash(namespaced rewrite) == hash(bare original)

because the transformation was name-only, and names are metadata. 176/184 proved
the campaign unfaithful while every local check was green; 184/184 closed it.

**The rule generalises to any mechanical rewrite.** State what the transformation
preserves, express it as a comparison against something the transformation did
not produce, and run it as a postcondition. A campaign that can only be checked
by its own output can only tell you it did something consistently.

Two secondary lessons:

- **Text matching around `(` is brittle because reference position is
  GRAMMATICAL, not textual.** The same regex over-matched (`parse-nat` also
  matched `parse-nat-go`, renaming a different definition's call site) and
  under-matched (missing bare-token references). An AST-level rewrite has neither
  failure mode. The over-match failed loudly and was fixed in minutes; the
  under-match was silent and survived to the registry.
- **Loud failures are cheap; silent ones are the ones to design against.** The
  campaign's one reported failure cost a retry. Its eight unreported ones cost a
  full re-derivation of the invariant to find.

## Canonical bytes without semantic identity (2026-08-01)

> A representation does not need to participate in SEMANTIC IDENTITY to require
> CANONICAL BYTES.

Artifact hashes protect semantic identity. Canonical store bytes protect
reproducibility, reviewability, and clean no-op behaviour — different properties,
and the second set does not follow from the first.

`codebase/meta/*.json` is the case. Metadata is not hashed into artifact
identity, so a formatting difference changes nothing semantically. It still
mattered: there was no single writer, the committed corpus held two encodings,
and touching any object rewrote its record with identical content and different
bytes. A no-op update produced a diff, and the store could not be reproduced by
the kernel shipping with it. Now one `encodeMeta`, and `oath store-check` gates
that every committed record is exactly what it produces.

### The correction that got there

The first diagnosis was backwards, and the process of getting it right is the
reusable part.

I sampled ONE metadata file, found it compact, and reported that the committed
store was compact while the kernel emitted indented. A decision was taken on that
basis — make compact canonical, avoid a 187-file migration. Counting instead of
sampling gave the opposite picture: **174 indented, 23 compact**, with the kernel
already matching the majority. Both encodings arrived in a SINGLE commit, a
corpus rebuild that rewrote records for the objects it re-put and left the rest
at their older encoding. The split was residue, not a decision, and the migration
was 23 files rather than 187.

The stated reasoning had been sound — minimise churn, preserve blame, prefer the
established format — and applying those same criteria to the corrected facts
reversed the conclusion. Which is the point: the reasoning survived, the premise
did not, and only counting could tell them apart.

## The epistemic contracts (2026-07-31)

Each layer has its own contract, and — the part that makes the taxonomy sharp —
its own failure mode. They are not variations of one mistake:

| layer | contract | failure mode | what goes wrong |
|---|---|---|---|
| 1. HISTORICAL ASSERTIONS | preserve what only the participants knew | **evidence is LOST** | information destroyed at admission and unrecoverable |
| 2. DERIVED FACTS | trust nothing history can recompute | **evidence is INVENTED** | self-certification: the party being checked supplies the check |
| 3. EQUIVALENCE | never leave equality to an implementation | **evidence is SPLIT** | permanent fork; two correct implementations disagree forever |

Layer 1 examples: the author's statement, `parent`, `parent_rev`, publication
digest, signatures. Layer 2: transition, revision counts, guarantees, ownership
state. Layer 3: the Ed25519 verification equation, canonical `S`, point encoding,
base64 identity.

The discoverability asymmetry is what tells you where blind implementation stops
being the right tool:

| layer | mechanically DISCOVERABLE? | mechanically CHECKABLE? |
|---|---|---|
| historical assertions | yes | yes |
| derived facts | yes | yes |
| equivalence | **no** | yes — that a convention is PINNED, never that it is right |

Layer 3's check is genuinely weaker and should not be presented as equal to the
others. You cannot mechanically verify that a convention is correct; you can only
verify that one was chosen (IMPL-EQUIVALENCE-PINNED). Layers 1 and 2 have a test
— could history reconstruct this, and who computes it — and layer 3 has none,
which is exactly why the blind rounds surfaced its questions and could not settle
them.

### What the project turned out to be about

Every difficult design decision this year reduced to one of four questions:

- who is allowed to ASSERT this?
- can history RECONSTRUCT this?
- what does this identity NAME?
- what counts as the SAME thing?

None is a programming-language question. Oath began as a language with proofs and
is more accurately described now as a protocol for epistemic boundaries — the
proofs are one kind of evidence it carries rather than the point of it.

### And a note on where this stops being the story

The methodology has earned its keep: it generalised beyond the section it was
built on, produced a defect class nobody was looking for, and — most usefully —
documented its own boundary. It is now a GUARDRAIL, not the project. The
remaining unwitnessed obligations belong to the ordinary evolution of §8.6 rather
than to a research narrative, and further cycles spent refining the measurement
would be sharpening a tool instead of using it.

## Three layers, and why the last one resists methodology (2026-07-31)

The protocol turns out to have three conceptual layers, and they need different
kinds of decision:

| layer | question | how it is settled |
|---|---|---|
| 1. HISTORICAL ASSERTIONS | what must be preserved because only the participants knew it | by asking whether history could reconstruct it |
| 2. DERIVED FACTS | what history should recompute rather than trust | by asking who computes it and whether they are trusted |
| 3. EQUIVALENCE RELATIONS | which byte sequences or signatures count as the same thing | by CONVENTION — pinned, versioned, applied consistently |

Layers 1 and 2 have answers you can argue to. Layer 3 does not: "when are two
signatures the same signature" and "when are two octet sequences the same
publication" are not questions with mathematically correct answers, which is
exactly why the blind rounds surfaced them and could not resolve them. Reasonable
systems choose differently. What matters is not which convention is chosen but
that it is EXPLICIT, VERSIONED, and applied everywhere — an unstated equivalence
is the one defect that makes two correct implementations disagree forever.

§8.6.4a already does this well for signatures and says so in its own words ("this
version pins them"), which is why it is the model for the two open decisions
rather than another instance of them.

The partition that fell out of layers 1 and 2:

| preserved forever | always derived |
|---|---|
| the author's statement (envelope octets) | name transition |
| publication digest | guarantee |
| artifact hash | property verdicts |
| parent | termination |
| parent_rev | ownership state |
| signatures | revision counts |

### The inversion: a justification should survive its application

`parent_rev` was almost adopted for the wrong reason, and the difference matters
beyond §8.

  weak: store `parent_rev` so replay protection works.
  strong: store every historical assertion, because history cannot reconstruct it.

Under the second, replay protection is a THEOREM of the design rather than its
premise — and the design keeps standing if replay protection stops being the
motivating concern. A justification tied to one application dies with that
application; a justification about what history can and cannot recover does not.

The precise fact at stake makes it concrete. A verifier can always derive what
the revision ACTUALLY WAS. It can never derive what the AUTHOR BELIEVED it was.
Publish against revision 37 after someone advanced the name to 38, and history
tells you the current revision was 38 — it cannot tell you an envelope was signed
believing 37. That belief exists only in the signed statement, and discarding it
destroys it permanently. Replay detection is downstream of that.

### Honest measurement declines before it improves

Every time a measurement here became more discriminating, its headline number got
worse:

- implementability scores FELL because inference was refused rather than papered
  over;
- envelope witnessed coverage stayed at 8 while the obligation count rose to 20,
  because obligations were split correctly and nothing was written to cover them;
- the rule matrix got "worse" when it stopped conflating causal influence with
  faithful representation.

A project optimising the ratio would have avoided all three. The objective here
is the TRUTHFULNESS of the ratio, and 8/20 that honestly says twelve are
unwitnessed is worth more than 20/20 obtained by weakening obligations or
inflating witnesses. Treat a declining number after a measurement change as
evidence the measurement improved, and an improving one as the thing to check.

## The dividing line the whole record model follows (2026-07-31)

> The journal preserves everything the publisher SIGNED, and nothing the registry
> merely COMPUTED.

One line, and it decides both §8.6 record-model questions — it would have
predicted them rather than being extracted from them afterwards.

The journal had it exactly backwards. It stored `name_transition`, which the
registry computes, and discarded `parent_rev`, which the author signed. So the
computed thing was durable and trusted, while the evidence disappeared at
admission. Fixing each separately produced the same answer twice, which is how
the line got found.

`parent_rev` is unlike `name_transition` in the way that matters: the author KNEW
it at publication time — they had to, in order to sign the envelope. The signed
statement is "I believe I am publishing against revision 37", and that is
evidence about what the author believed, not a fact about history that can be
recovered later. Reconstructing 37 from the journal answers a different question:
what the revision actually WAS, not what the author claimed it was. Only the
first can expose a stale envelope.

## DECIDED: the signed revision is persisted (2026-07-31)

`parent_rev` is now a journal member, and ENV-VERIFY-REVISION requires it to
equal both the envelope's value and the revision derived from preceding history.

Without it, ABA replay survived offline verification entirely. After `n` → A → B
→ A, an envelope signed against the FIRST A carries a stale revision — but clause
5 compares only name, artifact and parent, every one of which is valid again
after the cycle, and no entry member recorded the revision that would expose it.
§8.6.2 claims "ABA protection is unaffected" because the ENVELOPE carries the
revision; the journal then discarded it at admission, so the protection held only
inside a live store. An offline auditor could not check the single property the
revision exists to provide.

It is a STRING, never a JSON number — §8.6.1 makes the revision unbounded and a
float64 reader corrupts values past 2^53. That is not a hypothetical: the
envelope fixture demonstrating arbitrary precision was itself carried in a lossy
form, and this member was designed around that finding rather than repeating it.

MIGRATION. Verified before landing: all 548 committed entries re-serialise
byte-identically under the new field order, because §8.2.1 omits empty members
and no existing entry carries the field. History is not rewritten, no chain value
moves, no entry digest changes. Entries predating the member are verified without
the check, and a verifier reports the revision as unavailable rather than as
matched — the same disclosure discipline as reconstructed transitions.

## DECIDED: a transition is derived, not consumed (2026-07-31)

Offline verification derives facts; it does not consume them.

`name_transition` is written by the store, and it is not a publisher claim — it
is a property of journal history. Reading it from the entry inverted the trust
model for precisely the field that decides whether §8.6.4 clause 5 runs: a store
could label a transition `unchanged`, attach a genuine signature over an
unrelated envelope, and the entry passed every clause. That is the asymmetry this
project has spent its whole life removing everywhere else.

Now ENV-VERIFY-DERIVED-TRANSITION: the verifier derives the transition from
preceding journal history, and a stored value disagreeing with the derived one is
a verification failure.

**Entry verification is therefore a whole-journal operation.** Accepting that is
not a new cost — chain integrity, replay protection, revision evolution and
ownership history are already journal properties, and an isolated entry has never
been independently meaningful. `name_transition` was the one field pretending
otherwise, and the pretence is what the attack used.

Legacy entries predating the field are reconstructed under §8.6.2's KIND
restriction and reported as reconstructed rather than as stated. History is not
rewritten; how it was interpreted is disclosed.

STILL OPEN, and each is protocol design rather than specification engineering:
whether `parent_rev` must be committed into each accepted entry (without it, ABA
replay survives offline verification); which Ed25519 equation defines validity
where cofactored and cofactorless disagree; and whether canonical base64 pad bits
are part of publication identity.

## A fifth defect class: the defective witness (2026-07-31)

§8.6 was chosen to test whether the four-class taxonomy generalised. It produced
a FIFTH class, which was the pre-registered "most valuable outcome":

| class | subject of the defect |
|---|---|
| 1 MISSING MEANING | the specification |
| 2 HIDDEN NORMATIVE INPUTS | the specification |
| 3 INTERNAL INCONSISTENCY | the specification |
| 4 INCOMPLETELY DEFINED OBJECTS | the specification |
| **5 DEFECTIVE WITNESS** | **the measurement apparatus** |

The first four are all defects in the document. The fifth is a defect in the
thing measuring the document, and it is invisible to every gate built for the
first four — drift checks compare committed fixtures against generated ones, and
in both §8.6 instances BOTH HALVES WERE EQUALLY WRONG, so nothing drifted.

Now IMPL-WITNESS-FAITHFUL, and gated: `check-fixture-integrity` requires every
canonical envelope record to reproduce its own octets and forbids non-string
members. Verified against both historical defects.

### Re-examining §12 for the class, as the pre-registration required

It was there all along; we lacked the name and classified each instance inside
one of the first four:

- a vector carried §12.4's character-rule REJECTION obligation while the prose
  stated only the exclusion (round four) — the fixture was the sole source of a
  normative rule;
- `model_licenses`, the per-vector model override the whole precedence test
  depends on, exists nowhere in the prose;
- `fixtures/MANIFEST.md` cites DESIGN.md as the licence vectors' authority — a
  document outside the declared normative surface;
- the licence vector schema is documented nowhere, so the conformance surface
  itself cannot be consumed without inference.

That retroactive fit is the strongest evidence the class is real rather than an
artefact of §8.6: naming it made four previously-miscategorised findings resolve
into one shape.

## Four defect classes, and the limits of the measurement language (2026-07-31)

Four blind rounds against §12 produced four qualitatively different failures, in
a progression that looks like it generalises beyond this section:

| round | class | what it means |
|---|---|---|
| 1 | MISSING MEANING | the document did not say enough to proceed honestly |
| 2 | HIDDEN NORMATIVE INPUTS | the meaning existed, but lived in fixtures or in the implementation rather than the declared surface |
| 3 | INTERNAL INCONSISTENCY | enough was communicated to implement, but the document contradicted itself and leaned on undocumented implementation constants |
| 4 | INCOMPLETELY DEFINED OBJECTS | every field was correct and the encoding still did not say what it identified |

The fourth is the one worth naming, because field-level review cannot see it.
Nothing was missing from §12.4's list of inputs; the identity simply never
answered "identity of WHAT?". That became IMPL-IDENTITY-SUBJECT, and
check-normative-source now asks it of every identity encoder mechanically — the
review question turned into a column in a table.

### The refusal is transmitted by the process, not by the implementer

The most valuable artifact of round four is not a repair. Facing three
irreproducible vectors, the implementer identified that both readings satisfied
the stated rule, verified the alternative reading in a SEPARATE probe, and
declined to adopt it because doing so would be inference — leaving the vectors
failing and reporting the gap.

That behaviour was not specific to that subject. It came from §13's definition of
inference and from the dispatch brief. The original N-version goal was "make an
independent kernel agree"; this is a different and better one — make an
independent implementer behave scientifically. The discipline now lives in the
published specification and the harness rather than in whoever is running.

### The measurement language has limits, and it has hit one

LICENSE-CLOSURE-EXCLUSIVE is unwitnessed and, more importantly, UNWITNESSABLE:
every vector hands the closure over pre-assembled, so the fixture family can
express closure EVALUATION and cannot express closure CONSTRUCTION. Half of what
`policy=composition` means is therefore unmeasured.

This is not a missing test case, and adding cases will never fix it. It is the
fixture language being unable to state the property — the same situation as an
implementation language that cannot express an invariant. The honest consequence
is that "out of scope for this family" must never be reported as equivalent to
"witnessed": the rule-to-vector matrix distinguishes them for exactly this
reason, and the distinction is what makes the limitation visible instead of
comfortable.

The next work on fixtures is therefore about EXPRESSIVENESS rather than coverage.
A family that could state "this store, this dependency graph, this artifact
outside the closure" would witness the rule; no number of (assertions, verdict)
records ever will.

## The epistemology applied to itself (2026-07-31)

The implementability experiment ended up governed by the same rules as the
software it measures. That was not planned; each constraint was added because the
alternative had already failed once, and the alternatives failed for the reasons
Oath's own design anticipates.

| Oath principle | the experiment's analogue |
|---|---|
| a publisher cannot choose `PROVEN` | the dispatcher cannot choose `PASS` after the fact |
| the registry reports only what it reproduced | the ledger reports only observed outcomes |
| identity binds exact bytes | the surface digest binds the exact normative surface |
| the journal is append-only | rounds are append-only |
| historical claims are not reinterpreted | old rounds are not re-scored |
| campaign identity fixes the evaluation context | the surface digest fixes the experimental context |
| assertion precedes derivation, and is labelled | the hypothesis precedes dispatch, and is labelled |

Two of these were learned the hard way rather than reasoned to.

**Old rounds are not re-scored.** Removing a file from the export allowlist
instantly broke both historical claims, because re-exporting an old commit with
today's list computes a surface that experiment never used. The fix was to bind
each claim to the file list it was actually given — the same move as binding an
evaluation to its model's content rather than to a version string it can outlive.

**A hypothesis after the result carries no information.** "The specification is
sufficient", written after a `PASS`, is a description. Written before dispatch it
is a prediction that can be wrong. That is precisely the assertion/derivation
distinction the project draws everywhere else, applied to its own methodology —
and the ledger cannot detect its absence from its own contents, which is why it
had to become a rule.

The same logic governs WHEN a constraint may be added. §13.4's requirement that a
verdict name its sections and its surface was written while a run was in flight
and its outcome unknown. Deciding how a `PASS` may be phrased after obtaining one
is indistinguishable from shaping the claim to fit the result.

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
