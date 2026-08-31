#!/usr/bin/env python3
"""Reconcile the committed corpus against the live registry BY CLASSIFICATION.

The two are not supposed to be equal, and making them equal would damage both:
forcing the corpus to match the registry pollutes a reproducible input set with
deployment history; forcing the registry to match the corpus would mean deleting
from an append-only journal, which is not an operation that exists.

    codebase/         curated, reproducible INPUT SET     — what builds
    live registry     append-only OPERATIONAL HISTORY     — what happened

WHAT THIS CHECKS, and why it is not a count. A total-equality check ("383 names,
as expected") passes if one legitimate name vanishes while one unexplained name
appears — the two errors cancel, and the cancellation is invisible. So the
condition is:

    Every live name belongs to EXACTLY ONE declared category, and every REQUIRED
    member of every category is present with the expected artifact relationship.

Both halves are load-bearing. The first catches arrivals; the second catches
disappearances. Neither is expressible as a number.

    corpus name absent live      FAIL     something that should be public is not
    hash mismatch                FAIL     same name, different artifact — identity moved
    alias                        expected declared mirror prefix, artifact identical
    stdlib publication           expected declared in the manifest, artifact identical
    operational probe            review   declared, explained, depended on by nothing
    registry-only artifact       review   declared, explained, never in the corpus
    UNDECLARED live name         FAIL     nobody has said what this is
    AMBIGUOUS                    FAIL     two categories claim the same name

The undeclared case is the ratchet. Today's unexplained names are declared as
unexplained — honestly, with what the journal actually shows — so that history is
recorded rather than rationalised, while any NEW unaccounted name fails. That is
the difference between a policy and an amnesty.

Usage:
  check-registry-reconciliation.py <live-names.json> [--journal <log.jsonl>]
  check-registry-reconciliation.py --fetch [--journal <log.jsonl>]
  check-registry-reconciliation.py <live-names.json> --policy <policy.json>

--journal enriches an UNDECLARED failure with the name's first publication,
authorship and signing status, so the report says what the thing IS rather than
only that it was not expected.

--policy points at a different declaration, with its `corpus` and
`stdlib_from_manifest` paths resolved relative to the policy file. That is what
lets the ratchet be tested against a fully synthetic tree, with no credentials and
no network — see scripts/test-reconciliation-ratchet.py.
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
POLICY = ROOT / "registry-reconciliation.json"
BUCKET = "gs://oath-prod-503514-oath-store"


def norm(d):
    """names.json values are either a hash string or a record carrying one."""
    out = {}
    for k, v in d.items():
        out[k] = v if isinstance(v, str) else (v.get("hash") or v.get("Hash") or "")
    return out


def fetch(obj, dest):
    r = subprocess.run(["gsutil", "-q", "cp", f"{BUCKET}/{obj}", str(dest)],
                       capture_output=True, text=True)
    if r.returncode != 0:
        print(f"cannot fetch {BUCKET}/{obj}: {r.stderr.strip()}", file=sys.stderr)
        print("Refusing to reconcile against a store I could not read.", file=sys.stderr)
        sys.exit(2)


def load_journal(path, raw=None):
    """name -> (seq, time, author, signed) for its FIRST appearance.

    `raw`, when given, is filled with the whole first-binding record, which the
    freeze check needs — it must distinguish "unsigned" from "unsigned AND after
    the boundary", and that is a fact about the entry rather than about the name.
    """
    first = {}
    if not path:
        return first
    for line in Path(path).read_text().splitlines():
        if not line.strip():
            continue
        try:
            e = json.loads(line)
        except json.JSONDecodeError:
            continue
        n = e.get("name")
        if not n or n in first or e.get("status") not in (None, "accepted"):
            continue
        first[n] = (e.get("seq"), (e.get("time") or "")[:19], e.get("author"),
                    bool(e.get("author_pubkey")))
        if raw is not None:
            raw[n] = e
    return first


def main():
    argv = sys.argv[1:]
    if not argv:
        print(__doc__.strip().rsplit("Usage:", 1)[-1].strip())
        return 2
    policy_path = POLICY
    if "--policy" in argv:
        i = argv.index("--policy")
        policy_path = Path(argv[i + 1])
        argv = argv[:i] + argv[i + 2:]
    journal_path = None
    if "--journal" in argv:
        i = argv.index("--journal")
        journal_path = argv[i + 1]
        argv = argv[:i] + argv[i + 2:]

    policy = json.loads(policy_path.read_text())
    # Declared paths resolve relative to the DECLARATION, not to the repo, so a
    # policy describing a synthetic tree is self-contained.
    base = policy_path.resolve().parent
    if argv[0] == "--fetch":
        tmp = Path(tempfile.mkdtemp())
        fetch("names.json", tmp / "names.json")
        live = norm(json.loads((tmp / "names.json").read_text()))
        if journal_path is None:
            fetch("log.jsonl", tmp / "log.jsonl")
            journal_path = str(tmp / "log.jsonl")
    else:
        live = norm(json.loads(Path(argv[0]).read_text()))

    corpus = norm(json.loads((base / policy["corpus"]).read_text()))
    first_raw = {}
    first_seen = load_journal(journal_path, first_raw)

    manifest = json.loads((base / policy["stdlib_from_manifest"]).read_text())
    ns = manifest.get("namespace", "oath")
    published = {f"{ns}/{d['name']}": d["name"]
                 for d in manifest["definitions"]
                 if d.get("membership") == "project-publication" and d.get("export")}
    demos = policy.get("demonstration_prefixes", [])
    probes = {p["name"]: p["reason"] for p in policy.get("operational_probes", [])}
    regonly = {p["name"]: p["reason"] for p in policy.get("registry_only", [])}
    aliases = policy.get("alias_prefixes", [])

    failures, reviews, counts = [], [], {}

    def tally(cat):
        counts[cat] = counts.get(cat, 0) + 1

    # ---- classification: every live name must match EXACTLY ONE category -------
    def categories_of(name):
        cats = []
        if name in corpus:
            cats.append("corpus")
        for a in aliases:
            if name.startswith(a["prefix"]):
                cats.append(f"alias:{a['prefix']}")
        for dm in demos:
            if name.startswith(dm["prefix"]):
                cats.append("demonstration")
        if name in published:
            cats.append("stdlib_publication")
        if name in probes:
            cats.append("operational_probe")
        if name in regonly:
            cats.append("registry_only")
        return cats

    def describe(name):
        """What the journal knows about a name, for a failure that must be actionable."""
        bits = [f"artifact {live.get(name,'?')[:12]}…"]
        if name in first_seen:
            seq, when, author, signed = first_seen[name]
            bits.append(f"first published at journal {seq} ({when})")
            bits.append(f"author {(author or 'unattributed')[:16]}"
                        + ("… [SIGNED — a key proved this]" if signed
                           else " [UNSIGNED — the author is a recorded label, not evidence]"))
        elif first_seen:
            bits.append("NOT FOUND in the journal — it is in names.json but no entry binds it")
        return "; ".join(bits)

    for name in sorted(live):
        cats = categories_of(name)
        if len(cats) > 1:
            failures.append(f"AMBIGUOUS `{name}`: claimed by {len(cats)} categories ({', '.join(cats)}). "
                            f"A name must belong to exactly one, or the accounting double-counts it "
                            f"and a disappearance in one category is masked by membership in another")
            tally("ambiguous")
            continue
        if not cats:
            failures.append(f"UNDECLARED live name `{name}` — {describe(name)}. "
                            f"No category matched: not in the corpus, not under a declared alias "
                            f"prefix, not a standard-library publication, not a declared probe or "
                            f"registry-only artifact. Classify it in registry-reconciliation.json — "
                            f"silence is what lets a namespace fill up with things nobody chose")
            tally("undeclared")
            continue

        cat = cats[0]
        if cat == "corpus":
            if live[name] != corpus[name]:
                failures.append(f"HASH MISMATCH on `{name}`: corpus {corpus[name][:12]}… vs live "
                                f"{live[name][:12]}… — same name, different artifact, so identity "
                                f"moved on one side only")
                tally("hash_mismatch")
            else:
                tally("corpus_present_identical")
        elif cat.startswith("alias:"):
            prefix = cat.split(":", 1)[1]
            bare = name[len(prefix):]
            if bare not in corpus:
                failures.append(f"alias `{name}` mirrors nothing: `{bare}` is not in the corpus")
                tally("undeclared")
            elif corpus[bare] != live[name]:
                failures.append(f"alias `{name}` has DRIFTED: mirrors `{bare}` (corpus "
                                f"{corpus[bare][:12]}…) but resolves to {live[name][:12]}… — an alias "
                                f"pointing elsewhere is a mismatch, not an alias")
                tally("hash_mismatch")
            else:
                tally("alias")
        elif cat == "stdlib_publication":
            bare = published[name]
            if corpus.get(bare) != live[name]:
                failures.append(f"standard-library publication `{name}` does not match its corpus "
                                f"artifact: corpus {corpus.get(bare,'(absent)')[:12]}… vs live "
                                f"{live[name][:12]}…")
                tally("hash_mismatch")
            else:
                tally("stdlib_publication")
        elif cat == "demonstration":
            tally("demonstration")
        elif cat == "operational_probe":
            reviews.append(f"operational probe `{name}` — {probes[name]}")
            tally("operational_probe")
        elif cat == "registry_only":
            reviews.append(f"registry-only artifact `{name}` — {regonly[name]}")
            tally("registry_only")

    # ---- the freeze: no NEW unowned names ------------------------------------
    # Legacy ambiguity is preserved; new ambiguity may not be created. A first
    # binding after the pinned boundary that carries no signature is a name with no
    # cryptographic principal behind it, created after the registry stopped
    # allowing that — a hard failure, not a REVIEW line, because the whole point is
    # that the category cannot grow.
    lu = policy.get("legacy_unowned")
    if lu and first_raw:
        boundary = lu["boundary_seq"]
        frozen = {n for n, r in first_raw.items()
                  if (r.get("seq") or 0) <= boundary and not r.get("author_pubkey")}
        if lu.get("expected_count") is not None and len(frozen) != lu["expected_count"]:
            failures.append(f"the frozen legacy set derives to {len(frozen)} names but the policy "
                            f"declares {lu['expected_count']}. The set is derived from immutable "
                            f"history at a pinned boundary, so this number cannot move on its own — "
                            f"either the boundary was changed or the journal is not the one this "
                            f"policy was written against")
            tally("unowned_new")
        for name, r in sorted(first_raw.items()):
            if (r.get("seq") or 0) <= boundary or r.get("author_pubkey"):
                continue
            if name not in live:
                continue
            failures.append(f"UNOWNED NEW NAME `{name}` — first bound at journal {r.get('seq')} "
                            f"({(r.get('time') or '')[:19]}) by an UNSIGNED entry labelled "
                            f"{r.get('author')!r}, after the freeze at {boundary}. New names require a "
                            f"signed PUBLICATION (an `oath publish` envelope, not merely a signed request): "
                            f"bearer authorization grants service access, not name ownership. The legacy "
                            f"set is closed and may not grow")
            tally("unowned_new")

    # ---- required membership: every category member must be PRESENT -----------
    # This is the half a count cannot express. A vanished alias and a new
    # unexplained name cancel out in any total, and only an explicit presence
    # requirement notices the disappearance.
    for name, h in sorted(corpus.items()):
        if name not in live:
            failures.append(f"corpus name `{name}` is ABSENT from the registry (artifact {h[:12]}…) "
                            f"— the corpus claims something the registry does not hold")
            tally("corpus_absent_live")

    for a in aliases:
        if a.get("mirrors") != "corpus":
            continue
        missing = [n for n in sorted(corpus) if a["prefix"] + n not in live]
        if missing:
            failures.append(f"alias prefix `{a['prefix']}` is declared a COMPLETE mirror of the "
                            f"corpus but {len(missing)} member(s) are absent live: "
                            f"{', '.join(missing[:5])}{'…' if len(missing) > 5 else ''} — a mirror "
                            f"missing entries is not a mirror, and its absence cancels against any "
                            f"new name in a total")
            tally("required_absent")

    for pub, bare in sorted(published.items()):
        if pub not in live:
            failures.append(f"standard-library publication `{pub}` is declared exported in "
                            f"{policy['stdlib_from_manifest']} but is ABSENT from the registry")
            tally("required_absent")

    for name in sorted(list(probes) + list(regonly)):
        if name not in live:
            failures.append(f"declared name `{name}` is ABSENT from the registry. The journal is "
                            f"append-only, so a declared name disappearing means the declaration is "
                            f"wrong or the store lost it — both need a human")
            tally("required_absent")

    # ---- report ---------------------------------------------------------------
    print(f"corpus   {len(corpus)} definitions   ({policy['corpus']})")
    print(f"registry {len(live)} names          ({policy['registry']})")
    print()
    print("BY CATEGORY (classification, not count — a total would let an arrival cancel a departure)")
    for cat in ("corpus_present_identical", "alias", "stdlib_publication",
                "demonstration", "operational_probe", "registry_only", "corpus_absent_live",
                "required_absent", "hash_mismatch", "ambiguous", "undeclared",
                "unowned_new"):
        if counts.get(cat):
            verdict = policy["verdicts"].get(cat, "fail")
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

    print("\nRECONCILED. Every live name classified into exactly one declared category, and every "
          "required member of every category is present at the expected artifact.")
    print("The two sets are NOT equal and are not meant to be: the corpus is what builds, "
          "the registry is what happened.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
