# Blind licence evaluation

An independent implementation of SPEC §12 from the normative text alone, used to
test whether the specification is a sufficient conformance target rather than a
description of a kernel that already exists.

## Round one — 2026-07-30, base `d736dac8f97a`

**Classification: PASS WITH UNAVOIDABLE INFERENCE. Evidence AGAINST the pre-fix
specification, not conformance evidence for the amended one.**

That distinction is the whole result and it is easy to lose. The agent
reproduced 8/8 vectors and derived the §12.4 digest correctly on its first
attempt. Read as a score, that is a clean pass. Read as evidence, it is close to
meaningless, because the agent also reported — unprompted — that it could not
have done so honestly:

> The verdicts: no. […] the model is not in the spec […] A reader with §12 and
> no fixtures cannot produce a single verdict. The vectors are functioning as
> normative text here.

A blind run that passes by inferring from fixtures has not shown the
specification is sufficient. It has shown the fixtures are, which was never in
question. The score was the least informative thing the run produced.

### What it found

Four defects, all in the specification, all independently reproduced before
being accepted:

1. **§12.4's encoding was not injective.** Inputs were encoded one per line as
   `input=<name>=<expr>`, and §8.6.1 permits `=` inside a name, so
   `name="a=b"/"MIT"` and `name="a"/"b=MIT"` hashed identically. No rule
   excluded control characters either, so a single assertion containing an LF
   reproduced the digest of a two-input composition — a forgery surface in the
   one value whose purpose is to make a change of method detectable. §12.4
   already *stated* `LICENSE-IDENTITY-INPUT`, which the encoding violated:
   stating a rule does not discharge the encoding that implements it.
2. **The document had drifted from the bytes.** §8.6.1 described a six-line
   `oath-publish/1` envelope while the kernel emitted seven-line `/2` and the
   vectors contained only `/2`. Surfaced by a dangling cross-reference: §12
   cited a `license` field §8.6.1 did not define.
3. **§12.2 granted everything on an empty composition.** Read literally, both
   tables fall through every row on zero inputs and return the operator's
   identity — `YES` for permissions. A false permission reachable by reading the
   specification *correctly*.
4. **The model was absent from the normative surface.** §12.3 said only that a
   model "MAY contain any set of identifiers". The agent reverse-engineered four
   rows from the vectors and demonstrated that the entire `Apache-2.0` row was
   unconstrained: all-`UNSTATED`, all-`YES`, and all-`NO` each still passed 8/8.

It also contributed a cleaner formulation adopted into §12.2: permissions fold
as MINIMUM and obligations as MAXIMUM over `NO < UNSTATED < YES`, which makes
`UNSTATED`'s contagion a consequence of its position rather than a separate
rule.

### Contamination — this run was NOT perfectly blind

Two commit **subject lines** reached the agent's terminal through ordinary setup
commands (`git checkout`, `git log --oneline -1`) in the worktree it ran in.

They probably did not determine any of the four findings: each was derived from
the supplied text and independently reproducible from it, and none corresponds
to the content of the leaked subjects. But "probably did not matter" is a
judgement, not a measurement, and the honest record is that the isolation was
instructional rather than structural — the agent was *told* not to inspect
history in an environment where it *could*. An experiment whose blindness rests
on the subject's compliance is partly measuring compliance.

Recorded rather than discounted. Round one stands as evidence against the
pre-fix specification, which is a claim the contamination does not threaten:
every finding is a defect that reproduces from the supplied files alone.

### Superseded text

Findings the agent reported that had already been fixed between dispatch and
its return — `LICENSE-PERMISSION-NO` unwitnessed, no prohibiting licence in the
model — were independently found by the rule-to-vector matrix in `234e3ee`. The
agent was therefore testing a §12 that no longer existed by the time it
reported. Concurrent repair of the artifact under test is its own methodological
cost, and the fix is to pin and freeze, not to hurry.

## Round two — 2026-07-30, base `dda0b1a6ef88`

**Classification: PASS WITH INFERENCE AGAIN — but a different, much smaller
inference, and a far sharper set of findings.**

16/16 vectors passed on the first execution, and the digest reproduced first
attempt with no iteration. That is again not the result. The claim under test
was that an unseen implementation could reproduce §12 WITHOUT inference, and it
does not hold — for one blocking reason and several smaller ones.

The isolation held: `git archive` export, preflight-verified, no `.git`. The
agent stayed inside the dispatch root and disclosed two boundary events of its
own accord (it wrote a file one directory up by mistake and moved it back, then
`ls`d that directory, seeing only `scratchpad` and `tasks`). Neither exposed
project content. It also disclosed recognising the project from prior context
without consulting it — a residual the harness cannot remove, and the reason its
clean first-run score should be discounted rather than celebrated.

### What round one had fixed, and it held

`LICENSE-MODEL-PUBLISHED` worked exactly as designed. The agent reported it never
needed to reverse-engineer a row, called `model.json` "the strongest part of this
section", and every verdict traced to a citation in a derivation log it wrote
BEFORE opening the vectors. The round-one repair is confirmed by the thing that
found the defect.

### The blocking hole

**`policy` was a required component of a normative identity with no definition
anywhere in the document** — no vocabulary, no default, no semantics, no stated
source. The agent could only obtain it by reading it out of a fixture record.
Without the vectors it could not have produced ANY digest. This is §12.3's own
warning ("silently promotes the fixtures to normative text") reappearing one
field over, in a field that selects the input set and therefore changes the
verdict as well as the identity.

### Four defects that survived round one

1. **The digest bound the model's VERSION STRING, not its bytes.** Flip one `NO`
   to `YES` in the lattice, leave `spdx-lattice/1` untouched, and every published
   evaluation identity still verifies while meaning the opposite. Verbatim the
   harm §12.4 opens by naming. §11.2 — the section §12.4 claims to be applying
   one layer out — hashes the waiver SET rather than a version, for exactly this
   reason. Now `model-digest`.
2. **The character rule was unwitnessed, and the forgery reproduced a PUBLISHED
   digest.** `LICENSE-IDENTITY-UNAMBIGUOUS` has two clauses; only the two-line
   split had a vector. A single assertion whose expression embeds LF reproduces
   the two-member vector's digest `cd8ae415…` byte-for-byte. The reference kernel
   refused it — but only in `evaluateLicensing`, so any direct caller of
   `evaluationDigest` could still forge. A safety rule enforced at one call site
   is enforced nowhere; refusal now lives in the digest function.
3. **"One pair per consumed assertion" was ambiguous, and one reading collides.**
   Skipping members that assert nothing makes `{a:MIT, b:(none)}` encode exactly
   as `{a:MIT}` while their verdicts differ — all-`UNSTATED` against a commercial
   grant. Both readings passed every vector.
4. **The lookup was completely unconstrained.** With matching undefined, `mit`,
   `MIT ` and `(MIT)` yield `UNSTATED` from one conformant registry and a FULL
   COMMERCIAL GRANT from another that helpfully normalises. `fixtures/MANIFEST.md`
   promises that false permission is the direction that matters; that guarantee
   held for §12.2's fold and failed completely for §12.3's lookup one layer below
   it. The fold cannot protect against a wrong answer handed to it.

Also: compound-vs-lookup precedence was unordered (a model MAY contain a compound
key, and lookup-first resolves it); sorting by name alone is not a total order;
duplicates and text encoding were unstated.

### The finding about our own gates

11 of 18 mutations the agent tried survived the full suite. More pointed: the
rule-to-vector matrix — built specifically to find unwitnessed prose — reported
`MATRIX: COMPLETE` while the clause the specification itself calls "a forgery
surface" had no vector. Witnessing was tracked per RULE IDENTIFIER, and a rule
can carry several independent clauses. The matrix now requires a vector per
clause, and is checked in the failing direction.

Two rules were also found to SHADOW each other: with precedence rejecting
compounds unconditionally, disabling `LICENSE-LOOKUP-COMPOUND` changed nothing,
so each hid the other's removal and the scorer called both witnessed. Precedence
governs WHERE the test runs; the compound rule governs WHETHER compounds are
rejected — gating on both restores independent measurability.

License family after repair: 16/16 obligations witnessed (was 11/11 over a
smaller inventory), 22 vectors, matrix complete with clause-level coverage.

### Still open

`LICENSE-IDENTITY-*` binds NAMES, which §9 states are mutable without changing
identity, and never an artifact hash — while §11.2, one layer in, binds
`artifact=<64 hex>`. So `app→lib` and `lib→app` are the same evaluation, and
after a repoint the same digest covers different code. This is a design decision
about what an evaluation is scoped to, not a defect to patch silently, and it is
left open deliberately.
