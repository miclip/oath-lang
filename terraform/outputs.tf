output "api_url" {
  description = "Public URL of the oath serve API (Cloud Run)."
  value       = google_cloud_run_v2_service.server.uri
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

output "db_connection_name" {
  description = "Cloud SQL connection name, when enable_database = true. Empty otherwise."
  value       = var.enable_database ? google_sql_database_instance.index[0].connection_name : ""
}
