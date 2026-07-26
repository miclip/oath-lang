#!/usr/bin/env bash
# Push the example corpus to a remote Oath registry, in dependency order.
#
# Why this exists: the registry is a re-proving REPLICA of the committed store,
# not a mirror of it, and the two drift. Verdicts are recomputed by the registry
# from content — that is the whole trust story — so a definition whose verdict
# was computed under a BROKEN registry (e.g. z3 unavailable) keeps that wrong
# verdict forever: the prove-worker re-proves properties but never re-derives
# termination. Re-putting is what recomputes it.
#
# Ordering matters on a cold registry (a definition cannot be elaborated before
# its dependencies exist) and is harmless on a warm one, so this always pushes in
# the Makefile's topological EXAMPLES order.
#
# Usage:
#   OATH_REGISTRY=https://registry.oath-lang.org \
#   OATH_TOKEN=<write-capable bearer token> \
#   scripts/push-corpus.sh [--dry-run] [file ...]
#
# With no file arguments, pushes the full corpus (EXAMPLES + EXHIBITS). Named
# files are pushed as given, which is how you test a single definition first.
# Never commit a token; fetch it from the secret store at call time.
set -euo pipefail

REGISTRY="${OATH_REGISTRY:?set OATH_REGISTRY, e.g. https://registry.oath-lang.org}"
TOKEN="${OATH_TOKEN:?set OATH_TOKEN to a write-capable bearer token}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

DRY=0
FILES=()
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    *) FILES+=("$a") ;;
  esac
done

# Dependency order, kept in ONE place: the Makefile. Parsing it here means the
# order cannot drift between `make verify` and a registry push — the divergence
# that left the rational family off the registry entirely.
if [ ${#FILES[@]} -eq 0 ]; then
  read -r -a ORDER <<< "$(make -C "$ROOT" --no-print-directory print-order)"
  for f in "${ORDER[@]}"; do FILES+=("examples/$f.oath"); done
fi

echo "registry: $REGISTRY"
echo "files:    ${#FILES[@]}"
[ "$DRY" -eq 1 ] && echo "(dry run — nothing will be sent)"
echo

ok=0; blocked=0; failed=0
for rel in "${FILES[@]}"; do
  path="$ROOT/$rel"
  [ -f "$path" ] || { echo "MISSING  $rel"; failed=$((failed+1)); continue; }
  name="$(basename "$rel" .oath)"

  if [ "$DRY" -eq 1 ]; then printf '  would push %s\n' "$name"; ok=$((ok+1)); continue; fi

  # jq -Rs builds a correctly escaped JSON string from arbitrary source text.
  body="$(jq -Rs --arg m tools/call \
    '{jsonrpc:"2.0",id:1,method:$m,params:{name:"put",arguments:{source:.}}}' < "$path")"

  resp="$(curl -sS --max-time 180 "$REGISTRY/mcp" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'content-type: application/json' \
    -d "$body" 2>&1)" || { echo "HTTP FAIL $name"; failed=$((failed+1)); continue; }

  text="$(printf '%s' "$resp" | jq -r '.result.content[0].text // .error.message // "no response"' 2>/dev/null)"
  nacc="$(printf '%s' "$text" | grep -c '✓' || true)"
  nblk="$(printf '%s' "$text" | grep -ciE 'blocked|rejected' || true)"
  ok=$((ok+nacc)); blocked=$((blocked+nblk))
  printf '  %-14s accepted=%-3s blocked=%s\n' "$name" "$nacc" "$nblk"
  [ "$nblk" -gt 0 ] && printf '%s\n' "$text" | grep -iE 'blocked|rejected' | sed 's/^/      /'
done

echo
echo "accepted: $ok   blocked: $blocked   failed: $failed"
[ "$failed" -eq 0 ]
