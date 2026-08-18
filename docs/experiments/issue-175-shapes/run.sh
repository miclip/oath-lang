#!/bin/sh
# The instrument behind docs/experiments/issue-175-shapes.md.
#
#   run.sh          re-ask every scored query against the committed corpus
#
# WHAT IS AND IS NOT REPRODUCED HERE. This re-runs the QUERIES and prints what
# each mode answers, which is the decidable half: whether a shape reaches its
# artifact is a fact about the tool and the corpus, and it is what the
# documentation's claims rest on. It does NOT reproduce the study's SCORING,
# because the queries were written by someone who already knew each target — the
# limit the record states in full, and the reason the issue is left open.
#
# TIMING: --implies proves, so it is slow where nothing proves. The fragment
# probes at the end are the expensive pair; intent 3a alone ran over ten minutes
# because every candidate burns the full rlimit before answering NO VERDICT.
# Budget ~20 minutes for a whole run.
#
# THE COMMITTED STORE IS NOT TOUCHED: everything runs against a temporary copy.
set -eu
root=$(git rev-parse --show-toplevel)
here=$(dirname "$0")
( cd "$root" && make build >/dev/null )
store=$(mktemp -d); cp -R "$root/codebase/"* "$store/"
export OATH_STORE="$store"

echo "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ Makefile codebase/ docs/experiments/issue-175-shapes/)" ] || echo ' +MODIFIED')"
echo

ask() {  # ask <mode> <file> <one-line note> [extra flag]
  echo "=== $2  [$1${4:+ $4}]"
  echo "    $3"
  # STATUS FROM THE COMMAND, NOT FROM THE PIPELINE. A POSIX pipeline reports its
  # LAST stage, so `oath find ... | sed` returns sed's status and `set -e` never
  # fires — and the first-line suppression below would turn a one-line error
  # into an apparently empty result, which reads exactly like "found nothing".
  # An instrument that cannot tell a crash from a negative result is worse than
  # none, so the answer is captured first and the status checked before it is
  # shown.
  if ! out=$("$root/oath/oath" find "$1" "$here/$2" ${4:+"$4"} 2>&1); then
    echo "FAILED: oath find $1 $2 exited nonzero; this run records nothing" >&2
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    exit 1
  fi
  # The whole answer, indented. NOT filtered to the lines that agree with the
  # write-up: a probe that shows only what it expects cannot report a change.
  printf '%s\n' "$out" | sed -n '2,$p' | sed 's/^/  /'
  echo
}

# THE POSITIVE CONTROLS COME FIRST. The write-up's headline is a NUMERATOR
# (5 of 7), and a reproduction that runs only the intents that CHANGED cannot
# verify it — nor notice one of the two already-passing intents regressing
# against a later tool or corpus.
echo "########## the two intents already SATISFIED at the baseline (controls)"
ask --implies intent-02-header-fallback.oath        "expect: header-or proves it"
ask --implies intent-09-delimited-prefix.oath       "expect: media-type-is + path-is proved; str-prefix REFUTED"

echo "########## the RETURN axis (intent 1)"
ask --spec    intent-01-a-returns-collection.oath   "expect: nothing, and NO signature-compatible fallback"
ask --implies intent-01-b-returns-one.oath          "expect: config-missing proves BOTH laws"

echo "########## the ABSTRACTION axis (intent 11)"
ask --implies intent-11-a-takes-a-value.oath        "expect: nothing provably satisfies it"
ask --implies intent-11-b-takes-a-test.oath         "expect: any PROVES both; all REFUTED with a countermodel"

echo "########## the POLYMORPHISM axis (intent 12) — the 2x2"
ask --implies intent-12-a-monomorphic.oath          "expect: nothing"
ask --implies intent-12-b-polymorphic-explicit.oath "expect: filter + take-while proved; drop-while refuted"
ask --implies intent-12-c-polymorphic-inferred.oath "expect: IDENTICAL to the row above — the application is inferred"

echo "########## the fragment limit (intents 6, 3a) — shape does not reach these"
ask --implies intent-06-record-field.oath           "expect: refutations plus NO VERDICT on the target"
ask --implies intent-03a-json-scan.oath             "expect: NO VERDICT only (slow: ~10 min)"

echo "########## and --details NAMES an unsettled candidate"
ask --implies intent-06-record-field.oath           "expect: record-field named, with the reason" --details

rm -rf "$store"
