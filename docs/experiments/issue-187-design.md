# #187 design pass — module/import: resolution is TOOLING, not language

This is the design pass #187 asked for, and it begins where the issue said it must:
with the language-vs-tooling question, and a stated **no**. The conclusion is that
the current flat-namespace + content-addressed-closure design is already correct,
and the friction is removed by an authoring tool that touches neither the language,
the SPEC, nor what any `Def` hashes to. A falsifier — measurable on the committed
corpus — is stated so a later session can overturn this rather than inherit it on
faith.

Grounded in the mechanism map (file:line throughout); nothing here is argued from
taste.

## 1. The gating question, answered: TOOLING

A `Def`'s identity is name-free. At elaboration `elabName` resolves a bare
reference through `e.st.Resolve(name)` and bakes the **hash** into the term
(`&Term{K:"ref", Hash: h}`, `surface.go:707,718`); the name does not survive into
the stored object. Every "dependency list" in the system — `oath explain`'s
`DEPENDENCIES (N, exact by hash)` (`explain.go:493`), the `.oathpkg` export closure
(`registry.go:62`), the compiled artifact's embedded `ProvenanceManifest.Closure`
(`program.go:597`) — is **recomputed post-hoc** by one walker, `collectDeps`
(`ast.go:177`), over those baked-in hashes. There is no stored dependency record
because none is needed: the closure is determined by identity.

So a module system that changed what a definition hashes to would violate the
substrate's core rule and gain nothing. The gap is entirely at the **authoring
layer**, and it is narrow and precise:

> A `.oath` source file references bare names. Those names resolve against whatever
> ambient store is active at put time (`Store.Resolve`, a flat `names.json`
> lookup, `store.go:123`). The file states nothing about **which** external names
> it needs, **where** each comes from, or at **which hash** — and the failure is
> late and silent: an absent name is an `"unknown name %q"` elaboration error
> (`surface.go:721`), and a name that resolves to a *different* hash than intended
> produces a different closure with **no diagnostic at all**.

That last clause is the whole of the friction (`strmap-consumer-friction.md` #4,
`discovery-consumer-friction.md` #2): resolution is against an ambient, mutable,
per-store binding, and the source cannot express an intention that a mismatch
could violate.

**None of this requires a language change.** The three things missing —
specification of intent, fetch, and reproducibility — are all authoring-layer
concerns an out-of-band tool supplies. The rest of this document designs that tool
and states the test that would prove it insufficient.

## 2. The design: `oath resolve` + a lockfile + `oath put --lock`

Three sub-problems, each met by an existing mechanism:

| sub-problem | today | the tool |
|---|---|---|
| **specify intent** — "my `reverse` is `#7bb628…`" | nothing; resolution is ambient | a **lockfile**: external name → hash, generated, hand-editable |
| **fetch** — the objects must be in the target store | `remoteEnvelopeOf(hash)` (`remote.go:165`), `.oathpkg` import (`registry.go:127`) exist, but only as whole-namespace bundles | `oath resolve` fetches exactly the file's external closure |
| **reproducibility** — same source → same closure | `oath explain` verifies *after* the fact | `oath put --lock` verifies *before* elaboration, failing early on a mismatch |

### `oath resolve <file> [--remote <url>] [-o <file>.lock]`

1. **Compute the external name set.** A `.oath` batch's *declared* names vs its
   *referenced* names is already separated by `publish_closure.go` — `collectDeclared`
   (`publish_closure.go:77`) gathers declared symbols, and `qualifyNames` rewrites
   only same-batch references, leaving external ones bare (`publish_closure.go:22`).
   The external set is: names referenced (by `elabName`/`parseTy`) that the batch
   does not declare.
2. **Pin each external name to a hash.** From the local store (`Resolve`) or the
   registry (`remoteNameRevision(name)`, `remote.go:151`). A name that resolves
   nowhere, or to two different hashes across the sources consulted, is reported —
   the author pins one in the lockfile by hash.
3. **Fetch** any pinned object absent from the target store, via `remoteEnvelopeOf`
   (single object) or the existing `.oathpkg` path (a closure bundle).
4. **Write the lockfile** (`<file>.lock`): the external dependencies as
   `name → hash (+ source)`, plus the resolved transitive closure. Its shape is the
   forward-direction twin of `ProvenanceManifest` (`program.go:597`), which already
   records `Entry`, `Closure []string`, and derived `Requirements` post-hoc — the
   lockfile records the *subset the author must pin* (the external names) ahead of
   time.

### `oath put --lock <file>.lock <file>`

Before elaborating, verify every locked name resolves, in the target store, to the
locked hash. On absence or mismatch, **fail early and precisely** — "`reverse` is
locked to `#7bb628…` but this store binds it to `#a1b2c3…`" — instead of the late
`"unknown name"` or the silent wrong-closure of today. This is the `--expect
name=hash` idea from `discovery-consumer-friction.md` #2, generalized from a flag
to a first-class lockfile: it turns the ambient binding into a **checked** one.

Crucially, `put --lock` still elaborates ordinary bare-name source — the lock is a
*guard over resolution*, not a new source form. Identity is untouched: the `Def`
hashes exactly as it does today, because the lock only constrains **which** object
each name must already point at, never what gets baked in.

## 3. Answering #187's design questions

1. **Tooling or language?** Tooling. Identity encodes the closure; a lockfile
   guards resolution without changing it. (Falsifier in §4.)
2. **Name vs hash — what does an import pin?** The lockfile pins **hashes**; source
   keeps readable **names**. It is **generated** by `resolve` and **editable** by
   hand — the standard lockfile split, and exactly the "readable names in source,
   hashes in a lock artifact" shape the issue anticipated.
3. **What is the unit?** The **file** is an authoring convenience (a batch of
   `Def`s, no identity — confirmed: `apiPut` accepts only top-level `data`/`defn`,
   `api.go:49`). The **lockfile** is the dependency-declaration unit, per-file or
   per-project. **No new "package" object.** Identity is per-definition, and
   `ProvenanceManifest` already yields a per-*program* closure with no package
   concept — inventing one would be an unjustified addition.
4. **Where does resolution happen, and from where?** At a new `resolve` step,
   against the local store or a registry (reusing `remoteNameRevision` /
   `remoteEnvelopeOf`). `put --lock` verifies; plain `put` is unchanged. A bare
   name maps to a source via the registry's own name→hash — the tool adds no new
   namespace semantics to `Resolve` (which stays a flat lookup).
5. **Relationship to what exists.** It reuses, rather than duplicates:
   `publish --namespace`'s identity-neutral source-transform discipline (validate by
   re-elaboration, `publish_closure.go:171`), the registry's name→hash, the remote
   fetch primitives, the `.oathpkg` closure bundle, and the `ProvenanceManifest`
   record shape. The lockfile is the one genuinely new artifact — and it is
   authoring metadata beside the file, not in it.
6. **Ambiguity and shadowing.** A store's `names.json` is 1:1 (one hash per name at
   a time, `store.go:123`), so *in-store* ambiguity does not arise. The real
   ambiguity is **cross-store**: the same source elaborates differently in a
   repointed or different store. The lockfile pins the hash and `put --lock` fails
   on disagreement — the ambiguity is resolved by declaration. When a *registry*
   offers several candidates for one name (different authors/namespaces), `resolve`
   reports them and the lockfile pins one by hash.

## 4. The falsifier — measurable on the committed corpus

Per the standing rule that an architectural issue keeps a credible path to "no
change required," here is the test whose **pass** closes #187 as tooling and whose
**fail** would justify a language change. It uses a real consumer the corpus's
other files reference by bare name.

**Setup.** `docs/experiments/discovery-consumer/app.oath` — its body
`(str-join 44 (reverse [Str] args))` references the external names `str-join`
(`#b41104b62aae`), `reverse` (`#7bb6285884d0`), and `str-append` (`#7d158d0455d3`,
via a property), all committed in `codebase/`. `app.oath` itself is **not** a
corpus member — it is exactly the case the tool exists for: a program the corpus
does not contain, whose author must otherwise know which files to pre-load.

**PASS (no change required), the expected outcome:**
1. `oath resolve app.oath` against the committed corpus writes `app.oath.lock`
   pinning `str-join`, `reverse`, `str-append` to the three hashes above.
2. Into a **fresh empty store**, `oath resolve app.oath --remote <corpus-as-registry>`
   fetches exactly those objects (plus their transitive closure) and writes the
   same lockfile.
3. `oath put --lock app.oath.lock app.oath` in that fresh store elaborates
   `args-in-reverse` to the **same object hash** it gets when put against the full
   corpus — because both stores present the identical dependency hashes, and
   identity is a function of the closure, not of the store. The invariant is
   cross-store hash identity, verified by putting `app.oath` both ways and
   comparing. No language, SPEC, or identity change was involved.

**The stress case the falsifier must survive** (this is where a language change
would announce itself): repoint `reverse` in the target store to a *different*
object, then `oath put --lock app.oath.lock app.oath`. It must **fail early** with
a name/hash mismatch — proving the lockfile makes the intention checkable, the
exact property finding #2 says is absent. If the only way to express the intended
dependency were an in-**source** hash reference (`#7bb628…` in the body), that
would be a surface change; the lockfile carries the pin out-of-band, so it is not
needed.

**FALSIFIED (language change needed) would look like:** a concrete authoring or
reproducibility need, met against a real consumer, that the lockfile cannot serve
without changing what a definition means or hashes to. None is known; the burden is
on the counterexample.

## 5. Explicitly out of scope (and why)

- **A source `import`/`require` keyword.** It would put resolution *in* the file,
  re-coupling source to an ambient decision — the opposite of the fix. The lockfile
  keeps names (readable) and hashes (reproducible) in separate artifacts.
- **A `#hash` reference in source** (finding #2's "syntax to pin a hash"). A pure
  **surface** convenience, identity-neutral — the `Def` hashes the same. Deferred:
  the lockfile already carries the pins, and mixing hashes into source hurts
  readability for no reproducibility gain the lock does not already provide. Revisit
  only if a consumer shows the lockfile insufficient.
- **A "package" identity object.** Unjustified: identity is per-definition, and the
  per-program closure already exists (`ProvenanceManifest`).
- **Signing the lock / the manifest.** `ProvenanceManifest` is deliberately unsigned
  (`program.go:782`); binding a manifest to an artifact digest is the separate #116
  concern and does not gate resolution.

## 6. Recommendation

Build the minimal tool — `oath resolve` (compute external set, pin, fetch, write
lock) and `oath put --lock` (verify then elaborate) — and run the §4 falsifier
against `app.oath`. The expected result closes #187 with **no** change to the
language, the SPEC, or any hash. Do not add a source import form, a package object,
or a hash-in-source syntax unless the falsifier fails.

This keeps #187 in the class the project prefers: an architectural question
answered **no**, with the "no" made checkable rather than asserted.
