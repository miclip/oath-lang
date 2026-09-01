#!/bin/zsh
# optimizer-consumer: build a verified peephole optimizer over an expression TREE and
# measure which correctness properties the prover discharges. The finding is that the
# PER-STEP soundness lemmas prove but the END-TO-END correctness (ev (opt e) = ev e)
# does not — a composed-recursion limit, NOT a tree-induction gap. The instrument RUNS
# the kernel (put/prove/hint) under a capped z3 so the divergent goal fails in seconds.
set -u
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a repo" >&2; exit 1; }
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
here="$root/docs/experiments/optimizer-consumer"

# Validate before exporting: an empty OATH_STORE falls back to ./codebase, and prove/put
# would then mutate the committed, append-only corpus.
OATH_STORE=$(mktemp -d) || { echo "SETUP FAILED: mktemp -d failed" >&2; exit 1; }
[ -d "$OATH_STORE" ] || { echo "SETUP FAILED: temp store missing" >&2; exit 1; }
export OATH_STORE
trap 'rm -rf "$OATH_STORE"' EXIT
cap() { OATH_PROVE_RLIMIT=15000000 OATH_PROVE_WALLCAP_SEC=120 OATH_PROVE_MEMORY_MB=3000 "$@"; }

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

put=$("$OATH" put "$here/optimizer.oath" --new 2>&1)
if ! printf '%s' "$put" | grep -q 'opt '; then
  echo "SETUP FAILED: optimizer.oath did not elaborate:" >&2; printf '%s\n' "$put" >&2; exit 1
fi
# A falsified property would mean a definition is WRONG, and the later prove checks would
# then report 'unproven' and let the script PASS while claiming a false transform correct.
if printf '%s' "$put" | grep -qi 'FALSIFIED'; then
  echo "SETUP FAILED: a property was FALSIFIED — a definition is wrong, not merely unproven:" >&2
  printf '%s\n' "$put" | grep -iE 'FALSIFIED|✗' >&2; exit 1
fi
# The write-up claims opt and swap TEST clean; confirm EACH def's summary line reports
# `tested` (200 cases) individually — a count across the three `preserves` props could
# reach two while opt or swap was itself indeterminate.
for d in opt swap; do
  if ! printf '%s' "$put" | grep -E "$d +#[0-9a-f]+ +tested"; then
    echo "SETUP FAILED: $d did not test clean (200 cases):" >&2
    printf '%s\n' "$put" | grep -E "$d +#" >&2; exit 1
  fi
done >/dev/null

echo "1 — the PIECES prove: per-step smart-constructor soundness is verifiable"
a=$(cap "$OATH" prove add-s 2>&1); m=$(cap "$OATH" prove mul-s 2>&1)
if printf '%s' "$a" | grep -q 'PROVEN    sound' && printf '%s' "$m" | grep -q 'PROVEN    sound'; then
  pass "add-s.sound and mul-s.sound PROVEN — folding a step preserves the value"
else bad "expected both smart-constructor soundness lemmas proven; got: $(printf '%s\n%s' "$a" "$m" | grep -iE 'sound|proven' | tr '\n' ' ')"; fi

echo "2 — the WHOLE does not: end-to-end correctness diverges even with the pieces HINTED"
# The claim is "unproven WITH both hints", so the hints MUST be confirmed installed —
# otherwise an independently-unprovable opt.preserves would pass this check vacuously.
h1=$("$OATH" hint opt preserves add-s.sound 2>&1)
h2=$("$OATH" hint opt preserves mul-s.sound 2>&1)
o=$(cap "$OATH" prove opt 2>&1)
if ! printf '%s' "$h1" | grep -q 'lemma admitted' || ! printf '%s' "$h2" | grep -q 'lemma admitted'; then
  bad "a hint did not install (add-s: $(printf '%s' "$h1" | tr '\n' ' '); mul-s: $(printf '%s' "$h2" | tr '\n' ' ')) — cannot claim opt is unproven WITH hints"
elif printf '%s' "$o" | grep -q 'unproven  preserves'; then
  pass "opt.preserves UNPROVEN with add-s.sound + mul-s.sound admitted — the composed induction does not discharge"
else bad "expected opt.preserves unproven; got: $(printf '%s' "$o" | grep -iE 'preserves|proven' | tr '\n' ' ')"; fi

echo "3 — it is NOT a tree-induction gap: the structural lemma proves and RESCUES the identity case"
i=$(cap "$OATH" prove ido 2>&1)
# The experiment's claim is specifically that STRUCTURE proves by INDUCTION and then
# PRESERVES proves DIRECTLY from it — so the strategies, not just the verdicts, must match.
si=$(printf '%s' "$i" | grep 'PROVEN' | grep 'structure')
pi=$(printf '%s' "$i" | grep 'PROVEN' | grep 'preserves')
if printf '%s' "$si" | grep -q 'induction on binder' && printf '%s' "$pi" | grep -q 'direct'; then
  pass "ido.structure (ido e = e) PROVEN by induction, and ido.preserves PROVEN directly from it — tree induction works"
else bad "expected structure by induction and preserves direct; got: $(printf '%s' "$i" | grep -iE 'structure|preserves|proven' | tr '\n' ' ')"; fi

echo "4 — but a genuine transformation has NO structural rescue: swap (correct by commutativity) does not prove"
s=$(cap "$OATH" prove swap 2>&1)
if printf '%s' "$s" | grep -q 'unproven  preserves'; then
  pass "swap.preserves UNPROVEN — a value-preserving tree transform whose correctness composes ev with swap does not discharge"
else bad "expected swap.preserves unproven; got: $(printf '%s' "$s" | grep -iE 'preserves|proven' | tr '\n' ' ')"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks; the composed-recursion demand reproduces under a capped z3"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"; exit 1
fi
