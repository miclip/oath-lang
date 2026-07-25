# The Oath registry infrastructure (#14), v1: a single-writer filesystem store
# on a gcsfuse-mounted bucket, fronted by the stateless `oath serve` on Cloud
# Run, with the proof worker as a scheduled Cloud Run Job (worker.tf). Trust
# lives in the client and the signatures (docs/registry-auth.md), not here — the
# host stores re-verifiable bytes.
#
# v1 constraint, stated honestly: the store is the filesystem store over
# gcsfuse, whose journal + name index have a single safe writer, so the serve
# service is pinned to ONE instance. Multi-instance is the Postgres/GCS store
# driver work (enable_database, and #14) — until then this scales to 1, not N.

locals {
  services = concat([
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "storage.googleapis.com",
    "cloudscheduler.googleapis.com",
    "compute.googleapis.com",
  ], var.enable_database ? ["sqladmin.googleapis.com"] : [])
}

resource "google_project_service" "enabled" {
  for_each                   = toset(local.services)
  service                    = each.value
  disable_dependent_services = false
  disable_on_destroy         = false
}

# ---------------------------------------------------------------------------
# The store bucket. In v1 it holds the WHOLE filesystem store (objects/, meta/,
# names.json, log.jsonl, proofq/) behind a gcsfuse mount. Everything in it is
# non-secret and re-verifiable by hash, so it MAY be world-readable (a public
# registry); tokens and signing keys never live here. Versioning is on so a bad
# write to the mutable index/journal is recoverable.
# ---------------------------------------------------------------------------
resource "google_storage_bucket" "objects" {
  name                        = "${var.project_id}-${var.name_prefix}-store"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = var.objects_public ? "inherited" : "enforced"

  versioning {
    enabled = true
  }
  # The store is the audit trail; guard against a `terraform destroy` wiping it.
  lifecycle {
    prevent_destroy = true
  }
}

resource "google_storage_bucket_iam_member" "public_read" {
  count  = var.objects_public ? 1 : 0
  bucket = google_storage_bucket.objects.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}

# ---------------------------------------------------------------------------
# The service account the serve + worker run as. It owns the store bucket
# (read/write via gcsfuse) and reads the tokens secret. Least privilege: no
# project-wide roles.
# ---------------------------------------------------------------------------
resource "google_service_account" "server" {
  account_id   = "${var.name_prefix}-server"
  display_name = "Oath registry server + worker"
}

resource "google_storage_bucket_iam_member" "server_store" {
  bucket = google_storage_bucket.objects.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.server.email}"
}

# ---------------------------------------------------------------------------
# API / control plane — the stateless `oath serve` binary on Cloud Run. State
# lives on the gcsfuse-mounted bucket. Pinned to a single instance (see the file
# header); gcsfuse requires the gen2 execution environment.
# ---------------------------------------------------------------------------
resource "google_cloud_run_v2_service" "server" {
  name     = "${var.name_prefix}-server"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    service_account       = google_service_account.server.email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

    # Single writer: the filesystem journal + name index cannot be safely shared
    # by concurrent instances until the DB-backed store driver exists.
    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    volumes {
      name = "store"
      gcs {
        bucket    = google_storage_bucket.objects.name
        read_only = false
      }
    }
    volumes {
      name = "tokens"
      secret {
        secret = google_secret_manager_secret.tokens.secret_id
        items {
          version = "latest"
          path    = "tokens.json"
        }
      }
    }

    containers {
      image = var.server_image
      # Default CMD is `serve`; the entrypoint builds --http from $PORT.
      ports {
        container_port = 8080
      }
      env {
        name  = "OATH_STORE"
        value = "/store"
      }
      env {
        name  = "OATH_TOKENS_FILE"
        value = "/secrets/tokens/tokens.json"
      }
      volume_mounts {
        name       = "store"
        mount_path = "/store"
      }
      volume_mounts {
        name       = "tokens"
        mount_path = "/secrets/tokens"
      }
      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }
    }
  }

  depends_on = [google_project_service.enabled]
}

# Public endpoint; authorization is by bearer token + repoint policy, not by who
# can reach the URL.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.server.name
  location = google_cloud_run_v2_service.server.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# ---------------------------------------------------------------------------
# Cloud SQL (Postgres) for the eventual name index + journal. OFF by default:
# the v1 gcsfuse store needs no database, and an idle db-f1-micro still bills.
# Turn on (enable_database = true) alongside the Postgres store driver (#14).
# ---------------------------------------------------------------------------
resource "google_sql_database_instance" "index" {
  count            = var.enable_database ? 1 : 0
  name             = "${var.name_prefix}-index"
  database_version = "POSTGRES_15"
  region           = var.region

  settings {
    tier              = var.db_tier
    availability_type = "ZONAL"
    disk_autoresize   = true
    ip_configuration {
      ipv4_enabled = true
    }
    backup_configuration {
      enabled = true
    }
  }

  deletion_protection = true
  depends_on          = [google_project_service.enabled]
}

resource "google_sql_database" "registry" {
  count    = var.enable_database ? 1 : 0
  name     = "registry"
  instance = google_sql_database_instance.index[0].name
}

resource "random_password" "db" {
  count   = var.enable_database ? 1 : 0
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  count    = var.enable_database ? 1 : 0
  name     = "${var.name_prefix}-app"
  instance = google_sql_database_instance.index[0].name
  password = random_password.db[0].result
}

resource "google_secret_manager_secret" "db_password" {
  count     = var.enable_database ? 1 : 0
  secret_id = "${var.name_prefix}-db-password"
  replication {
    auto {}
  }
  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "db_password" {
  count       = var.enable_database ? 1 : 0
  secret      = google_secret_manager_secret.db_password[0].id
  secret_data = random_password.db[0].result
}

resource "google_secret_manager_secret_iam_member" "server_db_password" {
  count     = var.enable_database ? 1 : 0
  secret_id = google_secret_manager_secret.db_password[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}

resource "google_project_iam_member" "server_sql" {
  count   = var.enable_database ? 1 : 0
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.server.email}"
}
