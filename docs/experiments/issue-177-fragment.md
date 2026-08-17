# #177 — how big is `--implies`'s blind spot, and does another mode cover it?

**Status: MEASURED. The recommendation is DECLINE**, on the second of the two
decline conditions the issue states.

`docs/experiments/issue-177-fragment/` is the instrument that derived the set;
it assigns no verdict and says so. This assigns one.

## The set, derived from the prover rather than guessed

`oath find --implies` appends the CALLER'S query property to a candidate and
proves that synthetic property with `self` bound to the candidate. **A goal the
translator cannot build never reaches a solver**, so the mode reports no verdict
— which a caller cannot distinguish from the artifact not existing.

So the measured question is not *do this definition's own laws translate?* but
**can any query mentioning this candidate be translated?**, which turns on the
candidate's body, because a mentioning query inlines it.

Universe: **210 live names over 208 live objects**, from `codebase/names.json`.

| class | per-object | per-name |
|---|---:|---:|
| unreachable | 12 | 12 |
| inconclusive-polymorphic | 0 | 0 |
| reachable | 182 | 183 |
| not-a-candidate (`data`) | 14 | 15 |
| **total** | **208** | **210** |

**12 of 194 candidates — 6.2%.** The translator's own refusals, counted from
the generated report rather than recalled:

| refusal | n |
|---|---:|
| `"lam" terms are outside the provable fragment` | 8 |
| `hmac-sha256 is outside the provable fragment (trusted crypto primitive)` | 3 |
| `apply2 must be fully applied to inline` | 1 |

**The check fired.** `record-field` and `json-string-value`, the two members
#74's falsifier named, are both in the derived set. The derivation contains
them rather than being built from them, which is what makes the other ten
evidence rather than restatement.

## Decline condition 1: small, but NOT peripheral

6.2% is small. **Peripheral it is not.** All twelve, from the generated report:

`gh-record`, `gh-sign`, `gh-webhook`, `hmac-kat-rfc4231-2`,
`json-scoped-string`, `json-string-value`, `no-field-can-inject`,
`record-field`, `record-under`, `secret-is-usable`, `spin-partial`, `webhook`.

Ten of the twelve are the webhook application's own definitions and its
crypto exhibits — exactly what an agent searching for "parse a signed webhook
payload" would want. `spin-partial` is a partial-application exhibit and the
one member that is genuinely peripheral. The blind spot is small by count and
central by content, so **condition 1 does not fire.**

Note also that every member is `tested` or `asserted`, none `proven` — as
expected, since a definition the translator cannot handle cannot have been
proven through it.

## Decline condition 2: another mode DOES reach them

Run against both known members. Committed store untouched throughout
(`git status codebase/` clean before and after); the e-graph test used a
throwaway copy.

**`--spec` does NOT reach them.** Authored queries matching each target's shape
but stating the law in the author's own words:

| target | `--spec` result |
|---|---|
| `record-field` | first law matched a DIFFERENT definition (`config-key`, proven as `empty-stays-empty`); second law matched nothing |
| `json-string-value` | no definition states the law; 5 compatible-signature candidates |

Both targets appear in the output **only in the COMPATIBLE SIGNATURE fallback
list** — SURFACED, not SATISFIED, in #74's terms. A caller still has to read
every candidate. Counting that as a hit is the specific error this measurement
was warned against.

**`--equiv` DOES reach them, and this is what decides the issue.** An author
writes their own implementation of the intent, puts it, and asks what is
equivalent:

| author's fresh definition | `--equiv` returns |
|---|---|
| `my-sanitise` — printable ASCII passes, else `"-"` | **`record-field`** (`#d7f475762489`) |
| `my-upto-quote` — `take-while` printable and not `"` | **`json-string-value`** (`#e84c7faa721c`) |

Matched by eHash — same function up to the rewrite system — with no shared name
and no shared property text. **The blindness is not structural.**

## Verdict: DECLINE

The issue's own second condition is met: another mode reaches the same targets,
so an intent layer does not need a different spine — it needs to try more than
one mode. That is a note for whoever builds one, not an issue.

**And the note has a real cost attached, which should travel with it.**
`--implies` and `--spec` take a LAW; `--equiv` takes an IMPLEMENTATION. An
author who can already write the implementation has solved a large part of the
problem the intent layer exists to solve, so "another mode covers it" is true
and is not free. #175's translation work should treat `--equiv` as the fallback
for fragment-blind targets and price that asymmetry rather than assume the modes
are interchangeable.

## What this does NOT establish

- **That 6.2% generalises.** It is this corpus, whose composition is a choice —
  weighted toward provable arithmetic and structural recursion, with one
  application's helpers attached. A corpus of applications would have a larger
  blind spot; nothing here samples one.
- **That `--equiv` reaches every member.** Two of twelve were tested, chosen
  because #74 named them. The other ten are untested and the mechanism —
  eHash equality — gives no reason to expect a member to fail, which is a
  reason to expect rather than a measurement.
- **Anything about latency.** `--implies` took 48s and 992s on two queries in
  #74's run; `--equiv` was sub-second here. That difference was not measured
  systematically and is not part of this verdict.
