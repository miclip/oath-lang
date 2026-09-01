#!/bin/zsh
# effects-consumer: compose THREE capabilities (process_env + file_read + record_sink)
# in one verified CLI, and measure both what the effects model delivers and the frictions
# composition surfaces. The corpus witness main-fetch exercised one capability; this
# builds and RUNS a three-capability program end to end. Requires the Go toolchain
# (oath build shells out to `go build`) and a capped z3 for the proofs.
set -u
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a repo" >&2; exit 1; }
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "SETUP FAILED: this experiment builds a native binary; go must be on PATH" >&2; exit 1; }
here="$root/docs/experiments/effects-consumer"

# Validate the temp dirs BEFORE exporting: an empty OATH_STORE makes Oath fall back to
# ./codebase, and prove/put would then mutate the committed, append-only corpus.
OATH_STORE=$(mktemp -d) || { echo "SETUP FAILED: mktemp -d failed (store)" >&2; exit 1; }
work=$(mktemp -d) || { echo "SETUP FAILED: mktemp -d failed (work)" >&2; rm -rf "$OATH_STORE"; exit 1; }
[ -d "$OATH_STORE" ] && [ -d "$work" ] || { echo "SETUP FAILED: temp dir missing" >&2; exit 1; }
export OATH_STORE
trap 'rm -rf "$OATH_STORE" "$work"' EXIT
cap() { OATH_PROVE_RLIMIT=15000000 OATH_PROVE_WALLCAP_SEC=120 OATH_PROVE_MEMORY_MB=3000 "$@"; }

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

# list BEFORE str: str.oath's str-split/str-join are typed (List Str), so loading str
# first drops them silently. Check every import status — a partial store would let later
# checks pass against missing definitions.
for f in list str; do
  o=$("$OATH" put "$root/examples/$f.oath" --new 2>&1)
  if printf '%s' "$o" | grep -qiE 'error|✗'; then
    echo "SETUP FAILED: $f.oath did not import cleanly:" >&2; printf '%s\n' "$o" | grep -iE 'error|✗' >&2; exit 1
  fi
done
put=$("$OATH" put "$here/ingest.oath" --new 2>&1)
if printf '%s' "$put" | grep -qiE 'error|✗|FALSIFIED'; then
  echo "SETUP FAILED: ingest.oath did not elaborate/test cleanly:" >&2; printf '%s\n' "$put" | grep -iE 'error|✗|FALSIFIED' >&2; exit 1
fi

echo "1 — the effects model DELIVERS: a 3-capability program proves over ALL simulated worlds"
pr=$(cap "$OATH" prove ingest 2>&1)
if printf '%s' "$pr" | grep -q 'proven: 3/3'; then
  pass "ingest PROVEN 3/3 (no-args, missing-env, emits-file-body) over every env/readfile/emit world at once"
else bad "expected 3/3 proven; got: $(printf '%s' "$pr" | grep -iE 'proven|unproven' | tr '\n' ' ')"; fi

echo "2 — confinement tracks a THREE-capability record"
if printf '%s' "$put" | grep -q 'cap: confined'; then
  pass "the {emit,env,readfile} record verdicts cap: confined — no capability escapes"
else bad "expected cap: confined; got: $(printf '%s' "$put" | grep -iE 'confined|escape' | tr '\n' ' ')"; fi

echo "3 — the build wires all three authorities and states the exit-70 invariant"
b=$("$OATH" build ingest -o "$work/ingest" 2>&1)
if [ ! -x "$work/ingest" ]; then
  echo "SETUP FAILED: build did not produce a binary:" >&2; printf '%s\n' "$b" >&2; exit 1
fi
if printf '%s' "$b" | grep -q 'emit (record_sink)' && printf '%s' "$b" | grep -q 'env (process_env)' \
   && printf '%s' "$b" | grep -q 'readfile (file_read)' && printf '%s' "$b" | grep -q 'exits 70'; then
  pass "requires: emit (record_sink), env (process_env), readfile (file_read) — each resolved before launch or exit 70"
else bad "expected all three requirements + the exit-70 invariant; got: $(printf '%s' "$b" | grep -iE 'requires|record_sink|process_env|file_read|70' | tr '\n' ' ')"; fi

echo "4 — end to end (DEFAULT sink = stdout): the record and failure paths, with the environment isolated"
printf 'payload-XYZ' > "$work/conf"
# Isolate ambient state: env -u NOPE (the program reads the variable NAMED by argv) and
# OATH_EMIT_PATH (which would redirect the record off stdout, changing the output below).
# The trailing `; printf .` sentinel defeats command substitution's stripping of trailing
# newlines, so an EXTRA blank line after `ok` is caught rather than swallowed.
happy=$(env -u OATH_EMIT_PATH MYCONF="$work/conf" "$work/ingest" MYCONF 2>/dev/null; printf .)
noenv=$(env -u NOPE -u OATH_EMIT_PATH "$work/ingest" NOPE 2>/dev/null)
noargs=$(env -u OATH_EMIT_PATH "$work/ingest" 2>/dev/null)
nofile=$(env -u OATH_EMIT_PATH MYCONF="$work/does-not-exist" "$work/ingest" MYCONF 2>/dev/null)
# In the DEFAULT sink, emit prints the record to stdout and main prints the "ok" return —
# exactly two lines and a trailing newline, then the sentinel. Any extra line breaks it.
happy_expected=$(printf 'payload-XYZ\nok\n.')
if [ "$happy" = "$happy_expected" ] \
   && [ "$noenv" = "no config path in environment" ] \
   && [ "$noargs" = "usage: ingest <config-var>" ] \
   && [ "$nofile" = "config file empty or missing" ]; then
  pass "default sink: stdout is the record then 'ok'; missing-env, no-args and missing-file each report"
else bad "end-to-end paths wrong: happy=[$happy] (want [$happy_expected]) noenv=[$noenv] noargs=[$noargs] nofile=[$nofile]"; fi

echo "5 — NOT friction: the CONFIGURED sink (OATH_EMIT_PATH) SEPARATES records from the result, and provisions"
# Measures two things: the record lands in the sink FILE while stdout carries only the
# result, and an unwritable sink path is a PROVISION failure (exit 70) before launch. The
# call-time write-failure -> "" path is decidable from the provider source (compile.go),
# not exercised here — it needs an open that succeeds at probe then fails at write, which
# is not portably constructible; so this check does not claim to measure it.
sink="$work/sink.log"; : > "$sink"
cout=$(MYCONF="$work/conf" OATH_EMIT_PATH="$sink" "$work/ingest" MYCONF 2>/dev/null)
crec=$(cat "$sink" 2>/dev/null)
# A path UNDER a regular file cannot be opened (ENOTDIR) on any host, root included —
# no dependence on an assumed-absent directory.
: > "$work/blocker"
MYCONF="$work/conf" OATH_EMIT_PATH="$work/blocker/sink.log" "$work/ingest" MYCONF >/dev/null 2>&1; provrc=$?
if [ "$cout" = "ok" ] && [ "$crec" = "payload-XYZ" ] && [ "$provrc" -eq 70 ]; then
  pass "OATH_EMIT_PATH routes the record to a separate file (stdout carries only the result), and an unwritable sink is a provision failure (exit 70) — the channel demand is already met"
else bad "expected separated sink; got stdout=[$cout] sinkfile=[$crec] unwritable-exit=[$provrc]"; fi

echo "6 — DEMAND (measured): the reads' \"\"-failure value is LOSSY — absent and empty are one value"
: > "$work/empty.conf"   # an existing but EMPTY file
missing=$(env -u OATH_EMIT_PATH MYCONF="$work/does-not-exist" "$work/ingest" MYCONF 2>/dev/null)
empty=$(env -u OATH_EMIT_PATH MYCONF="$work/empty.conf" "$work/ingest" MYCONF 2>/dev/null)
if [ "$missing" = "$empty" ] && [ "$missing" = "config file empty or missing" ]; then
  pass "a MISSING file and an EMPTY file yield the identical result — readfile returns \"\" for both, so the program cannot tell them apart (env conflates unset/empty the same way)"
else bad "expected missing and empty to be indistinguishable; got missing=[$missing] empty=[$empty]"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks; the multi-capability program and its demands reproduce"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"; exit 1
fi
