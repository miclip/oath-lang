# CDN for the object store. Content-addressed objects never change, so they are
# the ideal CDN case: an aggressive, effectively-permanent cache with global
# reach and near-zero origin cost. This is the read path — the cheap one — and
# it needs no trusted compute at all, because clients verify by hash.
#
# Only created when objects are public (var.objects_public). If reads go through
# the API instead, delete this file.

resource "google_compute_backend_bucket" "objects" {
  count       = var.objects_public ? 1 : 0
  name        = "${var.name_prefix}-objects-cdn"
  bucket_name = google_storage_bucket.objects.name
  enable_cdn  = true

  cdn_policy {
    cache_mode = "CACHE_ALL_STATIC"
    # Immutable content: cache long and hard.
    default_ttl = 31536000
    max_ttl     = 31536000
    client_ttl  = 31536000
  }
}

resource "google_compute_url_map" "objects" {
  count           = var.objects_public ? 1 : 0
  name            = "${var.name_prefix}-objects-urlmap"
  default_service = google_compute_backend_bucket.objects[0].id
}

resource "google_compute_target_http_proxy" "objects" {
  count   = var.objects_public ? 1 : 0
  name    = "${var.name_prefix}-objects-proxy"
  url_map = google_compute_url_map.objects[0].id
}

resource "google_compute_global_address" "objects" {
  count = var.objects_public ? 1 : 0
  name  = "${var.name_prefix}-objects-ip"
}

resource "google_compute_global_forwarding_rule" "objects" {
  count      = var.objects_public ? 1 : 0
  name       = "${var.name_prefix}-objects-fr"
  target     = google_compute_target_http_proxy.objects[0].id
  ip_address = google_compute_global_address.objects[0].id
  port_range = "80"
}
