#!/usr/bin/env python3
"""Pin the essay-claims coverage ratchet's tokenizer (#154).

The ratchet compares each page's numeric INVENTORY against a recorded baseline,
so what counts as a number decides what it can see. Every case below was a
bypass: a figure that could be added to an essay while the gate reported that
nothing was added unregistered.

Tested here rather than only through the gate's own run because the gate passes
over unchanged pages — a tokenizer regression would present as "still PASS",
which is the failure mode the ratchet exists to remove.
"""

import importlib.util
import re
import sys
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "gate", Path(__file__).resolve().parent / "check-essay-claims.py"
)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

# (line, numbers it must contribute)
LINES = [
    ("<span>The corpus holds 236 definitions.</span>", ["236"]),
    # NEGATIVE figures were invisible: the leading `-` was rejected and the rest
    # read as word-adjacent, so `-12` contributed nothing.
    ("<p>a drift of -12 lines</p>", ["-12"]),
    # Prose sharing a line with an attribute. Skipping the whole LINE let a real
    # claim in through formatting alone.
    ('<p className="metric">The run took 42 seconds.</p>', ["42"]),
    ('<a href="/corpus/9">see 17 entries</a>', ["17"]),
    # Attribute values themselves are not prose.
    ('<p className="eyebrow">Essay</p>', []),
    ('<div className="grid-3 gap-4">text</div>', []),
    ('import X from "./3"', []),
    ('import "./styles-3.css"', []),
    # Prose that merely BEGINS with the word: a real claim, not a declaration.
    ("import 12 records to begin", ["12"]),
    # Thousands separators are one figure, not two.
    ("<span>5,184 lines</span>", ["5,184"]),
    # SUFFIXED forms were invisible — a timestamp could be added to the
    # nine-minute-gap page, which is full of them, while the gate reported that
    # nothing was added.
    ("<code>12:22Z</code>", ["12:22Z"]),
    ("<span>took 10ms</span>", ["10ms"]),
    ("<span>a 10x speedup</span>", ["10x"]),
    ("<span>1e6 draws</span>", ["1e6"]),
    # Leading-decimal forms were invisible.
    ("<span>.5 percent</span>", [".5"]),
    ("<span>-.25 seconds</span>", ["-.25"]),
]

# Same VALUE, different claim. A multiset of values cannot tell these apart, so
# deleting one and adding the other was an undetectable exchange.
CONTEXT = [
    ("<span>43 definitions</span>", "<span>43 properties</span>"),
    # The qualifier must come from PROSE, not the closing tag.
    ("<code>10</code> retries", "<code>10</code> failures"),
    # A qualifier on the NEXT source line: ordinary JSX wrapping.
    ("There are 43\n definitions", "There are 43\n properties"),
]


def main() -> int:
    bad = []
    for line, want in LINES:
        got = gate.NUMERIC.findall(re.sub(r"\s+", " ", gate.TAG.sub(" ", line)))
        if gate.IMPORT.search(line):
            got = []
        if got != want:
            bad.append(f"{line!r} -> {got}, want {want}")

    for a, b in CONTEXT:
        ka = gate.page_key_probe(a) if hasattr(gate, "page_key_probe") else None
        # Exercise the keying the way page_numbers does.
        def keys(line):
            out = []
            clean = re.sub(r"\s+", " ", gate.TAG.sub(" ", line))
            for m in gate.NUMERIC.finditer(clean):
                after = gate.WORD.findall(clean[m.end():m.end() + 40])
                before = gate.WORD.findall(clean[:m.start()])
                key = (after[0] if after else (before[-1] if before else "-")).lower()
                out.append(f"{m.group()}@{key}")
            return out
        if keys(a) == keys(b):
            bad.append(f"same-valued claims are indistinguishable: {a!r} vs {b!r}")

    # DISCOVERY COMPLETENESS. The page list is derived, not written down, and
    # both times it was written down it was short by one — what-remains, then the
    # essays index. This asserts every rendered page in the stated surface has a
    # baseline, which is the property those two misses violated.
    for rel in gate.ratcheted_pages():
        if rel not in gate.BASELINE:
            bad.append(f"{rel} is in the surface but has no baseline")
    for rel in gate.BASELINE:
        if rel not in gate.ratcheted_pages():
            bad.append(f"{rel} has a baseline but is no longer discovered")

    # The baseline must describe the pages as they ARE, or the ratchet starts
    # already failing and gets disabled rather than fixed.
    for rel, base in gate.BASELINE.items():
        if gate.page_numbers(rel) != base:
            bad.append(f"BASELINE for {rel} is stale")

    if not LINES or not gate.BASELINE:
        print("ESSAY-RATCHET TESTS: VOID — no cases", file=sys.stderr)
        return 1
    if bad:
        print("ESSAY-RATCHET TESTS: FAIL")
        for b in bad:
            print(f"  {b}")
        return 1
    print(f"ESSAY-RATCHET TESTS: PASS — {len(LINES)} tokenizer case(s), "
          f"{len(CONTEXT)} context case(s), {len(gate.BASELINE)} page(s) discovered "
          f"and baselined")
    return 0


if __name__ == "__main__":
    sys.exit(main())
