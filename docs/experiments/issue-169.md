# #169 — does a property over the existing types close each failure?

**What this file is:** #169's own falsifier, made runnable, and its output. It
is the check the issue asks for before any design work:

> **If every failure mode this conflation produces can be closed by a property
> over the existing types, then the distinction buys naming and not safety**,
> and this should be declined with that argument recorded.

**It proposes no type, chooses no design, and does not recommend a disposition.**
The falsifier was stated precisely enough to run, and running it produced a
sharper result than either answer: **#169's condition turns on what "closed"
means, and the three available readings do not agree.** Under the weakest the
condition is met; under the other two it is not, for all three failures rather
than just the byte/text one. Choosing between them is a judgment, not a
measurement, and it is not made here.

**Read parts 5 and 6 with parts 7 and 8.** Those two were written as closures
and are left in place because the way they FAIL is the result. Stated at the
precision that was actually measured, and the wording matters because it IS the
result: **for part 5's decoder contract, a third body — `utf8-decode-cheat` —
PASSES every generated check and earns `tested`, while still mojibaking.** It
does not SATISFY the properties: `multibyte-shrinks` is false of it at `©`, as
part 7 shows. Saying "satisfies" would assert the opposite of this file's
finding, which is that a property can be true, discriminating, and undischarged.
Part 8 does the same for the secret guard. Part 6's
`scoped-string` contract has NO such body here: `scoped-string-via-decode` calls
the checked decoder, so nothing in this file exhibits an extractor that passes
part 6 while producing the defect. That experiment was not run, and an earlier
version of this summary claimed it had been. Removing parts 5 and 6 would hide
the measurement that matters; overstating what was measured against them would
be the same defect the parts themselves are about.

Run on 2026-08-12 against `910e8a6`, with the binary built by `make build` and
z3 4.16.0 on PATH.

## What is inherited, and not re-derived

#159's partition of `V_CP` is used as given
(`docs/experiments/issue-159-refinements.md`); it is not re-measured here, and
that file is the authority for it. The partition splits on DIAGONAL MEMBERSHIP
first — a value is on the diagonal Δ exactly when **every** element is in
`0..255`, both bounds — and only then on element range:

| class | the value | `ρ`, the identity retag | observable? |
|---|---|---|---|
| **A** | `v ∈ Δ` and every element `< 0x80` | defined both ways | **no** — the two roles denote the same thing |
| **B** | `v ∈ Δ` and some element `>= 0x80` | defined both ways | **yes, silently** |
| **C** | `v ∉ Δ` — some element outside `0..255`, above it or below it | CP→OCT undefined | **yes, loudly — but by a tool** |

The lower bound is not decoration: `[-1]` is class C, not class A, and the
`utf8-valid` guard used throughout this file excludes it for the same reason.

The three failures the falsifier has to be pointed at are the three the
application actually produced, from `docs/experiments/webhook-friction.md` §3
and the receiver's own comments:

    F1  class C   a Cyrillic secret reaches `hmac-sha256`; the request dies
    F2  class B   a Latin-1 secret; the digest silently disagrees with the sender
    F3  class B   a UTF-8 repository name through `bytes-str`; mojibake in the log

**F3 is probed hardest**, because it is the case #169 names as the one that
weighs against declining: nothing raises, and no property was written because
nobody knew to write one.

## Method

Every definition below is put into a **scratch store**, with `--new`. Nothing
WRITES `codebase/` or `fixtures/`; `codebase/` is read once, to copy it. The
scratch store is a COPY of `codebase/` rather than an empty `mktemp -d`,
because the probes call corpus names — `bytes-str`, `str-bytes`, `bytes-ok`, `secret-is-usable` — which an
empty store cannot resolve.

```sh
SP=$(mktemp -d)
cp -R codebase "$SP/store"
OATH_STORE="$SP/store" ./oath/oath put <file>.oath --new
```

One consequence worth stating rather than leaving implicit: the new-binding
guard keys on the canonical store's resolved-path IDENTITY, so a copy at a
scratch path is not the store it protects and the guard does not fire here.
`--new` is passed anyway.

`git status --porcelain codebase/ fixtures/` was empty after the runs below.

## Part 1 — naming the byte domain

The discriminating check in part 4 cannot be STATED without a name for "this
`(List Int)` is UTF-8", which is why the domain is declared first and measured
in its own right before anything is asked of it.

```lisp
; --- THE NAMED BYTE DOMAIN ---------------------------------------------------
; Without a name for "this (List Int) is UTF-8", no property can quantify over
; the set where the codepoint/byte confusion is observable.

(defn utf8-cont [] [(b Int)] Bool
  (and (<= 128 b) (<= b 191))
  (prop accepts-a-continuation [] (utf8-cont 186))
  (prop rejects-a-lead [] (not (utf8-cont 208)))
  (prop rejects-ascii [] (not (utf8-cont 65))))

(defn utf8-valid [] [(bs (List Int))] Bool
  (match bs
    ((Nil) true)
    ((Cons b1 r1)
      (if (and (<= 0 b1) (<= b1 127))
          (utf8-valid r1)
          (if (and (<= 194 b1) (<= b1 223))
              (match r1
                ((Nil) false)
                ((Cons b2 r2) (if (utf8-cont b2) (utf8-valid r2) false)))
              (if (and (<= 224 b1) (<= b1 239))
                  (match r1
                    ((Nil) false)
                    ((Cons b2 r2)
                      (match r2
                        ((Nil) false)
                        ((Cons b3 r3)
                          (if (and (utf8-cont b2) (utf8-cont b3))
                              (utf8-valid r3)
                              false)))))
                  (if (and (<= 240 b1) (<= b1 244))
                      (match r1
                        ((Nil) false)
                        ((Cons b2 r2)
                          (match r2
                            ((Nil) false)
                            ((Cons b3 r3)
                              (match r3
                                ((Nil) false)
                                ((Cons b4 r4)
                                  (if (and (and (utf8-cont b2) (utf8-cont b3)) (utf8-cont b4))
                                      (utf8-valid r4)
                                      false)))))))
                      false))))))
  (prop empty-is-valid [] (utf8-valid (Nil [Int])))
  (prop ascii-is-valid [] (utf8-valid (str-bytes "hi")))
  (prop a-lone-continuation-is-not [] (not (utf8-valid (Cons [Int] 186 (Nil [Int])))))
  (prop a-truncated-lead-is-not [] (not (utf8-valid (Cons [Int] 208 (Nil [Int])))))
  (prop cyrillic-ka-is-valid []
    (utf8-valid (Cons [Int] 208 (Cons [Int] 186 (Nil [Int])))))
  (prop e-acute-is-valid []
    (utf8-valid (Cons [Int] 195 (Cons [Int] 169 (Nil [Int]))))))

(defn utf8-multibyte [] [(bs (List Int))] Bool
  (any [Int] (fn [(b Int)] (<= 128 b)) bs)
  (prop ascii-is-not-multibyte [] (not (utf8-multibyte (str-bytes "hi"))))
  (prop cyrillic-ka-is-multibyte []
    (utf8-multibyte (Cons [Int] 208 (Cons [Int] 186 (Nil [Int]))))))
```

```
✓ utf8-cont        #9c85fd7773d2  tested (200 cases per property) · total
✓ utf8-valid       #dc235b7c0c3d  tested (200 cases per property) · total
✓ utf8-multibyte   #a347c183c560  tested (200 cases per property) · total
```

All eleven properties passed. **What `utf8-valid` does NOT do:** it accepts
surrogate encodings (`ED A0 80`) and encodings above U+10FFFF reachable through
`F4`, because it checks lead-byte class and continuation-byte class only. Those
are not on any path this file measures, and no claim below depends on them.

## Part 2 — the instruments

A check that falsifies `bytes-str` establishes nothing until something is seen
to satisfy it, so a real encoder and decoder are present purely as controls.

```lisp
; --- INSTRUMENTS -------------------------------------------------------------
; A real UTF-8 encoder and decoder, present ONLY as controls: a check that
; falsifies `bytes-str` establishes nothing until something is seen to satisfy
; it. Both cover U+0000..U+07FF (one- and two-byte forms) and no further.

(defn utf8-encode [] [(s Str)] (List Int)
  (match s
    ((SNil) (Nil [Int]))
    ((SCons c rest)
      (if (and (<= 0 c) (<= c 127))
          (Cons [Int] c (utf8-encode rest))
          (if (and (<= 128 c) (<= c 2047))
              (Cons [Int] (+ 192 (e-div c 64))
                (Cons [Int] (+ 128 (e-mod c 64)) (utf8-encode rest)))
              (Cons [Int] 63 (utf8-encode rest))))))
  (prop empty-encodes-empty [] (== (utf8-encode (SNil)) (Nil [Int])))
  (prop ascii-is-the-identity [(s Str)]
    (if (all [Int] (fn [(c Int)] (and (<= 0 c) (<= c 127))) (str-bytes s))
        (== (utf8-encode s) (str-bytes s))
        true))
  (prop e-acute-is-two-bytes []
    (== (utf8-encode (SCons 233 (SNil)))
        (Cons [Int] 195 (Cons [Int] 169 (Nil [Int])))))
  (prop cyrillic-ka-is-two-bytes []
    (== (utf8-encode (SCons 1082 (SNil)))
        (Cons [Int] 208 (Cons [Int] 186 (Nil [Int]))))))

(defn utf8-decode [] [(bs (List Int))] Str
  (match bs
    ((Nil) (SNil))
    ((Cons b1 r1)
      (if (and (<= 0 b1) (<= b1 127))
          (SCons b1 (utf8-decode r1))
          (if (and (<= 194 b1) (<= b1 223))
              (match r1
                ((Nil) (SNil))
                ((Cons b2 r2)
                  (if (utf8-cont b2)
                      (SCons (+ (* 64 (- b1 192)) (- b2 128)) (utf8-decode r2))
                      (SNil))))
              (SNil)))))
  (prop empty-decodes-empty [] (== (utf8-decode (Nil [Int])) (SNil)))
  (prop decodes-cyrillic-ka []
    (== (utf8-decode (Cons [Int] 208 (Cons [Int] 186 (Nil [Int])))) (SCons 1082 (SNil))))
  (prop decodes-e-acute []
    (== (utf8-decode (Cons [Int] 195 (Cons [Int] 169 (Nil [Int])))) (SCons 233 (SNil))))
  (prop inverts-encode-in-range [(s Str)]
    (if (all [Int] (fn [(c Int)] (and (<= 0 c) (<= c 2047))) (str-bytes s))
        (== (utf8-decode (utf8-encode s)) s)
        true)))
```

```
✓ utf8-encode      #d4600ce750b2  tested (200 cases per property) · total
✓ utf8-decode      #c14be5b0386a  tested (200 cases per property) · total
```

**Both cover U+0000..U+07FF and no further.** Three- and four-byte forms are
outside them; `utf8-decode` returns the empty `Str` on a lead byte it does not
handle. Every witness used below is inside that range (`é` is U+00E9, `к` is
U+043A), and nothing here is a proposal for a standard-library codec.

**An external oracle, so the instruments are not merely agreeing with
themselves.** `openssl` signs the shell's bytes, which is what GitHub signs:

```
$ printf '%s' '{}' | openssl dgst -sha256 -hmac 'éééééééééééééééééééééééééééééééé' -r
80de556a7ea0eefbbe1476f4b95172c4dcbedd091207fb1a3e8110d597b55348 *stdin

$ oath eval '(hex-encode (hmac-sha256 (utf8-encode "éééééééééééééééééééééééééééééééé") (str-bytes "{}")))'
80de556a7ea0eefbbe1476f4b95172c4dcbedd091207fb1a3e8110d597b55348

$ oath eval '(hex-encode (hmac-sha256 (str-bytes "éééééééééééééééééééééééééééééééé") (str-bytes "{}")))'
cac69f8e3e4f47f5909c3a588614e8bbc041da381829613f6d25861ac76a50d6
```

(`oath eval` prints the digest as an `SCons` chain; it is shown here as text.
The secret is thirty-two `é`, spelled out in full because an abbreviation would
not reproduce — and an elided `…` is itself a class-C codepoint.)

`utf8-encode` reproduces the sender's digest byte for byte; `str-bytes` does
not. F2 is therefore a measured disagreement with a party outside this
repository, not a claim about a function this file wrote.

## Part 3 — the secret side, F1 and F2

Three guards: the one in place when the receiver crashed, the obvious repair for
that crash, and the one that shipped.

```lisp
; --- THE SECRET SIDE ---------------------------------------------------------
; Three guards: the one that was there when the receiver crashed, the obvious
; repair for that crash, and the one that shipped.

(defn guard-length-only [] [(s Str)] Bool
  (<= 16 (str-len s))
  (prop admits-the-cyrillic-secret [] (guard-length-only "ключключключключключключключключ")))

(defn guard-byte-range [] [(s Str)] Bool
  (and (<= 16 (str-len s)) (bytes-ok (str-bytes s)))
  (prop refuses-the-cyrillic-secret []
    (not (guard-byte-range "ключключключключключключключключ")))
  (prop admits-the-latin1-secret []
    (guard-byte-range "éééééééééééééééééééééééééééééééé")))

; THE CLAIM a guard has to make good: the bytes this receiver signs are the
; bytes the sender signed. The sender signs the environment variable's UTF-8.
(defn signs-the-senders-bytes [] [(s Str)] Bool
  (== (str-bytes s) (utf8-encode s))
  (prop holds-on-ascii [] (signs-the-senders-bytes "abcdef0123456789"))
  (prop fails-on-latin1 [] (not (signs-the-senders-bytes "éééééééééééééééééééééééééééééééé")))
  (prop fails-on-cyrillic [] (not (signs-the-senders-bytes "ключключключключключключключключ"))))

(defn faithful-under-length-only [] [(s Str)] Bool
  (if (guard-length-only s) (signs-the-senders-bytes s) true)
  (prop holds [(s Str)] (faithful-under-length-only s)))

(defn faithful-under-byte-range [] [(s Str)] Bool
  (if (guard-byte-range s) (signs-the-senders-bytes s) true)
  (prop holds [(s Str)] (faithful-under-byte-range s)))

(defn faithful-under-shipped [] [(s Str)] Bool
  (if (secret-is-usable s) (signs-the-senders-bytes s) true)
  (prop holds [(s Str)] (faithful-under-shipped s)))

; The same three, at the two named secrets, because the generator never
; produces a codepoint above 127.
(defn witness-length-only-latin1 [] [] Bool
  (faithful-under-length-only "éééééééééééééééééééééééééééééééé")
  (prop the-check-fires [] (not (witness-length-only-latin1))))

(defn witness-byte-range-latin1 [] [] Bool
  (faithful-under-byte-range "éééééééééééééééééééééééééééééééé")
  (prop the-check-still-fires [] (not (witness-byte-range-latin1))))

(defn witness-shipped-latin1 [] [] Bool
  (faithful-under-shipped "éééééééééééééééééééééééééééééééé")
  (prop the-check-does-not-fire [] (witness-shipped-latin1)))

(defn witness-shipped-cyrillic [] [] Bool
  (faithful-under-shipped "ключключключключключключключключ")
  (prop the-check-does-not-fire [] (witness-shipped-cyrillic)))

; THE PRICE of the guard that closes both: it also refuses a legitimate secret
; that the sender and this receiver would in fact agree on, whenever the
; operator's secret is not ASCII. Nothing here says that price is wrong; it is
; recorded because it is what "closed by a property" cost.
(defn shipped-refuses-a-non-ascii-secret [] [] Bool
  (secret-is-usable "ключключключключключключключключ")
  (prop refused [] (not (shipped-refuses-a-non-ascii-secret))))
```

```
✓ guard-length-only #8e264b88e845  tested · total
✓ guard-byte-range #f43f4506db6e  tested · total
✓ signs-the-senders-bytes #ee46e33fd99c  tested · total
✓ faithful-under-length-only #437c1864cba2  tested · total
✓ faithful-under-byte-range #0537d3f853ab  tested · total
✓ faithful-under-shipped #274d7b9a66cb  tested · total
✓ witness-length-only-latin1 #c54b34e33a84  tested · total
✓ witness-byte-range-latin1 #96866a30c72f  tested · total
✓ witness-shipped-latin1 #c2d0e0e5a1c2  tested · total
✓ witness-shipped-cyrillic #716e9b6465d1  tested · total
✓ shipped-refuses-a-non-ascii-secret #fb615fb1184f  tested · total
```

Every property passed — including the ones asserting a check FIRES
(`the-check-fires`, `the-check-still-fires`, `refused`) and the ones asserting
it does not (`the-check-does-not-fire`), which is the two-way control. Read as a
ladder, with each row a MUTATION of the row below it:

| guard | F1 (class C) | F2 (class B) | what it costs |
|---|---|---|---|
| length only | **open** — the crash | **open** | nothing |
| `+ bytes-ok (str-bytes s)` | closed | **open** — `witness-byte-range-latin1` fires | nothing |
| `+ printable ASCII` (shipped) | closed | closed | refuses every non-ASCII secret |

**The middle row is the mutation that matters.** It is the repair the crash
prompts — the error message names the byte range — and it closes F1 while
leaving F2 exactly where it was. The measurement:

```
$ oath eval '(bytes-ok (str-bytes "ключ"))'                    false : Bool
$ oath eval '(bytes-ok (str-bytes "éééé"))'                    true  : Bool
$ oath eval '(hex-encode (hmac-sha256 (str-bytes "ключ") (str-bytes "{}")))'
                                                error: byte list element out of range 0..255
$ oath eval '(== (str-bytes "éééé")  (utf8-encode "éééé"))'    false : Bool
$ oath eval '(== (str-bytes "hello") (utf8-encode "hello"))'   true  : Bool
```

So a property over the existing types catches both F1 and F2 as they actually
occurred, and it catches them the same way: by narrowing the admitted set to
class A, where `ρ` is unobservable. `shipped-refuses-a-non-ascii-secret` records
the price. Whether that price is acceptable is not measured here.

**Do not read this as "closed" without part 8**, which applies the same
adversarial-body test used on F3 to this guard and finds a body that PASSES all
five of these generated checks while admitting a class-B secret. It does not
satisfy the properties — `usable-encodes` is false of it above 255 — and the gap
between passing and satisfying is this file's result rather than a wording
preference.

## Part 4 — F3, the mojibake, probed hardest

`bytes-str` reinterprets. Directly:

```
$ oath eval '(length [Int] (utf8-encode "ключ"))'          8 : Int
$ oath eval '(str-len (bytes-str  (utf8-encode "ключ")))'  8 : Int
$ oath eval '(str-len (utf8-decode (utf8-encode "ключ")))' 4 : Int
$ oath eval '(bytes-str (utf8-encode "ключ"))'             ÐºÐ»ÑŽÑ‡
```

(The last line is the `Str` rendered as text; `oath eval` prints an `SCons`
chain and the rendering was done outside the kernel.)

### The check matrix

```lisp
; --- THE CHECKS --------------------------------------------------------------
; Each def carries exactly one universal property; its VERDICT is the
; measurement. Names say which class of #159's partition the guard admits.

; A1 — class A control. The two roles denote the same thing, so the wrong
; function and the right one are indistinguishable. This is why the defect ships.
(defn class-a-agree [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (not (utf8-multibyte v)))
      (== (bytes-str v) (utf8-decode v))
      true)
  (prop holds [(v (List Int))] (class-a-agree v)))

; B1 — class B, stated against a REFERENCE decoder.
(defn class-b-agree [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (utf8-multibyte v))
      (== (bytes-str v) (utf8-decode v))
      true)
  (prop holds [(v (List Int))] (class-b-agree v)))

; B2 — class B, stated with NO reference decoder: a multibyte sequence must
; decode to fewer codepoints than it has bytes. Expressible only because the
; byte domain has a name.
(defn class-b-shrinks [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (utf8-multibyte v))
      (< (str-len (bytes-str v)) (length [Int] v))
      true)
  (prop holds [(v (List Int))] (class-b-shrinks v)))

; B2' — the same check pointed at the correct decoder. Without this the
; falsification of B2 would establish nothing: a check nothing satisfies is
; not a detector.
(defn class-b-shrinks-decode [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (utf8-multibyte v))
      (< (str-len (utf8-decode v)) (length [Int] v))
      true)
  (prop holds [(v (List Int))] (class-b-shrinks-decode v)))

; M1 — MUTATION of B2: delete the named domain from the guard. The check must
; stop discriminating.
(defn unguarded-shrinks [] [(v (List Int))] Bool
  (< (str-len (bytes-str v)) (length [Int] v))
  (prop holds [(v (List Int))] (unguarded-shrinks v)))

(defn unguarded-shrinks-decode [] [(v (List Int))] Bool
  (< (str-len (utf8-decode v)) (length [Int] v))
  (prop holds [(v (List Int))] (unguarded-shrinks-decode v)))

; M2 — the property the corpus ACTUALLY states about `bytes-str`, and the same
; property pointed at the correct decoder.
(defn corpus-roundtrip-reinterpret [] [(s Str)] Bool
  (== (bytes-str (str-bytes s)) s)
  (prop holds [(s Str)] (corpus-roundtrip-reinterpret s)))

(defn corpus-roundtrip-decode [] [(s Str)] Bool
  (== (utf8-decode (str-bytes s)) s)
  (prop holds [(s Str)] (corpus-roundtrip-decode s)))
```

Two of these are stated against a REFERENCE decoder (`class-a-agree`,
`class-b-agree`) and one is not (`class-b-shrinks`) — the difference matters,
because a check needing a correct decoder to exist is not available to someone
who has not noticed they need one. `class-b-shrinks` needs only the named
domain: a valid multibyte sequence must decode to fewer codepoints than it has
bytes, and that is checkable with no reference implementation anywhere.

```
✓ class-a-agree    #6e07a7569b1b  passed 200 cases
✓ class-b-agree    #dec0387d6057  passed 200 cases
✓ class-b-shrinks  #7fe9fd31e492  passed 200 cases
✓ class-b-shrinks-decode #9add47a7b8c0  passed 200 cases
✗ unguarded-shrinks #943dd60dd194  FALSIFIED: holds     counterexample: Nil
✗ unguarded-shrinks-decode #d1ac79f3988c  FALSIFIED: holds  counterexample: Nil
✓ corpus-roundtrip-reinterpret #5dc3c37ac5c5  passed 200 cases
✗ corpus-roundtrip-decode #6fd425cbeac7  FALSIFIED after 4 cases
      counterexample: (SCons 11 (SCons -1 (SCons 20 (SCons -2 SNil))))
```

**Four of those readings are wrong if taken at face value** — the three class-B
passes and the one falsification — **and the next section is why.**

### Calibrating the instrument before reading it

The class-B rows passed. Before that is read as "the check does not fire", the
question is whether the generated-case tester ever ENTERS the domain those
checks quantify over. Each property below asserts the domain is NEVER reached,
so **passing means never**:

```lisp
; --- INSTRUMENT CALIBRATION --------------------------------------------------
; Does the generated-case tester ever ENTER the domain the class-B checks
; quantify over? If not, those checks passed vacuously and say nothing.
; Each property asserts the domain is NEVER reached; PASSING means never.

(defn generator-never-makes-a-high-codepoint [] [(s Str)] Bool
  (not (any [Int] (fn [(c Int)] (<= 128 c)) (str-bytes s)))
  (prop holds [(s Str)] (generator-never-makes-a-high-codepoint s)))

(defn generator-never-makes-a-high-byte [] [(v (List Int))] Bool
  (not (any [Int] (fn [(b Int)] (<= 128 b)) v))
  (prop holds [(v (List Int))] (generator-never-makes-a-high-byte v)))

(defn generator-never-enters-class-b [] [(v (List Int))] Bool
  (not (and (utf8-valid v) (utf8-multibyte v)))
  (prop holds [(v (List Int))] (generator-never-enters-class-b v)))

(defn generator-never-makes-a-valid-nonempty-utf8 [] [(v (List Int))] Bool
  (not (and (utf8-valid v) (not (== v (Nil [Int])))))
  (prop holds [(v (List Int))] (generator-never-makes-a-valid-nonempty-utf8 v)))
```

```
✓ generator-never-makes-a-high-codepoint #8e2ed599e2c0  passed 200 cases
✓ generator-never-makes-a-high-byte #41f0df595d0a  passed 200 cases
✓ generator-never-enters-class-b #9496f238f8ca  passed 200 cases
✗ generator-never-makes-a-valid-nonempty-utf8 #9b24340cb306  FALSIFIED after 5 cases
      counterexample: (Cons 1 Nil)
```

**The generated-case tester is structurally blind to class B.** Over 200 cases
it produced no codepoint and no byte at or above 128. The fourth row is the
control that keeps this from being a statement about a broken guard: the tester
DOES reach `utf8-valid` — it is only the multibyte half it never reaches. So
every class-B row above passed **vacuously**, and the same is true of the three
`faithful-under-*` rows in part 3.

`corpus-roundtrip-decode`'s falsification is real but is also not the reading it
invites: its counterexample contains `-1`, which is class C, so it witnesses
non-scalar junk rather than mojibake.

**The fourth calibration row is what keeps `class-a-agree` out of that list.**
The tester DOES generate valid non-empty UTF-8; it is only the multibyte half it
never reaches. So the class-A control passed over values actually visited, and
its pass is evidence rather than silence — which is exactly the asymmetry the
issue turns on: the corpus's default evidence route covers the class where the
confusion is unobservable and misses the class where it is not.

### The witnesses, chosen rather than generated

```lisp
; --- WITNESSES, chosen rather than generated ---------------------------------
; The generated-case tester never enters class B, so every class-B verdict here
; is pinned to a named value instead. `(utf8-encode "ключ")` is the eight bytes
; GitHub would send for a Cyrillic repository name.

(defn ka-bytes [] [] (List Int)
  (utf8-encode "ключ")
  (prop is-eight-bytes [] (== (length [Int] (ka-bytes)) 8))
  (prop is-valid-utf8 [] (utf8-valid (ka-bytes)))
  (prop is-class-b [] (utf8-multibyte (ka-bytes))))

; The class-B checks, evaluated AT the witness rather than over the generator.
(defn witness-class-b-agree [] [] Bool
  (class-b-agree (ka-bytes))
  (prop the-check-fires [] (not (witness-class-b-agree))))

(defn witness-class-b-shrinks [] [] Bool
  (class-b-shrinks (ka-bytes))
  (prop the-check-fires [] (not (witness-class-b-shrinks))))

(defn witness-class-b-shrinks-decode [] [] Bool
  (class-b-shrinks-decode (ka-bytes))
  (prop the-check-is-satisfiable [] (witness-class-b-shrinks-decode)))

(defn witness-class-a-agree [] [] Bool
  (class-a-agree (str-bytes "hi"))
  (prop the-check-does-not-fire [] (witness-class-a-agree)))

; The corpus round-trip property, at a witness rather than at generator junk.
(defn witness-corpus-roundtrip-reinterpret [] [] Bool
  (corpus-roundtrip-reinterpret "ключ")
  (prop passes-for-the-wrong-function [] (witness-corpus-roundtrip-reinterpret)))

(defn witness-corpus-roundtrip-decode [] [] Bool
  (== (utf8-decode (str-bytes "ключ")) "ключ")
  (prop fails-for-the-right-function [] (not (witness-corpus-roundtrip-decode))))

; --- MUTATION of the class-B check: drop ONLY the multibyte half of the domain.
; The check must stop discriminating — it now condemns class A, where the two
; functions genuinely agree.
(defn valid-only-shrinks [] [(v (List Int))] Bool
  (if (utf8-valid v) (< (str-len (bytes-str v)) (length [Int] v)) true)
  (prop holds [(v (List Int))] (valid-only-shrinks v)))

(defn valid-only-shrinks-decode [] [(v (List Int))] Bool
  (if (utf8-valid v) (< (str-len (utf8-decode v)) (length [Int] v)) true)
  (prop holds [(v (List Int))] (valid-only-shrinks-decode v)))

; The mutation, pinned at a class-A witness. `Nil` already falsifies it
; degenerately (0 < 0); this shows the mutant condemns ordinary ASCII text,
; which is behaviour both functions get right.
(defn witness-valid-only-shrinks [] [] Bool
  (valid-only-shrinks (str-bytes "hi"))
  (prop the-mutant-condemns-class-a [] (not (witness-valid-only-shrinks))))

(defn witness-valid-only-shrinks-decode [] [] Bool
  (valid-only-shrinks-decode (str-bytes "hi"))
  (prop the-mutant-condemns-class-a [] (not (witness-valid-only-shrinks-decode))))

; And the unguarded mutation at the same witness, for the same reason.
(defn witness-unguarded-shrinks [] [] Bool
  (unguarded-shrinks (str-bytes "hi"))
  (prop the-mutant-condemns-class-a [] (not (witness-unguarded-shrinks))))
```

```
✓ ka-bytes         #26029c2d8e9a  is-eight-bytes, is-valid-utf8, is-class-b — all passed
✓ witness-class-b-agree #707727c3e3bf  the-check-fires                passed
✓ witness-class-b-shrinks #80801852b760  the-check-fires              passed
✓ witness-class-b-shrinks-decode #cb712e45e300  the-check-is-satisfiable passed
✓ witness-class-a-agree #eb204ad16735  the-check-does-not-fire        passed
✓ witness-corpus-roundtrip-reinterpret #cfbaccc256af  passes-for-the-wrong-function passed
✓ witness-corpus-roundtrip-decode #5f8bba0048e5  fails-for-the-right-function passed
✗ valid-only-shrinks #823e71e66c1c  FALSIFIED: holds   counterexample: Nil
✗ valid-only-shrinks-decode #beb14d3d6ae7  FALSIFIED: holds  counterexample: Nil
✓ witness-valid-only-shrinks #2ab3da7cb9f4  the-mutant-condemns-class-a passed
✓ witness-valid-only-shrinks-decode #bdc591d76aaf  the-mutant-condemns-class-a passed
✓ witness-unguarded-shrinks #76ef0ca1c152  the-mutant-condemns-class-a passed
```

At the witness, every check discriminates in both directions:

```
$ oath eval '(class-b-shrinks        (utf8-encode "ключ"))'  false : Bool
$ oath eval '(class-b-shrinks-decode (utf8-encode "ключ"))'  true  : Bool
$ oath eval '(class-b-agree          (utf8-encode "ключ"))'  false : Bool
$ oath eval '(class-a-agree          (str-bytes "hi"))'      true  : Bool
```

### The mutations the check has to survive

Each row deletes something from `class-b-shrinks` and must make it stop
discriminating. Both do:

| mutation | at `(utf8-encode "ключ")` | at `(str-bytes "hi")` — class A |
|---|---|---|
| none | fires on `bytes-str`, silent on `utf8-decode` | silent on both |
| drop `utf8-multibyte` | fires | **fires — condemns correct behaviour** |
| drop the whole domain | fires | **fires — condemns correct behaviour** |

Every cell measured, `false` meaning the check fires:

```
$ oath eval '(class-b-shrinks           (utf8-encode "ключ"))'  false : Bool
$ oath eval '(class-b-shrinks-decode    (utf8-encode "ключ"))'  true  : Bool
$ oath eval '(class-b-shrinks           (str-bytes "hi"))'      true  : Bool
$ oath eval '(class-b-shrinks-decode    (str-bytes "hi"))'      true  : Bool
$ oath eval '(valid-only-shrinks        (utf8-encode "ключ"))'  false : Bool
$ oath eval '(valid-only-shrinks        (str-bytes "hi"))'      false : Bool
$ oath eval '(valid-only-shrinks-decode (str-bytes "hi"))'      false : Bool
$ oath eval '(unguarded-shrinks         (utf8-encode "ключ"))'  false : Bool
$ oath eval '(unguarded-shrinks         (str-bytes "hi"))'      false : Bool
$ oath eval '(unguarded-shrinks-decode  (str-bytes "hi"))'      false : Bool
```

Both mutants are also falsified at `Nil` (`0 < 0`), which is degenerate and not
the demonstration; the class-A witness is. **The named domain is what makes the
check a mojibake detector rather than a constant `false`.**

### What the corpus actually says about `bytes-str` today

The receiver states one property of it, `inverts-str-bytes`, and the measurement
is that this property **selects the wrong function**:

```
✓ corpus-roundtrip-reinterpret  passes  — the reinterpretation satisfies it
✓ witness-corpus-roundtrip-decode: fails-for-the-right-function  passed
      i.e. (== (utf8-decode (str-bytes "ключ")) "ключ")  is  false
```

A correct decoder does not satisfy it, because `str-bytes` yields codepoints and
a decoder expects UTF-8. And the spec-strength instrument agrees with the wrong
function too:

```
$ oath mutate bytes-str
✓ killed    match collapsed to arm 0 by inverts-str-bytes
generated mutation score: 1/1 mutants killed
```

**A perfect score on the function whose defect is the subject of the issue.**
That is the documented reading of that number — it measures the generator's
reach, not what the specification excludes — arriving on the case that produced
the issue.

### Whether proof reaches it

`class-b-*` use `any`, whose `lam` is outside the provable fragment — the first
three proof attempts returned `"lam" terms are outside the provable fragment`
and nothing else. They were re-spelled with a direct recursion and put again:

```lisp
; A lambda-free spelling of the multibyte half of the domain: `any` takes a
; `lam`, which the prover refuses, so the class-B checks above could only ever
; be TESTED. Same predicate, written as a direct recursion.
(defn utf8-high [] [(bs (List Int))] Bool
  (match bs
    ((Nil) false)
    ((Cons b r) (if (<= 128 b) true (utf8-high r))))
  (prop ascii-has-no-high-byte [] (not (utf8-high (str-bytes "hi"))))
  (prop ka-has-a-high-byte [] (utf8-high (utf8-encode "ключ")))
  (prop agrees-with-utf8-multibyte [(v (List Int))]
    (== (utf8-high v) (utf8-multibyte v))))

(defn class-b-shrinks-p [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (utf8-high v))
      (< (str-len (bytes-str v)) (length [Int] v))
      true)
  (prop holds [(v (List Int))] (class-b-shrinks-p v)))

(defn class-b-shrinks-decode-p [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (utf8-high v))
      (< (str-len (utf8-decode v)) (length [Int] v))
      true)
  (prop holds [(v (List Int))] (class-b-shrinks-decode-p v)))

(defn class-b-agree-p [] [(v (List Int))] Bool
  (if (and (utf8-valid v) (utf8-high v))
      (== (bytes-str v) (utf8-decode v))
      true)
  (prop holds [(v (List Int))] (class-b-agree-p v)))
```

```
✓ utf8-high        #b442ff0fa754  tested (200 cases per property) · total
✓ class-b-shrinks-p #ea611de78ae7  tested (200 cases per property) · total
✓ class-b-shrinks-decode-p #6f150fad1813  tested (200 cases per property) · total
✓ class-b-agree-p  #20b3f888c344  tested (200 cases per property) · total
```

**`agrees-with-utf8-multibyte` passing 200 cases is worth almost nothing here**,
for the reason established above: those 200 cases contain no byte at or above
128, so the two spellings were compared only where both are trivially `false`.
What holds them together at the witness is the pair `ka-has-a-high-byte` and
`ka-bytes`'s `is-class-b`, which are the same value under the two predicates.

```
$ oath prove class-b-shrinks-p         · unproven  no direct proof; induction did not discharge
$ oath prove class-b-shrinks-decode-p  · unproven  no direct proof; induction did not discharge
$ oath prove class-b-agree-p           · unproven  no direct proof; induction did not discharge
```

**This is an implementation limit, not a semantic fact.** Unproven is not
disproof, and nothing follows from it about whether the properties hold.

## Part 5 — F3 from detectable to excluded (and part 7 shows this is not closure)

Parts 1–4 show a property over the existing types DETECTS the mojibake. This
part asks for more: that a definition claiming to decode cannot BE the
reinterpretation and still be accepted. It was written as a closure and it is
not one — **part 7 exhibits a third body that PASSES every generated check here
and
still mojibakes.** The result stands as an exclusion of one body; read it that
way, and read part 7 before drawing anything from it.

```lisp
; --- THE CHECKED DECODER -----------------------------------------------------
; One contract, stated over the named domain plus explicit class-B witnesses,
; and TWO bodies under it: the real decoder, and the reinterpretation. The
; contract is character-for-character the same in both; only the body differs.

(defn utf8-decode-checked [] [(bs (List Int))] Str
  (utf8-decode bs)

  ; 1. THE DOMAIN PROPERTY — universally quantified, needs no witness and no
  ;    reference implementation.
  (prop multibyte-shrinks [(v (List Int))]
    (if (and (utf8-valid v) (utf8-high v))
        (< (str-len (utf8-decode-checked v)) (length [Int] v))
        true))
  ; 2. THE CLASS-A OBLIGATION — on the diagonal below 0x80 the decode is the
  ;    identity on codepoints, which is what makes the defect invisible there.
  (prop ascii-is-reinterpretation [(v (List Int))]
    (if (and (utf8-valid v) (not (utf8-high v)))
        (== (utf8-decode-checked v) (bytes-str v))
        true))
  ; 3. CLASS-B WITNESSES — required because the generator never enters class B.
  (prop decodes-cyrillic [] (== (utf8-decode-checked (utf8-encode "ключ")) "ключ"))
  (prop decodes-e-acute [] (== (utf8-decode-checked (utf8-encode "é")) "é"))
  (prop decodes-a-mixed-string []
    (== (utf8-decode-checked (utf8-encode "a-é-ключ")) "a-é-ключ"))
  ; 4. ROUND-TRIP against the encoder, over the range both cover.
  (prop inverts-the-encoder [(s Str)]
    (if (all [Int] (fn [(c Int)] (and (<= 0 c) (<= c 2047))) (str-bytes s))
        (== (utf8-decode-checked (utf8-encode s)) s)
        true)))

; THE MUTANT: same contract, body replaced by the reinterpretation.
(defn utf8-decode-mutant [] [(bs (List Int))] Str
  (bytes-str bs)

  (prop multibyte-shrinks [(v (List Int))]
    (if (and (utf8-valid v) (utf8-high v))
        (< (str-len (utf8-decode-mutant v)) (length [Int] v))
        true))
  (prop ascii-is-reinterpretation [(v (List Int))]
    (if (and (utf8-valid v) (not (utf8-high v)))
        (== (utf8-decode-mutant v) (bytes-str v))
        true))
  (prop decodes-cyrillic [] (== (utf8-decode-mutant (utf8-encode "ключ")) "ключ"))
  (prop decodes-e-acute [] (== (utf8-decode-mutant (utf8-encode "é")) "é"))
  (prop decodes-a-mixed-string []
    (== (utf8-decode-mutant (utf8-encode "a-é-ключ")) "a-é-ключ"))
  (prop inverts-the-encoder [(s Str)]
    (if (all [Int] (fn [(c Int)] (and (<= 0 c) (<= c 2047))) (str-bytes s))
        (== (utf8-decode-mutant (utf8-encode s)) s)
        true)))
```

The two differ in the body and in nothing else — the six properties are
character-for-character identical.

```
✓ utf8-decode-checked #da5b44170f96  tested (200 cases per property) · total
    prop multibyte-shrinks        passed 200 cases
    prop ascii-is-reinterpretation passed 200 cases
    prop decodes-cyrillic         passed 200 cases
    prop decodes-e-acute          passed 200 cases
    prop decodes-a-mixed-string   passed 200 cases
    prop inverts-the-encoder      passed 200 cases

✗ utf8-decode-mutant #f6a710fe0868
    FALSIFIED: decodes-cyrillic, decodes-e-acute, decodes-a-mixed-string · total
    prop multibyte-shrinks        passed 200 cases
    prop ascii-is-reinterpretation passed 200 cases
    prop decodes-cyrillic         FALSIFIED after 0 cases
    prop decodes-e-acute          FALSIFIED after 0 cases
    prop decodes-a-mixed-string   FALSIFIED after 0 cases
    prop inverts-the-encoder      passed 200 cases
```

### `put`'s exit status is NOT the refusal, and a first draft of this section said it was

Split into two files, the good body puts at 0 and the mutant at 2:

```
$ oath put wrapper-good.oath   --new ; echo $?      0
$ oath put wrapper-mutant.oath --new ; echo $?      2
```

**That is a warning, not a refusal**, and reading it as one was an overclaim
caught in review. `oath/store.go` says so at the line that implements it —
*"FALSIFIED ENTRIES REPOINT … falsification is an honest recorded verdict, not a
rejection"* — and the behaviour is measured rather than read. Binding the good
body to a name and then submitting the mutant under **that same name**, with no
policy in the store:

```
$ oath put good.oath --new
$ python3 -c '...names.json...'                the-decoder -> da5b44170f96

$ oath put mut.oath ; echo $?                  2
✗ the-decoder #f6a710fe0868  FALSIFIED: decodes-cyrillic, …
    (name repointed; old version da5b44170f96 remains immutable)
$ python3 -c '...names.json...'                the-decoder -> f6a710fe0868
```

**The falsified body took the name.** Exit 2 told automation something was
wrong and stopped nothing.

### What DOES refuse it, and it already exists

`<store>/policy.json` with `forbid_falsified`. Same submission, same name, same
bodies — the only change is four lines of policy:

```json
{ "rules": [ { "names": ["*"], "forbid_falsified": true } ] }
```

```
$ oath put mut.oath ; echo $?                  0
⛔ the-decoder  BLOCKED: policy: falsified definitions may not hold this name
    object stored as #f6a710fe0868 (FALSIFIED: …); the name still points at
    its previous version
$ python3 -c '...names.json...'                the-decoder -> da5b44170f96
$ tail -1 log.jsonl
{'name': 'the-decoder', 'hash': 'f6a710fe…', 'status': 'blocked', 'prev': None}
```

| store policy | `put` exit | journal | the name afterwards |
|---|---|---|---|
| none | **2** | repointed | **the mutant** |
| `forbid_falsified` | **0** | `blocked` | **unmoved — the good body** |

**The exit status is inverted from the outcome, in both rows.** The run that
refuses exits 0; the run that lets the wrong body take the name exits 2. So the
signal is the NAME and the journal status, never `$?` — which is also why the
first two attempts to measure this file's exit codes were wrong: `$?` read after
a pipeline reports the pipeline's last command, and read inside a `printf`
argument list reports the preceding command substitution.

`oath verify` exits 0 for both bodies and carries its verdict only in its output
and in the stored guarantee.

**The closure is therefore: a property over the existing types + a policy the
kernel already implements.** No new language feature appears in either half.

### Which properties did the killing, and why it matters

**Only the three explicit class-B witnesses.** Both universally quantified
properties passed on the mutant:

| property | quantified over | kills the mutant? |
|---|---|---|
| `multibyte-shrinks` | the named domain | **no** — generator never enters it |
| `ascii-is-reinterpretation` | class A | no — the mutant is correct there |
| `inverts-the-encoder` | in-range `Str` | **no** — generator produces only ASCII |
| `decodes-cyrillic` | one named value | **yes** |
| `decodes-e-acute` | one named value | **yes** |
| `decodes-a-mixed-string` | one named value | **yes** |

`inverts-the-encoder` is the row worth pausing on. It is the round-trip property
a careful author would write without ever having heard of this issue, it is
universally quantified, and it does **not** kill the reinterpretation — because
every `Str` the generator produces is ASCII, where the two functions agree.

**This is the generator's blindness constraining the EVIDENCE, not the
PROPERTY.** `multibyte-shrinks` is a true statement about the mutant's domain
and it fires the moment it is evaluated there — part 4 measures exactly that,
`(class-b-shrinks (utf8-encode "ключ"))` is `false`. What the blindness removes
is the tester's ability to FIND the input; it does not make the property
unwritable, unstateable, or false. A witness supplies what generation cannot.

And the generated mutation engine cannot reach this mutation at all:

```
$ oath mutate utf8-decode-checked
no mutation points in utf8-decode-checked (body has no mutable operators,
literals, or branches)
```

Swapping a whole body for a different function is outside what the engine
generates, which is the documented gap between what a mutation score measures
(the generator's reach) and what a specification excludes. The contract here
excludes the reinterpretation; the score would never have said so.

## Part 6 — the call site, which is where F3 actually happened

Part 5 constrains a decoder written for the purpose. The objection that survives
it — raised in review, and correct — is that F3 did not happen in a decoder. It
happened inside `json-scoped-string`, whose last step is
`(bytes-str (json-string-value v))`, and no contract on a NEW definition says
anything about that call.

So the same experiment is run on the receiver's own extractor. Both definitions
below are `json-scoped-string`, copied, differing in that one call and in
nothing else. The contract is identical and says nothing about which functions
may be called — it is about what the definition RETURNS.

```lisp
; --- THE CALL SITE -----------------------------------------------------------
; `json-scoped-string` is the receiver's own field extractor, and F3 happened
; INSIDE it: its last step is `(bytes-str (json-string-value v))`. Both defs
; below are that function, copied, differing in that one call and in nothing
; else. The contract is identical and is about the DEFINITION'S OBSERVABLE
; BEHAVIOUR, not about which function it may call.

(defn scoped-string-via-reinterpret [] [(scope Str) (key Str) (body (List Int))] Str
  (match (bytes-after (str-bytes scope) body)
    ((None) "-")
    ((Some inner)
      (match (bytes-after (str-bytes (str-append (str-append "\"" key) "\":\"")) inner)
        ((None) "-")
        ((Some v) (bytes-str (json-string-value v))))))

  (prop ascii-repo-round-trips []
    (== (scoped-string-via-reinterpret "\"repository\":{" "full_name"
          (utf8-encode "{\"repository\":{\"full_name\":\"miclip/oath-lang\"}}"))
        "miclip/oath-lang"))
  (prop cyrillic-repo-round-trips []
    (== (scoped-string-via-reinterpret "\"repository\":{" "full_name"
          (utf8-encode "{\"repository\":{\"full_name\":\"miclip/ключ\"}}"))
        "miclip/ключ"))
  (prop absent-scope-is-marked [(key Str) (body (List Int))]
    (== (scoped-string-via-reinterpret "\"nope\":{" key body) "-")))

(defn scoped-string-via-decode [] [(scope Str) (key Str) (body (List Int))] Str
  (match (bytes-after (str-bytes scope) body)
    ((None) "-")
    ((Some inner)
      (match (bytes-after (str-bytes (str-append (str-append "\"" key) "\":\"")) inner)
        ((None) "-")
        ((Some v) (utf8-decode-checked (json-string-value v))))))

  (prop ascii-repo-round-trips []
    (== (scoped-string-via-decode "\"repository\":{" "full_name"
          (utf8-encode "{\"repository\":{\"full_name\":\"miclip/oath-lang\"}}"))
        "miclip/oath-lang"))
  (prop cyrillic-repo-round-trips []
    (== (scoped-string-via-decode "\"repository\":{" "full_name"
          (utf8-encode "{\"repository\":{\"full_name\":\"miclip/ключ\"}}"))
        "miclip/ключ"))
  (prop absent-scope-is-marked [(key Str) (body (List Int))]
    (== (scoped-string-via-decode "\"nope\":{" key body) "-")))
```

```
✗ scoped-string-via-reinterpret #c80df6635ea8
    FALSIFIED: cyrillic-repo-round-trips · total
    prop ascii-repo-round-trips   passed 200 cases
    prop cyrillic-repo-round-trips FALSIFIED after 0 cases
    prop absent-scope-is-marked   passed 200 cases

✓ scoped-string-via-decode #bdfbf986fa92  tested (200 cases per property) · total
    prop ascii-repo-round-trips   passed 200 cases
    prop cyrillic-repo-round-trips passed 200 cases
    prop absent-scope-is-marked   passed 200 cases
```

**The receiver's own code, unchanged except for its name, is falsified by one
property.** The property does not forbid calling `bytes-str`; it says the
extractor must return the repository name that was in the body, and calling the
reinterpretation there makes it return `miclip/ÐºÐ»ÑŽÑ‡` instead. `bytes-str`
remains callable, and a definition that calls it wrongly stops verifying.

`ascii-repo-round-trips` passes on both, which is the class-A control in its
third appearance: the difference is invisible on the inputs the corpus, the
fixtures and the generator all favour.

Composed with part 5's policy row, the chain has no gap in it:

    a property over the existing types   falsifies the extractor that calls
                                         `bytes-str`
    `forbid_falsified` in policy.json    that definition cannot hold the name
    the previous version stays live      the mojibake path is not reachable
                                         through the name

Neither half is new work. The property is three lines of ordinary Oath, and the
policy flag has been in the kernel since the team store.

## Part 7 — the closure does not hold, and this is how it fails

**VOCABULARY, because this file's result lives in the difference.** A body
*PASSES* a check when the generated cases it was run against did not falsify it,
and it earns `tested`. A body *SATISFIES* a property when the property is true of
it for every value in the property's domain. Every cheat below PASSES and none
SATISFIES: `multibyte-shrinks` is false of `utf8-decode-cheat` at `©`, and
`usable-encodes` is false of the secret cheat above 255. Writing "satisfies" for
either would state the opposite of what was measured, and earlier drafts of this
file did so in four places.


Parts 5 and 6 were written as a closure. Review objected that the contract's
universe is the three witnessed values plus whatever the generator reaches, not
class B — CLAUDE.md's universe check, applied to this file. That objection is
correct, and the measurement is the point of this part.

### A decoder that PASSES the whole contract's generated checks and still mojibakes

```lisp
; A decoder that decodes ONLY the three witnessed inputs and reinterprets
; everything else. Same six properties as `utf8-decode-checked`, verbatim.
(defn utf8-decode-cheat [] [(bs (List Int))] Str
  (if (== bs (utf8-encode "ключ")) (utf8-decode bs)
  (if (== bs (utf8-encode "é")) (utf8-decode bs)
  (if (== bs (utf8-encode "a-é-ключ")) (utf8-decode bs)
      (bytes-str bs))))

  (prop multibyte-shrinks [(v (List Int))]
    (if (and (utf8-valid v) (utf8-high v))
        (< (str-len (utf8-decode-cheat v)) (length [Int] v))
        true))
  (prop ascii-is-reinterpretation [(v (List Int))]
    (if (and (utf8-valid v) (not (utf8-high v)))
        (== (utf8-decode-cheat v) (bytes-str v))
        true))
  (prop decodes-cyrillic [] (== (utf8-decode-cheat (utf8-encode "ключ")) "ключ"))
  (prop decodes-e-acute [] (== (utf8-decode-cheat (utf8-encode "é")) "é"))
  (prop decodes-a-mixed-string []
    (== (utf8-decode-cheat (utf8-encode "a-é-ключ")) "a-é-ключ"))
  (prop inverts-the-encoder [(s Str)]
    (if (all [Int] (fn [(c Int)] (and (<= 0 c) (<= c 2047))) (str-bytes s))
        (== (utf8-decode-cheat (utf8-encode s)) s)
        true)))
```

```
✓ utf8-decode-cheat #81cbf68a490a  tested (200 cases per property) · total
    prop multibyte-shrinks        passed 200 cases
    prop ascii-is-reinterpretation passed 200 cases
    prop decodes-cyrillic         passed 200 cases
    prop decodes-e-acute          passed 200 cases
    prop decodes-a-mixed-string   passed 200 cases
    prop inverts-the-encoder      passed 200 cases
```

**All six.** And at any class-B value that is not one of the three witnesses:

```
$ oath eval '(utf8-valid (utf8-encode "©"))'                        true  : Bool
$ oath eval '(utf8-high  (utf8-encode "©"))'                        true  : Bool
$ oath eval '(length [Int] (utf8-encode "©"))'                       2 : Int
$ oath eval '(str-len (utf8-decode-cheat (utf8-encode "©")))'        2 : Int
$ oath eval '(str-len (utf8-decode       (utf8-encode "©")))'        1 : Int
$ oath eval '(== (utf8-decode-cheat (utf8-encode "©")) "©")'        false : Bool
$ oath eval '(utf8-decode-cheat (utf8-encode "©"))'                 Â©
```

Side by side in the store, the wrong decoder and the right one are
indistinguishable:

```
utf8-decode-cheat   #81cbf68a490a  func  tested (200 cases per property) · total
utf8-decode-checked #da5b44170f96  func  tested (200 cases per property) · total
utf8-decode-mutant  #f6a710fe0868  func  FALSIFIED: … · total
```

`forbid_falsified` blocks the third row and has nothing to say about the first.
So part 5 excluded ONE whole-body mutant; it did not close the failure mode.

### The property is fine. The EVIDENCE is what is missing.

This is worth separating carefully, because the two readings lead opposite ways.
`multibyte-shrinks` is a correct SPECIFICATION — true of the real decoder,
false of the cheat, which is exactly what a discriminating property should be.
(Not "a true statement the cheat violates": a universal cannot be true of a body
and false at one of its values. The property is valid; the cheat fails it.) At
`©` the cheat returns 2 codepoints from 2 bytes, so the property is false there.
The property discriminates. Nothing is wrong with how it is written.

What is missing is any way to DISCHARGE it:

- **Generation cannot.** The tester never enters class B — part 4's calibration,
  200 cases per property, no byte at or above 128.
- **Proof does not discharge THE ROUND-TRIP.** Scoped deliberately: this is the
  only goal submitted. `multibyte-shrinks` was never sent to the prover, so
  nothing here establishes that proof fails on it — see the summary, which says
  so. Re-spelled without the `lam` that blocks the fragment, the round-trip is
  still not discharged, and the cheat's version did not return within a
  ten-minute budget:

```lisp
; lam-free spelling of the round-trip guard, so the prover can see it.
(defn cps-in-range [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest) (if (and (<= 0 c) (<= c 2047)) (cps-in-range rest) false)))
  (prop ascii-in-range [] (cps-in-range "hi"))
  (prop cyrillic-in-range [] (cps-in-range "ключ")))

(defn roundtrip-real [] [(s Str)] Bool
  (if (cps-in-range s) (== (utf8-decode (utf8-encode s)) s) true)
  (prop holds [(s Str)] (roundtrip-real s)))

(defn roundtrip-cheat [] [(s Str)] Bool
  (if (cps-in-range s) (== (utf8-decode-cheat (utf8-encode s)) s) true)
  (prop holds [(s Str)] (roundtrip-cheat s)))
```

```
$ oath prove roundtrip-real   · unproven  no direct proof; induction did not discharge
$ oath prove roundtrip-cheat  (killed at 10m)
```

So what actually gates a definition is the finite witness list, and a finite
witness list is satisfiable by an implementation that special-cases exactly it.

**The gap is not between a property and a type. It is between a property and its
discharge.** That is a different question from the one #169 asks, and it is the
one this experiment ended up measuring.

## Part 8 — the same test breaks F1 and F2, so the result is uniform

Part 7 was first written as an F3-only finding, with F1 and F2 still recorded as
closed. Review objected that the adversarial-body test had been applied to one
row of the table and not the others. It was, and applying it to the others
changes the answer.

```lisp
; The same adversarial-body test, applied to F1/F2's guard. This rejects exactly
; the codepoints the corpus's two NON-ASCII witnesses are built from — `é` (233),
; which is class B, and the Cyrillic block "ключ" uses, which is class C — and
; admits everything else. (Not "two class-B witnesses": the taxonomy is the point
; of this file and ключ is above 255.)
; The five properties are `secret-is-usable`'s, verbatim.
(defn secret-is-usable-cheat [] [(secret Str)] Bool
  (and (<= 16 (str-len secret))
       (all [Int] (fn [(c Int)]
                    (and (not (== c 233))
                         (not (and (<= 1072 c) (<= c 1103)))))
            (str-bytes secret)))

  (prop empty-is-not-usable [] (not (secret-is-usable-cheat (SNil))))
  (prop short-is-not-usable [(s Str)]
    (if (< (str-len s) 16) (not (secret-is-usable-cheat s)) true))
  (prop non-ascii-is-not-usable [(s Str)]
    (not (secret-is-usable-cheat (str-append "ключключключключключключключключ" s))))
  (prop latin1-is-not-usable [(s Str)]
    (not (secret-is-usable-cheat (str-append "ééééééééééééééééééééééééééééééé" s))))
  (prop usable-encodes [(s Str)]
    (if (secret-is-usable-cheat s) (bytes-ok (str-bytes s)) true)))
```

```
✓ secret-is-usable-cheat #1233100ce8c3  tested (200 cases per property) · total
    prop empty-is-not-usable      passed 200 cases
    prop short-is-not-usable      passed 200 cases
    prop non-ascii-is-not-usable  passed 200 cases
    prop latin1-is-not-usable     passed 200 cases
    prop usable-encodes           passed 200 cases
```

All five of the receiver's own properties. And at an unwitnessed class-B secret
— thirty-two `©`, a printable Latin-1 character neither witness mentions (`©`
is U+00A9, so `str-bytes` yields 169 and `bytes-ok` is satisfied):

```
$ oath eval '(secret-is-usable-cheat "©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©")'
true  : Bool          <- ADMITTED
$ oath eval '(secret-is-usable "©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©")'
false : Bool          <- refused by the guard that shipped

$ printf '%s' '{}' | openssl dgst -sha256 -hmac '©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©' -r
a3eb6830c88b6a2a69380a27110247194234122d9678fedf7b5fad9988375bc4 *stdin
$ oath eval '(hex-encode (hmac-sha256 (str-bytes "©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©©") (str-bytes "{}")))'
c975c9e7db9794efda95523ba627a98f8eb215dff030ce29e591c96ff3ff6e7b
```

(Thirty-two `©`, spelled out: the guard has a sixteen-character minimum, so an
abbreviated secret is refused for the wrong reason and reproduces nothing. This
is the second time an elision broke a transcript in this file.)

**That is F2, live, under a guard that PASSES the receiver's contract.** The
digests disagree, nothing raises, and the definition carries `tested · total`.
It does not SATISFY that contract — `usable-encodes` is false of it above 255 —
and the whole point is that nothing in the guarantee ladder can tell the two
apart here.

`usable-encodes` is the property that should have excluded the ABOVE-255 half of
this — F1 — and it is the
same shape as `multibyte-shrinks`: universally quantified, true of the real
guard, false of the cheat — at any admitted codepoint above 255 — and never
exercised there, because the generator produces ASCII. `secret-is-usable` in the
committed corpus is `tested`, not proven; its meta carries no proofs.

So the three rows are not different after all. **What separated them in the
first draft was which one had been attacked.**

## What was tested, and what was not

**Tested:**

- F1 is caught by a property over existing types (`bytes-ok (str-bytes s)`), and
  the crash is reproduced at `oath eval`.
- F2 is NOT caught by that property — measured at a named witness, against
  `openssl`'s digest as an external oracle — and IS caught by the shipped
  printable-ASCII guard, which refuses class B outright.
- **Neither F1's nor F2's guard is CLOSED under the adversarial-body standard.**
  A guard rejecting exactly the two witnessed codepoint ranges passes all five generated checks of
  of the receiver's properties, admits a thirty-two-`©` secret, and produces a
  digest `openssl` disagrees with.
- F3 is detected by a property over existing types that needs no reference
  decoder, provided the byte domain is given a name; both mutations of that
  property stop it discriminating.
- One whole-body reinterpretation IS excluded by a contract over the named
  domain plus class-B witnesses, and `forbid_falsified` in `policy.json` turns
  that falsification into `blocked`, leaving the name on the previous version.
  Measured with the two-way control: WITHOUT the policy the falsified body TAKES
  the name, so `put`'s exit 2 is a warning and not a refusal.
- The same exclusion reaches the receiver's OWN call site — but read the
  qualifier, because a first draft said "copied verbatim" and that is wrong about
  the part that matters. The `json-scoped-string` BODY is the receiver's; the
  CONTRACT is not. Production carries `absent-scope-is-marked`,
  `value-has-no-quote` and `value-has-no-tab`
  (`apps/github-webhook/webhook.oath:189-208`); this experiment reworks those and
  ADDS the round-trip witnesses that do the falsifying. So what is shown is that
  **a property nobody had written would catch the defect** — not that the
  receiver's existing definition stops verifying. The distinction is the whole
  subject of this file arriving one level up: the defect was always there, and
  the evidence for it had to be authored.
- **F3 is NOT closed.** A decoder that special-cases the three witnessed inputs
  and reinterprets everything else passes all six generated checks, carries the
  same `tested · total` guarantee line as the correct decoder, is untouched by
  `forbid_falsified`, and returns `Â©` for `©`.
- Neither evidence route discharges the universal property that would exclude
  it: generation never enters class B, and `oath prove` does not discharge the
  round-trip (one attempt unproven, one killed at ten minutes).
- The killing was done entirely by the three explicit witnesses. Both
  universally quantified properties in the same contract passed on the mutant,
  including the round-trip property an author would write without knowing about
  this issue.
- The generated-case tester never enters class B in 200 cases per property, so
  every class-B property here passes vacuously under the corpus's default
  evidence route.
- The prover does not discharge the class-B properties.
- The one property the corpus states about `bytes-str` is satisfied by the
  reinterpretation and falsified by a correct decoder, and `oath mutate` scores
  that specification 1/1.

**Not tested, and not to be inferred from anything above:**

- **Whether some OTHER property over existing types detects F3 without naming
  the domain.** Nothing here searches the space of properties; the measurement
  is about the two candidates the corpus and the receiver actually contain.
- **Whether `bytes-str` can still be CALLED.** It can, and nothing here changes
  that. What part 6 measures is narrower and is the thing that matters: a
  definition that calls it where a decode is meant stops verifying. No property
  was run, or could be, that forbids the call itself.
- **Whether the price of the shipped guard is acceptable.** It refuses every
  non-ASCII secret; that is recorded, not judged.
- **Whether a type would have made any of these properties unnecessary.** No
  type was declared, and #159's exclusion test was not applied to anything here.
- Three- and four-byte UTF-8, surrogates, and encodings above U+10FFFF, for the
  reasons in parts 1 and 2.
- `record-field` and the log path. The receiver's own closure of F3 there —
  marking a non-ASCII field absent — is a third instance of the same ASCII
  narrowing and was not re-measured. The extractor's `bytes-after` scan IS
  exercised, by part 6, against complete JSON bodies; what is untested is
  everything downstream of it.
- Any behaviour of the committed store. `codebase/` and `fixtures/` were not
  written; the probes ran against a throwaway copy.

## The falsifier's outcome

#169 stated the condition itself:

> **If every failure mode this conflation produces can be closed by a property
> over the existing types, then the distinction buys naming and not safety**,
> and this should be declined with that argument recorded.

**"Closed" is doing all the work in that sentence, and it does not fix a
standard.** Three readings are available, they give three different answers, and
which one #169 meant is what actually decides the issue.

| standard | F1 | F2 | F3 | |
|---|---|---|---|---|
| **A** a property that would have caught the mistake that ACTUALLY happened is writable over the existing types | yes | yes | yes | parts 3, 5, 6 |
| **B** that property is DISCHARGED, so a wrong body cannot carry the name | no | no | no | parts 4, 7, 8 |
| **C** the wrong body is unwritable | no | no | no | not measured; this is the type's claim |

Under **A** the falsifier wins and #169 should be declined. Under **B** it does
not, and F1 and F2 fail it exactly as F3 does — the first draft of this section
said otherwise, and part 8 is the measurement that corrected it.

**This file therefore does not recommend declining #169.** It cannot, without
choosing between A and B, and that choice is not a measurement.

### What the run establishes for whoever makes that choice

- **GENERATION NEVER REACHES CLASS B.** Measured directly: no codepoint and no
  byte at or above 128 across 200 cases per property. Stated as an upper bound
  only, because that is all the calibration measured — the tester DOES draw
  negative values (`-1` and `-2` appear in the `corpus-roundtrip-decode`
  transcript), which are class C, so "every generated variable is class A" would
  be false and an earlier version of this line said it. The claim is about what
  the TESTER DRAWS, and two further narrowings keep it honest. Properties
  like `latin1-is-not-usable [(s Str)]` do CHECK class-B values — they construct
  one by prefixing `é` onto a generated suffix — so the values examined reach
  class B even though the drawn ones never do; what generation cannot supply is a
  class-B value it was not told to build. And the closures in parts 5
  and 6 are not in this set at all: they are killed by Cyrillic and Latin-1
  witnesses chosen by hand. Writing "every closure rests on class-A generation"
  would credit the generator with exclusions a person made.
- **The universal properties that would exclude the wrong bodies are all
  writable and all true.** `multibyte-shrinks` and `usable-encodes` are false of
  their respective cheats. Nothing is wrong with how they are stated.
- **No instrument discharged the ones that were ATTEMPTED, and the others were
  not attempted.** Precision matters here because this is the file's central
  claim and a first draft overstated it into "no available instrument discharges
  them". What was run: generation, across every quantified property, blind to the
  domain by measurement; and `oath prove` on the DECODER ROUND-TRIP, which
  returned unproven and did not return within ten minutes on the cheat's version.
  **`multibyte-shrinks` and `usable-encodes` were never submitted to the prover
  at all.** So "proof does not reach this" is established for one goal and
  assumed for the rest, and the assumption is the kind this file exists to refuse.
  The corpus's own `secret-is-usable` is `tested`, which is an observation about
  what was done rather than about what could be.
- **So what gates a definition in practice is a finite witness list**, and a
  finite witness list is satisfiable by a body that special-cases exactly it —
  demonstrated twice, on two unrelated guards, with identical `tested · total`
  guarantee lines beside the correct versions.

### The honest qualifier, because the cheats are adversarial

`utf8-decode-cheat` and `secret-is-usable-cheat` are not mistakes anyone would
make. They were written to satisfy a known contract, which is not how the real
defects arose — F3 arose from calling `bytes-str`, and parts 5 and 6 show a
property catches THAT. So standard A is not a straw man, and a reader who thinks
adversarial bodies are the wrong threat model should read the table as ending at
row A.

What the cheats do establish is narrower and still load-bearing: **the guarantee
line does not distinguish them.** Two definitions, byte-identical verdicts, one
correct and one not. Whatever #169 decides, that is a fact about the evidence
layer rather than about the type system, and it would survive a `Bytes` type
being added or declined.

### Where this leaves the question

A `Bytes` type is one way to reach standard C, where nothing needs discharging
because the mistake is unwritable. It is not the only way to reach standard B —
but the alternative has to be stated carefully, because a first draft of this
paragraph overstated it. The three rows are governed by DIFFERENT properties:
F3 by `multibyte-shrinks` and the decoder round-trip; F1 by `usable-encodes` on
`secret-is-usable`; and F2 by NEITHER — see the correction below, since `©`
satisfies `bytes-ok` while its digest still disagrees. Discharging one says
nothing about the others.

So the standard-B route is not "prove the round-trip"; it is **discharge every
class-B obligation in the program**, whatever each one is. A prover would have
to take them one at a time, and `usable-encodes` was not attempted here.

**AND `usable-encodes` GOVERNS F1 BUT NOT F2, which earlier drafts assumed in
several places.** The cheat demonstrates both — it admits a class-B `©` whose
digest disagrees with OpenSSL, which is F2, and it can admit an above-255
codepoint, which is F1. What it does not do is let `usable-encodes` carry F2:
`str-bytes "©"` is 169, so `bytes-ok` holds and the property is TRUE there while
the digest still disagrees.
For `©` — and every codepoint in 128..255 — `str-bytes` yields values inside
0..255, so `bytes-ok` holds while the sender's UTF-8 digest still differs.
Proving `usable-encodes` universally would therefore leave F2 standing: a guard
can reject everything outside 0..255 and admit `©`. F2 needs its own universal
property relating the admitted codepoints to the ENCODING the counterparty
signs, and no such property is stated in this file. The three failures do not
reduce to two obligations.

**A DOMAIN-AWARE GENERATOR IS NOT THAT ROUTE, AND A FIRST DRAFT OF THIS
PARAGRAPH SAID IT WAS.** It would reach all three blindnesses at once and would
kill both cheats recorded here — that is real and it is worth having. But
standard B says no incorrect body may retain the name, and sampling a named
domain for a finite number of deterministic cases cannot establish that: a body
can special-case exactly the values the generator draws, precisely as the cheats
above special-case exactly the values a human chose. **The document made, one
level up, the mistake it spends part 7 diagnosing** — a finite witness set is a
finite witness set however it is chosen. Generation raises the cost of cheating;
it does not close the class.

The one thing it does claim is that **#169 cannot be settled by asking whether a
property can be written, because the answer to that is yes and it is not
sufficient.**

## Reproduction

Every fenced `lisp` block above is a file, and they are printed in dependency
order. Extracting them from this document and putting them in that order
reproduces the run, which is how the transcripts above were checked:

```sh
make build
SP=$(mktemp -d); cp -R codebase "$SP/store"
python3 - "$SP" <<'EOF'
import sys, pathlib, re
v = pathlib.Path(sys.argv[1])
doc = pathlib.Path("docs/experiments/issue-169.md").read_text()
fence = chr(96) * 3
for i, b in enumerate(re.findall(fence + r"lisp\n(.*?)" + fence, doc, re.S)):
    (v / f"block{i:02d}.oath").write_text(b)
EOF
export OATH_STORE="$SP/store"          # the eval/prove lines below need it too
for f in "$SP"/block*.oath; do
  ./oath/oath put "$f" --new
done
```

(The fence is spelled `chr(96) * 3` because a literal one inside this block
would close it.)

Replayed that way into a store copied fresh from `codebase/`, every definition
hash printed above was reproduced identically. The extractor writes twelve files,
`block00.oath` through `block11.oath`:

    block00  the named domain            block06  the lambda-free re-spelling
    block01  the instruments             block07  the checked decoder + mutant
    block02  the secret-side guards      block08  the two call-site variants
    block03  the check matrix            block09  the witness-cheating decoder
    block04  the generator calibration   block10  the round-trip proof attempt
    block05  the witnesses                block11  the cheating secret guard

`put` exits 2 on any file containing a falsified definition, and several blocks
contain one deliberately. Measured over the replay: `block03`, `block04`,
`block05`, `block07` and `block08` exit 2; every other block exits 0.

**`block09` and `block11` exiting 0 are the whole finding of parts 7 and 8** —
those are the two cheating bodies being accepted. Note also what capturing that
takes: `$?` must be read into a variable before any command substitution runs,
or a `$(basename …)` in the same `echo` resets it and every block reports 0.
That mistake was made three times while producing this file.

### The `forbid_falsified` control

Not reachable from the loop above, because it needs both bodies bound to ONE
name in a store with a policy. Save as a file and run it from the repository
root, passing the directory the blocks were extracted into:

```sh
#!/bin/sh
set -e
B="$1"; SP=$(mktemp -d); cp -R codebase "$SP/store"
for i in 00 01 06; do OATH_STORE="$SP/store" ./oath/oath put "$B/block$i.oath" --new >/dev/null; done

awk '/^; THE MUTANT:/{m=1} !m' "$B/block07.oath" | sed 's/utf8-decode-checked/the-decoder/g' > "$SP/good.oath"
awk '/^; THE MUTANT:/{m=1}  m' "$B/block07.oath" | sed 's/utf8-decode-mutant/the-decoder/g'  > "$SP/mut.oath"
name() { python3 -c "import json;print(json.load(open('$SP/store/names.json'))['the-decoder'][:12])"; }

OATH_STORE="$SP/store" ./oath/oath put "$SP/good.oath" --new >/dev/null; echo "bound    -> $(name)"
# EXPECTED FAILURES ARE ASSERTED, NOT SWALLOWED. `|| true` treats a parse error,
# a missing store and the falsification this is FOR as one outcome, so the
# control would pass while measuring nothing — the defect this whole file is
# about, in its own reproduction script.
echo "--- no policy:"
rc=0; OATH_STORE="$SP/store" ./oath/oath put "$SP/mut.oath" >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || { echo "FAIL setup: expected exit 2 (falsified), got $rc"; exit 1; }
echo "name now -> $(name)"
OATH_STORE="$SP/store" ./oath/oath put "$SP/good.oath" >/dev/null;      echo "reset    -> $(name)"
echo '{ "rules": [ { "names": ["*"], "forbid_falsified": true } ] }' > "$SP/store/policy.json"
echo "--- forbid_falsified:"
# Captured rather than piped to `grep -q`: -q consumes the line, so the
# transcript below could never contain the BLOCKED output it claims to show.
out=$(OATH_STORE="$SP/store" ./oath/oath put "$SP/mut.oath" 2>&1)
printf '%s\n' "$out" | grep -E 'BLOCKED' \
  || { echo "FAIL setup: policy did not report BLOCKED"; exit 1; }
echo "name now -> $(name)"
tail -1 "$SP/store/log.jsonl" | python3 -c "import sys,json;print('journal  ->',json.load(sys.stdin)['status'])"
```

Its output, run as printed:

```
bound    -> da5b44170f96
--- no policy:
name now -> f6a710fe0868
reset    -> da5b44170f96
--- forbid_falsified:
⛔ the-decoder      BLOCKED: policy: falsified definitions may not hold this name
name now -> da5b44170f96
journal  -> blocked
```

`oath prove` and the `oath eval` lines are run afterwards against that same
store — which is why `OATH_STORE` is EXPORTED above rather than set per command:
the names this file defines do not exist in the default committed store.
