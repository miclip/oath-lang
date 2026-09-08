#!/usr/bin/env python3
"""End-to-end integration test for the §7.5 exclusion-scoped campaign (#98).

SUBPROCESS-LEVEL AND DELIBERATELY BOUNDED. It runs the REAL `oathrs` binary over
a two-file corpus at a small `n`: real `--shard` emissions, a synthesized empty
envelope for the declined shard, the real `--merge-shards`, and the harness's
adjudication. That is every piece of the mechanism, on a corpus small enough to
be a committed test.

WHAT IT IS NOT. It is NOT the conformance campaign and establishes nothing about
the real corpus — that run is 177 shards of hours each and is dispatched, not
tested. This exists because a harness that has only ever been reasoned about is a
hypothesis: the two defects this test's manual predecessor caught (an assumed
short-hash width, and a matrix edit that would have produced a cross product)
were both invisible to the unit tests.

COST: ~17 minutes, because it proves. It is deliberately OUT of the per-push
gate (`make check-campaign-exclusions`, which is prover-free) and has its own
target, `make check-campaign-integration`. Do not move it into a fast gate.

It SKIPS only when the binary or z3 is absent, and says which.
"""

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
OATHRS = os.path.join(REPO, "oathrs", "target", "release", "oathrs")
HARNESS = os.path.join(HERE, "campaign-subset.py")
CORPUS = [os.path.join(REPO, "examples", "arith.oath"),
          os.path.join(REPO, "examples", "ediv.oath")]
COLS = [1, 2, 3, 7, 8, 32, 100]


def _why_skip():
    if not os.path.exists(OATHRS):
        return (f"{OATHRS} is not built — run "
                f"`cargo build --release --manifest-path oathrs/Cargo.toml`")
    if shutil.which("z3") is None:
        return "z3 is not on PATH — the shards cannot prove without it"
    for f in CORPUS:
        if not os.path.exists(f):
            return f"{f} is missing from the corpus"
    return None


def _shard_of(h, p, n):
    return int.from_bytes(hashlib.sha256((h + "#" + str(p)).encode("ascii")).digest()[:8],
                          "big") % n


def _run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


@unittest.skipIf(_why_skip() is not None, _why_skip() or "")
class Integration(unittest.TestCase):
    """One bounded campaign, start to finish, against the real binary."""

    @classmethod
    def setUpClass(cls):
        cls.dir = tempfile.mkdtemp(prefix="oath-campaign-it-")
        env = dict(os.environ, OATHRS_Z3_MEMORY_MB="3000")
        # 1. one unsharded emission, to learn the corpus's property universe.
        p = _run([OATHRS, "prove", "--shard", "0/1", "--hints",
                  os.path.join(REPO, "fixtures", "prove", "outcomes.json")] + CORPUS, env=env)
        assert p.returncode == 0, f"probe emission failed: {p.stderr[:400]}"
        props = []
        for line in p.stdout.splitlines():
            if line.startswith("#") or line.startswith("shard\t") or line.startswith("campaign\t"):
                continue
            f = line.split("\t")
            props.append((f[0], int(f[1])))
        assert props, "the probe emission carried no attempt results"
        cls.props = props

        # 2. a seed matching THIS corpus (the committed one covers all 40 files,
        #    and every absent definition would be a legitimate mismatch).
        with open(os.path.join(REPO, "fixtures", "prove", "outcomes.json"), encoding="utf-8") as fh:
            out = json.load(fh)
        hashes = {h for h, _ in props}
        out["definitions"] = [d for d in out["definitions"] if d["hash"] in hashes]
        cls.hints = os.path.join(cls.dir, "outcomes.json")
        with open(cls.hints, "w", encoding="utf-8") as fh:
            json.dump(out, fh)

        # 3. an n that ISOLATES some property, and the artefact naming it.
        with open(os.path.join(REPO, "codebase", "names.json"), encoding="utf-8") as fh:
            rev = {v: k for k, v in json.load(fh).items()}
        chosen = None
        for n in range(3, 60):
            occ = {}
            for h, pi in props:
                occ.setdefault(_shard_of(h, pi, n), []).append((h, pi))
            solo = sorted((s, v[0]) for s, v in occ.items() if len(v) == 1)
            if solo:
                chosen = (n, solo[0][0], solo[0][1])
                break
        assert chosen, "no n in 3..59 isolates a property of this corpus"
        cls.n, cls.excluded_shard, (eh, ep) = chosen
        cls.universe = os.path.join(cls.dir, "universe.txt")
        with open(cls.universe, "w", encoding="utf-8") as fh:
            fh.write("# bounded integration corpus\n")
            fh.write("# name\thash\tprop\t" + "\t".join(f"n={c}" for c in COLS) + "\n")
            for h, pi in sorted(props):
                fh.write("\t".join([rev.get(h, "unknown-" + h[:8]), h, str(pi)]
                                   + [str(_shard_of(h, pi, c)) for c in COLS]) + "\n")
        cls.name = rev.get(eh, "unknown-" + eh[:8])
        cls.artefact = os.path.join(cls.dir, "exclusions.json")
        with open(cls.artefact, "w", encoding="utf-8") as fh:
            json.dump({"format": "oath-campaign-exclusions/v1", "n": cls.n, "exclusions": [{
                "name": cls.name, "hash": eh, "prop": ep, "prop_name": "integration",
                "shard": cls.excluded_shard,
                "reason": "bounded integration test; not a conformance exclusion",
                "return_condition": "never applies to the conformance corpus",
            }]}, fh)

        # 4. the REAL shards, for exactly the derived matrix.
        m = _run([sys.executable, HARNESS, "matrix",
                  "--universe", cls.universe, "--artefact", cls.artefact])
        assert m.returncode == 0, m.stderr
        cls.matrix = json.loads(m.stdout)
        cls.shard_dir = os.path.join(cls.dir, "shards")
        os.makedirs(cls.shard_dir)
        for i in cls.matrix:
            r = _run([OATHRS, "prove", "--shard", f"{i}/{cls.n}",
                      "--hints", cls.hints] + CORPUS, env=env)
            assert r.returncode == 0, f"shard {i} failed: {r.stderr[:400]}"
            with open(os.path.join(cls.shard_dir, f"shard-{i}.txt"), "w", encoding="utf-8") as fh:
                fh.write(r.stdout)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.dir, ignore_errors=True)

    def _merge(self):
        return _run([sys.executable, HARNESS, "merge", "--universe", self.universe,
                     "--artefact", self.artefact, "--shard-dir", self.shard_dir,
                     "--hints", self.hints, "--oathrs", OATHRS] + CORPUS)

    def test_01_the_declined_shard_is_absent_from_the_matrix(self):
        self.assertNotIn(self.excluded_shard, self.matrix)
        self.assertEqual(len(self.matrix), self.n - 1)

    def test_02_without_the_synthesized_envelope_the_merge_refuses(self):
        """The empty envelope is load-bearing, not decoration."""
        r = self._merge()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("missing emission for shard", r.stderr + r.stdout)

    def test_03_synthesize_then_merge_passes_over_the_named_subset(self):
        s = _run([sys.executable, HARNESS, "synthesize", "--universe", self.universe,
                  "--artefact", self.artefact, "--out", self.shard_dir,
                  "--campaign-from", os.path.join(self.shard_dir,
                                                  f"shard-{self.matrix[0]}.txt")])
        self.assertEqual(s.returncode, 0, s.stderr)
        r = self._merge()
        self.assertEqual(r.returncode, 0, f"stdout={r.stdout}\nstderr={r.stderr[-2000:]}")
        self.assertIn("PASS\tunion == S over the corpus MINUS 1 named exclusion(s)", r.stdout)
        self.assertIn(self.name, r.stdout)
        self.assertIn("does NOT establish the full-corpus guarantee", r.stdout)

    def test_04_the_kernel_really_reported_the_exclusion_uncovered(self):
        """The PASS must rest on the merge's own diagnostic, not on our bookkeeping."""
        r = self._merge()
        self.assertIn("was attempted by NO shard", r.stderr)
        self.assertIn("mismatch(es)", r.stderr)


if __name__ == "__main__":
    reason = _why_skip()
    if reason:
        print(f"SKIPPED: {reason}")
    unittest.main(verbosity=2)
