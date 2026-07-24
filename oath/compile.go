package main

import (
	"fmt"
	"math"
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
			if _, ok := capWiring(st, n, &capTy.Args[i]); !ok {
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

// strTypeHash is the hash of the `Str` datatype in this store (the string type
// by convention), or "" if none is defined. isStrTy recognizes a type as that
// datatype — the compiler represents such values as native Go strings.
func strTypeHash(st *Store) string { h, _ := st.Resolve("Str"); return h }

func isStrTy(strHash string, t *Ty) bool {
	return strHash != "" && t != nil && t.K == "data" && t.Hash == strHash
}

func isPureEntry(st *Store, t *Ty) bool {
	sh := strTypeHash(st)
	if t == nil || t.K != "fun" || !isStrTy(sh, t.B) {
		return false
	}
	a := t.A
	if a == nil || a.K != "data" || len(a.Args) != 1 || !isStrTy(sh, &a.Args[0]) {
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
func capWiring(st *Store, name string, t *Ty) (string, bool) {
	sh := strTypeHash(st)
	strToStr := t.K == "fun" && isStrTy(sh, t.A) && isStrTy(sh, t.B)
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
	st      *Store
	b       strings.Builder
	fname   map[string]string // def hash → emitted Go function name
	order   []string          // emission order (deps first)
	seen    map[string]bool
	strHash string // hash of the Str datatype; its values compile to Go strings
	setHash string // hash of the Set datatype; its values compile to native osets
	mapHash string // hash of the Map datatype; its values compile to native omaps
	setOps  map[string]setOp // recognized Set/Map-op hashes → native helper + arity
	// Type tracking for record field resolution: the kernel's own checker,
	// threaded alongside compilation. ctx mirrors the de Bruijn env.
	chk *checker
	ctx []*Ty
}

// setOp names the native Go helper a recognized Set operation lowers to and its
// arity (so only a SATURATED application is intercepted).
type setOp struct {
	helper string
	arity  int
}

// setOpTable maps the recognized Set/Map-operation names to their native
// lowering. A Set flows at runtime as an oset and a Map as an omap; these
// helpers keep values in that form. Arity is the SATURATED argument count.
var setOpTable = map[string]setOp{
	"set-empty":  {"osetEmpty", 0},
	"set-member": {"osetMember", 2},
	"set-add":    {"osetAdd", 2},
	"set-union":  {"osetUnion", 2},
	"set-inter":  {"osetInter", 2},
	"set-size":   {"osetSize", 1},
	"set-elems":  {"osetElems", 1},
	"map-empty":  {"omapEmpty", 0},
	"map-insert": {"omapInsert", 3},
	"map-lookup": {"omapLookup", 2},
	"map-has":    {"omapHas", 2},
	"map-keys":   {"omapKeys", 1},
	"map-values": {"omapValues", 1},
	"map-size":   {"omapSize", 1},
	"map-merge":  {"omapMerge", 2},
}

// resolveNativeContainers records the Set/Map datatype hashes and the recognized
// operation hashes, so the compiler can lower them to native oset/omap code.
func (e *emitter) resolveNativeContainers() {
	e.setHash, _ = e.st.Resolve("Set")
	e.mapHash, _ = e.st.Resolve("Map")
	e.setOps = map[string]setOp{}
	for name, op := range setOpTable {
		if h, ok := e.st.Resolve(name); ok {
			e.setOps[h] = op
		}
	}
}

func emitProgram(st *Store, entry string, capTy *Ty) (string, error) {
	e := &emitter{st: st, fname: map[string]string{}, seen: map[string]bool{}, strHash: strTypeHash(st)}
	e.resolveNativeContainers()
	if err := e.closure(entry); err != nil {
		return "", err
	}
	e.b.WriteString(`// Generated by oath build — do not edit.
// Values: int64 | bool | string | *closure | *ctorV (type-erased).
package main

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"sort"
	"unicode/utf8"
)

var _ = io.ReadAll
var _ = http.Get
var _ = utf8.DecodeRuneInString
var _ = math.Float64bits
var _ = sort.Slice

// bi parses a decimal integer literal into an arbitrary-precision value.
func bi(s string) *big.Int { v, _ := new(big.Int).SetString(s, 10); return v }

// ra parses a rational literal ("num/den") into an exact big.Rat.
func ra(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }

// canonF canonicalizes a float64: every NaN becomes the one canonical NaN, so
// runtime identity matches the kernel (and prover). -0.0 and ±inf are kept.
func canonF(f float64) float64 {
	if math.IsNaN(f) {
		return math.Float64frombits(0x7FF8000000000000)
	}
	return f
}

// Numeric conversions (mirror the interpreter). Widening is total; the
// Float→{Rat,Int} narrowings panic on non-finite input, matching eval's error.
func i2r(x *big.Int) *big.Rat { return new(big.Rat).SetInt(x) }
func i2f(x *big.Int) float64  { f, _ := new(big.Float).SetInt(x).Float64(); return canonF(f) }
func r2f(x *big.Rat) float64  { f, _ := x.Float64(); return canonF(f) }
func f2r(x float64) *big.Rat {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		panic("to-rat of non-finite float")
	}
	return new(big.Rat).SetFloat64(x)
}
func rfloor(x *big.Rat) *big.Int { return new(big.Int).Div(x.Num(), x.Denom()) }
func ffloor(x float64) *big.Int {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		panic("floor of non-finite float")
	}
	i, _ := big.NewFloat(math.Floor(x)).Int(nil)
	return i
}

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


func structEq(a, b any) bool {
	switch x := a.(type) {
	case *big.Int:
		return x.Cmp(b.(*big.Int)) == 0
	case *big.Rat:
		return x.Cmp(b.(*big.Rat)) == 0
	case float64:
		// Bitwise (Leibniz / SMT =) on canonicalized values: NaN == NaN, +0 != -0.
		return math.Float64bits(canonF(x)) == math.Float64bits(canonF(b.(float64)))
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

// oset is the native representation of a Set: a hash map keyed by the element's
// canonical decimal, mapping to the element. Membership/size are O(1); the pure
// updates copy-on-write (O(n)); iteration re-materializes the sorted list, so
// the compiled Set agrees with the structural sorted-list model on every
// observable output (the differential gate).
type oset = map[string]*big.Int

func osetEmpty() any { return oset{} }
func osetAdd(x, s any) any {
	old := s.(oset)
	n := make(oset, len(old)+1)
	for k, v := range old {
		n[k] = v
	}
	xi := x.(*big.Int)
	n[xi.String()] = xi
	return any(n)
}
func osetMember(x, s any) any { _, ok := s.(oset)[x.(*big.Int).String()]; return any(ok) }
func osetUnion(a, b any) any {
	n := make(oset)
	for k, v := range a.(oset) {
		n[k] = v
	}
	for k, v := range b.(oset) {
		n[k] = v
	}
	return any(n)
}
func osetInter(a, b any) any {
	n := oset{}
	bm := b.(oset)
	for k, v := range a.(oset) {
		if _, ok := bm[k]; ok {
			n[k] = v
		}
	}
	return any(n)
}
func osetSize(s any) any { return any(big.NewInt(int64(len(s.(oset))))) }
func osetSorted(s any) []*big.Int {
	m := s.(oset)
	ks := make([]*big.Int, 0, len(m))
	for _, v := range m {
		ks = append(ks, v)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].Cmp(ks[j]) < 0 })
	return ks
}
func osetElems(s any) any {
	var lst any = &ctorV{idx: 0} // Nil
	ks := osetSorted(s)
	for i := len(ks) - 1; i >= 0; i-- {
		lst = &ctorV{idx: 1, fields: []any{ks[i], lst}} // Cons
	}
	return lst
}
func osetFromList(l any) any {
	n := oset{}
	for c := l.(*ctorV); c.idx == 1; c = c.fields[1].(*ctorV) {
		xi := c.fields[0].(*big.Int)
		n[xi.String()] = xi
	}
	return any(n)
}

// omap is the native representation of a Map: a hash map keyed by the key's
// canonical decimal, mapping to the (key, value) entry. Lookup/has/size are
// O(1); pure updates copy-on-write; boundaries (keys/values/match) materialize
// the key-sorted entries so the compiled Map agrees with the structural model.
type omapEnt struct {
	k *big.Int
	v any
}
type omap = map[string]omapEnt

func omapEmpty() any { return omap{} }
func omapInsert(k, v, m any) any {
	old := m.(omap)
	n := make(omap, len(old)+1)
	for kk, vv := range old {
		n[kk] = vv
	}
	ki := k.(*big.Int)
	n[ki.String()] = omapEnt{ki, v}
	return any(n)
}
func omapLookup(k, m any) any {
	if e, ok := m.(omap)[k.(*big.Int).String()]; ok {
		return &ctorV{idx: 1, fields: []any{e.v}} // Some v
	}
	return &ctorV{idx: 0} // None
}
func omapHas(k, m any) any { _, ok := m.(omap)[k.(*big.Int).String()]; return any(ok) }
func omapSize(m any) any   { return any(big.NewInt(int64(len(m.(omap))))) }
func omapSorted(m any) []omapEnt {
	mm := m.(omap)
	es := make([]omapEnt, 0, len(mm))
	for _, e := range mm {
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool { return es[i].k.Cmp(es[j].k) < 0 })
	return es
}
func omapKeys(m any) any {
	var lst any = &ctorV{idx: 0}
	es := omapSorted(m)
	for i := len(es) - 1; i >= 0; i-- {
		lst = &ctorV{idx: 1, fields: []any{es[i].k, lst}}
	}
	return lst
}
func omapValues(m any) any {
	var lst any = &ctorV{idx: 0}
	es := omapSorted(m)
	for i := len(es) - 1; i >= 0; i-- {
		lst = &ctorV{idx: 1, fields: []any{es[i].v, lst}}
	}
	return lst
}
func omapPairs(m any) any {
	var lst any = &ctorV{idx: 0}
	es := omapSorted(m)
	for i := len(es) - 1; i >= 0; i-- {
		pair := &ctorV{idx: 0, fields: []any{es[i].k, es[i].v}}
		lst = &ctorV{idx: 1, fields: []any{pair, lst}}
	}
	return lst
}
func omapMerge(a, b any) any {
	// left-biased: entries of a win on a key collision.
	n := make(omap)
	for k, e := range b.(omap) {
		n[k] = e
	}
	for k, e := range a.(omap) {
		n[k] = e
	}
	return any(n)
}
func omapFromList(l any) any {
	n := omap{}
	for c := l.(*ctorV); c.idx == 1; c = c.fields[1].(*ctorV) {
		p := c.fields[0].(*ctorV) // Pair k v
		ki := p.fields[0].(*big.Int)
		n[ki.String()] = omapEnt{ki, p.fields[1]}
	}
	return any(n)
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
			w, _ := capWiring(st, n, &capTy.Args[i])
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
	// Recognized Set operations lower to native oset helpers at their call
	// sites, so neither the operation nor its sorted-list helpers are emitted.
	if _, ok := e.setOps[h]; ok {
		e.seen[h] = true
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

// recognizeSetOp lowers a SATURATED call of a recognized Set operation to its
// native oset helper (composing recursively through its arguments). Returns
// ok=false for anything else, so normal compilation proceeds.
func (e *emitter) recognizeSetOp(t *Term, depth int, self string) (string, bool, error) {
	head, args := unwindApp(t)
	if head.K != "ref" {
		return "", false, nil
	}
	op, ok := e.setOps[head.Hash]
	if !ok || len(args) != op.arity {
		return "", false, nil
	}
	parts := make([]string, len(args))
	for i := range args {
		a, err := e.expr(args[i], depth, self)
		if err != nil {
			return "", false, err
		}
		parts[i] = a
	}
	return fmt.Sprintf("%s(%s)", op.helper, strings.Join(parts, ", ")), true, nil
}

// expr compiles a term to a Go expression. depth = number of binders in
// scope (env has that many entries, innermost last).
func (e *emitter) expr(t *Term, depth int, self string) (string, error) {
	// Native containers: a saturated Set operation lowers to its oset helper.
	if s, ok, err := e.recognizeSetOp(t, depth, self); err != nil {
		return "", err
	} else if ok {
		return s, nil
	}
	switch t.K {
	case "var":
		return fmt.Sprintf("env[%d]", depth-1-t.Idx), nil
	case "int":
		// Wrap literals as any(...) so the concrete-type assertions that
		// primitive operands carry (e.g. `.(int64)`, `.(string)`) apply — a
		// bare typed constant like int64(1) can't be type-asserted.
		return fmt.Sprintf("any(bi(%q))", t.Int.String()), nil
	case "rat":
		return fmt.Sprintf("any(ra(%q))", t.Rat.RatString()), nil
	case "float":
		// Emit exact IEEE bits (canonicalized) so the compiled constant is the
		// same value the interpreter and prover see — no decimal round-trip.
		return fmt.Sprintf("any(math.Float64frombits(0x%016x))", math.Float64bits(canonFloat(t.Float))), nil
	case "bool":
		return fmt.Sprintf("any(%s)", strconv.FormatBool(t.Bool)), nil
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
		// Str values are represented as native Go strings (data refinement):
		// SNil → "", SCons codepoint rest → the rune prepended to rest.
		if t.Hash == e.strHash && e.strHash != "" {
			if t.Idx == 0 { // SNil
				return `any("")`, nil
			}
			// SCons Int Str: fields[0] = codepoint, fields[1] = rest (a Go string)
			return fmt.Sprintf("any(string(rune(%s.(*big.Int).Int64())) + %s.(string))", parts[0], parts[1]), nil
		}
		// Set/Map values are native osets/omaps: the MkSet/MkMap constructor wraps
		// a list, so build the native map from it (keeping the representation
		// uniform even when constructed directly rather than via an operation).
		if t.Hash == e.setHash && e.setHash != "" {
			return fmt.Sprintf("osetFromList(%s)", parts[0]), nil
		}
		if t.Hash == e.mapHash && e.mapHash != "" {
			return fmt.Sprintf("omapFromList(%s)", parts[0]), nil
		}
		return fmt.Sprintf("(&ctorV{idx: %d, fields: []any{%s}})", t.Idx, strings.Join(parts, ", ")), nil
	case "match":
		s, err := e.expr(t.A, depth, self)
		if err != nil {
			return "", err
		}
		// Match on a Str: the scrutinee is a native Go string, not a ctorV.
		// arm 0 = SNil (empty), arm 1 = SCons (head codepoint, rest string).
		if t.Hash == e.strHash && e.strHash != "" {
			return e.matchStr(t, s, depth, self)
		}
		// Match on a Set: one constructor (MkSet), irrefutable. Its bound
		// variable is the (List Int) of sorted elements, materialized from the
		// native oset scrutinee.
		if t.Hash == e.setHash && e.setHash != "" {
			e.ctx = append(e.ctx, &Ty{K: "data"}) // placeholder for the bound List
			arm, err := e.expr(&t.Arms[0], depth+1, self)
			e.ctx = e.ctx[:len(e.ctx)-1]
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(func(env []any) any { return %s }(append(append([]any{}, env...), osetElems(%s))))", arm, s), nil
		}
		// Match on a Map: one constructor (MkMap), irrefutable; its bound var is
		// the key-sorted (List (Pair Int Int)) materialized from the omap.
		if t.Hash == e.mapHash && e.mapHash != "" {
			e.ctx = append(e.ctx, &Ty{K: "data"})
			arm, err := e.expr(&t.Arms[0], depth+1, self)
			e.ctx = e.ctx[:len(e.ctx)-1]
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(func(env []any) any { return %s }(append(append([]any{}, env...), omapPairs(%s))))", arm, s), nil
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

// matchStr compiles a `match` on a Str value, whose runtime representation is a
// native Go string. arm 0 is SNil (the empty string); arm 1 is SCons, binding
// the head codepoint (as int64) and the rest (a Go string) — the same field
// order (codepoint, rest) the structural ctorV would carry, so de Bruijn
// resolution is unchanged.
func (e *emitter) matchStr(t *Term, s string, depth int, self string) (string, error) {
	md, err := e.st.GetDef(t.Hash)
	if err != nil {
		return "", err
	}
	snil, err := e.expr(&t.Arms[0], depth, self)
	if err != nil {
		return "", err
	}
	fields := instCtorFields(md, t.Hash, nil, 1) // SCons fields: [Int, Str]
	e.ctx = append(e.ctx, fields...)
	scons, err := e.expr(&t.Arms[1], depth+len(fields), self)
	e.ctx = e.ctx[:len(e.ctx)-len(fields)]
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`(func(scrut string, env []any) any {
		if scrut == "" { return %s }
		r, sz := utf8.DecodeRuneInString(scrut)
		env = append(append([]any{}, env...), any(big.NewInt(int64(r))), any(scrut[sz:]))
		_ = env
		return %s
	})(%s.(string), env)`, snil, scons, s), nil
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

// isKind reports whether term t synthesizes to a type of kind k (used to
// type-direct numeric-overloaded and conversion primitive codegen).
func (e *emitter) isKind(t *Term, k string) bool {
	ty, err := e.chk.synth(e.ctx, t)
	return err == nil && ty != nil && ty.K == k
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
	// Arithmetic and comparison are numeric-overloaded: Int (*big.Int) or Rat
	// (*big.Rat). We type-direct off the first operand's synthesized sort so the
	// emitted assertions match the runtime representation. Rat is exact real
	// division (Quo, no truncation) and has no `%`; that is enforced upstream by
	// the type checker, so we only reach `%` on Int here.
	rat := false
	if len(t.Args) > 0 {
		if ty, err := e.chk.synth(e.ctx, &t.Args[0]); err == nil && ty.K == "rat" {
			rat = true
		}
	}
	if rat {
		ratOp := map[string]string{"+": "Add", "-": "Sub", "*": "Mul", "/": "Quo"}
		switch t.Op {
		case "+", "-", "*", "/":
			return fmt.Sprintf("any(new(big.Rat).%s(%s.(*big.Rat), %s.(*big.Rat)))", ratOp[t.Op], args[0], args[1]), nil
		case "neg":
			return fmt.Sprintf("any(new(big.Rat).Neg(%s.(*big.Rat)))", args[0]), nil
		case "<":
			return fmt.Sprintf("any(%s.(*big.Rat).Cmp(%s.(*big.Rat)) < 0)", args[0], args[1]), nil
		case "<=":
			return fmt.Sprintf("any(%s.(*big.Rat).Cmp(%s.(*big.Rat)) <= 0)", args[0], args[1]), nil
		case "==":
			return fmt.Sprintf("any(structEq(%s, %s))", args[0], args[1]), nil
		}
	}
	// Float: native float64, IEEE arithmetic (total; div-by-zero = ±inf), NaN
	// canonicalized. `==` is structural (bitwise), `fp-eq` is IEEE equality.
	isFloat := false
	if len(t.Args) > 0 {
		if ty, err := e.chk.synth(e.ctx, &t.Args[0]); err == nil && ty.K == "float" {
			isFloat = true
		}
	}
	if isFloat {
		fop := map[string]string{"+": "+", "-": "-", "*": "*", "/": "/"}
		switch t.Op {
		case "+", "-", "*", "/":
			return fmt.Sprintf("any(canonF(%s.(float64) %s %s.(float64)))", args[0], fop[t.Op], args[1]), nil
		case "neg":
			return fmt.Sprintf("any(canonF(-%s.(float64)))", args[0]), nil
		case "<":
			return fmt.Sprintf("any(%s.(float64) < %s.(float64))", args[0], args[1]), nil
		case "<=":
			return fmt.Sprintf("any(%s.(float64) <= %s.(float64))", args[0], args[1]), nil
		case "==":
			return fmt.Sprintf("any(structEq(%s, %s))", args[0], args[1]), nil
		case "fp-eq":
			return fmt.Sprintf("any(%s.(float64) == %s.(float64))", args[0], args[1]), nil
		}
	}
	// Numeric conversions — unary, dispatched on the source kind.
	switch t.Op {
	case "to-rat":
		if e.isKind(&t.Args[0], "int") {
			return fmt.Sprintf("any(i2r(%s.(*big.Int)))", args[0]), nil
		}
		return fmt.Sprintf("any(f2r(%s.(float64)))", args[0]), nil
	case "to-float":
		if e.isKind(&t.Args[0], "int") {
			return fmt.Sprintf("any(i2f(%s.(*big.Int)))", args[0]), nil
		}
		return fmt.Sprintf("any(r2f(%s.(*big.Rat)))", args[0]), nil
	case "floor":
		if e.isKind(&t.Args[0], "rat") {
			return fmt.Sprintf("any(rfloor(%s.(*big.Rat)))", args[0]), nil
		}
		return fmt.Sprintf("any(ffloor(%s.(float64)))", args[0]), nil
	}
	// Integers are arbitrary-precision (*big.Int); + - * / % never overflow.
	bigOp := map[string]string{"+": "Add", "-": "Sub", "*": "Mul", "/": "Quo", "%": "Rem"}
	cmp := func(op string) string {
		return fmt.Sprintf("any(%s.(*big.Int).Cmp(%s.(*big.Int)) %s 0)", args[0], args[1], op)
	}
	switch t.Op {
	case "+", "-", "*", "/", "%":
		// Quo/Rem truncate toward zero / take the dividend's sign (SPEC); both
		// panic on a zero divisor, matching eval's div/mod-by-zero error.
		return fmt.Sprintf("any(new(big.Int).%s(%s.(*big.Int), %s.(*big.Int)))", bigOp[t.Op], args[0], args[1]), nil
	case "neg":
		return fmt.Sprintf("any(new(big.Int).Neg(%s.(*big.Int)))", args[0]), nil
	case "<":
		return cmp("<"), nil
	case "<=":
		return cmp("<="), nil
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
	}
	return "", fmt.Errorf("cannot compile primitive %q", t.Op)
}

// sortedDepList is a helper for deterministic emission (unused fields kept
// minimal; ordering handled in closure()).
var _ = sort.Strings
