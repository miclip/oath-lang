# Oath registry — infrastructure (Terraform, GCP)

Infrastructure-as-code for the #14 registry, deployed from GitHub Actions.
**For the end-to-end deploy walkthrough, see [`docs/deploy.md`](../docs/deploy.md).**
This file is the resource-level reference.

## What it provisions (v1)

The v1 registry is a single-writer filesystem store on a gcsfuse-mounted bucket,
fronted by `oath serve` on Cloud Run, with the proof worker as a scheduled Job.
The host is not a trust root — clients re-verify by hash and re-earn proofs
(docs/registry-auth.md) — so most of it is dumb, cheap storage.

| Concern | Resource | Why |
|---|---|---|
| Store | GCS bucket, gcsfuse-mounted at `/store` (`main.tf`) | The whole filesystem store: objects, meta, `names.json`, `log.jsonl`, `proofq/`. Versioned. Everything in it is non-secret and re-verifiable. |
| API | Cloud Run service `oath serve` (`main.tf`) | Stateless; single instance (the fs journal/index has one safe writer). gen2 exec env for gcsfuse. |
| Proof worker | Cloud Run Job `oath prove-worker` + Cloud Scheduler (`worker.tf`) | Drains the proof queue out of band, binds `require_proven` names once proven (SPEC §8.5). |
| Auth | Secret Manager tokens (`tokens.tf`) | Bearer-token principals; Terraform mints an initial `admin` token (output, sensitive). |
| Database | Cloud SQL Postgres (`main.tf`, gated) | **Off by default** (`enable_database=false`) — v1 needs no DB. For the future Postgres name-index/journal driver. |
| CDN | Backend bucket (`cdn.tf`, gated off) | Future: only over a SEPARATE immutable objects bucket, never the v1 combined store. |

Bootstrap (`bootstrap/bootstrap.sh`, run once) stands up what `apply` cannot
create for itself: the Terraform state bucket, Artifact Registry, and the keyless
Workload Identity Federation trust + deployer service account.

## Deploy

Keyless from CI — no service-account key in GitHub:

1. `terraform/bootstrap/bootstrap.sh` (once) → sets up WIF + state bucket + AR.
2. Set the printed secrets on the repo.
3. Run the **deploy** workflow (or push a `v*` tag). CI builds the image and
   `terraform apply`s.

Manual apply still works for local iteration:

```sh
terraform init -backend-config="bucket=PROJECT-oath-tfstate" -backend-config="prefix=oath/state"
terraform apply -var project_id=PROJECT -var server_image=REGION-docker.pkg.dev/PROJECT/oath/oath:TAG
```

`prevent_destroy` guards the store bucket (and the SQL instance when enabled) —
the store is the audit trail and is not regenerable.

## Honest limits (read these)

1. **Single instance.** The filesystem store over gcsfuse has one safe writer, so
   the serve service is pinned to `min=max=1` and the worker runs briefly out of
   band. Real, persistent, low-traffic — not multi-instance. Multi-instance is
   the Postgres name-index + GCS object-store drivers (`enable_database` + #14) —
   designed in [`docs/store-drivers.md`](../docs/store-drivers.md): a byte-level
   backend seam, GCS for immutable objects, transactional Postgres for the name
   index + journal + proof queue. The config is built so that is a driver change,
   not a re-architecture.
2. **gcsfuse durability, not transactions.** Writes persist to GCS, but gcsfuse
   rename is copy-then-delete, not atomic. Two writers (serve + a worker tick
   overlapping) can in principle race the journal/index. Low-traffic v1 keeps the
   window tiny; the DB-backed store is the transactional fix.
3. **Bearer tokens today.** `oath serve` authenticates with tokens; signature-
   verified serve (docs/registry-auth.md) is still backlog. The worker can
   already *sign* its verdicts (docs/deploy.md → signed verdicts).
