package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(nil)
	f := filepath.Join(t.TempDir(), "k.key")
	if err := os.WriteFile(f, []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func jsonRPC(w http.ResponseWriter, text string, isErr bool) {
	w.Header().Set("Content-Type", "application/json")
	e := "false"
	if isErr {
		e = "true"
	}
	io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":`+jsonQuote(text)+`}],"isError":`+e+`}}`)
}

func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Friction item #2 (publish-consumer-friction.md): a refused publication is HTTP 200
// with isError set, and remotePut once read only the content — so cmdRemotePut printed
// the refusal and exited 0, reporting a publication that never happened as success.
// A refusal must now come back as a Go error (so the command exits nonzero), and a
// success must not.
func TestRemotePutSurfacesToolRefusal(t *testing.T) {
	key := writeTestKey(t)
	src := "(defn x [] [] Int 1)"

	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonRPC(w, "error: new names require a signed publication", true)
	}))
	defer refuse.Close()
	if _, err := remotePut(refuse.URL, key, src, ""); err == nil {
		t.Fatal("a refused publication must return an error, not be swallowed as output (exit 0)")
	} else if strings.HasPrefix(err.Error(), "error:") {
		t.Fatalf("the server prefix was not stripped (double error:): %q", err.Error())
	}

	accept := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonRPC(w, "✓ x  #abc  tested", false)
	}))
	defer accept.Close()
	out, err := remotePut(accept.URL, key, src, "")
	if err != nil {
		t.Fatalf("a successful publication must not error: %v", err)
	}
	if !strings.Contains(out, "x") {
		t.Fatalf("success output not returned: %q", out)
	}
}
