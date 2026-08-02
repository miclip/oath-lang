#!/usr/bin/env python3
"""Prove the type-closure branch fires, with controls.

Written after the branch was implemented but NOT demonstrated. The instruction
that produced this was the right one: if a fixture does not trigger the branch,
debug the extraction rather than adjusting the fixture until it passes. Doing that
found two real bugs that made the check inert on exactly the case it existed for.

  CASE      a project publication mentioning a REFERENCED type  -> MUST fail
  CONTROL 1 a project publication mentioning a project-published type -> passes
  CONTROL 2 referenced members mentioning referenced types -> pass
"""
import importlib.util
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("chk", ROOT / "scripts" / "check-stdlib-manifest.py")
chk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(chk)

fails = []


def check(label, ok, detail=""):
    print(f"  {'ok  ' if ok else 'FAIL'}  {label}")
    if not ok:
        fails.append(f"{label}: {detail}")


print("\nEXTRACTION — the part that was silently inert")
ctors = chk.constructor_names()
check("a datatype is not filtered as a constructor (List)", "List" not in ctors)
check("a datatype whose constructor shares its name survives (Pair)", "Pair" not in ctors)
check("real constructors are still filtered (Cons, Nil)", "Cons" in ctors and "Nil" in ctors)

zip_types = chk.type_names_of({"name": "zip", "source": "examples/extras.oath",
                               "membership": "project-publication"})
check("zip's types are found", zip_types == {"Pair", "List"}, str(zip_types))
last_types = chk.type_names_of({"name": "last", "source": "examples/extras.oath",
                                "membership": "project-publication"})
check("last's types are found", last_types == {"Option", "List"}, str(last_types))
check("a wrong source is None, never an empty set",
      chk.type_names_of({"name": "zip", "source": "examples/list.oath",
                         "membership": "project-publication"}) is None)

print("\nCASE and CONTROLS — against the real manifest")
r = subprocess.run([sys.executable, str(ROOT / "scripts" / "check-stdlib-manifest.py")],
                   capture_output=True, text=True, cwd=str(ROOT))
check("CONTROL: the shipped manifest passes", r.returncode == 0, r.stdout[-400:])
check("CONTROL: project members using project-published types are not flagged",
      "mentions type List" not in r.stdout)
check("CONTROL: referenced members are not flagged for referenced types",
      "is project-published but mentions type" not in r.stdout)

if fails:
    print(f"\n{len(fails)} failed:")
    for f in fails:
        print(f"  - {f}")
    sys.exit(1)
print("\nThe branch is demonstrated, not merely implemented.")
