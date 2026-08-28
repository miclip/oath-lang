# Tutorial: finding proven code without trusting a name

Suppose you need a commutative binary operation. On GitHub you'd search text,
find a thousand `add`s, and audit one on faith. Here you *ask the commons what it
can prove*, and get back verified implementations — matched by what they **do**,
not what they're **called**. This walks through all four ways to ask.

The setup is just the repo's committed store, which already contains `rat-add`
and `rat-mul`, both **proven** commutative. Nothing below trusts a name.

## 1. By example — "who shares a law with this?"

Point at a definition; get every other one that satisfies the same property,
matched by the property's content hash.

```console
$ oath find rat-add
  · commutes [proven here]  #f230af55f94f
      rat-mul   (proven as "commutes")  ← proven on both: interchangeable for this law
  · assoc [proven here]  #59b248e21d01
      (no definition in the store satisfies this)
```

`rat-mul` came back with **no name trusted** — only because its commutativity
property *hashes to the same value* as `rat-add`'s. Properties are
content-addressed the same way code is: the function is `self` and the binders
are de Bruijn, so "commutativity" has one canonical hash wherever it appears.

## 2. By a fresh spec — "who proved *this*?"

You usually don't have an example — you have a spec. Write it as a `(defn ...)`
whose property is the query (`self` is the sought function). A spec query needs no
body — you are querying BECAUSE you have no implementation — so write just the
signature and the property:

```console
$ cat commutative.oath
(defn wanted [] [(a Int) (b Int)] Int
  (prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))

$ oath find --spec commutative.oath
spec query "wanted" — which proven definitions satisfy it (by content hash, no name, no example):
  · commutative [tested here]  #f230af55f94f
      rat-add   (proven as "commutes")  ← a proven implementation of this spec
      rat-mul   (proven as "commutes")  ← a proven implementation of this spec
```

Note the types: your spec is over `Int`, the implementations are over `Rat`, and
they still match. Discovery is **up to operand types** — a law polymorphic in its
type matches across the types it ranges over (the numeric tower earned that).

## 3. The syntactic wall — and proving through it

Content-hashing is fast but syntactic. Write the *same* law a different way —
operands of `==` flipped — and the hash surface misses it:

```console
$ cat flipped.oath
(defn wanted [] [(a Rat) (b Rat)] Rat
  (prop flipped [(a Rat) (b Rat)] (== (wanted b a) (wanted a b))))

$ oath find --spec flipped.oath
  · flipped [tested here]  #7ab35f3163c4
      (no definition in the store satisfies this)
```

Nothing — the AST differs, so the hash differs. For the cases the hash surface
misses, ask the *prover*: `find --implies` appends your spec to each
signature-compatible definition and proves it (via Z3), so it finds anything
that **provably satisfies** the spec, however that definition wrote its own. A
candidate whose signature matches only *up to operand types* is admitted too,
with your whole query property re-typed to it — binders and body-embedded types
alike — and the hit says which signature it was proved at:

```console
$ oath find --implies flipped.oath
spec query "wanted" — which definitions PROVABLY satisfy it (proof-implication, not shape match):

  · flipped
      apply2             ← provably satisfies it at (-> Int Int Int) (direct (lemma-free), cross-type: query property re-typed)
      max2               ← provably satisfies it at (-> Int Int Int) (direct (lemma-free), cross-type: query property re-typed)
      rat-add            ← provably satisfies it (direct (lemma-free))
      rat-mul            ← provably satisfies it (direct (lemma-free))
      4 REFUTED — proved NOT to satisfy it (a countermodel exists)
      1 NO VERDICT — the prover did not settle it (a limit of this prover, NOT a fact about the definition)
```

Now they're found. Each of these operations is commutative in its own domain —
addition and multiplication over the rationals, addition and maximum over the
integers — and Z3 knows all four, so the flipped statement discharges directly
in every case. Syntactic when it can, semantic when it must.

Look at the two labelled hits. Your spec is over `Rat`; `apply2` and `max2` are
over `Int`, so they were proved with your property re-typed to *their* signature
— that is what the `at (-> Int Int Int)` and the `cross-type` label report.
`max2` is the one worth pausing on: it is `(if (< a b) b a)`, and it states no
commutativity law anywhere. Nothing about it *says* commutative. It came back
because the prover was asked, and maximum genuinely is.

### The last two lines are not a residue

Nine definitions were signature-compatible with that query. Four proved, and the
other five are the two counted lines — which are **not one thing**, and the
difference is the whole reason they are counted separately.

**A refutation is a finding.** Four definitions were proved *not* to satisfy your
spec, each with a concrete countermodel. That is something established about the
commons, in the same currency as a hit: you now know those four are the wrong
tool for this job, and you know why. It is not "the search came up short".

**A no-verdict is a fact about the tool.** One candidate was neither proved nor
refuted. Nothing follows about it — in particular, *not* that it fails your spec.
Reporting it beside the refutations, under a different label, is the point: "no
proof" is not "disproof", and a report that summed them would be stating a limit
of this prover as a property of somebody's code.

`--details` names both groups and shows the evidence:

```console
$ oath find --implies flipped.oath --details
spec query "wanted" — which definitions PROVABLY satisfy it (proof-implication, not shape match):

  · flipped
      apply2             ← provably satisfies it at (-> Int Int Int) (direct (lemma-free), cross-type: query property re-typed)
      max2               ← provably satisfies it at (-> Int Int Int) (direct (lemma-free), cross-type: query property re-typed)
      rat-add            ← provably satisfies it (direct (lemma-free))
      rat-mul            ← provably satisfies it (direct (lemma-free))
      4 REFUTED — proved NOT to satisfy it (a countermodel exists)
          e-div              countermodel (by evaluation): -16, 11 at (-> Int Int Int)
          e-mod              countermodel (by evaluation): 2, 0 at (-> Int Int Int)
          pow                countermodel (by evaluation): 2, 0 at (-> Int Int Int)
          rat-recover        countermodel (by evaluation): 4/5, -2
      1 NO VERDICT — the prover did not settle it (a limit of this prover, NOT a fact about the definition)
          spin-partial       apply2 must be fully applied to inline at (-> Int Int Int)
```

Every countermodel is a pair you can check yourself, because it is exactly the
environment the goal was falsified in:

```console
$ oath eval '(pow 2 0)'          # → 1 : Int
$ oath eval '(pow 0 2)'          # → 0 : Int
```

`(pow 2 0)` is 1 and `(pow 0 2)` is 0, so exponentiation is emphatically not
commutative and `2, 0` is the proof. `rat-recover` is the interesting one: it is
a **fully proven** definition whose one law is `(== (rat-recover a b) a)` — it is
proven to be a *projection*, and a projection is about as far from commutative as
a two-argument function gets. Proven does not mean "proven to be what you wanted";
this report tells you which.

The countermodels above all say **by evaluation**. Oath ran the goal on concrete
values before calling Z3, and a goal that evaluates to `false` under some
environment *is* false — evaluation is the reference semantics, so no proof of it
can exist. When the sampled values do not falsify a goal, it goes to the solver,
and a countermodel the solver finds is labelled `(solver)` instead.

Now the last line, and read it precisely. `spin-partial` did **not** fail. Its
body returns `(apply2 x)` — a *partially applied* function — and partial
application is outside the fragment this prover translates to SMT, so the goal
could never be handed to Z3 at all. Note what the message does not say: it says
nothing about whether `spin-partial` is commutative. It reports where the
instrument stopped. A better prover would move that line; nothing about
`spin-partial` would have changed.

Summary counts are the default because the answer is what *proved*, and on a
large registry naming every miss would bury it. Reach for `--details` when the
misses are what you are actually asking about — "why didn't it find X?" is a
question the counts alone cannot answer.

## 4. The e-graph — "which of these are the same function?"

The deepest question is body-equivalence: two *different implementations* that
compute the same thing. Put a few, deliberately varied:

```console
$ oath put example.oath   # four little sums:
(defn sum-ab [] [(a Int) (b Int)] Int (+ a b))
(defn sum-ba [] [(a Int) (b Int)] Int (+ b a))
(defn sum3-l [] [(a Int) (b Int) (c Int)] Int (+ (+ a b) c))
(defn sum3-r [] [(a Int) (b Int) (c Int)] Int (+ a (+ b c)))
```

`sum-ab` and `sum-ba` have **different identities** (`#6c07…` vs `#7a82…`) — they
are genuinely two objects. But they're the same function, and `find --equiv`
says so, by normalizing each body to a canonical form and comparing:

```console
$ oath find --equiv sum-ab
definitions equivalent to sum-ab (#6c0735984f65) — same function up to the rewrite rules, distinct identities:
  eHash 79de5037be8b
      sum-ba   #7a822fd6f5ca

$ oath find --equiv sum3-l          # associativity, too
  eHash 7a63c2aa3e19
      sum3-r   #09741761a5b5
```

The rules are commutativity and **type-directed associativity** — and the
type-direction is the interesting part. Over `Int`, `(+ (+ a b) c)` and
`(+ a (+ b c))` collapse. Over `Float` they would **not**, because float addition
isn't associative — the very law `examples/float.oath` falsifies. The e-graph
only applies a rewrite where it's sound, and it learned where that is straight
from the numeric tower.

## The two things to take away

**No name was ever trusted.** Every match above is by content hash, by proof, or
by canonical form — never by a label anyone can repoint. That's what makes a
shared, deduplicated commons possible instead of ten thousand siloed copies.

**Identity was never touched.** `sum-ab` and `sum-ba` stay two distinct objects
with two distinct hashes; the e-graph draws an *equivalence edge* over the hash
graph, it never merges them. Discovery is a view over the substrate, never a
change to it — which is exactly why it can keep getting smarter (deeper rules,
more of the ladder) without ever forking reality. See
[docs/discovery.md](../discovery.md) and [docs/egraph.md](../egraph.md).
