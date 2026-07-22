package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// `oath build <name>` — the first rung of #13: compile a definition's
// dependency closure to a standalone native executable, by emitting a
// self-contained Go program and letting `go build` do native codegen.
//
// The run/verify distinction, made explicit: compiled programs carry NO
// fuel or depth bounds — those are verification semantics. What they carry
// instead is provenance: the compiler REFUSES any entry point that failed
// the gate (unstoreable anyway), was falsified, or was never verified — an
// executable is a proof-carrying artifact, or it isn't built.
//
// Stage-1 entry protocol: main : (-> (List Str) Str). argv (after the
// program name) becomes the list; the result is written to stdout with a
// trailing newline. Exit code 0. Capability entry points (real IO wired at
// the boundary — effects stage 4) are the next rung.
//
// Compilation model: type-erased. Values are Go `any` (int64, bool, string,
// *closure, *ctorV); each Oath function becomes one Go function taking and
// returning `any`, generics erased, matches by constructor index, direct
// recursion. Not fast-path native — but genuinely compiled, and the
// differential gate (`compiled output == oath eval output`) keeps it honest.

func cmdBuild(st *Store, name, out string) {
	h, ok := st.Resolve(name)
	if !ok {
		fail(fmt.Errorf("no definition named %q", name))
	}
	d, err := st.GetDef(h)
	if err != nil {
		fail(err)
	}
	m, err := st.GetMeta(h)
	if err != nil {
		fail(err)
	}
	if d.K != "func" {
		fail(fmt.Errorf("%s is a data definition; entry points are functions", name))
	}
	// Provenance gate: executables are proof-carrying artifacts.
	switch m.Guarantee.Level {
	case "falsified":
		fail(fmt.Errorf("%s is FALSIFIED (%s) — refusing to build an executable from a broken oath", name, strings.Join(m.Guarantee.Falsified, ", ")))
	case "asserted":
		fail(fmt.Errorf("%s has no verified properties — swear and verify an oath before building", name))
	}
	// Entry protocols: (-> (List Str) Str), or capability-first
	// (-> {caps...} (-> (List Str) Str)) with every field wired (stage 2).
	capTy, ok := entryShape(st, d.Ty)
	if !ok {
		fail(fmt.Errorf("%s : %s — entry protocol requires (-> (List Str) Str) or (-> {caps} (List Str) Str) with wireable capabilities", name, debugTy(d.Ty)))
	}
	if capTy != nil {
		for i, n := range capTy.Names {
			if _, ok := capWiring(n, &capTy.Args[i]); !ok {
				fail(fmt.Errorf("no real-world wiring for capability field %s : %s (wireable: fetch (-> Str Str), env (-> Str Str), readfile (-> Str Str))", n, debugTy(&capTy.Args[i])))
			}
		}
		// Confinement gate: an entry point that STORES or RETURNS its
		// capability has no business receiving the real one.
		if len(m.Confinement) > 0 && m.Confinement[0] == "escapes" {
			fail(fmt.Errorf("%s's capability parameter ESCAPES (stored or returned) — refusing to hand it the real world", name))
		}
	}

	src, err := emitProgram(st, h, capTy)
	if err != nil {
		fail(err)
	}
	tmp, err := os.MkdirTemp("", "oath-build-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(src), 0o644); err != nil {
		fail(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		fail(err)
	}
	if out == "" {
		out = name
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		fail(err)
	}
	cmd := exec.Command("go", "build", "-o", abs, ".")
	cmd.Dir = tmp
	if b, err := cmd.CombinedOutput(); err != nil {
		fail(fmt.Errorf("go build failed:\n%s", string(b)))
	}
	fmt.Printf("built %s → %s  (entry %s : %s, guarantee: %s)\n",
		name, out, name, printTy(st, d.Ty, m.TyVarNames), guaranteeString(m.Guarantee))
}

// entryShape classifies an entry type. Returns (nil, true) for the pure
// protocol (-> (List Str) Str); (capRecord, true) for the capability-first
// protocol (-> {caps} (-> (List Str) Str)); (nil, false) otherwise.
func entryShape(st *Store, t *Ty) (*Ty, bool) {
	if isPureEntry(st, t) {
		return nil, true
	}
	if t != nil && t.K == "fun" && t.A != nil && t.A.K == "record" && isPureEntry(st, t.B) {
		return t.A, true
	}
	return nil, false
}

func isPureEntry(st *Store, t *Ty) bool {
	if t == nil || t.K != "fun" || t.B == nil || t.B.K != "str" {
		return false
	}
	a := t.A
	if a == nil || a.K != "data" || len(a.Args) != 1 || a.Args[0].K != "str" {
		return false
	}
	if m, err := st.GetMeta(a.Hash); err == nil {
		return m.Name == "List"
	}
	return false
}

// capWiring returns the Go expression for a capability field's REAL
// implementation. This is effects stage 4: authority enters the program
// exactly once, here, at the boundary — everything below received it as an
// ordinary argument and was verified against all simulated worlds.
func capWiring(name string, t *Ty) (string, bool) {
	strToStr := t.K == "fun" && t.A != nil && t.A.K == "str" && t.B != nil && t.B.K == "str"
	if !strToStr {
		return "", false
	}
	switch name {
	case "fetch":
		return `capFn(func(s string) string {
		resp, err := http.Get(s)
		if err != nil { return "" }
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil { return "" }
		return string(b)
	})`, true
	case "env":
		return `capFn(os.Getenv)`, true
	case "readfile":
		return `capFn(func(s string) string {
		b, err := os.ReadFile(s)
		if err != nil { return "" }
		return string(b)
	})`, true
	}
	return "", false
}

// ---------- emitter ----------

type emitter struct {
	st    *Store
	b     strings.Builder
	fname map[string]string // def hash → emitted Go function name
	order []string          // emission order (deps first)
	seen  map[string]bool
	// Type tracking for record field resolution: the kernel's own checker,
	// threaded alongside compilation. ctx mirrors the de Bruijn env.
	chk *checker
	ctx []*Ty
}

func emitProgram(st *Store, entry string, capTy *Ty) (string, error) {
	e := &emitter{st: st, fname: map[string]string{}, seen: map[string]bool{}}
	if err := e.closure(entry); err != nil {
		return "", err
	}
	e.b.WriteString(`// Generated by oath build — do not edit.
// Values: int64 | bool | string | *closure | *ctorV (type-erased).
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

var _ = io.ReadAll
var _ = http.Get

// capFn lifts a real-world Go function into an Oath closure value.
func capFn(f func(string) string) *closure {
	return &closure{code: func(env []any, arg any) any { return f(arg.(string)) }}
}

type ctorV struct {
	idx    int
	fields []any
}

type closure struct {
	env  []any // captured, innermost last
	code func(env []any, arg any) any
}

func apply(f, a any) any {
	c := f.(*closure)
	return c.code(append(append([]any{}, c.env...), a), a)
}

func runeLen(s string) int64 { return int64(len([]rune(s))) }

func strStartsWith(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func strEndsWith(s, p string) bool   { return len(s) >= len(p) && s[len(s)-len(p):] == p }
func strContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func structEq(a, b any) bool {
	switch x := a.(type) {
	case int64:
		return x == b.(int64)
	case bool:
		return x == b.(bool)
	case string:
		return x == b.(string)
	case *ctorV:
		y := b.(*ctorV)
		if x.idx != y.idx || len(x.fields) != len(y.fields) {
			return false
		}
		for i := range x.fields {
			if !structEq(x.fields[i], y.fields[i]) {
				return false
			}
		}
		return true
	}
	panic("structEq on function value")
}

`)
	for _, h := range e.order {
		if err := e.emitDef(h); err != nil {
			return "", err
		}
	}
	// main: argv → List Str; capability record (if any) wired with REAL
	// implementations, applied first.
	caps := ""
	entryCall := fmt.Sprintf("%s(nil, args)", e.fname[entry])
	if capTy != nil {
		var fields []string
		for i, n := range capTy.Names {
			w, _ := capWiring(n, &capTy.Args[i])
			fields = append(fields, w)
		}
		caps = fmt.Sprintf("\tvar realWorld any = &ctorV{idx: -1, fields: []any{%s}}\n", strings.Join(fields, ", "))
		entryCall = fmt.Sprintf("apply(%s(nil, realWorld), args)", e.fname[entry])
	}
	fmt.Fprintf(&e.b, `
func main() {
	var args any = &ctorV{idx: 0} // Nil
	for i := len(os.Args) - 1; i >= 1; i-- {
		args = &ctorV{idx: 1, fields: []any{os.Args[i], args}} // Cons
	}
%s	out := %s
	fmt.Println(out.(string))
	os.Exit(0)
}
`, caps, entryCall)
	return e.b.String(), nil
}

// closure orders the entry's dependency closure, functions only, deps first.
func (e *emitter) closure(h string) error {
	if e.seen[h] {
		return nil
	}
	e.seen[h] = true
	d, err := e.st.GetDef(h)
	if err != nil {
		return err
	}
	if d.K != "func" {
		return nil // datatypes are erased to ctor indices
	}
	for dep := range collectDepsBody(d) {
		if err := e.closure(dep); err != nil {
			return err
		}
	}
	e.fname[h] = "f_" + smtName(e.st.NameOf(h)) + "_" + h[:8]
	e.order = append(e.order, h)
	return nil
}

// emitDef compiles one function to a Go function of shape
// func(env []any, arg any) any, uncurried across its leading lams by
// chaining closures exactly like the evaluator does.
func (e *emitter) emitDef(h string) error {
	d, _ := e.st.GetDef(h)
	name := e.fname[h]
	e.chk = &checker{st: e.st, selfTyVars: d.TyVars, selfTy: d.Ty}
	e.ctx = nil
	// A def value is its body evaluated in an empty env: for a lam chain we
	// emit fn(env, arg) = body of the FIRST lam with arg bound; deeper lams
	// become closures inside. To keep uniform apply semantics we emit the
	// def as a zero-arg construction returning its value, plus a fast entry
	// for the common fully-applied case handled by the expression compiler.
	fmt.Fprintf(&e.b, "// %s\nfunc %s(env []any, arg any) any {\n", e.st.NameOf(h), name)
	if d.Body.K == "lam" {
		e.ctx = []*Ty{d.Body.Ty}
		body, err := e.expr(d.Body.A, 1, h)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.b, "\t_ = env\n\tenv = []any{arg}\n\t_ = env\n\treturn %s\n}\n\n", body)
		return nil
	}
	body, err := e.expr(d.Body, 0, h)
	if err != nil {
		return err
	}
	fmt.Fprintf(&e.b, "\t_ = env\n\t_ = arg\n\treturn %s\n}\n\n", body)
	return nil
}

// expr compiles a term to a Go expression. depth = number of binders in
// scope (env has that many entries, innermost last).
func (e *emitter) expr(t *Term, depth int, self string) (string, error) {
	switch t.K {
	case "var":
		return fmt.Sprintf("env[%d]", depth-1-t.Idx), nil
	case "int":
		return fmt.Sprintf("int64(%d)", t.Int), nil
	case "bool":
		return strconv.FormatBool(t.Bool), nil
	case "str":
		return strconv.Quote(t.Str), nil
	case "lam":
		e.ctx = append(e.ctx, t.Ty)
		body, err := e.expr(t.A, depth+1, self)
		e.ctx = e.ctx[:len(e.ctx)-1]
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(&closure{env: env, code: func(env []any, arg any) any { _ = arg; return %s }})", body), nil
	case "app":
		f, err := e.expr(t.A, depth, self)
		if err != nil {
			return "", err
		}
		a, err := e.expr(t.B, depth, self)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("apply(%s, %s)", f, a), nil
	case "let":
		bound, err := e.expr(t.A, depth, self)
		if err != nil {
			return "", err
		}
		e.ctx = append(e.ctx, t.Ty)
		body, err := e.expr(t.B, depth+1, self)
		e.ctx = e.ctx[:len(e.ctx)-1]
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(func(env []any) any { return %s }(append(append([]any{}, env...), %s)))", body, bound), nil
	case "if":
		c, err := e.expr(t.A, depth, self)
		if err != nil {
			return "", err
		}
		th, err := e.expr(t.B, depth, self)
		if err != nil {
			return "", err
		}
		el, err := e.expr(t.C, depth, self)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(func() any { if %s.(bool) { return %s }; return %s })()", c, th, el), nil
	case "prim":
		return e.prim(t, depth, self)
	case "ref":
		fn, ok := e.fname[t.Hash]
		if !ok {
			return "", fmt.Errorf("unemitted reference %s", shortHash(t.Hash))
		}
		return e.defValue(t.Hash, fn)
	case "self":
		return e.defValue(self, e.fname[self])
	case "ctor":
		var parts []string
		for i := range t.Args {
			a, err := e.expr(&t.Args[i], depth, self)
			if err != nil {
				return "", err
			}
			parts = append(parts, a)
		}
		return fmt.Sprintf("(&ctorV{idx: %d, fields: []any{%s}})", t.Idx, strings.Join(parts, ", ")), nil
	case "match":
		s, err := e.expr(t.A, depth, self)
		if err != nil {
			return "", err
		}
		md, err := e.st.GetDef(t.Hash)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("(func(scrut *ctorV, env []any) any {\n\t\tswitch scrut.idx {\n")
		scrutTy, tyErr := e.chk.synth(e.ctx, t.A)
		for i := range t.Arms {
			n := len(md.Ctors[i])
			if tyErr == nil && scrutTy.K == "data" {
				for _, f := range instCtorFields(md, scrutTy.Hash, scrutTy.Args, i) {
					e.ctx = append(e.ctx, f)
				}
			} else {
				for j := 0; j < n; j++ {
					e.ctx = append(e.ctx, tInt()) // placeholder; only records need truth
				}
			}
			arm, err := e.expr(&t.Arms[i], depth+n, self)
			e.ctx = e.ctx[:len(e.ctx)-n]
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "\t\tcase %d:\n\t\t\tenv = append(append([]any{}, env...), scrut.fields...)\n\t\t\t_ = env\n\t\t\treturn %s\n", i, arm)
		}
		b.WriteString("\t\t}\n\t\tpanic(\"non-exhaustive\")\n\t})(" + s + ".(*ctorV), env)")
		return b.String(), nil
	case "record":
		var parts []string
		for i := range t.Args {
			a, err := e.expr(&t.Args[i], depth, self)
			if err != nil {
				return "", err
			}
			parts = append(parts, a)
		}
		// Records compile as ctorV with idx -1 and canonical field order.
		return fmt.Sprintf("(&ctorV{idx: -1, fields: []any{%s}})", strings.Join(parts, ", ")), nil
	case "field":
		r, err := e.expr(t.A, depth, self)
		if err != nil {
			return "", err
		}
		rt, err := e.chk.synth(e.ctx, t.A)
		if err != nil {
			return "", fmt.Errorf("cannot type record expression for field %q: %v", t.Op, err)
		}
		if rt.K != "record" {
			return "", fmt.Errorf("field %q on non-record type %s", t.Op, debugTy(rt))
		}
		for i, n := range rt.Names {
			if n == t.Op {
				return fmt.Sprintf("(%s.(*ctorV).fields[%d])", r, i), nil
			}
		}
		return "", fmt.Errorf("record %s has no field %q", debugTy(rt), t.Op)
	}
	return "", fmt.Errorf("cannot compile %q terms yet", t.K)
}

// defValue emits a reference to a def as a value: lam-chains become their
// outermost closure; zero-param defs evaluate their body.
func (e *emitter) defValue(h, fn string) (string, error) {
	d, err := e.st.GetDef(h)
	if err != nil {
		return "", err
	}
	if d.Body.K == "lam" {
		return fmt.Sprintf("(&closure{env: nil, code: %s})", fn), nil
	}
	return fmt.Sprintf("%s(nil, nil)", fn), nil
}

func (e *emitter) prim(t *Term, depth int, self string) (string, error) {
	var args []string
	for i := range t.Args {
		a, err := e.expr(&t.Args[i], depth, self)
		if err != nil {
			return "", err
		}
		args = append(args, a)
	}
	bin := func(op string) string {
		return fmt.Sprintf("(%s.(int64) %s %s.(int64))", args[0], op, args[1])
	}
	switch t.Op {
	case "+", "-", "*":
		return "any" + bin(t.Op), nil
	case "/":
		return fmt.Sprintf("any(%s.(int64) / %s.(int64))", args[0], args[1]), nil
	case "%":
		return fmt.Sprintf("any(%s.(int64) %% %s.(int64))", args[0], args[1]), nil
	case "neg":
		return fmt.Sprintf("any(-%s.(int64))", args[0]), nil
	case "<":
		return "any" + bin("<"), nil
	case "<=":
		return "any" + bin("<="), nil
	case "and", "or":
		// Oath's and/or are NOT short-circuiting: both operands evaluate.
		op := "&&"
		if t.Op == "or" {
			op = "||"
		}
		return fmt.Sprintf("(func() any { a := %s.(bool); b := %s.(bool); return any(a %s b) })()", args[0], args[1], op), nil
	case "not":
		return fmt.Sprintf("any(!%s.(bool))", args[0]), nil
	case "==":
		return fmt.Sprintf("any(structEq(%s, %s))", args[0], args[1]), nil
	case "++":
		return fmt.Sprintf("any(%s.(string) + %s.(string))", args[0], args[1]), nil
	case "str-len":
		return fmt.Sprintf("any(runeLen(%s.(string)))", args[0]), nil
	case "starts-with":
		return fmt.Sprintf("any(strStartsWith(%s.(string), %s.(string)))", args[0], args[1]), nil
	case "ends-with":
		return fmt.Sprintf("any(strEndsWith(%s.(string), %s.(string)))", args[0], args[1]), nil
	case "str-contains":
		return fmt.Sprintf("any(strContains(%s.(string), %s.(string)))", args[0], args[1]), nil
	}
	return "", fmt.Errorf("cannot compile primitive %q", t.Op)
}

// sortedDepList is a helper for deterministic emission (unused fields kept
// minimal; ordering handled in closure()).
var _ = sort.Strings
