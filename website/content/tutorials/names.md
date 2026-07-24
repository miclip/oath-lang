# Tutorial: names aren't identity

In most systems a name is load-bearing: repoint what `validate` means and code
that calls it silently changes — the root of dependency-confusion attacks and
"someone republished the package" breakage. Oath is content-addressed, so a name
is just a mutable label over an immutable object, and this walk shows why that
means a name collision can never silently change what your code computes.

## A definition *is* its hash

`put` a definition and you get back a hash — the SHA-256 of its canonical AST.
That hash is its identity; the name is metadata pointing at it.

```console
$ oath put '(defn base [] [] Int 1)'
✓ base   #09e3b06fdbfa  asserted · total

$ oath put '(defn uses-base [] [] Int (+ base 1))'
✓ uses-base   #8a94fa733946  asserted · total

$ oath eval '(uses-base)'
2 : Int
```

Crucially, when `uses-base` was elaborated, the name `base` was resolved *once* to
its hash and that **hash** was frozen into `uses-base`'s AST. `uses-base` doesn't
depend on the *label* `base`; it depends on the *object* `#09e3b0…`.

## Repointing a name can't reach into existing code

Now redefine `base` to something completely different:

```console
$ oath put '(defn base [] [] Int 100)'
✓ base   #1332710abd30  asserted · total  (name repointed; old version 09e3b06fdbfa remains immutable)

$ oath eval '(uses-base)'
2 : Int
```

`uses-base` still computes **2**, not 101. The label `base` now points at a new
object (`#1332…`), and the old `#09e3b0…` is still there, permanent and immutable —
but `uses-base` was pinned to it by hash, so moving the label couldn't touch it.
No spooky action at a distance; no dependency silently changing under you.

Names *do* matter at exactly one moment — `put` time, when a name gets resolved to
a hash. To adopt the new `base`, you'd **re-put** `uses-base`, which re-resolves
the name and produces a *new* `uses-base` (a new hash) computing 101. Upgrading is
an explicit, content-addressed act, never an accident.

## What this buys you

- **Name collisions can't corrupt.** Two teams can both define `validate`; they're
  two different objects with two different hashes. The label resolves to one; the
  other still exists and everything that referenced it is unaffected. The worst a
  collision does is make you say *which* one you meant — a naming inconvenience,
  not a correctness hazard. (Namespace with a convention like `team-a/validate` to
  keep both labels around.)
- **Identical code deduplicates.** Two people who write the byte-identical
  definition land on the *same* hash — one object, several names. No 10,000 copies
  of the same function.
- **Publishing needs no trusted server.** `oath import` refuses any byte that
  doesn't hash to its claimed name and re-verifies locally, so a registry is just
  a directory of bundles; trust lives in the importer, not the host.

## The one-liner

**Humans navigate by names; the machine reasons by hashes; correctness only ever
depends on the hashes.** That's the substrate move everything else is built on —
including [discovery](discovery.md), where you find proven code by *what it does*
precisely because names were never authoritative to begin with.
