# The verification worker pool (#14)

Proving is the one guarantee the submission path never recomputes. Everything
else a repoint policy enforces — `forbid_falsified`, `require_total`,
`min_mutation_score` — is re-run synchronously against the submitted object
before the name moves (`api.go` `apiPut` → `verifyDef`, `evalPolicy`). SMT
proving is unbounded work (SPEC §7.2), so it cannot run inside an HTTP put. This
document is the model for doing it out of band, and how it maps onto the hosted
infrastructure in `terraform/`.

The design principle throughout: **the worker is a convenience and a policy tier,
never a root of trust.** Proofs are re-earned by whoever consumes an object, so a
consumer never has to believe the worker actually proved anything — it re-proves.
That is what lets the whole pool be dumb, restartable, and idempotent.

## The split repoint decision

A `require_proven` name cannot be decided at put time, so the decision splits
(SPEC §8.5):

```
put ─▶ gate + tests + mutation (synchronous)
        │
        ├─ falsified / no props ─▶ blocked        (can never prove)
        ├─ already fully proven ─▶ bind now
        └─ tested, unproven     ─▶ PENDING ──▶ enqueue(hash, gate=true)
                                                  name stays put

prove-worker ─▶ claim ─▶ apiProveHash ─▶ fully proven?
                                          ├─ yes ─▶ re-check policy ─▶ Repoint + journal accepted (signed)
                                          └─ no  ─▶ journal blocked  (name unchanged)
```

The worker has a second role with no gate: `--scan` seeds the queue from every
`tested`-but-unproven definition and upgrades the index to `proven` in place.
This is what makes a registry's `proven` badge one it *earned* by re-proving,
not one a publisher asserted.

## Determinism is what makes the queue trivial

Proving hash `H` is a pure function of the store's content: seeds derive from the
content hash, the solver is pinned (SPEC §10 check 5), and the proof set is
hash-keyed metadata that merges. Three consequences:

- **Idempotent.** Re-proving `H` — after a crash, a redeploy, or a double
  dispatch — yields the identical verdict and merges to a no-op.
- **No exclusion needed for correctness.** A job claimed by two workers is wasted
  CPU, never corrupted state. Leases exist only to avoid the waste.
- **Restartable.** A worker holds no state; the queue and the store are the
  state. Kill it mid-proof and its lease ages out; another worker reclaims.

## Reference queue (filesystem store)

`Store` (`proofq.go`) exposes the queue as four operations, deliberately small so
the hosted store can reimplement them against a database without touching the
worker:

| Operation | Filesystem store | Hosted store |
|---|---|---|
| `EnqueueProof(job)` | atomic write `proofq/<hash>.job` | `INSERT … ON CONFLICT DO NOTHING` |
| `ClaimProof(now, ttl)` | rename `.job`→`.lease` (atomic single-winner); reclaim `.lease` older than `ttl` | `SELECT … FOR UPDATE SKIP LOCKED` + lease column |
| `CompleteProof(hash)` | remove `.lease`/`.job` | `DELETE` / mark done |
| `ProofQueueDepth()` | count queue files | `SELECT count(*)` |

A job is `{hash, name, submitter, enqueued, gate}`. `gate=true` means a deferred
name-bind is waiting on the outcome; `gate=false` is a background upgrade.

## Hosted mapping (the Terraform)

The infrastructure in `terraform/` already anticipates this; the pool is the one
CPU-heavy tier deliberately left out of the first cut (see `terraform/README.md`).

- **Queue** → a `proof_jobs` table in the existing Cloud SQL (Postgres) instance,
  claimed with `FOR UPDATE SKIP LOCKED`. (Cloud Tasks/PubSub is an alternative,
  but Postgres already holds the name index and journal transactionally, and the
  gate's name-bind wants that same transaction — keep them together.)
- **Enqueue** → the `oath serve` API, on a `pending` put, inserts a job in the
  same transaction that journals `pending`.
- **Workers** → a separate Cloud Run **Jobs** service (not the request-serving
  Cloud Run *service*): CPU-heavy, autoscaled on queue depth, each replica
  running `oath prove-worker` against the shared store bucket + DB. They need Z3
  in the image and a mounted signing key (Secret Manager) so verdicts are signed.
- **Name-bind** → the worker's `Repoint` runs as a Postgres transaction, which is
  where the async name-index concurrency story (two puts racing the same name)
  gets its real answer — last-writer-wins on the filesystem store is only
  adequate single-process.

## Open edges (honest)

- **Retry/backoff.** A genuinely unprovable object is journaled `blocked` and the
  job dropped; a *transient* Z3 timeout looks the same to the worker and is also
  dropped. A real pool needs a bounded retry with backoff, distinguishing "proof
  refuted/absent" from "solver ran out of wall clock" (the §7.2 attempt-validity
  distinction the kernel already draws — a killed solver records no verdict).
  Today: re-enqueue by hand (or re-put) to retry.
- **Sandboxing.** Workers run Z3 on submitted content. Cloud Run Jobs give
  process/network isolation; a hardened pool would also rlimit the solver
  (already wall-capped) and drop egress.
- **Proof artifacts.** The worker records the verdict, not the proof transcript.
  Publishing the transcript (so a skeptic can replay the exact SMT run without
  re-deriving it) is a later addition; it does not change trust, since re-proving
  is the real check.
- **Fairness.** The reference queue is a deterministic scan (avoids starvation)
  but not a priority queue. Gate jobs (someone is waiting on a name) arguably
  outrank background upgrades; the hosted table can order by `(gate desc,
  enqueued)`.

## Try it

```sh
oath keygen --out registry
printf '{"rules":[{"names":["*"],"require_proven":true}]}' > codebase/policy.json
oath put dbl.oath                 # ⏳ PENDING PROOF — name not bound
oath prove-worker --key registry.key --once   # proves, signs, binds
oath ls                           # dbl … PROVEN
oath log                          # pending → prove(signed) → put/accepted(signed)
```
