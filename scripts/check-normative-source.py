#!/usr/bin/env python3
"""Every byte contributing to a normative identity must have a normative source.

WHY THIS EXISTS. §12.4 encoded a member with no determinable publication as the
literal string `unpublished` — a value appearing NOWHERE in docs/SPEC.md. The
kernel was entirely self-consistent, the fixtures matched the kernel, and the
fixtures were published as conformance vectors. But two of those vectors were
IRREPRODUCIBLE from the specification: an independent implementation searched
over 160,000 candidate encodings, could not find one, and correctly refused to
tune itself to match.

The bug was never the word `unpublished`. Had the constant been `none` or
`missing` the defect would have been identical. The bug is that the
implementation KNEW SOMETHING THE SPECIFICATION DID NOT, and an identity
encoding is the one place where that is fatal rather than merely untidy — every
byte in it is load-bearing for a value other implementations must reproduce
exactly.

WHAT IT CHECKS. Every string literal written into a canonical identity encoding
must appear somewhere in docs/SPEC.md. That is a deliberately weak condition —
appearing in the spec is not the same as being NORMATIVELY DEFINED there — but it
is mechanically checkable, and it catches the entire class of defect where a
constant exists only in the reference. A value the specification never mentions
cannot possibly be derived by someone reading it.

WHAT IT CANNOT CHECK. That a mentioned value is properly specified, or that the
prose around it is coherent. Those need a reader, and in this project they have
repeatedly needed a BLIND reader. This gate closes the mechanical half so the
expensive half can be spent on judgement.

The encoder list is explicit rather than discovered: a new identity encoding is
exactly the kind of thing that should require a deliberate edit here, not be
silently absorbed.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# (file, function, spec section) — every function that builds bytes some other
# implementation must reproduce exactly.
# (file, function, spec section, SUBJECT FIELD) — the last column answers
# "identity of what?" (§13, IMPL-IDENTITY-SUBJECT). Naming it here forces the
# question to be asked of every encoder, because the failure is invisible to
# field-level review: nothing is missing from the input list, and the encoding
# still does not say what the value is a name for. §12.4 bound a method and a set
# of members with no subject for months, and every field in it was correct.
ENCODERS = [
    ("oath/envelope.go", "envelopeEncodeAs", "§8.6.1", "artifact="),
    ("oath/license.go", "evaluationDigest", "§12.4", "subject="),
    ("oath/mutate.go", "campaignEncode", "§11.2", "artifact="),
]

# Literals that are structure rather than content: separators and formatting that
# every encoding uses and no specification spells out as a value.
STRUCTURAL = {"", "\n", "=", "\r", " ", "\t", "-\n"}


def body(path: Path, func: str) -> str:
    src = path.read_text()
    # Anchor on the DEFINITION, not on any line mentioning the name: a one-line
    # wrapper that merely CALLS the encoder would otherwise be extracted, yielding
    # no literals and a vacuous pass.
    m = re.search(rf"^func (?:\([^)]*\) )?{re.escape(func)}\(", src, re.M)
    if not m:
        return ""
    start = m.start()
    nxt = re.search(r"^func ", src[start + 10:], re.M)
    return src[start: start + 10 + nxt.start()] if nxt else src[start:]


def literals(code: str):
    """Quoted literals the encoder writes into the byte stream.

    Encoders differ in HOW they emit: WriteString for line-at-a-time builders,
    key/value tables for fixed-shape records. Both forms are extracted, because a
    check that understood only one would silently measure nothing on the other —
    and reporting zero literals as success is exactly the failure this gate is
    supposed to prevent, so an encoder yielding none is a FAILURE, not a skip.
    """
    out = []
    for m in re.finditer(r'WriteString\(([^)]*)\)', code):
        out += re.findall(r'"((?:[^"\\]|\\.)*)"', m.group(1))
    # Format strings: the keys live in the FORMAT, not in a separate literal.
    # campaignEncode emitted 7 of its 8 lines this way and every one of them went
    # unchecked while this gate reported "ok" — the vacuous pass it exists to
    # prevent, occurring inside it.
    for m in re.finditer(r'(?:Fprintf|Sprintf)\([^,]*,\s*"((?:[^"\\]|\\.)*)"', code):
        fmtstr = m.group(1)
        for part in re.split(r"%[-+# 0-9.]*[a-zA-Z]", fmtstr):
            if part.strip():
                out.append(part)
    # Key/value tables: {"key", value} entries inside a [][2]string literal.
    for m in re.finditer(r'\{"([a-z_-]+)",\s*[^}]*\}', code):
        out.append(m.group(1) + "=")
    return out


def main():
    spec = (ROOT / "docs" / "SPEC.md").read_text()
    failures, checked = [], 0

    print("NORMATIVE SOURCE — every identity byte must be findable in the spec\n")
    for rel, func, section, subject in ENCODERS:
        code = body(ROOT / rel, func)
        if not code:
            failures.append(f"{rel}: function {func}() not found — re-pin this check rather than dropping it")
            continue
        lits = [l for l in literals(code) if l not in STRUCTURAL]
        if not lits:
            failures.append(f"{rel}:{func}() — no literals extracted; the check is measuring nothing here")
            continue
        emitted = {l.replace("\\n", "\n").strip("\n") for l in lits}
        if subject not in emitted:
            failures.append(
                f"{rel}:{func}() binds no SUBJECT — it emits no {subject!r} line, so the "
                f"value it produces does not say what it is an identity OF (§13, "
                f"IMPL-IDENTITY-SUBJECT)")

        missing = []
        for lit in sorted(set(lits)):
            # Compare on the value the encoder actually emits: Go escapes are
            # source syntax, not bytes.
            val = lit.replace("\\n", "\n").replace("\\t", "\t").replace('\\"', '"').strip("\n")
            if not val or val in STRUCTURAL:
                continue
            checked += 1
            probe = val[:-1] if val.endswith("=") else val
            if probe not in spec:
                missing.append(val)
        status = "ok" if not missing else f"{len(missing)} UNSOURCED"
        subj = "subject ok" if subject in emitted else "NO SUBJECT"
        print(f"  {section:<8} {rel}:{func}()  {len(set(lits))} literal(s), {status}, {subj}")
        for v in missing:
            failures.append(
                f"{rel}:{func}() emits {v!r}, which appears nowhere in docs/SPEC.md — "
                f"an implementation reading the spec cannot derive this byte")

    print()
    if checked == 0:
        print("FAIL: no literals were checked at all — this gate is measuring nothing")
        return 1
    if failures:
        print(f"NORMATIVE SOURCE: FAIL — {len(failures)} finding(s)")
        for f in failures:
            print(f"  {f}")
        print()
        print("A constant that exists only in the reference makes the affected vectors")
        print("irreproducible from the specification, which is indistinguishable — to a")
        print("blind reader — from a specification that cannot be implemented.")
        return 1

    print(f"NORMATIVE SOURCE: PASS — {checked} identity literal(s) across "
          f"{len(ENCODERS)} encoder(s) are findable in the specification.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
