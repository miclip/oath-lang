package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #189: with --public-reads a request carrying NO credentials is served as an
// anonymous read-only principal; a request whose credentials FAILED to validate is
// never laundered into anonymous access, and without --public-reads nothing is.
func TestAnonymousReadEligible(t *testing.T) {
	cases := []struct {
		name                     string
		public                   bool
		pubkey, signature, authz string
		want                     bool
	}{
		{"public + no creds -> anonymous", true, "", "", "", true},
		{"public + a (failed) signature -> NOT anonymous", true, "deadbeef", "bad", "", false},
		{"public + a pubkey alone -> NOT anonymous", true, "deadbeef", "", "", false},
		{"public + a (failed) bearer token -> NOT anonymous", true, "", "", "Bearer nope", false},
		{"closed + no creds -> 401, never anonymous", false, "", "", "", false},
		{"closed + everything -> never anonymous", false, "deadbeef", "sig", "Bearer x", false},
	}
	for _, c := range cases {
		if got := anonymousReadEligible(c.public, c.pubkey, c.signature, c.authz); got != c.want {
			t.Errorf("%s: anonymousReadEligible(%v,%q,%q,%q) = %v, want %v",
				c.name, c.public, c.pubkey, c.signature, c.authz, got, c.want)
		}
	}
}

// Public reads must NOT enable public writes: an anonymous principal (canWrite=false,
// signed=false) is refused every state-changing tool, so --public-reads widens reads
// only. Guards the invariant the flag's whole design rests on.
func TestAnonymousPrincipalCannotWrite(t *testing.T) {
	st, err := newStoreWithBackend(newMemBackend(), "mem")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"put", "reserve", "delegate"} {
		_, err := mcpCallTool(st, tool, json.RawMessage(`{}`), "anonymous", false, false, true)
		if err == nil {
			t.Fatalf("anonymous principal was allowed to call %q", tool)
		}
		if !strings.Contains(err.Error(), "read-only") && !strings.Contains(err.Error(), "SIGNED") {
			t.Fatalf("%q refused anonymous, but not for lack of write capability: %v", tool, err)
		}
	}
}

// The client side of #189: a nil signer must produce a genuinely ANONYMOUS request —
// no X-Oath-Pubkey, no X-Oath-Signature, no Authorization — so a --public-reads
// registry can serve it and a closed one can cleanly 401 it. A stray credential here
// would defeat the "read public code with nothing" story.
func TestNilSignerSendsAnonymousRequest(t *testing.T) {
	var pubkey, sig, authz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pubkey, sig, authz = r.Header.Get("X-Oath-Pubkey"), r.Header.Get("X-Oath-Signature"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}],"isError":false}}`)
	}))
	defer srv.Close()

	out, err := mcpCallSignedBy(context.Background(), srv.URL, nil, "ls", map[string]any{})
	if err != nil {
		t.Fatalf("anonymous read failed: %v", err)
	}
	if out != "ok" {
		t.Fatalf("got %q, want ok", out)
	}
	if pubkey != "" || sig != "" || authz != "" {
		t.Fatalf("nil signer sent credentials: pubkey=%q sig=%q authz=%q", pubkey, sig, authz)
	}
}
