# The AUTHORIZED-KEYS gate for signature auth (#83).
#
# WHY THIS EXISTS: `oath serve` computes a signed request's write capability as
#
#     canWrite := authKeys == nil || authKeys[pubHex]
#
# so a server started WITHOUT an authorized-keys file treats every valid
# Ed25519 signature as write-capable. The live registry ran exactly that way:
# --authorized-keys was never passed and OATH_AUTHORIZED_KEYS was never set, so
# any keypair could publish to the public registry and repoint names. That was
# expensive to reach while no client spoke signature auth; `oath put --remote`
# reduced it to one command, which is what made closing it urgent rather than
# theoretical.
#
# FAIL CLOSED. The default is an EMPTY list, which is deliberate: an empty JSON
# array parses to a non-nil, empty key set, so every signed write is refused
# while reads keep working. An unset variable therefore denies publication rather
# than granting it to everyone — the opposite of the behaviour being fixed.
#
# The key values are NOT committed. Public keys are safe to publish but doing so
# permanently binds a git identity to a registry principal, and the authoritative
# list of who may publish belongs to the server, not to a file in the repo (see
# the *.pub rule in .gitignore). Supply them at apply time via a tfvars file or
# -var, and rotate by writing a new secret version — the server reads "latest".
#
# SCOPE, so this is not oversold: this gates the SIGNATURE path only. Bearer
# tokens are authenticated on a separate branch and remain a write path
# regardless, so the effect is to narrow publication to "listed keys plus
# write-capable tokens", not to seal it.

variable "authorized_publish_keys" {
  description = <<-EOT
    Hex-encoded Ed25519 public keys permitted to PUBLISH via signature auth.
    Empty (the default) denies all signed writes and still serves reads — fail
    closed. Never commit real key values here; pass them at apply time.
  EOT
  type        = list(string)
  default     = []

  validation {
    # A malformed entry would be rejected by loadAuthorizedKeys at STARTUP, and
    # `oath serve` exits on a corrupt file — so a typo here takes the registry
    # down rather than merely failing to authorize one key. Catch it at plan time.
    condition = alltrue([
      for k in var.authorized_publish_keys : can(regex("^[0-9a-f]{64}$", k))
    ])
    error_message = "Each key must be exactly 64 lowercase hex characters (a 32-byte Ed25519 public key). Uppercase hex is rejected: the server compares the hex string, so ABAB… and abab… are different principals."
  }
}

resource "google_secret_manager_secret" "authorized_keys" {
  secret_id = "${var.name_prefix}-authorized-keys"
  replication {
    auto {}
  }
  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "authorized_keys" {
  secret = google_secret_manager_secret.authorized_keys.id
  # A bare JSON array of hex pubkeys — the shape loadAuthorizedKeys parses.
  secret_data = jsonencode(var.authorized_publish_keys)
}

resource "google_secret_manager_secret_iam_member" "server_authorized_keys" {
  secret_id = google_secret_manager_secret.authorized_keys.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}
