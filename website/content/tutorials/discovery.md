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
whose property is the query (`self` is the sought function; the body is any
trivial placeholder):

```console
$ cat commutative.oath
(defn wanted [] [(a Int) (b Int)] Int (+ a b)
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
(defn wanted [] [(a Rat) (b Rat)] Rat (+ a b)
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
with your binders re-typed to it, and the hit says which signature it was proved
at:

```console
$ oath find --implies flipped.oath
spec query "wanted" — which definitions PROVABLY satisfy it (proof-implication, not shape match):

  · flipped
      apply2             ← provably satisfies it at (-> Int Int Int) (direct (lemma-free), cross-type: query binders re-typed)
      max2               ← provably satisfies it at (-> Int Int Int) (direct (lemma-free), cross-type: query binders re-typed)
      rat-add            ← provably satisfies it (direct (lemma-free))
      rat-mul            ← provably satisfies it (direct (lemma-free))
```

Now they're found. Each of these operations is commutative in its own domain —
addition and multiplication over the rationals, addition and maximum over the
integers — and Z3 knows all four, so the flipped statement discharges directly
in every case. Syntactic when it can, semantic when it must.

Look at the two labelled hits. Your spec is over `Rat`; `apply2` and `max2` are
over `Int`, so they were proved with your binders re-typed to *their* signature
— that is what the `at (-> Int Int Int)` and the `cross-type` label report.
`max2` is the one worth pausing on: it is `(if (< a b) b a)`, and it states no
commutativity law anywhere. Nothing about it *says* commutative. It came back
because the prover was asked, and maximum genuinely is.

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
