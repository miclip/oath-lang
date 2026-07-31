#!/usr/bin/env python3
"""Materialize an isolated dispatch root for a blind implementation.

WHY THIS EXISTS. Round one of the blind licence evaluation ran in a git worktree
with an instruction not to inspect repository history. Two commit SUBJECT LINES
still reached the agent's terminal through ordinary setup commands (`git
checkout`, `git log --oneline -1`). They almost certainly did not determine any
of that run's four findings, but the run cannot be described as perfectly blind,
and an experiment whose isolation rests on the subject's compliance is measuring
compliance as much as it is measuring the specification.

The fix is the same one the release-binary gate applies: make the capability
absent rather than forbidden, then VERIFY THE PRODUCED ENVIRONMENT rather than
trusting the dispatch instructions. A tree with no `.git` cannot leak history
through `git log`, branch names, reflogs, tags, commit subjects, or diffs, no
matter what the agent runs or how a setup command is worded.

ALLOWLIST, NOT DENYLIST. Files are selected by pathspec from the pinned commit;
anything not named is absent because it was never written, not because a
deletion step remembered it. A denylist fails open on every path added after it
was authored — which is exactly the failure mode this whole exercise keeps
finding elsewhere.

WHAT ISOLATION CANNOT DO. It removes this repository's history from the
environment. It cannot remove a public remote, a model's prior exposure to the
project, or knowledge carried in from another session. Those are bounded by
dispatching a fresh agent and by recording the residual risk honestly — not by
this script, and not by claiming a stronger result than the setup earns.

Usage:  python3 scripts/blind-export.py <sha> <dest-dir>
"""

import hashlib
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# The complete supplied surface. §12 is not self-contained: it cites §8.6.1 for
# the licence field and its character rules, §8.6.4 for historical verification,
# and §11 for campaign identity — so the normative document ships whole. Shipping
# an excerpt would mean hand-selecting which cross-references the subject may
# resolve, which is itself a form of coaching.
ALLOW = [
    "docs/SPEC.md",
    "fixtures/license/model.json",
    "fixtures/license/vectors.jsonl",
    "fixtures/MANIFEST.md",
    "scripts/license-rule-matrix.py",
]

# Paths whose PRESENCE invalidates the run. Checked against the produced tree, so
# this is a verification of the artifact rather than a description of intent.
FORBIDDEN_NAMES = {".git", ".github"}
FORBIDDEN_PREFIXES = ("oath/", "oathrs/", "codebase/", "docs/experiments/", "website/")
FORBIDDEN_FILES = {"DESIGN.md", "CLAUDE.md", "README.md", "oathrs/DIVERGENCES.md"}

# Strings that must not appear anywhere in the exported tree. The reference
# implementation's own identifiers are the tell: if any of these survive, some
# file is carrying kernel source or design history that the pathspec did not
# obviously include.
FORBIDDEN_CONTENT = [
    "func evaluationDigest",
    "licenseModel = map[string]grants",
    "Co-Authored-By",
]


def sh(*args, **kw):
    return subprocess.run(args, capture_output=True, text=True, cwd=ROOT, **kw)


def export(sha: str, dest: Path):
    dest.mkdir(parents=True, exist_ok=True)
    # `git archive` writes a tree with no .git and no history by construction.
    # Extracting a tar is preferable to copying the working tree: an untracked or
    # dirty file cannot ride along, so the export is exactly the pinned commit.
    ar = subprocess.run(["git", "archive", "--format=tar", sha, "--"] + ALLOW,
                        capture_output=True, cwd=ROOT)
    if ar.returncode != 0:
        print("FAIL: git archive:", ar.stderr.decode()[:400])
        sys.exit(1)
    tar = subprocess.run(["tar", "-x", "-C", str(dest)], input=ar.stdout, capture_output=True)
    if tar.returncode != 0:
        print("FAIL: tar extract:", tar.stderr.decode()[:400])
        sys.exit(1)


def digest(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def preflight(dest: Path, sha: str) -> list:
    """Verify the PRODUCED tree. Every finding here invalidates the dispatch."""
    bad = []
    files = sorted(p for p in dest.rglob("*") if p.is_file())
    rels = [p.relative_to(dest).as_posix() for p in files]

    for p in dest.rglob("*"):
        rel = p.relative_to(dest).as_posix()
        if p.name in FORBIDDEN_NAMES or any(part in FORBIDDEN_NAMES for part in p.parts):
            bad.append(f"history leak: {rel} is present")
        if rel in FORBIDDEN_FILES or rel.startswith(FORBIDDEN_PREFIXES):
            bad.append(f"forbidden path exported: {rel}")

    allowed = set(ALLOW) | {"MANIFEST.sha256", "BRIEF.md"}
    for rel in rels:
        if rel not in allowed:
            bad.append(f"unlisted file in dispatch root: {rel}")
    for want in ALLOW:
        if want not in rels:
            bad.append(f"allowlisted file missing from export: {want}")

    for p in files:
        if p.name in ("MANIFEST.sha256", "BRIEF.md"):
            continue
        try:
            text = p.read_text()
        except UnicodeDecodeError:
            continue
        for needle in FORBIDDEN_CONTENT:
            if needle in text:
                bad.append(f"reference-implementation content in {p.relative_to(dest)}: {needle!r}")

    # git must be unable to resolve a repository from inside the dispatch root.
    r = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                       capture_output=True, text=True, cwd=dest)
    if r.returncode == 0:
        top = r.stdout.strip()
        if not top.startswith("/private") and not Path(top).samefile(dest):
            bad.append(f"git resolves a repository from the dispatch root: {top}")
    return bad


def main():
    if len(sys.argv) != 3:
        print(__doc__.strip().split("Usage:")[-1].strip())
        return 2
    sha, dest = sys.argv[1], Path(sys.argv[2]).resolve()

    full = sh("git", "rev-parse", sha).stdout.strip()
    if not full:
        print(f"FAIL: {sha} is not a commit in this repository")
        return 1
    if dest.exists():
        shutil.rmtree(dest)

    export(full, dest)

    files = sorted(p for p in dest.rglob("*") if p.is_file())
    lines = [f"{digest(p)}  {p.relative_to(dest).as_posix()}" for p in files]
    (dest / "MANIFEST.sha256").write_text("\n".join(lines) + "\n")

    # The brief records the pinned SOURCE identity without exposing any commit
    # message. A SHA is a coordinate; a subject line is a summary of intent, and
    # only the second one can coach.
    (dest / "BRIEF.md").write_text(
        f"# Dispatch root\n\n"
        f"Pinned source: `{full}`\n\n"
        f"This directory is the COMPLETE set of inputs supplied for this task. It was\n"
        f"produced by allowlist export from a pinned commit and contains no repository\n"
        f"history: no `.git`, no branches, no tags, no commit messages, and no diffs.\n"
        f"There is no reference implementation here and none is reachable.\n\n"
        f"`MANIFEST.sha256` lists every supplied file with its SHA-256, so the inputs\n"
        f"you worked from can be verified after the fact.\n\n"
        f"Files:\n" + "".join(f"- `{p.relative_to(dest).as_posix()}`\n" for p in files
                              if p.name != "BRIEF.md") + "\n")

    bad = preflight(dest, full)
    print(f"exported {len(files)} file(s) from {full[:12]} to {dest}")
    for p in files:
        print(f"  {digest(p)[:12]}  {p.relative_to(dest).as_posix()}")
    print()
    if bad:
        print(f"PREFLIGHT: FAIL — {len(bad)} finding(s); this dispatch root is NOT isolated")
        for b in bad:
            print(f"  {b}")
        return 1
    print("PREFLIGHT: PASS")
    print("  no .git, no forbidden path, no reference-implementation content, and every")
    print("  file in the tree is one the allowlist names. Isolation is a property of the")
    print("  produced directory, not of the instructions given to the agent.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
