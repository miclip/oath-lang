#!/usr/bin/env python3
"""Gate STRUCTURAL figures in prose — claims about the shape of the repository
rather than the state of the corpus (#153).

check-doc-numbers.py derives its expectations from fixtures/prove/outcomes.json,
which is the CORPUS surface and is well covered. Nothing recomputed line counts,
file counts or "N guarded files", so nothing failed when they drifted. README
said the kernel was ~2,000 lines while the core measured 3,727 — the claim
survived that core more than doubling, and was corrected only because a human
fact-checked an article against the repo.

THE CLAIM CARRIES ITS OWN DEFINITION, AND THAT IS WHY THIS NEEDS NO MANIFEST.
The prose already cites the invocation that produces the figure:

    an identity/typecheck/eval core of ~5,200
    lines of dependency-free Go (`wc -l oath/{ast,canon,check,eval,surface}.go`)

so the FILE SET is part of the sentence, not a separate list that can drift from
it. #153 worried that "whatever records the claim has to record the set, or it
will silently measure something else" — deriving the set from the claim is the
version of that where the two cannot disagree, because they are the same text. A
sixth core file changes what the sentence MEANS, and the sentence is what gets
edited.

THE BAND COMES FROM THE WRITTEN PRECISION, for the same reason. `~5,200` is
deliberately not `5,184`: an exact count is wrong the next time anyone touches
those files, which would fire on every unrelated commit and train people to
update the number without reading the sentence. But a declared tolerance would
be another number to maintain. The author already expressed the tolerance by
choosing how precisely to write it — `~5,200` claims the hundreds, `~2,000`
claims the thousands — so the rule is: an approximate figure must ROUND TO WHAT
WAS WRITTEN, at the precision it was written to.

    ~5,200  -> 5,150 .. 5,249   (5,184 passes)
    ~2,000  -> 1,500 .. 2,499   (3,727 fails, which is the defect above)

A figure written WITHOUT `~` is exact, because exactness is the safe default and
approximation should be a deliberate mark rather than an assumption.

A figure written in a form this tokenizer does not recognise as a complete token
is not seen. It matches a decimal-comma integer with optional surrounding
punctuation, and deliberately not identifiers, references, versions, dates,
times or components of any of them.

A claim inside an INDENTED BLOCK (four spaces or a tab) is treated as a code
example and not scanned. That is markdown's own reading, and the converse rule
fires on every table here.

WHAT THIS CANNOT SEE, stated because the gap is the reason to cite. It checks a
figure that CITES its derivation. A bare structural number — "three backends",
"N guarded files" — cites nothing, and no gate can recompute a claim whose
definition exists only in the author's head; deciding which numbers in prose are
structural is not something text supports. So this does not make every
structural figure checked. It makes a CITED one checked, which turns citing from
a courtesy to the reader into the thing that buys the claim a guard.

COMMANDS ARE PARSED, NEVER EXECUTED. The invocation comes out of a document, and
a gate that shelled out would be running text as code on the strength of it
living in our own repo. Only the shapes below are understood; anything
else FAILS rather than being guessed at, run, or waved through with a note —
a claim the gate can see but cannot evaluate is precisely the one an author
would assume is covered.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
# THE TRACKED PROSE SET, ASKED OF GIT rather than written down here. A glob was
# wrong twice in a row for the same reason: `docs/*.md` missed docs/tutorial/ and
# docs/experiments/, and `docs/**/*.md` still missed apps/*/README.md,
# plugin/README.md and the website mirrors. Each fix was correct about the
# previous omission and blind to the next, which is what a hand-maintained
# population always does.
#
# It fails OPEN, which is what makes the drift invisible: README alone keeps the
# claim count nonzero, so a cited figure in an unscanned file leaves the gate
# green while being completely unchecked. git's index is the authority on what
# prose this repository has, so the universe comes from there.
def tracked_prose():
    out = subprocess.run(
        ["git", "ls-files", "*.md"], cwd=ROOT, capture_output=True, text=True
    )
    if out.returncode != 0:
        return None
    return sorted(p for p in out.stdout.split("\n") if p)


# A COUNT-PRODUCING invocation. This is the population, and narrowing to it is
# what makes anchoring on the command workable.
#
# Anchoring on "looks like a shell command" fired 35 times on prose: `git status`
# in a runbook, `ls -la` in a transcript, and `find --equiv` — which is Oath's
# own subcommand, not the shell's. A gate that cries wolf is one people learn to
# skip, which is the failure this whole issue is about arriving one layer up.
#
# The claim being gated is "a figure, and the command that produces it", so the
# command must be able to produce a FIGURE. `wc` is that; `git status` cannot be
# the derivation of a number under any reading. Deriving the population from what
# the claim quantifies over is the rule this detector broke three times by
# writing down a prose pattern instead.
def is_count_invocation(cmd: str) -> bool:
    """Is this command a COUNT — the kind of thing a figure can be derived from?

    ANY `wc` invocation, in command position, with something to count. Not a list
    of spellings: matching `wc -l`/`wc -c` as substrings skipped `wc --lines`,
    requiring the first token skipped `env LC_ALL=C wc …` and `cd oath && wc …`,
    and accepting any `wc` token anywhere read grep's PATTERN in `grep -c wc f`
    as the command. Each of those was a repair correct about the previous
    omission and blind to the next — so the question is asked once, properly:
    split into sub-commands, drop the prefixes that do not consume an argument,
    and look at what is actually being run.

    Which SPELLINGS can be evaluated is a SEPARATE question, answered by
    evaluate(), and an unsupported one FAILS rather than being skipped. This
    function decides only whether a span is claiming to derive a number.

    Excluded deliberately: a bare `wc -l` with nothing to count. That is prose
    mentioning the tool ("the assertion derived its count with `wc -l`") and
    derives no particular figure.
    """
    # Sub-commands. A pipe also carries INPUT, which is what lets the right-hand
    # side count without naming operands.
    parts, piped_into = [], []
    for run in re.split(r"&&|\|\||;", cmd):
        segs = run.split("|")
        for i, seg in enumerate(segs):
            parts.append(seg)
            piped_into.append(i > 0)

    for seg, fed in zip(parts, piped_into):
        toks = seg.split()
        # Prefixes that run something ELSE rather than consuming an argument.
        while toks and (toks[0] in ("env", "command", "exec", "time", "sudo", "nice")
                        or ("=" in toks[0] and not toks[0].startswith("-"))):
            toks = toks[1:]
        if not toks:
            continue
        name = toks[0].rsplit("/", 1)[-1]
        if name != "wc":
            continue
        operands = [t for t in toks[1:] if not t.startswith("-")]
        if operands or fed:
            return True
    return False


INVOCATION_SPAN = re.compile(r"`(?P<cmd>[^`]+)`")
# A figure may be followed by PUNCTUATION — a quote, comma, period, paren. The
# `(?!\S)` boundary required whitespace, so `reports 5,200"` had no figure at all
# and the citation beside it was reported as unassociated. Found when this gate
# ran over prose describing this gate.
# Trailing punctuation is allowed — a quote, comma, period, paren — but a NUMERIC
# SEPARATOR followed by more digits means this is a COMPONENT of a compound
# token, not a figure. Without that second guard, allowing punctuation turned
# `2026-09-19` into three figures and `12:22Z` into one, any of which could then
# be associated with a nearby citation.
# BOTH directions, and a figure may not START mid-number. A forward guard alone
# rejected `2026` in `2026-09-19` and still matched `19`; adding the separator
# lookbehind still matched `9`, because a match may begin at any character.
# Each miss was caught by the test rather than by review, which is the point of
# having pinned the tokenizer at all.
#
# A REFERENCE IS NOT A FIGURE: `#143`, `§11`, `v2`, `SHA256` name things. The
# start boundary excludes any WORD character, not a list of prefixes — excluding
# just `v` still read `256` out of `SHA256.` and `2` out of `V2)`. Allowing trailing
# punctuation made `For #143, \`wc -l f\` reports 10.` associate the citation with
# 143 rather than 10 — a false failure on ordinary prose, and the nearest-figure
# rule makes the wrong one win.
FIGURE = re.compile(
    r"(?P<approx>[~≈])?(?<![\w,#§.-])(?<![\w][-:./])(?P<num>\d[\d,]*)(?![\d,]*\w)(?![-:./]\d)"
)
LOOKBACK = 120


def glob_files(spec: str):
    """Expand one path argument, including a single {a,b,c} brace group."""
    m = re.match(r"^(?P<pre>[^{]*)\{(?P<alts>[^}]*)\}(?P<post>.*)$", spec)
    specs = (
        [m.group("pre") + a + m.group("post") for a in m.group("alts").split(",")]
        if m
        else [spec]
    )
    out = []
    for s in specs:
        if any(ch in s for ch in "*?["):
            out.extend(sorted(ROOT.glob(s)))
        else:
            out.append(ROOT / s)
    return out


def evaluate(cmd: str):
    """Compute a supported invocation's value. Returns (value, None) or
    (None, why-unsupported). Never executes anything."""
    parts = cmd.split()
    if len(parts) >= 3 and parts[0] == "wc" and parts[1] == "-l":
        total = 0
        for spec in parts[2:]:
            files = glob_files(spec)
            if not files:
                return None, f"no file matches {spec!r}"
            for f in files:
                if not f.is_file():
                    return None, f"{f.relative_to(ROOT)} is not a file"
                total += f.read_text(encoding="utf-8", errors="replace").count("\n")
        return total, None
    return None, f"unsupported invocation {cmd!r}"


def band(num_text: str, approx: bool):
    """The range a claim admits. Exact unless marked approximate, in which case
    the written precision IS the tolerance."""
    value = int(num_text.replace(",", ""))
    if not approx:
        return value, value
    digits = num_text.replace(",", "")
    trailing = len(digits) - len(digits.rstrip("0"))
    step = 10 ** trailing if trailing else 1
    if step == 1:
        return value, value
    return value - step // 2, value + step // 2 - 1


def main() -> int:
    docs = tracked_prose()
    if docs is None:
        print("STRUCTURAL NUMBERS: VOID — could not list tracked prose (not a git "
              "checkout?); this check did NOT run", file=sys.stderr)
        return 1
    claims, failures = 0, []
    for rel in docs:
        path = ROOT / rel
        if not path.is_file():
            continue
        raw = path.read_text(encoding="utf-8")
        # FENCED CODE BLOCKS ARE EXAMPLES, NOT CLAIMS. A shell transcript showing
        # `wc -c < str.bin` is demonstrating a command, not asserting a figure
        # about this repository, and reading one as a claim is the same category
        # error as reading `find --equiv` as the shell's find.
        raw = re.sub(r"```.*?```", " ", raw, flags=re.S)
        raw = re.sub(r"~~~.*?~~~", " ", raw, flags=re.S)  # markdown's other fence
        # INDENTED code blocks are code too. A four-space-indented table of
        # examples is markdown's oldest code form, and this repo's prose uses it
        # constantly — so a table DESCRIBING commands read as prose CITING them.
        # Four or more spaces, or a tab: markdown's indented code block. More
        # deeply indented examples were left in prose by an exactly-four rule.
        # The converse is accepted deliberately — a list continuation indented
        # that far is treated as code and not scanned — because the alternative
        # fires on every table in this repository, and a gate that cries wolf is
        # one people stop reading. Recorded with the other limits below.
        raw = "\n".join(
            " " if re.match(r"^(?: {4,}|\t)", line) and line.strip() else line
            for line in raw.split("\n")
        )
        text = re.sub(r"\s+", " ", raw)
        for m in INVOCATION_SPAN.finditer(text):
            cmd = m.group("cmd")
            counts = is_count_invocation(cmd)
            # Could this span count ANYTHING? A `wc` with no operand and no pipe
            # cannot derive a figure under any reading, wrapper or not — that is
            # decidable without knowing which leading token is a wrapper, and it
            # is what keeps prose like "derived its count with `wc -l`" out of the
            # ambiguous bucket instead of failing the gate over history.
            toks = re.split(r"[\s|;]+", cmd)
            wc_at = next((j for j, t in enumerate(toks) if t == "wc" or t.endswith("/wc")), None)
            mentions_wc = wc_at is not None and (
                "|" in cmd or any(not t.startswith("-") and t for t in toks[wc_at + 1:])
            )
            if not counts and not mentions_wc:
                continue  # backticked prose, not a claimed derivation
            # BOTH SIDES, nearest wins. Looking only backwards rejected the
            # natural "`wc -l …` reports 5,200 lines" as unassociated — and, worse,
            # would silently check the command against an unrelated number that
            # happened to precede it while the real figure went unchecked.
            fig, fig_dist = None, None
            before = text[max(0, m.start() - LOOKBACK):m.start()]
            for f in FIGURE.finditer(before):
                d = len(before) - f.end()
                if fig_dist is None or d < fig_dist:
                    fig, fig_dist = f, d
            after = text[m.end():m.end() + LOOKBACK]
            for f in FIGURE.finditer(after):
                d = f.start()
                if fig_dist is None or d < fig_dist:
                    fig, fig_dist = f, d
            if not counts:
                # AMBIGUOUS, AND THAT FAILS CLOSED. The span invokes `wc` but this
                # could not tell whether it derives a figure — an unrecognised
                # wrapper (`somewrapper wc -l …`) reads the same as a command
                # taking `wc` as an ARGUMENT (`grep -c wc file`), and no rule over
                # text separates them without knowing every command.
                #
                # Scoped to spans that DO have a figure beside them, which is what
                # keeps it quiet on prose that merely mentions the tool: a bare
                # "derived its count with `wc -l`" has no figure in reach and is
                # not a claim. A wc-shaped span next to a number is.
                if fig is not None:
                    failures.append(("unsupported",
                        f"  {rel}\n    cites `{cmd}`\n"
                        f"    but this could not tell whether it derives a figure"))
                continue
            if fig is None:
                failures.append(("unassociated",
                    f"  {rel}\n    cites `{cmd}`\n"
                    f"    but no figure appears within {LOOKBACK} characters of it"))
                continue
            claims += 1
            value, why = evaluate(cmd)
            if why:
                # A CITED CLAIM THAT CANNOT BE EVALUATED IS A FAILURE, NOT A NOTE.
                # It was a note first, and that made the gate report success while
                # knowingly leaving a detected figure unchecked.
                failures.append(("unsupported",
                                 f"  {rel}\n    cites `{cmd}`\n    but {why}"))
                continue
            lo, hi = band(fig.group("num"), bool(fig.group("approx")))
            if not (lo <= value <= hi):
                failures.append(("drifted",
                    f"  {rel}\n"
                    f"    claims  {fig.group('approx') or ''}{fig.group('num')}"
                    f"   (admits {lo:,}..{hi:,})\n"
                    f"    `{cmd}` measures {value:,}"))

    # VERIFY THE INSTRUMENT. Finding nothing compares two empty sets and passes,
    # which is this repo's most familiar defect wearing a new costume: the claim
    # the gate exists for is that structural figures ARE checked, and zero
    # checked figures cannot support it.
    if claims == 0:
        print("STRUCTURAL NUMBERS: VOID — no claim cited an invocation this gate "
              "understands, so nothing was checked", file=sys.stderr)
        return 1

    if failures:
        print("STRUCTURAL NUMBERS: FAIL\n")
        print("\n".join(text for _, text in failures))
        # Guidance per KIND: the two failures call for opposite actions, and
        # printing both trailers for either teaches the reader to skim past the
        # half that applies.
        kinds = {kind for kind, _ in failures}
        if "drifted" in kinds:
            print("\nA figure written with ~ must round to what was written, at the")
            print("precision it was written to; one written without ~ must be exact.")
            print("Update the sentence — and read it while you are there, since a")
            print("number that moved this far usually means the claim around it has too.")
        if "unassociated" in kinds:
            print("\nAn invocation cited with no figure near it is either a claim this")
            print("gate could not associate, or a citation whose number drifted away from")
            print("it. Put the figure and the command in one sentence.")
        if "unsupported" in kinds:
            print("\nA claim citing a derivation this gate cannot evaluate is NOT checked,")
            print("and a note saying so would let the run stay green over it. Either teach")
            print("evaluate() that shape, or drop the citation so the sentence stops")
            print("implying a guard it does not have.")
        return 1

    print(f"STRUCTURAL NUMBERS: PASS — {claims} prose figure(s) match the repository")
    return 0


if __name__ == "__main__":
    sys.exit(main())
