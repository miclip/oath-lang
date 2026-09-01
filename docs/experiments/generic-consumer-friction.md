# generic-consumer — a demand list from ad-hoc polymorphism (#33 B1)

The flywheel's "depend on Oath, don't improve it" exercise, aimed this time at
GENERICS — the dictionary-passing convention (`docs/generics.md`) that no prior
consumer round exercised. The consumer is a generic **run-length encoder over any
`Eq` dictionary** (`generic-consumer/rle.oath`): it threads an `Eq` dictionary
exactly as the shipped combinators do, and asks which of its natural properties
survive being quantified — as B1 quantifies everything — over EVERY dictionary,
lawful or not. The instrument is [`generic-consumer/run.sh`](generic-consumer/run.sh),
which RUNS the kernel (put/prove/eval) under a capped z3.

## What the evidence is, and what it is not

Every outcome is MEASURED. The headline — the natural round-trip property is
FALSIFIED over all dictionaries — is a `put` verdict with a concrete counterexample.
That the algorithm is nonetheless correct is an `eval`. That length-preservation does
not prove is a capped `oath prove`. None is argued from taste.

The convention itself is not the friction, and the exercise confirms it: the `Eq`
dictionary threads cleanly, `eqd` verdicts `confined` like any capability record, and
the falsification below is the design working as intended — "PROVEN means for every
dictionary" is exactly what makes a lawless dictionary a valid counterexample.

---

## 1. B1 cannot constrain a dictionary to LAWFUL, so a generic consumer's most natural property is FALSIFIED — DEMAND (this is the case for B2)

**Headline, and it is not a defect — it is a missing expressiveness the consumer
runs into immediately.** The obvious property of a codec is that it round-trips:
`rle-decode (rle eqd xs) = xs`. State it and the tester FALSIFIES it in two cases:

```
✗ rle   FALSIFIED: round-trips
  counterexample: {eq <fn ... some distinct pairs → true ...>}, (Cons -10 (Cons -3 Nil))
```

The counterexample is a LAWLESS `eq` — one that reports two distinct elements equal.
`rle` then groups them into one run, and `rle-decode` reproduces the run's element,
not the original, so the round-trip fails. B1 quantifies over EVERY dictionary and
cannot say "for a lawful `eq`" — that is B2, deliberately deferred — so the property
a consumer most wants to write is not merely unprovable but FALSE as stated.

And the sting: **the algorithm is correct.** For a lawful `eq`, `eval` shows the
round-trip holding exactly (`run.sh` §3): `rle-decode (rle {eq ==} [3,3,7]) = [3,3,7]`.
So the code a consumer wrote is right; the property is falsified only because it
quantifies over dictionaries the consumer never intended. The strongest property that
DOES survive every dictionary is a weaker one — length-preservation (§3) — because a
run always carries the exact COUNT grouped whether or not the grouping was lawful.

**Consumer hurt: anyone writing a law about a generic operation** — which is the
point of proving generic code. The measured cost is that the natural correctness
property cannot be stated, only a projection of it that loses what the consumer meant.

**DEMAND: B2 — a way to constrain a dictionary to LAWFUL (a verified `Eq`/`Ord`
instance), so a law that depends on reflexivity/antisymmetry can be stated and
proven over the instances that satisfy it.** `docs/generics.md` already names B2
("verified laws") and defers it; this consumer is the grounded reason it is worth
the cost — not "richer would be nice", but "the property I want is FALSIFIED without
it, while the code is correct." Whatever shape B2 takes (a lawfulness obligation on
the dictionary, a refinement over the record, a discovery-layer relation), the test
is whether `round-trips` becomes STATABLE and PROVABLE for lawful `Eq`.

**Evidence class:** the FALSIFIED verdict with its lawless-dictionary counterexample
and the lawful-`eq` round-trip `eval` are MEASURED (`run.sh` §2–3).

---

## 2. `match` had no nested patterns, so any algorithm over a list-of-structs paid a double-destructure tax — RESOLVED

**Second, and language-level rather than generics-specific — but it compounded here.**
A run reached through a list `Cons` could not be destructured in one step:
`(Cons (MkRun n x) t)` was rejected with `pattern binders must be names`, so every
case became two matches, `(Cons r t)` then `(match r ((MkRun n x) ...))`. `rle` and
`rle-decode` both carried this, and any code walking a `List` of records did.

**RESOLVED — nested constructor patterns now elaborate.** `match` accepts a nested
`(Ctor binders...)` in a binder position and desugars it to a fresh binder plus an
inner `match`, so the natural `(Cons (MkRun n x) t)` is available and `rle.oath` now
uses it. The desugaring is **identity-preserving**: it produces the SAME core AST as
the hand-written two-step form (binder names are metadata, so the fresh name changes
no hash), which is why `rle`/`rle-decode` keep their pre-change hashes and no corpus
or conformance fixture moves.

**Scope, stated exactly — it is single-constructor (product) destructuring, not
general pattern matching.** The nested constructor MUST be the sole constructor of its
datatype (`MkRun`, `Cons`-of-record, any product). A nested pattern over a SUM type is
REFUSED by name — `(MkWrap (Some x))`/`(MkWrap (None))` would each desugar to a
single-arm inner match (non-exhaustive) and duplicate the outer `MkWrap` arm; merging
sibling arms under one outer constructor is pattern-matrix compilation, deliberately
out of scope. A literal in a pattern position (`(MkRun 5 x)`) is likewise rejected.

**Specification.** This is identity-preserving surface sugar, exactly like the list-
and string-literal sugar SPEC §1.4 already documents, so it changes no hash, verdict
or semantic — and no corpus definition uses it, so it imposes nothing on cross-kernel
conformance. SPEC §1.4 records it as OPTIONAL surface sugar (a conforming elaborator
MAY reject nested patterns; one that accepts them MUST use the desugaring above),
which is why the second Rust kernel is unaffected.

**Evidence class:** identity preservation (flat vs nested → identical hash), deep and
multiple-field nesting evaluating correctly, sibling-binder-capture avoidance, the
sum-type refusal, and the literal-binder rejection are covered by
`oath/match_nested_test.go`; the publish-transformation reaching nested binders by
`oath/publish_closure_test.go`; and `rle.oath` exercising the nested form is MEASURED
by `run.sh` §1.

---

## 3. A generic aggregate's provable-looking property does not PROVE — structural induction cannot generalise over the run structure — TOOL LIMIT

**Third.** Length-preservation — the property that DOES survive every dictionary —
still does not prove:

```
· unproven  decode-preserves-length   no direct proof; induction did not discharge
proven: 0/1 properties
```

Its dependencies (`length`, `append`, `replicate`, `rle-decode`) are all PROVEN, so
the prover's missing-lemma note (the stats-consumer demand-1 diagnostic) stays
SILENT — correctly reporting that what remains is an INTRINSIC wall, not a missing
lemma. The obligation is an aggregate (`length`) over a nested recursion whose
inductive shape depends on the tail's already-encoded run, and z3's structural
induction cannot generalise it. This is a TOOL limit, not a fact about the world: the
property PASSES 200 cases and is true, and `run.sh` keeps it as a `tested` exhibit
rather than deleting a true statement the prover cannot yet reach.

**DEMAND (weak, low actionability): none pressing.** Recorded so the next session
does not read "did not prove" as "is false", and as one more data point that
aggregate-over-nested-recursion is a recurring shape at the edge of the prover's
reach (compare the stats-consumer's `mean` bounds). Not a reason to add induction
tactics on momentum.

**Evidence class:** the capped-z3 unproven verdict, and the silent note with
dependencies proven, are MEASURED (`run.sh` §4).

---

## What is NOT friction, recorded so it is not re-derived

- **Dictionary passing works, and the falsification is the design, not a bug.** The
  `Eq` dictionary threads as an ordinary parameter, `eqd` verdicts `confined`, and the
  tester supplying a lawless dictionary is EXACTLY the "PROVEN means for every
  dictionary" guarantee `docs/generics.md` describes. The exercise found no defect in
  the convention — it found the edge of what B1 can EXPRESS, which is B2's job.
- **The missing-lemma diagnostic behaved correctly on a foreign consumer.** With every
  dependency proven, the note stayed silent on an intrinsically-hard goal — a live
  cross-check that the diagnostic distinguishes "hard goal" from "missing lemma"
  outside the case it was built for.
