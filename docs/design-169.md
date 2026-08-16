# #169 — three candidate designs, evaluated on what F3 COSTS (recommendation WITHDRAWN, §4)

**Design only. No experiment was run for this file, and nothing in the
repository was changed but this file.** Every measured number is cited to the
file that produced it. Claims DERIVED from a measured rule rather than measured
directly are marked `DERIVED` and carry the command that would settle them.

**Each candidate is evaluated in its STRONGEST form, not in the form Oath
happens to implement today.** A design refuted because the current kernel lacks
a feature has not been evaluated; it has been dated. Where a candidate needs
machinery that does not exist, that machinery is specified well enough to be
priced, and the price is the finding.

## 0. What is being decided, and on what

#169 asks whether Oath needs a type that separates bytes from text.
`docs/experiments/issue-159-refinements.md` closed one shape of the answer —
refinements of the form `{v : (List Int) | P v}` cannot do it, because both roles
are carried by ONE value — and declared three constructions OUTSIDE that result:

> **Record wrappers, opaque or abstract types, and distinct datatypes were
> declared OUTSIDE it**, and being outside a result is not being refuted by one.

Those three are what this file evaluates. It does not re-open #159 and does not
re-derive its partition.

**The driver is F3's COST, not F3's closure.** #169's own falsifier asked whether
a property over the existing types closes each failure;
`docs/experiments/issue-169.md` answers it — four of five close, F5 admits no
Oath-level property at all, so the quantifier fails and #169 is **not** declined
on that argument. What that file records BESIDE the result is what a type would
have to buy:

> every property that closes F3 encodes the intended role, and the role is not
> in the types … the evidence cannot be READ OFF the artefact under test at any
> cost of diligence, because the fact it must encode is not present in the
> values.

So the test applied to each candidate is not *does it typecheck*, not *is it
cheap*, and not *does it resemble a real type system*. It is:

> **At the point where `[195,169]` acquires a reading, WHO supplies that reading,
> and is the supplier in a position to be right?**

A design earns a recommendation only if the answer is an **authoritative
boundary** — a party that knows because the fact was determined outside the
program. A design whose answer is *the author, at a call site, unchecked* has
moved the mistake, not removed it.

**The three candidates differ on exactly one axis, and naming it early is worth
more than the three write-ups.** All three can carry the ENCODING in the type —
§2.3 shows what that costs each of them, and it is not the same. What separates
them is **who may MINT a value of that type, and who may take the tag back off**:

| candidate | how the encoding enters identity | who may mint | who may UN-mint |
|---|---|---|---|
| **A** record wrapper | the record's FIELD NAME, which is hashed | the host, plus anyone with a record literal | anyone, with `(. e f)` |
| **C** distinct datatype | the datatype's STRUCTURE — which must be MANUFACTURED per encoding (§2.3) | the host, plus anyone with a constructor | anyone, with `match` |
| **B** opaque datatype | the datatype's STRUCTURE, plus a visibility rule in identity | **the host, plus only the abstraction's own definitions** | **nobody** |

**All three carry the encoding in identity, and all three therefore make the
ordinary wrong-decoder call a type error (§2.4). All three can also take the
host's mint, on the same protocol change and at the same price (§1.4).** The
column that separates them is the LAST one, and it is the only one that does.

Since the question is who supplies the reading, an axis about minting is the
whole question — but it is a NARROWER axis than "which candidate prevents F3's
mistake", and this file said the wider thing before §2.3 and §2.4 were written.

**But being able to mint authoritatively and HAVING A SITE TO DO IT AT are two
different things, and the second is not free — for any candidate.** Under the
handler protocol as §14 currently binds it there is no point in the trace where a
host could mint, so the boundary mint is unavailable to all three until the
protocol changes, and available to all three once it does. §1.3
states that finding and §1.4 specifies and prices the change; the recommendation
in §4 is conditional on it.

## 1. The value, and the trace every candidate is run against

From `docs/experiments/issue-169.md` part 10 and
`docs/experiments/issue-159-refinements.md` §"The diagonal":

    (utf8-encode "é")  =  [195, 169]
    (bytes-str [195, 169])  =  the two codepoints 195, 169  =  the text "Ã©"

- As an **ISO-8859-1 decode** the pair `([195,169], "Ã©")` is **correct** — not
  asserted but proven: `latin1-is-exact`, by induction, 4 seconds.
- As a **UTF-8 decode** it is **wrong**; the correct result is `"é"`.
- `both-roles-cannot-be-satisfied`: the conjunction of the two role obligations
  at this value is `false`.

The trace in the shipped receiver (`apps/github-webhook/webhook.oath`), which is
where F3 actually happened:

| step | site | type today | who determined it |
|---|---|---|---|
| **origin** | the request body, `Request`'s fourth field | `(List Int)` | the **generic HTTP boundary** — octets off the wire, and nothing more |
| **authenticate** | `:669`, HMAC over `(req-body r)` | `(List Int)` | the sender, by holding the secret — this establishes ORIGIN, and nothing about encoding (§1.1) |
| **admit** | `:674`, **415 unless `media-type-is "application/json"`** | — | the **receiver's own contract gate** |
| **scan** | `bytes-after`, `json-string-value` over the body | `(List Int)` | nobody; role-neutral, correct at either reading |
| **convert** | `:195`, `(bytes-str (json-string-value v))` | `(List Int) -> Str` | **the author**, by writing `bytes-str` |
| **use** | `:304`, the extracted `full_name` reaches `record-field` and the log | `Str` | — |

### 1.1 The boundary premise, stated at the right layer

**Two different boundaries are in this trace, and collapsing them is an error
that would decide the whole question wrongly.**

**The generic `Request` boundary does not determine the body's encoding, and
SPEC §14.1a is right to type it `(List Int)`.** SPEC §3's **ADMIT** rule —
*"An implementation MUST decode the octets as UTF-8 and MUST REFUSE input that is
not"* — is applied to `Request`'s method, path and header fields, which are `Str`
because HTTP determines their encoding. It is deliberately not applied to the
body, because HTTP does not.

**But F3 does not occur at that boundary.** It occurs strictly downstream of
`:674`, where this receiver has already answered **415** to anything that is not
`application/json`. Past that gate the encoding IS determined, and not by the
author:

> **JSON text exchanged between systems that are not part of a closed ecosystem
> MUST be encoded using UTF-8** — RFC 8259 §8.1,
> <https://www.rfc-editor.org/rfc/rfc8259#section-8.1>.

**The `charset` parameter is not part of that authority.** RFC 8259 §11 defines
no `charset` parameter for `application/json`, so `media-type-is` accepting
`application/json; charset=utf-8` alongside the bare form (`:534`) is harmless
compatibility with senders that emit one. It is not a second source of the
encoding fact, and a parameter naming some other charset would not displace what
§8.1 requires.

**Neither is the HMAC, and keeping the two apart matters.** `:669` computes
`hmac-sha256` over `(req-body r)` alone — it authenticates the BODY, not the
headers, so the content-type gate at `:674` reads an **unauthenticated** value.
And authenticity is irrelevant to §8.1 in either direction: the requirement
attaches to what a media type MEANS, not to who sent it, so an authentic sender
may declare `application/json` and transmit something else, while an anonymous
one is bound by §8.1 exactly as much. **The encoding authority here is the
CONTRACT ALONE**, and reading the signature as part of it would overstate what
this trace establishes.

So the fact F3 needs — *these octets are UTF-8 text* — **exists, is external to
the program, and is established by an authoritative contract before the
conversion runs.** An earlier draft of this file concluded that no such fact
existed anywhere, which is true of generic HTTP and false of this trace, and that
error alone would have produced a decline.

### 1.2 Why an in-language classifier still encodes the author's wrong choice

Having the fact available is not the same as having it supplied. Consider the
obvious in-language repair, which any of the three candidates permits:

    (if (media-type-is "application/json" ct) (as-utf8 body) (as-opaque body))

This is a real improvement: it replaces one assertion **per conversion** with one
assertion **per boundary**, at a site that inspects a value the program can
actually see. It is not a fact, and three ways it is silently wrong survive:

- **The mapping is the author's.** `content-type ⟹ encoding` is written by hand.
  Mapping `text/plain` to UTF-8, or honouring a `charset=iso-8859-1` parameter
  that the registration does not define and that cannot displace §8.1, produces
  a confidently mis-tagged value and nothing downstream can tell.
- **The tag outlives the justification.** The receiver SCANS and SLICES the body
  (`bytes-after`, `json-string-value`). A slice taken at an arbitrary offset of a
  UTF-8 buffer is not UTF-8, and it inherits the tag.
- **The classifier is one call among many.** Nothing obliges a second entry
  point, a test helper or a later maintainer to route through it.

**What converts the assertion into a fact is the HOST applying the contract at
admission** — the same party, at the same layer, with the same MUST-REFUSE
discipline that §3 already imposes on header fields. That is the sense in which
the reading becomes authoritative, and it is no stronger a sense than the one
this project already accepts for `Request`'s `Str` fields.

**Whether that host has a SITE at which to do it is a separate question, and §1.3
answers it: not today.**

**And that is why the minting axis decides the outcome.** A host-performed mint
is authoritative only if the program cannot forge the same tag by hand;
otherwise the guarantee is "the reading is right unless somebody wrote the other
thing", which is where the language already is.

### 1.3 The mint has no site under the protocol as it stands

**This is the finding that most changes what the rest of this file may claim, so
it is stated before the candidates rather than inside one.**

SPEC §14.1a declares the handler protocol's argument type normatively:

    (data Request []
      (Req Str Str (List (Pair Str Str)) (List Int) Int))

and **PROTO-TYPES-BY-IDENTITY** makes that declaration, not a name, what makes an
entry point a handler. §14.2's transformation runs to COMPLETION before the
handler is invoked: the backend answers 400 for the refusal rows and otherwise
constructs this five-field value, with the body as `(List Int)`, and calls
`(-> Request Response)`. Both shipped backends do exactly that —
`oath/compile.go:1538` builds the five-field constructor directly, and
`oath/llvm.go:4086` builds the same value while `oath/llvm.go:5006` CHECKS the
entry's argument type by hash and field count.

**The receiver's content-type gate is not at that boundary. It is application
code strictly downstream of it** — `apps/github-webhook/webhook.oath:674`, inside
`gh-webhook`, after the method check, the secret check, the HMAC and the path
check. So the trace's *admit* step is a site the HOST never occupies, and the
host's own site — Request construction — is one where the application's contract
is not known, because nothing in §14 lets an entry point say what media types it
speaks.

Two consequences, and the second is the one that binds B:

- **A host mint at admission is not available, at any price paid inside the
  language.** Grouped-Def visibility (B's kernel half, §"Permanent obligation")
  restricts WHICH DEFINITIONS may construct a value. Every definition is Oath
  code invoked after `Request` exists, so the tightest possible visibility rule
  still leaves the mint being performed by the program.
- **So B's mint, as the shipped trace stands, is the in-language form of §1.2** —
  the very case B's own falsifier (i) names as degradation to a single-site
  author assertion. B's closure claim therefore cannot rest on the existing
  protocol; it rests on a protocol change, which §1.4 specifies so that it can be
  priced.

**It binds every candidate, not only B.** B is named above because B is the one
whose write-up CLAIMED the boundary mint; A and C are in the same position and
always were — no record literal and no public constructor is reachable before
`Request` exists either. The mint is unavailable to all three today, and §1.4
makes it available to all three at one price.

This is not a defect in §14. §14.1a's `(List Int)` body is right for the reason
§1.1 gives: the generic HTTP boundary does not determine the body's encoding. The
finding is narrower — **the fact §1.1 establishes exists at a layer the current
protocol gives the host no way to report.**

### 1.4 `PROTO-BODY-IS-TAGGED` — the protocol change that would supply the site

Specified here to the depth §0 requires of machinery a candidate needs and Oath
does not have: *"specified well enough to be priced, and the price is the
finding."* **It is a proposal in this file and normative nowhere.**

**The types. The body field must have ONE static type, so the tag cannot be the
body's type — it has to be a SUM the host chooses a branch of:**

    (data Body [] (Opaque <opaque carrier>) (Utf8 <utf8 carrier>))
    (data Request []
      (Req Str Str (List (Pair Str Str)) Body Int))

**This shape is common to all three candidates and only the CARRIERS differ**,
which is worth stating here rather than three times below: `Request` is one
declaration with one identity, so "the body arrives as the UTF-8 type where the
contract determines it" is not expressible as a choice of field type. Under B and
C the carriers are `Utf8Bytes`/`OpaqueBytes` over §2.2's octet model; under A they
are the record wrappers `{utf8 (List Int)}` and `{opaque (List Int)}`.

The sum's two branches are what make a mis-declared body a branch the program
must take rather than a value it may misread — and that much is candidate-neutral
too. What is NOT neutral is whether the branch can be forged: `Body`'s
constructors are public in every candidate, so `(Utf8 x)` is as mintable as its
carrier. Under B the carrier is opaque and the forgery has no `x` to offer; under
A and C the carrier is a record literal or a public constructor away.

**The declaration is the entry's TYPE, and it declares only which protocol the
entry speaks — never the mapping.** The author does NOT write
`application/json ⟹ UTF-8`; if they did, §1.2's first failure mode returns
intact and the whole exercise has moved the author's assertion from a call site
to a declaration. The mapping is the specification's: §14 gains a normative
media-type table, and §1.1's parameter argument is promoted into it — a `charset`
parameter cannot displace what a registration requires.

**A stronger declaration is available and is deliberately not taken here:** the
entry could declare the media-type SET it accepts, letting the host answer 415
itself. That is coherent — the accepted set is genuinely the application's fact,
unlike the mapping — but it moves a status decision out of the application, it
does not change who mints, and it does not change the price below. It is named
so that the choice made here reads as a choice.

**The operation, at Request construction, before the handler is invoked.** The
backend reads the `content-type` field from the header list it has already built
under §14.2 — name canonicalized, value octets preserved — looks it up, and

- if no field is present, more than one `content-type` field line was sent, the
  value is malformed, or the registration does not determine an encoding —
  constructs `(Opaque …)`. The repetition case must be written down rather than
  left to a first-match convention: §14.2 PRESERVES repeated field lines, and the
  shipped receiver selects with `header-or`, which takes the FIRST
  (`webhook.oath:87`). If the host's selection rule and the application's differ,
  the value the tag was derived from is not the value the application gated on —
  the two would silently disagree about one request. §14.2 already REFUSES a
  repeated `Host` (row 22), so declining to tag here is the conservative member
  of an existing family rather than a new kind of rule;
- if the registration determines UTF-8 — validates the body octets by §3's ADMIT
  well-formedness rule, the same rule reused rather than a second one, and
  constructs `(Utf8 …)` on success, `(Opaque …)` on failure.

**Minting `Opaque` on validation failure rather than REFUSING is a decision, and
its reason is stated because the alternative is defensible.** Refusing would pull
an application's status choice — the receiver's own 415, and the 400-vs-415
question — into §14, making it normative for applications that never asked; and
§14.2's refusal rows are for what the model cannot represent faithfully, which an
ill-formed body is not. It represents perfectly well as octets.

**What the tag then means, stated no wider.** It is a host observation about
(the declared media type, the specification's registry, the octets as received) —
authoritative in exactly §1.1's sense and no further. It says nothing about the
sender's honesty, and it does not need to: §1.1 shows the encoding authority is
the contract, not authenticity. Validation is what keeps a lying sender from
obtaining a false `Utf8` — they obtain `Opaque`. And it reaches only media types
whose registration determines an encoding: an endpoint accepting `text/csv` gets
`Opaque` and is exactly where it is today.

**The price. It is not small, and it is owed by EVERY candidate that wants a
boundary-supplied tag — not by B alone.** B's write-up carried none of it before
§1.3 was written; A's and C's still owe the same table, because the sum above is
the only way any of them gets a tagged body under one `Request` identity.

| layer | what changes |
|---|---|
| **SPEC §14.1a** | `Request`'s declaration changes, so its identity changes. `PROTO-TYPES-BY-IDENTITY` makes that a FORK of the handler protocol: every conforming backend, every compiled handler and both §14.3 vectors re-identify at once. `Body` and whichever carriers the candidate uses become normative declarations alongside `List`, `Pair`, `Request`, `Response` — under A the carriers are record types, so the protocol's identity comes to depend on two hashed FIELD NAMES |
| **SPEC §14.2** | the body row splits. The octets are still PRESERVE; the tag is a new outcome, and under HDR-PRINCIPLE's five dispositions it is none of them — the media type is a header distinction being LIFTed into the body's type. §14.2 gains either a sixth disposition or an explicit rule that the tag is DERIVED and outside the enumeration. Choosing which is part of this price, not a detail: §14.1's own history is that the enumeration was wrong twice by being short |
| **SPEC §14 (new text)** | the media-type→encoding table; the no-registration, absent-field and malformed-field rules; the parameter rule. Plus §14.3 vectors for a tagged and an untagged body, since §14.0's obligation — *two backends produce the same `Request` value* — now quantifies over the tag |
| **SPEC §1** — **no candidate** | **No change, in any of the three.** `hmac-sha256` and `bytes-eq-ct` are normative over `(List Int)` (§1, #78) and are the only primitives taking an ADT, so a tagged body reaches neither directly — and in every candidate the gap is closed by an ordinary definition rather than by normative text. A projects: `(. r utf8)` is already a `(List Int)`. C `match`es out and assembles eight `Bool`s into an `Int` per octet (§2.2). **B performs the SAME assembly inside the opaque group and exports only the digest** — a group member may `ctor`/`match` against the group's own Defs, which is what the visibility rule says, so `Utf8Bytes -> (List Int)` exists as a PRIVILEGED helper and what leaves the group is `hmac-of : (List Int) -> Utf8Bytes -> (List Int)` — key first, as `hmac-sha256 key msg` requires, with only the MESSAGE assembled internally — returning the 32-byte digest. The caller holds the key and the received signature as `(List Int)` already, and runs `bytes-eq-ct` on the digest itself; no further export is needed for the shipped receiver's check. What differs between the candidates is WHO may run the assembly — the minting axis — not what §1 must say |
| **Go backend** | `oath/compile.go:1538` constructs the five-field `Req` directly; it gains the lookup, the validation and the sum construction. **AND THIS MIGRATION HAS NO FAIL-CLOSED GATE, which an earlier draft of this row claimed it did.** Replacing the body's TYPE does not create a new `EntryShape`: the entry is still `shapeHandler`/`shapeHandlerCaps`, so `program.go:87`'s terminal sentinel never fires. After the protocol hash moves, `compile.go:1537-1539` still compiles and still puts the raw list in field 4, and LLVM's construction is equally untyped. A backend that ignored the change would build a program that runs and is wrong. **The seam was derived from the wrong boundary** — the claim is about the BODY's representation and the sentinel owns the ENTRY's shape, so it cannot quantify over the thing being changed. Adopting §1.4 therefore has to design an exhaustiveness mechanism that covers the body transformation itself; that mechanism does not exist, is not sketched here, and is part of the price |
| **LLVM backend** | `oath/llvm.go:4086` builds the same value and `:5006` checks the argument type's hash and field count, so the fork is observed there too; UTF-8 validation must be written in C, over a body already bounded at 1 MiB. (The LLVM backend refuses the `http_request` CLIENT capability, not the handler protocol — it implements §14 with its own HTTP/1.1 server, so there is no backend here that escapes the change) |
| **identity / migration** | `Request`'s hash moves, so every definition whose type mentions it re-identifies — `gh-webhook`, `gh-record`, `gh-request` and their properties. Mutation scores and waivers are structure×seed facts and DROP by construction. `codebase/names.json`, the journal and the playground snapshot move with it — the last because `check-playground-snapshot` fires on corpus IDENTITY, which is exactly what this changes |
| **conformance** | §14.0 states that §10's five conformance points **do not reach §14**. So this half's conformance cost is §14.3's vectors and cross-backend agreement, NOT §10. B's OTHER half — the grouped Def — is the one that touches §10 and the blind Rust kernel. **Two different conformance surfaces, and only the second was priced before this section** |

**The SPEC §1 row was three rows, and charged B for a cost B does not have.** An
earlier version of this table charged B either a re-typing of §1's two crypto
primitives over the octet model or an exported octet view — and concluded that
"B's opacity and the shipped receiver's signature check are in tension, and the
resolution is a change to §1's primitives rather than to an application". **Both
are withdrawn.** The assembly the tension was about is exactly what a privileged
group member is for: it runs inside the abstraction, and what crosses the
boundary is a fixed-size digest — 32 bytes whatever the body was, so it is not a
view of the body at all. The signature check
compiles against B with no SPEC change, and what B pays over C is then the
grouped-Def visibility form alone (§4's table; over A it is that form plus
§2.2's structural model, which C pays too). Two consequences follow, and both
are readings of committed text rather than measurements:

- **The digest export does not violate falsifier (ii)**, which had to be stated
  in terms of a payload VIEW to say so: a fixed-size result is not one, whatever
  the body's length. See B's falsifier below, where the restatement and the
  reason it is not a weakening are recorded.
- **§2.2's exactness discharges a hazard §1 names.** §1 makes an element outside
  `0..255` a RUNTIME ERROR in both primitives. An assembly out of eight `Bool`s
  cannot produce one, so the privileged helper is total and in range by the type,
  where a `(List Int)` body carries the obligation at every call.

**And one structural choice inside the change, priced both ways.** Replacing
`Request` gives one protocol and forces migration of every existing handler.
Adding a second protocol type beside it avoids migration and leaves both backends
and §14.3 carrying two handler protocols permanently — the accumulating-exception
shape `CLAUDE.md` names as a missing-artefact trigger. Neither is free, and this
file does not choose between them.

**What §1.4 does NOT price.** No effort estimate, consistent with §5. And it does
not settle whether the octet operations an application needs can all live inside
the abstraction: `bytes-after`, `json-string-value` and `bytes-str` are
application-local definitions in `apps/github-webhook/webhook.oath`, so with a
tagged body they must either become tag-preserving library operations, join the
opaque group, or be served by an exported view — the last being available only
where the view does not RECOVER the payload, which for a scan that returns a
slice of the body it plainly does.

**The first two routes are the same mechanism as the HMAC row above, so what is
open here is VOLUME and not feasibility.** A group member may take the payload
apart; the scan can therefore live in the group and hand back tagged slices, at
the cost of moving application-local definitions into a library someone
maintains and proves. That is a second finding about B's abstraction boundary,
not about its mint: it costs work and it costs no SPEC change, and this file
does not measure how much of the receiver's octet surface would have to move.

## 2. What Oath's identity model fixes, which constrains all three candidates

Measured facts, not design opinion. Each is load-bearing below.

| fact | authority |
|---|---|
| Only `Def` is hashed; **names, type-variable names, constructor names and property names are metadata, outside identity** | SPEC §1, §9 |
| A `data` Def encodes `u32` tyvars, `u32` ctor count, then per ctor `list<Ty>` fields — **no type name, no constructor names** | SPEC §1.2, tag `0x01` |
| Two structurally identical declarations are **ONE OBJECT**. `(data Str [] (SNil) (SCons Int Str))` and `(data Bytes [] (BNil) (BCons Int Bytes))` put to `e6bbed8bc934…` from two independent stores, byte-identical object files | `docs/experiments/issue-159.md` §"Result — the two printed hashes" |
| A value of one is accepted where the other is declared, with no conversion — `oath/kernel_test.go:460` `TestAliasedADTsBothElaborate` returns a `Run` where `Interval` is declared and it typechecks | `oath/check.go:80-101` (`tyEq` compares `Hash`) |
| **There is no implicit coercion anywhere**, and no subtyping relation in the checker | `oath/check.go`; SPEC §2 |
| **RECORDS ARE THE ONE PLACE A NAME ENTERS IDENTITY.** Record types are anonymous and structural, but *field names are hashed* — "names strictly ascending bytewise"; "field names are semantic — part of the interface and therefore part of the hash" | SPEC §1.2 tags `0x08`/`0x1D`; `oath/ast.go:15-18` |
| No opaque type, no constructor privacy, no module boundary, no visibility modifier exists today. Constructor lookup *scans the name index*; `ctor` requires only a data hash and a valid index | SPEC §1.4, §2 |
| `Str`'s scalar range is enforced at the boundaries octets CROSS, never at construction — a kernel **MUST NOT** reject `(SCons -1 (SNil))`, because *"a rule attached to `SCons` would bind every `(data T [] (A) (B Int T))`"* | SPEC §3 |
| `Str` runs over a native representation: a Go `string` (`oath/compile.go:1772`), a packed UTF-8 buffer with view-based tails in LLVM (`oath/llvm.go:1746`). Both PACK by refusing, never substituting | `oath/compile.go:731-771`, `oath/llvm.go:2031-2046` |
| Native recognition is **hard-coded per type, by name→hash, in BOTH backends**. Go recognises `Str`, `Set` and `Map` and carries a fixed OPERATION table for them. LLVM recognises `Str` — `strTypeHash` is `st.Resolve("Str")`, and the hash then drives `ctor` emission into a packed `T_STR` and `match` into `emitStrMatch` — but has no native DEFINITION lowering, which is the only thing `llvm.go:358` says | `oath/compile.go:360-392`; `oath/program.go:239` (`strTypeHash`), `oath/llvm.go:5081` (resolved once per emitter), `oath/llvm.go:455-456` (ctor), `oath/llvm.go:1110-1112` (match) |

### 2.1 Spelling hazards — real, and NOT a candidate's falsifier

Two shapes are already taken, and a design that reaches for either gets an
existing corpus type instead of a new one.

- **The cons-list spelling `(data Bytes [] (BNil) (BCons Int Bytes))` IS `Str`** —
  measured, byte for byte, from two independent stores.
- **The newtype spelling `(data Bytes [] (MkBytes (List Int)))` is structurally
  identical to `examples/set.oath`'s `(data Set [] (MkSet (List Int)))`**, bound
  in the committed store to `16a9d47302b9d1ef…`.

  > `DERIVED` from SPEC §1's encoding rule plus the measured `Str`/`Bytes`
  > result; not run here. Settled by
  > `OATH_STORE=$(mktemp -d) ./oath/oath put bytes.oath --new --json` on that
  > declaration, compared with `codebase/names.json`'s `Set`.

  If it holds, the consequence exceeds a name clash: the Go backend intercepts
  constructors BY HASH (`oath/compile.go:1784` → `osetFromList`), so such a
  `Bytes` would compile to a **sorted, deduplicated** native set while
  `oath eval` — the reference — keeps the list as written.

**These are hazards of a SPELLING, not properties of a candidate.** A design is
free to choose a shape that collides with nothing, and sections B and C do. What
the hazards establish is a build-time hygiene obligation — *hash any new
structural model against the committed store before adopting it* — and one
durable warning: **structural distinctness is not owned.** A shape unique today
is shared the moment anyone declares it again, and nothing announces that.

### 2.2 The structural model both B and C use

To compare the minting axis rather than two arbitrary shapes, B and C are given
the **same** model. An octet is not an `Int`, and saying so is what makes the
shape distinct from `Str`, from `Set`, and from any `(List Int)` wrapper:

    (data Octet [] (Oct Bool Bool Bool Bool Bool Bool Bool Bool))
    (data Bytes [] (BNil) (BCons Octet Bytes))      ; an octet string

**The eight `Bool`s are the point, and `(Oct Int)` — which this section carried
first — was wrong.** `Int` is ℤ (§2), so `(Oct Int)` has infinitely many
inhabitants and `(Oct -1)` is a well-typed value. The range would then have to be
restored by a predicate re-checked at every use or by a host refusal at a
boundary — and SPEC §3 forbids the tempting middle route explicitly: a kernel
**MUST NOT** reject `(SCons -1 (SNil))`, because *"a rule attached to `SCons`
would bind every `(data T [] (A) (B Int T))`"*. The same sentence binds `Oct`. So
the earlier model quietly reproduced F4's shape — the exact defect §"What it does
not fix" says these candidates leave alone — while the surrounding prose claimed
the range had become "a property of `Oct`'s introduction". It had not.

**Eight `Bool`s have exactly 256 inhabitants, by the type.** There is no range
predicate, no `bytes-ok`, no boundary refusal and nothing for a backend to
reject: every value of `Octet` IS a byte, and every byte is a value of `Octet`.
The domain is exact rather than constrained, which is the difference between a
type that carries a fact and a type that carries a fact plus an obligation.

This passes `CLAUDE.md`'s guard directly — *"The test for a new language
capability is whether it has a STRUCTURAL MODEL"* — for the same reason `Str`
does: an ordinary inductive datatype, PROVABLE by structural induction, with a
native representation available to any backend that wants one.

**Two costs, stated because exactness moves work rather than removing it.**
Nothing carries an octet's numeric value any more, so every arithmetic use — a
UTF-8 continuation-byte test, an HMAC, a comparison — goes through an explicit
bit assembly, which is a definition someone writes and proves. And case analysis
on `Octet` splits into 256 cases if a proof takes all eight bits apart, where a
bounded `Int` would have offered one variable and a hypothesis. Structural
induction over `Bytes` is unaffected; it is the ELEMENT that got harder to reason
about numerically and impossible to get wrong.

No declaration in `examples/` or `apps/` carries `Bool` constructor fields at
all, so this shape collides with nothing the corpus declares — and §2.1's hygiene
obligation still applies against the STORE, which holds objects no `.oath`
declares.

### 2.3 Tagging PER ENCODING, and the one naming device in identity

**All three candidates are run below with tags that name an ENCODING, not a
role.** An earlier version of this file gave that refinement to B alone — B
carried `Utf8Bytes`/`OpaqueBytes` while A carried `{octets}`/`{codepoints}` and C
carried one `Bytes` — and then reported as B's advantage a property that
follows from the tagging rather than from B. §0's rule requires the opposite:
each candidate in its strongest form. §2.4 states what changes.

Supplying that refinement to A and to C is not symmetric work, and the asymmetry
is a genuine finding rather than a detail of spelling.

**Names are metadata everywhere in identity except ONE place.** §2's table: a
`data` Def encodes tyvar count, ctor count and per-ctor field types, with no type
name and no constructor names — so *"`Utf8Bytes`" and "`Latin1Bytes`" are not
facts a hash can see.* A record type (`Ty` tag `0x08`) encodes each field's NAME
as bytes. **A record field name is the only name in the language that enters
identity.**

Three consequences, in the order they bite:

- **For A the refinement is free and unbounded.** `{utf8 (List Int)}` and
  `{latin1 (List Int)}` are different types by construction, and any further
  encoding is another field name. Nothing is manufactured.
- **For C the obvious spelling ALIASES.** `(data Utf8Bytes [] (MkUtf8 Bytes))`
  and `(data Latin1Bytes [] (MkLatin1 Bytes))` encode identically — one ctor, one
  field of the same type — so they are ONE OBJECT and the "distinct datatype per
  encoding" is a single type with two names. This is §2.1's hazard arriving as
  the candidate's own central mechanism rather than as a collision with someone
  else's type.
- **So C must MANUFACTURE the distinction, and the material available is
  narrow.** A `data` Def encodes exactly three things — tyvar count, ctor count,
  and each ctor's field types — so a difference must be made out of one of them.
  That gives junk (a phantom tyvar, a dead constructor, a phantom field: carried
  in identity forever, one piece per encoding) or a field type that is already
  distinct, which bottoms out either in more junk or in **a record field name —
  A's mechanism, borrowed**. C is given the last below because it is the honest
  strongest form; this file does not claim the list is exhaustive, only that
  names are not on it.

**Which is worth stating as a result about the language and not about the
candidates: per-encoding type distinctness in Oath ultimately routes through a
record field name, because that is the only naming device identity carries.**

This reaches B too, since B's model is C's. If B's grouped Def hashes its members
as one canonically ordered unit, then two members with identical shape are
distinguishable BY POSITION and B gets the distinction without junk and without
records — **but that is an obligation the grouped form must state explicitly, not
a property it may be assumed to have.** A grouped form that does not say so
leaves B with exactly C's problem.

Settled by reading rather than by running: `Ty` tag `0x08` carries `str` name per
field (SPEC §1.2), `Def` tag `0x01` carries no names at all, and a record type is
legal as a constructor field type — `elabDataRaw` elaborates each field with the
general `parseTy`, which admits a brace (`oath/surface.go`).

### 2.4 What equal tagging changes, stated once and plainly

**Under encoding-specific tags, the ordinary wrong-decoder call is a TYPE ERROR
in A, in B and in C alike.** `latin1-decode` declares the Latin-1 type as its
argument; a UTF-8-tagged value has a different type; `tyEq` compares hashes; there
is no coercion and no subtyping anywhere in the checker (§2). This holds whatever
the tag is made of — a hashed field name, a manufactured shape, or an opaque
member of a group.

**So B does NOT uniquely prevent F3's original call-site mistake.** Any of the
three prevents it once the value at that site carries an encoding tag. Saying
otherwise credits opacity with what tagging did, and this file said otherwise
before this section existed.

**What survives the correction is one thing, and it is smaller and sharper.**

|  | wrong decoder, applied directly | explicit unwrap or retag |
|---|---|---|
| **A** | type error | **available** — `{latin1 (. r utf8)}`, a record literal over a projection |
| **C** | type error | **available** — `match` out of one wrapper, `ctor` into the other |
| **B** | type error | **forbidden** — `ctor` and `match` against the group are restricted |

**The same correction reaches one line further, and is made here rather than left
for a reader to find.** B's decoder is written `-> (Option Str)` while A's and C's
were written `-> Str`. Nothing about opacity produces that: a decoder that checks
well-formedness returns an option in any of the three, and *"malformed input has
no `Str` to become"* is a property of the decoder's RESULT TYPE, not of the
candidate. Read every `-> Str` below as available in option form to whichever
candidate wants it.

**And the escape in A and C is not a function someone has to write; it is a
LANGUAGE OPERATOR.** Record literals and `(. e f)` are unrestricted (SPEC §1.4;
`oath/check.go:434-447`), and constructor lookup requires only a data hash and a
valid index (§2). Nothing needs to be defined, exported, or noticed in review.
That is the whole of what separates the candidates on this axis — not whether the
mistake is possible, but whether undoing the tag is one operator away.

**One limitation is SHARED and belongs here rather than under any candidate.** A
tag survives only the operations the abstraction owns. The receiver SCANS and
SLICES its body, so under A and C the payload is projected out and everything
downstream of the scan is untagged again — the mistake returns at the scan's
output rather than at the boundary. B's answer is that scanning must happen
inside the abstraction, which is the abstraction-boundary question §1.4 records
and does not resolve.

---

# Candidate A — a record wrapper

    {utf8   (List Int)}
    {latin1 (List Int)}
    {opaque (List Int)}

Encoding-specific, per §2.3 — and A is the one candidate for which that costs
nothing IN THE LANGUAGE, because a record field name is already in identity. Any
further encoding is another field name, with no new declaration and no
manufactured structure.

**That cheapness does not extend to the boundary, and an earlier version of this
section assumed it did.** It said the body field "becomes `{opaque (List Int)}`,
or `{utf8 (List Int)}` where a contract determines it" — which is not a type. A
field has ONE static type, `Request` is one declaration with one identity
(§14.1a), and a field whose type varies per request does not exist. So A needs
exactly the sum §1.4 specifies:

    (data Body [] (Opaque {opaque (List Int)}) (Utf8 {utf8 (List Int)}))
    (data Request []
      (Req Str Str (List (Pair Str Str)) Body Int))

**A therefore pays §1.4's price in full: the same host classification against the
specification's media-type table, the same fork of `Request`'s identity under
`PROTO-TYPES-BY-IDENTITY`, the same work in both backends, the same §14.3
vectors, and the same migration.** **No candidate owes a SPEC §1 row** (§1.4's
table), so this is not a line where A is cheaper; what A has instead is that its
route to `hmac-sha256` — the projection `(. r utf8)` — is available at every
site rather than inside an abstraction. That is the defect stated as an access
rule, not a discount.

Everything below about projecting and retagging is unchanged by the sum: it
applies to the record inside the branch, which is as open as it ever was.

## The trace

Origin is boundary-supplied, under §1.4 and not otherwise: the host classifies,
constructs `(Utf8 {utf8 …})` or `(Opaque {opaque …})`, and the handler receives
one `Body`. **The mint is the host's, exactly as in B** — A's deficiency was never
about who could mint at the boundary, and §1.3's finding applies to A identically:
without §1.4 there is no boundary site and the tag is the program's.

**The conversion is no longer a free choice between two applicable functions.**
`utf8-decode : {utf8 (List Int)} -> Str` and
`latin1-decode : {latin1 (List Int)} -> Str` have DIFFERENT argument types, so at
a `{utf8 …}` value only one of them applies and the other is a type error (§2.4).
`[195,169]` still satisfies both readings; what has changed is that the value
now says which one was intended, and the checker enforces it.

## Who supplies the reading, and can it still be silently wrong

Authoritative at the origin **under §1.4, and nowhere without it**; and at the
conversion the author can no longer be silently wrong by picking the wrong
decoder — that is now a type error. What the author can still do is take the tag
off, and A supplies two ways with no definition written:

**The projection is one operator past the branch.** `(match b ((Utf8 r) (. r
utf8)) …)` yields the bare `(List Int)`, at which point `bytes-str` applies again
and F3's actual site is reachable exactly as before. The `match` is forced by the
sum and is not a guard: it costs the author a branch they were going to write
anyway, and the projection inside it is unrestricted. No diagnostic anywhere.

**The retag is the same operator twice**, and is why #159 named hidden
constructors as the load-bearing half of a newtype. Record literals and `(. e f)`
are unrestricted (SPEC §1.4; `oath/check.go:434-447`), so

    (defn retag [] [(t {utf8 (List Int)})] {latin1 (List Int)}
      {latin1 (. t utf8)})

is two lines, total, well-typed, and is `ρ` — the identity retag #159's `S` exists
to forbid — wearing a record. Nothing needs it to be a definition; the expression
is legal at any use site.

## Closes F3, or relocates the choice?

**Relocates it — and the relocation is further than this file previously
credited.** With encoding-specific fields the wrong decoder is refused by the
checker, which is a gain in SAFETY and not only in legibility; an earlier version
of this section said "a real gain in legibility and none in safety", and that was
an artefact of tagging A by role while tagging B by encoding.

What A does not do is SEAL. The tag is removable by a language operator, so a
tagged value means *someone last wrote this field name*, and no reader downstream
can distinguish a boundary-supplied tag from a retagged one.

## #159's exclusion test

A classifies `{utf8 v}`, a different value from `v` — the criterion's second
row, *the thing classified is no longer the bare list*. So A is **not refuted by
#159**, and **that is all it means**. The test is an exclusion test, not a
sufficiency test; `CLAUDE.md` names inverting it as "the overclaim this file keeps
having to correct". A is refuted below on its own evidence.

## Structural model and native representation

The payload stays `(List Int)`, so **no new inductive type is introduced** — A's
cheapest property and its weakest. There is nothing new to prove over. A backend
may lay a one-field record out as its field, so the wrapper can cost nothing at
run time; nothing requires it and no backend does it today. `Body` itself is an
ordinary two-constructor datatype, and that cost is identical in all three
candidates.

## Permanent obligation for a future backend

Record layout for a one-field record — already required. Plus **§14's
`PROTO-TYPES-BY-IDENTITY`**, which A now incurs unconditionally rather than "if
the wrapper reaches `Request`": a boundary-supplied tag REQUIRES the wrapper to
reach `Request`, through §1.4's sum. An entry point "is a handler because of the
IDENTITY of its argument and result types", so the body field's new type
re-identifies `Request`, and every conforming backend, compiled handler and §14
vector changes identity at once. A fork of the handler protocol, not an addition
to it — and the same fork B and C incur, not a lesser one.

## What it does not fix

F3 proper — narrowly: the direct wrong-decoder call is refused, and the same
mistake one projection later is not. **F5** (#167 — no Oath-level subject exists,
so no type supplies one); **F4** (#164 — SPEC §3 mandates unchecked `Str`
construction, untouched); **`R`** (#159's other obligation), which A could
discharge only if the retag were unavailable, and it is available.

## Falsifier

> **A separates the roles only if no total, well-typed function carries the
> payload from one wrapper to the other unchanged.**

**The condition already fires**, settled by reading the checker rather than by
running anything: `retag` above is such a function, and it needs no definition —
the expression is legal inline. A survives as a LABEL on a boundary-supplied
value and is refuted as a SEAL.

**Note what the falsifier does and does not now say.** It is about the SEAL, and
it fires. It says nothing about the wrong-decoder call, which A refuses (§2.4) —
so "A is refuted" must not be read as "A leaves F3 where it was".

---

# Candidate B — an opaque octet datatype

    (data Octet [] (Oct Bool Bool Bool Bool Bool Bool Bool Bool))
    (data Bytes [] (BNil) (BCons Octet Bytes))      ; §2.2, with construction and
                                                    ; matching RESTRICTED

Strongest form, and the one this file recommends CONDITIONALLY — on §1.4, whose
absence is §1.3's finding and §4's stated branch. Two refinements are part of the
candidate rather than embellishments on it:

- **The tag is per-ENCODING, not per-role.** `Utf8Bytes` and `Latin1Bytes` (and
  `OpaqueBytes`) are distinct opaque types over the same model. A role tag says
  *these are octets*, which no decoder needs to be told; an encoding tag says
  *these octets are UTF-8 text*, which is exactly the fact §1.1 shows the
  contract determines.
  **This refinement is NOT B's to claim.** §2.3 gives it to A and C as well, and
  §2.4 states what follows: the wrong-decoder call is a type error in all three,
  and it is the tagging that does that work, not the opacity. B's distinctive
  claim is only about the escape.
  **B must also say how its own per-encoding types stay distinct.** Its model is
  C's, so two same-shaped opaque types alias unless the grouped Def distinguishes
  members by POSITION (§2.3). If the grouped form does not state that, B inherits
  C's manufacture cost, and the honest strongest form of B is then C's
  declarations plus opacity.
- **The contract gate mints.** `Utf8Bytes` values exist only where an
  authoritative boundary applied §1.1's contract, and the sole way out is
  `utf8-decode : Utf8Bytes -> (Option Str)`.
  **This refinement is what §1.3 shows has no site under the current handler
  protocol**, and §1.4 is the change that would give it one. Everything below
  describing a boundary mint describes B *with §1.4*; B without it is the
  in-language form, and its own falsifier (i) fires.

## The trace

| step | with B | who supplies the reading |
|---|---|---|
| origin | the body arrives as `(Utf8 …)` or `(Opaque …)`, tagged by the host under §1.4's table | **the contract**, applied by the party that knows — *and only under §1.4; today this row does not exist and the body arrives as `(List Int)`* |
| admit | the receiver's own 415 gate, unchanged and still application-owned | the application, about its own contract — not about the encoding |
| scan | scanning is inside the abstraction and preserves the tag, or hands out `OpaqueBytes` | the library, once — and as a proof obligation, not a given |
| convert | `utf8-decode : Utf8Bytes -> (Option Str)` | **determined** — no other decoder accepts the type |
| use | `Str`, or the `None` branch the type forces | — |

## Who supplies the reading, and can it still be silently wrong

**The wrong-decoder call is a type error rather than a silent value** —
`latin1-decode` cannot be applied to a `Utf8Bytes`, since its argument type
differs, `tyEq` compares hashes, and there is no coercion anywhere in the checker
(§2). **This is shared with A and C under equal tagging (§2.4) and is not
evidence for B.** What is B's alone is that the tag cannot then be removed: there
is no projection and no `ctor`/`match` — but see §4: laundering does NOT need one,
because the permitted decoder plus an ordinary encoder recovers the octets. As
written this passage claimed the value cannot be laundered back to
`(List Int)` and re-read.

The two residual routes are the interesting part, and both convert silence into
noise:

- **The mint is wrong** — a sender declares `application/json` and transmits
  Latin-1. The tag then claims what the bytes are not. But `utf8-decode` returns
  `(Option Str)`, so malformed input has **no `Str` to become**; the program must
  branch on `None`. "Loud" here means *the type forces a branch*, not *something
  raises* — Oath has no exceptions, which makes the option-returning decoder the
  design rather than a concession. **Also not B's** (§2.4): A and C may return an
  option too.
- **A slice loses well-formedness** — the receiver scans and slices, and a cut
  mid-sequence yields tagged bytes that are not UTF-8. Same answer: the decode
  branches. **And this is the case A and C cannot reach** — not because they
  admit the wrong decoder, which they do not, but because only opacity forbids
  the escape: with a projection or a public constructor available, the payload
  comes back out as `(List Int)` and `bytes-str` applies, with no definition
  written and nothing to review.

So the honest statement is not *B cannot be wrong*. It is: **B removes the silent
failure and leaves a loud one**, which is the distinction #169 was opened about —
*nothing raises, and no property was written because nobody knew to write one.*
**And the credit is divided:** tagging removes the silent WRONG-DECODER failure
in any of the three (§2.4); opacity is what keeps the removal from being undone
one operator later.

## Closes F3, or relocates the choice?

**Closes it, conditional on §1.4 — and the condition is not satisfied today.**
The mint must be performed by the boundary rather than by an in-language
classifier (§1.2), and §1.3 shows the current handler protocol offers no boundary
site: the body arrives as `(List Int)` and every gate is program code downstream
of it. So the accurate statement has two branches, and only the first is closure:

- **With §1.4's protocol change**, the mint is the host's, F3's wrong decoder is
  a type error, and B closes F3.
- **Without it**, B's mint is author-supplied at the receiver's own gate. That is
  a single-site author assertion — still better than A and C, whose assertion is
  per-conversion and forgeable anywhere — and **it is not closure.**

## #159's exclusion test

B classifies by PROVENANCE — which definition produced the value — the criterion's
first row, *classification depends on something carried alongside the value*.
**Not refuted by #159, and not thereby supported.** B is argued for on §1.1's
contract and the minting axis, not on passing an exclusion test.

## Structural model and native representation

**B has a structural model: §2.2's inductive octet datatype.** It is the same
model C uses, provable the same way, and it satisfies `CLAUDE.md`'s guard on
its own terms. Opacity adds no representation and no semantics to values — it is a
**static authority rule** over who may write a `ctor` or a `match` against that
Def, discharged in the gate and **erased before any backend sees the program**.

**That erasure is a SPLIT, not an absence, and stating it as an absence would
overclaim.**

- **Opacity imposes no backend obligation.** It is discharged in the gate and
  gone; a backend compiles `Bytes` exactly as it would compile the transparent
  datatype, and never learns the type was restricted.
- **The structural model still costs every future backend a representation
  choice**, permanently, exactly as `Str` and `Set`/`Map` do. There are three
  honest discharges and only three: **run the inductive model unchanged**; **add
  a native byte buffer** — a packed buffer with view-based tails, by the argument
  already recorded at `oath/llvm.go:1746` for `Str`, and **strictly simpler than
  `Str`'s**: no decoding, and no refusal at all, because §2.2's octet type has
  exactly 256 inhabitants and every one of them is a byte. `Str` packs BY
  REFUSING because a `Str` element can be out of range; a packed `Bytes` has
  nothing to refuse and the pack is TOTAL in both directions; or **refuse the
  subset by name**, which `CLAUDE.md` requires be done "refused and named, never
  wrapped, replaced, or silently approximated".

**§3 does not tell you which of the three to take.** Whether the inductive model
is deployable at request scale depends on what eight `Bool` fields and a spine
node actually cost under each backend, and **that has not been measured** — §3
carries no number, and since §2.2's model is not `(List Int)` plus anything, no
comparison with the shipped representation is available either. So "run the
inductive model unchanged" is UNMEASURED rather than demonstrated to be unusable,
and the measurement is owed before a native representation is chosen. An earlier
version of this paragraph called the native buffer "practically mandatory" on
per-byte costs that were uncited and about a different structure.

## Permanent obligation — the KERNEL half, which is B's distinctive price

The backend half is above and is shared with C: a representation choice, on every
future backend, discharged three ways. What follows is the half only B carries.

**It is no longer the whole of B's distinctive price, and calling it that was the
error §1.3 corrects.** B costs TWO new normative structures, in two different
sections, with two different conformance surfaces: the visibility form below
(SPEC §1 and §2, reaching §10 and the blind Rust kernel) and §1.4's protocol
change (SPEC §14, reaching §14.3's vectors and both backends, and NOT reaching
§10 by §14.0). Neither substitutes for the other — visibility without §1.4 leaves
the mint with the program, and §1.4 without visibility leaves the tag forgeable —
so B's price is their sum. **Only the first is a PREMIUM, though:** §1.4 is owed
by A and C as well (§1.4's table), so what B pays over the other two is the
visibility form and nothing else.

**And the visibility rule is not only a restriction; it is B's ACCESS route.** A
group member may `ctor`/`match` against the group's own Defs, which is what lets
the octet assembly SPEC §1's crypto primitives need live inside the abstraction
while the boundary stays sealed — the reason no candidate owes a §1 change
(§1.4). A form that restricted construction without admitting privileged members
would not be a weaker version of B; it would be unusable, because nothing could
build a `Bytes` at all.

**Visibility must be part of IDENTITY, or it is not enforceable.** If it were
metadata it could be stripped by any store, a second kernel would have nothing
normative to implement, and §10 conformance could not reach it. Putting it in
identity has one immediate benefit — a transparent re-declaration of the same
shape is a *different object*, so the re-declaration leak that defeats any
metadata-level scheme does not arise.

**And one hard problem, which is the honest shape of the cost.** The Def must say
WHO may construct, and the natural answer — name the friend definitions by hash —
is **circular**: the friends mention the type, so the type cannot contain their
hashes. The workable form is a **grouped Def hashed as one unit** — a module
introduced as a new identity form, its members canonically ordered, its
visibility rule internal to the group. That is:

- a new tag and canonicalization rule in SPEC §1;
- a new gate rule in SPEC §2, implemented independently by `oathrs/` under the
  blind discipline;
- new §10 conformance surface and new golden fixtures;
- and the migration question every identity change carries.

**Nothing about that is cheap, and none of it lands on a backend** — the
opacity rule is erased before a backend sees the program, so what B adds here is
identity and visibility semantics in the KERNELS. Against `CLAUDE.md`'s guard
that is the good half of the trade: B does not smuggle in a primitive with no
structural model, and the only obligation it leaves a future backend is the
ordinary representation choice its structural model would cost anyway.

## What it does not fix

**F5** (#167) — a representation defect and a diagnostic defect, neither an Oath
value; no type reaches it, and `docs/experiments/issue-169.md`'s verdict on it is
unchanged. **F4** (#164) — `Str`'s scalar range remains SPEC §3 host discipline,
untouched. **What changed here is that B no longer REPEATS it:** with §2.2's
eight-`Bool` octet the new type has no range to enforce and no boundary
discipline to get wrong, where the earlier `(Oct Int)` model added a second type
with exactly `Str`'s problem. F4 is unfixed and no longer duplicated — a
narrowing, not a fix. **`R`** it does fix, structurally, since `ρ` has no
spelling.

## Falsifier

> **B closes F3 only if (i) the mint is performed by the boundary that applies
> the contract, not by an in-language classifier, and (ii) no total function
> reachable by a program — and no COMPOSITION of the abstraction's exports —
> RECOVERS a mint-tagged value's payload as `(List Int)` or `Str`, other than
> through the contract's decoder.**

**(ii) is a restatement, and the reason it is not a weakening is worth having
explicitly**, since `CLAUDE.md` requires a replaced assertion to pin its contract
at least as tightly. The earlier wording was *no total `Bytes -> Str`*. It is
wrong in both directions: it forbids `hmac-of : (List Int) -> Utf8Bytes -> (List
Int)` composed with `bytes-str`, which B must export and which hands back a
32-byte digest rather than the body; and it says nothing about a surface of small
exports — `length`, `nth` — that recovers the payload between them while no
single member has the forbidden type. The restatement drops the first and catches
the second. **The design fact doing the work in the first case is narrow and
worth stating as such:** a fixed-size result is not a payload VIEW — the export's
type is the same 32 bytes whatever the body was, so nothing downstream can read
the tagged octets out of it. That is a property of the export's shape, and it is
all (ii) needs; no claim about SHA-256 is made or required here.

Both are decidable by inspecting a proposed design rather than by a run.
**(i) FIRES AGAINST THE PROTOCOL AS IT STANDS, not only against a hypothetical
in-language design** — §1.3: §14.1a's `Request` carries a `(List Int)` body and
is fully constructed before any handler runs, so there is no boundary site to
mint at and the mint is the program's by construction. The consequence is
degradation to a single-site assertion rather than refutation, and the repair is
not a coding choice but §1.4's protocol change.
**(ii) does not fire on the shipped receiver's signature check**, which was the
one concrete case anyone had against it: the bit assembly `Bytes -> (List Int)`
is a PRIVILEGED member of the group, what leaves it is `hmac-of : (List Int) ->
Utf8Bytes -> (List Int)` — the key supplied by the caller, only the message
assembled inside — and the caller compares that digest with the received
signature using `bytes-eq-ct`, both already `(List Int)` in its own hands. The
shipped check is one substitution away from that shape: `webhook.oath:668-669`
reads `(bytes-eq-ct (hex-decode given) (hmac-sha256 (str-bytes secret) (req-body
r)))`, where the key comes from a `Str` capability and the signature from a
header, so only the third value — the body — is the one the group would hold.
The digest is fixed-size and so is not a view of the body (§1.4). It fires if
the abstraction exports anything that puts the payload back together — which is
the one thing opacity is for, and which the scan surface §1.4 records is the
open question about.

A third condition falsifies the FEASIBILITY rather than the design: if the
grouped-Def identity form cannot be specified without forking existing hashes,
B's price rises from "new normative section" to "encoding change", and
`CLAUDE.md`'s standing warning applies — *encoding changes fork reality*.

---

# Candidate C — a distinct (transparent) datatype

Same structural model as B (§2.2), public constructors, **one type per
encoding** — which is where C's cost appears, per §2.3:

    (data Utf8Bytes   [] (MkUtf8   {utf8   Bytes}))
    (data Latin1Bytes [] (MkLatin1 {latin1 Bytes}))

**The obvious spelling would not have worked, and the reason is C's own
mechanism.** `(data Utf8Bytes [] (MkUtf8 Bytes))` and
`(data Latin1Bytes [] (MkLatin1 Bytes))` are ONE OBJECT: constructor names are
metadata, so both encode as one ctor with one field of the same type. C's
distinctness has to be built out of STRUCTURE, and the only structural device
that carries a name is a record field — so the declarations above reach
per-encoding identity by borrowing A's mechanism. The alternative is a dead
constructor or a phantom field per encoding: junk in identity, forever.

## The trace, and who supplies the reading

Origin is boundary-supplied on the same terms as A's and B's — through §1.4's
sum, since `Request`'s body field has one static type and C's two encoding types
cannot both be it. C pays that table in full — and no candidate owes a SPEC §1
row (§1.4), C's route to the `(List Int)` primitives being a public `match` plus
an author-written bit assembly. **The conversion is not a free choice between
two applicable functions** — `utf8-decode` takes `Utf8Bytes`, `latin1-decode`
takes `Latin1Bytes`, and applying the wrong one is a type error, exactly as in A
and B (§2.4).

**What is free is minting and retagging, because `ctor` and `match` are
unrestricted.** Constructor lookup needs only a data hash and a valid index (§2),
so

    (MkLatin1 {latin1 (match r ((MkUtf8 x) (. x utf8)))})

is legal at any use site, needs no definition, and turns a boundary-established
`Utf8Bytes` into a `Latin1Bytes` nobody checked.

So even with a **host-performed mint**, C's tag means *someone claimed this*
rather than *the boundary established this* — and the two are indistinguishable
downstream. That is the precise sense in which C is A with a different spelling:
the encoding is in identity, the authority is not. Under equal tagging the
resemblance is closer than the earlier draft's, since C's per-encoding
distinctness is A's hashed field name wearing a datatype.

## Closes F3, or relocates the choice?

**Relocates.** The wrong decoder is refused by the checker — the same gain A
gets, and for the same reason (§2.4), which the earlier draft credited to B
alone. Not closure, because `ctor` and `match` put the tag back on any value at
any site.

## #159's exclusion test

The thing classified is no longer the bare list — criterion row two, not refuted.
#159 says so directly rather than by implication: its own note records that the
monomorphic-`Bytes` measurement "is a fact about datatypes and does not follow
from anything here, nor this from it."

## Structural model and native representation

§2.2's model, which is C's genuine strength and the reason it is not dismissed.
It is the pattern the project already uses and trusts —

> **PROVE OVER THE STRUCTURAL MODEL, RUN OVER A NATIVE REPRESENTATION**

— a third time after `Str` and `Set`/`Map`, and the easiest of the three: no
decoding, no refusal, a view-based tail by the recorded argument. **Easiest is
now literal rather than comparative**: `Str` and `Set`/`Map` both have inhabitants
their native form cannot hold — an out-of-range scalar, a duplicate — and §2.2's
octet type has none.

## Permanent obligation for a future backend

Pack/unpack for ONE more recognised type — the shared inner `Bytes` of §2.2: a
fourth in Go, which already resolves
`Str` (`oath/compile.go:559`) plus `Set` and `Map` (`oath/compile.go:383-384`),
and a second in LLVM, which resolves `Str` (`oath/llvm.go:5081`) — **and a TOTAL one, in both directions, with nothing to
refuse**, since every `Octet` is a byte and every byte is an `Octet` (§2.2). That is strictly less than what `Str` carries, and the earlier
version of this line said "element refusal by name (`0..255`) held to `Str`'s
standard", which the eight-`Bool` model makes unnecessary rather than cheap. Plus
a differential gate against `oath eval` matching
`TestCompileNativeSetDifferential`; and, since recognition is hard-coded per type
in BOTH backends, another special case rather than an instance of a general
mechanism. **In LLVM that is one more hard-coded recognition beside `Str`, not
the backend's first** — `strTypeHash` resolves `Str` by name and the hash then
drives `ctor` into a packed `T_STR` and `match` into `emitStrMatch`
(`oath/program.go:239`, `oath/llvm.go:455-456`, `oath/llvm.go:1110-1112`), which
is exactly the shape `Bytes` would take. An earlier version of this line said the
LLVM backend "recognises none", misreading `llvm.go:358` — that comment is about
having no native DEFINITION lowering (no Set/Map-style operation table, so
nothing is pruned from emission), not about type recognition. The correction cuts
the priced cost: there is a pattern to follow, and the part that has no seam in
LLVM is native OPERATIONS, not native types. Plus §14's
`PROTO-TYPES-BY-IDENTITY`, which C incurs unconditionally for the reason given
under A: a boundary-supplied tag requires the type to reach `Request` through
§1.4's sum.

**ONE recognition, not one per encoding — and an earlier version of this section
charged the latter.** It reasoned that since recognition is hard-coded by
name→hash, each encoding's wrapper is another entry in that table. It is not.
The encodings differ only in a one-field wrapper over the SAME inner type —
`(MkUtf8 {utf8 Bytes})`, `(MkLatin1 {latin1 Bytes})` — and what a backend gives
a native form to is `Bytes`. The wrappers are ordinary one-field constructors
over a one-field record and compile on the generic `ctor`/`match` path, carrying
whatever representation `Bytes` already has; nothing about them is resolved by
name. Settled by reading the seam rather than by running: `resolveNativeContainers`
resolves exactly `Set` and `Map` (`oath/compile.go:383-385`) and `strTypeHash`
exactly `Str` (`oath/program.go:239`, called once at `oath/compile.go:559`), and
every datatype outside those two tables takes the generic path. **So each further encoding costs a declaration in identity (§2.3's
borrowed record field name) and nothing in either backend's recognition table.**

**Which makes C's backend obligation IDENTICAL to B's, not larger.** B's model is
C's, so both pay exactly one recognition per backend that wants a native
representation — or none, under the "run the inductive model unchanged" discharge
B's section states. A pays none, because it introduces no structural model at
all. That is the whole of the backend axis, and it separates A from {B, C} rather
than B from C.

## What it does not fix

F3 proper — narrowly, in A's sense: the direct wrong-decoder call is refused and
the retag is not. **F5**, **F4**, and — unlike B — **`R`**, since the constructor
is public.

## Falsifier

> **C separates the ROLES only if no total, well-typed function mints one role's
> type from the other's payload.**

**The condition already fires**, by the same argument as A and with no definition
required: the `ctor`/`match` expression above. C's structural distinctness is real
and its authority is not, and the second is what F3 turns on.

**And as with A, this falsifier is about the SEAL.** It fires; it says nothing
about the wrong-decoder call, which C refuses.

*(The §2.1 spelling hazards are not this falsifier — they say only that two
particular shapes are taken. But §2.3's aliasing is not a spelling hazard for C:
it is C's own mechanism failing at its central job, and it is why the
declarations above are shaped as they are.)*

---

# 3. Representation evidence

**THIS SECTION CARRIES NO NUMBER, AND THAT IS THE FINDING RATHER THAN A GAP IN
IT.** An earlier version opened with per-byte costs for `(List Int)` under each
backend and scaled them to a resident size for a 1 MiB body held as §2.2's octet
datatype. Both are withdrawn, for two independent reasons, and either alone is
sufficient:

- **The per-byte figures had no producing artifact.** Nothing in this repository
  records the run that produced them, and this file's own front matter requires
  every measured number to be cited to the file that produced it. An uncited
  figure is not a measurement; it is a recollection. (The LLVM runtime carries a
  separate arena-bytes-per-octet estimate in a comment at `O_HTTP_BODYMAX`. It is
  a different number, derived for a different purpose, and it is NOT substituted
  here.)
- **They were about the wrong structure anyway.** They described `(List Int)` —
  the type §14.1a gives the request body and the type the shipped receiver
  actually holds. §2.2's model is a different structure, and since §2.2 became an
  eight-`Bool` octet it is not `(List Int)` plus anything: **no boxed `Int` per
  byte survives in it at all.**

**THE COST OF §2.2's MODEL IS WHOLLY UNMEASURED, IN MAGNITUDE AND IN DIRECTION.**
An earlier version of this section carried a `DERIVED` claim that §2.2 was
strictly more expensive per byte than `(List Int)`, on the reasoning that its
value contained everything `(List Int)`'s did plus one node per element. **That
reasoning died with `(Oct Int)`.** An eight-`Bool` octet and a boxed arbitrary-
precision `Int` are not comparable by inspection: one is eight fields of a
two-valued type, the other a heap-allocated bignum, and which costs more under
either backend is a question about representations this file has not measured and
must not guess at.

What remains true, and is all that remains: **both backends allocate per
CONSTRUCTOR** — in Go a heap `ctorV` plus a `[]any` of its fields
(`oath/compile.go:921`), in the LLVM runtime an `OVal` from `val(T_CTOR)` plus a
field array from `o_fields` (`oath/llvm.go:2223-2227`) — so §2.2 spends a `BCons`
node and an `Oct` node per byte, whatever eight `Bool` fields cost inside the
latter. That says the model allocates. It does not say how much, and it does not
rank the model against anything.

**What this establishes, narrowly.** That MEASURING THE ACTUAL MODEL is a
precondition of choosing a native representation for B or C, because nothing else
in this file bears on the question — and #99, where a definition OOM-killed the
registry at a 512Mi limit, is why that measurement is worth taking rather than an
efficiency footnote. It is not enough to price the pack/unpack obligation, to call
a native layout mandatory, to say at what body size the inductive model stops
being deployable, or to say whether §2.2's model is dearer or cheaper than what
the webhook runs today.

**What it does not establish.** It is not a safety argument — nothing about
representation cost bears on whether `[195,169]` carries its reading, and every
verdict in A, B and C stands unchanged at any cost. Nor is it an argument for the
type: a compile-time refinement to a native layout — the mechanism `Set` and `Map`
already use — would address any such cost with no type distinction at all.

# 4. Recommendation — WITHDRAWN

**There is no recommendation. B's seal does not hold, and B was the only
candidate that could have qualified under §0's rule, so this file recommends
NONE of the three.** What follows the withdrawal is left standing because the
price comparison is still correct and still useful; it is the CONCLUSION that
was wrong.

**THE ESCAPE, which is constructive rather than argued.** B's falsifier,
stated in its own section, fires on condition (ii) — *no total function
reachable by a program maps a mint-tagged value to `(List Int)` other than
through the contract's decoder*. This file exempted recovery "through the
contract's decoder" and treated the seal as intact. That exemption is fatal,
because the decoder's OUTPUT is an ordinary `Str` and re-encoding it needs no
privilege at all:

    host mints           Utf8Bytes [195,169]
    permitted decoder    -> Str  (SCons 233 SNil)
    utf8-encode          -> (List Int) [195,169]      <- ordinary definition
    bytes-str            -> "Ã©"                       <- F3, reproduced

Step three is not hypothetical: `utf8-encode` is written in Oath in
`docs/experiments/issue-169.md`, is unprivileged, and carries a PROVEN property
— `e-acute-is-two-bytes` — asserting exactly this equality. So the tag is
removable by composition, using only definitions the corpus already contains.

**AND THE DILEMMA IS GENERAL, WHICH IS THE RESULT WORTH KEEPING.** A sealed
byte type is useful only if some function takes it to `Str` — the receiver's
whole purpose is to read a repository NAME out of a body. Export that function
and the composition above removes the tag. Withhold it and the type cannot be
used for the task that motivated it. **Opacity restricts who may CONSTRUCT a
value; it cannot restrict what a caller does with a value it has legitimately
been given.** That is a property of the sealing mechanism, not of this
particular formulation, and it is why the withdrawal is not repaired by
tightening B's specification.

**What is NOT established by the withdrawal:** that no type distinction can
help. This refutes the SEAL argument — the one property B was recommended for.
The weaker benefits all three candidates share are untouched and are set out
in §2.4: given equal, encoding-specific tags, every candidate turns the
ordinary wrong-decoder CALL into a type error. That is a real gain and it is
not what this file recommended B for.

**WHAT FOLLOWS IS THE WITHDRAWN ARGUMENT, KEPT SO THE MISTAKE IS LEGIBLE.** It
reasons from a seal §4 has since disproved; read it as the case that WOULD have
held had the tag been unremovable. Two further overstatements inside it were
found by review and are corrected here rather than in place, because patching a
superseded argument sentence by sentence hides that the whole passage is
superseded:

- **§1.4 leaves no wrong-mint route** (against the claim near line 760). The
  host validates UTF-8 BEFORE minting and constructs `Opaque` on failure, so a
  malformed Latin-1 body never receives the tag; and if the bytes ARE valid
  UTF-8, the declared JSON contract makes the tag correct whatever the sender
  intended. The passage's "wrong mint reaches `utf8-decode`'s `None` branch"
  cannot occur under the protocol this file itself specifies.
- **A and C do not require a per-conversion assertion** (against the claim near
  line 792). Without §1.4 they can classify ONCE at the receiver gate and
  propagate an encoding-specific wrapper, which makes direct wrong-decoder calls
  type errors exactly as §2.4 describes. Their tags stay forgeable and
  removable — that part stands — but the assertion is not inherently repeated,
  and saying it was overstated B's advantage in the author-minted case.

**The margin is narrower than the earlier draft's, and the narrowing is the more
useful result.** §2.3 and §2.4 remove the comparison's asymmetry: given equal,
encoding-specific tags, all three refuse the ordinary wrong-decoder call, so what
B is recommended FOR is one property and one only — **the tag cannot be removed,
so the value's provenance survives to the point of use.** Against C every other
row of the price table below is identical, and against A the only further
difference is the structural model both B and C introduce. A reader deciding
whether B's price is worth paying should be pricing exactly that one property,
not the whole of the gap this file previously reported.

**And the PRICE side narrows with it. It is set out in full below rather than
netted off, because two of its rows were wrong in OPPOSITE directions and a
reader who was given only the difference could not see that.** Two costs this
file previously charged do not exist: B was charged a re-typing of SPEC §1's
crypto primitives (or an escaping octet view), and C was charged one native
recognition PER ENCODING. Both are withdrawn above, at §1.4's table and at C's
permanent-obligation section respectively.

| layer | A | B | C |
|---|---|---|---|
| **§1.4's protocol change** — SPEC §14.1a/§14.2/new §14 text, host classification, both backends, §14.3 vectors, `Request`'s identity and the corpus migration | **yes** | **yes** | **yes** |
| **SPEC §1's crypto primitives** | none | none — a privileged group member assembles the message, and `hmac-of : (List Int) -> Utf8Bytes -> (List Int)` returns a fixed-size digest the caller compares itself | none |
| **A new structural model (§2.2), hence a representation choice on every future backend** — ONE recognition where a backend takes one, or none under the inductive discharge | none — the payload stays `(List Int)` | **one** | **one** |
| **Per further encoding** | a record field name; nothing declared | a group member, distinguished by POSITION if the grouped form says so — otherwise C's cost | a declaration borrowing A's field name (§2.3) |
| **A new IDENTITY FORM in the kernels** — grouped Def with an internal visibility rule, SPEC §1 and §2, new §10 surface, new fixtures, implemented blind in `oathrs/` | none | **yes** | none |
| **buys a SEAL** | no | **no — WITHDRAWN, §4** | no |

**THE SEAL ROW READ `yes` FOR B UNTIL §4 WITHDREW IT, and the row is kept
rather than deleted because the withdrawal is the finding.** B's price is
unchanged and still correctly stated below; what changed is that it buys
nothing the other two do not, so nothing in this table now separates B from
C except cost. Wherever the prose below still reasons from B's premium,
read it as an account of what B WOULD have bought had the seal held.

**Read down the columns, not across the last row.** Three things fall out, and
the third is new:

- **B's premium over C is exactly one item: the grouped-Def identity form.**
  Every other row is identical between them, including the backend obligation,
  since B's model IS C's. That is a cleaner question than this file previously
  posed, and it is the one to decide.
- **B's premium over A is that form plus the structural model's representation
  choice.** A introduces no new inductive type, which is its one genuine
  cheapness and is unrelated to the escape.
- **C's premium over A buys nothing on the axis the file is deciding.** Both
  fail the seal, for the same reason and by their own falsifiers; C pays a
  backend recognition and a manufactured per-encoding declaration on top. What C
  gets for that is not authority but DOMAIN EXACTNESS — §2.2's octet has 256
  inhabitants by the type, where A's payload is `(List Int)` and carries a range
  obligation at every use (§2.2). Worth something, and not what F3 turns on.

**§1.4's cost is common to all three, and that is NOT a cancellation.** It does
not distinguish the candidates, but it does not drop out of the decision either:
it is the one row reaching both backends, §14.3's vectors and the corpus's
identity at once, it is a precondition of every column, and if it is refused none
of the three closes F3 — the comparison is then void rather than won by the
cheapest. **No size ranking is intended or available** (§5: no effort comparison
was made); what is claimed is reach and precedence. Stating it as "it cancels" would
hide a shared dependency behind an arithmetic that only ever runs on the
differences.

So the comparison a decision should actually run has two stages, in this order:
*is §1.4's protocol change acceptable at all?* — and only if it is, *is a tag
that cannot be removed worth one new identity form in the kernels?*

The reasoning, in the order it has to be read:

1. **The fact exists.** F3 occurs downstream of the receiver's own gate, which
   has already answered 415 to anything that is not `application/json`, and RFC
   8259 §8.1 requires UTF-8 for JSON interchange. The authority is that contract
   alone — not the HMAC, which covers the body and not the headers, and not the
   `charset` parameter, which the registration does not define (§1.1).
2. **All three refuse the wrong decoder, so that is not the reason to prefer
   any of them.** Under encoding-specific tags (§2.3) the ordinary call-site
   mistake is a type error in A, B and C alike (§2.4). An earlier version of this
   list said A and C "leave F3's exact mistake — the wrong decoder, applied
   silently — available", and that was false: it compared B's encoding tags with
   A's and C's role tags.
3. **What A and C cannot deliver is the SEAL — and it is not the MINT.** Given
   §1.4 all three receive a host-classified body, so the boundary supplies the tag
   in every candidate. The tag then comes off with a language operator — a
   projection in A, a `match` in C — needing no definition and leaving nothing to
   review, after which the payload is bare `(List Int)` and F3's actual site is
   reachable again. So a tagged value means *someone last wrote this*,
   indistinguishable downstream from *the boundary established this*.
4. **B cannot, and this numbered claim is WITHDRAWN (§4).** Its premise —
   "no way to remove it" — is false: the permitted decoder composes with an
   ordinary encoder to recover the octets. Stated as originally written: with
   an encoding tag minted only where the contract was applied AND no way to
   remove it, the wrong decoder is a type
   error, the payload cannot be laundered back, and mis-tagged bytes force a
   `None` branch. F3's silent failure becomes a loud one, which is the
   distinction #169 was opened about.
5. **The condition is that the mint be the boundary's, not the program's — and
   the protocol as it stands offers no boundary site.** §14.1a's `Request`
   carries a `(List Int)` body and is fully constructed before any handler runs,
   so the receiver's content-type gate is program code and B's mint, today, is
   the program's (§1.3). This is not a condition to watch for in a future
   proposal; it is unsatisfied now. §1.4 specifies the change that would satisfy
   it and prices it.

**So the recommendation is conditional in a stronger sense than "watch for the
cheap variant", and the branch where the condition is refused is stated rather
than left to be inferred:**

- **If §1.4's protocol change is undertaken**, B qualifies under §0's rule and is
  the only one of the three that does.
- **If it is not**, B does not close F3 and this file does not recommend it. What
  B is then is a labelled, unforgeable, single-site AUTHOR assertion: better than
  A and C on the same trace, and short of the standard §0 sets.

**Qualifying is not the same as being worth building, and this file prices B
rather than approving it.** Its cost is the SUM of two new normative structures:
a new identity form — a grouped Def with an internal visibility rule — in SPEC §1
and §2, in both kernels, with new §10 surface and new fixtures; **plus** §1.4's
handler-protocol change, which forks `Request`'s identity and lands on both
backends at once. **SPEC §1's primitives are not in that sum** — the earlier
draft put them there, and the privileged group helper is why they are not
(§1.4). Three things should be measured before this is committed:

- **The cheaper design that is not one of the three candidates.** If the host
  applies §3's ADMIT rule to a body whose content type determines its encoding,
  the body arrives as `Str` (or as a host-minted sum) and F3 as it happened
  cannot occur, because there is no byte list on that path left to misread. That closes the same failure with no new
  type — while inheriting a public constructor, so an author can still forge the
  text case, which is precisely the gap B's opacity closes. Named as the
  alternative to measure first, deliberately not recommended here: it is a §14
  change with its own protocol-identity cost.
  **§1.3 sharpens this bullet into the main reason to measure it.** This design
  and all three candidates need the SAME §14 boundary change — a host that applies
  a contract to the body before the handler sees it — so that cost is common
  across the whole field. **Common is not cancelled:** it is the largest item any
  of them carries and every one of them depends on it, so it decides whether
  there is a field at all before it stops separating the members. Once it is
  granted, what separates them is the kernel half, and the question becomes
  precise: **is the grouped-Def visibility form worth exactly what it buys over a
  host-validated but publicly-constructible arriving type?** That is one
  measurable comparison rather than several open-ended ones.
- **Whether the grouped-Def form can be specified without forking existing
  hashes.** If not, B's price moves into the class `CLAUDE.md` warns about
  directly.
- **Whether §1.4's protocol change is acceptable at all**, since it forks
  `Request`'s identity by `PROTO-TYPES-BY-IDENTITY`, splits §14.2's body row, and
  lands on both backends and §14.3's vectors at once. This is prior to the other
  two, and prior for every candidate: if the answer is no, no amount of
  kernel-side visibility work reaches F3, because the mint stays with the
  program. It no longer puts SPEC §1's two ADT-taking primitives in the path;
  that clause is withdrawn with the row that produced it.

**Recorded so the ground for this recommendation is not misread:** #169's own
falsifier did **not** fire. `docs/experiments/issue-169.md` closes F1–F4 and finds
F5 unclosable by any property, so the quantifier fails and #169 is not declined on
its stated argument. This recommendation rests on a different question — *who
supplies the reading* — applied to the candidate space #159 left open.

# 5. What this document does NOT establish

- **That any of the three candidates closes F3.** The recommendation is
  WITHDRAWN (§4): B's seal is defeated by composing the permitted decoder with
  an ordinary encoder, and B was the only candidate that could have qualified.
  What survives is the weaker, shared gain in §2.4 — equal tagging makes the
  wrong-decoder CALL a type error — and the observation that opacity governs
  CONSTRUCTION and cannot govern what a legitimate holder does next.
- **That the PRICE table is settled.** Review raised two objections to it that
  are recorded here unresolved, because the recommendation is withdrawn and
  refining the cost of candidates none of which is recommended would be work
  without a consumer. Both are real and a follow-up that revives any candidate
  must settle them first: (1) §1.4's helper accepts only `Utf8Bytes`, while the
  shipped receiver authenticates the body BEFORE its content-type gate — so the
  `OpaqueBytes` paths cannot perform the existing HMAC check, and the claim that
  the signature check compiles unchanged is false; an opaque-carrier helper is
  needed. (2) C's per-encoding distinctness does not have to route through a
  record field name — differing constructor counts, phantom type-variable counts
  or recursively distinct field shapes all yield distinct datatype identities,
  so presenting A's naming mechanism as necessary for C overstates C's cost.
- **That §1.4's migration can be made SAFE, as distinct from priced.** The
  protocol change moves `Request`'s body type without moving its `EntryShape`,
  so nothing in the current kernel forces a backend to notice: both would keep
  compiling and would keep constructing the old representation. An
  exhaustiveness mechanism covering the body transformation would have to be
  designed, and this file neither designs nor sketches one. It is named in
  §1.4's Go-backend row and it is the first thing an adoption would have to
  settle — before the type question, because a migration that cannot fail
  closed is a worse hazard than the conflation it repairs.
- **That B works, or that B was ever distinguished by WHO MINTS.** Under §1.4's
  protocol the host mints for A and C as well — §0's table and §1.4 both say so.
  B's only claimed difference was the seal, i.e. who may UN-mint, and §4
  withdraws that. What survives about B is a cost: an OPACITY change that is
  kernel-side, and a grouped-Def identity form whose specifiability is open.
  Read together with §4, that leaves B strictly more expensive than C for no
  demonstrated benefit.
- **That the authoritative mint is available.** It is not, under the handler
  protocol as §14 binds it (§1.3). §1.4 specifies a change that would supply it
  and prices the layers it touches; that specification has not been reviewed
  against §14's other rules, has not been implemented, and is normative nowhere.
  **Absent it, the mint is author-supplied in EVERY candidate and B's closure
  claim does not hold** — which is the first thing a follow-up would have to
  settle, ahead of the grouped-Def question. A and C are not exempt: `Request`'s
  body field has one static type, so their boundary tag needs the same sum and
  the same fork.
- **That §1.4's price is complete.** It names the layers a change to the body's
  type reaches; it does not claim to have found every rule in §14.2's table, in
  §14.2a's boundary or in §14.3's vectors that a tagged body would disturb.
- **That B is safer than A or C at the ordinary call site.** It is not. Under
  equal, encoding-specific tags all three refuse the wrong decoder (§2.4); B's
  claim is confined to the escape, and any wider reading of this file's
  recommendation is a reading of an earlier draft.
- **That C's per-encoding types are cheap.** §2.3's declarations are C's
  strongest form found here, not a proof that no cheaper distinct shape exists;
  what IS settled is that constructor and type names cannot supply the
  distinction, because identity does not carry them. **What is now settled the
  other way is where that cost lands:** in IDENTITY, one declaration per
  encoding, and NOT in either backend's recognition table, which takes the shared
  inner `Bytes` once and compiles the wrappers generically (C's permanent
  obligation). An earlier version charged a recognition per encoding.
- **That the corrected prices change any candidate's verdict.** They do not, and
  the direction matters: B got cheaper by one row and C got cheaper by one row,
  and neither is a verdict. A and C still fail their own falsifiers on the SEAL,
  which is a structural fact about who may write a projection or a `ctor` — no
  price reaches it. What the corrections change is the SIZE of the question a
  decision has to answer, not its answer.
- **That B's exports are safe as a class.** Only the two named here were
  examined: the contract's decoder, and `hmac-of : (List Int) -> Utf8Bytes ->
  (List Int)`, which §1.4's privileged helper makes possible. What is claimed for
  the second is only that a fixed-size digest is not a total payload view, so it
  does not expose the tagged body. Any further export has to be argued against
  falsifier (ii) on its own terms, including in composition with the others.
- **That B's grouped Def distinguishes same-shaped members.** §2.3 states it as
  an obligation the form must discharge. If it does not, B inherits C's
  manufacture cost.
- **That B's abstraction can export what an application needs.** `bytes-after`,
  `json-string-value` and `bytes-str` are application-local in the shipped
  receiver, and §1.4 records rather than resolves what a tagged body does to
  them. **The signature check is no longer part of this open question** — a
  privileged group member assembles the message, the caller supplies the key, and
  a fixed-size digest is what leaves — but the SCAN surface is, and it is the
  larger half: how much of a receiver's octet handling has to move inside the
  group is not measured here.
- **Any implementation estimate.** No effort comparison was made, and §3 supplies
  no cost figure to substitute for one.
- **ANY REPRESENTATION COST, FOR ANY OF THE MODELS DISCUSSED — IN MAGNITUDE OR
  IN DIRECTION.** §3 carries no number and no ranking. All it carries is that both
  backends allocate per CONSTRUCTOR (`oath/compile.go:921`,
  `oath/llvm.go:2223-2227`), so §2.2's model allocates a `BCons` and an `Oct` per
  byte. **Whether that is dearer or cheaper than the `(List Int)` the webhook runs
  today is unknown**: since §2.2's octet became eight `Bool`s there is no boxed
  `Int` per byte in it, so the two structures are not comparable by inspection.
  Earlier versions carried per-byte costs with no producing artifact anywhere in
  this repository, scaled them to a resident size for a structure they were not
  measured over, and then — after those were withdrawn — kept a directional claim
  that rested on the superseded `(Oct Int)` model. All of it is withdrawn (§3).
  The measurement is owed before either B or C chooses a native representation,
  and it is not owed by this file.
- **The `DERIVED` claim as a measurement.** One remains, in §2.1 — the colliding
  newtype spelling — and it carries the command that settles it. It was not run.
  §3's directional cost claim was also marked `DERIVED` and has been withdrawn
  outright rather than re-derived, because the model it reasoned about is no
  longer the model.
- **Anything about F5**, which remains a diagnostic and representation failure
  with no Oath-level subject, reached by no property and no type.
- **That §2.1's spelling hazards are exhaustive.** They are the two shapes this
  file happened to check. Structural distinctness is not owned, so any adopted
  model should be hashed against the committed store when it is adopted, and
  re-checked when the corpus grows.
