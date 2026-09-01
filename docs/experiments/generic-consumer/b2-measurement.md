# B2 (lawful dictionaries): what it can and cannot deliver — MEASURED

The generic-consumer friction list, demand 1, asked for B2 — a way to constrain a
dictionary to LAWFUL — with the test: *"the round-trip `rle-decode (rle eqd xs) = xs`
becomes STATABLE and PROVABLE for lawful `Eq`."* Before designing B2 this measures
whether that test is achievable, and isolates what actually blocks it. The headline:
**the demand conflated two independent walls, B2 addresses only one, and the RLE
round-trip clears neither with the laws B2 describes.**

The instrument is [`b2-measurement.sh`](b2-measurement.sh) with fixture
[`b2.oath`](b2.oath); it RUNS the kernel (put/prove/eval) under a capped z3 and prints
`PASS — 4/4`. Every verdict quoted here is one of its checks, reproducible from a fresh
store — nothing runs against the committed corpus.

## The two walls are independent

A generic property over a dictionary can fail to be PROVEN for two unrelated reasons:

1. **Lawlessness** — the property is FALSE over the space B1 quantifies (every
   dictionary, lawful or not), because a lawless `eq` is a valid counterexample. A
   law on the dictionary excludes the counterexamples. This is B2's target.
2. **Induction reach** — the property is TRUE but z3's structural induction cannot
   discharge it. A prover-strength limit (friction §3), unrelated to the dictionary.

The demand's test needs BOTH cleared. The RLE round-trip clears neither.

## Wall 1: the equivalence laws are not enough for RLE — it needs substitutivity/fidelity

`docs/generics.md` frames B2 as *verified type-class laws* but pins only the ORDERING
laws (totality, transitivity, antisymmetry); it does NOT specify what "lawful `Eq`"
means. So which laws an `Eq` dictionary must carry is an open design choice, and this
measurement bears on it. Check 2 supplies the always-equal dictionary
`{eq (fn [_ _] true)}`, a LAWFUL equivalence — reflexive, symmetric, transitive — and
measures:

```
rle-decode (rle {eq (fn _ _ true)} [1,3,5]) = [1,1,1]   ≠  [1,3,5]
```

So **the equivalence laws alone are insufficient for RLE**: an eq that is a valid
equivalence still groups distinct elements, and decode reproduces one per run. What the
round-trip needs is SUBSTITUTIVITY / FIDELITY — `eq a b ⇒ a = b` (equivalently
`eq a b = (a == b)`), that `eq` respects kernel equality — which is strictly stronger
than an equivalence relation. This does NOT contradict B2; it constrains it: **if** B2's
`Eq` laws are only the equivalence laws, they do not make RLE's round-trip true, and a
lawful-`Eq` bundle that is to serve RLE must include substitutivity. Reflexivity
(below) needs only reflexivity — so different properties need different-strength laws,
which is itself the argument for a general per-property hypothesis over one fixed
"lawful Eq" bundle.

## Wall 2: even with fidelity, RLE's round-trip is blocked by induction

Check 1 rewrites the round-trip with the concretely lawful, maximally-faithful `eq` —
the kernel's `==` on `Int`, no dictionary at all (`rle-mono` in `b2.oath`). If
lawlessness (or fidelity) were the only blocker, this would prove. With every
dependency (`length`, `append`, `replicate`, `rle-decode`) proven first:

```
· unproven  round-trips-mono   no direct proof; induction did not discharge
```

**A concretely lawful, fully-faithful eq does NOT make the round-trip provable.** The
blocker is the same nested-recursion induction wall friction §3 measured for the
weaker length-preservation property. B2 cannot move this verdict; a stronger induction
capability would, and that is a separate project from lawful dictionaries.

## The induction-reachable law: FALSIFIED generic, PROVEN monomorphic — and what that does and does not show

The motivating case for a lawfulness hypothesis is a property whose proof is within
induction's reach. Reflexivity of list-equality is the clean one: it needs only that
`eq` is reflexive (well inside the equivalence laws), and its proof is ordinary list
induction. What is MEASURED here is the generic FALSIFIED / monomorphic PROVEN pair;
the generic-with-hypothesis proof is a design hypothesis, stated as such below.

- **Check 3 — generic, over all dictionaries: FALSIFIED**, by a non-reflexive `eq`:
  `eqby-refl` with prop `(== (list-eq-by [Int] eqd xs xs) true)`.
- **Check 4 — monomorphic, with the lawful `==`: PROVEN.** `list-eqm` with prop
  `(== (list-eqm xs xs) true)` → `∎ PROVEN reflexive · induction on binder 0`.

**What these two facts establish, stated exactly:** the generic reflexivity is false
ONLY because B1 admits a non-reflexive `eq`, and the same shape proves once `eq` is
lawful AND the dictionary is erased. What they do NOT establish is that a future
generic property carrying a lawfulness HYPOTHESIS would prove. That version keeps the
array-encoded dictionary application and adds a nested quantified antecedent, and the
interaction of that antecedent with structural induction is exactly the unbuilt
mechanism. So:

> **DESIGN HYPOTHESIS (not measured):** a lawful-eq hypothesis would move `eqby-refl`
> from FALSIFIED to PROVEN. It is plausible because the monomorphic proof exists and
> the law it needs is weak, but it can only be confirmed by building the mechanism and
> measuring — the quantified-hypothesis × induction interaction is precisely what is
> not yet implemented.

## What B2's mechanism would be (design, not built)

The honest shape is a **lawfulness hypothesis carried in the property's identity**, so
the hash reflects exactly what was proven (`lawful(eqd) ⇒ P`), rather than an
out-of-band assumption that hands `P` a "proven" verdict secretly depending on a law.
Concretely a nested universal quantifier usable in a prop body:

```
(prop round-trips-lawful [(eqd {eq (-> Int Int Bool)}) (xs (List Int))]
  (if (forall [(a Int) (b Int)] (== ((. eqd eq) a b) (== a b)))       ; the fidelity law
      (== (rle-decode [Int] (rle [Int] eqd xs)) xs)
      true))
```

- Implication needs no new primitive — `(if P Q true)` is `P ⇒ Q`, already used across
  `examples/generic.oath`.
- The lowering path already exists: the lemma mechanism emits `(assert (forall ...))`
  axioms scoped to a proof (`oath/prove.go`), and dictionary fields are already
  array-encoded and quantifiable. The genuinely missing piece is a **nested `forall`
  term** — today prop binders are the only universals, so a law with its own bound
  variables (`a`, `b`, distinct from the prop binders) cannot be stated.

The cost is not small and not local:

- a new O1 opcode for `forall` — a **SPEC §1 identity/encoding change**, the
  reality-forking category;
- an eval restriction: a `forall` over an infinite domain is not executable, so it is
  **proof-only** and must be refused in runnable code;
- a testing-model change: a forall-hypothesis prop is not meaningfully testable (a
  lawless sample makes the antecedent false and the prop vacuously pass), so it skips
  the "tested" rung — a real change to the guarantee ladder;
- prover work to emit the nested quantifier;
- **the second (Rust) kernel** must implement it too, if any corpus definition uses it.

## Conclusion

- Demand 1's literal test ("round-trips STATABLE and PROVABLE for lawful Eq") is not
  reached: RLE's round-trip needs SUBSTITUTIVITY/fidelity even to be TRUE — the
  equivalence laws alone are insufficient (wall 1), so a lawful-`Eq` bundle serving RLE
  must include it — and it stays UNPROVEN with a fully-faithful eq (wall 2, induction).
- B2's mechanism has a measured motivating case (reflexivity: FALSIFIED generic,
  PROVEN monomorphic) and a clearly-labelled design hypothesis for the generic win.
- Building it is a SPEC §1 identity-forking, two-kernel change deferred as "the novel
  piece." Whether to build it is a milestone decision, not momentum: its value is now
  measured, and the property that motivated the demand would remain unproven until the
  separate induction wall is also addressed.

**Evidence class:** all four verdicts (round-trips-mono UNPROVEN with deps proven;
always-equal round-trip eval `[1,1,1]`; eqby-refl FALSIFIED with counterexample;
list-eqm reflexive PROVEN by induction) are MEASURED by `b2-measurement.sh` under a
capped z3. The generic-with-hypothesis proof outcome is a DESIGN HYPOTHESIS, labelled
as such above.
