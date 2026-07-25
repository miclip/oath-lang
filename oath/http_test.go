package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http/httptest"
	"testing"
)

func signedRequest(t *testing.T, body []byte) (pubHex string, r *reqHeaders) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex = hex.EncodeToString(pub)
	sig := hex.EncodeToString(ed25519.Sign(priv, body))
	return pubHex, &reqHeaders{pub: pubHex, sig: sig}
}

type reqHeaders struct{ pub, sig string }

// A valid Ed25519 signature over the body authenticates the caller as its key;
// the principal IS the pubkey.
func TestAuthSignatureValid(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	pubHex, h := signedRequest(t, body)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("X-Oath-Pubkey", h.pub)
	r.Header.Set("X-Oath-Signature", h.sig)

	got, ok := authenticatePrincipal(r, body, nil)
	if !ok || got != pubHex {
		t.Fatalf("valid signature: got (%q,%v), want (%q,true)", got, ok, pubHex)
	}
}

// A present-but-invalid signature is a hard rejection — never a silent
// fall-through to token auth (which would launder a forged key).
func TestAuthSignatureInvalidRejected(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	_, h := signedRequest(t, body)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("X-Oath-Pubkey", h.pub)
	r.Header.Set("X-Oath-Signature", h.sig)
	// Tamper the body after signing.
	tampered := append([]byte(nil), body...)
	tampered[0] = '['

	// Even with a valid token present, the bad signature must win → rejected.
	tokens := map[string]tokenEntry{"tok-abcdefabcdef12": {Principal: "admin"}}
	r.Header.Set("Authorization", "Bearer tok-abcdefabcdef12")
	if _, ok := authenticatePrincipal(r, tampered, tokens); ok {
		t.Fatal("invalid signature authenticated (or fell through to token)")
	}
}

// Bearer tokens still work when no signature headers are present.
func TestAuthBearerToken(t *testing.T) {
	tokens := map[string]tokenEntry{"tok-abcdefabcdef12": {Principal: "alice"}}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer tok-abcdefabcdef12")

	got, ok := authenticatePrincipal(r, []byte("{}"), tokens)
	if !ok || got != "alice" {
		t.Fatalf("token auth: got (%q,%v), want (alice,true)", got, ok)
	}
	// No auth at all → rejected.
	if _, ok := authenticatePrincipal(httptest.NewRequest("POST", "/mcp", nil), []byte("{}"), tokens); ok {
		t.Fatal("unauthenticated request accepted")
	}
}

// A name reserved to a key (owner_pubkey) may only be repointed by that key;
// a put authored by any other principal is blocked.
func TestOwnerPubkeyScopedRepoint(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	owner := hex.EncodeToString(pub)
	other := hex.EncodeToString([]byte("00000000000000000000000000000000"))

	st := newStore(t)
	writePolicy(t, st, `{"rules":[{"names":["foo"],"owner_pubkey":"`+owner+`"}]}`)

	// The owner binds the name.
	reps, err := apiPut(st, `(defn foo [] [] Int 1)`, owner, "")
	if err != nil {
		t.Fatal(err)
	}
	if reps[len(reps)-1].Status != "accepted" {
		t.Fatalf("owner put: status=%q, want accepted", reps[len(reps)-1].Status)
	}

	// A different principal tries to move it → blocked, name unchanged.
	reps, err = apiPut(st, `(defn foo [] [] Int 2)`, other, "")
	if err != nil {
		t.Fatal(err)
	}
	if reps[len(reps)-1].Status != "blocked" {
		t.Fatalf("impostor put: status=%q, want blocked", reps[len(reps)-1].Status)
	}
	if h, _ := st.Resolve("foo"); h == "" {
		t.Fatal("name lost")
	}
}
