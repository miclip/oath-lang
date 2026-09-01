# optimizer-consumer — a demand from proving a transformation correct

The flywheel's "depend on Oath, don't improve it" exercise, aimed this time at the
proof shape a *verified codebase kernel* most wants for its own sake: **the correctness
of a program that transforms programs.** The consumer is a verified peephole optimizer
over an arithmetic expression TREE ([`optimizer-consumer/optimizer.oath`](optimizer-consumer/optimizer.oath)):
an `Expr` datatype, an evaluator `ev`, constant-folding smart constructors `add-s`/`mul-s`,
and an optimizer `opt`, with the correctness property every such transform wants —
`ev (opt e) = ev e`. The instrument is
[`optimizer-consumer/run.sh`](optimizer-consumer/run.sh), which RUNS the kernel
(put/prove/hint) under a capped z3 and prints `PASS — 4/4`.

## What the evidence is

Every verdict is MEASURED — a `put`, a capped `oath prove`, or an `oath hint` then
prove. The claims below about VERDICTS are measured; the claim about the ROOT CAUSE
(e-matching divergence) is an inference from the phase diagnostic, and is labelled as
such. The population is these specific definitions in this kernel, not "optimizers in
general".

---

## 1. End-to-end correctness of a composed recursive transform does not prove — DEMAND

**Headline.** The per-step correctness of the optimizer PROVES; its end-to-end
correctness does not.

- `add-s.sound` = `(== (ev (add-s a b)) (+ (ev a) (ev b)))` — PROVEN, direct.
- `mul-s.sound` = `(== (ev (mul-s a b)) (* (ev a) (ev b)))` — PROVEN, direct.
- `opt.preserves` = `(== (ev (opt e)) (ev e))` — **UNPROVEN**, even with both soundness
  lemmas admitted via `oath hint`:

```
· unproven  preserves   no direct proof; induction did not discharge
```

So the pieces a human would decompose the proof into are all verified, and the whole
still does not discharge. That is the demand: **the natural correctness statement of a
recursive transformation — an evaluator composed with the transform — is exactly the
statement the prover cannot reach here.**

**It is NOT a tree-induction gap.** Two controls establish that binary-tree structural
induction works in this kernel:

- `ido.structure` = `(== (ido e) e)` for the identity rebuild — PROVEN by induction on
  the tree; and with it present, `ido.preserves` = `(== (ev (ido e)) (ev e))` PROVES
  DIRECTLY. A structure-preserving transform is rescued by one structural lemma.
- Independently, `examples/tree.oath` proves `t-size.size-flatten-length`
  (`t-size t = length (t-flatten t)`) and `t-size.size-insert` by structural induction
  over `(Node Tree Int Tree)`, whose `Node` case genuinely needs `IH(l)` AND `IH(r)`.

So two-recursive-child induction discharges. What fails is narrower.

**The distinguishing factor (INFERRED from the `OATH_PROVE_SPLIT=1` diagnostic).** On
`opt.preserves` and on the controls below, z3 returns `verdict=unknown` in the direct
and lemma-free phases and then exhausts the whole rlimit — it does not find a
counter-model and does not discharge, it DIVERGES. The goals that prove (`ido.structure`,
the `tree.oath` props) either never unfold the evaluator (`ido.structure` is pure
constructor equality) or reduce through an available list lemma (`append`-length). The
goals that fail all require the inductive step to unfold a recursive EVALUATOR (`ev`)
applied to terms BUILT by another recursive function (`opt`/`swap`) — the shape under
which the `:pattern`-triggered function axioms appear to e-match without making
progress. This is a plausible, not proven, root cause; the verdicts above are the
measured facts.

**A genuine transformation has no structural rescue.** `ido` is rescued because it
preserves structure, so `ido e = e` exists as a clean structural lemma. A transform
that CHANGES the tree has none. `swap` (flip each `Add`/`Mul`'s operands; correct
because `+` and `*` commute) is the control:

- `swap.preserves` = `(== (ev (swap e)) (ev e))` — **UNPROVEN**.

There is no `swap e = e` to lean on, and the composed correctness diverges exactly as
`opt`'s does.

**Consumer hurt: anyone verifying a compiler, optimizer, normalizer, or interpreter
pass** — the central use case of a substrate whose pitch is machine-checked identity
for code. The per-step lemmas are provable and the human decomposition is obvious, but
the kernel will not assemble it, and for a genuine transform the human cannot supply a
structural bridge either.

**DEMAND: discharge composed-recursion correctness (`fold (transform e) = fold e`)**,
or give the author a first-class way to drive the induction — an explicit
induction-scheme selection, a trigger discipline for evaluator axioms over
freshly-constructed terms, or admitting a per-constructor rewrite lemma the way a human
would. Whatever the shape, the test is whether `swap.preserves` and `opt.preserves`
become PROVABLE without erasing the recursion. This is a PROVER-INTERNAL change with a
wide blast radius (trigger emission feeds the §7.2 scripts and the cross-kernel byte
oracle), so it is a milestone, not a momentum fix.

**Relation to the RLE §3 wall (`generic-consumer-friction.md`).** Both are
induction-reach limits, but the shapes differ: §3 is a single aggregate over a nested
run structure; this is the COMPOSITION of an evaluator with a tree transform, with a
measured asymmetry (structure-preserving rescuable, value-preserving not) that §3 does
not have. Recorded separately so the two are not collapsed.

**Evidence class:** the four verdicts (add-s/mul-s sound PROVEN; opt.preserves UNPROVEN
with hints; ido.structure + ido.preserves PROVEN; swap.preserves UNPROVEN) are MEASURED
by `run.sh` (4/4). The e-matching-divergence attribution is INFERRED from the phase
diagnostic and labelled as such.

---

## 2. `match` has no wildcard / default arm — FRICTION

**Second, language-level.** The smart constructors must enumerate every non-literal
right operand because a `match` arm binds a named constructor and there is no `_`
default: `add-s`'s `(Lit m)` branch spells out `(Add x y)` and `(Mul x y)` cases that
do identical work. Every peephole rule over a multi-constructor type pays this, and it
compounds with the absence of nested SUM patterns (a nested `(Add (Lit m) (Lit n))`
would be the natural spelling but is refused — nested patterns are single-constructor
only, by design). It is not a correctness problem; it obscures the rule and grows
quadratically in constructor count.

**DEMAND (minor): a wildcard / default match arm**, or an explicit decision that
exhaustive-by-constructor matching is the intended discipline and catch-alls are out of
scope. The cost is real and recurs in every transform over a sum type.

**Evidence class:** decidable from the source (the enumerated branches in `optimizer.oath`);
HAND-verified.

---

## What is NOT friction, recorded so it is not re-derived

- **Tree induction works.** The controls (`ido.structure`, `tree.oath`) prove by
  genuine two-recursive-child structural induction. The demand in §1 is composed
  recursion specifically, not tree shape — do not re-file it as "add tree induction."
- **The optimizer is correct.** `opt` TESTS clean (200 cases) and its pieces are
  PROVEN; the unproven verdict is a prover-reach limit, not a false statement. `run.sh`
  keeps it as a `tested` exhibit, not a deleted one.
