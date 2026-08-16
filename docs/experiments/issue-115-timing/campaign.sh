#!/bin/sh
# One complete bracketed replicate of the nine-cell grid behind
# docs/experiments/issue-115-timing.md.
#
#   campaign.sh <binary> <workdir> <first-port> [replicate-label]
#
# THIS EXISTS BECAUSE THE PROTOCOL IS NOT THE INSTRUMENT. `latency.py` measures
# ONE cell correctly and knows nothing about the campaign the reported figures
# depend on: controls bracketing every replicate, the two controls being the
# SAME payload, one fresh port per cell, and TIME_WAIT drained between cells.
# Left in prose, every one of those is an instruction a reproducer can follow
# imperfectly while every check in `latency.py` still passes -- which is this
# repository's own rule that asserting an obligation cannot create the structure
# needed to satisfy it. So the campaign is a program, and the bracket verdict it
# prints is computed rather than eyeballed.
#
# WRITTEN IN POSIX `sh` AND IT MATTERS. `zsh` does not word-split an unquoted
# variable, so the obvious `for o in $offsets` loop silently measures three
# cells where this one measures nine -- and the run looks entirely successful.
# That is not hypothetical; it happened while this campaign was being built.
#
# Prints one JSON line per run to <workdir>/results.jsonl, then the replicate's
# bracket verdict on stdout. Exits non-zero if any run fails or the campaign's
# own invariants do not hold.
set -eu

# LEAVE NO RESIDUE IN THE REPOSITORY. `mksize.py` imports `mkpayloads.py`
# through importlib, so every campaign would otherwise write a `__pycache__`
# into a committed experiment directory -- gitignored, therefore invisible, and
# still not something a measurement run should create. Structural, rather than
# an `rm` at the end that a failed run would skip.
PYTHONDONTWRITEBYTECODE=1
export PYTHONDONTWRITEBYTECODE

here=$(cd "$(dirname "$0")" && pwd)
mksize="$here/../issue-115-composition/mksize.py"
latency="$here/latency.py"

[ $# -ge 3 ] || { echo "usage: campaign.sh <binary> <workdir> <first-port> [label]" >&2; exit 2; }
bin=$1; work=$2; port=$3; rep=${4:-r1}
[ -x "$bin" ] || { echo "REFUSED: $bin is not an executable" >&2; exit 2; }
[ -f "$mksize" ] || { echo "REFUSED: cannot find $mksize" >&2; exit 2; }

pay="$work/payloads"
mkdir -p "$work"
results="$work/results.jsonl"
: > "$results"

# THE GRID IS STATED ONCE, HERE, and the offsets are DERIVED from the observed
# percentages rather than transcribed. A transcribed offset is a duplicate of a
# derivation, and a duplicate is correct exactly once.
sizes="6032 7740 25249"
pcts="0.003 0.221 0.759"
control_payload=""

echo "== generating payloads ==" >&2
for n in $sizes; do
  for p in $pcts; do
    off=$(python3 -c "print(round($p*$n))")
    python3 "$mksize" "$pay" --offset "$off" "$n" >&2
  done
done

# The control cell is the MEDIAN observed size at the MEDIAN observed position.
control_payload="$pay/at-7740-1711.json"
[ -f "$control_payload" ] || { echo "REFUSED: control payload was not generated" >&2; exit 1; }

# DRAIN TIME_WAIT BEFORE EACH CELL. One cell at 10,000 requests leaves ~10,200
# entries, and macOS holds them 2*MSL = 30s against a 16,384-port ephemeral
# range.
#
# THE DRAIN REFUSES RATHER THAN WARNS, AND A BROKEN COUNTER IS NOT A COUNT OF
# ZERO. An earlier version piped `netstat` into `grep -c` with `|| true`, which
# turns an unavailable or differently-flagged `netstat` into the answer "0
# connections in TIME_WAIT" -- the precondition then appears satisfied on every
# cell, instantly, and the campaign prints an ordinary bracket verdict for runs
# whose declared condition was never established. That is this repository's own
# rule that a check unable to tell its setup failed from the defect it hunts is
# worse than no check. So the counter's failure is caught separately from its
# value, the counter is PROVED usable once before any cell runs, and exhausting
# the cap aborts the campaign instead of annotating it.
tw_count() {
  out=$(netstat -an -p tcp 2>/dev/null) || return 1
  printf '%s\n' "$out" | grep -c TIME_WAIT || true   # `grep -c` exits 1 at zero
}

tw_probe() {
  out=$(netstat -an -p tcp 2>/dev/null) || {
    echo "REFUSED: \`netstat -an -p tcp\` failed; the TIME_WAIT precondition" \
         "cannot be established and the campaign would silently skip it" >&2
    exit 1; }
  # VALIDATE AGAINST A SOCKET WE CREATE, not against whatever the host happens
  # to have. An idle machine with no listeners is a VALID and arguably better
  # host to benchmark on, and `netstat` reporting nothing there is correct
  # rather than broken -- so requiring a pre-existing state refuses the best
  # case. Binding one makes the probe self-sufficient: we know exactly what it
  # should now see.
  probe_port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
  python3 - "$probe_port" <<'PYEOF' &
import socket, sys, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(1); time.sleep(3); s.close()
PYEOF
  probe_pid=$!
  sleep 1
  out=$(netstat -an -p tcp 2>/dev/null)
  kill "$probe_pid" 2>/dev/null; wait "$probe_pid" 2>/dev/null
  printf '%s\n' "$out" | grep -q "\.$probe_port " || {
    echo "REFUSED: \`netstat -an -p tcp\` did not report a listener this script" \
         "had just bound on port $probe_port, so the TIME_WAIT counter cannot" \
         "be trusted and the campaign would silently skip its precondition" >&2
    exit 1; }
}

drain() {
  i=0
  while [ "$i" -lt 40 ]; do
    tw=$(tw_count) || { echo "REFUSED: TIME_WAIT counter failed mid-campaign" >&2; exit 1; }
    [ "$tw" -lt 4000 ] && return 0
    i=$((i + 1)); sleep 1
  done
  echo "REFUSED: $tw connections still in TIME_WAIT after ${i}s. The protocol" \
       "requires draining below 4000 before each cell; this run would not be" \
       "one of the runs the reported figures describe." >&2
  exit 1
}

# FORWARD TERMINATION TO THE ACTIVE CELL. latency.py reaps its own server on a
# signal, but only if it RECEIVES one: a CI timeout or a `kill` aimed at this
# script hits the shell alone, leaving the measurement and its bound server
# running to contaminate or block whatever starts next. A terminal-generated
# Ctrl-C reaches the whole process group and would be fine; nothing else is.
active=""
_forward() {
  [ -n "$active" ] && kill -TERM "$active" 2>/dev/null && wait "$active" 2>/dev/null
  trap - TERM INT HUP
  kill -"$1" $$ 2>/dev/null
}
trap '_forward TERM' TERM
trap '_forward INT'  INT
trap '_forward HUP'  HUP

run() {   # run <label> <payload>
  drain
  echo "  $rep $1  port $port" >&2
  python3 "$latency" "$bin" "$work/c$port" "$port" "$2" \
      --warmup 200 --n 10000 --label "$1" >> "$results" &
  active=$!
  wait "$active"; rc=$?
  active=""
  [ "$rc" = 0 ] || exit "$rc"
  port=$((port + 1))
}

# PROVE THE COUNTER BEFORE THE FIRST CELL, not after. `drain` is only as good as
# `tw_count`, and a counter that cannot work returns its verdict instantly on
# every cell -- so the probe runs once, here, ahead of any measurement.
tw_probe

echo "== $rep: control, nine cells, control ==" >&2
run "control-A" "$control_payload"
for n in $sizes; do
  for p in $pcts; do
    off=$(python3 -c "print(round($p*$n))")
    pc=$(python3 -c "print(f'{100*$off/$n:.1f}')")
    run "$n/$pc%" "$pay/at-$n-$off.json"
  done
done
run "control-B" "$control_payload"

python3 - "$results" "$rep" <<'PY'
import json, sys

rows = [json.loads(l) for l in open(sys.argv[1])]
rep = sys.argv[2]

# EVERY INVARIANT THE CAMPAIGN CLAIMS IS CHECKED HERE, not assumed from the fact
# that the loop above ran. A driver that merely EXECUTES the protocol proves
# nothing if a payload was regenerated, a port collided, or a cell dropped out;
# the reported figures rest on these holding, so they are asserted.
if len(rows) != 11:
    sys.exit("REFUSED: %d runs recorded, expected 11 (control + 9 cells + control)"
             % len(rows))

a = next(r for r in rows if r["label"] == "control-A")
b = next(r for r in rows if r["label"] == "control-B")

# CONTROL IDENTITY BY DIGEST, NOT BY NAME AND NOT BY AXES. The bracket compares
# two runs on the assumption they measured the SAME work; a regenerated or
# repointed file makes that false while both runs still answer 202 and both
# labels still say "control". Size and key offset are the CELL's coordinates,
# and two different bodies can share both -- so checking those identifies the
# cell and quietly permits different bytes inside it. The digest is the only
# comparison a lookalike cannot pass.
if a["payload_sha256"] != b["payload_sha256"]:
    sys.exit("REFUSED: the two controls measured different bytes (%s vs %s) -- "
             "the bracket compares nothing"
             % (a["payload_sha256"][:12], b["payload_sha256"][:12]))
for k in ("bytes", "key_offset"):
    if a[k] != b[k]:
        sys.exit("REFUSED: the two controls differ in %s (%r vs %r)"
                 % (k, a[k], b[k]))

cells = [r for r in rows if r["label"] not in ("control-A", "control-B")]
if len({r["label"] for r in cells}) != 9:
    sys.exit("REFUSED: %d distinct cell labels, expected 9"
             % len({r["label"] for r in cells}))
for r in rows:
    if r["n"] != 10000 or r["warmup"] != 200:
        sys.exit("REFUSED: %s ran %d/%d, not the declared 200/10,000"
                 % (r["label"], r["warmup"], r["n"]))
    if r["emit_lines"] != r["warmup"] + r["n"]:
        sys.exit("REFUSED: %s emitted %d lines for %d requests"
                 % (r["label"], r["emit_lines"], r["warmup"] + r["n"]))

# THE BRACKET VERDICT, COMPUTED. The thresholds are the ones the document
# declares; printing the drift alongside them is what lets a later reader see
# that the partition does not balance on the exact cut.
# UNROUNDED. Applying the threshold to the 0.1 us display value lets rounding
# move a replicate across the cut, which would make KEEP/REJECT a property of
# the formatting rather than of the drift it is gating on.
_am, _bm = a["total"].get("median_ns"), b["total"].get("median_ns")
_ap, _bp = a["total"].get("p95_ns"), b["total"].get("p95_ns")
if None in (_am, _bm, _ap, _bp):
    raise SystemExit("REFUSED: this run predates the unrounded quantiles; "
                     "re-run latency.py so the bracket gate is applied to the "
                     "measured value rather than to its printed form")
dm = 100 * (_bm - _am) / _am
dp = 100 * (_bp - _ap) / _ap
ok = abs(dm) < 2 and abs(dp) < 5
print("%s bracket: control-A -> control-B  median %+.2f%%  p95 %+.2f%%  -> %s"
      % (rep, dm, dp, "KEEP" if ok else "REJECT (do not use this replicate)"))
print("%s cells: " % rep + "  ".join(
    "%s %.1f/%.1f" % (r["label"], r["total"]["median_us"], r["total"]["p95_us"])
    for r in cells))
PY
