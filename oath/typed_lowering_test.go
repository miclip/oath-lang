package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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
//	raw     — what the canonical stored bytes carry, via decodeDef, no checkDef
//	loaded  — what a backend sees, after the store's load path has inferred
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

// A Str's elements are CODEPOINTS, and any typed lowering of `match` on Str
// must produce codepoints — not the bytes of some encoding.
//
// This is pinned before the LLVM lowering exists, because the trap is already
// laid: both backends PRINT a Str as UTF-8, so `(SCons 233 (SCons 65 SNil))`
// writes `c3 a9 41` from either and they agree at the boundary. The LLVM
// runtime stores a Str as exactly those packed bytes with a BYTE length, so a
// lowering that indexed `s[0]` would report 195 where the reference reports
// 233 — and the printed output would still match, so the existing three-way
// gate would not notice.
//
// IT COMPARES A COMPILED PROGRAM AGAINST THE REFERENCE, not the reference
// against itself. An earlier version called `apiEval` alone, which exercises
// only the structural evaluator — it would have passed for any byte-indexing
// backend, which is precisely the regression it exists to catch. The LLVM
// backend still REFUSES match on Str, so today the compiled side is the Go
// backend; when LLVM gains the lowering it joins here and the constraint is
// already written down.
//
// The length probe COUNTS RECURSIVELY. An earlier version matched two
// constructors deep and returned 2 without checking the tail was empty, so a
// three-byte implementation produced 2 as well and the probe could not tell
// them apart.
func TestStrElementsAreCodepointsNotEncodedBytes(t *testing.T) {
	st := llvmStore(t)
	// EXACTLY two codepoints, by structure rather than by counting — arithmetic
	// is still refused by the LLVM backend, and this is exact where a
	// two-deep match that ignored the final tail was not: a three-BYTE view
	// has a third element and fails here.
	put(t, st, `(defn is-two [] [(s Str)] Bool
		(match s
			((SNil) false)
			((SCons a r1)
				(match r1
					((SNil) false)
					((SCons b r2) (match r2 ((SNil) true) ((SCons c r3) false)))))))`)
	put(t, st, `(defn cp-head [] [(s Str)] Int
		(match s ((SNil) -1) ((SCons c rest) c)))`)
	// 233 is U+00E9, TWO bytes in UTF-8; 65 is 'A', one byte. A byte-indexing
	// implementation reports head 195 and length 3.
	// The entry returns a LITERAL selected by the checks, because building a Str
	// from computed parts is still refused by the LLVM backend and would make
	// this skip for an unrelated reason.
	put(t, st, `(defn cp-probe [] [(args (List Str))] Str
		(if (== (cp-head (SCons 233 (SCons 65 (SNil)))) 233)
		    (if (is-two (SCons 233 (SCons 65 (SNil)))) "head=233 len=2" "len-WRONG")
		    "head-WRONG"))`)
	markVerified(t, st, "cp-probe")

	const want = "head=233 len=2"
	ref, err := apiEval(st, `(cp-probe (Nil [Str]))`)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	if !strings.Contains(ref, "104") { // 'h' — the reference really took the good branch
		t.Fatalf("the REFERENCE produced %s, which is not %q; the probe is not measuring what it claims", ref, want)
	}

	for _, be := range []struct {
		name  string
		build func() (string, error)
	}{
		{"go-emit/2", func() (string, error) { b, _ := buildProgram(t, st, "cp-probe"); return b, nil }},
		{"llvm-ir/1", func() (string, error) {
			prog, err := planProgram(st, "cp-probe")
			if err != nil {
				return "", err
			}
			// Key on the emitter's REASON, not on a substring of the help text —
			// that text lists unsupported features, so `strings.Contains(err,
			// "match on Str")` matched it even after match on Str was
			// implemented, and this skipped while claiming to be blocked.
			if _, err := emitLLVM(st, prog); err != nil {
				return "", err
			}
			requireClang(t)
			return buildLLVM(t, st, "cp-probe"), nil
		}},
	} {
		bin, err := be.build()
		if err != nil {
			t.Errorf("%s refused cp-probe: %v", be.name, err)
			continue
		}
		out, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("%s: run: %v", be.name, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != want {
			t.Errorf("%s printed %q, reference expects %q — a Str element is a codepoint, not an encoded byte",
				be.name, got, want)
		}
	}
}

// AN INT THE BACKEND CANNOT REPRESENT IS REFUSED, NEVER WRAPPED.
//
// Oath's Int is ℤ — unbounded, SPEC §1, and the `int` term carries a big.Int.
// The LLVM runtime stores int64, so it implements a SUBSET, and the whole
// question is what a subset does at its boundary. Two's-complement wraparound
// would make the backend disagree with `oath eval` on a value the language
// considers ordinary, and the three-way gate would only catch it if some test
// happened to reach that magnitude.
//
// The CONTROL matters as much as the refusal: an in-range literal must still
// lower, or "refuses out-of-range" would be satisfied by refusing everything.
func TestOutOfRangeIntIsRefusedNotWrapped(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn big-lit [] [(args (List Str))] Str (if (== 99999999999999999999999999 0) "z" "n"))`)
	markVerified(t, st, "big-lit")
	prog, err := planProgram(st, "big-lit")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err = emitLLVM(st, prog)
	if err == nil {
		t.Fatal("lowered an Int literal outside int64 — it must be refused, because wrapping it would " +
			"silently disagree with the reference on an ordinary Oath value")
	}
	if !strings.Contains(err.Error(), "99999999999999999999999999") {
		t.Errorf("refused but did not name the literal: %v", err)
	}

	// CONTROL: in range still lowers, so the refusal is a boundary and not a wall.
	put(t, st, `(defn small-lit [] [(args (List Str))] Str (if (== 42 42) "y" "n"))`)
	markVerified(t, st, "small-lit")
	ok, err := planProgram(st, "small-lit")
	if err != nil {
		t.Fatalf("plan control: %v", err)
	}
	if _, err := emitLLVM(st, ok); err != nil {
		t.Errorf("refused an in-range Int literal: %v", err)
	}
}

// PACKING A Str ELEMENT IS INJECTIVE OR IT IS REFUSED — never substituted.
//
// This is a BACKEND SUBSET BOUNDARY, not a claim that the value is illegal
// Oath. #133 asks the normative question — is Str defined only over Unicode
// scalar values, or can it hold arbitrary integers under some non-UTF-8
// canonical representation — and it is open. The interpreter remains the
// semantic reference and is ALLOWED to disagree here, because the backend is
// explicitly refusing an unsupported case rather than returning a wrong value.
//
// What was there before: `string(rune(n))`, which yields U+FFFD for a negative,
// a surrogate, or anything above 0x10FFFF. So `SCons -1 SNil`, `SCons 55296
// SNil` and `SCons 1114112 SNil` all printed `ef bf bd` — three distinct values,
// one output, no constructor inverse. BOTH native backends did it, so they
// agreed with each other and disagreed with the reference, and the three-way
// gate stayed green throughout. Two identically wrong lowerings agree.
//
// Note these are INT-TO-Str CONSTRUCTION cases, not malformed UTF-8 input: no
// malformed byte sequence exists yet, and the defect is in choosing how to
// encode a mathematical Int.
func TestStrElementIsPackedInjectivelyOrRefused(t *testing.T) {
	st := llvmStore(t)

	// Both directions. The accepted cases are the control: without them,
	// "refuses non-scalars" would be satisfied by refusing everything.
	for _, ok := range []struct {
		name string
		cp   string
		want string
	}{
		{"sc-ascii", "65", "A"},             // 41
		{"sc-latin", "195", "Ã"},            // c3 83 — U+00C3, NOT the byte 0xC3
		{"sc-max", "1114111", "\U0010ffff"}, // the largest scalar, 0x10FFFF
	} {
		put(t, st, `(defn `+ok.name+` [] [(args (List Str))] Str (SCons `+ok.cp+` (SNil)))`)
		markVerified(t, st, ok.name)
		bin, _ := buildProgram(t, st, ok.name)
		out, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("%s: run: %v", ok.name, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != ok.want {
			t.Errorf("%s packed % x, want % x", ok.name, got, ok.want)
		}
		prog, err := planProgram(st, ok.name)
		if err != nil {
			t.Fatalf("%s: plan: %v", ok.name, err)
		}
		if _, err := emitLLVM(st, prog); err != nil {
			t.Errorf("llvm-ir/1 refused the encodable Str element %s: %v", ok.cp, err)
		}
	}

	// Each non-scalar CLASS refused, and refused BY NAME — so three refused
	// inputs produce three distinguishable messages and cannot collapse to one
	// output the way they previously collapsed to one string.
	seen := map[string]string{}
	for _, bad := range []struct{ name, cp, why string }{
		{"sc-neg", "-1", "negative"},
		{"sc-surrogate-lo", "55296", "0xD800"},
		{"sc-surrogate-hi", "57343", "0xDFFF"},
		{"sc-above-max", "1114112", "0x110000"},
	} {
		put(t, st, `(defn `+bad.name+` [] [(args (List Str))] Str (SCons `+bad.cp+` (SNil)))`)
		markVerified(t, st, bad.name)
		prog, err := planProgram(st, bad.name)
		if err != nil {
			t.Fatalf("%s: plan: %v", bad.name, err)
		}
		_, err = emitLLVM(st, prog)
		if err == nil {
			t.Errorf("llvm-ir/1 lowered the non-scalar Str element %s (%s) — it must refuse, "+
				"because substituting U+FFFD makes distinct Str values identical", bad.cp, bad.why)
			continue
		}
		if !strings.Contains(err.Error(), bad.cp) {
			t.Errorf("%s: refused without naming the element: %v", bad.name, err)
		}
		if prev, dup := seen[err.Error()]; dup {
			t.Errorf("%s and %s produce the SAME refusal message — the whole point is that "+
				"distinct inputs stay distinguishable", prev, bad.name)
		}
		seen[err.Error()] = bad.name

		// The Go backend encodes at RUNTIME, so its refusal is there. It must
		// still be a refusal and not a substitution.
		gbin, _ := buildProgram(t, st, bad.name)
		out, _ := exec.Command(gbin).CombinedOutput()
		if strings.Contains(string(out), "�") {
			t.Errorf("%s: go-emit/2 substituted U+FFFD instead of refusing", bad.name)
		}
		if !strings.Contains(string(out), "cannot encode Str element") {
			t.Errorf("%s: go-emit/2 did not refuse; output was %q", bad.name, string(out))
		}
	}
}

// MATCHING A Str OBSERVES CODEPOINTS AND PRESERVES THE PACKED REMAINDER.
//
// The tail must remain canonical packed UTF-8 for what is left, not a view that
// begins mid-sequence. So each case walks the string one scalar at a time and
// checks the whole sequence, which is what makes "the remainder is exact" a
// claim rather than a hope: a decoder that advanced by the wrong number of
// bytes would put the next match at a continuation byte and be caught here.
func TestStrMatchWalksScalarsOfEveryWidth(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn s-head [] [(s Str) (d Int)] Int (match s ((SNil) d) ((SCons c r) c)))`)
	put(t, st, `(defn s-tail [] [(s Str)] Str (match s ((SNil) (SNil)) ((SCons c r) r)))`)
	put(t, st, `(defn s-empty [] [(s Str)] Bool (match s ((SNil) true) ((SCons c r) false)))`)

	for _, tc := range []struct {
		name string
		lit  string // an Oath Str literal
		want string // "ok" when every scalar and the final emptiness match
	}{
		// one, two, three and four byte scalars, each followed by ASCII so the
		// remainder after the wide scalar is checked too.
		{"w1", `(SCons 65 (SCons 66 (SNil)))`, "ok"},     // A B
		{"w2", `(SCons 233 (SCons 66 (SNil)))`, "ok"},    // é B   c3 a9 42
		{"w3", `(SCons 8364 (SCons 66 (SNil)))`, "ok"},   // € B   e2 82 ac 42
		{"w4", `(SCons 128512 (SCons 66 (SNil)))`, "ok"}, // 😀 B  f0 9f 98 80 42
		{"empty", `(SNil)`, "ok"},
	} {
		var body string
		if tc.name == "empty" {
			body = `(if (s-empty ` + tc.lit + `) "ok" "WRONG")`
		} else {
			first := "0"
			switch tc.name {
			case "w1":
				first = "65"
			case "w2":
				first = "233"
			case "w3":
				first = "8364"
			case "w4":
				first = "128512"
			}
			// head is the scalar; the tail's head is 66; the tail's tail is empty.
			body = `(if (== (s-head ` + tc.lit + ` 0) ` + first + `)
				  (if (== (s-head (s-tail ` + tc.lit + `) 0) 66)
				      (if (s-empty (s-tail (s-tail ` + tc.lit + `))) "ok" "REMAINDER-WRONG")
				      "SECOND-WRONG")
				  "FIRST-WRONG")`
		}
		name := "walk-" + tc.name
		put(t, st, `(defn `+name+` [] [(args (List Str))] Str `+body+`)`)
		markVerified(t, st, name)

		gbin, _ := buildProgram(t, st, name)
		gout, err := exec.Command(gbin).Output()
		if err != nil {
			t.Fatalf("%s: go run: %v", name, err)
		}
		if got := strings.TrimRight(string(gout), "\n"); got != tc.want {
			t.Errorf("%s go-emit/2 = %q, want %q", name, got, tc.want)
		}
		lbin := buildLLVM(t, st, name)
		lout, err := exec.Command(lbin).Output()
		if err != nil {
			t.Fatalf("%s: llvm run: %v", name, err)
		}
		if got := strings.TrimRight(string(lout), "\n"); got != tc.want {
			t.Errorf("%s llvm-ir/1 = %q, want %q — the tail must be canonical packed UTF-8 "+
				"for what remains, not a view starting mid-sequence", name, got, tc.want)
		}
	}
}

// MALFORMED PACKED STORAGE FAILS EXPLICITLY — it is not decoded by locale, not
// replaced, and not exposed as pseudo-codepoints.
//
// It is reachable: a capability value arrives from getenv and is NOT validated
// on the way in (#133 covers boundary validation). So the decoder meets bytes
// the packing side would never have produced, and guessing would be exactly the
// substitution this backend just stopped doing.
func TestMalformedPackedStrFailsExplicitly(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn s-head2 [] [(s Str) (d Int)] Int (match s ((SNil) d) ((SCons c r) c)))`)
	put(t, st, `(defn read-head [] [(w {env (-> Str Str)}) (args (List Str))] Str
		(if (== (s-head2 ((. w env) "OATH_MALFORMED_TEST") 0) 65) "A" "other"))`)
	markVerified(t, st, "read-head")
	bin := buildLLVM(t, st, "read-head")

	// A lone continuation byte: never produced by the packing side, and not
	// valid UTF-8 from any source.
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_MALFORMED_TEST=\x80")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("decoded malformed packed storage and printed %q instead of refusing", out)
	}
	if !strings.Contains(string(out), "not valid UTF-8") {
		t.Errorf("failed without saying why: %q", out)
	}
	if strings.Contains(string(out), "�") {
		t.Errorf("substituted U+FFFD instead of refusing: %q", out)
	}

	// CONTROL: valid input still decodes, so the check is a boundary and not a wall.
	cmd = exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_MALFORMED_TEST=AB")
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "A" {
		t.Errorf("control printed %q, want %q", got, "A")
	}
}
