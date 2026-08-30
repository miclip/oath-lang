#!/bin/sh
# Falsifier for `oath serve --public-reads` (#189) — anonymous READ access so a
# consumer can clone public code with nothing but a URL, while WRITES stay signed.
#
# THE CLAIM, stated as something that can fail:
#   against a --public-reads registry, `clone`/`hydrate`/`ls --remote` succeed with NO
#   --key; against a registry without it they are cleanly refused with a "pass --key"
#   hint; and a WRITE is refused with no key on either. The server change widens reads
#   only.
#
# It attacks the claim on both server postures and on the write path. Read-only against
# the repo (throwaway stores; loopback servers killed on exit; nothing in codebase/).
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd)
here=$(cd "$(dirname "$0")" && pwd)
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
app="$here/checklist.oath"

unset OATH_STORE OATH_REGISTRY OATH_KEY OATH_KMS_KEY OATH_BACKEND OATH_AUTHOR \
      OATH_AUTHORIZED_KEYS OATH_HTTP_ADDR OATH_NAMESPACE OATH_HOME OATH_STDLIB \
      OATH_STORE_LOCK OATH_DB_DRIVER OATH_DB_DSN OATH_OBJECT_BUCKET OATH_PUBLIC_READS 2>/dev/null || true

ledger=$(mktemp)
SRV_PID=""
cleanup() {
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null
  while read -r d; do [ -n "$d" ] && rm -rf "$d"; done < "$ledger"
  rm -f "$ledger"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mk() { d=$(mktemp -d) || { echo "SETUP FAILED: mktemp" >&2; exit 1; }; printf '%s\n' "$d" >> "$ledger"; printf '%s' "$d"; }
work=$(mk)

# start_server <store> <keyfile> <marker> <mode> — mode "public" adds --public-reads.
# Readiness probes with a KEYED ls, which works on either posture, and requires the
# marker (positive proof it is OUR server).
marker_probe() {
  mp_out="$work/probe.out"; : > "$mp_out"
  OATH_STORE="$1" "$OATH" ls --remote "$2" --key "$3" >"$mp_out" 2>/dev/null &
  mp_pid=$!
  ( sleep 4; kill "$mp_pid" 2>/dev/null ) >/dev/null 2>&1 & mp_wd=$!
  wait "$mp_pid" 2>/dev/null; kill "$mp_wd" 2>/dev/null; wait "$mp_wd" 2>/dev/null
  grep -q "$4" "$mp_out" 2>/dev/null
}
start_server() {
  pstore=$(mk); attempt=0
  while [ "$attempt" -lt 6 ]; do
    attempt=$((attempt + 1))
    port=$(( 20000 + ( ($$ + attempt * 251) % 20000 ) ))
    url="http://127.0.0.1:$port"
    if [ "$4" = public ]; then
      OATH_STORE="$1" "$OATH" serve --http "127.0.0.1:$port" --public-reads >"$work/serve.$port.log" 2>&1 &
    else
      OATH_STORE="$1" "$OATH" serve --http "127.0.0.1:$port" >"$work/serve.$port.log" 2>&1 &
    fi
    p=$!; SRV_PID="$p"; tries=0
    while [ "$tries" -lt 40 ]; do
      tries=$((tries + 1))
      if marker_probe "$pstore" "$url" "$2" "$3"; then SRV_URL="$url"; return 0; fi
      kill -0 "$p" 2>/dev/null || break
      sleep 0.25
    done
    kill "$p" 2>/dev/null; wait "$p" 2>/dev/null; SRV_PID=""
  done
  return 1
}

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
head2() { printf '\n%s\n' "$1"; }

# --- SETUP -------------------------------------------------------------------------
head2 "SETUP — publisher store, lock, key"
src=$(mk)
( cd "$root" && git archive HEAD:codebase ) | ( cd "$src" && tar -x ) || { echo "SETUP FAILED: git archive" >&2; exit 1; }
OATH_STORE="$src" "$OATH" put "$app" --new >/dev/null 2>&1 || { echo "SETUP FAILED: put" >&2; exit 1; }
lock="$work/checklist.lock"
OATH_STORE="$src" "$OATH" resolve "$app" -o "$lock" >/dev/null 2>&1 || { echo "SETUP FAILED: resolve" >&2; exit 1; }
"$OATH" keygen --out "$work/k" >/dev/null 2>&1 || { echo "SETUP FAILED: keygen" >&2; exit 1; }
key="$work/k.key"
marker="PubReads$$X$(awk 'BEGIN{srand();printf "%06d", int(rand()*1000000)}')"
printf '(data %s [] (Mk%s))\n' "$marker" "$marker" > "$work/marker.oath"
OATH_STORE="$src" "$OATH" put "$work/marker.oath" --new >/dev/null 2>&1 || { echo "SETUP FAILED: marker" >&2; exit 1; }

# --- 1. PUBLIC-READS: keyless reads work -------------------------------------------
head2 "1 — --public-reads: a consumer clones public code with NO key"
start_server "$src" "$key" "$marker" public || { echo "SETUP FAILED: public server" >&2; exit 1; }
if OATH_STORE="$(mk)" "$OATH" ls --remote "$SRV_URL" >/dev/null 2>&1; then
  pass "ls --remote succeeds with no key"
else bad "keyless ls failed against a --public-reads server"; fi
cpub=$(mk)
if OATH_STORE="$cpub" "$OATH" clone "$app" --lock "$lock" --remote "$SRV_URL" >/dev/null 2>&1; then
  pass "clone --remote succeeds with no key"
else bad "keyless clone failed against a --public-reads server"; fi
b="$work/bin"
if OATH_STORE="$cpub" "$OATH" build checklist -o "$b" >/dev/null 2>&1 && [ "$("$b" a b 2>/dev/null | head -1)" = "1. a" ]; then
  pass "the keyless-cloned store builds and runs — public code cloned with nothing but a URL"
else bad "the keyless-cloned store did not build+run"; fi
# WRITES stay signed: an unsigned put is refused even here.
if OATH_STORE="$(mk)" "$OATH" put --remote "$SRV_URL" "$app" >/dev/null 2>&1; then
  bad "an unsigned put SUCCEEDED against a --public-reads server — writes were widened too"
else pass "an unsigned WRITE is still refused — --public-reads widens reads only"; fi
kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null; SRV_PID=""

# --- 2. CLOSED: keyless reads refused, keyed reads work ----------------------------
head2 "2 — without --public-reads: keyless reads are cleanly refused"
start_server "$src" "$key" "$marker" closed || { echo "SETUP FAILED: closed server" >&2; exit 1; }
lsout=$(OATH_STORE="$(mk)" "$OATH" ls --remote "$SRV_URL" 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "keyless ls succeeded against a closed server"
elif printf '%s' "$lsout" | grep -qi 'anonymous\|--public-reads\|pass --key'; then
  pass "keyless ls is refused with a hint to pass --key or enable --public-reads"
else bad "keyless ls failed, but without a useful hint: $lsout"; fi
cc=$(mk)
if OATH_STORE="$cc" "$OATH" clone "$app" --lock "$lock" --remote "$SRV_URL" >/dev/null 2>&1; then
  bad "keyless clone succeeded against a closed server"
else pass "keyless clone is refused against a closed server"; fi
# The SAME read succeeds WITH a key — so it is the key that was missing, nothing else.
if OATH_STORE="$(mk)" "$OATH" ls --remote "$SRV_URL" --key "$key" >/dev/null 2>&1; then
  pass "the same ls succeeds WITH --key — only the credential was missing"
else bad "keyed ls failed against a closed server"; fi
kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null; SRV_PID=""

# --- completeness guard ------------------------------------------------------------
expected_checks=7
head2 "RESULT"
if [ "$checks" -ne "$expected_checks" ]; then
  printf 'INCOMPLETE — ran %d checks, expected %d\n' "$checks" "$expected_checks" >&2
  exit 1
fi
if [ "$failures" -eq 0 ]; then
  printf 'PASS — %d/%d checks; public reads let a consumer clone with no key, and writes stay signed\n' "$checks" "$expected_checks"
  exit 0
else
  printf 'FAIL — %d of %d checks failed\n' "$failures" "$checks" >&2
  exit 1
fi
