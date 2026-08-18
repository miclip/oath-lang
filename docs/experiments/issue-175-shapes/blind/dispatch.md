# How the blind run was dispatched

## The export

Each subject got its own copy of a directory containing ONLY:

    oath           the CLI, built from the commit under test
    codebase/      a copy of the committed corpus
    TASK.md        task.md in this directory
    GUIDE.md       guide-treatment.md (treatment) or guide-control.md (control)

No `.git`, no `docs/`, no `CLAUDE.md`, no `examples/`, no `apps/`. The corpus
itself necessarily contains the targets — that is what the subjects are
searching — but nothing in the export maps an INTENT to an artifact.

## Preflight

The task and guide were grepped for every target name (`config-missing`,
`header-or`, `media-type-is`, `path-is`, `take-while`, `filter`, `drop-while`,
`record-field`, `json-string-value`, `str-prefix`). Zero hits. Two words matched
as ordinary English and were inspected: "any" ("does any definition state this
law?") and "contains" ("the corpus already contains a definition"). Neither
reads as a definition name, and `contains` is not the answer to the intent it
appears in.

## Why the guide is not the shipped page

`docs/discovery.md`'s section names four of the seven targets in its worked
examples — the examples ARE these intents. Testing the shipped page against them
would hand the subject its answer. `guide-treatment.md` is the transferable
content: the three axes and the procedure, stated generically, no definition
names and no signatures.

`guide-control.md` is `guide-treatment.md` truncated at the section under test,
leaving only the four modes and the query-file syntax — enough to write a query
at all, which is the point of a control rather than an absence.

## The dispatch prompt, verbatim

Identical for all four subjects except the directory. It names the files to read
and asks the context question, and says nothing about shapes, axes, the
baseline, or which intents have artifacts.

    Work in the directory /tmp/blind175/<DIR> and nowhere else.

    Read /tmp/blind175/<DIR>/TASK.md and carry out the task it describes, in
    full. Read /tmp/blind175/<DIR>/GUIDE.md as it instructs.

    This is a hands-on task, not a search: you are expected to write query files
    with shell heredocs and run the `./oath` binary in that directory
    repeatedly, iterating until you have an answer for each of the seven
    intents. Some queries are slow; let them finish.

    Before you begin, answer this and include it at the top of your report: what
    context or background information were you given about this project BEFORE
    you read TASK.md? Be specific — list any project instructions, remembered
    notes, commit summaries, or task history you received. If you received none,
    say so explicitly.

    Then produce the report TASK.md asks for.

## The dispatch target

The read-only search agent, which does not inherit this project's instructions,
memory index, or commit digest. The general-purpose target is handed all three
and would be void by construction. Every subject was asked what it received and
answered; those answers are in the reports.

## Scoring

`scoring-key.md` was written BEFORE any report arrived — the mapping, the
denominator, and the rules. Deciding those afterwards is how a run gets scored
into agreement with its author.
