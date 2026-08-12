#!/bin/sh
# THE INT64 BOUNDARY, THREE WAYS (#166).
#
# This script ran #166's cheap falsifier first: is a CHECKED int64 — fail-closed,
# trapping on overflow rather than wrapping — enough, so that the
# arbitrary-precision runtime is unnecessary? It is not. `sum-past-int64-max`
# refused at runtime while `oath eval` and the Go backend answered, and
# `sum-past-max-from-argv` showed the overflow can depend on input no compiler
# sees, so no build-time analysis could have decided it. The falsifier did not
# fire and the representation moved. README.md is the record.
#
# WHAT IT ASSERTS NOW IS STRICTLY STRONGER. Where it recorded that the LLVM
# artifact REFUSED, it records the exact value the artifact produces. Refusal is
# one bit; the value is the whole answer.
#
# THE COMPARISON IS THREE-WAY AND THE INTERPRETER IS THE REFERENCE. Comparing the
# two backends against each other only shows they agree, which two identically
# wrong lowerings also do.
#
# CONTROLS, because a probe that cannot fail is decoration:
#   - the interpreter decoder must not collapse a non-empty Str to ""
#   - the comparator must REPORT a disagreement when handed a wrong reference
#   - `neg` must still be refused BY NAME, or the subset boundary has been
#     deleted rather than moved
#   - the value each artifact prints is read, not just compared, so three
#     identically wrong implementations cannot pass by agreeing
#
# fuzz.py is the other half of the evidence here and is not run by this script:
# these are the named boundary cases, that is the randomised search.
#
# Requires: clang (the LLVM backend shells out to it), python3 (to decode the
# interpreter's structural Str).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
work=$(mktemp -d)
store=$(mktemp -d)
cleanup() { rm -rf "$work" "$store"; }
trap cleanup EXIT INT TERM
export OATH_STORE="$store"

nl='
'

if ! command -v clang >/dev/null 2>&1; then
  if [ -n "${CI:-}" ]; then
    echo "clang is absent, so the LLVM backend did not run. In CI that is a failure, not a skip."
    exit 1
  fi
  echo "clang not available (local); skipping. This is a hard failure under CI=1."
  exit 0
fi

# BUILT FROM THIS CHECKOUT. `oath/oath` is a gitignored artifact that can predate
# the source, and a stale one would measure a backend nobody changed — which is
# the entire subject here. (That is not hypothetical: a stale binary produced one
# false alarm during this work, reporting a multiplication defect that had
# already been reverted from the source.) OATH_BIN overrides for a caller testing
# a specific binary on purpose.
oath="${OATH_BIN:-}"
if [ -z "$oath" ]; then
  oath="$work/oath"
  ( cd "$root/oath" && go build -o "$oath" . ) || {
    echo "FAIL setup: could not build the CLI from this checkout"; exit 1; }
fi
[ -x "$oath" ] || { echo "FAIL setup: $oath is not executable"; exit 1; }

fail=0
ran=0
check() { # check <label> <expected> <actual>
  ran=$((ran + 1))
  if [ "$2" = "$3" ]; then
    printf '  ok    %s\n' "$1"
  else
    printf '  FAIL  %s\n        want [%s]\n        got  [%s]\n' "$1" "$2" "$3"
    fail=$((fail + 1))
  fi
}

# decode_str turns the interpreter's structural Str — (SCons 104 (SCons ...)) —
# into the bytes a compiled program prints. It takes a FILENAME rather than
# stdin: `python3 -` reads its program from stdin, so a heredoc and piped data
# compete for one channel and the decoder silently returns "" for every input.
cat > "$work/decode_str.py" <<'PY'
import sys, re
out = open(sys.argv[1]).read()
i = out.rfind(' : ')
if i >= 0: out = out[:i]
sys.stdout.write(''.join(chr(int(n)) for n in re.findall(r'\(SCons (\d+)', out)))
PY
decode_str() { python3 "$work/decode_str.py" "$1"; }

# 0. THE INSTRUMENT, BEFORE ANYTHING IS MEASURED WITH IT.
printf '(SCons 111 (SCons 107 SNil)) : Str\n' > "$work/probe.txt"
if [ "$(decode_str "$work/probe.txt")" != "ok" ]; then
  echo "FAIL setup: the interpreter decoder is broken, so every comparison below would be meaningless"
  exit 1
fi

# 1. PUT. `oath build` refuses an entry that was falsified or never verified, so
#    reaching `tested` is the first half of being able to measure anything.
if ! OATH_AUTHOR="${OATH_AUTHOR:-claude-main}" "$oath" put "$here/int64-boundary-probe.oath" --new > "$work/put.txt" 2>&1; then
  echo "FAIL setup: oath put failed"; cat "$work/put.txt"; exit 1
fi
check "every definition reaches at least 'tested'" "0" \
      "$(grep -Ec 'asserted|falsified|✗' "$work/put.txt" || true)"

# 2. THE FAIL-CLOSED CONTROL. `neg` is the remaining Int boundary and is not
#    lowered. The Go backend must compile it and the LLVM backend must refuse it
#    by name, or the boundary has been deleted rather than moved.
if ! "$oath" build negate-control -o "$work/go-control" > "$work/build-control-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused the control, so it is not a BACKEND-SUBSET boundary"
  cat "$work/build-control-go.txt"; exit 1
fi
"$oath" build negate-control --backend llvm -o "$work/ll-control" > "$work/build-control.txt" 2>&1 && rc=0 || rc=$?
check "the LLVM backend still refuses negation" "1" \
      "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "  ...and names it" "1" \
      "$(grep -c 'primitive operation "neg"' "$work/build-control.txt" || true)"
check "  ...and emitted no executable" "0" \
      "$([ -e "$work/ll-control" ] && echo 1 || echo 0)"

# BUILD the four entries inside the subset, both backends.
for n in small-sum-is-computed sum-reaches-int64-max sum-past-int64-max sum-past-max-from-argv; do
  if ! "$oath" build "$n" -o "$work/go-$n" > "$work/build-go-$n.txt" 2>&1; then
    echo "FAIL setup: the Go backend refused $n"; cat "$work/build-go-$n.txt"; exit 1
  fi
  if ! "$oath" build "$n" --backend llvm -o "$work/ll-$n" > "$work/build-ll-$n.txt" 2>&1; then
    echo "FAIL: the LLVM backend refused $n, so arbitrary-precision Int is not reaching the emitter"
    cat "$work/build-ll-$n.txt"; exit 1
  fi
done

# three_way prints what disagreed, or nothing. EVERY exit status is captured: this
# runs inside a command substitution, so under `set -e` a crashing binary would
# kill only the subshell and leave the empty output that a PASS looks like.
three_way() { # three_way <name> [argv...]
  n=$1; shift
  argv='(Nil [Str])'
  for a in "$@"; do argv="(Cons [Str] \"$a\" $argv)"; done
  rc=0
  "$oath" eval "($n $argv)" > "$work/eval.txt" 2> "$work/eval.err" || rc=$?
  if [ "$rc" != "0" ]; then printf 'the interpreter exited %s' "$rc"; return 0; fi
  want=$( { decode_str "$work/eval.txt"; printf X; } ); want=${want%X}

  rc_go=0; "$work/go-$n" "$@" > "$work/go.out" 2> "$work/go.err" || rc_go=$?
  rc_ll=0; "$work/ll-$n" "$@" > "$work/ll.out" 2> "$work/ll.err" || rc_ll=$?
  if [ "$rc_go" != "0" ] || [ "$rc_ll" != "0" ]; then
    printf 'a compiled program exited nonzero (go=%s llvm=%s)' "$rc_go" "$rc_ll"; return 0
  fi
  got_go=$( { cat "$work/go.out"; printf X; } ); got_go=${got_go%X}
  got_ll=$( { cat "$work/ll.out"; printf X; } ); got_ll=${got_ll%X}
  got_go=${got_go%"$nl"}   # the CLI protocol appends exactly one newline
  got_ll=${got_ll%"$nl"}
  if [ "$got_go" != "$want" ] && [ "$got_ll" != "$want" ]; then
    printf 'BOTH backends disagree with the interpreter'
  elif [ "$got_go" != "$want" ]; then
    printf 'the Go backend disagrees with the interpreter'
  elif [ "$got_ll" != "$want" ]; then
    printf 'the LLVM backend disagrees with the interpreter'
  fi
}

# 3. THE ADD IS REAL. Without this every result below is equally consistent with
#    `+` having been lowered to something that ignores its arguments.
check "all three paths agree on a small sum" "" "$(three_way small-sum-is-computed)"
check "  ...and the sum is actually computed" "sum-ok" \
      "$("$work/ll-small-sum-is-computed" | tr -d '\n')"

# 4. THE OLD IN-RANGE BOUNDARY, kept because a regression to a fixed-width
#    runtime would show up here first.
check "all three paths agree at exactly int64 max" "" "$(three_way sum-reaches-int64-max)"
check "  ...and the sum reached int64 max" "reached-max" \
      "$("$work/ll-sum-reaches-int64-max" | tr -d '\n')"

# 5. THE COMPARATOR MUST BE ABLE TO FAIL. It is handed a reference from a
#    DIFFERENT entry point, so the three paths are no longer running the same
#    program and it must report a disagreement. Recorded BEFORE any result it is
#    trusted for. It is the SAME function under test with one input swapped — a
#    separately written comparison would prove only that the copy works.
mismatch() {
  "$oath" eval '(small-sum-is-computed (Nil [Str]))' > "$work/mis.txt" 2>&1 || { printf 'eval failed'; return 0; }
  want=$( { decode_str "$work/mis.txt"; printf X; } ); want=${want%X}
  got=$( { "$work/go-sum-reaches-int64-max"; printf X; } ); got=${got%X}; got=${got%"$nl"}
  got_ll=$( { "$work/ll-sum-reaches-int64-max"; printf X; } ); got_ll=${got_ll%X}; got_ll=${got_ll%"$nl"}
  if [ "$got" != "$want" ] && [ "$got_ll" != "$want" ]; then printf 'BOTH backends disagree with the interpreter'; fi
}
check "CONTROL: a mismatched reference IS reported" \
      "BOTH backends disagree with the interpreter" "$(mismatch)"

# ---------- 6. THE WITNESS ----------
#
# (+ 9223372036854775807 1) leaves int64 by one, sequenced before a literal Str.
# Under the checked prototype the LLVM artifact exited 70 here while the other
# two answered. It must now produce the value, and the value must be the one
# `oath eval` produces — 9223372036854775808, which the old backend could not
# even write down as a literal.
check "the witness agrees three ways" "" "$(three_way sum-past-int64-max)"
check "  ...and the answer is EXACT, not wrapped or refused" "exact" \
      "$("$work/ll-sum-past-int64-max" | tr -d '\n')"

rc_ll=0; "$work/ll-sum-past-int64-max" > /dev/null 2> "$work/w-ll.err" || rc_ll=$?
check "  ...and the artifact no longer refuses" "0" "$rc_ll"
check "  ...leaving no overflow diagnostic behind" "0" \
      "$(grep -c 'Int overflow' "$work/w-ll.err" || true)"

# ---------- 7. THE SAME WITNESS WITH AN OPERAND FROM OUTSIDE ----------
#
# The second operand is the first codepoint of argv[1], which does not exist
# until the process runs — so the overflow cannot have been decided statically.
# The assertion is an IDENTITY, (max + c) - max == c, which holds for whatever
# the input is and which no fixed-width runtime can satisfy for any c > 0.
check "an input-dependent sum past int64 max agrees three ways" "" \
      "$(three_way sum-past-max-from-argv abc)"
check "  ...and the round trip through the large value is exact" "past-max-exactly" \
      "$("$work/ll-sum-past-max-from-argv" abc | tr -d '\n')"
check "  ...and a non-ASCII first codepoint too" "past-max-exactly" \
      "$("$work/ll-sum-past-max-from-argv" 'ü' | tr -d '\n')"
check "  ...while no argument still takes the other branch" "no-argument" \
      "$("$work/ll-sum-past-max-from-argv" | tr -d '\n')"
check "  ...and that branch agrees three ways" "" "$(three_way sum-past-max-from-argv)"

echo
if [ "$fail" = "0" ]; then
  echo "int64 boundary probe: all $ran checks passed"
else
  echo "int64 boundary probe: $fail of $ran FAILED"
fi
exit "$fail"
