#!/usr/bin/env python3
"""The journal entry's members are enumerated TWICE in docs/SPEC.md. Derive the
comparison instead of trusting either.

§8 names the members with their meanings; §8.2.1 fixes their ORDER and is the
authority. Both are prose, both are hand-maintained, and they have drifted twice:
`parent_rev` fell out of §8's list unnoticed, and `applied_via` was never added
when §8.6.3 introduced it. Neither drift was visible to any gate, because the
conformance vectors do not exercise the members that went missing — the omission
fails OPEN, which is why a reader is the wrong instrument for it.

This does not remove the duplication; §8's annotations carry meaning §8.2.1 has
no room for. It makes the duplicate CHECKED, which is the next best thing, and it
reports which side is short rather than only that they differ.
"""
import re
import sys
from pathlib import Path

SPEC = Path(__file__).resolve().parent.parent / "docs" / "SPEC.md"


def order_list(text: str) -> list[str]:
    """§8.2.1's normative order, read from its fenced block."""
    m = re.search(r"### 8\.2\.1 Canonical entry encoding\n(.*?)```\n(.*?)```",
                  text, re.S)
    if not m:
        return []
    return [w for w in re.split(r"[,\s]+", m.group(2)) if w]


def named_list(text: str) -> set[str]:
    """§8's enumeration: the backticked members between the section's opening and
    its hand-off sentence to §8.2.1."""
    m = re.search(r"Append-only, one JSON object per line:(.*?)The exact member ORDER",
                  text, re.S)
    if not m:
        return set()
    return set(re.findall(r"`([a-z_][a-z_0-9]*)`", m.group(1)))


def main() -> int:
    text = SPEC.read_text(encoding="utf-8")
    order, named = order_list(text), named_list(text)

    # Verify the INSTRUMENT before interpreting it: an anchor that stopped
    # matching yields two empty sets, which compare equal and report success.
    if not order or not named:
        print(f"VOID — could not read one of the lists (order={len(order)} "
              f"named={len(named)}); this check did NOT run")
        return 1

    # ONE DIRECTION ONLY, and the asymmetry is principled rather than lazy.
    # §8.2.1 is the authority, so every member it orders must be named in §8.
    # The converse is not checkable here: §8 backticks STATUS VALUES and other
    # vocabulary in the same prose (`accepted`, `pending`, `require_proven`), and
    # nothing distinguishes a member name from a value name by spelling. A
    # two-way check reported nine false positives on its first run.
    missing = [m for m in order if m not in named]
    if missing:
        print("SPEC MEMBER LISTS DISAGREE")
        print(f"  in §8.2.1's order but NOT named in §8: {' '.join(missing)}")
        print("  → §8.2.1 is the authority, so §8's list is stale — and it fails OPEN:")
        print("    a reader building the entry type from §8 alone silently drops these.")
        return 1

    print(f"SPEC MEMBER LISTS: AGREE — §8 names all {len(order)} members §8.2.1 orders")
    return 0


if __name__ == "__main__":
    sys.exit(main())
