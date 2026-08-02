# 005 — the first approval-gated CI publication (`oath/filter`)

2026-08-02. The production publication path executed end to end: PR, merge,
protected-Environment approval, Workload Identity Federation, Cloud KMS signing by
a DELEGATED key, and acceptance by the live registry.

Two defects surfaced, both only reachable by a real publication.

**The plan omitted already-live dependencies.** `filter` depends on `List`.
Publication resolves references against the LOCAL store, so `List` had to be
present there even though it needed no republishing — but the plan was
pending-aware and emitted only `filter`. `put` failed with `unknown type "List"`
before anything was signed. The previous project publication was chosen
DEPENDENCY-FREE on purpose, so this could not have surfaced until a member had a
dependency at all. Fixed by splitting `order.txt` (what must exist locally) from
`pending.txt` (what must be signed).

**The receipt called delegated signing a failure.** It required every publication
to be signed by the namespace HOLDER, and the CI publisher signs as a DELEGATE —
which is the entire point of delegation, and which the same workflow separately
refuses to run without. The check has been wrong since delegation shipped. It now
asks the correct question: was the signer entitled, meaning the holder or a key
granted under the prefix and not since revoked, derived from the journal.

The publication itself was correct throughout; only the verdict on it was wrong.

# Standard Library Publication — oath/*

## Manifest

- commit: `77a1626482c4`
- manifest digest: `8084aa9954f2bc70fbf2e74d6960b088a07df57af6f05c669167cca5871ae767`
- membership modes: project-publication, referenced

## Signer

- KMS key version: `projects/oath-prod-503514/locations/us-central1/keyRings/oath-authority/cryptoKeys/oath-project/cryptoKeyVersions/1`
- public key: `4ecd572dffebe8fc36b376fdee1cb358863a6d61fda2e37fb6c6e9c4ac1ffa6c`
- fingerprint: `934d7c3bbc02ef8ad3568ae02c2254267e338a0d7e66f09fa36fb6f76944d31b`

## Library members

- `oath/List`
- `oath/abs`
- `oath/append`
- `oath/filter`
- `oath/length`
- `oath/reverse`

## Non-members under the namespace

Bound under `oath/` and NOT part of the standard library. Listed, not
failed: the registry is append-only historical infrastructure and the
library is a curated view over it.

- `oath/step8-probe-disposable`
- `oath/step8-probe-second`

## Verification

- ✓ repository manifest reproduced
- ✓ all 6 project-published members are live under oath/
- ✓ no referenced member was republished under the namespace
- ✓ all 12 referenced members resolve to a signed pinned publication
- ✓ envelope bytes carry the manifest's artifact
- ✓ journal entries persisted and chain intact
- ✓ signatures are by the holder or a current delegate
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

- journal snapshot: `rl.jsonl`, 1273 entries
- signed entries: 420
- publications under `oath/`: 8
