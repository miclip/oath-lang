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

## PHASE 5 — READ THIS BEFORE PICKING WORK

  Phase 1  Can AI own software?                   registry, proofs, trust
  Phase 2  Can the trust model survive reality?   authority, delegation, CI, transfer
  Phase 3  Can the implementation be replaced?    Go backend, LLVM backend, the boundary
  Phase 4  Can someone build software and FORGET they are using Oath?
  Phase 5  Can the compiler FAITHFULLY OBSERVE the language?             <- here

Sharper: **Phase 5 is about reducing the semantic gap between specification and
execution.** #114, #115, #126 and #118 all fit that description, and none of them
made Oath more expressive — they made existing semantics more faithfully
realized.

**IMPLEMENTATION IS NO LONGER THE LIMITING FACTOR; CALIBRATION OF CLAIMS IS.**
That sentence explains nearly every significant correction of recent sessions:
distinguishing launch failure from failure before OBSERVABLE STARTUP; separating
provisioning from authority narrowing; refusing rather than replacing; a backend
SUBSET versus language semantics; deriving a witness's universe from the CLAIM
rather than the implementation; and evidence versus mechanism.

**So the metric is not size.** The temptation is to equate "the compiler is
getting bigger" with "the compiler is getting better". The opposite is the
measure: *the compiler gets better when its observable behaviour converges on the
language semantics, regardless of how many features it implements.* This is where
the project stopped accumulating MECHANISMS and started accumulating EVIDENCE —
most of the important commits were not new-feature commits; they narrowed a claim
until it exactly matched what had been demonstrated. Continue that.

### THE QUEUE — one ordering, in three buckets; position is not priority

The buckets encode DIFFERENT CLOCKS, not just different priorities:

  more EXPENSIVE if delayed   #122, #121, #117/#69, #133 — while the runtime
                              is still small enough to reshape
  more VALUABLE if delayed    #130, #134 — after more evidence has accumulated
                              to calibrate them
  waiting for a TRIGGER       #128

**Compiler/runtime — the window is closing.**

  1. ~~**#122** handler header model~~  SHIPPED as SPEC §14 — see below
  2. **#121** hex-decode                unblocks real use
  3. **#117 / #69** scoped authority    language design, and see below
  4. **#133** scalar-only Str           language design, NORMATIVE

**#122 SHIPPED, WITH ONE STEP OUTSTANDING.** SPEC **§14** is the handler
protocol's normative Request model — read it there; this file deliberately does
NOT restate its rules. It is new normative text, so this repo's definition of
done requires a **blind round scoped to it, and that round has NOT been run.**
`scripts/blind-export.py --section 14` is wired and preflights clean; the surface
is deliberately prose-only, because a vector file would let a subject reproduce
the answer without deriving the rules.

**THAT IS ALSO WHY THE RULES ARE NOT SUMMARIZED HERE.** A dispatched session
inherits this file in its system prompt, so a summary of §14 would hand a "blind"
subject the model without it ever opening the specification — the §13
contamination problem arriving through the coaching channel rather than the
export. The general rule, worth keeping past #122: **CLAUDE.md points at
normative text and never paraphrases it.** A paraphrase can also drift from the
SPEC, which is the same disease as the four superseded queue orderings.

**Feedback/tooling — the window is NOT closing.** Improves confidence in future
work; nothing depends on it immediately.

  5. **#130** vacuity signal, guard/subject overlap
  6. **#134** typed refusal reasons

**Documentation hygiene.**

  7. **#128** — DEFERRED UNTIL AFTER THE NEXT EXTERNALLY-VISIBLE MILESTONE. It
     has a TRIGGER, not a position. It changes no semantics, unblocks no user,
     and closes no narrowing window — and it is small and easy, which is
     precisely why it would interrupt architectural momentum if it sat in the
     middle of the queue. **Do not let "it is open" become "it is next."**

### Standing instructions attached to the queue

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
registry above), #114 (the compiler boundary), #115 (the second backend), #126
(required values), #118 (typed Str lowering). Open research projects, each its
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
