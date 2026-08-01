#!/usr/bin/env python3
"""Prove the reconciliation ratchet checks CLASSIFICATION, not today's count.

A reconciliation check that reproduces a total is worthless in the one case that
matters: a legitimate name vanishes while an unexplained name appears, the total
is unchanged, and both errors are invisible. This exercises exactly that, plus
the ordinary arrival case, against a fully SYNTHETIC tree — no credentials, no
network, no dependency on what the live registry happens to hold today.

The procedure, which is the point rather than an implementation detail:

  1. add one synthetic live name outside every declared category
  2. confirm the ratchet FAILS and identifies that exact name
  3. add it to an explicit allowed category
  4. confirm the ratchet PASSES
  5. remove the synthetic state and confirm the original baseline is restored

Run: python3 scripts/test-reconciliation-ratchet.py
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHECK = ROOT / "scripts" / "check-registry-reconciliation.py"

H = {n: f"{i:064x}" for i, n in enumerate(["alpha", "beta", "gamma"], start=1)}


def build(tree: Path):
    """A miniature of the real arrangement: corpus, alias mirror, publication, probe."""
    (tree / "corpus.json").write_text(json.dumps(H))
    (tree / "manifest.json").write_text(json.dumps({
        "namespace": "lib",
        "definitions": [{"name": "alpha", "membership": "project-publication", "export": True}],
    }))
    (tree / "policy.json").write_text(json.dumps({
        "policy_version": 1,
        "statement": "synthetic fixture",
        "corpus": "corpus.json",
        "registry": "synthetic",
        "stdlib_from_manifest": "manifest.json",
        "verdicts": {"corpus_present_identical": "expected", "alias": "expected",
                     "stdlib_publication": "expected", "operational_probe": "review",
                     "registry_only": "review", "corpus_absent_live": "fail",
                     "required_absent": "fail", "hash_mismatch": "fail",
                     "ambiguous": "fail", "undeclared": "fail"},
        "alias_prefixes": [{"prefix": "mirror/", "mirrors": "corpus", "reason": "synthetic mirror"}],
        "operational_probes": [{"name": "lib/probe", "reason": "synthetic probe"}],
        "registry_only": [],
    }))


def baseline():
    live = dict(H)
    live.update({f"mirror/{n}": h for n, h in H.items()})
    live["lib/alpha"] = H["alpha"]
    live["lib/probe"] = "f" * 64
    return live


def run(tree: Path, live: dict):
    (tree / "live.json").write_text(json.dumps(live))
    r = subprocess.run([sys.executable, str(CHECK), str(tree / "live.json"),
                        "--policy", str(tree / "policy.json")],
                       capture_output=True, text=True)
    return r.returncode, r.stdout + r.stderr


def declare(tree: Path, name: str, reason: str):
    p = tree / "policy.json"
    d = json.loads(p.read_text())
    d["registry_only"].append({"name": name, "reason": reason})
    p.write_text(json.dumps(d))


def main():
    failures = []

    def check(label, cond, detail=""):
        print(f"  {'ok  ' if cond else 'FAIL'}  {label}")
        if not cond:
            failures.append(f"{label}\n{detail}")

    with tempfile.TemporaryDirectory() as td:
        tree = Path(td)
        build(tree)

        print("\nBASELINE")
        rc, out = run(tree, baseline())
        check("a clean synthetic tree reconciles", rc == 0, out)
        n_baseline = len(baseline())

        print("\nSTEP 1-2  an arrival outside every category must FAIL and be NAMED")
        live = baseline()
        live["rogue/thing"] = "a" * 64
        rc, out = run(tree, live)
        check("exits nonzero", rc == 1, out)
        check("names the exact offending name", "rogue/thing" in out, out)
        check("says no category matched", "UNDECLARED" in out, out)

        print("\nSTEP 1-2b THE CASE A COUNT CANNOT SEE:")
        print("          one arrival + one departure — total UNCHANGED")
        live = baseline()
        live["rogue/thing"] = "a" * 64
        del live["mirror/beta"]
        check("total is identical to baseline", len(live) == n_baseline,
              f"{len(live)} vs {n_baseline}")
        rc, out = run(tree, live)
        check("still exits nonzero", rc == 1, out)
        check("names the ARRIVAL", "rogue/thing" in out, out)
        check("names the DEPARTURE", "beta" in out and "mirror/" in out, out)

        print("\nSTEP 1-2c a vanished required member ALONE must fail")
        live = baseline()
        del live["lib/alpha"]
        rc, out = run(tree, live)
        check("exits nonzero", rc == 1, out)
        check("names the absent publication", "lib/alpha" in out, out)

        print("\nSTEP 1-2d a name claimed by TWO categories must fail")
        declare(tree, "lib/probe", "synthetic double-claim")
        rc, out = run(tree, baseline())
        check("exits nonzero", rc == 1, out)
        check("reports it as ambiguous", "AMBIGUOUS" in out, out)
        d = json.loads((tree / "policy.json").read_text())
        d["registry_only"] = [x for x in d["registry_only"] if x["name"] != "lib/probe"]
        (tree / "policy.json").write_text(json.dumps(d))

        print("\nSTEP 3-4  declaring it into a category must make it PASS")
        declare(tree, "rogue/thing", "synthetic — declared in step 3")
        live = baseline()
        live["rogue/thing"] = "a" * 64
        rc, out = run(tree, live)
        check("exits zero once classified", rc == 0, out)
        check("appears under REVIEW rather than silently", "rogue/thing" in out and "REVIEW" in out, out)

        print("\nSTEP 5    removing the synthetic state restores the baseline")
        d = json.loads((tree / "policy.json").read_text())
        d["registry_only"] = [x for x in d["registry_only"] if x["name"] != "rogue/thing"]
        (tree / "policy.json").write_text(json.dumps(d))
        rc, out = run(tree, baseline())
        check("baseline reconciles again", rc == 0, out)

    if failures:
        print(f"\n{len(failures)} assertion(s) failed:\n")
        for f in failures:
            print(f"---\n{f}")
        return 1
    print("\nThe ratchet checks classification, not count: an arrival that exactly "
          "cancels a departure still fails, and names both.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
