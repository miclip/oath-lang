package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
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

	got, canWrite, signed, ok := authenticatePrincipal(r, body, nil, nil)
	if !signed {
		t.Fatal("valid signature must report signed=true")
	}
	if !ok || got != pubHex || !canWrite {
		t.Fatalf("valid signature: got (%q,write=%v,%v), want (%q,true,true)", got, canWrite, ok, pubHex)
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
	if _, _, _, ok := authenticatePrincipal(r, tampered, tokens, nil); ok {
		t.Fatal("invalid signature authenticated (or fell through to token)")
	}
}

// Bearer tokens still work when no signature headers are present, and carry
// their granted capability: read-only by default, write only if scoped.
func TestAuthBearerToken(t *testing.T) {
	tokens := map[string]tokenEntry{
		"tok-readonlyabcd12": {Principal: "alice"},
		"tok-writerxyz90abc": {Principal: "bob", Write: true},
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer tok-readonlyabcd12")
	got, canWrite, _, ok := authenticatePrincipal(r, []byte("{}"), tokens, nil)
	if !ok || got != "alice" || canWrite {
		t.Fatalf("read-only token: got (%q,write=%v,%v), want (alice,false,true)", got, canWrite, ok)
	}

	r2 := httptest.NewRequest("POST", "/mcp", nil)
	r2.Header.Set("Authorization", "Bearer tok-writerxyz90abc")
	if got, cw, _, ok := authenticatePrincipal(r2, []byte("{}"), tokens, nil); !ok || got != "bob" || !cw {
		t.Fatalf("write token: got (%q,write=%v,%v), want (bob,true,true)", got, cw, ok)
	}

	// No auth at all → rejected.
	if _, _, _, ok := authenticatePrincipal(httptest.NewRequest("POST", "/mcp", nil), []byte("{}"), tokens, nil); ok {
		t.Fatal("unauthenticated request accepted")
	}
}

// A read-only principal can read but not author: put is refused, ls is allowed.
func TestCapabilityGate(t *testing.T) {
	st := newStore(t)
	putArgs := []byte(`{"source":"(defn z [] [] Int 1)"}`)

	if _, err := mcpCallTool(st, "put", putArgs, "alice", false, false, false); err == nil {
		t.Fatal("read-only principal was allowed to put")
	}
	if _, err := mcpCallTool(st, "ls", nil, "alice", false, false, false); err != nil {
		t.Fatalf("read-only principal blocked from ls: %v", err)
	}
	if _, err := mcpCallTool(st, "put", putArgs, "bob", true, false, false); err != nil {
		t.Fatalf("write principal blocked from put: %v", err)
	}
	if h, ok := st.Resolve("z"); !ok || h == "" {
		t.Fatal("write put did not bind the name")
	}
}

// The registration gate (#66): with an authorized-keys allowlist, only listed
// keys may WRITE; an unlisted key still authenticates and reads. Empty allowlist
// = open contribution.
func TestRegistrationGate(t *testing.T) {
	body := []byte(`{}`)
	apub, apriv, _ := ed25519.GenerateKey(nil)
	upub, upriv, _ := ed25519.GenerateKey(nil)
	aHex, uHex := hex.EncodeToString(apub), hex.EncodeToString(upub)
	allow := map[string]bool{aHex: true}

	sign := func(pub ed25519.PublicKey, priv ed25519.PrivateKey) *http.Request {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.Header.Set("X-Oath-Pubkey", hex.EncodeToString(pub))
		r.Header.Set("X-Oath-Signature", hex.EncodeToString(ed25519.Sign(priv, body)))
		return r
	}

	if p, cw, sg, ok := authenticatePrincipal(sign(apub, apriv), body, nil, allow); !ok || !cw || !sg || p != aHex {
		t.Fatalf("authorized key: (%q,write=%v,signed=%v,%v), want write+signed", p, cw, sg, ok)
	}
	if p, cw, sg, ok := authenticatePrincipal(sign(upub, upriv), body, nil, allow); !ok || cw || !sg || p != uHex {
		t.Fatalf("unlisted key: (%q,write=%v,signed=%v,%v), want authenticated, signed, no write", p, cw, sg, ok)
	}
	if _, cw, _, ok := authenticatePrincipal(sign(upub, upriv), body, nil, nil); !ok || !cw {
		t.Fatal("open contribution (nil allowlist): any signer should write")
	}

	// A bearer token must report signed=false even when its PRINCIPAL NAME is a
	// pubkey hex — which is a real configuration (the live registry's token
	// principal was repointed to an operator's pubkey). Inferring "was signed"
	// from the principal string would misdiagnose exactly this case, telling a
	// token holder their key is missing from the allowlist.
	toks := map[string]tokenEntry{"t0k": {Principal: aHex, Write: false}}
	rt := httptest.NewRequest("POST", "/mcp", nil)
	rt.Header.Set("Authorization", "Bearer t0k")
	if p, cw, sg, ok := authenticatePrincipal(rt, body, toks, allow); !ok || cw || sg || p != aHex {
		t.Fatalf("token with pubkey-shaped principal: (%q,write=%v,signed=%v,%v), want authenticated, NOT signed, no write", p, cw, sg, ok)
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

// #70: a serve instance and a prove-worker are separate processes over one
// store. A reader that cached metadata at startup kept serving the verdicts it
// saw then — the registry's proven count sat frozen for hours while the worker
// had actually advanced the store, and only a redeploy revealed it. Simulate
// exactly that: a SECOND Store over the same root records a proof, and the
// first must observe it after RefreshMutable.
func TestServeObservesOutOfBandVerdicts(t *testing.T) {
	root := t.TempDir()
	reader, err := OpenStore(root)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := apiPut(reader, `(defn oob [] [] Int 1
		(prop triv [(x Int)] (== x x)))`, "test", ""); err != nil {
		t.Fatal(err)
	}
	h, _ := reader.Resolve("oob")

	// Warm the reader's cache, as a long-lived server does.
	if m, err := reader.GetMeta(h); err != nil || len(m.ProvenProps) != 0 {
		t.Fatalf("baseline: err=%v proven=%v", err, m.ProvenProps)
	}

	// A DIFFERENT process (a worker) records a proof against the same root.
	worker, err := OpenStore(root)
	if err != nil {
		t.Fatalf("OpenStore worker: %v", err)
	}
	wm, err := worker.GetMeta(h)
	if err != nil {
		t.Fatal(err)
	}
	wm.ProvenProps = []int{0}
	wm.Guarantee.Level = "proven"
	if err := worker.SetMeta(h, wm); err != nil {
		t.Fatalf("worker SetMeta: %v", err)
	}

	// Without refreshing, the reader is stale — this is the bug.
	if m, _ := reader.GetMeta(h); len(m.ProvenProps) != 0 {
		t.Fatal("precondition failed: reader was not actually caching")
	}
	// After refreshing, it must see the worker's verdict.
	reader.RefreshMutable()
	m, err := reader.GetMeta(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ProvenProps) != 1 || m.Guarantee.Level != "proven" {
		t.Fatalf("stale after refresh: proven=%v level=%q", m.ProvenProps, m.Guarantee.Level)
	}

	// Objects are content-addressed and must stay cached across a refresh —
	// dropping them would be pure cost with no correctness gain.
	if _, err := reader.GetDef(h); err != nil {
		t.Fatalf("GetDef after refresh: %v", err)
	}
}
