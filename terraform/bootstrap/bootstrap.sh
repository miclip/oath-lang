#!/usr/bin/env bash
# One-time GCP bootstrap for deploying the Oath registry from GitHub Actions.
# Run this ONCE, locally, with an account that can administer the project. It
# creates the things CI cannot create for itself (they must exist before the
# first apply): the Terraform state bucket, the Artifact Registry repo, and the
# keyless Workload Identity Federation trust between this GitHub repo and a
# deployer service account. It is idempotent — safe to re-run.
#
# Prereqs: gcloud (authenticated: `gcloud auth login`), a billing-linked project.
#
# Usage:
#   PROJECT_ID=my-proj GITHUB_REPO=owner/repo ./bootstrap.sh
# Optional: REGION (default us-central1), PREFIX (default oath).
set -euo pipefail

: "${PROJECT_ID:?set PROJECT_ID=your-gcp-project}"
: "${GITHUB_REPO:?set GITHUB_REPO=owner/repo (the repo deploying from)}"
REGION="${REGION:-us-central1}"
PREFIX="${PREFIX:-oath}"

POOL="${PREFIX}-github-pool"
PROVIDER="${PREFIX}-github-provider"
DEPLOYER="${PREFIX}-deployer"
DEPLOYER_SA="${DEPLOYER}@${PROJECT_ID}.iam.gserviceaccount.com"
STATE_BUCKET="${PROJECT_ID}-${PREFIX}-tfstate"
AR_REPO="oath"

echo "== enabling the APIs bootstrap needs =="
gcloud services enable \
  iam.googleapis.com iamcredentials.googleapis.com sts.googleapis.com \
  cloudresourcemanager.googleapis.com serviceusage.googleapis.com \
  artifactregistry.googleapis.com storage.googleapis.com \
  --project="${PROJECT_ID}"

PROJECT_NUMBER="$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')"

echo "== Terraform state bucket: gs://${STATE_BUCKET} =="
if ! gcloud storage buckets describe "gs://${STATE_BUCKET}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud storage buckets create "gs://${STATE_BUCKET}" \
    --project="${PROJECT_ID}" --location="${REGION}" --uniform-bucket-level-access
fi
gcloud storage buckets update "gs://${STATE_BUCKET}" --versioning

echo "== Artifact Registry: ${AR_REPO} (${REGION}) =="
if ! gcloud artifacts repositories describe "${AR_REPO}" \
  --location="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud artifacts repositories create "${AR_REPO}" \
    --repository-format=docker --location="${REGION}" --project="${PROJECT_ID}" \
    --description="Oath registry images"
fi

echo "== deployer service account: ${DEPLOYER_SA} =="
if ! gcloud iam service-accounts describe "${DEPLOYER_SA}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud iam service-accounts create "${DEPLOYER}" \
    --project="${PROJECT_ID}" --display-name="Oath CI deployer"
fi

# Roles the main terraform needs to apply. Broad because a deployer creates
# services, buckets, secrets, schedulers, SAs, and IAM bindings.
for ROLE in \
  roles/run.admin \
  roles/iam.serviceAccountAdmin \
  roles/iam.serviceAccountUser \
  roles/storage.admin \
  roles/secretmanager.admin \
  roles/cloudscheduler.admin \
  roles/artifactregistry.writer \
  roles/serviceusage.serviceUsageAdmin \
  roles/resourcemanager.projectIamAdmin \
  roles/cloudsql.admin; do
  gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${DEPLOYER_SA}" --role="${ROLE}" \
    --condition=None --quiet >/dev/null
done

echo "== Workload Identity Federation pool/provider =="
if ! gcloud iam workload-identity-pools describe "${POOL}" \
  --location=global --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud iam workload-identity-pools create "${POOL}" \
    --location=global --project="${PROJECT_ID}" --display-name="GitHub Actions"
fi
if ! gcloud iam workload-identity-pools providers describe "${PROVIDER}" \
  --location=global --workload-identity-pool="${POOL}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud iam workload-identity-pools providers create-oidc "${PROVIDER}" \
    --location=global --workload-identity-pool="${POOL}" --project="${PROJECT_ID}" \
    --display-name="GitHub OIDC" \
    --issuer-uri="https://token.actions.githubusercontent.com" \
    --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
    --attribute-condition="assertion.repository=='${GITHUB_REPO}'"
fi

# Let ONLY this repo impersonate the deployer SA.
gcloud iam service-accounts add-iam-policy-binding "${DEPLOYER_SA}" \
  --project="${PROJECT_ID}" --role="roles/iam.workloadIdentityUser" \
  --member="principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL}/attribute.repository/${GITHUB_REPO}" \
  --quiet >/dev/null

PROVIDER_RESOURCE="projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL}/providers/${PROVIDER}"

cat <<EOF

============================================================
Bootstrap complete. Set these on the GitHub repo
(${GITHUB_REPO} → Settings → Secrets and variables → Actions):

  SECRETS
    GCP_PROJECT_ID    ${PROJECT_ID}
    GCP_DEPLOYER_SA   ${DEPLOYER_SA}
    GCP_WIF_PROVIDER  ${PROVIDER_RESOURCE}
    TF_STATE_BUCKET   ${STATE_BUCKET}

  VARIABLE (optional; defaults to us-central1)
    GCP_REGION        ${REGION}

Then run the "deploy" workflow (Actions → deploy → Run workflow → type DEPLOY),
or push a v* tag. See docs/deploy.md.
============================================================
EOF
