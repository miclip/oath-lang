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
