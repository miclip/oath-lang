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
      max_retries           = 0
      # A --scan pass proves the whole corpus in dependency order (fast for the
      # provable defs) but z3 burns the full deterministic budget on each
      # non-theorem before giving up, so a full pass is long. The timeout must fit
      # a whole pass, or the proof-state fingerprint never settles and the
      # non-theorems get re-attempted forever; keep it BELOW worker_schedule so
      # runs never overlap (concurrent gcsfuse writers → stale handles).
      #
      # 8h, not 2h: GCP's per-core speed is well below a dev laptop's, so z3 goals
      # that finish under their rlimit budget locally hit the 600s wall-cap here —
      # the whole heavy tail runs several times slower than local. At 2h a pass
      # timed out mid-tail every time, committing nothing new and re-attempting the
      # same doomed level forever (the corpus sat at 73/105 for hours). max_retries
      # is 0 so a run that still overruns fails cleanly instead of auto-burning a
      # second full-length attempt.
      timeout = "28800s"

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
          for_each = local.cloud_active ? [1] : []
          content {
            name       = "cloudsql"
            mount_path = "/cloudsql"
          }
        }
        resources {
          limits = {
            # 8 vCPU because the worker proves a dependency level N-way in parallel
            # (one z3 per core) — a full corpus pass becomes minutes, not hours.
            # 16 GiB because z3's inductive proofs are memory-heavy (at 2Gi the
            # container was OOM-killed mid-run). The runtime base must match the
            # pinned z3 build's glibc (ubuntu:24.04) or z3 fails to exec — see
            # Dockerfile.
            cpu    = "8"
            memory = "16Gi"
          }
        }
      }
      dynamic "volumes" {
        for_each = local.cloud_active ? [1] : []
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
