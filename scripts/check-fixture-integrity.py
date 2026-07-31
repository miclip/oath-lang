#!/usr/bin/env python3
"""Fixture corpus self-authentication against its generators.

THE INVARIANT: every committed fixture has one reproducible producer, one unambiguous
identity, and an automated check that the repository contains exactly the producer's
output.

Not cryptographic authentication of authorship — structural assurance that what CI
consumes is exactly what the current generators claim to produce. A conformance suite
whose inputs can drift is measuring something other than the kernel.

WHY A COMPLETE TREE COMPARISON, not a check of known paths. Comparing paths the
generator emits can only find files that CHANGED; it cannot find files the generator
stopped emitting, which stay committed forever and quietly misrepresent what the corpus
is. `fixtures/canonical/` currently holds 62 pre-O1 `.json` files that no generator has
produced for some time. A path-based check would report success over them.

Four failure classes, deliberately distinguished because they need different fixes:

  DRIFT      committed content differs from generated  -> regenerate, or a real change
  STALE      committed but not generated               -> the generator stopped; delete
  MISSING    generated but not committed               -> generation not committed
  COLLAPSED  two logical identities, one file          -> filesystem folded them

STALE and COLLAPSED must not be confused. A file the generator no longer emits is dead
weight; a file whose CASE-VARIANT is emitted is a LIVE fixture the filesystem folded,
and deleting it removes something the suite depends on. Acting on an unqualified
"stale" list would have deleted the six golden encoding fixtures check 2 validates —
they have a different producer (hand-authored, manifest-driven) rather than none.

COLLAPSED is the subtle one and the reason generation goes to a clean temp directory:
on a case-insensitive filesystem `map.bin` and `Map.bin` are one inode, so a corpus of
187 definitions can yield 186 files with nothing reporting a problem.
"""

import base64
import filecmp
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FIXTURES = ROOT / "fixtures"


def relfiles(base: Path):
    """Every file under base, as posix-relative paths."""
    out = set()
    for p in base.rglob("*"):
        if p.is_file():
            out.add(p.relative_to(base).as_posix())
    return out


def generate(dest: Path):
    r = subprocess.run([str(ROOT / "oath" / "oath"), "fixtures", str(dest)],
                       capture_output=True, text=True, cwd=ROOT)
    if r.returncode != 0:
        print("FAIL: fixture generation failed")
        print(r.stdout[-2000:] or r.stderr[-2000:])
        sys.exit(1)


def detect_collapse(gen: Path):
    """Names that differ only by case land on one inode on a case-insensitive
    filesystem. Detect it by comparing case-insensitive identity counts."""
    problems = []
    for d in sorted({p.parent for p in gen.rglob("*") if p.is_file()}):
        seen = {}
        for p in d.iterdir():
            if not p.is_file():
                continue
            k = p.name.lower()
            seen.setdefault(k, []).append(p.name)
        for k, v in seen.items():
            if len(v) > 1:
                problems.append(f"{d.relative_to(gen)}/: {v} differ only by case")
    return problems


def main():
    failures = []

    if not (ROOT / "oath" / "oath").exists():
        print("FAIL: build the kernel first (make build)")
        return 1

    tmp = Path(tempfile.mkdtemp(prefix="oath-fixtures-"))
    try:
        generate(tmp)

        committed = relfiles(FIXTURES)
        generated = relfiles(tmp)

        stale_raw = sorted(committed - generated)
        missing = sorted(generated - committed)

        # A "stale" file whose CASE-FOLDED name IS emitted is a collision artifact, not
        # a dead file — it is a live fixture the filesystem folded onto its case-variant.
        # Reporting it as stale would advise deleting something the suite depends on,
        # which nearly happened here.
        gen_lower = {f.lower() for f in generated}
        collision_artifacts = [f for f in stale_raw if f.lower() in gen_lower]
        stale = [f for f in stale_raw if f.lower() not in gen_lower]
        common = sorted(committed & generated)

        drift = [f for f in common
                 if not filecmp.cmp(FIXTURES / f, tmp / f, shallow=False)]

        # COLLAPSED: the generator emitted N files but the corpus has fewer identities
        # than the store does. Checked against names.json, which is the authority.
        names = json.loads((ROOT / "codebase" / "names.json").read_text())
        canon = [f for f in generated if f.startswith("canonical/") and f.endswith(".bin")]
        collapsed = []
        if len(canon) != len(names):
            collapsed.append(
                f"canonical/: {len(canon)} .bin files for {len(names)} definitions — "
                f"{len(names) - len(canon)} identity(ies) have no fixture")
        collapsed += detect_collapse(tmp)

        for f in stale:
            failures.append(f"STALE      {f} — committed but no generator emits it")
        for f in collision_artifacts:
            failures.append(f"COLLAPSED  {f} — live fixture folded onto its case-variant; do NOT delete")
        for f in missing:
            failures.append(f"MISSING    {f} — generated but not committed")
        for f in drift:
            failures.append(f"DRIFT      {f} — committed content differs from generated")
        for c in collapsed:
            failures.append(f"COLLAPSED  {c}")

        # SELF-REPRODUCTION. A witness must be able to produce what it witnesses.
        # Every `canonical` envelope record pairs a STRUCTURED envelope with the
        # octets it claims to encode to — and for months the structured half
        # carried oath-publish/1's six keys while the octets carried /2's seven,
        # so no record could produce its own bytes. Both §10.1 and MANIFEST.md
        # asserted that they could. This is a defect in the MEASUREMENT
        # APPARATUS rather than in the kernel or the spec, and no existing gate
        # looked for it: drift compares committed against generated, and both
        # were equally wrong.
        env = tmp / "envelope" / "vectors.jsonl"
        if env.exists():
            checked = 0
            for line in env.read_text().splitlines():
                if not line.strip():
                    continue
                v = json.loads(line)
                if v.get("kind") != "canonical":
                    continue
                checked += 1
                octets = base64.b64decode(v["octets_b64"]).decode("utf-8")
                keys = [l.split("=", 1)[0] for l in octets.split("\n") if l][1:]
                have = sorted(v.get("envelope", {}).keys())
                if have != sorted(keys):
                    failures.append(
                        f"UNREPRODUCIBLE envelope/vectors.jsonl {v.get('label','?')!r}: structured "
                        f"half has {have}, octets encode {sorted(keys)} — the record cannot produce "
                        f"its own bytes")
                for k, val in v.get("envelope", {}).items():
                    if not isinstance(val, str):
                        failures.append(
                            f"LOSSY     envelope/vectors.jsonl {v.get('label','?')!r}: member {k!r} is "
                            f"a JSON {type(val).__name__}, not a string — a float64 JSON reader "
                            f"corrupts large values, defeating the rule the vector witnesses")
            if checked == 0:
                failures.append("UNREPRODUCIBLE no canonical envelope records found — this check "
                                "is measuring nothing")

        # MANIFEST: every emitted family must be described, or the corpus
        # under-describes itself and a candidate kernel can pass by ignoring what
        # nobody mentioned.
        manifest = (FIXTURES / "MANIFEST.md").read_text() if (FIXTURES / "MANIFEST.md").exists() else ""
        families = sorted({f.split("/")[0] for f in generated if "/" in f})
        for fam in families:
            if fam not in manifest:
                failures.append(f"UNLISTED   {fam}/ is emitted but absent from MANIFEST.md")

        print(f"generated {len(generated)} files into a clean directory; "
              f"committed tree has {len(committed)}")
        print(f"  families: {', '.join(families)}")
        print()

        if failures:
            # Counts per class FIRST, and never truncated. A gate that shows the first
            # N findings lets a whole failure class hide behind a long list of another —
            # which is the same under-reporting this check exists to catch.
            by_class = {}
            for f in failures:
                by_class.setdefault(f.split()[0], []).append(f)
            print(f"FIXTURE INTEGRITY: FAIL — {len(failures)} finding(s)")
            for cls in sorted(by_class):
                print(f"  {cls:<10} {len(by_class[cls])}")
            print()
            for cls in sorted(by_class):
                for f in by_class[cls][:6]:
                    print(f"  {f}")
                if len(by_class[cls]) > 6:
                    print(f"  {cls:<10} ... and {len(by_class[cls]) - 6} more of this class")
            print()
            print("Every committed fixture must have one reproducible producer and one")
            print("unambiguous identity. A suite whose inputs can drift is measuring")
            print("something other than the kernel.")
            return 1

        print("FIXTURE INTEGRITY: PASS")
        print("  the committed tree is exactly the current generators' output —")
        print("  no drift, no stale files, no collapsed identities, manifest complete.")
        return 0
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
