package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"
)

// The client must transmit the EXACT envelope bytes it signed, base64-encoded —
// not base64 of a re-encoding. A re-encoding that differed by one byte would
// attest to a statement the registry never sees, and the failure would surface
// as the registry rejecting a signature the client believes is correct.
func TestPutObjectArgsCarryTheExactSignedBytes(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	env := pubEnvelope{Op: "put", Name: "alice/two", Artifact: hashOfBytes([]byte("x")),
		Parent: noParent, ParentRev: big.NewInt(0), Author: pubHex, License: "MIT"}
	signed := envelopeEncode(env)
	sig := hex.EncodeToString(ed25519.Sign(priv, signed))

	args := putObjectArgs("T0JK", &objectNaming{ParamNames: []string{"n"}}, string(signed), sig)

	// The envelope must decode back to the byte-identical statement that was
	// signed, and the signature must verify over THOSE bytes.
	got, err := decodeEnvelopeB64(args["envelope"].(string))
	if err != nil {
		t.Fatalf("transmitted envelope is not canonical base64: %v", err)
	}
	if string(got) != string(signed) {
		t.Fatalf("transmitted envelope differs from the signed bytes:\n got %q\nwant %q", got, signed)
	}
	raw, _ := hex.DecodeString(args["signature"].(string))
	if !ed25519.Verify(pub, got, raw) {
		t.Fatal("the signature does not verify over the transmitted envelope bytes")
	}
	if args["object"] != "T0JK" {
		t.Fatalf("object octets were altered in transit: %v", args["object"])
	}

	// naming must marshal as an OBJECT, matching the tool's declared schema — a
	// JSON string here is what the server's schema explicitly does not accept.
	body, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back["naming"].(map[string]any); !ok {
		t.Fatalf("naming did not marshal as an object: %T", back["naming"])
	}

	// Absent naming must be OMITTED rather than sent as null: the server treats
	// "no vocabulary" as legal and fits positional names, and a null would have
	// to be special-cased on the far side.
	if _, ok := putObjectArgs("T0JK", nil, string(signed), sig)["naming"]; ok {
		t.Fatal("a nil vocabulary was transmitted as a present field")
	}
}
