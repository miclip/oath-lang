# #68 NARROW milestone — the carrier round-trips and the `seq.len` induction scheme

**THIS IS A PRELIMINARY FEASIBILITY PROBE. IT IS NOT AN ATTEMPT ON §11.3'S
MILESTONE, AND IT DOES NOT ENGAGE THAT FALSIFIER — neither firing it nor clearing
it.** The milestone remains not started, exactly as §11.5 says.

**Getting this classification right took three passes and it is the most
important sentence in the file**, so the reasoning is kept rather than tidied
away. §11.3 does not ask whether the round-trip is PROVABLE. It says **the
milestone must build the generator that emits it**, and it says an obligation
that does not discharge under the pinned conditions HAS FAILED. These scripts
were written by hand; no kernel emits them. So if this record were filed as a
§11.3 attempt, then by §11.3's own bound the undischarged generator obligation
would mean the falsifier had FIRED and #68 were DECLINED — on the strength of a
probe that nobody ran the milestone for. That reading is available from the text
and it is wrong, which is why the classification has to be stated rather than
assumed.

A gate is engaged by RUNNING the thing it gates. What this probe establishes is
narrower and still worth having:

> On the branch §11.3 singles out as the one needing machinery that does not
> exist, there is no MATHEMATICAL obstacle. The scheme exists, its measure is
> well-founded, and z3 4.16.0 closes both subgoals at 0.3% of the pinned budget.

The remaining risk on this branch is entirely one of IMPLEMENTATION — a real
risk, and a different one. Read this as *the DECLINE that could have been sitting
here was not found*, not as *the obligation is met* and not as *the falsifier was
tested*.

## Why this obligation was attempted first

§11.3 singles it out as the one requiring machinery that does not exist:

> **And the second round-trip needs an induction principle this repository does
> not currently have.** [...] Building that scheme is part of the milestone, and
> **if it cannot be supplied, the milestone has failed and the falsifier fires**

It is therefore the cheapest place to find a DECLINE, if a DECLINE is there. It is
also the only obligation §11.3 exempts from its no-further-tactic bound, so it is
the only one whose attempt can legitimately introduce new proving machinery.

## Conditions, pinned by §11.2 and matched exactly

| | |
|---|---|
| solver | z3 **4.16.0** (`/opt/homebrew/bin/z3`), the pinned version — not a substitution |
| rlimit | **4000000**, `(set-option :rlimit 4000000)` prepended OUTSIDE the core script |
| harness | mirrors `oath/prove.go`'s `runZ3Budget`: rlimit header before the core, `(get-info :rlimit)` / `(get-info :reason-unknown)` after |
| wall cap | z3 `-T:600`, a VALIDITY guard only — a wall hit is INVALID, never an outcome (SPEC §7.2, #29). No run hit it; the longest was 2s |

**No kernel code was written and no store was touched.** §11.3 places this
obligation BELOW the property language — `Seq` is an SMT sort, so no Oath property
can mention `seq.++` — which means `oath put` / `oath prove` cannot express it and
a scratch store is not the instrument. The instrument is the SMT-LIB script, which
is what §7 already defines an outcome over: a pure function of (script bytes,
solver version, rlimit).

## The encoding

`to-seq` and `of-seq` are written in the Go kernel's OWN emission style — a
`declare-fun` plus a patterned defining equation, plus per-constructor equations —
copied in shape from `fixtures/prove/scripts/length-0.smt2`, not invented. The
datatype spelling `List_Int` / `Nil_List_Int` / `Cons_List_Int` is likewise the
kernel's.

```smt2
(declare-datatypes ((List_Int 0)) (((Nil_List_Int ) (Cons_List_Int (Cons_List_Int_0 Int) (Cons_List_Int_1 List_Int)))))
(declare-fun fn_to_seq_Int (List_Int) (Seq Int))
(assert (forall ((p0 List_Int)) (! (= (fn_to_seq_Int p0) (ite ((_ is Nil_List_Int) p0) (as seq.empty (Seq Int)) (seq.++ (seq.unit (Cons_List_Int_0 p0)) (fn_to_seq_Int (Cons_List_Int_1 p0))))) :pattern ((fn_to_seq_Int p0)))))
(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))
(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1)) (seq.++ (seq.unit q0) (fn_to_seq_Int q1)))))
(declare-fun fn_of_seq_Int ((Seq Int)) List_Int)
(assert (forall ((s0 (Seq Int))) (! (= (fn_of_seq_Int s0) (ite (= (seq.len s0) 0) Nil_List_Int (Cons_List_Int (seq.nth s0 0) (fn_of_seq_Int (seq.extract s0 1 (- (seq.len s0) 1)))))) :pattern ((fn_of_seq_Int s0)))))
```

Both right-hand sides are §11.3's, unchanged, including the `as` annotation §9.7b
requires. Both functions are DEFINED rather than declared-and-constrained, which
is what §11.3 demands — an uninterpreted `of-seq` would make both round-trips
satisfiable-when-negated and the gate would DECLINE for every possible encoding.

## The scheme

The scheme is well-founded recursion on the natural measure `seq.len s`, emitted
as exactly two subgoals. It is the sequence-sorted analogue of §7.2's existing
recursion-induction — induction along the function's own recursion — and it adds
nothing else:

    BASE   seq.len s = 0                              ⊢ to-seq(of-seq s) = s
    STEP   seq.len s > 0,
           to-seq(of-seq t) = t  where t = seq.extract s 1 (seq.len s - 1)
                                                      ⊢ to-seq(of-seq s) = s

Both `unsat` on the negation gives `∀s. to-seq(of-seq s) = s`, provided the
measure strictly decreases. **That side condition was itself checked rather than
assumed** — see `S measure` below. It is the step that licenses treating the
`of-seq` equation as a conservative definitional extension, so it is not
bookkeeping.

## Results

Every figure is `z3 4.16.0`, rlimit budget 4000000. `used` is the rlimit actually
consumed, from `(get-info :rlimit)`. The sha256 is of the COMPLETE script —
header, core, subgoal and telemetry — so a reconstruction can be checked without
trusting this table.

| subgoal | verdict | used | sha256 of full script |
|---|---|---|---|
| **`∀s. to-seq(of-seq s) = s`** — base | **unsat** | 10662 | `7ae18f4ca1b0be8b626022cbc205a2ecfc47f22f69442cef79aca51991338fce` |
| **`∀s. to-seq(of-seq s) = s`** — step | **unsat** | 12909 | `0f7098ab32901392e609e58ac3919b1f4616f3f195aa7190ff5849fc483447ad` |
| `∀xs. of-seq(to-seq xs) = xs` — base | unsat | 2082 | `8a711e0b8a22bdd4136f957f7d8e177967a50db9e2da9acb3fba6085b2a858fc` |
| `∀xs. of-seq(to-seq xs) = xs` — step | unsat | 63737 | `c30fe9c3dd341bd706170aaa0a96fd0512ebb3a1c87bc9fd331c9ed6c777c943` |
| `S measure` — the measure decreases | unsat | 2347 | `75d9138652a14cf2ca47732c26623a81b584ec420d51bc7869c0927332b38f86` |

**The margin is large and worth recording, because it is what makes the result
robust rather than marginal.** The most expensive of the five subgoals is the
FIRST round-trip's step at 63737 of 4000000 — about 1.6% of the budget. The
obligation this run was aimed at is cheaper still: its step spent 12909, about
0.3%. A result that squeaked in at 3.9M would be one solver-version nudge from
reversing; neither of these is that.

The first round-trip is recorded here although it was outside the instruction that
prompted this run: it needs only §7.2's existing structural induction over `List`,
it cost seconds, and §11.3's falsifier names EITHER carrier round-trip, so leaving
it unmeasured would have left half a named condition unknown for no saving.

## Controls, and what each one rules out

A positive proof result is worth nothing until the instrument is shown to
discriminate. Five controls, each attacking a different way this could have been a
false positive:

| control | verdict | what it rules out |
|---|---|---|
| **C1** the goal with NO induction scheme | `unknown`, full 4000100 consumed | that the scheme is decoration. The naive goal exhausts the entire budget, so the two subgoals are doing the work |
| **C3** `of-seq` mutated to recurse on `seq.extract s 0 (n-1)` (init, not tail) | step `unknown`, 4000126 | that the step subgoal passes regardless of `of-seq`'s definition |
| **C4** `to-seq` mutated to `seq.++ (to-seq xs) (seq.unit x)` (append at end) | step `unknown`, 4000126 | that the step subgoal is insensitive to `to-seq`'s definition |
| **C3 base** the same broken `of-seq`, base subgoal | **unsat**, 21903 | nothing — and that is the point. The base case is TRUE for the mutant, since at length 0 the recursion is never taken. It is recorded because it shows the base subgoal ALONE cannot witness the claim, which is precisely why the scheme needs both |
| **S measure** | unsat, 2347 | that the induction is on a measure which might not decrease |

**The controls are hashed on the same terms as the positive results, because a
control quoted as prose is not evidence** — an rlimit outcome is a function of the
complete script bytes, and a reader who cannot rebuild those bytes is trusting
this table rather than checking it. Review caught this record asserting the
controls without supplying them.

| control | core used | subgoal used | sha256 of full script |
|---|---|---|---|
| C1 | core (unmutated) | `c1` below | `a195e8fa489e28c7442e6f7621e736296c20a329f269feac715daec3282e5e55` |
| C3 step | `mut-of-seq` | step of round-trip 2 | `f6d59e9859ddacb4068e6d63ff6ec4e21a8e47720e94ad261008b8111d205fd0` |
| C3 base | `mut-of-seq` | base of round-trip 2 | `dc254c7071fe44039cb9c6ee523bc2373c0735a0e220f867c288fa05ab1d1e5d` |
| C4 step | `mut-to-seq` | step of round-trip 2 | `188b61e2a6ca562790a438093dd44d6a6a860154df95585006c57aa56b82e54d` |

C1's subgoal — the goal with no case split and no induction hypothesis:

```smt2
(declare-const s (Seq Int))
(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))
(check-sat)
```

`mut-of-seq` is the core with its LAST line replaced — `seq.extract s0 1 …` becomes
`seq.extract s0 0 …`, so the recursion takes the init rather than the tail. The
measure still decreases, so the scheme still applies and only the CLAIM is broken,
which is what makes it a clean control:

```smt2
(assert (forall ((s0 (Seq Int))) (! (= (fn_of_seq_Int s0) (ite (= (seq.len s0) 0) Nil_List_Int (Cons_List_Int (seq.nth s0 0) (fn_of_seq_Int (seq.extract s0 0 (- (seq.len s0) 1)))))) :pattern ((fn_of_seq_Int s0)))))
```

`mut-to-seq` is the core with lines 3 and 5 replaced — both `seq.++` operands
swapped, so `to-seq` appends the head at the END. Both lines must change together
or the core is self-contradictory rather than mutated:

```smt2
(assert (forall ((p0 List_Int)) (! (= (fn_to_seq_Int p0) (ite ((_ is Nil_List_Int) p0) (as seq.empty (Seq Int)) (seq.++ (fn_to_seq_Int (Cons_List_Int_1 p0)) (seq.unit (Cons_List_Int_0 p0))))) :pattern ((fn_to_seq_Int p0)))))
```

```smt2
(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1)) (seq.++ (fn_to_seq_Int q1) (seq.unit q0)))))
```

## What this does NOT establish

- **The milestone is not passed.** The four transport equations for `append`,
  `take`, `drop`, `length` were not attempted, and neither were the 14 rotation
  laws — which §11.3 calls the sharper half, because bridges can be provable and
  still leave the family undecided on L5's length-and-index coupling.
- **Nothing is registered.** No §9.4 bridge-registry entry exists, and §11.2
  requires entries keyed by definition AND instantiation (`append@[Int]`, …).
- **No cross-kernel claim whatsoever.** §11.2 requires the equivalences to
  reproduce byte-identically in BOTH kernels. No kernel emits these scripts; I
  hand-wrote them in the Go kernel's style. Byte-identity between kernels is
  entirely unaddressed by this run and cannot be inferred from it.
- **Consistency of the axiom set is argued, not proved by the solver.** The
  intended control (`C0`, satisfiability of the core alone) returned `unknown` at
  the full budget, so it establishes nothing. What supports non-vacuity is
  (a) the mathematical argument — a structurally recursive total function over a
  datatype, and a recursion on a strictly decreasing natural measure over the
  finite `Seq` sort, are both conservative definitional extensions, with the
  decrease checked above — and (b) the observation that C1 runs against the
  unmutated core and returns `unknown` rather than `unsat`, which a core from
  which z3 could readily derive false would not do. That is corroboration, not
  proof: z3 failing to find a contradiction inside 4000000 rlimit is not evidence
  that none exists. **C3 and C4 carry no weight here at all** — they REPLACE
  axioms, so they are statements about different axiom sets, and an earlier draft
  of this bullet cited them as if they bore on the original core. They do not;
  review caught it. C1 is the only run in this record that speaks to the
  consistency of the core actually used for the positive results.
- **The mutants returned `unknown`, not `sat`.** They FAILED TO DISCHARGE rather
  than being refuted. For a gate whose pass condition is `unsat` that is the
  discrimination that matters, but *not proved* is not *disproved* and the
  distinction is not cosmetic.

## Reproducing

**The byte layout is exact, and one detail in it is easy to get wrong** — a
review caught this recipe when it was stated loosely, and a recipe that does not
reproduce the hash makes the hash decorative. In `runZ3Budget` terms the script
is `header + core + subgoal + "\n(get-info :rlimit)\n(get-info :reason-unknown)\n"`,
and because the subgoal already ends in a newline that trailing concatenation
leaves **a blank line between `(check-sat)` and `(get-info :rlimit)`**. That
blank line is part of the hashed bytes. Written out, every script is exactly:

    (set-option :rlimit 4000000)\n
    <the 7 lines of the core block above, each ending \n>
    <the subgoal block, each line ending \n>
    \n
    (get-info :rlimit)\n
    (get-info :reason-unknown)\n

with no other whitespace anywhere. Check the sha256 against the table before
running `z3 -T:600`; if it does not match, the reconstruction is wrong and the
verdict below it means nothing. The subgoal blocks are:

**base of `∀s. to-seq(of-seq s) = s`:**

```smt2
(declare-const s (Seq Int))
(assert (= (seq.len s) 0))
(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))
(check-sat)
```

**step of `∀s. to-seq(of-seq s) = s`:**

```smt2
(declare-const s (Seq Int))
(assert (> (seq.len s) 0))
(define-fun ih_tail () (Seq Int) (seq.extract s 1 (- (seq.len s) 1)))
(assert (= (fn_to_seq_Int (fn_of_seq_Int ih_tail)) ih_tail))
(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))
(check-sat)
```

**base of `∀xs. of-seq(to-seq xs) = xs`:**

```smt2
(assert (not (= (fn_of_seq_Int (fn_to_seq_Int Nil_List_Int)) Nil_List_Int)))
(check-sat)
```

**step of `∀xs. of-seq(to-seq xs) = xs`:**

```smt2
(declare-const x Int)
(declare-const xs List_Int)
(assert (= (fn_of_seq_Int (fn_to_seq_Int xs)) xs))
(assert (not (= (fn_of_seq_Int (fn_to_seq_Int (Cons_List_Int x xs))) (Cons_List_Int x xs))))
(check-sat)
```

**the measure strictly decreases:**

```smt2
(declare-const s (Seq Int))
(assert (> (seq.len s) 0))
(assert (not (= (seq.len (seq.extract s 1 (- (seq.len s) 1))) (- (seq.len s) 1))))
(check-sat)
```
