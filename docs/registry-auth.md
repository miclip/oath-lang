# Registry auth: signed puts, keys as principals

**Status:** decision of record for the public registry (#14) and the eventual
hardening of the team store (docs/teamstore.md). NOT yet built — the current
`oath serve --http --tokens` uses bearer tokens; this pins the target so the
hosted layer is built key-first rather than retrofitted.

## The decision

**A principal is a keypair. Authorship is a signature over the object hash,
carried in the bundle. Every journal entry is signed.** Bearer tokens, if kept
at all, are a compatibility shim for the connection, never the source of
provenance.

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
3. `oath serve`: verify signatures; bind scopes to keys in the repoint policy;
   tokens become an optional transport shim.
4. `export`/`import`: carry and verify the signature end-to-end.

The verification model (re-prove locally) is unchanged — signatures add the
authorship dimension on top of the hash and the proof. The hosted infrastructure
(GCS objects, Cloud Run API, Cloud SQL name index + journal) is in
`terraform/` and is agnostic to this: it stores signed bytes and an index; the
trust lives in the signatures and the client, not the server.
