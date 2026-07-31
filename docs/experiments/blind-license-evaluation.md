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

### Resolved after round two: what an evaluation is about

Round two left one item open deliberately, because it was an architecture
question rather than a defect: `LICENSE-IDENTITY-*` bound NAMES, which §9 states
are mutable without changing identity, while §11.2 one layer in binds an artifact
hash.

Settled in favour of the artifacts, and the decisive evidence was that Oath had
already answered it everywhere else. `explainPkg` carries a human-readable name,
but guarantee, termination, properties and spec strength all describe the
artifact reached at that hash; verdict fields are hash-keyed facts throughout the
kernel. Licensing was the lone exception, so this was not a new identity rule for
§12 — it was bringing §12 back into line.

The governing sentence now in §12.4:

> A licence evaluation is a verdict about exact artifacts under exact publication
> terms. Names are discovery paths and MUST NOT contribute to evaluation identity.

An input is a TRIPLE, and each component answers a question the other two cannot:

| component | answers |
|---|---|
| `input-artifact` | which code was evaluated |
| `input-publication` | whose grant was consumed (§8.2.2 entry digest) |
| `input-license` | what terms that grant asserted |

The publication component is what stops the artifact binding from over-correcting.
Licensing is a publication claim, not a property of the code, so the same artifact
can carry different assertions under different publications — and the same
expression asserted by two different principals is two different grants over the
same bytes. Binding artifact and expression alone would collapse those, which is
the same class of error as binding the name: an identity that fails to
distinguish claims differing in who made them.

Both original failure modes are closed, and each is now witnessed by a vector
that fails if the property is lost:

- renaming or aliasing identical artifacts does not create a different evaluation
  (`LICENSE-IDENTITY-ARTIFACT`);
- repointing a name to different code cannot reuse the old digest, because the
  artifact closure changes.

One consequence worth noting: a dependency with no bound name no longer poisons a
composition. It was previously marked `(unresolved)` and forced the whole result
to `UNSTATED` merely for lacking a name — but a hash fully identifies a member,
and a name was never what made it evaluable.

License family: 18/18 obligations witnessed, matrix complete.

## Round three — 2026-07-30, base `7abe61650c82`, surface `1bb5f8fc…`

**Classification: PASS-WITH-INFERENCE.** 23/25 vectors on the first run, and the
first round where the score went DOWN — because two published fixtures turned out
not to be reproducible from the specification at all.

Isolation held (allowlist export, preflight-verified, no `.git`). The subject
disclosed two contamination facts unprompted, including one the harness cannot
remove: its session context carried the project's `CLAUDE.md` from outside the
working directory. It reports that file mentions nothing about licensing, and
correctly declined to weigh whether that mattered — recorded as a fact, per
`IMPL-CONTAMINATION-RECORDED`.

### The result that matters most

**Two published vectors were IRREPRODUCIBLE from the normative text.** The
collision-pair vectors omit `publication`, and the reference kernel encoded the
absent value as a literal `unpublished` — a magic string appearing NOWHERE in the
specification. The subject searched more than 160,000 candidate encodings, failed
to reproduce either digest, and then did the right thing: it refused to tune its
implementation to match, and reported the fixtures as unreproducible instead.

That is the experiment working exactly as intended. An undocumented sentinel in
the reference is indistinguishable, to a blind reader, from a specification that
cannot be implemented — and a subject willing to reverse-engineer would have
buried it. Now `LICENSE-PUBLICATION-SENTINEL`: the sentinel is `-`, it is never
omitted, and never substituted.

### The defect that had a sibling

`engine` was a required component of a normative identity with no vocabulary, no
default and no stated source. That is the IDENTICAL defect round two found in
`policy` — repaired one line above, and never looked for in its neighbour.
Fixing one instance of a defect class and not searching for its siblings is how
the class survives. Now `LICENSE-ENGINE-DEFINED`, which also settles that the
engine is not a property of the model: a published model file carrying an
`engine` key made two components of the digest non-independent.

### Two contradictions I introduced

- `LICENSE-IDENTITY-INPUT` still read "any consumed NAME … MUST change the
  digest" while `LICENSE-IDENTITY-ARTIFACT` said names MUST NOT contribute. It
  also omitted the model digest, the artifact hash and the publication identity —
  under-describing the very encoding it enumerates.
- §8.6.1's rejection rule still named `oath-publish/1` as "the format tag this
  version defines" while its own encoding block said `/2` — the same drift
  `check-spec-vs-fixtures` was built to catch, in a sentence that gate does not
  read.

### U+2028: the forgery surface, one code point up

The character rule excluded LF, CR, `<0x20` and `0x7F`. U+2028 and U+2029 are
none of those, so a conformant implementation accepts them — and a Unicode-aware
line splitter (ECMAScript's `split(/\r?\n|\u2028|\u2029/)`, ICU, several text
pipelines) then reads a ONE-member evaluation as a multi-member composition
carrying a grant nobody published. §8.2.1 escapes both by name for exactly this
hazard. §12.4 had again inherited a rule's letter without its purpose — the third
time this specific inheritance failure has appeared in three rounds.

### Our own gates, again

The subject ran 30 clause-level mutants and found three that survived the suite:
`LICENSE-POLICY-DEFINED`'s constraint clause (its only vector tested that the
digest DIFFERS, which is `LICENSE-IDENTITY-INPUT`'s clause — so an implementation
could evaluate an unrecognised policy as `composition`, report agreement, and
pass), `LICENSE-INPUT-COMPLETE`'s duplicate clause, and
`LICENSE-ORDER-INDEPENDENT`'s tie-break clause.

Its sharpest observation is about the matrix: it reported all three
`[witnessed]` and printed `MATRIX: COMPLETE`, because the matrix measures *a
vector CLAIMING a rule*, not *a vector CONSTRAINING it* — "exactly the gap the
script's own docstring says it exists to close, occurring inside the script".
The clause-level refinement added after round two moved the boundary without
removing it: claiming is still not constraining.

### Open, and not silently patched

- Nothing requires `input-license` to match the expression inside the envelope
  whose entry digest is `input-publication`. A registry can name a real
  publication beside a false expression; the identity verifies perfectly and
  proves only that the registry hashed what it said it hashed. Binding the
  publication captures the REFERENCE, never the agreement between the reference
  and the expression beside it.
- The `-` sentinel is lossy: an absent assertion, a `/1` envelope, and an
  envelope literally asserting `-` all encode identically, though §8.6.1 insists
  the first two are different historical facts.

License family after repair: 20/20 obligations witnessed (was 18/18), matrix
complete with clause coverage for three more rules.

## Round four — 2026-07-31, base `78700a5896f9`, surface `e0ee5df5…`

**Classification: PASS-WITH-INFERENCE, CONSTRUCTIVE. The pre-registered
hypothesis is DISPROVED.** 27/30, and three of those failures were left standing
on purpose.

This was the first round with a hypothesis recorded BEFORE dispatch: *the
declared normative surface is now sufficient for an independent implementation of
§12 without inference*. It is not. Eleven things were undetermined, and the
inferred list says exactly where.

### The prediction was half right, in an informative way

The prediction was that findings would fall at the prose/normative-data boundary,
that abstraction being new and this its first independent user. The implementer
called that boundary **"the best-specified part of §12"** — so the prediction was
wrong about where the weakness lay. It still produced three real defects there:
the model's RETRIEVAL is unspecified (an outside auditor holding
`model=spdx-lattice/1` and a content digest has no stated way to resolve them);
consumer behaviour on a MALFORMED model is undefined (reject or degrade, both
conformant); and nothing obliges a verifier to RECOMPUTE `model-digest`, which
leaves the attack that field exists to stop fully intact — serve lattice `M`,
evaluate against `M'`, publish `SHA-256(M)`.

The largest finding was somewhere else entirely.

### The identity never named what it was an evaluation OF

There was no `subject`. The encoding bound a method and a multiset of members,
and §12.2 has defined a composition as "an artifact TOGETHER WITH its transitive
dependency closure" the entire time. Two consequences, both reproduced:

- evaluating `A` over closure `{A, B}` and `B` over closure `{B, A}` — two entry
  points into one component, or a cycle — produce the SAME digest, so no verdict
  can be attributed to a subject;
- every empty closure collapses to one identity, because with no members there is
  nothing left to distinguish them. `LICENSE-FOLD-NONEMPTY` fixes the VERDICT for
  an empty closure and says nothing about its identity.

The distinguished artifact was in the definition from the start and absent from
the encoding — the same rule-versus-encoding disagreement §12.4's own notes
already record twice.

### The refusal that cost the score

Three failing vectors turn on ONE undetermined clause: `LICENSE-POLICY-DEFINED`
said what an implementation MUST NOT do — not invent a policy, not evaluate an
unrecognised one as `composition` — and never said what to EMIT. Refusing
outright and encoding-then-withholding both satisfy it, and they disagree on the
digest, which is where identity lives.

The implementer chose refusal, then verified in a SEPARATE probe that all three
vectors reproduce exactly under the other reading, and **did not change the
implementation**, on the grounds that adopting the fixtures' reading would be
inference by §13.2's definition. That is the behaviour the constructive
annotation exists to record, arrived at independently.

### Two coverage gaps, one of them the dangerous one

§12.3 names three lookup hazards by hand — `mit`, `MIT `, `(MIT)`. Vectors
covered the first and third. **The missing one is the only one whose lenient
reading yields a full commercial grant** rather than another `UNSTATED`; the two
that were covered both fail safe. §12.3's own note predicts this scenario word
for word, and it remained true of the vector set.

And the U+2028 clause — added in response to round three's audit — had **no
vector at all**. A stated-but-unwitnessed forgery surface is the worst
combination: an implementation excluding only control octets passes everything.

### A fixture was carrying a rule the prose did not contain

§11.2 and §8.6.1 both state that a producer given a character-rule violation MUST
REJECT. §12.4 stated only the exclusion — so the rejection obligation was carried
by vector 18 rather than by the specification. A vector asserting a rule the
document does not contain is the exact inversion §13 exists to detect, occurring
inside the section §13 is about.

### Repaired

`subject` bound; the policy behaviour defined; the rejection obligation stated;
`model-digest` recomputation made mandatory; `IMPL-DATA-RETRIEVABLE` added; the
stale "pair" wording corrected to "triple"; vectors added for the subject, the
trailing space and U+2028. 22/22 obligations witnessed, matrix complete with
clause coverage for the two new clauses.

### Not repaired, deliberately

The publisher/expression agreement question (still the open design record); the
model's extensibility, since `LICENSE-MODEL-SCHEMA`'s "exactly five dimensions"
means a superseding model cannot add one; and `LICENSE-CLOSURE-EXCLUSIVE`, which
this fixture format structurally cannot witness — every record hands the closure
over pre-assembled, so closure ASSEMBLY is untested and half of what
`policy=composition` means is unmeasured.
