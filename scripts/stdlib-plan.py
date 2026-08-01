#!/usr/bin/env python3
"""Emit one single-definition source file per manifest export, in dependency order.

Publication signs ONE definition per envelope — a single signature must not cover
several independent name transitions — and it resolves references against the
local store, so a dependent cannot be published before what it depends on exists
there. Both constraints are structural, so the plan is generated rather than
maintained by hand.

The rename is TOKEN-ANCHORED. A paren-anchored pattern was used for an earlier
namespace campaign and failed in both directions: it over-matched (`parse-nat`
also matched `parse-nat-go`) and under-matched (a nullary constant is referenced
as a bare token, so eight definitions published as artifacts that were valid and
not the ones intended). Nothing failed at the time; the defect was visible only by
comparing hashes afterwards.
"""

import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def main():
    out = Path(sys.argv[sys.argv.index("--out") + 1]) if "--out" in sys.argv else Path("/tmp/plan")
    out.mkdir(parents=True, exist_ok=True)
    man = json.loads((ROOT / "stdlib" / "oath-stdlib.json").read_text())
    ns = man["namespace"]
    # ONLY project-publication entries are planned. A `referenced` member is
    # selected, not republished: the project creates no oath/<name> binding and
    # makes no licence assertion, so there is nothing for a publisher to sign.
    # Planning one would republish what the project claimed only to recommend.
    exports = [d for d in man["definitions"]
               if d.get("export") and d.get("membership") == "project-publication"]
    want = {d["name"] for d in exports}

    # Split every source file into top-level forms and keep the ones we export.
    forms = {}
    for src in {d["source"] for d in exports if d.get("source")}:
        text = (ROOT / src).read_text()
        depth, cur = 0, []
        for ch in text:
            if ch == "(":
                depth += 1
            if depth > 0:
                cur.append(ch)
            if ch == ")":
                depth -= 1
                if depth == 0:
                    f = "".join(cur).strip()
                    cur = []
                    m = re.match(r"\((?:data|defn)\s+([\w/-]+)", f)
                    if m and m.group(1) in want:
                        forms[m.group(1)] = f
    missing = want - set(forms)
    if missing:
        print(f"FAIL: no source form found for {sorted(missing)}")
        return 1

    # Dependency order, from the committed store.
    deps = {}
    for n in want:
        r = subprocess.run([str(ROOT / "oath" / "oath"), "explain", n, "--json"],
                           capture_output=True, text=True, cwd=str(ROOT),
                           env={"OATH_STORE": str(ROOT / "codebase"), "PATH": "/usr/bin:/bin"})
        try:
            d = json.loads(r.stdout).get("dependencies") or []
        except json.JSONDecodeError:
            d = []
        deps[n] = [x.split()[0] if isinstance(x, str) else x.get("name", "") for x in d]
    order, seen = [], set()

    def visit(n):
        if n in seen:
            return
        seen.add(n)
        for d in deps.get(n, []):
            if d in want:
                visit(d)
        order.append(n)

    for n in sorted(want):
        visit(n)

    for n in order:
        body = forms[n]
        for other in sorted(want, key=len, reverse=True):
            body = re.sub(r"(?<![\w/-])" + re.escape(other) + r"(?![\w-])", f"{ns}/{other}", body)
        (out / f"{n}.oath").write_text(body + "\n")
    (out / "order.txt").write_text("\n".join(order) + "\n")
    print(f"plan: {len(order)} definition(s) in dependency order → {', '.join(order)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
