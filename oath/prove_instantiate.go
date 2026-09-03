package main

import "fmt"

// DETERMINISTIC INSTANTIATION of function defining equations for a structural-
// induction subgoal (docs/deterministic-instantiation.md).
//
// z3's search is NON-MONOTONE in its axiom set, and on COMPOSED RECURSION — an
// inductive goal that unfolds a recursive evaluator over terms built by another
// recursive function — the quantified defining-equation axioms do not e-match to
// a proof: measured, the Mul subgoals of both `opt.preserves` and
// `swap.preserves` exhaust a 15,000,000 rlimit and return `unknown`. Replacing
// those quantified axioms with GROUND INSTANCES at the terms the subgoal
// actually mentions discharges them at 5,183 and 1,847 rlimit respectively, with
// a wrong-transform control returning `sat` in both cases (no fabrication).
//
// SOUNDNESS. A ground instance of `f(pattern) = body` is a theorem of the
// definition IFF `f` is defined at that point; instantiating a PARTIAL
// function's equation at an undefined argument asserts a false equation and lets
// z3 discharge anything. The gate is TOTALITY, and it is inherited rather than
// restated: `fnEqn` metadata is recorded only on the branch of ensureFn that
// already decided `isTotal(tm.Termination)` and emitted a quantified axiom. A
// function with no quantified axiom therefore has no substitution-ready data and
// cannot be instantiated, by construction rather than by a second check that
// could drift from the first.
//
// FAIL-SAFE DIRECTION. Every step below returns "no instances" rather than a
// guess when the shape is not the one it recognises. A missed composition costs
// a proof the kernel does not have today; it can never produce a wrong instance.
// The instantiated attempt is a PREVIEW: only `unsat` is taken from it, and the
// unchanged quantified induction attempt runs on every other result.

// fnEqn is the substitution-ready form of an axiomatized function's defining
// equation: everything ensureFn used to build the quantified axiom, kept so a
// GROUND instance can be produced by re-entering the same translation at a
// different point. The instance is therefore a substitution into the very
// equation it replaces — it cannot drift from it, because it is translated by
// the same code from the same body.
type fnEqn struct {
	hash     string   // self-frame hash, for translating the body in its own frame
	def      *Def     // self-frame definition
	name     string   // the SMT symbol, e.g. fn_ev
	argSorts []string // parameter sorts, in order
	ret      string   // result sort
	bodyTerm *Term    // the body with type arguments substituted and lambdas stripped
	axiomIdx int      // index into smtCtx.axioms of the quantified equation
}

// groundTerm is an SMT term this strategy may instantiate at, carried with the
// structure needed to fold a match over it WITHOUT a term simplifier. When
// ctorIdx >= 0 the term is a constructor application of the induction datatype
// and args are its field values, so selecting a function's match arm and binding
// the fields directly IS the folded right-hand side — selector-on-constructor,
// which is a datatype axiom, applied structurally instead of textually.
type groundTerm struct {
	expr    string
	sort    string
	ctorIdx int
	args    []smtVal
}

func atom(expr, sort string) groundTerm {
	return groundTerm{expr: expr, sort: sort, ctorIdx: -1}
}

// instantiateAt emits one ground instance of fe's defining equation at gt, and
// returns the right-hand side as a groundTerm when the RHS is itself a
// constructor application (so a further instance can be folded against it).
//
// The two paths differ only in how much folding the datatype structure licenses:
// with a known constructor the selected arm is translated against the field
// values (the folded form the experiment's simplifier had to compute textually);
// otherwise the whole body is translated at the term, which yields the
// tester/selector `ite` chain — equally sound, and exactly what is wanted at a
// term like `(fn_swap f0)` whose constructor is unknown.
func (c *smtCtx) instantiateAt(fe *fnEqn, gt groundTerm) (string, groundTerm, bool) {
	if len(fe.argSorts) != 1 || fe.argSorts[0] != gt.sort {
		return "", groundTerm{}, false
	}
	body := fe.bodyTerm
	env := []smtVal{{expr: gt.expr, sort: gt.sort}}
	// Arm selection is meaningful only when the body matches on its OWN
	// parameter. In the body's frame env is [p0] and tr resolves a var by
	// env[len(env)-1-Idx], so the parameter is de Bruijn index 0.
	folded := false
	if gt.ctorIdx >= 0 && body != nil && body.K == "match" && body.A != nil &&
		body.A.K == "var" && body.A.Idx == 0 && gt.ctorIdx < len(body.Arms) {
		body = &body.Arms[gt.ctorIdx]
		env = append(env, gt.args...)
		folded = true
	}
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = fe.def, fe.hash
	rhs, _, err := c.tr(body, env)
	c.selfDef, c.selfHash = saveDef, saveHash
	if err != nil {
		return "", groundTerm{}, false
	}
	out := atom(rhs, fe.ret)
	if folded && body.K == "ctor" {
		// The arm rebuilds a constructor: record its index and field values so a
		// function instantiated AT this term can fold in turn.
		out.ctorIdx = body.Idx
		out.args = nil
		ok := true
		c.selfDef, c.selfHash = fe.def, fe.hash
		for i := range body.Args {
			a, as, err := c.tr(&body.Args[i], env)
			if err != nil {
				ok = false
				break
			}
			out.args = append(out.args, smtVal{expr: a, sort: as})
		}
		c.selfDef, c.selfHash = saveDef, saveHash
		if !ok {
			out = atom(rhs, fe.ret)
		}
	}
	return fmt.Sprintf("(assert (= (%s %s) %s))", fe.name, gt.expr, rhs), out, true
}

// composition is a nested call `outer(inner(b))` on the induction binder, found
// STRUCTURALLY in the property body. It is the shape the measurement covers: a
// recursive observer applied to the result of a recursive transformer.
type composition struct {
	outer *fnEqn
	inner *fnEqn
}

// findCompositions walks the property body for `outer(inner(b_i))` where both
// functions are axiomatized (hence total — see fnEqn) and b_i is the induction
// binder. It is a read-only structural walk: anything it does not recognise
// yields no composition, and no composition yields no instantiated attempt.
func (c *smtCtx) findCompositions(d *Def, h string, p *Prop, binder int) []composition {
	// In the property's frame env is [b0..bn-1]; tr resolves env[len-1-Idx], so
	// binder i is de Bruijn index len(binders)-1-i AT THE TOP LEVEL. Under any
	// binder the index shifts, so the walk carries a depth and compares against
	// want+depth. Getting this wrong could only select the wrong function PAIR —
	// the ground terms come from the induction loop, never from this walk — but a
	// wrong pair is a wasted attempt, so it is tracked rather than assumed away.
	want := len(p.Binders) - 1 - binder
	var out []composition
	seen := map[string]bool{}
	var walk func(t *Term, self *Def, selfHash string, depth int)
	walk = func(t *Term, self *Def, selfHash string, depth int) {
		if t == nil {
			return
		}
		if t.K == "app" {
			if oh, oargs := unwindApp(t); len(oargs) == 1 {
				if outer := c.eqnOf(oh, self, selfHash); outer != nil {
					if ih, iargs := unwindApp(oargs[0]); oargs[0].K == "app" && len(iargs) == 1 {
						if inner := c.eqnOf(ih, self, selfHash); inner != nil &&
							iargs[0].K == "var" && iargs[0].Idx == want+depth {
							key := outer.name + "\x00" + inner.name
							if !seen[key] {
								seen[key] = true
								out = append(out, composition{outer: outer, inner: inner})
							}
						}
					}
				}
			}
		}
		switch t.K {
		case "lam":
			walk(t.A, self, selfHash, depth+1)
			return
		case "let":
			walk(t.A, self, selfHash, depth)
			walk(t.B, self, selfHash, depth+1)
			return
		case "match":
			walk(t.A, self, selfHash, depth)
			// Arm i binds constructor i's fields, so each arm sits one binder
			// deeper per field of that constructor.
			var arity []int
			if md, err := c.st.GetDef(t.Hash); err == nil {
				for ci := range md.Ctors {
					arity = append(arity, len(md.Ctors[ci]))
				}
			}
			for i := range t.Arms {
				n := 0
				if i < len(arity) {
					n = arity[i]
				} else {
					return // unknown arity: stop rather than compare a shifted index
				}
				walk(&t.Arms[i], self, selfHash, depth+n)
			}
			return
		}
		for _, k := range []*Term{t.A, t.B, t.C} {
			walk(k, self, selfHash, depth)
		}
		for i := range t.Args {
			walk(&t.Args[i], self, selfHash, depth)
		}
	}
	walk(&p.Body, d, h, 0)
	return out
}

// eqnOf resolves an application head to its recorded defining equation, or nil
// when the head is not an axiomatized total function this context has declared.
func (c *smtCtx) eqnOf(head *Term, self *Def, selfHash string) *fnEqn {
	var h string
	var tyargs []Ty
	switch head.K {
	case "ref":
		h, tyargs = head.Hash, head.TyArgs
	case "self":
		h, tyargs = selfHash, head.TyArgs
	default:
		return nil
	}
	if h == "" {
		return nil
	}
	if i, ok := c.fnEqnByKey[tyKey(h, tyargs)]; ok {
		return &c.fnEqns[i]
	}
	return nil
}

// instantiatedSubgoal builds the ground-instance premises for one constructor
// case of structural induction, and the set of quantified axioms they replace.
//
// The instance schema, per composition `outer(inner(b))` at parent P = (C f0..fn):
//
//	inner @ P                 unfolds the transformer at the constructed value
//	outer @ P                 unfolds the observer at the constructed value
//	outer @ RHS(inner, P)     the observer at the transformer's RESULT
//	outer @ (inner fi)        for every RECURSIVE field fi
//
// The last row is what the induction hypotheses speak about — the loop asserts
// `outer(inner(fi)) = outer(fi)` — so these instances are the terms the subgoal
// ALREADY mentions, not an extra guess about shape. Measured: without them both
// scripts are `sat`, with an explicit countermodel that violates the observer's
// own equation at a point nothing pins.
//
// This is the measured-minimal premise set: the ablation in INSTANTIATION.md
// leaves exactly these (plus the two hypotheses) after dropping the individually
// redundant branch instances, and re-ablating each of the remainder returns
// `sat`.
func (c *smtCtx) instantiatedSubgoal(d *Def, h string, p *Prop, binder int, dt *dtInfo, ci int, fieldConsts []string) ([]string, map[int]bool) {
	comps := c.findCompositions(d, h, p, binder)
	if len(comps) == 0 {
		return nil, nil
	}
	parent := groundTerm{expr: dt.ctors[ci], sort: dt.name, ctorIdx: ci}
	if len(fieldConsts) > 0 {
		parent.expr = "(" + dt.ctors[ci]
		for i, fc := range fieldConsts {
			parent.expr += " " + fc
			parent.args = append(parent.args, smtVal{expr: fc, sort: dt.fields[ci][i]})
		}
		parent.expr += ")"
	}
	if len(parent.args) != len(dt.fields[ci]) {
		return nil, nil
	}

	var out []string
	omit := map[int]bool{}
	seen := map[string]bool{}
	add := func(fe *fnEqn, gt groundTerm) (groundTerm, bool) {
		a, rhs, ok := c.instantiateAt(fe, gt)
		if !ok {
			return groundTerm{}, false
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
		omit[fe.axiomIdx] = true
		return rhs, true
	}

	for _, comp := range comps {
		t, ok := add(comp.inner, parent)
		if !ok {
			continue
		}
		if _, ok := add(comp.outer, parent); !ok {
			continue
		}
		add(comp.outer, t)
		for fi, fs := range dt.fields[ci] {
			if fs != dt.name || fi >= len(fieldConsts) {
				continue
			}
			inner := fmt.Sprintf("(%s %s)", comp.inner.name, fieldConsts[fi])
			add(comp.outer, atom(inner, comp.inner.ret))
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, omit
}
