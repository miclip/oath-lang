# Oath registry — infrastructure (Terraform, GCP)

Infrastructure-as-code for hosting the public content-addressed registry (#14).
This is the **infra shape**, not a running service — see the honest caveats
below.

## What it provisions

The architecture follows straight from the substrate (content-addressed, trust
by re-verification), so the host is *not* a root of trust and most of it is dumb
and cheap:

| Concern | Resource | Why |
|---|---|---|
| Objects + meta | GCS bucket (immutable, world-readable) + Cloud CDN | Content-addressed blobs; clients re-verify by hash, so no trusted compute on reads. Immutable → perfect CDN case. |
| Name index + journal + discovery indexes | Cloud SQL (Postgres) | The one mutable, contended state: transactional repoints, the hash-chained (and, per `docs/registry-auth.md`, signed) journal, `propHash`/`eHash` → def indexes. |
| API | Cloud Run (`oath serve`) | Stateless; state lives in the bucket + DB, so it scales to zero. |
| Secrets | Secret Manager | DB password (later: no shared auth secret — trust is signature-based). |

`terraform apply` also enables the needed APIs and wires a least-privilege
service account for the server.

Not provisioned here (yet): the **verification worker pool** (a CPU-heavy Cloud
Run Jobs / queue tier that re-runs Z3 for `require_proven` enforcement and
`find --implies`). The *kernel* side of it is built — `oath prove-worker` drains
a proof queue, proves out of band, and binds `require_proven` names once proven
(SPEC §8.5); `docs/registry-verification.md` gives the design and how it maps
onto a `proof_jobs` table in the Cloud SQL instance below plus a Cloud Run Jobs
worker service. What's left is the Terraform for that Jobs service + table and
the GCS/Postgres store drivers. (The other policy checks — `forbid_falsified`,
`require_total`, `min_mutation_score` — are already recomputed synchronously in
the put path, so they need no worker.)

## Apply

```sh
# 1. build + push the oath serve container to Artifact Registry (separate step).
# 2. then:
terraform init
terraform apply \
  -var project_id=YOUR_PROJECT \
  -var server_image=REGION-docker.pkg.dev/YOUR_PROJECT/oath/serve:TAG
```

`prevent_destroy` guards the object bucket and the SQL instance — the store is
the audit trail and is not regenerable.

## Honest caveats (read these)

1. **Not applied here.** This repo ships the config, not a deployment. Applying
   needs *your* GCP project, billing, and credentials, and it provisions real
   billed resources — that step is yours, not the toolchain's.
2. **The app layer is the real work.** This stands up a *server*, and today's
   `oath serve` uses bearer tokens over a filesystem store. Turning this into the
   registry means the substrate work in `docs/registry-auth.md` — **signed puts,
   keys as principals, signed journal entries** — plus a GCS-backed store driver
   and a Postgres name-index/journal driver. The infra is agnostic to all of it
   (it stores signed bytes and an index); the trust lives in the signatures and
   the client. Build that first; this deploys it.
3. **v1 can be smaller.** Drop the CDN (`-var objects_public=false`, delete
   `cdn.tf`) and the worker pool, trust submitted verdicts initially, and this is
   a bucket + Cloud Run + Postgres — a weekend-shaped deploy. Harden toward the
   full picture from there.
