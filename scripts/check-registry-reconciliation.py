#!/usr/bin/env python3
"""Reconcile the committed corpus against the live registry BY CATEGORY.

The two are not supposed to be equal, and making them equal would damage both:
forcing the corpus to match the registry pollutes a reproducible input set with
deployment history; forcing the registry to match the corpus would mean deleting
from an append-only journal, which is not an operation that exists.

    codebase/         curated, reproducible INPUT SET     — what builds
    live registry     append-only OPERATIONAL HISTORY     — what happened

So this does not synchronize and does not diff. It CLASSIFIES, against the
declared policy in registry-reconciliation.json:

    corpus name absent live      FAIL     something that should be public is not
    hash mismatch                FAIL     same name, different artifact — identity moved
    alias                        expected declared mirror prefix, artifact identical
    stdlib publication           expected declared in the manifest, artifact identical
    operational probe            review   declared, explained, depended on by nothing
    registry-only artifact       review   declared, explained, never in the corpus
    UNDECLARED live name         FAIL     nobody has said what this is

The last line is the ratchet. Today's unexplained names are declared as
unexplained — honestly, with what the journal actually shows — so that history is
recorded rather than rationalised, while any NEW unaccounted name fails. That is
the difference between a policy and an amnesty.

Usage:
  check-registry-reconciliation.py <live-names.json>
  check-registry-reconciliation.py --fetch      (needs gsutil access to the store)
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
POLICY = ROOT / "registry-reconciliation.json"
BUCKET = "gs://oath-prod-503514-oath-store/names.json"


def norm(d):
    """names.json values are either a hash string or a record carrying one."""
    out = {}
    for k, v in d.items():
        out[k] = v if isinstance(v, str) else (v.get("hash") or v.get("Hash") or "")
    return out


def load_live(arg):
    if arg == "--fetch":
        tmp = Path(tempfile.mkdtemp()) / "names.json"
        r = subprocess.run(["gsutil", "-q", "cp", BUCKET, str(tmp)],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(f"cannot fetch {BUCKET}: {r.stderr.strip()}", file=sys.stderr)
            print("Refusing to reconcile against a store I could not read.", file=sys.stderr)
            sys.exit(2)
        return norm(json.loads(tmp.read_text()))
    return norm(json.loads(Path(arg).read_text()))


def main():
    if len(sys.argv) != 2:
        print(__doc__.strip().rsplit("Usage:", 1)[-1].strip())
        return 2

    policy = json.loads(POLICY.read_text())
    live = load_live(sys.argv[1])
    corpus = norm(json.loads((ROOT / policy["corpus"]).read_text()))

    manifest = json.loads((ROOT / policy["stdlib_from_manifest"]).read_text())
    ns = manifest.get("namespace", "oath")
    published = {f"{ns}/{d['name']}": d["name"]
                 for d in manifest["definitions"]
                 if d.get("membership") == "project-publication" and d.get("export")}

    probes = {p["name"]: p["reason"] for p in policy.get("operational_probes", [])}
    regonly = {p["name"]: p["reason"] for p in policy.get("registry_only", [])}
    aliases = policy.get("alias_prefixes", [])

    failures, reviews, counts = [], [], {}

    def tally(cat):
        counts[cat] = counts.get(cat, 0) + 1

    # 1. Every corpus definition must be live, at the same artifact.
    for name, h in sorted(corpus.items()):
        if name not in live:
            failures.append(f"corpus name `{name}` is ABSENT from the registry "
                            f"(artifact {h[:12]}…) — the corpus claims something the registry does not hold")
            tally("corpus_absent_live")
        elif live[name] != h:
            failures.append(f"HASH MISMATCH on `{name}`: corpus {h[:12]}… vs live {live[name][:12]}… "
                            f"— same name, different artifact, so identity moved on one side only")
            tally("hash_mismatch")
        else:
            tally("corpus_present_identical")

    # 2. Every live name not in the corpus must be accounted for.
    for name in sorted(set(live) - set(corpus)):
        h = live[name]

        alias = next((a for a in aliases if name.startswith(a["prefix"])), None)
        if alias:
            bare = name[len(alias["prefix"]):]
            if bare not in corpus:
                failures.append(f"alias `{name}` mirrors nothing: `{bare}` is not in the corpus")
                tally("undeclared")
            elif corpus[bare] != h:
                failures.append(f"alias `{name}` has DRIFTED: mirrors `{bare}` "
                                f"(corpus {corpus[bare][:12]}…) but resolves to {h[:12]}… "
                                f"— an alias that points elsewhere is a mismatch, not an alias")
                tally("hash_mismatch")
            else:
                tally("alias")
            continue

        if name in published:
            bare = published[name]
            if corpus.get(bare) != h:
                failures.append(f"standard-library publication `{name}` does not match its corpus "
                                f"artifact: corpus {corpus.get(bare,'(absent)')[:12]}… vs live {h[:12]}…")
                tally("hash_mismatch")
            else:
                tally("stdlib_publication")
            continue

        if name in probes:
            reviews.append(f"operational probe `{name}` — {probes[name]}")
            tally("operational_probe")
            continue

        if name in regonly:
            reviews.append(f"registry-only artifact `{name}` — {regonly[name]}")
            tally("registry_only")
            continue

        failures.append(f"UNDECLARED live name `{name}` (artifact {h[:12]}…): it is not in the "
                        f"corpus, not a declared alias, not a standard-library publication, and not "
                        f"a declared probe or registry-only artifact. Classify it in "
                        f"registry-reconciliation.json — silence is what lets a namespace fill up "
                        f"with things nobody chose")
        tally("undeclared")

    print(f"corpus   {len(corpus)} definitions   ({policy['corpus']})")
    print(f"registry {len(live)} names          ({policy['registry']})")
    print()
    print("BY CATEGORY")
    for cat in ("corpus_present_identical", "alias", "stdlib_publication",
                "operational_probe", "registry_only", "corpus_absent_live",
                "hash_mismatch", "undeclared"):
        if counts.get(cat):
            verdict = policy["verdicts"].get(cat, "expected")
            print(f"  {cat:26} {counts[cat]:5}   {verdict}")

    if reviews:
        print("\nREVIEW — declared, explained, and depended on by nothing:")
        for r in reviews:
            print(f"  · {r}")

    if failures:
        print("\nFAIL")
        for f in failures:
            print(f"  ✗ {f}")
        return 1

    print("\nRECONCILED. Every corpus definition is live at an identical artifact, and every "
          "extra live name is accounted for.")
    print("The two sets are NOT equal and are not meant to be: the corpus is what builds, "
          "the registry is what happened.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
