# #68 NARROW milestone — the four transport equations

**THIS IS THE MILESTONE'S TRANSPORT ATTEMPT, AND §11.3'S FALSIFIER FIRED.**
The generator §11.3 requires was built — both kernels emit it, byte-identically,
held to SPEC §7.4 by `make check-bridge-bytes` — and it was RUN at §11.2's
pinned budget and solver. `transport-take-step` did not discharge. §11.3 says an
obligation that does not discharge under exactly those conditions HAS FAILED,
and that the failure of any one of the four transport equations is the condition
under which #68 is DECLINED. That condition is met.

The issue's STATUS is not asserted here and no word of this file changes it —
`gh` is the authority on that, as it is for every issue. What is recorded here
is the falsifier's OUTPUT, which is a fact about this run.

**AND THE DECLINE IS WIDER THAN THE ONE SUBGOAL, BECAUSE THIS RUN COMMITTED TWO
PROTOCOL VIOLATIONS.** They are set out in full below. Their consequence, stated
here so it is not discoverable only by reading to the end: **NO transport
equation is credited as discharged by this run — not `append`, not `length`, not
`drop`.** The three `unsat` results were obtained under an axiom set that was
changed after watching a goal fail, which is the reach §11.3's bound exists to
forbid, and a result obtained past that bound cannot discharge anything whatever
it says.

An earlier draft of this file argued the opposite — that §11.6's probe/attempt
distinction applied, that the falsifier was untouched, and that a future
milestone run remained available. That reading is withdrawn. It rested on the
rotation laws not having been attempted, but §11.3's condition is a disjunction:
the transport equations failing is sufficient on its own, and nothing in §11.3
makes the gate wait for the rotation half. **There is no unrun milestone left on
this branch to appeal to.**

**WHAT SURVIVES, AND IT IS NOT A CONSOLATION PRIZE — IT IS A DIFFERENT CLAIM.**
SPEC §7.4 pins what the obligations ARE and explicitly fixes no solver budget;
the bytes, the two kernels' agreement on them, and the gates holding both to the
document stand entirely independently of what running them returned. Nothing
below impugns the specification. The violations are about what this RUN may
claim, never about what the bytes are.

## Conditions, pinned by §11.2 and matched exactly

| | |
|---|---|
| solver | z3 **4.16.0**, the pinned version — `oath bridge-obligation --prove` REFUSES to run under any other, rather than substituting silently |
| rlimit | **4000000** (`proveDirectRlimit`), prepended OUTSIDE the hashed script bytes |
| harness | the kernel's own `runZ3Budget`, the same one every §7.2 proof attempt goes through |
| wall cap | a VALIDITY guard only — a wall hit is INVALID, never an outcome (SPEC §7.2, #29). None fired |
| corpus | `GOMAXPROCS=3` for the gate runs; irrelevant to the solver, which is single-goal and serial here |

## Outcomes

`oath bridge-obligation --prove`, verbatim:

```
# solver=Z3 version 4.16.0 - 64 bit rlimit=4000000
measure-decreases	unsat
roundtrip2-base	unsat
roundtrip2-step	unsat
transport-append-base	unsat
transport-append-step	unsat
transport-length-base	unsat
transport-length-step	unsat
transport-take-base	unsat
transport-take-step	unknown
transport-drop-base	unsat
transport-drop-step	unsat
```

Per equation. **The column is headed SOLVER RESULT, not verdict, and the
distinction is the whole point of the section below**: a §11.3 verdict is what
this run forfeited, so the table can report only what z3 returned.

| equation | solver result at 4M | rlimit consumed | §11.3 credit |
|---|---|---|---|
| `append` | both subgoals `unsat` | 1119 base, 30253 step | **NONE** — obtained past the bound |
| `length` | both subgoals `unsat` | 1004 base, 1624 step | **NONE** — obtained past the bound |
| `drop` | both subgoals `unsat` | 1392 base, 606196 step | **NONE** — obtained past the bound |
| `take` | base `unsat`, step `unknown` | 1448 base; the step consumes the whole 4000000 | **FAILED** — this is what fired the falsifier |

Deterministic: the rlimit counter is, so three consecutive runs returned
identical consumption figures to the digit.

**A READER WHO TAKES ONLY THE MIDDLE COLUMNS AWAY HAS TAKEN THE WRONG THING.**
Three greens and one red reads like a near miss with one hard case left. It is
not: the three greens carry no §11.3 credit, so the transport half of the
milestone stands at zero discharged equations, and `take` is not the remaining
work — it is the failure that already ended the attempt.

**`unknown` IS NOT `sat`, AND THE DISTINCTION IS THE WHOLE VALUE OF THIS ROW.**
A `sat` would mean the `take` transport equation is FALSE, which would be a
finding about the encoding. `unknown` means z3 4.16.0 did not settle it within
4000000 rlimit. The obligation is not refuted; it is not proved. The recorded
outcome table in `oath/bridge_test.go` pins `unknown` specifically for that
reason — an assertion of "anything but unsat" would have accepted a refutation
as if it were the same news.

## What is established about `take`, and what is NOT

**ESTABLISHED, and it is one sentence: `transport-take-step` returns `unknown`
at rlimit 4000000 under z3 4.16.0, deterministically.** That is the measurement.
It is not a refutation — `unknown` is not `sat`, so nothing here says the
transport equation is false — and it is not a proof of anything about the
equation's truth.

**NOT ESTABLISHED, and an earlier draft of this file claimed it: that the
obligation is TRUE and that a short proof of it exists.** Neither follows from
anything measured. The draft rested on two side experiments, and re-reading them
against what they actually contain is what withdrew the claim:

- a `1 ≤ c ≤ 1 + len t ⊢ extract (unit a ++ t) 0 c = unit a ++ extract t 0 (c-1)`
  check over free constants returned `unsat` cheaply. That is a DIFFERENT
  FORMULA. It mentions no bridged function and no bridge, so it bounds the cost
  of one sequence identity and says nothing about the obligation;
- a "ground version of the step" returned `unsat` cheaply. But making it ground
  meant replacing `(fn_to_seq_Int f1)` by a free sequence and the recursive
  result by another free sequence — which SEVERS the terms from `take`'s and
  `to-seq`'s defining axioms. What was proved is a statement about arbitrary
  sequences that resembles the obligation; it is not the obligation with its
  quantifiers discharged.

So "true with a short proof" was an inference from two things that are neither.
It is the failure this repository names most often — *is this sentence about the
WORLD, or about my TOOL?* — and it was written by someone who had just quoted
that rule. Recorded rather than deleted, because the sentence was persuasive and
the next person to write it will find it equally so.

**A conjecture, labelled as one and load-bearing on nothing:** `take`'s defining
axiom instantiates its own trigger (`(fn_take_Int (- p0 1) (Cons_List_Int_1 p1))`
matches `(fn_take_Int p0 p1)`), so the search may be looping. `append` and `drop`
have the same shape, which is why this is offered as a guess and not a finding.

## The two protocol violations

§11.3's bound: *"An obligation that does not discharge under exactly those
conditions HAS FAILED, and no retry with a larger budget, an added tactic or a
hand-supplied lemma counts as discharging it"*, with one named exemption — the
`seq.len` induction scheme — which neither violation below is.

### Violation 1: the axiom set was changed after watching a goal fail

**The sequence, in the order it happened.** The transport obligations were first
built on §7.4.1's carrier core. `transport-append-step` returned `unknown` at
the full budget. The preamble was then reduced — `of-seq` and the patterned
`ite`-form equation removed — and the goal re-run. It returned `unsat` at 30253.
The reduced preamble became §7.4.4.

**Why that is a reach and not a design decision.** The original defence, kept
here because it is the one a reader will reconstruct: the reduction is §7.2's
own relevance discipline, which already admits to a proof script only what a
goal's footprint reaches; and it was applied uniformly to all eight subgoals
rather than selected per obligation. Both statements are true. Neither answers
the objection.

**§11.3 does not bar tactics that are FITTED; it bars tactics introduced to
RESCUE a failing obligation, and this one was.** The distinction the defence
relies on — uniform versus per-goal — is about whether the reach was *narrowly
aimed*, not about whether it was a reach. And the admitted axiom set is a
search-steering knob in this very kernel, not a neutral packaging choice:
`prove.go` runs a LEMMA-FREE attempt first precisely because a budget-limited
solver is non-monotone in its axioms, with a measured case of a goal provable at
2294 rlimit lemma-free and unprovable at 400M with its admissible lemmas. So
changing which axioms a goal carries, in response to that goal failing, is a
solver-configuration change of exactly the kind the bound names.

**Consequence, which is why this is at the top of the file:** every `unsat` in
this run was obtained under the post-reach encoding, so none of them discharges
anything under §11.3. That includes the three equations whose subgoals both came
back `unsat`.

**What it does NOT taint.** SPEC §7.4's bytes. §7.4 fixes no budget and makes no
claim about what running an obligation returns; the reduced preamble is
specified, derivable from the document, and reproduced byte-identically by a
blind second kernel. A future milestone re-attempt inherits a defensible
artefact and a spent gate.

### Violation 2: the forbidden larger-budget retries

Two runs of `transport-take-step` at rlimit 400000000 — one hundred times the
pinned budget, and the value §11.2 explicitly says "answers a different question
and does not discharge the falsifier". One here under an 1800-second wall clock,
one by the second kernel's implementer under 120 seconds. Neither returned.

They were run as diagnostics and are offered as no part of any discharge, but
§11.3's bound is on the RETRY, not on what the retry is called. Recorded as a
violation rather than as a footnote, because "it was only a diagnostic" is the
form this rule will always be broken in.

### The declaration blocks — not a violation, recorded for completeness

The four bridged functions' declaration blocks were transcribed from the
kernel's own §7.2 translation, via `directAttemptScript` on `append`, `length`,
`take` and `drop` in the committed corpus, rather than hand-derived from the
Oath source. A semantically equivalent but differently spelled axiom would have
made each obligation a claim about a function nothing else in the kernel uses.
This one predates any failure and steered nothing.

## What was NOT done

- **No rotation laws.** None of the 14 was attempted. This does NOT leave the
  milestone's pass condition open: §11.3's condition is a disjunction, the
  transport half already failed it, and running the rotation family could not
  have changed that. Not attempting them is why this run is cheap, not why it
  is incomplete.
- **No §9.4 registry entry.** §11.2 requires entries keyed by definition AND
  instantiation, and requires each to be PROVED. None was created, and under
  violation 1 none could have been: an `unsat` obtained past §11.3's bound is
  not a proof for registry purposes.
- **No further bridge solver experiments.** The run is over. The obligations are
  still executed by `oath bridge-obligation --prove` and by the Go suite's
  recorded-outcome table, but those are REGRESSION GATES over bytes that are
  already fixed — they re-measure a settled fact, and neither is a new attempt
  at the milestone.

## Cross-kernel

`oathrs/src/bridge.rs` was extended BLIND — a dispatched implementer working in
a tree containing `docs/SPEC.md` and `oathrs/` and neither the Go kernel, its
fixtures, nor its gates — and produced all eleven digests byte-identically on
the first run. It found six defects in §7.4's PROSE, all repaired: a sentence
about `define-fun` and bound variables that contradicted §7.4.7/§7.4.8's own
blocks, an opening sentence that excluded `length`, an "and no others" whose
scope contradicted the emission order, an undefined `id`, an unstated
reconciliation between the keying prohibition and the bare-name manifest, and a
self-referential cross-reference. **The bytes were right and the prose was not**,
which is the same result shape the first §7.4 round produced.
