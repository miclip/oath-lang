# #170 — the Int generator's range, and whether widening it would buy anything

**What this file is:** #170's own falsifier, run, plus the measurements it asked
for. The verdict is **NO GENERATOR CHANGE REQUIRED**, and the argument for it is
not that the blindness is harmless — it is measured and it is total — but that
no generation this project could adopt removes it.

**Corpus state.** Everything below was measured against the committed store at
`77c1e15`, kernel `oath-kernel/0.7`. Seeds derive from hashes and scores are
structure×seed facts, so the state has to be named: `codebase/names.json` carries
**210 live names resolving to 208 unique object hashes** (two aliased objects,
`Interval`/`Run` and `rot`/`rot-f`). Nothing here wrote to `codebase/` or
`fixtures/`; every run used a fresh copy of the store.

`6069d8a` and `77c1e15` differ by one line of `CLAUDE.md` and are the same corpus
for every purpose in this file.

## The verdict, stated before the evidence

#170 stated its own condition for declining:

> **Run this first.** If the properties that need class-B values can be
> discharged by explicit witnesses, and witness-driven checks are how this corpus
> already works in practice, then the generator's range is a limitation without a
> cost, and this should be declined.

Both halves hold, and are shown below. What #170 also said is that the argument
against declining — that a witness must be chosen by someone who already knows
the defect exists — is *an argument*, and that the falsifier exists to make it
face a measurement. It has now faced one, and the measurement does not rescue it:

**THE "STANDARD A / STANDARD B" VOCABULARY BELOW IS RETIRED IN `issue-169.md`,
AND THIS FILE'S CONCLUSION SURVIVES ITS RETIREMENT.** Those two readings were
that document's invention; it now applies #169's own sentence — *closed by a
property over the existing types* — and reports its per-row result and verdict
there. **Read them there rather than here: this file records no verdict of its
own about #169 and any restatement would go stale.** The two rows below are kept
because they are what THIS file measured, and both are about GENERATION, which
is a question about evidence rather than about closure — so the retirement
changes no number and no conclusion in this document.

- **Where a property that would have caught the actual mistake is enough**
  ("standard A" in the old vocabulary), explicit witnesses recover every known
  defect **for which an Oath-level property can be written at all** — every
  failure #169 cites except #167's, whose subject is a host diagnostic.
  They do it today, in four unrelated definitions, as ordinary practice.
- **Where that property must also be DISCHARGED so no wrong body can carry the
  name** ("standard B"), witnesses fail — and so does every finite generation, at any range.
  A body can special-case exactly the values a generator draws precisely as it
  can special-case exactly the values a person chose. Widening the draw raises the
  cost of cheating; it does not close the class.
- **And the generator cannot choose a regime.** Nothing in the type system
  distinguishes a codepoint from a number, so `intIn(-20, 20)` cannot know
  whether it is generating an index or a character. Any widening is therefore
  uniform, and uniform widening was already measured to trade detection away.

So the change is declined **not because the cost is zero, but because the
proposed instrument cannot buy the standard that would justify it.** Declining
accepts a real, named cost: a class-B defect nobody has thought of stays
undetected, and a witness list cannot find what nobody suspects. That cost is not
removed by this decision; it is recorded as the price of it.

**What this file does not do.** It does not settle what "closed" means for
#169; it measures what a generator change could and could not deliver. That
question is answered elsewhere — `issue-169.md` applies #169's own sentence over
five failure modes and reaches one categorical result. Read its exhaustive table
rather than this sentence.

## The falsifier, reproduced

`docs/experiments/issue-169.md` is a runnable document: every fenced `lisp` block
is a file, and putting them in order into a store copied fresh from `codebase/`
reproduces the run. `oath put` exits 2 on any file containing a falsified
definition.

**THE TABLE BELOW IS PINNED TO `77c1e15`, WHERE THAT DOCUMENT HELD TWELVE
BLOCKS.** It has since grown parts 9–12 and extracts **twenty-seven**, so the
replay script at the end of this file — which reads the CURRENT document — now
executes `block00` through `block26`, renumbers the blocks this table names, and
reports six more exit-2 blocks than it lists. That is not a divergence: it is a measurement of a different,
larger document. To reproduce exactly what is tabulated here, pin the source:

    git show 77c1e15:docs/experiments/issue-169.md > /tmp/issue-169-at-77c1e15.md

and point the extractor at that file. For the current document's own manifest and
exit codes, read its Reproduction section rather than this table.

Replayed at `77c1e15` against a fresh copy of `codebase/`:

| block | exit | what it is |
|---|---|---|
| `block00` | 0 | the named byte domain |
| `block01` | 0 | the instruments |
| `block02` | 0 | the secret-side guards |
| `block03` | **2** | the check matrix |
| `block04` | **2** | the generator calibration |
| `block05` | **2** | the witnesses |
| `block06` | 0 | the lambda-free re-spelling |
| `block07` | **2** | the checked decoder + mutant |
| `block08` | **2** | the two call-site variants |
| `block09` | **0** | **the witness-cheating decoder — accepted** |
| `block10` | 0 | the round-trip proof attempt |
| `block11` | **0** | **the cheating secret guard — accepted** |

Identical to the pattern that document records. The five exit-2 blocks contain
deliberately falsified definitions and are the controls: they show the harness
*can* refuse. `block09` and `block11` exiting 0 is the finding — two bodies that
satisfy every stated check while being wrong, admitted with the same
`tested · total` guarantee line as the correct versions.

That is the whole of what the falsifier establishes, **and one thing it
emphatically does not establish, which a first draft of this section claimed.**
These two recorded cheats are *not* immune to a wider generator. They are fixed
bodies that special-case exactly three witnessed strings and fall back to
`bytes-str`, so any unwitnessed class-B value kills them. Measured against the
replayed store:

```
(== (utf8-decode-cheat (utf8-encode "ж")) "ж")                     -> false
(== (utf8-decode-cheat (utf8-encode "©")) "©")                     -> false
(< (str-len (utf8-decode-cheat (utf8-encode "ж")))
   (length [Int] (utf8-encode "ж")))                               -> false
(== (utf8-decode-cheat (utf8-encode "ключ")) "ключ")               -> true   (control)
```

So a generator that reached 128..2047 would falsify `inverts-the-encoder` and
`multibyte-shrinks` on this body, and `issue-169.md` says so directly — *"a
domain-aware generator … would kill both cheats recorded here — that is real and
it is worth having."* **Widening is a real improvement against these artifacts.**

What the cheats demonstrate is therefore narrower, and the quantifier order is
the whole of it: **for any finite check set there exists a body that passes it**
— not *for any body there exists a finite check set that kills it*. These two
were written against a human's witness list and die to a generator; a body
written against the generator's draw dies to a human's witness. Neither direction
closes the class, and the argument in this file rests on the first quantifier,
not on these two artifacts surviving anything.

## The affected universe, derived from the claim

#170 scopes itself: *"the universe is every property whose domain includes values
the generator cannot draw, not the `Str` case that motivated it. Derive it from
the claim."*

The claim quantifies over property BINDERS whose generated value can transitively
contain an `Int` — because every `Int` in a generated value comes from one arm of
`genValue`, whatever wraps it. So the universe is not `Str` and `(List Int)`; it
is every binder reaching that arm through **records, ADT constructor fields, and
generated function tables** as well.

Two questions were asked of that population and kept apart. The observed half
goes through `genPropCase`, which the kernel names as the sole authority on the
tester's seed and size schedule, rather than reproducing the derivation.

| | |
|---|---|
| live objects with ≥1 property | **190** |
| properties | **506** |
| property binders | **849** |
| binders statically Int-reaching | **833** |
| …of which drew no `Int` in 200 cases | **0** |
| binders NOT Int-reaching | **16** — 13 `Rat`, 3 `Float` |
| `int`-arm draws across all binders, 200 cases each | **226,264** |
| observed `int`-arm range | **exactly `[-20, 20]`** |
| affine coefficients — a DIFFERENT draw, never summed with the above | 6,834, in `[-10, 10]` |

**98.1% of the corpus's property binders are Int-reaching, and every one of them
realized the reach in practice.** The sixteen exceptions are the entire non-`Int`
numeric surface: `Rat` and `Float` have their own generator arms.

*The last row is separated because a first version of this table added it in.*
The coefficients of a generated affine function are integers inside a generated
value, but `gen.go` draws them from `intIn(-3, 3)` and `intIn(-10, 10)`, not from
the `int` arm this issue is about. Summing them gave 233,098 — a figure that
would keep looking correct if the `int` arm's range changed underneath it, which
is the whole failure mode #170 exists to examine. They are reported because they
are evidence in the same direction (bounded, nowhere near 128) and kept apart
because they answer a different question.

The shapes carrying the reach are the point, since they are what "not only
`Str`/`List Int`" means concretely:

| count | binder type | how the `Int` arm is reached |
|---|---|---|
| 340 | `Int` | directly |
| 185 | `(List Int)` | ADT constructor field |
| 126 | `Str` | ADT — `(SCons Int Str)` |
| 21 | `Interval`/`Run` | ADT field |
| 15 | `Request` | ADT field |
| 15 | `(-> Int Bool)` | generated function **table keys** |
| 14 | `Tree` | ADT field |
| 10 | `{lt (-> Int (-> Int Bool))}` | **record** of function tables |
| 9 each | `(List Str)`, `(-> Str Str)`, `(-> Int (-> Int Int))`, `{eq …}` | ADT / table / record |
| 8 | `{fetch (-> Str Str)}` | capability record → table |
| 7 each | `Set`, `KV`, `Map`, `(List (Pair Str Str))`, `(-> Int Int)` | ADT / table |
| 5 each | `{emit …, env …}`, `{emit …, secret …}` | capability records |
| 4 | `(List (Pair Int Int))` | nested ADT |
| 3 each | `(List Interval)`, `(List (List Int))` | nested ADT |
| 2 each | `Queue`, `(-> Int (Option Int))`, `{env …, readfile …}`, `{first Str last Str}` | ADT / table / record |

The dictionary and capability-record binders matter most for scope: a
`{fetch (-> Str Str)}` is generated as a record of finite tables whose keys and
values are `Str`, hence `SCons Int` chains, hence the same `intIn(-20, 20)` draw.
**The blindness reaches the effectful-style tests as well as the arithmetic
ones**, which is not visible from the `Str` framing #170 was opened with.

*Reconciliation with the ledger, because the two figures differ and neither is
wrong.* `fixtures/prove/outcomes.json` records 191 definitions and 513
properties; the table above says 190 and 506. The ledger is **per-NAME** and the
measurement is **per-OBJECT**: `rot` and `rot-f` are one object with seven
properties, counted twice there and once here. 190 + 1 = 191 and 506 + 7 = 513.

## The scratch baseline

#170 requires that any generator change be judged by corpus movement — *"mutation
scores and survivor counts before and after… a generator change that improves one
class by losing another is not an improvement."* No change was made, so what
follows is the BEFORE, recorded so a future attempt has something to beat. It was
taken in a copied store; `codebase/` and `fixtures/` were untouched and
`oath fixtures` was not run.

Campaign identity: kernel `oath-kernel/0.7`, engine `mutants-1`, 60 cases,
500,000 fuel per case. **The per-object records are committed** as
`issue-170-mutation-baseline.csv` beside this file; the totals below are
summaries of it, and a before/after comparison should read the rows.

One `oath mutate` per unique live hash, all 208:

| class | objects |
|---|---|
| scored | **160** |
| func with properties, no mutation points in the body | 30 |
| func with no properties (`apply2`, `leak`, `spin-partial`, `stash`) | 4 |
| data declaration (not mutation-testable) | 14 |

**Aggregate 1796/3319 mutants killed = 54.11%.** Survivors **1523** — 1 waived
(`count-append`), 25 reached no verdict, 1497 unadjudicated.

> **READ THAT NUMBER AS THE GENERATOR'S REACH, NOT THE SPECIFICATIONS' STRENGTH.**
> This was `oath mutate` without `--prove`, so the 1497 survivors are
> UNADJUDICATED: nothing here distinguishes a mutant a proven property excludes
> (the campaign merely never drew the distinguishing input) from one no property
> excludes at all. 54.11% is therefore a statement about generated executions,
> and reading it as spec strength is the exact mislabelling this project already
> corrected once — see `CLAUDE.md` on a PROVEN definition scoring worst in the
> corpus. The figure is used below only to compare BEFORE and AFTER under one
> unchanged instrument, which is a use that survives the caveat; no claim about
> what the corpus's specifications exclude rests on it. 89 objects score
100%; 71 carry at least one survivor. Mean per-object score 77.60%; the gap
between that and 54.11% is the tail, where `no-field-can-inject` (0/147),
`hdr-probe` (3/322), `gh-request` (13/244) and `gh-record` (30/201) dominate the
denominator.

**Two facts about the RECORDED scores, which a future comparison must not read as
a baseline.** The scores in `codebase/meta/` are not this measurement:

- **14 objects are stale, all in the optimistic direction.** `fib` 17/17 recorded
  against 13/17 measured, and similarly `hex-encode`, `pow`, `range`,
  `rat-recover`, `replicate`, `reverse-onto`, `rle-expand`, `rot`, `rot-h2`,
  `rot-h3`, `rot-hl`, `show-nat`, `str-len`. In every case the delta is exactly
  that object's no-verdict count. `ec8ebf7` made property outcomes three-valued —
  fuel exhaustion stopped counting as a kill — and nothing re-scored the corpus
  afterwards, so the committed record still counts 25 mutants as killed that the
  harness never evaluated.
- **34 scored objects carry no recorded score at all**, most of the `apps/`
  corpus among them (`gh-record`, `gh-request`, `hdr-probe`, `check-config`,
  `json-scoped-string`, `no-field-can-inject`, the `config-*`/`mi-*`/`si-*`
  families). Recorded aggregate over the same 160 objects is 1389/1729 — roughly
  1,590 mutants of denominator missing.

Neither is repaired here. Both mean the same thing for #170: **a before/after
comparison must re-measure the before**, and reading `codebase/meta/` for it would
compare a generator change against a record taken under different scoring rules.

## Existing witness practice, which is the falsifier's first half

Class-B values enter this corpus by hand, and always have. A `prop` with an empty
binder list is a closed property — a literal witness with nothing generated.

- **`take`/`drop` boundary witnesses**, `examples/extras.oath:19,20,33,34,35`:
  `drop-zero`, `drop-one`, `take-zero`, `take-one`, `take-two`, each a fully
  literal list at n ∈ {0,1,2}. They sit beside the generated laws
  (`drop-all`, `drop-length`, `take-then-drop-rebuilds`) rather than replacing
  them. These exist for the same reason the boundary bias in the `Int` arm exists:
  the split-agent experiment found six `take`/`drop` mutants surviving because
  uniform draws seldom produce the distinguishing small n.
- **`bytes-ok` at 256**, `examples/http.oath:138` —
  `(prop rejects-oversized [(rest (List Int))] (not (bytes-ok (Cons [Int] 256 rest))))`,
  with `rejects-negative` at `:137` doing the same with `-1`. The generator cannot
  draw either bound; both are written down.
- **Hex witnesses through 255**, `examples/webhook.oath:100,103,109,161` —
  `decodes-ff`, `decodes-a-pair-of-pairs`, and `encodes-255`. 255 is the largest
  literal in any hex property, and it is always spelled in decimal.
- **#169's Cyrillic and Latin-1 closures**, parts 5 and 6 —
  `decodes-cyrillic` (`"ключ"`), `decodes-e-acute` (`"é"`) and
  `decodes-a-mixed-string` (`"a-é-ключ"`), stated as class-B witnesses. Those
  witnesses are what part 5's and part 6's closures rest on. They are also what
  `block09` and `block11` cheat against. (`issue-169.md` marks F3 CLOSED on
  part 6's `cyrillic-repo-round-trips`, and records the cost: any such property
  works only by encoding which reading of the bytes was intended, a fact the
  types do not carry. Read it there.)

The practice is narrow and it shows in the corpus profile: **literals ≥ 128
appear in exactly 8 definitions across 5 files**, and every one is an HTTP status
code, a byte-range boundary (255, 256), or the deliberately-wrong bound in
`undertested.oath`. The largest integer literal in any property in the corpus is
500, an HTTP status. Nothing carries a large arbitrary magnitude.

So the falsifier's first half is not merely satisfiable — it describes what this
corpus already does. That is the ground for declining.

## The judgment, made explicit

**Witnesses recover any KNOWN defect, and require prior knowledge.** A property
that would have caught what actually happened is writable for every failure #169
cites except #167's, whose subject is a host diagnostic rather than an Oath
value. **How far that gets, per row, is `issue-169.md`'s table and is not
restated here.** What follows is about witnesses and generation.
For F3 and F1 the closing evidence includes a hand-chosen class-B
literal, because the generator never enters that region. The generator
contributes nothing to those closures; crediting it would credit the tester with
exclusions a person made. The cost of this route is exactly its mechanism:
someone must already know the value that matters.

**Standard B — finite generation cannot discharge an infinite domain, at any
range.** `Str` codepoints are unbounded and `Int` is ℤ. Two hundred deterministic
cases per property sample a finite set, and for any finite set there is a body
that special-cases it. `block09` and `block11` are that construction aimed at a
human's witness list; the same construction aimed at the generator is if anything
easier, since the draw is a pure function of the definition's hash and therefore
knowable in advance. Widening `[-20, 20]` to anything changes which finite set is
sampled. It does not make the set infinite.
**Only proof reaches standard B.** When this was written, #169 recorded that the
two universal properties which would carry it had never been submitted to the
prover at all. **That is no longer true and the result went the other way:**
#169's parts 9 and 12 submit them, and several prove — `usable-encodes` by
induction in three seconds among them. **Proving them did not settle the question,
and #169's exhaustive table is the authority on where each row lands; it is not
restated here, because a duplicated verdict is what goes stale.** The reason
proof was not sufficient generalises, and is the part worth carrying: a store
policy gates a NAME, so an obligation reaches a failure only when the gated name
is where that failure enters — and `require_proven` reads only the properties the
SUBMITTED definition carries, so it does not preserve an obligation across a
repoint at all.

#169's part 12 measures both. Read that document rather than this summary.

**Read #169's part 12 for the condition, because the bare timings mislead.** The
gate consults only the properties the GUARDED NAME carries, so F2's 14-second
proof of the standalone `faithful-under-shipped-p` is not what gates anything —
the load-bearing evidence is `guard-ascii-both`, the guard carrying the
obligation on its own name, at 121 seconds, against a byte-range guard that
proves `usable-encodes` in 7 seconds and would otherwise bind while still
mismatching the digest.

Nothing in THIS file's argument changes — generation still cannot reach standard
B, for the reason above — but the sentence that proof had never been tried is
superseded.

**And without a type/domain distinction the generator cannot choose a regime.**
The `Int` arm serves array indices, loop counters, codepoints, bytes and HTTP
status codes with one draw, because the type system offers it one type. The
narrow range is *correct* for the first two — it is why the boundary bias exists
and why six `take`/`drop` mutants stopped surviving — and *blind* for the rest.
There is no information available at the draw site to tell them apart. This is
#169's finding one layer down: a byte/text distinction, if one were ever
introduced, would give the generator the discriminator it now lacks; #170 asks
what can be done without one, and the answer is **nothing uniform, because every
uniform choice is wrong for some regime.**

Which is why the measured outcome of widening is not a surprise. #162 implemented
`intIn(-128, 127)` and ran it across the corpus: **0 newly falsified definitions**,
2 newly indeterminate (both `fib`), no other verdict moved — and detection of the
two known `config.oath` defects went *down*, 51.4% → 24.4% and 49.4% → 45.2%,
because 256 values dilute each individual codepoint further than 148 do. Buying
that back with cases costs 16.44× the corpus's evaluations for 99% detection of a
0.14% event, and does nothing for a rarer one.

**Reach is not detection, and detection is not discharge.** Uniform widening buys
the first and loses the second. Only proof reaches the third.

## Where #162 sits — a bounded reach improvement, not a universal fix

#162's dependency-closure literal weighting is the one proposal that addresses
the regime problem rather than the range: instead of widening uniformly, weight
generated `Str` codepoints toward the literals appearing in the definition's
**canonical dependency closure**. The closure is the right source and the issue
says why — `collectDeps` and `sortedDepHashes` already compute it, it is already
normative because it determines the hash, and own-body-only does not work
(`config.oath` is the counterexample: the literal 61 lives in `config-key`'s body
while the false properties belong to `config-has-key` and `config-missing`, which
only call it).

**It is bounded by construction, and this file should not be read as an argument
for landing it.** Weighting toward literals a definition mentions reaches
literals a definition mentions. It would not have produced `256` for `bytes-ok`
(the body branches on `255`), it does not reach a Cyrillic codepoint that appears
nowhere in the closure, and it cannot help a definition that branches on no
literal at all. Against standard B it changes nothing, for the reason above: a
weighted finite sample is a finite sample.

It also remains **PARKED with a measurement as its trigger**, not a feeling:
land it only if a step-2 campaign shows survived→killed > 0. Over 3,319 mutants
it bought zero. The baseline in this file is measured over the same 3,319-mutant
population, which is what makes it the right control for that comparison — and
the merge caveat on the issue applies to any replay, since the runnable revision
carries a frozen `codebase/` and would otherwise re-measure the old corpus and
reproduce zero forever.

## What was measured, and what was not

**Measured.** The falsifier replay and its twelve exit codes. The static
Int-reachability of all 849 property binders and the 226,264 integers actually
drawn into them through the tester's own schedule. The mutation baseline over all
208 live objects. The staleness of the recorded scores. The witness-practice
citations and the corpus's above-127 literal profile.

**Not measured, and not to be inferred from the above.**

- **No domain-aware generator was built or run.** What WAS measured is weaker and
  is above: `utf8-decode-cheat` evaluates false on unwitnessed class-B inputs, so
  a generator reaching that region would kill it. That is a fact about this body,
  established by `oath eval` on four expressions — not a measurement of any
  generator, and not evidence about a body written to defeat one.
- **No class-B obligation was proven HERE, and that is now the only half of this
  bullet that stands.** `usable-encodes` was not submitted in this file, and when
  this was written it had not been submitted in #169 either. It since was:
  #169's part 9 proves it in 3 seconds, and `multibyte-shrinks` is the one that
  still returns no verdict. So "proof does not reach this" is established for
  `multibyte-shrinks` and REFUTED for `usable-encodes`; read #169 for the current
  state rather than this line.
- **What "closed" means is not settled by any of this, and is no longer open.**
  This file treated it as a judgment about what a `tested` verdict should mean.
  `issue-169.md` now settles it from #169's own text rather than by judgment, and
  reaches a categorical verdict; nothing measured HERE decides that question
  either way.
- **The 30 objects with no mutation points and the 4 with no properties were not
  investigated.** They sit outside the mutation baseline and inside the binder
  universe or neither, depending on which; nothing here characterises them.

## Reproduction

```sh
make build

# --- the falsifier replay ---------------------------------------------------
SP=$(mktemp -d); cp -R codebase "$SP/store"
python3 - "$SP" <<'EOF'
import sys, pathlib, re
v = pathlib.Path(sys.argv[1])
doc = pathlib.Path("docs/experiments/issue-169.md").read_text()
fence = chr(96) * 3
for i, b in enumerate(re.findall(fence + r"lisp\n(.*?)" + fence, doc, re.S)):
    (v / f"block{i:02d}.oath").write_text(b)
EOF
export OATH_STORE="$SP/store"
# UNSET, not merely overridden by OATH_STORE: when OATH_BACKEND=cloud, OpenStore
# IGNORES the path it was handed and opens the remote registry — so an inherited
# value would send these `put --new` calls at the live store while every line
# below still looked local.
unset OATH_BACKEND
for f in "$SP"/block*.oath; do
  ./oath/oath put "$f" --new >/dev/null 2>&1; rc=$?      # rc BEFORE any $( )
  echo "$(basename "$f") exit=$rc"
done
```

`$?` must be read into a variable before any command substitution runs, or the
`$(basename …)` resets it and every block reports 0. That mistake is recorded in
`issue-169.md` as having been made three times; it was avoided here by writing
`rc=$?` on the same line as the `put`.

### The mutation baseline

**The per-object records are committed, not just the aggregate**, as
`docs/experiments/issue-170-mutation-baseline.csv` — one row per unique live
hash, carrying the class, killed, total, survived, waived and no-verdict counts,
and the aliases sharing the object. A before/after comparison needs the rows, and
an aggregate cannot be decomposed back into them.

```sh
# One run per UNIQUE LIVE HASH, not per name: two names sharing an object share
# every mutant. Records are keyed by HASH — `Map` and `map` are two live names
# that collide on a case-insensitive filesystem, and keying by name silently
# overwrites one with the other, which inflated a first pass of this table.
SP=$(mktemp -d); cp -R codebase "$SP/store"
python3 - "$SP" <<'EOF'
import sys, json, collections, subprocess, os, re, csv
sp = sys.argv[1]
env = dict(os.environ, OATH_STORE=sp + "/store")
env.pop("OATH_BACKEND", None)   # cloud IGNORES OATH_STORE; mutate WRITES meta
names = json.load(open("codebase/names.json"))
by = collections.defaultdict(list)
for n, h in names.items():
    by[h].append(n)

SCORE = re.compile(r"generated mutation score: (\d+)/(\d+) mutants killed(.*)")
rows, cls = [], collections.Counter()
for h, ns in sorted(by.items(), key=lambda kv: sorted(kv[1])[0]):
    rep = sorted(ns)[0]
    p = subprocess.run(["./oath/oath", "mutate", rep],
                       capture_output=True, text=True, env=env)
    r = {"name": rep, "hash": h, "names": "|".join(sorted(ns)),
         "killed": "", "total": "", "survived": "", "waived": "", "no_verdict": ""}
    m = SCORE.search(p.stdout)
    if m:
        k, t, tail = int(m.group(1)), int(m.group(2)), m.group(3)
        w = re.search(r"\+(\d+) waived", tail)
        nv = re.search(r"\+(\d+) reached no verdict", tail)
        r.update(cls="scored", killed=k, total=t, survived=t - k,
                 waived=int(w.group(1)) if w else 0,
                 no_verdict=int(nv.group(1)) if nv else 0)
    elif "swears no properties" in p.stdout:
        r["cls"] = "no-props"
    elif "no mutation points" in p.stdout:
        r["cls"] = "no-mutation-points"
    elif "only function definitions can be mutation-tested" in p.stderr:
        r["cls"] = "not-a-func"
    else:
        # A run whose output matches nothing recognised is a BROKEN MEASUREMENT,
        # not a definition with no score. Skipping it silently is how an
        # aggregate stays plausible while the population quietly shrinks.
        sys.exit("UNRECOGNISED outcome for %s (exit %d):\n%s\n%s"
                 % (rep, p.returncode, p.stdout[-400:], p.stderr[-400:]))
    cls[r["cls"]] += 1
    rows.append(r)
if len(rows) != len(by):
    sys.exit("INCOMPLETE: %d rows for %d objects" % (len(rows), len(by)))

with open("docs/experiments/issue-170-mutation-baseline.csv", "w", newline="") as f:
    w = csv.DictWriter(f, ["name", "hash", "names", "cls", "killed", "total",
                           "survived", "waived", "no_verdict"])
    w.writeheader()
    for r in rows:
        w.writerow(r)

s = [r for r in rows if r["cls"] == "scored"]
K, T = sum(r["killed"] for r in s), sum(r["total"] for r in s)
print("unique live hashes:", len(by), dict(cls))
print("aggregate: %d/%d = %.4f" % (K, T, K / T))
print("survivors: %d (waived %d, no verdict %d)"
      % (T - K, sum(r["waived"] for r in s), sum(r["no_verdict"] for r in s)))
print("at 100%%: %d  with survivors: %d  mean per-object: %.4f"
      % (sum(1 for r in s if r["survived"] == 0),
         sum(1 for r in s if r["survived"] > 0),
         sum(r["killed"] / r["total"] for r in s) / len(s)))
EOF
```

The `UNRECOGNISED` branch is the control, and it was RUN rather than reasoned
about: pointed at an empty store the script stops on the first definition instead
of reporting a clean aggregate over nothing. A regex that silently skips what it
does not match cannot tell a broken run from a definition with no score.

`oath mutate` WRITES the score back into `meta/`, which is why both blocks copy
the store first. `oath fixtures` must not be run to produce these figures: it
writes proof verdicts back into `codebase/`, so it mutates the thing being
measured.

### The binder census

Committed as `oath/gen_int_reach_test.go`, because every headline figure in *the
affected universe* comes from it and a measurement whose instrument is described
rather than shipped is an assertion with numbers attached.

```sh
cd oath && go test -run TestIntReachCensus -count=1 -v
cd oath && INT_REACH_OUT=/tmp/binders.json go test -run TestIntReachCensus -count=1
```

It calls `genPropCase` — the kernel's sole authority on the tester's schedule —
rather than reproducing the seed derivation, since a reproduction yields a
population that looks like the tester's and stops being it the moment the
schedule changes. It hardcodes no counts: the figures are output, and the corpus
state that interprets them is named at the top of this file.

It does **not** supersede `oath/gen_str_reach_test.go`, which measures the same
generator through the same seam and answers a different question: how often
codepoint 61 reaches the `key`/`k` binder of two named properties, against a
threshold pre-registered before the first run (#161/#162). That is narrow and
pre-committed; this is corpus-wide and has no threshold. The overlap is the seam
and nothing else.

It asserts three things. Each control below was run by reverting the change in
place, watching the suite, and restoring — not by reading the code:

| claim | control | result |
|---|---|---|
| the static walker is SOUND — nothing draws an `Int` into a binder the walk called `Int`-free | make the `int` arm return `false` | FAILs, naming all 833 binders |
| the census is not empty | make the object filter reject everything | FAILs: *census measured nothing: 0 objects, 0 properties, 0 binders* |
| the affine coefficients are counted apart from the `int` arm | append them to the `arm` bucket instead | reports 233,098 / 0 — **and PASSES** |
| a draw outside `int64` is counted, not dropped | inject `2^200` alongside each `int`-arm value | range becomes `[-20, 2^200]`; draws double |
| every property can be generated at all | — | not mutated |

**Read row three as the limitation it is.** The affine split is a REPORTING
decision, not a protected invariant: folding the coefficients back reproduces the
old conflated total exactly (226,264 + 6,834 = 233,098) and no assertion notices.
Nothing here would catch that regression, and stating that is worth more than
bolting on a check that would only restate the split it is supposed to guard.
Row five is unmutated for a related reason: forcing a real generation failure
needs a binder the tester cannot generate, and the corpus has none — a
`t.Fatalf` inverted to fire on success would exercise the line without testing
the claim.

**Row four is the one that matters for a future widening, and it was a defect
until review found it.** `Int` is ℤ, so the values a widened generator could draw
are exactly the ones that need not fit `int64` — and the census originally
skipped any draw that did not, which would have left it reporting a serene
`[-20, 20]` over precisely the inputs it had been pointed at. Bounds are now
`*big.Int` end to end, serialised as decimal strings so the JSON does not
reintroduce the ceiling. **An instrument built to judge a range change must not
carry a range limit of its own**, and this one did.

The soundness assertion has already earned its place on a defect nobody planted.
Review asked for the type walk to be bounded against nested-recursive datatypes,
which is correct — keying the cycle check on the printed type does not terminate
on a declaration like `Bush a = … | Cons a (Bush (Bush a))`. The first attempt
replaced the key with a finite abstraction but only MARKED it visited, so
computing the argument flags for `(List (List Int))` consumed the key on the way
in and the outer type came back `Int`-free. The assertion failed immediately and
named eleven binders across `flatten`, `config-*` and `header-first`. Caching the
result rather than the visit fixes it, and every figure returned to its previous
value — which is the second half of the control.

The converse of the soundness assertion — statically reachable implies observed —
is REPORTED (0 misses today) and deliberately not asserted: a constructor
carrying an `Int` that is never selected would make it false without anything
being wrong.

## Refs

`oath/gen.go` (the `int` arm and its boundary-bias comment), `oath/verify.go`
(`genPropCase`, `propCases = 200`), `docs/experiments/issue-169.md` (the
falsifier and the A/B/C standards), 169, 162, 161, `ec8ebf7` (three-valued
property outcomes), `fixtures/prove/outcomes.json` (the per-name ledger).
