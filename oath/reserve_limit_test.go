package main

import (
	"strings"
	"testing"
)

// RES-RESERVATION-LIMIT: one key may hold at most maxReservationsPerPrincipal
// namespaces.
func TestReservationLimit(t *testing.T) {
	st := newMemStoreForTest(t)
	pubHex, priv := newKey(t)

	for i := 0; i < maxReservationsPerPrincipal; i++ {
		ns := string(rune('a'+i)) + "space/*"
		oct, sig := signRes(t, priv, ns, noAuthority, 0)
		if _, err := apiReserve(st, oct, sig, pubHex); err != nil {
			t.Fatalf("reservation %d of %d was refused: %v", i+1, maxReservationsPerPrincipal, err)
		}
	}

	// One past the limit.
	oct, sig := signRes(t, priv, "overflow/*", noAuthority, 0)
	_, err := apiReserve(st, oct, sig, pubHex)
	if err == nil {
		t.Fatalf("a key held %d namespaces and was allowed another", maxReservationsPerPrincipal)
	}
	for _, want := range []string{"limit", "NEST", "permanent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal is missing %q — it should say what to do instead:\n%v", want, err)
		}
	}
	if h, _ := reservationRev(st, "overflow/*"); h != noAuthority {
		t.Error("the refused reservation was recorded anyway")
	}

	// The limit is PER KEY. A different principal is unaffected.
	otherHex, otherPriv := newKey(t)
	oct, sig = signRes(t, otherPriv, "elsewhere/*", noAuthority, 0)
	if _, err := apiReserve(st, oct, sig, otherHex); err != nil {
		t.Errorf("a different key was blocked by another key's usage: %v", err)
	}

	// Nesting is the intended route past the limit, and must still work: a prefix
	// already held covers everything beneath it, so no new claim is needed.
	if !namespaceCovers("aspace/*", "aspace/deep/thing") {
		t.Error("a held prefix does not cover names beneath it, so the error's advice is wrong")
	}
}

// The existing live configuration must not be retroactively illegal: the operator
// key holds two namespaces (handle variants), which is a legitimate pattern.
func TestReservationLimitAllowsHandleVariants(t *testing.T) {
	if maxReservationsPerPrincipal < 2 {
		t.Fatalf("limit of %d breaks holding handle variants, which the live registry already does",
			maxReservationsPerPrincipal)
	}
}
