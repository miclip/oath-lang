VERDICT: YES — deterministic instantiation of the FUNCTION DEFINING EQUATIONS is
sufficient to discharge both the Add and Mul structural-induction cases of
`opt.preserves`, without any soundness-lemma instance.

Evidence, as files rather than prose:

    z3-output/emitted-quantified-Mul-UNKNOWN.txt   the divergence being explained
                                                   (unknown, 15,000,262 rlimit, "canceled")
    z3-output/defs-instantiated-Mul-UNSAT.txt      the same goal, definition-instantiated
                                                   (unsat, 5,183 rlimit)
    z3-output/control-badopt-Mul-SAT.txt           a WRONG optimizer through the same
                                                   machinery (sat -- no fabrication)

Independently reproduced: a second party reran all six scripts and obtained the same
six verdicts.

---

# Quantifier-free instantiation of the `opt.preserves` induction subgoals

Beside each prover-emitted script are quantifier-free counterparts: same datatype
declaration, same uninterpreted functions, same `f0`/`f1` induction hypotheses,
same negated goal — with all quantified axioms REMOVED and replaced by ground
instances.

**The primary result is the definition-only variant**, and it is only worth reading
together with the wrong-optimizer control below: an instantiation procedure that
returns `unsat` for a FALSE optimizer would be manufacturing proofs, and every
`unsat` here would be void.

| file | SHA-256 |
|---|---|
| `opt-preserves-defs-instantiated-binder0-ctor1-Add.smt2` | `1ff62f255c133a12ebc8ab7bbb47b5234d230bb4ac763635471f1cdf3ee505b1` |
| `opt-preserves-defs-instantiated-binder0-ctor2-Mul.smt2` | `a3f1766313c830a064ef8ea3f5f698a5af50caef5f051a1c1eed76827c8df490` |
| `bado-preserves-defs-instantiated-binder0-ctor1-Add.smt2` (control) | `6166f92f18956981551196df34c767bc434b8fa25a74569282f0235017d4a49c` |
| `bado-preserves-defs-instantiated-binder0-ctor2-Mul.smt2` (control) | `76e812152b89a6b9e06c56b8c5828ebfb583623e27eecf9b3f63960ab6ffaaeb` |
| `bado-preserves-induction-binder0-ctor1-Add.smt2` (control, emitted) | `899e77e24f7abd2a6e4e6302a2e54b238c7cb4f5a71b0a7dc86a0a88ece4f63f` |
| `bado-preserves-induction-binder0-ctor2-Mul.smt2` (control, emitted) | `a77999aa63f1a7ebe5e61b8ba5fb3c4718bc4ee92fd77da96c179eeb16c1a5c9` |
| `opt-preserves-lemma-instantiated-binder0-ctor1-Add.smt2` (secondary) | `d0269165e4271ce60fea63a97736520dff76102cea2350c9659f8f60e10b1cf2` |
| `opt-preserves-lemma-instantiated-binder0-ctor2-Mul.smt2` (secondary) | `19dd49a39edbe8e10c2cabe0958c4036d601240058edd4053ce617a19780c03e` |
| `instantiate-defs.py` (generator, primary) | `765953df00a2c6986435eb38fd65991169a4c1290e1124baef6e242650d129fc` |
| `instantiate-lemma.py` (generator, secondary) | `86031d3731e2d53376b0f7a3a1fc2b463ef8f38d497417e8b4edc6be6f0d8082` |
| `bad-optimizer.oath` (control source) | `e22ea1f60b7906850c8bf911a4e048f924347682054cd6362c34400b0adedb40` |

Raw z3 outputs for the three evidence classes are FILES, in `z3-output/`, each
carrying the exact command that produced it:

    emitted-quantified-Mul-UNKNOWN.txt    the emitted quantified Mul subgoal   -> unknown
    emitted-quantified-Add-unsat.txt      the emitted quantified Add subgoal   -> unsat
    defs-instantiated-Add-UNSAT.txt       definition-instantiated Add          -> unsat
    defs-instantiated-Mul-UNSAT.txt       definition-instantiated Mul          -> unsat
    control-badopt-Add-SAT.txt            WRONG optimizer, Add                 -> sat
    control-badopt-Mul-SAT.txt            WRONG optimizer, Mul                 -> sat

## Results

`:rlimit 15000000`, z3 4.16.0.

| script | verdict | consumed rlimit | reason |
|---|---|---|---|
| emitted `binder 0 ctor 1` (Add), quantified | **unsat** | 7,071 | `""` |
| emitted `binder 0 ctor 2` (Mul), quantified | **unknown** | 15,000,262 | `"canceled"` |
| **defs-instantiated Add** | **unsat** | 3,234 | `""` |
| **defs-instantiated Mul** | **unsat** | 5,183 | `""` |
| **control: wrong optimizer, Add** | **sat** | 2,023 | `""` |
| **control: wrong optimizer, Mul** | **sat** | 2,217 | `""` |
| *(secondary)* lemma-instantiated Add / Mul | unsat | 1,252 / 1,248 | `""` |

**Both constructor subgoals follow from the FUNCTION DEFINITIONS alone**, given the
right ground instances — no appeal to `add-s.sound` or `mul-s.sound`. The Mul case
goes from 15,000,262 rlimit (exhausted, `canceled`) to 5,183 (`unsat`): a ~2,900x
reduction against the budget. The obstacle is quantifier instantiation, not the
arithmetic, the case analysis, or a missing lemma.

## NON-FABRICATION CONTROL

`bad-optimizer.oath` defines `bado`, which MIRRORS `opt`'s shape exactly — recurse
on both children, combine with a smart constructor — but its smart constructor
`zero-s` discards both operands and returns `(Lit 0)`. So `bado` preserves literals
and maps every `Add` and `Mul` parent to `(Lit 0)`, and `(ev (bado e)) = (ev e)` is
FALSE. The kernel agrees: `oath put` reports

    ✗ bado  #cc7e86608aca  FALSIFIED: preserves
        counterexample: (Mul (Mul (Lit 6) (Lit -12)) (Lit -7))

Its subgoals were emitted by the same `scriptAttempts` seam, instantiated by the
SAME generator invoked the same way, with the same negated Add/Mul goals and the
same IH scheme. **Both come back `sat`.** The procedure does not turn a false
optimizer into a proof, so the `unsat` results above are measurements rather than
artifacts of the instantiation.

`bado` had to be genuinely RECURSIVE for this control to be worth anything. A first
attempt returned `(Lit 0)` directly from the `Add`/`Mul` branches; being
non-recursive, the kernel INLINED it into the goal and emitted no `fn_bado`
defining equation at all — leaving nothing to instantiate and no structural parity
with `opt`. Recursing through `zero-s` restores parity: the emitted control script
has an uninterpreted `fn_bado` with its own defining equation, exactly as `opt` has
`fn_opt`.

## THE TWO CONSTRUCTOR SUBGOALS DO NOT BEHAVE ALIKE

The emitted Add subgoal **already discharges in its quantified form** (unsat, 7,071
rlimit). Only Mul exhausts the budget. Because the induction strategy needs EVERY
constructor subgoal to return `unsat`, one failing case leaves `opt.preserves`
unproven — so "the composed induction does not discharge" is true of the property
while being false of half its subgoals.

## Mapping: every ground equality to the quantified equation it instantiates

Each emitted script declares two uninterpreted functions and asserts a DEFINING
EQUATION for each, of the form `∀ p0. f p0 = <body>`:

    fn_ev  defining equation      -- the evaluator
    fn_opt defining equation      -- the optimizer (add-s/mul-s INLINED into it)
                                     `fn_bado` in the control, with `zero-s` inlined

The `opt` scripts additionally carry two 2-variable soundness LEMMAS
(`add-s.sound`, `mul-s.sound`). **The generator discards them: a premise is
admitted only if it is a ground instance of a defining equation.** The control
scripts have no lemmas at all, which is part of why the two are comparable.

Write `P` for the parent term (`(Add_Expr f0 f1)` / `(Mul_Expr f0 f1)`) and `T` for
the optimizer's result at `P` — obtained by substituting `p0 := P` into the
optimizer's own defining equation and folding the constructor case split.

| line | instantiates | substitution | role |
|---|---|---|---|
| 7 | opt defining equation | `p0 := P` | unfolds `fn_opt P` to `T` |
| 8 | ev defining equation | `p0 := P` | unfolds `fn_ev P` to `fn_ev f0 + fn_ev f1` (resp. `*`) |
| 9 | ev defining equation | `p0 := T` | evaluates the optimizer's result |
| 10 | ev defining equation | `p0 := <fold branch of T>` | `(Lit_Expr (+ (Lit_Expr_0 (fn_opt f0)) (Lit_Expr_0 (fn_opt f1))))` |
| 11 | ev defining equation | `p0 := <rebuild branch of T>` | `(Add_Expr (fn_opt f0) (fn_opt f1))` |
| 12 | ev defining equation | `p0 := (fn_opt f0)` | see below |
| 13 | ev defining equation | `p0 := (fn_opt f1)` | see below |
| 14, 15 | *(carried verbatim)* | — | induction hypotheses on `f0`, `f1` |

In the CONTROL, `T` is just `(Lit_Expr 0)` — a single constructor-valued leaf that
IS `T`, so lines 10 and 11 collapse into line 9 and the control has five premises
(7, 8, 9, plus `fn_ev` at `(fn_bado f0)` and `(fn_bado f1)`) rather than seven.

The generator derives `T` from the optimizer's defining equation using only
datatype rewrites (selector-on-constructor, tester-on-constructor, `ite` folding).
**That simplifier is self-checked against z3**, not trusted: run with mode
`checksimp` it emits `(assert (not (= T_raw T_simplified)))` and reports the
verdict. All four scripts return `unsat`, so each simplified term provably equals
the raw substituted one.

### The instance the shape does not suggest

Lines 12 and 13 are `fn_ev` at the OPTIMIZED SUBTERMS `(fn_opt f0)`, `(fn_opt f1)` —
not at the parent, and not at a branch of `T`. They are not optional: WITHOUT them
(generator mode `base`) both scripts are **sat**, with this countermodel (Add,
abridged):

    f0 = (Lit_Expr 7)          fn_opt f0 = (Lit_Expr -28100)
    f1 = (Lit_Expr 8)          fn_opt f1 = (Lit_Expr 0)
    fn_ev (Lit_Expr 0) = 28101

The model sets `fn_ev (Lit_Expr 0) = 28101`, violating `fn_ev (Lit n) = n` — and
nothing forbids it, because no ground instance pins `fn_ev` at that term. The fold
branch reads the PAYLOAD of the optimized subterms through `Lit_Expr_0`, so `fn_ev`
must be pinned AT those subterms for the payload to connect back to the induction
hypotheses. Instantiating only at the parent and at `T`'s branches — the shape the
goal's syntax suggests — leaves that link absent.

## Controls

**Consistency.** Removing the negated goal gives **sat** for all four scripts, so no
`unsat` rests on a contradiction among the instances.

**Necessity, per premise** (definition-instantiated `opt`; each assert dropped in
turn, identical for Add and Mul):

| dropped | verdict |
|---|---|
| 7 opt defn @ parent | sat |
| 8 ev defn @ parent | sat |
| 9 ev defn @ `T` | unsat |
| 10 ev defn @ fold branch | unsat |
| 11 ev defn @ rebuild branch | unsat |
| 12 ev defn @ `(fn_opt f0)` | sat |
| 13 ev defn @ `(fn_opt f1)` | sat |
| 14 IH on `f0` | sat |
| 15 IH on `f1` | sat |

**Lines 9, 10 and 11 are individually redundant but not jointly so** — dropping
`{10,11}` stays `unsat`, while `{9,10}`, `{9,11}` and `{9,10,11}` all go `sat`. So
the requirement is: EITHER `fn_ev` at the whole term `T`, OR `fn_ev` at BOTH of
`T`'s constructor-valued branches; either route lets congruence carry `fn_ev`
through the case split. Dropping 10 and 11 leaves a **minimal** set of seven
premises, verified minimal by re-ablating each. The shipped scripts keep 10 and 11
so every branch is present explicitly.

## Secondary comparison: the lemma-instantiated variant

`opt-preserves-lemma-instantiated-*` replaces lines 9-13 with a SINGLE ground
instance of the soundness lemma at `q0 := (fn_opt f0)`, `q1 := (fn_opt f1)`, giving
five premises; `unsat` at ~1,250 rlimit, all five necessary. **It tests a weaker
claim** — it assumes the smart-constructor soundness result rather than deriving
the case from the definitions — and is retained only for contrast.

## Commands

    # regenerate (primary; 'Add'/'Mul' selects the constructor)
    python3 instantiate-defs.py opt-preserves-induction-binder0-ctor1-Add.smt2  Add ext \
        opt-preserves-defs-instantiated-binder0-ctor1-Add.smt2
    python3 instantiate-defs.py bado-preserves-induction-binder0-ctor1-Add.smt2 Add ext \
        bado-preserves-defs-instantiated-binder0-ctor1-Add.smt2
    # 'base' instead of 'ext' omits lines 12-13 and reproduces the sat result above
    # 'checksimp' emits and runs the simplifier self-check instead

    # run any script, with the header the kernel's runZ3Budget prepends
    { echo '(set-option :rlimit 15000000)'; cat <script>.smt2; } | z3 -smt2 /dev/stdin

    # consistency control
    { echo '(set-option :rlimit 15000000)'; grep -v 'assert (not' <script>.smt2; } | z3 -smt2 /dev/stdin

    # necessity control: drop the assert on line N
    { echo '(set-option :rlimit 15000000)'; sed 'Nd' <script>.smt2; } | z3 -smt2 /dev/stdin

    # regenerate the control's emitted subgoals from source
    OATH_STORE=$(mktemp -d) oath put bad-optimizer.oath --new     # reports FALSIFIED, exit 2
    # then export attempts 03/04 via the seam in EXTRACTION.md

Instances are produced by SUBSTITUTION into the parsed emitted axioms, never
retyped. Carried over verbatim from each emitted script: the datatype declaration,
both `declare-fun`s, `b0`/`f0`/`f1`, both induction hypotheses, and the negated
goal. `b0` remains declared and unused, exactly as emitted.

## Scope

These scripts are a DIAGNOSTIC, not a proof of `opt.preserves`. They discharge two
constructor subgoals under hand-chosen ground instances; they do not establish the
induction, and nothing here is wired into the kernel or any gate. The rlimit figures
are the reproducible measurement — deterministic for a fixed script and z3 version.

## What this does and does not establish

Deterministic instantiation of the two function defining equations, at ground terms
derived mechanically from the goal, suffices for both structural-induction cases of
`opt.preserves` — Add and Mul, `unsat` at 3,234 and 5,183 rlimit against a
15,000,000 budget, with no `add-s.sound` or `mul-s.sound` instance present. The
honest shape of the finding is narrower than "the induction now goes through",
in three respects. Only the **Mul** case is the actual divergence: the emitted Add
subgoal already closes in its quantified form at 7,071 rlimit, so half of what
looked like a composed-recursion wall was never stuck, and the property reads as
unproven because the induction strategy needs every constructor subgoal. The
**wrong-optimizer control stays open**: `bado` — same shape, same machinery, same
IH scheme, a smart constructor that discards its operands — comes back `sat` in
both cases, so the procedure is not turning false optimizers into proofs; but one
counterexample optimizer is not a class of them, and nothing here bounds what a
differently-wrong definition would do. And the **necessary instances were not
predictable from the goal's syntax**: `fn_ev` had to be pinned at the optimized
subterms `(fn_opt f0)`, `(fn_opt f1)`, which are neither the parent nor a branch of
the optimizer's result, and omitting them leaves both scripts `sat` with an
explicit countermodel.

So this is **evidence for a prover lever, not a kernel result.** It says a
definition-instantiation strategy is worth designing for goals of this shape. It
does NOT claim a kernel implementation — nothing here is wired into `oath prove`,
no strategy was added, and the ground terms were chosen by a generator written for
these two scripts. It does NOT claim universality — two constructor cases of one
property in one corpus, at one budget, under one z3 version. The measurement that
would justify building the lever is the one this file does not contain: whether the
same mechanical choice of instances discharges goals across the corpus, and whether
a rule for choosing them can be stated without reference to how these particular
bodies are written.
