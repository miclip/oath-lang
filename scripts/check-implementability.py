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
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERDICTS = {"PASS", "PASS-WITH-INFERENCE", "FAIL"}
REQUIRED = ["round", "date", "sections", "source", "verdict", "inferred",
            "contamination", "evidence", "implementer_statement"]
# A bound claim must also record WHICH FILES were supplied. Without it the digest
# can only be reproduced by whatever the exporter happens to ship today.
REQUIRED_IF_BOUND = ["supplied"]


def recompute_surface(source: str, supplied=None) -> str | None:
    """Re-export the pinned commit and return its surface digest, or None.

    `supplied` is the file list the claim RECORDS. Reproducing with it rather than
    with the exporter's current allowlist is what makes a historical claim stay
    checkable: the allowlist evolves — a measurement script was removed from it
    after round three — and re-exporting with today's list would silently compute
    a surface the experiment never used.
    """
    tmp = Path(tempfile.mkdtemp(prefix="oath-implcheck-"))
    try:
        cmd = [sys.executable, str(ROOT / "scripts" / "blind-export.py")]
        if supplied:
            cmd += ["--paths", ",".join(supplied)]
        cmd += [source, str(tmp / "root")]
        r = subprocess.run(cmd, capture_output=True, text=True, cwd=ROOT)
        if r.returncode != 0:
            return None
        for line in r.stdout.splitlines():
            if "surface digest" in line:
                return line.split()[-1]
        return None
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def surface_declaration():
    """The NORMATIVE DATA §13.1b declares, and what blind-export actually ships.

    These must agree. §13's IMPL-SURFACE-DECLARED says the normative surface is
    the document plus the declared data — not whatever a particular export
    contained — so a declaration that can drift from the exporter reintroduces
    exactly the prose-vs-artifact gap this project keeps finding. Parsed from the
    table rather than restated here, so this check cannot agree with itself.
    """
    spec = (ROOT / "docs" / "SPEC.md").read_text()
    i = spec.index("### 13.1b")
    j = spec.index("**IMPL-DATA-SUPERSEDED", i)
    sec = spec[i:j]
    witness_at = sec.index("CONFORMANCE WITNESSES ONLY")
    declared = set(re.findall(r"`(fixtures/[^`]+|scripts/[^`]+)`", sec[:witness_at]))

    exporter = (ROOT / "scripts" / "blind-export.py").read_text()
    m = re.search(r"^DATA = \[(.*?)\]", exporter, re.S | re.M)
    shipped = set(re.findall(r'"([^"]+)"', m.group(1))) if m else set()
    return declared, shipped


def main():
    path = ROOT / "docs" / "implementability.json"
    doc = json.loads(path.read_text())
    rounds = doc.get("rounds", [])
    if not rounds:
        print("FAIL: the ledger has no rounds — this check is measuring nothing")
        return 1

    failures = []
    print(f"INDEPENDENT IMPLEMENTABILITY — SPEC §13\n")

    declared, shipped = surface_declaration()
    if not declared:
        failures.append("§13.1b declares no normative data — either the table moved or this "
                        "check stopped matching it; re-pin rather than dropping it")
    for f in sorted(declared - shipped):
        failures.append(f"§13.1b declares {f} as NORMATIVE DATA but blind-export does not ship it — "
                        f"a subject would be asked to derive from an artifact it was never given")
    for f in sorted(shipped - declared):
        failures.append(f"blind-export ships {f} as normative data but §13.1b does not declare it — "
                        f"the measured surface is larger than the specified one")
    print(f"  normative data: {len(declared)} declared, {len(shipped)} shipped"
          + ("  ✓ agree" if declared == shipped else "  ✗ DISAGREE"))
    print()
    print(f"  {len(rounds)} recorded run(s)\n")

    seen = set()
    verified = 0
    for r in rounds:
        n = r.get("round", "?")
        # Outcome fields are required of a COMPLETED round. A pre-registered one
        # has no outcome yet, and demanding one would make pre-registration
        # impossible — which is the point of separating the two states.
        if r.get("status") != "DISPATCHED":
            for f in REQUIRED:
                if f not in r:
                    failures.append(f"round {n}: missing required field `{f}` (§13.3)")
        # PRE-REGISTRATION. A round may be recorded as DISPATCHED before it
        # returns, carrying a hypothesis and no verdict. Recording the hypothesis
        # first is what makes it falsifiable: a hypothesis written after the
        # result is a narration of the result, and would quietly convert every
        # round into a confirmation.
        if r.get("status") == "DISPATCHED":
            if r.get("verdict"):
                failures.append(f"round {n}: DISPATCHED but already carries a verdict — "
                                f"remove `status` when recording the outcome")
            if not (r.get("hypothesis") or "").strip():
                failures.append(f"round {n}: DISPATCHED without a pre-registered hypothesis")
            print(f"  round {n}  DISPATCHED (pre-registered)      §{','.join(r.get('sections', []))}")
            sd_pending, src_pending = r.get("surface_digest"), r.get("source")
            got = recompute_surface(src_pending, r.get("supplied")) if sd_pending else None
            ok = got == sd_pending
            if sd_pending and not ok:
                failures.append(f"round {n}: pre-registered surface {sd_pending[:16]}… does not "
                                f"reproduce from {src_pending[:12]}")
            print(f"    surface: {'verified ' + sd_pending[:16] + '…' if ok else 'UNVERIFIED'}")
            if ok:
                verified += 1
            print(f"    hypothesis on record; outcome not yet known")
            continue

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
        if sd is not None:
            for f in REQUIRED_IF_BOUND:
                if not r.get(f):
                    failures.append(f"round {n}: bound claim without `{f}` — the digest could only "
                                    f"be reproduced by the exporter's current allowlist, not by the "
                                    f"surface actually supplied")
        if sd is None:
            note = (r.get("surface_note") or "").strip()
            if not note:
                failures.append(
                    f"round {n}: no surface_digest and no surface_note — an unbound claim "
                    f"must say WHY it is unbound (§13.3)")
            status = "UNBOUND (see surface_note)"
        else:
            got = recompute_surface(src, r.get("supplied"))
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
                verified += 1

        # §13.2 IMPL-CONSTRUCTIVE-FAILURE. Surfaced in the report because a lower
        # vector count carrying a refusal to infer is a BETTER result than a
        # higher one obtained by adaptation, and nothing else in the output says so.
        if r.get("constructive") and not (r.get("constructive_note") or "").strip():
            failures.append(f"round {n}: marked constructive with no note saying what was refused (§13.2)")

        infn = len(r.get("inferred") or [])
        print(f"  round {n}  {v:<20} vectors {r.get('vectors','—'):<7} "
              f"§{','.join(r.get('sections', []))}")
        print(f"    surface: {status}")
        print(f"    {infn} inferred rule(s), {len(r.get('contamination') or [])} contamination note(s)"
              + ("  [CONSTRUCTIVE: refused to infer]" if r.get("constructive") else ""))

    print()
    if failures:
        print(f"IMPLEMENTABILITY LEDGER: FAIL — {len(failures)} finding(s)")
        for f in failures:
            print(f"  {f}")
        return 1

    # A gate that verified nothing must not be the reason CI is green. Every round
    # being UNBOUND is a legitimate ledger state exactly once — before the export
    # harness existed — and never again, so it is reported as a failure rather than
    # allowed to pass silently.
    if verified == 0:
        print("IMPLEMENTABILITY LEDGER: FAIL — no claim's surface digest could be verified")
        print("  Every recorded round is unbound or unverifiable, so this check confirmed")
        print("  nothing. A check that quietly matches nothing is worse than no check.")
        return 1

    best = [r for r in rounds if r.get("verdict") == "PASS"]
    print(f"IMPLEMENTABILITY LEDGER: PASS — every claim is structurally sound and all")
    print(f"{verified} bound surface digest(s) reproduce from their source commits.")
    if not best:
        print()
        print("  NOTE: no run has yet reached §13's PASS. Every recorded attempt produced a")
        print("  working implementation whose author reported that some rule had to be")
        print("  inferred rather than derived. The specification is not yet independently")
        print("  implementable, and the passing vector scores are not evidence that it is.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
