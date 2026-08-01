# 004 — migrating the live `oath/*` delegation to `oath-delegate/2`

2026-08-01. Moving the production CI authority onto the format that carries a
delegation revision, before the delegation became operationally load-bearing.

## Why do it at all

`#106` added `delegation_rev`: a permission-state revision that a grant or
revocation must state and that only an ACCEPTED statement advances. Without it,
revocation is not durable — a grant signed before a revocation stays submittable
after it, because nothing in the envelope records which permission state it was
written against.

Fixing the kernel did not fix the registry. The live `oath/*` delegation was
still a `/1` statement, so the CI authority governing the standard library still
lacked the property. The window to migrate was now: the delegation was hours old,
four entries existed, all held by one person, and no external party depended on
it. Later it would have been a format migration plus a spec amendment.

## What was there first

```
seq 1241  oath-reserve/1   reserve  oath/*                     accepted → authority_rev 1
seq 1247  oath-delegate/1  delegate → 26923b…                  accepted
seq 1248  oath-delegate/1  delegate → 26923b…                  rejected
seq 1250  oath-delegate/1  revoke   → 26923b…                  accepted
seq 1254  oath-delegate/1  delegate → 26923b…                  accepted
```

Holder `4ecd57…`, authority_rev 1, delegation_rev 3 (1248 was refused and
therefore confers nothing and counts for nothing).

**The defect is visible in this history.** 1247 and 1254 are byte-identical
envelopes: same op, namespace, subject, authority, authority_rev. One is the
original grant, the other a deliberate re-grant after a revocation, and under
`/1` nothing distinguishes them. A replay of 1247 and the legitimate 1254 are the
same bytes.

## The steps

1. **Deploy first.** The registry ran `b8e41ee`, which predates `/2`; a revocation
   would have been refused as an unreadable envelope. Deployed `83a335e`.
2. **Revoke through the new mechanism.** `oath revoke 'oath/*' --kms-key …`
   derived `delegation_rev=3` from the live journal independently, matching the
   count above. Recorded at seq 1263.
3. **Replay the original `/1` bytes.** Taken verbatim from seq 1247 — envelope
   and signature — and resubmitted as the holder, since the realistic replayer is
   someone who HAS the key: a retry loop, a redeploy, an old file. Refused:

   > stale delegation state: signed against delegation_rev=0, but "oath/\*" is at 4

   Preserved at seq 1264 as `rejected` with its envelope intact. Authority
   untouched at rev 1, and the permission state did not advance — a refusal
   confers nothing and costs nothing.
4. **Re-grant under `/2`** at delegation_rev 4. Recorded at seq 1265.
5. **Confirm the delegate can publish.** Nothing was pending, so running the
   workflow would have correctly no-opped and confirmed nothing. Instead the CI
   key republished an existing member directly — same artifact, no new name, no
   semantic change — which exercises the authority path and nothing else.
   `oath/abs` at seq 1266, "the registry persisted the exact 286 bytes that were
   signed".
6. **Verify everything.** Journal VERIFIED, 1266 entries, chain intact. Holder
   `4ecd57…` at rev 1, delegate `26923b…` active. Both keys non-exportable in
   Cloud KMS, matching the journal and the manifest, with no local private
   material for either. The delegate's 8 publications survive the full revoke
   cycle with authorship intact.

## Why `/1` is not simply refused

A `/1` envelope carries no permission state, so it reads as state 0. That makes it
acceptable as a prefix's FIRST delegation — where it is indistinguishable from a
correct `/2` at 0 — and stale forever after, since any accepted delegation
advances the counter past it.

The legacy format therefore self-deprecates rather than being specially rejected,
and the refusal a replay meets is **"this is stale"**, which is true, rather than
**"this is old"**, which would also refuse the one case that is genuinely sound.

## What this cost, and what it caught

Two backward-compatibility breaks, both found by auditing the LIVE journal and
neither by the committed corpus, which contains no delegations at all:

- verification re-encoded `/1` statements as `/2` before checking them —
  invalidating correct signatures rather than rejecting bad ones;
- the kind-dispatch matched only the current version constant, so `/1` entries
  stopped being recognised as delegations and the journal failed outright.

Generalised into SPEC as `AUTH-VERSION-IS-SIGNED-DATA`, with the corollary that
earned it: **a compatibility corpus must contain the history it exists to
protect.** Fixtures that predate a feature cannot defend it.

## What the milestone can now claim

The first post-merge automated publication demonstrates not merely that automation
publishes under delegated authority, but that it publishes under authority whose
revocation is independently versioned and replay-safe.
