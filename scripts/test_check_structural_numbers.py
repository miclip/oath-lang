#!/usr/bin/env python3
"""Pin the structural-number gate's two judgements.

BOTH TOOK SEVERAL ATTEMPTS AND NEITHER IS OBVIOUS, which is the reason they are
tested here rather than exercised only through the gate's own run. The gate
passes today over a single real claim, so a regression in either judgement would
show up as "still PASS" — the failure mode that makes an instrument worthless.
"""

import importlib.util
import sys
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "gate", Path(__file__).resolve().parent / "check-structural-numbers.py"
)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

# Is this span claiming to DERIVE a number? Every False here was a false positive
# that fired on real prose in this repository, and every True a spelling an
# earlier version skipped in silence.
INVOCATIONS = [
    ("wc -l oath/ast.go", True),
    ("wc --lines f", True),                  # not the `wc -l` substring
    ("env LC_ALL=C wc -l oath/ast.go", True),  # wrapper prefix
    ("cd oath && wc -l ast.go", True),       # after a shell operator
    ("/usr/bin/wc -l f", True),              # absolute path
    ("find . | wc -l", True),                # counts its INPUT, no operands
    ("ls | wc -l | cat", True),              # piped onward, still a count
    ("wc -l", False),                        # prose mentioning the tool
    ("wc -l | cat", False),                  # nothing to count
    ("grep -c wc file", False),              # `wc` is grep's PATTERN
    ("grep wc -l file", False),
    ("echo wc", False),
    ("git status", False),                   # cannot derive a number
    ("find --equiv", False),                 # Oath's subcommand, not the shell's
    ("time wc -l f", True),                  # wrapper
    ("sudo wc -l f", True),
]

# Spans that INVOKE wc but that the classifier cannot confirm derive a figure.
# These fail CLOSED when a figure sits beside them, because an unrecognised
# wrapper (`somewrapper wc -l x`) and a command taking wc as an ARGUMENT
# (`grep -c wc file`) are indistinguishable in text — while a wc with nothing to
# count is decidably a mention and stays quiet.
AMBIGUOUS = [
    ("somewrapper wc -l scripts/x", True),
    ("wc -l", False),        # nothing to count: a mention, not a claim
    ("wc -l | cat", True),   # a pipe means it could be counting its input
    ("git status", False),
]

# An approximate figure admits what ROUNDS to it at the precision written; an
# exact one admits only itself. The band is read off the claim rather than
# declared, so this is where that reading is pinned.
# A figure may end in PUNCTUATION. Requiring whitespace after it meant
# `reports 5,200"` carried no figure, so the citation beside it was reported as
# unassociated — found when the gate ran over prose describing the gate.
PUNCTUATED = [
    ('reports 5,200"', "5,200"),
    ("holds 236.", "236"),
    ("(43)", "43"),
    ("5,184 lines", "5,184"),
]

# A component of a compound token is NOT a figure. Allowing trailing punctuation
# without this turned a date into three figures and a timestamp into one.
NOT_FIGURES = ["2026-09-19", "12:22Z", "4.16.0", "1/2",
               # References name things; they are not quantities.
               "#143", "§11", "v2", "SHA256.", "V2)", "O1", "Z3,",
               "SHA-256.", "RFC-3339)", "issue-143.", "file.go:5006)",
               # A match must begin at a COMPLETE token: a sign or decimal point
               # before the digits means this is a suffix, not the figure.
               "-12.", ".5,"]

BANDS = [
    ("5,200", True, 5150, 5249),
    ("2,000", True, 1500, 2499),   # 3,727 is outside: the #128 defect
    ("4,600", True, 4550, 4649),
    ("5,184", False, 5184, 5184),  # no ~: exact
    ("5,184", True, 5184, 5184),   # ~ with no trailing zeros claims every digit
    ("7", True, 7, 7),
]


def main() -> int:
    bad = []
    for cmd, want in INVOCATIONS:
        got = gate.is_count_invocation(cmd)
        if got != want:
            bad.append(f"is_count_invocation({cmd!r}) = {got}, want {want}")
    for cmd, want in AMBIGUOUS:
        toks = cmd.split()
        wc_at = next((j for j, t in enumerate(toks) if t == "wc" or t.endswith("/wc")), None)
        could = wc_at is not None and (
            "|" in cmd or any(not t.startswith("-") and t for t in toks[wc_at + 1:])
        )
        got = could and not gate.is_count_invocation(cmd)
        if got != want:
            bad.append(f"ambiguous({cmd!r}) = {got}, want {want}")
    for text, want in PUNCTUATED:
        got = [m.group("num") for m in gate.FIGURE.finditer(text)]
        if want not in got:
            bad.append(f"FIGURE over {text!r} = {got}, want it to include {want!r}")
    for text in NOT_FIGURES:
        got = [m.group("num") for m in gate.FIGURE.finditer(text)]
        if got:
            bad.append(f"FIGURE over compound token {text!r} yielded {got}")
    for text, approx, lo, hi in BANDS:
        got = gate.band(text, approx)
        if got != (lo, hi):
            bad.append(f"band({text!r}, approx={approx}) = {got}, want {(lo, hi)}")

    # The controls must be able to fail: an empty table would pass vacuously.
    # EVERY table, not the two that existed when this guard was written. An
    # emptied population runs no assertions and prints PASS, which is the
    # vacuous-success case the whole file exists to prevent — and the guard
    # itself had it for three of its five tables.
    tables = {"INVOCATIONS": INVOCATIONS, "PUNCTUATED": PUNCTUATED,
              "NOT_FIGURES": NOT_FIGURES, "BANDS": BANDS, "AMBIGUOUS": AMBIGUOUS}
    if empty := [name for name, rows in tables.items() if not rows]:
        print(f"STRUCTURAL-NUMBERS TESTS: VOID — empty population(s): "
              f"{', '.join(empty)}; those behaviours were not tested",
              file=sys.stderr)
        return 1
    if bad:
        print("STRUCTURAL-NUMBERS TESTS: FAIL")
        for b in bad:
            print(f"  {b}")
        return 1
    # The summary counts EVERY table it ran. It listed four of five for a while,
    # which is the same defect these gates exist to remove — a report that
    # understates its own coverage is as misleading as one that overstates it,
    # and it is the half nobody checks.
    print(f"STRUCTURAL-NUMBERS TESTS: PASS — {len(INVOCATIONS)} invocation(s), "
          f"{len(BANDS)} band(s), {len(PUNCTUATED)} punctuation case(s), "
          f"{len(NOT_FIGURES)} compound-token case(s), "
          f"{len(AMBIGUOUS)} ambiguity case(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
