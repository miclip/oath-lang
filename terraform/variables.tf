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

variable "worker_wallcap_sec" {
  type        = number
  description = "Per-goal z3 wall-clock safety cap (seconds) for the proof worker (OATH_PROVE_WALLCAP_SEC). NOT part of any recorded verdict — a cap hit is an environmental abort, never an outcome — so this is purely a host-speed knob. Back to the 600s default: it was raised to 1800s on the theory that the corpus plateau was wall-clock-limited, which turned out to be WRONG (the cause was stale termination metadata, fixed by re-putting). The raise bought no verdicts and tripled the cost of every futile attempt, since on slow cores the wall cap binds before the rlimit budget — a full rescan then ran ~9.5h before even reaching the rot arms and timed out short of finishing. Evidence 600s suffices: the registry proved 99 → 121 under it. Raise it only with evidence that a goal returns a verdict at the larger cap and not the smaller."
  default     = 600
}

variable "worker_schedule" {
  type        = string
  description = "Cron schedule for the proof worker Job (Cloud Scheduler). Must be longer than the Job's per-task timeout (28800s / 8h) so runs never overlap (concurrent writers on the gcsfuse store trigger stale handles — an overlapping scheduled tick + manual run wedged the corpus at 73/105 for hours). Once the corpus is fully proven, the fingerprint gate makes each scan a fast no-op."
  default     = "0 6 * * *"
}

variable "enable_database" {
  type        = bool
  description = "Provision Cloud SQL (Postgres) + the DSN secret for the name index + journal. This does NOT switch the store to Postgres — serve/worker keep using the filesystem store until activate_cloud_backend=true. Off by default so you do not pay for an idle instance."
  default     = false
}

variable "activate_cloud_backend" {
  type        = bool
  description = "Switch serve + worker to the Postgres/GCS store (OATH_BACKEND=cloud + DSN + Cloud SQL socket). Requires enable_database. Keep false until AFTER `oath migrate-store` has copied the fs store into the DB — flipping it before migration would serve an EMPTY registry. This is the deliberate cutover flip (docs/store-drivers.md)."
  default     = false
}

variable "db_tier" {
  type        = string
  description = "Cloud SQL machine tier (only used when enable_database = true)."
  default     = "db-f1-micro"
}

variable "public_reads" {
  type        = bool
  description = "Serve READS anonymously (#189): a consumer may hydrate/clone with no key, while WRITES stay signature-gated. On for the public registry. Sets OATH_PUBLIC_READS on the serve container; toggling it re-applies without an image rebuild."
  default     = true
}
