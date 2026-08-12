#!/usr/bin/env python3
"""Differential fuzz for the LLVM backend's arbitrary-precision Int.

FOUR ORACLES, NOT TWO. Each generated case asserts a fact this script computed
with Python's arbitrary-precision ints; the generated program then checks that
fact, and `oath eval`, the Go backend and the LLVM backend must all three answer
"all-ok". So a wrong expectation cannot pass unnoticed — the three paths would
agree on "case-N" together — and a wrong LLVM lowering cannot pass either.

WHY FUZZ AT ALL, when the acceptance script already has named cases: hand-picked
cases encode the failure modes their author imagined, and a limb-carry defect
lives at magnitudes nobody thinks to write down. The generator draws bit lengths
that STRADDLE the 32-bit limb boundary (31/32/33, 63/64/65, …) precisely because
that is where a base-2^32 implementation goes wrong, and it is the one thing a
reader of the code cannot supply by reading harder.

Usage:  fuzz.py [cases] [seed]        (defaults: 120 cases, seed 1)
        OATH_BIN=/path/to/oath fuzz.py
"""
import atexit
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile

CASES = int(sys.argv[1]) if len(sys.argv) > 1 else 120
SEED = int(sys.argv[2]) if len(sys.argv) > 2 else 1

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))

# Bit lengths that straddle the 32-bit limb boundary, plus a few far above it.
# A base-2^32 implementation fails at 32k and 32k±1 and nowhere interesting in
# between, so the sample is deliberately not uniform.
WIDTHS = ([1, 2, 3, 7, 8, 15, 16] +
          [31, 32, 33, 63, 64, 65, 95, 96, 97, 127, 128, 129, 191, 192, 193] +
          [255, 256, 257, 400, 401, 700])


def truncdiv(a, b):
    """Truncated quotient and its remainder — NOT Python's floor division.

    `oath eval` truncates toward zero and gives the remainder the DIVIDEND's
    sign: (/ -7 2) is -3 and (% -7 2) is -1, measured rather than assumed.
    Python's // floors, so -7 // 2 is -4, and using it here would generate
    expectations that every correct implementation rejects.
    """
    q = abs(a) // abs(b)
    if (a < 0) != (b < 0):
        q = -q
    return q, a - b * q


def draw(rng):
    """One signed operand. Zero and the exact powers of two are drawn on
    purpose: zero is the value sign-magnitude can spell two ways, and 2^k is
    where a carry crosses a limb."""
    kind = rng.random()
    if kind < 0.06:
        v = 0
    elif kind < 0.18:
        v = 1 << rng.choice(WIDTHS)
    elif kind < 0.30:
        v = (1 << rng.choice(WIDTHS)) - 1
    else:
        v = rng.getrandbits(rng.choice(WIDTHS))
    return -v if rng.random() < 0.45 else v


def limb_boundary_pair(rng):
    """Operands whose sum or difference CROSSES a 32-bit limb boundary.

    THIS IS NOT A REFINEMENT, IT IS A REPAIR, and the way it was found is worth
    more than the code. The first version of this file drew both operands
    independently, which passed a deliberately injected bug — magadd discarding
    its final carry limb — 150 cases at a time. Two independent draws almost
    never sum across a limb boundary: a random k-bit operand has a top limb that
    is not full, so the top limb rarely overflows, and the one defect class this
    fuzzer exists to find was the one it could not reach.

    So the crossing is CONSTRUCTED rather than hoped for: pick a limb boundary
    t = 2^(32m), put a just below it, and choose b to land just past.
    """
    m = rng.randrange(1, 9)
    t = 1 << (32 * m)
    near = rng.randrange(4)
    a = t - 1 - near                      # just under a limb boundary
    b = 1 + rng.randrange(4) + near       # ...so a + b lands just over it
    if rng.random() < 0.3:
        a = t - 1                         # all limbs full: every limb carries
        b = a
    if rng.random() < 0.25:
        a, b = -a, -b                     # the same crossing on the other side
    return a, b


def case(rng):
    """An (Oath condition, label) pair that is TRUE under Z."""
    if rng.random() < 0.30:
        a, b = limb_boundary_pair(rng)
        # The families a carry or borrow reaches — division included, because a
        # divisor sitting exactly on a limb boundary is where the accumulator's
        # r < b invariant is tightest.
        k = rng.choice((0, 1, 3, 8, 9, 10))
    else:
        a, b = draw(rng), draw(rng)
        k = rng.randrange(11)
    if k == 0:
        return f"(== (+ {a} {b}) {a + b})", "add"
    if k == 1:
        return f"(== (- {a} {b}) {a - b})", "sub"
    if k == 2:
        return f"(== (* {a} {b}) {a * b})", "mul"
    if k == 3:
        # CANCELLATION. The interesting one for sign-magnitude: (a+b)-b walks
        # through a magnitude that may be larger than either operand and then
        # back, and equal magnitudes with unlike signs are the only path that
        # can produce a negative zero.
        return f"(== (- (+ {a} {b}) {b}) {a})", "cancel"
    if k == 4:
        return f"(== (+ {a} (- 0 {a})) 0)", "annihilate"
    # Ordering: `not` is not lowered by this backend, so the Bool is funnelled
    # through an `if` into an Int and compared there.
    if k == 5:
        return f"(== (if (< {a} {b}) 1 0) {1 if a < b else 0})", "lt"
    if k == 6:
        return f"(== (if (<= {a} {b}) 1 0) {1 if a <= b else 0})", "le"
    if k == 7:
        return f"(== (if (== {a} {b}) 1 0) {1 if a == b else 0})", "eq"

    # DIVISION. The divisor is forced nonzero — a zero divisor is a runtime
    # FAILURE in all three paths, not a value to compare, so it belongs in the
    # acceptance script's controls and not in a generator whose whole premise is
    # that every case has an answer.
    if b == 0:
        b = 1 + rng.randrange(1 << 40)
        if rng.random() < 0.5:
            b = -b
    q, r = truncdiv(a, b)
    if k == 8:
        return f"(== (/ {a} {b}) {q})", "div"
    if k == 9:
        return f"(== (% {a} {b}) {r})", "mod"
    # THE IDENTITY, which is the one case neither of the two above can fail
    # alone. A quotient and a remainder computed by separate code can each pass
    # their own comparison while disagreeing with each other; a == b*q + r is
    # what binds them, and it is checked against operands rather than against a
    # precomputed answer.
    return f"(== (+ (* {b} (/ {a} {b})) (% {a} {b})) {a})", "divmod-identity"


def build_source(rng):
    conds = [case(rng) for _ in range(CASES)]
    body = '"all-ok"'
    for i in range(len(conds) - 1, -1, -1):
        cond, label = conds[i]
        body = f'(if {cond}\n      {body}\n      "case-{i}-{label}")'
    return conds, f"""; GENERATED by fuzz.py — seed {SEED}, {CASES} cases. Not committed.
(data List [a] (Nil) (Cons a (List a)))
(data Str [] (SNil) (SCons Int Str))
(defn int-fuzz [] [(args (List Str))] Str
  {body}
  (prop all-ok [] (== (int-fuzz (Nil [Str])) "all-ok")))
"""


def decode(out):
    i = out.rfind(" : ")
    if i >= 0:
        out = out[:i]
    return "".join(chr(int(n)) for n in re.findall(r"\(SCons (\d+)", out))


def main():
    rng = random.Random(SEED)
    conds, src = build_source(rng)
    work = tempfile.mkdtemp()
    store = tempfile.mkdtemp()
    # REGISTERED, not wrapped in a context manager: main() leaves through eight
    # different sys.exit paths, and atexit covers every one of them without
    # re-indenting the body around a `with`. Each run compiles three binaries and
    # a store, so leaking them is measured in tens of megabytes per invocation.
    # OATH_FUZZ_KEEP keeps both directories when a failing case needs inspecting
    # — the one time the artifacts are worth more than the space.
    if not os.environ.get("OATH_FUZZ_KEEP"):
        atexit.register(shutil.rmtree, work, ignore_errors=True)
        atexit.register(shutil.rmtree, store, ignore_errors=True)
    else:
        print("OATH_FUZZ_KEEP set — leaving %s and %s behind" % (work, store))
    env = dict(os.environ, OATH_STORE=store, OATH_AUTHOR="claude-main")

    oath = os.environ.get("OATH_BIN")
    if not oath:
        oath = os.path.join(work, "oath")
        r = subprocess.run(["go", "build", "-o", oath, "."],
                           cwd=os.path.join(ROOT, "oath"), capture_output=True, text=True)
        if r.returncode:
            sys.exit("FAIL setup: could not build the CLI\n" + r.stderr)

    srcfile = os.path.join(work, "int-fuzz.oath")
    open(srcfile, "w").write(src)

    def run(args, **kw):
        return subprocess.run(args, capture_output=True, text=True, env=env, **kw)

    r = run([oath, "put", srcfile, "--new"])
    if r.returncode:
        sys.exit("FAIL setup: oath put failed\n" + r.stdout + r.stderr)

    r = run([oath, "eval", "(int-fuzz (Nil [Str]))"])
    if r.returncode:
        sys.exit("FAIL: the interpreter exited %d\n%s" % (r.returncode, r.stderr))
    want = decode(r.stdout)

    outs = {}
    for backend, flags in (("go", []), ("llvm", ["--backend", "llvm"])):
        exe = os.path.join(work, backend)
        r = run([oath, "build", "int-fuzz"] + flags + ["-o", exe])
        if r.returncode:
            sys.exit("FAIL: the %s backend refused the generated program\n%s%s"
                     % (backend, r.stdout, r.stderr))
        r = run([exe])
        if r.returncode:
            sys.exit("FAIL: the %s binary exited %d\n%s" % (backend, r.returncode, r.stderr))
        outs[backend] = r.stdout.rstrip("\n")

    print("  seed %d, %d cases, widths up to %d bits" % (SEED, CASES, max(WIDTHS)))
    print("  interpreter : %s" % want)
    print("  go backend  : %s" % outs["go"])
    print("  llvm backend: %s" % outs["llvm"])

    # THE INSTRUMENT FIRST. If the generator emitted a program that is false
    # under Z, all three paths agree on the same "case-N" and nothing about the
    # LLVM lowering has been tested. That is a generator bug, and it must not be
    # reported as a backend disagreement.
    if want != "all-ok":
        i = int(want.split("-")[1])
        sys.exit("FAIL: the GENERATOR is wrong, not a backend — every path "
                 "rejects %s (%s). No lowering was tested." % (want, conds[i][0]))

    bad = [b for b in ("go", "llvm") if outs[b] != want]
    if bad:
        for b in bad:
            i = int(outs[b].split("-")[1]) if outs[b].startswith("case-") else None
            detail = conds[i][0] if i is not None else outs[b]
            print("  %s backend disagrees: %s\n    %s" % (b, outs[b], detail))
        sys.exit(1)
    print("  ok — four-way agreement (python, interpreter, go, llvm)")


if __name__ == "__main__":
    main()
