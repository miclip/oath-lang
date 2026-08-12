# Is a compiled artifact's digest a function of the closure? (#116)

#116 asks what a provenance signature would be taken *over*. Its fourth bullet
is the premise this experiment measures:

> Reproducible builds: two builds of the same closure should produce the same
> digest, or a signature is over a moving target. Go's build IDs and paths make
> this non-trivial.

**Nothing here signs anything, and nothing here changes the kernel or the
specification.** This is a measurement of what `oath build` already produces,
and a record of what would have to be true before signing an artifact digest
means anything.

## The answer

**No. Today the artifact digest is not a function of the closure, in both
backends, for three independent reasons.** The one that matters most is the one
no build flag can fix:

| # | cause | backends | fixable by | 
|---|---|---|---|
| 1 | **the emitter's output order is unpinned** | Go **and** LLVM | a code change in `oath` (§Causes) |
| 2 | the build path is embedded | Go only | `go build -trimpath` (measured) |
| 3 | the output **filename** is embedded | LLVM only | a code change in `oath`: link to a constant name, then move (measured) |

Cause 1 is the finding. It was not previously suspected, it defeats the other
two repairs on its own, and it is a defect in `oath` rather than in a toolchain.

## Rerunning

```sh
./docs/experiments/issue-116-reproducibility/reproducibility.sh
```

~7 seconds. It builds the CLI from the checkout, `put --new`s one program into a
scratch `OATH_STORE` (both created with `mktemp -d` and removed on exit), and
never writes to `codebase/` or `fixtures/`. `REPRO_REPEATS=N` changes the sample
size; `OATH_BIN=/path/to/oath` measures a specific binary instead of building one.

**It asserts the controls and the filename finding, and only REPORTS the
repeated-build digest counts.** That split is deliberate: cause 1 is
intermittent, so an assertion like "N rebuilds agreed" would be a gate that
fails at random, and a run that happens to be uniform is a sample rather than a
proof of determinism. The script says so in its own output.

## Environment

Findings 1 and 3 are structural. Finding 2 and every literal hash below are
specific to this host, and hashes are recorded as evidence of what was run
rather than as values another machine should reproduce.

```
go version go1.25.6 darwin/arm64
Apple clang version 21.0.0 (clang-2100.1.1.101), arm64-apple-darwin25.5.0
macOS 26.5.2 (25F84), arm64
program: docs/experiments/issue-158-llvm-subset/show-from-marker.oath
```

`show-from-marker` is reused rather than copied: it is self-contained (it
declares `List` and `Str` itself), it is one of the few programs both backends
accept, and — relevant here — `str-contains` and `str-tail-from` are siblings in
its dependency closure with no edge between them, which is exactly the shape
whose emission order is unpinned.

## Causes

### 1. The emitters order top-level functions by Go map iteration

Both backends walk the entry's dependency closure and append to an `order` slice
as they go, recursing over the deps in **map iteration order**, which Go
randomises per process:

- `oath/compile.go:1608` — `for dep := range collectDepsBody(d)`, feeding
  `e.order` (`compile.go:1614`), which is the sole driver of emission at
  `compile.go:1123-1127`.
- `oath/llvm.go:356` — the same loop, its own copy, feeding `e.order`
  (`llvm.go:362`), consumed at `llvm.go:1983`.

When two definitions are siblings with no dependency between them, which one is
emitted first is decided by a randomised iterator. The result is a **pure
reordering**: identical content, identical line count.

Measured over 30 emissions of each backend's source, capturing the generated
text itself (see *Method* below):

```
Go source :  27 x b51b71fd4558b72d   3 x 5918780f5994a4a7
LLVM IR   :  25 x 717c8e033335f012   5 x 74128b08574d42d5
```

Exactly two outcomes each, which is what one swappable sibling pair predicts.
The Go source difference is a pure permutation of lines (`sort` of both files
agrees); the LLVM IR is not, only because SSA temporaries are numbered in
emission order, so swapping two functions renumbers `%t42`/`%t67` downstream.
Same root cause, two surface forms.

The artifact digest tracks the source digest **exactly** — 14/14 paired runs of
the LLVM backend, where the IR was captured and the artifact hashed in the same
invocation:

```
iter 1-11, 13   IR 717c8e033335f012  ->  artifact a88ffe05d402843c
iter 12, 14     IR 74128b08574d42d5  ->  artifact 993c877b6e8c070c
```

So clang and ld are deterministic here given identical input; the variance is
entirely upstream, in `oath`.

**The backend-neutral layer already does this correctly.** `programClosure` in
`oath/program.go:881-890` materialises the same dep set into a slice,
`sort.Strings` it, and then recurses. The two backend emitters each omit that
step. Three closure walks, one of them right — which is the "one authoritative
path, derive the rest" rule in CLAUDE.md, met from the failing side.

### 2. The Go backend embeds the build directory

`oath/compile.go:65` builds in `os.MkdirTemp("", "oath-build-")` and shells out
to `go build`, so the random directory name lands in the Go build ID and the
binary:

```
$ strings -a <artifact> | grep -o 'oath-build-[0-9]*'
oath-build-115930045        # one build
oath-build-1243130962       # the next
```

**The operative variable is the random suffix, not `cwd` or `TMPDIR`.** Two
builds with *identical* `cwd` and `TMPDIR` still differ, because `MkdirTemp`
draws a fresh suffix every time. In the script's sampled run every Go build was
distinct — 8 distinct digests from 8 rebuilds — while the LLVM backend showed
only the 2 of cause 1.

`-trimpath` removes it. Measured on a **scratch copy** of `oath/` with
`exec.Command("go", "build", "-trimpath", "-o", abs, ".")`, the repository
source untouched:

```
without -trimpath   4 builds, 4 distinct digests
with    -trimpath   12 builds, 11 x 19c5c4d2a7622b75 + 1 x c1ebd15a83353dce
```

The single outlier is cause 1 showing through, not a path effect — which is the
point: `-trimpath` is necessary and not sufficient.

Confirmed independently of `oath`, on a fixed `main.go` built in three
directories:

```
plain      dir-aaaa / dir-bbbb / dir-ccccccccccc  ->  3 distinct digests
-trimpath  all three                              ->  1 digest (9e017e5771004…)
```

### 3. The output filename is embedded — LLVM backend only

macOS's linker applies an ad-hoc signature carrying a **signing identifier**.
`clang -o <path>` names it after the output, so for the LLVM backend the
basename is inside the signed bytes:

```
$ oath build show-from-marker --backend llvm -o alpha   # Identifier=alpha
$ oath build show-from-marker --backend llvm -o gamma   # Identifier=gamma
```

The artifact for a given closure therefore depends on what it was called when it
was built. (Renaming the file afterwards changes no bytes at all, so it neither
restores nor perturbs the digest — the name is fixed at link time.)

**The Go backend does not do this.** Its identifier is the constant `a.out`,
because the Go toolchain links to its own internal path and moves the result:

```
$ oath build show-from-marker -o g-alpha   # Identifier=a.out
$ oath build show-from-marker -o g-gamma   # Identifier=a.out
```

A constant identifier only shows the name is absent from *that field*, so the
negative is established separately, by isolating the output name with both other
causes held still — the real emitted source, one fixed build directory,
`-trimpath`:

```
$ cd fixed && go build -trimpath -o alpha . && go build -trimpath -o gamma .
19c5c4d2a7622b75011876ac105a7503a674a5d76d0a0623f268a4676ae510ed  alpha
19c5c4d2a7622b75011876ac105a7503a674a5d76d0a0623f268a4676ae510ed  gamma
```

Byte-identical: the output name is absent from the Go artifact entirely, not
merely from its identifier. The control that the comparison discriminates at
all is the same source built in a *different* directory without `-trimpath`,
which differs.

**And cause 3 is repairable, contrary to the first version of this table.**
Linking to a constant basename and moving the result — exactly the trick Go's
own toolchain uses — gives a stable artifact under any final name:

```
$ clang -O1 -o p.c-dir/oath-artifact p.c && mv p.c-dir/oath-artifact alpha
$ clang -O1 -o p.c-dir/oath-artifact p.c && mv p.c-dir/oath-artifact gamma
03b85406885812b305051b029ef842df32a8d3c7d7de62cf79e6d6493887f07d  alpha
03b85406885812b305051b029ef842df32a8d3c7d7de62cf79e6d6493887f07d  gamma
```

`Identifier=oath-artifact` in both, and `codesign --verify --strict` still
passes after the move — so the remedy costs nothing in signature validity. The
same two files linked directly as `-o c-alpha` / `-o c-gamma` differ, which is
the control that this comparison detects the effect it is claiming to remove.

**How this is measured is the point, and the obvious test is worthless here.**
Comparing `digest(alpha)` against `digest(gamma)` looks like the natural check
and cannot fail for the reason its label gives: every Go build already differs
from every other Go build (cause 2), and either emitter may reorder itself
(cause 1), so that comparison reports "differs" even when the filename has no
effect whatever — which is exactly the case for the Go backend. The script
therefore asserts the **embedded identifier**, which is immune to both
confounders, with a control that rebuilding the *same* name yields the *same*
identifier.

That correction is not cosmetic: the first version of this record claimed cause
3 applied to both backends, on the strength of a digest comparison that was
measuring cause 2. Reading the mechanism out of the artifact refuted it.

## What the host signature does, and where it stops

Relevant to #116's question about *where* a signature could live — in
particular "an appended section", which the issue notes would break code
signing. Measured, both backends:

| artifact | `codesign --verify --strict` | executes? |
|---|---|---|
| as built | PASS | yes |
| one byte flipped | FAIL | Go: killed (rc 137); LLVM: **ran normally** |
| one byte appended | FAIL | **yes, both** |

Two things worth carrying:

- **Appending bytes breaks `codesign` and does not stop the program.** So an
  append-the-signature scheme would produce artifacts that run fine and fail
  the platform's own verification — the worst of both, and it is why the issue
  lists a detached file and a registry record beside it.
- **Execution is not a tamper detector.** The flipped LLVM artifact ran and
  produced correct output; the byte landed somewhere the loader never validated
  and the program never reached. `codesign` caught it, running it did not. A
  single flip is not guaranteed to be observable at runtime, which is why the
  script asserts the `codesign` rows and only *reports* the execution rows.

## Controls

The comparison used throughout is "do these two SHA-256 digests differ", and a
comparison that cannot distinguish a mutated artifact from an untouched one
would report "identical" everywhere — which is indistinguishable from a
perfectly reproducible build. So the controls run first and are asserted:

- **Identity** — an untouched `cp` of each artifact must compare *identical*.
- **Detection** — a flipped byte and an appended byte must each compare
  *different*, in both backends, and must each fail `codesign --verify --strict`.

Both directions are needed: identity alone is satisfied by a comparison that
never reports a difference, detection alone by one that always does.

**Every assertion class was verified by fault injection**, in a temporary copy
run from the real directory and deleted afterwards — not by reading it:

| injected fault | expected | observed |
|---|---|---|
| mutator made a no-op | detection checks fail, identity still passes | exit 8, exactly those 8 checks failed |
| `gamma` overwritten with `alpha` | filename check fails | exit 4, both backends' rows failed |
| identity copy corrupted | identity check fails | (same run) both backends' rows failed |

The filename rows were rewritten after that injection run, so their fault
injection is the rewrite itself: asserting the identifier against the basename
made the **Go** rows fail immediately (`want [alpha gamma] got [a.out a.out]`),
which is how cause 3's true scope was found. A check that had passed for both
backends failed for one the moment it was pointed at the mechanism.

The mutator carries its own guard for a reason: Python's `open(dst,'wb')`
creates mode 0644, so the first version of the flip test dropped the execute bit
and reported `rc=126 permission denied`. That reads exactly like *the kernel
rejected the tampered artifact* and is in fact *this script made the file
unrunnable*. The script now `chmod +x`es the mutants and asserts the bit is set.

## What this does NOT establish

- **One program, one host, one toolchain.** `show-from-marker` is the corpus
  here. Only **cause 1** is host-independent — it is Go map iteration inside
  `oath` itself, so it holds wherever that code runs. Cause 2 is a property of
  the Go toolchain and cause 3 of the Mach-O ad-hoc signature; neither is
  claimed beyond the host measured. Even for cause 1 the *rates* above (roughly
  10–17% outliers) are a property of this program's closure shape — a program
  with more unordered sibling pairs has more reachable orderings, and one with
  none would look deterministic.
- **Cross-machine reproducibility is untested**, and it is the property
  attestation actually needs. Everything here varies *one* machine against
  itself. A different Go/clang version, SDK, or architecture will produce
  different bytes for reasons this experiment never provoked.
- **No claim about ELF or any non-macOS host.** Finding 3's mechanism is the
  Mach-O ad-hoc signature; the equivalent question on Linux is open.
- **Nothing about whether a signature is the right design**, which is #116's
  actual subject. This only measures whether the thing a signature would cover
  currently holds still.

## A correction to the measurement that prompted this

The initial observation was that LLVM artifacts were byte-identical across
distinct `cwd`/`TMPDIR` paths while Go artifacts differed in 658,810 bytes. The
first half reproduces under those conditions and the second is real, but three
attributions did not survive being factored:

- **LLVM is not reproducible either.** Byte-identical across a four-cell
  cwd×TMPDIR matrix was a *sample*; over more rebuilds it takes two values
  (cause 1). Holding the environment fixed was never the operative control.
- **`cwd` and `TMPDIR` are not what makes Go builds differ.** Two builds with
  both held identical still differ, because the random `MkdirTemp` suffix is
  redrawn per build. The environment does not have to change at all.
- **The differing-byte count is not a meaningful magnitude.** It does not scale
  with the size of the cause: two build directories of *equal* length differ in
  671,377 bytes while two of *different* length differ in 481,120, at identical
  file size. It is a hash avalanche through the build ID, so the honest
  statistic is identical-or-not, and this record does not quote byte counts as
  evidence of severity.

Two confounds were mine, and both are recorded because both are easy to repeat.

The first matrix built each cell to a *different output filename*, which made
all four LLVM cells differ and briefly looked like it contradicted the original
result. It was cause 3, not a reproducibility difference. Holding the basename
fixed restored byte-identity across the matrix — and turned the confound into
cause 3 itself.

The second survived longer and was caught in review, not by me: cause 3 was
first *asserted* by comparing two artifacts' digests across two output names,
which passed for both backends and was recorded as applying to both. That
comparison cannot fail for the reason its label gives, because causes 1 and 2
already guarantee two builds differ. Asserting the embedded identifier instead
refuted the Go half immediately. The same shape as the first confound —
a digest difference read as evidence for whichever cause was in mind — and the
repair in both cases was to observe the mechanism rather than the difference.

## Method note

Causes 1's per-source measurements required capturing the generated text, which
the CLI does not expose (`compile.go:69` and `llvm.go:2089` both `defer
os.RemoveAll(tmp)`). They were taken by patching a **scratch copy** of `oath/`
to write `src`/`ir` to `$OATH_DUMP_SRC` before the temp directory is created,
and building that copy — the repository source was not modified, and
`git status` was clean afterwards. The `-trimpath` measurement was taken the
same way. Those patches are measurement scaffolding and are deliberately not
checked in; the script in this directory measures the artifacts only, which is
what it can do through the real CLI.

## If this is repaired

All three causes have a proposed repair. **Two of them were measured; cause 1's
was not**, and the table says which is which rather than letting "there is a
fix" stand in for "the fix was shown to work". **Nothing in this experiment
applies any of them** — a change to emission order moves every compiled
artifact's bytes, and this record exists to say what that would and would not
buy, not to spend it.

| cause | repair | measured effect |
|---|---|---|
| 1 | sort the dep keys before recursing, in both emitters | not measured (would need the change) |
| 2 | pass `-trimpath` to `go build` | 12 builds → 1 digest, bar cause 1's outlier |
| 3 | link under a constant basename, then move | byte-identical under any final name; signature still valid |

The fix for cause 1 is already written in this repository, at
`oath/program.go:881-890`: collect the dep keys into a slice, `sort.Strings`,
then recurse — which keeps the "deps first" post-order guarantee while making
sibling order total. It would need doing twice, once per backend, or once by
giving both a single ordering seam to share — and that choice is the interesting
one, since three closure walks with one of them correct is what produced this
defect.

**Ordering matters if they are done separately: cause 1 is the only one that can
hide the others.** While emission order varies, no measurement of causes 2 or 3
is stable enough to confirm its own repair — which is how this record's first
draft attributed cause 3 to a backend that does not have it.
