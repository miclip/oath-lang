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

> **THAT LIST IS A SNAPSHOT OF THIS MILESTONE, NOT THE LIVE BOUNDARY.** Arithmetic
> was lifted by #166 and dynamic `Str` construction by #164; `Rat`, `Float`,
> `Set`/`Map`, the handler protocol and `neg` remain refused. The sentence above
> is left as written because it records what this milestone shipped — but a
> reader meeting it alone would take it as current, which is the exact way this
> project's prose rots: **a paragraph's class changes without its text changing.**
> `oath/llvm.go` is the authority on what is refused today.

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
namespaces, cryptographic ownership, discovery, `explain`. The AUTHORITY chain
for a second principal is now integration-tested end to end
(`TestNonOperatorKeyReservesPublishesLicensedAndIsThirdPartyVerifiable`): a key
never named in any policy reserves a namespace, publishes an Apache-2.0 artifact
into it, a different self-generated key is refused at the AUTHORITY gate (citing
the namespace, not an authentication failure), and a third party DISCOVERS the
artifact by its law, resolves it to the exact published bytes, verifies the
journal, and reads the Apache-2.0 grants — from the store alone.

Two boundaries that test does NOT cross, each covered elsewhere, stated so the
claim is not overread:
- the HOSTED auth boundary (`authenticatePrincipal`, the `--authorized-keys`
  allowlist) — the "no operator action" half rests on that allowlist being
  absent, which is a deployment fact and is tested by the gate's own tests, not
  re-proven here (the test exercises the core acceptance path directly);
- the SOCIAL model — a real person doing this unaided. That walkthrough belongs
  to the first external contributor and is deliberately not a scheduled step: a
  mechanism test proves the paths work, not that a stranger can find them, and
  simulating one would manufacture exactly the reassurance the real thing exists
  to withhold.

**#66 IS NOT THE GATE — verify the deployment, not this file.** The live registry
runs NO `--authorized-keys` allowlist (checked 2026-08-01 against the Cloud Run
service: no such arg, and only `OATH_STORE`, `OATH_STORE_LOCK`,
`OATH_TOKENS_FILE` in env). `authenticatePrincipal` computes `canWrite := authKeys
== nil || authKeys[pubHex]`, so **any key that signs can already write.** An
earlier version of this claimed the registry had exactly one authorized key and
that #66 blocked the milestone; that was never the deployed configuration.

The authority chain is demonstrated in-process by the test named above, and the
live registry runs no allowlist, so the finish line is reachable TODAY with no
operator action. The METHOD against the live
registry, recorded so nobody re-derives it: a fresh key, `reserve`, a fresh key,
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

## The startup read's calibration — the two wrong turns before "claim types, not routes"

The rule in `CLAUDE.md` says independence is a property of the CLAIM, not of the
activity that produced the evidence. It arrived by making the opposite mistake
twice, and both are worth recording because each looked like the obvious repair.

**First: crediting a second READER.** The initial rule offered re-dispatch as
independent confirmation. But a second reader shares the first's model, priors
and the same leaked file, so two correlated subjects are one measurement taken
twice — it establishes REPRODUCIBILITY, not independence.

**Second: crediting INSPECTION.** The repair then said the artefact outranks any
reader of it, which is true for a DECIDABLE claim — two sentences that contradict
each other are checkable by quoting them, and priming cannot make them agree. It
is false for a JUDGMENT about presentation, where a primed inspector reaches a
primed reading just as easily. The route was being credited when the claim's type
was doing the work.

Both were caught by review, not by re-reading. The session had already been
obeying the correct rule without naming it: the queue-rule contradiction was
settled by quoting both sentences, while "the most misleading stretch in the
file" only became usable once the 130-line span was counted — the judgment was
never the evidence, the count was.

**And the channel taxonomy was wrong until the last pass.** The calibration
described TWO contamination channels (context, guidance) and then reasoned about
MODEL correlation as though it were one of them. They are three, they close by
different means, and only the first is closed today. Writing the third row's
consequence exposed the sharpest fact about the leak: it lives INSIDE the
artefact under test, so no reader closes it — which is what makes it a debt to
retire rather than a calibration to live with.

## #80 — the prefilter that works is not a structural one

Closed on a mechanism the issue did not name, after its own recommendation was
built and measured unsound. **367.882s → 0.054s** (`ff137c1`).

**The issue proposed narrowing candidates "by signature and property SHAPE"
before invoking Z3.** Both shape filters were implemented and gated by a
soundness test:

    states-the-law     drops ALL FOUR provers
    commutative-head   drops max2 — body `(if (< a b) b a)`, provably
                       commutative anyway

`find --implies` exists precisely to see THROUGH shape — it is the route to
semantic matches that content-hash matching misses. **Filtering on shape
reinstates the miss the prover was called to fix.** That is not a bug in the two
filters; it is what the category buys.

The sound STRUCTURAL filters barely helped either. Of the two candidates
carrying 99.98% of the runtime (`e-div` 229.6s, `pow` 138.2s), `total-only`
eliminated neither and `non-recursive` eliminated one — 1.00x and 1.60x.

**What worked is not structural at all.** A concrete pass evaluates the goal
under generated environments and skips the solver only when one makes it FALSE.
Evaluation is the reference semantics, so a goal false under some concrete
environment IS false and no valid proof exists — the skip and the proof would
contradict each other. **The argument is about the GOAL, not about any corpus**,
which is what separates it from a heuristic tuned to `examples/`.

**And the guard is the half that keeps it honest.** Generation failure,
evaluator errors, fuel exhaustion and a non-Bool result are all INDETERMINATE
and defer to the solver: *the evaluator could not finish* is not *the property
is false*. A non-terminating candidate makes that difference visible, and a
control asserts it — because a prefilter that rejects MORE looks better, and
that is the signature of a fabricating one.

**The measurement also exposed a defect older than the work** (#156): the tool
prints only `proven`, so refuted-with-a-countermodel, the-solver-declined and
the-wall-cap-fired all render as "no definition provably satisfies this". Asking
what a bounded search would owe its reader is what surfaced it — the residue
obligation was already unmet, before any bound existed.

**A methodological failure of mine is recorded with it**, because the correction
came from the implementer rather than from me. I measured the package at 413s
and reported a 12x CI regression as fact. The tree had been left mid-mutation by
a budget-terminated run: 365s of that is what the suite costs with this feature
DISABLED, which the mutation control later quantified exactly. The package was
~38s before and ~37s after. The follow-up work was still worth doing, for a
different reason than the one I gave it — a correct repair attached to a wrong
diagnosis, which this file already warns is indistinguishable from a fix until
someone re-measures.

## The closing-verb parser: twice more, and the second by the commit documenting the first

`CLAUDE.md` has carried a rule about GitHub's lexical closing-verb parser since a
commit containing "would close #145 falsely" — inside a sentence arguing against
closing it — closed #145. On 2026-08-10 it fired twice more, and the shape of the
recurrence is worth keeping even though the rule itself did not change.

**The first was ordinary.** The #68 verdict commit's body ended with "This
section does not close #68 and asserts nothing about its status" — a sentence
whose entire purpose was to deny the action it performed. The goal that produced
that commit said explicitly to keep closing verbs out of the body.

**The second was the commit written to document the first.** Its title was *"The
sentence saying it did not close #68 closed #68"*, which carries the pattern
twice, and it closed #68 again. The rule was known, quoted, and being obeyed in
spirit; what failed was the assumption that a NEGATED or QUOTED verb is inert.

**A third cost, from the same two closes, was separate and easy to miss.** A parser close records
`stateReason: COMPLETED`. #68's verdict was DECLINE, so the metadata said the
opposite of the decision, and `gh issue close --reason "not planned"` requires the
issue to be OPEN — correcting it meant reopening and re-closing. The state was
right and the reason was wrong, which is the kind of defect nobody looks for.

Both durable rules are in `CLAUDE.md`; this is the record of what earned them.
The general form has appeared elsewhere in this project under other names: a rule
can be understood, endorsed, and violated in the same act of explaining it,
because the explanation is written in the vocabulary the rule governs.

## #158 — a stated compiler milestone, retired by its own falsifier before any work

**The admission.** `CLAUDE.md` requires a NEW CONSUMER *or a separately stated
compiler milestone* before the LLVM backend broadens, so that it cannot widen on
momentum. #158 was filed deliberately under the second clause, with an argument
for why the first could not resolve itself here: `oath/llvm.go` refuses
arithmetic, `Rat`, `Float`, `Set`/`Map`, dynamic `Str` construction and the
handler protocol, so it compiled a subset **no real program lived inside** — and
a backend nobody can use cannot attract the consumer whose demand would justify
lifting the refusal. Held indefinitely, a guard against momentum becomes
paralysis.

**The falsifier the issue wrote against itself.** *Enumerate what `llvm.go`
accepts today, and try to write a useful program inside it. If one exists, close
this and file what it revealed instead.* Cheap, and it could win — which is why
it was placed before any building.

**It won in one attempt.** `docs/experiments/issue-158-llvm-subset/show-from-marker.oath`
is a file viewer that prints a document from the first occurrence of a marker
onward. It compiles through `oath build --backend llvm`, runs natively on real
files, and agrees with `oath eval` and with the Go backend. **No line of
`oath/llvm.go` changed.** The acceptance gate runs the real CLI into a scratch
store, uses `oath eval` as the reference for both backends, asserts a control
that must fire, and keeps a fail-closed control one arithmetic operation outside
the subset that LLVM must still refuse by name; it runs from
`TestLLVMSubsetAcceptanceScript` rather than sitting in a directory.

**What the attempt revealed, which is the part worth keeping.** The binding
constraint is not `Rat` and not arithmetic — it is **dynamic `Str` construction**.
A program in the subset can COMPARE and SEARCH runtime strings but cannot BUILD
one, so every result it produces is a literal or a suffix of an input. Filed as
#164 with code-derived evidence; the queue's trigger for it reads *nothing can be
reported that cannot be quoted*.

**Two things the close deliberately does not claim.** `docs/tutorial/circle.md`
— #158's own chosen target — still does not compile; what was refuted is *no
useful program fits*, not *circle fits*. And it is one program: the subset is
shown non-empty and useful, not measured against what people want.

**Closed NOT_PLANNED, not COMPLETED.** The milestone's work was never done; its
premise was withdrawn. The distinction matters because a parser close records
`COMPLETED`, and a milestone reading as achieved would misrepresent the backend's
reach to every later session.

**The durable lesson is hoisted into `CLAUDE.md`** under *an implementation limit
reported as a semantic fact*, one level up from where that rule previously lived:
the premise ADMITTING a work item is a claim too. *Refuses arithmetic* is a fact
about the tool; *no real program lives inside* is a claim about programs, and it
was inferred by reading a REFUSAL LIST — which enumerates what is excluded and
therefore cannot measure what is admitted. The complement of a list you can read
is not a set you have looked at.

## #166 — the falsifier that lost, and the defect it found in the backend nobody was changing

**The forced item.** The LLVM backend stored `Int` as a `long long` and refused
any literal outside it. That was an honest subset — it refused rather than
wrapped, and said so — but `Int` is ℤ *because the prover's `Int` is unbounded*,
so a machine-width `Int` would put overflow reasoning into every arithmetic
proof in the corpus. That makes the width a SEMANTIC commitment rather than a
representation a backend chooses, and it is the distinction the queue's
structural-model rule turns on.

**The cheap falsifier ran first and did not fire.** A *checked* int64 — trapping
on overflow with a named diagnostic — is fail-closed rather than wrapping, so if
nothing the backend accepts could distinguish it from ℤ, the trap was the honest
design. It was built, not argued about, and pointed at the boundary. Three
things made the negative result decisive:

- the distinguishing property is **PROVEN**, so the artifact was not disagreeing
  with another implementation — it was failing to exhibit a property the kernel
  proved of the definition it was compiled from;
- a second entry took its operand from `argv`, so **one binary took both sides
  of the boundary from its input**: no argument completed, and any NON-EMPTY
  argument refused. The qualifier is not pedantry — the entry matches `SNil`
  first and returns `"empty-argument"` before reaching the addition, so an empty
  argument completes too, and "any argument refuses" was false as first written.
  What the witness needs is only that ONE runtime-supplied value crosses the
  boundary and another does not, which it does. No build-time analysis
  reproduces that, and it closes the objection that a compiler could simply
  refuse the overflowing case statically;
- the old `int-range` check meant the subset **could not name the right answer**
  — `9223372036854775808` was itself a refused literal — so ℤ's result was
  observable only as "none of the above".

Scope, stated because it is easy to overread: this establishes that the subset
ADMITS a distinguishing program. It is not a claim that programs anyone wants
overflow. Different populations, and only the first was measured.

**The representation.** Sign-magnitude, base 2³², least-significant limb first,
written into the emitted C runtime; 32-bit limbs so every intermediate fits a
64-bit unsigned, avoiding compiler-specific 128-bit types as well as any
library. Literals cross as **decimal digits rather than limbs** — the digits are
the canonical form the AST already holds, so nothing has to agree with the
runtime about limb order or width. That is what retired `int-range`.

**The finding worth more than the feature.** Holding all three paths to *naming*
the by-zero condition, rather than merely to failing, exposed a defect in the Go
backend, which nothing in this work was changing. `big.Int.Quo` and
`big.Int.Rem` BOTH panic with `division by zero`, so a compiled artifact
reported a division fault for a modulo the program never performed, while the
interpreter distinguished the two. It had been wrong since the Go backend gained
arithmetic, and the comment above the emission asserted precisely the
correspondence that did not hold.

> **A DIFFERENTIAL GATE THAT COMPARES ONLY SUCCESS-PATH VALUES IS BLIND TO
> EVERY DIFFERENCE IN HOW THE PATHS FAIL.**

The first draft of this paragraph credited the wrong instrument — it said a
two-way gate could not have seen this because both sides inherited the error
from one host library. That is a true sentence about a different situation. It
is not what happened: `oath eval` reports `modulo by zero` itself and inherits
nothing, so eval-versus-Go was already a disagreement and TWO paths would have
been enough. What no number of paths supplies is the assertion. The gate
compared the values programs printed, and a program that dies prints nothing to
compare — so the divergence sat in a channel the gate never read.

Adding the third path is what happened; asserting the DIAGNOSTIC is what found
it. Recorded this way round because attributing a discovery to the more
impressive-sounding mechanism is a recurring error here — the same shape as
crediting the prover for a defect a single evaluation exposed.

**Two assertions replaced under #156's rule, each pinning more than before.**
The old refusal test asserted one bit, and "refused" is satisfied by a backend
that refuses everything; the replacement pins the VALUE — the digits must reach
the emitted IR, and a sum leaving int64 must equal the exact answer, excluding
wrapping (int64 min) and saturating (int64 max) by naming the value that is
neither. And the acceptance runner's floor of 20 checks permitted deleting the
entire arithmetic section while the script ran 67: **a count is a proxy for
coverage**, so the families are now asserted by LABEL, which is what owns the
claim.

**Verifying that second one took three attempts, and the two failures are the
general lesson.** The first mutation broke the script, so the test failed at its
did-not-reach-a-verdict check rather than at the new assertion — a probe asking
"did it fail?" cannot tell a working gate from a crashing one. The second was
served from Go's **test cache** and measured nothing at all, which looks
identical to a pass. It fires correctly under `-count=1` with a mutation that
leaves the harness valid.

**The fuzzer's own instrument was wrong first.** It passed a deliberately
injected carry bug 150 cases at a time, because two independently drawn operands
almost never sum across a 32-bit limb boundary — a random *k*-bit operand has a
top limb that is not full. The one defect class the fuzzer existed to catch was
the one its generator could not reach.

**Provenance, recorded because it shaped the outcome.** The work began under a
two-agent session whose transcript body was lost mid-run to a known front-end
defect; the session was aborted rather than continued into a live turn, and the
remainder finished directly. What was in the tree was sound — the bignum runtime
and its evidence. What the interrupted run had not yet reached was everything
describing the OLD boundary: a test table still listing `/` and `%` as refused, a
fail-closed control built on `/` after `/` had been lowered, and three prose
descriptions. All were found by review. **An interrupted run leaves the artefacts
that describe its own starting state, and those are the ones nothing fails on.**

Landed `c6b5dd9`. Retired from the forced bucket on landing, without waiting for
its neighbour — which is what per-entry retirement conditions are for.

## #119 — the entry's interface becomes one variant, and exhaustiveness becomes a type error

**The defect was a defect CLASS, which is why the fix is an API and not two
lines.** `if len(prog.Requirements) > 0` decided whether to build and apply the
capability record. But an entry typed `(-> {} (-> (List Str) Str))` requires no
authority and STILL takes the record, because arity comes from its type — so the
count passed argv where the record belonged. The Go backend printed a closure;
the LLVM backend did the same thing independently, hours apart, in a file that
shares no code with the first. **A bug that reproduces itself in a fresh file is
an API problem, not an attention problem.**

Two questions were being answered by one field: *"requires nothing"* is about
AUTHORITY, *"takes no argument"* is about SHAPE. Nothing stopped a backend author
from reaching for the more obvious-looking one.

**`EntryShape` names the four COMBINATIONS** — cli, cli-with-capabilities,
handler, handler-with-capabilities — rather than carrying a protocol plus a flag,
because a protocol-and-a-boolean is still two things a consumer can read one of.
`CapTy` is gone from `CompiledProgram` entirely, which is what makes "no arity
inference" structural rather than a convention: the record type is still derived
during planning for `entryRequirements`, it is simply never carried where a
backend could reach it. `len(Requirements)` survives only where it sizes provider
arrays and prints the authority list — the question the list actually answers.

**EXHAUSTIVENESS IS A COMPILE ERROR, NOT A TEST**, which Go does not give you for
free:

    type entryShapeTable = [numEntryShapes]struct{}
    var _ entryShapeTable = [len(goEntries)]struct{}{}

Two array types are identical only when their lengths are, so a missing or
surplus row is a type error at the declaration, in the file that owns the
decision. `numEntryShapes` is a terminal sentinel in the iota block, so the
variant's size is DERIVED rather than written down.

**THE FIRST ATTEMPT WAS WEAKER AND LOOKED FINISHED, WHICH IS THE PART WORTH
KEEPING.** It used maps plus a hand-written `entryShapes()` list and checked
totality at every lookup. That is a real check — but the LIST is itself a place a
new constant can be omitted, and omitting it there leaves every "does this table
decide every shape?" check passing while measuring a universe smaller than the
variant. **The authority for the size has to be something the compiler derives.**
The issue is about inferring a fact instead of consuming it, and the first repair
reproduced that one level up.

**A LENGTH CHECK IS NOT A COVERAGE CHECK**, stated rather than papered over: a
table can reach the right length with a MIDDLE index left at its zero value, and
for one backend the zero value is a legitimate row — exactly the plain CLI
answer, so it cannot be rejected on sight. Each row therefore names the shape it
decides, checked against its index at run time. The compile-time half catches an
omitted shape; the run-time half catches a hole. **Neither subsumes the other.**

**A REFUSAL IS A CASE.** The LLVM backend covers a subset, and the shapes it
declines are rows in the same table rather than an early `if` that has to
remember. The row holds a constructor rather than a stored `refusalReason`,
because the refusal vocabulary is closed by tracing assignments to declared
constants and a reason carried through a struct field is not traceable —
correctly, since a computed reason is not vocabulary.

**Also corrected: a comment claiming the manifest's reader recovers arity from
`requirements`.** False, and the empty capability record is the counterexample —
the same presence-versus-contents confusion this variant exists to remove,
reappearing one layer up in the record that describes the build.

**The test set covers both directions, which is more than exhaustiveness.**
Totality alone is satisfiable by a variant carrying cases nothing can produce —
a green check over a universe partly made of fiction. `TestClassifyEntryInhabits
EveryShape` closes that from the other side: every named shape is reachable from
a real entry type, so the variant is neither under- nor over-specified.

Landed `ebff84e`.

## #167 — the fix that would have made a handler killable from the internet

**The asymmetry.** A host-side refusal in a go-emit artifact panicked: status 2
plus a goroutine dump naming the temp directory the Go source was emitted into.
The same condition in an llvm-ir artifact wrote one stderr line and exited 70.
One source definition, two answers to a supervisor reading the status. The
falsifier — *maybe exit status is not part of the contract* — lost on evidence:
70 is asserted in four test files and documented in `docs/effects.md` and
`README.md`.

**THE OBVIOUS FIX WAS WRONG, AND WORSE THAN THE DEFECT.** Writing the line and
`os.Exit(70)` at the detection site is what landed first. A handler is a
long-lived server whose input a remote party chooses, and a well-typed handler
can reach a refusal from that input — **dividing by a body byte that arrived as
zero is enough.** Exiting at the raise site hands that party the process,
against SPEC 14.2's rule that a remote party must never be able to end a host.
net/http had been recovering the panic per connection, so the process survived;
the "improvement" would have introduced a remote process-kill.

**So the CONDITION is a fact about the language and the DISPOSITION is a fact
about the boundary, and only the boundary knows it.** `oathRefuse` raises an
`oathRefusal` value; each boundary disposes of one — standalone `main` exits 70,
the handler adapter answers 500 and keeps serving. The boundary owns the
transformation, not the place that detected the condition, which is the pattern
this project keeps arriving at from unrelated directions.

**The classification, stated rather than implied.** A REFUSAL is reachable by a
well-typed program: a codepoint outside the scalars, a zero divisor, octets that
are not text. A PANIC is unreachable unless the compiler is wrong. Applying the
split found a merge that described a broken representation as *"out of range
0..255"* — an implementation defect reported as a semantic fact, which is this
file's own named failure mode. `oathRefusalOf` is the single place the two are
told apart, and it re-raises what is not a refusal, because `recover()` cannot be
selective.

**The witness gap, and why a structural assertion was not enough.** The
handler's re-raise was covered only by *"both boundaries call one classifier"* —
which establishes that the code is SHAPED correctly, not that a running artifact
BEHAVES correctly. The behavioural witness pins both directions: a clean response
means a compiler defect was swallowed, a dead process means SPEC 14.2 is
violated. Verified by removing the re-raise.

**And six unguarded skips would have hidden it.** Four `go toolchain` and two
filename skips meant an absent toolchain or an unaccommodating filesystem would
remove these witnesses in CI while the run stayed green — the failure
`requireClang` names twenty lines above in the same package, reproduced by tests
written after it.

**FOUR REVIEW FINDINGS ON ONE TEST, ALL THE SAME SHAPE.** The assertion was for a
line's ABSENCE, and every defect made absence easier to observe than presence: a
`signal 0` liveness probe that fails on Windows against a healthy server; a
stderr condition that passed when both substrings appeared; a bare
`bytes.Buffer` read while `os/exec` was writing it; and `cmd.Process.Wait`, which
waits for the process and leaves the copy goroutine running, where `cmd.Wait`
joins the copiers.

> **A WITNESS FOR SOMETHING NOT HAPPENING HAS TO PROVE IT WAS IN A POSITION TO
> SEE IT.**

Landed `43f457c`, witnesses completed `3c80992`.

## #168 — the compiler stops being a coin flip, and the cause the issue named was half of it

Both backends walked the dependency closure with `for dep := range
collectDepsBody(d)`, each into its own `order` slice, so definitions with no
edge between them came out in whatever order Go's map iterator chose. The
generated program was a permutation of itself run to run. Measured on the
committed corpus before the repair, over 30 emissions each: `gh-webhook` 29/29
differed from the first, `main-echo` 25/29, `circle` 13/29, `main-fetch` 0/29
(its closure is one definition, so nothing could permute). After: 0/29 for all
four, and the emitted source is a PURE PERMUTATION of what it was — same length,
same multiset of lines, so nothing was added or dropped by the change.

**The consolidation, not two sorts.** `emissionOrder` in `program.go` is now the
only dependency-ordering walk; both backends call it and neither retains any
closure code. The order is deterministic because each definition's dependencies
are visited in HASH order — canonical identity, so the order is a property of
what is being compiled.

**A backend's native lowerings are PRUNED, not filtered.** The Go backend lowers
recognised `Set`/`Map` operations at their call sites, and the sorted-list
helpers those operations are defined in terms of are reachable only through them.
Filtering a single fixed order would have emitted those helpers as dead code —
or, had the filter also dropped their names, left the emitted program calling
functions nobody wrote. So the native set is an ARGUMENT to the shared walk. The
emitted definition set is byte-for-byte the same as before the change, checked
on all four corpus entries.

**THE ISSUE NAMED THE CLOSURE WALK. THE CLAIM WAS "EMISSION IS A FUNCTION OF ITS
INPUTS", AND A SECOND CAUSE SAT OUTSIDE THE NAMED DECOMPOSITION.** Both emitted
function names are built from `Store.NameOf`, which returned the first name Go's
map iterator produced for a hash. A hash may carry SEVERAL names — structurally
identical declarations are one object, and `Interval`/`Run` and `rot`/`rot-f`
alias in this corpus today — so an aliased definition anywhere in a program's
closure moved the generated source independently of any ordering. It now returns
the least name. Nothing in the four corpus closures is aliased, which is exactly
why fixing only what the issue named would have looked complete: the corpus
cannot currently witness the second cause, and the test fixture was built to.

That is `derive the universe from the CLAIM, not from the implementation's
decomposition` arriving again — the walk was the enumerated proxy, and
`NameOf` is one layer out from it.

**The control had to be structural.** The first version asserted that the
pre-#168 walk produced ≥2 distinct orders over 100 draws, which asserts that Go
RANDOMISES map iteration — unspecified, not promised to vary. It now asserts that
the fixture contains two EMITTED definitions that are dependencies of a common
parent and of neither each other, so its order is genuinely free; the observed
variety is logged, not claimed. Restricting that count to emitted definitions was
not cosmetic: counting datatype dependencies made it pass over a fixture whose
order was forced, since datatypes are erased and pairwise independent.

**And the fixture's own first draft was refuted by content addressing.** Four
identity functions on `Str` are ONE object under four names, so the closure held
a single sibling and there was nothing to permute. The control caught it.

N=100 repetitions is worth stating against the measured rates rather than
asserted: the pre-repair Go split of 27/3 recorded in
`docs/experiments/issue-116-reproducibility` implies a majority rate p ≈ 0.9, so
a still-broken emitter passes 100 repetitions with probability ≈ 3e-5, and the
LLVM split of 25/5 gives ≈ 1e-8. Those rates were measured on the experiment's
program, not on the test fixture; the structural control is what establishes that
the fixture can vary at all.

**What this does NOT establish.** Artifact-level reproducibility of `oath build`
end to end: `-trimpath` and the LLVM basename cause are recorded in the same
experiment, independent, and deliberately out of scope. Cross-machine
reproducibility is untouched. And no corpus entry compiles under the LLVM
backend today — the LLVM half of the byte-identity witness runs on the fixture
only.

## The startup read, run against a dispatched reader — and the channel that leaked

The stateless read prescribed by `CLAUDE.md`'s step 1 was run against the
read-only search target. Its context audit came back clean on all four forbidden
categories: no work item or queue, no memory index, no commit digest or branch
state, no handoff summary. The target receives none of them, as the sequence
claims.

**And the run was contaminated anyway, through a channel the sequence does not
name.** The dispatch prompt paraphrased the file's own step 1 — including its
list of forbidden categories, nearly verbatim — so the reader recognised the
startup sequence rather than met it, and said so unprompted:

> when I reached that passage I recognised it rather than met it … my answer is
> **not** independent evidence that the startup sequence is what a cold reader
> finds first.

The file's calibration already named three — CONTEXT, closed by the dispatch
target; GUIDANCE, living inside the artefact and closable by no reader; and
MODEL, closable only by a reader that does not share these priors. The PROMPT is
a FOURTH: written fresh each run by the party who already knows what the file
means to say, invisible to any preflight, and invisible to question 1 as well,
since a leading prompt carries no work state. The rule that follows is in
`CLAUDE.md`; it is not restated here.

**What the run established, and it is the decidable class only.** Three defects
were confirmed by inspection and repaired: a bucket entry whose sentence ended on
a bare comma and ran into the next issue with no separator, so the boundary
between `#116`'s trigger and `#162`'s entry was unreadable; a list introduced as
"Three rules" carrying four bullets; and the first bucket's retirement rule
stated twice, eighty lines apart, each copy carrying something the other lacked
(the per-item retirement condition, and the measure-the-analogy requirement).
The two copies were merged into one and the second deleted. Those hold whatever
the reader was primed with, because the artefact settles them.

**What it did not establish.** The reader's account of what the file says to do
first is void for the reason above. Its structural observation — that the bucket
the rule sends an unforced session to carries eight bare issue numbers while the
buckets it tells you to justify overriding carry every elaboration in the
section — is a JUDGMENT about presentation, reduces to a count nobody has taken,
and stays provisional. So does its report that `## PHASE 5 — READ THIS BEFORE
PICKING WORK` competes for the first slot despite the parenthetical that defuses
it 250 lines earlier.

**One QUANTITATIVE observation** — settled by a measurement rather than by the
artefact, and nobody had taken it: the file did not fit a single read. The reader's first `Read`
returned 948 of 1896 lines against a token cap. The queue sits inside that first
page; the naming rules, the conformance-mode warning and the `codebase/` reset
hazards do not. A reader that stopped at the cap would have the work list and
none of the operational hazards.

### The second read, and the limit the instrument cannot escape

The read was re-run against the same target with a deliberately narrow prompt:
name the file, ask the three questions, nothing else. The forbidden-category
check moved out of the dispatch and onto the operator, where it cannot prime
anything.

**It leaked anyway, and the reason is structural.** The three questions are
SPECIFIED IN THE FILE. Any faithful dispatch quotes them, so the reader meets
its own task written down in the artefact and recognises step 1 rather than
finding it:

> when I reached lines 120-127 I **recognised** the startup sequence as the
> thing I was already executing rather than discovering it cold … Discount my
> answer to question 2 accordingly: it is evidence the sequence is findable, but
> weaker evidence than a clean run would give.

That calibration is right and is the disposition to keep: question 2 is WEAK
evidence by construction, not void. A reader still has to locate the sequence in
1,900 lines; recognising the questions does not say where the answer lives. But
no dispatch through this route can make it strong, and no rule about prompt
wording changes that.

**Question 3 is unaffected**, because its findings are decidable from the
artefact. Both runs converged on several independently — two readers, different
prompts, same defects — which clears "not one reader's say-so" without either
run's priming mattering, since the artefact settles them either way.

**What was repaired.** The file forbade recording a run's findings or their
provenance, and carried both: a paragraph narrating a falsifier firing and the
diagnosis that preceded it; a count attributed to "a primed reader"; three
sentences recording that an earlier draft of the surrounding prose had been
wrong. Each is the GUIDANCE channel the file names as closable by no reader,
self-inflicted. The rules were kept and the stories deleted.

**What was NOT repaired, and is left for a session with the appetite.** Both
readers reported, decidably: closed issues occupying top-level sections against
the file's own instruction; long parked-experiment descriptions where it demands
pointers; issues elaborated as live work that the bucket table does not hold;
and six of seven bucket-4 items appearing nowhere else in the file, so the bucket
an unforced session is steered to is the one the file describes least. These are
checkable and were not acted on here.

### The queue's descriptions, replaced by the pointers its own rule demands

Two readers converged on the queue's buckets being informationally lopsided:
the bucket an unforced session is steered to carries seven bare issue numbers,
while the buckets whose stated reason to wait must be overridden carry every
elaboration in the section. Both framed it as bucket 4 being under-described.

**The file's own rule says the repair runs the other way.** It prefers a POINTER
over a description "because a pointer cannot go stale in this direction", and
says a description "must be re-verified every time anything lands". By that rule
the bare numbers are correct and the elaborations are the defect. Removed:
sixteen lines of runbook for a parked experiment, a closed issue's precision
correction, and a status sentence claiming an issue was unanswered that had since
been closed.

**One deletion was wrong and review caught it.** The runbook was removed on the
strength of a keyword grep against the issue — `20a24cc`, `throwaway`, `frozen
codebase` all matched, so the content looked present. It was not: the issue named
the revisions without carrying the procedure, so the pointer would have led to a
trigger nobody could evaluate. The measurement confirmed that TOKENS appeared, not
that the PROCEDURE did. The procedure is now on the issue, with the two traps that
make the trigger unfireable if missed, and the pointer is honest.

**A tooling note that cost three separate mistakes in one session.** This file is
hard-wrapped near 78 columns, so any multi-word `grep` silently misses a phrase
that spans a line break. It reported no duplicate paragraph where one existed, and
no parent rule where one existed. Normalise first — `tr '\n' ' '` — before
concluding a phrase is absent.

## #181 — the run-stability fixpoint stops committing a non-fixpoint

SPEC §7.2 already required the recorded proven set to BE a fixpoint of F, and
both kernels violated it at the one place the requirement is hardest to keep:
when the round bound is reached while S is still changing. oathrs committed the
last (non-fixpoint) set SILENTLY; the Go reference committed it with a warning.
Either way, `outcomes.json` could carry a truncation indistinguishable
downstream — the cross-kernel oracle, the #139 fingerprint, the #98 sharded
verifier all trust S to be F(S).

Measured before touching anything, and the measurement set the fix's shape: the
committed corpus converges at ROUND 2 of the 8-round bound, and NO member of S is
present only via abort carry-forward. Latent-with-margin — so the response is to
fail LOUD on bound-exhaustion (a corpus that needs more rounds raises the bound),
not to quietly raise it. The measurement itself was only cheap because the round
structure is driven solely by the PROVING properties: a reduced z3 rlimit makes
the ~145 futile goals abort fast while every genuine proof (reached far below the
budget) survives, turning a nine-hour derivation into a two-minute one — verified
faithful by the proven set matching, and the budget-starved inductive proofs all
recovering at round 0 under a higher budget.

Both kernels now REFUSE instead of recording, and one clause of §7.2 makes the
only conformant option explicit. The durable lesson is NOT new: it is the
universe-from-the-claim rule again. #181 was filed against `oathrs/src/prove.rs`,
and an oathrs-only fix passed its own tests — but the CLAIM is a §7.2 rule whose
structural owners are BOTH kernels and the spec, so hardening one file created a
deterministic cross-kernel divergence. Review caught that, then two more defects
the first fix introduced reaching for the same too-narrow universe: the Go error
path left the non-fixpoint set in the store's CACHED `Meta` pointer (restored
now), and `scanBulkProve` swept the error under "scan complete" and recorded the
done-fingerprint (withheld now, so a failed scan retries). Three consecutive
repairs failing for the same reason — routing around a claim wider than the file
named — is the tell the file already documents.

The new normative sentence had a scoped blind round. A clean read-only reader,
given the exported SPEC alone (no kernel, no CLAUDE.md, `.git`-free,
preflight-verified) and a neutral prompt, reconstructed the required behaviour
from the text exactly — MUST fail, MUST NOT record, no outcome — and found no
second defensible reading, noting the clause names and forecloses every
alternative. It volunteered two underspecifications, both acceptable: the bound
VALUE is deliberately the reference's choice (larger allowed), and the operational
mechanics of "fail" are unpinned — but the only conformance-observable half of
that (writing a partial `outcomes.json`) is already forbidden by "MUST NOT record
the non-fixpoint state," leaving just the exit-code, which is ordinary impl
latitude. A single clean-target read, so weighed as such; the volunteered gaps are
evidence it read rather than agreed.
