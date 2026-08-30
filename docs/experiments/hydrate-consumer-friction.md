# Friction: the fresh-machine clone over `oath hydrate`

A ranked demand list produced by BUILDING the loop, not by reading the code. The
consumer is `docs/experiments/hydrate-consumer/checklist.oath`; the instrument is
`docs/experiments/hydrate-consumer/run.sh`, which walks publisher -> registry ->
fresh machine -> binary as a falsifier (22 checks).

**Ranking is by how badly a real fresh-machine clone is hurt**, not by how hard
each is to fix. Only three items, all met while building; nothing is included
because it seemed plausible.

**Each demand is NAMED, not designed.** What one command or behaviour would
remove the friction is stated; the shape of it — flag, subcommand, default,
protocol change — is left as a design question, deliberately.

## What the evidence is, and what it is not

Every transcript below was taken by hand against a real `oath serve --http`
registry over a throwaway store extracted from `codebase@HEAD`, in a cleared
`OATH_*` environment. It is a LOCAL registry, not registry.oath-lang.org: the
authentication and fetch paths are the same code, but no claim here is a
measurement of the deployed service.

Transcripts are literal captures. Where a block would otherwise print a
`mktemp -d` path or the whole corpus, the normalization is shown as part of the
transcript, never applied silently — either as a pipe in the command itself, or
by capturing `oath`'s output to a file and filtering it in a following command.
The second form is used wherever an exit status is quoted, because `$?` after a
pipeline is the LAST stage's status: `oath … | sed …; echo "rc=$?"` reports sed.
Every `rc=` line below is therefore `oath`'s own, taken unpiped. Shell variables
in a command line (`$lock`, `$URL`, `$key`, `$fresh`, `$out`) were set in the
capturing script; nothing is elided by hand.

Two kinds of evidence appear, and they are worth different amounts:

- **ASSERTED** — `run.sh` checks the underlying STATE observation on every run,
  so it cannot silently stop being true. Named per item below.
- **HAND-MEASURED** — a transcript captured once, reproduced here.
  `run.sh` asserts no error TEXT anywhere; message wording is hand-measured in
  all three items and will not be caught if it drifts.

The distinction matters because two of the three demands below are about what a
message SAYS, and nothing in this repo currently guards that.

---

## 1. Reading PUBLIC code requires a signing key — TOOL LIMITATION

**Blocking.** This is the sharpest item: it stops the canonical story `hydrate`
exists to enable — clone public code onto a machine that has nothing — before
any object is fetched.

Every remote read path refuses without `--key`:

```
$ OATH_STORE="$fresh" oath hydrate "$lock" --remote "$URL"; echo "rc=$?"
error: oath hydrate --remote authenticates every read; pass --key <file>: no signing key: pass --key <file> or --kms-key <full resource name including /cryptoKeyVersions/N>
rc=1

$ OATH_STORE="$fresh" oath resolve "$app" --remote "$URL" -o "$work/out.lock"; echo "rc=$?"
error: oath resolve --remote authenticates every read; pass --key <file>: no signing key: pass --key <file> or --kms-key <full resource name including /cryptoKeyVersions/N>
rc=1

$ OATH_STORE="$fresh" oath ls --remote "$URL" 2>&1 | sed "s|$fresh|\$fresh|g"
error: a registry is configured (http://127.0.0.1:32156) but it authenticates every read and no signing key was given.
Pass --key <file> or --kms-key <resource> to read it, or --local to read $fresh on purpose.
Refusing to show you a different store than the one you asked for: no signing key: pass --key <file> or --kms-key <full resource name including /cryptoKeyVersions/N>
```

(That block is piped, so an `$?` after it would be sed's. Measured unpiped, the
command exits 1:)

```
$ OATH_STORE="$fresh" oath ls --remote "$URL" >/dev/null 2>&1; echo "rc=$?"
rc=1
```

The same read succeeds with `--key`, so nothing but the key is missing:

```
$ OATH_STORE="$fresh" oath ls --remote "$URL" --key "$key" >"$out" 2>&1; echo "rc=$?"
rc=0
$ head -3 "$out"
Flaky            #4bb5e1cbb831  data  2 constructors
Interval         #f103d27ee151  data  1 constructors
KV               #a88fcdcbeae3  data  2 constructors
```

The refusal is CLIENT-SIDE and fires before the registry is contacted at all —
measured by aiming the same command at a port with nothing on it. Without a key
it produces the identical key error; with a key, the same command gets as far as
a connection attempt:

```
$ OATH_STORE="$fresh" oath hydrate "$lock" --remote http://127.0.0.1:1; echo "rc=$?"
error: oath hydrate --remote authenticates every read; pass --key <file>: no signing key: pass --key <file> or --kms-key <full resource name including /cryptoKeyVersions/N>
rc=1

$ OATH_STORE="$fresh" oath hydrate "$lock" --remote http://127.0.0.1:1 --key "$key"; echo "rc=$?"
error: fetching #1d0a9ecfb0ea: Post "http://127.0.0.1:1/mcp": dial tcp 127.0.0.1:1: connect: connection refused
rc=1
```

So this is a precondition of the CLIENT, and says nothing about what any
particular registry would have served anonymously.

**Population hurt: fresh-machine consumers of PUBLIC code** — anyone cloning a
lock they did not author, against a registry they do not write to. The measured
cost is exactly this: they must generate or provide an Ed25519 signing key in
order to READ public dependencies. A CI job, a container image, a reviewer
checking a claim, and a first-time reader all pay it. Publishers are not hurt:
they hold a key already, because they sign.

It is a LIMITATION, not a gap. The design already separates the two: signature
auth exists so that WRITES have unforgeable authorship, and read-only bearer
tokens exist for MCP clients (`docs/registry-auth.md`); `oath hydrate` takes no
token flag (its flags are `--from --key --remote`), so a public read has no
third position.

**DEMAND: a remote read that needs no signing key when the registry serves the
object publicly.** Whether that is an anonymous read mode, a flag, a
registry-declared public surface, or the absence of a client-side precondition
is a design question. What matters is that "clone public code with nothing but a
URL" becomes expressible at all.

**Evidence class:** the refusal of `hydrate --remote` without `--key`, and that
it writes nothing, are ASSERTED by `run.sh` (section 3). `resolve --remote` and
`ls --remote` are HAND-MEASURED only; all message texts are hand-measured.

**RESOLVED** (#189). `oath serve --public-reads` serves READS to anonymous callers
(the store is public and re-verifiable); WRITES stay signature-gated, unchanged. The
client reads anonymously when no `--key` is given, so `clone`/`hydrate`/`resolve`/`ls
--remote` need no key against such a registry — "clone public code with nothing but a
URL" is now expressible. It is OPT-IN: a registry without the flag still 401s an
anonymous read, and the client's error names both remedies (pass `--key`, or ask the
operator to enable `--public-reads`). The invariant that matters is preserved and
CI-guarded: a credential that FAILED to validate is never laundered into anonymous
access (`anonymousReadEligible`), and an anonymous principal is refused every write
(`put`/`reserve`/`delegate`/…). `public-reads-falsifier.sh` witnesses the end to end
on both postures; the deployed registry would enable it with a separate deploy.

## 2. An unavailable dependency reports its HASH, not its NAME — DIAGNOSTIC LIMITATION

**Second.** It does not block a working registry, but on a registry that has
drifted it turns a one-line diagnosis into an investigation — and drift is the
normal state of the deployed one.

Measured by deliberately making the registry unable to serve `show-nat`: the
served store is a copy of `codebase@HEAD` with that one object file removed,
which is what corpus drift looks like from a consumer's side.

```
$ sn=$(grep '"show-nat"' "$lock" | grep -oE "[0-9a-f]{64}"); echo "$sn"
1d0a9ecfb0eab9baa4782839fac41b93f9490d6ac98cc04d3001555cc97e108c
$ rm -f "$served/objects/$sn.bin"     # the drift: this registry can no longer serve show-nat

$ OATH_STORE="$c2" oath hydrate "$lock" --remote "$URL" --key "$key"; echo "rc=$?"
error: fetching #1d0a9ecfb0ea: error: no object #1d0a9ecfb0ea in this store
rc=1
```

**The diagnostic limitation** is that the message never says `show-nat`. The
information is already in hand: the lock the command was given maps
`"show-nat"` to that exact 64-hex hash in its `dependencies` object (printed in
full above), so the failing hash is one lookup from the name that binds it. A consumer instead gets twelve hex
digits and must grep the lock themselves to learn which dependency is missing —
and for a TRANSITIVE object, which the lock lists in `closure` but does not name,
they cannot learn it from the lock at all.

**Separately, a formatting defect**: the line carries a doubled `error:` prefix.
Comparing the two arms shows where the second one comes from — the message
arriving from the server already carries its own `error:` prefix, which the
client then prefixes again. The local arm, reading the same damaged store off
disk, prints a single prefix and is equally name-less:

```
$ OATH_STORE="$c2b" oath hydrate "$lock" --from "$served"; echo "rc=$?"
error: fetching #1d0a9ecfb0ea: no definition with hash 1d0a9ecfb0ea
rc=1
```

So the two travel together in the measured line but are independent: fixing the
prefix would leave the consumer just as unable to name the missing dependency.

**Population hurt: any consumer hydrating against a registry whose corpus has
drifted from the lock's.** That is not hypothetical here: CLAUDE.md records that
the live registry deliberately lags `codebase/` and that this is expected rather
than a defect to fix by syncing. The consumer who meets the drift is precisely
the one with the least context to interpret a bare hash.

Nothing in the design prevents naming the dependency, and this repo already
values the pattern — *name the thing* is why the port-order test checks
observable startup and why refusals are named rather than wrapped.

**DEMAND: when a fetch fails, name the lock dependency the hash is bound to (and
say plainly when it is a transitive object with no direct name), with one
`error:` prefix.** Whether that is error wrapping at the fetch site, a lock-aware
reporter, or a structural change to how remote errors cross the boundary is a
design question.

**Evidence class:** that a hash the registry cannot serve FAILS and leaves the
consumer store untouched is ASSERTED by `run.sh` (section 3, via a tampered
hash). The specific `show-nat` scenario, both message texts, and the doubled
prefix are HAND-MEASURED.

**RESOLVED** (#190). `oath hydrate` now names the failing hash's lock dependency
and emits a single prefix — the transcripts above are the SUPERSEDED behaviour,
kept as the measurement that motivated the fix. Current output:

```
$ oath hydrate <lock> --from <served-store>     # a direct dependency is missing
error: fetching show-nat (#1d0a9ecfb0ea): no definition with hash 1d0a9ecfb0ea
$ oath hydrate <lock> --remote <url> --key <k>   # same, over the wire — one prefix
error: fetching show-nat (#1d0a9ecfb0ea): no object #1d0a9ecfb0ea in this store
```

A transitive object the lock lists in `closure` but binds no name to is now
labelled as such rather than implying a name. `hydrateFetchLabel` names both
branches (unit-tested) and the client no longer re-prefixes a server error
(`mcpCallSignedBy`, unit-tested).

---

## 3. No single operation takes a lock to a store where the app runs — DESIGN GAP

**Smallest of the three, and the only one that is a gap rather than a
limitation.** It costs a consumer one extra command and one confusing error;
nothing is unreachable.

The clone is two commands, and the state between them is misleading:

```
$ OATH_STORE="$c3" oath hydrate "$lock" --remote "$URL" --key "$key" >"$out" 2>&1; echo "rc=$?"
rc=0
$ sed "s|$c3|\$c3|g" "$out"
hydrated 5 object(s), bound 4 name(s) into $c3

$ OATH_STORE="$c3" oath build checklist -o "$work/checklist"; echo "rc=$?"
error: no definition named "checklist"
rc=1

$ OATH_STORE="$c3" oath put --lock "$lock" checklist.oath --new; echo "rc=$?"
✓ numbered         #18f88d0d9c99  tested (200 cases per property) · total
    prop empty-is-empty           passed 200 cases
    prop one-item                 passed 200 cases
✓ checklist        #600c7dbf9f07  tested (200 cases per property) · total
    prop single                   passed 200 cases
rc=0

$ OATH_STORE="$c3" oath build checklist -o "$work/checklist" >"$out" 2>&1; echo "rc=$?"
rc=0
$ sed "s|$work|\$work|g" "$out"
built checklist → $work/checklist  (entry checklist : (-> (List Str) Str), guarantee: tested (200 cases per property), backend: go-emit/2)
  requires: no capabilities
  provenance: 2e21baad812be82c974da875a1b5bf27ba5dfbf8c944afc4a01ef4f89281391d  (oath provenance $work/checklist)
```

The lock, in full, is what makes the middle state expected rather than a bug:

```
$ sed "s|/Users/miclip/workspace/oath-lang|\$repo|" "$lock"
{
  "format": "oath-lock/1",
  "file": "$repo/docs/experiments/hydrate-consumer/checklist.oath",
  "dependencies": {
    "List": "fa452d59a2358fadba69764616745354a1e34465079c2a63e5f4c5d56f2baf05",
    "Str": "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
    "show-nat": "1d0a9ecfb0eab9baa4782839fac41b93f9490d6ac98cc04d3001555cc97e108c",
    "str-append": "7d158d0455d341a46cfcc678cfd41d35db89b574dc45158746ad922b8874e3a4"
  },
  "closure": [
    "1d0a9ecfb0eab9baa4782839fac41b93f9490d6ac98cc04d3001555cc97e108c",
    "30df3863fbb189976a0d624ec7697a62cfaf940554ae436027eea19c4cdfbc5e",
    "7d158d0455d341a46cfcc678cfd41d35db89b574dc45158746ad922b8874e3a4",
    "e6bbed8bc9347e15a4c088498c04f67596e1ca5478ce84dd81bd05457063e588",
    "fa452d59a2358fadba69764616745354a1e34465079c2a63e5f4c5d56f2baf05"
  ]
}
```

It records the source FILE and its DIRECT DEPENDENCIES by name and hash. **That
is not an omission.** A lock is about a source file's dependencies, and a source
file may define several names — `checklist.oath` defines two, `numbered` and
`checklist` — so "the app's entry name" is not a field the lock obviously owes
anyone, and demanding one would be designing rather than naming.

**The gap is that nothing composes the two steps.** `hydrate` reports success
and has done everything it promised; the obvious next command then fails with
`no definition named "checklist"`, which reads like a broken clone rather than
an incomplete one. There is no single operation whose postcondition is "this
store can build the app this lock was resolved from" — and the state in between
gives a consumer no signal that one more command is expected.

A smaller note on the `file` field: it is absolute here because this measurement
invoked `resolve` with an absolute path. It is recorded as given, not
canonicalized — a relative invocation writes a relative path:

```
$ ( cd docs/experiments/hydrate-consumer && oath resolve checklist.oath -o "$work/rel.lock" )
$ grep '"file"' "$work/rel.lock"
  "file": "checklist.oath",
```

So the absolute path is a property of this invocation, not of locks. Either way
the consumer passes the source file explicitly, so the field is not load-bearing
in this loop.

**Population hurt: every routine fresh clone.** Unlike items 1 and 2 this is not
a corner — it is the ordinary path, paid once per clone by everyone who takes it.
That it is ranked third is a statement about severity, not frequency: it costs a
command and a moment of confusion, where item 1 costs the scenario entirely.

**DEMAND: one reproduction operation whose result is a store the app can be
built from, not merely a store its dependencies are satisfied in.** Whether that
is a command composing hydrate and `put --lock`, a lock that carries enough to
identify the entry, or something else is a design question — as is whether the
app's verification (which `put --lock` performs, and which a consumer arguably
should not skip) belongs inside it.

**Evidence class:** the state observations are ASSERTED by `run.sh` — that
`checklist` is unbound after hydrate and `build` fails there (section 4), and
that `put --lock` then binds it and the build succeeds (sections 5-6). The
transcripts, the lock contents, and the relative-path measurement above are
HAND-MEASURED.

**RESOLVED** (#191). `oath clone <app> --lock <lock> [--from | --remote --key]`
composes the two steps: it hydrates the lock's closure AND admits+verifies the app
in one command, so the `no definition named <app>` failure is gone — the app is
present and proven. (clone removes the "app absent" obstacle; whether `build` then
accepts it is build's own gate for an ENTRY, which clone does not stand in for.)
Verification stays inside it — clone runs the same `verifyLock` + `put` the
`put --lock` path does, because a reproduction that skipped the proof would defeat
the point. It is transactional against VALIDATION failure and materializes a FRESH
store: the whole admission runs in a scratch and commits to the target only once
every definition is accepted, so a bad lock or a non-verifying app leaves the target
untouched (a mid-commit I/O failure is the one exception — the same limit any
multi-write on the fs store has). The target must be pristine (no bindings, no
`policy.json`) — a governed store goes through `put --lock`, which enforces its policy. `clone-falsifier.sh` witnesses
that the gap is real (hydrate alone leaves the app absent), that clone closes it
over both `--from` and `--remote`, that the app is verified rather than copied, that
a mid-app failure leaves the target untouched, and that a mismatched lock, a
non-empty target, and an empty source are each refused.
