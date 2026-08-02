package main

import "testing"

// A CLI that accepts a flag it does not implement asserts something false about
// what it did. This is pinned because the failure is silent and permanent: during
// the v0.10.0 exercise `publish --name sandbox/thing` bound a bare top-level name
// on the live registry, and names cannot be unbound.
func TestKnownFlagsCoverWhatCommandsParse(t *testing.T) {
	// Flags that MUST be known, or a working invocation starts being refused.
	for cmd, flags := range map[string][]string{
		"publish":   {"--key", "--kms-key", "--license", "--remote", "--dry-run", "-y"},
		"reserve":   {"--key", "--dry-run", "-y"},
		"transfer":  {"--to", "--recipient-key", "--key", "--dry-run", "-y"},
		"authority": {"--remote", "--key", "--kms-key"},
		"ls":        {"--remote", "--local", "--key"},
	} {
		for _, f := range flags {
			if !knownFlags[cmd][f] {
				t.Errorf("%s parses %s but it is absent from knownFlags, so a working command is now refused", cmd, f)
			}
		}
	}
	// Flags that must NOT be known — each one was, or could be, mistaken for real.
	for cmd, flags := range map[string][]string{
		"publish": {"--name", "--namespace", "--author"},
		"reserve": {"--to"},
	} {
		for _, f := range flags {
			if knownFlags[cmd][f] {
				t.Errorf("%s lists %s as known but does not parse it: it would be accepted and ignored", cmd, f)
			}
		}
	}
	if knownFlagList("publish") == "(none)" {
		t.Error("publish reports no known flags; the refusal message would name no alternative")
	}
}
