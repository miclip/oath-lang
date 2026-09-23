#!/usr/bin/env python3
"""Gate the QUANTITATIVE PROSE CLAIMS on the website's essay and docs pages.

WHAT THIS ESTABLISHES, precisely (#154). Two different things, and conflating
them is what the issue was filed about:

  the MANIFEST   every registered claim still holds — recomputed from the
                 ledger, checked against dated evidence, or held unchanged.
  the RATCHET    no page gained a number since the baseline was last recorded.

Both are needed for a new figure and neither substitutes for the other: the
manifest checks a VALUE, the baseline records that the page CONTAINS it.

The second exists because the first cannot see its own gaps: a figure added to
an essay tomorrow is not registered, so it is not checked, and without the
ratchet nothing fails. "ESSAY CLAIMS: PASS" then reads as "essay numbers are
gated" while establishing only "the registered ones still hold".

WHERE THIS STOPS, and why here. Extraction is a REGEX OVER TSX, not a parse, so
the exceptions below are the ones a regex cannot close: each was found by review,
each was checked against the real pages, and none occurs today. Hardening past
them means parsing TSX properly, which is worth doing if one ever appears and is
disproportionate before then. They are listed so the next reader meets a stated
boundary rather than discovering one.

It is still NOT the case that every number on these pages is verified. The
ratchet does not verify the unregistered ones; it makes ADDING one visible,
which is the difference between a claim that is wrong and a claim the gate
cannot see.

WHY NOT THE FULL INVERSION #154 PROPOSES — measured, not preferred. Requiring
every numeric hit to be registered means classifying ~119 of them, of which 16
are claims about this repository and the rest are narrative: timestamps, dates,
"55 minutes apart". The ignore list would run several times the manifest, and
#154 names that case itself: "if the ignore list ends up larger than the
manifest, the honest answer may be to keep the manifest and narrow the CLAIM".

TWO THINGS IT CANNOT SEE, stated rather than left to be discovered:

  - a figure rendered by an IMPORTED COMPONENT rather than written in the page
    file. The scanned surface is these four files; #154 asked for that boundary
    to be a rule rather than a habit, and this is the rule.
  - an exchange where the removed and added figures share BOTH value and
    qualifying word.
  - a figure on a line that is shaped like an ES module declaration — prose
    reading `import data from "version 3"` is dropped with the real imports. No
    such line exists on these pages (checked, not assumed), and separating them
    reliably needs a parse rather than a regex.
  - a figure inside a JSX EXPRESSION STRING that contains `<`, such as
    `<code>{"x < 10"}</code>`. Extraction is a regex over TSX, not a parse, so
    the `<` reads as a tag opener. No page uses that form today; a JSX-aware
    parse is the fix if one ever does, and the limit is recorded rather than
    left to be discovered.
  - a letter-prefixed IDENTIFIER such as `v2`, `O1` or `Z3`. Those are names,
    not quantities, and treating them as figures would put every version string
    on these pages into the inventory for no gain.

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



# ---------------------------------------------------------------------------
# COVERAGE (#154). The manifest above verifies what it holds; it says nothing
# about whether it HOLDS ENOUGH — a number added to an essay tomorrow is not in
# it, so it is not checked, and nothing fails. The gate could not tell "this
# claim is correct" from "this claim is invisible to me".
#
# WHAT WAS MEASURED BEFORE CHOOSING, because #154 offers three answers and says
# the decision should be deliberate. Across the four pages: 43 numbers sit in a
# corpus context, 51 are narrative (timestamps, dates, durations, "55 minutes
# apart"), and 16 are registered. Inverting the gate — requiring every hit to be
# registered — means classifying ~94 and carrying an ignore list near 78 entries,
# almost five times the manifest. #154 names that case exactly and says the
# honest answer is then to keep the manifest rather than build the bigger list.
#
# SO NEITHER OF ITS TWO ANSWERS, BUT THE DEFECT IS STILL FIXED. The complaint is
# not that unregistered numbers exist; it is that ADDING one is invisible. A
# ratchet on how many numbers each page contains catches exactly that, needs no
# ignore list, and forces the decision #154 wanted forced: register the new
# figure, or bump the baseline and say why.
#
# What it does NOT do, stated so the report cannot be read as more: it does not
# verify unregistered numbers, and it cannot tell a new CLAIM from a new date.
# It converts "invisible by construction" into "visible and counted", which is
# the half that was missing.
# A numeric token, INCLUDING a negative one. `-12` was invisible: the leading
# `-` was rejected by the lookbehind and the remaining digits were word-adjacent,
# so a negative figure could be added and counted as nothing. These pages already
# contain negative examples.
NUMERIC = re.compile(
    r"(?<![\w.#])-?(?:\d[\d,]*(?:\.\d+)?|\.\d+)(?:[eE][-+]?\d+)?[\w:]*"
)

# ATTRIBUTE VALUES are not prose. Stripping them is not the same as skipping the
# LINE they sit on: `<p className="metric">The run took 12 seconds.</p>` is a
# perfectly ordinary JSX line carrying a real claim, and dropping it whole let an
# unchecked figure in through formatting alone.
# JSX TAGS GO ENTIRELY, not just their attributes. The qualifier is the word the
# figure describes, and with tags left in place `<code>10</code> retries` keys on
# `code` — so swapping "retries" for "failures" left the inventory identical and
# walked straight past the same-valued-exchange check. The baseline was full of
# `@code` and `@span` keys, which is what that looks like from outside.
TAG = re.compile(r"<[^>]*>")
# An import DECLARATION, not prose that happens to begin with the word. Matching
# `^\s*import ` removed any wrapped JSX line starting with "import", so a figure
# in "…import 12 records…" vanished from the inventory and could be added
# unnoticed.
IMPORT = re.compile(r"""^\s*import\s+(?:["']|[\w{*][^;]*\sfrom\s+["'])""")


WORD = re.compile(r"[A-Za-z]+")


def page_numbers(rel):
    """A page's prose figures, each keyed by the word it qualifies.

    THE WHOLE PAGE IS NORMALISED FIRST — tags stripped, whitespace collapsed —
    and scanned as one text. Scanning line by line broke on ordinary JSX
    wrapping: "There are 43" with "definitions" on the next line keyed as
    `43@are`, so swapping that next line to "properties" was an undetectable
    same-valued exchange. Worse, re-wrapping a paragraph changed keys without
    changing a word of rendered prose, which is how a gate earns a reputation for
    firing on nothing and gets switched off.

    VALUE ALONE CANNOT SEE A SAME-VALUED EXCHANGE: delete a claim containing 43
    and add a different one also containing 43, and a multiset of values is
    unchanged. The key distinguishes "43 definitions" from "43 properties". It
    does not distinguish two claims agreeing in both — a real residual, and a
    much smaller one.
    """
    text = (ROOT / rel).read_text(encoding="utf-8")
    text = "\n".join(l for l in text.split("\n") if not IMPORT.search(l))
    text = re.sub(r"\s+", " ", TAG.sub(" ", text))
    out = []
    for m in NUMERIC.finditer(text):
        after = WORD.findall(text[m.end():m.end() + 40])
        before = WORD.findall(text[max(0, m.start() - 40):m.start()])
        key = (after[0] if after else (before[-1] if before else "-")).lower()
        out.append(f"{m.group()}@{key}")
    return sorted(out)


# THE PAGES ARE DISCOVERED, NOT LISTED. The manifest names four, and the
# filesystem has five: essays/what-remains/page.tsx carries 10 figures and was
# outside the ratchet entirely — a hand-written page list being short by one,
# which is the same defect one layer up from the one #154 was filed about.
def ratcheted_pages():
    # RECURSIVE, so the essays INDEX (essays/page.tsx) is included along with
    # each essay's own directory. A one-level glob missed it, and the index is a
    # rendered page carrying quantitative prose of its own — the same
    # short-by-one that left what-remains outside, one directory up.
    pages = sorted(
        str(p.relative_to(ROOT)) for p in ROOT.glob("website/app/essays/**/page.tsx")
    )
    pages.append(ARCH)  # the one docs page in the stated surface
    return sorted(set(pages))


# The numeric inventory of each page when the baseline was recorded — the
# figures themselves, keyed by what they qualify, not how many.
#
# A COUNT CANNOT SEE AN EXCHANGE. Removing one figure while adding another leaves
# the cardinality identical, so a scalar ratchet reports that nothing was added
# while an unregistered claim walks in. Comparing inventories also lets the
# failure name the figure, which is the difference between "something changed"
# and a message someone can act on.
BASELINE = {
    'website/app/docs/architecture/page.tsx': ['-0.0@smt', '0.0@ne', '0.1@and', '0.1@is', '0.1f@f', '0.1f@literal', '0.1f@z', '0.2@is', '0.2f@f', '0.30000000000000004@the', '0.3f@is', '0@included', '0x7F@inside', '1.0@x', '10@is', '10@there', '10²⁴@prints', '11@so', '1@are', '1@is', '1@operations', '256@in', '256@of', '2@are', '2@for', '2@with', '2@with', '3@answers', '3@answers', '3@spec', '3@spec', '3@there', '600@and', '60@cases', '754@binary'],
    'website/app/essays/building-oath/page.tsx': ['-401@tested', '-4@because', '02@building', '0@out', '1@the', '1@to', '200@generated', '200@generated', '200@proven', '200@proven', '256@of', '2@the', '3,@because', '401,@passes', '5@every', '5@the', '5@the', '5@to', '7@because'],
    'website/app/essays/nine-minute-gap/page.tsx': ['04@what', '12:22Z@a', '12:22Z@got', '12:22Z@the', '12:22Z@z', '12:31Z@the', '13:17:14Z@so', '13:17Z@guarantee', '200@cases', '2026@an', '29@july', '3@properties', '3@properties', '3@properties', '43@measured', '43@measured', '43@measured', '43@measured', '55@minutes'],
    'website/app/essays/outside-audit/page.tsx': ['0.7@z', '03@an', '07@corpus', '07@n', '07@registry', '07@solver', '07@website', '123@fully', '134@proven', '136@and', '168@definitions', '18@corpus', '18@n', '18@solver', '18@website', '192@fully', '2026@corpus', '2026@n', '2026@registry', '2026@solver', '2026@website', '207@properties', '218@proven', '247@definitions', '289@properties', '30@registry', '33@model', '348@proven', '37@fully', '38@website', '4.16@definitions', '41@the', '41@with', '427@properties', '4@falsified', '5.5@an', '5.5@an', '50,@model', '50@the', '50@with', '51@tested', '521@proven', '56@definitions', '682@properties', '70@fully', '754@float', '88@definitions'],
    'website/app/essays/page.tsx': ['01@title', '02@title', '03@title', '04@title', '12:22Z@the', '1@and', '2,@and', '5.5@role'],
    'website/app/essays/what-remains/page.tsx': ['-4@kills', '01@what', '0@out', '1@not', '200@cases', '200@generated', '2@not', '3,@kills', '5:@every', '7@kills'],
}


def coverage_failures():
    out = []
    missing = [p for p in ratcheted_pages() if p not in BASELINE]
    if missing:
        out.append(
            "  pages with no recorded baseline: " + ", ".join(missing) + "\n"
            "    Every essay page is in the stated surface. Record its inventory\n"
            "    in BASELINE, or the figures on it are outside the ratchet."
        )
    for rel, base in sorted(BASELINE.items()):
        try:
            now = page_numbers(rel)
        except OSError as e:
            out.append(f"  {rel}\n    could not be read: {e}")
            continue
        from collections import Counter
        added = sorted((Counter(now) - Counter(base)).elements())
        gone = sorted((Counter(base) - Counter(now)).elements())
        if not added and not gone:
            continue
        lines = [f"  {rel}"]
        if added:
            lines.append(f"    ADDED and unverified: {', '.join(added)}")
            lines.append("    TWO STEPS, not a choice between them:")
            lines.append("      1. if it is a claim about this repository, register it in")
            lines.append("         claims() above so its VALUE is checked — derived from the")
            lines.append("         ledger, evidenced against a capture, or frozen with a note;")
            lines.append("      2. add it to BASELINE either way, or this keeps firing.")
            lines.append("    Registering alone does not re-arm the ratchet: the manifest")
            lines.append("    verifies values, BASELINE records what the page contains, and a")
            lines.append("    figure needs both to be both checked and accounted for.")
        if gone:
            lines.append(f"    REMOVED: {', '.join(gone)}")
            lines.append("    Update BASELINE so the ratchet keeps its grip — left stale, it")
            lines.append("    permits an exchange that this comparison exists to catch.")
        out.append("\n".join(lines))
    return out


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
    if cov := coverage_failures():
        print("ESSAY CLAIMS: FAIL — the manifest no longer covers the pages\n")
        print("\n".join(cov))
        return 1

    print(f"ESSAY CLAIMS: PASS — {derived} derived claim(s) match the ledger, "
          f"{len(backed)} pinned claim(s) confirmed against evidence, "
          f"{len(frozen)} frozen (unchanged, not independently verified); "
          f"page number inventories unchanged, so nothing was added unregistered")
    for c in backed:
        print(f"  evidenced: {c['page'].split('/')[-2]}/{c['id']} "
              f"[{c['provenance_file']}] — {c['note']}")
    for c in frozen:
        print(f"  frozen   : {c['page'].split('/')[-2]}/{c['id']} — {c['note']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
