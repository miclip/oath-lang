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
