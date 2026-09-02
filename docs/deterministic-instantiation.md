# Deterministic instantiation — a prover strategy (DESIGN ONLY)

Status: DESIGN. Go/no-go measured (GO) in
`docs/experiments/optimizer-consumer/generated-subgoals/{INSTANTIATION.md,swap/SWAP-GENERALIZATION.md}`.
No kernel code implements this yet. This document is the design the implementation
session works from; it is NOT normative and changes no verdicts until Phase 2 writes
the SPEC text.

## The problem it solves

z3's search is NON-MONOTONE in its axiom set: adding a true lemma can divert
e-matching into budget exhaustion, so a goal provable from a smaller axiom set
returns `unknown` from a larger one. SPEC §7.2 makes this concrete with the `q-drop`
corpus witness and handles it by iterating F to a fixpoint — which is correct but
pays a price in BOTH directions:

- **proof-dropped** (the ledger's #2c friction): a proof recorded in one pass is not
  re-derivable from the final state, so it is demoted.
- **provable-but-stuck** (measured on `swap.preserves`): a true theorem is never
  recorded, because the full lemma set diverts the search that a subset would have
  completed.

Both are the SAME root cause. The prover's induction strategy asserts the function
defining equations as QUANTIFIED axioms and relies on z3 to instantiate them; on
composed-recursion goals (a recursive evaluator over terms built by another
recursive function) that instantiation diverges — measured as the Mul case of both
`opt.preserves` and `swap.preserves` exhausting a 15,000,000 rlimit.

## The technique (measured)

Replace the quantified defining-equation axioms with GROUND INSTANCES at the terms
the goal actually mentions. Measured outcome, two independent transforms, z3 4.16.0,
rlimit 15,000,000:

| goal | quantified | defs-instantiated |
|---|---|---|
| `opt.preserves` Mul (folding) | unknown, 15,000,262 | unsat, 5,183 |
| `swap.preserves` Mul (commutativity) | unknown, 15,000,112 | unsat, 1,847 |

with a non-fabrication control in each: a deliberately WRONG transform, through the
identical machinery, returns `sat` on its false case. The obstacle is quantifier
instantiation — not the arithmetic, the case split, or a missing lemma.

## Design questions (the three the experiment left open)

### 1. Instance selection — must be STRUCTURAL, not per-goal

The experiment's `ext` schema, generalized. For an induction subgoal on binder `b`
at constructor `C` with fields `f0..fn`, transforming function `g`, evaluator `ev`:

- **parent equation**: `g((C f0..fn))` → its RHS (the one match arm `C` selects).
- **`ev` at the parent**: `ev((C f0..fn))` → its RHS.
- **`ev` at the transformed result**: `ev(RHS_of_g)`.
- **`ev` at each recursed subterm**: `ev(g(fi))` for every RECURSIVE field `fi` —
  these are exactly the terms the induction hypotheses `ev(g(fi)) = ev(fi)` speak
  about, so they let z3 discharge the goal by rewriting rather than search.

Every instance is a substitution into a parsed defining equation (the match arm),
keyed off constructor structure — derivable from (the function bodies' match arms,
which fields of `C` are recursive), with NO per-example tuning. The count is
O(functions × recursive-fields): finite and small. This is the crux to get right;
`instantiate-defs.py` is the reference the kernel implementation must reproduce
structurally rather than by its current line/shape assumptions.

### 2. Soundness — gate on TOTALITY (reuse the existing verdict)

A ground instance of `f(pattern) = body` is a theorem of the definition IFF `f` is
defined at that point. Instantiating a PARTIAL function's equation at an argument
where it is undefined asserts a false equation and would let z3 "prove" anything.

GATE: instantiate the defining equations only of functions whose totality is already
PROVEN (the kernel computes this — `terminationOf` and the confinement/totality
verdicts). A function total on the relevant constructor case makes its equation
there a theorem. This reuses an existing verdict; it adds no new analysis, and it is
the ONE thing that must not be skipped. The empirical control (wrong transform →
`sat`) is evidence, not proof, of soundness; the totality gate is the argument.

### 3. Strategy-sequence placement — instantiated FIRST, quantified FALLBACK

Current order: lemma-free direct → direct → induction (per ctor) → direct-fallback.
Add the ground-instantiated induction subgoal as the primary induction form, keeping
the quantified form as a fallback. Instantiated is both faster and more complete on
the measured cases, but its generality beyond Add/Mul trees is unproven — so
fallback guarantees NO goal that proves today can regress. Reassess dropping the
quantified fallback only after the corpus run in Phase 1.

The payoff is determinism: because an instantiated proof does not depend on the
ambient lemma set, F becomes MONOTONE on instantiation-discharged goals. The §7.2
fixpoint then keeps the proof (no demotion) and warm/cold still converge — the
friction's demand satisfied WITHOUT the unsound verdict-retention that §7.2 forbids.

## Conformance, SPEC, second kernel

Adding the strategy changes `prove/outcomes.json`: `swap.preserves`,
`opt.preserves`, `q-drop`'s `drop-back-only`, and any other stuck composed-recursion
goals move `unknown` → `proven`. This is a verdict change, not an identity change —
no hashes move. But verdicts are the conformance surface, so:

- SPEC §7 (strategy list) and §7.2 gain NORMATIVE text describing the instantiation
  schema and the totality gate, precise enough for a blind kernel to reproduce the
  same instances and the same outcomes. This is new normative text → blind-test it
  per the blind-implementation discipline before shipping.
- `oathrs` must implement the strategy from that text and re-derive the fixtures;
  the oracle byte-checks the direct-attempt scripts. Cross-kernel green is the gate.
- Revisit `q-drop`'s role: it is §7.2's witness that F is non-monotone. If
  instantiation makes `drop-back-only` deterministic, the witness may need a
  different example, or a note that instantiation removes THIS instance while the
  fixpoint requirement stands for any residual non-monotone goal.

## Phased plan

- **Phase 0 — go/no-go. DONE (GO).** Two transforms, controls, in the experiment.
- **Phase 1 — Go kernel.** Implement structural instance-selection in the induction
  strategy, gated on totality, instantiated-first/quantified-fallback. Falsifier:
  run the WHOLE corpus and require outcomes only GAIN proofs — zero regressions and
  zero new proofs of a false property (the control discipline as a corpus test). If
  any currently-proven goal regresses, or selection needs per-goal tuning, the design
  is wrong and stops here.
- **Phase 2 — SPEC.** Write the normative strategy text; regenerate fixtures;
  blind-test the new text.
- **Phase 3 — second kernel.** oathrs blind re-derivation; oracle byte-check;
  cross-kernel green.

Phase 1 is local and free (Go + z3). It is the next implementation step and the one
that can still return "no change required". Phases 2–3 are the deliberate
normative/cross-kernel work — not gated on money, gated on doing it carefully.

## Why this is the fix and retention is not

Retention (make `prove` keep a verdict it can't reproduce) violates §7.2's
requirement that every recorded proof be re-derivable from the recorded state, and
makes verdicts history-dependent. Deterministic instantiation removes the CAUSE — the
search-dependence — so the proof reproduces from any superset and is never diverted.
The verdict stays a function of the artifact and its recorded state, which is the
property §7.2 exists to protect.
