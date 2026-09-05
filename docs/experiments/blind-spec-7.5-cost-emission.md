# SPEC §7.5 per-attempt cost emission — implementation report

Working root: this directory. Everything below was derived from `docs/SPEC.md`
alone; no other implementation was consulted or is reachable from here.

## 1. What was implemented

**`oathrs/src/cost.rs` (new).** The record and its wire format.

- `CostRecord` — the eight §7.5 members plus an optional `wall_ms`.
- `encode_record` — one JSON object, UTF-8, terminated by exactly one LF. Every
  string member is JSON-escaped, so a detail or reason containing `\n` cannot
  split a record across lines.
- `CostSink` — an open destination; `record()` writes and **flushes** in the same
  call, so the flush completes before control returns to the prover and hence
  before the next attempt begins. Nothing is buffered.
- `read_prefix` / `parse_record` — the consumer side: whole lines only, a
  trailing partial line treated as **absent** rather than corrupt, and **unknown
  members ignored**.
- `strategy` — this kernel's strategy-name constants (see §2.3).

**`oathrs/src/prove.rs`.**

- `run_z3` was split into `run_z3_measured` → `run_z3_inner`, which now also
  returns the solver's own `(get-info :rlimit)` counter and the attempt's wall
  time. The classification, the script bytes, the budgets and the returned
  outcome are unchanged.
- `Prover` gained an optional `cost: Option<&CostSink>` and a single method
  `Prover::attempt(def_hash, pi, strategy, detail, script, budget)`. **Every
  §7.2 property-proof attempt now goes through that one method**, so "one record
  per solver attempt" is a property of the code path rather than of a list of
  call sites someone remembered to annotate. `attempt` returns exactly what
  `run_z3` returned, so a sink can never change a verdict.
- The property index `pi` is threaded through `prove_prop` and every `try_*`
  strategy so a record can be keyed by `(hash, prop)`.
- New entry points `prove_shard_cost`, `verify_sharded_z3_cost`,
  `prove_all_cost`; the existing `prove_shard` / `verify_sharded_z3` /
  `prove_all` are now `…_cost(…, None)`, i.e. one code path with the sink as a
  pure observer rather than a second driver that could drift.

**`oathrs/src/main.rs`.** `oath prove --cost-out <file>`, accepted with
`--shard i/n`, `--verify-shards n`, and the plain unsharded `prove`; refused
with `--merge-shards` (a merge makes no solver attempts). The shard result stays
on **stdout**; cost records go to the **file**; the "N record(s)" summary goes to
**stderr**.

**Tests.** 8 unit tests in `src/cost.rs` (no solver needed) and 7 integration
tests in `oathrs/tests/cost_emission.rs` (skip cleanly without z3). Each carries
a `FAILS IF:` note naming the mutation it detects. The most load-bearing:

| test | what would make it fail |
|---|---|
| `attaching_the_sink_changes_nothing_the_run_reports` | anything in the cost path perturbing the run — stdout is compared byte-for-byte with and without `--cost-out`, with a control proving the emitting run really emitted |
| `budget_is_this_attempts_rlimit_not_the_runs_nominal_one` | writing `z3_rlimit()` (the run's nominal budget) into `budget` for every attempt — the most natural way to get that field wrong |
| `an_aborted_attempt_is_invalid_with_no_verdict_and_no_invented_counter` | substituting `"unknown"` for an abort, setting `invalid: false`, or inventing a `consumed` for a killed process. Has a control run on the *same corpus at the same budget* under the normal wall cap, which records no aborts |
| `a_killed_shard_leaves_a_readable_prefix` | buffering to the end of the shard, or not flushing — the test kills a running shard and asserts the file already had complete records, then reads them |
| `a_trailing_partial_line_is_absent_not_corrupt` | erroring on a truncated tail. Checks *every* truncation offset of a 3-record file |
| `a_malformed_complete_line_is_an_error` | swallowing malformed complete lines, which would make the truncation test above pass vacuously |
| `every_record_is_written_and_flushed_before_the_next_one` | dropping the flush or batching writes — the sink is handed a writer that logs the write/flush sequence |

`cargo build --release` and `cargo test` both pass, with and without
`--no-default-features`. Existing suite: 74 → 82 lib tests, 7 → 14 integration
tests, nothing regressed. No new warnings, no new dependencies.

---

## 2. Everywhere the specification did not determine the answer

### 2.1 The destination is not specified, only constrained

> "Records are written to a destination DISTINCT from the shard result of §7.5
> above, so that consuming either never requires parsing the other."

That is the *only* constraint. §7.5 does not say whether the destination is a
file, a second stream, a file descriptor, or how a caller selects it.

**Chose:** a `--cost-out <path>` flag writing to a file, created or **truncated**.
Truncation is itself a guess — §7.5 says nothing about reopening — and I picked
it so that a consumer reading a valid prefix of a killed run cannot find a
*previous* run's records appended below it. Append would have been defensible
too, and would make the prefix rule ambiguous.

I read "distinct" as *different streams*, not as *the cost emission may not be a
file*. Since the shard result goes to stdout, a file is unambiguously distinct.

### 2.2 Which runs may emit

> "A kernel offering sharded mode MAY additionally emit a COST RECORD per solver
> attempt."

Ambiguous between *permission to emit* and *permission scoped to sharded runs*.

**Chose:** the permissive reading — `--cost-out` also works on the unsharded
`oath prove`. The record shape mentions no shard, §7.5 forbids nothing here, and
the from-empty iteration is the run whose cost is least recoverable otherwise.
**But the records of an unsharded run are not one application of `F`**: the
iteration re-attempts a goal in a later round under a larger lemma state and
emits a second record for it, so a consumer summing them measures the whole
iteration, not one pass. (Observed: a 2-property corpus that emits 2 records
under `--shard 0/1` emits 4 under plain `prove`.) This is documented at
`prove_all_cost` and is a real semantic difference between the two modes that
§7.5 does not discuss.

I also **refused** `--cost-out` together with `--merge-shards`. §7.5 does not
mention this case. A merge runs no solver, so it would produce an empty file,
and an empty emission is indistinguishable from "this campaign cost nothing".
Refusing is a choice; silently emitting nothing would also satisfy the text.

### 2.3 `strategy` and `detail` have no vocabulary anywhere in the specification

This is the largest gap, and it is a gap **by decision**, not by omission. §7.5
requires:

>     strategy    string   the §7.2 strategy that emitted the attempt
>     detail      string   that strategy's own discriminator, "" when it has none

while §7.2, declining to make `prove/attempts.txt` normative, says:

> "It would also mean designing labels, a detail vocabulary, ordering and an
> encoding as an interchange format; the shapes used here exist only in the
> reference implementation and are deliberately unspecified"

So §7.5 makes two members **mandatory** whose content §7.2 explicitly refuses to
define. Nothing in `docs/SPEC.md` names a single strategy label. An independent
implementer can satisfy the *shape* of these fields and cannot possibly agree
with another kernel on their *content*. (§7.5 is at least self-consistent about
the consequence: "§10 does not compare it across kernels".)

**Chose** — invented, and *not* an interchange contract:

| `strategy` | when | `detail` |
|---|---|---|
| `lemma-free` | §7.2 #53 lemma-free first attempt | `""` |
| `direct` | direct proof | `""` |
| `direct-fallback` | §7.2 #50 full-budget direct retry after induction failed | `""` |
| `instantiation` | deterministic instantiation, one constructor subgoal | `binder=<k>,ctor=<c>` |
| `induction` | structural induction, one constructor subgoal | `binder=<k>,ctor=<c>` |
| `lex` | lexicographic induction subgoal | `i=<i>,j=<j>,ctor=<c>` or `…,ctor2=<c'>` |
| `recursion-base` | recursion-induction BASE obligation | `""` |
| `recursion-step` | recursion-induction STEP, one guard group | `group=<n>` |

Sub-choices inside that, each undetermined:

- **`direct-fallback` is a separate strategy name** rather than a second `direct`
  record distinguished only by its larger `budget`. §7.5 gives no rule; a
  consumer keying on `(strategy, detail)` would otherwise merge two distinct
  attempts of the sequence.
- **`detail` grammar** (`k=v` comma-separated) is invented outright.
- Constructor names are the source-level names, not their SMT-mangled forms.

### 2.4 What counts as "a solver attempt"

§7.5 says "per solver attempt" and §7.2 speaks of *attempts* without a boundary
definition. Three cases had to be decided:

1. **A goal outside the provable fragment.** No script is built and no solver
   runs, but §7.2 records it as a valid non-proof. **Chose: no record** — "per
   *solver* attempt", and there was none. A reader who took "attempt" to mean
   "the strategy was tried" would emit one here, with no `consumed` and an
   arguable `verdict`.
2. **§6.1.1's termination-measure probes.** These *do* invoke z3
   (`measure_site_unsat`), so on a literal reading of "per solver attempt" they
   qualify. **Chose: excluded.** They are not "the §7.2 strategy that emitted the
   attempt", and — decisively — the record shape has no way to express them: it
   requires a `hash` **and** a `prop`, and a termination probe belongs to a
   definition, not to a property. This is a gap in the record shape rather than
   only my preference; see §4.
3. **Attempts a strategy discards without tainting the run** (the lemma-free
   first attempt, deterministic instantiation — §7.2 says their failure "must
   never taint the run"). **Chose: recorded, including when they abort**, and
   with `invalid: true` when they do. `invalid` is a fact about the *attempt*,
   not about whether the property was tainted by it. A reader who tied `invalid`
   to the property's §7.2 #72 taint would emit `invalid: false` here.

### 2.5 `consumed` vs `invalid` — read as independent

> "`consumed` … the solver's reported resource counter, or NULL when the attempt
> ended without the solver reporting one (an abort kills the process, and
> inventing a number there would report a measurement that was never taken)"

The parenthetical explains NULL *only* through aborts, which invites the reading
`consumed = invalid ? null : n`. **Chose the independent reading:** `consumed` is
present whenever z3's appended `(get-info :rlimit)` parsed and absent whenever it
did not, regardless of validity.

Consequences, both reachable in this kernel and neither anticipated by the
parenthetical:

- `invalid: true` **with** a non-null `consumed` — §7.2's `canceled`-below-budget
  and `memout` conditions are aborts, and z3 reports a counter for both.
- `invalid: false` with a non-null `consumed` for **`unsat` and `sat`**, not only
  for `unknown`. This needed a real code change: the previous verdict scan
  returned as soon as it saw `unsat`, so the counter had to be extracted before
  it or every *proved* goal would report no cost at all — the exact opposite of
  the section's stated purpose.

`consumed: null` therefore occurs on spawn failure, wall-cap kill, and a wait
error — the cases where no telemetry exists.

### 2.6 Smaller choices, listed rather than smoothed over

- **`prop` is 0-based.** §7.5 says only "the property index within that
  definition". Consistency with §7.2/§7.3's `proven_props` indices and with the
  shard emission makes this near-forced, but it is not stated.
- **Member order** in the emitted object is §7.5's listing order, with `wall_ms`
  last. JSON objects are unordered and nothing depends on it; it is a
  readability choice.
- **The optional wall-clock member is named `wall_ms`.** §7.5 requires it be
  "clearly distinguished" and names nothing. It is emitted whenever the attempt
  ran, and no consumer here reads it — §7.5 forbids requiring it.
- **Numbers are bare unsigned decimal**, never quoted. §7.5 says "integer".
- **Record order is attempt order, and records are not deduplicated.** §7.5
  states no ordering rule and no uniqueness rule. `(hash, prop, strategy,
  detail)` is *not* a key: an unsharded iterating run repeats it across rounds.
- **A write failure after the sink is open is a stderr warning, not a run
  failure** — otherwise the run's outcome would depend on the emission, which
  §7.5 forbids. But **a destination that cannot be opened at all fails the run
  before any attempt**, because an operator who asked for the emission and
  silently got none has no way to notice. §7.5 addresses neither; the asymmetry
  is mine.
- **An empty emission is legitimate** (a shard owning no property-bearing
  definition attempts nothing). `read_prefix("")` returns no records and
  `truncated: false`.
- **A complete line that does not parse is an error**, not a skipped line. §7.5
  says only that a *partial* line is absent; since the flush rule makes a
  complete line a complete record, a malformed complete one is a real defect.

---

## 3. What I could not determine at all

- **Whether the emission is meant to be readable by a second kernel.** §7.5
  writes "**Wire format (normative once offered)**" and mandates eight members,
  yet the content of two of them is undefined anywhere (§2.3) and §7.5 itself
  says §10 does not compare the emission across kernels. I could not decide
  whether the format is intended as an interchange format with two
  deliberately-local fields, or as a purely intra-kernel diagnostic that merely
  fixes its own framing. I implemented it as the latter, which is what makes the
  invented vocabulary acceptable; if the former were intended, this
  implementation cannot be conformant and neither can any other.
- **What a kernel whose solver has no rlimit-style counter should write.**
  `budget` is "the effective rlimit for THIS attempt" and `consumed` "the
  solver's reported resource counter". Both presuppose z3's rlimit model. §7.5
  offers no rendering for a solver that budgets differently, and does not say
  whether `budget` may be null. Not a problem for this kernel; unresolvable in
  general.
- **Whether a record is expected for an attempt that never reaches the solver**
  (§2.4 case 1). I read "solver attempt" literally, but the section's motivation
  — "the cost of a sharded campaign is not otherwise recoverable" — is equally
  served either way, and nothing in the text settles it.

---

## 4. Internal inconsistencies and tensions with the rest of `docs/SPEC.md`

1. **§7.5 mandates fields whose vocabulary §7.2 deliberately declined to
   design.** (§2.3 above.) §7.2's stated reason for keeping `prove/attempts.txt`
   out of conformance is verbatim that designing "labels, a detail vocabulary,
   ordering and an encoding as an interchange format" was not worth doing, and
   that "an obligation no reader could reconstruct from this document is worse
   than none". §7.5 then imposes exactly `strategy` and `detail` as MUST-carry
   members. This is the one place where I had to write something no reader of
   the specification could reproduce, and §7.5 makes it mandatory to write it.

2. **The `consumed` gloss contradicts §7.2's own abort taxonomy.** "(an abort
   kills the process…)" is true of the wall cap and of a crash, but §7.5's own
   `invalid` row defines an abort as "a wall cap, a memout, missing telemetry,
   **any** environmental invalidating condition", and §7.2 classifies `memout`
   and `canceled`-below-budget as aborts — both of which arrive *with* z3's
   counter, from a process that exited normally. The parenthetical's causal story
   ("an abort kills the process") does not hold for the abort class the adjacent
   row defines, and steers a reader toward `consumed = invalid ? null : n`, which
   is wrong in both directions.

3. **The record shape cannot express every solver attempt a §7-conformant kernel
   makes.** `hash` + `prop` key a record to a property, but §6.1.1's
   integer-ranking / measure search also runs the solver, per *definition*, with
   no property index. "A COST RECORD per solver attempt" is therefore not
   satisfiable as written unless "solver attempt" is silently narrowed to §7.2
   property attempts — which the `strategy` row ("the §7.2 strategy") does imply,
   but the headline sentence does not say. Since §6.1.1's search can be a
   substantial share of a run's cost, this materially narrows what "the cost of a
   sharded campaign" means.

4. **Minor:** §7.5 says "Records are written to a destination DISTINCT from the
   shard result of §7.5 above" — "§7.5 above" refers to the shard emission, but
   §7.5 never actually specifies a wire format or a destination for the *shard
   result* either (this kernel's `oath-sharded-verification/v1` on stdout is its
   own invention). The distinctness requirement is stated against something the
   specification does not itself pin down.

5. **Not an inconsistency, but worth recording:** the flush rule and the
   valid-prefix rule are the only part of §7.5's emission that a second
   implementer could reconstruct exactly, and they are stated precisely enough to
   be tested directly — the killed-shard test in `cost_emission.rs` is a literal
   transcription of the section's own motivating scenario. The contrast with
   `strategy`/`detail` is stark, and it is the clearest illustration in this
   section of the difference between an obligation with a wire format behind it
   and one without.

---

# Addendum — against the REVISED §7.5

Everything above is the original report and stands unchanged. This section
covers only the revised text: what each of the four changes did to the findings
it was prompted by, what the revision introduced, and what I still had to infer.

The code moved with the text. `cargo build --release` and `cargo test` pass, with
and without `--no-default-features`: 85 lib tests (was 82) and 16 integration
tests (was 14).

## 5. Did each revision resolve what I raised?

### 5.1 `strategy`/`detail` declared OPAQUE — RESOLVED, and it found a defect

This closes §2.3 and §4.1, and it closes them the right way. My finding was that
§7.5 made two members mandatory whose content §7.2 had explicitly declined to
design, so an implementer had to write something no reader of the specification
could reproduce. The revision does not paper that over — it says the omission is
deliberate, gives §7.2's reason as its own, and replaces the missing vocabulary
with the two requirements that actually matter (DISTINGUISH, STABLE). That is
the horn I would have argued for: declining the obligation rather than inventing
an interchange vocabulary to satisfy it.

**But it did not merely bless what I had already written. It exposed a real
defect in my implementation, which my original report described without
recognising as one.** §2.6 records, as a consumer caveat, that "(hash, prop,
strategy, detail) is *not* a key: an unsharded iterating run repeats it across
rounds". Under the revised text that is not a caveat — it is a failure of the
DISTINGUISH requirement. A property re-attempted in a later round under a grown
candidate set repeats the entire strategy sequence with identical labels over
DIFFERENT script bytes, so the labels do not separate the two attempts and the
emission is not joinable to this kernel's own scripts, which is the stated
purpose of the requirement.

Measured, not argued: on the 4-definition list corpus the unsharded emission had
**8 colliding `(hash, prop, strategy, detail)` pairs** out of 29 records.

Fixed by a pass index — `retry=<n>` appended to `detail`, using exactly the
freedom the revision grants, since the discriminator is opaque and kernel-chosen.
It is 0 and therefore absent on every first attempt, so **the sharded emission is
byte-identical to what it was before**; only the iterating driver, which is my
own permissive extension (§2.2), is affected. The counter is incremented by the
driver at the point it decides to re-attempt, not by the prover, because an
unchanged candidate set is served from the driver's cache without reaching the
solver at all and a pass that makes no attempt must not advance the
discriminator.

Relaxed where I claimed more than the section grants:

- `budget_is_this_attempts_rlimit_not_the_runs_nominal_one` asserted four
  literal label strings. Its control — that the budget spread is BETWEEN
  strategies rather than one strategy varying — is now stated as a property of
  the partition the labels induce, naming none of them.
- The new `strategy_and_detail_distinguish_a_propertys_attempts_and_are_stable`
  asserts the two requirements and nothing else: pairwise-distinct labels within
  a property (in both drivers), and identical labels and budgets across two runs
  of the same inputs. It carries a control that strips the pass index and
  observes the collisions return, so it is testing a live mechanism rather than
  an accident of the corpus. `consumed` is deliberately excluded from the
  stability comparison — §7.5 says it is reported, never compared.
- The module doc of `cost.rs`, the `strategy` module doc, and the test file's
  header now state the OPAQUE/DISTINGUISH/STABLE contract and that the portable
  key is `(hash, prop)`, replacing the old §7.2-quote justification.

### 5.2 `consumed` and `invalid` INDEPENDENT — RESOLVED; no behaviour change, better evidence

This closes §2.5 and §4.2. The parenthetical whose causal story ("an abort kills
the process") did not hold for the abort class the adjacent row defines is gone,
and the replacement states the independence in both directions and names the two
crossing cases. My implementation already read it that way, so **nothing about
the emitted records changed** — including the code change §2.5 describes, which
extracts z3's counter before the verdict scan so a PROVED goal reports its cost.

What was missing was evidence for the direction no test could produce on demand:
an abort that DID report a counter is a `memout` or a below-budget `canceled`,
and neither is conjurable from a test harness. So the derivation is now a pure
function, `cost_record` in `prove.rs`, and
`consumed_and_invalid_are_independent_in_both_directions` asserts all four
combinations of (outcome × counter) directly, including that one. The comment at
the assignment names the forbidden derivation it is not doing.

Two smaller alignments in the same direction:

- `budget_is_this_attempts_rlimit_not_the_runs_nominal_one` now asserts that
  every record with verdict `"unsat"` carries a non-null `consumed` — the
  "a proved goal records its cost" half, on real z3 rather than on a stub.
- `an_aborted_attempt_is_invalid_with_no_verdict_and_no_invented_counter`
  asserts `consumed: null` for a 1 ms wall cap. That was, and still is, correct
  for that abort class, but read as a general statement it is the derivation the
  section forbids. Its doc comment now scopes it explicitly and points at the
  pure test for the other direction.
- `CostRecord::well_formed` says nothing about `consumed`, deliberately, and now
  says why: a well-formedness check that constrained it would reject records the
  section requires a producer to write.

Worth recording because it follows from the independence and is not stated: in
this kernel `invalid: false` with `consumed: null` is also reachable — a `sat` or
`unsat` verdict whose appended `(get-info :rlimit)` did not parse is still a
valid outcome. The revised text permits this; nothing needs to change.

### 5.3 Scope narrowed to §7.2 property-proof attempts — RESOLVED as to §6.1.1

This closes §4.3 and §2.4 case 2. The record shape genuinely could not express a
§6.1.1 measure-search call — it needs a `hash` and a `prop`, and that search is
per definition — and the revision resolves it by putting those calls out of
scope rather than by widening the record, which is the only one of the two that
does not require designing a second key. It also states the consequence for a
reader, that the emission is not the whole solver cost of a run. My §4.3 said
that narrowing was implied by the `strategy` row but not by the headline
sentence; now the headline says it.

My implementation already emitted for exactly that set, and structurally rather
than by convention: every emitting attempt goes through `Prover::attempt`, and
`measure_site_unsat` calls `run_z3` directly. That was previously asserted only
in a comment. It is now witnessed by
`the_emission_is_scoped_to_property_proof_attempts`, over a corpus whose
`countdown` is non-structurally recursive and property-free. Its control is
`countdown`'s analysis verdict: `measure` is reachable only through the §6.1.1
solver search, so the corpus provably makes the excluded calls, and the test then
asserts no record is keyed to that definition.

**It does NOT resolve §2.4 case 1, and it makes that case harder rather than
easier — see 6.1.**

### 5.4 Portable framing vs kernel-local values — RESOLVED

This closes the first bullet of §3, which was the thing I could not decide at
all: whether the format was meant as an interchange format with two
deliberately-local fields, or as an intra-kernel diagnostic that fixes its own
framing. The answer is neither of my two options and is better than both — the
framing is portable and the label values are not, stated as a division rather
than as a global property of the emission, with the portable key named.

I implemented it as the intra-kernel reading, which turns out to be the correct
half for the labels and too weak for the framing. Nothing in the code assumed
cross-kernel comparability, so no defect followed, but the framing obligations
were satisfied incidentally rather than deliberately. The docs and the test
header now state the division positively, and the tests no longer name a label.

## 6. What the revision introduced

### 6.1 The narrowed headline collides with the members that describe a solver call

This is the one place the revision made something worse, and it is a direct
consequence of the fix in 5.3.

The headline now reads "a COST RECORD per §7.2 PROPERTY-PROOF attempt". But
`budget` is "the effective rlimit for THIS attempt", `consumed` is "the solver's
reported resource counter", and `invalid` is "true iff the attempt was an abort".
All three describe a SOLVER call. A §7.2 property-proof attempt that never
reaches the solver — my §2.4 case 1, a goal outside the provable fragment, which
§7.2 records as a valid non-proof with no script built — is now INSIDE the scope
the headline names and has no representable `budget`, no `consumed`, and no
meaningful `invalid`.

Under the previous "per solver attempt" wording that case was cleanly outside and
I said so. The narrowing removed the §6.1.1 problem and created this one, and the
two are different: §6.1.1's calls are solver calls with no property, and this is
a property attempt with no solver call. The record shape cannot express either.

I have not changed behaviour: no record is emitted for such an attempt, which is
what the three member definitions require even though the headline now reads the
other way. If the intent is that only attempts reaching the solver are in scope,
the headline needs the word back — "per §7.2 property-proof SOLVER attempt", or
equivalent — because as written the scope sentence and the member table disagree
about a case both of them reach.

### 6.2 DISTINGUISH does not say over what set

"the values DISTINGUISH a property's attempts from one another" — over which
attempts? One application of `F`? One process? One emission? For a sharded run it
cannot matter: each property is attempted once. It matters for any kernel that
offers the emission on an iterating driver, which the section neither permits nor
forbids (my §2.2), because that is where the repeats live.

I read it as *all attempts appearing in one emission*, because that is the
reading under which the stated purpose — joining the emission to the kernel's own
scripts — actually works, and I changed the implementation to satisfy it (5.1).
The weaker reading, *distinct within one pass of the strategy sequence*, was
already satisfied and would have made the collisions above conformant. Nothing in
the text chooses between them.

### 6.3 STABLE does not say across what

"be STABLE within a kernel". I read this as: the same inputs produce the same
labels within one kernel build, which is what the two-run test asserts. A reading
of "stable across versions of a kernel" would forbid ever renaming a label, and
would make the labels a compatibility surface — close to the interchange contract
the section is explicitly declining. I do not think that is meant, but it is not
excluded either.

### 6.4 Two things I raised are unchanged and remain open

- **§4.4 stands verbatim.** "Records are written to a destination DISTINCT from
  the shard result of §7.5 above" still measures distinctness against something
  the specification does not itself pin down: §7.5 specifies no wire format and
  no destination for the shard result. This kernel's
  `oath-sharded-verification/v1` on stdout remains its own invention, and
  "distinct" remains satisfiable by construction rather than checkable.
- **The rlimit presupposition stands** (§3, second bullet). `budget` is "the
  effective rlimit for THIS attempt" with no rendering for a solver that budgets
  differently and no statement of whether it may be null. The revision moves
  `consumed` closer to an answer — "this specification requires only that a
  kernel report what its solver reports" — but says nothing about `budget`, and
  `budget` is the member with no null permitted anywhere in the text.

## 7. Where I still had to infer rather than derive

Carried forward unchanged from the original report, because the revision touched
none of them: the DESTINATION (§2.1 — still constrained only by "distinct", and
truncate-vs-append still a guess); WHICH RUNS MAY EMIT (§2.2 — the permissive
reading, now with the consequence in 5.1 attached to it); and every item in §2.6
— `prop` being 0-based, member order, the name `wall_ms`, record ordering and the
absence of a uniqueness rule, the asymmetry between an unopenable destination
(fails the run) and a write failure after opening (a warning), and a malformed
COMPLETE line being an error.

New to this round:

- **6.1** — whether a property attempt that never reaches the solver is in scope.
  The headline and the member table now point opposite ways; I followed the
  member table.
- **6.2** — the set DISTINGUISH quantifies over. I chose the stronger reading and
  changed code to meet it. Under the weaker reading that change was unnecessary.
- **6.3** — what STABLE is stable across. I chose within-a-build.
- **`retry=<n>` itself.** The mechanism is derived; the spelling is not, and it
  sits in the opaque field precisely so that no reader ever needs to derive it.

One thing that is no longer an inference and was the largest in the original
report: the `strategy`/`detail` vocabulary. Inventing it is now what the section
asks for, not a gap I filled on my own authority. That is a genuine improvement
in the text, and it is worth being explicit that the improvement came from
DECLINING an obligation rather than from specifying harder — the same move §7.2
had already made, now made consistently in the section that had contradicted it.
