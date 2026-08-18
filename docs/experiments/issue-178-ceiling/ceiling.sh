#!/bin/sh
# NOTE ON OUTPUT: the probes are EXPECTED to crash — that is the measurement —
# so the shell prints "Segmentation fault" job notices for most of them. They
# are suppressed at each probe site rather than globally, so a crash in the
# harness ITSELF would still be visible.
# The instrument behind docs/experiments/issue-178-ceiling.md.
#
#   ceiling.sh            measure both shapes at both stack limits
#
# TWO SHAPES, DIFFERING ONLY IN LIVE LOCALS. `count-down` and `count-down-wide`
# in depth.oath are the same recursion; the wide one holds four extra bound
# values across the recursive call, so its emitted C frame is larger. If the
# ceiling is stack/frame, the wide one fails proportionally sooner — which is
# the claim, and the constants are not.
#
# BISECTION, NOT A SWEEP. The failure is a SIGSEGV, so a linear scan past the
# threshold costs one crash per step. `lo` is always a depth that SUCCEEDED and
# `hi` always one that FAILED, so the reported figure is a measured success and
# never an inference.
#
# The pipeline hazard this measurement already hit once: `binary n | head; echo
# $?` reports HEAD's status, not the binary's. Every probe below runs the
# binary directly with its output discarded, so `$?` is the program's.
set -eu
root=$(git rev-parse --show-toplevel)
here=$(dirname "$0")
# `oath/oath` is gitignored, so a clean checkout has none and a developer
# checkout may hold a stale one. Build it, so the figures describe THIS source.
( cd "$root" && make build >/dev/null )
store=$(mktemp -d); cp -R "$root/codebase/"* "$store/"
out=$(mktemp -d)
export OATH_STORE="$store"
"$root/oath/oath" put "$here/depth.oath" --new >/dev/null 2>&1
for n in depth-probe depth-probe-wide; do
  "$root/oath/oath" build "$n" --backend llvm -o "$out/$n-ll" >/dev/null 2>&1
  "$root/oath/oath" build "$n"                -o "$out/$n-go" >/dev/null 2>&1
done

tight() {  # tight <binary> <stack-KB>
  lo=1000; hi=4000000
  # A REJECTED ulimit MUST NOT fall through to the default. `ulimit -s N; cmd`
  # in a subshell runs cmd at the UNCHANGED limit when the ulimit fails, and
  # the surrounding `if` suppresses errexit — so both columns would report
  # plausible thresholds measured at the same stack size. 111 is distinguishable
  # from the program's own exits.
  if ! ( ulimit -s "$2" 2>/dev/null ); then
    echo "REFUSED: this shell cannot set a ${2}KB stack (hard limit $(ulimit -Hs))" >&2
    exit 1
  fi
  # VALIDATE BOTH ENDPOINTS FIRST. The loop's invariant is that `lo` succeeds
  # and `hi` fails; with hard-coded bounds neither is checked, so a platform
  # where 4,000,000 survives — or where 1,000 already fails, or where `ulimit
  # -s` itself fails and every probe is read as a crash — still exits with a
  # plausible-looking threshold. This measurement hit exactly that: a Go bisect
  # started at a lower bound that did not succeed and reported it as the answer.
  if ! ( ulimit -s "$2" 2>/dev/null || exit 111; exec 2>/dev/null; "$1" "$lo" >/dev/null 2>&1 ); then
    echo "REFUSED: depth $lo already fails under a ${2}KB stack — the lower" >&2
    echo "  bound is not a success, so any bisection from it is meaningless" >&2
    exit 1
  fi
  if ( ulimit -s "$2" 2>/dev/null || exit 111; exec 2>/dev/null; "$1" "$hi" >/dev/null 2>&1 ); then
    echo "REFUSED: depth $hi still succeeds under a ${2}KB stack — the upper" >&2
    echo "  bound is not a failure, so the ceiling is above the search range" >&2
    exit 1
  fi
  while [ $((hi - lo)) -gt 1 ]; do
    mid=$(( (lo + hi) / 2 ))
    if ( ulimit -s "$2" 2>/dev/null || exit 111; exec 2>/dev/null; "$1" "$mid" >/dev/null 2>&1 ); then lo=$mid; else hi=$mid; fi
  done
  # lo and hi are now adjacent: lo is the deepest SUCCESS, hi the shallowest
  # FAILURE. Reporting lo alone would be a bound, not a boundary.
  echo "$lo"
}

echo "platform: $(uname -sm)   default stack: $(ulimit -s) KB"
# The CLI has no --version; identify the source instead, which is what a later
# reader needs to know the figures describe.
# EVERY INPUT TO THE MEASURED ARTIFACT, not just the compiler: `codebase/` is
# copied into the temporary store and the probes are built from it, so a change
# to a dependency like `parse-nat` alters what is measured. `git diff --quiet`
# would also miss staged and untracked files; `status --porcelain` does not.
# The transcript is EXCLUDED: it is this script's output, and `> transcript.txt`
# truncates it before the check runs, so including it would report +MODIFIED on
# every run including a clean one — a marker that always fires means nothing.
echo "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ Makefile codebase/ docs/experiments/issue-178-ceiling/depth.oath docs/experiments/issue-178-ceiling/ceiling.sh)" ] || echo ' +MODIFIED')"
clang --version 2>/dev/null | head -1 | sed 's/^/clang: /' || true
# Half the figures below are Go ceilings, and the Go toolchain governs frame
# layout and the runtime's stack behaviour.
go version 2>/dev/null | sed 's/^/go: /' || true
echo
printf '%-8s %10s %10s %7s\n' shape 8176KB 16352KB ratio
for s in "narrow depth-probe" "wide depth-probe-wide"; do
  set -- $s
  a=$(tight "$out/$2-ll" 8176); b=$(tight "$out/$2-ll" 16352)
  printf '%-8s %10s %10s %7s\n' "$1" "$a" "$b" \
    "$(python3 -c "print('%.3f' % ($b/$a))")"
done
echo
# BOTH SHAPES ON GO TOO. The Go/LLVM ratio is not a constant — it is 12x for the
# narrow shape and 3.6x for the wide one — so measuring only one shape here would
# leave the reported ratio unreproducible from this instrument.
go_ceiling() {  # go_ceiling <binary> <lo> <hi>
  lo=$2; hi=$3
  "$1" "$lo" >/dev/null 2>&1 || { echo "REFUSED: Go lower bound $lo already fails" >&2; exit 1; }
  "$1" "$hi" >/dev/null 2>&1 && { echo "REFUSED: Go upper bound $hi still succeeds" >&2; exit 1; }
  while [ $((hi - lo)) -gt 1 ]; do
    mid=$(( (lo + hi) / 2 ))
    if ( exec 2>/dev/null; "$1" "$mid" >/dev/null 2>&1 ); then lo=$mid; else hi=$mid; fi
  done
  echo "$lo"
}
echo "Go backend (runtime 1 GB goroutine-stack limit, not ulimit):"
gn=$(go_ceiling "$out/depth-probe-go" 1000000 5000000)
gw=$(go_ceiling "$out/depth-probe-wide-go" 100000 900000)
printf '  narrow max depth ~= %s\n  wide   max depth ~= %s\n' "$gn" "$gw"
"$out/depth-probe-go" 5000000 2>&1 | head -1 | sed 's/^/  diagnostic: /'
# CAPTURE, THEN CHECK. Discarding the streams and printing "(no diagnostic)"
# would make that claim whatever the artifact emitted, and the disposition this
# measurement supports rests on the failure being SILENT.
llout=$(mktemp)
# `set -e` would abort here the moment the probe fails — which is the EXPECTED
# outcome — so the status is taken inside an `if`, never from a bare command.
# PINNED to the same 8 MB limit the table was measured at. At the ambient limit
# — unlimited, say — 400000 could SUCCEED, and a warning that still exits 0
# would let a reproduction omit the silent-failure evidence this document rests
# on. An unexpected success is a hard failure of the harness.
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
if ( ulimit -s 8176 2>/dev/null || exit 111; exec "$out/depth-probe-ll" 400000 ) >"$llout" 2>&1; then rc=0; else rc=$?; fi
if [ "$rc" = 0 ]; then
  echo "  FAILED: depth 400000 SUCCEEDED under an 8176KB stack — the ceiling" >&2
  echo "  moved, so this run does not reproduce the reported refusal" >&2
  rm -f "$llout"; exit 1
fi
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
rm -f "$llout"
