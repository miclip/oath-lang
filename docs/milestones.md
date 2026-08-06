# Milestone records

What each milestone established, and what it deliberately did not. This file is
**not** loaded into every session — `CLAUDE.md` is, and it carries only the
instructions a session needs BEFORE it picks work. Anything here is history:
read it when you are working on the thing it describes, or when you want to know
why a decision went the way it did.

The rule that keeps the split honest: **if a paragraph tells a future session
what to DO, it belongs in `CLAUDE.md`; if it tells them what HAPPENED, it belongs
here.**

---

## Phase 4 — can someone build software and FORGET they are using Oath?

PHASE 4 WAS NOT ABOUT HIDING EVIDENCE. The target, stated precisely: **guarantees
stay LOUD at trust boundaries and become QUIET during ordinary composition.** A
developer should see them when choosing or importing an artifact, accepting a
capability, publishing, compiling, crossing a policy boundary, or diagnosing a
refusal — and should not have to restate or re-inspect them while composing
pieces already admitted. So the question was never "how much can be hidden" but
WHERE EVIDENCE IS DECISION-RELEVANT VERSUS MERELY REPETITIVE.

The webhook application was the instrument because it crosses several of those
boundaries naturally: ingress, signature verification, capability injection,
outbound action, structured output, compiled execution, and deployment under
change.

---

## #120 — the application, and the friction log

`apps/github-webhook` is a GitHub webhook receiver that verifies
`X-Hub-Signature-256`, scans the repository out of the signed body, and appends a
self-describing record to a log `report.sh` consumes. `deliver.sh` signs with
**openssl** and posts with **curl** — it shares no code with Oath, which makes
every accepted delivery a real cross-implementation check of `hmac-sha256`. It is
wired into `make check-app` and CI, and it runs against a COPY of `codebase/`
because `oath put` is the only way to check a source file and it always writes.

The language and the protocol were UNCHANGED: `oath/`, `oathrs/`, `docs/SPEC.md`
and `examples/` were not touched; the 21 new definitions are the application.

**`docs/experiments/webhook-friction.md` is the deliverable, and it is ranked.**
The top four, with what they actually cost:

1. **`process_env` grants the environment, not a variable.** Launched with the
   secret unset, version 1 bound its port and ACCEPTED A DELIVERY FORGED UNDER
   THE EMPTY KEY. The #114 launch gate did what it promises and it was not
   enough — it is loud about capability KINDS and silent about capability
   CONTENT. Six properties passed throughout. Closed structurally by #126.
2. **The handler protocol has no header model (#122).** `X-GitHub-Event` — what
   GitHub documents and sends — is not what an Oath handler sees; net/http
   canonicalizes it to `X-Github-Event`. One backend's normalization is visible
   in Oath source and nothing propagates it to the author.
   `apps/github-webhook/hdr-probe.oath` is the runnable witness.
3. **No JSON, and no correct bytes→text.** `str-bytes` is PROVEN; its inverse
   cannot be written correctly, because `Str` is codepoints and a body is bytes
   and both are the same shape. **#118's datatype slice should be byte lists and
   text — the numeric demand was one `show-nat`.**
4. **The corpus has two scopes.** `oath fixtures` reads the STORE;
   `oathrs/conformance.sh` and `make verify` read `examples/`. Adding definitions
   anywhere else produced sixteen confident, wrong divergence reports. Both now
   enumerate `examples/*.oath` plus `apps/*/*.oath`.

Three findings about the INSTRUMENTS, which matter as much: a property passed 200
generated cases and was false (a tab forging a column — the generator cannot
reach adversarial byte sequences); strengthening the artifact silently made its
strongest property vacuous while still reporting `passed 200 cases`; and a
property whose guard restates the implementation's own predicate is not
independent evidence — `application/jsonp` passed a `str-prefix` test in both.

Also filed from the exercise: **#121** (`hex-decode` kept the prefix it decoded,
so `examples/webhook.oath` accepted `<valid digest>zz` while claiming to fail
closed — filed rather than fixed at the time, because it changes the conformance
corpus; fixed later with total-failure semantics and a validity predicate the
strengthened property could quantify over).

### What the exercise improved, stated narrowly

> Oath did not improve its own ability to detect mistaken confidence. The project
> improved the PRACTICES used to detect it, and identified two concrete
> mechanisms that could eventually make part of that detection automatic (#130).

The evidence for the narrow reading: across the application, **Oath's own
instruments found ZERO of the twelve defects.** The properties were not lazy —
they falsified four generated cases during authoring and forced a real fix — but
every near-miss came from independent review. Formal instruments caught ordinary
semantic failures; review caught shared mistaken boundaries, vacuity, and
near-misses the properties had encoded ALONG WITH the implementation.

---

## #126 — required values are structural

**Required values are structural and enforced. Callable authority exists but
remains coarse.**

`#126` split the provisioning half out of #117. A capability-record field whose
type is a VALUE rather than a function is a required value: the host must supply
it before any Oath code runs, or the program does not start.

    (defn gh-webhook [] [(caps {emit (-> Str Str) secret Str}) (r Request)] Response

THE IDENTITY STATEMENT, worth preserving exactly:

> Required launch data is tamper-evident because it is expressed in the
> capability record's field types, and those types already participate in
> artifact identity.

No side manifest, no deploy-time assertion, no new identity mechanism. `{env}`
hashes to #160db1993221 and `{env emit}` to #7db989101eeb; nothing had to be
added for this to be tamper-evident.

THE ARCHITECTURAL FINDING is the confinement repair, not the feature:

> "Capability record" was accidentally modeled as "record whose fields are all
> authority-bearing functions."

Value fields exposed it — every use of one was reported as an escape and `oath
build` refused to hand the program the real world. The repair distinguishes by
TYPE: a projected function is authority and still subject to escape analysis; a
projected non-function is data. The surviving control — a bare function
projection still ESCAPES — is what makes that a correction rather than a
relaxation, and `CLAUDE.md` carries the instruction not to delete it.

---

## #118 — what the compiler can and cannot do

> The LLVM backend observes `Str` according to Oath's CODEPOINT semantics while
> retaining PACKED UTF-8 as an internal representation.

      Bool, Int, packed Str        faithful runtime representations
      Str matching                 exposes codepoints as Int, exact remainder
      int64 and scalar subsets     FAIL CLOSED — refused, never wrapped or replaced
      the verified Def closure     REMAINED SUFFICIENT (4660/4660 subterm types
                                   recoverable; 286/286 polymorphic call sites carry
                                   instantiation in the RAW canonical bytes)
      a typed lowering IR          NOT REQUIRED — #114's no-IR decision holds, and
                                   now holds against a corpus measurement
      everything else              explicitly refused and named

STILL REFUSED, deliberately: arithmetic, Rat and Float, Set/Map, dynamic Str
construction, the handler protocol.

Open from this work: **#133** (is Str defined only over Unicode scalar values —
NORMATIVE, needs SPEC prose and a scoped blind round; the information-loss half
is already fixed as a backend subset boundary) and **#134** (typed refusal
reasons instead of prose).

---

## The standard library, delegation, and the finish line

### The finish line

> A developer with no privileged access can install an Oath client, obtain
> authorization, publish a licensed artifact into their own namespace, and
> another developer can independently discover, verify, and consume it.

Every PIECE of it exists — signed publication, licence assertion and evaluation,
namespaces, cryptographic ownership, discovery, `explain`. What has never been
tested is a SECOND PRINCIPAL, so the cryptography, ownership model and licensing
semantics are validated while the SOCIAL model is not.

**#66 IS NOT THE GATE — verify the deployment, not this file.** The live registry
runs NO `--authorized-keys` allowlist (checked 2026-08-01 against the Cloud Run
service: no such arg, and only `OATH_STORE`, `OATH_STORE_LOCK`,
`OATH_TOKENS_FILE` in env). `authenticatePrincipal` computes `canWrite := authKeys
== nil || authKeys[pubHex]`, so **any key that signs can already write.** An
earlier version of this claimed the registry had exactly one authorized key and
that #66 blocked the milestone; that was never the deployed configuration.

So the finish line is probably reachable TODAY with no operator action. The
method, recorded so nobody re-derives it: a fresh key, `reserve`, a fresh key,
`reserve`, `publish --key`, then independent `find` / `explain` / `license` /
`verify`. Two cautions — a reservation is PERMANENT, so the namespace name must
satisfy `CLAUDE.md`'s naming rules; and the ownership freeze means creation
requires a signed PUBLICATION envelope, so `oath publish --key` succeeds where a
bare `put --remote --key` is refused.

The walkthrough belongs to the first external contributor, who is a person rather
than a scheduled step.

#66 remains real work (delegated token minting, authorized-key registration) but
it is what an operator turns ON to CONSTRAIN onboarding, not what a stranger
needs switched off.

### The standard library

**FINISHED.** `oath/*` is reserved to a project key held in Cloud KMS (local copy
destroyed before the first publication), and holds four names — `List`, `length`,
`append`, `reverse` — published in dependency order, Apache-2.0, KMS-signed.
`docs/receipts/001-*` is a generated receipt whose eight checks all pass. A PR
validation workflow (`stdlib-pr.yml`) exists and is deliberately incapable of
publishing.

### Delegation

**BUILT AND USABLE.** `oath delegate <ns>/* --to <pubkey>` and `oath revoke <ns>/*
--from <pubkey>`, plus the `delegate` MCP tool, all over ONE acceptance path
(`apiDelegate`). Seven conformance vectors witness the rules (holder grants,
revocation removes, non-holder cannot grant, a delegate cannot delegate onward,
stale grant refused, bad signature refused, a registry-authored label creates
nothing). `explain` renders holder and delegates distinctly — a delegate must
NEVER appear as the namespace owner.

### Delegated CI publishing — two milestones, do not let one block the other

**PROTOCOL: operationally demonstrated.** Ran against the live registry with
KMS-held keys (`docs/receipts/003-step8-passed.md`): accepted authority, refused
authority with the rejected intent PRESERVED and verifiable, revocation, both
post-revocation attempts blocked at the authority gate and preserved, authority
recovery by the holder, authorship unchanged throughout, and re-delegation
restoring publication. Journal 1247 → 1255, custody PASS.

The three authority outcomes all have live end-to-end evidence:

      statement   journal        authority
      accepted    preserved      changes       ✓
      rejected    preserved      unchanged     ✓
      invalid     not preserved  unchanged     ✓  (AUTH-REFUSALS-ARE-PRESERVED)

**DEPLOYMENT: implemented, approval-gated, not yet run.** `stdlib-publish.yml`
awaits two OPERATOR actions — a `stdlib-publish` GitHub Environment with required
reviewers, and `GCP_PUBLISHER_SA=oath-publisher@oath-prod-503514.iam.gserviceaccount.com`.
Neither should be done by the party the gate constrains. Its first run should be a
NO-DELTA execution proving approval, WIF, gates, manifest reproduction and plan
derivation while signing nothing and writing nothing — which claims something
ADDITIONAL rather than repeating the protocol demonstration: that the approved
automation correctly uses the already-proven delegated authority path.

OPEN: **#106** — `authority_rev` versions holder state, not delegation state, so
replay resistance for delegation is deduplication rather than version
progression. A design question, not a defect; surfaced by running the protocol,
not by inspection.

REMAINING: wire the post-merge publisher against a DELEGATED key, and grant that
key KMS access. Not done, and it needs its own authorization. The post-merge
publish workflow is NOT built, deliberately: wiring it before the above would
make CI depend on a protocol path nobody can invoke normally, inspect through the
public surface, or reproduce from fixtures — turning hand-crafted state into
production authority.

---

## SPEC §14's withdrawn rules — the instances behind the transport-distinction rule

`CLAUDE.md` carries the principle and deliberately not these cases: round 10's
subject inherits that file, and naming them would coach it on the §14 questions
it exists to derive. This file is not in a dispatched session's context.

Three normative rules were written and withdrawn the same day, each found by
implementing against a real stack rather than by reading:

- **Obsolete line folding.** The rule required a 400. Go's `net/textproto`
  unfolds during parsing and hands the adapter the joined value with no trace a
  fold occurred, so the adapter cannot refuse what it can no longer see. Replaced
  by canonicalization: exactly one SP, pinning the count RFC 9112 leaves open.
- **Absolute-form authority.** The rule said the authority must never be taken
  from the request target. RFC 9112 §3.2.2 requires precisely that — an origin
  server MUST ignore the `Host` field and use the target's authority — so the
  rule contradicted HTTP, and Go was right.
- **Authority agreement.** The rule was extended to HTTP/1.1 absolute form, which
  has a precedence rule rather than a conflict to detect. Scoped to HTTP/2+.

All three mandated a distinction the transport layer had collapsed before any
Oath code ran. Two of them were written in the same sitting as each other.

**The pattern to watch for:** a rule phrased as "MUST preserve X" where X is
recovered from a parsed representation rather than from the wire. If the host
library normalized, joined, promoted, or deleted X during parsing, the rule is
unsatisfiable no matter how carefully it is worded.

---

## The coaching leak: what the record actually supports

`CLAUDE.md` carries the rule — coaching material may point at normative text and
must not restate it — and `make check-coaching-leak` enforces the IDENTIFIER
half of it: naming a rule fails CI, describing one in prose does not. Saying
the gate enforces the rule, unqualified, was itself a version of the overclaim
this section exists to correct. A first
draft of this section claimed the two blind rounds of the handler model
empirically validated that — round 9's subject could have read the rules in the
coaching channel, round 10's could not. **That claim is false and is recorded
here as false**, because the correction is the more useful artifact.

Checked against git: at round 9's dispatch commit, `CLAUDE.md` already said
*"this file deliberately does NOT restate its rules"* and named none. The leak
was found and removed while PREPARING round 9, which that round's contamination
notes say. So **`CLAUDE.md` named no rule of the section under test at either dispatch**,
and there is no before-and-after to compare. The author of the comparison was the
person who had made the fix and then forgotten it.

That is deliberately narrower than "both channels were clean", which a second
draft of this retraction asserted and which the evidence does not reach: the memory index was checked BY HAND while preparing round 9 and that
check is an attestation rather than a mechanical one, and the gate covers only
`CLAUDE.md` and only identifiers, so a paraphrase anywhere passes. Git establishes the contents of one file. **The
retraction of an overclaim contained a fresh overclaim**, which is worth the line
it costs to record.

### What the record does support

- The same file acquired the leak **four times in one session**, three of them
  within ten minutes of the rule against it being written down. Every one was
  caught by review rather than by the author.
- The response that survives is therefore mechanical rather than attentive: a
  gate that fails CI, so the fix does not depend on catching it by hand each
  time. That is a claim about durability, not about either round.
- Round 10's subject reported seeing project notes and stated that nothing in
  them states any rule of the section under test. That is **consistent with** a
  clean channel and is an attestation about available context — not proof, since
  both rounds also inherited an unchecked memory index and the gate cannot detect
  a paraphrase.

### The distinction worth keeping

*The mitigation exists* and *the mitigation changed what a subject could know*
are different claims, and only the first is established. Asserting the second
required a comparison that does not exist — which is the same overclaim habit
this project keeps finding, committed in a section congratulating itself on
having closed one.


---

## Where the defects were found, session by layer

Worth preserving because it looks discouraging and is not. Over one session,
independent review stopped finding bugs in Oath and started finding bugs in the
MEASUREMENTS about Oath:

    bug in the implementation          adapter: method/path octets, body-read error
    bug in the specification           §14's rules, three withdrawn the day written
    bug in the blind-review apparatus  the export leaked coaching material by path
    bug in the contamination controls  the gate covered one file, not the channel
    bug in the disposition taxonomy    no state for "the subject never reached it"
    bug in the gate validating that    twelve, each found on the previous fix
    bug in the probe validating THAT   a crash read as a catch

That is what a system pushing trust one layer deeper looks like. Each time a
layer became reliable enough, the next became the weakest link and therefore
visible.

**It is not monotonic, and the exception matters.** The `received-at` bug is an
IMPLEMENTATION bug and it surfaced last — the timestamp was taken after the body
was read, recording completion rather than receipt. It was found by the
transformation table's own question, *where does this fact enter?*, applied per
row. Earlier layers do not retire as the weak link moves up; finding what remains
in them requires the higher layers to exist first.

## The startup instrument — why it was void for four sessions, and what its first two runs found

**The check.** `CLAUDE.md`'s startup sequence step 1 asks a reader with no session
state what the file tells them to do first. It measures ORDERING, bucket
ASSIGNMENT and PLACEMENT — the queue's judgment, as distinct from its membership.
(Bucket MEMBERSHIP did gain an authority in the same session, via the coverage
check added to step 2; assignment did not, and cannot.)

**Why it was void.** It did not run for four consecutive sessions on the record
in `CLAUDE.md` at the time, and did not run in the session that repaired it
either — that session ran the REDEFINED instrument instead, which is a different
thing and is why it is not counted as a fifth void run. The diagnosis on record
blamed session continuity: handoff prompts, compaction summaries, `/compact`
being automatic on any long session. That framing made it sound like a discipline
problem with a lucky-session cure. It was structural. A
session cannot be its own stateless reader, because three injections land before step 1
can execute — a SessionStart hook digest (which in the observed case named the
exact issue last looked at), user memory loaded every session, and `CLAUDE.md`
itself, in context before the step that says to read it. The claim's universe is
*readers with no session state*; the session's own first act was never in it.

**The repair** was to point the instrument at a subject that could occupy that
universe — a dispatched subagent, believed at the time to have fresh context, no
compaction summary and no hook digest — rather than to retire the check. That
belief was half wrong, and run 3 below is where it broke. Calibration matters: such a
reader shares the author's model and priors, so it cannot witness "this file
assumes knowledge only a Claude session has", but it can witness prose describing
work that already shipped. It is not the banned simulated newcomer: that rule
forbids inventing a reader with different KNOWLEDGE, which cannot be un-known;
this substitutes different CONTEXT, which can be withheld.

**Run 1 (2026-08-04) — valid, seven findings.** Context audit clean. It reported:
every numbered queue item was individually deferred and the section never stated
that sum, so a reader obeying it starts nothing; the one actionable instruction
(#150) sat ~900 lines away, outside all buckets, under commit hygiene; #150's
urgency argument carried no availability note, so the reader picked a task blocked
until 2026-08-09; "nothing is currently expensive-if-delayed" was live while #149
was open; #149 — an unguarded attacker-reachable crash — had no bucket at all;
#147's retirement notice sat fifteen lines above its own retraction with only the
retirement formatted as queue state; and three sections enumerated open work and
disagreed on the set.

**What the status check could not see: all of it.** Step 2 as it stood was
COMPLETE throughout — every issue's state was recorded correctly. What was wrong
was judgment, which no command validates.

Two of the seven were the exception that proved worth automating: an open issue
sitting in no bucket, and a bucket row for a closed one, are MEMBERSHIP facts
with an external authority. Step 2 gained a coverage check for exactly those in
the same session, so a rerun of run 1 today would find five, not seven. The other
five — ordering, placement, an urgency claim contradicting a bucket, an
availability constraint stated in the wrong section — remain judgment and remain
invisible to any command.

**Run 2 — VOID, and it diagnosed its own voidness.** A second stateless reader
landed on the right item in one pass and reported that the previously-misdirecting
passages now carry explicit demotions. That looks like verification and is not:
run 1's findings had been written into `CLAUDE.md` itself, ahead of the queue, so
the reader met the previous run's conclusions before reaching the section under
test. It said so unprompted. By step 1's own rule the run is VOID, and the
honest state after both runs is **one valid run, one void run, repair
unverified.**

**And a third run would ALSO be void as things stand.** A second adversarial pass
found that stripping the run record from `CLAUDE.md` did not strip its
CONCLUSIONS: six of run 1's seven findings survive there as paraphrases in the
repaired prose, each attached to the rule it motivated. That is the same leak in
its second form, and every repair added a fresh instance, because the natural way
to record a fix is to say what it fixed. Removing them is a larger edit to
`CLAUDE.md`'s established style than one session should make unilaterally, so it
is a decision left open rather than a defect quietly carried.

Recording it as "the repair verified" was the first draft here, and it is the
error the runs exist to catch: a result treated as evidence for the claim
adjacent to the one it supports.

**The durable rule, which is why this is a milestone and not a note:** an
instrument's results must not be stored where its future subjects read. It is the
same failure as the coaching leak recorded above — a summary reaching the subject
through the guidance channel rather than the export, where no preflight can see
it — arriving this time through a repair written in good faith.

**Run 3 (2026-08-06) — VOID, and it falsified the repair's premise.** Dispatched
to the GENERAL-PURPOSE target. Its context audit reported, before it read a line:
the full text of `CLAUDE.md`; the user memory index, naming ten issues and
carrying an explicit verdict that a `CLAUDE.md` claim was stale; and a git digest
naming the three most recently touched issues. So two of the three injections the
repair claimed to have removed were still present, and the third had returned in
a different costume. **The premise "a dispatched subagent has no hook digest" was
simply false for that target.**

**Run 4 (2026-08-06) — CONTEXT-CLEAN, and it located the actual variable.** Not
"valid" without qualification, because there are TWO contamination channels and
this run was only clean on one. The CONTEXT channel — what the reader is handed
before opening the file — was clean, which is what run 3 failed. The GUIDANCE
channel — prior runs' conclusions surviving as paraphrases inside `CLAUDE.md`
itself — was still leaking, which is what voided run 2.

**What that supports, stated narrowly — and NOT "its findings stand", which was
the first draft here.** A guidance leak can prime a reader toward an ANALOGOUS
defect without recording the finding itself, so the absence of the exact text
does not restore independence. What the run's findings rest on instead is
whether each was independently CONFIRMED:

    confirmed by measurement in-session    stands on the measurement, not on
    (the 110/71 line counts, the 130-line   the reader's independence
    span, the read-truncation boundary,
    #65/#66 appearing only in the table)

    judgment-shaped ("the single most      PROVISIONAL until a guidance-clean
    misleading stretch", the competing-     run reproduces it
    candidate list)

Any AGREEMENT it expressed with an existing repair is worth nothing, for exactly
the reason run 2 was void. Calling the run simply "valid" would be this
project's most familiar error — a state reported as its stronger neighbour.

A probe first
established that a different dispatch target — the read-only search agent —
receives no `CLAUDE.md`, no memory index, no digest and no issue number anywhere.
Re-run against it, the context audit came back clean: no issue, no work item, no
queue, no handoff. **The contamination was never a property of subagents; it is a
property of the TARGET, and the instrument had never named one.** Step 1 now does.

The reader disclosed two leakage-adjacent facts unprompted rather than suppressing
them — the MCP tool names reveal the project's subsystem vocabulary, and the
current date dates the file against a `closed 2026-08-02` line. Neither names an
issue, so neither voids the run under step 1's own criterion. A witness that
volunteers its own contamination is behaving correctly.

**What run 4 found.** Finding the entry point was easy and it said so — the
startup sequence's self-declared uniqueness ("THERE IS ONLY ONE") is what let it
stop weighing candidates, and the parenthetical demoting `## PHASE 5` worked on
it specifically. Getting from the entry point to a TASK was hard, for five
reasons, of which two were structural and measurable:

- **The queue's top-down rule CONTRADICTED ITSELF in two sentences.** "Start from
  the highest non-empty bucket" and "if it is empty, nothing is forced" point
  different ways the moment the first bucket empties. The session followed the
  first and landed on #146 in bucket 2; the reader followed the second, and —
  reasoning that bucket 2's own gloss ("after more evidence has accumulated")
  argues against starting it and that every bucket-3 trigger reads unfired —
  landed on #151 in bucket 4.

  **This was NOT "two defensible picks under a working rule", and recording it
  that way was the first draft here.** The repaired rule resolves it in the
  reader's favour: a bucket-2 pick must justify overriding that bucket's stated
  reason to wait, and the session never had such a justification, while a
  bucket-4 pick needs none. So the divergence was a real defect in the text, the
  reader was right, and the session was wrong. Under the repaired rule two
  readers CAN legitimately choose different bucket-4 items — which is why step 3
  now compares against what the rule EXCLUDES rather than item-to-item.
- **The EMPTY bucket carried the most prose in the section** — 130 lines of
  window-closing retrospective about #145, #147 and #149, every paragraph bolded
  and urgent, with the sentence that resolves it ("the bucket is empty") at the
  END. Measured against the 8-line table, prose volume ran inversely to
  actionability. The reader's phrasing: the single most misleading stretch in the
  file.
- The `SKIM RULE` asserted closed-issue write-ups "are not the majority". Counted
  over the 239 lines following the table: 110 retrospective against 71 open-issue
  guidance. **False** — and a duplicated fact about the file's own composition,
  which is the same defect class as a duplicated issue status.
- `## Roadmap / backlog` held a second enumeration of issue status outside the
  bucket table, which the file's own rule forbids, plus a paragraph narrating a
  list that had already been deleted.
- A default file read stops at line 948, so a reader answering from the first page
  never reaches `## Working in this repo`, the oracle-mode conformance
  instruction, or the note that a bare `conformance.sh` is a nine-hour job.

The first four were repaired in the same session by editing `CLAUDE.md`. **The
fifth was NOT repaired and cannot be from inside the file** — the truncation
point is a harness property. It was only MITIGATED, by the file getting shorter:
the sections a first-page reader misses moved up, but they still sit past the
boundary. Recording it as repaired would be the same overstatement these runs
exist to catch.

**The rule this run adds, and it generalises past this instrument:** *a subject is
not clean because it is fresh — only because nothing was injected into it, and
what gets injected is a property of the harness rather than of the dispatch.*
Run 3's own audit was the only thing that could have caught it; a run that skips
question 1 and reads straight to the verdict cannot tell the two apart, because a
contaminated reader produces a report that looks exactly like a clean one.

## The queue entries retired from CLAUDE.md — #145, #147, #149, #133, #121

These sat in the queue's `more EXPENSIVE if delayed` bucket long after that
bucket emptied, in the urgent register live work is written in. Their durable
lessons were hoisted to rules in `CLAUDE.md`; the stories are here.

**#145 — the playground served stale hashes.** The window was stated precisely
when it was admitted: not that the drift grows, but that it **accrued per
visitor and did not stop**, `/try` being the front door for the audience most
likely to test that the hash IS the identity. Serving the right hashes
discharged exactly that (c7a18be); behavioural conformance for the served kernel
followed (df442f3). It closed by SPLITTING rather than by finishing — the same
move #130 made in the same session. What was left, artifact freshness, became
#148, which is DEPLOYMENT work with no per-visitor clock and so did not inherit
the bucket.

Its falsifier LOST rather than went unused: #145 asked whether `/try`'s corpus
might be a deliberate PIN, in which case the repair was a label and not a
regeneration. The answer was already in the tree — `playground-assets` says
"Regenerate after any kernel or corpus change" — so the question was settled by
reading, before any work started.

**#147 — a bound derived for the wrong environment.** `maxEvalDepth` was one
constant derived as a MEMORY budget: correct natively, meaningless on wasm, where
the binding constraint is the JS host stack Go's runtime borrows. It is now
per-target (`eval_depth_native.go`, `eval_depth_wasm.go`), with the wasm value
deliberately far beneath the lowest measured ceiling because a browser tab's is
not knowable from node. The general form — a bound derived for one deployment
environment, silently inherited by a second, with the justifying comment still
reading correctly — is now a rule in `CLAUDE.md`.

**Its close was PREMATURE, which is why #149 followed.** The depth guard fires on
the EVALUATOR path; the same crash was reachable through the unguarded PARSER.
The claim was not discharged — it moved to the door still open. Reading "#147
shipped" as "this class is closed" is exactly what the witness discipline warns
about: a regression witness proves the repaired door, not the class.

**#149 — admitted to the expensive bucket by ANALOGY, and measurement took it
back out.** The admitting argument was exposure: the playground serves this
kernel to every visitor, so delay lengthens the window. That inherited #147's
failure mode by analogy, and the analogy is false. Measured: native survives
500,000 levels of nesting, wasm throws at ~20,000, and the kernel is healthy
after five consecutive overflows. Nothing persists, so there is no per-visitor
clock. An item admitted by inheriting a neighbour's failure mode never has a
clock of its own established, and nothing prompts anyone to establish one later.

What survived the correction is a different defect and a real one: **the parser
could terminate outside Oath's error channel.** The contract is that malformed or
excessive input becomes an Oath error; a host exception crossing the boundary is
the host leaking through the abstraction, whatever it is called. Stating it as
"throws a JS RangeError" would name TODAY'S WITNESS — the claim has to survive
Wasmtime, WASI, or an embedder whose exception is named something else or
nothing at all.

Note which question produced that correction, because "is the parser recursive?"
would not have: it was *what happens to the visitor AFTER the overflow?* The
first asks about the code, the second about the claim.

**And the measurement that saved the last claim.** The quadratic in `find
--equiv` was REPORTED as a consequence of copying the binder context per
primitive. That mechanism was real and was fixed — and measuring showed it was
not the dominant cost, because the recursive oracle allocated the same (1243 MB
against 1224 MB). Repairing it as reported would have left a false claim
standing: that the gigabytes were an artifact of the rewrite rather than a
property the algorithm always had. Hence the rule: confirm a reported finding by
measurement before repairing it.

**#133 — the ADMIT boundaries** (4337bf5, cd50e03). All six discharged, the last
measured rather than reasoned about: four distinct playground sources
content-addressed to ONE definition, `accepted`, on the deployed binary. What is
worth carrying forward is the shape of the miss, not the fix. `CLAUDE.md` said of
#133 *"what remains is not a task"* on the same day the issue recorded an open
one — and **step 2 of the startup sequence cannot catch that class**, because the
status never changed. Only reading the issue against the paragraph does. That is
the concrete reason the pointer discipline exists: a sentence summarising an
issue's REMAINING WORK decays silently, while the issue does not.

What remained beyond it is the category move #69 owns, and the reason the
invariant cannot live in identity today: `Str` has no identity of its own —
`(data IntStack [] (Empty) (Push Int IntStack))` hashes to exactly `Str`'s hash,
so a rule attached to `SCons` would give Unicode semantics to an integer stack.

**#121 — three instruments, three blind spots, one seven-line definition**
(604c546). The broken decoder carrying the CORRECT general property scored
`tested · passed 200 cases`: the generator could not reach the input that
falsifies it. Proof caught what testing could not, and mutation scoring still
needed literal witnesses afterwards.

## #130 and #144 — the two repairs that were measured and NOT made

Retired from `CLAUDE.md`'s #146 entry, which now carries only the constraints
they impose on future work.

**#130 proposed two instruments; neither was built.** Both were measured as
MISFIRING on the issue's own flagship instance, and the work that emerged
instead — survivor adjudication, `oath mutate --prove` — shipped. The leftover
question (how often reach and exclusion actually diverge) became #146 rather
than staying under #130's title, because keeping it there would have left title,
motivation and remaining work describing three different things. That is the
origin of the rule now in `CLAUDE.md`: when an issue's remaining question is no
longer the one it was opened to answer, close it or split it.

**#144 established that the OBVIOUS repair is the wrong one.** Widening
generation moved the corpus by **+2 of 1203** and made three definitions WORSE.
Reaching the guard is not the fix — the surviving mutants sit in branches no
property observes at all, so a better draw REALLOCATES a fixed case budget
rather than adding to it. This is why #146 is framed as a measurement question
and not as generator work.

## #146 — reach vs exclusion: the falsifier won, 1 of 106

**The question.** A mutation survivor means generated executions did not
distinguish the mutant. That is compatible with two different situations —
REACH (a proven property excludes it; the campaign never drew the distinguishing
input) and an EXCLUSION GAP (no proven property distinguishes it at all).
Existence was already settled by `hex-nibble`. Prevalence was open.

**The result, over all 106 proven objects a live name reaches in `codebase/`,
carrying 209 survivors:**

    proof-refuted                    11    all on hex-nibble
    every proven property holds     168
    no verdict                       21    residue — see below
    mutant not provably total         8
    waived equivalent                 1

    definitions with >=1 proof-refuted survivor      1 of 106
    of the 93 HIGHER-SCORING proven definitions      0

**This is the outcome the issue nominated in advance as complete**, arguing to
leave `--prove` opt-in, cheap when unused, and justified by expressiveness
rather than frequency. The outcome that would have changed the default reporting
— a material fraction of the higher-scoring proven definitions also carrying
proof-refuted survivors — is zero. A registered falsifier winning is the point
of registering it.

**The claim is bounded by its population and stops there:** *in this corpus,
reach/exclusion divergence is a property of one definition.* Not "it is rare".
`examples/` is the exhibit set this project chose, weighted toward provable
arithmetic and structural recursion.

**The residue is irreducible, not budget-limited.** 21 survivors across five
definitions reached no verdict, and every one was re-run at the full 400M budget
and returned no verdict again — so the capped sweep found everything an uncapped
one would have. Escalating ONLY the no-verdict set is sound rather than
convenient: "still holds" means each proven property was PROVEN (`unsat`) on the
mutant, and no budget turns `unsat` into `sat`. Only the `unknown`s could move.

**The bounded mode that made it a measurement.** A per-attempt cap on the SMT
context, defaulting to 1/1000 of the proof budget:

    hex-nibble verdicts     identical at 400K, 1M, 4M and the full 400M
    greet-or-guest          2031 s -> 3.28 s
    the whole 106-def sweep 30 s

Sound at any cap because both terminal verdicts require a positive solver answer
(`sat` / `unsat`) while exhaustion yields `unknown` — so a cap trades COVERAGE
for time and can never invert a verdict. Which is exactly why the residue is now
its own reported line: it is the only disposition a larger budget could move,
and merging it with the settled ones would report the instrument's reach as the
specification's silence.

**Two instrument defects, both found during the run, both flattering.** The
population file lacked a trailing newline, so `while read` dropped its last
entry — and the completion assertion derived its expected count with `wc -l`,
which undercounts identically. The check agreed with itself and certified
COMPLETE over 105 of 106. It is the same shape as the `git diff` / `git diff
HEAD` note in `CLAUDE.md`: **an instrument guarding a hazard can share the
hazard's blind spot.** Second, the sweep first recovered dispositions by
matching prose; they are now machine-readable, because prose-matching had
already mis-scored this very measurement once.
