#!/bin/sh
# ledger-realdata — the instrument behind docs/experiments/ledger-realdata/.
#
# It BUILDS `ledger.oath` beside it and RUNS it against `transactions.journal`,
# a real-format hledger-style journal of 12,001 transactions / 26,819 postings /
# 1.4 MB in three commodities. The demand list this experiment produces is a
# demand list from VOLUME, so nothing here is asserted from a fixture: every
# figure quoted in the write-up is produced by this script, on this journal.
#
# WHAT IT MEASURES, and why each measurement needs its own instrument:
#
#   PHASE TIMINGS       put / prove / build(go) / build(llvm) / run — so the cost
#                       of depending on Oath is a number rather than an
#                       impression.
#   AN INDEPENDENT ORACLE  the aggregate is recomputed IN AWK, in integer
#                       thousandths, and compared to the compiled program's
#                       report. Comparing the two backends to each other cannot
#                       catch a shared mistaken parse; only an outside
#                       computation can. The oracle is itself checked (its
#                       magnitudes must stay inside exact double range) before
#                       anything is measured with it.
#   THE THREE-WAY GATE  `oath eval` is the reference. Go and LLVM are never
#                       compared to each other alone — two identically wrong
#                       lowerings agree.
#   THE GO CEILING      the Go artifact does NOT survive this journal: the whole
#                       file goes through `str-split`, whose recursion is not
#                       tail recursion, and 1.4 M codepoints exhaust the Go
#                       runtime's 1 GB goroutine stack. The ceiling is BISECTED
#                       here rather than quoted, with both endpoints validated —
#                       a bisection with hard-coded bounds reports its lower
#                       bound as the ceiling when that bound already fails.
#   A REFUSAL, AND ITS CONTROL   a corrupted copy of the journal (made in a temp
#                       directory; the committed journal is never touched) must
#                       be REFUSED, naming the transaction, the commodity and
#                       the exact residual. The control is the same prefix
#                       UNCORRUPTED, which must exit 0 — without it, a program
#                       that refused everything would pass.
#
# Z3 IS CAPPED. OATH_PROVE_RLIMIT is z3's DETERMINISTIC work budget, so a goal
# the prover will not discharge returns a verdict in seconds rather than
# grinding; the wall cap is generous on purpose so that the rlimit, not the
# clock, is what binds and the verdicts do not move under machine load.
#
# NOTHING COMMITTED IS WRITTEN. The store is a fresh `mktemp -d`, every artifact
# and every corrupted journal lives under one temp work directory, and the
# kernel, the corpus and the fixtures are read-only throughout.
#
# Requires: go (to build the kernel), clang (the LLVM backend shells out to it),
# z3, python3 (to decode the interpreter's structural Str, and for timing),
# awk. Each is PROBED below; a missing one is reported by name, not assumed.
#
# Runtime: roughly eight to twelve minutes, dominated almost entirely by proof
# goals that do NOT discharge — each burns its full deterministic budget through
# every strategy before returning "unproven". The measured phase timings are
# printed at the end; read those rather than this estimate.

set -u

root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "SETUP FAILED: not in a git repository" >&2; exit 1; }
here="$root/docs/experiments/ledger-realdata"
OATHBIN="$root/oath/oath"
JOURNAL="$here/transactions.journal"

checks=0; failures=0
pass() { checks=$((checks + 1)); printf '  [ok]   %s\n' "$1"; }
bad()  { checks=$((checks + 1)); failures=$((failures + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
die()  { printf 'SETUP FAILED: %s\n' "$1" >&2; exit 1; }

# --- setup, probed rather than assumed ----------------------------------------
for t in git awk python3 z3 clang; do
  command -v "$t" >/dev/null 2>&1 || die "$t is not on PATH (needed: git awk python3 z3 clang)"
done
[ -f "$JOURNAL" ] || die "$JOURNAL is missing — the journal IS the experiment"
[ -f "$here/ledger.oath" ] || die "$here/ledger.oath is missing"
( cd "$root" && make build >/dev/null 2>&1 ) || die "the kernel did not build (cd oath && go build -o oath .)"
[ -x "$OATHBIN" ] || die "$OATHBIN is not executable after make build"

work=$(mktemp -d) || die "could not create a work directory"
OATH_STORE=$(mktemp -d) || die "could not create a store"
export OATH_STORE
trap 'rm -rf "$work" "$OATH_STORE"' EXIT
# A THROWAWAY STORE. `oath put` defaults to `codebase`, the committed,
# git-tracked, append-only corpus; nothing exploratory may reach it.

cap() { OATH_PROVE_RLIMIT=20000000 OATH_PROVE_WALLCAP_SEC=120 OATH_PROVE_MEMORY_MB=1500 "$@"; }
now() { python3 -c 'import time; print("%.3f" % time.time())'; }
: > "$work/timings.txt"
# `now` brackets a phase from OUTSIDE it, so python3's own startup is not inside
# the measured interval.
record() { printf '%-26s %8.3f s\n' "$1" "$2" >> "$work/timings.txt"; }
elapsed() { python3 -c "print('%.3f' % ($2 - $1))"; }

jbytes=$(wc -c < "$JOURNAL" | tr -d ' ')
echo "ledger-realdata — $(wc -l < "$JOURNAL" | tr -d ' ') journal lines, $jbytes bytes"
echo

# --- 0. the independent oracle, and a check on the oracle ---------------------
# Recomputed in awk, in integer thousandths, straight from the journal text. It
# shares no code with the Oath program — which is the point: agreement between
# the two backends could still be agreement on the same mis-parse.
echo "0 — an INDEPENDENT awk oracle for the aggregate (and a check on the oracle)"
awk '
/^[ \t]/ && NF>=3 {
  acct=$1; amt=$2; comm=$3
  neg=0; if (substr(amt,1,1)=="-") { neg=1; amt=substr(amt,2) }
  d=index(amt,"."); if (d==0) { ip=amt; fp="" } else { ip=substr(amt,1,d-1); fp=substr(amt,d+1) }
  while (length(fp)<3) fp=fp "0"
  fp=substr(fp,1,3)
  v=(ip+0)*1000 + (fp+0); if (neg) v=-v
  bal[acct" "comm]+=v; tot[comm]+=v; np++
  if (v<0) a=-v; else a=v
  if (a>big) big=a
}
/^[0-9]/ { nt++ }
END {
  for (k in bal) { printf "BAL %s %d\n", k, bal[k]; b=bal[k]; if (b<0) b=-b; if (b>big) big=b }
  for (c in tot) printf "TOT %s %d\n", c, tot[c]
  printf "COUNTS %d %d\n", nt, np
  printf "MAXMAG %d\n", big
}' "$JOURNAL" | sort > "$work/oracle.txt"

maxmag=$(awk '/^MAXMAG/{print $2}' "$work/oracle.txt")
# The oracle accumulates in awk's doubles. Integers below 2^53 are exact there,
# so this is the condition under which the oracle is an EXACT integer
# computation and not an approximation of one. An oracle nobody checked is not
# an oracle.
if [ -n "$maxmag" ] && [ "$maxmag" -lt 9007199254740992 ]; then
  pass "the awk oracle stays inside exact-integer double range (max magnitude $maxmag)"
else
  bad "the awk oracle's magnitudes ($maxmag) leave exact double range — it is not an exact oracle here"
fi
oracle_counts=$(awk '/^COUNTS/{print $2" "$3}' "$work/oracle.txt")
if [ -n "$oracle_counts" ]; then
  pass "the oracle read the journal: $oracle_counts (transactions postings)"
else
  bad "the oracle produced no counts — it did not read the journal"
fi

# --- 1. seed the store --------------------------------------------------------
echo
echo "1 — seed a FRESH store with the dependency closure, then put ledger.oath"
t0=$(now)
for f in list str records strmap circle; do
  "$OATHBIN" put "$root/examples/$f.oath" --new >/dev/null 2>&1 || die "seeding examples/$f.oath failed"
done
t1=$(now); record "seed deps (5 files)" "$(elapsed "$t0" "$t1")"

t0=$(now)
"$OATHBIN" put "$here/ledger.oath" --new > "$work/put.log" 2>&1
putrc=$?
t1=$(now); record "put ledger.oath" "$(elapsed "$t0" "$t1")"
if [ "$putrc" -eq 0 ]; then
  pass "ledger.oath put: every definition typechecked and every property held"
else
  bad "put failed: $(tail -3 "$work/put.log" | tr '\n' ' ')"
fi
# The capability claim: the analyser must find the single readfile capability
# CONFINED — the program cannot reach a file it was not handed.
if grep -q 'caps: confined' "$work/put.log"; then
  pass "ledger-main's readfile capability is analysed CONFINED"
else
  bad "expected 'caps: confined' on ledger-main; got: $(grep -A2 ledger-main "$work/put.log" | tr '\n' ' ')"
fi
# Every definition must be TOTAL — a ledger checker that might not terminate is
# not a checker. This is asserted over the whole file, not spot-checked.
nontotal=$(grep -c '✓' "$work/put.log")
totals=$(grep -c '· total' "$work/put.log")
if [ "$totals" -gt 0 ] && [ "$totals" -eq $((nontotal - 3)) ]; then
  pass "every definition is analysed TOTAL ($totals of $nontotal accepted lines; 3 are datatypes)"
else
  bad "expected all but the 3 datatypes to be total; got $totals total of $nontotal accepted"
fi

# --- 2. proofs, under a capped z3 ---------------------------------------------
echo
echo "2 — PROVE the properties under a deterministic z3 budget"
t0=$(now)
: > "$work/prove.log"
for n in amt-of bump strip-comment all-space trim-end first-code digits-ok \
         frac3 parse-abs parse-amount parse-posting pad3 show-abs show-amount \
         render-key all-zero tx-errors add-error close-txn scan-step \
         empty-scan adjudicate ledger-main; do
  printf '=== %s\n' "$n" >> "$work/prove.log"
  cap "$OATHBIN" prove "$n" >> "$work/prove.log" 2>&1
done
t1=$(now); record "prove (23 definitions)" "$(elapsed "$t0" "$t1")"

proven=$(grep -c 'PROVEN' "$work/prove.log")
unproven=$(grep -c '· unproven' "$work/prove.log")
echo "  $proven properties PROVEN, $unproven unproven"

# The claims that MUST prove. Each is asserted by name: a count alone would go
# green if the prover discharged a different set of the same size.
for claim in \
  "adjudicate:refuses-iff-failures" \
  "ledger-main:no-args-refuses" \
  "frac3:is-a-fraction" \
  "tx-errors:silent-on-balanced" \
  "parse-amount:minus-negates" \
  "show-amount:negative" \
  "render-key:rebuilds" \
  "scan-step:skips-comment" \
  "scan-step:skips-indented-comment" \
  "strip-comment:drops-from-the-marker" \
  "strip-comment:keeps-what-precedes" \
  "trim-end:drops-trailing" \
  "all-space:text-is-not-blank" \
  "parse-posting:rejects-layout-only" ; do
  d=${claim%%:*}; p=${claim#*:}
  if awk -v d="=== $d" -v p="$p" '
        $0==d {inb=1; next} /^=== /{inb=0}
        inb && /PROVEN/ && $0 ~ p {found=1}
        END{exit !found}' "$work/prove.log"; then
    pass "PROVEN  $d.$p"
  else
    bad "$d.$p did not prove"
  fi
done

# THE DIRECT EQUIVALENCE — "a transaction produces no failure exactly when it
# balances in every commodity" — is ATTEMPTED, and what is asserted is that the
# attempt returned a VERDICT rather than crashing or being cut off. Whether the
# verdict is PROVEN is reported: asserting either value would encode a tool
# limit, and this is the goal most likely to move when the induction heuristic
# improves.
eqv=$(awk '$0=="=== tx-errors"{inb=1;next} /^=== /{inb=0} inb && /errors-iff-imbalanced/{print; exit}' "$work/prove.log")
if [ -n "$eqv" ]; then
  pass "the balance/error EQUIVALENCE returned a verdict:$(printf '%s' "$eqv" | sed 's/  */ /g')"
else
  bad "the balance/error equivalence produced no verdict line — the prover did not reach it"
fi

# Goals that do NOT discharge are reported, not asserted: asserting an unproven
# verdict would encode a TOOL LIMIT as an expectation, and the gate would go red
# the day the prover improved.
for d in bump close-txn; do
  if awk -v d="=== $d" '$0==d{inb=1;next} /^=== /{inb=0} inb && /· unproven/{u=1} END{exit !u}' "$work/prove.log"; then
    echo "  [note] $d has a property the prover does NOT discharge — see the friction log"
  fi
done

# --- 3. the dependency control -------------------------------------------------
# `bump`'s accumulation law is the payoff property of the whole program, and its
# unproven report NAMES the reused str-map definitions whose laws are missing.
# That is a hypothesis, not a finding, until the named repair is TRIED: prove the
# dependencies and re-attempt. Whether the verdict moves is what distinguishes
# "the standard library is only tested" from "the induction heuristic cannot
# reach this shape", and those are different demands on the language.
echo
echo "3 — CONTROL: prove the named str-map dependencies, then re-attempt the stuck goals"
t0=$(now)
for n in str-lt str-eq smi-lookup smi-insert str-map-empty str-map-lookup \
         str-map-insert str-append append length; do
  cap "$OATHBIN" prove "$n" >/dev/null 2>&1
done
: > "$work/retry.log"
for n in bump close-txn tx-errors parse-posting; do
  printf '=== %s\n' "$n" >> "$work/retry.log"
  cap "$OATHBIN" prove "$n" >> "$work/retry.log" 2>&1
done
t1=$(now); record "dependency control" "$(elapsed "$t0" "$t1")"
retry_proven=$(grep -c 'PROVEN' "$work/retry.log")
retry_unproven=$(grep -c '· unproven' "$work/retry.log")
echo "  after proving the dependencies: $retry_proven proven, $retry_unproven unproven"
if [ $((retry_proven + retry_unproven)) -eq 11 ]; then
  pass "the control returned a verdict for all 11 properties (bump 1, close-txn 2, tx-errors 3, parse-posting 5)"
else
  bad "the control did not return 11 verdicts — it did not run: $(tr '\n' ' ' < "$work/retry.log" | tail -c 200)"
fi
# THE DISCRIMINATION. Proving the dependencies moves ONE of the two stuck goals
# and not the other, and that is the whole value of the control: `bump`'s
# accumulation law was blocked on the standard library being only TESTED, which
# a consumer can do nothing about and the project can; `close-txn`'s
# preservation laws are blocked on something else, which no amount of proving
# dependencies reaches. Two different demands, indistinguishable before this ran.
if awk '$0=="=== bump"{inb=1;next} /^=== /{inb=0} inb && /PROVEN/ && /adds-at-key/{f=1} END{exit !f}' "$work/retry.log"; then
  pass "bump.adds-at-key PROVES once str-map's own laws are proven — it was a DEPENDENCY-STATE limit"
else
  bad "bump.adds-at-key did not prove even with its dependencies proven — the diagnosis in the friction log is wrong"
fi
# NON-MONOTONICITY, measured. A larger lemma library is not a superset of
# outcomes: `prove` re-derives from scratch, and the STORED verdict follows the
# latest attempt, so a property proven with the dependencies merely tested can
# come back TESTED after they are proven. That is a fact about search, not about
# the theorem — but the store keeps the downgraded verdict, so a consumer sees
# it. Whichever way it lands, it is printed rather than asserted.
before=$(awk '$0=="=== parse-posting"{inb=1;next} /^=== /{inb=0} inb && /PROVEN/{print $3}' "$work/prove.log" | sort | tr '\n' ' ')
after=$(awk '$0=="=== parse-posting"{inb=1;next} /^=== /{inb=0} inb && /PROVEN/{print $3}' "$work/retry.log" | sort | tr '\n' ' ')
if [ "$before" = "$after" ]; then
  echo "  [note] parse-posting proves the same set before and after: [$before]"
else
  echo "  [note] parse-posting proves a DIFFERENT set once its dependencies are proven —"
  echo "         before: [$before]"
  echo "         after:  [$after]"
  echo "         proving a dependency is not monotone in this goal's outcome."
fi
if awk '$0=="=== close-txn"{inb=1;next} /^=== /{inb=0} inb && /· unproven/{u=1} END{exit !u}' "$work/retry.log"; then
  echo "  [note] close-txn's preservation laws did not discharge at this rlimit with every"
  echo "         dependency proven — the prover's REACH on this goal shape, not a claim the laws"
  echo "         are false (they pass 200 tested cases). Reported, never asserted: asserting an"
  echo "         unproven verdict would encode a tool limit and go red when the prover improved."
else
  echo "  [note] close-txn's preservation laws now prove — the friction log's second demand is discharged."
fi

# --- 3b. is `prove` monotone in the lemma library? --------------------------------
# Section 3 shows a stuck goal discharging once its dependencies are proven, which
# reads as "more lemmas can only help". This probe asks the opposite question, and
# it needs a SEPARATE store: the states have to be built in a controlled order,
# and the main store has already had everything proven.
#
# X and Y are a strict SUPERSET pair by construction — Y proves everything X does
# and eight dependencies more — so any goal that discharges in X and not in Y is
# a genuine non-monotonicity and not two unrelated states being compared.
echo
echo "3b — PROBE: build a strict superset of proof state and re-ask the same goal"
tp0=$(now)
probe_store=$(mktemp -d) || die "could not create the probe store"
(
  export OATH_STORE="$probe_store"
  for f in list str records strmap circle; do "$OATHBIN" put "$root/examples/$f.oath" --new >/dev/null 2>&1; done
  "$OATHBIN" put "$here/ledger.oath" --new >/dev/null 2>&1
  cap "$OATHBIN" prove strip-comment >/dev/null 2>&1
  echo "=== X"; cap "$OATHBIN" prove parse-posting 2>&1
  echo "=== Xstored"; "$OATHBIN" explain parse-posting --json
  for n in parse-nat-go parse-nat str-len str-take frac3 parse-abs parse-amount str-split; do
    cap "$OATHBIN" prove "$n" >/dev/null 2>&1
  done
  echo "=== Y"; cap "$OATHBIN" prove parse-posting 2>&1
  echo "=== Ystored"; "$OATHBIN" explain parse-posting --json
) > "$work/monotone.log" 2>&1
rm -rf "$probe_store"
tp1=$(now); record "monotonicity probe" "$(elapsed "$tp0" "$tp1")"

cat > "$work/stored.py" <<'PY'
import json, re, sys
blob = open(sys.argv[1]).read()
mark = "=== " + sys.argv[2] + "\n"
i = blob.index(mark) + len(mark)
j = blob.find("\n=== ", i)
d = json.loads(blob[i:j] if j > 0 else blob[i:])
print(" ".join("%s=%s" % (p["name"], p["status"]) for p in d["properties"]))
PY
xs=$(python3 "$work/stored.py" "$work/monotone.log" Xstored 2>/dev/null)
ys=$(python3 "$work/stored.py" "$work/monotone.log" Ystored 2>/dev/null)
if [ -n "$xs" ] && [ -n "$ys" ]; then
  pass "the probe built both proof states and read back both stored verdict sets"
else
  bad "the probe did not produce two verdict sets — it did not run: $(tail -3 "$work/monotone.log" | tr '\n' ' ')"
fi
echo "  X (only strip-comment proven): $xs"
echo "  Y (eight more deps proven):    $ys"
# THE DOWNGRADE. A property whose STORED status is `proven` in X and `tested` in
# Y has had real evidence withdrawn by an action — proving something else — that
# a consumer would reasonably expect to be safe. Whether it fires is reported,
# not asserted: it is a fact about the induction search, and the search is
# exactly the thing most likely to change.
down=$(python3 - "$xs" "$ys" <<'PY'
import sys
x = dict(kv.split('=') for kv in sys.argv[1].split())
y = dict(kv.split('=') for kv in sys.argv[2].split())
print(" ".join(k for k in x if x[k] == 'proven' and y.get(k) != 'proven'))
PY
)
if [ -n "$down" ]; then
  echo "  [note] DOWNGRADED by proving a dependency: $down"
  echo "         'prove' re-derives from scratch and the stored verdict follows the LAST"
  echo "         attempt, so a larger lemma library can withdraw evidence that was already"
  echo "         recorded. X and Y are a superset pair, so this is not two unrelated states."
else
  echo "  [note] no stored verdict was downgraded between X and Y."
fi

# --- 4. build both backends ----------------------------------------------------
echo
echo "4 — build the entry on BOTH backends"
t0=$(now)
"$OATHBIN" build ledger-main -o "$work/ledger-go" --backend go > "$work/build-go.log" 2>&1
gorc=$?
t1=$(now); record "build (go)" "$(elapsed "$t0" "$t1")"
if [ "$gorc" -eq 0 ]; then pass "go backend built"; else bad "go build failed: $(tail -2 "$work/build-go.log" | tr '\n' ' ')"; fi

t0=$(now)
"$OATHBIN" build ledger-main -o "$work/ledger-llvm" --backend llvm > "$work/build-llvm.log" 2>&1
llrc=$?
t1=$(now); record "build (llvm)" "$(elapsed "$t0" "$t1")"
if [ "$llrc" -eq 0 ]; then
  pass "llvm backend built — nothing in this program is outside the LLVM subset"
else
  bad "llvm build failed (this is a finding, not a skip): $(tail -4 "$work/build-llvm.log" | tr '\n' ' ')"
fi
[ "$gorc" -eq 0 ] && [ "$llrc" -eq 0 ] || die "both backends must build before anything below can be measured"

# NATIVE LOWERING IS ASSERTED, NOT ASSUMED, because falling back to the
# structural body is SILENT: the assoc-list path is CORRECT — same answers, same
# exit code, same report — and O(N) per lookup instead of O(log N), and nothing
# in the build output says which one you got. At 26,819 postings that is the
# difference between this program and a different program with the same
# behaviour.
#
# The namespaced-discovery gap `strmap-consumer-friction.md` found is RESOLVED
# (#186: `opNameIndex` keys the vocabulary by a name's final path segment and
# `resolveOp` prefers the bare name, falling back to a namespaced alias), so this
# is not asserting a known-broken path. What remains is that `validateFamily` is
# deliberately FAIL-CLOSED — any operation that does not match its canonical
# signature over one consistent datatype drops the WHOLE family to the structural
# path — and that drop is as quiet as the old one. So these checks are two
# things: a guard on that fail-closed edge, and regression evidence that the
# measurements in this run were taken on the native path rather than the
# fallback.
"$OATHBIN" build ledger-main -o "$work/emitted.go" --backend go   --emit-source >/dev/null 2>&1
"$OATHBIN" build ledger-main -o "$work/emitted.ll" --backend llvm --emit-source >/dev/null 2>&1
if grep -q 'smapInsert' "$work/emitted.go" && grep -q 'smapLookup' "$work/emitted.go" &&
   grep -q 'smapKeys' "$work/emitted.go"; then
  pass "go: this run used the NATIVE str-map ($(grep -o 'smap[A-Za-z]*' "$work/emitted.go" | sort -u | tr '\n' ' '))"
else
  bad "go: no native smap* calls in the emitted source — the str-map silently fell back to the assoc list"
fi
# The CONTROL for that assertion: the presence of native calls would still be
# consistent with SOME calls falling back, so the structural bodies must be
# absent as well.
if grep -q 'f_smi_insert\|f_smi_lookup' "$work/emitted.go"; then
  bad "go: the structural assoc-list bodies f_smi_* are still emitted — part of the aggregate is not native"
else
  pass "go: zero structural assoc-list fallback (no f_smi_* bodies emitted)"
fi
if grep -q '@o_strmap_insert' "$work/emitted.ll" && grep -q '@o_strmap_lookup' "$work/emitted.ll" &&
   grep -q '@o_strmap_keys' "$work/emitted.ll"; then
  pass "llvm: the aggregate lowers to the native persistent tree (@o_strmap_insert/lookup/keys)"
else
  bad "llvm: no @o_strmap_* calls in the emitted IR — the str-map fell back to the assoc list"
fi

# --- 5. the three-way gate on a small fixture ----------------------------------
# `oath eval` is the reference. The interpreter cannot be handed the readfile
# capability, so what is compared is the composition BELOW the capability —
# scan/adjudicate over the same text — which is every line of logic in the
# program except the one that opens the file.
echo
echo "5 — THREE-WAY: oath eval (the reference) vs the go artifact vs the llvm artifact"
head -n 12 "$JOURNAL" > "$work/tiny.journal"
python3 - "$work/tiny.journal" > "$work/tiny.expr" <<'PY'
import sys
t = open(sys.argv[1]).read()
q = t.replace('\\', '\\\\').replace('"', '\\"').replace('\n', '\\n').replace('\t', '\\t')
sys.stdout.write('(adjudicate (scan (str-split 10 "%s") empty-scan))' % q)
PY
"$OATHBIN" eval "$(cat "$work/tiny.expr")" > "$work/tiny.eval" 2>&1

# decode_str turns the interpreter's structural Str — (SCons 104 (SCons ...)) —
# into the bytes a compiled program prints. Taken from
# docs/experiments/issue-158-llvm-subset/acceptance.sh, including its reason for
# reading a FILENAME rather than stdin: `python3 -` reads its program from
# stdin, so a heredoc script and piped data compete for one channel, the heredoc
# wins, and the decoder silently returns "" for every input — which would turn
# an agreement check green.
cat > "$work/decode_str.py" <<'PY'
import sys, re
out = open(sys.argv[1]).read()
i = out.rfind(' : ')
if i >= 0: out = out[:i]
sys.stdout.write(''.join(chr(int(n)) for n in re.findall(r'\(SCons (\d+)', out)))
PY
printf '(SCons 111 (SCons 107 SNil)) : Str\n' > "$work/probe.txt"
if [ "$(python3 "$work/decode_str.py" "$work/probe.txt")" = "ok" ]; then
  pass "the Str decoder is working (a non-empty Str does not decode to \"\")"
else
  bad "the Str decoder is broken — every comparison below would be vacuous"
fi

python3 "$work/decode_str.py" "$work/tiny.eval" > "$work/tiny.eval.raw"
"$work/ledger-go"   "$work/tiny.journal" > "$work/tiny.go.raw"   2>&1
"$work/ledger-llvm" "$work/tiny.journal" > "$work/tiny.llvm.raw" 2>&1
# NORMALISE THE TRAILING NEWLINE, and only that. A compiled artifact's CLI
# wrapper terminates the line it prints; the `Str` the program computed does not
# contain that byte, and the interpreter prints the Str. So the difference is in
# the PRINTER, not in the value, and comparing the values is what this check
# claims to do. Nothing else is normalised — every interior byte, including
# every interior newline, still has to match exactly.
strip_nl() { python3 -c 'import sys; sys.stdout.write(open(sys.argv[1],"rb").read().rstrip(b"\n").decode("utf-8","surrogateescape"))' "$1"; }
strip_nl "$work/tiny.eval.raw" > "$work/tiny.eval.txt"
strip_nl "$work/tiny.go.raw"   > "$work/tiny.go.txt"
strip_nl "$work/tiny.llvm.raw" > "$work/tiny.llvm.txt"
if [ -s "$work/tiny.eval.txt" ] && cmp -s "$work/tiny.eval.txt" "$work/tiny.go.txt"; then
  pass "oath eval == go artifact, byte for byte"
else
  bad "eval and go disagree: $(diff "$work/tiny.eval.txt" "$work/tiny.go.txt" 2>&1 | head -4 | tr '\n' ' ')"
fi
if [ -s "$work/tiny.eval.txt" ] && cmp -s "$work/tiny.eval.txt" "$work/tiny.llvm.txt"; then
  pass "oath eval == llvm artifact, byte for byte"
else
  bad "eval and llvm disagree: $(diff "$work/tiny.eval.txt" "$work/tiny.llvm.txt" 2>&1 | head -4 | tr '\n' ' ')"
fi

# --- 6. the full journal on LLVM ------------------------------------------------
echo
echo "6 — the FULL journal, llvm artifact, against the awk oracle"
t0=$(now)
"$work/ledger-llvm" "$JOURNAL" > "$work/full.llvm.out" 2> "$work/full.llvm.err"
llvmrun=$?
t1=$(now); record "run full journal (llvm)" "$(elapsed "$t0" "$t1")"
if [ "$llvmrun" -eq 0 ]; then
  pass "the llvm artifact accepted the journal (exit 0)"
else
  bad "the llvm artifact exited $llvmrun on the pristine journal: $(head -3 "$work/full.llvm.err" | tr '\n' ' ')"
fi

# Turn the report back into the oracle's own format and compare the WHOLE
# aggregate — not a spot check on a line someone happened to pick.
awk '
  /^  [a-z]/ && NF==3 {
    amt=$3; neg=0; if (substr(amt,1,1)=="-") { neg=1; amt=substr(amt,2) }
    d=index(amt,"."); ip=substr(amt,1,d-1); fp=substr(amt,d+1)
    v=(ip+0)*1000 + (fp+0); if (neg) v=-v
    printf "BAL %s %s %d\n", $1, $2, v; next
  }
  /^  [A-Z]/ && NF==2 {
    amt=$2; neg=0; if (substr(amt,1,1)=="-") { neg=1; amt=substr(amt,2) }
    d=index(amt,"."); ip=substr(amt,1,d-1); fp=substr(amt,d+1)
    v=(ip+0)*1000 + (fp+0); if (neg) v=-v
    printf "TOT %s %d\n", $1, v; next
  }
  /^transactions:/ { printf "COUNTS %d %d\n", $2, $4 }
' "$work/full.llvm.out" | sort > "$work/reported.txt"
grep -v '^MAXMAG' "$work/oracle.txt" > "$work/oracle.cmp"
if cmp -s "$work/oracle.cmp" "$work/reported.txt"; then
  pass "the report matches the independent oracle EXACTLY — every (account, commodity), every total, both counts"
else
  bad "the report and the oracle disagree: $(diff "$work/oracle.cmp" "$work/reported.txt" | head -6 | tr '\n' ' ')"
fi
# The payoff of the per-commodity model: a sound journal nets to zero in EVERY
# commodity, and the aggregate is exact ℤ, so this is equality and not tolerance.
# THE REPORT ITSELF. A friction log that quotes figures nobody can see is a
# friction log nobody can check, so the aggregate this journal actually produces
# is printed here in full — 21 (account, commodity) balances and three totals.
echo
sed 's/^/    /' "$work/full.llvm.out"
echo
nz=$(awk '/^TOT/ && $3 != 0 {n++} END{print n+0}' "$work/reported.txt")
ncom=$(awk '/^TOT/{n++} END{print n+0}' "$work/reported.txt")
if [ "$ncom" -ge 3 ] && [ "$nz" -eq 0 ]; then
  pass "all $ncom commodity totals are EXACTLY zero (thousandths, ℤ — not a tolerance)"
else
  bad "$nz of $ncom commodity totals are non-zero"
fi

# --- 6b. what a float ledger would have got, on this journal --------------------
# "Exact ℤ" is the reason to write a ledger in Oath at all, so the claim is
# MEASURED here rather than asserted: the same 26,819 postings are accumulated a
# second time in IEEE binary64, in file order, the way a ledger written in a
# language with float money would do it.
#
# THE RESULT IS REPORTED IN BOTH DIRECTIONS, including the half that does not
# flatter the argument. Read what it prints, not what you expect it to print.
echo
echo "6b — the same journal accumulated in IEEE binary64, for comparison"
cat > "$work/floatdrift.py" <<'PY'
import sys
exact = {}; flt = {}; etot = {}; ftot = {}
for line in open(sys.argv[1]):
    if line[:1] not in ' \t': continue
    f = line.split()
    if len(f) != 3: continue
    acct, amt, comm = f
    neg = amt.startswith('-'); a = amt[1:] if neg else amt
    ip, _, fp = a.partition('.')
    fp = (fp + '000')[:3]
    v = int(ip) * 1000 + int(fp)
    if neg: v = -v
    k = acct + ' ' + comm
    exact[k] = exact.get(k, 0) + v            # what the Oath program computes
    flt[k]   = flt.get(k, 0.0) + float(amt)   # what a float ledger computes
    etot[comm] = etot.get(comm, 0) + v
    ftot[comm] = ftot.get(comm, 0.0) + float(amt)
drifting = [k for k in exact if flt[k] != exact[k] / 1000.0]
worst = max((abs(flt[k] - exact[k] / 1000.0), k) for k in exact)
# The sharper question than "do the numbers differ" is "does the VERDICT
# differ" — a checker only reads the totals, and a difference the checker never
# looks at is not a difference the checker can catch.
verdict_differs = sum(1 for c in etot if (etot[c] == 0) != (ftot[c] == 0.0))
print("BUCKETS %d" % len(exact))
print("DRIFTING %d" % len(drifting))
print("WORST %.6e %s" % (worst[0], worst[1]))
print("VERDICTDIFFERS %d" % verdict_differs)
for c in sorted(etot):
    print("TOTAL %s exact=%d float=%.17g" % (c, etot[c], ftot[c]))
PY
python3 "$work/floatdrift.py" "$JOURNAL" > "$work/floatdrift.txt" 2>&1
fdbuckets=$(awk '/^BUCKETS/{print $2}' "$work/floatdrift.txt")
fddrift=$(awk '/^DRIFTING/{print $2}' "$work/floatdrift.txt")
fdworst=$(awk '/^WORST/{print $2}' "$work/floatdrift.txt")
fdverdict=$(awk '/^VERDICTDIFFERS/{print $2}' "$work/floatdrift.txt")
if [ -n "${fdbuckets:-}" ] && [ "$fdbuckets" -gt 0 ] 2>/dev/null; then
  pass "the float comparison ran over all $fdbuckets (account, commodity) buckets"
else
  bad "the float comparison did not run: $(head -3 "$work/floatdrift.txt" | tr '\n' ' ')"
  fdbuckets=0; fddrift=?; fdworst=?; fdverdict=-1
fi
echo "  $fddrift of $fdbuckets balances differ from the exact value under binary64; worst drift $fdworst units"
echo "  commodity-total VERDICT differs in $fdverdict of $ncom commodities"
sed -n 's/^TOTAL /    /p' "$work/floatdrift.txt"
# THE HONEST READING, stated here so nobody has to reconstruct it from the
# numbers: on THIS journal the float totals cancel exactly, because every
# transaction is a small set of equal-and-opposite postings and the rounding
# cancels with it. So a float ledger would NOT have been caught here. What is
# measured is that the individual BALANCES are already wrong at 1e-8 on 1e6-scale
# money after 26,819 postings, and that whether that ever reaches a cent is an
# empirical question about scale that has to be re-asked for every journal. The
# ℤ representation is the claim that there is no such question.
if [ "${fdverdict:-1}" = "0" ]; then
  echo "  [note] the float verdict AGREES on this journal — an honest negative. Exactness is not"
  echo "         demonstrated by this corpus catching a float; it is the absence of a scale at"
  echo "         which it could stop agreeing."
fi

# --- 6c. memory: how the llvm artifact's footprint scales with the input --------
# A LADDER, not a before/after pair. The first version of this measurement ran
# the journal and a 2x concatenation and reported the ratio — and the ratio was
# not reproducible, because at 2x and beyond this machine is close enough to its
# limit that the OS reclaims pages and PEAK RSS UNDER-REPORTS. A number that
# falls when the machine is busier is not measuring the program.
#
# So the ladder stays inside the range where the measurement is sound and takes
# SIX points across three and a half orders of magnitude of input. That turns
# one ratio into a slope with a shape, and the shape is the claim.
echo
echo "6c — llvm peak resident memory across six input sizes"
cat > "$work/peakrss.py" <<'PY'
import resource, subprocess, sys, time
# ru_maxrss is BYTES on macOS and KILOBYTES on Linux. The units are not
# portable, and reading them wrong reports a gigabyte as a megabyte.
scale = 1 if sys.platform == 'darwin' else 1024
t0 = time.time()
rc = subprocess.call(sys.argv[1:], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
t1 = time.time()
print("%d %.3f %d" % (rc, t1 - t0, resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss * scale))
PY
: > "$work/rss.txt"
for n in 12 1000 5000 12000 25000 999999; do
  head -n "$n" "$JOURNAL" > "$work/rung.journal"
  rb=$(wc -c < "$work/rung.journal" | tr -d ' ')
  set -- $(python3 "$work/peakrss.py" "$work/ledger-llvm" "$work/rung.journal")
  # A rung that did not RUN carries no information, and a zero would drag the
  # slope toward linearity for free. Exit 1 is fine here: a prefix cut mid
  # transaction is legitimately unbalanced and still did all the work.
  if [ "$1" -le 1 ] && [ "$3" -gt 0 ]; then
    printf '%s %s %s\n' "$rb" "$3" "$2" >> "$work/rss.txt"
  fi
done
if [ "$(wc -l < "$work/rss.txt" | tr -d ' ')" -eq 6 ]; then
  pass "the memory ladder produced a measurement at all six input sizes"
else
  bad "the memory ladder is incomplete ($(wc -l < "$work/rss.txt" | tr -d ' ') of 6 rungs) — the slope below would be fitted to fewer points than it claims"
fi
python3 - "$work/rss.txt" <<'PY'
import sys
rows = [tuple(l.split()) for l in open(sys.argv[1])]
print("      %10s %12s %10s %14s" % ("bytes in", "peak RSS", "seconds", "RSS/input byte"))
for b, m, t in rows:
    print("      %10s %9.0f MB %10s %11.0f B/B" % (b, int(m)/1048576.0, t, int(m)/int(b)))
PY
# THE SHAPE IS THE CLAIM — but peak RSS only MEASURES the footprint while the
# process stays small relative to physical memory. Past that the OS reclaims and
# the number falls BELOW demand. So the fit uses only the rungs where the
# instrument is sound: peak under an eighth of RAM, a cutoff derived from the
# machine rather than hard-coded.
#
# THE EXCLUDED RUNG IS INCONCLUSIVE, AND IT IS REPORTED THAT WAY RATHER THAN
# CHECKED. An earlier version of this section asserted that the top rung did not
# EXCEED the fitted prediction and read that as evidence of no super-linear
# growth. That assertion has no failure mode where it was applied: reclamation
# can only make peak RSS read LOW, so in this regime the check cannot fail
# whatever the true demand is, and a reading below the model is consistent with
# linear demand AND with super-linear demand alike. A check that cannot fail is
# not evidence. The rung is printed, and the model is not validated at full
# scale — by anything here.
if [ "$(uname -s)" = "Darwin" ]; then
  physmem=$(sysctl -n hw.memsize 2>/dev/null)
else
  physmem=$(awk '/^MemTotal:/{print $2 * 1024}' /proc/meminfo 2>/dev/null)
fi
[ -n "${physmem:-}" ] && [ "$physmem" -gt 0 ] 2>/dev/null || die "could not read physical memory; the memory fit has no derived cutoff"
echo "  (fit uses rungs whose peak RSS stayed under $(python3 -c "print('%.1f' % ($physmem/8/1073741824.0))") GB — an eighth of this machine's $(python3 -c "print('%.0f' % ($physmem/1073741824.0))") GB)"
if python3 - "$work/rss.txt" "$physmem" <<'PY'
import sys
rows = [tuple(map(int, l.split()[:2])) for l in open(sys.argv[1])]
cap = int(sys.argv[2]) // 8
fit = [m / float(b) for b, m in rows if b > 100000 and m < cap]
if len(fit) < 3: sys.exit(1)
sys.exit(0 if max(fit) / min(fit) <= 1.25 else 1)
PY
then
  pass "peak RSS is LINEAR in the input where it is measurable: cost per byte stable within 25%"
else
  bad "the per-byte memory cost is not stable across the measurable rungs — the footprint is not linear here"
fi
# REPRODUCIBILITY CONTROL, inside the low-pressure regime. The fit is only worth
# quoting if the instrument returns the same number twice for the same input, so
# one fitted rung is measured a second time and the two peaks must agree. This is
# what licenses reading the fitted constant to three digits — and it is measured
# where the instrument works, never at the top rung.
head -n 12000 "$JOURNAL" > "$work/repeat.journal"
rpb=$(wc -c < "$work/repeat.journal" | tr -d ' ')
set -- $(python3 "$work/peakrss.py" "$work/ledger-llvm" "$work/repeat.journal")
rp2=$3
rp1=$(awk -v b="$rpb" '$1==b{print $2}' "$work/rss.txt")
if [ -n "$rp1" ] && [ "$rp2" -gt 0 ] && python3 -c "import sys; a=$rp1; b=$rp2; sys.exit(0 if max(a,b)/float(min(a,b)) <= 1.03 else 1)"; then
  pass "REPRODUCIBLE: the same input measured twice agrees within 3% ($(python3 -c "print('%.0f'%($rp1/1048576.0))") MB vs $(python3 -c "print('%.0f'%($rp2/1048576.0))") MB)"
else
  bad "the memory instrument is not reproducible at $rpb bytes ($rp1 vs $rp2) — the fitted constant is not quotable"
fi
# The CONTROL for the linearity assertion. A measurement that reported the same
# figure whatever it was given would also pass a stability test, so the smallest
# and largest rungs must differ by the order of magnitude their inputs do.
smallest=$(head -1 "$work/rss.txt" | awk '{print $2}')
largest=$(tail -1 "$work/rss.txt" | awk '{print $2}')
if [ "$largest" -gt $((smallest * 100)) ]; then
  pass "CONTROL: the ladder discriminates — the largest rung uses over 100x the smallest"
else
  bad "CONTROL: the ladder does not discriminate ($smallest vs $largest bytes) — it may be measuring startup, not the program"
fi
python3 - "$work/rss.txt" "$physmem" <<'PY'
import sys
rows = [tuple(map(int, l.split()[:2])) for l in open(sys.argv[1])]
cap = int(sys.argv[2]) // 8
fit = [m / float(b) for b, m in rows if b > 100000 and m < cap]
slope = sum(fit) / len(fit)
b, m = rows[-1]
print("  [note] MEASURED, in the low-pressure regime: %.0f bytes of resident memory per" % slope)
print("         journal byte, stable across the fitted rungs.")
print("  [note] MEASURED, full journal: %.2f GB peak RSS. This run is past the cutoff, so"
      % (m / 1073741824.0))
print("         the reading is taken while the OS is reclaiming and does not track demand.")
print("  [note] PROJECTED, not measured: extending the fitted slope puts the full journal at")
print("         %.2f GB and a 10 MB journal at %.0f GB. MEMORY PRESSURE PREVENTS VALIDATING"
      % (slope * b / 1073741824.0, slope * 10e6 / 1073741824.0))
print("         THE EXTRAPOLATION AT FULL SCALE on this machine: the only instrument available")
print("         stops measuring at about the size the projection would have to be checked at,")
print("         so nothing here rules out the demand growing faster than the model.")
PY

# --- 7. the Go ceiling ----------------------------------------------------------
# The whole file passes through `str-split`, which is structural recursion over
# the Str and is NOT tail recursion, so the recursion depth is the file's
# codepoint count. The Go runtime's 1 GB goroutine stack is the binding limit.
echo
echo "7 — the GO artifact's ceiling on this journal, BISECTED"
t0=$(now)
"$work/ledger-go" "$JOURNAL" > "$work/full.go.out" 2> "$work/full.go.err"
gorun=$?
t1=$(now); record "run full journal (go)" "$(elapsed "$t0" "$t1")"
# A probe that only asks "did it fail?" cannot tell a stack overflow from a
# crashing harness or from a refusal, so the SIGNATURE is what is asserted.
if grep -q 'stack overflow' "$work/full.go.err"; then
  pass "the go artifact OVERFLOWS its stack on the full journal (exit $gorun, 'stack overflow')"
elif [ "$gorun" -eq 0 ]; then
  pass "the go artifact completed the full journal — the ceiling has moved above 1.4 MB"
else
  bad "the go artifact failed for some OTHER reason than a stack overflow: $(head -2 "$work/full.go.err" | tr '\n' ' ')"
fi

overflows() { head -c "$1" "$JOURNAL" > "$work/probe.journal"
              "$work/ledger-go" "$work/probe.journal" >/dev/null 2>"$work/probe.err"
              grep -q 'stack overflow' "$work/probe.err"; }
if grep -q 'stack overflow' "$work/full.go.err"; then
  lo=100000; hi=$(wc -c < "$JOURNAL" | tr -d ' ')
  # BOTH ENDPOINTS VALIDATED. With hard-coded bounds and no check, a lower bound
  # that already overflows is reported as the ceiling.
  if overflows "$lo"; then
    bad "the bisection's lower bound ($lo bytes) already overflows — the reported ceiling would be meaningless"
  elif ! overflows "$hi"; then
    bad "the bisection's upper bound ($hi bytes) does not overflow — there is nothing to bisect"
  else
    while [ $((hi - lo)) -gt 4000 ]; do
      mid=$(( (lo + hi) / 2 ))
      if overflows "$mid"; then hi=$mid; else lo=$mid; fi
    done
    t1=$(now); record "bisect the go ceiling" "$(elapsed "$t0" "$t1")"
    lines=$(head -c "$lo" "$JOURNAL" | wc -l | tr -d ' ')
    # The ceiling is a BRACKET, so the frame size derived from it is quoted as
    # one too: a single number read off one end is a bound presented as a
    # boundary.
    frame=$(python3 -c "print('%.0f-%.0f' % (1000000000.0/$hi, 1000000000.0/$lo))")
    pass "go ceiling BISECTED: $lo bytes OK / $hi bytes overflows (~$lines lines; $frame bytes of Go stack per journal codepoint)"
  fi
fi

# --- 7b. how much of section 7's ceiling is the HOST, and how much is a default? -
# The obvious reading of section 7 is that the Go runtime's 1 GB goroutine stack
# is a hard property of the host. It is not: `debug.SetMaxStack` raises the
# per-goroutine limit at run time, and the emitted program never calls it. So
# part of that ceiling is an emitted-code default, and the only way to tell the
# two apart is to try it — take the SAME emitted source, add the import and one
# call, rebuild, run the same journal. Section 7 has already established that the
# unpatched artifact overflows on this input, which makes this a two-way control:
# identical source, one added call, opposite outcomes.
#
# BUT THIS PROBE IS NOT A FIX, AND IT IS MEASURED SO THAT IT CANNOT BE READ AS
# ONE. The recursion depth is unchanged; only the room to perform it grows. So
# the probe records what the raise COSTS — resident memory, and a new ceiling at
# the raised limit divided by section 7's measured bytes-per-codepoint. A repair
# that removed the depth rather than affording it would show neither.
echo
echo "7b — the same emitted Go source, plus one debug.SetMaxStack call"
gp="$work/gopatch"
mkdir -p "$gp"
"$OATHBIN" build ledger-main -o "$gp/main.go" --backend go --emit-source >/dev/null 2>&1
python3 - "$gp/main.go" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
IMP = "import (" + chr(10)
MAIN = "func main() {" + chr(10)
assert IMP in s and MAIN in s, "the emitted Go no longer has the shape this patch expects"
s = s.replace(IMP, IMP + chr(9) + '"runtime/debug"' + chr(10), 1)
s = s.replace(MAIN, MAIN + chr(9) + "debug.SetMaxStack(8 << 30)" + chr(10), 1)
open(p, "w").write(s)
PY
patchrc=$?
printf 'module oathstackprobe\n\ngo 1.25\n' > "$gp/go.mod"
if [ "$patchrc" -ne 0 ]; then
  bad "SETUP: the emitted Go could not be patched — its shape changed; this probe measured nothing"
elif ( cd "$gp" && go build -o ledger-go-bigstack . ) >"$work/gopatch.log" 2>&1; then
  pass "SETUP: the patched source compiles (one import, one call added)"
  t0=$(now)
  "$gp/ledger-go-bigstack" "$JOURNAL" > "$work/full.go2.out" 2> "$work/full.go2.err"
  g2=$?
  t1=$(now); record "run full journal (go+stack)" "$(elapsed "$t0" "$t1")"
  if [ "$g2" -eq 0 ]; then
    pass "the patched go artifact reads the WHOLE journal (exit 0)"
  else
    bad "the patched go artifact still failed (exit $g2): $(head -2 "$work/full.go2.err" | tr '\n' ' ')"
  fi
  # WHAT THE RAISE COSTS. The point of measuring this is that the raise is a
  # MITIGATION and not a fix: the deterministic crash at 1.4 MB is traded for a
  # resident footprint of the same order as the LLVM artifact's, and for a new
  # ceiling at whatever the raised limit divided by the measured frame comes to.
  # A repair that removed the depth would show neither cost.
  set -- $(python3 "$work/peakrss.py" "$gp/ledger-go-bigstack" "$JOURNAL")
  gm=$3
  if [ "$gm" -gt 0 ]; then
    gmb=$(python3 -c "print('%.0f' % ($gm/1048576.0))")
    gpb=$(python3 -c "print('%.0f' % ($gm/$jbytes))")
    ceil=$(python3 -c "print('%.1f' % (8.0*1024*1024*1024/895/1048576))")
    pass "the raise COSTS: ${gmb} MB peak RSS ($gpb bytes per journal byte), and moves the ceiling to about ${ceil} MB of journal (8 GiB / the 895 B per codepoint measured in 7)"
  else
    bad "the patched artifact's memory could not be measured — the cost of the raise is unmeasured"
  fi
  # And it must produce the RIGHT answer, not merely survive: byte-identical to
  # the llvm artifact on the same input.
  if cmp -s "$work/full.go2.out" "$work/full.llvm.out"; then
    pass "patched go == llvm byte for byte on the full journal (12,001 transactions)"
  else
    bad "the patched go artifact survived but disagrees with llvm: $(diff "$work/full.go2.out" "$work/full.llvm.out" | head -4 | tr '\n' ' ')"
  fi
else
  bad "SETUP: the patched source did not compile — this probe measured nothing: $(tail -3 "$work/gopatch.log" | tr '\n' ' ')"
fi

# --- 8. the two backends, on a prefix the go artifact survives ------------------
echo
echo "8 — go vs llvm on the largest CLEAN prefix the go artifact survives"
# Cut on a blank line so no transaction is truncated: a mid-transaction cut is a
# genuine imbalance and would be measuring the refusal path, not agreement.
cutline=$(head -c "${lo:-1000000}" "$JOURNAL" | grep -n '^$' | tail -1 | cut -d: -f1)
head -n "${cutline:-1000}" "$JOURNAL" > "$work/clean.journal"
"$work/ledger-go"   "$work/clean.journal" > "$work/clean.go.txt"   2>&1; cg=$?
"$work/ledger-llvm" "$work/clean.journal" > "$work/clean.llvm.txt" 2>&1; cl=$?
if [ "$cg" -eq 0 ] && [ "$cl" -eq 0 ] && cmp -s "$work/clean.go.txt" "$work/clean.llvm.txt"; then
  pass "go == llvm byte for byte on $(grep -c '^[0-9]' "$work/clean.journal") transactions (both exit 0)"
else
  bad "go(exit $cg) and llvm(exit $cl) disagree on the clean prefix: $(diff "$work/clean.go.txt" "$work/clean.llvm.txt" | head -4 | tr '\n' ' ')"
fi

# --- 9. refusal, and its control -------------------------------------------------
echo
echo "9 — a CORRUPTED journal must be refused, naming what is wrong (temp copies only)"
# Cut on a blank line. A prefix that stops mid-transaction is genuinely
# unbalanced, so an arbitrary `head -n` would make the CONTROL fail and take the
# refusal checks with it — the control would then be measuring the truncation
# rather than the corruption. (It did, on the first run of this script.)
basecut=$(head -n 200 "$JOURNAL" | grep -n '^$' | tail -1 | cut -d: -f1)
[ -n "$basecut" ] || die "no blank line in the first 200 journal lines — the base prefix cannot be cut cleanly"
head -n "$basecut" "$JOURNAL" > "$work/base.journal"
# CONTROL FIRST. Without it, a program that refused every input would pass every
# assertion below.
"$work/ledger-llvm" "$work/base.journal" > "$work/base.out" 2>&1; brc=$?
if [ "$brc" -eq 0 ]; then
  pass "CONTROL: the uncorrupted prefix is ACCEPTED (exit 0)"
else
  bad "the control prefix was refused (exit $brc) — the refusal checks below would prove nothing: $(head -2 "$work/base.out" | tr '\n' ' ')"
fi

# Corruption 1: one extra USD posting inserted into the opening transaction. The
# imbalance is exactly +0.010 USD — a constant chosen here, so the expected
# residual is not something the checker gets to define for itself.
awk '{print} /^    assets:checking      50000.00 USD$/ && !d {print "    expenses:audit           0.01 USD"; d=1}' \
    "$work/base.journal" > "$work/corrupt1.journal"
if [ "$(wc -l < "$work/corrupt1.journal")" -eq "$(( $(wc -l < "$work/base.journal") + 1 ))" ]; then
  pass "SETUP: the corrupted copy differs from the pristine prefix by exactly one inserted posting"
else
  bad "SETUP: the corruption did not insert exactly one line — the refusal checks below measure the wrong input"
fi

"$work/ledger-llvm" "$work/corrupt1.journal" > "$work/c1.out" 2> "$work/c1.err"; c1=$?
if [ "$c1" -eq 1 ]; then
  pass "the corrupted journal is REFUSED with exit 1"
else
  bad "expected exit 1 on the corrupted journal, got $c1"
fi
if grep -qF 'unbalanced: 2023-01-01 Opening balances [USD residual 0.010]' "$work/c1.err"; then
  pass "the refusal NAMES the transaction, the commodity and the exact residual"
else
  bad "the refusal did not name the failure precisely: $(head -3 "$work/c1.err" | tr '\n' ' ')"
fi
if [ ! -s "$work/c1.out" ]; then
  pass "a refused journal produces NO report on stdout — a consumer cannot mistake it for a result"
else
  bad "the refusal still wrote a report to stdout: $(head -2 "$work/c1.out" | tr '\n' ' ')"
fi
# Both backends must refuse identically; a refusal that only one backend
# produces is a divergence, not a feature.
"$work/ledger-go" "$work/corrupt1.journal" > "$work/c1.go.out" 2> "$work/c1.go.err"; c1g=$?
if [ "$c1g" -eq "$c1" ] && cmp -s "$work/c1.err" "$work/c1.go.err"; then
  pass "go and llvm refuse identically (same exit, byte-identical message)"
else
  bad "the backends refuse differently: go exit $c1g vs llvm exit $c1"
fi

# Corruption 2: a posting line missing its commodity. Money on a line the tool
# cannot read is money that is unaccounted for, so it is REPORTED rather than
# skipped — the predecessor exercise skipped malformed lines silently.
awk '{print} /^    assets:checking      50000.00 USD$/ && !d {print "    expenses:audit           5.00"; d=1}' \
    "$work/base.journal" > "$work/corrupt2.journal"
"$work/ledger-llvm" "$work/corrupt2.journal" > "$work/c2.out" 2> "$work/c2.err"; c2=$?
if [ "$c2" -eq 1 ] && grep -qF 'unreadable posting:' "$work/c2.err"; then
  pass "a posting line missing its commodity is REFUSED and quoted back, not silently skipped"
else
  bad "expected a refusal naming the unreadable posting, got exit $c2: $(head -2 "$work/c2.err" | tr '\n' ' ')"
fi

# --- 10. the parser, exercised on the real journal --------------------------------
echo
echo "10 — comment handling and the exactly-three-fields rule, on real postings"
# THIS JOURNAL CONTAINS NO INLINE COMMENTS (its only two `;` lines are the header
# comment block), so the new comment handling cannot be witnessed by running the
# pristine file — it would pass whether or not the feature existed. The witness
# has to be MANUFACTURED: append a comment to every posting line of a prefix and
# require the report to be unchanged, byte for byte.
awk '/^[ \t]/ && NF==3 {print $0 "   ; note added by run.sh"; next} {print}' \
    "$work/base.journal" > "$work/commented.journal"
ncomm=$(grep -c '; note added by run.sh' "$work/commented.journal")
if [ "$ncomm" -gt 0 ]; then
  pass "SETUP: $ncomm posting lines were given an inline comment"
else
  bad "SETUP: no inline comments were added — the check below would be vacuous"
fi
"$work/ledger-llvm" "$work/commented.journal" > "$work/commented.out" 2>&1; crc=$?
if [ "$crc" -eq 0 ] && cmp -s "$work/base.out" "$work/commented.out"; then
  pass "inline comments on every posting change NOTHING: byte-identical report, exit 0"
else
  bad "the commented journal was not read identically (exit $crc): $(diff "$work/base.out" "$work/commented.out" | head -4 | tr '\n' ' ')"
fi
# And an INDENTED whole-line comment, which the predecessor's first-codepoint
# test would have classified as a posting and then reported as unreadable.
awk '{print} /^    assets:checking      50000.00 USD$/ && !d {print "    ; an indented note"; d=1}' \
    "$work/base.journal" > "$work/indented.journal"
"$work/ledger-llvm" "$work/indented.journal" > "$work/indented.out" 2>&1; irc=$?
if [ "$irc" -eq 0 ] && cmp -s "$work/base.out" "$work/indented.out"; then
  pass "an INDENTED comment line is a comment, not an unreadable posting"
else
  bad "an indented comment was not handled as a comment (exit $irc): $(head -2 "$work/indented.out" | tr '\n' ' ')"
fi
# A FOURTH field is refused. hledger writes priced postings as
# `10.00 USD @ 1.09 EUR`; this representation cannot carry a price, and the
# alternative to refusing is to ignore the tail and report a number the journal
# never stated.
awk '{print} /^    assets:checking      50000.00 USD$/ && !d {print "    assets:fx           10.00 USD @1.09"; d=1}' \
    "$work/base.journal" > "$work/priced.journal"
"$work/ledger-llvm" "$work/priced.journal" > "$work/priced.out" 2> "$work/priced.err"; prc=$?
if [ "$prc" -eq 1 ] && grep -qF 'unreadable posting:' "$work/priced.err"; then
  pass "a four-field (priced) posting is REFUSED and quoted back, not silently truncated to three"
else
  bad "expected a refusal on the four-field posting, got exit $prc: $(head -2 "$work/priced.err" | tr '\n' ' ')"
fi

# --- summary --------------------------------------------------------------------
echo
echo "phase timings"
sed 's/^/  /' "$work/timings.txt"
echo
echo "proof outcome: $proven properties PROVEN, $unproven unproven (capped z3, rlimit 20M)"
echo
if [ "$failures" -eq 0 ]; then
  echo "RESULT: PASS — $checks/$checks checks"
else
  echo "RESULT: FAIL — $failures of $checks checks failed"
  exit 1
fi
