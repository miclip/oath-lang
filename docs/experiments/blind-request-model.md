# Blind round 9 — SPEC §14, the handler protocol's Request model

**Verdict: PASS-WITH-INFERENCE (10 inferences).**
Surface `93294d60b71fa10d…`, exported from `7baef0e` — `docs/SPEC.md` alone, no
fixtures, no witnesses. Subject implemented in Python 3 (stdlib only): 69 tests,
all passing; §14.3's worked vector reproduces exactly from raw wire octets.

This is the round §14 was written to face. It is recorded here in full because
the report — not the implementation — is the result.

---

## The headline finding: §14's central obligation is not checkable from §14

§14.0 states the whole point of the section:

> A backend that compiles `(-> Request Response)` MUST implement §14. **Two
> backends satisfying it produce the SAME `Request` value from the same HTTP
> request, and that is the entire obligation.**

§14.2 then defers the type:

> The type is the one bound to the name `Request` in the store; §14 constrains
> its CONTENTS.

**That store is not in the normative surface, and no other section declares
`Request`.** The subject could not determine the arity or names of the
header-pair type, whether `headers` is a list or some other container, or the
declared types of the five fields. §14.3 writes entries as `("accept", "*/*")`,
which is not Oath surface syntax — §1.4 has records, constructors and list sugar,
and no tuple literal.

So a second backend built blind from §14 can satisfy **every behavioural rule in
the section** and still produce a value of a *different type*, with a different
identity and a different §3.2 printing. The section's own success criterion
cannot be evaluated from the section.

That is the defect worth the round. Everything below is smaller.

---

## What the prediction got right, and what it got wrong

Pre-registered before dispatch: **PASS-WITH-INFERENCE**, with seven named
interpretation questions and the sorting-versus-exclusion ordering called the
weakest.

**The verdict was predicted correctly.** The reasoning was that text written in
one pass by the adapter's own author is precise exactly where a defect was found
and loose everywhere else — and that is what happened. All four obligations added
after review (the normative ordering, `host`, framing exclusion, the ASCII
refusal) came back **DERIVED**.

**The seven questions were largely wrong**, and informatively so. Six were
derived, not inferred:

| pre-registered question | outcome |
|---|---|
| is `host` a header entry or a separate field? | **DERIVED** (D13/D14) |
| does raw path include the query and preserve percent-encoding? | **DERIVED** (D16) |
| does sorting happen before or after exclusion? | **DERIVED** (D9) |
| are Connection tokens HTTP OWS only? | **INFERRED** (I5) |
| are non-representable octets rejected or decoded? | **DERIVED** (D8) |
| are framing fields a fixed set or library-inherited? | **DERIVED** (D10) |
| do repeats stay separate and ordered? | **DERIVED** (D4/D5) |

The specifically-named weakest point was derived from the sentence that makes it
normative, and the subject reported it as the mutation that killed the most tests
— the opposite of weak.

**The lesson is about pre-registration, not about §14.** Predicting *that* a text
is loose turns out to be much easier than predicting *where*. Six confident
guesses were wrong, and only a hypothesis recorded before the evidence could show
that; written afterwards it would have been a narration of the ten inferences the
subject actually found.

---

## The ten inferences

Ordered by how much they threaten the section's own claim.

1. **I1 — the declaration of `Request` and the header-pair constructor.** Above.
   Not a matter of degree.
2. **I3 — the octet→codepoint mapping for `method` and `path`.**
   REQ-HEADER-OCTETS-ARE-ASCII is scoped to "any field name or value that would
   appear in `headers`". Both other `Str` fields have no stated mapping and no
   refusal, so a raw `0xE2` in a request target is codepoint 226 under a latin-1
   reading, something else under UTF-8, and a 400 under a third. A scheme signing
   the request target sees three different values.
3. **I2 — `received-at`: `Int` or `Rat`?** "seconds since the Unix epoch" with no
   type. `Rat` is available and exact; no rounding rule is given, so under that
   reading nothing constrains two backends to agree at all.
4. **I4 — the replacement for obsolete line folding.** The text forbids
   delivering it as an embedded newline and names no substitute. Fold-then-refuse
   and fold-to-SP give different observable outcomes for the same request.
5. **I6 — several `connection` field lines.** The text says "the `connection`
   field", singular, while §14 itself forbids joining repeats.
6. **I7 — ordering when both `:authority` and a `host` field line arrive.**
   REQ-HEADER-REPEATS-PRESERVED makes their relative order significant, and §14
   does not discuss the case.
7. **I8 — trailer-section fields.** Only the field NAME `trailer` is excluded.
   Dropping them or appending them are both defensible, and the difference is a
   change in the Oath value produced by *framing* rather than by the request —
   the exact failure REQ-FRAMING-FIELDS-EXCLUDED's own rationale objects to.
8. **I5 — tokenizing the `connection` value.** "a comma-separated list of field
   names" with no grammar.
9. **I10 — HTTP/1.1 absolute-form targets**, where the authority arrives by a
   third route the section does not name.
10. **I9 — a message that cannot be de-framed at all.** Arguably outside scope.

---

## Seven defects in the prose

**(a) `Connection: close` becomes a nomination.** §14 calls the value "a
comma-separated list of field names". RFC 9110 §7.6.1 defines it as connection
*options*, of which `close` is the canonical example and is **not** a field name.
Read literally — which is what DERIVED means — a backend must exclude a field
named `close` from `headers` on essentially every HTTP/1.1 request. Harmless in
practice; the text gives no way to distinguish an option from a nominated name.

**(b) §14.3's worked vector is not a legal HTTP/1.1 request, and covers half the
section.** It carries no `Host`, while REQ-HOST-IS-A-HEADER calls `Host`
mandatory in HTTP/1.1 — so the section's only concrete example is a message it
elsewhere describes as impossible.

The coverage, counted exactly. §14 states **twelve** `REQ-*` obligations:

| | obligation | witnessed by §14.3? |
|---|---|---|
| 1 | REQ-HEADER-NAMES-LOWERCASE | yes, claimed |
| 2 | REQ-HEADER-ORDER-LEXICOGRAPHIC | yes, claimed |
| 3 | REQ-HEADER-REPEATS-PRESERVED | yes, claimed |
| 4 | REQ-HEADER-VALUES-NOT-JOINED | yes, claimed |
| 5 | REQ-METHOD-VERBATIM | incidentally — the vector fixes `method` |
| 6 | REQ-PATH-IS-RAW | **partly** — the target carries no percent-encoding, so "NOT percent-decoded" is untested |
| 7 | REQ-HEADER-VALUE-OCTETS | **no** |
| 8 | REQ-HEADER-OCTETS-ARE-ASCII | **no** |
| 9 | REQ-FRAMING-FIELDS-EXCLUDED (incl. nomination) | **no** |
| 10 | REQ-HOST-IS-A-HEADER | **no** |
| 11 | REQ-BODY-IS-OCTETS | **no** |
| 12 | REQ-TIME-IS-DATA | **no** |

**Six of twelve have no vector at all**, and they are the six carrying the
security argument. The strongest rule in the section — the ASCII refusal, whose
rationale is that a transcoded value makes a handler verify a signature over
bytes that never arrived — is witnessed by nothing.

**(h) §14.3 miscounts its own coverage.** The prose says *"Three obligations are
jointly witnessed here"* and then names **four** rule identifiers, because one
clause witnesses REQ-HEADER-REPEATS-PRESERVED and REQ-HEADER-VALUES-NOT-JOINED
together. Three observations, four obligations. Small, but this is the sentence a
reader would use to decide what the example proves.

**(c) The `Str`/octet rationale is narrower than the rule it justifies.** It
argues from "the printable US-ASCII range … codepoint and octet coincide" and
then admits HTAB (0x09), which is outside that range. The rule is sound; the
stated reason does not cover the exemption granted in the same paragraph.

**(d) "it cannot reinstate a name excluded above" is vacuous.** Nomination is a
removal mechanism. There is no reading under which it could add an entry.

**(e) The parser/adapter boundary is never drawn, and one rule depends on it.**
REQ-HEADER-VALUE-OCTETS says a value "crosses unchanged" and, in the next
sentence, that OWS removal "is parsing, not normalization" — while §14 defines
the adapter's input as "whatever reached it". Two backends whose host libraries
strip OWS differently can both claim conformance.

**(f) A mutation finding, in the specification's favour, and worth stating
precisely.** The subject mutated ASCII lowering into a **Unicode-aware** one and
all 69 tests still passed — a genuinely *equivalent* mutant under §14, because
every octet on which the two disagree (0xC0–0xDE) lies outside 0x20–0x7E and
refuses before the difference is observable. But a **locale-aware** lowering is
not equivalent: a Turkish-locale mapping sends ASCII `I` to U+0131, which no
refusal catches. So the clause

> applying a locale- or Unicode-aware transformation would make the result depend
> on a table outside this specification

is doing real work in exactly one of its two halves. The subject's first test set
missed this (no test name contained an `I`) and it added one to kill the locale
mutant.

**(g) The strongest rule has no companion for two of the fields it protects.**
REQ-HEADER-OCTETS-ARE-ASCII argues that transcoding "would put a lie in the Oath
value: the handler would verify a signature over bytes that are not the bytes
that arrived, and no test of the artifact could detect it". That applies verbatim
to `path`, which REQ-PATH-IS-RAW justifies with "signed schemes rely on the raw
form". The section identifies the hazard, states the reasoning, applies it to one
of three `Str` fields, and is silent on the other two. This is (g) and I3 being
the same defect seen from the prose side and the implementation side.

---

## Contamination

Disclosed before dispatch and unchanged: **IMPL-ISOLATED-SESSION is violated** —
the harness cannot strip this project's `CLAUDE.md` and memory index from a
dispatched session's system prompt.

Mitigated before dispatch: `CLAUDE.md`'s queue entry had paraphrased §14's rules
verbatim (lowercased names, lexicographic order, host, raw target, framing
excluded, non-ASCII refused). That would have handed the subject the entire model
through the coaching channel rather than the export. It was reduced to a pointer,
and the durable rule recorded: **coaching material may direct a subject to
normative text; it must not restate the normative answer.**

The subject disclosed, unprompted:

- It **recognised the project** — the two-kernel structure, the registry, issue
  numbers `#122`/`#133`, the phrase "blind implementation" — and reported
  quarantining that knowledge. Its prior context concerned phases and roadmap
  rather than §14's rules, so there was no concrete reference behaviour to leak.
- **The moment it was most tempted**: resolving I1 by going to look for the
  `Request` declaration in a store it suspected existed. It did not, and recorded
  the gap instead. That restraint produced the round's most valuable finding.
- It read nothing outside the dispatch root and its own work directory. **That is
  an attestation, not a machine-checked fact.**

The subject was given **no hint of the seven pre-registered questions**. Naming
the ambiguities would have produced agreement with the prediction instead of a
test of it, and the prediction outcome above is a measurement only because of
that omission.

---

## The verdict is NOT a measurement — and the rule saying so was written here

This run disclosed session contamination. Under §13's
**IMPL-CONTAMINATION-IS-NOT-A-MEASUREMENT** — added as a consequence of this
round — **no `PASS` was ever admissible from it**, and the count of ten
inferences is **not a bound in either direction**.

The PASS prohibition is sound and asymmetric: a sufficiency claim is
unsupportable from a contaminated run, because the environment is exactly what
may have supplied the missing rule.

**The first draft of that rule went further and was wrong.** It argued the count
was a *lower* bound, on the reasoning that contamination can only resolve an
ambiguity silently and never invent one. This round is its own counterexample.
The largest finding — that §14 defers its central type to a store outside the
surface — came from a subject that reported it *suspected such a store existed*.
That suspicion came from recognising the project. A clean subject might have
declared a pair type of its own and never noticed the deferral. **Contamination
inflates as well as deflates.**

So what survives is not the number but the evidence, and only where each finding
is **independently verifiable from the surface**: "§14.2 defers to a type bound
in a store, and no supplied file declares it" is checkable by reading §14.2 and
requires trusting nothing about the subject. I1 and findings (a) through (h) are
all of that kind. Any arithmetic over them is not.

The rule exists because the ledger had been contradicting the prose:
IMPL-ISOLATED-SESSION forbids a claim from a contaminated run while §13.3
requires a verdict of every completed round, so a disclosed-contaminated run that
finished had nowhere to be recorded — and rounds 7, 8 and 9 each recorded one.
The schema and the prose disagreed and the schema won silently, which is the same
defect §13 exists to catch, occurring in §13. The checker now fails any `PASS`
without an affirmative pre-registered `session_isolated: true`.

## What this round does NOT establish

Per IMPL-VERDICT-SCOPED, and per the `claim_scope` recorded before dispatch:

- It establishes that **a working implementation was produced from the text
  ONLY AFTER supplying ten inferences**, on the exported surface
  **`93294d60b71fa10d…`** (the digest the ledger binds; `docs/SPEC.md` within it
  hashes to `e68c305883e6…`). It does **not** establish that the text
  communicates the model: I1 says the section cannot determine the value its
  own success criterion is stated over, so "communicates the model" is exactly
  the claim this round refutes.
- It does **not** establish fixture coverage — §14 declares no normative data,
  and finding (b) counts the one worked example as witnessing four of twelve
  obligations by claim, two incidentally, and six not at all.
- It does **not** establish that the Go adapter conforms. That rests on a
  separate end-to-end gate and ten mutations.
- It does **not** establish that every rule interaction has been exercised.
