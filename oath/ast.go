package main

// The AST is the language. There is no textual source of truth: the canonical
// JSON encoding of these structs is hashed to produce a definition's identity,
// so field order and omitempty behavior are part of the language spec.
//
// Variables are de Bruijn indices (Var 0 = innermost binder), so definitions
// are alpha-canonical by construction. Names never appear in hashed content;
// they live in Meta.

// Ty is a type expression.
type Ty struct {
	K    string `json:"k"`              // int | bool | var | fun | data | rec
	Var  int    `json:"var,omitempty"`  // k=var: index into the enclosing def's type params
	A    *Ty    `json:"a,omitempty"`    // k=fun: domain
	B    *Ty    `json:"b,omitempty"`    // k=fun: codomain
	Hash string `json:"hash,omitempty"` // k=data: content hash of the ADT
	Args []Ty   `json:"args,omitempty"` // k=data|rec: type arguments
}

func tInt() *Ty                     { return &Ty{K: "int"} }
func tBool() *Ty                    { return &Ty{K: "bool"} }
func tVar(i int) *Ty                { return &Ty{K: "var", Var: i} }
func tFun(a, b *Ty) *Ty             { return &Ty{K: "fun", A: a, B: b} }
func tDataTy(h string, args []Ty) *Ty { return &Ty{K: "data", Hash: h, Args: args} }
func tRec(args []Ty) *Ty            { return &Ty{K: "rec", Args: args} }

// Term is an expression. "rec" in types and "self" in terms refer to the
// definition currently being defined — the standard escape hatch that makes
// recursion compatible with content addressing (a hash cannot contain itself).
type Term struct {
	K      string `json:"k"`                // var | int | bool | lam | app | let | if | prim | ref | self | ctor | match
	Idx    int    `json:"idx,omitempty"`    // k=var: de Bruijn index; k=ctor: constructor index
	Int    int64  `json:"int,omitempty"`    // k=int
	Bool   bool   `json:"bool,omitempty"`   // k=bool
	Ty     *Ty    `json:"ty,omitempty"`     // k=lam: param type; k=let: bound type
	A      *Term  `json:"a,omitempty"`      // lam body | app fn | let bound | if cond | match scrutinee
	B      *Term  `json:"b,omitempty"`      // app arg | let body | if then
	C      *Term  `json:"c,omitempty"`      // if else
	Op     string `json:"op,omitempty"`     // k=prim: + - * / % neg == < <= and or not
	Hash   string `json:"hash,omitempty"`   // k=ref|ctor|match: hash of the referenced def / ADT
	TyArgs []Ty   `json:"tyargs,omitempty"` // k=ref|self|ctor: type instantiation
	Args   []Term `json:"args,omitempty"`   // k=prim|ctor: arguments
	Arms   []Term `json:"arms,omitempty"`   // k=match: one arm per constructor, in constructor order; arm i has ctor i's fields pushed as binders
}

// Prop is a machine-checkable property: forall binders, body evaluates to true.
// Binders must be concrete (monomorphic) types so inputs can be generated.
// Props are part of the definition and therefore part of its hash: changing
// the spec creates a different definition.
type Prop struct {
	Binders []Ty `json:"binders"`
	Body    Term `json:"body"`
}

// Def is the unit of code. Exactly one of the data/func field groups is used.
type Def struct {
	K      string `json:"k"`      // data | func
	TyVars int    `json:"tyvars"` // number of type parameters
	Ctors  [][]Ty `json:"ctors,omitempty"` // k=data: field types per constructor ("rec" = this ADT)
	Ty     *Ty    `json:"ty,omitempty"`    // k=func: declared type
	Body   *Term  `json:"body,omitempty"`  // k=func
	Props  []Prop `json:"props,omitempty"` // k=func
}

// Guarantee is the honestly-tracked verification status of a definition.
// It lives in Meta, not in the hashed Def: what was checked can improve
// over time without changing what the code IS.
type Guarantee struct {
	Level     string   `json:"level"`               // asserted | tested | falsified | proven
	Cases     int      `json:"cases,omitempty"`     // tested: cases per property
	Falsified []string `json:"falsified,omitempty"` // falsified: names of failed properties
}

// Meta is everything humans need and machines don't: names. Pure metadata,
// never hashed, freely editable.
type Meta struct {
	Name          string    `json:"name"`
	TyVarNames    []string  `json:"tyvar_names,omitempty"`
	CtorNames     []string  `json:"ctor_names,omitempty"`
	PropNames     []string  `json:"prop_names,omitempty"`
	Guarantee     Guarantee `json:"guarantee"`
	MutantsKilled int       `json:"mutants_killed,omitempty"` // spec strength: mutants the props caught
	MutantsTotal  int       `json:"mutants_total,omitempty"`  // spec strength: mutants generated
	Termination   string    `json:"termination,omitempty"`    // structural | nonrecursive | unknown (funcs only)
}

// collectDeps returns the set of definition hashes a def references.
func collectDeps(d *Def) map[string]bool {
	deps := map[string]bool{}
	var walkTy func(t *Ty)
	walkTy = func(t *Ty) {
		if t == nil {
			return
		}
		if t.K == "data" {
			deps[t.Hash] = true
		}
		walkTy(t.A)
		walkTy(t.B)
		for i := range t.Args {
			walkTy(&t.Args[i])
		}
	}
	var walkTerm func(t *Term)
	walkTerm = func(t *Term) {
		if t == nil {
			return
		}
		if t.Hash != "" {
			deps[t.Hash] = true
		}
		walkTy(t.Ty)
		for i := range t.TyArgs {
			walkTy(&t.TyArgs[i])
		}
		walkTerm(t.A)
		walkTerm(t.B)
		walkTerm(t.C)
		for i := range t.Args {
			walkTerm(&t.Args[i])
		}
		for i := range t.Arms {
			walkTerm(&t.Arms[i])
		}
	}
	for _, c := range d.Ctors {
		for i := range c {
			walkTy(&c[i])
		}
	}
	walkTy(d.Ty)
	walkTerm(d.Body)
	for _, p := range d.Props {
		for i := range p.Binders {
			walkTy(&p.Binders[i])
		}
		walkTerm(&p.Body)
	}
	return deps
}
