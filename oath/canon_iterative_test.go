package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/big"
	"runtime"
	"strings"
	"testing"
)

// The canonical term encoder was made ITERATIVE for #149. This is
// identity-critical code: the claim is not that hashes still agree but that the
// ENCODING did not move, so the differential compares BYTES.
//
// A hash comparison would be a weaker statement resting on collision
// resistance. Bytes are the thing SPEC §1 defines and the thing a second kernel
// must reproduce.

// encTermRecursive is the encoder EXACTLY as it stood before the rewrite, kept
// as the oracle so "this refactor changed nothing" has a witness that can fail.
func encTermRecursive(e *enc, t *Term) {
	switch t.K {
	case "var":
		e.u8(tagTmVar)
		e.u32(uint32(t.Idx))
	case "int":
		e.u8(tagTmInt)
		e.bigint(t.Int)
	case "rat":
		e.u8(tagTmRat)
		e.bigint(t.Rat.Num())
		e.bigint(t.Rat.Denom())
	case "float":
		e.u8(tagTmFloat)
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(canonFloat(t.Float)))
		e.b = append(e.b, buf[:]...)
	case "bool":
		e.u8(tagTmBool)
		if t.Bool {
			e.u8(1)
		} else {
			e.u8(0)
		}
	case "lam":
		e.u8(tagTmLam)
		e.ty(t.Ty)
		encTermRecursive(e, t.A)
	case "app":
		e.u8(tagTmApp)
		encTermRecursive(e, t.A)
		encTermRecursive(e, t.B)
	case "let":
		e.u8(tagTmLet)
		e.ty(t.Ty)
		encTermRecursive(e, t.A)
		encTermRecursive(e, t.B)
	case "if":
		e.u8(tagTmIf)
		encTermRecursive(e, t.A)
		encTermRecursive(e, t.B)
		encTermRecursive(e, t.C)
	case "prim":
		e.u8(tagTmPrim)
		e.str(t.Op)
		e.u32(uint32(len(t.Args)))
		for i := range t.Args {
			encTermRecursive(e, &t.Args[i])
		}
	case "ref":
		e.u8(tagTmRef)
		e.hash(t.Hash)
		e.tys(t.TyArgs)
	case "self":
		e.u8(tagTmSelf)
		e.tys(t.TyArgs)
	case "ctor":
		e.u8(tagTmCtor)
		e.hash(t.Hash)
		e.u32(uint32(t.Idx))
		e.tys(t.TyArgs)
		e.u32(uint32(len(t.Args)))
		for i := range t.Args {
			encTermRecursive(e, &t.Args[i])
		}
	case "match":
		e.u8(tagTmMatch)
		e.hash(t.Hash)
		encTermRecursive(e, t.A)
		e.u32(uint32(len(t.Arms)))
		for i := range t.Arms {
			encTermRecursive(e, &t.Arms[i])
		}
	case "record":
		e.u8(tagTmRecord)
		e.u32(uint32(len(t.Names)))
		for i, n := range t.Names {
			e.str(n)
			encTermRecursive(e, &t.Args[i])
		}
	case "field":
		e.u8(tagTmField)
		encTermRecursive(e, t.A)
		e.str(t.Op)
	default:
		panic("encode: unknown Term kind " + t.K)
	}
}

func encodeBoth(t *Term) (iter, rec []byte) {
	a := &enc{}
	a.term(t)
	b := &enc{}
	encTermRecursive(b, t)
	return a.b, b.b
}

// TestEncoderBytesMatchOracleOnCorpus is the population that matters most: every
// term the kernel actually stores.
func TestEncoderBytesMatchOracleOnCorpus(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	seen, checked := map[string]bool{}, 0
	for name, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		terms := []*Term{}
		if d.Body != nil {
			terms = append(terms, d.Body)
		}
		for i := range d.Props {
			terms = append(terms, &d.Props[i].Body)
		}
		for _, tm := range terms {
			it, rc := encodeBoth(tm)
			if !bytes.Equal(it, rc) {
				t.Fatalf("%s: canonical bytes MOVED (%d vs %d bytes)", name, len(it), len(rc))
			}
			checked++
		}
	}
	if checked < 150 {
		t.Fatalf("only %d terms compared; the corpus did not load", checked)
	}
	t.Logf("iterative encoder is byte-identical on %d corpus terms", checked)
}

// TestEncoderBytesMatchOracleOnAdversarial covers the shapes the corpus does not
// contain — including the depths that motivated the rewrite.
func TestEncoderBytesMatchOracleOnAdversarial(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	sconsH, sconsI, _ := st.FindCtor("SCons")
	snilH, snilI, _ := st.FindCtor("SNil")
	consH, consI, _ := st.FindCtor("Cons")
	nilH, nilI, _ := st.FindCtor("Nil")
	someH, someI, _ := st.FindCtor("Some")

	ct := func(h string, idx int, tys []Ty, args ...*Term) *Term {
		c := &Term{K: "ctor", Hash: h, Idx: idx, TyArgs: tys}
		for _, a := range args {
			c.Args = append(c.Args, *a)
		}
		return c
	}
	strSpine := func(n int) *Term {
		acc := ct(snilH, snilI, nil)
		for k := 0; k < n; k++ {
			acc = ct(sconsH, sconsI, nil, &Term{K: "int", Int: big.NewInt(int64(97 + k%26))}, acc)
		}
		return acc
	}
	polySpine := func(n int) *Term {
		acc := ct(nilH, nilI, []Ty{*tInt()})
		for k := 0; k < n; k++ {
			acc = ct(consH, consI, nil, &Term{K: "int", Int: big.NewInt(int64(k))}, acc)
		}
		return acc
	}
	broadRecord := func(n int) *Term {
		r := &Term{K: "record"}
		for k := 0; k < n; k++ {
			r.Names = append(r.Names, string(rune('a'+k/26))+string(rune('a'+k%26)))
			r.Args = append(r.Args, Term{K: "int", Int: big.NewInt(int64(k))})
		}
		return r
	}

	// EVERY TERM VARIANT, so no case of the switch is left uncompared.
	variants := map[string]*Term{
		"var":    {K: "var", Idx: 3},
		"int":    {K: "int", Int: big.NewInt(-99)},
		"rat":    {K: "rat", Rat: big.NewRat(-3, 7)},
		"float":  {K: "float", Float: 1.5},
		"nan":    {K: "float", Float: math.NaN()},
		"bool":   {K: "bool", Bool: true},
		"lam":    {K: "lam", Ty: tInt(), A: &Term{K: "var"}},
		"app":    {K: "app", A: &Term{K: "var"}, B: &Term{K: "int", Int: big.NewInt(1)}},
		"let":    {K: "let", Ty: tInt(), A: &Term{K: "int", Int: big.NewInt(1)}, B: &Term{K: "var"}},
		"if":     {K: "if", A: &Term{K: "bool"}, B: &Term{K: "int", Int: big.NewInt(1)}, C: &Term{K: "int", Int: big.NewInt(2)}},
		"prim":   {K: "prim", Op: "+", Args: []Term{{K: "int", Int: big.NewInt(1)}, {K: "int", Int: big.NewInt(2)}}},
		"prim0":  {K: "prim", Op: "+"},
		"ref":    {K: "ref", Hash: consH, TyArgs: []Ty{*tInt()}},
		"self":   {K: "self", TyArgs: []Ty{*tBool()}},
		"ctor":   ct(someH, someI, []Ty{*tInt()}, &Term{K: "int", Int: big.NewInt(1)}),
		"ctor0":  ct(nilH, nilI, []Ty{*tInt()}),
		"match":  {K: "match", Hash: consH, A: &Term{K: "var"}, Arms: []Term{{K: "int", Int: big.NewInt(1)}, {K: "int", Int: big.NewInt(2)}}},
		"match0": {K: "match", Hash: consH, A: &Term{K: "var"}},
		"record": {K: "record", Names: []string{"a", "b"}, Args: []Term{{K: "int", Int: big.NewInt(1)}, {K: "bool"}}},
		"rec0":   {K: "record"},
		"field":  {K: "field", Op: "x", A: &Term{K: "record", Names: []string{"x"}, Args: []Term{{K: "int", Int: big.NewInt(1)}}}},
		// the shapes that motivated the rewrite
		"str-spine-5000":  strSpine(5000),
		"poly-spine-2000": polySpine(2000),
		"broad-record":    broadRecord(400),
		"nested-mixed":    {K: "if", A: &Term{K: "bool"}, B: strSpine(64), C: polySpine(64)},
	}
	for name, tm := range variants {
		it, rc := encodeBoth(tm)
		if !bytes.Equal(it, rc) {
			t.Errorf("%s: canonical bytes MOVED\n  iterative: %d bytes\n  recursive: %d bytes", name, len(it), len(rc))
			continue
		}
		if len(it) == 0 {
			t.Errorf("%s: encoded to nothing", name)
		}
	}
	t.Logf("iterative encoder is byte-identical on %d adversarial shapes", len(variants))
}

// TestDecoderRoundTripsEveryCorpusObject is the differential for the iterative
// DECODER, and it needs no oracle copy: for every stored object, decoding and
// re-encoding must reproduce the original bytes EXACTLY.
//
// That is a stronger statement than "agrees with the old decoder". It says the
// decoder is a faithful inverse of the encoder over the real corpus, which is
// the property identity depends on — a decoder that agreed with a buggy oracle
// would pass an oracle differential and fail this.
func TestDecoderRoundTripsEveryCorpusObject(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	seen, checked := map[string]bool{}, 0
	for name, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		original, ok, err := st.be.getObject(h)
		if err != nil || !ok {
			continue
		}
		d, err := decodeDef(original)
		if err != nil {
			t.Errorf("%s: decode failed: %v", name, err)
			continue
		}
		if got := encodeDef(d); !bytes.Equal(got, original) {
			t.Errorf("%s: round trip changed the bytes (%d -> %d)", name, len(original), len(got))
			continue
		}
		// And the hash is its own name, which is the identity claim itself.
		if hashDef(d) != h {
			t.Errorf("%s: decoded object no longer hashes to its own name", name)
		}
		checked++
	}
	if checked < 150 {
		t.Fatalf("only %d objects round-tripped; the corpus did not load", checked)
	}
	t.Logf("decoder round-trips %d corpus objects byte-for-byte", checked)
}

// TestDecoderRefusesOversizedDuringConstruction is the PRE-ADMISSION claim.
//
// The old decoder could not be the oracle here: surviving a deep hostile object
// is the defect, so the authority is the encoding format plus the portable
// profile's refusal contract. What must hold is that refusal happens WHILE
// decoding — the structure never fully exists — and that the failure is the
// typed resource error rather than a fault or a malformed-input diagnostic.
func TestDecoderRefusesOversizedDuringConstruction(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	sconsH, sconsI, ok := st.FindCtor("SCons")
	if !ok {
		t.Fatal("corpus has no SCons")
	}
	snilH, snilI, _ := st.FindCtor("SNil")

	// Built DIRECTLY, bypassing admission — this is the object an attacker
	// writes into a store, which never passed the gate.
	spine := func(n int) *Term {
		acc := &Term{K: "ctor", Hash: snilH, Idx: snilI}
		for k := 0; k < n; k++ {
			acc = &Term{K: "ctor", Hash: sconsH, Idx: sconsI,
				Args: []Term{{K: "int", Int: big.NewInt(97)}, *acc}}
		}
		return acc
	}
	strTy := &Ty{K: "data", Hash: snilH}

	// AT the limit: admitted and round-trips.
	small := &Def{K: "func", Ty: strTy, Body: spine(1000)}
	if _, err := decodeDef(encodeDef(small)); err != nil {
		t.Fatalf("a small object must decode, got %v", err)
	}

	// FAR over: refused, as a RESOURCE limit and not as malformed input.
	huge := &Def{K: "func", Ty: strTy, Body: spine(maxCanonicalNodes)}
	encoded := encodeDef(huge)
	_, err = decodeDef(encoded)
	if err == nil {
		t.Fatal("an oversized object must be refused")
	}
	var rl *resourceLimitErr
	if !errors.As(err, &rl) {
		t.Fatalf("refusal must be a typed resource limit, got %T: %v", err, err)
	}
	for _, wrong := range []string{"unexpected end", "unknown", "malformed", "non-canonical"} {
		if strings.Contains(strings.ToLower(err.Error()), wrong) {
			t.Errorf("resource refusal reads as malformed input (%q): %v", wrong, err)
		}
	}

	// REFUSAL IS DURING CONSTRUCTION, not after — proven by COST: a decoder that
	// built the whole structure and then checked would consume every byte.
	//
	// Measured through decodeDefRaw's own driver rather than by calling d.term()
	// on a DEF's bytes. The first version of this assertion did the latter: the
	// def header's first byte was read as a term tag, the decoder failed with
	// "unknown tag" after ONE byte, and the assertion passed for a reason that
	// had nothing to do with the budget. It would have passed identically with
	// the counter removed.
	instrumented := &dec{b: encoded}
	_, derr := instrumented.def()
	if derr == nil {
		t.Fatal("the instrumented decode must also refuse")
	}
	var rl2 *resourceLimitErr
	if !errors.As(derr, &rl2) {
		t.Fatalf("the instrumented decode failed for the wrong reason: %v", derr)
	}
	if instrumented.pos >= len(encoded) {
		t.Errorf("the decoder consumed all %d bytes before refusing; refusal must "+
			"happen while constructing, not after", len(encoded))
	}
	t.Logf("refused as a resource limit after %d of %d bytes (%.1f%%)",
		instrumented.pos, len(encoded), 100*float64(instrumented.pos)/float64(len(encoded)))
}

// TestDecoderStaysAliveAfterOversized: an oversized object must not damage the
// decoder for subsequent, valid ones. A kernel that refuses once and then
// misbehaves has moved the failure rather than removed it.
func TestDecoderStaysAliveAfterOversized(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	sconsH, sconsI, _ := st.FindCtor("SCons")
	snilH, snilI, _ := st.FindCtor("SNil")
	spine := func(n int) *Term {
		acc := &Term{K: "ctor", Hash: snilH, Idx: snilI}
		for k := 0; k < n; k++ {
			acc = &Term{K: "ctor", Hash: sconsH, Idx: sconsI,
				Args: []Term{{K: "int", Int: big.NewInt(97)}, *acc}}
		}
		return acc
	}
	strTy := &Ty{K: "data", Hash: snilH}
	good := encodeDef(&Def{K: "func", Ty: strTy, Body: spine(10)})
	bad := encodeDef(&Def{K: "func", Ty: strTy, Body: spine(maxCanonicalNodes)})

	for i := 0; i < 5; i++ {
		if _, err := decodeDef(bad); err == nil {
			t.Fatalf("round %d: oversized object was accepted", i)
		}
		d, err := decodeDef(good)
		if err != nil {
			t.Fatalf("round %d: a valid object failed after a refusal: %v", i, err)
		}
		if !bytes.Equal(encodeDef(d), good) {
			t.Fatalf("round %d: a valid object round-tripped differently after a refusal", i)
		}
	}
}

// TestDecoderRefusesHugeDeclaredLengthBeforeAllocating witnesses the EAGER half
// of the counter: a length is charged when it is READ, not as its elements are
// decoded.
//
// Without that, a header claiming millions of arguments allocates
// make([]Term, n) before a single node is counted — u32 caps a length at 1<<24,
// and a Term is well over a hundred bytes, so one crafted header reserves
// gigabytes. Per-node counting alone still refuses eventually, which is why the
// mutant that removes eager charging survived the during-construction test and
// needed this one.
func TestDecoderRefusesHugeDeclaredLengthBeforeAllocating(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	consH, consI, ok := st.FindCtor("Cons")
	if !ok {
		t.Fatal("corpus has no Cons")
	}

	// A constructor header declaring an implausible argument count, with no
	// argument bytes following it at all.
	e := &enc{}
	e.u8(tagTmCtor)
	e.hash(consH)
	e.u32(uint32(consI))
	e.tys(nil)
	e.u32(uint32(1 << 23)) // 8,388,608 arguments, ~128x the profile
	crafted := e.b

	before := runtime.MemStats{}
	runtime.ReadMemStats(&before)

	d := &dec{b: crafted}
	_, err = d.term()
	if err == nil {
		t.Fatal("a header declaring 8M arguments must be refused")
	}
	var rl *resourceLimitErr
	if !errors.As(err, &rl) {
		t.Fatalf("must be refused as a RESOURCE limit before allocation, got %v", err)
	}

	after := runtime.MemStats{}
	runtime.ReadMemStats(&after)
	// A make([]Term, 8388608) is hundreds of megabytes. Refusing before it
	// should cost essentially nothing; 64MB is a generous ceiling that still
	// separates the two outcomes by an order of magnitude.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Errorf("refusing a huge declared length allocated %d MB — the length must be "+
			"charged when READ, before the slice is reserved", grew>>20)
	}
	// The whole header is 44 bytes; consuming it all and stopping is correct.
	t.Logf("refused after %d bytes with no element decoded", d.pos)
}
