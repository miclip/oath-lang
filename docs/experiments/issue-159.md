# #159 — a `Bytes` datatype and `Str` are the same object

**What this file is:** a record of what was MEASURED for #159, and nothing that
was concluded from it. Three parts, in the order they were run:

1. the identity reproduction — a `Bytes` datatype declared with the shape a byte
   list needs produces the same canonical object as `Str`, byte for byte;
2. #159's own falsifier, stated, with an audit of the instrument that would
   decide it — which is INCOMPLETE, and which would not close #159 even if it
   came back green;
3. the `ключ` versus Latin-1 question, run against the two webhook members of
   the committed corpus.

**It proposes no repair, chooses no design, and declares no verdict on #159.**
That is the human's fork, and it is deliberately not taken here.

Run on 2026-08-07, with the binary at `oath/oath`: parts 1–2 against `eafef02`,
part 3 against `eccb790`.

## The claim under test

`Str` is codepoints and a body is bytes, and both are `(List Int)`-SHAPED
inductive types — shaped, not equal: `Str` and `(List Int)` are different
objects and Oath already refuses to substitute one for the other (measured near
the end of this file). The question here is narrower and is about the shape:
whether declaring a distinct `Bytes` datatype with the shape a byte list needs
produces a NEW type, or merely reproduces the object `Str` already is.

## Method

Each declaration is put **into its own fresh store**, by a separate invocation
that shares nothing with the other. Independence is the point: the two hashes
below are produced by runs that cannot have influenced one another, and the two
object files are compared afterwards as artifacts.

Each store is a throwaway temporary directory and `--new` is supplied, because
the binding is fresh in that store. Nothing here reads or writes `codebase/`.

The two declarations are CREATED here rather than displayed, so the transcript
runs from a clean checkout with nothing else on disk:

```sh
cat > str.oath <<'EOF'
(data Str []
  (SNil)
  (SCons Int Str))
EOF

cat > bytes.oath <<'EOF'
(data Bytes []
  (BNil)
  (BCons Int Bytes))
EOF
```

The operation is then:

```sh
OATH_STORE=$(mktemp -d) ./oath/oath put str.oath   --new
OATH_STORE=$(mktemp -d) ./oath/oath put bytes.oath --new
```

That is the operation, and it is the exploratory form CLAUDE.md requires. One
practical note, because the literal command above cannot produce the file
comparison that follows: `OATH_STORE=$(mktemp -d) cmd` exports the directory to
`cmd` only, so the shell never learns the path and the object cannot be
retrieved afterwards. The runs below therefore keep the same prefix and read the
path back out of the variable from inside the same invocation. Both runs are
below; each `mktemp -d` is evaluated separately, so the two stores are distinct
directories and the two invocations still share nothing:

```sh
OATH_STORE=$(mktemp -d) sh -c \
  './oath/oath put "$1" --new --json; cp "$OATH_STORE"/objects/*.bin "$2"' \
  _ str.oath str.bin

OATH_STORE=$(mktemp -d) sh -c \
  './oath/oath put "$1" --new --json; cp "$OATH_STORE"/objects/*.bin "$2"' \
  _ bytes.oath bytes.bin
```

## Result — the two printed hashes

Run 1, `str.oath`, in its own store:

```json
{
  "name": "Str",
  "hash": "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
  "kind": "data",
  "status": "accepted",
  "ctors": 2,
  "journal_position": 1
}
```

Run 2, `bytes.oath`, in a different store:

```json
{
  "name": "Bytes",
  "hash": "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
  "kind": "data",
  "status": "accepted",
  "ctors": 2,
  "journal_position": 1
}
```

Both report `journal_position: 1` — each is the first and only entry in its own
store, which is what confirms the runs were independent rather than one store
observed twice.

Two elisions, so that a reader re-running the commands is not surprised: `put
--json` prints a one-element ARRAY, unwrapped above, and each object also carries
a `journal_entry` digest, dropped above because it is a hash of the ENTRY rather
than of the definition. It will not match anything printed here, and that is not
a store-to-store difference — the entry carries a `time` field, so putting the
SAME source into two fresh stores a second apart yields two different
`journal_entry` values while `hash` stays fixed. That is the assertion/observation
split the journal is built on, visible in the digest: `hash` is what the
definition IS, `journal_entry` is a record of one act of publishing it.

The full hashes are equal:

```
Str    e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588
Bytes  e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588
```

## Byte identity — comparing the two independently produced object files

Equal hashes are a claim about a digest. The stronger claim is that the two runs
emitted the same bytes, and that is checked by comparing the extracted objects
directly:

```
$ ls -l str.bin bytes.bin
-rw-r--r--  25  str.bin
-rw-r--r--  25  bytes.bin

$ cmp str.bin bytes.bin
$ echo $?
0

$ shasum -a 256 str.bin bytes.bin
e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588  str.bin
e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588  bytes.bin
```

`cmp` reports no differing byte. Both files are 25 bytes, and each file's
SHA-256 **is** the hash the corresponding run printed — so identity here is the
digest of the canonical O1 encoding, and the two declarations do not merely
collide under a digest, they encode to the same bytes.

Both dumps, in full:

```
00000000: 4f31 0100 0000 0000 0000 0200 0000 0000  O1..............
00000010: 0000 0201 0700 0000 00                   .........
```

Twenty-five bytes for a two-constructor datatype, and no name — not `Str`, not
`SCons`, not `Bytes`, not `BCons` — appears anywhere in them. The encoding
carries arity and field types only.

### The comparison instrument was proven to fail on a one-byte difference

The result above is a SILENCE, and `cmp` is equally silent over two empty files
or two files that failed to be written. A silence is only evidence once the
instrument has been seen to speak, so the comparison was made to fail on the
smallest difference it must catch — a single byte — before its silence over
`str.bin` and `bytes.bin` was read as identity.

The mutation, applied to a copy of the object rather than to either run's
output:

```sh
cp str.bin mutant.bin
printf '\x01' | dd of=mutant.bin bs=1 seek=24 count=1 conv=notrunc
```

That rewrites offset 24 — the final byte, `0x00` → `0x01` — and nothing else:

```
00000000: 4f31 0100 0000 0000 0000 0200 0000 0000  O1..............
00000010: 0000 0201 0700 0000 01                   .........
                              ^^ the mutated byte
```

`cmp -l`, which lists every differing position, reports exactly one, and the
file is still 25 bytes — so what follows is a reaction to content, not to a
length change:

```
$ cmp -l str.bin mutant.bin
    25   0   1
$ wc -c < str.bin ; wc -c < mutant.bin
      25
      25
```

And `cmp` fires:

```
$ cmp str.bin mutant.bin
str.bin mutant.bin differ: char 25, line 1
$ echo $?
1
```

The mutated byte is deliberately the LAST one: `cmp` reporting `char 25` proves
it read all twenty-five bytes rather than short-circuiting on an early
difference, which a mutation nearer the start would not have shown. The digest
moves with it — `mutant.bin` hashes to `7e908ec0…8de9` — so neither half of the
byte-identity check is insensitive.

Both results, side by side, are the whole of the instrument's calibration:

| comparison | `cmp` exit | reading |
|---|---|---|
| `str.bin` vs `bytes.bin` — independent stores | 0, no output | no differing byte |
| `str.bin` vs `mutant.bin` — one byte flipped | 1, `differ: char 25` | fails as it must |

## The same object the committed corpus uses

The `str.oath` above is stripped of the comments and neighbouring definitions
that `examples/str.oath` carries, so it is worth confirming it is not a reduced
stand-in. It is not — `codebase/names.json` binds `Str` to
`e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588`, the same
hash. (Read only; the committed store was not written to.) The collision is with
the corpus `Str`, not with a simplification of it.

## Controls

A hash that agreed with everything would establish nothing. Each control below
was put by the same route — its own fresh store, one declaration — and its
object file compared against `str.bin`. Every structural perturbation moves the
hash; only the renaming does not:

| declaration | hash | vs `str.bin` |
|---|---|---|
| `(data Str   [] (SNil) (SCons Int Str))` | `e6bbed8b…e588` | baseline |
| `(data Bytes [] (BNil) (BCons Int Bytes))` | `e6bbed8b…e588` | **identical** |
| `(data Bytes [] (BNil) (BCons Bool Bytes))` | `7e383768…3633` | differs |
| `(data Bytes [] (BCons Int Bytes) (BNil))` | `73ce8374…c8e9` | differs |
| `(data Bytes [] (BNil) (BCons Int Bytes) (BOops))` | `88094d19…0eec` | differs |
| `(data Bytes [] (BNil) (BCons Bytes Int))` | `022a3be2…8bb1` | differs |
| `(data Bytes [a] (BNil) (BCons Int (Bytes a)))` | `65947cb0…3724` | differs |
| `(data Bytes [] (BNil) (BCons Bytes))` | `7ee4b63c…ff61` | differs |
| `(data Bytes [] (BNil) (BCons Int Int Bytes))` | `9a0a3a7e…fcdd` | differs |

Each row varies exactly one thing, and the last two were added because an
earlier draft claimed "arity" was load-bearing on the strength of rows that
never varied a CONSTRUCTOR'S FIELD COUNT — `BOops` varies the number of
constructors and `[a]` varies the number of TYPE PARAMETERS, which are two
different arities and neither is the one the word was doing work for. Dropping
`BCons`'s `Int` and adding a second one are the controls that measure it:

    row                        what it varies
    ------------------------------------------------------------
    (BCons Bool Bytes)         a field's TYPE
    (BCons Int Bytes) (BNil)   CONSTRUCTOR ORDER
    … (BOops)                  CONSTRUCTOR COUNT
    (BCons Bytes Int)          FIELD ORDER
    [a] … (Bytes a)            TYPE-PARAMETER COUNT
    (BCons Bytes)              CONSTRUCTOR FIELD COUNT — one removed
    (BCons Int Int Bytes)      CONSTRUCTOR FIELD COUNT — one added

So constructor order, field order, field type, constructor count, constructor
field count and type-parameter count are all load-bearing. The identifier chosen
for the type and for its constructors is not. So the collision is a consequence
of names being metadata, and not of the encoding being coarse.

## What is NOT established here

- Nothing about whether this is a defect. #159 asks whether a *type-level*
  distinction between octets and codepoints is wanted; this reproduction does
  not answer that.
- Nothing about how a store behaves when both declarations are published into
  it. That was not measured here, and no claim about naming, aliasing or
  conflict follows from anything above.
- Nothing about the boundary functions. `str-bytes`/`bytes-str` and the scalar
  range discipline were not examined.
- Nothing about `Str`'s scalar-value invariant. `SCons` admits any `Int` in
  these declarations; where the ≤ 0x10FFFF / surrogate restriction is actually
  enforced was not investigated (SPEC §3 and #69 are the places to look, and
  neither was read for this run).

## #159's falsifier, and an audit of the instrument that would decide it

Every architectural issue must keep a credible path to "no change required", so
#159's is stated here as a claim that could come back true:

> **Host discipline completely distinguishes octets from text at every `Str`
> crossing.**

What follows derives the universe that claim quantifies over, and audits whether
the existing witnesses actually measure it. **It does not answer the claim, and
no guard or test was added.**

**AND THE CLAIM IS NECESSARY BUT NOT SUFFICIENT FOR "NO CHANGE REQUIRED" — the
first draft of this section had it as sufficient, and the later sections of this
same file refute that.** Host discipline governs CROSSINGS. It says nothing
about what happens to an admitted `Str` afterwards, and both residues measured
below are in-language flows that occur AFTER admission has already succeeded:
`str-bytes` feeding a digest, and `bytes-str` reinterpreting a body. A perfectly
disciplined host admits a valid UTF-8 `Str` and a genuine octet list, and the
program then confuses them, because both are `(List Int)` and nothing has
crossed anything.

So the falsifier as stated can retire the ADMIT/PACK half of #159 and cannot
retire #159. Closing the issue as declined would additionally require showing
that the in-language flows are disciplined too — and the measurements below are
evidence against that, not for it. This is recorded rather than quietly
corrected because the overstatement is the interesting part: a falsifier scoped
to boundaries reads as exhaustive precisely when the defect has stopped being a
boundary problem.

### The universe, derived from the claim

The claim quantifies over `Str` CROSSINGS, and SPEC §3 already names them as two
normative transformations rather than leaving them to be enumerated off the
implementation. ADMIT names four sites and PACK one:

- **ADMIT** — source text (§1.4), a capability result, a command-line argument,
  a request field (§14.2). MUST decode as UTF-8 and MUST REFUSE otherwise.
- **PACK** — a `Str` becomes octets; MUST encode injectively or refuse by name.

Their runtime membership is owned by structures, not by a list anyone wrote
down: `capabilityKinds()` (`oath/program.go:288`) is the single source of truth
for the capability members — `capRequiredValue` plus every kind in
`capabilityVocabulary` — and the CLI and handler entry protocols own argv and
request fields respectively.

### The audit

| universe member | owner | guard | mutation-sensitive witness |
|---|---|---|---|
| source text → parse | `lex` | present | **yes** — `TestSourceMustBeValidUTF8`, plus a collapse test comparing two malformed sources |
| source file → read | `readSourceFile` | present | **yes** — `TestReadSourceFileRefusesMalformedBytes` |
| required value | `capRequiredValue` | present (`compile.go:985`, `llvm.go:1172`) | **yes** — `TestMalformedRequiredValueRefusesLaunch`, both backends |
| capability `env` (`capProcessEnv`) | `capabilityVocabulary` | present (`compile.go:205`) | **yes** — two tests, both backends, including the encoded-U+FFFD discriminator |
| capability `readfile` (`capFileRead`) | `capabilityVocabulary` | present in BOTH backends (`compile.go:217`; `llvm.go:1105`, reaching `o_strn_host` directly rather than through `o_str_host`, because file contents may carry a NUL) | **none found** — UNMEASURED, see below |
| capability `fetch` (`capHTTPRequest`) | `capabilityVocabulary` | present (`compile.go:230`); **Go only** — the LLVM backend refuses `http_request` by name, so there is no second crossing to witness | **none found** — UNMEASURED, see below |
| capability `emit` (`capRecordSink`) | `capabilityVocabulary` | n/a — not an ADMIT site | — see note |
| command-line argument | CLI entry protocol | present (`compile.go:1306`, `llvm.go:1047`) | **none** — measured by mutation, Go backend only |
| request field | handler entry protocol | present | yes — `handler_test.go` answers **400** on a malformed obs-text header and on a malformed request target, with an ASCII control answering 200; a `Connection`-nominated field answers **200** because exclusion runs BEFORE the check, so those octets never become a `Str` |
| PACK, `Str` → octets | `oathStrCons` (Go, runtime), `nonScalarStrElement` (LLVM, compile time) | present (`compile.go:541`, `llvm.go:279`/`:430`) | yes — `typed_lowering_test.go` refuses three non-scalar classes by name, with a control against refusing everything |

`emit` is the row that a naive reading of "every capability kind" gets wrong,
and it is why the universe has to come from the transformation rather than from
the kind list. `capRecordSink` returns the literal `"ok"`; no host octets become
a `Str` through it. Its `Str` argument travels OUTBOUND, so it is governed by
PACK, not ADMIT. Ranging over `capabilityKinds()` and demanding an ADMIT guard
per kind would have manufactured a defect here.

### The argv gap is a missing WITNESS over a PRESENT guard, and that was measured

argv is guarded in both backends. What is absent is any test that would notice
if it stopped being. Asserting that from the absence of a matching `grep` would
be exactly the reasoning this repo distrusts, so it was measured by mutation, in
a throwaway worktree:

```sh
# oath/compile.go, the argv admit — guard removed, nothing else changed
-  oathStrFromHost(fmt.Sprintf("command-line argument %%d", i), os.Args[i]), args}}
+  os.Args[i], args}}
```

The full `oath` package suite is **green** with that mutation applied:

```
$ (cd oath && go test ./... -count=1)
ok  	oath	38.858s
```

A green suite over an inert mutation would prove nothing, so the mutation was
confirmed live by building a program with the Go backend, before and after, and
running it on a malformed argument:

| Go-backend binary | `café` | `caf\xff` |
|---|---|---|
| real (guard present) | exit 0, prints `café` | **exit 70**, `command-line argument 1 is not valid UTF-8` |
| mutant (guard deleted) | exit 0, prints `café` | **exit 0**, prints `caf\xff` |

So that guard is real, its deletion changes observable behaviour, and no test in
the package fails.

**WHAT THAT ESTABLISHES, EXACTLY, AND WHAT IT DOES NOT.** The edit was to
`oath/compile.go` alone, so the demonstrated witness gap is over the GO
BACKEND's argv guard and nothing else. The LLVM argv guard (`llvm.go:1047`),
`readfile` and `fetch` were never mutated; a run of an unmutated LLVM binary is
a control, not a second measurement. Their rows in the table above therefore
read `none` on the strength of no test in the repo naming them, which is an
absence-of-witness argument and weaker than argv's — the very reasoning this
section refused to accept for argv. They are listed as UNMEASURED rather than
as demonstrated gaps, and closing that would mean running the same mutation
FOUR more times, not three — the count follows from the crossings rather than
from the capability names, and reading it off the kind list gives three:

| unmeasured crossing | site |
|---|---|
| Go `readfile` | `compile.go:217` |
| Go `fetch` | `compile.go:230` |
| LLVM argv | `llvm.go:1047` |
| LLVM `readfile` | `llvm.go:1105` |

`readfile` is two crossings because each backend admits host octets through its
own code — `oathStrFromHost` in Go, `o_strn_host` in the emitted C — and `fetch`
is one because the LLVM backend refuses `http_request` rather than implementing
it. So the four are not a capability list at all; they are the ADMIT sites that
no test names, which is the universe the claim quantifies over.

One asymmetry worth recording, because it changes what a mutation would prove.
Go calls `oathStrFromHost` once per site, so each Go crossing is separately
mutable at its call site. LLVM funnels argv and `readfile` through the single
`o_strn_host`, so a mutation INSIDE that function would delete both LLVM
crossings at once and a per-site mutation is needed to separate them. That is
the structural-owner distinction, not a defect: LLVM already has the exclusive
admission point Go spreads across four call sites.

**This is a witness gap, not a guard gap** — and the distinction is what keeps
it from being read as support for #159. Missing witnesses over present guards
are closable by writing tests, and closing them would not motivate a type at
all.

### The part that does NOT close by testing

`examples/http.oath` offers a predicate intended to guard bytes before they
reach the crypto. **It is a predicate the corpus can call, not a check the
corpus runs** — `examples/webhook.oath` names it only in property antecedents
and never in the handler, which the end-to-end measurements further down
demonstrate. What follows is about what the predicate can ESTABLISH when it is
called, which is the part a type would change; where it is called is a separate
defect and is measured separately below.

```lisp
(defn bytes-ok [] [(bs (List Int))] Bool          ; examples/http.oath:132
  (match bs
    ((Nil) true)
    ((Cons b rest) (and (and (<= 0 b) (<= b 255)) (bytes-ok rest)))))
```

That establishes membership of `0..255` and nothing else. In particular it
cannot establish that a list came from an ENCODING rather than from `str-bytes`,
which despite its name performs no encoding — it is the identity on the
codepoint list:

```lisp
(defn str-bytes [] [(s Str)] (List Int)           ; examples/http.oath:143
  (match s
    ((SNil) (Nil [Int]))
    ((SCons c rest) (Cons [Int] c (str-bytes rest)))))
```

Evaluated, with `é` — one codepoint 233, two UTF-8 octets `[195, 169]`:

```
$ ./oath/oath eval '(str-bytes "é")'
(Cons 233 Nil) : (List Int)
$ ./oath/oath eval '(bytes-ok (str-bytes "é"))'
true : Bool
```

The guard PASSES a list that is not the encoding of the text it came from, and
those are the bytes that get signed. The failing case is only incidental:

```
$ ./oath/oath eval '(bytes-ok (str-bytes "€"))'
false : Bool
```

`€` is rejected because its codepoint 8364 exceeds 255, not because provenance
was checked — every character in U+0080..U+00FF passes while denoting different
octets than its text does. The file's own comment concedes the point ("For ASCII
the two coincide … a non-ASCII secret would silently sign different bytes than
intended").

No host guard can close this, because both lists have the same type `(List
Int)` — `str-bytes`'s result and a request body are indistinguishable to any
predicate over that type. And the obvious repair does not reach it either: by
the reproduction at the top of this file, a monomorphic `Bytes` declared with a
byte list's shape is `Str`, so it separates nothing here. This is the residue of
the claim that host discipline cannot reach, and the part of #159 that some
type-level distinction would have to answer — which one is open, and this file
does not propose it.

### Status

The claim is NOT decided here. What is established is that the instrument which
would decide it is incomplete in a specific, listed way, and that its
incompleteness has two different characters — witness gaps that testing closes
(one measured, FOUR unmeasured, enumerated as crossings in the table above), and
one gap that host discipline is structurally unable to close.

And, separately from the instrument: even a green verdict on this claim would
not close #159, for the reason stated above the audit. The claim is about
crossings; the residue is in-language.

## `ключ` versus Latin-1, demonstrated against the committed corpus

`docs/experiments/webhook-friction.md` entry 3 and the comment at
`apps/github-webhook/webhook.oath:377` both claim the Latin-1 case is WORSE than
the Cyrillic one: every codepoint is in byte range, nothing is raised, and the
digest disagrees forever. That claim was taken from a source comment, which this
repo does not accept as evidence, so it was run.

Every figure below describes the TWO webhook members of the committed corpus —
`examples/webhook.oath` and `apps/github-webhook/webhook.oath` — evaluated at
their existing definitions. Nothing was added, and `oath eval` resolves names
out of `codebase/` without writing to it.

Three secrets are used throughout, each 28–32 characters so the length floor is
never what decides:

    ASCII      correct-horse-battery-staple       the control
    LATIN-1    ééé…  (31 × U+00E9)                codepoint 233, in byte range
    CYRILLIC   ключ…  (32 chars, U+043A etc.)     codepoints > 255

### The two guards disagree about which secrets they refuse

```
$ ./oath/oath eval '(bytes-ok (str-bytes "ключключключключключключключключ"))'
false : Bool
$ ./oath/oath eval '(bytes-ok (str-bytes "ééééééééééééééééééééééééééééééé"))'
true : Bool

$ ./oath/oath eval '(secret-is-usable "ключключключключключключключключ")'
false : Bool
$ ./oath/oath eval '(secret-is-usable "ééééééééééééééééééééééééééééééé")'
false : Bool
```

`bytes-ok` (`examples/http.oath:132`) refuses the Cyrillic secret and ADMITS the
Latin-1 one. `secret-is-usable` (`apps/github-webhook/webhook.oath:385`) refuses
both, because its second conjunct demands printable ASCII rather than byte
range. That difference is the entire finding, and it is why the two corpus
members behave differently below.

### End to end, with a control that discriminates

Each handler was driven at its own entry point with a CORRECTLY SIGNED request —
signed the way the outside world signs, by `openssl dgst -sha256 -hmac`, which
takes the secret as its UTF-8 octets. Body `hello` for `webhook`, `{}` and a
`ping` event for `gh-webhook`.

**The invocations in full, because a status table without the commands behind it
is a copied result rather than a measurement.** The capability records, the
headers, the timestamps and the signature construction are all here; `octets`
builds a body as a `(List Int)` term and is the same helper used again further
down:

```sh
octets() { python3 -c 'import sys
t="(Nil [Int])"
for b in reversed(sys.argv[1].encode("utf-8")): t="(Cons [Int] %d %s)"%(b,t)
print(t)' "$1"; }

A='correct-horse-battery-staple'
L=$(python3 -c 'print("é" * 31)')
C=$(python3 -c 'print("ключ" * 8)')
HELLO=$(octets 'hello')
PING=$(octets '{}')

for s in "$A" "$L" "$C"; do
  sig=$(printf 'hello' | openssl dgst -sha256 -hmac "$s" -r | cut -d' ' -f1)
  gsig=$(printf '{}'   | openssl dgst -sha256 -hmac "$s" -r | cut -d' ' -f1)
  echo "--- secret: $s"

  printf '  webhook:     '
  ./oath/oath eval "(webhook {emit (fn [(p Str)] \"ok\")
                              env  (fn [(k Str)] (if (== k \"WEBHOOK_SECRET\") \"$s\" \"300\"))}
    (Req \"POST\" \"/hook\"
      (Cons [(Pair Str Str)] (Pair [Str Str] \"x-signature\" \"$sig\")
        (Cons [(Pair Str Str)] (Pair [Str Str] \"x-timestamp\" \"1000\")
          (Nil [(Pair Str Str)])))
      $HELLO 1000))" 2>&1

  printf '  gh-webhook:  '
  ./oath/oath eval "(gh-webhook (caps-with (fn [(p Str)] \"ok\") \"$s\")
    (gh-request \"sha256=$gsig\" \"ping\" \"/hook\" \"application/json\" $PING))" 2>&1
done
```

`env` answers `300` to every key that is not `WEBHOOK_SECRET` — that is the
freshness window — and `received-at` and `x-timestamp` are both `1000`, so
`within-window` cannot be what decides any row. `emit` returns a non-empty `Str`,
so the sink cannot be what decides either. `caps-with` and `gh-request` are the
app's own constructors (`apps/github-webhook/webhook.oath:557`, `:507`), so the
GitHub handler is driven through the same shapes its own properties use.

The raw output, verbatim:

```
--- secret: correct-horse-battery-staple
  webhook:     (Resp 202 Nil Nil) : Response
  gh-webhook:  (Resp 200 Nil Nil) : Response
--- secret: ééééééééééééééééééééééééééééééé
  webhook:     (Resp 401 Nil Nil) : Response
  gh-webhook:  (Resp 500 Nil Nil) : Response
--- secret: ключключключключключключключключ
  webhook:     error: byte list element out of range 0..255
  gh-webhook:  (Resp 500 Nil Nil) : Response
```

which is the table:

| secret | `examples/webhook.oath` | `apps/github-webhook/webhook.oath` |
|---|---|---|
| ASCII (control) | **202** accepted | **200** accepted |
| LATIN-1 | **401** — silently rejected | **500** — refused, deployment named as broken |
| CYRILLIC | **error: byte list element out of range 0..255** | **500** — refused, deployment named as broken |

The control is not decoration: without it, "the digests disagree" would be
equally consistent with the `openssl` invocation being wrong. It answers 202/200
on the same code path, so the instrument discriminates.

`examples/webhook.oath` is the unguarded case, and it is unguarded by
CONSTRUCTION rather than by oversight — `bytes-ok` appears in that file only in
property ANTECEDENTS (`webhook.oath:226`, `:241`), never in the handler body
(`:174`–`:189`). So the properties are proven about the fragment of the input
space where the conflation cannot bite, and the handler ships without the check
its own specification assumes. That is the shape the friction log describes:

- CYRILLIC fails LOUDLY. The out-of-range element reaches `hmac-sha256` and
  raises. In the deployed shape recorded at `apps/…/webhook.oath:371` this is a
  per-connection panic that `net/http` recovers, so the process serves dropped
  connections while looking healthy — bad, but it announces itself on the first
  delivery.
- LATIN-1 fails SILENTLY, and the 401 is the whole problem: it is
  indistinguishable from an attacker sending a bad signature. Nothing is raised,
  no code path notices, and the operator debugs an authentication failure that
  is an encoding failure.

**The friction log's claim holds in the direction that matters.** Latin-1 is
worse, silently, and for the reason it gives. Its word "forever" is narrowed in
the next section — the narrowing does not touch the ranking.

### The digests, and what is measured versus what is inferred

The secret is GENERATED rather than written out, so the transcript is runnable
and an elision cannot silently stand in for a 31-character key. `oath eval`
prints a `Str` as its `SCons` spine, so `dec` renders it back to text:

```sh
L=$(python3 -c 'print("é" * 31)')          # 31 x U+00E9
A='correct-horse-battery-staple'
dec() { python3 -c 'import sys,re; print("".join(chr(int(n)) for n in re.findall(r"SCons (\d+)", sys.stdin.read())))'; }
for s in "$L" "$A"; do
  o=$(./oath/oath eval "(hex-encode (hmac-sha256 (str-bytes \"$s\") (str-bytes \"hello\")))" | dec)
  x=$(printf 'hello' | openssl dgst -sha256 -hmac "$s" -r | cut -d' ' -f1)
  echo "oath    : $o"; echo "openssl : $x"
  [ "$o" = "$x" ] && echo "verdict : AGREE" || echo "verdict : DISAGREE"
done
```

Its output, verbatim — first iteration the Latin-1 secret `$L`, second the ASCII
control `$A`:

```
oath    : 4938bb9bfa00ec1fcd462bfbb776996c953a877b9f83e620cfc23825385f4ba8
openssl : 3f61f74a5a51d9cf50d16c003608a1c69d43a2e13ed4fba908a2ad84d1c50201
verdict : DISAGREE
oath    : a40460d9975174bd9bc606c25634606992cc1e838d7d58ed27a00f0af68a8038
openssl : a40460d9975174bd9bc606c25634606992cc1e838d7d58ed27a00f0af68a8038
verdict : AGREE
```

**Say which half is measured.** The DISAGREEMENT is measured for the body
`hello`, and once only. What holds for every request is the KEY divergence:
`str-bytes` yields `[233]` where UTF-8 yields `[195, 169]`, so the receiver and
the sender compute HMAC-SHA256 under different keys on every single request,
whatever the body. That is a structural fact about the two encodings, not a
sampled one.

The step from there to "every legitimate delivery gets a 401" is an INFERENCE,
and it is worth stating as one: two distinct HMAC keys are not guaranteed to
produce distinct digests for every body, so agreement is not impossible — it
requires a collision between the two keyed functions. Nothing here establishes
that no such body exists, and no receiver can rely on one arriving. So the
supported sentence is *the digests are computed under different keys on every
request, and disagree on every body that does not collide*, which is weaker than
the friction log's "forever" and carries the same operational consequence.

### A THIRD site, in the other direction — where a guard fires for the wrong reason

`secret-is-usable` closes the SECRET boundary of one corpus member. It does not
close the corpus, and the site it does not reach runs in the opposite direction:
`bytes-str` (`apps/github-webhook/webhook.oath:125`) reinterprets body octets as
codepoints. Driven with a GitHub-shaped payload whose body is the actual UTF-8
octets of `{"repository":{"full_name":"café"}}`, alongside an ASCII control:

A payload is 30-odd octets, so the terms are GENERATED rather than spelled out
as a `Cons` chain — the helper is part of the reproduction, not an aside:

```sh
octets() { python3 -c 'import sys
t="(Nil [Int])"
for b in reversed(sys.argv[1].encode("utf-8")): t="(Cons [Int] %d %s)"%(b,t)
print(t)' "$1"; }

CAFE=$(octets '{"repository":{"full_name":"café"}}')
ACME=$(octets '{"repository":{"full_name":"acme/tools"}}')
```

```
$ ./oath/oath eval "(json-scoped-string \"\\\"repository\\\":{\" \"full_name\" $CAFE)"
(SCons 99 (SCons 97 (SCons 102 (SCons 195 (SCons 169 SNil))))) : Str

$ ./oath/oath eval '(str-bytes "café")'
(Cons 99 (Cons 97 (Cons 102 (Cons 233 Nil)))) : (List Int)
```

extracted codepoints: [99, 97, 102, 195, 169]     ⟶ "cafÃ©"
café is                [99, 97, 102, 233]         ⟶ the text

The extraction is mojibake, and `bytes-str` is doing exactly what it is
specified to do. `./oath/oath explain bytes-str --json` is the authority for what
it is specified to do, and it records `PROVEN (all 2 properties…)`:
`empty-is-empty` and `inverts-str-bytes`. The second is the load-bearing one —
it is the guarantee that `bytes-str` is the IDENTITY on the numbers, and neither
property asks it to DECODE anything.

**But the mojibake does NOT reach the app's output, and the first draft of this
section said it did.** Every variable field of the record passes through
`record-field` (`:287`), which admits only codepoints 32..126 and replaces the
whole field with `"-"` otherwise. Measured on the emitted line:

```sh
H='(Cons [(Pair Str Str)] (Pair [Str Str] "x-github-delivery" "abc123")
     (Cons [(Pair Str Str)] (Pair [Str Str] "x-github-event" "push")
       (Nil [(Pair Str Str)])))'

./oath/oath eval "(gh-record (Req \"POST\" \"/hook\" $H $CAFE 1000))"
./oath/oath eval "(gh-record (Req \"POST\" \"/hook\" $H $ACME 1000))"   # control
```

`eval` prints a `Str` as its `SCons` chain, so the two results are given below as
the codepoints they are, DECODED — the decoding is this file's, not the tool's:

```
café payload         ⟶  oath-gh/1	abc123	push	-	1000
acme/tools control   ⟶  oath-gh/1	abc123	push	acme/tools	1000
```

**A guard fires, and — unlike the `€` case above — it is not an accident.**
`record-field` was written to stop a tab or a quote forging a column boundary in
a TSV (`:279`), but the repository call site carries a dedicated property,
`a-non-ascii-repository-is-marked-absent` (`:625`, `tested` at 200 cases, not
proven), whose comment names this exact mechanism: `bytes-str` reinterprets a
continuation byte as a codepoint and would write mojibake, and `record-field`
marks the field absent instead. The stated reasoning is that reporting a name
the receiver cannot faithfully represent as one it does not have "is true,
rather than as a name that is subtly not the real one."

That is a deliberate, defensible choice. The cost was accepted knowingly: a
consumer of the log cannot distinguish a repository field that was absent from
one whose name was not ASCII.

### Decoding IS expressible today — measured, because two drafts claimed it was not

The first draft said the app was choosing between mojibake and data loss with no
third option, and a later one said a type separation "forces a decoder". **Both
are claims about the corpus dressed as claims about the language, which is the
error this repo tests for by asking whether a sentence is about the WORLD or
about the TOOL.** So it was settled by writing the decoder — in today's Oath,
with no language change, into a COPY of the store rather than the canonical one:

```sh
TMP=$(mktemp -d); cp -R codebase "$TMP/store"
```

`utf8.oath` in full — nothing elided, because a transcript whose source is
summarised is a copied output rather than evidence — and CREATED here rather than
displayed, so the `put` after it has something to read:

```sh
cat > utf8.oath <<'EOF'
; A 1-2 byte UTF-8 decoder, written to settle an expressibility question.
(defn utf8-cont [] [(c Int)] Bool
  (and (<= 128 c) (<= c 191))
  (prop accepts-a-continuation [] (utf8-cont 169))
  (prop rejects-ascii [] (not (utf8-cont 99))))

(defn utf8-decode [] [(bs (List Int))] (Option Str)
  (match bs
    ((Nil) (Some [Str] (SNil)))
    ((Cons b rest)
      (if (and (<= 0 b) (<= b 127))
          (match (utf8-decode rest)
            ((None) (None [Str]))
            ((Some s) (Some [Str] (SCons b s))))
          (if (and (<= 194 b) (<= b 223))
              (match rest
                ((Nil) (None [Str]))
                ((Cons c more)
                  (if (utf8-cont c)
                      (match (utf8-decode more)
                        ((None) (None [Str]))
                        ((Some s) (Some [Str] (SCons (+ (* 64 (- b 192)) (- c 128)) s))))
                      (None [Str]))))
              (None [Str])))))
  (prop decodes-cafe []
    (== (utf8-decode (Cons [Int] 99 (Cons [Int] 97 (Cons [Int] 102
          (Cons [Int] 195 (Cons [Int] 169 (Nil [Int])))))))
        (Some [Str] "café")))
  (prop a-lone-continuation-fails-closed [(rest (List Int))]
    (== (utf8-decode (Cons [Int] 169 rest)) (None [Str])))
  (prop a-truncated-sequence-fails-closed []
    (== (utf8-decode (Cons [Int] 195 (Nil [Int]))) (None [Str]))))
EOF

OATH_STORE="$TMP/store" ./oath/oath put utf8.oath --new
```

Note the third property is CONCRETE rather than guarded. The guarded form —
quantify a lead byte and require `(and (<= 194 b) (<= b 223))` — would restate
the implementation's own predicate inside the property, which this repo does not
accept as independent evidence, and the vocabulary has no `implies` anyway.

It is accepted, `total`, and its properties pass:

```
✓ utf8-cont        #7a4395e2a9e7  tested (200 cases per property) · total
    prop accepts-a-continuation   passed 200 cases
    prop rejects-ascii            passed 200 cases
✓ utf8-decode      #12fb0872f017  tested (200 cases per property) · total
    prop decodes-cafe             passed 200 cases
    prop a-lone-continuation-fails-closed passed 200 cases
    prop a-truncated-sequence-fails-closed passed 200 cases
```

`café` is codepoints 99, 97, 102, 233 and octets 99, 97, 102, 195, 169. The
decoder is applied to the OCTETS, spelled out rather than abbreviated:

```
$ OATH_STORE="$TMP/store" ./oath/oath eval \
    '(utf8-decode (Cons [Int] 99 (Cons [Int] 97 (Cons [Int] 102
       (Cons [Int] 195 (Cons [Int] 169 (Nil [Int])))))))'
(Some (SCons 99 (SCons 97 (SCons 102 (SCons 233 SNil))))) : (Option Str)

$ OATH_STORE="$TMP/store" ./oath/oath eval '(utf8-decode (Cons [Int] 169 (Nil [Int])))'
None : (Option Str)
```

Codepoint **233**, which is `café` — the `decodes-cafe` property compares against
the literal `"café"` and passes, so the decoder agrees with the language's own
lexer. Malformed input fails closed.

**This is a 1–2 byte subset**, written to settle an expressibility question and
nothing more. It is not a library, it is not proposed as one, and **the
extension to three and four bytes is NOT merely more of the same** — `E0`, `ED`,
`F0` and `F4` each need special second-byte bounds to reject overlong encodings,
surrogates, and codepoints above U+10FFFF, and a decoder built by adding
ordinary continuation checks would accept malformed UTF-8. What the subset
establishes is that the language expresses this kind of function at all, not
that a correct one is a small job.

One provenance note, because a hash is an identity and substituting one silently
would be the exact defect this file is about. An earlier draft of this section
printed `#ec9f27030cce` for `utf8-decode` while eliding the property block with
`…`. Since a definition's hash covers its properties, that hash names a source
the draft did not carry and which cannot be recovered from it. The transcript
above is a fresh run of the source printed above, in full, and `#12fb0872f017`
is that source's hash. **The measured claim is unchanged** — the decoder is
accepted, `total`, its properties pass, and the two evals answer as shown; only
the artifact is now the one a reader can actually re-run.

### `(List Int)` and `Str` are ALREADY distinct types — the conflation is one level lower

A draft of this section said a body's `(List Int)` "already IS a `Str`", so no
conversion need appear. **That is false, and it is worth recording as an error
rather than deleting, because it is the same mistake the whole file is about:
assuming two things are one type because they have the same SHAPE.** Oath keeps
them apart, in both directions, and the well-typed calls are the control:

```
$ ./oath/oath eval '(str-len (Nil [Int]))'
error: argument type mismatch: expected #e6bbed8bc934, got (#fa452d59a235 Int)
$ ./oath/oath eval '(bytes-ok "abc")'
error: argument type mismatch: expected (#fa452d59a235 Int), got #e6bbed8bc934

  control — the same two functions, correctly typed:
$ ./oath/oath eval '(str-len "abc")'                        ⟶  3 : Int
$ ./oath/oath eval '(bytes-ok (Cons [Int] 65 (Nil [Int])))' ⟶  true : Bool
```

`Str` is `#e6bbed8b…` and `List` is `#fa452d59…`. They are different objects, so
a conversion at the `Str` boundary is ALREADY mandatory and ALREADY named —
`str-bytes` and `bytes-str` are those functions. Nothing implicit is happening.

**So the conflation is not between `Str` and `(List Int)`. It is INSIDE `(List
Int)`.** A `(List Int)` that came out of `str-bytes` holds codepoints; a `(List
Int)` that is a request body holds octets; the type is identical and no guard
over it can tell them apart. That is exactly the residue the crypto section
measured, and it is one level below where a draft of this file put it.

**Which changes what the reproduction at the top of this file means.** A
monomorphic `(data Bytes [] (BNil) (BCons Int Bytes))` collides with **`Str`** —
not with `(List Int)`. So that declaration does not separate octets from
codepoints; it produces a second name for the codepoint type, which is the
opposite of what it was reaching for. The identity result kills that option for
a more specific reason than "everything is the same type", and the reason
matters for what to try instead.

**What this file does NOT establish.** Whether a distinct byte type is the right
repair, what it should be, or whether one is needed at all. Distinct types do not
by themselves force a CORRECT conversion — a reinterpreting `Bytes -> Str`
typechecks perfectly well, being `bytes-str` with the constructors renamed. All
that is established is where the ambiguity actually lives, which is the
prerequisite for choosing.

Reached from the PACK side rather than the ADMIT side, this says what the secret
case alone does not: the conflation is not a property of secrets or of
cryptography. It is a property of `(List Int)` denoting two things.

## Reproducibility caveat

The `hash` field reproduces exactly across runs. The `journal_entry` field does
not — it differs between two runs of an identical script, because a journal
entry commits more than the object. Compare the `hash`, not the entry.
