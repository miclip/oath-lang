package main

import (
	"fmt"
	"sort"
	"testing"
)

// #118's LOAD-BEARING QUESTION, answered with a measurement rather than by
// analogy: does typed lowering need something the verified `Def` closure cannot
// express, or is the closure still the neutral representation of a body?
//
// #114 deliberately did NOT invent an expression IR, on the grounds that the
// Def closure already is that representation. Typed lowering is where that
// claim either holds or breaks, because a typed backend needs the type of
// EVERY subterm, and the closure stores types only on BINDERS (`lam`, `let`).
//
// The claim under test: every subterm's type is RECOVERABLE from the closure
// plus the checker, so no separate typed IR is required to know types.
//
// It uses `GetDef` DELIBERATELY, because that is what a backend calls and
// therefore what a backend sees. The separate question of what the STORED bytes
// carry — before the load path's inference backfills anything — is measured by
// TestPolymorphicCallSitesCarryTheirInstantiation, and the two differ.
//
// This is a measurement over the whole committed corpus, not a sample. If it
// ever reports a gap, that gap is the argument for a typed IR — and the gap
// itself will say exactly which construct needs one.
func TestEverySubtermTypeIsRecoverableFromTheDefClosure(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skipf("committed store unavailable: %v", err)
	}
	byName := st.Names()
	if len(byName) < 50 {
		t.Fatalf("only %d names in the store; this is meant to measure the whole corpus", len(byName))
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	var terms, recovered int
	gaps := map[string]int{}
	for _, name := range names {
		h, ok := byName[name]
		if !ok {
			continue
		}
		d, err := st.GetDef(h)
		if err != nil {
			t.Errorf("%s (%s) will not load: %v — a whole-corpus measurement that skips it is measuring a subset",
				name, shortHash(h), err)
			continue
		}
		if d.K != "func" || d.Body == nil {
			continue
		}
		chk := &checker{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
		var walk func(ctx []*Ty, tm *Term)
		walk = func(ctx []*Ty, tm *Term) {
			if tm == nil {
				return
			}
			terms++
			if _, err := chk.synth(ctx, tm); err == nil {
				recovered++
			} else {
				gaps[tm.K]++
			}
			switch tm.K {
			case "lam":
				walk(append(ctx, tm.Ty), tm.A)
				return
			case "let":
				walk(ctx, tm.A)
				walk(append(ctx, tm.Ty), tm.B)
				return
			case "match":
				walk(ctx, tm.A)
				ad, err := st.GetDef(tm.Hash)
				if err != nil {
					return
				}
				// The arm binders need the ADT's INSTANTIATION, exactly as the
				// emitter computes it. Pushing placeholders instead would make
				// the measurement report gaps that are artefacts of the walker.
				scrutTy, tyErr := chk.synth(ctx, tm.A)
				for i := range tm.Arms {
					if i >= len(ad.Ctors) {
						break
					}
					fieldTys := make([]*Ty, len(ad.Ctors[i]))
					for f := range ad.Ctors[i] {
						fieldTys[f] = &ad.Ctors[i][f]
					}
					if tyErr == nil && scrutTy != nil && scrutTy.K == "data" {
						fieldTys = instCtorFields(ad, scrutTy.Hash, scrutTy.Args, i)
					}
					sub := append([]*Ty{}, ctx...)
					sub = append(sub, fieldTys...)
					walk(sub, &tm.Arms[i])
				}
				return
			}
			walk(ctx, tm.A)
			walk(ctx, tm.B)
			walk(ctx, tm.C)
			for i := range tm.Args {
				walk(ctx, &tm.Args[i])
			}
		}
		walk(nil, d.Body)
	}

	if terms == 0 {
		t.Fatal("walked no terms; the measurement is not reading anything")
	}
	pct := 100 * float64(recovered) / float64(terms)
	kinds := make([]string, 0, len(gaps))
	for k := range gaps {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	summary := ""
	for _, k := range kinds {
		summary += fmt.Sprintf(" %s=%d", k, gaps[k])
	}
	t.Logf("subterm types recoverable from the Def closure: %d/%d (%.1f%%); unrecovered by kind:%s",
		recovered, terms, pct, summary)

	// EVERY kind, not a list I chose. A hardcoded subset would pass a real gap
	// in `lam`, `let`, `self`, or any term form added later — which is the same
	// mistake as measuring the ports I expected instead of every listener.
	for _, k := range kinds {
		t.Errorf("%d %q subterms have no recoverable type — a typed lowering IR would be justified for %s",
			gaps[k], k, k)
	}
}

// The SECOND thing a typed lowering IR is usually justified by: monomorphisation
// needs to know, at each call site, which types a polymorphic callee was
// instantiated at.
//
// THE FIRST VERSION OF THIS TEST MEASURED THE WRONG THING, and the mistake is
// worth keeping visible because it is the one this project keeps making. It
// called `GetDef`, which runs `checkDef`, which INFERS omitted type arguments
// and backfills them into the returned AST. So it reported that every call site
// carries its instantiation — while inference was what put them there. A
// measurement of "no inference is needed" whose data came from inference.
//
// So it measures BOTH, and the delta is the finding:
//
//   raw     — what the canonical stored bytes carry, via decodeDef, no checkDef
//   loaded  — what a backend sees, after the store's load path has inferred
//
// A monomorphising backend genuinely does see concrete arguments everywhere.
// But "the Def closure is the neutral representation of a body" quietly means
// "as reconstituted by the checker" for exactly the raw-vs-loaded difference,
// and #118 should know which of the two it is relying on.
func TestPolymorphicCallSitesCarryTheirInstantiation(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skipf("committed store unavailable: %v", err)
	}
	byName := st.Names()
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	type counts struct{ sites, withArgs, throughTyVar int }
	var raw, loaded counts

	// A call site is polymorphic if its CALLEE takes type parameters. `self` is
	// one too: a polymorphic recursive function instantiates itself, and a
	// monomorphising backend needs that instantiation for the same reason it
	// needs a `ref`'s. Excluding it would have hidden every recursive case.
	count := func(d *Def, c *counts, where string, report func(string, ...any)) {
		var walk func(tm *Term)
		walk = func(tm *Term) {
			if tm == nil {
				return
			}
			poly := false
			switch tm.K {
			case "ref", "ctor":
				if callee, err := st.GetDef(tm.Hash); err == nil && callee.TyVars > 0 {
					poly = true
				}
			case "self":
				poly = d.TyVars > 0
			}
			if poly {
				c.sites++
				if len(tm.TyArgs) == 0 {
					report("%s: a polymorphic %q call site carries NO type arguments", where, tm.K)
				} else {
					c.withArgs++
					for i := range tm.TyArgs {
						if tyMentionsVar(&tm.TyArgs[i]) {
							c.throughTyVar++
							break
						}
					}
				}
			}
			walk(tm.A)
			walk(tm.B)
			walk(tm.C)
			for i := range tm.Args {
				walk(&tm.Args[i])
			}
			for i := range tm.Arms {
				walk(&tm.Arms[i])
			}
		}
		walk(d.Body)
	}

	rawGaps := 0
	for _, name := range names {
		h := byName[name]

		// RAW: the canonical bytes, decoded without checkDef.
		b, ok, err := st.be.getObject(h)
		if err != nil || !ok {
			t.Errorf("%s (%s): stored object unreadable (err=%v ok=%v)", name, shortHash(h), err, ok)
			continue
		}
		rd, err := decodeDef(b)
		if err != nil {
			t.Errorf("%s (%s): stored object will not decode: %v", name, shortHash(h), err)
			continue
		}
		if rd.K == "func" && rd.Body != nil {
			count(rd, &raw, name, func(string, ...any) { rawGaps++ })
		}

		// LOADED: what a backend actually sees.
		ld, err := st.GetDef(h)
		if err != nil {
			t.Errorf("%s (%s) will not load: %v", name, shortHash(h), err)
			continue
		}
		if ld.K == "func" && ld.Body != nil {
			count(ld, &loaded, name, func(f string, a ...any) { t.Errorf(f, a...) })
		}
	}

	if loaded.sites == 0 {
		t.Fatal("found no polymorphic call sites; the measurement is not reading anything")
	}
	t.Logf("polymorphic call sites  raw: %d sites, %d carry type arguments (%d omitted in the stored bytes)",
		raw.sites, raw.withArgs, rawGaps)
	t.Logf("polymorphic call sites  loaded: %d sites, %d carry type arguments, %d instantiate through the caller's own type variable",
		loaded.sites, loaded.withArgs, loaded.throughTyVar)
	if rawGaps > 0 {
		t.Logf("THE DELTA IS %d: that many instantiations exist only because the load path inferred them, "+
			"so monomorphisation depends on the checker running, not on the stored closure alone", rawGaps)
	}
}

// tyMentionsVar reports whether a type still contains a type VARIABLE — i.e. the
// instantiation is relative to the enclosing definition's own parameters rather
// than fully concrete. Local to the test: it is a property of the measurement,
// not of the kernel.
func tyMentionsVar(t *Ty) bool {
	if t == nil {
		return false
	}
	if t.K == "var" {
		return true
	}
	if tyMentionsVar(t.A) || tyMentionsVar(t.B) {
		return true
	}
	for i := range t.Args {
		if tyMentionsVar(&t.Args[i]) {
			return true
		}
	}
	return false
}
