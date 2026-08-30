# Friction log: reproducing a TWO-FILE consumer in a store that starts empty

The instrument is [`resolve-consumer/run.sh`](resolve-consumer/run.sh), which
asserts the behaviour behind these findings and exits 0 on **162/162 checks in
about 57 s** (56.5-57.2 s across runs) — a figure that includes building the
kernel from source and linking the consumer with the Go backend, so most of it
is toolchain, not Oath.

It builds `numbered-args : (-> (List Str) Str)` — print the arguments sorted
lexicographically, one per line, numbered from 1 — split deliberately across two
files. `lib.oath` holds four helpers the commons does not have (an `Ord`
dictionary for `Str`, a lexicographic sort, a line renderer, a numbering pass);
`main.oath` holds the entry point, whose three calls come from **both** places.
Eleven corpus operations are reused and every one was located by asking what the
commons can prove, with no name assumed.

[`discovery-consumer-friction.md`](discovery-consumer-friction.md) asked whether
the commons can be **found**. This asks what happens after: whether a consumer
that depends on the commons *and* on code the commons has never seen can be
**reproduced somewhere else**. The split is the whole subject, because it is what
makes the lockfile's dependency set a MIXTURE — corpus names and sibling names
pinned side by side, from two different origins — which a single-file consumer
never produces.

This is the demand list that fell out of doing it. Ranked most valuable first,
which is **not** the order in which the frictions were met.

---

## Provenance, and which figures are asserted

Every number here was produced against the corpus **as of commit `8a379e1`**.
`run.sh` derives what it can live from `HEAD:codebase` — the dependency hashes it
pins, the tree signature it re-checks — but a few figures are **snapshot-pinned
regression expectations** at `8a379e1`, not re-derivations: the shape-match counts
(`shape … 1`) and the lib closure size (`12 external names across 15 objects`) are
written into the assertions, so a corpus whose exhibits change would trip them and
need them updated. They are pins that catch drift, not values that track it; the
prose stays a faithful record of the run it describes.

**Three classes of evidence appear below and they are not worth the same.**

- **Asserted by `run.sh`** — the shape counts, every `--implies` verdict quoted,
  the `NO VERDICT` result and its candidate count, §2's chaining failure and the
  three absences it leaves behind, both refusals in §1, the lockfile name sets
  and pins, all five hashes, and the interpreted/compiled output comparison. A
  regression trips the script.
- **Hand-measured, NOT asserted** — every wall-clock timing in §3 (the ~57 s
  total above included), the `oath prove` outcomes in §6, and the plain-`put` control
  quoted at the end of "What worked". `run.sh` contains no timing assertion and
  never invokes `oath prove`. These are observations from building this.
- **Read from the implementation** — the source citations. Line numbers are at
  `8a379e1`.

**Every measurement is over THIS corpus.** "Five definitions share
`(-> Int Str Str)`" is a fact about the committed exhibits, not about programs.

**Classification, used strictly below.** No **defect** was found: nothing that
exists behaved wrongly. Findings 1 and 2 are **absent workflows** — a mechanism
that does not exist, with nothing wrong about the ones that do. Finding 3 is a
**measured cost** and a prover ceiling. Findings 4 and 5 are **correct results
whose ambiguity is in the question, not the answer**. Finding 6 is a **stated
ceiling**, plus one thing that is simply **unmeasured**.

---

## 1. A lockfile cannot hydrate a store — ABSENT WORKFLOW, and the next demand

This is the sharpest finding of the exercise and the one a third party is most
likely to be hurt by, because it appears at the moment reproduction is attempted
by someone who was not there for the build.

`main.oath.lock` *identifies* everything a rebuild needs: the direct external
names with their hashes, **and** the full transitive object closure
(`oathLock.Closure`, `oath/resolve.go:27-32`). But a closure is a sorted list of
**hashes**, not object bodies, and the lock names no source to fetch them from —
so it identifies the rebuild without containing it. It reads exactly like a
manifest you could hand to a fresh store. It is not one.

```
$ OATH_STORE=$(mktemp -d) oath put --lock main.oath.lock main.oath --new
error: the store cannot resolve this file's dependencies: line 23: unknown type "Str"
                                                                          (exit 1)
$ OATH_STORE=$(mktemp -d) oath resolve --lock main.oath.lock
error: `oath resolve` has no flag "--lock", and silently ignoring it would let this
command do something other than what you asked.
Known flags for resolve: --from --key --remote -o                          (exit 1)
```

Both refusals are correct, and the second is actively good — silently ignoring an
unknown flag would let the command do something other than what was asked
(`oath/main.go:157-158`). The gap is that there is no command to ask instead.

**The two halves of reproduction live in different commands and neither closes the
loop**, which is visible in the source rather than inferred from the symptoms:

- `verifyLock` (`oath/resolve.go:292-296`, called from `oath/main.go:319`)
  recomputes the source's external set **against the target store** and demands
  an exact match. There is no fetch in it. In an empty store the classification
  fails before the lock is even compared — which is why the error names a missing
  `Str`, not a hash mismatch.
- `cmdResolve` (`oath/resolve.go:324`) does all the fetching, and it is driven by
  `computeExternal(elab, src)` — the **source file**, elaborated against a store
  seeded from `--from` or a registry. The closure it fetches is derived from
  elaboration, never read from a lock.

So `put --lock` verifies without fetching, and `resolve` fetches without reading
a lock. A consumer holding the lock and not the source store has a checkable
record of exactly what it should get and no way to get it.

**The workaround is the whole of §2**, and it is asserted: re-`resolve` both files
from the combined source store, and all five hashes come back identical — the
regenerated `main.oath.lock` is byte-identical to the first. So the hashes are
reproducible; it is the *lockfile* that does not reproduce them.

**THE DEMAND: a lock-driven hydration path — one command that takes a lockfile
and an object source, and populates a store to match it.** Nothing new needs to
be computed; the closure is already in the file. Naming it is as far as this log
goes — the shape it should take is a design question, and this consumer is one
data point, not a specification.

**SATISFIED** — built as `oath hydrate <lock> --from <store>` (`cmdHydrate`,
`oath/resolve.go`): it fetches exactly the lock's closure by hash, typechecks it
against only that closure (so an unclosed lock is refused, not silently completed
from the source), and binds the direct names — after which the `put --lock` that
opens this section succeeds. The claim was attacked, not narrated:
`resolve-consumer/hydrate-falsifier.sh` witnesses that the gap is real, the
reproduction is identity-neutral and idempotent, failure paths leave the target
untouched (witnessed by STORE STATE), and the non-empty-target naming edges (#188)
preserve every bound name's constructor vocabulary. A final arm stands up a
throwaway `oath serve` and repeats the core claim over a **registry** object source
(`--remote <url> --key`): the closure is fetched by signed reads, auth is required,
and a hash the registry cannot serve fails without touching the target. Out of
scope, and stated at the command: true I/O-atomicity of the commit phase (the
transactionality is against validation failure, not a mid-commit backend write).

Note what this does *not* say. Reproducibility is not broken: identity is a
function of the closure and that held everywhere it was checked. What is missing
is the ability to reproduce **from the lock plus an object source** through a
single command — the lock names the closure, a source holds the bytes, and today
no command marries the two. That is the form the question takes as soon as the
rebuilder is not the builder.

---

## 2. The `--from` store has to be assembled by hand — ABSENT WORKFLOW

`oath resolve` takes exactly one source: `--from` and `--remote` are mutually
exclusive by construction (`oath/main.go:225-230`). A consumer whose external
names come from two origins therefore needs a single store that already holds
both, and building it is the author's problem.

The natural-looking sequence fails. Resolving `lib.oath` from the corpus produces
a store holding lib's closure and nothing else — **12 external names, 15
objects** — so the corpus operation `main.oath` needs is not in it:

```
$ OATH_STORE=<a fresh empty probe> oath resolve main.oath --from <the library target>
error: line 24: unknown name "join-with"                                    (exit 1)
```

`run.sh` asserts this arm in STEP 5, **immediately after the locked library is
put and nowhere else** — the window is one step wide, because STEP 6 fetches
`join-with` into that store and the failure can no longer be observed. Five
checks: that `join-with` is genuinely unbound in the target beforehand (without
which the arm would prove nothing), that the resolve fails naming the missing
operation, and that it wrote **nothing** — an object store left present and
EMPTY, no `names.json` at all, and no lockfile.

Those absences are asserted as three separate claims rather than one, because
opening a store creates empty `meta/` and `objects/` directories: "the directory
is untouched" would be false, and `ls` of a *missing* directory counts zero
exactly like an empty one. Present-and-empty is the exact claim, and it is the
checkable one.

The failure is clean, then — no half-populated store, no stale lock to mistake
for a good one. And the store it failed against is minimal **by construction**,
correctly so: `resolve` fetches the closure of the file it was given. Nothing is
wrong with either. But it means the output of one resolve is not usable as the
input of the next, and the two-file workflow the tool exists to serve is exactly
the one that wants that.

What `run.sh` does instead, in STEP 4, is the manual assembly:

```
src = git archive HEAD:codebase        # the commons, as committed
oath put lib.oath --new                # + the sibling library
```

and then both resolves point at `src`. It works — 11 checks in STEP 6 confirm the
lock pins corpus names against `HEAD:codebase/names.json` and sibling names
against the library objects, and **neither authority could have supplied the
other's half**: HEAD's `names.json` has never heard of `sort-strs`, and the
library store's binding for `join-with` is only there because `resolve` fetched
it. But assembling it is a step with no tool support and no check on whether you
got it right.

Ranked second because it is entirely worked around by one `put`, and because a
publishing author has such a store already — it is their working store. It bites
the same party §1 bites: someone rebuilding who did not do the authoring.

This is adjacent to, and not the same as, the non-empty-target edge recorded on
`#188`. That one asks whether resolve can write into a store that already holds
objects; this one asks where the *source* comes from. The first is exercised and
worked (see "What worked"); the second is untouched by it.

---

## 3. One law in twelve does not return, and it costs the whole timeout — MEASURED COST

Of the eleven property queries, ten are effectively free. Hand-measured
`--implies` wall clock, once each, on the committed corpus:

```
zip-with 0.03   show-nat 0.06   join-with 0.09   length 0.33   str-len 0.42
str-lt   0.53   range    0.56   str-drop  0.98   sort-by 1.24   str-prefix 1.50
str-append 3.70                                          total ≈ 9.4 s
```

Discovery is not the expensive part of this workflow. One law is.

`(<= (str-len (wanted n s)) (str-len s))` — *dropping codepoints never lengthens
the string* — is true of `str-drop`, is the most natural thing a consumer would
write about a suffix operation, and gets no verdict:

```
$ oath find --implies need-str-drop-monotone.oath --timeout 20s
  · it-never-grows
      1 NO VERDICT — a strategy attempt was environmentally aborted, so no
      negative verdict is valid (SPEC §7.2)
  SEARCH INCOMPLETE — the --timeout of 20s elapsed after 49 candidate checks.
```

**The candidate count does not move with the bound — 49 at 20 s and 49 at 90 s —**
because the first candidate's single strategy consumes the whole budget whatever
it is. So the cost of including this law is the timeout, exactly, and the yield is
zero.

Three things are worth separating here, and only the first is a friction.

- **The cost is real and unavoidable from the query side.** There is no way to
  ask for "the laws that are cheap" and no signal, before running, that one law
  will consume the budget. The law was removed from `need-str-drop.oath` by hand,
  after measuring, and kept in `need-str-drop-monotone.oath` because the failure
  is the measurement. It is ranked third rather than higher because it cost
  nothing: the three laws that *do* return already pin `str-drop` uniquely, so
  the expensive law bought the consumer nothing it did not have.
- **The verdict is reported correctly, and that is not a small thing.**
  `NO VERDICT` is a fact about the prover's rlimit, not about `str-drop`. A
  surface that rendered it as a refutation would make a consumer reject a correct
  dependency. `run.sh` asserts that nothing is claimed satisfied *and* nothing is
  claimed refuted.
- **Why this law and not the other ten** is a plausible explanation, not a
  measurement: it relates two recursive functions over `Str` with no concrete
  unfolding depth to anchor it, where the ten that return are equalities at fixed
  depth. I did not test that hypothesis.

---

## 4. `show-int` and `show-nat` are both right — CORRECT, ambiguous by construction

The rendering need states four laws at concrete numbers (`0`→`"0"`, `7`→`"7"`,
`10`→`"10"`, single digits are one wide). `--implies` returns **both**
`show-int` and `show-nat` as provably satisfying every one, and refutes
`str-spaces`. `run.sh` asserts all three outcomes.

This is not a failure of the query and there is nothing to fix in the surface.
The two definitions differ on **negatives**, and the consumer never supplies one —
its indices come from `(range 1 (+ 1 n))`. The laws say what the consumer
actually requires; requiring more would be writing a specification for a case
that cannot arise.

The friction, such as it is, sits one step later: **the surface reports two
candidates and offers no ranking.** The guarantee column is the only tiebreak
available — `show-nat` is PROVEN, `show-int` is tested (200 cases per property) —
and reading it is the consumer's job. That is arguably correct: the choice is a
consumer's to make, and a tool that picked for you would be hiding the ambiguity
rather than resolving it. Recorded here so that a later reader meeting two rows
does not mistake it for an unranked mess.

Ranked fourth because nothing goes wrong if you read the output.

---

## 5. A single law is a weak query, and the corpus proves it three ways — CORRECT

`--spec` matches by property content hash, so it reports coincidences honestly and
without comment. Three from this run, all asserted:

| the law | what else matched | why |
|---|---|---|
| `the-empty-string-is-zero` | **`str-code`**, and *not* `str-len` | `str-code` is an injective numeric encoding of a string (positional, base 1114112), and the empty string encodes to 0 exactly as it measures 0. `str-len` does not state this law at all. |
| `everything-prefixes-itself` | `str-eq` (proven as `reflexive`), `media-type-is`, `path-is` | reflexivity is shared by every equality-shaped test |
| `dropping-nothing-changes-nothing` | `str-pad-left`, `str-split-join` survive it under `--implies` | dropping nothing is a no-op for a padder and a split-then-rejoin too |

In each case the second law in the same file settles it: `a-codepoint-adds-one`
refutes `str-code` and `parse-nat`; `a-prefix-survives-appending` refutes `str-eq`
and `str-lt`; `dropping-one-drops-the-head` refutes all four rivals and leaves
`str-drop` alone.

The same shape appears in the join query and is the sharpest instance, because
here the coincidence is a *satisfaction* rather than a shape match: `first-or`
**provably satisfies** `a-single-piece-is-itself` and is refuted on the other two.
A consumer who wrote only the singleton law would have been handed a definition
that returns the first element of a list, proven to satisfy their specification.

**Nothing here is a defect and nothing needs a repair.** It is the cost of a
content-hash surface working exactly as designed, and the lesson is about the
QUERY: one law is a coincidence detector. This corpus makes that cheap to learn
because it is small and curated; a larger one would make it expensive.

Related, and the counter-example that keeps this honest: `need-zip-with.oath` is
the one need of eleven the cheap surface settles **outright** — all three laws
matched by content hash, no solver. That happens when the consumer's laws and the
target's are the same sentences, here because both are the defining unfold of a
fold, which has one natural spelling. It is the exception, not the pattern.

---

## 6. The dictionary helpers cannot be proven — a STATED CEILING, and one UNMEASURED gap

Two of the four sibling helpers are refused by the prover, and the refusals are
precise:

```
$ oath prove str-order
· unproven  it-is-str-lt        str-lt must be fully applied
· unproven  it-is-irreflexive   str-lt must be fully applied
proven: 0/2 properties

$ oath prove sort-strs
· unproven  (all four)          str-lt must be fully applied
proven: 0/4 properties
```

`str-order`'s body is the record literal `{lt str-lt}` — a bare function
reference, which the provable fragment does not admit. **Eta-expanding does not
help**, and I checked rather than assumed:

```
{lt (fn [(a Str) (b Str)] (str-lt a b))}
· unproven  "lam" terms are outside the provable fragment
```

So a dictionary-valued definition is outside the fragment either way, and this is
a ceiling of the provable fragment rather than anything specific to this
consumer: the corpus's own `gh-by-count` (`apps/github-webhook/report.oath:478`)
is `tested` for exactly the same reason. `sort-strs` inherits it through
`(str-order)`.

That is the honest cost of the dictionary convention (`docs/generics.md`): a type
class *is* a capability record, records of functions are values, and values
carrying functions do not reach the prover. All four helpers are
`tested (200 cases per property)` and every property passes — a legitimate rung
on the guarantee ladder, and the same rung the corpus's own dictionary
definitions sit on.

**UNMEASURED, stated as such:** whether `number-line`, `number-lines` and
`numbered-args` are provable is **not known**. They contain no dictionary and no
lambda, so nothing refuses them; `oath prove` simply did not return within the
bounds I gave it (>4 minutes each) and I stopped rather than spend the time.
That is a gap in this log, not a finding about the definitions — and `run.sh`
never invokes `oath prove`, so nothing in the instrument covers it either.

Ranked last because it changes nothing a consumer can do, and because it was
known before this exercise started.

---

## What worked

Five things went right that were not guaranteed to, and each is asserted.

- **The second resolve into the same, now non-empty, target succeeded.** STEP 5
  resolves `lib.oath` into a store asserted empty (`ls -A`); STEP 6 resolves
  `main.oath` into that *same* store, which by then holds the library and its
  closure. This is the non-empty-target case `#188` records as unhardened, and on
  this consumer it worked without a special case: *8 external names across 20
  objects*, then `put --lock` accepted and the hash matched.
- **Overlapping closures merged cleanly.** The two resolves share objects — `Str`,
  `List` and `str-append` are in both, and `str-lt` arrives once as a lib
  dependency and again as a `main` property reference. 15 objects then 20, into
  one store, with no collision — and STEP 6 snapshots every pre-existing `meta`
  object before the second resolve and asserts each survives byte-for-byte, so
  "no metadata loss" is witnessed, not merely observed.
- **The mixed lockfile pins verified against two independent authorities.**
  `main.oath.lock`'s eight dependencies are checked in STEP 6 as: `join-with`,
  `str-append`, `str-lt`, `List`, `Str` against `HEAD:codebase/names.json`, and
  `sort-strs`, `number-lines`, `number-line` against the library objects put in
  STEP 4. No hash is written into the script; both sides are read from their
  authority.
- **Interpreted and Go-built behaviour agreed, byte for byte.** Four inputs — three
  args, one arg, none, and eleven to cross the single-digit boundary — compared
  through files with `cmp`, because command substitution strips trailing newlines
  and would pass a backend that dropped the final `\n`.
- **All five definition hashes reproduced across stores.** `str-order`
  `#20554d214c04`, `sort-strs` `#a997e0314b01`, `number-line` `#f5f547256452`,
  `number-lines` `#e522506ad351`, `numbered-args` `#67af10804de3` — identical
  against the full corpus, against the empty-started target, and again after the
  §1 re-resolve into a third store. Identity is a function of the closure, not of
  the store, and that held every time it was checked.

And the guard held in the direction that matters: repointing a **sibling** name in
the target made `put --lock` refuse before elaborating — *"number-lines resolves
to `#a997e0314b01`, but the lock pins `#e522506ad351`"* — while a plain `put` in
the same tampered store **succeeded**, elaborating a different object
`#c15176bfaf82` at exit 0. `#187`'s design demonstrated that refusal on corpus
names; this shows it holds for names the corpus has never seen. *(hand-measured
during construction; `run.sh` does not carry this arm.)*

---

## Closing

The two-file split produced exactly one new demand, and it is the one in §1. Every
other finding is either a workaround with a known cost, a correct answer to an
ambiguous question, or a ceiling that was already written down.

That is a narrower result than "the tool needs work", and it is the supportable
one: on this consumer, against this corpus, the resolution machinery reproduced
every hash it was asked to, and the only thing it could not do was reproduce them
**from the lockfile alone**.
