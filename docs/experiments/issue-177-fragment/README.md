# #177 — the `--implies` fragment blind spot: the harness

**This directory is an INSTRUMENT, not a result.** It derives the set of corpus
definitions that `oath find --implies` cannot serve, from the committed store and
from the prover's own translator. **No verdict on #177 is assigned here**, and
none should be read into the counts: the issue's two decline criteria ask
whether the blind spot is *small and peripheral* and whether *another mode
reaches the same targets*, and this harness measures neither of those. It
measures the set. `transcript.txt` is one run of it.

## What is being measured, precisely

`oath find --implies` does **not** prove a candidate's stored properties. It
appends the CALLER'S query property to the candidate and proves that synthetic
property, with `self` bound to the candidate — `aug.Props = append(d.Props, qp)`
then `proveOne(&aug, …)`, `oath/api.go:1195-1256`. If the translator cannot
build a goal, no solver is consulted and the mode reports NO VERDICT, which the
caller cannot distinguish from the artifact not existing.

So the measured question is not *do this definition's own laws translate?* — a
definition with an untranslatable law may still be reachable by a different
query. It is:

> can ANY query mentioning this candidate be translated?

which turns on the candidate's own BODY, because a mentioning query inlines it.
The probe asks it with the minimal mentioning query, `(let [x (self b…)] true)`
— trivially true, so it carries no information except whether a goal can be
BUILT. It works at any return type because `tr` translates a `let`'s bound value
eagerly, before the body and whether or not the body uses it (`oath/prove.go:763-768`);
that is the probe's one load-bearing assumption about the translator, and it is
checkable at that line. Where the return type is equatable, the same question is
asked again as `(== (self b…) (self b…))` and the two verdicts must agree.

| class | meaning |
|---|---|
| **unreachable** | no query mentioning this definition can be translated |
| **reachable** | the minimal mentioning query translates, so some query can reach it |
| **inconclusive-polymorphic** | a polymorphic definition that bailed at the probe's concrete instantiation. That does not establish every instantiation bails, so it is held OUT of the blind-spot count rather than counted with a caveat — a count whose members carry different epistemic status is one nobody can use. No corpus member is in this class today; the branch exists because a class with no members is the one that gets silently mis-specified |
| **not-a-candidate** | a `data` declaration; `apiFindImplies` skips every non-`func` object before any proving happens, so it is outside the mode's universe rather than unreached within it |

The report ALSO carries the stored-property view — how many of a definition's own
laws the translator can build a goal for — as a **separate** statistic. That is
what `docs/experiments/issue-68.md` §6 counts, and it is what reconciles against
`fixtures/prove/attempts.txt`. **The two sets differ in both directions on this
corpus**, so neither is an approximation of the other.

**Recorded because the counts looked fine while it was wrong:** the first version
of this harness derived the blind spot from the stored properties. It produced a
plausible table, passed its own controls, and contained both errors the paragraph
above now guards against — a definition with no properties whose body is
untranslatable was filed as "no properties", and a definition whose only property
bails was filed as unreachable when a query does reach it. Review caught it, not
the controls: the universe came from the implementation's decomposition instead
of from the claim.

## Three scope limits, stated because each has a tempting stronger reading

- **A translation bail is a SUBSET of `--implies` NO VERDICT, not the whole of
  it.** `classifyProofStatus` maps the prover's `unknown` to no-verdict whether
  the goal was untranslatable or merely undischarged, so a goal that translates
  and defeats the solver reports identically to the caller. This harness
  measures the STRUCTURAL half — the goals no budget can reach — and says
  nothing about the rest.
- **A bailing candidate can still receive a verdict.** `apiFindImplies` runs a
  concrete-countermodel probe by evaluation before it reaches the prover, so an
  untranslatable candidate may come back REFUTED. That is a verdict, and it is
  still not a discovery: it tells the searcher this candidate does not match.
- **This is a fact about THIS corpus.** `examples/` and `apps/` are the exhibits
  this project chose, not a sample of programs. Every figure the harness prints
  quantifies over the committed corpus and over nothing else.

## The instrument

Two producers and one consumer, deliberately separate: the Go side measures and
never interprets, the Python side joins and never measures.

| artefact | what it establishes |
|---|---|
| `oath/corpus_census_test.go` | the UNIVERSE — every live name in `codebase/names.json`, every object it resolves to, and the store's own verdicts. Pre-existing (#68); unchanged by this work. |
| `oath/fragment_probe_test.go` | the prover's OWN enumeration seam (`smtCtx.enumerate`) run live over the same store: scripts emitted per goal, and the translator's verbatim refusal for a goal that emits none. |
| `blindspot.py` | the join, the controls, the partition, the members and the counts. |
| `run.sh` | the driver; digests every file under `codebase/` and `fixtures/` before and after and FAILS if any byte moved. |

### Why the probe and not `fixtures/prove/attempts.txt`

The committed fixture records a row only where a script exists, so absence
spells both *the goal bailed* and *nobody looked*.
`scripts/prove-reasons.py` says exactly that at the point where its fourth
control would have gone, and names the enumerating producer as the artefact that
closes it. `oath/fragment_probe_test.go` is that producer, run live — which also
supplies the bail REASON, something the fixture format has no place to put.

The fixture is still read, as a RECONCILIATION target: a disagreement between
the live bail set and the fixture's no-row set is a fact about the fixture's
freshness, and it is reported rather than swallowed.

### Why it enumerates rather than reading term kinds

A parallel analysis of which term forms "look untranslatable" would encode its
author's list of refusals and drift from the translator's. The seam used here is
the same code path the prover runs; `oath/prove_attempts_test.go` holds the
AST-level control that every solver call on that path is behind `smtCtx.solve`,
which enumeration short-circuits.

## Controls, and the mutation that makes each fail

Every one below was watched failing before the green run was believed.

| control | where | mutation that fires it |
|---|---|---|
| the two enumeration paths agree on the script count for EVERY property | probe | return `len(attempts)+1` from the outcome-carrying path |
| the `let` and `==` synthetic queries agree wherever both typecheck | probe | make the `let` form bind a constant instead of the self-call — it then reports every candidate reachable |
| `mentionsSelf` visits EVERY child-bearing field of `Term` | probe (`TestMentionsSelfCoversEveryChildField`, ungated) | make the walker skip `[]Term` — a `self` planted in `Args`, or in `Arms`, then reads as absent. The hand-written walker did exactly that: it listed the four fields its author remembered and missed `Arms`, silently disabling the control below |
| a definition with a translating **self-mentioning** stored property is never called unreachable | probe | force `runQuery` to bail. The `self`-mention gate matters: a law that never mentions the definition translates without the body being reached, so it is no evidence about the body — an ungated version would abort the probe on a perfectly valid corpus member |
| a PROVEN property never bails | probe | force `Bail` true for a proven definition |
| a bail always carries a reason | probe | blank the returned detail |
| the fault-injection env seams are OFF | probe | set `OATH_PROVE_FORCE_ABORT` — it returns before translation, producing the same zero attempts a bail does |
| a zero-attempt exit is a TRANSLATION bail (`unknown`), not some other early return | probe | disable the guard above and abort a real property: the exit reports `invalidated` and is refused |
| the corpus is non-empty, and both sides of the boundary are populated | probe | filter the object set to nothing |
| census and probe describe ONE corpus (hashes, names, property arity) | join | delete an object from the probe's report |
| every property index was visited | join | truncate a property list |
| every bail cause matches a listed pattern — for stored properties AND for the synthetic query | join | rewrite either reason to an unlisted string, or blank it |
| the fixture reconciliation reports rows the live corpus no longer has | join | append a row for a non-existent definition to a copy of `attempts.txt` — without this direction, a removed definition leaves stale rows and the report still says the two agree exactly |
| the partition sums back to the census's own universe | join | decrement the census's `live_objects` / `live_names` header, leaving its body intact |
| every FUNCTION was measured (a `data` declaration need not be) | join | mark a function `not-probed` |
| a polymorphic bail is held out of the blind-spot count | join | mark a bailing object polymorphic — it leaves `unreachable` and lands in `inconclusive-polymorphic` |
| `record-field` and `json-string-value`, **pinned as (name, hash) pairs**, are in the blind spot | join | mark either reachable; or repoint the name to a different unreachable object — a name-only pin would have passed that |
| the probe PASSed rather than SKIPped | `run.sh` | unset `OATH_FRAGMENT_OUT` — `go test` still prints `PASS` |
| no file under `codebase/` or `fixtures/` changed content | `run.sh` | create a file under `fixtures/` between the two snapshots |
| that digest is non-empty | `run.sh` | point `find` at nothing — two empty files compare equal |
| `fixtures/` and `fixtures/prove/attempts.txt` are present | `run.sh` / join | delete `fixtures/` — the digest stays non-empty from `codebase/` alone and the reconciliation would silently have nothing to do, so the run would PASS having measured half its universe |
| OUTDIR is not inside a guarded tree, after full canonicalization | `run.sh` | pass `codebase/missing/deep` (parent does not exist); a symlink to `codebase/`; a path under a symlink to `fixtures/`; or `missing/../codebase/probe`, where the `..` sits behind a nonexistent component. All are refused before anything is created — the last one DID create `codebase/probe` before the guard resolved unresolved components, which is why resolution runs to a fixpoint |

The last three are the ones a green run would otherwise hide: a skipped test, a
harness that dirtied the store, and a content check with nothing in it all look
exactly like success from the exit code.

**The store check compares CONTENT, not `git status`.** A status comparison
cannot see a file that was already modified going in being modified again — its
` M` code does not change — and `codebase/log.jsonl` is append-only and is
exactly what a stray `make verify` would grow. A dirty tree is the situation in
which this check most needs to be trustworthy, so it digests the bytes.

**Why `record-field` and `json-string-value` are a hard control rather than a
spot-check.** They are the only two members of this set with an EXTERNAL witness
— `docs/experiments/issue-74-falsifier.md` reached each by running
`oath find --implies` and reading the NO VERDICT reason off the CLI. A
derivation that computes some other set would be indistinguishable from a
correct one by its counts alone; these two are the only rows that can tell the
difference.

They are pinned as **(name, hash) pairs**, and the pair is the point.
`apiFindImplies` iterates `st.Names()`, so a hash no live name reaches is not a
candidate at all and a hash-only pin would check something the mode never looks
at — while a name-only pin follows a mutable pointer, and repointing it to a
different unreachable object would let the control pass on a witness never taken
for that object. The pair separates *the witness is stale, retake it* from *the
instrument disagrees with an external observation*, which are different repairs.

## Reproducing

    docs/experiments/issue-177-fragment/run.sh [OUTDIR]

About three seconds. **No solver runs and nothing is written to the store** —
the probe opens the filesystem backend at `codebase/`, reads, and closes.
`OUTDIR` defaults to a fresh `mktemp -d` and holds the intermediate JSON, which
is regenerable and deliberately not committed. It may be relative — it is
canonicalized to an absolute path before use, because the two Go tests run inside
a `cd oath` subshell and a relative path would otherwise resolve against `oath/`
there and against the root everywhere else. The run prints its own verdict
line; a run that does not print `SUMMARY:` did not finish.

The probe SKIPS unless `OATH_FRAGMENT_OUT` is set, so `go test ./...` pays
nothing for it. That also means **nothing in CI runs it** — it is a measurement,
not an invariant, and it is checked by running it.
