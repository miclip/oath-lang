package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardNewNames refuses to bind a NEW name in the CANONICAL store without --new.
//
// Two claims, kept apart because either can pass while the other is broken:
//
//	WHICH ACT   a new binding is refused; re-pointing an existing name is not
//	WHERE       only the repository's canonical corpus is guarded
//
// The claim is about the ACT, not the spelling: a name-shape check would catch
// `polyspine50` and wave through `spine` — identical pollution of the committed
// store, identical permanent journal entries, no warning. So the cases below
// deliberately include an innocuous name.

// canonicalStore opens the repository's real corpus with the process CWD at the
// repo root, which is what makes st.Root resolve to the canonical path. Tests
// run from oath/, so without the chdir "codebase" would resolve to
// oath/codebase and the guard would never fire — a setup error that would make
// every assertion below vacuous, which is why it is asserted rather than
// assumed.
func canonicalStore(t *testing.T) *Store {
	t.Helper()
	t.Chdir("..")
	st, err := OpenStore(defaultStoreDir)
	if err != nil {
		t.Fatalf("could not open the canonical store: %v", err)
	}
	if !isCanonicalStore(st.Root) {
		t.Fatalf("setup failed: %q is not recognised as canonical, so these tests would prove nothing", st.Root)
	}
	return st
}

func TestGuardNewNamesRefusesFreshBindings(t *testing.T) {
	st := canonicalStore(t)
	for _, name := range []string{"scratchprobe", "spine", "tmp1"} {
		if _, exists := st.Resolve(name); exists {
			t.Fatalf("setup: %q already exists, so this case proves nothing", name)
		}
		src := "(defn " + name + " [] [(x Int)] Int (+ x 1))"
		err := guardNewNames(st, src, false)
		if err == nil {
			t.Errorf("%q is a new name and must be refused without --new", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal must NAME what it refused; got %v", err)
		}
	}
}

func TestGuardNewNamesAllowsRepoints(t *testing.T) {
	st := canonicalStore(t)
	// This is what `make verify` does on every run: re-put the whole corpus. If
	// it required --new, everyone would pass the flag by default and the guard
	// would mean nothing.
	if _, exists := st.Resolve("length"); !exists {
		t.Skip("corpus does not contain `length`")
	}
	if err := guardNewNames(st, "(defn length [] [(xs (List Int))] Int 0)", false); err != nil {
		t.Errorf("re-putting an existing name must not require --new, got %v", err)
	}
}

func TestGuardNewNamesRespectsTheFlag(t *testing.T) {
	st := canonicalStore(t)
	if err := guardNewNames(st, "(defn scratchprobe [] [(x Int)] Int (+ x 1))", true); err != nil {
		t.Errorf("--new must permit a fresh binding, got %v", err)
	}
}

// TestGuardNewNamesDefersParseErrors: malformed source must reach the parser's
// own diagnostic with its line number, not a confusing name-binding refusal.
func TestGuardNewNamesDefersParseErrors(t *testing.T) {
	st := canonicalStore(t)
	if err := guardNewNames(st, "(defn broken [] [(x Int)", false); err != nil {
		t.Errorf("malformed source must fall through to the real parse error, got %v", err)
	}
}

// TestGuardNewNamesCountsEveryForm — every fresh name must be reported, not just
// the first, or a reader fixes one and is surprised by the next.
func TestGuardNewNamesCountsEveryForm(t *testing.T) {
	st := canonicalStore(t)
	src := "(defn freshaaa [] [(x Int)] Int x)\n(defn freshbbb [] [(x Int)] Int x)"
	err := guardNewNames(st, src, false)
	if err == nil {
		t.Fatal("two fresh names must be refused")
	}
	for _, want := range []string{"freshaaa", "freshbbb", "2 new name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must mention %q; got %v", want, err)
		}
	}
}

// TestGuardNewNamesSkipsNonCanonicalStores is the CI regression this guard
// caused on the day it landed. Publishing into a FRESH store makes every name
// new by definition — check-stdlib-manifest.py republishes 52 artifacts into a
// temp dir, and oathrs/conformance.sh rebuilds the corpus from empty. In an
// empty store a new name is a RECONSTRUCTION, not a publication decision.
func TestGuardNewNamesSkipsNonCanonicalStores(t *testing.T) {
	src := "(defn scratchprobe [] [(x Int)] Int (+ x 1))"

	// CONTROL first: the canonical store must still refuse, or the case below
	// proves only that guardNewNames does nothing at all.
	st := canonicalStore(t)
	if err := guardNewNames(st, src, false); err == nil {
		t.Fatal("control failed: the canonical store must refuse a fresh binding")
	}

	tmp, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("could not open a scratch store: %v", err)
	}
	if err := guardNewNames(tmp, src, false); err != nil {
		t.Errorf("a non-canonical store must not be guarded, got %v", err)
	}
}

// TestIsCanonicalStoreRecognisesAliases is the caveat this design was narrowed
// to close. An earlier version exempted any run with OATH_STORE set, treating
// the variable as evidence of intent — sound while typed per-command, false the
// moment it is exported. `export OATH_STORE=codebase` in a shell profile would
// then have disabled the guard permanently and invisibly, for the exact store it
// exists to protect. Identity is compared by RESOLVED PATH, so every spelling of
// the canonical store is still the canonical store.
func TestIsCanonicalStoreRecognisesAliases(t *testing.T) {
	t.Chdir("..")
	abs, err := filepath.Abs(defaultStoreDir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for _, spelling := range []string{
		defaultStoreDir,                  // "codebase"
		"./" + defaultStoreDir,           // "./codebase"
		abs,                              // absolute
		abs + string(filepath.Separator), // trailing separator
		filepath.Join(abs, "..", defaultStoreDir), // a detour through the parent
	} {
		if !isCanonicalStore(spelling) {
			t.Errorf("%q names the canonical store and must be recognised", spelling)
		}
	}
	for _, other := range []string{"", t.TempDir(), filepath.Join(abs, "objects")} {
		if isCanonicalStore(other) {
			t.Errorf("%q is NOT the canonical store and must not be treated as one", other)
		}
	}
	// A symlink to the canonical store is the same store.
	link := filepath.Join(t.TempDir(), "aliased")
	if err := os.Symlink(abs, link); err == nil {
		if !isCanonicalStore(link) {
			t.Error("a symlink to the canonical store must be recognised as canonical")
		}
	}
}
