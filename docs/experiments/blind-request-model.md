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

---

# Blind round 10 — the repaired §14

**Verdict: PASS-WITH-INFERENCE (8 inferences, plus a ninth outside scope).**
Surface `dd3447a666e91aca…`, exported from `303a0db` — `docs/SPEC.md` alone.
Subject implemented in Python 3 (stdlib only): 82 tests, all passing; 28 mutants
written, 27 killed. Both §14.3 vectors reproduce exactly.

Round 9's gaps were the question. §14 was frozen from dispatch until this record.

---

## The headline: the repair moved the deferral down one level

Round 9's structural finding was that §14 stated its whole obligation as *two
backends produce the SAME `Request` value* while deferring that type to a store
outside the surface. §14.1a was written to close it, and argues:

> It follows from the declarations above under §1's canonical encoding, exactly
> as every other identity in this specification does — so a kernel computes it
> rather than trusting a constant.

**§1 does not define how a `data` declaration's type variables are numbered.**
The subject chose left-to-right from zero and recorded that the other reading
changes `Pair`'s digest and therefore `Request`'s. So:

- the value's SHAPE is now determined — the subject did **not** have to invent a
  type to hold header entries, which is the half of round 9's finding that closed;
- the value's IDENTITY is still not — and §14.0's obligation is stated over the
  value, not its contents.

In the subject's words: *"I can show two backends agree on the contents, and I
cannot show they agree on the value."*

The remedy for a deferral deferred one level down. That is the finding.

## The coverage table overclaims twice, in two different ways

§14.3 was rewritten specifically to answer round 9's complaint that the old
vector witnessed four obligations of twelve. The new table claims all thirteen.
Two of those claims are wrong — and **review had to separate them, because they
are different defects with different remedies.**

**`PROTO-NOMINATION-BY-PRESENCE` — the RULE is correct; the TABLE row is not.**
The subject built the mutant that nominates WITHOUT the presence test and it
**survives all 82 tests including both vectors.** That is true and it does not
mean what the first draft of this record said. §13's `IMPL-UNOBSERVABLE-PINNED`
gives three states, not one:

| state | the specification | a witness | remedy |
|---|---|---|---|
| UNWITNESSED | makes an observable claim | none constrains it | write the vector |
| UNOBSERVABLE BUT PINNED | chooses one alternative | none CAN distinguish them | say so; the choice is the point |
| UNDEFINED | chooses nothing | nothing to witness | define the object |

Nomination-by-presence is the middle row, and §14.2 already says so: *"the two
readings are behaviourally identical — removing a field that is not there is a
no-op. What differs is what the specification SAYS."* A pinned unobservable
choice **is not under-witnessed** because nothing separates its alternatives.

So the defect is narrower and still real: §14.3's table names a witness for it
and counts it among thirteen covered, asserting an observable witness for
something the same section admits is unobservable. **Fix the table row, not the
rule.**

**`REQ-TIME-IS-DATA` — genuinely unwitnessed, first row.** Receipt time IS
observable, so a vector CAN separate a conformant backend from one reading the
message. §14.3's vector fixes no time and carries no date-bearing field, and the
subject had to invent a request carrying `Date:` to get a discriminating test.
Remedy: write the vector.

A coverage table written to fix a coverage complaint asserted coverage it did not
have — once by miscategorising a pinned choice, once by simply not having it.

## The parser boundary cannot support the rules built on it

`PROTO-PARSER-BOUNDARY` was added to settle where parsing ends and §14 begins. It
fixes the adapter's input as *(name, value) pairs in arrival order with OWS
removed, plus the method, the raw request target, and the body octets.* Three of
§14's own obligations cannot be discharged from exactly that:

| rule | needs, and cannot get |
|---|---|
| `PROTO-AUTHORITY-SOURCE` | `:authority` distinguishable from an ordinary field line |
| `REQ-TIME-IS-DATA` | an observation the tuple omits |

The subject also listed `PROTO-AUTHORITY-MUST-AGREE` here, and **review removed
it**: §14 states that rule as a property of the message enforced by the transport
before an adapter runs, deliberately and in those words, so its absence from the
adapter's input is by design rather than a gap.

and `PROTO-TRAILERS-ARE-NOT-HEADERS` is unenforceable there: once trailers are in
the pair sequence the adapter cannot tell them apart, so the rule silently binds
the parser instead.

The subject names this precisely: it is the same failure §14.2a itself diagnoses
for the withdrawn obs-fold refusal — *"the adapter cannot refuse what it can no
longer see"* — **recurring inside the rule written to fix it.**

## The rest

- **The scope of "Exactly one `host` entry ever appears" is unstated**, and two
  independent readers split on it. It sits inside a paragraph opening *"This
  governs HTTP/2 and HTTP/3 ONLY"*, so it reads either as part of that scoping or
  as a global invariant — and under the global reading it contradicts
  *"MUST NOT reorder, deduplicate, or drop repeats"* for two HTTP/1.1 `Host`
  lines. The blind subject took it as possibly global and inferred a
  collapse-or-refuse rule; review took it as strictly local, which delivers two
  `host` entries instead. Neither reading is unreasonable, and the split IS the
  evidence — recorded as a scope ambiguity rather than a self-contradiction,
  because the first wording of this entry claimed more than that.
- **The governing principle has two dispositions and §14 uses three.**
  `HDR-PRINCIPLE` offers PRESERVE and CANONICALIZE; the nine framing names and
  everything `connection` nominates are DISCARDED, which is neither, and the
  §14.1 classification table has no row for it.
- **`REQ-HEADER-VALUE-OCTETS` obligates a layer §14 says it does not bind** — it
  forbids DELIVERING obs-fold as an embedded newline, while unfolding was placed
  before the adapter and §14 binds the adapter.

Eight inferences besides: repeated `Host` lines, `connection` list grammar, the
status code for an authority mismatch, absolute-form target grammar, whether a
lifted authority is octet-checked, an empty `Host` value, the adapter's access to
the version, and `:scheme` on HTTP/2.

## Round 9's gaps, dispositioned one at a time

The research question was whether ROUND 9's specific gaps closed, so each gets an
answer rather than being folded into a summary.

| round 9 gap | disposition |
|---|---|
| octet rule covered headers only, not `method`/`path` | **closed** — derived, covering all four `Str` fields |
| `Connection: close` read as a phantom field name | **closed** — derived correctly |
| the vector was not a legal HTTP/1.1 request | **closed** — both new vectors reproduce exactly |
| vacuous "cannot reinstate" clause; HTAB rationale | **closed** — removed and repaired; neither reported |
| the seven inferences §14.2a settles | **closed** — all appear under DERIVED |
| the `Request` type deferred outside the surface | **half** — shape closed, identity not |
| the parser/adapter boundary undrawn | **replaced** — drawn, and the drawn boundary is insufficient |

All but two closed; one half closed; one replaced by a new defect of the same
kind. Stated per gap, not as a count.

## Judged against the pre-registration, claim by claim

Recorded before dispatch; each was allowed to lose on its own.

| claim | outcome |
|---|---|
| **H1** structural gap closed | **HALF** — shape closed, identity not. Do not read the confirmed half as rescuing it. |
| **H2** new inference in repair-added text | **CONFIRMED** — six of eight inferences and five of seven contradictions |
| **H3** verdict PASS-WITH-INFERENCE | **CONFIRMED** |

Of five named risks: two hit (notation, parser boundary — the second worse than
predicted), one partial, one **right location wrong defect** (I predicted an
unstated ordering at nomination; the subject found the rule inert and its
coverage claim false), one missed.

**The unpredicted findings are not weaker for being unpredicted** — the list is
what was already suspected, so what is absent from it is the more informative
part. The coverage-table defect, the third disposition, and the misplaced
obs-fold obligation were all unlisted.

## Contamination, and evidence the mitigation worked

The IMPL-ISOLATED-SESSION violation stands: the harness cannot strip `CLAUDE.md`
from a dispatched session. But the subject reported seeing project notes naming
the system and the issue and stated that **nothing in them states any §14 rule** —
which is exactly what `make check-coaching-leak` and moving the repair summary to
`docs/milestones.md` were built to achieve. Round 9's subject could have read the
rules there; round 10's could not.

It disclosed recognising HTTP and RFC 9110/9112, which §14 cites throughout, and
recorded as INFERENCES the places where HTTP knowledge would have supplied an
answer the specification does not — quoted-strings in a list, `CONNECT`
authority-form — rather than deriving them.

Its prompt was identical to round 9's. No hint of the claims or risks was given,
which is why the scoring above is a measurement rather than a confirmation.
