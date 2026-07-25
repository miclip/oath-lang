# Deploying the Oath registry (GitHub Actions → GCP)

This ships the #14 registry as a single-instance v1: `oath serve` (HTTP MCP) on
Cloud Run over a gcsfuse-mounted store bucket, with the proof worker as a
scheduled Cloud Run Job. Auth to GCP is keyless (Workload Identity Federation) —
no service-account key ever touches GitHub.

## What gets deployed

| Piece | Resource | Notes |
|---|---|---|
| API | Cloud Run service (`oath serve`) | Bearer-token auth; **single instance** (see the constraint below) |
| Store | GCS bucket, gcsfuse-mounted at `/store` | Whole filesystem store: objects, meta, `names.json`, `log.jsonl`, `proofq/`. Versioned. |
| Proof worker | Cloud Run **Job** (`oath prove-worker`) | Ticked by Cloud Scheduler (default every 5 min); binds `require_proven` names once proven |
| Tokens | Secret Manager | Terraform mints an initial `admin` token and outputs it |
| Images | Artifact Registry | Built + pushed by CI |
| Database | Cloud SQL (Postgres) | **Off by default** — v1 needs no DB; enable with the Postgres driver (#14) |

### The v1 constraint, up front

The store is the filesystem store over gcsfuse. Its journal and name index have a
**single safe writer**, so the serve service is pinned to one instance and the
worker runs briefly out of band. This is a real, persistent, low-traffic
registry — not a multi-instance one. Multi-instance needs the Postgres name-index
+ GCS object drivers (`enable_database` + #14); the infra here is built so that is
a driver change, not a re-architecture.

## One-time setup (do this async)

1. **Create a GCP project and link billing.**

2. **Bootstrap** — creates the Terraform state bucket, Artifact Registry, the
   keyless WIF trust, and the deployer service account. Run locally with an
   account that can administer the project:

   ```sh
   gcloud auth login
   PROJECT_ID=your-project GITHUB_REPO=miclip/oath-lang \
     ./terraform/bootstrap/bootstrap.sh
   # optional: REGION=us-central1 PREFIX=oath
   ```

   It prints the four secret values to set next. (Idempotent — safe to re-run.)

3. **Set GitHub repo variables** (Settings → Secrets and variables → Actions →
   **Variables** tab). Keyless auth means there are no secrets to store — these
   are all identifiers, not credentials (WIF security is the provider's
   repo-bound attribute condition + IAM, not the secrecy of these strings):

   | Variable | From bootstrap output |
   |---|---|
   | `GCP_PROJECT_ID` | your project id |
   | `GCP_DEPLOYER_SA` | `oath-deployer@…iam.gserviceaccount.com` |
   | `GCP_WIF_PROVIDER` | `projects/NUMBER/locations/global/workloadIdentityPools/…/providers/…` |
   | `TF_STATE_BUCKET` | `PROJECT-oath-tfstate` |
   | `GCP_REGION` | optional; defaults to `us-central1` |

## Deploy

- **Manually:** Actions → **deploy** → Run workflow → type `DEPLOY`.
- **Or** push a `v*` tag (`git tag v0.1.0 && git push --tags`).

CI builds the image (kernel + pinned Z3 4.16.0), pushes it to Artifact Registry,
and `terraform apply`s. The run summary prints the API URL. Grab the admin token:

```sh
# locally, with the state backend configured, or from Cloud Console:
terraform -chdir=terraform output -raw admin_token
```

## Verify it's live

```sh
API=https://…run.app         # from the workflow summary
TOKEN=…                      # terraform output -raw admin_token

# MCP initialize over HTTP
curl -s "$API/mcp" -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
```

Put a definition, then watch the worker prove a `require_proven` name on the next
tick (`oath log` shows `pending → prove → put/accepted`).

## Turning on signed verdicts (optional)

By default the worker records proofs **unsigned** (still fully re-verifiable). To
make the `proven` badge an authenticated "the registry re-proved this"
(docs/registry-auth.md):

```sh
oath keygen --out registry
gcloud secrets create oath-registry-key --data-file=registry.key --project=$PROJECT_ID
gcloud secrets add-iam-policy-binding oath-registry-key \
  --member="serviceAccount:oath-server@$PROJECT_ID.iam.gserviceaccount.com" \
  --role=roles/secretmanager.secretAccessor --project=$PROJECT_ID
```

Then mount it into the worker Job and set `OATH_KEY` to the mount path (a small
addition to `worker.tf`; the entrypoint signs automatically when `OATH_KEY` is
present). Keep the `.key` out of git.

## Cost note

v1 is cheap: Cloud Run scales the serve instance and the worker Job to near-zero
between requests/ticks, the store bucket is pennies, and the database is off. The
main always-on cost is the single min-instance serve container. Drop
`min_instance_count` to 0 in `main.tf` if cold starts are acceptable and you want
it cheaper still (the store persists in GCS regardless).

## Tear down

`terraform destroy` (the store bucket and — if enabled — the SQL instance carry
`prevent_destroy`; remove those blocks first if you really mean it). The store
bucket is the audit trail; back it up before destroying.
