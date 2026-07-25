//go:build cloud

package main

// Cloud backend (#14, docs/store-drivers.md): immutable content-addressed
// objects in GCS, the contended mutable state (name index, journal, proof queue)
// in Postgres with a cross-instance advisory lock. Compiled only with
// `-tags cloud`, so the default kernel build stays zero-dependency.
//
// This file uses ONLY the standard library (net/http for the GCS REST API,
// database/sql for Postgres). The Postgres DRIVER is not imported here — the
// deployment build registers one via a blank import (see backend_cloud_pgdriver
// notes in docs/store-drivers.md), e.g. `_ "github.com/lib/pq"`. That keeps this
// file compilable and reviewable with no external dependency, while the real
// driver is plugged in at build time.
//
// STATUS: the GCS object path is unit-tested (httptest). The Postgres path is
// compile-verified and its SQL is specified, but it has NOT been integration-
// tested against a live database in this repo, and it writes the non-regenerable
// journal. Validate it against a staging Postgres before pointing production at
// it. OpenStore only selects this backend when OATH_BACKEND=cloud is set
// explicitly; the deployed registry stays on the filesystem store until you opt
// in.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

func init() {
	cloudBackendOpener = func() (backend, string, error) {
		bucket := os.Getenv("OATH_OBJECT_BUCKET")
		dsn := os.Getenv("OATH_DB_DSN")
		driver := os.Getenv("OATH_DB_DRIVER")
		if bucket == "" || dsn == "" {
			return nil, "", fmt.Errorf("cloud backend needs OATH_OBJECT_BUCKET and OATH_DB_DSN")
		}
		if driver == "" {
			driver = "postgres"
		}
		pg, err := openPGIndex(driver, dsn)
		if err != nil {
			return nil, "", err
		}
		be := &cloudBackend{objects: newGCSObjects(bucket), index: pg}
		return be, "cloud:gcs+" + driver, nil
	}
}

// cloudBackend composes GCS (immutable objects/meta) with Postgres (mutable
// index/journal/queue + lock).
type cloudBackend struct {
	objects *gcsObjects
	index   *pgIndex
}

func (c *cloudBackend) getObject(h string) ([]byte, bool, error) {
	return c.objects.get("objects/" + h + ".bin")
}
func (c *cloudBackend) putObject(h string, b []byte) error {
	return c.objects.put("objects/"+h+".bin", b)
}
func (c *cloudBackend) getMeta(h string) ([]byte, bool, error) {
	return c.objects.get("meta/" + h + ".json")
}
func (c *cloudBackend) putMeta(h string, b []byte) error { return c.objects.put("meta/"+h+".json", b) }
func (c *cloudBackend) listObjects() ([]string, error)   { return c.objects.listHashes() }

func (c *cloudBackend) readNames() ([]byte, bool, error)      { return c.index.readNames() }
func (c *cloudBackend) writeNames(b []byte) error             { return c.index.writeNames(b) }
func (c *cloudBackend) readJournal() ([]byte, error)          { return c.index.readJournal() }
func (c *cloudBackend) appendJournal(line []byte) error       { return c.index.appendJournal(line) }
func (c *cloudBackend) enqueueProof(h string, b []byte) error { return c.index.enqueueProof(h, b) }
func (c *cloudBackend) claimProof(now time.Time, ttl time.Duration) ([]byte, bool, error) {
	return c.index.claimProof(now, ttl)
}
func (c *cloudBackend) completeProof(h string) error { return c.index.completeProof(h) }
func (c *cloudBackend) proofDepth() int              { return c.index.proofDepth() }
func (c *cloudBackend) lock() (func(), error)        { return c.index.lock() }

var _ backend = (*cloudBackend)(nil)

// ---------------------------------------------------------------------------
// GCS objects — immutable content-addressed blobs over the JSON REST API,
// authenticated by the Cloud Run metadata-server access token. Zero-dependency.
// ---------------------------------------------------------------------------

type gcsObjects struct {
	bucket    string
	http      *http.Client
	base      string // storage host, overridable for tests (default storage.googleapis.com)
	staticTok string // if set, used instead of the metadata server (tests)
	mu        sync.Mutex
	token     string
	exp       time.Time
}

func newGCSObjects(bucket string) *gcsObjects {
	return &gcsObjects{bucket: bucket, http: &http.Client{Timeout: 30 * time.Second}, base: "https://storage.googleapis.com"}
}

// accessToken fetches (and caches) an OAuth token from the metadata server.
func (g *gcsObjects) accessToken() (string, error) {
	if g.staticTok != "" {
		return g.staticTok, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.exp) {
		return g.token, nil
	}
	req, _ := http.NewRequest("GET",
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", err
	}
	g.token = t.AccessToken
	g.exp = time.Now().Add(time.Duration(t.ExpiresIn-60) * time.Second)
	return g.token, nil
}

func (g *gcsObjects) do(req *http.Request) (*http.Response, error) {
	tok, err := g.accessToken()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return g.http.Do(req)
}

func (g *gcsObjects) get(object string) ([]byte, bool, error) {
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media",
		g.base, g.bucket, url.PathEscape(object))
	req, _ := http.NewRequest("GET", u, nil)
	resp, err := g.do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("gcs get %s: %s", object, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	return b, err == nil, err
}

func (g *gcsObjects) put(object string, body []byte) error {
	u := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s",
		g.base, g.bucket, url.QueryEscape(object))
	req, _ := http.NewRequest("POST", u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := g.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gcs put %s: %s: %s", object, resp.Status, b)
	}
	return nil
}

func (g *gcsObjects) listHashes() ([]string, error) {
	var out []string
	pageToken := ""
	for {
		u := fmt.Sprintf("%s/storage/v1/b/%s/o?prefix=objects/", g.base, g.bucket)
		if pageToken != "" {
			u += "&pageToken=" + url.QueryEscape(pageToken)
		}
		req, _ := http.NewRequest("GET", u, nil)
		resp, err := g.do(req)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			// objects/<hash>.bin -> <hash>
			n := it.Name
			if len(n) > len("objects/")+len(".bin") {
				out = append(out, n[len("objects/"):len(n)-len(".bin")])
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Postgres index — names, journal, proof queue, and the cross-instance lock.
// database/sql only; the driver is registered by the deployment build.
// ---------------------------------------------------------------------------

const pgSchema = `
create table if not exists names   (name text primary key, hash text not null);
create table if not exists journal (seq bigserial primary key, line text not null);
create table if not exists proofq  (hash text primary key, job text not null, leased_at timestamptz);
`

type pgIndex struct {
	db   *sql.DB
	mu   sync.Mutex
	held *sql.Conn // set while the advisory lock is held; mutating writes route here
}

func openPGIndex(driver, dsn string) (*pgIndex, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if _, err := db.Exec(pgSchema); err != nil {
		return nil, fmt.Errorf("postgres schema: %w", err)
	}
	return &pgIndex{db: db}, nil
}

// exec routes a mutating statement through the lock-held connection when one is
// active (so it is serialized cross-instance), else the pool.
func (p *pgIndex) exec(q string, args ...any) error {
	p.mu.Lock()
	held := p.held
	p.mu.Unlock()
	var err error
	if held != nil {
		_, err = held.ExecContext(context.Background(), q, args...)
	} else {
		_, err = p.db.Exec(q, args...)
	}
	return err
}

func (p *pgIndex) readNames() ([]byte, bool, error) {
	rows, err := p.db.Query(`select name, hash from names`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var n, h string
		if err := rows.Scan(&n, &h); err != nil {
			return nil, false, err
		}
		m[n] = h
	}
	if len(m) == 0 {
		return nil, false, rows.Err()
	}
	b, _ := json.Marshal(m)
	return b, true, rows.Err()
}

func (p *pgIndex) writeNames(b []byte) error {
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	// Whole-index replace under the held lock — correct because the lock
	// serializes repoints cross-instance (docs/store-drivers.md notes the
	// finer-grained transactional repoint as the follow-on refinement).
	if err := p.exec(`delete from names`); err != nil {
		return err
	}
	for n, h := range m {
		if err := p.exec(`insert into names(name, hash) values($1,$2)`, n, h); err != nil {
			return err
		}
	}
	return nil
}

func (p *pgIndex) readJournal() ([]byte, error) {
	rows, err := p.db.Query(`select line from journal order by seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var buf bytes.Buffer
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		buf.WriteString(line)
	}
	return buf.Bytes(), rows.Err()
}

func (p *pgIndex) appendJournal(line []byte) error {
	return p.exec(`insert into journal(line) values($1)`, string(line))
}

func (p *pgIndex) enqueueProof(hash string, b []byte) error {
	return p.exec(`insert into proofq(hash, job, leased_at) values($1,$2,null)
	               on conflict(hash) do update set job=excluded.job`, hash, string(b))
}

func (p *pgIndex) claimProof(now time.Time, ttl time.Duration) ([]byte, bool, error) {
	// Real single-dispatch: lock one available (or stale-leased) row, mark it
	// leased, return its job.
	var job string
	err := p.db.QueryRow(`
		update proofq set leased_at=$1
		where hash = (
			select hash from proofq
			where leased_at is null or leased_at < $2
			order by hash limit 1
			for update skip locked
		)
		returning job`, now, now.Add(-ttl)).Scan(&job)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(job), true, nil
}

func (p *pgIndex) completeProof(hash string) error {
	return p.exec(`delete from proofq where hash=$1`, hash)
}

func (p *pgIndex) proofDepth() int {
	var n int
	_ = p.db.QueryRow(`select count(*) from proofq`).Scan(&n)
	return n
}

// lock takes a session advisory lock on a dedicated connection and routes
// mutating writes through it until released — serializing the name-index and
// journal read-modify-write across every serve/worker instance.
func (p *pgIndex) lock() (func(), error) {
	ctx := context.Background()
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `select pg_advisory_lock(1)`); err != nil {
		conn.Close()
		return nil, err
	}
	p.mu.Lock()
	p.held = conn
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		p.held = nil
		p.mu.Unlock()
		_, _ = conn.ExecContext(ctx, `select pg_advisory_unlock(1)`)
		conn.Close()
	}, nil
}
