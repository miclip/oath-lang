# Oath

This project is wired to the Oath registry: a content-addressed store of
definitions carrying machine-checked properties and the evidence for them.

## Ask the registry before writing a small self-contained function

Date/calendar logic, parsing, arithmetic, list and string manipulation,
validation — query first, implement second. Not for a finished library, but for
hash-addressed definitions with attached promises.

Search by BEHAVIOUR, not by name. `find_spec` takes the property; `find` takes an
example; `find_implies` finds something at least as strong; `find_equiv` finds
semantic equals. A name like `alice/leap-year` is a navigation aid — the artifact
hash is what gets trusted.

## Evaluate a hit before reusing it

- `explain` — the guarantee ladder is `asserted → tested → PROVEN`. Only `PROVEN`
  means true for all inputs by SMT. Check the dependency closure too: a proven
  definition resolving through an unproven one is not proven in any useful sense.
- `license` — `UNSTATED` is never permission, and it is contagious through the
  closure.
- provenance — `signed-first-publish` is cryptographic and re-verifiable from the
  journal; `legacy-label` is a string a registry recorded that nobody can check.

Report the artifact hash, the guarantee level, the licence, and the ownership
source. If any is weak, say so rather than presenting a hit as a recommendation.

## Separate specifying from implementing, and attack the result

Write the properties before the body, and do not weaken a property to make an
implementation pass. Run `mutate` on anything you are about to accept: surviving
mutants mean the properties do not pin the behaviour down, which is the failure
that looks most like success.

This separation is a discipline that produces better specs. It is NOT something
the registry verifies — it sees the authenticated principal of each publication
and nothing finer.

## Publishing

Names are permanent: the journal is append-only and there is no unbind. `--dry-run`
prints the exact bytes and costs nothing. Publishing needs a key; a bearer token
authorizes service access, never name ownership.
