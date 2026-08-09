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
> webhook digest defect confuses them.

That is the sentence later steps have to make true or false. It is stated here
so the universe can be derived FROM IT rather than from #69's design space or
from the kernel's decomposition, per this repo's rule that a witness derives its
universe from the claim.

### Step 1's universe is NECESSARY, NOT SUFFICIENT, for that claim

**Step 1 derives one component of the claim's universe: the digest defect, where
text is read as octets.** #159's residue has a measured second direction —
`bytes-str` reading a request body's octets as codepoints (`issue-159.md` §"A
THIRD site, in the other direction"). Its universe is NOT derived here.

Stated in the file rather than left to a reader's inference, because the failure
it prevents is specific: **a mechanism can satisfy every pair derived below and
still not close #159.** No later step may report closure from this universe
alone; the reverse-direction universe has to be derived first, and until it is,
the strongest available verdict is *insufficient* or *not yet decided* — never
*closes*. This is the same shape as the correction already recorded on #159's
own falsifier, where a claim scoped to boundaries read as exhaustive precisely
because the defect had stopped being a boundary problem.

## What is already established, and not re-derived here

`docs/experiments/issue-159.md` is the measured record; the #159 comment of
2026-08-08 (commit `60e9074`) summarises it. Three of its results are load-
bearing for the derivation below and are cited rather than repeated:

- **`Str` and `(List Int)` are ALREADY distinct types**, in both directions,
  with well-typed controls. The conflation is not between them — it is INSIDE
  `(List Int)`, one level lower (§"`(List Int)` and `Str` are ALREADY distinct
  types").
- **`str-bytes` performs no encoding.** It is the identity on the codepoint
  spine, so the value it produces is a codepoint list wearing an octet list's
  type (§"The crypto measurement" / §"`ключ` versus Latin-1").
- **A monomorphic `Bytes` datatype puts to `Str`'s hash, byte for byte**, from
  two independent stores (§"Result — the two printed hashes"). Cited only as a
  constraint on what a mechanism may assume; not evaluated here.

The origin measurement is `docs/experiments/webhook-friction.md` entry 3. #133
records why `Str` has no identity of its own.

## The universe, derived from the digest defect

The defect is a disagreement between two functions, not between two types. The
receiver computes its digest keyed by `(str-bytes secret)`; every peer that
signs — `openssl`, GitHub — computes it keyed by the UTF-8 octets of the same
secret. So there are two functions `Str -> (List Int)`:

    C(s)  =  str-bytes s              the CODEPOINT reading — in-language, PROVEN,
                                      TOTAL on Str
    O(s)  =  the UTF-8 octets of s    the OCTET reading — what a peer means by
                                      "the bytes of s"; DEFINED ONLY where every
                                      element of s is a Unicode scalar value

and the universe the closure claim quantifies over is the set of PAIRS

    U  =  { (C(s), O(s))  :  s : Str, every element of s a scalar value }

**The domain restriction is derived, not convenient.** SPEC §3 states that `Str`
CONSTRUCTION IS UNCHECKED and that a kernel MUST NOT reject a non-scalar element
at construction — `(SCons -1 (SNil))` is an ordinary value — while PACK MUST
encode each element injectively **or refuse it by name**. So `O` has no value on
a non-scalar `Str`, and a universe written over all of `Str` would be asserting
pairs whose second component does not exist. The excluded class is named and
measured below rather than waved past.

Both components inhabit **one type**, `(List Int)`. The defect is that the
program supplies the first where the protocol requires the second, and the pair
is the smallest object that can express that, which is why the universe is pairs
and not values.

`O` is not realized in the committed corpus — no UTF-8 encoder is bound in
`codebase/` — so `python3`/`openssl` stand in for it below. That is a property
of the corpus, not of the language; `issue-159.md` §"Decoding IS expressible
today" measured a 1–2 byte decoder written in current Oath.

### The partition, by the codepoints `s` contains

Exhaustive and disjoint by construction. The empty string falls in class A.

| class | `s` contains | `C(s)` vs `O(s)` | is `C(s)` a well-formed octet list? |
|---|---|---|---|
| **A** ASCII | no codepoint ≥ U+0080 | **equal as values** | yes |
| **B** Latin-1 | some codepoint in U+0080..U+00FF, none above | **different values** | **yes** — every element is in 0..255 |
| **C** wide | some codepoint > U+00FF | different values | no — an element exceeds 255 |

Measured, verbatim:

```
$ ./oath/oath eval '(str-bytes "hi")'
(Cons 104 (Cons 105 Nil)) : (List Int)              # C, class A
$ ./oath/oath eval '(str-bytes "é")'
(Cons 233 Nil) : (List Int)                         # C, class B
$ ./oath/oath eval '(str-bytes "Ã©")'
(Cons 195 (Cons 169 Nil)) : (List Int)              # C, class B
$ ./oath/oath eval '(str-bytes "ключ")'
(Cons 1082 (Cons 1083 (Cons 1102 (Cons 1095 Nil)))) : (List Int)   # C, class C

$ python3 -c 'for s in ["hi","é","Ã©","ключ"]: print(list(s.encode("utf-8")))'
[104, 105]                                          # O("hi")     == C("hi")
[195, 169]                                          # O("é")
[195, 131, 194, 169]                                # O("Ã©")
[208, 186, 208, 187, 209, 142, 209, 135]            # O("ключ")
```

### The digest witness, with its message stated

**The message is held constant at `hi` for every row**, and both sides are shown
in full, because a digest is a function of key AND message and a table of key
labels alone is not reproducible:

```sh
for s in 'correct-horse-battery-staple' 'é' 'ключ'; do
  printf '  key C("%s"): ' "$s"
  ./oath/oath eval "(hex-encode (hmac-sha256 (str-bytes \"$s\") (str-bytes \"hi\")))" 2>&1 \
    | python3 -c 'import sys,re; t=sys.stdin.read(); m="".join(chr(int(n)) for n in re.findall(r"SCons (\d+)", t)); print(m if m else t.strip())'
  printf '  openssl (key O("%s")): ' "$s"
  printf 'hi' | openssl dgst -sha256 -hmac "$s" -r | cut -d' ' -f1
done
```

`oath eval` prints a `Str` as its `SCons` spine, so the `python3` filter renders
`hex-encode`'s result back to text; it decides nothing. Output, verbatim:

```
  key C("correct-horse-battery-staple"): 9f17a8d3d1ca75f4de963f878d19e78a611d3aeb451707218b0473c6a3593ba9
  openssl (key O("correct-horse-battery-staple")): 9f17a8d3d1ca75f4de963f878d19e78a611d3aeb451707218b0473c6a3593ba9
  key C("é"): 3a169af12b91e599a5c454790156426c43bd0aacd1b28cdc6cd930fd8a354f3f
  openssl (key O("é")): 736b1ddb4b8685c2375cf5e24d67dea428aee0ebd97865bfc79fd11ad8195092
  key C("ключ"): error: byte list element out of range 0..255
  openssl (key O("ключ")): a6bb06fd5113d614381228dadccbccdbeb05e99168c5f3e2dff41a0875e0babf
```

**Class A is the control, and it is the whole reason the defect ships.** `C = O`
there as values, so the two readings coincide and no confusion is expressible.
Every ASCII test, fixture and property passes under either reading, and the
first row AGREES.

**Class B is the silent witness.** `C(s) ≠ O(s)`, and yet `C(s)` is a perfectly
well-formed octet list, so the digest is simply computed under a different key
and the second row DISAGREES. Nothing is raised.

The class A row is the discriminating control for that instrument: the same two
commands agree there, so the class B disagreement is not the `openssl`
invocation being wrong. Scoped as the existing record scopes it — the
disagreement is measured for the message `hi`; what holds for every message is
that the two sides are keyed differently.

**Class C is where a tool, not the type, intervenes** — see the world/tool split
below.

### Class D — non-scalar `Str`, excluded from U, and why the exclusion is safe

A `Str` whose elements are not all Unicode scalar values constructs without
complaint, and `str-bytes` carries the element through unchanged:

```
$ ./oath/oath eval '(SCons -1 (SNil))'
(SCons -1 SNil) : Str
$ ./oath/oath eval '(SCons 55296 (SNil))'          # a surrogate
(SCons 55296 SNil) : Str
$ ./oath/oath eval '(str-bytes (SCons -1 (SNil)))'
(Cons -1 Nil) : (List Int)
$ ./oath/oath eval '(bytes-ok (str-bytes (SCons -1 (SNil))))'
false : Bool
```

`O` is undefined here, so these strings carry no pair and are outside U. **The
exclusion cannot smuggle a silent case out of the universe**, and that is a
consequence of the ranges rather than an assurance: every value in `0..255` IS a
scalar value, so a non-scalar element is necessarily outside `0..255`, so every
class D string fails `bytes-ok` exactly as class C does. Class B — the silent
class, the one the closure claim has to reach — contains no class D member by
construction.

The digest defect's own secrets arrive by ADMIT (`process_env`), which SPEC §3
requires to decode as UTF-8 or refuse, so they are scalar-valued before they
reach `str-bytes`. Class D is reachable only by constructing a `Str` in-language.

### The identical-value witness, which is what makes class B more than "similar"

Class B is not merely a class where two values differ while looking alike. **The
ranges of `C` and `O` INTERSECT**, and the intersection is inhabited by a
witness both readings claim:

    C("Ã©")  =  (Cons 195 (Cons 169 Nil))  =  O("é")

Decided in-language, by the kernel's own equality rather than by reading two
printed spines, with a control that must come back `false`:

```
$ ./oath/oath eval '(== (str-bytes "Ã©") (Cons [Int] 195 (Cons [Int] 169 (Nil [Int]))))'
true : Bool
$ ./oath/oath eval '(== (str-bytes "é")  (Cons [Int] 195 (Cons [Int] 169 (Nil [Int]))))'
false : Bool                                    # control — the instrument discriminates
```

So U contains pairs drawn from DISTINCT strings whose readings collide on a
single value: one `(List Int)` value is simultaneously the codepoint reading of
`"Ã©"` and the octet reading of `"é"`. This is the mojibake pair, arriving as an
equality rather than as an anecdote.

**Whether that intersection is fatal to a mechanism, harmless, or avoidable by
attaching the distinction somewhere other than the value is exactly what step 2
evaluates. It is NOT decided here, and this witness is not an argument.**

## World claims and tool claims

Kept separate because they read identically in a status line and only the first
kind survives the tool improving.

**WORLD — about the language and the values:**

- `C` and `O` are distinct functions into `(List Int)` — `C` total on `Str`, `O`
  defined only on scalar-valued `Str` — and on their common domain they agree
  exactly on class A.
- Every value in `0..255` is a Unicode scalar value, so class D is disjoint from
  classes A and B.
- `C` is the identity on the codepoint spine; no encoding happens (cited).
- The ranges of `C` and `O` intersect outside class A, witnessed by the `==`
  above.
- `(List Int)` is one type and both readings inhabit it; the pair, not either
  component, is what carries the defect.

**TOOL — about this kernel, this corpus, this run:**

- `error: byte list element out of range 0..255` is the **Go kernel's runtime
  range check** reached through `hmac-sha256`. It is not a type error and not a
  language-level distinction between the readings. Class C failing loudly is an
  IMPLEMENTATION LIMIT REPORTED, not a semantic fact: nothing in a class C value
  says "these are codepoints" — only that some element is outside `0..255`. The
  class C row of the digest transcript above is that error; `openssl` computed a
  digest for the same secret on the same message.

- `bytes-ok` is a predicate over VALUE RANGE, not over provenance, so it passes
  on all of class A and all of class B and fails on classes C and D alike. Its
  dangerous case is its pass:

  ```
  $ for s in 'hi' 'é' 'Ã©' 'ключ'; do ./oath/oath eval "(bytes-ok (str-bytes \"$s\"))"; done
  true : Bool          # class A
  true : Bool          # class B
  true : Bool          # class B — and this value is O("é")
  false : Bool         # class C
  ```
- `openssl` and `python3` are the stand-ins for `O`; the UTF-8 standard, not
  this repository, is what makes them the reference.
- Printed `(Cons …)` spines are `oath eval`'s rendering. The identical-value
  claim rests on the in-language `==`, not on the rendering.

## What step 1 does NOT establish

- Nothing about refinement types, their identity, `#69`, or `#133`'s obstacle.
- Nothing about whether class B is reachable in any deployment other than the
  webhook one already measured.
- **Nothing about the OTHER direction of the residue** — `bytes-str`
  reinterpreting a body as codepoints, `issue-159.md` §"A THIRD site". That
  universe is NOT derived here, and per the necessary-not-sufficient note at the top,
  U alone cannot support a closure verdict. Deriving it is the next step's first
  obligation, ahead of evaluating any mechanism.
- The universe above is derived from the DIGEST defect as scoped, so nothing
  here says whether the two directions need one mechanism or two.
