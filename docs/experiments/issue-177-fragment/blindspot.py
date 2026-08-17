#!/usr/bin/env python3
"""#177 — join the corpus census with the prover's translation-bail outcomes and
emit the `--implies` blind spot: its members, per-object and per-name, and its
counts.

WHAT IS BEING MEASURED, precisely. `oath find --implies` does NOT prove a
candidate's stored properties. It APPENDS the caller's query property to the
candidate and proves that synthetic property, with `self` bound to the candidate
(`oath/api.go:1195-1256`). A goal the translator cannot build never reaches a
solver, so the mode returns NO VERDICT — which the caller cannot distinguish
from the artifact not existing.

So the blind spot is NOT "definitions whose own properties do not translate".
A candidate with an untranslatable law may still be reachable by a different
query. What puts a candidate beyond EVERY query is its own BODY, which any
mentioning query inlines. The probe measures that, and this file reports the
stored-property view separately, as its own statistic — the two differ in both
directions on this corpus, so treating either as an approximation of the other
would misreport the set. Reading the candidate's stored properties as the goal
is the first thing this experiment got wrong, and it is recorded here because
the counts looked entirely reasonable while it was wrong.

THE TWO INPUTS AND WHY NEITHER IS SUFFICIENT ALONE.

  census.json    oath/corpus_census_test.go. The UNIVERSE: every live name in
                 `codebase/names.json`, every object those names resolve to, and
                 the store's own verdicts. It knows nothing about translation.
  fragment.json  oath/fragment_probe_test.go. The prover's OWN enumeration seam,
                 run live over the same store: how many scripts the strategy
                 sequence emits for each goal, and — for a goal that emits none —
                 the translator's verbatim refusal. It knows nothing about the
                 corpus sources or the aliasing.

THIS SCRIPT ADDS NO EVIDENCE. It joins, controls, and partitions.

WHY NOT `fixtures/prove/attempts.txt` AS THE BAIL SOURCE. The committed fixture
holds a row only where a script exists, so absence spells both "the goal bailed"
and "nobody looked" — scripts/prove-reasons.py says exactly that at the place
where its fourth control would have gone, and names the enumerating producer as
the artefact that closes it. fragment.json IS that producer, run live. The
fixture is still read here, but as a RECONCILIATION target: if the live bail set
and the fixture's no-row set disagree, that is a fact about the fixture's
freshness and it is reported rather than swallowed.

    python3 docs/experiments/issue-177-fragment/blindspot.py CENSUS FRAGMENT

Exits non-zero, printing every failure, if any control fails.
"""

import json
import re
import sys
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
ATTEMPTS = ROOT / "fixtures" / "prove" / "attempts.txt"

# REQUIRED MEMBERS, PINNED AS (NAME, HASH) PAIRS. #177's falsifier record
# (docs/experiments/issue-74-falsifier.md) established the blind spot EXISTS by
# naming two members of it, each reached by running `oath find --implies` and
# reading the NO VERDICT reason. A derivation of the same set that does not
# contain them is measuring something else, and would be indistinguishable from a
# correct one by its counts alone. So this is a hard control, not a spot-check:
# these are the only externally-witnessed rows this instrument can be checked
# against.
#
# THE PAIR, not the name and not the hash alone, because the witness is a fact
# about both. `apiFindImplies` iterates `st.Names()`, so an object no live name
# reaches is not a candidate at all and a hash-only pin would check something the
# mode never looks at; while a name-only pin follows a mutable pointer, and
# repointing it to a DIFFERENT unreachable object would let the control pass on a
# witness never taken for that object. Checking both distinguishes the three
# cases, and only one of them indicts the instrument:
#
#   the name is gone, or points elsewhere -> the WITNESS is stale: retake it by
#                                            running `oath find --implies` again
#   the pair holds but the object is not
#   in the derived blind spot             -> the INSTRUMENT disagrees with an
#                                            external observation
REQUIRED = {
    "record-field": "d7f4757624895cf6131ab1138c8e010d64f50f5ae1aa99fdd25698305e6b468e",
    "json-string-value": "e84c7faa721c257c7426bbec454cfd2f5fb1869002fb0755b0eebed59cb095ae",
}

# EVERY BAIL REASON MUST MATCH EXACTLY ONE PATTERN, and an unmatched one FAILS.
#
# The patterns are transcribed from the prover's own fmt.Errorf sites, cited
# below, so a reader can check the list against the source rather than against
# this file's memory of it. They are grouped by the QUESTION #177 asks: does the
# refusal turn on the SUBJECT'S CARRIER (a different representation might fix
# it) or on something a representation change cannot touch — a term form the
# translator does not model, a callee it refuses to model, an arity.
#
# An unlisted cause is an AMENDMENT TRIGGER, never something disposed of at the
# point of use. This is #68 §9.3's last row, applied to a different question:
# a cause with no row is a gap in the criterion, and silently bucketing it as
# "other" is how a fabricated exclusion gets in.
CAUSES = [
    (r'^"(?P<what>.+)" terms are outside the provable fragment$',
     "unsupported-term-form", "prove.go:1012"),
    (r'^(?P<what>.+) is outside the provable fragment \(trusted crypto primitive\)$',
     "opaque-primitive", "prove.go:828"),
    (r'^type (?P<what>.+) is outside the provable fragment$',
     "unsupported-type", "prove.go:482"),
    (r'^primitive (?P<what>.+) is outside the provable fragment for Float$',
     "unsupported-float-primitive", "prove.go:715"),
    (r'^primitive (?P<what>.+) is outside the provable fragment$',
     "unsupported-primitive", "prove.go:835"),
    (r'^(?P<what>.+) is untranslatable over (?P<sort>.+)$',
     "untranslatable-operand-sort", "prove.go:831"),
    (r'^partial application is outside the provable fragment$',
     "partial-application", "prove.go:998"),
    (r'^application head "(?P<what>.+)" is outside the provable fragment$',
     "unsupported-application-head", "prove.go:1002"),
    (r'^data reference is outside the provable fragment$',
     "data-reference", "prove.go:1019"),
    (r'^(?P<what>.+) must be fully applied to inline$',
     "partial-application", "prove.go:1047"),
    (r'^(?P<what>.+) must be fully applied$',
     "partial-application", "prove.go:1027"),
]
CAUSES = [(re.compile(p), tag, site) for p, tag, site in CAUSES]


def classify(reason):
    """Return (tag, site) for a bail reason, or (None, None) if unmatched."""
    for rx, tag, site in CAUSES:
        if rx.match(reason):
            return tag, site
    return None, None


def read_attempts(path):
    """(name, prop_index) -> set of strategies, from the committed fixture."""
    att = defaultdict(set)
    rows = 0
    for line in Path(path).read_text().splitlines():
        if not line or line.startswith("#"):
            continue
        f = line.split("\t")
        att[(f[0], int(f[1]))].add(f[2])
        rows += 1
    return att, rows


def main(census_path, fragment_path):
    census = json.loads(Path(census_path).read_text())
    frag = json.loads(Path(fragment_path).read_text())
    problems = []

    # ------------------------------------------------------------------ join --
    # CONTROL 1 — the two artefacts must describe the SAME corpus. A verdict is a
    # fact about a hash, so a join across two snapshots would attribute a
    # translation result to a definition that was never probed while every count
    # below still added up. Set equality on hashes, on live names, and on the
    # per-object property arity: three independent ways for a drift to show.
    cobj = {o["hash"]: o for o in census["objects"]}
    fobj = {o["hash"]: o for o in frag["objects"]}
    for h in sorted(set(cobj) - set(fobj)):
        problems.append(f"census has object {h[:12]} ({cobj[h]['canonical_name']}), the probe does not")
    for h in sorted(set(fobj) - set(cobj)):
        problems.append(f"the probe has object {h[:12]} ({fobj[h]['canonical_name']}), the census does not")
    cnames = {n["name"]: n["hash"] for n in census["names"]}
    fnames = {n: o["hash"] for o in frag["objects"] for n in o["live_names"]}
    if cnames != fnames:
        for n in sorted(set(cnames) ^ set(fnames)):
            problems.append(f"live name {n!r} is in one artefact and not the other")
        for n in sorted(set(cnames) & set(fnames)):
            if cnames[n] != fnames[n]:
                problems.append(f"live name {n!r} resolves differently in the two artefacts")
    for h in sorted(set(cobj) & set(fobj)):
        cp, fp = cobj[h]["prop_count"], fobj[h]["prop_count"]
        if cp != fp:
            problems.append(f"{cobj[h]['canonical_name']} ({h[:12]}): census says {cp} properties, the probe {fp}")

    # CONTROL 2 — the probe must have visited every property it claims to have
    # measured, with no index missing and none invented.
    for h, o in sorted(fobj.items()):
        idx = sorted(p["index"] for p in (o["props"] or []))
        if idx != list(range(o["prop_count"])):
            problems.append(f"{o['canonical_name']}: probed property indices {idx}, expected 0..{o['prop_count'] - 1}")

    # CONTROL 3 — every bail carries a cause this file can name. An unmatched
    # reason means the translator grew a refusal the criterion does not cover.
    # It covers BOTH refusal channels. The synthetic query is the authority for
    # the blind-spot partition, and it can meet a refusal no stored property in
    # this corpus reaches — so checking only the stored-property reasons would
    # let an unclassified cause into the very table this control exists to
    # protect, while still printing "All controls passed".
    unmatched = Counter()
    for o in fobj.values():
        for p in (o["props"] or []):
            if p["bail"]:
                if not p.get("bail_reason"):
                    problems.append(f"{o['canonical_name']}[{p['index']}] bailed with no recorded reason")
                elif classify(p["bail_reason"])[0] is None:
                    unmatched[p["bail_reason"]] += 1
        if o.get("query") == "bail":
            r = o.get("query_reason") or ""
            if not r:
                problems.append(f"{o['canonical_name']}: the synthetic query bailed with no recorded reason")
            elif classify(r)[0] is None:
                unmatched[r] += 1
    for r, k in unmatched.most_common():
        problems.append(f"AMENDMENT REQUIRED: {k} bail(s) with an unlisted cause: {r!r}")

    # STOP HERE IF THE JOIN ITSELF IS UNSOUND. Controls 1-3 establish that the
    # two artefacts describe one corpus and that every bail carries a nameable
    # cause; nothing below is meaningful otherwise, and running on would crash
    # on the first missing hash. A traceback and a reported control failure look
    # the same to a caller checking an exit code — this repo's own rule about a
    # probe that cannot tell a broken gate from a firing one — so the diagnosis
    # is printed rather than left to the stack trace.
    if problems:
        print("\n".join(["## CONTROL FAILURES — the join is unsound and no partition was computed", ""]
                        + [f"- {p}" for p in problems]), file=sys.stderr)
        return 1

    # ------------------------------------------------------- the partition ----
    # THE BLIND SPOT IS THE SYNTHETIC-QUERY VERDICT, NOT THE STORED PROPERTIES.
    #
    # `apiFindImplies` appends the CALLER'S query property to the candidate and
    # proves that (api.go:1195-1256). A candidate's own untranslatable property
    # therefore does not make it unreachable — a different query might translate.
    # What puts a candidate beyond every query is its own BODY, which any
    # mentioning query inlines. The probe measures exactly that.
    #
    # The two are genuinely different sets on this corpus, in BOTH directions,
    # which is why the stored-property view is reported below as its own
    # statistic rather than as an approximation of this one.
    #
    # `not-a-candidate` is a data declaration. `apiFindImplies` skips every
    # `d.K != "func"` before any proving happens (api.go:1176), so such an object
    # is not in the mode's universe at all — a different thing from being in it
    # and unreachable, and merging the two would inflate a fragment figure with
    # a phenomenon that has nothing to do with the fragment.
    # A POLYMORPHIC BAIL IS NOT A BAIL. The probe instantiates type parameters at
    # `Int`, so a translating instantiation is a positive existential witness
    # while a bailing one establishes nothing about the other instantiations. Such
    # an object is INCONCLUSIVE and is kept out of the blind-spot count, not
    # qualified inside it with a footnote — a count whose members carry different
    # epistemic status is a count nobody can use. (No object in the committed
    # corpus is in this class today; the branch exists because the classifier must
    # be right for the corpus that has one, and a class with no members is exactly
    # the one that gets silently mis-specified.)
    def cls(o):
        if o["query"] == "reachable":
            return "reachable"
        if o["query"] == "bail":
            return "inconclusive-polymorphic" if o.get("polymorphic") else "unreachable"
        if o["def_kind"] != "func":
            return "not-a-candidate"
        return "NOT MEASURED"

    obj_class = {h: cls(o) for h, o in fobj.items()}

    # CONTROL — every FUNCTION must have been measured. A data declaration that
    # the probe could not build a query for is expected and named above; a
    # function that it could not is an unmeasured candidate, and reporting a
    # blind-spot count over a population with holes in it is the defect this
    # whole file is arranged to avoid.
    for h, k in sorted(obj_class.items()):
        if k == "NOT MEASURED":
            problems.append(
                f"{fobj[h]['canonical_name']} is a function the probe did not measure: "
                f"{fobj[h].get('query_reason', '(no reason given)')}")

    # The stored-property view, kept SEPARATE. It is what #68 §6 counts, it is
    # what reconciles with attempts.txt, and it answers a different question:
    # which of a definition's OWN laws the prover can reason about at all.
    def prop_cls(o):
        ps = o["props"] or []
        if not ps:
            return "no-properties"
        b = sum(1 for p in ps if p["bail"])
        if b == 0:
            return "all-translate"
        return "none-translate" if b == len(ps) else "some-translate"

    obj_prop_class = {h: prop_cls(o) for h, o in fobj.items()}

    # PER-OBJECT and PER-NAME are DIFFERENT STATISTICS and neither is a rounding
    # of the other. Translatability is a fact about the def, so the per-object
    # column is the one that describes what the corpus IS; the per-name column
    # describes what it OFFERS a searcher, and keeps every alias, because two
    # names for one blind object are two names a searcher can fail to find.
    per_obj = Counter(obj_class.values())
    per_name = Counter(obj_class[h] for h in cnames.values())

    prop_total = sum(len(o["props"] or []) for o in fobj.values())
    prop_bail = sum(1 for o in fobj.values() for p in (o["props"] or []) if p["bail"])
    name_prop_total = sum(len(fobj[h]["props"] or []) for h in cnames.values())
    name_prop_bail = sum(1 for h in cnames.values() for p in (fobj[h]["props"] or []) if p["bail"])

    # CONTROL 4 — the partition must sum back to the census's own universe. The
    # two numbers come from different artefacts on purpose; a literal expectation
    # here would be correct exactly once.
    if sum(per_obj.values()) != census["live_objects"]:
        problems.append("the per-object partition does not sum to the census's live-object count")
    if sum(per_name.values()) != census["live_names"]:
        problems.append("the per-name partition does not sum to the census's live-name count")

    # CONTROL 5 — the required members. See REQUIRED above.
    for want, wanth in sorted(REQUIRED.items()):
        live = cnames.get(want)
        if live is None:
            problems.append(
                f"WITNESS STALE: the name {want!r} is no longer live, so the external "
                f"observation this control rests on no longer describes the corpus. Re-take it "
                f"by running `oath find --implies` and update REQUIRED — do not simply drop it")
        elif live != wanth:
            problems.append(
                f"WITNESS STALE: {want!r} now resolves to {live[:12]}, not the witnessed "
                f"{wanth[:12]}. The falsifier's NO VERDICT was observed for the OLD object, so "
                f"it says nothing about this one. Re-take the witness and update REQUIRED")
        elif obj_class[live] != "unreachable":
            problems.append(
                f"REQUIRED member {want!r} ({wanth[:12]}) is not in the derived blind spot "
                f"(this derivation classifies it {obj_class[live]!r}) — the falsifier witnessed "
                f"`oath find --implies` returning NO VERDICT for this exact object, so a "
                f"derivation that omits it is measuring something other than the blind spot")

    # ------------------------------------- reconciliation with the fixture ----
    # NOT a control on this measurement: the live enumerator is the authority
    # and the fixture is the thing being checked. A divergence is a finding about
    # fixtures/prove/attempts.txt, reported here because this is the first
    # instrument in the tree that can see one.
    # A MISSING FIXTURE IS A CONTROL FAILURE, NOT A SKIPPED SECTION. Silently
    # omitting the reconciliation would let the report claim agreement it never
    # tested — the absent half looking exactly like the agreeing half.
    recon = []
    if not ATTEMPTS.exists():
        problems.append(f"{ATTEMPTS} is missing: the reconciliation cannot run, and a report "
                        "without it does not establish what this one claims")
    else:
        att, rows = read_attempts(ATTEMPTS)
        pinned = {k for k, v in att.items() if v}
        live_bail, live_ok = set(), set()
        for h, o in fobj.items():
            for n in o["live_names"]:
                for p in (o["props"] or []):
                    (live_bail if p["bail"] else live_ok).add((n, p["index"]))
        fixture_says_bail = live_ok - pinned          # live: emits scripts; fixture: no row
        fixture_says_ok = live_bail & pinned          # live: bails;         fixture: has rows
        # A THIRD DIRECTION, and the only one neither set above can show: rows
        # for a (name, property) the live corpus no longer has. Those keys are in
        # `pinned` and in neither live set, so a two-row reconciliation reports
        # exact agreement while the fixture still carries a removed definition.
        orphaned = pinned - (live_ok | live_bail)
        recon = [rows, sorted(fixture_says_bail), sorted(fixture_says_ok), sorted(orphaned)]

    # ------------------------------------------------------------- report -----
    out = []
    out.append("Derived from the corpus census (`oath/corpus_census_test.go`) joined onto the "
               "prover's live enumeration seam (`oath/fragment_probe_test.go`). NO SOLVER RAN; "
               "nothing was written to the store.")
    out.append("")
    out.append(f"Universe: **{census['live_names']} live names** over **{census['live_objects']} "
               f"live objects**, from `codebase/names.json`, carrying **{prop_total} properties** "
               f"per-object (**{name_prop_total}** per-name).")
    out.append("")

    out.append("## The blind spot — what `oath find --implies` cannot reach")
    out.append("")
    out.append("`apiFindImplies` appends the CALLER'S query property to each candidate and proves "
               "that synthetic property with `self` bound to the candidate (`oath/api.go:1195-1256`). "
               "So the question is not whether a candidate's own laws translate — it is whether ANY "
               "query mentioning the candidate can be translated, which turns on the candidate's "
               "BODY, since a mentioning query inlines it. That is what the probe measures: the "
               "minimal mentioning query, `(let [x (self b…)] true)`, cross-checked against "
               "`(== (self b…) (self b…))` wherever the return type is equatable.")
    out.append("")
    out.append("| class | per-object | per-name |")
    out.append("|---|---:|---:|")
    for k in ("unreachable", "inconclusive-polymorphic", "reachable", "not-a-candidate"):
        out.append(f"| **{k}** | {per_obj[k]} | {per_name[k]} |")
    out.append(f"| **total** | **{sum(per_obj.values())}** | **{sum(per_name.values())}** |")
    out.append("")
    out.append("`not-a-candidate` is a `data` declaration: `apiFindImplies` skips every non-`func` "
               "object before any proving happens, so it is outside the mode's universe rather than "
               "unreached within it. `inconclusive-polymorphic` is a polymorphic definition that "
               "bailed at the probe's concrete instantiation — which does not establish that every "
               "instantiation bails, so it is held out of the blind-spot count rather than counted "
               "with a caveat.")
    out.append("")

    unreach = sorted((fobj[h]["canonical_name"], h) for h in fobj if obj_class[h] == "unreachable")
    out.append(f"### Members — unreachable ({len(unreach)} objects)")
    out.append("")
    out.append("| object | live names | level | properties | the translator's refusal |")
    out.append("|---|---|---|---:|---|")
    for cname, h in unreach:
        o = fobj[h]
        poly = ""
        out.append(f"| `{cname}` | {', '.join('`' + n + '`' for n in o['live_names'])} | {o['level']} | "
                   f"{o['prop_count']} | `{o.get('query_reason', '')}`{poly} |")
    out.append("")

    out.append("### How this differs from the stored-property view")
    out.append("")
    out.append("The set below is NOT the set of definitions whose own properties fail to translate. "
               "The two differ in both directions on this corpus, which is why the second is reported "
               "separately rather than as an approximation of the first.")
    out.append("")
    a = {fobj[h]["canonical_name"] for h in fobj if obj_class[h] == "unreachable"}
    b_ = {fobj[h]["canonical_name"] for h in fobj if obj_prop_class[h] == "none-translate"}
    out.append("| | members |")
    out.append("|---|---|")
    out.append("| unreachable, yet **some or all of its own properties translate** | "
               + (", ".join(f"`{n}`" for n in sorted(a - b_)) or "—") + " |")
    out.append("| **none of its own properties translate**, yet a query CAN reach it | "
               + (", ".join(f"`{n}`" for n in sorted(b_ - a)) or "—") + " |")
    out.append("| both | " + (", ".join(f"`{n}`" for n in sorted(a & b_)) or "—") + " |")
    out.append("")

    out.append("## Properties the translator cannot build a goal for")
    out.append("")
    out.append("A separate statistic, and the one `docs/experiments/issue-68.md` §6 counts: which of "
               "a definition's OWN laws the prover can reason about at all. It does not decide "
               "`--implies` reachability, per the table above.")
    out.append("")
    out.append("| | per-object | per-name |")
    out.append("|---|---:|---:|")
    out.append(f"| **bailed in translation — no script, so this law is one the prover cannot "
               f"reason about at all** | {prop_bail} | {name_prop_bail} |")
    out.append(f"| translated — at least one script emitted | {prop_total - prop_bail} | {name_prop_total - name_prop_bail} |")
    out.append(f"| **total** | **{prop_total}** | **{name_prop_total}** |")
    out.append("")

    out.append("### The causes, verbatim from the prover")
    out.append("")
    out.append("| cause | tag | site | per-object properties |")
    out.append("|---|---|---|---:|")
    bycause = Counter()
    for o in fobj.values():
        for p in (o["props"] or []):
            if p["bail"]:
                bycause[p["bail_reason"]] += 1
    for reason, k in sorted(bycause.items(), key=lambda kv: (-kv[1], kv[0])):
        tag, site = classify(reason)
        out.append(f"| `{reason}` | {tag or '**UNLISTED**'} | {site or '—'} | {k} |")
    out.append("")

    out.append("### Definitions grouped by how many of their own properties translate")
    out.append("")
    out.append("| class | per-object | per-name |")
    out.append("|---|---:|---:|")
    pc_obj = Counter(obj_prop_class.values())
    pc_name = Counter(obj_prop_class[h] for h in cnames.values())
    for k in ("none-translate", "some-translate", "all-translate", "no-properties"):
        out.append(f"| **{k}** | {pc_obj[k]} | {pc_name[k]} |")
    out.append(f"| **total** | **{sum(pc_obj.values())}** | **{sum(pc_name.values())}** |")
    out.append("")

    for k in ("none-translate", "some-translate"):
        members = sorted((fobj[h]["canonical_name"], h, fobj[h]) for h in fobj if obj_prop_class[h] == k)
        out.append(f"#### Members — {k} ({len(members)} objects)")
        out.append("")
        if not members:
            out.append("*(none)*")
            out.append("")
            continue
        out.append("| object | live names | level | properties | bailed | causes | `--implies` |")
        out.append("|---|---|---|---:|---:|---|---|")
        for cname, h, o in members:
            ps = o["props"] or []
            bad = [p for p in ps if p["bail"]]
            tags = sorted({classify(p["bail_reason"])[0] or "UNLISTED" for p in bad})
            out.append(f"| `{cname}` | {', '.join('`' + n + '`' for n in o['live_names'])} | "
                       f"{o['level']} | {len(ps)} | {len(bad)} | {', '.join(tags)} | {obj_class[h]} |")
        out.append("")

    nop = sorted(fobj[h]["canonical_name"] for h in fobj if obj_prop_class[h] == "no-properties")
    out.append(f"#### Members — no-properties ({len(nop)} objects)")
    out.append("")
    out.append(", ".join(f"`{n}`" for n in nop) if nop else "*(none)*")
    out.append("")

    if recon:
        rows, fsb, fso, orph = recon
        out.append("## Reconciliation with `fixtures/prove/attempts.txt`")
        out.append("")
        out.append(f"The committed fixture pins {rows} candidate scripts. It records a row only "
                   "where a script exists, so its no-row set is a PROXY for the bail set; the live "
                   "enumeration above is the authority. Both disagreement directions are reported "
                   "because they indict different things.")
        out.append("")
        out.append("| direction | count | members |")
        out.append("|---|---:|---|")
        out.append(f"| live emits scripts, fixture pins none (fixture is STALE or incomplete) | {len(fsb)} | "
                   + (", ".join(f"`{n}`[{i}]" for n, i in fsb) if fsb else "—") + " |")
        out.append(f"| live bails, fixture pins scripts (fixture is AHEAD of the store) | {len(fso)} | "
                   + (", ".join(f"`{n}`[{i}]" for n, i in fso) if fso else "—") + " |")
        out.append(f"| fixture pins a row the live corpus has no property for (a REMOVED definition "
                   f"or property) | {len(orph)} | "
                   + (", ".join(f"`{n}`[{i}]" for n, i in orph) if orph else "—") + " |")
        out.append("")
        if not fsb and not fso and not orph:
            out.append("**The two agree exactly.** The fixture's no-row set and the live bail set "
                       "are the same set, so §6 of `docs/experiments/issue-68.md` may be read as a "
                       "translation-bail count at this commit rather than only as a no-script count.")
            out.append("")

    print("\n".join(out))

    if problems:
        print("\n".join(["", "## CONTROL FAILURES", ""] + [f"- {p}" for p in problems]), file=sys.stderr)
        return 1
    print("All controls passed.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    sys.exit(main(sys.argv[1], sys.argv[2]))
