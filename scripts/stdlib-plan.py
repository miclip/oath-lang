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

    # PLAN ONLY WHAT IS PENDING, when a journal is supplied. Republishing an
    # already-live member is a valid recorded no-op — and it SIGNS and WRITES, so
    # every merge re-signed the entire library. The first automated publication
    # added five journal entries for one new definition, and the cost grows with
    # library size times merges.
    #
    # Without a journal the plan is the whole library, which is right for a first
    # publication and for anyone reasoning about the library as a whole.
    pending_names = None
    if "--pending-from" in sys.argv:
        snap = Path(sys.argv[sys.argv.index("--pending-from") + 1])
        live = set()
        import base64
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
        before = len(exports)
        pending_names = {d["name"] for d in exports if f"{ns}/{d['name']}" not in live}
        print(f"pending: {len(pending_names)} of {before} project-published members are not yet live")
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
    # ORDER.TXT IS EVERY MEMBER; PENDING.TXT IS WHAT MUST BE SIGNED.
    #
    # These are different questions and conflating them was a real defect. Local
    # elaboration resolves references against the LOCAL store, so a member whose
    # dependency is already live still needs that dependency PRESENT locally —
    # emitting only the pending set produced `unknown type "List"` for the first
    # member that had a dependency at all. (The first project publication was
    # chosen dependency-free precisely so a failure would be unambiguously in the
    # credential chain; that is why this surfaced only now.)
    #
    # So: put everything in dependency order, publish only what the registry lacks.
    (out / "order.txt").write_text("\n".join(order) + "\n")
    pend = [n for n in order if n in pending_names] if pending_names is not None else list(order)
    (out / "pending.txt").write_text("\n".join(pend) + "\n")
    print(f"plan: {len(order)} definition(s) in dependency order → {', '.join(order)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
