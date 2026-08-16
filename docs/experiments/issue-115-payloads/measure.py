#!/usr/bin/env python3
"""Measure body length and `repository` offset over the checked-in payloads.

NORMALISATION, stated because the numbers depend on it. GitHub delivers compact
JSON; the checked-in examples are pretty-printed. Each file is therefore parsed
and re-serialised with no whitespace before measuring, which is the closest
available stand-in for delivered bytes.

DO NOT PASS parse_int=str. An earlier version of this measurement did, to avoid
the 2**53 rounding that bites webhook DELIVERY IDs, and it quoted every number
in the document — inflating every byte count and shifting every offset. The ids
inside these payloads are well below 2**53, so ordinary parsing is exact here
and the guard below asserts it rather than trusting the claim.

Usage: measure.py [dir]   (defaults to this file's directory)
"""
import json
import os
import statistics
import sys


def rows(d):
    out = []
    for f in sorted(os.listdir(d)):
        if not f.endswith(".json"):
            continue
        raw = open(os.path.join(d, f), "rb").read()
        obj = json.loads(raw)
        if not isinstance(obj, dict):
            continue
        body = json.dumps(obj, separators=(",", ":"))
        # Exactness guard: re-parsing the compact form must reproduce the object.
        # If any integer had exceeded 2**53 this would not hold.
        if json.loads(body) != obj:
            raise SystemExit("REFUSED: %s does not round-trip; a numeric "
                             "literal lost precision and the byte counts "
                             "would be wrong" % f)
        off = body.find('"repository"')
        if off < 0:
            continue
        out.append((f.split("-")[0], len(body), off, 100.0 * off / len(body)))
    return out


def main():
    d = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.abspath(__file__))
    r = rows(d)
    if not r:
        raise SystemExit("REFUSED: no payloads measured; wrong directory?")
    by = {}
    for ev, ln, off, pct in r:
        by.setdefault(ev, []).append((ln, off, pct))
    print("| event | n | median bytes | median offset | median %% |")
    print("|---|---|---|---|---|")
    for ev in sorted(by):
        v = by[ev]
        print("| `%s` | %d | %d | %d | %.1f%% |" % (
            ev, len(v),
            statistics.median([x[0] for x in v]),
            statistics.median([x[1] for x in v]),
            statistics.median([x[2] for x in v])))
    lens = [x[1] for x in r]
    pcts = [x[3] for x in r]
    print("| **all** | **%d** | **%d** | — | **%.1f%%** |" % (
        len(r), statistics.median(lens), statistics.median(pcts)))
    print()
    print("range: body %d-%d bytes; repository at %.1f%%-%.1f%%" % (
        min(lens), max(lens), min(pcts), max(pcts)))
    # How many `push` examples carry no commits at all — the size-floor claim.
    n_push = n_empty = 0
    for f in sorted(os.listdir(d)):
        if not f.startswith("push-") or not f.endswith(".json"):
            continue
        o = json.loads(open(os.path.join(d, f), "rb").read())
        n_push += 1
        if not o.get("commits"):
            n_empty += 1
    print("push examples: %d, of which %d carry ZERO commits" % (n_push, n_empty))


if __name__ == "__main__":
    main()
