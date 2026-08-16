#!/usr/bin/env python3
"""The two payloads behind docs/experiments/issue-115-premise.md.

Both are exactly 409,600 bytes of valid JSON and differ ONLY in whether the
`repository` object precedes or follows the padding.  That is the whole
experimental variable: `json-scoped-string` scans for "\\"repository\\":{" from
the front of the body, so where that key sits decides how much of the list the
scan walks -- and nothing else in the program cares.

THE LENGTHS MUST BE EQUAL OR THE COMPARISON MEANS NOTHING.  Receipt cost is
linear in body length, so two payloads of different sizes would differ for a
reason that has nothing to do with field position.  The pad is solved for rather
than guessed, and the assertion below is the control.

    python3 mkpayloads.py <outdir>     ->  repo-first.json, repo-last.json
"""
import json
import os
import sys

N = 409_600
REPO = '"repository":{"id":1,"full_name":"miclip/oath-lang"}'
TAIL = ',"action":"opened"}'


def build(repo_first: bool) -> str:
    """Solve for the pad length that makes the body exactly N bytes."""
    fixed = len('{' + REPO + ',"pad":""' + TAIL)
    padlen = N - fixed
    if padlen < 0:
        raise SystemExit("N is too small to hold the fixed structure")
    pad = 'A' * padlen
    if repo_first:
        s = '{' + REPO + ',"pad":"' + pad + '"' + TAIL
    else:
        s = '{"pad":"' + pad + '",' + REPO + TAIL
    assert len(s) == N, (len(s), N)
    json.loads(s)  # a malformed body would be refused before the scan
    return s


def main() -> None:
    outdir = sys.argv[1] if len(sys.argv) > 1 else "."
    os.makedirs(outdir, exist_ok=True)
    for name, first in (("repo-first.json", True), ("repo-last.json", False)):
        body = build(first)
        with open(os.path.join(outdir, name), "w") as fh:
            fh.write(body)
        print(f"{name}: {len(body)} bytes")


if __name__ == "__main__":
    main()
