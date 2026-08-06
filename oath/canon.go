package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
)

// canonNaN is the single quiet-NaN bit pattern every NaN collapses to, so that
// (like SMT-LIB's one FP NaN) a NaN has exactly one identity and one encoding.
const canonNaN = 0x7FF8000000000000

// canonFloat normalizes a float64 for the kernel's value/identity model: every
// NaN (any payload, signaling or quiet) becomes the one canonical NaN. Finite
// values, ±0.0, and ±inf are left exactly as-is (−0.0 is a distinct value).
func canonFloat(f float64) float64 {
	if math.IsNaN(f) {
		return math.Float64frombits(canonNaN)
	}
	return f
}

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
	tagTyFloat  = 0x0A
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
	tagTmFloat  = 0x20
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

// propHash is the content address of a PROPERTY on its own: the canonical
// encoding of its binders and body, hashed. Because the function under proof is
// `self` (not a ref) and binders are de Bruijn, a pure algebraic law like
// commutativity `(== (self a b) (self b a))` has the SAME propHash wherever it
// appears — so "which proven definitions satisfy this spec?" is a hash lookup,
// not a search. This is content-addressing applied to specs, not just code.
func propHash(p *Prop) string {
	e := &enc{}
	e.tys(p.Binders)
	e.term(&p.Body)
	s := sha256.Sum256(e.b)
	return hex.EncodeToString(s[:])
}

// generalizeTypes replaces the PRIMITIVE leaf types (Int, Rat, Float, Bool)
// inside a set of binder types with positional type variables, assigned by
// order of first appearance and shared across equal leaves. Structure (fun,
// data, record, rec) is preserved, and data hashes are kept — so `(List Int)`
// becomes `(List t0)` (matching `(List Rat)`), but `List` and `Tree` stay
// distinct. This is the anti-unification that lets a law match across the
// operand types it is polymorphic in.
func generalizeTypes(binders []Ty) []Ty {
	next := 0
	seen := map[string]int{}
	var gen func(t *Ty) Ty
	gen = func(t *Ty) Ty {
		switch t.K {
		case "int", "rat", "float", "bool":
			idx, ok := seen[t.K]
			if !ok {
				idx = next
				next++
				seen[t.K] = idx
			}
			return *tVar(idx)
		case "fun":
			return *tFun(ptrTy(gen(t.A)), ptrTy(gen(t.B)))
		case "data":
			return *tDataTy(t.Hash, genArgs(t.Args, gen))
		case "rec":
			return *tRec(genArgs(t.Args, gen))
		case "record":
			out := *t
			out.Args = genArgs(t.Args, gen)
			return out
		default:
			return *t
		}
	}
	out := make([]Ty, len(binders))
	for i := range binders {
		out[i] = gen(&binders[i])
	}
	return out
}

func genArgs(args []Ty, gen func(*Ty) Ty) []Ty {
	out := make([]Ty, len(args))
	for i := range args {
		out[i] = gen(&args[i])
	}
	return out
}

func ptrTy(t Ty) *Ty { return &t }

// propHashGeneral is the content address of a property with its binder types
// GENERALIZED (generalizeTypes). So commutativity over Int and over Rat — same
// body (which for a pure algebraic law carries no types), binders that both
// generalize to `[t0, t0]` — hash to the same value and match. Exact same-type
// matching still uses propHash; this is the cross-type discovery key.
func propHashGeneral(p *Prop) string {
	e := &enc{}
	e.tys(generalizeTypes(p.Binders))
	e.term(&p.Body)
	s := sha256.Sum256(e.b)
	return hex.EncodeToString(s[:])
}

// tyBytes is the canonical encoding of a single type — used to compare
// signatures (a self-referential property is portable to any definition of the
// same signature, since the function is `self`, not a name).
func tyBytes(t *Ty) []byte {
	e := &enc{}
	e.ty(t)
	return e.b
}

func termBytes(t *Term) []byte {
	e := &enc{}
	e.term(t)
	return e.b
}

// commutativePrims commute for EVERY operand type (structural `==` is symmetric;
// `and`/`or` are non-short-circuiting; `+`/`*` commute even for Float). So
// sorting their operands into a canonical order is a sound rewrite everywhere.
var commutativePrims = map[string]bool{"+": true, "*": true, "==": true, "and": true, "or": true}

// isACPrim reports whether a primitive is associative-AND-commutative for the
// given operand type — so a chain of it can be flattened and re-sorted.
// `and`/`or` are AC over Bool always; `+`/`*` are AC over Int and Rat but NOT
// Float (float addition is not associative — the law examples/float.oath
// falsifies — so flattening a Float chain would be unsound). `==` commutes but
// does not associate, so it is never flattened.
func isACPrim(op string, argTy *Ty) bool {
	switch op {
	case "and", "or":
		return true
	case "+", "*":
		return argTy != nil && (argTy.K == "int" || argTy.K == "rat")
	}
	return false
}

// acFlatten collects the leaves of a chain of one AC operator (already-normalized
// sub-terms of the same op are recursed into).
func acFlatten(op string, args []Term) []Term {
	var out []Term
	for i := range args {
		if args[i].K == "prim" && args[i].Op == op && len(args[i].Args) == 2 {
			out = append(out, acFlatten(op, args[i].Args)...)
		} else {
			out = append(out, args[i])
		}
	}
	return out
}

// acRebuild sorts the leaves and rebuilds a canonical right-nested chain, so any
// association/order of the same leaves yields one form.
func acRebuild(op string, leaves []Term) *Term {
	sort.Slice(leaves, func(i, j int) bool {
		return bytes.Compare(termBytes(&leaves[i]), termBytes(&leaves[j])) < 0
	})
	cur := leaves[len(leaves)-1]
	for i := len(leaves) - 2; i >= 0; i-- {
		cur = Term{K: "prim", Op: op, Args: []Term{leaves[i], cur}}
	}
	return &cur
}

// eNormalize rewrites a term to a canonical form under the confluent algebraic
// rewrite rules: commutativity (canonical operand order) and TYPE-DIRECTED
// associativity (flatten + sort `+`/`*` chains over Int/Rat and `and`/`or` over
// Bool — never Float). It is type-aware, threading the checker and de Bruijn
// context so it knows an operator's operand type. It NEVER affects identity
// (docs/egraph.md): a definition's hash is still the O1 encoding of its ACTUAL
// AST; this only draws equivalence edges between existing objects.
func eNormalize(chk *checkerMachine, ctx []*Ty, t *Term) *Term {
	if t == nil {
		return nil
	}
	push := func(ty *Ty) []*Ty { return append(append([]*Ty{}, ctx...), ty) }
	nt := *t
	switch t.K {
	case "lam":
		nt.A = eNormalize(chk, push(t.Ty), t.A)
	case "let":
		nt.A = eNormalize(chk, ctx, t.A)
		nt.B = eNormalize(chk, push(t.Ty), t.B)
	case "if":
		nt.A = eNormalize(chk, ctx, t.A)
		nt.B = eNormalize(chk, ctx, t.B)
		nt.C = eNormalize(chk, ctx, t.C)
	case "app":
		nt.A = eNormalize(chk, ctx, t.A)
		nt.B = eNormalize(chk, ctx, t.B)
	case "field":
		nt.A = eNormalize(chk, ctx, t.A)
	case "match":
		nt.A = eNormalize(chk, ctx, t.A)
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
			nt.Arms[i] = *eNormalize(chk, armCtx, &t.Arms[i])
		}
	case "prim", "ctor", "record":
		nt.Args = make([]Term, len(t.Args))
		for i := range t.Args {
			nt.Args[i] = *eNormalize(chk, ctx, &t.Args[i])
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

// eHash is the equivalence-class key of a definition: its signature plus its
// e-normalized body. Two definitions with the same eHash compute the same
// function up to the rewrite rules — the same equivalence class, though (by
// design) DIFFERENT identities.
func eHash(st *Store, d *Def) string {
	chk := &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
	e := &enc{}
	e.ty(d.Ty)
	if d.Body != nil {
		e.term(eNormalize(chk, nil, d.Body))
	}
	s := sha256.Sum256(e.b)
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

func (e *enc) u8(v byte)    { e.b = append(e.b, v) }
func (e *enc) u32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *enc) i64(v int64)  { e.b = binary.BigEndian.AppendUint64(e.b, uint64(v)) }
func (e *enc) str(s string) { e.u32(uint32(len(s))); e.b = append(e.b, s...) }

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

// ty emits a type's canonical bytes ITERATIVELY (#149).
//
// The claim that made this look safe was WRONG, and self-caught while testing
// dec.ty: Ty depth is capped at maxSyntaxNesting only for ELABORATED defs, and a
// DECODED def never passed the reader. hashDef runs on every stored object,
// including bundle imports — so a structure the profile ADMITS (65,536 nodes, all
// of them nested arrows) overflowed the host stack while being hashed.
// Reproduced under a 1MB stack limit before this change.
//
// The lesson is the one this repo keeps relearning: a bound cited for one path
// does not transfer to another that reaches the same code by a different route.
func (e *enc) ty(root *Ty) {
	stack := []encTyItem{{ty: root}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if it.name != nil {
			e.str(*it.name)
			continue
		}
		t := it.ty
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
			stack = append(stack, encTyItem{ty: t.B}, encTyItem{ty: t.A})
		case "data":
			e.u8(tagTyData)
			e.hash(t.Hash)
			e.u32(uint32(len(t.Args)))
			for i := len(t.Args) - 1; i >= 0; i-- {
				stack = append(stack, encTyItem{ty: &t.Args[i]})
			}
		case "rec":
			e.u8(tagTyRec)
			e.u32(uint32(len(t.Args)))
			for i := len(t.Args) - 1; i >= 0; i-- {
				stack = append(stack, encTyItem{ty: &t.Args[i]})
			}
		case "record":
			e.u8(tagTyRecord)
			e.u32(uint32(len(t.Names)))
			for i := len(t.Names) - 1; i >= 0; i-- {
				stack = append(stack, encTyItem{ty: &t.Args[i]})
				n := t.Names[i]
				stack = append(stack, encTyItem{name: &n})
			}
		default:
			panic("encode: unknown Ty kind " + t.K)
		}
	}
}

// encTyItem is one pending type emission: a type, or a field name that must
// appear before its type.
type encTyItem struct {
	ty   *Ty
	name *string
}

func (e *enc) tys(ts []Ty) {
	e.u32(uint32(len(ts)))
	for i := range ts {
		e.ty(&ts[i])
	}
}

// encItem is one pending emission for the iterative encoder: a term, a literal
// string, or a length prefix that must appear AFTER an earlier child's bytes.
type encItem struct {
	kind byte // 't' term | 's' string | 'n' u32
	term *Term
	s    string
	n    uint32
}

// term emits a term's canonical bytes ITERATIVELY (#149).
//
// This is identity-critical code, so the conversion is byte-preserving by
// construction rather than by intent: every case emits exactly the bytes the
// recursive version emitted, and children are PUSHED IN REVERSE so they pop in
// source order. canon_iterative_test.go compares the two byte-for-byte over the
// corpus and over deep adversarial structures — hash equality would be a weaker
// claim than the one being made, which is that the ENCODING did not move.
//
// WHY TERMS AND NOT TYPES. A Ty's depth is bounded by maxSyntaxNesting (512),
// because type expressions can only nest as far as the source nests. A TERM's
// depth is not: `Str` is an inductive datatype, so a 5,000-rune literal is a
// 5,000-long SCons spine from one syntax node. Linear spines are the shape that
// escapes the syntax bound, which is why they are the shape that needed this.
func (e *enc) term(root *Term) {
	stack := []encItem{{kind: 't', term: root}}
	push := func(it encItem) { stack = append(stack, it) }
	pushTerm := func(t *Term) { push(encItem{kind: 't', term: t}) }

	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch it.kind {
		case 's':
			e.str(it.s)
			continue
		case 'n':
			e.u32(it.n)
			continue
		}
		t := it.term
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
		case "float":
			// IEEE-754 binary64, 8 big-endian bytes, with NaN canonicalized to a
			// single quiet pattern (canonFloat) so one value has one encoding.
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
			pushTerm(t.A)
		case "app":
			e.u8(tagTmApp)
			pushTerm(t.B)
			pushTerm(t.A)
		case "let":
			e.u8(tagTmLet)
			e.ty(t.Ty)
			pushTerm(t.B)
			pushTerm(t.A)
		case "if":
			e.u8(tagTmIf)
			pushTerm(t.C)
			pushTerm(t.B)
			pushTerm(t.A)
		case "prim":
			e.u8(tagTmPrim)
			e.str(t.Op)
			e.u32(uint32(len(t.Args)))
			for i := len(t.Args) - 1; i >= 0; i-- {
				pushTerm(&t.Args[i])
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
			for i := len(t.Args) - 1; i >= 0; i-- {
				pushTerm(&t.Args[i])
			}
		case "match":
			// The arm COUNT is emitted after the scrutinee's bytes, so it is a
			// work item rather than an immediate write.
			e.u8(tagTmMatch)
			e.hash(t.Hash)
			for i := len(t.Arms) - 1; i >= 0; i-- {
				pushTerm(&t.Arms[i])
			}
			push(encItem{kind: 'n', n: uint32(len(t.Arms))})
			pushTerm(t.A)
		case "record":
			e.u8(tagTmRecord)
			e.u32(uint32(len(t.Names)))
			for i := len(t.Names) - 1; i >= 0; i-- {
				pushTerm(&t.Args[i])
				push(encItem{kind: 's', s: t.Names[i]})
			}
		case "field":
			e.u8(tagTmField)
			push(encItem{kind: 's', s: t.Op})
			pushTerm(t.A)
		default:
			panic("encode: unknown Term kind " + t.K)
		}
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

	// nodes counts canonical nodes AS THEY ARE CONSTRUCTED (#149). The decoder
	// is a PRE-ADMISSION constructor: admitDef bounds a structure that already
	// exists, and this is what brings it into existence, so a budget checked
	// afterwards cannot protect it. Refusal happens at the node that crosses
	// the limit, before the rest of the object is built.
	nodes int
}

// countNodes charges n nodes against the portable profile and refuses the moment
// the budget is crossed. Every decoded node charges exactly ONE.
func (d *dec) countNodes(n int) error {
	d.nodes += n
	if d.nodes > maxCanonicalNodes {
		return errTooManyNodes()
	}
	return nil
}

// reserveNodes checks that a declared collection length COULD fit, WITHOUT
// charging for it.
//
// The distinction is the accepted domain, not an optimisation. Charging the
// length and then charging each child as it decodes bills a wide structure
// TWICE: a prim with 32,768 leaf arguments is 32,769 nodes and was refused near
// the limit, so the decoder accepted less than the profile documents and a valid
// object became undecodable. Reserving preserves the boundary while still
// refusing a header that claims millions of elements before make() reserves the
// memory — u32 caps a length at 1<<24 and a Term is well over a hundred bytes,
// so one crafted header would otherwise reserve gigabytes.
func (d *dec) reserveNodes(n int) error {
	if d.nodes+n > maxCanonicalNodes {
		return errTooManyNodes()
	}
	return nil
}

func (d *dec) fail(f string, a ...any) error {
	return fmt.Errorf("O1 decode @%d: %s", d.pos, fmt.Sprintf(f, a...))
}

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
	// Reject NEGATIVE ZERO. sign=1 with an empty magnitude decodes to the same value
	// as the canonical sign=0 form, so two byte sequences would mean one integer — and
	// since identity is the hash of these bytes, that is two content-addressed
	// identities for 0.
	//
	// The kernel never PRODUCES this (encoding is canonical), which is exactly why it
	// survived: the gap is only reachable by bytes that did not come from an encoder.
	// The store's load path validates sha256(bytes)==name and typechecks, deliberately
	// not encode(decode(b))==b, and its own comment cites the hosted-store threat model
	// as the reason to re-validate. A hand-crafted negative zero therefore loaded at a
	// distinct hash in precisely the threat model that check exists to defend against.
	//
	// The blind Rust kernel rejected this from the spec text alone (DIVERGENCES #76).
	// Conformance passed anyway because the reject corpus exercises SOURCE-level
	// rejects and has no hostile-object-bytes class — an obligation nothing witnessed.
	if sign == 1 && n == 0 {
		return nil, d.fail("non-canonical integer (negative zero): sign=1 with an empty magnitude is a second encoding of 0")
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

// decTyTask mirrors decTask for types: a destination to write into, or a
// record entry whose NAME is read before its type.
type decTyTask struct {
	kind byte // 't' decode a type into dst | 'r' record entry
	dst  *Ty
	node *Ty
	idx  int
}

// ty decodes one type ITERATIVELY, charging nodes as it builds (#149).
//
// This was the LAST pre-admission recursion. dec.term was repaired first because
// its exposure is cheaper to reach — `Str` makes a 5,000-rune literal a
// 5,000-deep spine from flat source — but a type carries no such sugar, so
// hostile depth here needs genuinely nested BYTES. Narrower, equally
// pre-admission: the reader never saw these bytes, so nothing upstream bounds
// them, and a budget checked after construction cannot protect the construction.
//
// tys() stays a loop: it calls ty() once per element, and each call handles its
// own nesting internally, so depth does not accumulate across a list.
func (d *dec) ty() (*Ty, error) {
	root := &Ty{}
	stack := []decTyTask{{kind: 't', dst: root}}
	for len(stack) > 0 {
		task := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		push := func(t decTyTask) { stack = append(stack, t) }

		if task.kind == 'r' {
			name, err := d.str()
			if err != nil {
				return nil, err
			}
			if task.idx > 0 && name <= task.node.Names[task.idx-1] {
				return nil, d.fail("record fields not strictly ascending: %q after %q",
					name, task.node.Names[task.idx-1])
			}
			task.node.Names[task.idx] = name
			push(decTyTask{kind: 't', dst: &task.node.Args[task.idx]})
			continue
		}

		dst := task.dst
		if err := d.countNodes(1); err != nil {
			return nil, err
		}
		tag, err := d.u8()
		if err != nil {
			return nil, err
		}
		switch tag {
		case tagTyInt:
			*dst = Ty{K: "int"}
		case tagTyRat:
			*dst = Ty{K: "rat"}
		case tagTyFloat:
			*dst = Ty{K: "float"}
		case tagTyBool:
			*dst = Ty{K: "bool"}
		case tagTyVar:
			v, err := d.u32()
			if err != nil {
				return nil, err
			}
			*dst = Ty{K: "var", Var: v}
		case tagTyFun:
			*dst = Ty{K: "fun", A: &Ty{}, B: &Ty{}}
			push(decTyTask{kind: 't', dst: dst.B})
			push(decTyTask{kind: 't', dst: dst.A})
		case tagTyData:
			h, err := d.hash()
			if err != nil {
				return nil, err
			}
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			*dst = Ty{K: "data", Hash: h, Args: make([]Ty, n)}
			for i := n - 1; i >= 0; i-- {
				push(decTyTask{kind: 't', dst: &dst.Args[i]})
			}
		case tagTyRec:
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			*dst = Ty{K: "rec", Args: make([]Ty, n)}
			for i := n - 1; i >= 0; i-- {
				push(decTyTask{kind: 't', dst: &dst.Args[i]})
			}
		case tagTyRecord:
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			*dst = Ty{K: "record", Names: make([]string, n), Args: make([]Ty, n)}
			for i := n - 1; i >= 0; i-- {
				push(decTyTask{kind: 'r', node: dst, idx: i})
			}
		default:
			return nil, d.fail("unknown Ty tag 0x%02x", tag)
		}
	}
	return root, nil
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

// decTask is one pending decode step. `dst` is where the decoded term will be
// WRITTEN, which is what removes attach logic: a child never has to find its
// parent, because its destination was chosen when the parent was built.
//
// Destinations stay valid because child slices are ALLOCATED IN FULL once their
// count is known. Appending as children complete would reallocate and invalidate
// pointers into earlier elements — a bug that would corrupt decoded structures
// only for terms wide enough to trigger a grow, which is precisely the size
// range this rewrite exists to handle.
type decTask struct {
	kind byte  // 't' decode a term into dst | 'm' match-arms | 'r' record entry
	dst  *Term // kind 't'
	node *Term // kinds 'm' and 'r': the parent being filled
	idx  int   // kind 'r': which entry
}

// term decodes one term ITERATIVELY, counting canonical nodes AS IT BUILDS
// (#149).
//
// THIS IS A PRE-ADMISSION CONSTRUCTOR, which is why it is stricter than the
// other repaired walkers. admitDef bounds a structure that already EXISTS; the
// decoder is what brings it into existence, so a budget checked afterwards
// cannot protect it. A crafted stored object overflowed the host stack while
// being decoded, before there was anything to count. Refusal therefore happens
// DURING construction, at the node that crosses the limit.
//
// The recursive decoder remains as the differential oracle for VALID objects. It
// cannot be the oracle for deep hostile ones: surviving those is the defect, so
// for them the authority is the encoding format plus the portable profile's
// refusal contract, not the old implementation's behaviour.
func (d *dec) term() (*Term, error) {
	root := &Term{}
	stack := []decTask{{kind: 't', dst: root}}
	for len(stack) > 0 {
		task := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		push := func(t decTask) { stack = append(stack, t) }

		switch task.kind {
		case 'm':
			// The arm COUNT is encoded after the scrutinee's bytes, so it is
			// read here rather than when the match node was created.
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			task.node.Arms = make([]Term, n)
			for i := n - 1; i >= 0; i-- {
				push(decTask{kind: 't', dst: &task.node.Arms[i]})
			}
			continue
		case 'f':
			// A field access encodes its NAME after the record's bytes.
			name, err := d.str()
			if err != nil {
				return nil, err
			}
			task.node.Op = name
			continue
		case 'r':
			name, err := d.str()
			if err != nil {
				return nil, err
			}
			if task.idx > 0 && name <= task.node.Names[task.idx-1] {
				return nil, d.fail("record fields not strictly ascending: %q after %q",
					name, task.node.Names[task.idx-1])
			}
			task.node.Names[task.idx] = name
			push(decTask{kind: 't', dst: &task.node.Args[task.idx]})
			continue
		}

		dst := task.dst
		if err := d.countNodes(1); err != nil {
			return nil, err
		}
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
			*dst = Term{K: "var", Idx: v}
		case tagTmInt:
			v, err := d.bigint()
			if err != nil {
				return nil, err
			}
			*dst = Term{K: "int", Int: v}
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
			*dst = Term{K: "rat", Rat: new(big.Rat).SetFrac(num, den)}
		case tagTmFloat:
			if d.pos+8 > len(d.b) {
				return nil, d.fail("unexpected end in float")
			}
			bits := binary.BigEndian.Uint64(d.b[d.pos : d.pos+8])
			d.pos += 8
			// Strict canonical form: any NaN must be THE canonical NaN. Other NaN
			// payloads (or signaling NaNs) are rejected, exactly as a non-reduced
			// rational is — one value, one encoding.
			if math.IsNaN(math.Float64frombits(bits)) && bits != canonNaN {
				return nil, d.fail("non-canonical NaN bit pattern 0x%016x", bits)
			}
			*dst = Term{K: "float", Float: math.Float64frombits(bits)}
		case tagTmBool:
			v, err := d.u8()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, d.fail("bool byte 0x%02x", v)
			}
			*dst = Term{K: "bool", Bool: v == 1}
		case tagTmLam:
			ty, err := d.ty()
			if err != nil {
				return nil, err
			}
			*dst = Term{K: "lam", Ty: ty}
			push(decTask{kind: 't', dst: newChild(&dst.A)})
		case tagTmApp:
			*dst = Term{K: "app"}
			b := newChild(&dst.B)
			a := newChild(&dst.A)
			push(decTask{kind: 't', dst: b})
			push(decTask{kind: 't', dst: a})
		case tagTmLet:
			ty, err := d.ty()
			if err != nil {
				return nil, err
			}
			*dst = Term{K: "let", Ty: ty}
			b := newChild(&dst.B)
			a := newChild(&dst.A)
			push(decTask{kind: 't', dst: b})
			push(decTask{kind: 't', dst: a})
		case tagTmIf:
			*dst = Term{K: "if"}
			c := newChild(&dst.C)
			b := newChild(&dst.B)
			a := newChild(&dst.A)
			push(decTask{kind: 't', dst: c})
			push(decTask{kind: 't', dst: b})
			push(decTask{kind: 't', dst: a})
		case tagTmPrim:
			op, err := d.str()
			if err != nil {
				return nil, err
			}
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			*dst = Term{K: "prim", Op: op, Args: make([]Term, n)}
			for i := n - 1; i >= 0; i-- {
				push(decTask{kind: 't', dst: &dst.Args[i]})
			}
		case tagTmRef:
			h, err := d.hash()
			if err != nil {
				return nil, err
			}
			tys, err := d.tys()
			if err != nil {
				return nil, err
			}
			*dst = Term{K: "ref", Hash: h, TyArgs: tys}
		case tagTmSelf:
			tys, err := d.tys()
			if err != nil {
				return nil, err
			}
			*dst = Term{K: "self", TyArgs: tys}
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
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			*dst = Term{K: "ctor", Hash: h, Idx: idx, TyArgs: tys, Args: make([]Term, n)}
			for i := n - 1; i >= 0; i-- {
				push(decTask{kind: 't', dst: &dst.Args[i]})
			}
		case tagTmMatch:
			h, err := d.hash()
			if err != nil {
				return nil, err
			}
			*dst = Term{K: "match", Hash: h}
			push(decTask{kind: 'm', node: dst})
			push(decTask{kind: 't', dst: newChild(&dst.A)})
		case tagTmRecord:
			n, err := d.u32()
			if err != nil {
				return nil, err
			}
			if err := d.reserveNodes(n); err != nil {
				return nil, err
			}
			*dst = Term{K: "record", Names: make([]string, n), Args: make([]Term, n)}
			for i := n - 1; i >= 0; i-- {
				push(decTask{kind: 'r', node: dst, idx: i})
			}
		case tagTmField:
			*dst = Term{K: "field"}
			// The field NAME follows the record's bytes, so it is read by a
			// deferred entry rather than here.
			push(decTask{kind: 'f', node: dst})
			push(decTask{kind: 't', dst: newChild(&dst.A)})
		default:
			return nil, d.fail("unknown Term tag 0x%02x", tag)
		}
	}
	return root, nil
}

// newChild allocates a child slot and returns a stable pointer to it.
func newChild(slot **Term) *Term {
	*slot = &Term{}
	return *slot
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
// decodeDefRaw decodes canonical bytes into a Def. The driver lives on `dec` so
// its progress is observable: a test asserting that an oversized object is
// refused DURING construction has to be able to see how far the decoder got.
func decodeDefRaw(b []byte) (*Def, error) {
	return (&dec{b: b}).def()
}

func (d *dec) def() (*Def, error) {
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

// decodeDef wraps decoding with ADMISSION (#149), for the same reason
// elaboration does: this is where a Def is CONSTRUCTED from external bytes.
//
// GetDef checked admission itself, which missed the bundle-import path —
// registry.go decodes, typechecks and calls StoreObject directly, so an
// oversized bundle object entered the cache and every later GetDef returned it
// from there without ever reaching that check. Found by external review.
//
// NOTE what this does NOT fix: decodeDefRaw is still recursive, so a crafted
// object can overflow while being DECODED, before there is a structure to
// count. That is the pre-admission ordering defect recorded on #149; admission
// here bounds what is ADMITTED, not what is CONSTRUCTED.
func decodeDef(b []byte) (*Def, error) {
	d, err := decodeDefRaw(b)
	if err != nil {
		return d, err
	}
	return d, admitDef(d)
}
