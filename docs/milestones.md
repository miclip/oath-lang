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

Also filed from the exercise: **#121** (`hex-decode` keeps the prefix it decoded,
so `examples/webhook.oath` accepts `<valid digest>zz` while claiming to fail
closed — filed rather than fixed, because it changes the conformance corpus).

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
