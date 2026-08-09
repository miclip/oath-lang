# #163 — re-verifying that SPEC §4's `Str` generation rule is unimplemented

**What this file is:** the evidence for #163, re-derived on current `main`, and
nothing concluded from it. It re-runs the issue's claim against the tree as it
stands, exhibits a generated `Str` through the kernel's own generation path, and
records the scoped searches that show neither kernel nor any fixture implements
the rule.

**It proposes no repair and edits no normative text.** `docs/SPEC.md` is
untouched by this work; what §4 should say instead is #163's second step and is
deliberately not taken here. No other normative rule was surveyed for the same
defect — the issue raises that as a wider question, and it stays open.

Run on 2026-08-09 against `0df0956cf80c1b206cdc7d61539c850c4e2956a6`, with
`go version go1.25.6 darwin/arm64`. Every `go test` invocation below was run
with `GOMAXPROCS=3`.

## The rule under test

`docs/SPEC.md` §4, under "Generation by type — draw order is normative"
(line 570):

```
- `Str`: length `below(size+1)`, then that many draws of `below(7)` into
  alphabet `"ab xyz!"` (bytes, in that order).
```

`"ab xyz!"` is the seven bytes `97 98 32 120 121 122 33` — `a`, `b`, space,
`x`, `y`, `z`, `!`.

## 1. The type kind the rule is written for is gone — and §4 now has two rules for `Str`

**A correction to the framing, made after review.** The first version of this
file claimed the rule is unreachable in principle and contradicts §1. That is
too strong, and the weaker version is the interesting one.

What IS decidable from the text. §1's binary encoding table (line 48) records
the primitive type as removed:

```
| 0x03 | *(reserved — was the `str` primitive type; strings are now the `Str` datatype)* |
```

and §3 (line 436) says "**Strings** are NOT primitive". The Go kernel agrees
mechanically — the tag exists as a constant and is referenced by nothing:

```
$ grep -rn 'tagTyStr' oath/
oath/canon.go:49:	tagTyStr    = 0x03
```

One declaration, no encoder use, no decoder arm, so `0x03` is neither emitted
nor accepted and a `Ty{K: "str"}` cannot be decoded out of any object. **A
generator therefore cannot dispatch §4's `Str` rule on a primitive type kind,
because there is no such kind.**

**But that does not make the rule impossible to honour**, and saying so would
overstate it. `Str` still exists as an ordinary datatype with a determined
identity, so a kernel could implement §4's rule by recognising that datatype's
hash — which is exactly what this file's own witness does in order to *find*
`Str` values. Neither kernel does. So §2–§4 below establish a DIVERGENCE
between the specification and both kernels, not an impossibility.

**The sharper defect is inside §4 alone.** §3 makes `Str` an ordinary datatype,
so §4's `Data` rule — "if size ≤ 0, choose uniformly among constructors with no
recursive field … else uniformly among all constructors … fields generated
left-to-right at size−1" — applies to `Str` values as much as the `Str` rule
does. **Two normative rules in one list prescribe different draws for the same
value, and §4 says nothing about which wins.** That is decidable from the
artefact: both rules are in the same paragraph of `docs/SPEC.md` §4, and no
measurement is needed to see it. A kernel written from the specification today
must pick one, and two kernels may pick differently while both claim conformance.

That the `Str` rule was *written for* the removed primitive is an inference, not
a text fact — but a well-supported one: it draws "into alphabet `"ab xyz!"`
(bytes, in that order)", which describes a string of bytes rather than a spine of
`SCons`-carried Unicode scalar values, and #59 is what turned the latter into the
representation.

A vestige survives in the Go pretty-printer (`oath/pretty.go:61`, `printTy`'s
`case "str": return "Str"`) and is dead for the same reason: nothing constructs
that type. It is a printer arm, not an implementation of the generation rule,
and it is noted here so that a later `grep` for `"str"` in `oath/` is not
mistaken for one.

## 2. Neither generator has a `Str` arm

The Go generator dispatches on seven type kinds, none of them `str`:

```
$ grep -n '^	case "' oath/gen.go
53:	case "int":
64:	case "rat":
70:	case "float":
81:	case "bool":
83:	case "record":
93:	case "fun":
126:	case "data":
```

The Rust generator dispatches on the same set (`oathrs/src/gen.rs`, `Ty::Int`,
`Ty::Bool`, `Ty::Rat`, `Ty::Float`, `Ty::Fun`, `Ty::Record`, `Ty::Data`, with
`Ty::Rec`/`Ty::Var` erroring). `Str` therefore takes the `data` arm in both, and
its codepoints come from the `int` arm — which has two branches, both far from
`"ab xyz!"`: one draw in four takes the boundary table `[-2,-1,0,1,2]`, and the
rest take `intIn(-20, 20)` (`oath/gen.go:59-63`, and §4's own `Int` rule).

## 3. The alphabet literal appears nowhere in either kernel or the fixtures

The issue's own search, re-run, and scoped to exclude the witness added by this
work (which necessarily contains the string, because it quotes the rule):

```
$ grep -rn 'ab xyz' oath/ oathrs/ fixtures/ | grep -v gen_str_spec_alphabet_test.go
$ echo $?
1
```

No matches. The rule's `below(7)` draw likewise appears nowhere in either
kernel, under the same exclusion and for the same reason:

```
$ grep -rn 'below(7)' oath/*.go oathrs/src/*.rs | grep -v gen_str_spec_alphabet_test.go
$ echo $?
1
```

**No fixture exercises the rule either.** `fixtures/verify/*.txt` is the only
fixture family that pins a *generated* input — it records counterexamples for
falsified properties — and there are three in the whole corpus, none of them a
`Str`:

```
$ grep -rn 'counterexample' fixtures/verify/
fixtures/verify/f-tenths.txt:2:    counterexample:
fixtures/verify/f-scale-inv.txt:2:    counterexample: -1.6666666666666667f
fixtures/verify/bad-reverse.txt:3:    counterexample: (Cons -1 Nil), (Cons -8 (Cons 2 (Cons 15 Nil)))

$ grep -rn 'counterexample.*SCons' fixtures/verify/
$ echo $?
1
```

(The two `SCons` chains that do appear under `fixtures/` are in
`fixtures/prove/scripts/full-name-0.smt2` and `greet-0.smt2`. Those are string
*literals* from the properties themselves, elaborated by the string-literal
sugar in `oath/surface.go:490`; they are not generated values.)

## 4. The exhibit: a generated `Str` outside the declared alphabet

`oath/gen_str_spec_alphabet_test.go` draws `Str` values through `genPropCase` —
the same function `runProp` binds its cases from, so no schedule is reproduced
that could drift from the one the kernel runs — and scores every emitted
codepoint against `"ab xyz!"`.

**It has two arms, because the claim's universe and a stable ratchet are not the
same set**, and the distinction matters when reading the numbers below:

- **ARM A is the real population.** Every `Str` binder of every property of
  every live definition in the committed store, each drawn at its own
  definition's seed base and its own property index. This is the set the claim
  quantifies over: the `Str` values verification actually binds. It is reported
  in **two windows** — the REAL window is `propCases` (200), exactly what
  `runProp` runs, and is the only figure that may be described as what
  verification binds; the EXTENDED window continues the same seed path to 2000
  cases, because a zero over 200 cases is consistent with a rare event and a
  zero over 2000 is not. Arm A's size moves with the corpus, so it carries no
  equality-pinned totals.
- **ARM B is the pinned instrument.** One synthetic single-`Str`-binder property
  at seed base 0. **Its stream is synthetic** — no committed property samples
  it — and that is the point: the population is fixed, so every figure can be
  pinned by equality and any change to the generator is loud.

```
$ cd oath && GOMAXPROCS=3 go test -run 'TestSpecStrAlphabetIsUnreachedByTheGenerator' -v
=== RUN   TestSpecStrAlphabetIsUnreachedByTheGenerator

=== SPEC §4 Str alphabet "ab xyz!" vs the generator ===
ARM A — real population: 83 live properties carrying 126 (property, Str binder) positions,
        each drawn at its own definition's seed base and property index.
        REAL window = 200 cases (what runProp runs); EXTENDED window = 2000 cases (same seed path).
  EXHIBIT 1: bytes-str prop 1 binder 0, case c=5 (size 5) -> Str codepoints [-15 1 -1]
             codepoints in SPEC §4's alphabet "ab xyz!": [] (want none)
  EXHIBIT 2: bytes-str prop 1 binder 0, case c=6 (size 6) -> Str codepoints [1 -13 18]
             codepoints in SPEC §4's alphabet "ab xyz!": [] (want none)
  EXHIBIT 3: bytes-str prop 1 binder 0, case c=7 (size 7) -> Str codepoints [0]
             codepoints in SPEC §4's alphabet "ab xyz!": [] (want none)
  ARM A (real, 200 cases/prop): 25200 values drawn   non-empty: 11161   codepoints emitted: 19004   distinct: 41   observed range: [-20, 20]
    occurrences of each codepoint SPEC §4 declares:  'a'=0  'b'=0  ' '=0  'x'=0  'y'=0  'z'=0  '!'=0
    VERDICT: the generated alphabet and SPEC §4's declared alphabet are DISJOINT
  ARM A (extended, 2000 cases/prop): 252000 values drawn   non-empty: 110031   codepoints emitted: 188454   distinct: 41   observed range: [-20, 20]
    occurrences of each codepoint SPEC §4 declares:  'a'=0  'b'=0  ' '=0  'x'=0  'y'=0  'z'=0  '!'=0
    VERDICT: the generated alphabet and SPEC §4's declared alphabet are DISJOINT
ARM B — synthetic single-Str-binder property, seed base 0, size schedule c%8
  ARM B: 200000 values drawn   non-empty: 87186   codepoints emitted: 149449   distinct: 41   observed range: [-20, 20]
    occurrences of each codepoint SPEC §4 declares:  'a'=0  'b'=0  ' '=0  'x'=0  'y'=0  'z'=0  '!'=0
    VERDICT: the generated alphabet and SPEC §4's declared alphabet are DISJOINT

--- PASS: TestSpecStrAlphabetIsUnreachedByTheGenerator (0.22s)
PASS
ok  	oath	0.563s
```

The first exhibit is worth reading as a value and not only as a statistic, and
it is drawn from inside the real window rather than from a counterfactual case.
`bytes-str`'s second property binds a `Str`, and at case 5 — one of the 200
cases verification runs for that property — the tester generated the
three-codepoint value `[-15, 1, -1]`. None of the three is in `"ab xyz!"`;
**`-15` and `-1` are not Unicode scalar values at all**, so they are outside
anything §3 says a `Str` denotes, let alone outside §4's declared alphabet.

All three windows observe exactly the same alphabet — 41 distinct codepoints
spanning `[-20, 20]` — and every member of `"ab xyz!"` is ≥ 32, which is why the
seven zeros are structural rather than lucky.

The corroborating measurement one layer down, already committed as the #161
artefact, reports the same alphabet from the other direction. It is a synthetic
single-binder sweep like arm B, and its figures describe that stream:

```
$ cd oath && GOMAXPROCS=3 go test -run 'TestStrGeneratorCodepointAlphabet' -v
=== Str codepoint alphabet, 200000 draws, size schedule c%8 ===
non-empty strings: 87186/200000   codepoints emitted: 149449   distinct: 41   range: [-20, 20]
codepoint 61 ('='): 0 occurrences
codepoint 10 ('\n'): 2708 occurrences
codepoint 44 (','): 0 occurrences
alphabet: -20 -19 -18 -17 -16 -15 -14 -13 -12 -11 -10 -9 -8 -7 -6 -5 -4 -3 -2 -1 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20
occurrences of NEGATIVE codepoints (outside any scalar range): 69561
```

### The instrument's controls

A disjointness verdict from a scan that can never report a hit is not evidence,
so the witness asserts before it measures. The membership predicate accepts all
seven declared codepoints and rejects `61`. The `Str`-spine scan reports
`[97 98]` for a hand-built `"a=b"`, nothing for `[1 2 3]`, and rejects a
non-`Str` value rather than scoring it zero.

**And it identifies `Str` by IDENTITY, not by shape.** `(List Int)` has exactly
the nil/cons shape `Str` has, so a walker checking constructor arities alone
accepts it — and the "a binder that stopped being `Str` fails loudly" control it
promises would then be vacuous. The scan checks the committed `Str` datatype's
hash and constructor indices at every node, and a `(List Int)` value built from
the store's own `Nil`/`Cons` is asserted to be *rejected*. Review found this: the
first version of the witness reused a shape-only walker.

Arm A adds the controls its corpus-dependent population needs. Its enumeration
walks every live name and treats an unreadable definition as FATAL rather than
skipping it, so the population cannot silently shrink while the verdict still
reads as complete. Aliased names resolving to one object are counted once, since
the seed base is a fact about the object. And the run fails unless at least one
site, one drawn value, one emitted codepoint and one non-empty `Str` were
produced inside the real window — without which a zero could be a fact about the
corpus or about string length rather than about the alphabet.

### The revert-check

The zeros were confirmed to be a property of the generator and not of the
counter, by mutating the generator in throwaway `git worktree`s — `oath/gen.go`'s
int arm rewritten, nothing else changed — and re-running the same test.

**Widened**, `r.intIn(-20, 20)` to `r.intIn(-20, 127)`. Every window reports the
declared codepoints, and the real window reports them too — the mutation is
visible in the 200 cases verification actually runs, not only in the extended
stream:

```
  ARM A (real, 200 cases/prop): 25200 values drawn   ... distinct: 148   observed range: [-20, 127]
  ARM A (extended, 2000 cases/prop): 252000 values drawn   ... distinct: 148   observed range: [-20, 127]
  ARM B: 200000 values drawn   ... distinct: 148   observed range: [-20, 127]
    RECORDED MEASUREMENT IS STALE (ARM A real) — codepoint 97 ('a') ... now occurs 95 times over 25200 draws, where this file records 0.
    ... (one line per declared codepoint, in all three windows) ...
    RECORDED MEASUREMENT IS STALE (ARM A) — observed codepoints in [-20, 127], outside the recorded [-20, 20].
FAIL
```

**Narrowed**, `r.intIn(-20, 20)` to `r.intIn(-10, 20)`. This is the direction
review pointed out: a narrowing leaves every declared codepoint at zero, so a
one-sided bound would pass while the recorded figures went stale. Arm B's
equality pin is what catches it, and arm A's containment bound correctly does
not fire — that is the division of labour between the arms, not a gap:

```
  ARM A (real, 200 cases/prop): 25200 values drawn   ... distinct: 31   observed range: [-10, 20]
  ARM A (extended, 2000 cases/prop): 252000 values drawn   ... distinct: 31   observed range: [-10, 20]
  ARM B: 200000 values drawn   ... distinct: 31   observed range: [-10, 20]
    RECORDED MEASUREMENT IS STALE (ARM B) — observed codepoints in [-10, 20] with 31 distinct,
    where this file records [-20, 20] with 41.
FAIL
```

The worktrees were removed; neither mutation touched the working tree. Note that
the *exhibits* still printed under both mutations — an exhibit cannot fail — and
it is the ratchet assertions that carry the verdict. Note too that the emission
totals are unchanged under both, which is why arm B pins them separately: they
record the *length* schedule, and neither mutation touched it.

## A SECOND OBSERVATION, RECORDED NOT CHASED: generated `Str` values carry non-scalar codepoints

**Recorded because it was asked for, and CORRECTED after review, because the
first version of this section called it a defect and §3 says it is not.** The
correction is the useful part; the observation stands either way.

The witness is the exhibit above: `bytes-str` prop 1 binder 0, case 5 — one of
the 200 cases verification runs for that property — binds the `Str`
`[-15, 1, -1]`. Neither `-15` nor `-1` is a Unicode scalar value. It is not
rare: in the synthetic 200 000-draw sweep, **69 561 of 149 449 emitted
codepoints are negative** (46.5%), reported by the #161 artefact's own alphabet
test.

**What SPEC §3 actually says about that, quoted rather than paraphrased:**

```
CONSTRUCTION IS UNCHECKED, and a KERNEL MUST NOT reject a non-scalar element
at construction. `(SCons -1 (SNil))` is an ordinary value of the semantics in
this section.
```

§3 uses `(SCons -1 (SNil))` as its own example. The scalar range is "a property
of the boundaries a `Str` CROSSES, not of the datatype", enforced by hosts at
ADMIT and PACK. So a generator emitting `-15` is **producing an ordinary value
of the language, not violating the specification**, and a generator that
special-cased `Str` to avoid it would be the incompatible change — exactly the
"rule attached to `SCons`" §3 rejects, since `Str`'s canonical form is shared by
every `(data T [] (A) (B Int T))`.

**What remains true and is worth someone's attention** is a calibration point,
not a defect: a property quantifying over `Str` is quantified over the whole
datatype, which §3 deliberately makes wider than text. Roughly half the
codepoints any `Str` property is tested on are values no ADMIT boundary could
ever produce. That says something about what `tested` means for text-handling
definitions — the same class of question as #161, and adjacent to #69, whose
refinement types are the recorded route from host discipline to identity.

It is filed here as an observation with its witness and its correct normative
reading. Whether it becomes an issue is the human's call, and it should be made
knowing §3 permits what was seen.

## What this establishes, and what it does not

**Established.** Neither kernel implements SPEC §4's `Str` rule, and no fixture
exercises it. §1 reserves the primitive type kind the rule was written for, so
no generator can dispatch it as a type kind — though a kernel could still honour
it by recognising the `Str` datatype's identity, which is why this is a
divergence and not an impossibility. Within §4 itself, the `Str` rule and the
`Data` rule prescribe different draws for the same values with no stated
precedence. Across the 126 `(property, Str binder)` positions in the
committed corpus, over the 200 cases per property that verification actually
runs — 25 200 generated values, 19 004 codepoints, drawn at each definition's
own verification seed — the generated codepoints are disjoint from the alphabet
§4 declares, spanning `[-20, 20]` against a declared alphabet whose smallest
member is 32. Continuing the same seed path to 2000 cases per property (252 000
values, 188 454 codepoints) changes nothing.

**Not established.** The measurement samples; it does not prove that no
execution anywhere could emit `a`. It does not say what §4 should say instead.
And it says nothing about whether any *other* normative rule is unreachable —
the issue asks that question and this work did not go looking.

Every figure here is about this corpus and this generator, and the arm-B figures
are about a synthetic stream no committed property samples. The ratchets in
`oath/gen_str_spec_alphabet_test.go` exist so that if any of them stops being
true, this file is re-read rather than left standing.

## What step 2 did with this

Step 2 resolved the precedence rather than deleting the rule. §4's `Str` entry
now states that the **`Data` rule governs**, delegates to it, spells out the
resulting draw sequence as a worked instance, and names the `Data` rule as
authoritative should the two ever disagree — so the redundancy is documentation
with a stated tie-break rather than a second source of truth. The reason for
choosing explicit precedence over deletion is in that commit and on #163.

`oath/gen_str_spec_order_test.go` is what makes the rewritten entry worth its
normativity: an implementation written from the new sentences alone reproduces
the kernel's generated value AND leaves the rng in the same state, over 44 000
generations spanning sizes −3 to 8. **Stated precisely, after review corrected an
overstatement:** the rng is splitmix64 over one word of state that advances by a
constant per call, so an equal final state establishes an equal DRAW COUNT, not
which primitive each draw used. Sweeping seeds constrains every modulus whose
outcome changes the value; what it cannot see is the modulus at a FORCED
selection — `below(1)` at size 0, where only the draw's EXISTENCE is observable.
That existence is precisely what §4's "single-candidate selection is not skipped"
clause requires, and it is what the test pins.

## The blind round on the changed text (step 3)

Step 2 rewrote normative text, so the blind rule applies — scoped to that text.
This section is the round's full record: what the subject was given, what the
harness can and cannot vouch for about its isolation, its unedited answer, and a
disposition for each finding.

### Context audit — THREE RUNS, AND THE FIRST ONE WAS CONTAMINATED

The round was run three times. **Recording all three is the point.** Run 1's
context audit was wrong in a way I asserted rather than checked; run 2 closed the
CONTEXT channel with an audit taken before the extract was read; run 3 used a
different model, because two readers sharing the author's model are one
measurement taken twice. Each run and its full transcript is below, in order.

**RUN 1 — a general-purpose subagent. CONTAMINATED; its result does not stand
alone.**

| channel | state |
| --- | --- |
| repository access | **CLOSED, harness-verified.** The dispatch returned `tool_uses: 0` — no file opened, no command run, no search. That is the harness's count, not the subject's own claim, which is why it outweighs the subject's closing sentence saying the same thing. |
| always-loaded guidance | **CONTAMINATED, issue-specifically.** A general-purpose subagent inherits `CLAUDE.md`, and `CLAUDE.md`'s queue names #163 and *describes the rule under test*: "SPEC §4 still specifies a primitive `Str` generator that was deleted when `Str` became inductive, asserting printable-ASCII coverage NEITHER kernel provides." I first wrote that this channel was merely "open" because `CLAUDE.md` points at normative text rather than paraphrasing it. That general rule holds; this entry is the exception, and I did not go and look. |
| model | **OPEN.** Same model as the author. It cannot witness an assumption the author and it both hold. Closed only by run 3, below. |

**What the priming could have reached, stated narrowly rather than waved away.**
It describes the OLD entry's alphabet claim and that no kernel implements it. It
does NOT describe the `Data`-rule delegation, the `below(1)`/`below(2)`
selection, the field order or the size clamp — the substance of parts 1–3. So it
bears most directly on question 3b, *"is a length drawn before the codepoints?"*,
which is exactly the answer the priming makes cheap.

**RUN 2 — the read-only search target, with a context audit asked FIRST.** Same
extract, same four questions, preceded by a question naming each forbidden
category. Its answer, verbatim:

> I do NOT have: (a) no project instructions file (no CLAUDE.md content anywhere
> in context — only the generic "init" skill description that mentions CLAUDE.md
> by name, not its contents); (b) no memory index or remembered conclusions;
> (c) no commit digest, branch state, or diff (only the bare fact "Is a directory
> a git repo: Yes" — no actual git data); (d) no handoff or session summary;
> (e) no GitHub issue number and no work queue.

Note the asymmetry in what backs each run: run 1's isolation is vouched for by a
harness tool-use count, run 2's by the subject's own enumeration of its context.
Run 2's is the more informative answer and the less independently verifiable one,
and neither closes the MODEL channel.

**RUN 2 REPRODUCED THE DERIVATION** — the same pseudocode, the same 1/4/7 draw
counts for the size-2 trace, and the same four answers. That agreement across a
primed and an unprimed reader, on material the priming does not describe, is the
durable result of this round.

**One qualification, which "exactly" would have papered over.** A prose aside in
run 2 says the `SCons` head costs "1 or 2 draws". Its own pseudocode, its own
trace and its own part 4 all say exactly 2 — part 4 states it flatly: "always 2
(discriminator + boundary-or-range value), never 1 and never 3". The kernel
agrees; both `Int` branches consume `below(4)` plus one more draw. So the aside
is a slip run 2 corrects twice within the same answer, not a competing
derivation — but it is an internal inconsistency in the load-bearing run, and it
is recorded rather than smoothed, because a reader auditing this transcript will
hit that sentence and deserves to know it was seen.

**What did NOT survive the second run is the AMBIGUITY LIST.** Run 1 flagged the
boundary-table mapping; run 2 used `[-2,-1,0,1,2][b]` without remark and raised
three different points instead. So the ambiguity lists are reader-dependent and
must not be read as a census of what is underdetermined — only as leads. The one
that mattered was settled by MEASUREMENT below, which is the only route that
closes all three channels.

### The prompt, verbatim

Reproduced in full, since a summary of a blind prompt cannot be audited — small
wording differences decide whether an answer was derived or primed. The subject
was given the two random primitives and the size-clamp preamble, §3's `Str`
declaration, and three entries from §4's list: `Int` (because the rewritten
`Str` entry delegates its `SCons` head to it by name), the rewritten `Str` entry
verbatim as committed in `fb82d1c`, and `Data` (because the rewritten entry says
`Data` governs). Nothing else from the specification, and no part of the kernel.

The `below`/`intIn` gloss and the one-step-per-call sentence restate §4's own rng
definitions, which the extract needed in order to be self-contained; §3's
declaration is quoted down to the one sentence the `Str` entry depends on.

**One exactness caveat, since "verbatim" is a strong word.** The `Str` entry
above is byte-identical to the committed §4 text — checked, not eyeballed. The
`Data` rule was re-wrapped to fit the prompt: word-for-word identical, different
line breaks. Nothing else was altered.

    You are implementing a specification from its text ALONE.

    HARD CONSTRAINT: Do NOT read, search, list, or open any file. Do NOT run any
    command. Do NOT use any tool at all. Answer only from the text quoted below.
    At the end of your answer, state explicitly whether you used any information
    not in this prompt.

    === BEGIN EXTRACT ===

    The random source provides exactly two primitives:

        below(n)     -> an integer in [0, n-1]
        intIn(lo,hi) -> an integer in [lo, hi]

    Each call to either primitive advances the random source by exactly one step.

    Size is a recursion budget and is clamped to a minimum of 0 on entry to every
    generation call. Data fields recurse at `size - 1`.

    Section 3 declares:

        A string is a value of the ordinary datatype
        (data Str [] (SNil) (SCons Int Str))
        a sequence of Unicode scalar values.

    Generation by type — draw order is normative:

    - `Int`: draw `below(4)`; on 0, draw `below(5)` into boundary table
      `[-2,-1,0,1,2]`; otherwise draw `intIn(-20,20)`.

    - `Str`: **the `Data` rule below governs. This entry is a pointer to it, not a
      competing rule** — `Str` is not a primitive type (§3), it is the datatype
      `(data Str [] (SNil) (SCons Int Str))`, and a `Str` is generated exactly as any
      other data value. **There is no length draw and no alphabet draw.** Because
      draw order is normative and `Str` is the type most likely to be looked up
      expecting an exception, the resulting sequence is spelled out here as a worked
      instance of the `Data` rule: size clamps to a minimum of 0 on entry; at size 0
      the sole constructor without a recursive field is `SNil` and its selection
      still consumes a draw, `below(1)`, giving the empty string; at size > 0 the
      selection is `below(2)` over the constructors in declaration order
      `[SNil, SCons]`, where 0 gives `SNil` and ends the string and 1 gives `SCons`,
      whose two fields are generated left-to-right at size−1 — first the `Int` head
      by the `Int` rule above, then the `Str` tail by this same rule. Every codepoint
      is therefore an `Int` draw. **Should this paragraph and the `Data` rule ever
      disagree, the `Data` rule is authoritative and this paragraph is the defect.**

    - Data: if size ≤ 0, choose uniformly among constructors with no recursive
      field (error if none); else uniformly among all constructors — and the
      selection ALWAYS consumes exactly one below(k) draw, including when k = 1
      (single-candidate selection is not skipped, in either size branch). Fields
      generated left-to-right at size−1.

    === END EXTRACT ===

    Produce, purely from that text:

    1. Pseudocode for `genStr(size, r)` returning the list of codepoints, using
       only `below` and `intIn`. Be exact about the ORDER and the COUNT of draws.
    2. The full draw trace for `genStr(size = 2, r)` as a decision tree: name each
       draw in order, what each outcome means, and where the generation stops.
       Cover every branch.
    3. Answer these four questions with a yes/no and one sentence:
       a. How many draws does `genStr(size = 0, r)` take?
       b. Is a length drawn before the codepoints?
       c. If the size-0 selection has only one possible outcome, is its draw skipped?
       d. For `SCons`, which field is generated first?
    4. State anything in the extract you found ambiguous, underdetermined, or that
       you had to guess at. Be specific and quote the sentence. If nothing was
       ambiguous, say so plainly — do not invent problems.

Question 4's last clause is deliberate. A reader asked to find problems finds
problems; the licence to say "nothing was ambiguous" is what makes the two
findings it did report worth reading.

### Run 1's response, unedited

> **1. Pseudocode for `genStr(size, r)`**
>
> Per the extract, `Str` is not a primitive — it's generated exactly by the `Data` rule for `(data Str [] (SNil) (SCons Int Str))`, with the worked-out draw counts given explicitly in the `Str` paragraph. `Int` is generated by the separate `Int` rule.
>
> ```
> genStr(size, r):
>     size = max(size, 0)                 # clamp on entry, per the general rule
>
>     if size <= 0:
>         c = below(1)                    # only SNil has no recursive field; k=1
>                                          # draw still happens (selection is never skipped)
>         # c == 0 always  ->  SNil
>         return []                       # empty string
>
>     else:  # size > 0
>         c = below(2)                    # over [SNil, SCons], declaration order
>         if c == 0:
>             return []                   # SNil, stop
>         else:  # c == 1, SCons
>             head = genInt(size - 1, r)  # Int field first
>             tail = genStr(size - 1, r)  # Str field second, same rule, recursively
>             return [head] + tail
>
> genInt(size, r):
>     size = max(size, 0)                 # clamp applies to every generation call,
>                                          # though the Int rule itself never reads size
>     k = below(4)
>     if k == 0:
>         b = below(5)
>         table = [-2, -1, 0, 1, 2]
>         return table[b]
>     else:                               # k in {1,2,3}
>         return intIn(-20, 20)
> ```
>
> Draw count for one `Int`: always exactly **2** draws (`below(4)` then either `below(5)` or `intIn(-20,20)`).
> Draw count for one `Str` constructor selection: always exactly **1** draw (`below(2)` if size>0, `below(1)` if size≤0).
>
> **2. Draw trace for `genStr(size = 2, r)`**
>
> ```
> draw 1: below(2)          [Str selection, size=2>0, over [SNil, SCons]]
> ├── 0 -> SNil. Result = "". STOP. (1 draw total)
> └── 1 -> SCons. Generate Int head, then Str tail at size=1.
>
>     draw 2: below(4)      [Int head, k-selector]
>     ├── 0 -> draw 3: below(5)      -> head = table[draw3] ∈ {-2,-1,0,1,2}
>     └── 1,2,3 -> draw 3: intIn(-20,20) -> head = draw3's value
>     (either way, exactly draws 2-3 consumed; proceed to tail)
>
>     draw 4: below(2)      [Str tail selection, size=1>0]
>     ├── 0 -> SNil. tail = "". Result = [head]. STOP. (4 draws total)
>     └── 1 -> SCons. Generate Int head2, then Str tail2 at size=0.
>
>         draw 5: below(4)  [Int head2, k-selector]
>         ├── 0 -> draw 6: below(5)      -> head2 = table[draw6]
>         └── 1,2,3 -> draw 6: intIn(-20,20) -> head2 = draw6's value
>         (exactly draws 5-6 consumed; proceed to tail2)
>
>         draw 7: below(1)  [Str tail2 selection, size=0<=0; only SNil qualifies]
>         └── 0 (only possible outcome, still drawn) -> SNil. tail2 = "".
>             Result = [head, head2]. STOP. (7 draws total)
> ```
>
> Every leaf is covered: the run stops at draw 1 (empty string, length 0), at draw 4 (length 1), or at draw 7 (length 2 — the maximum reachable at size 2, since size decrements by 1 per `SCons` and generation is forced to `SNil` once size ≤ 0). The value of draw 3 / draw 6 (which primitive fires, `below(5)` vs `intIn`) never changes the *count* or *position* of subsequent draws — only the resulting codepoint.
>
> **3. Four questions**
>
> **a. How many draws does `genStr(size = 0, r)` take?**
> 1 draw. At size ≤ 0 only `SNil` has no recursive field, so exactly one `below(1)` draw is made (always yielding 0), and generation stops immediately.
>
> **b. Is a length drawn before the codepoints?**
> No. There is no length draw anywhere in the rule; the text says explicitly "There is no length draw and no alphabet draw." Length emerges implicitly from however many times the per-level constructor-selection draw (`below(2)`, or `below(1)` at the base) comes up `SCons` before hitting `SNil` or running out of size.
>
> **c. If the size-0 selection has only one possible outcome, is its draw skipped?**
> No. The `Data` rule states the selection "ALWAYS consumes exactly one `below(k)` draw, including when k = 1 (single-candidate selection is not skipped, in either size branch)" — so `below(1)` is drawn even though its outcome is forced.
>
> **d. For `SCons`, which field is generated first?**
> The `Int` head field first, then the `Str` tail field. The `Str` paragraph states it directly: "whose two fields are generated left-to-right at size−1 — first the `Int` head by the `Int` rule above, then the `Str` tail by this same rule."
>
> **4. Ambiguities / underdetermined points**
>
> Two minor points, neither of which affects the draw *order* or *count* derived above, but which are not fully pinned down by the text:
>
> 1. **Which `below(5)` outcome maps to which boundary-table entry.** The text says: "on 0, draw `below(5)` into boundary table `[-2,-1,0,1,2]`." It never states the mapping formula explicitly (e.g., that outcome `i` selects `table[i]`); I inferred the natural in-order index mapping (`table[b]`) rather than finding it spelled out. This affects only which *value* is produced by draw 3/6, not the sequence of draws.
>
> 2. **Whether the "clamped to a minimum of 0 on entry to every generation call" rule has any effect on `Int` generation.** The general note says every generation call clamps size on entry, implying `genInt` also receives and clamps a size parameter — but the `Int` rule itself never mentions size at all, so it's unclear whether size is even passed to it or simply irrelevant/unused. I treated it as passed-but-unused, which doesn't change the draw sequence for `Str`, but the text doesn't explicitly confirm that `genInt` takes a size argument at all.
>
> Beyond those two, the `Str`-specific paragraph is unusually explicit (it exists precisely to preempt the "Str might have a length/alphabet draw" assumption) and directly states the draw at size 0, the draw at size>0, the constructor order, the field order, and that selection is never skipped — so the core derivation in parts 1–3 is not guesswork.
>
> I used no information beyond the extract quoted in the prompt; per the hard constraint, no files, searches, or tools were used to produce this answer.

### The primary result

Parts 1–3 are correct against the kernel — in BOTH runs, identically — and
`oath/gen_str_spec_order_test.go` is what says so rather than my reading of them:
its `genStrFromSpecText` is the same algorithm both subjects derived, and it
reproduces the kernel's value and draw count over 44 000 generations. The four
answers are right in both runs: one draw at size 0, no length draw, the forced
selection not skipped, the `Int` head first.

**The rewritten `Str` entry is derivable.** That is what the round was run to
find out.

### Run 2's prompt delta

Run 2 used the SAME extract and the SAME questions 1–4, byte for byte. Two
changes only: the hard constraint moved to the top, and a question 0 was
prepended, answered before the extract was read.

    HARD CONSTRAINT: Do NOT read, search, list, or open any file. Do NOT run any
    command. Do NOT use any tool at all. Answer only from the text quoted in this
    prompt. This is a controlled derivation exercise; using tools would
    invalidate it.

    QUESTION 0 — ANSWER THIS FIRST AND HONESTLY. Before you read anything below,
    what context were you given? Specifically, state whether your context
    contains any of: (a) a project instructions file (e.g. CLAUDE.md) or its
    contents; (b) a memory index or remembered conclusions; (c) a commit digest,
    branch state or diff; (d) a handoff or session summary; (e) any mention of a
    GitHub issue number or a work queue. Quote or describe anything you find. If
    you have none of these, say so plainly. Answer this before reading the rest —
    do not let the extract below influence it.

The five categories are `CLAUDE.md`'s own forbidden list for its startup
instrument, used here for the same reason: an issue-only check waves through a
digest that primes just as hard.

### Run 2's response, unedited except for two marked redactions

**The two redactions, stated before the transcript so nothing inside it is
authorial.** An operator email and a local filesystem path were replaced with
`<redacted>` placeholders where they appear in the context audit — neither is
evidence about isolation, and a committed document is the wrong place for
either. The redactions are marked inline. Everything else below is
character-for-character as returned.


> **QUESTION 0 — What context I was given**
>
> Before reading the extract, my actual context for this turn consists of:
>
> - A system-reminder with two fields: `userEmail` (`<redacted>`) and `currentDate` (2026-08-09), with a note that this context "may or may not be relevant" to the task.
> - A system-reminder listing deferred tools available via ToolSearch (names only, no schemas) — things like CronCreate, WebFetch, various mcp__oath-registry__* and mcp__oath__* tools, Gmail/Calendar/Drive tools, sinesync tools, browser-automation tools, etc.
> - A system-reminder listing available Skills (dataviz, artifact-design, code-review, simplify, init, security-review, etc.) with one-line descriptions.
> - The environment block naming this as a "file search specialist" persona for Claude Code, with a working directory `<redacted local path>`, noting it is a git repo, on macOS/zsh.
> - The user's prompt itself, containing the hard constraint and the extract.
>
> I do NOT have: (a) no project instructions file (no CLAUDE.md content anywhere in context — only the generic "init" skill description that mentions CLAUDE.md by name, not its contents); (b) no memory index or remembered conclusions; (c) no commit digest, branch state, or diff (only the bare fact "Is a directory a git repo: Yes" — no actual git data); (d) no handoff or session summary; (e) no GitHub issue number and no work queue.
>
> So: none of (a)–(e) are present. The only "extra" context is the userEmail/currentDate reminder and tool/skill name lists, none of which bear on the technical question below. Per the hard constraint, I will not use any tool and will answer purely from the quoted extract.
>
> ---
>
> **1. Pseudocode for `genStr(size, r)`**
>
> ```
> genStr(size, r):
>     size = max(size, 0)               # clamp on entry
>     if size <= 0:
>         c = below(1, r)               # 1 draw; only candidate is SNil (no recursive field)
>         # c is always 0 -> SNil
>         return []                     # empty string, 1 draw total
>     else:
>         c = below(2, r)               # 1 draw; selection over [SNil, SCons]
>         if c == 0:
>             return []                 # SNil, stops here
>         else:                         # c == 1, SCons
>             head = genInt(r)          # Int rule, at size-1 (Int draw count doesn't depend on size)
>             tail = genStr(size - 1, r) # Str tail, recursive call at size-1
>             return [head] + tail
>
> genInt(r):
>     d = below(4, r)                   # 1 draw
>     if d == 0:
>         b = below(5, r)               # 1 draw into boundary table
>         return [-2,-1,0,1,2][b]
>     else:
>         return intIn(-20, 20, r)      # 1 draw
> ```
>
> Draw order per `SCons` step: constructor-selection draw, then `Int` draw(s) for head (1 or 2 draws), then recurse into tail (which starts with its own constructor-selection draw). No length draw, no alphabet draw, ever.
>
> ---
>
> **2. Full draw trace for `genStr(size = 2, r)`**
>
> Entry: size = 2, clamped to 2 (already ≥ 0).
>
> - **D1: constructor selection at size 2** — size > 0, so `below(2)` over `[SNil, SCons]`.
>   - **D1 = 0 → SNil.** String ends immediately. Result: `[]`. (Total draws: 1.)
>   - **D1 = 1 → SCons.** Continue: generate Int head, then Str tail at size 1.
>     - **D2: Int discriminator** — `below(4)`.
>       - **D2 = 0 → boundary branch.**
>         - **D3: boundary index** — `below(5)`, selects from `[-2,-1,0,1,2]`. This fixes the head codepoint. (Draws so far: 3.)
>       - **D2 ∈ {1,2,3} → range branch.**
>         - **D3: `intIn(-20,20)`** fixes the head codepoint. (Draws so far: 3.)
>       - (Either way, exactly one head value is now fixed, using either 2 draws (D2, D3) if boundary, same count either way — 2 draws for the Int in both sub-branches: D2 always happens, then exactly one more draw (D3) happens whether it's the boundary-table draw or the intIn draw.)
>     - Now recurse: **genStr(size = 1, r)** for the tail.
>       - **D4: constructor selection at size 1** — size > 0, so `below(2)` over `[SNil, SCons]`.
>         - **D4 = 0 → SNil.** Tail ends. Result: `[head]`. (Total draws: 4.)
>         - **D4 = 1 → SCons.** Generate second Int head, then Str tail at size 0.
>           - **D5: Int discriminator** — `below(4)`.
>             - **D5 = 0 → boundary branch.**
>               - **D6: boundary index** — `below(5)` → second codepoint fixed. (Draws so far: 6.)
>             - **D5 ∈ {1,2,3} → range branch.**
>               - **D6: `intIn(-20,20)`** → second codepoint fixed. (Draws so far: 6.)
>           - Recurse: **genStr(size = 0, r)** for the final tail.
>             - **D7: constructor selection at size 0** — size ≤ 0, so choose uniformly among constructors with no recursive field. Only `SNil` qualifies, so this is `below(1)`.
>               - **D7 = 0 → SNil** (only possible outcome, but the draw is still consumed per the Data rule: "the selection ALWAYS consumes exactly one below(k) draw, including when k = 1"). String ends. Result: `[head1, head2]`. (Total draws: 7.)
>
> Every leaf of this tree terminates in `SNil`; there is no other way to stop. The branches, by total draw count, are:
> - 1 draw → `""` (empty)
> - 4 draws → 1 codepoint string
> - 7 draws → 2 codepoint string (the maximum reachable at size 2, since size decrements by 1 each `SCons` and generation must stop — via forced `SNil` — once size hits 0)
>
> Note: within each "Int" sub-step the draw count is always 2 (discriminator + boundary-or-range value), never 1 and never 3; I did not observe any text allowing the Int rule to short-circuit.
>
> ---
>
> **3. Short answers**
>
> a. **How many draws does `genStr(size = 0, r)` take?** 1 draw. At size ≤ 0 only `SNil` (no recursive field) qualifies, and per the Data rule "the selection ALWAYS consumes exactly one below(k) draw, including when k = 1" — so the single `below(1)` draw still happens even though its outcome is forced.
>
> b. **Is a length drawn before the codepoints?** No — the extract states explicitly "There is no length draw and no alphabet draw"; length is an emergent property of how many times the `SCons` branch is chosen before `SNil` is chosen, not something drawn directly.
>
> c. **If the size-0 selection has only one possible outcome, is its draw skipped?** No — the extract is explicit: "the selection ALWAYS consumes exactly one below(k) draw, including when k = 1 (single-candidate selection is not skipped, in either size branch)."
>
> d. **For `SCons`, which field is generated first?** The `Int` head is generated first, then the `Str` tail — "whose two fields are generated left-to-right at size−1 — first the `Int` head by the `Int` rule above, then the `Str` tail by this same rule."
>
> ---
>
> **4. Ambiguities / underdetermined points / things I had to guess at**
>
> - **The extract gives two Int-draw counts but doesn't explicitly total them.** The `Int` rule says "draw `below(4)`; on 0, draw `below(5)`... otherwise draw `intIn(-20,20)`." This clearly means always 1 draw (the `below(4)`) plus always exactly 1 more draw (either the `below(5)` or the `intIn` — these are described as alternatives, not additions), for a total of exactly 2 draws per Int. I'm confident in this reading, but the text never states "2 draws total" as a single explicit number — I derived it by reading "on 0, draw X; otherwise, draw Y" as mutually exclusive single follow-up draws. This is a minor inferential step rather than a directly quoted fact.
>
> - **"below(1)" mechanics are asserted, not derived.** The text asserts as a conclusion ("still consumes a draw, `below(1)`") that size-0 selection is a `below(1)` call, which requires reading "choose uniformly among constructors with no recursive field" plus "the selection ALWAYS consumes exactly one below(k) draw" together — i.e., k here is the count of eligible (non-recursive-field) constructors, which is 1 for `Str`. The extract does the algebra for us in this specific case, so it's not really ambiguous for `Str`, but the general Data rule doesn't spell out that "k" always means "size of the eligible constructor set" as opposed to some fixed constant — I inferred that from context rather than finding it stated as a general definition.
>
> - **Whether "size" clamping happens on the *recursive call* or is re-clamped each time.** The general preamble says "Size is a recursion budget and is clamped to a minimum of 0 on entry to every generation call." I applied this per call (i.e., `genStr(size-1, ...)` clamps `size-1` again on entry), which matters if size could go negative — but since size decrements by exactly 1 and the base case (size ≤ 0) triggers before any negative value could be produced from a positive size, this clamping is in practice inert for `Str`. Not a real ambiguity, just something I had to reason through rather than being told directly.
>
> Beyond these two minor inferential steps (both of which the extract supports unambiguously once the two relevant sentences are combined), I found nothing else genuinely ambiguous or underdetermined in the extract — the draw order, draw counts, termination condition, and the authority of the Data rule over the prose paragraph are all stated explicitly.

**Where the two runs differ, and where they do not.** The pseudocode, the 1/4/7
draw counts and all four answers are the same. Run 2 additionally observes that
every leaf terminates in `SNil` and that the `Int` sub-step is always exactly two
draws — both correct, neither asked for; the second of those is also what
contradicts its own "1 or 2 draws" aside, dispositioned in the context audit
above. The ambiguity lists share nothing.

### RUN 3 — a DIFFERENT MODEL, because two correlated readers are one measurement taken twice

Runs 1 and 2 share the author's model, so their agreement cannot witness an
assumption all three of us hold. Review pressed exactly this point. Run 3 was
therefore dispatched to **Claude Haiku 4.5** — a different model from the author's
— on the read-only target, same extract, same context audit, with question 3
extended by one item: *exactly how many draws does one `Int` cost?*, aimed at the
one place run 2 contradicted itself.

Its context audit:

> I was given none of: (a) a project instructions file or its contents, (b) a
> memory index or remembered conclusions, (c) a commit digest, branch state, or
> diff, (d) a handoff or session summary, (e) any GitHub issue number or work
> queue. I have only the extract of text you provided.

Its derivation, unedited:

> ```
> genStr(size, r):
>   size = max(size, 0)
>
>   if size == 0:
>     r.below(1)                    // Draw: constructor selection (SNil only)
>     return []
>   else:
>     constructor = r.below(2)      // Draw: constructor selection (SNil or SCons)
>
>     if constructor == 0:          // SNil
>       return []
>     else:                          // SCons
>       // Generate Int head
>       int_choice = r.below(4)      // Draw: Int case decision
>       if int_choice == 0:
>         idx = r.below(5)           // Draw: boundary index
>         head = [-2, -1, 0, 1, 2][idx]
>       else:
>         head = r.intIn(-20, 20)    // Draw: general Int value
>
>       // Generate Str tail recursively at size - 1
>       tail = genStr(size - 1, r)
>
>       return [head] ++ tail
> ```
>
> Each path through the pseudocode shows draw order left-to-right. Int generation always consumes exactly 2 draws (one to decide case, one for the value).
>
> **2. For `genStr(size = 2, r)`: possible draw counts and string lengths**
>
> | Total Draws | String Length | Path |
> |-------------|---------------|------|
> | 1           | 0             | First below(2) selects SNil |
> | 4           | 1             | SCons → Int (2 draws) → genStr(1) has below(2)→0 (1 draw) |
> | 7           | 2             | SCons → Int (2 draws) → genStr(1) has below(2)→1 → Int (2 draws) → genStr(0) below(1) (1 draw) |
>
> **3. Short answers**
>
> a. **1 draw.** At size 0, a single `below(1)` is drawn to select SNil.
>
> b. **No.** The Str generation draws for constructor selection and field generation only; there is no explicit length draw.
>
> c. **No.** The extract states: "the selection ALWAYS consumes exactly one below(k) draw, including when k = 1 (single-candidate selection is not skipped, in either size branch)."
>
> d. **The Int head.** The text says: "whose two fields are generated left-to-right at size−1 — first the `Int` head by the `Int` rule above, then the `Str` tail."
>
> e. **2 draws.** One `below(4)` to decide the case, then exactly one more draw (either `below(5)` or `intIn(-20,20)`).
>
> **4. Ambiguities**
>
> The extract is clear and well-specified. No ambiguities required guessing. The draw order, counts, and structure of all generation rules are explicit; the worked Str example removes any uncertainty about how the Data rule applies to this datatype.

**Three results from run 3.** The derivation and the 1/4/7 counts match runs 1
and 2. Its answer to 3e is 2 draws, unambiguously — settling run 2's internal
contradiction against run 2's own slip. And it reported NO ambiguities, having
been given the same explicit licence to say so.

**How much that closes, stated so a reader can discount it themselves.** Haiku
4.5 is a different model from the author's, which is what the MODEL channel asks
for; it is not a different vendor or a different training lineage, so the
correlation is REDUCED rather than eliminated. Three readers, two of them sharing
the author's model, one not, all reaching the same algorithm — that is the
evidence, and it is worth what it is worth.

### Run 2's findings, and what they say about the step 2 disposition

Run 2 raised three points instead of run 1's two, and none of them overlaps.
Quoted from its answer:

> - **The extract gives two Int-draw counts but doesn't explicitly total them.**
>   [...] I derived it by reading "on 0, draw X; otherwise, draw Y" as mutually
>   exclusive single follow-up draws. This is a minor inferential step rather
>   than a directly quoted fact.
> - **"below(1)" mechanics are asserted, not derived.** [...] the general Data
>   rule doesn't spell out that "k" always means "size of the eligible
>   constructor set" as opposed to some fixed constant — I inferred that from
>   context rather than finding it stated as a general definition. **The extract
>   does the algebra for us in this specific case, so it's not really ambiguous
>   for `Str`.**
> - **Whether "size" clamping happens on the recursive call or is re-clamped each
>   time.** [...] since size decrements by exactly 1 and the base case (size ≤ 0)
>   triggers before any negative value could be produced from a positive size,
>   this clamping is in practice inert for `Str`.

**Dispositioned in turn, and none blocks reproduction.**

The first is DERIVABLE and was derived — the subject reached exactly 2 draws per
`Int` and got it right; "not stated as a single number" is a readability note,
not an underdetermination. The third is NON-OBSERVABLE for `Str` by the
subject's own argument, which is correct: size decrements by one from a positive
value, so the clamp cannot fire on a `Str` recursion, and `oath/gen.go` clamps on
entry regardless.

**The second is the one worth keeping, because it argues for the step 2
disposition rather than against it.** The `Data` rule never defines `k` as the
cardinality of the eligible constructor set; a reader has to infer it. For `Str`
that inference is unnecessary — the rewritten entry states `below(1)` and
`below(2)` outright. **So the worked instance is not merely redundant: it is
load-bearing for exactly the reader the entry exists for**, and had step 2 chosen
DELETION, this reader would have had to derive `k` from an undefined term. That
is evidence the disposition was right, arriving from a reader who knew nothing
about the choice. The gap in the general `Data` rule is real, is not in the
changed text, and is recorded here rather than repaired.

### Disposition of run 1's finding 1 — the boundary-table index mapping

**MEASURED, CONFIRMED, AND OUT OF SCOPE — recorded rather than repaired.**

The finding is about §4's `Int` rule, which this change did not touch. That is
NOT why it is being left alone; predating the change decides nothing. It is left
alone because it does not block deriving the delegated `Str` sequence, and
because repairing it is a different edit from the one #163 authorised.

What the wording gives a reader: `below(n)` is defined as `next() mod n`, so
`below(5)` ranges over `[0,4]`; the table `[-2,-1,0,1,2]` has exactly five
entries in a written order. Index-by-position is a reading in which both the
modulus and the written order do work — but it is not the ONLY such reading
(`table[4-b]` uses both too), so the mapping is fixed by ORDINARY CONVENTION
rather than by any sentence. The subject named exactly that: it "inferred the
natural in-order index mapping rather than finding it spelled out".

**It is observable, and the size of that was measured rather than argued.** With
`gen.go`'s boundary indexing reversed to `boundary[len-1-below(len)]` in a
throwaway worktree, nothing else changed, a committed conformance transcript
moves:

```
committed   fixtures/verify/bad-reverse.txt
    counterexample: (Cons -1 Nil), (Cons -8 (Cons 2 (Cons 15 Nil)))
reversed    the same definition under the mutated kernel
    counterexample: (Cons 1 Nil), (Cons -8 (Cons -2 (Cons 15 Nil)))
```

`-1` becomes `1` and `2` becomes `-2`. So this is not a stylistic gap: the
mapping is on §10's conformance surface, and two kernels choosing differently
would diverge in bytes. Two Go tests also fail under the mutation, so the
committed evidence does discriminate.

**And it has already been survived by the thing that would have caught it.**
`oathrs` was built BLIND from `docs/SPEC.md` and reproduces all 191
`verify/*.txt` transcripts byte-identically, counterexamples included — verified
again at this commit. An independent implementer reading only the specification
did land on `table[b]`. So the accurate verdict is **underspecified in the text,
determined by convention, and empirically survived** — three different states,
and collapsing them into "the spec is wrong" or "the spec is fine" loses the
part that matters.

**Not repaired here.** It changes no `Str` draw and blocks no reproduction of
the delegated sequence, and #163's scope is §4's `Str` entry. It is written down
here, with its measurement, for a decision that is not this commit's to make.

### Disposition of run 1's finding 2 — whether `Int` generation takes a size argument

**NON-OBSERVABLE, THEREFORE NOT A DEFECT — and demonstrated, not asserted.**

The `Int` rule reads: draw `below(4)`; on 0, draw `below(5)` into the boundary
table; otherwise draw `intIn(-20,20)`. No branch, modulus, bound or draw count
mentions size. The clamp preamble constrains what a generation call does with a
size it USES; a rule that never reads size cannot behave differently for having
been handed one.

The demonstration is already in the repository and was not written for this
purpose, which is why it counts: `genIntFromSpecText` in
`oath/gen_str_spec_order_test.go` takes **no size parameter at all** —

```go
func genIntFromSpecText(r *rng) int64 {
```

— and the derivation built on it reproduces the kernel's value and draw count
over 44 000 generations across sizes −3 to 8. The kernel's `genValue` does pass
size to its `int` arm. Both readings, same bytes. The subject reached the same
conclusion by inspection ("passed-but-unused, which doesn't change the draw
sequence"); the test is what makes it a measurement instead of two readers
agreeing.

**No text change.** An unobservable distinction does not need adjudicating, and
adding a sentence to say so would put the author's resolved uncertainty into
normative text for every future reader to inherit.

### What step 3 changed in the specification

Nothing. All five findings across the two runs were dispositioned above and none
blocks reproduction of the delegated `Str` sequence:

| finding | run | verdict |
| --- | --- | --- |
| boundary-table index mapping | 1 | observable and MEASURED, gap in the adjacent `Int` rule, already derived correctly by an independent kernel — recorded, not repaired |
| does `Int` generation take a size argument | 1 | NON-OBSERVABLE; demonstrated by a size-free derivation reproducing the kernel |
| `Int`'s total draw count not stated as a number | 2 | DERIVABLE, and derived correctly |
| `k` undefined in the general `Data` rule | 2 | real gap in the `Data` rule; the rewritten `Str` entry states the two values outright, which is why it does not reach `Str` |
| re-clamping on the recursive call | 2 | NON-OBSERVABLE for `Str`; the clamp cannot fire on this recursion |

**The step 2 text stands as committed**, and one finding turned into evidence
for it: the reader who could not define `k` from the `Data` rule did not need to,
because the worked instance had already done that algebra.

### What is ESTABLISHED and what stays PROVISIONAL

The round supports two different claims and they are not worth the same. Keeping
them apart is the whole calibration:

**ESTABLISHED — decidable, and no reader's priors touch it — over a SAMPLED
population.** `gen_str_spec_order_test.go` implements the rewritten text and
reproduces the kernel's generated value and draw count on **every one of 44 000
generations: 4 000 seeds at each of 11 sizes (−3, −1, 0…8), covering every size
the case schedule reaches and both sides of the clamp**, with two
deliberately-wrong derivations as controls. That is settled by a command rather
than by anyone's reading — but it is AGREEMENT OVER THAT POPULATION, not a proof
of equivalence. A divergence at some unsampled seed or a larger size would
survive it. No structural argument for the general case is offered here, and
claiming one would repeat the mistake this whole record exists to correct.

**PROVISIONAL — the reader claim.** *An independent reader given only these
sentences arrives at that algorithm.* Three did, and it is still provisional,
for a reason worth naming exactly:

**The only HARNESS-VERIFIED isolation datum in this whole record belongs to the
CONTAMINATED run.** Run 1's `tool_uses: 0` came from the dispatcher. Runs 2 and
3 — the ones the conclusion rests on — closed CONTEXT by their own account of
their own context, and a subject's self-report is precisely the kind of evidence
this repository declines to treat as established elsewhere. Run 3's "I have only
the extract" is also loose on its face: it necessarily also received the hard
constraint and the questions.

MODEL is addressed by run 3 being a different model, and addressed is not
eliminated — same vendor, same lineage.

So: three readers, one derivation, no harness-side confirmation for the two that
matter. That is worth recording and not worth calling established. What would
upgrade it is a dispatch that reports the subject's full context from the harness
side rather than from the subject.

**NOT established, and not attempted.** That these sentences carry no assumption
the whole Claude family shares. No reader available here can witness that, and
saying otherwise would be the exact overreach step 1 was corrected for.
