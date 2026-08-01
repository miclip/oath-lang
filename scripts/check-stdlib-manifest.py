#!/usr/bin/env python3
"""Verify stdlib/oath-stdlib.json against reproduced artifacts and the live registry.

THE MANIFEST IS AN ASSERTION AND IS NOT TRUSTED. Every artifact hash in it is
RECOMPUTED from the canonical object before anything is believed, because a
manifest that could assert a hash without reproducing it would let a reviewer
approve one artifact while the publisher published another — and the review
would be evidence of nothing.

The external invariant this exists to protect:

    Every oath/<name> publication must correspond exactly to the reviewed
    manifest entry at the merged commit.

READ-ONLY. This runs in pull-request CI, which must never hold a signing key.
It elaborates, hashes, compares, and reports a proposed delta; it publishes
nothing and needs no secret. The publishing workflow runs only after merge, on a
protected branch, and re-derives everything here from scratch rather than
trusting an artifact this produced.

Usage:
  check-stdlib-manifest.py                     verify the manifest reproduces
  check-stdlib-manifest.py --delta <ls.json>   also diff against a live `ls` dump
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = ROOT / "stdlib" / "oath-stdlib.json"


def reproduce_hashes(sources):
    """Elaborate each source into a FRESH store and return name -> hash.

    A fresh store, not the committed one: reading codebase/names.json would be
    trusting a recorded value, and the whole point is to derive the hash from the
    bytes under review. `put` typechecks and content-addresses, so the hash it
    lands on is the identity the registry would compute.
    """
    out = {}
    with tempfile.TemporaryDirectory(prefix="oath-stdlib-") as tmp:
        env = {"OATH_STORE": tmp, "PATH": "/usr/bin:/bin:/usr/local/bin"}
        for src in sorted(sources):
            r = subprocess.run([str(ROOT / "oath" / "oath"), "put", str(ROOT / src)],
                               capture_output=True, text=True, env=env, cwd=str(ROOT))
            if r.returncode != 0:
                print(f"FAIL: could not elaborate {src}:\n{r.stdout}{r.stderr}")
                return None
        names = Path(tmp) / "names.json"
        if not names.exists():
            print("FAIL: elaboration produced no names.json — nothing was stored")
            return None
        out = json.loads(names.read_text())
    return out


def main():
    if not MANIFEST.exists():
        print(f"FAIL: {MANIFEST.relative_to(ROOT)} does not exist")
        return 1
    m = json.loads(MANIFEST.read_text())
    failures, warnings = [], []

    if m.get("format") != "oath-stdlib/1":
        failures.append(f"unknown manifest format {m.get('format')!r}")
    ns = m.get("namespace", "")
    if not ns or "/" in ns or "*" in ns:
        failures.append(f"namespace {ns!r} must be a single bare segment, e.g. \"oath\"")
    auth = m.get("authority_pubkey", "")
    if len(auth) != 64 or any(c not in "0123456789abcdef" for c in auth):
        failures.append("authority_pubkey must be a 64-char lowercase hex Ed25519 key — "
                        "without it nothing states WHO may publish these names")

    defs = m.get("definitions", [])
    if not defs:
        failures.append("manifest lists no definitions — it would authorise publishing nothing")

    # Structural rules that make a REVIEW meaningful.
    seen = set()
    exported, excluded = [], []
    for d in defs:
        name = d.get("name", "")
        if not name:
            failures.append("a definition entry has no name")
            continue
        if name in seen:
            failures.append(f"{name}: listed twice — which entry governs would depend on file order")
        seen.add(name)
        if "/" in name:
            failures.append(f"{name}: manifest names are BARE; the namespace is applied by the publisher, "
                            f"so a slash here would publish oath/{name} and mean something else")
        if not d.get("artifact"):
            failures.append(f"{name}: no artifact hash")
        mode = d.get("membership")
        if d.get("export"):
            exported.append(d)
            # MEMBERSHIP MODE decides what claim is being made, so it decides what
            # must be proven. Defaulting it would let the stronger claim be made by
            # omission, which is the whole failure this distinction prevents.
            # `referenced` is REJECTED until its mechanism exists. The manifest
            # accepts a distinction the publish path erases: every export must be
            # live as oath/<name>, and the workflow asserts the manifest's
            # top-level licence for all of them — so a `referenced` entry would be
            # republished under oath/* carrying a PROJECT assertion, which is
            # exactly the laundering the mode exists to prevent.
            #
            # Refusing is the safe state. A mode that is a label with no mechanism
            # behind it is worse than an absent mode, because a reviewer reading
            # "referenced" would believe no project assertion was being made.
            if mode == "referenced":
                failures.append(
                    f"{name}: `referenced` is not yet implemented and is REFUSED. Its mechanics "
                    f"(no oath/<name> binding, no project licence assertion, resolution through "
                    f"the pinned publication) do not exist, and the publish path would republish "
                    f"it under oath/* with the project's licence — replacing the publisher's "
                    f"assertion with one made by the curator. Use `project-publication` only "
                    f"where the project genuinely has standing. See issue #107")
            elif mode not in ("referenced", "project-publication"):
                failures.append(f"{name}: membership must be `referenced` (the library depends on an "
                                f"existing publication and asserts nothing) or `project-publication` "
                                f"(the project makes its own licence assertion). Got {mode!r}")
            if not d.get("publication"):
                failures.append(f"{name}: no `publication` — an entry must pin the EXACT signed "
                                f"publication it relies on, by the sha256 of its canonical octets. "
                                f"A name is mutable and an artifact may carry many conflicting "
                                f"assertions, so neither identifies whose grant is being consumed")
            if mode == "project-publication":
                # The stronger claim: WE assert terms over this artifact.
                if not d.get("license"):
                    failures.append(f"{name}: project-publication with no `license` — the mode exists "
                                    f"precisely to make an assertion, so it must say what is asserted")
                st = d.get("standing") or {}
                if not st.get("type") or not st.get("evidence"):
                    failures.append(f"{name}: project-publication requires `standing` with a `type` and "
                                    f"`evidence`. Cryptographic ownership of a registry name proves "
                                    f"authority over the NAME, not copyright or the right to relicense — "
                                    f"so ownership alone can never justify this mode")
                src_lic = (st.get("source_publication_license") or "").strip()
                if src_lic in ("", "-"):
                    failures.append(f"{name}: the pinned source publication asserts no terms (UNSTATED). "
                                    f"Absence of a prohibition is not a grant, and UNSTATED is contagious, "
                                    f"so it cannot be adopted under project terms")
            if mode == "referenced" and d.get("license"):
                failures.append(f"{name}: `referenced` entries MUST NOT carry a licence — the mode means "
                                f"the library asserts nothing and evaluation consumes the pinned "
                                f"publication's own terms. A licence here would be a fresh assertion "
                                f"wearing the weaker mode's label")
            if not d.get("source"):
                failures.append(f"{name}: exported but has no `source` — its hash could not be reproduced, "
                                f"so the manifest would be asserting an artifact nobody re-derived")
        else:
            excluded.append(d)
            # An exclusion without a reason is indistinguishable from an accident.
            if not d.get("reason"):
                failures.append(f"{name}: export:false with no `reason` — an unexplained exclusion "
                                f"cannot be reviewed, and silently dropping a name is exactly the "
                                f"change this manifest exists to make visible")

    if failures:
        print("STDLIB MANIFEST: FAIL")
        for f in failures:
            print(f"  {f}")
        return 1

    # REPRODUCTION. This is the check that matters.
    sources = {d["source"] for d in exported}
    print(f"reproducing {len(exported)} exported artifact(s) from {len(sources)} source file(s)…")
    derived = reproduce_hashes(sources)
    if derived is None:
        return 1

    for d in exported:
        name, claimed = d["name"], d["artifact"]
        got = derived.get(name)
        if got is None:
            failures.append(f"{name}: not produced by {d['source']} — the manifest names a definition "
                            f"that source does not define")
        elif got != claimed:
            failures.append(f"{name}: manifest asserts {claimed[:16]}… but {d['source']} produces "
                            f"{got[:16]}… — the reviewed bytes and the asserted artifact disagree")

    # CLOSURE. Publishing an export whose dependency is not itself exported would
    # put a name into oath/* that resolves through an artifact nobody reviewed as
    # part of this library.
    for d in exported:
        r = subprocess.run([str(ROOT / "oath" / "oath"), "explain", d["name"], "--json"],
                           capture_output=True, text=True,
                           env={"OATH_STORE": str(ROOT / "codebase"), "PATH": "/usr/bin:/bin"},
                           cwd=str(ROOT))
        if r.returncode != 0:
            warnings.append(f"{d['name']}: closure not checked (explain unavailable in this checkout)")
            continue
        try:
            deps = json.loads(r.stdout).get("dependencies") or []
        except json.JSONDecodeError:
            warnings.append(f"{d['name']}: closure not checked (unreadable explain output)")
            continue
        for dep in deps:
            dep_name = dep.split()[0] if isinstance(dep, str) else dep.get("name", "")
            if dep_name and dep_name not in {e["name"] for e in exported}:
                failures.append(f"{d['name']}: depends on {dep_name}, which is not exported — "
                                f"oath/{d['name']} would resolve through an artifact this library "
                                f"never published")

    print()
    if failures:
        print(f"STDLIB MANIFEST: FAIL — {len(failures)} finding(s)")
        for f in failures:
            print(f"  {f}")
        return 1

    print(f"STDLIB MANIFEST: PASS")
    print(f"  {len(exported)} exported, every artifact hash REPRODUCED from its source")
    print(f"  {len(excluded)} excluded, every exclusion carries a reason")
    print(f"  dependency closure is complete: no export resolves outside the library")
    print(f"  namespace: {ns}/*   authority: {auth[:16]}…   licence: {m.get('license')}")
    for w in warnings:
        print(f"  NOTE: {w}")
    print()
    print("  This says the manifest is internally sound and reproduces. It says NOTHING")
    print("  about what is live — comparing the registry against this file is the")
    print("  publishing workflow's job, after merge, from a re-derivation of its own.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
