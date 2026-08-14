# #165 — the memory design for the LLVM runtime

**Status: STEPS 1–3 IMPLEMENTED; STEP 4 IMPLEMENTED FOR THE PLAIN HANDLER
ONLY.** The retention invariant and the explicit request arena are in the runtime
and gated, and `(-> Request Response)` is now lowered against that release point:
a serve loop, the SPEC §14.2 boundary, and the arena released after each
response has been serialised.

**`(-> {caps} (-> Request Response))` REMAINS REFUSED, AND THIS DOCUMENT IS THE
REASON.** A capability record is resolved ONCE before the listener binds — that
is #114's invariant and what the launch gate states — so the values in it must
live for the PROCESS. The runtime still allocates everything from the request
arena, which is released after each response, so a record resolved at launch
would be freed by the first request that completes. The two regions below are
designed and only one of them is built; until the program-lifetime region
exists, the capability-first shape is refused by name rather than compiled into
a program whose second request applies a freed closure.

Nothing here is normative and no wire format or SPEC text is proposed —
the design was written down while the evidence for it was fresh, and the parts
that have since landed are marked at the line rather than left to be inferred
from the order-of-work list at the end.

## The dependency runs the opposite way from how #165 filed it

#165 asked whether the fork was even open, and made the answer turn on whether
the handler protocol would ever be in the LLVM backend's scope. **That framing
was backwards, and the code said so at the line.** The comment above
`llvmRuntimeC` **at the time this was written** — since replaced, and quoted here
as the historical reasoning rather than as current code:

> MEMORY IS NEVER FREED. A CLI program runs once and exits, so an arena that is
> only ever reclaimed by process exit is honest for this slice — and stated,
> because it is exactly **the reason the handler protocol is refused below**: a
> long-running server would leak per request.

So the handler protocol was not an independent question whose answer decided
whether memory mattered. **Handlers were refused BECAUSE of the memory model.**
Memory was upstream, and settling it is what makes the handler protocol
available.

**THAT REASON IS NOW SPENT, AND SO IS THE REFUSAL IT SUPPORTED.** Step 3 landed
the arena and its release point, so the per-request lifetime objection no longer
holds: a long-running entry does not leak per request for want of a release.
What remained after that was the LOWERING — no request loop, no
response-serialization boundary to release a request's arena against — which was
*this has not been written here* rather than *this cannot be done here*. It has
since been written, and the plain handler compiles.

**A DIFFERENT LIFETIME OBJECTION IS WHAT NOW REFUSES THE CAPABILITY-FIRST
SHAPE, and the two are worth keeping apart because they read alike.** The first
was about memory a request never gives back; this one is about memory a request
takes away from something that outlives it. A capability record resolved before
the listener binds must survive every request, and the only region this runtime
has is released after each one — so `llvmRefuseHandlerCaps` states a property of
the ALLOCATOR, not an unwritten function. That is *this cannot be done here*,
truthfully, until the region below exists: the missing thing is a structure, and
no amount of lowering supplies it.

## The design

Two regions, both determined by the entry protocol rather than inferred.

    program-lifetime   capability records, required values, string literals,
                       the provenance manifest
    request-lifetime   everything allocated while computing one answer

For a CLI entry that is one request released at exit — which is what `xalloc`
did before step 3 by never freeing at all, so the old runtime was the degenerate
case of this design rather than something it replaced.

**AS IMPLEMENTED (step 3), THE TWO REGIONS ARE ONE.** Everything `xalloc` hands
out comes from the request arena, capability records and required values
included: a CLI program is one request, so a separate program-lifetime region
would be a distinction with no observable consequence and a second lifetime to
keep straight. The split above is what a handler entry will need, not what the
runtime carries today. String literals are IR constants and the provenance
manifest is a linker directive, so neither was ever allocated.

**The release point is after serialisation, and that is what makes it sound.**
A handler is `(r Request) -> Response`. The runtime turns the `Response` into
octets at the boundary; only then is the request arena released. So nothing has
to survive the request — the thing that would have escaped has already become
bytes. **No escape analysis over VALUES is required at all.**

**`Str` tail views survive untouched.** A tail shares its parent's buffer instead
of copying, which was sound before step 3 only because nothing was freed. Under
this design a view points into the arena of the request that created it and dies
with it, so the condition written at that line — *the buffer's lifetime, not
where it came from* — continues to hold. It is now written that way in the
runtime, and the arena frees its blocks rather than rewinding them for the same
reason: reuse would turn a released view into a WRONG ANSWER instead of a
dangling one. The three alternatives all break it:
reference counting (a view holds a pointer into a buffer it owns no reference
to), tracing collection (an interior pointer must keep its whole buffer alive),
and general region inference (the region must be proven to outlive the view).
**That is the property that selects this design over those.**

## The one thing that could break it, and why it cannot today

A capability is the only thing an Oath program hands to the host, so a capability
that KEPT what it was given would hold request memory past the release point.

Measured rather than assumed: `o_cap_emit` opens, writes and `fclose`s inside the
call and returns a fresh `OVal`. `process_env` reads host memory out;
`file_read` reads into fresh memory; `http_request` is refused by this backend.
None retains.

**And it is not left as a rule.**
`TestEmittedRuntimeDeclaresNoStaticStorageForValues` asserts that the emitted
runtime declares **exactly one** object of static storage duration able to hold a
pointer — the arena root, `static OBlock *o_arena_blocks`, permitted on the
timing argument below — and **no other**. A provider therefore has nowhere of its
own to put a retained pointer: retention is unavailable rather than forbidden.
Before step 3 the claim was absolute, and stating it that way now would be
describing the previous runtime.

A batching or asynchronous sink would have to ADD static storage, and that is
what the test sees.

**STEP 3 CHANGED THE INVARIANT FROM A QUANTITY TO A TIMING, AND THE WEAKER
READING OF IT IS THE ONE TO AVOID.** The arena needs an ownership root, and
`static OBlock *o_arena_blocks` is it. That root DOES retain request memory:
every `OVal` and every buffer a request allocates sits in a block reachable from
it. Writing it up as harmless bookkeeping would be the whole argument got wrong —
what makes it different from the slot this test forbids is not what it can hold
but WHEN:

- the root may retain request allocations **for the duration of the request**;
- `o_arena_release` **clears it, then frees**, so nothing is reachable from
  static storage after the release point;
- every **other** static pointer stays forbidden, because nothing clears one.

So the declaration scan is now half the argument. It establishes that the root is
the only such slot, keyed on the exact declaration rather than on the name — a
`static OVal *o_arena_blocks` is a bare slot nothing clears and must not inherit
a permission argued for the root. `TestArenaReleaseClearsItsOwnershipRoot`
carries the other half: a C driver that `#include`s the runtime (necessary, since
the root has internal linkage) allocates, releases, and asserts the root is null,
that a second release is a no-op, and that the arena is reusable; alongside a
structural check that the clear precedes every `free`. That ordering has no
observable consequence today, since nothing runs between, which is exactly why it
is pinned structurally and not claimed as behaviour.

**What that pair does NOT see:** a release that cleared correctly and then walked
the list wrongly — freeing a block before reading its `next` — satisfies every
assertion, because the reads usually still return the right bytes. That class
needs a sanitizer and no gate in this repo runs one.

**WHAT THE TEST ESTABLISHES, STATED AT THE PRECISION IT MEASURES.** It checks
DECLARATIONS: the only object of static storage duration able to hold a pointer
is the arena root, matched on its exact declaration, and no other exists. It does
**not** establish that no capability retains request memory — a
provider could call `putenv(o_cstr(arg))`, which keeps the buffer it is handed,
or pass `arg` to a worker thread, declaring nothing at all. **Neither is
visible to it.** Retention through a libc call or a thread handoff is an OPEN
obligation, needing an audit at the capability boundary, and it is recorded here
rather than left to be assumed covered by a test whose name once implied it was.

Evasion forms controlled: the `static` keyword at
file scope and inside a function, multi-word and struct types, a trailing
comment, and a file-scope declaration with no `static` keyword at all — which C
gives static storage duration anyway.

**THE SCAN IS DEFENCE, NOT THE FIX, AND THE REAL FIX IS TO REMOVE THE SLOT** —
which is still the rule for a VALUE slot, and is exactly why the arena's root had
to earn its exemption on a clearing argument rather than on being useful.
Five review rounds on that one test each found a different way in, and the
pattern is worth more than any of the repairs: every version enumerated a FORM —
column zero, then the keyword, then a single-word type, then IR call syntax —
while the claim quantifies over STATIC STORAGE DURATION and over WRITES TO A
MUTABLE POINTER. A scanner will keep having holes because it is approximating a
population the C language defines.

`o_kept` existed only so the linker could not strip `@oath_provenance`. **That
has been done rather than deferred:** the symbol is now appended to
`@llvm.used`, a directive that allocates nothing and has no setter, and the slot,
its accessor and the test that guarded it are gone. With no mutable pointer of
program lifetime, there is nothing for a provider to park anything in — the class
is removed rather than watched. Verified end to end: a program builds through the
LLVM backend, runs, and `oath provenance` still reads the manifest out of the
linked binary.

**AND THE ANCHOR IS NOT LOAD-BEARING TODAY, WHICH IS ITSELF THE FINDING.** With
the directive removed entirely, the manifest still survives and `oath provenance`
still reads it: clang at the default optimisation level does not strip the
unreferenced global. So the original `o_keep` was guarding against a stripping
that does not currently happen — and it introduced a mutable program-lifetime
pointer in order to do it. Nothing here asserts the anchor works, because
removing it also passes; it is kept because it is the correct mechanism and would
matter under `-O2` or LTO, not because anything measured demands it. A test
claiming otherwise would be measuring nothing.

## What is NOT settled

- **Confinement answers a different question.** It classifies whether a
  CAPABILITY escapes a call — `"confined" | "escapes"` for higher-order
  parameters, `""` for first-order ones because they have nothing to leak. It
  says nothing about whether an allocated VALUE outlives a request, and this
  design does not need it to. Across the corpus only four parameters are marked
  `escapes`, two of them (`leak`, `stash`) deliberate exhibits.
- **Arena growth within a request is unbounded**, exactly as before. A single
  request that allocates without limit still exhausts memory; this design bounds
  lifetime, not size. Blocks are freed rather than rewound at release, so a
  request cannot reclaim its own memory early either — deliberate, since reuse
  is what would invalidate a `Str` tail view.
- **Nothing is measured about cost.** No allocation profile exists because no
  LLVM-compiled program runs long enough to produce one. Step 3 replaced a
  `calloc` per object with a carve out of 64KB blocks; that is expected to be
  cheaper and nothing here measures whether it is.
- **Cross-request sharing is out of scope.** Any future capability that
  legitimately needs to retain — a connection pool, a cache — is a new design
  question and would trip the test above, which is the intended behaviour.

## Order of work

1. **The retention invariant.** DONE, and deliberately first: it is the piece
   that becomes harder to add the more capabilities exist, and it held at the
   time by accident of how three functions are written rather than by anything
   stated.
2. **This document.** DONE.
3. **Make the current arena explicit** — name the scope and its release point,
   changing nothing observable, so the release point exists where a handler will
   need it. **DONE.** `xalloc` carves from blocks held on a single ownership
   root; `o_arena_release` clears the root and frees the list; the emitter places
   the call after `o_print` and before `main` returns. Zero-initialisation is
   preserved by the carve-forward-and-free invariant rather than by a `memset`,
   and alignment is derived from `_Alignof(max_align_t)` rather than assumed.
   The order in the emitted `main` is witnessed, not documented — see
   `TestEmittedMainReleasesTheArenaAfterSerialising`.
4. **The handler protocol in the LLVM backend**, which is what this unblocks.
   **DONE FOR `(-> Request Response)`; NOT DONE FOR THE CAPABILITY-FIRST SHAPE,
   and the second half is a REGION that does not exist rather than code nobody
   has written.**

   What landed: a dependency-free HTTP/1.1 serve loop in the emitted runtime,
   the SPEC §14.2 transformation, and the arena released after each response has
   been serialised — the release point step 3 built, used at the boundary it was
   built for. A runtime refusal inside a handler becomes one stderr line and a
   500 rather than an exit, which SPEC §14.2 requires and which the arena is
   what makes cheap: unwinding abandons every intermediate frame, and a runtime
   that freed per value would leak exactly what a refused request allocated.

   What did not: `(-> {caps} (-> Request Response))`. A capability record is
   resolved once before the listener binds, so its values need PROGRAM-LIFETIME
   storage; everything here comes from the request arena, which is released
   after each response, so the first completed request would free the record and
   the second would apply a freed closure. Building the program-lifetime region
   of the design above is the work, and per-request resolution is not a
   substitute — it would move authority provisioning after the port is bound and
   destroy the property the launch gate exists to state.

## A note on the instrument, because twelve rounds is data

The declaration scan took twelve review rounds, and every one found a different
C form: column zero, then the `static` keyword, then multi-word types, then a
trailing comment, then a cast in the initialiser, then a parenthesised
declarator, then `_Thread_local`, then file-scope objects with no keyword at
all. Brace-depth tracking was then tried to replace the indentation heuristic
and made things worse — multi-line function signatures and block comments both
read as file-scope declarations to a line scanner, so the fail-closed default
became noise.

**The scanner was reverted to indentation with its gap NAMED** — a file-scope
declaration written with leading whitespace is missed, and the emitted runtime
writes none. A known narrow gap is worth more than an unbounded one, and a
scanner is not a C parser however many rounds are spent pretending otherwise.

That is the argument for the structural fix having been the right move rather
than the twelfth repair: **the slot is gone, so the class it created is gone**,
and what remains is defence over the rest of the runtime with a stated limit.
