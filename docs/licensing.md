# Licensing

Licensing in Oath is an **evidence domain**, not a metadata field. It follows the
same split as every other verdict the registry produces:

| the publisher | the registry |
|---|---|
| ASSERTS terms, signed, immutable once published | DERIVES what a composition permits, reproducibly |

Nothing else about the system changes. A licence assertion is part of a signed
publication envelope; a licensing verdict is a derived fact recomputed from the
journal on demand, with an identity that lets you re-derive it rather than trust
it.

## What this is not

- **Not legal advice.** The registry evaluates a lattice; it does not practise
  law.
- **Not `PROVEN`.** Compatibility over a finite lattice is decided by evaluation,
  not proved over unbounded inputs. Reusing the word would overload the strongest
  claim the system makes.
- **Not a licence scanner.** Nothing infers terms from a `LICENSE` file, a header
  comment, or a package manifest. Terms are asserted by a publisher who signed
  for them, or they are `UNSTATED`.

## Asserting terms

A licence is asserted in the publication envelope (SPEC §8.6.1), so it is covered
by the author's signature:

```
oath publish --remote https://registry.oath-lang.org \
             --key mykey.key \
             --license Apache-2.0 \
             mydef.oath
```

`--dry-run` prints the exact bytes that would be signed before signing anything:

```
EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):
  | oath-publish/2
  | op=put
  | name=pow
  | artifact=090cc5373e20…
  | parent=090cc5373e20…
  | parent_rev=1
  | author=65ea5701d92e…
  | license=Apache-2.0
```

Three consequences worth understanding before you publish:

- **The registry does not validate the expression.** This layer is the notary for
  what the author signed; checking SPDX syntax would drift publication into
  policy enforcement.
- **Terms belong to the PUBLICATION, not to the artifact.** The same code
  published twice under different terms is two assertions and two evaluations —
  which is what makes dual licensing expressible, and relicensing possible
  without changing a hash.
- **A re-publication of identical content still asserts.** An `unchanged`
  transition carries terms exactly as an `applied` one does (§12.3
  `LICENSE-ASSERTED-BY-PUBLICATION`). That is how you relicense: the code does
  not change, the terms do.

Omitting `--license` asserts nothing, which is recorded as the explicit
no-assertion sentinel `-` rather than as an empty field. "The publisher chose to
say nothing" and "the format had no licence line" are different historical facts.

## Previewing before you publish

A publication is permanent, so the question a publisher has — *what will my users
actually be permitted to do?* — is worth answering BEFORE signing. `--dry-run`
above makes the signed BYTES checkable; `oath license <file> --assert <SPDX>` makes
the derived VERDICT checkable, one layer up:

```
oath license mydef.oath --assert Apache-2.0
```

It elaborates the single definition against your local store, takes its transitive
dependency closure exactly as `oath license <name>` does, and evaluates the
composition with the proposed term standing in for the not-yet-existing
publication. Only the subject's term is a proposal; the dependencies' terms are read
from your store's publication records. Nothing is signed and nothing is published.

Because it reads local facts, the preview predicts the verdict a publication INTO
THAT STORE would derive — faithful for a fully-local publish flow, where the
dependencies' terms are in the local journal. It is limited for the ordinary remote
flow: a licence lives in the signed publication envelope on the registry it was
published to, and `resolve`/`hydrate` bring a dependency's object and name but not
that envelope, so a remotely-published dependency reads UNSTATED locally. The
verdict is never wrong — UNSTATED is honest about missing evidence — but it is less
informative than the registry's, and previewing against a remote registry's
assertions is not yet wired.

## Reading the verdict

```
oath license <name>
```

or, over MCP, the `license` tool — which returns JSON because the consumer is
usually an agent deciding whether it may ship something.

The verdict has five dimensions. Four are PERMISSIONS and one is an OBLIGATION,
and they combine with **opposite polarity**:

| dimension | kind | combines |
|---|---|---|
| commercial use | permission | any `NO` → `NO` |
| redistribution | permission | any `NO` → `NO` |
| modification | permission | any `NO` → `NO` |
| patent grant | permission | any `NO` → `NO` |
| share-alike | **obligation** | any `YES` → `YES` |

A prohibition anywhere defeats a permission; a reciprocal requirement anywhere
binds the whole composition. Equivalently: permissions fold as MINIMUM and
obligations as MAXIMUM over `NO < UNSTATED < YES`.

### UNSTATED is not permission

This is the single most important thing to get right, and it is where Oath
differs most from a licence scanner.

`UNSTATED` means *no evidence*, and it is **contagious**: one unknown or
unmodelled dependency makes the entire composition unknown, however many others
granted. Ignorance can establish neither a permission nor the absence of an
obligation.

A consumer may adopt "treat `UNSTATED` as deny" or "require explicit grants". The
registry will not choose that for you, and it will not quietly upgrade absence of
a prohibition into a grant.

## The evaluation identity

Every verdict carries a digest binding **what was evaluated and by what**:

```json
{
  "engine": "oath-license/1",
  "model": "spdx-lattice/1",
  "model_digest": "a3aafb3713e3…",
  "policy": "composition",
  "evaluation_digest": "10a68f83e42f…"
}
```

- `engine` — the evaluator, versioned independently of the lattice it consults.
- `model` + `model_digest` — the lattice, bound by **content** and not only by
  name. Editing a row while holding the version string fixed changes every
  identity; without that, a lattice could be altered and every historical verdict
  would silently mean something else.
- `policy` — how the input set was SELECTED. Only `composition` is defined: the
  artifact together with its exact transitive dependency closure.
- `evaluation_digest` — binds the subject and every consumed input.

Each input is a TRIPLE, and each component answers a question the others cannot:

| component | answers |
|---|---|
| artifact hash | which code was evaluated |
| publication identity | whose grant was consumed |
| asserted expression | what terms that grant stated |

Names are **discovery paths** and contribute nothing to identity. Renaming a
definition does not change its evaluation; repointing a name to different code
does, because the closure changed.

## The model is published, not built in

The lattice mapping identifiers to grants lives in
[`fixtures/license/model.json`](../fixtures/license/model.json), incorporated by
reference as NORMATIVE DATA (SPEC §13.1b). It is not frozen into the
specification: a legal model is expected to be corrected, and §12.4 already makes
each version distinguishable, so freezing it would make every correction a
specification fork.

Two rules keep it honest:

- an identifier **absent** from the model yields `UNSTATED` — safe;
- a **compound** expression (`MIT OR Apache-2.0`, `... WITH ...`) is never
  resolved, because choosing a disjunct is a decision with legal consequence and
  belongs to the consumer.

Identifiers match by **exact octet equality**. `mit`, `MIT ` and `(MIT)` are not
`MIT`. A registry that helpfully normalised would turn an expression the
publisher never wrote into a grant.

## What a verdict does not tell you

Recorded in the output rather than left implicit:

- whether the asserted terms are TRUE — the registry notarises a claim, it does
  not audit the claimant;
- whether the expression matches what the referenced publication actually
  contains — currently an open design question, see DESIGN.md;
- anything about jurisdictions, obligations outside the five dimensions, or
  compatibility questions the model does not represent.

## The corpus asserts Apache-2.0

Since 2026-08-01 the live registry's corpus carries signed licence assertions:
184 names, `Apache-2.0`, each in a publication signed by the publisher's key. So
a composition query returns a real verdict rather than the honest-but-empty
`UNSTATED` a corpus with no assertions must produce.

Apache-2.0 was chosen against the model rather than by reputation. It is the only
row granting all four permissions with no share-alike obligation — MIT, ISC, BSD
and Unlicense all leave `patent_grant` UNSTATED, and because UNSTATED is
contagious, a corpus published under any of them would make the patent dimension
unknown for every downstream composition.

## See also

- SPEC §12 — normative licence evaluation
- SPEC §13.1b — the model as normative data
- `DESIGN.md` — why licensing is an evidence domain, and the open questions
- `docs/experiments/blind-license-evaluation.md` — five blind implementation
  rounds against §12, and what each found
