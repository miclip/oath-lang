package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// externalClosure finds the EXTERNAL names a source references and their transitive
// object closure, excluding the batch's own definitions.
func TestExternalClosure(t *testing.T) {
	from := newStore(t)
	put(t, from, `(defn base [] [(x Int)] Int (+ x 1))`)
	baseHash, _ := from.Resolve("base")

	direct, closure, err := externalClosure(from, `(defn user [] [(x Int)] Int (base (base x)))`)
	if err != nil {
		t.Fatal(err)
	}
	if direct["base"] != baseHash {
		t.Errorf("external dep 'base' should pin to %s, got %v", baseHash, direct)
	}
	if len(direct) != 1 {
		t.Errorf("only 'base' is external (Int and + are builtins), got %v", direct)
	}
	found := false
	for _, h := range closure {
		if h == baseHash {
			found = true
		}
	}
	if !found {
		t.Errorf("the closure must include base %s: %v", baseHash, closure)
	}
}

// verifyLock recomputes the source's external set against the store and requires
// the lock to match it EXACTLY: a matching lock passes; a wrong hash, a missing
// entry (stale lock), and an extra entry (lock for another file) all fail.
func TestVerifyLock(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn base [] [(x Int)] Int (+ x 1))`)
	h, _ := st.Resolve("base")
	src := `(defn user [] [(x Int)] Int (base x))`

	if err := verifyLock(st, src, oathLock{Dependencies: map[string]string{"base": h}}); err != nil {
		t.Errorf("a matching lock must verify: %v", err)
	}
	if err := verifyLock(st, src, oathLock{Dependencies: map[string]string{"base": "deadbeef"}}); err == nil {
		t.Error("a mismatched hash must fail verification")
	} else if !strings.Contains(err.Error(), "the lock pins") {
		t.Errorf("the mismatch must be named precisely: %v", err)
	}
	// Stale lock: the source references base, but the lock omits it.
	if err := verifyLock(st, src, oathLock{Dependencies: map[string]string{}}); err == nil {
		t.Error("a lock that omits a referenced dependency must fail (stale lock)")
	}
	// Extra entry: the lock pins a name the source does not reference.
	if err := verifyLock(st, src, oathLock{Dependencies: map[string]string{"base": h, "unused": h}}); err == nil {
		t.Error("a lock with an unreferenced pin must fail (wrong file / stale lock)")
	}
}

// The falsifier as a unit test: a consumer resolved into a FRESH store (its deps
// fetched, its external names bound) elaborates to the SAME hash as against the
// store that already held the deps — identity is a function of the closure, not
// of the store.
func TestResolveCrossStoreIdentity(t *testing.T) {
	from := newStore(t)
	put(t, from, `(defn base [] [(x Int)] Int (+ x 1))`)
	src := `(defn user [] [(x Int)] Int (base (base x)))`

	// Simulate `oath resolve --from`: fetch the closure into a fresh target, bind
	// the direct external names.
	target := newStore(t)
	direct, closure, err := externalClosure(from, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range closure {
		d, err := from.GetDef(h)
		if err != nil {
			t.Fatal(err)
		}
		m, _ := from.GetMeta(h)
		if m == nil {
			m = &Meta{}
		}
		if _, err := target.StoreObject(d, m); err != nil {
			t.Fatal(err)
		}
	}
	for n, h := range direct {
		if _, err := target.Repoint(n, h); err != nil {
			t.Fatal(err)
		}
	}
	// verifyLock must now pass against the freshly-populated target.
	if err := verifyLock(target, src, oathLock{Dependencies: direct}); err != nil {
		t.Fatalf("the fetched store must satisfy its own lock: %v", err)
	}

	rTarget := put(t, target, src)
	rFrom := put(t, from, src)
	ht := rTarget[len(rTarget)-1].Hash
	hf := rFrom[len(rFrom)-1].Hash
	if ht == "" || ht != hf {
		t.Errorf("cross-store identity broken: fresh+fetched %q vs deps-present %q", ht, hf)
	}
}

// P1: a datatype reached only through a CONSTRUCTOR (a string literal, `(Cons ...)`,
// a match arm) — not an explicit type application — must still be captured, or a
// fresh store cannot resolve the constructor.
func TestExternalClosureCapturesConstructorDeps(t *testing.T) {
	from := newStore(t)
	put(t, from, `(data Color [] (Red) (Green))`)
	// user references the constructor Red — no type application mentions Color.
	direct, closure, err := externalClosure(from, `(defn user [] [] Color (Red))`)
	if err != nil {
		t.Fatal(err)
	}
	colorHash, _ := from.Resolve("Color")
	if direct["Color"] != colorHash {
		t.Errorf("a datatype reached via a constructor must be pinned: got %v", direct)
	}
	found := false
	for _, h := range closure {
		if h == colorHash {
			found = true
		}
	}
	if !found {
		t.Errorf("the closure must include the constructor's datatype: %v", closure)
	}
}

// P2: an early form references an ambient name that a LATER form redeclares. The
// early reference is genuinely external and must be captured, even though the name
// ends up declared in the batch.
func TestExternalClosureOrderDependentShadowing(t *testing.T) {
	from := newStore(t)
	put(t, from, `(defn base [] [(x Int)] Int (+ x 1))`)
	baseHash, _ := from.Resolve("base")
	// form1 uses external base; form2 redeclares base locally.
	src := `(defn user [] [(x Int)] Int (base x))
	        (defn base [] [(x Int)] Int (* x 2))`
	direct, _, err := externalClosure(from, src)
	if err != nil {
		t.Fatal(err)
	}
	if direct["base"] != baseHash {
		t.Errorf("form1's external `base` must be pinned to the ambient hash %s despite form2 redeclaring it, got %v",
			baseHash, direct)
	}
}

// The same-hash-alias case: an early batch form is byte-identical to an ambient
// definition under ANOTHER name, and a later form references that ambient name. It
// is external (declared by no earlier form) and its object must be fetched, even
// though a batch form shares its hash. Classification is by name, not hash.
func TestExternalClosureSameHashAlias(t *testing.T) {
	from := newStore(t)
	put(t, from, `(defn foo [] [(x Int)] Int (+ x 1))`)
	fooHash, _ := from.Resolve("foo")
	src := `(defn bar [] [(x Int)] Int (+ x 1))
	        (defn user [] [(x Int)] Int (foo x))`
	direct, closure, err := externalClosure(from, src)
	if err != nil {
		t.Fatal(err)
	}
	if direct["foo"] != fooHash {
		t.Errorf("foo is external despite bar sharing its hash: %v", direct)
	}
	found := false
	for _, h := range closure {
		if h == fooHash {
			found = true
		}
	}
	if !found {
		t.Errorf("the closure must include foo's object %s even though bar produces the same hash: %v", fooHash, closure)
	}
}


// The object endpoint round-trips: its payload decodes back to the same object with
// its content address preserved — the primitive oath resolve --remote fetches by.
func TestApiObjectPayloadRoundTrips(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn base [] [(x Int)] Int (+ x 1))`)
	h, _ := st.Resolve("base")

	payload, err := apiObjectPayload(st, h)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		DefB64  string `json:"def_b64"`
		MetaB64 string `json:"meta_b64"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, payload)
	}
	defBytes, err := base64.StdEncoding.DecodeString(p.DefB64)
	if err != nil {
		t.Fatalf("def_b64 not base64: %v", err)
	}
	d, err := decodeDef(defBytes)
	if err != nil {
		t.Fatalf("def did not decode: %v", err)
	}
	if got := hashDef(d); got != h {
		t.Errorf("round-tripped object hashes to %s, want %s", got, h)
	}
	if _, err := apiObjectPayload(st, "deadbeef"); err == nil {
		t.Error("a missing hash must error, not return a payload")
	}
}
