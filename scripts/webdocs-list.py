#!/usr/bin/env python3
"""The docs the website publishes, read from website/lib/refdocs.ts.

ONE LIST, not two. A Makefile with its own copy of the filenames would drift from
the manifest the site renders, and the failure would be a doc that copies but is
never linked, or is linked but never copies — both of which look like the site is
fine until someone follows the link.

--check additionally verifies every listed file exists and that nothing in the
manifest points at a file docs/ does not have.
"""
import re, sys, pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
src = (ROOT / "website" / "lib" / "refdocs.ts").read_text()
files = re.findall(r'file:\s*"([^"]+)"', src)

if "--check" in sys.argv:
    missing = [f for f in files if not (ROOT / "docs" / f).exists()]
    if missing:
        print("ERROR: refdocs.ts lists files that do not exist in docs/:", ", ".join(missing))
        sys.exit(1)
    if len(files) != len(set(files)):
        print("ERROR: refdocs.ts lists the same file twice")
        sys.exit(1)
    sys.exit(0)

print("\n".join(files))
