#!/usr/bin/env sh
# Reproduce every query behind docs/experiments/issue-74-falsifier.md.
#
# Run from the repository root, with ./oath/oath built (`make build`) and the
# canonical store in ./codebase. `oath find` is READ-ONLY: `git status
# --porcelain codebase/ fixtures/` is empty afterwards, and the demand log is not
# written because demandRecording is off for the local CLI (oath/demand.go).
#
# NAMING. `-verbatim` queries carry the target definition's OWN property text,
# copied from `oath get`. `-authored` queries state the same intent in a form the
# target does not use — they are what an author who does not know the artifact
# could write, and they are what the F2 score in the report is computed from.
# `c1-` is a control, not one of the 13 demands.
#
# COST. Most calls return in under a second. Two do not, and they are the point
# of the latency paragraph in the report: c1 --implies took 48.4s and i3-authored
# --implies took 992.3s on the machine that produced the committed transcript.
# Pass FAST=1 to skip those two.

set -eu
Q="$(dirname "$0")/queries"
O=./oath/oath
[ -x "$O" ] || { echo "build first: make build" >&2; exit 1; }

run() {  # run <mode-flag> <query-basename> [extra...]
  printf '\n########## oath find %s %s %s ##########\n' "$1" "$2" "${3-}"
  # shellcheck disable=SC2086
  "$O" find "$1" "$Q/$2.oath" ${3-} 2>&1
}

printf '########## there is no intent front door ##########\n'
"$O" find "take the longest prefix of a list satisfying a predicate" 2>&1 || true
"$O" find --equiv "prefix while predicate holds" 2>&1 || true

printf '\n########## modes 1 and 4 take a name ##########\n'
"$O" find take-while 2>&1
"$O" find --equiv take-while 2>&1
"$O" find --equiv record-field 2>&1

# --- the eight demands whose intent has a satisfying committed artifact -------
run --spec i1-authored                  # NOT REACHED — wrong return shape
run --implies i1-authored --details     # NOT REACHED — no compatible signature
run --spec i1-authored-shape-corrected  # the control: shape fixed, now found
run --implies i1-authored-shape-corrected --details

run --spec    i2-verbatim               # VERBATIM hit
run --spec    i2-authored               # miss, falls to the signature list
run --implies i2-authored --details     # SATISFIED, 0.03s

run --spec    i3-verbatim               # VERBATIM hit
run --spec    i3-authored               # miss
[ "${FAST-}" = 1 ] || run --implies i3-authored --details   # 992.3s, NO VERDICT

run --spec    i5-authored               # hit, but AST-identical to the target's law
run --implies i5-authored --details     # also proved

run --spec    i6-verbatim               # VERBATIM hit
run --spec    i6-authored               # miss
run --implies i6-authored --details     # NO VERDICT — outside the provable fragment

run --spec    i9-verbatim               # VERBATIM hit
run --spec    i9-authored               # miss, falls to the signature list
run --implies i9-authored --details     # SATISFIED, 2.59s — and REFUTES str-prefix

run --spec    i11-verbatim              # VERBATIM hit (Int)
run --spec    i11-authored              # the real intent, over Str — nothing
run --implies i11-authored --details    # nothing: the answer is `any`, a different shape

run --spec    i12-authored              # nothing
run --implies i12-authored --details    # nothing
run --spec    i12-verbatim-mono         # nothing — monomorphic query, forall a target
run --spec    i12-verbatim-poly         # hit: take-while, drop-while, filter

# --- control: paraphrase robustness where a compatible artifact EXISTS -------
run --spec    c1-contains-int-authored
[ "${FAST-}" = 1 ] || run --implies c1-contains-int-authored --details  # 48.4s, REACHED

printf '\n########## the corpus does answer intent 11, by composition ##########\n'
"$O" eval '(any [Str] (fn [(s Str)] (== s "beta")) (Cons [Str] "alpha" (Cons [Str] "beta" (Nil [Str]))))'

printf '\n########## the store must be unchanged ##########\n'
git status --porcelain codebase/ fixtures/
printf '(empty above = find did not mutate)\n'
