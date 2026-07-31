# Deployment delta review — registry cutover

**Status: PREPARED, NOT DEPLOYED.** This is step 1 of the activation sequence. It
exists so the cutover is reviewed as a whole rather than justified by whichever
feature prompted it.

- **Live registry runs:** `53c8de0` (deployed 2026-07-28, `workflow_dispatch`)
- **Current HEAD is ahead by:** 86 commits, 58 touching `oath/` or `docs/SPEC.md`
- **Kernel diff:** 37 files, ~7,758 insertions
- **Deploy is manual** and should stay manual. Nothing here is an argument for
  deploying today.

## What the cutover actually contains

It is not "a licence evaluator". Licensing is one of five behavioural changes,
and the smallest risk of them.

| # | change | affects | risk |
|---|---|---|---|
| 1 | **Client-side signing** — signed publication envelopes, `envelope_b64` / `author_pubkey` / `author_sig` persisted verbatim | what the service accepts and stores | new code path; no existing entry uses it |
| 2 | **`parent_rev` persisted** (§8.2.1) | journal format | additive, `omitempty` |
| 3 | **Transition DERIVED, not consumed** (ENV-VERIFY-DERIVED-TRANSITION) | verification semantics | changes what verification *means*; see below |
| 4 | **Envelope `oath-publish/2`** with the `license` field | envelope format | `/1` still readable |
| 5 | **Licence evaluation** (§12) | a new derived verdict surfaced in `explain` | read-only; derives nothing from stored state |

## Backward compatibility: checked, not assumed

**The live journal cannot contain any of the new members.** At `53c8de0` the
`LogEntry` struct has exactly sixteen: `seq time author verifier name kind status
hash prev error guarantee termination context pubkey sig chain`. `NameTransition`,
`EnvelopeB64`, `AuthorPubkey`, `AuthorSig` and `ParentRev` did not exist, so no
live entry carries one.

That makes each new rule vacuous over existing history rather than hostile to it:

- **derived transition** — nothing to cross-check against, so every live entry is
  reconstructed under §8.6.2's KIND restriction and reported as reconstructed;
- **`ENV-VERIFY-REVISION`** — no entry carries a revision, so the check is skipped
  and reported as *unavailable* rather than *matched*;
- **signature clauses** — entries with none of the three fields remain unsigned,
  which is what they already are.

**Verified against the committed corpus with the new binary:** `oath audit` →
`JOURNAL: VERIFIED — 548 entries, chain intact`, exit 0. All 548 entries also
re-serialise byte-identically under the new field order, because §8.2.1 omits
empty members. No chain value moves and no entry digest changes.

**Caveat that this does NOT cover:** the committed corpus is a proxy for the live
journal, and the two are known to diverge (the live store lags `codebase/`). The
argument above rests on the *deployed struct definition*, not on the committed
data, which is why it holds for both — but the live journal should still be
audited with the new binary before the cutover is considered done.

## Residual risks

1. **Untested against live data.** Every claim here is from the deployed source
   and the committed corpus. Nothing has been run against the live journal.
2. **First write under the new format is the real test.** Existing entries are
   safe by omission; the new members only appear on entries the new binary
   writes.
3. **Rollback is not symmetric.** An old binary reading a journal containing new
   members will ignore them — including `author_sig`, so a signed entry would be
   silently reported as unsigned. Rolling back after any signed publication
   downgrades evidence rather than restoring a prior state.
4. **The licence model is normative data, and the image does not serve it.**
   *Corrected from an earlier draft of this document, which claimed evaluations
   would break without the file. They will not:* the kernel never reads
   `fixtures/license/model.json` at runtime — `licenseModelBytes()` generates the
   canonical bytes from the compiled-in table and `licenseModelDigest()` hashes
   those, so the fixture is EMITTED by `oath fixtures`, never consumed. Verdicts
   and digests are correct in an image containing no fixtures at all.

   The real problem is one layer out. §12.3 LICENSE-MODEL-PUBLISHED requires the
   model to be published, and §13.1b's IMPL-DATA-RETRIEVABLE requires normative
   data to be retrievable from its declared path with the bytes verifiable against
   the digest an identity binds. A deployed registry that serves no model leaves a
   third party holding `model=spdx-lattice/1` and `model-digest=<hex>` with no way
   to resolve them — so evaluations are reproducible only by someone who already
   has the repository, which is the precise failure §13 exists to prevent. Not a
   correctness bug; an auditability one, and it should be closed before the
   registry publishes evaluations to anyone but us.

## Gates at the proposed cutover commit

| gate | result |
|---|---|
| `go test ./...` | ok |
| journal audit over 548 entries | VERIFIED, chain intact |
| mutation boundary (release binary carries no rule-disable path) | PASS |
| spec-vs-fixtures (prose describes emitted bytes) | PASS |
| normative source + identity subject | PASS |
| implementability ledger | PASS (4 bound surfaces reproduce) |
| licence conformance | 22/22 obligations witnessed |
| envelope conformance | **8/20** — twelve unwitnessed, enumerated |
| fixture integrity | 5 findings, all the known #95 `map`/`Map` case-fold baseline |

## Cutover runbook

Two phases with a hard gate between them. The distinction is not procedural
caution — it is where reversibility actually ends.

### Phase A — REVERSIBLE (deploy + read-only)

**Steps 1, 2 and 5 are now ENFORCED BY `deploy.yml`** rather than performed by
hand. The workflow reads the bucket from terraform state, snapshots the journal
before `terraform apply`, verifies it, and re-verifies against that baseline
after the smoke test. Both snapshots upload as a 90-day artifact whether the
deploy succeeded or failed — a failed cutover is exactly when the before/after
pair is needed, and it cannot be reconstructed later.

1. **Snapshot** the live journal before apply. *(automated)* The bucket is read
   from `terraform output store_bucket`, never reconstructed from a name prefix:
   guessing the prefix would silently snapshot the wrong bucket and produce a
   green custody check over a journal that is not the live one.
2. **Verify the snapshot** with `cutover-check.py`. *(automated, blocking)* Every
   entry must re-encode byte-identically, the chain must be intact, no signature
   set may be partial, no unsigned entry may claim a revision. A journal that is
   ALREADY inconsistent blocks the deploy — otherwise a pre-existing defect gets
   attributed to this cutover and the baseline comparison means nothing.
3. **Deploy the new binary. Publish nothing.** *(automated)*
4. **Read-only smoke checks.** *(manual)* `explain` on existing names still
   reports unsigned and licensing `UNSTATED`; ownership and revision counts
   unchanged; no existing entry reinterpreted as signed.
5. **Baseline comparison.** *(automated, blocking)* `cutover-check.py
   <post-deploy> <pre-deploy>` — existing entries byte-identical, nothing
   disappeared. The pre-existing smoke test proves the endpoint SERVES; it says
   nothing about whether history was reinterpreted, which is this cutover's
   actual risk.

**A permissions failure is not a first deploy.** The workflow distinguishes them:
a readable bucket with no `log.jsonl` is a genuine first deploy and warns; a
bucket it cannot list FAILS the deploy. Conflating them would let a missing IAM
grant read as "no history to protect" and deploy blind over a journal nobody
could see.

**GO / NO-GO GATE.** Phase A completing cleanly is its own decision point. Do not
proceed to B on the same authorisation.

> **ROLLBACK ENDS HERE.** Everything above is reversible: an old binary can read
> a journal it wrote and nothing has changed. Once step 6 runs, rollback becomes
> an EVIDENCE DOWNGRADE — the old binary cannot represent `author_sig`,
> `envelope_b64` or `parent_rev`, so it silently reports a signed publication as
> unsigned. That is not a restored prior state; it is the same history with proof
> removed.

### Phase B — FORWARD-ONLY in evidentiary terms

6. **Publish exactly ONE** deliberately chosen signed artifact with an explicit
   licence assertion.
7. **Verify immediately:** envelope bytes persisted exactly; author signature
   verifies; `parent_rev` preserved; transition independently DERIVED and agreeing
   with any stored value; the prior unsigned publication unchanged; licence
   evaluation consuming the real assertion and reporting the expected model,
   policy, inputs and digest.
8. **Snapshot the post-pilot journal and verify from a CLEAN client** —
   `cutover-check.py <post-pilot> <post-deploy>`, expecting exactly one added
   entry.

Only after 8 should broader signed adoption be considered.

## What this review does NOT authorise

Deployment. The sequence after this document is: deploy the current kernel;
publish ONE deliberately chosen signed artifact with explicit licensing terms;
verify end to end that the envelope bytes persisted exactly, that `oath audit`
verifies the signature, that the OLD publication remains honestly unsigned and
`UNSTATED`, and that the evaluation and its identity derive correctly from the
real journal and dependency closure. Only then consider broader adoption.

**"Re-signing the corpus" means publishing NEW signed assertions over existing
artifact hashes.** It does not mean retroactively converting the 548 historical
entries into signed or licensed publications. Those entries are honestly
unsigned and licensing-unstated, and they must stay that way — rewriting them
would destroy the exact evidence the journal exists to preserve.
