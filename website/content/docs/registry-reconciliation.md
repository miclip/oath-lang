# Reconciling the committed corpus with the live registry

The repository holds 187 definitions. The registry holds 383. That gap was read
once as the registry having lost a migration, and the honest answer is that
nothing was lost — the two are different objects and were never supposed to match.

| | |
|---|---|
| `codebase/` | a curated, reproducible **input set** — what builds |
| the registry | append-only **operational history** — what happened |

Making them equal would damage both. Forcing the corpus to match the registry
pollutes a reproducible input set with deployment history, namespaced aliases and
probe artifacts. Forcing the registry to match the corpus means deleting from an
append-only journal, which is not an operation that exists.

So the goal is not synchronization. It is a **declared reconciliation policy**:
every extra name is accounted for, and nothing is unaccounted.

## The policy

`registry-reconciliation.json` declares it; `scripts/check-registry-reconciliation.py`
enforces it.

> Every committed-corpus definition must be present live at an identical artifact
> hash. Every live name that is not in the corpus must fall into a declared
> category.

| category | verdict | meaning |
|---|---|---|
| corpus name absent live | **fail** | the corpus claims something the registry does not hold |
| hash mismatch | **fail** | same name, different artifact — identity moved on one side |
| alias | expected | a declared mirror prefix, resolving to the identical artifact |
| standard-library publication | expected | declared in the manifest, identical artifact |
| operational probe | review | declared and explained; depended on by nothing |
| registry-only artifact | review | declared and explained; never entered the corpus |
| **undeclared** | **fail** | nobody has said what this is |

## Where the 383 goes

```
187  corpus definitions, present live at identical artifacts
187  michael/oath/*   — a namespaced mirror of the corpus
  5  oath/*           — standard-library publications (List, abs, append, length, reverse)
  2  oath/step8-probe-*  — operational probes
  2  dbl, id-check    — registry-only artifacts
───
383
```

Zero hash mismatches. Zero corpus definitions missing. The corpus is a strict,
hash-identical **subset** of the registry, which is the relationship that was
always intended and had simply never been stated.

## The four that are not clean, stated rather than smoothed over

- **`oath/step8-probe-disposable`, `oath/step8-probe-second`** — created by an
  authority exercise, and named after a step in a conversation. That is the
  naming mistake recorded in `CLAUDE.md`: process references do not belong in a
  permanent public namespace, and `oath/*` is reserved for the standard library.
  The journal is append-only and there is no unbind, so documentation is the only
  correction available.
- **`dbl`** — journal sequence 1. The registry's first entry, authored `admin`,
  a bootstrap smoke test written before the corpus was ever pushed.
- **`id-check`** — written directly against the registry by the operator key,
  journal 823.

None of these is explained away. They are declared as what they are, with what
the journal actually shows, because the alternative is a policy that launders
history into looking deliberate.

## Why `undeclared` fails

That line is the ratchet, and it is the whole point of writing this down.

Today's unexplained names are declared *as unexplained*. History is recorded
rather than rationalised. But any NEW name arriving without a classification
fails the check — so the policy cannot quietly absorb the next probe, the next
manual test, the next artifact nobody chose to publish.

The difference between a policy and an amnesty is whether it constrains what
happens next.

## Running it

```
python3 scripts/check-registry-reconciliation.py --fetch           # reads the live store
python3 scripts/check-registry-reconciliation.py names.json        # offline
```

It is deliberately not wired into ordinary CI: it requires read access to the
production store, and a check that cannot run without credentials is a check that
fails for the wrong reason. Run it after publishing, and when the two sets look
like they have drifted.
