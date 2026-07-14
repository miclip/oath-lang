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
# Check 5: analyses (termination, confinement, mutation, guarantee).
# Field-wise: proof-derived fields (`proven`, the tested->proven level
# upgrade) are SMT / stage 3 and are compared modulo proof. Mutation scores
# are store metadata recorded selectively; compared only where the fixture
# records one (every such score must match).
# ---------------------------------------------------------------------------
echo "== Check 5: analyses (analyses/*.json) =="
"$BIN" analyze --out "$TMP/analyze" "$EX"/*.oath 2>/dev/null

getstr(){ grep "\"$2\"" "$1" | head -1 | sed -E 's/.*: "([^"]*)".*/\1/'; }
getnum(){ grep "\"$2\"" "$1" | head -1 | sed -E 's/[^0-9]//g'; }
confarr(){ awk 'f&&/\]/{f=0} f{gsub(/[ ]/,"");print} /"confinement": \[/{f=1}' "$1"; }

abad=0; an=0
for f in "$FIX"/analyses/*.json; do
  an=$((an+1)); n="$(basename "$f" .json)"; mine="$TMP/analyze/$n.json"
  if [ ! -f "$mine" ]; then echo "  FAIL: no analysis produced for $n"; abad=1; fail=1; continue; fi

  [ "$(getstr "$f" kind)" = "$(getstr "$mine" kind)" ] \
    || { echo "  FAIL $n: kind"; abad=1; fail=1; }

  flvl="$(getstr "$f" level)"; [ "$flvl" = "proven" ] && flvl="tested"
  [ "$flvl" = "$(getstr "$mine" level)" ] \
    || { echo "  FAIL $n: level (fixture $(getstr "$f" level) -> $flvl vs $(getstr "$mine" level))"; abad=1; fail=1; }

  if [ "$(getstr "$f" kind)" = "func" ]; then
    [ "$(getstr "$f" termination)" = "$(getstr "$mine" termination)" ] \
      || { echo "  FAIL $n: termination"; abad=1; fail=1; }
    [ "$(confarr "$f")" = "$(confarr "$mine")" ] \
      || { echo "  FAIL $n: confinement"; abad=1; fail=1; }
    fc="$(grep -q '"cases"' "$f" && getnum "$f" cases || echo none)"
    mc="$(grep -q '"cases"' "$mine" && getnum "$mine" cases || echo none)"
    [ "$fc" = "$mc" ] || { echo "  FAIL $n: cases ($fc vs $mc)"; abad=1; fail=1; }
    if grep -q '"mutants_total"' "$f"; then
      if ! grep -q '"mutants_total"' "$mine"; then
        echo "  FAIL $n: fixture has a mutation score, mine has none"; abad=1; fail=1
      else
        [ "$(getnum "$f" mutants_killed)" = "$(getnum "$mine" mutants_killed)" ] \
          && [ "$(getnum "$f" mutants_total)" = "$(getnum "$mine" mutants_total)" ] \
          || { echo "  FAIL $n: mutation $(getnum "$f" mutants_killed)/$(getnum "$f" mutants_total) vs $(getnum "$mine" mutants_killed)/$(getnum "$mine" mutants_total)"; abad=1; fail=1; }
      fi
    fi
  fi
done
[ $abad -eq 0 ] && echo "  PASS: $an analyses match (termination, confinement, mutation, level modulo stage-3 proof)"

echo
if [ $fail -eq 0 ]; then
  echo "CONFORMANCE: PASS (checks 1-5)"
else
  echo "CONFORMANCE: FAIL"
fi
exit $fail
