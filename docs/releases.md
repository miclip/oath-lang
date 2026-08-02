# Releases

Release notes for the Oath kernel. Each release is cut by tagging `vX.Y.Z`;
this file is the source the GitHub Release body is taken from, so the notes
a user reads on the release page and the notes in the repository are one text.

**Identity stability is stated for every release**, because it is the only
question that matters when upgrading a content-addressed system: if artifact
hashes move, everything published against the old version resolves differently.

---

## v0.10.0 — signed namespace transfer

Adds signed namespace transfer with bilateral consent, CAS-bound authority state,
delegation clearing, reservation-limit enforcement, and one-shot transfer
attempts. A refused submitted attempt cannot later become effective without fresh
signatures from both parties.

**Identity is unchanged.** 187 definitions, **zero hash changes** from v0.9.0.

### Why it exists

Two valid principals could agree on a change of authority and the protocol had no
way to express it. The rule forbidding transfer was there to stop a hostile
seizure, and it caught every consensual handover with it — consolidating
namespaces, moving from a personal key to an organisation key, a squatter who had
been persuaded. A design that refuses a transaction all parties want is wrong
independently of any adversary.

```
oath transfer 'alice/*' --to <pubkey> --recipient-key their.key --dry-run
```

### What an accepted transfer does

```
holder             A → B
authority_rev      n → n+1
delegates          cleared
delegation_rev     advances by one
exact-name owners  unchanged
authorship         unchanged
```

Both parties sign the **same** canonical statement: the holder authorises
surrender, the recipient accepts custody. A namespace cannot be pushed onto a key
that did not countersign, because custody carries obligations — it counts against
the recipient's reservation cap and confers duties over everything beneath the
prefix.

Delegations do not survive. They were granted by the old holder, and carrying them
across would give the recipient publishers it never authorised.

### One-shot attempts

Every transfer carries a random `attempt` nonce covered by both signatures, and
once the registry records the attempt — accepted **or** refused — those bytes are
spent. A transfer refused because the recipient was at their namespace limit does
not quietly become effective a month later when the limit frees; both parties sign
again. Retrying is normal and costs nothing.

The guarantee is narrow and stated as such: it stops a refused transfer coming back
to life. It does **not** let a recipient withdraw a countersignature the holder has
not yet submitted.

### Also in this release

- **Reservation limit** — one key may hold five namespaces. A mistake guard, not a
  squatting defence: keys are free.
- **Unknown CLI flags are refused.** A flag a command does not implement was
  absorbed as a positional argument, so `publish --name X` silently bound a
  different name. Every command now refuses unrecognised flags and lists the ones
  it knows.
- **`sandbox/*`** — a public namespace for protocol demonstrations and
  intentionally non-production artifacts. Not endorsement, not standard-library
  membership.

### Upgrading

Nothing to migrate. Transfer is additive, and no existing operation changes shape.

---

## v0.9.0 — durable revocation, honest views, and a frozen ownership boundary

`v0.8.0` made publishing possible. This one makes the authority around it hold up
under the things that actually go wrong: a replayed file, a stale local answer, a
token creating a name nobody owns.

**Identity is unchanged.** 187 definitions present in both releases, **zero hash
changes**. Anything published against v0.8.0 resolves identically here.

### Revocation is now durable (`oath-delegate/2`)

Delegation revocation held only until someone resubmitted a grant they still had.
Confirmed by test, not argued: delegate → revoke → replay the *original grant
bytes*, unchanged and validly signed → the delegate was re-activated. The
realistic replayer is a retry loop or a redeploy, not an attacker.

A prefix now carries two revisions, because it has two states:

| | |
|---|---|
| `authority_rev` | who **holds** the prefix |
| `delegation_rev` | who may **publish** under it |

Every accepted grant or revocation advances the delegation revision once; refused
statements do not. A grant must state the revision it replaces and is refused when
it does not match.

**`oath-delegate/1` still verifies** and is still accepted as a prefix's *first*
delegation, where it is indistinguishable from a correct `/2` at revision 0. After
any delegation event it is stale — so the old format self-deprecates rather than
being cut off, and a replay is refused for being *stale*, which is true, rather
than for being *old*, which would also refuse the one sound case.

**If you run a registry with existing delegations**, re-issue them: revoke and
re-grant under `/2`. `docs/receipts/004-delegation-rev-migration.md` is the live
migration, step by step.

### Read commands answer from the store you asked for

`oath authority` read the **local** store while a registry was configured, and
reported a prefix held on that registry as `UNCLAIMED` — then recommended the one
permanent, irreversible act in the protocol. Not stale; confidently wrong about
the only question it exists to answer.

> Reservation advice is not given unless the authority state being reported is the
> same authority state a reservation would be evaluated against.

| situation | behaviour |
|---|---|
| registry readable | authoritative → advise, labelled with the endpoint |
| registry unreadable | local view under a **NOT AUTHORITATIVE** banner, no advice, **exit 1** |
| no registry configured | local store is where a reservation would land → advise |

**Behaviour change for scripts.** `oath authority` now exits 1 when it cannot
reach authoritative state, so a caller can tell *no answer* from a *negative
answer*. Exit 0 with a printed warning would let a script reserve anyway.

`oath ls` and `oath log` now honour `--remote` instead of silently dropping it,
and print the view they answered from. Both need `--key`/`--kms-key` when a
registry is configured, since registries authenticate reads; `--local` asks for
the local store on purpose. A silent substitution is no longer possible — the
local/registry gap is real (187 names against 383) and was once read as the
registry having lost a migration.

### New names require a cryptographic principal

A bearer token could create a permanent top-level name with no key behind it. It
can no longer.

> No new name may enter a hosted registry without cryptographic ownership
> established at creation.

A token still authorizes search, evaluation, proving, and preparing a publication.
Creating a name needs a signed publication — directly, or through a key delegated
under a reserved namespace. **Tokens authorize service access; keys establish
identity and authority.** Refusals say exactly that, and how to proceed.

Local, unhosted use is untouched: `oath put` against your own store needs no key.

**Existing unowned names are preserved, not rewritten.** They stay historically
valid, owned by an unverifiable label, protected by operator policy — and they are
a *closed* set, derived from a pinned journal boundary rather than from "whatever
is currently unowned", so the category cannot silently expand. They are not
adopted retroactively: a later signed republication proves authorship of that
publication, not ownership of the original name.

### `oath plugin install`

Wires a coding assistant to the substrate — MCP servers, a registry-first skill,
and four subagents (search, properties, implement, adversary).

```
oath plugin install                 # Claude Code, current project
oath plugin install --codex         # Codex: AGENTS.md + .codex/mcp.json
oath plugin install --user          # all projects
oath plugin install --dry-run       # print exactly what would be written
```

Existing MCP configuration is **merged, never clobbered**: unrelated keys survive,
a server already configured under one of our names is left alone, and a malformed
config is refused rather than replaced.

The subagent split is a working discipline, not a checked property — the registry
sees one authenticated principal per publication and cannot distinguish a
specifier from an implementer within a session. The plugin says so.

### Corpus and registry reconciliation

The committed corpus and the live registry are different objects — a reproducible
input set against append-only operational history — and forcing them equal would
damage both. `registry-reconciliation.json` declares a policy;
`scripts/check-registry-reconciliation.py` enforces it by **classification, not
count**: every live name in exactly one declared category, every required member
present. A total-equality check passes when one legitimate name vanishes as one
unexplained name appears.

### Specification

- **`DEL-REV-DISTINCT` / `DEL-CAS`** — the delegation revision and its
  compare-and-swap.
- **`AUTH-VERSION-IS-SIGNED-DATA`** — a statement's format version is part of the
  signed octets, not ambient information from the verifier. Dispatch from the
  parsed envelope, reproduce *that* version's encoding, recognise every kind ever
  accepted. Re-encoding history under the current emitter invalidates correct
  signatures rather than rejecting bad ones.

Its corollary is worth repeating: **a compatibility corpus must contain the
history it exists to protect.** Both backward-compatibility breaks in this release
were caught against the live journal and neither against the committed fixtures,
which hold no delegations at all.

---

## v0.8.0 — publishing, namespaces, and a standard library

`v0.7.0` had no way to publish to a registry. This one does, and adds the
authority model that makes publishing to a *shared* registry meaningful.

**Identity is unchanged.** 161 definitions present in both releases, **zero hash
changes**. Anything published against v0.7.0 resolves identically here. This is
additive.

### Publishing

```
oath keygen alice                    # your key IS your identity
oath reserve 'alice/*'               # claim a namespace — first come, permanent
oath publish --license Apache-2.0 x.oath
oath authority 'alice/*'             # who holds a prefix, before you claim one
```

A publication is a signed statement. The client prints the exact octets, signs
those bytes, and confirms the registry persisted the same bytes it signed.

### Namespace authority (SPEC §8.7)

Publishing a name never grants the prefix — one publication must not silently
capture a namespace. Prefix authority comes only from an explicit signed
reservation. Names already published beneath a prefix stay with their owners.

`key/*` and `sys/*` are protocol roots and are not claimable. Everything else is
first-come.

### Delegation — automation without handing over the namespace

```
oath delegate 'alice/*' --to <ci-key>
oath revoke   'alice/*' --from <ci-key>
```

A delegate may bind names under your prefix and nothing else: no reserving, no
delegating onward, no revoking, no other prefix. Revocation returns control of
everything it published while delegated, and leaves its authorship in the journal
untouched — control moves, credit does not.

### Signing seam

`--key <file>` or `--kms-key <resource>`. Publication depends on *who signs*,
never on where the key lives, so a namespace key can be held in a managed signer
and never reach the process. The client verifies every signature locally before
anything is transmitted.

### Standard library

```
oath stdlib                 # what the library offers and where each member lives
oath get stdlib/map         # follows the member wherever it is
```

A curated index rather than a namespace. Members are either **published** by the
project or **referenced** — selected from their author's own publication, with no
republication and no project licence assertion. Consumption is uniform; provenance
is one command away.

### Licensing (SPEC §12)

Terms are asserted by a publisher and derived by a registry. `UNSTATED` is never
permission and is contagious across a dependency closure.

### Documentation

`docs/publishing.md` is the practical path from no key to library membership.
`docs/authority.md` is why the model looks like this. The full reference set now
renders from the repository markdown, specification included.

### Known limits

- no transfer, release or expiry of a reserved namespace — a prefix is permanent
- the standard-library index is a local file, not itself signed or verified
- `--remote` works on `put`, `publish`, `license`, `reserve`, `delegate`, `revoke`
  and hard-errors elsewhere rather than silently reading the local store

---

## v0.7.0 and earlier

Released before this file existed. See the [GitHub releases]
(https://github.com/miclip/oath-lang/releases) for their notes and assets.
