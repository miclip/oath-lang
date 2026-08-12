#!/bin/sh
# End-to-end acceptance for the webhook receiver.
#
# This is the gate that makes the application DEPEND on Oath rather than merely
# be written in it: it puts the definitions, compiles a binary, runs it, and
# drives it with an independent sender over a real socket. A kernel change that
# breaks compilation, the capability launch gate, the handler adapter, or
# hmac-sha256 fails here.
#
# WHAT EACH CHECK WITNESSES matters more than how many there are. Every
# assertion below distinguishes the handler's claimed behaviour from a plausible
# wrong one — not merely from a parse error:
#
#   404/415/405       three misconfigurations that version 2 answered 202
#   the forgery       a mutation removing the secret guard makes it pass again
#   ping vs push      the same failing sink, one word of the event name apart
#   the repository    absent unless the JSON scan actually ran over the body
#
# Requires: openssl, curl, python3 (to forge under the empty key).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
oath="$root/oath/oath"
work=$(mktemp -d)
# A PER-RUN PORT, because a well-known number collects squatters. An abandoned
# test double from an unrelated project sat on the old fixed 8899 for five days,
# and this suite reported the difference as fourteen application defects.
#
# SCOPE, kept to what this actually buys: it removes the STALE-OCCUPANT case.
# It does NOT make two concurrent runs of this suite safe — ephemeral allocation
# reserves nothing, so two runs can still draw overlapping triples, and one of
# them then measures a port the other owns. Concurrent runs were never supported
# (a fixed port made them collide by construction) and are not supported now;
# measured rather than assumed, by running two at once and watching one fail.
# OATH_TEST_PORT pins the base for anyone debugging.
port="${OATH_TEST_PORT:-}"
if [ -z "$port" ]; then
  port=$(python3 - <<'PICKPORT'
import socket
# A base whose +1 and +2 are free too, since the suite uses all three. Ephemeral
# allocation gives no such guarantee, so the neighbours are checked and the
# whole triple retried rather than assumed.
for _ in range(50):
    s = socket.socket(); s.bind(("", 0)); base = s.getsockname()[1]; s.close()
    # base+2 must be a PORT. Where the ephemeral range reaches the top of it,
    # bind() on 65536 raises OverflowError rather than OSError, so an unlucky
    # draw would escape the retry instead of taking it.
    if base + 2 > 65535:
        continue
    # SELECTION MUST BIND WHAT THE PREFLIGHT BINDS, or it can hand back a triple
    # the preflight then rejects — an abort where a retry was available. base and
    # base+1 are wildcard binds for the servers, so they are probed dual-stack;
    # base+2 is a loopback bind.
    def free(port, loopback=False):
        try:
            if not loopback and socket.has_dualstack_ipv6():
                s = socket.create_server(("", port), family=socket.AF_INET6,
                                         dualstack_ipv6=True, reuse_port=False)
            else:
                s = socket.create_server(("127.0.0.1" if loopback else "", port),
                                         reuse_port=False)
        except OSError:
            return None
        return s
    held = [free(base), free(base + 1), free(base + 2, loopback=True)]
    ok = all(h is not None for h in held)
    for h in held:
        if h is not None: h.close()
    if not ok:
        continue
    print(base); break
else:
    raise SystemExit("could not find three consecutive free ports")
PICKPORT
  ) || { echo "FAIL setup: could not allocate a free port triple"; exit 1; }
fi
url="http://127.0.0.1:$port/hook"
secret="0123456789abcdef0123456789abcdef"
pid=""

holder=""
probe=""
killer=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null
  [ -n "$holder" ] && kill "$holder" 2>/dev/null
  [ -n "$probe" ] && kill "$probe" 2>/dev/null
  [ -n "$killer" ] && kill "$killer" 2>/dev/null
  chmod -R u+rwx "$work" 2>/dev/null || true   # the launch probe leaves a 000 dir
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

# THE PORT MUST BE FREE BEFORE ANYTHING IS MEASURED ON IT.
#
# A stranger already listening here does not make this suite fail to START — it
# makes it run to completion against the WRONG SERVER and report the difference
# as defects in the application. Not hypothetical: an orphaned test double from
# an unrelated project sat on this port for five days, and this suite reported
# fourteen substantive failures — wrong status codes, a missing repository, a
# missing schema — every one a true statement about a server nobody here wrote.
#
# Indistinguishable from a real regression by construction, because every
# assertion is about a RESPONSE and a foreign server produces responses. So this
# runs first and fails as SETUP, which this repo separates from a defect on
# purpose: a check that cannot tell "my setup is broken" from "the thing I hunt
# is present" is worse than no check.
#
# IT BINDS RATHER THAN LISTING LISTENERS, AND BINDS EVERY ADDRESS THIS SUITE
# WILL. `lsof` is not required by this script and is absent on some systems, so
# keying on it would SKIP the check exactly where it is needed — the defect this
# check exists to prevent, one level up. python3 is already required.
#
# The universe is derived from what the suite USES, not from the one port that
# is easy to remember: three ports, and two different bind addresses. `:$port`
# is a WILDCARD bind, so probing 127.0.0.1 alone would pass while a process
# holding another interface on that port makes the server's real bind fail —
# the same proxy mistake in the address that the port list makes in the count.
# Each row below binds the way its user binds. If a new port or address is
# added below, it belongs here too.
# The port+2 rows are probed only when the launch-probe block will actually run.
# That block is skipped as root (a 000 directory is still writable), so requiring
# its port there would fail setup over an address the suite never touches.
addrs=":$port :$((port + 1))"
[ "$(id -u)" != "0" ] && addrs="$addrs 127.0.0.1:$((port + 2))"

ports_ok=1
for spec in $addrs; do
  python3 - "$spec" <<'PORTCHECK' || ports_ok=0
import socket, sys

host, _, port = sys.argv[1].rpartition(":")
port = int(port)

# MIRROR WHAT THE SERVER DOES, rather than testing a convenient approximation.
# Go's ListenAndServe(":PORT") opens a DUAL-STACK IPv6 wildcard where the host
# supports one, so an AF_INET probe of 0.0.0.0 can succeed while the server's
# real bind fails against an IPv6-only holder. The point of binding instead of
# listing listeners is that the probe agrees with the server BY CONSTRUCTION —
# which only holds while it makes the same call.
try:
    if host == "" and socket.has_dualstack_ipv6():
        s = socket.create_server(("", port), family=socket.AF_INET6,
                                 dualstack_ipv6=True, reuse_port=False)
    else:
        s = socket.create_server((host, port), reuse_port=False)
except OSError as e:
    sys.stderr.write("%s is unavailable: %s\n" % (sys.argv[1], e))
    sys.exit(1)
s.close()
PORTCHECK
done
if [ "$ports_ok" != "1" ]; then
  echo "FAIL setup: an address this suite needs is taken, so it would measure whatever"
  echo "  is there and report the difference as application defects."
  echo "  Stop what is holding it, or set OATH_TEST_PORT to a free base port."
  exit 1
fi

# await_ours waits for OUR server to answer, and fails setup if it died.
#
# THE PREFLIGHT ABOVE IS AN EARLY ERROR, NOT A GUARANTEE. It closes its sockets
# before any server binds, so a process arriving in that window — two overlapping
# `make check-app` runs, most plausibly — wins the port instead. The readiness
# loop then succeeds on the FIRST answer from anyone, and every assertion after
# it measures a stranger. That is the same wrong-server measurement, re-entered
# through a door the preflight cannot close.
#
# What this catches is the child that LOST: a server refused the port exits on
# EADDRINUSE, so a dead pid after the poll means anything answering is not ours.
# Checked after the poll rather than before, because losing is what takes time to
# observe.
#
# WHAT IT DOES NOT CATCH, stated rather than implied: if a stranger answers while
# our child has not yet reached its own bind, the poll returns and the pid is
# still alive — liveness at that instant is not ownership. Closing that needs the
# server to identify itself, which is an application change and out of scope here.
# The per-run port above removes the stale-fixture cause rather than the
# possibility, so the residual is a stranger arriving in a window of
# milliseconds on a port the OS has just called free.
await_ours() { # await_ours <pid> <url>
  i=0
  while [ $i -lt 50 ] && ! curl -sS -o /dev/null "$2" 2>/dev/null; do i=$((i + 1)); sleep 0.1; done
  # NOT `kill -0`: an unwaited child that has exited stays a ZOMBIE under dash
  # (which is /bin/sh on the CI runner) and answers kill -0 perfectly well, so
  # the check would pass for exactly the corpse it exists to detect. This file
  # already documents that hazard for the launch probes; the same trap was
  # walked into one helper later. `ps -o stat=` reports Z for a zombie and
  # nothing at all once reaped, so both failure shapes are covered and neither
  # reaps the child that the caller still has to `wait` for.
  st=$(ps -o stat= -p "$1" 2>/dev/null || true)
  case "${st# }" in
    "" | Z*)
      echo "FAIL setup: the server exited instead of serving $2 — most likely it lost the"
      echo "  port to another process, so anything answering there is not ours."
      exit 1
      ;;
  esac
}

fail=0
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then
    printf '  ok    %-46s %s\n' "$1" "$3"
  else
    printf '  FAIL  %-46s want %s, got %s\n' "$1" "$2" "$3"
    fail=$((fail + 1))
  fi
}

post() { # post <url> <content-type> <event> <delivery> <sig> <body-file>
  curl -sS -o /dev/null -w '%{http_code}' -X POST "$1" \
    -H "Content-Type: $2" -H "X-GitHub-Event: $3" -H "X-GitHub-Delivery: $4" \
    -H "X-Hub-Signature-256: $5" --data-binary "@$6"
}
sign() { openssl dgst -sha256 -mac HMAC -macopt "key:$1" -binary < "$2" | od -An -v -tx1 | tr -d ' \n'; }

# A body shaped like a GitHub delivery: compact JSON with a repository object.
printf '{"action":"opened","repository":{"id":1,"full_name":"miclip/oath-lang"},"sender":{"login":"miclip"}}' > "$work/body.json"

# A COPY OF THE STORE, because `oath put` is the only way to check a source
# file and it always writes: even a put whose every definition is a no-op
# appends one journal line per definition. A gate that mutates the store it is
# measuring is exactly the failure CLAUDE.md warns about, and in CI it would
# leave `codebase/` dirty on every run. 2.6 MB is a cheap price for hermetic.
cp -R "$root/codebase" "$work/codebase"
export OATH_STORE="$work/codebase"

echo "building"
if ! "$oath" put "$here/webhook.oath" > "$work/put.out" 2>&1; then
  echo "  FAIL  oath put rejected the application:"; sed 's/^/    /' "$work/put.out"; exit 1
fi
if ! "$oath" build gh-webhook -o "$work/gh-webhookd" > "$work/build.out" 2>&1; then
  echo "  FAIL  oath build failed:"; sed 's/^/    /' "$work/build.out"; exit 1
fi

# The launch gate, before anything is served: an unwritable sink must stop the
# program starting, not make every write fail silently.
#
# EVERY LAUNCH PROBE IS BOUNDED, and that is one function rather than three
# copies of a loop. What these probe is "the program started anyway" — so on a
# regression the process runs forever, and a foreground invocation hangs
# `make check-app` until CI's job timeout instead of reporting the very
# regression it was written to report. **A check that cannot fail FAST cannot
# fail usefully.** The first probe was fixed for this; the two added later
# reintroduced it, which is why the pattern is now a function.
#
# `&& code=0 || code=$?` rather than a bare `wait`: under `set -e` a wait
# returning 70 aborts the script before the assignment, and the check reads
# back an empty string.
# It BLOCKS on wait and bounds it with a killer, rather than polling `kill -0`:
# a poll only observes termination once the child is reaped, and whether an
# unwaited child still answers `kill -0` is shell-dependent — bash reaps it,
# dash (which is /bin/sh on the CI runner) need not. The polling version was
# correct here and would have burned the full timeout per probe there, so
# "bounded" would have meant "always waits the bound".
launch_code() { # launch_code <env assignments...> -- prints exit code or still-running
  env "$@" "$work/gh-webhookd" > /dev/null 2>&1 &
  probe=$!
  # `>/dev/null` ON THE TIMER, and it is not tidiness. launch_code is called
  # inside a command substitution, so the timer subshell inherits that pipe —
  # and killing the subshell leaves its `sleep` holding the write end open, so
  # the substitution blocks for the full timeout waiting for EOF. Every
  # SUCCESSFUL probe paid five seconds. Measured: 5.013s for a child that
  # exited instantly, 0.01s once the timer's stdout is detached.
  ( sleep 5; kill "$probe" 2>/dev/null ) >/dev/null 2>&1 &
  killer=$!
  wait "$probe" 2>/dev/null && lc=0 || lc=$?
  # `|| true` on the KILL, not only on the wait. On the timeout path the killer
  # has already fired and exited, so `kill` returns non-zero and `set -e` aborts
  # the function before it can report — which broke the exact branch this exists
  # for, silently, while the success path worked. Found by running a program
  # that never exits and watching the probe print nothing.
  kill "$killer" 2>/dev/null || true; wait "$killer" 2>/dev/null || true
  probe=""; killer=""
  # 143 is SIGTERM — the killer fired, so the program was still running. The
  # artifact's own exits are 0, 1 and 70, so this cannot collide with one.
  [ "$lc" = "143" ] && lc="still-running"
  printf %s "$lc"
}

mkdir -p "$work/nodir" && chmod 000 "$work/nodir" 2>/dev/null || true
if [ "$(id -u)" != "0" ]; then
  check "unwritable sink refuses to launch" "70" \
        "$(launch_code OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work/nodir/x.log" OATH_HTTP_ADDR=":$port")"

  # AND IT REFUSES BEFORE BINDING, which the check above does NOT witness: a
  # program that bound the port, then resolved capabilities, then exited 70
  # would satisfy it exactly. Verified by emitting exactly that order — the
  # check above still passed and this one failed with EADDRINUSE.
  #
  # So HOLD THE PORT and see which failure wins. Provisioning first is 70;
  # binding first is 1. The second case is the CONTROL — without it, "70" could
  # just mean the program never got as far as either.
  python3 -c "
import socket,time,sys
s=socket.socket(); s.bind(('127.0.0.1',$((port+2)))); s.listen(1)
sys.stderr.write('held\\n'); sys.stderr.flush(); time.sleep(30)" 2> "$work/held" &
  holder=$!
  i=0; while [ $i -lt 50 ] && ! grep -q held "$work/held" 2>/dev/null; do i=$((i+1)); sleep 0.1; done

  # THE HOLDER MUST ACTUALLY HOLD THE PORT. If it never bound, both probes below
  # would see a FREE port, the control would return 70 instead of 1, and the
  # pair would read as a timing regression rather than a broken harness. A check
  # that cannot tell its own setup failed from the defect it hunts is worse than
  # no check.
  if grep -q held "$work/held" 2>/dev/null; then
    check "capabilities resolve BEFORE the port is bound" "70" \
          "$(launch_code OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work/nodir/x.log" OATH_HTTP_ADDR="127.0.0.1:$((port+2))")"
    check "  ...control: a provisionable sink reaches bind" "1" \
          "$(launch_code OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work/held.log" OATH_HTTP_ADDR="127.0.0.1:$((port+2))")"
  else
    printf '  FAIL  %-46s could not hold 127.0.0.1:%s\n' "port-holder setup" "$((port+2))"
    fail=$((fail + 1))
  fi
  # `|| true` on the kill as well as the wait — the same set -e trap as in
  # launch_code, one branch over. When the holder FAILED to bind it has already
  # exited, so this kill returns non-zero and aborts the suite before the
  # remaining checks and the summary. The setup-failure branch printed its FAIL
  # and then silently took the whole run with it.
  kill "$holder" 2>/dev/null || true; wait "$holder" 2>/dev/null || true; holder=""
fi
chmod 755 "$work/nodir" 2>/dev/null || true

OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work/events.log" \
  OATH_HTTP_ADDR=":$port" "$work/gh-webhookd" > "$work/out" 2> "$work/err" &
pid=$!
await_ours "$pid" "http://127.0.0.1:$port/"

echo "serving on :$port"
sig=$(sign "$secret" "$work/body.json")

check "a signed delivery is accepted" \
      "202" "$(post "$url" application/json push d-1 "sha256=$sig" "$work/body.json")"
check "a query string on the hook URL is fine" \
      "202" "$(post "$url?x=1" application/json push d-2 "sha256=$sig" "$work/body.json")"
check "the ping handshake is answered" \
      "200" "$(post "$url" application/json ping d-3 "sha256=$sig" "$work/body.json")"
check "a wrong signature is refused" \
      "401" "$(post "$url" application/json push d-4 "sha256=$(sign wrong-secret-but-long-enough "$work/body.json")" "$work/body.json")"
check "an unprefixed digest is not a signature" \
      "401" "$(post "$url" application/json push d-5 "$sig" "$work/body.json")"
# `hex-decode` USED to keep the prefix it managed to decode, so `<valid digest>zz`
# decoded to the same 32 bytes as the valid digest; the length check in
# `gh-signature` closed it here, and #121 later closed it in the decoder too. This
# check therefore now passes for two independent reasons — which is the point of
# keeping it: it fails the moment either one stops holding.
check "a valid digest with trailing junk is refused" \
      "401" "$(post "$url" application/json push d-5b "sha256=${sig}zz" "$work/body.json")"
check "an unsigned request is refused" \
      "401" "$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$url" -H 'Content-Type: application/json' --data-binary "@$work/body.json")"
check "a signed delivery to the wrong path 404s" \
      "404" "$(post "http://127.0.0.1:$port/hoook" application/json push d-6 "sha256=$sig" "$work/body.json")"
check "a form-encoded delivery 415s" \
      "415" "$(post "$url" application/x-www-form-urlencoded push d-7 "sha256=$sig" "$work/body.json")"
check "a charset parameter is still JSON" \
      "202" "$(post "$url" 'application/json; charset=utf-8' push d-8 "sha256=$sig" "$work/body.json")"
# application/jsonp STARTS WITH application/json and is not JSON. A prefix test
# accepted it — found by review, not by the 13 properties, because the property
# guarding this case restated the implementation's own predicate.
check "a longer media type is not a match" \
      "415" "$(post "$url" application/jsonp push d-9 "sha256=$sig" "$work/body.json")"
check "a space is not a parameter delimiter" \
      "415" "$(post "$url" 'application/json payload' push d-10 "sha256=$sig" "$work/body.json")"
check "a GET is a 405, not a 401" \
      "405" "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/hook")"

# THE ACCEPT PATH ACTUALLY RAN, and the JSON scan ran with it. A receiver that
# answered 202 without reading the body would pass every check above.
check "the repository is read out of the signed body" \
      "miclip/oath-lang" "$(head -1 "$work/events.log" | cut -f4)"
check "the record declares its schema" \
      "oath-gh/1" "$(head -1 "$work/events.log" | cut -f1)"
# GitHub's HMAC covers the BODY ONLY, so the headers are unsigned: anyone who
# observes a valid delivery can replay it with a tab in the delivery id. Before
# record-field, that forged a sixth column and (with the consumer's arity check)
# took down the whole log.
# The same table the properties drive, over a real socket, because these two
# fields come from HEADERS and headers are what the adapter normalizes. A
# property proves gh-record is safe on a Request VALUE; only this proves the
# value the adapter builds is the one gh-record was proven safe on.
# TAB ONLY, and the omission is deliberate rather than an oversight. Go's HTTP
# parser permits HTAB inside a field value (RFC 9110) but a raw LF or CR ENDS
# the header line, so neither is deliverable over HTTP at all — the properties
# cover those, which is the right place for a hazard that depends on the
# transport rather than on this one.
#
# The byte must be built by an escape-interpreting printf and substituted in.
# `printf "ev%sil" '\t'` does NOT interpret the escape — %s takes its argument
# literally — so an earlier version of this loop sent the two printable
# characters backslash-t and asserted nothing. It passed, and it would have
# passed with header sanitization removed.
tab=$(printf '\t')
inject_before=$(wc -l < "$work/events.log" | tr -d ' ')
post "$url" application/json "ev${tab}il" d-inj-1 "sha256=$sig" "$work/body.json" > /dev/null
post "$url" application/json push "de${tab}liv" "sha256=$sig" "$work/body.json" > /dev/null
check "a tab in an unsigned header arrives as one byte" \
      "1" "$(printf %s "$tab" | wc -c | tr -d ' ')"
check "two injected deliveries make two records" \
      "2" "$(( $(wc -l < "$work/events.log" | tr -d ' ') - inject_before ))"
check "no record has anything but five fields" \
      "5" "$(awk -F'\t' 'NF != 5 { print NF; exit } END { print 5 }' "$work/events.log" | head -1)"
check "the ping wrote nothing" \
      "0" "$(cut -f2 "$work/events.log" | grep -c '^d-3$' || true)"
# `&& echo 0 || echo 1` rather than `; echo $?`: under `set -e` a non-zero exit
# inside command substitution aborts the subshell before the echo runs, so the
# check reads back an empty string and the FAILING case cannot be observed at
# all. The version that could only see success passed while proving nothing.
check "the consumer accepts the log" \
      "0" "$("$here/report.sh" "$work/events.log" > /dev/null 2>&1 && echo 0 || echo 1)"
printf '1785000000\tpush\tolder-format\n' > "$work/old.log"
check "the consumer refuses a foreign format" \
      "1" "$("$here/report.sh" "$work/old.log" > /dev/null 2>&1 && echo 0 || echo 1)"
# The TAG is not the schema; the tag and the field count are. A truncated line
# carrying oath-gh/1 would otherwise aggregate as an empty repository.
printf 'oath-gh/1\td\tpush\n' > "$work/short.log"
check "the consumer refuses a truncated record" \
      "1" "$("$here/report.sh" "$work/short.log" > /dev/null 2>&1 && echo 0 || echo 1)"
# A redelivery must be counted ONCE in the aggregate, not merely noticed in the
# header. Two records, one delivery id, one line of output.
printf 'oath-gh/1\tdup\tpush\tr/r\t1\noath-gh/1\tdup\tpush\tr/r\t2\n' > "$work/dup.log"
check "a redelivery is aggregated once" \
      "1" "$("$here/report.sh" "$work/dup.log" | grep -c 'push')"

kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null || true; pid=""

# A NON-ASCII SECRET. `str-bytes` yields codepoints and hmac-sha256 wants bytes,
# so before secret-is-usable checked for it, a correctly signed delivery under a
# 32-character Cyrillic secret PANICKED the request handler: empty reply, stack
# trace, process still listening. 500 is the answer; a dropped connection is not.
env OATH_VALUE_SECRET="ключключключключключключключключ" OATH_EMIT_PATH="$work/utf8.log" \
  OATH_HTTP_ADDR=":$port" "$work/gh-webhookd" > /dev/null 2>&1 &
pid=$!
await_ours "$pid" "http://127.0.0.1:$port/"
utf8sig=$(python3 -c "import hmac,hashlib;print(hmac.new('ключключключключключключключключ'.encode(),open('$work/body.json','rb').read(),hashlib.sha256).hexdigest())")
check "a non-encodable secret is refused, not a panic" \
      "500" "$(post "$url" application/json push utf8 "sha256=$utf8sig" "$work/body.json")"
kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null || true; pid=""

# LATIN-1 IS THE QUIET ONE. Every codepoint is in byte range so nothing errors,
# but str-bytes yields the codepoint while openssl signs UTF-8 — the digests
# disagree and every legitimate delivery would 401 forever. 500 says the
# configuration is wrong, which is what it is.
latin1="ééééééééééééééééééééééééééééééé"
env OATH_VALUE_SECRET="$latin1" OATH_EMIT_PATH="$work/latin1.log" \
  OATH_HTTP_ADDR=":$port" "$work/gh-webhookd" > /dev/null 2>&1 &
pid=$!
await_ours "$pid" "http://127.0.0.1:$port/"
check "a secret with a space is refused" \
      "500" "$(env OATH_VALUE_SECRET="0123456789 abcdef" OATH_EMIT_PATH="$work/sp.log" OATH_HTTP_ADDR=":$((port+1))" "$work/gh-webhookd" > /dev/null 2>&1 & sp=$!; i=0; while [ $i -lt 50 ] && ! curl -sS -o /dev/null "http://127.0.0.1:$((port+1))/" 2>/dev/null; do i=$((i+1)); sleep 0.1; done; c=$(post "http://127.0.0.1:$((port+1))/hook" application/json push sp "sha256=$(sign "0123456789 abcdef" "$work/body.json")" "$work/body.json"); kill $sp 2>/dev/null; wait $sp 2>/dev/null || true; echo "$c")"
check "a latin-1 secret is refused, not mis-signed" \
      "500" "$(post "$url" application/json push l1 "sha256=$(sign "$latin1" "$work/body.json")" "$work/body.json")"
kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null || true; pid=""

# THE FAIL-OPEN, and it is now closed STRUCTURALLY rather than answered.
#
# Version 1 launched with no secret and accepted a delivery forged under the
# empty key. Version 2 launched and answered 500 to everything — better, and
# still a listening process a health check calls healthy. With `secret Str`
# (#126) the secret is a REQUIRED VALUE: the host must supply it before any Oath
# code runs, so there is no running program to forge a delivery against and no
# `""` for the handler to observe.
# `env -u`, because the caller may already have OATH_VALUE_SECRET exported —
# the README's own quickstart says to export it. Inheriting it would make this
# probe launch successfully and block until its timeout, reporting
# "still-running" or, worse, passing something that was never tested.
check "no secret at all: refuses to launch" "70" \
      "$(launch_code -u OATH_VALUE_SECRET OATH_EMIT_PATH="$work/forged.log" OATH_HTTP_ADDR=":$port")"
check "an EMPTY secret: refuses to launch" "70" \
      "$(launch_code OATH_VALUE_SECRET="" OATH_EMIT_PATH="$work/forged.log" OATH_HTTP_ADDR=":$port")"
check "  ...and nothing was recorded" "0" "$(wc -l < "$work/forged.log" 2>/dev/null | tr -d ' ' || echo 0)"

# MISSING AND EMPTY ARE DIFFERENT OPERATOR MISTAKES and say so. Collapsing them
# is the same defect as answering "" for an absent capability.
: > "$work/forged.log"
env -u OATH_VALUE_SECRET OATH_EMIT_PATH="$work/forged.log" OATH_HTTP_ADDR=":$port" "$work/gh-webhookd" 2> "$work/m1" >/dev/null || true
OATH_VALUE_SECRET="" OATH_EMIT_PATH="$work/forged.log" OATH_HTTP_ADDR=":$port" "$work/gh-webhookd" 2> "$work/m2" >/dev/null || true
check "missing says 'is not provided'" \
      "1" "$(grep -c 'is not provided' "$work/m1" || true)"
check "empty says 'provided but empty'" \
      "1" "$(grep -c 'provided but empty' "$work/m2" || true)"

# AND APPLICATION POLICY STAYS IN OATH. A secret that is present and non-empty
# satisfies PROVISIONING, so the program launches; `secret-is-usable`'s length
# and printable-ASCII rules are the artifact's own and still answer 500. #126
# makes one class of misconfiguration unreachable; it does not absorb policy.
echo
if [ "$fail" = "0" ]; then echo "webhook acceptance: all checks passed"; else echo "webhook acceptance: $fail FAILED"; fi
exit "$fail"
