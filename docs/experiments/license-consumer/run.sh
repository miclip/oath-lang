#!/bin/zsh
# license-consumer falsifier: the DERIVED-licensing experience — the preview a
# publisher gets before committing terms (demand 2, the substantive finding), plus
# the contagion MECHANIC shown with a deliberately unlicensed dependency. It
# publishes real signed envelopes (only a publication asserts terms — §12.3) to a
# served registry and reads the verdict the registry computes.
#
# NOTE: `List` is published UNLICENSED here to exercise contagion. That is NOT how
# the live registry ships the standard library — verified, registry.oath-lang.org
# derives commercial_use YES with Apache-2.0 across the closure. An earlier draft
# mistook this experiment's local store for the world; see the friction doc §1.
set -u
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a repo" >&2; exit 1; }
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
here="$root/docs/experiments/license-consumer"

W=$(mktemp -d); REG=$(mktemp -d)
"$OATH" keygen --out "$W/holder" >/dev/null 2>&1
printf '%s\n' '{"dummy-token-abcdef123456":{"principal":"reader"}}' > "$W/tokens.json"
OATH_STORE="$REG" "$OATH" serve --http 127.0.0.1:8797 --tokens "$W/tokens.json" >"$W/serve.log" 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null; rm -rf "$W" "$REG"' EXIT
URL="http://127.0.0.1:8797"
for i in $(seq 1 100); do curl -s "$URL/" 2>/dev/null | grep -q . && break; sleep 0.1; done
curl -s "$URL/" >/dev/null 2>&1 || { echo "server never came up:"; cat "$W/serve.log"; exit 1; }

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

# The publisher's working store; put the foundation so the app elaborates locally.
F=$(mktemp -d)
OATH_STORE="$F" "$OATH" put "$here/base-list.oath" --new >/dev/null 2>&1

# lic <name> — the DERIVED verdict for one grant field, read from the registry.
verdict() { OATH_STORE="$REG" "$OATH" license "$1" 2>&1; }

echo "1 — publish the List datatype UNLICENSED (a deliberately untermed dependency)"
OATH_STORE="$F" "$OATH" publish --remote "$URL" --key "$W/holder.key" -y "$here/base-list.oath" >/dev/null 2>&1
OATH_STORE="$F" "$OATH" put "$here/tally.oath" --new >/dev/null 2>&1
# DEMAND 2, RESOLVED. Before the permanent act, PREVIEW what asserting Apache-2.0
# would yield for this file against the local closure — check-then-publish, not
# publish-then-check.
# Snapshot the store the preview ACTUALLY runs against ($F), not the registry — a
# preview that accidentally wrote would write HERE, so this is the boundary the
# no-mutation claim is about (CLAUDE.md: measure the right universe).
# Hash EVERY file's path and contents — names, journal, objects and the whole
# meta tree — so a preview that rewrote any object or metadata record would be
# caught, not just one that added a name.
snap() { find "$1" -type f -exec shasum {} + 2>/dev/null | sort; }
f_before=$(snap "$F")
pv=$(OATH_STORE="$F" "$OATH" license "$here/tally.oath" --assert Apache-2.0 2>&1)
if printf '%s' "$pv" | grep -Eq 'commercial use +UNSTATED' &&
   printf '%s' "$pv" | grep -Eq 'tally +Apache-2.0' &&
   printf '%s' "$pv" | grep -q '^PREVIEW' &&
   [ "$f_before" = "$(snap "$F")" ]; then
  pass "PREVIEW: oath license --assert derives the composition verdict WITHOUT writing to its own store"
else bad "expected a preview verdict that published nothing; got: $(printf '%s' "$pv" | grep -iE 'commercial|PREVIEW' | tr '\n' ' ')"; fi

echo "  ...and publish a tally application that asserts Apache-2.0 and depends on List"
OATH_STORE="$F" "$OATH" publish --remote "$URL" --key "$W/holder.key" --license Apache-2.0 -y "$here/tally.oath" >/dev/null 2>&1

out=$(verdict tally)
# The preview PREDICTED the published verdict: both derive all-UNSTATED for the same
# composition. The grant lines are what a publisher acts on, so those must match.
pv_grants=$(printf '%s' "$pv" | grep -E 'commercial use|redistribution|modification|patent grant|share-alike')
pub_grants=$(printf '%s' "$out" | grep -E 'commercial use|redistribution|modification|patent grant|share-alike')
if [ -n "$pv_grants" ] && [ "$pv_grants" = "$pub_grants" ]; then
  pass "the preview's grants MATCH the published verdict's — check-then-publish is faithful"
else bad "preview did not predict the published verdict:\n  preview: $(printf '%s' "$pv_grants" | tr '\n' ';')\n  actual:  $(printf '%s' "$pub_grants" | tr '\n' ';')"; fi
if printf '%s' "$out" | grep -Eq 'tally +Apache-2.0' && printf '%s' "$out" | grep -Eq 'List.*no terms asserted'; then
  pass "the registry records tally's Apache-2.0 and lists List as an assertion with NO terms"
else bad "expected tally licensed and List unlicensed in the assertion list; got: $(printf '%s' "$out" | grep -iE 'tally|list' | tr '\n' ' ')"; fi

echo "2 — the contagion MECHANIC: one unlicensed dependency drowns out the app's Apache-2.0"
# Apache-2.0 grants commercial/redistribute/modify YES on its own; here they are all
# UNSTATED because ONE unlicensed dependency (List) is contagious. This is the engine
# behaving correctly, not a claim about the stdlib (which the live registry licenses).
if printf '%s' "$out" | grep -Eq 'commercial use +UNSTATED' &&
   printf '%s' "$out" | grep -Eq 'modification +UNSTATED' &&
   printf '%s' "$out" | grep -q 'CONTAGIOUS'; then
  pass "one unlicensed dependency makes every grant UNSTATED — absence of a grant is not a grant"
else bad "expected all-UNSTATED contagion from the unlicensed List; got: $(printf '%s' "$out" | grep -iE 'commercial|modif|unstated' | tr '\n' ' ')"; fi

echo "3 — CONTROL: relicense List as Apache-2.0 too, and the SAME app now derives a real verdict"
# Relicensing is a re-publication of identical content under new terms (§12.3), so
# the code and hash do not move; only the asserted terms do. This isolates the
# finding as 'the stdlib is unlicensed', not 'the tool cannot derive'.
OATH_STORE="$F" "$OATH" publish --remote "$URL" --key "$W/holder.key" --license Apache-2.0 -y "$here/base-list.oath" >/dev/null 2>&1
out=$(verdict tally)
if printf '%s' "$out" | grep -Eq 'commercial use +YES' &&
   printf '%s' "$out" | grep -Eq 'modification +YES'; then
  pass "with List also licensed, the composition derives commercial/modification YES — the engine works, the corpus was the gap"
else bad "expected a determinate YES verdict once every input is licensed; got: $(printf '%s' "$out" | grep -iE 'commercial|modif' | tr '\n' ' ')"; fi

echo "4 — composition of DIFFERENT licenses: relicense List as GPL-3.0-only"
# The app asserts Apache-2.0, the dependency GPL-3.0-only. share-alike is an
# OBLIGATION, so it propagates up: the composition inherits GPL's share-alike.
OATH_STORE="$F" "$OATH" publish --remote "$URL" --key "$W/holder.key" --license GPL-3.0-only -y "$here/base-list.oath" >/dev/null 2>&1
out=$(verdict tally)
if printf '%s' "$out" | grep -Eq 'share-alike obligation +YES'; then
  pass "an Apache app over a GPL dependency inherits the share-alike obligation — the composition is copyleft"
else bad "expected share-alike YES from the GPL dependency; got: $(printf '%s' "$out" | grep -iE 'share-alike' | tr '\n' ' ')"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks; the demand list reproduces against a live registry"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"; exit 1
fi
