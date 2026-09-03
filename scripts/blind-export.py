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

Usage:  python3 scripts/blind-export.py [--section 8.6] [--paths a,b,c] <sha> <dest-dir>
"""

import hashlib
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# The supplied surface, in the three kinds §13.1a defines. Anything not in one of
# these lists is absent by construction.
#
# The normative document ships WHOLE. §12 is not self-contained — it cites §8.6.1
# for the licence field and its character rules, §8.6.4 for historical
# verification, §8.2.2 for publication identity and §11 for campaign identity —
# and shipping an excerpt would mean hand-selecting which cross-references the
# subject may resolve, which is itself a form of coaching.
PROSE = ["docs/SPEC.md"]

# Per-section surfaces. The methodology is only a methodology if it is not
# licensing-shaped, so the harness takes a SECTION rather than assuming one.
# Each entry is (normative data, conformance witnesses) for that section, and the
# data list must match what §13.1b declares — check-implementability enforces it.
# Per-section FORBIDDEN sets. Most blind tasks implement a section from scratch and
# must not see EITHER kernel. A task that REPAIRS oathrs against new normative text
# obviously needs oathrs/ — but must still not see oath/, which is where the
# reference implementation of the same rule lives. Inverting the exclusion rather
# than relaxing it keeps the N-version property intact.
FORBIDDEN_BY_SECTION = {
    "10.0a": ("oath/", "codebase/", "docs/experiments/", "website/"),
    # #68 §7.4: implement the bridge obligations in oathrs from the SPEC alone.
    # docs/experiments/ matters MORE here than in any previous round —
    # issue-68.md carries the milestone's design argument AND a probe record
    # containing the literal scripts with their hashes, so shipping it would
    # hand the subject the answer and the check together.
    "7.4": ("oath/", "codebase/", "docs/experiments/", "website/", "scripts/"),
    # §7.2 (deterministic instantiation): repair oathrs against the new normative
    # strategy. It needs oathrs/ and the conformance corpus, but MUST NOT see oath/
    # (the reference instantiation lives in oath/prove_instantiate.go), nor
    # docs/experiments/ (the algorithm writeups + instantiate-defs.py), nor
    # docs/deterministic-instantiation.md — the last is excluded automatically
    # because PROSE ships only docs/SPEC.md, so the design doc is never in ALLOW.
    "7.2": ("oath/", "codebase/", "docs/experiments/", "website/"),
}

SURFACES = {
    "12": (["fixtures/license/model.json"],
           ["fixtures/license/vectors.jsonl", "fixtures/MANIFEST.md"]),
    # §8.6 declares NO normative data. That is the honest current state, not an
    # omission in this table: if the section turns out to need an artifact it
    # never declared, that is a finding for the round rather than something to
    # quietly supply here.
    "8.6": ([], ["fixtures/envelope/vectors.jsonl", "fixtures/MANIFEST.md"]),
    # #103: repair oathrs against SPEC §10.0a. The Rust kernel and the fixture
    # corpus it must reproduce; NOT oath/, which implements the same rule.
    "10.0a": ([], ["oathrs/", "fixtures/canonical/", "fixtures/hashes.txt",
                   "examples/", "fixtures/MANIFEST.md"]),
    # §14 (#122): the handler protocol's Request model. NO normative data and NO
    # witnesses — deliberately, and this is the strongest form of the test rather
    # than a gap. The section's whole claim is that its PROSE determines one
    # canonical Request value from an HTTP request; a subject given a vector file
    # could reproduce the vector without ever deriving the rules, which is
    # exactly the corroboration §13 exists to withhold. The worked example in
    # §14.3 is inside the prose and ships with it.
    #
    # The subject's deliverable is an adapter, not a kernel: give it an HTTP
    # request and it must produce the header list, method and path §14 requires.
    # oath/ is forbidden by default here, which matters more than usual — the Go
    # adapter IS the reference implementation of this section.
    "14": ([], []),
    # #68 §7.4: bridge obligations in the Rust kernel. NO witnesses, and that is
    # the strong form rather than a gap. §7.4 prints the script text literally,
    # so the subject does not need a vector to BUILD from — what is being tested
    # is whether the prose determines the bytes EXACTLY (whitespace, the nullary
    # constructor's trailing space, obligation order, manifest layout). Handing
    # over a hash manifest would let it iterate against the answer instead of
    # deriving it, which is the corroboration §13 exists to withhold.
    # fixtures/campaign/ is NOT a §7.4 witness and leaks nothing about it. It is
    # here because the first §7.4 round could not run `cargo test` at all:
    # campaign.rs include_str!s that file, so its absence broke the existing
    # suite and the subject had to verify in a scratch copy. A blind subject that
    # cannot run the tests already in the tree is a weaker subject than one that
    # can — reported by the round itself.
    "7.4": ([], ["oathrs/", "fixtures/campaign/"]),
    # §7.2: the Rust kernel to repair and the full conformance corpus it must
    # reproduce. NO normative data. The witnesses are the fixtures the kernel
    # checks itself against and the corpus source conformance reads — none of
    # which carries the instantiation ALGORITHM: prove/scripts.txt and
    # prove/attempts.txt pin only the quantified direct/induction attempts (the
    # instantiated attempt is deliberately outside that surface, §7.2), and
    # prove/outcomes.json is a verdict target, not a derivation. The subject must
    # derive the schema from §7.2 prose; the fixtures only tell it WHICH verdicts
    # to reach, never HOW.
    "7.2": ([], ["oathrs/", "fixtures/", "examples/", "apps/"]),
}

# NORMATIVE DATA: incorporated by reference, schema and interpretation defined in
# the prose (§13.1b). Part of the specification, so consuming it is derivation.
DATA = SURFACES["12"][0]

# CONFORMANCE WITNESSES: for the subject to CHECK itself against, never to build
# from. Supplied because a run that cannot self-check produces a weaker report.
WITNESSES = SURFACES["12"][1]

# DELIBERATELY EXCLUDED, and this is a correction rather than an omission.
# scripts/rule-matrix.py is our COVERAGE MEASUREMENT TOOL — neither prose
# nor data, but a description of our own intent. Round three's subject disclosed
# that one of its EXPECTED labels nudged a pre-boundary assumption, so its
# presence made the surface easier than the specification it was standing in for.
# A rule taken from tooling is inferred, not derived (§13.1a).
EXCLUDED = ["scripts/rule-matrix.py"]

# Paths removed even though they sit inside an allowlisted tree.
EXCLUDE_WITHIN = ["oathrs/DIVERGENCES.md", "oathrs/target",
                  # A HARNESS that already implements the rule under test is a
                  # second statement of it inside the supplied surface. Round 6's
                  # subject read conformance.sh to find write sites and met an awk
                  # copy of §10.0a plus comments restating it — after writing its
                  # derivation, but it correctly flagged that its confidence was
                  # then partly corroborated rather than independent. The measured
                  # surface must not contain the answer.
                  "oathrs/conformance.sh"]

ALLOW = PROSE + DATA + WITNESSES

# Paths whose PRESENCE invalidates the run. Checked against the produced tree, so
# this is a verification of the artifact rather than a description of intent.
FORBIDDEN_NAMES = {".git", ".github"}
FORBIDDEN_PREFIXES = ("oath/", "oathrs/", "codebase/", "docs/experiments/", "website/")
FORBIDDEN_FILES = {"DESIGN.md", "CLAUDE.md", "README.md", "oathrs/DIVERGENCES.md"}

# Coaching material is forbidden by BASENAME, at any depth, because the claim is
# about the KIND of file rather than about one path. `FORBIDDEN_FILES` above
# holds repo-relative paths, so a nested `oathrs/CLAUDE.md` matched none of them
# — and the §10.0a surface deliberately lifts the `oathrs/` prefix ban in order
# to ship the Rust, so such a file WOULD have been exported. A session-guidance
# file is the purest form of the thing this export exists to withhold: it states
# the reference implementation's conclusions in prose, which is exactly what a
# blind subject is supposed to derive from the specification instead.
FORBIDDEN_BASENAMES = {"CLAUDE.md", "AGENTS.md", "DESIGN.md"}

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
    # Exclude coaching material that lives INSIDE an allowlisted tree.
    # oathrs/DIVERGENCES.md is the record of every ambiguity previous blind rounds
    # found — design history, and precisely what a fresh subject must not read.
    # git pathspec magic does the removal at archive time, so it is never written.
    excludes = [f":(exclude){e}" for e in EXCLUDE_WITHIN]
    ar = subprocess.run(["git", "archive", "--format=tar", sha, "--"] + ALLOW + excludes,
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
        if p.is_file() and p.name in FORBIDDEN_BASENAMES:
            bad.append(f"coaching material exported: {rel} — session guidance "
                       f"states the reference implementation's conclusions, which "
                       f"a blind subject must derive from the specification")

    for rel in rels:
        if rel in EXCLUDED and rel not in ALLOW:
            bad.append(f"measurement tooling in dispatch surface: {rel} — neither "
                       f"normative prose nor normative data (§13.1a)")

    allowed = set(ALLOW) | {"MANIFEST.sha256", "BRIEF.md"}
    trees = tuple(a for a in ALLOW if a.endswith("/"))
    for rel in rels:
        if rel.startswith(trees):
            continue
        if rel not in allowed:
            bad.append(f"unlisted file in dispatch root: {rel}")
    for want in ALLOW:
        if want.endswith("/"):
            if not any(r.startswith(want) for r in rels):
                bad.append(f"allowlisted tree exported empty: {want}")
            continue
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
    # An optional explicit path list REPRODUCES a historical surface. A claim binds
    # the bytes that were supplied, and the exporter's allowlist evolves — so
    # re-exporting an old commit with today's list produces a different surface
    # than the experiment actually used, and the recomputation would be checking
    # the wrong thing while looking rigorous.
    paths = None
    argv = sys.argv[1:]
    global DATA, WITNESSES
    if len(argv) >= 3 and argv[0] == "--section":
        sec = argv[1]
        if sec not in SURFACES:
            print(f"FAIL: no declared surface for §{sec}")
            return 1
        DATA, WITNESSES = SURFACES[sec]
        global FORBIDDEN_PREFIXES
        if sec in FORBIDDEN_BY_SECTION:
            FORBIDDEN_PREFIXES = FORBIDDEN_BY_SECTION[sec]
        argv = argv[2:]
    if len(argv) >= 3 and argv[0] == "--paths":
        paths = [p for p in argv[1].split(",") if p]
        argv = argv[2:]
    if len(argv) != 2:
        print(__doc__.strip().split("Usage:")[-1].strip())
        return 2
    sha, dest = argv[0], Path(argv[1]).resolve()
    global ALLOW
    if paths is not None:
        ALLOW = paths
    else:
        ALLOW = PROSE + DATA + WITNESSES

    full = sh("git", "rev-parse", sha).stdout.strip()
    if not full:
        print(f"FAIL: {sha} is not a commit in this repository")
        return 1
    if dest.exists():
        shutil.rmtree(dest)

    export(full, dest)

    files = sorted(p for p in dest.rglob("*") if p.is_file())
    lines = [f"{digest(p)}  {p.relative_to(dest).as_posix()}" for p in files]
    manifest = "\n".join(lines) + "\n"
    (dest / "MANIFEST.sha256").write_text(manifest)

    # The SURFACE DIGEST identifies the exact published surface a blind run was
    # given. An implementability claim (§13) is meaningless without it: "the
    # specification is implementable" is only ever a statement about a specific set
    # of supplied bytes, and quoting a commit SHA is not enough, since the same
    # commit can be exported with a different allowlist.
    surface = hashlib.sha256(manifest.encode()).hexdigest()

    # The brief records the pinned SOURCE identity without exposing any commit
    # message. A SHA is a coordinate; a subject line is a summary of intent, and
    # only the second one can coach.
    (dest / "BRIEF.md").write_text(
        f"# Dispatch root\n\n"
        f"Pinned source: `{full}`\n"
        f"Surface digest: `{surface}`\n\n"
        f"This directory is the COMPLETE set of inputs supplied for this task. It was\n"
        f"produced by allowlist export from a pinned commit and contains no repository\n"
        f"history: no `.git`, no branches, no tags, no commit messages, and no diffs.\n"
        f"There is no reference implementation here and none is reachable.\n\n"
        f"`MANIFEST.sha256` lists every supplied file with its SHA-256, so the inputs\n"
        f"you worked from can be verified after the fact.\n\n"
        f"## What each supplied file IS\n\n"
        f"This distinction is load-bearing. Consuming normative data is DERIVATION;\n"
        f"reading a rule out of a witness is INFERENCE.\n\n"
        f"NORMATIVE PROSE — the specification itself:\n"
        + "".join(f"- `{f}`\n" for f in PROSE) +
        f"\nNORMATIVE DATA — incorporated by reference; schema and interpretation are\n"
        f"defined in the prose, so you may consume these as specification:\n"
        + "".join(f"- `{f}`\n" for f in DATA) +
        f"\nCONFORMANCE WITNESSES — check yourself against these, but do NOT read rules\n"
        f"out of them. A rule obtained from a fixture is inferred, not derived:\n"
        + "".join(f"- `{f}`\n" for f in WITNESSES) + "\n")

    # REPRODUCTION IS NOT RE-VALIDATION. `--paths` replays a surface recorded in
    # the ledger to check that its digest still derives from its source commit.
    # Isolation was a property of the ORIGINAL dispatch and was verified then;
    # re-running preflight here asks a different question and answers it wrongly,
    # because the forbidden set is section-specific and has since changed. A
    # historical surface would then read as unverifiable purely because today's
    # rules differ from the ones it was exported under.
    if paths is not None:
        print(f"exported {len(files)} file(s) from {full[:12]} to {dest}")
        print(f"  surface digest {surface}")
        print("  (reproduction mode: isolation was verified at dispatch, not re-checked)")
        return 0

    bad = preflight(dest, full)
    print(f"exported {len(files)} file(s) from {full[:12]} to {dest}")
    print(f"  surface digest {surface}")
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
