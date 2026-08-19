#!/bin/sh
# Re-asks EVERY query both demand-5 subjects wrote, against the committed corpus.
#
# Every one, not a selection: a verifier that runs a subset while the reports say
# it runs all of them is the same defect as a capped list that does not say it
# capped. Seventeen queries, all --spec (fast); the subjects also ran --implies
# on several, which is slow and adds nothing here — the claims that matter are
# content-hash matches, and --spec is the mode that makes them.
#
# WHY THIS AND NOT A TRANSCRIPT. The verdict rests on what `oath find` answered
# for queries the subjects wrote — in particular on three claimed CONTENT-HASH
# matches in run 2, which are the strongest evidence in the record. A pasted
# transcript can only be read; these can be re-asked, so the claims are
# decidable from the corpus.
#
# It does NOT reproduce the STUDY. Whether a reader arrives at these queries
# from the intent is the behavioural claim, and no script settles that.
#
# The committed store is not touched: everything runs against a temporary copy.
set -eu
root=$(git rev-parse --show-toplevel)
here=$(dirname "$0")
( cd "$root" && make build >/dev/null )
store=$(mktemp -d); cp -R "$root/codebase/"* "$store/"
export OATH_STORE="$store"
echo "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ codebase/ docs/experiments/issue-176-compositions/)" ] || echo ' +MODIFIED')"
echo
ask() {  # ask <dir> <file> <claim>
  echo "=== $1/$2"
  echo "    CLAIM: $3"
  # Status from the COMMAND, not the pipeline: `oath find | sed` reports sed's
  # status, so an error would print as an empty result and read as "found
  # nothing".
  if ! out=$("$root/oath/oath" find --spec "$here/$1/$2" 2>&1); then
    echo "FAILED: oath find --spec $1/$2 exited nonzero" >&2
    printf '%s\n' "$out" | sed 's/^/    /' >&2; exit 1
  fi
  printf '%s\n' "$out" | sed -n '2,$p' | sed 's/^/  /'
  echo
}
echo "########## RUN 2 — the three content-hash matches the verdict rests on"
ask queries-run2 q3-accept.oath          "the accept law, written from the intent, hits gh-webhook's own accepts-github-signed"
ask queries-run2 q6-ping-trap.oath       "the ping trap hits gh-webhook's own ping-does-not-record"
ask queries-run2 q7-silent-sink-trap.oath "the empty-sink trap hits gh-webhook's own a-non-ping-does-record"

echo "########## RUN 2 — the rest, in the order the subject wrote them"
ask queries-run2 q1-probe-handler.oath   "a reflexive law at the handler's shape names gh-webhook"
ask queries-run2 q2-strlit.oath          "string literals elaborate; this probe was a syntax check"
ask queries-run2 q4-naive-202.oath       "a flat 202 assertion is NOT stated by the corpus"
ask queries-run2 q5-naive-ping.oath      "asserting 202 for a ping is NOT stated either"
ask queries-run2 q8-any-usable-secret.oath "generalising off the fixture secret is NOT stated"

echo "########## RUN 1 — every query, in order"
ask queries-run1 q1.oath                 "a probe at (-> Str (List Int) Str) names gh-sign"
ask queries-run1 q2.oath                 "a probe at (-> Str (List Int) Request) names nothing"
ask queries-run1 q3.oath                 "a probe at the five-argument shape names gh-request"
ask queries-run1 q4.oath                 "an end-to-end wording that did NOT hash-match"
ask queries-run1 q5.oath                 "the same, with the lam removed"
ask queries-run1 q6.oath                 "the signing law hits gh-sign's own survives-gh-signature"
ask queries-run1 q7.oath                 "the request-builder laws hit gh-request"
ask queries-run1 q8.oath                 "the subject's CALIBRATION control: a deliberately false law"
ask queries-run1 q9.oath                 "is a one-call builder hiding? no"

rm -rf "$store"
