# #115 — what the boxed body representation actually costs

**Status: MEASURED, AND HELD.** The receipt cost was confirmed and quantified.
The mechanism #115 proposes for recovering it was not tested, and the larger
cost this exercise uncovered was measured but not addressed. **The
recommendation at the end is HOLD, not proceed.**

Nothing here is normative. No SPEC text is proposed and no wire format changes.

**THE PROTOTYPE NO LONGER EXISTS, AND THAT IS DELIBERATE.** It was a temporary
modification to `oath/llvm.go`, made to take one measurement and reverted once
the numbers below were recorded. It was never committed and no patch was kept:
while it was applied the LLVM backend exited 70 on any handler that read a
request body, so it was never a candidate for merge in any form, and keeping a
copy would have invited someone to treat it as one. The measurements it produced
are the deliverable; the code was scaffolding. This document therefore describes
it in the past tense throughout, and the description below is deliberately
detailed enough to rebuild it from scratch if that is ever wanted.

## The brief this exercise started from was wrong in two places

Both corrections came out of reproducing a baseline rather than assuming one,
and the second is worth more than the measurement the exercise was sent to take.

**THE PATH WAS WRONG.** The brief said the request body reaches `hmac-sha256`
through `str-bytes`. It does not. `gh-webhook` reads

```
(hmac-sha256 (str-bytes secret) (req-body r))
```

so `(req-body r)` is handed to `hmac-sha256` **directly**, and the only thing
crossing `str-bytes` is the 32-character secret. Any reasoning that put the body
through the `Str` boundary was reasoning about a path this program does not
take.

**THE HEADLINE FIGURE WAS MEASURING FIELD POSITION, NOT CRYPTO.** The 368 MiB
attributed to the signed path is a real measurement of a real run, and it is not
a fact about the crypto path. Re-measured here on the unmodified baseline, with
a *correct* HMAC so the handler runs to completion (four runs, peak RSS,
409,600-byte body, secret `0123456789abcdef0123456789abcdef`):

| signed delivery, baseline | peak RSS (4 runs) | over idle | status |
|---|---|---|---|
| `repository` **first** in the body | 71,280–71,296 KiB | 69,828 KiB | 202 |
| `repository` **last** in the body | 376,656–376,672 KiB | 375,204 KiB | 202 |

**5.4× apart, from moving one field.** The bodies are the same length, the
signature is verified in both, and HMAC-SHA256 runs over all 409,600 octets
either way. A single number taken from one payload therefore cannot be
attributed to "the crypto path" at all, and the 368 MiB quoted on #165 and #115
is the `repository`-last case of this table. The figure appears in no committed
document, so nothing under `docs/` required editing. **For the current state of
those issue comments, read #165 and #115** — they are the authority for their
own text, and a status recorded here would be correct exactly once.

**THE PROBE HAD TO BE SIGNED CORRECTLY OR IT WOULD HAVE SHOWN NOTHING.** A wrong
signature is refused 401 *before* the JSON scan, and the scan is the only thing
in this program that cares where a field sits. An earlier version of this probe
used a deliberately bad signature, reproduced no order-dependence whatsoever,
and would have read as a refutation. The `202` in both rows is what certifies
the handler ran to the end; any other status means an earlier refusal was
measured instead.

## The claim that survives, and the universe it quantifies over

Stripping the two errors leaves a question the signed path cannot answer, because
on the signed path receipt, crypto and scan are summed into one number and the
scan dominates by a factor that depends on the payload.

> **What does the boxed `(List Int)` representation cost to simply RECEIVE a
> body, before anything looks inside it?**

The **unsigned 401** isolates exactly that: a 409,600-byte body is read off the
socket and built into a value, `gh-signature` finds no `x-hub-signature-256`
header, and the handler answers 401 without any consumer touching the body. No
handler logic, no JSON scan, no crypto — and no field-order dependence, because
nothing has looked inside.

## Baseline: the receipt cost, and order-independence

Peak RSS of the whole server process via `/usr/bin/time -l`. The server is a
single-process accept loop that releases its request arena after serialising
each answer, so a process maximum is the right instrument and a sampler would
race the release. Five repetitions per cell; the idle control sends no request
at all, and without it a peak figure has nothing to subtract from.

| baseline (LLVM) | idle | `repository` first | `repository` last |
|---|---|---|---|
| mean | 1,456 KiB | 70,406.4 KiB | 70,403.2 KiB |
| min–max | 1,456–1,456 | 70,368–70,416 | 70,368–70,416 |

**Receipt cost = 68,950 KiB ≈ 67.3 MiB.**

**ORDER-INDEPENDENCE CONFIRMED, and it is the precondition for trusting the rest
of this document.** The two orderings sit **3.2 KiB** apart on a ~69,000 KiB
delta, while the spread *within* each ordering is 48 KiB — the difference between
field positions is smaller than the run-to-run noise. Set against the 5.4× spread
on the signed path in the same binary, this is not a weak effect but an absent
one, which is what makes a single unsigned number a legitimate figure to quote.

## The prototype, as it was

A tag `T_BYTES` was added to the runtime value union, and `o_http_value` built
the body as length + a pointer aliasing the octets already contiguous in the
request arena, instead of the cons-list loop. One `OVal` where the boxed form
allocated four objects per octet — an `o_int`, its magnitude, a two-pointer
field array and a `Cons`. O(1) in body length; it copied nothing.

| prototype (LLVM) | idle | `repository` first | `repository` last |
|---|---|---|---|
| mean | 1,456 KiB | 2,867.2 KiB | 2,867.2 KiB |
| min–max | 1,456–1,456 | 2,864–2,880 | 2,864–2,880 |

**Receipt cost = 1,411 KiB ≈ 1.38 MiB.**

> **68,950 KiB → 1,411 KiB: a 48.9× reduction, removing 98.0% of the cost of
> receiving a body.**

The residual is the 400 KiB body buffer plus the `OBuf` doubling that produced
it, which is what a packed representation should cost and is close enough to it
to need no further explanation.

**NO STUBBING WAS REQUIRED**, and that fact carries less information than it
appears to. The program compiled and linked unchanged — but this is a runtime
representation swap below the type system, so "it compiles" says nothing about
whether any consumer still works. It does not. See below.

## The control that fired, and the first prototype it condemned

The measurement claims the unsigned path never looks inside the body. A drop in
peak RSS cannot distinguish *never needed the body* from *silently skipped work
it should have done*, so the same binary was driven down a path that **does**
consume the body.

**THE FIRST CUT OF THIS PROTOTYPE WAS SILENTLY BROKEN AND THE STATUS CODE HID
IT.** It guarded `o_idx` and `o_field` — the match protocol — which was the set
of consumers that came to mind rather than the set the claim quantifies over.
`o_byte_list_len` tests `cur->tag == T_CTOR` **directly**, so a packed body
reaching it did not abort and did not match: it read as an **empty list**.
`hmac-sha256` then ran over zero octets, mismatched, and answered `401` —
byte-identical to the legitimate refusal, from a server that had computed
nothing. Had the RSS number been taken on the strength of "the 401 came back",
it would have been a real-looking figure produced in part by a broken server.

A third guard was added at `o_byte_list_len`. **STATED NARROWLY, BECAUSE THE
TEMPTING WIDER CLAIM IS FALSE:** that is the single entry for the runtime's
**crypto byte conversion** — `o_byte_list` calls it before its own walk, and
`hmac-sha256` and `bytes-eq-ct` reach octets only through `o_byte_list`. It is
**not** a chokepoint for every structural consumer of the body. The JSON scan
does not go through it at all; `json-scoped-string` walks the list through the
compiled match protocol, and what catches that is the `o_idx`/`o_field` pair.

So the prototype needed guards at **two unrelated boundaries**, and neither was
sufficient alone. There was no single structural owner of "reads the body as
octets" to guard, which is itself part of what this exercise found.

With all three guards in place. **The second row of each pair is TAMPERED, not
signed** — an `x-hub-signature-256` header carrying a well-formed but wrong
digest, which is what forces HMAC over the body while still ending in a
refusal. Calling it "signed" would make the baseline's `401` read as a
contradiction; the correctly-signed deliveries are the `202` runs in the table
further up:

```
base  unsigned                -> 401  alive
base  tampered (sig present)  -> 401  alive
proto unsigned                -> 401  alive     <- body genuinely never touched
proto tampered (sig present)  -> DIED  "#115 prototype: packed body was read as octets"
```

That pair is what licenses the headline number: the unsigned path demonstrably
does not touch the body, and a signature-present path demonstrably does. The Go
backend was rebuilt and driven down both paths as a further control and was
unaffected, confirming the change was LLVM-only.

**AND THE REVERT WAS VERIFIED THE SAME WAY, NOT ASSUMED.** After `oath/llvm.go`
was restored and the kernel rebuilt, a correctly-signed delivery answered 202 at
71,280 KiB — the pre-prototype baseline figure to the kilobyte — and a tampered
one answered 401 with the server alive. A textual revert is not evidence that
the backend works again.

**AND THE HOLE IS A RESULT, NOT JUST AN INCIDENT.** It says something about the
real design that the compile did not: **not every octet consumer goes through
the match protocol.** A genuine packed representation must adapt the direct
traversals too, not only `Cons`/`Nil` matching, and those traversals fail
*silently* — as an empty list — rather than loudly. That is a cost of the
mechanism which no measurement of receipt would have surfaced.

## End control

Taken separately by the advisor, on the same question. **ON THIS MACHINE, AT A
LATER TIME** — so it is a repetition under different load, not an independent
platform, and it does not witness anything about portability. It is recorded
because the load conditions were captured alongside it, which is what makes the
agreement below interpretable rather than merely reassuring:

- 38 GiB free; no `z3` competing for memory; two parented `oath serve`
  processes present, both at low RSS.
- `repository`-first: 1,488 → 70,416 KiB, 401.
- `repository`-last: 1,472 → 70,368 KiB, 401.

Both land inside the min–max ranges measured above (70,368–70,416), from
slightly different idle floors. **The claim that supports is bounded:** the
receipt cost and its order-independence are stable across time and load on this
host. Whether they hold on another platform is untested — the arena arithmetic
underneath them depends on `sizeof(OVal)` and the target's fundamental
alignment, so a different target would need its own measurement.

## Recommendation: HOLD

Narrow, and each clause is bounded by what was actually run.

**CONFIRMED.** The boxed `(List Int)` representation is the cost of receiving a
body: 68,950 KiB for 400 KB, removable to 1,411 KiB. #115's premise is correct
*about receipt*. This is a decidable, quantitative result and it is not in doubt.

**NOT PROVEN: TYPED IR AS THE MECHANISM.** The prototype was a hand-written C
representation swap in the LLVM runtime. It was not monomorphisation, it was not
a typed IR, and it adapted not one consumer. What it established is **one
measured point under the current buffering architecture** — and, specifically,
**NOT A BOUND IN EITHER DIRECTION.** The 1,411 KiB residual is mostly the `OBuf`
doubling and the 400 KiB body buffer that produced it, neither of which this
exercise showed to be unavoidable: a mechanism that streamed the body or skipped
that intermediate copy could land *below* it, recovering more than the 67,539
KiB measured here. So this is neither a floor on the residual nor a ceiling on
the saving. An earlier draft of this document called it a ceiling, which was a
property of this prototype reported as a property of the problem. The two guard
boundaries it needed are direct evidence that the consumer-adaptation half is
where the difficulty lives, and this exercise did none of it.

**MEASURED BUT UNTESTED: THE STRUCTURAL SCAN, WHICH IS THE LARGER COST.** The
signed table above prices it: with `repository` late, the run costs 375,204 KiB
against 69,828 KiB with it early — **305,376 KiB ≈ 298 MiB attributable to field
position alone, 4.4× the entire receipt cost measured above.**
**This receipt-only prototype did not test the scan.** It packed the body at
construction and left every consumer alone, so it says nothing either way about
what the scan would cost against a packed representation. `json-scoped-string`
walks a structural list, so a real change would have to materialise the body or
rewrite the scan — and either could move this figure **in either direction**,
which is precisely what was not measured. **Do not fold this into the receipt
result.** It is a separate question, plausibly the more important one, and
nothing here has tested a fix for it.

**HOW MUCH RECEIPT COST MATTERS IS A PROPERTY OF THE PAYLOAD, NOT OF THE
PROGRAM**, and the two measured payloads bracket it about as widely as a single
program can:

| signed delivery | receipt share of the run |
|---|---|
| `repository` first | 68,950 of 69,828 KiB — **98.7%** |
| `repository` last | 68,950 of 375,204 KiB — **18.4%** |

Same handler, same body length, same crypto; only the field position differs. So
"receipt is where the memory goes" and "receipt is a rounding error" are both
true here, of different payloads, and neither generalises to *deliveries* as a
class — nothing in this exercise sampled what real GitHub payloads look like, and
`examples/`-style reasoning about a corpus would not help, because the relevant
population is one nobody here has characterised.

The honest summary is that #115's premise survives on receipt, that the share of
a delivery's memory receipt accounts for ranges from ~99% to ~18% across the two
payloads measured, and that the mechanism remains unevaluated. That is three
different states, and collapsing them into "#115 is confirmed" would be the
overclaim this project keeps having to correct.

## Reproducing

**THE INSTRUMENT IS COMMITTED; THE PROTOTYPE IS NOT. That asymmetry decides
which figures here are auditable, and it is stated rather than left to be
discovered.**

`issue-115-premise/measure.sh` takes one peak-RSS reading in one of four modes
(`idle`, `unsigned`, `tampered`, `signed`), and `issue-115-premise/mkpayloads.py`
regenerates both bodies — byte-identical to the ones measured, with the
equal-length assertion as its control, since receipt cost is linear in body
length and unequal payloads would differ for a reason unrelated to field
position.

```sh
# The store destination MUST NOT EXIST: `cp -R codebase <dir>` into an existing
# directory creates <dir>/codebase, leaving OATH_STORE pointed at whatever was
# already there -- a stale store that still builds, and mutates on put.
make build                     # `oath/oath` is gitignored; a clean checkout has none
w=$(mktemp -d); store=$(mktemp -d)/codebase
python3 docs/experiments/issue-115-premise/mkpayloads.py "$w"
cp -R codebase "$store"
OATH_STORE="$store" ./oath/oath put apps/github-webhook/webhook.oath
OATH_STORE="$store" ./oath/oath build gh-webhook --backend llvm -o "$w/ghd"
docs/experiments/issue-115-premise/measure.sh "$w/ghd" "$w/run" 21100 signed "$w/repo-last.json"
```

Every binary was built into a hermetic store copy so that `oath put` never wrote
to the committed store; `codebase/` and `fixtures/` were untouched throughout.
The harness waits for **this** server to report its own bind rather than for the
port to answer — a connect probe is satisfied by a stale listener that already
owns the port, which would send the request to one process while reading RSS
from another and yield a valid-looking measurement of two unrelated things. It
also checks the server is still alive before reading the instrument, and cleans
up both the timer and its child on every exit path so a failed run cannot leave
an orphan that binds later and contaminates the next one. Each of those checks
is there because the corresponding failure happened: an early version killed
`/usr/bin/time` instead of the server and reported nothing at all.

**WHAT THIS REPRODUCES, AND WHAT IT CANNOT.**

| figure | reproducible from this repository? |
|---|---|
| idle, unsigned receipt cost, order-independence | **yes** — clean source + the harness |
| signed order-dependence, the 5.4× and the 368 MiB correction | **yes** — same |
| the prototype's 1,411 KiB, the 48.9× and 98.0% | **no** — the producing code was deliberately discarded |
| the abort control | **no** — same |

The load-bearing correction in this document — that a single signed number
measures field position rather than the crypto path — falls entirely in the
reproducible half, and was re-derived from clean source with the committed
harness after the prototype was reverted. **The prototype rows rest on this
record alone.** That is the accepted cost of not landing throwaway code, and
anyone who needs them audited must rebuild the prototype from the description
above and re-measure.

**REBUILDING THE PROTOTYPE MEANS REWRITING IT.** No patch was kept, so anyone
repeating this starts from the description in "the prototype, as it was": a
`T_BYTES` tag on the runtime value union, `o_http_value` building the body as
length + an alias into the request arena instead of the cons-list loop, and
abort guards at both `o_idx`/`o_field` and `o_byte_list_len`. Rewriting it is a
feature of this record rather than a cost of it — the guards are the part that
is easy to get wrong, and getting them wrong is silent.
