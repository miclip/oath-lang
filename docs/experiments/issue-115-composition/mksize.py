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
the committed `repo-last.json` AND `repo-first.json` byte for byte, and it
refuses to write anything if it does not -- which is what makes the scaling table
auditable against the figures the premise document already published, instead of
being a second measurement of a second payload family.

POSITION IS A SECOND AXIS AND IT IS QUANTIZED, NOT CONTINUOUS. `at()` builds a
body of exactly n bytes with `"repository":{` starting at a REQUESTED byte
offset, and every offset that the structure cannot express is REFUSED by name
rather than silently rounded to a nearby one -- a generator that quietly moves
the independent variable produces a table whose rows are not the rows it labels.
The two extremes route through `_mk.build` UNCHANGED, so the committed premise
payloads remain the endpoints of this axis rather than lookalikes of them.

    python3 mksize.py <outdir> [N ...]              ->  last-<N>.json per N
    python3 mksize.py <outdir> --cells [N ...]      ->  the size x position grid
    python3 mksize.py <outdir> --offset <O> [N ...] ->  at-<N>-<O>.json per N
                                 O is first | mid | last | a byte offset
"""
import importlib.util
import hashlib
import json
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
PREMISE = os.path.join(os.path.dirname(HERE), "issue-115-premise")

_spec = importlib.util.spec_from_file_location(
    "issue115_mkpayloads", os.path.join(PREMISE, "mkpayloads.py"))
_mk = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_mk)

# sha256 of the two payloads the recorded figures were measured against.
REF_SHA256 = "0ac617402b93adb8c7552ea05d6ce20bab5d3dcdecb8a7b84a6342223acd2f37"
REF_FIRST_SHA256 = "8d6553f5061c7219b5a6057a514aee684568e6aeef946038cdb6a800cbbb206c"
PINNED = _mk.N          # what the premise experiment measured: 409,600
DEFAULT = [102_400, 204_800, 409_600, 819_200]

# The grid. Its SIZES are the endpoints of the committed scaling sweep plus the
# pinned size, so the grid spans the same factor of eight the linearity control
# already covers and shares one cell with it; its POSITIONS are the two orderings
# the premise experiment measured plus the midpoint that neither axis has ever
# visited. Both are overridable from the command line precisely because they are
# a choice and not a derivation.
CELL_SIZES = [102_400, 409_600, 819_200]
CELL_POSITIONS = ["first", "mid", "last"]

KEY = '"repository":{'
# The ONE piece of body structure this file adds. mkpayloads has a pad BEFORE
# `repository` (last) or AFTER it (first) and never both, so an interior offset
# needs a second pad segment that no committed generator spells. It is written
# here once, and only ever reached for offsets that are neither extreme.
MID_OPEN = ',"pad2":"'
MID_CLOSE = '"'


def _build(n: int, repo_first: bool) -> str:
    """mkpayloads' OWN build at size n, with its module-level N restored after."""
    saved = _mk.N
    try:
        _mk.N = n
        return _mk.build(repo_first=repo_first)
    finally:
        _mk.N = saved


def first_offset() -> int:
    """Where `repository` starts in a `repo`-first body: immediately after `{`."""
    return 1


def last_offset(n: int) -> int:
    """Where `repository` starts in a `repo`-last body of n bytes."""
    return n - len(_mk.REPO) - len(_mk.TAIL)


def feasible(n: int):
    """The offsets a body of exactly n bytes can actually place the key at.

    Returned as (extremes, interior_lo, interior_hi). The gaps are real: opening
    a pad segment costs a fixed number of structural bytes, so offsets 2..9 and
    the nine below `last` cannot be hit at all. They are reported rather than
    approximated.
    """
    lo = len('{"pad":"') + len('",')    # smallest interior offset: empty pre-pad
    hi = last_offset(n) - len(MID_OPEN) - len(MID_CLOSE)   # empty post-pad
    return (first_offset(), last_offset(n)), lo, hi


def body(n: int, offset=None) -> str:
    """One body of exactly n bytes with `"repository":{` at `offset`.

    `offset=None` keeps the original meaning of this function -- the LAST
    ordering, which is what the scaling control varies length over.

    The two extremes are produced by `_mk.build` itself, unchanged, so this
    function cannot drift from the premise payloads at the points where the two
    families are supposed to coincide. Only the interior is spliced, and it is
    spliced out of `_mk.build`'s own output rather than re-spelled.
    """
    if offset is None:
        offset = last_offset(n)
    if offset == first_offset():
        return _build(n, repo_first=True)
    if offset == last_offset(n):
        return _build(n, repo_first=False)

    (_, _), lo, hi = feasible(n)
    if not (lo <= offset <= hi):
        raise ValueError(
            "offset %d is not expressible in a %d-byte body: the feasible "
            "offsets are %d (repo-first), %d..%d (interior) and %d (repo-last)"
            % (offset, n, first_offset(), lo, hi, last_offset(n)))

    base = _build(n, repo_first=False)
    head = base[:len('{"pad":"') + (offset - lo)] + '",'
    tail_pad = 'A' * (last_offset(n) - offset - len(MID_OPEN) - len(MID_CLOSE))
    return head + _mk.REPO + MID_OPEN + tail_pad + MID_CLOSE + _mk.TAIL


def check(s: str, n: int, offset: int) -> None:
    """Every body is validated on BOTH axes before it reaches a file.

    Length alone would pass a body whose key moved, and offset alone would pass
    one that is the wrong size -- and each is exactly the variable the other
    table column is labelled with. The key must also occur EXACTLY once: a pad
    that happened to contain the key would give a scan a second, earlier stop
    and the measured position would not be the requested one.
    """
    if len(s) != n:
        raise SystemExit("REFUSED: body is %d bytes, requested %d" % (len(s), n))
    if s.count(KEY) != 1:
        raise SystemExit(
            "REFUSED: %r occurs %d times, not once" % (KEY, s.count(KEY)))
    got = s.index(KEY)
    if got != offset:
        raise SystemExit(
            "REFUSED: %r starts at byte %d, not the requested %d"
            % (KEY, got, offset))
    json.loads(s)   # a malformed body would be refused before the scan


def resolve(spec: str, n: int) -> int:
    """`first`/`mid`/`last`, or a literal byte offset, as an offset into n bytes.

    `mid` is the midpoint of the FEASIBLE interior, not of the body: n//2 would
    name an offset the structure cannot express for small n, and the whole point
    of the named positions is that they are always buildable.
    """
    if spec == "first":
        return first_offset()
    if spec == "last":
        return last_offset(n)
    if spec == "mid":
        _, lo, hi = feasible(n)
        if hi < lo:
            raise SystemExit("REFUSED: %d bytes has no interior offset" % n)
        return (lo + hi) // 2
    return int(spec)


def selfcheck() -> None:
    """THE CONTROL RUNS BEFORE ANYTHING IS WRITTEN.

    It runs mkpayloads' OWN main() rather than re-deriving what it would have
    produced. The payload files are build artifacts and are not committed, so
    there is no reference on disk to compare against; generating one is the only
    way to make this an equality check instead of a restatement. A generator
    that has already written files by the time it finds it disagrees leaves a
    directory that looks measurable.

    BOTH EXTREMES ARE CHECKED, not just `repo-last`. Position is now an axis of
    this generator, and its two endpoints are supposed to BE the premise
    payloads; checking one endpoint would leave the other free to drift while
    the refusal still reported a clean run.
    """
    mine = {"repo-last.json": body(PINNED, last_offset(PINNED)),
            "repo-first.json": body(PINNED, first_offset())}
    refs = {}
    with tempfile.TemporaryDirectory() as tmp:
        argv = sys.argv
        try:
            sys.argv = ["mkpayloads.py", tmp]
            _mk.main()
        finally:
            sys.argv = argv
        for name in mine:
            with open(os.path.join(tmp, name), "rb") as fh:
                refs[name] = fh.read()
    for name, ref in refs.items():
        if ref != mine[name].encode():
            raise SystemExit(
                "REFUSED: this generator disagrees with mkpayloads.py on %s at "
                "N=%d, so the payloads would not be comparable with the premise "
                "figures" % (name, PINNED))
    if _mk.N != PINNED:
        raise SystemExit("REFUSED: mkpayloads.N was not restored after patching")

    # AND AN INDEPENDENT WITNESS, because the comparison above cannot fail the
    # way it is meant to: `mine` and `refs` are both produced by _mk in this same
    # run, so a change to _mk.build() or _mk.N moves BOTH sides and the equality
    # still holds while the bytes no longer match the published measurements.
    # A self-referential check is a refusal that cannot fire. The digests below
    # are of the payloads the recorded figures were taken against, so they are
    # fixed independently of anything this program can alter.
    for name, want in (("repo-last.json", REF_SHA256),
                       ("repo-first.json", REF_FIRST_SHA256)):
        ref = refs[name]
        if len(ref) != PINNED:
            raise SystemExit(
                "REFUSED: mkpayloads produced %d bytes for %s, not the pinned %d"
                % (len(ref), name, PINNED))
        got = hashlib.sha256(ref).hexdigest()
        if got != want:
            raise SystemExit(
                "REFUSED: %s is sha256 %s, not the %s the recorded figures were "
                "measured against — the payload has drifted and the data would "
                "not be comparable" % (name, got, want))


def guard_outdir(outdir: str) -> None:
    """These payloads are BUILD ARTIFACTS and must not be committed.

    Enforced on the ACT rather than on the filename: a naming convention is one
    `git add -A` away from being ignored, and 800 KiB of 'A' in the history is
    not removable afterwards. The check is by repository IDENTITY, not by string
    prefix, so a symlinked or relative path into the tree is still caught.
    """
    try:
        root = subprocess.run(
            ["git", "-C", HERE, "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return          # not a checkout at all; nothing to protect
    real = os.path.realpath(outdir)
    if real == os.path.realpath(root) or real.startswith(
            os.path.realpath(root) + os.sep):
        raise SystemExit(
            "REFUSED: %s is inside the repository at %s. These payloads are "
            "build artifacts, are not committed, and are megabytes of padding; "
            "write them to a temporary directory instead." % (outdir, root))


def main() -> None:
    args = sys.argv[1:]
    if not args:
        raise SystemExit(
            "usage: mksize.py <outdir> [N ...]\n"
            "       mksize.py <outdir> --cells [N ...]\n"
            "       mksize.py <outdir> --offset <first|mid|last|BYTES> [N ...]")
    outdir, args = args[0], args[1:]

    mode, offspec = "sizes", None
    if args and args[0] == "--cells":
        mode, args = "cells", args[1:]
    elif args and args[0] == "--offset":
        if len(args) < 2:
            raise SystemExit("--offset needs first|mid|last|BYTES")
        mode, offspec, args = "offset", args[1], args[2:]
    sizes = [int(a) for a in args]

    guard_outdir(outdir)
    selfcheck()

    if mode == "cells":
        plan = [("cell-%d-%s.json" % (n, p), n, p)
                for n in (sizes or CELL_SIZES) for p in CELL_POSITIONS]
    elif mode == "offset":
        plan = [("at-%d-%s.json" % (n, offspec), n, offspec)
                for n in (sizes or DEFAULT)]
    else:
        plan = [("last-%d.json" % n, n, "last") for n in (sizes or DEFAULT)]

    # THE WHOLE GRID IS BUILT AND VALIDATED BEFORE ANY FILE IS OPENED, for the
    # same reason the self-check runs first: feasibility depends on SIZE, so a
    # cell that cannot be built is discovered part-way through a run that has
    # already written its earlier cells. A directory holding six of nine cells
    # is not a failed run, it is a run that looks measurable — and a table built
    # from it silently reports a grid it does not have.
    built = []
    for name, n, spec in plan:
        try:
            off = resolve(spec, n)
            s = body(n, off)
        except ValueError as e:
            raise SystemExit("REFUSED: %s: %s" % (name, e))
        check(s, n, off)            # BOTH axes, on every body, before writing
        built.append((name, n, off, s))

    os.makedirs(outdir, exist_ok=True)
    for name, n, off, s in built:
        with open(os.path.join(outdir, name), "w") as fh:
            fh.write(s)
        note = ""
        if n == PINNED and off == last_offset(n):
            note = "  (pinned; matches repo-last.json)"
        elif n == PINNED and off == first_offset():
            note = "  (matches repo-first.json)"
        print("%s: %d bytes, %r at %d%s" % (name, len(s), KEY, off, note))


if __name__ == "__main__":
    main()
