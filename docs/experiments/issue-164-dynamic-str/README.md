# #164: building a `Str` at runtime in the LLVM backend

The LLVM backend folded a `Str` constructor chain of `Int` literals to a
compile-time constant and refused every chain that did not fold
(`reasonDynamicStr`). So **emitted IR never lowered an Oath `Str` constructor to
runtime construction**, and #158 measured what that bounds the backend to:

> programs whose every result is a string LITERAL or a SUFFIX of an input.

Such a program can search and echo. It cannot report. No `"missing key: " ++ k`,
no counts. This directory records the change that lifted it, and the two things
that had to be settled before any code was written: what the trigger now is, and
what happens to a codepoint the compiler cannot see.

## Part 1 — the falsifier is gone, and its absence is the finding

#166 offered a cheap falsifier against itself and it was run first and lost.
**#164 has no equivalent, and the reason is worth recording rather than
skipping.**

The trigger as `CLAUDE.md` carried it was a demand test: *a program someone WANTS
that needs construction — not an argument that it would be nice.* The issue's
latest comment retires it, and the argument is structural rather than a change of
mind:

> a backend that cannot build a string cannot host the consumer whose demand the
> trigger asks for.

The trigger was satisfiable only by doing the work, which makes it not a trigger.
It was correct when written, under a framing in which the LLVM backend was a
first slice built to test #114's seam claim and was on nobody's path. #115's
scope line reads *MLIR/LLVM instead of emitting Go* and `compile.go` calls itself
*the first rung of #13*; under that reading the Go backend is the staged step and
this one is the intended production path, so a missing feature here is on the
critical path rather than waiting for demand. Same circularity #158 was filed to
break, one layer down.

The comment also fixes the ORDER — this is second, behind #166 — and gives the
reason as **forced before free**: `Int` is ℤ because the prover's `Int` is
unbounded, so #166 was forced by the language's semantics, whereas the
representation of a runtime-constructed `Str` is a free choice this backend gets
to make. That ordering was followed; #166 landed first.

So "no falsifier was run" here is not a corner cut. The item that would have been
falsified — *is the constraint binding?* — was answered by #158's measurement,
and the demand-shaped falsifier that replaced it was withdrawn on the issue as
circular. What remains is a design question, and it is Part 2.

## Part 2 — the disposition, derived rather than invented

A literal chain has its codepoints checked at COMPILE time and refuses
`reasonStrElementRange` rather than substituting U+FFFD. A runtime-constructed
`Str` cannot have that check at compile time. So: what happens when a runtime
codepoint is negative, a surrogate, or above `0x10FFFF`?

**The compile-time refusal does not transpose, and neither does the
interpreter's acceptance.** SPEC §3 settles it, and settles it in both
directions:

> **CONSTRUCTION IS UNCHECKED, and a KERNEL MUST NOT reject a non-scalar element
> at construction.** `(SCons -1 (SNil))` is an ordinary value of the semantics in
> this section.

and, in the same section:

> **That obligation binds kernels, not compiled backends.** A backend […] that
> stores it as packed UTF-8 performs PACK at the moment of construction — so
> refusing `(SCons -1 …)` there is the PACK obligation discharged early, not a
> second semantics. […] What a backend MUST NOT do is substitute.

Verified against the interpreter rather than read off the page:
`oath eval` on `(SCons -1 (SCons 55296 (SCons 1114112 (SNil))))` returns the
value and exits 0.

So **PACK moves to run time; it does not disappear.** The literal case still
discharges it at compile time — cheaper, and a failed build beats a process that
exits 70 — and `o_str_cons` discharges the rest at construction. `oath eval`
remains the reference and is ALLOWED to disagree here: it builds what this
backend refuses to carry. **A refusal is never evidence that a value is illegal
Oath**, only that this backend does not carry it.

The three classes are refused SEPARATELY, and that is not cosmetic. What
`string(rune(n))` used to do was turn `-1`, `55296` and `1114112` into the same
three bytes: three distinct values, one encoding, no constructor inverse.
Collapsing the three DIAGNOSTICS would be the same defect relocated from the
output into the error text, so the gate asserts they stay distinguishable.

## Part 3 — the lowering

`strLiteral` still folds a fully literal chain, and `nonScalarStrElement` still
scans the literal elements and refuses a bad one at build time. What changed is
the fall-through: a chain that does not fold now lowers instead of refusing.

**Left to right**, as SPEC §3 requires of every constructor: the head's
instructions are emitted before the tail's, so a failure in the head happens
first. Emission order is evaluation order here because neither operand is a
block.

**`nonScalarStrElement` returns a `*big.Int`.** It used to return an `int64` and
answer "not bad" for any magnitude it could not hold, so
`(SCons 99999999999999999999 (SNil))` reached the generic non-constant refusal
and blamed the wrong thing. A magnitude beyond int64 is non-scalar with
certainty — `0..0x10FFFF` fits in a few bits — so it is now reported. The
signature was the whole obstacle: the old one could not name a value it could not
hold.

**A decimal renderer had to be written for the diagnostic.** The runtime is
sign-magnitude base 2³² and had no way to print one, and a refusal that cannot
NAME the element it refused lets two distinct values produce one message.
Repeated division by 10⁹ — nine digits per pass, so `(rem << 32) | limb` stays
inside the 64-bit intermediate everything else in that runtime is written to
respect. Quadratic in the limb count and only ever reached on the way to
`exit(70)`.

**`reasonDynamicStr` is retired, not deleted.** It stays in the vocabulary beside
`reasonIntRange`, because a reason is a published contract term and removing one
silently changes what a caller's `==` means — with a comment saying that a
caller branching on either today is writing a branch that can never be taken.

## Part 4 — the tail is a view, and construction is the first allocating producer

`o_str_tail` returns a pointer INTO its parent's buffer rather than a copy.
`CLAUDE.md` names that as one of three things not to delete, with the condition
written at the line: it is sound because every `Str` buffer is immutable and
outlives the program — literals are IR constants, capability values come from
`getenv`. Runtime construction is the first ALLOCATING producer of `Str` buffers,
which is exactly the change that would invalidate a view if an allocation were
ever reused.

**`o_str_cons` allocates a fresh buffer holding the encoded head followed by a
COPY of the tail's bytes.** The copy is not an optimisation left on the table: a
`Str` value may already be a window onto a buffer another value spans, so a
constructed `Str` must own contiguous bytes of its own. Nothing frees, so both
the new buffer and every view into the old one stay valid for the process
lifetime.

The condition at the line was extended rather than caveated, because **the
condition is on the buffer's LIFETIME, not on where the buffer came from.**

**What the evidence actually reaches, stated narrowly because it is easy to
overread.** `str-held-tail` takes a tail FIRST, builds two more strings, and uses
the tail only afterwards. It witnesses LIFETIME, not the copy: a lowering that
shared the tail's bytes and never wrote them would pass, and would be correct to
pass. What would fail it is any `o_str_cons` that writes into, frees or reuses a
buffer a view already spans — confirmed, see Part 6.

## Part 5 — the evidence

`../issue-158-llvm-subset/str-construction.oath` holds the named cases and runs
from that directory's acceptance script, which #164 asked to be extended rather
than replaced. **67 → 123 checks.** `oath eval` is the reference for both
backends throughout; comparing the two backends alone lets two identically wrong
lowerings agree.

Construction, compared three ways:

| entry | what it rules out |
|---|---|
| `str-report` | `"missing key: " ++ k` — the case the subset could not express at all |
| `str-shifted` | every codepoint is arithmetic on an input codepoint, so the result is in no literal and is not a substring of the input |
| `str-mirrored` | order-reversing, so agreement cannot come from a lowering that hands back a view of its argument |
| `str-held-tail` | a tail held across two later allocations |

Each has a second assertion reading the ANSWER, not just the agreement: three
paths that all returned the argument unchanged would agree. Multi-byte input
(`café`) is covered on two of them, so a byte-versus-codepoint confusion cannot
pass.

The non-scalar disposition is **not** run through the comparator, because the
three paths disagree by design. Each side is asserted on its own terms: the
interpreter ACCEPTS and carries the element in the value; both backends REFUSE,
naming the element; neither substitutes, on either stream; the LLVM artifact
exits 70, names the class too, and prints nothing on stdout; and the four
messages stay four distinct messages.

**The two backends failed differently and it was not papered over**, the same
shape as the zero-divisor block: the Go backend panicked out of `oathStrCons`
and exited 2, the LLVM artifact exited 70. No specification fixes either, so
what was asserted of both was what the LANGUAGE determines — construction FAILS
and NAMES the element — and the exit code was asserted only of the artifact
whose own runtime fixed it.

**That asymmetry is gone, and this paragraph is why the split was left standing
here.** Naming the element and naming the STATUS are separate claims, and only
the first was in this round's scope — widening it would have been momentum. The
work under #167 gave the Go backend's emitted runtime a single `oathRefuse`
door: one stderr line, exit 70, no goroutine dump. So the exit code is now
asserted of both artifacts and the tests above gained that half. What remains
asymmetric is prose: the LLVM runtime spells out why a zero divisor has no
answer, the Go line stops at the condition.

**The fail-closed control did not move, and that is the result.** #166 moved it
three times (`line-count-report` → `line-pair-report` → `int-halved` → the
current `int-negated`) because each landing widened the primitive vocabulary.
#164 widened `Str` construction and no primitive, so `neg` still separates "the
subset grew" from "the refusals were deleted" and needed no replacement. A
control that moves whenever anything lands is not measuring a boundary.

Four focused Go tests in `oath/llvm_str_cons_test.go` cover the same claims
in-process — construction, the runtime refusal by class, the compile-time fold
and literal refusal, and tail-view survival — the third being the control for the
first: lowering EVERYTHING through `o_str_cons` would satisfy every
dynamic-construction assertion while silently retiring the build-time check and
turning every literal into a runtime allocation.

## Part 6 — mutation results

Every mutation reverted and the file re-hashed to its pre-mutation value. All Go
runs used `-count=1`; the test cache will otherwise serve a stale pass and the
mutation measures nothing.

| injected defect | caught by |
|---|---|
| the old `reasonDynamicStr` refusal restored | script fails at setup, by name; Go test fails |
| `o_str_cons` drops the tail copy | construction and tail-view tests, all inputs |
| surrogate check removed | `cons-surrogate` — artifact emitted `\xed\xa0\x80` instead of exiting 70 |
| U+FFFD substituted instead of refusing | 11 of 123 checks, including no-substitution and distinctness |
| `o_str_cons` recycles the consumed buffer | **`str-held-tail` only** — 3 of 123 |
| decimal renderer loses interior zero-padding | `cons-astronomical` renders 65×10²¹ as `65000` |
| `strLiteral` refuses to fold any non-empty chain | the fold test |
| the whole `str-construction` family deleted | **not** the script, which stays green at 67 checks — the family LABELS in `TestLLVMSubsetAcceptanceScript` |

Three of those rows carry more than a tick.

**`o_str_cons` recycling the consumed buffer failed `str-held-tail` and nothing
else.** The construction entries passed under that mutant. So the tail-view entry
is not redundant with them: it catches a class no other check reaches, which is
the justification for its existence rather than an assertion of it.

**Deleting the family left the script GREEN**, at 67 checks — which is exactly
the count before #164 touched it, so the deletion is invisible to any measure of
size and the count floor in the Go test would have waved it through. The label
list caught all five. That is the mechanism working as designed: a count is a
proxy for coverage, and the structural owner of "each family is exercised" is
the labels.

Every figure in this section was DERIVED by running the thing it describes,
including the 67 baseline, which came from the script at the commit before this
work. The first draft of this file carried `65 → 124` from memory and both ends
were wrong — the kind of copied number a code review does not ask about.

**The U+FFFD mutation found a defect in the check written to catch it.** As first
written, the no-substitution assertion grepped stderr alone — but a backend that
substitutes prints its replacement to STDOUT and says nothing on stderr, so the
check could not fail for the reason its label gave. It now reads both streams for
both backends. Found by building the mutant, not by rereading the code, which is
the whole argument for building it.

## What is not established

- **Performance.** `o_str_cons` copies the tail, so building a string one element
  at a time is O(n²). That is the same cost the Go backend pays for its own
  prepend-and-concat and is a representation choice this backend may revisit; it
  is not a semantic commitment, and nothing here measures it.
- **The copy itself.** Part 4 says why: the evidence reaches buffer LIFETIME, and
  a lowering that shared bytes without ever writing them would pass correctly.
- ~~**A diagnostic asymmetry, noted and deliberately not asserted.**~~ **FIXED
  in a follow-up, and the reasoning above was half right.** The Go backend tested
  `IsInt64` before it classified, so past int64 it named the element without its
  class while this backend named both. Declining to pin the WORDING was correct —
  that would redden a build over an improved message. But the gate can pin
  something weaker and more durable: that both backends name the same CLASS for
  the same runtime value. It does now, and it fires on `cons-astronomical` when
  the ordering is put back. The repair in `compile.go` is ordering rather than new
  logic — once the sign is known, a magnitude outside int64 is above 0x10FFFF with
  certainty.

  Worth recording as the general form, because "out of scope" was doing two jobs
  here: the other backend was correctly out of scope for the LOWERING, and the
  AGREEMENT between them never belonged to either backend alone. A cross-backend
  claim has no home inside one backend's issue, which is how it nearly became a
  sentence in a write-up instead of a check.
- **`Rat`, `Float`, `Set`, `Map`, the handler protocol, `http_request` and
  `neg`** all remain refused by name. Nothing here reaches them.
