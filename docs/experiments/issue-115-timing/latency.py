#!/usr/bin/env python3
"""Warm steady-state request latency for the compiled `gh-webhook` handler.

    latency.py <binary> <workdir> <port> <payload> [--warmup W] [--n N] [--label L]

A SEPARATE INSTRUMENT FROM `issue-115-premise/measure.sh`, NOT A MODE ON IT.
That harness starts the server, sends exactly ONE request, kills it and reads
peak RSS out of `/usr/bin/time -l`. Its whole lifecycle is one cold request,
because a peak is a high-water mark and a second request cannot lower it. Warm
steady-state latency is the opposite lifecycle -- one server, many requests,
with the first ones deliberately thrown away -- so the two cannot share a run.

WHAT IT DOES SHARE IS THE ONE THING THAT WOULD DRIFT INVISIBLY: the bytes of a
signed request. A harness that spelled its own headers could measure a
different path through the same binary while reporting the same cell label, and
nothing in either output would say so. So `signed_request()` below is checked,
before any server is launched, against the request `measure.sh` actually builds
-- by executing THAT script's own generator with the socket layer replaced by a
recorder. There is one definition of what a signed request is and it lives in
`measure.sh`; this file holds a copy that is refused if it disagrees.

WHAT THE CLOCK INCLUDES. Two clocks are recorded per request, both
`time.perf_counter_ns()`:

    total   just before `socket.create_connection` .. the recv that returns
            b"" (server EOF).  Connect, write, server work, read.
    rt      just before `sendall` .. the same EOF.  Connect excluded.

Both stop at EOF and NEITHER includes `close()`. The request carries
`Connection: close`, so every request is a fresh TCP connection and the connect
cost cannot be amortised away -- reporting `rt` alongside `total` is what makes
the size of that fixed cost visible instead of buried.

Also inside every figure, because they are inside the program under test: the
handler's `record_sink` append to the emit log, and the loopback stack. This is
the latency of a delivery to this process, not of the handler function.

WARMUP IS EXPLICIT AND DISCARDED, NOT SMOOTHED AWAY. The first requests pay
page faults, allocator growth and first-touch on the request arena; a median
over a run including them is a median of two populations. Warmup requests are
sent, validated exactly as the timed ones are, and their timings dropped.

EVERY REQUEST'S STATUS IS CHECKED, NOT JUST ONE. `202` is what certifies the
handler ran to completion and the JSON scan actually executed; a `401` anywhere
in the run means an earlier refusal was timed instead, and at these percentiles
a handful of refusals would look like a fast tail rather than an invalid run.
The emit log is counted at the end for the same reason from the other side: it
must hold exactly one line per request, all of them the repository name, or the
scan did not find the key it was placed at.
"""
import hashlib
import hmac
import json
import os
import re
import signal
import socket
import statistics
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
MEASURE_SH = os.path.join(
    os.path.dirname(HERE), "issue-115-premise", "measure.sh")

EXPECT_STATUS = "HTTP/1.1 202 Accepted"
EXPECT_EMIT = "miclip/oath-lang"
KEY = b'"repository":{'


def measure_sh() -> str:
    with open(MEASURE_SH) as fh:
        return fh.read()


def secret() -> str:
    """The signing secret, READ OUT OF `measure.sh` rather than copied here.

    A constant in this file would be a second authority for a value that
    `measure.sh` already owns, and the failure would be silent in the worst
    possible way: the request comparison below invokes that script's OWN
    generator, so a local copy is handed to BOTH sides and they agree with each
    other while neither agrees with what `measure.sh` actually signs. A
    duplicate that both halves of a check share is not checked at all.

    It also feeds the server's `OATH_VALUE_SECRET`, so the server, the client
    and the reference harness cannot drift apart in any combination.
    """
    m = re.search(r"^secret=(\S+)$", measure_sh(), re.M)
    if not m:
        raise SystemExit(
            "REFUSED: no `secret=` assignment found in %s -- the value this "
            "harness must share with it cannot be derived, so the check did "
            "NOT run" % MEASURE_SH)
    return m.group(1)


def axes(body: bytes):
    """The cell's coordinates, READ OFF THE PAYLOAD rather than taken on trust.

    `--label` is free text. Nothing in a status code or an emit line depends on
    WHICH body was sent, so a mistyped label or a swapped payload produces a
    run that passes every other check in this file while attributing perfectly
    valid timings to the wrong grid cell -- and a table assembled from those
    labels reports a grid it does not have. The size and the key's offset are
    facts about the bytes, so they are measured and emitted alongside every
    result, and a label that states them must agree.

    The key must occur EXACTLY ONCE, for `mksize.py`'s reason: a pad that
    happened to contain it would give the scan an earlier stop, and the
    measured position would not be the requested one.
    """
    if body.count(KEY) != 1:
        raise SystemExit(
            "REFUSED: %r occurs %d times in the payload, not once -- the scan's "
            "stopping point is not the offset this cell claims"
            % (KEY.decode(), body.count(KEY)))
    off = body.index(KEY)
    return len(body), off, 100.0 * off / len(body)


def check_label(label: str, n: int, pct_of_body: float) -> None:
    """A label of the form `<bytes>/<pct>%` must match the payload it names.

    Labels that do not parse (`control-A`) are left alone: the control's
    identity is that the SAME file is passed at both ends, which is the
    driver's business, and inventing a convention for it here would reject
    correct runs. What this closes is the case where a label DOES make a
    claim about the axes and the claim is false.
    """
    m = re.match(r"^(\d+)/(\d+(?:\.\d+)?)%$", label)
    if not m:
        return
    want_n, want_pct = int(m.group(1)), float(m.group(2))
    if want_n != n or round(pct_of_body, 1) != round(want_pct, 1):
        raise SystemExit(
            "REFUSED: label %r claims %d bytes at %.1f%%, but the payload is "
            "%d bytes with the key at %.1f%% -- the timings would be filed "
            "under a cell they do not belong to"
            % (label, want_n, want_pct, n, pct_of_body))


def signed_request(body: bytes, secret: str) -> bytes:
    """The bytes of one correctly-signed delivery. Checked against measure.sh."""
    mac = hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return (b"POST /hook HTTP/1.1\r\nHost: 127.0.0.1\r\n"
            b"Content-Type: application/json\r\n"
            b"X-GitHub-Event: push\r\n"
            b"X-GitHub-Delivery: 00000000-0000-0000-0000-000000000001\r\n"
            b"X-Hub-Signature-256: sha256=" + mac.encode() + b"\r\n"
            b"Content-Length: " + str(len(body)).encode() + b"\r\n"
            b"Connection: close\r\n\r\n") + body


def _measure_sh_request(payload: str) -> bytes:
    """What `measure.sh` sends in `signed` mode, captured without a server.

    Its generator is embedded in the shell script as a heredoc, so it is
    extracted and executed here with `socket.create_connection` replaced by a
    recorder. Executing it is the point: a regex that merely LOOKED at the
    header lines would be a second reading of the same text and could agree
    with a builder that had drifted in some byte it did not think to check.
    """
    m = re.search(r"<<'PY'\n(.*?)\nPY\n", measure_sh(), re.S)
    if not m:
        raise SystemExit(
            "REFUSED: no python heredoc found in %s -- the harness this file "
            "checks itself against has changed shape, so the check did NOT "
            "run" % MEASURE_SH)
    captured = []

    class _Rec:
        def sendall(self, b):
            captured.append(b)

        def recv(self, _n):
            return b""

        def close(self):
            pass

    real_cc = socket.create_connection
    real_argv, real_stdout = sys.argv, sys.stdout
    socket.create_connection = lambda *a, **k: _Rec()
    sys.argv = ["measure-builder", "0", payload, "signed", secret()]
    try:
        sys.stdout = open(os.devnull, "w")
        exec(compile(m.group(1), MEASURE_SH + ":heredoc", "exec"), {})
    finally:
        sys.stdout.close()
        socket.create_connection = real_cc
        sys.argv, sys.stdout = real_argv, real_stdout
    if not captured:
        raise SystemExit(
            "REFUSED: measure.sh's generator sent nothing; the check did NOT "
            "run")
    return b"".join(captured)


def selfcheck(payload: str) -> None:
    """Run before any server is launched, so a disagreement writes no data."""
    body = open(payload, "rb").read()
    mine, theirs = signed_request(body, secret()), _measure_sh_request(payload)
    if mine != theirs:
        raise SystemExit(
            "REFUSED: this harness's signed request differs from measure.sh's "
            "(%d vs %d bytes). The two would be timing different paths through "
            "the same binary under the same cell label." % (len(mine),
                                                            len(theirs)))


def one(port: int, req: bytes):
    """Send one request, return (total_ns, rt_ns, status-line)."""
    t0 = time.perf_counter_ns()
    s = socket.create_connection(("127.0.0.1", port), timeout=120)
    s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    t1 = time.perf_counter_ns()
    s.sendall(req)
    out = b""
    while True:
        c = s.recv(65536)
        if not c:
            break
        out += c
    t2 = time.perf_counter_ns()
    s.close()
    status = (out.split(b"\r\n")[0].decode("latin1") if out
              else "<no response: connection closed empty>")
    return t2 - t0, t2 - t1, status


def rank(ordered, num, den):
    """The nearest-rank order statistic: the ceil(num/den * n)-th smallest,
    1-indexed, of an already-sorted sequence.

    STATED, NOT ASSUMED: with a few hundred samples the interpolating and
    nearest-rank conventions differ visibly in the tail, and a percentile whose
    method is unnamed is not reproducible.

    THE RANK IS COMPUTED IN INTEGER ARITHMETIC AND THE QUANTILE IS PASSED AS A
    RATIO, not as a float. `ceil(p*n)` evaluated through a binary float rounds
    the wrong way for some (p, n): 0.99*2099 is 2078.0099999999998, and a
    truncating ceil of that lands on rank 2078 where the definition says 2079.
    It is an off-by-one that appears for some sample counts and not others,
    which is the hardest kind to notice -- the figure stays plausible and the
    documented method stops describing the computation.

    THE MEDIAN GOES THROUGH THIS TOO, at num/den = 1/2. `statistics.median`
    AVERAGES the two central samples at even n, which is a different estimator
    from the one this document names; with 10,000 samples the two agree to far
    inside the reported precision, but a figure whose stated method is not the
    method used is not reproducible from the statement.
    """
    n = len(ordered)
    k = -(-num * n // den)                  # ceil(num*n/den), exactly
    return ordered[min(max(k, 1), n) - 1]


def main() -> None:
    args = sys.argv[1:]
    if len(args) < 4:
        raise SystemExit(__doc__.strip().splitlines()[2].strip())
    binary, work, port, payload = args[0], args[1], int(args[2]), args[3]
    rest, warmup, n, label = args[4:], 50, 300, os.path.basename(payload)
    while rest:
        flag, rest = rest[0], rest[1:]
        if flag == "--warmup":
            warmup, rest = int(rest[0]), rest[1:]
        elif flag == "--n":
            n, rest = int(rest[0]), rest[1:]
        elif flag == "--label":
            label, rest = rest[0], rest[1:]
        else:
            raise SystemExit("unknown flag %r" % flag)

    selfcheck(payload)

    body = open(payload, "rb").read()
    nbytes, key_offset, key_pct = axes(body)
    check_label(label, nbytes, key_pct)
    sec = secret()
    req = signed_request(body, sec)
    os.makedirs(work, exist_ok=True)
    emit = os.path.join(work, "emit.log")
    open(emit, "w").close()
    errpath = os.path.join(work, "server.err")
    err = open(errpath, "w")
    env = dict(os.environ,
               OATH_VALUE_SECRET=sec,
               OATH_EMIT_PATH=emit,
               OATH_HTTP_ADDR="127.0.0.1:%d" % port)
    # HANDLERS BEFORE THE CHILD EXISTS. Installing them after `Popen` leaves a
    # window in which a signal terminates this harness under the default action
    # while the server is already running — orphaned, still holding its port.
    # The holder is mutable so the handler can be armed before there is
    # anything to reap.
    _child = []

    def stop():
        if _child and _child[0].poll() is None:
            p_ = _child[0]
            p_.send_signal(signal.SIGTERM)
            try:
                p_.wait(timeout=10)
            except subprocess.TimeoutExpired:
                p_.kill()
                p_.wait()

    def _reap(signum, _frame):
        stop()
        signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)

    for _sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        try:
            signal.signal(_sig, _reap)
        except (ValueError, OSError):
            pass  # not on the main thread, or unavailable on this platform

    proc = subprocess.Popen([binary], stdout=open(
        os.path.join(work, "server.out"), "w"), stderr=err, env=env)
    _child.append(proc)


    try:
        # WAIT FOR **THIS** SERVER TO REPORT ITS OWN BIND, not for the port to
        # answer: a connect probe is satisfied by a stale listener that already
        # owns the port, and the run would then time one process while
        # attributing the figures to another. Same discipline as measure.sh,
        # and for the same reason.
        want = "handler listening on 127.0.0.1:%d" % port
        deadline = time.time() + 15
        while time.time() < deadline:
            err.flush()
            if want in open(errpath).read():
                break
            if proc.poll() is not None:
                break
            time.sleep(0.05)
        err.flush()
        if want not in open(errpath).read():
            sys.stderr.write("SETUP FAIL: this server never reported binding "
                             "127.0.0.1:%d\n%s\n" % (port, open(errpath).read()))
            raise SystemExit(1)

        samples, bad = [], []
        for i in range(warmup + n):
            total, rt, status = one(port, req)
            if status != EXPECT_STATUS:
                bad.append((i, status))
                if len(bad) > 3:
                    break
            if i >= warmup:
                samples.append((total, rt))
        if bad:
            raise SystemExit(
                "MEASUREMENT INVALID: %d request(s) did not answer %r; first "
                "was request %d with %r. The run did not exercise the path it "
                "claims." % (len(bad), EXPECT_STATUS, bad[0][0], bad[0][1]))
        if proc.poll() is not None:
            raise SystemExit(
                "MEASUREMENT INVALID: server exited during the run (%s)"
                % errpath)
    finally:
        stop()

    lines = [l.strip() for l in open(emit) if l.strip()]
    # EXACT FIELD, NOT SUBSTRING. The record is five tab-separated fields and
    # the repository is the fourth; `EXPECT_EMIT in line` would accept
    # `miclip/oath-lang-fork`, or the name appearing in any other column, and
    # would then certify a run that measured the wrong output. This is the
    # witness that the JSON scan reached the field the timing is attributed to,
    # so it has to test that and not something it implies.
    def _bad(l):
        f = l.split("\t")
        return len(f) != 5 or f[3] != EXPECT_EMIT
    if len(lines) != warmup + n or any(_bad(l) for l in lines):
        raise SystemExit(
            "MEASUREMENT INVALID: emit log holds %d lines for %d requests, "
            "expected all to be 5 tab-separated fields with repository %r "
            "-- the JSON scan did not run to the "
            "key on every request" % (len(lines), warmup + n, EXPECT_EMIT))

    tot = [a for a, _ in samples]
    rts = [b for _, b in samples]
    # THE AXES ARE EMITTED, NOT JUST THE LABEL. A later reader assembling a
    # table can then key on facts measured from the payload rather than on a
    # string somebody typed on the command line.
    # THE PAYLOAD'S DIGEST IS PART OF THE RECORD. Size and key offset are the
    # AXES, and two different bodies can share both -- so they identify a CELL
    # and not the work that was done in it. Anything downstream asking "were
    # these two runs measuring the same bytes?" needs the bytes' identity, and
    # a hash is the only answer that cannot be satisfied by a lookalike.
    out = {"label": label, "payload": os.path.basename(payload),
           "payload_sha256": hashlib.sha256(body).hexdigest(),
           "bytes": nbytes, "key_offset": key_offset,
           "key_pct_of_body": round(key_pct, 3),
           "warmup": warmup, "n": len(samples),
           "emit_lines": len(lines)}
    for name, xs in (("total", tot), ("rt", rts)):
        o = sorted(xs)
        # EVERY REPORTED QUANTILE GOES THROUGH ONE FUNCTION, the median
        # included. Two estimators under one heading is how a summary stops
        # matching the method its document names.
        out[name] = {"min_us": round(o[0] / 1000, 1),
                     "median_us": round(rank(o, 1, 2) / 1000, 1),
                     "p95_us": round(rank(o, 95, 100) / 1000, 1),
                     # UNROUNDED, for gates. A KEEP/REJECT threshold applied to
                     # the 0.1 us display value can be moved across the cut by
                     # rounding alone, which would make the verdict a property
                     # of the formatting rather than of the drift.
                     "median_ns": rank(o, 1, 2),
                     "p95_ns": rank(o, 95, 100),
                     "p99_us": round(rank(o, 99, 100) / 1000, 1),
                     "p999_us": round(rank(o, 999, 1000) / 1000, 1),
                     "max_us": round(o[-1] / 1000, 1),
                     "mean_us": round(statistics.fmean(xs) / 1000, 1)}
    # THE RAW SAMPLES ARE PART OF THE OUTPUT, NOT A DEBUG AID. A summary line
    # cannot answer whether a high percentile is stable, whether the run drifted
    # from its first tenth to its last, or what a 99.9th percentile is -- and
    # those are decided after the run, from data that no longer exists if only
    # the summary was kept.
    out["samples_ns_total"] = tot
    out["samples_ns_rt"] = rts
    print(json.dumps(out))


if __name__ == "__main__":
    main()
