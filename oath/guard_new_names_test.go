package main

import (
	"strings"
	"testing"
)

// guardNewNames refuses to bind a NEW name without --new (#149 session).
//
// The claim is about the ACT, not the spelling: a name-shape check would catch
// `polyspine50` and wave through `spine` — identical pollution of the committed
// store, identical permanent journal entries, no warning. So these tests assert
// on new-vs-existing, and deliberately include an innocuous-looking name.

func TestGuardNewNamesRefusesFreshBindings(t *testing.T) {
	t.Setenv("OATH_STORE", "") // the guard applies to the DEFAULT store only
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}

	// A name that does not exist is refused, however ordinary it looks.
	for _, name := range []string{"scratchprobe", "spine", "tmp1"} {
		src := "(defn " + name + " [] [(x Int)] Int (+ x 1))"
		if _, exists := st.Resolve(name); exists {
			t.Fatalf("setup: %q already exists in the corpus, so this case proves nothing", name)
		}
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
	t.Setenv("OATH_STORE", "")
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	// An EXISTING name must pass with no flag. This is what `make verify` does
	// on every run: it re-puts the whole corpus and must not need --new, or the
	// guard would push everyone into passing it by default and mean nothing.
	if _, exists := st.Resolve("length"); !exists {
		t.Skip("corpus does not contain `length`; pick another existing name")
	}
	src := "(defn length [] [(xs (List Int))] Int 0)"
	if err := guardNewNames(st, src, false); err != nil {
		t.Errorf("re-putting an existing name must not require --new, got %v", err)
	}
}

func TestGuardNewNamesRespectsTheFlag(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	if err := guardNewNames(st, "(defn scratchprobe [] [(x Int)] Int (+ x 1))", true); err != nil {
		t.Errorf("--new must permit a fresh binding, got %v", err)
	}
}

// TestGuardNewNamesDefersParseErrors: a broken source must produce the parser's
// own diagnostic with its line number, not a confusing name-binding refusal.
func TestGuardNewNamesDefersParseErrors(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	if err := guardNewNames(st, "(defn broken [] [(x Int)", false); err != nil {
		t.Errorf("malformed source must fall through to the real parse error, got %v", err)
	}
}

// TestGuardNewNamesCountsEveryForm — a multi-definition file must have ALL its
// fresh names reported, not just the first. A guard that stops at the first
// would let a reader fix one name and be surprised by the next.
func TestGuardNewNamesCountsEveryForm(t *testing.T) {
	t.Setenv("OATH_STORE", "")
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	src := "(defn freshaaa [] [(x Int)] Int x)\n(defn freshbbb [] [(x Int)] Int x)"
	err = guardNewNames(st, src, false)
	if err == nil {
		t.Fatal("two fresh names must be refused")
	}
	for _, want := range []string{"freshaaa", "freshbbb", "2 new name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must mention %q; got %v", want, err)
		}
	}
}

// TestGuardNewNamesSkipsExplicitStores is the CI regression this guard caused on
// the day it landed. Publishing into a FRESH store makes every name new by
// definition — scripts/check-stdlib-manifest.py republishes 52 artifacts into a
// temp dir, and oathrs/conformance.sh rebuilds the corpus from empty. In an
// empty store a new name is a RECONSTRUCTION, not a publication decision.
//
// Setting OATH_STORE is someone stating where they intend to write, which is
// the opposite of the casual write this guards.
func TestGuardNewNamesSkipsExplicitStores(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	src := "(defn scratchprobe [] [(x Int)] Int (+ x 1))"

	t.Setenv("OATH_STORE", "") // control: default store -> refused
	if err := guardNewNames(st, src, false); err == nil {
		t.Fatal("control failed: the default store must still refuse a fresh binding, " +
			"otherwise the case below proves nothing")
	}

	t.Setenv("OATH_STORE", t.TempDir())
	if err := guardNewNames(st, src, false); err != nil {
		t.Errorf("an explicitly chosen store must not be guarded, got %v", err)
	}
}
