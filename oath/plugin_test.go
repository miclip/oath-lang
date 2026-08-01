package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Installing a plugin must never take over a developer's tool configuration.
// Silently replacing a configured server would be a poor advertisement for a
// system whose whole pitch is that what someone stated is preserved.
func TestPluginInstallMergesRatherThanClobbers(t *testing.T) {
	existing := []byte(`{"mcpServers":{"my-existing":{"command":"foo"}},"other":"keep me"}`)
	merged, added, err := mergeMCP(existing, "https://registry.example")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(merged, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["other"] != "keep me" {
		t.Error("an unrelated top-level key was dropped")
	}
	servers := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["my-existing"]; !ok {
		t.Error("an existing MCP server was removed by the install")
	}
	for _, want := range []string{"oath", "oath-registry"} {
		if _, ok := servers[want]; !ok {
			t.Errorf("%s was not added", want)
		}
	}
	if len(added) != 2 {
		t.Errorf("added=%v, want both servers", added)
	}

	// A name the project already uses is THEIRS. Ours does not overwrite it.
	taken := []byte(`{"mcpServers":{"oath":{"command":"their-own-thing"}}}`)
	merged, added, err = mergeMCP(taken, "https://registry.example")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(merged, &cfg)
	servers = cfg["mcpServers"].(map[string]any)
	if servers["oath"].(map[string]any)["command"] != "their-own-thing" {
		t.Error("the install overwrote a server the project had already configured under our name")
	}
	if len(added) != 1 || added[0] != "oath-registry" {
		t.Errorf("added=%v, want only oath-registry", added)
	}

	// Malformed config: refuse rather than replace. Losing someone's servers to a
	// stray comma is not an acceptable install.
	if _, _, err := mergeMCP([]byte("{not json"), "https://registry.example"); err == nil {
		t.Error("a malformed MCP config was overwritten instead of refused")
	}
}

// The plugin content must not claim the subagent split is registry-checked. It is
// a workflow discipline: the registry sees one authenticated principal per put and
// cannot distinguish a specifier from an implementer within a session.
func TestPluginDoesNotOverclaimAuthorshipSeparation(t *testing.T) {
	for name, body := range map[string]string{
		"oath-implement": agentImplement,
		"codex AGENTS":   codexAgents,
	} {
		// Whitespace-normalised: the sources are wrapped markdown, so a required
		// phrase can straddle a line break and a naive Contains would report a
		// missing disclaimer that is plainly there.
		low := strings.Join(strings.Fields(strings.ToLower(body)), " ")
		if !strings.Contains(low, "not something the registry verifies") &&
			!strings.Contains(low, "what the registry can and cannot see") {
			t.Errorf("%s does not state that the separation is unverified by the registry; "+
				"an agent reading it would believe a guarantee that does not exist", name)
		}
	}
}
