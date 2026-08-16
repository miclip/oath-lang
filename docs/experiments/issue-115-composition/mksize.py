#!/usr/bin/env python3
"""The variable-length bodies behind the scaling control in
docs/experiments/issue-115-composition.md.

`issue-115-premise/mkpayloads.py` PINS N = 409,600 and must keep doing so: the
premise experiment's whole control is that its two payloads are equal in length,
and a generator that takes N as an argument cannot assert that. So this is a
separate entry point rather than a new flag on that one.

IT DERIVES THE BODY STRUCTURE FROM mkpayloads.py RATHER THAN RESTATING IT. Two
generators that each spell out the same JSON skeleton are two things that can
drift, and the drift would be invisible: both would keep producing valid,
correctly-sized JSON while measuring subtly different programs. `build` is
imported and called with the module's own N patched, so there is exactly one
definition of what one of these bodies IS.

THE SELF-CHECK IS THE POINT, NOT DECORATION. At N = 409,600 this must reproduce
the committed `repo-last.json` byte for byte, and it refuses to write anything if
it does not -- which is what makes the scaling table auditable against the
figures the premise document already published, instead of being a second
measurement of a second payload family.

    python3 mksize.py <outdir> [N ...]     ->  last-<N>.json for each N
"""
import importlib.util
import hashlib
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
PREMISE = os.path.join(os.path.dirname(HERE), "issue-115-premise")

_spec = importlib.util.spec_from_file_location(
    "issue115_mkpayloads", os.path.join(PREMISE, "mkpayloads.py"))
_mk = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_mk)

# sha256 of the repo-last.json the recorded figures were measured against.
REF_SHA256 = "0ac617402b93adb8c7552ea05d6ce20bab5d3dcdecb8a7b84a6342223acd2f37"
PINNED = _mk.N          # what the premise experiment measured: 409,600
DEFAULT = [102_400, 204_800, 409_600, 819_200]


def body(n: int) -> str:
    """One `repository`-last body of exactly n bytes, via mkpayloads' own build.

    Only the LAST ordering is generated. The scaling control varies LENGTH; the
    premise experiment already varies POSITION at fixed length, and mixing the
    two axes in one file is how a reader ends up unable to say which variable a
    number belongs to.
    """
    saved = _mk.N
    try:
        _mk.N = n
        return _mk.build(repo_first=False)
    finally:
        _mk.N = saved


def main() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: mksize.py <outdir> [N ...]")
    outdir = sys.argv[1]
    sizes = [int(a) for a in sys.argv[2:]] or DEFAULT

    # THE CONTROL RUNS BEFORE ANYTHING IS WRITTEN, and it runs mkpayloads' OWN
    # main() rather than re-deriving what it would have produced. The payload
    # files are build artifacts and are not committed, so there is no reference
    # on disk to compare against; generating one is the only way to make this an
    # equality check instead of a restatement. A generator that has already
    # written files by the time it finds it disagrees leaves a directory that
    # looks measurable.
    mine = body(PINNED)
    with tempfile.TemporaryDirectory() as tmp:
        argv = sys.argv
        try:
            sys.argv = ["mkpayloads.py", tmp]
            _mk.main()
        finally:
            sys.argv = argv
        with open(os.path.join(tmp, "repo-last.json"), "rb") as fh:
            ref = fh.read()
    if ref != mine.encode():
        raise SystemExit(
            "REFUSED: this generator disagrees with mkpayloads.py at N=%d, so "
            "the scaling payloads would not be comparable with the premise "
            "figures" % PINNED)
    if _mk.N != PINNED:
        raise SystemExit("REFUSED: mkpayloads.N was not restored after patching")

    # AND AN INDEPENDENT WITNESS, because the comparison above cannot fail the
    # way it is meant to: `mine` and `ref` are both produced by _mk in this same
    # run, so a change to _mk.build() or _mk.N moves BOTH sides and the equality
    # still holds while the bytes no longer match the published measurements.
    # A self-referential check is a refusal that cannot fire. The digest below
    # is of the `repo-last.json` the recorded figures were taken against, so it
    # is fixed independently of anything this program can alter.
    if len(ref) != PINNED:
        raise SystemExit(
            "REFUSED: mkpayloads produced %d bytes, not the pinned %d"
            % (len(ref), PINNED))
    got = hashlib.sha256(ref).hexdigest()
    if got != REF_SHA256:
        raise SystemExit(
            "REFUSED: repo-last.json is sha256 %s, not the %s the recorded "
            "figures were measured against — the payload has drifted and the "
            "scaling data would not be comparable" % (got, REF_SHA256))

    os.makedirs(outdir, exist_ok=True)
    for n in sizes:
        s = body(n)
        assert len(s) == n, (len(s), n)
        with open(os.path.join(outdir, "last-%d.json" % n), "w") as fh:
            fh.write(s)
        print("last-%d.json: %d bytes%s"
              % (n, len(s), "  (pinned; matches repo-last.json)"
                 if n == PINNED else ""))


if __name__ == "__main__":
    main()
