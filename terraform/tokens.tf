# Bearer-token auth for `oath serve` (docs/teamstore.md). The server maps a
# token to an authenticated principal and ignores any client-supplied author.
# Tokens are secrets — they live in Secret Manager, never in the store bucket or
# in git.
#
# So the first deploy comes up usable, Terraform mints ONE initial principal
# ("admin") and outputs its token (sensitive). Add or rotate principals by
# writing a new secret version (a JSON object of token -> {principal}); the
# server reads "latest".

resource "random_password" "admin_token" {
  length  = 40
  special = false
}

resource "google_secret_manager_secret" "tokens" {
  secret_id = "${var.name_prefix}-tokens"
  replication {
    auto {}
  }
  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "tokens" {
  secret = google_secret_manager_secret.tokens.id
  # The initial admin is write-capable (it's the operator/bootstrap principal).
  # Additional principals you add later default to READ-ONLY unless you set
  # "write": true — a bearer token should not author or move names by default.
  secret_data = jsonencode({
    (random_password.admin_token.result) = { principal = "admin", write = true }
  })
}

resource "google_secret_manager_secret_iam_member" "server_tokens" {
  secret_id = google_secret_manager_secret.tokens.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}
