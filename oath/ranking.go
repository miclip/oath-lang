package main

import (
	"fmt"
	"strings"
)

// Ranking-function termination (#56). The structural checker (termination.go)
// only sees descent through datatype matching, so a function that recurses on an
// integer COUNTER — `replicate n x` on `n → n-1` guarded by `n>0`, `range lo hi`
// on `lo → lo+1` guarded by `lo<hi` — comes back "unknown", which makes the
// prover leave it uninterpreted and prove NOTHING about it (prove.go ensureFn).
//
// This closes that: a function is total if some well-founded integer MEASURE
// μ over its parameters strictly decreases and stays ≥ 0 at every self-call,
// given the path guards. We enumerate a small candidate set (each Int
// parameter, and each ordered difference of two Int parameters) and let Z3
// discharge, for every call site,
//
//     guards  ⟹  ( μ(args) < μ(params)  ∧  μ(params) ≥ 0 )
//
// A strictly-decreasing sequence of non-negative integers is finite, so a μ
// that clears every site witnesses termination — and because Z3 verifies it,
// the verdict is exactly as trustworthy as the PROVEN tier that depends on it.
//
// Soundness rests on COMPLETENESS of site collection: every self-call must be
// found and discharged, or the whole attempt fails. Anything the walker cannot
// fully analyze (a self-call reached as a value, under an untranslatable guard,
// or through a binder whose sort is unknown) sets `bad`, yielding a conservative
// "cannot prove" — never a false "total".

// rankSite is one self-call: the path guards reaching it and the SMT expression
// passed at each parameter position.
type rankSite struct {
	guards []string // SMT Bool exprs; their conjunction is the path condition
	args   []string // SMT exprs, one per parameter position (index = position)
}

type rankWalker struct {
	c       *smtCtx
	nparams int
	sites   []rankSite
	bad     bool
	fresh   int
}

// freshConst declares and returns a new SMT constant of the given sort, for a
// binder (match field, let, inner lambda) introduced inside the body.
func (w *rankWalker) freshConst(sort string) smtVal {
	name := fmt.Sprintf("b%d", w.fresh)
	w.fresh++
	w.c.decls = append(w.c.decls, fmt.Sprintf("(declare-const %s %s)", name, sort))
	return smtVal{expr: name, sort: sort}
}

// walk traverses the body. `spine` carries the argument terms of an application
// chain, so a chain that bottoms out at `self` yields the full argument list;
// `guards` is the accumulated path condition, `broken` records that some guard
// on this path could not be translated (any self-call under it is unprovable).
func (w *rankWalker) walk(t *Term, env []smtVal, guards []string, broken bool) {
	if t == nil || w.bad {
		return
	}
	switch t.K {
	case "self":
		w.recordSite(t, nil, env, guards, broken)
	case "app":
		w.walkApp(t, env, guards, broken)
	case "if":
		cnd, _, err := w.c.tr(t.A, env)
		thenGuards, elseGuards, thenBroken, elseBroken := guards, guards, broken, broken
		if err != nil {
			// A self-call under an untranslatable guard cannot be discharged.
			thenBroken, elseBroken = true, true
		} else {
			thenGuards = appendStr(guards, cnd)
			elseGuards = appendStr(guards, "(not "+cnd+")")
		}
		w.walk(t.A, env, guards, broken)
		w.walk(t.B, env, thenGuards, thenBroken)
		w.walk(t.C, env, elseGuards, elseBroken)
	case "let":
		bound, s, err := w.c.tr(t.A, env)
		var bv smtVal
		if err != nil {
			// Keep de Bruijn alignment but poison uses of this binder.
			bs := "Int"
			if t.Ty != nil {
				if so, e2 := w.c.sortOf(t.Ty); e2 == nil {
					bs = so
				}
			}
			bv = w.freshConst(bs)
		} else {
			bv = smtVal{expr: bound, sort: s}
		}
		w.walk(t.A, env, guards, broken)
		w.walk(t.B, append(cloneEnv(env), bv), guards, broken)
	case "lam":
		// A lambda inside the body (not a top-level parameter): its binder is a
		// value we cannot relate to the measure. Push a fresh const so a self
		// escaping into it fails at site recording.
		bs := "Int"
		if t.Ty != nil {
			if so, e2 := w.c.sortOf(t.Ty); e2 == nil {
				bs = so
			}
		}
		w.walk(t.A, append(cloneEnv(env), w.freshConst(bs)), guards, broken)
	case "match":
		w.walkMatch(t, env, guards, broken)
	default:
		// prim | ctor | record | field | ref | var | literals: no binders and no
		// path condition, but children may contain self-calls (e.g. a ctor arg).
		w.walk(t.A, env, guards, broken)
		w.walk(t.B, env, guards, broken)
		w.walk(t.C, env, guards, broken)
		for i := range t.Args {
			w.walk(&t.Args[i], env, guards, broken)
		}
		for i := range t.Arms {
			w.walk(&t.Arms[i], env, guards, broken)
		}
	}
}

// walkApp collects an application spine. If it bottoms out at `self`, the spine
// is that self-call's arguments; every argument is also walked (an argument can
// itself contain a self-call).
func (w *rankWalker) walkApp(t *Term, env []smtVal, guards []string, broken bool) {
	spine := []*Term{}
	cur := t
	for cur.K == "app" {
		spine = append([]*Term{cur.B}, spine...)
		cur = cur.A
	}
	if cur.K == "self" {
		w.recordSite(cur, spine, env, guards, broken)
	} else {
		w.walk(cur, env, guards, broken)
	}
	for _, a := range spine {
		w.walk(a, env, guards, broken)
	}
}

func (w *rankWalker) walkMatch(t *Term, env []smtVal, guards []string, broken bool) {
	w.walk(t.A, env, guards, broken)
	scrutExpr, scrutSort, err := w.c.tr(t.A, env)
	var dt *dtInfo
	if err == nil {
		dt = w.c.dtBySort[scrutSort]
	}
	for i := range t.Arms {
		env2 := cloneEnv(env)
		armGuards := guards
		armBroken := broken
		if dt != nil && i < len(dt.fields) && i < len(dt.sels) {
			// Bind the arm's field binders to the scrutinee's SELECTORS rather
			// than fresh constants (#57), so a measure over a datatype field
			// stays connected to the counter the body reads (rle-expand's
			// `(Run n v)` binds n to `(cnt r)`). Add the constructor tester as a
			// path guard so the arm is sound — skipped for a single-constructor
			// datatype, where the tester is always true.
			for fi, fs := range dt.fields[i] {
				env2 = append(env2, smtVal{expr: fmt.Sprintf("(%s %s)", dt.sels[i][fi], scrutExpr), sort: fs})
			}
			if len(dt.ctors) > 1 {
				armGuards = appendStr(guards, fmt.Sprintf("((_ is %s) %s)", dt.ctors[i], scrutExpr))
			}
		} else {
			// Unknown field sorts / selectors: cannot bind this arm's variables
			// soundly, so a self-call inside it must not be trusted.
			armBroken = true
		}
		w.walk(&t.Arms[i], env2, armGuards, armBroken)
	}
}

// recordSite translates a self-call's guards and arguments and appends a site.
// A partial application (wrong arity), a poisoned path, or any untranslatable
// argument sets `bad` — the attempt as a whole then fails, conservatively.
func (w *rankWalker) recordSite(self *Term, spine []*Term, env []smtVal, guards []string, broken bool) {
	if broken || len(spine) != w.nparams {
		w.bad = true
		return
	}
	args := make([]string, w.nparams)
	for i, a := range spine {
		e, _, err := w.c.tr(a, env)
		if err != nil {
			w.bad = true
			return
		}
		args[i] = e
	}
	w.sites = append(w.sites, rankSite{guards: append([]string{}, guards...), args: args})
}

func appendStr(xs []string, x string) []string {
	out := make([]string, len(xs)+1)
	copy(out, xs)
	out[len(xs)] = x
	return out
}

func cloneEnv(env []smtVal) []smtVal {
	out := make([]smtVal, len(env))
	copy(out, env)
	return out
}

// measure is a ranking-function candidate: the value of one parameter, the
// difference of two, or an Int-typed FIELD of a datatype parameter reached by a
// selector (#57 — e.g. the count inside rle-expand's `Run`). μ(params) and
// μ(args) are formed by substitution.
type measure struct {
	i, j int    // i alone (j<0), or (param_i - param_j) when sel==""
	sel  string // if non-empty, μ = (sel param_i): a datatype-field measure
}

func (m measure) at(vals []string) string {
	if m.sel != "" {
		return fmt.Sprintf("(%s %s)", m.sel, vals[m.i])
	}
	if m.j < 0 {
		return vals[m.i]
	}
	return fmt.Sprintf("(- %s %s)", vals[m.i], vals[m.j])
}

// ranksTotal reports whether an integer ranking function proves d total, with
// every obligation discharged by Z3. Conservative: false when Z3 is
// unavailable, the body cannot be fully analyzed, or no candidate clears every
// site. h is d's content hash (self-resolution while translating arguments).
func ranksTotal(st *Store, d *Def, h string) bool {
	if d.K != "func" || z3Available() != nil {
		return false
	}
	c := newSmtCtx(st, d, h)

	// Strip the parameters, declaring each as an SMT constant. Int parameters are
	// the measure candidates; others are declared over fresh uninterpreted sorts
	// so translations that mention them are still well-formed.
	body := d.Body
	var env []smtVal
	var intParams []int
	for i := 0; body.K == "lam"; i++ {
		sort := ""
		if body.Ty != nil {
			if s, err := c.sortOf(body.Ty); err == nil {
				sort = s
			}
		}
		if sort == "" {
			sort = fmt.Sprintf("P%d", i)
			c.decls = append(c.decls, fmt.Sprintf("(declare-sort %s 0)", sort))
		}
		if sort == "Int" {
			intParams = append(intParams, i)
		}
		name := fmt.Sprintf("p%d", i)
		c.decls = append(c.decls, fmt.Sprintf("(declare-const %s %s)", name, sort))
		env = append(env, smtVal{expr: name, sort: sort})
		body = body.A
	}
	w := &rankWalker{c: c, nparams: len(env)}
	w.walk(body, env, nil, false)
	if w.bad || len(w.sites) == 0 {
		return false
	}

	// Candidate measures: each Int parameter, each ordered difference of two, and
	// each Int-typed FIELD of a single-constructor datatype parameter reached by
	// its selector (#57 — rle-expand's counter lives inside its `Run` parameter,
	// bound by the body's match to `(cnt r)`).
	var cands []measure
	for _, i := range intParams {
		cands = append(cands, measure{i: i, j: -1})
	}
	for _, i := range intParams {
		for _, j := range intParams {
			if i != j {
				cands = append(cands, measure{i: i, j: j})
			}
		}
	}
	for i := range env {
		if dt := c.dtBySort[env[i].sort]; dt != nil && len(dt.ctors) == 1 && len(dt.sels) == 1 {
			for fi, fs := range dt.fields[0] {
				if fs == "Int" {
					cands = append(cands, measure{i: i, sel: dt.sels[0][fi]})
				}
			}
		}
	}
	if len(cands) == 0 {
		return false // no measure candidate
	}

	paramSyms := make([]string, len(env))
	for i := range env {
		paramSyms[i] = env[i].expr
	}
	// Only the declarations enter the obligation — NOT c.axioms (callee defining
	// equations). A quantified defining axiom would make the query undecidable,
	// so Z3 could answer `unknown` and the verdict would stop being a pure
	// function of the definition (a cross-kernel non-determinism the blind kernel
	// flagged, oathrs DIVERGENCES 65). Omitting the axioms only weakens the
	// premises — it can never yield a false `measure` — and keeps the obligation
	// decidable linear-integer arithmetic over the parameter constants.
	preamble := strings.Join(c.decls, "\n") + "\n"

	for _, m := range cands {
		if w.measureClearsAllSites(m, paramSyms, preamble) {
			return true
		}
	}
	return false
}

// measureClearsAllSites checks that μ strictly decreases and stays ≥ 0 at every
// site. Each obligation is the negation `guards ∧ ¬(μ(args) < μ(params) ∧
// μ(params) ≥ 0)`; UNSAT means the implication is valid.
func (w *rankWalker) measureClearsAllSites(m measure, paramSyms []string, preamble string) bool {
	muParams := m.at(paramSyms)
	for _, site := range w.sites {
		muArgs := m.at(site.args)
		guard := "true"
		if len(site.guards) > 0 {
			guard = "(and " + strings.Join(site.guards, " ") + ")"
		}
		script := preamble +
			fmt.Sprintf("(assert (and %s (not (and (< %s %s) (>= %s 0)))))\n(check-sat)",
				guard, muArgs, muParams, muParams)
		res, capHit := runZ3Budget(script, effectiveRlimit())
		if capHit || !strings.HasPrefix(res, "unsat") {
			return false
		}
	}
	return true
}
