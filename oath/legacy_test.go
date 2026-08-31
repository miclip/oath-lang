package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// putArgsFor builds the tool arguments for one put, so tests state the SOURCE and
// nothing else.
func putArgsFor(t *testing.T, src string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"source": src})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// THE FREEZE. Legacy ambiguity is preserved; new ambiguity may not be created.
//
// The rule is narrow on purpose: it refuses CREATION of a name by a request with
// no signed publication, on a hosted registry. It does not refuse publication, and
// it does not touch local development.
func TestFreezeRefusesTokenNameCreation(t *testing.T) {
	st := newMemStoreForTest(t)
	src := "(defn brand-new [] [(x Int)] Int x)"

	// HOSTED + unsigned + a name that does not exist → refused, with a repair path.
	_, err := mcpCallTool(st, "put", putArgsFor(t, src), "agent", true, false, true)
	if err == nil {
		t.Fatal("a token-only request CREATED a new name — the freeze is not in force")
	}
	for _, want := range []string{"signed publication", "SERVICE ACCESS", "NAME OWNERSHIP", "oath keygen", "oath publish", "brand-new"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal is missing %q — an agent needs the boundary AND the way forward:\n%v", want, err)
		}
	}
	if nameExists(st, "brand-new") {
		t.Error("the refused creation bound the name anyway")
	}

	// LOCAL (hosted=false) → unaffected. Development must not require a key.
	if _, err := mcpCallTool(st, "put", putArgsFor(t, src), "", true, false, false); err != nil {
		t.Fatalf("the freeze broke local, unhosted use: %v", err)
	}
	if !nameExists(st, "brand-new") {
		t.Fatal("local put did not bind the name")
	}

	// That name is now UNSIGNED-first-bound within the boundary, so it is legacy:
	// an unsigned hosted update is still permitted. Preserve, do not rewrite.
	if !isLegacyUnowned(st, "brand-new") {
		t.Fatal("an unsigned first binding inside the boundary is not classified legacy")
	}
	if _, err := mcpCallTool(st, "put", putArgsFor(t, "(defn brand-new [] [(x Int)] Int (+ x 0))"),
		"agent", true, false, true); err != nil {
		t.Errorf("an unsigned update to a LEGACY name was refused; the freeze must preserve them: %v", err)
	}
}

// A name first bound after the boundary is NOT legacy, whatever it looks like.
// This is the property that makes the set closed by construction rather than by
// discipline — without it, "frozen" is a description that grows.
func TestFrozenSetCannotExpandPastTheBoundary(t *testing.T) {
	st := newMemStoreForTest(t)
	if _, err := mcpCallTool(st, "put", putArgsFor(t, "(defn late [] [(x Int)] Int x)"), "", true, false, false); err != nil {
		t.Fatal(err)
	}
	// Inside a boundary that covers it: legacy.
	if !legacyUnownedAt(st, 9999)["late"] {
		t.Error("a name bound before the boundary should be in the frozen set")
	}
	// With the boundary set BEFORE it: excluded, though nothing about the name changed.
	if legacyUnownedAt(st, 0)["late"] {
		t.Error("a name bound AFTER the boundary entered the frozen set — the freeze can expand, " +
			"which makes it a description rather than a set")
	}
}
