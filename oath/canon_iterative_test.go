package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
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

// TestDecoderAcceptsWideStructuresAtTheProfileBoundary. The decoder must accept
// exactly what admitDef accepts — no more and NO LESS.
//
// An earlier version charged a collection's length AND then charged each child
// as it decoded, billing wide structures twice. A prim with 32,768 leaf
// arguments is 32,769 nodes, comfortably inside the profile, and was refused.
// That is not a missed refusal but a narrowed language: a valid stored object
// became undecodable, so admission and decoding disagreed about the domain.
//
// Found by external review. Every case below is WIDE rather than deep, because
// depth was the shape already covered and width is where the double charge fell.
func TestDecoderAcceptsWideStructuresAtTheProfileBoundary(t *testing.T) {
	wide := func(n int) *Term {
		p := &Term{K: "prim", Op: "+"}
		for i := 0; i < n; i++ {
			p.Args = append(p.Args, Term{K: "int", Int: big.NewInt(int64(i))})
		}
		return p
	}
	for _, n := range []int{1, 100, 32768, maxCanonicalNodes - 2} {
		term := wide(n)
		nodes, ok := countCanonicalNodes(&Def{K: "func", Body: term}, maxCanonicalNodes)
		if !ok {
			t.Fatalf("setup: a %d-argument prim is not inside the profile", n)
		}
		e := &enc{}
		e.term(term)
		d := &dec{b: e.b}
		got, err := d.term()
		if err != nil {
			t.Errorf("a %d-argument prim is %d nodes and inside the profile, but decoding "+
				"refused it: %v", n, nodes, err)
			continue
		}
		if len(got.Args) != n {
			t.Errorf("%d arguments decoded as %d", n, len(got.Args))
		}
		// Decoding must charge exactly one node per node — the same number the
		// admission counter reaches.
		if d.nodes != nodes {
			t.Errorf("decoder counted %d nodes for a structure admitDef counts as %d",
				d.nodes, nodes)
		}
	}
}

// TestTypeDecoderIsIterativeAndExact covers dec.ty, the LAST pre-admission
// recursion, with the same two independent obligations dec.term needed —
// neither of which the other can stand in for:
//
//	EXACT DOMAIN PARITY      decoding accepts precisely what admitDef accepts
//	REFUSAL BEFORE ALLOCATION a declared length is rejected before make()
//
// Hostile depth here needs genuinely nested BYTES: a type carries no
// linear-spine sugar, which is why this exposure was narrower than dec.term's
// and repaired second.
func TestTypeDecoderIsIterativeAndExact(t *testing.T) {
	// DEPTH: a nested arrow far past any host stack must decode, not fault.
	nest := func(n int) *Ty {
		t := tInt()
		for i := 0; i < n; i++ {
			t = tFun(tInt(), t)
		}
		return t
	}
	for _, n := range []int{1, 100, 10000} {
		e := &enc{}
		e.ty(nest(n))
		d := &dec{b: e.b}
		got, err := d.ty()
		if err != nil {
			t.Fatalf("a %d-deep function type must decode, got %v", n, err)
		}
		// Re-encoding must reproduce the bytes exactly.
		e2 := &enc{}
		e2.ty(got)
		if !bytes.Equal(e2.b, e.b) {
			t.Fatalf("a %d-deep type did not round-trip", n)
		}
	}

	// WIDTH, and PARITY with admission: the same structure must count the same.
	wide := func(n int) *Ty {
		args := make([]Ty, n)
		for i := range args {
			args[i] = *tInt()
		}
		return &Ty{K: "rec", Args: args}
	}
	for _, n := range []int{1, 100, 32768} {
		ty := wide(n)
		nodes, ok := countCanonicalNodes(&Def{K: "func", Ty: ty}, maxCanonicalNodes)
		if !ok {
			t.Fatalf("setup: a %d-argument rec type is not inside the profile", n)
		}
		e := &enc{}
		e.ty(ty)
		d := &dec{b: e.b}
		if _, err := d.ty(); err != nil {
			t.Errorf("a %d-argument rec type is %d nodes and inside the profile, but "+
				"decoding refused it: %v", n, nodes, err)
			continue
		}
		if d.nodes != nodes {
			t.Errorf("type decoder counted %d nodes for a structure admitDef counts as %d",
				d.nodes, nodes)
		}
	}

	// REFUSAL BEFORE ALLOCATION: a declared length far past the profile, with
	// no element bytes following it.
	e := &enc{}
	e.u8(tagTyRec)
	e.u32(uint32(1 << 23))
	d := &dec{b: e.b}
	_, err := d.ty()
	var rl *resourceLimitErr
	if !errors.As(err, &rl) {
		t.Fatalf("a type header declaring 8M arguments must be refused as a resource "+
			"limit before allocation, got %v", err)
	}

	// OVERSIZED DEPTH is refused as a resource limit, not as a fault.
	deep := &enc{}
	deep.ty(nest(maxCanonicalNodes))
	dd := &dec{b: deep.b}
	if _, err := dd.ty(); !errors.As(err, &rl) {
		t.Fatalf("an oversized type must be refused as a resource limit, got %v", err)
	}
	if dd.pos >= len(deep.b) {
		t.Error("the type decoder consumed every byte before refusing")
	}
}

// encTyRecursive is the type encoder EXACTLY as it stood before the iterative
// rewrite, kept as the oracle. enc.ty is identity-critical for the same reason
// enc.term is, so the differential compares BYTES.
func encTyRecursive(e *enc, t *Ty) {
	switch t.K {
	case "int":
		e.u8(tagTyInt)
	case "bool":
		e.u8(tagTyBool)
	case "rat":
		e.u8(tagTyRat)
	case "float":
		e.u8(tagTyFloat)
	case "var":
		e.u8(tagTyVar)
		e.u32(uint32(t.Var))
	case "fun":
		e.u8(tagTyFun)
		encTyRecursive(e, t.A)
		encTyRecursive(e, t.B)
	case "data":
		e.u8(tagTyData)
		e.hash(t.Hash)
		e.u32(uint32(len(t.Args)))
		for i := range t.Args {
			encTyRecursive(e, &t.Args[i])
		}
	case "rec":
		e.u8(tagTyRec)
		e.u32(uint32(len(t.Args)))
		for i := range t.Args {
			encTyRecursive(e, &t.Args[i])
		}
	case "record":
		e.u8(tagTyRecord)
		e.u32(uint32(len(t.Names)))
		for i, n := range t.Names {
			e.str(n)
			encTyRecursive(e, &t.Args[i])
		}
	default:
		panic("encode: unknown Ty kind " + t.K)
	}
}

// TestTypeEncoderBytesMatchOracle covers enc.ty over the corpus and over the
// shapes that motivated the rewrite.
//
// The claim that made this look unnecessary was WRONG and self-caught: enc.ty
// was labelled BOUNDED on the grounds that Ty depth is capped at
// maxSyntaxNesting — but that cap belongs to the READER, and a DECODED def never
// passed it. hashDef runs on every stored object including bundle imports, so a
// structure the profile ADMITS overflowed while being hashed. Reproduced under a
// 1MB stack limit before the fix.
func TestTypeEncoderBytesMatchOracle(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	compare := func(name string, ty *Ty) {
		t.Helper()
		a, b := &enc{}, &enc{}
		a.ty(ty)
		encTyRecursive(b, ty)
		if !bytes.Equal(a.b, b.b) {
			t.Errorf("%s: type bytes MOVED (%d vs %d)", name, len(a.b), len(b.b))
		}
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
		if d.Ty != nil {
			compare(name, d.Ty)
			checked++
		}
		for ci, ctor := range d.Ctors {
			for i := range ctor {
				compare(fmt.Sprintf("%s ctor %d field %d", name, ci, i), &ctor[i])
				checked++
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d types compared; the corpus did not load", checked)
	}

	realHash, _, _ := st.FindCtor("Cons")
	nest := func(n int) *Ty {
		x := tInt()
		for i := 0; i < n; i++ {
			x = tFun(tInt(), x)
		}
		return x
	}
	wideRec := &Ty{K: "record"}
	for i := 0; i < 300; i++ {
		wideRec.Names = append(wideRec.Names, fmt.Sprintf("f%03d", i))
		wideRec.Args = append(wideRec.Args, *tInt())
	}
	for name, ty := range map[string]*Ty{
		"int": tInt(), "bool": tBool(), "rat": tRat(), "float": tFloat(),
		"var": tVar(3), "fun": tFun(tInt(), tBool()),
		"data":       {K: "data", Hash: realHash, Args: []Ty{*tInt()}},
		"rec":        {K: "rec", Args: []Ty{*tInt(), *tBool()}},
		"record":     wideRec,
		"empty-rec":  {K: "rec"},
		"nest-1000":  nest(1000),
		"nest-10000": nest(10000),
	} {
		compare(name, ty)
	}
	t.Logf("iterative type encoder is byte-identical on %d corpus types and 12 shapes", checked)
}

// TestDecoderRefusesNestedWideCollections is the P1 external review found.
//
// reserveNodes originally CHECKED a declared length without charging it, so the
// reservation was invisible to the next check. Crafted bytes could nest a `rec`
// as the first child of progressively narrower `rec`s: every check passed
// because only decoded nodes counted, while each level allocated another
// near-limit slice. Quadratic memory from a few bytes of headers, with no node
// limit ever reached.
//
// The repair is provenance: the length is charged UP FRONT and its children are
// decoded as prepaid tasks that do not charge again — exact parity with
// admitDef, and a reservation that survives nesting.
func TestDecoderRefusesNestedWideCollections(t *testing.T) {
	// Each level: a `rec` header declaring n args, whose FIRST arg is the next
	// level. Tiny input, enormous implied allocation.
	e := &enc{}
	const levels = 40
	for i := 0; i < levels; i++ {
		e.u8(tagTyRec)
		e.u32(uint32(60000 - i))
	}
	e.u8(tagTyInt)
	crafted := e.b
	if len(crafted) > 500 {
		t.Fatalf("setup: the crafted input should be tiny, got %d bytes", len(crafted))
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	d := &dec{b: crafted}
	_, err := d.ty()
	if err == nil {
		t.Fatalf("%d nested rec headers declaring ~60,000 args each must be refused", levels)
	}
	var rl *resourceLimitErr
	if !errors.As(err, &rl) {
		t.Fatalf("must be refused as a RESOURCE limit, got %v", err)
	}

	runtime.ReadMemStats(&after)
	// Two levels at 60,000 Ty each would already be tens of megabytes; forty
	// would be gigabytes. 64MB separates "refused after one or two slices" from
	// "allocated its way down the nest".
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Errorf("refusing %d nested wide collections allocated %d MB from %d bytes of "+
			"input — the reservation must persist across nesting", levels, grew>>20, len(crafted))
	}

	// The same shape in TERMS: nested ctor headers.
	st, err2 := OpenStore("../codebase")
	if err2 != nil {
		t.Fatalf("could not open the corpus: %v", err2)
	}
	consH, consI, _ := st.FindCtor("Cons")
	e2 := &enc{}
	for i := 0; i < levels; i++ {
		e2.u8(tagTmCtor)
		e2.hash(consH)
		e2.u32(uint32(consI))
		e2.tys(nil)
		e2.u32(uint32(60000 - i))
	}
	runtime.GC()
	runtime.ReadMemStats(&before)
	d2 := &dec{b: e2.b}
	if _, err := d2.term(); !errors.As(err, &rl) {
		t.Fatalf("nested wide ctor headers must be refused as a resource limit, got %v", err)
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Errorf("the term decoder allocated %d MB on nested wide headers", grew>>20)
	}
}

// eNormalizeRecursive is the normalizer EXACTLY as it stood before the iterative
// rewrite, retained as the oracle. eNormalize feeds eHash, which `find --equiv`
// uses to decide that two definitions are the same function — so a changed
// normal form changes DISCOVERY results, not just performance.
func eNormalizeRecursive(chk *checkerMachine, ctx []*Ty, t *Term) *Term {
	if t == nil {
		return nil
	}
	push := func(ty *Ty) []*Ty { return append(append([]*Ty{}, ctx...), ty) }
	nt := *t
	switch t.K {
	case "lam":
		nt.A = eNormalizeRecursive(chk, push(t.Ty), t.A)
	case "let":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
		nt.B = eNormalizeRecursive(chk, push(t.Ty), t.B)
	case "if":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
		nt.B = eNormalizeRecursive(chk, ctx, t.B)
		nt.C = eNormalizeRecursive(chk, ctx, t.C)
	case "app":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
		nt.B = eNormalizeRecursive(chk, ctx, t.B)
	case "field":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
	case "match":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
		nt.Arms = make([]Term, len(t.Arms))
		scrutTy, terr := chk.synth(ctx, t.A)
		md, derr := chk.st.GetDef(t.Hash)
		for i := range t.Arms {
			armCtx := ctx
			if terr == nil && derr == nil && scrutTy.K == "data" && i < len(md.Ctors) {
				for _, f := range instCtorFields(md, scrutTy.Hash, scrutTy.Args, i) {
					armCtx = append(append([]*Ty{}, armCtx...), f)
				}
			}
			nt.Arms[i] = *eNormalizeRecursive(chk, armCtx, &t.Arms[i])
		}
	case "prim", "ctor", "record":
		nt.Args = make([]Term, len(t.Args))
		for i := range t.Args {
			nt.Args[i] = *eNormalizeRecursive(chk, ctx, &t.Args[i])
		}
		if t.K == "prim" && commutativePrims[t.Op] && len(nt.Args) == 2 {
			argTy, _ := chk.synth(ctx, &t.Args[0])
			if isACPrim(t.Op, argTy) {
				return acRebuild(t.Op, acFlatten(t.Op, nt.Args))
			}
			if bytes.Compare(termBytes(&nt.Args[0]), termBytes(&nt.Args[1])) > 0 {
				nt.Args[0], nt.Args[1] = nt.Args[1], nt.Args[0]
			}
		}
	}
	return &nt
}

// TestNormalizerMatchesOracleOnCorpus is the parity obligation for the last
// repaired walker. Compared as canonical BYTES of the normal form, because that
// is what eHash hashes and what discovery compares.
func TestNormalizerMatchesOracleOnCorpus(t *testing.T) {
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
		if err != nil || d.Body == nil {
			continue
		}
		// Each side gets its OWN copy: chk.synth publishes inferred TyArgs into
		// the term, so a shared input would let the first run prepare the
		// second — the same defect that nearly invalidated the checker port.
		a := deepCopyDef(d)
		b := deepCopyDef(d)
		chkA := &checkerMachine{st: st, selfTyVars: a.TyVars, selfTy: a.Ty}
		chkB := &checkerMachine{st: st, selfTyVars: b.TyVars, selfTy: b.Ty}

		ea, eb := &enc{}, &enc{}
		ea.term(eNormalize(chkA, nil, a.Body))
		eb.term(eNormalizeRecursive(chkB, nil, b.Body))
		if !bytes.Equal(ea.b, eb.b) {
			t.Errorf("%s: normal form MOVED (%d vs %d bytes)", name, len(ea.b), len(eb.b))
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("only %d definitions normalized; the corpus did not load", checked)
	}
	t.Logf("iterative normalizer agrees with the oracle on %d definitions", checked)
}

// TestNormalizerHandlesDeepStructures: a term at the profile's edge must
// normalize without host recursion. The recursive version would fault here,
// which is the whole reason for the rewrite — so the oracle is NOT consulted.
func TestNormalizerHandlesDeepStructures(t *testing.T) {
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
	chk := &checkerMachine{st: st}
	for _, n := range []int{10, 1000, 20000} {
		out := eNormalize(chk, nil, spine(n))
		if out == nil {
			t.Fatalf("n=%d normalized to nil", n)
		}
		// Structure preserved: walk the spine and count.
		depth, cur := 0, out
		for cur != nil && cur.K == "ctor" && len(cur.Args) == 2 {
			depth++
			cur = &cur.Args[1]
		}
		if depth != n {
			t.Errorf("n=%d normalized to a spine of %d", n, depth)
		}
	}
	// A broad term at the profile's width.
	wide := &Term{K: "record"}
	for i := 0; i < 5000; i++ {
		wide.Names = append(wide.Names, fmt.Sprintf("f%04d", i))
		wide.Args = append(wide.Args, Term{K: "int", Int: big.NewInt(int64(i))})
	}
	if out := eNormalize(chk, nil, wide); len(out.Args) != 5000 {
		t.Errorf("a 5,000-field record normalized to %d fields", len(out.Args))
	}
}

// TestNormalizerBoundedWorkOnDeepBinders is the second P1 external review found:
// removing host recursion does not by itself bound the work PER NODE.
//
// The context was copied whole at every binder, so a deep lambda/let spine was
// quadratic even though the traversal was iterative — a valid 10,000-binder
// spine allocated roughly 400MB, and a profile-edge spine gigabytes, leaving
// `find --equiv` resource-exhaustible after the recursion was gone.
//
// A persistent context makes extension O(1); a slice is materialised only where
// chk.synth needs one.
func TestNormalizerBoundedWorkOnDeepBinders(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	// A lambda spine: 10,000 nested binders, well inside the profile.
	spine := func(n int) *Term {
		body := &Term{K: "var", Idx: 0}
		cur := body
		for i := 0; i < n; i++ {
			cur = &Term{K: "lam", Ty: tInt(), A: cur}
		}
		return cur
	}
	term := spine(10000)
	if _, ok := countCanonicalNodes(&Def{K: "func", Body: term}, maxCanonicalNodes); !ok {
		t.Fatal("setup: the spine is not inside the profile")
	}

	chk := &checkerMachine{st: st}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out := eNormalize(chk, nil, term)
	runtime.ReadMemStats(&after)

	if out == nil {
		t.Fatal("normalized to nil")
	}
	depth := 0
	for cur := out; cur != nil && cur.K == "lam"; cur = cur.A {
		depth++
	}
	if depth != 10000 {
		t.Fatalf("normalized to a %d-binder spine, want 10000", depth)
	}
	// Copying the context per binder is ~400MB here. Linear behaviour is a few
	// megabytes; 64MB separates the two by an order of magnitude without being
	// brittle about allocator details.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Errorf("normalizing a 10,000-binder spine allocated %d MB — extending the "+
			"binder context must be O(1), not a copy of the whole context", grew>>20)
	}
}

// TestACFlattenHandlesDeepChains covers the other P1: a normalized
// associative-commutative chain is as deep as the term that produced it, and
// acFlatten descended it on the host stack.
//
// The structural gate did NOT see this. Its detector recognised a selector
// directly on a parameter, and acFlatten descends `args[i].Args` — two steps
// away, through an index. The detector was widened in the same commit; this
// test covers the behaviour rather than the detector.
func TestACFlattenHandlesDeepChains(t *testing.T) {
	chain := func(n int) []Term {
		cur := Term{K: "int", Int: big.NewInt(0)}
		for i := 0; i < n; i++ {
			cur = Term{K: "prim", Op: "+", Args: []Term{
				{K: "int", Int: big.NewInt(int64(i + 1))}, cur}}
		}
		return []Term{cur, {K: "int", Int: big.NewInt(-1)}}
	}
	for _, n := range []int{1, 10, 20000} {
		out := acFlatten("+", chain(n))
		if len(out) != n+2 {
			t.Fatalf("n=%d flattened to %d leaves, want %d", n, len(out), n+2)
		}
	}
	// ORDER is preserved, checked against the RECURSIVE original rather than
	// against a hand-written expectation. My first attempt wrote the order out
	// by hand and got it backwards — the chain nests right, so leaves emerge
	// outermost-first — which would have failed a correct implementation.
	// Deriving the expectation from the oracle removes my reading of the
	// nesting from the test entirely.
	for _, n := range []int{1, 3, 10, 200} {
		got := acFlatten("+", chain(n))
		want := acFlattenRecursive("+", chain(n))
		if len(got) != len(want) {
			t.Fatalf("n=%d: %d leaves vs oracle's %d", n, len(got), len(want))
		}
		for i := range want {
			if !bytes.Equal(termBytes(&got[i]), termBytes(&want[i])) {
				t.Fatalf("n=%d: leaf %d differs from the oracle — flattening changed leaf order", n, i)
			}
		}
	}
}

// acFlattenRecursive is the flattener as it stood before the rewrite, kept as
// the ordering oracle.
func acFlattenRecursive(op string, args []Term) []Term {
	var out []Term
	for i := range args {
		if args[i].K == "prim" && args[i].Op == op && len(args[i].Args) == 2 {
			out = append(out, acFlattenRecursive(op, args[i].Args)...)
		} else {
			out = append(out, args[i])
		}
	}
	return out
}

// TestACNormalizationCostIsUnchangedByTheRewrite records a PRE-EXISTING
// quadratic and pins it against regression, rather than claiming a fix.
//
// External review reported that many commutative primitives beneath a deep
// binder spine allocate gigabytes and attributed it to ctxSlice materialising
// the context per primitive. The mechanism is real, and caching it is correct —
// but measurement shows it is NOT the dominant cost:
//
//	iterative  1224 MB
//	recursive  1243 MB
//
// The recursive normalizer, which predates all of this work, allocates the same.
// The cost is AC normalization itself: acFlatten and termBytes run at EVERY
// level of a nested commutative chain, so the work is quadratic in chain depth.
// That is an algorithmic property of e-graph normalization, not something the
// iterative rewrite introduced or could remove.
//
// So the honest claim is narrow: the rewrite made `find --equiv` STACK-safe and
// left its COMPLEXITY unchanged. `find --equiv` remains resource-exhaustible on
// an admitted deeply-nested commutative chain, by the algorithm rather than by
// recursion — recorded on #149 rather than implied to be closed.
//
// This test exists so the cost cannot silently WORSEN: the iterative version
// must stay within a small factor of the oracle it replaced.
func TestACNormalizationCostIsUnchangedByTheRewrite(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	build := func() *Term {
		body := &Term{K: "var", Idx: 0}
		for i := 0; i < 2000; i++ {
			body = &Term{K: "prim", Op: "==", Args: []Term{{K: "var", Idx: 0}, *body}}
		}
		term := body
		for i := 0; i < 500; i++ {
			term = &Term{K: "lam", Ty: tInt(), A: term}
		}
		return term
	}
	measure := func(f func()) uint64 {
		var a, b runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&a)
		f()
		runtime.ReadMemStats(&b)
		return b.TotalAlloc - a.TotalAlloc
	}
	iter := measure(func() { eNormalize(&checkerMachine{st: st}, nil, build()) })
	rec := measure(func() { eNormalizeRecursive(&checkerMachine{st: st}, nil, build()) })

	// Within 2x of the oracle. A regression that made the iterative version
	// materially more expensive would show here; the shared quadratic would not.
	if iter > rec*2 {
		t.Errorf("the iterative normalizer allocated %d MB against the oracle's %d MB — "+
			"the rewrite was supposed to change stack usage, not cost", iter>>20, rec>>20)
	}
	t.Logf("iterative %d MB, oracle %d MB — the quadratic is shared, and pre-existing",
		iter>>20, rec>>20)
}
