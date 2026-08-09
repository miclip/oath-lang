# #159 — can a refinement discipline reach the residue?

**What this file is:** the record of an evaluation of #69's refinement types
against #159, run in steps, with each step committed before the next begins.

**STEPS 1, 2 AND 4 ARE PRESENT.** There is no step 3; the numbering is the
session's plan and nothing is missing between them.

- **Step 1** states the claim under evaluation and derives the universe it
  quantifies over — the population `U`, and on it the two obligations `R` and
  `S`. It evaluates no mechanism.
- **Step 2** evaluates ONE construction against `R` and `S`: refinement types
  whose base value remains the original `(List Int)`. It reaches a decidable
  result for that construction and deliberately does not generalise past it.
- **Step 4** names the CLASS of mechanism that step 2's argument does not
  reach. It designs nothing, measures nothing, and chooses nothing.

No step reaches a verdict on #159 or on #69 as a whole.

Run on 2026-08-09 against `fc3f8f6`, with `oath/oath` rebuilt from that tree.
`oath eval` resolves names out of `codebase/` without writing to it. Step 2's
three supporting definitions were put and proved in a THROWAWAY COPY of the
store, never in `codebase/` — nothing was put, proved or waived in the canonical
store, and `git status codebase/` was clean before and after.

## The claim under evaluation

> **#69's refinement types close #159** — a refinement discipline distinguishes
> the CODEPOINT reading of a `(List Int)` from the OCTET reading everywhere the
> webhook residue confuses them.

That is the sentence later steps have to make true or false. It is stated here
so the universe can be derived FROM IT rather than from #69's design space or
from the kernel's decomposition, per this repo's rule that a witness derives its
universe from the claim.

## What is already established, and not re-derived here

`docs/experiments/issue-159.md` is the measured record; the #159 comment of
2026-08-08 (commit `60e9074`) summarises it. Three of its results are load-
bearing below and are cited rather than repeated:

- **`Str` and `(List Int)` are ALREADY distinct types**, in both directions,
  with well-typed controls. The conflation is not between them — it is INSIDE
  `(List Int)`, one level lower (§"`(List Int)` and `Str` are ALREADY distinct
  types").
- **`str-bytes` performs no encoding.** It is the identity on the codepoint
  spine (§"The crypto measurement" / §"`ключ` versus Latin-1").
- **A monomorphic `Bytes` datatype puts to `Str`'s hash, byte for byte**, from
  two independent stores (§"Result — the two printed hashes"). Cited as a
  constraint on what a mechanism may assume; not evaluated here.

The origin measurement is `docs/experiments/webhook-friction.md` entry 3. #133
records why `Str` has no identity of its own.

## Why the universe is not pairs of encodings

A first derivation of this file made the universe

    { (C(s), O(s)) : s : Str }        C(s) = str-bytes s, O(s) = the UTF-8 octets

and that is **the wrong universe**, kept here because the error is instructive
rather than embarrassing. That set relates two ENCODINGS of one string, so it
answers *was this value encoded correctly*. It presupposes exactly what is in
question: to form the pair at all you must already know which component is text
and which is octets. **The separation claim is about telling the two apart when
the underlying value is the same**, and a relation indexed by `s` can never
exhibit that case, because it never puts one value in two roles.

So the universe is over VALUES CARRYING A ROLE, and the encoding functions
become what generates its members rather than what its members are. It arrives
in two layers, and both are needed: a POPULATION of tagged values (`U`), and on
it the obligations a mechanism must discharge — `R`, refusing the UNCHANGED
retag of a CP member that has no OCT member at the same value, and `S`,
separating same-value pairs that have two live roles. `S` is the part no
predicate over the value can reach, and `R` is the part that one can.

## The universe, derived from the residue

A **role** is what a `(List Int)` value is being taken to mean:

    CP    the elements are Unicode codepoints — text
    OCT   the elements are octets — bytes on a wire, keys of a digest

The residue's members are **tagged values** `(v, r)`, `v : (List Int)`,
`r ∈ {CP, OCT}`, and the tagged population is

    U  =  { (v, CP) : v ∈ V_CP }  ∪  { (v, OCT) : v ∈ V_OCT }

      V_CP   = every (List Int) — `Str` CONSTRUCTION IS UNCHECKED (SPEC §3), so
               any list of Ints is some `Str`'s element list and can arrive
               tagged CP
      V_OCT  = lists of elements in 0..255 — what can be octets at all

The tag is **provenance**, not a component of the value: `(List Int)` carries
`v` and nothing else, which is the whole of #159 restated in the vocabulary the
claim needs.

**`V_CP` is defined by provenance and `V_OCT` by representability, and mixing
those two up is a live error rather than a pedantic one** — an earlier draft
defined `V_CP` as the SCALAR lists, which is a validity condition, and then
concluded that a non-scalar `Str`'s spine carried no role at all. It carries a
CP role: it came out of a genuine `Str`. What it lacks is an OCT counterpart.

### The fault is one operation, in both directions

The webhook residue's two measured sites are the same primitive move — a
**retag that is the identity on the value**:

    ρ : (v, r)  ↦  (v, r')          r ≠ r',  v unchanged

| direction | site | what it does | role supplied → role required |
|---|---|---|---|
| **text → octets** | the handler's own digest — `apps/github-webhook/webhook.oath:669` and `examples/webhook.oath:179`, both `hmac-sha256 (str-bytes secret) (req-body r)` | `str-bytes` is the identity on the codepoint spine, so the digest is keyed by a CP value | **CP → OCT** |
| **octets → text** | `bytes-str` (`apps/…/webhook.oath:125`), reached on the handler path at `:195` inside `json-scoped-string`, itself called from `:304` | reinterprets the body's octets as codepoints; no decode happens | **OCT → CP** |

Those are the RECEIVER sites, deliberately: `gh-sign` (`apps/…:484`) and the
signature built inside `accepts-correctly-signed` (`examples/webhook.oath:227`)
contain the same expression but are a spec helper and a property antecedent, and
citing them would put the residue in the test scaffolding rather than in the
code that runs.

Both directions are in U by construction, and neither is deferred. **Any further
site is another instance of ρ**, so it adds a location, not a class — which is
why the universe is derived from the residue rather than from a list of call
sites.

### The diagonal, where a value carries both roles at once

    Δ  =  V_CP ∩ V_OCT  =  V_OCT

strictly — `(Cons 1082 Nil)` is in `V_CP` and not in `V_OCT`. **The entire OCT
half of U therefore lies on the diagonal**, and that is the asymmetry the rest
of this section turns on.

It holds for two separate reasons, worth keeping apart because only the second
is a fact about Unicode: `V_CP` is everything, so the containment is immediate;
and every value in `0..255` is additionally a Unicode SCALAR value, which is
what makes an octet list not merely CP-tagged but a WELL-FORMED text — the
reason `bytes-str` never has to refuse anything (measured below).

On Δ the tag is invisible in the value, and both tags are genuinely inhabited.
The sharpest witness is one value in both roles at one site — `v = [195, 169]`,
message `hi`:

`hex-encode` returns a `Str`, which `oath eval` prints as an `SCons` spine, so
`dec` renders it back to text. It decides nothing and is part of every digest
transcript in this file:

```
$ dec() { python3 -c 'import sys,re; t=sys.stdin.read(); m="".join(chr(int(n)) for n in re.findall(r"SCons (\d+)", t)); print(m if m else t.strip())'; }
$ V='(Cons [Int] 195 (Cons [Int] 169 (Nil [Int])))'
$ ./oath/oath eval "(hex-encode (hmac-sha256 $V (str-bytes \"hi\")))" | dec   # v as OCT
736b1ddb4b8685c2375cf5e24d67dea428aee0ebd97865bfc79fd11ad8195092
$ printf 'hi' | openssl dgst -sha256 -hmac 'é' -r | cut -d' ' -f1            # the peer, also OCT
736b1ddb4b8685c2375cf5e24d67dea428aee0ebd97865bfc79fd11ad8195092             # AGREE
$ ./oath/oath eval '(hex-encode (hmac-sha256 (str-bytes "é") (str-bytes "hi")))' | dec
3a169af12b91e599a5c454790156426c43bd0aacd1b28cdc6cd930fd8a354f3f             # the CP path — different key
$ ./oath/oath eval "(== (str-bytes \"Ã©\") $V)"
true : Bool                                                                  # v as CP is the text "Ã©"
```

One value. Tagged OCT it is the UTF-8 of `"é"` and keys the digest the outside
world computes. Tagged CP it is the text `"Ã©"`. Nothing in `(List Int)`
separates those two facts.

### S — the separation obligation, as a relation rather than a population

U is a POPULATION of tagged values. The obligation the closure claim states is a
RELATION on it: the pairs of members a mechanism must hold apart. Only same-value
pairs can state that obligation, because a pair with two different values is
already held apart by the value:

    S  =  { ((v, CP), (v, OCT))  :  v ∈ V_OCT }

**S is exactly the diagonal, in bijection with `V_OCT`.** So the separation
obligation is precisely as large as the set of octet lists: every possible
request body, and every secret whose codepoints happen to fit in byte range.

Its two subsets are the two halves of the measurement, and they are subsets of
S rather than of U — a distinction that matters, because they are conditions on
`v`, applied to a pair:

    S_ascii  =  { ((v, CP), (v, OCT)) ∈ S  :  every element of v < 0x80 }
    S_high   =  { ((v, CP), (v, OCT)) ∈ S  :  some element of v ≥ 0x80 }

- **`S_ascii` is the OBSERVATIONAL-EQUALITY control.** Its two members denote the
  same thing: the same digest, and a decode that is the identity. Nothing an
  Oath program can compute distinguishes them, which is why the corpus's ASCII
  fixtures pass under either tag and caught nothing.
- **`S_high` is the silent webhook hazard**, in both directions at once — the
  digest is keyed differently and the decode produces mojibake, with nothing
  raised on either path.

**Class C contributes NO pair to S, and that is a property of the values rather
than a scoping decision.** An off-diagonal `v` has no OCT member for the pair's
second component to be, so no same-value pair exists to separate. It is
**RANGE-DISCRIMINABLE**: a predicate over the value alone decides it, and
`bytes-ok` is such a predicate, needing no provenance. The same is true of a
non-scalar `Str`'s spine, for the same reason and one step further out.

**Contributing no pair to S is NOT the same as being outside the obligation, and
conflating those two would let a later step claim closure too cheaply.** A
class C secret still reaches `hmac-sha256` — a CP-tagged value arriving at an
OCT position, which is the fault the claim names. It simply is not a SEPARATION
fault. So the obligation has two components, and the residue splits at the
diagonal into exactly these:

    R  =  { (v, CP)  :  v ∈ V_CP \ V_OCT }     REFUSE THE RETAG — a CP member
                                                with no OCT member at the SAME
                                                value, reaching an OCT position.
                                                Decidable from the value;
                                                `bytes-ok` decides it

    S  =  { ((v, CP), (v, OCT))  :  v ∈ V_OCT } SEPARATE — same value, two live
                                                roles. NOT decidable from the
                                                value, by construction

**`R` FORBIDS `ρ`, NOT ENCODING, and the difference decides what step 2 may
count as a failure.** `[1082]` is the text `"к"` and it certainly has an octet
reading — UTF-8 `[208, 186]`. What it does not have is an OCT member carrying
the SAME list, which is why the identity retag must be refused there. A real
encoder CHANGES the value and is therefore not `ρ` at all; a mechanism that
refuses `ρ` while admitting an explicit encode satisfies `R`, and reading `R`
as "these values may not become octets" would reject exactly the repair the
residue calls for. (The non-scalar members differ in the reason and not in the
obligation: UTF-8 has no encoding for them, and SPEC §3 leaves a kernel the
choice of an injective encoding or a named refusal.)

**Closure requires both, and step 2 must evaluate a mechanism against R and S
alike.** R is not already discharged: what refuses a class C secret today is the
Go kernel's runtime range check, not a type — an implementation limit reported
at run time, which is a different guarantee from a value that cannot reach that
position. S is the harder half and the one this file's measurements are pointed
at, but "harder" is not "the whole obligation".

### The partition — where confusion is observable, and where it is not

`ρ` is what confusion IS, so the classes are the classes of `ρ`'s behaviour.

The classes partition `V_CP` on DIAGONAL MEMBERSHIP first, and only then on
element range — which is what makes them disjoint. A value is on the diagonal
exactly when **every** element is in `0..255` — BOTH bounds, since `V_CP` is
every `(List Int)` and a negative element is as far off the diagonal as a wide
one. A single out-of-range element takes the whole value off it, however many
bytes sit beside it.

| class | values | `ρ` | observable? |
|---|---|---|---|
| **A** diagonal, ASCII | `v ∈ Δ` and every element < 0x80 | defined both ways | **NO — the two roles denote the same thing.** The equality control |
| **B** diagonal, high | `v ∈ Δ` and some element ≥ 0x80 (so every element is in `0..255`) | defined both ways | **YES, SILENTLY** — changes the HMAC key; changes the decoded text |
| **C** off-diagonal | `v ∉ Δ` — some element outside `0..255`, above it (`1082`) or below it (`-1`) | CP→OCT undefined; no OCT member exists, so **no pair in S** | **YES, LOUDLY — but by a tool**, see below |

The classes are conditions on VALUES; `S_ascii` and `S_high` above are the same
two conditions read as conditions on a PAIR. Class A ↔ `S_ascii`, class B ↔
`S_high`, and class C ↔ `R` — no pair, and the refusal obligation instead.

Disjoint and exhaustive over `V_CP` by construction, and the mixed case is the
one to check: `[233, 1082]` is **class C, not class B**, because `1082` puts it
off the diagonal — the high byte beside it changes nothing about whether the
value can carry an OCT role at all. Measured, not reasoned:

```
$ ./oath/oath eval '(bytes-ok (Cons [Int] 233 (Cons [Int] 1082 (Nil [Int]))))'
false : Bool
$ ./oath/oath eval '(hex-encode (hmac-sha256 (Cons [Int] 233 (Cons [Int] 1082 (Nil [Int]))) (str-bytes "hi")))'
error: byte list element out of range 0..255
```

**Class A is the equality control and it is why the defect ships.** For a
value all of whose elements are below 0x80, the text with those codepoints has
exactly those UTF-8 octets, so retagging changes nothing observable — the digest
agrees, the decode round-trips:

```
$ printf '  key C("correct-horse-battery-staple"): '; ...   # transcript below
  key C("correct-horse-battery-staple"): 9f17a8d3…3593ba9
  openssl (key O(same), message hi):     9f17a8d3…3593ba9            AGREE

$ ./oath/oath eval "(bytes-str <octets of 'hi'>)"
(SCons 104 (SCons 105 SNil)) : Str        →  "hi"                    IDENTITY
```

Every ASCII fixture, property and test in the corpus lives here, under either
tag, which is precisely why nothing caught the residue. It is also the control
in the instrument sense: a measurement reporting that confusion is observable in
class A would indict the measurement, not the language.

**Class B is where both directions bite, and neither raises.**

```
  # CP → OCT, message hi
  key C("é"):                3a169af12b91e599a5c454790156426c43bd0aacd1b28cdc6cd930fd8a354f3f
  openssl (key O("é")):      736b1ddb4b8685c2375cf5e24d67dea428aee0ebd97865bfc79fd11ad8195092
                                                                     DISAGREE, silently

  # OCT → CP
$ ./oath/oath eval "(bytes-str <octets of 'café'>)"
(SCons 99 (SCons 97 (SCons 102 (SCons 195 (SCons 169 SNil))))) : Str →  "cafÃ©"
```

**Class C is where a TOOL, not the type, intervenes — and only in one
direction.** `[1082, …]` has no OCT member, so the CP→OCT retag is refused:

```
  key C("ключ"):             error: byte list element out of range 0..255
  openssl (key O("ключ")):   a6bb06fd5113d614381228dadccbccdbeb05e99168c5f3e2dff41a0875e0babf
```

There is no matching class in the OCT→CP direction, and that follows from
`V_OCT ⊆ V_CP` rather than from a gap in testing: **every octet list is a valid
codepoint list, so `bytes-str` has no loud case at all.** A body that is not
valid UTF-8 in the first place is re-read as text in silence:

```
$ ./oath/oath eval '(bytes-str (Cons [Int] 233 (Nil [Int])))'
(SCons 233 SNil) : Str        →  "é"        # 0xE9 alone is not valid UTF-8
$ ./oath/oath eval '(bytes-str (Cons [Int] 255 (Cons [Int] 254 (Nil [Int]))))'
(SCons 255 (SCons 254 SNil)) : Str  →  "ÿþ"  # nor is 0xFF 0xFE
```

So the two directions are not symmetric in the only way an operator would
notice: the digest direction announces itself on class C, and the decode
direction never announces itself anywhere.

### The full transcripts, with their messages stated

A digest is a function of key AND message, so a table of key labels is not
reproducible. The message is `hi` throughout.

```sh
for s in 'correct-horse-battery-staple' 'é' 'ключ'; do
  printf '  key C("%s"): ' "$s"
  ./oath/oath eval "(hex-encode (hmac-sha256 (str-bytes \"$s\") (str-bytes \"hi\")))" 2>&1 \
    | python3 -c 'import sys,re; t=sys.stdin.read(); m="".join(chr(int(n)) for n in re.findall(r"SCons (\d+)", t)); print(m if m else t.strip())'
  printf '  openssl (key O("%s")): ' "$s"
  printf 'hi' | openssl dgst -sha256 -hmac "$s" -r | cut -d' ' -f1
done
```

```
  key C("correct-horse-battery-staple"): 9f17a8d3d1ca75f4de963f878d19e78a611d3aeb451707218b0473c6a3593ba9
  openssl (key O("correct-horse-battery-staple")): 9f17a8d3d1ca75f4de963f878d19e78a611d3aeb451707218b0473c6a3593ba9
  key C("é"): 3a169af12b91e599a5c454790156426c43bd0aacd1b28cdc6cd930fd8a354f3f
  openssl (key O("é")): 736b1ddb4b8685c2375cf5e24d67dea428aee0ebd97865bfc79fd11ad8195092
  key C("ключ"): error: byte list element out of range 0..255
  openssl (key O("ключ")): a6bb06fd5113d614381228dadccbccdbeb05e99168c5f3e2dff41a0875e0babf
```

The class A row is the discriminating control: the same two commands agree
there, so the class B disagreement is not the `openssl` invocation being wrong.
Scoped as the existing record scopes it — the disagreement is measured for the
message `hi`; what holds for every message is that the two sides are keyed
differently.

The reverse direction, with its body built as octets rather than spelled out:

`oct` builds a body from a string's UTF-8 octets; `txt` renders a returned `Str`
back to text as a Python `repr`, so an unprintable codepoint cannot be lost in
the terminal. Neither decides anything.

```sh
oct() { python3 -c 'import sys
t="(Nil [Int])"
for b in reversed(sys.argv[1].encode("utf-8")): t="(Cons [Int] %d %s)"%(b,t)
print(t)' "$1"; }
txt() { python3 -c 'import sys,re; print(repr("".join(chr(int(n)) for n in re.findall(r"SCons (\d+)", sys.stdin.read()))))'; }

for s in 'hi' 'café' 'ключ'; do ./oath/oath eval "(bytes-str $(oct "$s"))"; done          # spines
for s in 'hi' 'café' 'ключ'; do ./oath/oath eval "(bytes-str $(oct "$s"))" | txt; done    # as text
```

The spines, verbatim:

```
(SCons 104 (SCons 105 SNil)) : Str
(SCons 99 (SCons 97 (SCons 102 (SCons 195 (SCons 169 SNil))))) : Str
(SCons 208 (SCons 186 (SCons 208 (SCons 187 (SCons 209 (SCons 142 (SCons 209 (SCons 135 SNil)))))))) : Str
```

and the same three through `txt`, verbatim:

```
'hi'                    # class A — the identity, and the reason nothing caught this
'cafÃ©'                 # class B — mojibake
'ÐºÐ»Ñ\x8eÑ\x87'        # class B — and two codepoints, U+008E and U+0087, are C1
                        # controls with no printable form: not merely wrong text
```

**Every octet of `ключ` is in byte range, so the Cyrillic BODY is class B while
the Cyrillic SECRET is class C.** The class of a member is a property of the
VALUE and its role, never of the string a human had in mind.

### Non-scalar `Str` — a CP member with no OCT counterpart

`Str` construction is unchecked: SPEC §3 says a kernel MUST NOT reject a
non-scalar element at construction, so `(SCons -1 (SNil))` is an ordinary `Str`
and `(Cons -1 Nil)` is an ordinary CP-tagged member of U. **What it lacks is an
OCT role**, because UTF-8 has no encoding for a non-scalar value — a fact about
the encoding, not about any kernel's boundary policy. It therefore contributes
no pair to S and falls under `R` instead, exactly as class C does — the refusal
obligation, not an exemption.

Stating it that way matters, because SPEC §3's PACK does NOT simply require
refusal: it requires that an implementation *encode each element injectively OR
refuse that element by name*, and a kernel taking the first branch would still
not be producing UTF-8. This kernel takes the second, measured below.

```
$ ./oath/oath eval '(SCons -1 (SNil))'
(SCons -1 SNil) : Str
$ ./oath/oath eval '(str-bytes (SCons -1 (SNil)))'
(Cons -1 Nil) : (List Int)
$ ./oath/oath eval '(bytes-ok (str-bytes (SCons -1 (SNil))))'
false : Bool
```

**Leaving it out of S cannot smuggle a silent case out of the obligation**, and
that is a consequence of the ranges rather than an assurance: every value in
`0..255` is a scalar value, so a non-scalar element is necessarily outside
`0..255`, so no non-scalar value is on the diagonal. `S_high` — the silent set —
contains none of them. The digest defect's own secrets arrive by ADMIT
(`process_env`), which SPEC §3 requires to decode as UTF-8 or refuse.

## World claims and tool claims

Kept separate because they read identically in a status line and only the first
kind survives the tool improving.

**WORLD — about the language and the values:**

- `(List Int)` carries a value and no role; the role is provenance, and the
  residue in both directions is a retag `ρ` that is the identity on the value.
- `V_OCT ⊆ V_CP`, strictly. Every octet list is a legitimate codepoint list, so
  the OCT half of U lies entirely on the diagonal and the OCT→CP direction has
  no refusable case.
- The separation obligation is the same-value relation
  `S = {((v, CP), (v, OCT)) : v ∈ V_OCT}`, in bijection with `V_OCT`; everything
  off the diagonal is range-discriminable and contributes no pair. Those
  off-diagonal members carry the REFUSAL obligation `R` instead, which is part
  of the claim and not discharged by falling outside `S`.
- On `S_ascii` the two roles denote the same thing, so `ρ` is unobservable
  there.
- **A non-scalar `Str`'s spine carries a CP role and no OCT role.** It is a
  member of U, not an exclusion from it, and it contributes no pair to S.
- `str-bytes` is the identity on the codepoint spine; no encoding happens
  (cited).
- Non-scalar `Str` values carry no OCT role, and none of them is on the
  diagonal.

**TOOL — about this kernel, this corpus, this run:**

- `error: byte list element out of range 0..255` is the **Go kernel's runtime
  range check** reached through `hmac-sha256`. It is not a type error and not a
  language-level distinction between roles. Class C failing loudly is an
  IMPLEMENTATION LIMIT REPORTED, not a semantic fact: nothing in a class C value
  says "this is text" — only that some element is too large to be an octet.
- `bytes-ok` is a predicate over VALUE RANGE, not over provenance. It accepts
  exactly the diagonal, so it passes on all of class A and all of class B and
  cannot see the tag:

  ```
  $ for s in 'hi' 'é' 'Ã©' 'ключ'; do ./oath/oath eval "(bytes-ok (str-bytes \"$s\"))"; done
  true : Bool          # class A
  true : Bool          # class B
  true : Bool          # class B — and this value is the UTF-8 of "é"
  false : Bool         # class C
  ```

- `openssl` and `python3` stand in for the octet reading of a string; the UTF-8
  standard, not this repository, is what makes them the reference. No UTF-8
  encoder is bound in `codebase/`.
- Printed `(Cons …)`/`(SCons …)` spines are `oath eval`'s rendering; the
  diagonal claim rests on the in-language `==`, not on the rendering.

## What step 1 does NOT establish

- Nothing about refinement types, their identity, #69, or #133's obstacle.
- Nothing about whether one mechanism or two would be needed for the two
  directions of `ρ`.
- Nothing about how many corpus or deployment sites instantiate `ρ`. U is
  derived from the residue, so a new site is a new location in an existing
  class; counting locations is a different question and is not asked here.

---

# Step 2 — refinements over the same base value, against `R` and `S`

## The construction under test, and what it is not

`docs/refinements.md` is the #69 design note, and it is **DESIGN ONLY** — no
kernel implements refinements, so nothing in this step measures a refinement
type. What it does is reason from that note, supplying every fact the reasoning
depends on by measurement.

The construction tested here is

    {v : (List Int) | P v}

— a refinement whose **base value remains the original `(List Int)`**. A record
wrapper, an opaque or abstract type, a nominal newtype and a distinct datatype
are all OUTSIDE it, because each changes the value or the type's identity rather
than attaching a predicate to the value already flowing.

Four properties of the design are load-bearing, cited rather than paraphrased:

- **The form.** `{x: T | P}` — a base type and a proposition
  (`docs/refinements.md` §"THE IDENTITY DECISION").
- **Identity is SYNTACTIC**, over the O1 canonical encoded AST rather than
  source text: *"`{x | x > 0}` and `{x | 0 < x}` are different types with
  different hashes"* (§"THE IDENTITY DECISION", §"'Syntactic' means the CANONICAL
  ENCODED AST").
- **Subtyping is by implication, at the call.** `{x: T | P} <: {x: T | Q}` iff
  `P ⟹ Q`, discharged by SMT; `{x: T | P} <: T` always; `T <: {x: T | P}` only
  with a discharged obligation (§"Subtyping: where the SMT actually goes").
- **Obligations get the guarantee ladder, not a binary** — proven / tested /
  falsified / asserted, where only *falsified* is a hard error, because *"an
  undischargeable obligation must NOT be a hard type error"* (same section).

## Case 1 — the range predicate. It discharges `R`

    Octets  =  {v : (List Int) | bytes-ok v}

`bytes-ok` (`examples/http.oath:132`) is exactly the decision procedure `R`
needs, and the corpus comments have been asking for this refinement by name for
some time (`examples/http.oath:128`, `apps/…/webhook.oath:487`,
`examples/webhook.oath:222` — all three say `{b: Int | 0 <= b <= 255}` would
have said it in the type).

Against `R`: a class C value reaching an OCT position needs `T <: Octets`, and
where that obligation is SETTLED the outcome is what `R` asks for — a falsified
obligation is the ladder's one hard error. **`R` becomes EXPRESSIBLE in the
type, out of the Go kernel's runtime range check.** That is a real gain and this
step does not diminish it.

**But expressible is not discharged, and the gap is the ladder's by design.**
An obligation that is neither proved nor falsified is RECORDED — as `tested` if
the deterministic tester found no counterexample, as `asserted` otherwise — and
in both cases the call proceeds, because only *falsified* is a hard error. In either case a class C value still reaches its OCT
position, which is exactly what `R` forbids. So the construction satisfies `R`
on the obligations a checker SETTLES — proves or falsifies — and not in general — and the residual is
not hypothetical bookkeeping, since the obligation here is a recursive predicate
over a list and `docs/refinements.md` says quantified or recursive refinements
*"will sometimes not discharge"*.

One further limit, from the design's own silence rather than from scepticism:
it does not settle flow-sensitivity, so what happens after a runtime `bytes-ok`
test in an enclosing branch is not something this step can read off it.

**Now the shape of what just happened, which is the whole of case 1's bearing on
`S`.** The extension of `bytes-ok` is exactly `Δ`, and `Δ` is exactly `S`'s
domain. So the predicate that discharges everything OFF the diagonal accepts
BOTH members of every pair ON it. The instrument is not weak here; it is
maximally blind, and by construction.

## Case 2 — two syntactically distinct predicates. Different hashes, and NOT kept apart

A second spelling of the same set, associated the other way and written with
strict inequalities against the neighbouring integers:

```
(defn byte-range-ok [] [(bs (List Int))] Bool
  (match bs
    ((Nil) true)
    ((Cons b rest) (and (< -1 b) (and (< b 256) (byte-range-ok rest))))) …)
```

Measured in a throwaway copy of the store — the corpus-level ANALOGUE of the
design's identity rule, since refinement types do not exist to be hashed:

```
$ OATH_STORE=<copy> ./oath/oath ls | grep -E 'bytes-ok|byte-range-ok|spellings-agree'
byte-range-ok    #961b502d5afc  func  tested (200 cases per property) · total
bytes-ok         #d2406871baf1  func  PROVEN (all 3 properties, Z3 over unbounded ints) · total
spellings-agree  #90e4a488f855  func  tested (200 cases per property) · total

$ OATH_STORE=<copy> ./oath/oath prove spellings-agree
∎ PROVEN    holds-for-every-list         induction on binder 0 (Z3, unbounded ints)
proven: 1/1 properties
```

`spellings-agree` is `(== (bytes-ok v) (byte-range-ok v))`, PROVEN for every
list. So the two predicates are **different objects with the same extension**,
which is precisely the situation the identity decision describes for
`{v | bytes-ok v}` and `{v | byte-range-ok v}`: different propositions,
different canonical ASTs, **different type hashes**.

**And that buys nothing at a call.** Subtyping is `P ⟹ Q` by SMT, and the
implication holds in BOTH directions here — that is what the proof above says —
so each is a subtype of the other and either may be passed wherever the other is
required.

> **DIFFERENT HASHES IS NOT KEPT APART AT A CALL.** Syntactic identity keeps two
> types from being the same OBJECT. Semantic subtyping is what decides whether a
> value may cross a boundary, and it erases the syntactic distinction exactly
> when the two predicates have the same extension.

**Two questions live here and only one of them is the obligation**, which an
earlier draft of this paragraph ran together — it said two refinements are kept
apart at a call when their extensions DIFFER, and that is simply wrong.
`{x|P} <: {x|Q}` iff `P ⟹ Q`, i.e. iff the extension of `P` is a SUBSET of the
extension of `Q`. Non-interchangeability is failed INCLUSION, not difference:
`{v | all elements in 0..127}` and `{v | all elements in 0..255}` have different
extensions and one is still freely passable as the other.

    (i)   are the two TYPES interchangeable?     — inclusion between extensions
    (ii)  does a type separate the two MEMBERS   — the obligation S states
          of a pair in S?

Getting (i) is easy and buys nothing. Two refinements that disagree anywhere —
outside `Δ`, or on part of it — fail inclusion in one direction and are
genuinely not interchangeable. **And every one of them still treats both members
of every `S` pair identically**, because separation would need a predicate that
accepts one member and rejects the other, while membership is a function of `v`
alone and the two members ARE one `v`. Whichever side of the predicate that
value falls, both members fall with it.

So the sharpening runs the other way from what the draft claimed: you can
manufacture as many mutually non-implying refinement types over `(List Int)` as
you like, and not one of their PREDICATES decides which role a given `v` is in.
Whether the TYPES so declared can still block a call is a different question,
and it is answered in the result section rather than here.

## Case 3 — a predicate mentioning the producer. Vacuous, and its negation empty

The one remaining hope for a value predicate is to name where the value CAME
FROM:

    {v : (List Int) | ∃ s : Str. v == str-bytes s}

**It is true of every value.** `str-bytes` and `bytes-str` are mutually inverse,
so `s = bytes-str v` witnesses the existential for any `v`. One direction is
already PROVEN in the committed corpus (`bytes-str`'s `inverts-str-bytes`,
`#db845547035a`: `(bytes-str (str-bytes s)) == s`). The other was not, so it was
proved for this step, in the store copy:

```
(defn str-bytes-is-onto [] [(v (List Int))] Bool
  (== (str-bytes (bytes-str v)) v)
  (prop holds-for-every-list [(v (List Int))] (str-bytes-is-onto v)))
```

```
$ OATH_STORE=<copy> ./oath/oath prove str-bytes-is-onto
lemma library: 8 from dependencies, 1 from prior runs
∎ PROVEN    holds-for-every-list         induction on binder 0 (Z3, unbounded ints)
proven: 1/1 properties
```

`str-bytes` is therefore a BIJECTION `Str ↔ (List Int)`, machine-checked in both
directions. So:

- the provenance predicate is **VACUOUSLY TRUE** — its extension is every
  `(List Int)`, so it types nothing out;
- its negation, `{v | ¬∃s. v == str-bytes s}`, has an **EMPTY EXTENSION** — no
  value satisfies it.

One admits everything and the other admits nothing, and the reason is the same
fact from step 1 wearing different clothes: `str-bytes` performs no encoding, so
"came from a `Str`" is not a property that any value fails.

**Both are EXTENSIONAL facts, and this step stops there rather than predicting
what a checker would do with them.** What a call does depends on how an
implementation discharges `T <: {x | P}` for a predicate that is quantified over
`Str` and recursive — whether the tester can decide the inner existential at a
generated value (it would have to construct the witness `bytes-str v`, or reach
for the surjectivity lemma proved above), and whether an unrefuted, unproved
obligation lands `tested` or `asserted`. `docs/refinements.md` specifies none of
that, no kernel implements refinements, and three drafts of this paragraph each
asserted a different operational outcome before this one declined to.

The extensional result is what the case needed and it does not depend on any of
it: **a predicate that no value fails and a predicate that no value satisfies
are the two ways of saying nothing**, and naming the producer gives exactly
those two.

The dependent spelling `{v | v == str-bytes s}`, with `s` a free variable, is a
RELATION between two values rather than a property of one. It can be stated
where `s` is in scope, and the design note describes refinements over `x` alone;
a type carrying a free variable could not travel with the value past the binding
of `s`, which is exactly where the digest's octets go.

## The result, decidable, and for this construction only

The three cases are not the evidence. They are the three ways one might hope to
escape what follows, and each fails for its own reason.

### What no predicate can do

> **The two members of any pair in `S` are the SAME VALUE `v`, and membership in
> `{v : (List Int) | P v}` is a function of `v`. So for every predicate `P`,
> both members are in the type or both are out.**

That quantifies over ALL predicates rather than sampling them, and no search
would help — which is what makes it decidable rather than empirical. **What it
establishes is that no predicate DECIDES a value's role**, and it is worth
stating exactly that much, because an earlier draft of this section stretched it
into "no refinement separates any pair", which is FALSE and was caught in
review.

### Separation at a call, once the declarations have to be SOUND

A call is not checked by testing the value. It is checked by SUBTYPING between
DECLARED types, so the question is what refinements the two ends can soundly
carry. An earlier draft answered that with "any two incomparable predicates",
concluded the separation would be nominal, and left the outcome undetermined.
**That was wrong, and the universe from step 1 is what refutes it.**

**The CP end cannot carry a non-trivial refinement at all.** A sound result type
`str-bytes : Str -> {x : (List Int) | P x}` needs every value `str-bytes` can
return to satisfy `P` — and `str-bytes-is-onto`, PROVEN above, says its range is
ALL of `(List Int)`. So `ext(P) = V_CP =` every list, and `P` is vacuous. This is
the same measurement doing double duty: the bijection that empties the
provenance predicate also forbids any sound refinement on the producer's result.

**The OCT end carries exactly `Δ`.** `ext(Q) ⊆ Δ` or the type admits values that
are not octets; `ext(Q) ⊇ Δ` or it rejects legitimate bodies. So `ext(Q) = Δ`.

Now read the two directions off the subtyping rule, `P ⟹ Q` iff
`ext(P) ⊆ ext(Q)`:

| direction | check | extensions | result |
|---|---|---|---|
| **OCT → CP** — `bytes-str` on a body | `Q ⟹ P` | `Δ ⊆` every list | **HOLDS — the call is allowed** |
| **CP → OCT** — the digest | `P ⟹ Q` | every list `⊄ Δ` | fails, and falsifiably: `[1082]` is a counterexample |

**The reverse direction gets no protection at all, and that conclusion depends
on nothing left open.** `Q ⟹ P` holds outright, so no ladder state, no
obligation granularity and no flow-sensitivity is involved: reading a request
body as text is exactly as permitted after the refinements as before.

**And the forward direction's block is a RANGE block — it is `R` again, not
`S`.** It falsifies on `[1082]`, a class C value, and against the DECLARED types
it falsifies for every secret including ASCII ones, because `str-bytes`'s
refinement is vacuous. A flow-sensitive checker could do better, and the design
note leaves flow-sensitivity open, so this step cannot say the forward call is
rejected outright.

**But look at what the flow-sensitive route actually buys, because it is not
separation.** The app already guards with `secret-is-usable`
(`apps/github-webhook/webhook.oath:663`), whose second conjunct demands
printable ASCII; a checker that narrowed the secret to `{x | ascii x}` after
that guard would discharge `⟹ Octets`, admitting the ASCII secret while `[233]`
never reaches the narrowing at all. That works — and it works by **EXCLUDING
`S_high` from the program**, not by telling CP from OCT. What is left is
`S_ascii`, where step 1 measured the two roles as observationally equal and
there is nothing to separate.

So the honest forward-direction statement is narrower than "the block fails" and
sharper than "the block works": on the diagonal a predicate can only ADMIT or
EXCLUDE a value in BOTH roles, so a program either refuses class B secrets
outright — losing every legitimate one along with the dangerous ones — or admits
them and is wrong about them. There is no third option, and that is a property
of the construction rather than of any checker's cleverness.

The nominal escape survives only by making a declaration UNSOUND — excluding
legitimate CP values, or admitting non-octets. That is not a discipline a
correct program can adopt, so it is not an open question about the design; it is
a wrong turn, recorded here because it took a review pass to see it was one.

**The two obligations, then:**

    R  —  EXPRESSIBLE, and discharged exactly on the obligations a checker
          SETTLES (proves or falsifies). Where one lands `tested` or
          `asserted` the call proceeds and a class C value still reaches an OCT
          position, so the construction does NOT satisfy R in general
    S  —  NOT SEALED by this construction. OCT→CP is ALLOWED outright by
          subtyping, unconditionally. CP→OCT can be blocked, but only by RANGE:
          on Δ a predicate admits or excludes a value in BOTH roles, so a
          program can refuse class B wholesale — restricting itself to S_ascii,
          where the roles coincide — and cannot tell the two roles apart

The two are limits of different kinds, and collapsing them would misreport both.
`R`'s is a COVERAGE limit: a better solver, a lemma or `oath hint` moves
individual obligations from unsettled to settled, and nothing about the
construction stands in the way. `S`'s is not a solver limit at all — no proof
effort makes a predicate see a distinction the value does not carry, and no
declaration a correct program can make blocks the reverse direction at all.

## What step 2 does NOT establish

- **Nothing about a record wrapper, an opaque or abstract type, a nominal
  newtype, or a distinct datatype.** Each changes the value or the type's
  identity, so each is a different construction and none inherits this result.
  Note especially that `issue-159.md`'s measurement — a monomorphic `Bytes`
  DATATYPE puts to `Str`'s hash — is a fact about datatypes and does not follow
  from anything here, nor this from it.
- Nothing about refinements over other base types, or about #69 as a whole. #69
  is a general feature and this is one construction inside it.
- Nothing about whether discharging `R` alone justifies the feature. That is a
  judgement about cost, and no measurement here speaks to it.
- Nothing about `S` being unreachable by any mechanism. What is established is
  that a predicate over the unchanged value cannot reach it — which is a
  statement about this construction, not about the design space.

## Provenance of step 2's measurements

`str-bytes-is-onto` (`#33ee2d264b24`), `byte-range-ok` (`#961b502d5afc`) and
`spellings-agree` (`#90e4a488f855`) were put with `--new` into a COPY of
`codebase/` in a scratch directory and proved there. They are not in the
canonical store and their names are not bound in it — an exercise gets its own
store, never the namespace holding the standard library. `git status codebase/`
was clean before and after, and `bytes-ok`'s hash `#d2406871baf1` is the
committed corpus object, unchanged.

---

# Step 4 — the class of mechanism step 2 does not reach

**This step names a CLASS and designs nothing.** It adds no measurement, and it
rests entirely on the SHAPE of step 2's argument rather than on any new fact. If
it reads as a proposal, a preference between the options below, or the beginning
of a design, it has gone wrong.

## The criterion, which generates the class rather than listing it

Step 2's result comes from one sentence:

> membership in `{v : (List Int) | P v}` is a FUNCTION OF `v`, and the two
> members of every pair in `S` are one `v`.

Everything else in step 2 — the two spellings, the vacuous producer predicate,
the soundness argument that empties the CP end — is a consequence of that
premise or a failed attempt to escape it. So the criterion for what step 2 does
not reach is exactly the negation of its premise:

> **A mechanism is outside step 2's result iff how it CLASSIFIES a value — what
> type that value has, and therefore what it may be passed to — is not solely a
> function of the original, unchanged `(List Int)`.**

**The subject of that sentence is the MECHANISM'S CLASSIFICATION, not the role**,
and getting that wrong makes the criterion vacuous. Two earlier drafts did: they
said the ROLE is not a function of the value, which is true of step 2's own
construction — indeed it is the whole defect — so the criterion admitted the
very thing it was meant to exclude. What distinguishes step 2's construction is
that ITS classification, membership in `{v | P v}`, IS solely a function of `v`.

Two ways for a classification to fail that, and both matter because they cover
different options:

    classification depends on something       phantom types, nominal types with
    carried ALONGSIDE the value               hidden constructors, a brand or
                                              result index however obtained

    the thing classified is no longer         record wrappers, distinct
    the bare list                             datatypes, opaque types

Stating it as a criterion rather than a list matters, because a list invites the
reading that these are the options. They are instances.

## Three named candidates — two are in the class, one only in a particular form

- **PHANTOM TYPES.** The role rides in a type parameter that no value inhabits,
  so two values with identical representation carry different types. Provenance
  lives in the type index.
- **NOMINAL TYPES / NEWTYPES WITH HIDDEN CONSTRUCTORS.** The role IS the type's
  identity, and the hidden constructor is the load-bearing half rather than the
  wrapper: a wrapper whose constructor is freely available is one retag away
  from the value it wraps, which is the same laundering shape step 2 found in
  *forgetting a refinement is free*.

**EFFECT / CAPABILITY TYPING is named third because this step's brief named it,
and reporting it accurately means reporting that its ORDINARY form does NOT
satisfy the criterion.** An effect discipline types a COMPUTATION — what it may
do, what authority it holds. Effect rows and capability records alike describe
the computation, and neither keeps WHICH capability produced a value once that
value is bound: what comes back is a bare `(List Int)`, both roles are again one
value at one type, and that is step 2's premise restored rather than escaped.

It enters the class only in a RESULT-INDEXED or BRANDED form, where the role
persists on the value — and at that point the persistence is doing the work, and
it is the first instance's machinery under another name. So the accurate
statement is not "effect typing is a third way" but "provenance must persist on
the value, and an effect system is one place a brand could come from".

Worth pointing at rather than rediscovering: Oath's capability model is option 3
of `docs/effects.md` §"Options considered", chosen because *"capability passing
adds zero type rules to the trusted core"* — and effect rows, option 1, were
considered and declined there. So nothing in the language today carries a role
this way, in either form. That is a statement about what exists, not an argument
against the option.

**These are different features with different costs, and the costs are not
compared here.** One known obstacle is worth pointing at rather than
rediscovering: Oath's types are STRUCTURAL, so the nominal instance runs into
the thing #133 recorded and `issue-159.md` measured — `Str` has no identity of
its own, and a monomorphic `Bytes` datatype puts to `Str`'s hash byte for byte.
That is a pointer to a known result, not an assessment of the option.

The list is not exhaustive. Anything satisfying the criterion belongs to the
class whether or not it is named here.

## What step 2 declared OUT, which is not the same as refuted

Step 2's construction was refinements over the unchanged `(List Int)`. **Record
wrappers, opaque or abstract types, and distinct datatypes were declared OUTSIDE
it**, and being outside a result is not being refuted by one:

> **None of them is excluded by anything in this file. Any of them may still
> work, and step 2 says nothing either way about them.**

Each changes the value or the type's identity, so each fails step 2's premise by
the criterion's SECOND row — which is the same statement seen from the other
side, and the reason the criterion has to have that row at all.

## What a later evaluation of any candidate would owe

Not guidance about mechanism, only the scope already fixed by step 1: a
candidate has to be evaluated against **both** obligations, `R` and `S`, and in
**both** directions of `ρ`. Step 2's own finding is the reason to say so — the
CP→OCT direction is the one the residue's original write-up leads with, while
the OCT→CP direction turned out to be the one nothing protected at all.

## What step 4 does NOT establish

- That any mechanism in the class WORKS. The criterion says step 2's argument
  does not apply to them; it says nothing about whether they discharge `R` or
  seal `S`.
- Any ranking, preference or recommendation among the instances.
- Anything about cost, feasibility, or fit with the O1 encoding. The one
  obstacle named above is a pointer to an existing measurement, not an
  evaluation.
