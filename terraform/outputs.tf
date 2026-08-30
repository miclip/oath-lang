output "api_url" {
  description = "Public URL of the oath serve API (Cloud Run)."
  value       = google_cloud_run_v2_service.server.uri
}

output "public_reads" {
  description = "Whether the serve container is configured to answer reads anonymously (#189). The deploy smoke test asserts the live posture matches this."
  value       = var.public_reads
}

output "store_bucket" {
  description = "GCS bucket holding the content-addressed store (objects, meta, journal, name index)."
  value       = google_storage_bucket.objects.name
}

output "admin_token" {
  description = "Bearer token for the initial 'admin' principal. Use as `Authorization: Bearer <token>`. Rotate by writing a new version of the tokens secret."
  value       = random_password.admin_token.result
  sensitive   = true
}

output "worker_job" {
  description = "Cloud Run Job that drains the proof queue (ticked by Cloud Scheduler)."
  value       = google_cloud_run_v2_job.worker.name
}

output "objects_cdn_ip" {
  description = "Global IP for the object CDN, when objects_public + cdn.tf are enabled. Empty otherwise."
  value       = var.objects_public ? google_compute_global_address.objects[0].address : ""
}

output "custom_domain_dns" {
  description = "DNS records to add at the parent domain's host to activate the custom domain. Empty when custom_domain is unset."
  value       = var.custom_domain == "" ? [] : google_cloud_run_domain_mapping.registry[0].status[0].resource_records
}

output "db_connection_name" {
  description = "Cloud SQL connection name, when enable_database = true. Empty otherwise."
  value       = var.enable_database ? google_sql_database_instance.index[0].connection_name : ""
}

# Needed by the pre-manual-run safety check in docs/deploy.md. An operator about
# to execute the worker by hand must address the SAME deployment the scheduler
# does; guessing the project or region prints an empty execution table, which
# reads as "nothing is running" and is exactly the fail-open this check exists to
# avoid. Emitting them removes the guess.
output "project_id" {
  description = "GCP project the deployment lives in."
  value       = var.project_id
}

output "region" {
  description = "Region the Cloud Run service and Job are deployed to."
  value       = var.region
}
