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

// compareTermsCanonical returns exactly what
// bytes.Compare(termBytes(a), termBytes(b)) returns, without materialising
// either encoding past the first byte at which they differ (#151).
//
// WHY NOT CACHE EACH SUBTREE'S BYTES INSTEAD. The obvious repair — encode every
// normalized node once and keep the result — is itself quadratic: in a chain of
// depth d the node at level i encodes to O(d-i) bytes, so the cache alone is
// O(d²) and the memory is the very thing being repaired. What is quadratic here
// is not that bytes are recomputed but that WHOLE SUBTREES are encoded to decide
// an ordering that is almost always settled by the first byte. `==` over a chain
// compares a leaf against the rest of the chain at every level; the tags differ
// immediately, and today both sides are encoded in full to discover that.
//
// The doubling is what keeps it faithful AND cheap. Encoding is re-run rather
// than resumed, so the total work is a geometric series in the position of the
// first difference — at most twice the bytes a single comparison genuinely
// needs. Across a whole term the bound is the standard smaller-of-two-subtrees
// argument: a comparison costs the SHORTER encoding, and no node can be its own
// sibling's size all the way down without the term growing exponentially.
//
// EXACT FIDELITY IS BY CONSTRUCTION, not by a re-derived comparator. This runs
// the real encoder and stops it; it does not walk the two terms deciding what
// their encodings would have compared like. A hand-written structural
// comparator would be a second implementation of the byte layout — the thing
// this repository has already learned to refuse — and it would silently disagree
// wherever a length prefix orders differently from the field it prefixes.
func compareTermsCanonical(a, b *Term) int {
	for limit := 64; ; limit *= 2 {
		ea, eb := &enc{limit: limit}, &enc{limit: limit}
		ea.term(a)
		eb.term(b)
		n := len(ea.b)
		if len(eb.b) < n {
			n = len(eb.b)
		}
		if c := bytes.Compare(ea.b[:n], eb.b[:n]); c != 0 {
			return c
		}
		// Only the bytes BOTH sides have produced can decide anything, so the
		// result is settled solely when neither side has more to write.
		//
		// An earlier version also returned early when one side was complete and
		// the other merely truncated past that length, on the argument that a
		// complete encoding whose bytes all agree must be a proper prefix and so
		// compares less. That is true — the encoding is a self-delimiting TLV
		// tree, so no complete encoding is a proper prefix of another and the
		// case cannot arise at all. Which is exactly why it was deleted: it was
		// unreachable code whose correctness rested on an argument about the
		// format, and inverting it changed no test. Looping instead is correct
		// whether or not that argument holds, and costs at most one extra
		// doubling on a case that never occurs.
		if !ea.truncated && !eb.truncated {
			return bytes.Compare(ea.b, eb.b)
		}
	}
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

// ---------- unit, idempotence and involution rules ----------
//
// The second rung of the rewrite set. Commutativity and associativity reorder a
// chain; these REMOVE structure from it, so a body carrying an identity element,
// a duplicated Bool operand, or a doubled `neg` lands in the same equivalence
// class as the body it is equal to.
//
// Every rule below is TYPE-DIRECTED, and the direction is exact rather than
// approximate — see acUnitLeaf for why the leaf's own kind is sufficient
// evidence of the chain's operand type.

var (
	acOneInt = big.NewInt(1)
	acOneRat = big.NewRat(1, 1)
)

// acUnitLeaf reports whether a leaf of an `op` chain is that operator's IDENTITY
// ELEMENT, and may therefore be dropped.
//
// TYPE DIRECTION IS CARRIED BY THE LEAF'S OWN KIND, and that is exact, not a
// shortcut: `numericTy` (check.go) refuses operands of mixed numeric type and
// the language has no numeric coercion, so an `int`-kinded literal inside a `+`
// proves the whole chain is Int-typed. A Float chain cannot smuggle an `int` 0
// past this test, because such a term does not typecheck.
//
// 0 IS DELIBERATELY NOT A FLOAT ADDITIVE IDENTITY. `-0.0 + 0.0` is `+0.0`, a
// DISTINCT value under the kernel's Leibniz `==`, so dropping a Float `+ 0.0`
// would merge two definitions that disagree on an input. Float `+` never reaches
// this function through the AC path (isACPrim is false for it), and the float
// case below is confined to `*`, where `x * 1.0` is `x` for every IEEE value:
// ±0.0 keeps its sign, ±inf is unchanged, and NaN stays NaN — which is an
// identity here rather than a near-miss only because the kernel canonicalizes
// every NaN to one bit pattern (canonFloat).
func acUnitLeaf(op string, t *Term) bool {
	switch op {
	case "+":
		switch t.K {
		case "int":
			return t.Int != nil && t.Int.Sign() == 0
		case "rat":
			return t.Rat != nil && t.Rat.Sign() == 0
		}
	case "*":
		switch t.K {
		case "int":
			return t.Int != nil && t.Int.Cmp(acOneInt) == 0
		case "rat":
			return t.Rat != nil && t.Rat.Cmp(acOneRat) == 0
		case "float":
			// Exactly 1.0. -1.0 and NaN both fail this comparison.
			return t.Float == 1
		}
	case "and":
		return t.K == "bool" && t.Bool
	case "or":
		return t.K == "bool" && !t.Bool
	}
	return false
}

// idempotentPrims are the operators for which `op(x, x)` is `x`. Bool `and` and
// `or` only — `+` and `*` are NOT idempotent (`a + a` is `2a`), which is a
// distinction a rule keyed on "same operand twice" would lose.
var idempotentPrims = map[string]bool{"and": true, "or": true}

// acDropUnits removes the identity-element leaves of an `op` chain.
//
// If EVERY leaf is the identity then the chain IS the identity, so one of the
// dropped leaves is kept — that leaf rather than a freshly constructed literal,
// because it already carries the operand type. An Int 0 and a Rat 0 are
// different terms with different bytes, and only the input says which one this
// chain was written at.
func acDropUnits(op string, leaves []Term) []Term {
	kept := make([]Term, 0, len(leaves))
	for i := range leaves {
		if !acUnitLeaf(op, &leaves[i]) {
			kept = append(kept, leaves[i])
		}
	}
	if len(kept) == 0 {
		return []Term{leaves[0]}
	}
	return kept
}

// mulUnitSurvivor returns the operand that survives `x * 1.0` over FLOAT, or nil
// when the rule does not apply.
//
// Int and Rat never reach here: their `*` chains are associative-commutative, so
// their unit is dropped by acDropUnits inside acRebuild. Float `*` commutes but
// does not associate, so it is never flattened and needs the rule stated at the
// node — the one place where "which type is this?" cannot be read off a chain,
// which is why this one takes the synthesized operand type and refuses to fire
// without it.
func mulUnitSurvivor(op string, argTy *Ty, args []Term) *Term {
	if op != "*" || argTy == nil || argTy.K != "float" || len(args) != 2 {
		return nil
	}
	if acUnitLeaf("*", &args[0]) {
		return &args[1]
	}
	if acUnitLeaf("*", &args[1]) {
		return &args[0]
	}
	return nil
}

// negInvolution returns the operand of a doubled `neg`, or nil.
//
// THERE IS NO OPERAND TYPE TO DIRECT ON, which is why this takes no Ty and is
// stated as an absence rather than left unsaid: `neg`'s typing rule admits Int,
// Rat and Float and nothing else (check.go's numericTy), and the rule is sound
// on all three. Flipping a sign twice restores the exact value, and for Float
// that includes ±0.0, ±inf and NaN — NaN's sign bit carries no identity because
// the kernel canonicalizes every NaN to one pattern.
func negInvolution(op string, args []Term) *Term {
	if op != "neg" || len(args) != 1 {
		return nil
	}
	in := args[0]
	if in.K == "prim" && in.Op == "neg" && len(in.Args) == 1 {
		return &in.Args[0]
	}
	return nil
}

// ---------- control-flow and binding rules ----------
//
// The third rung. These are restricted by Oath's STRICT EVALUATION ORDER and by
// BINDING STRUCTURE rather than by any operand type's algebra, so unlike the
// unit rules above they carry no Float carve-out and hold at every well-formed
// type.

// ifSelect returns the term a normalized `if` node reduces to, or nil.
//
// TWO RULES, AND THE SECOND IS THE RESTRICTED ONE. A constant condition selects
// its branch unconditionally: the other branch was never going to be evaluated,
// so discarding it removes no work whatever it contains — which is why the
// branches carry no side condition of their own.
//
// Identical branches may drop the condition ONLY IF THE CONDITION IS ALREADY A
// VALUE — a `var`, which call-by-value has already bound to a value, or a Bool
// literal. eval.go evaluates an `if` condition before selecting a branch, so a
// condition that is a COMPUTATION is part of what the term does: dropping a
// divergent one turns a non-terminating term into a terminating one. That is
// removing divergence, not preserving meaning.
func ifSelect(t *Term) *Term {
	if t.A == nil || t.B == nil || t.C == nil {
		return nil
	}
	if t.A.K == "bool" {
		if t.A.Bool {
			return t.B
		}
		return t.C
	}
	if t.A.K == "var" && compareTermsCanonical(t.B, t.C) == 0 {
		return t.B
	}
	return nil
}

// etaReduce returns the term a normalized `lam` node eta-reduces to, or nil.
//
// `fn x. (f x)` is `f` only under conditions that fail in three different ways,
// and the ADMITTED HEAD SET IS BOUNDED BY COST AS WELL AS BY SEMANTICS:
//
//   - THE ARGUMENT MUST BE EXACTLY THE REMOVED BINDER (`var` 0). `fn x. (f y)`
//     is not an eta redex at all.
//   - THE HEAD MUST BE A VALUE, because the left side is a value whatever `f`
//     is, while the right side evaluates `f` immediately — a head that diverges
//     would MOVE divergence from application time to construction time.
//   - THE BINDER MUST NOT OCCUR FREE IN THE HEAD, which is not about evaluation
//     at all: removing the binder shifts every free index, and an occurrence of
//     the binder itself has no index to shift to.
//
// ONLY HEADS OF CONSTANT SIZE ARE ADMITTED, AND THAT IS THE WHOLE REASON THE
// LAST TWO CONDITIONS ARE CHEAP TO DECIDE HERE RATHER THAN BY A TRAVERSAL:
//
//	var   one node. Its index IS the freeness question — index 0 is the removed
//	      binder itself and is rejected — and the shift is one decrement.
//	ref   one node carrying a hash and TYPE arguments, so it contains no term de
//	      Bruijn index at all: the binder cannot occur free in it and the shift
//	      is vacuous. It is not a value by KIND — eval.go's `ref` case evaluates
//	      the referenced body — so it is admitted only when a store lookup shows
//	      that body BEGINS WITH `lam`, which makes evaluating it a closure
//	      construction. A nullary definition's body is not a lam, and its ref can
//	      diverge.
//
// A `lam` HEAD IS EQUALLY SOUND AND IS DELIBERATELY NOT ADMITTED. It is
// unbounded: deciding freeness and shifting indices inside it means walking a
// subtree that contains the rest of the term, which is quadratic on the tower
// `fn x. ((fn y. …) x)` — measured at ×3.9 per doubling and 1.8 GB at depth
// 4000, on a term the portable profile admits and `find --equiv` normalizes.
// RE-ADMITTING IT REQUIRES MAKING FREENESS AND SHIFTING O(1) FIRST — a
// min-free-index attribute computed during normalization, and shifts
// accumulated through a chain of etas rather than applied per level — not
// merely re-establishing that the rewrite is sound, which it already is.
// TestEtaNormalizationScalesSubQuadraticallyWithTowerDepth is what holds that
// line; it was watched failing against the lam-head implementation.
func etaReduce(st *Store, lam *Term) *Term {
	if lam.A == nil {
		return nil
	}
	b := lam.A
	if b.K != "app" || b.A == nil || b.B == nil {
		return nil
	}
	if b.B.K != "var" || b.B.Idx != 0 {
		return nil
	}
	h := b.A
	switch h.K {
	case "var":
		if h.Idx == 0 {
			// The head IS the binder being removed — a free occurrence, with
			// nothing to shift it to.
			return nil
		}
		r := *h
		r.Idx = h.Idx - 1
		return &r
	case "ref":
		if st == nil {
			return nil
		}
		d, err := st.GetDef(h.Hash)
		if err != nil || d.Body == nil || d.Body.K != "lam" {
			return nil
		}
		r := *h
		return &r
	}
	return nil
}

// acFlatten collects the leaves of a chain of one AC operator (already-normalized
// sub-terms of the same op are recursed into).
// acFlatten collects the leaves of an associative-commutative chain,
// ITERATIVELY (#149).
//
// A normalized `and`/`or`/`+`/`*` chain is as deep as the term that produced it,
// so this mapped an admitted 65,536-node structure onto the host stack from
// `find --equiv`. The structural gate did not see it: its detector recognised a
// selector directly on a parameter and this descends `args[i].Args`, two steps
// away. Found by external review; the detector was widened in the same commit.
//
// Children are pushed in REVERSE so leaves come out in source order, which is
// what acRebuild then sorts — a reordering here would be invisible after the
// sort for most inputs and visible for any chain containing equal-comparing
// leaves.
func acFlatten(op string, args []Term) []Term {
	var out []Term
	stack := make([]Term, 0, len(args))
	for i := len(args) - 1; i >= 0; i-- {
		stack = append(stack, args[i])
	}
	for len(stack) > 0 {
		a := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if a.K == "prim" && a.Op == op && len(a.Args) == 2 {
			stack = append(stack, a.Args[1], a.Args[0])
			continue
		}
		out = append(out, a)
	}
	return out
}

// acRebuild sorts the leaves and rebuilds a canonical right-nested chain, so any
// association/order of the same leaves yields one form.
//
// Each leaf is encoded ONCE into a sort key rather than re-encoded inside the
// comparator (#151). The comparator ran O(k log k) times and encoded both of its
// arguments every time, so a k-leaf chain paid O(k log k) encodings for O(k)
// distinct leaves. The ORDER is unchanged: the keys are the same bytes the
// comparator computed, and leaves that tie are byte-identical, so which of them
// an unstable sort puts first cannot be observed in the output.
//
// THE UNIT AND IDEMPOTENCE RULES ARE APPLIED HERE BECAUSE THIS IS THE ONE PLACE
// THAT SEES A WHOLE CHAIN. Every route into an AC normal form — the chain root
// in eNormalize, forceAC materialising a deferred interior, and the retained
// recursive oracle — arrives through this function on the output of acFlatten,
// so a rule stated here is a rule those three cannot state differently. Applying
// it per NODE instead would be the same defect the flatten already avoids: the
// leaf multiset is a property of the maximal chain, not of any level in it.
//
// Dropping runs BEFORE keys are computed, so a dropped leaf is never encoded.
// Deduplication runs AFTER the sort, where equal leaves are adjacent and the
// comparison is the same canonical bytes the ordering already used — never a
// second notion of "the same leaf".
func acRebuild(op string, leaves []Term) *Term {
	leaves = acDropUnits(op, leaves)
	keyed := make([]struct {
		t   Term
		key []byte
	}, len(leaves))
	for i := range leaves {
		keyed[i].t = leaves[i]
		keyed[i].key = termBytes(&leaves[i])
	}
	sort.Slice(keyed, func(i, j int) bool {
		return bytes.Compare(keyed[i].key, keyed[j].key) < 0
	})
	if idempotentPrims[op] {
		w := 1
		for i := 1; i < len(keyed); i++ {
			if bytes.Equal(keyed[i].key, keyed[w-1].key) {
				continue
			}
			keyed[w] = keyed[i]
			w++
		}
		keyed = keyed[:w]
	}
	cur := keyed[len(keyed)-1].t
	for i := len(keyed) - 2; i >= 0; i-- {
		cur = Term{K: "prim", Op: op, Args: []Term{keyed[i].t, cur}}
	}
	return &cur
}

// eNormalize rewrites a term to a canonical form under the confluent algebraic
// rewrite rules: commutativity (canonical operand order), TYPE-DIRECTED
// associativity (flatten + sort `+`/`*` chains over Int/Rat and `and`/`or` over
// Bool — never Float), the structure-REMOVING rules above — identity elements
// (`+ 0` over Int/Rat, `* 1` over Int/Rat/Float, `and true`, `or false`), Bool
// idempotence, and `neg` involution — and the control-flow and binding rules:
// constant-condition `if`, identical branches under a value condition, and eta
// over a value head. It is type-aware, threading the checker and de Bruijn
// context so it knows an operator's operand type. It NEVER affects identity
// (docs/egraph.md): a definition's hash is still the O1 encoding of its ACTUAL
// AST; this only draws equivalence edges between existing objects.
// ctxList is a PERSISTENT binder context. Extending is O(1) and sharing is safe
// because no node is ever mutated.
//
// The obvious representation — copy the whole []*Ty per binder — makes an
// iterative traversal quadratic anyway: a valid 10,000-binder spine allocated
// roughly 400MB, and a profile-edge spine gigabytes, so `find --equiv` stayed
// resource-exhaustible after the recursion was removed. Removing host recursion
// does not by itself bound the work per node. Found by external review.
//
// A slice is materialised only where chk.synth actually needs one, which is once
// per prim and once per match rather than once per binder.
type ctxList struct {
	ty     *Ty
	parent *ctxList
	depth  int

	// materialised caches the slice form. A node is immutable once built, so
	// the cache is safe to share across every consumer of that context.
	//
	// Caching is not an optimisation. Without it the copy merely MOVED from
	// per-binder to per-synth-call: a term with ~10,000 lambdas and ~20,000
	// commutative primitives beneath them is inside the profile and still
	// allocated gigabytes, because each primitive materialised the whole
	// context. Found by external review, on the commit that fixed the
	// per-binder copy.
	materialised []*Ty
}

func ctxExtend(c *ctxList, ty *Ty) *ctxList {
	d := 1
	if c != nil {
		d = c.depth + 1
	}
	return &ctxList{ty: ty, parent: c, depth: d}
}

func ctxSlice(c *ctxList) []*Ty {
	if c == nil {
		return nil
	}
	if c.materialised != nil {
		return c.materialised
	}
	out := make([]*Ty, c.depth)
	for n, i := c, c.depth-1; n != nil; i-- {
		out[i] = n.ty
		n = n.parent
	}
	c.materialised = out
	return out
}

// eNormItem is one pending normalization. `exit` marks the second visit to a
// node whose children are done — only `prim` needs one, for AC normalization.
//
// chainOp is the operator of this node's PARENT, when that parent is a
// commutative primitive with two arguments — that is, when this node sits
// somewhere a same-op chain could continue (#151). A node whose own operator
// equals it is an INTERIOR node of an associative-commutative chain, and its
// rebuild is left to the chain's root. It is the parent's operator rather than a
// flag because interior-ness is exactly "my parent will flatten through me",
// and the parent is the only one that knows.
type eNormItem struct {
	t       *Term
	ctx     *ctxList
	dst     *Term
	exit    bool
	chainOp string
}

// eNormalize rewrites a term to its e-graph normal form, ITERATIVELY (#149).
//
// The LAST exposed structural recursion, and the only one that was
// POST-ADMISSION: it runs on terms the profile has already bounded, so unlike
// the decoder it needed no admission logic — but a permitted 65,536-node linear
// spine still exceeds host stack safety, and this is reached from
// `find --equiv`.
//
// It is BOTTOM-UP: a node's result depends on its normalized children, so
// children are scheduled first and the node revisits itself afterwards — `prim`
// to apply AC normalization, `match` to synthesize its scrutinee's type once
// that scrutinee has been normalized.
//
// Children are pushed in REVERSE so they pop in source order. For ARGUMENTS
// that is presentational: chk.synth is called once per node, not per argument,
// so their relative order is not observable — a control that reordered them
// moved nothing, which is how that was established rather than assumed. It
// matters for the SCRUTINEE, whose normalization must precede its own synth.
func eNormalize(chk *checkerMachine, ctx []*Ty, t *Term) *Term {
	if t == nil {
		return nil
	}
	root := &Term{}
	var c0 *ctxList
	for _, ty := range ctx {
		c0 = ctxExtend(c0, ty)
	}
	stack := []eNormItem{{t: t, ctx: c0, dst: root}}

	// DEFERRED AC INTERIORS (#151). A node marked here is a normalized
	// `op(x, y)` whose flatten-and-rebuild has NOT been done, because its parent
	// carries the same operator and will either flatten straight through it or
	// force it. The set exists to distinguish that from the other way a same-op
	// child can reach its parent unrebuilt — one whose own AC test came out
	// false, which today's acFlatten still descends through and which must keep
	// being left exactly as it is.
	//
	// Nothing escapes: a node is marked only when its parent is a commutative
	// two-argument primitive, and every prim schedules an exit visit, so the
	// parent always runs and always resolves it.
	var deferredAC map[*Term]bool
	deferAC := func(d *Term) {
		if deferredAC == nil {
			deferredAC = map[*Term]bool{}
		}
		deferredAC[d] = true
	}
	// forceAC materialises a deferred interior. It is the same work the node
	// would have done in place, and it reaches the whole deferred sub-chain
	// beneath it in one pass, because acFlatten descends through every same-op
	// node regardless of who deferred it.
	forceAC := func(d *Term) {
		if deferredAC[d] {
			delete(deferredAC, d)
			*d = *acRebuild(d.Op, acFlatten(d.Op, d.Args))
		}
	}

	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		push := func(x eNormItem) { stack = append(stack, x) }
		extend := ctxExtend

		if it.exit && it.t.K == "match" {
			cur, dst := it.t, it.dst
			scrutTy, terr := chk.synth(ctxSlice(it.ctx), cur.A)
			md, derr := chk.st.GetDef(cur.Hash)
			for i := len(cur.Arms) - 1; i >= 0; i-- {
				armCtx := it.ctx
				if terr == nil && derr == nil && scrutTy.K == "data" && i < len(md.Ctors) {
					for _, f := range instCtorFields(md, scrutTy.Hash, scrutTy.Args, i) {
						armCtx = extend(armCtx, f)
					}
				}
				push(eNormItem{t: &cur.Arms[i], ctx: armCtx, dst: &dst.Arms[i]})
			}
			continue
		}
		// `if` and `lam` are TRUE POST-CHILD phases, unlike match's above: their
		// children are scheduled during the descent and this runs once those
		// children are normalized. Neither node's children can be a deferred AC
		// interior — deferral is set only when the parent is a commutative
		// two-argument primitive, and these are not — so what is inspected here
		// is always fully materialised.
		if it.exit && it.t.K == "if" {
			if s := ifSelect(it.dst); s != nil {
				*it.dst = *s
			}
			continue
		}
		if it.exit && it.t.K == "lam" {
			if s := etaReduce(chk.st, it.dst); s != nil {
				*it.dst = *s
			}
			continue
		}
		if it.exit {
			// AC normalization, applied once the arguments are normalized.
			// synth is called on the ORIGINAL argument, as it always was: the
			// normalized copy may differ, and changing which term is
			// synthesized changes what gets inferred into it.
			nt := it.dst
			// INVOLUTION. `neg` is unary, so it is not a chain and has no
			// deferred interior to force: it is not in commutativePrims, so its
			// child was pushed with an empty chainOp and can never have been
			// deferred.
			if s := negInvolution(it.t.Op, nt.Args); s != nil {
				*nt = *s
				continue
			}
			if commutativePrims[it.t.Op] && len(nt.Args) == 2 {
				argTy, _ := chk.synth(ctxSlice(it.ctx), &it.t.Args[0])
				if isACPrim(it.t.Op, argTy) {
					// ONE FLATTEN AND ONE SORT PER MAXIMAL CHAIN (#151).
					//
					// Rebuilding here at every level was the quadratic: both
					// acFlatten and acRebuild walk the whole chain, and a chain
					// of depth d ran them d times. An interior node instead
					// leaves its normalized `op(x, y)` alone, so the chain
					// arrives at its root in one piece and is flattened once.
					//
					// The leaf MULTISET is what survives either way; only the
					// pre-sort ORDER differs, and acRebuild sorts on the
					// canonical bytes, so leaves that could be ordered
					// differently are byte-identical and the rebuilt chain is
					// the same bytes. That is not reasoning to trust on its own
					// — it is pinned by a recorded digest in
					// canon_iterative_test.go, which is the only witness that
					// can see a change here at all.
					if it.chainOp == it.t.Op {
						deferAC(nt)
					} else {
						*nt = *acRebuild(it.t.Op, acFlatten(it.t.Op, nt.Args))
					}
				} else {
					// This node does not associate, so a deferred child has no
					// parent left to absorb it and must be materialised now.
					// Reachable when synth disagrees between a parent and its
					// same-op child — the child is AC, the parent is not.
					forceAC(&nt.Args[0])
					forceAC(&nt.Args[1])
					// FLOAT `* 1`. The unit rule for the one commutative,
					// NON-associating operator that has a unit — so it cannot be
					// carried by the chain machinery above. It runs after
					// forceAC, so a surviving operand is always materialised.
					if s := mulUnitSurvivor(it.t.Op, argTy, nt.Args); s != nil {
						*nt = *s
						continue
					}
					if compareTermsCanonical(&nt.Args[0], &nt.Args[1]) > 0 {
						nt.Args[0], nt.Args[1] = nt.Args[1], nt.Args[0]
					}
				}
			}
			continue
		}

		cur, dst := it.t, it.dst
		*dst = *cur
		switch cur.K {
		case "lam":
			if cur.A != nil {
				// The exit is pushed FIRST so it pops LAST: eta inspects a body
				// that has already been normalized, which is what lets a chain
				// of etas reduce from the inside out in one pass.
				push(eNormItem{t: cur, ctx: it.ctx, dst: dst, exit: true})
				dst.A = &Term{}
				push(eNormItem{t: cur.A, ctx: extend(it.ctx, cur.Ty), dst: dst.A})
			}
		case "let":
			if cur.B != nil {
				dst.B = &Term{}
				push(eNormItem{t: cur.B, ctx: extend(it.ctx, cur.Ty), dst: dst.B})
			}
			if cur.A != nil {
				dst.A = &Term{}
				push(eNormItem{t: cur.A, ctx: it.ctx, dst: dst.A})
			}
		case "if":
			// Exit first, so it pops last (see `lam` above).
			push(eNormItem{t: cur, ctx: it.ctx, dst: dst, exit: true})
			for _, c := range []struct {
				src *Term
				dst **Term
			}{{cur.C, &dst.C}, {cur.B, &dst.B}, {cur.A, &dst.A}} {
				if c.src != nil {
					*c.dst = &Term{}
					push(eNormItem{t: c.src, ctx: it.ctx, dst: *c.dst})
				}
			}
		case "app":
			if cur.B != nil {
				dst.B = &Term{}
				push(eNormItem{t: cur.B, ctx: it.ctx, dst: dst.B})
			}
			if cur.A != nil {
				dst.A = &Term{}
				push(eNormItem{t: cur.A, ctx: it.ctx, dst: dst.A})
			}
		case "field":
			if cur.A != nil {
				dst.A = &Term{}
				push(eNormItem{t: cur.A, ctx: it.ctx, dst: dst.A})
			}
		case "match":
			dst.Arms = make([]Term, len(cur.Arms))
			// THE SCRUTINEE IS NORMALIZED BEFORE ITS TYPE IS SYNTHESIZED, which
			// is why match needs an exit phase rather than doing this inline.
			//
			// The recursive version runs `nt.A = eNormalize(t.A)` and only then
			// `chk.synth(ctx, t.A)`. Normalizing can MUTATE t.A — synth
			// publishes inferred TyArgs into the term it is given — so
			// synthesizing first sees a different term. A first draft here did
			// exactly that and agreed with the oracle on the whole corpus
			// anyway, because no corpus scrutinee carries omitted type
			// arguments. Faithful ordering is not something to infer from a
			// green differential.
			push(eNormItem{t: cur, ctx: it.ctx, dst: dst, exit: true})
			if cur.A != nil {
				dst.A = &Term{}
				push(eNormItem{t: cur.A, ctx: it.ctx, dst: dst.A})
			}
		case "prim", "ctor", "record":
			dst.Args = make([]Term, len(cur.Args))
			// A child may continue this node's chain only if this node is the
			// shape acFlatten descends through: a commutative primitive with
			// two arguments. Anything else ends the chain, and its children
			// carry no chainOp.
			childChain := ""
			if cur.K == "prim" {
				push(eNormItem{t: cur, ctx: it.ctx, dst: dst, exit: true, chainOp: it.chainOp})
				if commutativePrims[cur.Op] && len(cur.Args) == 2 {
					childChain = cur.Op
				}
			}
			for i := len(cur.Args) - 1; i >= 0; i-- {
				push(eNormItem{t: &cur.Args[i], ctx: it.ctx, dst: &dst.Args[i], chainOp: childChain})
			}
		}
	}
	return root
}

// eHash is the equivalence-class key of a definition: its signature plus its
// e-normalized body. Two definitions with the same eHash compute the same
// function up to the rewrite rules — the same equivalence class, though (by
// design) DIFFERENT identities.
//
// TWO PASSES, AND THEY ARE DIFFERENT KINDS OF THING. eNormalize applies the
// CONFLUENT rules directly to a normal form. eCanonicalArith (egraph.go) then
// runs the rules that have no confluent orientation — distributivity and its
// inverse, over Int and Rat — through a real e-graph, and extracts the cheapest
// representative of the root class. It returns its input unchanged whenever the
// e-graph finds nothing to do or cannot afford to run, so a body with no
// arithmetic redex hashes exactly the bytes it did before.
func eHash(st *Store, d *Def) string {
	chk := &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
	e := &enc{}
	e.ty(d.Ty)
	if d.Body != nil {
		// A COPY, NEVER THE STORED BODY. eNormalize calls chk.synth on the
		// ORIGINAL subterm and the checker publishes inferred type arguments
		// into the term it is given; Store.GetDef caches, so that write would
		// reach every later consumer of a definition whose bytes are its
		// identity. Discovery must be unable to move what it is reading.
		e.term(eCanonicalArith(chk, eNormalize(chk, nil, cloneTerm(d.Body))))
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

// enc appends canonical bytes in stream order and NEVER backpatches — the `'n'`
// and `'s'` work items in enc.term exist precisely so that a length emitted
// after a child's bytes is still written in order. That property is what makes
// a truncated encoding a true PREFIX of the full one, which compareTermsCanonical
// depends on.
//
// limit, when non-zero, stops encoding once that many bytes exist. It does not
// change which bytes are produced, only how many; truncated records whether
// anything was left unwritten.
//
// THE LIMIT IS OWNED BY THE BYTE SEAM, NOT BY THE WORK LOOPS. Checking it only
// between enc.term's work items bounds the number of NODES visited after the
// window closes, which is not the claim: one item can append a whole big.Int
// magnitude, a whole string, or a whole type encoding, so a 64-byte window could
// materialise megabytes while every byte written was still a faithful prefix.
// The structural owner of "how many bytes exist" is the place bytes are
// appended, and that is `room` — every other method here derives its behaviour
// from it, so a new emitter cannot be added that silently escapes the bound.
type enc struct {
	b         []byte
	limit     int
	truncated bool
}

// room reports how many of the next n bytes may be written, and records
// truncation when that is fewer than n. With no limit it is the identity, which
// is what keeps unlimited encoding byte-identical to the pre-limit encoder.
func (e *enc) room(n int) int {
	if e.limit <= 0 {
		return n
	}
	r := e.limit - len(e.b)
	if r < 0 {
		r = 0
	}
	if r < n {
		e.truncated = true
		return r
	}
	return n
}

// raw and rawStr are the only two places bytes reach e.b outside the fast paths
// below, which are the same operation specialised so the unlimited encoder does
// not pay for a bounds computation on every tag byte.
func (e *enc) raw(p []byte)    { e.b = append(e.b, p[:e.room(len(p))]...) }
func (e *enc) rawStr(s string) { e.b = append(e.b, s[:e.room(len(s))]...) }

func (e *enc) u8(v byte) {
	if e.room(1) == 1 {
		e.b = append(e.b, v)
	}
}

func (e *enc) u32(v uint32) {
	if e.limit <= 0 {
		e.b = binary.BigEndian.AppendUint32(e.b, v)
		return
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	e.raw(buf[:])
}

func (e *enc) i64(v int64) {
	if e.limit <= 0 {
		e.b = binary.BigEndian.AppendUint64(e.b, uint64(v))
		return
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	e.raw(buf[:])
}

func (e *enc) str(s string) { e.u32(uint32(len(s))); e.rawStr(s) }

// bigint encodes an arbitrary-precision integer canonically: a sign byte
// (0x00 for ≥0, 0x01 for <0), then a u32 magnitude length, then the minimal
// big-endian magnitude bytes (no leading zeros; zero is sign 0x00, length 0).
//
// The DECLARED length is always the true one — truncation drops trailing bytes
// and never rewrites a prefix that has already been committed — so the length is
// computed from BitLen rather than from the materialised magnitude. big.Int has
// no prefix accessor, so v.Bytes() still allocates the whole magnitude when even
// one byte of it is wanted; what the guard buys is that a bigint lying entirely
// beyond the window costs nothing at all.
func (e *enc) bigint(v *big.Int) {
	if v.Sign() < 0 {
		e.u8(1)
	} else {
		e.u8(0)
	}
	magLen := (v.BitLen() + 7) / 8 // exactly len(v.Bytes())
	e.u32(uint32(magLen))
	if magLen == 0 {
		return
	}
	if e.room(magLen) == 0 {
		return
	}
	e.raw(v.Bytes())
}

func (e *enc) hash(h string) {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) != 32 {
		panic(fmt.Sprintf("malformed hash reference %q in definition", h))
	}
	e.raw(raw)
}

// stop reports that the window is closed. It is only ever consulted with work
// still PENDING, so reaching the limit there means bytes were left unwritten —
// which is what licenses it to record the truncation rather than merely observe
// it. `room` would record the same thing at the next append; this exists so that
// a structure with millions of remaining nodes does not walk them to find out.
func (e *enc) stop() bool {
	if e.limit > 0 && len(e.b) >= e.limit {
		e.truncated = true
		return true
	}
	return false
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
		if e.stop() {
			return
		}
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
		if e.stop() {
			return
		}
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
		// The ONLY effect of a limit is to stop early. Bytes are appended in
		// stream order, so whatever has been written when this fires is exactly
		// a prefix of the full encoding — never a rearrangement of it. The
		// bound itself is enforced at the byte seam (see enc.room); this only
		// avoids walking the remaining nodes once nothing more can be written.
		if e.stop() {
			return
		}
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
			e.raw(buf[:])
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
		if e.stop() {
			return
		}
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

// reserveNodes charges a declared collection length UP FRONT, before the slice
// is allocated, and the children it pays for are then decoded as PREPAID tasks
// that do not charge again.
//
// Both halves are load-bearing and each was wrong on its own:
//
//	charge the length AND each child   bills wide structures twice, so the
//	                                   decoder accepted less than the profile
//	                                   documents and a valid object became
//	                                   undecodable
//	check the length without charging  leaves the reservation invisible to the
//	                                   NEXT check, so nested collections each
//	                                   allocate a near-limit slice while every
//	                                   check passes — quadratic memory from a
//	                                   few bytes of nested `rec` headers
//
// Charging once, eagerly, and marking the children prepaid gives exact parity
// with admitDef AND a reservation that persists across nesting.
func (d *dec) reserveNodes(n int) error {
	return d.countNodes(n)
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

	// prepaid: see decTask.
	prepaid bool
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
			push(decTyTask{kind: 't', dst: &task.node.Args[task.idx], prepaid: true})
			continue
		}

		dst := task.dst
		if !task.prepaid {
			if err := d.countNodes(1); err != nil {
				return nil, err
			}
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
				push(decTyTask{kind: 't', dst: &dst.Args[i], prepaid: true})
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
				push(decTyTask{kind: 't', dst: &dst.Args[i], prepaid: true})
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
	node *Term // kinds 'm', 'r' and 'f': the parent being filled
	idx  int   // kind 'r': which entry

	// prepaid marks a child whose node was already charged by its parent's
	// declared length. Charging it again would bill wide structures twice;
	// not charging the length at all would let nested collections allocate
	// unboundedly. Provenance is what lets both be avoided.
	prepaid bool
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
				push(decTask{kind: 't', dst: &task.node.Arms[i], prepaid: true})
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
			push(decTask{kind: 't', dst: &task.node.Args[task.idx], prepaid: true})
			continue
		}

		dst := task.dst
		if !task.prepaid {
			if err := d.countNodes(1); err != nil {
				return nil, err
			}
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
				push(decTask{kind: 't', dst: &dst.Args[i], prepaid: true})
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
				push(decTask{kind: 't', dst: &dst.Args[i], prepaid: true})
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
