#!/usr/bin/env python3
"""Check SPEC §14.2's transformation table is a total, single-valued mapping.

WHY THIS EXISTS. #122's closure criterion stopped being "a blind round reports
fewer unresolved items" and became a STRUCTURAL property: every transport
distinction has exactly one disposition, owned by exactly one layer. That is only
a better criterion if it is checked rather than asserted — otherwise it is the
same judgement call with a firmer voice.

The table earned this. It was rewritten after three successive repairs each fixed
a real omission and introduced another at the boundary it touched, and twelve
further defects were found IN the table during its own construction: a row
restating another, a disposition assigned to a layer that cannot observe it, a
refusal ordered after the values it compares are destroyed, a miscount. Every one
would have been caught here.

WHAT IT CHECKS, and the limit is stated because the check is narrower than the
criterion: that row numbers are unique, that each row carries exactly one
disposition from the declared vocabulary, that each names exactly one layer, and
that the prose's own counts agree with the rows. It CANNOT check that the
disposition is the RIGHT one, or that no transport fact is missing — those are
what a blind round is for, and the point of the table is that such a round can
now report "wrong disposition" or "missing row" instead of "I could not tell
where this rule lives".

IT ALSO CANNOT DETECT A SEMANTIC DUPLICATE. Two rows restating one distinction in
different words — "a message that cannot be framed" and "an empty malformed
message" — pass, because the cells are prose and normalized prose differs. Closing
that would need a stable machine-readable identity per row in the specification
itself, which is a change to §14 rather than to this gate, and is not worth making
for a hazard review can see and a regex cannot. The limit is recorded so a later
reader does not mistake this gate's PASS for a proof of single-valuedness; it
proves the mapping is total over a pinned inventory, single-valued by row number,
and consistent with the counts §14.3 states about it.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DISPOSITIONS = {"PRESERVE", "CANONICALIZE", "DISCARD", "LIFT", "REFUSE"}
LAYERS = {"parser", "adapter"}
# The permitted forms, matched WHOLE. Testing for the substring "or" anywhere
# would accept "parser and adapter (or another layer)", which assigns ownership
# to both — the thing the column exists to forbid. The either/or form exists
# because §14.2a offers two boundary shapes and which layer can discharge a
# refusal follows from which shape the parser supplies.
ALLOWED_LAYERS = (["parser"], ["adapter"], ["parser", "or", "adapter"])
# THE EXPECTED INVENTORY, pinned. Without it the gate validates whichever rows
# happen to be present and a deleted row passes silently — so it could not
# establish the totality it advertises. 25 is absent deliberately: it restated
# row 24 and was retired, and numbers are labels rather than a sequence.
#
# Adding or removing a row REQUIRES editing this set, which is the intended
# friction: a change to which transport facts §14 disposes of is a normative
# change and should not be possible to make silently.
EXPECTED_ROWS = {
    1: "method", 2: "target", 3: "absolute", 4: "authority", 5: "host",
    6: "method", 7: "scheme", 8: "case", 9: "octets", 10: "value",
    11: "ows", 12: "folding", 13: "order", 14: "repeats", 15: "connection",
    16: "nominated", 17: "trailer", 18: "body", 19: "coding", 20: "receipt",
    21: "version", 22: "host", 23: "authority", 24: "framed",
    26: "empty", 27: "http",
}
# The VALUE is an identity token, not the full wording. Pinning whole sentences
# would break on any legitimate rewrite; pinning nothing lets a row's meaning be
# replaced while its number survives, which passes an inventory check on numbers
# alone. A token is the middle: reword freely, but row 2 must still be about the
# request target.
# A table line that looks like data: starts with `|`, is not the header, and is
# not the `|---|` separator.
UNPARSED = re.compile(r"\|(?!\s*#\s*\|)(?!\s*-+\s*\|)\s*[^|]*\S")


def rows(spec: str):
    """Yield (number, distinction, where, disposition, rule) for each table row."""
    start = spec.index("### 14.2 The transformation")
    end = spec.index("### 14.2a")
    for line in spec[start:end].splitlines():
        m = re.match(r"\|\s*(\d+)\s*\|(.+?)\|(.+?)\|(.+?)\|(.*)\|\s*$", line)
        if m:
            yield (int(m.group(1)), *(g.strip() for g in m.groups()[1:]))
        elif UNPARSED.match(line):
            # A DATA-LIKE line that did not parse is a FAILURE, not an absence.
            # A lost delimiter or a non-numeric label would otherwise drop a row
            # silently and the gate would certify a table it had not read — the
            # precise fail-open shape every gate in this session started with.
            yield ("?", line.strip()[:70], "", "", "")


def main() -> int:
    spec = (ROOT / "docs" / "SPEC.md").read_text()
    bad, seen = [], {}
    table = list(rows(spec))
    if not table:
        print("TRANSFORMATION TABLE: no rows parsed — the gate is measuring nothing")
        return 1

    got = {n for n, *_ in table if isinstance(n, int)}
    for num, what, *_ in table:
        if isinstance(num, int) and num in EXPECTED_ROWS:
            token = EXPECTED_ROWS[num]
            # WHOLE WORDS. Substring membership would accept "methodology" for
            # "method" and "ghost" for "host" — the silent substitution this
            # inventory exists to reject, wearing the token as a costume.
            words = re.sub(r"[^a-z0-9]+", " ", what.lower()).split()
            if token not in words:
                bad.append(f"row {num} no longer mentions {token!r}: {what[:50]!r} — a row's "
                           f"NUMBER surviving while its distinction is replaced is silent "
                           f"substitution, which an inventory of numbers alone cannot see")
    if got != set(EXPECTED_ROWS):
        for missing in sorted(set(EXPECTED_ROWS) - got):
            bad.append(f"row {missing} is MISSING — the table must dispose of every listed distinction")
        for extra in sorted(got - set(EXPECTED_ROWS)):
            bad.append(f"row {extra} is not in the expected inventory — adding a distinction is a "
                       f"normative change and must update EXPECTED_ROWS deliberately")

    seen_what = {}
    for num, what, where, disp, _rule in table:
        if num == "?":
            bad.append(f"a table line did not parse as a row: {what!r}")
            continue
        if num in seen:
            bad.append(f"row {num} appears twice ({seen[num][:40]!r} and {what[:40]!r})")
        seen[num] = what
        # Keyed on the DISTINCTION too, not just the label. The same distinction
        # under two numbers is a mapping that is not single-valued — which is the
        # property this gate exists to certify — and row 25 was exactly that
        # before it was retired.
        key = re.sub(r"[^a-z0-9]+", " ", what.lower()).strip()
        if key and key in seen_what:
            bad.append(f"rows {seen_what[key]} and {num} name the same distinction {what[:44]!r}")
        seen_what[key] = num

        # EXACTLY ONE disposition. A row naming two would be the distributed model
        # returning: a distinction whose handling a reader must assemble.
        named = {d for d in DISPOSITIONS if re.search(rf"\b{d}\b", disp)}
        if len(named) != 1:
            bad.append(f"row {num} names {len(named)} disposition(s) {sorted(named)} — must be exactly one")

        # EXACTLY ONE owning layer, or "parser or adapter" where §14.2a offers two
        # boundary forms and which layer can discharge the row follows from which
        # form the parser supplies.
        # The permitted forms are EXACTLY three, matched whole rather than by
        # substring: one layer, or the specific either/or §14.2a's two boundary
        # forms create. Testing for "or" anywhere would accept "parser and
        # adapter (or another layer)", which assigns ownership to both — the
        # thing the column exists to forbid.
        plain = re.sub(r"[^a-z ]", " ", where.lower()).split()
        if plain not in ALLOWED_LAYERS:
            bad.append(f"row {num} owning layer {where!r} is not one of: "
                       f"parser, adapter, 'parser or adapter'")

    # The prose counts refusal rows. A count asserted about a set rather than
    # counted from it is the specific error §14.3 records making three times.
    actual = sorted(n for n, _w, _l, d, _r in table if "REFUSE" in d)
    WORDS = {"one":1,"two":2,"three":3,"four":4,"five":5,"six":6,"seven":7,"eight":8,
             "nine":9,"ten":10,"eleven":11,"twelve":12,"thirteen":13}
    m = re.search(r"table contains (\w+) refusal rows — ([\d, and]+) —", spec)
    if not m:
        bad.append("no refusal-row count found in §14.3 to check against the table")
    else:
        claimed = sorted(int(x) for x in re.findall(r"\d+", m.group(2)))
        if claimed != actual:
            bad.append(f"§14.3 claims refusal rows {claimed}; the table has {actual}")
        # The WRITTEN NUMERAL too, not only the list. "SIX refusal rows — 9, 22,
        # 23, 24 and 27" enumerates five and says six, which is exactly the
        # miscount §14.3 records making three times and this gate exists to end.
        word = WORDS.get(m.group(1).lower())
        if word is None:
            bad.append(f"§14.3's refusal count {m.group(1)!r} is not a number word this gate can check")
        elif word != len(actual):
            bad.append(f"§14.3 says {m.group(1).upper()} refusal rows but enumerates {len(actual)}")

    if bad:
        print("TRANSFORMATION TABLE: FAIL\n")
        for b in bad:
            print(f"  {b}")
        return 1

    print(f"TRANSFORMATION TABLE: PASS — {len(table)} rows, each with exactly one "
          f"disposition and one owning layer; refusal rows {actual} match §14.3")
    return 0


if __name__ == "__main__":
    sys.exit(main())
