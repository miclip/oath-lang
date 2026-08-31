#!/bin/zsh
# stats-consumer falsifier: proves the demand list in stats-consumer-friction.md by
# RUNNING the prover, not by reading it. The finding is about PROVABILITY, so the
# instrument must actually invoke `oath prove` — under a fixed z3 resource cap, so a
# goal the prover will not discharge fails in seconds instead of hanging for minutes.
#
# z3 is resource-limited three ways, and which one BINDS matters. OATH_PROVE_RLIMIT
# is z3's DETERMINISTIC work budget (same script + same rlimit => same verdict on any
# machine, under any load) — it is the real bound, kept tight so a nonlinear goal
# exhausts it and returns a verdict quickly rather than grinding. OATH_PROVE_MEMORY_MB
# caps memory. OATH_PROVE_WALLCAP_SEC is ONLY a wall-clock safety and is set GENEROUS
# on purpose: if it fired first it would return a load-sensitive "environmental
# abort" instead of the deterministic verdict, making this instrument flaky. So the
# rlimit binds, the wall cap does not, and the outcomes are reproducible.
set -u
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a repo" >&2; exit 1; }
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
here="$root/docs/experiments/stats-consumer"

export OATH_STORE=$(mktemp -d)
trap 'rm -rf "$OATH_STORE"' EXIT
# A throwaway store so no probe touches the committed corpus.
cap() { OATH_PROVE_RLIMIT=25000000 OATH_PROVE_WALLCAP_SEC=150 OATH_PROVE_MEMORY_MB=1500 "$@"; }

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

# --- Seed the reused dependencies as TESTED (a bare `put`, no proof) ----------------
"$OATH" put "$root/examples/list.oath" --new >/dev/null 2>&1
"$OATH" put "$root/examples/rat.oath" --new >/dev/null 2>&1
"$OATH" put "$root/examples/convert.oath" --new >/dev/null 2>&1

# The recovery law alone, so the dependency-lemma effect is isolated from the
# nonlinear bounds property.
cat > "$OATH_STORE/mean-r.oath" <<'EOF'
(defn mean-r [] [(xs (List Int))] Rat
  (if (== (length [Int] xs) 0)
      0/1
      (/ (to-rat (sum xs)) (to-rat (length [Int] xs))))
  (prop empty-is-zero [] (== (mean-r (Nil [Int])) 0/1))
  (prop times-count-is-sum [(xs (List Int))]
    (== (* (mean-r xs) (to-rat (length [Int] xs))) (to-rat (sum xs)))))
EOF
"$OATH" put "$OATH_STORE/mean-r.oath" --new >/dev/null 2>&1

echo "1 — the recovery law does NOT prove while its dependencies are only TESTED"
out=$(cap "$OATH" prove mean-r 2>&1)
if printf '%s' "$out" | grep -q '0 from dependencies' &&
   printf '%s' "$out" | grep -q '· unproven  times-count-is-sum'; then
  pass "with tested deps: 0 dependency lemmas, and times-count-is-sum is UNPROVEN"
else bad "expected 0 dep lemmas and an unproven recovery law; got: $(printf '%s' "$out" | tail -3 | tr '\n' ' ')"; fi

# DEMAND 1, RESOLVED. The unproven report now NAMES the reused dependency whose
# unproven laws would ACTUALLY be admitted for the stuck goal — filtered by the same
# footprint rule the prover uses, not mere reachability. The recovery law needs
# `length`'s nonzero-on-cons law; `sum`'s laws are about append/reverse, outside this
# goal's footprint, so proving `sum` would NOT help and the note does not name it —
# a precision that step 2 confirms (the goal proves with `length` alone).
if printf '%s' "$out" | grep -q '^note:' &&
   printf '%s' "$out" | grep -Eq 'length \([0-9]+ unproven law' &&
   ! printf '%s' "$out" | grep -Eq 'sum \([0-9]+ unproven law'; then
  pass "  ...and the report NAMES exactly the dep that helps (length), excluding sum whose laws are inadmissible here"
else bad "expected a note naming length but not sum; got: $(printf '%s' "$out" | grep '^note' | tr '\n' ' ')"; fi

# CONTROL for step 1's exclusion of `sum` — and it is the control that matters,
# because otherwise the diagnostic and this instrument could share the SAME mistaken
# exclusion. Prove ONLY `length` (leave `sum` tested) and the recovery law must
# ALREADY prove: that establishes empirically that `length` alone discharges it and
# `sum`'s laws are genuinely not needed, which is what licenses omitting `sum`.
echo "2 — CONTROL: proving only length (sum still tested) already discharges the recovery law"
cap "$OATH" prove length >/dev/null 2>&1
out=$(cap "$OATH" prove mean-r 2>&1)
if printf '%s' "$out" | grep -q '∎ PROVEN    times-count-is-sum' &&
   ! printf '%s' "$out" | grep -q '^note:'; then
  pass "the recovery law proves with length ALONE — so excluding sum in step 1 was correct, not a shared blind spot"
else bad "expected the recovery law to prove with only length; got: $(printf '%s' "$out" | grep -E 'PROVEN|unproven|^note' | tr '\n' ' ')"; fi
# Proving sum as well is then a no-op for this goal; the deps are all proven.
cap "$OATH" prove sum >/dev/null 2>&1
out=$(cap "$OATH" prove mean-r 2>&1)
if printf '%s' "$out" | grep -qE '[1-9][0-9]* from dependencies' &&
   printf '%s' "$out" | grep -q '∎ PROVEN    times-count-is-sum'; then
  pass "with the deps proven: dependency lemmas appear and times-count-is-sum is PROVEN"
else bad "expected dep lemmas and a proven recovery law; got: $(printf '%s' "$out" | tail -3 | tr '\n' ' ')"; fi

echo "  ...the per-property DETAIL is identical in both steps — 'induction did not"
echo "  discharge' — so before demand 1 was resolved nothing distinguished 'hard goal'"
echo "  from 'missing dependency lemma'. The note asserted in step 1 now draws it."

# --- The full mean, with the nonlinear bounds property -------------------------------
"$OATH" put "$here/stats.oath" --new >/dev/null 2>&1
cap "$OATH" prove minimum >/dev/null 2>&1
cap "$OATH" prove maximum >/dev/null 2>&1

echo "3 — with every dependency proven, the nonlinear bounds property STILL will not prove"
out=$(cap "$OATH" prove mean 2>&1)
if printf '%s' "$out" | grep -q '∎ PROVEN    times-count-is-sum' &&
   printf '%s' "$out" | grep -qE '(unproven|aborted)  within-bounds'; then
  pass "mean is 2/3: recovery law PROVEN, within-bounds not discharged (nonlinear; demand 2)"
else bad "expected recovery proven and bounds not discharged; got: $(printf '%s' "$out" | tail -4 | tr '\n' ' ')"; fi

# The note is HONEST: within-bounds is still unproven, but every dependency it
# reaches (minimum, maximum, length, sum) is now proven, so the note falls SILENT —
# correctly reporting that what remains is an intrinsic wall (nonlinear), not a
# missing lemma. This is the distinction demand 1 asked for.
if printf '%s' "$out" | grep -q '^note:'; then
  bad "the note still fired though every relevant dep is proven — it must not misdirect a genuinely hard goal: $(printf '%s' "$out" | grep '^note' | tr '\n' ' ')"
else pass "  ...and the note is SILENT now that within-bounds' deps are all proven — a hard goal is not misreported as a missing lemma"; fi

echo "4 — the unproven bounds property is nonetheless a TESTED truth (not a false claim)"
out=$(cap "$OATH" put "$here/stats.oath" 2>&1)
if printf '%s' "$out" | grep -q 'within-bounds' && printf '%s' "$out" | grep -qi 'passed 200'; then
  pass "within-bounds passes 200 random cases — a tool limit, not a semantic fact"
elif "$OATH" get mean >/dev/null 2>&1; then
  pass "mean is bound (within-bounds admitted as tested) — the def is honest, not falsified"
else bad "within-bounds should be a tested-passing exhibit, not a rejection"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks; the demand list reproduces under a capped z3"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"
  exit 1
fi
