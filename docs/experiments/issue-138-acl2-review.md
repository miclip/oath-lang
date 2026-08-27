# ACL2 comparative review (#138)

**Status: COMPLETE.** Falsifiers pre-registered before the ACL2 evidence was
gathered (this section and the per-section *Pre-registered falsifier* blocks were
written first, on the corpus state recorded below); ACL2 evidence and verdicts
filled afterward, against evidence from primary ACL2 sources. This ordering is the
point — a review whose verdicts are written after a motivated reading finds
transformations everywhere, which the pre-registration exists to prevent. The
outcome ledger is at the end; two verdicts run against Oath's going-in position,
one is a clean win. One implementation issue extracted (typed proof-failure
taxonomy).

## What this is, and is not

Not "learn ACL2". Not implementation. No code. The deliverable is one sentence per
topic: *here is what ACL2 has thirty years of evidence about, and here is whether
Oath intentionally agrees, intentionally differs, or is wrong.*

**The reframing that is the headline** (Michael, on the issue): ACL2 is not a
prover to replace Z3. It is **thirty years of research into what happens AFTER an
automatic proof attempt does not succeed.** The architecture comparison is a
smaller gap than it looks — SPEC §7.2 already carries structural induction,
lexicographic induction, recursion-induction along a function's own measure (#56),
a self-lemma fixpoint, and relevance filtering over a proven-lemma library, and
`reverse(reverse xs) = xs` is proven today by induction over Z3. So whatever the
gap is, it is not "Oath needs an induction engine." It is what ACL2 learned about
the space after the first automatic attempt fails.

## Method

Each section names, IN ADVANCE, the ACL2 finding that would count as *Oath being
wrong* — the same discipline the implementability ledger applies to blind rounds.
A section concluding "Oath intentionally differs" with no falsifier is
unfalsifiable and worthless: it absorbs any evidence. Where the evidence neither
confirms nor falsifies, the verdict says so rather than rounding to agreement.

## Oath's current state, measured (the baseline the falsifiers were written on)

- **219 definitions: 129 proven · 86 tested · 4 falsified** (`fixtures/prove/outcomes.json`).
  The comment thread's 2026-08 figures (189: 125/60/4) have grown; the gap below
  grew with them.
- **The entire record Oath keeps for a property that did not prove is one bit.**
  A definition's meta carries `prop_names` and a `proven_props` index list plus a
  whole-definition `guarantee.level` (`proven` / `tested` / `falsified` /
  `asserted`). A property that did not prove is simply absent from `proven_props`.
  There is no per-property reason. Concretely, `abs-small.bounded-wrongly` is a
  property that is FALSE sitting at level `tested` — observably identical to a
  property that is TRUE but unproven. The following all collapse to `tested`:
  false-but-no-witness-found; true-but-needs-an-induction-not-chosen;
  true-but-needs-an-excluded-lemma; true-but-outside-the-SMT-fragment;
  rlimit-exhausted; bad-formulation. Only `falsified` (a counterexample was found)
  is named. That is the load-bearing gap, and §5 is the load-bearing section.
- **Two graphs, one of which exists.** The DEPENDENCY graph (*this proof COULD use
  these proven properties* — §7.2's candidate closure) exists and is a pure
  function of the corpus. The PROOF graph (*this proof ACTUALLY used these*) does
  not: Oath asks Z3 whether a goal discharges, never what discharged it. There are
  no unsat cores anywhere in the kernel or SPEC (verified). Every registry metric
  today — fan-in, reuse, centrality — is therefore *availability, not necessity*.
- **`oath hint` (#67) exists**: it admits a *named* proven lemma the §7.2 relevance
  filter excluded. SPEC §7.2 itself frames it as "a reachability lever over the
  same non-monotone solver." It is a manual hint, and §6 lists manual hints among
  the things Oath supposedly rejects. That contradiction is real and §3 must
  resolve it, not assume it away.
- **The non-monotonicity witness is real and normative.** SPEC §7.2:
  `q-peek.peek-is-head` discharges at 2,294 rlimit with NO lemmas and does not
  terminate within the full 400M budget once its twelve legitimately-relevant
  lemmas are admitted. A budget-limited solver is non-monotone in its axiom set,
  severely enough to decide verdicts, not merely speed. This is load-bearing for
  §2/§4 (it makes closure-based invalidation *sound* and core-based invalidation
  *unsound*, below).

---

## §1 Proof workflow — "proof failed, now what?"

**Oath's position.** Oath has `prop`, proof status, the registry, and dependency
closure. It has almost no story around *proof failed → now what*. The bet is that
the interactive human loop ACL2 institutionalized — inspect the stuck goal,
conjecture a lemma, prove it, retry — is one an AGENT runs against the REGISTRY,
not a human runs at a REPL.

**Pre-registered falsifier.** ACL2's thirty years show that the lemma a human
conjectures after a failed proof characteristically requires INVENTING a term or
generalization not already present anywhere in the corpus — so the winning lemma
is *created*, not *retrieved*. If the productive next step is predominantly
invention rather than retrieval of an existing proven artifact, then "the agent
discovers the steering from the registry" is not merely incomplete, it is aimed at
the wrong operation.

**ACL2 evidence.** ACL2's prover is a fixed heuristic "waterfall" that does not
backtrack; on failure it does not emit a reason, it STOPS and prints the goals it
got stuck on (gag-mode's "key checkpoints" — the unsimplifiable goals not descended
from another checkpoint), and hands diagnosis to the user. The intended workflow is
**"The Method"** (`THE-METHOD`, Kaufmann & Moore): inspect the failed attempt *from
the beginning, not the end*; the normal repair is a NEW rewrite lemma added to the
to-do list and proved FIRST, then retry — an iterative human loop, not an automatic
search. `FAILURE` stresses that a failed proof does not mean the conjecture is
false even when the last printed formula looks false, and that the user "must figure
out what you know that ACL2 doesn't … and how to impart that knowledge in the form
of rules." So the productive lemma is characteristically a fact the human RECOGNIZES
the goal needs. But ACL2's own recent tooling is moving toward automating the
retrieval/suggestion end: DrLA (2023) uses cgen counterexamples + THEORY EXPLORATION
over existing theory to *suggest a missing hypothesis* automatically — evidence that
part of "what the human knows" is discoverable from the existing corpus rather than
invented. [THE-METHOD; FAILURE; HINTS-AND-THE-WATERFALL; arXiv 2311.08857]

**Verdict — AGREE on the substrate, and the falsifier FIRES on the generative
step.** Oath's "the steering is reusable proven artifacts in a shared registry"
has thirty years behind it: Moore's retrospective names *shared certified books of
definitions and lemmas* as exactly what industrial ACL2 users build and trade — the
crown-jewel reusable asset — which is Oath's content-addressed registry by another
name. "You guide the prover most of the time simply by identifying lemmas for it to
prove" is the ACL2 way of saying the substrate is proven artifacts, not scripts.
But the pre-registered falsifier fires on the hard core: ACL2's own authors state
that GENERALIZATION "requires more creativity than our provers have," and the human
supplies the generalizing lemma precisely because the machine cannot invent it.
Oath has NO story for *proof failed → conjecture the key generalization*: the
registry answers RETRIEVAL (is this fact already proven somewhere?), not INVENTION
(what new lemma would unlock this?). So the honest result is split — Oath is right
that a reusable-artifact substrate replaces the book ecosystem, and that is a real
strength, but the generative "now what" step is where ACL2 locates irreducible
creativity, and "the agent discovers it" is an unbacked hope, not a solved problem.
That is the actual `proof failed, now what` gap, and the registry does not close it.

---

## §2 Lemma ecology

**Oath's position** (settled in the thread, not a proposal): a lemma is an
EMERGENT ROLE, not a construct. There is no `lemma` form — SPEC §1.4's whole
grammar is `data` and `defn`; §7.2 defines a lemma purely by role (a previously
proven property in the dependency closure, admitted by the relevance filter). Reuse
decides what is foundational, after the fact. Importance is therefore a graph
property, to be DERIVED (like campaign identity, authority, provenance), never an
author annotation or stored metadatum.

**Pre-registered falsifier** (verbatim from the thread, the refined form): *an
ACL2 workflow needing a persistent author-supplied classification of a theorem
that cannot be reconstructed from the proof graph PLUS the deterministic artifacts
already required to reproduce a proof (script bytes, solver version, rlimit).* A
theorem ACL2 labels "a key lemma" does NOT falsify this if that role is recoverable
after the fact; only a human-maintained semantic classification irrecoverable from
reproducible artifacts does.

**ACL2 evidence.** ACL2 has NO "this theorem is important/foundational" annotation.
A lemma becomes central by being reused — `include-book`'d and `:use`'d across the
shared community books — which is emergent, exactly Oath's claim. What ACL2 DOES
make author-supplied and persistent is the *operational role*: `rule-classes`
(defaulting to `:rewrite`) classifies HOW a proven fact is later used, and "you
determine the kind of rule ACL2 stores" — a class not recoverable from the theorem
STATEMENT alone (the same formula can be a `:rewrite` or a `:forward-chaining`
rule; a `:corollary` reshapes the stored rule away from the statement). Enable/
disable is likewise author-supplied (`defthmd` = exported but disabled), and theory
curation (`:in-theory`) is documented as "crucial" because too many active rewrite
rules cause loops, wrong normal forms, and slowdown. The `local`/`defthmd`
mechanism makes the API-vs-scaffolding split first-class: scaffolding is `local`
and vanishes from the client's world; hazardous-if-enabled rules are exported
`defthmd`-disabled. [RULE-CLASSES; INTRODUCTION-TO-THE-DATABASE; TIPS; LOCAL;
Milestones 2019]

**Verdict — hypothesis CONFIRMED on its own terms; the falsifier misses IMPORTANCE
but lands one layer over, on RELEVANCE.** ACL2 has no persistent importance
classification, so the pre-registered hypothesis — *importance is emergent from the
graph, not an author annotation* — is exactly right and has thirty years behind it:
foundational lemmas in ACL2 are the widely-reused ones, discovered after the fact,
never declared. The refined falsifier does not fire for importance.

But the evidence forces a distinction the hypothesis blurred, and it is worth more
than the confirmation: **importance is emergent, but RELEVANCE — which lemmas to
activate for a given goal — is where persistent author input survives in BOTH
systems.** ACL2's `rule-classes` themselves are an artifact of its rewriting engine
(Z3 takes axioms and searches; it needs no human-oriented rewrite rules), so they
do not falsify Oath. But the deeper thing `:in-theory` curation solves — taming a
solver that DERAILS when too many facts are active — is not an ACL2 artifact: it is
the q-peek non-monotonicity witness, which Oath carries in its own SPEC. ACL2
answers it with human theory curation; Oath answers it with the AUTOMATED relevance
filter, which is a genuine advance — automating what ACL2 left to hand. The catch:
where the filter fails, Oath falls back to `oath hint`, and an `oath hint` entry IS
a persistent, author-supplied, per-goal relevance classification the dependency
graph cannot reconstruct (the filter deliberately excluded the named lemma). So the
strong claim "*all* proof steering is emergent/derivable" is refuted — by ACL2's
`:in-theory` experience and by Oath's own #67. The lemma-role model is vindicated
for what it actually claimed (importance); Oath is arguably stronger than ACL2 on
relevance (automated vs. manual); and the residue of manual relevance steering is
real, small, and already named `oath hint`. That is not a defeat — it is the exact
size of the concession, which §3 must scope.

---

## §3 Automation boundary — which steering survives if the human disappears?

**Oath's position.** The question is not "how do we reproduce ACL2 hints" but
whether the agent discovers them, the registry provides them, or they should exist
at all. Oath already shipped a concession: `oath hint`. §6 lists manual hints as
rejected. One of the two must give.

**Pre-registered falsifier.** ACL2 evidence that an ESSENTIAL and LARGE class of
steering — a custom induction scheme, a specific `:use` — requires insight
underivable from the corpus and the goal, i.e. intrinsically human and not
agent-discoverable. If such steering is essential and common, "steering survives
the human disappearing" fails, and §6's blanket rejection of manual hints is
wrong (which `oath hint`'s existence already suggests). The converse also counts:
if ACL2's evidence is that most hints are mechanical reachability levers a search
could supply, that CONFIRMS Oath's bet and demotes `oath hint` to a scoped,
agent-suppliable mechanism rather than an embarrassment.

**ACL2 evidence.** Both apply, on different classes of steering, and the split is
the whole answer. Human steering is ESSENTIAL, not provisional: "you guide the
theorem prover most of the time simply by identifying lemmas for it to prove," and
the ACL2 authors are explicit that some steering exceeds automation — generalization
"requires more creativity than our provers have," which is exactly why the user
states the generalizing lemma. The entire (6 MB) user manual is described by Moore
as being "about how to override the automatic choices," chiefly induction. AND yet
much steering IS mechanical: the DrLA tool automates hypothesis suggestion from
counterexamples, and industrial ACL2 works because humans ship reusable *lemma
libraries* — retrieval, not fresh invention, carries the everyday load.
[INTRODUCTION-TO-THE-THEOREM-PROVER; ACL2 Induction Heuristics; Milestones 2019]

**Verdict — the falsifier FIRES for the generative class and is REFUTED for the
retrieval class; the contradiction resolves in favour of `oath hint`.** §6's
"reject manual hints" is wrong, and thirty years of ACL2 evidence is decisive about
why: manual steering is not a smell, it is the design. So the contradiction the
issue names resolves cleanly — **`oath hint` stays, and §6 must drop "manual hints"
from its rejection list, or scope it precisely.** SPEC §7.2 already frames `oath
hint` correctly as "a reachability lever over the same non-monotone solver," which
is exactly the role ACL2's `:use`/`:in-theory` hints play. The reframe that makes
it not-an-embarrassment: Oath admits a *named proven lemma the relevance filter
excluded* — a RETRIEVAL of an existing artifact, never an invention — so it is the
mechanical, agent-suppliable end of ACL2's hint spectrum, and an agent searching the
registry is a plausible supplier of it (DrLA is ACL2 doing exactly this kind of
search). What Oath has NO answer for is the GENERATIVE end: the novel generalization
and the custom induction scheme, where ACL2's authors locate irreducible creativity.
So "which steering survives if the human disappears?" splits precisely: the
retrieval lever survives (registry + agent search), the generative insight does not
(and Oath should stop implying it will). The honest scoping: name `oath hint` a
retrieval lever, not a hint in ACL2's fuller sense, and record that Oath's automation
story covers relevance-retrieval and stops at generalization — the same line ACL2
drew and never crossed.

---

## §4 Library evolution

**Oath's position.** Content-addressing plus closure-based invalidation is the
identity and maintenance model: a definition's hash is its identity, changing it
changes every dependent's hash, and proof state drops for every definition whose
§7.2 candidate closure contained the changed artifact — *regardless of whether any
proof leaned on it*. That last clause is not conservatism, it is soundness: by the
q-peek non-monotonicity witness, changing an artifact can flip a verdict from
proven to not-proven WITHOUT that artifact appearing in the old proof's core, so
invalidating only core members would leave a stale `proven` behind. Closure
membership is the sound basis; proof-core membership cannot be, while the budget is
finite.

**Pre-registered falsifier.** ACL2 evidence that whole-artifact content identity
is too COARSE to be usable at scale — that maintainability REQUIRES decoupling a
theorem's stable interface from its proof in a way content-addressing forbids, or
that a change content-addressing treats as identity-preserving is one ACL2's
experience shows must break dependents (a soundness miss). Either would show the
identity model is wrong, not merely strict.

**ACL2 evidence.** ACL2's identity model is COARSER and weaker than Oath's, by
deliberate tradeoff. The unit of certification is the whole BOOK (a `.cert` file),
never the individual theorem; there is no per-theorem incremental re-check. Book
identity is the `book-hash`, whose DEFAULT is a fingerprint of file SIZE and
WRITE-DATE — not a content hash — chosen for speed; a real checksum mode exists but
is off by default. Invalidation is timestamp-driven per-book over the `include-book`
DAG; a prover change defaults to "recertify EVERYTHING," softened only by a
HAND-MAINTAINED list of feature-dependencies (a maintainer must know in advance
which prover constant a book leans on). The release gate is brute: the prover cannot
ship unless all ~100k community-book theorems re-certify. And the durable
maintenance finding is that theorem STATEMENTS are stable while proof SCRIPTS/hints
are volatile — subgoal hints "can be broken either by changes to the relevant
functions or by changes to ACL2 system heuristics," because a proof is a trajectory
through the rewriter and any upstream change moves the checkpoint a hint was tuned
to. Russinoff's RTL library over two decades shows the split at scale: specs frozen
for backward compatibility, proofs continuously churning. [CERTIFICATE; book-hash
xdoc; cert.pl; Swords 2018; Russinoff 2019; Milestones 2019]

**Verdict — the falsifier does NOT fire; Oath is cleanly stronger here, and ACL2's
scripts-volatile lesson vindicates a choice Oath already made.** Neither branch of
the falsifier lands. Content-addressing is not too coarse — it is FINER than ACL2's
per-book, size-plus-mtime default, and it is a content-address that DEFINES identity
rather than a fingerprint that merely detects drift. And it misses no breaking
change: closure-based invalidation is not conservative but SOUND, and the SPEC
carries the proof — by the q-peek non-monotonicity witness a change can flip a
verdict without the changed artifact appearing in the old proof's core, so
invalidating only used artifacts would strand a stale `proven`; closure membership
is the only sound basis, which ACL2's coarse "recertify everything" approximates by
hand and Oath derives exactly. Oath even has ACL2's release-gate analog without the
manual feature-dependency list: the async `require_proven` prove-worker re-verifies
against current identity. The one lesson to import rather than congratulate: ACL2's
thirty years say proof SCRIPTS are the volatile burden and STATEMENTS are stable —
and Oath eliminated scripts by construction (it stores proven facts + a derived
relevance filter, not a trajectory). That is the architecture being right by
subtraction. The single exception is the residue from §2/§3: an `oath hint` entry is
Oath's one script-like artifact, a per-goal lemma selection, and ACL2's evidence
predicts it will rot exactly as a subgoal hint does — a solver-version or corpus
change can make the named lemma no longer the one that discharges the goal, and
nothing re-examines it. Small, but real, and now named.

---

## §5 Failure taxonomy — the load-bearing section

**Oath's position** (implicit, and the one most worth testing): the reason a proof
failed is largely DERIVABLE. Oath could distinguish, at least, false-with-witness
(already: `falsified`), outside-the-SMT-fragment, rlimit-exhausted, and
needs-a-lemma/induction — instead of collapsing all of them to `tested`. The 86
tested definitions are a study population that already exists in the fixtures; the
review classifies the failures this project has been accumulating rather than
constructing examples.

**Pre-registered falsifier.** ACL2 evidence that reliable failure classification
REQUIRES human judgment no automated analysis reconstructs — that ACL2, with
thirty years, still cannot automatically tell *false* from *needs-a-lemma* from
*needs-an-induction*, and the diagnosis is fundamentally interactive. If ACL2's
own failure diagnosis is irreducibly human, Oath's hope to derive a taxonomy is
naive and §5 should be abandoned rather than implemented.

**ACL2 evidence.** ACL2's waterfall does NOT emit a reason for failure — it stops,
prints the "key checkpoints" (the goals it could not simplify, not descended from
another checkpoint), and hands diagnosis to the human. In thirty years it
automates exactly ONE classification: *the conjecture is false* — via cgen, which
finds a concrete counterexample by random/strategic testing (`prove/cgen`,
default-on in ACL2s). And even that is one-sided: absence of a counterexample never
certifies truth. Every other class — missing lemma, missing/wrong induction, failed
generalization, unrelieved hypothesis, rewrite loop, resource exhaustion — is
diagnosed by a human reading checkpoints, at best SEMI-automatically when the human
points a targeted tool (`break-rewrite`'s `:failure-reason`, `with-brr-data`
provenance, `accumulated-persistence`) at a *suspected* rule. The frontier of
automation is DrLA (2023): from cgen's counterexample/witness split it can suggest a
missing HYPOTHESIS — narrow, and explicitly not a general "why did this fail"
oracle. [FAILURE; INTRODUCTION-TO-KEY-CHECKPOINTS; BREAK-REWRITE; arXiv 2311.08857,
2311.08856; 1105.4394]

**Verdict — the falsifier FIRES on the ambition, and the boundary it draws is the
finding.** The pre-registered falsifier was that reliable failure classification is
irreducibly human. It fires against the STRONG reading (Oath derives a full "why it
failed"): ACL2's thirty years did NOT automate the distinction between *needs a
lemma* and *needs an induction*, and Oath will not either. But the exact place the
line falls is more useful than a defeat:

1. **The one class ACL2 automates is the one class Oath already names.** `falsified`
   = ACL2's cgen counterexample. Independent thirty-year convergence on WHERE the
   automatable boundary sits, and not a coincidence: a counterexample is a positive,
   checkable witness, while every other failure is the *absence* of a proof, which
   has no witness to classify.
2. **Oath's Z3 architecture makes a DIFFERENT small set of classes cheaply
   derivable** — ones that do not correspond to ACL2's rewriting-specific tools.
   *Outside-the-SMT-fragment* is a STATIC property of the goal, computable without
   ever calling the solver. *Budget-exhausted* is solver-OBSERVABLE (z3 reports
   `:reason-unknown` and the rlimit it hit), distinct from a fast genuine `unknown`.
   Neither needs the human checkpoint-reading ACL2 depends on.
3. **The valuable "needs THIS lemma" class is exactly what ACL2 could not
   automate — so Oath must not claim to derive it.** What Oath's content-addressed
   registry can do instead is turn the residual into a QUERY — *does a proof of this
   shape already exist?* — rather than the human RECOGNITION ACL2 relies on. That is
   a genuinely different mechanism, but it is downstream: reachable only once the
   coarse derivable classes are separated, and not part of the first change.

So §5 is actionable, but the honesty is the point: a SCOPED taxonomy of the classes
Oath can actually derive, and an explicit refusal to promise the human-only one that
ACL2's evidence shows is out of reach. The current one bit throws away the
derivable classes along with the underivable one.

---

## §6 What Oath should explicitly reject

**Candidates:** interactive theorem proving; manual hints (but see §3 — `oath hint`
exists); proof scripts; human-authored induction schemes.

**Pre-registered falsifier (per candidate).** For each, ACL2 evidence that the
rejected thing is NECESSARY to reach the theorems Oath targets — not incidental to
ACL2's rewriting architecture, which Oath sidesteps with Z3. The honest test each
must pass: is ACL2's reliance on this a fundamental need, or an artifact of its
engine? A candidate that ACL2 needs only because it rewrites is safe for Oath to
reject; one ACL2 needs because the PROBLEM is hard is not.

**ACL2 evidence and verdict, per candidate.**

- **Interactive theorem proving — REJECT stands, ACL2 agrees.** ACL2 has an
  interactive `proof-checker` but treats it as SECONDARY; the bet is the automatic
  waterfall plus a grown rule database, because a rule persists and reshapes all
  future proofs while a tactic script guides one. Oath rejecting interactive
  proving is consistent — *provided* the registry + agent supply what ACL2's shared
  database supplies. That proviso is the load-bearing condition, not a footnote.

- **Manual hints — REJECT the rejection (established in §3).** ACL2's thirty years
  make manual steering essential. `oath hint` is the correct, predicted concession;
  §6 must drop or precisely scope this candidate. The engine test is decisive the
  OTHER way here: hints answer non-monotonicity, which is a fundamental hardness
  (q-peek), not an ACL2-rewriter artifact.

- **Proof scripts — REJECT stands, and this is Oath's strongest vindication.** The
  single clearest thirty-year finding is that proof SCRIPTS/hints are the volatile
  maintenance burden (Swords; Russinoff). Oath discarded them by construction —
  proven facts + a derived relevance filter, no stored trajectory — which is the
  fragility ACL2 fights, removed rather than managed. Fundamental win, not an engine
  artifact. Caveat carried from §4: `oath hint` is a residual mini-script and
  inherits the fragility in miniature.

- **Human-authored induction schemes — REJECT WITH A NAMED LIMIT, not as solved.**
  ACL2 chooses induction automatically but its authors call the heuristic fallible
  ("no guarantee"), require a user-supplied measure when the guess fails, and orient
  the entire manual around "how to override the automatic choices." This is NOT a
  rewriting artifact — a hard theorem can need an induction no local heuristic finds,
  and that is intrinsic. Oath automates the common cases (structural, lexicographic,
  measure/recursion-induction #56) and genuinely covers more than ACL2 does
  hint-free, but it will hit the same wall on a theorem needing a scheme its
  heuristic does not generate, with no `:induct` escape hatch. So rejecting
  human-authored schemes is right for what Oath automates and dishonest as a blanket
  claim: the residual is a real ceiling, to be stated, not a solved problem.

The through-line: the two rejections that hold (interactive proving, proof scripts)
hold because Oath REPLACED the thing ACL2 needed — the shared database, the
persisted-fact substrate. The two that do not (manual hints, and induction-as-solved)
fail because they target a HARDNESS ACL2 met and Oath inherits: non-monotonicity and
the occasional un-guessable induction. Reject what you have replaced; do not reject
what you have merely renamed.

---

## The one implementation issue

The evidence confirms the going-in candidate and, more usefully, SCOPES it — the
scoping is the contribution, because the naïve version (derive *why* every proof
failed) is exactly what §5 shows ACL2 could not do in thirty years.

**Extract: give a non-proven property a TYPED, DERIVED failure reason, drawn only
from what Oath can compute — never the human-only "which lemma" class.** The record
today is one bit (absent from `proven_props`); `abs-small.bounded-wrongly`, a FALSE
property, is observably identical to a true-but-unproven one. Replace the bit with a
small closed vocabulary, every member of which is derivable without human judgment:

- `falsified` — a counterexample exists (Oath already has this; it is precisely the
  one class ACL2 automates, and keeping it as the head of the vocabulary states the
  validated boundary out loud).
- `outside-fragment` — the goal STATICALLY contains constructs outside Oath's
  SMT-encodable fragment; decidable by inspecting the goal, no solver call.
- `budget-exhausted` — z3 returned `unknown` with the rlimit reached
  (solver-observable, and already distinguished internally per §7.2's abort
  handling; this surfaces it as a persistent per-property verdict instead of a
  transient one).
- `unproven` — attempted validly, solver returned `unknown` below budget: the
  honest residual, the class ACL2's thirty years show is NOT further auto-classifiable
  into needs-lemma vs needs-induction.

**What this issue explicitly does NOT do**, and the review is the reason: it does
not claim to identify the missing lemma or the missing induction. §5's evidence is
that this is human recognition in ACL2, not derivation, and promising it would be
the overclaim the failure taxonomy exists to avoid. The content-addressed follow-on
— turning an `unproven` into a registry QUERY ("does a proof of this shape exist?")
— is real and is Oath's genuine advantage over ACL2's human checkpoint-reading, but
it is DOWNSTREAM: reachable only once the coarse classes are separated, and a
separate issue if the evidence for it ever accumulates. One change, not a program.

**Why this one and not the others.** The other candidates the thread surfaced were
tested and set aside by the evidence: proof-centrality / foundational-ness is
DERIVABLE from the existing dependency graph (§2) and needs no new artifact — and
its sharper form (load-bearing vs. merely-reachable) needs unsat cores Oath cannot
soundly use for invalidation anyway (§4), so it is not a storage change; the
identity/invalidation model is already stronger than ACL2's (§4), nothing to fix;
`oath hint` is a concession to NAME and scope (§3/§6), a documentation act, not an
implementation. The failure taxonomy is the one place where Oath discards
information it demonstrably HAS — the static fragment classification, the observable
budget-exhaustion, the counterexample it already computes — collapsing all of them,
plus the one it honestly cannot derive, into a single bit.

**Its relationship to #134, stated as the review was asked to state it.** #134 gives
BACKEND refusals a typed reason instead of prose; this gives PROOF failures a typed
reason instead of a bit. They are the same disease at two layers — the system knows
why it said no and throws it away at the boundary — and the review's finding is that
they should share the typed-reason DISCIPLINE (a closed, derived vocabulary; no
free-text; no promise beyond what is computed), though not necessarily one code
path: #134 classifies why a construct is UNSUPPORTED by a backend, this classifies
why a goal did not DISCHARGE, and conflating the two vocabularies would be the
category error §5 warns against (a backend refusal is about the tool; an `unproven`
is about the goal). Same rule, two registers.

---

## Status and honesty ledger

Filed as the review's single extracted issue (#183): the typed proof-failure
taxonomy above. The pre-registered falsifiers fired as follows — recorded so the
review is falsifiable in retrospect, not narrated into agreement:

| § | pre-registered falsifier | outcome |
|---|---|---|
| 1 | winning lemma is invented, not retrieved | FIRES on the generative core; refuted for the retrievable majority |
| 2 | persistent author classification irrecoverable from the graph | does not fire for IMPORTANCE (hypothesis confirmed); lands on RELEVANCE, where `oath hint` is Oath's own instance |
| 3 | essential steering intrinsically human | FIRES for generalization/induction; refuted for retrieval — resolves the `oath hint` contradiction in its favour |
| 4 | content-identity too coarse, or misses a break | does NOT fire; Oath cleanly stronger than ACL2's per-book size+mtime default |
| 5 | failure classification irreducibly human | FIRES on the full-taxonomy ambition; the derivable sub-classes survive and are the extracted issue |
| 6 | a rejected thing is fundamentally necessary | FIRES for manual hints and induction-as-solved; holds for interactive proving and proof scripts |

Two verdicts run AGAINST Oath's going-in position (§1's generative gap, §3/§6's
manual-hint rejection) and one is a clean win (§4); that spread is the evidence the
falsifiers were not decorative. **The evidence is characterized from primary ACL2
sources gathered for this review; where a claim rested on a figure a source could
not confirm (e.g. quantitative hint-prevalence, the "~170k broken theorems"
number), it was not used.** The one limit this review cannot escape: it reads ACL2's
thirty years, it does not re-run them — the falsifiers test whether Oath's positions
survive that reading, not whether they survive a thirty-year deployment Oath has not
had.
