package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The surface syntax is NOT the language — it is an input format, one of many
// possible projections of the canonical AST. S-expressions were chosen because
// they are trivial to parse and trivial to emit: uniformity over sugar.
// Names written here are resolved to de Bruijn indices and content hashes
// during elaboration and then discarded from the hashed definition.
//
// Grammar:
//   (data Name [tyvars] (Ctor fieldTy ...) ...)
//   (defn name [tyvars] [(param ty) ...] retTy body (prop pname [(x ty) ...] body) ...)
// Terms:
//   123, true, false, x
//   (fn [(x ty) ...] body)
//   (let (x ty expr) body)
//   (if c t e)
//   (match scrut ((Ctor x y) body) ...)
//   (+ a b) (- a b) (* a b) (/ a b) (% a b) (neg a) (== a b) (< a b) (<= a b)
//   (and a b) (or a b) (not a)
//   (name [tyargs] arg ...)   — call a generic def/ctor; omit [tyargs] when it takes none
// Types:
//   Int, Bool, tyvar, (-> a b c), (Name tyargs...), Name

type sx struct {
	K    string // list | brack | brace | sym | int | str
	Sym  string
	Int  int64
	Str  string
	Kids []sx
	Line int
}

func (x sx) isSym(s string) bool { return x.K == "sym" && x.Sym == s }

// --- lexer + reader ---

type token struct {
	kind string // ( ) [ ] { } sym int str
	sym  string
	i    int64
	s    string
	line int
}

func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	for i < len(src) {
		ch := src[i]
		switch {
		case ch == '\n':
			line++
			i++
		case ch == ' ' || ch == '\t' || ch == '\r':
			i++
		case ch == ';':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case ch == '(' || ch == ')' || ch == '[' || ch == ']' || ch == '{' || ch == '}':
			toks = append(toks, token{kind: string(ch), line: line})
			i++
		case ch == '"':
			j := i + 1
			var b strings.Builder
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' && j+1 < len(src) {
					switch src[j+1] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case '"':
						b.WriteByte('"')
					case '\\':
						b.WriteByte('\\')
					default:
						return nil, fmt.Errorf("line %d: unknown escape \\%c", line, src[j+1])
					}
					j += 2
					continue
				}
				if src[j] == '\n' {
					line++
				}
				b.WriteByte(src[j])
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("line %d: unclosed string literal", line)
			}
			toks = append(toks, token{kind: "str", s: b.String(), line: line})
			i = j + 1
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\r\n()[]{};\"", rune(src[j])) {
				j++
			}
			word := src[i:j]
			if n, err := strconv.ParseInt(word, 10, 64); err == nil {
				toks = append(toks, token{kind: "int", i: n, line: line})
			} else {
				toks = append(toks, token{kind: "sym", sym: word, line: line})
			}
			i = j
		}
	}
	return toks, nil
}

type reader struct {
	toks []token
	pos  int
}

func (r *reader) read() (sx, error) {
	if r.pos >= len(r.toks) {
		return sx{}, fmt.Errorf("unexpected end of input")
	}
	t := r.toks[r.pos]
	r.pos++
	switch t.kind {
	case "int":
		return sx{K: "int", Int: t.i, Line: t.line}, nil
	case "str":
		return sx{K: "str", Str: t.s, Line: t.line}, nil
	case "sym":
		return sx{K: "sym", Sym: t.sym, Line: t.line}, nil
	case "(", "[", "{":
		closer := ")"
		kind := "list"
		if t.kind == "[" {
			closer = "]"
			kind = "brack"
		}
		if t.kind == "{" {
			closer = "}"
			kind = "brace"
		}
		var kids []sx
		for {
			if r.pos >= len(r.toks) {
				return sx{}, fmt.Errorf("line %d: unclosed %q", t.line, t.kind)
			}
			if r.toks[r.pos].kind == closer {
				r.pos++
				return sx{K: kind, Kids: kids, Line: t.line}, nil
			}
			k, err := r.read()
			if err != nil {
				return sx{}, err
			}
			kids = append(kids, k)
		}
	}
	return sx{}, fmt.Errorf("line %d: unexpected %q", t.line, t.kind)
}

// parseForms reads all top-level forms from source text.
func parseForms(src string) ([]sx, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	r := &reader{toks: toks}
	var out []sx
	for r.pos < len(r.toks) {
		x, err := r.read()
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// --- elaborator ---

type elab struct {
	st         *Store
	tyvars     []string // type variable names in scope
	scope      []string // term variable names, innermost last
	dataSelf   string   // name of the data def being defined ("" otherwise)
	funcSelf   string   // name of the func def being defined ("" otherwise)
	selfTyVars int
}

func (e *elab) errAt(x sx, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", x.Line, fmt.Sprintf(format, args...))
}

func (e *elab) lookupVar(name string) (int, bool) {
	for i := len(e.scope) - 1; i >= 0; i-- {
		if e.scope[i] == name {
			return len(e.scope) - 1 - i, true
		}
	}
	return 0, false
}

func (e *elab) lookupTyVar(name string) (int, bool) {
	for i, n := range e.tyvars {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

func (e *elab) parseTy(x sx) (*Ty, error) {
	switch x.K {
	case "brace":
		// Record type: {name Ty name Ty ...}. Author order is irrelevant —
		// fields are sorted into canonical form here, so two spellings of
		// the same record are the same type with the same hash.
		return e.parseRecord(x, func(v sx) (*Ty, error) { return e.parseTy(v) })
	case "sym":
		switch x.Sym {
		case "Int":
			return tInt(), nil
		case "Bool":
			return tBool(), nil
		case "Str":
			return tStr(), nil
		}
		if i, ok := e.lookupTyVar(x.Sym); ok {
			return tVar(i), nil
		}
		if x.Sym == e.dataSelf {
			if e.selfTyVars != 0 {
				return nil, e.errAt(x, "%s takes %d type arguments", x.Sym, e.selfTyVars)
			}
			return tRec(nil), nil
		}
		if h, ok := e.st.Resolve(x.Sym); ok {
			d, err := e.st.GetDef(h)
			if err != nil {
				return nil, err
			}
			if d.K != "data" {
				return nil, e.errAt(x, "%s is a function, not a type", x.Sym)
			}
			if d.TyVars != 0 {
				return nil, e.errAt(x, "%s takes %d type arguments", x.Sym, d.TyVars)
			}
			return tDataTy(h, nil), nil
		}
		return nil, e.errAt(x, "unknown type %q", x.Sym)
	case "list":
		if len(x.Kids) == 0 {
			return nil, e.errAt(x, "empty type")
		}
		head := x.Kids[0]
		if head.isSym("->") {
			if len(x.Kids) < 3 {
				return nil, e.errAt(x, "-> needs at least two types")
			}
			tys := make([]*Ty, 0, len(x.Kids)-1)
			for _, k := range x.Kids[1:] {
				t, err := e.parseTy(k)
				if err != nil {
					return nil, err
				}
				tys = append(tys, t)
			}
			out := tys[len(tys)-1]
			for i := len(tys) - 2; i >= 0; i-- {
				out = tFun(tys[i], out)
			}
			return out, nil
		}
		if head.K != "sym" {
			return nil, e.errAt(x, "type must start with a name")
		}
		var args []Ty
		for _, k := range x.Kids[1:] {
			t, err := e.parseTy(k)
			if err != nil {
				return nil, err
			}
			args = append(args, *t)
		}
		if head.Sym == e.dataSelf {
			if len(args) != e.selfTyVars {
				return nil, e.errAt(x, "%s takes %d type arguments, got %d", head.Sym, e.selfTyVars, len(args))
			}
			return tRec(args), nil
		}
		h, ok := e.st.Resolve(head.Sym)
		if !ok {
			return nil, e.errAt(x, "unknown type %q", head.Sym)
		}
		d, err := e.st.GetDef(h)
		if err != nil {
			return nil, err
		}
		if d.K != "data" {
			return nil, e.errAt(x, "%s is a function, not a type", head.Sym)
		}
		if len(args) != d.TyVars {
			return nil, e.errAt(x, "%s takes %d type arguments, got %d", head.Sym, d.TyVars, len(args))
		}
		return tDataTy(h, args), nil
	}
	return nil, e.errAt(x, "expected a type")
}

var primArity = map[string]int{
	"+": 2, "-": 2, "*": 2, "/": 2, "%": 2, "neg": 1,
	"==": 2, "<": 2, "<=": 2, "and": 2, "or": 2, "not": 1,
	"++": 2, "str-len": 1,
}

// parseRecord elaborates {name X name X ...} into sorted (names, items),
// shared by record types and record literals.
func (e *elab) parseRecord(x sx, elabItem func(sx) (*Ty, error)) (*Ty, error) {
	if len(x.Kids)%2 != 0 {
		return nil, e.errAt(x, "record needs name/value pairs")
	}
	type pair struct {
		name string
		ty   Ty
	}
	var pairs []pair
	for i := 0; i < len(x.Kids); i += 2 {
		if x.Kids[i].K != "sym" {
			return nil, e.errAt(x.Kids[i], "record field name must be a symbol")
		}
		t, err := elabItem(x.Kids[i+1])
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, pair{name: x.Kids[i].Sym, ty: *t})
	}
	sort.Slice(pairs, func(a, b int) bool { return pairs[a].name < pairs[b].name })
	out := &Ty{K: "record"}
	for i, p := range pairs {
		if i > 0 && p.name == pairs[i-1].name {
			return nil, e.errAt(x, "duplicate record field %q", p.name)
		}
		out.Names = append(out.Names, p.name)
		out.Args = append(out.Args, p.ty)
	}
	return out, nil
}

func (e *elab) elabTerm(x sx) (*Term, error) {
	switch x.K {
	case "int":
		return &Term{K: "int", Int: x.Int}, nil
	case "str":
		return &Term{K: "str", Str: x.Str}, nil
	case "brace":
		// Record literal: {name expr name expr ...}, sorted like the type.
		if len(x.Kids)%2 != 0 {
			return nil, e.errAt(x, "record literal needs name/value pairs")
		}
		type fpair struct {
			name string
			term Term
		}
		var pairs []fpair
		for i := 0; i < len(x.Kids); i += 2 {
			if x.Kids[i].K != "sym" {
				return nil, e.errAt(x.Kids[i], "record field name must be a symbol")
			}
			t, err := e.elabTerm(x.Kids[i+1])
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, fpair{name: x.Kids[i].Sym, term: *t})
		}
		sort.Slice(pairs, func(a, b int) bool { return pairs[a].name < pairs[b].name })
		out := &Term{K: "record"}
		for i, p := range pairs {
			if i > 0 && p.name == pairs[i-1].name {
				return nil, e.errAt(x, "duplicate record field %q", p.name)
			}
			out.Names = append(out.Names, p.name)
			out.Args = append(out.Args, p.term)
		}
		return out, nil
	case "sym":
		switch x.Sym {
		case "true":
			return &Term{K: "bool", Bool: true}, nil
		case "false":
			return &Term{K: "bool", Bool: false}, nil
		}
		return e.elabName(x, nil, false)
	case "list":
		if len(x.Kids) == 0 {
			return nil, e.errAt(x, "empty expression")
		}
		head := x.Kids[0]
		if head.K == "sym" {
			switch head.Sym {
			case "fn":
				return e.elabFn(x)
			case "let":
				return e.elabLet(x)
			case "if":
				if len(x.Kids) != 4 {
					return nil, e.errAt(x, "if needs condition, then, else")
				}
				c, err := e.elabTerm(x.Kids[1])
				if err != nil {
					return nil, err
				}
				th, err := e.elabTerm(x.Kids[2])
				if err != nil {
					return nil, err
				}
				el, err := e.elabTerm(x.Kids[3])
				if err != nil {
					return nil, err
				}
				return &Term{K: "if", A: c, B: th, C: el}, nil
			case "match":
				return e.elabMatch(x)
			case "list":
				// List-literal sugar (#35): (list e0 e1 …) elaborates to
				// (Cons e0 (Cons e1 … (Nil))) with the element type inferred, so
				// it is byte-identical to the explicit chain. `Nil`/`Cons` must be
				// defined; this is a reserved head, so a name `list` cannot be
				// applied (there is none in the corpus).
				consH, consIdx, ok1 := e.st.FindCtor("Cons")
				nilH, nilIdx, ok2 := e.st.FindCtor("Nil")
				if !ok1 || !ok2 {
					return nil, e.errAt(x, "(list …) requires the List type (Nil and Cons) to be in scope")
				}
				var elems []*Term
				for _, k := range x.Kids[1:] {
					el, err := e.elabTerm(k)
					if err != nil {
						return nil, err
					}
					elems = append(elems, el)
				}
				acc := &Term{K: "ctor", Hash: nilH, Idx: nilIdx}
				for i := len(elems) - 1; i >= 0; i-- {
					acc = &Term{K: "ctor", Hash: consH, Idx: consIdx, Args: []Term{*elems[i], *acc}}
				}
				return acc, nil
			case ".":
				if len(x.Kids) != 3 || x.Kids[2].K != "sym" {
					return nil, e.errAt(x, ". needs a record expression and a field name")
				}
				r, err := e.elabTerm(x.Kids[1])
				if err != nil {
					return nil, err
				}
				return &Term{K: "field", A: r, Op: x.Kids[2].Sym}, nil
			}
			if arity, ok := primArity[head.Sym]; ok {
				if len(x.Kids)-1 != arity {
					return nil, e.errAt(x, "%s takes %d arguments, got %d", head.Sym, arity, len(x.Kids)-1)
				}
				var args []Term
				for _, k := range x.Kids[1:] {
					a, err := e.elabTerm(k)
					if err != nil {
						return nil, err
					}
					args = append(args, *a)
				}
				return &Term{K: "prim", Op: head.Sym, Args: args}, nil
			}
			// Named application: (name [tyargs] arg ...)
			rest := x.Kids[1:]
			var tyargs []Ty
			hasTyArgs := false
			if len(rest) > 0 && rest[0].K == "brack" {
				hasTyArgs = true
				for _, k := range rest[0].Kids {
					t, err := e.parseTy(k)
					if err != nil {
						return nil, err
					}
					tyargs = append(tyargs, *t)
				}
				rest = rest[1:]
			}
			if _, isLocal := e.lookupVar(head.Sym); isLocal && hasTyArgs {
				return nil, e.errAt(x, "local variable %q cannot take type arguments", head.Sym)
			}
			base, err := e.elabName(head, tyargs, hasTyArgs)
			if err != nil {
				return nil, err
			}
			if base.K == "ctor" {
				// Constructors are saturated: all remaining kids are fields.
				var args []Term
				for _, k := range rest {
					a, err := e.elabTerm(k)
					if err != nil {
						return nil, err
					}
					args = append(args, *a)
				}
				base.Args = args
				return base, nil
			}
			return applyChain(base, rest, e)
		}
		// Compound head: ((compose f g) x)
		base, err := e.elabTerm(head)
		if err != nil {
			return nil, err
		}
		return applyChain(base, x.Kids[1:], e)
	}
	return nil, e.errAt(x, "expected an expression")
}

func applyChain(base *Term, args []sx, e *elab) (*Term, error) {
	out := base
	for _, k := range args {
		a, err := e.elabTerm(k)
		if err != nil {
			return nil, err
		}
		out = &Term{K: "app", A: out, B: a}
	}
	return out, nil
}

// elabName resolves a bare name: local variable, then the def being defined
// (recursion), then constructor, then stored definition.
func (e *elab) elabName(x sx, tyargs []Ty, hasTyArgs bool) (*Term, error) {
	name := x.Sym
	if i, ok := e.lookupVar(name); ok {
		return &Term{K: "var", Idx: i}, nil
	}
	// Type arguments may be OMITTED and inferred (#35): a bracket group with the
	// wrong count is still an error, but no brackets at all defers to the
	// typechecker, which solves and backfills the type arguments before hashing.
	if name == e.funcSelf {
		if hasTyArgs && len(tyargs) != e.selfTyVars {
			return nil, e.errAt(x, "%s takes %d type arguments, got %d", name, e.selfTyVars, len(tyargs))
		}
		return &Term{K: "self", TyArgs: tyargs}, nil
	}
	if h, idx, ok := e.st.FindCtor(name); ok {
		d, err := e.st.GetDef(h)
		if err != nil {
			return nil, err
		}
		if hasTyArgs && len(tyargs) != d.TyVars {
			return nil, e.errAt(x, "constructor %s takes %d type arguments, got %d", name, d.TyVars, len(tyargs))
		}
		return &Term{K: "ctor", Hash: h, Idx: idx, TyArgs: tyargs}, nil
	}
	if h, ok := e.st.Resolve(name); ok {
		d, err := e.st.GetDef(h)
		if err != nil {
			return nil, err
		}
		if d.K != "func" {
			return nil, e.errAt(x, "%s is a data type, not a value", name)
		}
		if hasTyArgs && len(tyargs) != d.TyVars {
			return nil, e.errAt(x, "%s takes %d type arguments, got %d", name, d.TyVars, len(tyargs))
		}
		return &Term{K: "ref", Hash: h, TyArgs: tyargs}, nil
	}
	if hasTyArgs {
		return nil, e.errAt(x, "unknown name %q", name)
	}
	return nil, e.errAt(x, "unknown name %q", name)
}

// (fn [(x ty) ...] body)
func (e *elab) elabFn(x sx) (*Term, error) {
	if len(x.Kids) != 3 || x.Kids[1].K != "brack" {
		return nil, e.errAt(x, "fn needs a [(name type) ...] parameter list and a body")
	}
	names, tys, err := e.parseParams(x.Kids[1])
	if err != nil {
		return nil, err
	}
	e.scope = append(e.scope, names...)
	body, err := e.elabTerm(x.Kids[2])
	if err != nil {
		return nil, err
	}
	e.scope = e.scope[:len(e.scope)-len(names)]
	for i := len(tys) - 1; i >= 0; i-- {
		body = &Term{K: "lam", Ty: tys[i], A: body}
	}
	return body, nil
}

// (let (x ty expr) body)
func (e *elab) elabLet(x sx) (*Term, error) {
	if len(x.Kids) != 3 || x.Kids[1].K != "list" || len(x.Kids[1].Kids) != 3 || x.Kids[1].Kids[0].K != "sym" {
		return nil, e.errAt(x, "let needs (name type expr) and a body")
	}
	b := x.Kids[1]
	ty, err := e.parseTy(b.Kids[1])
	if err != nil {
		return nil, err
	}
	bound, err := e.elabTerm(b.Kids[2])
	if err != nil {
		return nil, err
	}
	e.scope = append(e.scope, b.Kids[0].Sym)
	body, err := e.elabTerm(x.Kids[2])
	if err != nil {
		return nil, err
	}
	e.scope = e.scope[:len(e.scope)-1]
	return &Term{K: "let", Ty: ty, A: bound, B: body}, nil
}

// (match scrut ((Ctor x y) body) ...)
func (e *elab) elabMatch(x sx) (*Term, error) {
	if len(x.Kids) < 3 {
		return nil, e.errAt(x, "match needs a scrutinee and at least one arm")
	}
	scrut, err := e.elabTerm(x.Kids[1])
	if err != nil {
		return nil, err
	}
	type armInfo struct {
		idx  int
		body *Term
	}
	var dataHash string
	var arms []armInfo
	for _, a := range x.Kids[2:] {
		if a.K != "list" || len(a.Kids) != 2 || a.Kids[0].K != "list" || len(a.Kids[0].Kids) == 0 || a.Kids[0].Kids[0].K != "sym" {
			return nil, e.errAt(a, "match arm must be ((Ctor binders...) body)")
		}
		pat := a.Kids[0]
		cname := pat.Kids[0].Sym
		h, idx, ok := e.st.FindCtor(cname)
		if !ok {
			return nil, e.errAt(pat, "unknown constructor %q", cname)
		}
		if dataHash == "" {
			dataHash = h
		} else if dataHash != h {
			return nil, e.errAt(pat, "constructor %s belongs to a different data type", cname)
		}
		d, err := e.st.GetDef(h)
		if err != nil {
			return nil, err
		}
		nFields := len(d.Ctors[idx])
		if len(pat.Kids)-1 != nFields {
			return nil, e.errAt(pat, "constructor %s has %d fields, pattern binds %d", cname, nFields, len(pat.Kids)-1)
		}
		var binders []string
		for _, b := range pat.Kids[1:] {
			if b.K != "sym" {
				return nil, e.errAt(b, "pattern binders must be names")
			}
			binders = append(binders, b.Sym)
		}
		e.scope = append(e.scope, binders...)
		body, err := e.elabTerm(a.Kids[1])
		if err != nil {
			return nil, err
		}
		e.scope = e.scope[:len(e.scope)-len(binders)]
		arms = append(arms, armInfo{idx: idx, body: body})
	}
	d, err := e.st.GetDef(dataHash)
	if err != nil {
		return nil, err
	}
	ordered := make([]*Term, len(d.Ctors))
	for _, a := range arms {
		if ordered[a.idx] != nil {
			return nil, e.errAt(x, "duplicate arm for constructor %d", a.idx)
		}
		ordered[a.idx] = a.body
	}
	m, _ := e.st.GetMeta(dataHash)
	var out []Term
	for i, a := range ordered {
		if a == nil {
			cn := fmt.Sprintf("constructor %d", i)
			if m != nil && i < len(m.CtorNames) {
				cn = m.CtorNames[i]
			}
			return nil, e.errAt(x, "non-exhaustive match: missing arm for %s", cn)
		}
		out = append(out, *a)
	}
	return &Term{K: "match", Hash: dataHash, A: scrut, Arms: out}, nil
}

func (e *elab) parseParams(b sx) ([]string, []*Ty, error) {
	var names []string
	var tys []*Ty
	for _, p := range b.Kids {
		if p.K != "list" || len(p.Kids) != 2 || p.Kids[0].K != "sym" {
			return nil, nil, e.errAt(p, "parameter must be (name type)")
		}
		ty, err := e.parseTy(p.Kids[1])
		if err != nil {
			return nil, nil, err
		}
		names = append(names, p.Kids[0].Sym)
		tys = append(tys, ty)
	}
	return names, tys, nil
}

func tyvarNames(b sx) ([]string, error) {
	var out []string
	for _, k := range b.Kids {
		if k.K != "sym" {
			return nil, fmt.Errorf("line %d: type variables must be names", k.Line)
		}
		out = append(out, k.Sym)
	}
	return out, nil
}

// elabData: (data Name [tyvars] (Ctor fieldTy ...) ...)
func elabData(st *Store, x sx) (*Def, *Meta, error) {
	if len(x.Kids) < 3 || x.Kids[1].K != "sym" || x.Kids[2].K != "brack" {
		return nil, nil, fmt.Errorf("line %d: data needs a name, [tyvars], and constructors", x.Line)
	}
	name := x.Kids[1].Sym
	tvs, err := tyvarNames(x.Kids[2])
	if err != nil {
		return nil, nil, err
	}
	e := &elab{st: st, tyvars: tvs, dataSelf: name, selfTyVars: len(tvs)}
	var ctors [][]Ty
	var ctorNames []string
	for _, c := range x.Kids[3:] {
		if c.K != "list" || len(c.Kids) == 0 || c.Kids[0].K != "sym" {
			return nil, nil, e.errAt(c, "constructor must be (Name fieldTy ...)")
		}
		ctorNames = append(ctorNames, c.Kids[0].Sym)
		fields := []Ty{}
		for _, f := range c.Kids[1:] {
			ty, err := e.parseTy(f)
			if err != nil {
				return nil, nil, err
			}
			fields = append(fields, *ty)
		}
		ctors = append(ctors, fields)
	}
	def := &Def{K: "data", TyVars: len(tvs), Ctors: ctors}
	meta := &Meta{Name: name, TyVarNames: tvs, CtorNames: ctorNames, Guarantee: Guarantee{Level: "asserted"}}
	return def, meta, nil
}

// elabFunc: (defn name [tyvars] [(param ty) ...] retTy body prop...)
func elabFunc(st *Store, x sx) (*Def, *Meta, error) {
	if len(x.Kids) < 6 || x.Kids[1].K != "sym" || x.Kids[2].K != "brack" || x.Kids[3].K != "brack" {
		return nil, nil, fmt.Errorf("line %d: defn needs name [tyvars] [(param ty)...] retTy body", x.Line)
	}
	name := x.Kids[1].Sym
	tvs, err := tyvarNames(x.Kids[2])
	if err != nil {
		return nil, nil, err
	}
	e := &elab{st: st, tyvars: tvs, funcSelf: name, selfTyVars: len(tvs)}
	pnames, ptys, err := e.parseParams(x.Kids[3])
	if err != nil {
		return nil, nil, err
	}
	retTy, err := e.parseTy(x.Kids[4])
	if err != nil {
		return nil, nil, err
	}
	fullTy := retTy
	for i := len(ptys) - 1; i >= 0; i-- {
		fullTy = tFun(ptys[i], fullTy)
	}
	e.scope = append([]string{}, pnames...)
	body, err := e.elabTerm(x.Kids[5])
	if err != nil {
		return nil, nil, err
	}
	e.scope = nil
	for i := len(ptys) - 1; i >= 0; i-- {
		body = &Term{K: "lam", Ty: ptys[i], A: body}
	}

	var props []Prop
	var propNames []string
	for _, p := range x.Kids[6:] {
		if p.K != "list" || len(p.Kids) != 4 || !p.Kids[0].isSym("prop") || p.Kids[1].K != "sym" || p.Kids[2].K != "brack" {
			return nil, nil, e.errAt(p, "property must be (prop name [(x ty) ...] body)")
		}
		pe := &elab{st: st, funcSelf: name, selfTyVars: len(tvs)}
		bnames, btys, err := pe.parseParams(p.Kids[2])
		if err != nil {
			return nil, nil, err
		}
		pe.scope = bnames
		pbody, err := pe.elabTerm(p.Kids[3])
		if err != nil {
			return nil, nil, err
		}
		binders := make([]Ty, len(btys))
		for i, t := range btys {
			binders[i] = *t
		}
		props = append(props, Prop{Binders: binders, Body: *pbody})
		propNames = append(propNames, p.Kids[1].Sym)
	}

	def := &Def{K: "func", TyVars: len(tvs), Ty: fullTy, Body: body, Props: props}
	meta := &Meta{Name: name, TyVarNames: tvs, PropNames: propNames, ParamNames: pnames, Guarantee: Guarantee{Level: "asserted"}}
	return def, meta, nil
}
