#!/usr/bin/env python3
"""Render the corpus census emitted by TestCorpusCensus as markdown.

This script does NO measurement. Every figure it prints comes from the JSON that
`oath/corpus_census_test.go` writes out of the committed store, or from
`fixtures/prove/outcomes.json`; nothing here computes a verdict, and nothing here
reads `codebase/meta/` (a walk of that directory answers a question about the
store's HISTORY, not about the corpus).

The split into sections is deliberate: each block of the write-up carries the
exact command that regenerates it, so a reader can re-derive any table on its own
rather than trusting the surrounding prose.

    cd oath && OATH_CENSUS_OUT=/tmp/census.json go test -run TestCorpusCensus -count=1
    python3 scripts/corpus-census.py /tmp/census.json summary
    python3 scripts/corpus-census.py /tmp/census.json objects
    python3 scripts/corpus-census.py /tmp/census.json names
    python3 scripts/corpus-census.py /tmp/census.json reconcile
"""

import json
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# The guarantee ladder as SPEC/`Guarantee.Level` defines it. This list fixes the
# ORDER rows are printed in and nothing else: the renderer prints every level it
# actually observes, so a level the kernel gains and this list has not is still
# counted and still shown, and the column totals go on adding up to the row
# count. A fixed list that decided WHICH levels to print would be a duplicate of
# the kernel's vocabulary, and would drift from it silently.
LEVEL_ORDER = ["proven", "tested", "falsified", "asserted"]


def levels_in(*collections):
    """Every level observed, known ones first in ladder order, then any others."""
    seen = {x["level"] for c in collections for x in c}
    return [lv for lv in LEVEL_ORDER if lv in seen] + sorted(seen - set(LEVEL_ORDER))


def props_cell(props):
    """Every property of one row, each marked with its own verdict.

    A count would collapse exactly the thing being enumerated, so the verdicts
    are spelled out per property: `+` proven, `-` not proven.
    """
    if not props:
        return "*(none)*"
    return " ".join(f"`{'+' if p['proven'] else '-'}{p['name']}`" for p in props)


def summary(c):
    o, n = c["objects"], c["names"]
    out = []
    out.append(f"- source declarations (`examples/*.oath` + `apps/*/*.oath`, kernel reader): **{c['source_declarations']}**")
    out.append(f"- live names (`codebase/names.json`): **{c['live_names']}**")
    out.append(f"- distinct objects those names resolve to: **{c['live_objects']}**")
    out.append(f"- objects reached by more than one live name: **{len(c['aliased_objects'])}**")
    out.append("")
    for h, names in sorted(c["aliased_objects"].items(), key=lambda kv: kv[1]):
        row = next(x for x in o if x["hash"] == h)
        out.append(f"  - `{h[:12]}` ({row['kind']}, canonical `{row['canonical_name']}`) ← {', '.join('`%s`' % x for x in names)}")
    out.append("")

    ok, on = Counter(x["kind"] for x in o), Counter(x["kind"] for x in n)
    lk, ln = Counter(x["level"] for x in o), Counter(x["level"] for x in n)
    out.append("| | per-object | per-name |")
    out.append("|---|---:|---:|")
    out.append(f"| rows | {len(o)} | {len(n)} |")
    for k in ("data", "defn"):
        out.append(f"| kind `{k}` | {ok[k]} | {on[k]} |")
    for lv in levels_in(o, n):
        flag = "" if lv in LEVEL_ORDER else "  ← not in the known guarantee ladder"
        out.append(f"| level `{lv}` | {lk[lv]} | {ln[lv]} |{flag}")
    po = sum(x["prop_count"] for x in o)
    pn = sum(len(x["props"]) for x in n)
    vo = sum(x["proven_count"] for x in o)
    vn = sum(sum(p["proven"] for p in x["props"]) for x in n)
    out.append(f"| properties | {po} | {pn} |")
    out.append(f"| properties proven | {vo} | {vn} |")
    out.append(f"| properties not proven | {po - vo} | {pn - vn} |")
    out.append("")
    out.append(f"The per-name column exceeds the per-object column by **{pn - po} properties "
               f"({vn - vo} of them proven)**. That difference is the aliasing, and it is the whole "
               f"reason both columns are printed.")
    return "\n".join(out)


def objects(c):
    out = ["| # | object | canonical | live names | kind | declaration site(s) | level | proven/props | term | mutants | properties |",
           "|---:|---|---|---|---|---|---|---:|---|---:|---|"]
    for i, x in enumerate(sorted(c["objects"], key=lambda r: r["canonical_name"]), 1):
        names = ", ".join(f"`{v}`" for v in x["live_names"])
        src = "<br>".join(f"`{s['file'].replace('../', '')}:{s['line']}`" for s in x["declaration_sites"])
        mut = f"{x['mutants_killed']}/{x['mutants_total']}" if x["mutants_total"] else "—"
        if x["waived_mutants"]:
            mut += f" (+{x['waived_mutants']} waived)"
        out.append(
            f"| {i} | `{x['hash'][:12]}` | `{x['canonical_name']}` | {names} | {x['kind']} | {src} | "
            f"{x['level']} | {x['proven_count']}/{x['prop_count']} | {x.get('termination') or '—'} | {mut} | "
            f"{props_cell(x['props'])} |")
    return "\n".join(out)


def names(c):
    out = ["| # | name | object | alias of | kind | source | level | proven/props | properties (as this name spells them) |",
           "|---:|---|---|---|---|---|---|---:|---|"]
    canon = {x["hash"]: x["canonical_name"] for x in c["objects"]}
    for i, x in enumerate(c["names"], 1):
        pv = sum(p["proven"] for p in x["props"])
        alias = f"`{canon[x['hash']]}`" if x["is_alias"] else "—"
        src = f"{x['source_file'].replace('../', '')}:{x['source_line']}"
        out.append(
            f"| {i} | `{x['name']}` | `{x['hash'][:12]}` | {alias} | {x['kind']} | `{src}` | "
            f"{x['level']} | {pv}/{len(x['props'])} | {props_cell(x['props'])} |")
    return "\n".join(out)


def reconcile(c):
    """Compare the census against fixtures/prove/outcomes.json.

    The fixture is the ledger `check-doc-numbers` reads, so which universe it
    enumerates decides what every prose figure in this repo means.
    """
    oc = json.loads((ROOT / "fixtures/prove/outcomes.json").read_text())["definitions"]
    by_name = {x["name"]: x for x in c["names"]}
    out = []
    out.append(f"- `fixtures/prove/outcomes.json` rows: **{len(oc)}**")
    out.append(f"- distinct object hashes among those rows: **{len({x['hash'] for x in oc})}**")
    live, ocn = set(by_name), {x["name"] for x in oc}
    out.append(f"- rows whose name is not live: **{len(ocn - live)}**")
    out.append(f"- live names with no row: **{len(live - ocn)}**")
    absent = sorted(live - ocn)
    withprops = [m for m in absent if by_name[m]["props"]]
    out.append(f"  - of those, carrying at least one property: **{len(withprops)}** {withprops if withprops else ''}")
    out.append(f"  - by kind: {dict(Counter(by_name[m]['kind'] for m in absent))}")
    out.append("")

    # The fixture stores prop_count/proven_count ALONGSIDE the per-property list
    # they summarise. Totalling the summaries without checking them against the
    # list would let a stale pair of numbers be reported as agreeing with the
    # store while the enumeration underneath says otherwise — the same defect the
    # census asserts away inside the store's own metadata.
    inconsistent = []
    for r in oc:
        if r["prop_count"] != len(r["props"]):
            inconsistent.append(f"`{r['name']}`: prop_count={r['prop_count']} but props lists {len(r['props'])}")
        pv = sum(1 for p in r["props"] if p["proven"])
        if r["proven_count"] != pv:
            inconsistent.append(f"`{r['name']}`: proven_count={r['proven_count']} but props marks {pv} proven")
    out.append(f"- fixture rows whose summary counts disagree with their own property list: **{len(inconsistent)}**")
    out.extend(f"  - {d}" for d in inconsistent)
    out.append("")

    dis = []
    for r in oc:
        s = by_name.get(r["name"])
        if s is None:
            continue
        if r["hash"] != s["hash"]:
            dis.append(f"`{r['name']}`: fixture hash {r['hash'][:12]}, store {s['hash'][:12]}")
        elif r["level"] != s["level"]:
            dis.append(f"`{r['name']}`: fixture level {r['level']}, store {s['level']}")
        elif [(p["name"], p["proven"]) for p in r["props"]] != [(p["name"], p["proven"]) for p in s["props"]]:
            # Compared as ORDERED lists, not sets. Properties are positional in
            # the hashed Def and carry no name of their own, so two properties
            # may share a spelling; a set comparison would call [true, false]
            # equal to [false, true] and would swallow a dropped duplicate.
            dis.append(f"`{r['name']}`: per-property verdicts differ")
    out.append(f"- rows disagreeing with the store on hash, level or any per-property verdict: **{len(dis)}**")
    out.extend(f"  - {d}" for d in dis)
    out.append("")
    # Totals derived from the per-property lists, never from the summary fields.
    fp = sum(len(x["props"]) for x in oc)
    fv = sum(1 for x in oc for p in x["props"] if p["proven"])
    ff = sum(1 for x in oc if x["props"] and all(p["proven"] for p in x["props"]))
    out.append(f"- fixture rows totalling (counted from the per-property lists): **{fp} properties**, "
               f"**{fv} proven**, **{ff} definitions fully proven**")
    out.append("- the same figures computed per-OBJECT from the census: "
               f"**{sum(x['prop_count'] for x in c['objects'])} properties**, "
               f"**{sum(x['proven_count'] for x in c['objects'])} proven**, "
               f"**{sum(1 for x in c['objects'] if x['prop_count'] and x['proven_count'] == x['prop_count'])} objects fully proven**")
    return "\n".join(out)


def main():
    if len(sys.argv) != 3 or sys.argv[2] not in ("summary", "objects", "names", "reconcile"):
        sys.exit(f"usage: {sys.argv[0]} <census.json> summary|objects|names|reconcile")
    c = json.loads(Path(sys.argv[1]).read_text())
    print({"summary": summary, "objects": objects, "names": names, "reconcile": reconcile}[sys.argv[2]](c))


if __name__ == "__main__":
    main()
