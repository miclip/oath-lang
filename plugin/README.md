# The Oath plugin

Gives a coding assistant a way to *ask the registry before writing code*, and to
split verification work across subagents whose separation is checked rather than
merely intended.

## What it installs

**Skills** — `registry-first` (search by behaviour before implementing) and
`publishing` (namespaces, signing, permanence).

**Subagents**, which exist to produce *separation*, not just parallelism:

| agent | writes | never |
|---|---|---|
| `oath-search` | candidates + evidence | the implementation |
| `oath-properties` | the specification | the body |
| `oath-implement` | the body | the properties it satisfies |
| `oath-adversary` | falsifications | confirmations |

**MCP servers** — `oath` (local `oath serve`) and `oath-registry` (the public
registry over HTTP).

## What the split does, and what it does not

The separation improves the work: an agent that writes properties to fit a body it
already wrote will write weak properties, and an agent that wrote the code is the
worst candidate to attack it. Keeping those roles apart is worth doing.

**It is not something the registry can verify.** Being clear about this matters
more than the feature does.

The registry sees the authenticated principal of each `put` and nothing else.
Subagents inside one assistant session share that principal, so the registry sees
one submitter and cannot tell a specifier from an implementer. Authorship
separation (`attributeAuthorship`) is derived by diffing a submission against the
previous version — spec authorship is inherited when props are unchanged, body
authorship when the body is — so it distinguishes SUCCESSIVE puts by DIFFERENT
submitters, and within one session it distinguishes nothing.

To make it checkable, each agent needs its own signing key and its own submission.
Even then the property is weaker than it looks: authorship separation is
defeatable by one party holding two keys (issue #82).

So: a workflow discipline that produces better specs and harder review, not a
guarantee. The guarantees this system offers are the ones with evidence attached —
`PROVEN` properties, mutation scores, signed publications — and the adversary
agent's `mutate` run is worth more than the org chart of who ran it.

## Install

```
oath plugin install                 # Claude Code, into the current project
oath plugin install --codex         # Codex (AGENTS.md + MCP config)
oath plugin install --user          # Claude Code, for all projects
oath plugin install --dry-run       # print exactly what would be written
```

## The first move

Not "write code" — "ask the registry". Not for a finished library, but for
hash-addressed definitions carrying machine-checked promises. A name is a
navigation aid; the artifact hash is what gets trusted.
