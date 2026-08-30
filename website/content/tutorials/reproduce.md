# Tutorial: reproduce a verified program on a fresh machine

A content-addressed codebase makes a promise the file world cannot: given the
identity of a program and somewhere to fetch its objects, any machine can
reconstruct it **exactly** — and, because Oath definitions carry their proofs,
reconstruct it *verified*, not merely copied. This tutorial walks the whole
reproduction path end to end: a publisher resolves a program to a lockfile, and a
fresh machine — a CI runner, a container, a reviewer's laptop — clones it into a
store it can build from. The fresh machine holds the program's **source and its
lock** (its "project", the way a checkout holds source and a lockfile) and needs no
pre-installed dependencies: it fetches the dependency closure from a store or a URL,
checks every object against its hash and types, and re-runs the program's own
property checks locally.

Three commands do the work, smallest to largest:

- `oath resolve` writes a **lockfile**: the program's external dependencies, pinned
  by hash, plus their full transitive object closure.
- `oath hydrate` populates a store to match a lock — it *fetches* the closure the
  lock only *identifies*.
- `oath clone` is the one-shot: hydrate the closure **and** admit-and-verify the
  program, so `oath build` then works.

## The program (the publisher)

Here is a small program — it numbers its arguments as a checklist. Save it as
`checklist.oath`:

```lisp
(defn numbered [] [(n Int) (items (List Str))] Str
  (match items
    ((Nil) "")
    ((Cons h t)
      (str-append (show-nat n)
        (str-append ". " (str-append h (str-append "\n" (numbered (+ n 1) t)))))))
  (prop empty-is-empty [(n Int)] (== (numbered n (Nil [Str])) "")))

(defn checklist [] [(args (List Str))] Str
  (numbered 1 args)
  (prop single [(s Str)]
    (== (checklist (Cons [Str] s (Nil [Str]))) (str-append "1. " (str-append s "\n")))))
```

It depends on two definitions from the standard library — `show-nat` and
`str-append` — plus the `List` and `Str` datatypes. The publisher works from a
store that has them. If you are in the Oath repo, seed a throwaway one from the
committed store (in practice the standard library lives in a registry you resolve
against; this keeps the tutorial self-contained and never writes the tracked
corpus):

```console
$ pub=$(mktemp -d); export OATH_STORE="$pub"
$ git archive HEAD:codebase | tar -x -C "$pub"
```

(`$pub` names the publisher store; we reuse it when the fresh machine clones from it,
after the fresh machine has switched `OATH_STORE` to its own directory.) Put the
program, and it is verified on the way in:

```console
$ oath put checklist.oath --new
✓ numbered         #289f9790c2ff  tested (200 cases per property) · total
    prop empty-is-empty           passed 200 cases
✓ checklist        #97fbbc9335c3  tested (200 cases per property) · total
    prop single                   passed 200 cases
```

Now resolve it to a lockfile. Resolving does **not** touch identity — it records
the intention, which external names the program uses and at which hashes:

```console
$ oath resolve checklist.oath -o checklist.oath.lock
resolved 4 external name(s) across 5 object(s) in the closure -> checklist.oath.lock
```

```json
{
  "format": "oath-lock/1",
  "file": "checklist.oath",
  "dependencies": {
    "List": "fa452d59a2358fadba69764616745354a1e34465079c2a63e5f4c5d56f2baf05",
    "Str": "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
    "show-nat": "1d0a9ecfb0eab9baa4782839fac41b93f9490d6ac98cc04d3001555cc97e108c",
    "str-append": "7d158d0455d341a46cfcc678cfd41d35db89b574dc45158746ad922b8874e3a4"
  },
  "closure": [
    "1d0a9ecfb0eab9baa4782839fac41b93f9490d6ac98cc04d3001555cc97e108c",
    "30df3863fbb189976a0d624ec7697a62cfaf940554ae436027eea19c4cdfbc5e",
    "7d158d0455d341a46cfcc678cfd41d35db89b574dc45158746ad922b8874e3a4",
    "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
    "fa452d59a2358fadba69764616745354a1e34465079c2a63e5f4c5d56f2baf05"
  ]
}
```

`dependencies` names what the program directly references; `closure` is every
object needed to typecheck them — a **sorted list of hashes, not object bodies**.
That distinction is the whole point of the next step: the lock *identifies* the
rebuild without *containing* it.

## The fresh machine

The fresh machine has the program source and its lock, and an **empty** store of
its own:

```console
$ export OATH_STORE=$(mktemp -d)   # a fresh, empty store
```

The obvious command fails — the lock says what the program needs, not the objects
themselves:

```console
$ oath put --lock checklist.oath.lock checklist.oath --new
error: the store cannot resolve this file's dependencies: line 1: unknown type "Str"
```

`oath clone` closes that gap in one command. Point it at the publisher's store as
the object source:

```console
$ oath clone checklist.oath --lock checklist.oath.lock --from "$pub"
cloned 5 dependency object(s) from the source; admitted checklist.oath:
✓ numbered         #289f9790c2ff  tested (200 cases per property) · total
    prop empty-is-empty           passed 200 cases
✓ checklist        #97fbbc9335c3  tested (200 cases per property) · total
    prop single                   passed 200 cases
```

Read what that verdict says. The dependency closure arrived — each object checked
against its hash and typechecked; the program's own properties **ran again** on the
fresh machine (here as tests, 200 cases each — the "tested" verdict); and `checklist`
hashes to `#97fbbc9335c3`, *the same hash the publisher saw*. Identity was reproduced,
not asserted. (The dependencies' verdicts are the publisher's recorded metadata,
carried with each object; a consumer who wants them re-earned locally runs `oath
prove`, which the guarantee ladder never imports.) Now build and run it:

```console
$ oath build checklist -o ./checklist
built checklist → ./checklist  (entry checklist : (-> (List Str) Str), guarantee: tested (200 cases per property), backend: go-emit/2)
$ ./checklist milk eggs bread
1. milk
2. eggs
3. bread
```

## Over the wire, with no key

`--from` used a local store; a real fresh machine fetches from a **registry**. An
Oath registry is public and re-verifiable, so reads need no identity — but a
server exposes them only when its operator opts in. The publisher serves its store
(`$pub` from earlier) with `--public-reads`, in a terminal of its own:

```console
$ OATH_STORE="$pub" oath serve --http 127.0.0.1:8080 --public-reads
oath team store: http://127.0.0.1:8080/mcp (writes signed; PUBLIC reads (anonymous, read-only); ...)
```

Now the fresh machine — a new, empty store again — clones with `--remote` and **no
`--key`**:

```console
$ export OATH_STORE=$(mktemp -d)   # a fresh, empty store (clone refuses a non-empty one)
$ oath clone checklist.oath --lock checklist.oath.lock --remote http://127.0.0.1:8080
✓ checklist        #97fbbc9335c3  tested (200 cases per property) · total
    prop single                   passed 200 cases
$ oath build checklist -o ./checklist && ./checklist buy-milk call-alice
1. buy-milk
2. call-alice
```

Same hash, same guarantees, and the only thing fetched over the network was the
dependency closure — the source and lock were already in hand, the deps came from
the URL, no key required. Two properties are worth stating plainly:

- **Reads are anonymous; writes are not.** `--public-reads` widens *reads* only.
  An unsigned `put` against the same server is still refused — publishing carries
  unforgeable authorship, and that gate is untouched. A registry started *without*
  `--public-reads` answers a keyless read with a 401 and a hint to pass `--key`.
- **The clone is checked and transactional.** `clone` re-runs the program's own
  property checks on the fresh machine (as tests here; proofs are re-earned
  separately with `oath prove`), and a program that fails them never lands. It
  materializes a **fresh** store (it refuses a non-empty or policy-governed
  target), and it does the whole admission in a scratch, committing only once every
  definition is accepted — so a bad lock or a non-verifying program leaves the
  target untouched.

## The pieces underneath

`clone` is a convenience over two primitives you can run yourself:

- `oath hydrate checklist.oath.lock --from <store>` (or `--remote <url>`) populates
  a store with **just the dependency closure** — no program. Useful when several
  programs share a dependency set, or to inspect what a lock pulls in. After it,
  `oath build checklist` still fails: the deps are present, the program is not.
- `oath put --lock checklist.oath.lock checklist.oath` then admits the program,
  verifying that the store resolves its dependencies *exactly* as the lock pins
  them before it elaborates. `clone` is `hydrate` followed by this put, in one
  transactional step.

A note on the lock's `file` field: it records the path `resolve` was given, but
`put --lock` and `clone` take the source file as an argument, so the lock travels
independently of where the source lives. And the lock never participates in
identity — it guards which objects a build sees, and nothing about what a
definition hashes to.

## What this gives you

The file world reproduces a build by trusting a tarball and a lockfile that pins
version *strings*. Oath pins **hashes**: the fresh machine cannot be handed a
different object under the same name, because the name resolves through the hash
the lock fixed, and every object re-hashes to its own identity on arrival. And
because each object carries its verdict, "reproduced" means more than the same bytes:
the program's property checks re-run where it landed, and its dependencies' proofs
are re-earnable there with `oath prove` — never asked to be trusted from afar.

Related: [names aren't identity](names.md) for why a hash, not a name, is what a
lock pins; [the guarantee ladder](guarantee-ladder.md) for what "tested" and
"proven" verdicts mean; and [the compiled circle calculator](circle.md) for
`oath build` on its own.
