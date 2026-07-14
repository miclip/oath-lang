#!/usr/bin/env bash
# Conformance harness for the independent Rust kernel.
# Stage 1: checks 1-3 (identity + gate).  Stage 2: checks 4-5 (dynamic).
# Exits nonzero if any check fails.
set -u

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
# Check 1: hashes + canonical bytes
# ---------------------------------------------------------------------------
echo "== Check 1: hashes + canonical bytes =="
"$BIN" hash "$EX"/*.oath > "$TMP/hashes.txt" 2> "$TMP/hash.err" \
  || { echo "  FAIL: hash command errored"; cat "$TMP/hash.err"; fail=1; }
if diff <(sort "$TMP/hashes.txt") <(sort "$FIX/hashes.txt") > "$TMP/hash.diff"; then
  echo "  PASS: $(wc -l < "$TMP/hashes.txt" | tr -d ' ') definition hashes match"
else
  echo "  FAIL:"; cat "$TMP/hash.diff"; fail=1
fi
"$BIN" canon --out "$TMP/canon" "$EX"/*.oath 2>/dev/null
cbad=0; cn=0
for f in "$FIX"/canonical/*.json; do
  cn=$((cn+1)); n="$(basename "$f")"
  cmp -s "$f" "$TMP/canon/$n" || { echo "  FAIL: canonical bytes differ for $n"; cbad=1; fail=1; }
done
[ $cbad -eq 0 ] && echo "  PASS: $cn canonical/*.json byte-identical"

# ---------------------------------------------------------------------------
# Check 2: golden encoding fixtures
# ---------------------------------------------------------------------------
echo "== Check 2: golden encoding fixtures =="
if "$BIN" enctest "$FIX/encoding" > "$TMP/enc.out" 2>&1; then
  echo "  PASS: 5 golden encodings reproduced byte-for-byte"
else
  echo "  FAIL:"; cat "$TMP/enc.out"; fail=1
fi

# ---------------------------------------------------------------------------
# Check 3: the gate
# ---------------------------------------------------------------------------
echo "== Check 3: the gate =="
"$BIN" hash "$EX"/*.oath > /dev/null 2>&1 \
  && echo "  PASS: whole examples corpus accepted" \
  || { echo "  FAIL: an example was rejected"; fail=1; }
rbad=0; rn=0
for f in "$FIX"/gate/reject/*.oath; do
  rn=$((rn+1))
  "$BIN" hash "$f" > /dev/null 2>&1 && { echo "  FAIL: $(basename "$f") wrongly accepted"; rbad=1; fail=1; }
done
[ $rbad -eq 0 ] && echo "  PASS: all $rn reject fixtures rejected"

# ---------------------------------------------------------------------------
# Check 4: verification output (verdicts + counterexamples) byte-for-byte
# ---------------------------------------------------------------------------
echo "== Check 4: verification (verify/*.txt) =="
"$BIN" verify --out "$TMP/verify" "$EX"/*.oath 2>/dev/null
vbad=0; vn=0
for f in "$FIX"/verify/*.txt; do
  vn=$((vn+1)); n="$(basename "$f")"
  cmp -s "$f" "$TMP/verify/$n" || { echo "  FAIL: verify output differs for $n"; vbad=1; fail=1; }
done
[ $vbad -eq 0 ] && echo "  PASS: $vn verify/*.txt byte-identical (verdicts + counterexamples)"

# ---------------------------------------------------------------------------
# Proof run (shared by checks 5 and 6). This drives z3 and is the slow step
# (several minutes): one goal per property, plus structural induction.
# ---------------------------------------------------------------------------
echo "== Proving (z3) — shared by checks 5-6, this is the slow step =="
"$BIN" prove "$EX"/*.oath > "$TMP/prove.txt" 2>/dev/null \
  || { echo "  FAIL: prove command errored"; fail=1; }

# ---------------------------------------------------------------------------
# Check 6: proof outcomes reproduce fixtures/prove/outcomes.json exactly
# (per definition, per property: proven true/false).
# ---------------------------------------------------------------------------
echo "== Check 6: proof outcomes (prove/outcomes.json) =="
python3 - "$FIX/prove/outcomes.json" "$TMP/prove.txt" <<'PY'
import json, sys
want = {d['name']: [p['proven'] for p in d['props']]
        for d in json.load(open(sys.argv[1]))['definitions']}
mine = {}
for line in open(sys.argv[2]):
    p = line.rstrip('\n').split('\t')
    if len(p) == 3:
        mine[p[0]] = [c == '+' for c in p[2]]
bad = [n for n in want if mine.get(n, []) != want[n]]
props = sum(len(v) for v in want.values())
if bad:
    print("  FAIL: proof outcomes differ for:", ", ".join(bad)); sys.exit(1)
print("  PASS: %d definitions / %d property outcomes reproduce outcomes.json"
      % (len(want), props))
PY
[ $? -ne 0 ] && fail=1

# ---------------------------------------------------------------------------
# Check 5 (full): analyses byte-identical, including the proof-derived
# guarantee upgrade (level "proven", proven counts) fed from the prove run.
# ---------------------------------------------------------------------------
echo "== Check 5: analyses (analyses/*.json) — full equality =="
"$BIN" analyze --proofs "$TMP/prove.txt" --out "$TMP/analyze" "$EX"/*.oath 2>/dev/null
abad=0; an=0
for f in "$FIX"/analyses/*.json; do
  an=$((an+1)); n="$(basename "$f")"
  cmp -s "$f" "$TMP/analyze/$n" || { echo "  FAIL: analysis differs for $n"; abad=1; fail=1; }
done
[ $abad -eq 0 ] && echo "  PASS: $an analyses/*.json byte-identical (termination, confinement, mutation, guarantee incl. proof)"

echo
if [ $fail -eq 0 ]; then
  echo "CONFORMANCE: PASS (checks 1-6)"
else
  echo "CONFORMANCE: FAIL"
fi
exit $fail
