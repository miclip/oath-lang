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
# The corpus is not only examples/. `oath fixtures` derives fixtures from the
# STORE, so anything put into codebase/ gets a fixture — while this script read
# examples/*.oath alone. Adding the #120 application produced sixteen "verify
# output differs" failures that were not divergences at all: the Rust kernel had
# never been shown the source. Two scopes for one corpus, disagreeing silently.
# SRC is the corpus as the fixture generator sees it; app sources come LAST
# because they depend on the examples.
SRC="$(ls "$EX"/*.oath) $(ls "$ROOT"/apps/*/*.oath 2>/dev/null)"
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
"$BIN" hash $SRC > "$TMP/hashes.txt" 2> "$TMP/hash.err" \
  || { echo "  FAIL: hash command errored"; cat "$TMP/hash.err"; fail=1; }
if diff <(sort "$TMP/hashes.txt") <(sort "$FIX/hashes.txt") > "$TMP/hash.diff"; then
  echo "  PASS: $(wc -l < "$TMP/hashes.txt" | tr -d ' ') definition hashes match"
else
  echo "  FAIL:"; cat "$TMP/hash.diff"; fail=1
fi
"$BIN" canon --out "$TMP/canon" $SRC 2>/dev/null
# SPEC §10.0a CONF-COVERAGE-FROM-CORPUS: enumerate the CORPUS, not the fixture
# directory. Iterating the directory defined coverage as "whatever files exist",
# so a definition whose fixture was missing was not compared, not counted, and
# could not fail — the check reported "186 byte-identical" for a 187-definition
# corpus and that was true and useless (#95).
#
# Filenames use §10.0a's encoding (CONF-FIXTURE-FILENAME): '_'->'__' and an
# uppercase letter X -> '_x'. Without it `map` and `Map` collide on a
# case-insensitive filesystem and one definition's bytes vanish.
cbad=0; cn=0; cmiss=0
ls "$FIX/canonical" > "$TMP/fixlist"
ls "$TMP/canon"     > "$TMP/genlist"
while IFS="$(printf '\t')" read -r name _hash; do
  [ -n "$name" ] || continue
  enc="$(printf '%s' "$name" | awk '{
    out=""
    for (i = 1; i <= length($0); i++) {
      c = substr($0, i, 1)
      if (c == "_") out = out "__"
      else if (c ~ /[A-Z]/) out = out "_" tolower(c)
      else out = out c
    }
    print out
  }')"
  cn=$((cn+1))
  # SPEC §10.0a CONF-FILENAME-EXACT: match against a LISTING, not with `test -f`.
  # On a case-insensitive filesystem `[ -f _Map.bin ]` is true when only
  # `_map.bin` exists, and `cmp` then compares a file with itself and passes — so
  # an implementation emitting `_X` instead of `_x` produced 0 failures on macOS
  # and 15 under exact matching. The rule was unfalsifiable on the platform it was
  # written for.
  grep -qxF "$enc.bin" "$TMP/fixlist" || {
    echo "  FAIL: no canonical fixture for definition $name (expected $enc.bin)"
    cmiss=$((cmiss+1)); fail=1; continue; }
  grep -qxF "$enc.bin" "$TMP/genlist" || {
    echo "  FAIL: kernel emitted no canonical file named $enc.bin for $name"
    cbad=1; fail=1; continue; }
  cmp -s "$FIX/canonical/$enc.bin" "$TMP/canon/$enc.bin" || {
    echo "  FAIL: canonical bytes differ for $name ($enc.bin)"; cbad=1; fail=1; }
done < "$FIX/hashes.txt"
if [ $cbad -eq 0 ] && [ $cmiss -eq 0 ]; then
  echo "  PASS: $cn/$cn corpus definitions have byte-identical canonical bytes (O1)"
fi

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
"$BIN" hash $SRC > /dev/null 2>&1 \
  && echo "  PASS: whole corpus accepted" \
  || { echo "  FAIL: a corpus definition was rejected"; fail=1; }
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
"$BIN" verify --out "$TMP/verify" $SRC 2>/dev/null
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
# check. ORACLE (OATHRS_CONFORMANCE_PROVE=oracle): no solver runs; instead the
# kernel must reproduce the DIRECT-ATTEMPT scripts byte-for-byte under the pinned
# solver version. Since a script's result is a pure function of (script bytes,
# solver version, rlimit) — SPEC §7.2, deterministic budget — byte-identical
# scripts under identical pins DETERMINE identical results FOR THOSE SCRIPTS.
# Per-push CI uses oracle mode; the full mode runs on a schedule and on demand.
#
# READ THAT SCOPE LITERALLY: oracle mode does NOT cover check 6 for a goal that
# proves by induction. Its verdict rests on subgoal scripts this mode never
# compares, and only the scheduled full run re-derives them. The per-push job is
# a strong gate, not a complete one.
#
# WHAT THIS MODE ESTABLISHES, stated narrowly because it once claimed more. It
# used to print "outcomes are determined: f(script bytes, solver, rlimit), all
# three pinned" after comparing the DIRECT attempt alone — 447 of the 2904
# scripts this corpus emits — so for every goal that fell through to induction
# the sentence asserted a determinism nothing had checked (#139).
#
# §10 has since decided that candidate-script exposure is NOT part of the
# conformance surface, because it tolerates differing proof methods and
# comparing candidate sets would standardize search instrumentation rather than
# observable semantics. So the cross-kernel byte oracle covers the direct
# attempts, permanently and by decision. The inductive scripts' bytes are still
# DETERMINED by §7.2's naming and subgoal rules — the obligation exists for every
# kernel; it simply has no cross-kernel witness, and is not supposed to.
#
# TWO RESIDUES remain, and full mode is what covers them. Both fixtures fix the
# RECORDED lemma state, which §7.2 makes a parameter of script identity: a cold
# fixpoint attempts a goal under smaller intermediate lemma sets as siblings
# become proven, and those scripts differ in bytes. And the inductive subgoals
# are compared across kernels by nothing. So oracle mode determines the
# DIRECT-ATTEMPT outcome under the recorded lemma state — not the goal's verdict:
# a goal whose direct attempt is inconclusive and whose induction succeeds has a
# verdict resting on scripts this mode never compared. Empirical re-derivation is
# what checks those. Naming both residues is what makes "scope the full run"
# (#139) a decidable question rather than a preference.
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
  if "$BIN" scripts --outcomes "$FIX/prove/outcomes.json" $SRC > "$TMP/scripts.txt" 2>/dev/null \
     && diff "$TMP/scripts.txt" "$FIX/prove/scripts.txt" > "$TMP/scripts.diff"; then
    n=$(( $(wc -l < "$TMP/scripts.txt" | tr -d ' ') - 1 ))
    echo "  PASS: $n direct-attempt scripts byte-identical to prove/scripts.txt"
  else
    echo "  FAIL: script byte oracle diverged:"; head -20 "$TMP/scripts.diff" 2>/dev/null; fail=1
  fi

  # CANDIDATE SCRIPTS: DECIDED OUT (#139). This block used to invoke
  # `scripts --attempts`, compare the whole candidate set, and FAIL a kernel
  # whose bytes differed. §10 decided that exposure is not part of the
  # conformance surface, so all of that is gone — asking and enforcing would be
  # the harness requiring what the protocol does not, which is the exact
  # requested-versus-required conflation this file corrected once already.
  #
  # The reason is §10 point 5: proof METHODS may differ where multiple proofs
  # exist. Comparing candidate sets would standardize proof-search
  # instrumentation rather than observable proof semantics, constraining every
  # future kernel's search.
  #
  # WHAT THE DECISION COSTS, since pretending it costs nothing is the easiest way
  # to have it reversed later by someone who notices: the comparison WOULD have
  # determined the verdict of a goal whose direct attempt is inconclusive and
  # whose induction succeeds. That guarantee is deliberately declined, not
  # absent — it is recoverable by empirical re-derivation, whereas a standardized
  # instrumentation format would not be recoverable from. Nothing here reads as
  # pending, because nothing is.
  want_attempts=$(( $(wc -l < "$FIX/prove/attempts.txt" | tr -d ' ') - 1 ))
  echo "  CANDIDATE SCRIPT COMPARISON: OUTSIDE THE CONFORMANCE SURFACE (#139)"
  echo "    prove/attempts.txt pins $want_attempts candidate scripts for the reference kernel"
  echo "    and is guarded by its Go test suite. §7.2's rules DETERMINE these bytes for"
  echo "    any kernel; §10 does not require exposing them, so this harness does not ask."
  echo "  f(script bytes, solver, rlimit) determines the DIRECT-ATTEMPT outcome under the"
  echo "  recorded lemma state — not every goal's verdict: one proved by induction rests"
  echo "  on scripts compared by nothing here, and a cold fixpoint emits further scripts"
  echo "  under smaller intermediate lemma sets. Re-deriving those is what full mode buys."

  echo
  if [ $fail -eq 0 ]; then
    echo "CONFORMANCE: PASS (checks 1-4 + byte oracle over the direct attempts)"
  else
    echo "CONFORMANCE: FAIL"
  fi
  exit $fail
fi

echo "== Proving (z3) — shared by checks 5-6, this is the slow step =="
# Author hints (#67, SPEC §7.2/§10) are store METADATA — not in `.oath` source
# and not in any hash — so a kernel that elaborates only source cannot know
# them. They reach us the same way the recorded proof state does: through the
# fixture channel. `--hints` takes ONLY the hint lists from outcomes.json (never
# the recorded verdicts), so the re-derivation stays cold; without it the three
# hinted goals would be attempted with a lemma set the reference never used.
# WALL CAP HEADROOM. The cap is a per-goal WALL limit, and it is the thing that
# turns gradual corpus growth into an abrupt failure: nothing degrades visibly
# until a goal crosses it, then that goal has no valid verdict and check 6 cannot
# complete. The corpus grew 2.6x in a fortnight (162 -> 419 direct-attempt
# scripts) while the cap stayed at its default, which is how a run that used to
# finish began aborting.
#
# So it scales with the work rather than being re-raised whenever it bites — this
# is the SECOND time raising it has been the fix (see the commit titled "CI
# full-conformance job: raise the wall cap").
#
# BE HONEST ABOUT WHAT THIS BUYS: headroom improves COMPLETION RELIABILITY. It
# does not improve throughput. Goals still run strictly serially, so a bigger cap
# makes a slow run slower rather than faster. Throughput needs proof sharding,
# which is a separate change to the kernel (see the --shard work).
if [ -z "${OATHRS_Z3_WALL_CAP_MS:-}" ]; then
  _goals=$(wc -l < "$FIX/prove/scripts.txt" 2>/dev/null | tr -d ' ')
  [ -z "$_goals" ] && _goals=0
  # 600s floor, plus 2s of headroom per direct-attempt script. At 419 scripts that
  # is ~1438s. The multiplier is a judgement, not a derivation: it tracks growth
  # without unbounded patience, and the report below shows whether it is enough.
  _cap=$(( 600000 + _goals * 2000 ))
  # BOUNDED, because scaling the cap cuts both ways. A larger per-goal ceiling means a
  # hard goal runs LONGER before aborting, so the serial total grows — and the CI job
  # has a 350-minute timeout. Unbounded scaling would trade "aborted goals" for "killed
  # job", which is strictly worse: an abort is reported with diagnostics, a killed job
  # leaves nothing. 20 minutes per goal is the ceiling until sharding makes total wall
  # time tractable.
  _max=1200000
  [ "$_cap" -gt "$_max" ] && _cap=$_max
  export OATHRS_Z3_WALL_CAP_MS="$_cap"
  echo "  wall cap: ${_cap}ms per goal (600s floor + 2s x ${_goals} scripts, capped at 20m)"
else
  echo "  wall cap: ${OATHRS_Z3_WALL_CAP_MS}ms (from the environment)"
fi
echo "  NOTE: the cap governs COMPLETION, not speed. Goals run serially, so a larger"
echo "        cap makes a slow run LONGER, never shorter. Throughput needs proof"
echo "        sharding; until then this job may still exceed a CI time limit, and that"
echo "        is a scheduling fact rather than a kernel divergence."

_t0=$(date +%s)
"$BIN" prove --hints "$FIX/prove/outcomes.json" $SRC > "$TMP/prove.txt" 2> "$TMP/prove.err"
pstat=$?
_elapsed=$(( $(date +%s) - _t0 ))
echo "  proving wall time: ${_elapsed}s"
if [ $pstat -ne 0 ]; then
  echo "  FAIL: prove command errored (exit $pstat)"; sed -n '1,5p' "$TMP/prove.err"; fail=1
fi
# SPEC §7.2 attempt validity is PER PROPERTY (#72): an environmental abort (wall
# cap, missing telemetry, memout) no longer invalidates the run — the run
# succeeds with partial results and marks the aborted properties '!'. Such a
# property has NO valid verdict, so conformance can neither confirm nor deny its
# outcome: report it as an ENVIRONMENTAL inconclusive (not a divergence) and fail
# the run, since check 6 requires every outcome to be re-derived.
if grep -q '!' "$TMP/prove.txt"; then
  echo "  FAIL (environmental, NOT a kernel divergence): properties aborted with no valid verdict."
  echo "        Typically slow hardware: the wall cap fired before the rlimit was"
  echo "        exhausted. Re-run with a raised OATHRS_Z3_WALL_CAP_MS (the cap never"
  echo "        affects outcomes — they are f(script bytes, solver, rlimit))."
  grep '!' "$TMP/prove.txt" | sed -n '1,10p' | sed 's/^/          /'
  grep '^ABORTED' "$TMP/prove.err" | sed -n '1,10p' | sed 's/^/          /'
  echo "        This is a PER-GOAL z3 wall timeout, not the harness or CI job timeout."
  echo "        Distinguishing them matters: a per-goal abort leaves a property with no"
  echo "        valid verdict and is reported here; a harness or CI timeout kills the run"
  echo "        outright and leaves no report at all."
  fail=1
fi

# EARLY WARNING. A fixed ceiling fails abruptly: nothing degrades visibly until a
# goal crosses it. Reporting how close the worst goals came makes the NEXT crossing
# predictable rather than a surprise — the failure mode is gradual growth against a
# constant, so the useful signal is proximity, not the binary outcome.
if [ -s "$TMP/prove.err" ]; then
  _near=$(grep -c 'NEAR-CAP' "$TMP/prove.err" 2>/dev/null || echo 0)
  [ "$_near" -gt 0 ] && {
    echo "  WARNING: $_near goal(s) used >80% of the wall cap without aborting."
    echo "           They will abort as the corpus grows. Raise the cap or shard the work."
  }
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
"$BIN" analyze --proofs "$TMP/prove.txt" --out "$TMP/analyze" $SRC 2>/dev/null
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
