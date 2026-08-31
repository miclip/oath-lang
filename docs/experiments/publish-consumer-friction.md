# Friction: publishing a governed library, and delegating the right to publish

A ranked demand list produced by BUILDING the publisher's half of the flywheel,
not by reading the code. The sources are
`docs/experiments/publish-consumer/*.oath`; the instrument is
`docs/experiments/publish-consumer/run.sh`, which walks reserve -> publish ->
delegate -> consume -> refuse -> revoke as a falsifier (47 checks).

**Ranking is by how badly a real PUBLISHER is hurt** — the party who holds a key,
reserves a prefix, publishes a library and hands a machine the right to publish
into it. Consumer-side friction is out of scope; it is
`hydrate-consumer-friction.md`. Nothing is listed because it seemed plausible:
every item was hit while building, and each carries a transcript.

**Each demand is NAMED, not designed.** What one command or behaviour would
remove the friction is stated; the shape of it — flag, subcommand, protocol
change, message — is deliberately left open.

**Each item states TOOL LIMITATION or DESIGN GAP.** A limitation is something the
current tools do not do that the current design already permits. A gap is
something the design has no way to express, so no amount of CLI work reaches it.

## What the evidence is, and what it is not

Every transcript was taken by hand against a registry started as
`OATH_STORE="$REG" oath serve --http 127.0.0.1:29119 --public-reads` over a
throwaway `mktemp -d` store, in a cleared `OATH_*` environment, with three freshly
generated keys (holder / automation / stranger).
It is a LOCAL registry, not registry.oath-lang.org: the same code paths, but no
claim here is a measurement of the deployed service. Nothing in `codebase/` was
written.

Transcripts are literal captures, and every command line below is executable as
written given the variables its own section declares — the ranked items all use
the set below; the closing section's delegation block is from a separate capture
session and declares its own. The shell variables were set once in the capturing shell — `$D` the
sources, `$C` the keys and scratch files, `$URL` the loopback registry, and one
throwaway store each for the registry (`$REG`), the holder (`$PUB`), the delegate
(`$AUTOS`), a scratch publisher (`$F`) and the namespaced foundation (`$T2`):

```
$ D=$PWD/docs/experiments/publish-consumer
$ C=/tmp/cap
$ URL=http://127.0.0.1:29119
$ REG=$(mktemp -d)
$ PUB=$(mktemp -d)
$ AUTOS=$(mktemp -d)
$ F=$(mktemp -d)
$ T2=$(mktemp -d)
```

Where a block would print a `mktemp -d` path the normalization is shown as a pipe
in the command itself, never applied silently. Every `rc=` line is `oath`'s own
status, taken from a separate UNPIPED run of the identical command, because `$?`
after a pipeline is the last stage's. Nothing is elided by hand: no command below
contains an ellipsis.

Two kinds of evidence appear and they are worth different amounts:

- **ASSERTED** — `run.sh` checks the underlying STATE observation on every run,
  so it cannot silently stop being true. Named per item.
- **HAND-MEASURED** — captured once, reproduced here, and NOT re-checked on every
  run. `run.sh` does check some exit codes and some specific message substrings as
  part of its state assertions (it greps for `unknown type` / `unknown name` on the
  staged publish failures, and for `BLOCKED` and the delegation/revocation
  diagnostics), so those particular facts are guarded. What is hand-measured is the
  FULL wording of every transcript below, and the exit code and message of any
  command `run.sh` does not itself run — most sharply `put --remote` (item 2), which
  it never invokes. A per-item "Evidence class" line names which is which.

---

## 1. A grant covers the whole prefix, and there is no narrower unit — DESIGN GAP

**Worst.** It is the only item where the safe action is unavailable rather than
awkward: a publisher who wants a build machine to publish one name has no way to
say so.

The entire vocabulary, from the usage line:

```
$ OATH_STORE="$PUB" oath delegate
error: usage: oath delegate <namespace>/* --to <pubkey> [--key <file> | --kms-key <res>] [--remote <url>]
```

One subject, one prefix. No name, no pattern, no subset of any kind — the prefix
is the only unit the command can express. The statement that is signed carries no
field a narrower one could travel in either:

```
$ OATH_STORE="$PUB" oath delegate 'michael/*' --to "$(cat $C/automation.pub)" --remote "$URL" --key "$C/holder.key" --dry-run 2>&1 | sed -n '/EXACT BYTES/,/^$/p'
EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):
  | oath-delegate/2
  | op=delegate
  | namespace=michael/*
  | subject=946bbd9180d3bff23aa26fd9912103c0b9d52d0d97d19e5273d12f6c98563942
  | authority=60eda29b809cde3a2d249a72199fa1a0f7cf72b43b490cf794d693cac50f9af0
  | authority_rev=1
  | delegation_rev=0
  | pubkey=60eda29b809cde3a2d249a72199fa1a0f7cf72b43b490cf794d693cac50f9af0
```

What the holder is told they are agreeing to:

```
$ OATH_STORE="$PUB" oath delegate 'michael/*' --to "$(cat $C/automation.pub)" --remote "$URL" --key "$C/holder.key" -y 2>&1 | sed -n '/^DELEGATED/,$p'
DELEGATED michael/*
  subject:  946bbd9180d3bff23aa26fd9912103c0b9d52d0d97d19e5273d12f6c98563942
  holder:   60eda29b809cde3a2d249a72199fa1a0f7cf72b43b490cf794d693cac50f9af0  (authority is UNCHANGED — this grants permission, not ownership)
  at authority revision: 1

Keys that may now publish under michael/* (besides the holder):
  946bbd9180d3bff23aa26fd9912103c0b9d52d0d97d19e5273d12f6c98563942

Each may bind names under this prefix and nothing else. None may reserve,
delegate onward, or revoke. The holder may withdraw any of them at any time.
```

"Each may bind names under this prefix" is exact, and it is the problem. The
automation key in this experiment exists to publish ONE definition,
`michael/item-report`. Measured, it can bind anything:

```
$ cat "$C/unrelated.oath"
(defn billing-credentials [] [(n Int)] Int
  (+ n 1)
  (prop increments [(n Int)] (== (billing-credentials n) (+ n 1))))

$ OATH_STORE="$AUTOS" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$C/unrelated.oath" 2>&1 | grep -E '^(.{0,3}michael|error|all )'
✓ michael/billing-credentials #b85da16ad31d  tested (200 cases per property) · total
all 1 definitions published under michael.

$ grep -o '"michael/[a-z-]*"' "$REG/names.json" | tr '\n' ' '
"michael/billing-credentials" "michael/count-items" "michael/decimal" "michael/numbered-lines" "michael/text-join"
```

The withdrawal half of the promise is real and was measured: revocation works and
the registry enforces it on the next publication (see the closing section). What
is missing is a grant SMALLER than the prefix. Names are permanent and there is no
unbind, so anything a delegate binds before a revocation stays bound.

**Publisher hurt: anyone automating publication.** A CI runner, a release bot, a
second contributor. The measured cost is that the smallest grantable unit of trust
is "every name under my prefix". The holder's alternative is to put the signing
key on the build machine, which is worse.

It is a GAP: the envelope above is the whole statement, and it has no field a
narrower grant could be expressed in, so there is nothing for a CLI flag to
carry.

**DEMAND: a grant narrower than the reservation.** Whether that is an exact name,
a sub-prefix, or an enumerated set of names is a design question. What matters is
that "this key may publish `michael/item-report` and nothing else" becomes
expressible at all.

**Evidence class:** the grant, the delegate's successful publication of
`michael/item-report`, and the delegation revision moving 0 -> 1 while the
authority revision stays 1 are ASSERTED (`run.sh` §5, §6). The
`michael/billing-credentials` publication, the envelope bytes, the usage line and
every message text are HAND-MEASURED.

---

## 2. `put --remote` cannot create a name, misdirects, and reports success — TOOL LIMITATION

**Second, and the easiest of the five to trip.** It is the command
whose own source describes cold-registry publication, and against a cold registry
it fails while reporting success.

The first thing a publisher tries, with a key, on an empty registry:

```
$ OATH_STORE="$PUB" oath put --remote "$URL" --key "$C/holder.key" "$D/base-list.oath"
error: new names require a signed principal. Bearer authorization grants SERVICE ACCESS, not NAME OWNERSHIP.
  A token authorizes use of this registry; a key establishes who you are and what you may govern.
  To publish "List": generate a key (`oath keygen`) and publish with `--key`, or use a key delegated
  to you under a reserved namespace (`oath delegate`). Everything short of creating a name — search,
  evaluate, prove, and preparing the publication itself — still works with the token alone.

$ OATH_STORE="$PUB" oath put --remote "$URL" --key "$C/holder.key" "$D/base-list.oath" >/dev/null 2>&1; echo "rc=$?"
rc=0
```

The multi-file form, which exists for exactly this case, behaves the same and
binds nothing:

```
$ OATH_STORE="$PUB" oath put --remote "$URL" --key "$C/holder.key" "$D/base-list.oath" "$D/base-str.oath" >/dev/null 2>&1; echo "rc=$?"
rc=0
$ cat "$REG/names.json" 2>/dev/null || echo "(names.json absent - nothing bound)"
(names.json absent - nothing bound)
```

Three defects in one command, and they compound:

- **It advises the flag that was passed.** `--key` was given. The repair the
  publisher needs is `oath publish`, which the message never names. The gate
  (`oath/mcp.go`, `hosted && auth == nil`) requires a publication ENVELOPE, and
  `remotePut` signs the HTTP request but sends no envelope, so a keyed
  `put --remote` can never create a name however the key is supplied.
- **It exits 0.** `remotePut` (`oath/remote.go`) decodes `result.content` and never
  reads `result.isError`, which `mcpCall` and `mcpCallSignedBy` in the same file
  both do. A tool-level refusal returns as ordinary output, so `cmdRemotePut`
  prints it and returns normally. A publishing script sees success.
- **The message has no trailing newline**, so the next prompt or line of output
  runs onto it. The last twelve bytes, against a refusal from the same session
  that does end in one:

```
$ OATH_STORE="$PUB" oath put --remote "$URL" --key "$C/holder.key" "$D/base-list.oath" 2>&1 | tail -c 12 | od -c
0000000    t   o   k   e   n       a   l   o   n   e   .
0000014
$ OATH_STORE="$PUB" oath publish --remote "$URL" --key "$C/holder.key" -y "$C/foundation.oath" 2>&1 | tail -c 12 | od -c
0000000    t   r   a   n   s   i   t   i   o   n   s  \n
0000014
```

`main.go` explains that the command "takes several files because publication order
is load-bearing on a cold registry", and on a genuinely cold registry every name is
new, so the feature cannot be used for its stated purpose.

**Publisher hurt: anyone scripting publication, and anyone bootstrapping.** The
measured cost is a publication that did not happen, reported as success, to a
caller that has no other signal.

It is a LIMITATION, and the smallest of the five to fix.

**DEMAND: `put --remote` fails when the registry refuses, and its refusal names the
operation that would succeed.** Whether the command later gains an
envelope-carrying mode is a separate design question; the exit code is not.

**Evidence class:** entirely HAND-MEASURED. `run.sh` does not invoke `put --remote`
anywhere — it publishes through `oath publish` — so nothing in this repo currently
guards the exit code, the message, or the missing newline.

**RESOLVED** (#2). `remotePut` now reads `result.isError`, so a refused publication
returns a Go error and `put --remote` exits nonzero instead of printing the refusal
and exiting 0 — and because the error then goes through `fail()`, the message regains
its trailing newline (the third defect fell out of the first). The refusal itself
(`bearerRefusal`) now names the command that succeeds: `oath publish --key <file>`,
with an explicit note that `oath put --remote` sends a signed request but no
publication ENVELOPE, so it cannot create a name. A Go test
(`TestRemotePutSurfacesToolRefusal`) pins both directions — a refusal errors, a
success does not — and is negative-controlled against removing the check.

---

## 3. Publishing a dependent definition requires fetching your own names back — TOOL LIMITATION

**Third.** It blocks nothing permanently — one extra command clears it — but every
publisher hits it the first time they split a library from an app, and the error
does not say what to do.

`oath publish` elaborates against the LOCAL store, and a namespaced publication
binds `michael/x`, so a dependent whose source names `michael/x` cannot be
published from the store that produced it. Four arms, the same command each time,
only the local store differing.

Arm 1, an empty store:

```
$ OATH_STORE="$F" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$D/item-report-app.oath" 2>&1 | tail -1
error: this closure does not elaborate: line 1: unknown type "Str"

$ OATH_STORE="$F" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$D/item-report-app.oath" >/dev/null 2>&1; echo "rc=$?"
rc=1
```

Arm 2, after putting the foundation locally:

```
$ OATH_STORE="$F" oath put "$D/base-list.oath" --new >/dev/null 2>&1
$ OATH_STORE="$F" oath put "$D/base-str.oath" --new >/dev/null 2>&1
$ OATH_STORE="$F" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$D/item-report-app.oath" 2>&1 | tail -1
error: this closure does not elaborate: line 2: unknown name "michael/text-join"

$ OATH_STORE="$F" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$D/item-report-app.oath" >/dev/null 2>&1; echo "rc=$?"
rc=1
```

Arm 3 is the point. The library is put locally too, from the same source the
holder published:

```
$ OATH_STORE="$F" oath put "$D/item-report-library.oath" --new >/dev/null 2>&1
$ grep -o '"[a-z-]*"' "$F/names.json" | tr '\n' ' '
"count-items" "decimal" "numbered-lines" "text-join"
$ OATH_STORE="$F" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$D/item-report-app.oath" 2>&1 | tail -1
error: this closure does not elaborate: line 2: unknown name "michael/text-join"
```

The store holds the objects already, and they are the published ones:

```
$ grep '"text-join"' "$F/names.json" | grep -oE '[0-9a-f]{64}'
301348788bfcbc5ef62cd31421ed735197463bb343543721db72f44804df3850
$ grep '"michael/text-join"' "$REG/names.json" | grep -oE '[0-9a-f]{64}'
301348788bfcbc5ef62cd31421ed735197463bb343543721db72f44804df3850
```

Nothing is missing except a BINDING. Arm 4 — the publisher round-trips through the
registry to obtain a name for an object already on disk:

```
$ OATH_STORE="$F" oath resolve "$D/item-report-app.oath" --remote "$URL" -o "$C/f.lock" 2>&1 | tail -1
resolved and fetched 6 external name(s) across 6 object(s) in the closure -> /tmp/cap/f.lock
$ OATH_STORE="$F" oath publish --remote "$URL" --key "$C/automation.key" --namespace michael -y "$D/item-report-app.oath" 2>&1 | grep -E '^(.{0,3}michael|all )'
✓ michael/item-report #5f21bdcacf6b  tested (200 cases per property) · total
all 1 definitions published under michael.
```

That `resolve --remote` binds names as well as writing the lock is what keeps this
to one command; it is not in the command's own description and was found by
inspecting the store afterwards.

**Publisher hurt: anyone whose second definition depends on their first.** The
measured cost is a network round trip to learn a name mapping the publisher
authored, plus a diagnosis: the message names `michael/text-join` but says nothing
about `--namespace`, about the local store, or about the identical object being
present under another name. An offline publisher cannot proceed at all.

It is a LIMITATION: `--namespace` is already a client-side source transformation,
so the mapping bare -> qualified is fully known locally at publish time; nothing in
the design prevents the local store from being left in the state the publication
implies.

**DEMAND: publishing under a namespace leaves the publisher able to build on what
they just published, without a fetch.** Whether that is `publish` binding the
qualified names locally, a `--namespace` that applies to reads too, or an explicit
"adopt what I published" command is a design question.

**Evidence class:** arms 1 and 2, and that neither moved the registry, are
ASSERTED (`run.sh` §6, two staged checks); the delegate's subsequent successful
publication is ASSERTED. Arm 3 — the identical-object-under-a-bare-name case,
which is the sharpest evidence — is HAND-MEASURED only, as are all message texts
and exit codes.

---

## 4. A compilable CLI entry needs an ADDITIONAL binding of `Str` under that bare name — DESIGN GAP

**Fourth.** Narrow, and precisely locatable: a namespace holds types perfectly well
— literals, annotations and structural matching all work — but the entry protocol
resolves the string type by the bare name `Str`, so a store must carry that
binding in ADDITION to whatever else it calls the type.

A namespaced foundation puts and yields the corpus's own hashes:

```
$ OATH_STORE="$T2" oath put "$C/ns-foundation.oath" --new 2>&1 | tail -2
✓ michael/List     #fa452d59a235  data (2 constructors)
✓ michael/Str      #e6bbed8bc934  data (2 constructors)
```

A definition typed entirely against those names elaborates, and its string literal
typechecks against `michael/Str` — so nothing about namespaced types is broken:

```
$ cat "$C/ns-entry.oath"
(defn ns-report [] [(args (michael/List michael/Str))] michael/Str
  "ok"
  (prop constant-output [(args (michael/List michael/Str))]
    (== (ns-report args) "ok")))
$ OATH_STORE="$T2" oath put "$C/ns-entry.oath" --new 2>&1 | tail -2
✓ ns-report        #9488d56d9d7a  tested (200 cases per property) · total
    prop constant-output          passed 200 cases
```

Two arms over ONE store, differing only in whether the bare name `Str` is ALSO
bound to the object `michael/Str` is already bound to. Arm 1:

```
$ OATH_STORE="$T2" oath build ns-report -o "$C/nsbin" 2>&1 | tail -1
error: ns-report : (-> (#fa452d59a235 #e6bbed8bc934) #e6bbed8bc934) — entry protocol requires (-> (List Str) Str), (-> (List Str) Result) with Result = (Ok Str | Fail Int Str), (-> Request Response), or any of these with a leading {caps} record
$ OATH_STORE="$T2" oath build ns-report -o "$C/nsbin" >/dev/null 2>&1; echo "rc=$?"
rc=1
```

Arm 2 adds the second binding and changes nothing else — not the definition, not
its hash, not the object:

```
$ python3 -c 'import json,sys; p=sys.argv[1]+"/names.json"; n=json.load(open(p)); n["Str"]=n["michael/Str"]; json.dump(n,open(p,"w"),indent=2)' "$T2"
$ cat "$T2/names.json"
{
  "michael/List": "fa452d59a2358fadba69764616745354a1e34465079c2a63e5f4c5d56f2baf05",
  "michael/Str": "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
  "ns-report": "9488d56d9d7a36de2b7ec0f85ff2bb51bdec5bcd8cc8fbef39b9ccaa3999fbab",
  "Str": "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588"
}
$ OATH_STORE="$T2" oath build ns-report -o "$C/nsbin" >/dev/null 2>&1; echo "rc=$?"
rc=0
```

`List` needs no such binding — it is matched against the canonical prototype hash,
which `michael/List` satisfies. Only `Str` is resolved by name.

**Publisher hurt: anyone publishing an executable program under a namespace.** The
measured cost is that a consumer's store must carry a bare `Str` binding for the
program to build, so the publisher's namespaced closure is not self-sufficient:
something outside every reservation must also be bound, by someone, to the right
object.

Note also what the error says: it prints the entry's type as hashes
(`(-> (#fa452d59a235 #e6bbed8bc934) #e6bbed8bc934)`), so the one fact that would
explain the failure — that no `Str` name resolves — appears nowhere in it.

It is a GAP: the recognizer is `strTypeHash(st)`, a by-name resolution in
`oath/program.go`, and the language has no other way to designate the string type.
`isHandlerEntry` went the other way (structural, per SPEC §14.1a) after a by-name
check proved wrong in both directions.

**DEMAND: entry recognition identifies the string type without requiring a
particular bare name to be bound.** Whether that is a canonical `Str` prototype
hash (as `List` already has), a declaration on the definition, or a store-level
designation is a design question.

**Evidence class:** entirely HAND-MEASURED. `run.sh` publishes the foundation bare
and asserts it lands with the corpus's hashes; nothing in it exercises a
namespaced foundation or the two-arm build.

**RESOLVED** (item 4). `strTypeHash` no longer resolves `Str` only by name: when the
store binds no `Str`, it falls back to the canonical Str CONTENT HASH (`protoStr` —
the same identity `isStrTy` and both backends already use, and the structural
recognition `List` has via `protoList`). So a program typed entirely against a
namespaced `michael/Str` (byte-identical, same hash) now builds and runs with no bare
`Str` bound — verified: the two-arm scenario above needs no second binding, and
`ns-report` keeps its hash (`#9488d56d9d7a`), so this is build-time recognition, not
identity. Recognition and lowering share the one hash, so they cannot diverge. A store
that DOES name a `Str` still governs — the bound name wins over the fallback — which
preserves #184 (a str-map keyed on a superseded-by-name Str stays inadmissible). A Go
test (`TestEntryRecognisesStrWithoutBareName`) pins both the fallback and the
bound-name-wins case, negative-controlled. What remains unaddressed is the secondary
note above — the entry-shape error still prints types as hashes.

---

## 5. There is no way to publish several new BARE names in one operation — TOOL LIMITATION

**Fifth.** Ergonomic, and it lands hardest on the cold start, when everything is new.

A signed publication covers exactly one name transition, which is a deliberate
protocol rule:

```
$ cat "$C/foundation.oath"
(data List [a]
  (Nil)
  (Cons a (List a)))
(data Str []
  (SNil)
  (SCons Int Str))

$ OATH_STORE="$PUB" oath publish --remote "$URL" --key "$C/holder.key" -y "$C/foundation.oath" 2>&1 | tail -1
error: signed publication takes exactly one definition per envelope, found 2: a single signature must not cover several independent name transitions

$ OATH_STORE="$PUB" oath publish --remote "$URL" --key "$C/holder.key" -y "$C/foundation.oath" >/dev/null 2>&1; echo "rc=$?"
rc=1
```

`--namespace` lifts this — it publishes a whole closure as one envelope per name,
in dependency order — but only for names being namespaced. For BARE names there is
no batch path, and by item 2 the other multi-file command binds nothing at all.

**Publisher hurt: anyone bootstrapping a registry, and anyone publishing a bare
library.** The measured cost is one file and one command per definition, in
hand-maintained dependency order, for names that cannot be namespaced — which by
item 4 includes the string type every CLI program's entry needs bound.

It is a LIMITATION: `cmdPublishClosure` already does exactly this work — topo sort,
one signed envelope per name, confirm-and-bind before each dependent — and is
reachable only when a namespace is supplied. The one-signature-per-transition rule
is not what blocks it; that rule is preserved by the namespaced path.

**DEMAND: one operation that publishes a multi-definition closure of NEW bare
names, one signature per name.** Whether that is `publish` accepting a batch
without `--namespace`, an explicit `--closure`, or a bootstrap command is a design
question.

**Evidence class:** `run.sh` publishes the two foundation datatypes as two separate
one-definition files and asserts both land — it works AROUND this item rather than
measuring it. Everything above is HAND-MEASURED.

---

## What is NOT friction, recorded so it is not re-derived

- **An unauthorized signed delegation IS adjudicated by the registry, and the
  refusal is journalled.** The CLI declines to sign such a statement before
  sending it, which is a courtesy, not the enforcement point — and an earlier
  draft of this document wrongly ranked that preflight as a design gap. Measured
  by building a `delegate` envelope by hand, signing it with the delegate's key
  outside the CLI, and POSTing it.

  This block is from a SEPARATE capture session with its own registry and its own
  three keys, so its hashes do not match the ones above; `$C`, `$URL` and `$REG`
  are that session's. `$AUTO` and `$STR` are the public keys `oath keygen` wrote
  beside the private ones, and the two files the `curl` reads are produced by the
  script shown first:

  ```
  $ AUTO=$(cat "$C/automation.pub")
  $ STR=$(cat "$C/stranger.pub")
  $ python3 - <<PY
  import base64, json, binascii
  from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
  raw = binascii.unhexlify(open("$C/automation.key").read().strip())
  sk = Ed25519PrivateKey.from_private_bytes(raw[:32])
  env = ("oath-delegate/2\n"
         "op=delegate\n"
         "namespace=michael/*\n"
         "subject=$STR\n"
         "authority=$AUTO\n"
         "authority_rev=1\n"
         "delegation_rev=1\n"
         "pubkey=$AUTO\n").encode()
  body = json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delegate",
          "arguments":{"envelope": base64.b64encode(env).decode(),
                       "signature": sk.sign(env).hex()}}}).encode()
  open("$C/body2.bin","wb").write(body)
  open("$C/tsig2.txt","w").write(sk.sign(body).hex())
  PY
  ```

  The envelope names the signer as the authority, which is what makes it
  WELL-FORMED and lets it reach the holder check. (Signing one that names the real
  holder as `authority` while the `pubkey` is the delegate is refused earlier, as
  `malformed delegation`, and is NOT journalled — measured, and worth knowing
  before reading the journal as a complete record of attempts.)

  ```
  $ curl -s -X POST "$URL/mcp" -H 'Content-Type: application/json' \
      -H "X-Oath-Pubkey: $AUTO" -H "X-Oath-Signature: $(cat $C/tsig2.txt)" \
      --data-binary "@$C/body2.bin"
  {"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"error: namespace \"michael/*\" is held by 1f191f4d1004, not by the signer a8dfe6721c4a: only the current holder may grant or revoke","type":"text"}],"isError":true}}

  $ tail -1 "$REG/log.jsonl" | python3 -c 'import json,sys; e=json.load(sys.stdin); print(json.dumps({k:e[k] for k in ("kind","status","name","author","error")}, indent=1))'
  {
   "kind": "delegate",
   "status": "rejected",
   "name": "michael/*",
   "author": "a8dfe6721c4a4fed25d927b3a0dc797b0f1421e28bcfb58e2cf1e3f4054c59ee",
   "error": "namespace \"michael/*\" is held by 1f191f4d1004, not by the signer a8dfe6721c4a: only the current holder may grant or revoke"
  }
  ```

  `apiDelegate` (`oath/delegate.go`) refuses on the holder check and preserves the
  envelope, signature and author in the journal — deliberately, for the same
  reason a blocked publication keeps its object. So the artifact an incident
  review needs exists. What this leaves is INSTRUMENT coverage: `run.sh` §7
  exercises only the CLI preflight, so the server path above is hand-measured and
  unguarded.

- **Revocation is enforced by the registry.** After the holder revokes, the same
  key, same store and same prefix is refused server-side, and the delegation
  revision advances while the authority revision does not. ASSERTED (`run.sh`
  §11).

- **A blocked publication stores its object and journals `blocked`.** The name does
  not move and the attempt survives as evidence. ASSERTED (§10).

- **`resolve --remote` hydrates the store it runs in.** Surprising for a command
  that advertises a lockfile, and it is what reduces item 3's workaround to one
  command. ASSERTED (§6, §8), not complained about.

- **Namespacing is identity-neutral.** Each `michael/*` object is byte-identical to
  a bare put of the same source, and the foundation datatypes reproduce the
  committed corpus's hashes from scratch. ASSERTED (§3, §4). This is what makes
  items 3 and 4 friction about NAMES rather than about objects.
