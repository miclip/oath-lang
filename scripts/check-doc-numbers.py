#!/usr/bin/env python3
"""Assert that quantitative claims in prose match the machine's own ledger.

WHY THIS EXISTS. README.md and DESIGN.md carried "99 definitions fully proven (299
properties)" long after the real figures were 123 and 348/427. Nothing was wrong with
the kernel; the prose simply drifted, and nothing could notice because no check reads
it. `make check-web-ledger` gates website/lib/outcomes.json and nothing else, so every
number written in prose is unverified — in a project whose entire claim is that
verdicts are re-derived rather than asserted.

A number in a document is a claim like any other. This makes the documented ones
checkable against fixtures/prove/outcomes.json, which is itself regenerated from the
store, so the chain runs prose -> fixture -> corpus with no human assertion in it.

DELIBERATELY NARROW. It checks claims that are pinned to an exact phrasing, so it
cannot silently stop matching and pass. A claim whose sentence is rewritten fails
loudly and is re-pinned on purpose, which is the correct failure: a check that
quietly matches nothing is worse than no check (see #95, where a loop enumerated
fixture files and could not notice a missing one).

Run: python3 scripts/check-doc-numbers.py
"""

import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def derived():
    """Every figure comes from the ledger, never from this file."""
    d = json.loads((ROOT / "fixtures/prove/outcomes.json").read_text())["definitions"]
    total = sum(x["prop_count"] for x in d)
    proven = sum(x["proven_count"] for x in d)
    full = sum(1 for x in d if x["prop_count"] and x["proven_count"] == x["prop_count"])
    names = json.loads((ROOT / "codebase/names.json").read_text())
    return {
        "fully_proven": full,
        "proven_props": proven,
        "total_props": total,
        "corpus": len(names),
    }


# (file, regex with ONE capture group, key, human description)
CLAIMS = [
    ("README.md", r"(\d+) definitions are\s*\n?\s*fully proven", "fully_proven",
     "README: definitions fully proven"),
    ("README.md", r"fully proven \((\d+) of \d+ properties proven overall\)", "proven_props",
     "README: proven properties"),
    ("README.md", r"fully proven \(\d+ of (\d+) properties proven overall\)", "total_props",
     "README: total properties"),
    ("DESIGN.md", r"lemma library \(§7\.2\): (\d+) definitions", "fully_proven",
     "DESIGN: definitions fully proven"),
    ("DESIGN.md", r"\*\*Coverage\.\*\* (\d+) definitions", "corpus",
     "DESIGN: corpus size"),
]


def main():
    vals = derived()
    failures = []
    checked = 0

    for fname, pattern, key, desc in CLAIMS:
        text = (ROOT / fname).read_text()
        m = re.search(pattern, text)
        if not m:
            # An unmatched pattern is a FAILURE, not a skip. Skipping would let a
            # reworded sentence silently drop out of coverage while the check still
            # reported success.
            failures.append(f"{desc}: pattern no longer matches {fname} — re-pin it")
            continue
        checked += 1
        claimed = int(m.group(1))
        if claimed != vals[key]:
            failures.append(
                f"{desc}: prose says {claimed}, the ledger derives {vals[key]}")

    if checked == 0:
        print("FAIL: no claims matched at all — this check is measuring nothing")
        return 1

    print(f"derived from fixtures/prove/outcomes.json + codebase/names.json:")
    for k, v in sorted(vals.items()):
        print(f"  {k:<14} {v}")
    print()

    if failures:
        print(f"DOC NUMBERS: FAIL — {len(failures)} claim(s) do not match the ledger")
        for f in failures:
            print(f"  {f}")
        return 1

    print(f"DOC NUMBERS: PASS — {checked} prose claim(s) match the machine's ledger")
    return 0


if __name__ == "__main__":
    sys.exit(main())
