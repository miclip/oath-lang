# license-consumer — a demand list from the licensing evidence domain

The flywheel's "depend on Oath, don't improve it" exercise, aimed this time at
LICENSING — the evidence domain no prior consumer round exercised. The consumer is
a one-line application (`tally`, the length of a list) that asserts its own terms
and depends on the `List` datatype, published exactly as the standard library ships
it. The instrument is [`license-consumer/run.sh`](license-consumer/run.sh), which
publishes real signed envelopes to a served registry — only a publication asserts
terms (SPEC §12.3), so nothing here is asserted by hand — and reads the verdict the
registry derives.

## What the evidence is, and what it is not

Every verdict is MEASURED by running `oath license` against a live registry after
real publications. The licensing engine is correct and the exercise proves it: over
an all-licensed closure it derives `commercial YES` / `modification YES` from
Apache, propagates a GPL dependency's `share-alike` obligation up into an Apache
application (§ below), and names the untermed culprit when a dependency is
UNSTATED. Contagion, transparency, obligation-propagation — all work.

**One claim in an earlier draft of this document was WRONG, and correcting it is
the sharpest lesson of the round** — see §1.

---

## 1. WITHDRAWN — "the stdlib asserts no license" was a fact about my LOCAL store, not the world

An earlier draft led with a headline demand: the standard library asserts no
terms, so UNSTATED contagion makes every composition on it indeterminate. It was
built by publishing an unlicensed `List` to a fresh registry and watching a
licensed `tally` derive all-UNSTATED — reproducible, and reproducing the wrong
thing. **The finding was a fact about the store I built, reported as a fact about
Oath's corpus** — exactly the "is this sentence about the WORLD or about my TOOL?"
error this project keeps having to catch.

Verified against the DEPLOYMENT, not a document: the live registry
(`registry.oath-lang.org`) derives `commercial_use: YES` for `reverse`, with every
assertion in the closure `Apache-2.0`. The standard library IS licensed
(`docs/licensing.md`: 184 names, Apache-2.0, signed, since 2026-08-01). What is
unlicensed is the committed `codebase/` store, which lags the live registry — the
`codebase/`-vs-live drift CLAUDE.md documents as EXPECTED — and the fresh local
store the experiment created, which reproduced that lagging state rather than the
registry a real consumer resolves from.

So there is no demand here. A consumer resolving from the live registry gets
determinate verdicts; the mechanism the run.sh still demonstrates — that ONE
unlicensed dependency is contagious, and that relicensing it flips the composition
to a determinate `YES` — is the engine working, shown with a deliberately
unlicensed dependency, not a claim about the stdlib.

**The residual, and it is hygiene not a gap:** committing the live registry's
license assertions back into `codebase/` would make LOCAL evaluation match live, so
this exact mistake is harder to make. That is the same drift every other part of
`codebase/` has, deliberately, and it is recorded here only so the withdrawn demand
is not silently re-derived by the next consumer who tests against a local store.

**Evidence class:** the live-registry `commercial_use: YES` / Apache-2.0 assertions
are MEASURED against `registry.oath-lang.org`; the `codebase/` all-UNSTATED is
MEASURED locally; the contagion mechanic and its relicense control are MEASURED in
`run.sh`.

---

## 2. A licence verdict cannot be PREVIEWED before publication — you must publish, permanently, to see it — TOOL LIMITATION

**Second.** Terms are asserted only in a signed publication envelope, and `oath
license` reads them only from the journal. `oath put` takes no `--license`, and
there is no `oath license` mode that evaluates a proposed assertion against a local
closure. So a publisher deciding whether Apache-2.0 gives their composition the
terms they intend cannot find out without PUBLISHING — and a publication is
permanent (the name binding and the asserted terms both enter the append-only
journal). `--dry-run` prints the envelope bytes it would sign, including the licence
field, but does not derive the composition verdict those terms would produce.

**Consumer hurt: a publisher choosing terms.** The one question a licence chooser
has — "what will my users actually be permitted to do?" — is answerable only after
the irreversible act. The measured cost is that licence selection is
publish-then-check rather than check-then-publish.

**DEMAND: derive a composition's licence verdict from a PROPOSED assertion against
the local closure, before signing.** Whether that is `oath license <file>
--assert <SPDX>`, a `--license` on a `--dry-run` publish that runs the evaluator,
or a local `put --license`, is a design question; what matters is that the verdict
is checkable before the permanent act, as `publish --dry-run` already makes the
BYTES checkable.

**Evidence class:** that `--license` is publish-only and `put` rejects it is
decidable from the CLI; HAND-verified. The absence of a preview path is
HAND-observed.

**RESOLVED.** `oath license <file> --assert <SPDX>` now derives a composition's
verdict from a PROPOSED assertion, against the local closure, before anything is
signed:

```
$ oath license tally.oath --assert Apache-2.0
  commercial use           UNSTATED      ...
  assertions consumed (2):
    tally    Apache-2.0                  # the term you are PROPOSING
    List     (none)  — no terms asserted # its real, published terms
PREVIEW — nothing was signed or published. ...
```

It elaborates the single definition against the local store, computes its
dependency closure exactly as `oath license <name>` does, and evaluates with the
proposed term standing in for the subject's not-yet-existing publication — check-
then-publish, mirroring how `publish --dry-run` already makes the signed BYTES
checkable, one layer up at the derived verdict. The dependencies' terms stay REAL
(read from their publications); only the subject's is a proposal. Nothing is signed
or published — no name binds and no journal entry is written (opening a store still
creates its directory, as every read command does; the guarantee is about the
irreversible act, not the filesystem). `run.sh` asserts both that the preview leaves
its store's names, journal and objects byte-identical and that its grants MATCH the
verdict the eventual publication derives — the preview is faithful, not an
approximation.

Scope, stated so it is not overread: the preview evaluates the LOCAL store's facts,
exactly as `oath license <name>` does, so it predicts the verdict a publication INTO
THIS STORE would derive. It is faithful for a fully-local publish flow, where the
dependencies' terms are in the local journal. It is NOT the whole story for the
common remote flow — see demand 3, which the preview work surfaced. `evaluateLicensingSubject` / `cmdLicensePreview` in
`oath/license.go`, unit-tested in `license_preview_test.go` (the proposed term
drives the verdict; dependency contagion still applies).

**Also fixed, a pre-existing bug the preview work surfaced.** Building the preview
made it worth checking what closure `oath license` actually evaluates, and it was
DIRECT dependencies only: for `aay → bee → cee`, `oath license aay` listed `aay`
and `bee` but not `cee`. A restrictive or unlicensed TRANSITIVE dependency was
silently dropped, so the verdict could read permissive when the true composition is
not — the exact failure a licensing check exists to prevent. Both the published
path and the preview now evaluate the full transitive closure (`licensingClosure`),
and the run.sh grant-match check confirms the two still agree. Found by measuring
the tool, not reading it — `oath license aay` before the fix named two inputs where
the composition has three.

---

## 3. Licensing facts do not travel with the code — a local licence verdict is blind to remote terms — DEMAND

**Surfaced by building the preview, and it bites the published path too.** A licence
is asserted in a signed PUBLICATION envelope, which lives in the journal of the
registry it was published to. `resolve`, `hydrate` and `clone` bring a dependency's
OBJECT and NAME into a local store, but not that signed envelope — the local journal
they reconstruct is a chain of local `put`s, which carry no licence. So
`assertedLicense` reads nothing for a dependency published elsewhere, and BOTH
`oath license <name>` and the new preview report it UNSTATED, while the registry
that holds the envelope derives the real term (e.g. Apache-2.0 → YES).

The preview makes this visible because its whole purpose is to predict the eventual
publication: for the ordinary `oath publish --remote` flow, the dependencies' terms
are on the target registry, the local store lacks them, and there is no remote
preview path — so the prediction is only as complete as the local licence facts.
The verdict is never WRONG (UNSTATED is honest about missing evidence, and
contagion keeps it safe), but it is less INFORMATIVE than the registry's.

**Consumer hurt: anyone evaluating a licence locally against remotely-published
dependencies** — which is the normal case. The measured cost is a local UNSTATED
where the registry would give a determinate answer.

**DEMAND: make the licence evidence reachable where the composition is evaluated.**
Either (a) `resolve`/`hydrate` bring each dependency's signed publication envelope so
local evaluation carries the same facts the registry has, or (b) a remote preview /
evaluation path — `oath license <file> --assert <SPDX> --remote <url>` — fetches the
dependencies' asserted terms from the registry the artifact will be published to.
(a) makes the whole local evidence layer complete, not just licensing; (b) is
narrower and answers the preview's exact question. Either removes the blindness; the
preview shipped scoped to local facts and flags when it lacks them, which is the
honest interim.

**Evidence class:** that `finalizePublish`/clone reconstruct a local put-journal
without the signed licence envelope is decidable from the code (HAND-verified); the
resulting local UNSTATED for a remotely-Apache dependency is the mechanism §2's
scope note and this section both describe.

**PARTLY ADDRESSED, and the harder half is a design not yet built — recorded so the
dead end is not walked again.** The reading half is done: `oath license` now reads a
member's terms BY HASH (`licenseOfHash`), the §12.2-correct identity, so a member
whose terms ARE local but whose NAME is not bound — the shape of a transitive
closure member — is no longer reported UNSTATED for terms the journal holds.

The transport half — carrying the registry's signed publications into a local store
via the lock — was BUILT AND REVERTED, because the obvious mechanism is wrong in a
way worth writing down. Re-journaling a fetched publication into the local store
gives it a NEW entry identity (fresh timestamp/seq/chain), so the §8.2.2 publication
identity the licensing digest binds is store-specific and not reproducible; and
authenticating a lock's carried publication against the source's CURRENT head breaks
the moment the dependency is relicensed, refusing an unedited older lock. Both are
one root cause: **a publication's authenticated identity is its position in the
SOURCE's journal chain, and that cannot be reproduced by re-journaling elsewhere.**

So the correct design is not journal-replay. It is a licence-EVIDENCE overlay: the
lock carries each dependency's signed publication together with a REPRODUCIBLE
identity (a digest of the statement, not of a journal line), a local evidence store
that `licenseOfHash` consults ALONGSIDE the journal, and authentication against the
source's HISTORY rather than its current head (so relicensing does not invalidate a
pinned lock). That is a materially larger change — a new store, a registry history
query, a reproducible publication identity — and it is the shape of the remaining
demand.

**DEMAND (remaining): a source-identity-preserving licence-evidence overlay.** Carry
each dependency's signed publication with a reproducible identity in the lock;
consult it via an evidence store, not the journal; authenticate against registry
history. Until then, `oath license`/preview read only what a store already holds,
and read it correctly by hash.

---

## What is NOT friction, recorded so it is not re-derived

- **Contagion is TRANSPARENT.** The verdict lists every assertion it consumed and
  marks the untermed ones "no terms asserted", so a consumer can see EXACTLY which
  dependency made a composition UNSTATED. The system does not hide the culprit
  behind a bare verdict; it names it — which is also what let the withdrawn §1
  finding be checked and retracted rather than left standing.
- **The engine is correct and complete for what it claims.** Determinate grants
  over licensed closures, UNSTATED contagion, and share-alike obligation
  propagation across an Apache-over-GPL composition all reproduce (`run.sh`). The
  exercise found no defect in the evaluator, and — verified against the live
  registry — no gap in the corpus either: the standard library is Apache-2.0. The
  one real thing the round produced is the preview (demand 2).
- **UNSTATED is not NO.** The output says so and the model enforces it: an
  unlicensed dependency does not PROHIBIT reuse, it leaves the terms unknown, and
  the consumer is told to adopt their own policy (treat UNSTATED as deny, or
  require explicit grants). The registry deliberately does not choose that stance
  for the reader.
