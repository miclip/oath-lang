package main

import "fmt"

// The typechecker is the trusted kernel. Everything is explicitly annotated,
// so checking is pure structural synthesis with no inference, no unification,
// and no constraint solving — a deliberate trade: annotations are cheap for a
// machine author, and a checker this small is easy to trust and easy to port.

type checker struct {
	st         *Store
	selfTyVars int
	selfTy     *Ty // declared type of the func being checked; nil outside a func def
}

// substTy replaces type variables with the given arguments.
func substTy(t *Ty, args []Ty) *Ty {
	switch t.K {
	case "int", "bool", "str":
		return t
	case "var":
		if t.Var < len(args) {
			c := args[t.Var]
			return &c
		}
		return t
	case "fun":
		return tFun(substTy(t.A, args), substTy(t.B, args))
	case "data", "rec", "record":
		as := make([]Ty, len(t.Args))
		for i := range t.Args {
			as[i] = *substTy(&t.Args[i], args)
		}
		out := *t
		out.Args = as
		return &out
	}
	return t
}

// resolveRec replaces "rec" (the ADT being defined) with a concrete data
// reference once the ADT's hash is known.
func resolveRec(t *Ty, h string) *Ty {
	switch t.K {
	case "int", "bool", "str", "var":
		return t
	case "fun":
		return tFun(resolveRec(t.A, h), resolveRec(t.B, h))
	case "data", "rec", "record":
		as := make([]Ty, len(t.Args))
		for i := range t.Args {
			as[i] = *resolveRec(&t.Args[i], h)
		}
		if t.K == "rec" {
			return tDataTy(h, as)
		}
		out := *t
		out.Args = as
		return &out
	}
	return t
}

func tyEq(a, b *Ty) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.K != b.K || a.Var != b.Var || a.Hash != b.Hash || len(a.Args) != len(b.Args) || len(a.Names) != len(b.Names) {
		return false
	}
	for i := range a.Names {
		if a.Names[i] != b.Names[i] {
			return false
		}
	}
	if !tyEq(a.A, b.A) || !tyEq(a.B, b.B) {
		return false
	}
	for i := range a.Args {
		if !tyEq(&a.Args[i], &b.Args[i]) {
			return false
		}
	}
	return true
}

func tyHasFun(t *Ty) bool {
	if t == nil {
		return false
	}
	if t.K == "fun" {
		return true
	}
	for i := range t.Args {
		if tyHasFun(&t.Args[i]) {
			return true
		}
	}
	return tyHasFun(t.A) || tyHasFun(t.B)
}

func tyIsConcrete(t *Ty) bool {
	if t == nil {
		return true
	}
	if t.K == "var" || t.K == "rec" {
		return false
	}
	for i := range t.Args {
		if !tyIsConcrete(&t.Args[i]) {
			return false
		}
	}
	return tyIsConcrete(t.A) && tyIsConcrete(t.B)
}

// checkTyWF validates a type: variable indices in range, data references
// exist with matching arity, "rec" only where allowed.
func checkTyWF(st *Store, t *Ty, ntyvars int, allowRec bool) error {
	switch t.K {
	case "int", "bool", "str":
		return nil
	case "record":
		if len(t.Names) != len(t.Args) {
			return fmt.Errorf("record field names and types out of sync")
		}
		for i := range t.Names {
			if i > 0 && t.Names[i] <= t.Names[i-1] {
				return fmt.Errorf("record fields must be sorted and unique (canonical form): %q after %q", t.Names[i], t.Names[i-1])
			}
			if err := checkTyWF(st, &t.Args[i], ntyvars, allowRec); err != nil {
				return err
			}
		}
		return nil
	case "var":
		if t.Var < 0 || t.Var >= ntyvars {
			return fmt.Errorf("type variable %d out of range (%d in scope)", t.Var, ntyvars)
		}
		return nil
	case "fun":
		if err := checkTyWF(st, t.A, ntyvars, allowRec); err != nil {
			return err
		}
		return checkTyWF(st, t.B, ntyvars, allowRec)
	case "rec":
		if !allowRec {
			return fmt.Errorf("self-referential type outside a data definition")
		}
		if len(t.Args) != ntyvars {
			return fmt.Errorf("self-referential type applied to %d arguments, expected %d", len(t.Args), ntyvars)
		}
		for i := range t.Args {
			if err := checkTyWF(st, &t.Args[i], ntyvars, allowRec); err != nil {
				return err
			}
		}
		return nil
	case "data":
		d, err := st.GetDef(t.Hash)
		if err != nil {
			return err
		}
		if d.K != "data" {
			return fmt.Errorf("%s is not a data definition", shortHash(t.Hash))
		}
		if len(t.Args) != d.TyVars {
			return fmt.Errorf("data %s applied to %d type arguments, expected %d", shortHash(t.Hash), len(t.Args), d.TyVars)
		}
		for i := range t.Args {
			if err := checkTyWF(st, &t.Args[i], ntyvars, allowRec); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown type form %q", t.K)
}

// instCtorFields instantiates constructor idx of data def d (hash h) at the
// given type arguments, resolving self-references.
func instCtorFields(d *Def, h string, tyargs []Ty, idx int) []*Ty {
	fields := d.Ctors[idx]
	out := make([]*Ty, len(fields))
	for i := range fields {
		out[i] = resolveRec(substTy(&fields[i], tyargs), h)
	}
	return out
}

func pushCtx(ctx []*Ty, t *Ty) []*Ty {
	out := make([]*Ty, len(ctx)+1)
	copy(out, ctx)
	out[len(ctx)] = t
	return out
}

// synth computes the type of a term, or fails. ctx is the de Bruijn context:
// ctx[len-1] is Var 0.
func (c *checker) synth(ctx []*Ty, t *Term) (*Ty, error) {
	switch t.K {
	case "var":
		if t.Idx < 0 || t.Idx >= len(ctx) {
			return nil, fmt.Errorf("variable index %d out of scope", t.Idx)
		}
		return ctx[len(ctx)-1-t.Idx], nil
	case "int":
		return tInt(), nil
	case "bool":
		return tBool(), nil
	case "str":
		return tStr(), nil
	case "record":
		if len(t.Names) != len(t.Args) {
			return nil, fmt.Errorf("record field names and values out of sync")
		}
		out := &Ty{K: "record", Names: append([]string{}, t.Names...)}
		for i := range t.Args {
			if i > 0 && t.Names[i] <= t.Names[i-1] {
				return nil, fmt.Errorf("record fields must be sorted and unique (canonical form)")
			}
			ft, err := c.synth(ctx, &t.Args[i])
			if err != nil {
				return nil, err
			}
			out.Args = append(out.Args, *ft)
		}
		return out, nil
	case "field":
		rt, err := c.synth(ctx, t.A)
		if err != nil {
			return nil, err
		}
		if rt.K != "record" {
			return nil, fmt.Errorf("field access on non-record (type %s)", debugTy(rt))
		}
		for i, n := range rt.Names {
			if n == t.Op {
				return &rt.Args[i], nil
			}
		}
		return nil, fmt.Errorf("record %s has no field %q", debugTy(rt), t.Op)
	case "lam":
		if err := checkTyWF(c.st, t.Ty, c.selfTyVars, false); err != nil {
			return nil, err
		}
		bt, err := c.synth(pushCtx(ctx, t.Ty), t.A)
		if err != nil {
			return nil, err
		}
		return tFun(t.Ty, bt), nil
	case "app":
		ft, err := c.synth(ctx, t.A)
		if err != nil {
			return nil, err
		}
		if ft.K != "fun" {
			return nil, fmt.Errorf("applied a non-function (type %s)", debugTy(ft))
		}
		at, err := c.synth(ctx, t.B)
		if err != nil {
			return nil, err
		}
		if !tyEq(at, ft.A) {
			return nil, fmt.Errorf("argument type mismatch: expected %s, got %s", debugTy(ft.A), debugTy(at))
		}
		return ft.B, nil
	case "let":
		if err := checkTyWF(c.st, t.Ty, c.selfTyVars, false); err != nil {
			return nil, err
		}
		et, err := c.synth(ctx, t.A)
		if err != nil {
			return nil, err
		}
		if !tyEq(et, t.Ty) {
			return nil, fmt.Errorf("let annotation mismatch: declared %s, got %s", debugTy(t.Ty), debugTy(et))
		}
		return c.synth(pushCtx(ctx, t.Ty), t.B)
	case "if":
		ct, err := c.synth(ctx, t.A)
		if err != nil {
			return nil, err
		}
		if ct.K != "bool" {
			return nil, fmt.Errorf("if condition must be Bool, got %s", debugTy(ct))
		}
		tt, err := c.synth(ctx, t.B)
		if err != nil {
			return nil, err
		}
		et, err := c.synth(ctx, t.C)
		if err != nil {
			return nil, err
		}
		if !tyEq(tt, et) {
			return nil, fmt.Errorf("if branches disagree: %s vs %s", debugTy(tt), debugTy(et))
		}
		return tt, nil
	case "prim":
		return c.synthPrim(ctx, t)
	case "ref":
		d, err := c.st.GetDef(t.Hash)
		if err != nil {
			return nil, err
		}
		if d.K != "func" {
			return nil, fmt.Errorf("%s is a data definition, not a term", shortHash(t.Hash))
		}
		if len(t.TyArgs) != d.TyVars {
			return nil, fmt.Errorf("reference to %s given %d type arguments, expected %d", shortHash(t.Hash), len(t.TyArgs), d.TyVars)
		}
		for i := range t.TyArgs {
			if err := checkTyWF(c.st, &t.TyArgs[i], c.selfTyVars, false); err != nil {
				return nil, err
			}
		}
		return substTy(d.Ty, t.TyArgs), nil
	case "self":
		if c.selfTy == nil {
			return nil, fmt.Errorf("self-reference outside a function definition")
		}
		if len(t.TyArgs) != c.selfTyVars {
			return nil, fmt.Errorf("self-reference given %d type arguments, expected %d", len(t.TyArgs), c.selfTyVars)
		}
		for i := range t.TyArgs {
			if err := checkTyWF(c.st, &t.TyArgs[i], c.selfTyVars, false); err != nil {
				return nil, err
			}
		}
		return substTy(c.selfTy, t.TyArgs), nil
	case "ctor":
		d, err := c.st.GetDef(t.Hash)
		if err != nil {
			return nil, err
		}
		if d.K != "data" {
			return nil, fmt.Errorf("%s is not a data definition", shortHash(t.Hash))
		}
		if t.Idx < 0 || t.Idx >= len(d.Ctors) {
			return nil, fmt.Errorf("constructor index %d out of range", t.Idx)
		}
		if len(t.TyArgs) != d.TyVars {
			return nil, fmt.Errorf("constructor given %d type arguments, expected %d", len(t.TyArgs), d.TyVars)
		}
		for i := range t.TyArgs {
			if err := checkTyWF(c.st, &t.TyArgs[i], c.selfTyVars, false); err != nil {
				return nil, err
			}
		}
		fields := instCtorFields(d, t.Hash, t.TyArgs, t.Idx)
		if len(t.Args) != len(fields) {
			return nil, fmt.Errorf("constructor takes %d arguments, got %d", len(fields), len(t.Args))
		}
		for i := range t.Args {
			at, err := c.synth(ctx, &t.Args[i])
			if err != nil {
				return nil, err
			}
			if !tyEq(at, fields[i]) {
				return nil, fmt.Errorf("constructor field %d: expected %s, got %s", i, debugTy(fields[i]), debugTy(at))
			}
		}
		return tDataTy(t.Hash, t.TyArgs), nil
	case "match":
		st, err := c.synth(ctx, t.A)
		if err != nil {
			return nil, err
		}
		if st.K != "data" {
			return nil, fmt.Errorf("match scrutinee must be a data value, got %s", debugTy(st))
		}
		if t.Hash != "" && t.Hash != st.Hash {
			return nil, fmt.Errorf("match arms are for a different data type than the scrutinee")
		}
		d, err := c.st.GetDef(st.Hash)
		if err != nil {
			return nil, err
		}
		if len(t.Arms) != len(d.Ctors) {
			return nil, fmt.Errorf("match has %d arms, data type has %d constructors", len(t.Arms), len(d.Ctors))
		}
		var result *Ty
		for i := range t.Arms {
			armCtx := ctx
			for _, f := range instCtorFields(d, st.Hash, st.Args, i) {
				armCtx = pushCtx(armCtx, f)
			}
			at, err := c.synth(armCtx, &t.Arms[i])
			if err != nil {
				return nil, err
			}
			if result == nil {
				result = at
			} else if !tyEq(result, at) {
				return nil, fmt.Errorf("match arms disagree: %s vs %s", debugTy(result), debugTy(at))
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("unknown term form %q", t.K)
}

func (c *checker) synthPrim(ctx []*Ty, t *Term) (*Ty, error) {
	argTys := make([]*Ty, len(t.Args))
	for i := range t.Args {
		at, err := c.synth(ctx, &t.Args[i])
		if err != nil {
			return nil, err
		}
		argTys[i] = at
	}
	need := func(n int) error {
		if len(argTys) != n {
			return fmt.Errorf("primitive %s takes %d arguments, got %d", t.Op, n, len(argTys))
		}
		return nil
	}
	allInt := func() error {
		for _, a := range argTys {
			if a.K != "int" {
				return fmt.Errorf("primitive %s requires Int arguments, got %s", t.Op, debugTy(a))
			}
		}
		return nil
	}
	allBool := func() error {
		for _, a := range argTys {
			if a.K != "bool" {
				return fmt.Errorf("primitive %s requires Bool arguments, got %s", t.Op, debugTy(a))
			}
		}
		return nil
	}
	allStr := func() error {
		for _, a := range argTys {
			if a.K != "str" {
				return fmt.Errorf("primitive %s requires Str arguments, got %s", t.Op, debugTy(a))
			}
		}
		return nil
	}
	switch t.Op {
	case "++":
		if err := need(2); err != nil {
			return nil, err
		}
		if err := allStr(); err != nil {
			return nil, err
		}
		return tStr(), nil
	case "str-len":
		if err := need(1); err != nil {
			return nil, err
		}
		if err := allStr(); err != nil {
			return nil, err
		}
		return tInt(), nil
	case "+", "-", "*", "/", "%":
		if err := need(2); err != nil {
			return nil, err
		}
		if err := allInt(); err != nil {
			return nil, err
		}
		return tInt(), nil
	case "neg":
		if err := need(1); err != nil {
			return nil, err
		}
		if err := allInt(); err != nil {
			return nil, err
		}
		return tInt(), nil
	case "<", "<=":
		if err := need(2); err != nil {
			return nil, err
		}
		if err := allInt(); err != nil {
			return nil, err
		}
		return tBool(), nil
	case "and", "or":
		if err := need(2); err != nil {
			return nil, err
		}
		if err := allBool(); err != nil {
			return nil, err
		}
		return tBool(), nil
	case "not":
		if err := need(1); err != nil {
			return nil, err
		}
		if err := allBool(); err != nil {
			return nil, err
		}
		return tBool(), nil
	case "==":
		if err := need(2); err != nil {
			return nil, err
		}
		if !tyEq(argTys[0], argTys[1]) {
			return nil, fmt.Errorf("== requires both sides the same type: %s vs %s", debugTy(argTys[0]), debugTy(argTys[1]))
		}
		if tyHasFun(argTys[0]) {
			return nil, fmt.Errorf("== is not defined on function types")
		}
		return tBool(), nil
	}
	return nil, fmt.Errorf("unknown primitive %q", t.Op)
}

// checkDef validates a whole definition against the kernel rules.
func checkDef(st *Store, d *Def) error {
	switch d.K {
	case "data":
		if len(d.Ctors) == 0 {
			return fmt.Errorf("data definition needs at least one constructor")
		}
		for ci, fields := range d.Ctors {
			for fi := range fields {
				if err := checkTyWF(st, &fields[fi], d.TyVars, true); err != nil {
					return fmt.Errorf("constructor %d field %d: %w", ci, fi, err)
				}
			}
		}
		return nil
	case "func":
		if d.Ty == nil || d.Body == nil {
			return fmt.Errorf("function definition needs a type and a body")
		}
		if err := checkTyWF(st, d.Ty, d.TyVars, false); err != nil {
			return err
		}
		c := &checker{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
		got, err := c.synth(nil, d.Body)
		if err != nil {
			return err
		}
		if !tyEq(got, d.Ty) {
			return fmt.Errorf("body has type %s, declared %s", debugTy(got), debugTy(d.Ty))
		}
		for pi, p := range d.Props {
			var ctx []*Ty
			for bi := range p.Binders {
				b := &p.Binders[bi]
				if !tyIsConcrete(b) {
					return fmt.Errorf("property %d: binders must be concrete types so inputs can be generated", pi)
				}
				if err := checkTyWF(st, b, 0, false); err != nil {
					return fmt.Errorf("property %d binder %d: %w", pi, bi, err)
				}
				ctx = pushCtx(ctx, b)
			}
			pt, err := c.synth(ctx, &p.Body)
			if err != nil {
				return fmt.Errorf("property %d: %w", pi, err)
			}
			if pt.K != "bool" {
				return fmt.Errorf("property %d must be Bool, got %s", pi, debugTy(pt))
			}
		}
		return nil
	}
	return fmt.Errorf("unknown definition form %q", d.K)
}

// debugTy renders a type for error messages without needing store metadata.
func debugTy(t *Ty) string {
	if t == nil {
		return "<nil>"
	}
	switch t.K {
	case "int":
		return "Int"
	case "bool":
		return "Bool"
	case "str":
		return "Str"
	case "record":
		s := "{"
		for i, n := range t.Names {
			if i > 0 {
				s += " "
			}
			s += n + " " + debugTy(&t.Args[i])
		}
		return s + "}"
	case "var":
		return fmt.Sprintf("t%d", t.Var)
	case "fun":
		return "(-> " + debugTy(t.A) + " " + debugTy(t.B) + ")"
	case "data":
		s := "#" + shortHash(t.Hash)
		for i := range t.Args {
			s += " " + debugTy(&t.Args[i])
		}
		if len(t.Args) > 0 {
			return "(" + s + ")"
		}
		return s
	case "rec":
		s := "self"
		for i := range t.Args {
			s += " " + debugTy(&t.Args[i])
		}
		if len(t.Args) > 0 {
			return "(" + s + ")"
		}
		return s
	}
	return "?"
}
