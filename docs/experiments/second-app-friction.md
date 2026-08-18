# The second application's friction: what building the consumer demanded

`apps/github-webhook/report.oath` is the webhook receiver's consumer, rewritten
from `report.sh`'s 55 lines of shell into 666 lines of Oath. This records what
the language and the backends made hard, in the shape of
`docs/experiments/webhook-friction.md` — the demand list the FIRST application
produced.

**It is a demand list, not a complaint and not a plan.** Nothing here is
proposed as work.

## What worked, including the thing predicted to fail

It builds on **both** backends, and Go and LLVM produce byte-identical output on
the same log. That was not expected: the task was dispatched on the premise that
dedup needs a `Set` and group-and-count needs a `Map`, both of which the LLVM
backend refuses by name.

**The premise was wrong, and the reason is worth more than the prediction.**
`Set` in this corpus is `set-empty : Set = (MkSet (Nil [Int]))` — an ordinary
Oath datatype wrapping a list. What LLVM refuses is the NATIVE CONTAINER
REFINEMENT (#13): the compile-time lowering of that datatype into a host hash
map. So the refusal costs *performance*, not *expressiveness*, and a program
using `Set` structurally compiles anywhere.

That looked like the `PROVE OVER THE STRUCTURAL MODEL, RUN OVER A NATIVE
REPRESENTATION` split working as designed, and an earlier draft of this file said
so: that the refusal costs performance and not expressiveness.

**THAT WAS WRONG, AND REVIEW FOUND IT BY GROWING THE INPUT.** The structural
`Set` works on LLVM until it does not:

```
 1000 unique deliveries: go exit=0   llvm exit=0
 3000 unique deliveries: go exit=0   llvm exit=0
 5500 unique deliveries: go exit=0   llvm exit=139
```

139 is SIGSEGV. `set-member` recurses over a list that grows with every distinct
delivery, and the emitted C runtime has a fixed stack where Go's goroutine
stacks grow. Duplicate-heavy or malformed logs of the same size succeed, because
the set stays small — so the failure is a function of DISTINCT deliveries, and
**the event log is append-only, so ordinary use reaches it.**

**So the native-container refinement is not only a performance choice on this
backend; without it the structural fallback has a correctness ceiling.** A
program can be built, verified, proven where the fragment allows, agree with the
other backend on every test log — and still fail on the seven-thousandth
webhook. Nothing in the guarantee ladder is measuring that.

**THE EXIT CODE ABOVE IS NOW HISTORICAL AND THE CEILING IS NOT.** A stack guard
turned 139-and-silence into exit 70 with a diagnostic, and made the same
condition a 500 in a handler instead of a dead process. It did not raise the
ceiling: `docs/experiments/issue-178-ceiling.md` measures the artifact at ~3,950
distinct records on LLVM and ~24,800 on Go, so the demand this section records
stands unchanged. Only the failure's legibility moved.

## The demand: THE ENTRY PROTOCOL CANNOT EXPRESS A FAILING EXIT

This is the one real gap, it was found by the acceptance contract rather than by
inspection, and it is not a backend issue.

`report.sh` exits **1** when the log is malformed. `report.oath` prints the same
refusal and exits **0**:

```
oath consumer,  foreign format: exit=0
shell consumer, foreign format: exit=1
oath consumer,  valid log:      exit=0
```

The compiled CLI entry protocol is `(-> (List Str) Str)`: a program returns a
string, the runtime prints it, and the process exits 0. **There is no term a
program can write that means "print this diagnostic AND exit non-zero".** The
non-zero exits that exist belong to the runtime, not the program:

- **70** — a declared capability could not be provisioned, decided *before* the
  entry point runs;
- **70** — a runtime refusal (division by zero, an out-of-range octet), which
  prints the RUNTIME's message.

So the available workaround is to make a positive rejection count reach a
runtime refusal. That exits non-zero and **destroys the application's own
diagnostic**, replacing `refusing: 1 line(s) … are not oath-gh/1 with 5 fields`
with a division-by-zero message. A consumer that cannot say why it refused is
worse than one that cannot signal that it did.

**Stated as the demand:** a compiled Oath program can compute a refusal, can
describe it, and cannot report it to its caller. Any tool that participates in a
pipeline — `&&`, `set -e`, CI, a Makefile — reads the exit status, so this is
the difference between a program that can be composed and one that can only be
read.

## Consequence, recorded rather than worked around

`report.oath` is committed and correct in its OUTPUT, and is **not wired into
`acceptance.sh` as a replacement for `report.sh`**, because it cannot satisfy
two of that suite's four checks — the two that assert a non-zero exit. Wiring it
in would mean weakening those checks, which is the failure the #156 rule names:
an assertion may be replaced when the contract changes, but the replacement must
pin the new contract at least as tightly.

The shell consumer stays until the protocol can express what it expresses.

## The second demand: `Set` IS INT-ONLY, SO DEDUPLICATING STRINGS NEEDS AN ENCODING

The consumer deduplicates by delivery id, which is a `Str`. `Set` in this corpus
is `MkSet (List Int)` — monomorphic over `Int` — so there is no way to hold a set
of strings. The program therefore has to supply its own injection:

```
(defn str-code [] [(s Str)] Int
  (match s
    ((SNil) 0)
    ((SCons c rest) (+ (+ c 1) (* 1114112 (str-code rest))))))
```

That is bijective base-1114112 numeration: each codepoint contributes `c + 1`, so
digits run 1…1114112 and never zero, which is the standard bijection between
strings and naturals. It is injective by construction rather than a hash — and
that matters, because **a collision would silently merge two distinct deliveries
into one**, turning a dedup into a data-loss bug that no test over realistic ids
would find.

The program swears it rather than assuming it —
`distinct-strings-have-distinct-codes`, with `only-the-empty-string-codes-zero`
and a head-recoverability law beneath it. That is the right response, and it is
worth noticing what it cost: **an application had to design, implement and prove
a numbering scheme in order to use a set.**

**Stated as the demand:** either `Set` becomes polymorphic, or the corpus offers
a proven `Str → Int` injection so that every application needing one does not
invent its own. Two applications inventing two encodings is the outcome to avoid,
and this is the first.

## Smaller friction, recorded without ceremony

- **`readfile` is a capability, so the consumer takes a caps record.** That is
  right, and it means the entry shape is `(-> {caps} (List Str) Str)` rather
  than the plain CLI one — the same shape the webhook needed for HTTP, arrived
  at from a different direction.
- **The provable fragment ends where the parsing begins.** Dedup and aggregation
  pass lambdas to list combinators, which #177 measured as outside the fragment,
  so the definitions doing the real work are `tested` rather than `proven`. The
  laws that DO prove are the ones about refusal and shape, which is a reasonable
  place for the boundary to fall and was not chosen.
