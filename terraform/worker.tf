# The verification worker pool (#14), v1: a Cloud Run Job that drains the proof
# queue once per invocation, ticked by Cloud Scheduler. It mounts the same store
# bucket as the serve service, proves require_proven candidates, and binds their
# names once proven (SPEC §8.5). CPU-heavy (Z3), so it gets more headroom than
# the serve service and runs out of band rather than in a request.
#
# v1 runs the worker UNSIGNED — proofs are recorded but not signed. To enable
# signed verdicts ("the registry re-proved this", docs/registry-auth.md):
#   1. oath keygen --out registry
#   2. gcloud secrets create <prefix>-registry-key --data-file=registry.key
#   3. grant the server SA secretAccessor, mount it, and set OATH_KEY to the
#      mount path (the entrypoint signs automatically when OATH_KEY is present).

resource "google_cloud_run_v2_job" "worker" {
  name     = "${var.name_prefix}-worker"
  location = var.region

  template {
    template {
      service_account       = google_service_account.server.email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"
      max_retries           = 1
      # A --scan pass proves the whole corpus in dependency order (fast for the
      # provable defs) but z3 burns the full deterministic budget on each
      # non-theorem before giving up, so a full pass is long. The timeout must fit
      # a whole pass, or the proof-state fingerprint never settles and the
      # non-theorems get re-attempted forever; keep it BELOW worker_schedule so
      # runs never overlap (concurrent gcsfuse writers → stale handles).
      timeout = "7200s"

      volumes {
        name = "store"
        gcs {
          bucket    = google_storage_bucket.objects.name
          read_only = false
        }
      }

      containers {
        image = var.server_image
        args  = ["worker"]
        env {
          name  = "OATH_STORE"
          value = "/store"
        }
        # Serialize mutable-store writes against the serve instance.
        env {
          name  = "OATH_STORE_LOCK"
          value = "1"
        }
        # Cloud backend env — present only when enable_database=true (cutover).
        dynamic "env" {
          for_each = local.cloud_env
          content {
            name  = env.value.name
            value = env.value.secret == null ? env.value.value : null
            dynamic "value_source" {
              for_each = env.value.secret == null ? [] : [1]
              content {
                secret_key_ref {
                  secret  = env.value.secret
                  version = "latest"
                }
              }
            }
          }
        }
        volume_mounts {
          name       = "store"
          mount_path = "/store"
        }
        dynamic "volume_mounts" {
          for_each = var.enable_database ? [1] : []
          content {
            name       = "cloudsql"
            mount_path = "/cloudsql"
          }
        }
        resources {
          limits = {
            # z3 needs real memory for the inductive proofs; at 2Gi the container
            # was OOM-killed mid-run (16 GiB requires cpu >= 4). Also: the runtime
            # base must match the pinned z3 build's glibc (ubuntu:24.04) or z3
            # fails to exec and every proof aborts — see Dockerfile.
            cpu    = "4"
            memory = "16Gi"
          }
        }
      }
      dynamic "volumes" {
        for_each = var.enable_database ? [1] : []
        content {
          name = "cloudsql"
          cloud_sql_instance {
            instances = local.cloud_sql_instances
          }
        }
      }
    }
  }

  depends_on = [google_project_service.enabled]
}

# Cloud Scheduler ticks the Job. It authenticates to the Cloud Run Admin API as a
# dedicated least-privilege SA that may only invoke this Job.
resource "google_service_account" "scheduler" {
  account_id   = "${var.name_prefix}-scheduler"
  display_name = "Oath proof-worker scheduler"
}

resource "google_cloud_run_v2_job_iam_member" "scheduler_invoke" {
  name     = google_cloud_run_v2_job.worker.name
  location = google_cloud_run_v2_job.worker.location
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.scheduler.email}"
}

resource "google_cloud_scheduler_job" "worker_tick" {
  name     = "${var.name_prefix}-worker-tick"
  region   = var.region
  schedule = var.worker_schedule

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project_id}/locations/${var.region}/jobs/${google_cloud_run_v2_job.worker.name}:run"
    oauth_token {
      service_account_email = google_service_account.scheduler.email
    }
  }

  depends_on = [
    google_project_service.enabled,
    google_cloud_run_v2_job_iam_member.scheduler_invoke,
  ]
}
