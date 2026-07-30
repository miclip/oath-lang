package main

// Client configuration (#86): `~/.oath/config`, `oath config`, `oath new`.
//
// WHAT THIS IS, AND MORE IMPORTANTLY WHAT IT IS NOT. This is LOCAL initialization —
// defaults so a client need not repeat `--remote` and `--key` on every command. It
// does NOT create authority. `oath new michael` does not reserve `michael/*` on any
// registry, and this file says so at every point a reader might assume otherwise,
// because a command that looks like it claimed a namespace and did not is exactly the
// kind of quiet overclaim the rest of this system refuses.
//
// Namespace authority is claimed by publishing under a name (trust on first publish,
// derived from the journal) or by an explicit signed operation — never by writing a
// local file. A config file is a convenience for the caller; the registry never sees
// it and would have no reason to believe it.
//
// PROVENANCE IS REPORTED, NOT JUST VALUES. `oath config` shows where each setting
// came from — flag, environment, config file, or built-in default. That matters for
// the same reason ownership sources do: a value read from a file the operator forgot
// they wrote is indistinguishable from one they intended, until something asks which
// it was. Debugging "why did it publish there?" needs the source, not the value.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// clientConfig is the on-disk shape of ~/.oath/config. Every field is optional; an
// absent field means "no preference", not "empty".
type clientConfig struct {
	// Registry is the default endpoint for publish/head operations.
	Registry string `json:"registry,omitempty"`
	// Key is the path to the signing key used to publish. Never the key itself: a
	// config file is read by tooling, copied between machines, and pasted into bug
	// reports, none of which a private key should survive.
	Key string `json:"key,omitempty"`
	// Namespace is the default prefix for names this client publishes. A LOCAL
	// default only — it confers no ownership of that prefix anywhere.
	Namespace string `json:"namespace,omitempty"`
	// Author is an optional label for unsigned local puts. Ignored by a registry when
	// the request is signed, since there the principal IS the key.
	Author string `json:"author,omitempty"`
}

// setting is one resolved value plus where it came from.
type setting struct {
	Value  string
	Source string // "flag" | "env:NAME" | "config" | "default" | "unset"
}

func oathHome() string {
	if v := os.Getenv("OATH_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".oath"
	}
	return filepath.Join(home, ".oath")
}

func configPath() string { return filepath.Join(oathHome(), "config") }

// loadClientConfig reads ~/.oath/config. A missing file is not an error: an
// unconfigured client is a valid state, and inventing defaults for a file that does
// not exist would make `oath config` report settings nobody chose.
func loadClientConfig() (*clientConfig, bool, error) {
	b, err := os.ReadFile(configPath())
	if os.IsNotExist(err) {
		return &clientConfig{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var c clientConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, true, fmt.Errorf("corrupt %s: %w", configPath(), err)
	}
	return &c, true, nil
}

// resolve picks a value by precedence — flag, then environment, then config file,
// then default — and records which one won. Precedence is fixed and documented so a
// caller can predict it rather than discover it.
func resolve(flagVal, envName, cfgVal, deflt string) setting {
	if flagVal != "" {
		return setting{flagVal, "flag"}
	}
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			return setting{v, "env:" + envName}
		}
	}
	if cfgVal != "" {
		return setting{cfgVal, "config"}
	}
	if deflt != "" {
		return setting{deflt, "default"}
	}
	return setting{"", "unset"}
}

// cmdConfig prints the resolved client configuration with the provenance of each
// value, then states plainly what the configuration does not establish.
func cmdConfig(setKV []string) {
	cfg, existed, err := loadClientConfig()
	if err != nil {
		fail(err)
	}

	if len(setKV) > 0 {
		for _, kv := range setKV {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				fail(fmt.Errorf("expected key=value, got %q", kv))
			}
			switch k {
			case "registry":
				cfg.Registry = v
			case "key":
				cfg.Key = v
			case "namespace":
				cfg.Namespace = v
			case "author":
				cfg.Author = v
			default:
				fail(fmt.Errorf("unknown setting %q (registry, key, namespace, author)", k))
			}
		}
		if err := writeClientConfig(cfg); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s\n\n", configPath())
	}

	reg := resolve("", "OATH_REGISTRY", cfg.Registry, "")
	key := resolve("", "OATH_KEY", cfg.Key, "")
	author := resolve("", "OATH_AUTHOR", cfg.Author, "unattributed")
	store := resolve("", "OATH_STORE", "", "./codebase")

	fmt.Printf("CLIENT CONFIGURATION  (%s)\n", configPath())
	if !existed {
		fmt.Printf("  no config file — every value below is an environment variable or a default\n")
	}
	fmt.Println()
	for _, row := range []struct {
		name string
		s    setting
	}{
		{"registry", reg},
		{"key", key},
		{"namespace", setting{Value: cfg.Namespace, Source: sourceOf(cfg.Namespace)}},
		{"author", author},
		{"store", store},
	} {
		v := row.s.Value
		if v == "" {
			v = "(unset)"
		}
		fmt.Printf("  %-10s %-44s from %s\n", row.name, v, row.s.Source)
	}

	fmt.Printf("\nWHAT THIS CONFIGURATION DOES NOT ESTABLISH:\n")
	fmt.Printf("  A namespace here is a local DEFAULT for names this client publishes. It\n")
	fmt.Printf("  confers no ownership of that prefix on any registry. Authority over a name\n")
	fmt.Printf("  comes from publishing under it (recorded in the journal) or from an explicit\n")
	fmt.Printf("  signed operation — never from a file on this machine, which no registry reads.\n")
	if key.Value == "" {
		fmt.Printf("\n  No signing key is configured, so publications would be unsigned and their\n")
		fmt.Printf("  authorship unverifiable. `oath new <namespace>` creates one.\n")
	}
}

func sourceOf(v string) string {
	if v == "" {
		return "unset"
	}
	return "config"
}

func writeClientConfig(c *clientConfig) error {
	if err := os.MkdirAll(oathHome(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the file names a key path and a default identity. Not secret, but not
	// world-readable either.
	return os.WriteFile(configPath(), append(b, '\n'), 0o600)
}

// cmdNew initializes a local client identity: a signing key under ~/.oath/keys and a
// config naming it, the namespace, and the registry.
//
// It is deliberately OFFLINE. It contacts nothing, reserves nothing, and returns no
// authority — so it cannot fail halfway and leave a caller believing a namespace is
// theirs. Claiming a prefix is a separate, signed, online act; conflating the two
// would make "I ran oath new" sound like evidence.
func cmdNew(namespace, registry string) {
	if namespace == "" {
		fail(fmt.Errorf("usage: oath new <namespace> [--remote <url>]"))
	}
	if strings.ContainsAny(namespace, "/ \t") {
		fail(fmt.Errorf("namespace %q must be a single segment: nesting is expressed by the names you publish (michael/service1/foo), not by the namespace default", namespace))
	}

	cfg, _, err := loadClientConfig()
	if err != nil {
		fail(err)
	}

	keyDir := filepath.Join(oathHome(), "keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		fail(err)
	}
	keyPath := filepath.Join(keyDir, namespace)
	if _, err := os.Stat(keyPath + ".key"); err == nil {
		fmt.Printf("key %s.key already exists; keeping it\n", keyPath)
	} else {
		cmdKeygen(keyPath)
	}

	cfg.Namespace = namespace
	cfg.Key = keyPath + ".key"
	if registry != "" {
		cfg.Registry = registry
	}
	if err := writeClientConfig(cfg); err != nil {
		fail(err)
	}

	fmt.Printf("\ninitialized %s\n", configPath())
	fmt.Printf("  namespace  %s\n", cfg.Namespace)
	fmt.Printf("  key        %s\n", cfg.Key)
	if cfg.Registry != "" {
		fmt.Printf("  registry   %s\n", cfg.Registry)
	}
	fmt.Printf("\nTHIS RESERVED NOTHING. No registry was contacted, and %q is not yours\n", namespace)
	fmt.Printf("anywhere. It is a local default for the names this client publishes.\n")
	fmt.Printf("Authority over a name is established by PUBLISHING under it — the first\n")
	fmt.Printf("signed publication is what a registry records and what an auditor can check.\n")
	if cfg.Registry != "" {
		fmt.Printf("\nPublish with:  oath publish --remote %s --key %s <file.oath>\n", cfg.Registry, cfg.Key)
	}
}
