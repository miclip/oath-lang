# The webhook friction log (#120)

**What this is.** A record of everything an application wanted that Oath did not
have, written while building `apps/github-webhook` — a GitHub webhook receiver
that verifies `X-Hub-Signature-256`, extracts the repository from the signed
body, and appends a record to a durable log that a separate script consumes.

**The rule the build ran under**, from #120: *build what you would have built in
Go six months ago, and extend neither the language nor the protocol on the first
pass.* When the application wanted something Oath lacked, write down what was
wanted, what the workaround was, and what it cost — do not fix it. **The
language and protocol are unchanged.** Nothing in `oath/`, `oathrs/`,
`docs/SPEC.md` or `examples/` was touched; the application is entirely
definitions in `apps/github-webhook/` plus three shell scripts.

**What the application is, and how it was exercised.** `deliver.sh` signs a
payload with **openssl** and posts it with **curl**, exactly as GitHub's
documented scheme describes. It shares no code with Oath — that independence is
the point, and it is a real cross-implementation check of `hmac-sha256`. The
bodies are real GitHub event payloads (10–14 KB of JSON, fetched from the API
for `miclip/oath-lang`) reshaped as webhook deliveries. `report.sh` reads the
log and is the DEPENDENT: it is what makes this an application rather than an
example, and it is what broke when the record format changed.

Four modifications after it first worked, each forced by something observed:

| | what forced it | what changed |
|---|---|---|
| **1** | the log said an event happened but not to what | scan the repository out of the JSON body |
| **2** | **a forged delivery was accepted with the secret unset** | refuse to serve without a usable secret |
| **3** | three misconfigurations were answered `202` | method, path and content-type discipline; the `ping` handshake |
| **4** | the consumer silently misread the old format | the record declares its schema; field order follows the record model |

Six further rounds followed REVIEW rather than observation, and found twelve
more defects that the thirteen properties did not — including one that panicked
the request handler. Entry 9 is the tally, and it is the most useful number
here.

---

## Ranked

Ranked by **what it cost and what it risked**, not by how interesting it is.
The first three decide what happens to the language next; the fourth is about
this repository's own gates and should be read by anyone adding to the corpus.

**If you read one other entry, read 9** — thirteen properties and six review
rounds found twelve defects between them with NO overlap, and the reason they do
not overlap is structural rather than accidental. It is the sharpest thing here
about the guarantee ladder's reach, and it is not a language finding, which is
why it is not in the top four.

### 1. `process_env` grants the environment, not a variable — so a missing secret is not a launch failure

**FAIL-OPEN. Demonstrated by exploiting it, not by reading the code.**

*What was wanted:* the receiver must not start without `GITHUB_WEBHOOK_SECRET`.

*What happened:* it started. `oath build` reports

> `every one is resolved before the entry point runs, or the program exits 70.`

and that is true and it was not enough. The `env` requirement resolved, because
`process_env` is *the authority to read the environment*, which the host has
whether or not any particular variable exists. The gate is loud about capability
KINDS and silent about capability CONTENT — and in the source,
`((. caps env) "GITHUB_WEBHOOK_SECRET")` looks exactly like a value that must be
there.

```
$ env -u GITHUB_WEBHOOK_SECRET ./gh-webhookd &
oath handler listening on :8897
$ curl -X POST .../hook -H "X-Hub-Signature-256: sha256=$(hmac_sha256('', body))" ...
  HTTP 202
$ cat events.log
1785692887  push  forged-0001  miclip/oath-lang
```

An attacker who knows the deployment is misconfigured signs with the **empty
key** and is authenticated. Six properties covered this handler at the time and
all six passed; none of them said anything about it.

*Workaround:* refuse every request when the secret is missing or shorter than 16
characters, with `500` (this deployment is broken) rather than `401` (you are
not authorized). Verified: the identical forgery now gets `500`, a 15-character
secret gets `500`, 16 characters gets `202`.

*What it cost:* one definition, one branch, two properties — and the workaround
is **strictly weaker than what was wanted**. A handler is a pure function with
no startup, so it cannot refuse to LAUNCH, only to answer. The port still binds.
A monitoring system sees a healthy process serving 500s.

*What would have fixed it:* `process_env(keys = ["GITHUB_WEBHOOK_SECRET"])` —
**#117**, narrowed capability requirements. This is #117 arriving as a security
bug rather than as a design preference, and it is the strongest evidence this
exercise produced about what to build next.

---

### 2. The handler protocol has no header model, so one backend's normalization is visible in Oath source

**FAILS SILENTLY, and fails open in the shape that matters.**

*What was wanted:* read `X-GitHub-Event`, the header GitHub documents and sends.

*What happened:* it is not there. Go's `net/http` canonicalizes header keys, so
`X-GitHub-Event` arrives as `X-Github-Event`. `apps/github-webhook/hdr-probe.oath`
is the runnable witness:

```
$ curl -X POST localhost:8898/ -H 'X-GitHub-Event: push' -d '{}'
MISS-as-documented|push|MISS-lowercase
```

*Workaround:* spell the headers the way Go canonicalizes them, and keep the
probe as a regression witness.

*What it cost:* nothing this time, because the guess was right. The cost is what
it risks. Modification 3 branches on `(== (header-or hs "X-Github-Event" "-")
"ping")`; had that been spelled as documented, the ping branch would be dead
code, every ping would fall through to the record path, and **no test of the
Oath file would catch it** — the artifact is correct with respect to the
`Request` value it is given, and the transformation happens before that value
exists.

*The deeper problem:* `Request` is a backend-neutral type in `program.go`, but
what a backend PUTS in it is unspecified. Two conformant backends may disagree
about header casing, ordering and repeats, and an application written against
one will silently misbehave on the other — which the LLVM backend cannot
currently demonstrate, because it refuses the handler protocol entirely. The
compile.go comment documents the canonicalization honestly; nothing propagates
it to the artifact author. **Not #117 and not #118 — this is the handler
protocol's own gap.**

---

### 3. No JSON, and no way to get bytes back into text

*What was wanted:* `repository.full_name` out of the request body. In Go:
`json.Unmarshal` into a two-field struct and one field access — five lines,
total, with escape handling, whitespace tolerance and nested-path addressing
included.

*Workaround:* five definitions (`bytes-str`, `bytes-prefix`, `bytes-after`,
`json-string-value`, `json-scoped-string`) and thirteen properties, implementing a
byte SCAN rather than a parse. It works on every real payload tested.

*What it cost:* 46 lines of code (the whole receiver is 243), thirteen
properties, and four documented failure modes that a parser
would have removed for free —

- it cannot address a nested path, only search for a literal, so the scope is
  bounded by hand (`"repository":{` then `"full_name":"`);
- it assumes **compact** JSON. `"repository": {` with one space matches nothing
  and silently yields the absent marker. This is not hypothetical: the first
  attempt at the test fixtures used `json.dumps` defaults and produced exactly
  that;
- it does not understand escapes, so a value containing `\"` truncates;
- if GitHub reorders its keys or nests an object literally spelled
  `"repository":{` earlier, it reads the wrong one and says nothing.

*The sub-finding, which is about types rather than libraries:* `str-bytes : Str
-> (List Int)` exists and is PROVEN. **Its inverse does not exist and cannot be
written correctly**, because `Str` is a list of CODEPOINTS and a request body is
a list of BYTES. `bytes-str` reinterprets rather than decodes; on a UTF-8
repository name it produces mojibake, silently, and the type system cannot see
the difference because both are `(List Int)`-shaped.

**And it was not theoretical — it was a live crash in the file that documents
it.** `secret-is-usable` checked length only, so a 32-character non-ASCII secret
was admitted, `str-bytes` handed codepoints above 255 to `hmac-sha256`, and:

```
$ GITHUB_WEBHOOK_SECRET=ключключключключключключключключ ./gh-webhookd &
$ curl ... -H "X-Hub-Signature-256: sha256=<correct digest>" ...
curl: (52) Empty reply from server
http: panic serving 127.0.0.1: byte list element out of range 0..255
```

The process survives — net/http recovers per connection — so it serves dropped
connections indefinitely while a health check sees a listening port. A Latin-1
secret would have been worse: every codepoint in byte range, nothing raised, and
the digest silently disagreeing with GitHub forever.

Found by review, on a file whose header paragraph already described the general
problem. **Writing the hazard down at the top of the file did not stop it being
present at the bottom of the file**, which is the argument for a type that says
which of the two a value holds rather than a comment that says which one it
should hold. `secret-is-usable` now requires `bytes-ok (str-bytes secret)`, and
that conjunct is a refinement type spelled as a runtime check (#69).

*What proved:* `bytes-str` and `bytes-prefix` reached PROVEN; `bytes-after` did
not — its `finds-at-head` property needs induction over the needle plus
`drop`/`length` reasoning, and a prove run was still grinding on it after fifteen
minutes. It stays `tested`, honestly.

*For #118:* the datatype slice this application actually demanded is **byte
lists and text**, not numerics. Numeric work was `show-nat` on one field.

---

### 4. The corpus has two scopes, and they disagreed silently

*What was wanted:* to add definitions to the project and have every existing
gate cover them.

*What happened:* `oath fixtures` derives fixtures from the **store**, so the
sixteen new definitions immediately got hashes, verify transcripts, analyses and
proof scripts. `oathrs/conformance.sh` — the cross-kernel gate — read
`examples/*.oath`. The Rust kernel was never shown the source, produced no
output for any of them, and the gate reported:

```
== Check 4: verification (verify/*.txt) ==
  FAIL: verify output differs for gh-webhook.txt
  ... 15 more
```

Sixteen divergence reports, none of them a divergence. **The dangerous part is
what a divergence report instructs you to do:** CLAUDE.md says treat one as a
spec bug or a kernel bug and never "fix" `oathrs` by copying from `oath/`. Acting
on these would have meant investigating a blind kernel for a defect that was
entirely in a shell glob.

`make verify` had the same scope, with a worse failure: it re-puts
`examples/*.oath` to rebuild the store from source, so a from-scratch rebuild
would have produced a store the committed fixtures no longer describe. This
repo has been bitten by exactly that before — `rat`, `convert` and `circle` were
in the store and pinned in fixtures while missing from the `EXAMPLES` list, and
that is how the live registry ended up with no rational family at all.

*Workaround, and the right fix:* both now enumerate `examples/*.oath` plus
`apps/*/*.oath`. Nothing about the corpus model changed — the app is a corpus
member like any other, and the Rust kernel now verifies the most feature-dense
artifact in it.

*What it cost:* about twenty minutes, and it is ranked here rather than lower
because of what it nearly cost. **A gate whose scope is narrower than the thing
it measures does not fail quietly — it fails LOUDLY and WRONGLY**, which is
worse, because the report is confident and points somewhere else.

---

### 5. Every specification that must construct a valid input needs helper definitions, and there is no scratch scope

*What was wanted:* to check a handler's accept path. The generator cannot invent
a valid HMAC signature, so a property has to build one.

*Workaround:* four definitions — `gh-spec-secret`, `gh-sign`, `gh-request`,
`caps-with` — that exist only to be called from properties.

*What it cost:* four permanently bound names in the same namespace as the
artifact they specify. There is no test scope, no fixture scope, and no scratch
store: **anything you want to compile, you must first `put`, and `put` binds a
name in an append-only journal.** Two names in this repo's store exist for no
other reason: `hdr-probe` (an exploratory probe, since promoted to a documented
witness) and `bytes-until` (written by hand, then replaced by `take-while` from
the library ten minutes later, and unreferenced ever since).

This is the permanence guarantee — a genuine trust-boundary property — firing
during ordinary iteration, which is exactly the LOUD/QUIET question #120 asks
about. It should be loud when publishing to a registry. It fires identically
when trying something in a local store for thirty seconds.

*Related:* `gh-sign` shares `hmac-sha256` with the handler it specifies, which
is circular for one step. What breaks the circle is outside the file: the RFC
4231 known-answer test in `examples/webhook.oath`, and openssl computing the
same digest over the wire in `deliver.sh`.

---

### 6. A property passed 200 generated cases and was false

`gh-record`'s `has-four-fields` (now `has-five-fields`) claimed a tab-separated
record has a fixed field count. With the JSON scan stopping only at the closing
quote, a repository name containing a literal tab forges an extra column:

```
$ oath eval '(length [Str] (str-split 9 (gh-record (Req ... "{\"repository\":{\"full_name\":\"evil\tcolumn\"}}" ...))))'
5 : Int
```

The generator will not assemble that byte sequence by chance and running it
longer would not help. What found it was asking what a value could contain.

*Fix, in the artifact rather than the property:* the scan stops at any raw
control byte as well as at the quote — which is also more correct, since JSON
forbids raw control bytes in strings. The field count is now true by
construction. `oath eval` on the same crafted body returns `4`.

**AND IT WAS STILL WRONG AFTERWARDS, IN THE SAME FUNCTION.** The fix went into
the JSON scan; the two HEADER fields of the same record kept splicing their
values in raw, so a tab in `X-Github-Delivery` still produced six columns. Found
by review, four rounds later — after the class had been identified, fixed once,
and written up prominently in this file.

Those two are worse than the one that was found, because **GitHub's HMAC covers
the body only**. The headers are unsigned: anyone who observes a valid delivery
can replay it with a tab spliced into the delivery id, with no secret, and
corrupt the log. Once `report.sh` checked the field count, that became a denial
of service on the consumer through data nobody authenticated.

*Real fix:* one `record-field`, applied to every variable field, with a property
saying the output can never contain a tab. The field count is now true because
of a function rather than because of a habit.

*What it cost:* ten minutes the first time and a near-miss the second. It is
ranked this high because of what it says about the instrument: **the guarantee ladder's
middle rung is only as good as the generator's reach, and its reach into
adversarial byte sequences is nil.** Mutation scoring measures a different
thing. Nothing in the tooling would have flagged this.

---

### 7. Strengthening the artifact silently weakened its specification

Modification 2 made an unusable secret a `500`. That made
`accepts-github-signed` — the only property that reaches the accept path —
almost entirely vacuous, because a generated `Str` reaches sixteen codepoints
almost never, so nearly every case took the `true` branch. **It still reported
`passed 200 cases`.**

*Workaround:* construct the capability world in the property (`caps-with`),
keeping the sink and the body quantified and pinning only the secret.

*What it cost:* the four scaffolding definitions in entry 5, and a real risk of
not noticing. The report says `passed 200 cases` either way. Mutation scoring
would eventually show it as a drop in kills, but nothing connects "I added a
guard to the body" to "a property about an unrelated path is now vacuous".

*The general shape:* a guarded property is vacuously true whenever the guard
fails, and the guarantee report does not distinguish `200 passed` from
`200 skipped`. **A vacuity signal — how many generated cases actually reached
the conclusion — would have caught this, and entry 6 is the only reason I
looked.**

---

### 8. A handler has no startup, so a configuration error cannot prevent the port from binding

Called out separately from entry 1 because it is a protocol gap rather than a
capability-granularity gap, and it would still bite with #117 done for any check
that is not expressible as a capability requirement.

In Go: `if secret == "" { log.Fatal(...) }` in `main`. In Oath the artifact IS
`(-> Request Response)`; there is nowhere to put a check that runs once. The
only startup the protocol has is capability provisioning, so every launch-time
invariant must be encoded as one, or degraded into a per-request answer.

---

### 9. Thirteen properties, six review rounds, twelve defects — and no overlap

**The most useful number this exercise produced.** After the application worked
and passed its own specification, six rounds of `codex review --uncommitted`
found twelve real defects. The 13 properties on the handler found none of them,
and the properties were not lazy — they had already falsified four generated
cases during authoring and forced a real fix (entry 6).

| found by review | what it was |
|---|---|
| `report.sh` counted redeliveries twice | announced deduplication it did not perform |
| `application/jsonp` | prefix test for `application/json` |
| `application/json payload` | a space accepted as a parameter delimiter |
| non-ASCII secret | **panicked the request handler**, empty reply, per-connection |
| Latin-1 secret | no error, wrong digest, every delivery 401 forever |
| `sha256=<valid digest>zz` | `hex-decode` keeps the prefix it decoded (#121) |
| the launch probe in `acceptance.sh` | hangs forever on the regression it tests |
| the report's columns | heading said repository-then-event, output was the reverse |
| `make check-app` | worked only if another target had built `oath/oath` first |
| a secret containing a space | the code required 33–126, the README said "ASCII" |
| `oath-gh/1` with four fields | the tag was checked, the field count was not |
| a tab in an **unsigned** header | entry 6's defect again, in the same function, after the write-up |

**The two classes do not overlap, and the reason is structural.**

Properties caught TYPE and RANGE errors — a generated `(List Int)` holding
`-19`, a `Str` whose codepoints exceed byte range. The generator is very good at
those because they are what random values are.

Review also caught a defect I had ALREADY FOUND, fixed once, and written up at
length — entry 6, present twice more in the same function. Knowing the class was
not enough; only a function every field goes through was.

Review caught BOUNDARY-CONDITION and PREDICATE-SHAPE errors — `jsonp` versus
`json`, 64 characters versus 66, U+00E9 versus its UTF-8 encoding. A generator
does not produce `application/jsonp` by chance, and **worse, several of these
properties could not have caught the defect at any sample size**:
`unreadable-content-type-is-415` guards on `(not (str-prefix "application/json"
ctype))` — the implementation's own predicate. Every `ctype` the guard admits is
one the handler also rejects. The property is true for a receiver that gets the
predicate wrong in any way, as long as it is wrong CONSISTENTLY. The
specification and the artifact shared the mistake, and sharing it made the
mistake invisible. Mutation scoring cannot see it either: mutating the body
mutates the guard with it.

*Fixes:* `media-type-is` and `path-is` now have the shape *a prefix is a match
only when what follows is a delimiter*, each with a property asserting that a
LONGER value does NOT match — a claim about the predicate rather than a
restatement of it. `gh-signature` requires exactly 64 characters.
`secret-is-usable` requires printable ASCII. `acceptance.sh` grew a check per
defect, from 16 to 27.

*What it says:* the guarantee ladder is strong exactly where a random value is a
good adversary, and it has no reach at all where the adversary is a
near-miss — a string one character longer, an encoding one byte different.
Nothing in the tooling distinguishes a property that constrains the artifact
from one that restates it. **A warning when a property's guard names the same
function its subject branches on would be cheap, and it would have flagged three
of the twelve.**

---

### 10. The record model's four categories were sharp enough to catch me misusing them

Not friction — the opposite, and the only entry here where an Oath idea earned
its keep in a place it was not designed for.

Modification 4 laid the record's fields out against `DESIGN.md`'s categories
(assertion / derived / observation / equivalence) and labelled the delivery id,
the event and the repository all as ASSERTED. Review pointed out that **GitHub's
HMAC covers the body only.** The repository is read out of bytes the signature
protects. The delivery id and the event name ride along in unsigned headers, so
an adversary who captured one valid body-and-signature pair can vary the delivery
id on every replay and defeat the consumer's deduplication, or relabel the event
at will.

Nothing fixes that at the receiver: a value cannot be bound to a signature its
signer never computed over it. What the model did was make the mistake
*sayable* — "asserted by whom, and covered by what?" is exactly the question the
four categories are built to force, and it split one column of the record from
the other two. The record is now documented in three tiers, and a consumer is
told which single column survived authentication.

*What it says:* the categories are not registry-specific. They are worth
applying to anything an artifact writes out, and they are more useful when the
answer is uncomfortable.

---

### 11. Two of the list library's functions are monomorphic

`contains` and `index-of` are `Int`-only while `any`, `all`, `drop`, `take`,
`take-while`, `filter` and `map` are polymorphic. Cost: one failed `put`
(`contains takes 0 type arguments, got 1`) and a lookup — trivial, recorded
because it is a one-line fix that will keep costing people a minute each.

---

### 12. `oath find` exists and never surfaced during authoring

`bytes-until` was written by hand when `take-while` already existed, PROVEN, in
the corpus. The discovery layer is genuinely good and is invoked from a separate
command that an author writing a definition has no prompt to reach for. Cost: a
redundant definition and a permanently bound name (entry 5).

---

### 13. `caps-with` is reported as `sink: ESCAPES`

Correct — it puts the sink in a record and returns it — but the confinement
analysis cannot distinguish *packaging a capability for a caller that already
holds it* from *leaking one*. Cost: nothing yet; noted because a verdict that
cannot tell those apart will eventually be read as noise.

---

## What did NOT create friction

Worth as much as the list above, because the guarantees that stayed quiet during
ordinary composition are the Phase 4 target:

- **The crypto interoperated on the first request.** openssl signed, Oath
  verified, `202`. No byte-order, argument-order or encoding problem — the RFC
  4231 known-answer test in `examples/webhook.oath` had already pinned all of
  them across two kernels.
- **`oath build` never needed thought.** Capabilities are declared by the entry
  point's type; the compiler derives requirements, imports and the launch gate.
  It was never restated and never in the way.
- **`DESIGN.md`'s record model transferred to an application's own output** and
  caught a real misclassification while doing it. Entry 10.
- **The record type, `match`, `Option` and higher-order capability records all
  behaved.** Constructing a capability world inside a property — the fix for
  entry 7 — worked first time with no special support.
- **Re-putting merged metadata correctly** across five rounds of modification.
  Verdicts survived; nothing had to be regenerated.
- **Properties caught real mistakes as they were written**, including two the
  generator falsified in four cases (entry 5's byte-range guards).
- **13 properties on the handler, none of them restated at a call site.** The
  guarantee was decision-relevant at exactly two moments — `oath put` and
  `oath build` — and silent in between.

## Where the guarantees were LOUD, and whether they should have been

#120 asks where evidence is decision-relevant versus merely repetitive.

| moment | loud? | right? |
|---|---|---|
| `oath put` — verdicts, falsifications, counterexamples | very | **yes**, every time; two real defects surfaced here |
| `oath build` — requirements, launch gate, provenance digest | yes | **yes** — it is the one place the authority is stated |
| binding a name | yes, and permanent | **no**, not for a local store during iteration (entry 5) |
| composing already-admitted definitions | silent | **yes** |
| a *missing* capability's contents | **silent** | **no** — entry 1, and it is the whole finding |

The pattern is not "too much evidence". It is that the loudness is attached to
**operations** (put, build, publish) rather than to **risk**. Binding a
throwaway name is as loud as publishing a library; a `process_env` requirement
that will be satisfied by an empty string is as quiet as one that will not.

## What this says about the queue

- **#117 (narrowed capability requirements) is the top item**, and entry 1 is
  the argument. It is the difference between this program refusing to start and
  this program accepting forged deliveries.
- **#118's datatype slice should be byte lists and text**, not numerics. The
  application's numeric demand was one `show-nat`. Its text demand was ninety
  lines of scanning and one function that cannot be written correctly (entry 3).
- **The handler protocol needs a header model** — **#122**, filed from entry 2.
  Separate from #117 and #118, and currently invisible because only one backend
  implements the protocol, which makes now a good time to decide rather than a
  reason to defer.
- **Property generation has no reach into near-misses** (entry 9). Not a
  roadmap item so much as a boundary to state out loud: the ladder is strong
  where a random value is a good adversary and absent where the adversary is one
  character longer or one encoding different. Reviewing an artifact is not
  optional because it carries proofs.
- **A vacuity signal on generated properties** (entry 7) is small, cheap, and
  would have caught a real weakening. So would a warning when a property's guard
  names the same function its subject branches on — that shape hid three of the
  twelve defects in entry 9.
- **#119 (entry shape as an explicit variant) cost nothing here.** The
  application never confused the two entry protocols. That is evidence for
  ranking it below the four above, not for closing it.
