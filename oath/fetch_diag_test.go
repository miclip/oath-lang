package main

import (
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #190, defect 1: a hydrate fetch error must NAME the lock dependency the failing
// hash is bound to, not print a bare hash — and must say plainly when the hash is a
// transitive object the lock binds no name to.
func TestHydrateFetchLabelNamesTheDependency(t *testing.T) {
	full := strings.Repeat("a", 64)
	lock := oathLock{
		Format:       oathLockFormat,
		Dependencies: map[string]string{"show-nat": full},
		Closure:      []string{full, strings.Repeat("b", 64)},
	}
	if got := hydrateFetchLabel(lock, full); !strings.Contains(got, "show-nat") || !strings.Contains(got, "#"+full[:12]) {
		t.Fatalf("a bound hash must be named: got %q", got)
	}
	// Same-hash aliases are supported: every binding name must appear, sorted and
	// deterministic, never whichever the map yields first.
	aliased := oathLock{Dependencies: map[string]string{"rot-f": full, "rot": full}}
	if got := hydrateFetchLabel(aliased, full); got != "rot/rot-f (#"+full[:12]+")" {
		t.Fatalf("aliases must all appear, sorted: got %q", got)
	}

	trans := strings.Repeat("b", 64)
	got := hydrateFetchLabel(lock, trans)
	if strings.Contains(got, "show-nat") {
		t.Fatalf("a transitive hash must not borrow a name: got %q", got)
	}
	if !strings.Contains(got, "transitive object") || !strings.Contains(got, "#"+trans[:12]) {
		t.Fatalf("a transitive hash must be labelled as such, with its short hash: got %q", got)
	}
}

type edTestSigner struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (s edTestSigner) PublicKey(context.Context) (ed25519.PublicKey, error) { return s.pub, nil }
func (s edTestSigner) Sign(_ context.Context, m []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, m), nil
}
func (s edTestSigner) Description() string { return "test" }

// #190, defect 2: the server marks a tool failure with isError AND prefixes the text
// with "error: " (handleRPC). The client must not carry that prefix into its Go
// error, or fail() double-prefixes it. A regression here reintroduces the doubled
// "error: error:" a remote consumer measured.
func TestRemoteToolErrorHasSinglePrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Exactly the shape handleRPC emits for a missing object.
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"error: no object #deadbeefdead in this store"}],"isError":true}}`)
	}))
	defer srv.Close()

	pub, priv, _ := ed25519.GenerateKey(nil)
	_, err := mcpCallSignedBy(context.Background(), srv.URL, edTestSigner{priv, pub}, "object", map[string]any{"hash": "deadbeefdead"})
	if err == nil {
		t.Fatal("an isError response must surface as a Go error")
	}
	if strings.HasPrefix(err.Error(), "error:") {
		t.Fatalf("client re-prefixed the server error (fail() would double it): %q", err.Error())
	}
	if err.Error() != "no object #deadbeefdead in this store" {
		t.Fatalf("want the server message with its prefix stripped, got %q", err.Error())
	}
}
