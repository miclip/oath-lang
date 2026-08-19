# Run 2 — criterion fixed BEFORE dispatch

Run 1's task asked for a signed REQUEST. Demand 5's own want is "to check a
handler's accept path", and its recorded workaround is FOUR definitions:
`gh-spec-secret`, `gh-sign`, `gh-request`, `caps-with`. Run 1 therefore tested a
subproblem, and its fuller answer was volunteered rather than asked for — which
is post-hoc and cannot close the leg. This run poses the demand at its own width.

## Scoring

    FOUND      the report shows a call to the handler that would be ACCEPTED,
               naming the signer, the request builder and the capability
               constructor, however spelled. gh-spec-secret is optional: a
               caller may supply their own secret.
    PARTIAL    reaches a signed request but not the handler call, or names the
               pieces without assembling them.
    NOT FOUND  "none found", or an assembled call that would be rejected.

A reader who reads definitions with `oath get` and reasons correctly counts as
FOUND: the question is whether a caller GETS THERE, not whether a mode emits the
chain. The ROUTE is recorded separately.

## Bonus signal, not scored

Whether the subject warns that the property can silently exercise a REJECT path
while passing. Demand 6 of the friction log is exactly that defect — a property
that passed 200 cases and was false — so a reader who raises it unprompted has
found the trap the demand exists because of. Recorded, not scored, because the
task hints at it.

> **CORRECTION, added after the run and deliberately NOT folded into the text
> above.** The cross-reference is wrong: demand 6 is a property that was FALSE
> (`gh-record`'s field count, forged by a tab). The VACUITY trap — a property
> that "still reported `passed 200 cases`" while nearly every case took the
> trivial branch — is demand 7, and it is about `accepts-github-signed`, the
> law this run's subject hash-matched.
>
> The CRITERION is unchanged; only the citation was wrong. This file is the
> pre-dispatch record, so it is annotated rather than rewritten — editing a
> pre-registration after seeing the result destroys the only property that makes
> registering it worth anything.

## What each outcome means

    FOUND      the untested leg closes. #176 can close: both compositional
               demands reachable at their own width.
    PARTIAL    the mitigation reaches the subproblem and not the demand. #176
               stays open with a precise target.
    NOT FOUND  the decline fails on its own trigger, and the case for machinery
               is measured rather than argued.
