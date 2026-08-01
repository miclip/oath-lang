package main

import (
	"strings"
	"testing"
)

// A blind review of the transfer text asked whether transferring a PARENT prefix
// could seize a nested prefix held by a third party, or clear its delegations.
//
// It cannot, and the reason is upstream of transfer entirely: RES-FIRST-COME
// forbids overlapping prefixes across keys, so `al/*` and `al/sub/*` can never be
// held by two different principals. The attack has no reachable starting state.
//
// Pinned as a test because the argument is non-local — someone reading the
// transfer rules alone would not find the protection, and relaxing the overlap
// rule later would silently open this.
func TestNestedPrefixSeizureHasNoReachableState(t *testing.T) {
	st := newMemStoreForTest(t)
	aHex, a := newKey(t)
	bHex, b := newKey(t)
	reserveFor(t, st, aHex, a, "al/*")

	oct, sig := signRes(t, b, "al/sub/*", noAuthority, 0)
	_, err := apiReserve(st, oct, sig, bHex)
	if err == nil {
		t.Fatal("a third party reserved a prefix nested under another key's — the transfer " +
			"seizure scenario is now reachable and XFER-NO-CAPTURE does not cover prefixes")
	}
	if !strings.Contains(err.Error(), "overlap") {
		t.Errorf("refused for the wrong reason, so the protection may not be the overlap rule: %v", err)
	}
}
