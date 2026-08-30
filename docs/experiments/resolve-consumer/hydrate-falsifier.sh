#!/bin/sh
# Falsifier for `oath hydrate` — the inverse of `oath resolve`, built to close the
# gap the resolve-consumer flywheel surfaced (docs/experiments/resolve-consumer-friction.md
# §1): a consumer holding a LOCKFILE but not the source store had a checkable record
# of exactly which objects it needed and no command to obtain them.
#
# THE CLAIM, stated as something that can fail:
#   given a lockfile and an object source, `oath hydrate` populates a store so that
#   `oath put --lock` — which FAILS against an empty store — then SUCCEEDS, and every
#   hydrated object carries the hash the lock pinned. Nothing is recomputed: the
#   closure is already enumerated in the lock.
#
# The script does not narrate a happy path; it ATTACKS the claim:
#   - the gap is real (put --lock genuinely fails on the empty store) — else there is
#     nothing to close and every later PASS is vacuous;
#   - hydrate reproduces the pinned closure by hash, exactly (no more, no fewer, no
#     substitutions), and binds each direct name to the hash the lock names;
#   - hydrate is transactional against VALIDATION failure: a source missing an object,
#     a tampered lock, or an unclosed lock leaves the target UNTOUCHED — witnessed by
#     STORE STATE, never by the error message (it does NOT claim atomicity against a
#     mid-commit I/O failure, and does not test one);
#   - hydrate is identity-neutral (the put's object hash matches resolving directly)
#     and idempotent;
#   - a non-primary alias survives a non-empty target (no lost constructor vocabulary);
#   - the write is refused into the canonical corpus and with no source named.
#
# Read-only against the repo: the source is a throwaway copy of codebase@HEAD, and
# every target is a fresh mktemp -d. It builds nothing into codebase/.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd)
here=$(cd "$(dirname "$0")" && pwd)
OATH="$root/oath/oath"
[ -x "$OATH" ] || { echo "SETUP FAILED: build oath first (cd oath && go build -o oath .)" >&2; exit 1; }
main="$here/main.oath"

# Run in a CLEAN Oath environment. Every command sets OATH_STORE explicitly, and the
# refusal checks depend on the ambient having NO source and NO store override — an
# inherited OATH_REGISTRY would give "no --from" a remote source, and an exported
# OATH_STORE would make the canonical-corpus check target something other than
# ./codebase. Clear them so the scenarios are the ones the labels claim.
unset OATH_STORE OATH_REGISTRY OATH_KEY OATH_BACKEND OATH_AUTHOR 2>/dev/null || true

ledger=$(mktemp)
cleanup() { while read -r d; do [ -n "$d" ] && rm -rf "$d"; done < "$ledger"; rm -f "$ledger"; }
trap cleanup EXIT INT TERM
mk() { d=$(mktemp -d) || { echo "SETUP FAILED: mktemp" >&2; exit 1; }; printf '%s\n' "$d" >> "$ledger"; printf '%s' "$d"; }
work=$(mk)  # per-run scratch for lockfiles; cleaned via the ledger, never a shared /tmp path

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
head2() { printf '\n%s\n' "$1"; }

# store_untouched <dir> <label> — a target a FAILED hydrate must not have written to:
# no object files and no bound name. Absence of output is not absence of state, so
# this reads the store, not the transcript.
store_untouched() {
  d=$1; l=$2
  objs=0; [ -d "$d/objects" ] && objs=$(ls -A "$d/objects" 2>/dev/null | wc -l | tr -d ' ')
  named=no; [ -f "$d/names.json" ] && grep -q '"[0-9a-f]\{64\}"' "$d/names.json" 2>/dev/null && named=yes
  if [ "$objs" = 0 ] && [ "$named" = no ]; then pass "$l"
  else bad "$l (objects=$objs, named=$named)"; fi
}

# lock field extractors — the "closure" array is bare hash strings; "dependencies"
# is name->hash. Both are pulled from the lock the harness generated, not hard-coded.
closure_hashes() { sed -n '/"closure"/,/]/p' "$1" | grep -oE '[0-9a-f]{64}'; }
dep_lines()      { sed -n '/"dependencies"/,/}/p' "$1" | grep -oE '"[a-zA-Z0-9_-]+": "[0-9a-f]{64}"'; }
main_hash()      { printf '%s' "$1" | grep -oE '#[0-9a-f]{12}' | head -1; }

# ---------------------------------------------------------------------------------
# SETUP: a source store (codebase@HEAD + the consumer's own lib + main), and the
# lock resolve writes from it. The corpus comes from HEAD, not the worktree, so this
# runs unchanged on a dirty tree and never reports staged edits as "the committed corpus".
head2 "SETUP — source store and lock"
src=$(mk)
( cd "$root" && git archive HEAD:codebase ) | ( cd "$src" && tar -x ) || { echo "SETUP FAILED: git archive" >&2; exit 1; }
OATH_STORE="$src" "$OATH" put "$here/lib.oath" --new >/dev/null 2>&1 || { echo "SETUP FAILED: put lib.oath" >&2; exit 1; }
OATH_STORE="$src" "$OATH" put "$main"          --new >/dev/null 2>&1 || { echo "SETUP FAILED: put main.oath" >&2; exit 1; }
lock=$work/main.lock
OATH_STORE="$src" "$OATH" resolve "$main" -o "$lock" >/dev/null 2>&1 || { echo "SETUP FAILED: resolve" >&2; exit 1; }
n_closure=$(closure_hashes "$lock" | wc -l | tr -d ' ')
n_deps=$(dep_lines "$lock" | wc -l | tr -d ' ')
printf '  lock: %s direct name(s), %s-object closure\n' "$n_deps" "$n_closure"
[ "$n_closure" -ge 2 ] && [ "$n_deps" -ge 2 ] || { echo "SETUP FAILED: degenerate lock" >&2; exit 1; }

# ---------------------------------------------------------------------------------
head2 "1 — THE GAP IS REAL: put --lock fails against an empty store"
# If this ever passed, the empty store would already satisfy the lock and every PASS
# below would be vacuous. This is the control that makes the falsifier mean anything.
empty=$(mk)
if OATH_STORE="$empty" "$OATH" put --lock "$lock" "$main" --new >/dev/null 2>&1; then
  bad "put --lock SUCCEEDED against an empty store — the premise is dead, later checks are vacuous"
else
  pass "put --lock fails against an empty store (the gap hydrate exists to close)"
fi
store_untouched "$empty" "  ...and the failed put --lock left the empty store untouched"

# ---------------------------------------------------------------------------------
head2 "2 — HYDRATE CLOSES IT"
tgt=$(mk)
out=$(OATH_STORE="$tgt" "$OATH" hydrate "$lock" --from "$src" 2>&1); rc=$?
if [ "$rc" = 0 ]; then pass "hydrate exits 0 — $out"; else bad "hydrate failed ($rc): $out"; fi

# object count is EXACTLY the closure length — not ">=", which a stray write would pass
objs=0; [ -d "$tgt/objects" ] && objs=$(ls -A "$tgt/objects" 2>/dev/null | wc -l | tr -d ' ')
if [ "$objs" = "$n_closure" ]; then pass "target holds exactly the $n_closure closure objects"
else bad "target holds $objs objects, lock closure is $n_closure"; fi

# every closure hash is present as an object, addressed by its own hash
missing=0
for h in $(closure_hashes "$lock"); do [ -f "$tgt/objects/$h.bin" ] || missing=$((missing + 1)); done
if [ "$missing" = 0 ]; then pass "every closure hash is present, addressed by its own hash"
else bad "$missing closure hash(es) absent from the hydrated store"; fi

# every direct dependency NAME is bound to the hash the lock pinned
wrongbind=0
for pair in $(dep_lines "$lock" | tr -d ' '); do
  # pair is "name":"hash"
  if ! grep -q "$pair" "$tgt/names.json" 2>/dev/null; then
    # names.json is pretty-printed with a space; normalise both sides before matching
    name=$(printf '%s' "$pair" | sed 's/^"\([^"]*\)".*/\1/')
    hash=$(printf '%s' "$pair" | grep -oE '[0-9a-f]{64}')
    grep -q "\"$name\": \"$hash\"" "$tgt/names.json" 2>/dev/null || wrongbind=$((wrongbind + 1))
  fi
done
if [ "$wrongbind" = 0 ]; then pass "every direct name is bound to its pinned hash"
else bad "$wrongbind direct name(s) bound to a hash other than the lock's"; fi

# THE payoff: put --lock now succeeds where it failed in check 1
put_out=$(OATH_STORE="$tgt" "$OATH" put --lock "$lock" "$main" --new 2>&1); rc=$?
if [ "$rc" = 0 ]; then pass "put --lock now SUCCEEDS — the same command, the same lock, a hydrated store"
else bad "put --lock still fails after hydrate ($rc): $(printf '%s' "$put_out" | head -1)"; fi
hydrated_main=$(main_hash "$put_out")

# ---------------------------------------------------------------------------------
head2 "3 — IDENTITY-NEUTRAL: the same object hash as resolving directly"
# hydrate must not perturb identity. Resolve main --from src into a fresh store and
# put it; the resulting main hash must equal the hydrated path's. Content addressing
# makes this an identity check on the whole closure, not just main.
fresh=$(mk); fresh_lock=$work/fresh.lock
OATH_STORE="$fresh" "$OATH" resolve "$main" --from "$src" -o "$fresh_lock" >/dev/null 2>&1 || bad "control resolve --from failed"
direct_out=$(OATH_STORE="$fresh" "$OATH" put --lock "$fresh_lock" "$main" --new 2>&1)
direct_main=$(main_hash "$direct_out")
if [ -n "$hydrated_main" ] && [ "$hydrated_main" = "$direct_main" ]; then
  pass "main hashes to $hydrated_main both ways — hydrate is identity-neutral"
else bad "main hash differs: hydrated=$hydrated_main direct=$direct_main"; fi

# ---------------------------------------------------------------------------------
head2 "4 — IDEMPOTENT: a second hydrate is a no-op, not a doubling or an error"
# Baseline is the count RIGHT NOW, not the closure length: check 2's put --lock
# elaborated and stored main itself, so the store legitimately holds one more object
# than the closure. The idempotence claim is hydrate-twice == hydrate-once.
before=0; [ -d "$tgt/objects" ] && before=$(ls -A "$tgt/objects" 2>/dev/null | wc -l | tr -d ' ')
out2=$(OATH_STORE="$tgt" "$OATH" hydrate "$lock" --from "$src" 2>&1); rc=$?
objs2=0; [ -d "$tgt/objects" ] && objs2=$(ls -A "$tgt/objects" 2>/dev/null | wc -l | tr -d ' ')
if [ "$rc" = 0 ] && [ "$objs2" = "$before" ]; then pass "second hydrate exits 0 and the object count is unchanged ($objs2)"
else bad "second hydrate perturbed the store (rc=$rc, objects=$objs2 vs $before before): $out2"; fi

# ---------------------------------------------------------------------------------
head2 "5 — TRANSACTIONAL: a source missing an object writes NOTHING"
# Remove one non-root closure object from a copy of the source. hydrate must fail
# WHOLE — the fetch/verify happens in an in-memory scratch, so a mid-closure failure
# cannot leave a half-populated target.
srcbad=$(mk); cp -R "$src/." "$srcbad/"
victim=$(closure_hashes "$lock" | sed -n '3p')   # not the first (a leaf/root edge); a middle object
rm -f "$srcbad/objects/$victim.bin"
t_partial=$(mk)
if OATH_STORE="$t_partial" "$OATH" hydrate "$lock" --from "$srcbad" >/dev/null 2>&1; then
  bad "hydrate SUCCEEDED from a source missing #$(printf '%s' "$victim" | cut -c1-12)"
else pass "hydrate fails when the source is missing a closure object"; fi
store_untouched "$t_partial" "  ...and left the target untouched — no half-populated store"

# ---------------------------------------------------------------------------------
head2 "6 — INTEGRITY: a tampered lock is refused, target untouched"
# Flip one hex digit of one closure hash. That hash names no object anywhere, so the
# fetch fails by content address — a lock cannot smuggle in an object it mis-hashes.
victim2=$(closure_hashes "$lock" | sed -n '1p')
flipped=$(printf '%s' "$victim2" | sed 's/.$//')$(printf '%s' "$victim2" | sed 's/.*\(.\)$/\1/' | tr '0123456789abcdef' '123456789abcdef0')
tamper_lock=$work/tamper.lock
sed "s/$victim2/$flipped/" "$lock" > "$tamper_lock"
t_tamper=$(mk)
if OATH_STORE="$t_tamper" "$OATH" hydrate "$tamper_lock" --from "$src" >/dev/null 2>&1; then
  bad "hydrate accepted a lock with a tampered closure hash"
else pass "hydrate refuses a tampered closure hash (no object answers to it)"; fi
store_untouched "$t_tamper" "  ...and left the target untouched"

# ---------------------------------------------------------------------------------
head2 "6b — CLOSEDNESS: an incomplete lock is refused even when the source could complete it"
# Drop one TRANSITIVE-ONLY object from the closure (a hash the closure lists but the
# dependencies do not) while leaving the SOURCE intact. hydrate must not paper over
# the gap from the source: the closure is the whole universe it commits, so an object
# left referencing the dropped hash must fail closedness. Regression witness for the
# P1 where recursing into the source silently completed an unclosed lock.
deps_file="$work/deps.txt"   # under $work (ledger-tracked), never the worktree
dep_lines "$lock" | grep -oE '[0-9a-f]{64}' | sort -u > "$deps_file"
drop=$(closure_hashes "$lock" | sort -u | grep -vxF -f "$deps_file" | head -1)
rm -f "$deps_file"
if [ -z "$drop" ]; then
  bad "SETUP: no transitive-only closure object to drop"
else
  incomplete_lock=$work/incomplete.lock
  grep -v "$drop" "$lock" > "$incomplete_lock"
  t_incomplete=$(mk)
  if OATH_STORE="$t_incomplete" "$OATH" hydrate "$incomplete_lock" --from "$src" >/dev/null 2>&1; then
    bad "hydrate SUCCEEDED on a lock omitting transitive #$(printf '%s' "$drop" | cut -c1-12) (source still holds it)"
  else pass "hydrate refuses an incomplete closure the source could have completed"; fi
  store_untouched "$t_incomplete" "  ...and left the target untouched"
fi

# ---------------------------------------------------------------------------------
head2 "7 — REFUSALS: no source, and the canonical corpus"
t_nosrc=$(mk)
if OATH_STORE="$t_nosrc" "$OATH" hydrate "$lock" >/dev/null 2>&1; then
  bad "hydrate ran with no --from and no --remote"
else pass "hydrate refuses with no object source named"; fi

# Against the DEFAULT store (codebase), with no OATH_STORE override, hydrate must
# refuse to write the tracked corpus even though a source is given.
canon_out=$(cd "$root" && "$OATH" hydrate "$lock" --from "$src" 2>&1); rc=$?
if [ "$rc" != 0 ] && printf '%s' "$canon_out" | grep -q 'canonical corpus'; then
  pass "hydrate refuses to write the canonical corpus"
else bad "hydrate did not refuse the canonical corpus (rc=$rc): $(printf '%s' "$canon_out" | head -1)"; fi

# ---------------------------------------------------------------------------------
head2 "8 — ALIAS PRESERVATION: a non-primary alias survives a non-empty target"
# The subtle non-empty-target hazard (StoreObject discards an incoming Aliases map,
# so a lock binding a NON-primary alias into a target that already holds the object
# would lose that alias's constructor vocabulary). Two structurally identical
# datatypes share one hash H; the source knows H as Qux with Foo a non-primary alias;
# the target already holds H as Baz. Hydrating a lock that binds Foo must keep Foo's
# MkFoo vocabulary, or `put --lock` on a consumer that pattern-matches MkFoo fails.
ad=$(mk)
printf '(data Foo [] (MkFoo))\n'                              > "$ad/foo.oath"
printf '(data Qux [] (MkQux))\n'                              > "$ad/qux.oath"
printf '(data Baz [] (MkBaz))\n'                              > "$ad/baz.oath"
printf '(defn use-foo [] [(x Foo)] Int (match x ((MkFoo) 0)))\n' > "$ad/usefoo.oath"
asrc=$(mk); atgt=$(mk); ulock="$ad/usefoo.lock"
OATH_STORE="$asrc" "$OATH" put "$ad/foo.oath" --new >/dev/null 2>&1   # H under Foo
OATH_STORE="$asrc" "$OATH" put "$ad/qux.oath" --new >/dev/null 2>&1   # H now Qux, Foo a non-primary alias
OATH_STORE="$asrc" "$OATH" resolve "$ad/usefoo.oath" -o "$ulock" >/dev/null 2>&1
OATH_STORE="$atgt" "$OATH" put "$ad/baz.oath" --new >/dev/null 2>&1   # target pre-holds H under Baz
if OATH_STORE="$atgt" "$OATH" hydrate "$ulock" --from "$asrc" >/dev/null 2>&1; then
  pass "hydrate into a target already holding the object (under another name) succeeds"
else bad "hydrate failed against a non-empty target"; fi
# Decidable evidence of the fix: the target's meta for H must carry Foo's MkFoo
# vocabulary (as primary name or alias). Without the union merge this entry is gone.
H=$(grep -oE '[0-9a-f]{64}' "$ulock" | head -1)
if grep -A3 '"Foo"' "$atgt/meta/$H.json" 2>/dev/null | grep -q 'MkFoo'; then
  pass "the bound alias Foo keeps its MkFoo constructor vocabulary in the target meta"
else bad "Foo's MkFoo vocabulary was lost on commit into the non-empty target"; fi
# End-to-end payoff: a consumer that pattern-matches MkFoo puts against the lock.
if OATH_STORE="$atgt" "$OATH" put --lock "$ulock" "$ad/usefoo.oath" --new >/dev/null 2>&1; then
  pass "put --lock on a consumer matching MkFoo SUCCEEDS — no unknown-constructor failure"
else bad "put --lock failed: the bound alias could not resolve its constructor"; fi

# SAME NAME, CONFLICTING VOCABULARY: target holds Foo=[MkOld], source holds Foo=[MkNew]
# (same structure, same hash). The lock is authored against the source, so hydrate
# must make Foo mean MkNew — keeping the target's stale MkOld would fail the consumer.
cd2=$(mk); cs=$(mk); ct=$(mk)
printf '(data Foo [] (MkNew))\n'                                 > "$cd2/foo_new.oath"
printf '(data Foo [] (MkOld))\n'                                 > "$cd2/foo_old.oath"
printf '(defn use-new [] [(x Foo)] Int (match x ((MkNew) 0)))\n' > "$cd2/usenew.oath"
clock="$cd2/usenew.lock"
OATH_STORE="$cs" "$OATH" put "$cd2/foo_new.oath" --new >/dev/null 2>&1
OATH_STORE="$cs" "$OATH" resolve "$cd2/usenew.oath" -o "$clock" >/dev/null 2>&1
OATH_STORE="$ct" "$OATH" put "$cd2/foo_old.oath" --new >/dev/null 2>&1
OATH_STORE="$ct" "$OATH" hydrate "$clock" --from "$cs" >/dev/null 2>&1
if OATH_STORE="$ct" "$OATH" put --lock "$clock" "$cd2/usenew.oath" --new >/dev/null 2>&1; then
  pass "on a same-name/different-vocabulary collision the SOURCE vocabulary wins (MkNew resolves)"
else bad "put --lock failed: the target's stale vocabulary shadowed the source's for a bound name"; fi

# ---------------------------------------------------------------------------------
head2 "9 — DAMAGED TARGET: a present meta record is not trusted as a present object"
# A store can hold meta/<h>.json while its object is missing or corrupt. hydrate must
# not treat the meta record as proof the object exists and only fix naming — it must
# (re)write the object, or it binds a name to an unreadable hash and reports success.
dd=$(mk); ds=$(mk); dt=$(mk)
printf '(data Foo [] (MkFoo))\n'                                 > "$dd/foo.oath"
printf '(defn use-foo [] [(x Foo)] Int (match x ((MkFoo) 0)))\n' > "$dd/usefoo.oath"
dlock="$dd/d.lock"
OATH_STORE="$ds" "$OATH" put "$dd/foo.oath" --new >/dev/null 2>&1
OATH_STORE="$ds" "$OATH" resolve "$dd/usefoo.oath" -o "$dlock" >/dev/null 2>&1
OATH_STORE="$dt" "$OATH" put "$dd/foo.oath" --new >/dev/null 2>&1
Hd=$(grep -oE '[0-9a-f]{64}' "$dlock" | head -1)
rm -f "$dt/objects/$Hd.bin"   # damage: keep meta, drop the object
OATH_STORE="$dt" "$OATH" hydrate "$dlock" --from "$ds" >/dev/null 2>&1
if [ -f "$dt/objects/$Hd.bin" ]; then pass "hydrate rewrote the missing object rather than trusting the stale meta"
else bad "hydrate left the object missing while reporting success"; fi
if OATH_STORE="$dt" "$OATH" put --lock "$dlock" "$dd/usefoo.oath" --new >/dev/null 2>&1; then
  pass "put --lock reads the rehydrated object — the bound name resolves"
else bad "put --lock failed: the bound name pointed at an unreadable object"; fi

# ---------------------------------------------------------------------------------
# Completeness guard: a skipped block must not exit green with fewer checks. Bump
# this deliberately when adding an assertion (negative-control it by editing down).
expected_checks=23
head2 "RESULT"
if [ "$checks" -ne "$expected_checks" ]; then
  printf 'INCOMPLETE — ran %d checks, expected %d: an assertion block was skipped or double-counted\n' "$checks" "$expected_checks" >&2
  exit 1
fi
if [ "$failures" -eq 0 ]; then
  printf 'PASS — %d/%d checks; the claim survived every attack\n' "$checks" "$expected_checks"
  exit 0
else
  printf 'FAIL — %d of %d checks failed\n' "$failures" "$checks" >&2
  exit 1
fi
