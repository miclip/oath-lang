#!/usr/bin/env python3
"""SPEC §7.5 EXCLUSION-SCOPED sharded campaign (#98).

The full-corpus `union == S` campaign has never completed: one property,
`gh-counts` prop 1, MEASURED at 218 minutes for a single seeded attempt
sequence and a property is INDIVISIBLE, so no shard count reaches below it.
This harness runs the campaign over the corpus MINUS a NAMED set of properties
and reports exactly what it did not cover.

WHAT THIS BUYS AND WHAT IT COSTS. It buys a campaign that RUNS. It costs the
full-corpus guarantee: while the exclusion set is non-empty the campaign does
NOT establish `F(S) = S` over the whole corpus, only over the corpus minus the
named exclusions. Every reporting path here says so; a pass that does not name
what it skipped is the defect this whole issue is about.

THE ASSIGNMENT RULE IS NOT REIMPLEMENTED ON TRUST. `shard_of` below is checked
against every value pinned in `fixtures/prove/shards.txt` before it is used
(`verify_rule`). The fixture is the authority (SPEC §7.5: "Only a pinned
expectation computed OUTSIDE any kernel can say so"); this file is a consumer of
it, and refuses to run if the two disagree.

THE CAMPAIGN IDENTITY IS COPIED, NEVER RECOMPUTED. A synthesized empty envelope
takes its `campaign` line verbatim from a real emission of the same run. A second
implementation of `campaign_identity` here could drift from the kernel's and
would be accepted by a merge that agreed with itself — the exact shape of defect
SPEC §7.5 warns about for the assignment rule.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys

HERE_DIR = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE_DIR)
ARTEFACT = os.path.join(HERE_DIR, "campaign-exclusions.json")
SHARDS_TXT = os.path.join(REPO, "fixtures", "prove", "shards.txt")
FORMAT = "oath-campaign-exclusions/v1"


class Bad(Exception):
    """A refusal. Every one is a hard stop; none is a warning."""


# --------------------------------------------------------------------------
# the partition rule, and the fixture that owns it
# --------------------------------------------------------------------------

def shard_of(h: str, p: int, n: int) -> int:
    """SPEC §7.5: first_64_bits(SHA-256(h ++ "#" ++ decimal(p))) mod n."""
    d = hashlib.sha256((h + "#" + str(p)).encode("ascii")).digest()
    return int.from_bytes(d[:8], "big") % n


def load_universe(path: str = SHARDS_TXT):
    """The corpus's property universe, and the pinned columns, from shards.txt."""
    rows, pinned_ns = [], None
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            if line.startswith("#"):
                m = re.match(r"#\s*name\t", line)
                if m:
                    cols = line.rstrip("\n").split("\t")
                    pinned_ns = [int(c.split("=")[1]) for c in cols[3:]]
                continue
            if not line.strip():
                continue
            f = line.rstrip("\n").split("\t")
            if len(f) < 4:
                raise Bad(f"malformed row in {path}: {line!r}")
            rows.append((f[0], f[1], int(f[2]), [int(x) for x in f[3:]]))
    if not rows:
        raise Bad(f"{path} pinned no properties; the universe would be empty")
    if not pinned_ns:
        raise Bad(f"{path} has no column header; the pinned n values are unknown")
    return rows, pinned_ns


def verify_rule(rows, pinned_ns) -> int:
    """Refuse to run unless shard_of reproduces EVERY pinned value."""
    checked = 0
    for name, h, p, vals in rows:
        if len(vals) != len(pinned_ns):
            raise Bad(f"{name} prop {p}: {len(vals)} columns for {len(pinned_ns)} pinned n values")
        for n, want in zip(pinned_ns, vals):
            got = shard_of(h, p, n)
            if got != want:
                raise Bad(
                    f"assignment rule disagrees with {SHARDS_TXT}: {name} prop {p} "
                    f"at n={n} computes {got}, fixture pins {want}"
                )
            checked += 1
    if checked == 0:
        raise Bad("the rule check compared nothing; it would pass vacuously")
    return checked


# --------------------------------------------------------------------------
# the exclusion artefact
# --------------------------------------------------------------------------

def load_exclusions(path: str = ARTEFACT):
    with open(path, "rb") as fh:
        raw = fh.read()
    digest = hashlib.sha256(raw).hexdigest()
    try:
        doc = json.loads(raw.decode("utf-8"))
    except Exception as e:
        raise Bad(f"{path} is not valid JSON: {e}")
    if doc.get("format") != FORMAT:
        raise Bad(f"{path}: format is {doc.get('format')!r}, expected {FORMAT!r}")
    if not isinstance(doc.get("n"), int) or doc["n"] < 1:
        raise Bad(f"{path}: n must be an integer >= 1")
    ex = doc.get("exclusions")
    if not isinstance(ex, list):
        raise Bad(f"{path}: exclusions must be a list")
    seen = set()
    out = []
    for e in ex:
        for k in ("name", "hash", "prop", "prop_name", "reason", "return_condition"):
            if k not in e:
                raise Bad(f"{path}: an exclusion is missing {k!r} — every entry must carry "
                          f"the property, the REASON and the CONDITION under which it returns")
            if isinstance(e[k], str) and not e[k].strip():
                raise Bad(f"{path}: {e.get('name')} prop {e.get('prop')}: {k!r} is empty")
            if k in ("reason", "return_condition", "prop_name", "name") and not isinstance(e[k], str):
                # §7.5 requires a REASON and a RETURN CONDITION, which are text a
                # reader acts on. A null, a number or an object satisfies "not
                # empty" and says nothing, so the type is part of the requirement.
                raise Bad(f"{path}: {e.get('name')} prop {e.get('prop')}: {k!r} must be a "
                          f"non-empty string, got {type(e[k]).__name__}")
        h = e["hash"]
        if not (isinstance(h, str) and len(h) == 64 and all(c in "0123456789abcdef" for c in h)):
            raise Bad(f"{path}: {e['name']}: hash is not 64 lowercase hex characters")
        if not isinstance(e["prop"], int) or e["prop"] < 0:
            raise Bad(f"{path}: {e['name']}: prop must be a non-negative integer")
        key = (h, e["prop"])
        if key in seen:
            raise Bad(f"{path}: duplicate exclusion for {e['name']} prop {e['prop']}")
        seen.add(key)
        out.append(e)
    return doc["n"], out, digest


def resolve(n_req, exclusions, rows):
    """Excluded shards, with the refusals that make the narrowing sound."""
    universe = {(h, p): name for name, h, p, _ in rows}
    for e in exclusions:
        key = (e["hash"], e["prop"])
        if key not in universe:
            raise Bad(f"STALE EXCLUSION: {e['name']} prop {e['prop']} ({e['hash'][:12]}) is not a "
                      f"property of this corpus — it was removed, renamed or rehashed. Delete the "
                      f"entry (and its pin) rather than carrying it.")
        if universe[key] != e["name"]:
            raise Bad(f"STALE EXCLUSION: {e['hash'][:12]} prop {e['prop']} is named "
                      f"{universe[key]!r} in the corpus, not {e['name']!r}")
        want = shard_of(e["hash"], e["prop"], n_req)
        if "shard" in e and e["shard"] != want:
            raise Bad(f"{e['name']} prop {e['prop']}: artefact records shard {e['shard']}, "
                      f"the rule computes {want} at n={n_req}")
    excluded_props = {(e["hash"], e["prop"]) for e in exclusions}
    members = {}
    for name, h, p, _ in rows:
        members.setdefault(shard_of(h, p, n_req), []).append((name, h, p))
    excluded_shards = set()
    for e in exclusions:
        s = shard_of(e["hash"], e["prop"], n_req)
        in_scope = [m for m in members[s] if (m[1], m[2]) not in excluded_props]
        if in_scope:
            raise Bad(
                f"UNSOUND EXCLUSION: shard {s} (n={n_req}) holds {e['name']} prop {e['prop']} "
                f"but ALSO holds in-scope propert(ies) {[(m[0], m[2]) for m in in_scope]}. A shard "
                f"is the unit a campaign can decline to run, so excluding this one would silently "
                f"drop those too. Choose an n that isolates the exclusion."
            )
        excluded_shards.add(s)
    # A CAMPAIGN WITH NO IN-SCOPE WORK IS REFUSED HERE, NOT DISCOVERED LATER.
    # THE UNIVERSE IS PROPERTIES, NOT SHARD INDICES, and the first version of
    # this guard got that wrong: it asked whether every shard index was
    # excluded, which is false whenever the corpus is smaller than n — one
    # excluded property at n=177 leaves 176 EMPTY shards running and a merge
    # free to report a scoped pass over nothing at all. The claim being
    # protected is about work, so the question is whether any property remains.
    in_scope_total = sum(1 for _, h, p, _ in rows if (h, p) not in excluded_props)
    if in_scope_total == 0:
        raise Bad(f"every property in the universe is excluded: the campaign would attempt "
                  f"nothing, and a merge over no work is not a narrowed claim, it is no claim.")
    if len(excluded_shards) >= n_req:
        raise Bad(f"all {n_req} shard(s) are excluded: the campaign has no real emission to "
                  f"take its campaign identity from.")
    return excluded_shards, members


# --------------------------------------------------------------------------
# reporting
# --------------------------------------------------------------------------

def describe(exclusions):
    if not exclusions:
        return "no exclusions — the claim is the FULL corpus"
    return "; ".join(f"{e['name']} prop {e['prop']} ({e['prop_name']})" for e in exclusions)


def subset_identity(base_campaign: str, digest: str) -> str:
    """Bind the base campaign identity to the exclusion set that scoped it.

    Returned in FULL. A truncated identity is a smaller space than the thing it
    identifies, and this one is reported as the identity of a verification.
    """
    return hashlib.sha256(
        (base_campaign + "\n" + FORMAT + "\n" + digest).encode("ascii")
    ).hexdigest()


# --------------------------------------------------------------------------
# envelope synthesis
# --------------------------------------------------------------------------

BANNER = ("# oath-sharded-verification/v2 — CONTRIBUTION ONLY (shard {i} of {n}); "
          "NOT verified until merged with `oath prove --merge-shards`\n")


def campaign_of(path: str) -> str:
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            if line.startswith("campaign\t"):
                return line.rstrip("\n").split("\t", 1)[1]
    raise Bad(f"{path} carries no `campaign` line; it is not a shard emission")


def synthesize(shard: int, n: int, campaign: str) -> str:
    """An emission carrying NO attempt results — the wire form of 'not run'.

    It is a real emission in every other respect, so the merge applies its
    ordinary campaign-identity, range and duplicate checks to it unchanged.
    """
    return BANNER.format(i=shard, n=n) + f"shard\t{shard}\t{n}\ncampaign\t{campaign}\n"


# --------------------------------------------------------------------------
# merge adjudication
# --------------------------------------------------------------------------

NO_SHARD = re.compile(
    r"^\s*PARTITION: (?P<name>\S+) \((?P<short>[0-9a-f]{6,})\) prop (?P<prop>\d+) "
    r"was attempted by NO shard"
)

# The merge states how many mismatches it found. Parsing lines WITHOUT checking
# that total would let a truncated, wrapped or filtered stderr hide a mismatch
# and still look like a clean exclusion-only failure.
DECLARED = re.compile(r"does NOT equal the seed S \((?P<count>\d+) mismatch\(es\)\)")

# The merge's own exit codes: 0 = verified, 1 = a stated failure. Anything else
# — a signal (128+n or a negative from subprocess), a panic, an abort — means the
# process did not reach a verdict, and its stderr is not a mismatch report to be
# adjudicated. Accepting one would turn a crash into a pass.
NORMAL_RCS = (0, 1)


def adjudicate(rc, stderr, exclusions):
    """Accept the merge's failure ONLY when the mismatch set is exactly the exclusions.

    Any other mismatch — a differing verdict, a double-attempted property, an
    unexpected uncovered one — is a real failure and is re-raised untouched.
    """
    # The kernel's short-hash WIDTH is its own affair (it prints 8 today), so match
    # by PREFIX against the artefact's full 64-character identity rather than
    # guessing a length — a guessed width fails as a false mismatch, which is the
    # safe direction but still a bug, and it fired once before this comment existed.
    if rc not in NORMAL_RCS:
        return False, (f"the merge exited {rc}, which is neither 0 (verified) nor 1 (a stated "
                       f"failure) — it did not reach a verdict, so there is nothing to "
                       f"adjudicate. Its stderr is NOT a mismatch report.")
    # The artefact's NAME is part of the match, not decoration: two definitions can
    # share a short-hash prefix, and a prefix-only match would then accept a
    # DIFFERENT property's uncovered diagnostic as though it were the named one.
    want = {(e["hash"], e["prop"], e["name"]) for e in exclusions}
    lines = [l for l in stderr.splitlines() if l.strip().startswith("PARTITION:")
             or "  " == l[:2] and l.strip()]
    mism = [l for l in stderr.splitlines() if l.startswith("  ") and l.strip()]
    if rc == 0:
        if not exclusions:
            return True, "merge PASSED over the full corpus"
        return False, ("the merge PASSED although %d propert(ies) were excluded and never "
                       "attempted — the exclusion set is stale or an emission covered it "
                       "anyway; investigate rather than accept" % len(exclusions))
    decl = DECLARED.search(stderr)
    if not decl:
        return False, ("the merge failed but reported no mismatch count — this is a failure "
                       "BEFORE the self-check (a malformed emission, a campaign-identity "
                       "rejection, a missing shard), not a coverage result:\n" + stderr.strip())
    declared = int(decl.group("count"))
    if declared != len(mism):
        return False, (f"the merge declared {declared} mismatch(es) but {len(mism)} diagnostic "
                       f"line(s) were parsed — the report is truncated or reshaped, and a "
                       f"mismatch may be hidden. Refusing to adjudicate it.")
    matched, foreign, ambiguous = set(), [], []
    for l in mism:
        m = NO_SHARD.match(l)
        if not m:
            foreign.append(l.strip())
            continue
        short, prop, name = m.group("short"), int(m.group("prop")), m.group("name")
        hits = [k for k in want if k[0].startswith(short) and k[1] == prop and k[2] == name]
        if len(hits) == 1:
            matched.add(hits[0])
        elif not hits:
            foreign.append(l.strip())
        else:
            ambiguous.append(l.strip())
    if foreign:
        return False, ("the merge reported mismatches that are NOT the named exclusions:\n  "
                       + "\n  ".join(foreign))
    if ambiguous:
        return False, ("a reported short hash matched more than one exclusion; the artefact is "
                       "ambiguous at this width:\n  " + "\n  ".join(ambiguous))
    if matched != want:
        missing = sorted(k[2] + " prop " + str(k[1]) for k in want - matched)
        return False, ("the uncovered set is not the exclusion set — named but NOT reported "
                       "uncovered: " + ", ".join(missing))
    return True, "the merge's only mismatches are exactly the named exclusions"


# --------------------------------------------------------------------------
# commands
# --------------------------------------------------------------------------

def cmd_check(a):
    rows, pinned = load_universe(a.universe)
    checked = verify_rule(rows, pinned)
    n, ex, digest = load_exclusions(a.artefact)
    if a.n is not None and a.n != n:
        raise Bad(f"--n {a.n} but the artefact is pinned to n={n}; an exclusion is only sound "
                  f"at the n that isolates it")
    shards, members = resolve(n, ex, rows)
    print(f"universe: {a.universe}")
    print(f"assignment rule checked against {a.universe}: {checked} pinned value(s), all agree")
    print(f"corpus universe: {len(rows)} properties; n={n}; shards holding work: {len(members)}")
    print(f"exclusion artefact sha256: {digest}")
    print(f"exclusions ({len(ex)}): {describe(ex)}")
    print(f"excluded shard(s): {sorted(shards)}")
    print(f"executed shards: {n - len(shards)} of {n}")
    for e in ex:
        print(f"  - {e['name']} prop {e['prop']} ({e['prop_name']}) -> shard "
              f"{shard_of(e['hash'], e['prop'], n)}")
        print(f"      reason: {e['reason']}")
        print(f"      returns when: {e['return_condition']}")
    if ex:
        print("SCOPE: union == S would hold over the corpus MINUS the above; the FULL-corpus "
              "guarantee is NOT established.")
    return 0


def cmd_matrix(a):
    rows, pinned = load_universe(a.universe)
    verify_rule(rows, pinned)
    n, ex, _ = load_exclusions(a.artefact)
    shards, _ = resolve(n, ex, rows)
    print(json.dumps([i for i in range(n) if i not in shards]))
    return 0


def cmd_excluded(a):
    rows, pinned = load_universe(a.universe)
    verify_rule(rows, pinned)
    n, ex, _ = load_exclusions(a.artefact)
    shards, _ = resolve(n, ex, rows)
    print(json.dumps(sorted(shards)))
    return 0


def cmd_synthesize(a):
    rows, pinned = load_universe(a.universe)
    verify_rule(rows, pinned)
    n, ex, _ = load_exclusions(a.artefact)
    shards, _ = resolve(n, ex, rows)
    campaign = campaign_of(a.campaign_from)
    os.makedirs(a.out, exist_ok=True)
    for s in sorted(shards):
        p = os.path.join(a.out, f"shard-{s}.txt")
        # SYNTHESIS NEVER OVERWRITES. An emission already sitting here for an
        # EXCLUDED shard means that shard actually ran — a matrix or config
        # error — and replacing it with an empty envelope would erase the only
        # evidence that an excluded property was attempted, letting the merge
        # report a scoped pass over a campaign that broke its own coverage rule
        # (§7.5: each member of E MUST be attempted by NO shard). Refuse and
        # keep the evidence.
        if os.path.exists(p):
            raise Bad(f"shard {s} is EXCLUDED but {p} already exists: that shard was executed. "
                      f"Synthesis will not overwrite it — an excluded property was attempted, "
                      f"which is a campaign disagreeing with its own scope. Investigate the "
                      f"matrix rather than replacing the emission.")
        with open(p, "w", encoding="utf-8") as fh:
            fh.write(synthesize(s, n, campaign))
        print(f"synthesized empty envelope for excluded shard {s}: {p}")
    if not shards:
        print("no excluded shards; nothing synthesized")
    return 0


def cmd_merge(a):
    rows, pinned = load_universe(a.universe)
    verify_rule(rows, pinned)
    n, ex, digest = load_exclusions(a.artefact)
    shards, _ = resolve(n, ex, rows)
    args = []
    for i in range(n):
        p = os.path.join(a.shard_dir, f"shard-{i}.txt")
        if not os.path.exists(p):
            raise Bad(f"missing emission for shard {i}: {p}")
        args += ["--shard-in", p]
    base = campaign_of(os.path.join(a.shard_dir, "shard-%d.txt" %
                                    next(i for i in range(n) if i not in shards)))
    cmd = [a.oathrs, "prove", "--merge-shards", str(n), "--hints", a.hints] + args + a.corpus
    proc = subprocess.run(cmd, capture_output=True, text=True)
    sys.stderr.write(proc.stderr)
    ok, why = adjudicate(proc.returncode, proc.stderr, ex)
    sub = subset_identity(base, digest)
    if not ok:
        print(f"FAIL\tsubset campaign\t{why}")
        return 1
    if not ex:
        print(f"PASS\tunion == S over the FULL corpus\tcampaign {base}")
        return 0
    print(f"PASS\tunion == S over the corpus MINUS {len(ex)} named exclusion(s): {describe(ex)}"
          f"\tsubset-campaign {sub}\tbase-campaign {base}")
    print("SCOPE: this run does NOT establish the full-corpus guarantee. "
          f"Not covered: {describe(ex)}.")
    return 0


def cmd_n(a):
    n, _, _ = load_exclusions(a.artefact)
    print(n)
    return 0


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    for sp in ():
        pass
    c = sub.add_parser("check", help="validate the artefact and the partition (no prover)")
    c.add_argument("--n", type=int, default=None)
    c.set_defaults(fn=cmd_check)
    m = sub.add_parser("matrix", help="print the shard indices to execute, as JSON")
    m.set_defaults(fn=cmd_matrix)
    e = sub.add_parser("excluded-shards", help="print the excluded shard indices, as JSON")
    e.set_defaults(fn=cmd_excluded)
    # THE PARTITION SIZE HAS ONE AUTHORITY, AND IT IS THE ARTEFACT. Without this
    # the workflow spelled `n` a second time in the worker command, so moving the
    # artefact to a different n left the matrix and the merge reading the new
    # value while every worker still proved against the old one — indices past
    # the end failing outright, smaller changes producing emissions the merge
    # rejects on partition identity. Updating the pin does not catch it: the pin
    # guards the artefact's bytes, not a copy of one of its fields living in
    # another file.
    nn = sub.add_parser("n", help="print the partition size the artefact declares")
    nn.set_defaults(fn=cmd_n)
    s = sub.add_parser("synthesize", help="write empty envelopes for the excluded shards")
    s.add_argument("--out", required=True)
    s.add_argument("--campaign-from", required=True,
                   help="a REAL emission of this campaign; its campaign id is copied verbatim")
    s.set_defaults(fn=cmd_synthesize)
    g = sub.add_parser("merge", help="run --merge-shards and adjudicate against the exclusions")
    g.add_argument("--shard-dir", required=True)
    g.add_argument("--hints", default=os.path.join(REPO, "fixtures", "prove", "outcomes.json"))
    g.add_argument("--oathrs", default=os.path.join(REPO, "oathrs", "target", "release", "oathrs"))
    g.add_argument("corpus", nargs="+")
    g.set_defaults(fn=cmd_merge)
    for sp in (c, m, e, s, g, nn):
        # The universe DEFAULTS to the pinned fixture, which is its authority. The
        # seam exists so the harness can be run end to end without a multi-hour
        # campaign; every report prints the path it used, so a wrong one is visible
        # rather than silent.
        sp.add_argument("--universe", default=SHARDS_TXT,
                        help="property universe (default: the pinned fixtures/prove/shards.txt)")
        sp.add_argument("--artefact", default=ARTEFACT,
                        help="exclusion artefact (default: scripts/campaign-exclusions.json)")
    a = ap.parse_args(argv)
    try:
        return a.fn(a)
    except Bad as err:
        print(f"REFUSED: {err}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
