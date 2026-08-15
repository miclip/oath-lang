#!/bin/sh
# End-to-end acceptance for the webhook receiver.
#
# This is the gate that makes the application DEPEND on Oath rather than merely
# be written in it: it puts the definitions, compiles a binary, runs it, and
# drives it with an independent sender over a real socket. A kernel change that
# breaks compilation, the capability launch gate, the handler adapter, or
# hmac-sha256 fails here.
#
# IT RUNS THE WHOLE SEQUENCE ONCE PER BACKEND — `go` and `llvm` — because two
# backends may lower one program differently and the only claim worth making is
# that the OBSERVABLE behaviour is the same. A pass over one backend witnesses
# that backend and nothing else. The passes are otherwise identical: the same
# assertions, in the same order, against a binary built by
# `oath build gh-webhook --backend <backend>` into its own work directory, on
# its own freshly allocated and preflighted port triple.
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
# AND THE COUNT IS AN INVARIANT, NOT A STATISTIC. Every executed check is
# counted per backend, and the two totals MUST agree: a pass that died early,
# skipped a branch, or took a setup-failure fork would otherwise report zero
# failures and look exactly like a complete run. The old script asserted "how
# many there are" matters less than what they witness and then had no counter at
# all, so nothing could tell a short pass from a clean one. The summary reports
# both passes and their counts for the same reason.
#
# Requires: openssl, curl, python3 (to forge under the empty key), and clang —
# which is a requirement of this suite rather than of the application, because
# the llvm pass is not optional. See the clang gate near the bottom.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
oath="$root/oath/oath"
work=$(mktemp -d)

BACKENDS="go llvm"

# Per-pass state. Every one of these is reset by run_backend; they are globals
# because POSIX sh has no locals and the cleanup trap must see them.
backend=""
work_b=""
bin=""
port=""
url=""
pid=""
holder=""
probe=""
killer=""
# The second server, on port+1. It is a tracked global for the same reason the
# others are: `await_ours` can exit the script from underneath it, and an
# untracked child then outlives the run holding a port.
sp=""
checks=0
fail=0

cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null
  [ -n "$holder" ] && kill "$holder" 2>/dev/null
  [ -n "$probe" ] && kill "$probe" 2>/dev/null
  [ -n "$killer" ] && kill "$killer" 2>/dev/null
  [ -n "$sp" ] && kill "$sp" 2>/dev/null
  chmod -R u+rwx "$work" 2>/dev/null || true   # the launch probe leaves a 000 dir
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

secret="0123456789abcdef0123456789abcdef"

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
#
# EACH BACKEND PASS DRAWS ITS OWN TRIPLE. The passes are sequential and each
# reaps its servers before the next begins, so reuse would probably work — but
# "probably" is what a stale listener exploits, and a fresh draw costs
# milliseconds. When OATH_TEST_PORT pins the base, pass n is pinned to
# base + 3n, so the pinned run keeps the property the allocator gives: no two
# passes share an address.
#
# A PINNED BASE MUST FIT EVERY PASS, and the top of the range is where it stops
# doing so silently. A base of 65531 was usable for one pass and is not usable
# for two: the shifted triple runs past 65535, where python's bind() raises
# OverflowError rather than OSError and the preflight dies with a traceback
# instead of reporting an unusable port. Refused here, by name.
#
# THE STRIDE IS THREE EVEN AS ROOT, WHICH IS DELIBERATE AND SLIGHTLY
# CONSERVATIVE. As root the launch-probe block is skipped and a pass touches
# only two ports, so bases 65531-65533 could in principle be made to work by
# making the stride depend on `id -u`. They are refused instead: a root-only
# port layout is a second arrangement to reason about, and the stride would
# become a hand-maintained copy of "how many ports does a pass use" — a fact
# owned by the preflight's address list, one edit away from disagreeing with it.
# The cost of refusing is three values of a debugging variable at the very top
# of the range, and the message names the usable maximum. Raised in review as a
# P3; declined on purpose rather than missed.
pick_port_base() { # pick_port_base <pass-index>
  if [ -n "${OATH_TEST_PORT:-}" ]; then
    highest=$((OATH_TEST_PORT + 3 * (npasses - 1) + 2))
    if [ "$highest" -gt 65535 ]; then
      echo "FAIL setup: OATH_TEST_PORT=$OATH_TEST_PORT leaves no room for $npasses passes —" >&2
      echo "  each takes three consecutive ports and the last would need $highest." >&2
      echo "  Pin a base at or below $((65535 - 3 * (npasses - 1) - 2))." >&2
      return 1
    fi
    printf %s "$((OATH_TEST_PORT + 3 * $1))"
    return 0
  fi
  python3 - <<'PICKPORT'
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
    # base+2 is a loopback bind. The preflight now probes the wildcard rows in
    # the family the BACKEND binds, so this stays dual-stack deliberately: it is
    # the stronger of the two, so a triple it accepts is one both preflights
    # accept, which is the direction the invariant above needs.
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
}

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
#
# AND IT RUNS PER BACKEND, over that backend's own triple: the go pass proving
# its addresses were free says nothing about the addresses the llvm pass draws.
#
# THE WILDCARD FAMILY IS ALSO PER BACKEND, and that is the same rule rather than
# a new one. "Mirror what the server does" was written when there was one
# server; there are now two and they do NOT make the same call. Go's
# ListenAndServe(":PORT") opens a DUAL-STACK IPv6 wildcard; the llvm runtime
# binds an AF_INET `sockaddr_in` (oath/llvm.go, `socket(AF_INET, ...)`). Probing
# dual-stack for the llvm pass never passes a port the llvm server would fail on
# — it is strictly stronger — but it FAILS SETUP on a port held only on IPv6
# that llvm could serve perfectly well, which aborts a run that had nothing
# wrong with it. The loopback row needs no split: both runtimes bind AF_INET
# there, and so does the probe.
#
# ONE BINDER, TWO CALLERS. The pass preflight below and the second server
# started late in the pass both have to ask "can this backend bind here?", and
# two spellings of that question would be free to disagree — which is this
# repo's most familiar defect, arriving in a probe whose whole value is that it
# agrees with the server by construction.
backend_family() { # backend_family <backend> -- how this backend's wildcard binds
  case "$1" in
    llvm) printf v4 ;;    # socket(AF_INET, SOCK_STREAM, 0) in the emitted runtime
    *)    printf dual ;;  # net.Listen's dual-stack IPv6 wildcard
  esac
}

bind_probe() { # bind_probe <host:port> <family> -- 0 if bindable, message on stderr
  python3 - "$1" "$2" <<'PORTCHECK'
import socket, sys

host, _, port = sys.argv[1].rpartition(":")
port = int(port)
family = sys.argv[2]

# MIRROR WHAT THE SERVER DOES, rather than testing a convenient approximation.
# An AF_INET probe of 0.0.0.0 can succeed while a dual-stack server's real bind
# fails against an IPv6-only holder, and a dual-stack probe can fail where an
# AF_INET server would have bound. The point of binding instead of listing
# listeners is that the probe agrees with the server BY CONSTRUCTION — which
# only holds while it makes the same call, and which call that is now depends on
# which backend built the binary.
try:
    if host == "" and family == "dual" and socket.has_dualstack_ipv6():
        s = socket.create_server(("", port), family=socket.AF_INET6,
                                 dualstack_ipv6=True, reuse_port=False)
    else:
        s = socket.create_server((host, port), reuse_port=False)
except OSError as e:
    sys.stderr.write("%s is unavailable: %s\n" % (sys.argv[1], e))
    sys.exit(1)
s.close()
PORTCHECK
}

preflight_ports() { # preflight_ports <base> <backend> -- 0 if every address is free
  family=$(backend_family "$2")
  addrs=":$1 :$(($1 + 1))"
  if [ "$(id -u)" != "0" ]; then
    addrs="$addrs 127.0.0.1:$(($1 + 2))"
  fi
  ports_ok=1
  for spec in $addrs; do
    bind_probe "$spec" "$family" || ports_ok=0
  done
  [ "$ports_ok" = "1" ]
}

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
      echo "FAIL setup [$backend]: the server exited instead of serving $2 — most likely it"
      echo "  lost the port to another process, so anything answering there is not ours."
      exit 1
      ;;
  esac
}

# check COUNTS as well as asserts, and the count is what makes a short pass
# visible. `checks` is per-backend; run_backend resets it and the summary
# requires the two passes to agree.
check() { # check <label> <expected> <actual>
  checks=$((checks + 1))
  if [ "$2" = "$3" ]; then
    printf '  ok    %-4s %-46s %s\n' "$backend" "$1" "$3"
  else
    printf '  FAIL  %-4s %-46s want %s, got %s\n' "$backend" "$1" "$2" "$3"
    fail=$((fail + 1))
  fi
}

post() { # post <url> <content-type> <event> <delivery> <sig> <body-file>
  curl -sS -o /dev/null -w '%{http_code}' -X POST "$1" \
    -H "Content-Type: $2" -H "X-GitHub-Event: $3" -H "X-GitHub-Delivery: $4" \
    -H "X-Hub-Signature-256: $5" --data-binary "@$6"
}
sign() { openssl dgst -sha256 -mac HMAC -macopt "key:$1" -binary < "$2" | od -An -v -tx1 | tr -d ' \n'; }

# The launch gate probes, before anything is served: an unwritable sink must stop
# the program starting, not make every write fail silently.
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
  env "$@" "$bin" > /dev/null 2>&1 &
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

# ---------------------------------------------------------------------------
# ONE PASS. Everything below the divider is the sequence that used to be the
# body of this script, unchanged in what it asserts, parameterised only by which
# backend built the binary and which triple of ports it runs on.
# ---------------------------------------------------------------------------
run_backend() { # run_backend <backend> <pass-index>
  backend="$1"
  checks=0
  fail=0
  work_b="$work/$backend"
  mkdir -p "$work_b"

  echo
  echo "=== backend: $backend ==="

  # pick_port_base prints its own reason on stderr when it refuses a pinned
  # base; this line adds the pass it happened in without restating the cause.
  port=$(pick_port_base "$2") || {
    echo "FAIL setup [$backend]: no port triple for this pass (see above)"; exit 1; }
  url="http://127.0.0.1:$port/hook"

  if ! preflight_ports "$port" "$backend"; then
    echo "FAIL setup [$backend]: an address this suite needs is taken, so it would measure"
    echo "  whatever is there and report the difference as application defects."
    echo "  Stop what is holding it, or set OATH_TEST_PORT to a free base port."
    exit 1
  fi

  # A body shaped like a GitHub delivery: compact JSON with a repository object.
  printf '{"action":"opened","repository":{"id":1,"full_name":"miclip/oath-lang"},"sender":{"login":"miclip"}}' > "$work_b/body.json"

  # A COPY OF THE STORE, because `oath put` is the only way to check a source
  # file and it always writes: even a put whose every definition is a no-op
  # appends one journal line per definition. A gate that mutates the store it is
  # measuring is exactly the failure CLAUDE.md warns about, and in CI it would
  # leave `codebase/` dirty on every run. 2.6 MB is a cheap price for hermetic.
  #
  # PER BACKEND, not shared: a put is a write, and two passes sharing one store
  # would make the second pass's journal depend on the first's — which is the
  # same mutation hazard one level in.
  cp -R "$root/codebase" "$work_b/codebase"
  export OATH_STORE="$work_b/codebase"

  echo "building ($backend)"
  if ! "$oath" put "$here/webhook.oath" > "$work_b/put.out" 2>&1; then
    echo "  FAIL  oath put rejected the application:"; sed 's/^/    /' "$work_b/put.out"; exit 1
  fi
  bin="$work_b/gh-webhookd"
  if ! "$oath" build gh-webhook --backend "$backend" -o "$bin" > "$work_b/build.out" 2>&1; then
    echo "  FAIL  oath build --backend $backend failed:"; sed 's/^/    /' "$work_b/build.out"; exit 1
  fi

  mkdir -p "$work_b/nodir" && chmod 000 "$work_b/nodir" 2>/dev/null || true
  if [ "$(id -u)" != "0" ]; then
    check "unwritable sink refuses to launch" "70" \
          "$(launch_code OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work_b/nodir/x.log" OATH_HTTP_ADDR=":$port")"

    # AND IT REFUSES BEFORE BINDING, which the check above does NOT witness: a
    # program that bound the port, then resolved capabilities, then exited 70
    # would satisfy it exactly. Verified by emitting exactly that order — the
    # check above still passed and this one failed with EADDRINUSE.
    #
    # So HOLD THE PORT and see which failure wins. Provisioning first is 70;
    # binding first is 1. The second case is the CONTROL — without it, "70" could
    # just mean the program never got as far as either.
    # THE HOLDER MUST BIND THE WAY THE PREFLIGHT BOUND, which is the same rule
    # the selector already follows one screen up, arriving one binder later.
    # `socket.socket(); bind()` sets no SO_REUSEADDR, while `create_server` sets
    # it by default — so a port the preflight CERTIFIED free could be refused
    # here, and the branch below then reported a broken harness. Not
    # hypothetical: running the sequence twice churns enough ephemeral ports
    # that an outbound curl leaves port+2 in TIME_WAIT, and `make check-app`
    # failed this on both backends at once. A bare bind refuses such a port;
    # create_server takes it. Two listeners still cannot coexist (that would
    # need SO_REUSEPORT), so the hold this establishes is as real as before.
    python3 -c "
import socket,time,sys
s=socket.create_server(('127.0.0.1',$((port+2))))
sys.stderr.write('held\\n'); sys.stderr.flush(); time.sleep(30)" 2> "$work_b/held" &
    holder=$!
    i=0; while [ $i -lt 50 ] && ! grep -q held "$work_b/held" 2>/dev/null; do i=$((i+1)); sleep 0.1; done

    # THE HOLDER MUST ACTUALLY HOLD THE PORT. If it never bound, both probes below
    # would see a FREE port, the control would return 70 instead of 1, and the
    # pair would read as a timing regression rather than a broken harness. A check
    # that cannot tell its own setup failed from the defect it hunts is worse than
    # no check.
    if grep -q held "$work_b/held" 2>/dev/null; then
      check "capabilities resolve BEFORE the port is bound" "70" \
            "$(launch_code OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work_b/nodir/x.log" OATH_HTTP_ADDR="127.0.0.1:$((port+2))")"
      check "  ...control: a provisionable sink reaches bind" "1" \
            "$(launch_code OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work_b/held.log" OATH_HTTP_ADDR="127.0.0.1:$((port+2))")"
    else
      # COUNTED AS THE TWO CHECKS IT REPLACES. This fork runs a different number
      # of assertions than the branch above, so leaving it uncounted would make
      # a broken harness show up as a per-backend count MISMATCH attributed to
      # the backend. It is a setup failure and says so; the count says how much
      # of the sequence it stood in for.
      printf '  FAIL  %-4s %-46s could not hold 127.0.0.1:%s\n' "$backend" "port-holder setup" "$((port+2))"
      fail=$((fail + 1))
      checks=$((checks + 2))
    fi
    # `|| true` on the kill as well as the wait — the same set -e trap as in
    # launch_code, one branch over. When the holder FAILED to bind it has already
    # exited, so this kill returns non-zero and aborts the suite before the
    # remaining checks and the summary. The setup-failure branch printed its FAIL
    # and then silently took the whole run with it.
    kill "$holder" 2>/dev/null || true; wait "$holder" 2>/dev/null || true; holder=""
  fi
  chmod 755 "$work_b/nodir" 2>/dev/null || true

  OATH_VALUE_SECRET="$secret" OATH_EMIT_PATH="$work_b/events.log" \
    OATH_HTTP_ADDR=":$port" "$bin" > "$work_b/out" 2> "$work_b/err" &
  pid=$!
  await_ours "$pid" "http://127.0.0.1:$port/"

  echo "serving on :$port ($backend)"
  sig=$(sign "$secret" "$work_b/body.json")

  check "a signed delivery is accepted" \
        "202" "$(post "$url" application/json push d-1 "sha256=$sig" "$work_b/body.json")"
  check "a query string on the hook URL is fine" \
        "202" "$(post "$url?x=1" application/json push d-2 "sha256=$sig" "$work_b/body.json")"
  check "the ping handshake is answered" \
        "200" "$(post "$url" application/json ping d-3 "sha256=$sig" "$work_b/body.json")"
  check "a wrong signature is refused" \
        "401" "$(post "$url" application/json push d-4 "sha256=$(sign wrong-secret-but-long-enough "$work_b/body.json")" "$work_b/body.json")"
  check "an unprefixed digest is not a signature" \
        "401" "$(post "$url" application/json push d-5 "$sig" "$work_b/body.json")"
  # `hex-decode` USED to keep the prefix it managed to decode, so `<valid digest>zz`
  # decoded to the same 32 bytes as the valid digest; the length check in
  # `gh-signature` closed it here, and #121 later closed it in the decoder too. This
  # check therefore now passes for two independent reasons — which is the point of
  # keeping it: it fails the moment either one stops holding.
  check "a valid digest with trailing junk is refused" \
        "401" "$(post "$url" application/json push d-5b "sha256=${sig}zz" "$work_b/body.json")"
  check "an unsigned request is refused" \
        "401" "$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$url" -H 'Content-Type: application/json' --data-binary "@$work_b/body.json")"
  check "a signed delivery to the wrong path 404s" \
        "404" "$(post "http://127.0.0.1:$port/hoook" application/json push d-6 "sha256=$sig" "$work_b/body.json")"
  check "a form-encoded delivery 415s" \
        "415" "$(post "$url" application/x-www-form-urlencoded push d-7 "sha256=$sig" "$work_b/body.json")"
  check "a charset parameter is still JSON" \
        "202" "$(post "$url" 'application/json; charset=utf-8' push d-8 "sha256=$sig" "$work_b/body.json")"
  # application/jsonp STARTS WITH application/json and is not JSON. A prefix test
  # accepted it — found by review, not by the 13 properties, because the property
  # guarding this case restated the implementation's own predicate.
  check "a longer media type is not a match" \
        "415" "$(post "$url" application/jsonp push d-9 "sha256=$sig" "$work_b/body.json")"
  check "a space is not a parameter delimiter" \
        "415" "$(post "$url" 'application/json payload' push d-10 "sha256=$sig" "$work_b/body.json")"
  check "a GET is a 405, not a 401" \
        "405" "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/hook")"

  # THE ACCEPT PATH ACTUALLY RAN, and the JSON scan ran with it. A receiver that
  # answered 202 without reading the body would pass every check above.
  check "the repository is read out of the signed body" \
        "miclip/oath-lang" "$(head -1 "$work_b/events.log" | cut -f4)"
  check "the record declares its schema" \
        "oath-gh/1" "$(head -1 "$work_b/events.log" | cut -f1)"
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
  inject_before=$(wc -l < "$work_b/events.log" | tr -d ' ')
  post "$url" application/json "ev${tab}il" d-inj-1 "sha256=$sig" "$work_b/body.json" > /dev/null
  post "$url" application/json push "de${tab}liv" "sha256=$sig" "$work_b/body.json" > /dev/null
  check "a tab in an unsigned header arrives as one byte" \
        "1" "$(printf %s "$tab" | wc -c | tr -d ' ')"
  check "two injected deliveries make two records" \
        "2" "$(( $(wc -l < "$work_b/events.log" | tr -d ' ') - inject_before ))"
  check "no record has anything but five fields" \
        "5" "$(awk -F'\t' 'NF != 5 { print NF; exit } END { print 5 }' "$work_b/events.log" | head -1)"
  check "the ping wrote nothing" \
        "0" "$(cut -f2 "$work_b/events.log" | grep -c '^d-3$' || true)"
  # `&& echo 0 || echo 1` rather than `; echo $?`: under `set -e` a non-zero exit
  # inside command substitution aborts the subshell before the echo runs, so the
  # check reads back an empty string and the FAILING case cannot be observed at
  # all. The version that could only see success passed while proving nothing.
  check "the consumer accepts the log" \
        "0" "$("$here/report.sh" "$work_b/events.log" > /dev/null 2>&1 && echo 0 || echo 1)"
  printf '1785000000\tpush\tolder-format\n' > "$work_b/old.log"
  check "the consumer refuses a foreign format" \
        "1" "$("$here/report.sh" "$work_b/old.log" > /dev/null 2>&1 && echo 0 || echo 1)"
  # The TAG is not the schema; the tag and the field count are. A truncated line
  # carrying oath-gh/1 would otherwise aggregate as an empty repository.
  printf 'oath-gh/1\td\tpush\n' > "$work_b/short.log"
  check "the consumer refuses a truncated record" \
        "1" "$("$here/report.sh" "$work_b/short.log" > /dev/null 2>&1 && echo 0 || echo 1)"
  # A redelivery must be counted ONCE in the aggregate, not merely noticed in the
  # header. Two records, one delivery id, one line of output.
  printf 'oath-gh/1\tdup\tpush\tr/r\t1\noath-gh/1\tdup\tpush\tr/r\t2\n' > "$work_b/dup.log"
  check "a redelivery is aggregated once" \
        "1" "$("$here/report.sh" "$work_b/dup.log" | grep -c 'push')"

  kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null || true; pid=""

  # A NON-ASCII SECRET. `str-bytes` yields codepoints and hmac-sha256 wants bytes,
  # so before secret-is-usable checked for it, a correctly signed delivery under a
  # 32-character Cyrillic secret PANICKED the request handler: empty reply, stack
  # trace, process still listening. 500 is the answer; a dropped connection is not.
  env OATH_VALUE_SECRET="ключключключключключключключключ" OATH_EMIT_PATH="$work_b/utf8.log" \
    OATH_HTTP_ADDR=":$port" "$bin" > /dev/null 2>&1 &
  pid=$!
  await_ours "$pid" "http://127.0.0.1:$port/"
  utf8sig=$(python3 -c "import hmac,hashlib;print(hmac.new('ключключключключключключключключ'.encode(),open('$work_b/body.json','rb').read(),hashlib.sha256).hexdigest())")
  check "a non-encodable secret is refused, not a panic" \
        "500" "$(post "$url" application/json push utf8 "sha256=$utf8sig" "$work_b/body.json")"
  kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null || true; pid=""

  # LATIN-1 IS THE QUIET ONE. Every codepoint is in byte range so nothing errors,
  # but str-bytes yields the codepoint while openssl signs UTF-8 — the digests
  # disagree and every legitimate delivery would 401 forever. 500 says the
  # configuration is wrong, which is what it is.
  latin1="ééééééééééééééééééééééééééééééé"
  env OATH_VALUE_SECRET="$latin1" OATH_EMIT_PATH="$work_b/latin1.log" \
    OATH_HTTP_ADDR=":$port" "$bin" > /dev/null 2>&1 &
  pid=$!
  await_ours "$pid" "http://127.0.0.1:$port/"
  # THE SECOND SERVER GETS THE SAME GUARANTEES AS THE FIRST, and used to get
  # none of them. It lived inside a command substitution, which cost it all
  # three: `await_ours` calls `exit 1`, and an exit inside `$( )` leaves only the
  # subshell, so it could not be used; a bare `c=$(post ...)` dies under `set -e`
  # before its own `echo`, so a curl that never connected read back as the EMPTY
  # STRING and was reported as the application's answer; and its readiness loop
  # had no death check at all. Measured, not reasoned about: 2 of 12 runs failed
  # here with `want 500, got ` — an empty status, printed as though the receiver
  # had produced it.
  #
  # THE PORT IS RE-PROBED IMMEDIATELY BEFORE THE BIND. port+1 was certified at
  # the top of the pass and is bound roughly ten seconds later, after dozens of
  # loopback connections — ample time for the kernel to hand it out as an
  # ephemeral source port. The pass preflight gives the MAIN server a
  # just-checked port; without this the second server was trading on a
  # certificate that had gone stale, which is a different guarantee wearing the
  # same words. Same binder as the preflight (`bind_probe`), same family, so the
  # two cannot disagree.
  #
  # NO RETRY AND NO SKIP. Every branch below either runs the 500 assertion or
  # fails setup loudly; none of them makes a bad run look like a good one.
  if ! bind_probe ":$((port + 1))" "$(backend_family "$backend")"; then
    echo "FAIL setup [$backend]: :$((port + 1)) was free at the start of this pass and is"
    echo "  not free now, so the second server would measure whatever took it. Not an"
    echo "  application result — nothing was asserted."
    exit 1
  fi
  env OATH_VALUE_SECRET="0123456789 abcdef" OATH_EMIT_PATH="$work_b/sp.log" \
    OATH_HTTP_ADDR=":$((port + 1))" "$bin" > /dev/null 2>&1 &
  sp=$!
  # The same helper the main servers use: it polls for an answer and then fails
  # SETUP if the child is dead or a zombie, so a server that lost the port can
  # never be read as a receiver that answered oddly.
  await_ours "$sp" "http://127.0.0.1:$((port + 1))/"
  # A NON-STATUS VALUE FOR A NON-ANSWER. `if sp_code=$(post ...)` keeps the
  # failing case observable — the assignment's own exit status is the branch
  # condition, so `set -e` never gets to abort anything — and the value it
  # substitutes is deliberately not a number, so a curl that could not speak to a
  # server we have already verified is alive is reported as exactly that rather
  # than as an HTTP status the receiver never sent.
  if sp_code=$(post "http://127.0.0.1:$((port + 1))/hook" application/json push sp \
                    "sha256=$(sign "0123456789 abcdef" "$work_b/body.json")" \
                    "$work_b/body.json"); then :; else
    sp_code="curl reached no answer from a live server"
  fi
  check "a secret with a space is refused" "500" "$sp_code"
  kill "$sp" 2>/dev/null || true; wait "$sp" 2>/dev/null || true; sp=""
  check "a latin-1 secret is refused, not mis-signed" \
        "500" "$(post "$url" application/json push l1 "sha256=$(sign "$latin1" "$work_b/body.json")" "$work_b/body.json")"
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
        "$(launch_code -u OATH_VALUE_SECRET OATH_EMIT_PATH="$work_b/forged.log" OATH_HTTP_ADDR=":$port")"
  check "an EMPTY secret: refuses to launch" "70" \
        "$(launch_code OATH_VALUE_SECRET="" OATH_EMIT_PATH="$work_b/forged.log" OATH_HTTP_ADDR=":$port")"
  check "  ...and nothing was recorded" "0" "$(wc -l < "$work_b/forged.log" 2>/dev/null | tr -d ' ' || echo 0)"

  # MISSING AND EMPTY ARE DIFFERENT OPERATOR MISTAKES and say so. Collapsing them
  # is the same defect as answering "" for an absent capability.
  : > "$work_b/forged.log"
  env -u OATH_VALUE_SECRET OATH_EMIT_PATH="$work_b/forged.log" OATH_HTTP_ADDR=":$port" "$bin" 2> "$work_b/m1" >/dev/null || true
  OATH_VALUE_SECRET="" OATH_EMIT_PATH="$work_b/forged.log" OATH_HTTP_ADDR=":$port" "$bin" 2> "$work_b/m2" >/dev/null || true
  check "missing says 'is not provided'" \
        "1" "$(grep -c 'is not provided' "$work_b/m1" || true)"
  check "empty says 'provided but empty'" \
        "1" "$(grep -c 'provided but empty' "$work_b/m2" || true)"

  # AND APPLICATION POLICY STAYS IN OATH. A secret that is present and non-empty
  # satisfies PROVISIONING, so the program launches; `secret-is-usable`'s length
  # and printable-ASCII rules are the artifact's own and still answer 500. #126
  # makes one class of misconfiguration unreachable; it does not absorb policy.

  if [ "$fail" = "0" ]; then
    printf '  %s: %d checks, all passed\n' "$backend" "$checks"
  else
    printf '  %s: %d checks, %d FAILED\n' "$backend" "$checks" "$fail"
  fi
}

# ---------------------------------------------------------------------------
# BOTH PASSES, THEN ONE SUMMARY OVER BOTH.
# ---------------------------------------------------------------------------

# BOTH BACKENDS ARE REQUIRED, ON EVERY MACHINE. clang is a dependency of this
# suite, not an optional extra: the claim it makes is that the go and llvm
# lowerings of one program behave identically, and a run that measured one of
# them has not made that claim at all. There is no environment in which a skip
# would be the right answer, because there is no environment in which half a
# differential is evidence.
#
# The earlier version skipped locally and failed under CI, borrowing
# oath/llvm_test.go's policy. That policy fits a TEST — one of hundreds, whose
# absence a reader can see in the run's own skip list. It does not fit a GATE
# whose entire subject is the agreement between two backends, and a skip that
# still exits 0 is a partial pass wearing a success code.
if ! command -v clang > /dev/null 2>&1; then
  echo "FAIL setup: clang is not on PATH, so the llvm backend cannot be built."
  echo "  This suite's claim is that both backends behave identically, which a run"
  echo "  over one of them does not establish. Install clang (brew install llvm, or"
  echo "  apt-get install clang) — there is no partial mode."
  exit 1
fi

npasses=2

total_checks=0
total_fail=0
ran=""
# `pass`, NOT `i`. POSIX sh has no locals, and `i` is the retry counter in
# await_ours and in the port-holder wait — so a loop index called `i` is
# overwritten by however many times a server took to answer, and the llvm pass
# would draw base+6 or base+9 under OATH_TEST_PORT instead of base+3. Timing
# deciding which port a PINNED run uses is the kind of defect that reproduces
# once a month; found by review, not by a run.
pass=0
for b in $BACKENDS; do
  run_backend "$b" "$pass"
  eval "checks_$b=\$checks"
  eval "fail_$b=\$fail"
  total_checks=$((total_checks + checks))
  total_fail=$((total_fail + fail))
  ran="$ran $b"
  pass=$((pass + 1))
done

echo
# EQUAL COUNTS ARE THE INVARIANT. Two passes over the same sequence execute the
# same number of checks, so a difference means one of them did not run the whole
# sequence — a branch taken, a fork skipped — and that cannot be read off the
# failure count, which would be zero in both. Reported as a failure of the SUITE
# rather than of a backend, because neither backend is the thing that is wrong.
summary_fail="$total_fail"
if [ "${checks_go:-0}" != "${checks_llvm:-0}" ]; then
  printf 'FAIL suite: the two passes ran DIFFERENT numbers of checks — go %d, llvm %d.\n' \
         "${checks_go:-0}" "${checks_llvm:-0}"
  echo "  One pass did not run the whole sequence, so a zero failure count means nothing"
  echo "  for it. Compare the two check lists above to find where they diverged."
  summary_fail=$((summary_fail + 1))
fi
printf 'webhook acceptance:%s — go %d checks (%d failed), llvm %d checks (%d failed), %d total\n' \
       "$ran" "${checks_go:-0}" "${fail_go:-0}" "${checks_llvm:-0}" "${fail_llvm:-0}" "$total_checks"
# THE DEFAULTS IN THAT COMPARISON ARE LOAD-BEARING. A pass that never ran leaves
# its variable unset, which reads as 0 and mismatches the other — so the
# invariant catches a missing pass, not merely a short one. It is why the
# comparison is against the recorded per-backend counts rather than against
# $npasses or the loop's own bookkeeping, which would agree with themselves.
if [ "$summary_fail" != "0" ]; then
  echo "webhook acceptance: $summary_fail FAILED"
else
  echo "webhook acceptance: all $total_checks checks passed on both backends"
fi
exit "$summary_fail"
