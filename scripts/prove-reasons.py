#!/usr/bin/env python3
"""Classify every non-proven corpus property by WHY it is not proven.

TWO INPUTS, AND ONLY ONE OF THEM HAS A COMMITTED PRODUCER.

  `ladder`   FIXTURE-ONLY, no solver. Reads `fixtures/prove/attempts.txt` — the
             committed pin of every script the strategy sequence CAN emit for
             each property — plus the corpus census. It settles exactly one
             boundary and one attribute, and nothing else:

               * whether a property ever reached the solver at all (a property
                 that emitted no candidate script never did), and
               * which induction strategies the goal was a CANDIDATE for.

             Both are properties of the goal's SHAPE, so the fixture determines
             them; neither needs a verdict. This mode exists because the only
             instrument that answers the rest costs the same nine hours the cold
             conformance re-derivation does, and a third of the answer does not
             require paying it. THIS MODE IS RUNNABLE TODAY.

  a JSONL    one record per non-proven property (the shape `load` below
  sweep      enforces), carrying the prover's own `propOutcome` plus per-attempt
             solver telemetry recovered from its OATH_PROVE_CALIBRATE and
             OATH_PROVE_SPLIT seams. Only this can say what z3 ANSWERED, so only
             this can separate refuted from countermodel-withheld from
             budget-exhausted from solver-unknown.

             REQUIRED FIELDS. `hash` and `prop_index` are the pair everything
             reconciles on; `name` and `prop_name` are DISPLAY labels and are
             validated against the census, because a stale one attributes a
             result to the wrong definition or detaches a tester falsification
             while every hash-keyed check passes. `status` is one of
             proven|refuted|invalidated|unknown. **`rlimit` is the budget the
             sweep ran at, a positive integer, identical on every record** — no
             budget, no report. Each entry of `attempts` carries `strategy`,
             `verdict`, `consumed`, `reason` and `budget`; `strategy` is required
             because a `sat` on a direct attempt refutes the goal while a `sat`
             on an induction subgoal does not, and without it the two are the
             same record. **`budget` is that ATTEMPT's rlimit and is NOT the
             record's `rlimit`**: the lemma-free probe and an induction-eligible
             direct attempt both run at 4M inside a sweep whose nominal budget is
             400M, so a `canceled` there exhausts 4M and reporting it against the
             record's budget inflates a cheap bail into a spent full budget.

             **NO PRODUCER FOR IT IS COMMITTED.** One was written and run at the
             pinned `proveRlimit`; it re-proves the whole non-proven set through
             every strategy and was deleted rather than committed, because a
             nine-hour sweep is not an instrument this repository can afford to
             offer. What is kept here is the FORMAT and the classification, which
             are the durable part: a bounded producer — the same seams at
             `proveDirectRlimit` rather than `proveRlimit` — emits the same
             records at a cost that fits a single command, and would then report
             its budget alongside every figure, because a 4M `unknown` is not a
             400M `unknown`. Until such a producer exists, the modes below have
             no input in this tree and cannot be run.

WHAT `ladder` DOES NOT SETTLE, stated because the reverse reading is the
tempting one: it does not classify the properties that DID reach the solver. For
those it reports the strategies attempted and stops. "Reached the solver" is not
a category of failure; it is the absence of one.

This script adds no evidence in either mode; it only partitions.

THE CATEGORIES ARE DERIVED FROM WHAT THE PROVER CAN WITNESS, not from what a
reader would like to know. Two consequences are worth stating up front because
they change what the output means:

  * RLIMIT EXHAUSTION IS NOT AN ENVIRONMENTAL TIMEOUT. rlimit is a deterministic
    work counter, so spending it produces a VALID non-verdict that is identical
    on every machine. The genuinely environmental class is `capHit` — wall-clock
    cap, memout, missing telemetry — which the kernel reports as `invalidated`,
    a different status. Merging the two would report a reproducible fact about a
    pinned budget as an accident of the host.

  * "OUTSIDE STRUCTURAL INDUCTION'S REACH" IS NOT A PEER OF THE OTHERS. Whether
    the goal had a datatype-typed binder at all is an ATTRIBUTE of the goal that
    co-occurs with budget exhaustion and with solver incompleteness alike, so it
    is cross-tabulated rather than made a competing bucket. A property with no
    datatype binder was never a candidate for induction in the first place;
    that is a different statement from "induction was tried and failed", and the
    kernel collapses both into one sentence.
    THIS GOVERNS `ladder` TOO, and more sharply: candidacy is the ONLY thing the
    fixture can say about a goal that reached the solver, which makes it exactly
    the input a reader is most likely to mistake for an outcome.

Every record lands in exactly one category, by the priority order in CATEGORIES.

    cd oath && OATH_CENSUS_OUT=/tmp/census.json go test -run TestCorpusCensus -count=1

    python3 scripts/prove-reasons.py ladder /tmp/census.json      # no solver

The remaining modes read a JSONL sweep, for which no producer is committed (see
above). They are the specification of what such a producer must emit. All four
REQUIRE the census — it carries the alias map and the tester's falsifications,
and without it they would be wrong rather than unavailable:

**AND THEIR OUTPUT IS NOT YET EVIDENCE, WHICH IS A STATEMENT ABOUT THIS FILE AND
NOT ABOUT THE FORMAT.** `ladder` runs against a committed fixture and reconciles
against the census; these four have never been run against a real producer,
because the one that existed drove the nine-hour sweep and was deleted rather
than committed. Every fail-open found in them so far — six across three review
rounds, each a field that could be absent or malformed and was read into a
category that looked like a measurement — was found by READING, never by running.
So the validation below is a specification that has been reviewed, not an
instrument that has been witnessed, and the first real sweep should be treated as
a test OF THIS FILE as much as of the corpus. Do not quote a number these modes
produce until that has happened.

    python3 scripts/prove-reasons.py all.jsonl summary  /tmp/census.json
    python3 scripts/prove-reasons.py all.jsonl table    /tmp/census.json
    python3 scripts/prove-reasons.py all.jsonl evidence /tmp/census.json
    python3 scripts/prove-reasons.py all.jsonl check    /tmp/census.json
"""

import json
import sys
from collections import Counter, defaultdict
from pathlib import Path

# The one fixed sentence the kernel returns when every strategy ran and none
# discharged the goal. Any OTHER detail on an `unknown` is a translation bail,
# and the detail names the construct that could not be encoded.
EXHAUSTED = "no direct proof; induction did not discharge"

# The only strategies whose `sat` is a statement about the ORIGINAL goal, and so
# the only ones that can yield a (withheld) countermodel. AUTHORITY: the two
# sites in oath/prove.go that build `propOutcome{status: "refuted"}` — the direct
# attempt and the direct fallback. An induction or lexicographic `sat` is a
# subgoal not discharging, and a `lemma-free` `sat` is not acted on at all.
REFUTING_STRATEGIES = {"direct", "direct-fallback"}

# Solver verdicts an attempt may carry. `capHit` is the environmental abort and
# is a verdict here because the prover's own telemetry emits it as one.
KNOWN_VERDICTS = {"unsat", "sat", "unknown", "capHit"}

# (key, heading, one-line meaning). Order IS the classification priority.
CATEGORIES = [
    ("proved-but-withheld", "Proved, but withheld as contradicting a counterexample",
     "the goal discharges, yet the deterministic tester holds a concrete counterexample for it — "
     "the kernel refuses to record an SMT proof that contradicts a witnessed falsification"),
    ("proves-on-attempt", "Proves when attempted",
     "the store records no proof, but the property discharges from the state the store itself records"),
    ("refuted", "Refuted — demonstrated non-theorem",
     "z3 returned a countermodel on an unquantified goal; the property is false"),
    ("countermodel-withheld", "Countermodel found, verdict withheld",
     "z3 returned `sat` but the goal is quantified, so the kernel refuses to call it refuted"),
    ("translation-bail", "Never reached the solver",
     "the goal could not be encoded and NO script was attempted; no SMT verdict of any kind exists for it"),
    ("late-translation-bail", "Encoding failed after the solver had already run",
     "some scripts were attempted and a later strategy then failed to encode — the goal DID reach the "
     "solver, so this is not the same claim as the row above and must not be pooled with it"),
    ("environmentally-invalidated", "Environmentally invalidated",
     "a wall-clock cap, memout or lost telemetry voided an attempt (`capHit`) — the only genuinely environmental class"),
    ("budget-exhausted", "Solver did not converge within the pinned rlimit",
     "z3 spent the whole deterministic budget and answered `unknown` with reason `canceled`"),
    ("solver-unknown", "Solver returned `unknown` for another reason",
     "z3 stopped short of the budget without deciding; its own reason string is the evidence"),
]
CATEGORY_KEYS = [c[0] for c in CATEGORIES]


def attempts(rec):
    return rec.get("attempts") or []


def classify(rec):
    """Exactly one category per record, by the priority above."""
    st = rec["status"]
    if st == "proven":
        # The kernel only withholds a proof it actually obtained (prove.go:2150),
        # so this is checked on `proven` and nowhere else.
        return "proved-but-withheld" if rec.get("_tester_falsified") else "proves-on-attempt"
    if st == "refuted":
        return "refuted"
    if st == "invalidated":
        return "environmentally-invalidated"
    # st == "unknown" from here.
    if rec.get("detail", "") != EXHAUSTED:
        # A bail is only "never reached the solver" if NOTHING was attempted.
        # A bail arriving after some scripts ran is a different fact, and the
        # `translation-bail` heading would state the stronger one. The corpus has
        # zero of these today (measured, see the experiment record) — which is a
        # fact about this corpus, not a property of the prover, so the classifier
        # has to be able to say it rather than assume it away.
        return "translation-bail" if not attempts(rec) else "late-translation-bail"
    # A `sat` ONLY refutes the original goal when it came from a direct attempt.
    # oath/prove.go turns `sat` into `refuted` at exactly two sites — the direct
    # attempt (:1589) and the direct fallback (:2003) — and nowhere else. A `sat`
    # on an induction or lexicographic SUBGOAL says that constructor case did not
    # discharge; a `sat` on `lemma-free` is not acted on at all. Pooling those
    # into `countermodel-withheld` would report "z3 found a countermodel to your
    # property, and we withheld it" about a property z3 never refuted — the
    # strongest wrong claim this table can make.
    if any(a["verdict"] == "sat" and a["strategy"] in REFUTING_STRATEGIES
           for a in attempts(rec)):
        return "countermodel-withheld"
    if any(a["verdict"] == "unknown" and a["reason"] == "canceled" for a in attempts(rec)):
        return "budget-exhausted"
    return "solver-unknown"


def evidence_of(rec, cat):
    """The property-level evidence for the category assigned."""
    ats = attempts(rec)
    if cat in ("proves-on-attempt", "proved-but-withheld"):
        # THE MAXIMUM OVER THE LADDER, NOT THE COST OF THE PROOF, and it is
        # labelled that way because the two differ whenever an earlier strategy
        # burned more than the one that eventually succeeded — direct search
        # exhausting the budget and induction then discharging cheaply is the
        # ordinary case, not a corner. Nothing in the record marks which attempt
        # won, so attributing the maximum to `method` would report a failed
        # attempt's consumption as the price of the proof. The narrower claim is
        # the one the data supports.
        spent = max((a["consumed"] for a in ats), default=-1)
        ev = f"discharged by `{rec.get('method','?')}`; {spent} rlimit units is the MAX over the whole ladder, not this strategy's cost"
        if cat == "proved-but-withheld":
            ev += "; tester counterexample stands"
        return ev
    if cat == "refuted":
        return "countermodel: " + " ".join(rec.get("detail", "").split())[:220]
    if cat == "countermodel-withheld":
        # Count only the `sat`s that are countermodels of the ORIGINAL goal —
        # the same filter classify() applies. Counting subgoal `sat`s here would
        # inflate the evidence for the category they are not evidence for.
        n = sum(1 for a in ats if a["verdict"] == "sat" and a["strategy"] in REFUTING_STRATEGIES)
        sub = sum(1 for a in ats if a["verdict"] == "sat" and a["strategy"] not in REFUTING_STRATEGIES)
        ev = f"{n} direct attempt(s) answered `sat`; goal is quantified so the refutation is discarded"
        if sub:
            ev += f" (plus {sub} subgoal `sat`(s), which are not countermodels of the goal)"
        return ev
    if cat in ("translation-bail", "late-translation-bail"):
        n = len(ats)
        return rec.get("detail", "") + (f" — after {n} attempt(s)" if n else "")
    if cat == "environmentally-invalidated":
        return rec.get("detail", "")
    if cat == "budget-exhausted":
        # NAME THE BUDGET EACH ATTEMPT ACTUALLY RAN AT, not the sweep's. A 4M
        # lemma-free bail and a 400M direct exhaustion are both `canceled`, and
        # collapsing them into "cancelled at the rlimit" reports the cheap one as
        # the expensive one — two different experiments under one figure.
        c = [a for a in ats if a["reason"] == "canceled"]
        by = sorted({a["budget"] for a in c})
        spent = max((a["consumed"] for a in c), default=-1)
        at = ", ".join(f"{b}" for b in by) or "?"
        return (f"{len(c)} attempt(s) cancelled, at rlimit {at}; max spent {spent}"
                + ("" if len(by) <= 1 else " — MIXED budgets, so this row spans more than one experiment"))
    reasons = sorted({a["reason"] for a in ats if a["verdict"] == "unknown"})
    return "z3 reason-unknown: " + (", ".join(f"`{r}`" for r in reasons if r) or "*(empty)*")


def load(path, census):
    """Records, validated against the census and pinned to one budget.

    THREE THINGS ARE CHECKED HERE THAT RECONCILIATION CANNOT SEE, because
    `check()` compares (hash, prop_index) pairs and a record can carry the right
    pair with the wrong everything else:

      * `rlimit` — the budget the sweep ran at. REQUIRED, and required to be the
        SAME on every record. A 4M `unknown` is not a 400M `unknown`, so a mixed
        sweep produces a `budget-exhausted` count that quantifies over two
        different experiments while looking like one; and a sweep that records no
        budget produces figures nobody can reproduce. This is the project's rule
        that every figure names its budget, enforced at the point the figures are
        built rather than asked for in prose.
      * `prop_name` against the census. Tester falsifications are keyed by
        property NAME in the meta, so a stale label silently detaches a
        falsification and files `proved-but-withheld` as `proves-on-attempt` —
        the inversion of the single most interesting row. The label is validated,
        and the falsification is then looked up by INDEX, which is what the
        reconciliation already pins.
      * duplicates, which mean overlapping shards.
    """
    recs = [json.loads(l) for l in Path(path).read_text().splitlines() if l.strip()]
    if not recs:
        raise SystemExit(f"{path} has no records; a report over an empty sweep is not a report")

    # falsified-by-index, derived from the census's own prop names for the object
    # rather than from the record's label.
    fals_idx, names_idx, live_names = {}, {}, {}
    for o in census["objects"]:
        fnames = set(o.get("falsified", ()))
        for p in o["props"]:
            names_idx[(o["hash"], p["index"])] = p["name"]
            fals_idx[(o["hash"], p["index"])] = p["name"] in fnames
            live_names[(o["hash"], p["index"])] = set(o["live_names"]) | {o["canonical_name"]}

    budgets, seen, bad = set(), {}, []
    for r in recs:
        k = (r["hash"], r["prop_index"])
        if k in seen:
            raise SystemExit(f"duplicate record for {r['name']} prop {r['prop_index']} — shards overlap")
        seen[k] = r
        # Presence is not enough: `"rlimit": null` passes a key check, becomes the
        # sole budget, and every report then names a budget of `None` — which is
        # exactly the "no budget, no report" invariant satisfied in form and
        # broken in substance. It must be a positive number.
        rl = r.get("rlimit")
        if not isinstance(rl, int) or isinstance(rl, bool) or rl <= 0:
            bad.append(f"{r['name']}[{r['prop_index']}] has rlimit {rl!r}; every figure must name a "
                       "positive integer budget, and a key holding null names nothing")
        else:
            budgets.add(rl)
        want = names_idx.get(k)
        if want is not None and r.get("prop_name") != want:
            bad.append(f"{r['name']}[{r['prop_index']}] is labelled {r.get('prop_name')!r} "
                       f"but the census calls it {want!r} — a stale label detaches its falsification")
        # `status` and `attempts` are what classify() branches on, and BOTH fail
        # OPEN if absent: a missing `attempts` list makes an attempt that was
        # cancelled or answered `sat` come out as `solver-unknown`, and a status
        # outside the enum falls through the `unknown` branch. Each produces a
        # category that looks like a measurement. Refuse instead.
        if r.get("status") not in ("proven", "refuted", "invalidated", "unknown"):
            bad.append(f"{r['name']}[{r['prop_index']}] has status {r.get('status')!r}, "
                       "outside proven|refuted|invalidated|unknown — it would fall through "
                       "to the `unknown` branch and be classified as if the solver had run")
        elif not isinstance(r.get("attempts"), list):
            # EVERY status needs the list, not just `unknown`. A `proven` record
            # without it renders "-1 rlimit units" and a `refuted` one loses the
            # attempt that refuted it — evidence invented from a missing field
            # rather than a truncated sweep being refused. Gating this on
            # `unknown` was the same fail-open the check exists to prevent.
            bad.append(f"{r['name']}[{r['prop_index']}] ({r['status']}) has no `attempts` list; "
                       "every record carries telemetry, and its absence is silently rendered "
                       "as evidence rather than refused")
        elif r["status"] in ("proven", "refuted", "invalidated") and not r["attempts"]:
            # EMPTY IS LEGAL ONLY FOR A PRE-SOLVER TRANSLATION BAIL, which is an
            # `unknown`. A `proven` with no attempts renders "-1 rlimit units", a
            # `refuted` loses the very attempt that refuted it, and an
            # `invalidated` claims an environmental abort of nothing. The list-type
            # check above passes all three, because `[]` is a list — right TYPE,
            # wrong FACT, which is the same gap that let the exhaustion case
            # through before it was checked separately.
            bad.append(f"{r['name']}[{r['prop_index']}] is {r['status']} with ZERO attempts; "
                       "only a pre-solver translation bail has no telemetry, and rendering "
                       "this would invent evidence (`-1 rlimit units`, an empty countermodel, "
                       "an invalidation with no attempt) from a missing field")
        elif r["status"] == "unknown" and not isinstance(r.get("detail"), str):
            # `classify()` compares `detail` against EXHAUSTED to tell a
            # translation bail from a solver that ran. A missing `detail`
            # defaults to something that is not EXHAUSTED, so the record comes out
            # as a translation bail — asserting an ENCODING FAILURE on the
            # strength of absent telemetry. Missing data cannot establish that.
            bad.append(f"{r['name']}[{r['prop_index']}] is unknown with `detail` "
                       f"{r.get('detail')!r}; classification compares it against the exhaustion "
                       "sentence, so an absent detail is silently read as a translation bail — "
                       "an encoding failure claimed from missing telemetry")
        elif r["status"] == "unknown" and r.get("detail") == EXHAUSTED and not r["attempts"]:
            # The exhaustion sentence SAYS every strategy ran, so zero telemetry
            # contradicts the record's own detail — lost telemetry, not a
            # solver that stopped short. An empty list is the right TYPE and the
            # wrong FACT, which is why the type check above does not reach it.
            bad.append(f"{r['name']}[{r['prop_index']}] reports the exhaustion detail but records "
                       "ZERO attempts; that is lost telemetry, and classifying it `solver-unknown` "
                       "would assert a solver verdict no attempt supports")
        # The record's `name` is only DISPLAYED, never joined on — but a display
        # name is what a reader attributes the result to, and a stale one
        # attributes it to the wrong definition while every hash-keyed check
        # passes. It must be a live name of the object it claims.
        # `has_datatype_binder` is READ BY THE CROSS-TAB AS A THREE-WAY IDENTITY
        # CHECK (is True / is False / is None), so any other value — `0`, `"yes"`,
        # a missing key on a record whose direct phase DID run — falls through all
        # three and the row silently stops summing to its own category total. The
        # reconciliation cannot see it: it compares (hash, prop_index) pairs, and
        # a record can carry the right pair and the wrong flag. Absent is legal
        # only when no direct-phase attempt ran, which is when the prover has
        # nothing to report.
        ran_direct = any(a.get("strategy") in REFUTING_STRATEGIES
                         for a in (r.get("attempts") or []))
        dtb = r.get("has_datatype_binder")
        if ran_direct and not isinstance(dtb, bool):
            bad.append(f"{r['name']}[{r['prop_index']}] ran a direct-phase attempt but has "
                       f"`has_datatype_binder` {dtb!r}; the cross-tab tests it by identity "
                       "against True/False/None, so any other value drops the record from "
                       "every column while the totals still look reconciled")
        elif not ran_direct and dtb is not None and not isinstance(dtb, bool):
            bad.append(f"{r['name']}[{r['prop_index']}] has `has_datatype_binder` {dtb!r}, "
                       "which is neither a boolean nor absent")
        if k in live_names and r.get("name") not in live_names[k]:
            bad.append(f"{r['hash'][:12]}[{r['prop_index']}] is displayed as {r.get('name')!r}, "
                       f"which is not a live name of that object ({sorted(live_names[k])}) — "
                       "the table would attribute it to the wrong definition")
        # Attempt telemetry: the three fields must be PRESENT and WELL-TYPED, plus
        # `strategy`, without which a `sat` cannot be told from a subgoal `sat`.
        # A typo'd verdict (`satt`) otherwise passes a key check and comes out as
        # `solver-unknown` — a malformed measurement rendered as a finding.
        for a in (r.get("attempts") or []):
            miss = {"verdict", "consumed", "reason", "strategy", "budget"} - set(a)
            if miss:
                bad.append(f"{r['name']}[{r['prop_index']}] has an attempt missing "
                           f"{sorted(miss)}; classification reads all five")
                break
            if a["verdict"] not in KNOWN_VERDICTS:
                bad.append(f"{r['name']}[{r['prop_index']}] has attempt verdict {a['verdict']!r}, "
                           f"outside {sorted(KNOWN_VERDICTS)}")
                break
            if a["strategy"] not in KNOWN_STRATEGIES:
                bad.append(f"{r['name']}[{r['prop_index']}] has attempt strategy {a['strategy']!r}, "
                           f"outside {sorted(KNOWN_STRATEGIES)}")
                break
            if not isinstance(a["consumed"], int) or isinstance(a["consumed"], bool):
                bad.append(f"{r['name']}[{r['prop_index']}] has non-integer `consumed` "
                           f"{a['consumed']!r}; the evidence line does arithmetic on it")
                break
            if not isinstance(a["reason"], str):
                bad.append(f"{r['name']}[{r['prop_index']}] has non-string `reason` {a['reason']!r}")
                break
            # THE RECORD'S `rlimit` IS THE SWEEP'S NOMINAL BUDGET; AN ATTEMPT'S
            # IS NOT THE SAME NUMBER. `oath/prove.go` runs the lemma-free probe
            # at proveLemmaFreeRlimit and an induction-eligible direct attempt at
            # proveDirectRlimit — both 4M — inside a sweep whose nominal budget is
            # proveRlimit at 400M. So a `canceled` on one of those is exhaustion
            # of 4M, and attributing it to the record's 400M reports a cheap
            # preliminary bail as if the full budget had been spent. That is the
            # project's own rule — every figure names ITS budget — failing at the
            # one place the number is actually consumed.
            if not isinstance(a["budget"], int) or isinstance(a["budget"], bool) or a["budget"] <= 0:
                bad.append(f"{r['name']}[{r['prop_index']}] has `budget` {a['budget']!r} on a "
                           f"{a['strategy']!r} attempt; it must be the positive rlimit THAT ATTEMPT "
                           "ran at, which is not the record's sweep budget for the 4M strategies")
                break
            # A `canceled` BELOW its budget is not exhaustion — it is an external
            # cancel, and the kernel says so in as many words (prove.go, attempt
            # validity, SPEC §7.2 / #29): a non-verdict is an OUTCOME only when
            # "the budget was genuinely spent (reason 'canceled' with consumed >=
            # rlimit; z3 overshoots by a few units)", and `canceled` below budget
            # is listed alongside memout as "the ENVIRONMENT talking". Classifying
            # one as `budget-exhausted` would report a signal as a deterministic
            # fact — the machine-dependence this format exists to abolish.
            # `capHit` IS THE ENVIRONMENTAL ABORT, and the kernel reflects it in
            # the STATUS: such a record is `invalidated`. An `unknown` carrying a
            # capHit attempt is telemetry contradicting itself, and classify()
            # would file it as `solver-unknown` — reporting a wall-clock or memout
            # death as a deterministic statement about the goal, which is the one
            # substitution this whole taxonomy exists to prevent.
            if a["verdict"] == "capHit" and r["status"] != "invalidated":
                bad.append(f"{r['name']}[{r['prop_index']}] is {r['status']} but carries a "
                           f"{a['strategy']!r} attempt with verdict `capHit`; capHit is the "
                           "environmental abort and the kernel reports it as `invalidated`, so "
                           "this record contradicts itself and would be classified as a "
                           "deterministic solver outcome")
                break
            if a["reason"] == "canceled" and a["consumed"] < a["budget"]:
                bad.append(f"{r['name']}[{r['prop_index']}] has a {a['strategy']!r} attempt "
                           f"`canceled` at {a['consumed']} of {a['budget']} rlimit — BELOW its "
                           "budget, which the kernel classifies as an external cancel and hence "
                           "environmental, not exhaustion; the record should have been "
                           "`invalidated`, and classifying it `budget-exhausted` asserts "
                           "determinism the telemetry denies")
                break
    if len(budgets) > 1:
        bad.append(f"records mix rlimit budgets {sorted(budgets)}; one report cannot span two experiments")
    if bad:
        raise SystemExit("SWEEP REJECTED:\n  - " + "\n  - ".join(bad))

    for r in recs:
        r["_tester_falsified"] = fals_idx.get((r["hash"], r["prop_index"]), False)
        r["_cat"] = classify(r)
    return recs, budgets.pop()


BUDGET_NOTE = (
    "Every figure below was measured at an rlimit of {b}. **A verdict is a "
    "function of (script bytes, solver version, rlimit)**, so a non-verdict at "
    "one budget is not a non-verdict at another: an `unknown` here says the "
    "solver did not converge WITHIN {b}, and says nothing about what it would "
    "do with more. The budget is part of the claim, not a footnote to it."
)


def summary(recs, aliases, budget):
    per_obj = Counter(r["_cat"] for r in recs)
    # Per-NAME: an aliased object's properties count once per live name.
    per_name = Counter()
    for r in recs:
        per_name[r["_cat"]] += len(aliases.get(r["hash"], [r["name"]]))
    out = [BUDGET_NOTE.format(b=budget), "",
           "| category | per-object | per-name | meaning |", "|---|---:|---:|---|"]
    for key, head, meaning in CATEGORIES:
        out.append(f"| **{head}** | {per_obj[key]} | {per_name[key]} | {meaning} |")
    out.append(f"| **total** | **{sum(per_obj.values())}** | **{sum(per_name.values())}** | |")
    out.append("")

    # Cross-tabulation: was structural induction even applicable?
    out.append("Structural induction's applicability, cross-tabulated against the categories "
               "where a solver actually ran. `has_datatype_binder` is the prover's own "
               "`hasDT` flag; it is absent when the direct phase never ran.")
    out.append("")
    out.append("| category | no datatype binder (induction never applicable) | has one | not reported |")
    out.append("|---|---:|---:|---:|")
    for key, head, _ in CATEGORIES:
        rs = [r for r in recs if r["_cat"] == key]
        no = sum(1 for r in rs if r.get("has_datatype_binder") is False)
        yes = sum(1 for r in rs if r.get("has_datatype_binder") is True)
        na = sum(1 for r in rs if r.get("has_datatype_binder") is None)
        # THE ROW MUST ACCOUNT FOR EVERY RECORD IN ITS CATEGORY. `load()` refuses
        # the values that would break this, so reaching here means the invariant
        # was broken some OTHER way — and a cross-tab that quietly omits records
        # is the failure this whole file is written against. Assert rather than
        # render: a wrong number is worse than no table.
        if no + yes + na != len(rs):
            raise SystemExit(
                f"CROSS-TAB REJECTED: category {key!r} holds {len(rs)} record(s) but the row "
                f"counts {no + yes + na} ({no} no / {yes} yes / {na} not-reported); some record's "
                "`has_datatype_binder` is neither True, False nor absent, so the row would "
                "under-report while every reconciliation check still passed")
        out.append(f"| {head} | {no} | {yes} | {na} |")
    return "\n".join(out)


def table(recs, aliases, budget):
    out = [BUDGET_NOTE.format(b=budget), "",
           "| # | definition | property | live names | category | evidence |",
           "|---:|---|---|---|---|---|"]
    order = {k: i for i, k in enumerate(CATEGORY_KEYS)}
    rs = sorted(recs, key=lambda r: (order[r["_cat"]], r["name"], r["prop_index"]))
    for i, r in enumerate(rs, 1):
        names = ", ".join(f"`{n}`" for n in aliases.get(r["hash"], [r["name"]]))
        ev = evidence_of(r, r["_cat"]).replace("|", "\\|")
        out.append(f"| {i} | `{r['name']}` | `{r['prop_name']}` | {names} | {r['_cat']} | {ev} |")
    return "\n".join(out)


def evidence(recs, aliases, budget):
    """The distinct evidence strings per category, with counts."""
    out = [BUDGET_NOTE.format(b=budget), ""]
    for key, head, meaning in CATEGORIES:
        rs = [r for r in recs if r["_cat"] == key]
        out.append(f"### {head} — {len(rs)} per-object")
        out.append("")
        if not rs:
            out.append("*(none)*")
            out.append("")
            continue
        out.append(f"{meaning}.")
        out.append("")
        groups = defaultdict(list)
        for r in rs:
            groups[evidence_of(r, key)].append(f"{r['name']}.{r['prop_name']}")
        out.append("| evidence | properties | count |")
        out.append("|---|---|---:|")
        for ev, props in sorted(groups.items(), key=lambda kv: (-len(kv[1]), kv[0])):
            shown = ", ".join(f"`{p}`" for p in sorted(props)[:6])
            if len(props) > 6:
                shown += f", … (+{len(props)-6})"
            out.append(f"| {ev.replace('|', chr(92)+'|')} | {shown} | {len(props)} |")
        out.append("")
    return "\n".join(out)


def check(recs, census_path, stream=None):
    """Assert the measurement's universe IS the census's non-proven set.

    RUN BY EVERY MODE, NOT ONLY BY `check`. As a mode it reports on stdout; as
    the precondition of a report it writes to stderr (pass stream=sys.stderr) so
    the markdown on stdout stays pasteable. Making it optional was the defect:
    an empty, truncated or shard-missing sweep still renders a table of plausible
    corpus totals, and a table that silently describes 3 records while claiming
    to classify 141 is worse than no table. A report that cannot state its
    universe does not get rendered.

    This is the assertion the tables rest on. Without it the categories could
    reconcile perfectly to a population that is not the one being described —
    the wrong-universe defect one level up from the classification itself. It
    compares (object hash, property index) pairs, never names: names alias, and
    a name-keyed comparison would silently accept a measurement that skipped an
    aliased object or counted it twice.
    """
    c = json.loads(Path(census_path).read_text())
    want = {(o["hash"], p["index"]) for o in c["objects"] for p in o["props"] if not p["proven"]}
    got = {(r["hash"], r["prop_index"]) for r in recs}
    problems = []
    for k in sorted(want - got):
        problems.append(f"census has non-proven {k[0][:12]}[{k[1]}] but NO record measured it")
    for k in sorted(got - want):
        problems.append(f"a record measured {k[0][:12]}[{k[1]}] which the census does not list as non-proven")

    per_obj = Counter(r["_cat"] for r in recs)
    if sum(per_obj.values()) != len(recs):
        problems.append("categories do not sum to the record count")
    for r in recs:
        if r["_cat"] not in CATEGORY_KEYS:
            problems.append(f"{r['name']}.{r['prop_name']} fell into unknown category {r['_cat']!r}")

    # Per-name projection, built by RESOLVING every live name and keeping
    # duplicate resolutions — never by a set union, which would collapse the
    # aliases this projection exists to preserve.
    aliases = {o["hash"]: o["live_names"] for o in c["objects"]}
    per_name = sum(len(aliases.get(r["hash"], [r["name"]])) for r in recs)
    name_unproven = sum(1 for n in c["names"] for p in n["props"] if not p["proven"])
    if per_name != name_unproven:
        problems.append(f"per-name projection is {per_name} but the census counts {name_unproven} non-proven per-name properties")

    w = stream or sys.stdout
    print(f"records                       : {len(recs)}", file=w)
    print(f"census non-proven (per-object): {len(want)}", file=w)
    print(f"census non-proven (per-name)  : {name_unproven}", file=w)
    print(f"per-name projection of records: {per_name}", file=w)
    print(f"categories                    : {dict(per_obj)}", file=w)
    if problems:
        print("\nRECONCILIATION FAILED:", file=w)
        for p in problems:
            print("  -", p, file=w)
        sys.exit(1)
    print("\nRECONCILES — every non-proven property of the census is classified exactly once.", file=w)


# ---------------------------------------------------------------------------
# `ladder` — the fixture-only derivation. No solver, no JSONL, no store walk.
# ---------------------------------------------------------------------------

ROOT = Path(__file__).resolve().parent.parent
ATTEMPTS = ROOT / "fixtures/prove/attempts.txt"

# The strategies whose PRESENCE is a statement about the goal's shape. Ordered
# as SPEC §7.2 reaches them. `direct`, `direct-fallback` and `lemma-free` are
# deliberately absent: every enumerated goal emits those, so their presence
# distinguishes nothing.
CANDIDACY = ["induction", "lexicographic", "recursion-induction"]

# EVERY strategy label the fixture may carry. The AUTHORITY is the set of
# `c.solve("<strategy>", ...)` call sites in oath/prove.go; this is a pinned copy
# of it, and the check below is what makes the copy safe to hold:
#
#     grep -o 'c\.solve("[a-z-]*"' oath/prove.go | sed 's/.*solve("//;s/"//' | sort -u
#
# A typo'd row (`inductoin`) would otherwise be accepted silently and change the
# answer rather than fail: the property still counts as having reached the
# solver, but drops out of the `induction` row and reappears as direct-only. So
# an unrecognised label is REFUSED. A genuinely new strategy in prove.go will
# also be refused, loudly, which is the correct direction — it is a demand to
# re-read this list, not a defect.
KNOWN_STRATEGIES = {"direct", "direct-fallback", "lemma-free",
                    "induction", "lexicographic", "recursion-induction"}


def read_attempts(path=ATTEMPTS):
    """(name, prop_index) -> the strategies the sequence can emit for it.

    FAILS on a missing fixture rather than returning an empty map. An absent
    attempts.txt would otherwise make every property look like it emitted no
    script — which is this mode's evidence of NEVER REACHING THE SOLVER, so the
    deleted fixture would be reported as a corpus-wide finding.
    """
    p = Path(path)
    if not p.exists():
        sys.exit(f"{p} is missing; without it this mode's silence is not evidence")
    out = defaultdict(set)
    rows, unknown = 0, defaultdict(int)
    for ln in p.read_text().splitlines():
        if not ln.strip() or ln.startswith("#"):
            continue
        f = ln.split("\t")
        if len(f) < 5:
            sys.exit(f"malformed row in {p}: {ln!r}")
        if f[2] not in KNOWN_STRATEGIES:
            unknown[f[2]] += 1
        out[(f[0], int(f[1]))].add(f[2])
        rows += 1
    if unknown:
        sys.exit(f"{p} carries unrecognised strategy label(s) "
                 + ", ".join(f"{k!r} ({v} row(s))" for k, v in sorted(unknown.items()))
                 + f"; known: {sorted(KNOWN_STRATEGIES)}. A label this script does not "
                   "recognise silently changes the candidacy tables instead of failing, "
                   "so the run is refused. If prove.go gained a strategy, add it here.")
    if not rows:
        sys.exit(f"{p} has no rows; nothing below would be measured")
    return out, rows


def ladder(census_path, attempts_path=ATTEMPTS):
    """Partition the census's non-proven set by what the script fixture settles.

    THE UNIVERSE IS THE CENSUS'S, NOT THE FIXTURE'S. attempts.txt is keyed by
    live NAME and holds a row only where a script exists, so taking its key set
    as the population would silently drop precisely the class this mode is here
    to count. The census decides who is in; the fixture decides which side of the
    boundary they fall.

    Both projections are reported because a verdict is a fact about the HASH
    while the fixture is written per NAME: the per-object column dedupes aliases,
    the per-name column keeps every live resolution.
    """
    c = json.loads(Path(census_path).read_text())
    att, nrows = read_attempts(attempts_path)

    live = defaultdict(list)          # hash -> its live names
    for n in c["names"]:
        live[n["hash"]].append(n["name"])
    name_hash = {n["name"]: n["hash"] for n in c["names"]}

    problems = []

    # CONTROL 1 — no orphan rows. Every (name, prop) the fixture pins must be a
    # property the census knows about, or the two artefacts describe different
    # corpora and neither column below means what it says.
    census_pairs = {(n["name"], p["index"]) for n in c["names"] for p in n["props"]}
    for k in sorted(set(att) - census_pairs):
        problems.append(f"attempts.txt pins {k[0]}[{k[1]}], which the census does not list")

    # CONTROL 2 — aliases must agree. A script is a function of the DEF, so two
    # names for one object must pin the same ladder. If they diverge, the
    # per-object projection below is choosing arbitrarily between them.
    for h, ns in live.items():
        if len(ns) < 2:
            continue
        obj = next(o for o in c["objects"] if o["hash"] == h)
        for pi in range(obj["prop_count"]):
            if len({frozenset(att.get((n, pi), ())) for n in ns}) > 1:
                problems.append(f"aliases {ns} disagree on the ladder for prop {pi}")

    def strategies(h, pi):
        for n in live[h]:
            if (n, pi) in att:
                return att[(n, pi)]
        return set()

    # CONTROL 3 — the signal must not be noise. "Emitted no script" is only
    # evidence of a translation bail if PROVEN properties never look that way:
    # a proven property necessarily reached the solver, so a proven property with
    # no pinned script would mean the fixture is incomplete rather than that the
    # goal was untranslatable.
    for o in c["objects"]:
        for p in o["props"]:
            if p["proven"] and not strategies(o["hash"], p["index"]):
                problems.append(
                    f"{o['canonical_name']}[{p['index']}] is PROVEN yet pins no script — "
                    "absence cannot mean 'never reached the solver'")

    # THERE IS NO CONTROL 4, AND THE REASON IS THE POINT.
    #
    # Control 3 catches a fixture that lost a PROVEN property's rows. It cannot
    # catch one that lost a NON-PROVEN property's rows, and neither can anything
    # else here: `attempts.txt` HAS NO ROW MEANING "enumerated, emitted nothing".
    # Absence is overloaded — it spells "the goal bailed" and "nobody looked"
    # with the same zero rows — so a stale fixture inflates the left-hand column
    # silently. Asserting harder does not help; the format has no place to put
    # the distinction, which is the artefact that is actually missing.
    #
    # A def-level version of the check was written and DELETED rather than
    # weakened, because it is unsound on the committed fixture: 12 definitions
    # legitimately pin no rows at all (every goal bails — `hmac-sha256`, `lam`),
    # so "has properties but no rows" is a true description of correct data. It
    # fired on the unmutated fixture, which is how it was caught.
    #
    # What closes this is the ENUMERATING PRODUCER, not the fixture: re-running
    # the ladder in enumerate mode and reading the outcome detail for each of the
    # properties counted here. That cross-check and its result are recorded in
    # docs/experiments/issue-68.md section 6. It is a fact about this corpus, not
    # a guarantee about the format, and it has to be re-run when either moves.
    #
    # In place of a control, the shape of the left-hand column is REPORTED below
    # (how many of its members belong to definitions that pin nothing at all,
    # versus definitions that pin rows for other properties). That is a
    # description, not a gate — but a fixture that lost rows would have to
    # distort it, so it is the cheapest thing that makes staleness visible.

    obj_np = [(o["hash"], p["index"]) for o in c["objects"] for p in o["props"] if not p["proven"]]
    name_np = [(n["name"], p["index"]) for n in c["names"] for p in n["props"] if not p["proven"]]

    def side(strats):
        return "no-candidate-script" if not strats else "reached-the-solver"

    per_obj = Counter(side(strategies(h, pi)) for h, pi in obj_np)
    per_name = Counter(side(att.get((n, pi), set())) for n, pi in name_np)

    # RECONCILIATION. The partition is built from attempts.txt and must sum back
    # to the census's own non-proven totals — the whole point of deriving the two
    # numbers from different artefacts. Neither total is written down here; a
    # literal would be correct exactly once.
    if sum(per_obj.values()) != len(obj_np):
        problems.append("per-object partition does not sum to the census's non-proven set")
    if sum(per_name.values()) != len(name_np):
        problems.append("per-name partition does not sum to the census's non-proven set")
    expanded = sum(len(live[h]) for h, _ in obj_np)
    if expanded != len(name_np):
        problems.append(f"per-object set expands by aliases to {expanded}, census per-name says {len(name_np)}")

    src = Path(attempts_path)
    try:
        src = src.relative_to(ROOT)
    except ValueError:
        pass  # a fixture from outside the repo (a control run); print it as given
    out = []
    out.append(f"Derived from `{src}` ({nrows} pinned candidate scripts) and the census. "
               "NO SOLVER RAN.")
    out.append("")
    out.append("| | per-object | per-name | what the fixture establishes |")
    out.append("|---|---:|---:|---|")
    out.append(f"| **Emitted no candidate script — never reached the solver** | "
               f"{per_obj['no-candidate-script']} | {per_name['no-candidate-script']} | "
               "the strategy sequence bailed before its first solver call, so no SMT verdict "
               "of any kind exists for the property — settled, and settled without z3 |")
    out.append(f"| **Reached the solver** | {per_obj['reached-the-solver']} | {per_name['reached-the-solver']} | "
               "at least one script was emitted; WHAT z3 ANSWERED IS NOT IN THIS FIXTURE, so these stay "
               "unclassified until some instrument records the solver's answer |")
    out.append(f"| **total non-proven (census)** | **{len(obj_np)}** | **{len(name_np)}** | |")
    out.append("")
    # A fully proven corpus is the SUCCESS state of this project, not an error:
    # both denominators go to zero and there is nothing to partition. Say so
    # rather than dividing — a percentage of an empty set has no reading.
    def pct(k, tot):
        return "n/a (no non-proven properties)" if not tot else f"{100.0 * k / tot:.1f}%"

    if not obj_np and not name_np:
        out.append("**Nothing to partition: the census records no non-proven property.** "
                   "Every figure below would quantify over an empty set.")
    else:
        out.append(f"Settled without running z3: **{per_obj['no-candidate-script']}/{len(obj_np)} "
                   f"({pct(per_obj['no-candidate-script'], len(obj_np))}) per-object**, "
                   f"**{per_name['no-candidate-script']}/{len(name_np)} "
                   f"({pct(per_name['no-candidate-script'], len(name_np))}) per-name**.")
    out.append("")

    # The left-hand column's SHAPE — see the note where control 4 would have been.
    # A def pinning nothing at all is the ordinary case here (every goal bails);
    # a def pinning rows for some properties and not others is the interesting
    # one, because that is the shape a partially stale fixture would also take.
    pinned_names = {k[0] for k in att}
    whole = [(n, pi) for n, pi in name_np if not att.get((n, pi)) and n not in pinned_names]
    partial = [(n, pi) for n, pi in name_np if not att.get((n, pi)) and n in pinned_names]
    if name_np:
        out.append(f"Of the {per_name['no-candidate-script']} per-name properties in the first row, "
                   f"{len(whole)} belong to definitions that pin NO script for any property, and "
                   f"{len(partial)} to definitions that pin scripts for other properties"
                   + (f" ({', '.join(f'`{n}`[{pi}]' for n, pi in sorted(partial))})" if partial else "")
                   + ". The second group is the one a partially stale fixture would swell; it is "
                     "listed rather than counted so a reader can check it by hand.")
        out.append("")

    # Candidacy, CROSS-TABULATED — never a competing bucket. Restricted to the
    # rows where a solver actually ran, because "was induction applicable" is
    # vacuous for a goal that never got that far.
    out.append("Induction candidacy, derived from the strategies the fixture records for each "
               "goal. A strategy appears iff the goal's shape admits it, so this is the same "
               "predicate the prover's `hasDT` seam reports — read off a committed artefact "
               "instead of a nine-hour run. It is an ATTRIBUTE of the goal, cross-tabulated "
               "against the partition above rather than competing with it.")
    out.append("")
    out.append("| candidate for | per-object | per-name |")
    out.append("|---|---:|---:|")
    reached_o = [(h, pi) for h, pi in obj_np if strategies(h, pi)]
    reached_n = [(n, pi) for n, pi in name_np if att.get((n, pi))]
    for s in CANDIDACY:
        o = sum(1 for h, pi in reached_o if s in strategies(h, pi))
        n = sum(1 for k in reached_n if s in att[k])
        out.append(f"| `{s}` | {o} | {n} |")
    o = sum(1 for h, pi in reached_o if not (strategies(h, pi) & set(CANDIDACY)))
    n = sum(1 for k in reached_n if not (att[k] & set(CANDIDACY)))
    out.append(f"| *none — direct attempts only* | {o} | {n} |")
    out.append(f"| **reached the solver** | **{len(reached_o)}** | **{len(reached_n)}** |")
    out.append("")
    out.append("The rows are not exclusive: a goal with a datatype binder and an ordered pair "
               "is a candidate for both. The combinations:")
    out.append("")
    out.append("| combination | per-object | per-name |")
    out.append("|---|---:|---:|")
    combo_o = Counter(" + ".join(s for s in CANDIDACY if s in strategies(h, pi)) or "direct only"
                      for h, pi in reached_o)
    combo_n = Counter(" + ".join(s for s in CANDIDACY if s in att[k]) or "direct only"
                      for k in reached_n)
    for k in sorted(set(combo_o) | set(combo_n), key=lambda x: (-combo_o[x], x)):
        out.append(f"| {k} | {combo_o[k]} | {combo_n[k]} |")

    if problems:
        print("\n".join(out))
        print("\nRECONCILIATION FAILED:")
        for p in problems:
            print("  -", p)
        sys.exit(1)
    out.append("")
    out.append(f"Reconciles: the partition sums to the census's {len(obj_np)} per-object / "
               f"{len(name_np)} per-name non-proven properties, no fixture row names a property "
               "the census does not hold, aliased objects pin identical ladders, and every "
               "PROVEN property pins at least one script.")
    return "\n".join(out)


def main():
    if len(sys.argv) >= 2 and sys.argv[1] == "ladder":
        if len(sys.argv) < 3:
            sys.exit(f"usage: {sys.argv[0]} ladder <census.json> [attempts.txt]")
        print(ladder(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else ATTEMPTS))
        return
    if len(sys.argv) < 3 or sys.argv[2] not in ("summary", "table", "evidence", "check"):
        sys.exit(f"usage: {sys.argv[0]} <records.jsonl> summary|table|evidence|check <census.json>\n"
                 f"   or: {sys.argv[0]} ladder <census.json> [attempts.txt]")
    # THE CENSUS IS REQUIRED BY ALL FOUR MODES, not just `check`. It was optional
    # once, and optional meant SILENTLY WRONG rather than absent: without it the
    # alias map is empty, so `rot`/`rot-f` — one object, two live names — are
    # counted once instead of twice and the per-NAME column reports per-OBJECT
    # numbers under a per-name heading; and the falsified map is empty, so a
    # property the deterministic tester refuted is filed `proves-on-attempt`
    # instead of `proved-but-withheld`, which inverts the single most interesting
    # row in the table. Both degrade into a plausible number, which is the
    # failure mode this repository is least able to notice.
    if len(sys.argv) < 4:
        sys.exit(f"{sys.argv[2]} needs the census: {sys.argv[0]} <records.jsonl> {sys.argv[2]} <census.json>\n"
                 "without it aliases collapse and tester falsifications are invisible, and the "
                 "output would be wrong rather than missing")
    c = json.loads(Path(sys.argv[3]).read_text())
    aliases = {o["hash"]: o["live_names"] for o in c["objects"]}
    recs, budget = load(sys.argv[1], c)
    if sys.argv[2] == "check":
        check(recs, sys.argv[3])
        return
    # Reconcile BEFORE rendering. `check` exits non-zero on any mismatch, so a
    # report is emitted only over a sweep whose universe is the census's — the
    # rule that a figure names its population, enforced rather than trusted.
    check(recs, sys.argv[3], stream=sys.stderr)
    print({"summary": summary, "table": table, "evidence": evidence}[sys.argv[2]](recs, aliases, budget))


if __name__ == "__main__":
    main()
