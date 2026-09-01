package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// `oath resolve` accepts (type ...) aliases (#188 follow-on). The interesting case is
// not that the form parses — it is WHERE the alias's own dependencies come from.
//
// An alias body is elaborated ONCE, at registration. Every later use expands the
// already-canonical body by cloning or substituting, resolving nothing. So a store-level
// resolution log armed around a USING form observes no resolution at all, and a type the
// source reaches only through an alias would be missing from the lock — a lock that looks
// complete, hydrates cleanly, and then fails to elaborate its own source. These tests pin
// the capture at registration instead, in the elaborator's request-local log.

// aliasDepSources: `Payload` is mentioned ONLY inside the alias body. The function
// projects `size`, an Int, so nothing outside the alias reaches Payload — not a type
// application, not a constructor, not a match arm. Inner is reachable only through
// Payload's constructor field, so it exercises the transitive closure too.
const (
	aliasDepStoreSrc = `(data Inner [] (In Int))
	                    (data Payload [] (Pay Inner))`

	aliasOnlySrc = `(type Env {decode (-> Payload Int) size Int})
	                (defn cost [] [(e Env)] Int (. e size))`

	aliasInlineSrc = `(defn cost [] [(e {decode (-> Payload Int) size Int})] Int (. e size))`

	// The CONTROL: the same shape with Payload absent everywhere. If this also
	// reported Payload as a dependency the test above would be measuring something
	// ambient rather than the alias body.
	aliasNoDepSrc = `(type Env {size Int})
	                 (defn cost [] [(e Env)] Int (. e size))`
)

func hasHash(closure []string, h string) bool {
	for _, c := range closure {
		if c == h {
			return true
		}
	}
	return false
}

// A type reached ONLY from inside an alias body is a direct dependency of the source,
// and its transitive closure is fetched with it.
func TestResolveCapturesAliasOnlyDependency(t *testing.T) {
	from := newStore(t)
	put(t, from, aliasDepStoreSrc)
	payloadHash, _ := from.Resolve("Payload")
	innerHash, _ := from.Resolve("Inner")

	direct, closure, err := externalClosure(from, aliasOnlySrc)
	if err != nil {
		t.Fatalf("a (type ...) form must elaborate in resolve: %v", err)
	}
	if direct["Payload"] != payloadHash {
		t.Errorf("Payload is referenced only by the ALIAS body and must still be pinned to %s; got %v",
			payloadHash, direct)
	}
	if len(direct) != 1 {
		t.Errorf("Payload is the only external name (Int is builtin, Env is the alias itself); got %v", direct)
	}
	if !hasHash(closure, payloadHash) {
		t.Errorf("the closure must hold Payload %s: %v", payloadHash, closure)
	}
	if !hasHash(closure, innerHash) {
		t.Errorf("the closure must hold Inner %s, reached transitively through Payload's field: %v",
			innerHash, closure)
	}

	// Control: with Payload gone from the alias body, nothing pins it. This is what
	// makes the assertion above evidence about the alias rather than about the store.
	ctlDirect, _, err := externalClosure(from, aliasNoDepSrc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ctlDirect["Payload"]; ok {
		t.Errorf("control: a source whose alias never mentions Payload must not pin it; got %v", ctlDirect)
	}
}

// The alias's own name is NOT a dependency. registerTypeAlias resolves it against the
// store to reject an alias over a data type — capturing that resolution (which arming
// Store.resolveLog around the form would do) would invent a dependency on an unrelated
// definition that merely shares the alias's spelling.
func TestResolveDoesNotPinTheAliasName(t *testing.T) {
	from := newStore(t)
	put(t, from, `(defn Env [] [(x Int)] Int (+ x 1))`) // a FUNCTION named Env, unrelated
	direct, _, err := externalClosure(from, aliasNoDepSrc)
	if err != nil {
		t.Fatalf("an alias may share a spelling with a stored function: %v", err)
	}
	if _, ok := direct["Env"]; ok {
		t.Errorf("the alias's own name is not a dependency of the source; got %v", direct)
	}
}

// Identity transparency THROUGH THE RESOLVE PATH: the alias spelling and the inline
// spelling produce the same object hash and the same lock — same direct set, same
// closure. The put path is pinned separately (TestTypeAliasPreservesIdentity); this is
// the claim resolve depends on, since a lock is only reusable if it describes the same
// objects the alias-free source would have produced.
func TestResolveAliasIsIdentityTransparent(t *testing.T) {
	from := newStore(t)
	put(t, from, aliasDepStoreSrc)

	elaborate := func(src string) (string, map[string]string, []string) {
		t.Helper()
		tmp, err := seedStore(from)
		if err != nil {
			t.Fatal(err)
		}
		direct, closure, err := computeExternal(tmp, src)
		if err != nil {
			t.Fatalf("elaborating %q: %v", src, err)
		}
		h, ok := tmp.Resolve("cost")
		if !ok {
			t.Fatalf("elaboration stored no object for cost")
		}
		return h, direct, closure
	}

	aliasH, aliasDirect, aliasClosure := elaborate(aliasOnlySrc)
	inlineH, inlineDirect, inlineClosure := elaborate(aliasInlineSrc)

	if aliasH != inlineH {
		t.Errorf("the alias must be identity-transparent in resolve too:\n alias  %s\n inline %s", aliasH, inlineH)
	}
	if !reflect.DeepEqual(aliasDirect, inlineDirect) {
		t.Errorf("the two spellings must pin the same direct dependencies:\n alias  %v\n inline %v",
			aliasDirect, inlineDirect)
	}
	if !reflect.DeepEqual(aliasClosure, inlineClosure) {
		t.Errorf("the two spellings must pin the same closure:\n alias  %v\n inline %v",
			aliasClosure, inlineClosure)
	}
}

// A chained alias (an alias whose body is another alias) carries the earlier alias's
// dependency: the second body resolves nothing itself — it expands the first — so the
// dependency has to survive from the first registration.
func TestResolveCapturesChainedAliasDependency(t *testing.T) {
	from := newStore(t)
	put(t, from, aliasDepStoreSrc)
	payloadHash, _ := from.Resolve("Payload")

	direct, _, err := externalClosure(from, `(type Dec (-> Payload Int))
	                                         (type Env {decode Dec size Int})
	                                         (defn cost [] [(e Env)] Int (. e size))`)
	if err != nil {
		t.Fatal(err)
	}
	if direct["Payload"] != payloadHash {
		t.Errorf("a dependency of the FIRST alias must survive into the second's expansion; got %v", direct)
	}
}

// The dependency log is per-elaboration, so `resolve` never carries the deps of a
// different source. A second elaboration against the same store reports only its own.
func TestResolveAliasDepsAreRequestLocal(t *testing.T) {
	from := newStore(t)
	put(t, from, aliasDepStoreSrc)

	if _, _, err := externalClosure(from, aliasOnlySrc); err != nil {
		t.Fatal(err)
	}
	direct, _, err := externalClosure(from, aliasNoDepSrc)
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 0 {
		t.Errorf("the second source references nothing external; a leaked log would show Payload: %v", direct)
	}
}

// A batch data type may not collide with a batch alias — the guard apiPut applies,
// mirrored in resolve so a lock is never written for source `put` will then refuse.
func TestResolveRefusesDataNameCollidingWithAlias(t *testing.T) {
	from := newStore(t)
	put(t, from, aliasDepStoreSrc)
	_, _, err := externalClosure(from, `(type Env {size Int})
	                                    (data Env [] (E Int))`)
	if err == nil || !strings.Contains(err.Error(), "already a type alias") {
		t.Fatalf("resolve must refuse what put refuses; got %v", err)
	}
}

// END TO END, through the real commands: resolve an alias-bearing source into a lock,
// hydrate that lock into a FRESH store, and put the source there under the lock. The
// intermediate steps are exercised by the real `oath` binary rather than by a local
// re-implementation of them, because the claim is about what the shipped commands do.
//
// The empty-store control is what makes the pass non-vacuous: if `put --lock` succeeded
// without hydrating, nothing below would be evidence that hydrate supplied anything.
func TestResolveHydratePutAliasSourceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "oath-test-bin")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	depsFile := filepath.Join(dir, "deps.oath")
	appFile := filepath.Join(dir, "app.oath")
	lockFile := filepath.Join(dir, "app.oath.lock")
	if err := os.WriteFile(depsFile, []byte(aliasDepStoreSrc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appFile, []byte(aliasOnlySrc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	storeA := filepath.Join(dir, "a")
	storeB := filepath.Join(dir, "b")
	storeEmpty := filepath.Join(dir, "empty")
	for _, d := range []string{storeA, storeB, storeEmpty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(store string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "OATH_STORE="+store, "OATH_REGISTRY=", "OATH_KEY=")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	mustRun := func(what, store string, args ...string) string {
		t.Helper()
		out, err := run(store, args...)
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", what, err, out)
		}
		return out
	}

	mustRun("put deps", storeA, "put", depsFile, "--new")
	mustRun("resolve", storeA, "resolve", appFile, "-o", lockFile)

	lockBytes, err := os.ReadFile(lockFile)
	if err != nil {
		t.Fatalf("resolve wrote no lock: %v", err)
	}
	if !strings.Contains(string(lockBytes), `"Payload"`) {
		t.Fatalf("the lock must pin the alias body's type Payload:\n%s", lockBytes)
	}

	// Control: without the closure, put --lock must fail. If it did not, the success
	// after hydrate would say nothing.
	if out, err := run(storeEmpty, "put", "--lock", lockFile, appFile, "--new"); err == nil {
		t.Fatalf("put --lock must fail against an empty store, or the hydrate below proves nothing:\n%s", out)
	}

	mustRun("hydrate", storeB, "hydrate", lockFile, "--from", storeA)
	mustRun("put --lock into the hydrated store", storeB, "put", "--lock", lockFile, appFile, "--new")

	// ...and the object is the same one the deps-present store produces.
	mustRun("put app into the source store", storeA, "put", appFile, "--new")
	a, err := OpenStore(storeA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenStore(storeB)
	if err != nil {
		t.Fatal(err)
	}
	ha, oka := a.Resolve("cost")
	hb, okb := b.Resolve("cost")
	if !oka || !okb {
		t.Fatalf("cost must be bound in both stores (a=%v b=%v)", oka, okb)
	}
	if ha != hb {
		t.Errorf("hydrating an alias-bearing source must be identity-neutral: source store %s, hydrated %s", ha, hb)
	}
}
