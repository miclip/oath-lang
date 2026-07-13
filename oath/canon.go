package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// hashDef computes a definition's identity: SHA-256 of its canonical encoding.
// v0 uses Go's deterministic struct-order JSON as the canonical form; a real
// implementation would specify a canonical binary encoding independent of any
// host language. De Bruijn indices in the AST make this alpha-canonical:
// renaming a variable cannot change a hash, because names aren't in here.
func hashDef(d *Def) string {
	b, err := json.Marshal(d)
	if err != nil {
		panic(err) // Def contains no unmarshalable types; cannot happen
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
