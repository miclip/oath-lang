# #175 — can documentation lift the SATISFIED rate, or is machinery needed?

**Status: MEASURED, AND THE FALSIFIER IS NOT DISCHARGED. The recommendation is
to SHIP THE DOCUMENTATION and leave #175 OPEN** pending the one measurement that
would settle it.

The evidence is consistent with declining — the rate more than doubles through
phrasing alone with the tool unchanged — but it does not establish the issue's
condition, and the gap is not a technicality. See "The verdict, narrowed" at the
end.

Nothing was built. No new mode, no ranking layer, no translation machinery.

## The falsifier, quoted rather than paraphrased

#175 states its own "no change required" condition:

> If the SATISFIED rate can be lifted by better *documentation* of how to phrase
> a query — worked examples of the shapes the corpus uses — rather than by
> machinery, then this is a docs problem and should be declined as an
> engineering one. The falsifier record's per-intent table is the baseline to
> beat: 2 of 7.

## Result: 2 of 7 becomes 5 of 7, with no change to the tool

| # | intent | baseline | re-phrased | what changed |
|---|---|---|---:|---|
| 1 | report a required config key the host did not supply | no | **yes** | RETURN shape: `(List Str)` → `Str` |
| 2 | read a request header by name, with a fallback | yes | yes | — |
| 3a | scan a byte body for a JSON string value | no | no | fragment-blind |
| 6 | make a field safe to splice into a delimited record | no | no | fragment-blind |
| 9 | a prefix match that only matches at a delimiter | yes | yes | — |
| 11 | test whether a list of `Str` contains an element | no | **yes** | ABSTRACTION: value → predicate |
| 12 | take the longest prefix whose elements pass a test | no | **yes** | POLYMORPHISM: `[a]` on the query definition |

The three recovered intents are all SHAPE failures on three different axes; the
two that stayed are both the SAME failure, and it is not a shape failure.

**THE TOOL DID NOT CHANGE.** `--spec`'s fallback hint about polymorphic type
application landed at d2dc407 on 2026-07-28, three weeks before the baseline run
(63fa523, 2026-08-16). Every hit below comes from a definition that was already
in the corpus at the baseline. The whole difference is how the query was
written.

## The three axes

**Return (intent 1).** The baseline asked for the missing keys, `(List Str)`.
`config-missing` returns the first one, `Str`. The baseline query returned
nothing AND no signature-compatible fallback, so the output was
indistinguishable from an empty corpus. Changing only the return type:

```
· nothing-needed-nothing-missing    config-missing  ← provably satisfies it
· a-missing-key-is-reported         config-missing  ← provably satisfies it
```

**Abstraction (intent 11).** The baseline asked for
`(-> Str (List Str) Bool)`. `any` is `(-> (-> a Bool) (List a) Bool)` — the
fixed value generalized to a predicate. Restated in that shape:

```
· a-passing-head-makes-it-true
    any    ← provably satisfies it (direct (lemma-free))
    1 REFUTED    all    countermodel: <fn {20→false 0→false -2→false} else true>, 13, (Cons 0 Nil)
```

**THIS ONE CONTRADICTS THE BASELINE RECORD, WHICH IS WHY IT IS THE MOST
IMPORTANT RESULT HERE.** `issue-74-falsifier.md` concluded of intent 11:

> No mode can reach it, because the satisfying artifact has a **different
> structural shape** from the intent … This is a gap distinct from
> law-statement and it survives any improvement to how laws are written.

It does not survive. What the baseline varied was the LAW; what reaches `any` is
varying the SHAPE, and the two were treated as one thing. The corrected claim is
narrower and still real: *generalizing primitive leaves does not bridge an extra
parameter* — true, and it says nothing about whether the caller can supply the
right shape themselves.

**Polymorphism (intent 12).** The flagship demand: `bytes-until` was written by
hand while `take-while` sat PROVEN. Monomorphic, it finds nothing even with the
law copied verbatim from the target. Declared `[a]`:

```
· every-kept-element-passes
    filter        ← provably satisfies it (induction on binder 1)
    take-while    ← provably satisfies it (induction on binder 1)
    1 REFUTED     drop-while    countermodel: <fn {-2→false -14→true 19→true} else false>, (Cons 4 (Cons 8 (Cons -2 Nil)))
```

**AND THE BASELINE ATTRIBUTED THIS TO THE WRONG HALF, which review caught and a
2x2 settled.** `issue-74-falsifier.md` says its (b) and (c) "state the same
mathematical law and differ only in whether the query's own recursion carries a
`[Int]`". They also differ in whether the DEFINITION is polymorphic, and that is
the half that matters:

| query definition | the law's recursive call | result |
|---|---|---|
| monomorphic | `(wanted p xs)` | nothing |
| `[a]` | `(wanted [Int] p xs)` | `filter`, `take-while` proved |
| `[a]` | `(wanted p xs)` | `filter`, `take-while` proved |

The type application is INFERRED, so writing it changes nothing. A first draft
of the documentation taught it as mandatory — a made-up rule, derived from
reading the baseline rather than from running the third row.

## The two that stayed, and why no phrasing reaches them

Both are #177's measured blind spot. `--implies` appends the query law to a
candidate and proves it; a body the translator cannot build never reaches a
solver, so the mode reports NO VERDICT — which the CLI already labels *"a limit
of this prover, NOT a fact about the definition"*.

```
intent 6   3 REFUTED (config-key, gh-group, shout) · 1 NO VERDICT   ← the target
intent 3a  2 NO VERDICT
```

Shape does not help, because the obstacle is the CANDIDATE's body, not the
query's signature. #177 measured this at 12 of 194 candidates and DECLINED it;
`--equiv` reaches them but takes an implementation rather than a law.

**BUT `--details` NAMES THEM, which the falsifier's scoring hides and which
review surfaced here.** The same intent-6 run with the flag:

```
1 NO VERDICT — the prover did not settle it (a limit of this prover, ...)
    record-field       "lam" terms are outside the provable fragment
```

That is the target, named, with the reason. It is still not SATISFIED — nothing
was proved, and the scoring above is unchanged — but it means the practical gap
for a fragment-blind target is smaller than "you must write the implementation":
the caller gets a name to read. The procedure in `discovery.md` puts `--details`
ahead of `--equiv` for exactly this reason.

## Circularity, excluded the way the baseline excluded it

The baseline dropped intent 5 because "the query came out AST-identical to the
target's own law". The same rule is applied here, and it cost one result:

- Intent 11's first draft stated `(== (wanted p (Cons x xs)) (or (p x) (wanted p xs)))`,
  which IS `any`'s own `cons-step` under self-substitution. It hit, and it was
  discarded; the scored law is `(or (not (p x)) (wanted p (Cons x xs)))`, the
  author's own wording, which `--spec` misses and `--implies` proves.
- Intent 3a's natural phrasing — "the scan never keeps a quote byte" — comes out
  AST-identical to `json-string-value`'s own `no-quote`, so `--spec` would hit
  it circularly. It is scored unsatisfied, on `--implies`, as the baseline
  scored it.

## Reproducing

`docs/experiments/issue-175-shapes/` holds every scored query as a file and
`run.sh` re-asks them against a temporary copy of the committed corpus, printing
each mode's whole answer rather than the lines that agree with this write-up.
`transcript.txt` is one run.

**All seven, including the two that already passed.** A first draft ran only the
intents that CHANGED, which cannot verify a numerator and would not notice a
prior hit regressing. Adding them back was not a formality: intent 9's law was
reconstructed with its arguments the wrong way round, and in that form
`str-prefix` PROVES it — the hits looked reproduced while the REFUTATION that is
the whole point of intent 9 had quietly vanished. A control that only confirms
is not a control. Budget ~20 minutes: `--implies` proves, so it is
slowest exactly where nothing proves, and intent 3a alone runs over ten minutes
before answering NO VERDICT.

**It reproduces the QUERIES, not the STUDY.** Whether a given shape reaches its
artifact is decidable and is what the documentation's claims rest on. Whether a
caller who does not know the target arrives at that shape is the open half, and
no script settles it.

## What this does NOT establish

- **That an author who does not know the target will follow the procedure.**
  This is the load-bearing limit and it is not a small one. The runs above were
  performed by someone who had read the baseline record and therefore knew which
  artifact each intent was supposed to reach. What is established is that the
  shapes exist, that the search over them is FINITE and directed, and that it
  terminates in a proof for 3 of the 5 previously-failing intents. What is NOT
  established is the behavioural claim — that a caller starting from the intent
  alone arrives there. Discharging that needs a reader who has not seen the
  targets, and it is the natural next measurement if this issue is reopened.
- **That 5 of 7 generalizes, or that an empty result is USUALLY a shape
  problem.** Seven intents from one application's friction log, chosen BECAUSE
  they had artifacts to find — so "the corpus has nothing" was impossible here
  by construction, and that is precisely the case a real caller cannot exclude.
  A first draft of the documentation stated the general likelihood anyway; it
  now names the sample. This is the corpus-versus-phenomenon distinction, met
  in the one place where the sampling bias is built into the selection rule.
- **That the axes are exhaustive.** Three axes were enough for these three
  failures. Nothing here samples for a fourth, so exhausting them does not
  establish that a corpus has no artifact — and the documentation says so
  rather than presenting them as a closed search.

## The verdict, narrowed

**The issue's condition is about a CALLER, and this run had an ORACLE.** It asks
whether the SATISFIED rate can be lifted *by documentation*; documentation acts
on someone who does not know the answer, and every query above was written by
someone who had read the baseline record and therefore knew which artifact each
intent should reach. What was measured is that the right shapes EXIST, are
reachable with no tool change, and are found by varying three named axes. What
the condition asks is whether a caller starting from the intent alone gets
there. Those are different populations, and reporting the first as the second is
the substitution this repository's own guidance is built around.

So:

    ESTABLISHED    3 of the 5 previously-failing intents are reachable by
                   re-shaping the query, with the tool unchanged and every hit
                   proved rather than hash-matched. The two that remain are
                   #177's fragment limit and no phrasing reaches them through
                   `--implies`.
    NOT ESTABLISHED that documentation lifts the rate. That needs a reader who
                   has not seen the targets, given the intents and the new
                   section, scored the same way.

**Ship the documentation regardless** — it is correct, it is derived from
measurement, and two of its claims were wrong until review caught them. **Leave
#175 open**, because a blind-reader run is a cheap measurement and it is the one
that decides the issue. Closing it on this run would be closing it on the
evidence the run explicitly says it does not have.
