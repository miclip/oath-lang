# A GitHub webhook receiver

An **application**, not an example (#120). The difference is that GitHub composes
the request, GitHub chooses the header names, GitHub decides what a signature
looks like, and none of that negotiates — and that something else reads what it
writes, so changing the output breaks a dependent.

It verifies `X-Hub-Signature-256`, extracts the repository from the signed JSON
body, and appends a self-describing record to a log that `report.sh` consumes.

```
apps/github-webhook/
  webhook.oath    the receiver: 20 definitions, 14 properties on the handler
  hdr-probe.oath  a runnable witness for the header-canonicalization finding
  deliver.sh      an INDEPENDENT sender — openssl + curl, no Oath
  report.sh       the dependent: reads the log, dedups redeliveries
  acceptance.sh   28 end-to-end checks; `make check-app`, and CI runs it
```

The friction it produced is `docs/experiments/webhook-friction.md`. That log is
the deliverable; this is the thing that generated it.

## Running it

```sh
make -C ../.. build
oath put apps/github-webhook/webhook.oath
oath build gh-webhook -o /tmp/gh-webhookd

export OATH_VALUE_SECRET=$(openssl rand -hex 24)       # required: no value, no launch
export GITHUB_WEBHOOK_SECRET=$OATH_VALUE_SECRET        # the SENDER's copy, for deliver.sh
OATH_HTTP_ADDR=:8899 OATH_EMIT_PATH=/tmp/events.log /tmp/gh-webhookd &

./deliver.sh some-payload.json push
./report.sh /tmp/events.log
```

`deliver.sh` signs with **openssl** and posts with **curl**, following GitHub's
documented scheme. It shares no code with Oath, which is the point: openssl
computing the digest and Oath verifying it is a real cross-implementation check
rather than one implementation agreeing with itself.

## What it answers

| status | meaning |
|---|---|
| `405` | not a `POST` — a request that could never carry a signature |
| `500` | **this deployment is broken**: no usable secret, or the sink failed |
| `401` | no signature, or one that did not verify |
| `404` | authenticated, wrong path — the hook URL is misconfigured |
| `415` | authenticated, not JSON — the hook's content type is misconfigured |
| `200` | authenticated `ping` — the handshake; nothing recorded |
| `202` | authenticated event, recorded |

The order is the design: **method** before authentication (a `GET` carries no
body to sign, so answering `401` would be a lie); **configuration** before
authentication (an unusable secret means no signature can be evaluated, and the
failure is ours); **signature** before routing (path and content type are facts
about this deployment, and an unauthenticated caller learns neither).

## The record

```
oath-gh/1	<delivery-id>	<event>	<repository>	<received-at>
```

Field order follows `DESIGN.md`'s record model, and laying the fields out
against it exposed a distinction worth stating plainly: **GitHub's HMAC covers
the body only.**

| field | | |
|---|---|---|
| `oath-gh/1` | equivalence | what counts as the same record format |
| `<delivery-id>` | **unsigned** | GitHub's transport label; the dedup key, and the best available one |
| `<event>` | **unsigned** | likewise a label, not a claim anyone signed |
| `<repository>` | **signed** | read out of the body the signature covers |
| `<received-at>` | observation | true about this host and nothing else; the one field that differs between two records of one delivery |

So `<repository>` is the only column an adversary cannot alter without the
secret, and therefore the only one worth a policy decision. Nothing can fix
this at the receiver — you cannot bind a value to a signature the signer did not
compute over it.

**The log is at-least-once.** A handler is a pure function with no state, so the
receiver cannot know it has seen a delivery before and cannot be idempotent.
Deduplication is `report.sh`'s job, it keys on an unsigned field, and it is
therefore a defence against GitHub's own redelivery — not against someone who
captured a valid body and signature and varies the id on each replay.

## Configuration

| variable | |
|---|---|
| `OATH_VALUE_SECRET` | the shared secret, a **required value** (#126): absent or empty and the program does not start — exit `70`, before the port is bound. Beyond that, **minimum 16 characters, printable non-whitespace ASCII** (`!` through `~`) is application policy and answers `500` per request. Non-ASCII is rejected rather than mis-signed: `str-bytes` yields codepoints, and a codepoint above 255 crashed the request handler before this was checked. |
| `OATH_EMIT_PATH` | the record sink. Checked at launch: an unwritable path means the program does not start. |
| `OATH_HTTP_ADDR` | listen address, default `:8080`. |

The compiled binary requires exactly `emit (record_sink)` and `secret
(required_value)`, and nothing else — no outbound HTTP client is present in it at
all (#114). `oath provenance /tmp/gh-webhookd` reads that back without executing
it.

## Two things this found, kept here because they are load-bearing

**The header names are spelled the way Go canonicalizes them,** not the way
GitHub documents them: `X-Github-Event`, not `X-GitHub-Event`. `hdr-probe.oath`
is the runnable witness. Getting this wrong fails silently.

**The receiver cannot start without a secret,** and that took two attempts.
Launched with the secret unset, version 1 bound its port and accepted a delivery
forged with the **empty key**; the launch gate had resolved `env` correctly,
because `process_env` is the authority to *read the environment*, not a promise
that any variable exists. Version 2 answered `500` to every request — better,
and still a listening process a health check calls healthy.

The secret is now a **required value**: the capability record declares
`secret Str`, not `env (-> Str Str)`, so the host must supply it before any Oath
code runs. There is no running program to forge a delivery against and no `""`
for the handler to observe. That is #126, and this application is why it exists.
Length and charset remain `secret-is-usable`'s job — provisioning is not policy.
