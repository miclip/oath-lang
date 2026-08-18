# Scoring key — written BEFORE the reports arrive

Mapping from TASK.md's numbering to the baseline record's, with the artifact
each intent has. Scored the same way the baseline scored: SATISFIED means a
mode REPORTED the artifact as satisfying the query, and the subject cites it.

| TASK # | baseline # | artifact | baseline verdict | oracle re-run |
|---|---|---|---|---|
| 1 | 1  | config-missing            | not satisfied | SATISFIED (return axis) |
| 2 | 2  | header-or                 | SATISFIED     | SATISFIED |
| 3 | 3a | json-string-value         | not satisfied | not satisfied (fragment) |
| 4 | 6  | record-field              | not satisfied | not satisfied (fragment) |
| 5 | 9  | media-type-is / path-is   | SATISFIED     | SATISFIED |
| 6 | 11 | any                       | not satisfied | SATISFIED (abstraction axis) |
| 7 | 12 | take-while (filter also proves the law) | not satisfied | SATISFIED (polymorphism axis) |

BASELINE TO BEAT: 2 of 7.   ORACLE RUN: 5 of 7.

## Rules, fixed in advance

- Denominator is 7, matching the baseline. Intents 3 and 4 count as NOT
  satisfied even when the subject correctly reports "none found" — that is the
  right answer and it is not a find. Their correct-negative rate is reported
  separately, because a subject that hallucinates a hit there is worse than one
  that does not.
- A subject naming the artifact WITHOUT quoting a mode that said so is not
  scored SATISFIED. TASK.md asks for the quote for this reason.
- `filter` for intent 7 counts: it provably satisfies the stated law, and the
  oracle run recorded both.
- Reaching a target only via `--details` on a NO VERDICT is SURFACED, not
  SATISFIED — the baseline's distinction, unchanged.
- A subject who reads outside its directory voids its own run. Both were told
  not to and both were asked to report what context they received.

## What each outcome means

    both near 5/7   the procedure TRANSFERS. #175's condition is met: decline
                    the engineering, the documentation is the fix.
    both near 2/7   the procedure does NOT transfer. Knowing the axes is not
                    the same as applying them, and the machinery case stands.
    split           one reader's success is not the claim; report the spread
                    and do not average it away.

## A hazard noticed mid-run, recorded because it could have corrupted the result

`--implies` is z3-heavy and SPEC §7.2 lets a strategy be ENVIRONMENTALLY
ABORTED under load — subject 1 hit exactly that on `filter` for intent 7, and
the CLI correctly refused to call it a negative. So machine load can turn a
subject's HIT into a NO VERDICT.

That makes concurrent heavy work on this machine a way for the measurer to
perturb the measurement. No `make ci-local`, no fixture regeneration and no
second verify run while a subject is live; the gates run after the last one
reports. Cheap to honour, and invisible in the output if ignored — a corrupted
control would just look like a low score, which is the result the treatment
group's author would most like to see.
