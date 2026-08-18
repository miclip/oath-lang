# Discovery: finding proven code by what it does, not what it's called

**Status:** first rung shipped (2026-07) — `oath find`; the rest is roadmap.
This is the layer that turns the content-addressed store from "a place your
code lives" into "a commons you can *draw from*" — you find an existing proven
definition by the **property it satisfies**, with no name trusted.

## Why this exists

Every other discovery path is name-keyed: `ls`, `get`, `dependents`, `context`
all take a name. But names are the one mutable, non-authoritative, collision-
prone layer (`docs/teamstore.md`, and the naming discussion generally). So
"reuse the proven `validate`" secretly means "reuse whatever *my store's* label
`validate` points at" — which is exactly the trust we removed everywhere else.
Discovery needs to key on *meaning*, not on a label.

## The key realization: properties are content-addressed too

A property is stored as `(binders, body)`, where the function under proof is
`self` (not a ref) and the binders are de Bruijn indices. So a **pure algebraic
law carries no names and no specific hashes** — commutativity is literally
`(== (self a b) (self b a))` — and its canonical encoding has **one hash**
wherever it appears, on any definition, of the matching operand types.

That means "which proven definitions satisfy this spec?" is a **hash lookup,
not a search**. `propHash` (canon.go) is the content address of a property;
`oath find` indexes it. Content-addressing, which already gave code a
name-independent identity, gives *specs* one too.

## The three-rung ladder

Discovery by meaning has three strengths of "the same," each looser than the
last and each further from name-dependence:

1. **Syntactic** — identical AST → same code hash. (Already how identity works.)
2. **Spec-equivalent** — different implementations that satisfy the **same
   property**. *This is the rung `oath find` implements*, and it was already
   latent in the proofs we store — it just had no query surface. If two defs
   share a law and both **prove** it, they are interchangeable *for that law*.
3. **Rewriting-equivalent (e-graph)** — different *bodies* that are provably
   equal under a rule set, collapsed to one canonical form. The deepest dedup;
   the biggest build; roadmap (DESIGN.md flags egg/e-graphs). Its hard
   constraint is below.

## `oath find` (rung 2, today)

Two front doors, both matching on the generalized property content hash
(`propHashGeneral` — matched *up to operand types*):

**Query by example** — point at a def whose property you want:
```
$ oath find rat-add
  · commutes [proven here]  #f230af55f94f
      rat-mul   (proven as "commutes")  ← proven on both: interchangeable for this law
  · assoc [proven here]  #59b248e21d01
      (no definition in the store satisfies this)
```

**Query by fresh spec** — write the property you want; the sought function is
`self`, the body is any trivial placeholder of the right type:
```
$ cat spec.oath
(defn wanted [] [(a Int) (b Int)] Int (+ a b)
  (prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))
$ oath find --spec spec.oath
spec query "wanted" — which proven definitions satisfy it (by content hash, no name, no example):
  · commutative [tested here]  #f230af55f94f
      rat-add   (proven as "commutes")  ← a proven implementation of this spec
      rat-mul   (proven as "commutes")  ← a proven implementation of this spec
```

Both find `rat-add`/`rat-mul` with **no name trusted** and (here) across types
— an `Int` spec matched the `Rat` implementations. Both are exposed over MCP
(`find` and `find_spec`), since agents are the intended consumers: generate a
spec, ask the commons who already proved it, reuse instead of rebuild.

## The invariant that protects the substrate

**The discovery layer never touches identity.** Code hashes and prop hashes are
*syntactic* and stay that way; `find` (and, later, the e-graph) only draw
*edges* between existing hashes — "these satisfy the same law," "these bodies
rewrite-equal." They never merge two objects into one identity or redefine a
hash. Identity is the O1 encoding (SPEC §1) and remains so. Semantics is a
*view over* the hash graph, never a *change to* it. This is what keeps the
e-graph from destabilizing the foundation when it lands.

## Honest limits (and the roadmap they imply)

`oath find` matches a property up to its operand types (`propHashGeneral`
generalizes the primitive leaf types in the binders to positional type
variables, so commutativity over `Int` and over `Rat` both become `[t0, t0]`
and match). What remains:

- **Body-embedded types aren't generalized yet.** Generalization today covers
  binder types, which is complete for the pure algebraic laws whose bodies
  carry no types (commutativity, associativity, idempotence). A law whose *body*
  mentions a type — a generic callee's type arguments, a `(Nil [Int])` in the
  statement — still matches only same-type (it's safe, never a false match,
  just not yet cross-type). *Next:* thread the same type-generalization through
  the body's `ctor`/`ref`/`self` type arguments.
- **Query by fresh spec** is now supported alongside query-by-example:
  `oath find --spec <file>` (and the `find_spec` MCP tool) elaborate a `(defn
  ...)` whose `(prop ...)` clauses are the query — the sought function is
  `self`, the body is any trivial well-typed placeholder — and return every
  proven definition that satisfies them. This is "I have a spec; who has proven
  an implementation?", the core commons interaction, with no name and no example
  needed. The dummy body is the one wart (you must write a well-typed
  placeholder to give `self` a signature); auto-synthesizing an inhabitant of
  the return type would remove it.
- **Proof-implication** is now available alongside the content-hash surface:
  `oath find --implies <file>` (and the `find_implies` MCP tool) finds every
  definition that PROVABLY satisfies the spec — via Z3, not by shape. Because a
  property is `self`-referential and de Bruijn it is *portable*: for each
  candidate definition, the query property is appended and proved (reusing
  the whole prover — the definition's body and its own proven properties as
  lemmas). This catches semantic matches the hash surface misses — commutativity
  written `(== (self b a) (self a b))` has a different AST from the usual form,
  so `find --spec` misses it, but `find --implies` proves `+` satisfies it. The
  win is finding defs that satisfy your spec *however they wrote their own*.

  Candidates are **signature-compatible, not same-signature**: a definition
  whose signature equals the query's *up to primitive leaves* is admitted with
  the query's binders re-typed to it, so an `Int` law reaches its `Rat`, `Float`
  and `Bool` counterparts and the hit reports the signature it was proved at.
  Admission is deliberately wider than truth — `(Int,Int)->Int` and
  `(Bool,Bool)->Bool` generalize alike — because the PROOF is the filter, not
  the signature. Only the property's BINDERS are re-typed; a property whose
  BODY carries its own type annotations no longer typechecks against the
  candidate and is rejected rather than approximated. Cost on the committed
  corpus, measured before this widened: half again as many candidates
  (6 -> 9), a third more solver calls (12 -> 16), and no measurable change in
  wall clock (5m44.257s -> 5m44.981s median), because the run is dominated by
  goals that never prove and the new candidates are not those.

  **Every admitted candidate is now classified and reported, not just the ones
  that proved** (#156). The prover returns four outcomes and they are kept four:

  | outcome | what it establishes | about what |
  |---|---|---|
  | `proven` | the definition satisfies the spec | the definition |
  | `REFUTED` | the definition provably does NOT satisfy it, and here is a countermodel | the definition |
  | `NO VERDICT` (unsettled) | the prover declined, or the goal was untranslatable | **this prover** |
  | `NO VERDICT` (aborted) | a strategy attempt was environmentally aborted, so no negative verdict is valid (SPEC §7.2) | **this run** |

  The split is a soundness matter rather than a presentational one. Before it,
  all three non-proven outcomes rendered as `(no definition provably satisfies
  this)` — a sentence about the WORLD, printed on the strength of a sentence
  about the TOOL. "No proof" is not "disproof"; a solver timeout is not a
  counterexample.

  **A refutation is a positive result and is presented as one.** It is a proof,
  just not the one you asked for: "this definition provably does not satisfy your
  law, and here are the values that show it". Filing that under "did not prove"
  reports a proof as an absence. Two things can produce one — the concrete
  pre-solver pass, whose falsifying environment is retained and reported `(by
  evaluation)`, and the solver, reported `(solver)`. The first is sound ahead of
  the prover for the reason the pass exists at all: evaluation is the reference
  semantics, so a goal that evaluates to `false` under a concrete environment is
  false and no valid proof of it exists.

  The no-match line now fires **only when nothing was admitted at all**, which is
  the one situation in which it is supportable. With a refutation on record the
  report has already said what was established; with an unsettled candidate on
  record it would be asserting exactly what the prover declined to decide.

  By default the proven ones are named, REFUTED ones are named with their
  countermodel, and the two unsettled classes are reported as COUNTS. The
  asymmetry is the content of calling a refutation a RESULT rather than residue:
  `2 REFUTED` is not actionable, because the finding IS which definition was
  disproved and by what — while an unresolved count IS actionable at a glance,
  since it says the answer is incomplete without needing the names. A large
  registry would bury the hits under its own unsettled candidates; it is not
  buried by its refutations, which are findings in their own right. `oath find --implies <file>
  --details` names every candidate and prints its evidence — the countermodel, or
  the prover's reason for not having one. `--details` is accepted on the
  `--implies` form only; the other three searches attempt no proof and so have no
  unproven residue to report.
- **Body-equivalence** has a first slice: `oath find --equiv <name>` (and the
  `find_equiv` MCP tool) find definitions that are the SAME FUNCTION up to the
  rewrite rules — a different implementation that normalizes to the same
  canonical form. This is the e-graph (docs/egraph.md); matched defs share an
  `eHash` but keep distinct identities. The shipped rules are:
  - commutativity (`(+ a b)` ≡ `(+ b a)`), sound for every operand type;
  - **type-directed associativity** (`(+ (+ a b) c)` ≡ `(+ a (+ b c))` over
    Int/Rat and `and`/`or` over Bool, but NOT Float, whose addition isn't
    associative);
  - **identity elements**, also type-directed: `x + 0` over Int/Rat but NOT
    Float (`-0.0 + 0.0` is `+0.0`, a distinct value), `x * 1` over Int/Rat/Float,
    `x and true`, `x or false`;
  - **Bool idempotence** (`x and x` ≡ `x`) — never `+` or `*`, which are not
    idempotent — and **`neg` involution** over Int/Rat/Float;
  - **constant-condition `if`**, identical `if` branches under a condition that
    is already a value, and **eta** over a `var` or `ref` head. These last are
    restricted by strict evaluation order rather than by type: dropping a
    divergent `if` condition would remove divergence, and eta over a computed
    head would move it.

  A full saturating engine — distributivity, e-classes, equality saturation,
  extraction — is the deeper version, and none of it is present today. Full
  extensional equivalence is undecidable, so this collapses things equal *under
  the rules*, never all equal functions.

The rungs compose: content-hash match (up to type) for the fast common case,
proof-implication for the semantic cases it misses, and the e-graph for
body-equivalence — every step loosening name-dependence further, and every one
of them drawing edges *over* the hash graph, none touching identity.

## When a query returns nothing: check the SHAPE before concluding absence

**An empty result and an empty corpus are the same output.** This bites on the
two modes that take a LAW, and it reaches them differently:

- `--implies` PREFILTERS on signature compatibility, so a definition whose
  signature does not match your query's up to primitive leaves is never
  considered, whatever its body proves.
- `--spec` applies no signature test at all — it compares generalized property
  hashes across every definition. Shape reaches it only THROUGH the property: a
  law that applies `self` embeds that application in its hash, so a
  differently-shaped query hashes differently and misses. A law that never
  mentions `self` carries no shape at all and can match a definition of any
  signature, which is worth knowing before reading a `--spec` hit as evidence
  that your shape was close.

Either way structure does not bridge, so a wrong shape and a missing artifact
look identical from outside — and the shape is the half you control. The
name-keyed modes (`find <name>` and `--equiv`) are not affected: they ask for a
name rather than a shape.

**And primitive leaves generalize LESS than the headline suggests.** `--implies`
re-types the property's BINDERS to each candidate, which is what lets an `Int`
law reach its `Rat` counterpart; `--spec` likewise generalizes only the binders
and hashes the property BODY unchanged. So a primitive written into the body —
in a type application, a constructor, an annotation — does not generalize at
all: laws that are otherwise identical but say `(Nil [Int])` and `(Nil [Rat])`
miss each other. Keep concrete types in the binders where you can.

### Start with a SIGNATURE PROBE, not with a law

This is the cheapest move available and it is worth making first.

A query's law only matches if it hash-equals a definition's own. But when it
*doesn't* match, `--spec` falls through to listing every definition whose
SIGNATURE is compatible — so a law you know will never match is a way of asking
"what does this corpus have at this shape?"

```
(defn wanted [] [(s Str)] Str s
  (prop refl [(x Str)] (== (wanted x) (wanted x))))
```

```
· refl  — no definition states this law as written
  4 definition(s) have a COMPATIBLE SIGNATURE:
    config-key         PROVEN ...
    gh-group           tested ...
    record-field       tested ...
    shout              PROVEN ...
```

Nothing about that query says what you want. It lists the corpus's definitions
at `(-> Str Str)` anyway. Probe a few shapes, read the names with `oath get`,
and only then write the real law — you will be writing it against a definition
you have seen rather than one you are guessing at.

**It is a map, not a census, in two ways.** The list is built only from
definitions that carry properties of their own, so one with no stated laws is
silently absent — an empty list is not proof that the shape is empty, and
`--implies` can still prove your query against such a body. And the list is
capped at eight names; past that it tells you how many more share the signature,
so widen your probe rather than reading eight as all of them.

**One caveat on the probe, and it is not fatal.** The reflexive law is only a
probe because nothing in the corpus states it. If some definition ever did,
`--spec` would report that content-hash match and print no neighbour list — you
would get a hit instead of a map. Nothing in this corpus states one today, and
if it happens the failure is legible rather than silent: you see a match you did
not expect.

**Two more moves in the same spirit**, both cheaper than guessing a law:

- `oath ls` lists every name with its hash, kind and guarantee — **not its
  signature**, so pair it with `oath get <name>` to see shapes. The corpus is
  small enough to read.
- `oath dependents <name>` walks from a name you found to its neighbours; a
  definition adjacent to a near-miss is often the one you want.

### The three axes, for when you have a shape and it still finds nothing

**These describe real gaps that nothing bridges. They are not, on the evidence
below, what finds artifacts** — probing and reading the corpus is. Reach for
them when a probe has told you the shape exists and your law still misses.

| axis | the guess that finds nothing | the shape the corpus used |
|---|---|---|
| **return** | the operation returns the whole collection — `(List Str)` | it returns ONE element — `Str` |
| **abstraction** | it takes a VALUE — `(x Str)` | it takes a TEST — `(p (-> a Bool))` |
| **polymorphism** | a monomorphic definition | `[a]` on the definition — the corpus's combinators are `forall a` |

**Return.** "Report a required key the host did not supply" was written to return
`(List Str)`, the missing keys. `config-missing` returns `Str`, the first one.
Both laws missed, with no signature-compatible fallback — indistinguishable from
an empty corpus. Changing only the return type, keeping both laws otherwise
identical in meaning, proved `config-missing` twice.

**Abstraction.** "Does this list of `Str` contain an element" was written as
`(-> Str (List Str) Bool)`. The corpus answers it with `any`, which is
`(-> (-> a Bool) (List a) Bool)` — the fixed value is generalized to a
*predicate*. The mismatch is an extra parameter, not a leaf type, so nothing
bridges it. Restated in the higher-order shape, `--implies` proves `any`
satisfies the law and REFUTES `all` with a countermodel.

**Polymorphism.** "Take the longest prefix whose elements pass a test" written
monomorphically finds nothing, even when the law is copied verbatim from
`take-while`'s own. Declaring the query definition `[a]` proves `take-while` AND
`filter`, and refutes `drop-while`.

**It is the DEFINITION's polymorphism that matters, not type application in the
law.** Measured as a 2x2 on one query:

| query definition | the law's recursive call | result |
|---|---|---|
| monomorphic | `(wanted p xs)` | nothing |
| `[a]` | `(wanted [Int] p xs)` | `filter`, `take-while` proved |
| `[a]` | `(wanted p xs)` | `filter`, `take-while` proved |

The application is inferred, so writing it changes nothing. Only the first row
is a different query.

### Writing a polymorphic query

A definition's type variables are **not in scope in its property binders** —
declare the definition polymorphic and state the law at a concrete type, which
is how the corpus's own polymorphic definitions state theirs:

```
(defn wanted [a] [(p (-> a Bool)) (xs (List a))] (List a) (Nil [a])
  (prop every-kept-element-passes [(p (-> Int Bool)) (xs (List Int))]
    (all [Int] p (wanted p xs))))
```

### The procedure

1. State the law in your own words. Do not try to guess how the target states
   it: `--implies` proves rather than matches, so a differently-worded law is
   fine, and `--spec` reports a content-hash match that a paraphrase will miss.
2. Run `--spec` first. It is fast, and its fallback list of
   **signature-compatible** definitions is the cheapest signal you have: if it
   names candidates, your signature is close and the law is worded differently.
   `get <name>` then shows you how they state it. (The fallback list is
   signature-derived; a `--spec` MATCH is not, so read the two differently.)
3. If it names nothing, try `--implies` before changing anything. **An empty
   fallback list does not mean no signature matched:** the neighbour list is
   built only from definitions that carry properties of their own, so a
   compatible definition with no stated laws is silently absent from it —
   and `--implies` can still prove your query against that body.
4. If that also finds nothing, vary the SHAPE. The three axes above are where to
   start; they are the ones measured, not a complete list, and exhausting them
   does not establish that the corpus has nothing.
5. On any shape that surfaced candidates, run `--implies`. It proves, so it
   finds definitions whose own laws look nothing like yours.
6. **`NO VERDICT` is not absence.** It reports a limit of the prover, not a fact
   about the definition — a body outside the provable fragment (`lam` terms,
   trusted crypto primitives) yields no goal to solve. Roughly 6% of this
   corpus's candidates are unreachable this way, measured in
   `docs/experiments/issue-177-fragment.md`, and they are not peripheral.
7. **When you see one, re-run with `--details`.** It NAMES each unsettled
   candidate and says why:

   ```
   1 NO VERDICT — the prover did not settle it (a limit of this prover, ...)
       record-field       "lam" terms are outside the provable fragment
   ```

   That is a name you can `get` and read. In the falsifier's terms the artifact
   is SURFACED rather than SATISFIED — nothing was proved — but a name and a
   reason is most of what you came for, and it costs one flag.
8. Only if that leaves you with nothing, `--equiv` is the remaining route — **with two things worth
   naming.** It takes an IMPLEMENTATION where `--spec` and `--implies` take a
   LAW, so if you can already write the implementation you have solved much of
   what you were searching for; it is a fallback, not an equal fourth option.
   And it is not known to reach every fragment-blind candidate: it matches
   implementations sharing an eHash under a limited rewrite system, and
   `issue-177-fragment.md` tested 2 of the 12 and says so. Reaching those two is
   evidence that the blindness is not structural, not a guarantee of coverage.

### What this section is measured against, including where it was wrong

The three axes came from re-phrasing seven intents drawn from an application's
friction log: one author's first pass found **2 of 7**, and re-shaped queries
found 5 of 7.

**That was then tested against readers, and the axes did not survive as the
explanation.** Four subjects who had never seen the corpus were given the seven
intents; two got this section's guidance, two got only the mode list and the
query syntax. Both groups averaged **6 of 7**, and the group WITHOUT the
guidance used fewer tool calls and fewer tokens to get there.

So the honest claims are narrower than the first version of this page made:

- A capable caller reaches most of these artifacts whether or not they read
  this. The 2-of-7 baseline reflects one author's single pass, not a ceiling.
- What the subjects actually used was signature probing, `oath ls` and
  `oath dependents` — which is why those now come first. Every one of them was
  invented independently by subjects; none was in the guidance.
- The one subject that missed an artifact was the one that followed the
  law-writing procedure most faithfully and deliberately avoided reading the
  corpus's names. That is a warning about how this page was previously framed.

The axes below remain true — they describe gaps nothing bridges — but they are a
diagnostic for a query that will not land, not a search strategy.

### What this does not fix

Two of the seven stayed unfound at every shape, both for the same reason: the
target's body is outside the provable fragment, so **`--implies` returns no
verdict however the query is written.** The obstacle is the CANDIDATE's body,
not your signature, which is why varying the shape does not help.

**That is a statement about `--implies`, not about every mode — and `--spec`
reaches these more often than you would expect.** It matches property content
hashes and never invokes the prover, so it is unaffected by the fragment. The
catch is that your law has to hash-equal the target's own.

**That happens more than "coincidence" suggests.** Two readers who had never
seen this corpus were given the intents behind both fragment-blind definitions
and reached both on `--spec`, first try, writing the law from the intent
sentence alone. A property's NAME is not part of its hash, so one of them naming
its law `never-contains-the-separator` where the target names its own
`never-contains-a-tab` is evidence they wrote the SAME law independently rather
than copying it. For a sharply worded intent the obvious law is often the law
the author wrote.

So: always try `--spec`, especially after a `NO VERDICT`. It is fast, and a hit
is a real find — a definition that literally states your law.
