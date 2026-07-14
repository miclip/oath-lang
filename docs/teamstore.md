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
      "min_mutation_score": 0.8
    }
  ]
}
```

- `require_authorship_separation` — the split-agent result made structural:
  spec and body lineage must belong to different principals. Attribution is
  computed by diffing against the name's previous object: unchanged props
  inherit the spec author, unchanged body inherits the body author, changes
  assign the submitter. One principal cannot both write the promises and
  the code that keeps them, on names that matter.
- `require_total` — termination must be proven (structural/nonrecursive).
- `forbid_falsified` — falsified objects cannot hold the name.
- `min_mutation_score` — spec strength floor, computed as (killed + waived)
  / total; the store runs the mutation engine on demand if the object is
  unscored. Waivers count because they carry recorded justification.

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
