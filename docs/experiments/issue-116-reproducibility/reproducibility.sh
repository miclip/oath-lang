#!/bin/sh
# BUILD REPRODUCIBILITY FOR #116 — is a compiled artifact's digest a function of
# the closure that produced it?
#
# #116 asks what a signature would be taken OVER. Its fourth bullet is the
# premise this script measures: "two builds of the same closure should produce
# the same digest, or a signature is over a moving target." Nothing here signs
# anything, and nothing here changes the kernel — this is a MEASUREMENT of what
# the current `oath build` already produces.
#
# It runs through the REAL CLI into a scratch OATH_STORE, for the reason
# ../issue-158-llvm-subset/acceptance.sh gives: the claim is about artifacts a
# person builds, not about what an in-process harness can arrange.
#
# WHAT IS ASSERTED AND WHAT IS ONLY REPORTED — the distinction is the whole
# design, because one of the findings is INTERMITTENT and an assertion over it
# would be a gate that fails at random:
#
#   ASSERTED  the controls, and the basename finding. All deterministic: each
#             has been observed to hold on every run, and each would fail for a
#             stated reason if the property it names stopped holding.
#   REPORTED  the repeated-build digest counts. The emitter's output varies
#             between runs (see README), so "N builds agreed" is a SAMPLE. A
#             run that happens to be uniform is not evidence of determinism,
#             and this script does not let its exit status say otherwise.
#
# The controls come FIRST and are what make the rest mean anything: a
# comparison that cannot distinguish a mutated artifact from an untouched one
# would report "identical" for every cell below, and that is exactly what a
# reproducible build looks like. `check_detects` is the gate on the gate.
#
# Requires: go, clang (the LLVM backend shells out to it), python3 (to mutate a
# byte), codesign + otool (macOS; the signature checks are skipped elsewhere and
# say so rather than passing silently).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
work=$(mktemp -d)
store=$(mktemp -d)
cleanup() { rm -rf "$work" "$store"; }
trap cleanup EXIT INT TERM

# The scratch store is the ONLY store this script writes to. `codebase/` is the
# committed, append-only corpus and a stray `put` into it binds a permanent
# name — so OATH_STORE is exported before any oath command runs, and every
# `put` below carries `--new` because that is what the canonical-store guard
# requires for a fresh binding.
export OATH_STORE="$store"

# How many times to rebuild the same program per backend. This is a SAMPLE
# SIZE, not a threshold: the script reports how many distinct digests it saw
# and does not fail on the answer.
#
# VALIDATED, because it is the one input that can silently delete the
# measurement: a zero, a negative or a non-numeric value makes both sampling
# loops run zero builds, and the script would then print "all asserted checks
# passed" over a measurement that never happened. A harness that cannot tell a
# broken setup from a clean run is worse than no harness.
REPEATS="${REPRO_REPEATS:-8}"
case "$REPEATS" in
  ''|*[!0-9]*) echo "FAIL setup: REPRO_REPEATS must be a positive integer, got [$REPEATS]"; exit 1 ;;
esac
[ "$REPEATS" -ge 1 ] || { echo "FAIL setup: REPRO_REPEATS must be >= 1, got [$REPEATS]"; exit 1; }

if ! command -v clang >/dev/null 2>&1; then
  if [ -n "${CI:-}" ]; then
    echo "clang is absent, so the LLVM backend did not run. In CI that is a failure, not a skip."
    exit 1
  fi
  echo "clang not available (local); skipping. This is a hard failure under CI=1."
  exit 0
fi

# BUILT FROM THIS CHECKOUT, for the reason the #158 script gives: `oath/oath` is
# a gitignored artifact that can predate the source, and `build` is precisely
# what this script measures. OATH_BIN overrides, for measuring a specific binary
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
note() { printf '  --    %s\n' "$1"; }

digest() { shasum -a 256 < "$1" | cut -d' ' -f1; }

# ---------- setup: one program, in a scratch store ----------
#
# show-from-marker.oath is #158's, and it is self-contained — it declares List
# and Str itself, so an empty store is all it needs. It is reused rather than
# copied because a second copy would drift from the original and the two would
# then describe different programs under one name.
#
# It also happens to be the right SHAPE for this measurement: `str-contains` and
# `str-tail-from` are siblings in the dependency closure with no edge between
# them, which is the configuration whose emission order is unpinned.
src="$root/docs/experiments/issue-158-llvm-subset/show-from-marker.oath"
[ -f "$src" ] || { echo "FAIL setup: $src is missing"; exit 1; }
if ! OATH_AUTHOR="${OATH_AUTHOR:-claude-main}" "$oath" put "$src" --new > "$work/put.txt" 2>&1; then
  echo "FAIL setup: oath put failed"; cat "$work/put.txt"; exit 1
fi
check "the program reaches at least 'tested'" "0" \
      "$(grep -Ec 'asserted|falsified|✗' "$work/put.txt" || true)"

mkdir -p "$work/ctl"
if ! "$oath" build show-from-marker -o "$work/ctl/go" > "$work/b1.txt" 2>&1; then
  echo "FAIL setup: the Go backend refused"; cat "$work/b1.txt"; exit 1
fi
if ! "$oath" build show-from-marker --backend llvm -o "$work/ctl/llvm" > "$work/b2.txt" 2>&1; then
  echo "FAIL setup: the LLVM backend refused"; cat "$work/b2.txt"; exit 1
fi

echo
echo "CONTROLS — can the comparison tell a mutated artifact from an untouched one?"
echo

# The mutator is an INSTRUMENT, so its own failure modes are closed first.
#
# TWO of them, both found by review rather than by reading:
#
#   1. Writing to a fresh path drops the execute bit (python's open() creates
#      mode 0644), which once made a flipped artifact look like it had been
#      REJECTED BY THE KERNEL when it had merely been made unrunnable here.
#      The mode is restored and asserted below.
#   2. A flip at an arbitrary offset can land INSIDE the embedded provenance
#      record. The reader would then correctly report a changed record, and the
#      "manifest is still read" check would fail for a reason opposite to its
#      label — the reader working, not breaking. So the flip offset is chosen to
#      avoid the record, and `mutate` PRINTS `SAFE` or `UNSAFE` for the caller
#      to assert rather than deciding silently.
#
# The record's byte range is found by locating the exact text `oath provenance`
# prints, which is the raw embedded record; a margin covers the surrounding
# marker bytes.
mutate() { # mutate <src> <dst> <mode: flip|append> [provenance-text-file]
  python3 - "$1" "$2" "$3" "${4:-}" <<'PY'
import sys
src, dst, mode = sys.argv[1], sys.argv[2], sys.argv[3]
provfile = sys.argv[4] if len(sys.argv) > 4 else ''
b = bytearray(open(src, 'rb').read())

if mode == 'append':
    b += b'X'
    open(dst, 'wb').write(bytes(b)); print('SAFE append'); raise SystemExit

if mode != 'flip':
    sys.exit("unknown mutation %r" % mode)

# Every byte range the provenance record occupies, plus a margin for markers.
MARGIN = 256
forbidden = []
if provfile:
    raw = open(provfile, 'rb').read().strip()
    if raw:
        i = b.find(raw)
        while i != -1:
            forbidden.append((i - MARGIN, i + len(raw) + MARGIN))
            i = b.find(raw, i + 1)

def inside(o):
    return any(lo <= o < hi for lo, hi in forbidden)

off = len(b) // 3
if inside(off):                      # walk forward to the first safe offset
    for cand in range(off, len(b)):
        if not inside(cand):
            off = cand
            break
open(dst, 'wb').write(bytes(b[:off] + bytearray([b[off] ^ 0x01]) + b[off + 1:]))
# UNSAFE is reported rather than raised: the caller asserts it, so a future
# layout change surfaces as a named failing check instead of a stack trace.
print(('SAFE' if (forbidden and not inside(off)) else 'UNSAFE'), 'flip', off)
PY
  chmod +x "$2"
}

for k in go llvm; do
  cp "$work/ctl/$k" "$work/ctl/$k-copy"
  # The ORIGINAL manifest, captured before anything is mutated. It is both the
  # region the flip must avoid and the reference the mutants' manifests are
  # compared against.
  orc=0; "$oath" provenance "$work/ctl/$k" > "$work/ctl/$k.prov" 2>/dev/null || orc=$?
  check "$k: the unmutated artifact's manifest reads" "0" "$orc"

  fs=$(mutate "$work/ctl/$k" "$work/ctl/$k-flip" flip "$work/ctl/$k.prov")
  mutate "$work/ctl/$k" "$work/ctl/$k-append" append > /dev/null
  # The flip must land OUTSIDE the embedded record, or the manifest check below
  # would be measuring the reader noticing a changed record.
  check "$k: the flip lands outside the provenance record" "SAFE" "$(printf '%s' "$fs" | cut -d' ' -f1)"

  # CONTROL A — the comparison must call an untouched copy IDENTICAL. Without
  # this, "detected a mutation" is equally consistent with a comparison that
  # reports every pair as different, which would make every reproducibility
  # result below meaningless in the other direction.
  check "$k: an untouched copy compares identical" \
        "$(digest "$work/ctl/$k")" "$(digest "$work/ctl/$k-copy")"

  # CONTROL B — and it must call a mutated one DIFFERENT. Two mutations,
  # because they are not the same threat: a flipped byte rewrites code in
  # place, while an appended byte is what a naive "attach the signature to the
  # end of the file" scheme would do, and #116 asks about exactly that.
  for v in flip append; do
    if [ "$(digest "$work/ctl/$k")" != "$(digest "$work/ctl/$k-$v")" ]; then r=detected; else r=missed; fi
    check "$k: a mutation ($v) is detected" "detected" "$r"
  done

  # The mutants must remain EXECUTABLE files, or "it did not run" below would be
  # a fact about this script rather than about the artifact.
  check "$k: the mutants kept the execute bit" "yes" \
        "$([ -x "$work/ctl/$k-flip" ] && [ -x "$work/ctl/$k-append" ] && echo yes || echo no)"

  # THE READER STAYS A READER, measured rather than assumed. #116's own framing
  # is that `oath provenance` reports a manifest FAITHFULLY, faithfulness being
  # the whole of the claim — so a tampered artifact must still be read, and read
  # correctly. This is what makes "reading is not verifying" a fact about the
  # command instead of a position, and it is cited by the design disposition in
  # the README, which is why it is asserted here rather than described there.
  # BOTH the exit status and the content are asserted, and they are captured
  # SEPARATELY on purpose: a POSIX pipeline reports the status of its LAST
  # command, so piping `oath provenance` into a parser would report python's
  # status and silently accept a provenance command that printed a usable
  # manifest and then failed. The output goes to a file first, and the status is
  # taken from the command itself.
  # The WHOLE manifest is compared, not a field this script thought to name. A
  # hand-picked field (`entry`) would pass while `backend`, `kernel` or a hash
  # changed underneath it — the same defect as any hand-written structural
  # matcher, which compares what its author remembered and accepts the rest.
  for v in flip append; do
    prc=0; "$oath" provenance "$work/ctl/$k-$v" > "$work/ctl/$k-$v.prov" 2>/dev/null || prc=$?
    check "$k: a $v mutant's manifest is still READ, not refused" "0" "$prc"
    check "  ...and is byte-identical to the original's" \
          "$(digest "$work/ctl/$k.prov")" "$(digest "$work/ctl/$k-$v.prov")"
  done
done

# ---------- what the host's own signature check says ----------
#
# REPORTED SEPARATELY FROM EXECUTION, because they disagree, and the
# disagreement is the finding rather than a wrinkle: `codesign` rejects an
# appended artifact that the kernel will still happily run.
if command -v codesign >/dev/null 2>&1; then
  echo
  echo "  the host code signature (macOS ad-hoc, applied by the linker):"
  for k in go llvm; do
    check "$k: the artifact as built passes codesign --verify --strict" "PASS" \
          "$(codesign --verify --strict "$work/ctl/$k" >/dev/null 2>&1 && echo PASS || echo FAIL)"
    for v in flip append; do
      check "  ...and a mutation ($v) FAILS it" "FAIL" \
            "$(codesign --verify --strict "$work/ctl/$k-$v" >/dev/null 2>&1 && echo PASS || echo FAIL)"
    done
    # NOT asserted: whether the mutant still RUNS. A flipped byte may land in a
    # region the loader never validates or the program never reaches, so
    # "it ran" is not a property of the mutation. It is printed because the
    # APPEND row is the one #116 needs: appended bytes break the signature and
    # do not stop the program.
    # Run through an inner `sh` whose OWN stderr is discarded. A mutant killed by
    # a signal makes the running shell print "Killed: 9" to stderr, which would
    # land in the middle of this report looking like the script had crashed.
    for v in append flip; do
      if sh -c '"$0" >/dev/null 2>&1 </dev/null' "$work/ctl/$k-$v" 2>/dev/null; then
        rr=ran
      else
        rr="rc=$?"
      fi
      note "$k $v: codesign rejects it; executing it gives [$rr]"
    done
  done
else
  note "codesign is absent (not macOS): the signature rows did not run"
fi

# ---------- the deterministic finding: the output FILENAME is inside the bytes ----------
#
# Same store, same closure, same command, same directory — only the output name
# differs. If the name ends up INSIDE the artifact, the digest is not a function
# of the closure, and a signature over "the artifact for this closure" has to
# say which FILE it meant.
#
# THIS IS ASSERTED ON THE MECHANISM, NOT ON A DIGEST COMPARISON, and the
# difference is the whole reason this block was rewritten. Comparing
# digest(alpha) against digest(gamma) looks like the obvious test and is
# WORTHLESS here: every Go build already differs from every other Go build
# because `oath build` compiles in a fresh `MkdirTemp` directory (see README
# cause 2), and both emitters can independently pick a different order (cause
# 1). So that comparison would report "differs" — and pass — even if the
# filename had no effect whatever. A check that cannot fail for the reason its
# label gives is not measuring its claim.
#
# What is asserted instead is the embedded signing identifier, which is immune
# to both confounders: the linker's ad-hoc signature carries the output BASENAME
# as its Identifier, so reading it back out of each artifact observes the
# filename inside the bytes directly. Two different names must yield two
# different identifiers, each equal to its own basename.
echo
echo "DETERMINISTIC FINDING — is the output filename inside the artifact?"
echo
mkdir -p "$work/name"
if command -v codesign >/dev/null 2>&1; then
  identifier() { codesign -dvvv "$1" 2>&1 | sed -n 's/^Identifier=//p'; }

  # THE TWO BACKENDS ANSWER DIFFERENTLY, and that contrast is the result rather
  # than an inconvenience — so each is asserted on its own terms instead of
  # being forced through one expectation.
  #
  # LLVM: `clang -o <path>` lets the linker name the signature after the output,
  # so the basename is inside the signed bytes.
  "$oath" build show-from-marker --backend llvm -o "$work/name/alpha" >/dev/null 2>&1
  "$oath" build show-from-marker --backend llvm -o "$work/name/gamma" >/dev/null 2>&1
  check "llvm: the signing identifier IS the output basename" \
        "alpha gamma" "$(identifier "$work/name/alpha") $(identifier "$work/name/gamma")"
  # The CONTROL on that reading: rebuilding the SAME name must give the SAME
  # identifier. Without it, "the two identifiers differed" is equally consistent
  # with an identifier that varies per build for some unrelated reason, which
  # would make the row above say nothing about the filename.
  "$oath" build show-from-marker --backend llvm -o "$work/name/alpha" >/dev/null 2>&1
  check "  ...and is stable across rebuilds of the same name" \
        "alpha" "$(identifier "$work/name/alpha")"

  # Go: the toolchain links to its own internal `a.out` and moves the result, so
  # the identifier is a CONSTANT and the output name is NOT inside the artifact.
  # Asserted because it is the reason the obvious digest comparison is
  # worthless for this backend: two Go builds to two names differ, but they
  # differ for cause 2, not because of the name.
  "$oath" build show-from-marker -o "$work/name/g-alpha" >/dev/null 2>&1
  "$oath" build show-from-marker -o "$work/name/g-gamma" >/dev/null 2>&1
  check "go: the signing identifier is a constant, NOT the basename" \
        "a.out a.out" "$(identifier "$work/name/g-alpha") $(identifier "$work/name/g-gamma")"
  # ---- and cause 3 is REPAIRABLE, which the first version of this record denied ----
  #
  # Linking to a constant basename and moving the result is what Go's own
  # toolchain does, and it is measured here rather than argued: two links of one
  # fixed source under one fixed name, moved to two different destinations, must
  # produce identical bytes. Plain C, because the mechanism belongs to
  # clang/ld and not to anything `oath` emits — using a real Oath artifact would
  # drag causes 1 and 2 back in and measure nothing.
  mkdir -p "$work/remedy"
  cat > "$work/remedy/p.c" <<'CCODE'
#include <stdio.h>
int main(void){ printf("hi\n"); return 0; }
CCODE
  ( cd "$work/remedy" && clang -O1 -o direct-alpha p.c && clang -O1 -o direct-gamma p.c )
  # THE CONTROL FIRST: linked directly under two names, the bytes must DIFFER.
  # Without it, "the remedy produced identical bytes" is equally consistent with
  # a comparison that cannot see the effect the remedy removes.
  if [ "$(digest "$work/remedy/direct-alpha")" = "$(digest "$work/remedy/direct-gamma")" ]; then r=same; else r=differs; fi
  check "clang: linking directly under two names differs (the effect exists)" "differs" "$r"

  ( cd "$work/remedy" && clang -O1 -o fixed-name p.c && mv fixed-name moved-alpha )
  ( cd "$work/remedy" && clang -O1 -o fixed-name p.c && mv fixed-name moved-gamma )
  check "  ...and linking under ONE name then moving is byte-identical" \
        "$(digest "$work/remedy/moved-alpha")" "$(digest "$work/remedy/moved-gamma")"
  check "  ...and the moved artifact still passes codesign --verify --strict" "PASS" \
        "$(codesign --verify --strict "$work/remedy/moved-alpha" >/dev/null 2>&1 && echo PASS || echo FAIL)"
else
  note "codesign is absent (not macOS): the filename rows did not run."
  note "  The mechanism measured here is the Mach-O ad-hoc signature; the"
  note "  equivalent question on ELF is open and is NOT asserted by silence."
fi

# ---------- the sampled measurement ----------
#
# REPORTED, NEVER ASSERTED. Each round rebuilds the same program from the same
# store with the same command; the only thing varying is the build itself. The
# number that matters is how many DISTINCT digests appear.
#
# A run showing 1 distinct digest does NOT establish determinism — it is one
# sample of a process observed to vary intermittently (README records the
# observed rates). Nothing below touches $fail.
echo
echo "SAMPLED MEASUREMENT — $REPEATS rebuilds of the same closure, per backend"
echo "  (reported only; a uniform run is a sample, not a proof of determinism)"
echo
mkdir -p "$work/rep"
for k in go llvm; do
  i=1
  : > "$work/rep/$k.txt"
  while [ "$i" -le "$REPEATS" ]; do
    if [ "$k" = llvm ]; then
      "$oath" build show-from-marker --backend llvm -o "$work/rep/prog" >/dev/null 2>&1
    else
      "$oath" build show-from-marker -o "$work/rep/prog" >/dev/null 2>&1
    fi
    digest "$work/rep/prog" >> "$work/rep/$k.txt"
    i=$((i + 1))
  done
  d=$(sort -u "$work/rep/$k.txt" | wc -l | tr -d ' ')
  note "$k: $d distinct digest(s) across $REPEATS identical rebuilds"
  sort "$work/rep/$k.txt" | uniq -c | while read -r n h; do
    note "      $n x $(printf '%s' "$h" | cut -c1-16)"
  done
done

echo
if [ "$fail" = "0" ]; then
  echo "issue-116 reproducibility: all $ran asserted checks passed"
else
  echo "issue-116 reproducibility: $fail of $ran asserted checks FAILED"
fi
exit "$fail"
