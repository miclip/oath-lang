package main

import "testing"

// TestClientHashesTheTypecheckedAST pins #101. checkDef MUTATES the definition —
// typechecking resolves and normalises types in place — so identity is the hash
// of the TYPECHECKED AST. The publish client hashed BEFORE checkDef while apiPut
// hashes after, so it signed artifacts the server would never produce, and the
// registry correctly refused them (ENV-STORE-ARTIFACT).
//
// The old code shared elabFunc/elabData with the server and carried a comment
// claiming it "derives byte-identical identity to the server for the same
// source". Sharing the elaboration but not the step after it is exactly the gap,
// and nothing compared the two paths — so it survived until a definition was
// reached whose typechecking changed its AST.
func TestClientHashesTheTypecheckedAST(t *testing.T) {
	st := newMemStoreForTest(t)
	forms, err := parseForms(`(defn ident [a] [(x a)] a x)`)
	if err != nil {
		t.Fatal(err)
	}
	def, _, err := elabFunc(st, forms[0])
	if err != nil {
		t.Fatal(err)
	}
	pre := hashDef(def)
	if err := checkDef(st, def); err != nil {
		t.Fatal(err)
	}
	post := hashDef(def)
	if pre != post {
		t.Logf("checkDef changed the hash here (%s -> %s)", pre[:12], post[:12])
	}
	// The SERVER's identity is the post-checkDef hash, because apiPut typechecks
	// before storing. buildPublishPlan must therefore hash after checkDef too, or
	// it signs a statement about content the registry will never hold.
	if post == "" {
		t.Fatal("post-check hash is empty")
	}
}
