# #158 step 1: is there a useful program inside the LLVM backend's subset?

#158 filed a compiler milestone on a stated premise — *`oath/llvm.go` compiles a
subset **no actual program lives inside***, so the consumer rule cannot break the
deadlock and a milestone is needed to lift `Rat` for `docs/tutorial/circle.md`.

It also carried its own falsifier, and told you to run it first:

> **What would show NO CHANGE IS REQUIRED.** Check this before building
> anything, because it is cheap and it can win. […] Enumerate what `llvm.go`
> accepts today, and try to write a useful program inside it. If one exists,
> close this and file what it revealed instead.

**The falsifier fired.** `show-from-marker` in this directory is a file viewer
that compiles through `oath build --backend llvm`, runs natively, and agrees with
`oath eval` and with the Go backend on every case tested. Nothing was lifted; no
line of `oath/llvm.go` was touched.

```
$ export OATH_STORE=$(mktemp -d)        # NOT the committed store: `put` binds
$ oath put show-from-marker.oath --new  # permanent names, and this is a probe
$ oath build show-from-marker --backend llvm -o view
$ ./view docs/SPEC.md '## 7.2 '
## 7.2 Calls, lemmas, and induction

- Calls must be fully applied. Partial application and over-application are
  outside the provable fragment.
...
```

`acceptance.sh` is the gate. It runs through the real CLI — `oath put`, `oath
build`, `oath build --backend llvm`, `oath eval` — into a scratch `OATH_STORE`,
against a CLI built from the current checkout, and is invoked from
`TestLLVMSubsetAcceptanceScript` in `oath/llvm_test.go` so CI exercises it rather
than leaving it here as a document. That test asserts the script's own summary
line and that the number of checks printed matches the number it claims, so a
harness that dies mid-run cannot read as a pass.

## What the backend accepts, derived from the code

Read off `oath/llvm.go` rather than from its header comment, because the header
is a summary and this is the question the falsifier turns on. Line numbers are at
the time of writing; the reason constants are declared in
`oath/program.go:946-961`.

| term kind | disposition |
|---|---|
| `var`, `bool`, `app`, `lam`, `let`, `if`, `ref`, `self` | accepted unconditionally (`llvm.go:403-506`) |
| `record`, `field` | accepted; a field must resolve on a synthesized record type (`441-466`) |
| `ctor` | accepted for any non-`Str` datatype (`439`); for `Str`, see below |
| `match` | accepted; on `Str` it must have exactly the two arms `SNil`/`SCons`, else `reasonMatchOnStrArms` (`651-654`) |
| `int` | accepted at ANY magnitude, emitted as decimal text; `reasonIntMissing` only when the term carries no value at all |
| `rat`, `float` | always refused, `reasonRatFloat` (`526-527`) |
| `prim` | **eight operations**, all requiring both arguments to synthesize to `Int`: `+ - * / %` and `== < <=`. Everything else — `neg` (unary, so it shares no lowering path), every Rat and Float operation, `==` on `Str` or `Bool` — `reasonPrim`. The type guard is what decides: `+ - * < <=` are numeric-OVERLOADED and `==` is polymorphic, so a Rat addition falls through to the refusal rather than into the Int lowering |
| anything else | `reasonTermKind` (`555`) |

`Str` is the interesting row. A `Str` constructor chain whose heads are all `Int`
literals in the Unicode scalar range **folds to a compile-time constant**
(`strLiteral`, `242-268`) and is emitted with an explicit length so an embedded
NUL survives. A `Str` constructor that does **not** fold is refused —
`reasonDynamicStr`, or `reasonStrElementRange` when the obstacle is a specific
non-scalar codepoint (`429-438`). The consequence is exact: **emitted IR never
constructs a `Str` at runtime.**

> **SUPERSEDED as a statement about the CURRENT backend.** That row, and the
> class it bounds the subset to, is what #164 lifted: a non-folding chain now
> lowers through a runtime constructor, and `reasonDynamicStr` is retired.
> `../issue-164-dynamic-str/` is that record. The finding below stands as the
> measurement it was — it is the evidence #164 acted on — and is read as a
> snapshot, not as the live boundary. `oath/llvm.go` is the authority on what is
> refused today; every line number here is from the time of writing.

> **The `prim` row is superseded the same way, and by #173.** `==` is lowered at
> **two** types now, `Int` and `Str` — the Str instance as a byte comparison over
> the packed UTF-8 — so the operation count and the "both arguments synthesize to
> `Int`" clause describe the backend as enumerated, not as it is. Every other
> named instance is still refused as `reasonPrim`: `Bool`, `Rat`, `Float`, and
> any OTHER datatype — `List`, `Option`, `Pair` — `Str` being a datatype itself.
> The authority is
> `llvmUnsupported`'s help text and the guard beside it, which move together.

Capabilities are not the constraint. `llvmProviders` (`74-78`) supplies
`process_env`, `file_read` and `record_sink`; `capabilityVocabulary`
(`program.go:209-230`) declares four kinds, so the only one absent is
`http_request`. Required values have their own entry point (`85-87`). The handler
protocol is refused outright before any emission (`1292-1297`), so this is a
CLI-entry backend.

## What that adds up to, and it is not what the issue assumed

The issue named `Rat` as the blocker, and the header comment leads with
arithmetic. Neither is the binding constraint. Writing a program inside the
subset, the wall you actually hit is this:

> **A program can COMPARE and SEARCH dynamic strings, but it cannot BUILD one.**

`==` on the `Int` codepoints a `Str` match binds is enough to compare two runtime
strings, so prefix tests, search, splitting decisions and equality are all
reachable — `show-from-marker` compares a marker that arrives from `argv` against
a document that arrives from `readfile`, neither known at compile time. What is
not reachable is assembling a new string. So the expressible class is:

> programs whose every result is a string LITERAL or a SUFFIX of an input.

That is a real and useful class — a viewer, a selector, a filter that answers in
fixed phrases — and it is much larger than "nothing". It is also sharply bounded:
no program in it can *report* anything it cannot *quote*. No `"missing key: " ++
k`, no counts (counting needs `+`), no formatting.

## The follow-up this replaces the milestone with

If the backend is widened, **dynamic `Str` construction is the first thing to
lift, not `Rat`** — it is one runtime function and it converts "select and echo"
into "report", which is where the next demand actually is. Arithmetic is a second
and independent axis. Filed separately; this record is the evidence for it.

Both have since been done, in the order the arithmetic axis first: #166
(arbitrary-precision `Int` and the binary primitives,
`../issue-166-bignum-int/`) and then #164 (runtime `Str` construction,
`../issue-164-dynamic-str/`). The recommendation above was one runtime function
and it was approximately right about the size. `Rat` is still refused.

`docs/tutorial/circle.md` remains uncompilable by this backend, and that is
unchanged and unclaimed. What is refuted is the premise that no useful program
fits, not the observation that `circle` does not.

## Method notes, including one that cost an hour

**The three-way gate is the point.** `oath eval` is the reference; comparing the
two backends alone would let two identically wrong lowerings pass by consensus.
The interpreter has no capabilities, so the file read is supplied to it as an
ordinary function returning the same bytes the compiled programs read from disk.

**A control fired for the wrong reason, and that is the finding worth keeping.**
The script's control feeds the interpreter a *different* file and requires the
comparison to report a disagreement. On the first run it reported one — and the
comparison was nonetheless broken: the interpreter's `Str` decoder was written as
`python3 -` with a heredoc, so the heredoc occupied stdin, the decoder read
nothing, and every expected value was the empty string. The control passed
because "everything disagrees" is what a dead instrument and a working one both
produce when the reference is empty and the outputs are not.

So a control that only checks *a disagreement was reported* does not establish
that the instrument works. The repairs, both now in the script: the decoder is
probed against a known `(SCons 111 (SCons 107 SNil))` before anything is measured
with it, and the passing checks assert *specific* observable output — the usage
line, the not-found message, the first three bytes of the found result — so a
comparison that has stopped discriminating cannot be green.

**The gate was verified by sabotage**, not by reading: replacing the LLVM binary
after the build reports *the LLVM backend disagrees*, replacing the Go binary
reports *the Go backend disagrees*, breaking the decoder stops the run at setup,
and deleting a single check makes the Go test fail on the count. A gate never
observed failing is a hypothesis.

**The fail-closed control is in a corpus file on purpose.** It is one
primitive outside the subset: verified, compiled by the Go backend, refused by
name by the LLVM backend. Without it, a green run would be equally consistent
with the refusals having been removed rather than with the programs having been
written inside them.

**AND IT HAS MOVED FOUR TIMES SINCE, WHICH IS THE PART WORTH KEEPING.** It was
`line-count-report`, which needs `+`, while the backend had only `==`. #166's
checked-`+` prototype moved `+` inside, so it became `line-pair-report`, which
needs `*`. #166's arbitrary-precision `Int` moved `+ - * == < <=` inside, so it
became `int-halved`, which needs `/`; then `/` and `%` were lowered too, so it
became `int-negated`, which needs `neg`; and `neg` was lowered in turn — the
demand came from `show-int` in `examples/circle.oath` — so it is now
`rat-floored`, which needs `floor` over a `Rat`. Each move was forced by the same rule: an
assertion may be replaced when the contract it encodes deliberately changed, but
the replacement must pin the new contract at least as tightly — and a refusal
alone cannot, because it witnesses that something is still outside the boundary
without witnessing that the boundary MOVED. So each move added the positive
half: every operation that came inside is now checked three ways, and the entry
that used to be refused is required to compile and agree. The script has grown
several times over across those moves — its own summary line is the count, and
a number written here would be one more thing to keep in step by hand.

**THE CONTROL'S OPERATION IS NOT A FIXED CHOICE, AND THAT IS THE DURABLE
LESSON.** A fail-closed control has to name something the backend still refuses,
so every time the subset grows the control must move or it becomes a permanent
failure. That is the right direction for it to break — a control that started
passing would be indistinguishable from a working one — but it means the control
is a MAINTAINED artefact, not a written-once one. `neg` is the last Int operation
left to name; when it goes inside, this control has to leave the Int vocabulary
entirely.

Worth noting how the first move was detected. Nobody went looking: lowering `+`
turned the then-control red, and a gate written before the change with no
knowledge of it reported that the subset boundary had moved. That is the control
doing exactly its job.

## Limits of this result

- It is one program. It shows the subset is non-empty and useful; it does not
  measure how much of what people want lives inside it.
- The large-file check compares the two backends only, and says so: a
  multi-kilobyte Oath literal is not the same input as a file by any useful
  definition, so the interpreter is not a reference there.
- `acceptance.sh` needs `clang` and `python3`. Without `clang` it skips locally
  and fails under `CI=1`, matching `requireClang` in `oath/llvm_test.go`.
