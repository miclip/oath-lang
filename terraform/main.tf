# The Oath registry infrastructure (#14). The architecture follows straight from
# the substrate: content-addressed objects are immutable, world-readable blobs
# (clients re-verify by hash and re-earn proofs, so the host is not a trust
# root); the only mutable, contended state — the name index and the
# tamper-evident journal — lives in a real database; and the stateless API is
# the `oath serve` binary. Trust lives in the signatures and the client
# (docs/registry-auth.md), not in this infrastructure.

locals {
  services = [
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "secretmanager.googleapis.com",
    "compute.googleapis.com",
    "storage.googleapis.com",
  ]
}

resource "google_project_service" "enabled" {
  for_each                   = toset(local.services)
  service                    = each.value
  disable_dependent_services = false
  disable_on_destroy         = false
}

# ---------------------------------------------------------------------------
# Object store — content-addressed, immutable, world-readable blobs.
# objects/<hash>.bin and meta/<hash>.json. This is the bulk of the bytes and it
# needs zero compute: it is dumb storage, fronted by a CDN (see cdn.tf).
# ---------------------------------------------------------------------------
resource "google_storage_bucket" "objects" {
  name                        = "${var.project_id}-${var.name_prefix}-objects"
  location                    = var.region
  uniform_bucket_level_access = true
  # Content-addressed: an object never changes, so versioning is pointless and
  # public-access-prevention is off only when we deliberately publish reads.
  public_access_prevention = var.objects_public ? "inherited" : "enforced"

  versioning {
    enabled = false
  }
  # Objects are permanent (the store is the audit trail). Guard against a
  # `terraform destroy` wiping the commons.
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
# Mutable state — the name index, the append-only signed journal, and the
# discovery indexes (propHash/eHash -> defs). The one place needing
# transactional consistency (two clients repointing the same name) and ordering
# (the hash-chained journal). Postgres.
# ---------------------------------------------------------------------------
resource "google_sql_database_instance" "index" {
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
  name     = "registry"
  instance = google_sql_database_instance.index.name
}

resource "random_password" "db" {
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  name     = "${var.name_prefix}-app"
  instance = google_sql_database_instance.index.name
  password = random_password.db.result
}

resource "google_secret_manager_secret" "db_password" {
  secret_id = "${var.name_prefix}-db-password"
  replication {
    auto {}
  }
  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "db_password" {
  secret      = google_secret_manager_secret.db_password.id
  secret_data = random_password.db.result
}

# ---------------------------------------------------------------------------
# API / control plane — the stateless `oath serve` binary on Cloud Run. It
# stores objects in the bucket, applies the repoint policy against Postgres, and
# appends signed journal entries. State lives elsewhere, so it scales to zero.
# ---------------------------------------------------------------------------
resource "google_service_account" "server" {
  account_id   = "${var.name_prefix}-server"
  display_name = "Oath registry server"
}

resource "google_storage_bucket_iam_member" "server_objects" {
  bucket = google_storage_bucket.objects.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.server.email}"
}

resource "google_secret_manager_secret_iam_member" "server_db_password" {
  secret_id = google_secret_manager_secret.db_password.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}

resource "google_project_iam_member" "server_sql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.server.email}"
}

resource "google_cloud_run_v2_service" "server" {
  name     = "${var.name_prefix}-server"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.server.email

    scaling {
      min_instance_count = 0
      max_instance_count = 10
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.index.connection_name]
      }
    }

    containers {
      image = var.server_image

      # The app reads objects from the bucket and the index from Postgres.
      # Auth is signature-based (docs/registry-auth.md); no shared token here.
      env {
        name  = "OATH_OBJECT_BUCKET"
        value = google_storage_bucket.objects.name
      }
      env {
        name  = "OATH_DB_INSTANCE"
        value = google_sql_database_instance.index.connection_name
      }
      env {
        name  = "OATH_DB_NAME"
        value = google_sql_database.registry.name
      }
      env {
        name  = "OATH_DB_USER"
        value = google_sql_user.app.name
      }
      env {
        name = "OATH_DB_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.db_password.secret_id
            version = "latest"
          }
        }
      }
      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
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

# The API is public; authorization is by signature + repoint policy, not by who
# can reach the endpoint. (Verification WORKERS for policy-gated proving would
# be a separate, CPU-heavy pool — Cloud Run jobs behind a queue — added when the
# server enforces forbid_falsified/require_total server-side.)
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.server.name
  location = google_cloud_run_v2_service.server.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
