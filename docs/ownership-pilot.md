# Ownership pilot — one name, enforced

**Scope: one public name is cryptographically owned and updatable under an
enforced owner-only repoint policy.** Not a claim that registry-wide ownership is
complete, that atomic trust-on-first-publish is solved, or that live non-owner
rejection has been exercised.

## Rollback semantics, written before the write

Removing the rule from `policy.json` restores the prior enforcement
configuration. It does **not** erase the owner's accepted signed publication, or
any blocked attempts, from the journal.

**Rollback changes future authority; it does not undo history.** The journal is
append-only, so a pilot that is later withdrawn leaves a permanent record that it
happened — which is the correct behaviour and the reason the pilot is scoped to
one name rather than a prefix.

## The policy

```json
{ "rules": [ { "names": ["pow"], "owner_pubkey": "65ea5701…" } ] }
```

Exact name, not a prefix. A prefix would claim a namespace and foreclose the
contributor question; one name forecloses nothing.

## What the negative result is

The live registry has exactly **one** authorized principal, so a non-owner
publication cannot reach the ownership gate — it would be refused at
authentication, which proves a different property.

The allowlist was deliberately NOT widened. Adding a second authorized principal
would alter the AUTHENTICATION boundary in order to demonstrate the
AUTHORIZATION boundary, creating more risk than the pilot removes. The honest
statement is therefore:

> Live owner-authorized enforcement verified. Non-owner rejection verified end to
> end in the local two-authorized-principal environment; not exercised live,
> because the registry has only one authorized principal.

The local proof is recorded in `oath/policy_prefix_test.go`
(`TestOwnershipRejectionComesFromOwnershipGate`) and in the phase-1 commit, which
records the journal entry showing an authenticated non-owner blocked by policy
rather than by an earlier gate.

## Result — 2026-08-01

**Live owner-authorized enforcement verified.** Journal entry 1015 on
registry.oath-lang.org:

```
status         accepted
author_pubkey  65ea5701d92e420a…        authentication + signature succeeded
parent_rev     1                        the signed revision persisted
transition     unchanged
envelope       oath-publish/2, 7 lines, license=Apache-2.0
```

Ownership permitted the update, the envelope bytes persisted exactly, and the
custody check over the live journal passed against the pre-pilot baseline: every
entry re-encodes to itself, the chain is intact, and history is unchanged.

**The name did NOT move, and that is the correct outcome.** The publication
re-published identical content, so the transition is `unchanged`. Claiming the
name moved would overstate what happened — a name moves when the artifact
changes, and this pilot deliberately changed nothing but the authority governing
it.

**Census before/after — exactly one name changed:**

```
- pow   admin           legacy-label   (none)   allow
+ pow   65ea5701d92e…   signed-adoption  pow    allow
+         ↳ OVERRIDES historical owner admin (legacy-label)
```

183 → 182 label-owned, 0 → 1 signed-adoption. No unrelated name changed its
effective policy or enforcement.

**Negative result, stated precisely:** non-owner rejection was verified end to
end in the local two-authorized-principal environment (phase 1: an authenticated,
correctly signed non-owner was blocked by the ownership gate, journalled, and the
name did not move). It was **not exercised live**, because the registry has only
one authorized principal — a non-owner would be refused at authentication, which
proves a different property.

### One thing the pilot surfaced

The census reports `[FAIL] no configured scope shadows a historical owner (1
shadowing)`. That is working as designed — its rationale says "an override is
legitimate but must be deliberate", and each override is listed with `↳
OVERRIDES`. But it means the census can never be all-PASS once ANY ownership is
configured, which is the normal end state. A permanent FAIL on intended
behaviour is the same "cries wolf" failure the publish client had, where a policy
refusal rendered as apparent tampering. It wants a distinct REVIEW level rather
than FAIL. Not changed here: the pilot should not also redesign the check that
audits it.

The go/no-go check is #4 — *every name remains publishable by its last
publisher* — and it passed with 0 names blocked, both before and after.
