#!/usr/bin/env python3
"""Check the independent-implementability ledger against SPEC §13.

WHAT THIS MEASURES, and it is not conformance. Conformance asks whether an
implementation agrees with the vectors. §13 asks whether the SPECIFICATION is
sufficient for someone who does not already know the answers to reconstruct it.
Three consecutive blind runs of this project produced the same shape of result —
the implementation passed, the vectors passed, and the implementer independently
reported it could not have built the thing honestly from the prose — which is a
failure no conformance suite can express, because a conformance suite is written
by someone who already has the answers.

WHAT IS ACTUALLY CHECKABLE HERE. A verdict is an attestation by the implementer
and cannot be re-derived by CI; re-running the experiment is the only way to
reproduce it, and a subject who has seen the answers is no longer a valid
subject. So this gate is deliberately split:

  MACHINE-CHECKED   the surface_digest is exactly what blind-export.py
                    reproduces from the recorded source commit
  STRUCTURALLY      the §13.3 fields are present; a PASS carries the
  ENFORCED          implementer's no-inference statement and an empty
                    inferred list; contamination is recorded, not omitted
  ATTESTED ONLY     the verdict itself, which points at its evidence

That split is the honest one. Claiming to verify the verdict would be the same
error the ledger exists to catch: asserting a property because the artifact
looks right rather than because anything re-derived it.

WHY THE SURFACE DIGEST IS LOAD-BEARING. "This specification is implementable" is
never a statement about a document — it is a statement about an exact set of
supplied bytes. The same commit exported with one extra file can turn
PASS-WITH-INFERENCE into PASS with the document unchanged. Binding the commit
alone would let the claim drift silently, which is precisely what §12.3's
published-model rule fixed one layer down.
"""

import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERDICTS = {"PASS", "PASS-WITH-INFERENCE", "FAIL"}
REQUIRED = ["round", "date", "sections", "source", "verdict", "inferred",
            "contamination", "evidence", "implementer_statement"]


def recompute_surface(source: str) -> str | None:
    """Re-export the pinned commit and return its surface digest, or None."""
    tmp = Path(tempfile.mkdtemp(prefix="oath-implcheck-"))
    try:
        r = subprocess.run([sys.executable, str(ROOT / "scripts" / "blind-export.py"),
                            source, str(tmp / "root")],
                           capture_output=True, text=True, cwd=ROOT)
        if r.returncode != 0:
            return None
        for line in r.stdout.splitlines():
            if "surface digest" in line:
                return line.split()[-1]
        return None
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    path = ROOT / "docs" / "implementability.json"
    doc = json.loads(path.read_text())
    rounds = doc.get("rounds", [])
    if not rounds:
        print("FAIL: the ledger has no rounds — this check is measuring nothing")
        return 1

    failures = []
    print(f"INDEPENDENT IMPLEMENTABILITY — SPEC §13\n")
    print(f"  {len(rounds)} recorded run(s)\n")

    seen = set()
    for r in rounds:
        n = r.get("round", "?")
        for f in REQUIRED:
            if f not in r:
                failures.append(f"round {n}: missing required field `{f}` (§13.3)")
        v = r.get("verdict")
        if v not in VERDICTS:
            failures.append(f"round {n}: verdict {v!r} is not one of {sorted(VERDICTS)}")
        if n in seen:
            failures.append(f"round {n}: duplicate round number")
        seen.add(n)

        # §13.2 IMPL-VERDICT-BY-SUBJECT: PASS requires the implementer's own
        # statement AND nothing inferred. An author cannot upgrade a verdict by
        # deciding in hindsight that an inference was obvious.
        if v == "PASS":
            if r.get("inferred"):
                failures.append(
                    f"round {n}: verdict PASS with {len(r['inferred'])} inferred rule(s) — "
                    f"§13.2 defines that as PASS-WITH-INFERENCE")
            if not (r.get("implementer_statement") or "").strip():
                failures.append(f"round {n}: verdict PASS without the implementer's statement (§13.2)")

        # §13.3 IMPL-CONTAMINATION-RECORDED. An empty list is a claim of none;
        # a missing key is an omission. They are different and must stay so.
        if "contamination" in r and r["contamination"] is None:
            failures.append(f"round {n}: contamination is null — record `[]` to claim none (§13.3)")

        ev = r.get("evidence")
        if ev and not (ROOT / ev).exists():
            failures.append(f"round {n}: evidence path does not exist: {ev}")

        # §13.3 IMPL-SURFACE-BOUND — the machine-checkable half.
        src, sd = r.get("source"), r.get("surface_digest")
        if sd is None:
            note = (r.get("surface_note") or "").strip()
            if not note:
                failures.append(
                    f"round {n}: no surface_digest and no surface_note — an unbound claim "
                    f"must say WHY it is unbound (§13.3)")
            status = "UNBOUND (see surface_note)"
        else:
            got = recompute_surface(src)
            if got is None:
                status = f"UNVERIFIABLE — cannot export {src[:12]} from this checkout"
                failures.append(f"round {n}: surface_digest recorded but {src[:12]} could not be exported")
            elif got != sd:
                status = f"MISMATCH — recomputed {got[:16]}…"
                failures.append(
                    f"round {n}: surface_digest {sd[:16]}… does not match the {got[:16]}… "
                    f"that {src[:12]} reproduces; the claim is bound to a surface that no "
                    f"longer exists")
            else:
                status = f"verified {sd[:16]}…"

        infn = len(r.get("inferred") or [])
        print(f"  round {n}  {v:<20} vectors {r.get('vectors','—'):<7} "
              f"§{','.join(r.get('sections', []))}")
        print(f"    surface: {status}")
        print(f"    {infn} inferred rule(s), {len(r.get('contamination') or [])} contamination note(s)")

    print()
    if failures:
        print(f"IMPLEMENTABILITY LEDGER: FAIL — {len(failures)} finding(s)")
        for f in failures:
            print(f"  {f}")
        return 1

    best = [r for r in rounds if r.get("verdict") == "PASS"]
    print("IMPLEMENTABILITY LEDGER: PASS — every claim is structurally sound and every")
    print("bound surface digest reproduces from its source commit.")
    if not best:
        print()
        print("  NOTE: no run has yet reached §13's PASS. Every recorded attempt produced a")
        print("  working implementation whose author reported that some rule had to be")
        print("  inferred rather than derived. The specification is not yet independently")
        print("  implementable, and the passing vector scores are not evidence that it is.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
