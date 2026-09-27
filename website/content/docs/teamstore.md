# The team store: authenticated principals and repoint policy

The hosted layer of the three-layer model (local stdio → team store →
public registry). Same kernel, same MCP tool surface, two differences that
change the trust story:

1. **Principals are authenticated.** `oath serve --http <addr> --tokens
   <file>` serves the MCP tools over HTTP (stateless streamable-HTTP: one
   JSON-RPC message per POST, `Authorization: Bearer <token>`). The journal
   author derives from the token's identity; a client-supplied `author`
   field is ignored. The server refuses to start without a tokens file — an
   unauthenticated network store would make every journal entry a lie.

   Tokens file (never commit one): `{"<token>": {"principal": "name"}}`,
   tokens ≥ 16 chars.

2. **Names move only through policy.** `<store>/policy.json` holds repoint
   rules. Storage stays unconditional past the typecheck gate — objects are
   content-addressed facts — but a submission failing policy is journaled
   `blocked` and the name keeps pointing at its previous version. Nothing
   breaks; dependents pin hashes anyway.

```json
{
  "rules": [
    {
      "names": ["sort", "max2"],
      "require_authorship_separation": true,
      "require_total": true,
      "forbid_falsified": true,
      "min_mutation_score": 0.8,
      "require_proven": true
    }
  ]
}
```

- `require_authorship_separation` — spec and body lineage must belong to
  different principals. Attribution is computed by diffing against the name's
  previous object: unchanged props inherit the spec author, unchanged body
  inherits the body author, changes assign the submitter. So ONE KEY cannot
  both write the promises and the code that keeps them, on names that matter.

  **A PRINCIPAL STRING, NOT A PARTY, AND THE DIFFERENCE IS THE WHOLE CEILING
  (#82).** The check is a comparison of two author strings, and a principal is
  whichever of two things authenticated the write:

  - under SIGNATURE auth, a keypair — so one person holding two defeats the
    check completely, and not as an exotic attack: running `oath keygen` on a
    laptop and again on a desktop produces exactly it;
  - under BEARER auth, a server-vouched LABEL from the tokens file. Two
    write-scoped tokens naming `alice` and `bob` satisfy this policy with NO
    key possession whatsoever, by anyone holding both tokens.

  So the policy does not establish distinct parties, and does not even
  establish distinct key possession — only distinct principal strings. The
  registry can verify what authenticated a write; it cannot verify who wrote
  it, and nothing in the journal distinguishes two parties from one party
  wearing two principals.

  The kernel says so rather than implying otherwise. `oath explain` derives,
  from the journal, which write ESTABLISHED each lineage — the entry where the
  props, or the body and constructors, last changed — and whether that exact
  transition carries a valid author signature (SPEC §8.6.6). It reports both as
  `spec_lineage` and `body_lineage`: `KEY_SIGNED` with the key and journal
  `seq`, or `UNKNOWN` with a named reason. `UNKNOWN` is never reported as
  unsigned, because the journal cannot tell a bearer write from a key-holder's
  write that left no envelope.

  The authorship rung follows from that. Two distinct principals whose
  lineages are each `KEY_SIGNED` by their own key reach
  `DISTINCT_KEYS_CUSTODY_UNVERIFIED`. Two bearer tokens, or any lineage still
  `UNKNOWN`, stay at `DISTINCT_PRINCIPALS_CUSTODY_UNVERIFIED`, and `explain`
  says key possession is unknown. Neither rung claims custody: one process
  holding both key files produces the distinct-keys record. The rung above,
  `SEPARATE_CUSTODY_ATTESTED`, is deliberately unreachable: no mechanism here
  earns it, so nothing emits it. A name bound through the `require_proven`
  worker stays `UNKNOWN` even when its author signed, since the worker's
  binding entry carries no envelope and nothing links it to the pending one.
  Read the policy as what it is — a real constraint on key reuse, and a
  discipline rather than a proof about people.
- `require_total` — termination must be proven (structural/nonrecursive).
- `forbid_falsified` — falsified objects cannot hold the name.
- `min_mutation_score` — spec strength floor, computed as (killed + waived)
  / total; the store runs the mutation engine on demand if the object is
  unscored. Waivers count because they carry recorded justification.
- `require_proven` — every property must be SMT-proven. Unlike the others,
  this is decided ASYNCHRONOUSLY: proving is too heavy to run inside a put,
  so the name-bind is DEFERRED (journaled `pending`) and the object is queued
  for `oath prove-worker`, which proves it out of band and binds the name once
  every property proves. See `docs/registry-verification.md` and SPEC §8.5.
- `owner_pubkey` — reserve the name to one Ed25519 key: only a put authenticated
  as that pubkey may repoint it. Combined with signature auth (below), this is
  unforgeable, self-sovereign name ownership.

## Authentication: signatures, not (just) tokens

`oath serve` authenticates a principal two ways, and a request needs one:

- **Signature (the real one).** `X-Oath-Pubkey` + `X-Oath-Signature`: an Ed25519
  signature over the raw request body. The principal is the key — unforgeable,
  offline-checkable, no shared secret, and the server holds nothing. Always on.
- **Bearer token.** A server-vouched principal — the standard MCP-over-HTTP auth,
  so off-the-shelf agent/MCP clients (Oath is a substrate *for AI to use*)
  connect the way they already do. First-class, not deprecated; only active when
  `--tokens` is given. The trade-off is honest: a token is a shared secret and
  the server vouches for it, so reserve names that matter to keys
  (`owner_pubkey`), which a token principal can't satisfy.

A present-but-invalid signature is rejected outright — it never falls through to
token auth, so a forged key can't be laundered into a token principal. An
unauthenticated store remains impossible: with no token file and no valid
signature, every request 401s. (docs/registry-auth.md)

**Registration gate (`--authorized-keys`, #66).** Optionally restrict *who may
write*: `oath serve --authorized-keys <file>` (or `OATH_AUTHORIZED_KEYS`), a JSON
array of hex pubkeys. When set, only a listed key may write — an unlisted key
still authenticates and READS (the store is public, re-verifiable), but its writes
are refused. Absent/empty = open contribution (any signer writes), the default.
This is the gate half of #66; delegated token minting (a key issuing scoped,
expiring tokens for its agents) is the other half, still open.

**Bearer tokens are capability-limited.** A token is read-only by default: it can
read, discover, and re-verify, but the state-changing tools — `put` (authors
objects, moves names) and `cross --record` — are refused unless the token is
granted `"write": true`. A signature always carries full capability (a key-holder
authors as themselves). So a leaked read-only token can browse, never corrupt;
writing needs either an explicitly write-scoped token or a signature. The tokens
file:

```json
{
  "<secret-token>": { "principal": "ci-bot", "write": true },
  "<other-token>":  { "principal": "readonly-agent" }
}
```

## Semantics worth knowing

- A blocked object is stored, verified, and addressable by hash — a later
  put of the SAME object by a principal that satisfies policy simply
  repoints (verdict metadata merges; nothing re-runs).
- Attribution inherits through `unattributed` history gracefully (falls
  back to the object's submitting author).
- The stdio transport (`oath serve` bare) is unchanged: local trust,
  self-reported author, policy still evaluated if policy.json exists —
  advisory discipline locally, enforcement on the hosted store.

## What this deliberately does not do yet

Tokens are static bearer secrets, not OAuth; there is no TLS termination
(front it with a proxy); rules match names exactly (no globs beyond `*`);
and policy is store-local — a public registry layer would need signed
verdicts and verifier trust, which is issue #14's territory.
