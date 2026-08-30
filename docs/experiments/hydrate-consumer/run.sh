#!/bin/sh
# Falsifier for the COMPLETE publisher -> fresh-machine loop that `oath hydrate`
# exists to enable. hydrate-falsifier.sh (docs/experiments/resolve-consumer/)
# attacks the hydrate COMMAND; this attacks the LOOP the command was built for,
# which is a different claim and can fail while every hydrate check passes.
#
# THE CLAIM, stated as something that can fail:
#   a machine holding ONLY the app source, its lockfile, a registry URL and a
#   signing key — no corpus, no publisher store, no network state of its own —
#   reproduces the PUBLISHER'S FULL OBJECT IDENTITY, and builds a binary whose
#   output is byte-exact against the expected string asserted below and agrees
#   with the interpreter on the same store.
#
# Note what that does NOT say. The publisher here is never built and never run,
# so nothing below compares the consumer's OUTPUT against the publisher's. What
# is compared against the publisher is the object hash; the output is compared
# against a literal expectation this file states, and against `oath run`.
#
# The script does not narrate the happy path; it attacks the claim:
#   - the fresh machine genuinely starts with nothing (put --lock FAILS, build
#     FAILS, and the store is untouched afterwards) — else every later PASS is
#     vacuous, because a contaminated store would satisfy the lock already;
#   - hydrate over the WIRE lands exactly the pinned closure, no more and no
#     fewer, each addressed by its own hash and bound to the name the lock pins;
#   - hydrate is NOT the whole clone: the app itself is still absent afterwards,
#     witnessed by store state, so the two-command shape is recorded rather than
#     assumed;
#   - the consumer's `checklist` hashes to the SAME object as the publisher's —
#     identity reproduced, not merely "a program that works";
#   - the built artifact's own provenance names that hash, so the binary is
#     bound to the reproduced identity rather than to a coincidence;
#   - the binary's output is BYTE-exact against a literal expected string, and
#     agrees with `oath run` (the interpreter is the reference — a compiled-only
#     assertion cannot tell a faithful backend from two agreeing mistakes);
#   - the wire failure paths (a hash the registry cannot serve, a missing key)
#     leave the consumer store UNTOUCHED — witnessed by STORE STATE, never by an
#     error message;
#   - the repository's canonical corpus is untouched by the whole run.
#
# Read-only against the repo: the publisher corpus is a throwaway extraction of
# codebase@HEAD (never the worktree, so a dirty tree cannot be reported as "the
# committed corpus"), every store is a ledger-tracked `mktemp -d`, and the
# registry runs over the throwaway publisher store on a loopback port and is
# killed the moment the wire arm ends.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd)
here=$(cd "$(dirname "$0")" && pwd)
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
app="$here/checklist.oath"
[ -f "$app" ] || { echo "SETUP FAILED: missing $app" >&2; exit 1; }

# Run in a CLEAN Oath environment: everything that selects a store, a remote, a
# signing key, a namespace or the served registry's auth is cleared, so no
# ambient configuration alters a scenario. Concretely — an inherited
# OATH_REGISTRY would give the "fresh machine" a second object source and make
# the emptiness control lie; an exported OATH_STORE would aim every command
# somewhere other than the temp store named on its own line. Every command still
# sets OATH_STORE explicitly; this only removes the ambient.
unset OATH_STORE OATH_REGISTRY OATH_KEY OATH_KMS_KEY OATH_BACKEND OATH_AUTHOR \
      OATH_AUTHORIZED_KEYS OATH_HTTP_ADDR OATH_NAMESPACE OATH_HOME OATH_STDLIB \
      OATH_STORE_LOCK OATH_DB_DRIVER OATH_DB_DSN OATH_OBJECT_BUCKET 2>/dev/null || true

ledger=$(mktemp)
SRV_PID=""   # the ONE live `oath serve` pid, if the wire arm has one running
cleanup() {
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null   # only the currently-live server, never a stale pid
  while read -r d; do [ -n "$d" ] && rm -rf "$d"; done < "$ledger"
  rm -f "$ledger"
}
# cleanup runs once, on EXIT. INT/TERM must actually STOP the script (a POSIX shell
# otherwise runs the trap and then CONTINUES), so they exit with the signal status,
# which triggers the EXIT trap and its single cleanup.
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mk() { d=$(mktemp -d) || { echo "SETUP FAILED: mktemp" >&2; exit 1; }; printf '%s\n' "$d" >> "$ledger"; printf '%s' "$d"; }
work=$(mk)  # per-run scratch for the lock, the key and the built binary

# start_server <store> <keyfile> <marker> — launch `oath serve --http` over
# <store> and set SRV_URL once the server that is genuinely OURS is answering.
# Readiness needs POSITIVE evidence of ownership, not process liveness: serve
# prints its ready line BEFORE binding (oath/http.go), and during a child's
# startup a probe can be answered by a DIFFERENT Oath registry already on that
# port while our child is still alive and about to fail its bind. So <store>
# carries a per-run unique <marker> name and the probe is a signed `ls --remote`
# whose output must CONTAIN that marker — a foreign registry serving a different
# store cannot produce it. A busy port is then observed as "the probe never shows
# the marker, then our child dies", and the next port is tried.
# marker_probe <store> <url> <key> <marker> — a signed `ls --remote` that returns 0
# iff the registry answers AND its listing contains <marker> (proof it is OURS).
# BOUNDED to ~4s by a watchdog, because a port held by a service that accepts
# connections but never answers HTTP would otherwise hang on oath's 300s client
# timeout — turning a six-port retry into a half-hour stall. No `timeout(1)`
# dependency: this machine has neither timeout nor gtimeout.
marker_probe() {
  mp_out="$work/probe.out"; : > "$mp_out"
  OATH_STORE="$1" "$OATH" ls --remote "$2" --key "$3" >"$mp_out" 2>/dev/null &
  mp_pid=$!
  ( sleep 4; kill "$mp_pid" 2>/dev/null ) >/dev/null 2>&1 &
  mp_wd=$!
  wait "$mp_pid" 2>/dev/null
  kill "$mp_wd" 2>/dev/null; wait "$mp_wd" 2>/dev/null
  grep -q "$4" "$mp_out" 2>/dev/null
}

start_server() {
  pstore=$(mk)   # throwaway local store for the probe command (it reads the registry)
  attempt=0
  while [ "$attempt" -lt 6 ]; do
    attempt=$((attempt + 1))
    port=$(( 20000 + ( ($$ + attempt * 251) % 20000 ) ))
    url="http://127.0.0.1:$port"
    OATH_STORE="$1" "$OATH" serve --http "127.0.0.1:$port" >"$work/serve.$port.log" 2>&1 &
    p=$!
    SRV_PID="$p"   # track IMMEDIATELY: an interrupt during the readiness loop must still reap it
    tries=0
    while [ "$tries" -lt 40 ]; do
      tries=$((tries + 1))
      if marker_probe "$pstore" "$url" "$2" "$3"; then
        SRV_URL="$url"   # the marker proves this is OUR server, not a colliding one
        return 0         # SRV_PID already = p
      fi
      kill -0 "$p" 2>/dev/null || break   # our child died (bind failed) — try the next port
      sleep 0.25
    done
    kill "$p" 2>/dev/null   # reap this failed attempt NOW (still ours; not yet recycled)
    wait "$p" 2>/dev/null
    SRV_PID=""              # cleared so cleanup never signals this now-dead (recyclable) pid
  done
  return 1
}

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
head2() { printf '\n%s\n' "$1"; }

# store_untouched <dir> <label> — a store a FAILED command must not have written
# to: no object, no bound name, no journal entry. Absence of output is not
# absence of state, so this reads the STORE, not the transcript. (An empty
# objects/ and meta/ directory is created by the attempt itself and is not
# state: nothing is stored, named or journalled.)
store_untouched() {
  d=$1; l=$2
  objs=0; [ -d "$d/objects" ] && objs=$(ls -A "$d/objects" 2>/dev/null | wc -l | tr -d ' ')
  metas=0; [ -d "$d/meta" ] && metas=$(ls -A "$d/meta" 2>/dev/null | wc -l | tr -d ' ')
  named=no; [ -f "$d/names.json" ] && grep -q '"[0-9a-f]\{64\}"' "$d/names.json" 2>/dev/null && named=yes
  journal=no; [ -s "$d/log.jsonl" ] && journal=yes
  if [ "$objs" = 0 ] && [ "$metas" = 0 ] && [ "$named" = no ] && [ "$journal" = no ]; then pass "$l"
  else bad "$l (objects=$objs, meta=$metas, named=$named, journal=$journal)"; fi
}

# lock field extractors — "closure" is bare hash strings, "dependencies" is
# name->hash. Both are read from the lock this run generated, never hard-coded.
closure_hashes() { sed -n '/"closure"/,/]/p' "$1" | grep -oE '[0-9a-f]{64}'; }
dep_lines()      { sed -n '/"dependencies"/,/}/p' "$1" | grep -oE '"[a-zA-Z0-9_-]+": "[0-9a-f]{64}"'; }

# ---------------------------------------------------------------------------------
# SETUP — THE PUBLISHER. A corpus store from codebase@HEAD, the app put into it
# (what a publisher does before shipping), and the lock resolve writes from it.
head2 "SETUP — the publisher: corpus@HEAD, the app, the lock"
corpus_before=$(cd "$root" && git status --porcelain codebase/ 2>/dev/null)
src=$(mk)
( cd "$root" && git archive HEAD:codebase ) | ( cd "$src" && tar -x ) || { echo "SETUP FAILED: git archive" >&2; exit 1; }
pub_out=$(OATH_STORE="$src" "$OATH" put "$app" --new 2>&1) || { echo "SETUP FAILED: publisher put: $pub_out" >&2; exit 1; }
pub_hash=$(grep '"checklist"' "$src/names.json" 2>/dev/null | grep -oE '[0-9a-f]{64}' | head -1)
[ -n "$pub_hash" ] || { echo "SETUP FAILED: publisher store bound no checklist name: $pub_out" >&2; exit 1; }
lock="$work/checklist.lock"
OATH_STORE="$src" "$OATH" resolve "$app" -o "$lock" >/dev/null 2>&1 || { echo "SETUP FAILED: resolve" >&2; exit 1; }
n_closure=$(closure_hashes "$lock" | wc -l | tr -d ' ')
n_deps=$(dep_lines "$lock" | wc -l | tr -d ' ')
printf '  publisher: checklist %s; lock pins %s direct name(s) across a %s-object closure\n' \
       "$pub_hash" "$n_deps" "$n_closure"
[ "$n_closure" -ge 2 ] && [ "$n_deps" -ge 2 ] || { echo "SETUP FAILED: degenerate lock" >&2; exit 1; }

# The registry the fresh machine will read from. The marker is a per-run unique
# name put into the SERVED store so the readiness probe has positive evidence it
# reached OUR server; it is not in the lock's closure, so it cannot alter the
# hydrate under test.
"$OATH" keygen --out "$work/k" >/dev/null 2>&1 || { echo "SETUP FAILED: keygen" >&2; exit 1; }
key="$work/k.key"
marker="HydrateLoop$$X$(awk 'BEGIN{srand();printf "%06d", int(rand()*1000000)}')"
printf '(data %s [] (Mk%s))\n' "$marker" "$marker" > "$work/marker.oath"
OATH_STORE="$src" "$OATH" put "$work/marker.oath" --new >/dev/null 2>&1 || { echo "SETUP FAILED: marker put" >&2; exit 1; }
start_server "$src" "$key" "$marker" || { echo "SETUP FAILED: could not start a local registry" >&2; exit 1; }
printf '  registry: %s (pid %s), ownership proven by the %s marker\n' "$SRV_URL" "$SRV_PID" "$marker"

# ---------------------------------------------------------------------------------
head2 "1 — THE FRESH MACHINE HAS NOTHING (the control that makes everything below mean something)"
# If either of these ever succeeded, the consumer store would already satisfy the
# lock and every later PASS would be vacuous.
tgt=$(mk)
if OATH_STORE="$tgt" "$OATH" put --lock "$lock" "$app" --new >/dev/null 2>&1; then
  bad "put --lock SUCCEEDED against the empty consumer store — the premise is dead, later checks are vacuous"
else
  pass "put --lock fails against the empty consumer store (nothing to resolve the app's deps against)"
fi
store_untouched "$tgt" "  ...and the failed put --lock left the store untouched"
if OATH_STORE="$tgt" "$OATH" build checklist -o "$work/nope" >/dev/null 2>&1 || [ -e "$work/nope" ]; then
  bad "build produced something on a machine that holds no definitions"
else
  pass "build checklist fails there too, and emits no artifact — the name is genuinely unbound"
fi

# ---------------------------------------------------------------------------------
head2 "2 — HYDRATE OVER THE WIRE: the lock's closure arrives from the registry"
hout=$(OATH_STORE="$tgt" "$OATH" hydrate "$lock" --remote "$SRV_URL" --key "$key" 2>&1); rc=$?
if [ "$rc" = 0 ]; then pass "hydrate --remote exits 0 — $hout"
else bad "hydrate --remote failed ($rc): $hout"; fi

# EXACTLY the closure — not ">=", which a stray write would pass.
objs=0; [ -d "$tgt/objects" ] && objs=$(ls -A "$tgt/objects" 2>/dev/null | wc -l | tr -d ' ')
if [ "$objs" = "$n_closure" ]; then pass "the consumer store holds exactly the $n_closure closure objects"
else bad "consumer holds $objs objects, the lock's closure is $n_closure"; fi

missing=0
for h in $(closure_hashes "$lock"); do [ -f "$tgt/objects/$h.bin" ] || missing=$((missing + 1)); done
if [ "$missing" = 0 ]; then pass "every closure hash is present, addressed by its own hash"
else bad "$missing closure hash(es) absent after hydrate"; fi

wrongbind=0
for pair in $(dep_lines "$lock" | tr -d ' '); do
  if ! grep -q "$pair" "$tgt/names.json" 2>/dev/null; then
    name=$(printf '%s' "$pair" | sed 's/^"\([^"]*\)".*/\1/')
    hash=$(printf '%s' "$pair" | grep -oE '[0-9a-f]{64}')
    grep -q "\"$name\": \"$hash\"" "$tgt/names.json" 2>/dev/null || wrongbind=$((wrongbind + 1))
  fi
done
if [ "$wrongbind" = 0 ]; then pass "every direct name is bound to the hash the lock pinned"
else bad "$wrongbind direct name(s) bound to a hash other than the lock's"; fi

# ---------------------------------------------------------------------------------
head2 "3 — WIRE FAILURE PATHS: witnessed by store state, never by the message"
# A dead registry ALSO makes hydrate fail and leaves the store untouched, so the
# missing-hash check below would false-green if the server had exited. Re-prove the
# registry is up AND ours (the marker) first; a down server is a SETUP failure here,
# not a silent pass.
if ! marker_probe "$(mk)" "$SRV_URL" "$key" "$marker"; then
  echo "SETUP FAILED: the registry stopped answering before the missing-hash test — that check would false-green" >&2
  exit 1
fi
# A hash the registry cannot serve. One hex digit of one closure hash is flipped,
# so it addresses no object anywhere; the fetch must fail WHOLE.
victim=$(closure_hashes "$lock" | sed -n '1p')
flipped=$(printf '%s' "$victim" | sed 's/.$//')$(printf '%s' "$victim" | sed 's/.*\(.\)$/\1/' | tr '0123456789abcdef' '123456789abcdef0')
tamper_lock="$work/tamper.lock"
sed "s/$victim/$flipped/" "$lock" > "$tamper_lock"
t_tamper=$(mk)
# Require a MISSING-OBJECT response from the live registry, not merely a nonzero
# exit: a transport failure (registry died in the window between the probe above and
# now) would also fail and leave the store untouched, and must not be credited here.
tout=$(OATH_STORE="$t_tamper" "$OATH" hydrate "$tamper_lock" --remote "$SRV_URL" --key "$key" 2>&1); rc=$?
if [ "$rc" = 0 ]; then
  bad "hydrate --remote accepted a hash the registry cannot serve"
elif printf '%s' "$tout" | grep -qi 'no object\|no definition'; then
  pass "hydrate --remote fails on a hash the registry cannot serve (a missing-object response, not transport)"
else
  bad "hydrate --remote failed, but not with a missing-object response (transport?): $tout"
fi
store_untouched "$t_tamper" "  ...and left that consumer store untouched — no half-populated clone"

t_nokey=$(mk)
# Require the refusal to be about the KEY, not merely a nonzero exit — a transport
# failure would also exit nonzero and would be the wrong reason. (This path refuses
# CLIENT-SIDE before contacting the registry, so it is transport-independent, but the
# check should PROVE that rather than trust it.)
nokey_out=$(OATH_STORE="$t_nokey" "$OATH" hydrate "$lock" --remote "$SRV_URL" 2>&1); rc=$?
if [ "$rc" = 0 ]; then
  bad "hydrate --remote ran with no signing key"
elif printf '%s' "$nokey_out" | grep -qi 'signing key\|pass --key'; then
  pass "hydrate --remote refuses without a signing key (every remote read is signed)"
else
  bad "hydrate --remote failed without --key, but not for the key: $nokey_out"
fi
store_untouched "$t_nokey" "  ...and wrote nothing"

# The wire arm is over. Stop the server NOW, while the pid is still ours, so the
# only kill happens with no reuse window; cleanup is the backstop if we exit
# earlier. Everything below runs on the consumer store alone — which is the
# point: a fresh machine finishes the clone offline.
kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null; SRV_PID=""

# ---------------------------------------------------------------------------------
head2 "4 — HYDRATE IS NOT THE WHOLE CLONE: the app itself is still absent"
# Recorded as state rather than assumed: the lock pins the app's DEPENDENCIES,
# so after hydrate the consumer can resolve them and still has no `checklist`.
# This is what makes the clone two commands rather than one.
if grep -q '"checklist"' "$tgt/names.json" 2>/dev/null; then
  bad "checklist is bound after hydrate alone — the lock apparently carries the app itself"
else pass "checklist is NOT bound after hydrate: the lock pins dependencies, not the app"; fi
if OATH_STORE="$tgt" "$OATH" build checklist -o "$work/nope2" >/dev/null 2>&1 || [ -e "$work/nope2" ]; then
  bad "build succeeded after hydrate alone, with no put --lock"
else pass "build still fails after hydrate alone — put --lock is a required second step"; fi

# ---------------------------------------------------------------------------------
head2 "5 — put --lock: the same command that failed in check 1"
put_out=$(OATH_STORE="$tgt" "$OATH" put --lock "$lock" "$app" --new 2>&1); rc=$?
if [ "$rc" = 0 ]; then pass "put --lock now SUCCEEDS — same command, same lock, a hydrated store"
else bad "put --lock still fails after hydrate ($rc): $(printf '%s' "$put_out" | head -1)"; fi
if grep -q '"checklist"' "$tgt/names.json" 2>/dev/null; then
  pass "checklist is now bound in the consumer store"
else bad "put --lock reported success but bound no checklist name"; fi

# THE REPRODUCTION CLAIM: the fresh machine's object is the PUBLISHER'S object.
# Content addressing makes this an identity check on the elaborated definition,
# not a claim that two programs merely behave alike.
# Both sides are read from names.json, so this compares the FULL 64-hex object
# identity; the CLI's 12-hex display prefix would be a weaker equality.
con_hash=$(grep '"checklist"' "$tgt/names.json" 2>/dev/null | grep -oE '[0-9a-f]{64}' | head -1)
if [ -n "$con_hash" ] && [ "$con_hash" = "$pub_hash" ]; then
  pass "the consumer's checklist hashes to $con_hash — the publisher's identity, reproduced"
else bad "identity NOT reproduced: publisher ${pub_hash:-<none>}, consumer ${con_hash:-<none>}"; fi

# ---------------------------------------------------------------------------------
head2 "6 — BUILD: a native binary on the fresh machine"
bin="$work/checklist"
build_out=$(OATH_STORE="$tgt" "$OATH" build checklist -o "$bin" 2>&1); rc=$?
if [ "$rc" = 0 ] && [ -x "$bin" ]; then pass "build exits 0 and produced an executable"
else bad "build failed (rc=$rc, executable=$([ -x "$bin" ] && echo yes || echo no)): $(printf '%s' "$build_out" | head -1)"; fi

# Bind the ARTIFACT to the reproduced identity: provenance is read from the
# binary without running it, so this is the artifact's own claim about what it
# was built from, checked against the hash put --lock reported.
prov=$("$OATH" provenance "$bin" 2>&1)
if printf '%s' "$prov" | grep -q "\"entry_hash\": \"$con_hash\""; then
  pass "the binary's own provenance names entry_hash $con_hash"
else bad "provenance does not name the reproduced hash: $(printf '%s' "$prov" | grep entry_hash | head -1)"; fi

# ---------------------------------------------------------------------------------
head2 "7 — RUN: byte-exact output, and the interpreter agrees"
# Fixed arguments and a literal expected string — an expectation asserted here,
# not a transcript of the publisher, who is never run. Compared with cmp, so a
# stray byte — a lost or doubled newline, a trailing space — fails; capturing
# into a shell variable would strip exactly the trailing newlines this entry
# protocol emits, which is the difference an assertion here is for.
expected="$work/expected.txt"
printf '1. milk\n2. eggs\n3. bread\n\n' > "$expected"
got="$work/got.txt"
"$bin" milk eggs bread > "$got" 2>"$work/run.err"; rc=$?
if [ "$rc" = 0 ]; then pass "the built binary exits 0 on: checklist milk eggs bread"
else bad "the binary exited $rc: $(head -1 "$work/run.err")"; fi
if cmp -s "$got" "$expected"; then
  pass "its output is byte-exact: $(od -c "$expected" | head -2 | tr -s ' ' | tr '\n' ' ')"
else bad "output differs from the expected numbered checklist: $(od -c "$got" | head -2 | tr -s ' ' | tr '\n' ' ')"; fi

# `oath eval`/`oath run` is the reference (CLAUDE.md: never judge a backend
# against itself). A compiled-only assertion cannot distinguish a faithful
# backend from a backend and an expectation that are wrong the same way.
interp="$work/interp.txt"
# Require the reference interpreter to EXIT 0 as well as match: it is the gate the
# backend is judged against, so a run that printed the right bytes and then failed
# must not be credited as agreement.
OATH_STORE="$tgt" "$OATH" run checklist -- milk eggs bread > "$interp" 2>"$work/interp.err"; irc=$?
if [ "$irc" != 0 ]; then bad "the reference interpreter exited $irc: $(head -1 "$work/interp.err")"
elif cmp -s "$interp" "$got"; then pass "the interpreter, on the same store, produces the same bytes"
else bad "compiled and interpreted output differ"; fi

# ---------------------------------------------------------------------------------
head2 "8 — THE REPOSITORY IS UNTOUCHED"
# The whole run is temp stores; this witnesses it against git rather than
# asserting it in a comment. Compared to the SETUP snapshot, so a tree that was
# already dirty is reported honestly instead of failing this run.
corpus_after=$(cd "$root" && git status --porcelain codebase/ 2>/dev/null)
if [ "$corpus_after" = "$corpus_before" ]; then pass "codebase/ is exactly as this run found it"
else bad "codebase/ changed during the run: $(printf '%s' "$corpus_after" | head -3)"; fi

# ---------------------------------------------------------------------------------
# Completeness guard: a skipped block must not exit green with fewer checks. Bump
# this deliberately when adding an assertion (negative-control it by editing down).
expected_checks=22
head2 "RESULT"
if [ "$checks" -ne "$expected_checks" ]; then
  printf 'INCOMPLETE — ran %d checks, expected %d: an assertion block was skipped or double-counted\n' "$checks" "$expected_checks" >&2
  exit 1
fi
if [ "$failures" -eq 0 ]; then
  printf 'PASS — %d/%d checks; the fresh-machine loop survived every attack\n' "$checks" "$expected_checks"
  exit 0
else
  printf 'FAIL — %d of %d checks failed\n' "$failures" "$checks" >&2
  exit 1
fi
