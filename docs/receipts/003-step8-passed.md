# Step 8, attempt 2 — PASSED

**Manual operator-driven protocol exercise. Not workflow-driven.**

Run against the live registry (image `b8e41ee`) with KMS-held keys. Journal 1247 → 1255.

## Acceptance statement

> The live registry preserves rejected signed intent while deriving no authority
> from it, and revocation removes delegated control without altering historical
> authorship.

**Both clauses demonstrated.**

## Clause 1 — rejected signed intent is preserved and confers nothing

A re-grant of an already-active delegate: a real protocol refusal on a validly
signed, correctly authenticated statement. Four dimensions, each established
independently rather than inferred from the others:

| | |
|---|---|
| submission | REFUSED, with the reason stated |
| journal | PRESERVED — 1247 → 1248, `kind=delegate status=rejected`, carrying the full envelope, signature and reason |
| cryptography | VERIFIED — the preserved envelope's signature is accepted by the kernel's own verifier |
| authority | UNCHANGED — rev 1, same single delegate |

This distinguishes *no attempt occurred* from *a valid attempt occurred, was
refused, and changed nothing* — two histories that resolve to identical authority.
Attempt 1 failed here, and its record is kept at `002-step8-aborted.md`.

## Clause 2 — revocation removes control without altering authorship

| seq | event | by |
|---|---|---|
| 1249 | CI publishes `oath/step8-probe-disposable` — accepted | CI key |
| 1250 | holder REVOKES the delegation | holder |
| 1251 | CI attempts to repoint the name it created — **blocked**, transition `none` | (refused) |
| 1252 | CI attempts a second new name — **blocked**, transition `none` | (refused) |
| 1253 | holder updates the name CI created — accepted | holder |
| 1254 | holder re-delegates | holder |
| 1255 | CI publishes again — accepted | CI key |

Both refusals were preserved and transition-neutral. Control returned to the
holder; **the first publication is still attributed to the CI key**, and no
historical entry was rewritten or reattributed.

`explain` reports three distinct facts and never conflates them:

```
namespace holder:       4ecd572dffebe8fc…
active delegate:        26923b6580a21f8c…
publication authorship: 26923b6580a21f8c…  via signed-first-publication
```

Journal custody: `CUTOVER CHECK: PASS`, 1255 entries, 402 signed, chain intact.

## Finding: revocation does not advance the authority revision

Expected in the sequence, did not happen: the revision stayed at 1 through
delegate, revoke and re-delegate.

It is defensible — an authority revision versions WHO HOLDS the prefix, and
delegation does not change the holder. But the consequence is that a delegation
envelope signed before a revocation remains submittable after it, because
`(authority, authority_rev)` still matches. The duplicate-grant check is what
currently prevents re-activation, not the compare-and-swap.

NOT a third-party attack: the submitter must be the signer, so only the holder can
replay their own earlier grant, and the holder could simply issue a new one. But
"revoked" is not durable against replay of the holder's own prior statement, and
the CAS does not order delegation events. Filed rather than fixed inside this
exercise.

## Test artifacts left in place

`oath/step8-probe-disposable` and `oath/step8-probe-second` are deliberately
named and deliberately retained. The history is the evidence; erasing it to make
the namespace look tidy would destroy the record this exercise exists to produce.
