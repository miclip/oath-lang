# #74's falsifier, run against the committed corpus

**What this is.** #74 claims a capability gap: an agent has *intent* ("parse a
signed webhook payload and return a validated event") and `oath find`'s four
modes all require it to already state a *law*. This file states a falsifier for
that claim, runs it, and reports the result.

**The falsifier, as posed.**

> If each real intent from all 13 ranked demands in
> `docs/experiments/webhook-friction.md` can reach its satisfying committed
> artifact through the four existing `oath find` modes **without already
> knowing an artifact name, the target's own property text, or a formal law**,
> then no capability gap remains and #74 should be narrowed or declined.

**As posed it cannot pass, and that is decided before any query runs.** Every
mode takes either a name or a `(defn ...)` carrying a formal law — the table in
the next section is read off the command's own argument list — so the forbidden
set is exactly the accepted set. Stated this way the falsifier is settled by one
line of CLI surface and 13 runs add nothing to it. Recorded rather than quietly
repaired, because a falsifier that cannot pass is not a measurement, and this
one was carried into the run before the defect was noticed.

**So the runnable form, which is what the 13 intents were actually measured
against:**

> **F2.** An author holds one of the 13 intents and does *not* know which
> artifact satisfies it. They write the best query they can — any mode, any
> phrasing, a law in their own words. Does a mode reach the satisfying artifact?

F2 forbids only what an author genuinely cannot have: the target's **name** and
the target's **own property text**. It permits a self-authored formal law,
because that is a cost an author can pay, and permitting it is what makes the
question empirical.

**"Reach" is two-valued, and collapsing it loses the result.** `oath find` can
name an artifact in two quite different ways:

> **SURFACED** — the artifact appears anywhere in the output, including in the
> unranked "COMPATIBLE SIGNATURE" list a miss prints. The author still has to
> read each candidate's properties to know which one answers them.
>
> **SATISFIED** — a mode reports the artifact as satisfying the query: a
> property-hash match, or a proof. The answer is the output, not a shortlist.

Both are reported. They differ sharply on this corpus, and the difference is the
most useful thing the run produced.

**Reproducing it.** Every query is committed:
`docs/experiments/issue-74-falsifier/queries/*.oath`, driven by `run.sh` in the
same directory, with the run that produced this report in `transcript.txt`.
`-verbatim` queries carry the target's own property text; `-authored` queries
state the same intent in a form the target does not use, and are what the F2
score is computed from. `FAST=1` skips the two `--implies` runs that take 48s
and 992s.

**What was run.** `oath find` in all four modes against `./codebase` at
`3d2ba4a`, using the binary from `make build`. `find` is read-only —
`git status --porcelain codebase/ fixtures/` was empty afterwards. The demand
log (`oath demand --all`) recorded nothing, correctly: `demandRecording` is off
for the local CLI by deliberate design (`oath/demand.go`), because a read that
writes to a git-tracked store would make two clones diverge by who searched
what.

---

## The four modes, and what each takes as input

This is structural and can be read off the command surface before any query
runs. It is stated first because it determines the shape of every result below.

| mode | invocation | what the caller must already have |
|---|---|---|
| 1. by example | `oath find <name>` | **a name** |
| 2. by fresh spec | `oath find --spec <file>` | **a formal law** whose canonical property AST hashes equal to the target's |
| 3. by proof-implication | `oath find --implies <file>` | **a formal law** + a signature compatible with the target's, up to primitive leaves |
| 4. by body-equivalence | `oath find --equiv <name>` | **a name** |

There is no fifth door. An intent submitted as text is parsed as a name:

```
$ oath find "take the longest prefix of a list satisfying a predicate"
error: no definition named "take the longest prefix of a list satisfying a predicate"
$ oath find --equiv "prefix while predicate holds"
error: no definition named "prefix while predicate holds"
```

**A shape prerequisite is a finding of this run and is not in #74's framing.**
Modes 2 and 3 both take a `(defn ...)`, so stating the law commits the caller to
the sought function's type — but the two enforce it by different mechanisms, and
the difference matters to anyone designing on top of them.

- **`--spec` does not compare signatures at all.** `findFromDef` (`oath/api.go`)
  matches purely on `propHashGeneral` and never reads `Def.Ty`. The shape
  constraint is indirect: a property body typed against `(List Str)` canonically
  encodes differently from one typed against `Str`, so it hashes differently.
  Signature enters only in the MISS branch, where `signatureNeighbours` builds
  the "COMPATIBLE SIGNATURE" hint list.
- **`--implies` does prefilter by signature** — compatible up to primitive
  leaves, with the query's binders re-typed — before attempting any proof
  (`docs/discovery.md`).

Both generalize primitive leaves, but not equally far. `propHashGeneral`
generalizes only the property's **binder** types (`generalizeTypes`, `canon.go`);
types written into the property **body** — a `(Nil [Int])`, a callee's type
application — are encoded verbatim, so an `Int` query reaches its `Rat` or `Bool`
counterpart under `--spec` only when the law's body carries no types. That is the
documented "body-embedded types aren't generalized yet" limit in
`docs/discovery.md`. `--implies` re-types the query's binders against each
candidate, so it crosses primitive leaves more freely, and rejects rather than
approximates a property whose body carries its own annotations.

Neither crosses a difference in *structure*: a query returning `(List Str)` does
not reach a definition returning `Str`, and a monomorphic query does not reach a
`forall a` definition.

So the prerequisite, stated exactly, is not the target's signature but its
**structural shape**. A caller who has the behaviour right and the shape wrong
gets nothing — not a near miss, not a hint. Intent 1 below is a controlled
instance.

---

## Results — all 13 ranked demands

**How each intent was derived, because this is the method's weakest link.** Most
of the 13 demands are not discovery questions. Their headline wants are for
things no artifact could supply: demand 1 wanted the program to *refuse to
start*, demand 2 wanted a header the adapter had already canonicalized away,
demand 3 wanted a real JSON parser and a byte-to-text decoder that cannot be
written. Scoring those against `oath find` would be meaningless.

So each row states an **artifact-seeking sub-intent** derived from the demand —
the search an author would plausibly run at that moment of friction — and pairs
it with the committed artifact that answers *that*. Demand 1's sub-intent is
"which required keys are missing", not "refuse to start"; `config-missing`
answers the sub-intent and nothing answers the want. Where a demand's want has no
artifact at all, that is recorded as `NO ARTIFACT` and left **unscored**, which
is why five rows carry no score.

The derivation is a judgement, it is mine, and it is the part of this experiment
a second reader should attack first. It is stated here rather than left implicit
because the alternative — scoring demands against artifacts that do not satisfy
their headline want — would silently overstate what `find` was asked to do. The
two gaps are different and are kept apart throughout: *the corpus lacks what the
demand wanted* (unscored) versus *discovery cannot reach what the corpus has*
(what F2 measures).

Each row then reports the best result of a query an author with that sub-intent,
but not the artifact, could actually write — the `-authored` files in
`queries/`.
`NO ARTIFACT` rows are **unscored**: the demand has no satisfying committed
artifact, so discovery is not the binding constraint on it, and counting one as
a discovery failure would report a gap in the corpus as a gap in `find`.

Demand 3 asks for two things and is split into two rows, so the table has 14
rows for 13 demands.

| # | intent | target artifact | SURFACED | SATISFIED | how |
|---|---|---|---|---|---|
| 1 | report a required config key the host did not supply | `config-missing` (PROVEN) | **no** | **no** | wrong return shape; corrected, both modes then find it |
| 2 | read a request header by name, with a fallback | `header-or` (tested) | yes (1 of 1) | **yes** | `--implies`, 0.03s |
| 3a | scan a byte body for a JSON string value, stopping at a quote or control byte | `json-string-value` (tested) | yes (1 of 5) | **no** | `--implies` NO VERDICT: outside the provable fragment |
| 3b | decode bytes to text correctly | — | *unscored* | *unscored* | NO ARTIFACT — cannot be written |
| 4 | make every gate cover a new corpus member | — | *unscored* | *unscored* | NO ARTIFACT — a build-scope gap |
| 5 | build a valid signed `Request` for a property | `gh-request` (tested) | *excluded* | *excluded* | hit, but the query came out AST-identical to the target's own law |
| 6 | make a field safe to splice into a delimited record | `record-field` (tested) | yes (1 of 3) | **no** | `--implies` NO VERDICT: outside the provable fragment |
| 7 | know how many generated cases reached the conclusion | — | *unscored* | *unscored* | NO ARTIFACT — a tooling signal |
| 8 | run a check once, before the port binds | — | *unscored* | *unscored* | NO ARTIFACT — a protocol shape |
| 9 | a prefix match that only matches at a delimiter | `media-type-is`, `path-is` (tested) | yes (2 of 3) | **yes** | `--implies`, 2.59s — and it REFUTES `str-prefix`, which `--spec` had matched |
| 10 | (not friction — the record model earned its keep) | — | *unscored* | *unscored* | NO ARTIFACT |
| 11 | test whether a list of `Str` contains an element | `any` (PROVEN), used with a predicate | **no** | **no** | no mode bridges to a different structural shape |
| 12 | take the longest prefix whose elements pass a test | `take-while` (PROVEN) | **no** | **no** | needs the target's polymorphism shape |
| 13 | tell packaging a capability apart from leaking one | — | *unscored* | *unscored* | NO ARTIFACT — a confinement verdict |

**Score.**

- **Posed falsifier: 0, and predetermined.** No query can satisfy it, for the
  structural reason above. Reported for completeness; it carries no evidence.
- **F2, over 8 eligible demands, of which 7 are scored:**

  | | reached | not reached |
  |---|---|---|
  | **SURFACED** | 4 (2, 3a, 6, 9) | 3 (1, 11, 12) |
  | **SATISFIED** | 2 (2, 9) | 5 (1, 3a, 6, 11, 12) |

  Intent 5 is **excluded from both**, scored against the run's own interest, and
  it is the one judgement call in the table. Its `--spec` query hit
  `gh-request`, and the law — "the request I build carries the body I gave it" —
  is one an author plausibly writes unprompted. But it came out AST-identical to
  `gh-request`'s own `keeps-the-body`, written after reading it, and `--spec`
  succeeds by matching exactly that hash. The run therefore cannot distinguish an
  independent hit from a copied one. Field-projection round-trips having a
  near-canonical law is a real observation and a reason to expect this class to
  be easier; it is not evidence from this run. `--implies` also proves
  `gh-request` satisfies it, in 0.02s — so a reader who accepts the law as
  independently authored should read the SATISFIED count as 3 of 8.

**The gap between the two rows is the finding.** Four of seven put the answer in
front of the author; two of seven got it identified. The remainder is a shortlist the author must disambiguate by reading
each candidate's properties — which is precisely the prerequisite loop #74 is
about, arriving one step later than the issue describes it.

---

## The five results that carry the argument

### Intent 12 — `take-while`, the one demand that IS a discovery finding

Entry 12 is the friction log's own report that `oath find` never surfaced:
`bytes-until` was written by hand while `take-while` sat PROVEN in the corpus.
So this is the case the falsifier most needs to settle.

**(a) The law an author would actually state from intent** — "every byte I keep
passed the test":

```
(defn wanted [] [(p (-> Int Bool)) (xs (List Int))] (List Int) (Nil [Int])
  (prop every-kept-byte-passes [(p (-> Int Bool)) (xs (List Int))]
    (all [Int] p (wanted p xs))))
```

`--spec`: `no definition states this law as written`, and no signature-compatible
candidates either. `--implies` on the same file, in 0.04s:
`(no definition provably satisfies this — in the signature-compatible, provable
set)`. The law is true of `take-while` and neither mode reaches it, because the
query is monomorphic and `take-while` is `forall a`.

**(b) `take-while`'s own law, copied verbatim from `oath get`, written
monomorphically** — still a miss, on both properties:

```
· empty-is-empty [tested here]  #63e28ffbfab6
    no definition states this law as written (matched by property content hash)
· cons-step [tested here]  #1646dbcdbb82
    no definition states this law as written (matched by property content hash)
```

**(c) The same law written polymorphically, with explicit type application** —
now it hits:

```
· empty-is-empty [tested here]  #126dc643c88a
    drop-while  (proven as "empty-is-empty")  ← a proven implementation of this spec
    filter      (proven as "empty-is-empty")  ← a proven implementation of this spec
    take-while  (proven as "empty-is-empty")  ← a proven implementation of this spec
· cons-step [tested here]  #81c7bdb13d5d
    take-while  (proven as "cons-step")  ← a proven implementation of this spec
```

So the flagship demand needs **three** pieces of prior knowledge, not one: the
law, the signature, and the target's *polymorphism shape*. The last is the
documented `propHashGeneral` limit ("body-embedded types aren't generalized
yet", `docs/discovery.md`) meeting an intent query, and it is invisible to the
caller: (b) and (c) state the same mathematical law and differ only in whether
the query's own recursion carries a `[Int]`.

Two smaller facts from the same run, both worth having: `oath find take-while`
— mode 1, with the name in hand — *also* misses on `cons-step` and falls through
to the signature list; and `oath find --equiv take-while` reports
`(no other definition normalizes to the same form)`, as does `--equiv
record-field`. Mode 4 is name-keyed by construction, so it cannot serve an
intent at all; on the two targets it was run against it also found nothing.

### Intent 11 — the intent whose answer has a different shape

Entry 11: `contains` and `index-of` are `Int`-only. The author wanted membership
over `(List Str)`.

```
(defn wanted [] [(x Str) (xs (List Str))] Bool false
  (prop head-is-a-member  [(x Str) (xs (List Str))] (wanted x (Cons [Str] x xs)))
  (prop nothing-is-in-empty [(x Str)]                (not (wanted x (Nil [Str])))))
```

`--spec`: nothing, and no signature-compatible candidates.
`--implies`: `(no definition provably satisfies this — in the signature-compatible, provable set)`.

**And the corpus does answer this intent.** `any` is PROVEN and polymorphic:

```
$ oath eval '(any [Str] (fn [(s Str)] (== s "beta")) (Cons [Str] "alpha" (Cons [Str] "beta" (Nil [Str]))))'
true : Bool
```

No mode can reach it, because the satisfying artifact has a **different
structural shape** from the intent — `(-> (-> a Bool) (List a) Bool)`, not
`(-> Str (List Str) Bool)`. Generalizing primitive leaves does not help: the
mismatch is an extra function parameter, not a leaf type. This is a gap distinct
from law-statement and it survives any improvement to how laws are written — the
answer to "does this list contain x" is a *composition*, not a definition.

> **THE LAST SENTENCE IS FALSIFIED, and by #175's own falsifier rather than by
> argument.** Re-asked in the higher-order shape — the same intent, a law in the
> author's own words, no tool change — `--implies` PROVES `any` satisfies it and
> refutes `all` with a countermodel. What this run varied was the LAW; what
> reaches `any` is varying the SHAPE, and this paragraph treated the two as one
> thing. The narrower claim survives: generalizing primitive leaves does not
> bridge an extra parameter. Record: `docs/experiments/issue-175-shapes.md`.
> The table above is left as it was run.

### Intent 6 — the strongest mode reports only the wrong definitions

Entry 6's fix was `record-field`. Paraphrased intent — "the output is printable
ASCII", rather than `record-field`'s own `never-contains-a-tab`:

`--spec` misses and lists three signature-compatible candidates
(`config-key`, `record-field`, `shout`). `--implies` — the mode that searches by
proof rather than shape — returns:

```
· output-is-printable
    2 REFUTED — proved NOT to satisfy it (a countermodel exists)
        config-key   countermodel (by evaluation): (SCons -9 SNil)
        shout        countermodel (by evaluation): (SCons -9 SNil)
    1 NO VERDICT — the prover did not settle it (a limit of this prover, NOT a fact about the definition)
        record-field   "lam" terms are outside the provable fragment
```

`record-field` — the definition the author wanted — IS named, under `--details`,
and the report is scrupulously honest about why: #156's four-way split says
plainly that a NO VERDICT is a fact about the prover, not about the definition.
So this is SURFACED, and the limit is precise: the mode **cannot certify** the
candidate it has already put in front of you. The two names it does certify are
refutations of definitions the author never wanted.

Proof-implication therefore cannot be the whole intent front door: any target
outside the provable fragment reaches the author as an uncertified candidate,
and `record-field` is such a target because it passes a lambda to `all`.

**This is not a one-off.** Intent 3a hits the identical wall. Paraphrased as
"the result holds no quote", `--implies` reported, after 992s:

```
· result-holds-no-quote
    2 NO VERDICT — the prover did not settle it (a limit of this prover, NOT a fact about the definition)
        json-string-value   "lam" terms are outside the provable fragment
        sort                no direct proof; induction did not discharge
```

`json-string-value` passes a lambda to `take-while`. Two of the seven not-reached
intents (3a, 6) fail for this same reason, and both targets are ordinary
application code — a byte scan and a field sanitizer. Passing a lambda to a list
combinator is not an exotic shape.

### Intent 1 — a wrong shape guess returns nothing, not a near miss

Entry 1's sub-intent, in artifact-seeking form: "report a required key the host
did not supply." The satisfying artifact is `config-missing`, PROVEN — which
returns the *first* such key. An author who has not read it would as reasonably
expect a list:

```
(defn wanted [] [(have (List Str)) (need (List Str))] (List Str) (Nil [Str])
  (prop nothing-needed-nothing-missing [(have (List Str))]
    (== (wanted have (Nil [Str])) (Nil [Str])))
  (prop a-missing-key-is-reported [(k Str)]
    (== (wanted (Nil [Str]) (Cons [Str] k (Nil [Str]))) (Cons [Str] k (Nil [Str])))))
```

`config-missing` returns `Str` — the *first* missing key. Both query properties
report `no definition states this law as written` — the property AST hashes
differently — with **no** signature-compatible fallback either, because
`signatureNeighbours` finds none. `--implies` on the
same file says `(no definition provably satisfies this — in the
signature-compatible, provable set)` for both. The mode's answer is
indistinguishable from "the corpus has nothing".

**The control.** Change only the return type — `(List Str)` to `Str` — keeping
both laws otherwise identical in meaning:

```
(defn wanted [] [(have (List Str)) (need (List Str))] Str SNil
  (prop nothing-needed-nothing-missing [(have (List Str))]
    (== (wanted have (Nil [Str])) SNil))
  (prop a-missing-key-is-reported [(k Str)]
    (== (wanted (Nil [Str]) (Cons [Str] k (Nil [Str]))) k)))
```

```
--spec     · nothing-needed-nothing-missing
               config-missing  (proven as "nothing-required-is-complete")  ← a proven implementation
           · a-missing-key-is-reported
               no definition states this law as written
               1 definition(s) have a COMPATIBLE SIGNATURE: config-missing
--implies  · nothing-needed-nothing-missing   config-missing  ← provably satisfies it
           · a-missing-key-is-reported        config-missing  ← provably satisfies it
```

One guess about the return *shape* is the whole difference between an empty
result and the right answer proved twice. The author's behavioural understanding
was correct in both runs. Note this is not a primitive-leaf difference, which
both modes generalize over — it is `(List Str)` against `Str`, and nothing
bridges that.

Recorded honestly: entry 1's *actual* fix was a language change (#126 —
provisioning a required value), not an artifact. A perfect discovery layer would
not have satisfied entry 1. It is in the table because its intent has a
satisfying artifact today, which is what the falsifier quantifies over.

Intent 3a is worded to what `json-string-value` actually does, not to what
demand 3 wanted. The scanner assumes compact JSON, does not understand escapes,
cannot address a nested path and can read the wrong object — all four are
documented in `webhook-friction.md`. The demand's real want was a JSON parser
and it is unmet; the sub-intent that has an artifact is the scan, and that is
what is scored.

### Intent 3b — the demand with no artifact, and why that is not a discovery result

Entry 3's sub-finding is that `str-bytes : Str -> (List Int)` is PROVEN and its
inverse "does not exist and cannot be written correctly", because `Str` is a
list of codepoints and a request body is a list of bytes. `bytes-str`
reinterprets rather than decodes. `oath find` cannot fail to surface a
definition that is absent, and #159 has since measured that a refinement of the
form `{v : (List Int) | P v}` cannot separate the two roles either, because one
value carries both.

It is listed as `NO ARTIFACT` rather than as a discovery failure for that
reason. Counting it against `find` would report a gap in the corpus as a gap in
the discovery layer.

---

## What DID work, stated fairly

F2 is not satisfied, and three things in the current layer are
nonetheless doing real work. Omitting them would make this a brief for #74
rather than a measurement.

**1. `--implies` survives paraphrase, for the fragment it can prove.** It is the
mode behind both SATISFIED results. Intent 2, a law stated in a form `header-or`
does not use:

```
· no-headers-means-the-default
      header-or  ← provably satisfies it (direct (lemma-free))          0.03s
```

Intent 9, where it also *discriminates*: `--spec` had matched `str-prefix` on
the verbatim `exact-matches` law, and proof refutes it on the second property —
a longer value does match `str-prefix`, which is the whole defect entry 9 was
about.

```
· identical-is-a-match
      media-type-is  ← provably satisfies it (direct (lemma-free))
      path-is        ← provably satisfies it (direct (lemma-free))
      str-prefix     ← provably satisfies it (direct)
· an-extra-letter-breaks-the-match
      media-type-is  ← provably satisfies it (induction on binder 0)
      path-is        ← provably satisfies it (induction on binder 0)
      1 REFUTED — str-prefix, countermodel (by evaluation): SNil          2.59s
```

A second run establishes the same robustness more strongly, and it is a
**separate control, not intent 11**. Intent 11's real query is over
`(Str, List Str)` and reaches nothing (above). This one asks the same question
over `(Int, List Int)`, where `contains` — a signature-compatible artifact —
does exist:

```
· empty-has-no-members
      contains   ← provably satisfies it (direct (lemma-free))
      si-member  ← provably satisfies it (direct (lemma-free))
· a-member-of-the-tail-is-a-member
      contains   ← provably satisfies it (direct (lemma-free))
      1 NO VERDICT — si-member: no direct proof; induction did not discharge   48.4s
```

Neither of `contains`'s own three property names or statements appears in the
query. So the residue after `--implies` is *any formal statement at all*, plus a
signature guess — not *the target's* statement. That is a real narrowing of
#74's claim, and it is why the issue should be narrowed rather than restated.

The control also isolates intent 11's failure cause. Same intent, same laws,
`Int` instead of `Str`: found and proved. The Str version fails not because the
law was badly stated but because no signature-compatible artifact exists — the
answer is `any` applied to a predicate.

**2. The signature-compatible fallback is a partial intent front door that
already exists, and it is what SURFACED 4 of 7.** In every case where a
signature-compatible definition existed at all, the correct target was in the
list: 1 of 1 (`header-or`), 1 of 3 (`record-field`), 1 of 5
(`json-string-value`), 2 of 3 (`media-type-is` and `path-is`), and 2 of 2
(`contains`, in the control).
It is not ranked and not filtered by evidence — the `json-string-value` list
carries `bad-reverse`, `FALSIFIED`, alongside `sort` and `reverse` — and its
closing instruction is `Try get <name> to read how each states its properties`,
which is the prerequisite loop F2 exists to detect. But it is the nearest
thing to an intent surface in the tool today, and any design should start from
it rather than beside it.

**3. The decision package #74 asks for is already shipped.** #74's "what the
response should contain" list — exact specs, provenance, dependency closure by
hash, proof status per property, honest limitations — is `oath explain`, and it
is more honest than the issue's list asks for. On `take-while` it volunteers a
stale spec-strength measurement, absent authorship separation, unstated
licensing, and that the recorded authorship is unsigned and so not independently
checkable.

Its entry point is a **name**, so it sits downstream of the missing translation
step rather than substituting for it. But it is not work #74 needs to design.

---

## The three design constraints, restated

From the issue, unchanged, because they bound whatever gets built:

1. **This must not touch identity.** Intent-matching is fuzzy and model-driven;
   it belongs strictly in the discovery layer, which draws edges over the hash
   graph and never merges or redefines one.
2. **The evidence must stay honest under fuzzy search.** Relevance ranking must
   never blur `proven` / `tested` / `REFUTED` / `NO VERDICT`. A fuzzy front end
   over an exact evidence base is fine; a fuzzy evidence base is not.
3. **A deterministic escape hatch must remain.** An agent must always be able to
   bypass intent-matching and query by exact spec, so results stay reproducible
   when it matters.

Two observations from this run bear on them. Constraint 2 is currently satisfied
by construction and would be easy to lose: the signature-compatible fallback
already lists a `FALSIFIED` definition beside proven ones with no ordering, and
that list is the surface an intent front door would most naturally extend.
Constraint 3 is satisfied today at zero cost — modes 2 and 3 *are* the escape
hatch, and nothing proposed here removes them.

---

## Conclusion: NARROW

One disposition, deliberately. An earlier heading read "NARROW, then proceed to
design on what remains", which is two verdicts wearing one sentence: it narrows
the issue AND rules on what to do next. The scope that survives is described
below as scope — what #74 still asks that the corpus does not answer — and
whether to design against it is not this file's call.

**F2 is not satisfied.** Over 7 scored eligible intents, 4 were SURFACED and 2
were SATISFIED. Of the 5 not satisfied: two (3a, 6) are blocked by the provable
fragment, one (11) by a structural-shape mismatch its author could not have
guessed around, one (12) by a polymorphism shape, and one (1) by a single wrong
guess about a return shape. The posed falsifier's 0 is predetermined and carries
no evidence. #74 is not declined.

It should be **narrowed** first, because part of what it asks for exists. The
decision package — candidates with exact specs, provenance, dependency closure,
per-property proof status, honest limitations, machine-readable reasons to
choose — is `oath explain`, shipped and name-keyed. That half should leave the
issue's scope.

**The surviving gap, named exactly:**

> Every `oath find` mode requires the caller to supply, in advance, one of two
> things it cannot derive from intent: **a name** (modes 1 and 4), or **a
> well-typed formal property whose type matches the target's structural shape**
> (modes 2 and 3). No mode accepts a description of what the caller wants to
> accomplish. Primitive leaves in the binders are generalized, so `Int` can reach
> `Rat`; structure is not, so a wrong guess about arity, return shape or
> polymorphism returns an empty result indistinguishable from an empty corpus —
> enforced by the property hash in `--spec` and by a signature prefilter in
> `--implies`.

Three sub-gaps sit inside it, and they are separable work:

- **the translation itself** — intent text to a candidate property *and a
  candidate structural shape*. This is #74's original claim and it stands; the
  shape half is the part its framing does not mention.
- **shape mismatch** — the satisfying artifact may have a different signature
  from the intent, or be a *composition* rather than a definition. Intent 11 is
  the witness: `any` answers it, is PROVEN, and is unreachable from the intent by
  any mode. Not implied by #74's framing, and not fixed by better law-writing.
- **fragment coverage** — proof-implication is the mode that best survives
  paraphrase, and it cannot serve targets outside the provable fragment.
  `record-field` and `json-string-value` are both outside it, for the same
  reason: they pass a lambda to a list combinator. An intent layer resting on
  `--implies` alone would be silently blind to that shape, and its latency
  ranged from 0.03s to 992s across four queries in this run.

The first is the issue as filed. The second and third are what running it found,
and both belong on #74 rather than as new issues, since neither is coherent
apart from the translation step.

## What this does NOT establish

- **Nothing about programs in general.** This measures 13 demands from one
  application against one committed corpus (195 functions and 15 datatypes, as
  `oath ls` reports it at `3d2ba4a`). `webhook-friction.md`
  is a demand list from building one webhook receiver, and `codebase/` is the
  exhibits this project chose. A different corpus, or a registry large enough
  that a weak intent-shaped law admits hundreds of candidates, would produce
  different numbers and could change which sub-gap dominates.
- **No claim that the paraphrases are the paraphrases an author would write.**
  They were written by someone who had read the targets. That biases *toward*
  hits, not away — the safe direction for a result that concluded "not
  satisfied", and the wrong direction for reading the `--implies` successes as a
  general rate.
- **No design.** Nothing here proposes a mechanism, and no `oath find` code was
  changed.
- **Nothing about whether the demands were well served.** F2 asks only whether
  the discovery layer reaches an artifact the corpus already holds. Several
  demands' headline wants are unmet by any artifact and stay unmet; that is
  `webhook-friction.md`'s finding and is untouched here.
