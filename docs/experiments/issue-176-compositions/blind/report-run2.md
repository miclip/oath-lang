# Run 2 — the subject's own words

Task: `task-run2-accept-path.md` — demand 5 at its own width ("check a handler's
accept path"). Verbatim excerpts below; **every query file the subject wrote is
committed in `queries-run2/`**, and `verify.sh` re-asks them.

## Context (asked first)

> **What I did NOT receive:** no CLAUDE.md or project instructions, no remembered
> notes, no commit summaries, no task history, no prior transcript, no hint about
> what this corpus contains, no hint about webhooks or handlers or what the
> answer would be. **I encountered no output from any other process on this
> machine.**

## Route

> **Before any `find`**, per GUIDE.md's "probing and reading the corpus is what
> finds artifacts", I ran `./oath ls` and spotted two handler-shaped names,
> `webhook` and `gh-webhook`, plus a whole `gh-*` family.

Then a signature probe at the handler's shape named `gh-webhook`. Then it wrote
the accept law from the intent — and hash-matched:

> ```
> · a-correctly-signed-push-is-accepted [tested here]  #984388904cfc
>     gh-webhook         (tested as "accepts-github-signed")
> ```
>
> That is a **content-hash MATCH**, not a signature-compatible fallback. I wrote
> the law from the intent sentence and named it differently from the target; it
> hashed identical to `gh-webhook`'s own `accepts-github-signed`.

Three matches in the run — q3, q6, q7. Its own reading was "three separate
independent-wording matches is not coincidence".

**That reading is not supported and is quoted rather than adopted.** Only q3
carries a name of the subject's own (`a-correctly-signed-push-is-accepted`); q6
and q7 reuse the target's names verbatim. And it ran `oath get gh-webhook`
before writing them, so it had read the laws it matched. Names are not part of
the hash, so a rename would not establish independence even where it occurred.
What the matches establish is that its law IS the corpus's law — the property it
hands back is the one `gh-webhook` is verified against, not a variant.

## Conclusion, verbatim

> ```
> (prop a-correctly-signed-push-is-accepted [(sink (-> Str Str)) (body (List Int))]
>   (if (bytes-ok body)
>     (let (rq Request (gh-request (gh-sign gh-spec-secret body)
>                                  "push" "/hook" "application/json" body))
>       (match (sink (gh-record rq))
>         ((SNil)
>          (== (match (gh-webhook (caps-with sink gh-spec-secret) rq) ((Resp s h b) s)) 500))
>         ((SCons c cs)
>          (== (match (gh-webhook (caps-with sink gh-spec-secret) rq) ((Resp s h b) s)) 202))))
>     true))
> ```

All four of demand 5's definitions — `gh-spec-secret`, `gh-sign`, `gh-request`,
`caps-with` — plus `gh-webhook`, `gh-record` and the `bytes-ok` guard.

## The trap signal — ASKED FOR by the task, answered with evidence

The task asked for this in those words — "anything I must be careful about so
the property is not silently exercising a reject path while appearing to pass"
— so it is not evidence of unprompted insight, and the pre-dispatch key said as
much ("recorded, not scored, because the task hints at it").

What is worth recording is that it DEMONSTRATED rather than asserted: it wrote the NAIVE law first (flat `202`) and
showed it hash-matches nothing —

> ```
> · a-signed-push-is-always-202 [tested here]  #afd64289f517
>     no definition states this law as written
> ```

— then the two traps, each confirmed by a content-hash match against a law
`gh-webhook` states itself:

> **`x-github-event` must not be `"ping"`** — a ping short-circuits to **200**
> and never calls `emit`. **This is the sharpest trap**: 200 looks like success.
>
> **`emit` must return a non-empty `Str`** — if the sink returns `SNil`, the
> accept path lands on **500**, not 202. … This is why the law `match`es on
> `(sink (gh-record rq))` … rather than asserting 202 flatly.

It listed all eight guards in body order with the status each returns (405, 500,
401, 401, 404, 415, 200-on-ping, 500-on-empty-sink), from reading the body.

## A finding about the corpus, not about discovery

> `oath mutate gh-webhook` = **203/208, 5 survivors**, three of which are
> `literal 45 → 46 / 44 / 0`. Byte 45 is `"-"`, the `header-or` fallback for
> `x-github-event`. Because `gh-request` *always* supplies an event header, the
> property family never exercises "no `x-github-event` header at all". If that
> case matters to you, `gh-request` cannot construct it — you would have to
> build the `Req` by hand.

Verified here against the committed store: 203/208 (98%), and `gh-webhook`
states 14 properties including the three matched above.

## Its own limits, which the record adopts

> **`--implies` cannot prove any of this.** `hmac-sha256` is a trusted crypto
> primitive outside the provable fragment … Every one of `gh-webhook`'s 14
> properties is `tested (200 cases per property)`, never proven … spec and body
> share one author (`claude-main`), so the spec was not written independently of
> the code.

And on the second handler:

> `webhook` … has **no** `gh-request`/`gh-sign`-style constructors — the law
> inlines a raw `Req`. Its spec strength is `84/176`, far weaker than
> `gh-webhook`'s 203/208. I read this rather than proving it.
