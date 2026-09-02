VERDICT: YES — the definition-instantiation result GENERALIZES. The same procedure
that discharged `opt.preserves` (correct by smart-constructor FOLDING) also discharges
`swap.preserves` (correct by COMMUTATIVITY of `+`/`*`) — a structurally different
transformation, stuck on the SAME obstacle, cracked by the SAME fix, with the
non-fabrication control holding. And `swap.preserves` is specifically a
STUCK-UNPROVEN goal, so this covers the OTHER direction of z3's non-monotonicity
(SPEC §7.2): not "a proof recorded then dropped" but "a true theorem never recorded".

This is the go/no-go measurement for a kernel instantiation STRATEGY. It is a GO.

---

# Second data point: `swap.preserves`

`swap` (in `../../optimizer.oath`) rebuilds the tree with operands REORDERED —
`(Add a b) -> (Add (swap b) (swap a))` — so it is value-preserving because `+` and
`*` commute, NOT because it preserves structure (its Control-2 role in `run.sh`:
"a genuine value-preserving transform whose correctness composes ev with swap does
NOT discharge"). Unlike `opt`, no smart-constructor soundness lemma is even in play;
the obstacle is purely the quantified unfolding of `ev` and `swap` at the Mul node.

All scripts below are the KERNEL'S OWN emitted bytes (the `scriptAttempts` seam,
`extract-instrumentation.go.txt`), instantiated by the SAME `../instantiate-defs.py`
invoked `<emitted> <Add|Mul> ext <out>`. Raw z3 outputs, each carrying its command,
are in `z3-output/`.

## Results (`:rlimit 15000000`, z3 4.16.0)

| script | verdict | consumed rlimit | note |
|---|---|---|---|
| emitted `binder 0 ctor 1` (Add), quantified | unsat | 5,407 | the Add case already proves |
| emitted `binder 0 ctor 2` (Mul), quantified | **unknown** | 15,000,112 | the STUCK case (`canceled`) |
| **defs-instantiated Add** | **unsat** | 1,853 | |
| **defs-instantiated Mul** | **unsat** | 1,847 | ~8,000x under budget vs the exhausted quantified run |
| **control: wrong swap, Add (FALSE case)** | **sat** | — | no fabrication |
| **control: wrong swap, Mul (correct case)** | **unsat** | — | |

The Mul subgoal goes from 15,000,112 rlimit (exhausted, `canceled`) to 1,847
(`unsat`) — the identical signature to `opt.preserves` Mul (15,000,262 -> 5,183),
on a transformation with a completely different correctness argument. The obstacle
is quantifier instantiation, not the arithmetic, the case split, or a missing lemma.

## Non-fabrication control

`bad-swap.oath` defines `swapb`, whose Add case DROPS one operand and DUPLICATES the
other — `(Add a b) -> (Add (swapb a) (swapb a))` — so `(ev (swapb e)) = (ev e)` is
FALSE (it computes `2*ev(a)` at an Add). Its Mul case is left correct, as a
positive foil. The kernel agrees the whole def is wrong:

    ✗ swapb  #4982dcf4d3f4  FALSIFIED: preserves
        counterexample: (Add (Lit -14) (Lit -3))

Its subgoals were emitted by the same seam and instantiated the same way. The FALSE
Add case comes back **sat** and the correct Mul case **unsat** — the procedure does
not turn a false transform into a proof, so the `unsat` results above are
measurements, not artifacts of instantiation.

## Checksums (primary artifacts)

| file | SHA-256 |
|---|---|
| `swap-preserves-defs-instantiated-binder0-ctor2-Mul.smt2` | `232fc4c1982b1c8791d68eb010a3973e648001cca3d61ae301939e77ee464fa2` |
| `swap-preserves-defs-instantiated-binder0-ctor1-Add.smt2` | `d6efebbb1a1672e313bf022e67ad502a145a18187d5f225199d6081018d8b765` |
| `badswap-preserves-defs-instantiated-binder0-ctor1-Add.smt2` (control) | `66d377bddfad71d802ff2312e4e203284fa219f80b60860c0948ba2f70693187` |
| `bad-swap.oath` (control source) | `4a72f74767488a5f37875911289e616f100f6bc797e467301da0de684bebdb93` |

## What this establishes, and what it does NOT

Establishes: the technique is not tied to `opt`'s smart-constructor shape. Two
composed-recursion goals with unrelated correctness arguments (folding;
commutativity) are both stuck on the Mul-case quantifier instantiation and both
discharged by grounding the FUNCTION DEFINING EQUATIONS at the terms the goal
mentions — with the wrong-transform control refusing in both. Because the
instantiated proof does not depend on which other lemmas are asserted, it removes
the search-dependence that makes §7.2's F non-monotone in BOTH directions
(proof-dropped, and — as here — provable-but-never-recorded).

Does NOT establish: (1) generality beyond the Add/Mul expression-tree family —
lists, nested/mutual recursion and higher-order are untested; (2) an AUTOMATIC
instance-selection rule — the `ext` schema (parent defn + inlined result + `ev` at
the two recursed subterms) is hand-tuned per shape here, and a kernel strategy must
DERIVE the instances from goal structure; (3) soundness of instantiating a PARTIAL
function's equation — grounding an equation at an undefined point is unsound, so a
kernel strategy must gate instantiation on the totality verdict it already computes.

Those three are the design questions for the kernel instantiation strategy, not
open questions about whether the technique works. It works.
