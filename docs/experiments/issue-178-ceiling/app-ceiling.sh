#!/bin/sh
# The instrument behind the "decline condition, answered" section of
# docs/experiments/issue-178-ceiling.md.
#
#   app-ceiling.sh        measure the COMMITTED APPLICATION's ceiling
#
# `ceiling.sh` beside this one measures two SYNTHETIC recursions, isolated from
# `Set`, `str-split` and the log format so nothing but the recursion is under
# test. That is what establishes stack/frame as the model. It cannot answer the
# issue's DECLINE CONDITION — "is the ceiling above realistic use?" — because a
# synthetic frame is not the application's frame, and #178 was opened on
# `apps/github-webhook/report.oath`, not on a probe.
#
# So this measures the artifact the issue names, on the input the issue names:
# DISTINCT delivery ids in a well-formed `oath-gh/1` log. Duplicate-heavy logs
# of the same length succeed, because the deduplicating set stays small — the
# independent variable is distinctness, not line count, and generating N lines
# with N distinct ids is what makes the two coincide.
#
# NOTE ON OUTPUT: the probes past the ceiling are EXPECTED to crash — that is
# the measurement — so the shell prints "Segmentation fault" notices. They are
# suppressed at each probe site rather than globally, so a crash in the harness
# ITSELF stays visible.
#
# BISECTION WITH BOTH ENDPOINTS VALIDATED, for the reason ceiling.sh records:
# with hard-coded bounds and no check, a lower bound that already fails is
# reported as the ceiling. That happened during this measurement.
set -eu
root=$(git rev-parse --show-toplevel)
here=$(dirname "$0")
( cd "$root" && make build >/dev/null )

# The COMMITTED corpus, copied — `report.oath` and every definition it reaches
# are already in it, so unlike ceiling.sh there is nothing to put. Copying
# rather than using the store in place keeps the committed journal untouched;
# `oath build` does not write, but the guarantee should not rest on that.
store=$(mktemp -d); cp -R "$root/codebase/"* "$store/"
out=$(mktemp -d); export OATH_STORE="$store"
# PUT THE SOURCE, DO NOT TRUST THE COPY. `oath build` resolves a NAME out of the
# store, so copying `codebase/` and building would measure whatever was last
# committed — and an uncommitted edit to `report.oath` would be reported as
# `+MODIFIED` in the provenance line while changing nothing about the artifact
# measured. Re-putting binds the name to THIS source. Every name already exists
# in the copied store, so no `--new` is needed and none is passed: the guard
# that refuses fresh bindings stays armed, and the committed store is untouched
# because this is a temporary copy.
"$root/oath/oath" put "$root/apps/github-webhook/report.oath" >/dev/null 2>&1
"$root/oath/oath" build gh-report-main --backend llvm -o "$out/report-ll" >/dev/null 2>&1
"$root/oath/oath" build gh-report-main                -o "$out/report-go" >/dev/null 2>&1

log=$(mktemp); probe=$(mktemp)
# N lines, N DISTINCT delivery ids, one repository so the group-and-count stage
# stays constant across the sweep and only the dedup set grows.
mklog() { python3 -c "
import sys
n=int(sys.argv[1])
with open(sys.argv[2],'w') as f:
    for i in range(n):
        f.write('oath-gh/1\td-%d\tpush\tmiclip/oath-lang\t%d\n' % (i, i))
" "$1" "$log"; }

# A SUCCESS IS NOT MERELY EXIT 0, AND NOT MERELY "THE REPOSITORY APPEARS".
# The entry point's disposition is TOTAL — every input is either refused or
# summarised (`gh-report`'s own `a-clean-log-is-summarised` / `any-bad-line-
# refuses`) — and BOTH exit 0. A bisection accepting either would converge on
# the largest log the program can REFUSE and report it as the largest it can
# PROCESS. Worse, the refusal ECHOES the offending lines, so any predicate
# searching the output for the repository name matches a refusal too; an
# earlier draft of this script did exactly that.
#
# So the predicate is the summary's exact first line, carrying the record count
# — which pins the successful branch AND checks every record was processed. A
# refusal cannot produce it (it begins "refusing: "), and neither can the usage
# string.
works() {  # works <binary> <stack-KB or ""> <n>
  mklog "$3"
  want="$log: $3 record(s), $3 distinct deliver(ies)"
  # Status taken from the ARTIFACT, never from a pipeline: `binary | grep` reports
  # grep's status, which this measurement has already been bitten by once.
  if [ -n "$2" ]; then
    ( ulimit -s "$2" 2>/dev/null || exit 111; "$1" "$log" ) >"$probe" 2>/dev/null || return 1
  else
    "$1" "$log" >"$probe" 2>/dev/null || return 1
  fi
  [ "$(head -1 "$probe")" = "$want" ]
}

ceiling() {  # ceiling <binary> <stack-KB or ""> <lo> <hi>
  lo=$3; hi=$4
  if [ -n "$2" ] && ! ( ulimit -s "$2" 2>/dev/null ); then
    echo "REFUSED: this shell cannot set a ${2}KB stack (hard limit $(ulimit -Hs))" >&2
    exit 1
  fi
  works "$1" "$2" "$lo" || { echo "REFUSED: $lo records already fails — the lower bound is not a success" >&2; exit 1; }
  works "$1" "$2" "$hi" && { echo "REFUSED: $hi records still succeeds — the ceiling is above the search range" >&2; exit 1; }
  while [ $((hi - lo)) -gt 1 ]; do
    mid=$(( (lo + hi) / 2 ))
    if works "$1" "$2" "$mid"; then lo=$mid; else hi=$mid; fi
  done
  echo "$lo"   # deepest SUCCESS; hi is the shallowest failure
}

echo "platform: $(uname -sm)   default stack: $(ulimit -s) KB"
# EVERY INPUT TO THE MEASURED ARTIFACT. `report.oath` is built from the copied
# corpus, so a change to it, to a definition it reaches, or to the compiler
# changes what this measures. The transcript is excluded for the reason
# ceiling.sh gives: this script truncates it before the check could run.
echo "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ Makefile codebase/ apps/github-webhook/report.oath docs/experiments/issue-178-ceiling/app-ceiling.sh)" ] || echo ' +MODIFIED')"
clang --version 2>/dev/null | head -1 | sed 's/^/clang: /' || true
go version 2>/dev/null | sed 's/^/go: /' || true
echo

a=$(ceiling "$out/report-ll" 8176  1000 40000)
b=$(ceiling "$out/report-ll" 16352 1000 80000)
echo "llvm  max distinct records @  8176KB: $a"
echo "llvm  max distinct records @ 16352KB: $b"
echo "      ratio 16352/8176: $(python3 -c "print('%.3f' % ($b/$a))")   bytes of stack per record: $(python3 -c "print('%.0f  %.0f' % (8176*1024/$a, 16352*1024/$b))")"
echo
# Go is bounded by the runtime's 1 GB goroutine stack, not by ulimit, so no
# limit is imposed here — passing one would suggest a dependence that does not
# exist.
g=$(ceiling "$out/report-go" "" 1000 200000)
echo "go    max distinct records (1 GB goroutine stack): $g"
echo "      go/llvm ratio at the 8176KB default: $(python3 -c "print('%.1f' % ($g/$a))")x"
echo
# THE FAILURE MODE IS HALF THE FINDING, so it is captured rather than assumed.
mklog $(( a * 2 ))
llout=$(mktemp)
# CAPTURING THE ARTIFACT'S STDERR REQUIRES `exec`, AND THIS TOOK TWO WRONG
# DRAFTS IN OPPOSITE DIRECTIONS.
#
#   exec 2>/dev/null; "$bin"   discards the artifact's stderr INSIDE the
#                              subshell, so "zero bytes" was true whatever the
#                              artifact emitted
#   "$bin"                     the subshell has SEVERAL commands, so it forks
#                              rather than implicitly exec-ing, SURVIVES its
#                              child's crash, and writes its own "Segmentation
#                              fault" notice into the captured stream — 55 bytes
#                              of SHELL output read as ARTIFACT output
#   exec "$bin"                the subshell is REPLACED by the artifact, so no
#                              shell remains to narrate, and the only bytes that
#                              can reach the file are the artifact's own
#
# Verified by running all three against a deliberately crashing C program. Note
# that a single-command subshell exec's implicitly and would have hidden this,
# which is why the isolation test had to reproduce the multi-command shape.
if ( ulimit -s 8176 2>/dev/null || exit 111; exec "$out/report-ll" "$log" ) >"$llout" 2>&1; then
  echo "FAILED: $(( a * 2 )) records SUCCEEDED under an 8176KB stack — the ceiling" >&2
  echo "  moved, so this run does not reproduce the reported failure" >&2
  rm -f "$llout" "$log"; exit 1
else rc=$?; fi
# THE CONTRACT IS NOW A REFUSAL, SO THIS ASSERTS IT RATHER THAN REPORTING IT.
# Until the stack guard landed this printed "exit 139, zero bytes on stdout and
# stderr" and that WAS the finding. An artifact that goes back to crashing
# silently is a regression, and a probe that merely describes whatever happened
# cannot say so.
if [ "$rc" = 139 ]; then
  echo "FAILED: the artifact crashed (139) instead of refusing — the stack" >&2
  echo "  guard did not fire, which is the defect it was built to remove" >&2
  rm -f "$llout"; exit 1
elif [ "$rc" != 70 ]; then
  echo "FAILED: expected exit 70, this backend's runtime-refusal code; got $rc" >&2
  rm -f "$llout"; exit 1
elif ! grep -q "exhausted its stack budget" "$llout"; then
  echo "FAILED: exit 70 with no stack diagnostic. 70 is also the" >&2
  echo "  provisioning-failure code, so without the message an operator" >&2
  echo "  cannot tell the two apart" >&2
  rm -f "$llout"; exit 1
else
  echo "llvm past ceiling: exit 70 and a diagnostic (was 139, silent):"
  fold -w 76 -s "$llout" | sed 's/^/    /'
fi
if "$out/report-go" "$log" >"$llout" 2>&1; then
  echo "go   at the same size: exit 0 (its own ceiling is far higher)"
else
  echo "go   past ITS ceiling: exit $?, diagnostic:"; head -2 "$llout" | sed 's/^/  /'
fi
rm -f "$llout" "$log" "$probe"
