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

## Round two — base `dda0b1a6ef88`

Dispatched against the repaired normative surface to test a different and
stronger claim:

> An unseen implementation can reproduce §12, the published model, verdicts, and
> evaluation identities **without inference**.

The round-one implementation and transcript are preserved as evidence and were
NOT adapted or extended; round two is a fresh agent with no access to either.

Isolation is now structural. `scripts/blind-export.py` materializes the dispatch
root by allowlist export from a pinned commit — `git archive`, so no `.git`, no
branches, tags, reflogs, commit subjects, or diffs exist to leak, and an
untracked working-tree file cannot ride along. A preflight then verifies the
PRODUCED DIRECTORY rather than trusting the dispatch instructions: it fails on
any `.git`, any forbidden path, any file the allowlist does not name, and on
reference-implementation identifiers appearing in the tree. This mirrors the
release-binary mutation gate, which verifies the artifact rather than the build
instructions, and it is checked in the failing direction against a deliberately
contaminated tree.

What isolation still cannot bound: a public remote, prior model exposure to this
project, or knowledge carried in from another session. Those are limited by
dispatching a fresh agent and by stating the residual risk, not by the script.
