---
name: registry-first
description: Before writing a small, self-contained function — date/calendar logic, parsing, arithmetic, list and string manipulation, validation — search the Oath registry for an existing proven artifact by BEHAVIOUR rather than by name. Use when starting any such implementation, when a helper "must already exist", or when the user asks whether something is already available.
---

# Ask the registry first

The first move on a small, self-contained function is not "write code". It is
"ask the registry".

This is not searching npm or GitHub for a finished library. The Oath registry
catalogs *smaller things*: hash-addressed definitions carrying machine-checked
promises and the evidence for them. A name like `alice/leap-year` is a
navigation aid — **the label is not the thing being trusted**. What you evaluate
and reuse is an artifact hash, its properties, and its evidence.

## Search by behaviour, not by name

A name search finds what someone happened to call a thing. A behaviour search
finds what actually satisfies the property, whatever it is called and whoever
wrote it. Prefer the latter — that is the capability the registry exists to
provide.

| tool | when |
|---|---|
| `find_spec` | you can state the property: "divisible by 4 except centuries unless divisible by 400" |
| `find` | you have an example input/output pair to match |
| `find_implies` | you need something at least as strong as a property you already hold |
| `find_equiv` | you have a body and want anything semantically equal to it |
| `ls`, `get` | you already have a name or hash |

Start from a behaviour query. Fall back to a name only when you have one.

## Then evaluate the candidate — do not just take it

A hit is a candidate, not an answer. Before recommending reuse, establish four
things, all of which are one tool call each:

1. **`explain <name>`** — what is actually proven, what is merely tested, what is
   asserted with no evidence at all. The guarantee ladder is
   `asserted → tested → PROVEN`, and only the last means "true for all inputs by
   SMT". A definition can be honestly registered and still be falsified — the
   corpus deliberately contains such exhibits.
2. **`license <name>`** — what the composition permits. `UNSTATED` is never
   permission, and it is contagious: one unlicensed dependency makes the whole
   closure's terms unknown.
3. **`explain`'s provenance** — who published it and how ownership is
   established. `signed-first-publish` is cryptographic and re-verifiable from
   the journal; `legacy-label` is a string a registry recorded and no one can
   check. Say which one you are relying on.
4. **The dependency closure.** A proven definition resolving through an
   unreviewed one is not reviewed.

## What to report back

State the artifact hash, not just the name — the name is how you found it, the
hash is what you are proposing to depend on. Then the guarantee level, the
licence, and the ownership source. If any of those is weak, say so plainly rather
than presenting a hit as a recommendation.

If nothing matches, say that too and write the implementation. "The registry has
nothing" is a real, useful answer that takes one query to obtain.

## Do not

- Do not trust a name because it looks authoritative. Holding `openai/*` means
  someone won a first-come rule for a string.
- Do not treat `tested` as `PROVEN`. Sampled properties passing 200 cases is
  evidence, not proof.
- Do not skip the licence check because the code is small.
