output "api_url" {
  description = "Public URL of the oath serve API (Cloud Run)."
  value       = google_cloud_run_v2_service.server.uri
}

output "object_bucket" {
  description = "GCS bucket holding content-addressed objects + meta."
  value       = google_storage_bucket.objects.name
}

output "objects_cdn_ip" {
  description = "Global IP for the object CDN (immutable-blob read path). Empty when objects are not public."
  value       = var.objects_public ? google_compute_global_address.objects[0].address : ""
}

output "db_connection_name" {
  description = "Cloud SQL connection name for the name index + journal."
  value       = google_sql_database_instance.index.connection_name
}
