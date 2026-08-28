#!/bin/sh
# The instrument behind docs/experiments/issue-65-rungs.md.
#
# For each rule #65 lists as REMAINING, put a variant beside a base it is equal
# to and ask --equiv whether the two normalize together. A rule that is already
# implemented connects them; one that is not, does not.
#
# WHY VARIANTS AND NOT THE CORPUS. --equiv over all 238 live names finds ZERO
# equivalences, because a curated corpus holds no redundant definitions. That
# measures the CORPUS, not the RULES. These variants measure the rules.
#
# THE COMMITTED STORE IS NOT TOUCHED: everything runs against a temporary copy,
# and `put --new` is used there deliberately — in a throwaway store a new name
# is a reconstruction, not a publication decision, which is why the guard on
# fresh bindings does not apply.
set -eu
root=$(git rev-parse --show-toplevel)
here=$(dirname "$0")
( cd "$root" && make build >/dev/null )
store=$(mktemp -d); cp -R "$root/codebase/"* "$store/"
export OATH_STORE="$store"

echo "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ codebase/ docs/experiments/issue-65-rungs/)" ] || echo ' +MODIFIED')"
echo

for f in rules-arith rules-neg-bool rules-distributivity rules-eta rules-if-value-condition; do
  if ! out=$("$root/oath/oath" put "$here/$f.oath" --new 2>&1); then
    echo "FAILED: put $f.oath" >&2; printf '%s\n' "$out" | sed 's/^/    /' >&2; exit 1
  fi
done

# ask <name> <yes|no> — query --equiv and CHECK the answer against what this
# record claims.
#
# IT ASSERTS RATHER THAN REPORTS, and that is the difference between a control
# and a decoration. Three rows here expect NOT-connected on purpose — a
# restricted rule (n-ifsame) and two rung-1b controls (df-fact over Float,
# d-other a different function) — and under the old print-only helper each would
# have rendered "connected (rule present)" if the rule ever went wrong, which
# reads exactly like every passing row above it. An expectation that cannot fail
# is not a control.
ask() {
  printf '  %-12s ' "$1"
  # Status from the COMMAND, not a pipeline: `oath find | sed` reports sed's.
  if ! out=$("$root/oath/oath" find --equiv "$1" 2>&1); then
    echo "FAILED: find --equiv $1" >&2; printf '%s\n' "$out" | sed 's/^/    /' >&2; exit 1
  fi
  case "$out" in
    *"no other definition normalizes to the same form"*) got=no;;
    *) got=yes;;
  esac
  if [ "$got" != "$2" ]; then
    if [ "$2" = yes ]; then echo "NOT connected   ** EXPECTED CONNECTED **"
    else echo "connected       ** EXPECTED NOT CONNECTED **"; fi
    echo "MISMATCH: $1 expected $2, got $got" >&2
    exit 1
  fi
  if [ "$got" = yes ]; then echo "connected       (rule present)"
  else echo "NOT connected   (correctly refused)"; fi
}

echo "########## rules #65 lists as REMAINING for rung 1"
ask e-unit yes        # x + 0
ask e-distrib yes     # x * 1
ask b-idem yes        # and p p
ask b-orid yes        # or p false
ask n-negneg yes      # neg (neg x)
ask eta-wrap yes      # eta
ask if-same yes       # if c x x, condition is a Bool BINDER (a value)
echo "########## and the SAME rule with a COMPUTED condition — restricted on purpose:"
echo "########## canon.go: dropping a divergent computation would remove divergence,"
echo "########## not preserve meaning. NOT connected here is CORRECT."
ask n-ifsame no      # if (< a 0) x x
echo
echo "########## the control: commutativity, known present before this run"
ask e-comm yes
echo
echo "########## DISTRIBUTIVITY — rung 1b: the e-graph now connects it"
ask d-fact yes
echo "########## and the two controls that tell the rule from a wholesale collapse."
echo "########## Float: binary64 rounds, so the two forms disagree on real inputs."
echo "########## d-other (a*b + b*c) is simply a DIFFERENT function from d-fact."
echo "########## NOT connected is CORRECT for both."
ask df-fact no
ask d-other no
echo "########## --implies proves the Int case too, by a different mechanism:"
# STATUS FROM THE COMMAND, like `ask` above. Piping straight into sed reports
# SED's status, so `set -e` would not fire if the prover were unavailable or the
# query invalid — and the script would exit 0 having printed nothing. This is the
# same hazard the ask helper already guards, reintroduced on the one line that
# did not use it.
if ! out=$("$root/oath/oath" find --implies "$here/distributivity-implies-query.oath" 2>&1); then
  echo "FAILED: find --implies distributivity-implies-query.oath" >&2
  printf '%s\n' "$out" | sed 's/^/    /' >&2
  exit 1
fi
# THE WINDOW REACHES THE REFUTATIONS ON PURPOSE. The last line is df-expand's
# Float countermodel — the prover independently refuting the very law the
# e-graph refuses to apply at that type. Two mechanisms, one exclusion, and a
# narrower window would print only the agreements.
printf '%s\n' "$out" | sed -n '2,11p' | sed 's/^/  /'
rm -rf "$store"
