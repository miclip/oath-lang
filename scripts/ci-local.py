#!/usr/bin/env python3
"""Run the CI gates that are safe to run locally, DERIVED from the workflows.

WHY THIS EXISTS. There is no local command that runs what CI runs, so every
pre-commit sweep was assembled from memory. That set was enumerated wrongly four
times in one week and reddened `main` twice. Replacing memory with a grep then
failed three more times, each differently: it matched commands inside YAML
COMMENTS (recommending a store-mutating target); it dropped the conformance
MODE, turning a two-minute gate into a nine-hour one; and it saw only `make`
lines, silently omitting the Go and Rust steps. A regex over YAML is not a parse.

THREE DESIGN DECISIONS, each the repair of one of those:

  - **The universe is derived from TRIGGERS, not from a filename.** A workflow
    gates a change iff it runs on `pull_request`. Hardcoding `conformance.yml`
    missed `stdlib-pr.yml` entirely, whose checks run on any PR touching
    `stdlib/**` or `examples/**` — including two this repository had never run
    locally.
  - **The unit is a STEP, not a line.** A `run:` block is one thing CI executes;
    splitting it produced fragments of shell scripts that no classification could
    name. Steps are named by their authors, which makes the table below readable
    rather than a wall of shell.
  - **AN UNCLASSIFIED STEP IS A FAILURE, NOT A DEFAULT.** If CI grows a step this
    file has never seen, the run stops and names it. There is deliberately no
    `check-*` prefix rule: a prefix rule is a default, and `mutation boundary` is
    the standing proof that names do not classify — it sounds mutating and is a
    gate. Defaulting either way restores the original problem: run it and a
    mutating step could reach the committed store; skip it and the sweep quietly
    narrows again.

NOT A CI REPLACEMENT. Steps that mutate, that depend on a skipped step, or that
need a machine this is not, are reported as skipped WITH THEIR REASON — so
"green here" can never be read as "green in CI".
"""

from __future__ import annotations

import argparse
import hashlib
import re
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WORKFLOWS = ROOT / ".github/workflows"

# ---------------------------------------------------------------------------
# The hand-maintained half, and deliberately the ONLY hand-maintained half:
# whether a step is safe to run HERE is a fact about the step, and no parse
# decides it.
#
# **THE KEY INCLUDES A DIGEST OF THE COMMAND BODY, NOT JUST THE STEP NAME.** A
# name-only key classifies a LABEL, and a label is not what runs: change the
# `build` step's body to call `make verify` while leaving its name alone, and a
# name-keyed table would still call it safe and mutate the journal. Binding to
# the body means any edit to what a step DOES makes it unknown again, which is
# the same rule this repository applies everywhere else — derive from the thing,
# not from a name for the thing. Refresh a digest with `--digests` after
# reviewing the change; that review is the point, not an inconvenience.
# ---------------------------------------------------------------------------

RUN: set[tuple[str, str]] = {
    ("conformance", "build", "824452667570"),
    ("conformance", "unit tests", "230135e255a8"),
    ("conformance", "playground corpus mirrors the committed store (#145)", "b119401a74f0"),
    ("conformance", "mutation boundary (release binary contains no rule-disable path)", "a5ac2d616a34"),
    ("conformance", "documented numbers match the machine's ledger (#96)", "4d2da9c20b5b"),
    ("conformance", "normative prose describes the bytes the kernel emits", "6dfee53dcfa9"),
    ("conformance", "independent-implementability ledger (SPEC §13)", "1fc7c0ec8b93"),
    ("conformance", "no coaching leak into always-loaded context", "734a42986146"),
    ("conformance", "§14's transformation is a total single-valued mapping", "cf40474ee9a9"),
    ("conformance", "identity constants are findable in the spec (SPEC §13)", "75e7abdc3809"),
    ("conformance", "bridge-obligation bytes match SPEC §7.4", "79e07338aadd"),
    ("conformance", "corpus/registry reconciliation ratchet", "c2f7b02c7374"),
    ("conformance", "stdlib type-closure branch is demonstrated", "d8f5a310e9a3"),
    ("conformance", "plugin assets match plugin/ sources", "4e93a7261cb7"),
    ("conformance", "website docs match the repository markdown", "b1608a7818b3"),
    ("conformance", "website tutorials match the repository markdown", "0f7e439a3d44"),
    ("conformance", "playground refuses lossy source at the JS boundary (#133)", "7e555c3b2443"),
    ("conformance", "served playground wasm reproduces corpus identities (#145)", "63707a484b57"),
    ("conformance", "committed store is canonically encoded (#100)", "4e69158cbd54"),
    ("conformance", "website proof ledger matches canonical fixtures (#30)", "f3ad1f92e1db"),
    ("conformance", "cargo test (behaviour no fixture family covers)", "47f879532188"),
    ("conformance", "conformance (checks 1-4 + byte oracle for 5-6)", "6874a9437af4"),
    ("conformance", "bridge obligations match SPEC §7.4 (Rust)", "b66168b1f352"),
    ("stdlib-pr", "Build the kernel FROM THE PROPOSED COMMIT", "d1121e35fa7a"),
    ("stdlib-pr", "Repository gates", "72f2c8fded50"),
    ("stdlib-pr", "Manifest reproduces, and membership rules hold", "ea3a78a679e9"),
}

# MUTATES: reaches the committed store, the append-only journal, or a published
# artifact. These are deliberate acts and must never ride along in a sweep.
MUTATES: dict[tuple[str, str], str] = {
    ("conformance", "re-verify examples against committed store", "e89023893cf4"):
        "re-puts every example and APPENDS to the append-only journal, then asserts "
        "names.json is unmoved — the assertion is only meaningful after the re-put",
}

# NEEDS-CI: correct to run, wrong to run HERE. Separate from MUTATES because a
# reader deciding whether to trust a local pass needs the reason, not just the fact.
NEEDS_ENV: dict[tuple[str, str], str] = {
    ("conformance", "the webhook application still works end to end (#120)", "66f73ce195e0"):
        "fails on some dev machines and passes in CI; not diagnostic locally",
    ("conformance", "pin z3 4.16.0 (proof outcomes are solver-version-sensitive, SPEC §10.5)", "d88ebb19e797"):
        "provisions the runner's z3; the gate itself pins the version it used",
    ("conformance", "pin z3 4.16.0", "d88ebb19e797"):
        "provisions the runner's z3",
    ("conformance", "six-check conformance (cold prove at the SPEC budget)", "1dc431d452c0"):
        "the 9+ hour cold re-derivation; schedule/dispatch only",
    ("conformance", "build library + CLI for wasm (prove feature excluded)", "d0acc6880fc4"):
        "wasm32-wasip1 target may not be installed locally",
    ("conformance", "build cloud driver (compile guard; stays dependency-free)", "a8e480713521"):
        "cloud build tag; the integration job's setup",
    ("conformance", "cloud build stays compilable", "a8e480713521"):
        "cloud build tag; the integration job's setup",
    ("conformance", "Postgres + GCS integration tests", "3d70b5e119a4"):
        "needs Postgres and GCS services",
    ("stdlib-pr", "Compute the proposed registry delta", "41f545e50141"):
        "needs origin/<base_ref> and PR context",
    ("stdlib-pr", "Dry-run publication plan (unsigned)", "de22e02100ad"):
        "produces a PR artifact; needs PR context",
    ("conformance", "reference kernel (Go)", "278e1036cfec"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "uses actions/setup-go@v5", "540f9fb35644"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "uses actions/setup-node@v4", "c938b22b2c3e"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "independent kernel (Rust) vs fixtures (byte-oracle mode)", "278e1036cfec"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "uses dtolnay/rust-toolchain@stable", "44c01668b418"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "independent kernel (Rust) — full empirical re-derivation", "278e1036cfec"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "uses dtolnay/rust-toolchain@stable", "44c01668b418"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "wasm32 cross-compile", "278e1036cfec"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "uses dtolnay/rust-toolchain@stable", "44c01668b418"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "cloud backend (Postgres) — integration", "278e1036cfec"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("conformance", "uses actions/setup-go@v5", "540f9fb35644"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("stdlib-pr", "Check out the PROPOSED tree", "278e1036cfec"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("stdlib-pr", "uses actions/setup-go@v5", "540f9fb35644"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("stdlib-pr", "uses actions/upload-artifact@v4", "9c2992e44032"):
        "runner infrastructure (checkout/setup/upload) or a job container; not a gate",
    ("stdlib-pr", "Proposal summary", "e395a90f689a"):
        "writes a PR comment body; needs PR context",
}


def gate_workflows() -> list[Path]:
    """Workflows that GATE a change, derived from their triggers.

    `pull_request` is the signal. Deploy and release workflows are excluded
    because they are dispatch- and tag-triggered: they run AFTER a decision
    rather than deciding anything.
    """
    out = []
    files = sorted(list(WORKFLOWS.glob("*.yml")) + list(WORKFLOWS.glob("*.yaml")))
    for wf in files:
        head = wf.read_text().split("jobs:", 1)[0]
        # Both spellings GitHub accepts: a `pull_request:` key, and the inline
        # list form `on: [push, pull_request]`. Missing either would omit a
        # whole workflow silently — the drift this file exists to catch, at the
        # one moment it matters most, which is when CI grows.
        # Every form GitHub accepts, because omitting one omits a WHOLE gating
        # workflow silently — and this command's value is that nothing is missed:
        #   on:\n  pull_request:        block mapping
        #   on: [push, pull_request]    flow sequence
        #   on: pull_request            scalar
        #   on: {pull_request: {}}      flow mapping
        if re.search(r"^\s*pull_request\b", head, re.M) or \
           re.search(r"^\s*(on|\"on\"):\s*(\[[^\]]*\bpull_request\b|"
                     r"\{[^}]*\bpull_request\b|pull_request\s*$)", head, re.M):
            out.append(wf)
    return out


def steps(wf: Path) -> list[tuple[str, str]]:
    """(step name, shell block) for every `run:` step, in file order."""
    out: list[tuple[str, str]] = []
    lines = wf.read_text().splitlines()
    name: str | None = None
    # Execution metadata that changes WHAT a step does without touching its
    # `run:` text. `env:` is the sharp one — a step could gain
    # OATHRS_CONFORMANCE_PROVE=full and launch the 9-hour job while its body
    # digest, and therefore its classification, stayed put.
    meta: list[str] = []
    i = 0
    while i < len(lines):
        raw, st = lines[i], lines[i].strip()
        if re.match(r"-?\s*(env|shell|working-directory):", st):
            meta.append(st)
            # A block-form `env:` carries its variables BENEATH it; recording only
            # the header would leave a value change invisible to the digest.
            if st.rstrip().endswith(":"):
                base = len(raw) - len(raw.lstrip())
                j = i + 1
                while j < len(lines):
                    nxt = lines[j]
                    if nxt.strip() and (len(nxt) - len(nxt.lstrip())) <= base:
                        break
                    if nxt.strip():
                        meta.append(nxt.strip())
                    j += 1
        if st.startswith("#"):
            i += 1
            continue
        # A new list item starts a new step: metadata accumulated for the
        # previous one must not leak forward. Without this reset, a service
        # block's `env:` attached itself to the next unrelated run step.
        if st.startswith("- "):
            meta = []
        m = re.match(r"-?\s*name:\s*(.+)$", st)
        if m:
            name = m.group(1).strip().strip('"\'')
        # `uses:` steps are the OTHER half of the universe. They are runner
        # infrastructure today (checkout, setup, upload), but an action-based
        # lint or security gate would be a real gate — and parsing only `run:`
        # would leave it invisible, which is the same incompleteness that made an
        # earlier version see only `make` lines.
        um = re.match(r"-?\s*uses:\s*(\S+)", st)
        if um:
            out.append((name or f"uses {um.group(1)}", f"#uses# {um.group(1)}",
                        "\n".join(sorted(meta))))
            name, meta = None, []
            i += 1
            continue
        if re.match(r"-?\s*run:", st):
            indent = len(raw) - len(raw.lstrip())
            body: list[str] = []
            tail = st.split("run:", 1)[1].strip().lstrip("|>").strip()
            if tail:
                body.append(tail)
            i += 1
            while i < len(lines):
                nxt = lines[i]
                s2 = nxt.strip()
                if s2 and (len(nxt) - len(nxt.lstrip())) <= indent:
                    break
                body.append(nxt)
                i += 1
            # YAML strips a block scalar's common indentation; preserving it
            # would feed indented text to indentation-sensitive constructs (a
            # heredoc'd Python block is the live example) and change what runs.
            first, rest = (body[0], body[1:]) if body else ("", [])
            pad = min((len(l) - len(l.lstrip()) for l in rest if l.strip()), default=0)
            block = "\n".join([first] + [l[pad:] if l.strip() else "" for l in rest]).strip()
            out.append((name or "(unnamed)", block, "\n".join(sorted(meta))))
            name, meta = None, []
            continue
        i += 1
    return out


def digest(body: str) -> str:
    return hashlib.sha256(body.encode()).hexdigest()[:12]


def classify(key: tuple[str, str], body: str, has_meta: bool = False) -> tuple[str, str]:
    d = digest(body)
    if body.startswith("#uses#"):
        # STRUCTURALLY not runnable, whatever the table says. This script runs
        # shell, not actions, and the synthesized body is a bash COMMENT — so
        # putting a `uses:` step in RUN would exit 0 and report a gate green
        # without running it. The refusal message invites adding unknown steps to
        # RUN, so the invitation has to be impossible to follow here rather than
        # merely unwise.
        return "needs-ci", "an action step; this runner executes shell, not actions"
    named = {k[:2] for k in RUN} | {k[:2] for k in MUTATES} | {k[:2] for k in NEEDS_ENV}
    full = key + (d,)
    if full in MUTATES:
        return "mutates", MUTATES[full]
    if full in NEEDS_ENV:
        return "needs-ci", NEEDS_ENV[full]
    if full in RUN:
        if has_meta:
            # NARROWED DELIBERATELY, after review found three separate ways local
            # execution diverged from CI's (shell fail-fast, env, working
            # directory). Reproducing a GitHub Actions runner is an unbounded
            # surface and is not what this tool is for. A step carrying execution
            # metadata is therefore NOT runnable here, whatever its digest says —
            # the classification cannot promise semantics this script does not
            # implement.
            return "needs-ci", ("carries env/shell/working-directory; this runner does not "
                                "reproduce step execution context")
        return "run", ""
    if key in named:
        return "unknown", f"step body CHANGED (now {d}) — re-review, then update its digest"
    return "unknown", "not classified in scripts/ci-local.py"


# Deliberately a (workflow, step) pair with NO digest: it names a step to compare
# against, not a classification entry. An earlier bulk digest refresh rewrote it
# anyway, which is why the comparisons below slice [:2] — a constant that looks
# like a table row will be treated as one.
SOLVER_STEP = ("conformance", "conformance (checks 1-4 + byte oracle for 5-6)")

# Gates that import the playground's ESM modules. CI pins Node 22 in a
# `setup-node` step this sweep skips, so on an older Node these fail for an
# environmental reason CI never reproduces — the same shape as the solver
# dependency, and the same repair.
NODE_STEPS = {
    ("conformance", "playground corpus mirrors the committed store (#145)"),
    ("conformance", "playground refuses lossy source at the JS boundary (#133)"),
    ("conformance", "served playground wasm reproduces corpus identities (#145)"),
}


def node_ok() -> bool:
    try:
        out = subprocess.run(["node", "--version"], capture_output=True, text=True, timeout=10)
    except Exception:
        return False
    m = re.match(r"v(\d+)\.(\d+)", out.stdout.strip())
    return bool(m) and (int(m.group(1)), int(m.group(2))) >= (22, 7)


def pinned_solver_present() -> bool:
    """The conformance gate's outcome is a function of the SOLVER VERSION.

    CI provisions z3 4.16.0 in a step this sweep deliberately skips, so running
    the gate here against whatever z3 is on PATH would report a result CI never
    produces — the same defect as running the names.json assertion without the
    verify it asserts.
    """
    try:
        out = subprocess.run(["z3", "--version"], capture_output=True, text=True, timeout=10)
    except Exception:
        return False
    return "4.16.0" in (out.stdout + out.stderr)


def main() -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--list", action="store_true",
                    help="show the derived plan and run nothing")
    ap.add_argument("--digests", action="store_true",
                    help="print each step's current body digest, for updating the table")
    args = ap.parse_args()

    wfs = gate_workflows()
    if not wfs:
        print("VOID — no workflow triggers on pull_request; this check did NOT run",
              file=sys.stderr)
        return 2

    plan: list[tuple[tuple[str, str], str, str, str]] = []
    for wf in wfs:
        for name, body, meta in steps(wf):
            key = (wf.stem, name)
            # The digest covers the body AND its execution metadata: identity is
            # what the step DOES, and env/shell/working-directory change that
            # without touching a character of the command.
            kind, why = classify(key, body + ("\n#meta#\n" + meta if meta else ""),
                                 has_meta=bool(meta))
            plan.append((key, kind, why, body))

    if args.digests:
        for wf in wfs:
            for name, body, meta in steps(wf):
                d = digest(body + ("\n#meta#\n" + meta if meta else ""))
                print(f'    ("{wf.stem}", "{name}", "{d}"),')
        return 0

    if not pinned_solver_present():
        # Compare on (workflow, step): plan keys carry no digest, but writing the
        # comparison against a differently-shaped constant is how this silently
        # never fired in an earlier version — always false, always runnable.
        plan = [(k, ("needs-ci" if k[:2] == SOLVER_STEP[:2] else kind),
                 ("local z3 is not the pinned 4.16.0, and this gate's outcome is "
                  "solver-version-sensitive" if k[:2] == SOLVER_STEP[:2] else why), body)
                for k, kind, why, body in plan]

    if not node_ok():
        plan = [(k, ("needs-ci" if k[:2] in NODE_STEPS else kind),
                 ("local Node is older than the 22.7 CI pins; these gates import the "
                  "playground's ESM modules" if k[:2] in NODE_STEPS else why), body)
                for k, kind, why, body in plan]

    if not node_ok():
        plan = [(k, ("needs-ci" if k[:2] in NODE_STEPS else kind),
                 ("local Node is older than the 22.7 CI pins; these gates import the "
                  "playground's ESM modules" if k[:2] in NODE_STEPS else why), body)
                for k, kind, why, body in plan]

    unknown = [k for k, kind, _, _ in plan if kind == "unknown"]
    if unknown:
        print("REFUSING TO RUN — these CI steps are not classified:\n", file=sys.stderr)
        for wfname, step in unknown:
            print(f"    {wfname}: {step}", file=sys.stderr)
        print("\nAdd each to RUN, MUTATES or NEEDS_ENV in scripts/ci-local.py.\n"
              "An unclassified step is a failure and not a default: running it could reach\n"
              "the committed store, and skipping it would quietly narrow the sweep again —\n"
              "which is the drift this script exists to stop.", file=sys.stderr)
        return 2

    if args.list:
        for (wfname, step), kind, why, _ in plan:
            print(f"  {kind:9s} {wfname:12s} {step[:56]:58s}{why}")
        return 0

    failures: list[str] = []
    ran = 0
    for (wfname, step), kind, _, body in plan:
        if kind != "run":
            continue
        ran += 1
        start = time.monotonic()
        # GitHub Actions runs `bash --noprofile --norc -eo pipefail`. Plain
        # shell=True is /bin/sh WITHOUT fail-fast, so a multi-command block whose
        # early check fails and whose last command succeeds would exit 0 and be
        # reported green while CI failed it — a fail-open in the runner itself.
        proc = subprocess.run(["bash", "--noprofile", "--norc", "-eo", "pipefail",
                               "-c", body], cwd=ROOT,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        secs = time.monotonic() - start
        ok = proc.returncode == 0
        print(f"  {'ok  ' if ok else 'FAIL'}  {step[:58]:60s}{secs:6.1f}s", flush=True)
        if not ok:
            failures.append(f"{wfname}: {step}")
            # The gate's own message is the diagnostic — these checks print what
            # drifted and what to run. Reporting only a step name would make this
            # worse than running the step by hand.
            for line in proc.stdout.decode("utf-8", "replace").rstrip().splitlines():
                print(f"        {line}")
            print()

    print()
    for (wfname, step), kind, why, _ in plan:
        if kind != "run":
            print(f"  skipped ({kind}) {wfname}: {step} — {why}")

    print(f"\n{ran - len(failures)}/{ran} run here; {len(plan) - ran} not run.")
    if failures:
        print("FAILING:\n  " + "\n  ".join(failures))
        return 1
    print("Every gate this script runs is green. That is NOT the same as CI green — "
          "see the skipped list above.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
