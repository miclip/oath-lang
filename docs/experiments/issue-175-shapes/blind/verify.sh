#!/bin/sh
# Re-derives the blind run's claimed matches from the corpus.
#
#   verify.sh      re-ask the subjects' own queries and print what the tool says
#
# WHY THIS EXISTS RATHER THAN A PASTED TRANSCRIPT. The blind run's numbers rest
# on what `oath find` answered for queries the subjects wrote. A transcript can
# only be read; these queries can be RE-ASKED, so the claims are decidable from
# the corpus rather than from a report. `subject-queries/` holds the queries
# verbatim.
#
# It does NOT reproduce the STUDY: whether a reader arrives at these queries
# from the intent alone is the behavioural claim, and no script settles it.
# What it settles is that the queries say what the write-up says they say.
#
# THE COMMITTED STORE IS NOT TOUCHED. ~15 minutes; the --implies runs are slow.
set -eu
root=$(git rev-parse --show-toplevel)
here=$(dirname "$0")
( cd "$root" && make build >/dev/null )
store=$(mktemp -d); cp -R "$root/codebase/"* "$store/"
export OATH_STORE="$store"

echo "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ Makefile codebase/ docs/experiments/issue-175-shapes/)" ] || echo ' +MODIFIED')"
echo

ask() {  # ask <mode> <file> <claim>
  echo "=== $2  [$1]"
  echo "    CLAIM: $3"
  # Status from the COMMAND, never the pipeline: `oath find | sed` reports sed's
  # status, so `set -e` would not fire and an error would print as an empty
  # result — indistinguishable from "found nothing".
  if ! out=$("$root/oath/oath" find "$1" "$here/subject-queries/$2" 2>&1); then
    echo "FAILED: oath find $1 $2 exited nonzero; this run records nothing" >&2
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    exit 1
  fi
  printf '%s\n' "$out" | sed -n '2,$p' | sed 's/^/  /'
  echo
}

echo "########## the two the ORACLE run wrote off, reached by BOTH blind subjects"
echo "########## — so BOTH subjects' own queries are re-asked, not one subject's twice"
ask --spec s1-intent-03.oath "subject 1: json-string-value states both laws (hash match)"
ask --spec s1-intent-04.oath "subject 1: record-field, hash match under a DIFFERENT property name"
ask --spec s2-intent-03.oath "subject 2: the same target, its own wording, names differing again"
ask --spec s2-intent-04.oath "subject 2: same target (weaker — it probed for candidates first)"

echo "########## the technique the CLEAN run found, which the guidance did not describe"
ask --spec signature-probe.oath "a reflexive law enumerates the corpus at this shape"

echo "########## the axes"
ask --implies s1-intent-01.oath "config-missing proves both laws"
ask --implies s1-intent-06.oath "any proves both laws; all REFUTED"
ask --implies s1-intent-07.oath "take-while proves both; drop-while REFUTED"

echo "########## subject 2's own positive control, at Int"
ask --spec s2-intent-06-control.oath "the same membership laws find contains at Int — so the Str absence is real"

rm -rf "$store"
