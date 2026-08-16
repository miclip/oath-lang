# #115 — the same grid measured in TIME

**Status: MEASURED. On the time axis the recommendation is DECLINE #115 AS
SCOPED**, for the same reason `issue-115-sizes.md` gave on the memory axis and
with a figure of its own rather than by inheriting that one.

Nothing is proposed as normative here and **nothing was built** — no prototype,
no backend change, no patch applied and reverted.

## Why this document exists

Four preceding documents measured `gh-webhook` in BYTES. #115's own motivation is
not bytes: it is *"what it does not do is run fast"*. A recommendation reached on
peak RSS therefore answers a question the issue did not ask, however carefully it
was measured — an argument about speed settled on a memory number is settled on
the wrong axis. This measures the axis the issue names, over the grid
`issue-115-sizes.md` already established as the real one: published GitHub
webhook payload sizes 6,032–25,249 bytes with a median near 7,740, and
`repository` between 0.3% and 75.9% of the way in with a median near 22.1%.

The nine cells are the same nine cells. The offsets were derived here as
`round(pct × n)` and reproduced that document's table exactly (18/1,333/4,578,
23/1,711/5,875, 76/5,580/19,164), which is the check that the two documents
describe one grid and not two.

## The instrument

`issue-115-timing/latency.py`. **A separate instrument from
`issue-115-premise/measure.sh`, not a mode on it.** That harness starts the
server, sends exactly ONE request, kills it and reads peak RSS out of
`/usr/bin/time -l`; its whole lifecycle is one cold request, because a peak is a
high-water mark and a second request cannot lower it. Warm steady-state latency
is the opposite lifecycle — one server, many requests, the first ones
deliberately discarded — so the two cannot share a run and a shared script would
have to branch on which lifecycle it was in.

**WHAT IT DOES SHARE IS THE ONE THING THAT WOULD DRIFT INVISIBLY: the bytes of a
signed request.** A harness that spelled its own headers could time a different
path through the same binary while reporting the same cell label, and nothing in
either output would say so. So before any server is launched, the harness
extracts the request generator from `measure.sh`, executes it with the socket
layer replaced by a recorder, and REFUSES if the captured bytes differ from its
own. There is one definition of what a signed request is, it lives in
`measure.sh`, and this file holds a copy that is rejected if it disagrees.

### What the clock includes

Two `time.perf_counter_ns()` clocks per request:

| clock | starts | stops |
|---|---|---|
| `total` | immediately before `socket.create_connection` | the `recv` returning `b""` (server EOF) |
| `rt` | immediately before `sendall` | the same EOF |

**Neither includes `close()`.** The request carries `Connection: close`, so every
request is a fresh TCP connection and the connect cost cannot be amortised away;
reporting both clocks is what makes the size of that fixed cost visible rather
than buried inside a single number. Measured here it is 59–76 µs, rising slightly
with body size for a reason this document does not explain.

**Also inside every figure, because they are inside the program under test:** the
handler's `record_sink` append to the emit log, the loopback TCP stack, and the
Python client's own send and receive loop. **These are latencies of a delivery to
this process, not of the handler function.** No attempt was made to subtract the
harness, and a figure that did would be a different measurement.

### Percentiles

**Nearest-rank**, the `ceil(p·n)`-th smallest sample, 1-indexed. Stated because
it is a choice: with a few hundred samples the interpolating and nearest-rank
conventions differ visibly in the tail, and a percentile whose method is unnamed
is not reproducible.

**THE MEDIAN GOES THROUGH THE SAME RULE, and the rank is computed in exact
integer arithmetic from a ratio rather than a float.** Both halves of that
sentence were repairs, and external review found them:

- `statistics.median` AVERAGES the two central samples at even `n`, which is a
  different estimator from the one named here. Two estimators under one heading
  is how a summary quietly stops matching the method its document states.
- `ceil(p·n)` evaluated through a binary float rounds the wrong way for some
  `(p, n)`: `0.99 × 2099` is `2078.0099999999998`, and a truncating ceil lands on
  rank 2078 where the definition says 2079.

**Neither defect moved a figure in this document**, and that was checked rather
than assumed: recomputing all 44 runs from their raw samples under the corrected
rule reproduces every median and p95 printed here to the last digit. The worst
median discrepancy across the 44 runs is 0.021 µs against a reported precision of
0.1 µs, and at `n = 10,000` the float rank happened to equal the exact rank for
both p95 and p99. They were latent rather than manifest — which is exactly why
they are recorded: the same code at a different sample count would have been
wrong, and nothing in the output would have said so.

## The protocol

- **200 warmup requests, discarded; 10,000 timed.** The first requests pay page
  faults, allocator growth and first-touch on the request arena, so a median over
  a run including them is a median of two populations. Warmup requests are sent
  and validated exactly as the timed ones are; only their timings are dropped.
- **One fresh server process per cell**, on its own port, started and killed by
  the harness. Cells are never measured against a shared server.
- **The harness waits for THIS server to report its own bind line**, not for the
  port to answer. A connect probe is satisfied by a stale listener that already
  owns the port, and the run would then time one process while attributing the
  figures to another. The discipline is `measure.sh`'s, for the same reason.
- **An identical control cell (7,740 bytes at 22.1%) is run FIRST and LAST in
  every replicate.** Without both ends, a sequence that drifted and a sequence
  that did not are indistinguishable from the inside.
- **TIME_WAIT is drained below 4,000 before each cell**, capped at 40 s. One cell
  at 10,000 requests leaves ~10,200 entries, and macOS holds them 2·MSL = 30 s
  against a 16,384-port ephemeral range. Cells use different server ports so the
  4-tuples differ and reuse is permitted in principle; waiting is cheap and
  removes the question rather than reasoning about the allocator. Measured peak
  during a single cell was 10,208, and **there were zero connect failures across
  all 44 runs.**

### Workload validation — what proves the timed path is the claimed path

**Every request's status is checked, not one of them.** `202 Accepted` is what
certifies the handler ran to completion and the JSON scan actually executed. A
wrong signature is refused `401` *before* the scan, and the scan is the only
thing in this program that cares where a field sits — so at these percentiles a
handful of refusals would read as a fast tail rather than as an invalid run. Any
non-202 invalidates the run instead of being reported beside it.

**The emit log is counted from the other side.** It must hold exactly one line
per request — warmup included — and every line must carry the extracted
repository name, or the scan did not reach the key at the offset the cell is
labelled with. Across the 44 runs this is 448,800 lines checked against 448,800
requests.

Both refusals were fired deliberately rather than trusted:

- a request builder drifted by one header → `REFUSED: this harness's signed
  request differs from measure.sh's (8034 vs 8026 bytes)`
- a deliberately wrong signature → `MEASUREMENT INVALID: 4 request(s) did not
  answer 'HTTP/1.1 202 Accepted'; first was request 0 with 'HTTP/1.1 401
  Unauthorized'`

A check never observed failing is a hypothesis.

## Two residual races, recorded rather than closed

Review found two windows the harness does not close, both between adjacent
statements:

- `latency.py` arms its signal handlers before spawning the server, but a
  handled signal arriving between `Popen` returning and the child being
  registered would reap nothing.
- `campaign.sh` traps and forwards, but a signal arriving between backgrounding
  `latency.py` and recording its PID would see an empty PID.

Closing them properly means blocking signals across spawn-and-register in both.
**They are left open deliberately.** Each is a window of a few instructions in a
harness run by hand, and the consequence is an orphaned server on a port —
recoverable, and visible in the very probe this campaign runs before its first
cell. Every earlier finding in this review changed what the measurement could
CERTIFY; these change only how it behaves when killed at one precise instant.

Anyone automating this under a supervisor should close them first, because the
scenario stops being hypothetical once a timeout is doing the killing.

## Reproduction

```sh
# The store destination MUST NOT EXIST: `cp -R codebase <dir>` into an existing
# directory creates <dir>/codebase, leaving OATH_STORE pointed at whatever was
# already there -- a stale store that still builds, and mutates on put.
make build                     # `oath/oath` is gitignored; a clean checkout has none
w=$(mktemp -d); store=$(mktemp -d)/codebase
cp -R codebase "$store"
OATH_STORE="$store" ./oath/oath put apps/github-webhook/webhook.oath
OATH_STORE="$store" ./oath/oath build gh-webhook --backend llvm -o "$w/ghd"

# ONE COMPLETE BRACKETED REPLICATE. This generates the nine payloads, runs the
# control cell, the nine cells and the control cell again -- each on its own
# port, with TIME_WAIT drained between them -- checks the campaign's own
# invariants, and PRINTS THE BRACKET VERDICT. Repeat with different first-ports
# for further replicates.
sh docs/experiments/issue-115-timing/campaign.sh "$w/ghd" "$w/r2" 22101 r2
```

**THE CAMPAIGN IS A PROGRAM, NOT A PARAGRAPH.** `latency.py` measures one cell
correctly and knows nothing about the protocol the reported figures depend on —
bracketing, control identity, one fresh port per cell, the drain. Left in prose,
every one of those is an instruction a reproducer can follow imperfectly while
every check inside `latency.py` still passes, and the published rejection rule
would not be reproducible from this document. `campaign.sh` is that structure,
and it asserts what it claims rather than merely executing it: eleven runs, nine
distinct cell labels, 200/10,000 on every one, one emit line per request, and
**the two controls agreeing on the size and key offset the instrument measured
off their bodies** — identity by content, because a regenerated or repointed file
makes "the same control at both ends" false while both runs still answer 202.

It is written in POSIX `sh` deliberately. `zsh` does not word-split an unquoted
variable, so the obvious loop over sizes silently measures three cells where this
one measures nine — and the run looks entirely successful. That is not
hypothetical; it happened while this campaign was being built.

Every binary was built into a hermetic store copy so that `oath put` never wrote
to the committed store; `codebase/` and `fixtures/` were untouched throughout.
The generated payloads are build artifacts and `mksize.py` refuses to write them
anywhere inside the repository.

### The measured configuration

A latency figure is a fact about a binary AND the machine that ran it, so both
are pinned. Reproducing on a different row does not reproduce these numbers.

| | |
|---|---|
| source commit | `7c3d5a11c5d4d3feedb1674925676d45b7899c11` (tracked tree clean) |
| machine | Apple M4, 10 cores, 16 GiB |
| OS | macOS 26.5.2, build 25F84 (Darwin 25.5.0) |
| C toolchain | Apple clang 21.0.0 (clang-2100.1.1.101), arm64-apple-darwin25.5.0 |
| harness interpreter | Python 3.14.6 |
| backend | `--backend llvm` |

The clang version is load-bearing rather than incidental: `--backend llvm` emits
textual LLVM IR plus a C runtime and shells out to clang, so that compiler
produced the binary under test.

## The rejected replicate

Four replicates were run. **One was rejected on its own end controls**, before
any of its cell figures were looked at:

| replicate | control-A → control-B, median | p95 | verdict |
|---|---|---|---|
| r1 | −6.72% | −14.47% | **REJECT** |
| r2 | +0.05% | −0.52% | keep |
| r3 | +0.07% | +0.16% | keep |
| r4 | −0.97% | −0.48% | keep |

r1 ran while the machine's load average was still decaying from an unrelated
source (~5.0–5.4 through r1, ~2.0–3.5 through r2–r4), and its control-A was
elevated while its control-B was already normal — which is exactly the shape a
bracketed control exists to expose. A fourth replicate was then run so that three
kept replicates remained, rather than reporting a two-replicate spread.

**THE CONTROL WAS PREDECLARED; THE NUMERIC THRESHOLD WAS NOT, AND SAYING
OTHERWISE WOULD OVERSTATE THE PROCEDURE.** Running an identical cell at both ends
was fixed before any of these runs; the specific `|median drift| < 2% and |p95
drift| < 5%` cut was written after the four drift figures were in hand. What
makes the rejection defensible anyway is that the partition does not depend on
where the cut is placed: **any median-drift threshold between 0.97% and 6.72%, or
any p95-drift threshold between 0.52% and 14.47%, separates exactly r1 from
exactly r2–r4.** The kept replicates are two orders of magnitude tighter than the
rejected one, and an independent observation — the load average — points the same
way. A threshold chosen after the fact is worth less than one chosen before; it
is worth something when the result is invariant across the range it could have
been chosen from, and that is the claim being made here and not a stronger one.

## The grid

n = 10,000 timed per cell, 200 warmup discarded, one fresh server process per
cell. Figures are per replicate; `spread` is (max − min)/min across the three.

**median µs**

| cell | r2 | r3 | r4 | median | spread |
|---|---|---|---|---|---|
| control-A | 421.5 | 422.2 | 421.6 | 421.6 | 0.17% |
| 6,032 / 0.3% | 290.0 | 285.1 | 284.0 | 285.1 | 2.11% |
| 6,032 / 22.1% | 364.1 | 365.6 | 365.1 | 365.1 | 0.41% |
| 6,032 / 75.9% | 570.2 | 567.3 | 562.2 | 567.3 | 1.42% |
| 7,740 / 0.3% | 319.2 | 315.4 | 318.0 | 318.0 | 1.20% |
| 7,740 / 22.1% | 422.2 | 422.8 | 419.4 | 422.2 | 0.81% |
| 7,740 / 75.9% | 683.7 | 678.0 | 682.4 | 682.4 | 0.84% |
| 25,249 / 0.3% | 657.1 | 664.1 | 657.5 | 657.5 | 1.07% |
| 25,249 / 22.1% | 1030.5 | 1036.8 | 1024.2 | 1030.5 | 1.23% |
| 25,249 / 75.9% | 1906.6 | 1903.3 | 1899.7 | 1903.3 | 0.36% |
| control-B | 421.7 | 422.5 | 417.5 | 421.7 | 1.20% |

**p95 µs**

| cell | r2 | r3 | r4 | median | spread |
|---|---|---|---|---|---|
| control-A | 445.7 | 443.5 | 441.9 | 443.5 | 0.86% |
| 6,032 / 0.3% | 308.9 | 305.8 | 299.0 | 305.8 | 3.31% |
| 6,032 / 22.1% | 384.2 | 387.1 | 385.6 | 385.6 | 0.75% |
| 6,032 / 75.9% | 594.4 | 598.4 | 592.6 | 594.4 | 0.98% |
| 7,740 / 0.3% | 348.0 | 339.5 | 333.9 | 339.5 | 4.22% |
| 7,740 / 22.1% | 440.5 | 452.4 | 441.9 | 441.9 | 2.70% |
| 7,740 / 75.9% | 710.8 | 713.5 | 715.7 | 713.5 | 0.69% |
| 25,249 / 0.3% | 682.4 | 688.7 | 686.8 | 686.8 | 0.92% |
| 25,249 / 22.1% | 1060.6 | 1083.2 | 1060.5 | 1060.6 | 2.14% |
| 25,249 / 75.9% | 1974.0 | 1967.7 | 1972.2 | 1972.2 | 0.32% |
| control-B | 443.4 | 444.2 | 439.8 | 443.4 | 1.00% |

**Consolidated, median of the three kept replicates.**

| body bytes | `repository` at | of body | median µs | p95 µs | min µs | µs per body byte |
|---|---|---|---|---|---|---|
| 6,032 | 18 | 0.3% | 285.1 | 305.8 | 237.8 | 0.0473 |
| 6,032 | 1,333 | 22.1% | 365.1 | 385.6 | 316.4 | 0.0605 |
| 6,032 | 4,578 | 75.9% | 567.3 | 594.4 | 503.5 | 0.0940 |
| 7,740 | 23 | 0.3% | 318.0 | 339.5 | 272.9 | 0.0411 |
| 7,740 | 1,711 | 22.1% | 422.2 | 441.9 | 367.7 | 0.0545 |
| 7,740 | 5,875 | 75.9% | 682.4 | 713.5 | 614.5 | 0.0882 |
| 25,249 | 76 | 0.3% | 657.5 | 686.8 | 586.9 | 0.0260 |
| 25,249 | 5,580 | 22.1% | 1030.5 | 1060.6 | 950.2 | 0.0408 |
| 25,249 | 19,164 | 75.9% | 1903.3 | 1972.2 | 1778.7 | 0.0754 |

**No p99 column is published.** The instability finding below is the reason: a
figure the text goes on to describe as not reportable does not belong in a table,
because a table is where a later reader takes a number from without reading the
paragraph that qualifies it.

**At the median observed size and the median observed position, one signed
delivery takes 422 µs at the median and 442 µs at the 95th percentile.** Across
the whole grid the worst cell is **1.903 ms median and 1.972 ms p95.**

The shape is the memory grid's shape. Position at fixed size costs
1.00/1.28/1.99× (6,032), 1.00/1.33/2.15× (7,740), 1.00/1.57/2.89× (25,249); size
at fixed position costs 1.00/1.12/2.31× (0.3%), 1.00/1.16/2.82× (22.1%),
1.00/1.20/3.36× (75.9%). These reproduce an earlier 1,000-request campaign to
within 1%, which is the cross-check that two independent campaigns agree.

## Why the sample count is 10,000 and not 1,000

An earlier campaign used 1,000 timed requests per cell with the same brackets and
the same three-replicate structure. **All three of its replicates passed their
end controls, and its p95 still spread up to 110.6% across replicates** — the
same cell reporting 671/400/398 µs and 464/471/945 µs. Its medians were fine
(≤4.0%).

That is the useful negative result, and it is why this document reports the
sample count as part of the protocol rather than as an implementation detail:

> **A bracketed control detects DRIFT. It does not detect a percentile that is
> unstable because the sample is too small — the run is honestly reporting a p95
> it does not have enough tail to estimate, and both ends agree about it.**

At 10,000 the worst p95 spread over the kept replicates is 4.22%. Cutting every
one of the 33 kept runs into ten consecutive blocks of 1,000 gives block medians
varying **≤4.36%** with no trend from first block to last, so within a kept run
the variation is scatter rather than drift and 10,000 back-to-back connections
neither degrade the server nor exhaust the loopback. Block p95s range up to
43.23%, which is the same tail sensitivity the p99 finding below describes.

**WITHIN-RUN STABILITY IS A PROPERTY OF A RUN, NOT A GUARANTEE OF THE
INSTRUMENT — and a later replicate demonstrated the failure directly.** Running
`campaign.sh` again after the tables above were fixed produced a control cell
whose block medians sat at ~425 µs for the first four blocks and ~355 µs for the
remaining six: a persistent 20.3% step partway through one run, not scatter.
**Its bracket rejected the replicate** (+16.77% median drift), which is the
mechanism working. Two details are worth carrying:

- The step is invisible in that run's p95 — the two controls' p95s differ by
  0.50% while their medians differ by 16.77%, because the faster regime's samples
  fall below the 95th percentile of the slower one. **A drift check on the tail
  alone would have passed this replicate.** Both statistics are bracketed here
  for that reason.
- **Six replicates have now been run on this machine and two were rejected.**
  The bracket is not a formality that always passes; on a normally-busy desktop
  it fires often enough that a campaign without one would have published a
  mixture sooner or later.

**AND 4.22% IS THE SPREAD THIS CAMPAIGN OBSERVED, NOT A BOUND ON THE
STATISTIC.** A later bracket-passing replicate, run with `campaign.sh` after the
tables above were fixed, reproduced **every median within 1.3%** — which is the
independent confirmation that the committed driver reproduces the committed
figures — while putting ONE cell's p95 (25,249 at 0.3%) **16.5% above** the
campaign value, with that same cell's median within 1.2%. The body of the
distribution was undisturbed and only the tail moved, which is what an
occasional external transient looks like.

So the honest ordering is: **the median is solid, the p95 is usable and
occasionally excursive, the p99 is not reportable.** A single number for "the p95
spread" describes the runs it was computed from, and the distinction between that
and a property of the measurement is the same one this repository insists on
between a corpus figure and a claim about programs in general.

**p99 did NOT stabilise**, spreading up to 17.8% across the kept replicates
(6,032/0.3% reported 332.1/376.3/319.5 µs), and p99.9 is worse. **Only the median
and the p95 are reportable at the few-percent level from this data**, which is
why no p99 appears in any table here. The instrument records every raw timing, so
a p99 can be recovered by anyone who re-runs it and is willing to state the
uncertainty; what is declined is publishing one as though it were as solid as the
two columns beside it.

## What is measured and what is not

**Measured: total warm steady-state latency of a complete signed delivery**, per
cell, on a single-process server driven serially, against an identical control at
both ends of every replicate.

**NOT measured: where the time goes.** No receipt/scan/crypto split was taken in
time. `issue-115-premise.md` decomposed a 409,600-byte run in BYTES and
`issue-115-composition.md` produced the allocation census; **neither is a time
attribution, and the 99.79% figure must not be read as one.** It says what the
allocations are, which makes it *plausible* that the same representation
dominates the time — the two grids do move together, at 9.6–11.3 published peak
KiB per µs across all nine cells — but plausible attribution is not measured
attribution, and one arithmetic step over two tables is exactly the kind of
near-linearity that `issue-115-sizes.md` already withdrew a claim for.

**NOT measured: throughput.** The server is a single-process accept loop and was
driven by one serial client; no concurrency was applied. Inverting a per-request
latency yields a number in requests per second, and that number is **not a
throughput measurement** — it is the reciprocal of a latency, blind to whatever
a concurrent load would do to the arena, the accept loop and the scheduler.
Establishing a throughput figure needs a load test, and none was run.

**NOT attempted: a prototype.** No representation change was built, applied or
measured. This is deliberate and is the direct consequence of the result rather
than a gap in it. The exercise carried a **stop condition declared before the
grid was run: if a complete delivery landed in single-digit milliseconds, no
prototype would be built**, because the question is whether the time cost is
large enough to justify the compiler programme #115 describes and a cost that
small cannot be. Every cell landed under 2 ms, so the condition fired and the
work stopped there. Building a prototype to speed up a 422 µs median would be
answering a question the measurement has already closed — and it would produce
exactly the reassuring artifact that makes a decline hard to state plainly.

## Recommendation: DECLINE #115 as scoped, on the time axis

#115 proposes a typed IR, monomorphisation, MLIR/LLVM, layout and calling
convention, optimization and debug info — a project its own text calls "many
sessions", motivated by "what it does not do is run fast" and never by a number.

**This is the number.** At real payload sizes and real field positions, a
complete signed webhook delivery — receipt, HMAC-SHA256 over the whole body, the
JSON scan, the handler and the emit — costs **422 µs median and 442 µs p95 at
the median cell**, and at the grid's worst cell **1.903 ms median and 1.972 ms
p95**.

**THOSE ARE THE CAMPAIGN'S CONSOLIDATED STATISTICS, NOT BOUNDS ON A DELIVERY**,
and the distinction is not pedantry here because this document records a
counterexample to reading them as bounds: a later replicate put one cell's p95
20.3% above that cell's median, where every cell in the campaign sat within
7.3%. Individual deliveries exceed these figures by a lot — across the 44 runs
the slowest single request was 5× to 44× its own run's median — and nothing
measured here bounds a single request. What the
figures support is a claim about the CENTRAL BEHAVIOUR of this handler at these
payloads, which is the claim the recommendation rests on.

It is enough for that claim, because the margin is wide rather than marginal:
the stop condition was single-digit milliseconds, the median cell is roughly an
order of magnitude below it, and even the grid's worst cell clears it. **#115 and these
experiments identify no deployed consumer and no latency budget these figures
fail** — a statement about what this record contains, not a survey of everything
that might exist.

`issue-115-sizes.md` reached DECLINE AS SCOPED on memory and named the two
backend-local representation changes as the proportionate response instead. This
document does not add a second recommendation; it removes the remaining argument
for the first one being wrong. **Both axes the issue is motivated by have now
been measured on the same grid, and neither supports the programme as scoped.**
Declining it does not discard the two named changes, which remain what
`issue-115-composition.md` argued for on evidence of their own.

## What would overturn this

- **A deployment whose CONCURRENCY makes the per-request cost material.** This is
  the live reopening condition and it is not closed by anything above: every
  figure here is serial. A receiver's standing cost is a per-request transient
  times its concurrency, and nothing in this repository characterises a
  concurrency. **A load test showing the arena, the accept loop or the scheduler
  behaving unlike the serial case would reopen the question on evidence this
  document does not have.**
- **A consumer with a stated latency budget these figures fail.** None exists;
  one would change the argument immediately, because "under 2 ms" is only an
  answer relative to a requirement.
- **Payloads outside the sampled range.** A `push` carrying hundreds of commits
  exceeds anything measured here; GitHub caps that array at 2,048 entries, and
  nobody has measured at that bound. The 75.9% column grows fastest with size, so
  the extrapolation that matters is the one least supported.
- **A time attribution that contradicts the allocation census.** If the time
  turns out NOT to be dominated by the representation the census names, the two
  named changes would not move these numbers, and the composition document's
  direction would need re-deriving on a time instrument rather than on a byte
  one.

## Limitations of this measurement

- **The machine was a normally-busy desktop, not a quiet one.** The three kept
  replicates ran at load average 2.0–3.5 with a user application consuming
  roughly one full core throughout. The end controls establish that the sequence
  did not drift and the replicates establish that the figures reproduce; neither
  establishes what a quiet machine would give. **That interference most likely
  inflated these runs rather than deflating them — but a single machine cannot
  bound any other environment, in either direction.** Slower hardware, a
  different scheduler or a loaded host could exceed these figures by an amount
  nothing here constrains, and "probably not faster than this here" is not
  "no slower than this anywhere".
- **One machine, one OS, one backend**, all pinned under "the measured
  configuration" above. Nothing here characterises the Go backend, and per
  CLAUDE.md the two backends are under no obligation to match each other — only
  to match `oath eval`.
- **The harness is inside the figure.** Loopback TCP, a Python client and the
  emit-log append are all counted. A handler-only figure would be smaller by an
  unmeasured amount.
- **The 44 runs' raw samples are not committed.** The instrument emits every
  timing, and re-running it is the way to obtain them; this document records the
  protocol precisely enough to do so, and no summary here can be audited against
  data that was not kept.
