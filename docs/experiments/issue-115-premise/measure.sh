#!/bin/sh
# The instrument behind docs/experiments/issue-115-premise.md.
#
#   measure.sh <binary> <workdir> <port> <mode> [payload]
#
#     idle       bind, send nothing, read the peak.  THE CONTROL: without it a
#                peak figure has nothing to subtract from.
#     unsigned   no signature header at all -> 401 before anything reads the
#                body.  This is the RECEIPT measurement.
#     tampered   an x-hub-signature-256 carrying a well-formed but WRONG digest
#                -> HMAC runs over the body, then 401.  Not "signed": the
#                refusal is the expected answer, and calling it signed makes a
#                401 read as a contradiction.
#     signed     a digest this handler accepts -> runs to completion, 202, and
#                the JSON scan actually executes.  ONLY THIS MODE REACHES THE
#                SCAN, which is why order-dependence is invisible in every
#                other one.
#
# ONE SCRIPT WITH FOUR MODES RATHER THAN FOUR SCRIPTS. The modes differ only in
# which request is sent; sharing the startup, liveness and read-out logic is
# what stops one of them drifting into measuring something slightly different
# from the others, which is the whole reason the numbers are comparable.
#
# WHAT IT MEASURES. Peak RSS of the server process. The server is a
# single-process accept loop that releases its request arena after serialising
# each answer, so the process maximum is the figure and a poller would race the
# release. /usr/bin/time -l reports maximum RSS in bytes, and reports rusage for
# a signal-terminated child -- which is what makes start/drive/kill/read valid.
#
# KILL THE SERVER, NOT THE TIMER. Signalling /usr/bin/time kills the instrument
# before it prints and the run reports nothing. The first version did exactly
# that, which is why the pid bookkeeping below is explicit.
#
# Prints: <peak-KiB> <TAB> <status>
set -eu

bin="$1"; work="$2"; port="$3"; mode="$4"; payload="${5:-}"
secret=0123456789abcdef0123456789abcdef

case "$mode" in
  idle|unsigned|tampered|signed) ;;
  *) echo "usage: measure.sh <binary> <workdir> <port> idle|unsigned|tampered|signed [payload]" >&2; exit 2 ;;
esac
if [ "$mode" != idle ] && { [ -z "$payload" ] || [ ! -f "$payload" ]; }; then
  echo "mode $mode needs a payload file" >&2; exit 2
fi

mkdir -p "$work"
: > "$work/emit.log"
OATH_VALUE_SECRET="$secret"
OATH_EMIT_PATH="$work/emit.log"
OATH_HTTP_ADDR="127.0.0.1:$port"
export OATH_VALUE_SECRET OATH_EMIT_PATH OATH_HTTP_ADDR

/usr/bin/time -l "$bin" > "$work/server.out" 2> "$work/time.txt" &
tpid=$!
spid=""

# CLEAN UP BOTH PIDS ON EVERY EXIT PATH. Killing only the timer leaves its
# server child alive; that orphan can bind LATER and be the thing a subsequent
# run connects to, which turns one setup failure into a silently wrong
# measurement in a different run. A trap covers the early exits too.
# It DISCOVERS the child if the signal arrives before the loop has recorded it:
# an INT between launch and the first pgrep would otherwise kill the timer and
# orphan exactly the process this trap exists to reap.
cleanup() {
  [ -z "$spid" ] && spid=$(pgrep -P "$tpid" 2>/dev/null | head -1 || true)
  [ -n "$spid" ] && kill "$spid" 2>/dev/null || true
  kill "$tpid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# WAIT FOR **THIS** SERVER TO REPORT ITS OWN BIND, NOT FOR THE PORT TO ANSWER.
# A connect probe cannot distinguish this child from a stale listener that
# already owns the port: the probe succeeds against the stale one, the request
# then goes there, and RSS is read from the freshly launched process -- a
# valid-looking measurement of two unrelated things. The binary prints
# "oath handler listening on <addr>" AFTER it binds, to the stderr captured
# here and truncated at launch, so that line is proof that THIS process owns
# THIS port. Child existence alone proves neither.
i=0
while [ "$i" -lt 300 ]; do
  spid=$(pgrep -P "$tpid" 2>/dev/null | head -1 || true)
  if grep -q "handler listening on 127.0.0.1:$port\$" "$work/time.txt" 2>/dev/null; then break; fi
  if [ -n "$spid" ] && ! kill -0 "$spid" 2>/dev/null; then break; fi
  i=$((i + 1)); sleep 0.05
done
if ! grep -q "handler listening on 127.0.0.1:$port\$" "$work/time.txt" 2>/dev/null; then
  echo "SETUP FAIL: this server never reported binding 127.0.0.1:$port" >&2
  sed 's/^/    /' "$work/time.txt" >&2
  exit 1
fi

status=idle
if [ "$mode" != idle ]; then
  status=$(python3 - "$port" "$payload" "$mode" "$secret" <<'PY'
import socket, sys, hmac, hashlib
port, path, mode, secret = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
body = open(path, 'rb').read()
if mode == 'unsigned':
    sig = b""
else:
    mac = (hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
           if mode == 'signed' else "00" * 32)
    sig = b"X-Hub-Signature-256: sha256=" + mac.encode() + b"\r\n"
req = (b"POST /hook HTTP/1.1\r\nHost: 127.0.0.1\r\n"
       b"Content-Type: application/json\r\n"
       b"X-GitHub-Event: push\r\n"
       b"X-GitHub-Delivery: 00000000-0000-0000-0000-000000000001\r\n" + sig +
       b"Content-Length: " + str(len(body)).encode() + b"\r\n"
       b"Connection: close\r\n\r\n") + body
try:
    s = socket.create_connection(('127.0.0.1', port), timeout=60)
    s.sendall(req)
    out = b""
    while True:
        c = s.recv(65536)
        if not c: break
        out += c
    s.close()
    sys.stdout.write(out.split(b"\r\n")[0].decode('latin1') if out
                     else "<no response: connection closed empty>")
except Exception as e:
    sys.stdout.write("<transport: %s>" % e)
PY
)
fi

# THE SERVER MUST STILL BE ALIVE. A crash mid-request frees the arena at exit,
# so the peak would then describe a process that never finished the work -- and
# a refusal produced by measuring nothing looks exactly like a real one.
if ! kill -0 "$spid" 2>/dev/null; then
  echo "SETUP FAIL: server died before it was measured (see $work/server.out)" >&2
  wait "$tpid" 2>/dev/null || true
  exit 1
fi
kill "$spid" 2>/dev/null || true
wait "$tpid" 2>/dev/null || true

rss=$(awk '/maximum resident set size/ { print $1 }' "$work/time.txt")
if [ -z "$rss" ]; then
  echo "SETUP FAIL: no maximum-RSS line; the instrument did not report" >&2
  sed 's/^/    /' "$work/time.txt" >&2
  exit 1
fi

# THE STATUS IS PART OF THE MEASUREMENT, NOT COMMENTARY ON IT. Each mode exists
# to drive ONE path, and the answer is what proves that path ran: 401 for the
# two refusals, 202 for the delivery that runs to completion and therefore
# reaches the JSON scan. A wrong secret, a moved route, a changed media type or
# an early 500 all still yield a plausible RSS figure while exercising
# something else entirely -- and a 401 recorded as "signed" is exactly how the
# order-dependence went missing the first time it was probed. So an unexpected
# answer INVALIDATES the run rather than being reported alongside it.
case "$mode" in
  unsigned|tampered) want="HTTP/1.1 401 Unauthorized" ;;
  signed)            want="HTTP/1.1 202 Accepted" ;;
  idle)              want="idle" ;;
esac
if [ "$status" != "$want" ]; then
  echo "MEASUREMENT INVALID: mode $mode expected '$want', got '$status'" >&2
  echo "  the run did not exercise the path it claims; the RSS figure is discarded" >&2
  exit 1
fi

echo "$((rss / 1024))	$status"
