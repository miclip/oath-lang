# Oath — context for Claude sessions

Oath is an AI-native "verified codebase kernel": definitions are
content-addressed (hash of canonical de Bruijn AST = identity), carry
machine-checked properties in their identity, and live in an immutable
object store where names are metadata. Built July 2026 by Michael + Claude
from a "what would a language designed only for AI look like?" conversation.
Positioning (settled after two external reviews): the syntax is disposable,
the substrate is the product.

## State of the project

- Kernel + CLI in `oath/` (Go, zero deps; ~5k lines). `docs/SPEC.md` is the
  NORMATIVE spec — any kernel change that affects hashes, verdicts, or
  semantics MUST update it (the hash is identity; encoding changes fork
  reality).
- Guarantee ladder is fully real: asserted → tested (deterministic,
  hash-seeded) → PROVEN (Z3: direct + structural induction + lemma
  library). Plus per-definition verdicts: termination (Foetus-lite),
  capability confinement (no-escape), spec strength (mutation testing),
  provenance (append-only journal with principals).
- ~29 functions / 97 properties fully proven (116 proven overall), incl.
  reverse-involution and insertion-sort correctness in full (sorted +
  permutation + idempotent + reverse-invariant, via the sorted-fixpoint
  lemma chain). Five modules (tree/interval/queue/rle/ediv) were authored
  end-to-end by split-agent pairs — spec agent and implementer firewalled,
  kernel refereeing; journal seq 180+ is the record. Deliberate honest
  exhibits: `bad-reverse` (falsified), `spin` (termination unproven),
  `abs-small` (tested-but-refuted at x=-401), `leak`/`stash` (ESCAPES).
- Effects = capability passing (records of functions), no effect system:
  see `docs/effects.md`. MCP server: `oath serve` (stdio), registered in
  `.mcp.json` — build the binary first or the server won't start.

## Roadmap / backlog

GitHub issues #2–#15 on miclip/oath-lang, four milestones:
M1 team store & policy (next), M2 conformance (spec ✓ → Rust/WASM kernel →
cross-kernel CI), M3 prover & effects depth, M4 research horizon.
Agreed ordering (Michael + external Codex review): spec before Rust port;
hosted store creates the need before the port happens.

## Working in this repo

- Toolchain: Go ≥1.25, `z3` on PATH (`brew install z3`). `make build`.
- `make verify` re-puts every example in dependency order; `make prove`
  runs the prover over everything twice (a second pass lets a definition's
  own earlier-proven props serve as lemmas — reverse-involution needs it).
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
- `docs/effects.md` — capability model RFC + staged roadmap.
- History of decisions lives in commit messages (deliberately detailed) and
  DESIGN.md; external review responses are summarized in DESIGN.md.
