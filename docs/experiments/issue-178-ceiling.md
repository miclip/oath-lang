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
| narrow | 174,270 | 348,691 | 2.001 | 48.0 → 48.0 |
| wide | 130,702 | 261,518 | 2.001 | 64.1 → 64.0 |

Doubling the stack doubles the depth to within 0.5%, and bytes-per-frame is
stable across both limits. **So the threshold is not mysterious and does not
need to be discovered per program**: it is the stack limit divided by the
frame the shape emits, and the frames here are 48 and 64 bytes.

At the 8,176 KB default that is ~174,000 frames for a simple recursion — which
is why `report.oath` failing at 5,500 deliveries is not a bare depth limit.
`set-member` walks the accumulated set once per record, so the work is
quadratic in distinct ids while the DEPTH per call is what overflows; the 5,500
figure belongs to that program's shape, not to the backend.

## Both backends have a ceiling

| shape | LLVM (8 MB default) | Go | Go ÷ LLVM |
|---|---:|---:|---:|
| narrow | 174,270 | 2,097,138 | **12.0x** |
| wide | 130,702 | 541,196 | **4.1x** |

LLVM exits **139** with no diagnostic; Go exits **2** printing `goroutine stack
exceeds 1000000000-byte limit` / `fatal error: stack overflow`. Go's limit is
the runtime's 1 GB goroutine stack rather than `ulimit`, and **it is not
unbounded**.

**THE RATIO IS NOT A CONSTANT, and an earlier draft of this file reported it as
one.** Twelve-fold is the narrow shape; the wide shape is 4.1x, because Go's
frame grows faster with live locals than the emitted C frame does. So "Go is
twelve times deeper" is a fact about one recursion, not about the backends, and
placing any particular program in the band requires measuring that program's
frames on both.

**So "the LLVM backend has a correctness ceiling" is true and incomplete** —
both backends do — **but the disparity #178 was opened on is real and is not
dissolved by that.** Between ~174,000 and ~2,097,000 frames the LLVM artifact
crashes while the Go artifact returns an answer, and `report.oath` fails inside
that band today. An earlier draft of this file concluded the issue should drop
its LLVM framing; that was an overcorrection, caught by review. A shared
property with different constants still leaves a twelve-fold range where one
backend works and the other does not, and programs live there.

## The difference that survives the correction

**Failure mode, not depth.** Go prints what happened and exits 2. LLVM exits 139
with nothing at all. A user whose program exceeds the depth learns *"fatal error:
stack overflow"* on one backend and gets a bare segfault on the other.

That is the part worth acting on, and it is much cheaper than any of #178's four
listed dispositions: a guard that detects exhaustion and reports it does not
raise the ceiling, does not need native containers, and turns an
undiagnosable crash into a message. This file does not propose one — naming a
direction is not a plan — but it is the disposition the measurement supports,
and it is not among the four the issue lists.

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

## What this does NOT establish

- **That 48 and 64 bytes generalise.** Two shapes on one platform, one compiler.
  A shape holding more live values per frame will sit lower, and the formula is
  the claim rather than the constants.
- **Anything about heap growth.** Only stack depth was varied. `report.oath`'s
  quadratic set walk is a separate cost that was not measured here.
- **Where a realistic deployment sits.** The issue's decline condition asks
  whether the ceiling is above realistic use. ~174,000 frames is far above any
  webhook log, and `report.oath` still failed at 5,500 records — so depth alone
  does not answer it, and the program's access pattern is what needs measuring
  to close that question.
