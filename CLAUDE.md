# Oath — context for Claude sessions

Oath is an AI-native "verified codebase kernel": definitions are
content-addressed (hash of canonical de Bruijn AST = identity), carry
machine-checked properties in their identity, and live in an immutable
object store where names are metadata. Built July 2026 by Michael + Claude
from a "what would a language designed only for AI look like?" conversation.
Positioning (settled after two external reviews): the syntax is disposable,
the substrate is the product.

## State of the project

- Two kernels: `oath/` (Go reference, ~7k lines, zero deps) and `oathrs/`
  (independent Rust, built BLIND from the spec — see "The second kernel").
  `docs/SPEC.md` is NORMATIVE — any change affecting hashes, verdicts, or
  semantics MUST update it; identity is the O1 binary encoding (§1), and
  encoding changes fork reality.
- Guarantee system, all real and CI-guarded: asserted → tested
  (deterministic, hash-seeded) → PROVEN (Z3, direct + structural induction,
  relevance-filtered lemma library per §7.2). Per-def verdicts: termination
  (lexicographic), confinement (closure-tracking), spec strength (mutation
  + justified waivers), provenance (append-only tamper-evident journal,
  authenticated principals on the HTTP store).
- 33 definitions fully PROVEN (insertion sort 7/7, reverse-involution, the
  KV world laws); honest exhibits remain deliberately: bad-reverse
  (falsified), spin (termination unproven), abs-small (tested-but-refuted).
- KEY OPERATIONAL FACT: mutation scores and waivers are structure×SEED
  facts (seeds derive from hashes) — never carry them across identity
  changes; `oath migrate-encoding` drops them by design. A fixture is only
  evidence once you know it was regenerated under the current identity.

## Roadmap / backlog

GitHub issues on miclip/oath-lang. ALL ENGINEERING ISSUES ARE CLOSED
(as of 2026-07-15): team store & policy, six-check cross-kernel
conformance + CI, O1 binary identity + migration, prover fixpoint +
relevance filtering, fixture coverage, stateful worlds. Open: #13
(compiler backend) and #14 (public registry) — research projects, each
deserving a dedicated session. Read closed issues for the full history;
commit messages carry the design reasoning.

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
- `docs/floats.md` — the IEEE `Float` identity decision (bit-identity, `==` is
  Leibniz/SMT `=`, canonical NaN); `docs/native-containers.md` — `Set`/`Map`
  compiled to native Go maps, differential-gated (#13).
- `docs/discovery.md` — `oath find`: discovery by property content-hash, not
  name (spec-query, cross-type, proof-implication); the invariant that the
  discovery layer never touches identity. `docs/tutorial/circle.md` — a worked
  compiled example (reads a radius, prints circle area over exact ℚ).
- `docs/experiments/` — split-agent, rematch, and flywheel writeups.
- `oathrs/DIVERGENCES.md` — 60+ entries; the N-version findings record.
- History of decisions lives in commit messages (deliberately detailed) and
  DESIGN.md; external review responses are summarized in DESIGN.md.
