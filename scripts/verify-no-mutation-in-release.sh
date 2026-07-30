#!/usr/bin/env bash
# Binary-level gate for the conformance-mutation boundary.
#
# The property under test is behavioural, not structural:
#
#   No invocation of the production binary, under any arguments or environment,
#   can disable a normative verification rule.
#
# Source-level tests cannot establish that. They run in a build that may include the
# machinery, and they check what the code does rather than what the shipped artifact
# CONTAINS. This gate builds the release binary and interrogates it directly.
#
# Why it matters more than ordinary hygiene: the mutation machinery exists to
# deliberately corrupt a verifier so its coverage can be measured. That is exactly the
# capability an attacker wants, and shipping it inside the artifact whose guarantees it
# switches off would mean the measurement tooling had become the vulnerability.
set -euo pipefail

cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail=0
note() { printf '  %-6s %s\n' "$1" "$2"; }

echo "== 1. build the release binary (no tags) =="
( cd oath && go build -o "$tmp/oath" . )
note PASS "built $(basename "$tmp")/oath"

echo "== 2. the scorer command must be ABSENT, not merely refused =="
# Absent means the dispatcher has no entry: the binary falls through to usage rather
# than reporting a disabled or unauthorized command.
out="$("$tmp/oath" conformance-score 2>&1 || true)"
if grep -qi "conformance mutation score\|obligations witnessed" <<<"$out"; then
  note FAIL "the release binary RAN the scorer"; fail=1
else
  note PASS "no scorer command"
fi

echo "== 3. mutation-specific markers must be absent — and each must be DISCRIMINATING =="
# A marker that appears in NEITHER build is worse than no check: it passes on the
# release binary for reasons unrelated to the machinery, and inflates the count of
# checks that appear to have run. The first version of this gate had exactly that
# defect — `disabledRules` is an unexported variable name Go does not retain in the
# string table, so the check could never fire in either direction.
#
# Every marker is therefore validated against BOTH builds: present in the tagged one
# (proving the check can detect the machinery) and absent from the release one (the
# property under test). Only string constants survive reliably; identifiers do not.
( cd oath && go build -tags conformance_mutation -o "$tmp/oath-tagged" . )
for marker in "disable-one-normative-rule" "withRulesDisabled" "harness/known-noop"; do
  in_tagged=$(strings "$tmp/oath-tagged" 2>/dev/null | grep -c -- "$marker" || true)
  in_release=$(strings "$tmp/oath" 2>/dev/null | grep -c -- "$marker" || true)
  if [ "$in_tagged" -eq 0 ]; then
    note FAIL "marker is NON-DISCRIMINATING (absent from the tagged build too): $marker"; fail=1
  elif [ "$in_release" -ne 0 ]; then
    note FAIL "found marker in release binary: $marker"; fail=1
  else
    note PASS "absent from release, present in tagged: $marker"
  fi
done

echo "== 4. the release binary still runs the published vector surface =="
if "$tmp/oath" vectors fixtures/envelope/vectors.jsonl 2>&1 | grep -q "VECTORS: PASS"; then
  note PASS "vectors run and pass"
else
  note FAIL "the release binary cannot run its own published fixtures"; fail=1
fi

echo "== 5. the tagged scorer builds separately and its anchors calibrate =="
score="$("$tmp/oath-tagged" conformance-score 2>&1 || true)"
if grep -q "obligations witnessed" <<<"$score"; then
  note PASS "scorer reports a measurement"
else
  note FAIL "the tagged scorer did not produce a score"; fail=1
fi
if grep -qi "HARNESS SELF-TEST FAILED" <<<"$score"; then
  note FAIL "the scorer's calibration anchors did not hold"; fail=1
else
  note PASS "calibration anchors hold (a load-bearing rule is witnessed; an inert one is not)"
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "MUTATION BOUNDARY: FAIL — the release artifact can weaken verification, or the"
  echo "scorer cannot measure. Both are release blockers."
  exit 1
fi
echo "MUTATION BOUNDARY: PASS"
echo "  the release binary contains no code path capable of disabling a verification"
echo "  rule, and the scorer is a separate artifact with its own build path."
