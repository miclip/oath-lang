# The Oath Authority Model

This paper explains why authority in an Oath registry looks the way it does. It
is not the specification — `docs/SPEC.md` §8.6 and §8.7 say what an
implementation must do — and it is not a tutorial. It is the rationale a person
building a second registry, or auditing this one, needs in order to understand
which invariants are load-bearing and why the obvious alternatives were rejected.

## The problem

A registry has exactly one mutable thing. Objects are content-addressed, so an
artifact's identity is a fact about its bytes and cannot be argued with. Names
are not: a name is a pointer, and pointers move.

Every question about authority is therefore a question about *who may move which
pointer*. There are only two shapes:

- **who may move THIS name?** — exact-name ownership
- **who may create and move names UNDER THIS PREFIX?** — prefix authority

These look like the same question and are not, and conflating them is the central
mistake the model exists to avoid.

## How many namespaces one key may hold

Five.

Namespaces nest — a prefix you hold covers everything beneath it, so
`yours/calendar/*` needs no separate claim — which makes a sixth top-level
reservation almost always a mistake rather than a need.

**This is a mistake guard, not a squatting defence.** Keys are free, so a
determined party generates more of them and the cap does nothing. It stops one key
accumulating ground it will never use, and reservations being permanent is what
makes that worth stopping. Anything stronger would have to make identity or
reservation cost something, and no such mechanism exists here.

## Advice about an irreversible act must come from the state that act will use

Reservation is permanent: no transfer, no release, no expiry. So a tool that tells
you whether a prefix is free is not an informational convenience — it is the last
check before the one act nothing undoes.

The rule that follows:

> Reservation advice MUST NOT be given unless the authority state being reported
> is the same authority state a subsequent reservation would be evaluated against.

Not "should be fresh". The same state, resolved the same way — because a reading
taken from somewhere else is not a stale answer to the question asked, it is a
confident answer to a different one. A local store knows nothing of a registry's
reservations, so it reports every prefix on that registry as unclaimed, including
the ones somebody holds.

Two consequences, and both are the point:

- reading a non-authoritative view is allowed, but it must be **labelled** as one
  and must offer **no** advice;
- reading the authoritative view is what makes the advice mean anything, so the
  check must consult the same registry the claim would be submitted to.

This generalises past reservation. It is the same discipline that makes a receipt
read the live registry rather than the publication plan, makes authority derive
from the journal rather than cached state, and makes replay resistance count
accepted history rather than current intent. Wherever an irreversible act is being
advised, the advice and the act must consult one evidence base.

## Two questions, two mechanisms

**Exact-name ownership is inferred.** The first principal to bind `alice/foo`
owns it. This is safe to infer because binding the name *is* the act — nothing is
claimed that the publication did not already do.

**Prefix authority is declared.** Nothing anyone publishes earns it. Publishing
`alice/foo` does not make you the owner of `alice/*`, because if it did, one
publication would silently capture every name under a prefix — including names
its publisher never considered, and names other people might later want. The
capture would be invisible at the moment it happened and unappealable afterwards.

So prefix authority comes only from an explicit, signed act: a **reservation**.

> **Prefix authority is never inferred from use. It is established by an explicit
> signed act.**

This is the single sentence the rest of the model follows from.

## Authority is a principal

The value a reservation claims — and the value a later reservation must match to
be accepted — is a **public key**: the key currently governing the prefix.

This follows a division the whole system already makes. **Facts are identified by
hashes; claims and authority are identified by principals.** An artifact is a
fact, so it is named by the hash of its bytes. A reservation is an act performed
*by someone*, so what it compares against is the someone.

Making authority a hash — of the prior reservation record, say — is the obvious
alternative, and it fails immediately: one authority may issue many reservations,
so a further rule would be needed to say *which* hash, and that rule would carry
no meaning the principal does not already carry. Worse, it would put a
registry-authored record between a principal and its own authority, when the
whole point is that authority rests on a signature.

## Authority revision versions the authority state

Each prefix carries a **revision**: a non-negative integer, `0` for a prefix no
key has ever held. A reservation states the `(authority, authority_rev)` it
expects to find, and is refused if the registry has moved on.

The pair is a compare-and-swap. It exists so that a reservation signed against
one state cannot be applied against a different one — a defence against replaying
an old, validly-signed statement into a world it was not written for.

The revision is what makes the swap meaningful. If acceptance did not advance it,
the pair could not distinguish the state before an accepted reservation from the
state after, which is the only thing it exists to do. An accepted reservation
carrying revision `n` therefore takes the prefix to revision `n + 1`.

Stated as a destination rather than an increment, deliberately. Two reservations
may both be accepted carrying the same `n` where a registry's check and append
are not one atomic operation; an increment rule would then demand `n + 2` while
the state is `n + 1`. The protocol cares what the resulting state *is*, not how a
particular implementation arrived at it.

An authority revision counts transitions of a *prefix's governance*. It is
unrelated to the revision of any name beneath it — those count publications, a
different thing about a different object. A thousand publications under `alice/*`
leave its authority revision untouched.

## Authority is replayed, never stored

The current holder of a prefix is **derived by replaying the journal**, not read
from a table.

A stored ownership table is a summary, and a summary can drift from the history
it summarises. Worse, it is a thing the registry authors — so a consumer trusting
it is trusting the registry rather than checking it, which defeats the purpose of
signing anything. The journal is what a third party actually holds, so the
journal is what authority is computed from. Two parties with the same journal
compute the same holder.

Replay counts an entry only if all three of the following hold: its signed
statement parses as a reservation, that signature verifies, and the registry
recorded it as accepted.

The status test is a **whitelist**, and this matters more than it looks.
Excluding a list of known rejection words and counting the remainder *fails
open*: a status the implementation has never seen would silently grant authority.
The set of statuses a registry may record is open-ended, so the only safe test is
membership of the accepting set.

Note also what replay does **not** consult: the entry's `kind`, its recorded
author field, or any other value the registry wrote and nobody signed. A
mislabelled entry must not be able to decide who governs a namespace. Only the
signed envelope says what a statement is.

## Order is what the registry recorded

Two reservations of the same prefix can both be accepted where the check and the
append are not atomic. Both then carry revision `0`, and "highest revision" is not
an order — it ties.

The tie is broken by **which came first in the registry's serialization of
accepted authority transitions**, and a verifier reads that as the order of
entries in the journal. The journal is the registry's record of what it committed
and in what sequence.

The distinction between "committed order" and "order of entries" constrains the
*writer*, not the reader: a registry that replicates, shards, or runs its journal
transactionally must record entries in the order it committed them. A verifier
never reconstructs a commit order — it could not, since nothing in the record
carries one separately — it reads the order it was given.

## Retention beats denial

A prefix may be reserved even when names beneath it are already owned by someone
else. Those names stay with their owners; the reservation is prospective.

The alternative — refusing a reservation if any name beneath it is already owned
— is the intuitive rule and it makes every namespace **deniable**. Anyone willing
to publish a single name into `alice/*` could block Alice from ever reserving it,
permanently and with no recourse. That is not a hypothetical: it is the cheapest
possible attack on the mechanism.

Retention opens no such attack. It also preserves a historical fact instead of
seizing it, and it is nothing more than most-specific-wins applied consistently:
an exact name is more specific than a prefix, so its owner keeps it.

The promise has to hold at *binding* time as well as at reservation time. A
prefix holder may not bind a name beneath their prefix that somebody else owns —
a promise kept only when the reservation is made and broken at every subsequent
publication is not a promise.

## Authority governs publication

Everything above establishes who holds a prefix. This is what holding it *does*:
where a reservation governs a name, the registry refuses to bind that name on
behalf of anyone but the holder.

Without this the model would be inert — a registry could accept every
reservation, resolve every name correctly, and let authority govern nothing.

Two constraints on that refusal. It does not extend beyond the prefix: holding
`alice/*` says nothing about any other name. And it does not depend on operator
configuration — a reservation is a record in the journal, and a registry that
enforced it only when separately configured would leave a publisher's namespace
unprotected precisely on the registries where no operator has acted on their
behalf, which is the situation the mechanism exists for.

## Protocol roots

Two first segments are not reservable: `key` and `sys`. Their meaning is assigned
by the kernel and no publisher should govern them.

The test is **intrinsic to the protocol, not merely important to it**. A
namespace holding a standard library is important; it is not intrinsic, because
it has a governing party, a history, and eventually delegations. Making such a
namespace unreservable would permanently prevent the protocol from representing
who governs it. So `oath/*` is an ordinary first-come prefix that the project
reserves like any other publisher.

Two consequences worth stating plainly. A protocol root constrains *reservation*
and nothing else — a name like `key/foo` retains its ownership and history
unchanged, because unreservable is not forbidden. And the root set is fixed by
the implementation rather than operator-configurable, since an editable reserved
list would reintroduce exactly the allocation power reservation removes.

## Reservation is not endorsement

A reservation of `openai/*` establishes one thing: that a key won this registry's
first-come rule for the octets `openai`. It is not identity, affiliation, or
endorsement, and no implementation may present it as such.

This is what makes first-come acceptable. Names are allocated to whoever asks
first, which permits squatting — and squatting here is an inconvenience rather
than a trust failure, because holding a string confers no ability to impersonate
anyone. Every artifact is signed, every publication is attributable, and every
authority chain is public. The property is the same one `github.com/<name>` has.

The alternative — allocating attractive names by verification or adjudication —
would be a second allocation mechanism sitting outside the protocol, operating on
judgement rather than signatures, and it would put the registry operator back in
the middle of exactly the flow that reservation exists to remove them from.

## What is deliberately absent

- **Transfer.** A held prefix cannot change hands. A reservation naming a prefix
  another key holds is refused, not treated as a transfer.
- **Transfer only.** Delegation now exists (SPEC §8.7.7): a holder may grant
  another key permission to publish under their prefix, and withdraw it. What
  remains absent is moving AUTHORITY itself.
- **Release and expiry.** A prefix cannot be relinquished.

These are omissions, not oversights, and the encoding anticipates them: a
reservation names an operation and states the authority state it replaces, so
transfer and delegation can arrive as additional signed operations without a
format change. That is the point of building the compare-and-swap now.

They must arrive as *signed protocol operations*, never as registry
configuration. The moment authority can change by an operator editing a file, the
guarantee collapses to "trust this registry" — and every property above exists to
avoid needing that.

## Custody is a deployment choice, not a protocol requirement

Nothing above says where a private key lives. The protocol requires that a
statement be signed by the key it names; it is indifferent to whether that key
sits in a file, a hardware token, or a managed signing service.

That indifference is deliberate and worth stating, because the reference
deployment made a specific choice and a reader should not mistake it for a rule.
The `oath/*` authority key is held in a cloud KMS and is non-exportable; the
client reaches it through a signer interface with a local-file implementation and
a managed-service implementation behind it. A registry operator who keeps their
key in a file is running the same protocol, and their signatures are neither
weaker nor differently verified.

What the protocol DOES fix is the shape of the evidence: canonical bytes, a
signature over exactly those bytes, and a public key that a verifier can check
without asking anyone. A custody arrangement can make a key harder to steal. It
cannot make a signature mean more.

## A worked example: the first standard-library slice

`oath/*` is held by a project key, established by a signed reservation, and holds
four names published in dependency order:

```
oath/List      oath/length      oath/append      oath/reverse
```

Every property this paper describes is observable in that slice:

- **identity is unchanged by naming.** `oath/reverse` is artifact
  `7bb6285884d0…` — the same artifact the bare name `reverse` has always had.
  Publishing it under a new name created a new binding, not a new object,
  because references are by hash.
- **exact-name ownership and prefix authority agree**, and are separately
  derivable: the first accepted publication of each name names the project key,
  and the reservation of `oath/*` names the same key. Neither was inferred from
  the other.
- **the closure is complete.** `reverse` needs `append`, which needs `length`,
  which needs `List`. Nothing in the slice resolves through an artifact outside
  it.
- **terms are asserted, not inherited.** Each publication carries `Apache-2.0`
  because the publisher signed for it, not because the artifact carries a licence
  — artifacts do not.
- **a third party can re-derive all of it** from the journal, without trusting
  the registry that served it.

## What a third party can check

Given only the journal, and no trust in the registry that produced it:

| claim | how it is checked |
|---|---|
| who governs `alice/*` | replay the journal; verify each reservation's signature |
| that the holder claimed it legitimately | the claim names the state it replaced, and that state is derivable |
| who owns `alice/foo` | the first accepted publication that bound it, and the key its envelope names |
| that a publication was authorised | the publisher is the exact-name owner, or the prefix holder |
| that the registry did not invent any of it | nothing above reads a value the registry authored and nobody signed |

The last row is the one to hold onto. Every input to every answer is either a
signed statement or something derived from signed statements. The registry's own
records — the `kind` of an entry, the author it noted down, the summaries it
keeps — are conveniences for reading, never inputs to authority.

## See also

- `docs/SPEC.md` §8.7 — normative reservation and prefix authority
- `docs/SPEC.md` §8.6 — signed publication, which exact-name ownership rests on
- `docs/licensing.md` — the other evidence domain built on signed publication
- `docs/registry-auth.md` — how a principal authenticates to a registry, which is
  a separate question from what that principal is authorised to do
