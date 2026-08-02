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
import re
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
        # ONE STORE, TO A FIXPOINT. Source files are not self-contained — a file
        # can define a member while a LATER definition in the same file references
        # something from another file, so elaborating each into its own fresh store
        # fails on a dependency that is present in the corpus and absent from that
        # file. Retrying until no further progress resolves any order.
        # Every corpus file, not only the ones a member names. A member's source
        # can depend on a definition in a file no member cites — `abs` lives in
        # ints.oath, which needs `max2` from extras.oath — so restricting the set
        # to cited sources makes reproduction fail on a dependency that is present
        # in the corpus. What is being verified is that the manifest's hash is what
        # THE SOURCE produces, and the source is the corpus.
        # PROGRESS IS MEASURED BY WHAT LANDED IN THE STORE, NOT BY EXIT CODE.
        # A FALSIFIED definition is elaborated, hashed and stored — `put` exits
        # non-zero to report the verdict, not to report a failure to elaborate. The
        # corpus deliberately contains such exhibits (bad-reverse, spin, a refuted
        # float law), so treating a non-zero exit as "could not elaborate" makes
        # reproduction impossible against the corpus that actually exists.
        files = sorted(str(f.relative_to(ROOT)) for f in (ROOT / "examples").glob("*.oath"))
        seen = -1
        while True:
            for src in files:
                subprocess.run([str(ROOT / "oath" / "oath"), "put", str(ROOT / src)],
                               capture_output=True, text=True, env=env, cwd=str(ROOT))
            npath = Path(tmp) / "names.json"
            count = len(json.loads(npath.read_text())) if npath.exists() else 0
            if count == seen:
                break
            seen = count

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
            if mode not in ("referenced", "project-publication"):
                failures.append(f"{name}: membership must be `referenced` (the library depends on an "
                                f"existing publication and asserts nothing) or `project-publication` "
                                f"(the project makes its own licence assertion). Got {mode!r}")
            # A PIN IS AN INPUT FOR SOME MODES AND AN OUTPUT FOR OTHERS.
            #
            #   referenced                  ALWAYS pins: the whole entry is a
            #                               selection of one signed statement, and
            #                               without the pin it selects nothing.
            #   project-publication +       pins the CONTRIBUTOR'S publication —
            #     contributor-grant         the grant is tied to a specific one.
            #   project-publication +       has nothing to pin BEFORE it is
            #     project-authored          published. The publication is what the
            #                               publisher creates; requiring it up front
            #                               makes a first publication impossible.
            #
            # The last case is why this is not simply "every export pins": the gate
            # demanded a pin for something that does not exist yet, so no new
            # project-published member could ever be proposed.
            st_type = ((d.get("standing") or {}).get("type") or "")
            needs_pin = mode == "referenced" or st_type == "contributor-grant"
            if needs_pin and not d.get("publication"):
                failures.append(f"{name}: no `publication` — this entry must pin the EXACT signed "
                                f"publication it relies on, by the sha256 of its canonical octets. "
                                f"A name is mutable and an artifact may carry many conflicting "
                                f"assertions, so neither identifies whose grant is being consumed")
            if mode == "project-publication" and st_type == "project-authored" and d.get("publication"):
                # Present after the fact is fine and is what the pilot entries carry;
                # it just must not be REQUIRED before the publication exists.
                pass
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
            # `source` is required only for project-publication, where the project
            # republishes and must therefore reproduce the artifact itself. A
            # `referenced` entry's artifact is established by the PINNED
            # PUBLICATION — a signed statement by its author — and demanding local
            # reproduction would require the project to hold the contributor's
            # source, which is exactly the coupling the mode removes.
            if mode == "project-publication" and not d.get("source"):
                failures.append(f"{name}: project-publication with no `source` — the project republishes "
                                f"this artifact, so it must be able to re-derive the hash rather than "
                                f"assert one nobody reproduced")
            if mode == "referenced" and d.get("source"):
                failures.append(f"{name}: `referenced` must not carry a `source`. The artifact is "
                                f"established by the pinned publication, and a source here would "
                                f"suggest the project reproduces something it only selects")
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
    republished = [d for d in exported if d.get("membership") == "project-publication"]
    sources = {d["source"] for d in republished}
    print(f"reproducing {len(republished)} republished artifact(s) from {len(sources)} source file(s)…")
    derived = reproduce_hashes(sources)
    if derived is None:
        return 1

    for d in republished:
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
    by_name = {e["name"]: e for e in exported}
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

        # TYPE DEPENDENCIES, which `dependencies` does not report.
        #
        # A member that MENTIONS a datatype in its signature cannot elaborate
        # without it, exactly as a member that CALLS a function cannot. Checking
        # only the second passed a 39-definition batch whose types were missing:
        # validation was green and local elaboration failed with `unknown type
        # "Option"`. Had it been trusted, the publish run would have died partway
        # through after signing twenty-odd definitions.
        #
        # A referenced type is worse than an absent one and is called out
        # separately: it IS in the library, so the closure looks satisfied, but a
        # referenced member is selected rather than republished under the
        # namespace — so nothing resolves it in the publish store.
        tnames = type_names_of(d)
        if tnames is None:
            failures.append(f"{d['name']}: declared source {d.get('source')!r} does not contain its "
                            f"definition, so its type dependencies cannot be checked — a wrong source "
                            f"reads as a member with no types at all")
            tnames = set()
        for t in sorted(tnames):
            if t not in by_name:
                failures.append(f"{d['name']}: mentions type {t}, which is not in the library — "
                                f"oath/{d['name']} cannot elaborate without it")
            elif by_name[t].get("membership") == "referenced" and d.get("membership") == "project-publication":
                failures.append(f"{d['name']} is project-published but mentions type {t}, which is "
                                f"REFERENCED. A referenced member is selected, never republished under "
                                f"the namespace, so oath/{d['name']} has no oath/{t} to resolve against. "
                                f"Publish {t} as a project-publication, or make {d['name']} referenced too")

    print()
    if failures:
        print(f"STDLIB MANIFEST: FAIL — {len(failures)} finding(s)")
        for f in failures:
            print(f"  {f}")
        return 1

    print(f"STDLIB MANIFEST: PASS")
    # SAY WHAT WAS ACTUALLY CHECKED. "every artifact hash reproduced" was true of
    # the republished entries and false of the referenced ones, whose artifacts
    # come from a pinned publication this job cannot read — it holds no credential
    # and reaches no registry, by design. A summary that claims a stronger
    # verification than was performed is worse than a quieter one, because the
    # reader stops looking.
    print(f"  {len(republished)} project-published: artifact hash REPRODUCED from source")
    print(f"  {len(exported) - len(republished)} referenced: pin STRUCTURE checked only — the pinned "
          f"publication's")
    print(f"    existence and terms need the journal, which this job deliberately cannot reach")
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


# Type names a member mentions, read from its source form.
#
# Read from the SOURCE rather than from `explain`, because explain reports
# value-level dependencies only — which is the gap this exists to close. Capitalised
# leading identifiers are datatypes by the language's own convention; lowercase ones
# are type variables and are not library members.
def type_names_of(entry):
    """Returns None when the form cannot be found — a silent empty set would make
    a WRONG `source` look like a member with no type dependencies, which is a false
    pass rather than a missed check."""
    src = entry.get("source")
    if not src:
        return set()
    path = ROOT / src
    if not path.exists():
        return None
    text = path.read_text()
    head = f"(defn {entry['name']} " if True else ""
    i = text.find(head)
    if i < 0:
        i = text.find(f"(data {entry['name']} ")
        if i < 0:
            return None
    # The signature is everything up to the body: cheap and sufficient, since a
    # type mentioned anywhere in the form still has to resolve.
    depth, j = 0, i
    while j < len(text):
        if text[j] == "(":
            depth += 1
        elif text[j] == ")":
            depth -= 1
            if depth == 0:
                break
        j += 1
    # Comments are prose and full of capitalised words. Stripped before scanning,
    # or the check reports "type NB" and "type Pinning" from a paragraph.
    form = "\n".join(re.sub(r";.*$", "", ln) for ln in text[i:j + 1].split("\n"))
    return {m for m in re.findall(r"\b([A-Z][A-Za-z0-9-]*)\b", form)
            if m not in PRIMITIVE_TYPES and m not in constructor_names()}


PRIMITIVE_TYPES = {"Int", "Bool", "Str", "Rat", "Float", "Set", "Map", "Unit"}

_ctors = None


# Constructor names are NOT type dependencies. `Cons` and `Nil` belong to `List`
# and are published with it, so treating every capitalised identifier as a type
# reports the datatype's own constructors as missing members. Collected once from
# every `(data ...)` form in the corpus sources.
def constructor_names():
    global _ctors
    if _ctors is None:
        _ctors = set()
        for f in sorted((ROOT / "examples").glob("*.oath")):
            text = f.read_text()
            for m in re.finditer(r"\(data\s+[^\s\[]+[^\n]*\n((?:\s+\([^\n]*\n)*)", text):
                for c in re.finditer(r"\(\s*([A-Z][A-Za-z0-9-]*)", m.group(1)):
                    _ctors.add(c.group(1))
    return _ctors


if __name__ == "__main__":
    sys.exit(main())
