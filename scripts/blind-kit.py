#!/usr/bin/env python3
"""Export a PROTOCOL KIT for a scoped blind round — not a source checkout.

WHY THIS EXISTS ALONGSIDE blind-export.py. That script exports a repository
subtree at a commit, which is right when the task is "implement the system".
It is wrong when the task is "implement ONE SECTION", because the smallest
useful subtree still carries the reference implementation, its tests, its
comments, and a conformance harness that restates the rule. Round 6 found
exactly that: oathrs/conformance.sh contained an awk implementation of the rule
under test, inside the surface supplied to the subject.

A kit is assembled from a NORMATIVE SURFACE, not filtered down from a tree:

  the section under test, plus the sections it normatively references
  the vectors that witness it
  a harness contract stating inputs and outputs
  nothing else

THE PREFLIGHT INSPECTS THE PRODUCED TREE, NEVER THE INCLUDE RULES. A check that
verified the rules would be verifying an intention; the archive is what the
subject actually reads. This is the same discipline blind-export.py adopted after
its own preflight was found to be checking the allowlist rather than the export.

Usage: blind-kit.py [--at <commit>] <section> <outdir>
       e.g. blind-kit.py 8.7 /tmp/kit
            blind-kit.py --at fe58b19 8.7 /tmp/kit   (reproduce a past round)
"""

import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Sections a kit ships, keyed by the section under test. Explicit rather than
# derived from cross-references in the prose: "§8.7 mentions §8.6" is a weaker
# question than "an implementer needs §8.6.1 to build this", and only a human can
# answer the second. A missing entry is a hard error, not a default.
KITS = {
    "8.7": {
        "sections": ["8.2.1", "8.6.1", "8.6.5", "8.7"],
        "fixtures": ["reserve/vectors.jsonl"],
        "task": "namespace reservation and prefix authority",
    },
}

# Paths that must never appear in a kit, matched against the produced tree.
FORBIDDEN_PARTS = ["oath/", "oathrs/", ".git", "DESIGN.md", "website/", "docs/experiments/",
                   "conformance.sh", "_test.go", "CLAUDE.md", "README.md"]

# Rule identifiers may appear ONLY in the spec excerpt and the vectors. Anywhere
# else in a kit they are a leak: a rule name in a comment or a filename tells the
# subject which distinctions matter without making the prose sufficient.
RULE_ID = re.compile(r"\b(RES|ENV|CONF|LICENSE|IMPL)-[A-Z0-9-]{3,}\b")

# Phrases that disclose the EXPECTED INTERPRETATION rather than the rule. These
# are the ones a reviewer reaches for when explaining why a rule is
# counter-intuitive — which is exactly the reasoning the round is meant to
# measure the subject reaching independently.
BANNED_PHRASES = [
    "denial vector", "deniable", "most-specific-wins", "most specific wins",
    "land grab", "squatting is permitted", "unreservable is not forbidden",
    "retention has no such vector", "the obvious rule", "implemented backwards",
    "instinct pulls", "counter-intuitive", "trap for",
]

# Signatures of executable reservation semantics. A kit may describe an
# interface; it may not ship an implementation of the thing under test.
CODE_SIGNS = [
    (re.compile(r"func\s+\w*[Rr]eserve"), "Go function implementing reservation"),
    (re.compile(r"fn\s+\w*reserve"), "Rust function implementing reservation"),
    (re.compile(r"def\s+\w*reserve"), "Python function implementing reservation"),
    (re.compile(r"HasPrefix|starts_with|startswith"), "prefix-matching implementation"),
]


def _at(path, rev):
    """File content at `rev`, or from the working tree when rev is None.

    A COMPLETED round's surface is a historical fact. A kit rebuilt from current
    text can never reproduce it once the section under test is repaired — and
    repairing it is the entire point of running the round. Pinning to a commit is
    the same fix the exporter needed: reproduce the bytes the experiment used, not
    the bytes current tooling would produce.
    """
    if rev is None:
        return (ROOT / path).read_bytes()
    r = subprocess.run(["git", "show", f"{rev}:{path}"], capture_output=True, cwd=ROOT)
    if r.returncode != 0:
        raise SystemExit(f"FAIL: cannot read {path} at {rev}")
    return r.stdout


def spec_sections(wanted, rev=None):
    """Extract the requested sections from docs/SPEC.md, in document order.

    A section runs from its heading to the next heading of the SAME OR SHALLOWER
    depth, so requesting 8.7 takes its subsections and stops at §9.
    """
    text = _at("docs/SPEC.md", rev).decode()
    lines = text.split("\n")
    heads = []
    for i, ln in enumerate(lines):
        m = re.match(r"^(#{2,5})\s+(?:§\s*)?(\d+(?:\.\d+[a-z]?)*)\s", ln)
        if m:
            heads.append((i, len(m.group(1)), m.group(2)))
    out = []
    for want in wanted:
        hit = next((h for h in heads if h[2] == want), None)
        if hit is None:
            raise SystemExit(f"FAIL: docs/SPEC.md has no section {want}")
        i, depth, _ = hit
        end = len(lines)
        for j, d, _ in heads:
            if j > i and d <= depth:
                end = j
                break
        out.append((want, "\n".join(lines[i:end]).rstrip()))
    return out


HARNESS = """# Harness contract

You are implementing ONE section of a specification. You have the normative text,
the vectors that witness it, and nothing else. There is no reference
implementation in this kit, by design.

## What you must produce, IN THIS ORDER

1. `DERIVATION.md` — the authority model as you derive it from the specification
   text ALONE. Write it BEFORE opening the vectors. State every question the text
   left you to answer, and how you answered it. Mark each as DERIVED (the text
   determines it) or INFERRED (you had to decide).

2. An implementation, against your derivation, of:
   - parsing and canonical re-encoding of the reservation envelope;
   - journal replay to current authority state;
   - the acceptance decision for a submitted reservation;
   - resolution of which authority governs a given name.

3. Only then, run the vectors and record what happened.

Producing the derivation first is the point of the exercise. If you open the
vectors first, the round measures whether you can pattern-match examples, which
is a different and much weaker question than whether the text is sufficient.

## Interfaces you may assume

You are NOT given a registry, a store, or a crypto library. Assume:

- `verify(pubkey: bytes32, message: bytes, signature: bytes64) -> bool`
  An Ed25519 verifier. Do not implement one; do not test it.

- A JOURNAL is an ordered, append-only sequence of ENTRIES. Each entry you need
  for this task exposes: a sequence number, a `kind` string, a `status` string,
  a `name` string, and an optional signed statement as
  `(envelope_octets: bytes, pubkey: bytes32, signature: bytes64)`.
  Entries with no signed statement exist and are ordinary history.

- A name is a UTF-8 string. Nothing in this task requires interpreting the bytes
  of a name beyond the separator the specification names.

## Reporting

Answer explicitly: could this section be implemented from the text alone? For
every INFERRED item, say what you assumed and what a different implementer might
reasonably have assumed instead. A disagreement you can name is a finding; a
disagreement you cannot is a defect the round failed to surface.
"""


def preflight(kit: Path, allowed_rule_files):
    """Inspect the PRODUCED tree. Returns a list of findings."""
    bad = []
    for p in sorted(kit.rglob("*")):
        if p.is_dir():
            continue
        rel = str(p.relative_to(kit))
        for part in FORBIDDEN_PARTS:
            if part in rel:
                bad.append(f"forbidden path present: {rel} (matched {part!r})")
        try:
            body = p.read_text()
        except (UnicodeDecodeError, OSError):
            continue
        low = body.lower()
        # THE TEXT UNDER TEST CANNOT CONTAMINATE ITSELF. Rule identifiers and
        # normative reasoning are exempted inside the excerpt and vectors, and
        # forbidden everywhere else.
        #
        # The first draft of this check flagged the specification, which was the
        # wrong reading of a correct signal. §8.7 deliberately carries its
        # retention-vs-denial argument IN the normative text, because a rule a
        # careful implementer will otherwise get backwards needs its reasoning to
        # be normative rather than commentary. A specification's job is to be
        # SUFFICIENT; stripping the reasoning to make the round harder would make
        # the round measure a document nobody would ship.
        #
        # Round 6's contamination finding was a SECOND, DUPLICATE statement of the
        # rule outside the text under test — a conformance script restating it.
        # That is what this catches.
        if rel not in allowed_rule_files:
            for m in set(RULE_ID.findall(body)):
                bad.append(f"{rel}: rule identifier {m}-… outside the spec excerpt and vectors")
            for ph in BANNED_PHRASES:
                if ph in low:
                    bad.append(f"{rel}: restates the rule under test outside the normative text: {ph!r}")
        if rel != "HARNESS.md":
            for rx, why in CODE_SIGNS:
                if rx.search(body):
                    bad.append(f"{rel}: executable reservation semantics present ({why})")
    if (kit / ".git").exists():
        bad.append("repository history (.git) present in the kit")
    return bad


def main():
    rev = None
    argv = sys.argv[1:]
    if "--at" in argv:
        i = argv.index("--at")
        rev = argv[i + 1]
        del argv[i:i + 2]
    if len(argv) != 2:
        print(__doc__.strip().rsplit("Usage:", 1)[-1].strip())
        return 2
    section, outdir = argv[0], Path(argv[1])
    kit_spec = KITS.get(section)
    if kit_spec is None:
        print(f"FAIL: no kit defined for section {section}; add one deliberately")
        return 1
    if outdir.exists() and any(outdir.iterdir()):
        print(f"FAIL: {outdir} exists and is not empty")
        return 1
    outdir.mkdir(parents=True, exist_ok=True)

    parts = ["# Normative surface (excerpt)\n",
             "Extracted from a specification. Sections not listed here are not part of\n"
             "this task and are not supplied.\n"]
    for name, body in spec_sections(kit_spec["sections"], rev):
        parts.append(f"\n<!-- section {name} -->\n{body}\n")
    (outdir / "SPEC-excerpt.md").write_text("\n".join(parts))

    fx = outdir / "fixtures"
    fx.mkdir()
    for rel in kit_spec["fixtures"]:
        dst = fx / Path(rel).name
        dst.write_bytes(_at(f"fixtures/{rel}", rev))

    (outdir / "HARNESS.md").write_text(HARNESS)

    allowed = {"SPEC-excerpt.md"} | {f"fixtures/{Path(r).name}" for r in kit_spec["fixtures"]}
    findings = preflight(outdir, allowed)

    files = sorted(p for p in outdir.rglob("*") if p.is_file())
    h = hashlib.sha256()
    for p in files:
        h.update(str(p.relative_to(outdir)).encode())
        h.update(b"\0")
        h.update(p.read_bytes())
        h.update(b"\0")
    digest = h.hexdigest()

    print(f"kit for §{section} — {kit_spec['task']}")
    for p in files:
        print(f"  {p.relative_to(outdir)}  ({p.stat().st_size} bytes)")
    print()
    if findings:
        print(f"PREFLIGHT: FAIL — {len(findings)} finding(s)")
        for f in findings:
            print(f"  {f}")
        print("\nThe kit was written but MUST NOT be dispatched.")
        return 1
    print("PREFLIGHT: PASS — no forbidden path, no rule identifier outside the")
    print("  excerpt and vectors, no disclosed interpretation, no reservation code.")
    print(f"surface digest {digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
