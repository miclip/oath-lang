#!/usr/bin/env python3
"""Generate a standard-library publication receipt from evidence, never from claims.

WHY THIS IS A GENERATOR AND NOT A TEMPLATE YOU FILL IN. A receipt whose ticks are
typed by a person is a claim about verification. A receipt whose ticks are
computed is verification. The difference matters most years later, when the
receipt is the only thing anyone reads and nobody remembers whether the checks
were actually run.

So every line below is DERIVED. A check that cannot be performed prints as
UNVERIFIED with the reason, and the receipt still generates — an honest gap is
worth more than a tick nobody earned, and a generator that refused to emit
anything would just push people back to writing receipts by hand.

Usage:
  publication-receipt.py <journal.jsonl> [--out docs/receipts/NNN.md]

The journal snapshot is the evidence. Pass a file pulled from the registry, not
the committed corpus — the receipt is about what is LIVE.
"""

import base64
import hashlib
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = ROOT / "stdlib" / "oath-stdlib.json"


def sh(*args, **kw):
    return subprocess.run(args, capture_output=True, text=True, cwd=str(ROOT), **kw)


def envelopes(rows):
    """name -> list of (envelope_kv, entry) for every signed publication."""
    out = {}
    for e in rows:
        if not e.get("envelope_b64"):
            continue
        try:
            oct_ = base64.b64decode(e["envelope_b64"], validate=True)
            lines = oct_.decode().rstrip("\n").split("\n")
        except Exception:
            continue
        if not lines[0].startswith("oath-publish/"):
            continue
        kv = dict(l.split("=", 1) for l in lines[1:] if "=" in l)
        kv["_octets"] = oct_
        out.setdefault(kv.get("name", ""), []).append((kv, e))
    return out


def main():
    if len(sys.argv) < 2:
        print(__doc__.strip().rsplit("Usage:", 1)[-1].strip())
        return 2
    snap = Path(sys.argv[1])
    out_path = None
    if "--out" in sys.argv:
        out_path = Path(sys.argv[sys.argv.index("--out") + 1])
    if not snap.exists():
        print(f"FAIL: {snap} does not exist")
        return 1

    rows = [json.loads(l) for l in snap.read_text().splitlines() if l.strip()]
    man = json.loads(MANIFEST.read_text())
    ns = man["namespace"]
    exported = [d for d in man["definitions"] if d.get("export")]
    want = [f"{ns}/{d['name']}" for d in exported]
    pubs = envelopes(rows)

    commit = sh("git", "rev-parse", "HEAD").stdout.strip()[:12]
    dirty = bool(sh("git", "status", "--porcelain", "stdlib/").stdout.strip())
    man_digest = hashlib.sha256(MANIFEST.read_bytes()).hexdigest()
    authority = man.get("authority_pubkey", "")

    checks = []

    def check(label, ok, detail=""):
        checks.append((label, ok, detail))

    # 1. the repository manifest reproduces
    r = sh(sys.executable, str(ROOT / "scripts" / "check-stdlib-manifest.py"))
    check("repository manifest reproduced", r.returncode == 0,
          "" if r.returncode == 0 else r.stdout.strip().splitlines()[-1] if r.stdout.strip() else "checker failed")

    # 2. every manifest export is live. NOT "and only those".
    #
    # LIBRARY MEMBERSHIP and NAMESPACE OCCUPANCY are different sets and must not
    # be required to be equal. The manifest exports are what the project endorses
    # as the library; the names bound under the prefix are what the registry
    # historically holds. A name can leave the library — that is a manifest edit,
    # reviewable and reversible — while its registry binding remains, because the
    # journal is append-only and removing a member is not a retraction of history.
    #
    # The first version of this check demanded equality, which made curation
    # impossible: dropping an entry would have turned the receipt permanently red,
    # so the only way to keep it green would have been never to remove anything.
    # A gate whose cheapest satisfaction is "never curate" is measuring the wrong
    # thing.
    live = [n for n in pubs if n.startswith(ns + "/")]
    missing = sorted(set(want) - set(live))
    nonmembers = sorted(set(live) - set(want))
    check(f"all {len(want)} library members are live",
          not missing,
          f"declared but NOT live: {missing}" if missing else "")
    # Exclusivity is opt-in. A manifest may claim the namespace holds nothing but
    # the library; this one does not, and demanding it by default would conflate a
    # curated view with the infrastructure it is a view over.
    if man.get("exclusive_namespace"):
        check("the namespace contains ONLY library members",
              not nonmembers,
              f"non-members present: {nonmembers}" if nonmembers else "")

    # 3. envelope bytes reproduce the artifact the manifest pins
    mismatched = []
    for d in exported:
        n = f"{ns}/{d['name']}"
        for kv, _ in pubs.get(n, []):
            if kv.get("artifact") != d["artifact"]:
                mismatched.append(f"{n}: envelope {kv.get('artifact','')[:12]} vs manifest {d['artifact'][:12]}")
    # NOT VACUOUS: with no publications there is nothing to compare, and a tick
    # would mean "examined nothing and found nothing wrong" — which reads
    # identically to "verified" on a receipt nobody re-runs.
    n_examined = sum(len(pubs.get(f"{ns}/{d['name']}", [])) for d in exported)
    check("envelope bytes carry the manifest's artifact",
          not mismatched and n_examined > 0,
          "; ".join(mismatched) if mismatched else
          ("no publications to examine — this check verified nothing" if n_examined == 0 else ""))

    # 4. journal entries persisted and are internally consistent
    r = sh(sys.executable, str(ROOT / "scripts" / "cutover-check.py"), str(snap))
    check("journal entries persisted and chain intact", r.returncode == 0,
          "" if r.returncode == 0 else "cutover-check reported findings")

    # 5. every publication is signed BY THE AUTHORITY KEY
    wrong_signer = []
    for n in want:
        for kv, e in pubs.get(n, []):
            if kv.get("author") != authority or e.get("author_pubkey") != authority:
                wrong_signer.append(n)
    check("signatures are by the recorded authority key", not wrong_signer and bool(live),
          f"not signed by {authority[:12]}…: {sorted(set(wrong_signer))}" if wrong_signer
          else ("no publications found" if not live else ""))

    # 6. the namespace authority is what the manifest names
    auth_live = None
    for e in rows:
        if e.get("kind") != "reserve" or e.get("status") != "accepted":
            continue
        try:
            oct_ = base64.b64decode(e["envelope_b64"], validate=True).decode()
        except Exception:
            continue
        kv = dict(l.split("=", 1) for l in oct_.rstrip("\n").split("\n")[1:] if "=" in l)
        if kv.get("namespace") == f"{ns}/*":
            auth_live = kv.get("pubkey")
    check("namespace authority verified from the journal", auth_live == authority,
          f"journal says {(auth_live or 'none')[:12]}…, manifest says {authority[:12]}…"
          if auth_live != authority else "")

    # 7. licence assertions match the manifest
    bad_lic = []
    for d in exported:
        n = f"{ns}/{d['name']}"
        expect = d.get("license") if d.get("membership") == "project-publication" else None
        for kv, _ in pubs.get(n, []):
            if expect and kv.get("license") != expect:
                bad_lic.append(f"{n}: {kv.get('license')} != {expect}")
    check("licence assertions reproduce the manifest",
          not bad_lic and n_examined > 0,
          "; ".join(bad_lic) if bad_lic else
          ("no publications to examine — this check verified nothing" if n_examined == 0 else ""))

    # 8. dependency closure is complete (the manifest gate already proves this)
    check("dependency closure reproduced", checks[0][1] and n_examined > 0,
          "" if checks[0][1] and n_examined > 0 else
          ("no publications to examine" if n_examined == 0 else "inherited from the manifest check above"))

    ok = all(c[1] for c in checks)
    n_pub = len([n for n in live])
    lines = []
    lines.append(f"# Standard Library Publication — {ns}/*\n")
    if dirty:
        lines.append("> **WARNING: `stdlib/` had uncommitted changes when this receipt was")
        lines.append("> generated.** The manifest digest below does not correspond to any commit.\n")
    lines.append("## Manifest\n")
    lines.append(f"- commit: `{commit}`")
    lines.append(f"- manifest digest: `{man_digest}`")
    lines.append(f"- membership modes: " + ", ".join(sorted({d.get('membership','?') for d in exported})) + "\n")
    lines.append("## Signer\n")
    lines.append(f"- KMS key version: `{man.get('authority_kms','(not recorded in manifest)')}`")
    lines.append(f"- public key: `{authority}`")
    lines.append(f"- fingerprint: `{hashlib.sha256(authority.encode()).hexdigest()}`\n")
    lines.append("## Library members\n")
    for n in sorted(want):
        lines.append(f"- `{n}`" + ("" if n in live else "  **(DECLARED BUT NOT LIVE)**"))
    lines.append("")
    lines.append("## Non-members under the namespace\n")
    if nonmembers:
        lines.append("Bound under `%s/` and NOT part of the standard library. Listed, not" % ns)
        lines.append("failed: the registry is append-only historical infrastructure and the")
        lines.append("library is a curated view over it.\n")
        for n in nonmembers:
            lines.append(f"- `{n}`")
    else:
        lines.append("_none — every name under the namespace is a library member_")
    lines.append("")
    lines.append("## Verification\n")
    for label, passed, detail in checks:
        mark = "✓" if passed else "✗"
        lines.append(f"- {mark} {label}" + (f" — {detail}" if detail else ""))
    lines.append("")
    lines.append("## What this receipt does and does not establish\n")
    lines.append("Every tick above was COMPUTED by `scripts/publication-receipt.py` from the")
    lines.append("journal snapshot named below, not asserted by a person. A check that could not")
    lines.append("be performed appears as ✗ with its reason rather than being omitted.\n")
    lines.append("It establishes that the repository, the manifest, the signed journal and the")
    lines.append("live namespace agree. It does NOT establish that the asserted licence terms are")
    lines.append("true, nor that the publisher held the rights they assert — the registry")
    lines.append("notarises a claim and does not audit the claimant.\n")
    lines.append(f"- journal snapshot: `{snap.name}`, {len(rows)} entries")
    lines.append(f"- signed entries: {len([e for e in rows if e.get('author_sig')])}")
    lines.append(f"- publications under `{ns}/`: {n_pub}")

    text = "\n".join(lines) + "\n"
    if out_path:
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(text)
        print(f"wrote {out_path}")
    else:
        print(text)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
