# Deploying the Oath registry (GitHub Actions → GCP)

> **Before any cutover, read `docs/deploy-delta.md`.** The live registry is many
> commits behind, and the delta now includes journal-format and verification-
> semantics changes rather than only new features. That document reviews the
> cutover as a whole and states what it does NOT authorise.

This ships the #14 registry as a single-instance v1: `oath serve` (HTTP MCP) on
Cloud Run over a gcsfuse-mounted store bucket, with the proof worker as a
scheduled Cloud Run Job. Auth to GCP is keyless (Workload Identity Federation) —
no service-account key ever touches GitHub.

## What gets deployed

| Piece | Resource | Notes |
|---|---|---|
| API | Cloud Run service (`oath serve`) | **Public reads** (anonymous, read-only — #189), writes signature/token-gated; **single instance** (see the constraint below). Toggle with the `public_reads` Terraform variable |
| Store | GCS bucket, gcsfuse-mounted at `/store` | Whole filesystem store: objects, meta, `names.json`, `log.jsonl`, `proofq/`. Versioned. |
| Proof worker | Cloud Run **Job** (`oath prove-worker`) | Ticked by Cloud Scheduler, **daily** (`0 6 * * *`); 12h task timeout; binds `require_proven` names once proven |
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

### The worker cadence is a CONSTRAINT, not a preference

`worker_schedule` MUST stay longer than the Job's task timeout, and the timeout
MUST fit a whole `--scan` pass. Both directions have already been violated in
production, and neither failure is obvious from the outside:

- **Ticking too often overlaps runs.** Two concurrent gcsfuse writers produce
  stale handles; an overlapping scheduled tick plus a manual run wedged the
  corpus at 73/105 for hours. This is the same single-safe-writer constraint
  above, arriving through the scheduler instead of through serve.

  **The cadence alone does NOT prevent this**, and the incident above is the
  proof: it only spaces *scheduled* runs apart. A manual execution launched
  during a scheduled pass reproduces the failure exactly. So before running the
  Job by hand, check that nothing is already executing:

  ```sh
  cd terraform
  JOB=$(terraform output -raw worker_job)   # never assume any of these three
  PROJ=$(terraform output -raw project_id)
  REG=$(terraform output -raw region)

  gcloud run jobs executions list --job "$JOB" \
    --project "$PROJ" --region "$REG" \
    --format='table(name, createTime, completionTime, status.conditions[0].type)'
  # ANY ROW WITH AN EMPTY completionTime IS STILL RUNNING — do not start another.
  ```

  Three details, each of which was wrong in an earlier draft of this paragraph
  and each of which fails the check OPEN — reporting "idle" while a pass runs:

  - **No `--filter`.** A filter on `completionTime` is easy to invert, and the
    inverted form lists exactly the finished runs while hiding the active one.
  - **No `--limit`.** A twelve-hour pass sits *below* any newer short executions,
    so truncating the list can show five completed rows while the run that
    matters is off the bottom.
  - **No assumed names.** `REGION` is set inside the bootstrap script's own
    process, not in your shell, and the job name comes from `name_prefix`. Guess
    either and the command inspects nothing, or the wrong deployment, and prints
    a reassuringly empty table.

  A pass can take twelve hours, so "it was probably finished" is not a check.
- **A timeout shorter than a pass never settles.** A pass that dies mid-tail
  commits nothing, so the proof-state fingerprint never advances and the
  non-theorems are re-attempted on every tick, forever. At a 2h timeout that
  happened every time.

Hence daily at `0 6 * * *` against a 12h timeout. An earlier version of this
table said "every 5 min", which is not merely stale — it describes the first
failure above. **Read `terraform/worker.tf` before changing either number**; its
comments carry the incident history that produced them.

Note the cadence only bites while the corpus is unsettled: once everything
provable is proven, the fingerprint gate makes each scan a fast no-op. Issue #140
proposes replacing the whole-corpus pass with a delta, which would make the
timeout stop being load-bearing at all.

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

## Custom domain (e.g. registry.oath-lang.org)

The `run.app` URL works as-is; this maps a vanity domain onto it with a
Google-managed TLS cert. One-time:

1. **Verify the parent domain** for the project. In
   [Google Search Console](https://search.google.com/search-console), verify
   ownership of `oath-lang.org` (add the TXT record it gives you at your DNS
   host), then in the GCP console add the project's deployer as a verified owner.
   Without this, the domain mapping fails to create.

2. **Point CI at the domain** — set a repo variable:

   ```sh
   gh variable set REGISTRY_DOMAIN --body registry.oath-lang.org
   ```

3. **Re-run the deploy.** Terraform creates the mapping and the run summary
   prints the DNS record to add (a `CNAME` to `ghs.googlehosted.com` for a
   subdomain). Also available any time:

   ```sh
   terraform -chdir=terraform output -json custom_domain_dns
   ```

4. **Add that record** at whatever hosts `oath-lang.org` DNS (your registrar or
   Vercel's DNS — the site itself stays on Vercel; this is just a new subdomain
   record). The cert provisions in ~15 min, then `https://registry.oath-lang.org`
   is live.

> Cloud Run managed domain mappings are regional and simple; for multi-region or
> heavier routing, the scale-up path is a global HTTPS load balancer with a
> serverless NEG (more Terraform, not needed for v1).

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
