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

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SPEC = ROOT / "docs" / "SPEC.md"

# The obligations, in §7.4.9's emission order. Each is (id, [block keys]) where a
# block key names a fenced block by the heading it lives under. Order matters:
# the script is the CONCATENATION of its blocks in this order, and the two
# families concatenate different things — a carrier is core+subgoal, a transport
# is transport-preamble + the function's declaration block + subgoal. Spelling
# each obligation as its own list is what keeps that difference DATA rather than
# a branch: adding a fifth bridged function is four more rows, not a new case.
OBLIGATIONS = [
    ("measure-decreases", ["core", "measure"]),
    ("roundtrip2-base", ["core", "rt2-base"]),
    ("roundtrip2-step", ["core", "rt2-step"]),
    ("transport-append-base", ["tcore", "append-decl", "append-base"]),
    ("transport-append-step", ["tcore", "append-decl", "append-step"]),
    ("transport-length-base", ["tcore", "length-decl", "length-base"]),
    ("transport-length-step", ["tcore", "length-decl", "length-step"]),
    ("transport-take-base", ["tcore", "take-decl", "take-base"]),
    ("transport-take-step", ["tcore", "take-decl", "take-step"]),
    ("transport-drop-base", ["tcore", "drop-decl", "drop-base"]),
    ("transport-drop-step", ["tcore", "drop-decl", "drop-step"]),
]

# Which §7.4 subsection supplies which blocks, and HOW MANY it must supply. The
# count is asserted rather than inferred: a subsection that grew or lost a fenced
# block would otherwise silently re-map which block is which, and the gate would
# compare real bytes against the wrong normative text and still print a verdict.
SECTIONS = [
    ("#### 7.4.1 The core", ["core"]),
    ("#### 7.4.2 The `seq.len` induction scheme", ["rt2-base", "rt2-step"]),
    ("#### 7.4.3 The measure obligation", ["measure"]),
    ("#### 7.4.4 The transport preamble", ["tcore"]),
    ("#### 7.4.5 `append`", ["append-decl", "append-base", "append-step"]),
    ("#### 7.4.6 `length`", ["length-decl", "length-base", "length-step"]),
    ("#### 7.4.7 `take`", ["take-decl", "take-base", "take-step"]),
    ("#### 7.4.8 `drop`", ["drop-decl", "drop-base", "drop-step"]),
]


def fail(msg):
    print(f"check-bridge-bytes: {msg}")
    sys.exit(1)


def extract_blocks():
    """Pull the normative fenced blocks out of §7.4.

    Anchored on the section headings rather than on block ORDER, so inserting
    prose between them cannot silently re-map which block is which — a failure
    that would still produce the right NUMBER of blocks and compare the wrong
    ones.
    """
    text = SPEC.read_text()
    start = text.find("### 7.4 Bridge obligations")
    if start < 0:
        fail("VOID — §7.4 heading not found; this check did NOT run")
    end = text.find("\n## 8.", start)
    if end < 0:
        fail("VOID — could not find the end of §7; this check did NOT run")
    sec = text[start:end]

    out = {}
    for heading, keys in SECTIONS:
        i = sec.find(heading)
        if i < 0:
            fail(f"VOID — subsection {heading!r} not found; this check did NOT run")
        # every fenced block after the heading, up to the following heading
        nxt = sec.find("\n#### ", i + 1)
        window = sec[i : nxt if nxt > 0 else len(sec)]
        blocks = re.findall(r"\n```\n(.*?)```", window, re.S)
        if len(blocks) != len(keys):
            fail(
                f"VOID — expected {len(keys)} fenced block(s) under {heading!r}, "
                f"found {len(blocks)}; this check did NOT run"
            )
        out.update(zip(keys, blocks))
    return out


def main():
    blocks = extract_blocks()
    # Shape checks on the two preambles, so a mis-anchored extraction that
    # happened to yield the right block COUNTS still cannot be interpreted.
    # `of-seq` distinguishes them: the carrier core declares it and the
    # transport preamble deliberately does not (§7.4.4), so asserting its
    # presence in one and absence in the other catches the two blocks being
    # swapped — which counting alone would wave through.
    if "declare-datatypes" not in blocks["core"] or "fn_of_seq_Int" not in blocks["core"]:
        fail("VOID — the extracted core does not look like the core; this check did NOT run")
    if "declare-datatypes" not in blocks["tcore"] or "fn_of_seq_Int" in blocks["tcore"]:
        fail("VOID — the extracted transport preamble does not look like the transport "
             "preamble; this check did NOT run")

    # WHICH KERNEL. Both are compared against the SPEC rather than against each
    # other, deliberately: Go==spec and Rust==spec together give Go==Rust, and
    # they also say WHICH side is wrong when they disagree. A direct kernel-to-
    # kernel diff cannot.
    #
    # PARSED STRICTLY, because this is a gate. A hand-rolled scan for --kernel
    # ignored everything else, so `--kernal rust` silently fell back to Go and
    # printed a green line while never checking Rust — a fail-open in the one
    # place a fail-open is least acceptable. argparse rejects unknown and
    # duplicate options for us.
    # argparse alone is not enough: a repeated store option keeps the LAST
    # value, so `--kernel rust --kernel go` would exit green having checked only
    # Go. Repeats are rejected outright rather than resolved.
    if sum(1 for a in sys.argv[1:] if a == "--kernel" or a.startswith("--kernel=")) > 1:
        fail("VOID — --kernel given more than once; refusing to guess which was meant")
    # allow_abbrev=False: otherwise `--kern rust --kernel go` slips past the
    # duplicate counter above (which only sees the full spelling), argparse takes
    # the last value, and the gate greens on Go while the caller asked for Rust.
    ap = argparse.ArgumentParser(allow_abbrev=False,
                                 description="compare a kernel's bridge obligations to SPEC §7.4")
    ap.add_argument("--kernel", choices=["go", "rust"], default="go")
    which = ap.parse_args().kernel

    if which == "go":
        # ALWAYS BUILD FROM SOURCE. An earlier draft preferred a prebuilt
        # `oath/oath` when one existed, which made the gate measure whatever
        # binary happened to be lying around — a stale one passes while the
        # source has diverged, and the gate reports agreement with §7.4 that the
        # tree does not have. Found by mutating the kernel and watching the gate
        # stay green. `go run` is a few seconds; measuring the wrong artifact is
        # free and wrong.
        runner = ["go", "run", "."]
        cwd = ROOT / "oath"
    elif which == "rust":
        # BUILD FIRST AND TAKE THE PATH FROM CARGO'S OWN OUTPUT.
        #
        # Two separate stale-artifact holes were closed here, both by review.
        # Running a pre-existing target/release/oathrs certified whatever binary
        # was lying around. Then reconstructing the path from `target_directory`
        # still broke under CARGO_BUILD_TARGET / `build.target`, where the
        # executable lands in target/<triple>/release — so the gate would either
        # VOID after a successful build or run an older native binary sitting at
        # the guessed path.
        #
        # The fix is to stop deriving the path at all. --message-format=json makes
        # cargo report the executable it actually produced, which is correct under
        # every target-dir and target-triple configuration by construction.
        build = subprocess.run(
            ["cargo", "build", "--release", "--message-format=json", "--bin", "oathrs"],
            cwd=ROOT / "oathrs", capture_output=True,
        )
        if build.returncode != 0:
            fail("VOID — `cargo build --release` failed, so no binary can be trusted; "
                 f"this check did NOT run:\n{build.stderr.decode(errors='replace').strip()}")
        binary = None
        for line in build.stdout.decode(errors="replace").splitlines():
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            if msg.get("reason") == "compiler-artifact" and msg.get("executable"):
                binary = Path(msg["executable"])
        if binary is None or not binary.exists():
            fail("VOID — cargo reported no executable for the `oathrs` bin; "
                 "this check did NOT run")
        runner = [str(binary)]
        cwd = ROOT
    else:
        fail(f"VOID — unknown --kernel {which!r} (want go|rust); this check did NOT run")

    problems = []
    manifest_lines = ["# id\tsha256(script)"]
    for oid, keys in OBLIGATIONS:
        want = "".join(blocks[k] for k in keys)
        try:
            # RAW BYTES, NOT text=True. Universal-newline translation would fold
            # a CRLF-emitting kernel to LF and let it pass a gate whose entire
            # claim is byte equality — the instrument silently repairing the
            # divergence it exists to find.
            got = subprocess.run(
                runner + ["bridge-obligation", "--emit", oid],
                cwd=cwd, capture_output=True, check=True,
            ).stdout
        except OSError as e:
            # e.g. a cross-compiled binary that cannot run on this host. VOID
            # rather than an uncaught traceback: the gate did not run, and
            # saying so is the difference between a known gap and a crash.
            fail(f"VOID — cannot execute the {which} kernel ({e}); this check did NOT run")
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
        print(f"check-bridge-bytes[{which}]: this kernel and SPEC §7.4 disagree.\n")
        for p in problems:
            print(p)
        sys.exit(1)

    print(f"check-bridge-bytes[{which}]: {len(OBLIGATIONS)} obligations byte-identical to "
          f"SPEC §7.4, manifest agrees")


if __name__ == "__main__":
    main()
