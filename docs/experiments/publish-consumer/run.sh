#!/bin/sh
# Falsifier for the PUBLISH side of the flywheel: an author with a key and a URL
# turns source into a governed, namespaced, signed library that a stranger with
# neither can consume — and hands a machine the right to publish into it without
# handing over the namespace.
#
# hydrate-consumer/run.sh attacks the CONSUMER loop over a registry someone else
# already populated. This attacks the half before it — reservation, delegation,
# signed publication under a namespace, and the authority that is supposed to keep
# the names there. Those can fail while every consumer check passes, because a
# consumer never asks who was allowed to write.
#
# THE CLAIM, stated as something that can fail:
#   starting from NOTHING — an empty registry store, an empty publisher store,
#   three freshly generated keys — an author reserves michael/*, publishes a
#   multi-definition library under it, DELEGATES publication to a separate
#   automation key, and that key publishes the dependent app from a store of its
#   own; the registry then binds exactly those names to exactly the objects the
#   sources produce; a consumer with no key at all resolves, clones, builds and
#   runs the app from the URL alone, reproducing the published object's identity;
#   the delegate can publish and NOTHING else; a third key can neither take the
#   namespace nor move a name inside it; and a revocation is enforced BY THE
#   REGISTRY on the very next publication. Every refusal is judged by registry
#   state, never by an error message.
#
# THREE KEYS, three roles, and the run is only meaningful because they are
# distinct:
#   holder      reserves michael/*, publishes the foundation and the library,
#               grants and revokes.
#   automation  a delegate. Publishes the dependent app; may do nothing else.
#   stranger    holds nothing, and never becomes a delegate.
#
# What it does NOT claim. Nothing here is proven (`oath prove` is not run); the
# guarantee on every definition is `tested`. Nothing is published to the live
# registry, and the committed corpus is never read except to compare two hashes.
# And the delegate's three refusals in section 7 are CLIENT-SIDE: the tool reads
# the registry's authority state and declines to sign. What they witness is that
# no such statement reaches the registry and the governance state does not move —
# NOT that the server would refuse one. Section 11 is the server-side
# adjudication: after revocation the registry itself blocks the ex-delegate.
#
# Read-only against the repo: every store is a ledger-tracked `mktemp -d`, the
# registry runs on a loopback port and is killed the moment its arm ends, and
# codebase/ is only ever read (two hashes) and is diffed at the end.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd)
here=$(cd "$(dirname "$0")" && pwd)
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
for f in base-list.oath base-str.oath item-report-library.oath item-report-app.oath unauthorized-text-join.oath; do
  [ -f "$here/$f" ] || { echo "SETUP FAILED: missing $here/$f" >&2; exit 1; }
done
# The anonymous-write check needs a client that will send an UNSIGNED request.
# Every `oath` write path refuses client-side without a key, which tests the CLI
# rather than the registry, so the raw call is made with curl. Absence is a SETUP
# failure, not a silently skipped check.
command -v curl >/dev/null 2>&1 || { echo "SETUP FAILED: curl is required for the anonymous-write check" >&2; exit 1; }

# Run in a CLEAN Oath environment: anything that could select a store, a remote, a
# key, a namespace or the served registry's auth is cleared, so no ambient
# configuration alters a scenario. An inherited OATH_REGISTRY would give the
# "empty registry" control a second source; an exported OATH_STORE would aim
# every command somewhere other than the temp store named on its own line; an
# ambient OATH_KEY would silently sign a "delegate" attempt as the holder.
# Every command still sets OATH_STORE explicitly.
unset OATH_STORE OATH_REGISTRY OATH_KEY OATH_KMS_KEY OATH_BACKEND OATH_AUTHOR \
      OATH_AUTHORIZED_KEYS OATH_HTTP_ADDR OATH_NAMESPACE OATH_STDLIB \
      OATH_STORE_LOCK OATH_DB_DRIVER OATH_DB_DSN OATH_OBJECT_BUCKET OATH_PUBLIC_READS 2>/dev/null || true
# OATH_HOME is POINTED at an empty dir, not unset: unsetting it makes oath fall back
# to the REAL ~/.oath/config (config.go), and a user who ran `oath new` has a
# configured namespace and key there — `publish` without --namespace would then
# qualify the "bare List/Str" foundation, and a configured key would sign a probe
# meant to be anonymous. An empty home neutralises both.
ledger=$(mktemp)
OATH_HOME=$(mktemp -d); export OATH_HOME; printf '%s\n' "$OATH_HOME" >> "$ledger"
SRV_PID=""
# NOSTORE is where the commands that need no store are pointed. `oath` opens a
# store BEFORE dispatch and `storeDir` defaults to `codebase`, so a bare `oath
# keygen` or `oath provenance` creates ./codebase in whatever directory the
# script was invoked from — and invoked from the repository root, that is the
# canonical corpus. Nothing is written there, which is exactly why the "repository
# untouched" check at the end does not catch it: an opened store is a created
# directory, not a tracked change. Every invocation below therefore names a store.
NOSTORE=""
cleanup() {
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null
  while read -r d; do [ -n "$d" ] && rm -rf "$d"; done < "$ledger"
  rm -f "$ledger"
}
# cleanup runs once, on EXIT. INT/TERM must actually STOP the script (a POSIX shell
# otherwise runs the trap and then CONTINUES), so they exit with the signal status,
# which triggers the EXIT trap and its single cleanup.
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# On mktemp failure, SIGNAL THE PARENT ($$ is the parent's PID even here in a
# command-substitution subshell) rather than `exit 1`, which would only leave the
# subshell — the caller `x=$(mk)` would then get an empty path, and an empty
# OATH_STORE falls back to the canonical `codebase`. The parent's TERM trap fires
# between commands, so it aborts before the empty value is ever used.
mk() { d=$(mktemp -d) || { echo "SETUP FAILED: mktemp" >&2; kill "$$"; exit 1; }; printf '%s\n' "$d" >> "$ledger"; printf '%s' "$d"; }
work=$(mk)
NOSTORE=$(mk)

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
head2() { printf '\n%s\n' "$1"; }

# bound <store> <name> — the FULL 64-hex object a name is bound to, or empty.
# Read from names.json rather than from CLI output, because the CLI displays a
# 12-hex prefix and a prefix comparison is a weaker equality than identity.
bound() { grep "\"$2\": " "$1/names.json" 2>/dev/null | grep -oE '[0-9a-f]{64}' | head -1; }
name_count() { grep -cE '": "[0-9a-f]{64}"' "$1/names.json" 2>/dev/null || echo 0; }
obj_count() { ls -A "$1/objects" 2>/dev/null | wc -l | tr -d ' '; }

# governance <store> <keyfile> — the registry's OWN report of who governs
# michael/*: holder, delegates, and both revisions. This is the state a
# reservation or a grant would be evaluated against, so every governance
# assertion below reads it rather than inferring from a store file. The view
# banner is dropped: it names the URL, which is per-run noise.
governance() { OATH_STORE="$1" "$OATH" authority 'michael/*' --remote "$SRV_URL" --key "$2" 2>&1 | grep -v '  view:'; }

# store_untouched <dir> <label> — a store a FAILED command must not have written
# to: no object, no bound name, no journal entry. Absence of output is not absence
# of state, so this reads the STORE, not the transcript.
store_untouched() {
  d=$1; l=$2
  objs=$(obj_count "$d")
  metas=0; [ -d "$d/meta" ] && metas=$(ls -A "$d/meta" 2>/dev/null | wc -l | tr -d ' ')
  named=no; [ -f "$d/names.json" ] && grep -q '"[0-9a-f]\{64\}"' "$d/names.json" 2>/dev/null && named=yes
  journal=no; [ -s "$d/log.jsonl" ] && journal=yes
  if [ "$objs" = 0 ] && [ "$metas" = 0 ] && [ "$named" = no ] && [ "$journal" = no ]; then pass "$l"
  else bad "$l (objects=$objs, meta=$metas, named=$named, journal=$journal)"; fi
}

# lock field extractors, read from the lock this run generated.
dep_names() { sed -n '/"dependencies"/,/}/p' "$1" | grep -oE '"[a-zA-Z0-9_/-]+": "[0-9a-f]{64}"' | sed 's/": .*//;s/^"//'; }
dep_hash()  { sed -n '/"dependencies"/,/}/p' "$1" | grep "\"$2\": " | grep -oE '[0-9a-f]{64}' | head -1; }

# marker_probe <store> <url> <marker> — an ANONYMOUS `ls --remote` that returns 0
# iff the registry answers AND its listing contains <marker>. Readiness needs
# POSITIVE evidence of ownership, not process liveness: `serve` prints its ready
# line before binding, so during our child's startup a probe can be answered by a
# DIFFERENT Oath registry already holding that port. The marker is a per-run unique
# name that only OUR served store carries. Bounded by a watchdog, because a port
# held by something that accepts connections and never answers would otherwise hang
# on oath's 300s client timeout; no timeout(1) dependency (this machine has none).
marker_probe() {
  mp_out="$work/probe.out"; : > "$mp_out"
  OATH_STORE="$1" "$OATH" ls --remote "$2" >"$mp_out" 2>/dev/null &
  mp_pid=$!
  ( sleep 4; kill "$mp_pid" 2>/dev/null ) >/dev/null 2>&1 &
  mp_wd=$!
  wait "$mp_pid" 2>/dev/null
  kill "$mp_wd" 2>/dev/null; wait "$mp_wd" 2>/dev/null
  grep -q "$3" "$mp_out" 2>/dev/null
}

# start_server <store> <marker> — `oath serve --http … --public-reads` over <store>.
start_server() {
  pstore=$(mk)
  attempt=0
  while [ "$attempt" -lt 6 ]; do
    attempt=$((attempt + 1))
    port=$(( 20000 + ( ($$ + attempt * 251) % 20000 ) ))
    url="http://127.0.0.1:$port"
    OATH_STORE="$1" "$OATH" serve --http "127.0.0.1:$port" --public-reads >"$work/serve.$port.log" 2>&1 &
    p=$!
    SRV_PID="$p"   # track IMMEDIATELY: an interrupt during the readiness loop must still reap it
    tries=0
    while [ "$tries" -lt 40 ]; do
      tries=$((tries + 1))
      if marker_probe "$pstore" "$url" "$2"; then SRV_URL="$url"; return 0; fi
      kill -0 "$p" 2>/dev/null || break   # our child died (bind failed) — next port
      sleep 0.25
    done
    kill "$p" 2>/dev/null
    wait "$p" 2>/dev/null
    SRV_PID=""
  done
  return 1
}

# ---------------------------------------------------------------------------------
head2 "SETUP — an empty registry, an empty publisher, three fresh keys"
corpus_before=$(cd "$root" && git status --porcelain codebase/ 2>/dev/null)

reg=$(mk)   # the registry's store: everything in it will arrive by publication
pub=$(mk)   # the holder's local store

for k in holder automation stranger; do
  OATH_STORE="$NOSTORE" "$OATH" keygen --out "$work/$k" >/dev/null 2>&1 || { echo "SETUP FAILED: keygen $k" >&2; exit 1; }
done
holderkey="$work/holder.key"; autokey="$work/automation.key"; strangerkey="$work/stranger.key"
hold_hex=$(grep -oE '[0-9a-f]{64}' "$work/holder.pub" 2>/dev/null | head -1)
auto_hex=$(grep -oE '[0-9a-f]{64}' "$work/automation.pub" 2>/dev/null | head -1)
str_hex=$(grep -oE '[0-9a-f]{64}' "$work/stranger.pub" 2>/dev/null | head -1)
[ -n "$hold_hex" ] && [ -n "$auto_hex" ] && [ -n "$str_hex" ] || { echo "SETUP FAILED: could not read the generated public keys" >&2; exit 1; }
# Three DISTINCT keys, or half the run is vacuous: a delegate that is secretly the
# holder proves nothing about delegation, and a stranger that is secretly the
# delegate proves nothing about refusal.
[ "$hold_hex" != "$auto_hex" ] && [ "$hold_hex" != "$str_hex" ] && [ "$auto_hex" != "$str_hex" ] ||
  { echo "SETUP FAILED: the three keys are not distinct" >&2; exit 1; }

# The readiness marker. It is put into the registry's store LOCALLY, before the
# server starts, because the readiness probe has to exist before anything can be
# published through the server it is proving. It is a datatype with no
# dependencies and a per-run unique name, so it cannot collide with michael/* and
# cannot participate in anything published below — but it IS a bound name, so the
# exact-state assertion counts it explicitly rather than pretending the registry
# holds only publications.
marker="PublishLoop$$X$(awk 'BEGIN{srand();printf "%06d", int(rand()*1000000)}')"
printf '(data %s [] (Mk%s))\n' "$marker" "$marker" > "$work/marker.oath"
OATH_STORE="$reg" "$OATH" put "$work/marker.oath" --new >/dev/null 2>&1 || { echo "SETUP FAILED: marker put" >&2; exit 1; }
marker_hash=$(bound "$reg" "$marker")
[ -n "$marker_hash" ] || { echo "SETUP FAILED: the marker did not bind in the registry store" >&2; exit 1; }
start_server "$reg" "$marker" || { echo "SETUP FAILED: could not start a local registry" >&2; exit 1; }
printf '  registry: %s (pid %s), ownership proven by the %s marker, --public-reads\n' "$SRV_URL" "$SRV_PID" "$marker"
printf '  holder %s…  automation %s…  stranger %s…\n' \
  "$(printf '%s' "$hold_hex" | cut -c1-12)" "$(printf '%s' "$auto_hex" | cut -c1-12)" "$(printf '%s' "$str_hex" | cut -c1-12)"

# The ORACLE store: the same sources, put BARE into a store of their own. Every
# hash this run expects on the registry is read from here, so nothing is
# hard-coded and the comparison is against what the author's own source produces
# rather than against a number someone typed.
oracle=$(mk)
OATH_STORE="$oracle" "$OATH" put "$here/base-list.oath" --new >/dev/null 2>&1 &&
OATH_STORE="$oracle" "$OATH" put "$here/base-str.oath" --new >/dev/null 2>&1 &&
OATH_STORE="$oracle" "$OATH" put "$here/item-report-library.oath" --new >/dev/null 2>&1 ||
  { echo "SETUP FAILED: the oracle store could not elaborate the sources" >&2; exit 1; }

# ---------------------------------------------------------------------------------
head2 "1 — THE REGISTRY STARTS WITH NOTHING (the control that makes every state assertion below a CHANGE)"
if [ "$(name_count "$reg")" = 1 ] && [ -n "$(bound "$reg" "$marker")" ]; then
  pass "the registry binds exactly one name, the readiness marker — no List, no Str, no michael/*"
else
  bad "the registry already binds $(name_count "$reg") name(s) before anything was published"
fi
empty_lock="$work/empty.lock"
r_out=$(OATH_STORE="$(mk)" "$OATH" resolve "$here/item-report-app.oath" --remote "$SRV_URL" -o "$empty_lock" 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "the app resolved against a registry that holds none of its dependencies"
else pass "resolving the app against that registry fails — its dependencies do not exist yet"; fi
if [ -e "$empty_lock" ]; then bad "a lock was written by a resolve that failed"
else pass "  ...and no lockfile was written"; fi

# ---------------------------------------------------------------------------------
head2 "2 — RESERVE michael/*: authority before any name exists"
a_before=$(governance "$pub" "$holderkey")
if printf '%s' "$a_before" | grep -q 'UNCLAIMED'; then
  pass "michael/* is UNCLAIMED, read from the registry the reservation will be evaluated against"
else bad "authority did not report michael/* unclaimed: $(printf '%s' "$a_before" | head -1)"; fi

res_out=$(OATH_STORE="$pub" "$OATH" reserve 'michael/*' --remote "$SRV_URL" --key "$holderkey" -y 2>&1); rc=$?
if [ "$rc" = 0 ] && printf '%s' "$res_out" | grep -q 'RESERVED michael/\*'; then
  pass "reserve michael/* succeeds, signed by the holder key"
else bad "reserve failed ($rc): $(printf '%s' "$res_out" | tail -2 | tr '\n' ' ')"; fi

a_after=$(governance "$pub" "$holderkey")
if printf '%s' "$a_after" | grep -q "HELD by $hold_hex" && printf '%s' "$a_after" | grep -q 'authority revision 1'; then
  pass "michael/* is now HELD by the holder key at authority revision 1"
else bad "authority after reserve is not the holder at revision 1: $(printf '%s' "$a_after" | head -1)"; fi

# ---------------------------------------------------------------------------------
head2 "3 — PUBLISH THE FOUNDATION: List and Str, under BARE names"
# Bare, and deliberately so — but for ONE narrow reason. String literals elaborate
# against any Str-shaped datatype (michael/Str included — friction item 4), and the
# entry shape's List is matched STRUCTURALLY, so michael/List already satisfies it.
# The single thing that needs a BARE name is CLI ENTRY recognition, which resolves
# the string type by the literal name `Str` (item 4). Publishing the foundation bare
# is what lets a consumer build the app's (-> (List Str) Str) entry. Each is its own
# file because a signed publication covers exactly one name transition.
OATH_STORE="$pub" "$OATH" put "$here/base-list.oath" --new >/dev/null 2>&1
OATH_STORE="$pub" "$OATH" put "$here/base-str.oath" --new >/dev/null 2>&1
ok=0
for f in base-list base-str; do
  OATH_STORE="$pub" "$OATH" publish --remote "$SRV_URL" --key "$holderkey" -y "$here/$f.oath" >"$work/pub.$f.out" 2>&1 || ok=1
done
reg_list=$(bound "$reg" "List"); reg_str=$(bound "$reg" "Str")
if [ "$ok" = 0 ] && [ "$reg_list" = "$(bound "$oracle" "List")" ] && [ "$reg_str" = "$(bound "$oracle" "Str")" ]; then
  pass "both foundation datatypes publish, and the registry binds them to the objects the source produces"
else bad "foundation publication did not bind List/Str as the source produces them (rc=$ok)"; fi

# Identity is structural, so a datatype declared from scratch in an empty store is
# the SAME OBJECT as the committed corpus's, with no shared provenance at all.
# codebase/ is only READ here.
cb_list=$(bound "$root/codebase" "List"); cb_str=$(bound "$root/codebase" "Str")
if [ -n "$cb_list" ] && [ "$reg_list" = "$cb_list" ] && [ "$reg_str" = "$cb_str" ]; then
  pass "they are byte-identical to the committed corpus's List and Str — identity is structural, not inherited"
else bad "the published foundation differs from the corpus: List $reg_list vs $cb_list"; fi

# ---------------------------------------------------------------------------------
head2 "4 — PUBLISH THE LIBRARY under michael/*: four names, four signatures"
lib_out="$work/publish-library.out"
OATH_STORE="$pub" "$OATH" publish --remote "$SRV_URL" --key "$holderkey" --namespace michael -y \
  "$here/item-report-library.oath" >"$lib_out" 2>&1; rc=$?
if [ "$rc" = 0 ] && grep -q 'all 4 definitions published under michael' "$lib_out"; then
  pass "the batch publishes as 4 separate signed envelopes, in dependency order"
else bad "the library publication failed ($rc): $(grep -i error "$lib_out" | head -1)"; fi

missing=""
for n in text-join decimal count-items numbered-lines; do
  [ -n "$(bound "$reg" "michael/$n")" ] || missing="$missing michael/$n"
done
if [ -z "$missing" ]; then pass "the registry binds all four michael/* library names"
else bad "unbound after publication:$missing"; fi

# THE IDENTITY CLAIM FOR NAMESPACING. Qualifying a name rewrites references; it
# must not change what a definition MEANS. Each published object is compared
# against the object a BARE put of the same source produced in the oracle store.
drift=0
for n in text-join decimal count-items numbered-lines; do
  [ "$(bound "$reg" "michael/$n")" = "$(bound "$oracle" "$n")" ] || drift=$((drift + 1))
done
if [ "$drift" = 0 ]; then
  pass "each michael/* object is byte-identical to the bare put of the same source — the namespace is naming, not meaning"
else bad "$drift of 4 michael/* objects differ from the bare put of the same source"; fi

# FRICTION ITEM 3, RESOLVED. Publishing under a namespace leaves the PUBLISHER's
# own store able to build on what it just published: `publish --namespace` adopts
# each qualified name into the local store (the object is already present — only
# the binding was missing), so a dependent published next from THIS store resolves
# it without a round trip to fetch a name mapping the publisher authored. Contrast
# §6, where the DELEGATE's store never published the library and so still must
# resolve --remote.
pub_adopted=0
for n in text-join decimal count-items numbered-lines; do
  [ "$(bound "$pub" "michael/$n")" = "$(bound "$oracle" "$n")" ] && pub_adopted=$((pub_adopted + 1))
done
if [ "$pub_adopted" = 4 ]; then
  pass "the publisher's own store now binds all four michael/* names locally — building on what you just published needs no fetch"
else bad "publish left the publisher's store with $pub_adopted/4 qualified names bound"; fi

# ---------------------------------------------------------------------------------
head2 "5 — DELEGATE michael/* TO AN AUTOMATION KEY: publication rights without ownership"
if printf '%s' "$a_after" | grep -q 'publication delegated to'; then
  bad "michael/* already lists a delegate before anything was granted"
else pass "before the grant the registry lists NO delegate for michael/* (delegation revision 0)"; fi

del_out=$(OATH_STORE="$pub" "$OATH" delegate 'michael/*' --to "$auto_hex" --remote "$SRV_URL" --key "$holderkey" -y 2>&1); rc=$?
if [ "$rc" = 0 ] && printf '%s' "$del_out" | grep -q 'DELEGATED michael/\*' && printf '%s' "$del_out" | grep -q "subject:  $auto_hex"; then
  pass "the holder grants michael/* to the automation key: DELEGATED, naming the subject"
else bad "delegate failed ($rc): $(printf '%s' "$del_out" | tail -2 | tr '\n' ' ')"; fi

# The two revisions move INDEPENDENTLY, and that is the whole distinction between
# permission and ownership: a grant must advance the delegation revision and leave
# the authority revision exactly where it was.
a_deleg=$(governance "$pub" "$holderkey")
if printf '%s' "$a_deleg" | grep -q "HELD by $hold_hex (authority revision 1)" &&
   printf '%s' "$a_deleg" | grep -q "publication delegated to $auto_hex" &&
   printf '%s' "$a_deleg" | grep -q 'delegation revision 1'; then
  pass "the registry now reports: authority revision UNCHANGED at 1, delegation revision 1, automation listed as a delegate"
else bad "governance after the grant is wrong: $(printf '%s' "$a_deleg" | tr '\n' ' ')"; fi

# ---------------------------------------------------------------------------------
head2 "6 — THE DELEGATE PUBLISHES THE DEPENDENT APP, from a store of its own"
# The automation key starts with NOTHING — no corpus, no copy of the holder's
# store. That is the shape a CI runner actually has, and it is what makes the next
# failure a finding rather than an artefact of the setup.
auto_store=$(mk)
reg_before_fail=$(cat "$reg/names.json")
# STAGE A: nothing at all resolves locally, so it stops at the first type.
early=$(OATH_STORE="$auto_store" "$OATH" publish --remote "$SRV_URL" --key "$autokey" --namespace michael -y \
        "$here/item-report-app.oath" 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "the app published from a store holding no definitions at all"
elif printf '%s' "$early" | grep -q 'unknown type "Str"'; then
  pass "publishing from a wholly empty store FAILS at the first type: publish elaborates LOCALLY, against nothing"
else bad "the empty-store publish failed, but not on the first unresolvable type: $(printf '%s' "$early" | tail -1)"; fi

# STAGE B: the same command with the FOUNDATION present locally. It still fails,
# and now the message isolates the friction — the missing names are the PUBLISHED
# ones, which exist only on the registry and only under their qualified spelling.
# Two stages rather than one, because stage A alone would be satisfied by any
# empty-store failure and would not witness this at all.
OATH_STORE="$auto_store" "$OATH" put "$here/base-list.oath" --new >/dev/null 2>&1
OATH_STORE="$auto_store" "$OATH" put "$here/base-str.oath" --new >/dev/null 2>&1
early2=$(OATH_STORE="$auto_store" "$OATH" publish --remote "$SRV_URL" --key "$autokey" --namespace michael -y \
         "$here/item-report-app.oath" 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "the app published from a store that cannot resolve michael/text-join"
elif printf '%s' "$early2" | grep -q 'unknown name "michael/text-join"'; then
  pass "  ...and with the foundation present it STILL fails, naming the published dependency the store lacks"
else bad "the second publish failed, but not for the missing qualified name: $(printf '%s' "$early2" | tail -1)"; fi
if [ "$reg_before_fail" = "$(cat "$reg/names.json")" ]; then
  pass "  ...neither attempt moved the registry: both refusals are client-side, before anything is signed"
else bad "a failed publish changed the registry's names"; fi

# The fix, and the surprise worth pinning: `resolve --remote` does not merely WRITE
# a lock, it hydrates the store it runs in — objects and bound names both. One
# command, not two, puts the delegate in a position to build against the library.
auto_lock="$work/automation.lock"
OATH_STORE="$auto_store" "$OATH" resolve "$here/item-report-app.oath" --remote "$SRV_URL" -o "$auto_lock" >/dev/null 2>&1
pubnames=0
for n in text-join decimal count-items numbered-lines; do
  [ "$(bound "$auto_store" "michael/$n")" = "$(bound "$oracle" "$n")" ] && pubnames=$((pubnames + 1))
done
if [ "$pubnames" = 4 ] && [ -f "$auto_lock" ]; then
  pass "an ANONYMOUS resolve --remote both writes the lock and BINDS the four published names into the delegate's store"
else bad "resolve --remote left the delegate with $pubnames/4 published names bound"; fi

app_out="$work/publish-app.out"
OATH_STORE="$auto_store" "$OATH" publish --remote "$SRV_URL" --key "$autokey" --namespace michael -y \
  "$here/item-report-app.oath" >"$app_out" 2>&1; rc=$?
reg_app=$(bound "$reg" "michael/item-report")
if [ "$rc" = 0 ] && [ -n "$reg_app" ]; then
  pass "the DELEGATE publishes michael/item-report — a key that owns nothing binds a name in someone else's namespace"
else bad "the delegated publication failed ($rc): $(grep -i error "$app_out" | head -1)"; fi

# Attribution, not just permission: the journal must record WHO signed it. A
# delegated publication recorded against the holder would erase the distinction
# the delegation exists to make.
jline=$(grep '"name":"michael/item-report"' "$reg/log.jsonl" 2>/dev/null | tail -1)
if printf '%s' "$jline" | grep -q "\"author\":\"$auto_hex\"" && printf '%s' "$jline" | grep -q '"status":"accepted"'; then
  pass "the journal attributes it to the AUTOMATION key, not the holder — permission delegated, authorship not"
else bad "the publication is not journalled against the automation key: $(printf '%s' "$jline" | head -c 160)"; fi

# ---------------------------------------------------------------------------------
head2 "7 — WHAT A DELEGATE MAY NOT DO (refused client-side; witnessed by governance state)"
gov_before=$(governance "$auto_store" "$autokey")
r1=$(OATH_STORE="$auto_store" "$OATH" reserve 'michael/*' --remote "$SRV_URL" --key "$autokey" -y 2>&1); rc1=$?
if [ "$rc1" = 0 ]; then bad "the delegate reserved the namespace it was merely granted"
elif printf '%s' "$r1" | grep -q 'already held by'; then
  pass "the delegate cannot RESERVE michael/*: taking it would be a transfer"
else bad "the delegate's reserve failed for another reason: $(printf '%s' "$r1" | tail -1)"; fi

r2=$(OATH_STORE="$auto_store" "$OATH" delegate 'michael/*' --to "$str_hex" --remote "$SRV_URL" --key "$autokey" -y 2>&1); rc2=$?
if [ "$rc2" = 0 ]; then bad "the delegate delegated onward to a third key"
elif printf '%s' "$r2" | grep -q 'only the current holder may grant or revoke'; then
  pass "the delegate cannot DELEGATE ONWARD: only the holder may grant"
else bad "the delegate's onward grant failed for another reason: $(printf '%s' "$r2" | tail -1)"; fi

# Both directions of revocation, because they are different temptations: removing a
# rival, and removing the constraint on yourself.
r3=$(OATH_STORE="$auto_store" "$OATH" revoke 'michael/*' --from "$str_hex" --remote "$SRV_URL" --key "$autokey" -y 2>&1); rc3=$?
r4=$(OATH_STORE="$auto_store" "$OATH" revoke 'michael/*' --from "$auto_hex" --remote "$SRV_URL" --key "$autokey" -y 2>&1); rc4=$?
if [ "$rc3" = 0 ] || [ "$rc4" = 0 ]; then bad "the delegate revoked a grant (third party rc=$rc3, itself rc=$rc4)"
elif printf '%s' "$r3" | grep -q 'only the current holder may grant or revoke' &&
     printf '%s' "$r4" | grep -q 'only the current holder may grant or revoke'; then
  pass "the delegate cannot REVOKE — neither a third key nor its own grant"
else bad "a revoke attempt failed for another reason: $(printf '%s' "$r3$r4" | tail -1)"; fi

if [ "$gov_before" = "$(governance "$auto_store" "$autokey")" ]; then
  pass "after all four attempts the registry's governance report is character-for-character unchanged"
else bad "the delegate's attempts moved the governance state"; fi

# ---------------------------------------------------------------------------------
head2 "8 — THE CONSUMER: no key, no store, no corpus — a URL and the app source"
con_a=$(mk)
if OATH_STORE="$con_a" "$OATH" build item-report -o "$work/nope" >/dev/null 2>&1 || [ -e "$work/nope" ]; then
  bad "build produced something on a machine that holds no definitions"
else pass "build fails on the fresh consumer machine and emits no artifact — the control for everything below"; fi
store_untouched "$con_a" "  ...and that failed build wrote nothing"

con_lock="$work/consumer.lock"
res=$(OATH_STORE="$con_a" "$OATH" resolve "$here/item-report-app.oath" --remote "$SRV_URL" -o "$con_lock" 2>&1); rc=$?
ndeps=0; [ -f "$con_lock" ] && ndeps=$(dep_names "$con_lock" | wc -l | tr -d ' ')
if [ "$rc" = 0 ] && [ "$ndeps" = 6 ]; then
  pass "an ANONYMOUS resolve --remote succeeds and pins exactly 6 dependencies"
else bad "anonymous resolve pinned $ndeps dependencies (rc=$rc): $(printf '%s' "$res" | tail -1)"; fi

lockdrift=0
for n in text-join decimal count-items numbered-lines; do
  [ "$(dep_hash "$con_lock" "michael/$n")" = "$(bound "$reg" "michael/$n")" ] || lockdrift=$((lockdrift + 1))
done
[ "$(dep_hash "$con_lock" "List")" = "$reg_list" ] || lockdrift=$((lockdrift + 1))
[ "$(dep_hash "$con_lock" "Str")" = "$reg_str" ] || lockdrift=$((lockdrift + 1))
if [ "$lockdrift" = 0 ]; then pass "every pinned hash is the hash the registry publishes for that name"
else bad "$lockdrift pinned hash(es) disagree with the registry"; fi

con=$(mk)
cl=$(OATH_STORE="$con" "$OATH" clone "$here/item-report-app.oath" --lock "$con_lock" --remote "$SRV_URL" 2>&1); rc=$?
if [ "$rc" = 0 ]; then pass "an ANONYMOUS clone into a second fresh store succeeds — public code, no key"
else bad "anonymous clone failed ($rc): $(printf '%s' "$cl" | tail -1)"; fi
if [ "$(obj_count "$con")" = 7 ] && [ "$(name_count "$con")" = 7 ]; then
  pass "  ...leaving exactly the 6-object closure plus the app, and 7 bound names"
else bad "the cloned store holds $(obj_count "$con") objects / $(name_count "$con") names, expected 7 / 7"; fi

con_app=$(bound "$con" "item-report")
if [ -n "$con_app" ] && [ "$con_app" = "$reg_app" ]; then
  pass "the consumer's item-report IS the published object $reg_app — identity reproduced across the publish boundary"
else bad "identity NOT reproduced: registry ${reg_app:-<none>}, consumer ${con_app:-<none>}"; fi

bin="$work/item-report"
b_out=$(OATH_STORE="$con" "$OATH" build item-report -o "$bin" 2>&1); rc=$?
if [ "$rc" = 0 ] && [ -x "$bin" ]; then pass "the cloned store builds a native binary"
else bad "build failed (rc=$rc): $(printf '%s' "$b_out" | head -1)"; fi
prov=$(OATH_STORE="$NOSTORE" "$OATH" provenance "$bin" 2>&1)
if printf '%s' "$prov" | grep -q "\"entry_hash\": \"$con_app\""; then
  pass "the binary's own provenance names entry_hash $con_app — the artifact is bound to the published identity"
else bad "provenance does not name the published hash: $(printf '%s' "$prov" | grep entry_hash | head -1)"; fi

# Fixed arguments and a literal expectation, compared with cmp so a stray byte
# fails; a shell variable would strip exactly the trailing newlines this program
# emits.
expected="$work/expected.txt"
# The trailing blank line is the CLI entry protocol's, not the program's: the
# returned Str already ends in a newline and the protocol emits one after it.
printf '3 items\n1. alpha\n2. beta\n3. gamma\n\n' > "$expected"
got="$work/got.txt"
"$bin" alpha beta gamma > "$got" 2>"$work/run.err"; rc=$?
if [ "$rc" = 0 ] && cmp -s "$got" "$expected"; then
  pass "its output is byte-exact: $(od -c "$expected" | head -1 | tr -s ' ')"
else bad "output differs (rc=$rc): $(od -c "$got" | head -1 | tr -s ' ')"; fi

# `oath run` is the reference. A compiled-only assertion cannot tell a faithful
# backend from a backend and an expectation that are wrong the same way.
interp="$work/interp.txt"
OATH_STORE="$con" "$OATH" run item-report -- alpha beta gamma > "$interp" 2>"$work/interp.err"; irc=$?
if [ "$irc" != 0 ]; then bad "the reference interpreter exited $irc: $(head -1 "$work/interp.err")"
elif cmp -s "$interp" "$got"; then pass "the interpreter, on the same store, produces the same bytes"
else bad "compiled and interpreted output differ"; fi

# ---------------------------------------------------------------------------------
head2 "9 — THE REGISTRY'S EXACT STATE: every name, every hash, and nothing else"
# Not ">= the names we published": a registry holding one MORE name than this run
# created would pass a subset check and fail here.
wrong=""
check_bind() { [ "$(bound "$reg" "$1")" = "$2" ] || wrong="$wrong $1"; }
# The marker is compared against the hash it had BEFORE the server started: it is
# setup, not a publication, and it must not have moved either.
check_bind "$marker" "$marker_hash"
check_bind "List" "$(bound "$oracle" "List")"
check_bind "Str" "$(bound "$oracle" "Str")"
for n in text-join decimal count-items numbered-lines; do check_bind "michael/$n" "$(bound "$oracle" "$n")"; done
check_bind "michael/item-report" "$con_app"
if [ "$(name_count "$reg")" = 8 ] && [ -z "$wrong" ]; then
  pass "the registry binds exactly 8 names — the marker, List, Str, 4 library names and the app — each to the expected object"
else bad "registry state is wrong: $(name_count "$reg") names (expected 8); mismatched:$wrong"; fi

# The snapshot every refusal below is judged against. Byte comparison of the whole
# name->hash map, so a refusal is credited by STATE and never by a message.
reg_snapshot=$(cat "$reg/names.json")
journal_before=$(wc -l < "$reg/log.jsonl" 2>/dev/null | tr -d ' ')

# ---------------------------------------------------------------------------------
head2 "10 — REFUSALS: a third key, and no key at all"
# (a) ANONYMOUS WRITE. Every `oath` write path refuses client-side without a key,
# which would test the CLI, so the request is made raw. --public-reads must widen
# reads ONLY.
anon=$(curl -s -X POST "$SRV_URL/mcp" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"put","arguments":{"source":"(defn anonymous-write [] [(n Int)] Int n)"}}}' 2>&1)
if printf '%s' "$anon" | grep -q '"isError":true' && printf '%s' "$anon" | grep -qi 'read-only'; then
  pass "an unsigned, unauthenticated put is refused by the REGISTRY as a read-only principal"
else bad "the anonymous write was not refused as read-only: $(printf '%s' "$anon" | head -c 200)"; fi

# (b) UNAUTHORIZED PUBLICATION by a key that is neither holder nor delegate. The
# stranger gets a store hydrated anonymously, so the attempt fails on AUTHORITY and
# not on a missing dependency — a refusal for the wrong reason would prove nothing.
stranger_store=$(mk)
OATH_STORE="$stranger_store" "$OATH" hydrate "$con_lock" --remote "$SRV_URL" >/dev/null 2>&1
hij=$(OATH_STORE="$stranger_store" "$OATH" publish --remote "$SRV_URL" --key "$strangerkey" \
      --namespace michael -y "$here/unauthorized-text-join.oath" 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "a key that neither holds nor is delegated michael/* repointed michael/text-join"
elif printf '%s' "$hij" | grep -q 'BLOCKED' && printf '%s' "$hij" | grep -q 'neither the holder nor a delegate whose scope covers this name'; then
  pass "the stranger's publication is BLOCKED by the REGISTRY, naming the holder and the submitter"
else bad "the unauthorized publish failed, but not on authority: $(printf '%s' "$hij" | tail -1)"; fi

# The object IS stored — a blocked publication is journalled evidence, not a
# discarded request — so the witness is that nothing NAMES it.
hij_hash=$(printf '%s' "$hij" | grep -oE 'object stored as #[0-9a-f]{12}' | grep -oE '[0-9a-f]{12}' | head -1)
if [ -n "$hij_hash" ] && ls "$reg/objects" 2>/dev/null | grep -q "^$hij_hash" && ! grep -q "$hij_hash" "$reg/names.json"; then
  pass "  ...its object is stored (#$hij_hash) and bound to NO name — refused by policy, not by discarding evidence"
else bad "the blocked object is not in the expected stored-but-unbound state (#${hij_hash:-none})"; fi

# (c) TAKING THE NAMESPACE.
resv=$(OATH_STORE="$stranger_store" "$OATH" reserve 'michael/*' --remote "$SRV_URL" --key "$strangerkey" -y 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "a third key reserved a namespace already held"
elif printf '%s' "$resv" | grep -q 'already held by'; then
  pass "reserving michael/* with the stranger key is refused — taking it would be a TRANSFER"
else bad "the unauthorized reserve failed for another reason: $(printf '%s' "$resv" | tail -1)"; fi

# THE STATE WITNESS for all three at once.
if [ "$reg_snapshot" = "$(cat "$reg/names.json")" ]; then
  pass "after all three attempts the registry's name->hash map is byte-identical to the snapshot"
else bad "the registry's names changed across the refusal attempts"; fi

journal_after=$(wc -l < "$reg/log.jsonl" 2>/dev/null | tr -d ' ')
if [ "$journal_after" -gt "$journal_before" ] && grep '"status":"blocked"' "$reg/log.jsonl" 2>/dev/null | grep -q "$str_hex"; then
  pass "and the refusal is RECORDED: an append-only journal entry, status blocked, naming the stranger key"
else bad "the blocked attempt is not journalled against the stranger key (lines $journal_before -> $journal_after)"; fi

# ---------------------------------------------------------------------------------
head2 "11 — REVOCATION: the grant is withdrawable, and the REGISTRY is what enforces it"
# This is the server-side adjudication of delegation. Section 7's refusals never
# reached the registry; this one does — the same key, the same prefix, the same
# command, before and after a revocation, with only the registry's state differing.
rev_out=$(OATH_STORE="$pub" "$OATH" revoke 'michael/*' --from "$auto_hex" --remote "$SRV_URL" --key "$holderkey" -y 2>&1); rc=$?
if [ "$rc" = 0 ] && printf '%s' "$rev_out" | grep -q 'REVOKED michael/\*'; then
  pass "the holder revokes the automation key: REVOKED"
else bad "revoke failed ($rc): $(printf '%s' "$rev_out" | tail -2 | tr '\n' ' ')"; fi

a_revoked=$(governance "$pub" "$holderkey")
if printf '%s' "$a_revoked" | grep -q "HELD by $hold_hex (authority revision 1)" &&
   printf '%s' "$a_revoked" | grep -q 'delegation revision 2' &&
   ! printf '%s' "$a_revoked" | grep -q "publication delegated to $auto_hex"; then
  pass "the registry reports: authority STILL revision 1, delegation revision 2, and no delegate listed"
else bad "governance after the revocation is wrong: $(printf '%s' "$a_revoked" | tr '\n' ' ')"; fi

# The same store, the same key, a prefix it was publishing into minutes ago.
exd=$(OATH_STORE="$auto_store" "$OATH" publish --remote "$SRV_URL" --key "$autokey" \
      --namespace michael -y "$here/unauthorized-text-join.oath" 2>&1); rc=$?
if [ "$rc" = 0 ]; then bad "the EX-delegate still published under michael/* after revocation"
elif printf '%s' "$exd" | grep -q 'BLOCKED' && printf '%s' "$exd" | grep -q 'neither the holder nor a delegate whose scope covers this name'; then
  pass "the ex-delegate's next publication is BLOCKED by the REGISTRY — revocation is enforced server-side, not by the client"
else bad "the post-revocation publish failed, but not on delegate status: $(printf '%s' "$exd" | tail -1)"; fi

if [ "$reg_snapshot" = "$(cat "$reg/names.json")" ]; then
  pass "  ...and michael/text-join still points where the holder published it"
else bad "the revoked key moved a name"; fi

kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null; SRV_PID=""

# ---------------------------------------------------------------------------------
head2 "12 — THE REPOSITORY IS UNTOUCHED"
corpus_after=$(cd "$root" && git status --porcelain codebase/ 2>/dev/null)
if [ "$corpus_after" = "$corpus_before" ]; then pass "codebase/ is exactly as this run found it"
else bad "codebase/ changed during the run: $(printf '%s' "$corpus_after" | head -3)"; fi

# ---------------------------------------------------------------------------------
# Completeness guard: a skipped block must not exit green with fewer checks. Bump
# this deliberately when adding an assertion (negative-control it by editing down).
expected_checks=48
head2 "RESULT"
if [ "$checks" -ne "$expected_checks" ]; then
  printf 'INCOMPLETE — ran %d checks, expected %d: an assertion block was skipped or double-counted\n' "$checks" "$expected_checks" >&2
  exit 1
fi
if [ "$failures" -eq 0 ]; then
  printf 'PASS — %d/%d checks; the publish loop survived every attack\n' "$checks" "$expected_checks"
  exit 0
else
  printf 'FAIL — %d of %d checks failed\n' "$failures" "$checks" >&2
  exit 1
fi
