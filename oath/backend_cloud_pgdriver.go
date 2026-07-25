//go:build cloud

package main

// The Postgres driver for the cloud backend's database/sql index (backend_cloud.go).
// It lives in its own cloud-tagged file so the default kernel build stays
// dependency-free — this is the ONLY place the tree references an external
// database driver, and only `-tags cloud` (the registry image + the Postgres
// integration test) compiles it. lib/pq registers itself under the name
// "postgres", which is OATH_DB_DRIVER's default.
import _ "github.com/lib/pq"
