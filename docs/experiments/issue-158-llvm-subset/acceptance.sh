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
#    int-arithmetic.oath is put SECOND and does not redeclare List or Str: it
#    reuses the bindings show-from-marker.oath made, so the two files describe
#    one store rather than two versions of the same datatypes.
if ! OATH_AUTHOR="${OATH_AUTHOR:-claude-main}" "$oath" put "$here/show-from-marker.oath" --new > "$work/put.txt" 2>&1; then
  echo "FAIL setup: oath put failed"; cat "$work/put.txt"; exit 1
fi
if ! OATH_AUTHOR="${OATH_AUTHOR:-claude-main}" "$oath" put "$here/int-arithmetic.oath" --new > "$work/put-int.txt" 2>&1; then
  echo "FAIL setup: oath put of the arithmetic entries failed"; cat "$work/put-int.txt"; exit 1
fi
if ! OATH_AUTHOR="${OATH_AUTHOR:-claude-main}" "$oath" put "$here/str-construction.oath" --new > "$work/put-str.txt" 2>&1; then
  echo "FAIL setup: oath put of the Str construction entries failed"; cat "$work/put-str.txt"; exit 1
fi
check "every definition reaches at least 'tested'" "0" \
      "$(cat "$work/put.txt" "$work/put-int.txt" "$work/put-str.txt" | grep -Ec 'asserted|falsified|✗' || true)"

# 2. BUILD, both backends. A refusal from the LLVM backend here is this
#    milestone's premise — that the subset holds no useful program — coming true.
if ! "$oath" build show-from-marker -o "$work/go-show-from-marker" > "$work/build-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused"; cat "$work/build-go.txt"; exit 1
fi
if ! "$oath" build show-from-marker --backend llvm -o "$work/llvm-show-from-marker" > "$work/build-llvm.txt" 2>&1; then
  echo "FAIL: the LLVM backend refused to compile show-from-marker"
  cat "$work/build-llvm.txt"; exit 1
fi
check "the LLVM backend names its lowering" "1" \
      "$(grep -c 'backend: llvm-ir/1' "$work/build-llvm.txt" || true)"
check "the artifact declares the file_read requirement" "1" \
      "$(grep -c 'requires: readfile (file_read)' "$work/build-llvm.txt" || true)"

# 3. THE FAIL-CLOSED CONTROL. `rat-floored` is verified, compiles under the Go
#    backend, and needs `floor` over a Rat — the one thing separating it from
#    everything the subset now reaches. The LLVM backend must refuse it BY NAME.
#    Without this, everything above is equally consistent with the subset's
#    refusals having been removed rather than with the programs having been
#    written inside them.
#
#    THE CONTROL HAS MOVED FOUR TIMES, and every move was forced by the same
#    rule. It was `line-count-report` (needs `+`) while the backend had only
#    `==`; #166's checked-`+` prototype moved `+` inside, so it became
#    `line-pair-report` (needs `*`); arbitrary-precision Int moved
#    `+ - * == < <=` inside, so it became `int-halved` (needs `/`); `/` and `%`
#    then landed too, so it became `int-negated` (needs `neg`); and `neg` has
#    now landed, so it is `rat-floored`. Each time the contract changed
#    deliberately and the replacement had to pin the new one AT LEAST AS TIGHTLY
#    — which a refusal alone cannot do. A refusal witnesses that something is
#    still outside the boundary; it does not witness that the boundary MOVED.
#    So every operation that came inside is checked POSITIVELY, three ways,
#    below — `int-negated` among them, as of this move.
#
#    THIS CONTROL IS STRONGER THAN THE ONE IT REPLACES, which is worth saying
#    because the previous move had to admit the opposite. `neg` was a LOWERING
#    gap — `(- 0 x)` reached the same value inside the subset — so refusing it
#    excluded a spelling rather than a semantics. Rat has no equivalent
#    spelling: the emitted runtime has no rational, and there is nothing inside
#    the subset that computes `(floor (to-rat n))`.
#
#    AND A SECOND CONTROL SITS BESIDE IT, on the TYPE GUARD rather than on the
#    operation. `neg` admits Int, Rat and Float and only Int is lowered, so a
#    backend that keyed the new lowering on the operator NAME would pass every
#    check in this script — including `int-negated` — while handing a Rat to an
#    integer runtime. `rat-negated` is the entry that tells the two apart.
if ! "$oath" build rat-floored -o "$work/go-control" > "$work/build-control-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused the control, so it is not a BACKEND-SUBSET boundary"
  cat "$work/build-control-go.txt"; exit 1
fi
"$oath" build rat-floored --backend llvm -o "$work/llvm-control" > "$work/build-control.txt" 2>&1 && rc=0 || rc=$?
check "the LLVM backend still refuses what it cannot lower" "1" \
      "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "  ...and names the primitive it lacks" "1" \
      "$(grep -c 'primitive operation "floor"' "$work/build-control.txt" || true)"
check "  ...and emitted no executable" "0" \
      "$([ -e "$work/llvm-control" ] && echo 1 || echo 0)"

if ! "$oath" build rat-negated -o "$work/go-guard" > "$work/build-guard-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused the type-guard control"
  cat "$work/build-guard-go.txt"; exit 1
fi
"$oath" build rat-negated --backend llvm -o "$work/llvm-guard" > "$work/build-guard.txt" 2>&1 && rc=0 || rc=$?
check "the LLVM backend refuses 'neg' at Rat" "1" \
      "$([ "$rc" != "0" ] && echo 1 || echo 0)"
# BY NAME, and the name is the point: a refusal naming `==` or `<` would mean
# the Rat was caught by a neighbouring operator and this control witnessed
# nothing about the guard on `neg` itself.
check "  ...and names 'neg' rather than a neighbouring operator" "1" \
      "$(grep -c 'primitive operation "neg"' "$work/build-guard.txt" || true)"

# 3b. THE OTHER HALF: the entry that needs `+` must now COMPILE and agree three
#     ways. A refusal here would mean checked `+` never reached the emitter, and
#     a wrong answer would mean it reached it and computes something else — both
#     invisible to the refusal check above.
if ! "$oath" build line-count-report -o "$work/go-line-count-report" > "$work/build-lcr-go.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused line-count-report"; cat "$work/build-lcr-go.txt"; exit 1
fi
"$oath" build line-count-report --backend llvm -o "$work/llvm-line-count-report" > "$work/build-lcr-ll.txt" 2>&1 && rc=0 || rc=$?
check "the LLVM backend now compiles the entry that needs '+'" "0" "$rc"

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
three_way() { # three_way <entry> [argv...]
  entry=$1; shift
  argv=$(argv_literal "$@")
  rc=0
  "$oath" eval "($entry $caps $argv)" > "$work/eval.txt" 2> "$work/eval.err" || rc=$?
  if [ "$rc" != "0" ]; then
    printf 'the interpreter exited %s on this input' "$rc"; return 0
  fi
  want=$( { decode_str "$work/eval.txt"; printf X; } ); want=${want%X}

  rc_go=0; "$work/go-$entry" "$@" > "$work/go.out" 2> "$work/go.err" || rc_go=$?
  rc_ll=0; "$work/llvm-$entry" "$@" > "$work/ll.out" 2> "$work/ll.err" || rc_ll=$?
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
      "$(three_way show-from-marker "$work/sample.txt" "---$nl")"

# The passing checks, recorded only now that the gate has been seen to fire.
set_reference_file "$work/sample.txt"
check "reads from the marker onwards"                    "" "$(three_way show-from-marker "$work/sample.txt" "---$nl")"
check "a marker at the very start returns the whole file" "" "$(three_way show-from-marker "$work/sample.txt" 'name')"
check "a marker matching the last line"                  "" "$(three_way show-from-marker "$work/sample.txt" 'trailing')"
check "an absent marker is reported, not empty output"   "" "$(three_way show-from-marker "$work/sample.txt" 'no-such-marker')"
check "a non-ASCII marker"                               "" "$(three_way show-from-marker "$work/sample.txt" 'café')"
check "an empty marker matches at position 0"            "" "$(three_way show-from-marker "$work/sample.txt" '')"
check "no arguments is the usage line"                   "" "$(three_way show-from-marker)"
check "one argument is the usage line"                   "" "$(three_way show-from-marker "$work/sample.txt")"

# The `+` half of the moved boundary, through the same comparator: the entry
# that used to be refused now has to give the interpreter's answer on both of
# its branches, not merely build.
check "the '+' entry agrees three ways on a file with newlines" "" \
      "$(three_way line-count-report "$work/sample.txt")"
check "  ...and on no arguments"                               "" \
      "$(three_way line-count-report)"
check "  ...and it really counted, rather than defaulting"     "has newlines" \
      "$("$work/llvm-line-count-report" "$work/sample.txt" | tr -d '\n')"

# ---------- #166: ARBITRARY-PRECISION Int, THREE WAYS ----------
#
# Every operation that came inside the boundary, checked positively. `oath eval`
# reads these literals through the kernel's big.Int and the LLVM artifact
# through the emitted C runtime's own decimal parser, so agreement is between
# two independent readings of the same digits rather than one computation
# written twice.
#
# TWO ASSERTIONS PER FAMILY, and the second is not redundant. `three_way` says
# the paths AGREE; it does not say they agree on "ok". Each entry returns a
# literal naming the first case that failed, so three identically wrong
# implementations would agree on `wrong-2-to-the-64` and pass the first check
# alone. The second check reads the answer.
#
# caps is emptied because these entries take only argv — the viewer's capability
# record is not part of their signature, and passing one would be a type error
# rather than a disagreement.
caps=""
for n in int-large-literals int-carry int-sign int-cancellation int-multiplication int-ordering \
         int-halved int-division-exactness int-division-signs int-division-boundaries \
         int-divmod-identity int-negated; do
  if ! "$oath" build "$n" -o "$work/go-$n" > "$work/b-go-$n.txt" 2>&1; then
    echo "FAIL setup: the Go backend refused $n"; cat "$work/b-go-$n.txt"; exit 1
  fi
  if ! "$oath" build "$n" --backend llvm -o "$work/llvm-$n" > "$work/b-ll-$n.txt" 2>&1; then
    echo "FAIL: the LLVM backend refused $n — arbitrary-precision Int is not reaching the emitter"
    cat "$work/b-ll-$n.txt"; exit 1
  fi
  check "$n agrees three ways" "" "$(three_way "$n")"
  check "  ...and the answer is 'ok', not an agreed-wrong one" "ok" \
        "$("$work/llvm-$n" | tr -d '\n')"
done

# And one magnitude no int64 could hold, read back out of the artifact rather
# than out of a comparison: 2^1024 - 1 has 309 decimal digits, and the old
# backend refused the literal at compile time.
check "a 309-digit literal survives compilation" "ok" \
      "$("$work/llvm-int-large-literals" | tr -d '\n')"

# ---------- THE ZERO-DIVISOR CONTROLS ----------
#
# A zero divisor is a runtime FAILURE, so it is not compared like everything
# above — each path is asked separately and all three must fail NAMING the same
# condition. The divisor is derived from runtime input and is zero for any
# input, so the failure cannot have been decided at compile time.
#
# THE THREE PATHS FAIL DIFFERENTLY AND THAT IS NOT PAPERED OVER: the interpreter
# reports an error and exits 1, the Go backend panics out of big.Int and exits 2,
# and the LLVM artifact exits 70, the code its runtime uses for every refusal.
# What is asserted is what the LANGUAGE determines — that the operation fails and
# names the condition — rather than a host framing no specification fixes.
#
# The no-argument branch is checked FIRST, three ways. Without it, "the binary
# exits nonzero when given an argument" is equally consistent with a binary that
# cannot run at all.
for n in int-divide-by-zero int-modulo-by-zero; do
  if ! "$oath" build "$n" -o "$work/go-$n" > "$work/b-go-$n.txt" 2>&1; then
    echo "FAIL setup: the Go backend refused $n"; cat "$work/b-go-$n.txt"; exit 1
  fi
  if ! "$oath" build "$n" --backend llvm -o "$work/llvm-$n" > "$work/b-ll-$n.txt" 2>&1; then
    echo "FAIL: the LLVM backend refused $n at BUILD time. A zero divisor is a"
    echo "      runtime condition here, so refusing the program is the wrong answer."
    cat "$work/b-ll-$n.txt"; exit 1
  fi
  check "$n runs normally when not asked to divide" "" "$(three_way "$n")"
  check "  ...and takes the no-argument branch" "no-argument" \
        "$("$work/llvm-$n" | tr -d '\n')"
done

# `division by zero` / `modulo by zero` is the phrase `oath eval` uses, so it is
# the phrase all three are held to. Naming the condition is the assertion; the
# wording around it is each host's own.
for pair in "int-divide-by-zero:division by zero" "int-modulo-by-zero:modulo by zero"; do
  n=${pair%%:*}; phrase=${pair#*:}

  rc_e=0; "$oath" eval "($n (Cons [Str] \"x\" (Nil [Str])))" > "$work/dz-eval.txt" 2>&1 || rc_e=$?
  check "$n: the interpreter fails" "1" "$([ "$rc_e" != "0" ] && echo 1 || echo 0)"
  check "  ...naming the condition" "1" "$(grep -c "$phrase" "$work/dz-eval.txt" || true)"

  rc_g=0; "$work/go-$n" x > "$work/dz-go.txt" 2>&1 || rc_g=$?
  check "  ...the Go backend fails" "1" "$([ "$rc_g" != "0" ] && echo 1 || echo 0)"
  check "  ...naming the condition" "1" "$(grep -c "$phrase" "$work/dz-go.txt" || true)"

  rc_l=0; "$work/llvm-$n" x > "$work/dz-ll.out" 2> "$work/dz-ll.err" || rc_l=$?
  check "  ...the LLVM backend fails" "70" "$rc_l"
  check "  ...naming the condition" "1" "$(grep -c "$phrase" "$work/dz-ll.err" || true)"
  check "  ...and printing no answer" "" "$(cat "$work/dz-ll.out")"
done

# ---------- #164: A Str BUILT FROM RUNTIME VALUES, THREE WAYS ----------
#
# The LLVM backend folded a literal Str chain and REFUSED every other one, which
# bounded it to results that are a literal or a SUFFIX of an input: it could
# search and echo, never report. Each entry here returns bytes that appear in no
# literal in the program and are not a suffix of any argument, so a backend still
# folding cannot produce them by accident.
#
# The fail-closed control did not move for #164, and that was the point: #164
# widened Str construction, not the primitive vocabulary, so the thing
# separating "the subset grew" from "the refusals were deleted" needed no
# replacement. A control that moves whenever anything lands is not measuring a
# boundary. It moved for `neg` because `neg` is what it excluded.
for n in str-report str-shifted str-mirrored str-held-tail; do
  if ! "$oath" build "$n" -o "$work/go-$n" > "$work/b-go-$n.txt" 2>&1; then
    echo "FAIL setup: the Go backend refused $n"; cat "$work/b-go-$n.txt"; exit 1
  fi
  if ! "$oath" build "$n" --backend llvm -o "$work/llvm-$n" > "$work/b-ll-$n.txt" 2>&1; then
    echo "FAIL: the LLVM backend refused $n — runtime Str construction is not reaching the emitter"
    cat "$work/b-ll-$n.txt"; exit 1
  fi
done

# TWO ASSERTIONS PER ENTRY, for the reason the arithmetic block gives: agreement
# is not correctness. Three paths that all returned the argument unchanged would
# agree, so the second check reads the answer a person would read.
check "str-report agrees three ways with no arguments" "" "$(three_way str-report)"
check "str-report agrees three ways on a key"          "" "$(three_way str-report host)"
check "  ...and really built the message"  "missing key: host" \
      "$("$work/llvm-str-report" host | tr -d '\n')"
check "str-report on a multi-byte key"                 "" "$(three_way str-report 'café')"
check "  ...and the non-ASCII survived construction" "missing key: café" \
      "$("$work/llvm-str-report" 'café' | tr -d '\n')"

# Every codepoint here is arithmetic on an input codepoint, so the result is in
# no literal and is not a substring of the input.
check "str-shifted agrees three ways"                  "" "$(three_way str-shifted HAL)"
check "  ...and the codepoints really moved"      "IBM" \
      "$("$work/llvm-str-shifted" HAL | tr -d '\n')"
check "str-shifted on an empty argument"               "" "$(three_way str-shifted '')"
check "str-shifted with no arguments"                  "" "$(three_way str-shifted)"

# Order-reversing, so agreement cannot come from a lowering that hands back a
# view of its argument.
check "str-mirrored agrees three ways"                 "" "$(three_way str-mirrored ab)"
check "  ...and the result is not a view of the input" "abba" \
      "$("$work/llvm-str-mirrored" ab | tr -d '\n')"
check "str-mirrored on a multi-byte argument"          "" "$(three_way str-mirrored 'café')"

# A TAIL HELD ACROSS LATER CONSTRUCTION. `o_str_tail` returns a pointer INTO its
# parent's buffer; runtime construction is the first ALLOCATING producer of Str
# buffers, so an allocator that reused or freed one would break a view taken
# before it — silently, and only for a program that holds a tail across a later
# construction. This entry is that program.
check "str-held-tail agrees three ways"                "" "$(three_way str-held-tail a)"
check "  ...and on a long argument"                    "" \
      "$(three_way str-held-tail 'a longer argument, held across several later allocations')"
check "  ...and the held tail is still intact"    "yab0123456789a" \
      "$("$work/llvm-str-held-tail" a | tr -d '\n')"

# ---------- #164: THE NON-SCALAR CLASSES ----------
#
# THE THREE PATHS DISAGREE HERE BY DESIGN, so this is not run through
# `three_way`; each is asked separately, as the zero-divisor block does. The
# disagreement is not a divergence, it is the honest subset boundary: SPEC 3 says
# construction is UNCHECKED and a KERNEL MUST NOT reject a non-scalar element,
# and says in the same place that an implementation storing a Str as packed UTF-8
# performs PACK at construction and may refuse there. So the interpreter accepts
# what both packed backends refuse, and each side is asserted on its own terms.
#
# THE TWO BACKENDS FAIL DIFFERENTLY AND THAT IS NOT PAPERED OVER, exactly as with
# a zero divisor: the Go backend panics and exits 2, the LLVM artifact exits 70,
# the code its runtime uses for every refusal. No specification fixes either, so
# what is asserted of both is what the LANGUAGE determines — that construction
# FAILS and NAMES the element — and the exit code is asserted only of the LLVM
# artifact, whose own runtime does fix it.
#
# The head of each SCons is computed from an INPUT codepoint, so nothing folds
# and the compile-time scan cannot see it: this is the runtime path and nothing
# else. Each entry returns "" for no arguments, which is what makes "it exits
# nonzero" evidence about the ELEMENT rather than about a binary that cannot run.
for n in str-nonscalar-negative str-nonscalar-surrogate str-nonscalar-above-max \
         str-nonscalar-astronomical; do
  if ! "$oath" build "$n" -o "$work/go-$n" > "$work/b-go-$n.txt" 2>&1; then
    echo "FAIL setup: the Go backend refused $n at BUILD time"; cat "$work/b-go-$n.txt"; exit 1
  fi
  if ! "$oath" build "$n" --backend llvm -o "$work/llvm-$n" > "$work/b-ll-$n.txt" 2>&1; then
    echo "FAIL: the LLVM backend refused $n at BUILD time. The element is computed from"
    echo "      runtime input, so refusing the PROGRAM is the wrong answer — the check"
    echo "      belongs at construction, not at compile time."
    cat "$work/b-ll-$n.txt"; exit 1
  fi
  # THE CONTROL, FIRST: the artifact must RUN. Without it every refusal below is
  # equally consistent with a binary that dies whatever it is given.
  check "$n runs when not asked to build a non-scalar" "" "$(three_way "$n")"
done

: > "$work/ns-messages.txt"
for pair in "str-nonscalar-negative:-65:(negative)" \
            "str-nonscalar-surrogate:55296:(a surrogate, 0xD800..0xDFFF)" \
            "str-nonscalar-above-max:1114112:(above the maximum scalar 0x10FFFF)" \
            "str-nonscalar-astronomical:65000000000000000000000:(above the maximum scalar 0x10FFFF)"; do
  n=${pair%%:*}; rest=${pair#*:}; el=${rest%%:*}; cls=${rest#*:}

  # THE KERNEL'S SIDE OF SPEC 3. The interpreter must CONSTRUCT the value, not
  # refuse it — and the element must appear in the result, so "it exited 0" is
  # not satisfied by an interpreter that quietly returned something else.
  rc_e=0; "$oath" eval "($n (Cons [Str] \"A\" (Nil [Str])))" > "$work/ns-eval.txt" 2>&1 || rc_e=$?
  check "$n: the interpreter ACCEPTS the runtime non-scalar" "0" "$rc_e"
  check "  ...and carries the element in the value" "1" \
        "$(grep -c -- "SCons $el" "$work/ns-eval.txt" || true)"

  # The Go backend. Nonzero and NAMING the element; the exit code is its own.
  rc_g=0; "$work/go-$n" A > "$work/ns-go.out" 2> "$work/ns-go.err" || rc_g=$?
  cat "$work/ns-go.out" "$work/ns-go.err" > "$work/ns-go.txt"
  check "  ...the Go backend refuses" "1" "$([ "$rc_g" != "0" ] && echo 1 || echo 0)"
  check "  ...naming the element" "1" "$(grep -cF -- "$el" "$work/ns-go.txt" || true)"

  # The LLVM backend. Exit 70, the element AND its class, and nothing on stdout —
  # a partial answer followed by a refusal would be worse than either.
  rc_l=0; "$work/llvm-$n" A > "$work/ns-ll.out" 2> "$work/ns-ll.err" || rc_l=$?
  check "  ...the LLVM backend refuses" "70" "$rc_l"
  check "  ...naming the element" "1" "$(grep -cF -- "$el" "$work/ns-ll.err" || true)"
  check "  ...and its class" "1" "$(grep -cF -- "$cls" "$work/ns-ll.err" || true)"
  check "  ...and printing no answer" "" "$(cat "$work/ns-ll.out")"

  # NO SUBSTITUTION, asserted directly rather than inferred from the exit code.
  # U+FFFD is what `string(rune(n))` produced before either backend refused, and
  # it made -1, 55296 and 1114112 into the same three bytes.
  #
  # BOTH STREAMS, and the first draft of this check read stderr alone — which is
  # exactly the wrong half. A backend that substituted rather than refused would
  # print its replacement to STDOUT and say nothing on stderr, so the check as
  # first written could not fail for the reason its label gives. Found by
  # building the mutant it was supposed to catch, not by rereading it.
  cat "$work/ns-ll.out" "$work/ns-ll.err" > "$work/ns-ll.txt"
  check "  ...and substituting nothing, on either stream" "0 0" \
        "$(grep -c '�' "$work/ns-go.txt" || true) $(grep -c '�' "$work/ns-ll.txt" || true)"

  cat "$work/ns-ll.err" >> "$work/ns-messages.txt"
done

# AND THE MESSAGES STAY DISTINGUISHABLE. Collapsing them would be the same defect
# as U+FFFD — distinct refused inputs producing one output — relocated from the
# result into the diagnostic, where nothing else here would see it.
check "the four refusals are four distinct messages" "4" \
      "$(sort -u "$work/ns-messages.txt" | grep -c 'cannot encode Str element' || true)"

# AN ASYMMETRY NOTED AND DELIBERATELY NOT ASSERTED. The Go backend tests
# `IsInt64` before it classifies, so past int64 it names the element without its
# class while the LLVM backend names both. Neither substitutes and both stay
# distinguishable, so it is a diagnostic difference rather than a semantic one —
# and pinning it would make a future improvement to that message turn this gate
# red for no reason a reader could act on. What is asserted of the Go backend
# above is the established contract: it REFUSES and it NAMES THE ELEMENT. Both
# hold whether or not the class is ever added.

# And the observable behaviour a user would check by eye, so three-way agreement
# on the WRONG answer cannot pass unnoticed.
check "the found output starts with the marker" "---" \
      "$("$work/llvm-show-from-marker" "$work/sample.txt" "---$nl" | head -c 3)"
check "the absent-marker message is the artifact's own" \
      "show-from-marker: marker not found" \
      "$("$work/llvm-show-from-marker" "$work/sample.txt" 'no-such-marker')"
check "the usage line is the artifact's own" \
      "usage: show-from-marker <file> <marker>" \
      "$("$work/llvm-show-from-marker")"

# A LARGE REAL FILE. The interpreter is not the reference here — a multi-kilobyte
# Oath literal is not the same input by any useful definition — so this compares
# the two backends only, and says so.
#
# The runs are NOT piped into `shasum`: a pipeline reports the status of its LAST
# command, so two crashing programs would hash two empty outputs and agree. Their
# statuses are checked first, and only then their bytes.
big_go=0; "$work/go-show-from-marker"   "$root/docs/SPEC.md" '## 7.2 ' > "$work/big-go.out"   2>&1 || big_go=$?
big_ll=0; "$work/llvm-show-from-marker" "$root/docs/SPEC.md" '## 7.2 ' > "$work/big-ll.out" 2>&1 || big_ll=$?
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
