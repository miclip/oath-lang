#!/bin/zsh
# generic-consumer falsifier: building a generic run-length encoder over ANY Eq
# dictionary (#33 B1), and measuring which of its natural properties survive being
# quantified over EVERY dictionary — lawful or not. The findings are about the B1
# convention's reach, so the instrument RUNS the kernel (put/prove/eval), it does not
# read it. z3 is resource-limited (OATH_PROVE_RLIMIT the deterministic work budget,
# WALLCAP/MEMORY as safety) so the unprovable goal fails in seconds, not minutes.
set -u
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a repo" >&2; exit 1; }
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
here="$root/docs/experiments/generic-consumer"

export OATH_STORE=$(mktemp -d)
trap 'rm -rf "$OATH_STORE"' EXIT
cap() { OATH_PROVE_RLIMIT=8000000 OATH_PROVE_WALLCAP_SEC=45 OATH_PROVE_MEMORY_MB=1000 "$@"; }

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

"$OATH" put "$root/examples/list.oath" --new >/dev/null 2>&1
lawful='{eq (fn [(a Int)(b Int)] (== a b))}'

echo "1 — the generic RLE builds and its length-preservation property TESTS clean"
out=$("$OATH" put "$here/rle.oath" --new 2>&1)
if printf '%s' "$out" | grep -Eq 'rle .*tested' && printf '%s' "$out" | grep -q 'decode-preserves-length  passed'; then
  pass "rle over any Eq dictionary is admitted; decode-preserves-length passes 200 cases"
else bad "expected rle tested with length-preservation passing; got: $(printf '%s' "$out" | grep -iE 'rle|length|error' | tr '\n' ' ')"; fi

echo "2 — THE FINDING: the natural round-trip property is FALSIFIED over all dictionaries"
# B1 cannot say 'for a LAWFUL eq' (that is B2), so the generator supplies a lawless
# dictionary that groups distinct elements, and decode reproduces the run's element.
cat > "$OATH_STORE/rt.oath" <<'EOF'
(defn rle-rt [] [(eqd {eq (-> Int Int Bool)}) (xs (List Int))] (List Int)
  (rle-decode [Int] (rle [Int] eqd xs))
  (prop round-trips [(eqd {eq (-> Int Int Bool)}) (xs (List Int))]
    (== (rle-rt eqd xs) xs)))
EOF
out=$("$OATH" put "$OATH_STORE/rt.oath" --new 2>&1)
if printf '%s' "$out" | grep -q 'FALSIFIED: round-trips'; then
  pass "the natural round-trip is FALSIFIED by a lawless eq dictionary — B1 cannot constrain to lawful"
else bad "expected the round-trip FALSIFIED over all dictionaries; got: $(printf '%s' "$out" | grep -iE 'round|falsif|tested' | tr '\n' ' ')"; fi

echo "3 — yet the ALGORITHM is correct: for a lawful eq the round-trip holds"
got=$("$OATH" eval "(rle-decode [Int] (rle [Int] $lawful (Cons [Int] 3 (Cons [Int] 3 (Cons [Int] 7 (Nil [Int]))))))" 2>&1)
if printf '%s' "$got" | grep -q '(Cons 3 (Cons 3 (Cons 7 Nil)))'; then
  pass "eval: with a lawful eq, rle-decode(rle xs) = xs — the falsification is the quantifier, not the code"
else bad "expected the lawful round-trip to reproduce the input; got: $(printf '%s' "$got" | tail -1)"; fi

echo "4 — even length-preservation does not PROVE: nested recursion defeats structural induction"
# Its dependencies (length, append, replicate, rle-decode) are all provable, so the
# note stays silent — the wall is intrinsic (an aggregate over a run structure the
# induction cannot generalise), not a missing lemma. A TOOL limit, not a false claim.
cap "$OATH" prove length  >/dev/null 2>&1
cap "$OATH" prove append  >/dev/null 2>&1
cap "$OATH" prove replicate >/dev/null 2>&1
cap "$OATH" prove rle-decode >/dev/null 2>&1
out=$(cap "$OATH" prove rle 2>&1)
if printf '%s' "$out" | grep -q '· unproven  decode-preserves-length' && ! printf '%s' "$out" | grep -q '^note:'; then
  pass "length-preservation stays UNPROVEN with every dependency proven — an intrinsic induction wall, honestly a tool limit"
else bad "expected an unproven-with-no-note verdict; got: $(printf '%s' "$out" | grep -iE 'unproven|PROVEN|note' | tr '\n' ' ')"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks; the demand list reproduces under a capped z3"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"; exit 1
fi
