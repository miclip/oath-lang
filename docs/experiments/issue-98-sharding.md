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
