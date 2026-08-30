#!/bin/sh
# A CONSUMER SPLIT ACROSS TWO FILES, RESOLVED INTO A STORE THAT STARTS EMPTY.
#
# The claim under test is narrower than "oath resolve works":
#
#     a (-> (List Str) Str) program whose external names come from TWO places —
#     the committed commons, and a sibling library that is not in the commons —
#     can be pinned, fetched, checked and built in a store that started with
#     nothing in it, and every definition gets the SAME HASH it gets against the
#     full corpus.
#
# discovery-consumer/run.sh asks whether the commons can be FOUND. This one asks
# what happens after: whether a two-file consumer can be REPRODUCED elsewhere.
# The distinction shows up in the lockfile, whose dependency set here is a
# MIXTURE — corpus names and sibling names, pinned side by side from two
# different origins — which a single-file consumer never produces.
#
# Every step is asserted, not printed. The script exits 0 only if all of them
# held; a setup failure aborts loudly rather than falling through to a summary
# line that would look like a pass.
#
# NOTHING IN THE WORKING TREE IS TOUCHED. The kernel is built into a temporary
# directory, every corpus-bearing store is extracted from `HEAD:codebase` — the
# commit, not the worktree, because "the committed corpus" is a claim about the
# commit — the target stores are `mktemp -d` empties, and the last check
# re-hashes codebase/ and fixtures/ to prove they are where they started.
# `put --new` is used throughout: in a throwaway store a new name is a
# reconstruction, not a publication decision, so the guard on fresh bindings
# does not apply.
set -eu

# COLLATION IS PART OF THE ASSERTION. lock_names sorts the dependency names, and
# an en_US.UTF-8 sort interleaves case ("length List range Str …") while a C sort
# does not ("List Str length range …"). The exact expected sets below are byte
# order, so the locale is pinned rather than inherited — otherwise the checks
# encode the machine that wrote them.
LC_ALL=C
export LC_ALL

root=$(git rev-parse --show-toplevel)
here=$(cd "$(dirname "$0")" && pwd)

checks=0
failures=0

# --- harness -----------------------------------------------------------------
# STATUS COMES FROM THE COMMAND, never from a pipeline: `oath ... | sed` reports
# sed's status, so under `set -e` a broken kernel would sail through printing
# nothing. Output is captured and inspected afterwards.
say()   { printf '%s\n' "$*"; }
head2() { printf '\n########## %s\n' "$*"; }
indent(){ printf '%s\n' "$1" | sed 's/^/    /'; }

# TEMPORARY EVERYTHING. The ledger is a FILE because new_* helpers are called
# through command substitution and therefore run in a SUBSHELL, where a shell
# variable they set is discarded and the trap would remove nothing — leaking a
# full corpus copy per run while every check still passes. It is read back a
# LINE AT A TIME rather than word-split, because a TMPDIR containing a space
# would otherwise turn `rm -rf` loose on a path prefix.
ledger=$(mktemp)
cleanup() {
  [ -f "$ledger" ] || return 0
  while IFS= read -r s; do [ -n "$s" ] && rm -rf "$s"; done < "$ledger"
  rm -f "$ledger"
}
trap cleanup EXIT

scratch=$(mktemp -d) || { echo "SETUP FAILED: mktemp" >&2; exit 1; }
printf '%s\n' "$scratch" >> "$ledger"
oath="$scratch/bin/oath"

# PREREQUISITE, PROBED RATHER THAN ASSUMED. This script builds the kernel from
# source and compiles the consumer with the Go backend; both need the Go
# toolchain, so there is no meaningful "skip the build" path.
command -v go >/dev/null 2>&1 || {
  echo "SETUP FAILED: no 'go' on PATH — this script builds the kernel and" >&2
  echo "              compiles the consumer with the Go backend. Install Go >= 1.25." >&2
  exit 1
}
mkdir -p "$scratch/bin"
# BUILT INTO THE SCRATCH DIR, not by `make build`. `make build` writes
# oath/oath in the worktree; a run of this script should leave the worktree
# exactly as it found it, and that includes artifacts git does not track.
( cd "$root/oath" && go build -o "$oath" . ) || {
  echo "SETUP FAILED: could not build the kernel" >&2; exit 1; }

oath_run() {  # oath_run <args...>  -> OUT ; aborts if the command fails
  if ! OUT=$("$oath" "$@" 2>&1); then
    printf 'SETUP FAILED: oath %s\n' "$*" >&2
    indent "$OUT" >&2
    exit 1
  fi
}
oath_fails() {  # oath_fails <args...> -> OUT ; aborts if the command SUCCEEDS
  if OUT=$("$oath" "$@" 2>&1); then
    printf 'SETUP FAILED: expected `oath %s` to fail, it succeeded\n' "$*" >&2
    indent "$OUT" >&2
    exit 1
  fi
}

# section <property-name> <report> — the slice of a find report belonging to ONE
# property. Without it, "str-drop provably satisfies it" anywhere in the output
# satisfies an assertion about ANY law, so a report that proved one law and
# skipped another would pass both checks.
section() { printf '%s\n' "$2" | awk -v want="  · $1" '
  function isHead() {
    return substr($0, 1, length(want)) == want &&
           (length($0) == length(want) || substr($0, length(want) + 1, 1) == " ")
  }
  isHead() { on = 1; next }        # --spec headers carry a trailing hash, so the
  /^  · / { on = 0 }               # match is a prefix and not an equality
  on { print }'; }
need_section() {  # abort if a slice came back empty — a renamed law must not pass silently
  s=$(section "$1" "$2")
  [ -n "$s" ] || { printf 'SETUP FAILED: could not slice the report by property %s\n' "$1" >&2; exit 1; }
  printf '%s' "$s"
}

pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
fail() { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
has()   { checks=$((checks + 1)); case "$3" in *"$2"*) printf '  [ok]   %s\n' "$1";;
          *) failures=$((failures + 1)); printf '  [FAIL] %s\n         expected to contain: %s\n' "$1" "$2" >&2;; esac; }
lacks() { checks=$((checks + 1)); case "$3" in *"$2"*) failures=$((failures + 1));
            printf '  [FAIL] %s\n         expected NOT to contain: %s\n' "$1" "$2" >&2;;
          *) printf '  [ok]   %s\n' "$1";; esac; }
eq()    { checks=$((checks + 1)); if [ "$2" = "$3" ]; then printf '  [ok]   %s\n' "$1";
          else failures=$((failures + 1)); printf '  [FAIL] %s\n         want: [%s]\n         got:  [%s]\n' "$1" "$2" "$3" >&2; fi; }

# A find report's candidate rows are `<name padded to 18> <marker>`, so the
# marker is matched TOGETHER WITH the name it belongs to. Matching them
# separately would let a report that proved one candidate and refuted another
# satisfy both assertions.
sat()   { has   "$1" "$(printf '%-18s ← provably satisfies it' "$2")" "$3"; }
unsat() { lacks "$1" "$(printf '%-18s ← provably satisfies it' "$2")" "$3"; }
ref()   { has   "$1" "$(printf '%-18s countermodel' "$2")" "$3"; }

# --- authorities -------------------------------------------------------------
# EVERY HASH IN THIS SCRIPT IS READ FROM AN AUTHORITY, never written into it.
# For a corpus name that authority is names.json AT HEAD; for a sibling name it
# is the store the definition was actually put into.
name_hash() {  # name_hash <names.json path> <name>
  sed -n "s/^[[:space:]]*\"$2\"[[:space:]]*:[[:space:]]*\"\([0-9a-f]\{64\}\)\".*$/\1/p" "$1"
}
committed_hash() {  # the hash the COMMIT binds a name to
  h=$(cd "$root" && git show HEAD:codebase/names.json |
      sed -n "s/^[[:space:]]*\"$1\"[[:space:]]*:[[:space:]]*\"\([0-9a-f]\{64\}\)\".*$/\1/p")
  [ -n "$h" ] || { echo "SETUP FAILED: no committed hash for $1" >&2; exit 1; }
  printf '%s' "$h"
}
store_hash() {  # store_hash <store> <name>
  h=$(name_hash "$1/names.json" "$2")
  [ -n "$h" ] || { echo "SETUP FAILED: $2 is not bound in $1" >&2; exit 1; }
  printf '%s' "$h"
}
# The lockfile's dependency block, isolated. `tail -n +2` drops the "dependencies"
# key line itself, which otherwise reads as a dependency named "dependencies";
# the closure array below it is bare strings and cannot match a name:hash line.
lock_block() { sed -n '/"dependencies"/,/^  }/p' "$1" | tail -n +2; }
lock_names() { lock_block "$1" | sed -n 's/^[[:space:]]*"\([^"]*\)".*$/\1/p' | sort | tr '\n' ' '; }
lock_dep()   { lock_block "$1" | sed -n "s/^[[:space:]]*\"$2\"[[:space:]]*:[[:space:]]*\"\([0-9a-f]\{64\}\)\".*$/\1/p"; }
short()      { printf '%s' "$1" | cut -c1-12; }

# pinned_to <label> <lockfile> <name> <expected hash>
pinned_to() { eq "$1" "$4" "$(lock_dep "$2" "$3")"; }

# --- stores ------------------------------------------------------------------
# THE CORPUS COMES FROM HEAD, NOT FROM THE WORKTREE. Copying codebase/ would
# copy whatever is staged or half-edited there and then report it as "the
# committed corpus" — the authority for that phrase is the commit, so it is read
# from the commit. It also means this runs unchanged on a dirty tree.
new_corpus_store() {
  s=$(mktemp -d)
  ( cd "$root" && git archive HEAD:codebase ) | ( cd "$s" && tar -x ) || {
    echo "SETUP FAILED: could not extract codebase/ from HEAD" >&2; exit 1; }
  printf '%s\n' "$s" >> "$ledger"
  printf '%s' "$s"
}
new_empty_store() {
  s=$(mktemp -d)
  printf '%s\n' "$s" >> "$ledger"
  printf '%s' "$s"
}

# A CONTENT SIGNATURE, not a status line, and not a diff. Three blind spots this
# must not share with the thing it guards:
#   - `git status --porcelain` prints a status CLASS and a path, so on a tree
#     that starts dirty an overwrite of the same file leaves both snapshots
#     reading " M codebase/names.json" while the bytes changed underneath.
#   - `git diff HEAD` on a TRACKED BINARY (codebase/objects/*.bin) emits only
#     "Binary files a/... and b/... differ" — a constant string carrying none of
#     the bytes — so a dirty-then-overwritten object diffs identically both times.
#   - `git ls-files --others` contributes PATHS ONLY, so an untracked file whose
#     bytes change under an unchanged name would leave the signature untouched.
# So hash the ACTUAL WORKING-TREE BYTES of every file under the two trees. A
# tracked file deleted in the worktree is recorded as MISSING so its removal
# still moves the signature.
tree_sig() {
  (
    cd "$root"
    git ls-files -- codebase fixtures |
      while IFS= read -r f; do
        if [ -f "$f" ]; then shasum -- "$f"; else echo "MISSING $f"; fi
      done
    git ls-files --others --exclude-standard -- codebase fixtures |
      while IFS= read -r f; do shasum -- "$f"; done
  ) | shasum
}
tree_before=$(tree_sig)

say "oath source: $(cd "$root" && git rev-parse --short HEAD)$(cd "$root" && [ -z "$(git status --porcelain -- oath/ codebase/)" ] || echo ' +MODIFIED')"
say "kernel built into a temporary directory; the worktree is not written to."

corpus=$(new_corpus_store)

# =============================================================================
head2 "THE NEED"
# =============================================================================
cat <<'NEED'
  A command-line program, entry protocol (-> (List Str) Str):

      $ numbered-args gamma alpha beta
      1. alpha
      2. beta
      3. gamma

  Required to satisfy three laws:
      no-args-is-empty                          ()     -> ""
      one-arg-is-line-one                       (s)    -> "1. " ++ s
      two-args-come-out-sorted-and-separated    (a b)  -> sorted, numbered, "\n"-joined

  It is split in two, and the split is the subject:
      lib.oath    four helpers the commons does NOT have — an Ord dictionary
                  for Str, a lexicographic sort, a line renderer, a numbering
                  pass — each a thin composition over definitions it does
      main.oath   the entry point, whose three calls come from BOTH places

  Eleven corpus operations are reused. STEP 1 and STEP 2 find every one of them
  by what it DOES; the rest of the script reproduces the whole thing in a store
  that starts empty.
NEED

export OATH_STORE="$corpus"

# =============================================================================
head2 "STEP 1 — PROBE THE SHAPES (before writing a single law)"
# =============================================================================
say "The cheapest query available: a signature and nothing else. --shape needs"
say "no property, so it is what a consumer can ask FIRST — and what it answers"
say "is how much a signature alone can settle. The answer varies enormously."
say ""
shape() {  # shape <probe basename> <expected row count>
  oath_run find --shape "$here/$1.oath"
  SHAPE=$OUT
  indent "$SHAPE"
  n=$(printf '%s\n' "$SHAPE" | tail -n +2 | grep -c '#' || true)
  eq "$1 returns exactly $2 candidate(s)" "$2" "$n"
}

shape probe-order-shape 1
has "the sort shape is settled by the signature ALONE — one candidate, PROVEN" \
    "sort-by            #$(short "$(committed_hash sort-by)")" "$SHAPE"
say ""
shape probe-join-shape 2
has "the joining shape names join-with, PROVEN" "join-with          #$(short "$(committed_hash join-with)")" "$SHAPE"
has "  ...and a second candidate it cannot rank" "first-or" "$SHAPE"
say ""
shape probe-render-shape 3
has "the rendering shape names show-nat, PROVEN"     "show-nat           #$(short "$(committed_hash show-nat)")" "$SHAPE"
has "  ...beside show-int, which is also plausible"  "show-int" "$SHAPE"
has "  ...and str-spaces, which is not"              "str-spaces" "$SHAPE"
say ""
shape probe-str-compare-shape 5
say ""
say "  THE LAST PROBE IS KEPT BECAUSE IT FAILS. Two of the eleven operations"
say "  this consumer reuses — the order it sorts by, and the prefix test one of"
say "  lib.oath's laws is written with — sit at the SAME signature, with three"
say "  definitions that are neither. A shape query returns all five and ranks"
say "  none. That is what the laws in STEP 2 are for."
has "the string-comparison shape returns the order it needs"  "str-lt" "$SHAPE"
has "  ...and the prefix test it also needs, indistinguishably" "str-prefix" "$SHAPE"
has "  ...and an equality test that is neither"                 "str-eq" "$SHAPE"

# =============================================================================
head2 "STEP 2 — THE ELEVEN NEEDS, BY LAW"
# =============================================================================
say "Two surfaces per need: --spec is a content-hash lookup (no solver), and"
say "--implies is one Z3 proof per candidate. The laws below were written from"
say "what the consumer REQUIRES, not copied from any target's own properties —"
say "which is why --spec misses most of them and --implies does not."

spec()    { oath_run find --spec    "$here/$1.oath"; SPEC=$OUT;  indent "$SPEC"; }
implies() { oath_run find --implies "$here/$1.oath"; IMPL=$OUT;  indent "$IMPL"
            lacks "$1: no candidate was left unsettled (no NO VERDICT)" "NO VERDICT" "$IMPL"; }

# --- 1. sort-by --------------------------------------------------------------
say ""; say "--- 1/11  sort-by : (-> {lt (-> a a Bool)} (List a) (List a))"
spec need-sort-by
has "--spec matches sort-by on preserves-length, by content hash alone" \
    "sort-by            (proven as \"preserves-length\")" "$(need_section preserves-length "$SPEC")"
implies need-sort-by
sat "sort-by is PROVED to preserve length"        sort-by "$(need_section preserves-length "$IMPL")"
sat "sort-by is PROVED to order a pair correctly" sort-by "$(need_section a-pair-comes-out-ordered "$IMPL")"

# --- 2. str-lt ---------------------------------------------------------------
say ""; say "--- 2/11  str-lt : (-> Str Str Bool)"
spec need-str-lt
has "--spec matches str-lt on irreflexivity" "str-lt             (proven as \"irreflexive\")" "$(need_section irreflexive "$SPEC")"
implies need-str-lt
irr=$(need_section irreflexive "$IMPL")
sat   "str-lt is PROVED irreflexive"                     str-lt     "$irr"
ref   "str-prefix is REFUTED on it, with a countermodel" str-prefix "$irr"
ref   "str-eq is REFUTED on it too"                      str-eq     "$irr"
unsat "  ...so the shape probe's tie is broken by PROOF, not by name" str-prefix "$irr"
sat "str-lt is PROVED to make the empty string least"    str-lt "$(need_section the-empty-string-is-least "$IMPL")"
sat "str-lt is PROVED to decide on the first codepoint"  str-lt "$(need_section a-smaller-first-codepoint-decides "$IMPL")"

# --- 3. show-nat -------------------------------------------------------------
say ""; say "--- 3/11  show-nat : (-> Int Str)"
spec need-show-nat
has "--spec finds no content-hash match — the laws are concrete, the target's are not" \
    "no definition states this law as written" "$(need_section zero-renders-as-zero "$SPEC")"
implies need-show-nat
ten=$(need_section ten-renders-as-two-digits "$IMPL")
one=$(need_section single-digits-are-one-wide "$IMPL")
seven=$(need_section seven-renders-as-seven "$IMPL")
sat "show-nat is PROVED to render 10 in two digits" show-nat "$ten"
sat "show-nat is PROVED to render 7 as a single \"7\"" show-nat "$seven"
ref "str-spaces is REFUTED"                         str-spaces "$one"
say ""
say "  AND THE HONEST RESULT: show-int satisfies every one of these laws too."
say "  They are all statements about NON-NEGATIVE inputs, which is all this"
say "  consumer supplies — its indices start at 1 — so the query cannot"
say "  separate them and should not pretend to. The guarantee column is the"
say "  only tiebreak the surface offers, and it is the consumer's to apply."
sat "show-int also PROVABLY satisfies the rendering laws" show-int "$ten"
sat "  ...on the single-digit width law"                  show-int "$one"
sat "  ...and on rendering 7 as \"7\" — every quoted law"  show-int "$seven"

# --- 4. str-append -----------------------------------------------------------
say ""; say "--- 4/11  str-append : (-> Str Str Str)"
spec need-str-append
has "--spec matches str-append on left-unit"  "str-append         (proven as \"left-unit\")"  "$(need_section left-unit "$SPEC")"
has "--spec matches str-append on length-adds" "str-append         (proven as \"length-adds\")" "$(need_section length-adds "$SPEC")"
implies need-str-append
ord=$(need_section the-left-operand-comes-first "$IMPL")
sat "str-append is PROVED to be a left unit"      str-append "$(need_section left-unit "$IMPL")"
sat "str-append is PROVED to be a right unit too" str-append "$(need_section right-unit "$IMPL")"
sat "str-append is PROVED to put the left operand first" str-append "$ord"
ref "the one rival at this shape is REFUTED on operand order" gh-report "$ord"

# --- 5. zip-with -------------------------------------------------------------
say ""; say "--- 5/11  zip-with : (-> (-> a b c) (List a) (List b) (List c))"
spec need-zip-with
say ""
say "  THE ONLY NEED OF THE ELEVEN THAT THE CHEAP SURFACE SETTLES OUTRIGHT:"
say "  all three laws matched by CONTENT HASH, no solver involved. That happens"
say "  when the consumer's laws and the target's laws are the same sentences —"
say "  here because both are the defining unfold of a fold, which has one"
say "  natural spelling. It is the exception, not the pattern."
for law in nil-left nil-right cons-step; do
  has "--spec matches zip-with on $law, by content hash" \
      "zip-with           (proven as \"$law\")" "$(need_section "$law" "$SPEC")"
done
implies need-zip-with
for law in nil-left nil-right cons-step; do
  sat "zip-with is PROVED on $law" zip-with "$(need_section "$law" "$IMPL")"
done

# --- 6. range ----------------------------------------------------------------
say ""; say "--- 6/11  range : (-> Int Int (List Int))"
spec need-range
has "--spec matches range on the span law" "range              (proven as \"length-is-span\")" \
    "$(need_section its-length-is-the-span "$SPEC")"
implies need-range
sat "range is PROVED to be empty on an empty span"   range "$(need_section an-empty-span-is-empty "$IMPL")"
sat "range is PROVED to count up from lo, half-open" range "$(need_section it-counts-up-from-lo "$IMPL")"
sat "range is PROVED to have the span's length"      range "$(need_section its-length-is-the-span "$IMPL")"

# --- 7. length ---------------------------------------------------------------
say ""; say "--- 7/11  length : (-> (List a) Int)"
spec need-length
has "--spec matches length on the cons step" "length             (proven as \"cons-adds-one\")" \
    "$(need_section cons-adds-one "$SPEC")"
implies need-length
for law in empty-is-zero cons-adds-one never-negative; do
  sat "length is PROVED on $law" length "$(need_section "$law" "$IMPL")"
done

# --- 8. join-with ------------------------------------------------------------
say ""; say "--- 8/11  join-with : (-> Str (List Str) Str)"
spec need-join-with
has "--spec has no content-hash match for the two-piece law — it is a paraphrase" \
    "no definition states this law as written" "$(need_section two-pieces-are-separated "$SPEC")"
has "  ...but its signature fallback names join-with, PROVEN" "join-with          PROVEN" \
    "$(need_section two-pieces-are-separated "$SPEC")"
implies need-join-with
sep=$(need_section two-pieces-are-separated "$IMPL")
sat "join-with is PROVED to join nothing to nothing" join-with "$(need_section empty-joins-to-empty "$IMPL")"
sat "join-with is PROVED to separate two pieces"     join-with "$sep"
ref "the rival at this shape is REFUTED on separation" first-or "$sep"
sat "  ...though it does satisfy the singleton law — one law would have chosen wrong" \
    first-or "$(need_section a-single-piece-is-itself "$IMPL")"

# --- 9. str-len --------------------------------------------------------------
say ""; say "--- 9/11  str-len : (-> Str Int)"
spec need-str-len
zero=$(need_section the-empty-string-is-zero "$SPEC")
has "--spec matches str-len on non-negativity" "str-len            (proven as \"nonneg\")" \
    "$(need_section never-negative "$SPEC")"
has "and the empty-string law content-hash-matches str-code — a DIFFERENT function" \
    "str-code           (tested as \"the-empty-string-is-zero\")" "$zero"
lacks "  ...while str-len, the target, does not state that law at all" "str-len            (" "$zero"
say ""
say "  One law, two functions: str-code returns the FIRST codepoint, which is"
say "  also 0 for the empty string. A single law is a weak query, and the"
say "  content-hash surface reports the coincidence rather than resolving it."
implies need-str-len
step=$(need_section a-codepoint-adds-one "$IMPL")
sat "str-len is PROVED on the cons step"      str-len  "$step"
ref "str-code is REFUTED on it, as it should be" str-code "$step"
ref "parse-nat is REFUTED on it too"             parse-nat "$step"

# --- 10. str-prefix ----------------------------------------------------------
say ""; say "--- 10/11  str-prefix : (-> Str Str Bool)  — the SAME shape as need 2"
spec need-str-prefix
self=$(need_section everything-prefixes-itself "$SPEC")
has "--spec matches str-prefix on reflexivity" "str-prefix         (proven as \"self\")" "$self"
has "  ...and str-eq on the same law, because both are reflexive" \
    "str-eq             (proven as \"reflexive\")" "$self"
has "  ...and the consumer's own media-type-is, same reflexive shape" "media-type-is" "$self"
has "  ...and path-is, likewise — every row the log quotes"           "path-is"       "$self"
implies need-str-prefix
app=$(need_section a-prefix-survives-appending "$IMPL")
sat "str-prefix is PROVED to survive appending"                    str-prefix "$app"
ref "str-eq is REFUTED on it — reflexivity alone would have chosen wrong" str-eq "$app"
ref "and str-lt, the other operation at this shape, is REFUTED too"  str-lt "$app"
sat "str-prefix is PROVED on the empty prefix"   str-prefix "$(need_section the-empty-string-prefixes-everything "$IMPL")"
sat "str-prefix is PROVED on the empty subject"  str-prefix "$(need_section nothing-nonempty-prefixes-the-empty-string "$IMPL")"

# --- 11. str-drop ------------------------------------------------------------
say ""; say "--- 11/11  str-drop : (-> Int Str Str)"
spec need-str-drop
has "--spec matches str-drop on drop-zero" "str-drop           (proven as \"drop-zero\")" \
    "$(need_section dropping-nothing-changes-nothing "$SPEC")"
implies need-str-drop
zero=$(need_section dropping-nothing-changes-nothing "$IMPL")
head_=$(need_section dropping-one-drops-the-head "$IMPL")
say ""
say "  Five definitions share this signature and THREE of them survive the"
say "  first law — dropping nothing is a no-op for a padder and for a"
say "  split-then-rejoin as well. The second law is the one that discriminates."
sat "str-pad-left survives the first law"      str-pad-left   "$zero"
sat "str-split-join survives the first law"    str-split-join "$zero"
sat "str-drop is PROVED to drop the head"      str-drop       "$head_"
ref "str-take is REFUTED on it"                str-take       "$head_"
ref "str-pad-left is REFUTED on it"            str-pad-left   "$head_"
ref "str-split-join is REFUTED on it"          str-split-join "$head_"
ref "gh-field is REFUTED on it"                gh-field       "$head_"
unsat "  ...leaving str-drop the only survivor" str-pad-left  "$head_"

# =============================================================================
head2 "STEP 3 — THE LAW THAT DOES NOT RETURN (a bounded negative)"
# =============================================================================
say "need-str-drop-monotone.oath states the most natural law of all about a"
say "suffix operation — dropping never lengthens — and it gets no verdict. It"
say "is kept OUT of need-str-drop.oath and asserted here instead, because the"
say "distinction it demonstrates is one this project pays for elsewhere:"
say ""
say "    NO PROOF IS NOT DISPROOF. The line below is a fact about the prover's"
say "    rlimit (SPEC §7.2), not about str-drop. A consumer reading it as a"
say "    refutation would reject a correct dependency."
say ""
oath_run find --implies "$here/need-str-drop-monotone.oath" --timeout 20s
indent "$OUT"
has   "the monotonicity law returns NO VERDICT, not a refutation" "NO VERDICT" "$OUT"
has   "  ...and the search says so: SEARCH INCOMPLETE"            "SEARCH INCOMPLETE" "$OUT"
has   "  ...after 49 candidate checks — the first candidate burns the whole budget" \
      "elapsed after 49 candidate checks" "$OUT"
lacks "no candidate is claimed to satisfy it"                     "provably satisfies it" "$OUT"
lacks "and none is claimed to be refuted by it"                   "countermodel" "$OUT"

# =============================================================================
head2 "STEP 4 — THE COMBINED SOURCE: HEAD's corpus + the sibling library"
# =============================================================================
say "An author's working store: the commons as committed, plus the library"
say "they are writing. It is the SOURCE that resolve fetches from, and the"
say "reference the cross-store hashes are compared against."
src=$(new_corpus_store)
export OATH_STORE="$src"
oath_run put "$here/lib.oath" --new
indent "$OUT"
has "the library's ordering dictionary passes its laws"   "prop it-is-irreflexive        passed" "$OUT"
has "the library's sort passes its ordering law"          "prop a-pair-comes-out-ordered passed" "$OUT"
has "the renderer's strongest law passes — the item is recoverable at an offset" \
    "prop the-item-survives-at-a-known-offset passed" "$OUT"
has "the numbering pass passes its depth-2 law"           "prop the-second-line-is-two   passed" "$OUT"
oath_run put "$here/main.oath" --new
indent "$OUT"
has "and the CLI's own three laws pass, against the full corpus" \
    "prop two-args-come-out-sorted-and-separated passed" "$OUT"

# THE REFERENCE HASHES, read out of the store they were put into.
ref_str_order=$(store_hash "$src" str-order)
ref_sort_strs=$(store_hash "$src" sort-strs)
ref_number_line=$(store_hash "$src" number-line)
ref_number_lines=$(store_hash "$src" number-lines)
ref_numbered_args=$(store_hash "$src" numbered-args)
say ""
say "  reference hashes, against HEAD's corpus:"
say "    str-order      #$(short "$ref_str_order")"
say "    sort-strs      #$(short "$ref_sort_strs")"
say "    number-line    #$(short "$ref_number_line")"
say "    number-lines   #$(short "$ref_number_lines")"
say "    numbered-args  #$(short "$ref_numbered_args")"

say ""
say "--- REUSE, NOT COPY: the CLI's resolved dependencies, against HEAD's names.json"
oath_run explain numbered-args
deps=$(printf '%s\n' "$OUT" | sed -n '/^DEPENDENCIES/,/^$/p')
indent "$deps"
has "the body's join resolves to the COMMITTED join-with object" \
    "join-with #$(short "$(committed_hash join-with)")" "$deps"
has "the law's str-lt resolves to the COMMITTED object"    "str-lt #$(short "$(committed_hash str-lt)")" "$deps"
has "the law's str-append resolves to the COMMITTED object" "str-append #$(short "$(committed_hash str-append)")" "$deps"
has "and the sibling helpers resolve to the library objects" "sort-strs #$(short "$ref_sort_strs")" "$deps"

# =============================================================================
head2 "STEP 5 — RESOLVE / LOCK / PUT lib.oath INTO A STORE THAT STARTS EMPTY"
# =============================================================================
target=$(new_empty_store)
export OATH_STORE="$target"
checks=$((checks + 1))
if [ -z "$(ls -A "$target")" ]; then printf '  [ok]   %s\n' "the target store starts empty — nothing in it at all"
else failures=$((failures + 1)); printf '  [FAIL] %s\n' "the target store was not empty" >&2; fi

lib_lock="$scratch/lib.oath.lock"
oath_run resolve "$here/lib.oath" --from "$src" -o "$lib_lock"
indent "$OUT"
has "resolve pins 12 external names and fetches a 15-object closure" \
    "resolved and fetched 12 external name(s) across 15 object(s)" "$OUT"
indent "$(cat "$lib_lock")"

eq "the library's lock pins exactly the corpus names it uses, and no others" \
   "List Str length range show-nat sort-by str-append str-drop str-len str-lt str-prefix zip-with " \
   "$(lock_names "$lib_lock")"
for n in length range show-nat sort-by str-append str-drop str-len str-lt str-prefix List Str; do
  pinned_to "  $n is pinned to the hash HEAD binds it to" "$lib_lock" "$n" "$(committed_hash "$n")"
done
say ""
say "  ELEVEN of the twelve are checked against HEAD's names.json above — the"
say "  eleven the consumer reuses. Nothing in this script writes a hash down."

oath_run put --lock "$lib_lock" "$here/lib.oath" --new
indent "$OUT"
eq "str-order gets the SAME hash in the empty-started store"    "$ref_str_order"    "$(store_hash "$target" str-order)"
eq "sort-strs gets the SAME hash"                               "$ref_sort_strs"    "$(store_hash "$target" sort-strs)"
eq "number-line gets the SAME hash"                             "$ref_number_line"  "$(store_hash "$target" number-line)"
eq "number-lines gets the SAME hash"                            "$ref_number_lines" "$(store_hash "$target" number-lines)"

# --- THE OUTPUT OF ONE RESOLVE IS NOT THE INPUT OF THE NEXT ------------------
# ASSERTED HERE AND NOWHERE ELSE, because the window is one step wide: after
# STEP 6 the target holds join-with and this can no longer be observed.
say ""
say "--- AND WHAT resolve JUST BUILT IS NOT ENOUGH TO BUILD THE REST"
say "The obvious next move is to resolve main.oath from the store resolve has"
say "just populated. It fails, and the failure is a finding rather than a bug:"
say "resolve fetches the closure of the FILE IT WAS GIVEN, so this target holds"
say "lib.oath's dependencies and nothing else — and main.oath calls a corpus"
say "operation lib.oath never mentions."
checks=$((checks + 1))
if [ -z "$(name_hash "$target/names.json" join-with)" ]; then
  printf '  [ok]   %s\n' "join-with is NOT bound in the library-only target — the reason it fails"
else
  failures=$((failures + 1))
  printf '  [FAIL] %s\n' "join-with was already bound in the target; the arm below proves nothing" >&2
fi
probe=$(new_empty_store)
probe_lock="$scratch/probe.oath.lock"
export OATH_STORE="$probe"
oath_fails resolve "$here/main.oath" --from "$target" -o "$probe_lock"
indent "$OUT"
has "resolving main.oath from the library-only store FAILS, and names the missing operation" \
    'unknown name "join-with"' "$OUT"
# WROTE NOTHING is asserted as three separate absences, because the store's own
# open() creates empty meta/ and objects/ directories — so a bare "the directory
# is untouched" would be false, and `ls` of a MISSING directory counts zero just
# like an empty one. Present-and-empty is the exact claim.
checks=$((checks + 1))
if [ -d "$probe/objects" ] && [ -z "$(ls -A "$probe/objects")" ]; then
  printf '  [ok]   %s\n' "the failed resolve left the probe's object store present and EMPTY"
else
  failures=$((failures + 1))
  printf '  [FAIL] %s\n' "the failed resolve wrote objects into the probe target" >&2
fi
# meta/ is the third of the "wrote nothing" absences the comment above names; a
# failed resolve that dropped a verdict file here would falsify §2 while objects
# and names stayed clean. open() creates it empty, so the claim is empty-not-absent.
checks=$((checks + 1))
if [ ! -d "$probe/meta" ] || [ -z "$(ls -A "$probe/meta" 2>/dev/null)" ]; then
  printf '  [ok]   %s\n' "  ...and wrote no metadata: probe/meta is absent or present-and-EMPTY"
else
  failures=$((failures + 1))
  printf '  [FAIL] %s\n' "the failed resolve wrote metadata into the probe target" >&2
fi
absent() { checks=$((checks + 1)); if [ ! -e "$2" ]; then printf '  [ok]   %s\n' "$1"
           else failures=$((failures + 1)); printf '  [FAIL] %s\n         exists: %s\n' "$1" "$2" >&2; fi; }
absent "  ...bound no names — there is no names.json at all" "$probe/names.json"
absent "  ...and wrote no lockfile: a failed resolve produces no artifact" "$probe_lock"
say ""
say "  So the two resolves cannot be chained, and STEP 6 does not try: it points"
say "  at the COMBINED source built in STEP 4. Assembling that store is a manual"
say "  step with no tool support — which is the finding, not this refusal."
export OATH_STORE="$target"

# =============================================================================
head2 "STEP 6 — RESOLVE / LOCK / PUT main.oath INTO THE SAME, NOW NON-EMPTY, STORE"
# =============================================================================
say "The target is no longer empty: it holds the library and its closure. This"
say "is the second resolve into a store that already has objects in it, and the"
say "lockfile it produces is the MIXTURE the two-file split exists to produce."
main_lock="$scratch/main.oath.lock"
# METADATA PRESERVATION (§2's "no metadata loss" claim, witnessed rather than
# observed): the target already holds the library closure and its verdicts.
# Snapshot every meta file now; after the second resolve merges main.oath's
# closure in, assert each pre-existing meta object survives byte-for-byte — a
# StoreObject that overwrote instead of merged would drop or rewrite one.
meta_before="$scratch/meta-before"; rm -rf "$meta_before"; mkdir -p "$meta_before"
[ -d "$target/meta" ] && cp -R "$target/meta/." "$meta_before/" 2>/dev/null
oath_run resolve "$here/main.oath" --from "$src" -o "$main_lock"
indent "$OUT"
has "resolve pins 8 external names and fetches a 20-object closure" \
    "resolved and fetched 8 external name(s) across 20 object(s)" "$OUT"
indent "$(cat "$main_lock")"

eq "the CLI's lock pins corpus names AND sibling names, side by side" \
   "List Str join-with number-line number-lines sort-strs str-append str-lt " \
   "$(lock_names "$main_lock")"
say ""
say "  COVERAGE, checked against two different authorities:"
for n in join-with str-append str-lt List Str; do
  pinned_to "  corpus  $n -> the hash HEAD binds it to" "$main_lock" "$n" "$(committed_hash "$n")"
done
pinned_to "  sibling sort-strs    -> the library object" "$main_lock" sort-strs    "$ref_sort_strs"
pinned_to "  sibling number-lines -> the library object" "$main_lock" number-lines "$ref_number_lines"
pinned_to "  sibling number-line  -> the library object" "$main_lock" number-line  "$ref_number_line"
say ""
say "  Neither authority could have supplied the other's half: HEAD's names.json"
say "  has never heard of sort-strs, and the library store's binding for"
say "  join-with is only there because resolve fetched it."

checks=$((checks + 1))
meta_lost=""
for f in "$meta_before"/*; do
  [ -e "$f" ] || continue          # empty snapshot dir: the glob is literal
  b=$(basename "$f")
  if [ ! -f "$target/meta/$b" ] || ! cmp -s "$f" "$target/meta/$b"; then
    meta_lost="$meta_lost $b"
  fi
done
if [ -n "$meta_lost" ]; then
  failures=$((failures + 1))
  printf '  [FAIL] %s\n         changed or dropped:%s\n' \
    "the second resolve lost or rewrote pre-existing metadata" "$meta_lost" >&2
elif [ -z "$(ls -A "$meta_before" 2>/dev/null)" ]; then
  failures=$((failures + 1))
  printf '  [FAIL] %s\n' "no metadata was snapshot before the resolve — the check witnessed nothing" >&2
else
  printf '  [ok]   %s\n' \
    "the second resolve preserved every pre-existing meta object byte-for-byte — no metadata loss"
fi

oath_run put --lock "$main_lock" "$here/main.oath" --new
indent "$OUT"
eq "numbered-args gets the SAME hash as against the full corpus" \
   "$ref_numbered_args" "$(store_hash "$target" numbered-args)"
say ""
say "  That is the invariant the whole exercise is about: identity is a function"
say "  of the closure, not of the store. Five definitions, two stores, one"
say "  seeded from a commit and one from nothing, same five hashes."

# =============================================================================
head2 "STEP 7 — RUN IT, AND ASSERT THE OUTPUT — INTERPRETED AND COMPILED"
# =============================================================================
# OUTPUT IS COMPARED AS BYTES, THROUGH FILES. Command substitution strips every
# trailing newline from both sides, so a `$(...)` comparison would pass a backend
# that dropped, added or duplicated the final \n while still calling itself
# byte-for-byte. cmp does not.
out="$scratch/out"; mkdir -p "$out"
bytes_eq() {  # bytes_eq <label> <expected-with-escapes> <file>
  checks=$((checks + 1))
  if printf "$2" | cmp -s - "$3"; then printf '  [ok]   %s\n' "$1"
  else failures=$((failures + 1))
    printf '  [FAIL] %s\n         want: %s\n         got:  %s\n' "$1" \
      "$(printf "$2" | od -c | tr '\n' ' ')" "$(od -c < "$3" | tr '\n' ' ')" >&2
  fi
}
interp() {  # interp <outfile> <args...>
  f=$1; shift
  if ! "$oath" run numbered-args ${1+-- "$@"} > "$f" 2>"$out/err"; then
    echo "SETUP FAILED: oath run numbered-args -- $*" >&2
    sed 's/^/    /' "$out/err" >&2; exit 1
  fi
}
interp "$out/three" gamma alpha beta
bytes_eq "oath run: three arguments come back sorted and numbered" '1. alpha\n2. beta\n3. gamma\n' "$out/three"
interp "$out/one" solo
bytes_eq "oath run: one argument is line one"                      '1. solo\n' "$out/one"
interp "$out/none"
bytes_eq "oath run: no arguments is empty output"                  '\n' "$out/none"
interp "$out/eleven" k j i h g f e d c b a
bytes_eq "oath run: eleven arguments cross the single-digit boundary" \
  '1. a\n2. b\n3. c\n4. d\n5. e\n6. f\n7. g\n8. h\n9. i\n10. j\n11. k\n' "$out/eleven"

say ""
bin="$scratch/numbered-args"
oath_run build numbered-args -o "$bin" --backend go
indent "$OUT"
for label_args in "three:gamma alpha beta" "one:solo" "eleven:k j i h g f e d c b a"; do
  lbl=${label_args%%:*}; argv=${label_args#*:}
  # shellcheck disable=SC2086
  if ! "$bin" $argv > "$out/$lbl-native" 2>"$out/err"; then
    echo "SETUP FAILED: the built binary did not run ($lbl)" >&2
    sed 's/^/    /' "$out/err" >&2; exit 1
  fi
  checks=$((checks + 1))
  if cmp -s "$out/$lbl" "$out/$lbl-native"; then
    printf '  [ok]   %s\n' "the Go-BUILT binary agrees with the interpreter on $lbl, byte for byte"
  else
    failures=$((failures + 1))
    printf '  [FAIL] %s\n' "compiled binary and interpreter disagree on $lbl" >&2
    cmp "$out/$lbl" "$out/$lbl-native" >&2 || true
  fi
done
if ! "$bin" > "$out/none-native" 2>"$out/err"; then
  echo "SETUP FAILED: the built binary did not run (no args)" >&2
  sed 's/^/    /' "$out/err" >&2; exit 1
fi
checks=$((checks + 1))
if cmp -s "$out/none" "$out/none-native"; then
  printf '  [ok]   %s\n' "the Go-BUILT binary agrees with the interpreter on no arguments"
else
  failures=$((failures + 1)); printf '  [FAIL] %s\n' "compiled and interpreted disagree on no arguments" >&2
fi

# =============================================================================
head2 "STEP 8 — THE REPRODUCTION EDGE: A LOCK CANNOT HYDRATE A STORE"
# =============================================================================
say "A lockfile carries every hash needed to rebuild this program — the direct"
say "dependencies AND the full transitive closure. It reads like a manifest a"
say "third party could hand to a fresh store. IT IS NOT ONE, and the reason is"
say "structural rather than an oversight:"
say ""
say "    put --lock only VERIFIES. verifyLock recomputes the source's external"
say "    set against the TARGET store and demands an exact match; there is no"
say "    fetch path in it, so an empty store fails classification before the"
say "    lock is even compared."
say ""
say "    resolve only accepts SOURCE. Its flags are --from, --remote, --key, -o."
say "    A lock is an output, never an input."
say ""
say "So the two halves of reproduction live in different commands and neither"
say "closes the loop. Both refusals are asserted below, then the workaround."
say ""
fresh=$(new_empty_store)
export OATH_STORE="$fresh"

oath_fails put --lock "$main_lock" "$here/main.oath" --new
indent "$OUT"
has "put --lock REFUSES in an empty store: it verifies, it does not fetch" \
    "the store cannot resolve this file's dependencies" "$OUT"
lacks "  ...and it does not silently put anything" "✓ numbered-args" "$OUT"
# Output-absence is not state-absence: witness the fresh store itself, the way
# STEP 5 witnesses the failed resolve. verifyLock rejects before elaboration, so
# nothing should have reached the store — assert that, do not infer it.
absent "  ...bound no names: the fresh store has no names.json" "$fresh/names.json"
checks=$((checks + 1))
if [ ! -d "$fresh/objects" ] || [ -z "$(ls -A "$fresh/objects" 2>/dev/null)" ]; then
  printf '  [ok]   %s\n' "  ...put no objects: the fresh store is present and EMPTY"
else
  failures=$((failures + 1))
  printf '  [FAIL] %s\n' "put --lock wrote objects into the fresh store before failing" >&2
fi
say ""
oath_fails resolve --lock "$main_lock"
indent "$OUT"
has "resolve REFUSES a lock as input, by name"        "has no flag \"--lock\"" "$OUT"
has "  ...and says what it does accept, rather than ignoring the flag" \
    "Known flags for resolve: --from --key --remote -o" "$OUT"
say ""
say "  Note the second refusal is deliberate and good: silently ignoring an"
say "  unknown flag would have let this command do something other than what"
say "  was asked. The gap is that there is no command to ask instead."
say ""
say "--- THE WORKAROUND, and it requires the SOURCE STORE, not the lock:"
oath_run resolve "$here/lib.oath" --from "$src" -o "$scratch/lib2.lock"
oath_run put --lock "$scratch/lib2.lock" "$here/lib.oath" --new
oath_run resolve "$here/main.oath" --from "$src" -o "$scratch/main2.lock"
oath_run put --lock "$scratch/main2.lock" "$here/main.oath" --new
indent "$OUT"
eq "re-resolved from the source: str-order is identical"     "$ref_str_order"     "$(store_hash "$fresh" str-order)"
eq "re-resolved from the source: sort-strs is identical"     "$ref_sort_strs"     "$(store_hash "$fresh" sort-strs)"
eq "re-resolved from the source: number-line is identical"   "$ref_number_line"   "$(store_hash "$fresh" number-line)"
eq "re-resolved from the source: number-lines is identical"  "$ref_number_lines"  "$(store_hash "$fresh" number-lines)"
eq "re-resolved from the source: numbered-args is identical" "$ref_numbered_args" "$(store_hash "$fresh" numbered-args)"
eq "and the regenerated lock is byte-identical to the first" \
   "$(shasum < "$main_lock")" "$(shasum < "$scratch/main2.lock")"
say ""
say "  The hashes are reproducible; the lockfile alone does not reproduce them."
say "  A consumer who has the lock and not the store has a checkable record of"
say "  what it SHOULD get and no way to get it — which is a real limit on"
say '  reproducibility, and is exactly what an added `resolve --lock` (or a'
say "  fetch path in put --lock) would close."

# =============================================================================
head2 "STEP 9 — THE COMMITTED TREE IS WHERE IT STARTED"
# =============================================================================
tree_after=$(tree_sig)
eq "codebase/ and fixtures/ are byte-for-byte where they started" "$tree_before" "$tree_after"

# =============================================================================
printf '\n##########\n'
# COMPLETENESS GUARD (CLAUDE.md: assert the final count so a skipped or deleted
# assertion block cannot exit green with fewer checks than documented). Pinned
# at 8a379e1; bump deliberately when adding or removing an assertion.
expected_checks=162
if [ "$checks" -ne "$expected_checks" ]; then
  printf 'INCOMPLETE — ran %d checks, expected %d: an assertion block was skipped or added without updating the guard\n' \
    "$checks" "$expected_checks" >&2
  exit 1
fi
if [ "$failures" -eq 0 ]; then
  printf 'ALL CHECKS PASSED — %d checks, 0 failures\n' "$checks"
  exit 0
fi
printf 'FAILED — %d checks, %d failures\n' "$checks" "$failures" >&2
exit 1
