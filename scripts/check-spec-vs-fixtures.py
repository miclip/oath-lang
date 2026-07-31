#!/usr/bin/env python3
"""Assert that the normative prose describes the bytes the kernel actually emits.

WHY THIS EXISTS. SPEC §8.6.1 documented an `oath-publish/1` envelope of six
`key=value` lines for several commits after the kernel began emitting
`oath-publish/2` with seven. Every other gate passed throughout: the kernel was
self-consistent, the fixtures were self-consistent, and the fixtures matched the
kernel. Nothing compared either against the DOCUMENT, so an independent
implementation reading the normative text would have built `/1` and failed every
canonical vector — which is exactly how it was eventually found, by a blind agent
rather than by CI.

This is a distinct failure class from the ones already gated:

  check-fixture-integrity   committed fixtures == generator output   (bytes vs bytes)
  check-doc-numbers         prose figures == the machine's ledger    (claims vs counts)
  conformance mutation      every rule has a vector that notices it  (rules vs vectors)
  THIS                      prose FORMAT == fixture bytes            (spec vs bytes)

The spec is the conformance target. When it drifts, every downstream check keeps
passing while the one artifact an independent implementer actually reads becomes
wrong — the most expensive possible place for a defect, because it is invisible
to everyone who already has the source.

DELIBERATELY STRUCTURAL, not a spelling check. It extracts the envelope shape the
prose declares — version token and field names in order — and compares it against
the shape a real fixture carries. A reworded sentence is fine; a renamed, added,
removed, or reordered field is not.
"""

import base64
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def spec_envelope_shapes():
    """Every fenced envelope block declared in SPEC §8.6.1, as (version, [fields])."""
    spec = (ROOT / "docs" / "SPEC.md").read_text()
    i = spec.index("#### 8.6.1")
    j = spec.index("#### 8.6.2")
    shapes = []
    for block in re.findall(r"```\n(.*?)```", spec[i:j], re.S):
        lines = [l for l in block.strip().split("\n") if l.strip()]
        if not lines or not lines[0].startswith("oath-publish/"):
            continue
        fields = [l.split("=", 1)[0] for l in lines[1:]]
        shapes.append((lines[0], fields))
    return shapes


def fixture_envelope_shape():
    """The shape a canonical envelope fixture actually carries."""
    path = ROOT / "fixtures" / "envelope" / "vectors.jsonl"
    for line in path.read_text().splitlines():
        if not line.strip():
            continue
        v = json.loads(line)
        if v.get("kind") != "canonical":
            continue
        octets = base64.b64decode(v["octets_b64"]).decode("utf-8")
        lines = octets.split("\n")
        if lines and lines[-1] == "":
            lines.pop()
        # Split at the FIRST `=` only: §8.6.1 permits `=` inside a value.
        return lines[0], [l.split("=", 1)[0] for l in lines[1:]]
    return None, None


def main():
    fix_ver, fix_fields = fixture_envelope_shape()
    if fix_ver is None:
        print("FAIL: no canonical envelope vector found — this check is measuring nothing")
        return 1

    shapes = spec_envelope_shapes()
    if not shapes:
        print("FAIL: §8.6.1 declares no envelope block — either the section moved or the")
        print("      fence format changed. Re-pin this check rather than deleting it.")
        return 1

    print(f"fixture emits : {fix_ver} with {len(fix_fields)} field(s)")
    print(f"                {', '.join(fix_fields)}")
    print(f"§8.6.1 declares {len(shapes)} envelope shape(s): {', '.join(v for v, _ in shapes)}")
    print()

    match = [s for s in shapes if s[0] == fix_ver]
    if not match:
        print(f"SPEC vs FIXTURES: FAIL")
        print(f"  the kernel emits {fix_ver}, which §8.6.1 does not document at all.")
        print(f"  documented: {', '.join(v for v, _ in shapes)}")
        print()
        print("  An implementation built from the normative text would produce a version")
        print("  the conformance vectors do not contain, and fail every one of them.")
        return 1

    spec_ver, spec_fields = match[0]
    if spec_fields != fix_fields:
        print("SPEC vs FIXTURES: FAIL")
        print(f"  {spec_ver} field order disagrees between the prose and the bytes.")
        print(f"    §8.6.1  : {', '.join(spec_fields)}")
        print(f"    fixture : {', '.join(fix_fields)}")
        print()
        print("  Field order is inside the signed bytes, so this is not cosmetic:")
        print("  a signature made under one order does not verify under the other.")
        return 1

    print("SPEC vs FIXTURES: PASS")
    print(f"  §8.6.1 documents {spec_ver} with exactly the fields, in the order, that the")
    print(f"  canonical vector carries — the normative text describes the emitted bytes.")
    older = [v for v, _ in shapes if v != fix_ver]
    if older:
        print(f"  ({len(older)} superseded shape(s) still documented for historical")
        print(f"  verification, per §8.6.4: {', '.join(older)})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
