package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SMT-backed proof: the top rung of the guarantee ladder, made real for an
// honest fragment. A property whose meaning (after transitively inlining
// non-recursive callees) lies in quantifier-free Int/Bool arithmetic — with
// higher-order binders as uninterpreted functions — is translated to SMT-LIB
// and its negation handed to Z3. `unsat` means the property holds for ALL
// inputs, not 200 samples: the definition's guarantee can say `proven`.
//
// Everything outside the fragment bails with a reason and stays `tested`:
// recursion and pattern matching (induction is future work), strings,
// records, division/modulo (SMT-LIB's Euclidean semantics differ from the
// kernel's truncated semantics — translating them would prove the wrong
// theorem). One honest caveat is inherent and documented: Z3 proves over
// unbounded integers while the evaluator uses int64, so `proven` here means
// "true in ideal integer arithmetic".

const proveTimeout = 10 * time.Second

// smtEnv maps de Bruijn binders to either a translated expression or an
// uninterpreted function symbol with its arity.
type smtVal struct {
	expr  string
	fn    string
	arity int
}

type smtCtx struct {
	st      *Store
	selfDef *Def // the definition whose props are being proven: "self" inlines to this
	decls   []string
	depth   int
}

func smtSortOf(t *Ty) (string, bool) {
	switch t.K {
	case "int":
		return "Int", true
	case "bool":
		return "Bool", true
	}
	return "", false
}

// declareBinder turns a prop binder into SMT declarations, or fails if the
// binder's type is outside the fragment.
func (c *smtCtx) declareBinder(i int, t *Ty) (smtVal, error) {
	name := fmt.Sprintf("b%d", i)
	if s, ok := smtSortOf(t); ok {
		c.decls = append(c.decls, fmt.Sprintf("(declare-const %s %s)", name, s))
		return smtVal{expr: name}, nil
	}
	if t.K == "fun" {
		var argSorts []string
		cur := t
		for cur.K == "fun" {
			s, ok := smtSortOf(cur.A)
			if !ok {
				return smtVal{}, fmt.Errorf("function binder with non-Int/Bool argument")
			}
			argSorts = append(argSorts, s)
			cur = cur.B
		}
		ret, ok := smtSortOf(cur)
		if !ok {
			return smtVal{}, fmt.Errorf("function binder with non-Int/Bool result")
		}
		c.decls = append(c.decls, fmt.Sprintf("(declare-fun %s (%s) %s)", name, strings.Join(argSorts, " "), ret))
		return smtVal{fn: name, arity: len(argSorts)}, nil
	}
	return smtVal{}, fmt.Errorf("binder type %s is outside the provable fragment", debugTy(t))
}

var smtPrimOps = map[string]string{
	"+": "+", "-": "-", "*": "*", "neg": "-",
	"<": "<", "<=": "<=", "and": "and", "or": "or", "not": "not", "==": "=",
}

func (c *smtCtx) tr(t *Term, env []smtVal) (string, error) {
	c.depth++
	defer func() { c.depth-- }()
	if c.depth > 256 {
		return "", fmt.Errorf("inlining too deep")
	}
	switch t.K {
	case "var":
		v := env[len(env)-1-t.Idx]
		if v.fn != "" {
			return "", fmt.Errorf("function binder used as a value (only full application is translatable)")
		}
		return v.expr, nil
	case "int":
		if t.Int < 0 {
			return fmt.Sprintf("(- %d)", -t.Int), nil
		}
		return fmt.Sprintf("%d", t.Int), nil
	case "bool":
		return fmt.Sprintf("%v", t.Bool), nil
	case "if":
		cnd, err := c.tr(t.A, env)
		if err != nil {
			return "", err
		}
		th, err := c.tr(t.B, env)
		if err != nil {
			return "", err
		}
		el, err := c.tr(t.C, env)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(ite %s %s %s)", cnd, th, el), nil
	case "let":
		bound, err := c.tr(t.A, env)
		if err != nil {
			return "", err
		}
		return c.tr(t.B, append(append([]smtVal{}, env...), smtVal{expr: bound}))
	case "prim":
		if t.Op == "/" || t.Op == "%" {
			return "", fmt.Errorf("%s is untranslatable (kernel truncates, SMT-LIB is Euclidean)", t.Op)
		}
		op, ok := smtPrimOps[t.Op]
		if !ok {
			return "", fmt.Errorf("primitive %s is outside the provable fragment", t.Op)
		}
		var parts []string
		for i := range t.Args {
			a, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", err
			}
			parts = append(parts, a)
		}
		return "(" + op + " " + strings.Join(parts, " ") + ")", nil
	case "app":
		head, args := unwindApp(t)
		switch head.K {
		case "var":
			v := env[len(env)-1-head.Idx]
			if v.fn == "" {
				return "", fmt.Errorf("application of a non-binder value")
			}
			if len(args) != v.arity {
				return "", fmt.Errorf("function binder must be fully applied")
			}
			var parts []string
			for _, a := range args {
				s, err := c.tr(a, env)
				if err != nil {
					return "", err
				}
				parts = append(parts, s)
			}
			return "(" + v.fn + " " + strings.Join(parts, " ") + ")", nil
		case "ref":
			d, err := c.st.GetDef(head.Hash)
			if err != nil {
				return "", err
			}
			return c.inlineDef(d, c.st.NameOf(head.Hash), args, env)
		case "self":
			// Inside a prop, "self" is the definition under proof, not
			// recursion — inline it (its own body must be non-recursive).
			if c.selfDef == nil {
				return "", fmt.Errorf("self-reference outside a definition")
			}
			return c.inlineDef(c.selfDef, "self", args, env)
		case "lam":
			// Beta-redex: substitute arguments directly.
			cur := head
			env2 := append([]smtVal{}, env...)
			consumed := 0
			for cur.K == "lam" && consumed < len(args) {
				s, err := c.tr(args[consumed], env)
				if err != nil {
					return "", err
				}
				env2 = append(env2, smtVal{expr: s})
				cur = cur.A
				consumed++
			}
			if consumed != len(args) || cur.K == "lam" {
				return "", fmt.Errorf("partial application is outside the provable fragment")
			}
			return c.tr(cur, env2)
		}
		return "", fmt.Errorf("application head %q is outside the provable fragment", head.K)
	case "ref":
		d, err := c.st.GetDef(t.Hash)
		if err != nil {
			return "", err
		}
		return c.inlineDef(d, c.st.NameOf(t.Hash), nil, env)
	case "self":
		if c.selfDef == nil {
			return "", fmt.Errorf("self-reference outside a definition")
		}
		return c.inlineDef(c.selfDef, "self", nil, env)
	}
	return "", fmt.Errorf("%q terms are outside the provable fragment", t.K)
}

// inlineDef substitutes a non-recursive definition's body at the call site.
func (c *smtCtx) inlineDef(d *Def, name string, args []*Term, env []smtVal) (string, error) {
	if d.K != "func" {
		return "", fmt.Errorf("data reference is outside the provable fragment")
	}
	if hasSelfRef(d.Body) {
		return "", fmt.Errorf("%s is recursive (induction is future work)", name)
	}
	cur := d.Body
	var callee []smtVal
	for i := 0; cur.K == "lam"; i++ {
		if i >= len(args) {
			return "", fmt.Errorf("%s must be fully applied to inline", name)
		}
		s, err := c.tr(args[i], env)
		if err != nil {
			return "", err
		}
		callee = append(callee, smtVal{expr: s})
		cur = cur.A
	}
	if len(callee) != len(args) {
		return "", fmt.Errorf("%s is over-applied; cannot inline", name)
	}
	return c.tr(cur, callee)
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

// proveProp attempts one property. Returns status: proven | refuted |
// unknown | outside, plus detail (bail reason, or Z3 model on refutation).
func proveProp(st *Store, d *Def, p *Prop) (string, string) {
	c := &smtCtx{st: st, selfDef: d}
	var env []smtVal
	for i := range p.Binders {
		v, err := c.declareBinder(i, &p.Binders[i])
		if err != nil {
			return "outside", err.Error()
		}
		env = append(env, v)
	}
	body, err := c.tr(&p.Body, env)
	if err != nil {
		return "outside", err.Error()
	}
	var script strings.Builder
	for _, d := range c.decls {
		script.WriteString(d + "\n")
	}
	fmt.Fprintf(&script, "(assert (not %s))\n(check-sat)\n(get-model)\n", body)

	ctx, cancel := context.WithTimeout(context.Background(), proveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "z3", "-in")
	cmd.Stdin = strings.NewReader(script.String())
	out, _ := cmd.CombinedOutput()
	text := string(out)
	switch {
	case strings.HasPrefix(text, "unsat"):
		return "proven", ""
	case strings.HasPrefix(text, "sat"):
		model := strings.TrimSpace(strings.TrimPrefix(text, "sat"))
		return "refuted", model
	}
	return "unknown", strings.TrimSpace(text)
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
	proven := 0
	anyRefuted := false
	for pi := range d.Props {
		pn := metaPropName(m, pi)
		status, detail := proveProp(st, d, &d.Props[pi])
		switch status {
		case "proven":
			proven++
			fmt.Fprintf(&b, "∎ PROVEN    %-28s holds for all inputs (Z3, unbounded ints)\n", pn)
		case "refuted":
			anyRefuted = true
			fmt.Fprintf(&b, "✗ REFUTED   %-28s counterexample over unbounded ints:\n", pn)
			for _, line := range strings.Split(detail, "\n") {
				fmt.Fprintf(&b, "      %s\n", line)
			}
		case "outside":
			fmt.Fprintf(&b, "· unproven  %-28s outside fragment: %s\n", pn, detail)
		default:
			fmt.Fprintf(&b, "· unproven  %-28s solver returned unknown\n", pn)
		}
	}
	fmt.Fprintf(&b, "proven: %d/%d properties\n", proven, len(d.Props))

	m.Guarantee.Proven = proven
	if proven == len(d.Props) && !anyRefuted && m.Guarantee.Level == "tested" {
		m.Guarantee.Level = "proven"
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
