# Tutorial: the guarantee ladder

Oath's one non-negotiable discipline is never dressing a claim up as more certain
than it is. A property doesn't just pass or fail — it sits on a **rung**, recorded
in the definition's metadata, and the whole system is built to keep that rung
honest. This is a tour of the ladder on real definitions from the corpus.

## `tested` → `proven`: the top of the ladder

`rat-add` swears two properties — commutativity and associativity — and they are
**proven**, not merely tested:

```console
$ oath get rat-add
  prop commutes: forall [(x0 Rat) (x1 Rat)]. (== (rat-add x0 x1) (rat-add x1 x0))
  prop assoc:    forall [(x0 Rat) (x1 Rat) (x2 Rat)]. (== (rat-add (rat-add x0 x1) x2) (rat-add x0 (rat-add x1 x2)))
guarantee: PROVEN (all 2 properties, Z3 over unbounded ints) · spec strength 1/1 mutants killed
```

`PROVEN` means Z3 discharged the property for **all** inputs, not that it passed a
sample. Below that is `tested` (all 200 deterministic, hash-seeded cases passed —
strong evidence, not a proof), and below that `asserted` (no properties at all).
A definition earns the highest rung its properties actually reach.

## `falsified`: a property that's just wrong

The corpus keeps *deliberately broken* exhibits, because hiding them would defeat
the point. `bad-reverse` claims reverse antidistributes over append — and it
doesn't:

```console
$ oath verify bad-reverse
✓ prop involution               passed 200 cases
✗ prop antidistributes-over-append FALSIFIED after 6 cases
    counterexample: (Cons -1 Nil), (Cons -8 (Cons 2 (Cons 15 Nil)))
```

One property holds, the other is `FALSIFIED` with a **concrete counterexample**.
A falsified definition is still stored (it's real, and honest exhibits are
valuable) — but `oath build` will refuse to compile an executable from it.

## The honest gap: `tested` but not `proven`

The most important rung is the one people usually paper over. `abs-small` passes
every test and still does not reach `proven` — here in the **Go kernel**:

```console
$ oath verify abs-small
✓ prop bounded-wrongly          passed 200 cases

$ oath prove abs-small
lemma library: 3 from dependencies, 0 from prior runs
· unproven  bounded-wrongly     no direct proof; induction did not discharge
proven: 0/1 properties
```

200 green cases, but the property stays `tested`, not `proven` — the system will
not upgrade a claim it couldn't actually close. That gap *is* the feature: the
verdict tells you exactly how far the guarantee goes. (`examples/undertested.oath`
is the exhibit for why the rungs differ — a sample can miss what a proof catches.)

**The reason is kernel-specific; the verdict is not.** That first line matters:
the Go kernel has three quantified lemmas about `abs` in scope, and under
SPEC §7 a `sat` in the presence of quantifiers is not a refutation — only
"unproven". The independent Rust kernel inlines `abs` instead, so the goal is
quantifier-free, its `sat` IS a genuine refutation, and it skips induction
entirely. Its CLI surfaces neither distinction, printing just the tally. It
takes files rather than store names, so `abs` is supplied from `examples/ints.oath`
(run from the repo root):

```console
$ cargo run --manifest-path oathrs/Cargo.toml --release -- \
    prove <(sed -n '6,13p' examples/ints.oath) examples/undertested.oath
abs	3/3	+++
abs-small	0/1	-
```

Both kernels record `proven: false`, which is why conformance passes. Only the
human-facing *reason* differs, and neither kernel prints a counterexample for
this definition. See `oathrs/DIVERGENCES.md` entry 40.

## Mutation: is the spec even watching?

A green property is worthless if it doesn't constrain the body. **Mutation
testing** asks: if we break the implementation in a type-preserving way, do the
properties notice?

```console
$ oath mutate rat-recover
generated mutation score: 4/4 mutants killed
```

`4/4 killed` means every mutant in this catalogue was caught by some property on
generated cases. That is the strongest thing a mutation run can say, and it is
still narrower than "the spec is tight": the catalogue is a finite set of
single-node edits, and the cases are a finite draw.

A *survivor* is a mutation the properties didn't notice **on generated cases**,
which is not the same as a hole in the specification. Surviving says only that
this campaign's draws didn't distinguish it — a property may still exclude the
mutant on an input the campaign never produced, whether because the value lies
outside the draw range or simply because a finite run didn't reach it. Nothing
about the score tells you which. On a `PROVEN` definition the two come apart
sharply: the corpus's worst score belongs to a definition proven for every
input.

Survivors are reported with their bodies, and `oath mutate --prove <name>` sorts
them against the definition's proven properties: `proof-refuted` (the spec does
exclude it — a finding about the tests), `equivalent` (waived, with a
justification), or `unadjudicated` (nothing settled it — including the case where
every proven property still holds, which is the closest thing to a demonstrated
gap: no PROVEN property distinguishes the mutant).

**Read the dispositions as a work queue, not as a taxonomy.** Each says something
different about what to do next, and two of them say *nothing* — which is the
point, because a survivor list that implies work everywhere manufactures it where
the evidence already says there is none:

| disposition | what it establishes | next action |
|---|---|---|
| `proof-refuted` | a proven property excludes the mutant; the campaign missed it | improve execution reach |
| `unadjudicated — every proven property still holds` | no PROVEN property distinguishes it | **inspect** — prove an existing property, write a new one, or waive |
| `unadjudicated — did not reach a verdict` | the proof attempt was inconclusive — timeout, resource cap, or untranslatable | improve proof support |
| `unadjudicated — not provably total` | totality was not PROVED, so the body cannot be asserted | none — a refusal, not a finding |
| `equivalent` | a human judged it indistinguishable, with justification on record | none — already adjudicated |

The last two rows are easy to misread as weak findings. They are not findings at
all: one is the tool declining to answer rather than guessing, the other is an
answer that has already been given.

The first two are opposite in a way worth being explicit about, since both can
sound like "the spec doesn't catch it". `proof-refuted` needs **no change to the
specification** — a property already excludes the mutant, and waiving it as
"equivalent" would record something false.

`every proven property still holds` is the row that needs looking at rather than
acting on, because it covers three different situations. Adjudication consults
only the PROVEN properties, so an existing UNPROVEN one may already exclude the
mutant — there the work is to prove it, not to write anything. If no property
does, a new one belongs. And if the mutant is genuinely indistinguishable, the
answer is `oath waive` with a justification, which is what makes the `equivalent`
row exist at all.

## Four dimensions, not one

Alongside property proofs, every definition carries three more verdicts, each
tracked as honestly:

- **Termination** — proven structurally (an argument strictly descends at every
  self-call) or via a Z3 measure; otherwise `termination unproven`, never faked.
- **Confinement** — whether the definition can only touch what it's handed
  (capability discipline).
- **Spec strength** — the mutation score above.

The through-line: a verdict means precisely what it says, no more. That's what
makes a proof here worth trusting — and a `tested` here worth *not* over-trusting.
Next: [numbers you can trust](numbers.md), or [the discovery layer](discovery.md).
