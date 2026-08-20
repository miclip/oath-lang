#!/usr/bin/env bash
# Instrument check for the full-derivation fingerprint gate (#139).
#
# A fingerprint that MISSES a determinism input silently skips a changed state
# and reports a false PASS — the failure this whole conformance harness exists to
# prevent. This asserts the two properties that keep the gate honest:
#   IDEMPOTENT  — an unchanged tree yields the same fingerprint (no false
#                 invalidation, which would make the gate useless);
#   SENSITIVE   — perturbing an input changes the fingerprint (no false skip).
# It covers a config override (rlimit) and a committed fixture (a file input),
# the two shapes the fingerprint reads. Run it whenever the fingerprint changes.
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
fp() { "$HERE/conformance.sh" --print-fingerprint 2>/dev/null; }
fail=0

a="$(fp)"; b="$(fp)"
[ -n "$a" ] || { echo "FAIL: fingerprint is empty"; fail=1; }
[ "$a" = "$b" ] && echo "PASS: idempotent" || { echo "FAIL: not idempotent ($a vs $b)"; fail=1; }

c="$(OATHRS_Z3_RLIMIT=424242 fp)"
[ "$a" != "$c" ] && echo "PASS: sensitive to an rlimit override" || { echo "FAIL: insensitive to OATHRS_Z3_RLIMIT — a determinism pin (§7.2) is unhashed"; fail=1; }

# A file input: the fingerprint scans all of fixtures/, so a new file there must
# change it (the gate would otherwise skip check 5 for a changed fixture set). A
# transient probe rather than perturbing committed content — this test writes
# nothing durable and is read-only over the committed store.
probe="$ROOT/fixtures/analyses/.fp-instrument-probe.tmp"
trap 'rm -f "$probe"' EXIT
: > "$probe"
d="$(fp)"
rm -f "$probe"
[ "$a" != "$d" ] && echo "PASS: sensitive to a new fixtures/ file" || { echo "FAIL: insensitive to a fixtures/ change — the gate would skip check 5 for a changed fixture"; fail=1; }

echo
[ $fail -eq 0 ] && echo "conformance_fp_test: PASS" || echo "conformance_fp_test: FAIL"
exit $fail
