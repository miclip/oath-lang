#!/usr/bin/env python3
"""Rule-to-vector matrix for SPEC §12 (license evaluation).

WHAT THIS CATCHES, and it is the opposite failure from the one the conformance scorer
catches. The scorer measures whether disabling an IMPLEMENTATION rule breaks a vector.
This measures whether every NORMATIVE rule in the prose has a vector at all — prose that
looks complete while nothing witnesses its load-bearing clauses.

Without stable identifiers the two failures are indistinguishable: "the vectors constrain
something the prose does not state" and "the prose states something no vector witnesses"
both present as a mismatch, and only one of them is a specification defect.

Run before dispatching any blind implementation. A blind run against unwitnessed prose
cannot tell a specification defect from the deliberate absence of a fixture, so the
matrix has to exist first or the run answers a question nobody asked.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Which vector is expected to witness which normative rule. Written from the SPEC
# identifiers, not from the fixtures, so a missing fixture shows up as a hole rather
# than being quietly defined out of existence.
# Rules the VECTOR family cannot reach, with the reason. Distinguished from
# unwitnessed because "this family cannot express it" and "nobody wrote it" need
# different fixes — conflating them is the failure this script exists to prevent.
OUT_OF_SCOPE = {
    "LICENSE-CLOSURE-EXCLUSIVE":
        "needs a STORE with an artifact outside the closure; a vector is (assertions, verdict) "
        "and cannot express 'and this unrelated publication changed nothing'",
    "LICENSE-MODEL-PUBLISHED":
        "an obligation about a FILE existing and matching the kernel, not about a verdict. "
        "Enforced by check-fixture-integrity, which regenerates license/model.json and "
        "byte-compares it — a vector cannot witness its own inputs being published",
}

# A rule identifier can cover SEVERAL independent clauses, and witnessing was
# tracked per identifier. LICENSE-IDENTITY-UNAMBIGUOUS has two — the two-line split
# and the character exclusion — and one vector covering the split made the whole rule
# read "witnessed" while the clause the prose itself calls "a forgery surface" had no
# vector at all. That clause was then demonstrated to reproduce a PUBLISHED digest.
# A rule listed here with N clauses now requires N claiming vectors.
CLAUSES = {
    "LICENSE-IDENTITY-UNAMBIGUOUS": [
        "the two-line split, so a name containing `=` cannot shift into the expression",
        "the character exclusion, so an embedded LF cannot inject an input line",
    ],
    "LICENSE-POLICY-DEFINED": [
        "an unrecognised policy is a different identity",
        "an unrecognised policy is not EVALUATED as composition",
    ],
    "LICENSE-INPUT-COMPLETE": [
        "a member asserting nothing still contributes",
        "a member appearing twice contributes twice",
    ],
    "LICENSE-ORDER-INDEPENDENT": [
        "presentation order does not change the digest",
        "artifact hash alone is not a total order",
    ],
    "LICENSE-LOOKUP-EXACT": [
        "case is significant",
        "surrounding punctuation is not stripped",
    ],
}

EXPECTED = {
    "LICENSE-LOOKUP-UNKNOWN": "unknown identifier among permissive dependencies",
    "LICENSE-LOOKUP-COMPOUND": "compound expression is not resolved",
    "LICENSE-PERMISSION-NO": "a known prohibition binds the composition",
    "LICENSE-PERMISSION-UNKNOWN": "absent assertion among permissive dependencies",
    "LICENSE-OBLIGATION-YES": "share-alike obligation propagates to the root",
    "LICENSE-OBLIGATION-UNKNOWN": "obligation dimension with an unknown input",
    "LICENSE-ORDER-INDEPENDENT": "input order does not change the digest",
    "LICENSE-IDENTITY-INPUT": "digest over method and sorted inputs",
    "LICENSE-CLOSURE-EXCLUSIVE": "an artifact outside the closure changes nothing",
    "LICENSE-MODEL-VERSIONED": "changing the model changes the evaluation",
    "LICENSE-IDENTITY-UNAMBIGUOUS": "a name containing = does not collide with a shifted split",
    "LICENSE-LOOKUP-EXACT": "a case-variant identifier is not the identifier",
    "LICENSE-LOOKUP-PRECEDENCE": "a compound is not resolved even when its operand is modelled",
    "LICENSE-POLICY-DEFINED": "an unrecognised policy is a different evaluation",
    "LICENSE-INPUT-COMPLETE": "a member asserting nothing still contributes an input pair",
    "LICENSE-ENGINE-DEFINED": "changing the engine changes the evaluation",
    "LICENSE-PUBLICATION-SENTINEL": "an undeterminable publication encodes the sentinel",
    "LICENSE-IDENTITY-ARTIFACT": "renaming members does not change the evaluation",
    "LICENSE-IDENTITY-PUBLICATION": "the same terms from a different publication is a different evaluation",
    "LICENSE-IDENTITY-MODEL-CONTENT": "editing the lattice changes the digest even under a fixed version string",
}


def spec_rules():
    spec = (ROOT / "docs" / "SPEC.md").read_text()
    sec = spec[spec.index("## 12. License evaluation"):]
    return sorted(set(re.findall(r"LICENSE-[A-Z-]+", sec)))


def vector_claims():
    """Rules the fixture family actually claims to witness, plus every label."""
    claims, labels = {}, []
    path = ROOT / "fixtures" / "license" / "vectors.jsonl"
    for line in path.read_text().splitlines():
        if not line.strip():
            continue
        v = json.loads(line)
        labels.append(v.get("label", ""))
        w = v.get("witnesses")
        if w:
            claims.setdefault(w, []).append(v.get("label", ""))
    return claims, labels


def main():
    rules = spec_rules()
    claims, labels = vector_claims()

    print("RULE-TO-VECTOR MATRIX — SPEC §12\n")
    print(f"  {len(rules)} normative rule identifier(s) in the prose")
    print(f"  {len(labels)} vector(s) in the family, {len(claims)} distinct rule(s) claimed\n")

    unwitnessed = []
    for r in rules:
        if r in OUT_OF_SCOPE:
            print(f"  [out-of-scope] {r:<28} {OUT_OF_SCOPE[r]}")
            continue
        want = EXPECTED.get(r, "")
        # A rule is witnessed if some vector claims it OR a vector's label plainly
        # covers the expectation. Label matching is a fallback, and deliberately loose:
        # a false NEGATIVE here (reporting a hole that is really covered) is cheap, a
        # false positive would hide exactly what this script exists to find.
        by_claim = claims.get(r, [])
        by_label = [l for l in labels if want and all(w in l.lower() for w in want.lower().split()[:3])]
        need = CLAUSES.get(r)
        if need and len(by_claim) < len(need):
            print(f"  [PARTIAL    ] {r:<28} {len(by_claim)} vector(s) for {len(need)} clause(s)")
            for c in need[len(by_claim):]:
                print(f"                 unwitnessed clause: {c}")
            unwitnessed.append(f"{r} (clause coverage: {len(by_claim)}/{len(need)})")
        elif by_claim:
            extra = f" [{len(by_claim)}/{len(need)} clauses]" if need else ""
            print(f"  [witnessed  ] {r:<28} claimed by: {by_claim[0][:44]}{extra}")
        elif by_label:
            print(f"  [by-label   ] {r:<28} likely: {by_label[0][:44]}")
            print(f"                 (no vector CLAIMS this rule; add `witnesses` to make it explicit)")
        else:
            print(f"  [UNWITNESSED] {r:<28} expected: {want or '(no expectation recorded)'}")
            unwitnessed.append(r)

    orphans = [c for c in claims if c.upper() not in {r.upper() for r in rules}
               and not c.upper().startswith("LICENSE-")]
    if orphans:
        print("\n  vectors claiming rules that are NOT normative identifiers:")
        for o in sorted(orphans):
            print(f"    {o}  — the prose does not name this; either add it or re-point the vector")

    print()
    if unwitnessed:
        print(f"MATRIX: INCOMPLETE — {len(unwitnessed)} normative rule(s) have no vector")
        for r in unwitnessed:
            print(f"  {r}")
        print("\nDispatching a blind implementation now would be unable to distinguish a")
        print("specification defect from a deliberately absent fixture, because an")
        print("unwitnessed rule produces the same symptom as an unstated one.")
        return 1
    print("MATRIX: COMPLETE — every normative rule this family can reach has a vector")
    if OUT_OF_SCOPE:
        print(f"  ({len(OUT_OF_SCOPE)} rule(s) out of scope for this family, listed above with the reason)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
