# Step 8, attempt 1 — ABORTED

**Manual operator-driven protocol exercise. Not workflow-driven.**

Stopped at step 3 of 13, before any state was mutated.

## What was being tested

> The live registry preserves rejected signed intent while deriving no authority
> from it, and revocation removes delegated control without altering historical
> authorship.

## Result

**FAILED on the first clause, before mutation.**

| step | | |
|---|---|---|
| 1 | baseline captured | journal 1247, `oath/*` held by `4ecd572d…` at rev 1, four names live |
| 2 | submit a validly signed delegation the registry refuses | re-grant of an already-active delegate — refused for a protocol reason, as intended |
| 3 | verify the refusal is journalled | **journal unchanged at 1247 — the statement was DISCARDED** |

The deployed registry returned an API error and appended nothing. A validly
signed, correctly authenticated delegation that it refused left no trace.

## Why this is a protocol defect and not a variant

`put` and `delegate` disagreed about what a refusal IS:

- a blocked publication stores its object and journals `blocked`; the name does
  not move. Refusal is a HISTORICAL EVENT.
- a refused delegation returned an error and vanished. Refusal was an API ERROR.

So `AUTH-ACCEPTANCE-IS-THE-BOUNDARY` was satisfied — no authority was derived —
while the half of the claim that says the intent is PRESERVED was not implemented
at all. Authority was right and history was gone, which is the failure mode that
looks correct from every angle except the one that matters afterwards.

## What it separated

Three outcomes that had been collapsed into one:

| | |
|---|---|
| submission outcome | accepted or refused |
| journal outcome | preserved or absent |
| authority outcome | state changed or unchanged |

A refusal can be historically real without being operationally effective. Only
the first and third were being modelled.

## Disposition

Repaired by `AUTH-REFUSALS-ARE-PRESERVED` (SPEC §8.7.0) and six boundary tests
pinning which attempts are journaled and which are not. Attempt 2 restarts from a
fresh baseline against the repaired registry.

**This record is kept deliberately.** The unchanged journal is evidence of the
OLD deployed behaviour, and it is more useful as a recorded failed attempt than
folded into the repaired exercise as though it had been part of it.
