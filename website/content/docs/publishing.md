# Publishing: keys, namespaces, and the standard library

How to get a key, claim a namespace, publish under it, delegate publication to
automation, and propose an addition to the standard library.

`docs/authority.md` explains *why* the authority model looks like this. This is
the *how*. Where they disagree, the specification (`docs/SPEC.md` §8.6, §8.7)
wins.

## One thing to know before you start

**Names are permanent.** The journal is append-only and there is no unbind
operation. You can repoint a name to different content; you cannot make it stop
existing. There is also no transfer of a reserved namespace, and no release.

So a publication is not like a commit. A bad commit can be reverted; a bound
name is there for good. `--dry-run` costs nothing and prints the exact bytes that
would be signed — use it.

## 1. Get a key

```
oath keygen alice
```

Writes `alice.key` (private, mode 0600 — keep it) and `alice.pub`. The public key
IS your identity: there is no account, no registration, and nothing to sign up
for. A registry knows you by your key and by nothing else.

Back it up somewhere you would trust with a password manager entry. There is no
recovery mechanism — the protocol has no way to prove you used to hold a key you
have lost, and no way to move a namespace to a new one.

Point the tools at it once and forget about it:

```
oath config key=~/.oath/keys/alice.key
oath config registry=https://registry.oath-lang.org
```

## 2. Reserve a namespace

Publishing a name does **not** give you the prefix. That is deliberate — one
publication must not silently capture a whole namespace — so a prefix is claimed
by an explicit signed act:

```
oath reserve 'alice/*' --dry-run     # shows the exact bytes, sends nothing
oath reserve 'alice/*'
```

First come, first served — and two different things are called "reserved" here,
which is worth separating before you try one:

| | |
|---|---|
| **protocol roots** — `key/*`, `sys/*` | unclaimable by anyone, ever. Their meaning is assigned by the kernel and no publisher governs them. |
| **already held** — `oath/*`, and others | ordinary prefixes somebody claimed first. `oath/*` is the project namespace, holding the standard library; it was reserved like any other, by the project key. |

So `oath reserve 'key/*'` is refused because the protocol forbids it, and
`oath reserve 'oath/*'` is refused because it is taken. Different rules, and the
error says which.

One key may hold **five** namespaces. Namespaces nest, so a prefix you already
hold covers everything under it and a sixth claim is rarely what you want.

Everything else is available if nobody holds it. `oath authority '<prefix>/*'`
tells you before you sign anything — and it answers from the registry you would
be reserving on, not from your local store, because advice about a permanent act
is worthless unless it comes from the state that act will be judged against.

That means it needs the same `--remote` and `--key` you would publish with. If it
cannot read the registry, it says so, shows the local view under a NOT
AUTHORITATIVE banner, and gives no advice at all — a prefix you hold looks
completely free from a local store, and "free" is exactly the answer that costs
you a namespace.

What a reservation gives you: only you may bind names under `alice/`. What it
does not give you:

- **it is not identity or endorsement.** Holding `openai/*` proves you won a
  first-come rule for a string, nothing more, and no tool will present it
  otherwise;
- **it does not take names that already exist.** If someone published
  `alice/thing` before you reserved `alice/*`, it stays theirs. You govern
  everything else, including every name not yet published. The reservation output
  lists anything retained this way, so you learn it immediately.

## 3. Publish

```
oath publish --license Apache-2.0 alice/sort.oath --dry-run
oath publish --license Apache-2.0 alice/sort.oath
```

One definition per publication — a single signature must not cover several
independent name transitions — and dependencies must be published before the
things that use them.

The licence is an **assertion you sign**, not something the registry checks.
Omitting it records the explicit "said nothing" sentinel rather than an empty
field, and `UNSTATED` is contagious: one unlicensed dependency makes an entire
composition's terms unknown. See `docs/licensing.md`.

Before it sends anything, the client prints the exact octets, signs those bytes,
and then confirms the registry persisted the same bytes it signed.

## 4. Delegate publication to automation

Do not put your namespace key in CI. Delegate instead:

```
oath delegate 'alice/*' --to <ci-public-key>
oath revoke   'alice/*' --from <ci-public-key>
```

A delegate may bind names under your prefix and nothing else. It cannot reserve,
cannot delegate onward, cannot revoke, and cannot touch any other prefix. You may
withdraw it at any time, and after revocation it keeps nothing — including names
it published while delegated, which return to your control. Its authorship of
those publications remains in the journal, because revocation changes who
controls a name and never who wrote it.

That asymmetry is the point. A stolen CI credential can publish under your
namespace until you revoke it. A stolen namespace key is permanent.

**Revocation is durable.** A grant states the delegation revision it replaces, so
a grant signed before a revocation cannot be resubmitted after it — the old bytes
are refused as stale, and the refusal is journaled rather than discarded. Without
that, revoking would only last until someone re-ran a deploy holding an old
envelope. `oath authority '<prefix>/*'` reports the current revision.

## 4b. Hand a namespace over

```
oath transfer 'alice/*' --to <pubkey> --recipient-key their.key --dry-run
```

Both parties sign the same statement — you authorize the handover, they accept
custody. A namespace cannot be pushed onto a key that did not countersign, because
custody carries obligations.

An accepted transfer clears **every** delegation under the prefix: the recipient
does not inherit publishers they never authorized, and must re-grant any that
should continue. Names already bound beneath the prefix stay with their owners,
and authorship never changes.

Use it to consolidate namespaces, move from a personal key to an organisation key,
or complete a negotiated handover. **It is not recovery** — it needs the current
holder's signature, so a lost key is still terminal.

**Each signed attempt is single-use.** Every transfer carries a random `attempt`
nonce covered by both signatures, and once the registry records the attempt —
accepted or refused — those bytes are spent. A transfer refused because the
recipient was at their namespace limit does not quietly become effective a month
later when the limit frees; both parties sign again. Retrying is normal and costs
nothing: `oath transfer` generates a fresh nonce every time.

One limit worth stating: this stops a refused transfer coming back to life. It
does not let you take back a countersignature the other party has not yet
submitted.

## 5. Propose an addition to the standard library

`oath/*` is the project namespace. Its contents are governed by
`stdlib/oath-stdlib.json` in this repository, and membership is decided by pull
request.

**Membership is not the same as being published.** Anything under `oath/` that
the manifest does not export is not part of the library, and the manifest is what
a consumer should read.

Add an entry and open a PR:

```json
{
  "name": "fast-sort",
  "artifact": "…",
  "publication": "…",
  "membership": "referenced",
  "export": true
}
```

Two membership modes. `referenced` is the DEFAULT and the normal path — not a
lesser form of membership.

A contributor is saying *"I think this should be part of the Oath standard
library"*, and the project is answering *"we agree, and we recommend this exact
publication"*. That is a complete endorsement. It does not require the project to
create a second publication or to make a licensing assertion it does not own.

So the library says **"these are the functions we recommend"**, not "these are
things we authored" — which is what lets it be an ecosystem rather than a
monorepo with extra steps:

```
stdlib/
  map       → referenced           → alice/map
  queue     → referenced           → bob/queue
  reverse   → project-publication  → oath/reverse
  append    → project-publication  → oath/append
```

`project-publication` is the EXCEPTION: core kernel code, work by maintainers on
behalf of the project, or code whose author has explicitly granted the project
the right to republish under `oath/*`.

**Using the library does not require caring which is which.**

```
oath stdlib                 # what the library offers, and where each member lives
oath get stdlib/map         # follows the member wherever it is
oath stdlib map             # the provenance, when you want it
```

`oath get stdlib/map` resolves through the index without you knowing whether the
member lives under `oath/` or under its author's namespace. The distinction
matters for provenance, licensing and governance; it does not matter for use.

It stays visible on request rather than hidden — `oath stdlib <name>` reports what
was followed, including that a referenced member's terms are its publisher's and
not the project's.

One honest limit: the index is a local file and is not itself signed or
content-addressed, so resolving through it trusts whoever supplied it. Everything
it resolves TO is signed and verifiable.

That also makes the PR a much smaller ask. You are not requesting *"please
publish my code under your namespace"* — you are requesting *"please include my
publication in the curated standard library"*, and your publication, your key and
your terms stay yours.

The two modes in detail:

| mode | meaning | requires |
|---|---|---|
| `referenced` | the library DEPENDS ON your existing publication. The project asserts no licence; evaluation consumes the terms you signed. **Use this for third-party work.** | a `publication` pinning the exact signed publication by digest, and no `license` field |
| `project-publication` | the project republishes the artifact under `oath/*` under ITS OWN licence assertion | `standing` recording why the project may make that grant |

`referenced` is intended to be the normal path for outside contributors, and is
**not yet implemented — it is currently refused** (issue #107). Its mechanics do
not exist: the publish path would republish the entry under `oath/*` carrying the
project's licence, which is exactly the substitution the mode exists to prevent.

The rule it will enforce, once built:

> Curation may select another party's assertion; it must not silently replace
> that assertion with one made by the curator.

Accepting a `referenced` member will mean *"the project has reviewed and selected
this exact signed publication"* — not *"the project owns it or may relicense
it"*. No `oath/<name>` is created, no project licence is asserted, and you keep
your publication and your authorship. Removal from the library is a manifest
change, never a deletion of history.

`project-publication` requires standing evidence beyond owning the name —
cryptographic ownership of a registry name proves authority over the *name*, not
copyright or a right to relicense. If you want your work published canonically
under `oath/*`, that needs an explicit signed grant tied to the exact artifact,
the target project and the permitted terms.

Opening the PR runs a validation job that reproduces every declared artifact hash
from source, checks the membership rules, and posts the exact registry delta your
change would produce — additions, repoints, licence changes, closure changes.
**That job cannot publish**: it holds no key and can reach no registry. It
establishes VALIDATION PASSED and never PUBLICATION AUTHORIZED.

Publication happens after merge, from a protected workflow, signed by a delegated
key.

### What gets accepted

- **dependency-closed.** Every dependency of an exported name must itself be
  exported. A member that resolves through an unreviewed artifact is not a
  member.
- **explained removals and repoints.** Consumers already depend on the old
  artifact; a change without a stated reason is refused by the delta check.
- **not everything that compiles.** Some definitions in this corpus are
  deliberate exhibits — falsified, unproven, or adversarial — and are mechanically
  admissible while being wrong for a library. `export: false` with a `reason` is
  how the manifest records that.

## Which store am I looking at?

Every read command says. `oath ls`, `oath log` and `oath authority` print the
view they answered from:

```
  view: https://registry.oath-lang.org
  view: ./codebase
```

They read the registry when one is configured, and the local store when one is
not. What they will never do is accept `--remote` and quietly answer from
somewhere else — if the registry cannot be read, that is an error, not a silent
fallback. `--local` asks for the local store on purpose.

This matters more than it sounds. A local store and a registry hold genuinely
different sets, and a plausible-looking listing from the wrong one reads as data
loss: 187 names locally against 383 on the registry has already been mistaken for
a failed migration.

## See also

- `docs/authority.md` — why the model looks like this
- `docs/licensing.md` — what a licence assertion means and what it does not
- `SPEC.md` §8.6, §8.7 — the normative rules
