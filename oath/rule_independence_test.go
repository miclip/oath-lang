//go:build conformance_mutation

package main

import "testing"

// The two §8.6.4a conditions on `A` must be INDEPENDENTLY switchable, or the
// mutation harness attributes a failure to whichever rule happened to be off.
//
// The hazard is specific: rejectWeakKey needs a decoded point to take an order,
// so the canonical decode runs first. An early return when it fails would mean
// disabling SIG-POINTS-CANONICAL also disabled SIG-SMALL-ORDER — silently, and
// only for exactly the inputs where both apply, which is where attribution
// matters most.
func TestPointRulesAreIndependentlySwitchable(t *testing.T) {
	// y = p: non-canonical, and it reduces to y = 0, a point of order 4. Both
	// rules have something to say about it, which is what makes it the probe.
	key := make([]byte, 32)
	key[0] = 0xed
	for i := 1; i < 31; i++ {
		key[i] = 0xff
	}
	key[31] = 0x7f

	if err := rejectWeakKey(key); err == nil {
		t.Fatal("both rules enabled: the key was accepted")
	}
	withRulesDisabled([]string{"SIG-POINTS-CANONICAL"}, func() {
		if err := rejectWeakKey(key); err == nil {
			t.Error("canonicity off: SIG-SMALL-ORDER should still refuse the order-4 point it reduces to")
		}
	})
	withRulesDisabled([]string{"SIG-SMALL-ORDER"}, func() {
		if err := rejectWeakKey(key); err == nil {
			t.Error("small-order off: SIG-POINTS-CANONICAL should still refuse a non-canonical encoding")
		}
	})
	// And the control: with BOTH off it must be admitted, or the test above would
	// pass for a rejectWeakKey that refuses everything unconditionally.
	withRulesDisabled([]string{"SIG-POINTS-CANONICAL", "SIG-SMALL-ORDER"}, func() {
		if err := rejectWeakKey(key); err != nil {
			t.Errorf("both rules off: the key was still refused (%v), so something else is rejecting it "+
				"and neither rule is witnessed by this input", err)
		}
	})
}
