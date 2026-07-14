# Split-agent experiments: the difficulty ladder

**Date**: 2026-07-14 · **Modules**: `tree.oath`, `interval.oath`, `queue.oath`,
`rle.oath`, `ediv.oath` · **Journal**: seq 180 onward

Five modules, written end-to-end by agent pairs with an information firewall:
a **spec agent** writes properties from a natural-language brief; an
**implementer agent** writes bodies from the props and spec-only `oath
context` slices; the kernel referees (gate → 200 deterministic cases/prop →
Z3 → mutation exam). Neither agent ever sees the other's work or any
dependency body. In the later runs the implementer wrote the module file and
ran `put` itself; the journal records those submissions under the
implementer principal. What the committed artifacts substantiate is exactly
that — the principal on the journal entries — not that the agent avoided
repo reads or acted without orchestration; the firewall is instruction-level
policy (see limitations below), and the orchestrator audited afterward
(props verbatim, journal, verdicts).

## The ladder

| Module | Difficulty design | Puts to green | Proven | Spec strength |
|---|---|---|---|---|
| `tree` (BST) | textbook, subtle member/insert interplay | **1** | 12/16 | 19/20 + 1 judged-equivalent |
| `interval` | non-textbook, fully in provable fragment | **1** | **18/18** | 33/38; all 5 survivors are `<`→`<=` inside min/max, equivalence machine-checked (`minmax-equiv.smt2`, Z3 unsat) |
| `queue` (two-list FIFO) | classic empty-front trap | **1** (implementer-principal) | 16/20 | 9/9 |
| `rle` (codec) | roundtrip oath, Int-recursion, canonicity | **1** (implementer-principal) | 3/15 (rest honestly tested: non-total callees) | **26/26** |
| `ediv` (Euclidean div/mod) | engineered to stumble: negative operands, / and % outside SMT fragment | **1** (implementer-principal) | 0/11 (all tested: % in bodies) | 27/30; all 3 survivors judged unreachable-input equivalents by hand analysis (bodies contain `%`, outside the SMT fragment — no machine artifact; the waived-mutants gap below) |

The ladder modules themselves total 17 functions / 80 properties. Including
the pre-existing library and examples, the store stands at 45 functions /
154 properties, 116 proven (29 definitions fully proven). Every module's
guarantee profile matches what the spec agent predicted from its private
validation before the implementer ever saw the contract.

## The headline finding — and it isn't the one we designed for

We built the ladder to put the falsification→repair loop on the record.
**It never fired at the implementation level: five modules, five first-try
greens**, including the module engineered for failure (Euclidean division's
negative-operand semantics). The counterexample loop turned out to be the
wrong place to look.

Where the loops actually fired — constantly — was **specification**:

1. **The mutation exam caught a real spec hole after everything was green
   and proven** (BST: duplicate placement unobservable through the flatten
   lens; the spec agent judged it "a spec hole, not a decision," added one
   anchor, Z3 proved it, the mutant died).
2. **Spec agents adopted the kernel as a spec-debugger, unprompted.** Given
   only *permission* to use a throwaway store, every spec agent from module
   2 onward built a private reference implementation, ran its own props
   through prove and mutate, wrote deliberate adversarial implementations
   (an RLE encoder that passes roundtrip with one run per element; a
   truncating-passthrough e-mod), and kept strengthening until its spec
   killed them. The interval spec agent reported "overlaps-touching killed a
   real surviving mutant" — of its own draft spec.
3. **Precise specs made implementations easy.** The implementers'
   first-try successes were not luck: the ediv implementer derived e-div
   from the division identity the spec pinned; the queue implementer
   handled the empty-front trap because peek-back-only spelled out the
   expected value shape. The verification effort concentrated where the
   error mass was — in getting the contract exactly right — and the
   implementation followed.

The reframed claim the ladder supports: **on this substrate, specification
is the load-bearing activity, and the kernel's verdict dimensions (proof,
testing, mutation) are the tools that make specs debuggable.** Implementation
becomes almost clerical once the contract is exact — which is precisely the
division of labor an AI-native language wants.

## Secondary observations

- **The guarantee ladder's honesty is legible in the results**: interval
  lands 18/18 PROVEN (non-recursive Int/Bool = the full-proof fragment);
  rle-expand is `termination unproven` and its props tested-only (Int-counted
  recursion); e-mod/e-div are tested-only (`%` is excluded from the SMT
  fragment because the kernel truncates and SMT-LIB is Euclidean — proving
  would certify the wrong theorem). Nobody had to remember these rules; the
  verdicts state them.
- **Equivalent mutants cluster in one syntactic family**: `(if (< a b) b a)`
  vs `<=` — min/max and duplicate-tiebreaks. Where the family is `%`-free,
  equivalence is machine-checkable (`minmax-equiv.smt2`); where bodies use
  `%` (ediv) the judgment is hand analysis only. A `waived-mutants` metadata
  field carrying the justification — and the SMT transcript when one exists —
  remains the right fix for making spec-strength scores machine-honest.
- **The firewall is policy, not enforcement.** Agent-driven implementers
  were told not to read the repo; nothing stops a malicious one. Server-side
  enforcement of context slices is exactly the M1 hosted-store threat model
  (#2/#3), and the `context`-hash journaling (#4) is its audit primitive.
- **Cost**: ~305k subagent tokens across 10 invocations for five modules,
  including the spec agents' private validation loops (the queue spec agent
  spent 40 minutes and 27 tool calls debugging its own spec — that is where
  the tokens went, and where they earned the first-try greens).

## A kernel bug the experiment found (#19)

`(data Interval [] (Ival Int Int))` and `(data Run [] (Run Int Int))` are
structurally identical, so they content-address to the *same object* — two
names, one hash. Working as designed. But `Meta` is hash-keyed, so putting
`Run` clobbered the shared meta's constructor names: `(Ival 1 5)` stopped
elaborating the moment the RLE module landed. Five hand-written example
modules never collided; the first agent-written corpus did so within hours.
The fix is a design decision (per-alias naming metadata vs. shared verdict
metadata) — filed as #19 rather than patched unilaterally.

## Prover gaps surfaced (backlog food)

- BST: count/sortedness-over-append lemmas absent from the library (4 props
  tested-only) — same lemma-mining pattern as #9.
- queue: 4 props unproven for the same reason (reverse/append interaction
  lemmas).
- merge (#17, pre-existing): lexicographic induction.
