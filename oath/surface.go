package main

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parseFloatLit recognizes an IEEE Float literal: a token ending in `f` whose
// prefix matches the PORTABLE decimal float grammar (0.1f, 1f, 3.14f, 1e9f,
// -2.5f). Returns ok=false for anything else, so bare symbols and Rat/Int
// literals fall through. We reject the parts of Go's ParseFloat grammar that
// are not portable — hex floats (`0x1p4`) and digit-separator underscores
// (`1_000`) — so a second kernel using a plain decimal float parser agrees
// byte-for-byte (SPEC §1.4).
func parseFloatLit(word string) (float64, bool) {
	if len(word) < 2 || word[len(word)-1] != 'f' {
		return 0, false
	}
	body := word[:len(word)-1]
	if strings.ContainsAny(body, "_xXpP") {
		return 0, false
	}
	f, err := strconv.ParseFloat(body, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

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
	K     string // list | brack | brace | sym | int | str | rat | float
	Sym   string
	Int   *big.Int
	Rat   *big.Rat
	Float float64
	Str   string
	Kids  []sx
	Line  int
}

func (x sx) isSym(s string) bool { return x.K == "sym" && x.Sym == s }

// --- lexer + reader ---

type token struct {
	kind string // ( ) [ ] { } sym int str rat float
	sym  string
	i    *big.Int
	r    *big.Rat
	f    float64
	s    string
	line int
}

// SOURCE IS TEXT, AND THAT IS AN IDENTITY OBLIGATION, NOT A CONVENIENCE.
//
// A string literal elaborates through []rune, and Go's []rune conversion
// substitutes U+FFFD for every byte that is not valid UTF-8. That codepoint
// lands in the SCons chain, so it lands in the canonical encoding, so it lands
// in the HASH — and four source files differing only in which malformed byte
// they carry content-address to one object. In a language where the hash IS the
// identity, and where names, journal entries and signatures are permanent, a
// non-injective front end is the one substitution that cannot be tolerated
// anywhere (#133).
//
// So the refusal is here, at the single entry to the parser, rather than at each
// call site that happens to read a file: `oath put`, `oath eval`, the --json
// paths and the MCP surface all reach the language through lex, and a check per
// caller is a check that a later caller will miss. oathrs already refuses these
// bytes — its reader is typed to UTF-8 — so this also closes a live cross-kernel
// divergence that no fixture could catch, the corpus being valid UTF-8
// throughout.
func lex(src string) ([]token, error) {
	if !utf8.ValidString(src) {
		return nil, fmt.Errorf("source is not valid UTF-8: a string literal cannot hold arbitrary bytes, " +
			"and substituting U+FFFD would make distinct sources content-address to one definition")
	}
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
			if n, ok := new(big.Int).SetString(word, 10); ok {
				toks = append(toks, token{kind: "int", i: n, line: line})
			} else if f, ok := parseFloatLit(word); ok {
				// An IEEE Float literal opts in with an `f` suffix (0.1f, 1f,
				// 3.14f). Checked before Rat so the suffix wins; a bare symbol
				// like `f` or `fold` (empty/non-numeric prefix) is unaffected.
				toks = append(toks, token{kind: "float", f: f, line: line})
			} else if rr, ok := new(big.Rat).SetString(word); ok {
				// A decimal (3.14) or fraction (1/2) literal — Int is tried first,
				// so a bare integer never reaches here.
				toks = append(toks, token{kind: "rat", r: rr, line: line})
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

// readFrame is one open delimiter awaiting its closer. Frames live on an
// explicit slice, NOT on the host call stack — see read().
type readFrame struct {
	kind   string // the sx kind to emit: list | brack | brace
	open   string // the opening token, for the "unclosed" message
	closer string
	line   int
	kids   []sx
}

// read consumes one form. It is ITERATIVE BY CONSTRUCTION, and that is a
// correctness property of the language rather than an optimisation (#149).
//
// The recursive version descended one host frame per open delimiter, so nesting
// depth in the SOURCE became depth on the host call stack. On wasm that stack is
// the embedder's, and it is exhausted at a depth Go cannot observe or grow —
// which made a purely syntactic property of a paste (how many "(" it contains)
// decide whether the kernel returned an Oath error or terminated outside Oath's
// error channel entirely. Measured before this change: ~20,000 delimiters threw
// a host RangeError out through oathCheck.
//
// THE INVARIANT THIS PROTECTS IS BACKEND INDEPENDENCE. Oath's accepted
// structural domain must not vary with an embedder's stack: the same artifact
// cannot be a program natively and a syntax error in the playground because the
// two borrow different stacks. An implementation may refuse for an explicit,
// documented resource limit — that is a language/runtime policy decision — but a
// limit must never emerge accidentally from host call-stack depth. #147 made the
// same correction one layer down, for a bound derived in one deployment
// environment and silently inherited by another.
//
// Frames are heap-allocated and bounded by the token count, so depth costs
// memory linear in the input rather than an unobservable host resource.
func (r *reader) read() (sx, error) {
	var stack []readFrame
	for {
		if r.pos >= len(r.toks) {
			// Report the INNERMOST unclosed delimiter, matching the recursive
			// version: it hit end-of-input inside the deepest active frame.
			if n := len(stack); n > 0 {
				return sx{}, fmt.Errorf("line %d: unclosed %q", stack[n-1].line, stack[n-1].open)
			}
			return sx{}, fmt.Errorf("unexpected end of input")
		}
		t := r.toks[r.pos]

		// A closer for the innermost frame completes it. Checked before the
		// switch because closers are not values and have no case there.
		if n := len(stack); n > 0 && t.kind == stack[n-1].closer {
			r.pos++
			f := stack[n-1]
			stack = stack[:n-1]
			done := sx{K: f.kind, Kids: f.kids, Line: f.line}
			if len(stack) == 0 {
				return done, nil
			}
			stack[len(stack)-1].kids = append(stack[len(stack)-1].kids, done)
			continue
		}

		r.pos++
		var val sx
		switch t.kind {
		case "int":
			val = sx{K: "int", Int: t.i, Line: t.line}
		case "rat":
			val = sx{K: "rat", Rat: t.r, Line: t.line}
		case "float":
			val = sx{K: "float", Float: t.f, Line: t.line}
		case "str":
			val = sx{K: "str", Str: t.s, Line: t.line}
		case "sym":
			val = sx{K: "sym", Sym: t.sym, Line: t.line}
		case "(", "[", "{":
			// SYNTAX NESTING ADMISSION. Reported as a typed resource refusal,
			// never as malformed syntax: the input is well-formed and larger
			// than this profile admits, and telling an author to fix a program
			// that has nothing wrong with it is a different (wrong) message.
			//
			// Enforceable here precisely BECAUSE this loop is iterative — the
			// limit is reached and reported rather than approached until a host
			// stack gives out. It also currently protects the elaborator, which
			// still descends this tree recursively.
			if len(stack) >= maxSyntaxNesting {
				return sx{}, errTooDeeplyNested(t.line)
			}
			f := readFrame{kind: "list", open: t.kind, closer: ")", line: t.line}
			switch t.kind {
			case "[":
				f.kind, f.closer = "brack", "]"
			case "{":
				f.kind, f.closer = "brace", "}"
			}
			stack = append(stack, f)
			continue
		default:
			// A mismatched or stray closer lands here, exactly as it did when
			// the recursive reader called itself on a non-value token.
			return sx{}, fmt.Errorf("line %d: unexpected %q", t.line, t.kind)
		}
		if len(stack) == 0 {
			return val, nil
		}
		stack[len(stack)-1].kids = append(stack[len(stack)-1].kids, val)
	}
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
	gensym     int            // counter for fresh binder names introduced by desugaring
	aliases    map[string]*Ty // batch-scoped type aliases (identity-transparent surface sugar)
}

// cloneTy deep-copies a type so an expanded alias cannot alias (in the pointer sense)
// the stored template — the expansion must be a fresh, independent tree, structurally
// identical to writing the type inline.
func cloneTy(t *Ty) *Ty {
	if t == nil {
		return nil
	}
	c := *t
	c.A = cloneTy(t.A)
	c.B = cloneTy(t.B)
	if t.Args != nil {
		c.Args = make([]Ty, len(t.Args))
		for i := range t.Args {
			c.Args[i] = *cloneTy(&t.Args[i])
		}
	}
	if t.Names != nil {
		c.Names = append([]string(nil), t.Names...)
	}
	return &c
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
		case "Rat":
			return tRat(), nil
		case "Float":
			return tFloat(), nil
		case "Bool":
			return tBool(), nil
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
		// A batch-scoped type alias expands to a fresh copy of its (already canonical)
		// type — identity-transparent sugar, so a def using the alias hashes identically
		// to one spelling the type inline. Checked after tyvars/dataSelf (a local tyvar
		// wins) and before stored data types (an alias may not shadow one — registration
		// rejects that).
		if t, ok := e.aliases[x.Sym]; ok {
			return cloneTy(t), nil
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
	"fp-eq":  2,
	"to-rat": 1, "to-float": 1, "floor": 1,
	// Crypto over byte lists (#78). The FIRST primitives taking an ADT argument.
	"hmac-sha256": 2, "bytes-eq-ct": 2,
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
	case "rat":
		return &Term{K: "rat", Rat: x.Rat}, nil
	case "float":
		return &Term{K: "float", Float: x.Float}, nil
	case "str":
		// String-literal sugar: "abc" elaborates to the codepoint chain
		// (SCons 97 (SCons 98 (SCons 99 (SNil)))). Str is an ordinary inductive
		// datatype of Unicode scalar values; there is no string primitive.
		sconsH, sconsIdx, ok1 := e.st.FindCtor("SCons")
		snilH, snilIdx, ok2 := e.st.FindCtor("SNil")
		if !ok1 || !ok2 {
			return nil, e.errAt(x, "string literals require the Str type (SNil and SCons) to be in scope")
		}
		runes := []rune(x.Str)
		acc := &Term{K: "ctor", Hash: snilH, Idx: snilIdx}
		for i := len(runes) - 1; i >= 0; i-- {
			acc = &Term{K: "ctor", Hash: sconsH, Idx: sconsIdx,
				Args: []Term{{K: "int", Int: big.NewInt(int64(runes[i]))}, *acc}}
		}
		return acc, nil
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
		// NESTED PATTERNS desugar to a fresh binder plus an inner match, so
		// `(Cons (MkRun n x) t)` becomes `(Cons g t)` with the body wrapped in
		// `(match g ((MkRun n x) <body>))`. This is a pure surface rewrite — the AST it
		// produces is identical to the hand-written two-step form, and since binder
		// NAMES are metadata (identity is de Bruijn), the fresh name changes no hash.
		// The inner match re-enters elabMatch, so nesting to any depth is handled by
		// recursion. The fresh name avoids every symbol the arm body mentions and every
		// name in scope, so it cannot capture a variable the body refers to.
		armBody := a.Kids[1]
		var binders []string
		// FAST PATH: a flat pattern (all binders are names) is the overwhelming majority,
		// and desugaring is unreachable for it — so skip building the avoidance set, which
		// would scan the scope and walk the whole arm body/pattern on every arm and make
		// deeply nested flat matches Θ(N²).
		hasNested := false
		for _, b := range pat.Kids[1:] {
			if b.K != "sym" {
				hasNested = true
				break
			}
		}
		if !hasNested {
			for _, b := range pat.Kids[1:] {
				binders = append(binders, b.Sym)
			}
		} else {
			// NESTED PATTERNS: the avoidance set keeps a generated name from colliding with
			// anything in scope, the arm body, or a sibling binder (which would capture the
			// wrong field).
			avoid := map[string]bool{}
			for _, s := range e.scope {
				avoid[s] = true
			}
			for _, s := range walkSyms(armBody) {
				avoid[s] = true
			}
			for _, s := range walkSyms(pat) {
				avoid[s] = true
			}
			type nestedPat struct {
				g   string
				pat sx
			}
			var nested []nestedPat
			for _, b := range pat.Kids[1:] {
				switch {
				case b.K == "sym":
					binders = append(binders, b.Sym)
				case b.K == "list" && len(b.Kids) > 0 && b.Kids[0].K == "sym":
					// A nested pattern desugars to a SINGLE-arm inner match, which is only
					// exhaustive — and only free of a duplicated outer arm — when its type has
					// exactly one constructor. A sum type would need every alternative under
					// this outer constructor grouped into one merged inner match (pattern-matrix
					// compilation); that is out of scope, so refuse it by name and point at the
					// two-step form rather than emit an unsound single-arm desugaring.
					ncn := b.Kids[0].Sym
					nh, _, nok := e.st.FindCtor(ncn)
					if !nok {
						return nil, e.errAt(b, "unknown constructor %q", ncn)
					}
					nd, err := e.st.GetDef(nh)
					if err != nil {
						return nil, err
					}
					if len(nd.Ctors) != 1 {
						return nil, e.errAt(b, "nested pattern %s has %d constructors; nested patterns are supported only for single-constructor (product) types — destructure %s in a separate match", ncn, len(nd.Ctors), ncn)
					}
					var g string
					for {
						e.gensym++
						g = fmt.Sprintf("__nest%d", e.gensym)
						if !avoid[g] {
							break
						}
					}
					avoid[g] = true
					binders = append(binders, g)
					nested = append(nested, nestedPat{g, b})
				default:
					return nil, e.errAt(b, "pattern binders must be names or nested (Ctor ...) patterns")
				}
			}
			// Wrap innermost-last so the first nested field's match is outermost, which
			// is the order the equivalent hand-written destructuring uses.
			for i := len(nested) - 1; i >= 0; i-- {
				np := nested[i]
				armBody = sx{K: "list", Line: np.pat.Line, Kids: []sx{
					{K: "sym", Sym: "match", Line: np.pat.Line},
					{K: "sym", Sym: np.g, Line: np.pat.Line},
					{K: "list", Line: np.pat.Line, Kids: []sx{np.pat, armBody}},
				}}
			}
		}
		e.scope = append(e.scope, binders...)
		body, err := e.elabTerm(armBody)
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
func elabDataRaw(st *Store, x sx, aliases map[string]*Ty) (*Def, *Meta, error) {
	if len(x.Kids) < 3 || x.Kids[1].K != "sym" || x.Kids[2].K != "brack" {
		return nil, nil, fmt.Errorf("line %d: data needs a name, [tyvars], and constructors", x.Line)
	}
	name := x.Kids[1].Sym
	tvs, err := tyvarNames(x.Kids[2])
	if err != nil {
		return nil, nil, err
	}
	e := &elab{st: st, tyvars: tvs, dataSelf: name, selfTyVars: len(tvs), aliases: aliases}
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
func elabFuncRaw(st *Store, x sx, aliases map[string]*Ty) (*Def, *Meta, error) {
	if len(x.Kids) < 6 || x.Kids[1].K != "sym" || x.Kids[2].K != "brack" || x.Kids[3].K != "brack" {
		return nil, nil, fmt.Errorf("line %d: defn needs name [tyvars] [(param ty)...] retTy body", x.Line)
	}
	name := x.Kids[1].Sym
	tvs, err := tyvarNames(x.Kids[2])
	if err != nil {
		return nil, nil, err
	}
	e := &elab{st: st, tyvars: tvs, funcSelf: name, selfTyVars: len(tvs), aliases: aliases}
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
		pe := &elab{st: st, funcSelf: name, selfTyVars: len(tvs), aliases: aliases}
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

// elabData and elabFunc wrap elaboration with ADMISSION (#149).
//
// The check lives HERE, at construction, and not at each call site. It was
// originally placed in apiPutSigned alone, which is where I happened to be
// looking — and external review found three other endpoints that elaborate
// user-supplied source and then typecheck or PROVE it (apiFindSpec,
// apiFindImplies, buildPublishPlan). A shallow source with a long string
// literal exceeds the node budget while passing the syntax-nesting limit, so
// those paths processed artifacts `put` correctly refuses.
//
// The claim quantifies over EVERY canonical structure the kernel builds from
// external input. Its universe is therefore the CONSTRUCTORS, not the callers a
// reader can enumerate — a list of call sites answers a different question and
// answers it completely.
func elabData(st *Store, x sx) (*Def, *Meta, error) { return elabDataWith(st, x, nil) }
func elabFunc(st *Store, x sx) (*Def, *Meta, error) { return elabFuncWith(st, x, nil) }

// elabDataWith / elabFuncWith elaborate a form with a batch's type aliases in scope.
// The no-alias forms above are the single-form entry points (queries, find-spec) that
// never see a (type …) declaration; the put loop uses these.
func elabDataWith(st *Store, x sx, aliases map[string]*Ty) (*Def, *Meta, error) {
	d, m, err := elabDataRaw(st, x, aliases)
	if err != nil {
		return d, m, err
	}
	return d, m, admitDef(d)
}

func elabFuncWith(st *Store, x sx, aliases map[string]*Ty) (*Def, *Meta, error) {
	d, m, err := elabFuncRaw(st, x, aliases)
	if err != nil {
		return d, m, err
	}
	return d, m, admitDef(d)
}

// registerTypeAlias handles a (type Name ty) top-level form: it binds Name to the
// canonical elaboration of ty in the batch-scoped alias map. An alias is
// identity-transparent surface sugar — it produces no stored object and no journal
// entry — so it must not shadow anything that DOES have identity: a builtin, a stored
// data type, or an earlier alias. The body is elaborated with no type variables in
// scope, so it must be a GROUND type (a tyvar reference fails as an unknown type).
// dataNameConflict rejects a (data Name ...) whose name IS an alias in the same batch:
// bare uses of that name resolve to the alias (checked first in parseTy), which would
// silently shadow the stored data type. Registration guards the other order (an alias
// over an existing data type). Returns nil (no line prefix) when there is no conflict;
// callers add their own location.
func dataNameConflict(name string, aliases map[string]*Ty) error {
	if _, isAlias := aliases[name]; isAlias {
		return fmt.Errorf("%q is already a type alias in this batch", name)
	}
	return nil
}

func registerTypeAlias(st *Store, x sx, aliases map[string]*Ty) error {
	if len(x.Kids) != 3 || x.Kids[1].K != "sym" {
		return fmt.Errorf("line %d: type alias must be (type Name ty)", x.Line)
	}
	name := x.Kids[1].Sym
	switch name {
	case "Int", "Rat", "Float", "Bool":
		return fmt.Errorf("line %d: %q is a builtin type and cannot be an alias", x.Line, name)
	}
	if _, ok := aliases[name]; ok {
		return fmt.Errorf("line %d: type alias %q is already defined in this batch", x.Line, name)
	}
	if h, ok := st.Resolve(name); ok {
		if d, err := st.GetDef(h); err == nil && d.K == "data" {
			return fmt.Errorf("line %d: %q is a data type and cannot be an alias", x.Line, name)
		}
	}
	e := &elab{st: st, aliases: aliases}
	ty, err := e.parseTy(x.Kids[2])
	if err != nil {
		return err
	}
	aliases[name] = ty
	return nil
}
