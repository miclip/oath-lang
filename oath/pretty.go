package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The projection printer renders canonical definitions back into readable
// text for human auditors. This direction is lossy-in-reverse: generated
// binder names (x0, x1, ...) replace whatever names the author used, because
// the canonical form never stored them. What you read here is guaranteed to
// be what the kernel checked — there is no other source of truth to drift from.

func dataName(st *Store, h string) string {
	if m, err := st.GetMeta(h); err == nil {
		return m.Name
	}
	return "#" + shortHash(h)
}

func ctorName(st *Store, h string, idx int) string {
	if m, err := st.GetMeta(h); err == nil && idx < len(m.CtorNames) {
		return m.CtorNames[idx]
	}
	return fmt.Sprintf("#%s.%d", shortHash(h), idx)
}

func printTy(st *Store, t *Ty, tvs []string) string {
	if t == nil {
		return "?"
	}
	switch t.K {
	case "int":
		return "Int"
	case "bool":
		return "Bool"
	case "str":
		return "Str"
	case "record":
		parts := make([]string, 0, len(t.Names))
		for i, n := range t.Names {
			parts = append(parts, n+" "+printTy(st, &t.Args[i], tvs))
		}
		return "{" + strings.Join(parts, " ") + "}"
	case "var":
		if t.Var < len(tvs) {
			return tvs[t.Var]
		}
		return fmt.Sprintf("t%d", t.Var)
	case "fun":
		parts := []string{}
		cur := t
		for cur.K == "fun" {
			parts = append(parts, printTy(st, cur.A, tvs))
			cur = cur.B
		}
		parts = append(parts, printTy(st, cur, tvs))
		return "(-> " + strings.Join(parts, " ") + ")"
	case "data", "rec":
		name := "self"
		if t.K == "data" {
			name = dataName(st, t.Hash)
		}
		if len(t.Args) == 0 {
			return name
		}
		parts := []string{name}
		for i := range t.Args {
			parts = append(parts, printTy(st, &t.Args[i], tvs))
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
	return "?"
}

type printer struct {
	st      *Store
	tvs     []string
	names   []string // binder names, innermost last
	counter int
}

func (p *printer) fresh() string {
	n := fmt.Sprintf("x%d", p.counter)
	p.counter++
	return n
}

func (p *printer) tyArgs(tyargs []Ty) string {
	if len(tyargs) == 0 {
		return ""
	}
	parts := make([]string, len(tyargs))
	for i := range tyargs {
		parts[i] = printTy(p.st, &tyargs[i], p.tvs)
	}
	return " [" + strings.Join(parts, " ") + "]"
}

func (p *printer) term(t *Term, selfName string) string {
	switch t.K {
	case "var":
		if t.Idx < len(p.names) {
			return p.names[len(p.names)-1-t.Idx]
		}
		return fmt.Sprintf("?v%d", t.Idx)
	case "int":
		return fmt.Sprintf("%d", t.Int)
	case "bool":
		return fmt.Sprintf("%v", t.Bool)
	case "str":
		return strconv.Quote(t.Str)
	case "record":
		parts := make([]string, 0, len(t.Names))
		for i, n := range t.Names {
			parts = append(parts, n+" "+p.term(&t.Args[i], selfName))
		}
		return "{" + strings.Join(parts, " ") + "}"
	case "field":
		return "(. " + p.term(t.A, selfName) + " " + t.Op + ")"
	case "lam":
		var params []string
		cur := t
		n := 0
		for cur.K == "lam" {
			name := p.fresh()
			p.names = append(p.names, name)
			params = append(params, "("+name+" "+printTy(p.st, cur.Ty, p.tvs)+")")
			cur = cur.A
			n++
		}
		body := p.term(cur, selfName)
		p.names = p.names[:len(p.names)-n]
		return "(fn [" + strings.Join(params, " ") + "] " + body + ")"
	case "app":
		// Flatten application chains.
		var args []string
		cur := t
		for cur.K == "app" {
			args = append([]string{p.term(cur.B, selfName)}, args...)
			cur = cur.A
		}
		return "(" + p.term(cur, selfName) + " " + strings.Join(args, " ") + ")"
	case "let":
		name := p.fresh()
		bound := p.term(t.A, selfName)
		p.names = append(p.names, name)
		body := p.term(t.B, selfName)
		p.names = p.names[:len(p.names)-1]
		return "(let (" + name + " " + printTy(p.st, t.Ty, p.tvs) + " " + bound + ") " + body + ")"
	case "if":
		return "(if " + p.term(t.A, selfName) + " " + p.term(t.B, selfName) + " " + p.term(t.C, selfName) + ")"
	case "prim":
		parts := []string{t.Op}
		for i := range t.Args {
			parts = append(parts, p.term(&t.Args[i], selfName))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case "ref":
		return p.st.NameOf(t.Hash) + p.tyArgs(t.TyArgs)
	case "self":
		return selfName + p.tyArgs(t.TyArgs)
	case "ctor":
		s := ctorName(p.st, t.Hash, t.Idx) + p.tyArgs(t.TyArgs)
		if len(t.Args) == 0 && len(t.TyArgs) == 0 {
			return s
		}
		parts := []string{s}
		for i := range t.Args {
			parts = append(parts, p.term(&t.Args[i], selfName))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case "match":
		d, err := p.st.GetDef(t.Hash)
		if err != nil {
			return "(match ?)"
		}
		s := "(match " + p.term(t.A, selfName)
		for i := range t.Arms {
			nFields := len(d.Ctors[i])
			var binders []string
			for j := 0; j < nFields; j++ {
				binders = append(binders, p.fresh())
			}
			p.names = append(p.names, binders...)
			body := p.term(&t.Arms[i], selfName)
			p.names = p.names[:len(p.names)-nFields]
			pat := ctorName(p.st, t.Hash, i)
			if len(binders) > 0 {
				pat += " " + strings.Join(binders, " ")
			}
			s += " ((" + pat + ") " + body + ")"
		}
		return s + ")"
	}
	return "?"
}

func printValue(st *Store, v Value) string {
	switch v.K {
	case "int":
		return fmt.Sprintf("%d", v.Int)
	case "bool":
		return fmt.Sprintf("%v", v.Bool)
	case "str":
		return strconv.Quote(v.Str)
	case "record":
		parts := make([]string, 0, len(v.Names))
		for i, n := range v.Names {
			parts = append(parts, n+" "+printValue(st, v.Fields[i]))
		}
		return "{" + strings.Join(parts, " ") + "}"
	case "data":
		name := ctorName(st, v.Hash, v.Idx)
		if len(v.Fields) == 0 {
			return name
		}
		parts := []string{name}
		for _, f := range v.Fields {
			parts = append(parts, printValue(st, f))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case "closure":
		return "<fn>"
	case "native":
		switch v.Native {
		case "id":
			return "<fn x. x>"
		case "affine":
			return fmt.Sprintf("<fn x. %d*x + %d>", v.NA, v.NB)
		case "const":
			return fmt.Sprintf("<fn _. %s>", printValue(st, *v.NVal))
		}
	}
	return "?"
}

func guaranteeString(g Guarantee) string {
	switch g.Level {
	case "tested":
		return fmt.Sprintf("tested (%d cases per property)", g.Cases)
	case "falsified":
		return "FALSIFIED: " + strings.Join(g.Falsified, ", ")
	case "proven":
		return "proven"
	}
	return "asserted (no properties checked)"
}

// printSpec renders the spec-only projection: everything an author needs to
// BUILD ON a definition (signature, properties, guarantee) and nothing it
// doesn't (the body). For data definitions the constructors are the spec.
func printSpec(st *Store, h string) (string, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	tvs := m.TyVarNames
	switch d.K {
	case "data":
		fmt.Fprintf(&b, "data %s", m.Name)
		if len(tvs) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(tvs, " "))
		}
		fmt.Fprintf(&b, "\n")
		for i, fields := range d.Ctors {
			cn := "?"
			if i < len(m.CtorNames) {
				cn = m.CtorNames[i]
			}
			fmt.Fprintf(&b, "  (%s", cn)
			for fi := range fields {
				fmt.Fprintf(&b, " %s", printTy(st, &fields[fi], tvs))
			}
			fmt.Fprintf(&b, ")\n")
		}
		fmt.Fprintf(&b, "  #%s\n", shortHash(h))
	case "func":
		fmt.Fprintf(&b, "%s : %s", m.Name, printTy(st, d.Ty, tvs))
		if len(tvs) > 0 {
			fmt.Fprintf(&b, "   forall %s", strings.Join(tvs, " "))
		}
		fmt.Fprintf(&b, "\n")
		for pi, p := range d.Props {
			pn := fmt.Sprintf("prop%d", pi)
			if pi < len(m.PropNames) {
				pn = m.PropNames[pi]
			}
			pp := &printer{st: st, tvs: nil}
			var binders []string
			for range p.Binders {
				binders = append(binders, pp.fresh())
			}
			pp.names = binders
			var bparts []string
			for bi := range p.Binders {
				bparts = append(bparts, "("+binders[bi]+" "+printTy(st, &p.Binders[bi], nil)+")")
			}
			fmt.Fprintf(&b, "  prop %s: forall [%s]. %s\n", pn, strings.Join(bparts, " "), pp.term(&p.Body, m.Name))
		}
		fmt.Fprintf(&b, "  guarantee: %s%s%s   #%s\n", guaranteeString(m.Guarantee), specStrengthString(m), termSuffix(m), shortHash(h))
	}
	return b.String(), nil
}

func termSuffix(m *Meta) string {
	switch m.Termination {
	case "structural", "nonrecursive":
		return " · total"
	case "unknown":
		return " · termination unproven"
	}
	return ""
}

func terminationString(term string) string {
	switch term {
	case "structural":
		return "total (structural recursion)"
	case "nonrecursive":
		return "total (non-recursive, all callees total)"
	case "unknown":
		return "not proven total (fuel-bounded at runtime)"
	}
	return ""
}

func specStrengthString(m *Meta) string {
	if m.MutantsTotal == 0 {
		return ""
	}
	return fmt.Sprintf(" · spec strength %d/%d mutants killed", m.MutantsKilled, m.MutantsTotal)
}

// printDef renders the full human projection of a definition.
func printDef(st *Store, h string) (string, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	tvs := m.TyVarNames

	switch d.K {
	case "data":
		fmt.Fprintf(&b, "data %s", m.Name)
		if len(tvs) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(tvs, " "))
		}
		fmt.Fprintf(&b, "\n")
		for i, fields := range d.Ctors {
			cn := "?"
			if i < len(m.CtorNames) {
				cn = m.CtorNames[i]
			}
			fmt.Fprintf(&b, "  (%s", cn)
			for fi := range fields {
				fmt.Fprintf(&b, " %s", printTy(st, &fields[fi], tvs))
			}
			fmt.Fprintf(&b, ")\n")
		}
	case "func":
		fmt.Fprintf(&b, "%s : %s", m.Name, printTy(st, d.Ty, tvs))
		if len(tvs) > 0 {
			fmt.Fprintf(&b, "   forall %s", strings.Join(tvs, " "))
		}
		fmt.Fprintf(&b, "\n")
		pr := &printer{st: st, tvs: tvs}
		fmt.Fprintf(&b, "  = %s\n", pr.term(d.Body, m.Name))
		for pi, p := range d.Props {
			pn := fmt.Sprintf("prop%d", pi)
			if pi < len(m.PropNames) {
				pn = m.PropNames[pi]
			}
			pp := &printer{st: st, tvs: nil}
			var binders []string
			for range p.Binders {
				binders = append(binders, pp.fresh())
			}
			pp.names = binders
			var bparts []string
			for bi := range p.Binders {
				bparts = append(bparts, "("+binders[bi]+" "+printTy(st, &p.Binders[bi], nil)+")")
			}
			fmt.Fprintf(&b, "  prop %s: forall [%s]. %s\n", pn, strings.Join(bparts, " "), pp.term(&p.Body, m.Name))
		}
	}

	fmt.Fprintf(&b, "hash: %s\n", h)
	fmt.Fprintf(&b, "guarantee: %s%s\n", guaranteeString(m.Guarantee), specStrengthString(m))
	if ts := terminationString(m.Termination); ts != "" {
		fmt.Fprintf(&b, "termination: %s\n", ts)
	}

	deps := collectDeps(d)
	if len(deps) > 0 {
		var names []string
		for dh := range deps {
			names = append(names, st.NameOf(dh))
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "deps: %s\n", strings.Join(names, ", "))
	}
	return b.String(), nil
}
