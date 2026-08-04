# Oath — context for Claude sessions

Oath is an AI-native "verified codebase kernel": definitions are
content-addressed (hash of canonical de Bruijn AST = identity), carry
machine-checked properties in their identity, and live in an immutable
object store where names are metadata. Built July 2026 by Michael + Claude
from a "what would a language designed only for AI look like?" conversation.
Positioning (settled after two external reviews): the syntax is disposable,
the substrate is the product.

**This file is instructions. `docs/milestones.md` is history** — what each
milestone established and why decisions went the way they did. If a paragraph
here tells you what HAPPENED rather than what to DO, move it there.

**THAT RULE IS NOT ENOUGH ON ITS OWN, BECAUSE A PARAGRAPH'S CLASS CHANGES WITHOUT
ITS TEXT CHANGING.** Guidance about work IN FLIGHT is written as instructions, is
correct as instructions, and silently becomes history the moment the work lands —
no word has to change for it to start lying. That is how this file accumulated
four superseded queue orderings, and it did it again the same day the rule above
was written: sixty lines of *do #141, then round 10, #122 is NOT shipped* stayed
in place after all three had landed. **So the queue is never read as fact — it is
checked against GitHub by step 2 of the startup sequence below, which carries the
exact command** (do not improvise one: the obvious `gh issue list` silently
omits closed and paginated issues) — and when writing about work in flight,
prefer a POINTER to the issue over a description of it,
because a pointer cannot go stale in this direction. A stale instruction is worse
than a missing one: it is confidently followed.

**WHEN SEVERAL ENTRY POINTS CAN LEGITIMATELY DISAGREE, REDUCE THEM TO ONE
AUTHORITATIVE PATH AND DERIVE THE REST FROM IT.** The consolidations this project
keeps arriving at are one move, not four: one transformation table instead of
distributed prose; one authority for a fact instead of duplicated state; one
`capabilityKinds()` instead of two lists; one startup sequence instead of
competing "first" instructions. Each replaced a set of sources that were all
correct in isolation and could drift apart — which is why the disagreement is
never visible from inside any one of them.

The test for whether you have this problem is not "is there duplication" but
**could these two disagree without either being wrong at the time it was
written**. If yes, one of them must become derived. This applies to code,
specifications, tooling and guidance alike; the startup sequence is simply the
instance where the artefact being consolidated is this file.

**AND THE RULE BELOW ABOUT WITNESSES GOVERNS THIS ONE: DERIVE THE PATH YOU
CONSOLIDATE ONTO FROM THE CLAIM, NOT FROM THE IMPLEMENTATION'S DECOMPOSITION.**
You can consolidate correctly and still be wrong, because two different
boundaries can each look like *the* single entry point, and succeeding at the
wrong one FEELS finished — there is one path now, and the duplication is gone.
Worked example, #133: source validation was consolidated into `lex`, genuinely
the single entry to the LANGUAGE, and three more substitutions were then found
one layer out each time — because the claim was about source BYTES, whose single
entry is a different function. Ask what the CLAIM quantifies over, then find that
set's entry point. A list of call sites read off the code answers a different
question, and answers it completely.

**ASSERTING AN OBLIGATION CANNOT CREATE THE STRUCTURE NEEDED TO SATISFY IT.**
This is the most general thing the project has learned, and it is broader than
Oath. Whenever a requirement feels blocked, ask what STRUCTURE the obligation
presupposes, and whether it exists — because the failure never looks like a
missing structure, it looks like a rule that needs stating more firmly. Five
instances, each already elsewhere in this file, all the same mistake:

    a `MUST reproduce` sentence   does not create a WIRE FORMAT
    a coverage table              does not create WITNESSES
    a gate                        does not create a MEASUREMENT
    prose                         does not create a TRANSFORMATION
    a requirement                 does not create an AUTHORITY

In every case the missing structure had to be designed explicitly before the
obligation meant anything, and in every case the tempting repair was to assert
harder. **The wire format IS the decision** — the sentence demanding it is not.

**AND ONE PARENT DOWN: AN ARTEFACT THAT RESTATES WHAT ANOTHER ARTEFACT
AUTHORITATIVELY DETERMINES MUST DERIVE IT, NOT DUPLICATE IT.** Whenever something
here depends on external state — a work item's status, a count, whether two
things are the same, which bytes a component emits — name the AUTHORITY for that
state and read it, rather than writing down its answer. A duplicate is correct
exactly once, and nothing announces when it stops being.

This is not a new rule; it is the one already generating four mechanisms that
were written as if unrelated:

    what depends on external state   the authority to derive it from
    ------------------------------------------------------------------------
    prose figures                    fixtures/outcomes.json (check-doc-numbers)
    the queue's class                each named issue's state (startup step 2)
    "are these the same?"            the canonical hash, not a walker (#141)
    "which scripts run?"             the prover's own solve seam (#139)

The first two derive a CLAIM from the authoritative source; the last two reuse
the authoritative MECHANISM instead of rebuilding it. Same rule, and recognising
that is worth more than either half: a hand-written duplicate fails the same way
in both cases, by comparing the fields its author remembered and silently
accepting everything else.

**AN ARTEFACT THAT MAKES ITS READER INFER WHAT IT COULD HAVE NAMED HAS DELEGATED
ITS OWN WORK.** Two prospective checks fall out, the same pattern on different
artefacts — each catching distributed meaning by READING, before any round or
review is dispatched:

- **In a specification:** if it describes RESULTS but never names the OPERATIONS
  producing them, the operations have not disappeared — the reader is inferring
  them. Look for verbs.
- **In this file:** if new guidance cannot be expressed by organizing existing
  guidance under a more general principle, it is accumulating advice rather than
  understanding, and the reader is inferring which of several overlapping rules
  governs.

**WHEN ADDING GUIDANCE, PREFER REPLACING A COLLECTION OF SIBLING RULES WITH THE
PARENT PRINCIPLE THAT GENERATES THEM.** Compression through abstraction, not
through deletion. **The signal to watch is not line count** — it is whether new
guidance starts EXPLAINING existing guidance instead of ORGANIZING it. A file
that grows while repeatedly finding the parent rule is healthier than a shorter
one carrying four versions of the same queue, which is what this file actually
had.

**AND THE TEST IS BEHAVIOURAL, NOT DIMENSIONAL — THE COLD READ IS AN INSTRUMENT.
IT IS THE STARTUP SEQUENCE, AND THERE IS ONLY ONE:**

  1. **Read this file cold** and note what it tells you to do first. Do not act.
  2. **Check the STATUS of every issue this file names.** Derive the list from
     THIS FILE, so the check cannot miss what the file mentions:

         ids=$(grep -o '#[0-9]\+' CLAUDE.md | tr -d '#' | sort -un)
         got=$(printf '%s\n' "$ids" | while read -r n; do
                 gh issue view "$n" --json number,state -q '"\(.number)\t\(.state)"'
               done)
         printf '%s\n' "$got"
         if [ -z "$ids" ]; then echo "INCOMPLETE — no issue ids found; run from the repo root"
         elif [ "$(printf '%s\n' "$ids" | wc -l)" = "$(printf '%s\n' "$got" | wc -l)" ]
         then echo "COMPLETE"
         else echo "INCOMPLETE — a lookup failed; this check did NOT run"; fi

     Not `gh issue list`: its default page is open issues only and the first
     thirty, and ANY fixed `--limit` silently truncates later — a check that
     claims completeness must take its universe from the claim (the issues this
     file names) rather than from a page of GitHub. The COUNT ASSERTION is not
     decoration: without it a transient API failure drops one issue's line and
     the output still looks like a clean report, which is this repo's most
     familiar defect wearing a new costume. It has already caught one instrument
     bug — an earlier `for n in $ids` did not word-split under zsh and the
     assertion reported INCOMPLETE rather than silently checking nothing.
     Status is the ONLY part of the queue with an external authority, and it is
     the part that silently rots: an issue closes elsewhere and no word here
     changes.
  3. **If they disagree, the file is wrong — repair it before starting work.**
     The disagreement IS the measurement; skipping the repair discards it and
     leaves the next session to make the same discovery.

**Step 2 CANNOT validate the ordering, the buckets, or the triggers**, and no
command can: those are judgment recorded here, with no external authority to
derive them from. Do not let a green status check read as a validated queue.
What tests the judgment is step 1 — whether a fresh reader picks the right first
task — which is exactly why the instrument is a cold READ and not a script.

Ask throughout: can a fresh reader orient correctly, quickly, and without acting
on stale guidance? Do NOT substitute a line budget or a token budget; size is
neither good nor bad, and a number is easy to satisfy without changing what a
reader does.

It earned instrument status the way anything here does — by catching what nothing
else could. CI was green, every gate passed, the issues were internally
consistent and the tree was clean, and a reader starting from this file would
still have begun the wrong work inside a minute, because sixty lines described
work that had already shipped. **No other check in this repo measures startup
correctness.** It is the same criterion as the conformance work, applied to
prose: not *is the document short*, but *does it produce the intended behaviour
in an independent reader* — which is why the cold read must be done at the START
of a session, by the reader whose behaviour is being measured, and cannot be
delegated to the author who already knows what the file meant to say.

The one worked example of the parent rule so far: "verify the measuring instrument before
interpreting its output" was placed ABOVE the gate advice it generates rather
than beside it as another peer bullet.

## PHASE 5 — READ THIS BEFORE PICKING WORK

  Phase 1  Can AI own software?                   registry, proofs, trust
  Phase 2  Can the trust model survive reality?   authority, delegation, CI, transfer
  Phase 3  Can the implementation be replaced?    Go backend, LLVM backend, the boundary
  Phase 4  Can someone build software and FORGET they are using Oath?
  Phase 5  Can the compiler FAITHFULLY OBSERVE the language?             <- here

Sharper: **Phase 5 is about reducing the semantic gap between specification and
execution.** #114, #115's LLVM half, #126 and #118 all fit that description, and
none of them made Oath more expressive — they made existing semantics more
faithfully realized.

**IMPLEMENTATION IS NO LONGER THE LIMITING FACTOR; CALIBRATION OF CLAIMS IS.**
That sentence explains nearly every significant correction of recent sessions:
distinguishing launch failure from failure before OBSERVABLE STARTUP; separating
provisioning from authority narrowing; refusing rather than replacing; a backend
SUBSET versus language semantics; deriving a witness's universe from the CLAIM
rather than the implementation; and evidence versus mechanism. The same
distinction keeps arriving in new clothes — *findings repaired* is not
*inferences resolved*, *requested by the harness* is not *required by the
protocol*, *not reached* is not *derived*, and **a completed milestone is not a
completed issue** (#115's LLVM half shipped while the issue is the wider #13b).
Each pair is one state being reported as a stronger neighbouring state, and each
was found by asking what the state actually MEANS rather than whether it looked
green.

**THE THROUGH-LINE, across authority, transfer, the compiler boundary, the
application, the blind rounds and §14: each step reduced where UNVERIFIED
ASSUMPTIONS could hide.** Different domains, one trajectory, and
the general form of "reducing the semantic gap".

**And it does NOT move defects upward — it makes previously hidden defects at
EVERY layer observable.** That distinction is load-bearing: an implementation
bug can legitimately surface late without contradicting architectural progress.
The session's own example is `received-at`, taken after the body was read, found
only once the specification was structured enough to ask the right question of
each row.

**So the metric is not size.** The temptation is to equate "the compiler is
getting bigger" with "the compiler is getting better". The opposite is the
measure: *the compiler gets better when its observable behaviour converges on the
language semantics, regardless of how many features it implements.* This is where
the project stopped accumulating MECHANISMS and started accumulating EVIDENCE —
most of the important commits were not new-feature commits; they narrowed a claim
until it exactly matched what had been demonstrated. Continue that.

### THE QUEUE — one ordering, in three buckets; position is not priority

**SETTLED, so that it is not reopened as a gap: candidate-script exposure is
OUTSIDE §10's conformance surface** — decided, recorded on #139, and reflected in
SPEC §7.2 and in `conformance.sh`'s output. `prove/attempts.txt` is a
reference-kernel diagnostic guarded by the Go suite; a kernel that does not emit
it is not deficient. The bytes remain DETERMINED by §7.2 for every kernel, so
what was declined is the cross-kernel witness, not the obligation. **Do not
re-derive this from the harness output** — the reasoning is on the issue,
because a later reader meeting only the absence would reasonably read it as
missing coverage.

The buckets encode DIFFERENT CLOCKS, not just different priorities:

  more EXPENSIVE if delayed   #133's last boundary — VERIFICATION DEBT, see below
  more VALUABLE if delayed    #130, #134, #138 — after more evidence has
                              accumulated to calibrate them
  waiting for a TRIGGER       #117/#69, #128

**Compiler/runtime — one item, and it is DEBT rather than a feature.** #117/#69
turns out never to have belonged here: the standing instruction below already
says it stopped blocking anything when #126 closed and must wait for a real
consumer to fix its scope. So it has a TRIGGER, not a position, and listing it
under a closing window contradicted the same file's own instruction.

What IS here is **#133's wasm ingestion path — the one boundary recorded as read
rather than RUN.** State the window precisely, because "expensive if delayed" is
easy to assert and this bucket is worthless if anything can be argued into it:
SPEC §1.4 and §3 are ALREADY SHIPPED and already say what ADMIT must do at every
boundary. So the claim is in normative text while one of its boundaries is
unmeasured, and the cost of delay is not that the defect grows — the playground
publishes nothing — but that **the gap between a shipped claim and its evidence
widens as more work cites the claim.** That is the only kind of window this
bucket should accept. Anything parked here that is not narrowing a claim makes
every other item look less urgent than it is.

If the next piece of work seems to belong here, say what makes it cheaper NOW
than later before adding it.

  **#133's compiler half SHIPPED** (4337bf5) and its slot is gone rather than
  renumbered around. **#133 IS STILL OPEN, and this file said otherwise for one
  session** — it read *"what remains is not a task"*, which was written the same
  day the issue itself recorded an open one: the wasm ingestion path, the single
  boundary I closed on paper but never ran. Step 2 cannot catch this class,
  because the issue's STATUS never changed; only reading the issue against the
  paragraph does. Prefer a POINTER for exactly this reason — a sentence
  summarising an issue's remaining work goes stale while the issue does not.

  What remains BEYOND that is a CATEGORY MOVE that
  #69 already owns: `Str`'s scalar range is enforced today by HOST DISCIPLINE at
  the boundaries octets cross, and refinement types would move it into ARTIFACT
  IDENTITY. Those are different classes of guarantee, not different amounts of
  one — and the reason the invariant cannot live in identity now is that `Str`
  has none of its own: `(data IntStack [] (Empty) (Push Int IntStack))` hashes to
  exactly `Str`'s hash, so a rule attached to `SCons` would give Unicode
  semantics to an integer stack. Do NOT re-file this as "add a check at
  construction" — SPEC §3 settles what a kernel does here; read it there.

  **#121 is CLOSED** (604c546) and its numbering is gone rather than struck
  through, because a queue that carries its own history is the thing step 2 of
  the startup sequence keeps catching. What is worth carrying forward is not the
  fix but one measurement it produced: the broken decoder carrying the CORRECT
  general property scored `tested · passed 200 cases`. The generator could not
  reach the input that falsifies it. Proof caught what testing could not, and
  mutation scoring still needed literal witnesses afterwards — three instruments,
  three different blind spots, on one seven-line definition.

**Feedback/tooling — the window is NOT closing.** Improves confidence in future
work; nothing depends on it immediately.

  1. **#130** vacuity signal, guard/subject overlap — now has a MEASURED
     instance on the committed corpus (a property whose guard the generator
     can never satisfy, scoring the corpus's worst 11/53). It is on the issue;
     do not re-derive it.
     **The obvious repair is the wrong one, and it has been measured: #144 is
     CLOSED because widening generation moved the corpus +2 of 1203 and made
     three definitions WORSE.** Reaching the guard is not the fix — the
     surviving mutants are in branches no property observes at all. Read #144
     before proposing generator work; a fixed case budget means a better draw
     REALLOCATES it rather than adding to it.
  2. **#134** typed refusal reasons
  3. **#139 SCOPING THE RE-DERIVATION with #140** (prove-worker delta) — one
     item, deliberately: both are *do not redo work whose answer is already
     determined*, one for re-deriving and one for proving, and neither can be
     judged without the other's answer. Doing them apart means deciding twice
     what "unchanged" means.
     **NOT the same work as #139's §10 DECISION above** — that one asks whether
     candidate-script exposure belongs in conformance; this one asks how much
     empirical re-derivation the LEMMA-STATE path still needs, which the
     fixtures deliberately do not pin. Both are "#139", which is why neither is
     called a half or a part: an ordering whose items share a name is not an
     ordering. This one goes late because scoping a re-derivation you cannot yet
     byte-check is scoping it on faith.

**Research — needs runway, not a slot.**

  4. **#138** ACL2 comparative review. It is a REGISTERED EXPERIMENT with a
     falsifier, not a reading task, and its value depends on the reading not
     being rushed: a hurried pass would find transformations everywhere, which
     is the outcome the falsifier exists to prevent. Read the issue for the
     falsifier and the method; neither is restated here.

**Documentation hygiene.**

  5. **#128** — DEFERRED UNTIL AFTER THE NEXT EXTERNALLY-VISIBLE MILESTONE. It
     has a TRIGGER, not a position. It changes no semantics, unblocks no user,
     and closes no narrowing window — and it is small and easy, which is
     precisely why it would interrupt architectural momentum if it sat in the
     middle of the queue. **Do not let "it is open" become "it is next."**

### Standing instructions attached to the queue

- **EVERY ARCHITECTURAL ISSUE MUST KEEP A CREDIBLE PATH TO "NO CHANGE
  REQUIRED".** A backlog of architectural questions is evidence of a stable
  foundation ONLY if those questions can still be answered no; otherwise
  architecture becomes unfalsifiable and the work becomes research for its own
  sake, which is indistinguishable from progress while you are doing it. So
  before starting one, state what result would show the current design is
  already correct — and prefer a falsifier that is MEASURABLE on the committed
  corpus over one that is merely arguable.
  Worked example, #140: the issue argued for a delta pass without stating its
  own "no". The sharp form turned out not to be *delta vs corpus* but *heavy
  tail outside the delta vs the whole tail*, because runtime is dominated by
  properties that never prove. Measuring it half-fired — median change
  re-attempts 0% of the tail, but a `List`-class change re-attempts 80% — which
  left the core argument standing and WITHDREW one sentence of it. Note what
  produced that: a falsifier stated precisely enough to be run. A vaguer one
  ("is this worth it?") would have returned the answer the author already held.

- **Do NOT reopen #118 as "add arithmetic".** Broadening the backend should
  require A NEW CONSUMER or a separately stated compiler milestone, not
  momentum. It would change the question from *is the compiler faithful?* back
  to *how much language can we implement?* — different goals, different
  evidence.
- **SCOPED AUTHORITY (#117) GOES LATE, and its scope must not widen.** It stopped
  blocking anything when #126 closed the webhook's fail-open structurally; what
  remains is a large type-system project. It risks becoming the next long
  infrastructure branch unless a real user or application demands a concrete
  scope first. The settled scope and the "keep the predicate vocabulary minimal"
  line live **on the issue** — read it there rather than re-deriving it.
- **THE FIRST EXTERNAL CONTRIBUTOR IS NOT A SCHEDULED STEP.** It depends on a
  real person's availability, so it arrives when it arrives. **Do NOT substitute
  invented "external-user" work for an actual external user** — a simulated
  newcomer walkthrough would produce exactly the reassuring evidence the real
  thing exists to withhold, and this project has already learned that a witness
  which cannot disappoint you is not a witness.
- **The next work is to DEPEND on Oath, not to improve it.** The application
  (#120) ran first, so it now constrains the compiler rather than the other way
  round: it did not redesign the language, it produced a ranked demand list.
  `docs/experiments/webhook-friction.md` is that list — read it before choosing.

## State of the project

- Two kernels: `oath/` (Go reference, zero deps in the DEFAULT build — the
  optional GCS+Postgres store backend adds a Postgres driver only under
  `-tags cloud`; conformance/wasm/default builds stay dependency-free) and
  `oathrs/` (independent Rust, built BLIND from the spec — see "The second
  kernel"). `docs/SPEC.md` is NORMATIVE — any change
  affecting hashes, verdicts, or semantics MUST update it; identity is the O1
  binary encoding (§1), and encoding changes fork reality.
- Guarantee system, all real and CI-guarded: asserted → tested
  (deterministic, hash-seeded) → PROVEN (Z3, direct + structural induction,
  relevance-filtered lemma library per §7.2; `oath hint` admits a named proven
  lemma the filter excluded — sound, identity-neutral, #67). Per-def verdicts:
  termination (lexicographic), confinement (closure-tracking), spec strength
  (mutation + justified waivers), provenance (append-only tamper-evident
  journal, authenticated principals on the HTTP store).
- ~105 definitions fully PROVEN (insertion sort 7/7, reverse-involution, the
  KV world laws, native set/map laws, queue/tree/interval); honest exhibits
  remain deliberately: bad-reverse (falsified), spin (termination unproven),
  abs-small (tested-but-refuted), and the SMT-incomplete/non-theorem defs stay
  `tested`.
- KEY OPERATIONAL FACT: mutation scores and waivers are structure×SEED
  facts (seeds derive from hashes) — never carry them across identity
  changes; `oath migrate-encoding` drops them by design. A fixture is only
  evidence once you know it was regenerated under the current identity.
- KEY OPERATIONAL FACT: **`oath fixtures` is NOT read-only.** It writes proof
  verdicts back into `codebase/` (meta/*, log.jsonl), so the next regeneration
  reads them and proves MORE. Counts climb run over run — 123 → 126 → 130 in one
  session — and `check-doc-numbers` starts failing on prose that was correct when
  written. Regenerating fixtures is therefore never a safe reflex when a gate is
  red: it mutates the thing being measured. If `check-fixture-integrity` fails,
  first check `git status codebase/`; if it is dirty, the drift is yours. Restore
  with `git checkout codebase/ fixtures/` rather than regenerating again. When a
  regeneration IS intended, commit `codebase/` and `fixtures/` together — a commit
  with one and not the other leaves them disagreeing, which is exactly the drift
  the gate exists to catch.

## The live registry (#14, closed)

The public content-addressed registry is DEPLOYED and live at
**registry.oath-lang.org** (GCP Cloud Run, deployed keyless from GitHub Actions;
custom domain + managed TLS; see `docs/deploy.md`). What it runs:
- **Signed puts** — Ed25519 signatures on journal entries (SPEC §8.4); a
  principal is a keypair (`oath keygen`, `put --key`).
- **Verification worker pool** — `oath prove-worker` proves `require_proven`
  names out of band (SPEC §8.5); the bulk `--scan` proves the corpus in
  dependency order, IN PARALLEL by dependency level (one z3 per core), gated on a
  proof-state fingerprint so a settled corpus is a no-op. `docs/registry-verification.md`.
- **Auth** — signature auth (`X-Oath-Pubkey`/`X-Oath-Signature`; the principal IS
  the key) with capability-limited bearer tokens (read-only by default) for
  MCP/AI clients; policy gains `owner_pubkey` (key-scoped names). `docs/registry-auth.md`.
- **Store-driver seam** — Store runs over a pluggable `backend` (backend.go):
  fs + in-memory (tested), and a GCS-objects + Postgres-index cloud backend
  behind `-tags cloud` (CI-integration-tested, `oath migrate-store` + Terraform
  wiring staged; the live cutover is deliberate — `docs/store-drivers.md`). The
  live registry still runs the fs-over-gcsfuse store (single-instance).
- **Release pipeline** — `git tag vX` cuts a GitHub Release of both kernels for
  all platforms (`.github/workflows/release.yml`); deploy is manual (`deploy.yml`).

The standard-library, delegation and finish-line state is in
`docs/milestones.md`. The one live fact worth carrying here: **the registry has
no authorized-key allowlist, so any key that signs can write** — verify the
deployment, not a document, before claiming otherwise.

## Blind implementation is a DEFINITION OF DONE, not an activity

Run it when a change introduces new normative text, scoped to that text — then
fix what matters and ship. Do NOT run rounds until they stop finding things: a
good reviewer can always improve a specification, and the rounds will happily
keep discovering their own unknowns.

  build -> stabilise the design -> if it added normative text, blind-test that
  text -> fix what matters -> ship -> move on

The stopping rule for a round is not "nothing more can be found" but **does this
change what a user can do**. By that measure rounds 4-6 had already stopped
earning their cost, while still producing genuinely good findings — which is
exactly how this becomes hard to notice from inside.

**A DISPATCHED SUBJECT INHERITS THIS FILE, SO: CLAUDE.md POINTS AT NORMATIVE TEXT
AND NEVER PARAPHRASES IT.** A summary of a section under test hands a "blind"
subject the model without it ever opening the specification — the contamination
problem arriving through the coaching channel rather than through the export,
where no preflight can see it. `make check-coaching-leak` catches a rule NAMED
here; it cannot catch a rule DESCRIBED here, and that half is discipline. The
rule outlives any particular round for a second reason: a paraphrase drifts from
the SPEC, which is the same disease as the four superseded queue orderings this
file used to carry.

## NAMING: names are permanent, so treat every one as a publication

The journal is append-only and there is NO unbind operation. A name bound in a
public namespace exists there forever, whatever the manifest later says about it.
Repointing changes what it resolves to; nothing removes it.

Two names under `oath/*` are already a mistake made this way —
`oath/step8-probe-disposable` and `oath/step8-probe-second`, created by a security
exercise and named after a step in a conversation. "step8" means nothing to
anyone who was not in that conversation, and they sit in the namespace reserved
for the standard library. They are recorded in the manifest as `export: false`
with the reason, because documentation is the only correction available.

**RULES, in the order they would have prevented that:**

1. **NEVER publish test or exercise artifacts into `oath/*`.** Reserve a separate
   prefix. An exercise needs *a* namespace with the right authority shape, not
   *the* namespace holding the standard library. Conflating the two was the whole
   error; the naming was downstream of it.
2. **A name must be legible to a stranger with no context.** Not to you, not to
   this session's transcript, not to a receipt it cross-references. Traceability
   to an internal record is worth far less than being self-explanatory, because
   the reader you are writing for has neither.
3. **No references to process, steps, dates, tickets, or agents in a name.**
   `step8`, `phase2`, `wip`, `tmp`, `claude-test` are all the same mistake.
4. **Ask "would I want this in `git log --all` forever?"** and then remember that
   a name is stronger than that — a bad commit can be reverted; a bound name
   cannot.
5. **The namespace and the standard library are DIFFERENT SETS.** `oath/*` is
   what the project governs; the standard library is what
   `stdlib/oath-stdlib.json` declares. Publishing into the namespace does not make
   something library, and the manifest is what a consumer should be reading —
   but everything under the prefix is what they will SEE.
6. **A dry run costs nothing.** `--dry-run` prints the exact bytes. There is no
   equivalent for un-publishing.

The asymmetry is the point, and it is the one the protocol exists to make you
feel: publishing is one command, and permanence is the guarantee. If a name is
not worth defending in a year, do not bind it.

## The compiler boundary (#114, closed 2026-08-02)

`oath/program.go` is BACKEND-NEUTRAL and `oath/compile.go` is the Go backend. The
dependency runs one way — Oath semantics → neutral requirements → backend
provider — and `oath/boundary_test.go` enforces it by resolving every identifier
to its declaring file (bindings, not names: a backend METHOD on a shared type is a
dependency; a same-spelled local is not).

- A capability record field denotes a KIND (`http_request`, `process_env`,
  `file_read`, `record_sink`). Backends supply kinds. **No new language or
  capability semantics may be defined in terms of Go constructs** — when adding to
  `compile.go`, ask whether you are describing Oath or describing Go.
- There is deliberately NO expression IR: the neutral representation of a body is
  the verified `Def` closure. #118 measured that decision against the corpus and
  it held (4660/4660 subterm types recoverable, 286/286 polymorphic call sites
  carrying instantiation in the raw canonical bytes).
- Every declared requirement resolves exactly once before launch or the program
  exits 70; undeclared authority is ABSENT from the binary (imports are
  requirement-driven), not merely unused.
- `oath provenance <file>` reads an embedded canonical manifest without executing
  the artifact and without opening a store. It is UNSIGNED — evidence about a
  cooperative artifact, not attestation (#116).

**THERE ARE NOW TWO BACKENDS.** `oath build --backend llvm` (`oath/llvm.go`) emits
textual LLVM IR plus a C runtime and shells out to clang — no new dependencies,
same pattern as emitting Go. It consumes `CompiledProgram` and references NOTHING
in compile.go. It is narrow on purpose and REFUSES the rest by name; it also
refuses `http_request`, which is the point — two backends may support different
subsets of one vocabulary.

**THREE THINGS NOT TO DELETE**, each a control that makes a claim a correction
rather than a relaxation:

- **A bare function projection out of a capability record still ESCAPES.** #126
  relaxed escape analysis for non-function fields; this is the boundary that
  keeps that a repair of a modeling error rather than a loosening.
- **The `Str` tail is a VIEW, and the condition is written at the line.**
  Immutable buffers with program lifetime permit views; shorter-lived storage
  requires copying. A future runtime change has a precise place where its old
  assumption becomes invalid — and it must change to a copy, not to a hope.
- **Unsupported constructs FAIL CLOSED** — refused and named, never wrapped,
  replaced, or silently approximated. That is what makes "backend subset" an
  honest claim instead of a semantic divergence.

TWO KNOWN LIMITATIONS, carried forward so "backend-neutral" is not mistaken for
"semantically complete": the capability vocabulary is global and name-based
(#117), and the manifest is not bound to the bytes it describes (#116).

## Writing a gate — the discipline that has found the most defects

**VERIFY THE MEASURING INSTRUMENT BEFORE INTERPRETING ITS OUTPUT.** That is the
class; everything below is an instance of it. The same mistake has now appeared
in five unrelated layers, each time as an instrument that reported success while
measuring something other than its claim:

  a property whose guard prevented ever reaching its conclusion
  a timing test that never observed the ordering it asserted
  a contamination check that proved only that identifiers were absent
  a coverage table that proved only that rows existed
  a gate probe that proved only that SOMETHING failed — while the gate was broken

The last is the sharpest and the easiest to repeat: a probe asking "did it fail?"
cannot distinguish a working gate from a crashing one. So a probe must check for
a crash first, and **the gate must PASS on the real input before any probe of it
means anything.**

**ASK WHAT MUTATION MAKES IT FAIL.** This repo is mostly gates, and the recurring
failure is a test that demonstrates the SETUP or one prerequisite and is then read
as evidence for the claim. If the only mutation that breaks a test is in parsing,
setup, or fixture generation, it is not witnessing its claim. VERIFY BY REVERTING
— undo the fix, watch it fail with the message you expect, restore. A test never
observed failing is a hypothesis. Prefer extracting the claimed behaviour into a
pure function and asserting every outcome, and assert the CONTROL so you know the
measurement discriminates.

**TWO QUESTIONS, AND THEY ATTACK DIFFERENT DIMENSIONS. Ask both.**

  1. **What mutation makes this fail?**
     — does the witness distinguish truth from falsehood?
  2. **What defines the universe this claim quantifies over?**
     — is it even looking at the right set?

A witness can pass the first and fail the second completely: perfectly
discriminating, over the wrong population. The principle underneath the second:

> **A witness must derive its universe from the CLAIM, never from the
> IMPLEMENTATION.** The implementation's boundaries are hypotheses. The claim's
> boundaries are what you are trying to establish.

Six instances in one session, in six unrelated layers, every one the same
mistake — *measuring the implementation's decomposition instead of the
property's*:

  claim                       measured                     should have measured
  ------------------------------------------------------------------------------
  nothing is listening        the ports I expected         every `oath serve --http`
  the corpus is verified      examples/                    what `oath fixtures` reads
  backends match Oath         providers -> vocabulary      both directions
  non-JSON is refused         the impl's own predicate     a longer type does not match
  the failure path works      "FAIL" was printed           the suite reached its verdict
  a tab cannot inject         an escaped `\t`              an actual 0x09 byte

This is why `pgrep -f 'oath serve --http'` is right and a port list is not; why
`capabilityKinds()` became one source of truth; why the three-way verdict became
a PURE FUNCTION with every outcome asserted; and why the port-order test checks
OBSERVABLE STARTUP rather than an exit code.

**AND WHEN THE CLAIM'S UNIVERSE HAS NO NAME, NAMING IT IS THE WORK** — the
property-shaped instance of *a new abstraction earns its place when it makes a
previously unaskable question expressible*, which is stated with its evidence
under the missing-artefact triggers below. Here it has a PROSPECTIVE test,
askable of any universal property before anything is run:

> *Could I write this property's guard without mentioning how the body works?*

If not, the set it quantifies over has no name yet, and the property is measuring
the implementation while looking like it measures the language. A property that
cannot SAY "every invalid input" will silently say "every input the
implementation rejects first" instead — not through carelessness, but because the
implementation's vocabulary is the only one in scope. The repair is never a
stronger sentence; it is a new definition whose entire job is to BE the set.
`hex-valid` is seven lines carrying no logic the decoder did not already have,
and the property was unwritable without it. **Cost is not the signal — EXISTENCE
is.**

**A FAILURE-PATH TEST IS INCOMPLETE UNTIL THE SUITE PROVES IT CONTINUED PAST THAT
FAILURE.** Printing `FAIL` is not evidence. Under `set -e` a bare `kill` on an
already-exited PID, a `wait` that returns non-zero, or a background timer holding
an inherited pipe will each stop the harness before its own verdict — and the
run then LOOKS like it stopped at the first problem rather than like it died.
Four instances of this in one session, every one on a path that only runs when
something is wrong, every one found by RUNNING that path rather than reading it:
a bounded probe copied back into its unbounded form at two new call sites; a
timeout branch that returned nothing because the killer had already exited; a
timer subshell that made every SUCCESSFUL probe block for the full timeout; and a
setup-failure branch that printed its FAIL and then took the remaining 28 checks
with it, silently. So: assert the final summary line, count the checks that ran,
and make the harness fail LOUDLY when its own setup did not work — a check that
cannot tell its setup failed from the defect it hunts is worse than no check.

**TWO TRIGGERS SAY "AN ARTEFACT IS MISSING", AND THEY FIRE AT DIFFERENT TIMES.**
A design that keeps ACCUMULATING EXCEPTIONS is the early one. The late one, worth
more because it fires when you are already committed: **consecutive repairs to
one issue failing for UNRELATED reasons.** Stop patching and ask what artefact
would have made every failed repair unnecessary — the individual failures look
independent precisely because each is routing around the same absent structure,
and that is what makes the pattern hard to see from inside a repair.

    issue          the repairs that failed              the missing artefact
    -------------------------------------------------------------------------
    §14            prose → deeper dependency →          a transformation table
                   relocated ambiguity
    attempts.txt   under-specification → §10            an interchange format
                   contradiction → normative doubt
    "same shape?"  structural-walker edge cases         the canonical hash
    this file      move history → stale instructions    derivation from `gh`

Note what the artefacts have in common: none is a rule, and no amount of rule-
writing produces one. This is the same law as *asserting an obligation cannot
create the structure needed to satisfy it*, met from the other end — there you
notice the structure is presupposed, here you notice its absence only after
several repairs have failed in different directions.

**BOTH TRIGGERS SAY AN ARTEFACT IS MISSING. NEITHER SAYS WHICH ONE, AND THIS
DOES:**

> **A NEW ABSTRACTION EARNS ITS PLACE WHEN IT MAKES A PREVIOUSLY UNASKABLE
> QUESTION EXPRESSIBLE.**

Not when it removes duplication, not when it shortens the code, not when it feels
cleaner — those are all satisfiable by an abstraction that changes nothing about
what can be said. Three instances, in three different LAYERS, which is why this
is a parent and not an observation about one issue:

    layer          the question that could not be asked      the artefact
    ---------------------------------------------------------------------------
    specification  "what OPERATION produced this outcome?"   §14's table
    language       "does EVERY invalid string fail closed?"  `hex-valid`
    protocol       "should attempts.txt be NORMATIVE?"       an interchange format

In each case the intuition existed first and had nowhere to land. §14's readers
could describe results but not name the operation behind one; the old property
could only talk about the decoder's recursion, never about the set of invalid
strings; and #139 was undecidable as posed until the thing under discussion —
representation, as distinct from semantics — had a name.

**AND THE CAVEAT IS HALF THE PRINCIPLE, because the reverse is the tempting
misreading: naming the abstraction makes the question EXPRESSIBLE; it does not
ANSWER it.** `hex-valid` did not prove the decoder wrong. The transformation
table did not repair §14. The interchange format did not decide §10 — the
decision there was to DECLINE the obligation, which is an answer the artefact
made available rather than one it supplied. Each abstraction's whole value was
turning an intuition into a question an instrument could then answer.

Which fixes a causal chain that is easy to report backwards. Not
*property → prover → bug*, but:

    named domain → a meaningful property → one evaluation exposes the defect
                 → the prover confirms the REPAIR

On #121 the prover never adjudicated the broken decoder at all; it was still
searching when it was killed, and a single `eval` against the named domain
settled it immediately. Attributing that discovery to proof credits the
instrument instead of the abstraction that let the instrument be pointed
somewhere. **Proof confirmed the repair. It did not find the defect.**

**THE MISSING ARTEFACT IS OFTEN A TRANSFORMATION.** A static description usually
becomes simpler rewritten as a total transformation with named operations — the
pattern the whole project arrived at independently, each time replacing FACTS
with TRANSFORMATIONS:

    authority              → journal transitions over (authority, authority_rev)
    transfer               → an authority-state transition
    the compiler boundary  → Oath semantics → neutral requirements → backend
    capability provision   → a launch transformation that may fail
    the handler protocol   → transport → Request

Each time the winning design stopped saying WHAT EXISTS and started saying HOW
ONE STATE BECOMES ANOTHER. That is where the verbs come from, and a design with
no verbs is one whose operations the reader is supplying.

**WHEN REPEATED BLIND ROUNDS REPLACE ONE INFERENCE WITH ANOTHER, STOP REPAIRING
PROSE LOCALLY.** Normalize the transformation until every input distinction has
exactly one declared disposition owned by exactly one layer.

The signal is specific and worth recognising early: a round closes the ambiguity
you fixed and reports a NEW one at the boundary that fix touched. Three of those
in a row means the object model is DISTRIBUTED, not that the prose is missing a
sentence — a decision lives in one place and the rule applying it in another, and
the gap between them is invisible from either. The tell that confirms it: the
undetermined readings turn out to be cases whose DECISION was already agreed and
whose APPLYING RULE was absent. Nothing was wrong; it was scattered.

The descent this produced, each step reducing what a reader must combine mentally:

    distributed prose -> localized rules -> foundational definitions
                      -> a single transformation table

**THE PROSPECTIVE TELL, which is worth more than the retrospective one:** a
section that keeps saying what the RESULT should be without naming the OPERATION
that produced it. Outcomes scattered across paragraphs are the distributed model
before anyone has noticed. Look for verbs — preserve, canonicalize, discard,
lift, refuse — and if a section has none, the operations exist but are unnamed,
and a reader is inferring them. That is the shape #122 spent three rounds
eliminating, and it is visible before any round is dispatched.

Two things fall out that no amount of re-reading finds. Normalizing forces a
missing OPERATION into the open — a verb the old vocabulary never had, because
nothing in the scattered form had to name what kind of thing each rule did. And
asking one question per row, *where does this fact enter?*, finds IMPLEMENTATION
errors rather than wording ones: it caught a value stamped at the wrong moment
and a behaviour no supported stack could produce.

Close such an issue on a STRUCTURAL criterion, not an empirical one. "Every
distinction has exactly one disposition" is checkable; "the last round found
fewer problems" is a judgement that gets easier to make each time. A later round
can still find a wrong disposition or a missing row — but it will no longer be
finding *"I could not tell where this rule lives."*

**DO NOT WRITE A SECOND STRUCTURAL-EQUIVALENCE ALGORITHM. CANONICAL IDENTITY
ALREADY ANSWERS THE QUESTION** — the sharpest instance of *derive, do not
duplicate* above, kept in full because the cost it names is specific. In a
content-addressed language, canonical
structural equality IS hash equality: a definition's hash is the canonical
encoding of its declaration, so two structurally identical declarations have one
hash by construction. Any later code asking whether two Oath declarations are
"the same shape" must first justify why hash equality is not the correct
relation — and that justification is usually not available.

The cost of ignoring this is not verbosity, it is INCOMPLETENESS. A hand-written
matcher compares the fields its author thought to compare and silently accepts
anything differing elsewhere; a hash compares every byte of the canonical form.
Reconstructing the declaration and hashing it is also self-maintaining, because
it stays aligned with artifact identity instead of drifting from it as a second,
hand-maintained notion of "same type".

Written after a structural walker was built, reviewed, found to compare a type
constructor's tag while ignoring surplus fields that change its identity, and
then deleted in favour of four lines that construct the declaration and hash it.

**BEFORE REQUIRING A TRANSPORT DISTINCTION TO BE PRESERVED, ESTABLISH THAT EVERY
SUPPORTED STACK CAN STILL OBSERVE IT AT THE ADAPTER BOUNDARY.** If the transport
has already erased it, the specification must CANONICALIZE it, REFUSE the input,
or OMIT the rule. **It cannot mandate preservation of information no
implementation receives.**

Three rules written in one sitting failed this and were withdrawn the same day,
each found by implementing against a real stack rather than by reading. The
instances are in `docs/milestones.md`, deliberately not here.

The two failure directions are the same axis and both are cheap to commit: a
blind round finds obligations too LOOSE to derive; review finds obligations too
STRONG to implement. A specification is wrong one way when a reader cannot
reconstruct the rule, and wrong the other when an implementer cannot obey it.
Only measuring against a real stack distinguishes "strict" from "unsatisfiable" —
reading cannot, and neither can taste.

**BUT THE AXIS IS NOT THE WHOLE SPACE, AND WHAT IS OFF IT IS INTRODUCED BY THE
REPAIR ITSELF.** One backed-out requirement produced three defects in three
successive attempts, each found by the NEXT question rather than by re-reading:

  required but unspecified          conformance demanded bytes no reader could
                                    derive — the vocabulary lived only in the
                                    reference implementation
  locally repaired, globally        two normative sections assigned incompatible
  contradictory                     obligations; each was fine alone
  author uncertainty made           "WHETHER THIS IS REQUIRED IS UNDECIDED" was
  normative                         written INTO the rule, handing every reader
                                    the open question

The parent: **a repair cannot validate a property that requires stepping outside
the repair's own perspective.** You cannot see the missing vocabulary because you
know it, cannot see the contradiction because you are reading one section, and
your own uncertainty feels like honesty rather than like text. So the three
checks are not a review checklist — they are the three perspectives an edit
cannot occupy, which is also why each already has an instrument here: another
SECTION (contradiction), another IMPLEMENTATION or mechanism (missing
vocabulary — what the second kernel and the blind rounds are for), another
AUTHOR or round (unstated assumptions). Could a reader who knows only this
document reconstruct it; does any OTHER section now disagree; and does the
sentence transmit a decision or transmit that one is pending. **Never resolve the third by
writing the uncertainty down** — a specification that records its author's open
questions makes every implementation inherit them. Put the question on the issue
and leave the text determinate, even if determinate means "not required".

**THE GATE IS THREE-WAY: `oath eval` is the reference.** Never compare one backend
against the other alone — two identically wrong lowerings agree. Writing the second
backend already found a real defect in the first (a type assertion on a concrete
type meant `match` on a directly-constructed value would not build at all), which
is the oathrs N-version argument arriving in the compiler.

**PROPERTIES AND REVIEW FIND DISJOINT DEFECT CLASSES.** Across the application,
Oath's own instruments found ZERO of the twelve defects — properties caught
ordinary semantic failures, review caught shared mistaken boundaries, vacuity,
and near-misses the properties had encoded ALONG WITH the implementation. A
property whose guard restates the implementation's own predicate is not
independent evidence, and mutation scoring cannot see it either, because
mutating the body mutates the guard with it.

## Before adding anything to the protocol

Ask which of four concerns a proposal belongs to — they now evolve
independently, and only the first changes what a conformant implementation must
do: PROTOCOL (normative; apply §13's guardrails), REGISTRY implementation,
AGENT WORKFLOW, UI/rendering. "The registry needs X" stopped being an argument
for changing the protocol.

For a new JOURNAL field, ask: WHO WAS THE ONLY PARTY CAPABLE OF KNOWING THIS AT
THE MOMENT IT HAPPENED? (DESIGN.md, four categories)
  - a participant signed it        → ASSERTION. Preserve verbatim.
  - nobody — it is computed        → DERIVED. Do not store; re-derive on every verification.
  - only the actor performing it   → OBSERVATION. Preserve, and LABEL it as an
    observation: it is authoritative about the observer and nothing else, and no
    verifier can check it (`time`, the authenticated principal).
  - it defines what counts as SAME → EQUIVALENCE. Pin it, explicitly, versioned.

## Roadmap / backlog

GitHub issues on miclip/oath-lang. Closed as of 2026-07: team store & policy,
conformance + CI, O1 identity, prover fixpoint, stateful worlds, #14 (the live
registry above), #114 (the compiler boundary), #126
(required values), #118 (typed Str lowering). **#115 is still OPEN** — the LLVM
backend landed and is described above, but the issue is the wider #13b (typed
IR, monomorphisation, MLIR), and a shipped subset is not a closed issue. It was
listed here as closed until the startup check compared this line against `gh`.
Open research projects, each its
own session: **#117** (narrowed capability requirements), **#116** (signed
provenance attestation), **#65** (discovery roadmap), **#66** (delegated token
minting + authorized-key registration — an opt-in CONSTRAINT on onboarding, not a
prerequisite for it). Read closed issues + commit messages for the design
reasoning; `docs/milestones.md` for what each milestone established.

## Working in this repo

- Toolchain: Go ≥1.25, `z3` on PATH (`brew install z3`). `make build`.
- `make verify` re-puts every example in dependency order; `make prove`
  is single-pass (apiProve reaches the §7.2 self-lemma fixpoint internally,
  with lemma-growth gating and relevance filtering).
- **The corpus is `examples/*.oath` PLUS `apps/*/*.oath`.** `make verify`,
  `oathrs/conformance.sh` and the `Makefile`'s `APPS` list must stay in step, or
  divergence reports become confidently wrong.
- The `codebase/` store IS COMMITTED (journal included — it's the audit
  trail and is not regenerable). Never edit it by hand; keep it in sync by
  committing after put/prove runs.
- **`make verify` APPENDS TO THE JOURNAL EVEN WHEN NOTHING CHANGES.** It re-puts
  every example into the DEFAULT store, and a re-put at an unchanged hash is
  still journalled `accepted` — so a documentation-only session that runs it
  leaves ~208 entries republishing the corpus at its existing hashes, with no
  object, meta or name moving. Check `git status codebase/` before committing,
  and `git checkout HEAD -- codebase/log.jsonl` if the entries record nothing
  (`HEAD` deliberately — see the index caveat below, which this line had wrong
  until review read the two together). The
  journal is append-only and tamper-evident, which is exactly why it should not
  advance for a change that did not touch identity.
  **THE INVERSE COSTS MORE AND HAS NO WARNING SIGN:** iterating on a definition
  re-puts it at each intermediate hash, and every draft is journalled `accepted`
  — the evidence layer asserting a publication that was never intended. The
  abandoned OBJECTS are inert (unnamed, reachable only by hash); the ENTRIES are
  not. So when a change lands over several attempts, rebuild in ONE pass before
  committing. The naming rules cover this hazard for names only, and a repointed
  name leaves no trace of the drafts it passed through.
  **TWO THINGS MAKE THE OBVIOUS RESET WRONG, and both were found by review after
  one of them had already bitten:**
    - `git checkout -- <path>` restores from the INDEX, not HEAD. After any
      `git add`, it silently restores the drafts it was meant to discard. Use
      `git checkout HEAD -- codebase/ fixtures/`, which resets both.
    - NOT EVERYTHING UNDER `codebase/` IS REGENERABLE. `oath waive` records a
      hand-written justification into meta; a blanket reset destroys it and a
      rebuild cannot bring it back. So READ `git diff HEAD -- codebase/` first
      and confirm every change is a re-put you made — if any is authored, save
      it before resetting or do not reset at all.
  Note that the SAME blindness appears in both commands, which is why the second
  bullet was written wrong first: plain `git diff` compares against the INDEX, so
  once anything is staged the inspection reports a clean tree and certifies the
  destructive reset as safe. `git diff HEAD` is the one that answers the question
  being asked. An instrument guarding a hazard can share the hazard's blind spot.
- Re-putting a definition MERGES metadata (the old wipe-wart is fixed):
  verdict fields (proofs, mutation score, waivers) are hash-keyed facts and
  survive; naming is per-alias — structurally identical defs are one object
  with several names (`aliases` in meta), and each name keeps its own
  constructor vocabulary. `oath waive` records judged-equivalent surviving
  mutants with justification; waivers report separately, never as kills.
- Known flake: proofs give Z3 15s per goal; under machine load a goal can
  time out and record fewer proven props. Re-running `oath prove <name>`
  converges (prior proven props persist as lemmas).
- Author attribution: pass `--author <principal>` or `OATH_AUTHOR`;
  convention so far: `claude-main` for this assistant, humans by GitHub
  handle. Unattributed puts are journaled as `unattributed`.
- Commit style: explain the design decision, not just the change; honest
  about limitations. Falsified/unproven results are features, not
  embarrassments — never hide them.
- The examples double as the conformance corpus (SPEC.md §10): treat
  hash changes in `codebase/names.json` as meaningful diffs.
- **EDITED A FILE UNDER `docs/`? RUN `make webdocs` AND COMMIT THE RESULT.**
  Several `docs/*.md` are mirrored verbatim into `website/content/docs/`
  (`website/lib/refdocs.ts` lists them), and `make check-web-docs` fails CI on
  any drift. It is a separate gate from `check-doc-numbers`, it is not part of
  `make verify`, and nothing local reminds you — this landed on `main` red for
  exactly that reason after SPEC §14 and `effects.md` were edited.
- Run `codex review --uncommitted` before committing, and iterate until clean.

## The team store

`oath serve --http <addr> --tokens <file>` is the hosted layer: MCP over
HTTP, principals authenticated by bearer token (client `author` fields are
ignored), repoint policy in `<store>/policy.json` (authorship separation
via props/body lineage diffing, require_total, forbid_falsified,
min_mutation_score). Blocked submissions store the object and journal
`blocked`; the name doesn't move. docs/teamstore.md has the full model.
Never commit a tokens file.

## The second kernel

`oathrs/` is an independent Rust kernel, built BLIND from docs/SPEC.md +
fixtures/ only (never the Go source), passing all six conformance checks —
including byte-identical hashes, verify transcripts, analyses, and 189/189
proof outcomes. `oathrs/conformance.sh` is the cross-kernel gate; run it
after any change to the Go kernel that could touch semantics, and treat
any divergence as a spec bug or kernel bug to be filed. Preserve its
independence: never "fix" oathrs by copying from oath/ — fix the spec and
let a blind agent fix the Rust. `oathrs/DIVERGENCES.md` is the record of
every ambiguity found this way.

**RUN IT IN ORACLE MODE. The default is the nine-hour job.**

    OATHRS_CONFORMANCE_PROVE=oracle ./oathrs/conformance.sh    # ~2 min

This is the gate **every push runs in CI**, and it is the one that decides
whether a change is safe. It checks 1-4 live and settles 5-6 by byte oracle: an
outcome is a pure function of (script bytes, solver version, rlimit), so 447
byte-identical direct-attempt scripts under a pinned z3 DETERMINE identical
outcomes — including the ones that never prove.

The bare `./oathrs/conformance.sh` defaults to `full`, the cold empirical
re-derivation. CI runs that only on `schedule` / `workflow_dispatch` with a
350-minute timeout. **It took over nine hours locally**, because ~145 properties
do not prove and each burns the full 400M rlimit through every strategy,
serially. The instruction above used to omit the mode, which sent a session
straight into the scheduled job while believing it was running the push gate.

**What full mode still buys, stated narrowly:** `scripts.txt` pins DIRECT-attempt
scripts only, so the oracle does not byte-check the structural, lexicographic or
recursion-induction scripts. Full mode is the only thing exercising that half of
§7.2 — which is a reason to pin those scripts too, not a reason to re-derive an
unchanged corpus. See the issue on scoping it.

**DO NOT PUT A `CLAUDE.md` (or any session guidance) INSIDE `oathrs/`.** The
§10.0a blind surface deliberately lifts the `oathrs/` prefix ban in order to ship
the Rust to a blind subject, so a nested guidance file gets EXPORTED — and
session guidance is the purest form of what the export exists to withhold: it
states the reference implementation's conclusions in prose, which is exactly what
the subject is supposed to derive from the specification. `blind-export.py` now
refuses these by BASENAME at any depth, but the cheaper rule is not to write one.
The same caution applies to any new file under an allowlisted tree: ask what a
blind subject would learn from it that the SPEC does not say.

## Doc map

- `docs/milestones.md` — **what each milestone established, and what it
  deliberately did not.** The history this file used to carry.
- `README.md` — tour + quickstart. `DESIGN.md` — rationale, spec-strength
  problem, prior art, split-agent experiment writeup, roadmap phases.
- `docs/SPEC.md` — normative kernel spec (conformance target).
- `docs/effects.md` — capability model RFC; all stages resolved except
  time/interleaving. `docs/teamstore.md` — hosted store + policy model.
- `docs/generics.md` — dictionary-passing convention (#33 B1): a type
  class is a capability record; generic combinators in
  examples/generic.oath, proven over ALL dictionaries; B2/B3 deferred.
- `docs/refinements.md` — refinement types (#69), DESIGN ONLY. Settles the
  load-bearing question: refinement identity is SYNTACTIC (semantic identity
  would make hashing undecidable and solver-dependent, and would break the
  blind-kernel experiment); logically-equal refinements are related by the
  DISCOVERY layer, as bodies already are. Obligations get the guarantee ladder
  rather than being a hard type error.
- `docs/floats.md` — the IEEE `Float` identity decision (bit-identity, `==` is
  Leibniz/SMT `=`, canonical NaN); `docs/native-containers.md` — `Set`/`Map`
  compiled to native Go maps, differential-gated (#13).
- `docs/publishing.md` — THE PRACTICAL GUIDE: keygen, reserve, publish, delegate,
  and how to propose a standard-library addition (membership modes, the PR flow,
  what gets accepted). The how; `authority.md` is the why.
- `docs/authority.md` — THE AUTHORITY MODEL as protocol, not as history: why
  authority is a principal, why revisions version authority state, why reservation
  is explicit while exact-name ownership is inferred, why retention beats denial,
  why protocol roots are `key`/`sys` only, and what a third party can check from
  the journal alone. Written for someone building a second registry; deliberately
  contains no methodology. SPEC §8.7 is the normative text.
- `docs/licensing.md` — licensing as an EVIDENCE DOMAIN: publisher asserts signed
  terms in the envelope, registry derives what a composition permits under a
  named versioned model. UNSTATED is never permission and is contagious.
  `oath license <name>` / the `license` MCP tool.
- `docs/discovery.md` — `oath find`: discovery by property content-hash, not
  name (spec-query, cross-type, proof-implication); the invariant that the
  discovery layer never touches identity; `docs/egraph.md` — semantic
  canonicalization (body-equivalence via AC-normalization; type-directed).
- `docs/tutorial/circle.md` — a worked compiled example (reads a radius, prints
  circle area over exact ℚ); `docs/tutorial/discovery.md` — the four `find`
  modes end to end (by example, fresh spec, proof-implication, e-graph).
- Registry (the #14 layer): `docs/deploy.md` — the GCP/CI deploy walkthrough;
  `docs/registry-auth.md` — signatures-as-principals decision; `docs/registry-verification.md`
  — the verification worker pool + async require_proven gate; `docs/store-drivers.md`
  — the backend seam, GCS/Postgres drivers, and the cutover runbook.
- SPEC §13 + `docs/implementability.json` — independent implementability. NOTE
  FOR FUTURE SESSIONS: this is a GUARDRAIL, not the project. It has done its job
  (generalised past §12, found a fifth defect class, documented its own
  boundary); do not spend cycles refining the measurement. Use it when changing
  normative text, and treat §8.6's unwitnessed obligations as ordinary evolution
  of that section. DESIGN.md carries the durable results: the three-layer model
  (historical assertions / derived facts / equivalence), their epistemic
  contracts and distinct failure modes, and the line the record model follows —
  *the journal preserves everything the publisher signed and nothing the registry
  merely computed*.
  The ledger asks: can an unseen implementer build a section from the published
  surface WITHOUT inference? Distinct from conformance; no run has reached PASS yet.
  `scripts/blind-export.py` builds the isolated dispatch root (preflight-verified,
  no `.git`); `docs/experiments/blind-license-evaluation.md` is the round record.
- `docs/experiments/` — split-agent, rematch, and flywheel writeups.
  `webhook-friction.md` is the ranked demand list from #120.
- `oathrs/DIVERGENCES.md` — 60+ entries; the N-version findings record.
- History of decisions lives in commit messages (deliberately detailed) and
  DESIGN.md; external review responses are summarized in DESIGN.md.
