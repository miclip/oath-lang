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
every test — and Z3 still can't prove it:

```console
$ oath verify abs-small
✓ prop bounded-wrongly          passed 200 cases

$ oath prove abs-small
· unproven  bounded-wrongly     no direct proof; induction did not discharge
proven: 0/1 properties
```

200 green cases, but the property stays `tested`, not `proven` — the system will
not upgrade a claim it couldn't actually close. That gap *is* the feature: the
verdict tells you exactly how far the guarantee goes. (`examples/undertested.oath`
is the exhibit for why the rungs differ — a sample can miss what a proof catches.)

## Mutation: is the spec even watching?

A green property is worthless if it doesn't constrain the body. **Mutation
testing** asks: if we break the implementation in a type-preserving way, do the
properties notice?

```console
$ oath mutate rat-recover
spec strength: 4/4 mutants killed
```

`4/4 killed` means every mutant of the body was caught by some property — a tight
spec. A *survivor* is a mutation the spec didn't notice, i.e. a hole in the
specification, and it's reported with its body so you can fix it (or `oath waive`
it with a justification if the mutant is genuinely equivalent).

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
