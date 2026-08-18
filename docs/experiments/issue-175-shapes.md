# #175 — can documentation lift the SATISFIED rate, or is machinery needed?

**Status: MEASURED, INCLUDING A CONTROLLED BLIND RUN. The recommendation is
DECLINE #175 — but NOT on the ground the issue offers.**

The issue's falsifier asks whether DOCUMENTATION lifts the hit rate. Measured
against a control, it does not: guided and unguided readers both averaged 6 of 7
and the unguided ones were cheaper. So that condition is NOT met.

What the run establishes instead undercuts the issue's premise directly. The two
CONTROL subjects — given the seven intents, the corpus, and nothing but the mode
list and query syntax — reached 6 of 7 each, with no translation layer and no
change to the tool. That is exactly the situation #175 describes. All four
subjects averaged 6 of 7 whether guided or not; the 2-of-7 baseline was one
author's single pass, not a ceiling.

**Decline because the gap #175 was opened to close is not there**, not because a
page fixed it.

## The falsifier, quoted rather than paraphrased

#175 states its own "no change required" condition:

> If the SATISFIED rate can be lifted by better *documentation* of how to phrase
> a query — worked examples of the shapes the corpus uses — rather than by
> machinery, then this is a docs problem and should be declined as an
> engineering one. The falsifier record's per-intent table is the baseline to
> beat: 2 of 7.

## Result: 2 of 7 becomes 5 of 7, with no change to the tool

| # | intent | baseline | re-phrased | what changed |
|---|---|---|---:|---|
| 1 | report a required config key the host did not supply | no | **yes** | RETURN shape: `(List Str)` → `Str` |
| 2 | read a request header by name, with a fallback | yes | yes | — |
| 3a | scan a byte body for a JSON string value | no | no | fragment-blind |
| 6 | make a field safe to splice into a delimited record | no | no | fragment-blind |
| 9 | a prefix match that only matches at a delimiter | yes | yes | — |
| 11 | test whether a list of `Str` contains an element | no | **yes** | ABSTRACTION: value → predicate |
| 12 | take the longest prefix whose elements pass a test | no | **yes** | POLYMORPHISM: `[a]` on the query definition |

The three recovered intents are all SHAPE failures on three different axes; the
two that stayed are both the SAME failure, and it is not a shape failure.

**THE TOOL DID NOT CHANGE.** `--spec`'s fallback hint about polymorphic type
application landed at d2dc407 on 2026-07-28, three weeks before the baseline run
(63fa523, 2026-08-16). Every hit below comes from a definition that was already
in the corpus at the baseline. The whole difference is how the query was
written.

## The three axes

**Return (intent 1).** The baseline asked for the missing keys, `(List Str)`.
`config-missing` returns the first one, `Str`. The baseline query returned
nothing AND no signature-compatible fallback, so the output was
indistinguishable from an empty corpus. Changing only the return type:

```
· nothing-needed-nothing-missing    config-missing  ← provably satisfies it
· a-missing-key-is-reported         config-missing  ← provably satisfies it
```

**Abstraction (intent 11).** The baseline asked for
`(-> Str (List Str) Bool)`. `any` is `(-> (-> a Bool) (List a) Bool)` — the
fixed value generalized to a predicate. Restated in that shape:

```
· a-passing-head-makes-it-true
    any    ← provably satisfies it (direct (lemma-free))
    1 REFUTED    all    countermodel: <fn {20→false 0→false -2→false} else true>, 13, (Cons 0 Nil)
```

**THIS ONE CONTRADICTS THE BASELINE RECORD, WHICH IS WHY IT IS THE MOST
IMPORTANT RESULT HERE.** `issue-74-falsifier.md` concluded of intent 11:

> No mode can reach it, because the satisfying artifact has a **different
> structural shape** from the intent … This is a gap distinct from
> law-statement and it survives any improvement to how laws are written.

It does not survive. What the baseline varied was the LAW; what reaches `any` is
varying the SHAPE, and the two were treated as one thing. The corrected claim is
narrower and still real: *generalizing primitive leaves does not bridge an extra
parameter* — true, and it says nothing about whether the caller can supply the
right shape themselves.

**Polymorphism (intent 12).** The flagship demand: `bytes-until` was written by
hand while `take-while` sat PROVEN. Monomorphic, it finds nothing even with the
law copied verbatim from the target. Declared `[a]`:

```
· every-kept-element-passes
    filter        ← provably satisfies it (induction on binder 1)
    take-while    ← provably satisfies it (induction on binder 1)
    1 REFUTED     drop-while    countermodel: <fn {-2→false -14→true 19→true} else false>, (Cons 4 (Cons 8 (Cons -2 Nil)))
```

**AND THE BASELINE ATTRIBUTED THIS TO THE WRONG HALF, which review caught and a
2x2 settled.** `issue-74-falsifier.md` says its (b) and (c) "state the same
mathematical law and differ only in whether the query's own recursion carries a
`[Int]`". They also differ in whether the DEFINITION is polymorphic, and that is
the half that matters:

| query definition | the law's recursive call | result |
|---|---|---|
| monomorphic | `(wanted p xs)` | nothing |
| `[a]` | `(wanted [Int] p xs)` | `filter`, `take-while` proved |
| `[a]` | `(wanted p xs)` | `filter`, `take-while` proved |

The type application is INFERRED, so writing it changes nothing. A first draft
of the documentation taught it as mandatory — a made-up rule, derived from
reading the baseline rather than from running the third row.

## The two that stayed for the ORACLE run — and did not stay for the blind one

**Superseded by "The blind run" below: both blind subjects reached both targets
on `--spec`, first try.** What survives is the narrower fact this section
measures, which is about `--implies` only.

Both are #177's measured blind spot. `--implies` appends the query law to a
candidate and proves it; a body the translator cannot build never reaches a
solver, so the mode reports NO VERDICT — which the CLI already labels *"a limit
of this prover, NOT a fact about the definition"*.

```
intent 6   3 REFUTED (config-key, gh-group, shout) · 1 NO VERDICT   ← the target
intent 3a  2 NO VERDICT
```

Shape does not help, because the obstacle is the CANDIDATE's body, not the
query's signature. #177 measured this at 12 of 194 candidates and DECLINED it;
`--equiv` reaches them but takes an implementation rather than a law.

**BUT `--details` NAMES THEM, which the falsifier's scoring hides and which
review surfaced here.** The same intent-6 run with the flag:

```
1 NO VERDICT — the prover did not settle it (a limit of this prover, ...)
    record-field       "lam" terms are outside the provable fragment
```

That is the target, named, with the reason. It is still not SATISFIED — nothing
was proved, and the scoring above is unchanged — but it means the practical gap
for a fragment-blind target is smaller than "you must write the implementation":
the caller gets a name to read. The procedure in `discovery.md` puts `--details`
ahead of `--equiv` for exactly this reason.

## Circularity, excluded the way the baseline excluded it

**And the exclusion turned out to be wrong for a caller who does not know the
target — see the blind run below.** It is right for the runs in THIS section,
whose author did. Kept as recorded, because that is what was done here.

The baseline dropped intent 5 because "the query came out AST-identical to the
target's own law". The same rule is applied here, and it cost one result:

- Intent 11's first draft stated `(== (wanted p (Cons x xs)) (or (p x) (wanted p xs)))`,
  which IS `any`'s own `cons-step` under self-substitution. It hit, and it was
  discarded; the scored law is `(or (not (p x)) (wanted p (Cons x xs)))`, the
  author's own wording, which `--spec` misses and `--implies` proves.
- Intent 3a's natural phrasing — "the scan never keeps a quote byte" — comes out
  AST-identical to `json-string-value`'s own `no-quote`, so `--spec` would hit
  it circularly. It is scored unsatisfied, on `--implies`, as the baseline
  scored it.

## Reproducing

`docs/experiments/issue-175-shapes/` holds every scored query as a file and
`run.sh` re-asks them against a temporary copy of the committed corpus, printing
each mode's whole answer rather than the lines that agree with this write-up.
`transcript.txt` is one run.

**All seven, including the two that already passed.** A first draft ran only the
intents that CHANGED, which cannot verify a numerator and would not notice a
prior hit regressing. Adding them back was not a formality: intent 9's law was
reconstructed with its arguments the wrong way round, and in that form
`str-prefix` PROVES it — the hits looked reproduced while the REFUTATION that is
the whole point of intent 9 had quietly vanished. A control that only confirms
is not a control. Budget ~20 minutes: `--implies` proves, so it is
slowest exactly where nothing proves, and intent 3a alone runs over ten minutes
before answering NO VERDICT.

**It reproduces the QUERIES, not the STUDY.** Whether a given shape reaches its
artifact is decidable and is what the documentation's claims rest on. Whether a
caller who does not know the target arrives at that shape is the open half, and
no script settles it.

## The blind run — the measurement that decides the issue

The oracle run above had a defect it stated plainly: every query was written by
someone who had read the baseline record and therefore knew each target. The
condition is about a CALLER, so it needed a caller.

**Two subjects, dispatched to a target that inherits no project instructions, no
memory index and no commit digest**, each into its own copy of an isolated
export: the `oath` binary, a corpus copy, the seven intents, and a guide. No
`.git`, no `docs/`, no `CLAUDE.md`. Both were asked what context they had
received before opening the task, and both answered clean; one disclosed two
weak DOMAIN signals (the working directory is named `oath-lang`, and MCP tool
names implying the mode names) which carry no work state and no part of the
answer key.

**THE GUIDE THEY READ IS NOT THE SHIPPED PAGE, and that is a real narrowing.**
The shipped section names four of the seven targets in its worked examples — the
examples ARE these intents — so it hands a reader of exactly these seven their
answer. What was exported is the transferable content: the three axes and the
procedure, stated generically, with no definition names and no signatures. A
preflight confirmed it; the only hits were "any" and "contains" as ordinary
English quantifiers.

| # | artifact | baseline | oracle | subject 1 | subject 2 |
|---|---|---|---|---|---|
| 1 | `config-missing` | no | yes | yes (`--implies`) | yes (`--implies`) |
| 2 | `header-or` | yes | yes | yes | yes |
| 3 | `json-string-value` | no | **no** | **yes** (`--spec`) | **yes** (`--spec`) |
| 4 | `record-field` | no | **no** | **yes** (`--spec`) | **yes** (`--spec`) |
| 5 | `media-type-is`/`path-is` | yes | yes | yes, BOTH | yes, BOTH |
| 6 | `any` | no | yes | yes | reached, **declined** |
| 7 | `take-while` | no | yes | yes | yes |
| | | **2 of 7** | **5 of 7** | **7 of 7** | **6 of 7** |

### The blind readers beat the oracle, on the two it had written off

Intents 3 and 4 are #177's fragment-blind pair. The oracle run scored them
unreachable because `--implies` returns NO VERDICT, and excluded the `--spec`
route as CIRCULAR — the author knew the target's law, so a hash match proved
nothing. Both blind subjects reached both targets on `--spec`, first try.

**And the circularity exclusion is wrong for a blind caller, which is
established mechanically rather than argued.** A property's NAME is not part of
its hash (`propHashGeneral` encodes generalized binders and the body, nothing
else). Subject 1 named its intent-4 law `never-contains-the-separator`;
`record-field` names its own `never-contains-a-tab`; both hash to
`#968def4dbf2a`. Different name, identical law, written by a reader who had
never seen the definition — that is CONVERGENT PHRASING, not copying, and it is
exactly what a content-hash surface exists to exploit.

The exclusion was built to stop an author who knows the answer from writing it
down. Applied to a caller who does not, it discards real finds. The oracle run
inherited it from the baseline and was wrong to.

### The one disagreement, and it is not about discovery

Intent 6 split. Subject 1 reported `any` as the reachable artifact. Subject 2
FOUND `any`, confirmed it with `--spec`, quoted it — and declined to report it:
a caller must write `(any [Str] (fn [(s Str)] (== s y)) xs)`, which it called
"a composition, not a definition satisfying the intent."

That is the baseline record's own reasoning, reached independently by a reader
who had never seen it. So both subjects reached the same artifact and disagreed
about whether reaching it counts. **The disagreement is about what SATISFIED
means, not about whether discovery worked** — and it is the same question the
oracle run answered one way and the baseline the other.

### What the subjects did that the oracle run did not

Both ran positive CONTROLS before reporting an absence: the same membership laws
at `Int` find `contains`, at `Str` find nothing — establishing their own method
was not silently broken. Neither was asked to. Subject 2 additionally refuted
the one unsettled candidate by evaluation. Subject 1 found that intent 5 has TWO
valid artifacts and that no delimiter-parametric version exists, and read
`filter`'s NO VERDICT on intent 7 correctly as SPEC §7.2's environmentally
aborted strategy rather than as a negative.

## The controlled run — and the null result

The first blind pair was compromised: this tool runs each command as
`bash -c '<whole script>'`, so everything the MEASURER runs is visible in `ps`,
and both control subjects independently reported seeing a script of mine whose
text named the targets. That is the answer key, leaked by the experimenter. Both
disclosed it unprompted; neither was asked about `ps`.

The run was repeated with the channel closed from both ends — the task forbids
inspecting other processes, and the measurer ran NO commands while any subject
was live. Four subjects, two per arm, differing only in whether they received
the three axes and the procedure.

| intent | artifact | t3 (guide) | t4 (guide) | c3 (none) | c4 (none) |
|---|---|---|---|---|---|
| 1 | `config-missing` | yes | **MISS** | yes | yes |
| 2 | `header-or` | yes | yes | yes | yes |
| 3 | `json-string-value` | yes | yes | yes | yes |
| 4 | `record-field` | yes | yes | yes | yes |
| 5 | `media-type-is`/`path-is` | yes | yes | yes | yes |
| 6 | `any` | yes | declined | declined | declined |
| 7 | `take-while` | yes | yes | yes | yes |
| | **score** | **7** | **5** | **6** | **6** |
| | tool calls | 61 | 70 | 55 | 47 |
| | tokens | 89K | 93K | 71K | 88K |

    TREATMENT  mean 6.0 of 7   65.5 calls   91.0K tokens
    CONTROL    mean 6.0 of 7   51.0 calls   79.5K tokens

**The guide made no difference to what was found, and the unguided arm was
cheaper.** Everything the earlier runs attributed to it reproduces without it.

## What actually found the artifacts, none of which was in the guide

- **THE SIGNATURE PROBE.** A query whose only law is reflexive —
  `(== (wanted x) (wanted x))` — matches nothing in THIS corpus, because nothing
  in it states such a law (checked). So `--spec` falls through to
  "N definition(s) have a COMPATIBLE SIGNATURE" and lists the definitions at that
  shape. Not universal: a stored reflexive property would hash-match and suppress
  the list, and the list omits definitions carrying no properties of their own. t3 wrote seventeen of them and called it "the cheapest
  map of the corpus and what surfaced most of the answers". Subjects in both
  arms invented it independently. The guidance never described it; it only said
  the fallback list was a useful signal when it happened to appear.
- **Reading the corpus.** c3 read all 238 names from `codebase/names.json`; c4
  and c1 ran `oath ls`. Every one disclosed it unprompted as possibly biasing
  their query shapes. (Two described `ls` as giving names AND signatures; it
  gives names, hashes, kinds and guarantees only — they were pairing it with
  `oath get`. Checked, because the write-up had repeated their wording.)
- **`oath dependents`.** t3 reached `config-missing` through
  `dependents config-key` — not through any find mode.

## The finding that indicts the documentation

t4 is the only subject that missed an artifact, and it is the one that followed
the law-writing procedure most faithfully:

> "I deliberately did not grep or read the full `ls` name list, `names.json`, or
> the object store for intent keywords — every candidate below was surfaced by a
> `find` mode's own output."

It then tried NINETEEN signature shapes for intent 1 and never guessed
`(List Str) x (List Str) -> Str`. Both unguided subjects read the names and had
it immediately.

The page framed discovery as law-writing and never mentioned that the corpus has
a readable index. On this evidence that framing cost a find. `docs/discovery.md`
now leads with probing and enumeration and demotes the axes to a diagnostic —
a repair the falsifier produced, aimed at the document rather than the issue.

## What this does NOT establish

- **ANYTHING ABOUT THE SHIPPED PAGE AS IT NOW STANDS.** This is the sharpest
  limit and it got stronger, not weaker, as the work went on. The treatment
  subjects read the axes and the law-writing procedure. The page now LEADS with
  signature probing, `oath ls` and `oath dependents` — the three things the run
  identified as what actually worked, and all three were ABSENT from what the
  subjects were given. So no arm of this experiment tested the current page.
  Its repair is motivated by the run and unmeasured by it; testing it needs
  fresh intents, because these seven are now worked examples in the text.
- **That two readers are independent of each other.** They share a model and its
  priors, so agreement between them separates a systematic effect from a one-off
  and does not corroborate a judgment. What carries the verdict here is not
  their agreement: it is that each produced tool output naming the artifact,
  which is decidable from the artefact and settles itself.
- **That 5 of 7 generalizes, or that an empty result is USUALLY a shape
  problem.** Seven intents from one application's friction log, chosen BECAUSE
  they had artifacts to find — so "the corpus has nothing" was impossible here
  by construction, and that is precisely the case a real caller cannot exclude.
  A first draft of the documentation stated the general likelihood anyway; it
  now names the sample. This is the corpus-versus-phenomenon distinction, met
  in the one place where the sampling bias is built into the selection rule.
- **That the axes are exhaustive.** Three axes were enough for these three
  failures. Nothing here samples for a fourth, so exhausting them does not
  establish that a corpus has no artifact — and the documentation says so
  rather than presenting them as a closed search.
