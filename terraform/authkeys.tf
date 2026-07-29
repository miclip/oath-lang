# An OPTIONAL, OFF-BY-DEFAULT allowlist for signature auth — an operator lever,
# NOT the onboarding model.
#
# Open contribution is a DESIGN DECISION, not an oversight (#66): the registry is
# public and re-derives every verdict from content bytes, so a hostile publish
# cannot forge evidence — it can only add an object that will be independently
# re-verified. `owner_pubkey` in policy.json locks the names that matter. An
# allowlist is therefore not what makes this registry safe, and switching it on
# by default would close a door that is open on purpose.
#
# WHY THIS IS NOT REGISTRATION. A Secret Manager allowlist means the only way to
# register a key is to hold GCP credentials and run terraform. That makes
# onboarding an infrastructure operation and the operator a permanent
# bottleneck — no new user who is not already an infra admin could ever publish.
# Public-registry onboarding must be self-service, so the real path is:
#
#   - anyone may publish (open contribution, verdicts re-derived on arrival);
#   - a key CLAIMS a namespace prefix on first publish, first-come;
#   - only that key may repoint names under it (#84 generalizes owner_pubkey from
#     an operator-edited per-name rule to a claimable prefix).
#
# Public keys also simply are not secrets, so Secret Manager is the wrong store
# for them on its own terms; the claimable-ownership record belongs in the store,
# which is mutable registry DATA rather than deployment config.
#
# WHAT THIS IS FOR: incident response and private deployments. If the public
# registry is being abused, or someone runs a closed internal instance, an
# operator can restrict signed writes to named keys without a code change. Both
# are legitimate; neither is onboarding.
#
# FAIL-SAFE DEFAULT: empty (the default) creates NOTHING and sets no env var, so
# `oath serve` sees authKeys==nil and contribution stays open. This matters
# because an empty allowlist is not neutral — a file containing [] parses to a
# non-nil EMPTY set and denies every signed write, so writing [] would silently
# close the registry. The list is therefore all-or-nothing by construction:
# absent means open, non-empty means exactly those keys.
#
# SCOPE: gates the SIGNATURE path only. Bearer tokens authenticate on a separate
# branch and stay write-capable, so enabling this narrows publication to "listed
# keys plus write tokens" rather than sealing it.

variable "authorized_publish_keys" {
  description = <<-EOT
    OPTIONAL operator lever, off by default. Hex Ed25519 public keys permitted to
    publish via signature auth. Empty (default) leaves contribution OPEN, which is
    the intended posture for the public registry (#66) — do not set this to close
    a registry you meant to leave open. Use for incident response or a private
    deployment. Never commit real key values; pass at apply time.
  EOT
  type        = list(string)
  default     = []

  validation {
    # A malformed entry is rejected by loadAuthorizedKeys at STARTUP and `oath
    # serve` exits, so a typo takes the registry DOWN rather than merely failing
    # to authorize one key. Catch it at plan time instead.
    condition = alltrue([
      for k in var.authorized_publish_keys : can(regex("^[0-9a-f]{64}$", k))
    ])
    error_message = "Each key must be exactly 64 lowercase hex characters (a 32-byte Ed25519 public key). Uppercase hex is rejected: the server compares the hex string, so ABAB… and abab… would be different principals."
  }
}

locals {
  authkeys_active = length(var.authorized_publish_keys) > 0
}

resource "google_secret_manager_secret" "authorized_keys" {
  count     = local.authkeys_active ? 1 : 0
  secret_id = "${var.name_prefix}-authorized-keys"
  replication {
    auto {}
  }
  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "authorized_keys" {
  count  = local.authkeys_active ? 1 : 0
  secret = google_secret_manager_secret.authorized_keys[0].id
  # A bare JSON array of hex pubkeys — the shape loadAuthorizedKeys parses.
  secret_data = jsonencode(var.authorized_publish_keys)
}

resource "google_secret_manager_secret_iam_member" "server_authorized_keys" {
  count     = local.authkeys_active ? 1 : 0
  secret_id = google_secret_manager_secret.authorized_keys[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}
