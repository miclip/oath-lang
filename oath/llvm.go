package main

// A SECOND BACKEND (#115, first slice): lower a verified program to textual LLVM
// IR and let clang produce the native executable.
//
// WHAT THIS IS FOR. #114 extracted a backend-neutral description of a compiled
// program — entry shape, required capability KINDS, dependency closure,
// provenance — and claimed a second backend could target it. A claim like that is
// worth exactly as much as the first attempt to use it, so this is the attempt.
// The test is not \"is this fast\" (it is not) but:
//
//	does a backend with a completely different value representation, calling
//	convention, provider table and startup path need anything from compile.go?
//
// The answer is enforced rather than asserted: boundary_test.go resolves every
// identifier in this file to its declaring file, so a single reference into the
// Go emitter fails the build. What this file consumes is CompiledProgram and the
// kernel's own AST — nothing else.
//
// WHAT IS DELIBERATELY NOT HERE. The typed IR, monomorphisation, layout and
// optimization that make #115 a real compiler project. Values here are boxed and
// uniform and every operation is a call into a C runtime, which is the same
// type-erased model the Go backend uses. That is on purpose: this slice exists to
// exercise the SEAM, and doing representation work at the same time would test two
// things at once so that a failure in either looked like a failure of both.
//
// NO DEPENDENCIES. IR is emitted as text and clang is invoked, exactly as the Go
// backend emits Go and invokes `go build`. Nothing links LLVM into the kernel.
//
// HONEST SUBSET. This backend refuses far more than it accepts — arithmetic, the
// numeric tower, native containers, dynamic Str construction, the handler
// protocol, and the http_request capability. Refusals are explicit and name what
// is missing. A backend that silently miscompiled what it did not understand
// would be worse than one that compiles almost nothing, because the differential
// gate is what makes any of this trustworthy.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// llvmBackendVersion identifies this lowering in the provenance manifest.
const llvmBackendVersion = "llvm-ir/1"

// ---------- the capability providers for THIS backend ----------

// llvmProvider is this backend's implementation of one capability kind: the name
// of a runtime function returning a capability value, or an error.
//
// Keyed by capabilityKind, exactly as the Go backend's table is. The two tables
// share nothing but their keys, which is the whole point — a kind is Oath's word
// for the authority, and how a host supplies it is a backend's business.
type llvmProvider struct {
	// Fn is a C runtime function with signature: OVal *fn(char **err).
	// It returns the capability value, or NULL with *err set to why THIS host
	// cannot supply it.
	Fn string
}

// llvmProviders is deliberately SMALLER than the Go backend's table, and that is
// a result rather than an omission: two backends may support different subsets of
// the same vocabulary, and the neutral layer does not have to care. A kind this
// backend cannot lower is a COMPILE failure naming the kind — the same refusal
// path #114 built, reached by a different backend.
//
// http_request is absent because a correct HTTP client (TLS included) is not
// something to hand-roll into a runtime for a first slice, and shelling out to a
// program that may not exist would make the capability's contract depend on the
// host's PATH.
var llvmProviders = map[capabilityKind]llvmProvider{
	capProcessEnv: {Fn: "o_cap_env"},
	capFileRead:   {Fn: "o_cap_readfile"},
	capRecordSink: {Fn: "o_cap_emit"},
}

func llvmProviderFor(r CapabilityRequirement) (llvmProvider, error) {
	p, ok := llvmProviders[r.Kind]
	if !ok {
		return llvmProvider{}, fmt.Errorf("capability %s (%s) has no implementation in the %s backend\n"+
			"  This backend supports: %s.\n"+
			"  The Go backend (`oath build` with no --backend) covers the full vocabulary.",
			r.Field, r.Kind, llvmBackendVersion, strings.Join(llvmKindNames(), ", "))
	}
	return p, nil
}

func llvmKindNames() []string {
	var out []string
	for k := range llvmProviders {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// ---------- the emitter ----------

type llvmEmitter struct {
	st      *Store
	b       strings.Builder // function bodies
	consts  strings.Builder // string constants
	fname   map[string]string
	order   []string
	seen    map[string]bool
	strHash string
	strNil  int      // SNil's constructor index in THIS store's Str
	strCons int      // SCons's index
	tmp     int      // SSA temporary counter
	lam     int      // hoisted lambda counter
	str     int      // string constant counter
	block   string   // the label of the block currently being emitted
	pending []string // hoisted lambda bodies, appended after the current function
	// Type tracking for record field resolution, threaded exactly as the kernel's
	// checker is elsewhere: a field projection needs the record's type to know
	// which slot a name refers to.
	chk *checker
	ctx []*Ty
}

func (e *llvmEmitter) next() string { e.tmp++; return fmt.Sprintf("%%t%d", e.tmp) }

// label starts a new basic block and records it. A phi names its predecessor
// BLOCK, and nested control flow moves that, so the current label has to be
// tracked rather than assumed to be the one an arm started in — getting it wrong
// yields IR clang rejects, or accepts with the wrong incoming value.
func (e *llvmEmitter) label(name string) {
	fmt.Fprintf(&e.b, "%s:\n", name)
	e.block = name
}

// strConst interns a NUL-terminated string constant and returns a pointer to it.
func (e *llvmEmitter) strConst(s string) string {
	e.str++
	name := fmt.Sprintf("@.s%d", e.str)
	b := []byte(s)
	var esc strings.Builder
	for _, c := range b {
		if c == '"' || c == '\\' || c < 0x20 || c >= 0x7f {
			fmt.Fprintf(&esc, "\\%02X", c)
		} else {
			esc.WriteByte(c)
		}
	}
	fmt.Fprintf(&e.consts, "%s = private unnamed_addr constant [%d x i8] c\"%s\\00\"\n",
		name, len(b)+1, esc.String())
	return name
}

// llvmUnsupported is the refusal every unlowerable construct routes through, so
// the message always says what was met and what the backend covers.
func llvmUnsupported(what string) error {
	return fmt.Errorf("the %s backend cannot lower %s\n"+
		"  This is a first slice: it covers datatypes, matching, closures, records,\n"+
		"  Str literals, Bool and the CLI entry protocol. Arithmetic, the numeric\n"+
		"  tower, Set/Map, dynamic Str construction and the handler protocol are not\n"+
		"  lowered yet. Build with the Go backend for full coverage.", llvmBackendVersion, what)
}

// strLiteral folds a Str constructor chain to a Go string.
//
// A Str literal in the AST is a nest of SCons constructors over Int codepoints,
// which the Go backend collapses at RUNTIME (string(rune(..)) + rest). Folding it
// at COMPILE time is what keeps integers out of this backend's runtime entirely:
// codepoints exist only inside literals, so a literal becomes a constant and
// nothing else needs to know what an Int is.
//
// Returns ok=false for a Str built from non-constant parts, which is refused
// rather than guessed at.
func (e *llvmEmitter) strLiteral(t *Term) (string, bool) {
	if t == nil || t.K != "ctor" || t.Hash != e.strHash || e.strHash == "" {
		return "", false
	}
	if t.Idx == e.strNil {
		return "", true
	}
	if t.Idx != e.strCons || len(t.Args) != 2 || t.Args[0].K != "int" || t.Args[0].Int == nil {
		return "", false
	}
	rest, ok := e.strLiteral(&t.Args[1])
	if !ok {
		return "", false
	}
	if !t.Args[0].Int.IsInt64() {
		return "", false
	}
	return string(rune(t.Args[0].Int.Int64())) + rest, true
}

// resolveStrCtors records SNil/SCons for this store's Str and checks their shape,
// since folding a literal assumes SCons carries (codepoint, rest).
func (e *llvmEmitter) resolveStrCtors() error {
	m, err := e.st.GetMeta(e.strHash)
	if err != nil {
		return err
	}
	d, err := e.st.GetDef(e.strHash)
	if err != nil {
		return err
	}
	e.strNil, e.strCons = -1, -1
	for i, n := range m.CtorNames {
		switch n {
		case "SNil":
			e.strNil = i
		case "SCons":
			e.strCons = i
		}
	}
	if e.strNil < 0 || e.strCons < 0 {
		return fmt.Errorf("Str does not declare SNil and SCons (found %v)", m.CtorNames)
	}
	if len(d.Ctors[e.strNil]) != 0 || len(d.Ctors[e.strCons]) != 2 {
		return fmt.Errorf("Str has an unexpected shape (SNil/%d, SCons/%d); this backend folds literals as SCons(codepoint, rest)",
			len(d.Ctors[e.strNil]), len(d.Ctors[e.strCons]))
	}
	return nil
}

// closure orders the entry's dependency closure, functions only, deps first —
// the same traversal every backend needs and none of them shares.
func (e *llvmEmitter) collect(h string) error {
	if e.seen[h] {
		return nil
	}
	e.seen[h] = true
	d, err := e.st.GetDef(h)
	if err != nil {
		return err
	}
	if d.K != "func" {
		return nil // datatypes are erased to constructor indices
	}
	for dep := range collectDepsBody(d) {
		if err := e.collect(dep); err != nil {
			return err
		}
	}
	e.fname[h] = "@f_" + smtName(e.st.NameOf(h)) + "_" + h[:8]
	e.order = append(e.order, h)
	return nil
}

// emitDef lowers one definition to an LLVM function of shape
// ptr @f(ptr %env, ptr %arg), mirroring the evaluator's env/arg discipline.
func (e *llvmEmitter) emitDef(h string) error {
	d, _ := e.st.GetDef(h)
	e.chk = &checker{st: e.st, selfTyVars: d.TyVars, selfTy: d.Ty}
	e.ctx = nil
	name := e.fname[h]
	fmt.Fprintf(&e.b, "; %s\ndefine ptr %s(ptr %%env, ptr %%arg) {\n", e.st.NameOf(h), name)
	e.label("entry")
	if d.Body.K == "lam" {
		// A def whose body is a lam is entered with its argument already bound:
		// env becomes exactly [arg], as the Go backend's `env = []any{arg}`.
		env := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_env1(ptr %%arg)\n", env)
		e.ctx = []*Ty{d.Body.Ty}
		v, err := e.expr(d.Body.A, env, 1, h)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.b, "  ret ptr %s\n}\n\n", v)
		return nil
	}
	v, err := e.expr(d.Body, "%env", 0, h)
	if err != nil {
		return err
	}
	fmt.Fprintf(&e.b, "  ret ptr %s\n}\n\n", v)
	return nil
}

// defValue produces a definition's VALUE: a closure when it is a function, and
// the evaluated body otherwise.
func (e *llvmEmitter) defValue(h string) (string, error) {
	d, err := e.st.GetDef(h)
	if err != nil {
		return "", err
	}
	fn, ok := e.fname[h]
	if !ok {
		return "", fmt.Errorf("unemitted reference %s", shortHash(h))
	}
	v := e.next()
	if d.Body.K == "lam" {
		fmt.Fprintf(&e.b, "  %s = call ptr @o_closure(ptr %s, ptr null)\n", v, fn)
		return v, nil
	}
	fmt.Fprintf(&e.b, "  %s = call ptr %s(ptr null, ptr null)\n", v, fn)
	return v, nil
}

// expr lowers one term, emitting instructions into the current block and
// returning the SSA value holding its result. env is the SSA value of the current
// environment; depth is its length, so a de Bruijn index resolves statically.
func (e *llvmEmitter) expr(t *Term, env string, depth int, self string) (string, error) {
	switch t.K {
	case "var":
		v := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_env_get(ptr %s, i32 %d)\n", v, env, depth-1-t.Idx)
		return v, nil

	case "bool":
		v := e.next()
		b := 0
		if t.Bool {
			b = 1
		}
		fmt.Fprintf(&e.b, "  %s = call ptr @o_bool(i32 %d)\n", v, b)
		return v, nil

	case "ctor":
		// A Str literal folds to a constant; anything else is a real constructor.
		if s, ok := e.strLiteral(t); ok {
			c := e.strConst(s)
			v := e.next()
			// Length is passed explicitly: a Str is a codepoint sequence and may
			// contain NUL, which strlen would silently truncate — and a backend
			// that truncated a value the interpreter preserves would break the
			// differential gate in the one direction nobody checks by eye.
			fmt.Fprintf(&e.b, "  %s = call ptr @o_strn(ptr %s, i32 %d)\n", v, c, len(s))
			return v, nil
		}
		if t.Hash == e.strHash && e.strHash != "" {
			return "", llvmUnsupported("a Str built from non-constant parts")
		}
		return e.build(t.Idx, t.Args, env, depth, self)

	case "record":
		// Records are constructors with index -1 and canonical field order — the
		// same representation the evaluator uses, which is what lets a capability
		// record be projected by slot.
		return e.build(-1, t.Args, env, depth, self)

	case "field":
		r, err := e.expr(t.A, env, depth, self)
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
				v := e.next()
				fmt.Fprintf(&e.b, "  %s = call ptr @o_field(ptr %s, i32 %d)\n", v, r, i)
				return v, nil
			}
		}
		return "", fmt.Errorf("record %s has no field %q", debugTy(rt), t.Op)

	case "app":
		f, err := e.expr(t.A, env, depth, self)
		if err != nil {
			return "", err
		}
		a, err := e.expr(t.B, env, depth, self)
		if err != nil {
			return "", err
		}
		v := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_apply(ptr %s, ptr %s)\n", v, f, a)
		return v, nil

	case "lam":
		return e.emitLam(t, env, depth, self)

	case "let":
		bound, err := e.expr(t.A, env, depth, self)
		if err != nil {
			return "", err
		}
		e2 := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_env_push(ptr %s, ptr %s)\n", e2, env, bound)
		e.ctx = append(e.ctx, t.Ty)
		v, err := e.expr(t.B, e2, depth+1, self)
		e.ctx = e.ctx[:len(e.ctx)-1]
		return v, err

	case "if":
		return e.emitIf(t, env, depth, self)

	case "match":
		return e.emitMatch(t, env, depth, self)

	case "ref":
		return e.defValue(t.Hash)

	case "self":
		return e.defValue(self)

	case "int", "rat", "float":
		return "", llvmUnsupported("numeric literals (Int, Rat, Float)")

	case "prim":
		return "", llvmUnsupported(fmt.Sprintf("the primitive operation %q", t.Op))
	}
	return "", llvmUnsupported(fmt.Sprintf("%q terms", t.K))
}

// build constructs a ctorV-shaped value: allocate a field array, fill it, wrap it.
func (e *llvmEmitter) build(idx int, args []Term, env string, depth int, self string) (string, error) {
	vals := make([]string, len(args))
	for i := range args {
		v, err := e.expr(&args[i], env, depth, self)
		if err != nil {
			return "", err
		}
		vals[i] = v
	}
	arr := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_fields(i32 %d)\n", arr, len(vals))
	for i, v := range vals {
		fmt.Fprintf(&e.b, "  call void @o_set(ptr %s, i32 %d, ptr %s)\n", arr, i, v)
	}
	v := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_ctor(i32 %d, i32 %d, ptr %s)\n", v, idx, len(vals), arr)
	return v, nil
}

// emitLam hoists a lambda to a top-level function and closes over the current
// environment. The hoisted body runs with env extended by its argument, which is
// how the evaluator binds a lam's parameter.
func (e *llvmEmitter) emitLam(t *Term, env string, depth int, self string) (string, error) {
	e.lam++
	name := fmt.Sprintf("@lam_%d", e.lam)

	// Emit the body into a separate buffer: LLVM functions cannot nest, and the
	// current one is mid-block.
	outer, outerBlock := e.b, e.block
	e.b = strings.Builder{}
	fmt.Fprintf(&e.b, "define ptr %s(ptr %%env, ptr %%arg) {\n", name)
	e.label("entry")
	e2 := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_env_push(ptr %%env, ptr %%arg)\n", e2)
	e.ctx = append(e.ctx, t.Ty)
	v, err := e.expr(t.A, e2, depth+1, self)
	e.ctx = e.ctx[:len(e.ctx)-1]
	if err != nil {
		e.b, e.block = outer, outerBlock
		return "", err
	}
	fmt.Fprintf(&e.b, "  ret ptr %s\n}\n\n", v)
	hoisted := e.b.String()
	e.b, e.block = outer, outerBlock

	// The hoisted function is appended after the current one completes; holding it
	// here would interleave two definitions.
	e.pending = append(e.pending, hoisted)

	c := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_closure(ptr %s, ptr %s)\n", c, name, env)
	return c, nil
}

func (e *llvmEmitter) emitIf(t *Term, env string, depth int, self string) (string, error) {
	c, err := e.expr(t.A, env, depth, self)
	if err != nil {
		return "", err
	}
	id := e.tmp
	lt, lf, lj := fmt.Sprintf("then%d", id), fmt.Sprintf("else%d", id), fmt.Sprintf("join%d", id)
	b := e.next()
	fmt.Fprintf(&e.b, "  %s = call i1 @o_truth(ptr %s)\n", b, c)
	fmt.Fprintf(&e.b, "  br i1 %s, label %%%s, label %%%s\n", b, lt, lf)
	e.label(lt)
	th, err := e.expr(t.B, env, depth, self)
	if err != nil {
		return "", err
	}
	thEnd := e.block
	fmt.Fprintf(&e.b, "  br label %%%s\n", lj)
	e.label(lf)
	el, err := e.expr(t.C, env, depth, self)
	if err != nil {
		return "", err
	}
	elEnd := e.block
	fmt.Fprintf(&e.b, "  br label %%%s\n", lj)
	e.label(lj)
	v := e.next()
	fmt.Fprintf(&e.b, "  %s = phi ptr [ %s, %%%s ], [ %s, %%%s ]\n", v, th, thEnd, el, elEnd)
	return v, nil
}

func (e *llvmEmitter) emitMatch(t *Term, env string, depth int, self string) (string, error) {
	if t.Hash == e.strHash && e.strHash != "" {
		return "", llvmUnsupported("match on Str")
	}
	d, err := e.st.GetDef(t.Hash)
	if err != nil {
		return "", err
	}
	if len(t.Arms) != len(d.Ctors) {
		return "", fmt.Errorf("match has %d arms for a %d-constructor type", len(t.Arms), len(d.Ctors))
	}
	s, err := e.expr(t.A, env, depth, self)
	if err != nil {
		return "", err
	}
	// The scrutinee's type arguments instantiate the constructor's field types.
	// Pushing the DECLARED types instead leaves type variables in the checker
	// context, so a field projection on a bound record fails to type — a valid
	// program refused, which is a different failure from an unsupported one and
	// must not be reported as the latter.
	scrutTy, tyErr := e.chk.synth(e.ctx, t.A)
	id := e.tmp
	idx := e.next()
	fmt.Fprintf(&e.b, "  %s = call i32 @o_idx(ptr %s)\n", idx, s)

	labels := make([]string, len(t.Arms))
	for i := range t.Arms {
		labels[i] = fmt.Sprintf("arm%d_%d", id, i)
	}
	join := fmt.Sprintf("mjoin%d", id)
	fmt.Fprintf(&e.b, "  switch i32 %s, label %%%s [\n", idx, labels[0])
	for i := range t.Arms {
		fmt.Fprintf(&e.b, "    i32 %d, label %%%s\n", i, labels[i])
	}
	fmt.Fprintf(&e.b, "  ]\n")

	results := make([]string, len(t.Arms))
	ends := make([]string, len(t.Arms))
	for i := range t.Arms {
		e.label(labels[i])
		armEnv := s
		cur := env
		fieldTys := make([]*Ty, len(d.Ctors[i]))
		for f := range d.Ctors[i] {
			fieldTys[f] = &d.Ctors[i][f]
		}
		if tyErr == nil && scrutTy != nil && scrutTy.K == "data" {
			fieldTys = instCtorFields(d, scrutTy.Hash, scrutTy.Args, i)
		}
		for f := range d.Ctors[i] {
			fv := e.next()
			fmt.Fprintf(&e.b, "  %s = call ptr @o_field(ptr %s, i32 %d)\n", fv, armEnv, f)
			ne := e.next()
			fmt.Fprintf(&e.b, "  %s = call ptr @o_env_push(ptr %s, ptr %s)\n", ne, cur, fv)
			cur = ne
			e.ctx = append(e.ctx, fieldTys[f])
		}
		v, err := e.expr(&t.Arms[i], cur, depth+len(d.Ctors[i]), self)
		e.ctx = e.ctx[:len(e.ctx)-len(d.Ctors[i])]
		if err != nil {
			return "", err
		}
		results[i] = v
		ends[i] = e.block
		fmt.Fprintf(&e.b, "  br label %%%s\n", join)
	}
	e.label(join)
	v := e.next()
	fmt.Fprintf(&e.b, "  %s = phi ptr", v)
	for i := range results {
		if i > 0 {
			fmt.Fprintf(&e.b, ",")
		}
		fmt.Fprintf(&e.b, " [ %s, %%%s ]", results[i], ends[i])
	}
	fmt.Fprintf(&e.b, "\n")
	return v, nil
}

// ---------- the C runtime ----------
//
// Written in C and compiled by clang alongside the emitted IR. That split is
// ordinary for an LLVM language and keeps the IR simple: every operation is a
// call, so the emitter never reasons about layout, and layout is #115's real
// subject rather than this slice's.
//
// The value model is type-erased and boxed, mirroring the Go backend's
// `any`/ctorV/closure. It is NOT the model a serious backend should end up with —
// monomorphisation and unboxing are the point of #115 — but matching the Go
// backend's semantics exactly is what lets the differential gate mean something
// here, and a new representation would have made a divergence ambiguous between
// "the boundary is wrong" and "my layout is wrong".
//
// MEMORY IS NEVER FREED. A CLI program runs once and exits, so an arena that is
// only ever reclaimed by process exit is honest for this slice — and stated,
// because it is exactly the reason the handler protocol is refused below: a
// long-running server would leak per request.
const llvmRuntimeC = `
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct OVal OVal;
typedef OVal *(*OCode)(OVal **env, OVal *arg);

enum { T_CTOR = 0, T_STR = 1, T_CLOS = 2, T_BOOL = 3 };

/* The capability protocol's CALL-failure value: a capability that was provided,
   was invoked, and could not complete. Provision failure is not a value — see
   o_require. */
#define CAP_FAIL ""

struct OVal {
  int tag;
  int idx;      /* constructor index; -1 for records */
  int n;        /* field count */
  const char *s;
  int slen;     /* byte length: a Str may contain NUL, so strlen is not enough */
  OVal **f;
  OCode code;
  OVal **env;
};

static void *xalloc(size_t n) {
  void *p = calloc(1, n);
  if (!p) { fputs("oath: out of memory\n", stderr); exit(70); }
  return p;
}
static OVal *val(int tag) { OVal *v = (OVal *)xalloc(sizeof(OVal)); v->tag = tag; v->idx = -1; return v; }

OVal *o_strn(const char *s, int n) {
  OVal *v = val(T_STR); v->s = s ? s : ""; v->slen = n; return v;
}
/* For genuine C strings — getenv results, literals internal to the runtime. */
OVal *o_str(const char *s) { return o_strn(s, s ? (int)strlen(s) : 0); }
OVal *o_bool(int b) { OVal *v = val(T_BOOL); v->idx = b ? 1 : 0; return v; }
int o_truth(OVal *v) { return v && v->tag == T_BOOL && v->idx != 0; }
int o_idx(OVal *v) { return v ? v->idx : -1; }
const char *o_cstr(OVal *v) { return (v && v->tag == T_STR && v->s) ? v->s : ""; }

/* A C API takes a NUL-TERMINATED string, and an Oath Str is a codepoint sequence
   that may contain NUL. Handing such a value to getenv or fopen would silently act
   on the PREFIX — opening a different file than the program named. The Go backend
   gets an error from the syscall layer and answers with the capability's failure
   value, so this answers the same way rather than acting on a truncation. */
static int o_has_nul(OVal *v) {
  if (!v || v->tag != T_STR) return 0;
  for (int i = 0; i < v->slen; i++) if (v->s[i] == 0) return 1;
  return 0;
}

OVal **o_fields(int n) { return (OVal **)xalloc(sizeof(OVal *) * (n > 0 ? n : 1)); }
void o_set(OVal **f, int i, OVal *v) { f[i] = v; }
OVal *o_ctor(int idx, int n, OVal **f) {
  OVal *v = val(T_CTOR); v->idx = idx; v->n = n; v->f = f; return v;
}
OVal *o_field(OVal *v, int i) {
  if (!v || v->tag != T_CTOR || i >= v->n) { fputs("oath: bad field access\n", stderr); exit(70); }
  return v->f[i];
}

/* Environments are immutable arrays; extending copies. The evaluator's env is a
   slice appended to per binder, and sharing a mutable one would let a closure
   observe bindings made after it was created. */
static int env_len(OVal **e) { return e ? (int)(intptr_t)e[0] : 0; }
OVal **o_env_push(OVal **e, OVal *v) {
  int n = env_len(e);
  OVal **out = (OVal **)xalloc(sizeof(OVal *) * (n + 2));
  out[0] = (OVal *)(intptr_t)(n + 1);
  for (int i = 0; i < n; i++) out[i + 1] = e[i + 1];
  out[n + 1] = v;
  return out;
}
OVal **o_env1(OVal *v) { return o_env_push(NULL, v); }
OVal *o_env_get(OVal **e, int i) {
  if (i < 0 || i >= env_len(e)) { fputs("oath: unbound variable\n", stderr); exit(70); }
  return e[i + 1];
}

OVal *o_closure(OCode code, OVal **env) {
  OVal *v = val(T_CLOS); v->code = code; v->env = env; return v;
}
OVal *o_apply(OVal *f, OVal *a) {
  if (!f || f->tag != T_CLOS) { fputs("oath: applied a non-function\n", stderr); exit(70); }
  return f->code(f->env, a);
}

/* argv becomes (List Str), built tail-first. The constructor indices are passed
   in rather than assumed: they are a fact about the List datatype in THIS store,
   and the compiler knows them. */
OVal *o_argv(int argc, char **argv, int nil_idx, int cons_idx) {
  OVal *acc = o_ctor(nil_idx, 0, o_fields(0));
  for (int i = argc - 1; i >= 1; i--) {
    OVal **f = o_fields(2);
    f[0] = o_str(argv[i]);
    f[1] = acc;
    acc = o_ctor(cons_idx, 2, f);
  }
  return acc;
}

void o_print(OVal *v) {
  if (v && v->tag == T_STR && v->slen > 0) fwrite(v->s, 1, (size_t)v->slen, stdout);
  fputc('\n', stdout);
}

/* Keeps the provenance blob in the binary: an artifact that cannot say what it
   was built from is not carrying provenance, and the linker drops data nothing
   references. Volatile so the store cannot be optimized away. */
static const char *volatile o_kept;
void o_keep(const char *p) { o_kept = p; }

/* ---------- capabilities ----------

   Each provider returns the capability value, or NULL with *err set to why THIS
   host cannot supply it. Provision failure is not an Oath value: o_require exits
   before any Oath code runs. A capability CALL that fails returns the empty
   string, which is the protocol's ordinary failure value. */

static OVal *cap_env_code(OVal **env, OVal *arg) {
  (void)env;
  if (o_has_nul(arg)) return o_str(CAP_FAIL);
  const char *v = getenv(o_cstr(arg));
  return o_str(v ? v : CAP_FAIL);
}
OVal *o_cap_env(char **err) { (void)err; return o_closure(cap_env_code, NULL); }

static OVal *cap_readfile_code(OVal **env, OVal *arg) {
  (void)env;
  if (o_has_nul(arg)) return o_str(CAP_FAIL);
  FILE *f = fopen(o_cstr(arg), "rb");
  if (!f) return o_str(CAP_FAIL);
  size_t cap = 4096, len = 0;
  char *buf = (char *)xalloc(cap);
  size_t got;
  while ((got = fread(buf + len, 1, cap - len - 1, f)) > 0) {
    len += got;
    if (len + 1 >= cap) { cap *= 2; char *nb = (char *)xalloc(cap); memcpy(nb, buf, len); buf = nb; }
  }
  /* A stream that returned bytes and then failed has not been read: the Go
     provider discards everything when os.ReadFile errors, and a partial answer
     that looks complete is worse than none. */
  int bad = ferror(f);
  fclose(f);
  if (bad) return o_str(CAP_FAIL);
  buf[len] = 0;
  return o_strn(buf, (int)len);
}
OVal *o_cap_readfile(char **err) { (void)err; return o_closure(cap_readfile_code, NULL); }

/* The sink FOLLOWS ITS PATH: the launch check opens and closes, each write
   reopens. Holding the descriptor would leave a rotated sink receiving records at
   the old inode forever while the program still reported success. */
static OVal *cap_emit_code(OVal **env, OVal *arg) {
  (void)env;
  const char *path = getenv("OATH_EMIT_PATH");
  /* Every write is checked, including the close: a buffered failure surfaces
     only at fclose, and reporting "ok" for a record that was lost is the one
     answer a sink must never give. The Go provider returns the failure value on
     a write error, so this does too. */
  if (!path || !*path) {
    size_t n = (arg && arg->slen > 0) ? (size_t)arg->slen : 0;
    if (n && fwrite(arg->s, 1, n, stdout) != n) return o_str(CAP_FAIL);
    if (fputc('\n', stdout) == EOF || fflush(stdout) != 0) return o_str(CAP_FAIL);
    return o_str("ok");
  }
  FILE *f = fopen(path, "a");
  if (!f) return o_str(CAP_FAIL);
  size_t n = (arg && arg->slen > 0) ? (size_t)arg->slen : 0;
  int bad = (n && fwrite(arg->s, 1, n, f) != n) || fputc('\n', f) == EOF;
  if (fclose(f) != 0) bad = 1;
  return o_str(bad ? CAP_FAIL : "ok");
}
OVal *o_cap_emit(char **err) {
  const char *path = getenv("OATH_EMIT_PATH");
  if (path && *path) {
    FILE *probe = fopen(path, "a");
    if (!probe) { *err = "sink cannot be opened for append"; return NULL; }
    fclose(probe);
  }
  return o_closure(cap_emit_code, NULL);
}

/* THE INVARIANT: every declared requirement is resolved exactly once before
   launch, or the executable does not start. 70 is EX_UNAVAILABLE, the same status
   the Go backend uses, so a supervisor reads both backends identically. */
OVal *o_require(OVal *(*provide)(char **), const char *field, const char *kind) {
  char *err = NULL;
  OVal *v = provide(&err);
  if (!v) {
    fprintf(stderr, "oath: this host cannot provide required capability %s (%s): %s\n",
            field, kind, err ? err : "unavailable");
    exit(70);
  }
  return v;
}
`

// ---------- program assembly ----------

// llvmDeclarations is the runtime's interface as LLVM sees it.
const llvmDeclarations = `
declare ptr @o_strn(ptr, i32)
declare ptr @o_bool(i32)
declare i1 @o_truth(ptr)
declare i32 @o_idx(ptr)
declare ptr @o_fields(i32)
declare void @o_set(ptr, i32, ptr)
declare ptr @o_ctor(i32, i32, ptr)
declare ptr @o_field(ptr, i32)
declare ptr @o_env_push(ptr, ptr)
declare ptr @o_env1(ptr)
declare ptr @o_env_get(ptr, i32)
declare ptr @o_closure(ptr, ptr)
declare ptr @o_apply(ptr, ptr)
declare ptr @o_argv(i32, ptr, i32, i32)
declare void @o_print(ptr)
declare void @o_keep(ptr)
declare ptr @o_require(ptr, ptr, ptr)
declare ptr @o_cap_env(ptr)
declare ptr @o_cap_readfile(ptr)
declare ptr @o_cap_emit(ptr)
`

// listCtorIndices reports the constructor indices of Nil and Cons for the List
// datatype the entry's argument uses.
//
// DERIVED rather than assumed. Constructor order is a fact about the datatype in
// THIS store; hardcoding 0 and 1 would be right today and silently wrong for a
// store whose List declares its constructors the other way round — and the
// failure would be a program that reverses its own arguments, which no type error
// catches.
func listCtorIndices(st *Store, prog *CompiledProgram) (nil_, cons int, err error) {
	d, err := st.GetDef(prog.EntryHash)
	if err != nil {
		return 0, 0, err
	}
	// Walk to the argv parameter: past the capability record when the entry takes
	// one. Resolving the NAME "List" instead would read the CURRENT binding, and a
	// store that has since rebound List would hand this program constructor
	// indices for a different datatype — argv silently reversed, or arms selected
	// wrongly, with no type error anywhere.
	ty := d.Ty
	if prog.CapTy != nil {
		ty = ty.B
	}
	if ty == nil || ty.A == nil || ty.A.K != "data" {
		return 0, 0, fmt.Errorf("entry does not take a datatype argument")
	}
	m, err := st.GetMeta(ty.A.Hash)
	if err != nil {
		return 0, 0, err
	}
	nil_, cons = -1, -1
	for i, n := range m.CtorNames {
		switch n {
		case "Nil":
			nil_ = i
		case "Cons":
			cons = i
		}
	}
	if nil_ < 0 || cons < 0 {
		return 0, 0, fmt.Errorf("the entry's argument type %s does not declare Nil and Cons (found %v)", m.Name, m.CtorNames)
	}
	// The runtime builds Nil with no fields and Cons with [head, tail]. That is an
	// assumption about LAYOUT, not just about naming, so it is checked: a datatype
	// that happens to be called List with a different shape would otherwise get
	// argv values that do not match the type the entry was verified against, and
	// nothing downstream would notice.
	ad, err := st.GetDef(ty.A.Hash)
	if err != nil {
		return 0, 0, err
	}
	if len(ad.Ctors[nil_]) != 0 || len(ad.Ctors[cons]) != 2 {
		return 0, 0, fmt.Errorf("%s has an unexpected shape (Nil/%d, Cons/%d); this backend builds argv as Nil and Cons(head, tail)",
			m.Name, len(ad.Ctors[nil_]), len(ad.Ctors[cons]))
	}
	// And the FIELD TYPES, not just their count: o_argv injects a Str head and a
	// tail of the same list type. A datatype shaped Cons Bool (List a) would take
	// a string where the verified program expects a Bool, and branch on it.
	fields := instCtorFields(ad, ty.A.Hash, ty.A.Args, cons)
	sh := strTypeHash(st)
	if len(fields) != 2 || !isStrTy(sh, fields[0]) ||
		fields[1] == nil || fields[1].K != "data" || fields[1].Hash != ty.A.Hash {
		return 0, 0, fmt.Errorf("%s's Cons is not (Str, %s); this backend builds argv as a list of Str", m.Name, m.Name)
	}
	return nil_, cons, nil
}

// emitLLVM lowers a compiled program to textual LLVM IR.
func emitLLVM(st *Store, prog *CompiledProgram) (string, error) {
	if prog.Protocol == entryHandler {
		return "", llvmUnsupported("the handler protocol\n" +
			"  A handler is long-running, and this runtime never frees memory — it would\n" +
			"  leak per request. That is a property of this slice's runtime, not of the\n" +
			"  protocol")
	}
	if err := prog.stampBackend(llvmBackendVersion); err != nil {
		return "", err
	}
	providers := make([]llvmProvider, len(prog.Requirements))
	for i, r := range prog.Requirements {
		p, err := llvmProviderFor(r)
		if err != nil {
			return "", err
		}
		providers[i] = p
	}
	nilIdx, consIdx, err := listCtorIndices(st, prog)
	if err != nil {
		return "", err
	}

	e := &llvmEmitter{st: st, fname: map[string]string{}, seen: map[string]bool{}, strHash: strTypeHash(st)}
	// Constructor indices are DERIVED, never assumed. A store whose Str declares
	// SCons first would otherwise have every non-empty literal folded to "" —
	// a silently wrong program, which is the one outcome this backend refuses to
	// risk.
	if e.strHash != "" {
		if err := e.resolveStrCtors(); err != nil {
			return "", err
		}
	}
	if err := e.collect(prog.EntryHash); err != nil {
		return "", err
	}
	var body strings.Builder
	for _, h := range e.order {
		e.b = strings.Builder{}
		e.pending = nil
		if err := e.emitDef(h); err != nil {
			return "", err
		}
		body.WriteString(e.b.String())
		for _, p := range e.pending {
			body.WriteString(p)
		}
	}

	// The capability boundary: authority enters exactly once, here.
	e.b = strings.Builder{}
	// Keyed on the RECORD, not the requirement count. An entry typed
	// (-> {} (-> (List Str) Str)) requires nothing and still TAKES a capability
	// argument: its arity comes from its type. The Go backend had this exact bug
	// and it is no less wrong here — argv would be passed where the record belongs
	// and the program would print a closure.
	var caps string
	if prog.CapTy != nil {
		e.label("entry")
		arr := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_fields(i32 %d)\n", arr, len(prog.Requirements))
		for i, r := range prog.Requirements {
			fn := e.strConst(r.Field)
			kn := e.strConst(string(r.Kind))
			v := e.next()
			fmt.Fprintf(&e.b, "  %s = call ptr @o_require(ptr @%s, ptr %s, ptr %s)\n",
				v, providers[i].Fn, fn, kn)
			fmt.Fprintf(&e.b, "  call void @o_set(ptr %s, i32 %d, ptr %s)\n", arr, i, v)
		}
		rec := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_ctor(i32 -1, i32 %d, ptr %s)\n", rec, len(prog.Requirements), arr)
		fmt.Fprintf(&e.b, "  ret ptr %s\n}\n\n", rec)
		caps = "define ptr @o_resolve_caps() {\n" + e.b.String()
		e.b = strings.Builder{}
	}

	// main.
	e.label("entry")
	args := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_argv(i32 %%argc, ptr %%argv, i32 %d, i32 %d)\n", args, nilIdx, consIdx)
	fmt.Fprintf(&e.b, "  call void @o_keep(ptr @oath_provenance)\n")
	entry := e.fname[prog.EntryHash]
	out := e.next()
	if prog.CapTy != nil {
		c := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_resolve_caps()\n", c)
		inner := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr %s(ptr null, ptr %s)\n", inner, entry, c)
		fmt.Fprintf(&e.b, "  %s = call ptr @o_apply(ptr %s, ptr %s)\n", out, inner, args)
	} else {
		fmt.Fprintf(&e.b, "  %s = call ptr %s(ptr null, ptr %s)\n", out, entry, args)
	}
	fmt.Fprintf(&e.b, "  call void @o_print(ptr %s)\n  ret i32 0\n}\n", out)
	mainFn := "define i32 @main(i32 %argc, ptr %argv) {\n" + e.b.String()

	// Provenance, carried as data. Same discipline as the Go backend: not a flag
	// and not an environment variable, because argv IS this program's input and
	// the environment belongs to any program holding `env`.
	blob := prog.Provenance.embeddedManifest()
	var esc strings.Builder
	for _, c := range []byte(blob) {
		if c == '"' || c == '\\' || c < 0x20 || c >= 0x7f {
			fmt.Fprintf(&esc, "\\%02X", c)
		} else {
			esc.WriteByte(c)
		}
	}

	var src strings.Builder
	src.WriteString("; Generated by oath build --backend llvm — do not edit.\n")
	src.WriteString("; Values are boxed and uniform; every operation is a runtime call.\n")
	src.WriteString(llvmDeclarations)
	fmt.Fprintf(&src, "\n@oath_provenance = constant [%d x i8] c\"%s\\00\"\n\n", len(blob)+1, esc.String())
	src.WriteString(e.consts.String())
	src.WriteString("\n")
	src.WriteString(body.String())
	src.WriteString(caps)
	src.WriteString(mainFn)
	return src.String(), nil
}

// llvmBuild compiles a program through this backend and writes the executable.
func llvmBuild(st *Store, prog *CompiledProgram, out string) error {
	if _, err := exec.LookPath("clang"); err != nil {
		return fmt.Errorf("clang is not on PATH; the %s backend emits textual IR and lets clang assemble it", llvmBackendVersion)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "oath-llvm-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "prog.ll"), []byte(ir), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		return err
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	cmd := exec.Command("clang", "-O1", "-o", abs, "rt.c", "prog.ll")
	cmd.Dir = tmp
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clang failed:\n%s\n--- IR ---\n%s", string(b), ir)
	}
	return nil
}
