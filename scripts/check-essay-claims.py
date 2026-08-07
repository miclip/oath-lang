#!/usr/bin/env python3
"""Gate the QUANTITATIVE PROSE CLAIMS on the website's essay and docs pages.

WHY THIS EXISTS. `make check-web-ledger` byte-diffs website/lib/outcomes.json
against fixtures/prove/outcomes.json, which keeps the browsable /corpus page
honest. But every number written into an essay is hardcoded JSX, and nothing
compared it to anything — so the site's "numbers read live from the machine's
ledger" claim was enforced for exactly one page. #93 found the consequence: an
essay contradicted the committed evidence it told readers to check.

WHAT IT CHECKS, and the distinction that makes it honest:

  DERIVED   the number is a fact about the CURRENT corpus. The expected text is
            recomputed here from the fixture on every run, so when the ledger
            moves, the page must move with it or this fails.
  PINNED    the number is a HISTORICAL or CAPTURED measurement that the current
            corpus does not reproduce and should not be expected to. It is
            checked for presence and exact wording, never against the ledger,
            and it must name where it came from.

Collapsing those two would be worse than no gate: it would either force a
historical figure to track a ledger that never described it, or quietly license
a live figure to go stale. A pinned claim is not a weaker derived claim.

The check is deliberately TEXT-EXACT (after whitespace normalization): a claim
that is reworded, split, or deleted fails just as loudly as one whose number is
wrong. A number is only as good as the sentence around it — "43 mutants killed"
and "43 of 43 mutants killed" are different claims.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


# ---------------------------------------------------------------- the ledger

class Ledger:
    """The authoritative sources. Every DERIVED expectation is computed from
    these, never written down twice."""

    def __init__(self):
        self.outcomes = json.loads(
            (ROOT / "fixtures/prove/outcomes.json").read_text())
        self.defs = self.outcomes["definitions"]
        self._analyses = {}

    def d(self, name):
        hits = [x for x in self.defs if x["name"] == name]
        if len(hits) != 1:
            die(f"outcomes.json has {len(hits)} entries for {name!r}; expected 1")
        return hits[0]

    def analysis(self, name):
        if name not in self._analyses:
            p = ROOT / "fixtures/analyses" / f"{name}.json"
            if not p.exists():
                die(f"missing analysis fixture {p.relative_to(ROOT)}")
            self._analyses[name] = json.loads(p.read_text())
        return self._analyses[name]

    def proven_of(self, name):
        e = self.d(name)
        return e["proven_count"], e["prop_count"]

    def mutation_of(self, name):
        a = self.analysis(name)
        if "mutants_killed" not in a or "mutants_total" not in a:
            die(f"analysis for {name!r} carries no mutation score")
        return a["mutants_killed"], a["mutants_total"]

    def cases_of(self, name):
        a = self.analysis(name)
        if "cases" not in a:
            die(f"analysis for {name!r} carries no case count")
        return a["cases"]

    def kernel(self):
        k = self.outcomes.get("kernel")
        if not k:
            die("ledger carries no kernel field")
        return k

    def solver_version(self):
        """The bare version out of the ledger's solver string.

        The ledger records `Z3 version 4.16.0 - 64 bit`; prose says `Z3 4.16.0`.
        Deriving the number rather than the whole string is what lets the gate
        compare the two forms without pinning the ledger's phrasing.
        """
        sv = self.outcomes.get("solver")
        if not sv:
            die("ledger carries no solver field")
        m = re.search(r"(\d+\.\d+\.\d+)", sv)
        if not m:
            die(f"no version number in solver string {sv!r}")
        return m.group(1)

    def capture(self):
        """The 2026-07-29 `explain` capture the nine-minute-gap essay narrates.

        Its numbers are read from the COMMITTED CAPTURE, never from the live
        corpus. The essay describes a dated event; deriving its figures from a
        ledger that keeps moving would make CI demand edits to history — the
        precise coupling this file exists to prevent, and one an earlier draft
        of this gate had.
        """
        txt = (ROOT / "docs/evidence/loop-after.txt").read_text()
        mp = re.search(r'"guarantee":\s*"PROVEN \(all (\d+) propert', txt)
        mk = re.search(r'"killed":\s*(\d+)', txt)
        mt = re.search(r'"total":\s*(\d+)', txt)
        if not (mp and mk and mt):
            die("loop-after.txt no longer carries the captured figures")
        return int(mp.group(1)), int(mk.group(1)), int(mt.group(1))

    def capture_before(self):
        """The BEFORE half of the pair, from `loop-before.txt`.

        An after-capture cannot witness a before-state, so the essay's `tested`
        reading is bound to the file that actually recorded it.
        """
        txt = (ROOT / "docs/evidence/loop-before.txt").read_text()
        m = re.search(r'"guarantee":\s*"tested \((\d+) cases per property\)"', txt)
        if not m:
            die("loop-before.txt no longer records the captured `tested` guarantee")
        return int(m.group(1))

    def abs_small_bound(self):
        """The bound in `abs-small`'s own property, from the source.

        The essay's counterexample is not a free-standing fact: it is
        -(bound+1), the smallest input the property excludes. Deriving it means
        a change to `undertested.oath` fails this gate instead of silently
        stranding the prose — which is the whole failure mode #128 catalogued.
        """
        src = (ROOT / "examples/undertested.oath").read_text()
        m = re.search(r"\(<=\s*\(abs-small x\)\s*(\d+)\)", src)
        if not m:
            die("cannot find abs-small's bound in examples/undertested.oath")
        bound = int(m.group(1))

        # THE BOUND IS NOT THE WHOLE WITNESS. -(bound+1) is a counterexample
        # only given the BODY: `(abs x)`. A body returning 0 for negatives
        # would leave the bound, the case count and the unproven verdict all
        # unchanged while making the essay's sentence false. This gate cannot
        # evaluate Oath semantics, so it pins the body instead and refuses
        # rather than certifying a witness it can no longer justify.
        body = re.search(r"\(defn abs-small \[\] \[\(x Int\)\] Int\s*\n\s*\(abs x\)",
                         src)
        if not body:
            die("abs-small's body is no longer `(abs x)`; -(bound+1) may not be "
                "a counterexample any more — re-derive the essay's witness by hand")
        return bound

    def totals(self):
        withprops = [x for x in self.defs if x["prop_count"] > 0]
        levels = {}
        for x in withprops:
            levels[x["level"]] = levels.get(x["level"], 0) + 1
        return {
            "defs": len(withprops),
            "props": sum(x["prop_count"] for x in withprops),
            "proven_props": sum(x["proven_count"] for x in withprops),
            "proven": levels.get("proven", 0),
            "tested": levels.get("tested", 0),
            "falsified": levels.get("falsified", 0),
        }


def die(msg):
    print(f"ESSAY CLAIMS: INSTRUMENT ERROR — {msg}", file=sys.stderr)
    sys.exit(2)


# ----------------------------------------------------------------- the claims

NINE = "website/app/essays/nine-minute-gap/page.tsx"
AUDIT = "website/app/essays/outside-audit/page.tsx"
BUILD = "website/app/essays/building-oath/page.tsx"
ARCH = "website/app/docs/architecture/page.tsx"


def claims(L):
    """Return the pinned claim manifest. DERIVED entries build their expected
    text from `L`; PINNED entries carry a literal plus its provenance."""
    t = L.totals()
    # FROM THE CAPTURE, not the corpus — see Ledger.capture().
    cap_props, eh_k, eh_n = L.capture()
    cap_cases = L.capture_before()
    eh_p = eh_t = cap_props
    rv_p, rv_t = L.proven_of("reverse")
    rv_k, rv_n = L.mutation_of("reverse")
    abs_bound = L.abs_small_bound()
    # THE EXHIBIT'S PREMISE, checked rather than assumed. `abs-small` is in the
    # corpus precisely BECAUSE it is tested-but-unproven; the essay's whole
    # point rests on that. If it ever becomes proven the prose needs a human,
    # not a mechanically-substituted "yes" that would read
    # "PROVEN? yes - the property is false at x = -401" — a contradiction this
    # gate would then enforce. Fail as an INSTRUMENT ERROR instead.
    if L.proven_of("abs-small")[0] > 0:
        die("abs-small is now PROVEN; the building-oath exhibit's premise no "
            "longer holds and the essay needs rewriting by hand")
    abs_cases = L.cases_of("abs-small")
    len_k, len_n = L.mutation_of("length")

    return [
        # ---- nine-minute-gap ------------------------------------------------
        dict(page=NINE, id="echo-handler-proven", kind="pinned",
             provenance_file="docs/evidence/loop-after.txt",
             provenance_needles=['echo-handler', 'proven'],
             note="dated 2026-07-29 capture; the essay narrates THAT event, "
                  "so this must not track a corpus that moves under it",
             source="docs/evidence/loop-after.txt: captured guarantee",
             text=f"— {eh_p}/{eh_t} properties, machine-checked by Z3."),
        dict(page=NINE, id="echo-handler-proven-table", kind="pinned",
             provenance_file="docs/evidence/loop-after.txt",
             provenance_needles=['echo-handler', 'PROVEN'],
             note="dated 2026-07-29 capture; the essay narrates THAT event, "
                  "so this must not track a corpus that moves under it",
             source="docs/evidence/loop-after.txt: captured guarantee",
             text=f"<code>PROVEN</code> — all {eh_t} properties, Z3"),
        dict(page=NINE, id="echo-handler-mutation", kind="pinned",
             provenance_file="docs/evidence/loop-after.txt",
             provenance_needles=['echo-handler'],
             note="dated 2026-07-29 capture; the essay narrates THAT event, "
                  "so this must not track a corpus that moves under it",
             source="docs/evidence/loop-after.txt: captured spec_strength",
             text=f"<td>{eh_k}/{eh_n} MEASURED</td>", count=2),

        dict(page=NINE, id="echo-handler-before-state", kind="pinned",
             provenance_file="docs/evidence/loop-before.txt",
             provenance_needles=['"guarantee": "tested', 'echo-handler'],
             note="the BEFORE half of the pair — bound to loop-before.txt, "
                  "because an after-capture cannot witness a before-state",
             source="docs/evidence/loop-before.txt: captured guarantee",
             text=f"<code>tested</code> — {cap_cases} cases per property"),

        # ---- outside-audit --------------------------------------------------
        dict(page=AUDIT, id="current-ledger-provenance", kind="derived",
             source="fixtures/prove/outcomes.json: kernel, solver",
             text=f"kernel <code>{L.kernel()}</code>, Z3 {L.solver_version()},"),
        dict(page=AUDIT, id="current-ledger-totals", kind="derived",
             source="fixtures/prove/outcomes.json: definitions with properties, "
                    "properties, proven properties, fully proven",
             text=(f"{t['defs']} definitions with properties, {t['props']} properties, "
                   f"{t['proven_props']} proven properties, and {t['proven']} fully proven "
                   f"definitions.")),
        dict(page=AUDIT, id="current-ledger-levels", kind="derived",
             source="fixtures/prove/outcomes.json: level counts",
             text=(f"It also keeps {t['tested']} tested definitions and "
                   f"{t['falsified']} falsified definitions in view.")),
        # DATED LEDGER SNAPSHOTS. The essay narrates what the ledger said ON A
        # DATE; the numbers are correct about the past and must never be
        # "corrected" to the present. No current artifact can verify them —
        # their provenance is the repository history at that date — so these
        # are FROZEN rather than provenance-backed, and the report says which.
        dict(page=AUDIT, id="snapshot-2026-07-18-drift", kind="pinned",
             source="ledger state at 2026-07-18, per the essay's own changelog",
             note="dated snapshot; frozen, not verifiable against today's ledger",
             text=("same 56 definitions and 207 properties as the canonical ledger, but 134 "
                   "proven and 37 fully proven instead of 136 and 38.")),
        dict(page=AUDIT, id="snapshot-2026-07-18-growth", kind="pinned",
             source="ledger state at 2026-07-18, per the essay's own changelog",
             note="dated snapshot; frozen, not verifiable against today's ledger",
             text=("to 88 definitions, 289 properties, 218 proven properties, and 70 fully "
                   "proven definitions, including dictionary-passing generics.")),
        dict(page=AUDIT, id="snapshot-corpus-168", kind="pinned",
             source="ledger state at the third review round, per the essay's changelog",
             note="dated snapshot; frozen, not verifiable against today's ledger",
             text=("grew to 168 definitions, 427 properties, 348 proven properties, and 123 "
                   "fully proven definitions;")),
        dict(page=AUDIT, id="flywheel-rematch-scores", kind="pinned",
             source="docs/experiments/flywheel.md (rematch table)",
             provenance_file="docs/experiments/flywheel.md",
             provenance_needles=["33/50", "41/50"],
             note="historical experiment result; the current corpus does not reproduce it",
             text=("founding specs scored 33/50, model specs scored 41/50 with the scorer, "
                   "and blind model specs also scored 41/50.")),

        # ---- building-oath --------------------------------------------------
        # The WHOLE verdict, not the `tested N/N ·` prefix. The tail is a claim
        # about what the kernels do, and it is the half that goes stale: the two
        # kernels reach `proven: false` by different routes, so a verdict line
        # asserting a refutation would be true of one and false of the other.
        # Checking the prefix alone left exactly that sentence unguarded.
        dict(page=BUILD, id="abs-small-verdict", kind="derived",
             source="fixtures/prove/outcomes.json: abs-small proven_count; "
                    "fixtures/analyses/abs-small.json: cases; "
                    "examples/undertested.oath: the property's bound, "
                    "whose -(bound+1) IS the counterexample",
             text=(f"tested {abs_cases}/{abs_cases} · "
                   f"PROVEN? no — "
                   f"the property is false at x = -{abs_bound + 1}")),
        dict(page=BUILD, id="length-spec-anchoring", kind="pinned",
             provenance_file="DESIGN.md",
             provenance_needles=["scored 1/5", "non-negative", "5/5"],
             source="DESIGN.md's spec-strength narrative; NOT the current score",
             note=(f"current fixtures/analyses/length.json is {len_k}/{len_n} — a different "
                   f"campaign on a different spec; do not 'correct' the prose to it"),
             text="took <code>length</code> from 1/5 to 5/5"),
        dict(page=BUILD, id="is-sorted-first-run", kind="pinned",
             provenance_file="DESIGN.md",
             provenance_needles=["scoring 0/5", "is-sorted"],
             source="DESIGN.md's guarantee-ladder correction",
             note="first-run score of a hand-written spec that no longer exists in the corpus",
             text="scored 0 out of 5."),

        # ---- docs/architecture ----------------------------------------------
        dict(page=ARCH, id="reverse-decision-package", kind="derived",
             source="fixtures/prove/outcomes.json (reverse proven_count/prop_count) + "
                    "fixtures/analyses/reverse.json (mutants_killed/mutants_total)",
             text=(f"<code>reverse</code>, proven {rv_p}/{rv_t} with {rv_k}/{rv_n} "
                   f"spec strength")),
        dict(page=ARCH, id="campaign-identity-score", kind="derived",
             source="fixtures/analyses/reverse.json: mutants_killed/mutants_total",
             text=f"A mutation score of {rv_k}/{rv_n} answers"),
    ]


# ------------------------------------------------------------------ the check

WS = re.compile(r"\s+")


def normalized(path):
    return WS.sub(" ", (ROOT / path).read_text())


def main():
    L = Ledger()
    manifest = claims(L)

    # Verify the instrument before interpreting its output: a manifest that
    # silently emptied, or a page that moved, must not read as "all claims pass".
    if not manifest:
        die("claim manifest is empty")
    pages = sorted({c["page"] for c in manifest})
    for p in pages:
        if not (ROOT / p).exists():
            die(f"page {p} does not exist — the manifest is stale, not the page")

    cache = {p: normalized(p) for p in pages}
    failures = []
    for c in manifest:
        want, got = c.get("count", 1), cache[c["page"]].count(c["text"])
        if got != want:
            failures.append((c, got, want))
        if c["kind"] == "pinned" and "provenance_file" in c:
            pf = ROOT / c["provenance_file"]
            if not pf.exists():
                die(f"provenance file {c['provenance_file']} is missing")
            body = pf.read_text()
            for needle in c.get("provenance_needles", []):
                if needle not in body:
                    failures.append((dict(c, text=f"[provenance] {needle} in "
                                                  f"{c['provenance_file']}"), 0, 1))

    derived = sum(1 for c in manifest if c["kind"] == "derived")
    pinned = len(manifest) - derived

    if failures:
        print("ESSAY CLAIMS: FAIL\n")
        for c, got, want in failures:
            print(f"  {c['page']}")
            print(f"    claim   : {c['id']} [{c['kind'].upper()}]")
            print(f"    source  : {c['source']}")
            print(f"    expected: {want}x  {c['text']!r}")
            print(f"    found   : {got}x")
            if c["kind"] == "derived":
                print("    -> the ledger moved, or the sentence was reworded. Update the")
                print("       page to the derived value; do NOT edit this expectation.")
            else:
                print("    -> a PINNED historical/captured claim changed. It is not a live")
                print("       number: confirm against its source before touching either.")
            print()
        print(f"{len(failures)} claim(s) failed "
              f"({derived} derived + {pinned} pinned checked)")
        return 1

    # THE REPORT MUST NOT CLAIM MORE THAN THE CHECK ESTABLISHES. A pinned claim
    # with a provenance file was checked against evidence; one without was only
    # held UNCHANGED. Both are useful and they are not the same assurance, so
    # they are counted separately rather than summed into "intact".
    backed = [c for c in manifest
              if c["kind"] == "pinned" and c.get("provenance_file")]
    frozen = [c for c in manifest
              if c["kind"] == "pinned" and not c.get("provenance_file")]
    print(f"ESSAY CLAIMS: PASS — {derived} derived claim(s) match the ledger, "
          f"{len(backed)} pinned claim(s) confirmed against evidence, "
          f"{len(frozen)} frozen (unchanged, not independently verified)")
    for c in backed:
        print(f"  evidenced: {c['page'].split('/')[-2]}/{c['id']} "
              f"[{c['provenance_file']}] — {c['note']}")
    for c in frozen:
        print(f"  frozen   : {c['page'].split('/')[-2]}/{c['id']} — {c['note']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
