# #169 — does a property over the existing types close each failure?

**What this file is:** #169's own falsifier, made runnable, and its output. It
is the check the issue asks for before any design work:

> **If every failure mode this conflation produces can be closed by a property
> over the existing types, then the distinction buys naming and not safety**,
> and this should be declined with that argument recorded.

**It proposes no type and chooses no design.** It does reach a verdict, which is
what the issue asked for — **and the verdict is stated exactly once, with the
exhaustive table under *The falsifier's outcome*, and deliberately NOT here.**
Five successive drafts of a summary restatement were each wrong, in five
different directions, and a summary that duplicates a verdict is exactly the
drift this project keeps having to repair. The table has one row for each of the
five failures #169 cites, each carrying a property that was actually run or a
statement of why none can be written. Read it there.

**The criterion is #169's own sentence — *closed by a property over the existing
types* — and nothing else.** Four drafts of the outcome section supplied
substitutes: three invented competing "standards", and a fourth pressed #159's
exclusion test into service as a filter on which properties count, which
inverts an exclusion test into a sufficiency test and writes ROLE where its
subject is CLASSIFICATION. Both errors are named in `CLAUDE.md` and in
`issue-159-refinements.md`; the outcome section records them rather than
hiding them. **The per-row result is NOT repeated here** — five successive
summaries were each wrong in a different direction. Read the table.

**Two things to know before reading parts 1–8.** First, **they are CORRECT AS
MEASUREMENTS AND WRONG AS A CONCLUSION, and part 9 is why**: their central claim
— that no instrument discharges the universal properties — rested on two proof
attempts the file declined to make and then reasoned about by analogy. Made,
three of the four succeed, in 3, 14 and 0 seconds. Second, **parts 1–8 and 12
measure DISCHARGE, which is a different question from closure** and does not
decide any row; three drafts of the outcome section built competing "standards"
out of those measurements and let the choice between them settle the verdict,
which is exactly what a falsifier must never permit.
**Parts 1–8 are left in place unedited
because the analogy they drew is the defect worth seeing**, and because every
measurement in them replays identically; what is superseded is the inference,
not the data.

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

Parts 1–8 were run on 2026-08-12 against `910e8a6`. **Parts 9–12 were run on
2026-08-15 against `812b549`**, and the whole of parts 1–8 was replayed at that
revision first: every definition hash reproduced identically and every block's
exit status matched, so the two halves are measurements of the same corpus.
Both used the binary from `make build` and z3 4.16.0 on PATH.

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

Three of the five failures the falsifier has to be pointed at are the three the
application actually produced, from `docs/experiments/webhook-friction.md` §3
and the receiver's own comments:

    F1  class C   a Cyrillic secret reaches `hmac-sha256`; the request dies
    F2  class B   a Latin-1 secret; the digest silently disagrees with the sender
    F3  class B   a UTF-8 repository name through `bytes-str`; mojibake in the log

**Parts 1–8 stop there, and that was an under-coverage of #169's own text**,
which cites two further findings as sharpening the same conflation. They get
rows in part 11 and in the exhaustive table:

    F4  #164      `Str`'s scalar range is unchecked; the kernel builds (SCons -1 …)
    F5  #167      a representation defect reported as `out of range 0..255`

**F3 is probed hardest** in parts 1–8, because it is the case #169 names as the
one that weighs against declining: nothing raises, and no property was written
because nobody knew to write one. It is not the row that decides the verdict —
F5 is, being the only one no property can close — but it is the row that carries
the COST recorded beside the result, which part 10 establishes.

## Method

Every definition below is put into a **scratch store**, with `--new`. Nothing
WRITES `codebase/` or `fixtures/`; `codebase/` is read once, to copy it. The
scratch store is a COPY of `codebase/` rather than an empty `mktemp -d`,
because the probes call corpus names — `bytes-str`, `str-bytes`, `bytes-ok`, `secret-is-usable` — which an
empty store cannot resolve.

```sh
# UNSET, not merely overridden by OATH_STORE: when OATH_BACKEND=cloud,
# OpenStore (oath/store.go:49) selects the cloud driver and IGNORES the path it
# was handed, so every put/prove below would target the LIVE registry — which
# has no authorized-key allowlist. `issue-170.md` carries the same guard.
unset OATH_BACKEND
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

## Part 9 — the proof attempts this file declined to make

Parts 7 and 8 rest on a claim the summary itself flagged as assumed rather than
established: *`multibyte-shrinks` and `usable-encodes` were never submitted to
the prover at all.* They have now been submitted, together with the same
obligation on every cheat. **They change the answer for two rows out of five,
and they change it in the direction the file did not expect.** Part 12 records
where a first draft of this part then over-read them, since PROVABLE and
EXCLUDES THE FAILURE are not the same question and F4 separates them.

Two spellings are involved and the difference is not cosmetic. `secret-is-usable`
is written with `all` + `fn`, and a `lam` is outside the provable fragment — so
the corpus definition is not merely unproven, it is **unsubmittable**. Proving
its obligation requires re-spelling the guard as a direct recursion, which
changes the body, hence the hash, hence the identity. What is proven below is
therefore a DIFFERENT DEFINITION that states the same obligation, and no verdict
here attaches to `#5365b4a06be5`.

```lisp
; --- THE OBLIGATIONS, RE-SPELLED SO THE PROVER CAN SEE THEM ------------------
; `oath prove secret-is-usable` returns 0/5, every property `"lam" terms are
; outside the provable fragment`. These are the same obligations as direct
; recursions. They are NOT the corpus definitions and do not inherit their
; hashes.

(defn printable-ascii-cps [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest)
      (if (and (<= 33 c) (<= c 126)) (printable-ascii-cps rest) false)))
  (prop empty-is-ok [] (printable-ascii-cps (SNil)))
  (prop ascii-is-ok [] (printable-ascii-cps "abcdef0123456789"))
  (prop latin1-is-not [] (not (printable-ascii-cps "é")))
  (prop cyrillic-is-not [] (not (printable-ascii-cps "ключ"))))

(defn not-the-two-witnessed-ranges [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest)
      (if (or (== c 233) (and (<= 1072 c) (<= c 1103)))
          false
          (not-the-two-witnessed-ranges rest))))
  (prop ascii-passes [] (not-the-two-witnessed-ranges "abcdef0123456789"))
  (prop e-acute-fails [] (not (not-the-two-witnessed-ranges "é")))
  (prop copyright-passes [] (not-the-two-witnessed-ranges "©©©©©©©©©©©©©©©©")))

(defn secret-is-usable-p [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (printable-ascii-cps secret))
  (prop usable-encodes [(s Str)]
    (if (secret-is-usable-p s) (bytes-ok (str-bytes s)) true)))

(defn secret-is-usable-cheat-p [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (not-the-two-witnessed-ranges secret))
  (prop usable-encodes [(s Str)]
    (if (secret-is-usable-cheat-p s) (bytes-ok (str-bytes s)) true)))

; F2's OWN obligation, which is NOT `usable-encodes` — `©` is 169, so
; `bytes-ok` holds of it while the digest still disagrees. This is part 3's
; `signs-the-senders-bytes`, which part 3 tested vacuously and never submitted.
(defn signs-the-senders-bytes-p [] [(s Str)] Bool
  (== (str-bytes s) (utf8-encode s))
  (prop holds-on-ascii [] (signs-the-senders-bytes-p "abcdef0123456789"))
  (prop fails-on-latin1 [] (not (signs-the-senders-bytes-p "éééééééééééééééé")))
  (prop fails-on-copyright [] (not (signs-the-senders-bytes-p "©©©©©©©©©©©©©©©©"))))

(defn faithful-under-shipped-p [] [(s Str)] Bool
  (if (secret-is-usable-p s) (signs-the-senders-bytes-p s) true)
  (prop holds [(s Str)] (faithful-under-shipped-p s)))

(defn faithful-under-cheat-p [] [(s Str)] Bool
  (if (secret-is-usable-cheat-p s) (signs-the-senders-bytes-p s) true)
  (prop holds [(s Str)] (faithful-under-cheat-p s)))
```

Every attempt, with its command and its wall time. `oath prove` produces no
output until it finishes, so a killed run yields nothing at all rather than a
partial verdict:

```
$ oath prove utf8-decode-checked            killed at 900s — NO OUTPUT
$ oath prove utf8-decode-checked            killed at 900s — NO OUTPUT (retry)
$ oath prove utf8-decode-cheat                                        61s
    · unproven  multibyte-shrinks            no direct proof; induction did not discharge
    ∎ PROVEN    ascii-is-reinterpretation    direct (lemma-free) (Z3, unbounded ints)
    ∎ PROVEN    decodes-cyrillic             direct (lemma-free) (Z3, unbounded ints)
    ∎ PROVEN    decodes-e-acute              direct (lemma-free) (Z3, unbounded ints)
    ∎ PROVEN    decodes-a-mixed-string       direct (lemma-free) (Z3, unbounded ints)
    · unproven  inverts-the-encoder          "lam" terms are outside the provable fragment
    proven: 4/6 properties

$ oath prove secret-is-usable                                          0s   0/5, every property lam-blocked
$ oath prove secret-is-usable-cheat                                    0s   0/5, every property lam-blocked

$ oath prove secret-is-usable-p                                        3s
    ∎ PROVEN    usable-encodes               induction on binder 0 (Z3, unbounded ints)
$ oath prove secret-is-usable-cheat-p                                154s
    · unproven  usable-encodes               no direct proof; induction did not discharge

$ oath prove signs-the-senders-bytes-p                                 0s   3/3 PROVEN
$ oath prove faithful-under-shipped-p                                 14s
    ∎ PROVEN    holds                        induction on binder 0 (Z3, unbounded ints)
$ oath prove faithful-under-cheat-p                                   79s
    · unproven  holds                        no direct proof; induction did not discharge
```

### Two results, and the second is the one that matters

**F1's and F2's obligations ARE provable.** `usable-encodes` discharges by
induction in three seconds and `faithful-under-shipped-p` in fourteen. The file
previously recorded these as "not attempted" and reasoned about them by analogy
to F3; the analogy was wrong.

**The prover never REFUTES, it only fails to certify.** Every cheat's obligation
came back `unproven` — not `REFUTED` — although `REFUTED` is in the verdict
vocabulary (`oath/prove.go:2359` prints it with a counterexample over unbounded
ints). And each of those obligations is genuinely FALSE, checked by hand rather
than inferred:

```
$ oath eval '(utf8-valid (utf8-encode "©"))'                                true
$ oath eval '(utf8-high  (utf8-encode "©"))'                                true
$ oath eval '(length [Int] (utf8-encode "©"))'                              2
$ oath eval '(str-len (utf8-decode-cheat (utf8-encode "©")))'               2
    guard holds, 2 < 2 is false  ->  multibyte-shrinks is FALSE of the cheat

$ oath eval '(secret-is-usable-cheat-p "ѐѐѐѐѐѐѐѐѐѐѐѐѐѐѐѐ")'                  true
$ oath eval '(bytes-ok (str-bytes "ѐѐѐѐѐѐѐѐѐѐѐѐѐѐѐѐ"))'                      false
    U+0450 is outside 1072..1103  ->  usable-encodes is FALSE of the cheat

$ oath eval '(secret-is-usable-cheat-p "©©©©©©©©©©©©©©©©")'                  true
$ oath eval '(signs-the-senders-bytes-p "©©©©©©©©©©©©©©©©")'                 false
    ->  faithful-under-cheat-p's `holds` is FALSE of the cheat
```

So the prover had a false goal in front of it three times and returned no verdict
each time. **That is a fact about this prover on these goals, not about the
goals.** Unproven is not disproof, and nothing here says a better decision
procedure would fail.

**The asymmetry is what makes it usable anyway.** Soundness gives one direction
for free: a false property is never proven. So "the obligation is proven" excludes
every incorrect body without the prover ever having to detect one — which is what
part 12 measures.

**`utf8-decode-cheat` earns four PROOFS while mojibaking**, which is strictly
worse than the `tested · total` line part 7 records. The three witness properties
are exactly the ones the cheat was built to satisfy, and they are the ones that
discharge; the universal that would exclude it is the one that does not.

## Part 10 — the role-blind test, which is what actually settles F3

Parts 5 to 7 leave F3 as *the property is fine, the evidence is missing*. That
diagnosis is incomplete, and the missing half is not about evidence at all.

`bytes-str` maps byte `n` to codepoint `n`. That IS ISO-8859-1 decoding. So one
function is a **correct Latin-1 decoder** and an **incorrect UTF-8 decoder**, and
the `(bytes, text)` pair it produces is the same pair under both descriptions.

```lisp
; --- THE ROLE-BLIND TEST -----------------------------------------------------
; Nothing here is a new function: `latin1-decode`'s body is `bytes-str`.

(defn latin1-decode [] [(bs (List Int))] Str
  (bytes-str bs)
  (prop empty-decodes-empty [] (== (latin1-decode (Nil [Int])) (SNil)))
  (prop c3-a9-is-two-characters []
    (== (latin1-decode (Cons [Int] 195 (Cons [Int] 169 (Nil [Int])))) "Ã©")))

; THE PAIR, named once so both roles are asked about the identical values.
(defn the-bytes [] [] (List Int)
  (utf8-encode "é")
  (prop is-c3-a9 []
    (== (the-bytes) (Cons [Int] 195 (Cons [Int] 169 (Nil [Int]))))))

(defn the-text [] [] Str
  (bytes-str (the-bytes))
  (prop is-a-tilde-then-copyright [] (== (the-text) "Ã©")))

(defn pair-is-right-as-latin1 [] [] Bool
  (== (the-text) (latin1-decode (the-bytes)))
  (prop correct-at-this-role [] (pair-is-right-as-latin1)))

(defn pair-is-right-as-utf8 [] [] Bool
  (== (the-text) (utf8-decode (the-bytes)))
  (prop wrong-at-this-role [] (not (pair-is-right-as-utf8))))

; --- THE TWO-WAY CONTROL -----------------------------------------------------
; `multibyte-shrinks` is the property part 5 uses to close F3. Held against a
; CORRECT Latin-1 decoder it FIRES.
(defn latin1-under-multibyte-shrinks [] [] Bool
  (< (str-len (latin1-decode (the-bytes))) (length [Int] (the-bytes)))
  (prop fires-on-a-correct-latin1-decoder [] (not (latin1-under-multibyte-shrinks))))

; The converse, so the measurement discriminates in both directions.
(defn utf8-under-latin1-length [] [] Bool
  (== (str-len (utf8-decode (the-bytes))) (length [Int] (the-bytes)))
  (prop fires-on-a-correct-utf8-decoder [] (not (utf8-under-latin1-length))))

; --- THE UNIVERSAL FORM, lam-free so the prover can see it -------------------
; LENGTH IS NOT CORRECTNESS, and a first draft rested the claim on it alone —
; review pointed out that a decoder emitting one ARBITRARY character per byte
; satisfies the length property. The claim is that byte n becomes codepoint n,
; so `latin1-is-exact` says exactly that, using `str-bytes` as the inverse
; direction rather than a second decoder.
(defn latin1-preserves-length [] [(v (List Int))] Bool
  (if (bytes-ok v) (== (str-len (latin1-decode v)) (length [Int] v)) true)
  (prop holds [(v (List Int))] (latin1-preserves-length v)))

(defn latin1-is-exact [] [(v (List Int))] Bool
  (if (bytes-ok v) (== (str-bytes (latin1-decode v)) v) true)
  (prop holds [(v (List Int))] (latin1-is-exact v)))

; The control: the same round-trip pointed at the UTF-8 decoder, correct at ITS
; role and required to fail this one.
(defn utf8-is-not-exact [] [] Bool
  (== (str-bytes (utf8-decode (the-bytes))) (the-bytes))
  (prop the-check-fires [] (not (utf8-is-not-exact))))

; The two role obligations at the ONE value, in one definition, so the
; contradiction is a single evaluated fact rather than two transcripts.
(defn both-roles-cannot-be-satisfied [] [] Bool
  (and (== (str-len (bytes-str (the-bytes))) (length [Int] (the-bytes)))
       (< (str-len (bytes-str (the-bytes))) (length [Int] (the-bytes))))
  (prop no-single-body-satisfies-both [] (not (both-roles-cannot-be-satisfied))))
```

```
✓ latin1-decode    #71509f471f7c  tested (200 cases per property) · total
✓ the-bytes        #d08b179b37b7  tested (200 cases per property) · total
✓ the-text         #26a6e531dc5e  tested (200 cases per property) · total
✓ pair-is-right-as-latin1 #fc8c5948a5ce   correct-at-this-role         passed
✓ pair-is-right-as-utf8   #97227fa10c90   wrong-at-this-role           passed
✓ latin1-under-multibyte-shrinks #84f1ffb7461b
      fires-on-a-correct-latin1-decoder   passed
✓ utf8-under-latin1-length #5dbae43a9f71
      fires-on-a-correct-utf8-decoder     passed
✓ latin1-preserves-length #ed9ced91736e  tested · total
✓ latin1-is-exact  #4ff83a531a41  tested · total
✓ utf8-is-not-exact #75f0dc6759c4   the-check-fires                    passed
✓ both-roles-cannot-be-satisfied #7f34007db140
      no-single-body-satisfies-both       passed

$ oath prove latin1-preserves-length                                   5s
    ∎ PROVEN    holds                        induction on binder 0 (Z3, unbounded ints)
$ oath prove latin1-is-exact                                           4s
    ∎ PROVEN    holds                        induction on binder 0 (Z3, unbounded ints)
```

**The reinterpretation is PROVABLY CORRECT at one role** — and the load-bearing
proof is `latin1-is-exact`, not the length one. `∀v. bytes-ok v ⟹ str-bytes
(bytes-str v) == v` is pointwise byte-to-codepoint preservation, which is what
ISO-8859-1 decoding IS on the byte range; length preservation follows from it and
does not imply it. **Meanwhile `multibyte-shrinks` condemns the same body.** One
body, two obligations, both stated in `(List Int)` and `Str` and nothing else,
and they are jointly unsatisfiable at `[195,169]`.

### What that does and does not establish

**It closes the VALUE-LEVEL question.** Let `P` be any predicate whose arguments
are a `(List Int)` and a `Str`. At `b = [195,169]`, `t = "Ã©"` the Latin-1 role
requires `P b t` and the UTF-8 role requires `not (P b t)`. `P b t` is one
`Bool`. **No such `P` serves both roles.** That is a logical consequence of
assigning opposite correctness labels to one pair, not a measurement, and
`no-single-body-satisfies-both` is NOT a measurement of it — it evaluates one
concrete instance, `length == 2 && length < 2`, and is an illustrative control
for the length pair only. Saying it evaluates the general claim would be the
overreach this file exists to refuse.

**It does not say `multibyte-shrinks` fails to discriminate, and stating it that
way would be false.** `multibyte-shrinks` is not a predicate over a pair; it
quantifies over a FUNCTION, and it separates `bytes-str` from `utf8-decode`
cleanly. The claim is narrower and worse:

> the property is not derived from the types. It is authored from knowledge of
> the intended ROLE, and it is false of a body that is provably correct at a
> different one.

So `multibyte-shrinks` cannot be adopted as a general obligation on
`(List Int) -> Str` — it would condemn `latin1-decode`, whose correctness has a
proof. It only works when the author already knows which of the two roles the
call site meant, and that is precisely the information the types do not carry
and a value cannot supply.

**This is the corrected reading of part 6's closing line.** That part ends at
*"the evidence for it had to be authored"*, which sounds like a scheduling
problem — nobody had got round to writing the property. It is not. The
information the property encodes is not recoverable from the artefact under
test at all, so no amount of diligence derives it; it has to be supplied from
outside, per call site, and being wrong about it is not detectable by anything
in this file.

### The falsification that looked like a closure, and was not

Stating the obvious universal — the decoder must agree with the reference —
falsifies cheat 1, which reads like F3 closing after all:

```lisp
(defn decode-cheat-agrees [] [(v (List Int))] Bool
  (== (utf8-decode-cheat v) (utf8-decode v))
  (prop holds [(v (List Int))] (decode-cheat-agrees v)))
```

```
✗ decode-cheat-agrees #b9e093eb8e89  FALSIFIED: holds
      counterexample: (Cons -17 Nil)
```

**The counterexample is class C, not class B.** Hand-checked:

```
$ oath eval '(utf8-valid (Cons [Int] -17 (Nil [Int])))'          false
$ oath eval '(utf8-decode       (Cons [Int] -17 (Nil [Int])))'   SNil
$ oath eval '(utf8-decode-cheat (Cons [Int] -17 (Nil [Int])))'   (SCons -17 SNil)
```

The two functions disagree on MALFORMED input, which is not the mojibake F3 is
about — the same trap this file already names for `corpus-roundtrip-decode`. The
control is a cheat that also defers to the real decoder outside `utf8-valid`:

```lisp
; --- THE CONTROL: a cheat that also defers to the real decoder outside the
; valid set. Written out in full rather than elided: every fenced lisp block in
; this file is extracted and replayed, and an elision does not parse. Two
; transcripts in this file were already broken by one.
(defn utf8-decode-cheat2 [] [(bs (List Int))] Str
  (if (utf8-valid bs)
      (if (== bs (utf8-encode "ключ")) (utf8-decode bs)
      (if (== bs (utf8-encode "é")) (utf8-decode bs)
      (if (== bs (utf8-encode "a-é-ключ")) (utf8-decode bs)
          (bytes-str bs))))
      (utf8-decode bs))

  ; The six properties of `utf8-decode-checked`, verbatim …
  (prop multibyte-shrinks [(v (List Int))]
    (if (and (utf8-valid v) (utf8-high v))
        (< (str-len (utf8-decode-cheat2 v)) (length [Int] v))
        true))
  (prop ascii-is-reinterpretation [(v (List Int))]
    (if (and (utf8-valid v) (not (utf8-high v)))
        (== (utf8-decode-cheat2 v) (bytes-str v))
        true))
  (prop decodes-cyrillic [] (== (utf8-decode-cheat2 (utf8-encode "ключ")) "ключ"))
  (prop decodes-e-acute [] (== (utf8-decode-cheat2 (utf8-encode "é")) "é"))
  (prop decodes-a-mixed-string []
    (== (utf8-decode-cheat2 (utf8-encode "a-é-ключ")) "a-é-ключ"))
  (prop inverts-the-encoder [(s Str)]
    (if (all [Int] (fn [(c Int)] (and (<= 0 c) (<= c 2047))) (str-bytes s))
        (== (utf8-decode-cheat2 (utf8-encode s)) s)
        true))
  ; … PLUS the agreement property that killed cheat 1.
  (prop agrees-with-the-real-decoder [(v (List Int))]
    (== (utf8-decode-cheat2 v) (utf8-decode v))))
```

```
✓ utf8-decode-cheat2 #20395d3aad41  tested (200 cases per property) · total
    prop multibyte-shrinks            passed 200 cases
    prop ascii-is-reinterpretation    passed 200 cases
    prop decodes-cyrillic             passed 200 cases
    prop decodes-e-acute              passed 200 cases
    prop decodes-a-mixed-string       passed 200 cases
    prop inverts-the-encoder          passed 200 cases
    prop agrees-with-the-real-decoder passed 200 cases

$ oath eval '(utf8-decode-cheat2 (utf8-encode "©"))'   (SCons 194 (SCons 169 SNil))
$ oath eval '(== (utf8-decode-cheat2 (utf8-encode "©")) "©")'   false
```

**All seven, and it still mojibakes.** So the earlier falsification was
incidental and closed nothing.

## Part 11 — the two failures the table was missing

#169 cites two further findings, and neither had a row. Both are failures of the
same conflation, and they behave differently from F1–F3 in ways the three-row
table could not show.

### F4 — the scalar range lives at the boundary, not in the type (#164)

**The kernel's behaviour here is REQUIRED, so it is not the failure**, and an
earlier heading that read "the scalar range is unchecked" invited the opposite
reading. SPEC §3 is explicit, and it is a decision rather than a gap:

> **CONSTRUCTION IS UNCHECKED, and a KERNEL MUST NOT reject a non-scalar element
> at construction.** `(SCons -1 (SNil))` is an ordinary value of the semantics in
> this section. […] The scalar range is therefore a property of the boundaries a
> `Str` CROSSES, not of the datatype.

Measured on all three paths — the kernel builds it, and both backends refuse it
at PACK. The program is a fixture like everything else here, so the transcript
below is reproducible rather than asserted:

```lisp
; The F4 boundary fixture. The entry protocol requires (-> (List Str) Str), so
; this takes argv and ignores it; the body is a Str the kernel builds happily.
(defn f4-main [] [(argv (List Str))] Str
  (SCons -1 (SNil))
  (prop builds [(argv (List Str))] (== (str-len (f4-main argv)) 1)))
```

```
$ oath eval '(SCons -1 (SNil))'                                (SCons -1 SNil) : Str
$ oath eval '(str-len (SCons -1 (SCons 55296 (SCons 1114112 (SNil)))))'      3 : Int

$ oath build f4-main -o f4go && ./f4go
oath: this backend cannot encode Str element -1 (negative): Str is packed as
UTF-8, which encodes only Unicode scalar values. Refusing rather than
substituting U+FFFD, which would make distinct Str values identical.
  exit=70

$ oath build f4-main --backend llvm -o f4llvm
error: the llvm-ir/1 backend cannot lower the Str element -1 — this backend
packs Str as UTF-8, which encodes only Unicode scalar values (0..0x10FFFF,
excluding surrogates 0xD800..0xDFFF). …
```

The property over the existing types is `str-scalar-ok`, and it is lam-free, so
unlike `secret-is-usable` it can be submitted as written.

```lisp
(defn str-scalar-ok [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest)
      (if (and (<= 0 c) (<= c 1114111))
          (if (and (<= 55296 c) (<= c 57343)) false (str-scalar-ok rest))
          false)))
  (prop empty-is-ok [] (str-scalar-ok (SNil)))
  (prop ascii-is-ok [] (str-scalar-ok "hi"))
  (prop cyrillic-is-ok [] (str-scalar-ok "ключ"))
  (prop negative-is-not [] (not (str-scalar-ok (SCons -1 (SNil)))))
  (prop surrogate-is-not [] (not (str-scalar-ok (SCons 55296 (SNil)))))
  (prop above-max-is-not [] (not (str-scalar-ok (SCons 1114112 (SNil))))))
```

```
✓ str-scalar-ok    #19d7a4e6ddb1  tested (200 cases per property) · total
$ oath prove str-scalar-ok                                             0s
    6/6 PROVEN, every one direct (lemma-free) (Z3, unbounded ints)
```

**And F4 is the one row generation is not blind to.** Part 4 measured only that
the tester draws no codepoint at or above 128; whether it draws NEGATIVE ones
was never asked, and it does:

```lisp
(defn generator-never-draws-a-nonscalar-str [] [(s Str)] Bool
  (str-scalar-ok s)
  (prop holds [(s Str)] (generator-never-draws-a-nonscalar-str s)))
```

```
✗ generator-never-draws-a-nonscalar-str #288996d90a92  FALSIFIED after 3 cases
      counterexample: (SCons -4 (SCons -19 SNil))
```

The adversarial body still passes the six witnessed properties — the taxonomy is
uniform that far — but here a single universal kills it, in two cases, with no
witness chosen by anyone:

```lisp
; The same adversarial-body test parts 7 and 8 apply. Rejects the three
; witnessed codepoints and admits every other non-scalar. The six properties
; are `str-scalar-ok`'s, verbatim.
(defn str-scalar-ok-cheat [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest)
      (if (or (== c -1) (or (== c 55296) (== c 1114112)))
          false
          (str-scalar-ok-cheat rest))))
  (prop empty-is-ok [] (str-scalar-ok-cheat (SNil)))
  (prop ascii-is-ok [] (str-scalar-ok-cheat "hi"))
  (prop cyrillic-is-ok [] (str-scalar-ok-cheat "ключ"))
  (prop negative-is-not [] (not (str-scalar-ok-cheat (SCons -1 (SNil)))))
  (prop surrogate-is-not [] (not (str-scalar-ok-cheat (SCons 55296 (SNil)))))
  (prop above-max-is-not [] (not (str-scalar-ok-cheat (SCons 1114112 (SNil))))))

; -2 is negative, 55297 is a surrogate, 1114113 is above the maximum — none is
; one of the three named values.
(defn cheat-admits-an-unwitnessed-nonscalar [] [] Bool
  (and (str-scalar-ok-cheat (SCons -2 (SNil)))
       (and (str-scalar-ok-cheat (SCons 55297 (SNil)))
            (str-scalar-ok-cheat (SCons 1114113 (SNil)))))
  (prop all-three-admitted [] (cheat-admits-an-unwitnessed-nonscalar)))

(defn real-guard-refuses-those [] [] Bool
  (or (str-scalar-ok (SCons -2 (SNil)))
      (or (str-scalar-ok (SCons 55297 (SNil)))
          (str-scalar-ok (SCons 1114113 (SNil)))))
  (prop none-admitted [] (not (real-guard-refuses-those))))

(defn cheat-agrees-with-the-real-guard [] [(s Str)] Bool
  (== (str-scalar-ok-cheat s) (str-scalar-ok s))
  (prop holds [(s Str)] (cheat-agrees-with-the-real-guard s)))

(defn cheat-is-sound-alone [] [(s Str)] Bool
  (if (str-scalar-ok-cheat s) (str-scalar-ok s) true)
  (prop holds [(s Str)] (cheat-is-sound-alone s)))
```

```
✓ str-scalar-ok-cheat #dcfc4702ea75  all six, verbatim, passed
✓ cheat-admits-an-unwitnessed-nonscalar #5270417b0071  -2, 55297 and 1114113 all admitted
✗ cheat-agrees-with-the-real-guard #43579e3a7a2f  FALSIFIED after 3 cases   (SCons -2 SNil)
✗ cheat-is-sound-alone #190f810ce63a             FALSIFIED after 2 cases   (SCons -18 (SCons 0 SNil))
$ oath prove cheat-is-sound-alone                                    116s   0/1 unproven
```

Both of those mention the correct guard, and that is stated rather than glossed:
F4 has no reference-free formulation the way `multibyte-shrinks` has one for F3,
because the scalar range IS the claim and there is no second observable to
compare against. What differs is REACH, not reference-freedom.

### Why F1 and F2 are not rescued the same way

The obvious next question is whether the same universal kills part 8's secret
cheat. It does not:

```lisp
(defn secret-cheat-agrees-with-shipped [] [(s Str)] Bool
  (== (secret-is-usable-cheat s) (secret-is-usable s))
  (prop holds [(s Str)] (secret-cheat-agrees-with-shipped s)))

(defn secret-cheat-is-sound-alone [] [(s Str)] Bool
  (if (secret-is-usable-cheat s) (secret-is-usable s) true)
  (prop holds [(s Str)] (secret-cheat-is-sound-alone s)))
```

```
✓ secret-cheat-agrees-with-shipped #12a03e227f8a  passed 200 cases
✓ secret-cheat-is-sound-alone      #dac6310d34e4  passed 200 cases
```

The reason is a **second blindness this file never measured, and it is a LENGTH
gate rather than a codepoint gate**. Both secret guards require sixteen
characters, so if the tester never draws a `Str` that long, both are false and
they agree vacuously. The third property is the control that keeps this from
being a statement about a broken guard — PASSING means never, so the falsified
row is the one showing the tester DOES draw non-empty values:

```lisp
(defn generator-never-draws-a-long-str [] [(s Str)] Bool
  (< (str-len s) 16)
  (prop holds [(s Str)] (generator-never-draws-a-long-str s)))

(defn generator-never-draws-eight [] [(s Str)] Bool
  (< (str-len s) 8)
  (prop holds [(s Str)] (generator-never-draws-eight s)))

(defn generator-never-draws-nonempty [] [(s Str)] Bool
  (== (str-len s) 0)
  (prop holds [(s Str)] (generator-never-draws-nonempty s)))
```

```
✓ generator-never-draws-a-long-str #b2891f2f7ebe   passed   (never reaches 16)
✓ generator-never-draws-eight      #6c9f123186c8   passed   (never reaches 8)
✗ generator-never-draws-nonempty   #536326ab30d3   FALSIFIED after 4 cases
      counterexample: (SCons -3 (SCons 12 (SCons -17 SNil)))
```

The third row is the control that keeps this from being a statement about a
broken guard: the tester does draw non-empty `Str` values, just short ones. So
a domain-aware generator would have to widen on TWO axes here, not one, and the
file's closing paragraph about domain-aware generation was reasoning from the
codepoint axis alone.

### F5 — the misleading range message (#167)

#169's citation is that `byte list element out of range 0..255` *was describing a
broken representation as a semantic fact*. The split has landed
(`oath/compile.go:835-841`), and it separates three classes that the one message
had merged:

**The disposition differs by EXECUTION PATH, and a first draft gave one column
for two paths.** Review caught it: the `oath eval` transcript below exits 1
through `oath/eval.go:451`, and `oathRefuse`/70 is what `oath/compile.go` emits
into a COMPILED artifact. Both are measured:

| class | reachable from well-typed Oath? | under `oath eval` | in a compiled artifact |
|---|---|---|---|
| a `(List Int)` element that is not an `Int` | **no** — the checker types it | check-time error, exit 1 | `panic` (a bug report, unreachable) |
| a byte-list element outside `0..255` | yes | `eval.go:451` error, exit 1 | `oathRefuse`, exit **70** |
| a `Str` element outside the scalars | yes, at PACK | none — the value is BUILT | `oathRefuseStrElement`, exit **70** |

```lisp
; The F5 compiled-path fixture. The refusal must be on a branch the PROPERTY does
; not reach, or `put` records the definition as having no verdict and `oath
; build` refuses it for having no verified properties. So the property covers
; the empty-argv branch only, and passing any argument takes the other one —
; which also gives the two-way control: the same binary exits 0 when the refusal
; is not reached.
(defn f5-main [] [(argv (List Str))] Str
  (match argv
    ((Nil) (hex-encode (hmac-sha256 (str-bytes "ok") (str-bytes "{}"))))
    ((Cons a rest)
      (hex-encode (hmac-sha256 (Cons [Int] 256 (Nil [Int])) (str-bytes "{}")))))
  (prop empty-argv-digests [] (== (str-len (f5-main (Nil [Str]))) 64)))
```

```
$ oath eval '(Cons [Int] "x" (Nil [Int]))' ; echo $?
error: expected Int, got #e6bbed8bc934
1
$ oath eval '(hex-encode (hmac-sha256 (Cons [Int] 256 (Nil [Int])) (str-bytes "{}")))' ; echo $?
error: byte list element out of range 0..255
1

$ oath build f5-main -o f5go
$ ./f5go ; echo $?   # empty argv: the refusal is not reached
                     # (control: the exit-70 below is not unconditional)
0
$ ./f5go trigger ; echo $?
oath: byte list element out of range 0..255
70
```

The third row's empty `oath eval` cell is the point of F4, not an omission: the
kernel BUILDS a non-scalar `Str` and never refuses it, exactly as SPEC §3
requires.

**This row has no property, and the reason is structural rather than a gap in
effort.** The falsifier asks for *a property over the existing types*, and a
property's subject is an Oath value. Neither half of F5 is one:

- The representation class has **no well-typed witness**. It is refused at check
  time, so no property can quantify over it — there is nothing in the domain.
- A refusal is **not an Oath value**. `oath/compile.go` says so at the type that
  implements it — *"It is not an Oath value, and no Oath code can observe it,
  branch on it or continue past it"* — and there is no `catch`, `try` or
  `recover` in the surface. So no property can say "this call refuses", let
  alone "it refuses with the right message".

A property CAN predict a refusal — `bytes-ok v` is exactly that, and it is what
closes F1. What has no Oath-level subject is the thing #167 is about: **which
class a diagnostic names.** The falsifier's sentence does not type here, and
recording the row as "unclosed" would overstate it in the other direction, as
though a property existed and had failed.

## Part 12 — `require_proven`, a closure route this file never tested

Part 5 measured `forbid_falsified` and found it powerless against a body that is
not falsified. It did not measure the policy flag next to it. `oath/policy.go:37`:

```go
RequireProven bool `json:"require_proven,omitempty"` // name only binds once EVERY property is SMT-proven (#14)
```

That flag turns part 9's asymmetry into a gate. Soundness gives the direction
that matters for free — a false property is never proven — so requiring an
obligation PROVEN excludes every body the obligation is false of, without the
prover ever having to detect one.

**Read that sentence exactly, because a first draft of this part did not.** It
says the gate excludes bodies the ATTACHED obligation is false of. It does NOT
say the gate excludes incorrect bodies, and the difference is the whole of the
correction below: an obligation can be provable, attached, and still true of a
wrong body. Measured end to end, one name, two bodies, and the contract is
`usable-encodes` alone:

```
=== 1. bind the correct body and PROVE it ===
   name     -> 688f043be371
∎ PROVEN    usable-encodes               induction on binder 0 (Z3, unbounded ints)
proven: 1/1 properties

=== 2. apply require_proven ===
{ "rules": [ { "names": ["*"], "require_proven": true } ] }

=== 3. submit the CHEAT under the same name ===
⏳ the-guard   PENDING PROOF: policy: this name requires proven properties; queued
               for the verification worker (name unchanged until proof lands)
   name     -> 688f043be371
   journal   pending f44aae41b25d

=== 4. run the verification worker to completion (--once) ===
· the-guard   #f44aae41b25d  tested (200 cases per property)
⛔ the-guard   name not bound — proof did not complete: not all properties proven
               (re-queue to retry a transient timeout)
prove-worker: drained (1 job(s) this pass); queue depth 0
   name     -> 688f043be371
   journal   accepted f44aae41b25d
   journal   blocked  f44aae41b25d

=== 5. CONTROL — the correct body is still admitted under the same policy ===
✓ the-guard   #688f043be371  PROVEN (all 1 properties, Z3 over unbounded ints) · total
   name     -> 688f043be371
```

| store policy | the cheat's fate | the name afterwards |
|---|---|---|
| none | accepted | **the cheat** |
| `forbid_falsified` | accepted — it is not falsified | **the cheat** |
| `require_proven` | `pending`, then `blocked` | **unmoved — the correct body** |

**That table is about THIS cheat under THIS contract**, and generalising it is
what the next section had to undo.

**`--once` is part of the command, not decoration.** Bare `prove-worker` polls on
an interval and never returns, so a foreground run reads as a hang rather than as
a result; the first attempt at this measurement was abandoned for that reason.

### The two ways a first draft of this part overreached, both found by review

The paragraph that stood here claimed this DISCHARGED the obligation for F1, F2
**and F4**. Review objected that the gate had been demonstrated on one property
attached to one name and the conclusion drawn for obligations living in OTHER
definitions. Both objections were checked by measurement, both held, and one of
them flips a row.

**`require_proven` requires the properties THAT NAME CARRIES, and nothing else.**
So the question is never "is the obligation provable" but **does proving the
attached contract logically exclude the failure.** That is the discriminator,
and it is not the same for the three rows.

**F2 — the gate above did not require F2's obligation at all.**
`faithful-under-shipped-p` is a separate definition, so a policy on `the-guard`
never touched it. The counterexample is not adversarial; it is part 3's middle
ladder row, the obvious repair the crash prompts:

```lisp
; Admits every codepoint in 0..255 and refuses everything above. It SATISFIES
; `usable-encodes` — every admitted value is in byte range — so a gate on that
; property alone binds it, while its digest disagrees with the sender.
(defn byte-range-cps [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest)
      (if (and (<= 0 c) (<= c 255)) (byte-range-cps rest) false)))
  (prop ascii-is-ok [] (byte-range-cps "abcdef0123456789"))
  (prop latin1-is-ok [] (byte-range-cps "©©©©©©©©©©©©©©©©"))
  (prop cyrillic-is-not [] (not (byte-range-cps "ключ"))))

(defn guard-byte-range-p [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (byte-range-cps secret))
  (prop usable-encodes [(s Str)]
    (if (guard-byte-range-p s) (bytes-ok (str-bytes s)) true)))

(defn faithful-under-byte-range-p [] [(s Str)] Bool
  (if (guard-byte-range-p s) (signs-the-senders-bytes-p s) true)
  (prop holds [(s Str)] (faithful-under-byte-range-p s)))

(defn byte-range-guard-admits-the-latin1-secret [] [] Bool
  (guard-byte-range-p "©©©©©©©©©©©©©©©©")
  (prop admitted [] (byte-range-guard-admits-the-latin1-secret)))

(defn byte-range-guard-is-unfaithful [] [] Bool
  (faithful-under-byte-range-p "©©©©©©©©©©©©©©©©")
  (prop the-check-fires [] (not (byte-range-guard-is-unfaithful))))
```

```
✓ guard-byte-range-p #81122934275a  usable-encodes  passed 200 cases
✓ byte-range-guard-admits-the-latin1-secret #a1c7bf3baa93  admitted        passed
✓ byte-range-guard-is-unfaithful #4dd6429698a0  the-check-fires            passed

$ oath prove guard-byte-range-p                                        7s
    ∎ PROVEN    usable-encodes               induction on binder 0 (Z3, unbounded ints)
```

**It PROVES `usable-encodes` in seven seconds and would bind**, admitting a
sixteen-`©` secret whose digest `openssl` disagrees with — sixteen because that
is the guard's minimum length, and the literal actually run. Part 3 already
measured that this guard leaves F2 open; what is new is that a proof gate on
`usable-encodes` does not notice.

**The repair is to attach the obligation to the guarded name**, which is what
review asked for, and it works — measured on all three guards, each carrying
BOTH properties on its own name:

```lisp
(defn guard-ascii-both [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (printable-ascii-cps secret))
  (prop usable-encodes [(s Str)]
    (if (guard-ascii-both s) (bytes-ok (str-bytes s)) true))
  (prop signs-the-senders-bytes [(s Str)]
    (if (guard-ascii-both s) (== (str-bytes s) (utf8-encode s)) true)))

(defn guard-byterange-both [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (byte-range-cps secret))
  (prop usable-encodes [(s Str)]
    (if (guard-byterange-both s) (bytes-ok (str-bytes s)) true))
  (prop signs-the-senders-bytes [(s Str)]
    (if (guard-byterange-both s) (== (str-bytes s) (utf8-encode s)) true)))

(defn guard-cheat-both [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (not-the-two-witnessed-ranges secret))
  (prop usable-encodes [(s Str)]
    (if (guard-cheat-both s) (bytes-ok (str-bytes s)) true))
  (prop signs-the-senders-bytes [(s Str)]
    (if (guard-cheat-both s) (== (str-bytes s) (utf8-encode s)) true)))
```

```
$ oath prove guard-ascii-both       #8b1370bc3017                    121s   2/2 PROVEN
$ oath prove guard-byterange-both   #ef77a42ad000                    killed at 900s — NO OUTPUT
$ oath prove guard-cheat-both       #77af9ab3f5b3                     19s   0/2 unproven
```

Only the correct guard binds. **But read the middle row exactly:**
`guard-byterange-both` is excluded by NO VERDICT, not by refutation — the same
reason F3's honest decoder is excluded. The gate admits what proves; it does not
detect a defect. That is sound and it is weaker than it looks.

**F4 — this row does NOT close, and the first draft said it did.** The claim was
made by analogy to F1, which is the exact move this file exists to refuse. The
measurement:

```
$ oath prove str-scalar-ok-cheat    #dcfc4702ea75                      0s   6/6 PROVEN
```

**The cheat proves its whole contract and binds.** All six properties are closed
facts about three named values, and proving them says nothing about the fourth.
Contrast F1, where `usable-encodes` is universally quantified over the admitted
set, so proving it EXCLUDES the failure by construction rather than by covering
witnesses.

And review's second point is the deeper one: proving `str-scalar-ok` certifies a
CLASSIFIER. It does not stop `(SCons -1 (SNil))` being constructed — SPEC §3
forbids a kernel from stopping it — and nothing requires any packing boundary to
consult the classifier. F4's obligation has **no Oath-observable consequence** to
state, because PACK is not an Oath operation, so the contract can only be the
predicate restating itself.

**So the corrected reading, before the third overreach below is added to it:
`require_proven` discharges and gates F1's obligation, and F2's only when the
faithful-encoding property is attached to the guarded name. No BEHAVIOURAL
contract measured here does the same for F3 or F4 — the next section shows what
does, and why it is worth less than it sounds. **None of this decides whether a
row is CLOSED** — that is whether a property EXISTS, settled in the outcome
section, and parts 5 and 6 already answered it for F3.

### The third overreach: F3 and F4 were marked unclosed without submitting the
### one contract that would close them

Review again, and again correct. The `no` verdicts for F3 and F4 rested on
`multibyte-shrinks` and on six witness facts. Neither is the contract that NAMES
the correct implementation — and under `require_proven` the generator's blindness
is irrelevant, because a false universal cannot prove however few cases anyone
draws. So it had to be submitted.

```lisp
(defn ref-decoder-good [] [(bs (List Int))] Str
  (utf8-decode bs)
  (prop agrees-with-the-reference [(v (List Int))]
    (== (ref-decoder-good v) (utf8-decode v))))

(defn ref-decoder-cheat [] [(bs (List Int))] Str
  (if (utf8-valid bs)
      (if (== bs (utf8-encode "ключ")) (utf8-decode bs)
      (if (== bs (utf8-encode "é")) (utf8-decode bs)
      (if (== bs (utf8-encode "a-é-ключ")) (utf8-decode bs)
          (bytes-str bs))))
      (utf8-decode bs))
  (prop agrees-with-the-reference [(v (List Int))]
    (== (ref-decoder-cheat v) (utf8-decode v))))

(defn ref-guard-good [] [(s Str)] Bool
  (str-scalar-ok s)
  (prop agrees-with-the-reference [(t Str)]
    (== (ref-guard-good t) (str-scalar-ok t))))

(defn ref-guard-cheat [] [(s Str)] Bool
  (match s
    ((SNil) true)
    ((SCons c rest)
      (if (or (== c -1) (or (== c 55296) (== c 1114112)))
          false
          (ref-guard-cheat rest))))
  (prop agrees-with-the-reference [(t Str)]
    (== (ref-guard-cheat t) (str-scalar-ok t))))
```

```
✓ ref-decoder-good  #0ea992496a86  tested · total
✓ ref-decoder-cheat #b10a9a0f6bc1  tested · total
✓ ref-guard-good    #7715a558e246  tested · total
✗ ref-guard-cheat   #bade3c63e45d  FALSIFIED after 4 cases   (SCons -6 SNil)

$ oath prove ref-decoder-good                                          0s   ∎ PROVEN  direct
$ oath prove ref-decoder-cheat                                        10s   · unproven
$ oath prove ref-guard-good                                            0s   ∎ PROVEN  direct
$ oath prove ref-guard-cheat                                         172s   · unproven
```

**Neither row closes on this contract, and the honest statement of WHY is the
whole value of the measurement.** The gate is real — an incorrect body cannot
take the gated name — and in both cases the gated name is not where the failure
lives. For F4 it protects the CLASSIFIER, while nothing requires a producer to
call one. For F3 it protects a DECODER, while part 6 establishes that F3
happened inside `json-scoped-string`, which calls `bytes-str` and keeps its own
name untouched by any contract on a decoder.

> The contract is `(== (f v) (utf8-decode v))`. The correct body proves it **by
> reflexivity, directly, in zero seconds — because the body IS `utf8-decode`.**

So the gate works and certifies nothing. It says *this name resolves to that
name*; it establishes no property of `utf8-decode` itself, and it is only
available to someone who has already written the correct decoder and knows to
point at it. `ref-decoder-cheat` still returns `(SCons 194 (SCons 169 SNil))`
for `©` and `ref-guard-cheat` still admits `(SCons -2 (SNil))` — they are
excluded from the NAME, not corrected.

### F4 AGAIN: the contract belongs at the PRODUCER, and there it closes

Review a third time, and a third correction to the same row. The `no` above was
reached from two places the contract does not belong — the classifier's witness
list, and the classifier's own name. **Neither is where the failure lives.** A
postcondition on whatever BUILDS the `Str` is still a property over the existing
types, it names a specification predicate rather than a correct implementation,
and it does not require the producer to call that predicate at all:

```lisp
(defn safe-chr [] [(n Int)] Str
  (if (and (<= 0 n) (<= n 1114111))
      (if (and (<= 55296 n) (<= n 57343)) (SNil) (SCons n (SNil)))
      (SNil))
  (prop result-is-scalar [(n Int)] (str-scalar-ok (safe-chr n)))
  (prop keeps-an-ordinary-codepoint [] (== (safe-chr 233) (SCons 233 (SNil)))))

(defn unsafe-chr [] [(n Int)] Str
  (SCons n (SNil))
  (prop result-is-scalar [(n Int)] (str-scalar-ok (unsafe-chr n)))
  (prop keeps-an-ordinary-codepoint [] (== (unsafe-chr 233) (SCons 233 (SNil)))))
```

```
✓ safe-chr         #2781556bf785  tested (200 cases per property) · total
✗ unsafe-chr       #1ef28650bd84  FALSIFIED: result-is-scalar
    prop result-is-scalar         FALSIFIED after 0 cases   counterexample: -18

$ oath prove safe-chr                                                  0s   2/2 PROVEN
$ oath prove unsafe-chr                                              301s   1/2
    · unproven  result-is-scalar             no direct proof; induction did not discharge
    ∎ PROVEN    keeps-an-ordinary-codepoint  direct (lemma-free) (Z3, unbounded ints)
```

**So F4's obligation IS behavioural and DOES discharge** — `str-scalar-ok
(producer n)` constrains what the producer may return, names no correct
implementation, and is writable by someone who has never seen one. `unsafe-chr`
cannot bind under `require_proven`; `safe-chr` can, and generation falsifies the
unsafe one in zero cases besides.

**WHERE F4 LANDED, and it moved three times before it settled.** The row reads
**CLOSED**. What settled it is the criterion, applied consistently: the
falsifier asks whether a property over the existing types exists that would have
caught the failure, and `str-scalar-ok (producer n)` is one — it proves, it
names no implementation, and generation falsifies the unsafe producer in zero
cases. The three earlier answers each measured something real and then answered
a different question:

    witnesses PROVE 6/6            — but the cheat proves the same six, because
                                     all six are closed facts about three values
    the classifier's NAME gates    — but nothing obliges a producer to call it
    the producer's postcondition   — this is the one the falsifier asks about

**And a policy still cannot require it of EVERY producer**: the gate binds
`safe-chr`, while this file's own `f4-main` builds `(SCons -1 (SNil))` under an
unrelated provable property and compiles until PACK refuses it at exit 70. That
is a limit on ENFORCEMENT, and enforcement is not what the falsifier asks about
— reading it as one is how the row came to be marked `no` twice.

**And it retires a claim made twice above: that F4's obligation has "no
Oath-observable consequence to state" because PACK is not an Oath operation.**
That was wrong. The consequence does not need PACK — it is the property of the
`Str` the producer RETURNS, and `str-scalar-ok` states it. The earlier reasoning
looked for the obligation at the boundary where the failure is REPORTED instead
of where it is INTRODUCED.

**And this sharpens part 10 rather than softening it — at the precision the
measurement supports, which is narrower than the first draft of this paragraph.**
Of the contracts submitted, the one that gates F3 is the one that names the
correct decoder, which is the ROLE supplied from outside, exactly as part 10
says it must be. **What is NOT established is that no behavioural contract
exists**: a property specifying UTF-8 semantics directly, without naming
`utf8-decode`, was never written here. Two submissions are not a search. The
supportable sentence is that F1 and F2 HAVE such a contract and it proves, and
that F3's was not found — not that it cannot be.

### THE GATE DOES NOT PRESERVE THE OBLIGATION, and this qualifies every `yes`

The controls above all resubmit the SAME contract under a new body. Review asked
what happens when a wrong body simply DELETES the obligation and attaches
something it can discharge — `require_proven` reads the properties the SUBMITTED
definition carries and does not compare them to the ones the name already held.
Measured, with the Latin-1-admitting guard and a vacuous property in place of
`usable-encodes`:

```
; NOT a `lisp` fence, deliberately: every lisp block in this file is extracted
; and replayed, and the policy controls' definitions live inside their shell
; scripts instead — as `the-decoder`'s already do. Fencing this as lisp would
; bind `the-guard` in the replay store and make the manifest disagree with the
; note about which names the replay does not create.
(defn the-guard [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (byte-range-cps secret))
  (prop is-a-predicate [] (== (the-guard (SNil)) (the-guard (SNil)))))
```

```
1. bound the proven guard      -> 688f043be371
∎ PROVEN    usable-encodes               induction on binder 0
2. submit the body that DELETED the obligation
   ⏳ the-guard   PENDING PROOF: …
3. prove-worker --once
   ✓ the-guard   #b92a2b0434ff  PROVEN (all 1 properties, Z3 over unbounded ints)
   ✓ the-guard   name bound after proof (was 688f043be371)
   name -> b92a2b0434ff   journal -> accepted

$ oath eval '(the-guard "©©©©©©©©©©©©©©©©")'                    true : Bool
```

**The name moved, and the guard it now names is F2, live.** A vacuous property
proves trivially, so the policy reports everything green while the contract that
made the previous body safe is simply gone. **So every discharge result in this
part holds under an assumption the demonstrated gate does not enforce: that the
obligation stays attached.**

**A SECOND FLAG RESTORES THE MEASURED ATTACK, and no more than that.** Adding
`min_mutation_score` blocks it, because a vacuous property kills nothing:

```
{ "rules": [ { "names": ["*"], "require_proven": true, "min_mutation_score": 1.0 } ] }

   ⛔ the-guard  BLOCKED: policy: spec strength 0/6 (0.00 incl. 0 waived) below required 1.00
   name -> 688f043be371   journal -> blocked
```

**Read that at exactly its width.** It blocks THIS attack — a replacement
property that kills 0 of 6 generated mutants. It does not establish that the
obligation is preserved in general, because a mutation score measures **the
generator's reach, not what the specification excludes** — the documented
caveat this file already turns on. A replacement property that is weak but not
vacuous, killing all six generated mutants while saying nothing about
encoding, is not ruled out by anything measured here and was not attempted.

**No policy field preserves a property set.** `oath/policy.go:27-46` carries
`require_authorship_separation` (spec author ≠ body author),
`require_total`, `forbid_falsified`, `min_mutation_score`, `require_proven`,
`owner_pubkey` and `trust_on_first_publish`. Ownership fields restrict WHO may
repoint; none of them constrains the contract a repoint may carry.

#### The deleted-obligation control, runnable

Both halves above, in one script — the attack under `require_proven` alone, then
the same attack with `min_mutation_score` added. Run from the repository root,
passing the directory the blocks were extracted into.

```sh
#!/bin/sh
set -e
unset OATH_BACKEND                       # cloud IGNORES OATH_STORE (store.go:49)
B="$1"; W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
cp -R codebase "$W/store"; export OATH_STORE="$W/store"; O=./oath/oath
for i in 00 01 06 12 23; do $O put "$B/block$i.oath" --new >/dev/null; done
name() { python3 -c "import json;print(json.load(open('$W/store/names.json')).get('the-guard','(unbound)')[:12])"; }

sed 's/secret-is-usable-p/the-guard/g' > "$W/good.oath" <<'EOF'
(defn secret-is-usable-p [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (printable-ascii-cps secret))
  (prop usable-encodes [(s Str)]
    (if (secret-is-usable-p s) (bytes-ok (str-bytes s)) true)))
EOF
# THE ATTACK: the Latin-1-admitting guard with the obligation DELETED and a
# vacuous property in its place. `byte-range-cps` comes from block23.
cat > "$W/dropped.oath" <<'EOF'
(defn the-guard [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (byte-range-cps secret))
  (prop is-a-predicate [] (== (the-guard (SNil)) (the-guard (SNil)))))
EOF

$O put "$W/good.oath" --new >/dev/null; GOOD=$(name)
$O prove the-guard >/dev/null; echo "bound + proven -> $GOOD"

echo '{ "rules": [ { "names": ["*"], "require_proven": true } ] }' > "$W/store/policy.json"
$O put "$W/dropped.oath" >/dev/null 2>&1 || true
$O prove-worker --once 2>&1 | grep -E 'bound|drained'
echo "require_proven only          -> $(name)"
[ "$(name)" = "$GOOD" ] && { echo "FAIL: expected the name to MOVE here"; exit 1; }
$O eval '(the-guard "©©©©©©©©©©©©©©©©")' | sed 's/^/  and it admits 16x-copyright: /'

$O put "$W/good.oath" >/dev/null; echo "reset                        -> $(name)"
echo '{ "rules": [ { "names": ["*"], "require_proven": true, "min_mutation_score": 1.0 } ] }' > "$W/store/policy.json"
out=$($O put "$W/dropped.oath" 2>&1) || true
printf '%s\n' "$out" | grep -q 'BLOCKED' || { echo "FAIL: expected BLOCKED"; exit 1; }
printf '%s\n' "$out" | head -1
echo "+ min_mutation_score         -> $(name)"
[ "$(name)" = "$GOOD" ] || { echo "FAIL: the name moved despite the score gate"; exit 1; }
echo "BOTH CONTROLS BEHAVED AS DOCUMENTED"
```

Its output, run as printed:

```
bound + proven -> 688f043be371
✓ the-guard        #b92a2b0434ff  PROVEN (all 1 properties, Z3 over unbounded ints)
✓ the-guard        name bound after proof (was 688f043be371)
prove-worker: drained (1 job(s) this pass); queue depth 0
require_proven only          -> b92a2b0434ff
  and it admits 16x-copyright: true : Bool
reset                        -> 688f043be371
⛔ the-guard        BLOCKED: policy: spec strength 0/6 (0.00 incl. 0 waived) below required 1.00
+ min_mutation_score         -> 688f043be371
BOTH CONTROLS BEHAVED AS DOCUMENTED
```

**Both branches are ASSERTED, including the one where the attack SUCCEEDS.** A
control that only checked for blocking would pass on a store where nothing binds
at all; requiring the name to MOVE under `require_proven` alone is what makes the
second half mean something.

**The price, recorded rather than judged.** `require_proven` requires EVERY
property proven, and the corpus's own `secret-is-usable` proves 0 of 5 because
four of them are lam-blocked. So the flag as it stands would refuse the shipped
guard too, so discharging F1's obligation at all means re-spelling the guard
lam-free — a different definition, a different hash.

## What was tested, and what was not

**Tested:**

- F1 is caught by a property over existing types (`bytes-ok (str-bytes s)`), and
  the crash is reproduced at `oath eval`.
- F2 is NOT caught by that property — measured at a named witness, against
  `openssl`'s digest as an external oracle — and IS caught by the shipped
  printable-ASCII guard, which refuses class B outright.
- **Neither F1's nor F2's guard is CLOSED under the adversarial-body standard
  BY THE GUARANTEE LADDER ALONE.** A guard rejecting exactly the two witnessed
  codepoint ranges passes all five generated checks of
  of the receiver's properties, admits a thirty-two-`©` secret, and produces a
  digest `openssl` disagrees with. **Part 12 EXCLUDES both cheats under a store
  POLICY** — neither can prove the obligation, so `require_proven` will not let
  it hold the name. That is a different mechanism, not a contradiction of this
  row, and it is exclusion of the measured cheats rather than closure of the
  rows: the same part shows a replacement that DELETES the obligation proves a
  vacuous property and takes the name anyway. Neither result decides whether the
  row is CLOSED — whether a property exists does, in the outcome section.
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
- **The GUARANTEE LADDER does not exclude a mojibaking decoder.** A body that
  special-cases the three witnessed inputs and reinterprets everything else
  passes all six generated checks, carries the same `tested · total` line as the
  correct decoder, is untouched by `forbid_falsified`, and returns `Â©` for `©`.
  **That is a fact about the evidence layer, not F3's row** — an earlier version
  of this bullet read "F3 is NOT closed" and contradicted the outcome section,
  which marks F3 CLOSED on a property that was written and run in part 6.
- Neither evidence route discharges the universal property that would exclude
  it: generation never enters class B, and `oath prove` does not discharge the
  round-trip (one attempt unproven, one killed at ten minutes). **Part 9 adds
  the attempt this bullet's scope excluded** — `multibyte-shrinks` itself,
  killed at 900s twice. **That is a statement about `multibyte-shrinks` alone,
  not about F3's row**, which the outcome section marks CLOSED — a property
  exists and part 6 ran it. What proof and policy bear on is ENFORCEMENT, and
  there the gate protects a decoder's name while F3 happened in
  `json-scoped-string`.
- The killing was done entirely by the three explicit witnesses. Both
  universally quantified properties in the same contract passed on the mutant,
  including the round-trip property an author would write without knowing about
  this issue.
- The generated-case tester never enters class B in 200 cases per property, so
  every class-B property here passes vacuously under the corpus's default
  evidence route.
- The one property the corpus states about `bytes-str` is satisfied by the
  reinterpretation and falsified by a correct decoder, and `oath mutate` scores
  that specification 1/1.

**Tested in parts 9–12, and three of these REPLACE a claim above rather than
adding to it:**

- **The prover DOES discharge three of the four behavioural obligations.**
  `usable-encodes` (3s), F2's `faithful-under-shipped-p` (14s) and
  `str-scalar-ok` (6/6, 0s) all prove. This replaces the bullet that used to
  read *"the prover does not discharge the class-B properties"*, which was
  written when only the decoder round-trip had been attempted.
  **Discharging is not closing, and F4 is where the two come apart** — its
  witness cheat proves the identical 6/6 contract, because all six properties
  are closed facts about three named values. What closes F4 is a DIFFERENT
  contract, `str-scalar-ok` as a postcondition on the producer, and part 12 has
  it: `safe-chr` proves 2/2, `unsafe-chr` cannot prove `result-is-scalar`.
- **F3's obligation does not return a verdict.** `oath prove
  utf8-decode-checked` was killed at 900s twice, with no output at all. That is
  an implementation limit; unproven is not disproof, and no-verdict is weaker
  still.
- **`require_proven` is a real gate, and it does NOT preserve the obligation.**
  Measured with a two-way control: an unprovable cheat is queued, fails, and is
  `blocked` with the name unmoved, while the correct body is admitted — and a
  PROVABLE body binds, so `pending` is genuinely resolved rather than mistaken
  for refusal. But a body that DELETES the contract and attaches a vacuous
  property proves 1/1 and takes the name, after which the guard admits the
  sixteen-`©` secret. `min_mutation_score` blocks that particular replacement.
  **None of this decides which rows are CLOSED** — that is the role-blind
  criterion's job, in the outcome section, and four drafts of a duplicate here
  were wrong. Two facts worth carrying: the gate consults only the properties
  the SUBMITTED definition carries, and a contract of the form
  `(== (f v) (reference v))` proves by REFLEXIVITY in 0s, excluding wrong bodies
  while certifying nothing.
- **The prover never REFUTED any cheat**, though `REFUTED` is in its vocabulary
  and all three obligations were hand-checked false.
- **`multibyte-shrinks` is role-dependent, not type-derived.** It is false of
  `latin1-decode` — the same body as `bytes-str` — whose correctness at the
  Latin-1 role is PROVEN. The two role obligations are jointly unsatisfiable at
  `[195,169]`.
- **Generation reaches F4's failure domain and not the others'**, and the secret
  guards are additionally blind by `Str` LENGTH, not only by codepoint.
- **F5 has no property at all**, for a structural reason: the representation
  class has no well-typed witness and a refusal is not an Oath value.

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

### What "closed" means, and one thing it does NOT mean

**"Closed" is not left open, and three drafts of this section behaved as though
it were.** They offered three readings — a property is *writable*, a property is
*discharged*, the wrong body is *unwritable* — and made the verdict depend on
which a reader picked. **A falsifier whose outcome turns on a definition the
author supplies is not a falsifier.** #169's own words fix the reading:

> can be **closed by a property over the existing types**

so the question per row is: **does a property exist, stated in terms of `Str`,
`(List Int)` and ordinary Oath definitions, that would have caught the mistake
that actually happened?** Everything else this file measured — whether such a
property can be PROVEN, and whether a store policy can make it BIND — is
evidence about **discharge and enforcement**, reported under those headings and
deciding no row.

**AND #159'S EXCLUSION TEST DOES NOT DISQUALIFY PROPERTIES — a fourth draft of
this section used it that way and was wrong twice over.** The test reads:

> a mechanism is OUTSIDE that result iff how it classifies a value is not solely
> a function of the ORIGINAL, UNCHANGED `(List Int)`

Two errors are easy here, and this file made both in one edit:

- **It is an EXCLUSION test, not a sufficiency test.** Failing it means a
  mechanism is NOT REFUTED by #159; passing it disqualifies nothing.
  `CLAUDE.md` names inverting this as "the overclaim this file keeps having to
  correct".
- **Its subject is the MECHANISM'S CLASSIFICATION, not the ROLE.**
  `issue-159-refinements.md` is explicit that writing ROLE as the subject makes
  the criterion vacuous, *"because the role is never a function of the value,
  that being the defect itself"*.

And its domain is candidate TYPE MECHANISMS — *what type a value has, and
therefore what it may be passed to* — not properties. So it says where a type
candidate may come from. It cannot be turned into a filter on which properties
count, and F3 is not unclosed by it.

### The exhaustive table

Every failure mode #169 cites, one row each, with the candidate property, the
role-blind test applied to it, and the result.

| | failure | a property over the existing types that would have caught it | run? | CLOSED? |
|---|---|---|---|---|
| **F1** | class C — a Cyrillic secret reaches `hmac-sha256`; the request dies | `usable-encodes`: `∀s. guard s ⟹ bytes-ok (str-bytes s)` | part 3; PROVEN in part 9 | **yes** |
| **F2** | class B — a Latin-1 secret; the digest silently disagrees with the sender | `signs-the-senders-bytes`: `∀s. guard s ⟹ str-bytes s == utf8-encode s`. NOT `usable-encodes` — `©` is 169, so `bytes-ok` holds while the digest still differs | part 3; PROVEN in part 9 | **yes** |
| **F3** | class B — a UTF-8 repository name through `bytes-str`; mojibake in the log | `cyrillic-repo-round-trips` on the extractor (part 6), and `(== (f v) (utf8-decode v))` on a decoder (part 12) | both run; part 6's FALSIFIES the receiver's own body | **yes — and read the next section, which is the substantive result** |
| **F4** | #164 — the scalar range lives in HOST DISCIPLINE rather than in the type, so a program can build a `Str` it cannot PACK. **The kernel constructing `(SCons -1 (SNil))` is SPEC-MANDATED (§3) and is NOT the defect** | `str-scalar-ok` — and, at the place the value is INTRODUCED, `str-scalar-ok (producer n)` as a postcondition | part 11; `str-scalar-ok` PROVEN 6/6, `safe-chr` PROVEN 2/2, `unsafe-chr` cannot prove `result-is-scalar` | **yes** |
| **F5** | #167 — a representation defect reported as `out of range 0..255`. **A DIAGNOSTIC AND REPRESENTATION FAILURE**, and in scope: #169 cites it as one of the two findings that sharpen the conflation, and the merged message existed BECAUSE bytes and text share a shape | **NONE EXISTS AND NONE CAN BE WRITTEN.** The representation half has no well-typed witness — `oath eval '(Cons [Int] "x" (Nil [Int]))'` fails at check time, so the domain is empty. The diagnostic half's subject is host text, and a refusal is not an Oath value: no `catch`, no `try`, no `recover` | n/a — nothing to run | **no** |

> **THE FALSIFIER DOES NOT FIRE. #169 is not declined on this argument.**

It requires EVERY failure mode this conflation produces to be closed by a
property over the existing types. **Four are. F5 is not, and no property can be
written for it at all** — so the quantifier fails and the condition for
declining is not met. That is the whole result, and it is stated here and
nowhere else in this file.

**F5 is categorical, not a swing cell.** Earlier drafts left its membership open
and let the verdict depend on it. It is a failure this conflation produces —
#169 says so, and the one message covered two classes precisely because the two
types share a shape — and it admits no Oath-level property, which is a
structural fact rather than a judgement about effort.

**AND THE VERDICT RESTS ON NO TIMEOUT.** F3 is closed on a property that was
written and RUN (part 6), not on anything the prover failed to discharge. The
four 900-second no-verdicts are evidence about DISCHARGE and decide no row —
which matters, because `unproven` is not `false` and a timeout is not a
counterexample.

### F3 closes, and WHAT IT COSTS is the finding

The falsifier asks whether a property exists. For F3 one does, it was run, and
it falsifies the receiver's own extractor — part 6. **A fourth draft of this
section marked F3 unclosed on an argument that does not hold**, and the
correction is worth more than the error, because what survives it is sharp.

`(utf8-encode "é")` is the two bytes `[195, 169]`; `bytes-str` maps them to the
two codepoints `[195, 169]`, which is the text `"Ã©"`. Consider that pair:

    ( [195, 169] , "Ã©" )

- As **ISO-8859-1 reinterpretation** it is **CORRECT** — not asserted but
  **PROVEN**: `latin1-is-exact`, `∀v. bytes-ok v ⟹ str-bytes (bytes-str v) == v`,
  by induction in 4 seconds.
- As **UTF-8 decoding** it is **WRONG**; the correct result is `"é"`.

**Both readings are of the same two values.** A property CAN still be written
that rejects the pair — `(== text (utf8-decode bs))` does, and it is a perfectly
ordinary predicate over `(List Int)` and `Str`. What it cannot do is derive the
verdict from the values, because the values do not carry which reading was
meant. **So it does not detect an error; it ENFORCES A CHOICE.** Measured, in
both directions:

| property | at `[195,169]` | of a provably-correct Latin-1 decoder |
|---|---|---|
| `multibyte-shrinks` | fires on `bytes-str` | **also fires** — condemns `latin1-decode`, whose correctness is PROVEN |
| `latin1-preserves-length` | silent on `bytes-str` | silent — but **fires on the correct UTF-8 decoder** |

`both-roles-cannot-be-satisfied` puts it in one evaluated fact: the conjunction
of the two role obligations at `[195,169]` is `false`.

**So every property that closes F3 encodes the intended role, and the role is
not in the types.** That is a statement about the CHARACTER of the closure, not
a denial of it:

| what closes F3 | what it supplies beyond the values |
|---|---|
| `cyrillic-repo-round-trips` — `== … "miclip/ключ"` | what the FIELD MEANS: that this JSON field is UTF-8 text |
| `(== (f v) (utf8-decode v))` | the intended decoder, by name — and the honest body proves it BY REFLEXIVITY in 0s, certifying nothing |
| `multibyte-shrinks` | the assumption that `f` is a decoder at all |

Part 6 reached the edge of this and stopped one step short — its closing line is
*"the evidence for it had to be authored"*. The sharper form: **the evidence
cannot be READ OFF the artefact under test at any cost of diligence, because the
fact it must encode is not present in the values.** Being wrong about that fact
is undetectable by anything measured in this file, and it is exactly the fact a
type would carry. Whether that argues for a type is not decided here.

### F5 is a diagnostic and representation failure, and has no Oath-level subject

Classified explicitly rather than left to swing the verdict. #167 covers two
things, and **neither is an Oath value**, so the falsifier's sentence has nothing
to quantify over:

| half | why no property can have it as its subject |
|---|---|
| a `(List Int)` element that is not an `Int` — the REPRESENTATION defect | statically unreachable. `oath eval '(Cons [Int] "x" (Nil [Int]))'` fails at check time: `error: expected Int, got #e6bbed8bc934`. There is no well-typed witness, so the domain is empty |
| *which class a diagnostic names* — the MESSAGE defect | a refusal is not an Oath value. `oath/compile.go` says so at the type that implements it — *"no Oath code can observe it, branch on it or continue past it"* — and the surface has no `catch`, `try` or `recover` |

A property can PREDICT a refusal — `bytes-ok v` is exactly that, and it is what
closes F1. What has no Oath-level subject is the thing #167 is about. **So F5 is
not closed, and calling it "unclosed" without this distinction would suggest a
property existed and lost.**

### Discharge is a separate question, and it was measured

None of what follows bears on whether a failure is CLOSED — that is settled
above by the criterion. It bears on whether a qualifying property, once written,
can be made to hold. Kept because it is the largest body of measurement in this
file and because it corrected the earlier drafts' central claim.

- **The obligations that qualify are provable.** F1's `usable-encodes`
  discharges by induction in 3 seconds, F2's in 14 (121 attached to the guard's
  own name), F4's `str-scalar-ok` 6/6 in 0 seconds. Earlier drafts recorded
  these as never attempted and reasoned about them by analogy; that was the
  file's largest error.
- **F3's splits, and conflating the two halves would misreport it.**
  `oath prove utf8-decode-checked` — a SIX-property bundle including
  `multibyte-shrinks` — was killed at 900 seconds twice with no output at all.
  The reference-agreement contract is a different goal and **PROVES directly in
  0 seconds** (`ref-decoder-good`), because the honest body IS the reference.
  So one of F3's candidate obligations discharges trivially and the other
  returns no verdict; the timeout is an implementation limit and neither half
  bears on whether F3 is CLOSED.
- **The prover did not refute anything HERE.** Every cheat's obligation returned
  `unproven`, not `REFUTED`, though `REFUTED` is in its vocabulary
  (`oath/prove.go:2359`) and all three obligations were hand-checked FALSE.
  **Stated as an observation about these runs, not a property of the prover** —
  the cheat goals were not re-run on the clean machine, so whether a
  contention-free run would have refuted some of them is untested. What
  soundness does give, and it is the direction the gate needs, is that a false
  property is never PROVEN.
- **`require_proven` excludes the measured cheats and does NOT preserve the
  obligation.** A body that DELETES the contract and attaches a vacuous property
  proves 1/1 and takes the name; the guard it then names admits a sixteen-`©`
  secret. `min_mutation_score` blocks that one replacement and is not shown to
  preserve a contract generally.
- **A policy gates a NAME, not a class.** So even a preserved obligation reaches
  a failure only when the gated name is that failure's entry point — true of the
  receiver's single secret guard, false of a decoder (F3 happened in
  `json-scoped-string`) and false of any one producer (this file's own `f4-main`
  builds an unpackable `Str` under a different name).

**THE PROOF RUNS WERE CONTAMINATED BY ORPHANED SOLVERS, and here is exactly what
that does and does not touch.** `oath prove` runs z3 as a child, and the
watchdog used for these runs SIGKILLed only the parent — so each 900-second kill
left a `z3 -in` spinning. Four were found alive afterwards, aged 1h17m to 5h11m,
matching the four kills exactly. Proofs are load-sensitive (15 seconds per goal),
so every later run in this file competed with them. Scoping the damage:

| result class | affected? | why |
|---|---|---|
| **PROVEN** verdicts | **no** | the proof succeeded DESPITE the load; load can prevent a proof, never manufacture one |
| **`unproven`** on the cheats | **partly — see the narrowing** | every one was hand-checked FALSE, and soundness guarantees a false property is never PROVEN, at any load. It does NOT guarantee the verdict was `unproven` rather than `REFUTED`: a cleaner run might have refuted some, which would be a stronger result. **The cheat goals were NOT among the clean re-runs**, so what is established is *they cannot prove*, not *this exact verdict is load-independent* |
| **NO VERDICT** at 900s | **no — RE-RUN CLEAN TO ESTABLISH IT** | a timeout under contention could have completed on an idle machine, so reasoning was not enough. All three were re-run after the orphans were killed: `utf8-decode-checked`, `faithful-under-byte-range-p` and `guard-byterange-both` each still `rc=137 elapsed=900s`, no output |
| **wall times** | **YES** | the seconds recorded here are indicative only, which the reproduction section already says |

**So no verdict in this file changes on anything that was re-measured, and the
timings are worth less than they look — which was checked rather than
reasoned.** The one gap, stated rather than glossed: the cheat goals were not
re-run, so *they cannot prove* is established while *`unproven` rather than
`REFUTED`* is an observation about the contaminated runs only. Three of the fast proofs were
re-run on an idle machine after the orphans were killed:

```
str-scalar-ok        1s   (recorded 0s)   PROVEN, unchanged
secret-is-usable-p   1s   (recorded 3s)   PROVEN, unchanged
latin1-is-exact      1s   (recorded 4s)   PROVEN, unchanged
```

Same verdicts, shorter wall times — the signature of load contamination, and
evidence that it moved seconds and not outcomes. **The three timed-out goals
were re-run clean as well**, because the argument that load "can only make a
timeout arrive sooner" is false — contention can turn a completing proof into a
timeout, so that row needed a measurement and not an argument:

```
utf8-decode-checked          rc=137  elapsed=900s   (unchanged, no output)
faithful-under-byte-range-p  rc=137  elapsed=900s   (unchanged, no output)
guard-byterange-both         rc=137  elapsed=900s   (unchanged, no output)
```

**And that clean re-run is what proved the process-group fix insufficient**: it
still left TWO orphans behind, which is why the published runner now sweeps
PPID-1 solvers and asserts none survive.

**A SECOND CONFOUND, worse than contention, and this file did not find it.** The
machine briefly ran OUT OF DISK during the window these timings were taken in.
z3 uses temporary files, so a goal can fail for reasons that have nothing to do
with the goal — and unlike load, that failure mode is not merely slower, it is
unrelated. **Every wall time recorded here was taken across that window and is
suspect for two independent reasons.**

**The four no-verdicts were then re-run INDEPENDENTLY, on an idle box with 34Gi
free, and all four reproduce:**

```
secret-is-usable-p  (control, first)   rc=0     2s     PROVEN, induction on binder 0
utf8-decode-checked                    rc=137   900s   0 bytes
faithful-under-byte-range-p            rc=137   900s   0 bytes
guard-byterange-both                   rc=137   900s   0 bytes
roundtrip-cheat                        rc=137   900s   0 bytes
secret-is-usable-p  (control, last)    rc=0     2s     PROVEN
```

**THE CONTROL IS THE ONLY REASON THE 900s LINES MEAN ANYTHING, and it ran FIRST
AND LAST.** `rc=137` with zero bytes looks identical whether z3 worked hard or
the binary died on startup — the "a probe that proves only that SOMETHING
failed" trap this repo keeps naming. Four silent kills beside a 2-second PROVEN
through the same harness is evidence; four silent kills alone would not be. And
running the control at BOTH ends is what rules out a box that degraded during
the 45 minutes, which — after a disk event — is exactly the assumption that had
already proved worthless once.

Note the control moved: 8 seconds in one run, 2 in the other. **What reproduced
is WHICH goals return and which do not**, identically across both runs. That is
this section's own point: timings are indicative, verdicts are not.

**KEEP THE READING NARROW.** These goals do not prove HERE, under THIS budget,
with THOSE contracts. **`unproven` is not `false`; a timeout is not a
counterexample.** No row of the verdict rests on one of them — F3 is closed on a
property that was written and RUN — and if a later reader makes one load-bearing,
this is the sentence that belongs beside it. The published runner now puts each proof in its own process group and
kills the GROUP, which was verified to leave no orphan where the old form left
four.

**Read the two enforcement findings above as limits on ENFORCEMENT, not as
closure verdicts.** Two
drafts of this file let them decide rows, which is how F1 and F2 came to be
marked both closed and "not established" in the same document.

### What the result costs, which is not the same as what it is

**The result itself is stated once, with the table, and is not repeated here.**
What follows is the part that survives whichever way a reader weighs the rows.

**For F3 the closing property exists and works — and
it works only by encoding which reading of the bytes was intended, a fact the
types do not carry and the values cannot supply. A wrong choice there is
undetectable by generation (blind to class B by measurement), by proof (the goal
returns no verdict in 900 seconds, twice), by `oath mutate` (which scores the
defective specification 1/1) and by the guarantee line (identical on a correct
decoder and a mojibaking one). **So "closed by a property over the existing
types" is true for F3 and does not mean what the falsifier's conclusion assumes
it means** — the property closes the failure the author already knew about, and
carries no evidence that the author chose right.

**The negative is narrow on purpose.** This does not say a `Bytes` type is
warranted, that the workaround is inadequate, or that any design should follow.
It says the argument #169 offered for declining does not, on this evidence,
carry. No candidate is sketched and no design work is done here.

### What the generator can and cannot reach

Separate from the criterion and from discharge: what the corpus's default
evidence route actually visits. Every line measured.

- **GENERATION NEVER REACHES CLASS B — but it DOES reach F4's domain.** No
  codepoint and no byte at or above 128 across 200 cases per property; a
  non-scalar `Str` drawn within 3. That asymmetry is why a single universal
  kills F4's cheating guard and not F3's cheating decoder.
- **A SECOND BLINDNESS the codepoint measurement hides: LENGTH.** The tester
  never draws a `Str` of eight characters, let alone sixteen, so both secret
  guards are false-and-agreeing on everything it produces. A domain-aware
  generator would have to widen on two axes, not one.
- **Stated as an upper bound, because that is all the calibration measured.**
  The tester DOES draw negative values (`-1` and `-2` in the
  `corpus-roundtrip-decode` transcript), which are class C, so "every generated
  variable is class A" would be false and an earlier version of this line said
  it. The claim is about what the TESTER DRAWS. Properties like
  `latin1-is-not-usable [(s Str)]` do CHECK class-B values — they construct one
  by prefixing `é` onto a generated suffix — so the values EXAMINED reach class
  B even though the drawn ones never do; what generation cannot supply is a
  class-B value it was not told to build. And parts 5 and 6 are not in this set
  at all: they are killed by Cyrillic and Latin-1 witnesses chosen by hand.
  Crediting the generator with those would credit it with exclusions a person
  made.
- **A DOMAIN-AWARE GENERATOR IS NOT A ROUTE TO DISCHARGE, AND A FIRST DRAFT OF
  THIS PARAGRAPH SAID IT WAS.** It would kill both cheats recorded here, which
  is real and worth having. But sampling a named domain for a finite number of
  deterministic cases cannot exclude every wrong body: one can special-case
  exactly the values the generator draws, precisely as the cheats here
  special-case exactly the values a human chose — and the draw is a pure
  function of the definition's hash, so it is knowable in advance. **The
  document made, one level up, the mistake it spends part 7 diagnosing.** A
  finite witness set is a finite witness set however it is chosen.

### The four failures do not reduce to one obligation

Worth stating because several earlier drafts assumed they did, in both
directions.

**`usable-encodes` GOVERNS F1 AND NOT F2.** `str-bytes "©"` is 169, so
`bytes-ok` holds and the property is TRUE there while the sender's UTF-8 digest
still differs — and every codepoint in `128..255` behaves the same way. Proving
`usable-encodes` universally would leave F2 standing: a guard can reject
everything outside `0..255` and admit `©`. Measured, not argued:
`guard-byte-range-p` PROVES `usable-encodes` in 7 seconds and admits a
sixteen-`©` secret. F2 needs its own property relating the admitted
codepoints to the ENCODING the counterparty signs, which part 9 states and
proves as `faithful-under-shipped-p`.

So F1, F2, F3 and F4 are governed by four different properties, and discharging
one says nothing about the others.

### The honest qualifier, because the cheats are adversarial

`utf8-decode-cheat` and `secret-is-usable-cheat` are not mistakes anyone would
make. They were written to satisfy a known contract, which is not how the real
defects arose — F3 arose from calling `bytes-str`, and parts 5 and 6 show a
property catches THAT.

**But nothing in the verdict rests on the cheats, and it matters that it does
not.** F3's row is settled by a property that was written and run in part 6; the
COST recorded beside it rests on one ordinary pair, `([195,169], "Ã©")`,
produced by ordinary code, and on `latin1-is-exact` being PROVEN. A reader who rejects adversarial bodies as a threat model loses none of
that argument. What the
cheats establish is separate and still load-bearing: **the guarantee line does
not distinguish them.** Two definitions, byte-identical verdicts, one correct
and one not — a fact about the evidence layer rather than about the type system,
and one that would survive a `Bytes` type being added or declined.

## Reproduction

Every fenced `lisp` block above is a file, and they are printed in dependency
order. Extracting them from this document and putting them in that order
reproduces the run, which is how the transcripts above were checked:

```sh
# UNSET, not merely overridden by OATH_STORE: when OATH_BACKEND=cloud,
# OpenStore (oath/store.go:49) selects the cloud driver and IGNORES the path it
# was handed, so every put/prove below would target the LIVE registry — which
# has no authorized-key allowlist. `issue-170.md` carries the same guard.
unset OATH_BACKEND
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
hash printed above was reproduced identically — **every `name #hash` pair the
document states, checked mechanically against the replayed store's
`names.json`, not by eye.**

**THE COUNT IS DERIVED, NOT WRITTEN DOWN.** A stated total goes stale the moment
a definition is added, which happened here: an earlier version of this paragraph
said "all 75 pairs" and was eleven short by the time the file was finished, so a
green report would have proved nothing about the ones it had stopped counting.
Run the check instead:

```sh
# The store path is passed as an ARGUMENT: the heredoc is quoted so the regex
# below survives the shell intact, which also means "$SP" would not expand
# inside it. It did not, in the first version of this block.
python3 - "$SP" <<'EOF'
import json, re, sys, pathlib
names = json.load(open(pathlib.Path(sys.argv[1]) / "store" / "names.json"))
doc   = pathlib.Path("docs/experiments/issue-169.md").read_text()
pairs = re.findall(r"\b([a-z][a-z0-9]*(?:-[a-z0-9]+)+)\s+#([0-9a-f]{12})\b", doc)
# CONTROL: the extractor must find a pair we know is stated, or it is measuring
# nothing and an empty mismatch list would look like success.
assert ("utf8-decode-checked", "da5b44170f96") in pairs, "extractor found nothing"
bad = [(n, h, (names.get(n) or "ABSENT")[:12])
       for n, h in pairs if not (names.get(n) and names[n][:12] == h)]
print(f"{len(pairs)} occurrences over {len(set(n for n,_ in pairs))} names")
unresolved = sorted({n for n, _, g in bad if g == "ABSENT"})
mismatched = [(n, h, g) for n, h, g in bad if g != "ABSENT"]
print("UNRESOLVED:", unresolved or "none")
for n, h, g in mismatched:
    print(f"  MISMATCH {n}: doc={h} store={g}")
# EXIT NON-ZERO ON DRIFT. A checker that prints a problem and exits 0 reports
# success to anything automating it — the defect this whole file is about, and
# the first version of this block had it.
# THIS CHECK'S UNIVERSE IS THE BLOCK REPLAY, and `the-decoder` / `the-guard` are
# outside it by construction: they exist only inside the policy-control scripts,
# and one of their bodies (the deleted-obligation guard) has no block at all. So
# this check cannot validate their hashes and does not pretend to — a draft that
# tried reported a false STALE on exactly that body. **Their transcripts are
# verified by a DIFFERENT instrument**: running each control script and diffing
# its output against the transcript printed beside it, which is byte-identical.
# Naming the instrument is the point; leaving the gap unstated is what would
# make this check overclaim.
ok = not mismatched and set(unresolved) <= {"the-decoder", "the-guard"}
print("HASH CHECK:", "PASS" if ok else "FAIL")
sys.exit(0 if ok else 1)
EOF
```

**Only two names may appear as unresolved, and NO mismatch may appear at all.**
`the-decoder` and `the-guard` exist solely inside the policy control scripts
below and are never bound by the block replay; both of their hashes equal the
definitions they were renamed from, which is content addressing behaving as it
should. Any other unresolved name means a block was dropped; any mismatch means
a transcript has drifted from the code that produced it.

The extractor writes twenty-seven files, `block00.oath` through `block26.oath`.
Parts 1–8 are blocks 00–11 and parts 9–12 are blocks 12–26. **This manifest is
read off a replay, not counted by hand** — inserting a block renumbers every
later one, and two earlier versions of this list were wrong for that reason:

    block00  the named domain             block14  the agreement property
    block01  the instruments              block15  the class-C-aware cheat
    block02  the secret-side guards       block16  the F4 boundary fixture
    block03  the check matrix             block17  str-scalar-ok
    block04  the generator calibration    block18  the non-scalar calibration
    block05  the witnesses                block19  the scalar-range cheat
    block06  the lambda-free re-spelling  block20  the secret-cheat universals
    block07  the checked decoder+mutant   block21  the Str-length calibration
    block08  the two call-site variants   block22  the F5 compiled fixture
    block09  the witness-cheating decoder block23  the byte-range guard
    block10  the round-trip proof attempt block24  the attached obligations
    block11  the cheating secret guard    block25  the reference contracts
    block12  the lam-free obligations     block26  the producer postconditions
    block13  the role-blind test

`put` exits 2 on any file containing a falsified definition, and several blocks
contain one deliberately. Measured over the replay: `block03`, `block04`,
`block05`, `block07`, `block08`, `block14`, `block18`, `block19`, `block21`,
`block25` and `block26` exit 2; every other block exits 0.

**`block09` and `block11` exiting 0 are the whole finding of parts 7 and 8** —
those are the two cheating bodies being accepted. **`block15` and `block20`
exiting 0 are the same finding for parts 10 and 11**: the class-C-aware decoder
survives the agreement property that killed cheat 1, and the secret cheat
survives its own. (`block19`, the scalar-range cheat, exits 2 — it is the one
row where a universal DOES catch the cheating body.) Note also what capturing that
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
# UNSET, not merely overridden by OATH_STORE: when OATH_BACKEND=cloud,
# OpenStore (oath/store.go:49) selects the cloud driver and IGNORES the path it
# was handed, so every put/prove below would target the LIVE registry — which
# has no authorized-key allowlist. `issue-170.md` carries the same guard.
unset OATH_BACKEND
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

### The `require_proven` control (part 12)

Same shape, different flag, and the same discipline about asserted failures.
**`--once` is load-bearing:** bare `prove-worker` polls on an interval and never
returns, so a foreground run looks like a hang instead of a result, and the
first attempt at this measurement was abandoned for exactly that reason.

```sh
#!/bin/sh
set -e
# UNSET, not merely overridden by OATH_STORE: when OATH_BACKEND=cloud,
# OpenStore (oath/store.go:49) selects the cloud driver and IGNORES the path it
# was handed, so every put/prove below would target the LIVE registry — which
# has no authorized-key allowlist. `issue-170.md` carries the same guard.
unset OATH_BACKEND
B="$1"; W=$(mktemp -d); cp -R codebase "$W/store"
export OATH_STORE="$W/store"; O=./oath/oath
# The four blocks the guards depend on. Block numbers shift when a block is
# inserted, so check them against the manifest above rather than trusting these.
for i in 00 01 06 12; do $O put "$B/block$i.oath" --new >/dev/null; done
name() { python3 -c "import json;print(json.load(open('$W/store/names.json')).get('the-guard','(unbound)')[:12])"; }

# ONE name, TWO bodies, and the property is character-for-character identical.
sed 's/secret-is-usable-p/the-guard/g' > "$W/good.oath" <<'EOF'
(defn secret-is-usable-p [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (printable-ascii-cps secret))
  (prop usable-encodes [(s Str)]
    (if (secret-is-usable-p s) (bytes-ok (str-bytes s)) true)))
EOF
sed 's/secret-is-usable-cheat-p/the-guard/g' > "$W/cheat.oath" <<'EOF'
(defn secret-is-usable-cheat-p [] [(secret Str)] Bool
  (and (<= 16 (str-len secret)) (not-the-two-witnessed-ranges secret))
  (prop usable-encodes [(s Str)]
    (if (secret-is-usable-cheat-p s) (bytes-ok (str-bytes s)) true)))
EOF

$O put "$W/good.oath" --new >/dev/null; echo "bound     -> $(name)"
$O prove the-guard | tail -2
echo '{ "rules": [ { "names": ["*"], "require_proven": true } ] }' > "$W/store/policy.json"

out=$($O put "$W/cheat.oath" 2>&1) || true
printf '%s\n' "$out" | grep -E 'PENDING PROOF' \
  || { echo "FAIL setup: policy did not queue the cheat"; exit 1; }
echo "after put -> $(name)"
# BOTH conditions, and the NAME checked BEFORE the good body is resubmitted.
# An alternation on 'not bound|drained' passes even if the worker PROVED and
# BOUND the cheat, because a successful pass prints `drained` too — and the
# resubmission below would then quietly restore the original hash and the script
# would exit 0 having measured nothing. This is the repo's standard failure:
# a probe that cannot tell the outcome it is testing from its opposite.
w=$($O prove-worker --once 2>&1); printf '%s\n' "$w"
printf '%s\n' "$w" | grep -q 'not bound' \
  || { echo "FAIL: worker did not report the cheat unbound"; exit 1; }
[ "$(name)" = 688f043be371 ] \
  || { echo "FAIL: the cheat took the name: $(name)"; exit 1; }
echo "after wkr -> $(name)"
$O put "$W/good.oath" 2>&1 | grep -E 'PROVEN'      # the control: still admitted
echo "control   -> $(name)"
```

Its output, run as printed:

```
bound     -> 688f043be371
∎ PROVEN    usable-encodes               induction on binder 0 (Z3, unbounded ints)
proven: 1/1 properties
⏳ the-guard        PENDING PROOF: policy: this name requires proven properties; queued for the verification worker (name unchanged until proof lands)
after put -> 688f043be371
· the-guard        #f44aae41b25d  tested (200 cases per property)
⛔ the-guard        name not bound — proof did not complete: not all properties proven (re-queue to retry a transient timeout)
prove-worker: drained (1 job(s) this pass); queue depth 0
after wkr -> 688f043be371
✓ the-guard        #688f043be371  PROVEN (all 1 properties, Z3 over unbounded ints) · total  (no-op: the name already pointed at this version)
control   -> 688f043be371
```

**The last two lines are the two-way control and are not decoration.** Without
them the run would show a policy that refuses things, which a policy refusing
EVERYTHING also does.

### The proof attempts

**Run these with `sh`.** The runner needs job control (`set -m`) to put each
proof in its own process group; non-interactive `zsh` refuses that option and
aborts with exit 1 and no message.

`timeout` is not present on every platform this was run on, so each attempt is
backgrounded with its own watchdog and its wall time recorded. Sequential on
purpose: proofs give z3 15 seconds per goal and are load-sensitive, so parallel
runs manufacture timeouts.

```sh
# UNSET, not merely overridden by OATH_STORE: when OATH_BACKEND=cloud,
# OpenStore (oath/store.go:49) selects the cloud driver and IGNORES the path it
# was handed, so every put/prove below would target the LIVE registry — which
# has no authorized-key allowlist. `issue-170.md` carries the same guard.
unset OATH_BACKEND

# Logs go to $SP, the scratch store's directory, NOT the checkout: run from the
# repository root as documented, `>"$1.txt"` drops one untracked file per proof
# into the working tree, and a reproduction that dirties the tree it is
# measuring is the hazard the rest of this file is about.
# ELAPSED TIME IS RECORDED, NOT JUST THE EXIT CODE. Without it a 3-second proof
# and a 900-second kill are told apart only by rc=137, and every reported
# duration below would be unverifiable from this script.
run() {
  s=$(date +%s)
  snapshot_z3                          # so reap_orphans touches only OUR strays
  # OWN PROCESS GROUP, and kill the GROUP. `oath prove` runs z3 as a CHILD
  # (`oath/z3host_native.go` execZ3), and SIGKILL on the parent skips its
  # context cancellation — so killing only $p leaves `z3 -in` running as an
  # orphan, spinning on the goal that timed out and loading every later proof in
  # a script whose whole point is that they run one at a time. This was not
  # hypothetical: four orphaned solvers, aged 1h17m to 5h11m and matching the
  # four 900-second kills exactly, were found alive after the runs below.
  set -m
  ./oath/oath prove "$1" >"$SP/prove-$1.txt" 2>&1 & p=$!
  ( sleep 900; kill -9 -"$p" 2>/dev/null ) >/dev/null 2>&1 & w=$!
  wait $p; rc=$?
  kill -9 -"$p" 2>/dev/null            # reap any survivor of a NORMAL exit too
  reap_orphans                         # ... and the ones the group kill races
  # KILL THE WATCHDOG'S GROUP, not the subshell alone — the same defect one
  # level down. `sleep 900` is the subshell's CHILD, so signalling $w leaves the
  # sleep running to its full term, one survivor per fast proof. And REAP after
  # signalling: `kill` alone leaves the shell to print its own "Terminated" job
  # notice to stderr, which `2>/dev/null` on the kill cannot suppress because
  # the shell is not the kill.
  kill -9 -"$w" 2>/dev/null; wait $w 2>/dev/null
  printf '%s\trc=%s\telapsed=%ss\n' "$1" "$rc" "$(( $(date +%s) - s ))"
}
# AFTER THE LOOP, ASSERT NOTHING SURVIVED — as CODE, not as a comment. A leaked
# solver is invisible in the transcript and changes the next run's timings, and
# an earlier version of this block described the check in prose and executed
# nothing, so the runner reported success with orphans alive. Call check_orphans
# after the loop below.
# THE GROUP KILL IS NOT SUFFICIENT ON ITS OWN — measured, not assumed. A clean
# re-run of the three timed-out goals with `set -m` and `kill -9 -$p` still left
# TWO orphaned solvers, so there is a race the group kill does not cover.
#
# SWEEP ONLY WHAT THIS RUN CREATED. A blanket "kill every PPID-1 z3" would also
# reap a co-tenant's solver on a shared box or CI account, so each proof snapshots
# the pre-existing z3 PIDs and reaps only ones that appeared since. Residual
# caveat, stated rather than papered over: a concurrent run that orphans a solver
# DURING this proof is indistinguishable from ours and would be reaped.
# RUN THIS WITH `sh`, NOT `zsh`. `set -m` is what puts each proof in its own
# process group; without it `kill -9 -$p` targets a group that does not exist.
# MEASURED, both shells: under `sh` it works; under non-interactive `zsh` the
# `set` is a fatal error (`can't change option: -m`) and the script ABORTS with
# exit 1 before this guard can print — so zsh fails closed and cannot hang, but
# it fails SILENTLY, which is why the requirement is stated here in prose too.
set -m 2>/dev/null || {
  echo "FAIL: this runner needs job control — run it with sh, not zsh"; exit 1; }

BASELINE=$(pgrep -f 'z3 -in' 2>/dev/null | sort | tr '\n' ' ')
snapshot_z3() { pgrep -f 'z3 -in' 2>/dev/null | sort > "$SP/.z3-before"; }
reap_orphans() {
  pgrep -f 'z3 -in' 2>/dev/null | sort > "$SP/.z3-after"
  comm -13 "$SP/.z3-before" "$SP/.z3-after" | while read -r pid; do
    [ "$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')" = 1 ] && kill -9 "$pid" 2>/dev/null
  done
}
# Controlled by feeding the awk the exact `ps` lines four REAL orphans produced
# in this session (` 6672     1 z3 -in`): it matches those, ignores a z3 with a
# live parent, and ignores `./oath/oath serve` — 1 and 0 respectively out of the
# count path. Spawning a synthetic orphan to test end-to-end is not portable
# here (no `setsid`, and the harness reaps the process group), so the logic is
# controlled directly and the live positive is the four that were found.
# COMPARED AGAINST THE BASELINE, not against zero. A shared host may already
# have someone else's orphaned solver, and failing on that would report OUR leak
# where there is none — while also contradicting the snapshot logic above, which
# deliberately refuses to touch another user's processes.
check_orphans() {
  new=$(pgrep -f 'z3 -in' 2>/dev/null | sort | tr '\n' ' ')
  leaked=""
  for pid in $new; do
    case " $BASELINE " in *" $pid "*) continue ;; esac
    [ "$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')" = 1 ] && leaked="$leaked $pid"
  done
  [ -z "$leaked" ] || { echo "FAIL: this run leaked orphaned solver(s):$leaked"; exit 1; }
  echo "no orphaned solvers leaked by this run"
}
# EVERY attempt this document reports, in the order it reports them —
# INCLUDING parts 4 and 7's, which an earlier version of this loop omitted while
# claiming to be exhaustive. The two `utf8-decode-checked` entries are the
# original and the retry: `oath prove` persists proven properties as lemmas, so
# re-running is the documented convergence remedy, and it is recorded here
# because it did NOT converge.
for n in class-b-shrinks-p class-b-shrinks-decode-p class-b-agree-p \
         roundtrip-real roundtrip-cheat \
         utf8-decode-checked utf8-decode-checked utf8-decode-cheat \
         secret-is-usable secret-is-usable-cheat \
         secret-is-usable-p secret-is-usable-cheat-p \
         str-scalar-ok str-scalar-ok-cheat cheat-is-sound-alone \
         latin1-preserves-length latin1-is-exact \
         signs-the-senders-bytes-p faithful-under-shipped-p faithful-under-cheat-p \
         guard-byte-range-p faithful-under-byte-range-p \
         guard-ascii-both guard-byterange-both guard-cheat-both \
         ref-decoder-good ref-decoder-cheat ref-guard-good ref-guard-cheat \
         safe-chr unsafe-chr
do run "$n"; done
check_orphans
```

**Budget it before starting: FIVE of these expire at the 900-second limit** —
`utf8-decode-checked` twice, plus `faithful-under-byte-range-p`,
`guard-byterange-both` and `roundtrip-cheat`. Part 7 records the last of those
at ten minutes under a smaller budget; the independent re-run reports it at
`rc=137 elapsed=900s` like the others, so it costs the full watchdog here. That
is 75 minutes of timeouts alone, and a full replay runs appreciably longer. A killed run
reports `rc=137 elapsed=900s`, which is how it is told apart from a fast one.

**The wall times are indicative, the VERDICTS are not.** Z3 gets 15 seconds per
goal and is load-sensitive, so a 3-second proof may replay in 2 or 5; what must
reproduce is which properties prove and which do not.

The watchdog's stdio is redirected to `/dev/null` deliberately: a timer subshell
holding an inherited pipe makes every SUCCESSFUL run block for the full budget,
which would have turned a 3-second proof into a 900-second one and read as a
failure.
