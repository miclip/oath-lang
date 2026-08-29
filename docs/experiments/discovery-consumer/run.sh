#!/bin/sh
# A CONSUMER OF THE COMMONS, END TO END.
#
# The claim under test is not "oath find works". It is narrower and harder:
#
#     a program that NEEDS list reversal and delimiter joining can find the
#     committed, PROVEN implementations of both without knowing their names,
#     and then REUSE those objects rather than copy their bodies.
#
# Every step below is asserted, not printed. The script exits 0 only if all of
# them held; a setup failure aborts loudly rather than falling through to a
# summary line that would look like a pass.
#
# THE COMMITTED STORE AND FIXTURES ARE NOT TOUCHED. Everything runs against
# throwaway stores extracted from `HEAD:codebase` — the commit, not the
# worktree, because "the committed corpus" is a claim about the commit — and the
# last check re-hashes codebase/ and fixtures/ to prove they are where they
# started. `put --new` is used in those
# copies deliberately: in a throwaway store a new name is a reconstruction, not
# a publication decision, so the guard on fresh bindings does not apply.
set -eu

root=$(git rev-parse --show-toplevel)
here=$(cd "$(dirname "$0")" && pwd)
oath="$root/oath/oath"

checks=0
failures=0

# --- harness -----------------------------------------------------------------
# STATUS COMES FROM THE COMMAND, never from a pipeline: `oath ... | sed` reports
# sed's status, so under `set -e` a broken kernel would sail through printing
# nothing. Output is captured and inspected afterwards.
say()  { printf '%s\n' "$*"; }
head2() { printf '\n########## %s\n' "$*"; }

oath_run() {  # oath_run <args...>  -> OUT
  if ! OUT=$("$oath" "$@" 2>&1); then
    printf 'SETUP FAILED: oath %s\n' "$*" >&2
    printf '%s\n' "$OUT" | sed 's/^/    /' >&2
    exit 1
  fi
}

# section <property-name> <report> — the slice of a find report belonging to ONE
# property. Without it, "str-join provably satisfies it" anywhere in the output
# satisfies an assertion about EITHER law, so a report that proved one law and
# skipped the other would pass both checks.
section() { printf '%s\n' "$2" | awk -v want="  · $1" '
  function isHead() {
    return substr($0, 1, length(want)) == want &&
           (length($0) == length(want) || substr($0, length(want) + 1, 1) == " ")
  }
  isHead() { on = 1; next }        # --spec headers carry a trailing hash, so the
  /^  · / { on = 0 }               # match is a prefix and not an equality
  on { print }'; }

pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
fail() { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }

has()   { checks=$((checks + 1)); case "$3" in *"$2"*) printf '  [ok]   %s\n' "$1";;
          *) failures=$((failures + 1)); printf '  [FAIL] %s\n         expected to contain: %s\n' "$1" "$2" >&2;; esac; }
lacks() { checks=$((checks + 1)); case "$3" in *"$2"*) failures=$((failures + 1));
            printf '  [FAIL] %s\n         expected NOT to contain: %s\n' "$1" "$2" >&2;;
          *) printf '  [ok]   %s\n' "$1";; esac; }
eq()    { checks=$((checks + 1)); if [ "$2" = "$3" ]; then printf '  [ok]   %s\n' "$1";
          else failures=$((failures + 1)); printf '  [FAIL] %s\n         want: [%s]\n         got:  [%s]\n' "$1" "$2" "$3" >&2; fi; }

# committed_hash <name> — the AUTHORITY for what a name resolves to in the
# committed corpus is names.json AT HEAD, not this script, not a constant
# written into it, and not the worktree copy.
committed_hash() {
  h=$(cd "$root" && git show HEAD:codebase/names.json |
      sed -n "s/^[[:space:]]*\"$1\"[[:space:]]*:[[:space:]]*\"\([0-9a-f]\{64\}\)\".*$/\1/p")
  [ -n "$h" ] || { echo "SETUP FAILED: no committed hash for $1" >&2; exit 1; }
  printf '%s' "$h"
}
short() { printf '%s' "$1" | cut -c1-12; }

# CLEANUP. new_store is called through command substitution, so it runs in a
# SUBSHELL: a shell variable it sets is discarded and the trap would remove
# nothing, leaking a full corpus copy per run while every check still passes. A
# FILE crosses the subshell boundary, so the ledger is a file — and it is read
# back a LINE AT A TIME rather than word-split, because a TMPDIR containing a
# space would otherwise turn `rm -rf` loose on a path prefix.
stores_ledger=$(mktemp)
cleanup() {
  [ -f "$stores_ledger" ] || return 0
  while IFS= read -r s; do [ -n "$s" ] && rm -rf "$s"; done < "$stores_ledger"
  rm -f "$stores_ledger"
}
trap cleanup EXIT
# THE CORPUS COMES FROM HEAD, NOT FROM THE WORKTREE. Copying codebase/ would
# copy whatever is staged or half-edited there and then report it as "the
# committed corpus" — the authority for that phrase is the commit, so it is read
# from the commit. It also means this runs unchanged on a dirty tree.
new_store() {  # a fresh copy of the corpus AT HEAD, recorded for cleanup
  s=$(mktemp -d)
  ( cd "$root" && git archive HEAD:codebase ) | ( cd "$s" && tar -x ) || {
    echo "SETUP FAILED: could not extract codebase/ from HEAD" >&2; exit 1; }
  printf '%s\n' "$s" >> "$stores_ledger"
  printf '%s' "$s"
}

# PREREQUISITE, PROBED RATHER THAN ASSUMED. This script builds the kernel from
# source and compiles the consumer with the Go backend; both need the Go
# toolchain, so there is no meaningful "skip the build" path — saying so here is
# more honest than a later fallback branch that `make build` has already made
# unreachable.
command -v go >/dev/null 2>&1 || {
  echo "SETUP FAILED: no 'go' on PATH — this script builds the kernel (make build)" >&2
  echo "              and compiles the consumer with the Go backend. Install Go >= 1.25." >&2
  exit 1
}
( cd "$root" && make build >/dev/null )
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
# So hash the ACTUAL WORKING-TREE BYTES of every file under the two trees —
# tracked and untracked alike — which is what byte-for-byte identity means. A
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

store=$(new_store)
export OATH_STORE="$store"

# =============================================================================
head2 "THE NEED"
# =============================================================================
cat <<'NEED'
  A command-line program, entry protocol (-> (List Str) Str):

      $ args-in-reverse alpha beta gamma
      gamma,beta,alpha

  Required to satisfy three laws:
      no-args-is-empty                    ()      -> ""
      one-arg-is-echoed                   (s)     -> s
      two-args-are-swapped-and-separated  (a b)   -> b ++ "," ++ a

  It is two capabilities short, and the author knows NEITHER NAME:
      1. reverse a list
      2. join a list of strings with a delimiter

  Everything below asks the commons for those two by what they DO.
NEED

# =============================================================================
head2 "STEP 1 — PROBE THE SHAPES (before writing a single law)"
# =============================================================================
say "The cheapest move available: a law that states nothing, so --spec falls"
say "through to listing every definition at that SIGNATURE, with its guarantee."
say ""
say "--- (-> (List a) (List a)), the reversal shape"
oath_run find --spec "$here/probe-list-shape.oath"
printf '%s\n' "$OUT" | sed 's/^/    /'
has "the probe surfaces a PROVEN candidate at the list shape" "reverse            PROVEN" "$OUT"
has "and surfaces the FALSIFIED twin beside it, labelled" "bad-reverse        FALSIFIED" "$OUT"
say ""
say "--- (-> Int (List Str) Str), the joining shape"
oath_run find --spec "$here/probe-join-shape.oath"
printf '%s\n' "$OUT" | sed 's/^/    /'
has "the probe surfaces str-join, PROVEN" "str-join           PROVEN" "$OUT"
say ""
say "  Read that second one carefully: the delimiter is an Int, not a Str. The"
say "  probe answered a question the author had not thought to ask — Str is an"
say "  inductive datatype of codepoints, so a delimiter is one codepoint."

# =============================================================================
head2 "STEP 2 — THE REVERSAL NEED, FIRST ATTEMPT (it finds nothing)"
# =============================================================================
say "The CLI's arguments are (List Str), so the obvious query is monomorphic."
oath_run find --implies "$here/need-reverse-monomorphic.oath" --details
printf '%s\n' "$OUT" | sed 's/^/    /'
lacks "the monomorphic query finds NOTHING — no definition is proved to satisfy it" "provably satisfies it" "$OUT"
lacks "  ...and reverse in particular is never named" "reverse " "$OUT"
has "and it is not silent: exactly 3 definitions are REFUTED" "3 REFUTED" "$OUT"
checks=$((checks + 1))
# 'countermodel (' matches an EVIDENCE ROW — "countermodel (by evaluation):" or
# "countermodel (solver):" — and not the summary line's "(a countermodel exists)".
ncm=$(printf '%s\n' "$OUT" | grep -c 'countermodel (')
if [ "$ncm" = 3 ]; then printf '  [ok]   %s\n' "each refutation carries a countermodel (3 of 3)"
else failures=$((failures + 1)); printf '  [FAIL] %s\n' "expected 3 countermodel rows, got $ncm" >&2; fi
say ""
say "  Nothing polymorphic can be admitted: the re-typed property would have to"
say "  pass a type argument the candidate does not take, so every forall-a"
say "  definition is rejected before the prover is asked. The corpus's list"
say "  combinators are all forall a. THE QUERY'S SHAPE IS THE BUG, not the corpus."

# =============================================================================
head2 "STEP 3 — THE REVERSAL NEED, RESTATED POLYMORPHICALLY"
# =============================================================================
say "--- surface A: --spec, a content-hash lookup (no proof, no solver)"
oath_run find --spec "$here/need-reverse.oath"
printf '%s\n' "$OUT" | sed 's/^/    /'
spec_inv=$(section involution "$OUT")
spec_anti=$(section antidistributes-over-append "$OUT")
[ -n "$spec_inv" ] && [ -n "$spec_anti" ] || { echo "SETUP FAILED: could not slice the --spec report by property" >&2; exit 1; }
has "--spec matches reverse on involution, by content hash alone"  "reverse            (proven as \"involution\")" "$spec_inv"
has "--spec matches reverse on antidistribution too"               "reverse            (proven as \"antidistributes-over-append\")" "$spec_anti"
# Finding #1: --spec renders the FALSIFIED twin as "tested as", indistinguishable
# from a merely-tested pass. Assert the exact mark, not just the name — if --spec
# is ever fixed to expose the refutation, these fail and force the log's headline
# finding to be re-checked instead of silently going stale.
has "--spec ALSO returns the falsified twin under involution, marked \"tested as\" (finding #1)"      "bad-reverse        (tested as \"involution\")" "$spec_inv"
has "--spec ALSO returns it under antidistribution, marked \"tested as\" (finding #1)"                "bad-reverse        (tested as \"antidistributes-over-append\")" "$spec_anti"
say ""
say "  Both are returned, and the hash surface cannot rank them — it matched a"
say "  law, and bad-reverse genuinely STATES that law. What separates them is"
say "  the guarantee, and, below, a proof."
say ""
say "--- surface B: --implies, one Z3 proof per candidate"
oath_run find --implies "$here/need-reverse.oath" --details
printf '%s\n' "$OUT" | sed 's/^/    /'
sec_inv=$(section involution "$OUT")
sec_anti=$(section antidistributes-over-append "$OUT")
[ -n "$sec_inv" ] && [ -n "$sec_anti" ] || { echo "SETUP FAILED: could not slice the report by property" >&2; exit 1; }
has "reverse is PROVEN to satisfy involution"            "reverse            ← provably satisfies it" "$sec_inv"
has "reverse is PROVEN to satisfy antidistribution too"  "reverse            ← provably satisfies it" "$sec_anti"
has "bad-reverse SURVIVES involution — the identity function is one" "bad-reverse        ← provably satisfies it" "$sec_inv"
has "bad-reverse is REFUTED on antidistribution, with a countermodel" "bad-reverse        countermodel" "$sec_anti"
lacks "and no candidate was left unsettled — no NO VERDICT in this search" "NO VERDICT" "$OUT"
say ""
say "  The identity function IS an involution, so bad-reverse survives the first"
say "  law and is disproved by the second — with concrete lists, not a label."
say "  A refutation is a finding: the commons said which definition is wrong."
say ""
lacks "no candidate was admitted CROSS-TYPE in this search" "cross-type" "$OUT"
say ""
say "  CROSS-TYPE, and the claim kept to what is decidable: --implies re-types a"
say "  query to a candidate by substituting PRIMITIVE leaves (int/rat/float/bool"
say "  only). THIS query's signature, (-> (List a) (List a)), contains no"
say "  primitive leaf at all — Str and List are datatypes — so no substitution"
say "  exists and no candidate CAN be admitted cross-type here. That half is a"
say "  fact about the signature. The joining query in step 4 is different, and"
say "  is discussed there."

# =============================================================================
head2 "STEP 4 — THE JOINING NEED"
# =============================================================================
say "The laws below were written from the CLI's requirement, NOT copied from"
say "str-join — which states a different law (joining one piece returns it)."
say ""
say "--- surface A: --spec MISSES, because a paraphrase has a different hash"
oath_run find --spec "$here/need-join.oath"
printf '%s\n' "$OUT" | sed 's/^/    /'
spec_empty=$(section empty-joins-to-empty "$OUT")
spec_two=$(section two-pieces-are-separated "$OUT")
[ -n "$spec_empty" ] && [ -n "$spec_two" ] || { echo "SETUP FAILED: could not slice the --spec report by property" >&2; exit 1; }
has "--spec finds no content-hash match for the empty-list law"  "no definition states this law as written" "$spec_empty"
has "--spec finds no content-hash match for the two-piece law"   "no definition states this law as written" "$spec_two"
has "the empty-list law's signature fallback names str-join, PROVEN" "str-join           PROVEN" "$spec_empty"
has "the two-piece law's fallback names it too"                     "str-join           PROVEN" "$spec_two"
say ""
say "--- surface B: --implies proves both laws against a body that never claimed them"
oath_run find --implies "$here/need-join.oath" --details
printf '%s\n' "$OUT" | sed 's/^/    /'
sec_empty=$(section empty-joins-to-empty "$OUT")
sec_two=$(section two-pieces-are-separated "$OUT")
[ -n "$sec_empty" ] && [ -n "$sec_two" ] || { echo "SETUP FAILED: could not slice the report by property" >&2; exit 1; }
has "str-join is proved to satisfy empty-joins-to-empty"    "str-join           ← provably satisfies it" "$sec_empty"
has "str-join is proved to satisfy two-pieces-are-separated" "str-join           ← provably satisfies it" "$sec_two"
lacks "nothing was refuted in this search" "REFUTED" "$OUT"
lacks "and nothing was left unsettled" "NO VERDICT" "$OUT"
lacks "no candidate was admitted CROSS-TYPE either — MEASURED, not assumed" "cross-type" "$OUT"
say ""
say "  CROSS-TYPE here is a different case from step 3, and the difference is"
say "  worth stating rather than papering over. This signature,"
say "  (-> Int (List Str) Str), DOES carry a primitive leaf: the delimiter. A"
say "  definition at (-> Rat (List Str) Str) or (-> Bool (List Str) Str) would"
say "  be admitted cross-type and the query re-typed to it. This corpus simply"
say "  holds no such definition — a fact about the CORPUS, not about the"
say "  mechanism, which is why the line above CHECKS the output for a cross-type"
say "  label instead of claiming none was possible."

# =============================================================================
head2 "STEP 5 — SELECT (the verdict decides, not the name)"
# =============================================================================
rev_hash=$(committed_hash reverse)
join_hash=$(committed_hash str-join)
app_hash=$(committed_hash str-append)
say "  reverse   #$(short "$rev_hash")   PROVEN on both laws        SELECTED"
say "  bad-reverse                REFUTED on antidistribution  rejected"
say "  str-join  #$(short "$join_hash")   PROVEN on both laws        SELECTED"
oath_run ls
has "the selected reverse is PROVEN in the committed corpus" "reverse          #$(short "$rev_hash")  func  PROVEN" "$OUT"
has "the selected str-join is PROVEN in the committed corpus" "str-join         #$(short "$join_hash")  func  PROVEN" "$OUT"
bad_hash=$(committed_hash bad-reverse)
has "the rejected twin is FALSIFIED in the committed corpus" "bad-reverse      #$(short "$bad_hash")  func  FALSIFIED" "$OUT"

# =============================================================================
head2 "STEP 6 — BUILD THE CONSUMER ON TOP OF THEM"
# =============================================================================
say "app.oath's body is (str-join 44 (reverse [Str] args)) — two calls. Neither"
say "algorithm is written there."
oath_run put "$here/app.oath" --new
printf '%s\n' "$OUT" | sed 's/^/    /'
has "the consumer's own three laws pass their generated tests" "prop two-args-are-swapped-and-separated passed" "$OUT"

say ""
say "--- REUSE, NOT COPY: the resolved dependency hashes, against codebase/names.json"
oath_run explain args-in-reverse
deps=$(printf '%s\n' "$OUT" | sed -n '/^DEPENDENCIES/,/^$/p')
printf '%s\n' "$deps" | sed 's/^/    /'
has "the body's reversal resolves to the COMMITTED reverse object" "reverse #$(short "$rev_hash")" "$deps"
has "the body's join resolves to the COMMITTED str-join object" "str-join #$(short "$join_hash")" "$deps"
has "the law's str-append resolves to the COMMITTED object too" "str-append #$(short "$app_hash")" "$deps"
say ""
say "  Those are the hashes in the committed corpus, read out of names.json by"
say "  this script rather than written into it. The consumer holds references."

say ""
say "--- and the consumer is itself PROVEN, not merely tested"
oath_run prove args-in-reverse
printf '%s\n' "$OUT" | sed 's/^/    /'
has "all three of the CLI's own laws are proved by Z3" "proven: 3/3 properties" "$OUT"

# =============================================================================
head2 "STEP 7 — RUN IT, AND ASSERT THE OUTPUT"
# =============================================================================
# OUTPUT IS COMPARED AS BYTES, THROUGH FILES. Command substitution strips every
# trailing newline from both sides, so a `$(...)` comparison would pass a backend
# that dropped, added or duplicated the final \n while still calling itself
# byte-for-byte. cmp does not.
out_dir="$store/.out"
mkdir -p "$out_dir"
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
  if ! "$oath" run args-in-reverse ${1+-- "$@"} > "$f" 2>"$out_dir/err"; then
    echo "SETUP FAILED: oath run args-in-reverse -- $*" >&2
    sed 's/^/    /' "$out_dir/err" >&2; exit 1
  fi
}
interp "$out_dir/three" alpha beta gamma
bytes_eq "oath run: three arguments come back reversed and comma-joined" 'gamma,beta,alpha\n' "$out_dir/three"
interp "$out_dir/one" solo
bytes_eq "oath run: one argument is echoed" 'solo\n' "$out_dir/one"
interp "$out_dir/none"
bytes_eq "oath run: no arguments is empty output" '\n' "$out_dir/none"

bin="$store/args-in-reverse"
oath_run build args-in-reverse -o "$bin"
printf '%s\n' "$OUT" | sed 's/^/    /'
if ! "$bin" alpha beta gamma > "$out_dir/three-native" 2>"$out_dir/err"; then
  echo "SETUP FAILED: the built binary did not run" >&2
  sed 's/^/    /' "$out_dir/err" >&2; exit 1
fi
checks=$((checks + 1))
if cmp -s "$out_dir/three" "$out_dir/three-native"; then
  printf '  [ok]   %s\n' "the COMPILED binary agrees with the interpreter, byte for byte"
else
  failures=$((failures + 1))
  printf '  [FAIL] %s\n' "compiled binary and interpreter disagree" >&2
  cmp "$out_dir/three" "$out_dir/three-native" >&2 || true
fi

# =============================================================================
head2 "STEP 8 — THE --equiv SWEEP OVER THE WHOLE COMMITTED CORPUS"
# =============================================================================
say "Question: does body-equivalence (the e-graph) connect ANY two definitions"
say "the committed corpus OFFERS — anything nontrivial, beyond commutativity and"
say "associativity? The sweep runs against a PRISTINE copy, so nothing this"
say "script put in the store is in the universe. What IS in it is stated below"
say "the result, where it can be read against the number."
sweep_store=$(new_store)
(
  export OATH_STORE="$sweep_store"
  "$oath" ls
) > "$sweep_store/.ls" 2>&1 || { echo "SETUP FAILED: ls on the sweep store" >&2; exit 1; }
funcs=$(awk '$3=="func"{print $1}' "$sweep_store/.ls")
nfuncs=$(printf '%s\n' "$funcs" | grep -c .)
matched=0
matches=""
for n in $funcs; do
  if ! out=$(OATH_STORE="$sweep_store" "$oath" find --equiv "$n" 2>&1); then
    echo "SETUP FAILED: find --equiv $n" >&2; printf '%s\n' "$out" | sed 's/^/    /' >&2; exit 1
  fi
  case "$out" in
    *"no other definition normalizes to the same form"*) ;;
    *) matched=$((matched + 1)); matches="$matches $n";;
  esac
done
nobjs="unknown"
[ -d "$sweep_store/objects" ] && nobjs=$(ls "$sweep_store/objects" | grep -c .)
nhashes=$(awk '$3=="func"{print $2}' "$sweep_store/.ls" | sort -u | grep -c .)
say ""
say "  swept: $nfuncs function NAMES, resolving to $nhashes distinct objects"
say "  definitions with an equivalent partner: $matched$matches"
eq "no live name in the committed corpus reaches a body-equivalent pair" "0" "$matched"
say ""
say "  THE UNIVERSE, stated exactly, because zero over the wrong set is worthless:"
say "  this sweeps every FUNCTION NAME the committed store resolves, which is what"
say "  a consumer of the commons can reach. The store holds $nobjs objects in all —"
say "  objects are immutable and repointing deletes nothing, so it also carries"
say "  superseded drafts that no live name reaches. Those are outside this sweep,"
say "  and outside \`oath find --equiv\` altogether: it is addressed by NAME."
say ""
say "  So: there is no match AT ALL among what the corpus OFFERS — not a"
say "  nontrivial one, and not a commutativity or associativity one either. A"
say "  curated corpus holds no redundant definitions, which measures the CORPUS"
say "  rather than the rule set. The controls below measure the rule set."

say ""
say "--- and zero is what a BROKEN sweep prints too, so: the controls"
oath_run put "$here/equiv-controls.oath" --new
printf '%s\n' "$OUT" | sed 's/^/    /'
ask_equiv() {  # ask_equiv <name> <yes|no> <label>
  if ! out=$("$oath" find --equiv "$1" 2>&1); then
    echo "SETUP FAILED: find --equiv $1" >&2; printf '%s\n' "$out" | sed 's/^/    /' >&2; exit 1
  fi
  case "$out" in *"no other definition normalizes to the same form"*) got=no;; *) got=yes;; esac
  if [ "$got" = "$2" ]; then pass "$3"; else fail "$3 (wanted $2, got $got)"; fi
  EQUIV_OUT=$out
}
ask_equiv ctl-delimiter-plus-zero yes "x+0 delimiter is connected to the CLI (identity element — beyond comm/assoc)"
has "  ...and the partner it names is the CLI itself" "args-in-reverse" "$EQUIV_OUT"
ask_equiv ctl-delimiter-times-one yes "x*1 delimiter is connected to the CLI (identity element)"
ask_equiv ctl-delimiter-other     no  "a DIFFERENT delimiter is correctly NOT connected"
ask_equiv ctl-distributed-factored yes "a*(b+c) is connected to a*b+a*c (distributivity, the least trivial rule)"
has "  ...to the expanded form specifically" "ctl-distributed-expanded" "$EQUIV_OUT"
say ""
say "  The instrument discriminates: it connects four definitions it should and"
say "  refuses one it should not. The corpus sweep's zero is therefore a fact"
say "  about the corpus, not about the sweep."

# =============================================================================
head2 "STEP 9 — THE COMMITTED TREE IS WHERE IT STARTED"
# =============================================================================
tree_after=$(tree_sig)
eq "codebase/ and fixtures/ are byte-for-byte where they started" "$tree_before" "$tree_after"

# =============================================================================
printf '\n##########\n'
if [ "$failures" -eq 0 ]; then
  printf 'ALL CHECKS PASSED — %d checks, 0 failures\n' "$checks"
  exit 0
fi
printf 'FAILED — %d checks, %d failures\n' "$checks" "$failures" >&2
exit 1
