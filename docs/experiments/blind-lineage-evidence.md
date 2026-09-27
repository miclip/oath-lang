# Blind reading: SPEC §8.6.6, lineage evidence (#82)

One blind implementation of the newly normative §8.6.6, run before the section
landed. **This is NOT a round in `docs/implementability.json`, and must not be
added to it as one.** Rounds from 10 onward must be pre-registered in a committed
ledger revision before dispatch, and their `source` must be a commit the surface
reproduces from. This run was dispatched from an uncommitted working tree. Its
source is `9b85e5ccb85c0b614f54b47e5ed4bb695e9c94fc`, an unreferenced
`git stash create` object holding the pre-repair tree, and no ledger revision
pre-registered it. Recording it retroactively would claim a pre-registration that
did not happen. What follows is the evidence for the repairs it drove, not a §13
verdict.

## Surface

- Exporter: `scripts/blind-export.py --section 8.6.6`. The `8.6.6` surface was
  added for this run: prose only, with no normative data and no witnesses.
- Supplied: `docs/SPEC.md` only.
- Surface digest: `54e5032ace627b1c432ce36528239b81f20ca6c65ddfa78e934d83c6e7ae5626`.
- Preflight: PASS. The directory has no `.git`, no forbidden path and no
  reference-implementation content.

## Isolation

The subject was dispatched to the read-only search agent type, which does not
receive this project's CLAUDE.md, memory index or commit digest. Before reading
anything, it reported what context it had received. It named its own
system prompt, the environment block, the user's email, attribution lines, a
skills list, and deferred-tool names including `mcp__oath__*`. It reported **no**
CLAUDE.md, memory, summary or prior conversation. The tool names reveal the
project's name and the fact that it has a registry. That is domain, not work
state: nothing in them could tell a reader what §8.6.6 says. The subject listed
every path it read, all inside the dispatch root, and reported using no project
knowledge from outside it. What it did use from outside is RFC 8032, RFC 4648
and the IEEE-754 layout, all of which §13.1b already names.

Two limits remain:

- The **PROMPT** channel was written by the author, deliberately narrow: the task,
  the I/O contract, and the report headings. No paraphrase of §8.6.6 was included.
- The **MODEL** channel is shared: the subject is the same model family as the
  author.

## What the subject was asked

Implement §8.6.6 as a program that reads a store laid out per §8.1 and a name, and
prints each lineage's outcome, establishing `seq`, key and reason. Then report
every inference, contradiction and unobservable obligation **before** any test
data existed for it. The agent type is read-only, so the subject returned its
program inline. The dispatcher saved it verbatim, undoing only the HTML entity
escaping of the transport.

## Implementer statement

The subject gave no single-sentence statement. Its report says the program is
Python with a hand-written Ed25519 verifier and a strict O1 decoder, untested by
it because no data was supplied, and lists 23 numbered inferences. Several of
those concern input handling rather than §8.6.6 itself. They are summarised
below, not quoted.

## Agreement

After the report was in, the program was run against 13 stores built by the
reference kernel through its real publish paths, with crafted journals for the
edge cases. The cases were:

- two signing keys
- two bearer labels
- one signed lineage and one bearer lineage
- a no-op republication with rejected and other-name entries
- an A→B→A return by a bearer
- a missing first object
- an inconsistent `prev`
- a replayed first envelope
- the async-gate worker bind
- history not reaching the binding
- a stale-revision replay that `VerifyLog` accepts
- one key signing both lineages
- props and body established at different revisions

**All 26 lineage outputs agreed with the reference** on outcome, establishing
`seq`, key and reason category. That shows the hand-derived program and the
reference coincide on these stores. It does not show that the text determines
every case, which is what the report below is for.

## Findings and repairs

Each finding is listed with what was done about it.

**Contradictions repaired**

1. *The judging "iff" against "the async gate … therefore UNKNOWN".* A `put`
   entry that did carry a valid envelope got two answers, and the subject
   invented a sixth reason (`async_gate`) to resolve it. **Repaired:** the gate
   paragraph now says a `put`-kind entry is judged by the same rule, with no
   special case. The gate comes out UNKNOWN because the worker's entry carries no
   envelope.
2. *§8.6.4 rejects the journal, while §8.6.6 reports UNKNOWN.* Nothing said
   whether the derivation presupposes a verified journal. **Repaired:** it reads
   the journal as recorded, does not verify it, and judges a failing entry like
   any other. Journal verification is a separate result.
3. *§8.1 requires re-validating objects, while §8.6.6 only says "present".*
   **Repaired:** an object is present when it loads as §8.1 requires (hash,
   decode, re-validate), and absent otherwise.

**Inferences closed by repair**

- The declared type and the type-variable count were silently excluded from both
  lineages, and a kind change was inferred to change the body. **Now stated.**
  There is a new test and a mutation control: including the type in the body
  comparison makes it fail.
- A name absent from `names.json` was outside the text. **Now:** both lineages
  are UNKNOWN with the reason "the name is not bound", which the reference
  already returned. The reason list is now six, stated exhaustively.
- Partial envelope members were ambiguous between "no envelope" and "does not
  verify". **Now:** "no envelope" means none of the three members are present.
  Any of them present means the judging rule applies.
- The skipped-entry list mixed statuses and kinds and read as exhaustive.
  **Now:** every non-`applied` entry is skipped, whatever its kind or status.

**A pre-existing defect the subject resolved silently**

§8.1 described `objects/<hash>.bin` as "compact canonical Def JSON". The files
are canonical O1 bytes. The subject decoded them as O1, reasoning from §1 and the
hash-equals-filename rule, and did not flag the conflict. **Repaired** in §8.1. It
is recorded here because a silent resolution is exactly the kind of fact a later
round would never see.

**Left as they are**

- *Input handling:* accepting a store's parent directory, missing files and
  non-string members. These belong to the program's harness, not §8.6.6. §8's
  journal rules govern a verifying reader, and §8.6.6 now says it is not one.
- *`seq` ties and gaps* (the subject stably sorted by `seq`). This was left open
  at first. It was then **repaired** after external code review showed that a
  `seq`-keyed fold lets a duplicate `seq` misassociate a transition. §8.6.6 now
  visits entries in journal (line) order and associates by position, which is
  the reading the subject's stable sort already produced on these stores.
- *`seq` reported for history-level UNKNOWNs:* both implementations report none,
  and no obligation depends on it.
- *Byte comparison of canonical sub-encodings standing in for structural
  comparison:* sound by §1's injectivity. No change.

**Unobservable obligations, as the subject listed them**

"MUST NOT persist", "MUST NOT report UNKNOWN as unsigned" and "MUST NOT borrow a
pending envelope" hold by construction in both implementations, and no store can
witness them. Stability follows from the algorithm. The reference witnesses it
with an append test; the subject's program was not given such a test.

## What this does not establish

It is one subject, from one model family, on one prompt. It says the repaired
text removed the specific readings this subject had to choose. It does not say
the section is now free of inference: a later reader meets text this subject
never saw. No custody or attestation question was in scope.
