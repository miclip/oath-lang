#!/usr/bin/env bash
# Stage-1 conformance harness for the independent Rust kernel.
# Runs conformance checks 1-3 from fixtures/MANIFEST.md end to end.
# Exits nonzero if any check fails.
set -u

# Resolve repo root (this script lives in oathrs/).
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
BIN="$HERE/target/release/oathrs"
FIX="$ROOT/fixtures"
EX="$ROOT/examples"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if [ ! -x "$BIN" ]; then
  echo "building oathrs..."
  ( cd "$HERE" && cargo build --release >/dev/null 2>&1 ) || { echo "BUILD FAILED"; exit 1; }
fi

fail=0

# ---------------------------------------------------------------------------
# Check 1: re-elaborating examples/*.oath reproduces every hash, and each
# canonical/<name>.json is byte-identical to what we emit.
# ---------------------------------------------------------------------------
echo "== Check 1: hashes + canonical bytes =="
"$BIN" hash "$EX"/*.oath > "$TMP/hashes.txt" 2> "$TMP/hash.err"
if [ $? -ne 0 ]; then
  echo "  FAIL: hash command errored"; cat "$TMP/hash.err"; fail=1
fi
if diff <(sort "$TMP/hashes.txt") <(sort "$FIX/hashes.txt") > "$TMP/hash.diff"; then
  echo "  PASS: $(wc -l < "$TMP/hashes.txt" | tr -d ' ') definition hashes match hashes.txt"
else
  echo "  FAIL: hash table differs:"; cat "$TMP/hash.diff"; fail=1
fi

"$BIN" canon --out "$TMP/canon" "$EX"/*.oath 2> "$TMP/canon.err"
if [ $? -ne 0 ]; then
  echo "  FAIL: canon command errored"; cat "$TMP/canon.err"; fail=1
fi
cbad=0; cn=0
for f in "$FIX"/canonical/*.json; do
  cn=$((cn+1))
  n="$(basename "$f")"
  if ! cmp -s "$f" "$TMP/canon/$n"; then
    echo "  FAIL: canonical bytes differ for $n"; cbad=1; fail=1
  fi
done
[ $cbad -eq 0 ] && echo "  PASS: $cn canonical/*.json byte-identical"

# ---------------------------------------------------------------------------
# Check 2: encoding/*.json golden bytes + manifest hashes (SPEC §1.5).
# Reconstructs the Defs in-code and compares to the fixtures.
# ---------------------------------------------------------------------------
echo "== Check 2: golden encoding fixtures =="
if "$BIN" enctest "$FIX/encoding" > "$TMP/enc.out" 2>&1; then
  echo "  PASS: 5 golden encodings reproduced byte-for-byte"
else
  echo "  FAIL:"; cat "$TMP/enc.out"; fail=1
fi
# Independently confirm the fixtures self-hash to their manifest values.
mbad=0
while IFS=$'\t' read -r case want note; do
  case "$case" in \#*|"") continue;; esac
  got="$(shasum -a 256 "$FIX/encoding/$case.json" | cut -d' ' -f1)"
  if [ "$got" != "$want" ]; then
    echo "  FAIL: $case hashes to $got, manifest says $want"; mbad=1; fail=1
  fi
done < "$FIX/encoding/manifest.txt"
[ $mbad -eq 0 ] && echo "  PASS: encoding fixtures match manifest.txt hashes"

# ---------------------------------------------------------------------------
# Check 3: gate rejects every gate/reject/*.oath and accepts the examples.
# ---------------------------------------------------------------------------
echo "== Check 3: the gate =="
if "$BIN" hash "$EX"/*.oath > /dev/null 2>&1; then
  echo "  PASS: whole examples corpus accepted"
else
  echo "  FAIL: an example was rejected"; fail=1
fi
rbad=0; rn=0
for f in "$FIX"/gate/reject/*.oath; do
  rn=$((rn+1))
  if "$BIN" hash "$f" > /dev/null 2>&1; then
    echo "  FAIL: $(basename "$f") was wrongly accepted"; rbad=1; fail=1
  fi
done
[ $rbad -eq 0 ] && echo "  PASS: all $rn reject fixtures rejected"

echo
if [ $fail -eq 0 ]; then
  echo "CONFORMANCE: PASS (checks 1-3)"
else
  echo "CONFORMANCE: FAIL"
fi
exit $fail
