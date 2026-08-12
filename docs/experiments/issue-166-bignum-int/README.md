# #166: arbitrary-precision `Int` in the LLVM backend

The LLVM backend stored `Int` as a `long long` and refused any literal outside
it. That was an honest subset — it said so, and it never wrapped — but Oath's
`Int` is ℤ, and #166 records why that is a *semantic* commitment rather than a
representation choice: the prover's `Int` is unbounded, so a machine-width `Int`
would put overflow reasoning into every arithmetic proof in the corpus.

This directory holds two things: the record of the cheap falsifier #166 offered
against itself, and the boundary evidence for the change that followed.

## Part 1 — the falsifier, run first, and it did not fire

#166 asks for the bignum runtime and then offers a way not to need it:

> **Cheap, and it can win — run it first.** A *checked* int64 is fail-closed
> rather than wrapping: trap on overflow and exit with a named diagnostic
> instead of producing a wrong answer. If no program the backend accepts can
> observably distinguish checked-int64 from ℤ, the arbitrary-precision runtime
> is unnecessary and the honest design is the trap.

So the trap was built first — `+` on two `Int`s lowered to a checked add, a
named diagnostic on overflow, nothing else about the numeric tower touched — and
pointed at the boundary. **A checked int64 is observably distinguishable from
Oath's `Int` by a program the subset accepts:**

| path | `(let (n Int (+ 9223372036854775807 1)) (if (== n int64min) "wrapped-int64" "not-wrapped"))` |
|---|---|
| `oath eval` | `not-wrapped` |
| Go backend | `not-wrapped`, exit 0 |
| LLVM, checked | no stdout, exit 70, `oath: Int overflow in '+': 9223372036854775807 + 1 leaves int64…` |

Three things made that decisive rather than suggestive.

`not-wrapped` was **PROVEN** — Z3, direct, over unbounded ints. So the artifact
was not disagreeing with another implementation; it was failing to exhibit a
property the kernel had proved of the definition it was compiled from.

Two literal operands invite the objection that a compiler could decide the
overflow statically and refuse at build time, which would make the trap an
implementation choice rather than a limit. So a second entry took its operand
from `argv` — the first codepoint of the first argument. It built without
complaint, and **one binary took both sides of the boundary from its input**: no
argument completed, any argument refused. No build-time analysis reproduces that.

And the subset could not even NAME the right answer. `9223372036854775808` was
refused at compile time by the old `int-range` check, so every comparison had to
be against int64 min — the value a *wrapping* backend produces — with ℤ's answer
observable only as "none of the above".

The honest scope of that result, stated because it is easy to overread: it
establishes that the subset ADMITS a distinguishing program, not that a program
anyone wants would overflow. Those are different populations. What it settles is
narrow and sufficient: **the trap alone is not an adequate answer to #166.**

## Part 2 — the representation

Sign-magnitude, base 2³², least-significant limb first, written into the emitted
C runtime. **No dependencies** — that is a constraint on the whole backend, not a
preference: IR is emitted as text and clang is invoked, and nothing links GMP.
32-bit limbs so every intermediate fits a 64-bit unsigned, which avoids
compiler-specific 128-bit types as well as any library.

Two invariants, established in exactly one place (`o_int_wrap`) and relied on
everywhere: `isign` is 0 **iff** the value is zero, and the limb count carries no
leading zeros. Together they make comparison a limb-count test first.

Three consolidations, each removing a way for two implementations of one fact to
drift apart:

- **Addition and subtraction are one function.** `a - b` is `a + b` against a
  flipped operand sign, so the carry/borrow reasoning exists once.
- **`==`, `<` and `<=` all derive from `o_int_cmp`.** A hand-written equality
  comparing the fields its author remembered is the incompleteness this project
  keeps finding; there is no second notion of "same `Int`" to drift from this one.
- **Literals are emitted as decimal text, not limbs.** The digits are the
  canonical form the AST already holds, so the emitter needs no encoding of its
  own and nothing has to agree with the runtime about limb order or width. This
  is what retires `int-range`: there is no longer a literal the backend cannot
  carry. `int-missing-value` stays — a term with no value is a malformed AST, not
  a magnitude.

Lowered: `+ - * / %` and `== < <=`, both operands `Int`. **`neg` stays refused
by name** — it is unary, so it shares no lowering path with the binary
operations and is the remaining Int boundary.

Division was the harder half and was a genuinely different problem: a
quotient/remainder algorithm, and a division-by-zero disposition that had to
match `oath eval`. That disposition is where the interesting finding is — see
below.

## Part 3 — the evidence

`probe.sh` — 18 checks. The same entries from Part 1, now asserting **strictly
more**: where the probe recorded that the artifact REFUSED, it records the exact
value it produces. Refusal is one bit; the value is the whole answer. The
input-dependent entry now asserts an *identity* rather than a constant —
`(max + c) - max == c` for whatever `c` the input supplies — which no
fixed-width runtime satisfies for any `c > 0`, whether it wraps or traps.

`fuzz.py` — randomised differential search, **four oracles**: Python computes
each expectation, and `oath eval`, the Go backend and the LLVM backend must all
answer `all-ok`. A wrong expectation cannot pass unnoticed (the three paths would
reject the same case together, which the script reports as a *generator* bug
rather than a backend one), and a wrong lowering cannot pass either.

The named cases live in `../issue-158-llvm-subset/int-arithmetic.oath` and run
from that directory's acceptance script, which #166 asked to be extended rather
than replaced: large-literal parsing, carry, sign, cancellation, multiplication,
ordering, division and modulo including both by-zero dispositions, and the `neg`
refusal control.

## The Go backend named the wrong operation, and only three-way agreement saw it

Holding all three paths to naming the by-zero condition — rather than merely to
failing — turned up a defect in the backend nobody was changing. `big.Int.Quo`
and `big.Int.Rem` BOTH panic with `division by zero`, so a compiled artifact
reported a division fault for a modulo the program never performed, while the
interpreter distinguished the two. `compile.go` now routes `/` and `%` through
`iquo`/`irem`, whose only job is to name the condition.

It had been wrong since the Go backend gained arithmetic, and the comment above
the emission asserted the opposite — *"both panic on a zero divisor, matching
eval's div/mod-by-zero error"*. A correspondence claimed in a comment and never
checked; the gate that checks it is nine lines long.

**The general form: a two-way differential gate cannot see an error two
implementations inherit from the same host library.** The Go backend agreed with
itself, and any comparison against it alone would have agreed too. It took a
third path, written against the specification rather than against Go, asking
what the failure was CALLED.

## The instrument was wrong first, and that is the finding

The fuzzer **passed a deliberately injected bug** — `magadd` discarding its final
carry limb — 150 cases at a time. Two independently drawn operands almost never
sum across a 32-bit limb boundary: a random *k*-bit operand has a top limb that
is not full, so the top limb rarely overflows. The one defect class the fuzzer
existed to find was the one its generator could not reach.

The repair is to CONSTRUCT the crossing rather than hope for it: pick a boundary
2³²ᵐ, put one operand just below it, choose the other to land just past. With
that, the same mutant is caught on case 0.

Mutation results after the repair, all at 150 cases:

| injected defect | caught |
|---|---|
| `magadd` drops the top carry limb | yes — `cancel` |
| `magmul` drops inter-limb carry propagation | yes — `mul` |
| `o_int_wrap` skips normalization | yes — `cancel` |
| `magsub` ignores the borrow | yes — `cancel` |
| `o_int_cmp` ignores sign for negatives | yes — `lt` |
| `o_int_dec` ignores the minus sign | yes — `lt` |
| `o_int_wrap` permits a negative zero | **no — equivalent** |

The last is not a gap. No caller reaches `o_int_wrap` with a negative sign and an
empty magnitude: cancellation passes sign 0 explicitly, multiplication by zero
has sign 0, and the emitter never writes the literal `-0`. Under the mutant every
zero is spelled the same way, so no program can tell. The clause is kept as
defence for callers that do not exist yet, and the code says so rather than
implying the mutation was missed.

One method note, earned: a stale `oath` binary produced a false alarm mid-run —
a multiplication defect reported against source that had already been reverted,
because the restore rebuilt the file and not the binary. Both scripts here build
the CLI from the checkout for exactly this reason, and `OATH_BIN` is the opt-in
override for a caller who means to test a specific binary.

## What is not established

- **Performance.** Schoolbook multiplication, an allocation per value, and
  nothing frees. This is a correct representation, not a fast one, and no
  measurement here says otherwise.
- **Unary negation.** `neg` is refused by name and is the remaining Int
  boundary. It is unary, so it shares no lowering path with the binary
  operations and none of the evidence here reaches it.
- **How large real programs get.** The magnitudes exercised here were chosen to
  break a base-2³² implementation, not sampled from anything.
