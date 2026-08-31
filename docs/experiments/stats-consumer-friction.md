# stats-consumer — a demand list from proving, not from tooling

The flywheel's "depend on Oath, don't improve it" exercise, aimed this time at the
PROVING experience rather than the registry/publishing surface the earlier rounds
covered (webhook, ledger, publish, resolve, hydrate, discovery, strmap). The
consumer is small on purpose: the arithmetic **mean of a list of integers as an
exact rational**, built registry-first on the corpus's `sum`, `length`, `minimum`,
`maximum`. The source is [`stats-consumer/stats.oath`](stats-consumer/stats.oath);
the reproducer is [`stats-consumer/run.sh`](stats-consumer/run.sh), which caps z3
(`OATH_PROVE_RLIMIT` / `OATH_PROVE_WALLCAP_SEC` / `OATH_PROVE_MEMORY_MB`) so a hard
goal fails fast instead of hanging.

## What the evidence is, and what it is not

Every proof outcome below is MEASURED by running `oath prove` under a fixed
resource cap. The two prevalence-shaped claims — "reusing tested code costs you its
lemmas" and "the bounds property is nonlinear" — are decidable from the artifact
and the prover output, not inferred. Where a property does not prove, the claim is
about the PROVER's reach, never about the property's truth: all three properties
PASS 200 randomized cases, so `within-bounds` is a `tested` truth the solver cannot
discharge, exactly like the corpus's honest `spin`/`abs-small` exhibits.

**Registry-first genuinely paid off, and that is the frame for everything below.**
A developer wanting list statistics finds `sum`, `length`, `minimum`, `maximum`
already in the corpus, proven; only `mean` had to be written. So the friction is
not "I had to build it" — it is what proving the one new definition ran into.

---

## 1. A dependency's GUARANTEE LEVEL silently determines what the consumer can prove — and the prover cannot say so — DEMAND

**Worst, because it is invisible and it inverts the registry-first advice.** The
recovery law `mean(xs) * count(xs) = sum(xs)` — the single most useful property of
an average, and one squarely inside Z3's complete real fragment — behaves like
this:

```
$ # reuse sum/length as TESTED (just `put`, not proven): 0 dependency lemmas
$ oath prove mean-r
lemma library: 0 from dependencies, 2 from prior runs
· unproven  times-count-is-sum   no direct proof; induction did not discharge

$ # after proving the SAME dependencies: their laws become lemmas
$ oath prove length ; oath prove sum
$ oath prove mean-r
lemma library: 5 from dependencies, 2 from prior runs
∎ PROVEN    times-count-is-sum   direct (Z3, unbounded ints)
```

The property is identical. The definition is identical. What changed is the
GUARANTEE LEVEL of the dependencies: reusing `sum`/`length` as `tested` hands the
prover ZERO of their laws, so `length(Cons h t) = 1 + length t` — the fact that a
non-empty list's length is nonzero, which the division needs — is not available,
and the goal does not discharge. Prove the dependencies first and the same goal is
`direct`.

**The trap is that the DIAGNOSTIC IS THE SAME in both cases:** `no direct proof;
induction did not discharge`. A consumer cannot tell "my goal is intrinsically
hard" from "I am missing a dependency's lemma because I reused it at `tested`." The
registry-first skill's `explain` step already tells a consumer to check a
candidate's guarantee level for TRUST; nothing tells them that reusing a `tested`
dependency also weakens what THEY can prove downstream, and the prover's own output
points nowhere.

**Consumer hurt: anyone proving a property whose argument routes through a reused
definition.** In the live corpus the standard library is proven, so this is masked;
it bites precisely the registry's selling point — reusing a stranger's `tested`
definition — and it bites silently.

**DEMAND: the unproven diagnostic should name the dependencies whose UNPROVEN laws
would likely have helped** (the def resolves its dependency closure; the ones at
`tested`/`asserted` whose properties are in the goal's vocabulary are the
candidates), OR the guarantee-ladder reporting should connect "this dependency is
only tested" to "so a proof through it will lack its lemmas." Whether that is a
line in `oath prove`'s output, a field in `explain`, or a `--why-unproven` mode is
a design question. What matters is that the causal link — guarantee level of the
input to provability of the output — stops being invisible.

**Evidence class:** the two prove transcripts (tested-deps vs proven-deps) and the
lemma-count line are MEASURED and reproduced by `run.sh`. That the missing law is
specifically `length`'s nonzero-on-cons is HAND-attributed from the shape of the
goal.

---

## 2. The most natural property of an average — that it lies between the min and the max — does not prove: it is nonlinear — TOOL LIMIT

**Second.** `min(xs) <= mean(xs) <= max(xs)` unfolds to `min·count <= sum <=
max·count`, and the `·count` makes it NONLINEAR — Z3's known weak spot. With every
dependency proven (11 lemmas available) it still does not discharge:

```
$ oath prove mean            # deps all proven
∎ PROVEN    empty-is-zero        direct
∎ PROVEN    times-count-is-sum   direct
· unproven  within-bounds        no direct proof; induction did not discharge
proven: 2/3 properties
```

This is a TOOL limit, not a fact about the world: `within-bounds` passes 200 cases
and is true. It is recorded here so the next session does not mistake "the prover
did not discharge it" for "the bound is false" — the distinction this project keeps
having to make. A consumer wanting a PROVEN bound on an aggregate hits it on the
first try, and the honest move is `mean`'s: keep the bound as a `tested` exhibit
rather than weaken it into something linear that no longer says what was meant.

**DEMAND (weak, low actionability): a documented pattern for bounds-over-aggregates**,
or an acknowledgement in the guarantee-ladder docs that a linear-arithmetic proof
fragment cannot reach `sum`-vs-`count·bound` shapes. Not a reason to add nonlinear
solving on momentum — that would change the question from "is the prover faithful"
back to "how much can it prove."

**Evidence class:** the prove transcript is MEASURED. "Nonlinear" is decidable from
the unfolded obligation, shown above.

---

## 3. Composing an Int fold with a ℚ result forces `to-rat` at every comparison, and the seeded-fold shape forces an awkward restatement — ERGONOMIC

**Third, minor.** `minimum`/`maximum` are SEEDED folds returning `Int`; `mean`
returns `Rat`. So the bounds property cannot be stated over a bare `xs` — it needs
a head element to seed the fold, forcing `(Cons [Int] h t)` — and every comparison
crosses the numeric tower, needing an explicit `to-rat` on the Int side:

```
(<= (to-rat (minimum h t)) (mean (Cons [Int] h t)))
```

Neither is wrong — `to-rat` is exact and the seed shape is a deliberate way to make
`minimum`/`maximum` total without an `Option`. The cost is that the property a
consumer wants to write (`min xs <= mean xs`) is three transformations away from the
property they CAN write. Recorded, not escalated: it is the tax of mixing a total
seeded fold with a widening conversion, and both halves are individually the right
design.

**Evidence class:** the source is the evidence; HAND-observed while writing it.

---

## What is NOT friction, recorded so it is not re-derived

- **`oath prove` can hang for minutes on a goal it will not discharge.** The
  unbounded default rlimit (400M) let `within-bounds` run past five minutes. This
  is not a defect — it is the deterministic work budget doing its job — but a
  consumer probing what proves should cap it: `OATH_PROVE_RLIMIT`,
  `OATH_PROVE_WALLCAP_SEC`, `OATH_PROVE_MEMORY_MB`. `run.sh` sets all three, and a
  tight cap turns the five-minute hang into a two-second `induction did not
  discharge`.
- **The mean itself was easy to write and total.** Reusing `sum`/`length`, the ℚ
  division, and the `if length == 0` guard (the rat-recover normalization pattern)
  made `mean` total and its recovery law provable. The datatype slice was never in
  the way — this round's friction is entirely in the PROOF layer, which is the
  point of aiming the exercise there.
