package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// SOURCE PUBLICATION IS UNSIGNED, and `put` says so rather than pretending.
//
// `put` once accepted an author statement beside the source (#83) and
// re-elaborated the source to check it against the signed artifact — two
// derivations compared for agreement. That path is removed (#102), and a client
// still sending a statement is making a claim the server can no longer honour.
// Dropping the fields silently would journal an UNSIGNED publication the caller
// believes it signed, so the request is refused and pointed at `put_object`.
//
// The statement used here is HONEST — correctly signed, for this source's real
// artifact, by the authenticated principal — so the only thing the refusal can
// be about is the path, not the statement.
func TestPutRefusesAnAuthorStatement(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	st := newMemStoreForTest(t)
	env := pubEnvelope{Op: "put", Name: "dbl", Artifact: artifactHashOf(t, st, gateSrc),
		Parent: noParent, ParentRev: firstRev(), Author: pubHex, License: noLicense}
	sig, err := envelopeSign(priv, env)
	if err != nil {
		t.Fatal(err)
	}
	envB64 := encodeEnvelopeB64(envelopeEncode(env))

	args := func(t *testing.T, extra map[string]any) []byte {
		t.Helper()
		m := map[string]any{"source": gateSrc}
		for k, v := range extra {
			m[k] = v
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	for _, tc := range []struct {
		label string
		extra map[string]any
	}{
		{"envelope and signature", map[string]any{"envelope": envB64, "signature": sig}},
		// Either alone is enough: a client that sends one meant to sign.
		{"envelope only", map[string]any{"envelope": envB64}},
		{"signature only", map[string]any{"signature": sig}},
	} {
		// Every transport: the old path admitted a statement only on a SIGNED
		// request, so the signed hosted case is the one that used to succeed and
		// the one a stale client will retry on.
		for _, tr := range []struct {
			label          string
			principal      string
			signed, hosted bool
		}{
			{"signed, hosted", pubHex, true, true},
			{"signed, local", pubHex, true, false},
			{"bearer, hosted", "agent", false, true},
			{"stdio", "", false, false},
		} {
			t.Run(tc.label+" / "+tr.label, func(t *testing.T) {
				st := newMemStoreForTest(t)
				_, err := mcpCallTool(st, "put", args(t, tc.extra), tr.principal, true, tr.signed, tr.hosted)
				if err == nil {
					t.Fatal("`put` accepted an author statement: either it re-elaborated source to check a signature (the path #102 removed) or it dropped the statement on the floor")
				}
				for _, want := range []string{"put_object", "unsigned"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal does not say %q — a client needs to be told where the path moved:\n%v", want, err)
					}
				}
				if nameExists(st, "dbl") {
					t.Error("the refused request bound the name anyway")
				}
				if n := len(st.ReadLog()); n != 0 {
					t.Errorf("the refused request journalled %d entries: a refusal to READ a request is not an attempt to publish", n)
				}
			})
		}
	}

	// CONTROL: the identical source WITHOUT a statement publishes, so the refusal
	// above is about the statement and not about the source or the transport.
	st2 := newMemStoreForTest(t)
	if _, err := mcpCallTool(st2, "put", args(t, nil), "", true, false, false); err != nil {
		t.Fatalf("control dead: unsigned source publication failed: %v", err)
	}
	if !nameExists(st2, "dbl") {
		t.Fatal("control dead: unsigned source publication did not bind the name")
	}

	// And the statement is a GOOD one: it publishes through the path it belongs
	// to, which is what the refusal directs the client towards.
	st3 := newMemStoreForTest(t)
	reps, err := publishObject(t, st3, gateSrc, pubHex, &pubAuth{Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: pubHex})
	if err != nil || len(reps) != 1 || reps[0].Status != "accepted" {
		t.Fatalf("control dead: the statement `put` refused is not acceptable to put_object either, so the refusal's redirection is wrong: %+v (%v)", reps, err)
	}
}
