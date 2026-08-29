# Friction log: building a consumer that DISCOVERS its dependencies

The instrument is [`discovery-consumer/run.sh`](discovery-consumer/run.sh), which
asserts the discovery-layer behaviour behind these findings and exits 0 on 47/47
checks in about seven seconds (the §4 timings are the one exception — see there). It
builds `args-in-reverse : (-> (List Str) Str)` — print the arguments reversed and
comma-separated — whose entire body is `(str-join 44 (reverse [Str] args))`.
Neither algorithm is written in the app; both were found by asking the commons
what it can prove, with no name assumed, and then referenced.

This is the demand list that fell out of doing it. Ranked most valuable first,
which is **not** the order in which the frictions were met.

Every number here was produced against the corpus **as of commit `18401fa`**
(238 function names, 237 distinct objects, in a store holding 342 objects), and —
with ONE class of exception — is asserted by `run.sh` on every run. The counts
are pinned to that immutable commit deliberately: `run.sh` re-derives them live
from `HEAD:codebase`, so a reader on a later corpus gets that corpus's numbers
from the script while this prose stays a faithful record of the run it describes.
The exception is the **timings in §4** (`1.8 s`, `0.08 s`, the two-minute kill):
those are hand-measured wall-clock observations from building this, NOT among the
script's 47 checks — `run.sh` contains no timing assertion and no `Str`-typed
reversal query, and it is marked as such where it appears. Everything else is a
measurement of THIS corpus, not of programs in general.

---

## 1. `oath find --spec` had no way to say FALSIFIED — FIXED

This was the sharpest finding of the exercise — the one a consumer is most likely
to be hurt by, because it appears at the moment of choosing — and it has since
been fixed. `run.sh` now asserts the corrected behaviour, so this section records
both the gap and its repair.

**The gap.** The corpus contains `bad-reverse`, the identity function. It
**states** both of `reverse`'s laws and is recorded
`FALSIFIED: antidistributes-over-append`. Querying `--spec` with those two laws
used to report:

```
  · antidistributes-over-append [tested here]  #2f9879ad154e
      bad-reverse        (tested as "antidistributes-over-append")
      reverse            (proven as "antidistributes-over-append")  ← a proven implementation of this spec
```

`bad-reverse` was rendered **`tested as`** for the very property it was refuted
on, under a header reading *"which proven definitions satisfy it"* — while
`oath ls` said, of the same object,
`bad-reverse … FALSIFIED: antidistributes-over-append`. The renderer mapped a
per-property **boolean** (`proven ? "proven" : "tested"`) so a refuted property
was structurally indistinguishable from one that merely passed generated tests.
A consumer reading `--spec` alone got *"tested against your law"* about a
definition **disproved** against it. The distinction the rest of the project is
careful about — *no proof is not disproof* — lived in `--implies` and was absent
from the cheaper surface reached first.

**The fix.** `oath/api.go`'s spec-query renderer now carries a THREE-valued
verdict — `proven` / `REFUTED` / `tested` — reading the same `Guarantee.Falsified`
field `oath ls` prints. A refuted match renders loudly and sorts last:

```
  · antidistributes-over-append [tested here]  #2f9879ad154e
      reverse            (proven as "antidistributes-over-append")  ← a proven implementation of this spec
      bad-reverse        (REFUTED as "antidistributes-over-append")  ← DISPROVED for this law: a countermodel exists (find --implies --details shows it)
```

It was a reporting gap, not a verification gap: the information was already in the
store. Note the honest nuance the three-valued mark exposes — `bad-reverse` is
still `(tested as "involution")` under the FIRST law, because the identity
function genuinely IS an involution; it is REFUTED only on the law it actually
fails. One definition, two different verdicts, each now visible.

The change covers both `oath find <name>` and `oath find --spec` (a shared
renderer), is guarded by `TestFindSpecMarksRefutedDistinctly` (plus a
tested-stays-tested control), and `run.sh` asserts the exact `REFUTED` row and
its disproved flag so a regression to two-valued trips the check.

Before the fix, two unrelated things saved the consumer, and a caller with less
discipline would have had neither: the signature-probe fallback list printed the
definition-level guarantee, and `--implies` refuted it with a countermodel. The
fix means `--spec` itself now carries the verdict, rather than leaving it to those
two backstops.

---

## 2. There is no import: reuse rides entirely on ambient store names

Discovery hands back a **name and a hash**. The consumer can use neither as a
dependency declaration.

`app.oath` says `(str-join 44 (reverse [Str] args))`. Those are bare identifiers
resolved by `oath put` against whatever `$OATH_STORE` happens to be at the moment
of the put. The file cannot say:

- *"my reversal is `#7bb6285884d068b0...`"* — there is no syntax to pin a hash;
- *"fetch `reverse` from this registry"* — there is no fetch in the source path;
  the definition must already be in the store the put runs against.

So the whole discovery story — *no name trusted* — terminates one step before the
artifact. Everything up to selection is name-independent; the act of USING the
selection is name-keyed, against an ambient, mutable, per-store binding. If two
stores spell `reverse` differently, or one has been repointed, the same source
file elaborates to a different object with no diagnostic.

Evidence is available **after** the fact — `oath explain` prints
`DEPENDENCIES (5, exact by hash)` and the script asserts those against
`names.json` at HEAD — but that is verification, not specification. It tells you
what you got, not what you asked for. Nothing in the source expressed an
intention that could have been violated.

**What would fix it:** a way for a definition to pin a dependency by hash, with
the name as a hint rather than the authority — the same inversion the registry
already applies everywhere else. Failing that, a `--expect <name>=<hash>` flag on
`put` would at least make the intention checkable at elaboration time instead of
inspectable afterwards.

This is the single largest gap between what the discovery layer promises and what
a consumer can actually hold onto.

---

## 3. The natural query is the wrong query, and finding that out costs a round trip

The CLI's arguments are a `(List Str)`, so the first reversal query was written
monomorphically over `(List Str)`. It finds **nothing**:

```
  · involution
      3 REFUTED — proved NOT to satisfy it (a countermodel exists)
          gh-drop-final-empty countermodel (by evaluation): (Cons SNil Nil)
          gh-group-labels    countermodel (by evaluation): (Cons SNil Nil)
          gh-refused-lines   countermodel (by evaluation): (Cons SNil Nil)
```

`reverse` is never considered. The reason is structural and invisible from the
query: `reverse` is `forall a`, the re-typed property would have to pass a type
argument the candidate does not take, so every polymorphic definition is rejected
before the prover is asked. The corpus's list combinators are *all* `forall a`.
Restating the query as `[a]` with explicit `[Int]` applications reaches `reverse`
immediately.

`docs/discovery.md` documents this as the "polymorphism axis", and the doc is
right. The friction was that **nothing in the failing output pointed at it** —
three refutations read like *"the corpus has three candidates at this shape and
all three are wrong"*, which is true and useless; what the consumer needed was
*"and N polymorphic definitions at this shape were never examined."*

**FIXED (the primary half).** `find --implies` now says exactly that when a
monomorphic query proves nothing and a polymorphic definition whose shape
*subsumes* it was skipped:

```
  3 polymorphic definition(s) share this shape but could not be lined up: proof-implication
  matches types, and THIS query is monomorphic while they range over one or more type
  parameters. Restate the query polymorphically — declare the SAME type parameters they
  have and pass them explicitly in the property (e.g. `[a]` in the signature and
  `(wanted [a] xs)` in the law) — to reach them.
```

A monomorphic query silently skipped these because `generalizeTypes` frees only
primitive leaves, so `(List Str)` and `(List a)` are genuinely different shapes; a
one-sided subsumption match (`typeSubsumes`) recognises that the polymorphic
`reverse` *would* match if the query were phrased polymorphically. The note is a
diagnostic only — it changes no verdict — and appears only on a complete search
that proved nothing, so a hit or an abort still speaks for itself. The round trip
is now guided rather than blind.

Two smaller pieces of the same friction:

- **The shape probe is an idiom, not a command.** The move that actually works —
  write a reflexive law that states nothing, so `--spec` falls through to listing
  every signature-compatible definition with its guarantee — is a documented
  trick that depends on no definition in the corpus ever stating reflexivity.
  It is genuinely the most useful single query in this whole exercise (it is what
  surfaced `str-join`, and it is where `bad-reverse`'s FALSIFIED label appeared),
  and it is spelled as a hack. **Half-fixed:** `oath ls` now prints each
  function's signature (`reverse … :: (-> (List a) (List a))`), so
  *"what does this corpus hold at this shape?"* is answerable with
  `oath ls | grep '(-> (List'` — a grep, not a probe. A dedicated `find --shape`
  query is the part still missing.
- **The probe answered a question that had not been asked.** It reported
  `str-join : (-> Int (List Str) Str)` — the delimiter is an `Int` codepoint, not
  a `Str`. The consumer's mental model was wrong and the probe corrected it for
  free. That is a point in the probe's favour and an argument for making it a
  command rather than a trick.

**FIXED (the secondary half).** Two changes closed it. `oath ls` now prints each
function's signature, so the corpus is greppable by shape. And `find --shape`
(with a `find_shape` MCP tool) makes it a real query: give a signature — a
`(defn ...)` with no property and no body of its own — and get back every
definition at that shape, matched up to operand types, each with its guarantee:

```
$ oath find --shape reversal.oath      # (defn wanted [] [(xs (List Str))] (List Str))
shape query — definitions at (-> (List Str) (List Str)) (matched up to operand types, ...):
      gh-drop-final-empty  …  (-> (List Str) (List Str))   tested …
      …
  MORE GENERAL — a type parameter subsumes this shape (usable here by instantiation):
      reverse              …  (-> (List a) (List a))       PROVEN …
```

The MORE GENERAL group is the finding #3 insight made a feature: a monomorphic
shape query surfaces the polymorphic definitions that range over the type it
fixed. It is a SHAPE match, not a proof — cheap, no solver — so `explain` before
choosing, exactly as with the other discovery surfaces.

---

## 4. `--implies` cost is shape-dependent by two orders of magnitude — the HANG is FIXED

*The timings in this section are hand-measured wall-clock, not part of `run.sh`'s
asserted checks — the script uses the fast `Int`-typed query and contains no
`Str` reversal query or timing assertion. They are the one class of number in
this log the instrument does not reproduce; everything else it does.*

The reversal laws stated over `Int` prove in **1.8 s**. The *same two laws*,
stated over `Str` — which is the type the consumer actually cares about — ran for
over two minutes without completing and were killed. Nothing in the interface
distinguished the two: same modes, same flags, same-looking query.

The join query, over `(-> Int (List Str) Str)`, returns in **0.08 s**.

The underlying cost is a fact about the solver over an inductive `Str`, and it is
not something a discovery layer can make cheap. What WAS a defect is that the
search **appeared to hang** and could only be ended by `Ctrl-C`, with no signal
and no partial answer. Both halves are now fixed:

- **Progress.** On a terminal, `--implies` streams a per-candidate line
  (`proving 47 · property 1/2  reverse`), so a slow search reports movement
  instead of looking dead. It goes to stderr and only when stderr is a terminal,
  so a piped or captured run is byte-for-byte unchanged.
- **A wall-clock budget.** `oath find --implies q.oath --timeout 30s` bounds the
  whole search. When it elapses the scan stops and the report says so, in exactly
  the vocabulary this project already uses for the other unreached cases:

  ```
  SEARCH INCOMPLETE — the --timeout of 30s elapsed after 289 candidate checks.
  The candidates not yet reached are NO VERDICT (search aborted) — never refuted
  and never absent, only unexamined. Re-run without --timeout for the full answer.
  ```

  The budget is enforced where the cost lives: at candidate boundaries (the scan
  stops before starting a new candidate) and on the solver (a single z3 proof is
  capped to the remaining budget). The dominant cost is the proof, so this holds
  the whole search to roughly the budget — measured, the `Str` reversal query that
  used to hang for two-plus minutes now returns in ~4.3s under `--timeout 4s`. A
  proof cut short by the cap is an environmental abort — NO VERDICT, never a false
  negative — and `find` records nothing to the store, so no verdict, identity, or
  reproducibility depends on wall-clock. The unbounded default (budget 0) leaves
  the cap at the host safety net and is byte-identical to before, which is what
  conformance runs.

So the consumer's reasonable instinct — state the law at the type you will use it
at — is still the expensive one, but it now fails LEGIBLY (bounded, with a
reported NO VERDICT) instead of by appearing to hang.

Mitigation found and used: state the query at the type the corpus's own laws use
(`Int` here) to DISCOVER the candidate cheaply. This is sound **as discovery** —
`reverse` is one content-addressed object whatever type you query at, so an `Int`
query finds the same `#7bb6285884d0` the CLI calls at `Str`. What it does **not**
do is prove `reverse`'s laws hold at `Str`: `reverse` is proven at `List Int`
only (its own properties are `Int`-stated, `examples/list.oath`), it is **not**
discharged parametrically, and this project's own §11.2 of
`docs/experiments/issue-68.md` records that licensing another instantiation from
an `Int`-only proof is unsound absent a parametric argument. So the trick buys a
cheap *find*, not a *proof at your type* — and the gap is real: the consumer
closes it only for the argument-list lengths its own three laws enumerate (0, 1,
2, proven at `Str` by unfolding), and nothing here proves the reversal correct
for an arbitrary-length `Str` argument list. A consumer has to know both halves.

---

## 5. Two verdict classes never appeared, and saying why is part of the result

`--implies --details` promises four outcomes. The real searches produced two.

**No `NO VERDICT`, in either search.** Both queries landed on bodies inside the
provable fragment. `docs/experiments/issue-177-fragment.md` measures roughly 6% of
this corpus's candidates as fragment-blind; this consumer's two needs did not hit
any of them. That is a fact about which definitions these queries reached, not
evidence that the class is rare — and `run.sh` asserts the absence rather than
leaving it unstated, so a future corpus that does produce one will fail the check
loudly instead of silently changing the story.

**No cross-type hit, and the two queries fail to produce one for *different*
reasons.** This is worth separating, because one is decidable and the other is a
corpus fact:

| query | signature | why no cross-type |
|---|---|---|
| reversal | `(-> (List a) (List a))` | **Impossible.** The re-typing substitution (`crossTypeSub`, api.go) is keyed on *primitive* kinds — `int`/`rat`/`float`/`bool`. This signature contains no primitive leaf at all; `Str` and `List` are datatypes. There is nothing to substitute. |
| joining | `(-> Int (List Str) Str)` | **Possible, absent.** The delimiter IS a primitive leaf, so a `(-> Rat (List Str) Str)` or `(-> Bool (List Str) Str)` definition would be admitted and the query re-typed to it. This corpus holds no such definition. |

I tried to exhibit the joining case directly by writing the query with a `Rat`
delimiter. It does not typecheck: `SCons` pins the delimiter to `Int`, so the law
cannot even be *stated* at another numeric type. Constructing a cross-type
demonstration would have meant inventing a definition for the purpose, which
would demonstrate the feature and nothing about this consumer.

So the honest report is: **a `(List Str) -> Str` CLI's needs have no cross-type
surface to exercise in this corpus**, half by construction and half by absence,
and `run.sh` checks for the label rather than claiming none could appear.

---

## 6. Corpus-wide `--equiv` finds nothing — and that is a statement about the corpus

Sweeping `find --equiv` over **every one of the 238 function names** in the corpus
(237 distinct objects; see the pinning note at the top) returns **zero** definitions with an equivalent
partner. Not a nontrivial one; not a commutativity or associativity one either.

Zero is also what a broken sweep prints, so the run puts five throwaway
definitions in afterwards and asserts what happens to them:

| control | expected | result |
|---|---|---|
| `(str-join (+ 44 0) ...)` vs the CLI | connected — additive identity | connected |
| `(str-join (* 44 1) ...)` vs the CLI | connected — multiplicative identity | connected |
| `(str-join 45 ...)` vs the CLI | **not** connected — different function | correctly refused |
| `(* a (+ b c))` vs `(* a b) + (* a c)` | connected — distributivity | connected |

The rules fire, including two beyond commutativity and associativity, and the
negative control is refused. **So the zero is a gap in the CORPUS, not a limit of
the layer.** A curated corpus contains no redundant definitions, which is exactly
what you would hope and exactly what makes `--equiv` unexercised here.

Two limits of the sweep, stated so the zero is not overread:

- **It is name-addressed.** `find --equiv` takes a name, and the store holds 342
  objects against 237 live function objects — roughly 90 superseded objects that
  no live name reaches are outside the sweep *and* outside the tool. The claim is
  about what the corpus **offers**, not about every object it has ever held.
- **It takes an implementation, not a law.** By the time you can write the body,
  you have solved most of what you were searching for. It is a fallback, and this
  exercise never had cause to reach for it as a discovery route.

**One unexplained observation, recorded because it was measured and not chased:**
the distributivity rule connected `(* a (+ b c))` to `(+ (* a b) (* a c))` with
*variable* operands, but did **not** connect the same two forms with *literal*
operands — `(* 4 (+ 10 1))` against `(+ (* 4 10) (* 4 1))`, which the elaborator
stores unfolded. Whether that is a deliberate restriction, a budget, or a gap in
the factoring pass was not established. It is a question, not a defect claim.

---

## 7. A dependency arrived through a LAW, not through the body

`app.oath` calls exactly two definitions. `oath explain` reports five
dependencies, and one of them — `str-append #7d158d0455d3` — is there only
because the CLI's third property is stated in terms of it.

Nothing is wrong with this, and the resolution is exact. But it means **the set of
things a consumer must discover is not the set of things it calls.** Writing the
specification pulled in a third definition that no discovery query had been aimed
at, resolved silently against an ambient name, with all of §2's exposure and none
of §2's deliberation. Property-side dependencies deserve the same scrutiny as
body-side ones and are much easier to acquire by accident.

---

## 8. Smaller things, mostly good

- **Bodyless spec queries work and matter.** `--spec` on a query with no body is
  the right default — you are querying *because* you have no implementation. When
  the return type matches no parameter the kernel cannot synthesize a
  placeholder, and the error says exactly that: *"needs an explicit body: no
  parameter has the return type to reuse as a placeholder — write any well-typed
  expression of the return type."* One of the better error messages in the
  toolchain; the fix took ten seconds.
- **`oath run` is the right first-class citizen.** Being able to execute a
  `(List Str) -> Str` program with no toolchain made the whole run cheap, and the
  compiled binary's stdout is byte-identical to the interpreter's — asserted with
  `cmp`, not with `$(...)`, which strips trailing newlines and would have passed
  regardless.
- **`--details` is not optional.** Without it the countermodels are counts. The
  single most useful line in the entire session was
  `bad-reverse  countermodel (by evaluation): (Cons -16 (Cons -13 (Cons 12 Nil))), (Cons 0 Nil)`,
  and it is behind a flag.

---

## 9. What worked well

Stated plainly, because a friction log that only lists friction misrepresents the
experience. This consumer was built in one session and nothing about the
substrate had to be worked around.

- **Refutations are findings, and they are actionable.** The commons did not just
  fail to recommend `bad-reverse`; it **disproved** it, on a concrete pair of
  lists, in under two seconds. That is a different and better product than a
  ranked search result.
- **The second law did the whole job.** Involution alone does not pin reversal —
  the identity function is an involution, and `--implies` shows `bad-reverse`
  *satisfying* it. Antidistribution separates them. The lesson generalizes: one
  law is a filter, two laws are a specification, and the tool made the difference
  visible rather than leaving it to taste.
- **The signature fallback is the best-designed part of `--spec`.** A miss that
  returns a map of the neighbourhood, with guarantees attached, is far more useful
  than a miss that returns nothing — and it is what made both needs findable.
- **Dependency evidence is hash-exact and checkable.** `oath explain` gave short
  hashes that `run.sh` compares against `names.json` at HEAD. The evidence is
  weaker than an import (§2) but it is real, and it is more than most ecosystems
  offer.
- **The consumer is itself PROVEN.** Three properties, `direct (lemma-free)`, in
  0.06 s, on top of two proven dependencies. Proof-carrying composition is not
  aspirational here; it is the default and it is fast.
- **Nothing had to be copied.** The original goal — reuse the store's objects
  rather than duplicate their bodies — was met, and is asserted by hash.

---

## Not exercised

Named so the log is not read as covering them:

- **MCP surfaces.** `find`, `find_spec`, `find_implies` and `find_equiv` are
  exposed over MCP and agents are the intended consumers. This exercise used the
  CLI throughout and says nothing about the MCP path.
- **The live registry.** Everything ran against throwaway stores extracted from
  `HEAD:codebase`. No name was published anywhere, and the committed store and
  fixtures are asserted byte-identical before and after the run.
- **`--implies` at scale.** The largest search admitted a handful of candidates.
  Nothing here speaks to how the report reads when a registry buries four hits
  under two hundred unsettled candidates.

---

## Reproducing

```console
$ docs/experiments/discovery-consumer/run.sh
...
ALL CHECKS PASSED — 47 checks, 0 failures
```

Needs Go on `PATH` (it builds the kernel and compiles the consumer with the Go
backend) and `z3`. It extracts its stores from `HEAD:codebase` rather than from
the worktree, so it reports on the committed corpus even on a dirty tree, and it
re-hashes `codebase/` and `fixtures/` at the end to prove it changed neither.
