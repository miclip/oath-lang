# Releases

Release notes for the Oath kernel. Each release is cut by tagging `vX.Y.Z`;
this file is the source the GitHub Release body is taken from, so the notes
a user reads on the release page and the notes in the repository are one text.

**Identity stability is stated for every release**, because it is the only
question that matters when upgrading a content-addressed system: if artifact
hashes move, everything published against the old version resolves differently.

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
