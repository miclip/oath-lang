# #159 — can a refinement discipline reach the residue?

**What this file is:** the record of an evaluation of #69's refinement types
against #159, run in steps, with each step committed before the next begins.

**ONLY STEP 1 IS PRESENT.** Step 1 states the claim under evaluation and derives
the universe that claim quantifies over. It evaluates no mechanism, proposes no
repair, and reaches no verdict on #159 or #69. Anything below that reads as an
argument for or against a design is a defect in this file.

Run on 2026-08-09 against `fc3f8f6`, with `oath/oath` rebuilt from that tree.
`oath eval` resolves names out of `codebase/` without writing to it; nothing was
put, proved or waived, and `git status` was clean before and after.

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
