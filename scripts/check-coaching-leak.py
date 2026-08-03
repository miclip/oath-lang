#!/usr/bin/env python3
"""Fail if always-loaded coaching material restates normative rule content.

WHY THIS EXISTS. A dispatched blind subject inherits this project's CLAUDE.md in
its system prompt — the IMPL-ISOLATED-SESSION violation §13 records and the
harness cannot yet remove. So anything CLAUDE.md says about a section's RULES is
handed to a subject that is supposed to derive them from the specification. The
export preflight cannot see this: it checks the archive, and the leak is not in
the archive.

It is not a hypothetical failure mode. Preparing one round, CLAUDE.md was found
to list a section's rules verbatim and was reduced to a pointer. Within ten
minutes of writing that down, the same file acquired the leak twice more — a
summary of what the repairs changed, and a list of which rules had been
withdrawn. Each was caught by review rather than by the author, which is the
argument for a check: the rule was known, stated, and freshly violated.

WHAT IT LOOKS FOR. Normative rule IDENTIFIERS, which are the compact form the
leak takes: `REQ-…`, `PROTO-…`, `HDR-…`, `IMPL-…`, `AUTH-…`. Naming one in
always-loaded material means the material is describing a rule rather than
pointing at it.

WHAT IT DELIBERATELY DOES NOT DO. It does not try to detect a paraphrase. A
sentence restating a rule without naming it would pass, and no cheap check finds
that. This closes the compact form and reduces the surface; it does not make the
prohibition self-enforcing, and CLAUDE.md still carries the rule in words.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Files a dispatched session inherits. NOT docs/ — a subject reads only what the
# export supplies, and the export's own preflight governs that.
COACHING = ["CLAUDE.md"]

# The identifier set is DERIVED FROM THE SPECIFICATION, not hardcoded. An earlier
# version listed five prefixes and silently ignored every other family the SPEC
# defines — CONF, DEL, ENV, LICENSE, RES, SIG, XFER among them — so the gate
# enforced a fraction of its stated prohibition while reporting success. Reading
# the real set means a new rule family is covered the moment it is written.
# ONE shape, used for both the declaration scan and the coaching scan. They were
# briefly different — the scan demanded a longer suffix than the declaration
# pattern allowed — so short rules like DEL-CAS were collected into the set and
# then never looked for. A gate whose two halves disagree about what it is
# looking for reports success by construction.
_IDENT = r"[A-Z][A-Z0-9]+(?:-[A-Z0-9]+)+"
RULE_ID = re.compile(r"\b" + _IDENT + r"\b")
# EVERY identifier inside a bold span, declaration or reference alike.
#
# Four narrower rules were tried and each missed real ones: anchoring to the
# opening `**` dropped the second of a declared pair; requiring a period inside
# the span dropped `**ENV-FIELD-COUNT**.`; requiring one immediately after
# dropped `**RES-PINNED-RECOVERABLE** (prerequisite).`; and some rules appear in
# SPEC only as mid-sentence references, so no declaration form finds them at all.
#
# So the direction was inverted. For a LEAK gate a false negative is the
# dangerous outcome — it reports success while the answer sits in a subject's
# system prompt — while a false positive costs one line in ALLOWED with a reason
# beside it. Collect broadly; exempt explicitly.
# The span, PLUS the character after it: the sentence-ending period sits inside
# the bold for some declarations (`**REQ-X.**`) and outside for others
# (`**ENV-FIELD-COUNT**.`), and testing only the inside missed the second form
# entirely — several ENV and RES rules were collected by nothing.
RULE_SPAN = re.compile(r"\*\*(.+?)\*\*(.?)", re.S)


# A rule is DECLARED as a bold identifier opening its paragraph:
#   **REQ-METHOD-VERBATIM.**            **PROTO-TYPES-BY-IDENTITY.**
#   **IMPL-CONTAMINATION-IS-NOT-A-MEASUREMENT (normative).**
# Matching declarations rather than every hyphenated capital is what separates a
# RULE from a value that merely looks like one — `PASS-WITH-INFERENCE` is a
# verdict, and a round's verdict is a result CLAUDE.md may legitimately state.


def spec_rule_ids() -> set:
    """Every normative rule identifier docs/SPEC.md DECLARES."""
    spec = (ROOT / "docs" / "SPEC.md").read_text()
    ids = set()
    for span, _ in RULE_SPAN.findall(spec):
        ids.update(RULE_ID.findall(span))
    return ids

# Identifiers that are ABOUT the harness rather than about a specification a
# subject might be asked to implement. Naming these coaches nobody: a blind
# subject is never asked to derive §13's methodology, and CLAUDE.md has to be
# able to state the isolation rule it is itself subject to.
ALLOWED = {
    # Bold TERMS that match the identifier shape but name no rule. Listed rather
    # than excluded by a pattern, because every pattern that separated them also
    # dropped real rules.
    "TWO-LEVEL",
    "SELF-REPRODUCTION",
    "ENVIRONMENT-CONSTRAINED",
    "FULLY-REPRODUCIBLE",
    "PASS-WITH-INFERENCE",
    # Harness rules. A blind subject is never asked to derive §13's methodology,
    # and CLAUDE.md must be able to state the isolation rule it is subject to.
    "IMPL-ISOLATED-SESSION",
    "IMPL-CONTAMINATION-IS-NOT-A-MEASUREMENT",
    "IMPL-VERDICT-SCOPED",
    "IMPL-PRE-REGISTERED",
    "IMPL-REPRODUCIBLE-INSTRUMENT",
    "IMPL-REPRODUCIBILITY-CLASS",
}


def main() -> int:
    defined = spec_rule_ids()
    if not defined:
        print("COACHING LEAK: no rule identifiers found in docs/SPEC.md — the gate "
              "cannot be measuring anything; check the pattern")
        return 1
    bad = []
    for rel in COACHING:
        p = ROOT / rel
        if not p.exists():
            continue
        for n, line in enumerate(p.read_text().splitlines(), 1):
            for m in RULE_ID.finditer(line):
                ident = m.group(0)
                if ident in defined and ident not in ALLOWED:
                    bad.append((rel, n, ident, line.strip()[:90]))

    if bad:
        print("COACHING LEAK: always-loaded material names normative rule identifiers.\n")
        for rel, n, ident, line in bad:
            print(f"  {rel}:{n}  {ident}")
            print(f"    {line}")
        print("\nA dispatched blind subject inherits these files in its system prompt, so")
        print("naming a rule hands it what it is supposed to derive. Point at the section")
        print("instead, and put the detail somewhere a subject does not inherit —")
        print("docs/milestones.md, or the issue.")
        print("\nIf the identifier is about the measurement harness rather than about a")
        print("specification under test, add it to ALLOWED in this script, with a reason.")
        return 1

    print(f"COACHING LEAK: none — {len(COACHING)} always-loaded file(s) name no rule identifiers")
    return 0


if __name__ == "__main__":
    sys.exit(main())
