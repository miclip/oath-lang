#!/bin/zsh
# B2 measurement: does "lawful dictionaries" deliver the round-trip the friction list
# asked for? This RUNS the kernel to isolate the two independent walls that stop a
# generic property from being PROVEN — lawlessness (B2's target) and induction reach
# (a separate prover limit) — and to measure where a lawful-eq hypothesis actually
# buys provability. z3 is resource-limited so the unprovable goal fails in seconds.
# Analysis: b2-measurement.md.
set -u
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a repo" >&2; exit 1; }
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
here="$root/docs/experiments/generic-consumer"

export OATH_STORE=$(mktemp -d)
trap 'rm -rf "$OATH_STORE"' EXIT
cap() { OATH_PROVE_RLIMIT=12000000 OATH_PROVE_WALLCAP_SEC=90 OATH_PROVE_MEMORY_MB=2500 "$@"; }

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

"$OATH" put "$root/examples/list.oath" --new >/dev/null 2>&1
"$OATH" put "$root/examples/generic.oath" --new >/dev/null 2>&1
"$OATH" put "$here/rle.oath" --new >/dev/null 2>&1
put=$("$OATH" put "$here/b2.oath" --new 2>&1)

echo "1 — the blocker is NOT lawlessness: with the concretely LAWFUL ==, RLE round-trip is UNPROVEN"
# Warm the lemma library by proving the dependencies (best effort), then prove the
# round-trip. The primary claim needs only "eq is lawful AND the verdict is a clean
# UNPROVEN (not an abort)". A dependency that ABORTS means the environment is
# under-resourced and the measurement cannot be attributed — fail loudly rather than
# read a resource abort as an intrinsic wall.
depbad=""
for d in length append replicate rle-decode; do
  o=$(cap "$OATH" prove "$d" 2>&1)
  # a fully-proven dependency prints "proven: N/N" and no "· unproven"/abort/error line
  if ! printf '%s' "$o" | grep -q 'proven:' || printf '%s' "$o" | grep -qiE 'unproven|abort|error'; then
    depbad="$depbad $d"
  fi
done
out=$(cap "$OATH" prove rle-mono 2>&1)
if [ -n "$depbad" ]; then
  bad "dependencies did not all fully prove ($depbad) — cannot attribute the round-trip to the induction wall"
elif printf '%s' "$out" | grep -q 'unproven  round-trips-mono' && printf '%s' "$out" | grep -q 'induction did not discharge'; then
  pass "every dependency proven, yet round-trips-mono is UNPROVEN — 'induction did not discharge' with a lawful eq: §3's induction wall, not lawlessness"
else bad "expected round-trips-mono unproven for the induction reason; got: $(printf '%s' "$out" | grep -iE 'round-trips|proven|abort|induction' | tr '\n' ' ')"; fi

echo "2 — WALL 1, and it is FIDELITY not equivalence: an always-equal LAWFUL equivalence still breaks the round-trip"
# {eq (fn _ _ true)} is reflexive, symmetric and transitive — a lawful EQUIVALENCE — yet
# it groups distinct elements, so decode reproduces one. Ordinary Eq laws are not enough
# for RLE; the fidelity law eq=(==) is. [1,3,5] -> all grouped -> [1,1,1].
got=$("$OATH" eval "(rle-decode [Int] (rle [Int] {eq (fn [(a Int)(b Int)] true)} (Cons [Int] 1 (Cons [Int] 3 (Cons [Int] 5 (Nil [Int]))))))" 2>&1)
if printf '%s' "$got" | grep -q '(Cons 1 (Cons 1 (Cons 1 Nil)))'; then
  pass "a lawful equivalence yields [1,1,1] != [1,3,5] — RLE needs fidelity (eq=(==)), not just Eq laws"
else bad "expected [1,1,1] from an always-equal eq; got: $(printf '%s' "$got" | tail -1)"; fi

echo "3 — B2's target: the induction-REACHABLE law is FALSIFIED over all dictionaries"
if printf '%s' "$put" | grep -q 'FALSIFIED: reflexive'; then
  pass "eqby-refl (list-eq-by reflexivity) FALSIFIED by a non-reflexive eq — exactly what a lawful-eq hypothesis would exclude"
else bad "expected eqby-refl FALSIFIED; got: $(printf '%s' "$put" | grep -iE 'reflexive|falsif|tested' | tr '\n' ' ')"; fi

echo "4 — and it PROVES once the eq is lawful: the same reflexivity, monomorphic with =="
out=$(cap "$OATH" prove list-eqm 2>&1)
if printf '%s' "$out" | grep -q 'PROVEN    reflexive'; then
  pass "list-eqm reflexive PROVEN by induction — reflexivity is induction-reachable, so B2 would buy provability HERE"
else bad "expected list-eqm reflexive PROVEN; got: $(printf '%s' "$out" | grep -iE 'reflexive|proven' | tr '\n' ' ')"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks; the two walls reproduce under a capped z3"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"; exit 1
fi
