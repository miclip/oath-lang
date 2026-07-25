# Custom domain for the serve API (e.g. registry.oath-lang.org). Cloud Run's
# managed domain mapping: Google provisions and renews the TLS cert; you add one
# DNS record (emitted as the custom_domain_dns output) at whatever hosts the
# parent domain. Gated on var.custom_domain — empty leaves the service on its
# run.app URL.
#
# PREREQUISITE: the parent domain (oath-lang.org) must be verified for this
# project in Google Search Console (https://search.google.com/search-console),
# and the deployer must be a verified owner, or the mapping fails to create. This
# is a one-time manual step; see docs/deploy.md.

resource "google_cloud_run_domain_mapping" "registry" {
  count    = var.custom_domain == "" ? 0 : 1
  location = var.region
  name     = var.custom_domain

  metadata {
    namespace = var.project_id
  }
  spec {
    route_name = google_cloud_run_v2_service.server.name
  }
}
