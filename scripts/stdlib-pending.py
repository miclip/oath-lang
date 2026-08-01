#!/usr/bin/env python3
"""What the manifest declares that the registry does not yet hold.

WHY THIS EXISTS RATHER THAN A GIT DIFF. Publication need was computed by
comparing HEAD~1 to HEAD, which asks "did this commit change the manifest" — a
question about GIT, when the one that matters is about the REGISTRY: is there
anything declared that is not published?

Those diverge constantly. A squash merge, a `workflow_dispatch`, or any run after
a FAILED publication all present a manifest identical to its parent while the
registry is still missing the entry. The first real publication attempt failed at
a credential guard, and every re-run afterwards reported "nothing to publish"
because the commit no longer differed — the work was pending and invisible.

Registry-relative is also idempotent: run it twice and the second run correctly
finds nothing, because the first one published. A diff-relative check has to be
right about history; this one only has to be right about now.

Usage: stdlib-pending.py <journal.jsonl>   → prints pending names, exits 0
       exit 1 if the journal cannot be read
"""

import base64
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def main():
    if len(sys.argv) != 2:
        print("usage: stdlib-pending.py <journal.jsonl>")
        return 1
    snap = Path(sys.argv[1])
    if not snap.exists():
        print(f"FAIL: {snap} does not exist")
        return 1
    man = json.loads((ROOT / "stdlib" / "oath-stdlib.json").read_text())
    ns = man["namespace"]
    # Only project-publication entries are ever published; referenced members are
    # selected and create no binding, so they can never be "pending".
    declared = [d["name"] for d in man["definitions"]
                if d.get("export") and d.get("membership") == "project-publication"]

    live = set()
    for line in snap.read_text().splitlines():
        if not line.strip():
            continue
        e = json.loads(line)
        if not e.get("envelope_b64") or e.get("status") != "accepted":
            continue
        try:
            oct_ = base64.b64decode(e["envelope_b64"], validate=True).decode()
        except Exception:
            continue
        if not oct_.startswith("oath-publish/"):
            continue
        kv = dict(l.split("=", 1) for l in oct_.rstrip("\n").split("\n")[1:] if "=" in l)
        live.add(kv.get("name", ""))

    pending = [n for n in declared if f"{ns}/{n}" not in live]
    for n in pending:
        print(n)
    print(f"# {len(pending)} pending of {len(declared)} declared", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
