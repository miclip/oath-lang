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
for f in "$FIX"/canonical/*.bin; do
  cn=$((cn+1)); n="$(basename "$f")"
  cmp -s "$f" "$TMP/canon/$n" || { echo "  FAIL: canonical bytes differ for $n"; cbad=1; fail=1; }
done
[ $cbad -eq 0 ] && echo "  PASS: $cn canonical/*.bin byte-identical (O1)"

# ---------------------------------------------------------------------------
# Check 2: golden encoding fixtures
# ---------------------------------------------------------------------------
echo "== Check 2: golden encoding fixtures =="
if "$BIN" enctest "$FIX/encoding" > "$TMP/enc.out" 2>&1; then
  echo "  PASS: 6 O1 golden encodings reproduced byte-for-byte + strict-decoder rejects"
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
abad=0; ac=0
for f in "$FIX"/gate/accept/*.oath; do
  ac=$((ac+1))
  "$BIN" hash "$f" > /dev/null 2>&1 || { echo "  FAIL: $(basename "$f") wrongly rejected"; abad=1; fail=1; }
done
[ $abad -eq 0 ] && echo "  PASS: all $ac accept fixtures accepted"

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
# Checks 5-6 come in two modes. FULL (default): cold re-derivation of every
# proof outcome — z3 at the SPEC §7.2 rlimit budget, run-stability fixpoint
# included. This is HOURS of solver time and is the definitive empirical
# check. ORACLE (OATHRS_CONFORMANCE_PROVE=oracle): no solver runs; instead
# the kernel must reproduce every direct-attempt script byte-for-byte
# (fixtures/prove/scripts.txt) under the pinned solver version. Since an
# outcome is a pure function of (script bytes, solver version, rlimit) —
# SPEC §7.2, deterministic budget — byte-identical scripts under identical
# pins DETERMINE identical outcomes. Per-push CI uses oracle mode; the full
# mode runs on a schedule and on demand.
# ---------------------------------------------------------------------------
MODE="${OATHRS_CONFORMANCE_PROVE:-full}"
if [ "$MODE" = "oracle" ]; then
  echo "== Checks 5-6 (byte-oracle mode): script identity + pinned solver =="
  WANT_SOLVER="$(python3 -c "import json;print(json.load(open('$FIX/prove/outcomes.json'))['solver'])")"
  GOT_SOLVER="$(z3 --version | head -1)"
  if [ "$WANT_SOLVER" = "$GOT_SOLVER" ]; then
    echo "  PASS: solver pinned ($GOT_SOLVER)"
  else
    echo "  FAIL: solver mismatch — fixtures were settled under \"$WANT_SOLVER\", this environment has \"$GOT_SOLVER\""
    fail=1
  fi
  if "$BIN" scripts --outcomes "$FIX/prove/outcomes.json" "$EX"/*.oath > "$TMP/scripts.txt" 2>/dev/null \
     && diff "$TMP/scripts.txt" "$FIX/prove/scripts.txt" > "$TMP/scripts.diff"; then
    n=$(( $(wc -l < "$TMP/scripts.txt" | tr -d ' ') - 1 ))
    echo "  PASS: $n direct-attempt scripts byte-identical to prove/scripts.txt"
    echo "  outcomes are determined: f(script bytes, solver, rlimit), all three pinned."
    echo "  (run without OATHRS_CONFORMANCE_PROVE=oracle for the empirical re-derivation)"
  else
    echo "  FAIL: script byte oracle diverged:"; head -20 "$TMP/scripts.diff" 2>/dev/null; fail=1
  fi

  echo
  if [ $fail -eq 0 ]; then
    echo "CONFORMANCE: PASS (checks 1-4 + byte oracle; outcomes determined, not re-derived)"
  else
    echo "CONFORMANCE: FAIL"
  fi
  exit $fail
fi

echo "== Proving (z3) — shared by checks 5-6, this is the slow step =="
"$BIN" prove "$EX"/*.oath > "$TMP/prove.txt" 2> "$TMP/prove.err"
pstat=$?
if [ $pstat -eq 3 ]; then
  # The wall cap is SAFETY-ONLY and not outcome-determining (SPEC §7.2):
  # outcomes are fixed by (script bytes, solver, rlimit). Slow hardware
  # that cannot exhaust the rlimit inside the default cap should raise
  # the cap, not be mistaken for a divergence.
  echo "  FAIL: run INVALIDATED — wall cap fired before rlimit exhausted."
  echo "        This is slow hardware, not a kernel divergence: re-run with"
  echo "        a raised OATHRS_Z3_WALL_CAP_MS (the cap never affects outcomes)."
  sed -n '1,5p' "$TMP/prove.err"; fail=1
elif [ $pstat -ne 0 ]; then
  echo "  FAIL: prove command errored (exit $pstat)"; sed -n '1,5p' "$TMP/prove.err"; fail=1
fi

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
