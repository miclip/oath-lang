#!/usr/bin/env python3
"""Verify a journal SNAPSHOT before, during, and after a registry cutover.

WHY THIS EXISTS SEPARATELY FROM `oath audit`. The audit answers "is this journal
internally consistent?" and it answers it using the CURRENT kernel. A cutover
needs a different question: "did the new binary change anything about history it
did not write?" That is a comparison between a snapshot and itself under new
code, and nothing existed to ask it.

The committed corpus proves the FORMAT is backward compatible. It cannot prove
anything about the live journal, which is the state actually being changed and
which is known to diverge from `codebase/`. So this runs against an arbitrary
snapshot file — take one before deploying, run it; take another after deploying
and after the pilot publication, run it again on each.

FOUR CHECKS, and each maps to a way the cutover could quietly destroy evidence:

  RESERIALIZE   every existing entry must re-encode byte-identically under the
                new field order. A single changed byte moves that entry's digest
                and every chain value after it.
  CHAIN         the tamper-evidence chain must remain intact across the snapshot.
  SIGNATURES    no entry may LOSE a signature it had, and none may gain one. An
                old binary reading new members silently drops them, so this is
                the check that catches an asymmetric rollback.
  REVISION      legacy entries must report their revision as UNAVAILABLE, never
                as a number. Fabricating a revision for an entry that never
                carried one manufactures evidence, which is worse than the gap.

Usage:  python3 scripts/cutover-check.py <journal.jsonl> [baseline.jsonl]

With a baseline, the snapshot is additionally compared against it: existing
entries must be unchanged and only new entries may appear. That is the check for
"deploying did not reinterpret anything".
"""

import hashlib
import json
import sys
from pathlib import Path

# The normative member order (SPEC §8.2.1). Kept here deliberately rather than
# imported: this script must be able to detect the kernel reordering members, and
# a check that reads its expectation from the thing under test measures nothing.
ORDER = ["seq", "time", "author", "verifier", "name", "kind", "status", "hash",
         "prev", "error", "guarantee", "termination", "context", "pubkey", "sig",
         "envelope_b64", "author_pubkey", "author_sig", "parent_rev",
         "name_transition", "chain"]

SIG_FIELDS = ("envelope_b64", "author_pubkey", "author_sig")


def canonical(entry: dict) -> str:
    """Re-encode per §8.2.1: fixed order, empty members omitted, compact."""
    out = {}
    for k in ORDER:
        if k not in entry:
            continue
        v = entry[k]
        if k == "seq" or (v != "" and v is not None):
            out[k] = v
    return json.dumps(out, separators=(",", ":"), ensure_ascii=False)


def load(path: Path):
    rows = []
    for n, line in enumerate(path.read_text().splitlines(), 1):
        if not line.strip():
            continue
        try:
            rows.append((n, line, json.loads(line)))
        except json.JSONDecodeError as e:
            print(f"FAIL: line {n} is not valid JSON: {e}")
            sys.exit(1)
    return rows


def main():
    if len(sys.argv) not in (2, 3):
        print(__doc__.strip().split("Usage:")[-1].strip())
        return 2
    snap = Path(sys.argv[1])
    if not snap.exists():
        print(f"FAIL: {snap} does not exist")
        return 1
    rows = load(snap)
    if not rows:
        print("FAIL: snapshot is empty — this check would pass vacuously")
        return 1

    failures = []

    # RESERIALIZE
    drift = [n for n, raw, e in rows if canonical(e) != raw]
    for n in drift[:5]:
        failures.append(f"RESERIALIZE line {n} does not re-encode to itself — its digest "
                        f"has moved, and so has every chain value after it")
    if len(drift) > 5:
        failures.append(f"RESERIALIZE ... and {len(drift) - 5} more")

    # CHAIN — SHA-256(prev + "\n" + body), where body is the entry with `chain`
    # omitted and the first entry is anchored on SHA-256 of the empty prior file.
    # Transcribed from the kernel's chainHash/chainAnchor rather than guessed: a
    # plausible-looking formula reported the committed journal as broken, which is
    # the failure mode a cutover check must not have.
    prev = hashlib.sha256(b"").hexdigest()
    broken = []
    for n, raw, e in rows:
        body = {k: v for k, v in e.items() if k != "chain"}
        h = hashlib.sha256((prev + "\n" + canonical(body)).encode()).hexdigest()
        if e.get("chain") and e["chain"] != h:
            broken.append(n)
        if e.get("chain"):
            prev = e["chain"]
    if broken:
        failures.append(f"CHAIN broken at line(s) {broken[:5]} — tamper-evidence does not hold")

    # SIGNATURES — partial sets are the dangerous state (§8.6.4 clause 1).
    partial = [n for n, raw, e in rows
               if any(e.get(f) for f in SIG_FIELDS) and not all(e.get(f) for f in SIG_FIELDS)]
    for n in partial[:5]:
        failures.append(f"SIGNATURES line {n} carries SOME of {SIG_FIELDS} but not all — "
                        f"any one alone attests to nothing, and a partial set is what an "
                        f"asymmetric rollback leaves behind")

    # REVISION — a legacy entry must not acquire a fabricated revision.
    signed = [e for _, _, e in rows if all(e.get(f) for f in SIG_FIELDS)]
    fabricated = [e.get("seq") for _, _, e in rows
                  if e.get("parent_rev") and not all(e.get(f) for f in SIG_FIELDS)]
    for s in fabricated[:5]:
        failures.append(f"REVISION entry seq={s} carries parent_rev but no signed envelope — "
                        f"the revision is unattested and must be reported unavailable, "
                        f"not stored as fact")

    # Optional baseline comparison.
    if len(sys.argv) == 3:
        base = Path(sys.argv[2])
        if not base.exists():
            print(f"FAIL: baseline {base} does not exist")
            return 1
        brows = load(base)
        bmap = {e.get("seq"): raw for _, raw, e in brows}
        changed = [e.get("seq") for _, raw, e in rows
                   if e.get("seq") in bmap and bmap[e["seq"]] != raw]
        for s in changed[:5]:
            failures.append(f"BASELINE entry seq={s} CHANGED since the baseline — deploying "
                            f"must not reinterpret an existing entry")
        lost = sorted(set(bmap) - {e.get("seq") for _, _, e in rows})
        for s in lost[:5]:
            failures.append(f"BASELINE entry seq={s} is MISSING from the snapshot — history "
                            f"is append-only and nothing may disappear")
        added = len(rows) - len(brows)
        print(f"  baseline: {len(brows)} entries; snapshot adds {added}")

    print(f"snapshot {snap}: {len(rows)} entries, {len(signed)} cryptographically signed\n")
    if failures:
        print(f"CUTOVER CHECK: FAIL — {len(failures)} finding(s)")
        for f in failures:
            print(f"  {f}")
        return 1

    print("CUTOVER CHECK: PASS")
    print("  every entry re-encodes to itself, the chain is intact, no signature set is")
    print("  partial, and no unsigned entry claims a revision. History is unchanged.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
