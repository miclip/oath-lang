variable "project_id" {
  type        = string
  description = "GCP project to deploy the Oath registry into."
}

variable "region" {
  type        = string
  description = "Region for Cloud Run and Cloud SQL."
  default     = "us-central1"
}

variable "name_prefix" {
  type        = string
  description = "Prefix for resource names (lets you run dev/prod side by side)."
  default     = "oath"
}

variable "server_image" {
  type        = string
  description = "Container image for `oath serve` (built + pushed separately, e.g. to Artifact Registry). The registry app layer is what turns this infra into a service; see docs/registry-auth.md."
}

variable "db_tier" {
  type        = string
  description = "Cloud SQL machine tier for the name index + journal."
  default     = "db-f1-micro"
}

variable "objects_public" {
  type        = bool
  description = "Make the object bucket world-readable. Sound because objects are content-addressed and re-verified by clients (a byte that doesn't hash to its name is rejected). Set false to keep reads behind the API."
  default     = true
}
