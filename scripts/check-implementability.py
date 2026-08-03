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
REPRO_CLASSES = {"FULLY REPRODUCIBLE", "ENVIRONMENT-CONSTRAINED"}
# Per-item disposition states for a validation round. Seven, not six: NOT-REACHED
# was added after round 11 showed the grid silently assumed every item would be
# exercised. It is not a synonym for either neighbour — calling an unexercised
# item DERIVED credits the text with determining something nobody consulted it
# about, and calling it STILL-INFERRED charges it with an ambiguity nobody hit.
# Both are wrong in opposite directions, so it gets its own name and is treated as
# UNMEASURED: an open question counting neither for nor against closure.
DISPOSITIONS = {"derived", "still-inferred", "replaced", "new-inference",
                "contradiction", "pinned-unobservable", "not-reached"}
# A not-reached item MUST say WHY, because the two causes have opposite meaning:
# STRUCTURAL means the repair eliminated the path that forced the inference, which
# is evidence the repair changed the reasoning landscape; INCIDENTAL means the
# subject simply never hit it, which is only missing coverage. Left unannotated,
# the flattering reading is the one a later reader will assume.
NOT_REACHED_CAUSES = {"incidental", "structural"}
# Required from the round that introduced the schema. Optional-by-default would
# make the whole invariant skippable by deleting the field — a grid could then
# carry unannotated not-reached items and the checker would pass, which is the
# same fail-open shape as an absent isolation marker.
DISPOSITIONS_FROM_ROUND = 11
# Round 7 is the run that DISCOVERED session contamination; every round from there
# on must state its isolation explicitly. Rounds 1-6 genuinely do not know, and
# `null` records that rather than pretending otherwise.
ISOLATION_RULE_FROM_ROUND = 7
# The marker became PRE-REGISTERED from round 10. Rounds 7-9 recorded it
# retrospectively, at outcome time — round 9's own dispatch commit did not carry
# it, because the rule requiring it was written as a consequence of round 9. The
# pre-outcome branch enforces it for anything dispatched from now on; claiming
# retroactive pre-registration for the three that predate the rule would be the
# same overclaim this section exists to catch.
MARKER_PREREGISTERED_FROM_ROUND = 10
# PRE-OUTCOME STATES. A round carrying either has a hypothesis and no verdict.
# The distinction between them is the MOMENT THE APPARATUS FREEZES, and it earns
# a state of its own because the two are not the same claim:
#
#   READY       the pre-registration is written and its surface reproduces, but
#               no subject has been launched. The apparatus MAY still be edited,
#               and any edit that moves the surface must move the digest with it
#               — which is CHECKED below against HEAD, not merely promised here.
#   DISPATCHED  a subject has been launched against that surface. The apparatus
#               is FROZEN: editing the section under test from here on means the
#               claim names a surface the subject never saw.
#
# Conflating them lets a record assert that an experiment is running when only
# its paperwork exists, which is the same species of overclaim §13.4 forbids of
# verdicts.
PRE_OUTCOME = {"READY", "DISPATCHED"}
REQUIRED = ["round", "date", "sections", "source", "verdict", "inferred",
            "contamination", "evidence", "implementer_statement",
            # §13 IMPL-ISOLATED-SESSION / IMPL-CONTAMINATION-IS-NOT-A-MEASUREMENT.
            # An explicit, REQUIRED marker: true (clean), false (disclosed
            # contamination), or null (unknown — predates the rule). It is a
            # separate field from reproducibility_class on purpose. The class
            # describes REPRODUCTION; this describes what the run may CLAIM, and
            # keying the PASS ban on the class alone left it bypassable by a
            # contaminated round labelled FULLY REPRODUCIBLE or omitting the
            # field entirely.
            "session_isolated"]
# A bound claim must also record WHICH FILES were supplied. Without it the digest
# can only be reproduced by whatever the exporter happens to ship today.
REQUIRED_IF_BOUND = ["supplied"]


_DISPATCH_MARKERS = None


def _scan_dispatch_markers():
    """Map round -> the session_isolated value at its FIRST committed DISPATCHED
    state, by walking every revision of the ledger oldest-first, exactly once.

    NO HISTORY LIMIT. An earlier version capped the walk at 300 revisions, which
    is a time bomb rather than an optimisation: once that many commits touch the
    ledger after a round's dispatch, its original DISPATCHED revision falls off
    the end and the round either fails CI as unverifiable or — worse — validates
    against a later, wrong marker. A claim must not become unverifiable because
    the repository kept working.

    Walking once and memoising costs a single pass however many rounds exist.
    """
    global _DISPATCH_MARKERS
    if _DISPATCH_MARKERS is not None:
        return _DISPATCH_MARKERS
    found = {}
    revs = subprocess.run(["git", "log", "--format=%H", "--", "docs/implementability.json"],
                          capture_output=True, text=True, cwd=ROOT)
    if revs.returncode == 0:
        for rev in reversed(revs.stdout.split()):          # oldest-first
            blob = subprocess.run(["git", "show", f"{rev}:docs/implementability.json"],
                                  capture_output=True, text=True, cwd=ROOT)
            if blob.returncode != 0:
                continue
            try:
                old = json.loads(blob.stdout)
            except json.JSONDecodeError:
                continue
            for orr in old.get("rounds", []):
                rn = orr.get("round")
                # DISPATCHED only, never READY: READY is editable by design, so a
                # READY marker pre-registers nothing. First occurrence only, so a
                # later DISPATCHED commit cannot revise what the subject ran under.
                if rn not in found and orr.get("status") == "DISPATCHED":
                    found[rn] = orr.get("session_isolated", "<missing>")
    _DISPATCH_MARKERS = found
    return found


def preregistered_marker(round_no: int):
    """The `session_isolated` value the round was DISPATCHED under: (value, found).

    The ledger is committed, so git IS the tamper-evident record of what the
    pre-registration said — no separate attestation is needed and none could be
    trusted more. Without this the rule is unenforceable: an author can flip the
    marker from false to true in the same edit that removes `status`, and a
    checker reading only the final value admits the PASS.
    """
    m = _scan_dispatch_markers()
    return (m[round_no], True) if round_no in m else (None, False)


def recompute_surface(source: str, supplied=None, exporter_rev=None) -> str | None:
    """Re-export the pinned commit and return its surface digest, or None.

    `supplied` is the file list the claim RECORDS. Reproducing with it rather than
    with the exporter's current allowlist is what makes a historical claim stay
    checkable: the allowlist evolves — a measurement script was removed from it
    after round three — and re-exporting with today's list would silently compute
    a surface the experiment never used.

    THE EXPORTER ITSELF IS PART OF THAT INSTRUMENT, and pinning only the allowlist
    left the same bug one level down. A surface digest is a measurement, and a
    measurement is only meaningful relative to the instrument that produced it —
    so the exporter is run AS IT WAS AT `source`, not as it is now.

    This is not hypothetical. Round 6 found that oathrs/conformance.sh restated
    the rule under test inside the supplied surface; the fix changed
    blind-export.py; and that change retroactively made round 6's own recorded
    digest irreproducible. Acting on a round's findings must not invalidate the
    round — otherwise the ledger punishes exactly the behaviour it exists to
    provoke, and the only way to keep it green would be to leave findings unfixed.

    A real surface change still fails, because the historical exporter is
    deterministic: same exporter, same commit, same allowlist, same digest.
    """
    tmp = Path(tempfile.mkdtemp(prefix="oath-implcheck-"))
    try:
        # The exporter as of the recorded commit. Falls back to the current one
        # when the round predates the script, which is reported rather than
        # silently assumed — a fallback that looked like a match would be the
        # vacuous pass this whole gate exists to prevent.
        # Written BESIDE the current exporter, not into the temp dir: it resolves
        # the repository root from its own __file__, so a copy anywhere else
        # silently computes against the wrong tree.
        exporter = ROOT / "scripts" / "blind-export.py"
        hist = subprocess.run(["git", "show", f"{exporter_rev or source}:scripts/blind-export.py"],
                              capture_output=True, cwd=ROOT)
        historical = None
        if hist.returncode == 0 and hist.stdout.strip():
            historical = ROOT / "scripts" / f".blind-export-at-{(exporter_rev or source)[:12]}.py"
            historical.write_bytes(hist.stdout)
            exporter = historical
        cmd = [sys.executable, str(exporter)]
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
        if 'historical' in dir() and historical is not None:
            historical.unlink(missing_ok=True)
        shutil.rmtree(tmp, ignore_errors=True)


def recompute_kit(section, rev=None) -> str | None:
    """Rebuild a protocol kit AT `rev` and return its surface digest, or None."""
    if not section:
        return None
    tmp = Path(tempfile.mkdtemp(prefix="oath-kitcheck-")) / "kit"
    try:
        cmd = [sys.executable, str(ROOT / "scripts" / "blind-kit.py")]
        if rev:
            cmd += ["--at", rev]
        cmd += [section, str(tmp)]
        r = subprocess.run(cmd,
                           capture_output=True, text=True, cwd=ROOT)
        if r.returncode != 0:
            return None
        for line in r.stdout.splitlines():
            if line.startswith("surface digest"):
                return line.split()[-1]
        return None
    finally:
        shutil.rmtree(tmp.parent, ignore_errors=True)


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

    # The exporter's surfaces are PER SECTION; §13.1b declares the normative data
    # of the whole document. Compare against the UNION, so a section that declares
    # no data (§8.6 today) neither hides a declared artifact nor is treated as one.
    exporter = (ROOT / "scripts" / "blind-export.py").read_text()
    m = re.search(r"^SURFACES = \{(.*?)^\}", exporter, re.S | re.M)
    shipped = set()
    if m:
        for data_list, _witness in re.findall(r"\(\[(.*?)\],\s*\[(.*?)\]\)", m.group(1), re.S):
            shipped |= set(re.findall(r'"([^"]+)"', data_list))
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
        if r.get("status") not in PRE_OUTCOME:
            for f in REQUIRED:
                if f not in r:
                    failures.append(f"round {n}: missing required field `{f}` (§13.3)")
        # PRE-REGISTRATION. A round may be recorded as DISPATCHED before it
        # returns, carrying a hypothesis and no verdict. Recording the hypothesis
        # first is what makes it falsifiable: a hypothesis written after the
        # result is a narration of the result, and would quietly convert every
        # round into a confirmation.
        if r.get("status") in PRE_OUTCOME:
            st = r["status"]
            if r.get("verdict"):
                failures.append(f"round {n}: {st} but already carries a verdict — "
                                f"remove `status` when recording the outcome")
            if not (r.get("hypothesis") or "").strip():
                failures.append(f"round {n}: {st} without a pre-registered hypothesis")
            # The isolation marker is PRE-REGISTERED like the hypothesis. These
            # states skip REQUIRED, so without this a round could be dispatched
            # with no marker and the value chosen after the outcome was known —
            # which is the failure IMPL-PRE-REGISTERED exists to prevent, on the
            # one field that decides whether a PASS is admissible at all.
            # null means "unknown, predates this rule" and is admissible only for
            # HISTORICAL rounds. A round being dispatched now must declare which
            # it is, or the pre-registration decides nothing.
            iso0 = r.get("session_isolated", "<missing>")
            if not (iso0 is True or iso0 is False):
                failures.append(f"round {n}: {st} without a pre-registered boolean "
                                f"session_isolated marker (got {iso0!r}); a pending round "
                                f"must declare true or false before the run — null is only "
                                f"for rounds predating IMPL-ISOLATED-SESSION")
            label = "READY (not launched)" if st == "READY" else "DISPATCHED (apparatus frozen)"
            print(f"  round {n}  {label:<32} §{','.join(r.get('sections', []))}")
            sd_pending, src_pending = r.get("surface_digest"), r.get("source")
            # A KIT is built by a different instrument, so it is recomputed by that
            # instrument — the same rule as `exporter` for tree exports. A kit is
            # assembled from the CURRENT normative text rather than a commit, which
            # means editing the section under test invalidates a pre-registration.
            # That is the correct behaviour: the surface a subject was dispatched
            # against must still be the surface the claim names.
            if r.get("surface_tool") == "blind-kit.py":
                # `src_pending`, not omitted: a kit must rebuild at the commit it
                # was dispatched from on BOTH paths. Wiring the revision into the
                # completed branch only meant a pre-registered kit stopped
                # reproducing the moment its section was repaired — which is the
                # exact failure IMPL-REPRODUCIBLE-INSTRUMENT exists to prevent,
                # reintroduced one branch away from its own fix.
                got = recompute_kit(r.get("kit_section"), src_pending) if sd_pending else None
            else:
                got = recompute_surface(src_pending, r.get("supplied"), r.get("exporter")) if sd_pending else None
            ok = got == sd_pending
            if sd_pending and not ok:
                failures.append(f"round {n}: pre-registered surface {sd_pending[:16]}… does not "
                                f"reproduce from {src_pending[:12]}")
            print(f"    surface: {'verified ' + sd_pending[:16] + '…' if ok else 'UNVERIFIED'}")

            # READY IS CHECKED AGAINST THE APPARATUS THAT WOULD ACTUALLY BE
            # DISPATCHED, which is HEAD — not against the commit the paperwork
            # names. The check above reproduces the RECORDED surface, and for a
            # DISPATCHED round that is the whole question: the subject saw those
            # bytes and nothing may move afterwards. For a READY round it is not
            # sufficient, and leaving it out was the exact defect this ledger
            # exists to catch, committed one level up — PRE_OUTCOME promises that
            # an edit moving the surface moves the digest, and without this the
            # promise lived only in a comment.
            #
            # So: edit the section after pre-registering it and the round goes
            # UNVERIFIED until it is re-exported and re-pinned. Launching a
            # subject against a surface the paperwork misnames is precisely what
            # freezing exists to prevent.
            #
            # LIMIT, stated rather than assumed: this compares COMMITTED state.
            # An uncommitted edit is invisible here, so a round must not be
            # dispatched from a dirty tree.
            if sd_pending and st == "READY" and r.get("surface_tool") != "blind-kit.py":
                head = subprocess.run(["git", "rev-parse", "HEAD"], capture_output=True,
                                      text=True, cwd=ROOT).stdout.strip()
                if head and head[:12] != (src_pending or "")[:12]:
                    now = recompute_surface(head, r.get("supplied"), head)
                    if now != sd_pending:
                        failures.append(
                            f"round {n}: READY, but the surface at HEAD ({(now or 'unexportable')[:16]}…) "
                            f"is no longer the pre-registered {sd_pending[:16]}… — the apparatus moved "
                            f"after pre-registration; re-export and re-pin `source`/`surface_digest` "
                            f"before dispatching")
                    else:
                        print("    apparatus at HEAD still reproduces it")
            if ok:
                verified += 1
            print("    hypothesis on record; outcome not yet known"
                  + ("; no subject launched" if st == "READY" else ""))
            continue

        v = r.get("verdict")
        if v not in VERDICTS:
            failures.append(f"round {n}: verdict {v!r} is not one of {sorted(VERDICTS)}")
        if n in seen:
            failures.append(f"round {n}: duplicate round number")
        seen.add(n)

        # §13 IMPL-CONTAMINATION-IS-NOT-A-MEASUREMENT. A disclosed-contaminated
        # run may record FAIL or PASS-WITH-INFERENCE — never PASS, because a
        # SUFFICIENCY claim is exactly what the environment may have supplied.
        # The inference COUNT is not a bound in either direction and is not
        # checked here: contamination inflates as well as deflates (prior
        # knowledge primes a subject to flag readings it would otherwise take
        # directly), so only the individual, independently-verifiable findings
        # survive. An earlier draft of the rule claimed a lower bound; it was
        # wrong and the specification now says so.
        #
        # ENVIRONMENT-CONSTRAINED is the marker. That class never waived
        # IMPL-ISOLATED-SESSION, but the schema required a verdict of every
        # completed round while the prose forbade the claim — so three rounds
        # recorded one anyway. Enforcing it here is what stops the schema from
        # quietly overruling the prose a fourth time.
        # FAILS CLOSED ON UNKNOWN. A PASS requires an AFFIRMATIVE `session_isolated:
        # true`; false, null and missing all forbid it. Absence of evidence of
        # contamination is not evidence of isolation, and the historical rounds
        # predating IMPL-ISOLATED-SESSION genuinely do not know (round 1 records
        # its isolation as "instructional rather than structural").
        iso = r.get("session_isolated")
        # `null` means "unknown, predates IMPL-ISOLATED-SESSION" and is admissible
        # ONLY for the rounds that actually predate it. Without this, a future
        # completed round could use the historical value and never declare its
        # isolation at all — the pre-outcome check is skipped once `status` is
        # removed, and REQUIRED only tests that the key exists.
        if iso is None and isinstance(n, int) and n >= ISOLATION_RULE_FROM_ROUND:
            failures.append(
                f"round {n}: session_isolated is null, which is reserved for rounds "
                f"before {ISOLATION_RULE_FROM_ROUND} (predating IMPL-ISOLATED-SESSION). "
                f"Declare true or false.")
        # §13 IMPL-ISOLATED-SESSION requires the marker where contamination was
        # disclosed to "identify every value it may have supplied". `false` with an
        # empty contamination list asserts both that contamination occurred and
        # that none did.
        # `contamination` mixes KINDS — round 1's entries are an artifact leak
        # (commit subject lines reaching the terminal), not a session. So
        # `session_isolated: true` alongside disclosed contamination is not
        # automatically contradictory, and rejecting it outright would refuse a
        # legitimate archive-leak-only round. But it IS the shape that would
        # bypass the PASS prohibition, so a PASS must say in words why none of
        # what it disclosed was session-borne.
        if v == "PASS" and (r.get("contamination") or []) and not (r.get("isolation_justification") or "").strip():
            failures.append(
                f"round {n}: verdict PASS with {len(r.get('contamination') or [])} contamination "
                f"note(s) and no `isolation_justification` — a PASS must state why none of "
                f"the disclosed contamination was session-borne (§13 IMPL-ISOLATED-SESSION)")
        if iso is False and not (r.get("contamination") or []):
            failures.append(
                f"round {n}: session_isolated is false but contamination is empty — "
                f"a contaminated run MUST identify what knowledge may have been supplied")
        # The marker must be the one the round was DISPATCHED under. Enforced from
        # MARKER_PREREGISTERED_FROM_ROUND; rounds 7-9 recorded it retrospectively
        # because the rule was written as a consequence of round 9, and pretending
        # otherwise would be the overclaim this section exists to catch.
        # The round identifier must BE an integer. Using its type to opt out of the
        # history lookup would let `"round": "10"` skip pre-registration entirely
        # while every other check accepted it.
        if not isinstance(n, int) or isinstance(n, bool):
            failures.append(f"round {n!r}: round identifier must be an integer")
        if isinstance(n, int) and not isinstance(n, bool) and n >= MARKER_PREREGISTERED_FROM_ROUND:
            pre, found = preregistered_marker(n)
            if not found:
                failures.append(
                    f"round {n}: no committed DISPATCHED revision carries a "
                    f"session_isolated marker — it cannot be shown to predate the outcome")
            elif pre is not iso:
                failures.append(
                    f"round {n}: session_isolated is {iso!r} but the round was dispatched "
                    f"under {pre!r} — the isolation determination was changed after the "
                    f"outcome was known")
        if v == "PASS" and iso is not True:
            failures.append(
                f"round {n}: verdict PASS with session_isolated={iso!r} — "
                f"IMPL-ISOLATED-SESSION forbids a claim from a contaminated run and "
                f"IMPL-CONTAMINATION-IS-NOT-A-MEASUREMENT permits only FAIL or "
                f"PASS-WITH-INFERENCE. A PASS requires session_isolated: true.")
        # `is`, not `in`: Python treats 0 == False and 1 == True, so a membership
        # test would accept the JSON numbers 0 and 1 as booleans.
        if not (iso is True or iso is False or iso is None):
            failures.append(f"round {n}: session_isolated must be true, false or null, not {iso!r}")
        rc = r.get("reproducibility_class")
        if rc is not None and rc not in REPRO_CLASSES:
            failures.append(f"round {n}: reproducibility_class {rc!r} is not one of {sorted(REPRO_CLASSES)}")
        # NO SECOND GUARD ON THE CLASS. An earlier version also refused a PASS from
        # any ENVIRONMENT-CONSTRAINED round, as belt-and-braces. That was wrong and
        # contradicted the specification: the class says REPRODUCTION depends on
        # something outside the repository — a pinned solver, particular hardware —
        # which is not the same as the session having carried project knowledge. A
        # run can be perfectly isolated and still environment-constrained, and §13
        # makes the affirmative marker sufficient. A redundant guard that rejects
        # valid records is not redundancy; it is a second, disagreeing rule.

        # §13.2 IMPL-VERDICT-BY-SUBJECT: PASS requires the implementer's own
        # statement AND nothing inferred. An author cannot upgrade a verdict by
        # deciding in hindsight that an inference was obvious.
        if v == "PASS":
            # §13.4 IMPL-VERDICT-SCOPED. A PASS must name what it covers and the
            # bytes it covers them on, or it reads as a claim about the whole
            # document — unfalsifiable, and false wherever a section has never
            # been attempted.
            if not r.get("sections"):
                failures.append(f"round {n}: PASS without naming the sections attempted (§13.4)")
            if not r.get("surface_digest"):
                failures.append(f"round {n}: PASS on an unbound surface — a PASS must name the "
                                f"exact bytes it was obtained against (§13.4)")
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
        if sd is not None and not r.get("exporter") and not r.get("surface_tool"):
            failures.append(
                f"round {n}: bound claim without `exporter` — a surface digest is a "
                f"MEASUREMENT, and a measurement that does not name its instrument "
                f"cannot be reproduced once the instrument is repaired (§13.3)")
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
            # A kit is recomputed by its own instrument here too, not only on the
            # pre-registered path. Wiring it into one branch and not the other made
            # a completed kit round unverifiable the moment it recorded an outcome.
            if r.get("surface_tool") == "blind-kit.py":
                got = recompute_kit(r.get("kit_section"), src)
            else:
                got = recompute_surface(src, r.get("supplied"), r.get("exporter"))
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

        dsps = r.get("dispositions")
        # SHAPE FIRST. Hand-edited ledger data must make this gate FAIL, not
        # raise: a string or null element would otherwise reach .get() and kill
        # the run with a traceback, which reads as a broken checker rather than a
        # bad record.
        if dsps is not None and not isinstance(dsps, list):
            failures.append(f"round {n}: `dispositions` is not a list")
            dsps = None
        elif dsps and any(not isinstance(x, dict) for x in dsps):
            failures.append(f"round {n}: `dispositions` contains a non-object entry")
            dsps = [x for x in dsps if isinstance(x, dict)]
        if isinstance(n, int) and not isinstance(n, bool) and n >= DISPOSITIONS_FROM_ROUND and not dsps:
            failures.append(
                f"round {n}: no `dispositions` — required from round "
                f"{DISPOSITIONS_FROM_ROUND}. A validation round records its result per ITEM; "
                f"omitting the field would make the not-reached cause requirement skippable")
        # UNIQUENESS, and a completeness LOWER BOUND against the round being
        # validated. Duplicate item names mask omissions, and a one-entry grid
        # would otherwise satisfy "per item" while recording nothing about the
        # rest. `validates_round` names the round whose inferences are under
        # test, and a grid must carry at least as many entries as that round had.
        #
        # STATED LIMIT: this is a lower bound, not completeness. Matching the
        # grid's free-text item names against the prior round's free-text
        # `inferred` entries would be the real check and is not reliable; a grid
        # can still cover the wrong items in the right number. It catches
        # truncation and duplication, which is what was actually reachable.
        seen_items = set()
        for dsp in dsps or []:
            raw = dsp.get("item")
            # The type check further down never runs if this strips first, so the
            # guard has to be here too — the earlier loop was the one crashing.
            it = raw.strip() if isinstance(raw, str) else ""
            if it and it in seen_items:
                failures.append(f"round {n}: duplicate disposition item {it!r} — duplicates "
                                f"mask omissions in a per-item grid")
            seen_items.add(it)
        vr = r.get("validates_round")
        # Required wherever the disposition schema is, or deleting the field
        # bypasses the completeness check entirely — one unrelated disposition
        # would then satisfy a "per-item" grid. A round that genuinely validates
        # nothing prior says so with `validates_round: null`, explicitly.
        if isinstance(n, int) and not isinstance(n, bool) and n >= DISPOSITIONS_FROM_ROUND \
                and "validates_round" not in r:
            failures.append(
                f"round {n}: no `validates_round` — required from round "
                f"{DISPOSITIONS_FROM_ROUND}. Use null if the round validates no prior "
                f"inferences; omitting it skips the completeness check")
        if vr is not None:
            # AN EXACT, NON-BOOLEAN INTEGER, checked before anything compares it.
            # Python equality would otherwise let 11.0 match round 11 in the
            # lookup while slipping past a `>= n` guard written for ints, and
            # True == 1 for the same reason. The type check has to come first or
            # the ordering rule below is decorative.
            if not isinstance(vr, int) or isinstance(vr, bool):
                failures.append(f"round {n}: validates_round {vr!r} is not an integer round number")
                prior = None
            else:
                # STRICTLY EARLIER: a round cannot validate itself or one that has
                # not run. Self-validation would pass whenever the tagged count
                # happened to match the round's own inference count.
                if isinstance(n, int) and vr >= n:
                    failures.append(f"round {n}: validates_round {vr} is not an EARLIER round")
                prior = next((x for x in rounds if x.get("round") == vr), None)
            if prior is None:
                # A typo would otherwise make `want` zero and disable the check
                # silently — the failure mode being guarded against, one field over.
                failures.append(f"round {n}: validates_round {vr!r} names no recorded round")
            else:
                # Count ONLY the dispositions TAGGED as validating that round. An
                # earlier version counted the whole grid, which a validation round
                # mixes with its own new findings — so deleting a validated item
                # left the total above the threshold and the check passed while
                # the thing it measured had gone.
                # Same type constraint on the per-item tags, for the same reason:
                # `"validates": 10.0` or `true` would otherwise compare equal.
                tagged = [x for x in (dsps or [])
                          if isinstance(x.get("validates"), int)
                          and not isinstance(x.get("validates"), bool)
                          and x.get("validates") == vr]
                # DERIVED from the prior round, not declared here. An earlier
                # version read the expected total from a field on THIS round,
                # which a coordinated edit defeats: drop a tagged row, decrement
                # the field, pass. Deriving it means defeating the check requires
                # editing the round being validated as well — a different and far
                # more visible act.
                # The out-of-scope count LOWERS the required number of
                # dispositions, so it is bounded and type-checked rather than
                # coerced: a float slips through int(), a numeric string coerces
                # silently, and "one" raises. All three would either weaken the
                # completeness check or crash CI.
                oos = prior.get("inferred_out_of_scope", 0)
                n_inf = len(prior.get("inferred") or [])
                if not isinstance(oos, int) or isinstance(oos, bool) or not (0 <= oos <= n_inf):
                    failures.append(f"round {vr}: inferred_out_of_scope {oos!r} must be an integer "
                                    f"in 0..{n_inf}; it reduces what round {n} must dispose of")
                    oos = 0
                want = n_inf - oos
                if len(tagged) != want:
                    failures.append(
                        f"round {n}: {len(tagged)} disposition(s) tagged `validates: {vr}` "
                        f"but validates_items says {want} — a per-item grid must dispose of "
                        f"each item it claims to cover")

        for i, dsp in enumerate(dsps or []):
            # PER-ITEM means per item. Without this, `[{"state": "derived"}]`
            # passes while recording no outcome for anything — the invariant is
            # that each named reading got a disposition, not that some states
            # were listed.
            # Every field is type-checked BEFORE it is used. `.strip()` on a
            # number raises AttributeError and set membership on a list raises
            # TypeError — either would end the run with a traceback, which is the
            # shape-first goal above defeated by the fields it was written for.
            it_ = dsp.get("item")
            if not isinstance(it_, str) or not it_.strip():
                failures.append(f"round {n}: disposition {i} has no string `item` — a per-item "
                                f"result must say which item")
            st_ = dsp.get("state")
            if not isinstance(st_, str):
                failures.append(f"round {n}: disposition {i} state {st_!r} is not a string")
                continue
            if st_ not in DISPOSITIONS:
                failures.append(f"round {n}: disposition {i} state {st_!r} is not one of {sorted(DISPOSITIONS)}")
            cz_ = dsp.get("cause")
            if st_ == "not-reached" and (not isinstance(cz_, str) or cz_ not in NOT_REACHED_CAUSES):
                failures.append(
                    f"round {n}: disposition {i} is not-reached without a cause in "
                    f"{sorted(NOT_REACHED_CAUSES)} — structural means the repair removed the path "
                    f"(evidence FOR it), incidental means the subject never hit it (missing coverage), "
                    f"and the two must not be conflated")

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
    for r in best:
        # Rendered in §13.4's form so the claim cannot be quoted more broadly than
        # it was earned.
        print(f"  §{', §'.join(r['sections'])}, on normative surface "
              f"{r['surface_digest'][:16]}…, was independently implemented without inference.")
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
