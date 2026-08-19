# How the demand-5 blind runs were dispatched

## The export

Each subject got its own copy of a directory containing ONLY:

    oath           the CLI, built from the commit under test
    codebase/      a copy of the committed corpus
    TASK.md        the run's task file, in this directory
    GUIDE.md       see below

No `.git`, no `docs/`, no `CLAUDE.md`, no `apps/`, no `examples/`.

## The guide is NOT duplicated here

It is `docs/discovery.md`'s section "When a query returns nothing", VERBATIM,
prefixed with the four modes and the query-file syntax so a query is writable at
all. Copying it into this directory would create a second copy that drifts from
the page under test — the thing being measured is the SHIPPED page, so the page
is the authority and this file points at it rather than restating it.

**No redaction was needed, and that is the improvement over #175's run.** That
export had to withhold the worked examples, because they name four of the seven
targets. The section names none of demand 5's artifacts — checked by grep for
`gh-request`, `gh-sign`, `gh-spec-secret`, `Request` — so the real page could be
shipped intact.

## Preflight

Task and guide grepped for `gh-request`, `gh-sign`, `gh-spec-secret`,
`hmac-sha256`, `gh-webhook`, `gh-record`, `caps-with`. Zero hits in both.

## The dispatch prompt

Names the directory and the two files, says the work is hands-on, warns that
some queries are slow, and asks the context question. Nothing about shapes,
composition, the corpus, or what the answer might be.

## The dispatch target

The read-only search agent, which does not inherit this project's instructions,
memory index, or commit digest.

## Runs

- **run 1** — `task-run1-signed-request.md`, scored by `scoring-key-run1.md`
  (written before dispatch). **Its task is NARROWER than demand 5**: it asks for
  a valid signed request, where the demand's own want is to check a handler's
  ACCEPT PATH, whose recorded workaround is four definitions. Review caught
  this; the record says so, and run 2 exists because of it.
- **run 2** — `task-run2-accept-path.md`, scored by `scoring-key-run2.md`. The
  demand at its own width.
