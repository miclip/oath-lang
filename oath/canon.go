package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
)

// Canonical binary encoding (format "O1", SPEC §1): a definition's identity
// is SHA-256 over a tag-length-value tree with no optional fields, no
// escaping, and no host-language inheritance. Every field is always written,
// integers are fixed-width big-endian, strings are length-prefixed raw
// UTF-8, and hash references are 32 raw bytes — so there is exactly one
// encoding per definition and nothing for two implementations to disagree
// about except the table of tags.
//
// The strict decoder enforces canonicality on load: unknown tags, malformed
// booleans, unsorted or duplicate record fields, and trailing bytes are all
// rejected, and the store additionally re-hashes and re-typechecks every
// object it reads.

const encMagic0 = 0x4F // 'O'
const encMagic1 = 0x31 // '1'

// Ty tags.
const (
	tagTyInt    = 0x01
	tagTyBool   = 0x02
	tagTyStr    = 0x03
	tagTyVar    = 0x04
	tagTyFun    = 0x05
	tagTyData   = 0x06
	tagTyRec    = 0x07
	tagTyRecord = 0x08
	tagTyRat    = 0x09
)

// Term tags.
const (
	tagTmVar    = 0x10
	tagTmInt    = 0x11
	tagTmBool   = 0x12
	tagTmStr    = 0x13
	tagTmLam    = 0x14
	tagTmApp    = 0x15
	tagTmLet    = 0x16
	tagTmIf     = 0x17
	tagTmPrim   = 0x18
	tagTmRef    = 0x19
	tagTmSelf   = 0x1A
	tagTmCtor   = 0x1B
	tagTmMatch  = 0x1C
	tagTmRecord = 0x1D
	tagTmField  = 0x1E
	tagTmRat    = 0x1F
)

// Def tags.
const (
	tagDefData = 0x01
	tagDefFunc = 0x02
)

// hashDef computes a definition's identity: SHA-256 of its canonical "O1"
// binary encoding, rendered as lowercase hex.
func hashDef(d *Def) string {
	s := sha256.Sum256(encodeDef(d))
	return hex.EncodeToString(s[:])
}

// hashDefV0 is the legacy JSON-based identity (kernel ≤0.6), retained ONLY
// for the one-shot store migration's old→new mapping.
func hashDefV0(d *Def) string {
	b, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ---------- encoder ----------

type enc struct{ b []byte }

func (e *enc) u8(v byte)     { e.b = append(e.b, v) }
func (e *enc) u32(v uint32)  { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *enc) i64(v int64)   { e.b = binary.BigEndian.AppendUint64(e.b, uint64(v)) }
func (e *enc) str(s string)  { e.u32(uint32(len(s))); e.b = append(e.b, s...) }

// bigint encodes an arbitrary-precision integer canonically: a sign byte
// (0x00 for ≥0, 0x01 for <0), then a u32 magnitude length, then the minimal
// big-endian magnitude bytes (no leading zeros; zero is sign 0x00, length 0).
func (e *enc) bigint(v *big.Int) {
	if v.Sign() < 0 {
		e.u8(1)
	} else {
		e.u8(0)
	}
	mag := v.Bytes()
	e.u32(uint32(len(mag)))
	e.b = append(e.b, mag...)
}

func (e *enc) hash(h string) {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) != 32 {
		panic(fmt.Sprintf("malformed hash reference %q in definition", h))
	}
	e.b = append(e.b, raw...)
}

func (e *enc) ty(t *Ty) {
	switch t.K {
	case "int":
		e.u8(tagTyInt)
	case "bool":
		e.u8(tagTyBool)
	case "rat":
		e.u8(tagTyRat)
	case "var":
		e.u8(tagTyVar)
		e.u32(uint32(t.Var))
	case "fun":
		e.u8(tagTyFun)
		e.ty(t.A)
		e.ty(t.B)
	case "data":
		e.u8(tagTyData)
		e.hash(t.Hash)
		e.tys(t.Args)
	case "rec":
		e.u8(tagTyRec)
		e.tys(t.Args)
	case "record":
		e.u8(tagTyRecord)
		e.u32(uint32(len(t.Names)))
		for i, n := range t.Names {
			e.str(n)
			e.ty(&t.Args[i])
		}
	default:
		panic("encode: unknown Ty kind " + t.K)
	}
}

func (e *enc) tys(ts []Ty) {
	e.u32(uint32(len(ts)))
	for i := range ts {
		e.ty(&ts[i])
	}
}

func (e *enc) term(t *Term) {
	switch t.K {
	case "var":
		e.u8(tagTmVar)
		e.u32(uint32(t.Idx))
	case "int":
		e.u8(tagTmInt)
		e.bigint(t.Int)
	case "rat":
		// A rational encodes as its reduced numerator and denominator; big.Rat
		// keeps them coprime with a positive denominator, so this is canonical.
		e.u8(tagTmRat)
		e.bigint(t.Rat.Num())
		e.bigint(t.Rat.Denom())
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
		e.term(t.A)
	case "app":
		e.u8(tagTmApp)
		e.term(t.A)
		e.term(t.B)
	case "let":
		e.u8(tagTmLet)
		e.ty(t.Ty)
		e.term(t.A)
		e.term(t.B)
	case "if":
		e.u8(tagTmIf)
		e.term(t.A)
		e.term(t.B)
		e.term(t.C)
	case "prim":
		e.u8(tagTmPrim)
		e.str(t.Op)
		e.terms(t.Args)
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
		e.terms(t.Args)
	case "match":
		e.u8(tagTmMatch)
		e.hash(t.Hash)
		e.term(t.A)
		e.terms(t.Arms)
	case "record":
		e.u8(tagTmRecord)
		e.u32(uint32(len(t.Names)))
		for i, n := range t.Names {
			e.str(n)
			e.term(&t.Args[i])
		}
	case "field":
		e.u8(tagTmField)
		e.term(t.A)
		e.str(t.Op)
	default:
		panic("encode: unknown Term kind " + t.K)
	}
}

func (e *enc) terms(ts []Term) {
	e.u32(uint32(len(ts)))
	for i := range ts {
		e.term(&ts[i])
	}
}

// encodeDef produces the canonical bytes whose SHA-256 is the definition's
// identity, and which the store persists verbatim.
func encodeDef(d *Def) []byte {
	e := &enc{}
	e.u8(encMagic0)
	e.u8(encMagic1)
	switch d.K {
	case "data":
		e.u8(tagDefData)
		e.u32(uint32(d.TyVars))
		e.u32(uint32(len(d.Ctors)))
		for _, fields := range d.Ctors {
			e.tys(fields)
		}
	case "func":
		e.u8(tagDefFunc)
		e.u32(uint32(d.TyVars))
		e.ty(d.Ty)
		e.term(d.Body)
		e.u32(uint32(len(d.Props)))
		for i := range d.Props {
			e.tys(d.Props[i].Binders)
			e.term(&d.Props[i].Body)
		}
	default:
		panic("encode: unknown Def kind " + d.K)
	}
	return e.b
}

// ---------- strict decoder ----------

type dec struct {
	b   []byte
	pos int
}

func (d *dec) fail(f string, a ...any) error { return fmt.Errorf("O1 decode @%d: %s", d.pos, fmt.Sprintf(f, a...)) }

func (d *dec) u8() (byte, error) {
	if d.pos >= len(d.b) {
		return 0, d.fail("unexpected end")
	}
	v := d.b[d.pos]
	d.pos++
	return v, nil
}

func (d *dec) u32() (int, error) {
	if d.pos+4 > len(d.b) {
		return 0, d.fail("unexpected end in u32")
	}
	v := binary.BigEndian.Uint32(d.b[d.pos:])
	d.pos += 4
	if v > 1<<24 {
		return 0, d.fail("implausible count/length %d", v)
	}
	return int(v), nil
}

func (d *dec) i64() (int64, error) {
	if d.pos+8 > len(d.b) {
		return 0, d.fail("unexpected end in i64")
	}
	v := int64(binary.BigEndian.Uint64(d.b[d.pos:]))
	d.pos += 8
	return v, nil
}

func (d *dec) str() (string, error) {
	n, err := d.u32()
	if err != nil {
		return "", err
	}
	if d.pos+n > len(d.b) {
		return "", d.fail("unexpected end in string")
	}
	s := string(d.b[d.pos : d.pos+n])
	d.pos += n
	return s, nil
}

func (d *dec) bigint() (*big.Int, error) {
	sign, err := d.u8()
	if err != nil {
		return nil, err
	}
	if sign > 1 {
		return nil, d.fail("bad integer sign byte %d", sign)
	}
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	if d.pos+n > len(d.b) {
		return nil, d.fail("unexpected end in integer")
	}
	mag := d.b[d.pos : d.pos+n]
	// Reject non-canonical leading zero (magnitude bytes are minimal).
	if n > 0 && mag[0] == 0 {
		return nil, d.fail("non-canonical integer (leading zero)")
	}
	d.pos += n
	v := new(big.Int).SetBytes(mag)
	if sign == 1 {
		v.Neg(v)
	}
	return v, nil
}

func (d *dec) hash() (string, error) {
	if d.pos+32 > len(d.b) {
		return "", d.fail("unexpected end in hash")
	}
	h := hex.EncodeToString(d.b[d.pos : d.pos+32])
	d.pos += 32
	return h, nil
}

func (d *dec) ty() (*Ty, error) {
	tag, err := d.u8()
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagTyInt:
		return tInt(), nil
	case tagTyRat:
		return tRat(), nil
	case tagTyBool:
		return tBool(), nil
	case tagTyVar:
		v, err := d.u32()
		if err != nil {
			return nil, err
		}
		return tVar(v), nil
	case tagTyFun:
		a, err := d.ty()
		if err != nil {
			return nil, err
		}
		b, err := d.ty()
		if err != nil {
			return nil, err
		}
		return tFun(a, b), nil
	case tagTyData:
		h, err := d.hash()
		if err != nil {
			return nil, err
		}
		args, err := d.tys()
		if err != nil {
			return nil, err
		}
		return tDataTy(h, args), nil
	case tagTyRec:
		args, err := d.tys()
		if err != nil {
			return nil, err
		}
		return tRec(args), nil
	case tagTyRecord:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		out := &Ty{K: "record"}
		for i := 0; i < n; i++ {
			name, err := d.str()
			if err != nil {
				return nil, err
			}
			if i > 0 && name <= out.Names[i-1] {
				return nil, d.fail("record fields not strictly ascending: %q after %q", name, out.Names[i-1])
			}
			t, err := d.ty()
			if err != nil {
				return nil, err
			}
			out.Names = append(out.Names, name)
			out.Args = append(out.Args, *t)
		}
		return out, nil
	}
	return nil, d.fail("unknown Ty tag 0x%02x", tag)
}

func (d *dec) tys() ([]Ty, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	var out []Ty
	for i := 0; i < n; i++ {
		t, err := d.ty()
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

func (d *dec) term() (*Term, error) {
	tag, err := d.u8()
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagTmVar:
		v, err := d.u32()
		if err != nil {
			return nil, err
		}
		return &Term{K: "var", Idx: v}, nil
	case tagTmInt:
		v, err := d.bigint()
		if err != nil {
			return nil, err
		}
		return &Term{K: "int", Int: v}, nil
	case tagTmRat:
		num, err := d.bigint()
		if err != nil {
			return nil, err
		}
		den, err := d.bigint()
		if err != nil {
			return nil, err
		}
		if den.Sign() <= 0 {
			return nil, d.fail("rational denominator must be positive")
		}
		g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(num), den)
		if g.Cmp(big.NewInt(1)) != 0 {
			return nil, d.fail("non-canonical rational (numerator/denominator not coprime)")
		}
		return &Term{K: "rat", Rat: new(big.Rat).SetFrac(num, den)}, nil
	case tagTmBool:
		v, err := d.u8()
		if err != nil {
			return nil, err
		}
		if v > 1 {
			return nil, d.fail("bool byte 0x%02x", v)
		}
		return &Term{K: "bool", Bool: v == 1}, nil
	case tagTmLam:
		ty, err := d.ty()
		if err != nil {
			return nil, err
		}
		a, err := d.term()
		if err != nil {
			return nil, err
		}
		return &Term{K: "lam", Ty: ty, A: a}, nil
	case tagTmApp:
		a, err := d.term()
		if err != nil {
			return nil, err
		}
		b, err := d.term()
		if err != nil {
			return nil, err
		}
		return &Term{K: "app", A: a, B: b}, nil
	case tagTmLet:
		ty, err := d.ty()
		if err != nil {
			return nil, err
		}
		a, err := d.term()
		if err != nil {
			return nil, err
		}
		b, err := d.term()
		if err != nil {
			return nil, err
		}
		return &Term{K: "let", Ty: ty, A: a, B: b}, nil
	case tagTmIf:
		a, err := d.term()
		if err != nil {
			return nil, err
		}
		b, err := d.term()
		if err != nil {
			return nil, err
		}
		c, err := d.term()
		if err != nil {
			return nil, err
		}
		return &Term{K: "if", A: a, B: b, C: c}, nil
	case tagTmPrim:
		op, err := d.str()
		if err != nil {
			return nil, err
		}
		args, err := d.terms()
		if err != nil {
			return nil, err
		}
		return &Term{K: "prim", Op: op, Args: args}, nil
	case tagTmRef:
		h, err := d.hash()
		if err != nil {
			return nil, err
		}
		tys, err := d.tys()
		if err != nil {
			return nil, err
		}
		return &Term{K: "ref", Hash: h, TyArgs: tys}, nil
	case tagTmSelf:
		tys, err := d.tys()
		if err != nil {
			return nil, err
		}
		return &Term{K: "self", TyArgs: tys}, nil
	case tagTmCtor:
		h, err := d.hash()
		if err != nil {
			return nil, err
		}
		idx, err := d.u32()
		if err != nil {
			return nil, err
		}
		tys, err := d.tys()
		if err != nil {
			return nil, err
		}
		args, err := d.terms()
		if err != nil {
			return nil, err
		}
		return &Term{K: "ctor", Hash: h, Idx: idx, TyArgs: tys, Args: args}, nil
	case tagTmMatch:
		h, err := d.hash()
		if err != nil {
			return nil, err
		}
		a, err := d.term()
		if err != nil {
			return nil, err
		}
		arms, err := d.terms()
		if err != nil {
			return nil, err
		}
		return &Term{K: "match", Hash: h, A: a, Arms: arms}, nil
	case tagTmRecord:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		out := &Term{K: "record"}
		for i := 0; i < n; i++ {
			name, err := d.str()
			if err != nil {
				return nil, err
			}
			if i > 0 && name <= out.Names[i-1] {
				return nil, d.fail("record fields not strictly ascending: %q after %q", name, out.Names[i-1])
			}
			t, err := d.term()
			if err != nil {
				return nil, err
			}
			out.Names = append(out.Names, name)
			out.Args = append(out.Args, *t)
		}
		return out, nil
	case tagTmField:
		a, err := d.term()
		if err != nil {
			return nil, err
		}
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		return &Term{K: "field", A: a, Op: name}, nil
	}
	return nil, d.fail("unknown Term tag 0x%02x", tag)
}

func (d *dec) terms() ([]Term, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	var out []Term
	for i := 0; i < n; i++ {
		t, err := d.term()
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// decodeDef parses canonical "O1" bytes, rejecting anything malformed or
// non-canonical (unknown tags, bad booleans, unsorted records, trailing
// bytes).
func decodeDef(b []byte) (*Def, error) {
	d := &dec{b: b}
	m0, err := d.u8()
	if err != nil {
		return nil, err
	}
	m1, err := d.u8()
	if err != nil {
		return nil, err
	}
	if m0 != encMagic0 || m1 != encMagic1 {
		return nil, fmt.Errorf("not O1 canonical bytes (magic 0x%02x%02x)", m0, m1)
	}
	tag, err := d.u8()
	if err != nil {
		return nil, err
	}
	var out *Def
	switch tag {
	case tagDefData:
		tv, err := d.u32()
		if err != nil {
			return nil, err
		}
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		def := &Def{K: "data", TyVars: tv}
		for i := 0; i < n; i++ {
			fields, err := d.tys()
			if err != nil {
				return nil, err
			}
			if fields == nil {
				fields = []Ty{}
			}
			def.Ctors = append(def.Ctors, fields)
		}
		out = def
	case tagDefFunc:
		tv, err := d.u32()
		if err != nil {
			return nil, err
		}
		ty, err := d.ty()
		if err != nil {
			return nil, err
		}
		body, err := d.term()
		if err != nil {
			return nil, err
		}
		def := &Def{K: "func", TyVars: tv, Ty: ty, Body: body}
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		for i := 0; i < n; i++ {
			binders, err := d.tys()
			if err != nil {
				return nil, err
			}
			if binders == nil {
				binders = []Ty{}
			}
			pbody, err := d.term()
			if err != nil {
				return nil, err
			}
			def.Props = append(def.Props, Prop{Binders: binders, Body: *pbody})
		}
		out = def
	default:
		return nil, d.fail("unknown Def tag 0x%02x", tag)
	}
	if d.pos != len(d.b) {
		return nil, d.fail("trailing bytes (%d unread)", len(d.b)-d.pos)
	}
	return out, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
