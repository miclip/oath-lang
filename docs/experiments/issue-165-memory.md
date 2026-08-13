# #165 — the memory design for the LLVM runtime

**Status: DESIGN ONLY.** Nothing here is implemented, nothing is normative, and
no wire format or SPEC text is proposed. It exists so the shape is settled while
the evidence for it is fresh, and so the one invariant it depends on is enforced
now rather than discovered to have been broken later.

## The dependency runs the opposite way from how #165 filed it

#165 asked whether the fork was even open, and made the answer turn on whether
the handler protocol would ever be in the LLVM backend's scope. **That framing
was backwards, and the code says so at the line.** Above `llvmRuntimeC`:

> MEMORY IS NEVER FREED. A CLI program runs once and exits, so an arena that is
> only ever reclaimed by process exit is honest for this slice — and stated,
> because it is exactly **the reason the handler protocol is refused below**: a
> long-running server would leak per request.

So the handler protocol is not an independent question whose answer decides
whether memory matters. **Handlers are refused BECAUSE of the memory model.**
Memory is upstream, and settling it is what makes the handler protocol
available.

## The design

Two regions, both determined by the entry protocol rather than inferred.

    program-lifetime   capability records, required values, string literals,
                       the provenance manifest
    request-lifetime   everything allocated while computing one answer

For a CLI entry that is one request released at exit — which is exactly what
`xalloc` does today, so the current runtime is the degenerate case of this
design rather than something it replaces.

**The release point is after serialisation, and that is what makes it sound.**
A handler is `(r Request) -> Response`. The runtime turns the `Response` into
octets at the boundary; only then is the request arena released. So nothing has
to survive the request — the thing that would have escaped has already become
bytes. **No escape analysis over VALUES is required at all.**

**`Str` tail views survive untouched.** A tail shares its parent's buffer instead
of copying, which is sound today only because nothing is freed. Under this
design a view points into the arena of the request that created it and dies with
it, so the condition written at that line — *the buffer's lifetime, not where it
came from* — continues to hold. The three alternatives all break it:
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

**And it is not left as a rule.** `TestEmittedRuntimeDeclaresNoMutableFileScopeState`
asserts the emitted runtime declares no mutable file-scope state, so a provider
has nowhere to put a retained pointer — retention is unavailable rather than
forbidden. The single exception is named: `o_kept` holds a pointer to
`@oath_provenance`, a static constant, so the linker cannot strip the embedded
manifest, and the assertion requires it to stay `const`.

A batching or asynchronous sink would have to ADD static storage, and that is
what the test sees.

**WHAT THE TEST ESTABLISHES, STATED AT THE PRECISION IT MEASURES.** It checks
DECLARATIONS: no object of static storage duration exists that could hold a
pointer. It does **not** establish that no capability retains request memory — a
provider could call `putenv(o_cstr(arg))`, which keeps the buffer it is handed,
or pass `arg` to a worker thread, declaring nothing at all. **Neither is
visible to it.** Retention through a libc call or a thread handoff is an OPEN
obligation, needing an audit at the capability boundary, and it is recorded here
rather than left to be assumed covered by a test whose name once implied it was.

Evasion forms controlled: the `static` keyword at
file scope and inside a function, multi-word and struct types, a trailing
comment, and a file-scope declaration with no `static` keyword at all — which C
gives static storage duration anyway.

**THE SCAN IS DEFENCE, NOT THE FIX, AND THE REAL FIX IS TO REMOVE THE SLOT.**
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
- **Arena growth within a request is unbounded**, exactly as today. A single
  request that allocates without limit still exhausts memory; this design bounds
  lifetime, not size.
- **Nothing is measured about cost.** No allocation profile exists because no
  LLVM-compiled program runs long enough to produce one.
- **Cross-request sharing is out of scope.** Any future capability that
  legitimately needs to retain — a connection pool, a cache — is a new design
  question and would trip the test above, which is the intended behaviour.

## Order of work

1. **The retention invariant.** Done, and deliberately first: it is the piece
   that becomes harder to add the more capabilities exist, and it holds today by
   accident of how three functions are written rather than by anything stated.
2. **This document.**
3. **Make the current arena explicit** — name the scope and its release point,
   changing nothing observable, so the release point exists where a handler will
   need it.
4. **The handler protocol in the LLVM backend**, which is what this unblocks.

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
