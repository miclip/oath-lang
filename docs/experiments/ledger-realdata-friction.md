# ledger-realdata friction log

The third "depend on Oath, don't improve it" exercise, after
`webhook-friction.md` (#120) and `ledger-friction.md`. The artefact is
`docs/experiments/ledger-realdata/ledger.oath`; the instrument that produces
every figure below is `run.sh` beside it, and the input is
`transactions.journal` — a real-format hledger-style journal of **12,001
transactions, 26,819 postings, 1,378,806 bytes, three commodities (USD, EUR,
AAPL)**, with two- and three-decimal amounts.

**The difference from the predecessor is VOLUME AND SHAPE, and it is the whole
point.** `ledger-app` was one commodity, integer minor units, an association
list and a dozen postings — every dimension that could be small was small, so
nothing it reported could distinguish "Oath is fine here" from "this input was
too small to ask". This one balances PER COMMODITY, carries fractional amounts
exactly as scaled ℤ thousandths, aggregates on the native `str-map` that
`ledger-friction.md` demanded, and reads a file large enough that one backend
does not survive it.

**The friction is the deliverable.** Everything below is measured by `run.sh` on
this journal; nothing is argued. Where a measurement did NOT support the
conclusion it was reaching for, that is recorded too, because a corpus that
only ever confirms is not evidence.

Environment for every figure: Apple M4, 16 GB, macOS; Go 1.25.6, clang 21.0.0,
Z3 4.16.0; `OATH_PROVE_RLIMIT=20000000`, `OATH_PROVE_WALLCAP_SEC=120`.

`./docs/experiments/ledger-realdata/run.sh` reproduces all of it — **57 checks,
all passing**, in about twenty-five minutes, almost all of it spent on proof goals
that do NOT discharge (each burns its full deterministic budget through every
strategy before returning "unproven"). It writes nothing outside two temporary
directories: the store is a fresh `mktemp -d`, and the committed corpus, the
kernel and the journal are read-only throughout.

The artefact is **40 definitions and 3 datatypes**, every one analysed TOTAL,
with **39 properties PROVEN and 8 unproven** under the capped z3 above. Every
figure below is from one run of that harness:

| phase | seconds |
|---|---:|
| seed the dependency closure (5 files) | 1.9 |
| `put` ledger.oath (elaborate, typecheck, 200 cases per property) | 1.6 |
| prove, 23 definitions | 630.5 |
| the dependency control | 389.1 |
| the proof-monotonicity probe | 332.6 |
| build, go / llvm | 0.7 / 1.1 |
| run the full journal, llvm / go+stack | 1.4 / 3.9 |
| bisect the go ceiling | 21.3 |

**Nothing here is slow except proving.** Over 95% of the wall time is proof
goals that do NOT discharge — each burns its full deterministic budget through
every strategy before returning "unproven".

## Demands, ranked by measured consumer impact

Ranked by *what a consumer loses*, which is not the same as which finding is
most interesting. #1 is a single design decision that this journal breaks in two
different ways on two different backends. #2 removes the reason to have chosen
Oath while leaving the program working. #3 and #4 cost only code.

### 1. A file is one inductive `Str`, and the whole file is parsed by structural recursion over it. That is the wall.

**One cause, measured in two currencies.** `readfile` has type `Str -> Str`, so
a file arrives as a single `Str` — a codepoint-at-a-time inductive datatype — and
`str-split` is the only way to turn it into lines. `str-split` recurses
structurally over that `Str` and rebuilds the head piece on the way back out, so
for a file of N codepoints it descends N frames and allocates N buffers.
Nothing in this program is deep or large. The INPUT is, and the model hands the
whole of it to the recursion at once.

Both of the two artifacts hit that, and they hit it in different places only
because their hosts price it differently.

#### (a) Go: a deterministic stack crash at 81% of this journal

The Go artifact dies on the pristine journal with the Go runtime's own panic —
not an Oath refusal, exit 2, no diagnostic of its own:

```
runtime: goroutine stack exceeds 1000000000-byte limit
fatal error: stack overflow
...
main.f_str_split_0292b5f6.func1(...)
main.oathStrHead(...)
```

`run.sh` bisects the ceiling with both endpoints validated:

| | bytes | journal lines | share of this journal |
|---|---:|---:|---:|
| largest prefix that SUCCEEDS | 1,116,549 | ~41,164 | 81% |
| smallest prefix that OVERFLOWS | 1,119,047 | | 81% |

The Go runtime's 1 GB goroutine stack divided by that ceiling is **894–896 bytes
of stack per journal codepoint** — a bracket, because the ceiling is one.

#### (b) LLVM: about 2.8 KB of resident memory per journal byte — linear as far as it can be measured, and it cannot be measured at full scale

The LLVM artifact does not crash here, because #178's Option A already gave it a
1 GiB dedicated worker stack. It reaches the OTHER half of the same cost
instead. Measured as a six-rung ladder across three and a half orders of
magnitude of input, peak RSS from `getrusage(RUSAGE_CHILDREN)` (the macOS/Linux
unit difference handled explicitly):

| journal bytes | peak RSS | RSS per input byte | in the fit? |
|---:|---:|---:|---|
| 421 | 3 MB | 6,538 | no — startup dominates |
| 27,116 | 74 MB | 2,850 | no — below the size floor |
| 135,422 | 365 MB | 2,829 | yes |
| 325,391 | 872 MB | 2,809 | yes |
| 677,973 | 1,815 MB | 2,807 | yes |
| **1,378,806** | **2,405 MB** | **1,829** | no — past the cutoff; **inconclusive** |

**Linearity is established only where the instrument works, and the full journal
is not there.** Peak RSS measures the footprint while the process stays small
relative to physical memory; past that the OS reclaims and the number falls
BELOW demand. So:

- The fit uses only rungs whose peak stayed under an eighth of physical memory,
  **a cutoff derived from the machine rather than hand-picked**. Over those
  rungs the per-byte cost is flat to within 1%, and `run.sh` asserts it (stable
  within 25%).
- **A reproducibility control runs inside that regime**: one fitted rung is
  measured a second time and the two peaks must agree within 3%. That is what
  licenses quoting the fitted constant at all.
- **And the cutoff is what made the constant reproducible.** This is a
  cross-run observation, not a figure the single run above produced: across four
  earlier harness runs on this machine the three fitted rungs came back at
  2,829 / 2,809 / 2,807 B/B every time — identical — while the excluded top rung
  wandered over 2,527, 2,097, 1,886 and 1,829. All of the variability was in the
  one rung the instrument cannot measure. Before the cutoff existed that rung was
  inside the fit and the whole measurement looked noisy to ±30%; it was not the
  program that was noisy. `run.sh` reproduces the fit and the within-run
  reproducibility control; it does not reproduce this history.
- **The excluded rung is INCONCLUSIVE, not corroborating.** An earlier version
  of this section checked that the top rung did not EXCEED the fitted prediction
  and read that as evidence against super-linear growth. That check has no
  failure mode where it was applied: reclamation can only make peak RSS read
  LOW, so it cannot fail whatever the true demand is, and a reading below the
  model is equally consistent with linear demand and with demand growing faster
  than the model. It has been removed. A check that cannot fail is not evidence.

What that leaves, stated as three separate kinds of claim:

| | |
|---|---|
| **MEASURED**, low-pressure regime | **2,815 bytes** of resident memory per journal byte, stable across the fitted rungs and reproducible within 3% |
| **MEASURED**, full journal | **2.35 GB peak RSS** — a reading taken while the OS is reclaiming, so it does not track demand |
| **PROJECTED**, not measured | extending the fitted slope puts the full journal at **~3.6 GB** and a 10 MB journal at **~26 GB** |

**Memory pressure prevents validating that extrapolation at full scale on this
machine.** The only instrument available stops measuring at about the size the
projection would have to be checked at, so nothing here rules out the demand
growing faster than the model — the linear result belongs to inputs up to
roughly 680 KB, and the full-journal and 10 MB figures are model output.

Meanwhile the program itself is fast: the whole file is read, parsed, validated,
aggregated and reported in **1.4 s**.

**Why it costs that.** Every `SCons` in the emitted LLVM allocates a fresh buffer
holding the prefix plus a copy of the tail, and the request arena frees nothing
within a run. `str-split` does that once per codepoint rather than once per line.
The runtime's own comment on the HTTP path puts the same ratio at "1 MiB is about
190 MiB of arena"; the file path measures an order of magnitude worse, for that
reason. This is #178's closing note — *"the next binding constraint is the HEAP,
not the stack"* — arriving in a second, unrelated program.

#### THE DEMAND: streaming, byte-oriented text

Not a bigger stack and not a smaller allocation constant. **A file should not
have to become one `Str` for a program to read its lines.** Concretely, in the
order they would help:

- **A line-oriented read capability** — `readfile-lines`-shaped, yielding
  `(List Str)` (or better, a fold) without the whole file ever existing as a
  single inductive value. This is a capability-vocabulary question (#117), not a
  backend one, and it removes the depth AND most of the allocation at once.
- **Or `Str` split results as VIEWS.** The LLVM runtime already hands out the
  TAIL of a `Str` as a view into its parent buffer; the HEAD is a fresh buffer
  plus a copy. Making both views would cut the per-codepoint allocation without
  touching the language, though it leaves the recursion depth alone.

The test for either is not a benchmark: it is whether a consumer can process a
file whose size is not bounded by memory. Today it is bounded by memory on one
backend and by stack on the other, and both bounds are proportional to the file.

#### Two measured MITIGATIONS — and neither of them is that demand

**Mitigation 1: `debug.SetMaxStack` in the emitted Go `main`.** The natural
reading of (a) is that 1 GB is a hard property of the Go runtime. It is not:
`debug.SetMaxStack` raises the per-goroutine limit at run time and the emitted
program never calls it, so part of that ceiling is an emitted-code default. The
only way to tell a host limit from a default is to try it, and section 7b does —
the SAME emitted source, one import and one call, rebuilt:

```go
import ( "runtime/debug" ... )
func main() { debug.SetMaxStack(8 << 30); ... }
```

| | result |
|---|---|
| emitted Go, as shipped | `fatal error: stack overflow`, exit 2, at 81% of the journal |
| emitted Go + `SetMaxStack(8 GiB)` | **exit 0**, 12,001 transactions, 3.9 s |
| ...what that costs | **1,693 MB peak RSS** — 1,288 bytes per journal byte |
| ...where the new ceiling sits | **~9.2 MB of journal** (8 GiB ÷ the 895 B/codepoint measured above) |
| ...and its output vs the LLVM artifact | **byte-identical** |

Identical source, one added call, opposite outcomes, with section 7's bisection
as the other half of the control. **But the recursion depth is unchanged; only
the room to perform it grows** — and the last three rows are there so that
cannot be read as a fix. The raise buys one thing: the Go artifact reads this
journal. It costs 1.7 GB resident, which puts the Go artifact in the same
territory as (b) rather than escaping it, and it relocates the ceiling to about
9.2 MB of journal — **a deterministic failure at a measured size traded for a
larger failure at a size nobody has measured**, still arriving as a raw runtime
traceback.

Two smaller things the numbers say in passing. The patched Go pays 1,288 bytes
per journal byte against LLVM's fitted 2,815 — the same order, the same cause, a
different constant. That one IS a sound reading rather than a projection: at
1,693 MB it sits below the reclamation cutoff that makes (b)'s own full-journal
rung inconclusive, so the Go artifact is the only one of the two whose
full-journal footprint this harness can actually measure. And 9.2 MB is not a
comfortable margin for a plaintext journal: it is about seven times this one.

Worth doing. Not the demand.

**Mitigation 2: a Go-side stack guard.** The emitted Go contains no stack check
at all. The LLVM runtime has `o_stack_floor` / `o_stack_exhausted` and turns the
equivalent condition into a legible exit 70 naming what happened — that was
#178's disposition, and it was never carried across to the other backend. This
raises nothing. It makes reaching the ceiling legible, which is the difference
between an Oath artifact declining and a Go binary crashing. Also worth doing;
also not the demand.

### 2. The payoff properties are the reason to use Oath, and this program's do not all reach PROVEN — one of them can even be un-proved.

Three separate measurements, all from `run.sh`, in increasing order of how
surprising they are. They are different failures, not three views of one: (a) is
the library's proof STATE, (b) is the induction heuristic's REACH, and (c) is
the store's HANDLING of a verdict it already holds.

**(a) The standard library ships TESTED, so a consumer's own conservation law
does not prove until they prove the library themselves.** `bump.adds-at-key` —
"adding an amount to a key changes that key's balance by exactly that amount",
the single accumulation primitive every posting in the journal passes through —
does not discharge while `str-map`'s own laws are merely tested. The unproven
report names the dependencies, and **`run.sh` tries the named repair rather than
believing it**: proving `str-lt`, `str-eq`, `smi-lookup`, `smi-insert`,
`str-map-empty`, `str-map-lookup`, `str-map-insert`, `str-append`, `append`,
`length` makes `bump.adds-at-key` PROVE.

So the demand is concrete and belongs to the project, not the consumer:
**ship the `str-map` / `str` / `list` corpus with its laws PROVEN, not
`tested`.** A consumer cannot reasonably be expected to discover that their
property is blocked on someone else's proof state, and the cost of finding out
is measured here at roughly two minutes of z3 plus knowing to look.

**(b) The direct balance/error equivalence does NOT discharge, with every
relevant law available.** The new property is

```
(prop errors-iff-imbalanced [(hdr Str) (ks Lines) (m StrMap)]
  (== (== (length [Str] (tx-errors hdr ks m)) 0) (all-zero ks m)))
```

— "a transaction produces no failure exactly when it balances in every
commodity", both directions in one biconditional, because a checker that misses
an imbalance and a checker that invents one are the same property failing on
opposite sides. Composed with `adjudicate.refuses-iff-failures` (which PROVES)
it is the whole safety chain: *balanced ↔ no failure ↔ a report rather than a
refusal.*

It is an ordinary structural induction over `ks`. It does not discharge with the
dependencies tested, and it still does not discharge after `run.sh`'s control
proves every dependency the unproven report names — measured separately at 78 s
for the single re-attempt with 19 dependency lemmas in the library.
`close-txn.preserves-balances` and
`preserves-totals` behave the same way.

**DEMAND: the induction heuristic should reach a structural recursion whose
result is consumed by `length … == 0`.** Note the shape: `tx-errors` returns a
LIST and the goal is about its EMPTINESS, so the induction has to carry a fact
about the list's constructor through a `length` call. That is the same class as
`ledger-friction.md` demand 3 — *a fold whose only obstacle is a function
appearing where the goal does not need its value* — surviving into a program
that has no association list left in it.

**(c) Proving a dependency can DOWNGRADE an already-stored `proven` verdict.**
This one was found, disbelieved, and then reproduced in a controlled probe
(`run.sh` section 3b) that builds a strict SUPERSET pair of proof states in its
own temporary store:

| state | what is proven | `parse-posting.rejects-two-fields` |
|---|---|---|
| X | `strip-comment` only | **`proven`** |
| Y ⊃ X | X plus eight more dependencies | **`tested`** |

`prove` re-derives from scratch and the STORED verdict follows the LAST attempt,
so a larger lemma library withdrew evidence that had already been recorded.
Nothing about the property changed; nothing the consumer did was wrong. In the
same step `rejects-four-fields` moved the other way, so the *capability* did not
shrink — but the *evidence in the store* did, and in a content-addressed system
where verdicts are hash-keyed facts about a definition, silently losing one is a
different thing from failing to gain one.

**DEMAND: `prove` should never lower a stored verdict.** A re-derivation that
fails to reproduce an existing proof is a fact about this attempt's search, not
about the theorem; the recorded verdict should be the best result ever obtained
for that (property hash, definition hash), and a weaker later attempt should be
a no-op. **First measured on a superset pair, so it is a real
non-monotonicity and not two unrelated states being compared.**

**A smaller, related precision demand.** The unproven note for
`errors-iff-imbalanced` names `show-amount`, `show-abs` and `pad3` as
dependencies whose laws might help. They cannot: the goal is about the LENGTH of
the error list, and those three only render the text INSIDE an element. The note
sends a consumer to prove the rendering chain — which `run.sh` did, and it
changed nothing. The footprint filter is over-approximating through a
constructor argument.

### 3. Reading and writing a signed decimal number costs seven definitions, and `parse-nat` still cannot fail.

`ledger-friction.md` demand 2 was "`parse-nat` has no signed sibling", costing
one hand-peeled `-`. With real money — signed, with two or three decimals — the
cost is now `parse-amount`, `parse-abs`, `frac3`, `digits-ok`, `pad3`,
`show-abs`, `show-amount`: **seven definitions, none of them about ledgers**.
Every program that reads a number will write them again.

The sharper half is that **`parse-nat` has no failure mode.** On a non-digit it
returns a number — `(- c 48)` for whatever byte it found — so no property about
`frac3` can be stated over "every `Str`", because there it is false. A
`digits-ok` predicate had to be defined purely so the real claim could be
written at all:

```
(prop is-a-fraction [(f Str)]
  (if (digits-ok f) (and (<= 0 (frac3 f)) (< (frac3 f) 1000)) true))
```

That property PROVES. It could not have been stated without `digits-ok`, which
carries no logic the parser did not already have — the `hex-valid` move (#121),
arriving independently in a second domain. **The recurrence is the finding**: a
total parser with no failure value forces every consumer to invent the domain
predicate before they can say anything true.

**DEMAND:**
- `parse-nat : Str → (Option Int)`, failing on a non-digit rather than
  returning garbage — and with it, the `digits-ok`-shaped predicate stops being
  something each consumer re-derives.
- `parse-decimal` and `show-scaled` in the standard library, over scaled ℤ, so
  exact fixed-point money is a library concern and not an application one.

### 4. Still open from `ledger-friction.md`: no forward references within a file.

Unchanged and now costlier: this file hand-orders **40 definitions** by
dependency, and adding `all-zero` meant placing it above `tx-errors` rather than
beside the property that uses it. At this size the ordering is bookkeeping a
reader has to maintain mentally while editing.

## The prior demand list, classified

| `ledger-friction.md` | status | evidence |
|---|---|---|
| **1. a native `Str`-keyed container** | **RESOLVED** | #184's `str-map` carries the whole aggregate; `run.sh` asserts NATIVE lowering in the emitted source on both backends |
| **2. `parse-nat` has no signed sibling** | **STILL OPEN**, and the cost has grown | seven definitions, above |
| **3. the conservation lemma does not auto-prove** | **HALF resolved — and only the half the new property can demonstrate** | see below |
| **4. no forward references** | **STILL OPEN** | 40 hand-ordered definitions |

**Demand 1 is resolved, and this exercise is the first consumer to use the
result at volume.** The `(account, commodity)` composite key is built by
concatenating with `0x1f` and split back for the report by `render-key` (whose
round-trip property PROVES), so the native container reaches a genuinely
composite aggregate — 26,819 postings folding into 21 buckets.

`run.sh` nonetheless asserts `smapInsert`/`smapLookup`/`smapKeys` in the emitted
Go, the ABSENCE of the `f_smi_*` structural bodies, and
`@o_strmap_insert`/`lookup`/`keys` in the emitted IR. **Not because the
namespaced-discovery gap is still open — it is not.** `strmap-consumer-friction.md`
found it from the registry side and #186 closed it: `opNameIndex` keys the
container vocabulary by a name's final path segment and `resolveOp` prefers the
bare name while falling back to a namespaced alias, so a store carrying str-map
only as `michael/oath/str-map-*` is discovered exactly as a bare-name store is.

The assertions earn their place for two other reasons. `validateFamily` is
deliberately FAIL-CLOSED — one operation that does not match its canonical
signature over a consistent datatype drops the WHOLE family to the structural
path — and that drop is as quiet as the old one was: same answers, same exit
code, same report, O(N) per lookup. And they are regression evidence for this
document: without them, the memory and timing figures above could have been
measured on the fallback and nothing would have said so.

**Demand 3 is where the classification has to be exact, because the obvious
summary is wrong in both directions.**

- **The single-step law is resolved.** `ledger-friction.md`'s blocked property
  was `bal-add.preserves-sum`, and its diagnosis was the `str-code` guard: the
  solver churned on a recursive function in an `if` condition whose VALUE the
  goal did not need. The native `str-map` removes that guard from the consumer's
  body entirely, and the analogous law here — `bump.adds-at-key` — **PROVES**
  (once the library's own laws are proven; see demand 2a above). To that extent
  demand 3 is discharged, and by a route it did not anticipate: the container
  landed, and the proof obstacle went with the code it was in.
- **The fold law is not resolved, and the new property is what shows it.**
  `close-txn.preserves-balances`, `close-txn.preserves-totals` and
  `tx-errors.errors-iff-imbalanced` all fail to discharge with every dependency
  proven. So the remaining obstacle is NOT the one demand 3 named — there is no
  guard, no `str-code`, and no association list left. **The demand as written is
  therefore both resolved and too narrow**: what still fails is structural
  induction over a list whose result is consumed by a `length … == 0` test, and
  that is a different, larger request than the one `ledger-friction.md` filed.
- **Nothing here says the properties are false.** All three pass 200 generated
  cases and are recorded `tested`. *Not proved* is not *disproved*; what is
  measured is the prover's reach, and the sentence has to say so.

## What is NOT friction

Recorded so a later change does not quietly regress it. These are the parts the
exercise confirms are load-bearing and correct, and several of them are the
things a smaller input could not have tested.

- **Exact ℤ money is the whole reason, and it holds at volume.** All 21
  (account, commodity) balances, all three commodity totals and both counts
  match an INDEPENDENT awk oracle exactly, in integer thousandths. The three
  commodity totals are `0.000` — equality, not a tolerance.
- **The float comparison is reported honestly, including the half that does not
  flatter the argument.** `run.sh` accumulates the same 26,819 postings a second
  time in IEEE binary64, in file order, the way a ledger written in a language
  with float money would. **17 of the 21 balances differ from the exact value**,
  worst drift `1.02e-08` units. But **the commodity-total VERDICT differs in 0 of
  3 commodities**: the totals cancel exactly, because each transaction is a small
  set of equal-and-opposite postings and the rounding cancels with them.

  So **a float ledger would NOT have been caught by this journal**, and saying
  otherwise would be reading a corpus as a statement about ledgers in general.
  The supportable claim is narrower and still the whole point: the balances are
  already wrong in the low bits after 26,819 postings, and whether that ever
  reaches a cent is an empirical question about scale that has to be re-asked for
  every journal. Exact ℤ removes the question rather than answering it — the
  value of the guarantee is the ABSENCE of a scale at which it could stop
  agreeing, which is exactly the kind of thing a passing corpus cannot
  demonstrate.
- **The native `str-map` is frictionless in use and fast at volume.** 26,819
  postings fold into 21 buckets; the whole journal is read, parsed, validated,
  aggregated and reported in 1.4 s. `run.sh` asserts the lowering in the emitted
  source on both backends, because it is the one property of the artifact that
  degrades silently if family validation ever rejects an operation — see
  prior demand 1 above.
- **The three-way gate held.** `oath eval` ≡ the Go artifact ≡ the LLVM artifact,
  byte for byte, on a fixture — and the two artifacts agree byte for byte on
  9,727 transactions — the largest clean prefix the Go artifact survives.
- **The refusal protocol is exactly right for this domain and identical across
  backends.** A corrupted journal exits 1, writes NOTHING to stdout, and names
  the transaction, the commodity and the exact residual —
  `unbalanced: 2023-01-01 Opening balances [USD residual 0.010]` — from both
  backends, byte for byte.
- **Capability confinement, again with no effort.** `readfile` is analysed
  `confined` automatically; the program cannot reach a file it was not handed.
- **Every definition is TOTAL**, analysed, with no annotation. For a checker that
  is not a nicety.
- **Type aliases (`Amount`, `Lines`, `Errors`) cost nothing and are identity-
  transparent**, exactly as SPEC §1 says. `Int` appears in this file as a
  codepoint, a length, a counter and a scaled money amount, and only one of those
  is scaled; naming it is worth real reader effort and costs no hash.
- **`oath build` is fast on both backends** — well under a second each, including
  the LLVM path that shells out to clang. Reading, parsing, validating and
  reporting 26,819 postings then takes 1.4 s (LLVM) or 3.9 s (Go, with the stack
  raised), so nothing here is slow; the costs are memory and proof, not time.
- **Comment and field-shape handling survives contact with the real file.** An
  inline `; note` appended to all 101 postings of a prefix leaves the report
  BYTE-IDENTICAL; an indented whole-line comment is a comment and not an
  unreadable posting; and a four-field priced posting (`10.00 USD @1.09`, which
  this representation cannot carry) is REFUSED and quoted back rather than
  silently truncated to three fields. The journal itself contains no inline
  comments, so `run.sh` MANUFACTURES them — a check that ran only on the
  pristine file would have passed whether or not the feature existed.
- **Nothing in this program is outside the LLVM subset.** A multi-commodity
  ledger with fixed-point arithmetic, string keys, native maps and a file
  capability builds on the narrow backend without a single refusal.
