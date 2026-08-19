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

ask() {  # ask <name> <expected base or ->
  printf '  %-12s ' "$1"
  # Status from the COMMAND, not a pipeline: `oath find | sed` reports sed's.
  if ! out=$("$root/oath/oath" find --equiv "$1" 2>&1); then
    echo "FAILED: find --equiv $1" >&2; printf '%s\n' "$out" | sed 's/^/    /' >&2; exit 1
  fi
  case "$out" in
    *"no other definition normalizes to the same form"*) echo "NOT connected   (rule absent)";;
    *) echo "connected       (rule present)";;
  esac
}

echo "########## rules #65 lists as REMAINING for rung 1"
ask e-unit        # x + 0
ask e-distrib     # x * 1
ask b-idem        # and p p
ask b-orid        # or p false
ask n-negneg      # neg (neg x)
ask eta-wrap      # eta
ask if-same       # if c x x, condition is a Bool BINDER (a value)
echo "########## and the SAME rule with a COMPUTED condition — restricted on purpose:"
echo "########## canon.go: dropping a divergent computation would remove divergence,"
echo "########## not preserve meaning. NOT connected here is CORRECT."
ask n-ifsame      # if (< a 0) x x
echo
echo "########## the control: commutativity, known present before this run"
ask e-comm
echo
echo "########## DISTRIBUTIVITY — --equiv misses it, as #65 says"
ask d-fact
echo "########## but --implies proves BOTH directions in one query:"
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
printf '%s\n' "$out" | sed -n '2,8p' | sed 's/^/  /'
rm -rf "$store"
