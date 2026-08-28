package main

import "testing"

// The native-container backends (omap/oset and the #184 smap) materialize
// results with hardcoded constructor indices — `&ctorV{idx:0}` for None/Nil,
// `idx:1` for Some/Cons. So the recognizer must admit a datatype ONLY in the
// canonical ORDER, not merely with the canonical NAMES: a reordered
// `(data Option [a] (Some a) (None))` passes a names check but would make a
// successful lookup emit `None`. This pins the order requirement (found by the
// #184 review; the bug pre-existed in omap).
func TestRecognizerRequiresCanonicalCtorOrder(t *testing.T) {
	reject := func(name, decl string, canon func(*Store, string) bool) {
		st := newStore(t)
		put(t, st, decl)
		h, ok := st.Resolve(name)
		if !ok {
			t.Fatalf("%s did not resolve", name)
		}
		if canon(st, h) {
			t.Errorf("reordered %s admitted as canonical — a native lowering would emit the wrong constructor: %s", name, decl)
		}
	}
	reject("Option", `(data Option [a] (Some a) (None))`, isCanonicalOption)      // Some=0, None=1
	reject("List", `(data List [a] (Cons a (List a)) (Nil))`, isCanonicalList)    // Cons=0, Nil=1

	// Control: the canonical order (None/Nil at 0) IS admitted, so the checks
	// above are observing the order and not rejecting everything.
	st := newStore(t)
	put(t, st, `(data Option [a] (None) (Some a))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	ho, _ := st.Resolve("Option")
	hl, _ := st.Resolve("List")
	if !isCanonicalOption(st, ho) {
		t.Errorf("canonical Option (None=0,Some=1) not admitted; the control is broken")
	}
	if !isCanonicalList(st, hl) {
		t.Errorf("canonical List (Nil=0,Cons=1) not admitted; the control is broken")
	}
}
