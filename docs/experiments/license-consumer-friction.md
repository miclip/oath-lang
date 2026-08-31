# license-consumer — a demand list from the licensing evidence domain

The flywheel's "depend on Oath, don't improve it" exercise, aimed this time at
LICENSING — the evidence domain no prior consumer round exercised. The consumer is
a one-line application (`tally`, the length of a list) that asserts its own terms
and depends on the `List` datatype, published exactly as the standard library ships
it. The instrument is [`license-consumer/run.sh`](license-consumer/run.sh), which
publishes real signed envelopes to a served registry — only a publication asserts
terms (SPEC §12.3), so nothing here is asserted by hand — and reads the verdict the
registry derives.

## What the evidence is, and what it is not

Every verdict is MEASURED by running `oath license` against a live registry after
real publications. The headline claim — "a licensed app on the unlicensed stdlib
derives UNSTATED for every grant" — is decidable from that output, and its CONTROL
is measured in the same run: relicensing the one dependency flips the whole
composition to a determinate verdict, which isolates the finding as a property of
the CORPUS, not of the engine.

The licensing engine is not the problem, and the exercise proves it: over an
all-licensed closure it derives `commercial YES` / `modification YES` from Apache,
and it propagates a GPL dependency's `share-alike` obligation up into an Apache
application (§4 below). Contagion, transparency, obligation-propagation — all work.

---

## 1. The standard library asserts NO license, so the licensing evidence domain is INERT for any real program — DEMAND

**Worst, and it is not a bug — it is an empty foundation under a working
mechanism.** `oath license` on any corpus definition is all-UNSTATED, because the
stdlib was published without terms. Measured with a licensed application on top:

```
$ oath license tally          # tally asserts Apache-2.0; it uses List
  commercial use           UNSTATED
  redistribution           UNSTATED
  modification             UNSTATED
  patent grant             UNSTATED
  share-alike obligation   UNSTATED

  assertions consumed (2):
    tally    Apache-2.0
    List     -   — no terms asserted
```

`tally` asserts Apache-2.0, which on its own grants commercial, redistribution and
modification. The composition grants NONE of them, because `List` — one ordinary
stdlib datatype — asserts nothing, and **UNSTATED is contagious by design**: absence
of a prohibition is not a grant, so one unstated input makes the whole composition
unstated. That contagion is a deliberate SAFETY property and it is correct. The
consequence is not: **every application built on the Oath standard library inherits
UNSTATED for every grant, so its own license is unusable.** The evidence domain is
fully built and produces nothing determinate for real code.

The CONTROL, measured in the same run, rules out a tooling defect: relicense the
single `List` dependency as Apache-2.0 (a re-publication under new terms — the code
and hash do not move, §12.3) and the SAME `tally` immediately derives `commercial
YES` / `modification YES`. Nothing about the engine changed; the input did.

**Consumer hurt: anyone who wants a determinate licence on anything they build.**
Because everything routes through the stdlib (a datatype, a fold, `Str`), the floor
for any real composition is UNSTATED until the stdlib asserts terms.

**DEMAND: the standard library must assert license terms**, so a composition over
it can be determinate. That is a LICENSING DECISION (which terms — permissive like
Apache-2.0/BSD, or copyleft) rather than a tooling change, plus a mechanism to
attach them: the `stdlib/oath-stdlib.json` manifest already declares membership and
authority, and is the natural place to declare terms that the project's own
publications assert. Until then, the honest documentation is that a licensed Oath
application must license every stdlib definition in its closure — which the
`referenced`-membership model makes someone else's decision to make.

**Evidence class:** the all-UNSTATED verdict, the assertion list naming `List` as
untermed, and the relicense control flipping it to YES are all MEASURED (`run.sh`
§1–3). That the corpus is uniformly unlicensed is decidable — `oath license` on any
corpus name shows it.

---

## 2. A licence verdict cannot be PREVIEWED before publication — you must publish, permanently, to see it — TOOL LIMITATION

**Second.** Terms are asserted only in a signed publication envelope, and `oath
license` reads them only from the journal. `oath put` takes no `--license`, and
there is no `oath license` mode that evaluates a proposed assertion against a local
closure. So a publisher deciding whether Apache-2.0 gives their composition the
terms they intend cannot find out without PUBLISHING — and a publication is
permanent (the name binding and the asserted terms both enter the append-only
journal). `--dry-run` prints the envelope bytes it would sign, including the licence
field, but does not derive the composition verdict those terms would produce.

**Consumer hurt: a publisher choosing terms.** The one question a licence chooser
has — "what will my users actually be permitted to do?" — is answerable only after
the irreversible act. The measured cost is that licence selection is
publish-then-check rather than check-then-publish.

**DEMAND: derive a composition's licence verdict from a PROPOSED assertion against
the local closure, before signing.** Whether that is `oath license <file>
--assert <SPDX>`, a `--license` on a `--dry-run` publish that runs the evaluator,
or a local `put --license`, is a design question; what matters is that the verdict
is checkable before the permanent act, as `publish --dry-run` already makes the
BYTES checkable.

**Evidence class:** that `--license` is publish-only and `put` rejects it is
decidable from the CLI; HAND-verified. The absence of a preview path is
HAND-observed.

---

## What is NOT friction, recorded so it is not re-derived

- **Contagion is TRANSPARENT.** The verdict lists every assertion it consumed and
  marks the untermed ones "no terms asserted", so a consumer can see EXACTLY which
  dependency made the composition UNSTATED — here, `List`. The system does not hide
  the culprit behind a bare verdict; it names it. This is the right design and the
  reason demand 1 is actionable at all.
- **The engine is correct and complete for what it claims.** Determinate grants
  over licensed closures, UNSTATED contagion, and share-alike obligation
  propagation across an Apache-over-GPL composition all reproduce (`run.sh` §3–4).
  The exercise found no defect in the evaluator — the gap is entirely that the
  corpus it evaluates asserts nothing.
- **UNSTATED is not NO.** The output says so and the model enforces it: an
  unlicensed dependency does not PROHIBIT reuse, it leaves the terms unknown, and
  the consumer is told to adopt their own policy (treat UNSTATED as deny, or
  require explicit grants). The registry deliberately does not choose that stance
  for the reader.
