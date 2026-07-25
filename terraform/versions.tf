terraform {
  required_version = ">= 1.5"

  # Remote state in GCS. The bucket is created out-of-band by
  # bootstrap/bootstrap.sh (state must live somewhere before the first apply);
  # CI supplies bucket + prefix via `-backend-config` at init time, so nothing
  # environment-specific is committed here.
  backend "gcs" {}

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.30" # gcsfuse volumes on Cloud Run v2 service + job
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
