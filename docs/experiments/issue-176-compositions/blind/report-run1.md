# Run 1 — the subject's own words

Task: `task-run1-signed-request.md`. Verbatim excerpts below; **every query file the subject wrote is committed in
`queries-run1/`**, and `verify.sh` re-asks them so the route and the claimed
content-hash matches can be re-derived rather than read.

## Context (asked first)

> **I was given no project-specific context, notes, or history about this corpus
> before reading `TASK.md`.** … A list of available skills and deferred tool
> names. That list happens to include MCP servers named `mcp__oath__*` … so the
> *word* "oath" and the shape of its verbs were visible to me before I read
> `TASK.md`. I did not call any of them …
>
> No remembered notes, no CLAUDE.md, no commit summaries, no prior task history,
> no hints about what is in the corpus. **I did not encounter any output from
> other processes on this machine.**

## Route

> I began with `oath ls` and `oath get` (the GUIDE's stated first move), which
> surfaced the `gh-*` cluster.

(That attribution is wrong about the page: it leads with the signature probe and
introduces enumeration afterwards. Recorded as the subject wrote it.)

Signature probes then pinned the shapes:

    q1  (-> Str (List Int) Str)                        -> gh-sign
    q3  (-> Str Str Str Str (List Int) Request)        -> gh-request
    q2  (-> Str (List Int) Request)                    -> nothing

## Conclusion, verbatim

> ```
> (gh-request (gh-sign secret body) "push" "/hook" "application/json" body)
> ```

and, unprompted, the fuller call it feeds:

> ```
> (match (gh-webhook (caps-with sink secret)
>          (gh-request (gh-sign secret body) "push" "/hook" "application/json" body))
>   ((Resp code hs bd) code))
> ```

## What it could and could not get a mode to say

> That the *composition* is accepted end to end: **no mode said so.** `--implies`
> structurally cannot (q4/q5 — `hmac-sha256` and `lam` are outside the fragment)
> and my `--spec` wordings did not hash-match. I established it two other ways
> instead, and I want the distinction on the record: 1. **I read it.** … 2. **I
> ran it**, with a secret of my own rather than the corpus's, via `oath eval`

| its `oath eval` controls | result |
|---|---|
| signed, `"push"`, `/hook`, `application/json` | `202` |
| body tampered after signing | `401` |
| signature not produced by `gh-sign` | `401` |
| secret `"short"` (fails `secret-is-usable`) | `500` |
| event `"ping"` | `200` |

## Two findings it volunteered, both re-verified against the tool here

> `oath explain` is blunt: `gh-sign`'s 2 properties and `gh-request`'s 3 are
> **tested, not proven** … `gh-request` has **no mutation score at all**
> ("spec strength UNMEASURED")

and a calibration control it ran unasked:

> Worth recording: **`[tested here]` is not a pass signal.** A law that is false
> for every input prints exactly the same marker. I ran this so I would not
> misread q4/q5's `[tested here]` as evidence.

Confirmed: the marker labels the QUERY definition's own guarantee level and
prints identically whatever the law says.

## Its own caveat, which the record adopts

> `gh-sign`'s 2 properties and `gh-request`'s 3 are **tested, not proven** — 200
> generated cases, not all inputs. … You are inheriting generated-test
> confidence, not proof — appropriate for generating property inputs, but do not
> cite it as a verified signer.
