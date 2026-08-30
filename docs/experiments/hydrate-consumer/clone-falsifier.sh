#!/bin/sh
# Falsifier for `oath clone` (#191) — the one-shot fresh-machine reproduction that
# composes `hydrate` (populate the lock's dependency closure) and `put --lock` (admit
# and VERIFY the app), so a consumer holding only the app source, its lock, and an
# object source reaches a store `oath build <app>` works in with ONE command.
#
# THE CLAIM, stated as something that can fail:
#   clone <app> --lock <lock> --from/--remote <src> leaves a store where the app is
#   PRESENT and VERIFIED — removing the `no definition named <app>` failure hydrate
#   alone leaves behind, which is the two-command gap #191 names. The test app,
#   checklist, is a buildable ENTRY, so here "present and verified" also means
#   build+run works; clone's own guarantee is present+verified, and build's entry gate
#   is build's, which is why the mid-app and refusal checks assert on admission state.
#
# It attacks the claim: the gap is real (hydrate alone does not make build work); the
# whole loop collapses to one command over both a local and a served-registry source;
# the app is verified, not merely copied; a mid-app failure leaves the target
# untouched; and it is refused into the canonical corpus, a non-empty or policy-
# governed target, and with no source. Read-only against the repo (throwaway stores; a
# loopback server killed on exit; nothing written to codebase/).
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd)
here=$(cd "$(dirname "$0")" && pwd)
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
app="$here/checklist.oath"

unset OATH_STORE OATH_REGISTRY OATH_KEY OATH_KMS_KEY OATH_BACKEND OATH_AUTHOR \
      OATH_AUTHORIZED_KEYS OATH_HTTP_ADDR OATH_NAMESPACE OATH_HOME OATH_STDLIB \
      OATH_STORE_LOCK OATH_DB_DRIVER OATH_DB_DSN OATH_OBJECT_BUCKET 2>/dev/null || true

ledger=$(mktemp)
SRV_PID=""
cleanup() {
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null
  while read -r d; do [ -n "$d" ] && rm -rf "$d"; done < "$ledger"
  rm -f "$ledger"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mk() { d=$(mktemp -d) || { echo "SETUP FAILED: mktemp" >&2; exit 1; }; printf '%s\n' "$d" >> "$ledger"; printf '%s' "$d"; }
work=$(mk)

# --- server lifecycle, marker-ownership readiness (see hydrate-consumer/run.sh) ----
marker_probe() {
  mp_out="$work/probe.out"; : > "$mp_out"
  OATH_STORE="$1" "$OATH" ls --remote "$2" --key "$3" >"$mp_out" 2>/dev/null &
  mp_pid=$!
  ( sleep 4; kill "$mp_pid" 2>/dev/null ) >/dev/null 2>&1 & mp_wd=$!
  wait "$mp_pid" 2>/dev/null; kill "$mp_wd" 2>/dev/null; wait "$mp_wd" 2>/dev/null
  grep -q "$4" "$mp_out" 2>/dev/null
}
start_server() {
  pstore=$(mk); attempt=0
  while [ "$attempt" -lt 6 ]; do
    attempt=$((attempt + 1))
    port=$(( 20000 + ( ($$ + attempt * 251) % 20000 ) ))
    url="http://127.0.0.1:$port"
    OATH_STORE="$1" "$OATH" serve --http "127.0.0.1:$port" >"$work/serve.$port.log" 2>&1 &
    p=$!; SRV_PID="$p"; tries=0
    while [ "$tries" -lt 40 ]; do
      tries=$((tries + 1))
      if marker_probe "$pstore" "$url" "$2" "$3"; then SRV_URL="$url"; return 0; fi
      kill -0 "$p" 2>/dev/null || break
      sleep 0.25
    done
    kill "$p" 2>/dev/null; wait "$p" 2>/dev/null; SRV_PID=""
  done
  return 1
}

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
head2() { printf '\n%s\n' "$1"; }
# build_runs <store> <expected-first-line> — build checklist from <store> and run it.
build_runs() {
  b="$work/bin.$checks"
  OATH_STORE="$1" "$OATH" build checklist -o "$b" >/dev/null 2>&1 || return 1
  out=$("$b" milk eggs 2>/dev/null) || return 1
  [ "$(printf '%s' "$out" | head -1)" = "$2" ]
}

# --- SETUP: publisher store (corpus@HEAD + app), the lock, a keypair ---------------
head2 "SETUP — publisher store, lock, registry"
src=$(mk)
( cd "$root" && git archive HEAD:codebase ) | ( cd "$src" && tar -x ) || { echo "SETUP FAILED: git archive" >&2; exit 1; }
OATH_STORE="$src" "$OATH" put "$app" --new >/dev/null 2>&1 || { echo "SETUP FAILED: publisher put" >&2; exit 1; }
lock="$work/checklist.lock"
OATH_STORE="$src" "$OATH" resolve "$app" -o "$lock" >/dev/null 2>&1 || { echo "SETUP FAILED: resolve" >&2; exit 1; }
"$OATH" keygen --out "$work/k" >/dev/null 2>&1 || { echo "SETUP FAILED: keygen" >&2; exit 1; }
key="$work/k.key"
marker="CloneProbe$$X$(awk 'BEGIN{srand();printf "%06d", int(rand()*1000000)}')"
printf '(data %s [] (Mk%s))\n' "$marker" "$marker" > "$work/marker.oath"
OATH_STORE="$src" "$OATH" put "$work/marker.oath" --new >/dev/null 2>&1 || { echo "SETUP FAILED: marker put" >&2; exit 1; }
printf '  publisher OK; lock at %s\n' "$lock"

# --- 1. THE GAP IS REAL: hydrate alone does not make build work --------------------
head2 "1 — THE GAP: hydrate alone leaves the app unbuildable"
g=$(mk)
OATH_STORE="$g" "$OATH" hydrate "$lock" --from "$src" >/dev/null 2>&1 || bad "setup: hydrate failed"
if grep -q '"checklist"' "$g/names.json" 2>/dev/null; then
  bad "checklist is bound after hydrate alone — no gap for clone to close"
else pass "after hydrate, checklist is unbound (the lock pins deps, not the app)"; fi
if build_runs "$g" "1. milk"; then bad "build worked after hydrate alone"
else pass "build fails after hydrate alone — the two-command gap #191 names"; fi

# --- 2. CLONE --from CLOSES IT: one command to a buildable store -------------------
head2 "2 — clone --from: one command, a buildable+verified store"
c=$(mk)
cout=$(OATH_STORE="$c" "$OATH" clone "$app" --lock "$lock" --from "$src" 2>&1); rc=$?
if [ "$rc" = 0 ]; then pass "clone --from exits 0"; else bad "clone --from failed ($rc): $cout"; fi
if grep -q '"checklist"' "$c/names.json" 2>/dev/null; then
  pass "checklist is now BOUND — clone admitted the app, not just its deps"
else bad "checklist unbound after clone"; fi
# clone VERIFIES (put --lock runs the properties): the transcript shows the verdict.
if printf '%s' "$cout" | grep -q 'checklist' && printf '%s' "$cout" | grep -qi 'tested\|proven\|passed'; then
  pass "clone VERIFIED the app (a guarantee verdict, not a bare copy)"
else bad "clone did not show a verification verdict: $cout"; fi
if build_runs "$c" "1. milk"; then pass "build + run works on the cloned store — the gap is closed in ONE command"
else bad "build/run failed after clone"; fi

# --- 3. CLONE --remote: the same, over a served registry ---------------------------
head2 "3 — clone --remote: the same claim over the wire"
start_server "$src" "$key" "$marker" || { echo "SETUP FAILED: could not start a local registry" >&2; exit 1; }
cr=$(mk)
if OATH_STORE="$cr" "$OATH" clone "$app" --lock "$lock" --remote "$SRV_URL" --key "$key" >/dev/null 2>&1; then
  pass "clone --remote exits 0"
else bad "clone --remote failed"; fi
if build_runs "$cr" "1. milk"; then pass "build + run works on the remote-cloned store"
else bad "build/run failed after clone --remote"; fi
kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null; SRV_PID=""

# --- 4. CLONE VERIFIES AGAINST THE LOCK: a mismatched lock is refused --------------
head2 "4 — clone still honours the lock: verifyLock runs on the app"
# The lock's CLOSURE is left intact (so hydrate fetches it cleanly); only its
# DEPENDENCY SET is made to mismatch the app, by pinning a name the source never
# references. hydrate therefore succeeds, and verifyLock — NOT the fetch — is what
# rejects. Altering a hash everywhere would fail during the fetch instead, and would
# still pass even if app-to-lock verification were removed entirely.
lh=$(sed -n '/dependencies/,/}/p' "$lock" | grep '"List"' | grep -oE '[0-9a-f]{64}')
badlock="$work/bad.lock"
awk -v h="$lh" '/"List": "/ && !d {print; print "    \"not-a-real-dep\": \"" h "\","; d=1; next} {print}' "$lock" > "$badlock"
b4=$(mk)
b4out=$(OATH_STORE="$b4" "$OATH" clone "$app" --lock "$badlock" --from "$src" 2>&1); rc=$?
if [ "$rc" = 0 ]; then
  bad "clone accepted a lock whose dependency set does not match the app"
elif printf '%s' "$b4out" | grep -qi 'lock does not match\|not referenced by the source'; then
  pass "clone rejects a lock mismatched to the app — verifyLock, not the fetch"
else bad "clone failed, but not at verifyLock (fetch?): $b4out"; fi
if grep -q '"checklist"' "$b4/names.json" 2>/dev/null; then
  bad "checklist was bound despite the mismatched lock"
else pass "  ...and did not admit the app"; fi

# --- 5. REFUSALS: no source, canonical corpus, and a non-empty target --------------
head2 "5 — refusals: no source, the canonical corpus, and a non-empty target"
n=$(mk)
if OATH_STORE="$n" "$OATH" clone "$app" --lock "$lock" >/dev/null 2>&1; then
  bad "clone ran with no object source"
else pass "clone refuses with no --from and no --remote"; fi
canon=$(cd "$root" && "$OATH" clone "$app" --lock "$lock" --from "$src" 2>&1); rc=$?
if [ "$rc" != 0 ] && printf '%s' "$canon" | grep -q 'canonical corpus'; then
  pass "clone refuses to write the canonical corpus"
else bad "clone did not refuse the canonical corpus (rc=$rc): $(printf '%s' "$canon" | head -1)"; fi
# clone MATERIALIZES a fresh store, so a non-empty target is refused — the property
# that keeps it a safe single operation (nothing to clobber, no policy to block).
ne=$(mk); ( cd "$root" && git archive HEAD:codebase ) | ( cd "$ne" && tar -x ) 2>/dev/null
if [ ! -s "$ne/names.json" ]; then bad "setup: the non-empty target is empty"
elif OATH_STORE="$ne" "$OATH" clone "$app" --lock "$lock" --from "$src" >/dev/null 2>&1; then
  bad "clone wrote into a non-empty store"
else pass "clone refuses a non-empty target (materializes a fresh store, or refuses)"; fi
# A source that declares nothing must not report a successful clone — the store would
# have no app to build. (A clean `put` exit is not proof the app landed.)
printf '; a comment-only source: no definitions\n' > "$work/empty.oath"
OATH_STORE="$src" "$OATH" resolve "$work/empty.oath" -o "$work/empty.lock" >/dev/null 2>&1
e5=$(mk)
if OATH_STORE="$e5" "$OATH" clone "$work/empty.oath" --lock "$work/empty.lock" --from "$src" >/dev/null 2>&1; then
  bad "clone reported success on a source that declares no definitions"
else pass "clone refuses a source with no definitions — nothing to build"; fi
# A policy-governed target is refused: clone admits through a scratch that does not
# carry the policy, so a governed store must go through put --lock, which enforces it.
pol=$(mk); printf '{"require_total": true}\n' > "$pol/policy.json"
if OATH_STORE="$pol" "$OATH" clone "$app" --lock "$lock" --from "$src" >/dev/null 2>&1; then
  bad "clone wrote into a policy-governed store, bypassing its policy"
else pass "clone refuses a policy-governed target (put --lock is what enforces policy)"; fi

# --- 6. TRANSACTIONAL: a multi-definition app that fails mid-way leaves nothing -----
head2 "6 — transactional: a mid-app failure leaves the target untouched"
# A two-definition app whose SECOND form is falsified. apiPut stores and repoints
# each definition as it goes, so a non-transactional clone would leave the deps and
# the first definition behind — and, because clone requires an empty target, block a
# retry in place. clone does the whole admission in a scratch and commits only if
# every definition is accepted, so this target must be EMPTY afterwards.
cat > "$work/badapp.oath" <<'OATHEOF'
(defn numbered [] [(n Int) (items (List Str))] Str
  (match items ((Nil) "") ((Cons h t) (str-append (show-nat n) (str-append ". " (str-append h (str-append "\n" (numbered (+ n 1) t)))))))
  (prop empty-is-empty [(n Int)] (== (numbered n (Nil [Str])) "")))
(defn checklist [] [(args (List Str))] Str
  (numbered 1 args)
  (prop wrong [(s Str)] (== (checklist (Cons [Str] s (Nil [Str]))) "DELIBERATELY WRONG")))
OATHEOF
OATH_STORE="$src" "$OATH" resolve "$work/badapp.oath" -o "$work/bad.lock" >/dev/null 2>&1
tb=$(mk)
if OATH_STORE="$tb" "$OATH" clone "$work/badapp.oath" --lock "$work/bad.lock" --from "$src" >/dev/null 2>&1; then
  bad "clone reported success on an app whose second definition is falsified"
else pass "clone fails on a falsified later definition"; fi
tbobjs=0; [ -d "$tb/objects" ] && tbobjs=$(ls -A "$tb/objects" 2>/dev/null | wc -l | tr -d ' ')
tbnamed=no; [ -s "$tb/names.json" ] && grep -q '"[0-9a-f]\{64\}"' "$tb/names.json" 2>/dev/null && tbnamed=yes
if [ "$tbobjs" = 0 ] && [ "$tbnamed" = no ]; then
  pass "  ...and the target is UNTOUCHED — no deps, no first definition, retryable in place"
else bad "  ...but the target was mutated (objects=$tbobjs, named=$tbnamed) — not transactional"; fi

# --- completeness guard ------------------------------------------------------------
expected_checks=17
head2 "RESULT"
if [ "$checks" -ne "$expected_checks" ]; then
  printf 'INCOMPLETE — ran %d checks, expected %d: an assertion block was skipped\n' "$checks" "$expected_checks" >&2
  exit 1
fi
if [ "$failures" -eq 0 ]; then
  printf 'PASS — %d/%d checks; clone collapses the fresh-machine loop into one verified command\n' "$checks" "$expected_checks"
  exit 0
else
  printf 'FAIL — %d of %d checks failed\n' "$failures" "$checks" >&2
  exit 1
fi
