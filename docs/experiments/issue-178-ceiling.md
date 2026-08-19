# #178 — where the structural-recursion ceiling actually is

**Status: MEASURED. The recommendation is to NARROW #178, not to drop its
LLVM-specific framing.** The ceiling is real, predictable, and both backends
have one — but there is a band, between 4.1x and 12.0x wide depending on the
recursion's shape, in which the LLVM artifact crashes and the Go artifact
succeeds, and the committed application sits in it.
What changes is the reason: not that one backend is broken, but that its
constant is far lower and its failure is silent.

Nothing was built. No lowering work, no runtime change, no native containers.

## What #178 recorded, and what it assumed

It recorded that `report.oath` segfaults on the LLVM backend at 5,500 distinct
delivery ids while Go returns a report, and framed that as a limit of one
backend. **5,500 was one program with one shape of data**, and the issue's own
decline condition said the ceiling was unmeasured.

## The ceiling is `stack_bytes ÷ frame_bytes`

Measured with two recursion shapes that differ only in how many live locals the
emitted C frame must hold, isolated from `Set`, `str-split` and the log format
so nothing but the recursion is under test. **Deepest depth that SUCCEEDED**,
bisected until the adjacent depth fails — so each figure is a boundary rather
than a bound:

| shape | 8,176 KB stack | 16,352 KB stack | ratio | bytes/frame |
|---|---:|---:|---:|---:|
| narrow | 171,558 | 345,979 | 2.017 | 48.8 → 48.4 |
| wide | 128,668 | 259,484 | 2.017 | 65.1 → 64.5 |

Doubling the stack doubles the depth to within 0.5%, and bytes-per-frame is
stable across both limits. **So the threshold is not mysterious and does not
need to be discovered per program**: it is the stack limit divided by the
frame the shape emits, and the frames here are 48 and 64 bytes.

At the 8,176 KB default that is ~172,000 frames for a simple recursion — which
is why `report.oath` failing at 5,500 deliveries is not a bare depth limit.
`set-member` walks the accumulated set once per record, so the work is
quadratic in distinct ids while the DEPTH per call is what overflows; the 5,500
figure belongs to that program's shape, not to the backend.

## Both backends have a ceiling

| shape | LLVM (8 MB default) | Go | Go ÷ LLVM |
|---|---:|---:|---:|
| narrow | 171,558 | 2,097,138 | **12.2x** |
| wide | 128,668 | 541,196 | **4.2x** |

Go exits **2** printing `goroutine stack exceeds 1000000000-byte limit` /
`fatal error: stack overflow`. Go's limit is the runtime's 1 GB goroutine stack
rather than `ulimit`, and **it is not unbounded**.

**LLVM USED TO EXIT 139 WITH NO DIAGNOSTIC. It now exits 70 and says why** — the
stack guard, described in its own section below. Every figure in this file was
first measured against the silent artifact and re-measured against the guarded
one; the ceilings moved by ~1%, which is the guard's margin and not its frame
cost.

**THE RATIO IS NOT A CONSTANT, and an earlier draft of this file reported it as
one.** Twelve-fold is the narrow shape; the wide shape is 4.1x, because Go's
frame grows faster with live locals than the emitted C frame does. So "Go is
twelve times deeper" is a fact about one recursion, not about the backends, and
placing any particular program in the band requires measuring that program's
frames on both.

**So "the LLVM backend has a correctness ceiling" is true and incomplete** —
both backends do — **but the disparity #178 was opened on is real and is not
dissolved by that.** Between ~172,000 and ~2,097,000 frames the LLVM artifact
crashes while the Go artifact returns an answer, and `report.oath` fails inside
that band today. An earlier draft of this file concluded the issue should drop
its LLVM framing; that was an overcorrection, caught by review. A shared
property with different constants still leaves a twelve-fold range where one
backend works and the other does not, and programs live there.

## The difference that survives the correction

**Failure mode, not depth.** Go printed what happened and exited 2. LLVM exited
139 with nothing at all. A user whose program exceeded the depth learned *"fatal
error: stack overflow"* on one backend and got a bare segfault on the other.

That was the part worth acting on, and it was much cheaper than any of #178's
four listed dispositions. **It has been done** — see the next section. It does
not raise the ceiling and does not close #178.

## Reproducing

`docs/experiments/issue-178-ceiling/` carries the instrument: `depth.oath` (the
two shapes, differing only in live locals held across the recursive call) and
`ceiling.sh`, which puts them into a throwaway store, builds both backends,
bisects each threshold and prints the platform, toolchain, source revision and
default stack limit alongside the figures. The committed store is untouched.

`transcript.txt` beside them is one run of it, so every figure in this file has
a persisted source rather than being copied from a terminal. **No gate ties the
two together** — a compiler or runtime change could make the table stale while
the repository stays green, and re-running `ceiling.sh` is the only check.

## The decline condition, answered: the ceiling is NOT above realistic use

The table above is two SYNTHETIC recursions. It establishes the model and
cannot answer the issue's decline condition, because a synthetic frame is not
the application's frame. So `app-ceiling.sh` measures the artifact #178 was
actually opened on, `apps/github-webhook/report.oath`, sweeping DISTINCT
delivery ids in a well-formed log:

| backend | max distinct records | stack per record |
|---|---:|---:|
| LLVM (8,176 KB default) | **3,949** | 2,120 bytes |
| LLVM (16,352 KB) | 7,913 | 2,116 bytes |
| Go (1 GB goroutine stack) | **24,797** | — |

Doubling the stack doubles the record count (ratio 2.004) and bytes-per-record
is stable across both limits, so the application obeys the same
`stack ÷ frame` model the probes do — at ~2,120 bytes per record, about 44x a
simple recursion's 48-byte frame, because the per-record work nests.

**An earlier draft of this file said the 5,500 figure "belongs to that
program's shape, not to the backend", contrasting it with depth. That framing
was wrong** and is corrected here: it IS depth. The per-record frame is what
makes ~4,000 rather than ~172,000 the limit, and the two are the same
phenomenon with different constants.

The Go/LLVM ratio on the real program is **6.3x**, inside the 4.2x-12.2x band
the two probes bracketed — so the model predicts the application rather than
merely coexisting with it.

**And this is what closes the decline condition.** The question was whether the
ceiling sits far above any realistic deployment. It does not, on either
backend: a verified consumer of an APPEND-ONLY event log stops at ~3,950
records compiled with LLVM and ~24,800 with Go. Twenty-five thousand webhook
deliveries is an ordinary lifetime volume for one repository, not an
adversarial input, and neither figure is a limit an operator would think to
check for.

**The guard does not change that.** It changes what reaching the ceiling LOOKS
like, and the ~1% the figures moved is its margin. #178 stays open.

## Reproducing the application figures

`app-ceiling.sh` beside `ceiling.sh`, with `app-transcript.txt` as one run.
It builds `gh-report-main` on both backends from a COPY of the committed
corpus, generates N lines with N distinct ids, and bisects N with both
endpoints validated.

**Its success predicate is not exit 0.** The entry point answers one of three
things and a usage string or a refusal also exits 0, so a bisection accepting
those would converge on the largest log the program can REFUSE and report it
as the largest it can PROCESS. It requires the repository line in the output.
An earlier terminal-derived set of figures did not check this and sat a few
percent away; the persisted run supersedes it.

## The disposition that was taken: a stack guard

Not one of #178's four listed dispositions, and it does NOT close the issue —
the ceiling is unchanged. What changed is that reaching it is now legible, and
that a HANDLER survives it.

Every emitted Oath body checks the stack before descending: it reads the frame
pointer, compares it against a floor, and on failure calls a runtime door that
already knows the two dispositions this backend has — a standalone program
exits **70** with a diagnostic, a handler answers **500** and keeps serving.

**THE HANDLER HALF IS A CONFORMANCE FIX, NOT AN ERGONOMIC ONE.** SPEC §14.2
answers 400 for an unrepresentable request field *"because a remote party must
not be able to halt a host"*. A body deep enough to exhaust the stack halted it.
Measured before and after, same program, same 8 MB stack:

| | control (500 B body) | 400,000 B body | control again |
|---|---|---|---|
| before | 200 OK | connection closed, **process dead** | connection refused |
| after | 200 OK | 500, one stderr line | 200 OK |

The obligation was already normative; this backend could not keep it. That is
why the guard routes through the existing refusal door rather than exiting
where it is detected — the door, not the guard, is what knows a server must
survive its input.

**THE BUDGET IS READ FROM THE HOST** (`getrlimit(RLIMIT_STACK)`), so a larger
`ulimit -s` raises the ceiling with no rebuild — verified against one binary at
8, 16 and 32 MB. That is also what keeps the message honest: the limit is a fact
about this artifact's environment, so the diagnostic says so rather than
claiming the program diverges. *No proof is not disproof*, one layer down.

**Two measurements that changed the implementation.** The first attempt read the
stack by taking the address of a local. That works and costs: an `alloca` whose
address escapes through `ptrtoint` enlarged every frame enough to cut the
application's ceiling from ~4,000 records to **~2,970** — a guard that made
exhaustion legible by making it arrive 26% sooner. Reading the frame pointer
instead (`llvm.frameaddress`) leaves frames unchanged; the ~1% that remains is
the 128 KB margin. Run time, median of 7 at three workload sizes, is inside
run-to-run variance:

| records | unguarded | guarded | |
|---:|---:|---:|---|
| 300 | 22.58 ms | 21.62 ms | −4.3% |
| 1,000 | 93.08 ms | 92.85 ms | −0.2% |
| 2,500 | 416.53 ms | 424.80 ms | +2.0% |

A single earlier run reported +13.6%; repeating it across sizes did not
reproduce that, and the honest statement is that the cost is not distinguishable
from noise here rather than that it is zero.

## Native containers landed — and the app ceiling did NOT move (#178 re-diagnosed)

The LLVM backend now lowers `Set`/`Map` operations to iterative native helpers
(`docs/native-containers.md`), removing the structural `set-member`/`si-*`
recursion this file's early drafts and #178's title blamed. Re-running
`app-ceiling.sh` on the artifact with native containers active:

| backend | max distinct records | before native containers |
|---|---:|---:|
| LLVM (8,176 KB) | **3,949** | 3,949 |
| Go | 24,797 | 24,797 |

**Zero change**, bytes-per-record identical. The structural container functions
are confirmed pruned (`f_si_member`/`f_set_member`/`f_set_add`/`f_map_insert` are
absent from the binary), so native lowering IS active — it simply is not what
bounds the app.

**The binding recursion is the RECORD-LIST fold, not the set.** `gh-dedup` and
`str-nub` recurse over the `(List Str)` of N records, and their kept-record
branch is `(Cons l (gh-dedup … rest))` — the recursive call is an ARGUMENT to
`Cons`, so it is NON-TAIL: N frames stay live. The `set-member`/`set-add` calls
at each frame compute and unwind BEFORE the recursion descends, so they never
contributed to peak depth. Native containers made them iterative and the peak
was never theirs.

Two consequences that redirect the issue:

- **Tail-call optimization would not fix it either** — these folds build a result
  list via `Cons`, so they are not in tail position.
- **The driver is the `List` representation.** Closing #178 for `report.oath`
  means native/iterative lists (a large change touching the most fundamental
  datatype and the prove-over-structural-model guarantee) or bounded input. The
  stack guard remains the mitigation: reaching the ceiling is a legible exit 70
  with a diagnostic, and a handler answers 500 rather than dying.

So native containers is a correct backend-faithfulness improvement that removes a
real (non-binding) recursion and closes the ceiling for a program whose set/map
operations ARE its deepest recursion — `report.oath`'s are not. #178 stays open,
re-diagnosed: its ceiling is list recursion, and the set was never the cause.

## Option A landed: the stack ceiling is fixed, and the heap is the next constraint

The compiled program now runs on a large dedicated worker stack (~1 GiB pthread,
the same choice Go's goroutine stacks make), with the stack guard's floor
re-derived from that thread's bounds. The emitted `@main` is a thin wrapper that
hands the program body (`@o_program`) to the runtime's `o_run`; failure to obtain
the large stack falls back to the main thread under the constructor's floor.

**The stack ceiling is fixed.** A pure recursion past 1 GiB refuses legibly (exit
70, a diagnostic) and never SIGSEGVs; the guard's floor derivation on the worker
thread is verified (`TestLLVMLargeStackRaisesCeilingAndStillRefuses`). For
`report.oath` specifically:

| N distinct records | LLVM (before) | LLVM (Option A) | Go |
|---:|---|---|---|
| 3,949 | segfault (ceiling) | **exit 0** | exit 0 |
| 20,000 | — | **exit 0** | exit 0 |
| 25,000 | — | **exit 0** | **exit 2** (goroutine-stack overflow) |
| 40,000 | — | **exit 0** | exit 2 |
| 60,000 | — | exit 137 (OOM) | exit 2 |

Two things this shows. #178's decline condition — *ceiling above realistic use
(~25,000 deliveries)* — is **met**: `report.oath` processes 40,000 records where it
crashed at 3,949. And the LLVM backend now outlasts the **Go** backend, whose
~1 GB goroutine stack overflows at ~25,000 because its per-frame cost (~43 KB) is
~20× the emitted C frame (~2,120 B). The two executors simply carry different
resource bounds and now both refuse legibly at theirs — the accepted state of the
system, not a semantic divergence.

**The next binding constraint is the HEAP, not the stack.** Past ~50,000 records
the LLVM artifact is OOM-killed (exit 137). This is not recursion depth — 50,000
frames fit 1 GiB with room to spare — it is the arena: the native `Set` is
immutable and `set-add` copies the whole array, so N incremental adds allocate
O(N²) slots into a request arena that is not freed until the program exits. That
limit was ALWAYS there (the structural list had the same O(N²) rebuild); the
stack crash at 3,949 simply masked it. Fixing the stack revealed it, the same way
native containers revealed the list recursion. It sits above realistic use, and
its ungraceful SIGKILL (versus the stack guard's legible exit 70) is a separate,
heap-shaped concern — filed apart from #178, which was about the stack.

## What this does NOT establish

- **That the guard catches EVERY exhaustion.** The check is the first
  instruction of a body, but the machine-code prologue has already reserved
  that body's frame by then — so a single frame larger than the 128 KB margin
  would fault before its own check. 128 KB is 16,384 spilled pointers in one
  function and nothing in this corpus approaches it, but that is a bound rather
  than a proof, and the supported claim is: exhaustion is caught for any
  program whose largest single frame fits in the margin.
- **That 48, 64 and 2,120 bytes generalise.** Three shapes on one platform, one
  compiler. The formula is the claim; the constants are not, and another
  consumer will have its own per-record cost.
- **Anything about heap growth.** Only stack depth was varied. `report.oath`'s
  quadratic set walk is a separate cost that was not measured here.
