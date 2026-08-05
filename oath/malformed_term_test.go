package main

import "testing"

// synth documents itself as TOTAL ON MALFORMED INPUT, and names the cases:
// "a `field` with no record, an `if` with no else, an `app` with no function or
// argument". The reason is not tidiness — GetDef revalidates objects written
// DIRECTLY into a store, which never passed the gate, so a malformed Def must
// become an error rather than a fault.
//
// `spine` violated that for `app`: it walked t = t.A while t.K == "app" and
// dereferenced nil. Found by porting `app` to the explicit machine (#149).
//
// The whole documented set is covered here, not just the case that was broken,
// because the contract quantifies over malformed children generally and a
// regression test for one door proves only that door.
func TestSynthIsTotalOnMalformedTerms(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	int1 := &Term{K: "int"}
	cases := []struct {
		name string
		term *Term
	}{
		{"app-no-function-no-argument", &Term{K: "app"}},
		{"app-no-argument", &Term{K: "app", A: int1}},
		{"app-no-function", &Term{K: "app", B: int1}},
		{"app-nested-missing-head", &Term{K: "app", A: &Term{K: "app"}, B: int1}},
		{"if-no-condition", &Term{K: "if", B: int1, C: int1}},
		{"if-no-then", &Term{K: "if", A: &Term{K: "bool"}, C: int1}},
		{"if-no-else", &Term{K: "if", A: &Term{K: "bool"}, B: int1}},
		{"let-no-bound", &Term{K: "let", Ty: tInt(), B: int1}},
		{"let-no-body", &Term{K: "let", Ty: tInt(), A: int1}},
		{"let-no-annotation", &Term{K: "let", A: int1, B: int1}},
		{"lam-no-body", &Term{K: "lam", Ty: tInt()}},
		{"lam-no-annotation", &Term{K: "lam", A: int1}},
		{"field-no-record", &Term{K: "field", Op: "x"}},
		{"prim-no-args", &Term{K: "prim", Op: "+"}},
		{"eq-one-arg", &Term{K: "prim", Op: "==", Args: []Term{*int1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &checker{st: st}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("synth FAULTED on malformed input: %v\n"+
						"  the contract is to REJECT, because GetDef revalidates objects "+
						"that never passed the gate", r)
				}
			}()
			if _, err := c.synth(nil, tc.term); err == nil {
				t.Errorf("malformed term was ACCEPTED; it must be rejected")
			}
		})
	}
}
