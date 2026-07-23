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
	if t == nil {
		return nil
	}
	switch t.K {
	case "int", "bool":
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
	case "int", "bool", "var":
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

// matchTy solves the type variables of a polymorphic pattern `pat` (whose var
// indices 0..len(subst)-1 are the variables being inferred) against a concrete
// type `got`, recording each solution in subst (nil = unsolved). It is ONE-SIDED:
// only pat's variables are solved; a variable appearing in `got` — an enclosing
// definition's type parameter — is an opaque constant that a pat variable may be
// solved to. Fails on a structural mismatch or a variable forced to two
// different types. This is the whole of the "inference/unification" that
// type-argument inference (#35) adds — it never unifies two unknowns.
func matchTy(pat, got *Ty, subst []*Ty) error {
	if pat == nil || got == nil {
		return fmt.Errorf("nil type in match")
	}
	if pat.K == "var" && pat.Var >= 0 && pat.Var < len(subst) {
		if subst[pat.Var] == nil {
			subst[pat.Var] = got
			return nil
		}
		if !tyEq(subst[pat.Var], got) {
			return fmt.Errorf("type argument %d cannot be both %s and %s", pat.Var, debugTy(subst[pat.Var]), debugTy(got))
		}
		return nil
	}
	if pat.K != got.K {
		return fmt.Errorf("expected %s, got %s", debugTy(pat), debugTy(got))
	}
	switch pat.K {
	case "int", "bool":
		return nil
	case "var":
		if pat.Var != got.Var {
			return fmt.Errorf("expected %s, got %s", debugTy(pat), debugTy(got))
		}
		return nil
	case "fun":
		if err := matchTy(pat.A, got.A, subst); err != nil {
			return err
		}
		return matchTy(pat.B, got.B, subst)
	case "data", "rec":
		if pat.Hash != got.Hash || len(pat.Args) != len(got.Args) {
			return fmt.Errorf("expected %s, got %s", debugTy(pat), debugTy(got))
		}
		for i := range pat.Args {
			if err := matchTy(&pat.Args[i], &got.Args[i], subst); err != nil {
				return err
			}
		}
		return nil
	case "record":
		if len(pat.Args) != len(got.Args) || len(pat.Names) != len(got.Names) {
			return fmt.Errorf("expected %s, got %s", debugTy(pat), debugTy(got))
		}
		for i := range pat.Names {
			if pat.Names[i] != got.Names[i] {
				return fmt.Errorf("record field name mismatch: %s vs %s", pat.Names[i], got.Names[i])
			}
		}
		for i := range pat.Args {
			if err := matchTy(&pat.Args[i], &got.Args[i], subst); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("cannot match type %s", debugTy(pat))
}

// inferReady reports whether a polymorphic reference node carries no explicit
// type arguments and therefore needs them inferred.
func inferReady(tyvars int, tyargs []Ty) bool { return tyvars > 0 && len(tyargs) == 0 }

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
	// Total on malformed input: a Def loaded from the store may have missing
	// children (e.g. a `fun` type with no domain/codomain). The trusted checker
	// path must return an error, never fault, so GetDef can reject the object.
	if t == nil {
		return fmt.Errorf("missing type")
	}
	switch t.K {
	case "int", "bool":
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

// strictlyPositiveTy rejects a datatype whose self-reference ("rec") occurs in
// a negative position — to the left of a function arrow, directly or through a
// container that could use its parameter negatively. Negative datatypes such
// as `data D = C (D -> D)` re-introduce nontermination with NO self-recursion,
// which the structural termination checker cannot see, so it would wrongly
// bless a diverging function as `total`. Strict positivity is what makes the
// `total` verdict honest for datatype-mediated recursion.
//
// Polarity flips on each function domain; codomain preserves it. A self-ref
// passed as an argument to another datatype keeps its polarity only if that
// datatype is transitively arrow-free (hence covariant in every parameter);
// otherwise it is conservatively treated as negative. This over-rejects the
// unusual covariant-through-an-arrow container, which is acceptable: the common
// shapes (List, Maybe, pairs, trees, records of data) are arrow-free.
func strictlyPositiveTy(st *Store, t *Ty, negative bool) error {
	if t == nil {
		return nil
	}
	switch t.K {
	case "rec":
		if negative {
			return fmt.Errorf("datatype is not strictly positive: self-reference in a negative position (left of ->)")
		}
		for i := range t.Args {
			if err := strictlyPositiveTy(st, &t.Args[i], negative); err != nil {
				return err
			}
		}
		return nil
	case "fun":
		if err := strictlyPositiveTy(st, t.A, !negative); err != nil {
			return err
		}
		return strictlyPositiveTy(st, t.B, negative)
	case "record":
		for i := range t.Args {
			if err := strictlyPositiveTy(st, &t.Args[i], negative); err != nil {
				return err
			}
		}
		return nil
	case "data":
		neg := negative
		if !dataArrowFree(st, t.Hash, map[string]bool{}) {
			neg = true
		}
		for i := range t.Args {
			if err := strictlyPositiveTy(st, &t.Args[i], neg); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// dataArrowFree reports whether a datatype's constructors contain no function
// type anywhere (transitively through referenced datatypes). Such a datatype
// is covariant in every parameter. Unknown/unreadable references are treated
// as not-arrow-free (conservative). Content addressing makes the reference
// graph acyclic, so the recursion terminates; `seen` guards defensively.
func dataArrowFree(st *Store, h string, seen map[string]bool) bool {
	if h == "" || seen[h] {
		return true
	}
	seen[h] = true
	d, err := st.GetDef(h)
	if err != nil || d.K != "data" {
		return false
	}
	arrow := false
	var walk func(t *Ty)
	walk = func(t *Ty) {
		if t == nil || arrow {
			return
		}
		switch t.K {
		case "fun":
			arrow = true
		case "data":
			if !dataArrowFree(st, t.Hash, seen) {
				arrow = true
			}
			for i := range t.Args {
				walk(&t.Args[i])
			}
		default:
			walk(t.A)
			walk(t.B)
			for i := range t.Args {
				walk(&t.Args[i])
			}
		}
	}
	for _, c := range d.Ctors {
		for i := range c {
			walk(&c[i])
		}
	}
	return !arrow
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
	// Total on malformed input: a Def loaded from the store may have missing
	// required children (a `field` with no record, an `if` with no else, an
	// `app` with no function or argument). Reject rather than fault so that
	// GetDef's revalidation can turn a bad object into an error, not a panic.
	if t == nil {
		return nil, fmt.Errorf("missing term")
	}
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
		// A polymorphic ref/self head with omitted type arguments (#35) is
		// inferred from the whole application spine before the ordinary rule.
		if head, args := spine(t); head.K == "ref" || head.K == "self" {
			if got, ok, err := c.tryInferApp(ctx, head, args, nil); ok {
				return got, err
			}
		}
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
		return c.synthCtor(ctx, t, nil)
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

// identityTyArgs is [var 0, …, var n-1] — the type variables as themselves, for
// instantiating a polymorphic type without substituting anything.
func identityTyArgs(n int) []Ty {
	out := make([]Ty, n)
	for i := range out {
		out[i] = Ty{K: "var", Var: i}
	}
	return out
}

// spine flattens an application chain into its head and argument terms in
// application order: (((f a) b) c) → (f, [a, b, c]).
func spine(t *Term) (*Term, []*Term) {
	var args []*Term
	for t.K == "app" {
		args = append([]*Term{t.B}, args...)
		t = t.A
	}
	return t, args
}

func headName(head *Term) string {
	if head.K == "self" {
		return "the recursive call"
	}
	return shortHash(head.Hash)
}

// synthCtor synthesizes a constructor application, inferring omitted type
// arguments (#35): the constructor's data type is matched against the expected
// type `exp` (if any) and its field types against the synthesizable arguments;
// the solved arguments are backfilled into the node, then every argument is
// checked against its now-concrete field type (so a nested `(Nil)` infers).
func (c *checker) synthCtor(ctx []*Ty, t *Term, exp *Ty) (*Ty, error) {
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
	rawFields := instCtorFields(d, t.Hash, identityTyArgs(d.TyVars), t.Idx)
	if len(t.Args) != len(rawFields) {
		return nil, fmt.Errorf("constructor takes %d arguments, got %d", len(rawFields), len(t.Args))
	}
	if inferReady(d.TyVars, t.TyArgs) {
		subst := make([]*Ty, d.TyVars)
		if exp != nil {
			_ = matchTy(tDataTy(t.Hash, identityTyArgs(d.TyVars)), exp, subst)
		}
		argTys := make([]*Ty, len(t.Args))
		for i := range t.Args {
			if at, e := c.synth(ctx, &t.Args[i]); e == nil {
				argTys[i] = at
				_ = matchTy(rawFields[i], at, subst)
			}
		}
		for i, s := range subst {
			if s == nil {
				return nil, fmt.Errorf("cannot infer type argument %d for constructor %s — add [types] or determining arguments", i, shortHash(t.Hash))
			}
		}
		t.TyArgs = make([]Ty, d.TyVars)
		for i := range subst {
			t.TyArgs[i] = *subst[i]
		}
	} else if len(t.TyArgs) != d.TyVars {
		return nil, fmt.Errorf("constructor given %d type arguments, expected %d", len(t.TyArgs), d.TyVars)
	} else {
		for i := range t.TyArgs {
			if err := checkTyWF(c.st, &t.TyArgs[i], c.selfTyVars, false); err != nil {
				return nil, err
			}
		}
	}
	fields := instCtorFields(d, t.Hash, t.TyArgs, t.Idx)
	for i := range t.Args {
		if err := c.check(ctx, &t.Args[i], fields[i]); err != nil {
			return nil, err
		}
	}
	return tDataTy(t.Hash, t.TyArgs), nil
}

// tryInferApp infers omitted type arguments for an application whose head is a
// polymorphic `ref`/`self` (#35): it peels one parameter per applied argument,
// solves the type variables by matching parameter types against synthesizable
// argument types and (in check mode) the result type against `exp`, backfills
// the head, and checks each argument against its now-concrete parameter type.
// handled=false means the head needs no inference; the caller falls back.
func (c *checker) tryInferApp(ctx []*Ty, head *Term, args []*Term, exp *Ty) (res *Ty, handled bool, err error) {
	var raw *Ty
	var nvars int
	switch head.K {
	case "ref":
		d, e := c.st.GetDef(head.Hash)
		if e != nil {
			return nil, false, e
		}
		if d.K != "func" || !inferReady(d.TyVars, head.TyArgs) {
			return nil, false, nil
		}
		raw, nvars = d.Ty, d.TyVars
	case "self":
		if c.selfTy == nil || !inferReady(c.selfTyVars, head.TyArgs) {
			return nil, false, nil
		}
		raw, nvars = c.selfTy, c.selfTyVars
	default:
		return nil, false, nil
	}
	paramTys := make([]*Ty, len(args))
	cur := raw
	for i := range args {
		if cur.K != "fun" {
			return nil, true, fmt.Errorf("%s applied to too many arguments", headName(head))
		}
		paramTys[i] = cur.A
		cur = cur.B
	}
	resultTy := cur
	subst := make([]*Ty, nvars)
	if exp != nil {
		_ = matchTy(resultTy, exp, subst)
	}
	argTys := make([]*Ty, len(args))
	for i := range args {
		if at, e := c.synth(ctx, args[i]); e == nil {
			argTys[i] = at
			_ = matchTy(paramTys[i], at, subst)
		}
	}
	for i, s := range subst {
		if s == nil {
			return nil, true, fmt.Errorf("cannot infer type argument %d for %s — add [types] or determining arguments", i, headName(head))
		}
	}
	tyargs := make([]Ty, nvars)
	for i := range subst {
		tyargs[i] = *subst[i]
	}
	head.TyArgs = tyargs
	for i := range args {
		solvedParam := substTy(paramTys[i], tyargs)
		if argTys[i] != nil {
			if !tyEq(argTys[i], solvedParam) {
				return nil, true, fmt.Errorf("argument %d: expected %s, got %s", i, debugTy(solvedParam), debugTy(argTys[i]))
			}
		} else if e := c.check(ctx, args[i], solvedParam); e != nil {
			return nil, true, e
		}
	}
	return substTy(resultTy, tyargs), true, nil
}

// check verifies t against the expected type exp, threading exp into the term so
// omitted type arguments (#35) can be inferred from context. It defaults to
// synthesize-then-compare; only the forms that can consume an expected type use
// it directly.
func (c *checker) check(ctx []*Ty, t *Term, exp *Ty) error {
	switch t.K {
	case "ctor":
		got, err := c.synthCtor(ctx, t, exp)
		if err != nil {
			return err
		}
		if !tyEq(got, exp) {
			return fmt.Errorf("expected %s, got %s", debugTy(exp), debugTy(got))
		}
		return nil
	case "lam":
		if exp != nil && exp.K == "fun" {
			if err := checkTyWF(c.st, t.Ty, c.selfTyVars, false); err != nil {
				return err
			}
			if !tyEq(t.Ty, exp.A) {
				return fmt.Errorf("lambda parameter %s does not match expected %s", debugTy(t.Ty), debugTy(exp.A))
			}
			return c.check(pushCtx(ctx, t.Ty), t.A, exp.B)
		}
	case "if":
		ct, err := c.synth(ctx, t.A)
		if err != nil {
			return err
		}
		if ct.K != "bool" {
			return fmt.Errorf("if condition must be Bool, got %s", debugTy(ct))
		}
		if err := c.check(ctx, t.B, exp); err != nil {
			return err
		}
		return c.check(ctx, t.C, exp)
	case "let":
		if err := checkTyWF(c.st, t.Ty, c.selfTyVars, false); err != nil {
			return err
		}
		if err := c.check(ctx, t.A, t.Ty); err != nil {
			return err
		}
		return c.check(pushCtx(ctx, t.Ty), t.B, exp)
	case "match":
		st, err := c.synth(ctx, t.A)
		if err != nil {
			return err
		}
		if st.K != "data" {
			return fmt.Errorf("match scrutinee must be a data value, got %s", debugTy(st))
		}
		if t.Hash != "" && t.Hash != st.Hash {
			return fmt.Errorf("match arms are for a different data type than the scrutinee")
		}
		d, err := c.st.GetDef(st.Hash)
		if err != nil {
			return err
		}
		if len(t.Arms) != len(d.Ctors) {
			return fmt.Errorf("match has %d arms, data type has %d constructors", len(t.Arms), len(d.Ctors))
		}
		for i := range t.Arms {
			armCtx := ctx
			for _, f := range instCtorFields(d, st.Hash, st.Args, i) {
				armCtx = pushCtx(armCtx, f)
			}
			if err := c.check(armCtx, &t.Arms[i], exp); err != nil {
				return err
			}
		}
		return nil
	case "app":
		if head, args := spine(t); head.K == "ref" || head.K == "self" {
			if got, ok, err := c.tryInferApp(ctx, head, args, exp); ok {
				if err != nil {
					return err
				}
				if !tyEq(got, exp) {
					return fmt.Errorf("expected %s, got %s", debugTy(exp), debugTy(got))
				}
				return nil
			}
		}
	}
	got, err := c.synth(ctx, t)
	if err != nil {
		return err
	}
	if !tyEq(got, exp) {
		return fmt.Errorf("expected %s, got %s", debugTy(exp), debugTy(got))
	}
	return nil
}

func (c *checker) synthPrim(ctx []*Ty, t *Term) (*Ty, error) {
	// `==` is polymorphic in its operand type, so an operand may be a bare
	// constructor — `(== xs (Nil))` — whose type arguments only the OTHER operand
	// determines (#35). Synthesize whichever operand can be, then CHECK the other
	// against it; the result is the same non-function type either way, so the
	// backfilled arguments do not depend on operand order.
	if t.Op == "==" {
		if len(t.Args) != 2 {
			return nil, fmt.Errorf("primitive == takes 2 arguments, got %d", len(t.Args))
		}
		known, err := c.synth(ctx, &t.Args[0])
		other := &t.Args[1]
		if err != nil {
			known, err = c.synth(ctx, &t.Args[1])
			other = &t.Args[0]
			if err != nil {
				return nil, fmt.Errorf("cannot infer the type of == operands — annotate one side")
			}
		}
		if tyHasFun(known) {
			return nil, fmt.Errorf("== is not defined on function types")
		}
		if err := c.check(ctx, other, known); err != nil {
			return nil, err
		}
		return tBool(), nil
	}
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
	switch t.Op {
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
				if err := strictlyPositiveTy(st, &fields[fi], false); err != nil {
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
		// Check the body against the declared type (bidirectional, #35): the
		// expected type flows down so omitted type arguments can be inferred, and
		// the checker backfills every solved argument into the AST before it is
		// hashed — so an inferred call is byte-identical to the explicit one.
		if err := c.check(nil, d.Body, d.Ty); err != nil {
			return err
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
			if err := c.check(ctx, &p.Body, tBool()); err != nil {
				return fmt.Errorf("property %d: %w", pi, err)
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
