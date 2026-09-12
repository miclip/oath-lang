# #98 design: deterministic proof sharding as a SELF-CHECKING VERIFIER

Status: DESIGN, refined by an adversarial pressure-test of the soundness argument
(recorded below). No kernel code has been written from this document yet. Written
with full context (this session has read `oath/`), which is why the IMPLEMENTATION
must be blind-dispatched and this file is design + spec, not code.

## What changed from the issue body

The issue framed sharding as "harness plumbing" — a throughput fix that makes the
serial cold conformance run complete. miclip's own comments then revised that:
sharding changes §7.2's initial conditions, "the thing the whole guarantee system
rests on", and the protocol questions must be answered first. This document
answers them, and the answer reframes the feature twice:

1. **Sharding cannot reproduce the cold run's claim.** It verifies a NECESSARY
   condition of it — self-consistency — fast and in parallel. Those are different
   claims and conflating them is the failure mode the comments name.
2. **It is a VERIFIER, not a theorem.** It does not ASSUME the committed proven
   set is a fixpoint; it recomputes one round of the fixpoint operator in parallel
   and ASSERTS the result equals the committed set, failing loudly on any
   mismatch. This distinction is load-bearing — see "The correction" below.

## The two claims, stated precisely

The prover computes a run-stability fixpoint (`prove.rs`, "TWO-LEVEL proof
fixpoint"): iterate `F` from the EMPTY state, where `F(S)` attempts every property
once with candidate lemmas drawn from `S` (fixed for the pass), until `F(S) = S`.
**The conformance outcome is DEFINED as this limit reached from empty.**

- **COLD-REACHABILITY** (today's full run): the limit reached by iterating `F`
  from `∅` is `S`. Inherently serial across its ≤8 rounds.
- **SELF-CONSISTENCY** (what a shard can check): `F(S) = S` for a GIVEN `S`.

Self-consistency is **necessary but not sufficient** for cold-reachability: the
kernel's own comment (`prove.rs:2456`) says the iteration scheme is pinned "to
make the limit deterministic when more than one self-consistent state exists" —
i.e. multiplicity of fixpoints is real and asserted by the code. A committed `S`
can be self-consistent and NOT be the state the cold iteration from `∅` reaches.

We build the self-consistency verifier, label it as exactly that, and keep the
cold run as the periodic ground truth (gated by the #139 fingerprint when inputs
are unchanged; run cold only when they change). This is the repo's standard move:
narrow the claim to what is supported (§10 declined the candidate-script
obligation; #145 declined freshness) rather than overclaim.

## Why a shard can recompute F(S) exactly (the sound core)

Confirmed against the code by adversarial review:

- `candidate_lemmas` (`prove.rs:2268`) is a PURE function of `(proven, static
  corpus, hints)`. It reads `proven` as a whole set and filters by static
  structure — `dep_closure` (2209), `footprint` (2155), `lemma_admissible` (2187)
  — with NO view of `in_run` vs `recorded` and NO order dependence (BTreeSets,
  sorted output at 2323). So with `proven = S` fixed, candidates(G) are identical
  in any order, in any shard.
- The DIRECT-attempt SCRIPT is a pure function of `(goal, candidates, corpus)`:
  `direct_script_opts` (1206) builds a fresh `Cx` per call, resets the const
  counter, emits decls in deterministic first-touch order; rlimit/memory options
  live OUTSIDE the hashed bytes (1048). The shipped byte-oracle `scripts_for`
  already relies on exactly this purity.
- **Non-monotonicity does not bite a genuinely re-derivable goal.** A
  budget-limited solver can fail under EXTRA axioms (2442), but a goal is never
  handed all of S — only its dep-closure lemmas. Seeding gives it exactly the
  lemmas it had at the cold fixpoint.
- **Lemma starvation is removed by seeding.** `combined = S` is complete for every
  shard, so availability never depends on what the shard itself proved.

Corollary: **dependency ordering WITHIN a shard is unnecessary** — with `combined
= S` fixed, goals are independent; no Gauss-Seidel, no rounds. Shard by
`first_64_bits(definition_hash) mod n` and attempt each assigned goal once.

## The correction: it VERIFIES F(S)=S, it does not assume it

The pressure-test broke the theorem "for EVERY goal the seeded verdict equals S's"
as an UNCONDITIONAL claim, on two cracks. Both are real, and both are handled by
making the scheme a self-checking verifier rather than a presumption.

- **Crack A — convergence is unenforced by the producer.** The cold fixpoint caps
  at `for round in 0..8` (2487) and on non-convergence exits SILENTLY (`recorded =
  in_run`, 2599) with no check, no error, no flag in `outcomes.json`. So a
  committed `S` may not satisfy `F(S) = S` at all. A scheme that ASSUMES it would
  be wrong. A scheme that RECOMPUTES `F(S)` and asserts `== S` catches exactly
  this: non-convergence surfaces as `union ≠ S`. Sharding thus adds a convergence
  check the producer currently lacks (a separate finding, below).
- **Crack B — abort carry-forward.** A goal can be in `S` via carry-forward
  (2564): it proved in an early round from a small lemma state, then ABORTED
  (wall-cap, memout, canceled-below-budget, missing-telemetry — `classify_
  nonverdict`) when re-attempted from full `S`, and its prior verdict was carried
  forward to satisfy `in_run == recorded`. A single shard attempt would abort it
  too, NOT reproduce `Proven`. So the verifier MUST replicate carry-forward
  faithfully: a goal that aborts environmentally AND is in the seed `S` carries
  `S`'s verdict rather than counting as a mismatch. Only a goal returning a
  DETERMINISTIC verdict (proven/unproven) that DIFFERS from `S` is a real
  mismatch. This mirrors the cold run's own converging round exactly, including
  which members are trust-carried rather than re-derived.

With these two, the scheme is sound AS A PROCEDURE: it is one converging round of
`F`, parallelized, with a loud `union == S` assertion. It confirms `S` is a
fixpoint (to the same degree the cold run's last round does, carry-forward
included). It does NOT confirm `S` is THE fixpoint reached from `∅` (multiplicity,
above) — that remains the cold run's job.

## The throughput win is real for THIS verifier

The tail is 26 goals consuming 94% of solver time. Seeding makes no single goal
faster (byte-identical script), but sharding spreads the 26 across `n` shards —
~26/n each — so each shard's wall time ≈ (26/n)·(up to the cap) + fast goals.
`n=8` puts ~3 slow goals per shard, fitting a normal CI window. That is the value:
a fast, parallel, every-push confirmation that `S` is self-consistent.

## Contract (normative; the blind implementer builds to this)

- `oathrs prove --shard i/n --hints outcomes.json <all files>`; `i ∈ 0..n`.
- ALL files are parsed and elaborated regardless of shard. **Elaboration failures
  stay GLOBAL** — a shard hiding an elaboration error because the broken definition
  fell outside it turns a broken corpus green. Single most important invariant.
- Only proof EXECUTION is partitioned. `recorded` is INITIALISED from the `proven`
  set carried by `--hints outcomes.json` (already parsed by `read_outcomes`
  main.rs:499, currently discarded by `cmd_prove` main.rs:251), not accumulated.
- Shard membership is `first_64_bits(definition_hash) mod n` — a normative key any
  runner reproduces. NOT input-file position or discovery order.
- Every property-bearing function definition belongs to exactly one shard; none skipped or attempted twice. A property-free definition has no proof work and lies outside the partition.
- **Carry-forward on abort:** a goal in the seed `S` that returns an environmental
  ABORT (any `classify_nonverdict` reason) carries `S`'s verdict; it is NOT a
  mismatch. Only a deterministic verdict differing from `S` is a mismatch. Mirror
  `prove.rs:2564`.
- The seed `S`'s identity (a hash of the proven set) is part of campaign identity:
  changing `S` must change what the run reports it verified.
- `n = 1` is exactly the unsharded seeded verifier. Ordering within a shard is
  unconstrained; output sorted by (definition-hash, property-index).

## Acceptance test (non-negotiable)

1. Take `S` = the committed `fixtures/prove/outcomes.json` proven set.
2. Run every shard for several `n` (1, 3, 4, 8) AND a second assignment function.
3. Merge shard verdicts, applying carry-forward-on-abort against `S`.
4. Assert EXACT per-goal equality with `S` — status AND diagnostics — the merged
   deterministic verdicts, with environmental aborts carried forward.
5. Assert each definition appears exactly once across the shard set.
6. Assert an injected elaboration error fails the run GLOBALLY, from every shard,
   including shards not containing the broken def.
7. Assert mutating the seed `S` changes the reported campaign identity.
8. **NEW, from crack A:** assert the verifier FAILS LOUDLY when handed a seed `S`
   that is NOT self-consistent (construct one by removing a member that other
   members depend on). A verifier that assumes rather than checks would pass this.

## Separate finding for the producer (crack A), not part of #98's shard work

The cold `prove_all_with` caps at 8 rounds and commits `S` with no record of
whether `in_run == recorded` was actually reached. A non-converged `S` is
committable and silent. Worth a small follow-up: record convergence (round reached,
or a boolean) in the run's output, so a consumer of `outcomes.json` can tell a
fixpoint from a truncation. The sharded verifier's `union == S` assertion is, as a
side effect, the first thing in the system that would catch a non-converged `S`.

## What is NOT in scope

- Cold-reachability is not sharded; it stays the definition, stays serial, cost
  managed by #139's fingerprint gate + scheduling.
- No §7.2 semantic change; the proven set, the fixpoint, and what PROVEN means are
  untouched. Seeding is the starting condition for a necessary-condition VERIFIER.
- No cost reduction; same solver CPU-seconds, wall time only.

## Blind-dispatch boundary

The `--shard` flag, the seed-from-`proven` initialisation, the shard-assignment
key, and the carry-forward-on-abort merge rule are the blind implementation,
derived from this contract + SPEC + fixtures, no reading of `oath/`. The claim
scoping and the two cracks above are DESIGN and belong in the spec/this file; they
are not re-derived by the blind subject.

## Provenance

The purity confirmations and the two cracks (A: unenforced convergence; B: abort
carry-forward) came from an adversarial review dispatched to refute the original
theorem. The original doc overstated an unconditional per-goal identity; this
version states the verifier form the review showed is actually sound.

## The attempt-position cap: measured, then WITHDRAWN

An attempt-position cap was designed as an answer to the campaign's one
unrunnable property: bound the §7.2 strategy sequence so no single property can
exceed the runner's ceiling. It was measured and then withdrawn. Two findings
survive it, and they are recorded here because a future attempt would otherwise
rediscover them at the same cost.

**1. A CAP COUNTED IN EXECUTED ATTEMPTS LETS AN OPTIONAL PERMISSION CHANGE A
RECORDED OUTCOME.** §7.2's attempt reuse is a PERMISSION — "a kernel that reruns
every duplicate is equally conformant". Measured over all 383 recorded-proven
properties: the three `merge` properties (`length-adds`, `preserves-counts`,
`keeps-sortedness`) each discharge at EXECUTED attempt 8 with one duplicate
served from the attempt cache, and therefore need NINE solver requests on a
kernel that declines the permission. A cap of 8 counted in executions keeps those
proofs on a reusing kernel and loses them on a rerunning one — two conformant
kernels, different recorded outcomes, which §10 compares exactly. **So any future
cap MUST count sequence POSITIONS (every request for a solver outcome, counted
BEFORE the reuse lookup) or make reuse mandatory.** Counting executions is not a
smaller version of the same rule; it is a different and broken one.

**2. THE WARM DISCHARGING-INDEX DISTRIBUTION, AND WHY IT DOES NOT BOUND THE COLD
PATH.** Over all 383 properties the committed corpus records PROVEN, measured at
the normative budget through the prover's own observer seam, the discharging
executed-attempt index is:

    index  1: 282    index  4:  54    index  6:   4
    index  2:  20    index  5:  18    index  8:   3
    index  3:   2

Maximum 8 (9 in positions). **That is the WARM path — every goal seen at the
settled lemma state — and it does NOT bound the cold path**, which §10's full
re-derivation actually runs. The obvious way to make a cold measurement
affordable is to attempt only the properties that end the run proven, on the
premise that an unproven property contributes no lemma. **That premise is FALSE
and the witness is `e-div` property 0**: it proves TRANSIENTLY under an
intermediate lemma set, contributes its lemma, and is not in the recorded proven
set — so restricting the run hides it. Measured directly by running one
definition's cold inner fixpoint twice, once attempting every property and once
attempting only the eventually-proven ones, and comparing the (epoch, lemma-state
fingerprint) seen before each eventually-proven attempt: `excluded-witness` and
`e-mod` agreed at every point, `e-div` differed at all three. One definition in
three. **A cold measurement restricted this way is unsound; the honest cold
derivation is the multi-hour job.**

The cap was withdrawn because the only value that fits the runner's ceiling is
below a proof the corpus already has.

## What replaced it: an EXCLUSION-SCOPED campaign — now with NOTHING excluded

The mechanism is in place and its exclusion set is EMPTY, which is the
full-corpus case. `scripts/campaign-exclusions.json` names each excluded property
with its identity, its REASON and the CONDITION under which it returns;
`scripts/campaign-subset.py` reads it, derives the executed matrix, refuses an `n`
whose excluded shard holds in-scope work, synthesizes the empty envelope for each
declined shard, and accepts the merge's failure ONLY when the complete mismatch
set is exactly the named exclusions. SPEC §7.5 carries the normative form and says
an empty set leaves the ordinary all-or-nothing rule unchanged.

**IT WAS BUILT TO EXCLUDE `gh-counts` prop 1, AND THE EXCLUSION WAS THEN TESTED
AND WITHDRAWN.** That sequence is the point, so it is recorded rather than tidied.

The exclusion rested on a PROJECTION of ~647 minutes against a 330-minute step
ceiling. (That figure is HISTORY, not a live estimate — it is what the exclusion
was justified by, and the measurement below replaced it.) The artefact's own return condition said to test it before keeping it.

**The first test was wrong, and is recorded so it is not repeated.** It ran shard
140 alone under the DEFAULT 600000ms per-attempt wall cap, finished in 100
minutes, and the property ABORTED. That is a configuration six times more
aggressive than the campaign's, which sets `OATHRS_Z3_WALL_CAP_MS=3600000`, so it
could not speak to the question asked. `codex review` caught it before the
exclusion was removed on that basis. The step timeout was honoured and the
per-attempt cap was not — an easy pair to conflate, and the whole difference.

**The correct test:**

```
shard 140 of 177, alone, OATHRS_Z3_WALL_CAP_MS=3600000
  EXIT=0, ELAPSED 218 minutes
  ae09e70a… prop 1 → unproven
```

218 minutes, inside the ceiling, and a real VERDICT — `unproven`, matching what
`S` records — not an abort. So including the shard verifies the property rather
than merely attempting it.

**One difference from the campaign remains, and it is stated rather than
glossed.** The test ran at `OATHRS_Z3_MEMORY_MB=3000`, the cap this machine
mandates; the CI worker sets 6000. 3000 is TIGHTER, so a memout is not the
untested direction — completing under the tighter cap is the harder case. What is
untested is the reverse: with more memory z3 may explore further on an attempt it
would otherwise abandon, so the shard could take LONGER in CI than 218 minutes
against a 330-minute ceiling. Even then the campaign is not wrong, only slower or
failed: the property is not in `S`, and an abort on a non-member is not a
mismatch. The dispatch is the real test of the remaining 112 minutes of headroom,
and a shard-140 timeout is the signal to restore the entry.

**Why the projection was wrong, twice over.** It extrapolated from two runs CUT
at 283 and 284 minutes that never finished the property — the trap this issue had
already recorded twice. And the attempt-reuse rule landed in `37c074a` had since
taken this property from 20 attempts to 15. The "REDUCED, not SOLVED" verdict on
that work was correct on the evidence then, and is superseded by measurement.

**What the machinery is still for.** It stays because §7.5's rule stays, and
because an empty list is a state a reader must be able to SEE — delete the
artefact and nothing says the campaign covers everything. Its guards are
exercised against a synthetic entry, so the validator does not retire the moment
there is nothing to validate.
