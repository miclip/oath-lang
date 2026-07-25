variable "project_id" {
  type        = string
  description = "GCP project to deploy the Oath registry into."
}

variable "region" {
  type        = string
  description = "Region for Cloud Run, the store bucket, and the scheduler."
  default     = "us-central1"
}

variable "name_prefix" {
  type        = string
  description = "Prefix for resource names (lets you run dev/prod side by side)."
  default     = "oath"
}

variable "server_image" {
  type        = string
  description = "Container image (built + pushed by CI to Artifact Registry). Runs `serve` in the Cloud Run service and `worker` in the proof Job."
}

variable "objects_public" {
  type        = bool
  description = "Make the store bucket world-readable. Everything in the store (objects, journal, name index) is non-secret and re-verifiable by hash, so a public registry is sound; tokens and signing keys live in Secret Manager, never the store. Leave false for a private v1 (reads go through the API)."
  default     = false
}

variable "custom_domain" {
  type        = string
  description = "Custom domain to map to the serve service (e.g. registry.oath-lang.org). Empty = use the run.app URL only. Requires the PARENT domain (oath-lang.org) verified for this project in Google Search Console first; apply fails otherwise."
  default     = ""
}

variable "worker_schedule" {
  type        = string
  description = "Cron schedule for the proof worker Job (Cloud Scheduler). Must be longer than the Job's per-task timeout so runs never overlap (concurrent writers on the gcsfuse store trigger stale handles)."
  default     = "*/30 * * * *"
}

variable "enable_database" {
  type        = bool
  description = "Provision Cloud SQL (Postgres) for the name index + journal. Off for the v1 filesystem-over-gcsfuse store, which needs no database; turn on when the Postgres store driver lands (#14) so you do not pay for an idle instance meanwhile."
  default     = false
}

variable "db_tier" {
  type        = string
  description = "Cloud SQL machine tier (only used when enable_database = true)."
  default     = "db-f1-micro"
}
