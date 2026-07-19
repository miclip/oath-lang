package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SMT-backed proof with structural induction: the top rung of the guarantee
// ladder. Three layers:
//
//  1. Translation. Monomorphic data instances become SMT algebraic
//     datatypes; pattern matches become tester/selector ite-chains; strings
//     map to SMT strings; non-recursive callees are inlined; RECURSIVE
//     functions are declared uninterpreted with their defining equations
//     asserted as quantified axioms.
//  2. Proof search. Each property is attempted directly (negate, check-sat),
//     then by structural induction on each datatype-typed binder: one
//     subgoal per constructor, with induction hypotheses for recursive
//     fields GENERALIZED over the remaining binders.
//  3. The lemma library. Proven properties of referenced definitions — and
//     earlier proven properties of the same definition — are asserted as
//     axioms. Proof power composes bottom-up through the hash graph exactly
//     like totality and confinement verdicts do.
//
// Honest bail-outs remain: division/modulo (kernel truncates, SMT-LIB is
// Euclidean — translating would prove the wrong theorem), records, partial
// application. And the standing caveat on every proof: Z3 reasons over
// unbounded integers; the evaluator uses int64.

// Deterministic proof budget (#17 adjudication): outcomes must be a pure
// function of (script bytes, solver version, budget). Wall-clock budgets
// are machine-dependent — the sum/CI episode and the q-drop borderline
// flip both trace to them — so the budget is z3's rlimit, a deterministic
// work counter: same script + same rlimit ⇒ same outcome on any machine.
// The wall clock survives only as a non-outcome-determining safety cap; a
// kernel whose wall cap fires before rlimit exhausts is running on
// pathological hardware and MUST report the run as non-conformant.
const proveRlimit = 400_000_000 // 3.5x the heaviest successful proof (calibrated)
// proveDirectRlimit is the reduced budget for the DIRECT attempt on a goal
// that has a datatype-typed binder — i.e. one where structural induction is a
// possible strategy (SPEC §7.2, #50). Such a direct attempt is almost always
// futile (the goal needs induction) yet, at the full budget, burns minutes
// before failing. Every direct proof that SUCCEEDS in the corpus consumes
// under ~3K rlimit (measured); this budget clears that by >1000x while making
// a futile direct attempt fail at 1/100th of the full budget. A goal that
// proves ONLY by full-budget direct is caught by the fallback direct attempt
// at proveRlimit after induction, so the outcome is unchanged — this is a
// pure performance refinement, budget-part-of-identity notwithstanding.
const proveDirectRlimit = 4_000_000
const proveWallCap = 600 * time.Second

var smtNameRe = regexp.MustCompile(`[^A-Za-z0-9]`)

func smtName(s string) string { return smtNameRe.ReplaceAllString(s, "_") }

type dtInfo struct {
	name    string
	ctors   []string
	sels    [][]string
	fields  [][]string        // field sorts
	recSel  map[string]string // records: field name → selector
	recSort map[string]string // records: field name → sort
}

type smtVal struct {
	expr     string
	sort     string
	fn       string
	argSorts []string
	ret      string
}

type lemma struct {
	ownIdx   int // index of the prop this lemma came from in the def under proof; -1 for dependency lemmas
	text     string
	defHash  string          // the definition the lemma belongs to
	mentions map[string]bool // every definition hash the lemma's binders/body reference
}

type smtCtx struct {
	st         *Store
	selfDef    *Def
	selfHash   string
	decls      []string
	axioms     []string           // defining equations of axiomatized recursive functions
	lemmas     []lemma            // proven properties usable as axioms (filtered per goal)
	dts        map[string]*dtInfo // by instance key
	dtBySort   map[string]*dtInfo
	fns        map[string]smtVal    // by instance key
	arrows     map[string][2]string // array sort → (domain, codomain)
	quantified bool
	depth      int
}

// propMentions collects every definition hash a property references
// (its binders and body), plus the hash of the definition it belongs to.
func propMentions(defHash string, p *Prop) map[string]bool {
	out := map[string]bool{defHash: true}
	shell := &Def{K: "func", TyVars: 0, Ty: tBool(), Body: &Term{K: "bool"},
		Props: []Prop{*p}}
	for h := range collectDeps(shell) {
		out[h] = true
	}
	return out
}

// goalFootprint is SPEC §7.2's relevance set: the definition under proof,
// every definition the property's body references, closed transitively
// through function bodies (their defining equations enter the SMT problem,
// so lemmas about them are usable; anything else is noise that slows the
// solver — the lemma library's scaling wall, #25).
func goalFootprint(st *Store, defHash string, d *Def, p *Prop) map[string]bool {
	fp := map[string]bool{}
	var add func(h string, def *Def)
	add = func(h string, def *Def) {
		if fp[h] {
			return
		}
		fp[h] = true
		for dh := range collectDepsBody(def) {
			dd, err := st.GetDef(dh)
			if err != nil {
				continue
			}
			add(dh, dd)
		}
	}
	add(defHash, d)
	for h := range propMentions(defHash, p) {
		if dd, err := st.GetDef(h); err == nil {
			add(h, dd)
		}
	}
	return fp
}

// collectDepsBody is collectDeps restricted to the definition's type, body
// and constructors — excluding its props, which are not part of the SMT
// problem for goals about OTHER definitions.
func collectDepsBody(d *Def) map[string]bool {
	shell := *d
	shell.Props = nil
	return collectDeps(&shell)
}

// lemmaAdmissible: same-definition sibling lemmas are admissible
// unconditionally — they are the §7.2 self-lemma fixpoint's foundation, and
// a sibling's proof chain may legitimately route through symbols the goal
// itself never mentions (sort.idempotent proves via sorted-is-fixpoint,
// which mentions is-sorted; the goal mentions only sort). The footprint
// test applies to DEPENDENCY lemmas, where the library's growth lives.
func lemmaAdmissible(l *lemma, goalDef string, fp map[string]bool) bool {
	if l.defHash == goalDef {
		return true
	}
	if l.mentions == nil {
		return true // pre-filter lemmas (shouldn't occur); fail open
	}
	for h := range l.mentions {
		if !fp[h] {
			return false
		}
	}
	return true
}

func newSmtCtx(st *Store, d *Def, h string) *smtCtx {
	return &smtCtx{st: st, selfDef: d, selfHash: h,
		dts: map[string]*dtInfo{}, dtBySort: map[string]*dtInfo{}, fns: map[string]smtVal{}}
}

func tyKey(h string, args []Ty) string {
	parts := []string{h}
	for i := range args {
		parts = append(parts, debugTy(&args[i]))
	}
	return strings.Join(parts, "|")
}

func (c *smtCtx) sortOf(t *Ty) (string, error) {
	switch t.K {
	case "int":
		return "Int", nil
	case "bool":
		return "Bool", nil
	case "str":
		return "String", nil
	case "fun":
		// Function values are SMT arrays, applied via select. This makes
		// them first-class: quantifiable in induction hypotheses and legal
		// as datatype fields (capability records).
		dom, err := c.sortOf(t.A)
		if err != nil {
			return "", err
		}
		cod, err := c.sortOf(t.B)
		if err != nil {
			return "", err
		}
		s := fmt.Sprintf("(Array %s %s)", dom, cod)
		if c.arrows == nil {
			c.arrows = map[string][2]string{}
		}
		c.arrows[s] = [2]string{dom, cod}
		return s, nil
	case "data":
		dt, err := c.ensureDT(t.Hash, t.Args)
		if err != nil {
			return "", err
		}
		return dt.name, nil
	case "record":
		dt, err := c.ensureRecordDT(t)
		if err != nil {
			return "", err
		}
		return dt.name, nil
	}
	return "", fmt.Errorf("type %s is outside the provable fragment", debugTy(t))
}

// ensureRecordDT declares a structural record as a single-constructor
// datatype. Records with function-typed fields (capabilities) stay outside
// the fragment — SMT datatype fields must be first-order.
func (c *smtCtx) ensureRecordDT(t *Ty) (*dtInfo, error) {
	var sorts []string
	for i := range t.Args {
		s, err := c.sortOf(&t.Args[i])
		if err != nil {
			return nil, err
		}
		sorts = append(sorts, s)
	}
	name := "Rec"
	for i, n := range t.Names {
		name += "_" + smtName(n) + "_" + smtName(sorts[i])
	}
	if dt, ok := c.dtBySort[name]; ok {
		return dt, nil
	}
	mk := "mk_" + name
	dt := &dtInfo{name: name, ctors: []string{mk}, recSel: map[string]string{}, recSort: map[string]string{}}
	var parts, sels []string
	for i, n := range t.Names {
		sel := mk + "_" + smtName(n)
		dt.recSel[n] = sel
		dt.recSort[n] = sorts[i]
		sels = append(sels, sel)
		parts = append(parts, fmt.Sprintf("(%s %s)", sel, sorts[i]))
	}
	dt.sels = [][]string{sels}
	dt.fields = [][]string{sorts}
	c.dts[name] = dt
	c.dtBySort[name] = dt
	c.decls = append(c.decls, fmt.Sprintf("(declare-datatypes ((%s 0)) (((%s %s))))", name, mk, strings.Join(parts, " ")))
	return dt, nil
}

// ensureDT declares the monomorphic datatype instance for (hash, args).
func (c *smtCtx) ensureDT(h string, args []Ty) (*dtInfo, error) {
	key := tyKey(h, args)
	if dt, ok := c.dts[key]; ok {
		return dt, nil
	}
	d, err := c.st.GetDef(h)
	if err != nil {
		return nil, err
	}
	m, err := c.st.GetMeta(h)
	if err != nil {
		return nil, err
	}
	name := smtName(m.Name)
	for i := range args {
		s, err := c.sortOf(&args[i])
		if err != nil {
			return nil, err
		}
		name += "_" + smtName(s)
	}
	dt := &dtInfo{name: name}
	c.dts[key] = dt // pre-register: recursive fields resolve to this name
	c.dtBySort[name] = dt
	var ctorDecls []string
	for ci := range d.Ctors {
		cn := smtName(m.CtorNames[ci]) + "_" + name
		dt.ctors = append(dt.ctors, cn)
		fields := instCtorFields(d, h, args, ci)
		var sels, sorts, parts []string
		for fi, f := range fields {
			s, err := c.sortOf(f)
			if err != nil {
				return nil, err
			}
			sel := fmt.Sprintf("%s_%d", cn, fi)
			sels = append(sels, sel)
			sorts = append(sorts, s)
			parts = append(parts, fmt.Sprintf("(%s %s)", sel, s))
		}
		dt.sels = append(dt.sels, sels)
		dt.fields = append(dt.fields, sorts)
		ctorDecls = append(ctorDecls, fmt.Sprintf("(%s %s)", cn, strings.Join(parts, " ")))
	}
	c.decls = append(c.decls, fmt.Sprintf("(declare-datatypes ((%s 0)) ((%s)))", name, strings.Join(ctorDecls, " ")))
	return dt, nil
}

// ensureFn declares a recursive function instance and asserts its defining
// equation as a quantified axiom.
func (c *smtCtx) ensureFn(h string, d *Def, args []Ty) (smtVal, error) {
	key := tyKey(h, args)
	if v, ok := c.fns[key]; ok {
		return v, nil
	}
	name := "fn_" + smtName(c.st.NameOf(h))
	ty := substTy(d.Ty, args)
	var argSorts []string
	cur := ty
	for cur.K == "fun" {
		s, err := c.sortOf(cur.A)
		if err != nil {
			return smtVal{}, err
		}
		argSorts = append(argSorts, s)
		cur = cur.B
	}
	ret, err := c.sortOf(cur)
	if err != nil {
		return smtVal{}, err
	}
	for i := range args {
		s, _ := c.sortOf(&args[i])
		name += "_" + smtName(s)
	}
	v := smtVal{fn: name, argSorts: argSorts, ret: ret}
	c.fns[key] = v // pre-register: recursive self-calls resolve here
	c.decls = append(c.decls, fmt.Sprintf("(declare-fun %s (%s) %s)", name, strings.Join(argSorts, " "), ret))

	body := termSubstTy(d.Body, args)
	var env []smtVal
	var params []string
	for i := 0; body.K == "lam"; i++ {
		p := fmt.Sprintf("p%d", i)
		s, err := c.sortOf(body.Ty)
		if err != nil {
			return smtVal{}, err
		}
		params = append(params, fmt.Sprintf("(%s %s)", p, s))
		env = append(env, smtVal{expr: p, sort: s})
		body = body.A
	}
	// Translate the body in the callee's own frame: self is (h, d).
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = d, h
	bexpr, _, err := c.tr(body, env)
	c.selfDef, c.selfHash = saveDef, saveHash
	if err != nil {
		return smtVal{}, err
	}
	// Assert the defining equation ONLY for functions proven total. For a
	// function whose termination is unproven, `f(x) = body` can be an
	// inconsistent constraint (e.g. f x = f x + 1 ⟹ ∀x. f(x)=f(x)+1, UNSAT),
	// and an inconsistent axiom lets Z3 discharge ANY goal by ex falso —
	// "proving" false properties, and poisoning every dependent through the
	// lemma library. The soundness of the top rung depends on this gate.
	// A non-total callee is left uninterpreted (declared, no equation): sound,
	// merely weaker (proofs that needed its definition come back `unknown`).
	tm, _ := c.st.GetMeta(h)
	if tm != nil && isTotal(tm.Termination) {
		app := name
		if len(env) > 0 {
			var syms []string
			for _, e := range env {
				syms = append(syms, e.expr)
			}
			app = fmt.Sprintf("(%s %s)", name, strings.Join(syms, " "))
			c.axioms = append(c.axioms, fmt.Sprintf("(assert (forall (%s) (! (= %s %s) :pattern (%s))))",
				strings.Join(params, " "), app, bexpr, app))
			c.quantified = true
		} else {
			c.axioms = append(c.axioms, fmt.Sprintf("(assert (= %s %s))", app, bexpr))
		}
	}
	return v, nil
}

var smtPrimOps = map[string]string{
	"+": "+", "-": "-", "*": "*", "neg": "-",
	"<": "<", "<=": "<=", "and": "and", "or": "or", "not": "not",
	"==": "=", "++": "str.++", "str-len": "str.len",
}

var smtPrimSorts = map[string]string{
	"+": "Int", "-": "Int", "*": "Int", "neg": "Int",
	"<": "Bool", "<=": "Bool", "and": "Bool", "or": "Bool", "not": "Bool",
	"==": "Bool", "++": "String", "str-len": "Int",
}

func (c *smtCtx) tr(t *Term, env []smtVal) (string, string, error) {
	c.depth++
	defer func() { c.depth-- }()
	if c.depth > 512 {
		return "", "", fmt.Errorf("inlining too deep")
	}
	switch t.K {
	case "var":
		v := env[len(env)-1-t.Idx]
		return v.expr, v.sort, nil
	case "int":
		if t.Int < 0 {
			return fmt.Sprintf("(- %d)", -t.Int), "Int", nil
		}
		return fmt.Sprintf("%d", t.Int), "Int", nil
	case "bool":
		return fmt.Sprintf("%v", t.Bool), "Bool", nil
	case "str":
		return `"` + strings.ReplaceAll(t.Str, `"`, `""`) + `"`, "String", nil
	case "if":
		cnd, _, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		th, s1, err := c.tr(t.B, env)
		if err != nil {
			return "", "", err
		}
		el, _, err := c.tr(t.C, env)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("(ite %s %s %s)", cnd, th, el), s1, nil
	case "let":
		bound, s, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		return c.tr(t.B, append(append([]smtVal{}, env...), smtVal{expr: bound, sort: s}))
	case "prim":
		if t.Op == "/" || t.Op == "%" {
			return "", "", fmt.Errorf("%s is untranslatable (kernel truncates, SMT-LIB is Euclidean)", t.Op)
		}
		op, ok := smtPrimOps[t.Op]
		if !ok {
			return "", "", fmt.Errorf("primitive %s is outside the provable fragment", t.Op)
		}
		var parts []string
		for i := range t.Args {
			a, _, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", "", err
			}
			parts = append(parts, a)
		}
		return "(" + op + " " + strings.Join(parts, " ") + ")", smtPrimSorts[t.Op], nil
	case "ctor":
		dt, err := c.ensureDT(t.Hash, t.TyArgs)
		if err != nil {
			return "", "", err
		}
		if len(t.Args) == 0 {
			return dt.ctors[t.Idx], dt.name, nil
		}
		var parts []string
		for i := range t.Args {
			a, _, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", "", err
			}
			parts = append(parts, a)
		}
		return fmt.Sprintf("(%s %s)", dt.ctors[t.Idx], strings.Join(parts, " ")), dt.name, nil
	case "record":
		// Reconstruct the record's type from field sorts to locate its
		// datatype; fields are canonically sorted so names align.
		var sorts []string
		var exprs []string
		for i := range t.Args {
			e, s, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", "", err
			}
			exprs = append(exprs, e)
			sorts = append(sorts, s)
		}
		name := "Rec"
		for i, n := range t.Names {
			name += "_" + smtName(n) + "_" + smtName(sorts[i])
		}
		dt, ok := c.dtBySort[name]
		if !ok {
			// Declare on first sight, mirroring ensureRecordDT.
			mk := "mk_" + name
			dt = &dtInfo{name: name, ctors: []string{mk}, recSel: map[string]string{}, recSort: map[string]string{}}
			var parts []string
			for i, n := range t.Names {
				sel := mk + "_" + smtName(n)
				dt.recSel[n] = sel
				dt.recSort[n] = sorts[i]
				parts = append(parts, fmt.Sprintf("(%s %s)", sel, sorts[i]))
			}
			c.dts[name] = dt
			c.dtBySort[name] = dt
			c.decls = append(c.decls, fmt.Sprintf("(declare-datatypes ((%s 0)) (((%s %s))))", name, mk, strings.Join(parts, " ")))
		}
		return fmt.Sprintf("(%s %s)", dt.ctors[0], strings.Join(exprs, " ")), name, nil
	case "field":
		re, rs, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		dt, ok := c.dtBySort[rs]
		if !ok || dt.recSel == nil {
			return "", "", fmt.Errorf("field access on non-record sort %s", rs)
		}
		sel, ok := dt.recSel[t.Op]
		if !ok {
			return "", "", fmt.Errorf("record sort %s has no field %q", rs, t.Op)
		}
		return fmt.Sprintf("(%s %s)", sel, re), dt.recSort[t.Op], nil
	case "match":
		s, ssort, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		dt, ok := c.dtBySort[ssort]
		if !ok {
			return "", "", fmt.Errorf("match on non-datatype sort %s", ssort)
		}
		var armExprs []string
		var resSort string
		for i := range t.Arms {
			env2 := append([]smtVal{}, env...)
			for fi, sel := range dt.sels[i] {
				env2 = append(env2, smtVal{expr: fmt.Sprintf("(%s %s)", sel, s), sort: dt.fields[i][fi]})
			}
			a, as, err := c.tr(&t.Arms[i], env2)
			if err != nil {
				return "", "", err
			}
			armExprs = append(armExprs, a)
			resSort = as
		}
		out := armExprs[len(armExprs)-1]
		for i := len(armExprs) - 2; i >= 0; i-- {
			out = fmt.Sprintf("(ite ((_ is %s) %s) %s %s)", dt.ctors[i], s, armExprs[i], out)
		}
		return out, resSort, nil
	case "app":
		head, args := unwindApp(t)
		switch head.K {
		case "var":
			// Array-encoded function value: apply via nested select.
			v := env[len(env)-1-head.Idx]
			expr, sort := v.expr, v.sort
			for _, a := range args {
				arrow, ok := c.arrows[sort]
				if !ok {
					return "", "", fmt.Errorf("application of a non-function value (sort %s)", sort)
				}
				s, _, err := c.tr(a, env)
				if err != nil {
					return "", "", err
				}
				expr = fmt.Sprintf("(select %s %s)", expr, s)
				sort = arrow[1]
			}
			return expr, sort, nil
		case "field":
			// ((. cap fetch) x): project then select.
			fe, fs, err := c.tr(head, env)
			if err != nil {
				return "", "", err
			}
			expr, sort := fe, fs
			for _, a := range args {
				arrow, ok := c.arrows[sort]
				if !ok {
					return "", "", fmt.Errorf("application of a non-function field (sort %s)", sort)
				}
				s, _, err := c.tr(a, env)
				if err != nil {
					return "", "", err
				}
				expr = fmt.Sprintf("(select %s %s)", expr, s)
				sort = arrow[1]
			}
			return expr, sort, nil
		case "ref":
			d, err := c.st.GetDef(head.Hash)
			if err != nil {
				return "", "", err
			}
			return c.call(head.Hash, d, head.TyArgs, args, env)
		case "self":
			return c.call(c.selfHash, c.selfDef, head.TyArgs, args, env)
		case "lam":
			cur := head
			env2 := append([]smtVal{}, env...)
			consumed := 0
			for cur.K == "lam" && consumed < len(args) {
				s, srt, err := c.tr(args[consumed], env)
				if err != nil {
					return "", "", err
				}
				env2 = append(env2, smtVal{expr: s, sort: srt})
				cur = cur.A
				consumed++
			}
			if consumed != len(args) || cur.K == "lam" {
				return "", "", fmt.Errorf("partial application is outside the provable fragment")
			}
			return c.tr(cur, env2)
		}
		return "", "", fmt.Errorf("application head %q is outside the provable fragment", head.K)
	case "ref":
		d, err := c.st.GetDef(t.Hash)
		if err != nil {
			return "", "", err
		}
		return c.call(t.Hash, d, t.TyArgs, nil, env)
	case "self":
		return c.call(c.selfHash, c.selfDef, t.TyArgs, nil, env)
	}
	return "", "", fmt.Errorf("%q terms are outside the provable fragment", t.K)
}

// call translates a fully-applied reference: recursive callees become
// axiomatized uninterpreted functions, non-recursive callees are inlined.
func (c *smtCtx) call(h string, d *Def, tyargs []Ty, args []*Term, env []smtVal) (string, string, error) {
	if d.K != "func" {
		return "", "", fmt.Errorf("data reference is outside the provable fragment")
	}
	if hasSelfRef(d.Body) {
		v, err := c.ensureFn(h, d, tyargs)
		if err != nil {
			return "", "", err
		}
		if len(args) != len(v.argSorts) {
			return "", "", fmt.Errorf("%s must be fully applied", c.st.NameOf(h))
		}
		if len(args) == 0 {
			return v.fn, v.ret, nil
		}
		var parts []string
		for _, a := range args {
			s, _, err := c.tr(a, env)
			if err != nil {
				return "", "", err
			}
			parts = append(parts, s)
		}
		return fmt.Sprintf("(%s %s)", v.fn, strings.Join(parts, " ")), v.ret, nil
	}
	// Non-recursive: inline.
	body := termSubstTy(d.Body, tyargs)
	var callee []smtVal
	for i := 0; body.K == "lam"; i++ {
		if i >= len(args) {
			return "", "", fmt.Errorf("%s must be fully applied to inline", c.st.NameOf(h))
		}
		s, srt, err := c.tr(args[i], env)
		if err != nil {
			return "", "", err
		}
		callee = append(callee, smtVal{expr: s, sort: srt})
		body = body.A
	}
	if len(callee) != len(args) {
		return "", "", fmt.Errorf("%s is over-applied; cannot inline", c.st.NameOf(h))
	}
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = d, h
	e, s, err := c.tr(body, callee)
	c.selfDef, c.selfHash = saveDef, saveHash
	return e, s, err
}

// termSubstTy instantiates every embedded type in a term.
func termSubstTy(t *Term, args []Ty) *Term {
	if t == nil || len(args) == 0 {
		return t
	}
	out := *t
	if t.Ty != nil {
		out.Ty = substTy(t.Ty, args)
	}
	if len(t.TyArgs) > 0 {
		tys := make([]Ty, len(t.TyArgs))
		for i := range t.TyArgs {
			tys[i] = *substTy(&t.TyArgs[i], args)
		}
		out.TyArgs = tys
	}
	out.A = termSubstTy(t.A, args)
	out.B = termSubstTy(t.B, args)
	out.C = termSubstTy(t.C, args)
	if len(t.Args) > 0 {
		as := make([]Term, len(t.Args))
		for i := range t.Args {
			as[i] = *termSubstTy(&t.Args[i], args)
		}
		out.Args = as
	}
	if len(t.Arms) > 0 {
		as := make([]Term, len(t.Arms))
		for i := range t.Arms {
			as[i] = *termSubstTy(&t.Arms[i], args)
		}
		out.Arms = as
	}
	return &out
}

func hasSelfRef(t *Term) bool {
	if t == nil {
		return false
	}
	if t.K == "self" {
		return true
	}
	if hasSelfRef(t.A) || hasSelfRef(t.B) || hasSelfRef(t.C) {
		return true
	}
	for i := range t.Args {
		if hasSelfRef(&t.Args[i]) {
			return true
		}
	}
	for i := range t.Arms {
		if hasSelfRef(&t.Arms[i]) {
			return true
		}
	}
	return false
}

// formulaWith renders a property as a formula. assign maps binder index →
// SMT expression; unassigned binders become universally quantified. Function
// binders cannot be quantified in FOL, so they fail unless assigned.
func (c *smtCtx) formulaWith(d *Def, h string, p *Prop, assign map[int]string) (string, error) {
	var env []smtVal
	var foralls []string
	for i := range p.Binders {
		b := &p.Binders[i]
		if e, ok := assign[i]; ok {
			s, err := c.sortOf(b)
			if err != nil {
				return "", err
			}
			env = append(env, smtVal{expr: e, sort: s})
			continue
		}
		s, err := c.sortOf(b)
		if err != nil {
			return "", fmt.Errorf("cannot quantify binder of type %s", debugTy(b))
		}
		q := fmt.Sprintf("q%d", i)
		foralls = append(foralls, fmt.Sprintf("(%s %s)", q, s))
		env = append(env, smtVal{expr: q, sort: s})
	}
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = d, h
	body, _, err := c.tr(&p.Body, env)
	c.selfDef, c.selfHash = saveDef, saveHash
	if err != nil {
		return "", err
	}
	if len(foralls) > 0 {
		c.quantified = true
		return fmt.Sprintf("(forall (%s) %s)", strings.Join(foralls, " "), body), nil
	}
	return body, nil
}

// calibLastConsumed records the rlimit the last runZ3 call spent. It backs the
// OATH_PROVE_SPLIT per-phase diagnostic (a durable debugging hook alongside
// OATH_PROVE_CALIBRATE); nothing in the proof outcome depends on it.
var calibLastConsumed int64

// runZ3 runs a goal at the full deterministic budget (SPEC §7.2). The
// OATH_PROVE_RLIMIT override exists for testing only.
func runZ3(script string) (string, bool) {
	return runZ3Budget(script, effectiveRlimit())
}

// effectiveRlimit is the full per-goal budget: the normative proveRlimit, or the
// OATH_PROVE_RLIMIT testing override when set.
func effectiveRlimit() int64 {
	rl := int64(proveRlimit)
	if v := os.Getenv("OATH_PROVE_RLIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			rl = n
		}
	}
	return rl
}

// directRlimit is the budget for the direct attempt on an inductive-eligible
// goal. It is clamped to the full budget so the reduced attempt can never be
// STRONGER than the full-budget fallback — otherwise lowering the full budget
// (the testing override) would leave the direct attempt running longer than the
// fallback that is supposed to subsume it, and the two kernels would diverge.
func directRlimit() int64 {
	full := effectiveRlimit()
	if proveDirectRlimit < full {
		return proveDirectRlimit
	}
	return full
}

// runZ3Budget runs a goal at an explicit rlimit. The budget is a runner option
// prepended OUTSIDE the hashed core script (SPEC §7.2), so the same goal at any
// budget emits byte-identical script bytes — only the outcome may differ. The
// direct attempt on a datatype-binder goal uses the reduced proveDirectRlimit
// here (see proveOne); everything else uses proveRlimit.
func runZ3Budget(script string, rl int64) (string, bool) {
	// Runner options are prepended outside the hashed core script (SPEC
	// §7.2) and the trailing get-info lines are the attempt-validity
	// telemetry, likewise outside the hashed bytes. An OPT-IN memory bound
	// (OATH_PROVE_MEMORY_MB) converts an OS-level death into a clean
	// memout abort — but it cannot be a default: z3 counts its upfront
	// arena RESERVATIONS (~21GB virtual on quantified goals) against
	// memory_max_size, so any bound below the reservation instantly kills
	// attempts that would have run fine, and any bound above it bounds
	// nothing. Environments that prefer memout-invalidation over OS death
	// set it explicitly; the validity rule below catches both.
	header := fmt.Sprintf("(set-option :rlimit %d)\n", rl)
	if v := os.Getenv("OATH_PROVE_MEMORY_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			header = fmt.Sprintf("(set-option :memory_max_size %d)\n", n) + header
		}
	}
	full := header + script + "\n(get-info :rlimit)\n(get-info :reason-unknown)\n"
	ctx, cancel := context.WithTimeout(context.Background(), proveWallCap)
	defer cancel()
	cmd := exec.CommandContext(ctx, "z3", "-in")
	cmd.Stdin = strings.NewReader(full)
	out, _ := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		// SPEC §7.2: the wall cap is SAFETY-ONLY. Hitting it means this
		// environment could not exhaust the rlimit budget — the attempt is
		// INVALID, never an outcome. (The blind Rust kernel implemented
		// invalidation correctly from the spec text; the reference
		// originally recorded cap hits as unknown — caught by cross-kernel
		// conformance, #17 epilogue.)
		return "", true
	}
	res := string(out)
	consumed, haveConsumed := int64(-1), false
	if i := strings.Index(res, "(:rlimit "); i >= 0 {
		s := strings.TrimRight(strings.TrimSpace(res[i+9:]), ")\n `")
		if j := strings.IndexAny(s, ")\n"); j >= 0 {
			s = s[:j]
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			consumed, haveConsumed = n, true
		}
	}
	reason, haveReason := "", false
	if i := strings.Index(res, `(:reason-unknown "`); i >= 0 {
		rest := res[i+len(`(:reason-unknown "`):]
		if j := strings.LastIndex(rest, `"`); j >= 0 {
			reason, haveReason = rest[:j], true
		}
	}
	if os.Getenv("OATH_PROVE_CALIBRATE") != "" {
		verdict := "unknown"
		if strings.HasPrefix(res, "unsat") {
			verdict = "unsat"
		} else if strings.HasPrefix(res, "sat") {
			verdict = "sat"
		}
		fmt.Fprintf(os.Stderr, "CALIB %s rlimit=%d reason=%q\n", verdict, consumed, reason)
		if !haveConsumed {
			raw := res
			if len(raw) > 400 {
				raw = raw[:400]
			}
			fmt.Fprintf(os.Stderr, "CALIB-RAW %q\n", raw)
		}
	}
	calibLastConsumed = consumed
	if strings.HasPrefix(res, "unsat") || strings.HasPrefix(res, "sat") {
		return res, false
	}
	// ATTEMPT VALIDITY (SPEC §7.2, #29): a non-verdict is an OUTCOME only
	// when z3's own telemetry proves the attempt was deterministic —
	// either the budget was genuinely spent (reason "canceled" with
	// consumed >= rlimit; z3 overshoots by a few units), or the solver
	// gave up for a reason that is a pure function of the script
	// (incompleteness). Everything else is the ENVIRONMENT talking:
	// memout (the memory bound fired — bound size is machine policy),
	// "canceled" below budget (an external cancel), or missing telemetry
	// (the process died mid-attempt). Recording any of those as unproven
	// would make verdicts depend on RAM and signals, which is the
	// machine-dependence this design exists to abolish.
	switch {
	case !haveConsumed || !haveReason:
		return "", true
	case reason == "":
		// A blank reason on a non-verdict is absence of evidence, not
		// evidence of determinism — the rule demands POSITIVE telemetry
		// (found by the blind kernel, DIVERGENCES 71 ambiguity 3).
		return "", true
	case strings.Contains(reason, "memout") || strings.Contains(reason, "memory"):
		return "", true
	case reason == "canceled" && consumed < rl:
		return "", true
	}
	return res, false
}

func sortedDepHashes(d *Def) []string {
	deps := collectDeps(d)
	out := make([]string, 0, len(deps))
	for h := range deps {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

type propOutcome struct {
	status string // proven | refuted | unknown
	method string // direct | induction on <binder>
	detail string // bail reason or countermodel
}

// proveOne attempts a single property: direct, then structural induction on
// each datatype-typed binder with IHs generalized over the other binders.
// pi is the property's own index — its own lemma (from a prior run) is
// excluded so a property can never prove itself.
func (c *smtCtx) proveOne(d *Def, h string, m *Meta, p *Prop, pi int) propOutcome {
	// Script stability: all emitted symbols are STRUCTURALLY named (binder
	// index, constructor-field index, function-parameter index) — no
	// counters exist, so a goal's script is canonical by construction:
	// byte-identical across attempt histories, warm/cold runs, and
	// independent kernel implementations. Names + assertion order steer
	// solver search; canonicality makes outcomes a pure function of
	// (goal, lemma set, solver version, rlimit).
	// SPEC §7.2 relevance (#25): only lemmas whose every mention lies inside
	// this goal's footprint enter the problem.
	footprint := goalFootprint(c.st, h, d, p)
	// Declare the goal's binders as fresh constants. Function-typed binders
	// are array-sorted constants like any other — uniformly quantifiable.
	var binderDecls []string
	consts := map[int]string{}
	binderSorts := make([]string, len(p.Binders))
	for i := range p.Binders {
		s, err := c.sortOf(&p.Binders[i])
		if err != nil {
			return propOutcome{status: "unknown", detail: err.Error()}
		}
		name := fmt.Sprintf("b%d", i)
		binderDecls = append(binderDecls, fmt.Sprintf("(declare-const %s %s)", name, s))
		consts[i] = name
		binderSorts[i] = s
	}

	goal, err := c.formulaWith(d, h, p, consts)
	if err != nil {
		return propOutcome{status: "unknown", detail: err.Error()}
	}

	script := func(extraDecls, extraAsserts []string, negated string, model bool) string {
		var b strings.Builder
		for _, x := range c.decls {
			b.WriteString(x + "\n")
		}
		for _, x := range c.axioms {
			b.WriteString(x + "\n")
		}
		// Canonical lemma order (script stability, part 2): emission sorted
		// by (definition hash, property index), so the script depends only
		// on the admissible lemma SET — never on whether a lemma arrived
		// from prior-run metadata or was proven moments ago. Assert order
		// steers solver search; acquisition history must not.
		var adm []*lemma
		for i := range c.lemmas {
			if c.lemmas[i].ownIdx != pi && lemmaAdmissible(&c.lemmas[i], h, footprint) {
				adm = append(adm, &c.lemmas[i])
			}
		}
		sort.Slice(adm, func(a, b2 int) bool {
			if adm[a].defHash != adm[b2].defHash {
				return adm[a].defHash < adm[b2].defHash
			}
			return adm[a].ownIdx < adm[b2].ownIdx
		})
		for _, l := range adm {
			b.WriteString(l.text + "\n")
		}
		for _, x := range binderDecls {
			b.WriteString(x + "\n")
		}
		for _, x := range extraDecls {
			b.WriteString(x + "\n")
		}
		for _, x := range extraAsserts {
			b.WriteString(x + "\n")
		}
		fmt.Fprintf(&b, "(assert (not %s))\n(check-sat)\n", negated)
		if model {
			b.WriteString("(get-model)\n")
		}
		return b.String()
	}

	// Direct attempt. An INVALID attempt (environmental abort) yields NO
	// EVIDENCE (SPEC §7.2): it must not end the run — a later strategy can
	// still prove the goal, and unsat is positive evidence no environment
	// can fake. Invalidity taints only the NEGATIVE case: recording
	// "unproven" with an invalid attempt among the strategies would
	// launder an environmental abort into a verdict, so that combination
	// invalidates at the end of proveOne instead. (Found in the wild:
	// z3 crashes with empty output on t-insert.insert-length's direct
	// script — deterministically — while induction proves the goal fine.)
	// A goal with a datatype-typed binder can be discharged by induction, so
	// its direct attempt is almost always futile — yet at the full budget it
	// burns minutes before failing (#50). Run it at the reduced
	// proveDirectRlimit; if that under-runs, induction is tried next, and a
	// full-budget direct FALLBACK below catches the rare goal that proves only
	// by heavy direct. The emitted script is byte-identical either way (the
	// budget is a runner option outside the hashed bytes, SPEC §7.2), so this
	// changes speed, not outcomes or script hashes.
	hasDTBinder := false
	for i := range binderSorts {
		if _, ok := c.dtBySort[binderSorts[i]]; ok {
			hasDTBinder = true
			break
		}
	}
	directScript := script(nil, nil, goal, !c.quantified)
	sawInvalid := false
	directStart := time.Now()
	var out string
	var capHit bool
	if hasDTBinder {
		out, capHit = runZ3Budget(directScript, directRlimit())
	} else {
		out, capHit = runZ3(directScript)
	}
	if os.Getenv("OATH_PROVE_SPLIT") != "" {
		pname := fmt.Sprintf("prop%d", pi)
		if pi < len(m.PropNames) {
			pname = m.PropNames[pi]
		}
		v := "unknown"
		switch {
		case capHit:
			v = "capHit"
		case strings.HasPrefix(out, "unsat"):
			v = "unsat"
		case strings.HasPrefix(out, "sat"):
			v = "sat"
		}
		fmt.Fprintf(os.Stderr, "SPLIT\t%s.%s\tphase=direct\thasDT=%v\twall=%s\tconsumed=%d\tverdict=%s\n",
			m.Name, pname, hasDTBinder, time.Since(directStart), calibLastConsumed, v)
	}
	if capHit {
		sawInvalid = true
		out = ""
	}
	switch {
	case strings.HasPrefix(out, "unsat"):
		return propOutcome{status: "proven", method: "direct"}
	case strings.HasPrefix(out, "sat") && !c.quantified:
		return propOutcome{status: "refuted", detail: strings.TrimSpace(strings.TrimPrefix(out, "sat"))}
	}

	// Induction on each datatype binder.
	for i := range p.Binders {
		dt, ok := c.dtBySort[binderSorts[i]]
		if !ok {
			continue
		}
		allUnsat := true
		for ci := range dt.ctors {
			var extraDecls, extraAsserts []string
			var fieldConsts []string
			for fi, fs := range dt.fields[ci] {
				fc := fmt.Sprintf("f%d", fi)
				extraDecls = append(extraDecls, fmt.Sprintf("(declare-const %s %s)", fc, fs))
				fieldConsts = append(fieldConsts, fc)
				if fs == dt.name {
					// Induction hypothesis: the induction binder becomes the
					// recursive field; every other binder is generalized
					// (∀-quantified — array-encoded functions included).
					ih, err := c.formulaWith(d, h, p, map[int]string{i: fc})
					if err != nil {
						allUnsat = false
						break
					}
					extraAsserts = append(extraAsserts, "(assert "+ih+")")
				}
			}
			if !allUnsat {
				break
			}
			ctorExpr := dt.ctors[ci]
			if len(fieldConsts) > 0 {
				ctorExpr = fmt.Sprintf("(%s %s)", dt.ctors[ci], strings.Join(fieldConsts, " "))
			}
			assign := map[int]string{}
			for k, v := range consts {
				assign[k] = v
			}
			assign[i] = ctorExpr
			subgoal, err := c.formulaWith(d, h, p, assign)
			if err != nil {
				allUnsat = false
				break
			}
			out, capHit := runZ3(script(extraDecls, extraAsserts, subgoal, false))
			if capHit {
				sawInvalid = true
				allUnsat = false
				break
			}
			if !strings.HasPrefix(out, "unsat") {
				allUnsat = false
				break
			}
		}
		if allUnsat {
			bname := fmt.Sprintf("binder %d", i)
			if i < len(m.ParamNames) { // best effort; prop binders are unnamed
				bname = fmt.Sprintf("binder %d", i)
			}
			return propOutcome{status: "proven", method: "induction on " + bname}
		}
	}
	// Lexicographic induction on ordered pairs of datatype binders (#17,
	// SPEC §7.2): for functions like merge that shrink EITHER argument.
	// Sound by the lexicographic subterm order: IH1 shrinks binder i with
	// every other binder generalized; IH2 pins binder i to the SAME
	// constructed value and shrinks binder j.
	for i := range p.Binders {
		dti, ok := c.dtBySort[binderSorts[i]]
		if !ok {
			continue
		}
		for j := range p.Binders {
			if j == i {
				continue
			}
			dtj, ok := c.dtBySort[binderSorts[j]]
			if !ok {
				continue
			}
			allUnsat := true
		lexCtorI:
			for ci := range dti.ctors {
				var declsI []string
				var fieldsI []string
				recI := []string{}
				for fi, fs := range dti.fields[ci] {
					fc := fmt.Sprintf("g%d", fi)
					declsI = append(declsI, fmt.Sprintf("(declare-const %s %s)", fc, fs))
					fieldsI = append(fieldsI, fc)
					if fs == dti.name {
						recI = append(recI, fc)
					}
				}
				ctorI := dti.ctors[ci]
				if len(fieldsI) > 0 {
					ctorI = fmt.Sprintf("(%s %s)", dti.ctors[ci], strings.Join(fieldsI, " "))
				}
				if len(recI) == 0 {
					// Base case in the first component: j and the rest stay
					// as their declared constants; no hypotheses.
					assign := map[int]string{}
					for k, v := range consts {
						assign[k] = v
					}
					assign[i] = ctorI
					subgoal, err := c.formulaWith(d, h, p, assign)
					if err != nil {
						allUnsat = false
						break
					}
					out, capHit := runZ3(script(declsI, nil, subgoal, false))
					if capHit {
						sawInvalid = true
						allUnsat = false
						break
					}
					if !strings.HasPrefix(out, "unsat") {
						allUnsat = false
						break
					}
					continue
				}
				// Recursive first component: case-split the second.
				for cj := range dtj.ctors {
					extraDecls := append([]string{}, declsI...)
					var extraAsserts []string
					var fieldsJ []string
					recJ := []string{}
					for fj, fs := range dtj.fields[cj] {
						fc := fmt.Sprintf("h%d", fj)
						extraDecls = append(extraDecls, fmt.Sprintf("(declare-const %s %s)", fc, fs))
						fieldsJ = append(fieldsJ, fc)
						if fs == dtj.name {
							recJ = append(recJ, fc)
						}
					}
					ctorJ := dtj.ctors[cj]
					if len(fieldsJ) > 0 {
						ctorJ = fmt.Sprintf("(%s %s)", dtj.ctors[cj], strings.Join(fieldsJ, " "))
					}
					// IH1: first component shrank; everything else (j
					// included) universally generalized.
					for _, fc := range recI {
						ih, err := c.formulaWith(d, h, p, map[int]string{i: fc})
						if err != nil {
							allUnsat = false
							break lexCtorI
						}
						extraAsserts = append(extraAsserts, "(assert "+ih+")")
					}
					// IH2: first component pinned to the SAME constructed
					// value; second shrank; the rest generalized.
					for _, fc := range recJ {
						ih, err := c.formulaWith(d, h, p, map[int]string{i: ctorI, j: fc})
						if err != nil {
							allUnsat = false
							break lexCtorI
						}
						extraAsserts = append(extraAsserts, "(assert "+ih+")")
					}
					assign := map[int]string{}
					for k, v := range consts {
						assign[k] = v
					}
					assign[i] = ctorI
					assign[j] = ctorJ
					subgoal, err := c.formulaWith(d, h, p, assign)
					if err != nil {
						allUnsat = false
						break lexCtorI
					}
					out, capHit := runZ3(script(extraDecls, extraAsserts, subgoal, false))
					if capHit {
						sawInvalid = true
						allUnsat = false
						break lexCtorI
					}
					if !strings.HasPrefix(out, "unsat") {
						allUnsat = false
						break lexCtorI
					}
				}
			}
			if allUnsat {
				return propOutcome{status: "proven", method: fmt.Sprintf("lexicographic induction on binders %d,%d", i, j)}
			}
		}
	}
	// Full-budget direct FALLBACK (#50). The datatype-binder direct attempt
	// above ran at the reduced budget; induction and lexicographic induction
	// have now failed. Retry the SAME direct script at the full budget so a
	// goal provable only by heavy direct search is not lost — this keeps the
	// outcome a pure function of (script, solver, full budget), identical to
	// the pre-#50 kernel, while the common inductive case already returned
	// fast above. The script is byte-identical to the reduced attempt, so no
	// new direct-attempt script hash is introduced (SPEC §7.2).
	if hasDTBinder {
		fb, fbCap := runZ3(directScript)
		if os.Getenv("OATH_PROVE_SPLIT") != "" {
			fmt.Fprintf(os.Stderr, "SPLIT\t%s\tphase=direct-fallback\tconsumed=%d\n", m.Name, calibLastConsumed)
		}
		if fbCap {
			sawInvalid = true
		} else {
			switch {
			case strings.HasPrefix(fb, "unsat"):
				return propOutcome{status: "proven", method: "direct"}
			case strings.HasPrefix(fb, "sat") && !c.quantified:
				return propOutcome{status: "refuted", detail: strings.TrimSpace(strings.TrimPrefix(fb, "sat"))}
			}
		}
	}
	if sawInvalid {
		return propOutcome{status: "invalidated", detail: "a strategy attempt was environmentally aborted and no remaining strategy proved the goal — no valid negative verdict exists (SPEC §7.2)"}
	}
	return propOutcome{status: "unknown", detail: "no direct proof; induction did not discharge"}
}

func apiProve(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	if d.K != "func" {
		return "", fmt.Errorf("only function definitions have properties to prove")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if len(d.Props) == 0 {
		return fmt.Sprintf("%s swears no properties — nothing to prove.\n", name), nil
	}
	if _, err := exec.LookPath("z3"); err != nil {
		return "", fmt.Errorf("z3 not found on PATH (brew install z3)")
	}

	var b strings.Builder
	// A property the deterministic tester already refuted has a concrete
	// counterexample; an SMT "proof" of it would be a contradiction we must
	// not record (defense in depth behind the totality gate above).
	testFalsified := map[string]bool{}
	for _, fn := range m.Guarantee.Falsified {
		testFalsified[fn] = true
	}
	// SPEC §7.2: a TWO-LEVEL fixpoint.
	//
	// Inner (self-lemma availability): iterate until no new property proves.
	// The lemma-growth gate (#24) makes it cheap: a goal is only re-attempted
	// when the same-def lemma set has actually grown since its last attempt —
	// outcome-identical to naive iteration, but full-budget burns on genuinely
	// unprovable goals happen once instead of once per pass.
	//
	// Outer (run stability): the recorded state must be a fixpoint of the
	// whole-RUN map F(S) = proven set produced by proving with S recorded.
	// A property proven EARLY in a cold run (small lemma set) is not
	// necessarily re-provable from the FINAL state: a budget-limited search
	// is non-monotone in its axiom set — extra lemmas can divert it. The
	// corpus witness is q-drop cold: drop-back-only proves from
	// {drop-front-nonempty} alone but NOT once drop-empty is also asserted,
	// so a single cold pass records a proof the store's own state cannot
	// reproduce, and the next warm run silently drops it. Iterating F until
	// S == F(S) makes every recorded proof re-derivable from exactly the
	// state the store records — warm and cold runs converge to the same
	// self-consistent verdicts.
	var outcomes []propOutcome
	var provenSet, withheld []bool
	var lemmaCount, ownProven int
	for round := 0; ; round++ {
		c := newSmtCtx(st, d, h)
		lemmaCount, ownProven = loadLemmaLibrary(c, st, d, h, m)
		outcomes = make([]propOutcome, len(d.Props))
		attempted := make([]bool, len(d.Props))
		provenSet = make([]bool, len(d.Props))
		lastEpoch := make([]int, len(d.Props))
		withheld = make([]bool, len(d.Props))
		epoch := 1 // bumps whenever a new own-lemma lands
		for {
			progress := false
			for pi := range d.Props {
				if provenSet[pi] || withheld[pi] {
					continue
				}
				if attempted[pi] && lastEpoch[pi] == epoch {
					continue // no relevant lemma has appeared since the last attempt
				}
				pn := metaPropName(m, pi)
				// Fresh translation context per attempt: the script must be a
				// function of (goal, lemma set) alone — a shared context would
				// leak axioms accrued by earlier attempts into later scripts,
				// the acquisition-history dependence this design eliminates.
				ac := newSmtCtx(st, d, h)
				loadLemmaLibrary(ac, st, d, h, m)
				already := map[int]bool{}
				for _, mp := range m.ProvenProps {
					already[mp] = true
				}
				for pj := range d.Props {
					if provenSet[pj] && !already[pj] {
						if f, err := ac.formulaWith(d, h, &d.Props[pj], nil); err == nil {
							ac.lemmas = append(ac.lemmas, lemma{ownIdx: pj, defHash: h, mentions: propMentions(h, &d.Props[pj]),
								text: "(assert " + f + ")"})
						}
					}
				}
				o := ac.proveOne(d, h, m, &d.Props[pi], pi)
				if o.status == "invalidated" {
					// SPEC §7.2: never record from a run whose wall cap fired
					// before the rlimit budget exhausted.
					return "", fmt.Errorf("prove run INVALIDATED at %s: %s", pn, o.detail)
				}
				attempted[pi] = true
				lastEpoch[pi] = epoch
				outcomes[pi] = o
				if o.status == "proven" {
					if testFalsified[pn] {
						withheld[pi] = true
						continue
					}
					provenSet[pi] = true
					progress = true
					epoch++
				}
			}
			if !progress {
				break
			}
		}
		var newIdx []int
		for pi := range d.Props {
			if provenSet[pi] {
				newIdx = append(newIdx, pi)
			}
		}
		prev := append([]int{}, m.ProvenProps...)
		sort.Ints(prev)
		if len(prev) == len(newIdx) {
			stable := true
			for i := range prev {
				if prev[i] != newIdx[i] {
					stable = false
					break
				}
			}
			if stable {
				break
			}
		}
		if round >= 7 {
			// Never observed; a cycle here would itself be deterministic,
			// but be honest about recording a state we couldn't stabilize.
			fmt.Fprintf(&b, "⚠ run-level fixpoint did not stabilize in %d rounds; recording the last result\n", round+1)
			break
		}
		m.ProvenProps = newIdx
	}
	if lemmaCount+ownProven > 0 {
		fmt.Fprintf(&b, "lemma library: %d from dependencies, %d from prior runs\n", lemmaCount, ownProven)
	}

	proven := 0
	var provenIdx []int
	anyRefuted := false
	for pi := range d.Props {
		pn := metaPropName(m, pi)
		o := outcomes[pi]
		switch {
		case withheld[pi]:
			fmt.Fprintf(&b, "· unproven  %-28s SMT claim contradicts a test counterexample; withheld\n", pn)
		case provenSet[pi]:
			proven++
			provenIdx = append(provenIdx, pi)
			fmt.Fprintf(&b, "∎ PROVEN    %-28s %s (Z3, unbounded ints)\n", pn, o.method)
		case o.status == "refuted":
			anyRefuted = true
			fmt.Fprintf(&b, "✗ REFUTED   %-28s counterexample over unbounded ints:\n", pn)
			for _, line := range strings.Split(o.detail, "\n") {
				fmt.Fprintf(&b, "      %s\n", line)
			}
		default:
			fmt.Fprintf(&b, "· unproven  %-28s %s\n", pn, o.detail)
		}
	}
	fmt.Fprintf(&b, "proven: %d/%d properties\n", proven, len(d.Props))

	m.Guarantee.Proven = proven
	m.ProvenProps = provenIdx
	// Set the level consistently from the CURRENT result — promote and demote.
	// A `proven` level must never outlive the proofs that justified it: an
	// existing `proven` from a prior kernel (e.g. before the non-total axiom
	// gate) that now proves fewer than all properties must fall back to its
	// underlying tested level, or the store would advertise `proven` with an
	// incomplete ProvenProps set.
	allProven := len(d.Props) > 0 && proven == len(d.Props) && !anyRefuted
	switch {
	case allProven && (m.Guarantee.Level == "tested" || m.Guarantee.Level == "proven"):
		m.Guarantee.Level = "proven"
	case !allProven && m.Guarantee.Level == "proven":
		// `proven` is only ever reached from `tested`; demote back to it.
		m.Guarantee.Level = "tested"
		if m.Guarantee.Cases == 0 {
			m.Guarantee.Cases = propCases
		}
	}
	if err := st.SetMeta(h, m); err != nil {
		return "", err
	}
	return b.String(), nil
}

func cmdProve(st *Store, name string) {
	out, err := apiProve(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

// loadLemmaLibrary populates c.lemmas exactly as apiProve does: proven
// properties of transitive dependencies, then the definition's own
// previously-proven properties (tagged by index). Shared by apiProve and
// the script-hash fixture generator so both see identical libraries.
func loadLemmaLibrary(c *smtCtx, st *Store, d *Def, h string, m *Meta) (int, int) {
	// Collect every candidate lemma (transitive-dependency proven props plus
	// the definition's own recorded proven props), then TRANSLATE in
	// canonical ascending (definition-hash, property-index) order — the same
	// order they are emitted in. Translation order determines first-touch
	// declaration/axiom accumulation, so it is part of script identity and
	// must be canonical, not traversal-dependent.
	type cand struct {
		dh string
		pi int
	}
	var cands []cand
	seen := map[string]bool{h: true}
	queue := []string{}
	for _, dep := range sortedDepHashes(d) {
		if !seen[dep] {
			seen[dep] = true
			queue = append(queue, dep)
		}
	}
	for qi := 0; qi < len(queue); qi++ {
		dh := queue[qi]
		dd, err := st.GetDef(dh)
		if err != nil {
			continue
		}
		for _, dep := range sortedDepHashes(dd) {
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
		if dd.K != "func" {
			continue
		}
		dm, err := st.GetMeta(dh)
		if err != nil {
			continue
		}
		for _, pi := range dm.ProvenProps {
			if pi >= 0 && pi < len(dd.Props) {
				cands = append(cands, cand{dh, pi})
			}
		}
	}
	ownStart := len(cands)
	for _, pi := range m.ProvenProps {
		if pi >= 0 && pi < len(d.Props) {
			cands = append(cands, cand{h, pi})
		}
	}
	ownCount := len(cands) - ownStart
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].dh != cands[b].dh {
			return cands[a].dh < cands[b].dh
		}
		return cands[a].pi < cands[b].pi
	})
	added := 0
	for _, cd := range cands {
		dd, err := st.GetDef(cd.dh)
		if err != nil {
			continue
		}
		own := -1
		if cd.dh == h {
			own = cd.pi
		}
		f, err := c.formulaWith(dd, cd.dh, &dd.Props[cd.pi], nil)
		if err != nil {
			continue
		}
		c.lemmas = append(c.lemmas, lemma{ownIdx: own, defHash: cd.dh, mentions: propMentions(cd.dh, &dd.Props[cd.pi]),
			text: "(assert " + f + ")"})
		added++
	}
	return added - ownCount, ownCount
}

// directAttemptScript reproduces, byte-for-byte, the direct-attempt script
// proveOne emits for property pi given the store's recorded lemma state.
// Fixtured as sha256 per property (SPEC §7.2 script stability): a conforming
// kernel must emit these exact bytes, which pins the naming scheme, lemma
// order, and translation without prose ambiguity.
func directAttemptScript(st *Store, h string, pi int) (string, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if pi < 0 || pi >= len(d.Props) {
		return "", fmt.Errorf("no property %d", pi)
	}
	c := newSmtCtx(st, d, h)
	loadLemmaLibrary(c, st, d, h, m)
	p := &d.Props[pi]
	footprint := goalFootprint(c.st, h, d, p)
	var binderDecls []string
	consts := map[int]string{}
	for i := range p.Binders {
		srt, err := c.sortOf(&p.Binders[i])
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("b%d", i)
		binderDecls = append(binderDecls, fmt.Sprintf("(declare-const %s %s)", name, srt))
		consts[i] = name
	}
	goal, err := c.formulaWith(d, h, p, consts)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, x := range c.decls {
		b.WriteString(x + "\n")
	}
	for _, x := range c.axioms {
		b.WriteString(x + "\n")
	}
	var adm []*lemma
	for i := range c.lemmas {
		if c.lemmas[i].ownIdx != pi && lemmaAdmissible(&c.lemmas[i], h, footprint) {
			adm = append(adm, &c.lemmas[i])
		}
	}
	sort.Slice(adm, func(a, b2 int) bool {
		if adm[a].defHash != adm[b2].defHash {
			return adm[a].defHash < adm[b2].defHash
		}
		return adm[a].ownIdx < adm[b2].ownIdx
	})
	for _, l := range adm {
		b.WriteString(l.text + "\n")
	}
	for _, x := range binderDecls {
		b.WriteString(x + "\n")
	}
	fmt.Fprintf(&b, "(assert (not %s))\n(check-sat)\n", goal)
	if !c.quantified {
		b.WriteString("(get-model)\n")
	}
	return b.String(), nil
}
