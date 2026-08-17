#!/bin/sh
# #177 — derive the `oath find --implies` blind spot from the committed corpus.
#
# WHAT IT RUNS, and why in this order:
#
#   1. the corpus census   (oath/corpus_census_test.go)  — the UNIVERSE, taken
#      from codebase/names.json: every live name, every object it resolves to.
#   2. the fragment probe  (oath/fragment_probe_test.go) — the prover's OWN
#      enumeration seam, run live over the same store, with no solver: how many
#      scripts the strategy sequence emits per goal, and the translator's own
#      refusal text for a goal that emits none.
#   3. the join            (blindspot.py) — controls, partition, members, counts.
#
# NOTHING HERE WRITES TO THE STORE. That is not asserted, it is CHECKED, and the
# check is over FILE CONTENT rather than over `git status`. Emptiness is
# deliberately not the test — a tree that was already dirty going in stays
# legible, and what matters is that this run changed nothing.
#
# `git status --short` alone cannot carry that claim: a file that was ALREADY
# modified going in keeps its ` M` code when modified again, so a status
# comparison reports "unchanged" while the store's bytes moved. `codebase/log.jsonl`
# is the realistic instance — it is append-only, it is the artefact a stray
# `make verify` grows, and a dirty tree is exactly the situation in which someone
# would want this check to be trustworthy. So the harness digests every file
# under both trees; `git status` is still recorded, for the reader.
#
# There is no `set -e`. Under it a failing check would take the rest of the
# harness with it, and the run would look like it stopped at the first problem
# rather than like it died — this repo has paid for that four times in one
# session. Every step is checked explicitly, the checks are COUNTED, and the
# final line reports the count alongside the verdict. A run that does not print
# that line did not finish.
#
#   docs/experiments/issue-177-fragment/run.sh [OUTDIR]
#
# OUTDIR defaults to a fresh mktemp -d and holds the intermediate JSON, which is
# regenerable in about three seconds and is deliberately not committed. The
# report goes to stdout; the caller redirects it.

checks=0
fails=0
ck() { # ck <description> <0-on-pass>
	checks=$((checks + 1))
	if [ "$2" -eq 0 ]; then
		echo "[PASS] $1"
	else
		echo "[FAIL] $1"
		fails=$((fails + 1))
	fi
}
die() { echo "[VOID] $1"; echo "SUMMARY: VOID after $checks check(s) — this harness did NOT run"; exit 2; }

root=$(git rev-parse --show-toplevel 2>/dev/null) || die "not inside a git repository"
cd "$root" || die "cannot cd to the repository root"
[ -f CLAUDE.md ] && [ -d oath ] && [ -d codebase ] || die "this does not look like the oath-lang root"
# fixtures/ is HALF THE CLAIMED UNIVERSE, not an optional extra: the digest below
# guards it and the join reconciles against fixtures/prove/attempts.txt. Under a
# sparse checkout the digest would still be non-empty from codebase/ alone and
# the reconciliation would silently have nothing to do, so the run would report
# PASS having measured half of what it says it measures.
[ -d fixtures ] || die "fixtures/ is missing; this harness guards and reconciles against it"
[ -f fixtures/prove/attempts.txt ] || die "fixtures/prove/attempts.txt is missing; the reconciliation cannot run"

command -v go >/dev/null 2>&1 || die "go is not on PATH"
command -v python3 >/dev/null 2>&1 || die "python3 is not on PATH"

out=${1:-$(mktemp -d)} || die "cannot create an output directory"
# REFUSE AN OUTDIR INSIDE A GUARDED TREE, before creating anything. The digest
# check below would notice the harness's own logs afterwards — but only after
# they had been written into the store this script promises not to touch, and a
# check that reports the damage it caused is not a guard.
#
# The whole path is canonicalized, not just its parent. Three ways a string
# comparison lets a path through: it is relative; some component is a SYMLINK
# into a guarded tree (including OUTDIR itself, when it already exists); or the
# parent does not exist yet, so resolving only `dirname` yields nothing at all.
# So: walk up to the nearest existing DIRECTORY, resolve that with `pwd -P`
# (which follows every symlink in it), and re-attach the components that do not
# exist yet.
canonicalize() {
	p=$1
	case $p in /*) ;; *) p=$PWD/$p ;; esac
	tail=""
	while [ ! -d "$p" ]; do
		b=$(basename "$p")
		d=$(dirname "$p")
		[ "$d" = "$p" ] && break
		tail=$b${tail:+/$tail}
		p=$d
	done
	base=$(cd "$p" 2>/dev/null && pwd -P) || return 1
	printf '%s\n' "${base%/}${tail:+/$tail}"
}
# ...AND THE UNRESOLVED SUFFIX MUST BE NORMALIZED, or `..` walks straight past the
# check. `missing/../codebase/probe` has no existing prefix beyond the root, so
# the whole thing comes back as the tail verbatim, matches no guarded prefix, and
# `mkdir -p` then resolves it into the store — which is exactly what happened when
# this was tested. Collapsing `.` and `..` is purely lexical here and that is
# sound: a component that does not exist cannot be a symlink.
normalize() {
	acc=/
	oldifs=$IFS
	IFS=/
	set -f
	for c in $1; do
		case $c in
			"" | .) ;;
			..) acc=$(dirname "$acc") ;;
			*) acc=${acc%/}/$c ;;
		esac
	done
	set +f
	IFS=$oldifs
	printf '%s\n' "$acc"
}
# Collapsing can expose an existing prefix that was hidden behind a `..`, and
# resolving that prefix can expose a new symlink, so the two run to a FIXPOINT
# rather than once each. The bound is a backstop against a pathological input,
# not an expected path: normal input converges in one or two rounds.
resolve_outdir() {
	r=$1
	i=0
	while [ "$i" -lt 16 ]; do
		n=$(normalize "$(canonicalize "$r")") || return 1
		[ "$n" = "$r" ] && { printf '%s\n' "$n"; return 0; }
		r=$n
		i=$((i + 1))
	done
	return 1
}
outreal=$(resolve_outdir "$out") || die "cannot resolve OUTDIR $out to a stable path"
rootreal=$(cd "$root" && pwd -P) || die "cannot resolve the repository root"
case $outreal in
	"$rootreal"/codebase | "$rootreal"/codebase/* | "$rootreal"/fixtures | "$rootreal"/fixtures/*)
		die "OUTDIR $out resolves to $outreal, inside codebase/ or fixtures/, which this harness must not write to" ;;
esac
# Adopt the canonical ABSOLUTE path. The census and probe run inside a `cd oath`
# subshell, so a relative OUTDIR would resolve against `oath/` there while every
# check below resolves it against the root — the harness would then look for a
# report the test wrote somewhere else, and report a failure with the wrong cause.
out=$outreal
mkdir -p "$out" || die "cannot create $out"

echo "repo   : $root"
echo "commit : $(git rev-parse --short HEAD)"
echo "out    : $out"
echo "go     : $(go version)"
echo "python : $(python3 --version 2>&1)"
echo

# --- BEFORE ------------------------------------------------------------------
# codebase/ AND fixtures/ together: `oath fixtures` writes verdicts back on some
# paths and a commit carrying one without the other leaves them disagreeing, so
# the pair is what has to be watched, not either alone.
snapshot() { # snapshot <path> — a content digest of every file under both trees
	find codebase fixtures -type f -print0 2>/dev/null | sort -z |
		xargs -0 shasum -a 256 > "$1" 2>/dev/null
}
git status --short codebase/ fixtures/ > "$out/git-before.txt"
snapshot "$out/digest-before.txt"
echo "--- git status --short codebase/ fixtures/ BEFORE ---"
cat "$out/git-before.txt"
[ -s "$out/git-before.txt" ] || echo "(clean)"
echo "content digest: $(wc -l < "$out/digest-before.txt" | tr -d ' ') files under codebase/ and fixtures/"
echo

# --- 1. the census -----------------------------------------------------------
( cd oath && OATH_CENSUS_OUT="$out/census.json" go test -run TestCorpusCensus -count=1 ) > "$out/census.log" 2>&1
ck "the corpus census ran" $?
[ -s "$out/census.json" ]
ck "the census emitted a non-empty report" $?

# --- 2. the fragment probe ---------------------------------------------------
( cd oath && OATH_FRAGMENT_OUT="$out/fragment.json" go test -run TestFragmentReach -count=1 -v ) > "$out/fragment.log" 2>&1
ck "the fragment probe ran (its own controls are assertions inside the test)" $?
[ -s "$out/fragment.json" ]
ck "the probe emitted a non-empty report" $?
# The probe SKIPS when OATH_FRAGMENT_OUT is unset, and a skip is a PASS to `go
# test`. Without this the harness would report a green run over a measurement
# that never happened — the failure mode this repo calls a probe that proves
# only that something did not crash.
grep -q '^--- PASS: TestFragmentReach' "$out/fragment.log"
ck "the probe reports PASS, not SKIP" $?

# --- 3. the join -------------------------------------------------------------
python3 docs/experiments/issue-177-fragment/blindspot.py \
	"$out/census.json" "$out/fragment.json" > "$out/blindspot.md" 2> "$out/blindspot.err"
ck "the join ran and every one of its controls passed" $?
[ -s "$out/blindspot.md" ]
ck "the join emitted a non-empty report" $?

# --- AFTER -------------------------------------------------------------------
git status --short codebase/ fixtures/ > "$out/git-after.txt"
snapshot "$out/digest-after.txt"
echo
echo "--- git status --short codebase/ fixtures/ AFTER ---"
cat "$out/git-after.txt"
[ -s "$out/git-after.txt" ] || echo "(clean)"
echo
# The digest must be non-empty, or the check would pass by measuring nothing —
# an empty file compares equal to an empty file.
[ -s "$out/digest-before.txt" ] && [ -s "$out/digest-after.txt" ]
ck "the content digest is non-empty (the check has something to compare)" $?
cmp -s "$out/digest-before.txt" "$out/digest-after.txt"
ck "no file under codebase/ or fixtures/ changed content, was added, or was removed" $?
cmp -s "$out/git-before.txt" "$out/git-after.txt"
ck "git status for codebase/ and fixtures/ is unchanged" $?

# --- the report --------------------------------------------------------------
echo
echo "=============================== JOIN OUTPUT ==============================="
echo
cat "$out/blindspot.md"
echo
echo "--- the join's control channel ---"
cat "$out/blindspot.err"
echo

if [ "$fails" -eq 0 ]; then
	echo "SUMMARY: PASS — $checks checks, 0 failures"
	exit 0
fi
echo "SUMMARY: FAIL — $checks checks, $fails failure(s)"
echo "logs: $out/census.log $out/fragment.log $out/blindspot.err"
exit 1
