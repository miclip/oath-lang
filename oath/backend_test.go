package main

import (
	"reflect"
	"testing"
	"time"
)

func memStore(t *testing.T) *Store {
	t.Helper()
	st, err := newStoreWithBackend(newMemBackend(), "mem")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The same source through the fs backend and the in-memory backend must produce
// byte-identical identity (the hash is backend-independent), the same object
// set, and a journal that verifies on both — proof the seam is faithful, not
// merely filesystem-shaped.
func TestBackendParityFSvsMem(t *testing.T) {
	src := `(defn dbl [] [(x Int)] Int (+ x x)
		(prop is-double [(x Int)] (== (dbl x) (+ x x))))`

	fsSt := newStore(t)
	memSt := memStore(t)
	for _, st := range []*Store{fsSt, memSt} {
		if _, err := apiPut(st, src, "tester", ""); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	fh, fok := fsSt.Resolve("dbl")
	mh, mok := memSt.Resolve("dbl")
	if !fok || !mok || fh != mh || fh == "" {
		t.Fatalf("identity diverged across backends: fs=%q mem=%q", fh, mh)
	}
	if err := fsSt.VerifyLog(); err != nil {
		t.Fatalf("fs journal: %v", err)
	}
	if err := memSt.VerifyLog(); err != nil {
		t.Fatalf("mem journal: %v", err)
	}
	if !reflect.DeepEqual(fsSt.AllHashes(), memSt.AllHashes()) {
		t.Fatalf("object set diverged: fs=%v mem=%v", fsSt.AllHashes(), memSt.AllHashes())
	}
	if fm, _ := fsSt.GetMeta(fh); fm.Guarantee.Level != "tested" {
		t.Fatalf("fs guarantee=%q, want tested", fm.Guarantee.Level)
	}
	if mm, _ := memSt.GetMeta(mh); mm.Guarantee.Level != "tested" {
		t.Fatalf("mem guarantee=%q, want tested", mm.Guarantee.Level)
	}
}

// The proof queue works over the in-memory backend with the same lease/reclaim
// semantics the fs backend has.
func TestMemBackendProofQueue(t *testing.T) {
	st := memStore(t)
	if err := st.EnqueueProof(ProofJob{Hash: "h1", Name: "n", Gate: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000_000, 0)
	j, err := st.ClaimProof(now, 10*time.Minute)
	if err != nil || j == nil || j.Hash != "h1" {
		t.Fatalf("claim: job=%v err=%v", j, err)
	}
	if j2, _ := st.ClaimProof(now.Add(time.Minute), 10*time.Minute); j2 != nil {
		t.Fatalf("claimed a live-leased job: %v", j2)
	}
	if j3, _ := st.ClaimProof(now.Add(20*time.Minute), 10*time.Minute); j3 == nil {
		t.Fatal("stale lease not reclaimed")
	}
	_ = st.CompleteProof("h1")
	if d := st.ProofQueueDepth(); d != 0 {
		t.Fatalf("depth after complete = %d, want 0", d)
	}
}
