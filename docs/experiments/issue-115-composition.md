# #115 — what the memory is actually made of

**Status: MEASURED. Nothing is proposed here and nothing was built.** This is an
attribution of the figure `issue-115-premise.md` measured but could not
decompose: the peak RSS of one signed 409,600-byte delivery on the LLVM backend.
The premise document established the SHARE that receipt accounts for; this
decomposes the remainder.

**IT DOES NOT EVALUATE #115'S PROPOSED MECHANISM.** When the premise document
says "the mechanism remains unevaluated" it means the packed-body proposal, and
that is still true: nothing alternative was implemented, built or timed here.
What this evaluates is the COMPOSITION of the cost the existing runtime already
pays. Those are different claims and conflating them would reverse the premise
document's status.

The instrument was a throwaway. It was reverted, and the restoration is proved
at the end rather than asserted.

## The question this answers, and the one it does not

`issue-115-premise.md` measured that receipt's share of a delivery's memory
ranges from ~99% to ~18% depending on where the `repository` key sits in the
body. Both numbers are about the same absolute quantity — receipt cost is linear
in body length and does not move — so the interesting half was always the
complement: the column that appears only when the scan has to walk the body, and
which nothing had ever looked inside.

That column is **306,272 KiB** of the 375,200 KiB this delivery adds to RSS. This
document says what is in it.

It does NOT establish anything about Oath programs in general. The universe here
is one program (`apps/github-webhook`), one backend (`llvm-ir/1`), one path (a
signed delivery that runs to completion), and one adversarial payload ordering.
The corpus is not a sample of programs; it is the exhibit this experiment chose.

## The instrument

`xalloc` is the single funnel: every allocation in the LLVM runtime reaches the
allocator through it, and it is `static`, so emitted IR cannot go round it. That
is what made a complete attribution cheap — the site was never in doubt.

    static void *xalloc_at(size_t n, int line, const char *fn) { ... }
    #define xalloc(n) xalloc_at((n), __LINE__, __func__)

No call site was edited. `__LINE__`/`__func__` identify all 24 sites in the
emitted `rt.c`, and two further axes were added on top:

- **`val()` split by tag** (`OVal:ctor/str/clos/bool/int`), via a one-shot note
  set immediately before the carve. Without this every boxed value in the
  runtime collapses into one C function and the table says nothing about Oath.
- **Request phase**, set at four points in `o_serve_loop`: `provision` (the perm
  region, `o_perm_state == 1`), `receipt` (`o_http_parse`), `adapt`
  (`o_http_adapt` / `o_nominations` / `o_http_value`), `handler` (`o_apply`),
  `respond` (`o_http_respond`). The dump fires after serialisation and before
  `o_arena_release()`, so it covers the whole request.

**THE TABLE HAS AN OVERFLOW BUCKET AND IT IS PRINTED.** A fixed-capacity table
that silently drops a row produces a short table that looks complete, which is
this repository's most familiar defect. The bucket read `0 0 0` on every run
reported here, and the row sum is printed separately from the running total so
the two can be compared rather than assumed equal.

The instrument measures **requested bytes**, and separately records the
align-rounded carve, the block-level `calloc`, and the arena high-water. Those
are four different quantities and the reconciliation below keeps them apart.
Alignment is `_Alignof(max_align_t)`, which on this host leaves almost every
allocation already aligned — 1,333 bytes of rounding across 8.2 million
allocations — so requested and carved are interchangeable at this scale and are
still reported separately rather than merged.

## Allocator closure — exact

One signed `repo`-last delivery, 202 Accepted:

    allocations          8,202,221
    requested bytes    382,561,627
    carved bytes       382,562,960      (+1,333 B of alignment rounding, total)
    overflow                     0
    row sum == total           yes      (8,202,221 / 382,561,627 / 382,562,960)
    arena blocks             5,818      382,812,160 B calloc'd
    arena peak live                     382,562,640 B

Nothing is freed inside a request, so cumulative allocation and peak live differ
by exactly 320 bytes — the perm-region total, which is outside the arena.

That is **933.99 bytes allocated per byte of request body**, for a program whose
work is to verify an HMAC and find one key in a JSON object.

## RSS reconciliation — kept separate, deliberately

The allocator table and the RSS figure are two instruments and they are not
interchangeable. Against the clean (uninstrumented) baseline:

    idle                                        1,440 KiB
    signed repo-last                          376,640 KiB
    over idle                                 375,200 KiB   = 384,204,800 B
    arena blocks calloc'd                     373,840 KiB   = 382,812,160 B
    requested bytes                           373,595 KiB   = 382,561,627 B

**The arena accounts for 99.64% of the RSS growth.** The 1,604.7 KiB that the
allocator table does not reach is block headers (5,818 × `sizeof(OBlock)`),
malloc metadata, unused tails of partly-filled blocks, stack, and page
granularity. It is not attributable by this instrument, which counts bytes
requested rather than pages resident, and no attempt is made here to split it
by call site.

## The two columns

    receipt         68,928 KiB   18.37%   linear in body length, invariant in
                                          `repository` position
    non-receipt    306,272 KiB   81.63%   appears only when the scan walks
    -----------------------------------
    over idle      375,200 KiB  100.00%

**The boundary is drawn by PHASE, and it differs from the premise document's.**
Here the HMAC's byte conversion (`o_byte_list`) sits in the non-receipt column
because it runs inside `o_apply` — it is Oath handler code in this program, not
transport. `issue-115-premise.md` counted it with receipt. The difference is
409,696 bytes, 0.11% of the total, and it is stated rather than left to be
discovered by someone comparing the two documents: with crypto counted as
receipt the band is 18.51%/99.26%, which is the premise document's "~18% to
~99%", and this document reproduces that band before re-cutting it.

### The non-receipt column — what is MEASURED

These are allocated bytes, attributed by the instrument. They sum to
**312,173,708 B** and every row is a direct reading:

| component | bytes | % of measured column |
|---|---:|---:|
| `o_env_push` + boxed closures (`OVal:clos`) | 252,461,680 | **80.87%** |
| boxed `Bool` (`OVal:bool`) | 59,051,520 | **18.92%** |
| crypto byte conversion (`o_byte_list`) | 409,696 | **0.13%** |
| other handler values (`Int`, `Str`, ctor, `o_str_cons`) | 250,812 | **0.08%** |
| **total** | **312,173,708** | **100.00%** |

**99.79% of the measured column is closure machinery and boxed Bools.**

### The same column expressed against RSS — and what is STIPULATED in it

Scaling to RSS gives a column of **306,272 KiB**, within which the same four
components account for 80.50% / 18.83% / 0.13% / 0.08% and a **0.46%
(1,415 KiB) RSS-to-requested-byte remainder** closes it to the byte.

**THE REMAINDER'S SPLIT BETWEEN THE TWO COLUMNS IS A STIPULATION, NOT A
MEASUREMENT, AND SAYING SO IS THE POINT.** The instrument attributes allocation
sites; it does not observe which phase owns resident pages, and no
phase-sensitive RSS measurement was taken. The 1,604.7 KiB gap is therefore
divisible in any proportion, and this presentation assigns 1,415 KiB here and
189.8 KiB to receipt. That choice is not derived from anything above.

What bounds the consequence is that the gap is 0.43% of RSS growth, so the
column total lies in **[304,857, 306,462] KiB** across every possible split — a
0.5% band — and the leading component's share lies in **[80.45%, 80.87%]**. No
conclusion in this document turns on where in that band the true value sits, and
the measured table above is the one to quote when it matters.

### The receipt column, 68,928 KiB

| component | bytes | KiB |
|---|---:|---:|
| body → Oath `(List Int)` value (adapt) | 68,815,413 | 67,202.6 |
| HTTP receipt buffers (`o_buf_put`, `o_http_parse`) | 1,571,930 | 1,535.1 |
| response serialisation | 256 | 0.3 |
| provisioning (perm region, 6 allocations) | 320 | 0.3 |
| RSS-to-requested-byte remainder | 194,353 | 189.8 |
| **total** | **70,582,272** | **68,928.0** |

Materialising the body as `(List Int)` costs **168.01 bytes per body byte** —
one `OVal` cons cell (72 B) plus one boxed `Int` (72 B) plus a two-slot field
array (16 B) plus one bignum limb (8 B), per octet. That is the representation
cost of the request value itself and it is paid before the handler is entered.

## The constants are integers, which is what makes this structural

Per byte of request body, in the handler phase:

    10.006   o_env_push calls
     4.003   closure boxes
     2.002   boxed Bools

and in the adapt phase, exactly one cons cell, one boxed `Int`, one field array
and one bignum limb each. `sizeof(OVal)` is 72 bytes. Environment arrays average
32.8 bytes, i.e. `(n+2)*8` with n ≈ 2.1 — **so the cost is allocation COUNT, not
environment depth.** Ten environment pushes and four closures per octet is a
fixed shape being paid 409,600 times.

### Is it linear? — measured at four body sizes, not inferred

Per-byte counts at ONE body size cannot distinguish linear from quadratic
growth, and the `repo`-first control does not help: it varies scan DISTANCE at
fixed LENGTH. So the question was measured directly, with the uninstrumented
binary and the committed harness:

| body bytes | peak RSS | over idle | B per body byte | ratio to previous |
|---:|---:|---:|---:|---:|
| 102,400 | 95,632 KiB | 94,192 KiB | 941.9 | — |
| 204,800 | 189,296 KiB | 187,856 KiB | 939.3 | 1.994 |
| 409,600 | 376,656 KiB | 375,216 KiB | 938.0 | 1.997 |
| 819,200 | 699,168 KiB | 697,728 KiB | 872.2 | 1.860 |

All four returned 202 and emitted `miclip/oath-lang`, so all four walked the
whole body.

**THE BOUNDED RESULT: over 102,400–819,200 bytes, doubling the body
approximately doubles the memory — 1.99, 2.00, 1.86 against 2.0 for linear and
4.0 for quadratic.** Eight times the body costs 7.4 times the memory, not 64
times.

That is a statement about the measured range and not an asymptotic one. Four
points cannot establish a growth class, and the largest point is 7% BELOW the
linear projection for a reason this experiment does not explain — memory
pressure and compression at ~700 MiB on this host are plausible confounds, and
the deviation is away from the growth the control was checking for, so it is
recorded and not chased. What the sweep does settle is the thing that would have
changed the reading of the per-octet constants: **nothing quadratic is happening
across a factor of eight in body length.**

`measure.sh` was unmodified. The scaling payloads come from
`issue-115-composition/mksize.py`, which exists because the committed
`mkpayloads.py` pins N = 409,600 by design — the premise experiment's control is
that its two bodies are equal in length, and a generator taking N as an argument
cannot assert that. `mksize.py` therefore IMPORTS `mkpayloads.build` rather than
restating the body structure, and refuses to write anything unless its
409,600-byte output is byte-identical to what `mkpayloads.py` itself produces.
That refusal was verified by mutation: a one-byte disagreement makes it exit with
no files written.

## The control: `repo`-first invariance

The attribution above would be an assertion if it rested on one run. It does
not. The same binary was run against `repo-first.json` — the same 409,600
bytes, the same secret, the same route, differing only in whether the
`repository` object precedes or follows the padding.

| component | repo-last | repo-first | ratio |
|---|---:|---:|---:|
| body → `(List Int)` | 68,815,413 | 68,815,413 | **1.000** |
| HTTP receipt buffers | 1,571,930 | 1,571,930 | **1.000** |
| crypto byte conversion | 409,696 | 409,696 | **1.000** |
| response serialisation | 256 | 256 | **1.000** |
| provisioning | 320 | 320 | **1.000** |
| closures + boxed `Bool` | 311,513,200 | 274,080 | **1,136.6** |
| other handler values | 250,812 | 254,024 | 0.987 |
| **total requested** | **382,561,627** | **71,325,719** | 5.36 |
| peak RSS | 376,656 KiB | 71,264 KiB | 5.29 |

Every component that is a function of body LENGTH is byte-identical between the
two payloads. The one component that moves is the one this document attributes
the column to, and it moves by three orders of magnitude. The "other handler
values" row is very slightly *larger* in repo-first, confirming it is not
scan-proportional either.

`repo-first` also closed exactly: 1,649,870 allocations, 71,325,719 requested,
71,327,064 carved, overflow 0, row sum == total, 1,067 blocks / 71,450,624 B.

**This is what makes the column's identification a measurement rather than a
reading of the code.** The instrument could have attributed those bytes to the
right C functions and still have been wrong about what they are FOR; an
independent variable that leaves five components untouched and collapses one by
1,136× settles it.

## Semantic and identity controls

A memory figure from a path that did not run is worth nothing, so what ran was
pinned independently of what was measured:

    status                    HTTP/1.1 202 Accepted   (harness invalidates otherwise)
    emitted record            oath-gh/1  00000000-...-0001  push  miclip/oath-lang
    body length               409,600 bytes exactly
    repo-last.json sha256     0ac617402b93adb8c7552ea05d6ce20bab5d3dcdecb8a7b84a6342223acd2f37
    repo-first.json sha256    8d6553f5061c7219b5a6057a514aee684568e6aeef946038cdb6a800cbbb206c
    provenance digest         3db050528d9a01b58c22a6aea02cc8b6c3383b0389cd4da03ee560c64b3bd12b

The emitted `miclip/oath-lang` is the load-bearing one: it proves the scan
reached the `repository` object at the tail of the body rather than terminating
early, which is the only thing that distinguishes this path from a cheaper one
that would produce a plausible-looking RSS figure. The provenance digest is
identical for the instrumented and the reverted build — the C runtime is not
part of the artifact manifest — which confirms the same program was measured on
both sides of the revert.

**The harness was not modified.** `issue-115-premise/measure.sh` and
`issue-115-premise/mkpayloads.py` were used exactly as committed; every change
was confined to `oath/llvm.go` and reverted. Every build went into a hermetic
store copy, and `codebase/` and `fixtures/` were clean throughout.

## Restoration proof

| control | result |
|---|---|
| `oath/llvm.go` sha256 before | `64bb007398dfd197ebfa33ff78ca22802fd29e1dde392427fd138169a4cb0933` |
| `oath/llvm.go` sha256 after | `64bb007398dfd197ebfa33ff78ca22802fd29e1dde392427fd138169a4cb0933` |
| `git status` / `git diff HEAD` | clean / empty |
| grep for `o_ai_`, `xalloc_at`, `THROWAWAY`, `O_AI_` | 0 hits |
| rebuilt idle | **1,440 KiB** — baseline exactly |
| rebuilt signed repo-last | **376,688 KiB**, 202, `miclip/oath-lang` |
| `#AI` instrumentation output in rebuilt run | **absent** |

A digest match proves the source was restored; the rebuilt behavioural run is
what proves the restored source still produces the baseline binary. Both are
recorded because neither implies the other.

**Observed tolerance: 48 KiB.** The instrumented run peaked at 376,656 KiB and
the post-revert rerun at 376,688 KiB, against a 376,640 KiB baseline. Run-to-run
peak-RSS jitter on this host is therefore ~48 KiB — 0.013%, immaterial to every
figure here, and wider than the 4 KiB agreement an earlier run happened to show.
Any future comparison on this path should carry the 48 KiB tolerance rather than
the 4 KiB coincidence.

**The instrumented run's +16 KiB is CONSISTENT with the instrument's static
table (96 sites × 176 bytes ≈ 16.5 KiB) and is not established by it.** The
jitter is three times the effect, so a single pair of runs cannot separate the
table's footprint from noise; the table's size is an upper bound on what the
instrument could have added, and that is the whole of the claim. It is worth
stating precisely because the temptation is to read a number that matches a
prediction as a confirmation of it.

## What the column is

**The column is the structural traversal itself.** Not the receipt, not the
crypto, not the JSON parse, not the response: 99.79% of it is the closure
environments, closure boxes and boxed Booleans that a per-octet walk over the
body allocates as it goes. The program's own logic — the comparisons, the field
extraction, the record it emits — is 0.08% of the column.

**This is the typed-IR argument arriving from the other side.** #118 measured the
no-expression-IR decision on REPRESENTABILITY and it held: 4,660 of 4,660
subterm types recoverable, 286 of 286 polymorphic call sites carrying their
instantiation in the raw canonical bytes. The neutral representation of a body is
the verified `Def` closure, and nothing needs to be added to it to know what a
term means. That answered *is a typed IR NEEDED to represent a body?* — no.

It did not answer, and was not asked, *what does lowering from that
representation COST at run time?* This measures that, and the two answers do not
conflict. A body can be perfectly representable and still lower into a shape
where every application allocates a fresh environment array and a closure box,
and every comparison allocates a `Bool`. The cost never shows up on a corpus of
small definitions; it shows up the first time something walks 409,600 octets,
where a per-application cost too small to notice becomes 616.4 bytes of closure
machinery per body byte, 765.7 bytes for the column as a whole, and 81.63% of
the process's memory.

**Representability was the question that could be settled statically. Cost is
the one that needed a payload.**

## The evident direction — named, not taken

Two allocations per octet are doing no work that survives the next iteration:

1. **Per-octet closure and environment allocation.** 10 environment pushes and 4
   closure boxes per body byte, at an average environment depth of 2.1. The
   arrays are copied and discarded immediately; nothing in the traversal
   captures them.
2. **Boxed `Bool`.** 2.002 per body byte, 72 bytes each, 18.83% of the column.
   `Bool` has two inhabitants and `OVal` is immutable once tagged.

**Both are REPRESENTATION, not SEMANTICS, and that is what makes this a safe
direction rather than a language question.** CLAUDE.md's split puts "how closures
are represented" on the backend's own side of the line; `Int` being ℤ and `Str`
being an inductive datatype are on the other. Changing either of the two above
binds no other backend, forces nothing on the Go backend or on `oath eval`, and
adds no proof obligation — the prover never sees a closure representation.

**Nothing here is designed, costed, or implemented, and this document does not
recommend starting.** Naming a direction is not a plan, and the standing rule
applies: an architectural item must keep a credible path to "no change
required", and one exists here.

The column is **position-dependent by three orders of magnitude**: on
`repo-first` the closure-and-`Bool` component is 274,080 bytes — 268 KiB, 0.38%
of that run's allocation. So if real deliveries place `repository` early, the
column is a worst-case artefact rather than a standing cost.

**WHETHER THEY DO IS UNKNOWN AND WAS NOT MEASURED.** Both payloads here are
synthetic, generated by `mkpayloads.py` to isolate one variable; no GitHub
delivery was captured and no distribution of real payloads was sampled — the
premise and consumer documents record the same gap. This experiment therefore
supplies the SHAPE of the "no change required" path and not the evidence for it.

**The column measured here is the ADVERSARIAL ordering, and that is the first
thing anyone picking this up should replace.** Sampling real deliveries is
cheaper than any of the work named above and it decides whether there is work
here at all.

## Reproducing

The instrumentation is not committed, by design — it was a throwaway and
carrying it would mean maintaining a second allocator. What is committed is
everything needed to re-derive the uninstrumented figures:

```sh
make build
w=$(mktemp -d); store=$(mktemp -d)/codebase
python3 docs/experiments/issue-115-premise/mkpayloads.py "$w"
cp -R codebase "$store"
OATH_STORE="$store" ./oath/oath put apps/github-webhook/webhook.oath
OATH_STORE="$store" ./oath/oath build gh-webhook --backend llvm -o "$w/ghd"
docs/experiments/issue-115-premise/measure.sh "$w/ghd" "$w/i" 21100 idle
docs/experiments/issue-115-premise/measure.sh "$w/ghd" "$w/l" 21101 signed "$w/repo-last.json"
docs/experiments/issue-115-premise/measure.sh "$w/ghd" "$w/f" 21102 signed "$w/repo-first.json"

# the scaling control
python3 docs/experiments/issue-115-composition/mksize.py "$w"
p=21110; for N in 102400 204800 409600 819200; do p=$((p+1))
  docs/experiments/issue-115-premise/measure.sh "$w/ghd" "$w/s$N" $p signed "$w/last-$N.json"
done
```

To re-derive the attribution, rename `xalloc` to `xalloc_at(n, line, fn)` in
`oath/llvm.go`'s `llvmRuntimeC`, add the `__LINE__`/`__func__` macro, set a
one-shot note in `val()` from the tag, set a phase global at the four points in
`o_serve_loop` named above, and dump the table before the success path's
`o_arena_release()`. The overflow bucket and the row-sum-versus-total assertion
are not optional: without them a truncated table is indistinguishable from a
complete one.
