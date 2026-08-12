# #169 — does a property over the existing types close each failure?

**What this file is:** #169's own falsifier, made runnable, and its output. It
is the check the issue asks for before any design work:

> **If every failure mode this conflation produces can be closed by a property
> over the existing types, then the distinction buys naming and not safety**,
> and this should be declined with that argument recorded.

**It proposes no type, chooses no design, and declares no verdict on #169.**
Where the measurements point is the human's fork, and it is deliberately not
taken here. Nothing below is an argument that a `Bytes` type is or is not
wanted; every sentence is either a command that was run or a reading of what it
printed.

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

So **F1 and F2 are both closed by a property over the existing types**, and the
property that closes both closes them the same way: by narrowing the admitted
set to class A, where `ρ` is unobservable. `shipped-refuses-a-non-ascii-secret`
records the price. Whether that price is acceptable is not measured here.

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

## What was tested, and what was not

**Tested:**

- F1 is closed by a property over existing types (`bytes-ok (str-bytes s)`), and
  the crash is reproduced at `oath eval`.
- F2 is NOT closed by that property — measured at a named witness, against
  `openssl`'s digest as an external oracle — and IS closed by the shipped
  printable-ASCII guard, which refuses class B outright.
- F3 is detected by a property over existing types that needs no reference
  decoder, provided the byte domain is given a name; both mutations of that
  property stop it discriminating.
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
- **Whether the price of the shipped guard is acceptable.** It refuses every
  non-ASCII secret; that is recorded, not judged.
- **Whether a type would have made any of these properties unnecessary.** No
  type was declared, and #159's exclusion test was not applied to anything here.
- Three- and four-byte UTF-8, surrogates, and encodings above U+10FFFF, for the
  reasons in parts 1 and 2.
- The JSON scan, `record-field`, and the log path. F3 is probed at `bytes-str`
  directly; the receiver's own closure of it (marking a non-ASCII field absent)
  is a third instance of the same ASCII narrowing and was not re-measured.
- Any behaviour of the committed store. `codebase/` and `fixtures/` were not
  written; the probes ran against a throwaway copy.

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
for f in "$SP"/block*.oath; do
  OATH_STORE="$SP/store" ./oath/oath put "$f" --new
done
```

(The fence is spelled `chr(96) * 3` because a literal one inside this block
would close it.)

Replayed that way into a store copied fresh from `codebase/`, every definition
hash printed above was reproduced identically. The seven blocks are, in order:
the named domain, the instruments, the secret-side guards, the check matrix, the
generator calibration, the witnesses, and the lambda-free re-spelling.

`oath prove` and the `oath eval` lines are run against the same store afterwards.
