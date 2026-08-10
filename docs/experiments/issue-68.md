# #68 — a read-only census of the committed corpus

**What this file is:** the baseline measurement that any claim about how much of
the corpus is proven, tested or stalled has to be built on — the universe first,
the numbers second. It enumerates every live name, every object those names
resolve to, and every property verdict on both, from the committed store.

**It calls no property blocked, stalled, SMT-incomplete or
reachable-by-sequence-theory; no verdict on #68 is offered or implied; nothing is
recommended.** Those are downstream questions, and running them against a
universe nobody had pinned down is how this repository has previously produced
figures that overstated a population by ten definitions.

Sections 1-5 are the census and sort nothing at all. **Section 6 draws exactly
one line through the non-proven set** — whether a candidate SMT script was ever
emitted for the goal — and cross-tabulates which induction strategies the goal
was a candidate for. Both are facts about the goal's SHAPE, decidable from a
committed fixture with no solver, and neither is a reason the goal failed. This
paragraph read "**It classifies nothing**" while sections 1-5 were the whole
file; section 6 made that flatly false, and the sentence would have gone on
reading correctly without a word of it changing.

**Nothing here writes.** The store is opened, read and closed, and `git status
codebase/ fixtures/` was clean before and after every run reported below. In
particular `make verify` was not run: it re-puts every example into the default
store, and a re-put at an unchanged hash is still journalled `accepted`, so a
read-only session that runs it leaves the journal advanced for a change that
touched no identity.

Run on 2026-08-08 against `cbe82ba`, go1.25.6, python 3.14.5. No solver is
invoked: every verdict below is read from the store as committed, never
re-derived.

## Why a census, and why it is not just a listing

The question "how much of the corpus is X?" needs a universe before it needs a
number, and there are three plausible universes here that give three different
answers:

    codebase/meta/*.json     295 records — every object EVER stored, superseded
                             ones included, each still carrying the verdicts it
                             held when a name last pointed at it. This answers a
                             question about the store's HISTORY while looking
                             exactly like a question about the corpus.
    codebase/names.json      210 live names. What the corpus OFFERS.
    the objects behind them  208 distinct hashes. What the corpus IS — and where
                             verdicts actually live, because a verdict is a fact
                             about a hash.

The gap between the second and the third is aliasing: names alias, objects do
not, so a set-keyed join silently collapses two names into one row and a
name-keyed sum silently counts one object twice. Both views are correct; they
answer different questions, and neither is a rounding of the other. So both are
enumerated below, in full, with no deduplication anywhere.

## The instrument

`oath/corpus_census_test.go` — a Go test in the kernel's own package, so it uses
the kernel's reader and the kernel's store rather than reimplementing either.

    ( cd oath && OATH_CENSUS_OUT=/tmp/census.json go test -run TestCorpusCensus -count=1 )
    python3 scripts/corpus-census.py /tmp/census.json summary
    python3 scripts/corpus-census.py /tmp/census.json objects
    python3 scripts/corpus-census.py /tmp/census.json names
    python3 scripts/corpus-census.py /tmp/census.json reconcile

Each generated block below is reproduced under the command that produces it, so
no table has to be taken on the surrounding prose's word.

**The two halves of the universe, and the assertion that binds them.** The SOURCE
half is every top-level form `parseForms` finds in `examples/*.oath` plus
`apps/*/*.oath` — the kernel's reader, not a regex, because the parser is the
authority on what a declaration is, and the test fails rather than skips if it
meets a top-level form that is neither `(data …)` nor `(defn …)`. The STORE half
is every entry in `codebase/names.json`. The test asserts the two name sets are
EQUAL, and reports the difference in each direction rather than a count mismatch,
because "210 ≠ 209" does not say which side moved.

**It pins no counts.** A hardcoded 210 in the test would be the duplicated-
authority defect the census exists to avoid, and would go stale the first time
the corpus grows. Every number is output; the only expectations are relations.

**The property universe is the DEFINITION, not the naming metadata.** A `Prop`
carries no name of its own: properties live in the hashed `Def` and are part of
identity, while `prop_names` is metadata supplying spellings. So `len(d.Props)`
decides how many properties exist and `prop_names` only decides what they are
called. The first version of this instrument counted the names, which would have
let a short or stale naming record delete a real property from every figure below
without one assertion firing — the same wrong-universe move the census was built
to avoid, one layer in. The two agree everywhere in this corpus, and switching
the universe changed no number here; it changed the instrument from
accidentally-right to right.

Further assertions, each of which would otherwise be an assumption:

- `prop_names` has exactly as many entries as the definition has properties, and
  so does every alias's naming record;
- every `proven_props` index names a real property — a stray index would leave
  the proven COUNT higher than the enumerated verdicts with nothing noticing;
- no index appears in `proven_props` twice — a duplicate inflates the stored
  summary and the raw list together, so they corroborate each other while both
  disagree with the enumeration;
- `guarantee.proven` equals the number of DISTINCT proven indices;
- the source form's kind agrees with the stored `Def.K`, and all declaration
  sites of one object agree with each other;
- every alias has an alias naming record, so a per-name row never silently
  reports the canonical name's spelling of a property.

`TestCorpusListsAgree` separately asserts the Makefile's `APPS` matches the
`apps/*/*.oath` glob. `oathrs/conformance.sh` derives its corpus from the same
glob; `APPS` is the only hand-written list of the three, and a hand-written list
is exactly the enumerable proxy this repo keeps finding underneath its
wrong-universe defects.

**What the instrument does NOT do.** It does not re-derive hashes from source. It
reads the hash a live name resolves to and trusts it. Confirming that the
committed source still content-addresses to the committed objects is check 1 of
`oathrs/conformance.sh` and `make verify`, and the latter appends to the journal;
that is a different claim from this one and is deliberately left to the tool that
already owns it.

## Controls — the instrument was watched failing before its output was believed

A green measurement means nothing until it has been seen to go red for the right
reason. Every mutation below was run in a detached `git worktree`, never in the
working checkout, and reverted immediately:

| mutation | result |
|---|---|
| none (baseline) | PASS |
| delete `(defn append …)` from `examples/list.oath` | FAIL — `live in codebase/names.json but NOT declared in the corpus source (1): [append]` |
| append a declaration absent from the store | FAIL — `declared in corpus source but NOT live in codebase/names.json (1): [census-control-probe]` |
| set `abs`'s `guarantee.proven` to 99 | FAIL — `guarantee.proven=99 but proven_props names 3 distinct properties` |
| add out-of-range index 77 to `abs`'s `proven_props` | FAIL — `proven_props index 77 out of range for 3 properties` |
| repeat an index: `abs`'s `proven_props` = `[0, 0, 2]` | FAIL — `proven_props lists index 0 more than once`, and `guarantee.proven=3 but proven_props names 2 distinct properties` |
| drop the last entry of `abs`'s `prop_names` | FAIL — `the definition carries 3 properties but prop_names has 2 entries` |
| drop the last entry of `rot-f`'s alias `prop_names` | FAIL — `"rot-f": 6 prop names but the object carries 7 properties` |
| drop `rot-f`'s alias naming record entirely | FAIL — `"rot-f" resolves to e715328a6ffa whose canonical name is "rot", but there is no alias naming record for it` |
| remove `hdr-probe.oath` from the Makefile's `APPS` | FAIL — `../apps/*/*.oath contains apps/github-webhook/hdr-probe.oath but the Makefile's APPS does not list it` |
| remove `rle` from the Makefile's `EXAMPLES` | FAIL — `../examples/*.oath contains rle but the Makefile's EXAMPLES+EXHIBITS does not list it` |
| remove `float` from the Makefile's `EXHIBITS` | FAIL — same, naming `float` |
| add a listed name with no file | FAIL — `the Makefile's EXAMPLES+EXHIBITS lists ghost but ../examples/*.oath does not find it` |
| rename the `EXAMPLES` variable | FAIL — `no EXAMPLES assignment found in the Makefile; this check did NOT run` |
| set one fixture row's `prop_count` to 99 | reported — `` `abs`: prop_count=99 but props lists 3 `` |
| set one fixture row's `proven_count` to 0 | reported — `` `abs`: proven_count=0 but props marks 3 proven `` |
| run the whole census under `OATH_BACKEND=cloud` | PASS, and the emitted census is byte-identical: 210 / 210 / 208. The filesystem backend is pinned, so the environment cannot redirect the measurement |
| fail one assertion with `OATH_CENSUS_OUT` pointing at an existing file | no census written, and the stale file removed — `assertions failed: no census written, and …/c19.json removed if it existed` |
| invert the source-kind → `Def.K` mapping so `defn` expects `data` | FAIL on all **194** `defn` objects, i.e. every one of them — the cross-check is live and total, not vacuous |
| relabel one census row's level `speculative` | rendered as its own row, flagged `← not in the known guarantee ladder`, and the level column still sums to the row count |
| none (restored) | PASS |

The two baseline runs matter as much as the failures: without them a
permanently-red instrument and a discriminating one look identical.

Two of those deserve a sentence. The `OATH_BACKEND=cloud` row exists because
`OpenStore(root)` consults that variable and, when it is set to `cloud`, IGNORES
the path it was handed and opens the remote registry instead — so a census
claiming to describe the committed store would have described something else
while every assertion still passed. The instrument now binds the filesystem
backend directly, and that row is what shows the binding holds. And the
inverted-mapping row is there because the earlier version of the kind check asked
`!= "data"`, which accepts an empty or unknown stored kind for every `(defn …)`
in the corpus: it would have passed over exactly the malformed data it exists to
catch.

The variable-rename control is there for a specific failure this repo has met
before: an instrument whose input silently parses to nothing passes over
everything and reports success. `makeVarFields` fails loudly instead of returning
an empty list, and that row is what demonstrates it.

The repeated-index control is the one worth keeping. `proven_props = [0, 0, 2]`
leaves `guarantee.proven = 3`, so the earlier version of the check — comparing
the summary against `len(proven_props)` — saw 3 and 3 and passed, while the
enumeration below it listed two proven properties. Two stored spellings of one
fact agreed with each other and disagreed with the thing they summarise.

## 1. Summary

    python3 scripts/corpus-census.py /tmp/census.json summary

- source declarations (`examples/*.oath` + `apps/*/*.oath`, kernel reader): **210**
- live names (`codebase/names.json`): **210**
- distinct objects those names resolve to: **208**
- objects reached by more than one live name: **2**

  - `f103d27ee151` (data, canonical `Run`) ← `Interval`, `Run`
  - `e715328a6ffa` (defn, canonical `rot`) ← `rot`, `rot-f`

| | per-object | per-name |
|---|---:|---:|
| rows | 208 | 210 |
| kind `data` | 14 | 15 |
| kind `defn` | 194 | 195 |
| level `proven` | 127 | 127 |
| level `tested` | 59 | 60 |
| level `falsified` | 4 | 4 |
| level `asserted` | 18 | 19 |
| properties | 506 | 513 |
| properties proven | 365 | 368 |
| properties not proven | 141 | 145 |

The per-name column exceeds the per-object column by **7 properties (3 of them proven)**. That difference is the aliasing, and it is the whole reason both columns are printed.

The source-declaration count and the live-name count are the same number for a
reason worth stating: the test asserts set equality, so this is not two
independent counts that happen to agree. Every name the corpus source declares is
live, and every live name is declared by the corpus source. There are no orphans
in either direction.

### The two aliased objects

Two objects are each reached by two live names, and both are declared twice, in
two different files:

| object | live names | kind | declaration sites |
|---|---|---|---|
| `f103d27ee151` | `Interval`, `Run` | data | `examples/interval.oath:10`, `examples/rle.oath:1` |
| `e715328a6ffa` | `rot`, `rot-f` | defn | `examples/rot.oath:1`, `examples/rot_f.oath:1` |

`Interval`/`Run` carries no properties, so it moves the row counts and no verdict
counts. `rot`/`rot-f` carries seven properties, three of them proven, and that
single object is the entire difference between the two columns above: 513 − 506 =
7 properties, 368 − 365 = 3 proven.

Both names spell all seven properties identically here — the alias naming record
exists and is exercised, but in this corpus it happens to hold the same strings.
That is a fact about today's corpus, not a property of aliasing: the mechanism
carries per-name spellings precisely because they are permitted to differ.

## 2. Per-object enumeration

One row per distinct object some live name resolves to, sorted by canonical name.
`+` marks a proven property, `-` one that is not proven. `mutants` is
killed/total, with waivers reported separately and never as kills.

    python3 scripts/corpus-census.py /tmp/census.json objects

| # | object | canonical | live names | kind | declaration site(s) | level | proven/props | term | mutants | properties |
|---:|---|---|---|---|---|---|---:|---|---:|---|
| 1 | `4bb5e1cbb831` | `Flaky` | `Flaky` | data | `examples/stateful.oath:48` | asserted | 0/0 | — | — | *(none)* |
| 2 | `a88fcdcbeae3` | `KV` | `KV` | data | `examples/stateful.oath:10` | asserted | 0/0 | — | — | *(none)* |
| 3 | `fa452d59a235` | `List` | `List` | data | `examples/list.oath:6` | asserted | 0/0 | — | — | *(none)* |
| 4 | `a9a8fb7ed67a` | `Map` | `Map` | data | `examples/map.oath:11` | asserted | 0/0 | — | — | *(none)* |
| 5 | `895777a9ff55` | `Option` | `Option` | data | `examples/records.oath:6` | asserted | 0/0 | — | — | *(none)* |
| 6 | `3c3d94c1dc9e` | `Pair` | `Pair` | data | `examples/records.oath:10` | asserted | 0/0 | — | — | *(none)* |
| 7 | `0100c76d0cda` | `Queue` | `Queue` | data | `examples/queue.oath:6` | asserted | 0/0 | — | — | *(none)* |
| 8 | `5d12ea7ba780` | `Request` | `Request` | data | `examples/http.oath:41` | asserted | 0/0 | — | — | *(none)* |
| 9 | `b40fd2d9d1c6` | `Response` | `Response` | data | `examples/http.oath:44` | asserted | 0/0 | — | — | *(none)* |
| 10 | `533302244706` | `Result` | `Result` | data | `examples/records.oath:62` | asserted | 0/0 | — | — | *(none)* |
| 11 | `f103d27ee151` | `Run` | `Interval`, `Run` | data | `examples/interval.oath:10`<br>`examples/rle.oath:1` | asserted | 0/0 | — | — | *(none)* |
| 12 | `16a9d47302b9` | `Set` | `Set` | data | `examples/set.oath:12` | asserted | 0/0 | — | — | *(none)* |
| 13 | `e6bbed8bc934` | `Str` | `Str` | data | `examples/str.oath:9` | asserted | 0/0 | — | — | *(none)* |
| 14 | `003bea7bfc59` | `Tree` | `Tree` | data | `examples/tree.oath:12` | asserted | 0/0 | — | — | *(none)* |
| 15 | `f8552a9f06ee` | `abs` | `abs` | defn | `examples/ints.oath:6` | proven | 3/3 | nonrecursive | 3/5 | `+non-negative` `+idempotent` `+is-x-or-negx` |
| 16 | `ca9250f107c3` | `abs-small` | `abs-small` | defn | `examples/undertested.oath:6` | tested | 0/1 | nonrecursive | — | `-bounded-wrongly` |
| 17 | `966495ef1b6f` | `all` | `all` | defn | `examples/list.oath:202` | proven | 2/2 | structural | 2/2 | `+empty-is-true` `+cons-step` |
| 18 | `dd0648da7466` | `any` | `any` | defn | `examples/list.oath:217` | proven | 2/2 | structural | 2/2 | `+empty-is-false` `+cons-step` |
| 19 | `78d23e273a48` | `append` | `append` | defn | `examples/list.oath:24` | proven | 3/3 | structural | 4/4 | `+length-adds` `+nil-right-identity` `+associative` |
| 20 | `6c0735984f65` | `apply2` | `apply2` | defn | `examples/exclusion.oath:30` | asserted | 0/0 | nonrecursive | — | *(none)* |
| 21 | `af6b61e2180a` | `bad-reverse` | `bad-reverse` | defn | `examples/bad_reverse.oath:7` | falsified | 0/2 | nonrecursive | — | `-involution` `-antidistributes-over-append` |
| 22 | `1d2158287a2c` | `bytes-after` | `bytes-after` | defn | `apps/github-webhook/webhook.oath:150` | tested | 0/2 | structural | — | `-missing-is-none` `-finds-at-head` |
| 23 | `d2406871baf1` | `bytes-ok` | `bytes-ok` | defn | `examples/http.oath:132` | proven | 3/3 | structural | 7/12 | `+empty-is-ok` `+rejects-negative` `+rejects-oversized` |
| 24 | `6254197a78fe` | `bytes-prefix` | `bytes-prefix` | defn | `apps/github-webhook/webhook.oath:133` | proven | 3/3 | structural | — | `+empty-is-prefix` `+nothing-precedes-empty` `+self` |
| 25 | `066e843a31cd` | `bytes-str` | `bytes-str` | defn | `apps/github-webhook/webhook.oath:125` | proven | 2/2 | structural | — | `+empty-is-empty` `+inverts-str-bytes` |
| 26 | `b63e7163b5dd` | `caps-with` | `caps-with` | defn | `apps/github-webhook/webhook.oath:557` | tested | 0/2 | nonrecursive | — | `-provides-the-secret` `-emit-is-the-sink` |
| 27 | `3bb186fa4728` | `check-config` | `check-config` | defn | `examples/config.oath:62` | proven | 2/2 | nonrecursive | — | `+no-args-is-usage` `+reports-ok-or-missing` |
| 28 | `b9df489c7e77` | `circle` | `circle` | defn | `examples/circle.oath:48` | tested | 0/2 | nonrecursive | 38/41 | `-r5` `-r10` |
| 29 | `9fa25283f279` | `clamp` | `clamp` | defn | `examples/ints.oath:22` | proven | 3/3 | nonrecursive | 2/4 | `+within-bounds` `+idempotent` `+identity-inside` |
| 30 | `026c7502118d` | `config-has-key` | `config-has-key` | defn | `examples/config.oath:31` | tested | 2/3 | structural | — | `+empty-has-nothing` `-finds-head` `+skips-mismatch` |
| 31 | `7995ac55afc6` | `config-key` | `config-key` | defn | `examples/config.oath:23` | proven | 3/3 | nonrecursive | — | `+reads-key-before-eq` `+whole-line-when-no-eq` `+empty-stays-empty` |
| 32 | `8c9e095b0d72` | `config-missing` | `config-missing` | defn | `examples/config.oath:44` | tested | 2/3 | structural | — | `+nothing-required-is-complete` `+reports-a-missing-key` `-complete-config-reports-nothing` |
| 33 | `6221bec8324b` | `contains` | `contains` | defn | `examples/extras.oath:37` | proven | 3/3 | structural | 2/2 | `+found-at-head` `+absent-from-empty` `+agrees-with-count` |
| 34 | `040c290d0c35` | `count` | `count` | defn | `examples/sort.oath:6` | proven | 2/2 | structural | 5/9 | `+non-negative` `+bounded-by-length` |
| 35 | `ed0b40d5d68f` | `count-append` | `count-append` | defn | `examples/sort.oath:28` | proven | 1/1 | nonrecursive | 0/1 (+1 waived) | `+splits-over-append` |
| 36 | `851d636b9476` | `count-by` | `count-by` | defn | `examples/generic.oath:25` | proven | 3/3 | structural | 8/8 | `+non-negative` `+bounded-by-length` `+cons-ledger` |
| 37 | `c2f6fe6558ca` | `count-matching` | `count-matching` | defn | `examples/list.oath:251` | proven | 3/3 | structural | 7/7 | `+empty-is-zero` `+non-negative` `+cons-step` |
| 38 | `f1e861a8d7ed` | `drop` | `drop` | defn | `examples/extras.oath:12` | proven | 4/4 | structural | 11/11 | `+drop-all` `+drop-zero` `+drop-one` `+drop-length` |
| 39 | `cedba00b3ab7` | `drop-while` | `drop-while` | defn | `examples/list.oath:240` | proven | 2/2 | structural | 3/3 | `+empty-is-empty` `+cons-step` |
| 40 | `100733cab4b2` | `e-div` | `e-div` | defn | `examples/ediv.oath:14` | tested | 3/5 | nonrecursive | 10/10 | `-division-identity` `+zero-divisor-is-zero` `-shift-by-divisor` `+negate-divisor-negates` `+quadrant-anchors` |
| 41 | `362696e75c13` | `e-mod` | `e-mod` | defn | `examples/ediv.oath:1` | tested | 5/6 | nonrecursive | 17/20 | `+nonneg` `+below-abs-divisor` `+zero-divisor-is-a` `-periodic` `+divisor-sign-irrelevant` `+quadrant-anchors` |
| 42 | `604e3e90c2ff` | `echo-handler` | `echo-handler` | defn | `examples/http.oath:115` | proven | 3/3 | nonrecursive | 43/43 | `+echoes-body-exactly` `+always-200` `+reports-method` |
| 43 | `817f85803f2a` | `embed-add` | `embed-add` | defn | `examples/convert.oath:21` | proven | 1/1 | nonrecursive | 1/1 | `+additive` |
| 44 | `f55335261c55` | `excluded-witness` | `excluded-witness` | defn | `examples/exclusion.oath:36` | tested | 1/2 | nonrecursive | 3/3 | `+independent-of-exclusion` `-reaches-excluded-op` |
| 45 | `135e96990463` | `f-double` | `f-double` | defn | `examples/float.oath:18` | proven | 1/1 | nonrecursive | 1/1 | `+is-scale` |
| 46 | `4b136716aca7` | `f-mul-id` | `f-mul-id` | defn | `examples/float.oath:12` | proven | 1/1 | nonrecursive | 1/1 | `+identity` |
| 47 | `fec8b5271ba3` | `f-scale-inv` | `f-scale-inv` | defn | `examples/float.oath:33` | falsified | 0/1 | nonrecursive | 3/3 | `-recovers` |
| 48 | `18889b76c77e` | `f-tenths` | `f-tenths` | defn | `examples/float.oath:25` | falsified | 0/1 | nonrecursive | 1/1 | `-is-three-tenths` |
| 49 | `7ce4d64ce4a0` | `fib` | `fib` | defn | `examples/arith.oath:28` | proven | 4/4 | measure | 17/17 | `+base-zero` `+base-one` `+unfold-step` `+nonneg` |
| 50 | `cc825b87e888` | `filter` | `filter` | defn | `examples/list.oath:105` | proven | 2/2 | structural | 4/4 | `+empty-is-empty` `+cons-step` |
| 51 | `bce060be0e7f` | `find` | `find` | defn | `examples/extras.oath:59` | proven | 2/2 | structural | 2/2 | `+empty-is-none` `+cons-step` |
| 52 | `775f5b2a57d7` | `first-or` | `first-or` | defn | `examples/circle.oath:43` | tested | 0/1 | nonrecursive | 0/1 | `-empty` |
| 53 | `08cce2f0575f` | `flat-map-option` | `flat-map-option` | defn | `examples/records.oath:33` | proven | 2/2 | nonrecursive | 1/1 | `+none-stays-none` `+some-applies` |
| 54 | `0497351b656c` | `flatten` | `flatten` | defn | `examples/list.oath:186` | proven | 3/3 | structural | 2/2 | `+empty-is-empty` `+cons-step` `+distributes-over-append` |
| 55 | `987c33ad5770` | `foldl` | `foldl` | defn | `examples/list.oath:156` | proven | 3/3 | structural | 2/2 | `+empty-is-seed` `+cons-step` `+two-step` |
| 56 | `5b345e9d645f` | `foldr` | `foldr` | defn | `examples/list.oath:137` | proven | 3/3 | structural | 2/2 | `+empty-is-seed` `+cons-step` `+two-step` |
| 57 | `c77f088bed04` | `full-name` | `full-name` | defn | `examples/records.oath:100` | proven | 2/2 | nonrecursive | 5/5 | `+length-adds-up` `+starts-from-parts` |
| 58 | `53219da89135` | `gh-record` | `gh-record` | defn | `apps/github-webhook/webhook.oath:299` | tested | 0/3 | nonrecursive | — | `-has-five-fields` `-declares-its-schema` `-never-empty` |
| 59 | `dc9d0dd11c56` | `gh-request` | `gh-request` | defn | `apps/github-webhook/webhook.oath:507` | tested | 0/3 | nonrecursive | — | `-is-a-post` `-keeps-the-path` `-keeps-the-body` |
| 60 | `bf2bf8a950ef` | `gh-sign` | `gh-sign` | defn | `apps/github-webhook/webhook.oath:483` | tested | 0/2 | nonrecursive | 22/22 | `-carries-the-algorithm` `-survives-gh-signature` |
| 61 | `4a5152b8c8eb` | `gh-signature` | `gh-signature` | defn | `apps/github-webhook/webhook.oath:42` | tested | 0/4 | nonrecursive | — | `-absent-header-is-none` `-unprefixed-is-rejected` `-strips-exactly-the-prefix` `-wrong-length-is-rejected` |
| 62 | `64468c9d46cc` | `gh-spec-secret` | `gh-spec-secret` | defn | `apps/github-webhook/webhook.oath:478` | tested | 0/1 | nonrecursive | — | `-is-usable` |
| 63 | `552ec5bd9d18` | `gh-webhook` | `gh-webhook` | defn | `apps/github-webhook/webhook.oath:659` | tested | 0/14 | nonrecursive | 203/208 | `-status-is-one-of-seven` `-never-leaks-a-body` `-non-post-is-405` `-unusable-secret-refuses-everything` `-unusable-secret-refuses-a-valid-signature` `-unsigned-is-401` `-tampering-is-rejected` `-unprefixed-signature-is-rejected` `-trailing-junk-after-a-valid-digest-is-rejected` `-wrong-path-is-404` `-unreadable-content-type-is-415` `-accepts-github-signed` `-ping-does-not-record` `-a-non-ping-does-record` |
| 64 | `1b4fc08a4e93` | `greet` | `greet` | defn | `examples/service.oath:7` | proven | 3/3 | nonrecursive | 26/26 | `+exact-shape` `+never-shorter-than-frame` `+same-world-same-answer` |
| 65 | `1da62c4f6353` | `greet-or-guest` | `greet-or-guest` | defn | `examples/service.oath:16` | proven | 2/2 | nonrecursive | 0/15 | `+none-means-guest` `+some-passes-through` |
| 66 | `f19d388b9584` | `hdr-probe` | `hdr-probe` | defn | `apps/github-webhook/hdr-probe.oath:41` | tested | 0/1 | nonrecursive | — | `-always-200` |
| 67 | `f0a62a28b4b4` | `header-first` | `header-first` | defn | `examples/http.oath:84` | proven | 3/3 | structural | 2/2 | `+empty-has-none` `+finds-head` `+skips-mismatch` |
| 68 | `d585aa88cdfb` | `header-or` | `header-or` | defn | `apps/github-webhook/webhook.oath:87` | tested | 0/2 | nonrecursive | — | `-absent-yields-fallback` `-present-yields-value` |
| 69 | `e98335ea6c14` | `hex-decode` | `hex-decode` | defn | `examples/webhook.oath:106` | proven | 7/7 | nonrecursive | 1/1 | `+empty-decodes-empty` `+decodes-ff` `+decodes-00` `+invalid-fails-closed` `+trailing-junk-fails-closed` `+a-bad-high-nibble-fails-closed` `+a-bad-low-nibble-fails-closed` |
| 70 | `c46f1fb5eecb` | `hex-decode-unchecked` | `hex-decode-unchecked` | defn | `examples/webhook.oath:90` | proven | 5/5 | structural | 7/7 | `+empty-decodes-empty` `+decodes-ff` `+decodes-00` `+decodes-both-nibbles` `+decodes-a-pair-of-pairs` |
| 71 | `007871398c18` | `hex-digit` | `hex-digit` | defn | `examples/webhook.oath:148` | tested | 0/3 | nonrecursive | 13/14 | `-zero-is-char-0` `-ten-is-char-a` `-roundtrips` |
| 72 | `85e837e0fa9b` | `hex-encode` | `hex-encode` | defn | `examples/webhook.oath:155` | tested | 0/3 | structural | 11/11 | `-empty-encodes-empty` `-encodes-255` `-roundtrips-through-decode` |
| 73 | `548c2b120a8b` | `hex-nibble` | `hex-nibble` | defn | `examples/webhook.oath:29` | proven | 2/2 | nonrecursive | 11/53 | `+digits` `+rejects-non-hex` |
| 74 | `954c1887f94a` | `hex-valid` | `hex-valid` | defn | `examples/webhook.oath:56` | proven | 7/7 | structural | 12/12 | `+empty-is-valid` `+pair-is-valid` `+odd-length-is-invalid` `+trailing-junk-is-invalid` `+zero-pair-is-valid` `+a-bad-high-nibble-is-invalid` `+a-bad-low-nibble-is-invalid` |
| 75 | `d497b8473178` | `hmac-kat-rfc4231-2` | `hmac-kat-rfc4231-2` | defn | `examples/webhook.oath:271` | tested | 0/3 | nonrecursive | 288/288 | `-matches-published-vector` `-arguments-are-not-symmetric` `-length-mismatch-is-not-equal` |
| 76 | `3fc1c9e49884` | `i-contains` | `i-contains` | defn | `examples/interval.oath:13` | proven | 4/4 | nonrecursive | 5/5 | `+contains-def` `+contains-endpoints` `+contains-empty-none` `+contains-outside-false` |
| 77 | `7ad9cbd2d649` | `i-hull` | `i-hull` | defn | `examples/interval.oath:57` | proven | 5/5 | nonrecursive | 11/15 | `+hull-contains-both` `+hull-tight-endpoints` `+hull-empty-left` `+hull-empty-right` `+hull-bounds-when-nonempty` |
| 78 | `416798295f35` | `i-intersect` | `i-intersect` | defn | `examples/interval.oath:42` | proven | 4/4 | nonrecursive | 5/7 | `+intersect-members` `+intersect-empty-iff-disjoint` `+intersect-sym-members` `+intersect-bounds-when-overlapping` |
| 79 | `ba067f27d425` | `i-overlaps` | `i-overlaps` | defn | `examples/interval.oath:24` | proven | 5/5 | nonrecursive | 9/11 | `+overlaps-def` `+overlaps-witness-sound` `+overlaps-complete` `+overlaps-touching` `+overlaps-empty-left` |
| 80 | `d679d2e21f62` | `index-of` | `index-of` | defn | `examples/extras.oath:139` | proven | 3/3 | structural | 8/8 | `+empty-none` `+found-at-head` `+miss-step` |
| 81 | `6ed53caa0ad2` | `init` | `init` | defn | `examples/extras.oath:86` | proven | 3/3 | structural | 3/3 | `+empty-is-empty` `+singleton` `+step` |
| 82 | `ae876364d0f2` | `initials-or` | `initials-or` | defn | `examples/records.oath:108` | proven | 2/2 | nonrecursive | 1/1 | `+none-falls-back` `+some-formats` |
| 83 | `07e9fddfeed3` | `insert` | `insert` | defn | `examples/sort.oath:63` | proven | 6/6 | structural | 4/5 | `+grows-length-by-one` `+keeps-sortedness` `+adds-one-occurrence` `+preserves-other-counts` `+commutative` `+noop-when-sorted-at-head` |
| 84 | `87dec595a754` | `insert-by` | `insert-by` | defn | `examples/generic.oath:97` | proven | 3/3 | structural | 4/4 | `+grows-length-by-one` `+counts-ledger` `+head-law` |
| 85 | `37c83ea0981d` | `int-embed` | `int-embed` | defn | `examples/convert.oath:11` | proven | 1/1 | nonrecursive | — | `+round-trips` |
| 86 | `2ba7f9f25661` | `is-none` | `is-none` | defn | `examples/records.oath:51` | proven | 2/2 | nonrecursive | 2/2 | `+none-is-none` `+some-is-not-none` |
| 87 | `f3ac0cfaff55` | `is-some` | `is-some` | defn | `examples/records.oath:42` | proven | 2/2 | nonrecursive | 2/2 | `+none-is-not-some` `+some-is-some` |
| 88 | `064d42c23954` | `is-sorted` | `is-sorted` | defn | `examples/sort.oath:33` | proven | 5/5 | structural | 5/5 | `+empty-and-singletons-sorted` `+tail-of-sorted-is-sorted` `+equal-neighbors-sorted` `+detects-inversion` `+head-law` |
| 89 | `dbfc0545b148` | `join-with` | `join-with` | defn | `examples/cli.oath:7` | proven | 3/3 | structural | 3/5 | `+empty-is-empty` `+singleton-is-identity` `+length-accounts` |
| 90 | `457b1fa82b61` | `json-scoped-string` | `json-scoped-string` | defn | `apps/github-webhook/webhook.oath:189` | tested | 0/3 | nonrecursive | — | `-absent-scope-is-marked` `-value-has-no-quote` `-value-has-no-tab` |
| 91 | `e84c7faa721c` | `json-string-value` | `json-string-value` | defn | `apps/github-webhook/webhook.oath:180` | tested | 0/3 | nonrecursive | — | `-no-quote` `-no-tab` `-no-newline` |
| 92 | `3b157b005950` | `kv-get` | `kv-get` | defn | `examples/stateful.oath:14` | proven | 2/2 | structural | 2/2 | `+empty-yields-default` `+bound-head-wins` |
| 93 | `8e45f81f8bf1` | `kv-put` | `kv-put` | defn | `examples/stateful.oath:23` | proven | 3/3 | nonrecursive | — | `+read-your-write` `+frame-other-keys` `+overwrite-last-wins` |
| 94 | `fb2a994de522` | `last` | `last` | defn | `examples/extras.oath:71` | proven | 3/3 | structural | 3/3 | `+empty-is-none` `+singleton` `+step` |
| 95 | `aeac46d7b0e9` | `leak` | `leak` | defn | `examples/leaky.oath:7` | asserted | 0/0 | nonrecursive | — | *(none)* |
| 96 | `a8497c2fac2b` | `length` | `length` | defn | `examples/list.oath:10` | proven | 3/3 | structural | 7/7 | `+non-negative` `+empty-is-zero` `+cons-adds-one` |
| 97 | `bbd85a6e3d20` | `lengths` | `lengths` | defn | `examples/cli.oath:22` | proven | 1/1 | structural | 1/1 | `+preserves-length` |
| 98 | `af979cf69be0` | `list-eq-by` | `list-eq-by` | defn | `examples/generic.oath:44` | proven | 4/4 | structural | 7/7 | `+nils-equal` `+length-mismatch-differs` `+cons-law` `+two-deep-orientation` |
| 99 | `9b19e91f0820` | `main-echo` | `main-echo` | defn | `examples/cli.oath:31` | proven | 2/2 | nonrecursive | 62/101 | `+no-args-is-tagged` `+single-arg-echoes` |
| 100 | `8b9c09a60d03` | `main-fetch` | `main-fetch` | defn | `examples/netcli.oath:7` | proven | 3/3 | nonrecursive | 70/70 | `+no-args-usage` `+fetches-first-arg` `+world-deterministic` |
| 101 | `d82c082fdb53` | `map` | `map` | defn | `examples/list.oath:37` | proven | 1/1 | structural | 1/1 | `+preserves-length` |
| 102 | `09cf1ccec66b` | `map-empty` | `map-empty` | defn | `examples/map.oath:56` | tested | 0/1 | nonrecursive | — | `-is-empty` |
| 103 | `895fa0dbd9fb` | `map-err` | `map-err` | defn | `examples/records.oath:75` | proven | 2/2 | nonrecursive | — | `+ok-passes-through` `+err-maps` |
| 104 | `958230af579c` | `map-has` | `map-has` | defn | `examples/map.oath:65` | tested | 0/1 | nonrecursive | 2/2 | `-present-after-insert` |
| 105 | `d572389add28` | `map-insert` | `map-insert` | defn | `examples/map.oath:60` | tested | 0/1 | nonrecursive | 1/1 | `-finds-inserted` |
| 106 | `89b0724159ec` | `map-keys` | `map-keys` | defn | `examples/map.oath:74` | tested | 0/1 | nonrecursive | — | `-counts-entries` |
| 107 | `937d56cee9ad` | `map-lookup` | `map-lookup` | defn | `examples/map.oath:51` | tested | 0/1 | nonrecursive | — | `-empty-has-none` |
| 108 | `a4bcf8b7fee8` | `map-merge` | `map-merge` | defn | `examples/map.oath:82` | tested | 0/2 | nonrecursive | 1/1 | `-empty-left` `-prefers-left` |
| 109 | `c68fdd467a0a` | `map-option` | `map-option` | defn | `examples/records.oath:24` | proven | 2/2 | nonrecursive | 1/1 | `+none-stays-none` `+some-maps` |
| 110 | `e2805294c809` | `map-result` | `map-result` | defn | `examples/records.oath:66` | proven | 2/2 | nonrecursive | — | `+ok-maps` `+err-passes-through` |
| 111 | `1d96b6899013` | `map-size` | `map-size` | defn | `examples/map.oath:70` | tested | 0/1 | nonrecursive | — | `-non-negative` |
| 112 | `ac0ad30f71d3` | `map-values` | `map-values` | defn | `examples/map.oath:78` | tested | 0/1 | nonrecursive | — | `-counts-entries` |
| 113 | `4c8f05ccdd56` | `max-by` | `max-by` | defn | `examples/generic.oath:89` | proven | 2/2 | nonrecursive | 2/2 | `+returns-an-argument` `+complements-min` |
| 114 | `04416a62699b` | `max2` | `max2` | defn | `examples/extras.oath:1` | proven | 4/4 | nonrecursive | 2/3 | `+ge-left` `+ge-right` `+is-one-of` `+picks-larger` |
| 115 | `6cce19284ea0` | `maximum` | `maximum` | defn | `examples/list.oath:80` | proven | 3/3 | structural | 4/5 | `+empty-is-seed` `+cons-step` `+ge-seed` |
| 116 | `7fd44d4b3b95` | `media-type-is` | `media-type-is` | defn | `apps/github-webhook/webhook.oath:534` | tested | 0/3 | nonrecursive | — | `-exact-matches` `-parameters-match` `-a-longer-type-is-not-a-match` |
| 117 | `be2f6d41596a` | `merge` | `merge` | defn | `examples/merge.oath:11` | proven | 3/3 | structural | 8/11 | `+length-adds` `+preserves-counts` `+keeps-sortedness` |
| 118 | `14fec16a5691` | `mi-insert` | `mi-insert` | defn | `examples/map.oath:20` | tested | 0/1 | structural | 6/10 | `-finds` |
| 119 | `57cdd57a0160` | `mi-keys` | `mi-keys` | defn | `examples/map.oath:30` | proven | 1/1 | structural | 1/1 | `+preserves-length` |
| 120 | `a626e5d5b527` | `mi-lookup` | `mi-lookup` | defn | `examples/map.oath:13` | tested | 0/1 | structural | 0/5 | `-empty` |
| 121 | `f669a4ca27c8` | `mi-merge` | `mi-merge` | defn | `examples/map.oath:45` | tested | 0/1 | structural | 0/5 | `-empty-left` |
| 122 | `41bedb35fab2` | `mi-values` | `mi-values` | defn | `examples/map.oath:37` | proven | 1/1 | structural | 1/1 | `+preserves-length` |
| 123 | `739f8efe0b4e` | `min-by` | `min-by` | defn | `examples/generic.oath:80` | proven | 2/2 | nonrecursive | 2/2 | `+returns-an-argument` `+obeys-the-test` |
| 124 | `9ab35d2eaad1` | `minimum` | `minimum` | defn | `examples/list.oath:93` | proven | 3/3 | structural | 4/5 | `+empty-is-seed` `+cons-step` `+le-seed` |
| 125 | `6da67355b5ae` | `no-field-can-inject` | `no-field-can-inject` | defn | `apps/github-webhook/webhook.oath:635` | tested | 0/4 | nonrecursive | — | `-tab-cannot-inject` `-newline-cannot-inject` `-carriage-return-cannot-inject` `-no-control-byte-can-inject` |
| 126 | `256d63e2e1c1` | `one-two-three` | `one-two-three` | defn | `examples/inferred.oath:16` | proven | 2/2 | nonrecursive | 8/8 | `+has-three` `+is-the-chain` |
| 127 | `134cff6b69d5` | `or-else` | `or-else` | defn | `examples/records.oath:13` | proven | 2/2 | nonrecursive | 1/1 | `+none-yields-default` `+some-wins` |
| 128 | `a977ae212309` | `parse-nat` | `parse-nat` | defn | `examples/circle.oath:24` | tested | 0/2 | nonrecursive | 2/2 | `-reads-zero` `-reads-42` |
| 129 | `5492d77957cd` | `parse-nat-go` | `parse-nat-go` | defn | `examples/circle.oath:18` | proven | 1/1 | structural | 0/12 | `+empty` |
| 130 | `e5446da12892` | `path-is` | `path-is` | defn | `apps/github-webhook/webhook.oath:449` | tested | 0/3 | nonrecursive | — | `-exact-matches` `-query-matches` `-prefix-alone-is-not-a-match` |
| 131 | `090cc5373e20` | `pow` | `pow` | defn | `examples/arith.oath:9` | proven | 4/4 | measure | 15/15 | `+zero-is-one` `+unfold-step` `+succ-exponent` `+one-base-is-one` |
| 132 | `c074a26278a9` | `product` | `product` | defn | `examples/list.oath:65` | proven | 3/3 | structural | 4/4 | `+empty-is-one` `+cons-step` `+two-step` |
| 133 | `091c2b59dc4f` | `q-drop` | `q-drop` | defn | `examples/queue.oath:41` | tested | 2/5 | nonrecursive | 5/5 | `+drop-front-nonempty` `-drop-back-only` `+drop-empty` `-drop-is-tail` `-peek-drop-rebuild` |
| 134 | `642d54aa1f8d` | `q-peek` | `q-peek` | defn | `examples/queue.oath:26` | proven | 5/5 | nonrecursive | 2/2 | `+peek-front-nonempty` `+peek-back-only` `+peek-empty` `+peek-nonempty-back-not-none` `+peek-is-head` |
| 135 | `743484c1807d` | `q-push` | `q-push` | defn | `examples/queue.oath:17` | proven | 5/5 | nonrecursive | 1/1 | `+push-appends` `+push-on-empty` `+push-length` `+push-count-pushed` `+push-order-anchor` |
| 136 | `ee89059c3cce` | `q-to-list` | `q-to-list` | defn | `examples/queue.oath:8` | proven | 5/5 | nonrecursive | 1/1 | `+to-list-def` `+to-list-front-only` `+to-list-back-only` `+to-list-length` `+to-list-order-anchor` |
| 137 | `d49edfaf7c34` | `range` | `range` | defn | `examples/extras.oath:183` | proven | 3/3 | measure | 7/7 | `+empty-when-done` `+unfold-step` `+length-is-span` |
| 138 | `e04d7c974bd9` | `rat-add` | `rat-add` | defn | `examples/rat.oath:10` | proven | 2/2 | nonrecursive | 1/1 | `+commutes` `+assoc` |
| 139 | `d60f775729e4` | `rat-floor` | `rat-floor` | defn | `examples/convert.oath:16` | proven | 1/1 | nonrecursive | — | `+is-lower-bound` |
| 140 | `d09a0eb33a7b` | `rat-mul` | `rat-mul` | defn | `examples/rat.oath:16` | proven | 2/2 | nonrecursive | 1/1 | `+commutes` `+distributes` |
| 141 | `c5647f27f669` | `rat-recover` | `rat-recover` | defn | `examples/rat.oath:26` | proven | 1/1 | nonrecursive | 4/4 | `+is-identity` |
| 142 | `d7f475762489` | `record-field` | `record-field` | defn | `apps/github-webhook/webhook.oath:286` | tested | 0/4 | nonrecursive | — | `-printable-passes-through` `-a-tab-is-rejected` `-a-newline-is-rejected` `-never-contains-a-tab` |
| 143 | `68e180cd139d` | `record-is-well-formed` | `record-is-well-formed` | defn | `apps/github-webhook/webhook.oath:585` | tested | 0/4 | nonrecursive | — | `-five-clean-fields-are-well-formed` `-six-fields-are-not` `-four-fields-are-not` `-a-newline-is-not` |
| 144 | `2bed447f1a3e` | `record-under` | `record-under` | defn | `apps/github-webhook/webhook.oath:596` | tested | 0/3 | nonrecursive | — | `-clean-values-are-well-formed` `-clean-values-reach-the-record` `-a-non-ascii-repository-is-marked-absent` |
| 145 | `be82b31a32c1` | `rename-key` | `rename-key` | defn | `examples/stateful.oath:38` | proven | 2/2 | nonrecursive | — | `+destination-holds-source` `+source-untouched-when-distinct` |
| 146 | `3fb40b27e9dc` | `replicate` | `replicate` | defn | `examples/extras.oath:170` | proven | 3/3 | measure | 9/9 | `+zero-is-empty` `+unfold-step` `+length-is-n` |
| 147 | `55607d7d5608` | `req-body` | `req-body` | defn | `examples/http.oath:64` | proven | 1/1 | nonrecursive | — | `+reads-body` |
| 148 | `11e9cdb9a695` | `req-headers` | `req-headers` | defn | `examples/http.oath:59` | proven | 1/1 | nonrecursive | — | `+reads-headers` |
| 149 | `b0c4a59b92c4` | `req-method` | `req-method` | defn | `examples/http.oath:49` | proven | 1/1 | nonrecursive | — | `+reads-method` |
| 150 | `eadb605e522c` | `req-path` | `req-path` | defn | `examples/http.oath:54` | proven | 1/1 | nonrecursive | — | `+reads-path` |
| 151 | `ea71fc7b5412` | `req-received-at` | `req-received-at` | defn | `examples/http.oath:69` | proven | 1/1 | nonrecursive | — | `+reads-received-at` |
| 152 | `7bb6285884d0` | `reverse` | `reverse` | defn | `examples/list.oath:44` | proven | 2/2 | structural | 3/3 | `+involution` `+antidistributes-over-append` |
| 153 | `cb57f3242430` | `reverse-onto` | `reverse-onto` | defn | `examples/list.oath:170` | proven | 3/3 | structural | 4/4 | `+empty-returns-acc` `+cons-step` `+matches-reverse-append` |
| 154 | `27a8605b1e12` | `rle-decode` | `rle-decode` | defn | `examples/rle.oath:15` | proven | 4/4 | structural | 2/2 | `+nil-decodes-empty` `+cons-law` `+distributes-over-append` `+anchor-with-zero-run` |
| 155 | `253440f0e899` | `rle-encode` | `rle-encode` | defn | `examples/rle.oath:24` | tested | 3/6 | structural | 14/14 | `-roundtrip` `+never-longer-than-input` `-uniform-list-is-one-run` `-two-runs-stay-two-runs` `+duplicate-head-merges` `+anchor-mixed` |
| 156 | `36fb384d7458` | `rle-expand` | `rle-expand` | defn | `examples/rle.oath:3` | proven | 5/5 | measure | 10/10 | `+nonpositive-is-empty` `+peel-one` `+length-is-count-arg` `+every-element-is-v` `+anchor-three-sevens` |
| 157 | `e715328a6ffa` | `rot` | `rot`, `rot-f` | defn | `examples/rot.oath:1`<br>`examples/rot_f.oath:1` | tested | 3/7 | nonrecursive | 20/22 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `-decomposes-in-range` |
| 158 | `48a856f52ace` | `rot-h2` | `rot-h2` | defn | `examples/rot_h2.oath:1` | tested | 3/7 | nonrecursive | 9/9 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `-decomposes-in-range` |
| 159 | `18228c2a22fa` | `rot-h3` | `rot-h3` | defn | `examples/rot_h3.oath:1` | tested | 4/7 | nonrecursive | 10/12 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `+decomposes-in-range` |
| 160 | `e9b80a4dbecf` | `rot-hl` | `rot-hl` | defn | `examples/rot_hl.oath:1` | tested | 4/7 | nonrecursive | 10/12 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `+decomposes-in-range` |
| 161 | `24800a6a9058` | `safe-get` | `safe-get` | defn | `examples/stateful.oath:52` | proven | 2/2 | nonrecursive | 1/1 | `+down-is-none` `+up-is-some` |
| 162 | `5365b4a06be5` | `secret-is-usable` | `secret-is-usable` | defn | `apps/github-webhook/webhook.oath:385` | tested | 0/5 | nonrecursive | — | `-empty-is-not-usable` `-short-is-not-usable` `-non-ascii-is-not-usable` `-latin1-is-not-usable` `-usable-encodes` |
| 163 | `874a3cb5d3f6` | `set-add` | `set-add` | defn | `examples/set.oath:49` | tested | 0/2 | nonrecursive | — | `-adds-member` `-add-idempotent` |
| 164 | `a91bbcd741e2` | `set-elems` | `set-elems` | defn | `examples/set.oath:70` | tested | 0/1 | nonrecursive | — | `-size-is-length` |
| 165 | `d067aca7a83d` | `set-empty` | `set-empty` | defn | `examples/set.oath:45` | tested | 0/1 | nonrecursive | — | `-has-nothing` |
| 166 | `8a059a2f7b56` | `set-inter` | `set-inter` | defn | `examples/set.oath:60` | tested | 0/1 | nonrecursive | 0/1 | `-empty-left` |
| 167 | `00b50eb902ce` | `set-member` | `set-member` | defn | `examples/set.oath:41` | tested | 0/1 | nonrecursive | — | `-empty-has-none` |
| 168 | `c73b49a04d37` | `set-size` | `set-size` | defn | `examples/set.oath:64` | tested | 0/1 | nonrecursive | — | `-non-negative` |
| 169 | `c2b36fe59543` | `set-union` | `set-union` | defn | `examples/set.oath:54` | tested | 0/2 | nonrecursive | 1/1 | `-empty-left` `-union-has-left` |
| 170 | `1788554af98a` | `shout` | `shout` | defn | `examples/records.oath:93` | proven | 2/2 | nonrecursive | 0/4 | `+grows-by-one` `+preserves-emptiness-never` |
| 171 | `a9472d7aa214` | `show-int` | `show-int` | defn | `examples/circle.oath:37` | tested | 0/1 | nonrecursive | 4/8 | `-roundtrip` |
| 172 | `1d0a9ecfb0ea` | `show-nat` | `show-nat` | defn | `examples/circle.oath:30` | proven | 1/1 | measure | 4/25 | `+single-digit` |
| 173 | `e3ba3eec0cba` | `si-insert` | `si-insert` | defn | `examples/set.oath:20` | proven | 3/3 | structural | 6/6 | `+commutes` `+idempotent` `+adds` |
| 174 | `32765987195d` | `si-inter` | `si-inter` | defn | `examples/set.oath:35` | proven | 1/1 | structural | 0/8 | `+empty-left` |
| 175 | `47e842f8daf5` | `si-member` | `si-member` | defn | `examples/set.oath:14` | proven | 1/1 | structural | 0/5 | `+empty-has-none` |
| 176 | `63f8d391e13d` | `si-union` | `si-union` | defn | `examples/set.oath:31` | proven | 1/1 | structural | 0/4 | `+empty-left` |
| 177 | `7c0ff56ae894` | `sign` | `sign` | defn | `examples/ints.oath:15` | proven | 2/2 | nonrecursive | 10/16 | `+bounded` `+reconstructs` |
| 178 | `14b8a3dd9719` | `singleton` | `singleton` | defn | `examples/inferred.oath:10` | proven | 2/2 | nonrecursive | — | `+is-one-element` `+unfolds` |
| 179 | `2248e151c30a` | `snoc` | `snoc` | defn | `examples/extras.oath:46` | proven | 3/3 | structural | 2/2 | `+single` `+cons-step` `+as-append` |
| 180 | `8ed4b7e956f3` | `sort` | `sort` | defn | `examples/sort.oath:90` | proven | 7/7 | structural | 2/2 | `+output-is-sorted` `+preserves-length` `+preserves-counts` `+snoc-is-insert` `+sorted-is-fixpoint` `+idempotent` `+reverse-invariant` |
| 181 | `5179ba32bb8d` | `sort-by` | `sort-by` | defn | `examples/generic.oath:123` | proven | 3/3 | structural | 2/2 | `+preserves-length` `+preserves-counts` `+unfolds-by-insertion` |
| 182 | `086cb1f93353` | `spin` | `spin` | defn | `examples/nontotal.oath:7` | falsified | 0/1 | unknown | 1/1 | `-claims-zero` |
| 183 | `b25520e6a37a` | `spin-partial` | `spin-partial` | defn | `examples/exclusion.oath:33` | asserted | 0/0 | unknown | — | *(none)* |
| 184 | `be38139f4560` | `stash` | `stash` | defn | `examples/leaky.oath:10` | asserted | 0/0 | nonrecursive | — | *(none)* |
| 185 | `7d158d0455d3` | `str-append` | `str-append` | defn | `examples/str.oath:21` | proven | 2/2 | structural | 3/4 | `+left-unit` `+length-adds` |
| 186 | `cc13f713e301` | `str-bytes` | `str-bytes` | defn | `examples/http.oath:143` | proven | 2/2 | structural | 1/1 | `+empty-maps-to-empty` `+preserves-length` |
| 187 | `caaaa5b2134a` | `str-drop` | `str-drop` | defn | `examples/str.oath:50` | proven | 2/2 | structural | 11/11 | `+drop-zero` `+take-drop-rebuilds` |
| 188 | `b41104b62aae` | `str-join` | `str-join` | defn | `examples/str.oath:74` | proven | 1/1 | structural | 1/3 | `+single` |
| 189 | `30df3863fbb1` | `str-len` | `str-len` | defn | `examples/str.oath:14` | proven | 1/1 | structural | 2/7 | `+nonneg` |
| 190 | `48d690bda44b` | `str-prefix` | `str-prefix` | defn | `examples/str.oath:30` | proven | 3/3 | structural | 3/4 | `+self` `+empty` `+append-is-prefixed` |
| 191 | `0292b5f64edd` | `str-split` | `str-split` | defn | `examples/str.oath:63` | proven | 1/1 | structural | 0/3 | `+never-empty` |
| 192 | `f3a62459d59a` | `str-split-join` | `str-split-join` | defn | `examples/str.oath:86` | proven | 1/1 | nonrecursive | — | `+roundtrip` |
| 193 | `77452b35b089` | `str-take` | `str-take` | defn | `examples/str.oath:42` | proven | 1/1 | structural | 3/11 | `+take-zero` |
| 194 | `25f8a4de0cc7` | `sum` | `sum` | defn | `examples/list.oath:54` | proven | 2/2 | structural | 3/4 | `+distributes-over-append` `+reverse-invariant` |
| 195 | `b00e9f7d7c2d` | `swap` | `swap` | defn | `examples/inferred.oath:23` | proven | 1/1 | nonrecursive | — | `+swap-involutive` |
| 196 | `a431dd53a6b3` | `t-flatten` | `t-flatten` | defn | `examples/tree.oath:16` | proven | 3/3 | structural | 2/2 | `+flatten-leaf` `+flatten-node` `+flatten-singleton` |
| 197 | `312101f6dfd4` | `t-insert` | `t-insert` | defn | `examples/tree.oath:27` | tested | 4/5 | structural | 6/6 | `+insert-count-inserted` `+insert-count-others` `+insert-length` `-insert-keeps-sorted` `+insert-dup-goes-right` |
| 198 | `2b71f64d6f4d` | `t-member` | `t-member` | defn | `examples/tree.oath:47` | tested | 3/4 | structural | 4/5 | `+member-leaf` `+member-root` `+member-insert-finds` `-member-flatten-equiv` |
| 199 | `1fc6a47774e2` | `t-size` | `t-size` | defn | `examples/tree.oath:60` | proven | 4/4 | structural | 7/7 | `+size-leaf` `+size-node` `+size-flatten-length` `+size-insert` |
| 200 | `1cca17c6d090` | `take` | `take` | defn | `examples/extras.oath:26` | proven | 4/4 | structural | 11/11 | `+take-then-drop-rebuilds` `+take-zero` `+take-one` `+take-two` |
| 201 | `0160610e8287` | `take-while` | `take-while` | defn | `examples/list.oath:229` | proven | 2/2 | structural | 3/3 | `+empty-is-empty` `+cons-step` |
| 202 | `8fd0dda44666` | `tenth-f` | `tenth-f` | defn | `examples/convert.oath:28` | proven | 1/1 | nonrecursive | — | `+is-point-one` |
| 203 | `8364dad02bbc` | `third-f` | `third-f` | defn | `examples/convert.oath:35` | proven | 1/1 | nonrecursive | — | `+rounds-nearest` |
| 204 | `19d25f94f919` | `unwrap-or` | `unwrap-or` | defn | `examples/records.oath:84` | proven | 2/2 | nonrecursive | — | `+ok-unwraps` `+err-defaults` |
| 205 | `04241aba6e93` | `webhook` | `webhook` | defn | `examples/webhook.oath:174` | tested | 0/5 | nonrecursive | 84/176 | `-unsigned-is-401` `-accepts-only-with-202` `-accepts-correctly-signed` `-tampering-is-rejected` `-never-leaks-a-body` |
| 206 | `b83924f8160e` | `within-window` | `within-window` | defn | `examples/webhook.oath:130` | proven | 3/3 | nonrecursive | 10/11 | `+same-instant-ok` `+symmetric` `+rejects-beyond` |
| 207 | `8994f7a68c09` | `zip` | `zip` | defn | `examples/extras.oath:101` | proven | 3/3 | structural | 2/2 | `+nil-left` `+nil-right` `+cons-step` |
| 208 | `d55d85255312` | `zip-with` | `zip-with` | defn | `examples/extras.oath:118` | proven | 3/3 | structural | 2/2 | `+nil-left` `+nil-right` `+cons-step` |

## 3. Per-name enumeration

One row per live name, sorted by name, with each name's properties spelled as
that name spells them. `rot` and `rot-f` both appear, each with its full seven
properties; that duplication is the point, and removing it would be the defect.

    python3 scripts/corpus-census.py /tmp/census.json names

| # | name | object | alias of | kind | source | level | proven/props | properties (as this name spells them) |
|---:|---|---|---|---|---|---|---:|---|
| 1 | `Flaky` | `4bb5e1cbb831` | — | data | `examples/stateful.oath:48` | asserted | 0/0 | *(none)* |
| 2 | `Interval` | `f103d27ee151` | `Run` | data | `examples/interval.oath:10` | asserted | 0/0 | *(none)* |
| 3 | `KV` | `a88fcdcbeae3` | — | data | `examples/stateful.oath:10` | asserted | 0/0 | *(none)* |
| 4 | `List` | `fa452d59a235` | — | data | `examples/list.oath:6` | asserted | 0/0 | *(none)* |
| 5 | `Map` | `a9a8fb7ed67a` | — | data | `examples/map.oath:11` | asserted | 0/0 | *(none)* |
| 6 | `Option` | `895777a9ff55` | — | data | `examples/records.oath:6` | asserted | 0/0 | *(none)* |
| 7 | `Pair` | `3c3d94c1dc9e` | — | data | `examples/records.oath:10` | asserted | 0/0 | *(none)* |
| 8 | `Queue` | `0100c76d0cda` | — | data | `examples/queue.oath:6` | asserted | 0/0 | *(none)* |
| 9 | `Request` | `5d12ea7ba780` | — | data | `examples/http.oath:41` | asserted | 0/0 | *(none)* |
| 10 | `Response` | `b40fd2d9d1c6` | — | data | `examples/http.oath:44` | asserted | 0/0 | *(none)* |
| 11 | `Result` | `533302244706` | — | data | `examples/records.oath:62` | asserted | 0/0 | *(none)* |
| 12 | `Run` | `f103d27ee151` | — | data | `examples/rle.oath:1` | asserted | 0/0 | *(none)* |
| 13 | `Set` | `16a9d47302b9` | — | data | `examples/set.oath:12` | asserted | 0/0 | *(none)* |
| 14 | `Str` | `e6bbed8bc934` | — | data | `examples/str.oath:9` | asserted | 0/0 | *(none)* |
| 15 | `Tree` | `003bea7bfc59` | — | data | `examples/tree.oath:12` | asserted | 0/0 | *(none)* |
| 16 | `abs` | `f8552a9f06ee` | — | defn | `examples/ints.oath:6` | proven | 3/3 | `+non-negative` `+idempotent` `+is-x-or-negx` |
| 17 | `abs-small` | `ca9250f107c3` | — | defn | `examples/undertested.oath:6` | tested | 0/1 | `-bounded-wrongly` |
| 18 | `all` | `966495ef1b6f` | — | defn | `examples/list.oath:202` | proven | 2/2 | `+empty-is-true` `+cons-step` |
| 19 | `any` | `dd0648da7466` | — | defn | `examples/list.oath:217` | proven | 2/2 | `+empty-is-false` `+cons-step` |
| 20 | `append` | `78d23e273a48` | — | defn | `examples/list.oath:24` | proven | 3/3 | `+length-adds` `+nil-right-identity` `+associative` |
| 21 | `apply2` | `6c0735984f65` | — | defn | `examples/exclusion.oath:30` | asserted | 0/0 | *(none)* |
| 22 | `bad-reverse` | `af6b61e2180a` | — | defn | `examples/bad_reverse.oath:7` | falsified | 0/2 | `-involution` `-antidistributes-over-append` |
| 23 | `bytes-after` | `1d2158287a2c` | — | defn | `apps/github-webhook/webhook.oath:150` | tested | 0/2 | `-missing-is-none` `-finds-at-head` |
| 24 | `bytes-ok` | `d2406871baf1` | — | defn | `examples/http.oath:132` | proven | 3/3 | `+empty-is-ok` `+rejects-negative` `+rejects-oversized` |
| 25 | `bytes-prefix` | `6254197a78fe` | — | defn | `apps/github-webhook/webhook.oath:133` | proven | 3/3 | `+empty-is-prefix` `+nothing-precedes-empty` `+self` |
| 26 | `bytes-str` | `066e843a31cd` | — | defn | `apps/github-webhook/webhook.oath:125` | proven | 2/2 | `+empty-is-empty` `+inverts-str-bytes` |
| 27 | `caps-with` | `b63e7163b5dd` | — | defn | `apps/github-webhook/webhook.oath:557` | tested | 0/2 | `-provides-the-secret` `-emit-is-the-sink` |
| 28 | `check-config` | `3bb186fa4728` | — | defn | `examples/config.oath:62` | proven | 2/2 | `+no-args-is-usage` `+reports-ok-or-missing` |
| 29 | `circle` | `b9df489c7e77` | — | defn | `examples/circle.oath:48` | tested | 0/2 | `-r5` `-r10` |
| 30 | `clamp` | `9fa25283f279` | — | defn | `examples/ints.oath:22` | proven | 3/3 | `+within-bounds` `+idempotent` `+identity-inside` |
| 31 | `config-has-key` | `026c7502118d` | — | defn | `examples/config.oath:31` | tested | 2/3 | `+empty-has-nothing` `-finds-head` `+skips-mismatch` |
| 32 | `config-key` | `7995ac55afc6` | — | defn | `examples/config.oath:23` | proven | 3/3 | `+reads-key-before-eq` `+whole-line-when-no-eq` `+empty-stays-empty` |
| 33 | `config-missing` | `8c9e095b0d72` | — | defn | `examples/config.oath:44` | tested | 2/3 | `+nothing-required-is-complete` `+reports-a-missing-key` `-complete-config-reports-nothing` |
| 34 | `contains` | `6221bec8324b` | — | defn | `examples/extras.oath:37` | proven | 3/3 | `+found-at-head` `+absent-from-empty` `+agrees-with-count` |
| 35 | `count` | `040c290d0c35` | — | defn | `examples/sort.oath:6` | proven | 2/2 | `+non-negative` `+bounded-by-length` |
| 36 | `count-append` | `ed0b40d5d68f` | — | defn | `examples/sort.oath:28` | proven | 1/1 | `+splits-over-append` |
| 37 | `count-by` | `851d636b9476` | — | defn | `examples/generic.oath:25` | proven | 3/3 | `+non-negative` `+bounded-by-length` `+cons-ledger` |
| 38 | `count-matching` | `c2f6fe6558ca` | — | defn | `examples/list.oath:251` | proven | 3/3 | `+empty-is-zero` `+non-negative` `+cons-step` |
| 39 | `drop` | `f1e861a8d7ed` | — | defn | `examples/extras.oath:12` | proven | 4/4 | `+drop-all` `+drop-zero` `+drop-one` `+drop-length` |
| 40 | `drop-while` | `cedba00b3ab7` | — | defn | `examples/list.oath:240` | proven | 2/2 | `+empty-is-empty` `+cons-step` |
| 41 | `e-div` | `100733cab4b2` | — | defn | `examples/ediv.oath:14` | tested | 3/5 | `-division-identity` `+zero-divisor-is-zero` `-shift-by-divisor` `+negate-divisor-negates` `+quadrant-anchors` |
| 42 | `e-mod` | `362696e75c13` | — | defn | `examples/ediv.oath:1` | tested | 5/6 | `+nonneg` `+below-abs-divisor` `+zero-divisor-is-a` `-periodic` `+divisor-sign-irrelevant` `+quadrant-anchors` |
| 43 | `echo-handler` | `604e3e90c2ff` | — | defn | `examples/http.oath:115` | proven | 3/3 | `+echoes-body-exactly` `+always-200` `+reports-method` |
| 44 | `embed-add` | `817f85803f2a` | — | defn | `examples/convert.oath:21` | proven | 1/1 | `+additive` |
| 45 | `excluded-witness` | `f55335261c55` | — | defn | `examples/exclusion.oath:36` | tested | 1/2 | `+independent-of-exclusion` `-reaches-excluded-op` |
| 46 | `f-double` | `135e96990463` | — | defn | `examples/float.oath:18` | proven | 1/1 | `+is-scale` |
| 47 | `f-mul-id` | `4b136716aca7` | — | defn | `examples/float.oath:12` | proven | 1/1 | `+identity` |
| 48 | `f-scale-inv` | `fec8b5271ba3` | — | defn | `examples/float.oath:33` | falsified | 0/1 | `-recovers` |
| 49 | `f-tenths` | `18889b76c77e` | — | defn | `examples/float.oath:25` | falsified | 0/1 | `-is-three-tenths` |
| 50 | `fib` | `7ce4d64ce4a0` | — | defn | `examples/arith.oath:28` | proven | 4/4 | `+base-zero` `+base-one` `+unfold-step` `+nonneg` |
| 51 | `filter` | `cc825b87e888` | — | defn | `examples/list.oath:105` | proven | 2/2 | `+empty-is-empty` `+cons-step` |
| 52 | `find` | `bce060be0e7f` | — | defn | `examples/extras.oath:59` | proven | 2/2 | `+empty-is-none` `+cons-step` |
| 53 | `first-or` | `775f5b2a57d7` | — | defn | `examples/circle.oath:43` | tested | 0/1 | `-empty` |
| 54 | `flat-map-option` | `08cce2f0575f` | — | defn | `examples/records.oath:33` | proven | 2/2 | `+none-stays-none` `+some-applies` |
| 55 | `flatten` | `0497351b656c` | — | defn | `examples/list.oath:186` | proven | 3/3 | `+empty-is-empty` `+cons-step` `+distributes-over-append` |
| 56 | `foldl` | `987c33ad5770` | — | defn | `examples/list.oath:156` | proven | 3/3 | `+empty-is-seed` `+cons-step` `+two-step` |
| 57 | `foldr` | `5b345e9d645f` | — | defn | `examples/list.oath:137` | proven | 3/3 | `+empty-is-seed` `+cons-step` `+two-step` |
| 58 | `full-name` | `c77f088bed04` | — | defn | `examples/records.oath:100` | proven | 2/2 | `+length-adds-up` `+starts-from-parts` |
| 59 | `gh-record` | `53219da89135` | — | defn | `apps/github-webhook/webhook.oath:299` | tested | 0/3 | `-has-five-fields` `-declares-its-schema` `-never-empty` |
| 60 | `gh-request` | `dc9d0dd11c56` | — | defn | `apps/github-webhook/webhook.oath:507` | tested | 0/3 | `-is-a-post` `-keeps-the-path` `-keeps-the-body` |
| 61 | `gh-sign` | `bf2bf8a950ef` | — | defn | `apps/github-webhook/webhook.oath:483` | tested | 0/2 | `-carries-the-algorithm` `-survives-gh-signature` |
| 62 | `gh-signature` | `4a5152b8c8eb` | — | defn | `apps/github-webhook/webhook.oath:42` | tested | 0/4 | `-absent-header-is-none` `-unprefixed-is-rejected` `-strips-exactly-the-prefix` `-wrong-length-is-rejected` |
| 63 | `gh-spec-secret` | `64468c9d46cc` | — | defn | `apps/github-webhook/webhook.oath:478` | tested | 0/1 | `-is-usable` |
| 64 | `gh-webhook` | `552ec5bd9d18` | — | defn | `apps/github-webhook/webhook.oath:659` | tested | 0/14 | `-status-is-one-of-seven` `-never-leaks-a-body` `-non-post-is-405` `-unusable-secret-refuses-everything` `-unusable-secret-refuses-a-valid-signature` `-unsigned-is-401` `-tampering-is-rejected` `-unprefixed-signature-is-rejected` `-trailing-junk-after-a-valid-digest-is-rejected` `-wrong-path-is-404` `-unreadable-content-type-is-415` `-accepts-github-signed` `-ping-does-not-record` `-a-non-ping-does-record` |
| 65 | `greet` | `1b4fc08a4e93` | — | defn | `examples/service.oath:7` | proven | 3/3 | `+exact-shape` `+never-shorter-than-frame` `+same-world-same-answer` |
| 66 | `greet-or-guest` | `1da62c4f6353` | — | defn | `examples/service.oath:16` | proven | 2/2 | `+none-means-guest` `+some-passes-through` |
| 67 | `hdr-probe` | `f19d388b9584` | — | defn | `apps/github-webhook/hdr-probe.oath:41` | tested | 0/1 | `-always-200` |
| 68 | `header-first` | `f0a62a28b4b4` | — | defn | `examples/http.oath:84` | proven | 3/3 | `+empty-has-none` `+finds-head` `+skips-mismatch` |
| 69 | `header-or` | `d585aa88cdfb` | — | defn | `apps/github-webhook/webhook.oath:87` | tested | 0/2 | `-absent-yields-fallback` `-present-yields-value` |
| 70 | `hex-decode` | `e98335ea6c14` | — | defn | `examples/webhook.oath:106` | proven | 7/7 | `+empty-decodes-empty` `+decodes-ff` `+decodes-00` `+invalid-fails-closed` `+trailing-junk-fails-closed` `+a-bad-high-nibble-fails-closed` `+a-bad-low-nibble-fails-closed` |
| 71 | `hex-decode-unchecked` | `c46f1fb5eecb` | — | defn | `examples/webhook.oath:90` | proven | 5/5 | `+empty-decodes-empty` `+decodes-ff` `+decodes-00` `+decodes-both-nibbles` `+decodes-a-pair-of-pairs` |
| 72 | `hex-digit` | `007871398c18` | — | defn | `examples/webhook.oath:148` | tested | 0/3 | `-zero-is-char-0` `-ten-is-char-a` `-roundtrips` |
| 73 | `hex-encode` | `85e837e0fa9b` | — | defn | `examples/webhook.oath:155` | tested | 0/3 | `-empty-encodes-empty` `-encodes-255` `-roundtrips-through-decode` |
| 74 | `hex-nibble` | `548c2b120a8b` | — | defn | `examples/webhook.oath:29` | proven | 2/2 | `+digits` `+rejects-non-hex` |
| 75 | `hex-valid` | `954c1887f94a` | — | defn | `examples/webhook.oath:56` | proven | 7/7 | `+empty-is-valid` `+pair-is-valid` `+odd-length-is-invalid` `+trailing-junk-is-invalid` `+zero-pair-is-valid` `+a-bad-high-nibble-is-invalid` `+a-bad-low-nibble-is-invalid` |
| 76 | `hmac-kat-rfc4231-2` | `d497b8473178` | — | defn | `examples/webhook.oath:271` | tested | 0/3 | `-matches-published-vector` `-arguments-are-not-symmetric` `-length-mismatch-is-not-equal` |
| 77 | `i-contains` | `3fc1c9e49884` | — | defn | `examples/interval.oath:13` | proven | 4/4 | `+contains-def` `+contains-endpoints` `+contains-empty-none` `+contains-outside-false` |
| 78 | `i-hull` | `7ad9cbd2d649` | — | defn | `examples/interval.oath:57` | proven | 5/5 | `+hull-contains-both` `+hull-tight-endpoints` `+hull-empty-left` `+hull-empty-right` `+hull-bounds-when-nonempty` |
| 79 | `i-intersect` | `416798295f35` | — | defn | `examples/interval.oath:42` | proven | 4/4 | `+intersect-members` `+intersect-empty-iff-disjoint` `+intersect-sym-members` `+intersect-bounds-when-overlapping` |
| 80 | `i-overlaps` | `ba067f27d425` | — | defn | `examples/interval.oath:24` | proven | 5/5 | `+overlaps-def` `+overlaps-witness-sound` `+overlaps-complete` `+overlaps-touching` `+overlaps-empty-left` |
| 81 | `index-of` | `d679d2e21f62` | — | defn | `examples/extras.oath:139` | proven | 3/3 | `+empty-none` `+found-at-head` `+miss-step` |
| 82 | `init` | `6ed53caa0ad2` | — | defn | `examples/extras.oath:86` | proven | 3/3 | `+empty-is-empty` `+singleton` `+step` |
| 83 | `initials-or` | `ae876364d0f2` | — | defn | `examples/records.oath:108` | proven | 2/2 | `+none-falls-back` `+some-formats` |
| 84 | `insert` | `07e9fddfeed3` | — | defn | `examples/sort.oath:63` | proven | 6/6 | `+grows-length-by-one` `+keeps-sortedness` `+adds-one-occurrence` `+preserves-other-counts` `+commutative` `+noop-when-sorted-at-head` |
| 85 | `insert-by` | `87dec595a754` | — | defn | `examples/generic.oath:97` | proven | 3/3 | `+grows-length-by-one` `+counts-ledger` `+head-law` |
| 86 | `int-embed` | `37c83ea0981d` | — | defn | `examples/convert.oath:11` | proven | 1/1 | `+round-trips` |
| 87 | `is-none` | `2ba7f9f25661` | — | defn | `examples/records.oath:51` | proven | 2/2 | `+none-is-none` `+some-is-not-none` |
| 88 | `is-some` | `f3ac0cfaff55` | — | defn | `examples/records.oath:42` | proven | 2/2 | `+none-is-not-some` `+some-is-some` |
| 89 | `is-sorted` | `064d42c23954` | — | defn | `examples/sort.oath:33` | proven | 5/5 | `+empty-and-singletons-sorted` `+tail-of-sorted-is-sorted` `+equal-neighbors-sorted` `+detects-inversion` `+head-law` |
| 90 | `join-with` | `dbfc0545b148` | — | defn | `examples/cli.oath:7` | proven | 3/3 | `+empty-is-empty` `+singleton-is-identity` `+length-accounts` |
| 91 | `json-scoped-string` | `457b1fa82b61` | — | defn | `apps/github-webhook/webhook.oath:189` | tested | 0/3 | `-absent-scope-is-marked` `-value-has-no-quote` `-value-has-no-tab` |
| 92 | `json-string-value` | `e84c7faa721c` | — | defn | `apps/github-webhook/webhook.oath:180` | tested | 0/3 | `-no-quote` `-no-tab` `-no-newline` |
| 93 | `kv-get` | `3b157b005950` | — | defn | `examples/stateful.oath:14` | proven | 2/2 | `+empty-yields-default` `+bound-head-wins` |
| 94 | `kv-put` | `8e45f81f8bf1` | — | defn | `examples/stateful.oath:23` | proven | 3/3 | `+read-your-write` `+frame-other-keys` `+overwrite-last-wins` |
| 95 | `last` | `fb2a994de522` | — | defn | `examples/extras.oath:71` | proven | 3/3 | `+empty-is-none` `+singleton` `+step` |
| 96 | `leak` | `aeac46d7b0e9` | — | defn | `examples/leaky.oath:7` | asserted | 0/0 | *(none)* |
| 97 | `length` | `a8497c2fac2b` | — | defn | `examples/list.oath:10` | proven | 3/3 | `+non-negative` `+empty-is-zero` `+cons-adds-one` |
| 98 | `lengths` | `bbd85a6e3d20` | — | defn | `examples/cli.oath:22` | proven | 1/1 | `+preserves-length` |
| 99 | `list-eq-by` | `af979cf69be0` | — | defn | `examples/generic.oath:44` | proven | 4/4 | `+nils-equal` `+length-mismatch-differs` `+cons-law` `+two-deep-orientation` |
| 100 | `main-echo` | `9b19e91f0820` | — | defn | `examples/cli.oath:31` | proven | 2/2 | `+no-args-is-tagged` `+single-arg-echoes` |
| 101 | `main-fetch` | `8b9c09a60d03` | — | defn | `examples/netcli.oath:7` | proven | 3/3 | `+no-args-usage` `+fetches-first-arg` `+world-deterministic` |
| 102 | `map` | `d82c082fdb53` | — | defn | `examples/list.oath:37` | proven | 1/1 | `+preserves-length` |
| 103 | `map-empty` | `09cf1ccec66b` | — | defn | `examples/map.oath:56` | tested | 0/1 | `-is-empty` |
| 104 | `map-err` | `895fa0dbd9fb` | — | defn | `examples/records.oath:75` | proven | 2/2 | `+ok-passes-through` `+err-maps` |
| 105 | `map-has` | `958230af579c` | — | defn | `examples/map.oath:65` | tested | 0/1 | `-present-after-insert` |
| 106 | `map-insert` | `d572389add28` | — | defn | `examples/map.oath:60` | tested | 0/1 | `-finds-inserted` |
| 107 | `map-keys` | `89b0724159ec` | — | defn | `examples/map.oath:74` | tested | 0/1 | `-counts-entries` |
| 108 | `map-lookup` | `937d56cee9ad` | — | defn | `examples/map.oath:51` | tested | 0/1 | `-empty-has-none` |
| 109 | `map-merge` | `a4bcf8b7fee8` | — | defn | `examples/map.oath:82` | tested | 0/2 | `-empty-left` `-prefers-left` |
| 110 | `map-option` | `c68fdd467a0a` | — | defn | `examples/records.oath:24` | proven | 2/2 | `+none-stays-none` `+some-maps` |
| 111 | `map-result` | `e2805294c809` | — | defn | `examples/records.oath:66` | proven | 2/2 | `+ok-maps` `+err-passes-through` |
| 112 | `map-size` | `1d96b6899013` | — | defn | `examples/map.oath:70` | tested | 0/1 | `-non-negative` |
| 113 | `map-values` | `ac0ad30f71d3` | — | defn | `examples/map.oath:78` | tested | 0/1 | `-counts-entries` |
| 114 | `max-by` | `4c8f05ccdd56` | — | defn | `examples/generic.oath:89` | proven | 2/2 | `+returns-an-argument` `+complements-min` |
| 115 | `max2` | `04416a62699b` | — | defn | `examples/extras.oath:1` | proven | 4/4 | `+ge-left` `+ge-right` `+is-one-of` `+picks-larger` |
| 116 | `maximum` | `6cce19284ea0` | — | defn | `examples/list.oath:80` | proven | 3/3 | `+empty-is-seed` `+cons-step` `+ge-seed` |
| 117 | `media-type-is` | `7fd44d4b3b95` | — | defn | `apps/github-webhook/webhook.oath:534` | tested | 0/3 | `-exact-matches` `-parameters-match` `-a-longer-type-is-not-a-match` |
| 118 | `merge` | `be2f6d41596a` | — | defn | `examples/merge.oath:11` | proven | 3/3 | `+length-adds` `+preserves-counts` `+keeps-sortedness` |
| 119 | `mi-insert` | `14fec16a5691` | — | defn | `examples/map.oath:20` | tested | 0/1 | `-finds` |
| 120 | `mi-keys` | `57cdd57a0160` | — | defn | `examples/map.oath:30` | proven | 1/1 | `+preserves-length` |
| 121 | `mi-lookup` | `a626e5d5b527` | — | defn | `examples/map.oath:13` | tested | 0/1 | `-empty` |
| 122 | `mi-merge` | `f669a4ca27c8` | — | defn | `examples/map.oath:45` | tested | 0/1 | `-empty-left` |
| 123 | `mi-values` | `41bedb35fab2` | — | defn | `examples/map.oath:37` | proven | 1/1 | `+preserves-length` |
| 124 | `min-by` | `739f8efe0b4e` | — | defn | `examples/generic.oath:80` | proven | 2/2 | `+returns-an-argument` `+obeys-the-test` |
| 125 | `minimum` | `9ab35d2eaad1` | — | defn | `examples/list.oath:93` | proven | 3/3 | `+empty-is-seed` `+cons-step` `+le-seed` |
| 126 | `no-field-can-inject` | `6da67355b5ae` | — | defn | `apps/github-webhook/webhook.oath:635` | tested | 0/4 | `-tab-cannot-inject` `-newline-cannot-inject` `-carriage-return-cannot-inject` `-no-control-byte-can-inject` |
| 127 | `one-two-three` | `256d63e2e1c1` | — | defn | `examples/inferred.oath:16` | proven | 2/2 | `+has-three` `+is-the-chain` |
| 128 | `or-else` | `134cff6b69d5` | — | defn | `examples/records.oath:13` | proven | 2/2 | `+none-yields-default` `+some-wins` |
| 129 | `parse-nat` | `a977ae212309` | — | defn | `examples/circle.oath:24` | tested | 0/2 | `-reads-zero` `-reads-42` |
| 130 | `parse-nat-go` | `5492d77957cd` | — | defn | `examples/circle.oath:18` | proven | 1/1 | `+empty` |
| 131 | `path-is` | `e5446da12892` | — | defn | `apps/github-webhook/webhook.oath:449` | tested | 0/3 | `-exact-matches` `-query-matches` `-prefix-alone-is-not-a-match` |
| 132 | `pow` | `090cc5373e20` | — | defn | `examples/arith.oath:9` | proven | 4/4 | `+zero-is-one` `+unfold-step` `+succ-exponent` `+one-base-is-one` |
| 133 | `product` | `c074a26278a9` | — | defn | `examples/list.oath:65` | proven | 3/3 | `+empty-is-one` `+cons-step` `+two-step` |
| 134 | `q-drop` | `091c2b59dc4f` | — | defn | `examples/queue.oath:41` | tested | 2/5 | `+drop-front-nonempty` `-drop-back-only` `+drop-empty` `-drop-is-tail` `-peek-drop-rebuild` |
| 135 | `q-peek` | `642d54aa1f8d` | — | defn | `examples/queue.oath:26` | proven | 5/5 | `+peek-front-nonempty` `+peek-back-only` `+peek-empty` `+peek-nonempty-back-not-none` `+peek-is-head` |
| 136 | `q-push` | `743484c1807d` | — | defn | `examples/queue.oath:17` | proven | 5/5 | `+push-appends` `+push-on-empty` `+push-length` `+push-count-pushed` `+push-order-anchor` |
| 137 | `q-to-list` | `ee89059c3cce` | — | defn | `examples/queue.oath:8` | proven | 5/5 | `+to-list-def` `+to-list-front-only` `+to-list-back-only` `+to-list-length` `+to-list-order-anchor` |
| 138 | `range` | `d49edfaf7c34` | — | defn | `examples/extras.oath:183` | proven | 3/3 | `+empty-when-done` `+unfold-step` `+length-is-span` |
| 139 | `rat-add` | `e04d7c974bd9` | — | defn | `examples/rat.oath:10` | proven | 2/2 | `+commutes` `+assoc` |
| 140 | `rat-floor` | `d60f775729e4` | — | defn | `examples/convert.oath:16` | proven | 1/1 | `+is-lower-bound` |
| 141 | `rat-mul` | `d09a0eb33a7b` | — | defn | `examples/rat.oath:16` | proven | 2/2 | `+commutes` `+distributes` |
| 142 | `rat-recover` | `c5647f27f669` | — | defn | `examples/rat.oath:26` | proven | 1/1 | `+is-identity` |
| 143 | `record-field` | `d7f475762489` | — | defn | `apps/github-webhook/webhook.oath:286` | tested | 0/4 | `-printable-passes-through` `-a-tab-is-rejected` `-a-newline-is-rejected` `-never-contains-a-tab` |
| 144 | `record-is-well-formed` | `68e180cd139d` | — | defn | `apps/github-webhook/webhook.oath:585` | tested | 0/4 | `-five-clean-fields-are-well-formed` `-six-fields-are-not` `-four-fields-are-not` `-a-newline-is-not` |
| 145 | `record-under` | `2bed447f1a3e` | — | defn | `apps/github-webhook/webhook.oath:596` | tested | 0/3 | `-clean-values-are-well-formed` `-clean-values-reach-the-record` `-a-non-ascii-repository-is-marked-absent` |
| 146 | `rename-key` | `be82b31a32c1` | — | defn | `examples/stateful.oath:38` | proven | 2/2 | `+destination-holds-source` `+source-untouched-when-distinct` |
| 147 | `replicate` | `3fb40b27e9dc` | — | defn | `examples/extras.oath:170` | proven | 3/3 | `+zero-is-empty` `+unfold-step` `+length-is-n` |
| 148 | `req-body` | `55607d7d5608` | — | defn | `examples/http.oath:64` | proven | 1/1 | `+reads-body` |
| 149 | `req-headers` | `11e9cdb9a695` | — | defn | `examples/http.oath:59` | proven | 1/1 | `+reads-headers` |
| 150 | `req-method` | `b0c4a59b92c4` | — | defn | `examples/http.oath:49` | proven | 1/1 | `+reads-method` |
| 151 | `req-path` | `eadb605e522c` | — | defn | `examples/http.oath:54` | proven | 1/1 | `+reads-path` |
| 152 | `req-received-at` | `ea71fc7b5412` | — | defn | `examples/http.oath:69` | proven | 1/1 | `+reads-received-at` |
| 153 | `reverse` | `7bb6285884d0` | — | defn | `examples/list.oath:44` | proven | 2/2 | `+involution` `+antidistributes-over-append` |
| 154 | `reverse-onto` | `cb57f3242430` | — | defn | `examples/list.oath:170` | proven | 3/3 | `+empty-returns-acc` `+cons-step` `+matches-reverse-append` |
| 155 | `rle-decode` | `27a8605b1e12` | — | defn | `examples/rle.oath:15` | proven | 4/4 | `+nil-decodes-empty` `+cons-law` `+distributes-over-append` `+anchor-with-zero-run` |
| 156 | `rle-encode` | `253440f0e899` | — | defn | `examples/rle.oath:24` | tested | 3/6 | `-roundtrip` `+never-longer-than-input` `-uniform-list-is-one-run` `-two-runs-stay-two-runs` `+duplicate-head-merges` `+anchor-mixed` |
| 157 | `rle-expand` | `36fb384d7458` | — | defn | `examples/rle.oath:3` | proven | 5/5 | `+nonpositive-is-empty` `+peel-one` `+length-is-count-arg` `+every-element-is-v` `+anchor-three-sevens` |
| 158 | `rot` | `e715328a6ffa` | — | defn | `examples/rot.oath:1` | tested | 3/7 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `-decomposes-in-range` |
| 159 | `rot-f` | `e715328a6ffa` | `rot` | defn | `examples/rot_f.oath:1` | tested | 3/7 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `-decomposes-in-range` |
| 160 | `rot-h2` | `48a856f52ace` | — | defn | `examples/rot_h2.oath:1` | tested | 3/7 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `-decomposes-in-range` |
| 161 | `rot-h3` | `18228c2a22fa` | — | defn | `examples/rot_h3.oath:1` | tested | 4/7 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `+decomposes-in-range` |
| 162 | `rot-hl` | `e9b80a4dbecf` | — | defn | `examples/rot_hl.oath:1` | tested | 4/7 | `+zero-is-identity` `+empty-absorbs` `+preserves-length` `-shift-one-moves-head-to-back` `-neg-one-pulls-last-to-front` `-periodic-in-length` `+decomposes-in-range` |
| 163 | `safe-get` | `24800a6a9058` | — | defn | `examples/stateful.oath:52` | proven | 2/2 | `+down-is-none` `+up-is-some` |
| 164 | `secret-is-usable` | `5365b4a06be5` | — | defn | `apps/github-webhook/webhook.oath:385` | tested | 0/5 | `-empty-is-not-usable` `-short-is-not-usable` `-non-ascii-is-not-usable` `-latin1-is-not-usable` `-usable-encodes` |
| 165 | `set-add` | `874a3cb5d3f6` | — | defn | `examples/set.oath:49` | tested | 0/2 | `-adds-member` `-add-idempotent` |
| 166 | `set-elems` | `a91bbcd741e2` | — | defn | `examples/set.oath:70` | tested | 0/1 | `-size-is-length` |
| 167 | `set-empty` | `d067aca7a83d` | — | defn | `examples/set.oath:45` | tested | 0/1 | `-has-nothing` |
| 168 | `set-inter` | `8a059a2f7b56` | — | defn | `examples/set.oath:60` | tested | 0/1 | `-empty-left` |
| 169 | `set-member` | `00b50eb902ce` | — | defn | `examples/set.oath:41` | tested | 0/1 | `-empty-has-none` |
| 170 | `set-size` | `c73b49a04d37` | — | defn | `examples/set.oath:64` | tested | 0/1 | `-non-negative` |
| 171 | `set-union` | `c2b36fe59543` | — | defn | `examples/set.oath:54` | tested | 0/2 | `-empty-left` `-union-has-left` |
| 172 | `shout` | `1788554af98a` | — | defn | `examples/records.oath:93` | proven | 2/2 | `+grows-by-one` `+preserves-emptiness-never` |
| 173 | `show-int` | `a9472d7aa214` | — | defn | `examples/circle.oath:37` | tested | 0/1 | `-roundtrip` |
| 174 | `show-nat` | `1d0a9ecfb0ea` | — | defn | `examples/circle.oath:30` | proven | 1/1 | `+single-digit` |
| 175 | `si-insert` | `e3ba3eec0cba` | — | defn | `examples/set.oath:20` | proven | 3/3 | `+commutes` `+idempotent` `+adds` |
| 176 | `si-inter` | `32765987195d` | — | defn | `examples/set.oath:35` | proven | 1/1 | `+empty-left` |
| 177 | `si-member` | `47e842f8daf5` | — | defn | `examples/set.oath:14` | proven | 1/1 | `+empty-has-none` |
| 178 | `si-union` | `63f8d391e13d` | — | defn | `examples/set.oath:31` | proven | 1/1 | `+empty-left` |
| 179 | `sign` | `7c0ff56ae894` | — | defn | `examples/ints.oath:15` | proven | 2/2 | `+bounded` `+reconstructs` |
| 180 | `singleton` | `14b8a3dd9719` | — | defn | `examples/inferred.oath:10` | proven | 2/2 | `+is-one-element` `+unfolds` |
| 181 | `snoc` | `2248e151c30a` | — | defn | `examples/extras.oath:46` | proven | 3/3 | `+single` `+cons-step` `+as-append` |
| 182 | `sort` | `8ed4b7e956f3` | — | defn | `examples/sort.oath:90` | proven | 7/7 | `+output-is-sorted` `+preserves-length` `+preserves-counts` `+snoc-is-insert` `+sorted-is-fixpoint` `+idempotent` `+reverse-invariant` |
| 183 | `sort-by` | `5179ba32bb8d` | — | defn | `examples/generic.oath:123` | proven | 3/3 | `+preserves-length` `+preserves-counts` `+unfolds-by-insertion` |
| 184 | `spin` | `086cb1f93353` | — | defn | `examples/nontotal.oath:7` | falsified | 0/1 | `-claims-zero` |
| 185 | `spin-partial` | `b25520e6a37a` | — | defn | `examples/exclusion.oath:33` | asserted | 0/0 | *(none)* |
| 186 | `stash` | `be38139f4560` | — | defn | `examples/leaky.oath:10` | asserted | 0/0 | *(none)* |
| 187 | `str-append` | `7d158d0455d3` | — | defn | `examples/str.oath:21` | proven | 2/2 | `+left-unit` `+length-adds` |
| 188 | `str-bytes` | `cc13f713e301` | — | defn | `examples/http.oath:143` | proven | 2/2 | `+empty-maps-to-empty` `+preserves-length` |
| 189 | `str-drop` | `caaaa5b2134a` | — | defn | `examples/str.oath:50` | proven | 2/2 | `+drop-zero` `+take-drop-rebuilds` |
| 190 | `str-join` | `b41104b62aae` | — | defn | `examples/str.oath:74` | proven | 1/1 | `+single` |
| 191 | `str-len` | `30df3863fbb1` | — | defn | `examples/str.oath:14` | proven | 1/1 | `+nonneg` |
| 192 | `str-prefix` | `48d690bda44b` | — | defn | `examples/str.oath:30` | proven | 3/3 | `+self` `+empty` `+append-is-prefixed` |
| 193 | `str-split` | `0292b5f64edd` | — | defn | `examples/str.oath:63` | proven | 1/1 | `+never-empty` |
| 194 | `str-split-join` | `f3a62459d59a` | — | defn | `examples/str.oath:86` | proven | 1/1 | `+roundtrip` |
| 195 | `str-take` | `77452b35b089` | — | defn | `examples/str.oath:42` | proven | 1/1 | `+take-zero` |
| 196 | `sum` | `25f8a4de0cc7` | — | defn | `examples/list.oath:54` | proven | 2/2 | `+distributes-over-append` `+reverse-invariant` |
| 197 | `swap` | `b00e9f7d7c2d` | — | defn | `examples/inferred.oath:23` | proven | 1/1 | `+swap-involutive` |
| 198 | `t-flatten` | `a431dd53a6b3` | — | defn | `examples/tree.oath:16` | proven | 3/3 | `+flatten-leaf` `+flatten-node` `+flatten-singleton` |
| 199 | `t-insert` | `312101f6dfd4` | — | defn | `examples/tree.oath:27` | tested | 4/5 | `+insert-count-inserted` `+insert-count-others` `+insert-length` `-insert-keeps-sorted` `+insert-dup-goes-right` |
| 200 | `t-member` | `2b71f64d6f4d` | — | defn | `examples/tree.oath:47` | tested | 3/4 | `+member-leaf` `+member-root` `+member-insert-finds` `-member-flatten-equiv` |
| 201 | `t-size` | `1fc6a47774e2` | — | defn | `examples/tree.oath:60` | proven | 4/4 | `+size-leaf` `+size-node` `+size-flatten-length` `+size-insert` |
| 202 | `take` | `1cca17c6d090` | — | defn | `examples/extras.oath:26` | proven | 4/4 | `+take-then-drop-rebuilds` `+take-zero` `+take-one` `+take-two` |
| 203 | `take-while` | `0160610e8287` | — | defn | `examples/list.oath:229` | proven | 2/2 | `+empty-is-empty` `+cons-step` |
| 204 | `tenth-f` | `8fd0dda44666` | — | defn | `examples/convert.oath:28` | proven | 1/1 | `+is-point-one` |
| 205 | `third-f` | `8364dad02bbc` | — | defn | `examples/convert.oath:35` | proven | 1/1 | `+rounds-nearest` |
| 206 | `unwrap-or` | `19d25f94f919` | — | defn | `examples/records.oath:84` | proven | 2/2 | `+ok-unwraps` `+err-defaults` |
| 207 | `webhook` | `04241aba6e93` | — | defn | `examples/webhook.oath:174` | tested | 0/5 | `-unsigned-is-401` `-accepts-only-with-202` `-accepts-correctly-signed` `-tampering-is-rejected` `-never-leaks-a-body` |
| 208 | `within-window` | `b83924f8160e` | — | defn | `examples/webhook.oath:130` | proven | 3/3 | `+same-instant-ok` `+symmetric` `+rejects-beyond` |
| 209 | `zip` | `8994f7a68c09` | — | defn | `examples/extras.oath:101` | proven | 3/3 | `+nil-left` `+nil-right` `+cons-step` |
| 210 | `zip-with` | `d55d85255312` | — | defn | `examples/extras.oath:118` | proven | 3/3 | `+nil-left` `+nil-right` `+cons-step` |

## 4. Reconciliation with `fixtures/prove/outcomes.json`

`fixtures/prove/outcomes.json` is the ledger `scripts/check-doc-numbers.py`
reads, so which universe it enumerates decides what every gated prose figure in
this repository means. It is compared against the census here rather than trusted
or replaced.

    python3 scripts/corpus-census.py /tmp/census.json reconcile

- `fixtures/prove/outcomes.json` rows: **191**
- distinct object hashes among those rows: **190**
- rows whose name is not live: **0**
- live names with no row: **19**
  - of those, carrying at least one property: **0** 
  - by kind: {'data': 15, 'defn': 4}

- fixture rows whose summary counts disagree with their own property list: **0**

- rows disagreeing with the store on hash, level or any per-property verdict: **0**

- fixture rows totalling (counted from the per-property lists): **513 properties**, **368 proven**, **127 definitions fully proven**
- the same figures computed per-OBJECT from the census: **506 properties**, **365 proven**, **127 objects fully proven**

Four facts fall out, all decidable from the artifacts above:

**The fixture is keyed per NAME.** 191 rows carry 190 distinct object hashes.
`rot` and `rot-f` each get a row, and they are the same object.

**Its universe is live names carrying at least one property.** All 19 live names
absent from it — 15 `data` declarations and the four property-less `defn`s
`apply2`, `leak`, `spin-partial`, `stash` — carry zero properties, and no name
carrying a property is missing. 210 − 19 = 191.

**It does not disagree with the store anywhere, and it is internally
consistent.** Zero rows differ from the store on object hash, guarantee level, or
any individual property verdict; and zero rows carry a `prop_count` or
`proven_count` that disagrees with the per-property list sitting beside it in the
same row. The second check is the reason the totals below are counted from those
lists rather than summed from the summary fields — a stale summary would
otherwise be reported as agreeing with the store while the enumeration underneath
it said otherwise. The ledger is a faithful projection of the committed store;
the only question it raises is which population it projects.

**Its property totals are therefore per-name totals.** 513 properties and 368
proven, against 506 and 365 counted per object. The seven-property, three-proven
difference is `rot`/`rot-f` counted twice. The third figure the ledger derives,
*definitions fully proven*, is 127 under both readings — but that agreement is a
coincidence of the present corpus rather than something the derivation protects:
neither aliased object is fully proven today, and an aliased object that became
fully proven would move the per-name figure and not the per-object one.

None of that says which figure a given sentence ought to quote. It says the two
figures exist and differ by a known amount, which is what was missing.

## 5. The names the 2026-07-26 issue comment cited, re-derived

The comment on #68 records a set of specific claims about specific definitions.
It is treated here as stale evidence and re-measured rather than reused. This is
a LOOKUP against the census — the current level and per-property verdicts of each
name the comment mentioned by name. It offers no explanation of any value, and in
particular attributes no cause to anything that changed.

| name | live? | object | level | proven/props |
|---|---|---|---|---:|
| `str-append` | yes | `7d158d0455d3` | proven | 2/2 |
| `str-len` | yes | `30df3863fbb1` | proven | 1/1 |
| `str-prefix` | yes | `48d690bda44b` | proven | 3/3 |
| `str-take` | yes | `77452b35b089` | proven | 1/1 |
| `str-drop` | yes | `caaaa5b2134a` | proven | 2/2 |
| `str-split` | yes | `0292b5f64edd` | proven | 1/1 |
| `str-join` | yes | `b41104b62aae` | proven | 1/1 |
| `str-split-join` | yes | `f3a62459d59a` | proven | 1/1 |
| `join-with` | yes | `dbfc0545b148` | proven | 3/3 |
| `greet` | yes | `1b4fc08a4e93` | proven | 3/3 |
| `greet-or-guest` | yes | `1da62c4f6353` | proven | 2/2 |
| `shout` | yes | `1788554af98a` | proven | 2/2 |
| `full-name` | yes | `c77f088bed04` | proven | 2/2 |
| `initials-or` | yes | `ae876364d0f2` | proven | 2/2 |
| `show-nat` | yes | `1d0a9ecfb0ea` | proven | 1/1 |
| `show-int` | yes | `a9472d7aa214` | tested | 0/1 |
| `parse-nat-go` | yes | `5492d77957cd` | proven | 1/1 |
| `e-div` | yes | `100733cab4b2` | tested | 3/5 |
| `e-mod` | yes | `362696e75c13` | tested | 5/6 |
| `rot` | yes | `e715328a6ffa` | tested | 3/7 |
| `rot-f` | yes | `e715328a6ffa` | tested | 3/7 |
| `rot-h2` | yes | `48a856f52ace` | tested | 3/7 |
| `rot-h3` | yes | `18228c2a22fa` | tested | 4/7 |
| `rot-hl` | yes | `e9b80a4dbecf` | tested | 4/7 |
| `rle-encode` | yes | `253440f0e899` | tested | 3/6 |

Every name the comment cites is still live. Reproduce with:

    python3 -c 'import json,sys; c=json.load(open("/tmp/census.json")); N={x["name"]:x for x in c["names"]};
    [print(n, N[n]["hash"][:12], N[n]["level"], sum(p["proven"] for p in N[n]["props"]), len(N[n]["props"])) for n in sys.argv[1:]]' \
      str-append str-len str-prefix str-take str-drop str-split str-join str-split-join \
      join-with greet greet-or-guest shout full-name initials-or show-nat show-int \
      parse-nat-go e-div e-mod rot rot-f rot-h2 rot-h3 rot-hl rle-encode

**The one quantitative claim in the comment, re-measured.** It records the cluster
`e-div`, `e-mod`, the four `rot*` arms, `rle-encode`, `show-nat`, `show-int` as
carrying *44 unproven properties across 9 definitions*. Those ten names resolve to
nine distinct objects today (`rot` and `rot-f` are one), and they carry **21
unproven properties counted per object, 25 counted per name** — under either
reading, not 44. `show-nat`, listed there, now has no unproven property at all.

The comment's figure is not re-derivable from the committed store as it stands,
and this file does not attempt to explain the difference: that is a question about
what happened between then and now, not a measurement of what is there.

For the record and dated like everything else here: `gh issue view 71` reported
`CLOSED` on 2026-08-08. `gh` is the authority on issue status; that line is a
reading taken on that date, not a fact this file asserts.

## 6. How much of "why is it not proven" a committed fixture already answers

The obvious way to find out why 141 properties are not proven is to prove them
again with the prover's diagnostic seams open — `OATH_PROVE_CALIBRATE` for the
per-attempt solver verdict and consumed rlimit, `OATH_PROVE_SPLIT` for the
datatype-binder flag — and read the telemetry. **That measurement was written,
run, and is NOT committed**, because at the pinned `proveRlimit` it costs what
the cold conformance re-derivation costs: every unproven property burns the full
budget through every strategy, serially, which is where the nine-hour figure
comes from. A sweep nobody can afford to run is not a tool the repository should
carry: re-proving the corpus is how the classification gets paid for, and this
question does not justify that price. What survives is the RECORD FORMAT and the classifier
that consumes it, in `scripts/prove-reasons.py`; a producer bounded to a smaller
budget is a separate instrument and is not written yet.

**A third of the answer does not require paying it, and the artefact that
supplies it was already committed for another purpose.** `fixtures/prove/attempts.txt`
(#139) pins the sha256 of every script the strategy sequence CAN emit for every
property, enumerated with `smtCtx.enumerate` set, which is to say WITHOUT running
a solver. Two facts follow from the pinned rows alone, and neither needs a
verdict:

- **A property with no row emitted no candidate script, so it never reached the
  solver.** There is no SMT answer to look up because no SMT problem was ever
  built.
- **Among goals that translate, a strategy appears iff the goal's shape admits
  it.** `induction` rows are emitted when some binder's sort is a datatype
  (`prove.go`'s `hasDTBinder`, the same predicate its `OATH_PROVE_SPLIT` seam
  prints), and the induction loop iterates over exactly those binders. So
  candidacy is readable off the fixture.
  **The leading clause is not a hedge, it is the condition.** A goal that bails
  in translation emits NO row for any strategy, datatype binder or not, because
  the bail happens before the strategy sequence is reached — so absence of an
  `induction` row does not imply absence of a datatype binder, and reading the
  biconditional unconditionally would misdescribe the no-script population. This
  is why the candidacy table below is restricted to the goals that reached the
  solver: within that set the two directions do coincide, and outside it the
  question is not merely unanswered but ill-posed.

<!-- -->

    ( cd oath && OATH_CENSUS_OUT=/tmp/census.json go test -run TestCorpusCensus -count=1 )
    python3 scripts/prove-reasons.py ladder /tmp/census.json

Run on 2026-08-08 against `61e9fe2`, `fixtures/prove/attempts.txt` at 2937 pinned
rows. **The budget is not a parameter of any figure in this section**, which is
the whole point of it: no solver was invoked, so there is no rlimit to report.

| | per-object | per-name | what the fixture establishes |
|---|---:|---:|---|
| **Emitted no candidate script — never reached the solver** | 51 | 51 | the strategy sequence bailed before its first solver call, so no SMT verdict of any kind exists for the property |
| **Reached the solver** | 90 | 94 | at least one script was emitted; what z3 answered is not in this fixture |
| **total non-proven** | **141** | **145** | |

**Settled without running z3: 51 of 141 per-object (36.2%), 51 of 145 per-name
(35.2%).** The two columns agree in absolute terms because neither aliased object
contributes to this class — `rot`/`rot-f`'s four extra non-proven properties all
reached the solver — so the aliasing moves the denominator and not the numerator.

The universe is the census's, not the fixture's. `attempts.txt` holds a row only
where a script exists, so taking its key set as the population would drop exactly
the class being counted.

### Induction candidacy, cross-tabulated

Whether the goal was a candidate for induction is an ATTRIBUTE of the goal, not a
competing bucket: it co-occurs with every failure mode alike, and "induction was
never applicable" is a different statement from "induction was tried and did not
discharge". Restricted to the properties that reached the solver, since the
question is vacuous for the others.

| candidate for | per-object | per-name |
|---|---:|---:|
| `induction` (a datatype-typed binder) | 61 | 65 |
| `lexicographic` (an ordered pair, #17) | 16 | 16 |
| `recursion-induction` (the callee's own recursion, #57) | 0 | 0 |
| *none — direct attempts only* | 29 | 29 |
| **reached the solver** | **90** | **94** |

The rows are not exclusive. The combinations, which are:

| combination | per-object | per-name |
|---|---:|---:|
| `induction` | 45 | 49 |
| direct only | 29 | 29 |
| `induction` + `lexicographic` | 16 | 16 |

Two things this table says that the summary line does not. **Every lexicographic
candidate is also a structural one** — no goal reaches the ordered-pair strategy
without a datatype binder — so lexicographic induction never widens the reachable
set here, it only offers a second route into it. And **`recursion-induction` is a
candidate on no unproven property at all**: its 30 pinned scripts belong to 15
properties, and all 15 are proven.

**That is a statement about SHAPE, and it must not be read as one about
OUTCOMES.** The fixture is enumerated with `smtCtx.enumerate` set, and in that
mode `solve` returns `unsat` without running anything while each strategy's
success return is bypassed (`oath/prove.go:223`), precisely so the walk continues
into every later strategy. **So a proven property pins scripts for strategies
that never ran on it** — whichever strategy actually discharged it short-circuits
in a real proof and the rest are never reached. The rows therefore establish that
15 proven properties were CANDIDATES for recursion-induction, and nothing about
whether it was attempted on any of them, let alone whether it succeeded. An
earlier draft of this paragraph said the strategy "has only ever succeeded" on
"the 15 properties it was tried on"; both clauses were unsupported by the
artefact they cited, and the section's own rule — candidacy is an attribute, not
an outcome — is what they broke. What the rows do support is the negative: no
unproven goal in this corpus has the shape recursion-induction applies to, so it
is not a strategy being defeated here, it is one not being reached.

### What this does not settle, stated because the reverse reading is the tempting one

The 90 that reached the solver stay unclassified. Refuted, countermodel-withheld,
rlimit-exhausted and solver-unknown are distinguished by what z3 ANSWERED, and no
byte of that is in a fixture of script hashes. This section moves a boundary; it
does not sort the remainder.

Two soundness questions were closed by measurement rather than assumed, both in
enumerate mode and both solver-free:

- **Is "no rows" the same set as the prover's translation bail?** Re-running the
  ladder in enumerate mode over all 51 reports `unknown` with a detail naming the
  construct, and zero errors: 34 `"lam" terms are outside the provable fragment`,
  16 `hmac-sha256 is outside the provable fragment (trusted crypto primitive)`,
  1 `apply2 must be fully applied to inline`. They are the same set on this
  corpus.
- **Can a property bail AFTER emitting a script?** Nothing forbids it in
  principle, and it would make "has rows" the wrong test. Measured: all 94
  per-name properties that emitted a script run their ladder to the end and
  return the one fixed exhaustion sentence. Zero late bails. That equivalence is
  a fact about this corpus, not a theorem about the prover.

### Controls

The reconciliation is only worth reading if it can fail, so each branch was
watched firing against a mutated fixture. The unmutated fixture passes first;
every mutation below exits non-zero naming the specific defect.

| mutation | what fired |
|---|---|
| *(none — the committed fixture)* | passes |
| fixture absent | `is missing; without it this mode's silence is not evidence` |
| fixture present but empty | `has no rows; nothing below would be measured` |
| a row naming a property the census does not hold | orphan row named |
| every script of a PROVEN property removed (`abs[0]`) | `is PROVEN yet pins no script — absence cannot mean 'never reached the solver'` |
| one alias's rows removed (`rot-f[0]`) | `aliases ['rot', 'rot-f'] disagree on the ladder for prop 0` |
| a truncated row | `malformed row` |
| a strategy label typo (`lemma-free` → `inductoin`) | `carries unrecognised strategy label(s)` |

The last one is not about the partition but about the candidacy tables: an
unrecognised label leaves the property counted as having reached the solver while
dropping it out of its `induction` row and into *direct only*, so it changes the
answer rather than failing. The vocabulary is pinned against its authority, the
`c.solve("<strategy>", …)` call sites in `oath/prove.go`; a strategy added there
will be refused here, loudly, which is the correct direction.

The fourth is the one that makes the partition mean anything. "Emitted no script"
is evidence of never reaching the solver only if proven properties never look
that way — a proven property necessarily reached the solver, so a proven property
with no pinned script would indict the fixture rather than the goal.

**AND THE CONTROL THAT DOES NOT EXIST, NAMED BECAUSE ITS ABSENCE IS LOAD-BEARING.**
Nothing above catches a fixture that lost a NON-PROVEN property's rows: such a
property would join the left-hand column silently and inflate the 51. The reason
is not oversight. **`attempts.txt` has no row meaning "enumerated, emitted
nothing"** — absence spells "the goal bailed" and "nobody looked" identically —
so the distinction has nowhere in the format to live, and no amount of checking
recovers what was never written down. External review raised exactly this, a
def-level check was written to answer it, and the check was **unsound and
deleted**: 12 definitions in this corpus legitimately pin no row for any of their
properties (every goal bails on `hmac-sha256` or `lam`), so "has properties, pins
nothing" is a true description of correct data. It fired on the unmutated
fixture, which is how it was caught.

What actually closes the question is the first bullet above — the enumerate-mode
re-run, whose 34 + 16 + 1 details sum to exactly the 51 — because that consults
the PRODUCER rather than the fixture. It is corroboration on this corpus, not a
property of the format, and it has to be re-run when either moves.

In place of the missing control the ladder now REPORTS the column's shape: of the
51, **50 belong to definitions that pin no script for any property and 1
(`excluded-witness[1]`) to a definition that pins scripts for its other
properties.** A partially stale fixture would have to swell that second group, so
the single name is printed rather than counted. That is a description, not a
gate, and it is offered as one.

## What this file deliberately does not do

- **No classification of WHY a goal the solver saw did not prove.** Nothing is
  labelled blocked, stalled or SMT-incomplete. Section 6 draws exactly one line
  through the 141 (per object; 145 per name) — did a candidate script exist at
  all — and records which strategies each goal was a candidate for. Both are
  facts about the goal's SHAPE, decidable from a committed fixture with no solver
  and no judgement. The 90 that reached the solver are still enumerated and not
  sorted, and sorting them requires knowing what z3 answered — which no committed
  artefact records and no instrument in this tree currently produces.
  This bullet said "no classification" flatly before section 6 existed; it was
  true when written, and leaving it would have made the file disagree with itself.
- **No verdict on #68.** Whether sequence-theory encoding is worth building, and
  against what falsifier, is untouched. The census is what such a decision would
  need underneath it, not an input tilted toward an answer.
- **No encoder and no SPEC change.** This said "no prover change" and that stopped
  being true when section 7's producer landed: `oath/prove.go` gained a
  diagnostic observer seam on `solve` and a telemetry cell on `runZ3Budget`. The
  seam is inert — nothing in the kernel installs an observer, no normative path
  reads it, and no proof outcome depends on it — but the file must not claim the
  proof path was left untouched when it was edited. The ENCODING is what nothing
  here reaches.
- **No hash re-derivation from source**, for the reason given under the
  instrument: that claim belongs to `oathrs/conformance.sh` check 1 and to
  `make verify`, and the latter writes.
- **No mutation of `codebase/`.** Verified after the fact: `git status
  codebase/` is clean, and the controls that did mutate the store ran in a
  detached worktree and were reverted there.

## 7. The bounded producer, and the other two thirds

Section 6 ends by saying a producer bounded to a smaller budget "is a separate
instrument and is not written yet." It is written now, it has been run over the
whole non-proven set, and this section is what it returned.

**EVERY FIGURE BELOW WAS MEASURED AT `rlimit = 4000000`.** That is not a
footnote to the numbers, it is part of them. A verdict is a function of (script
bytes, solver version, rlimit), so an `unknown` here says z3 did not converge
within 4M and says NOTHING about what it would do with more. The normative
per-goal budget is `proveRlimit` at 400M — one hundred times this — so **a 4M
`unknown` is not a 400M `unknown`**, and no row below may be restated as "the
prover cannot do this". What it can be restated as is "not settled within a
budget one hundredth of the normative one", which is a weaker and true claim.

The 4M figure is not arbitrary: `OATH_PROVE_RLIMIT=4000000` makes
`effectiveRlimit()` 4M, and `directRlimit()`/`lemmaFreeRlimit()` clamp to it, so
every strategy in the ladder runs at one budget and a single number describes all
of them. At the normative 400M those two strategies still run at 4M, so the
lemma-free probe and the induction-eligible direct attempt are budget-identical
here and in a normative run.

### The universe

All **141** non-proven per-object properties (145 per-name), taken from the
committed store's own metadata — the same set section 1 counts and the same set
`scripts/prove-reasons.py`'s `check()` reconciles against. Nothing was sampled,
excluded or truncated.

**90 of the 141 emit at least one candidate script; 51 emit none.** That split is
a FACT ABOUT THE GOALS, not a statement about this sweep's coverage: the 51 were
swept like everything else, and their goals were untranslatable rather than
skipped, truncated or budget-limited. Section 6 derives the same 90/51 boundary
from a committed fixture with no solver, which is the cross-check that the
producer's universe and the fixture's agree.

### What z3 answered

| category | per-object | per-name |
|---|---:|---:|
| Proves when attempted | 43 | 43 |
| Solver did not converge within the pinned 4M rlimit | 42 | 46 |
| Never reached the solver (translation bail) | 51 | 51 |
| Refuted — demonstrated non-theorem | 3 | 3 |
| Countermodel found, verdict withheld | 1 | 1 |
| Solver returned `unknown` for another reason | 1 | 1 |
| Proved-but-withheld / late translation bail / environmentally invalidated | 0 | 0 |
| **total** | **141** | **145** |

The 51 reproduce section 6's fixture-only derivation exactly, which is the point
of deriving them twice from different artefacts.

**The interesting row is the first.** 43 properties the store records as
unproven discharge from the state the store itself records — 35 of them at the
lemma-free probe, which admits no lemmas at all, in a median of 5 milliseconds.
This is the documented behaviour of a corpus whose proof state advances as
dependencies settle (`oath fixtures` writes verdicts back, and counts climb run
over run); it is not a discovery that the prover was wrong.

### Controls

- **The script hash.** Each property's direct-attempt script was sha256'd and
  compared to its row in `fixtures/prove/scripts.txt`. **90 matched, 0
  mismatched**, 51 have no direct script and no pinned row. A mismatch would mean
  a different recorded lemma state, hence a different experiment from the pinned
  one — never noise. 55 of the 90 were compared AS SENT to the solver; the other
  35 discharged before the direct attempt ran, so their script was rebuilt under
  the same store and lemma state and the record says which route was used. The
  two are not equally strong evidence and are counted separately.
- **One budget.** Every record carries `rlimit` 4000000; the classifier refuses a
  sweep that mixes budgets.
- **No environmental aborts.** Zero `capHit` attempts, so no row rests on a
  wall-clock cap, a memout, or lost telemetry.
- **Seriality, asserted.** The telemetry seam is a package-level cell, so it is
  sound only while one prover runs. Each property asserts that the number of
  solver attempts published equals the number the observer recorded; any foreign
  attempt inflates the total. It held for all 141.
- **Confirmed by a different route.** Ten of the 43, chosen cheapest-first within
  each discharging strategy so that all four are represented, were re-proved with
  the `oath prove` CLI against a COPY of the store — exercising the CLI, the
  normative prover path and the store write path, none of which the producer's
  harness touches. **All ten agreed on both the verdict and the discharging
  strategy**, as did five further properties the same invocations happened to
  cover. No invocation approached the three-minute limit.
- **Nothing was written.** `git status codebase/ fixtures/` clean before and
  after; the CLI confirmation ran against a temporary copy.

### The projection miss, which is worth more than the numbers above

The sweep was launched on a projection of **3.8 minutes, with 14.9 minutes given
as an absolute ceiling**. It took **18 minutes 8 seconds** — the ceiling was
breached, and the run finished inside its 20-minute wall with 111 seconds to
spare. Both cost models were wrong, and they were wrong in instructive ways:

- **Enumerated attempts overestimate volume.** 244 of the 686 enumerable attempts
  actually executed. The four properties with the largest enumerated counts
  (`gh-request` 0–2 at 73 each, `header-or[1]` at 27) execute exactly ONE attempt
  and finish in milliseconds, because the lemma-free probe discharges them. A
  goal emits many enumerable scripts when it has many binders and constructors,
  which is not the same thing as being expensive.
- **Wall time is not a function of rlimit.** rlimit is a deterministic WORK
  counter, not a clock. Attempts that burn exactly the same 4M differ by two
  orders of magnitude in seconds, so any projection built on budgets or attempt
  counts is unsound in principle rather than merely imprecise.
- **Six properties of 141 carried 83% of the cost.** `rot-h3` and `rot-hl`
  consumed 909 of the 1088 seconds, with four attempts each;
  `rot-h3.neg-one-pulls-last-to-front` alone took 236 seconds. The pre-launch
  sample of nine properties contained no `rot-*`.

The general shape is one this file's own controls section already names: a proxy
was chosen because it was ENUMERABLE — attempt counts are sitting in a committed
fixture — and it turned out to measure something adjacent to the claim. The
claim was wall-clock cost; the population that owns it is the set of goals that
fail to discharge, and nothing in the fixture identifies those.

### What section 7 does not settle

- **It does not classify at the normative budget.** Every non-converging row here
  is a statement about 4M. The 400M question is exactly the nine-hour sweep
  section 6 declines to carry, and this does not replace it.
- **It does not re-derive the 43 into the store.** The sweep writes nothing, so
  the corpus still records those properties as unproven. Whether to advance them
  is a separate decision with its own cost.

## 8. The verdict — WITHDRAWN AS SCOPED. It answers half of #68.

**READ THIS BEFORE THE NUMBERS BELOW.** Everything in this section evaluates
`Str` goals. #68 proposes encoding *"`Str` goals **(and `List`-of-scalar goals
where it applies)**"*. The second clause was dropped, and R1 — "no `Str` in
binders or signature" — then excluded **20 goals that are inside the proposal's
scope**: 14 typed `Int, (List Int)`, 4 typed `(List Int), (List Int)`, 2 typed
`(List Int)`.

That is this repository's own rule broken at the top of its own analysis: **a
witness must derive its universe from the CLAIM, never from a narrowing of it.**
The universe here came from a partial reading of the issue's own sentence, and
every count downstream inherits it.

**AND THE OMITTED CLASS IS THE MOST PLAUSIBLE ONE, WHICH IS WHY THIS IS WITHDRAWN
RATHER THAN FOOTNOTED.** The 14 `Int, (List Int)` goals are the `rot-*` rotation
laws — `periodic-in-length`, `decomposes-in-range`,
`shift-one-moves-head-to-back` and `neg-one-pulls-last-to-front`, the last across
all four of `rot`, `rot-h2`, `rot-h3` and `rot-hl` — and rotation over a sequence
is `seq.extract`
plus `seq.++`, which is the shape a sequence solver handles best. **Six of those
14** — the `rot-h3` and `rot-hl` properties — are what §7 measured as consuming
83% of the entire sweep; that figure belongs to those six, not to the class, and
an earlier draft of this paragraph attached it to all 14. A scoping slip excluded
the strongest candidates in the corpus, and the surviving analysis reads as if it
had considered them.

**A SECOND EXCLUSION WAS CLAIMED HERE AND IS WITHDRAWN — IT WAS MINE, AND IT WAS
WRONG.** This section argued that §7's removal of the **51 translation-bails** was
also a scoping error, on the reasoning that a bail is an ENCODING failure and #68
is an ENCODING change. The principle is sound. The application was not, and the
example carried it: `gh-signature` was cited as four `tested`, `Str`-typed
properties with no candidate scripts. **`fixtures/prove/attempts.txt` holds 17
rows for `gh-signature`.** It emitted candidate scripts, reached the solver, and
is not a bail at all.

With the example gone the claim collapses, because §6 already gives the 51 their
causes: **34 unsupported `lam` terms, 16 trusted `hmac-sha256` calls, and one
partial application.** A list-to-`Seq` representation adds support for none of
those. §7's exclusion of the 51 was correct.

**HOW IT GOT WRITTEN IS THE PART WORTH KEEPING.** The example was supplied by a
review, contradicted by the next review of the same file, and asserted here in
between without being checked — against this repository's own instruction not to
take an agent's report at face value. Verifying it cost one `grep`. A repair that
introduces a defect while fixing one is the failure mode this file has now
demonstrated three times in a single section; the first draft also reported "71
of 141 omitted", sweeping all 51 back into scope, which the same reasoning
refutes.

**SO ONE SCOPING ERROR STANDS, NOT TWO:** R1's exclusion of the 20 in-scope
`List`-of-scalar goals. That one is enough to withdraw the verdict on its own.

**SO NO CORRECTED TOTAL IS OFFERED.** Three attempts at this population have been
wrong — R1 dropped in-scope `List`-of-scalar goals, a repair swept in 51 goals
that do not belong, and a further repair rested on an unverified example. Quoting
a fourth number here would be the same mistake with better manners.

**WHAT STANDS — dispositions, not counts.** The three examined `Str` candidates
rest on their own evidence: `show-int.roundtrip` fails on an Int obligation that
survives stripping the `Str`, and the two `config.oath` properties are false and
therefore unprovable, though a `seq` encoding might refute them. Those hold
whatever the population turns out to be.

**WHAT DOES NOT STAND — every count, and any recommendation.** Not "0 of 42", not
"at most 2 of 42", not "decline", and not the zero in the title-question table
below, which is derived from the same 42 and therefore inherits the same wrong
universe. #68 is UNANSWERED.

**WHAT ANSWERING IT REQUIRES** is stated once, in full, at the end of this
section — *"Why this section stops here instead of being repaired again"*. It is
deliberately not summarised here: an earlier draft gave a short version that said
"over ALL properties that are not `proven`", which silently readmits the 43 whose
stored verdict is stale but which the CURRENT encoding proves. Two recipes for
one population is how this section acquired most of its defects.

Reaching the `List`-of-scalar half needs a criterion of its own — R1–R5 are
written about `Str` — and the producer makes re-running the measurement one
command, so the cost is the criterion, not the data.

---

### The `Str`-scoped result, unchanged

Sections 6 and 7 exist to make this question answerable. Within `Str` goals:
**at most 2 of the 42 examined, and neither is confirmed.** An earlier version of
this line said *zero*, and that count did not survive review; see the borderline
pair below.

### The title question — UNANSWERED, and for a DIFFERENT reason than the verdict

The issue is titled *"push `Str` properties from tested to PROVEN"*. That is a
narrower question than reachability, its universe is NOT the reachability
verdict's, and conflating the two produced three wrong answers here in
succession. Stated once, correctly:

| | its universe | why the analysis missed it |
|---|---|---|
| reachability verdict | `Str` goals **and** `List`-of-scalar goals, per #68's own sentence | R1 dropped 20 in-scope `List`-of-scalar goals |
| **title question** | `Str` properties **the current encoding cannot prove** | the analysis used the 42 budget-exhausted goals, which is neither a superset nor a subset of that |

**The `List`-of-scalar omission does NOT touch the title question** — the title
asks about `Str` only. Saying it did was an error in an earlier draft of this
section, and it contradicted a correction two paragraphs away.

**And the population is not "every `Str` property the store marks non-proven"
either.** §7 measured 43 properties that PROVE when attempted at 4M — their
stored verdicts are stale, and `gh-signature`'s four are among them, discharging
through the existing lemma-free path. Counting those as headroom for #68 would
attribute STORE ADVANCEMENT to an ENCODING CHANGE. That mistake was also made
here, twice, citing `gh-signature` first as a translation bail (it has 17 rows in
`attempts.txt`) and then as an open case (it already proves).

So the title question's population is: **`Str` properties that the CURRENT
encoding, at a stated budget, does not prove.** Deriving it means subtracting the
43 stale-verdict proofs before counting anything — which this file has the data
to do and did not do.

### Why this section stops here instead of being repaired again

Eight consecutive review rounds found eight real defects in these conclusions,
each unrelated to the last: a wrong exclusion criterion, a partial correction, an
overclaimed bound, a fabricated second exclusion, an unverified example, a
misattributed cost figure, two conflated universes. That pattern is this
repository's own signal that **an artefact is missing rather than a paragraph
being wrong** — and the missing artefact is a population derived from #68's claim
instead of from whichever subset was nearest to hand.

Repairing the prose again would produce a ninth number computed over a ninth
universe. **The counts in the remainder of this section are therefore WITHDRAWN
rather than corrected**, and the section is kept for its method, its data and its
three individual dispositions.

**What a correct answer requires**, so the next attempt starts from the claim:

1. Two populations, derived separately and never merged — #68's proposal (`Str`
   AND `List`-of-scalar) for reachability, and `Str`-only for the title question.
2. Subtract the 43 stale-verdict proofs from **BOTH**, before either is counted.
   A stored `tested` is not evidence the current encoding cannot prove it, and
   leaving them in would credit a sequence encoding with proofs the existing one
   already produces — the same store-advancement-as-encoding-impact error made
   twice above, which applies to reachability exactly as it does to the title.
3. Dispose of each translation-bail on whether a `Seq` REPRESENTATION would make
   it translatable — §6 gives their causes as 34 unsupported `lam`, 16 trusted
   `hmac-sha256`, one partial application, none of which it would.
4. A reachability criterion for `List`-of-scalar goals; R1–R5 are `Str`-only.

The producer makes re-running the data one command. The cost is the criteria.

**Those four are discharged, as criteria and not as results, by §9** — which
pins the populations and the `List`-of-scalar rules and deliberately measures
nothing about the corpus. Nothing below this line is repaired by it; §8's counts stay withdrawn.

### The population, and what it is not

The universe is the **42 per-object budget-exhausted** properties from §7 — the
ones that stall. NOT the 141 non-proven, and NOT the 51 that never reached the
solver: a goal the strategy sequence could not translate is not evidence about a
solver theory. Two neighbours are excluded and neither is a `Str` goal:
`abs-small.bounded-wrongly` (countermodel-withheld, `Int`) and
`set-inter.empty-left` (solver-unknown, `Set`).

**Measured at `rlimit = 4000000`, and what that does NOT establish.** The corpus
already records all 42 as unproven at the normative 400M, so a 4M non-convergence
is the EXPECTED observation and adds no information about them. It may not be
restated as "4M showed this is hard" — 400M already showed that. Only a 4M PROOF
would have been positive information, and this population is by construction not
that. §7's 43 proves-on-attempt were the positive-information rows; these are a
different set.

### The criterion, pinned before any type was derived

`Str` is a datatype (SPEC §3), translated to an SMT algebraic datatype with
user-defined recursive functions over it, so **nothing in the corpus reaches Z3's
`Seq` sort today**. The criterion is therefore counterfactual — if the goal were
re-encoded onto `Seq`, would it land in a fragment Z3 decides? All five conditions
required:

- **R1** a binder or the definition's result is `Str` as the kernel types it
  (byte strings are deliberately not `Str`, SPEC §2);
- **R2** the `Str` operations reduce to the seq signature — concat, length, index,
  extract, prefix/contains, indexof, replace, regular membership. A user-defined
  recursive function that is not one of these still has no theory after
  re-encoding;
- **R3** no quantifier alternation (a universal goal negates to quantifier-free,
  which is the target fragment);
- **R4** arithmetic entanglement bounded — strip the `Str` and ask whether an Int
  obligation remains that z3 was already failing on;
- **R5** recursion is not the obstacle — seq theory does not supply induction.

Writing it first is the control. A criterion written after seeing which goals one
would like to qualify is fitted, and this is the number the whole issue turns on.

### Result: 0 confirmed reachable, 2 borderline, 40 not reachable

Types come from the kernel (`oath get`), never from names — `bytes-after` is
`[(needle (List Int)) (hay (List Int))]` and is not a `Str` goal despite being
string-shaped work. The container datatypes were checked for a hidden `Str`:
`Map = List (Pair Int Int)`, `Set = List Int`, `Queue = List Int × List Int`,
`Run = Int × Int`. None carry one.

### R1 holds — the only candidates (3)

| property | binder types | signature | verdict |
|---|---|---|---|
| `config-has-key.finds-head` | `(List Str), Str` | `(-> (List Str) Str Bool)` | **BORDERLINE** — the property is FALSE, but a `seq.indexof`/`seq.extract` encoding could DECIDE it by refuting; R2–R5 unmeasured |
| `show-int.roundtrip` | `Int` | `(-> Int Str)` | NOT REACHABLE — fails R2/R4/R5 (Int div/mod) |
| `config-missing.complete-config-reports-nothing` | `Str, Str` | `(-> (List Str) (List Str) Str)` | **BORDERLINE** — same: falsity is not unreachability, and deciding includes refuting |

### R1 fails — no `Str` in binders or signature (39)

| binder types | n | properties |
|---|---:|---|
| `Int, (List Int)` | 14 | `rot-h2.decomposes-in-range`, `rot-h2.neg-one-pulls-last-to-front`, `rot-h2.periodic-in-length`, `rot-h2.shift-one-moves-head-to-back`, `rot-h3.neg-one-pulls-last-to-front`, `rot-h3.periodic-in-length`, `rot-h3.shift-one-moves-head-to-back`, `rot-hl.neg-one-pulls-last-to-front`, `rot-hl.periodic-in-length`, `rot-hl.shift-one-moves-head-to-back`, `rot.decomposes-in-range`, `rot.neg-one-pulls-last-to-front`, `rot.periodic-in-length`, `rot.shift-one-moves-head-to-back` |
| `(List Int), (List Int)` | 4 | `bad-reverse.antidistributes-over-append`, `bytes-after.finds-at-head`, `q-drop.drop-is-tail`, `q-drop.peek-drop-rebuild` |
| `Int, Int` | 4 | `e-div.division-identity`, `e-div.shift-by-divisor`, `e-mod.periodic`, `rle-encode.uniform-list-is-one-run` |
| `Int, Int, Map` | 3 | `map-has.present-after-insert`, `map-insert.finds-inserted`, `map-merge.prefers-left` |
| `Int, Set` | 3 | `set-add.add-idempotent`, `set-add.adds-member`, `set-union.union-has-left` |
| `Set` | 3 | `set-elems.size-is-length`, `set-size.non-negative`, `set-union.empty-left` |
| `(List Int)` | 2 | `q-drop.drop-back-only`, `rle-encode.roundtrip` |
| `Int` | 2 | `set-empty.has-nothing`, `set-member.empty-has-none` |
| `Tree, Int` | 2 | `t-insert.insert-keeps-sorted`, `t-member.member-flatten-equiv` |
| `Int, Int, Int, Int` | 1 | `rle-encode.two-runs-stay-two-runs` |
| `Map` | 1 | `map-size.non-negative` |

total: 42 = 40 not reachable + 2 borderline + 0 confirmed reachable

### The three candidates

**`show-int.roundtrip`** satisfies R1 through its RESULT type only; its binder is
`Int`. `show-nat n = if n < 10 then digit else str-append(show-nat (n/10), …)` —
the content is base-10 representation correctness over integer division and
modulo. Fails R2, R4 and R5. `Str` is the carrier, not the subject, and this is
the one property disposed of by the reasoned part of the criterion.

**`config-has-key.finds-head`** and **`config-missing.complete-config-reports-nothing`**
were expected to be the borderline cases, turning on whether `config-key` could be
re-expressed via `seq.indexof`/`seq.extract`. They ARE borderline, and a first
version of this section wrongly discharged them by observing that **they are
false** — neither guards its `key` against containing the separator `=`. Filed as
#161 with counterexamples and controls, because it is a corpus defect independent
of this one.

**FALSITY DOES NOT MAKE A GOAL UNREACHABLE, AND THE CRITERION SAYS SO IN ITS OWN
WORDS.** R1–R5 ask whether Z3's sequence theory can DECIDE the re-encoded goal,
and deciding includes answering `sat` with a counterexample. A `seq.indexof` /
`seq.extract` encoding of `config-key` is exactly the shape that could surface the
separator-in-key witness, turning both stalls into REFUTATIONS. That is a decision
procedure doing its job, not a goal outside its reach — and it would have caught
#161 automatically, which is the opposite of irrelevant.

So these two rows stay BORDERLINE, for the reason the rest of this section already
gives: R2–R5 are reasoned rather than measured, and the reduction of `config-key`
onto the `Seq` sort is explicitly left unresolved here. They cannot be discharged
by an argument the criterion does not contain.

The error is recorded rather than quietly corrected because of its shape: a true
observation about the properties (they are false) was substituted for the question
actually asked (can seq theory decide them). Both readings produce a tidy number,
and only one of them answers #68.

### What the soft part of the criterion actually carries

R2–R5 are REASONED about Z3's sequence solver, not measured: no goal was actually
re-encoded onto the `Seq` sort. That limit is real and it carries almost nothing,
which is worth stating rather than leaving as a caveat over the whole result:

- **39 of 42 fall to R1 alone**, decidable from the kernel's own types;
- **1 is disposed of by the reasoned part**, and it fails R4 on an Int obligation
  that survives stripping the `Str` entirely;
- **2 remain BORDERLINE** — the `config.oath` pair, which a `seq.indexof` /
  `seq.extract` encoding might DECIDE by refuting, and which the unmeasured part
  of the criterion cannot dispose of.

So the split is **40 not reachable, 2 borderline, 0 confirmed reachable**, and the
soft part of the criterion carries those 2 rather than nothing. An earlier version
of this section claimed 42 = 3 + 39 with the pair discharged as false, which
substituted a true observation for the question asked.

### Scope — a corpus figure, not a claim about sequence encodings

**RETIRED BY THE WITHDRAWAL AT THE HEAD OF THIS SECTION.** What follows was
written when the population was believed to be the 42, and it is kept only so the
retired claim is legible rather than silently deleted. Its arithmetic is sound and
its universe is wrong; do not quote it.

> The supportable sentence is *"in the current Oath corpus, at most 2 of 42 stalling
> properties are in reach of a sequence encoding, and neither is confirmed"*. It is
> NOT *"zero are"*, and it is NOT *"a `Str` sequence encoding is not worth
> building"*. The recommendation to decline rests on **at most 2 of 42**, which is
> small enough to carry it — but the recommendation and the count are different
> claims, and the count is not uniformly measured either. Its parts have different
> standing, and collapsing them is how a bound gets quoted as though it were an
> observation:
>
> - **39 of 42 excluded by R1 is MEASURED** — decidable from the kernel's own
>   types, no reasoning about Z3 involved;
> - **the step from 3 candidates to 2 is REASONED**, resting on R2/R4/R5 against
>   `show-int.roundtrip`, and R2–R5 were never measured because no goal was
>   re-encoded onto the `Seq` sort;
> - **the remaining 2 are UNRESOLVED**, not counted either way.
>
> So the honest form of the upper bound is *at most 3 measured, at most 2 once one
> reasoned exclusion is granted*. `examples/` is not a neutral sample of programs — it is the
> exhibits this project chose, weighted toward provable arithmetic and structural
> recursion because that is what the prover reaches. Three of 42 stalling properties
> touching `Str` at all is a fact about that weighting as much as about seq theory.

**What would change the answer:** a corpus with real text processing in it.
`docs/experiments/webhook-friction.md` concludes the datatype slice should have
been byte lists and text. If that lands and brings genuine `Str` manipulation,
this measurement should be re-run before the question is treated as settled — the
producer makes that one command rather than a fresh investigation.

### Method note

Two CORPUS DEFECTS — not two candidates — were settled by **a single `oath eval`,
in milliseconds, with no solver involved**. The distinction matters and an earlier
version of this note lost it: the evaluation settles that the two `config.oath`
properties are FALSE, which is #161. It does NOT settle whether sequence theory
could decide them, and those two remain borderline here for exactly that reason.
The prover had been running against both for the full budget and returned
`unknown` — which is the honest answer to the wrong question. This is the chain this file has recorded before: a named domain
makes a meaningful question askable, one evaluation exposes the defect, and proof
confirms a repair rather than finding the defect.

It also sharpens §7's caution in a new direction. §7 warned that a 4M `unknown`
must not be read as "the prover cannot do this". These two show the stronger
version: an `unknown` on a FALSE property reads as difficulty, and every
instrument in this repository reported them as merely unproven until one input
was tried.

## 9. PREREGISTRATION — the two populations and the criteria, pinned before any classification

**This section classifies nothing, counts nothing, and measures nothing about
the corpus — with ONE recorded exception, a solver capability probe named and
scripted in §9.4, which touches no corpus goal.** §8 withdrew every number it had because each was computed over a
universe chosen after the fact; a fifth number written the same way would be the
same defect with better manners. So this section does the part §8 named as the
missing artefact and stops: it DEFINES the populations, DERIVES them from #68's
own sentence rather than from whichever subset is nearest to hand, and PINS the
classification criteria — while the producer has not been re-run and no goal has
been classified under them.

**WHAT THIS IS NOT: A BLIND PREREGISTRATION, AND SAYING OTHERWISE WOULD BE THE
KIND OF OVERCLAIM THIS FILE EXISTS TO CATCH.** A first draft of this heading said
*fixed before any datum is read*, and that is false. §8 sits a few hundred lines
above, and it already records the candidates' binder types, the `Str` arm's
dispositions and which class its author considered most plausible; this very
section calls that class the strongest in the corpus. **The data are already in
the file and already in the author's head, and committing §9 first does not
un-read them.** The honest description is a PROSPECTIVE REANALYSIS INFORMED BY
§8: what it fixes in advance is the classification, not the author's knowledge.

**So what the ordering actually buys, stated at its real size.** A criterion
written after seeing which goals one would like to qualify is fitted, and the
reader cannot tell from the finished document. Git makes the WEAKER property
checkable: these rules land in their own commit, before any commit carrying a
disposition under them, so a rule bent to admit a goal must appear as an
amendment with a reason and a timestamp rather than as prose that was always
there. **The residual risk is not removed and no protocol here removes it** —
knowing which goals are the plausible candidates can shape a criterion toward
admitting them, invisibly. The two mitigations available are that the rules are
written in the vocabulary of types and term forms rather than by naming corpus
definitions, and that the amendment record is public. Neither is blindness.

**The amendment protocol, because a pinned criterion that is quietly edited is
worse than an unpinned one.** If applying these rules turns out to need a
decision this section does not contain, that is an INCOMPLETE criterion, not a
judgement call to make at the point of use. Record the amendment here, with its
reason, in a commit of its own, BEFORE the classification it enables — and
re-run every disposition it could move. An amendment made in the same commit as
the results it permits is indistinguishable from fitting.

**One verdict class is unreachable this round, by construction.** CONFIRMED
REACHABLE requires that a goal was actually re-encoded onto the `Seq` sort and
answered by z3 within a stated rlimit. No re-encoder exists — §8's criterion
already recorded R2–R5 as reasoned rather than measured, and that limit is
unchanged. So the achievable outcome of the next round is a partition into NOT
REACHABLE and BORDERLINE, and a reader expecting a confirmed count should stop
expecting one here rather than after reading the tables.

### 9.1 Two populations, derived separately, NEVER merged

#68 carries two questions with two universes, and §8's counts died of conflating
them. They are defined here as separate derivations that share steps without
sharing membership.

| | POPULATION A — reachability | POPULATION B — the title question |
|---|---|---|
| the question | can a sequence encoding DECIDE the goal? | can a sequence encoding turn a `Str` property from `tested` into `proven`? |
| authority for its scope | #68's proposal sentence: *"`Str` goals (and `List`-of-scalar goals where it applies)"* | #68's title: *"push `Str` properties from tested to PROVEN"* |
| subject rule | **R1 or L1** | **R1 only** |
| deciding includes refuting? | **yes** — a false property is decidable and counts | **no** — a false property can never become `proven` and is removed |
| budget | **§7's pinned rlimit, fixed here** — both populations are UPPER BOUNDS at that budget, and it is stated with every result | same |

**THE BUDGET IS PART OF THE MEMBERSHIP RULE, SO IT IS PINNED HERE AND NOT
CHOSEN LATER: step 2 runs at §7's rlimit.** What is pinned is the BUDGET, not
§7's result rows — those name hashes from §7's snapshot, and reusing them across
a change of identity subtracts objects the corpus no longer has while missing
ones it does. So the producer is RE-RUN at the classification snapshot, at §7's
rlimit; §7's rows may be reused only when classifying at §7's own commit. That
makes both populations **upper bounds**: a property the current encoding proves
only at the normative `proveRlimit` is still inside them, so every downstream
figure overstates the headroom available to a sequence encoding, in a known
direction. Re-running the subtraction at a different budget is an AMENDMENT
under the protocol above and re-derives BOTH populations. **It moves them in
whichever direction the budget moved**: RAISING the budget (solver and scripts
unchanged) proves more at step 2, subtracts more, and shrinks both; LOWERING it
subtracts less and GROWS both. An earlier draft claimed shrinkage
unconditionally, which is true only of the increase. A result quoted without its
budget is not a result.

**B IS A PROPER SUBSET OF A, and that makes conflating them MORE tempting rather
than less.** Every B member satisfies R1 and so enters A; A additionally holds
the `List`-of-scalar arm, and B additionally removes the false properties. A
`List Int` rotation law is in A and cannot be in B; a false `Str` property is in
A and is removed from B; a `Str` property the current encoding already proves is
in neither. **Nesting is not a rounding.** A figure over A is not a bound on B
in the direction anyone wants — B is smaller, but its members are classified
under a different question — so any sentence quoting one figure for both is
wrong however the arithmetic checks out. An earlier draft of this paragraph
asserted the two were NOT nested, which was false and was caught in review;
recorded rather than silently corrected, because the wrong set relationship
would have propagated into every downstream count.

### 9.2 The derivation, as a transformation

Each row is an OPERATION with an owner, so a later reader can ask of any
disposition which step made it and on what authority. **Steps 1–2 are shared and
the derivation BRANCHES AT STEP 3**: A takes both arms of the partition, B takes
the R1 arm only and never sees an L1-only goal. Steps 4–5 then run inside each
population, and step 6 applies to B alone. An earlier draft called steps 1–5
shared, which left every L1-only goal formally inside B — the exact conflation
§9.1 forbids, reintroduced by the table that was supposed to prevent it.

| # | operation | applied to | authority for the disposition |
|---|---|---|---|
| 1 | **START** from the non-proven per-object properties | the committed store's own metadata, §1's set | the store |
| 2 | **SUBTRACT** everything the CURRENT encoding already DECIDES — proofs and refutations alike | §7's *proves when attempted* row, §7's *refuted — demonstrated non-theorem* row, and every property carrying a recorded `refuted` verdict | §7's measured sweep; the store |
| 3 | **PARTITION** by subject type and BRANCH — A takes the R1 arm and the L1 arm, **B takes the R1 arm only**; what satisfies neither rule is dropped, being out of the universe rather than unreachable | the survivors of 1–2 | R1 (`Str`), L1 (`List` of scalar) |
| 4 | **TAG** each translation bail in the partitions with its REPRESENTABILITY disposition | the bails §6 and §7 agree emit no candidate script | §6's recorded cause, per bail |
| 5 | **CLASSIFY** each goal, taking step 4's tag as its first input | each partition | R1–R5 for `Str` subjects, L1–L5 for `List`-of-scalar subjects |
| 6 | **REMOVE** properties established FALSE — **population B only** | B's partition | an admissible falsity witness, per §9.2.1 |

#### 9.2.1 What establishes FALSE, pinned before step 6 is run

"Known false" is not self-defining, and left open it would let B's membership be
decided while classifying — which the amendment protocol forbids. **Two
categories of evidence are admissible, and nothing else:**

  - a **recorded `refuted` verdict** — the prover demonstrated a non-theorem.
    **Step 2 removes these from BOTH populations, so this category should never
    fire at step 6.** If a refutation is recorded after step 2's snapshot, do
    NOT remove it from B here: re-run step 2 for both populations instead. **The
    two populations must always rest on ONE snapshot** — removing late from B
    alone would leave A carrying a goal the current encoding has already
    decided, which is exactly the headroom overstatement step 2 exists to
    prevent, reintroduced by the repair;
  - a **counterexample EXHIBITED BY EVALUATION** and recorded with its concrete
    inputs, so a reader can reproduce it without a solver. This is the #161
    route.

**Explicitly NOT admissible**, because each looks like falsity and is not: a
`countermodel-withheld` row (the verdict is withheld precisely because the
countermodel was not confirmed — it becomes admissible only by being evaluated,
at which point it is the second category); a solver `unknown` however long it
ran; a reviewer's or an author's argument that the property "looks false"
without a witness. **A removal at step 6 cites its witness or does not happen.**

**ONE SNAPSHOT FOR THE WHOLE DERIVATION, NAMED IN THE RESULT.** Steps 1 and 2
read different artefacts — the store's current metadata, and §7's recorded sweep
— and nothing forces them to describe the same corpus. A verdict is a fact about
a HASH, so once identities move, §7's rows can name objects no live name
resolves to while step 1 enumerates goals the sweep never attempted. The residual
is then a subtraction between two different corpora, which is this file's own
store-history-versus-corpus error with a clock added.

**This mismatch is LIVE, not hypothetical.** §7 ran against the commit named at
the head of this file; `codebase/names.json` has moved since, in `ebbe7a2` — the
#161 config repair, which is the very pair §8 left BORDERLINE. So the rule, and
it forecloses the tempting shortcut:

  - pin every step to ONE commit, name it in the result, and **re-run the
    producer at that commit** rather than reusing §7's rows across a change of
    identity. The producer exists precisely so this costs one command;
  - reusing §7's sweep is permitted ONLY when classifying at §7's own commit;
  - **the `Str` dispositions §8 recorded are not carried forward automatically**
    — they were taken before `ebbe7a2` and must be re-derived at the
    classification snapshot like anything else.

**Step 2 in full, because it is the error §8 made twice.** A stored `tested` is
not evidence that the current encoding cannot prove the property; §7 measured a
set of them discharging from the state the store already records. Leaving those
in would credit a sequence encoding with proofs the EXISTING encoding produces —
store advancement reported as encoding impact. Subtract them from **BOTH**
populations, not just from B.

**A DECISION IS A DECISION WHETHER OR NOT THE STORE HAS CAUGHT UP.** §7's sweep
refuted goals whose refutations it did not persist — it writes nothing — so a
rule subtracting only RECORDED refutations would readmit them and credit the
sequence encoding with a decision the current encoding had just made in front of
us. Step 2 consumes §7's refuted row as well as the store's.

**AND THE SAME ARGUMENT REACHES REFUTATIONS, WHICH AN EARLIER DRAFT LEFT IN.**
Population A asks whether a sequence encoding could DECIDE a goal, and deciding
includes refuting — so a property the current encoding has ALREADY refuted is
already decided, and counting it as reachable-by-`Seq` credits the new encoding
with a decision the existing one made. Subtract recorded refutations at step 2,
from both populations. **The boundary is who decided it, not whether it is
true:** a property that is UNDECIDED today and later turns out false stays in A,
because there the sequence encoding would be the thing that settles it. That is
the same distinction §8 got wrong from the other side, when it removed false
properties from the reachable set for being false.

**And step 2's honest limit, pinned now rather than discovered later.** §7's
sweep ran at its own pinned rlimit, well below the normative `proveRlimit` — §7
states both figures and the ratio, and they are not restated here. The set it
identified is therefore a LOWER BOUND on staleness: a
property that would discharge at the normative budget but not at §7's is not
subtracted and stays in both populations. The residual may still contain
properties the current encoding proves. That biases every downstream figure in
the SAME direction — it overstates the headroom available to a sequence encoding
— and any result must say so rather than presenting the residual as exactly the
set the current encoding cannot reach.

### 9.3 Step 4 — translation bails are TAGGED by REPRESENTABILITY

A bail is a goal the strategy sequence could not translate at all, so it never
reached a solver. §8 first excluded these, then argued they were wrongly
excluded on an unverified example, then withdrew that argument. The rule that
settles it is not about which side was right; it is about which question a bail
answers.

> **Ask whether re-representing the subject as a `Seq` makes the TERM
> TRANSLATABLE. Do NOT ask whether a solver would then decide it.** The second
> question is step 5's, and a goal that still does not translate never gets
> there.

**A BAIL IS A CLASSIFICATION INPUT, NOT A MEMBERSHIP TEST — this is why the step
moved after the partition.** Membership in a population is decided by the
subject rules and by nothing else; that is what "derive the universe from the
claim" means here, and #68's claim is about subjects, not about which goals the
current translator happens to handle. A bailing goal that satisfies R1 or L1 is
IN, and the representability question then decides its VERDICT. An earlier draft
of this section made the bail an exclusion filter running before the partition,
which dropped in-scope goals out of the denominator and contradicted §9.5's own
membership rule two subsections later — the same shrink-the-universe move §8
died of, re-entering through a different door.

| the cause §6 records for the bail | does a `Seq` representation change it? | tag, and the verdict it forces |
|---|---|---|
| the carrier — a list/string term the translator cannot encode | **yes**, by hypothesis | **REPRESENTABLE** — no verdict yet; continue to L2–L5 / R2–R5 |
| an unsupported term form (e.g. `lam`) | no — the form is unsupported whatever the subject's carrier is | **UNAFFECTED** — **NOT REACHABLE** (hard), citing the cause |
| a trusted/opaque call the translator refuses to model | no — the refusal is about the callee, not the carrier | **UNAFFECTED** — **NOT REACHABLE** (hard), citing the cause |
| a partial application | no — arity, not representation | **UNAFFECTED** — **NOT REACHABLE** (hard), citing the cause |
| any cause not listed above | undetermined by this table | **AMENDMENT REQUIRED** before the goal is classified |

An UNAFFECTED bail is NOT REACHABLE rather than out of scope, and the difference
is exactly the one §8 kept losing: it is an in-scope goal that a sequence
encoding demonstrably does not reach, which is a finding. Dropping it instead
would shrink the denominator and inflate every proportion computed over it.

The last row is the point of writing this as a table: a cause with no row is a
gap in the criterion, and the protocol above says what to do with it. An
unlisted cause disposed of at the point of use is exactly how §8 acquired a
fabricated exclusion.

### 9.4 L1–L5 — the `List`-of-scalar criterion

R1–R5 (§8) are written about `Str` and their TEXT is unchanged. This is their
counterpart, and it is **SHAPE-BASED**: every rule is a question about the goal's
types and term forms, so it is answerable from the kernel's own `oath get` output
and the definition body, with no solver and no re-encoder. Where a rule cannot be
answered that way it is marked SOFT below, and a goal disposed of by a soft rule
alone is BORDERLINE, never NOT REACHABLE.

**R1–R5 CARRY NO HARDNESS LABELS, AND §9.5 CANNOT CLASSIFY WITHOUT THEM.** §8
wrote them before the hard/soft distinction existed, and it disposed of R2–R5
failures inconsistently as a result — one goal NOT REACHABLE, another pair
BORDERLINE, with nothing in the criterion saying which was right. Rather than
rewrite a withdrawn section's rules, the labels are DERIVED here by mapping each
`Str` rule onto the `List` rule that asks the same question, and every clause
below — the unfolding step, the admission of core equality, the bridge registry,
the two-part stripping test — applies to the `Str` arm through this mapping:

| `Str` rule | asks the same question as | hardness it inherits |
|---|---|---|
| R1 subject | L1 | HARD |
| R2 operations reduce to the seq signature | L2 | HARD, after unfolding, with core equality admitted and recursive symbols routed through the bridge registry |
| R3 no quantifier alternation | L3 | HARD, with L3's soft carve-out for a witness the signature can name |
| R4 arithmetic entanglement | L5 | SOFT throughout — a hard verdict needs a recorded failure on the RE-ENCODED goal |
| R5 recursion is not the obstacle | L4 | per L4a's precedence table — HARD only on a recorded decision failure over the re-encoded goal; L4b (induction-only) SOFT |

**R4 AND L5 SUPPLY NO HARD FAILURES TODAY, WHICH IS WORTH SAYING AS A FACT ABOUT
THE CRITERION** — and saying it WITHOUT naming any goal, since classifying one
here would break this section's own ordering control. Their hard branch needs a
recorded failure on a RE-ENCODED goal, and no goal has ever been re-encoded. An
outcome on the goal as the kernel translates it today is a different claim, and
treating one as the other is this repository's own *a statement about the tool is
not a statement about the world* arriving inside the criterion meant to prevent
it. **Consequently §8's `Str` dispositions are NOT carried forward** — as the
snapshot rule already requires — and where they land under these rules is the
next round's result, not this section's.

The criterion is counterfactual in the same way R1–R5 are: nothing in the corpus
reaches Z3's `Seq` sort today, so each rule asks what would be true IF the
subject were re-encoded onto `Seq`.

**L1 — SUBJECT (hard).** A binder of the property, the definition's result, **or
ANY term occurring in the goal** is typed `List σ` as the kernel types it, where
σ is a SCALAR: a sort with no user-datatype structure — `Int`, `Bool`, `Rat`,
`Float`. A user datatype is not a scalar, and that includes `Str`, so `List Str`
fails L1 on that subject (its `Str` part is R1's business, not L1's).

  - **"OR ANY TERM" IS LOAD-BEARING AND WAS MISSING FROM THE FIRST DRAFT.** A
    property can construct a list and apply list operations to it while every
    binder is an `Int` and the result is some other datatype — a law about an
    encoder applied to a literal-built list is exactly that shape. Keying the
    subject rule to binders and results alone would drop such goals at step 3
    as out of scope, which is the incomplete-population failure this whole
    section exists to repair, reproduced one level down. **The same widening
    applies to R1 through §9.4.1's mapping**: a `Str` term in the goal is a
    `Str` subject whether or not a binder carries the type.

  - **A distinct datatype whose CARRIER happens to be a list does NOT satisfy
    L1**, even where the kernel would unfold it to one. This is a decision, made
    here rather than at the point of use: the bridge #68 proposes must justify a
    datatype↔seq equivalence PER DECLARATION, and for a container whose intended
    semantics is not the carrier's — unordered, keyed, deduplicated — a `Seq`
    encoding of the carrier does not model the type. Admitting one would be
    exactly the soundness risk #68's own design constraints forbid. A later
    round that wants such a type in the population must ARGUE THE BRIDGE in an
    amendment; it may not simply widen the count.
  - **A goal carrying several sequence-shaped subjects: MEMBERSHIP comes from
    ANY ONE subject satisfying R1 or L1; the others affect only the VERDICT.**
    Deciding the goal means deciding all of it, so an unhandled subject forces
    NOT REACHABLE or BORDERLINE according to that subject's own hardness — but
    it never removes the goal from the population. An earlier draft wrote this
    as "qualifies only if EVERY subject is handled", which made one condition
    readable as both a membership test and a verdict test, so the denominator
    depended on which rule a classifier reached first.
  - **NESTED SEQUENCE SUBJECTS — `List Str`, `List (List τ)` — have their own
    clause, because without one the rule above is unsatisfiable for goals that
    contain them.** Such a subject satisfies neither L1 (its element is not a
    scalar) nor R1 (it is not itself `Str`), so a goal entering the population
    through some OTHER subject would have a subject no criterion reaches. Pinned
    disposition, in two parts:
      - a nested sequence subject **does not confer membership**: it satisfies
        no subject rule, so it cannot pull a goal into population A on its own;
      - where a goal is already a member and carries one, the nested subject is
        **SOFT**. `(Seq (Seq τ))` is expressible in z3, so this is not a
        shape-level impossibility; how far the decision procedures extend over
        nested sequences is exactly the unmeasured kind of question. So such a
        goal is BORDERLINE, never NOT REACHABLE on account of the nesting.

**L2 — SEQUENCE OPERATIONS (hard where the term forms are read off the body).**
**Evaluate L2 on the goal AFTER UNFOLDING every non-recursive user definition
applied to a list subject, to a fixpoint** — then every operation remaining on a
list subject must reduce to the seq signature: `seq.++`, `seq.len`, `seq.unit`,
`seq.empty`, `seq.at`, `seq.nth`, `seq.extract`, `seq.prefixof`, `seq.suffixof`,
`seq.contains`, `seq.indexof`, `seq.replace`. An operation still outside that
list after unfolding has no theory after re-encoding.

  - **THE WHITELIST IS LIST-SPECIFIC OPERATIONS ONLY. SMT CORE STRUCTURE IS
    ADMITTED THROUGHOUT AND IS NOT MEASURED AGAINST IT** — equality and
    disequality over sequence-valued terms, `ite`, and the boolean connectives.
    Core equality is defined at every sort, sequences included, so reading the
    whitelist as exhaustive over ALL term forms would hard-fail every law of the
    form *this sequence expression equals that one*, which is the shape most
    list laws take. An earlier draft was open to that reading.

  - **THE UNFOLDING STEP IS NOT A DETAIL; WITHOUT IT L2 IS A NAME CHECK.** A
    goal states its law about a user-defined wrapper, essentially never about
    `seq.++` directly, so a literal whitelist applied to the surface term would
    hard-fail every candidate in the corpus — including the ones this file
    elsewhere calls the strongest — before their bodies were looked at, and it
    would contradict L4a, which explicitly admits non-recursive user functions.
    An earlier draft did exactly that and was caught in review. What L2 rejects
    is an operation with no seq counterpart ONCE THE DEFINITIONS ARE GONE.
  - **Recursive user functions are L4a's business, not L2's.** Unfolding stops
    at them by definition, and L4a decides them — through the BRIDGE REGISTRY
    below, which is what stops L2 and L4a defining each other in a circle.

**THE BRIDGE REGISTRY — how a RECURSIVE user function may count as a seq
operation at all.** L2 admits non-recursive definitions by unfolding them; L4a
admits a recursive one only if it "is an L2 operation", and nothing so far says
how that could ever be established. Left there the pair is circular, and the
circle bites exactly the interesting goals: a list rotation expressed through
recursive `append`/`take`/`drop` has no route to qualify and no route to be
honestly refused. The rule that breaks it:

  - A recursive user definition counts as an L2 operation ONLY through an
    explicit REGISTRY ENTRY naming the definition and the seq operation it is
    claimed to denote — `append` ↦ `seq.++`, `take`/`drop` ↦ `seq.extract`, and
    so on. **An entry is a CLAIM OF EQUIVALENCE between a recursive definition
    over the inductive datatype and a seq term**, which is precisely the
    datatype↔seq bridge #68 proposes and #68's own design constraints say must
    be verified before anything is discharged through it.
  - **The registry is EMPTY at the time of writing, and adding an entry is an
    AMENDMENT** under the protocol above: name the definition, name the seq
    operation, state the equivalence, say whether it is proved or asserted.
  - **A recursive symbol with NO entry is not thereby unreachable** — absence of
    evidence is not disproof, and an empty registry must never be stricter than
    an asserted entry. What an empty entry means depends on the callee's
    termination verdict as well, so the registry does NOT decide alone: **the
    single authority for a recursive callee is the precedence table in L4a
    below**, which takes both facts as input. Nothing in this bullet is a
    verdict.
  - **An entry that is asserted rather than proved is SOFT.** Any goal whose
    classification depends on one is therefore BORDERLINE — never NOT REACHABLE
    and never CONFIRMED REACHABLE. That is the right resting place: the whole
    question #68 asks is whether such bridges can be justified, so a criterion
    that silently assumed one would be answering the issue by assumption.
  - **Regular membership IS in L2's signature, and this one is MEASURED rather
    than reasoned.** An earlier draft excluded it on the premise that `re.*` is
    a string facility whose applicability to `(Seq Int)` was unknown. That
    premise is false on the z3 this repository runs (4.16.0): `seq.in.re` over a
    `(Seq Int)` with a regex built from `seq.to.re`/`seq.unit` is accepted and
    solved. So `seq.in.re`, `seq.to.re` and the `re.*` constructors are admitted
    for the `List` arm on the same footing as for `Str`, and a goal using
    regular membership continues through the remaining rules instead of being
    parked at BORDERLINE by a wrong premise. The measurement is a one-line
    script and is worth re-running if the pinned solver moves.

**L3 — QUANTIFIERS (hard).** No quantifier alternation: the property's binders
are universal, and after negation no quantifier remains over a list, an index or
an element. A bounded existential is admitted ONLY if its witness is named by a
seq operation — `seq.indexof` supplies the index, `seq.extract` the segment. An
existential the signature cannot name survives negation as a universal over the
sequence and leaves the fragment.

**L4 — USER RECURSION.** Two clauses, and they differ in HARDNESS as well as in
direction, which an earlier draft of this section missed by labelling both hard:

  - **L4a — and it SPLITS ON TERMINATION, which the first draft missed by
    assuming every recursive callee is uninterpreted.** It is not: `ensureFn`
    (`oath/prove.go`) declares the symbol and, **for a function PROVEN TOTAL,
    asserts its defining equation as a quantified axiom**; only a non-total
    callee is left declared-but-uninterpreted. So:
      - a recursive callee **proven total** arrives carrying its defining
        equation, so the goal is not theory-free and may well be decided even
        though the callee matches no whitelisted operation;
      - a recursive callee **NOT proven total** is left declared without an
        equation, so the re-encoded goal is `Seq` plus an uninterpreted symbol —
        which is a weaker position, **not a hard failure**.
    Both are SOFT, and the exact dispositions with their precedence are in the
    table below, which is the authority; this bullet only says what the kernel
    does.
    **The axiom is a universal assertion, which is itself why this is soft
    rather than admitted:** it does not create alternation inside the negated
    goal, but it does take the problem out of the purely quantifier-free
    fragment L3 targets, and how far z3's sequence solver carries it is exactly
    the unmeasured thing.

**PRECEDENCE FOR A RECURSIVE CALLEE — ONE TABLE, BECAUSE TWO RULES REACHED THE
SAME QUESTION.** The bridge registry and the termination split are both about
"may this recursive symbol be treated as a sequence operation", and read as
peers they contradict each other in both directions: an unregistered total
callee would be simultaneously unresolved and SOFT, and a non-total callee with
an asserted entry would be simultaneously BORDERLINE and NOT REACHABLE. They are
therefore reduced to one derivation, consulted top to bottom, first match wins:

| # | registry entry | termination verdict | outcome |
|---|---|---|---|
| 1 | **PROVED** | any | the callee IS that seq operation — continue under L2, L4a does not fire |
| 2 | none, asserted, or "no counterpart claimed" | **proven total** | **BORDERLINE (SOFT)** — it arrives with its defining axiom, and whether that suffices is L4b's unmeasured question |
| 3 | none, asserted, or "no counterpart claimed" | **not proven total** | **BORDERLINE (SOFT)** — declared with no equation, so the re-encoded goal is `Seq` + an uninterpreted symbol. **HARD (NOT REACHABLE) only where a decision failure on the RE-ENCODED goal is actually recorded** |

**ROW 3 WAS HARD IN AN EARLIER DRAFT AND THAT WAS THIS REPOSITORY'S SIGNATURE
MISTAKE — AN IMPLEMENTATION LIMIT REPORTED AS A SEMANTIC FACT.** `terminationOf`
is conservative and answers `unknown` for any shape it cannot handle
(`oath/termination.go`), so "not proven total" is a statement about the
ANALYSER. And an uninterpreted symbol does not make a formula undecidable:
`Seq` + UF is frequently decided, not least when the goal is refutable. Hard-
failing every such goal would have excluded goals sequence theory can settle,
on the strength of a non-proof.

**Why a PROVED entry outranks everything and an ASSERTED one outranks nothing.**
Row 1 is an established equivalence, which is what the bridge is for; an
asserted entry establishes nothing and therefore changes no row. **So L4a is
hard in exactly one circumstance — a RECORDED decision failure over the
re-encoded goal — and nothing else about recursion produces a hard verdict.**
That is the honest consequence of having no re-encoder: recursion mostly cannot
be disposed of yet, and the criterion says so instead of guessing.
  - **L4b (SOFT).** The goal must not HOLD only by induction over the list. Seq
    theory supplies decision procedures, not induction. **But "this law is true
    only by induction" is a SEMANTIC JUDGEMENT, not a shape** — a recursive
    identity whose operations otherwise map onto `Seq` may or may not need
    spine induction once re-encoded, and no reading of `oath get` output settles
    it. So a goal disposed of by L4b ALONE is BORDERLINE, never NOT REACHABLE.
    Calling it hard would have licensed a post-hoc exclusion wearing a
    shape-based rule's clothes, which is the failure mode §8 records.

**L5 — LENGTH AND INDEX ARITHMETIC (SOFT throughout, until a re-encoded goal can
actually be run).** The coupling between `seq.len`, index expressions and Int
reasoning is where sequence solvers actually stop.

  - **The stripping test, the same instrument as R4 — and it is SOFT
    THROUGHOUT.** Replace every list term with a fresh variable and every length
    with a fresh non-negative Int. Whether an Int obligation REMAINS is read off
    the stripped goal, and a remaining one is evidence the sequence structure
    was the carrier rather than the subject. **It is not more than evidence.**
    Stripping DISCARDS constraints — the fresh variables are unrelated where the
    sequence terms were not — and re-encoding can add relationships and change
    what the solver simplifies, so a failure on the stripped obligation does not
    establish that re-encoding "moves nothing", and an `unknown` or an exhausted
    budget establishes less still. **The disposition is BORDERLINE. A hard NOT
    REACHABLE here needs a recorded failure on the RE-ENCODED goal at the pinned
    budget**, exactly as in L4a's row 3.
  - **Truncating division and modulo do NOT fail L5 as untranslatable.** An
    earlier draft of this rule said they did, citing §6 — that premise is
    SUPERSEDED: #71 is closed and the kernel translates `/` and `%` through
    `oath_tquo`/`oath_trem` (SPEC §7.1, `oath/prove.go`). They may still defeat
    the solver, and div/mod coupled to `seq.len` is outside any fragment z3
    decides — but that is a SOFT disposition yielding BORDERLINE, not a
    translation failure. The rotation laws are the class this would have
    wrongly excluded, and they are the strongest candidates the corpus has.
  - **Linear length constraints across a bounded number of concatenations are
    handled in practice but are not decided in general.** A goal whose only
    remaining coupling is of that form is SOFT, hence BORDERLINE, not reachable.

### 9.5 The verdict classes, pinned so they are not decided per goal

| class | condition |
|---|---|
| **OUT OF POPULATION (A)** | satisfies neither R1 nor L1. Not a verdict — the goal is not in A's universe, is excluded at step 3, and appears in NO denominator over A |
| **OUT OF POPULATION (B)** | fails R1 — **B's subject rule is R1 ALONE**, so every L1-only goal is out of B whatever its L-classification, and a rotation law can never touch the title question's denominator |
| **NOT REACHABLE** | is IN the population, and fails a HARD rule — on grounds decidable from the kernel's types and the definition body, OR from a measurement this section specifically admits (a RECORDED failure on the re-encoded goal, under R4/L5 and L4a's row 3; the §9.6 capability probe). No other measurement may harden a rule at the point of use |
| **BORDERLINE** | is in the population, and every remaining obstacle rests on a SOFT disposition — reasoning about z3's sequence solver rather than a measurement |
| **CONFIRMED REACHABLE** | the goal was re-encoded onto `Seq`, z3 answered `sat` or `unsat` within a stated rlimit, **AND no SOFT assumption remains open** — in particular every bridge-registry entry the encoding used is PROVED, not asserted. A goal decided through an asserted bridge stays BORDERLINE, because the bridge is the thing #68 asks about. **Not achievable this round; no re-encoder exists.** |

**THE FIRST ROW IS A MEMBERSHIP FACT, NOT A CLASSIFICATION, AND KEEPING THEM
APART IS THE WHOLE LESSON OF §8.** The subject rules DEFINE the population;
failing one means the goal was never in scope, not that a sequence encoding
cannot reach it. §8's headline table reported goals with no `Str` anywhere as
NOT REACHABLE, which put out-of-scope subjects into the denominator and made a
narrow universe look like a surveyed one. An earlier draft of this table
repeated it. A goal that fails both subject rules is reported, if at all, as
what was EXCLUDED and why — never as a verdict about sequence theory.

**FALSITY IS NOT UNREACHABILITY, and it is a step-6 concern, not a step-5 one.**
Deciding includes answering `sat` with a counterexample, so a false property is
classified for population A exactly as a true one is — §8 records this being got
wrong and corrected. Population B removes it at step 6 instead, for the
different reason that it can never become `proven`. **One goal, two populations,
two dispositions, no contradiction** — which is the whole reason the populations
are kept apart.

### 9.6 What this section deliberately does not do

- It runs nothing over the corpus. The producer is untouched and
  `git status codebase/ fixtures/` is unchanged by this commit.
- **It DOES invoke a solver once, and pretending otherwise would be the same
  class of overclaim as the heading this section already had to correct.** L2's
  regular-membership clause rests on a capability probe against the z3 on PATH
  (4.16.0), reproduced here in full so it is evidence rather than an assertion:

        (declare-const xs (Seq Int))
        (declare-const x Int)
        (assert (seq.in.re xs (re.+ (seq.to.re (seq.unit x)))))
        (assert (= (seq.len xs) 3))
        (check-sat)

  It returns `sat`. **It mentions no corpus goal and classifies nothing** — it
  establishes what the SOLVER supports, which is why it can sit inside a
  criterion instead of inside a result. A rule resting on an unmeasured premise
  about the solver is how the earlier draft got this backwards, parking goals at
  BORDERLINE on a guess.
- It looks up no candidate's type. Deriving types is step 3 and belongs to the
  round this section precedes.
- It offers no count, no bound and no recommendation for #68 — which remains
  UNANSWERED, exactly as §8 left it.
