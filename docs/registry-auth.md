# Registry auth: signed puts, keys as principals

**Status:** decision of record for the public registry (#14) and the eventual
hardening of the team store (docs/teamstore.md). NOT yet built — the current
`oath serve --http --tokens` uses bearer tokens; this pins the target so the
hosted layer is built key-first rather than retrofitted.

## The decision

**A principal is a keypair. Authorship is a signature over a canonical
publication ENVELOPE, persisted verbatim. Bearer tokens are a compatibility shim
for the connection, never the source of provenance.**

> **Amended 2026-07-30, when this was implemented (#83).** This section
> originally said authorship was "a signature over the object hash". That is
> not enough, and the correction is load-bearing: a signature over the artifact
> hash alone is valid *forever, for any name, in any operation* — a bearer
> credential wearing a signature's clothing. It cannot express "I publish THIS
> content under THIS name, replacing THIS version". The envelope (SPEC §8.6)
> binds operation, name, artifact, parent and per-name revision together, so a
> signature authorizes one state transition and no other.
>
> It also said "every journal entry is signed". That was a target, not a fact,
> and the gap is now measured rather than asserted — `oath audit` reports signed
> versus unsigned coverage, and the committed corpus is 531 unsigned entries.

## Why (and why now)

The substrate's whole promise is *trust you don't have to grant anyone*: objects
are content-addressed and re-verified on import, so the host is never a root of
trust. Bearer tokens quietly break that for the one dimension they cover —
authorship. With a token, the *server* writes `author: alice` because a shared
secret said so, so a compromised or dishonest server can forge provenance. The
journal claims "authenticated principals, tamper-evident"; the hash-chain makes
it tamper-*evident*, but token-based authorship is only tamper-evident if you
trust the writer.

A **signature** makes authorship *unforgeable and offline-verifiable*: the author
signs the canonical object hash (and the journal entry) with their private key;
anyone verifies against the public key without trusting the hub. That upgrades a
core feature from "the server says alice made this" to "alice mathematically made
this" — which is what a provenance system should have said from entry one.

Starting here is also the *cheap* moment: #14 isn't built and the team store has
no install base, so there's no token ecosystem to migrate. Retrofitting
signatures onto a token-based journal later is the expensive path, and it leaves
all historical provenance permanently weaker.

## The model

- **Key is the principal.** Identity is a public key (Ed25519). The human name
  (`alice`) is namespaced metadata *over* the key — the same move as
  content-addressing for code: the key is authoritative, the name is a label
  (see [names aren't identity](tutorial/names.md)). This dissolves key-discovery
  for the core: authorship is "signed by `pubkey X`", full stop.
- **Signed put.** A submission carries `sig = Sign(sk, objectHash)`. The gate
  accepts it only if `Verify(pk, objectHash, sig)` holds. `oath export` includes
  the signature; `oath import` checks hash + re-proves + verifies signature — all
  offline.
- **Signed journal entries.** Each `log.jsonl` entry gains a `pubkey` and a
  signature over its canonical fields (including the prior-entry hash it chains
  to). The provenance chain is now cryptographically verifiable end-to-end,
  independent of any server. `unattributed` becomes "no signature", explicitly.
- **Authorization is separate, and self-sovereign.** A signature *authenticates*
  (who); the repoint policy *authorizes* (may they move this name). Bind
  permissions to keys: a scope `@alice/*` is *owned by alice's key*; you prove
  you may move `@alice/foo` by signing. No accounts. The existing repoint policy
  (`forbid_falsified`, authorship separation, `require_total`, `min_mutation_
  score`) checks the *signing key* against the scope owner.

## Deliberately out of scope of the core

- **Reads / integrity** need no credential — content-addressing covers them (a
  byte that doesn't hash to its name is rejected regardless of signer).
- **Revocation** is not deletion (objects are immutable): publish a *signed
  revocation statement*; consumers honor it. A real design item, not a blocker.
- **Anti-spam / quota** is a resource concern (rate-limit, proof-of-work, or
  payment), orthogonal to identity.

## What this changes in the code (the build)

1. **[built]** Kernel: a `pubkey` + Ed25519 signature on the journal entry
   (`oath/store.go`); the signature covers the authored fields; `VerifyLog`
   rejects a bad or forged signature; `unattributed` = unsigned. Specified in
   SPEC §8.4.
2. **[built]** `oath` CLI: `oath keygen` generates a keypair; `oath put --key
   <file>` (or `OATH_KEY`) signs the entry and defaults the author label to the
   signer's pubkey.
3. **[built]** `oath serve`: authenticates a principal by an Ed25519 signature
   over the request body (`X-Oath-Pubkey` + `X-Oath-Signature`) — the principal
   IS the key, no shared secret. Bearer tokens still work but are now the
   optional transport shim (serve runs without a tokens file). The repoint policy
   gains `owner_pubkey`: a name only its owning key may repoint. A
   present-but-invalid signature is a hard rejection, never a fall-through.
4. `export`/`import`: carry and verify the signature end-to-end.

Related substrate (built alongside this): the **verification worker pool** —
`oath prove-worker` re-proves `require_proven` names out of band and signs the
resulting `proven` verdict, so the badge is an authenticated "the verifier
re-proved this" rather than a publisher's claim. See
`docs/registry-verification.md` and SPEC §8.5.

The verification model (re-prove locally) is unchanged — signatures add the
authorship dimension on top of the hash and the proof. The hosted infrastructure
(GCS objects, Cloud Run API, Cloud SQL name index + journal) is in
`terraform/` and is agnostic to this: it stores signed bytes and an index; the
trust lives in the signatures and the client, not the server.


## As implemented (#83): two signatures, two different claims

The working system has two independent signatures. Conflating them was the
mistake that took longest to see, because both are "a signature on a journal
entry":

| | question it answers | signer | what it cannot do |
|---|---|---|---|
| **entry signature** (SPEC §8.4) | has this record been altered since it was written? | whoever holds the store's key | establish authorship — a store can sign an entry naming anyone |
| **publication envelope** (SPEC §8.6) | who requested this exact transition? | the author, before submitting | prove the record was not later tampered with |

An author signs only what an author controls: name, artifact, parent, revision.
It MUST NOT cover `status`, `guarantee` or `termination` — those are the
registry's findings, re-derived from content bytes, and an author cannot predict
them. Requiring a signature over them would make a policy decision
indistinguishable from a forged statement.

The envelope BYTES are persisted, never reconstructed from the entry's fields.
Reconstruction would let a future encoder change silently invalidate historical
signatures: the signature stays correct and stops verifying, which is the worst
failure an audit trail has.

### The rule for tokens, unchanged and now enforced

The token says the caller may reach the registry. The signature says who
authored the publication. An author statement is only accepted on a *signed*
request — with a bearer token the principal is server-vouched, so a statement
naming a key could not be tied to the caller.

### What this does NOT establish

- **Atomic application.** The envelope expresses a compare-and-swap; the
  fs-over-gcsfuse store does not enforce it (compare and update are separate
  operations). Historical replay IS prevented; two correctly signed publications
  naming the same parent can still both verify. The transactional store is the
  prerequisite, not an optimisation.
- **Custody separation.** Distinct keys for spec and body are not evidence of
  independent authorship: one process holding both key files produces an
  identical record. `oath explain` reports
  `DISTINCT_KEYS_CUSTODY_UNVERIFIED` and keeps the limitation (#82).
