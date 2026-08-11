#!/bin/sh
# Three-way acceptance for show-from-marker: the interpreter, the Go backend and
# the LLVM backend must all produce the same bytes for the same program.
#
# It runs through the REAL CLI into a scratch OATH_STORE — `oath put`, `oath
# build`, `oath build --backend llvm`, `oath eval` — because the claim being
# tested is that a person can build and run this program, not that an in-process
# test harness can.
#
# `oath eval` is the reference. Comparing the two backends only shows they agree,
# which two identically wrong lowerings also do. The interpreter has no
# capabilities, so the file read is supplied to it as an ordinary function
# returning the SAME bytes the compiled programs read from disk — that
# equivalence is the one thing this script assumes, and check 0 is the control
# that makes a broken assumption visible: it feeds the interpreter a DIFFERENT
# file and requires the comparison to report a disagreement. A gate that has
# never been seen to fail is decoration.
#
# Requires: clang (the LLVM backend shells out to it), python3 (to decode the
# interpreter's structural Str and to write the Oath literal for the same bytes).
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
# The CLI is BUILT FROM THIS CHECKOUT, into the scratch directory. Reusing
# oath/oath would be faster and wrong: it is a gitignored build artifact that can
# predate the source, so a stale one would let this script pass while exercising
# CLI code nobody has changed — and `put`, argument passing and the build command
# are precisely what it claims to gate. Building here also leaves nothing behind
# in the repository. OATH_BIN overrides, for a caller testing a specific binary
# on purpose.
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

# The document this runs over: a header, a separator and a body, with a
# non-ASCII codepoint so a byte-vs-codepoint confusion cannot pass unnoticed.
printf 'name: demo\nowner: ada\n---\nbody: caf\303\251\ntrailing line\n' > "$work/sample.txt"
printf 'name: demo\nowner: ada\n---\nWRONG BODY\n' > "$work/wrong.txt"

# oath_literal writes a file's bytes as an Oath string literal using ONLY the
# escapes the Oath lexer accepts, so the interpreter receives exactly what the
# compiled programs read. It refuses anything it cannot spell rather than
# approximating it — an approximation would silently compare three paths on two
# different inputs.
oath_literal() {
  python3 - "$1" <<'PY'
import sys
out = ['"']
for ch in open(sys.argv[1], 'rb').read().decode('utf-8'):
    if ch == '\n':   out.append('\\n')
    elif ch == '\t': out.append('\\t')
    elif ch == '"':  out.append('\\"')
    elif ch == '\\': out.append('\\\\')
    elif ord(ch) < 0x20 or ord(ch) == 0x7f:
        sys.exit("no Oath spelling for U+%04X" % ord(ch))
    else: out.append(ch)
out.append('"')
sys.stdout.write(''.join(out))
PY
}

# argv_literal renders the SAME argument vector the compiled binaries receive as
# the (List Str) the interpreter needs.
argv_literal() {
  python3 - "$@" <<'PY'
import sys
out = '(Nil [Str])'
for a in reversed(sys.argv[1:]):
    q = (a.replace('\\', '\\\\').replace('"', '\\"')
          .replace('\n', '\\n').replace('\t', '\\t'))
    out = '(Cons [Str] "%s" %s)' % (q, out)
sys.stdout.write(out)
PY
}

# decode_str turns the interpreter's structural Str — (SCons 104 (SCons ...)) —
# into the bytes a compiled program prints. Str is an ordinary datatype in the
# interpreter; a compiled program prints what those codepoints spell.
#
# It takes a FILENAME rather than stdin, deliberately: `python3 -` reads its
# program from stdin, so a heredoc script and piped data compete for the same
# channel and the heredoc wins — the decoder then reads nothing and returns the
# empty string for every input. That decoder is silent, and against a comparison
# that expects agreement it would turn every check green.
cat > "$work/decode_str.py" <<'PY'
import sys, re
out = open(sys.argv[1]).read()
i = out.rfind(' : ')
if i >= 0: out = out[:i]
sys.stdout.write(''.join(chr(int(n)) for n in re.findall(r'\(SCons (\d+)', out)))
PY
decode_str() { python3 "$work/decode_str.py" "$1"; }

# The decoder is itself an instrument, so it is checked before anything is
# measured with it: a non-empty Str must not decode to the empty string.
printf '(SCons 111 (SCons 107 SNil)) : Str\n' > "$work/probe.txt"
if [ "$(decode_str "$work/probe.txt")" != "ok" ]; then
  echo "FAIL setup: the interpreter decoder is broken, so every comparison below would be meaningless"
  exit 1
fi

# EVERY capture below appends a sentinel and removes it, rather than using a
# plain $(...). Command substitution eats ALL trailing newlines, and this program
# legitimately returns a suffix of a text file — which normally ends in one. With
# the sentinel, the single newline the CLI protocol appends can be removed
# exactly once instead of being lost along with the result's own.

# 1. PUT. Every definition must reach at least `tested`: `oath build` refuses an
#    entry point that was falsified or never verified, so this is not setup, it
#    is the first half of the milestone's claim.
if ! OATH_AUTHOR="${OATH_AUTHOR:-claude-main}" "$oath" put "$here/show-from-marker.oath" --new > "$work/put.txt" 2>&1; then
  echo "FAIL setup: oath put failed"; cat "$work/put.txt"; exit 1
fi
check "every definition reaches at least 'tested'" "0" \
      "$(grep -Ec 'asserted|falsified|✗' "$work/put.txt" || true)"

# 2. BUILD, both backends. A refusal from the LLVM backend here is this
#    milestone's premise — that the subset holds no useful program — coming true.
if ! "$oath" build show-from-marker -o "$work/go" > "$work/build-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused"; cat "$work/build-go.txt"; exit 1
fi
if ! "$oath" build show-from-marker --backend llvm -o "$work/llvm" > "$work/build-llvm.txt" 2>&1; then
  echo "FAIL: the LLVM backend refused to compile show-from-marker"
  cat "$work/build-llvm.txt"; exit 1
fi
check "the LLVM backend names its lowering" "1" \
      "$(grep -c 'backend: llvm-ir/1' "$work/build-llvm.txt" || true)"
check "the artifact declares the file_read requirement" "1" \
      "$(grep -c 'requires: readfile (file_read)' "$work/build-llvm.txt" || true)"

# 3. THE FAIL-CLOSED CONTROL. `line-count-report` is verified, compiles under the
#    Go backend, and needs `+` — the one thing separating it from the viewer. The
#    LLVM backend must refuse it BY NAME. Without this, everything above is
#    equally consistent with the subset's refusals having been removed rather
#    than with the program having been written inside them.
if ! "$oath" build line-count-report -o "$work/go-control" > "$work/build-control-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused the control, so it is not a BACKEND-SUBSET boundary"
  cat "$work/build-control-go.txt"; exit 1
fi
"$oath" build line-count-report --backend llvm -o "$work/llvm-control" > "$work/build-control.txt" 2>&1 && rc=0 || rc=$?
check "the LLVM backend still refuses arithmetic" "1" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "  ...and names the primitive it lacks" "1" \
      "$(grep -c 'primitive operation "+"' "$work/build-control.txt" || true)"
check "  ...and emitted no executable" "0" \
      "$([ -e "$work/llvm-control" ] && echo 1 || echo 0)"

caps=""
set_reference_file() { caps="{readfile (fn [(p Str)] $(oath_literal "$1"))}"; }

# three_way prints what disagreed, or nothing. The interpreter is the reference
# for both backends, so a shared compiler bug cannot pass by consensus.
#
# EVERY EXIT STATUS IS CAPTURED, and a nonzero one is a verdict rather than a
# stop. `three_way` runs inside a command substitution, so under `set -e` a
# crashing binary would kill only that subshell — and the empty output it left
# behind is exactly what a PASSING case looks like here. A program that dies on
# one particular input would then be green on that input and nowhere else.
three_way() {
  argv=$(argv_literal "$@")
  rc=0
  "$oath" eval "(show-from-marker $caps $argv)" > "$work/eval.txt" 2> "$work/eval.err" || rc=$?
  if [ "$rc" != "0" ]; then
    printf 'the interpreter exited %s on this input' "$rc"; return 0
  fi
  want=$( { decode_str "$work/eval.txt"; printf X; } ); want=${want%X}

  rc_go=0; "$work/go" "$@" > "$work/go.out" 2> "$work/go.err" || rc_go=$?
  rc_ll=0; "$work/llvm" "$@" > "$work/ll.out" 2> "$work/ll.err" || rc_ll=$?
  if [ "$rc_go" != "0" ] || [ "$rc_ll" != "0" ]; then
    printf 'a compiled program exited nonzero (go=%s llvm=%s)' "$rc_go" "$rc_ll"; return 0
  fi
  got_go=$( { cat "$work/go.out"; printf X; } ); got_go=${got_go%X}
  got_ll=$( { cat "$work/ll.out"; printf X; } ); got_ll=${got_ll%X}
  # The CLI protocol appends exactly one newline. Remove that one, not every one.
  got_go=${got_go%"$nl"}
  got_ll=${got_ll%"$nl"}
  if [ "$got_go" != "$want" ] && [ "$got_ll" != "$want" ]; then
    printf 'BOTH backends disagree with the interpreter'
  elif [ "$got_go" != "$want" ]; then
    printf 'the Go backend disagrees with the interpreter'
  elif [ "$got_ll" != "$want" ]; then
    printf 'the LLVM backend disagrees with the interpreter'
  fi
}

# 0. THE CONTROL, FIRST. The comparison is handed an interpreter reference built
#    from a DIFFERENT file, so the three paths are no longer running on the same
#    input and the gate must say so. Without this, every green check below is
#    equally consistent with a comparison that cannot fail.
set_reference_file "$work/wrong.txt"
check "CONTROL: a wrong reference IS reported" \
      "BOTH backends disagree with the interpreter" \
      "$(three_way "$work/sample.txt" "---$nl")"

# The passing checks, recorded only now that the gate has been seen to fire.
set_reference_file "$work/sample.txt"
check "reads from the marker onwards"                    "" "$(three_way "$work/sample.txt" "---$nl")"
check "a marker at the very start returns the whole file" "" "$(three_way "$work/sample.txt" 'name')"
check "a marker matching the last line"                  "" "$(three_way "$work/sample.txt" 'trailing')"
check "an absent marker is reported, not empty output"   "" "$(three_way "$work/sample.txt" 'no-such-marker')"
check "a non-ASCII marker"                               "" "$(three_way "$work/sample.txt" 'café')"
check "an empty marker matches at position 0"            "" "$(three_way "$work/sample.txt" '')"
check "no arguments is the usage line"                   "" "$(three_way)"
check "one argument is the usage line"                   "" "$(three_way "$work/sample.txt")"

# And the observable behaviour a user would check by eye, so three-way agreement
# on the WRONG answer cannot pass unnoticed.
check "the found output starts with the marker" "---" \
      "$("$work/llvm" "$work/sample.txt" "---$nl" | head -c 3)"
check "the absent-marker message is the artifact's own" \
      "show-from-marker: marker not found" \
      "$("$work/llvm" "$work/sample.txt" 'no-such-marker')"
check "the usage line is the artifact's own" \
      "usage: show-from-marker <file> <marker>" \
      "$("$work/llvm")"

# A LARGE REAL FILE. The interpreter is not the reference here — a multi-kilobyte
# Oath literal is not the same input by any useful definition — so this compares
# the two backends only, and says so.
#
# The runs are NOT piped into `shasum`: a pipeline reports the status of its LAST
# command, so two crashing programs would hash two empty outputs and agree. Their
# statuses are checked first, and only then their bytes.
big_go=0; "$work/go"   "$root/docs/SPEC.md" '## 7.2 ' > "$work/big-go.out"   2>&1 || big_go=$?
big_ll=0; "$work/llvm" "$root/docs/SPEC.md" '## 7.2 ' > "$work/big-ll.out" 2>&1 || big_ll=$?
check "both backends survive a large real file" "0 0" "$big_go $big_ll"
check "  ...and produce the same bytes" \
      "$(shasum < "$work/big-go.out" | cut -d' ' -f1)" \
      "$(shasum < "$work/big-ll.out" | cut -d' ' -f1)"
check "  ...and that file really did contain the marker" "0" \
      "$(grep -c 'marker not found' "$work/big-ll.out" || true)"
check "  ...and the output is the section, not a stub" "1" \
      "$(head -c 64 "$work/big-ll.out" | grep -c '^## 7.2 ' || true)"

echo
if [ "$fail" = "0" ]; then
  echo "show-from-marker acceptance: all $ran checks passed"
else
  echo "show-from-marker acceptance: $fail of $ran FAILED"
fi
exit "$fail"
