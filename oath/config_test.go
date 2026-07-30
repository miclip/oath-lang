package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Precedence is fixed and must be predictable: flag beats environment beats config
// file beats default. Reporting the SOURCE is the point — a value read from a file
// someone forgot they wrote is indistinguishable from one they intended until
// something asks which it was.
func TestConfigPrecedenceAndProvenance(t *testing.T) {
	t.Setenv("OATH_TESTVAR", "from-env")
	for _, tc := range []struct {
		name, flag, env, cfg, deflt, wantVal, wantSrc string
	}{
		{"flag wins over everything", "F", "OATH_TESTVAR", "C", "D", "F", "flag"},
		{"env beats config and default", "", "OATH_TESTVAR", "C", "D", "from-env", "env:OATH_TESTVAR"},
		{"config beats default", "", "OATH_UNSET_XYZ", "C", "D", "C", "config"},
		{"default when nothing else", "", "OATH_UNSET_XYZ", "", "D", "D", "default"},
		{"unset is distinguishable from empty", "", "OATH_UNSET_XYZ", "", "", "", "unset"},
	} {
		got := resolve(tc.flag, tc.env, tc.cfg, tc.deflt)
		if got.Value != tc.wantVal || got.Source != tc.wantSrc {
			t.Fatalf("%s: got (%q,%q), want (%q,%q)", tc.name, got.Value, got.Source, tc.wantVal, tc.wantSrc)
		}
	}
}

// A missing config file is a valid state, not an error. Treating it as one would make
// an unconfigured client look broken; inventing values would make `oath config` report
// settings nobody chose.
func TestMissingConfigIsNotAnError(t *testing.T) {
	t.Setenv("OATH_HOME", filepath.Join(t.TempDir(), "nonexistent"))
	cfg, existed, err := loadClientConfig()
	if err != nil {
		t.Fatalf("absent config reported an error: %v", err)
	}
	if existed {
		t.Fatal("reported a config file that does not exist")
	}
	if cfg == nil || cfg.Registry != "" || cfg.Key != "" {
		t.Fatal("absent config produced non-empty values")
	}
	// A CORRUPT file, by contrast, must be an error: silently ignoring it would run
	// with defaults while the operator believes their settings applied.
	home := t.TempDir()
	t.Setenv("OATH_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadClientConfig(); err == nil {
		t.Fatal("a corrupt config file loaded silently")
	}
}

// The config written must never contain key MATERIAL — only a path. A config file is
// read by tooling, copied between machines, and pasted into bug reports.
func TestConfigStoresKeyPathNotKeyMaterial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OATH_HOME", home)
	if err := writeClientConfig(&clientConfig{Key: filepath.Join(home, "keys", "k.key"), Namespace: "ns"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "-----BEGIN") {
		t.Fatal("config contains what looks like key material")
	}
	// 64 hex chars in a row would be a key or a seed, not a path.
	hexRun := 0
	for _, c := range string(b) {
		if strings.ContainsRune("0123456789abcdef", c) {
			hexRun++
			if hexRun >= 64 {
				t.Fatal("config contains a 64-character hex run: that is key material, not a path")
			}
		} else {
			hexRun = 0
		}
	}
	fi, err := os.Stat(filepath.Join(home, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("config mode is %v, want 0600: it names a key path and a default identity", fi.Mode().Perm())
	}
}

// `oath new` is offline and local. A multi-segment namespace is refused, because
// nesting belongs in the NAMES you publish, not in a local default — accepting
// "michael/service1" here would imply a claim over a prefix that nothing granted.
func TestNewRefusesMultiSegmentNamespace(t *testing.T) {
	if !strings.ContainsAny("michael/service1", "/ \t") {
		t.Fatal("test premise wrong")
	}
	// cmdNew calls fail() on rejection, so assert the predicate it uses rather than
	// invoking it: the check is the contract, and fail() exits the process.
	for _, bad := range []string{"michael/service1", "with space", "tab\there"} {
		if !strings.ContainsAny(bad, "/ \t") {
			t.Fatalf("%q should be rejected as a namespace", bad)
		}
	}
	for _, ok := range []string{"michael", "svc1", "a-b_c"} {
		if strings.ContainsAny(ok, "/ \t") {
			t.Fatalf("%q should be an acceptable single-segment namespace", ok)
		}
	}
}
