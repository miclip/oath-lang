# Standard Library Publication — oath/*

> **Note added 2026-08-02.** This receipt records the check set as it stood at the
> time, and remains accurate for that moment. Two things have changed since, so it
> no longer reproduces verbatim:
>
> - the generator now runs **ten** checks rather than eight (referenced-member
>   resolution and non-republication were added with `referenced` membership);
> - **"signatures are by the recorded authority key"** is now **"signatures are by
>   the holder or a current delegate"**. The old wording was correct here — this
>   pilot predates delegation and the holder key signed directly — but it became
>   wrong the moment CI began signing as a delegate, and stayed wrong until
>   `docs/receipts/005` ran it for the first time since.
>
> Receipts are derived from live registry state, so a fresh one can always be
> generated. This file is not regenerated: it is the record of what was checked
> then, and rewriting it would destroy the thing it exists to preserve.


## Manifest

- commit: `f4dab2b1fe78`
- manifest digest: `2e51cbfb5f44caac3093e53fe6fe7e101a1cfc4bd1ad17a1733cecd1d25d8b1c`
- membership modes: project-publication

## Signer

- KMS key version: `projects/oath-prod-503514/locations/us-central1/keyRings/oath-authority/cryptoKeys/oath-project/cryptoKeyVersions/1`
- public key: `4ecd572dffebe8fc36b376fdee1cb358863a6d61fda2e37fb6c6e9c4ac1ffa6c`
- fingerprint: `934d7c3bbc02ef8ad3568ae02c2254267e338a0d7e66f09fa36fb6f76944d31b`

## Published names

- `oath/List`
- `oath/append`
- `oath/length`
- `oath/reverse`

## Verification

- ✓ repository manifest reproduced
- ✓ published names match the manifest exactly
- ✓ envelope bytes carry the manifest's artifact
- ✓ journal entries persisted and chain intact
- ✓ signatures are by the recorded authority key
- ✓ namespace authority verified from the journal
- ✓ licence assertions reproduce the manifest
- ✓ dependency closure reproduced

## What this receipt does and does not establish

Every tick above was COMPUTED by `scripts/publication-receipt.py` from the
journal snapshot named below, not asserted by a person. A check that could not
be performed appears as ✗ with its reason rather than being omitted.

It establishes that the repository, the manifest, the signed journal and the
live namespace agree. It does NOT establish that the asserted licence terms are
true, nor that the publisher held the rights they assert — the registry
notarises a claim and does not audit the claimant.

- journal snapshot: `live-post-pilot.jsonl`, 1246 entries
- signed entries: 395
- publications under `oath/`: 4
