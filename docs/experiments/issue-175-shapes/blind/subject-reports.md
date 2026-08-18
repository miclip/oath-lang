# The eight subjects' own words

**WHAT IS HERE AND WHAT IS NOT.** Each subject's CONTEXT disclosure and SUMMARY
are reproduced verbatim, along with every passage the write-up quotes. The
per-intent query listings are omitted; the queries themselves are committed in
`subject-queries/` and `verify.sh` re-asks them. The behavioural claims in
`issue-175-shapes.md` — that contexts were clean, what each subject scored, what
technique it used, and what it disclosed — rest on the text below.

Effort figures are the harness's own accounting, not the subjects' reports.

---

## RUN 1 — TREATMENT (guide), CONTAMINATED BY THE MEASURER

Both control subjects of this run saw, through `ps`, a script the measurer was
writing whose text named the targets. The treatment subjects did not report it,
but the channel was open while subject 1 ran, so this run's independence is not
established. It is kept because the contamination is itself the finding.

### s1 — 7 of 7 · 43 tool calls · 71K tokens

Context, verbatim:

> I was given **no substantive project context**. Specifically, I received
> **none** of: a CLAUDE.md or project-instructions file, remembered/memory
> notes, commit summaries, prior task history, a description of the `oath`
> language or its corpus, or any hint about which intents do or do not have
> answers.
>
> Two weak signals about the domain were therefore present before I opened
> TASK.md — the cwd name `oath-lang` and the `mcp__oath__*` tool names (which
> imply `find_spec` / `find_implies` / `find_equiv` modes). I read nothing from
> `/Users/miclip/workspace/oath-lang`, and nothing outside `/tmp/blind175/s1`
> afterwards.

Its intent-4 query, quoted because the convergence finding rests on it — the
property NAME differs from the target's while the law hashes identically:

> `(prop never-contains-the-separator [(x0 Str)] (not (contains 9 (str-bytes (wanted x0)))))`
> `· never-contains-the-separator [tested here]  #968def4dbf2a`
> `    record-field       (tested as "never-contains-a-tab")`

### s2 — 6 of 7 · 60 tool calls · 92K tokens

Context: clean; disclosed that MCP tool NAMES were in its deferred-tool list and
that it never loaded them.

On intent 6, having found `any` and confirmed it with `--spec`, it declined to
report it:

> A caller wanting intent 6 would write `(any [Str] (fn [(s Str)] (== s y)) xs)`.
> That is a *composition*, not a definition satisfying the intent, so I am
> **not** reporting `any` as a find.

It ran an unprompted positive control first:

> the identical query finds `contains` at `Int` and finds nothing at `Str`.

It also reported it did NOT enumerate: "I did **not** run `oath ls` and did not
read `names.json`."

### c1 — control · contaminated

> **One contamination I must disclose.** Later, while checking on my own
> background jobs with `ps`, the process listing showed command lines belonging
> to *other concurrent sessions on this machine* — including a heredoc of a
> `verify.sh` script containing claim strings about this corpus (naming
> `config-missing`, `json-string-value`, `record-field`, `take-while`/
> `drop-while`, `any`/`all`, and "contains at Int"). I did not seek it … By then
> I had already independently obtained my `--spec` results for all seven intents
> … I would rather over-disclose than pass off a leaked hint as my own inference.

### c2 — control · contaminated · 765 tool calls · 302K tokens

> Mid-task I ran `ps` to check whether my solver processes were alive. The
> output incidentally showed other concurrent processes … I saw it after I had
> already reached my conclusions for intents 1–5 and 7 via my own queries, and I
> did not use it as evidence for anything below.

Its own summary of the mechanism, reached unaided:

> Query *shape*, not just query *content*, determines reachability in `--spec`.

---

## RUN 2 — THE CONTROLLED RUN. No subject reported seeing another process.

### t3 — TREATMENT · 7 of 7 · 61 tool calls · 89K tokens

Context, verbatim on the point that matters:

> What I did **not** receive: **no CLAUDE.md or project instructions, no
> remembered notes or memory entries, no commit summaries, no git log/diff, no
> prior task history, no hints about the corpus, no names of any definitions in
> it, and nothing about the seven intents.**

Its method, which is the run's central finding:

> per GUIDE step 2, I used `--spec` **signature probes**: a query with the shape
> I was guessing and a trivial law `(== (wanted x) (wanted x))`. The law never
> matches, but the "N definition(s) have a COMPATIBLE SIGNATURE" fallback
> enumerates the corpus's definitions at that shape. That is the cheapest map of
> the corpus and it is what surfaced most of the answers.

And how it actually reached intent 1 — not through a find mode at all:

> That is how the name `config-missing` surfaced — **via `dependents`, not via a
> find mode.** … my signature probes at Result/Option/assoc-list shapes all
> missed because I never guessed `(List Str) × (List Str) -> Str`.

Disclosure of an incidental exposure, which it volunteered:

> while checking whether a long-running command had finished, I ran `ls -la` on
> the harness's task-output directory … That listing showed **file names, sizes
> and timestamps** of unrelated background tasks … I did not open or read any of
> them, and nothing in the names conveyed anything about this corpus.

### t4 — TREATMENT · 5 of 7 · 70 tool calls · 93K tokens

The only subject to miss an artifact, and the one that followed the page's
law-writing framing most faithfully:

> I deliberately did **not** grep or read the full `ls` name list, `names.json`,
> or the object store for intent keywords — every candidate below was surfaced
> by a `find` mode's own output (a spec hash match or a signature-compatible
> neighbour list), not by browsing names.

On intent 1, after nineteen shapes:

> **Conclusion: none found** … Nineteen shapes and four `--implies` runs is broad
> but, as GUIDE says, exhausting axes does not establish absence.

> I saw no other process's output or command lines at any point.

### c3 — CONTROL · 6 of 7 · 55 tool calls · 71K tokens

> **One disclosure that bears on how "blind" the search was:** after reading
> GUIDE.md I read `codebase/names.json` (238 names) and ran `./oath get` on ~20
> candidates to learn the surface syntax and the corpus's vocabulary. That is
> inside the permitted directory, but it means my query *phrasings* were
> informed by how the corpus states its laws, not invented purely from the
> English intents.
>
> No other process's command line or working files were seen. I ran no `ps`/`top`.

### c4 — CONTROL · 6 of 7 · 47 tool calls · 88K tokens

> Beyond `find`/`get`, I ran `./oath ls`, `./oath explain`, and `./oath demand`
> … `ls` gave me the corpus's **names and type signatures**, which I used to
> write well-typed queries. That means my query design was not name-blind, and
> it may have biased me toward the shapes I tried.
>
> I did not run `ps`/`top` or inspect any other process. No output from
> unrelated work appeared at any point.

(`oath ls` prints names, hashes, kinds and guarantees — not signatures. c4 was
pairing it with `get`. Checked against the tool, because the write-up had
initially repeated this subject's wording.)

Its own diagnosis of the polymorphism axis, rediscovered without the guide:

> This was a **false negative caused by my query shape**: the corpus's list
> combinators are polymorphic, and a monomorphic query is not signature-
> compatible with them.
