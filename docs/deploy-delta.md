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
4. **The licence model is normative data now** (§13.1b). The deployed service
   must ship `fixtures/license/model.json`, or every evaluation returns
   all-`UNSTATED` for the wrong reason.

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
