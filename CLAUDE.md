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
SECOND PRINCIPAL: the registry has exactly one authorized key, so the
cryptography, ownership model and licensing semantics are validated while the
SOCIAL model is not.

#66 (delegated token minting, authorized-key registration) is the gate. It does
not remove a defect; it creates a new actor, and with one come questions that are
product rather than protocol: can they reserve a namespace without operator
intervention, can ownership change without registry edits, does `explain` read
sensibly when the publisher is not you.

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
conformance + CI, O1 identity, prover fixpoint, stateful worlds, and #14 (the
live registry above). Open research projects, each its own session: **#13**
(compiler backend), **#65** (discovery roadmap), **#66** (delegated token minting
+ authorized-key registration gate — the registry's onboarding, deferred while
contribution is open). Read closed issues + commit messages for the design
reasoning.

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
