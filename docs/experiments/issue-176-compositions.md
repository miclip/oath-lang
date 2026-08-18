# #176 — how often is the answer a composition rather than a definition?

**Status: MEASURED. The recommendation is DECLINE THE MACHINERY — but the gap
#176 describes is REAL**, which is the opposite of what two earlier drafts of
this record concluded, and the corrections are kept visible below rather than
tidied away.

Two of these thirteen demands need a composition. No mode carries a caller from
the first-order intent to the higher-order artifact — measured. One of the two
has a documented mitigation that was measured working; the other is untested.

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

## Verdict: DECLINE THE MACHINERY — the gap is real and uncommon in this sample

Not the verdict two earlier drafts of this record reached, and the difference is
worth keeping visible: both of those declined on the ground that the gap did not
exist, and both were wrong.

**The gap exists.** No mode carries a caller from the first-order intent to
`any`. Measured, above, on the intent as written.

**Two of THESE thirteen demands need a composition** — two of the seven that
have an artifact at all. That is a statement about one application's ranked
friction log, not about how often intents need compositions in general; a
combinator-heavy corpus would score differently and nothing here samples one.
Whether 2 of 7 is low enough to decline on is a judgment, it is mine, and it is
recorded as such rather than attributed to the issue: the issue offers "one in
thirteen *may* not justify anything", I measured two, so its stated bar is not
straightforwardly met.

**It has a mitigation that is not machinery.** The bridge is a reframing: the
caller generalizes the value they are looking for into a test. That is written
down — it is the ABSTRACTION axis in `docs/discovery.md` — and it is what both
readers who got a mode to name `any` actually used, while neither unguided
reader reached it through discovery at all.

**And the mitigation is measured on ONE of the two, not both.** A first draft
claimed it served both; it does not, and the difference is checkable rather than
arguable. Demand 5's composition has no predicate to invent, so the abstraction
axis does not apply, and a signature probe at the shape a caller WANTS —
`(-> Str (List Int) Request)`, secret and body in, a signed request out — returns
nothing at all, not even a neighbour list:

```
· refl  — no definition states this law as written
          (no compatible-signature candidates)
```

What would plausibly surface `gh-sign` and `gh-request` is corpus ENUMERATION,
the other move `docs/discovery.md` now leads with — but that is a guess, nobody
searched demand 5 blind, and it is not counted here.

So: the thing #176 proposes building already exists in prose for ONE of the two
compositional demands in this sample, measured; for the other it is untested and
the obvious mitigation is a different one. That is still not a good trade for a
suggestion layer at 2 of 7 — but it is a weaker case than "already mitigated",
and stating it the strong way is what two earlier drafts of this verdict did
wrong.

**What would reopen it**, and this is prevalence rather than principle:

- a corpus or an application whose demands need compositions at a materially
  higher rate — 2 of 7 is the number to beat, and it is not a large sample;
- evidence that callers do NOT make the reframing when told about it. The
  measurement here is four readers on one intent, of whom two succeeded and both
  were the guided ones. That is thin, and a wider run could reverse it;
- a demand needing a chain longer than the ones measured, where naming the
  abstraction is not enough to get there. **Demand 5 IS such a chain, it is in
  this sample, and nobody has searched for it blind** — the cheapest next
  measurement if this is reopened, and the one that would test the decline
  hardest.

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
- **That either composition was FOUND as a composition.** This is the real gap
  in the sample, and it is not the one a first draft claimed. That draft said
  both instances were two-piece and that nothing here tests a longer chain —
  false: demand 5's is three definitions deep,
  `(gh-request (gh-sign (gh-spec-secret) body) ...)`, and longer still if the
  accept-path property's `caps-with` is counted. So the sample DOES contain a
  multi-step composition.

  What it does not contain is any evidence about DISCOVERING one. Demand 5 was
  excluded from #74's scoring and was never put to a blind reader, so the only
  intent any reader searched compositionally is demand 11's two-piece one. A
  three-step chain is where a suggestion layer would plausibly earn its keep,
  and whether a caller reaches one unaided is untested here.
