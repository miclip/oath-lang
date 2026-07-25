//go:build cloud

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeGCS is an in-memory stand-in for the GCS JSON API, enough to exercise the
// object backend's request construction and get/put/list round-trip.
func fakeGCS(t *testing.T, bucket string) (*gcsObjects, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	blobs := map[string][]byte{}
	prefix := "/storage/v1/b/" + bucket + "/o"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/upload/storage/v1/b/"+bucket+"/o":
			name := r.URL.Query().Get("name")
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			blobs[name] = b
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		case r.Method == "GET" && strings.HasPrefix(r.URL.EscapedPath(), prefix+"/"):
			esc := strings.TrimPrefix(r.URL.EscapedPath(), prefix+"/")
			name, _ := url.PathUnescape(esc)
			b, ok := blobs[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		case r.Method == "GET" && r.URL.Path == prefix:
			var items []map[string]string
			for k := range blobs {
				if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
					items = append(items, map[string]string{"name": k})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	g := newGCSObjects(bucket)
	g.base = srv.URL
	g.staticTok = "test-token"
	return g, srv
}

func TestGCSObjectsRoundTrip(t *testing.T) {
	g, srv := fakeGCS(t, "b")
	defer srv.Close()

	// Missing object → (nil, false, nil).
	if _, ok, err := g.get("objects/deadbeef.bin"); ok || err != nil {
		t.Fatalf("missing get: ok=%v err=%v", ok, err)
	}
	// Put then get round-trips the bytes.
	if err := g.put("objects/abc123.bin", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	b, ok, err := g.get("objects/abc123.bin")
	if err != nil || !ok || string(b) != "hello" {
		t.Fatalf("get after put: %q ok=%v err=%v", b, ok, err)
	}
	// listHashes strips objects/…​.bin down to the hash.
	_ = g.put("objects/zzz999.bin", []byte("x"))
	_ = g.put("meta/abc123.json", []byte("{}")) // must NOT appear in object list
	got, err := g.listHashes()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"abc123": true, "zzz999": true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("listHashes = %v, want the two object hashes only", got)
	}
}
