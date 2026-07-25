# One-off migration Job for the Postgres cutover (docs/store-drivers.md, step 2).
# Runs `oath migrate-store` INSIDE the cluster so it can read the gcsfuse store
# (source) and reach Cloud SQL over its socket (dest). Provisioned with
# enable_database; you execute it once, between provisioning (step 1) and the
# flip (step 3):
#
#   gcloud run jobs execute <prefix>-migrate --region <region>
#
# It sets OATH_OBJECT_BUCKET + OATH_DB_DSN (so the cloud backend is the
# destination) but NOT OATH_BACKEND — the SOURCE stays the filesystem store at
# /store. migrate-store verifies the migrated journal before it exits.
resource "google_cloud_run_v2_job" "migrate" {
  count    = var.enable_database ? 1 : 0
  name     = "${var.name_prefix}-migrate"
  location = var.region

  template {
    template {
      service_account       = google_service_account.server.email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"
      max_retries           = 0
      timeout               = "1800s"

      volumes {
        name = "store"
        gcs {
          bucket    = google_storage_bucket.objects.name
          read_only = false
        }
      }
      volumes {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [google_sql_database_instance.index[0].connection_name]
        }
      }

      containers {
        image = var.server_image
        args  = ["migrate-store"]
        env {
          name  = "OATH_STORE"
          value = "/store"
        }
        env {
          name  = "OATH_OBJECT_BUCKET"
          value = google_storage_bucket.objects.name
        }
        env {
          name = "OATH_DB_DSN"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.db_dsn[0].secret_id
              version = "latest"
            }
          }
        }
        volume_mounts {
          name       = "store"
          mount_path = "/store"
        }
        volume_mounts {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
        resources {
          limits = {
            cpu    = "2"
            memory = "2Gi"
          }
        }
      }
    }
  }

  depends_on = [google_project_service.enabled]
}
