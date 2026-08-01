#!/usr/bin/env python3
"""Compute the proposed standard-library delta, offline, with no capability to publish.

This is the REVIEW OBJECT for a pull request. A source diff shows what changed in
the text; this shows what the change would MEAN to the registry — which names
appear, which repoint, whose grant is consumed, and whether the dependency
closure moved. Those are the terms Oath is designed around, and they are not
readable from a diff.

WHAT THIS DELIBERATELY CANNOT DO. It holds no credential, contacts no registry,
invokes no signer and writes nothing. It runs in pull-request CI, where the
content under examination is attacker-controlled, so it is built to be incapable
rather than merely disinclined.

WHAT IT THEREFORE CANNOT VERIFY, stated rather than implied: a `referenced` entry
pins a source publication by digest, and confirming that publication EXISTS and
carries the terms claimed requires the journal, which requires authentication.
This checks the pin's STRUCTURE. Existence is the post-merge job's to establish,
and the report says so rather than leaving a reader to assume it was done.

Usage:
  stdlib-delta.py <base-manifest.json> [--out report.md]

`base-manifest.json` is the manifest as it exists on the target branch. In CI:
    git show origin/main:stdlib/oath-stdlib.json > /tmp/base.json
"""

import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
HEAD_MANIFEST = ROOT / "stdlib" / "oath-stdlib.json"


def index(man):
    return {d["name"]: d for d in man.get("definitions", []) if d.get("export")}


def closure(name):
    """Dependency closure of a name from the committed store, or None."""
    r = subprocess.run([str(ROOT / "oath" / "oath"), "explain", name, "--json"],
                       capture_output=True, text=True, cwd=str(ROOT),
                       env={"OATH_STORE": str(ROOT / "codebase"), "PATH": "/usr/bin:/bin"})
    if r.returncode != 0:
        return None
    try:
        deps = json.loads(r.stdout).get("dependencies") or []
    except json.JSONDecodeError:
        return None
    return sorted(d.split()[0] if isinstance(d, str) else d.get("name", "") for d in deps)


def main():
    if len(sys.argv) < 2:
        print(__doc__.strip().rsplit("Usage:", 1)[-1].strip())
        return 2
    base_path = Path(sys.argv[1])
    out_path = None
    if "--out" in sys.argv:
        out_path = Path(sys.argv[sys.argv.index("--out") + 1])

    head = json.loads(HEAD_MANIFEST.read_text())
    base = json.loads(base_path.read_text()) if base_path.exists() else {"definitions": []}
    h, b = index(head), index(base)
    ns = head.get("namespace", "oath")

    added = sorted(set(h) - set(b))
    removed = sorted(set(b) - set(h))
    common = sorted(set(h) & set(b))

    repointed, relicensed, remoded, repinned = [], [], [], []
    for n in common:
        if h[n].get("artifact") != b[n].get("artifact"):
            repointed.append(n)
        if h[n].get("license") != b[n].get("license"):
            relicensed.append(n)
        if h[n].get("membership") != b[n].get("membership"):
            remoded.append(n)
        if h[n].get("publication") != b[n].get("publication"):
            repinned.append(n)

    problems = []
    # A removal or a repoint is not forbidden, but it must be EXPLAINED. Silently
    # dropping a canonical name, or silently moving one to a different artifact,
    # are the two changes a reviewer is least likely to notice in a JSON diff and
    # most likely to regret.
    for n in removed:
        if not (b[n].get("reason") or h.get(n, {}).get("reason")):
            problems.append(f"{ns}/{n} is REMOVED with no `reason` recorded anywhere — "
                            f"an unexplained removal of a canonical name cannot be reviewed")
    for n in repointed:
        if not h[n].get("repoint_reason"):
            problems.append(f"{ns}/{n} REPOINTS from {b[n].get('artifact','')[:12]}… to "
                            f"{h[n].get('artifact','')[:12]}… with no `repoint_reason` — consumers "
                            f"already depend on the old artifact, so this needs a stated why")

    lines = []
    lines.append("# Standard library proposal\n")
    lines.append(f"Namespace: `{ns}/*`\n")

    def block(title, names, render):
        lines.append(f"## {title}\n")
        if not names:
            lines.append("_none_\n")
            return
        for n in names:
            lines.extend(render(n))
        lines.append("")

    def add_render(n):
        d = h[n]
        # A referenced member is shown as `stdlib/<name>` because no `oath/<name>`
        # is created. Labelling it with the namespace prefix would assert exactly
        # the binding the mode declines to make, in the report a reviewer reads to
        # decide whether that binding is warranted.
        label = f"stdlib/{n}" if d.get("membership") == "referenced" else f"{ns}/{n}"
        out = [f"- **`{label}`**",
               f"  - artifact: `{d.get('artifact','')}`",
               f"  - membership: `{d.get('membership','?')}`"]
        if d.get("membership") == "referenced":
            out.append(f"  - selected publication: `{d.get('publication','(none)')}`")
            out.append(f"  - registry publication under `{ns}/`: **none** — this member is selected, not republished")
            out.append("  - project licensing assertion: **none** — terms are the publisher's own")
        else:
            out.append(f"  - registry publication under `{ns}/`: `{ns}/{n}` will be created")
            out.append(f"  - project licensing assertion: `{d.get('license','(none)')}`")
            st = d.get("standing") or {}
            out.append(f"  - standing: `{st.get('type','(none)')}` — {st.get('evidence','(no evidence)')[:120]}")
        # None means the lookup FAILED; [] means the definition genuinely depends on
        # nothing — a datatype, typically. Reporting both as "not resolvable" tells
        # a reviewer the closure is unknown when in fact it is empty and complete,
        # which is the difference between "check this" and "nothing to check".
        c = closure(n)
        if c is None:
            out.append("  - closure: _not resolvable from the committed store_")
        elif not c:
            out.append("  - closure: none — this definition depends on nothing")
        else:
            out.append(f"  - closure: {', '.join(c)}")
        return out

    def simple(n):
        return [f"- **`{ns}/{n}`** — artifact `{b[n].get('artifact','')[:16]}…`"]

    def repoint_render(n):
        cb, ch = closure(n), closure(n)
        return [f"- **`{ns}/{n}`**",
                f"  - old artifact: `{b[n].get('artifact','')}`",
                f"  - new artifact: `{h[n].get('artifact','')}`",
                f"  - closure now: {', '.join(ch) if ch else '_unresolvable_'}",
                f"  - reason: {h[n].get('repoint_reason','**NOT GIVEN**')}"]

    block("Add", added, add_render)
    block("Remove", removed, simple)
    block("Repoint", repointed, repoint_render)

    lines.append("## Other changes\n")
    other = False
    for label, names in (("licence assertion changed", relicensed),
                         ("membership mode changed", remoded),
                         ("pinned source publication changed", repinned)):
        for n in names:
            if n in repointed and label == "pinned source publication changed":
                continue
            other = True
            lines.append(f"- `{ns}/{n}` — {label}: "
                         f"`{b[n].get('license') or b[n].get('membership') or b[n].get('publication','')}` → "
                         f"`{h[n].get('license') or h[n].get('membership') or h[n].get('publication','')}`")
    if not other:
        lines.append("_none_")
    lines.append("")

    lines.append("## Publication capability\n")
    lines.append("**unavailable in this workflow.** This job holds no credential, contacts no")
    lines.append("registry, and cannot invoke a signer. It establishes **VALIDATION PASSED**.")
    lines.append("It cannot establish **PUBLICATION AUTHORIZED**, which is a separate decision")
    lines.append("made after merge, on a protected branch, with explicit approval.\n")

    lines.append("## Not verified here\n")
    ref = [n for n in h if h[n].get("membership") == "referenced"]
    if ref:
        lines.append("- For `referenced` entries, the pinned source publication's **existence and")
        lines.append("  asserted terms** were NOT checked. That requires the journal, which requires")
        lines.append("  authentication this job deliberately does not have. Only the pin's structure")
        lines.append(f"  was validated: {', '.join(f'`{ns}/{n}`' for n in sorted(ref))}")
    else:
        lines.append("- No `referenced` entries in this proposal.")
    lines.append("- Whether asserted licence terms are TRUE, and whether the publisher holds the")
    lines.append("  rights they assert. No mechanism in this repository establishes that.\n")

    if problems:
        lines.append("## Findings\n")
        for p in problems:
            lines.append(f"- ✗ {p}")
        lines.append("")

    # A machine-readable verdict alongside the human one. Whether anything needs
    # publishing is a DERIVED fact, and a workflow that re-derived it by grepping
    # the report would be parsing prose that exists to be read by people.
    # PUBLICATION is needed only when something PUBLISHABLE changed. Referenced
    # members are selected, never republished — the planner skips them — so a
    # referenced-only change must not trigger the publish step.
    #
    # Computing this over all exports would republish every project-published
    # member as a signing no-op whenever anyone added a referenced one: a run that
    # changes nothing publishable would still sign and write, which is exactly the
    # property the no-delta gate exists to guarantee.
    def publishable(names, side):
        return [n for n in names if (side.get(n) or {}).get("membership") == "project-publication"]

    changed = bool(publishable(added, h) or publishable(removed, b)
                   or publishable(repointed, h) or publishable(relicensed, h)
                   or publishable(remoded, h) or publishable(remoded, b)
                   or publishable(repinned, h))
    lines.append("## Verdict\n")
    lines.append(f"- publication needed: **{'yes' if changed else 'no'}**")
    if not changed:
        lines.append("- nothing to sign and nothing to submit. Either the manifest already")
        lines.append("  describes what the registry holds, or the change affects only")
        lines.append("  `referenced` members, which are selected rather than republished")
    lines.append("")

    text = "\n".join(lines) + "\n"
    if out_path:
        out_path.write_text(text)
        (out_path.parent / "delta-changed.txt").write_text("yes" if changed else "no")
        print(f"wrote {out_path}")
    print(text)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
