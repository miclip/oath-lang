# #65 — which of the four rungs is actually left?

**Status: MEASURED, then RUNG 3 MEASURED AND DISPOSED. The recommendation
stands — NARROW #65 SHARPLY — and building rung 3 CONFIRMED it: both halves
connect ZERO pairs on the committed corpus.** Its PROOF half (`find --implies`)
shipped, because the prover filters that surface so liberal cross-type matching
is free of downside; its HASH half (`find --spec`) was DECLINED, because it
would churn a PERSISTED discovery/demand key for zero measured yield. One rung
was already shipped, **every rule the centerpiece lists as remaining was already
implemented**, and what is genuinely left after this is the e-graph data
structure itself, which this corpus cannot show working.

The "BUILT" subsection below records both dispositions and why they differ. The
issue states no falsifier — the standing instruction
requires one before starting architectural work — so this supplies one per rung:
**what would this rung buy, measured on the committed corpus?**

## Rung 2, cross-type proof-implication: ALREADY DONE

The issue says `find --implies` "currently restricts candidates to an EXACT
signature match". It does not. `apiFindImplies` admits any candidate whose
signature is equal up to primitive leaves and re-types the query property to it:

```go
if !bytes.Equal(tyBytes(d.Ty), qsig) {
    sub, ok := crossTypeCompatible(qd.Ty, d.Ty)
    if !ok { continue }
    qp = Prop{Binders: crossTypeRetypeBinders(qp.Binders, sub),
              Body:    *crossTypeRetypeBody(&qp.Body, sub)}
```

`docs/discovery.md` documents it as shipped. The rung is stale.

**The `Body` half of that snippet is RUNG 3, not rung 2, and it landed after
this measurement was taken.** When rung 2 was assessed the line read
`Body: qp.Body` — binders only — which is precisely the asymmetry the rung 3
sections below measure and dispose of. The snippet is shown in its CURRENT form
because a reader checking the claim reads the code, not this file's timestamp;
what the measurement established is unchanged either way, since rung 2's
question was whether cross-type admission existed at all, and it did.

## Rung 1's rule list: FOUR OF FIVE ALREADY DONE

The issue lists as remaining: unit/identity, idempotence, `if c x x = x`,
eta-reduction, `neg (neg x) = x`. Each was tested by putting a variant into a
throwaway store beside a base it is equal to, and asking `--equiv`:

| rule | variant tested | connected today? |
|---|---|---|
| unit `x + 0` | `(+ (+ (* 2 a) b) 0)` vs `(+ (* 2 a) b)` | **yes** |
| unit `x * 1` | `(+ (* 2 a) (* 1 b))` vs `(+ (* 2 a) b)` | **yes** |
| idempotence | `(and p p)` vs `p` | **yes** |
| `or p false` | `(or p false)` vs `p` | **yes** |
| double negation | `(neg (neg a))` vs `a` | **yes** |
| eta | `(map (fn [(x Int)] (f x)) xs)` vs `(map f xs)` | **yes** |
| `if c x x` , VALUE condition | `(if c a a)` vs `a`, `c` a Bool binder | **yes** |
| `if c x x` , COMPUTED condition | `(if (< a 0) a a)` vs `a` | no — and deliberately |

**All seven are implemented.** The last row is not a gap: `ifSelect` in
`canon.go` drops an identical-branch condition only when the condition is
already a VALUE, and says why —

> a condition that is a COMPUTATION is part of what the term does: dropping a
> divergent one turns a non-terminating term into a terminating one. That is
> removing divergence, not preserving meaning.

A first draft of this record tested only the computed form, found it
unconnected, and reported the rule missing. It was measuring a soundness
restriction. Review caught it; the value-condition fixture is committed beside
the other. `docs/experiments/issue-65-rungs/`
carries the variants and `run.sh`, which re-asks every row above and includes
commutativity as a CONTROL — a rule known present before this run, so a script
that reported "connected" for everything would be visibly wrong.

## Rung 1's real e-graph: its motivating case is served by `--implies`

The issue's argument for the full technique rests on distributivity:

> **Non-confluent rules → an actual e-graph.** Distributivity
> (`a*(b+c) ≡ a*b + a*c`) runs both directions with no obvious normal form.
> This forces the real technique (egg).

`--equiv` does miss it, as stated. **`--implies` does not.** One query, both
directions, lemma-free:

```
· factored-equals-expanded
    d-expand           ← provably satisfies it (direct (lemma-free))
    d-fact             ← provably satisfies it (direct (lemma-free))
```

So the equivalence the rung exists to reach is reachable today. What survives is
the issue's own tradeoff paragraph — the e-graph is O(1) dedup across a whole
store where SMT is a solver call per candidate — and that is an argument about
SCALE, not capability.

**The scale argument cannot be measured on this corpus.** `--equiv` was run
against all 238 live names: **223 functions, ZERO equivalences found.** That is
not a defect — a curated corpus has no redundant definitions, which is exactly
the condition under which store-wide dedup buys nothing. The use case is "is my
NEW implementation already in the commons?", and the candidate comes from
outside.

## Rung 3, body-embedded-type cross-type matching: its PROOF half is at most 21 pairs

**A first draft of this section said rung 3 would connect nothing, and that was
wrong.** It reasoned from names — the corpus has 114 Int definitions and only
twelve mentioning Rat or Float, none of them a counterpart of a blocked
definition, so (the argument went) there is nothing to bridge to.

That is an argument about SEMANTIC counterparts. Rung 3 is about SIGNATURE
admission, and the distinction between the two is the one this project keeps
having to make: `crossTypeCompatible` is deliberately wider than truth "because
the PROOF is the filter, not the signature".

**And the measurement already existed, committed, in
`oath/rung2_measure_test.go`.** `TestRung2CorpusCensus` reports:

    corpus: 223 live-named func defs, 622 query properties (353 with body-embedded types)
    pairs admitted — exact-signature: 978 ; signature-compatible: 1314 ; DELTA: 336
    delta pairs that TYPECHECK after re-typing binders: 89
    rejected as ill-typed (rung-3 residue): 247

**247 is NOT rung 3's population, and reading it as one was the second mistake
this section made.** It counts every signature-compatible pair `checkDef`
refused — for any reason. Rung 3 threads type generalization through a property
BODY's type arguments, so it can only unblock a pair whose body HAS them.
`TestRung3UpperBound`, added for this record, splits them — **and PINS both
figures**, so a corpus change that moves them fails the test rather than quietly
leaving this section sizing the rung from stale numbers:

    ill-typed delta pairs (the census's number)                     247
      query body carries type arguments — RUNG 3 UPPER BOUND         21
      rejected for other reasons — rung 3 certainly cannot help     226

**At most twenty-one — of ONE HALF of the rung.** Rung 3 reaches two surfaces:
the PROOF path (`--implies`, where cross-type candidates are rejected by
`checkDef`) and the HASH path (`propHashGeneral`, where a body-embedded type
keeps a law same-type-only even when nothing is ill-typed). The 21 bounds the
proof half. **The hash half is a separate population and is not measured here** —
pairs that typecheck perfectly well and simply never collide. A third reading of
this record claimed 21 sized the rung; it sizes half of it.

And the bound is not even the population of that half. Carrying body type
arguments is NECESSARY for the threading to help and is not SUFFICIENT: a
substitution mapping `Int` to a candidate's type VARIABLE leaves binders
non-concrete, and retyped `Bool` binders feeding `if`/`not` still require
`Bool`. Neither is repaired by rewriting body types. Deciding how many of the 21
actually become well-typed means applying the substitution — which is
implementing the rung.

Four readings of this population have now been recorded, and the sequence is the
point: **zero** (from names, wrong), **247** (from a rejection counter, wrong),
**21** (measured, but a necessary-not-sufficient bound), **≤21 for the proof
half only** (the hash half never counted). Each was a number my reasoning or the
implementation produced, read as a fact about the claim — the substitution this
repo documents and I made four times in one section.

**Its YIELD is unknown and cannot be measured without building the rung.** What
CAN be measured is the yield of the analogous population one rung down, and it
was: `TestRung2CorpusYield` proves one z3 goal per typed delta pair.

    delta proof attempts: 89 (ill-typed skipped: 247) in 45m50s
    [OATH_RUNG2_YIELD=1, gated and long-running; not pinned by a gate, because
     45 minutes of solver time cannot sit in the push path]
      proven    12
      refuted    3   (countermodel — the candidate does NOT satisfy the law)
      unknown   74   (solver declined — NOT a disproof)
    MISSED IMPLICATIONS at this budget: 12

**A delta population is NOT worthless.** Twelve real cross-type implications,
provable and unreachable by the exact-signature baseline — and they are exactly
the cross-primitive matches a first draft of this record argued could not exist:

    abs#1     -> f-mul-id        (Int law reaching a Float definition)
    e-div#3   -> rat-mul         (Int reaching Rat)
    e-mod#4   -> rat-recover
    max2#0    -> rat-recover
    rat-add#0 -> apply2, max2    (Rat reaching Int)

So the corpus DOES contain cross-primitive implications; the draft's name-based
reasoning missed them because names are not how the prover finds things.

**Extrapolating a rate to rung 3's 247 would be an extrapolation, not a
measurement**, and the two populations differ in kind — rung 3's are ill-typed
today precisely because their bodies carry types, which is a different obstacle
from a re-typed binder. What the rung-2 result establishes is a favourable
PRIOR, not a forecast: 13% of one delta population proved, and 83% did not
settle either way at a ten-minute-per-goal budget.

Applied to rung 3's ceiling of twenty-one, a 13% rate is about three finds —
and 83% of rung 2's goals did not settle at all, so the realistic figure is
lower, over a population that is itself an upper bound. That is the honest size
of the rung: bounded, small, and not established to be non-zero.

(A textual census run for this record reported 341/134 where the committed test
reports 353 properties. The committed test traverses the AST; the draft grepped
printed output. The committed number is the authority and the draft's is
withdrawn.)


### BUILT — proof half shipped; hash half DECLINED, both measured at ZERO

Rung 3 splits across two surfaces, and the two get different dispositions
because they carry different costs. Both were measured at zero yield on this
corpus; only one is worth shipping.

- **Proof half — SHIPPED** (`oath/api.go`). `apiFindImplies` threads the
  cross-type substitution through the query property's BODY
  (`crossTypeRetypeBody`), not only its binders, so a body-embedded `Int`
  annotation retypes to a candidate's `Rat`/`Float` and the augmentation
  typechecks. `checkDef` still gates well-typedness and Z3 still gates truth, so
  this admits more WELL-TYPED augmentations, never more proofs — the PROVER is
  the filter, which is exactly why this surface can afford liberal cross-type
  admission. `TestRung3ProofHalfConnects` pins how many of the 247 ill-typed
  delta pairs become well-typed with body retyping: **0** (the 21 that carry
  body types all fail the binder-concreteness obstacle too). Both new body
  traversals are iterative — `crossTypeRetypeBody` over the term and
  `retypeTyBySub` over inferred `TyArgs`, which `checkDef` can populate from
  admitted (not source-bounded) stored types — so a deep query cannot overflow
  the host stack (`TestRung3DeepInputIsIterative`, #149 class).

- **Hash half — DECLINED** (`propHashGeneral`, `find --spec`). It would
  generalize types embedded in a property BODY so two laws identical up to a
  body-embedded type share one content-hash key. It was BUILT and measured
  first: `TestRung2CorpusCensus` reported **0** typed delta pairs surfaced —
  the corpus has no cross-type LAW TWINS (the same law stated at two primitive
  types), so nothing collides. Then two costs surfaced that the zero yield does
  not justify paying:
  - **`propHashGeneral` is a PERSISTED key.** `demandRecord` (SPEC/#75's
    coverage instrument, enabled on the hosted store) keys `discovery-demand` by
    it. Changing the encoding strands historical demand counts — an identical
    future miss creates a new record instead of incrementing the old, splitting
    rankings and dropping established demand below the reporting floor.
  - **It reopens a false-collision.** Generalizing body types brings scoped type
    variables into scope alongside generalized primitives, and keeping them in
    one variable namespace makes `[Int, Int]` collide with `[var0, Rat]` — a
    false `find --spec` match. Fixing that needs a distinct encoding, which is
    itself the persisted-key change above.

  So the disposition is not "not yet" but "not here": a raw persisted hash key
  should not churn for zero measured yield. `docs/discovery.md` records the
  `--spec` surface as deliberately binder-only for this reason.

**The asymmetry between the two surfaces is PRINCIPLED, not a gap.** The
`--implies` surface is filtered by the prover, so admitting more cross-type
candidates costs at most solver time and never a false result — liberal
matching belongs there. The `--spec` surface is a raw content-hash key,
persisted and used for demand aggregation, so stability is worth more than the
extra reach — especially at zero measured yield. Both were built far enough to
MEASURE (0 and 0); one shipped, one was declined on cost, and neither was
decided by taste.

## Rung 4, fresh-spec ergonomics: measured non-friction

The rung calls the dummy body "a little ironic". Across the blind runs this
session — #175's seven-intent run and #176's two demand-5 runs —
**twenty-six committed query files were written by readers who had never seen
the corpus, and not one reported the dummy body as friction.** (Nine under
`issue-175-shapes/blind/subject-queries/`, seventeen under
`issue-176-compositions/blind/queries-run*/`. The committed files are a subset
of what the subjects wrote; the reports list more that were not preserved.)

It is also not what a first draft of this record called it — load-bearing for
the signature-probe technique. It is not: the probe needs the SIGNATURE, which
is declared separately; auto-synthesis would remove only the body token and
would leave probing intact. So the rung is a real, small ergonomic win with no
measured demand behind it.

## Recommendation

**Narrow #65 to what is left**, which is much less than it says:

- rung 2 — **close as done**, and record it, since the issue asserts the
  opposite of the code;
- rung 1's rule list — **close as done.** All seven are implemented; the one
  that looks absent is a stated soundness restriction on computed conditions;
- rung 1's real e-graph — **keep, with a TRIGGER rather than a plan**: a corpus
  containing redundancy, or an external candidate to dedup against. Its
  motivating example is already served by `--implies`, and its remaining
  argument is scale, which 223 non-redundant definitions cannot exercise;
- rung 3 — **proof half SHIPPED, hash half DECLINED; both measured at ZERO.**
  The proof half (`find --implies`) connects 0 of the 247 ill-typed delta pairs
  (`TestRung3ProofHalfConnects` pins 0) and is worth shipping because the prover
  filters that surface. The hash half (`find --spec`) surfaces 0 too, and was
  declined: `propHashGeneral` is a persisted demand key, so churning it for zero
  yield costs more than the extra reach. The "BUILT" subsection above is the
  record; the split is principled, not effort;
- rung 4 — **keep as small**, with the note that seventeen blind queries
  produced no complaint.

## What this does NOT establish

- **That the e-graph is not worth building.** It establishes that this corpus
  cannot show it working, which is a different claim and is the reason the
  recommendation is a trigger rather than a decline.
- **Rung 3's HASH half as a FORECAST for other corpora.** It is now BUILT and
  measured at 0 on THIS corpus (no cross-type law twins), but that is a fact
  about the committed exhibits, not a claim that body-type cross-type matches
  are rare in general — a corpus with a law stated at two primitive types would
  exercise it. The ≤21 proof-path bound is superseded by the built measurement
  (proof half connects 0).
- **That the ZERO yield generalizes beyond this corpus.** Both halves are
  measured at 0 HERE — the proof half shipped and pinned, the hash half
  measured before being declined — because the corpus has no cross-type law
  twins. That is a fact about the committed exhibits, not a claim that such
  twins are rare in general. Were one to appear, the proof surface would already
  reach it; the spec surface would need the declined hash change, whose cost
  (a persisted-key migration) is what the decline weighs against, and which a
  real twin would re-open on evidence rather than on taste.
- **That the seven rules tested are all the rules.** They are the ones the issue
  names. A rule it does not name could still be missing, and nothing here
  samples for that.
- **That the computed-condition restriction is optimal.** It is sound and
  stated. A totality analysis could safely drop the condition where the
  computation is known total — `terminationOf` already classifies that — and
  nothing here measures whether such a case exists in the corpus.
- **That zero equivalences means zero redundancy.** `--equiv` sees only what its
  rules express; two genuinely equivalent definitions outside those rules would
  also report zero. The corpus being curated makes redundancy unlikely, but that
  is an expectation, not a measurement.
