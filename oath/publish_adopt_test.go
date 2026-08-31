package main

import "testing"

// friction item 3: publishing under a namespace must leave the PUBLISHER's own
// store able to build on what it just published. adoptPublished binds the
// qualified name to its object locally — the object is already present, only the
// binding was missing — so a dependent published next resolves it without a fetch.

// The object lives only in the seed store (as it would after a fresh elaboration):
// adoption copies it into the local store AND binds the qualified name.
func TestAdoptPublishedCopiesAndBinds(t *testing.T) {
	seed := newMemStoreForTest(t)
	local := newMemStoreForTest(t)
	reps, err := apiPut(seed, `(defn foo [] [] Int 7)`, "tester", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("seed put: %v %+v", err, reps[0])
	}
	h := reps[0].Hash

	if _, ok := local.Resolve("michael/foo"); ok {
		t.Fatal("precondition: local must not already bind the qualified name")
	}
	newly, err := adoptPublished(local, seed, "michael/foo", h)
	if err != nil {
		t.Fatal(err)
	}
	if !newly {
		t.Error("the first adoption should report a NEW binding")
	}
	if cur, ok := local.Resolve("michael/foo"); !ok || cur != h {
		t.Errorf("michael/foo did not bind to %s locally: got %q ok=%v", h, cur, ok)
	}
	if _, err := local.GetDef(h); err != nil {
		t.Errorf("the object was not copied into the local store: %v", err)
	}

	// Idempotent: re-adopting the same (name, hash) is not a new binding, so the
	// publish summary does not over-report. (The negative control: without the
	// cur==hash short-circuit this would keep returning true.)
	newly, err = adoptPublished(local, seed, "michael/foo", h)
	if err != nil {
		t.Fatal(err)
	}
	if newly {
		t.Error("re-adopting an already-bound name at the same hash must report no change")
	}
}

// The common case: the object is ALREADY in the local store under a bare name
// (the publisher put it before publishing). Adoption must bind the qualified name
// by reusing that object, and must NOT depend on the seed still holding it.
func TestAdoptPublishedReusesLocalObject(t *testing.T) {
	seed := newMemStoreForTest(t) // deliberately empty
	local := newMemStoreForTest(t)
	reps, err := apiPut(local, `(defn foo [] [] Int 7)`, "tester", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("local put: %v %+v", err, reps[0])
	}
	h := reps[0].Hash

	newly, err := adoptPublished(local, seed, "michael/foo", h)
	if err != nil {
		t.Fatalf("adoption should reuse the local object without consulting the seed: %v", err)
	}
	if !newly {
		t.Error("the qualified name is a new binding even though the object is present")
	}
	if cur, _ := local.Resolve("michael/foo"); cur != h {
		t.Errorf("qualified name not bound to the local object: %q", cur)
	}
	// The bare name is untouched — adoption adds a binding, it does not move one.
	if cur, _ := local.Resolve("foo"); cur != h {
		t.Errorf("the original bare binding was disturbed: %q", cur)
	}
}
