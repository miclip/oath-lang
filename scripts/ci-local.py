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
import os
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
    # PROBE-GATED. These four are runnable HERE when their prerequisite is
    # actually present, and the probes beside main() demote them when it is not.
    # They sat in NEEDS_ENV on an assumption ("may not be installed") that was
    # false on the machine that wrote it.
    ("conformance", "build library + CLI for wasm (prove feature excluded)", "d0acc6880fc4"),
    ("conformance", "build cloud driver (compile guard; stays dependency-free)", "a8e480713521"),
    ("conformance", "cloud build stays compilable", "a8e480713521"),
    ("conformance", "Postgres + GCS integration tests", "3d70b5e119a4"),
    ("conformance", "build", "824452667570"),
    ("conformance", "unit tests", "230135e255a8"),
    ("conformance", "fingerprint instrument check (#139)", "58a5a527a296"),
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
    ("conformance", "pin z3 4.16.0 (proof outcomes are solver-version-sensitive, SPEC §10.5)", "947e3becff78"):
        "provisions the runner's z3; the gate itself pins the version it used",
    ("conformance", "pin z3 4.16.0", "947e3becff78"):
        "provisions the runner's z3",
    ("conformance", "six-check conformance (cold prove at the SPEC budget)", "bd65366a170a"):
        "the 9+ hour cold re-derivation; schedule/dispatch only",
    # #98 sharded verification (matrix + merge). Schedule/dispatch only, and each
    # needs a CI runner with the pinned z3 — the same class as the full job above.
    ("conformance", "pin z3 4.16.0", "74f95be38960"):
        "provisions the runner's z3 for the sharded matrix; schedule/dispatch only",
    ("conformance", "build oathrs", "4111ddfccb69"):
        "builds the release kernel on the runner for the sharded matrix; schedule/dispatch only",
    ("conformance", "prove shard ${{ matrix.shard }} of 8", "99f96a9e8191"):
        "one parallel shard of the union==S check; schedule/dispatch only, needs CI runners",
    ("conformance", "merge and verify union == S", "db0f8707ae9e"):
        "merges the shard emissions and runs the union==S gate; schedule/dispatch only",
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


def step_env(meta: str) -> dict[str, str] | None:
    """A step's `env:` block as a dict — or None if its metadata is not env-ONLY.

    THE NARROWING THIS RELAXES, AND EXACTLY HOW FAR. Review found three ways
    local execution diverged from CI's: shell fail-fast, env, and working
    directory. The repair refused every step carrying ANY of the three, which was
    right as one rule and is coarser than it needs to be: `env:` is a set of
    key/value pairs this runner can reproduce EXACTLY, while `shell:` and
    `working-directory:` change semantics it does not implement.

    So env-only steps become runnable WITH their env applied, and anything
    carrying shell or working-directory stays refused. A step that gains
    `shell:` later falls back to refused automatically, because this returns
    None the moment it sees one.
    """
    out: dict[str, str] = {}
    for line in [l for l in meta.splitlines() if l.strip()]:
        if re.match(r"-?\s*(shell|working-directory):", line.strip()):
            return None
        st = line.strip()
        if re.match(r"-?\s*env:", st):
            # BLOCK FORM ONLY. `env: {FOO: bar}` is a valid Actions flow mapping
            # and this parser cannot read it — left to the key/value branch it
            # yielded a variable literally named "env", and the step would then
            # run WITHOUT its real variables while being reported as
            # CI-equivalent. An unrecognised shape is refused, not guessed.
            if re.match(r"-?\s*env:\s*$", st):
                continue
            return None
        m = re.match(r'([A-Za-z_][A-Za-z0-9_]*):\s*(.*)$', line.strip())
        if not m:
            return None          # a shape this parser does not understand: refuse
        out[m.group(1)] = m.group(2).strip().strip('"\'')
    return out or None


# THE PARSER'S OWN CONTROL, run before any sweep reports anything.
#
# step_env is the one place this script RELAXES a reviewed refusal, so its
# refusals are what keep the relaxation honest: a step gaining `shell:` or
# `working-directory:` must fall back to not-runnable, silently and
# automatically. That is a property of a regex, and a regex edit can lose it
# without any test noticing — so the check runs at startup rather than living
# in a suite nobody invokes before pushing.
STEP_ENV_CASES: list[tuple[str, str, dict[str, str] | None]] = [
    ("env only", 'env:\nOATH_TEST_PG_DSN: "postgres://x"', {"OATH_TEST_PG_DSN": "postgres://x"}),
    ("env + shell", "env:\nFOO: bar\nshell: bash", None),
    ("env + working-directory", "env:\nFOO: bar\nworking-directory: oath", None),
    ("shell alone", "shell: bash", None),
    ("working-directory alone", "working-directory: oath", None),
    ("a line this parser cannot read", "env:\n- something odd", None),
    ("env as an inline flow mapping", "env: {FOO: bar}", None),
    ("an empty inline mapping", "env: {}", None),
    ("no metadata", "", None),
]


def step_env_selftest() -> list[str]:
    return [f"step_env({meta!r}) = {step_env(meta)!r}, want {want!r}"
            for name, meta, want in STEP_ENV_CASES if step_env(meta) != want]


def digest(body: str) -> str:
    return hashlib.sha256(body.encode()).hexdigest()[:12]


def classify(key: tuple[str, str], body: str, has_meta: bool = False,
             env_only: dict[str, str] | None = None) -> tuple[str, str]:
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
        if has_meta and env_only is None:
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


# CAPABILITY-PROBED STEPS. These were SKIPPED ON AN ASSUMPTION — "wasm32-wasip1
# may not be installed", "cloud build tag" — and on this machine every one of
# those assumptions was false: the target was installed and the cloud tag
# compiled. They had never run locally, for no reason.
#
# A skip that names a PREREQUISITE must test for it. Otherwise the reason ages
# into a fact nobody rechecks, and the sweep quietly covers less than it says —
# which is the same defect as a silent cap, in the one script whose whole job is
# reporting what it did not do.
WASM_STEPS = {("conformance", "build library + CLI for wasm (prove feature excluded)")}
CLOUD_BUILD_STEPS = {
    ("conformance", "build cloud driver (compile guard; stays dependency-free)"),
    ("conformance", "cloud build stays compilable"),
}
PG_STEPS = {("conformance", "Postgres + GCS integration tests")}


def wasm_target_present() -> bool:
    """CI installs wasm32-wasip1 via an action this sweep skips."""
    try:
        out = subprocess.run(["rustup", "target", "list", "--installed"],
                             capture_output=True, text=True, timeout=30)
    except Exception:
        return False
    return "wasm32-wasip1" in out.stdout


def cloud_deps_present() -> bool:
    """`-tags cloud` pulls a Postgres driver; without the module cached this
    needs the network, and a sweep that silently fetches is not a local gate."""
    try:
        out = subprocess.run(["go", "list", "-tags", "cloud", "-deps", "./..."],
                             capture_output=True, text=True, timeout=120,
                             cwd=str(ROOT / "oath"),
                             # READ-ONLY, and GOFLAGS is set rather than
                             # inherited: `-mod=mod` lets `go list` WRITE go.sum,
                             # so a probe — reached even by `--list` — could dirty
                             # tracked files before a single gate ran.
                             env={**os.environ, "GOFLAGS": "-mod=readonly", "GOPROXY": "off"})
    except Exception:
        return False
    return out.returncode == 0


def postgres_present(dsn: str | None) -> bool:
    """Is a DISPOSABLE PostgreSQL that accepts this DSN listening?

    OPT-IN IS REQUIRED, AND REACHABILITY IS NOT CONSENT. This step is
    DESTRUCTIVE: the cloud tests truncate `names`, `journal` and `proofq` with
    RESTART IDENTITY. The DSN is hard-coded in the workflow, so a developer who
    happens to keep a `postgres` role and an `oathtest` database on localhost —
    an unremarkable thing to have — would have its contents destroyed by a sweep
    that advertises itself as the safe subset. Finding a server is evidence that
    one EXISTS, never that anyone agreed to lose it.

    So OATH_CI_LOCAL_PG=1 must be set, and it means "the database at this DSN is
    disposable". This is the same line `make verify` sits on: correct in CI,
    destructive here, and therefore never automatic.

    THE DSN PROBED MUST BE THE ONE THE STEP RUNS AGAINST. The step carries its
    own `env:` block, which this runner applies — so probing the ambient
    OATH_TEST_PG_DSN would green-light the step against a database it never
    connects to, and the failure would read as a broken gate rather than a
    misread probe.

    A TCP CONNECT IS NOT A READINESS CHECK, and using one turned three different
    situations into "ready": some unrelated process holding 5432, a server still
    starting, and a Postgres that rejects this user or database. Each would have
    promoted the step and produced an apparent gate FAILURE from a missing
    prerequisite — the exact confusion the probes exist to remove.

    So this speaks the protocol: a StartupMessage, then one byte of reply. 'R' is
    an authentication request, which only a Postgres past startup sends, and
    reaching it means the server accepted this USER and DATABASE. 'E' is an error
    response — a real Postgres that REFUSED this DSN (no such role, no such
    database) — a missing prerequisite, not a gate to run. Anything else is not
    Postgres. No client tools are needed, and neither psql nor pg_isready is on
    every dev machine.

    WHAT IT DOES NOT CHECK, stated rather than implied: the PASSWORD. That 'R' is
    the challenge, not its outcome, so a server that would reject these
    credentials still promotes the step and the failure surfaces as a red gate
    rather than a skip. Completing SCRAM would be a client implementation inside
    a probe. This is deliberately the SAME BAR CI USES — its service container is
    health-checked with `pg_isready`, which also stops at "accepting connections"
    and never authenticates.
    """
    if not dsn or os.environ.get("OATH_CI_LOCAL_PG") != "1":
        return False
    try:
        import socket
        import struct
        from urllib.parse import urlparse
    except Exception:
        return False
    try:
        u = urlparse(dsn)
        host, port = u.hostname or "localhost", u.port or 5432
        user = u.username or "postgres"
        db = (u.path or "/postgres").lstrip("/") or "postgres"
        payload = b"".join(k.encode() + b"\x00" + v.encode() + b"\x00"
                           for k, v in (("user", user), ("database", db))) + b"\x00"
        msg = struct.pack("!ii", len(payload) + 8, 196608) + payload
        with socket.create_connection((host, port), timeout=3) as sock:
            sock.settimeout(3)
            sock.sendall(msg)
            first = sock.recv(1)
        return first == b"R"
    except Exception:
        return False


def main() -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--list", action="store_true",
                    help="show the derived plan and run nothing")
    ap.add_argument("--digests", action="store_true",
                    help="print each step's current body digest, for updating the table")
    args = ap.parse_args()

    broken = step_env_selftest()
    if broken:
        print("REFUSING TO RUN — this script's own metadata parser is wrong:\n",
              file=sys.stderr)
        for b in broken:
            print(f"    {b}", file=sys.stderr)
        print("\nstep_env decides which steps run WITH their env and which stay\n"
              "refused. If it stops rejecting `shell:` or `working-directory:`,\n"
              "this sweep would run steps whose execution context it does not\n"
              "reproduce, and report them as CI-equivalent.", file=sys.stderr)
        return 2

    wfs = gate_workflows()
    if not wfs:
        print("VOID — no workflow triggers on pull_request; this check did NOT run",
              file=sys.stderr)
        return 2

    plan: list[tuple[tuple[str, str], str, str, str, dict[str, str] | None]] = []
    for wf in wfs:
        for name, body, meta in steps(wf):
            key = (wf.stem, name)
            # The digest covers the body AND its execution metadata: identity is
            # what the step DOES, and env/shell/working-directory change that
            # without touching a character of the command.
            senv = step_env(meta) if meta else None
            kind, why = classify(key, body + ("\n#meta#\n" + meta if meta else ""),
                                 has_meta=bool(meta), env_only=senv)
            plan.append((key, kind, why, body, senv))

    if args.digests:
        for wf in wfs:
            for name, body, meta in steps(wf):
                d = digest(body + ("\n#meta#\n" + meta if meta else ""))
                print(f'    ("{wf.stem}", "{name}", "{d}"),')
        return 0

    # The DSN the PG step will actually run against: its own `env:` if it has
    # one (it does — the workflow hard-codes it), else whatever is exported here.
    pg_dsn = next((senv.get("OATH_TEST_PG_DSN") for k, _, _, _, senv in plan
                   if k[:2] in PG_STEPS and senv), None) or os.environ.get("OATH_TEST_PG_DSN")

    # A PROBE PER PREREQUISITE, all demoting through one path. Written as five
    # near-identical list comprehensions this drifted: the node block appeared
    # TWICE, doing its work a second time for nothing, which is what a copied
    # block does when there is no shared route.
    #
    # Compare on (workflow, step): plan keys carry no digest, and writing the
    # comparison against a differently-shaped constant is how an earlier version
    # silently never fired — always false, always runnable.
    def demote(plan, names, reason):
        # ONLY `run` IS DEMOTED. Rewriting every matching entry would also
        # rewrite `unknown` — the classification that makes this script REFUSE
        # when a step's body changed — turning a required refusal into a silent
        # skip for exactly the steps a probe governs. A fail-open in the guard
        # against fail-opens; it was latent in the solver and node demotions
        # before the probes made it reachable.
        return [(k, ("needs-ci" if (kind == "run" and k[:2] in names) else kind),
                 (reason if (kind == "run" and k[:2] in names) else why), body, senv)
                for k, kind, why, body, senv in plan]

    for present, names, reason in (
        (pinned_solver_present(), {SOLVER_STEP[:2]},
         "local z3 is not the pinned 4.16.0, and this gate's outcome is "
         "solver-version-sensitive"),
        (node_ok(), NODE_STEPS,
         "local Node is older than the 22.7 CI pins; these gates import the "
         "playground's ESM modules"),
        (wasm_target_present(), WASM_STEPS,
         "PROBED: rustup does not have wasm32-wasip1 installed here"),
        (cloud_deps_present(), CLOUD_BUILD_STEPS | PG_STEPS,
         "PROBED: the cloud build tag's module deps are not resolvable offline"),
        (postgres_present(pg_dsn), PG_STEPS,
         "DESTRUCTIVE (truncates names/journal/proofq) and opt-in: set "
         "OATH_CI_LOCAL_PG=1 to affirm the database is disposable, and have one "
         "reachable. A throwaway matching CI:\n"
         "             docker run -d --name oath-ci-pg -e POSTGRES_PASSWORD=postgres "
         "-e POSTGRES_DB=oathtest -p 5432:5432 postgres:15\n"
         "             export OATH_CI_LOCAL_PG=1"),
    ):
        if not present:
            plan = demote(plan, names, reason)

    unknown = [k for k, kind, _, _, _ in plan if kind == "unknown"]
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
        for (wfname, step), kind, why, _, _ in plan:
            print(f"  {kind:9s} {wfname:12s} {step[:56]:58s}{why}")
        return 0

    failures: list[str] = []
    ran = 0
    for (wfname, step), kind, _, body, senv in plan:
        if kind != "run":
            continue
        ran += 1
        start = time.monotonic()
        # GitHub Actions runs `bash --noprofile --norc -eo pipefail`. Plain
        # shell=True is /bin/sh WITHOUT fail-fast, so a multi-command block whose
        # early check fails and whose last command succeeds would exit 0 and be
        # reported green while CI failed it — a fail-open in the runner itself.
        # THE STEP'S OWN `env:` IS APPLIED, and nothing else about the runner is
        # reproduced. An env-only step is the one slice of execution context this
        # script can honour exactly; step_env refuses anything more.
        proc = subprocess.run(["bash", "--noprofile", "--norc", "-eo", "pipefail",
                               "-c", body], cwd=ROOT,
                              env={**os.environ, **(senv or {})},
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
    for (wfname, step), kind, why, _, _ in plan:
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
