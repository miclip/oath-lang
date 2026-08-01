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

## Extension — 7 names under enforcement, 2026-08-01

The pilot's mechanism generalises without a namespace decision, because six names
were ALREADY cryptographically owned: they were first published on the registry
by the signed adoption campaign, so trust-on-first-publish derives their owner
from history with no configuration at all.

**What the census refused first.** Modelling `trust_on_first_publish` across
`["*"]` returned:

```
[FAIL  ] every name remains publishable by its last publisher (177 would be blocked)
ENABLING ENFORCEMENT NOW WOULD FREEZE 177 NAME(S).
```

183 legacy-label names derive their owner from an unsigned first publication —
a LABEL, which no key can present — so enabling registry-wide enforcement would
lock the current publisher out of 177 names. The stage-4 precondition exists
exactly to catch this, and it did, before anything was written.

**What is enforced now.** Scoped to the six names whose first publication was
signed, plus `pow` by explicit adoption:

| names | mechanism | source |
|---|---|---|
| `bad-reverse`, `check-config`, `config-has-key`, `config-key`, `config-missing`, `spin` | trust-on-first-publish | `signed-first-publication` |
| `pow` | configured `owner_pubkey` + corroboration | `signed-adoption` |

Census: **3 PASS, 1 REVIEW, 0 FAIL**, 0 names blocked. The positive path is
verified live — the owner published `spin` under enforcement and the custody
check passed against the prior baseline with history unchanged.

**The 182 remaining names cannot be adopted this way.** Their first publication
was unsigned, so history offers no key to derive. Each needs either an explicit
`owner_pubkey` rule or a namespace under which a prefix rule can apply — and that
is the naming decision, still open. Enforcement is deliberately NOT enabled for
them: the census would report FAIL and the registry would freeze.

## Rooted namespaces — the naming decision, 2026-08-01

Bare names were acceptable while the registry was a corpus. They stop working
once names carry durable authority, because authority then has to be assigned one
artifact at a time: workable for seven names, untenable for 182.

```
michael/*                root authority
michael/oath/*           project namespace
michael/oath/reverse     artifact name
```

**Three claims stay separate.** Root owner (who is answerable for names under the
prefix), publisher (which key signed this exact publication), artifact identity
(which immutable hash the name points to). Namespace authority and authorship are
independent: an agent key can sign publications beneath a prefix the root owns.

### Verified end to end

| check | result |
|---|---|
| root key authorizes the prefix | `michael/*` rule applies; scope shown in the census |
| an authenticated non-owner is blocked | local, two authorized keys: seq 552 `blocked`, *"policy: name is owned by key 65ea…; submitter 831e…"* |
| the root owner can publish beneath it | seq 553 `accepted` |
| sibling and legacy names unaffected | bare `reverse` stays `admin` / `legacy-label` / no scope |
| old bare names still resolve | yes, and remain honestly legacy-owned |
| discovery does not conflate the two | see below |

### One artifact, two names, two authority stories

`michael/oath/reverse` and `reverse` resolve to the SAME artifact —
`7bb6285884d0` — because the name is metadata and content addressing does not
care what a thing is called. The registry keeps their provenance separate:

| name | artifact | owner | source |
|---|---|---|---|
| `michael/oath/reverse` | `7bb6285884d0` | `65ea5701d92e…` | signed-first-publication |
| `reverse` | `7bb6285884d0` | `admin` | legacy-label |

That is what makes signed republishing the honest migration: no artifact is
duplicated, no legacy entry is altered, and the new name carries cryptographic
ownership derived from ITS OWN first publication — needing no configured rule at
all.

### What was deliberately not done

**No blanket rule assigning the key to all 182 bare names.** It would satisfy
enforcement mechanically while freezing an accidental global naming scheme into
the protocol — using ownership policy to avoid the naming decision rather than
make it. The bare names stay exactly as they are: honest, unsigned, legacy-owned
history.
