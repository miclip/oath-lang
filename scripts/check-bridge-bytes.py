#!/usr/bin/env python3
"""SPEC §7.4 is the AUTHORITY for bridge-obligation bytes; the kernel is derived.

WHY THIS GATE EXISTS. §7.4 pins the exact SMT text of the datatype<->Seq bridge
obligations so a second kernel can reproduce it without reading the first one's
source. That only works if the reference kernel is actually held to the document
— otherwise the SPEC drifts into being a description of `oath/bridge.go`, and a
blind implementation would be measured against prose nobody checks.

So this gate reads the scripts OUT OF THE SPEC, asks the kernel to emit its own,
and compares bytes. A mismatch means one of them is wrong; the gate does not
guess which.

WHAT IT DELIBERATELY DOES NOT DO. It does not run z3 and says nothing about
whether the obligations DISCHARGE. Bytes and verdicts are different claims — a
kernel can emit perfect bytes for an obligation that fails — and merging them
here would let a solver outage read as a byte divergence.
"""

import hashlib
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SPEC = ROOT / "docs" / "SPEC.md"

# The obligations, in §7.4.4's emission order. Each is (id, [block keys]) where a
# block key names a fenced block by the heading it lives under. Order matters:
# the script is the concatenation of its blocks in this order.
OBLIGATIONS = [
    ("measure-decreases", "measure"),
    ("roundtrip2-base", "base"),
    ("roundtrip2-step", "step"),
]


def fail(msg):
    print(f"check-bridge-bytes: {msg}")
    sys.exit(1)


def extract_blocks():
    """Pull the four normative fenced blocks out of §7.4.

    Anchored on the section headings rather than on block ORDER, so inserting
    prose between them cannot silently re-map which block is which — a failure
    that would still produce four blocks and compare the wrong ones.
    """
    text = SPEC.read_text()
    start = text.find("### 7.4 Bridge obligations")
    if start < 0:
        fail("VOID — §7.4 heading not found; this check did NOT run")
    end = text.find("\n## 8.", start)
    if end < 0:
        fail("VOID — could not find the end of §7; this check did NOT run")
    sec = text[start:end]

    def one(heading, key):
        i = sec.find(heading)
        if i < 0:
            fail(f"VOID — subsection {heading!r} not found; this check did NOT run")
        # the next fenced block after the heading, up to the following heading
        nxt = sec.find("\n#### ", i + 1)
        window = sec[i : nxt if nxt > 0 else len(sec)]
        blocks = re.findall(r"\n```\n(.*?)```", window, re.S)
        if len(blocks) != (2 if key == "scheme" else 1):
            fail(
                f"VOID — expected {'2' if key == 'scheme' else '1'} fenced block(s) under "
                f"{heading!r}, found {len(blocks)}; this check did NOT run"
            )
        return blocks

    core = one("#### 7.4.1 The core", "core")[0]
    base, step = one("#### 7.4.2 The `seq.len` induction scheme", "scheme")
    measure = one("#### 7.4.3 The measure obligation", "measure")[0]
    return {"core": core, "base": base, "step": step, "measure": measure}


def main():
    blocks = extract_blocks()
    core = blocks["core"]
    if "declare-datatypes" not in core or "fn_of_seq_Int" not in core:
        fail("VOID — the extracted core does not look like the core; this check did NOT run")

    # ALWAYS BUILD FROM SOURCE. An earlier draft preferred a prebuilt
    # `oath/oath` when one existed, which made the gate measure whatever binary
    # happened to be lying around — a stale one passes while the source has
    # diverged, and the gate reports agreement with §7.4 that the tree does not
    # have. Found by mutating the kernel and watching the gate stay green.
    # `go run` is a few seconds; measuring the wrong artifact is free and wrong.
    runner = ["go", "run", "."]
    cwd = ROOT / "oath"

    problems = []
    manifest_lines = ["# id\tsha256(script)"]
    for oid, key in OBLIGATIONS:
        want = core + blocks[key]
        try:
            # RAW BYTES, NOT text=True. Universal-newline translation would fold
            # a CRLF-emitting kernel to LF and let it pass a gate whose entire
            # claim is byte equality — the instrument silently repairing the
            # divergence it exists to find.
            got = subprocess.run(
                runner + ["bridge-obligation", "--emit", oid],
                cwd=cwd, capture_output=True, check=True,
            ).stdout
        except subprocess.CalledProcessError as e:
            fail(f"VOID — kernel could not emit {oid!r}: {e.stderr.decode(errors='replace').strip()}; "
                 f"this check did NOT run")
        if got != want.encode():
            problems.append(
                f"BYTES DIFFER for {oid}:\n"
                f"  spec  {len(want.encode())} bytes sha256={hashlib.sha256(want.encode()).hexdigest()}\n"
                f"  kernel{len(got):>6} bytes sha256={hashlib.sha256(got).hexdigest()}"
            )
        manifest_lines.append(f"{oid}\t{hashlib.sha256(want.encode()).hexdigest()}")

    # The manifest the kernel prints must agree with one derived from the SPEC.
    # Checked separately from the scripts: identical scripts with a mis-ordered
    # or mislabelled manifest is a real divergence and the per-script comparison
    # above cannot see it.
    try:
        got_manifest = subprocess.run(
            runner + ["bridge-obligation"], cwd=cwd, capture_output=True, check=True
        ).stdout
    except subprocess.CalledProcessError as e:
        fail(f"VOID — kernel could not emit the manifest: {e.stderr.decode(errors='replace').strip()}; "
             f"this check did NOT run")
    want_manifest = "\n".join(manifest_lines) + "\n"
    if got_manifest != want_manifest.encode():
        problems.append(
            "MANIFEST DIFFERS from the one derived from §7.4:\n"
            f"--- spec-derived ---\n{want_manifest}"
            f"--- kernel ---\n{got_manifest.decode(errors='replace')}"
        )

    if problems:
        print("check-bridge-bytes: the kernel and SPEC §7.4 disagree.\n")
        for p in problems:
            print(p)
        sys.exit(1)

    print(f"check-bridge-bytes: {len(OBLIGATIONS)} obligations byte-identical to SPEC §7.4, "
          f"manifest agrees")


if __name__ == "__main__":
    main()
