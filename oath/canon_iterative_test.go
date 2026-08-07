package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
		if s := etaReduce(chk.st, &nt); s != nil {
			r := *s
			return &r
		}
	case "let":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
		nt.B = eNormalizeRecursive(chk, push(t.Ty), t.B)
	case "if":
		nt.A = eNormalizeRecursive(chk, ctx, t.A)
		nt.B = eNormalizeRecursive(chk, ctx, t.B)
		nt.C = eNormalizeRecursive(chk, ctx, t.C)
		if s := ifSelect(&nt); s != nil {
			r := *s
			return &r
		}
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
		// The unit / idempotence / involution rules, expressed through the SAME
		// helpers the normalizer under test calls. That is deliberate and it is
		// the same trade the oracle already makes for acFlatten and acRebuild:
		// the oracle's job is to witness the ITERATION, so re-deriving the rules
		// here would make it a second implementation of the rewrite set — and
		// two hand-written rule sets disagree exactly where nobody looks. What
		// this file cannot see inside a shared helper, the recorded digests in
		// TestNormalizerACBytesMatchOracleOnAdversarialChains can.
		if t.K == "prim" {
			if s := negInvolution(t.Op, nt.Args); s != nil {
				r := *s
				return &r
			}
		}
		if t.K == "prim" && commutativePrims[t.Op] && len(nt.Args) == 2 {
			argTy, _ := chk.synth(ctx, &t.Args[0])
			if isACPrim(t.Op, argTy) {
				return acRebuild(t.Op, acFlatten(t.Op, nt.Args))
			}
			if s := mulUnitSurvivor(t.Op, argTy, nt.Args); s != nil {
				r := *s
				return &r
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

// TestACNormalizationCostIsUnchangedByTheRewrite pins ONE RELATIVE BOUND: the
// iterative normalizer must not cost materially more than the recursive oracle
// it replaced. That is the claim the #149 rewrite had to support — stack safety
// bought without a cost regression — and it is a statement about the two
// implementations, not about how expensive either of them is.
//
// It was written when both sides allocated gigabytes on this term (1224 MB
// iterative against 1243 MB recursive) and AC normalization was quadratic in
// chain depth in both. #151 removed that quadratic from the iterative side, so
// the two numbers are now far apart and far smaller. NOTHING HERE NEEDED TO
// CHANGE, which is the point of a relative bound: the assertion compares the
// implementations, so it survives either of them getting faster and still fails
// if the iterative one regresses toward the oracle.
//
// The absolute figures #151 was measured against, and the tests that own the
// complexity claim, are below — this one deliberately does not assert either.
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
	// materially more expensive would show here. The bound is one-sided on
	// purpose: the iterative side coming in FAR under the oracle is #151
	// working, not something for this test to have an opinion about.
	if iter > rec*2 {
		t.Errorf("the iterative normalizer allocated %d MB against the oracle's %d MB — "+
			"the rewrite was supposed to change stack usage, not cost", iter>>20, rec>>20)
	}
	t.Logf("iterative %d MB, oracle %d MB — the iterative side carries no cost regression",
		iter>>20, rec>>20)
}

// ---------------------------------------------------------------------------
// #151 — the quadratic itself, which the test above deliberately does NOT claim
// to fix. The three tests below are its acceptance criteria, and they divide the
// obligation so that no one of them can be satisfied by the wrong repair. They
// were written to FAIL against the pre-repair normalizer, and each was watched
// failing before the repair was written:
//
//	SHAPE   allocation against CHAIN DEPTH. Owns the asymptotics; a constant-
//	        factor win does not satisfy it.
//	COST    the one term #151 measured. Owns the concrete claim against the two
//	        recorded baselines; a shape improvement that leaves this term
//	        gigabyte-scale does not satisfy it.
//	BYTES   the normal form is unchanged. Owns identity — eHash feeds
//	        `find --equiv`, so a cheaper normalizer that reorders leaves changes
//	        DISCOVERY results, which is a worse defect than the cost it fixes.
//
// SHAPE and COST measured the defect: ×4 per doubling on both AC paths, and
// 1224 MB on the term #151 recorded. BYTES passed before the repair as well as
// after it — its job was never to fail, but to constrain what the repair was
// allowed to change while making the other two pass.

// acAllocBytes is the allocation a single call attributes to itself. TotalAlloc
// is cumulative and never decreases, so this measures work DONE rather than
// memory retained — which is the right quantity for a complexity claim: a
// quadratic that is promptly collected is still quadratic.
func acAllocBytes(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// acChain builds the shape #151 measures: a right-nested chain of one
// commutative primitive over `var 0`, beneath a spine of binders that gives that
// variable a type. The binders are what make the operand type SYNTHESIZABLE, so
// the AC decision is reached rather than skipped.
func acChain(op string, prims, binders int, ty func() *Ty) *Term {
	body := &Term{K: "var", Idx: 0}
	for i := 0; i < prims; i++ {
		body = &Term{K: "prim", Op: op, Args: []Term{{K: "var", Idx: 0}, *body}}
	}
	term := body
	for i := 0; i < binders; i++ {
		term = &Term{K: "lam", Ty: ty(), A: term}
	}
	return term
}

// TestACNormalizationScalesSubQuadraticallyWithChainDepth is #151's first
// acceptance criterion: measure against CHAIN DEPTH, not just total allocation,
// so the SHAPE of the improvement is visible.
//
// A total-allocation bound alone cannot distinguish the two repairs that matter:
// a smaller constant in front of the same quadratic passes it, and that is not
// what #151 asks for — the term is admitted by the portable profile at depths
// far past anything measured here, so only the exponent bounds the exposure.
//
// The instrument is the DOUBLING RATIO. Quadratic work quadruples when depth
// doubles; linear work doubles; n log n sits just above 2. Measured today, every
// doubling on every op lands at 3.9-4.4, so the threshold of 3.0 is not a
// borderline call in either direction: it rejects the quadratic with margin and
// admits anything genuinely sub-quadratic with margin.
//
// BOTH AC PATHS ARE COVERED, because the two repeated walks #151 names live on
// different branches and a repair to one leaves the other quadratic:
//
//	`==`  commutes but does not associate, so it is never flattened — its cost
//	      is termBytes on both operands at every level of the chain.
//	`+`   over Int is AC, so it goes through acFlatten and acRebuild, which
//	      re-walk and re-sort the whole chain at every level.
func TestACNormalizationScalesSubQuadraticallyWithChainDepth(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	depths := []int{500, 1000, 2000, 4000}
	for _, c := range []struct {
		op   string
		ty   func() *Ty
		path string
	}{
		{"==", tInt, "commutative compare (termBytes per level)"},
		{"+", tInt, "associative-commutative (acFlatten/acRebuild per level)"},
	} {
		t.Run(c.op, func(t *testing.T) {
			var got []uint64
			for _, d := range depths {
				// The whole series must be inside the portable profile, or the
				// measurement is of a term the kernel would never admit.
				if _, ok := countCanonicalNodes(&Def{K: "func", Body: acChain(c.op, d, 1, c.ty)}, maxCanonicalNodes); !ok {
					t.Fatalf("setup: a depth-%d chain is outside the portable profile", d)
				}
				got = append(got, acAllocBytes(func() {
					eNormalize(&checkerMachine{st: st}, nil, acChain(c.op, d, 1, c.ty))
				}))
			}
			for i := range depths {
				ratio := 0.0
				if i > 0 {
					ratio = float64(got[i]) / float64(got[i-1])
				}
				t.Logf("depth %5d  %9d KB  x%.2f  (%s)", depths[i], got[i]>>10, ratio, c.path)
			}
			for i := 1; i < len(depths); i++ {
				ratio := float64(got[i]) / float64(got[i-1])
				if ratio > 3.0 {
					t.Errorf("doubling the chain from %d to %d multiplied allocation by %.2f "+
						"(%d KB -> %d KB) — AC normalization is quadratic in chain depth, so "+
						"`find --equiv` is resource-exhaustible on a term the profile admits (#151)",
						depths[i-1], depths[i], ratio, got[i-1]>>10, got[i]>>10)
				}
			}
		})
	}
}

// etaTower builds the shape the eta rule is exhaustible on:
//
//	T_k = fn x. ((fn y. T_{k+1}) x)
//
// Every level is an eta redex whose HEAD contains every deeper level, so a rule
// that walks its head to check binder-freeness and to shift indices pays
// O(depth) at each of O(depth) levels. The redexes fire from the inside out, so
// none of them is skipped and the heads stay large all the way up.
//
// Every binder is Int and the tower IS type-coherent: each application applies
// an `Int -> …` to an Int, so `etaTower(n)` synthesizes at `Int -> Int -> … ->
// Int` with no context. Nothing here depends on that — eta admission consults no
// type at all — but the measurement is of a term the checker accepts rather than
// of a shape the kernel would never see.
func etaTower(n int) *Term {
	cur := &Term{K: "var", Idx: 0}
	for k := 0; k < n; k++ {
		head := &Term{K: "lam", Ty: tInt(), A: cur}
		cur = &Term{K: "lam", Ty: tInt(), A: &Term{K: "app", A: head, B: &Term{K: "var", Idx: 0}}}
	}
	return cur
}

// TestEtaNormalizationScalesSubQuadraticallyWithTowerDepth is the same
// obligation as the AC SHAPE criterion above, on the rewrite that arrived after
// it — and it is here because the eta rule reintroduced exactly the defect #151
// removed, in a new place.
//
// A `lam` head is a sound eta head and an UNBOUNDED one. Admitting it makes the
// binder-freeness test and the index shift walk a subtree that contains the rest
// of the term, which is quadratic on the tower above: measured at ×2.3, ×4.2,
// ×4.2 per doubling before the narrowing, reachable from `find --equiv` on a
// term the portable profile admits. The repair is not a faster walk — it is
// admitting only heads of CONSTANT SIZE (`var`, `ref`), for which the test and
// the shift are O(1) and no tower can accumulate.
//
// The instrument is the DOUBLING RATIO, and the 3.0 threshold is the one the AC
// criterion already justifies: quadratic work quadruples per doubling, linear
// doubles, and n log n sits just above 2 — so 3.0 rejects the quadratic with
// margin and admits anything genuinely sub-quadratic with margin.
//
// The term is built OUTSIDE the measured closure, unlike the AC test. Building
// it is O(depth) and normalizing it was O(depth²); including a linear build in
// the measurement dilutes the ratio most at the smallest depth, which is where
// the instrument is weakest. Excluding it measures the rule and nothing else.
func TestEtaNormalizationScalesSubQuadraticallyWithTowerDepth(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	depths := []int{500, 1000, 2000, 4000}
	var got []uint64
	for _, d := range depths {
		// The whole series must be inside the portable profile, or the
		// measurement is of a term the kernel would never admit.
		tm := etaTower(d)
		if _, ok := countCanonicalNodes(&Def{K: "func", Body: tm}, maxCanonicalNodes); !ok {
			t.Fatalf("setup: a depth-%d eta tower is outside the portable profile", d)
		}
		got = append(got, acAllocBytes(func() {
			eNormalize(&checkerMachine{st: st}, nil, tm)
		}))
	}
	for i := range depths {
		ratio := 0.0
		if i > 0 {
			ratio = float64(got[i]) / float64(got[i-1])
		}
		t.Logf("tower depth %5d  %9d KB  x%.2f", depths[i], got[i]>>10, ratio)
	}
	for i := 1; i < len(depths); i++ {
		ratio := float64(got[i]) / float64(got[i-1])
		if ratio > 3.0 {
			t.Errorf("doubling the eta tower from %d to %d multiplied allocation by %.2f "+
				"(%d KB -> %d KB) — eta normalization is quadratic in tower depth, so "+
				"`find --equiv` is resource-exhaustible on a term the profile admits",
				depths[i-1], depths[i], ratio, got[i-1]>>10, got[i]>>10)
		}
	}
}

// The two allocation figures #151 recorded, on the term it recorded them for.
// They are CONSTANTS rather than live measurements for opposite reasons, and
// only one of them could be derived instead:
//
//	acRecordedIterative  the pre-repair iterative normalizer, which no longer
//	                     exists to be measured. The number is the only surviving
//	                     evidence of what the repair had to beat, which is
//	                     exactly why it is written down rather than derived.
//	acRecordedRecursive  the recursive oracle, which is retained in this file and
//	                     therefore CAN be measured live. It is measured live
//	                     below as well, and the constant is kept only so the two
//	                     can be seen to agree — a machine or toolchain that moved
//	                     the numbers shows up as a divergence in the log rather
//	                     than silently rebasing the claim.
const (
	acRecordedIterative = 1224 << 20
	acRecordedRecursive = 1243 << 20
)

// TestACNormalizationBeatsBothRecordedBaselines is #151's fourth acceptance
// criterion — improvement against BOTH baselines — on the exact term the issue
// measured: 6,000 commutative primitives beneath 2,000 binders, 16,001 canonical
// nodes, comfortably inside the portable profile.
//
// Beating the baselines by any margin at all is too weak to be worth asserting
// on its own: an implementation that came in one megabyte under would satisfy
// the letter and leave `find --equiv` exhaustible. So the bar is a QUARTER of
// the lower baseline. That number is not a prediction of the repair — removing
// the repeated walks should land far below it — it is the point past which an
// implementation is no longer merely quadratic with a better constant. The
// asymptotics are owned by the shape test above; this test owns the concrete
// term, and its threshold is deliberately loose enough not to fail a correct
// repair that is slower than hoped.
func TestACNormalizationBeatsBothRecordedBaselines(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	build := func() *Term { return acChain("==", 6000, 2000, tInt) }
	nodes, ok := countCanonicalNodes(&Def{K: "func", Body: build()}, maxCanonicalNodes)
	if !ok {
		t.Fatalf("setup: the 6,000-prim/2,000-binder term is outside the portable profile")
	}
	if nodes != 16001 {
		t.Fatalf("setup: the term has %d canonical nodes, not the 16,001 #151 measured", nodes)
	}

	iter := acAllocBytes(func() { eNormalize(&checkerMachine{st: st}, nil, build()) })
	rec := acAllocBytes(func() { eNormalizeRecursive(&checkerMachine{st: st}, nil, build()) })
	t.Logf("normalizer %d MB; oracle %d MB live, %d MB recorded; pre-repair iterative %d MB recorded",
		iter>>20, rec>>20, acRecordedRecursive>>20, acRecordedIterative>>20)

	// The live oracle, which cannot go stale.
	if iter >= rec {
		t.Errorf("the normalizer allocated %d MB against the recursive oracle's %d MB — "+
			"#151 requires beating the oracle, not matching it", iter>>20, rec>>20)
	}
	// The two recorded baselines, named in #151.
	if iter >= uint64(acRecordedRecursive) {
		t.Errorf("the normalizer allocated %d MB, not below the recorded recursive baseline of %d MB (#151)",
			iter>>20, acRecordedRecursive>>20)
	}
	if iter >= uint64(acRecordedIterative) {
		t.Errorf("the normalizer allocated %d MB, not below the recorded iterative baseline of %d MB (#151)",
			iter>>20, acRecordedIterative>>20)
	}
	if margin := uint64(acRecordedIterative) / 4; iter > margin {
		t.Errorf("the normalizer allocated %d MB against a required %d MB — under the lower "+
			"recorded baseline is not enough, because a smaller constant in front of the same "+
			"quadratic satisfies that and leaves `find --equiv` exhaustible (#151)",
			iter>>20, margin>>20)
	}
}

// acNest builds one association of the same leaf sequence. The three shapes are
// the ones a flattening bug distinguishes: `assoc` is what acFlatten's own output
// looks like, `left` is what it must still recognise, and `balanced` is what
// neither special case covers.
func acNest(op string, leaves []Term, shape string) Term {
	if len(leaves) == 1 {
		return leaves[0]
	}
	switch shape {
	case "right":
		cur := leaves[len(leaves)-1]
		for i := len(leaves) - 2; i >= 0; i-- {
			cur = Term{K: "prim", Op: op, Args: []Term{leaves[i], cur}}
		}
		return cur
	case "left":
		cur := leaves[0]
		for i := 1; i < len(leaves); i++ {
			cur = Term{K: "prim", Op: op, Args: []Term{cur, leaves[i]}}
		}
		return cur
	default: // balanced
		level := append([]Term{}, leaves...)
		for len(level) > 1 {
			var next []Term
			for i := 0; i < len(level); i += 2 {
				if i+1 == len(level) {
					next = append(next, level[i])
					continue
				}
				next = append(next, Term{K: "prim", Op: op, Args: []Term{level[i], level[i+1]}})
			}
			level = next
		}
		return level[0]
	}
}

// acPermute returns a small fixed set of orderings — identity, reverse, two
// rotations and an adjacent swap. Deterministic on purpose: a seeded shuffle
// would make a failure depend on a seed nobody recorded.
func acPermute(leaves []Term) [][]Term {
	n := len(leaves)
	rot := func(k int) []Term {
		out := make([]Term, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, leaves[(i+k)%n])
		}
		return out
	}
	rev := make([]Term, 0, n)
	for i := n - 1; i >= 0; i-- {
		rev = append(rev, leaves[i])
	}
	swapped := append([]Term{}, leaves...)
	if n > 1 {
		swapped[0], swapped[1] = swapped[1], swapped[0]
	}
	return [][]Term{append([]Term{}, leaves...), rev, rot(1), rot(n / 2), swapped}
}

func acInts(vals ...int64) []Term {
	out := make([]Term, 0, len(vals))
	for _, v := range vals {
		out = append(out, Term{K: "int", Int: big.NewInt(v)})
	}
	return out
}

// TestNormalizerACBytesMatchOracleOnAdversarialChains is #151's third acceptance
// criterion, and the one that constrains the repair rather than demanding it:
// the normal form must be preserved BYTE-FOR-BYTE.
//
// eHash hashes the normal form and `find --equiv` compares eHashes, so a
// normalizer that gets faster by reordering leaves does not lose performance
// evidence — it silently changes which definitions the registry reports as the
// same function. That is a worse defect than the cost being repaired, and it
// would pass every test above.
//
// The inputs are chosen for what a flattening optimization is most likely to get
// wrong, none of which the corpus contains:
//
//   - DUPLICATE and EQUAL-COMPARING leaves, where the sort's comparator returns
//     neither less nor greater. sort.Slice is not stable, so any change to the
//     order leaves are COLLECTED in is observable exactly here and nowhere else.
//   - VARIED ASSOCIATION of one leaf multiset, which is the whole point of
//     flattening: a repair that recognises only its own right-nested output
//     would leave left-nested and balanced inputs unflattened.
//   - MIXED OPERATORS, where a chain must stop at the boundary rather than
//     absorb a foreign node.
//   - Float `+`, which commutes but does NOT associate. It must still be
//     swapped and must NOT be flattened; the control below shows this suite can
//     see the difference.
func TestNormalizerACBytesMatchOracleOnAdversarialChains(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	norm := func(mk func() *Term) []byte {
		// Each side gets a FRESH term: chk.synth publishes inferred TyArgs into
		// the term it is given, so a shared input would let one run prepare the
		// other.
		return termBytes(eNormalize(&checkerMachine{st: st}, nil, mk()))
	}
	oracle := func(mk func() *Term) []byte {
		return termBytes(eNormalizeRecursive(&checkerMachine{st: st}, nil, mk()))
	}

	bools := func(vals ...bool) []Term {
		out := make([]Term, 0, len(vals))
		for _, v := range vals {
			out = append(out, Term{K: "bool", Bool: v})
		}
		return out
	}
	floats := func(vals ...float64) []Term {
		out := make([]Term, 0, len(vals))
		for _, v := range vals {
			out = append(out, Term{K: "float", Float: v})
		}
		return out
	}
	// A leaf multiset whose members compare EQUAL as bytes but are separate
	// nodes, plus a compound leaf repeated: the sort cannot order these, so any
	// change in collection order shows up in the result.
	compound := Term{K: "prim", Op: "*", Args: acInts(2, 3)}
	nested := Term{K: "prim", Op: "*", Args: []Term{{K: "prim", Op: "+", Args: acInts(1, 1)}, {K: "int", Int: big.NewInt(9)}}}

	// THE GOLDEN IS NOT BELT-AND-BRACES — IT IS THE ONLY WITNESS FOR THE AC
	// NORMAL FORM ITSELF, and that was established by mutation rather than
	// assumed.
	//
	// The retained oracle `eNormalizeRecursive` calls the SAME acFlatten and
	// acRebuild as the normalizer under test. That is correct for what the
	// oracle was built for — the iterative REWRITE left those two functions
	// alone — but it makes oracle parity structurally blind to any change
	// INSIDE them, which is exactly where a #151 repair has to land.
	//
	// Measured: inverting acRebuild's comparator, so every AC chain normalizes
	// to the reverse order, is caught by NOTHING in this repository. Not this
	// file's corpus differential (both sides move together), not the fixtures,
	// not `oathrs` — which does not implement eNormalize at all, so the normal
	// form is outside the cross-kernel conformance surface. Every `find --equiv`
	// equivalence class would silently change and every gate would stay green.
	//
	// So the bytes are pinned to a RECORDED digest. Its whole job is to make
	// changing the normal form a deliberate act with a visible diff: criterion 3
	// of #151 says PRESERVE, and preservation cannot be witnessed by comparing
	// two callers of the code being changed. If a future rule set intentionally
	// moves the normal form, updating these constants is the point at which
	// someone has to say so — and to notice that stored eHashes are now stale.
	//
	// THREE OF THESE DIGESTS WERE MOVED DELIBERATELY BY RUNG 1a, and each one is
	// accounted for at its own row below. That is the mechanism working, not a
	// nuisance: the rung added rules that REMOVE leaves from a chain, so every
	// case whose leaf multiset contains an identity element or a duplicated Bool
	// operand had to move, and every case without one had to stay. Six of the
	// nine did stay, which is what makes the three that moved evidence rather
	// than noise. Nothing stores an eHash, so no artifact was invalidated; what
	// changed is which definitions `find --equiv` reports as the same function.
	cases := []struct {
		name   string
		op     string
		ac     bool // flattened, so every association of one multiset collapses
		leaves []Term
		golden string // sha256 over every shape/permutation's normal form, in order
	}{
		{"int-plus-distinct", "+", true, acInts(5, 2, 9, 1),
			"f78fb78da54522d6179427a7dc34ee64db8d71fce1dbc2f889c996a5c705ad35"},
		{"int-plus-all-equal", "+", true, acInts(7, 7, 7, 7, 7),
			"711a33aeaf05042f9922551069461aaa52e4a8927365ac28fc3d7b9092802984"},
		{"int-plus-duplicates", "+", true, acInts(3, 1, 3, 1, 2, 3),
			"73cc7aa40b16ba9f23ded2beffdb77bafaaeb5e4ea8bc999e7381658acd2e67b"},
		// MOVED BY RUNG 1a (`* 1`): the leaf multiset {4,4,1,4,2} contains the
		// multiplicative identity, which acDropUnits removes, so the chain
		// rebuilds from four leaves instead of five. `int-plus-distinct` and
		// `int-plus-duplicates` also contain a 1 and did NOT move, because 1 is
		// not `+`'s identity — which is the control on this row's explanation.
		{"int-times-duplicates", "*", true, acInts(4, 4, 1, 4, 2),
			"b9ea30e63ef7d94651a9a5be3aa272a76797c3f49e769ddf2bed60003caf87da"},
		{"int-plus-compound-duplicates", "+", true,
			[]Term{compound, {K: "int", Int: big.NewInt(1)}, compound, nested, nested},
			"37d1f1308c73561ca7ad668a50ac71353d33b9bfdff1f4dc6da40480b993ce5b"},
		// MOVED BY RUNG 1a (`and true`, then idempotence): {T,F,T,T,F} loses its
		// three `true` leaves to the identity rule and its duplicate `false` to
		// idempotence, so a five-leaf chain rebuilds as the single leaf `false`.
		// Both rules are needed to reach that form and each is separately
		// witnessed in find_test.go.
		{"bool-and-duplicates", "and", true, bools(true, false, true, true, false),
			"95019114f461addf39baede6935405f335c347b397925aa8136104d3e2501b12"},
		// MOVED BY RUNG 1a (`or false`, then idempotence): the mirror case —
		// {F,F,T,F} loses its `false` leaves to `or`'s identity and rebuilds as
		// the single leaf `true`. Note the two rows move in OPPOSITE directions
		// (to `false` and to `true`), so a mutant that dropped one Bool literal
		// kind regardless of operator could not produce both.
		{"bool-or-duplicates", "or", true, bools(false, false, true, false),
			"b46050a16cce45da95eefdda2337dbb4d1f33b8294ac8e25748e551df1b0d8d7"},
		{"int-eq-duplicates", "==", false, acInts(6, 6, 2, 6),
			"c1a13c53bc9b0c36a40eb399db51a0b28d9d5b234f18550a3fd89b439cb6cf16"},
		{"float-plus-duplicates", "+", false, floats(1.5, 1.5, -0.5, 1.5),
			"4bf15dbacdb69c0868361ca3b9ca429f600691fcaed6834235623842978c0f1c"},
	}
	shapes := []string{"right", "left", "balanced"}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var canonical []byte
			digest := sha256.New()
			for _, shape := range shapes {
				for pi, perm := range acPermute(c.leaves) {
					leaves := perm
					mk := func() *Term { tm := acNest(c.op, leaves, shape); return &tm }
					got, want := norm(mk), oracle(mk)
					if !bytes.Equal(got, want) {
						t.Fatalf("%s/%s/perm%d: normal form MOVED from the oracle (%d vs %d bytes) — "+
							"eHash feeds `find --equiv`, so this changes discovery, not just cost",
							c.op, shape, pi, len(got), len(want))
					}
					digest.Write(got)
					if !c.ac {
						continue
					}
					// AC: every association and order of one multiset is one
					// normal form. This is the property `find --equiv` sells,
					// and the one a cheaper flatten is most likely to break.
					if canonical == nil {
						canonical = got
					} else if !bytes.Equal(canonical, got) {
						t.Fatalf("%s/%s/perm%d: an AC chain normalized to a DIFFERENT form than "+
							"another association of the same leaves", c.op, shape, pi)
					}
				}
			}
			if got := hex.EncodeToString(digest.Sum(nil)); got != c.golden {
				t.Errorf("the normal form MOVED: %s\n  want %s\n  got  %s\n"+
					"  eHash hashes this, `find --equiv` compares eHashes, and no other check in\n"+
					"  this repository can see the change — the retained oracle shares acFlatten and\n"+
					"  acRebuild with the code under test. If this move is intended, say so and\n"+
					"  account for every stored eHash it invalidates (#151 criterion 3).",
					c.name, c.golden, got)
			}
		})
	}

	// CONTROL. Float `+` does not associate, so re-association must NOT collapse
	// — if it did, the checks above would be passing vacuously on a normalizer
	// that flattens everything. This is the mutation that tells a working suite
	// from one that cannot see association at all.
	fl := floats(1.5, 2.5, 3.5, 4.5)
	l := norm(func() *Term { tm := acNest("+", fl, "left"); return &tm })
	r := norm(func() *Term { tm := acNest("+", fl, "right"); return &tm })
	if bytes.Equal(l, r) {
		t.Error("left- and right-associated Float chains normalized to the same form — " +
			"Float addition does not associate, so flattening it is unsound")
	}
	// And the same multiset over Int MUST collapse, so the control above is
	// discriminating rather than merely negative.
	il := norm(func() *Term { tm := acNest("+", acInts(1, 2, 3, 4), "left"); return &tm })
	ir := norm(func() *Term { tm := acNest("+", acInts(1, 2, 3, 4), "right"); return &tm })
	if !bytes.Equal(il, ir) {
		t.Error("left- and right-associated Int chains normalized differently — AC flattening is not happening")
	}

	// DEPTH, where a repair's shortcuts actually live. Byte parity on a chain
	// long enough that any per-level caching or early-exit is exercised, with
	// all-equal leaves so leaf order is unconstrained by the comparator.
	for _, c := range []struct {
		op    string
		depth int
		ty    func() *Ty
	}{
		{"==", 2000, tInt},
		{"+", 600, tInt},
		{"and", 600, tBool},
	} {
		mk := func() *Term { return acChain(c.op, c.depth, 1, c.ty) }
		if got, want := norm(mk), oracle(mk); !bytes.Equal(got, want) {
			t.Errorf("%s at depth %d: normal form MOVED from the oracle (%d vs %d bytes)",
				c.op, c.depth, len(got), len(want))
		}
	}
}

// TestCompareTermsCanonicalMatchesByteCompare is the differential for the
// comparator #151 introduced. Its claim is exact: for ANY two terms it returns
// what bytes.Compare over their full canonical encodings returns. Nothing else
// witnesses that directly — the normalizer tests only reach it through terms
// whose encodings differ in their first byte, which is the one case no
// implementation could get wrong.
//
// THE UNIVERSE IS PAIRS OF TERMS, so it is built from two sources that fail
// differently: every body in the corpus, which is what the kernel actually
// compares, and constructed pairs whose encodings agree for thousands of bytes,
// which is the only way to reach the doubling loop at all. The control at the
// end asserts that the second kind is really present — without it this test
// would pass while exercising a single 64-byte encode, which is precisely the
// vacuous witness this repository keeps finding.
func TestCompareTermsCanonicalMatchesByteCompare(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	var terms []*Term
	seen := map[string]bool{}
	for _, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		if d, err := st.GetDef(h); err == nil && d.Body != nil {
			terms = append(terms, d.Body)
		}
	}
	if len(terms) < 100 {
		t.Fatalf("only %d corpus bodies; the corpus did not load", len(terms))
	}

	// Pairs whose encodings agree far past the comparator's first window: one
	// long chain, and the same chain with a single leaf changed at the far end.
	longChain := func(n int, last int64) *Term {
		cur := Term{K: "int", Int: big.NewInt(last)}
		for i := 0; i < n; i++ {
			cur = Term{K: "prim", Op: "+", Args: []Term{{K: "int", Int: big.NewInt(1)}, cur}}
		}
		return &cur
	}
	deep := []*Term{longChain(200, 1), longChain(200, 2), longChain(201, 1), longChain(200, 1)}
	terms = append(terms, deep...)

	keys := make([][]byte, len(terms))
	for i, tm := range terms {
		keys[i] = termBytes(tm)
	}
	sign := func(n int) int {
		switch {
		case n < 0:
			return -1
		case n > 0:
			return 1
		}
		return 0
	}
	for i := range terms {
		for j := range terms {
			want := sign(bytes.Compare(keys[i], keys[j]))
			if got := sign(compareTermsCanonical(terms[i], terms[j])); got != want {
				t.Fatalf("pair (%d,%d): comparator said %d, bytes.Compare said %d "+
					"(%d vs %d encoded bytes)", i, j, got, want, len(keys[i]), len(keys[j]))
			}
		}
	}

	// CONTROL: the deep pairs must actually differ LATE, or the loop above
	// never left its first encode window and this test proved nothing about
	// the part of the comparator that can be wrong.
	firstDiff := func(a, b []byte) int {
		n := len(a)
		if len(b) < n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			if a[i] != b[i] {
				return i
			}
		}
		return n
	}
	da, db := termBytes(deep[0]), termBytes(deep[1])
	if d := firstDiff(da, db); d <= 64 {
		t.Fatalf("setup: the deep pair diverges at byte %d, inside the first encode "+
			"window — the doubling loop was never exercised", d)
	} else {
		t.Logf("%d corpus bodies + deep pairs; deepest divergence at byte %d of %d",
			len(terms)-len(deep), d, len(da))
	}
	if !bytes.Equal(termBytes(deep[0]), termBytes(deep[3])) {
		t.Fatal("setup: the equal-pair control is not equal")
	}
}

// TestNormalizerForcesDeferredChainUnderNonAssociatingParent covers the one
// branch #151's repair added that no other test reaches.
//
// Deferring an AC chain's rebuild to its root rests on the root existing: an
// interior node leaves itself un-normalized because a same-op parent will
// flatten straight through it. That parent decides ASSOCIATIVITY from its own
// synthesized operand type, and the two decisions can disagree — a parent whose
// first operand does not synthesize is not AC, and it must materialise the child
// it is no longer going to absorb. Without that, a deferred chain escapes
// un-normalized and `find --equiv` silently loses the equivalence.
//
// It needs a term whose operand types disagree between a node and its child,
// which no well-typed definition produces — so the corpus differential cannot
// reach it, and neutering the repair's force path passes every other test.
func TestNormalizerForcesDeferredChainUnderNonAssociatingParent(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	// args[0] is an out-of-range de Bruijn index, so the parent's synth fails
	// and the parent is not AC; args[1] is an ordinary Int chain, so the child
	// is AC and defers.
	mk := func() *Term {
		inner := acNest("+", acInts(3, 1, 2), "right")
		return &Term{K: "prim", Op: "+", Args: []Term{{K: "var", Idx: 99}, inner}}
	}
	if ty, err := (&checkerMachine{st: st}).synth(nil, &mk().Args[0]); err == nil {
		t.Fatalf("setup: the parent's first operand synthesized to %v — it must FAIL "+
			"for the parent to be non-associating", ty)
	}
	if ty, err := (&checkerMachine{st: st}).synth(nil, &mk().Args[1].Args[0]); err != nil || !isACPrim("+", ty) {
		t.Fatalf("setup: the child's operand must synthesize to an AC type, got %v/%v", ty, err)
	}

	got := termBytes(eNormalize(&checkerMachine{st: st}, nil, mk()))
	want := termBytes(eNormalizeRecursive(&checkerMachine{st: st}, nil, mk()))
	if !bytes.Equal(got, want) {
		t.Errorf("a deferred AC chain under a non-associating parent was left un-normalized "+
			"(%d vs the oracle's %d bytes)", len(got), len(want))
	}
	// The chain really must have been rebuilt: sorted leaves, not source order.
	if bytes.Equal(got, termBytes(mk())) {
		t.Error("the term came back unchanged — nothing was normalized at all")
	}
}

// TestBoundedEncodeIsAnExactPrefixInsideAtomicPayloads witnesses the LIMIT,
// which is a different claim from the comparator's ANSWER and needs its own
// test because no existing one can fail on it.
//
// compareTermsCanonical's cost argument is that a limited encode writes at most
// `limit` bytes, so the doubling loop's total work is a geometric series in the
// position of the first difference. Checking the limit only BETWEEN work items
// leaves that argument false: a single work item can append a whole big.Int
// magnitude, a whole string, or a whole TYPE encoding, so a 64-byte window can
// materialise megabytes. The ordering stays correct the whole time — the bytes
// are still a prefix, just far too many of them — which is precisely why the
// comparator differential is blind to it. Every assertion below is about SIZE
// and PREFIX EXACTNESS; the comparator's verdict is checked only as a
// regression guard.
//
// THE UNIVERSE IS DERIVED FROM THE CLAIM, not from the encoder's switch: it is
// the append paths that can emit an unbounded run of bytes without returning to
// enc.term's loop. That is bigint (`int`, `rat`), str (both the inline operator
// in `prim` and the deferred 's' item behind `record`/`field`), and the type
// encoder reached through `lam`/`let` (ty) and `self`/`ctor` (tys). A payload
// buried under a chain is included so the run does not begin at byte 1.
func TestBoundedEncodeIsAnExactPrefixInsideAtomicPayloads(t *testing.T) {
	const payload = 8000

	bigA := new(big.Int).Lsh(big.NewInt(1), 8*payload)
	bigB := new(big.Int).Add(bigA, big.NewInt(1)) // differs in the LAST magnitude byte
	strA := strings.Repeat("z", payload)
	strB := strA[:payload-1] + "y"
	deepTy := func(tail *Ty) *Ty {
		cur := tail
		for i := 0; i < payload; i++ {
			cur = &Ty{K: "fun", A: &Ty{K: "int"}, B: cur}
		}
		return cur
	}
	wideTys := func(tail *Ty) []Ty {
		out := make([]Ty, payload)
		for i := range out {
			out[i] = Ty{K: "int"}
		}
		out[len(out)-1] = *tail
		return out
	}
	one := Term{K: "int", Int: big.NewInt(1)}
	behind := func(t Term) *Term {
		// Three ordinary nodes first, so the unbounded run starts well inside
		// the encoding rather than at its second byte.
		return &Term{K: "prim", Op: "+", Args: []Term{one, {K: "prim", Op: "+", Args: []Term{one, t}}}}
	}

	cases := []struct {
		name string
		a, b *Term
	}{
		{"int-magnitude", &Term{K: "int", Int: bigA}, &Term{K: "int", Int: bigB}},
		{"rat-magnitude",
			&Term{K: "rat", Rat: new(big.Rat).SetInt(bigA)},
			&Term{K: "rat", Rat: new(big.Rat).SetInt(bigB)}},
		{"prim-operator",
			&Term{K: "prim", Op: strA, Args: []Term{one}},
			&Term{K: "prim", Op: strB, Args: []Term{one}}},
		{"record-field-name",
			&Term{K: "record", Names: []string{strA}, Args: []Term{one}},
			&Term{K: "record", Names: []string{strB}, Args: []Term{one}}},
		{"field-projection-name",
			&Term{K: "field", Op: strA, A: &one},
			&Term{K: "field", Op: strB, A: &one}},
		{"lam-binder-type",
			&Term{K: "lam", Ty: deepTy(tInt()), A: &one},
			&Term{K: "lam", Ty: deepTy(tBool()), A: &one}},
		{"self-type-arguments",
			&Term{K: "self", TyArgs: wideTys(tInt())},
			&Term{K: "self", TyArgs: wideTys(tBool())}},
		{"payload-behind-a-chain",
			behind(Term{K: "int", Int: bigA}),
			behind(Term{K: "int", Int: bigB})},
	}

	sign := func(n int) int {
		switch {
		case n < 0:
			return -1
		case n > 0:
			return 1
		}
		return 0
	}
	firstDiff := func(a, b []byte) int {
		n := len(a)
		if len(b) < n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			if a[i] != b[i] {
				return i
			}
		}
		return n
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for side, tm := range map[string]*Term{"a": c.a, "b": c.b} {
				full := termBytes(tm)
				if len(full) < 1000 {
					t.Fatalf("setup: %s encodes to only %d bytes — the payload is not large "+
						"enough for a 64-byte window to be exceeded", side, len(full))
				}
				// UNLIMITED MUST NOT MOVE. The limit is an added stop condition,
				// never a change to which bytes are produced.
				var oracle enc
				encTermRecursive(&oracle, tm)
				if !bytes.Equal(full, oracle.b) {
					t.Fatalf("%s: unlimited encoding moved from the pre-#151 oracle (%d vs %d bytes)",
						side, len(full), len(oracle.b))
				}
				if unl := (&enc{}); true {
					unl.term(tm)
					if unl.truncated {
						t.Fatalf("%s: an unlimited encode reported itself truncated", side)
					}
				}

				limits := make([]int, 0, 320)
				for l := 1; l <= 300; l++ {
					limits = append(limits, l)
				}
				limits = append(limits, 1000, 4096, len(full)-1, len(full), len(full)+1, 2*len(full))
				for _, limit := range limits {
					e := &enc{limit: limit}
					e.term(tm)
					want := limit
					if len(full) < want {
						want = len(full)
					}
					if len(e.b) != want {
						t.Fatalf("%s: a limit of %d over a %d-byte encoding produced %d bytes "+
							"(want %d) — the limit is honoured only BETWEEN work items, so one "+
							"bigint/str/ty append runs past it", side, limit, len(full), len(e.b), want)
					}
					if !bytes.Equal(e.b, full[:len(e.b)]) {
						t.Fatalf("%s: a limit of %d produced bytes that are not a prefix of the "+
							"full encoding", side, limit)
					}
					if got, wantTrunc := e.truncated, len(e.b) < len(full); got != wantTrunc {
						t.Fatalf("%s: a limit of %d wrote %d of %d bytes but reported truncated=%v",
							side, limit, len(e.b), len(full), got)
					}
				}
			}

			// CONTROL: the pair must diverge LATE, or the parity check below
			// never leaves the comparator's first window and proves nothing
			// about the doubling loop over a truncated atomic payload.
			ka, kb := termBytes(c.a), termBytes(c.b)
			if d := firstDiff(ka, kb); d <= 64 {
				t.Fatalf("setup: the pair diverges at byte %d, inside the first encode window", d)
			}
			for _, p := range [][2]*Term{{c.a, c.b}, {c.b, c.a}, {c.a, c.a}, {c.b, c.b}} {
				want := sign(bytes.Compare(termBytes(p[0]), termBytes(p[1])))
				if got := sign(compareTermsCanonical(p[0], p[1])); got != want {
					t.Fatalf("comparator said %d, bytes.Compare said %d over %d/%d encoded bytes",
						got, want, len(termBytes(p[0])), len(termBytes(p[1])))
				}
			}
		})
	}
}
