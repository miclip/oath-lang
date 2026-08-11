package main

import (
	"fmt"
	"strings"
	"testing"
)

// CONTROLS FOR THE #161 GUARDS on examples/config.oath.
//
// Two committed properties asserted something false: `config-has-key.finds-head`
// and `config-missing.complete-config-reports-nothing` both quantified over a
// `Str` key with no constraint against the key containing `config-key`'s
// delimiter. Each is now guarded by `(== (config-key key) key)` — the key
// round-trips through the parser — and both are PROVEN over unbounded ints.
//
// A PROOF IS NOT ENOUGH ON ITS OWN, WHICH IS THE ENTIRE REASON THIS FILE
// EXISTS. A vacuous property proves just as readily as a substantive one: had
// the guard been unsatisfiable, `finds-head` would still report `∎ PROVEN` and
// the record would be worse than before, because a false claim would have been
// replaced by an empty one carrying a stronger badge. So the guard needs
// evidence on BOTH sides, and neither half is sufficient alone:
//
//	FIRING      at key "a=b" the guard is FALSE, and the claim it excludes is
//	            genuinely false there. The guard removes a real falsifier
//	            rather than being decorative.
//	NON-FIRING  at key "a" the guard is TRUE, so the claim is EXERCISED, and it
//	            holds. The guard is satisfiable by ordinary keys rather than
//	            excluding everything.
//
// The two together are what distinguish "narrowed to the domain where the
// claim is true" from "narrowed until nothing is claimed".
//
// WHAT THIS DOES NOT ESTABLISH. The guard is written in terms of `config-key`,
// which `config-has-key` also calls, so a mutation of `config-key` moves the
// guard and the claim together. These properties are evidence about
// `config-has-key` GIVEN `config-key`'s contract, not about the pair; the
// separately-proven `config-key` properties are what carry that half.

// evalBool runs a source expression against the committed store and requires a
// boolean answer. A non-boolean result is a failure rather than a false: the
// controls below are only meaningful if each expression is the proposition it
// is being read as.
func evalBool(t *testing.T, st *Store, src string) bool {
	t.Helper()
	out, err := apiEval(st, src)
	if err != nil {
		t.Fatalf("eval %s: %v", src, err)
	}
	switch strings.TrimSpace(out) {
	case "true : Bool":
		return true
	case "false : Bool":
		return false
	}
	t.Fatalf("eval %s: expected a Bool, got %q — this control is not measuring a proposition", src, out)
	return false
}

func TestConfigGuardFiresOnRealFalsifiers(t *testing.T) {
	st := openCommittedStore(t)

	// --- finds-head ------------------------------------------------------
	// FIRING. "a=b" is a key that does not survive config-key: the line
	// "a=b" ++ "=v" parses to key "a", the head does not match, and the search
	// falls through to the unconstrained tail.
	if got := evalBool(t, st, `(== (config-key "a=b") "a=b")`); got {
		t.Error(`FIRING control failed: the guard holds at key "a=b", so it excludes nothing there`)
	}
	if got := evalBool(t, st, `(config-has-key (Cons [Str] (str-append "a=b" "=v") (Nil [Str])) "a=b")`); got {
		t.Error(`FIRING control failed: the UNGUARDED finds-head claim is TRUE at key "a=b", ` +
			`so the guard is excluding a case that never needed excluding`)
	}
	// The guarded property as written must therefore be true at "a=b" — true
	// BY EXCLUSION, which is what the two assertions above establish it is.
	if got := evalBool(t, st,
		`(if (== (config-key "a=b") "a=b") (config-has-key (Cons [Str] (str-append "a=b" "=v") (Nil [Str])) "a=b") true)`); !got {
		t.Error(`the guarded finds-head property is FALSE at key "a=b"; the guard does not cover its own falsifier`)
	}

	// NON-FIRING. "a" is an ordinary key: the guard admits it and the claim
	// holds, so the property has content on the inputs it still quantifies over.
	if got := evalBool(t, st, `(== (config-key "a") "a")`); !got {
		t.Error(`NON-FIRING control failed: the guard REJECTS the ordinary key "a"; the property may be vacuous`)
	}
	if got := evalBool(t, st, `(config-has-key (Cons [Str] (str-append "a" "=v") (Nil [Str])) "a")`); !got {
		t.Error(`NON-FIRING control failed: the finds-head claim is FALSE at the admitted key "a"`)
	}

	// --- complete-config-reports-nothing ---------------------------------
	// FIRING, same cause reached through config-has-key. Compared against the
	// empty string rather than eyeballed: the falsifying result is the key
	// itself coming back as "missing" from a config that contains it.
	if got := evalBool(t, st, `(== (config-key "a=b") "a=b")`); got {
		t.Error(`FIRING control failed: the guard holds at k "a=b"`)
	}
	if got := evalBool(t, st,
		`(== (config-missing (Cons [Str] (str-append "a=b" (str-append "=" "v")) (Nil [Str])) (Cons [Str] "a=b" (Nil [Str]))) (SNil))`); got {
		t.Error(`FIRING control failed: the UNGUARDED complete-config-reports-nothing claim is TRUE at k "a=b"`)
	}

	// NON-FIRING, and the isolation control with it: '=' in the VALUE is
	// harmless, which is why only `k` is guarded and `v` is left free.
	if got := evalBool(t, st,
		`(== (config-missing (Cons [Str] (str-append "a" (str-append "=" "v")) (Nil [Str])) (Cons [Str] "a" (Nil [Str]))) (SNil))`); !got {
		t.Error(`NON-FIRING control failed: the claim is FALSE at the admitted key k "a"`)
	}
	if got := evalBool(t, st,
		`(== (config-missing (Cons [Str] (str-append "a" (str-append "=" "x=y")) (Nil [Str])) (Cons [Str] "a" (Nil [Str]))) (SNil))`); !got {
		t.Error(`ISOLATION control failed: a '=' in the VALUE falsifies the claim, so guarding k alone is not enough`)
	}
}

// TestStoredConfigPropertiesCarryTheGuard is the assertion that makes the
// controls above mean anything about the COMMITTED objects.
//
// Everything in TestConfigGuardFiresOnRealFalsifiers evaluates hand-written
// expressions, so it establishes facts about `config-key`'s contract and would
// pass whether or not the stored properties were ever guarded. This evaluates
// the STORED property body — the bytes in the object the name resolves to —
// at the exact binding the guard exists to exclude.
//
// THE MUTATION THAT MAKES THIS FAIL: delete the guard from either property in
// examples/config.oath and re-put. The body then evaluates to false at
// key = "a=b" and this test fails, because that binding is a genuine falsifier
// (TestConfigGuardFiresOnRealFalsifiers is what establishes it is genuine).
// Nothing else in the suite notices, because the generator cannot produce that
// binding — which is #161 restated as a test, and #162 as its cause.
//
// THE DISCRIMINATION IS ASSERTED, NOT TAKEN ON TRUST, and it does not need
// that mutation to be run by hand. The sibling test evaluates the UNGUARDED
// expression at key "a=b" and requires FALSE; this one evaluates the STORED
// body at the same binding, through the same evaluator over the same store,
// and requires TRUE. Two contradictory results at one point mean the stored
// body is not the unguarded claim — checked on every run rather than once.
func TestStoredConfigPropertiesCarryTheGuard(t *testing.T) {
	st := openCommittedStore(t)
	strHash := strTypeHash(st)
	if strHash == "" {
		t.Fatal("Str is not bound in the committed store")
	}
	strDef, err := st.GetDef(strHash)
	if err != nil {
		t.Fatalf("reading the Str datatype: %v", err)
	}
	snilIdx, sconsIdx := -1, -1
	for i, c := range strDef.Ctors {
		switch len(c) {
		case 0:
			snilIdx = i
		case 2:
			sconsIdx = i
		}
	}
	if snilIdx < 0 || sconsIdx < 0 {
		t.Fatalf("Str does not have the expected nil/cons shape: %v", strDef.Ctors)
	}

	// "a=b" — a key that does not survive config-key — and "a", one that does.
	excluded := mkStr(strHash, snilIdx, sconsIdx, []int64{97, 61, 98})
	admitted := mkStr(strHash, snilIdx, sconsIdx, []int64{97})

	cases := []struct {
		defName  string
		propName string
		guarded  int // the binder the guard constrains
	}{
		{"config-has-key", "finds-head", 1},
		{"config-missing", "complete-config-reports-nothing", 0},
	}

	for _, tc := range cases {
		h, d, pi := propIndex(t, st, tc.defName, tc.propName)
		p := &d.Props[pi]

		// Build the remaining binders from the kernel's own generator at size
		// 0, so the non-guarded positions are base-case values (Nil, "") rather
		// than hand-assembled ones this test would have to keep in step with
		// their types.
		mkEnv := func(key Value) []Value {
			env := make([]Value, len(p.Binders))
			for i := range p.Binders {
				if i == tc.guarded {
					env[i] = key
					continue
				}
				v, err := genValue(st, &p.Binders[i], 0, &rng{s: 0})
				if err != nil {
					t.Fatalf("%s.%s: building binder %d: %v", tc.defName, tc.propName, i, err)
				}
				env[i] = v
			}
			return env
		}
		run := func(env []Value) (bool, error) {
			ev := &evaluator{st: st, fuel: propFuel}
			out, err := ev.eval(env, h, &p.Body)
			if err != nil {
				return false, err
			}
			if out.K != "bool" {
				return false, fmt.Errorf("property body evaluated to %s, not a bool", out.K)
			}
			return out.Bool, nil
		}

		// THE DISCRIMINATING ASSERTION. Guarded: true by exclusion. Unguarded:
		// false, and this fails.
		got, err := run(mkEnv(excluded))
		if err != nil {
			t.Fatalf("%s.%s at the excluded key: %v", tc.defName, tc.propName, err)
		}
		if !got {
			t.Errorf("%s.%s evaluates FALSE at key \"a=b\" — the COMMITTED property does not carry the guard. "+
				"This is the #161 defect, present in the store.", tc.defName, tc.propName)
		}

		// CONTROL: the same property on an admitted key must also hold, and
		// this one is NOT true by exclusion — it is the claim actually being
		// made. Without it, a property guarded into vacuity would pass above.
		got, err = run(mkEnv(admitted))
		if err != nil {
			t.Fatalf("%s.%s at the admitted key: %v", tc.defName, tc.propName, err)
		}
		if !got {
			t.Errorf("%s.%s evaluates FALSE at key \"a\", which the guard admits — the claim itself is broken",
				tc.defName, tc.propName)
		}
	}
}

// TestConfigGuardCostsNoGeneratedCases asks what the guard COST: how many of
// the 200 cases verification actually runs does it exclude?
//
// The answer is zero, and the reason is the defect #162 tracks — the generator
// draws Str codepoints from the generic Int arm over [-20,20], which contains no
// printable ASCII, so no generated key can hold '=' and none of the tester's
// cases were ever in the excluded region. The guard therefore narrows the
// property's STATED domain without narrowing the domain it is TESTED on.
//
// That is a fact worth asserting rather than assuming: if a future generator
// change makes '=' reachable, this count becomes non-zero and the honest
// reading of the `tested` figure changes with it. Failing loudly then is the
// point.
func TestConfigGuardCostsNoGeneratedCases(t *testing.T) {
	st := openCommittedStore(t)
	strHash := strTypeHash(st)
	if strHash == "" {
		t.Fatal("Str is not bound in the committed store")
	}

	// The excluded shape is "the key contains config-key's delimiter". 61 is
	// spelled out HERE, in a measurement of the generator, deliberately: the
	// properties themselves derive the domain from config-key's contract
	// instead, so the constant lives in exactly one normative place.
	const delimiter = 61

	cases := []struct {
		defName  string
		propName string
		binder   int // the guarded binder, positionally, per the source declaration
		excluded int // RECORDED: cases of the 200 the guard removes
	}{
		{"config-has-key", "finds-head", 1, 4},                      // (rest, key)
		{"config-missing", "complete-config-reports-nothing", 0, 3}, // (k, v)
	}

	for _, tc := range cases {
		// Reuses the resolver the sibling outcome controls already use, rather
		// than re-deriving a property index beside it.
		h, d, pi := propIndex(t, st, tc.defName, tc.propName)
		p := &d.Props[pi]
		if tc.binder >= len(p.Binders) {
			t.Fatalf("%s.%s has %d binders; the positional map expects at least %d", tc.defName, tc.propName, len(p.Binders), tc.binder+1)
		}
		if !isStrTy(strHash, &p.Binders[tc.binder]) {
			t.Fatalf("%s.%s binder %d is not Str; the positional binder map is stale", tc.defName, tc.propName, tc.binder)
		}

		// The tester's own seed path, not a lookalike — same two functions
		// runProp binds its cases from.
		sch := mustSchedule(st, h)
		excluded, nonEmpty, totalCps := 0, 0, 0
		for c := 0; c < propCases; c++ {
			env, err := genPropCase(st, p, sch, pi, c)
			if err != nil {
				t.Fatalf("%s.%s case %d: %v", tc.defName, tc.propName, c, err)
			}
			cps, ok := strCodepoints(env[tc.binder])
			if !ok {
				t.Fatalf("%s.%s case %d binder %d: not a Str spine", tc.defName, tc.propName, c, tc.binder)
			}
			if len(cps) > 0 {
				nonEmpty++
			}
			for _, cp := range cps {
				totalCps++
				if cp == delimiter {
					excluded++
					break
				}
			}
		}

		// CONTROL: a zero excluded-count is only evidence if the generator
		// produced strings at all. Without this, an empty-string generator
		// would report the same zero and mean the opposite.
		if totalCps == 0 || nonEmpty == 0 {
			t.Fatalf("%s.%s binder %d: the generator produced no codepoints over %d cases (%d non-empty); "+
				"a zero exclusion count would not be evidence", tc.defName, tc.propName, tc.binder, propCases, nonEmpty)
		}
		t.Logf("%s.%s binder %d: %d/%d generated cases excluded by the guard (%d non-empty keys, %d codepoints)",
			tc.defName, tc.propName, tc.binder, excluded, propCases, nonEmpty, totalCps)
		// THE RECORDED FIGURE WAS 0 AND IS NOW NON-ZERO, AND THAT IS THE
		// REPAIR LANDING RATHER THAN A REGRESSION.
		//
		// This test used to assert the guard cost NOTHING: the generator could
		// not produce the delimiter, so the guard narrowed the property's
		// STATED domain without narrowing the domain it was TESTED on — which
		// meant the `tested` figure was covering cases the guard had already
		// excluded on paper only. SPEC §4's literal rule closed that: the
		// delimiter is now generated, the guard now removes real cases, and the
		// `tested` figure means what it says.
		//
		// Pinned by EQUALITY, exactly as tightly as the zero was.
		if excluded != tc.excluded {
			t.Errorf("RECORDED MEASUREMENT IS STALE — %s.%s binder %d: the guard excludes %d of the %d "+
				"cases verification runs, where this file records %d. Re-read this file's figures and "+
				"anything quoting them, then update them together.",
				tc.defName, tc.propName, tc.binder, excluded, propCases, tc.excluded)
		}
		// CONTROL: a zero would mean §4's rule never puts the delimiter in
		// this binder, and the guard would once again be narrowing only the
		// stated domain.
		if excluded == 0 {
			t.Errorf("%s.%s binder %d: the guard excludes NO generated case, so it narrows the stated "+
				"domain without narrowing the tested one", tc.defName, tc.propName, tc.binder)
		}
	}
}
