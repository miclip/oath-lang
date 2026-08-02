# Oath — context for Claude sessions

Oath is an AI-native "verified codebase kernel": definitions are
content-addressed (hash of canonical de Bruijn AST = identity), carry
machine-checked properties in their identity, and live in an immutable
object store where names are metadata. Built July 2026 by Michael + Claude
from a "what would a language designed only for AI look like?" conversation.
Positioning (settled after two external reviews): the syntax is disposable,
the substrate is the product.

## State of the project

- Two kernels: `oath/` (Go reference, zero deps in the DEFAULT build — the
  optional GCS+Postgres store backend adds a Postgres driver only under
  `-tags cloud`; conformance/wasm/default builds stay dependency-free) and
  `oathrs/` (independent Rust, built BLIND from the spec — see "The second
  kernel"). `docs/SPEC.md` is NORMATIVE — any change affecting hashes, verdicts,
  or semantics MUST update it; identity is the O1 binary encoding (§1), and
  encoding changes fork reality.
- Guarantee system, all real and CI-guarded: asserted → tested
  (deterministic, hash-seeded) → PROVEN (Z3, direct + structural induction,
  relevance-filtered lemma library per §7.2; `oath hint` admits a named proven
  lemma the filter excluded — sound, identity-neutral, #67). Per-def verdicts: termination
  (lexicographic), confinement (closure-tracking), spec strength (mutation
  + justified waivers), provenance (append-only tamper-evident journal,
  authenticated principals on the HTTP store).
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

## The finish line (2026-08-01)

> A developer with no privileged access can install an Oath client, obtain
> authorization, publish a licensed artifact into their own namespace, and
> another developer can independently discover, verify, and consume it.

That is the milestone worth aiming at, and it is what turns Oath from a system
one person built into a system someone else can participate in. Every PIECE of it
now exists — signed publication, licence assertion and evaluation, namespaces,
cryptographic ownership, discovery, `explain`. What has never been tested is a
SECOND PRINCIPAL, so the cryptography, ownership model and licensing semantics
are validated while the SOCIAL model is not.

**#66 IS NOT THE GATE — verify the deployment, not this file.** The live registry
runs NO `--authorized-keys` allowlist (checked 2026-08-01 against the Cloud Run
service: no such arg, and only `OATH_STORE`, `OATH_STORE_LOCK`,
`OATH_TOKENS_FILE` in env). `authenticatePrincipal` computes
`canWrite := authKeys == nil || authKeys[pubHex]`, so **any key that signs can
already write.** An earlier version of this section claimed the registry had
exactly one authorized key and that #66 blocked the milestone; that was never the
deployed configuration.

So the finish line is probably reachable TODAY with no operator action, and the
next move is to RUN IT rather than to build the gate: a fresh key, `reserve`,
`publish --key`, then independent `find` / `explain` / `license` / `verify`. Two
cautions carried from this session — a reservation is PERMANENT, so the namespace
name must satisfy the naming rules below; and the ownership freeze means creation
requires a signed PUBLICATION envelope, so `oath publish --key` succeeds where a
bare `put --remote --key` is refused.

#66 remains real work (delegated token minting, authorized-key registration) but
it is what an operator turns ON to CONSTRAIN onboarding, not what a stranger needs
switched off. The open questions it raises are product rather than protocol: can a
newcomer reserve a namespace without operator intervention, can ownership change
without registry edits, does `explain` read sensibly when the publisher is not
you. Those get answered by the walkthrough.

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

## Where things actually stand (2026-08-01)

**The standard-library milestone is FINISHED.** `oath/*` is reserved to a project
key held in Cloud KMS (local copy destroyed before the first publication), and
holds four names — `List`, `length`, `append`, `reverse` — published in dependency
order, Apache-2.0, KMS-signed. `docs/receipts/001-*` is a generated receipt whose
eight checks all pass. A PR validation workflow (`stdlib-pr.yml`) exists and is
deliberately incapable of publishing.

**DELEGATION IS BUILT AND USABLE.** `oath delegate <ns>/* --to <pubkey>` and
`oath revoke <ns>/* --from <pubkey>`, plus the `delegate` MCP tool, all over ONE
acceptance path (`apiDelegate`). Seven conformance vectors witness the rules
(holder grants, revocation removes, non-holder cannot grant, a delegate cannot
delegate onward, stale grant refused, bad signature refused, a registry-authored
label creates nothing). `explain` renders holder and delegates distinctly — a
delegate must NEVER appear as the namespace owner.

DELEGATED CI PUBLISHING — two separate milestones, do not let one block the other:

**PROTOCOL: operationally demonstrated.** Step 8 ran against the live registry
with KMS-held keys (`docs/receipts/003-step8-passed.md`): accepted authority,
refused authority with the rejected intent PRESERVED and verifiable, revocation,
both post-revocation attempts blocked at the authority gate and preserved,
authority recovery by the holder, authorship unchanged throughout, and
re-delegation restoring publication. Journal 1247 → 1255, custody PASS.

The three authority outcomes now all have live end-to-end evidence:

  statement   journal        authority     
  accepted    preserved      changes       ✓
  rejected    preserved      unchanged     ✓
  invalid     not preserved  unchanged     ✓  (AUTH-REFUSALS-ARE-PRESERVED boundary)

**DEPLOYMENT: implemented, approval-gated, not yet run.** `stdlib-publish.yml`
awaits two OPERATOR actions — a `stdlib-publish` GitHub Environment with required
reviewers, and `GCP_PUBLISHER_SA=oath-publisher@oath-prod-503514.iam.gserviceaccount.com`.
Neither should be done by the party the gate constrains. Its first run should be a
NO-DELTA execution proving approval, WIF, gates, manifest reproduction and plan
derivation while signing nothing and writing nothing.

That run can claim something ADDITIONAL rather than repeating step 8: the approved
automation correctly uses the already-proven delegated authority path.

OPEN, from the exercise: #106 — authority_rev versions holder state, not
delegation state, so replay resistance for delegation is deduplication rather than
version progression. A design question, not a defect; surfaced by running the
protocol, not by inspection.

REMAINING: step 5 only — wire the post-merge publisher against a DELEGATED key,
and grant that key KMS access. Not done, and it needs its own authorization.

The post-merge publish workflow is NOT built, deliberately. Wiring it before the
above would make CI depend on a protocol path nobody can invoke normally, inspect
through the public surface, or reproduce from fixtures — turning hand-crafted
state into production authority.

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
  the verified `Def` closure. Typed IR and monomorphisation are #115.
- Every declared requirement resolves exactly once before launch or the program
  exits 70; undeclared authority is ABSENT from the binary (imports are
  requirement-driven), not merely unused.
- `oath provenance <file>` reads an embedded canonical manifest without executing
  the artifact and without opening a store. It is UNSIGNED — evidence about a
  cooperative artifact, not attestation (#116).

TWO KNOWN LIMITATIONS, carried forward so "backend-neutral" is not mistaken for
"semantically complete": the capability vocabulary is global and name-based (#117),
and the manifest is not bound to the bytes it describes (#116).

**THERE ARE NOW TWO BACKENDS.** `oath build --backend llvm` (`oath/llvm.go`) emits
textual LLVM IR plus a C runtime and shells out to clang — no new dependencies,
same pattern as emitting Go. It consumes `CompiledProgram` and references NOTHING
in compile.go, which `boundary_test.go` checks by resolved bindings. It is narrow
on purpose (datatypes, match, closures, records, Str literals, Bool, CLI entry)
and REFUSES the rest by name; it also refuses `http_request`, which is the point —
two backends may support different subsets of one vocabulary. #115's
proof-of-concept milestone is closed; #118 is typed lowering.

**WRITING A GATE? ASK WHAT MUTATION MAKES IT FAIL.** This repo is mostly gates, and
the recurring failure is a test that demonstrates the SETUP or one prerequisite and
is then read as evidence for the claim. If the only mutation that breaks a test is
in parsing, setup, or fixture generation, it is not witnessing its claim. Three
examples from one session: a confinement test asserting the absence of a symbol Go
never emits (passed for the control too); a "detects disagreement" test that
exercised only the decoder; a boundary check matching names rather than resolved
bindings, so a method on a shared type slipped through. VERIFY BY REVERTING —
undo the fix, watch it fail with the message you expect, restore. A test never
observed failing is a hypothesis. Prefer extracting the claimed behaviour into a
pure function and asserting every outcome, and assert the CONTROL so you know the
measurement discriminates.

**THE GATE IS THREE-WAY: `oath eval` is the reference.** Never compare one backend
against the other alone — two identically wrong lowerings agree. Writing the second
backend already found a real defect in the first (a type assertion on a concrete
type meant `match` on a directly-constructed value would not build at all), which
is the oathrs N-version argument arriving in the compiler.

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

## THE PROJECT HAS CHANGED PHASES (2026-08-02) — READ THIS BEFORE PICKING WORK

For months the bottleneck was "make the substrate trustworthy". It is now built
AND exercised: registry, authority, delegation, transfer, receipts, CI, standard
library, two native backends, proof-carrying compilation, and a backend-neutral
compilation boundary. Nearly every defect in those was found by USING them, not
by inspecting them.

  Phase 1  Can AI own software?                   registry, proofs, trust
  Phase 2  Can the trust model survive reality?   authority, delegation, CI, transfer
  Phase 3  Can the implementation be replaced?    Go backend, LLVM backend, the boundary
  Phase 4  Can someone build software and FORGET they are using Oath?   <- here

PHASE 4 IS NOT ABOUT HIDING EVIDENCE. The target, stated precisely: **guarantees
stay LOUD at trust boundaries and become QUIET during ordinary composition.** A
developer should see them when choosing or importing an artifact, accepting a
capability, publishing, compiling, crossing a policy boundary, or diagnosing a
refusal — and should not have to restate or re-inspect them while composing pieces
already admitted. So the question is not "how much can be hidden" but WHERE
EVIDENCE IS DECISION-RELEVANT VERSUS MERELY REPETITIVE. The webhook application
is a good instrument because it crosses several of those boundaries naturally:
ingress, signature verification, capability injection, outbound action, structured
output, compiled execution, and deployment under change.

**The next work is to DEPEND on Oath, not to improve it.** The roadmap, in order:

  1. **THE APPLICATION** and **#118** are INDEPENDENT PROBES OF THE SAME BOUNDARY,
     not a parent and a child. #118 asks what typed lowering actually requires;
     the application reveals which runtime and datatype features matter in
     practice. Whichever runs first constrains the second. Two dangers, one on
     each side: letting the application silently REDESIGN THE LANGUAGE, and
     letting #118 OPTIMIZE A SLICE NOBODY NEEDS.
  2. **Mark** — the first external contributor.

### THE NEXT SESSION'S INSTRUCTION, concretely — **it is #120**

Filed as an issue deliberately. Everything else in the backlog is a legible unit
of work and an application is not, so "what is next?" answered from the issue list
would keep returning the wrong thing. #120 makes the right answer the legible one.

> Turn `examples/webhook.oath` into something another system actually CALLS.
> Keep a friction log. Extend neither the language nor the protocol on the first
> pass. Then review the friction log before choosing the datatype and numeric
> slice for #118.

Friction log lives at `docs/experiments/webhook-friction.md` (the repo's existing
convention for this kind of record). Every time the application wants something
Oath lacks: write down WHAT was wanted, WHAT the workaround was, and HOW MUCH it
cost. That turns friction into evidence instead of backlog, and it produces a
demand-RANKED list of missing capabilities, language features and tooling — a
real deliverable even if the application itself turns out awkward.

An EXAMPLE is not an APPLICATION: the difference is whether anything depends on
it and whether it survives change. By the third modification you will know which
Oath surfaces protect a decision and which just make the author rehearse facts the
system already knows.

**DO NOT start another protocol feature.** Namespace aliases, more authority
operations, more receipt machinery, richer manifests, another workflow — these are
not bad, they are now OPTIMIZATION, and they are what a session naturally reaches
for because the backlog is full of them.

THE INSTRUCTION FOR THE APPLICATION, and it is the hard part: *build something you
would have built in Go six months ago, and do not add infrastructure unless the
application forces you to.* When the app needs something Oath lacks, WRITE IT DOWN
AND WORK AROUND IT. The deliverable is partly a list of what an application
actually demanded — which is worth far more than a language that quietly grew to
fit one program. "The application forced me to" is a judgement this assistant has
demonstrably made too generously.

Note that `examples/webhook.oath` already exists and runs. An EXAMPLE is not an
APPLICATION: the difference is whether anything depends on it and whether it is
maintained under change.

## Roadmap / backlog

GitHub issues on miclip/oath-lang. Closed as of 2026-07: team store & policy,
conformance + CI, O1 identity, prover fixpoint, stateful worlds, and #14 (the
live registry above), and **#114** (verified native entry points — effects stage 4;
see "The compiler boundary" below). Open research projects, each its own session:
**#115** (native optimization backend: typed IR, monomorphisation, LLVM), **#117**
(narrowed capability requirements — deliberately NOT part of #115, so a second
backend is not hostage to an effects-language redesign), **#116** (signed
provenance attestation), **#65** (discovery roadmap), **#66** (delegated token minting
+ authorized-key registration — an opt-in CONSTRAINT on onboarding, not a
prerequisite for it; the live registry has no allowlist and writes are open to any
signing key). Read closed issues + commit messages for the design reasoning.

## Working in this repo

- Toolchain: Go ≥1.25, `z3` on PATH (`brew install z3`). `make build`.
- `make verify` re-puts every example in dependency order; `make prove`
  is single-pass (apiProve reaches the §7.2 self-lemma fixpoint internally,
  with lemma-growth gating and relevance filtering).
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

## Doc map

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
- `oathrs/DIVERGENCES.md` — 60+ entries; the N-version findings record.
- History of decisions lives in commit messages (deliberately detailed) and
  DESIGN.md; external review responses are summarized in DESIGN.md.
