# #176 — how often is the answer a composition rather than a definition?

**Status: MEASURED. The recommendation is DECLINE THE MACHINERY — but the gap
#176 describes is REAL**, which is the opposite of what two earlier drafts of
this record concluded, and the corrections are kept visible below rather than
tidied away.

Two of these thirteen demands need a composition. No mode carries a caller from
the first-order intent to the higher-order artifact — measured.

**The decline's untested leg is CLOSED.** Demand 11 is reachable, and demand 5
was run twice — once at a task narrower than the demand (review caught that),
then again at the demand's own width. Both found it; the second assembled all
four of the demand's definitions.

It also warned, when asked to, about two branches that would leave a property
exercising a reject path — `ping` returning 200 and an empty sink returning 500.
**It did NOT find demand 7's own vacuity mechanism**, where the
`secret-is-usable` guard makes generated cases take the trivial branch while the
property still reports `passed 200 cases`. Those are different defects and the
distinction is kept below.

Nothing was built.

## The falsifier, quoted

> If the corpus's compositional answers are rare enough that surfacing single
> definitions is sufficient in practice, this is not worth machinery. **That is
> measurable**: over the 13 intents in `webhook-friction.md`, count how many are
> answered by a composition rather than a definition. The falsifier record found
> one clearly (intent 11). One in thirteen may not justify anything — and
> establishing that would be a legitimate decline.

## The criterion, stated first — without it the classification is arbitrary

A demand is answered by a COMPOSITION when satisfying it requires chaining more
than one corpus definition, or introducing an abstraction the intent never
mentioned. It is answered by a DEFINITION when one call does it.

The second clause is what separates demands 11 and 12, which otherwise look
alike — both end at a higher-order combinator taking a predicate:

- Demand 12 asks for "the longest prefix whose elements **pass a test**". The
  intent is already higher-order; the caller HAS a predicate. `take-while` is a
  direct answer.
- Demand 11 asks whether a list "**contains a given element**". The intent is
  first-order. Reaching `any` means inventing a predicate the caller never had —
  `(fn [(y Str)] (== y x))` — to bridge to a shape the intent did not describe.

Applying a combinator to a predicate you already hold is USE. Manufacturing one
to reach a combinator is COMPOSITION.

## The count: TWO of thirteen

| # | demand | answered by | class |
|---|---|---|---|
| 1 | report a required config key the host did not supply | `config-missing` | definition |
| 2 | read a request header by name, with a fallback | `header-or` | definition |
| 3 | JSON: a nested path out of the body | — | ABSENT (not a composition) |
| 4 | make every gate cover a new corpus member | — | build scope, not a definition |
| 5 | build a valid signed `Request` for a property | **`gh-request` ∘ `gh-sign`** | **COMPOSITION** |
| 6 | make a field safe to splice into a delimited record | `record-field` | definition |
| 7 | how many generated cases reached the conclusion | — | tooling signal |
| 8 | run a check once, before the port binds | — | protocol shape |
| 9 | a prefix match that only matches at a delimiter | `media-type-is`, `path-is` | definition |
| 10 | (not friction) | — | n/a |
| 11 | test whether a list of `Str` contains an element | **`any` + a predicate** | **COMPOSITION** |
| 12 | take the longest prefix whose elements pass a test | `take-while` | definition |
| 13 | tell packaging a capability apart from leaking one | — | analysis granularity |

**Two compositions. The issue named one; the second was found by review of this
record and is the sharper of the pair.** `gh-request` takes `sig` as an
arbitrary `Str`, so it does not build a valid signed request on its own — the
corpus's own property chains it, `(gh-request (gh-sign (gh-spec-secret) body)
"push" ...)` (`apps/github-webhook/webhook.oath`). Seven demands have a corpus artifact
(1, 2, 5, 6, 9, 11, 12); five are single definitions and two are compositions.

Worth noting how it was missed: #74's falsifier EXCLUDED demand 5 from scoring
(its query came out AST-identical to the target's own law), so it carried a
"has an artifact" label that was never re-examined for THIS question. A demand
excluded from one measurement is not classified for the next.

**The five with no artifact are not hidden compositions**, which is the part
worth checking rather than assuming — #176 asks a different question of these
demands than #74's falsifier did, so "no artifact" had to be re-examined for
"answerable by combining". None is: 4, 7, 8 and 13 are not code-level demands at
all (build scope, a tooling signal, a protocol shape, an analysis verdict), and
3 is a genuine missing capability — the scanner cannot address a nested path, so
no arrangement of what exists answers it.

## The premise HOLDS — and a first draft of this record got it backwards

#176 states of `any`:

> **And it is unreachable from that intent by every existing mode**, because the
> caller is looking for a definition that does the whole job and the corpus
> offers a piece that does half of it.

A first draft called that false, on the grounds that `--implies` proves `any`.
**Review caught the circularity, and it is worth stating plainly because it is
the kind of error that reads as evidence.** The query that reaches `any` is the
HIGHER-ORDER one — and writing it means manufacturing the predicate
`(fn [(y Str)] (== y x))`, which is precisely the composition step the issue
says is missing. I had used a reader who had already taken the missing step as
proof the step was not missing.

The intent as literally stated reaches nothing:

```
(defn wanted [] [(x Str) (xs (List Str))] Bool false
  (prop found-at-head   [(x0 Str) (x1 (List Str))] (wanted x0 (Cons [Str] x0 x1)))
  (prop absent-from-empty [(x0 Str)]               (not (wanted x0 (Nil [Str])))))

--spec     no definition states this law as written        (both laws)
--implies  (no definition provably satisfies this ...)     (both laws)
```

**So the gap is real.** No mode carries a caller from a first-order intent to a
higher-order combinator; the reframing has to happen in the caller's head first.

## What the clean readers did — and WHAT bridged the gap for them

#175's blind run put this intent in front of subjects who had never seen the
corpus. **Only the CLEAN four count** (t3, t4, c3, c4): the first pair's controls
saw a script of the measurer's that named `any`, the contamination recorded in
`issue-175-shapes/blind/subject-reports.md`, so they cannot witness "nobody was
told it existed". Two of the clean four had the shape guidance; two had none.

| subject | guidance | did a MODE name `any`? | how |
|---|---|---|---|
| t3 | yes | **yes** — `--implies` proved it | applied the ABSTRACTION axis: "a predicate instead of a value" |
| t4 | yes | **yes** — `--implies` proved it | same axis; then declined to COUNT it as a find |
| c3 | no | no | identified `any` by READING the corpus, then reported "none found" with it as the recommended fix |
| c4 | no | no | reported "none found"; named `any` only under "what I would try next" |

**Both readers who got a discovery mode to name `any` were the ones carrying the
abstraction axis; neither unguided reader did.** They reached `any` — by
enumerating the corpus and reading it — but not through discovery, and both
concluded "none found".

That is a narrow result on a single intent and it should not be inflated. But it
is the direct counterpart to #175's null, and it points the same way: the axis
did no measurable work across seven intents, and on the ONE intent that needs a
composition, it is what the two successful readers used.

**Three of the four declined to count `any` as an answer at all**, in their own
words — "that is a *composition*, not a definition satisfying the intent". One
reported it. So the category #176 names is real and contested by readers who
were never told it existed.

## Demand 5, searched blind — the leg that was untested

An earlier version of this record declined while admitting that demand 5's chain
had never been put to a reader, and named that as the cheapest measurement that
would test the decline hardest. It has now been run.

**THE TASK I POSED WAS NARROWER THAN DEMAND 5, AND THE ANSWER WAS NOT.** That
asymmetry has to be stated in both directions or the result gets read as
stronger or weaker than it is.

Demand 5's want is "to check a handler's accept path", and its recorded
workaround is FOUR definitions — `gh-spec-secret`, `gh-sign`, `gh-request`,
`caps-with`. I asked only for a valid signed request given a secret and a body,
which is a two-piece subproblem of that. **So the task under-specified the
demand**, and a run that stopped where the task stopped would be partial
evidence at best.

It did not stop there. Unprompted, the report gives the whole accept path —

```
(match (gh-webhook (caps-with sink secret)
         (gh-request (gh-sign secret body) "push" "/hook" "application/json" body))
  ((Resp code hs bd) code))
```

— names `caps-with` and `gh-webhook`, and RUNS it, with a secret it invented
rather than the corpus's. **So the chain it ASSEMBLED is three definitions
deep** — `gh-sign`, `gh-request`, `caps-with` — not two, and not the demand's
full four: `gh-spec-secret` is mentioned as a canned alternative to supplying
your own secret, and mentioning is not using. Three of the demand's four
definitions, plus the handler they feed.

**What that supports and what it does not.** It supports "a reader reached three of
demand 5's four definitions, and the handler they feed, from a task that asked
for two". It does not support "a reader asked for demand 5's
full intent reached it", because nobody asked that — the report volunteered the
remainder. A task posed at the demand's own width would be the cleaner
measurement, and this one is not it.

**One subject, the SHIPPED page verbatim.** Unlike #175's run, no redaction was
needed: the current "When a query returns nothing" section names none of demand
5's artifacts, so this tests the real documentation rather than a stand-in. The
export carried the binary, a corpus copy, the intent, and that page. Preflight
confirmed no target name appears in the task or the guide; the subject reported
a clean context and saw no other process's output.

The intent was put as a developer would: *"my property needs a valid, correctly
signed request — given a secret and a body, can this corpus give me one?"*

**Result: FOUND for the task as posed** — which, as the next paragraph records,
was narrower than demand 5 itself.

```
(gh-request (gh-sign secret body) "push" "/hook" "application/json" body)
```

**The route is the part that matters, and both moves it used are on the page —
though not in the order the subject thought.** It opened with `oath ls` and
`oath get`, calling that "the GUIDE's stated first move"; it is not. The page
leads with the SIGNATURE PROBE and introduces enumeration afterwards as "two
more moves in the same spirit". The subject's attribution is wrong and is quoted
here rather than repeated as fact, because what caused the find is exactly the
question this run exists to answer.

What it did, in order: enumeration surfaced the `gh-*` cluster, then signature
probes pinned the two shapes: a probe at `(-> Str (List Int) Str)` named `gh-sign`, one at
the five-argument shape named `gh-request`, and one at `(-> Str (List Int)
Request)` returned nothing. The
abstraction axis played no part, as predicted — demand 5 has no predicate to
invent.

**The empty probe does NOT establish that no single-call builder exists**, and
the record should not lean on it: the neighbour list omits definitions carrying
no properties of their own, so one could be present and invisible. Absence is
established separately, by resolving every LIVE name and reading its return
type — exactly one definition in the corpus returns a `Request`, and it is
`gh-request`. (Live names, not a walk of `dependents`, which surfaces
superseded objects alongside current ones.)

So the mitigation this record previously called "a guess, and not counted" is
measured: ENUMERATION plus PROBING reaches a two-piece composition whose pieces
have unrelated signatures. Which of the two did the decisive work is NOT
separable from one run — the subject used both, and a design that gave it only
one was not tested.

**It could not be proved end to end, and the subject said so precisely.**
`--implies` is structurally blind here — `hmac-sha256 is outside the provable
fragment (trusted crypto primitive)` — so no mode asserted the composition
works. It established that two other ways instead, and separated them from what
a mode said: it READ `gh-webhook`'s own `accepts-github-signed`, and it RAN the
chain under `oath eval` with a secret it invented, with four negative controls
(tampered body 401, foreign signature 401, unusable secret 500, `ping` 200).

**Two incidental findings, both verified here rather than taken:**

- `gh-request` carries **no mutation score** — `spec strength UNMEASURED`. It is
  the one unmeasured link in a chain a property test would depend on.

  **And a campaign run against it during this session scored 13 of 244 mutants
  killed — about 5%.** That result is NOT committed: it appeared in the
  canonical store as an unintended side effect while this record was being
  written, and a corpus verdict does not belong inside a documentation change
  (identity and fixtures move together, or they disagree). The store was
  restored to HEAD and the number is recorded here as an OBSERVATION to act on
  deliberately.

  **And it must not be read as spec strength**, which is this project's own
  sharpest recorded error and one I made in a first draft of this line. A
  campaign without `--prove` measures the GENERATOR'S REACH: 13/244 says
  generated executions killed few mutants, not that the properties pin little —
  a survivor may be logically excluded and simply never exercised. What is
  worth knowing is narrower: `gh-request` constructs the input every
  signed-webhook property depends on, and nothing has yet measured how much its
  three properties actually exclude. `oath mutate --prove` is what would say.
- **`[tested here]` is not a verdict.** The subject ran a deliberately false law
  as a calibration control so it would not misread that marker. It is right, and
  subtler than it first looks: the marker labels the QUERY definition's own
  guarantee level and prints identically whatever the law says.

## Run 2 — demand 5 at its own width

Run 1's task asked for a signed request; demand 5's want is "to check a
handler's accept path", four definitions deep. Review caught the narrowing, so
the run was repeated with the demand's own intent and a criterion fixed before
dispatch. Same isolated export, same shipped page, clean context reported.

**Result: FOUND.** The subject assembled all four —

```
(gh-request (gh-sign gh-spec-secret body) "push" "/hook" "application/json" body)
(gh-webhook (caps-with sink gh-spec-secret) rq)
```

— with `gh-record` and a `bytes-ok` guard besides.

**Route:** `oath ls` first, explicitly citing the page's own framing ("probing
and reading the corpus is what finds artifacts"), then a signature probe at the
handler's shape, then the accept law written from the intent.

**And that law CONTENT-HASH MATCHED `gh-webhook`'s own `accepts-github-signed`**
— three such matches in the run: the accept law and both traps.

**That is NOT evidence of convergent phrasing, and two drafts of this record
claimed it was, each less defensibly than the last.** The first said three
independently-worded matches. The second said each was named differently by the
subject. Checked against the committed queries, **only q3 is renamed** —
`a-correctly-signed-push-is-accepted`; q6 and q7 reuse the target's own names,
`ping-does-not-record` and `a-non-ping-does-record`, verbatim.

And the subject ran `oath get gh-webhook` early, before writing these queries,
so it had read the laws it went on to match. Names are not hashed, so even the
one rename proves nothing about independence. The #175 convergence finding
stands on its own subjects — one of which wrote its matching law as its FIRST
shape, before seeing any candidate — and this run adds nothing to it.

What the matches DO establish is that the subject's law is the corpus's law, so
the property it hands back is the one `gh-webhook` is verified against rather
than a plausible-looking variant.

### The trap it was ASKED about — and demonstrated rather than asserted

**The task asked for this**, in those words: "anything I must be careful about
so the property is not silently exercising a reject path while appearing to
pass". So the warning is not evidence of unprompted insight, and a first draft
called it that — while the pre-dispatch key already said "recorded, not scored,
because the task hints at it". The key was right and the write-up inflated it.

What the run does show is the EVIDENCE it brought: not an assertion but a
content-hash match against laws the corpus states itself, plus a demonstration
that the naive version matches nothing.

**Demand 7 of the friction log records a trap of this KIND about the very law
the subject matched — but not the same one.** It records that strengthening the
handler made
`accepts-github-signed` "almost entirely vacuous, because a generated `Str`
reaches sixteen codepoints almost never, so nearly every case took the `true`
branch. **It still reported `passed 200 cases`.**"

(Not demand 6, which is a different defect — a property that was FALSE rather
than vacuous, `gh-record`'s field count forged by a tab. A first draft cited it
and review caught the swap.)

It wrote the naive law first, flat `202`, and showed it hash-matches nothing.
Then two REJECT-PATH branches — a different defect from demand 7's vacuity, and
worth keeping apart: these make the naive law FALSE, where demand 7's guard
makes a law vacuously TRUE. Each is confirmed against a law the corpus states
itself:

- `"ping"` short-circuits to **200** without calling `emit`. "This is the
  sharpest trap: 200 looks like success."
- an `emit` returning `SNil` lands the accept path on **500**, not 202 — which
  is why the correct law matches on `(sink (gh-record rq))` and asserts per
  branch instead of asserting 202 flatly.

It then listed all eight of the handler's guards in body order with the status
each returns, from reading the body.

**What it did not raise** — the honest edge of the result — is demand 7's own
mechanism: that the `secret-is-usable` guard makes the law VACUOUS under
generation, because a generated `Str` almost never reaches sixteen codepoints,
so nearly every case takes the trivial branch and the property still reports
`passed 200 cases`. That is the opposite failure from the two it did find: those
make a naive law false and visible, this one makes a correct-looking law empty
and silent. Passing `gh-spec-secret` sidesteps it in practice, but the reasoning
that produced demand 7 is not in the report.

### One finding about the corpus rather than about discovery

`oath mutate gh-webhook` scores 203/208, and three of the five survivors are
`literal 45` — byte `-`, the `header-or` fallback for a missing
`x-github-event`. **What that shows is that no property OBSERVABLY CONSTRAINS
the fallback**, not that the case is never reached: several of `gh-webhook`'s
laws quantify over an arbitrary `Request` (`status-is-one-of-seven`,
`never-leaks-a-body`, `unsigned-is-401`), so generated cases can lack the header
even though `gh-request` always adds one. A first draft said the case is never
exercised; that is a stronger claim than surviving mutants support.

What is true and useful: the constructor kit cannot build that request, so a
property that wanted to pin the fallback deliberately would have to assemble the
`Req` by hand. Verified here: 98%, 14 properties. A gap in the specification of
a definition every signed-webhook property depends on — and not a #176 matter.

## Verdict: DECLINE THE MACHINERY

**The gap exists.** No mode carries a caller from the first-order intent to
`any`. Measured, on the intent as written.

**Two of THESE thirteen demands need a composition** — two of the seven with an
artifact. A statement about one application's ranked friction log, not about
intents in general. Whether 2 of 7 is low enough to decline on is a judgment, it
is mine, and the issue's own bar was one in thirteen.

**Documented moves reached both, at their own width.** Demand 11's bridge is the
ABSTRACTION axis, used by both readers who got a mode to name `any`. Demand 5's
is ENUMERATION plus SIGNATURE PROBING, run twice — the second time at the
demand's full accept-path intent, where the reader assembled all four
definitions and returned the corpus's own verified accept law rather than a
plausible variant of it.

So what #176 proposes building already exists in prose and reached both
compositional demands in this sample. Decline.

## What this does NOT establish

- **That two in thirteen generalises.** These are one application's demands,
  ranked by one author, against a corpus weighted toward provable arithmetic and
  structural recursion. A combinator-heavy library would score differently, and
  nothing here samples one.
- **That composition-answerable demands are easy to recognise, or that readers
  agree about them.** Among the four CLEAN readers, three declined to count
  `any` and one reported it as the answer. That split is evidence the category
  is real and contested — not evidence it is obvious, and not a four-versus-four
  result: the contaminated pair is excluded here for the same reason it is
  excluded above.
- **That an UNGUIDED reader reaches demand 5's chain, or that the page is what
  supplied the route.** The one reader who found it had the shipped page and
  used two moves the page lists — but it opened by ENUMERATING, which the page
  introduces only after the signature probe it actually leads with. So the
  subject did not follow the page's order, and #175's unguided readers reached
  for enumeration on their own. Attributing the find to the documentation is
  therefore not supported by this run; what is supported is that the moves that
  worked are the moves the page names.
- **That the composition can be VERIFIED by a mode.** It cannot: `--implies` is
  structurally blind here, because `hmac-sha256` is outside the provable
  fragment. The reader established the chain works by reading `gh-webhook`'s own
  property and by running it, and was careful to label which was which. A
  suggestion layer would have the same limit — it could propose the chain and
  could not prove it.
- **That one subject settles a behavioural claim.** It is one reader on one
  demand. It closes the leg this record had explicitly left open, and it does
  not make the result robust; a second reader disagreeing would be worth more
  than this one agreeing.
