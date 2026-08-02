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

// The guard must apply ONLY to catalogued commands. An absent entry means "not
// yet listed", never "takes no flags".
//
// This is pinned because conflating the two shipped: `oath serve --http … --tokens
// …` is what the container entrypoint runs, `serve` had no entry, every flag read
// as unknown, and the deployed container exited before binding a port. Cloud Run
// refused to shift traffic and the registry stayed up, which is the only reason it
// was cheap. The earlier test checked the TABLE and not that commands still RUN.
func TestUncataloguedCommandsAreNotFlagChecked(t *testing.T) {
	// Commands invoked by the container entrypoint, CI, and the Makefile. If any
	// gains an entry, every flag it accepts must be listed in the same edit.
	for _, cmd := range []string{"serve", "prove-worker", "prove", "audit", "verify",
		"find", "export", "import", "fixtures", "migrate-store", "store-check"} {
		if flags, catalogued := knownFlags[cmd]; catalogued {
			t.Errorf("%q is now flag-checked with %d known flags. That is fine ONLY if every "+
				"flag it accepts is listed — `serve --http --tokens --authorized-keys` broke a "+
				"production deploy this way. Verify the list is complete before removing this.",
				cmd, len(flags))
		}
	}
	// And a catalogued command still refuses the flag that caused a permanent name.
	if knownFlags["publish"]["--name"] {
		t.Error("publish would accept --name again")
	}
	if _, ok := knownFlags["publish"]; !ok {
		t.Error("publish is no longer catalogued, so the guard that stops --name is inert")
	}
}
