# effects-consumer — demands from composing capabilities

The flywheel's "depend on Oath, don't improve it" exercise, aimed at the CAPABILITY /
effects model (`docs/effects.md`) where every prior consumer round used at most one
authority. The consumer is a MULTI-capability CLI
([`effects-consumer/ingest.oath`](effects-consumer/ingest.oath)): it reads a config path
from the ENVIRONMENT (`process_env`), reads that FILE (`file_read`), and EMITS a record
derived from it (`record_sink`) — three authorities threaded through one verified entry
point, sequenced (env feeds readfile), with the empty-string CALL-failure protocol
branched on after each READ (the terminal `emit` is returned directly). The instrument is
[`effects-consumer/run.sh`](effects-consumer/run.sh), which RUNS the kernel (put/prove),
`oath build`s the program, and executes the native binary end to end (`PASS — 6/6`).

## What is NOT friction — the effects model delivers for MULTIPLE capabilities

Recorded first, because the corpus witness (`main-fetch`) proved the single-capability
case and the open question was whether the model composes. It does:

- **Verification quantifies over all worlds AT ONCE.** `ingest`'s three properties PROVE
  3/3 — the generator supplies every `env`/`readfile`/`emit` table simultaneously, so
  `emits-file-body` (the end-to-end contract) holds for every possible triple of worlds.
- **Confinement tracks a three-capability record.** The `{emit, env, readfile}` record
  verdicts `cap: confined`; no capability escapes, checked as one unit.
- **The build wires all three and states the invariant.** `requires: emit (record_sink),
  env (process_env), readfile (file_read)` — each resolved before launch or the program
  exits 70. Provenance lists all three requirements, derived from the type.
- **`record_sink` ALREADY separates its channel, fails, and provisions.** Measured
  (check 5): with `OATH_EMIT_PATH` set, records go to that FILE while the CLI result
  stays on stdout, and an unwritable sink path is a PROVISION failure that exits 70
  before the program runs. That a call-time WRITE failure returns `""` (like the reads)
  is decidable from the provider source (`compile.go`: `WriteString … err != nil →
  oathCapFailure`), not exercised by the runner — it needs an open that succeeds at probe
  then fails at write, which is not portably constructible. The stdout-and-`"ok"` default
  (unset `OATH_EMIT_PATH`) is a convenience for the unconfigured case, not the only
  behaviour.

**A withdrawn demand, recorded so it is not re-filed — and the error that produced it.**
The first draft of this round made "separate the sink from the CLI return, and let emit
fail" its headline demand, from reading only the stdout FALLBACK branch of the `emit`
provider (`oath/compile.go`). The `OATH_EMIT_PATH` branch — a separate file, a `""`
failure value, and provision-or-exit-70 — was right below it and already does both.
Reading the first branch of a provider and reporting its limits as the capability's
limits is the same "implementation limit reported as a fact" error the project keeps
correcting; the whole provider, and its environment-configured paths, is the unit to read.

So *purity-is-visible*, *verify-over-worlds*, *provision-once-or-exit-70*, and a
*separable, failable sink* all hold under composition. The demands below are the
genuine remainders: one semantic (the reads' failure value), two ergonomic.

**Evidence class:** the 3/3 proof, the `cap: confined` verdict, the three wired
requirements, the end-to-end run, and the configured-sink separation + exit-70 are
MEASURED by `run.sh` (checks 1–5).

---

## 1. The reads' `""` failure value is LOSSY — absent and empty are one value — DEMAND

**Headline, and the one genuine SEMANTIC gap.** `env` returns `""` for BOTH an unset
variable and one set to `""` (`os.Getenv`); `readfile` returns `""` for BOTH a missing
file and an empty one (an `os.ReadFile` error and a zero-byte read are indistinguishable
downstream). Measured (check 6): pointing `ingest` at a MISSING file and at an EMPTY file
yields the identical result — it cannot tell an operator which. Distinguishing "no
config" from "empty config" is exactly the kind of thing a real ingest tool must do.

Unlike the sink (which has `OATH_EMIT_PATH` and a real failure channel), the READ
capabilities have only the one lossy value. `effects.md` names the uniform
`(-> Str Str)`-with-`""` contract as a v0 slice; this consumer measures its concrete
cost on the reads specifically.

**DEMAND: a distinguishable failure result for the read capabilities** — a sum type
(`Present Str | Absent`), or a separate presence query — so "absent" and "empty" are
different Oath values a program can branch on. The test is that `ingest` can report
"no such config file" separately from "config file is empty."

**Evidence class:** the provider source (`os.Getenv`, `os.ReadFile`→`oathCapFailure`) is
DECIDABLE; the missing-vs-empty indistinguishability is MEASURED (`run.sh` check 6).

---

## 2. No named / aliased capability-record type — RESOLVED (type aliases)

**Was ergonomic friction, now shipped.** Oath types were structural and inline with no
type alias, so the capability record
`{emit (-> Str Str) env (-> Str Str) readfile (-> Str Str)}` was spelled out FOUR times
in `ingest.oath` — once in the signature and once in each property binder.

**RESOLVED — a `(type Name ty)` alias form.** `ingest.oath` now names the record ONCE
(`(type Cap {emit … env … readfile …})`) and refers to `Cap` in the signature and every
property binder. The alias is IDENTITY-TRANSPARENT surface sugar: it expands to the same
canonical type before hashing, so the aliased `ingest` hashes IDENTICALLY
(`#d2ffe2f12947`) to the earlier version that spelled the record out four times — no
object, no journal entry, no SPEC §1 identity change (recorded in SPEC §1.4 as OPTIONAL
sugar, like the list/string-literal sugar). An alias is batch-scoped and GROUND, and may
not shadow a builtin, a data type, or an earlier alias.

Aliases are a `put`-time source convenience. `oath publish` and `oath resolve` — the
paths that rewrite source into qualified names, or classify external dependencies by
surface name — REFUSE a `(type …)` form with guidance to expand it inline, because a
batch-local, identity-less alias has no place in those pipelines. Since expansion is
identity-transparent, the published/resolved objects are identical either way, so this
is a source-shape restriction, not a lost capability.

**Three SCOPED remainders, all clean follow-ups on the same mechanism.** (1) Parametric
aliases for the generics case: the dictionary parameter in `generic-consumer` is
`{eq (-> a a Bool)}`, whose `a` is a type VARIABLE of the using definition; a ground
alias cannot capture it, so naming it needs `(type Eq [a] {eq (-> a a Bool)})` used as
`(Eq Int)`. (2) Alias-aware `oath publish` and (3) `oath resolve` — both need external
dependencies tracked by hash rather than by surface name to carry aliases safely. The
capability-record case (ground, via `put`) is what this consumer measured and what is
now resolved.

**Evidence class:** the identity-transparent expansion is covered by
`oath/type_alias_test.go` (alias vs inline → identical hash) and demonstrated in
`ingest.oath` (same `#d2ffe2f12947` as before); the parametric remainder is decidable
from the generics source.

---

## 3. Sequencing effectful calls is a manual nest of `let` + empty-check — FRICTION

Because every read is `(-> Str Str)` with `""` for failure, a pipeline of calls is a
staircase: `env` result → check `""` → `readfile` → check `""` → use it. `ingest` is
three levels deep, and each new authority adds another `(let … (if (== x "") <fail> …))`.
There is no short-circuit, `do`-notation, or `Result`-style bind — the composition
ceremony option 3 was chosen (over monadic IO) to avoid reappears by hand at every call
site once a program uses more than one capability. It compounds with demand 1: a richer
failure result would make the branch meaningful rather than just a guard.

**DEMAND (minor): a short-circuiting sequencing form for the failure protocol**, or an
explicit decision that manual threading is the intended discipline. Not a correctness
problem; it grows linearly in capability count and buries the logic.

**Evidence class:** decidable from the source — the nested `let`/`if` staircase in
`ingest`.
