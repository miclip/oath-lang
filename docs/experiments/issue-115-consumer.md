# #115 — what a packed body costs the CONSUMER

**Status: MEASURED. The classification is HURTS THE SCAN, and the
recommendation is STILL UNRANKED.** The packed representation removes 97.9% of
receipt cost, as the premise exercise established. It makes the structural scan
**11.6% more expensive per octet scanned**. The two effects push in OPPOSITE
directions, which is precisely the case in which the payload-distribution
question stays decision-relevant — so this exercise did not unblock #115, and
saying which way it failed to is the result.

Nothing here is normative. No SPEC text is proposed and no wire format changes.

**THE PROTOTYPE NO LONGER EXISTS.** It was a temporary modification to
`oath/llvm.go`, LLVM-only, made to take these measurements and reverted once
they were recorded. It was never committed. Read the following four sentences
before any number below, because every one of them bounds what the numbers mean:

- **IT WAS THROWAWAY SCAFFOLDING, NOT A LANDING.** No part of it is proposed for
  merge, in any form.
- **IT WAS NOT A TYPED IR AND NOT MONOMORPHISATION.** It is a hand-written C
  representation swap in the LLVM runtime, below the type system. #115's actual
  mechanism remains unevaluated; what was evaluated is whether a packed body can
  be consumed at all, and at what cost.
- **IT IS FULLY REVERTED, AND THE REVERT IS PROVEN RATHER THAN ASSERTED** — see
  "Restoration" below, which reproduces the original baseline binary
  byte-for-byte.
- **IT TOUCHED NOTHING BUT `oath/llvm.go`.** Not the type checker, not the
  application source, not the committed harness, not `codebase/` or `fixtures/`.

The predecessor is `issue-115-premise.md`. It measured RECEIPT and said in its
own recommendation that the consumer-adaptation half "is where the difficulty
lives" and that it "did none of it". This is that half.

## The baseline was reproduced first, and it agreed

Reproduced from clean source with the committed harness
(`issue-115-premise/measure.sh`, `mkpayloads.py`) before anything was modified,
into a hermetic store copy so `oath put` never wrote to the committed store.

| signed delivery | premise document | reproduced here | agreement |
|---|---|---|---|
| `repository` **first**, over idle | 69,828 KiB | 69,827.2 KiB | −0.001% |
| `repository` **last**, over idle | 375,204 KiB | 375,213.3 KiB | +0.0025% |

Receipt (unsigned 401) reproduced at 68,944 KiB against the document's 68,950,
and its order-independence held (5.3 KiB apart across the two field positions).
**No disagreement was found, so nothing was re-derived on the strength of one.**

Idle floors are **per binary, not per session**, and this is not bookkeeping
pedantry: the prototype idles 16 KiB above the baseline because of its own
static table, so subtracting a single shared idle would have credited the
prototype with a saving it did not make. Every figure below is over ITS OWN
binary's idle.

## The prototype, as it was

A `T_BYTES` tag on the runtime value union. The request body became ONE `OVal`
carrying a pointer into the request arena plus a length, where the boxed form
allocated four objects per octet — an `o_int`, its magnitude, a two-pointer
field array and a `Cons`. O(1) in body length; it copied nothing.

`idx` and `n` are set on the cursor so that `o_idx` and every arity check
already in the runtime read it as a `Cons` or a `Nil` **without knowing it is
packed**. That is what let the match protocol work at all.

**THREE CONSUMER BOUNDARIES, REACHED THROUGH FOUR HELPER SITES.** The count is
the finding, not the mechanism — there is no single structural owner of "reads
the body" to adapt. The premise exercise found two boundaries; this one found a
third:

1. **The match protocol — one site, `o_field`.** It exposes the cursor lazily:
   field 0 is a byte `Int`, field 1 is a cursor over the tail, memoised in the
   unused `f` slot. This is the boundary `json-scoped-string` walks.
2. **The crypto byte conversion — two sites, `o_byte_list_len` and
   `o_byte_list`.** A wholly packed list is returned BY ALIAS: it is already the
   contiguous octets the function exists to produce, so nothing is copied and
   the conversion is O(1). **This is the branch the measurements exercise** —
   `gh-webhook` calls `(hmac-sha256 (str-bytes secret) (req-body r))`, so
   `o_byte_list` sees a wholly-`Cons` secret and a wholly-packed body, and
   nothing else.
   A `Cons` PREFIX followed by a packed TAIL is also handled — counted and
   copied — but **that branch is UNEXERCISED and must not be reported
   otherwise.** The general representation permits the shape; this program does
   not produce it at this boundary. The suffix `bytes-after` returns is itself
   wholly packed and flows only through matching and `bytes-str`, never reaching
   `o_byte_list`. So the mixed case is defensive code with no probe behind it,
   and an earlier draft of this document wrongly called it a shape the program
   "really reaches".
3. **Response serialisation — one site, the body walk in `o_http_response`** —
   **a boundary the premise document never named.** A handler echoing any body
   suffix into its response hands a cursor here. This program does not take that
   path, so it was adapted rather than measured. Without the adaptation the walk
   would have stopped at the first non-`Cons` and the terminator check would
   have answered 500 — loud, but only because that check happens to exist. The
   same omission at `o_byte_list_len` was SILENT, which is the asymmetry worth
   carrying.

Supporting the first of those, and **not itself a consumer boundary**: the 256
byte `Int`s are INTERNED, which is what removes the per-head `o_int` allocation
that the boxed form paid once per octet. Interning is sound because values are
immutable. They sit in STATIC storage for a reason that is worth stating exactly,
because the obvious reason is wrong: it is **not** that a consumer would
otherwise see a dangling head — `o_arena_release` runs strictly AFTER response
serialisation, so nothing within a request outlives the arena. It is that the
table is built ONCE and reused ACROSS requests; in the arena it would be freed at
each request's end while the initialised flag still claimed it was built, and the
next request would read freed memory.

**THE MEMO WAS KEPT FOR A REASON THAT MEASUREMENT DID NOT SUPPORT.** The
argument for it: `bytes-after` calls `bytes-prefix` at every position, so each
tail is demanded repeatedly and a fresh cursor per demand would turn an O(n·m)
pointer walk into O(n·m) allocations. Compiling the memo out costs **365,232
KiB** against **342,692** — worse, but 6.2%, nowhere near the blow-up predicted,
because `bytes-prefix` almost always fails on its FIRST byte and so demands each
tail about once anyway. Recorded because it was the reason the memo was written
and it was wrong.

## Behaviour controls, taken BEFORE any RSS figure

A faster wrong answer measures nothing, and a scan returning the wrong field is
exactly the failure this change could cause.

| | baseline | prototype |
|---|---|---|
| signed, `repository` first | 202, `miclip/oath-lang` | 202, `miclip/oath-lang` |
| signed, `repository` last | 202, `miclip/oath-lang` | 202, `miclip/oath-lang` |
| tampered (well-formed wrong digest) | 401 | 401 |
| unsigned | 401 | 401 |

The `202` is what certifies the handler ran to the end, and it is the control
that would have caught the premise exercise's silent failure: a packed body read
as an empty list HMACs over zero octets and answers `401` byte-identically to
the legitimate refusal. `go test ./oath -run LLVM` passes with the prototype in
place, which exercises runtime paths this webhook does not.

### The HMAC was confirmed separately, with a PINNED digest

The harness's `signed` mode computes the digest itself, so a 202 from it proves
the handler agrees with the harness — **not** that the digest is the one that
was accepted before the representation changed. So the same signature was sent
as a hardcoded hex to both binaries:

```
repo-first.json  hmac-sha256 = b53714ced6ca14141fa46681ec4c43bdb5b9ebac51304e6ab465aacaafca3694
repo-last.json   hmac-sha256 = b8f540636fd0bc911f836361324eea443f5d0dc315556eedd743f361434f37d5

  baseline  first / last, pinned digest      -> 202 / 202
  prototype first / last, pinned digest      -> 202 / 202
  either,   deliberately wrong digest        -> 401      (the probe discriminates)
  prototype last, the FIRST payload's digest -> 401      (the HMAC covers the octets)
```

The last two rows are why the first two mean anything: without them a probe that
accepted everything would look identical.

### Binary identity is NOT the manifest digest

Three hashes were being conflated, and only one of them answers "are these
different artifacts":

| | baseline | prototype |
|---|---|---|
| **SHA-256 of the binary** | `6011500ca7ff97ed85233650baf0b8d126d9fb6fc3a5b704bffcc897567f700f` | `57715686790e9af40a8464d5c8e9c14c8cc55d80c19668a15332e3d4706428f0` |
| `entry_hash` (Oath definition identity) | `552ec5bd…3711` | `552ec5bd…3711` |
| embedded manifest digest | `3db05052…d12b` | `3db05052…d12b` |

The binaries differ. The manifest digest is **identical, and correctly so**: it
is `sha256` of the CANONICAL manifest JSON (`ProvenanceManifest.digest`), which
records Oath-level identity, and the definitions did not change. Note that
hashing the OUTPUT of `oath provenance` does not reproduce it — that is an
indented rendering, not the canonical bytes — so a reader checking this figure
by piping the CLI into `shasum` will get a different answer and should not read
that as a discrepancy. **This is #116 exactly — the manifest is not bound to the
bytes it describes** — and it is
worth writing down that the limitation is observable here as two demonstrably
different binaries presenting one provenance digest. Anyone reaching for
`provenance` to tell two builds apart is using the wrong instrument.

## The measurements

Peak RSS of the server process via `/usr/bin/time -l`, over each binary's own
idle. A third payload was added — `repository` at the **midpoint** — so that the
shape of the cost is measured rather than inferred from two points. It is
byte-length-equal to the other two (409,600 bytes) and was generated by a
scratch script; the committed `mkpayloads.py` was not modified.

| cell | n | mean KiB | min–max | over idle |
|---|---|---|---|---|
| baseline idle | 9 | 1,440.0 | 1,440–1,440 | — |
| baseline unsigned (receipt) | 5 | 70,384.0 | 70,384–70,384 | 68,944.0 |
| baseline tampered | 6 | 70,992.0 | 70,992–70,992 | 69,552.0 |
| baseline signed, first | 9 | 71,271.1 | 71,248–71,296 | 69,831.1 |
| baseline signed, **mid** | 3 | 223,946.7 | 223,936–223,952 | 222,506.7 |
| baseline signed, last | 10 | 376,656.0 | 376,640–376,688 | 375,216.0 |
| prototype idle | 12 | 1,456.0 | 1,456–1,456 | — |
| prototype unsigned (receipt) | 5 | 2,889.6 | 2,880–2,896 | 1,433.6 |
| prototype tampered | 6 | 3,072.0 | 3,072–3,072 | 1,616.0 |
| prototype signed, first | 10 | 3,348.8 | 3,344–3,360 | 1,892.8 |
| prototype signed, **mid** | 3 | 173,781.3 | 173,776–173,792 | 172,325.3 |
| prototype signed, last | 9 | 344,147.6 | 344,128–344,160 | 342,691.6 |

| | baseline | prototype | |
|---|---|---|---|
| receipt | 68,944.0 | **1,433.6** | 48.1×, −97.9% |
| signed, `repository` first | 69,831.1 | **1,892.8** | 36.9×, −97.3% |
| signed, `repository` mid | 222,506.7 | **172,325.3** | 1.29×, −22.6% |
| signed, `repository` last | 375,216.0 | **342,691.6** | 1.09×, **−8.7%** |

## Why the last row barely moves, and why that is the answer

**Cost is LINEAR in scan distance in both binaries**, and the midpoint payload
is what establishes it rather than assuming it.

**SCAN DISTANCE IS THE OFFSET OF THE SCOPE STRING, NOT THE BODY LENGTH**, and
the two are not interchangeable — `json-scoped-string` searches for
`"repository":{` from the front, so what the scan walks is the distance to that
substring. Measured in the payloads rather than assumed: offset **1**
(`repository` first), **204,764** (mid) and **409,529** (last), so the endpoint
delta is **409,528 octets** and the midpoint sits at 50.0% of it.

| | slope | mid predicted | mid observed | error |
|---|---|---|---|---|
| baseline | 0.7457 KiB/octet scanned | 222,522.8 | 222,506.7 | +0.007% |
| prototype | **0.8322** KiB/octet scanned | 172,291.4 | 172,325.3 | −0.020% |

> **The isolated structural-scan slope worsens by 11.6%.**

The signed total falls only because the flat receipt term falls. Decomposing
`repository`-last:

| | receipt | non-receipt | total |
|---|---|---|---|
| baseline | 68,944 | 306,272 | 375,216 |
| prototype | 1,434 | **341,258** | 342,692 |
| change | **−67,510** | **+34,986** | −32,524 |

Read the middle column carefully, because the tempting summary is wrong. The
non-receipt cost is **not left untouched** — it goes UP, from 306,272 to 341,258
KiB. Two separate facts, and the recommendation depends on both:

> **The NET non-receipt cost RISES by 34,986 KiB, from 306,272 to 341,258.**

**THAT IS A NET, AND IT IS THE ONLY THING THE SUBTRACTION ESTABLISHES.** Two
readings that the numbers do NOT license, both tempting:

- **Not "packing eliminates none of the baseline non-receipt cost."** It
  demonstrably eliminates some: `o_byte_list` changes from allocating a buffer
  and copying 409,600 octets into it to returning them by alias, and that copy
  is inside this column. So at least one baseline component falls.
- **Not "the +34,986 KiB is lazy materialisation."** Its size is *consistent
  with* materialisation — one cursor plus one field array per octet scanned is
  the right order — but **the allocator was not instrumented**, so the observed
  net could equally combine removed baseline allocations with larger new ones.

What is established is the net and its sign: the non-receipt column goes UP.
Nothing in the recommendation depends on its composition — only on the fact that
receipt falls by 67,510 while non-receipt rises by 34,986, netting −32,524 KiB
(−8.7%). **The non-receipt column is 82% of the `repository`-last run at
baseline and 100% of it after packing, and packing makes it larger rather than
smaller.**

**SO THE CLASSIFICATION IS "HURTS THE SCAN", AND THE DISTINCTION FROM "IMPROVES
BOTH" IS THE WHOLE RESULT.** The signed TOTAL improves at every field position
tested. The SCAN — the thing this exercise was sent to measure, and the cost 4.4×
larger than receipt — is unambiguously worse per octet. Reporting the totals as
"improves both" would report a receipt win as a scan win, which is the class of
overclaim this project keeps having to correct.

## Recommendation: STILL UNRANKED

Narrow, and each clause bounded by what was run.

**THE ADAPTATION IS POSSIBLE.** That was one of the three candidate outcomes and
it is settled: a packed body can be consumed by an unmodified `json-scoped-string`
with identical behaviour, an identical accepted signature, and correct field
extraction. "Cannot adapt" is refuted. The adaptation cost **three consumer
boundaries through four helper sites** — the two the premise document named,
plus response serialisation, which it did not.

**THE EFFECTS OPPOSE, SO THE PAYLOAD QUESTION SURVIVES.** The exercise was run
on the theory that if a packed representation improved both receipt and scan,
#115 would rank itself and the payload-distribution question would become
irrelevant. It does not improve both: receipt improves 48×, the scan slope
degrades 11.6%.

**AND THE OPPOSITION IS ABOUT MAGNITUDE, NOT SIGN — which is the precise reason
this stays unranked rather than becoming a yes.** At all three measured field
positions of a 409,600-byte body the TOTAL improves; no measured case regresses.
What varies is HOW MUCH, and it varies enormously:

| scan distance | improvement in the signed total |
|---|---|
| `repository` at offset 1 | **−97.3%** |
| `repository` at offset 204,764 | −22.6% |
| `repository` at offset 409,529 | **−8.7%** |

A mechanism worth 97% and a mechanism worth 9% rank differently, and which one
#115 is depends on a property of real GitHub deliveries that nothing here has
sampled. That is still the blocker, and this exercise sharpens **what has to be
sampled** rather than removing the need to sample it:

> **BOTH BODY LENGTH AND SCAN DISTANCE, JOINTLY — not either alone.** Receipt
> saving scales with TOTAL BYTES; the scan penalty scales with the OFFSET of the
> scanned field. They are independent variables, and the benefit is the
> difference between two quantities governed by different ones. A follow-up that
> collects only payload sizes, or only field offsets, cannot rank #115.

The table above varies offset at a FIXED length, so it isolates one of the two
and says nothing about the other. An earlier draft of this document concluded
"what matters is not body SIZE but SCAN DISTANCE", which is exactly the
misdirection to avoid: it would have sent the follow-up to collect half the data
needed.

**WHAT WOULD MOVE THE LARGER COST IS DIFFERENT WORK.** The non-receipt column is
306,272 KiB at baseline — 82% of the `repository`-last run — and packing does
not shrink it; measured net, it grows to 341,258 KiB. Whatever its composition,
a mechanism aimed at that column is not "pack the body", and #115 as written
does not propose one.

**NOT ESTABLISHED, AND LABELLED AS SUCH:**

- **THERE IS NO CROSSOVER, AND AN EARLIER DRAFT OF THIS DOCUMENT CLAIMED ONE.**
  It projected the two scan slopes to a distance of ~785,640 octets at which the
  prototype would stop winning on the TOTAL. That figure was withdrawn because
  it is not merely unobserved, it is **incoherent**: a scan distance of 785,640
  octets requires a body at least that large, and the projection held the
  receipt saving fixed at its 409,600-byte value while receipt cost is itself
  linear in body length. Two variables were collapsed into one.
  A two-variable MODEL — receipt saving **0.16482 KiB per octet of BODY**
  against a scan penalty **0.08650 KiB per octet SCANNED**, with scan distance
  never exceeding body length — has the saving outgrowing the penalty by ~1.9×,
  and reproduces the measured `repository`-last net to within 1.4% (32,086
  predicted vs 32,524 observed).
  **THAT IS A MODEL, NOT A RESULT, AND IT IS NOT EVIDENCE THAT NO CROSSOVER
  EXISTS.** It reads one body size as a per-byte slope through the origin, which
  ignores fixed costs, the `OBuf` doubling thresholds, and the prototype's own
  static table — any of which can dominate at a small payload and none of which
  this exercise varied. **Only one body size, 409,600 bytes, was ever measured**,
  so the honest scope is three field positions at one length. A crossover at
  some other body size is neither shown nor excluded.
- **NEITHER COST HAS AN INSTRUMENTED CAUSE. THE ALLOCATOR WAS NOT
  INSTRUMENTED.** The SIZES of both are established — by subtraction, and
  corroborated by the linearity fit — and both attributions are read off the
  code's shape:
  the baseline's 306,272 KiB as the scan's per-call environment allocation, and
  the prototype's +34,986 KiB as lazy materialisation of the packed list. Each
  is a reading CONSISTENT WITH the measurement rather than a demonstrated cause.
  A different cause of the same size would not change the ranking conclusion,
  but it would change what work to aim at it.
- **ONE PLATFORM, ONE PROGRAM.** Everything here is one host, one handler and
  two field positions plus a synthetic midpoint. The arena arithmetic underneath
  depends on `sizeof(OVal)` and the target's fundamental alignment.

## Restoration, proven rather than asserted

A textual revert is not evidence that the backend works again.

- `oath/llvm.go` restored from `HEAD`; the tree is clean and the file is
  byte-identical to a pre-edit copy (`sha256 64bb0073…0933`).
- Rebuilt at the original output path, the artifact is **byte-identical to the
  original baseline binary** (`6011500c…700f`). The `-o` path is embedded in the
  artifact, so a rebuild to a different filename hashes differently; at a fixed
  path the build reproduces exactly.
- Remeasured, with idle controls at both ends of the sequence:

| cell | pre-prototype | post-revert | delta |
|---|---|---|---|
| idle | 1,440.0 | 1,440.0 | 0.000% |
| signed, first | 69,831.1 | 69,824.0 | −0.010% |
| signed, last | 375,216.0 | 375,216.0 | 0.000% |
| tampered | 69,552.0 | 69,560.0 | +0.012% |

202 with `miclip/oath-lang` on both signed orders, 401 tampered. Idle was
measured at both ends of the prototype sequence too (1,456 throughout, 12 reps),
so a box degrading mid-run would have been visible after rather than assumed
away. Disk and solver state were checked before and after: 39 GiB free, no `z3`.

## The harness, and the discard policy

**THE COMMITTED HARNESS WAS NOT MODIFIED.** It flaked with
`SETUP FAIL: server died before it was measured` on roughly **1 run in 10**
across ~100 runs, **including in `idle` mode where no request is sent** — which
is what establishes it as an instrument fault independent of the prototype.

Diagnosed by reading and then confirmed by mutation on a scratch copy: the
bind-wait loop reassigns `spid` from `pgrep` on every iteration BEFORE testing
the listening line, so a transient empty `pgrep` on the iteration that breaks the
loop leaves `spid` empty. `kill -0 ""` then fails and a live server is reported
dead. Forcing `spid=""` at that check reproduces the message exactly — and hangs,
because the branch's `wait "$tpid"` blocks on a `/usr/bin/time` whose child is
still healthy.

**POLICY APPLIED: a `SETUP FAIL` run contributes NO figure.** It fails CLOSED —
it cannot fabricate a wrong number, only lose a run — so affected reps were
discarded and re-run under a per-rep watchdog, and every cell's `n` above counts
only reps that produced a verdict. The transient-`pgrep` TRIGGER is a diagnosis;
the hang it causes is demonstrated. The committed instrument was left alone
because this exercise was not licensed to change it.

## Reproducing

The baseline half and the restoration half are reproducible from this repository
with the committed harness. **The prototype rows are not** — the producing code
was deliberately discarded, exactly as in the premise exercise, and rebuilding
means rewriting it from the description above. The guards are the part that is
easy to get wrong, and getting them wrong is silent.

The midpoint payload is regenerated by solving for a pad split that keeps the
body at 409,600 bytes with `"repository"` at offset 204,764 (50.0%); the
equal-length assertion is the control, since receipt cost is linear in body
length and unequal payloads would differ for a reason unrelated to field
position.
