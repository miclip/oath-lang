package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
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
