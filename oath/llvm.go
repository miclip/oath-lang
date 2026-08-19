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
// HONEST SUBSET. This backend refuses far more than it accepts — the rest of the
// numeric tower, native containers, and the http_request capability. Refusals
// are explicit and name what is missing.
//
// EVERY ENTRY SHAPE IS NOW LOWERED, which is why that list names no shape at
// all: `(-> Request Response)` compiles, with a dependency-free HTTP/1.1 server
// and SPEC §14.2's transformation written into the emitted runtime, and
// `(-> {caps} (-> Request Response))` compiles onto a process-lifetime
// allocation region beside the request arena (#173). What is still refused is a
// capability KIND and a set of language constructs, never an entry protocol. A backend that
// silently miscompiled what it did not understand would be worse than one that
// compiles almost nothing, because the differential gate is what makes any of
// this trustworthy.

import (
	"fmt"
	"math/big"
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
	// A required value is not provided through a provider FUNCTION POINTER: it
	// needs to know WHICH field it is supplying, and the o_require signature
	// carries no argument for that. It gets its own entry point instead, so the
	// four authority providers keep their signature unchanged.
	if r.Kind == capRequiredValue {
		return llvmProvider{}, nil
	}
	p, ok := llvmProviders[r.Kind]
	if !ok {
		return llvmProvider{}, newCapabilityRefusal(llvmBackendVersion, r.Field, r.Kind, llvmKindNames())
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
	order   []string // emission order, from the neutral emissionOrder
	strHash string
	strNil  int      // SNil's constructor index in THIS store's Str
	strCons int      // SCons's index
	tmp     int      // SSA temporary counter
	lam     int      // hoisted lambda counter
	str     int      // string constant counter
	guard   int      // stack-guard block counter, unique per module
	block   string   // the label of the block currently being emitted
	pending []string // hoisted lambda bodies, appended after the current function
	// Type tracking for record field resolution, threaded exactly as the kernel's
	// checker is elsewhere: a field projection needs the record's type to know
	// which slot a name refers to.
	chk *checkerMachine
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

// openOathFunc emits the header of an emitted OATH function: the define line,
// the entry block, and the stack-guard prologue.
//
// IT EXISTS TO MAKE THE GUARD EXCLUSIVE, which is the whole reason the two call
// sites do not just each emit their own prologue. The claim is "every Oath
// function this backend emits checks the stack before descending", and its
// universe is Oath function bodies — so the guard belongs at the one place a
// body's header is written, not at a list of sites someone enumerated. A list is
// exactly the proxy population this repo keeps getting caught by: it is correct
// when written and silently incomplete when a third shape is added.
// llvm_stackguard_test.go holds it to that by compiling a program and requiring
// every emitted body to carry the check.
//
// @o_resolve_caps is deliberately NOT routed through here. It is runtime
// plumbing, emitted once, called once before the entry point, and it is not an
// Oath body — it cannot appear in a recursive cycle, so guarding it would widen
// the claim past what it says.
func (e *llvmEmitter) openOathFunc(header string) {
	e.b.WriteString(header)
	e.label("entry")
	// THE FRAME POINTER, NOT AN ALLOCA. Taking the address of a local reads the
	// stack just as well and MEASURABLY COSTS MORE: an alloca whose address
	// escapes through ptrtoint enlarged every frame enough to cut the corpus
	// application's ceiling from ~4,000 records to ~2,970 — a guard that made
	// exhaustion legible by making it arrive 26% sooner. llvm.frameaddress
	// reads a register the ABI already maintains.
	sp, spi, floor, low, ok, fail := e.next(), e.next(), e.next(), e.next(),
		fmt.Sprintf("sgok%d", e.guard), fmt.Sprintf("sgfail%d", e.guard)
	e.guard++
	fmt.Fprintf(&e.b, "  %s = call ptr @llvm.frameaddress.p0(i32 0)\n", sp)
	fmt.Fprintf(&e.b, "  %s = ptrtoint ptr %s to i64\n", spi, sp)
	fmt.Fprintf(&e.b, "  %s = load i64, ptr @o_stack_floor\n", floor)
	// UNSIGNED. Addresses are not signed quantities, and `slt` would invert the
	// comparison for any stack mapped above 0x7fff_ffff_ffff_ffff — the guard
	// would then fire on every call instead of none, which is at least loud,
	// but it would be wrong on a host nobody here tests.
	fmt.Fprintf(&e.b, "  %s = icmp ult i64 %s, %s\n", low, spi, floor)
	fmt.Fprintf(&e.b, "  br i1 %s, label %%%s, label %%%s\n", low, fail, ok)
	e.label(fail)
	fmt.Fprintf(&e.b, "  call void @o_stack_exhausted()\n  unreachable\n")
	e.label(ok)
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
// llvmValueEnvVar is THIS backend's binding from a capability field to a host
// source. It is deliberately a separate function from the Go backend's
// valueEnvVar: the two agree today, and a backend that sourced values from a
// secret manager instead would change only its own. boundary_test.go would
// fail if this called the Go emitter's version.
func llvmValueEnvVar(field string) string {
	out := make([]byte, 0, len("OATH_VALUE_")+len(field))
	out = append(out, "OATH_VALUE_"...)
	for i := 0; i < len(field); i++ {
		c := field[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c-32)
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// llvmCheckValueBindings is the LLVM backend's own collision check. Same
// reasoning as the Go backend's, computed from its own mapping: a lossy
// field-to-variable mapping cannot tell `api-key` from `api_key`, and two
// requirements reading one variable is unconfigurable. Duplicated rather than
// shared because the two backends may bind values differently — boundary_test
// enforces that neither reaches into the other.
func llvmCheckValueBindings(prog *CompiledProgram) error {
	seen := map[string]string{}
	for _, r := range prog.Requirements {
		if r.Kind != capRequiredValue {
			continue
		}
		env := llvmValueEnvVar(r.Field)
		if prev, dup := seen[env]; dup {
			return &backendRefusal{
				Reason:  reasonValueBinding,
				Backend: llvmBackendVersion,
				Detail: fmt.Sprintf("required values %q and %q, which both bind to %s",
					prev, r.Field, env),
				Help: "  This backend sources a required value from an environment variable named after\n" +
					"  the field, and that mapping cannot tell these two apart — they would read one\n" +
					"  variable and receive the same value. Rename one of them.",
			}
		}
		seen[env] = r.Field
	}
	return nil
}

// llvmUnsupported builds a TYPED refusal (#134). The reason is the contract; the
// help paragraph below is presentation and may be rewritten freely.
//
// That paragraph is exactly why the separation exists: it LISTS unsupported
// features, so a test matching `strings.Contains(err, "match on Str")` stayed
// satisfied after match on Str was implemented and skipped forever while
// looking deliberate.
func llvmUnsupported(reason refusalReason, what string) error {
	return &backendRefusal{
		Reason:  reason,
		Backend: llvmBackendVersion,
		Detail:  what,
		Help: "  This is a first slice: it covers datatypes, matching, closures, records,\n" +
			"  Str (literals, matching, construction from values computed at runtime,\n" +
			"  and `==`), the #78 crypto boundary over byte lists (`hmac-sha256` and\n" +
			"  `bytes-eq-ct`, written out in the emitted runtime rather than linked from\n" +
			"  a host library), Bool (`and`, `or`, `not` — STRICT, so both operands of a\n" +
			"  binary operator are evaluated even when the first decides the result,\n" +
			"  matching `oath eval` — and `==`), the CLI entry protocol, and Int —\n" +
			"  arbitrary-precision, literals of any magnitude, with the binary\n" +
			"  operations `+ - * / %` and `== < <=` and unary `neg`. Division\n" +
			"  truncates toward zero and a zero divisor fails at runtime, matching\n" +
			"  `oath eval`. `==` at any type OTHER than Int, Str and Bool is refused.\n" +
			"  Every entry shape is covered — the CLI ones, and both handlers,\n" +
			"  (-> Request Response) and (-> {caps} (-> Request Response)), served\n" +
			"  over HTTP/1.1 by the emitted runtime.\n" +
			"  Rat and Float and Set/Map are refused, so `neg` at Rat or Float is\n" +
			"  refused with them. Build with the Go backend for full coverage.",
	}
}

// strLiteral folds a Str constructor chain to a Go string.
//
// A Str literal in the AST is a nest of SCons constructors over Int codepoints,
// which the Go backend collapses at RUNTIME (string(rune(..)) + rest). Folding it
// at COMPILE time is what keeps integers out of this backend's runtime entirely:
// codepoints exist only inside literals, so a literal becomes a constant and
// nothing else needs to know what an Int is.
//
// Returns ok=false for a Str built from non-constant parts. That is no longer a
// refusal (#164): the caller lowers such a chain through o_str_cons and PACK
// happens at run time. So this predicate now decides WHERE the codepoint check
// runs, not WHETHER the program builds — and a false negative here costs an
// allocation, where it used to cost a build.
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
	// A BACKEND SUBSET BOUNDARY, not a claim that the value is illegal Oath —
	// #133 asks that question and is open. This backend packs Str as UTF-8,
	// which encodes exactly the Unicode scalar values; outside them there is no
	// injective encoding here, and the previous `string(rune(n))` silently
	// produced U+FFFD, so three distinct Str values folded to identical bytes.
	if !isUnicodeScalar(t.Args[0].Int.Int64()) {
		return "", false
	}
	return string(rune(t.Args[0].Int.Int64())) + rest, true
}

// isUnicodeScalar reports whether n is encodable as UTF-8: 0..0x10FFFF, minus
// the surrogate range, which UTF-8 does not encode.
func isUnicodeScalar(n int64) bool {
	return n >= 0 && n <= 0x10FFFF && !(n >= 0xD800 && n <= 0xDFFF)
}

// nonScalarStrElement returns the first LITERAL Str element this backend cannot
// encode, so the refusal can NAME it instead of reporting the generic
// non-constant reason — two different repairs, and an operator should not have
// to guess.
//
// It scans only the literal elements of the spine, which is what makes it a
// COMPILE-TIME check: an element whose value is not known until the program runs
// is checked by o_str_cons in the emitted runtime instead. A chain may carry
// both, and then the statically-known bad element is the one reported, because
// reporting it costs nothing and a build that fails is cheaper than a process
// that exits 70.
//
// A magnitude beyond int64 is non-scalar with certainty — 0..0x10FFFF fits in a
// few bits — so it is REPORTED rather than skipped. Returning the big.Int rather
// than an int64 is what makes that expressible: the previous signature could not
// name a value it could not hold, so `(SCons 99999999999999999999 (SNil))`
// reached the generic non-constant refusal and blamed the wrong thing.
func nonScalarStrElement(t *Term, strHash string) (*big.Int, bool) {
	for t != nil && t.K == "ctor" && t.Hash == strHash && len(t.Args) == 2 {
		if t.Args[0].K == "int" && t.Args[0].Int != nil {
			n := t.Args[0].Int
			if !n.IsInt64() || !isUnicodeScalar(n.Int64()) {
				return n, true
			}
		}
		t = &t.Args[1]
	}
	return nil, false
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

// plan fixes what this backend emits and in what order, by asking the neutral
// layer for the entry's dependency-first order.
//
// This backend lowers no definition natively — it has no Set/Map recognition —
// so it prunes nothing and emits the whole function closure. The traversal
// itself lives in program.go: it was duplicated here once, and the copy ordered
// siblings by map iteration (#168).
func (e *llvmEmitter) plan(entry string) error {
	order, err := emissionOrder(e.st, entry, nil)
	if err != nil {
		return err
	}
	e.order = order
	for _, h := range order {
		e.fname[h] = "@f_" + smtName(e.st.NameOf(h)) + "_" + h[:8]
	}
	return nil
}

// emitDef lowers one definition to an LLVM function of shape
// ptr @f(ptr %env, ptr %arg), mirroring the evaluator's env/arg discipline.
func (e *llvmEmitter) emitDef(h string) error {
	d, _ := e.st.GetDef(h)
	e.chk = &checkerMachine{st: e.st, selfTyVars: d.TyVars, selfTy: d.Ty}
	e.ctx = nil
	name := e.fname[h]
	e.openOathFunc(fmt.Sprintf("; %s\ndefine ptr %s(ptr %%env, ptr %%arg) {\n", e.st.NameOf(h), name))
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
			if n, bad := nonScalarStrElement(t, e.strHash); bad {
				return "", llvmUnsupported(reasonStrElementRange, fmt.Sprintf(
					"the Str element %s — this backend packs Str as UTF-8, which encodes only "+
						"Unicode scalar values (0..0x10FFFF, excluding surrogates 0xD800..0xDFFF). "+
						"Refusing rather than substituting U+FFFD, which would make distinct Str "+
						"values identical", n))
			}
			// A Str WHOSE PARTS ARE NOT CONSTANTS IS BUILT AT RUNTIME.
			//
			// What this retires is `reasonDynamicStr`, and the retirement is the
			// point: the old refusal made every result of a compiled program a
			// literal or a SUFFIX of an input, so a program could search and echo
			// but never report — no `"missing key: " ++ k`, no counts (#164).
			//
			// PACK MOVES TO RUNTIME, IT DOES NOT DISAPPEAR. SPEC §3 says a kernel
			// MUST NOT reject a non-scalar element at construction, and says in the
			// same breath that a backend storing a Str as packed UTF-8 performs PACK
			// at the moment of construction, so refusing there is the PACK
			// obligation discharged early rather than a second semantics. The
			// literal case above discharges it at compile time; o_str_cons
			// discharges the rest at run time. What neither may do is substitute.
			//
			// So `oath eval` remains the reference and is ALLOWED to disagree here:
			// it constructs `(SCons -1 (SNil))` happily, and this backend refuses to
			// carry it. A refusal is never evidence that a value is illegal Oath.
			// NOT a refusal: resolveStrCtors already established that this store's
			// Str is SNil/0 and SCons/2, and SNil folded above, so reaching here
			// means the datatype is not the one that was resolved. A
			// backendRefusal would be the wrong classification — it says "valid
			// Oath, outside this backend's subset" — and labelling this
			// `dynamic-str` would leave a caller matching that reason receiving a
			// broken-invariant report instead of the subset boundary it asked
			// about.
			if t.Idx != e.strCons || len(t.Args) != 2 {
				return "", fmt.Errorf("Str constructor %d has %d arguments; this store's Str resolved to SNil/0 and SCons/2",
					t.Idx, len(t.Args))
			}
			// LEFT-TO-RIGHT, as SPEC §3 requires of every ctor: the head's
			// instructions are emitted before the tail's, so a failure in the head
			// happens first. Emission order IS evaluation order here, since neither
			// operand is a block.
			head, err := e.expr(&t.Args[0], env, depth, self)
			if err != nil {
				return "", err
			}
			rest, err := e.expr(&t.Args[1], env, depth, self)
			if err != nil {
				return "", err
			}
			v := e.next()
			fmt.Fprintf(&e.b, "  %s = call ptr @o_str_cons(ptr %s, ptr %s)\n", v, head, rest)
			return v, nil
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

	case "int":
		// THE LITERAL IS EMITTED AS DECIMAL TEXT, NOT AS A MACHINE WORD.
		//
		// This is what retires the old `int-range` refusal. The runtime stored a
		// long long and the compiler refused any literal outside it, which was
		// honest — it was a SUBSET that said so — and #166's own falsifier then
		// showed the subset is observably distinguishable from ℤ by a program
		// the backend accepts. So the representation moved rather than the
		// refusal, and there is now no Int literal this backend cannot carry.
		//
		// Text rather than limbs, deliberately: the digits are the canonical
		// form the AST already holds, so the emitter needs no encoding of its
		// own, and nothing has to agree with the runtime about limb order or
		// width. `int-missing-value` stays — a term with no value at all is a
		// malformed AST, not a magnitude.
		if t.Int == nil {
			return "", llvmUnsupported(reasonIntMissing, "an Int literal with no value")
		}
		v := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_int_dec(ptr %s)\n", v, e.strConst(t.Int.String()))
		return v, nil

	case "rat", "float":
		return "", llvmUnsupported(reasonRatFloat, "Rat and Float literals")

	case "prim":
		// THE Int PRIMITIVES, AND THE BOUNDARY IS `/`.
		//
		// The table is the whole vocabulary this backend lowers, and it is a
		// table rather than a chain of ifs because a reader asking "is X
		// supported" should find the answer in one place. Two shapes, and the
		// return type is what separates them: an ARITHMETIC op returns an Int,
		// an ORDERING op returns an i32 that o_bool lifts to a Bool.
		//
		// `neg` IS NOW LOWERED, and it is unary, so it lives BELOW this table
		// rather than in it — the second occupant of the third shape `not` opened.
		//
		// WHAT MOVED IS DEMAND, NOT EXPRESSIBILITY, and saying which is the whole
		// justification. `(- 0 x)` still reaches the same value through the binary
		// table above, so this adds nothing a program could not already say; the
		// comment that used to sit here declined a third shape on exactly that
		// ground, and it was right to. What overturns it is a CONCRETE CORPUS
		// PROGRAM: `show-int` in examples/circle.oath spells the sign flip
		// `(neg n)`, and circle.oath is the tutorial's worked compiled example.
		// Refusing the corpus's own spelling of an operation this backend already
		// implements makes the user rewrite a definition to suit the compiler,
		// which is the wrong way round — `(- 0 x)` being equivalent is what makes
		// the lowering CHEAP (one runtime function over the existing normalizer),
		// not what makes the demand illegitimate.
		//
		// So the boundary did not move because a better argument was found. It
		// moved because something asked, which is the same reason `not` is here.
		//
		// `and`, `or` AND `not` ARE LOWERED, AND THE OBLIGATION THAT CAME WITH
		// THEM WAS SEMANTIC RATHER THAN A CHOICE: THEY DO NOT SHORT-CIRCUIT.
		// Measured against the reference, `(and false (< (/ 1 0) 1))` and
		// `(or true (< (/ 1 0) 1))` both RAISE — the second operand is evaluated
		// even when the first has already decided the result. A lowering that
		// branched around the second operand would fail to raise where `oath
		// eval` raises, and would pass every test whose second operand happens
		// to be total, which is most of them.
		//
		// SO THE LOWERING BELOW EMITS NO BRANCH AT ALL. Both operands are
		// emitted unconditionally and in source order, exactly as the Int
		// operations above emit theirs, and only then are the two i1 truths
		// combined by a single `and`/`or` instruction. That is not merely one
		// correct way to do it — it is the shape that makes short-circuiting
		// UNREPRESENTABLE here, because there is no control flow to skip
		// anything with. A `br`-based lowering is the thing to refuse in review;
		// llvm_bool_test.go is where it fails.
		intArith := map[string]string{
			"+": "o_int_add", "-": "o_int_sub", "*": "o_int_mul",
			"/": "o_int_div", "%": "o_int_mod",
		}
		intOrder := map[string]string{"==": "o_int_eq", "<": "o_int_lt", "<=": "o_int_le"}
		arith, isArith := intArith[t.Op]
		order, isOrder := intOrder[t.Op]
		if len(t.Args) == 2 {
			// THE TYPE GUARD IS LOAD-BEARING, not a formality. `+ - * < <=` are
			// numeric-OVERLOADED in the language — the same op spells Rat and
			// Float arithmetic — and `==` is polymorphic over everything. So the
			// operand types decide whether this is the Int lowering at all, and
			// a Rat addition must fall through to the refusal below rather than
			// be handed to an integer runtime.
			//
			// The synthesis is hoisted out of the Int branch because `==` now has
			// THREE lowerings selected by operand type, and asking the checker
			// once per case would let the guards drift apart while each stayed
			// readable.
			at, aerr := e.chk.synth(e.ctx, &t.Args[0])
			bt, berr := e.chk.synth(e.ctx, &t.Args[1])
			typed := aerr == nil && berr == nil && at != nil && bt != nil
			if typed && at.K == "int" && bt.K == "int" && (isArith || isOrder) {
				a, err := e.expr(&t.Args[0], env, depth, self)
				if err != nil {
					return "", err
				}
				b, err := e.expr(&t.Args[1], env, depth, self)
				if err != nil {
					return "", err
				}
				if isArith {
					v := e.next()
					fmt.Fprintf(&e.b, "  %s = call ptr @%s(ptr %s, ptr %s)\n", v, arith, a, b)
					return v, nil
				}
				r := e.next()
				fmt.Fprintf(&e.b, "  %s = call i32 @%s(ptr %s, ptr %s)\n", r, order, a, b)
				v := e.next()
				fmt.Fprintf(&e.b, "  %s = call ptr @o_bool(i32 %s)\n", v, r)
				return v, nil
			}
			// `==` AT Str.
			//
			// A SECOND INSTANCE OF ONE POLYMORPHIC OPERATION, not a second
			// operation: `==` is structural equality at every type, and Str is a
			// datatype whose values are codepoint sequences. So the obligation is
			// the interpreter's `structEq` over the SCons/SNil spine, and the only
			// question a backend answers is how to compute it over ITS
			// representation.
			//
			// Both operands are required to be Str rather than one of them, even
			// though the checker already makes `==` homogeneous: the guard's job is
			// to select a lowering, and a guard that trusts a neighbouring
			// component's invariant selects on something it cannot see.
			//
			// The narrowness is deliberate and it is not a claim that `==` is hard
			// elsewhere. A ctor-chain equality is a recursive walk over o_idx and
			// o_field and would be a fine next slice; it is refused today because
			// what is lowered here is what a program actually demanded, and a
			// refusal that names the operation is how the next demand arrives.
			//
			// AND WHEN THAT SLICE COMES, THE QUESTION IT TURNS ON IS ALREADY
			// ANSWERED, so it is written down rather than re-derived. A single
			// structural walk over the runtime representation matches the
			// reference at every type IF AND ONLY IF every representation is
			// CANONICAL — otherwise two equal values differ bytewise and the walk
			// answers a silently wrong `false` instead of refusing. Measured
			// against `oath eval`, all three numeric representations qualify:
			//
			//	Int    sign-magnitude with ONE normalizer (o_int_wrap): no leading
			//	       zero limb, isign zero iff the value is, so bytes are unique
			//	Rat    reduced with the sign in the numerator, so structural
			//	       equality IS numeric equality
			//	Float  binary64 with a canonical NaN, and bit comparison is
			//	       REQUIRED rather than hazardous — docs/floats.md makes `==`
			//	       SMT `=`, NOT fp.eq, so `+0.0 == -0.0` is FALSE and a walk
			//	       that "helpfully" used IEEE equality would be the wrong one
			//
			// Str is the type that would NOT fall out of such a walk, because its
			// runtime form is not its structural form: the walk would see packed
			// bytes where the language sees a SCons spine. So this case survives a
			// generalisation rather than being replaced by it.
			if typed && t.Op == "==" && e.isStrTy(at) && e.isStrTy(bt) {
				a, err := e.expr(&t.Args[0], env, depth, self)
				if err != nil {
					return "", err
				}
				b, err := e.expr(&t.Args[1], env, depth, self)
				if err != nil {
					return "", err
				}
				r := e.next()
				fmt.Fprintf(&e.b, "  %s = call i32 @o_str_eq(ptr %s, ptr %s)\n", r, a, b)
				v := e.next()
				fmt.Fprintf(&e.b, "  %s = call ptr @o_bool(i32 %s)\n", v, r)
				return v, nil
			}
			// `==` AT Bool — THE THIRD INSTANCE OF THE SAME POLYMORPHIC OPERATION.
			//
			// A Bool carries no payload, so structural equality at this type is
			// equality of the two truth values and there is nothing to walk. It is
			// therefore the one instance that needs no runtime support at all:
			// o_truth is already the extractor `if` and `and`/`or` use, and `icmp
			// eq i1` is total on i1, so there is no bit pattern where this can
			// disagree with the reference's structEq.
			//
			// IT IS NOT LOWERED AS `xor`+`not` OR AS AN Int COMPARISON ON THE TAG.
			// Both would work today and both would be reading a REPRESENTATION —
			// the second especially, since it would depend on the runtime storing
			// a Bool's truth in the same field an Int uses. Comparing the two
			// truths is the lowering that stays correct if that ever changes.
			//
			// STRICT, AND FOR THE SAME STRUCTURAL REASON `and` AND `or` ARE: both
			// operands are emitted before either truth is read, and there is no
			// `br` for a short-circuit to hide behind. `==` has no short-circuiting
			// reading anyone would reach for, but the shape is the one that makes
			// that unrepresentable rather than merely unattempted.
			if typed && t.Op == "==" && at.K == "bool" && bt.K == "bool" {
				a, err := e.expr(&t.Args[0], env, depth, self)
				if err != nil {
					return "", err
				}
				b, err := e.expr(&t.Args[1], env, depth, self)
				if err != nil {
					return "", err
				}
				// combineBool takes two ALREADY-EMITTED operands, which is exactly
				// the invariant this case needs, and `icmp eq` is an operator over
				// two i1 registers like `and` and `or` are. Reusing it is what keeps
				// one place responsible for "both operands have run by now".
				return e.combineBool("icmp eq", a, b), nil
			}
			// THE CRYPTO PRIMITIVES (#78) — `hmac-sha256` AND `bytes-eq-ct`.
			//
			// These are the only primitives whose arguments are an ADT rather
			// than a scalar (SPEC §1), so they are the only ones whose lowering
			// needs to know a DATATYPE's shape: the operands are walked as a cons
			// list and the digest is built back into one.
			//
			// THE INDICES COME FROM THE TYPE IN THE TERM, NOT FROM THE NAME
			// "List". Resolving the name would read the store's CURRENT binding,
			// and a store that had since rebound List would hand this program
			// constructor indices for a different datatype — a digest built with
			// the wrong tag, matched against arms that do not fit it, with no type
			// error anywhere. byteListCtors reads the declaration the checker
			// already typed these arguments against.
			//
			// A shape it cannot recognise is an ERROR and not a backend refusal.
			// A refusal says "valid Oath, outside this backend's subset", and the
			// checker has already established that these operands are (List Int);
			// what is left is a datatype whose declaration does not match its own
			// type, which is a broken invariant rather than a subset boundary.
			if t.Op == "hmac-sha256" || t.Op == "bytes-eq-ct" {
				if typed && at.K == "data" && bt.K == "data" && at.Hash == bt.Hash {
					nilIdx, consIdx, cerr := e.byteListCtors(at)
					if cerr != nil {
						return "", cerr
					}
					// LEFT TO RIGHT, and it is observable rather than cosmetic:
					// an out-of-range element in EITHER operand is a runtime
					// error, the reference converts argument 0 in full before
					// argument 1, and emission order is evaluation order here.
					a, err := e.expr(&t.Args[0], env, depth, self)
					if err != nil {
						return "", err
					}
					b, err := e.expr(&t.Args[1], env, depth, self)
					if err != nil {
						return "", err
					}
					if t.Op == "bytes-eq-ct" {
						r := e.next()
						fmt.Fprintf(&e.b, "  %s = call i32 @o_bytes_eq_ct(ptr %s, ptr %s, i32 %d)\n", r, a, b, consIdx)
						v := e.next()
						fmt.Fprintf(&e.b, "  %s = call ptr @o_bool(i32 %s)\n", v, r)
						return v, nil
					}
					v := e.next()
					fmt.Fprintf(&e.b, "  %s = call ptr @o_hmac_sha256(ptr %s, ptr %s, i32 %d, i32 %d)\n", v, a, b, nilIdx, consIdx)
					return v, nil
				}
			}
			// `and` AND `or` AT Bool — STRICT, AND STRUCTURALLY SO.
			//
			// The two operands are emitted before either truth is read, which is
			// the whole semantic content of this case: `e.expr` writes the
			// operand's instructions into the current block, so by the time the
			// combining instruction is reached both operands have already run.
			// There is no `br`, no second basic block and no phi, so there is
			// nothing a short-circuit could skip.
			//
			// The type guard is the same one the Int and Str cases use, and for
			// the same reason: the checker's `allBool` already makes these
			// operators Bool-only, but a guard that rests on a neighbouring
			// component's invariant is selecting on something it cannot see. An
			// operand that does not synthesise falls through to the refusal.
			if boolOp, ok := map[string]string{"and": "and", "or": "or"}[t.Op]; ok &&
				typed && at.K == "bool" && bt.K == "bool" {
				a, err := e.expr(&t.Args[0], env, depth, self)
				if err != nil {
					return "", err
				}
				b, err := e.expr(&t.Args[1], env, depth, self)
				if err != nil {
					return "", err
				}
				return e.combineBool(boolOp, a, b), nil
			}
		}
		// THE UNARY SHAPE — `not` AT Bool AND `neg` AT Int. Outside the arity-2
		// block above rather than inside it, because neither returns a value of
		// the shape that block's two tables describe.
		//
		// BOTH ARE HERE BECAUSE SOMETHING ASKED, WHICH IS THE WHOLE REASON, AND
		// NEITHER IS HERE BECAUSE IT HAD NO OTHER SPELLING. `(not x)` is `(if x
		// false true)` and `(neg n)` is `(- 0 n)`; both reach the same value
		// through constructs this backend already lowers. Claiming otherwise
		// would put a statement about the LANGUAGE where a fact about this
		// backend's scope belongs, and the two stop agreeing the moment someone
		// checks. What is true is narrower and enough: a compiled program in this
		// repo's own corpus uses each spelling, so refusing either makes the
		// backend reject Oath that the rest of the toolchain accepts.
		if t.Op == "not" && len(t.Args) == 1 {
			if at, err := e.chk.synth(e.ctx, &t.Args[0]); err == nil && at != nil && at.K == "bool" {
				a, err := e.expr(&t.Args[0], env, depth, self)
				if err != nil {
					return "", err
				}
				// XOR WITH true RATHER THAN A COMPARISON WITH false. `not` is
				// negation of the truth value, and `xor i1 x, true` is total on
				// i1 — there is no third bit pattern for it to disagree with the
				// reference about.
				ta := e.truth(a)
				n := e.next()
				fmt.Fprintf(&e.b, "  %s = xor i1 %s, true\n", n, ta)
				return e.liftBool(n), nil
			}
		}
		// `neg` AT Int, AND THE TYPE GUARD IS WHAT KEEPS IT AT Int. `neg`'s
		// typing rule (check.go's numericTy) admits Rat and Float as well, and
		// this backend lowers neither — so an operand that synthesises to
		// anything but Int falls through to the refusal below, exactly as a Rat
		// addition falls out of the binary Int table above. A guard on the
		// OPERATOR NAME alone would hand a Rat to an integer runtime.
		if t.Op == "neg" && len(t.Args) == 1 {
			if at, err := e.chk.synth(e.ctx, &t.Args[0]); err == nil && at != nil && at.K == "int" {
				a, err := e.expr(&t.Args[0], env, depth, self)
				if err != nil {
					return "", err
				}
				// o_int_neg RATHER THAN A SUBTRACTION FROM AN EMITTED ZERO. The
				// two agree — o_int_sub already reduces to a sign flip when its
				// left operand is zero — but emitting a literal here would put a
				// value in the program that the source does not contain, and it
				// would allocate one Int per negation to compute nothing.
				v := e.next()
				fmt.Fprintf(&e.b, "  %s = call ptr @o_int_neg(ptr %s)\n", v, a)
				return v, nil
			}
		}
		return "", llvmUnsupported(reasonPrim, fmt.Sprintf("the primitive operation %q", t.Op))
	}
	return "", llvmUnsupported(reasonTermKind, fmt.Sprintf("%q terms", t.K))
}

// truth reads the i1 truth of a Bool value.
//
// It is the SAME extractor `if` uses (emitIf), and deliberately so: two ways to
// ask whether a Bool is true are two things that can disagree, and the language
// has one answer. o_truth is total — it answers 0 for anything that is not a
// true Bool — so a malformed value cannot trap here; what keeps a non-Bool from
// reaching it at all is the type guard at each call site.
func (e *llvmEmitter) truth(v string) string {
	r := e.next()
	fmt.Fprintf(&e.b, "  %s = call i1 @o_truth(ptr %s)\n", r, v)
	return r
}

// liftBool wraps an i1 back into a Bool value, through the same o_bool the
// ordering comparisons use.
func (e *llvmEmitter) liftBool(b string) string {
	z := e.next()
	fmt.Fprintf(&e.b, "  %s = zext i1 %s to i32\n", z, b)
	v := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_bool(i32 %s)\n", v, z)
	return v
}

// combineBool emits `op` over the truths of two operands that have ALREADY been
// emitted — which is the point, and the reason the operands are not emitted
// here. A helper that took two Terms and emitted them itself would be a place
// where a later edit could make one of them conditional; taking two registers
// means the caller has already committed to evaluating both.
func (e *llvmEmitter) combineBool(op, a, b string) string {
	ta, tb := e.truth(a), e.truth(b)
	r := e.next()
	fmt.Fprintf(&e.b, "  %s = %s i1 %s, %s\n", r, op, ta, tb)
	return e.liftBool(r)
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
	e.openOathFunc(fmt.Sprintf("define ptr %s(ptr %%env, ptr %%arg) {\n", name))
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

// emitStrMatch destructures a Str by CODEPOINT.
//
// Str is packed (bytes + length), not a cons chain, so there is no constructor
// index to switch on and the generic o_field path does not apply: the runtime
// decodes the next Unicode scalar and returns it with a view of the remainder.
// The arms are still indexed by THIS store's SNil/SCons, which resolveStrCtors
// has already pinned - the runtime reports 0 for empty and 1 for non-empty, and
// those are mapped here rather than assumed to coincide.
func (e *llvmEmitter) emitStrMatch(t *Term, env string, depth int, self string) (string, error) {
	if len(t.Arms) != 2 || e.strNil < 0 || e.strCons < 0 {
		return "", llvmUnsupported(reasonMatchOnStrArms, "a match on Str whose arms are not SNil and SCons")
	}
	s, err := e.expr(t.A, env, depth, self)
	if err != nil {
		return "", err
	}
	id := e.tmp
	idx := e.next()
	fmt.Fprintf(&e.b, "  %s = call i32 @o_str_idx(ptr %s)\n", idx, s)
	nilL := fmt.Sprintf("snil%d", id)
	consL := fmt.Sprintf("scons%d", id)
	join := fmt.Sprintf("sjoin%d", id)
	cmp := e.next()
	fmt.Fprintf(&e.b, "  %s = icmp eq i32 %s, 0\n", cmp, idx)
	fmt.Fprintf(&e.b, "  br i1 %s, label %%%s, label %%%s\n", cmp, nilL, consL)

	// SNil: no binders.
	e.label(nilL)
	nv, err := e.expr(&t.Arms[e.strNil], env, depth, self)
	if err != nil {
		return "", err
	}
	nEnd := e.block
	fmt.Fprintf(&e.b, "  br label %%%s\n", join)

	// SCons: the scalar as an Oath Int, then the remaining Str, pushed in
	// constructor field order so the arm's de Bruijn indices line up.
	e.label(consL)
	head := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_str_head(ptr %s)\n", head, s)
	e1 := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_env_push(ptr %s, ptr %s)\n", e1, env, head)
	tail := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_str_tail(ptr %s)\n", tail, s)
	e2 := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_env_push(ptr %s, ptr %s)\n", e2, e1, tail)
	e.ctx = append(e.ctx, intTy(), strTy(e.strHash))
	cv, err := e.expr(&t.Arms[e.strCons], e2, depth+2, self)
	e.ctx = e.ctx[:len(e.ctx)-2]
	if err != nil {
		return "", err
	}
	cEnd := e.block
	fmt.Fprintf(&e.b, "  br label %%%s\n", join)

	e.label(join)
	v := e.next()
	fmt.Fprintf(&e.b, "  %s = phi ptr [ %s, %%%s ], [ %s, %%%s ]\n", v, nv, nEnd, cv, cEnd)
	return v, nil
}

func intTy() *Ty { return &Ty{K: "int"} }

func strTy(hash string) *Ty { return &Ty{K: "data", Hash: hash} }

// isStrTy reports whether a synthesised type is THIS store's Str.
//
// The hash is the whole test, for the same reason emitMatch dispatches on it:
// Str is a datatype like any other and its identity is its content hash, so a
// name comparison would accept a different type someone else called Str and
// reject this one under an alias. An empty strHash means the store has no Str
// at all, and then nothing is one.
func (e *llvmEmitter) isStrTy(t *Ty) bool {
	return t != nil && t.K == "data" && t.Hash == e.strHash && e.strHash != ""
}

func (e *llvmEmitter) emitMatch(t *Term, env string, depth int, self string) (string, error) {
	if t.Hash == e.strHash && e.strHash != "" {
		return e.emitStrMatch(t, env, depth, self)
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
// MEMORY IS A REQUEST ARENA WITH A NAMED RELEASE POINT (#165, step 3). Every
// allocation a request makes comes from one region, and the region is released
// after that request's answer has been SERIALISED — for a CLI entry, after
// `o_print` and before `main` returns. Nothing observable changes: a CLI program
// is one request, so the region is filled once and released once, at the moment
// process exit would have reclaimed it anyway.
//
// THE HANDLER PROTOCOL IS WHAT THE RELEASE POINT WAS FOR, and it now uses it:
// o_serve releases after each response is serialised, so a long-running server
// is flat rather than growing per request. The release is sound for the reason
// that selected this design over reference counting, tracing and region
// inference: it happens after serialisation, so the value that could have
// escaped is already bytes and no escape analysis over VALUES is required.
//
// The arena is also what makes a REFUSAL cheap to unwind. A refusal inside a
// handler must become a 500 rather than an exit, so it abandons every frame
// between the condition and the serve loop — and a runtime that freed per value
// would leak exactly what a refused request allocated. Here the whole region
// goes at the release point whichever way the request ended.
const llvmRuntimeC = `
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <stddef.h>
#include <setjmp.h>
#include <errno.h>
#include <time.h>
/* THE SOCKET LAYER IS THE ONLY PART OF THIS RUNTIME THAT IS NOT PORTABLE C, and
   it is guarded rather than assumed. Every program this backend emits links
   this one file, CLI entries included, so an unconditional POSIX include would
   have made a build through this backend fail on Windows for programs that never
   open a socket - a platform this project publishes a binary for. What is
   refused there is the handler protocol, named, at the one place it is
   reachable. */
#if !defined(_WIN32)
#include <signal.h>
#include <unistd.h>
/* getrlimit, for the stack guard's budget. Inside the POSIX block for the same
   reason the sockets are: the CLI entry links this file on Windows too, and the
   guard falls back to a compiled-in budget there rather than failing to build. */
#include <sys/resource.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#endif

typedef struct OVal OVal;
typedef OVal *(*OCode)(OVal **env, OVal *arg);

enum { T_CTOR = 0, T_STR = 1, T_CLOS = 2, T_BOOL = 3, T_INT = 4 };

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
  /* T_INT storage: SIGN-MAGNITUDE, base 2^32, least-significant limb first.
     isign is -1, 0 or +1 and is 0 if and only if the value is zero; ilen
     carries no leading zero limbs. That pair of invariants is what makes
     comparison a limb-count check before anything else, and it is established
     in exactly one place — o_int_wrap. See o_int. */
  int isign;
  int ilen;
  unsigned int *imag;
  OVal **f;
  OCode code;
  OVal **env;
};

/* ---------- the request arena ----------

   ONE REGION, RELEASED AFTER SERIALISATION. Everything a request allocates is
   carved from blocks held on the list below, and o_arena_release frees the whole
   list once the answer has become octets. The emitter places that call, because
   WHERE the release goes is a property of the entry protocol and not of the
   allocator.

   ZERO-INITIALISATION IS PRESERVED BY AN INVARIANT, NOT BY A MEMSET, and callers
   depend on it: val leaves every field it does not assign at zero and
   o_magalloc hands out zero limbs. A block arrives zeroed from calloc, is carved
   STRICTLY FORWARD, and is FREED at release rather than rewound — so no byte is
   ever handed out twice, and every byte returned is untouched since calloc
   zeroed it. Reusing a block by resetting used would break that and would need
   a memset; it is deliberately not done.

   ALIGNMENT IS DERIVED FROM THE TARGET, NOT ASSERTED. The block base is
   calloc's, which is suitable for any type; every carve is then rounded to
   _Alignof(max_align_t), which is the same guarantee the C library gives and the
   same one the old calloc-per-object had for free. A hard-coded 16 would be a
   claim about the target rather than a property of it - right on the platforms
   this backend has been run on and silently wrong on one where the fundamental
   alignment is larger. The power-of-two assumption the mask below rests on is
   checked at COMPILE TIME, because a mask over a non-power-of-two alignment
   would misalign quietly. */

typedef struct OBlock OBlock;
struct OBlock { OBlock *next; char *base; size_t used; size_t cap; };

#define O_ARENA_ALIGN ((size_t)_Alignof(max_align_t))
#define O_ARENA_BLOCK ((size_t)65536)

typedef char o_arena_align_is_a_power_of_two[
  (O_ARENA_ALIGN != 0 && (O_ARENA_ALIGN & (O_ARENA_ALIGN - 1)) == 0) ? 1 : -1];

/* THE REQUEST ARENA'S OWNERSHIP ROOT.

   IT DOES RETAIN REQUEST MEMORY, AND SAYING OTHERWISE WOULD BE THE WHOLE
   ARGUMENT GOT WRONG. Every OVal, every Str buffer and every magnitude a request
   allocates lives inside a block reachable from here through base, so this
   pointer transitively holds the entire request. That is what an arena IS. The
   invariant that replaces "no static storage at all" is therefore about WHEN,
   not about WHAT:

     - this ONE root may retain request allocations FOR THE DURATION of the
       request;
     - o_arena_release CLEARS IT and then frees, so no request pointer is
       reachable from static storage after the release point;
     - every OTHER static pointer stays forbidden unless it can be shown never
       to hold a request allocation at all, because nothing clears one - a
       capability that parked a value in an unaccounted slot would hold it past
       the release the whole design turns on.

   The declaration scan in llvm_runtime_state_test.go permits this NAME AT THIS
   TYPE and nothing else, and the clearing half is witnessed separately: an
   exemption for a root that is never cleared would be an exemption for exactly
   the retention this forbids. */
static OBlock *o_arena_blocks;

/* ---------- the process-lifetime region (#173) ----------

   A SECOND REGION, NEVER RELEASED, AND THE EXEMPTION IT NEEDS IS A DIFFERENT
   ONE FROM THE ARENA'S. The arena root is permitted because its retention ENDS;
   this root's retention never ends, so what has to be true instead is that it
   NEVER HOLDS A REQUEST ALLOCATION. That is a claim about WHEN it can be carved
   from, and o_perm_state is the whole of it.

   WHY IT EXISTS. A capability-first handler is (-> {caps} (-> Request
   Response)). SPEC 14 and the launch gate both require the capability record to
   be resolved ONCE, before the listener binds, so a host that cannot supply the
   program's authority fails to launch rather than accepting traffic it cannot
   serve. The closures in that record - and the handler closure obtained by
   applying the entry to it - are therefore live for every request the process
   ever answers. Allocating them in the request arena would free them at the end
   of the FIRST request and apply a dangling closure on the second, which is why
   this backend refused the shape until this region existed. Asserting that the
   record must outlive the request does not create anywhere to put it.

   THE STATE MACHINE IS THE ARGUMENT, and it is one-way:

     0 CLOSED  the region cannot be carved from. The state at process start, and
               the state every CLI entry stays in for its whole run.
     1 OPEN    provisioning. Reached once, by o_perm_open, before any listener
               exists; xalloc carves from o_perm_blocks while it lasts.
     2 SEALED  terminal. o_serve_loop is entered only from here, so every
               allocation any request makes is an arena allocation, and no
               request pointer can reach this root.

   Both transitions are guarded and a violation is o_bug, not a silent fallback:
   an unexpected state means this compiler emitted a sequence the runtime does
   not implement, and continuing would put request memory somewhere nothing
   frees.

   WHAT IT DOES NOT ESTABLISH, for the same reason the arena's scan does not: a
   capability that hands a request pointer to libc, or writes one into a
   perm-allocated object it still holds, is invisible here. This governs where
   allocations LAND, not what a provider does with one afterwards. */
static OBlock *o_perm_blocks;
static int o_perm_state;

/* NOT ROUTED THROUGH THE REFUSAL DOOR BELOW, deliberately. Every other refusal
   leaves the process able to serve the next request; an allocator that cannot
   satisfy a 64K block has already lost the ability to answer anything, and
   turning that into a 500-and-continue would produce a server that accepts
   traffic it can never serve. It is a host-resource failure, not a condition of
   the program. */
static void o_oom(void) { fputs("oath: out of memory\n", stderr); exit(70); }

/* ---------- the refusal boundary ----------

   THIS RUNTIME ENDS A COMPUTATION IN EXACTLY TWO WAYS, and the two are told
   apart HERE rather than at each site, so that the classification is structural:

     o_refused   a HOST-SIDE REFUSAL. A condition a WELL-TYPED Oath program can
                 reach at run time - a zero divisor, a codepoint outside the
                 Unicode scalars, octets from outside the language that are not
                 text. These are the artifact's contract with its host.
     o_bug       a condition unreachable unless this compiler is WRONG - a field
                 access off the end of a constructor, an unbound variable, an
                 applied non-function. Not a refusal the artifact offers its
                 supervisor; a bug report.

   WHY A REFUSAL TRAVELS INSTEAD OF EXITING WHERE IT IS DETECTED. The CONDITION
   is a fact about the language; what should HAPPEN to it is a fact about the
   BOUNDARY the artifact runs under, and only the boundary knows. A standalone
   program has nothing left to do, so it exits 70. A handler is a long-lived
   server whose input is chosen by a remote party, and SPEC 14.2 is explicit
   that a remote party must never be able to end the process - so there the
   disposition is one stderr line, a 500, and a server that keeps serving. Same
   door, two dispositions. This is the same design the Go backend's runtime
   arrived at (oathRefuse / oathRefusalOf), reached independently because the
   obligation is the specification's rather than either language's.

   A BUG IS DELIBERATELY NOT CAUGHT. Converting a compiler defect into an
   orderly 500 would make it indistinguishable from the host declining to do
   something, which is the collapse this split exists to undo.

   THE MESSAGE IS ALREADY ON STDERR when o_refused is called. Every refusal site
   here formats its own diagnostic with its own operands, and routing those
   through one message-taking door would mean rebuilding each of them into a
   buffer for nothing - the classification is what has to be central, not the
   printing.

   LONGJMP IS SAFE HERE BECAUSE OF THE ARENA, and that is not a coincidence.
   Unwinding abandons every intermediate frame, so a runtime that freed per
   value would leak exactly the allocations a refused request made; every
   allocation here belongs to the request arena instead, and the serve loop
   releases the whole region after it has answered. Nothing else in this runtime
   holds a resource across a call that can refuse. */
static int o_request_live;
static jmp_buf o_request_jump;

static void o_refused(void) {
  /* Cleared BEFORE the jump, so the loop cannot re-enter this door while it is
     disposing of the refusal it is already handling. */
  if (o_request_live) { o_request_live = 0; longjmp(o_request_jump, 1); }
  exit(70);
}

static void o_bug(const char *what) {
  fprintf(stderr, "oath: %s\n", what);
  exit(70);
}

/* ---------- the stack guard ----------

   WHAT THIS IS FOR, and it is NOT a nicer diagnostic. Oath recursion becomes C
   recursion here, so a deep enough structure exhausts the process stack. Before
   this guard that was a SIGSEGV: exit 139, zero bytes on stdout and stderr,
   measured. Two consequences, and the second is a conformance failure rather
   than an ergonomics complaint:

     - a standalone program died with no way to tell exhaustion from a compiler
       bug or a host fault;
     - a HANDLER died, and its input is chosen by a remote party. SPEC 14.2
       answers 400 for an unrepresentable request field precisely "because a
       remote party must not be able to halt a host" - and a body deep enough to
       exhaust the stack halted it. The guarantee was already normative; this
       backend just could not keep it.

   Routing through o_refused is therefore the whole point, not an implementation
   convenience: it is the door that already knows a standalone program exits 70
   and a handler answers 500 and keeps serving. The arena is what makes the
   longjmp sound, for the reason recorded at that door - every allocation the
   abandoned frames made belongs to the request region.

   WHY A POINTER COMPARISON AND NOT A FRAME COUNTER. A counter must be
   DECREMENTED, which needs an epilogue at every return edge; the address of a
   local is exact, costs no bookkeeping, and cannot get out of step with the
   real stack. It also measures what actually runs out - BYTES - rather than a
   proxy for it, so a function with a large frame consumes the budget faster,
   which is the truth a counter would hide.

   THE BUDGET IS DERIVED FROM THE HOST, NOT COMPILED IN. The evaluator's depth
   guard records what happens otherwise: a constant calibrated on a developer
   machine survived as a comment that read correctly while the deployed
   container was OOM-killed before the guard could fire. getrlimit answers for
   the process actually running, so 'ulimit -s' raises this ceiling with no
   rebuild - which is also what keeps the refusal HONEST, since the limit is a
   fact about this artifact's environment and not about the program.

   ITS OWN FALSIFIER: if the guard ever fires on a program that would have
   COMPLETED within the real stack, the margin is wrong and the guard is worse
   than the crash it replaces - it would be reporting a limit the host does not
   have. That is why the margin is subtracted from a MEASURED limit rather than
   guessed, and why the diagnostic REPORTS the budget it derived: the test reads
   it back from there and pins it to the limit it imposed. */

/* Reserved below the floor so the refusal path can still run: the guard fires
   with this much stack left, and fprintf and longjmp need frames. A guard that
   fires with no room to report IS the crash it was meant to replace. */
#define O_STACK_MARGIN ((size_t)131072)

/* WHAT THE MARGIN ASSUMES, stated because it is the guard's one unclosed edge.
   The check runs as the first IR instruction of a body, but the machine-code
   PROLOGUE has already reserved that body's frame by then. So the guard is sound
   only while no emitted frame exceeds the margin: a single frame larger than
   128K would fault in its own prologue, before the check it is about to run.

   This backend's bodies are ptr f(ptr env, ptr arg) and every Oath value lives
   in the arena, so a frame holds spilled SSA temporaries and nothing else - 128K
   is 16,384 pointers in ONE function. Nothing in this corpus is within three
   orders of magnitude of that. It is a BOUND, not a proof, and the honest form
   of the claim is: exhaustion is caught for any program whose largest single
   frame fits in the margin. Closing it properly means a pre-frame probe, which
   is a different piece of work. */

/* TWO FALLBACKS, NOT ONE, BECAUSE THE TWO CASES PULL OPPOSITE WAYS. Both are
   used where getrlimit cannot answer, and the asymmetry that decides them is:
   erring SMALL costs an early refusal, which is legible; erring LARGE puts the
   floor BELOW the real stack boundary, so the fault arrives before the guard
   and the silent crash is back. A fallback is only safe if it cannot exceed the
   real stack.

     UNLIMITED rlimit   the real stack is at least this big by construction, so
                        a few megabytes is conservative.
     WINDOWS            the PE default stack RESERVE is 1 MB, and this runtime
                        cannot ask for it without pulling in windows.h. Half a
                        megabyte fits inside that with room for the margin. It
                        is deliberately pessimistic: a Windows CLI artifact will
                        refuse deeper recursion than it strictly must, and that
                        is the direction to be wrong in. Querying
                        GetCurrentThreadStackLimits would remove the guess. */
#define O_STACK_FALLBACK_UNLIMITED ((size_t)(4 * 1024 * 1024))
#define O_STACK_FALLBACK_WIN       ((size_t)(512 * 1024))

/* Not static: the emitted IR loads this at every function entry. */
uintptr_t o_stack_floor;
static size_t o_stack_budget;
/* Whether the budget came from a MEASURED rlimit or from a fallback. The
   diagnostic's remediation depends on it: 'raise ulimit -s' is true only in the
   first case, and is actively misleading in the second - Windows has no such
   command, and setting a POSIX limit to UNLIMITED selects the fallback and
   LOWERS the budget. Advice that is wrong in the direction of "try this" costs
   more than no advice. */
static int o_stack_from_rlimit;

/* A CONSTRUCTOR, so no entry protocol can forget it. main is emitted as IR and
   there are two shapes of it (CLI and serve); initialising from either would
   put the guard's correctness in the emitter, where a third shape would silently
   omit it. A constructor runs before main on the same stack, so the base it
   records is main's to within a frame - immaterial against the margin. */
/* THE STARTUP BLOCK, MEASURED RATHER THAN ASSUMED AWAY.

   argv and the environment are laid down at the stack TOP before any user code
   runs, so a constructor's own frame is already below it. Subtracting a whole
   RLIMIT_STACK from that frame puts the floor PAST THE END, and a process
   started with a large environment - ARG_MAX is megabytes on both supported
   platforms - faults before the guard can fire. Nothing about the failure would
   point here, and the invocation is legal rather than exotic.

   Both vectors are scanned and each string's END is taken, not its start: an
   earlier draft used the pointer alone, which under-measures by the length of
   the highest string and leaves exactly the same hole for one large variable.
   And argv is scanned rather than only environ, because an empty environment
   with a large argument vector has the same shape.

   glibc and dyld both pass (argc, argv, envp) to a constructor. Where that is
   not honoured the parameters are garbage, so every address is admitted only if
   it lies ABOVE our frame and within a sane distance of it - which also rejects
   a heap-allocated variable left by a setenv. A rejected vector costs the old
   under-measurement, never a floor further past the end. */
static void o_stack_top(uintptr_t *top, char **v, int n) {
  if (!v) return;
  for (int i = 0; (n < 0 || i < n) && v[i]; i++) {
    uintptr_t a = (uintptr_t)v[i];
    if (a <= *top || a - *top >= (uintptr_t)(64 * 1024 * 1024)) continue;
    *top = a + (uintptr_t)strlen(v[i]) + 1;
  }
}

/* THE THREE-ARGUMENT CONSTRUCTOR IS NOT PORTABLE, AND WHAT REPLACES IT NEEDS AN
   ALLOWANCE RATHER THAN AN APOLOGY. glibc and dyld both pass (argc, argv, envp)
   to an .init_array entry; the C and POSIX guarantee is only void(void), and a
   runtime honouring just that - musl, for instance - would leave those
   parameters INDETERMINATE. Reading them is harmless; passing them to a scan
   that DEREFERENCES the vector is a startup crash in a guard whose whole purpose
   is to prevent a crash.

   So the parameterised form is used only where the ABI is documented. Elsewhere
   the constructor takes no arguments and reads 'environ', which POSIX guarantees
   is valid here - and ARGV IS THEN UNMEASURED.

   AN EARLIER DRAFT CALLED THAT "the safe direction". IT IS THE OPPOSITE, and the
   error is easy to make because the words point the wrong way: failing to see
   the startup block leaves the measured top LOWER, so the floor computed from it
   is lower too, which is FURTHER PAST THE END. Unmeasured startup consumption
   makes the guard fire LATE or not at all - exactly the silent crash.

   The allowance closes it with a bound rather than a guess: Linux caps args and
   environment together at RLIMIT_STACK/4, so a quarter of the budget is an upper
   bound on what could not be seen. Where the block is smaller the guard refuses
   early, and THAT is the safe direction. */
static void o_stack_init_from(int argc, char **argv, char **envp, int startup_seen);

#if defined(_WIN32)
/* The command line arrives through the PEB, not on the stack, so there is no
   unmeasured stack-resident startup block here: startup_seen is 1. */
__attribute__((constructor))
static void o_stack_ctor(void) { o_stack_init_from(0, (char **)0, (char **)0, 1); }
#elif defined(__GLIBC__) || defined(__APPLE__)
__attribute__((constructor))
static void o_stack_ctor(int argc, char **argv, char **envp) {
  o_stack_init_from(argc, argv, envp, 1);
}
#else
__attribute__((constructor))
static void o_stack_ctor(void) {
  /* BLOCK SCOPE. This is the host C runtime's object, so nothing is allocated
     here - and keeping the declaration inside the one function that needs it
     also keeps it off the emitted runtime's file-scope surface, which
     TestEmittedRuntimeDeclaresNoStaticStorageForValues reads in full. */
  extern char **environ;
  o_stack_init_from(0, (char **)0, environ, 0);
}
#endif

static void o_stack_init_from(int argc, char **argv, char **envp, int startup_seen) {
  char probe;
  uintptr_t base = (uintptr_t)&probe;
  size_t budget;
#if !defined(_WIN32)
  o_stack_top(&base, argv, argc > 0 ? argc : 0);
  o_stack_top(&base, envp, -1);
#else
  (void)argc; (void)argv; (void)envp;
#endif
#if defined(_WIN32)
  budget = O_STACK_FALLBACK_WIN;
#else
  struct rlimit rl;
  if (getrlimit(RLIMIT_STACK, &rl) == 0 && rl.rlim_cur != RLIM_INFINITY &&
      rl.rlim_cur > 0)
    { budget = (size_t)rl.rlim_cur; o_stack_from_rlimit = 1; }
  else
    budget = O_STACK_FALLBACK_UNLIMITED;
#endif
  /* THE MARGIN IS PROPORTIONAL WHERE THE STACK IS SMALL, and an earlier draft
     got this backwards: it REPLACED any limit at or below twice the margin with
     the fallback, which on a host with a 256K stack substituted a 4M budget and
     put the floor megabytes past the end. A measured limit is never enlarged
     here - it is only ever reduced. */
  size_t margin = O_STACK_MARGIN;
  if (margin > budget / 4) margin = budget / 4;
  if (margin == 0) margin = 1;
  o_stack_budget = budget - margin;
  if (!startup_seen) {
    /* An UPPER BOUND on what could not be seen, not an estimate: see the header
       above. Halved rather than skipped if it would consume the whole budget,
       so a pathological limit still leaves the guard able to fire. */
    size_t allow = budget / 4;
    /* AND A FLOOR UNDER THE ALLOWANCE. Linux permits at least 32 pages of
       arguments and environment (MIN_ARG_PAGES) REGARDLESS of RLIMIT_STACK/4,
       so on a small stack a quarter of the budget is not an upper bound on what
       could not be seen - and an allowance that is not an upper bound is not an
       allowance, it is a smaller version of the same hole. */
    if (allow < (size_t)(128 * 1024)) allow = (size_t)(128 * 1024);
    if (allow >= o_stack_budget) {
      /* THE ALLOWANCE IS AN UPPER BOUND OR IT IS NOTHING. Where the startup
         block could legally be the whole stack, no floor computed here is
         guaranteed to sit inside it - and an earlier draft HALVED the allowance
         to make the arithmetic fit, which produces a number rather than a bound
         and puts the silent crash back on exactly the hosts least able to
         absorb it.

         So this refuses on the first call instead of guessing. A refusal naming
         a tiny budget is legible and points at 'ulimit -s'; a floor nobody can
         justify is the defect this guard exists to remove. */
      o_stack_budget = 4096;
    } else {
      o_stack_budget -= allow;
    }
  }
  /* Computed in uintptr_t, not by pointer arithmetic: '&probe - budget' is
     undefined once it leaves the object, and the comparison it feeds is the
     thing this guard rests on. The clamp is for a base below the budget, which
     no supported host presents and which would otherwise WRAP - turning the
     floor into a huge address and making every call refuse. */
  o_stack_floor = (o_stack_budget < base) ? base - (uintptr_t)o_stack_budget : (uintptr_t)1;
}


void o_stack_exhausted(void) {
  /* THE MESSAGE IS ABOUT THIS ARTIFACT, NOT ABOUT THE PROGRAM. Exhausting a
     budget is an implementation limit; saying "this program does not terminate"
     would promote a fact about the tool into a claim about the world, and the
     two are not distinguishable from here - a terminating program simply deeper
     than the budget reaches this same line. */
  fprintf(stderr,
      "oath: this artifact exhausted its stack budget of %zu bytes. That is a "
      "limit of THIS compiled artifact, not a property of the program: %s"
      "'oath eval' runs the same definition on its own budget. If the recursion "
      "has no base case the program does not terminate, but reaching this line "
      "is not evidence either way.\n",
      o_stack_budget,
      o_stack_from_rlimit
          ? "a larger 'ulimit -s' raises it with no rebuild, and "
          /* No rlimit was readable, so the budget is compiled in and no host
             setting moves it. Saying otherwise would send an operator to a
             control that does nothing - or, where the limit is UNLIMITED, to
             one that already selected this smaller fallback. */
          : "this host reports no stack limit this runtime can read, so the "
            "budget above is the compiled-in fallback and no host setting "
            "raises it; "
      );
  o_refused();
  /* o_refused exits or longjmps; it never returns. Declared noreturn to the IR,
     so the emitted prologue can end its failure block with 'unreachable'. */
  abort();
}

static OBlock *o_block_new(size_t cap) {
  OBlock *nb = (OBlock *)calloc(1, sizeof(OBlock));
  if (!nb) o_oom();
  nb->base = (char *)calloc(1, cap);
  if (!nb->base) { free(nb); o_oom(); }
  nb->cap = cap;
  return nb;
}

/* THE CARVE, over WHICHEVER region's block list it is handed. Parameterised
   rather than duplicated: the two regions differ in WHEN they are freed and in
   nothing else, and a second copy of this is where the oversized-block rule
   below would silently hold for one region and not the other. */
static void *o_carve(OBlock **head, size_t n) {
  /* The rounding must not wrap: a wrapped size would under-allocate silently,
     which is a heap overflow rather than the out-of-memory exit it looks like. */
  if (n > (size_t)-1 - (O_ARENA_ALIGN - 1)) o_oom();
  size_t need = (n + (O_ARENA_ALIGN - 1)) & ~(O_ARENA_ALIGN - 1);
  OBlock *b = *head;
  if (!b || b->cap - b->used < need) {
    /* A REQUEST THAT FILLS A BLOCK GETS ONE OF ITS OWN, LINKED BEHIND THE ACTIVE
       BLOCK RATHER THAN IN FRONT OF IT. Such a block is full the instant it is
       returned, so making it the head would hide whatever space the active block
       still has: the next small allocation would start a fresh 64K block, and a
       request alternating a big buffer with an OVal - a magnitude that fills a
       block, then the OVal wrapping it - would abandon a nearly empty block per
       result. Only the HEAD is ever carved from, so the head must stay the block
       with room.

       THE TEST IS >=, NOT >, AND THE BOUNDARY IS NOT A DETAIL. A request of
       EXACTLY one block consumes a standard block completely; routing it through
       the ordinary path below would put a full block at the head, which is the
       same defect with a different spelling. Equal-to is a dedicated allocation
       in every respect except size. */
    if (need >= O_ARENA_BLOCK) {
      OBlock *big = o_block_new(need);
      big->used = need;
      if (b) { big->next = b->next; b->next = big; }
      else { *head = big; }
      return big->base;
    }
    OBlock *nb = o_block_new(O_ARENA_BLOCK);
    nb->next = *head;
    *head = nb;
    b = nb;
  }
  void *p = b->base + b->used;
  b->used += need;
  return p;
}

/* THE ONE PLACE THE REGION IS CHOSEN. Every allocation in this runtime reaches
   the allocator through here, so the region an allocation belongs to is decided
   by the process's phase rather than by each call site remembering which one it
   is in - which is what makes "no request allocation is in the perm region" a
   property of one function instead of an audit of every caller. */
static void *xalloc(size_t n) {
  return o_carve(o_perm_state == 1 ? &o_perm_blocks : &o_arena_blocks, n);
}

/* THE TWO TRANSITIONS. One-way, guarded, and a violation is a compiler bug
   rather than a condition of the program - see the region's header. */
static void o_perm_open(void) {
  if (o_perm_state != 0) o_bug("the process-lifetime region was opened twice");
  o_perm_state = 1;
}
static void o_perm_seal(void) {
  if (o_perm_state != 1) o_bug("the process-lifetime region was sealed without being opened");
  o_perm_state = 2;
}

/* THE RELEASE POINT, AND THE HALF THAT DISCHARGES THE ROOT'S EXEMPTION. Called
   after the answer has been serialised and before main returns.

   IT CLEARS THE ROOT BEFORE IT FREES, and the order is the discipline rather
   than an optimisation: at no point is the static root pointing at memory that
   has been freed, so a future traversal, sanitizer or reentrant call cannot
   reach a released request through it. Clearing after the loop would leave the
   post-condition the same and the window open, which is why it is pinned
   structurally and not only by behaviour.

   IDEMPOTENT, and the arena is usable again afterwards: the next xalloc starts a
   fresh block list. Nothing else in this runtime frees - the exit(70) paths
   leave the arena to process exit, which is the same behaviour they had before
   an arena existed.

   IT RELEASES ONE REGION, AND THE OMISSION IS THE POINT. o_perm_blocks is
   deliberately untouched: it holds the capability record and the handler
   closure, which are live for every request this process will ever answer.
   Freeing both here would be the defect this region was added to remove. */
void o_arena_release(void) {
  OBlock *b = o_arena_blocks;
  o_arena_blocks = 0;
  while (b) {
    OBlock *next = b->next;
    free(b->base);
    free(b);
    b = next;
  }
}

static OVal *val(int tag) { OVal *v = (OVal *)xalloc(sizeof(OVal)); v->tag = tag; v->idx = -1; return v; }

OVal *o_strn(const char *s, int n) {
  OVal *v = val(T_STR); v->s = s ? s : ""; v->slen = n; return v;
}
/* For genuine C strings — getenv results, literals internal to the runtime. */
OVal *o_str(const char *s) { return o_strn(s, s ? (int)strlen(s) : 0); }
OVal *o_bool(int b) { OVal *v = val(T_BOOL); v->idx = b ? 1 : 0; return v; }

/* AN OATH Int IS UNBOUNDED, AND THIS IS THE REPRESENTATION THAT MAKES THAT TRUE.

   Oath's Int is mathematically Z - arbitrary precision, SPEC 1, and the int term
   carries a big.Int. That is a SEMANTIC commitment rather than a representation
   choice: the prover's Int is unbounded, so a machine-width Int would put
   overflow reasoning into every arithmetic proof in the corpus. A backend is
   free to lay out a Set or a Str however it likes and is not free to bound Int.

   This runtime used to store a long long and refuse what did not fit. Refusing
   is honest and it is still a SUBSET, and #166's own falsifier was run before
   this was written: a checked int64 is observably distinguishable from Z by a
   program the subset accepts, with the overflow depending on runtime input, so
   the cheap answer lost on evidence. docs/experiments/issue-166-bignum-int/.

   NO DEPENDENCIES, which is a constraint on the whole backend and not a
   preference: IR is emitted as text and clang is invoked, and nothing links GMP.
   So the arithmetic is written here, in the emitted runtime.

   SIGN-MAGNITUDE, base 2^32, least-significant limb first. Limbs are 32 bits so
   that every intermediate fits a 64-bit unsigned, which is what lets this avoid
   both compiler-specific 128-bit types and any dependency.

   TWO INVARIANTS, established in ONE place (o_int_wrap) and relied on
   everywhere: isign is 0 if and only if the value is zero, and ilen counts no
   leading zero limbs. Together they make comparison a limb-count test first, and
   they are why every constructor below routes through the wrapper instead of
   filling the fields itself. */

typedef unsigned int o_u32;
typedef unsigned long long o_u64;

/* THE WIDTH IS CHECKED, NOT ASSUMED, because two unrelated things here depend on
   it and both fail silently if it is wrong. The arithmetic above is base 2^32 and
   relies on every limb product fitting an o_u64; SHA-256 below relies on exact
   32-bit wraparound, and on a target where an unsigned int were 64 bits it would
   compute a different function and still produce 32 plausible bytes. Same
   discipline as the arena's alignment assertion: derived at COMPILE time rather
   than believed. */
typedef char o_u32_is_exactly_32_bits[(sizeof(o_u32) == 4) ? 1 : -1];

static o_u32 *o_magalloc(int n) {
  return (o_u32 *)xalloc(sizeof(o_u32) * (size_t)(n > 0 ? n : 1));
}

/* The single normalizer. Nothing else may decide how long a magnitude is. */
static int o_magnorm(const o_u32 *d, int n) {
  while (n > 0 && d[n - 1] == 0) n--;
  return n;
}

/* THE ONLY CONSTRUCTOR OF A T_INT. Values are immutable and nothing here frees,
   so magnitudes are SHARED rather than copied - a caller handing this an array
   it still holds is fine, because neither party can mutate one.

   The zero clause is DEFENSIVE RATHER THAN LIVE, and saying so is the honest
   reading: mutating it to permit a negative zero changes no program's output,
   because no caller reaches here with a negative sign and an empty magnitude -
   cancellation passes sign 0 explicitly, multiplication by zero has sign 0, and
   the emitter never writes the literal "-0". It is kept because it is what makes
   the invariant hold for callers that do not yet exist, and because a comparison
   that had to ask "is this the negative kind of zero" would be the drift this
   file is built to avoid. */
static OVal *o_int_wrap(int sign, o_u32 *mag, int n) {
  OVal *v = val(T_INT);
  n = o_magnorm(mag, n);
  v->ilen = n;
  v->isign = n == 0 ? 0 : (sign < 0 ? -1 : 1);
  v->imag = n == 0 ? 0 : mag;
  return v;
}

static int o_magcmp(const o_u32 *a, int na, const o_u32 *b, int nb) {
  if (na != nb) return na < nb ? -1 : 1;
  for (int i = na - 1; i >= 0; i--)
    if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
  return 0;
}

static o_u32 *o_magadd(const o_u32 *a, int na, const o_u32 *b, int nb, int *nr) {
  if (na < nb) { const o_u32 *t = a; a = b; b = t; int k = na; na = nb; nb = k; }
  o_u32 *r = o_magalloc(na + 1);
  o_u64 carry = 0;
  for (int i = 0; i < na; i++) {
    o_u64 s = (o_u64)a[i] + carry + (o_u64)(i < nb ? b[i] : 0u);
    r[i] = (o_u32)s;
    carry = s >> 32;
  }
  r[na] = (o_u32)carry;
  *nr = na + 1;
  return r;
}

/* THE BORROW LOOP, IN PLACE AND WRITTEN ONCE. Requires |r| >= |b|; the caller
   orders the operands by o_magcmp. Division subtracts thousands of times and
   allocating a result for each would be pure waste, but the reason this is the
   core rather than a second copy is correctness, not speed: two borrow loops
   are two chances to get the borrow wrong, and only one of them would be under
   the differential fuzzer's nose. */
static void o_magsubfrom(o_u32 *r, int rn, const o_u32 *b, int nb) {
  o_u64 borrow = 0;
  for (int i = 0; i < rn; i++) {
    o_u64 bi = (o_u64)(i < nb ? b[i] : 0u) + borrow;
    o_u64 ai = (o_u64)r[i];
    if (ai >= bi) { r[i] = (o_u32)(ai - bi); borrow = 0; }
    else { r[i] = (o_u32)(ai + ((o_u64)1 << 32) - bi); borrow = 1; }
  }
}

static o_u32 *o_magsub(const o_u32 *a, int na, const o_u32 *b, int nb, int *nr) {
  o_u32 *r = o_magalloc(na);
  for (int i = 0; i < na; i++) r[i] = a[i];
  o_magsubfrom(r, na, b, nb);
  *nr = na;
  return r;
}

static o_u32 *o_magmul(const o_u32 *a, int na, const o_u32 *b, int nb, int *nr) {
  if (na == 0 || nb == 0) { *nr = 0; return o_magalloc(1); }
  int n = na + nb;
  o_u32 *r = o_magalloc(n);
  for (int i = 0; i < n; i++) r[i] = 0;
  for (int i = 0; i < na; i++) {
    o_u64 carry = 0;
    for (int j = 0; j < nb; j++) {
      o_u64 t = (o_u64)a[i] * (o_u64)b[j] + (o_u64)r[i + j] + carry;
      r[i + j] = (o_u32)t;
      carry = t >> 32;
    }
    /* The product of na and nb limbs occupies at most na+nb, so this cannot run
       off the end; the bound is asserted rather than assumed. */
    for (int k = i + nb; carry && k < n; k++) {
      o_u64 t = (o_u64)r[k] + carry;
      r[k] = (o_u32)t;
      carry = t >> 32;
    }
  }
  *nr = n;
  return r;
}

/* Small values - codepoints from a Str match, and anything the runtime itself
   builds. LLONG_MIN has no positive counterpart, so the magnitude is taken in
   unsigned arithmetic where negating it is defined. */
OVal *o_int(long long n) {
  o_u64 m = n < 0 ? (o_u64)0 - (o_u64)n : (o_u64)n;
  o_u32 *d = o_magalloc(2);
  d[0] = (o_u32)m;
  d[1] = (o_u32)(m >> 32);
  return o_int_wrap(n < 0 ? -1 : 1, d, 2);
}

/* A LITERAL OF ANY MAGNITUDE, from its decimal text.

   The compiler emits the digits rather than a machine word, which is what
   retires the old compile-time range refusal: there is no longer a literal this
   backend cannot represent. Repeated multiply-by-ten is quadratic in the digit
   count and that is fine - this runs once per literal, at the point of use.

   Malformed text is a hard failure rather than a guess. It can only come from
   the emitter, so it means the compiler is broken, and continuing would put an
   invented number into a verified program. */
OVal *o_int_dec(const char *s) {
  int sign = 1;
  const char *p = s ? s : "";
  if (*p == '-') { sign = -1; p++; }
  else if (*p == '+') { p++; }
  if (!*p) o_bug("the compiler emitted an Int literal with no digits");
  int cap = 4, n = 0;
  o_u32 *d = o_magalloc(cap);
  for (; *p; p++) {
    if (*p < '0' || *p > '9') {
      char m[192];
      snprintf(m, sizeof m, "the compiler emitted a malformed Int literal '%.120s'", s ? s : "");
      o_bug(m);
    }
    o_u64 carry = (o_u64)(*p - '0');
    for (int i = 0; i < n; i++) {
      o_u64 t = (o_u64)d[i] * 10u + carry;
      d[i] = (o_u32)t;
      carry = t >> 32;
    }
    while (carry) {
      if (n == cap) {
        int nc = cap * 2;
        o_u32 *g = o_magalloc(nc);
        for (int i = 0; i < n; i++) g[i] = d[i];
        d = g;
        cap = nc;
      }
      d[n++] = (o_u32)carry;
      carry >>= 32;
    }
  }
  return o_int_wrap(sign, d, n);
}

/* MATCHING A Str OBSERVES CODEPOINTS, NOT BYTES.

   The packed buffer is UTF-8; the language value is a sequence of Unicode
   scalars. So SNil is an empty buffer, and SCons yields the next SCALAR as an
   Oath Int plus the remaining buffer as a Str.

   OWNERSHIP, stated rather than left to be discovered: the tail is a VIEW into
   the parent buffer, not a copy. That is sound because every Str buffer in this
   runtime is immutable and outlives every observation of a view into it -
   literals are IR constants, capability values come from getenv, which is stable
   for the process lifetime, and a buffer built by o_str_cons is arena memory
   that is never freed or written again before the arena is released. Nothing
   frees during a request, so a view cannot dangle. If a Str ever gains a buffer
   with a shorter lifetime, this is the line that has to change, and it must
   change to a copy rather than to a hope.

   THE ARENA DOES NOT WEAKEN THIS, AND THAT IS THE PROPERTY THAT SELECTED IT. A
   view points into the arena of the request that created it and dies WITH that
   request, released only after the answer has been serialised - so the view and
   the buffer it spans have exactly the same lifetime, which is what reference
   counting, tracing collection and region inference each fail to give an
   interior pointer for free.

   THE CONDITION IS ON THE BUFFER'S LIFETIME, NOT ON WHERE IT CAME FROM, which is
   why adding runtime construction added a clause here rather than a caveat: an
   allocating constructor is exactly the change that would invalidate a view if
   the allocation were ever reused, and o_str_cons is written so that it is
   not. Arena blocks are freed rather than rewound at release for the same
   reason - reuse is what would turn a released view into a wrong answer instead
   of a dangling one.

   STRICT DECODING, done by hand: no locale, no mbrtowc, no replacement. It
   rejects overlong encodings, surrogates and anything above 0x10FFFF, because
   accepting them would let two distinct byte sequences denote one Str and undo
   the injectivity the packing side now guarantees. A malformed buffer is a hard
   failure: it can only arrive from OUTSIDE (a capability value is not validated
   on the way in - see #133), and guessing would be exactly the substitution
   this backend just stopped doing. */
static int o_utf8_next(const unsigned char *p, int n, long long *cp) {
  if (n <= 0) return 0;
  unsigned c = p[0];
  if (c < 0x80) { *cp = c; return 1; }
  int len; long long v;
  if ((c & 0xE0) == 0xC0) { len = 2; v = c & 0x1F; }
  else if ((c & 0xF0) == 0xE0) { len = 3; v = c & 0x0F; }
  else if ((c & 0xF8) == 0xF0) { len = 4; v = c & 0x07; }
  else return 0;
  if (n < len) return 0;
  for (int i = 1; i < len; i++) {
    if ((p[i] & 0xC0) != 0x80) return 0;
    v = (v << 6) | (p[i] & 0x3F);
  }
  if (len == 2 && v < 0x80) return 0;      /* overlong */
  if (len == 3 && v < 0x800) return 0;     /* overlong */
  if (len == 4 && v < 0x10000) return 0;   /* overlong */
  if (v > 0x10FFFF) return 0;
  if (v >= 0xD800 && v <= 0xDFFF) return 0;
  *cp = v;
  return len;
}

static void o_str_malformed(void) {
  fputs("oath: a Str holds bytes that are not valid UTF-8; this backend packs Str as UTF-8 "
        "and refuses to decode malformed storage rather than replace or guess it\n", stderr);
  o_refused();
}

/* ADMITTING EXTERNAL BYTES AS A Str.

   The comment on o_utf8_next used to end "a capability value is not validated on
   the way in - see #133". This is that validation, and it moves the refusal from
   DECODE to INGESTION - the boundary where the property first becomes
   observable. Deciding once, at the edge, is also what lets everything inside
   the language treat a Str as text without re-asking (#121's shape).

   Failing at decode was not wrong, just late: the diagnostic named a buffer with
   no indication of which getenv or which argument produced it, and a program
   that never matched on the value would run to completion carrying bytes that
   are not text.

   The per-request HTTP boundary is not reached from here: SPEC 14.2 row 9
   governs every Str built from a request and answers 400 without invoking the
   handler, because a remote party must not be able to end the process. */
static void o_str_host_refuse(const char *what) {
  fprintf(stderr, "oath: %s is not valid UTF-8, so it has no Str value; refusing rather than "
                  "substituting U+FFFD, which would make distinct inputs identical. "
                  "Arbitrary octets need a bytes-typed channel, not Str.\n", what);
  o_refused();
}

/* One scan, and the length is taken ONCE: computing it per codepoint makes
   validation quadratic in the input, which for a launch-time check on a large
   value is a startup stall rather than a slow function. */
static int o_utf8_valid(const char *s, int n) {
  const unsigned char *p = (const unsigned char *)s;
  for (int i = 0; i < n; ) {
    long long cp;
    int k = o_utf8_next(p + i, n - i, &cp);
    if (k == 0) return 0;
    i += k;
  }
  return 1;
}

static OVal *o_strn_host(const char *s, int n, const char *what) {
  if (!o_utf8_valid(s, n)) o_str_host_refuse(what);
  return o_strn(s ? s : "", n);
}

/* NUL is U+0000, an ordinary scalar, so file contents carrying one validate
   normally; only the C-string form has to stop at it. */
static OVal *o_str_host(const char *s, const char *what) {
  return o_strn_host(s, s ? (int)strlen(s) : 0, what);
}

static int o_str_step(OVal *v, long long *cp) {
  if (!v || v->tag != T_STR) o_bug("not a Str");
  if (v->slen <= 0) return 0;
  int n = o_utf8_next((const unsigned char *)v->s, v->slen, cp);
  if (n == 0) o_str_malformed();
  return n;
}

/* 0 selects the empty-string arm, 1 the cons arm. The emitter maps those onto
   this store's SNil/SCons constructor indices, which are not assumed here. */
int o_str_idx(OVal *v) { long long cp; return o_str_step(v, &cp) == 0 ? 0 : 1; }

OVal *o_str_head(OVal *v) {
  long long cp; int n = o_str_step(v, &cp);
  if (n == 0) o_bug("head of an empty Str");
  return o_int(cp);
}

OVal *o_str_tail(OVal *v) {
  long long cp; int n = o_str_step(v, &cp);
  if (n == 0) o_bug("tail of an empty Str");
  return o_strn(v->s + n, v->slen - n);
}

/* EQUALITY AT Str IS STRUCTURAL CODEPOINT EQUALITY, COMPUTED AS A BYTE COMPARE.

   THE COMPARISON IS OVER CODEPOINTS AND THE IMPLEMENTATION LOOKS AT BYTES, so
   the two coincide only because the packing is INJECTIVE: distinct codepoint
   sequences have distinct UTF-8 encodings exactly when every buffer holds
   canonical UTF-8. That is an invariant of this runtime, not of UTF-8 - overlong
   forms encode the same scalar differently - and it is established at every
   place a T_STR buffer can come into existence:

     an IR literal      the emitter folds only Unicode scalars (strLiteral) and
                        Go's string(rune) emits the canonical form
     o_str_cons         refuses non-scalars by class and encodes canonically
     o_str_tail         a view starting at a decode boundary of a valid buffer
     o_strn_host        o_utf8_valid, at ingestion, for argv/getenv/file reads
     a Request field    SPEC 14.2 row 9 admits printable US-ASCII (plus HTAB in
                        a field value), where the scalar IS the octet

   So there is no path by which two buffers denote one Str value while differing
   in bytes, and none by which one buffer denotes two. If a future source of Str
   bytes skips validation, THIS is the function that silently starts answering a
   different question - a decode-and-compare loop would fail loudly instead, and
   is the repair, not a second validation somewhere upstream.

   TOTAL, and deliberately so. Comparing is not observing: equality answers for
   malformed storage where a match would refuse it, which keeps equality out of
   the business of adjudicating bytes it did not admit.

   The length test first is not an optimisation - memcmp over differing lengths
   would read past the shorter buffer. slen carries the real length because a
   Str may contain U+0000, and a tail is a VIEW whose bytes are not NUL
   terminated at its own end. */
int o_str_eq(OVal *a, OVal *b) {
  if (!a || a->tag != T_STR || !b || b->tag != T_STR) o_bug("== at Str applied to a value that is not a Str");
  if (a->slen != b->slen) return 0;
  if (a->slen == 0) return 1;
  return memcmp(a->s, b->s, (size_t)a->slen) == 0;
}

static OVal *o_int_arg(OVal *v) {
  if (!v || v->tag != T_INT) o_bug("not an Int");
  return v;
}

/* THE DIAGNOSTIC RENDERING OF AN Int, and it exists for exactly one reason: a
   refusal that cannot NAME the element it refused lets two distinct values
   produce one message, which is the collapse this whole boundary exists to
   prevent, moved from the output into the error text.

   Repeated division of the magnitude by 10^9 - nine digits per pass, because
   10^9 fits a 32-bit limb and (rem << 32) | limb fits the 64-bit intermediate
   every operation here is written to stay inside. Quadratic in the limb count
   and reached only on the way to exit(70), so its cost is never on a live path.

   Ten decimal digits per 32-bit limb is a safe bound (2^32 < 10^10), plus a
   sign and a terminator. */
static char *o_int_decimal(OVal *v) {
  int n = v->ilen;
  if (n == 0) { char *z = (char *)xalloc(2); z[0] = '0'; return z; }
  o_u32 *d = o_magalloc(n);
  for (int i = 0; i < n; i++) d[i] = v->imag[i];
  int cap = n * 10 + 2;
  char *rev = (char *)xalloc((size_t)cap + 1);
  int len = 0;
  while (n > 0) {
    o_u64 rem = 0;
    for (int i = n - 1; i >= 0; i--) {
      o_u64 cur = (rem << 32) | (o_u64)d[i];
      d[i] = (o_u32)(cur / 1000000000u);
      rem = cur % 1000000000u;
    }
    n = o_magnorm(d, n);
    /* A FULL NINE DIGITS while limbs remain, so an interior chunk keeps its
       leading zeros; the last chunk stops at its own most significant digit.
       Reaching n == 0 with rem == 0 would mean the value was zero, which the
       first line already returned. */
    for (int k = 0; k < 9 && (rem > 0 || n > 0); k++) {
      rev[len++] = (char)('0' + (int)(rem % 10));
      rem /= 10;
    }
  }
  char *out = (char *)xalloc((size_t)len + 2);
  int j = 0;
  if (v->isign < 0) out[j++] = '-';
  for (int i = len - 1; i >= 0; i--) out[j++] = rev[i];
  return out;
}

static int o_utf8_encode(long long cp, unsigned char *out) {
  if (cp < 0x80) { out[0] = (unsigned char)cp; return 1; }
  if (cp < 0x800) {
    out[0] = (unsigned char)(0xC0 | (cp >> 6));
    out[1] = (unsigned char)(0x80 | (cp & 0x3F));
    return 2;
  }
  if (cp < 0x10000) {
    out[0] = (unsigned char)(0xE0 | (cp >> 12));
    out[1] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
    out[2] = (unsigned char)(0x80 | (cp & 0x3F));
    return 3;
  }
  out[0] = (unsigned char)(0xF0 | (cp >> 18));
  out[1] = (unsigned char)(0x80 | ((cp >> 12) & 0x3F));
  out[2] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
  out[3] = (unsigned char)(0x80 | (cp & 0x3F));
  return 4;
}

static void o_str_cons_refuse(OVal *cp, const char *why) {
  fprintf(stderr, "oath: this backend cannot encode Str element %s (%s): Str is packed as "
                  "UTF-8, which encodes only Unicode scalar values. Refusing rather than "
                  "substituting U+FFFD, which would make distinct Str values identical.\n",
          o_int_decimal(cp), why);
  o_refused();
}

/* BUILDING A Str AT RUNTIME. This is SCons for a chain the compiler could not
   fold, and it is where PACK happens for everything the compile-time check
   could not see.

   REFUSAL, NEVER SUBSTITUTION, and each class named separately: string(rune(n))
   in the other backend once turned -1, 55296 and 1114112 into the same three
   bytes, so three distinct values had one encoding and the constructor had no
   inverse. The classes are split so three refused inputs stay three
   distinguishable messages.

   SPEC 3 calls (SCons -1 (SNil)) an ordinary value and forbids a KERNEL from
   rejecting it; the same paragraph permits a backend that stores a Str as packed
   UTF-8 to perform PACK at construction and refuse there. The interpreter
   remains the reference, and its disagreement here is the honest subset boundary
   rather than a divergence.

   A FRESH BUFFER, prefix plus a COPY of the tail. The copy is not an
   optimization left on the table: o_str_tail hands out a VIEW into its parent,
   so a Str value may be a window on a buffer someone else's value also spans,
   and a constructed Str must own contiguous bytes of its own. Every buffer here
   is immutable and nothing frees within a request, so both the new buffer and
   any view into the old one stay valid until the request's arena is released -
   which happens after the answer has been serialised, so no observation of
   either can outlive it. The trailing NUL is for o_cstr,
   which hands a C string to a capability; slen is what carries the real length,
   because a Str may contain U+0000.

   O(n) per cons and so O(n^2) to build a string one element at a time. That is
   the same cost the Go backend pays for its own prepend-and-concat and is a
   representation choice this backend is free to revisit; it is not a semantic
   commitment. */
OVal *o_str_cons(OVal *cpv, OVal *rest) {
  o_int_arg(cpv);
  if (cpv->isign < 0) o_str_cons_refuse(cpv, "negative");
  if (cpv->ilen > 1) o_str_cons_refuse(cpv, "above the maximum scalar 0x10FFFF");
  long long cp = cpv->ilen == 0 ? 0 : (long long)cpv->imag[0];
  if (cp > 0x10FFFF) o_str_cons_refuse(cpv, "above the maximum scalar 0x10FFFF");
  if (cp >= 0xD800 && cp <= 0xDFFF) o_str_cons_refuse(cpv, "a surrogate, 0xD800..0xDFFF");
  if (!rest || rest->tag != T_STR) o_bug("the tail of a Str constructor is not a Str");
  unsigned char enc[4];
  int k = o_utf8_encode(cp, enc);
  int n = rest->slen;
  char *buf = (char *)xalloc((size_t)k + (size_t)n + 1);
  memcpy(buf, enc, (size_t)k);
  if (n > 0) memcpy(buf + k, rest->s, (size_t)n);
  return o_strn(buf, k + n);
}

/* ADDITION AND SUBTRACTION ARE ONE OPERATION. bs is +1 for a + b and -1 for
   a - b, so subtraction is addition against a flipped operand sign and there is
   no second implementation of the carry/borrow reasoning to keep in step.

   The four cases below are what sign-magnitude costs, and each is decided by
   COMPARING MAGNITUDES rather than by guessing the result's sign: like signs
   add, unlike signs subtract the smaller from the larger and take the larger's
   sign, and equal magnitudes with unlike signs are exactly zero. That last one
   is the case a two-s complement implementation gets for free and this one has
   to name - it is also the only place a NEGATIVE ZERO could be created, which
   o_int_wrap then forbids. */
static OVal *o_int_addsub(OVal *a, OVal *b, int bs) {
  int sa = o_int_arg(a)->isign, sb = bs * o_int_arg(b)->isign;
  int n;
  if (sa == 0) return o_int_wrap(sb, b->imag, b->ilen);
  if (sb == 0) return o_int_wrap(sa, a->imag, a->ilen);
  if (sa == sb) {
    o_u32 *r = o_magadd(a->imag, a->ilen, b->imag, b->ilen, &n);
    return o_int_wrap(sa, r, n);
  }
  int c = o_magcmp(a->imag, a->ilen, b->imag, b->ilen);
  if (c == 0) return o_int_wrap(0, 0, 0);
  if (c > 0) {
    o_u32 *r = o_magsub(a->imag, a->ilen, b->imag, b->ilen, &n);
    return o_int_wrap(sa, r, n);
  }
  o_u32 *r = o_magsub(b->imag, b->ilen, a->imag, a->ilen, &n);
  return o_int_wrap(sb, r, n);
}

OVal *o_int_add(OVal *a, OVal *b) { return o_int_addsub(a, b, 1); }
OVal *o_int_sub(OVal *a, OVal *b) { return o_int_addsub(a, b, -1); }

/* UNARY NEGATION. It flips the sign and SHARES the magnitude, which is safe for
   the reason o_int_wrap states: values are immutable and nothing here frees, so
   two Ints pointing at one limb array cannot observe each other.

   It routes through the wrapper rather than assigning isign directly, which is
   what keeps zero out of trouble: -0 is 0 in C, but it is the wrapper that
   decides isign is 0 exactly when the value is, and a hand-filled T_INT here
   would be a second place that had to know that. */
OVal *o_int_neg(OVal *a) {
  return o_int_wrap(-o_int_arg(a)->isign, a->imag, a->ilen);
}

OVal *o_int_mul(OVal *a, OVal *b) {
  int n;
  o_u32 *r = o_magmul(o_int_arg(a)->imag, a->ilen, o_int_arg(b)->imag, b->ilen, &n);
  return o_int_wrap(a->isign * b->isign, r, n);
}

/* THE ONE ORDERING PRIMITIVE. ==, < and <= are all derived from it below rather
   than implemented three times: a hand-written equality that compared only the
   fields its author remembered is exactly the incompleteness this project keeps
   finding, and there is no second notion of "same Int" to drift from this one. */
int o_int_cmp(OVal *a, OVal *b) {
  int sa = o_int_arg(a)->isign, sb = o_int_arg(b)->isign;
  if (sa != sb) return sa < sb ? -1 : 1;
  if (sa == 0) return 0;
  int c = o_magcmp(a->imag, a->ilen, b->imag, b->ilen);
  return sa > 0 ? c : -c;
}

int o_int_eq(OVal *a, OVal *b) { return o_int_cmp(a, b) == 0; }
int o_int_lt(OVal *a, OVal *b) { return o_int_cmp(a, b) < 0; }
int o_int_le(OVal *a, OVal *b) { return o_int_cmp(a, b) <= 0; }

/* ---------- division ----------

   ONE CORE, TWO OPERATIONS. '/' and '%' differ only in which half of the same
   result they return and which sign they attach to it, so computing them apart
   would be two implementations of one fact - and the fact they share is exactly
   the one that must hold: a == b*q + r. A separate remainder routine could
   satisfy its own tests while disagreeing with the quotient beside it.

   BINARY LONG DIVISION, one bit at a time, deliberately. Knuth's algorithm D is
   the fast way and its quotient-estimate correction step is the part that gets
   written wrong; this shifts, compares and subtracts, which is slow and has
   nothing to estimate. The backend's stated position is that this is a correct
   representation and not a fast one, and division is where that position costs
   the most - so it is worth saying plainly rather than discovering later.

   The invariant is r < b at the top of every iteration, which is what bounds the
   accumulator: doubling it and setting one bit leaves r < 2b, so nb+1 limbs
   always suffice and the shift can never carry out of the top. */
static void o_magdivmod(const o_u32 *a, int na, const o_u32 *b, int nb,
                        o_u32 **qp, int *nq, o_u32 **rp, int *nr) {
  if (o_magcmp(a, na, b, nb) < 0) {
    /* |a| < |b|: the quotient is 0 and the remainder is the dividend. Handled
       first because the loop below would be correct but would spend na*32
       iterations discovering it. */
    o_u32 *r = o_magalloc(na);
    for (int i = 0; i < na; i++) r[i] = a[i];
    *qp = o_magalloc(1); (*qp)[0] = 0; *nq = 0;
    *rp = r; *nr = na;
    return;
  }
  int rn = nb + 1;
  o_u32 *q = o_magalloc(na);
  o_u32 *r = o_magalloc(rn);
  for (int i = 0; i < na; i++) q[i] = 0;
  for (int i = 0; i < rn; i++) r[i] = 0;
  for (int i = na * 32 - 1; i >= 0; i--) {
    o_u32 carry = 0;
    for (int k = 0; k < rn; k++) {
      o_u32 next = r[k] >> 31;
      r[k] = (r[k] << 1) | carry;
      carry = next;
    }
    r[0] |= (a[i >> 5] >> (i & 31)) & 1u;
    if (o_magcmp(r, o_magnorm(r, rn), b, nb) >= 0) {
      o_magsubfrom(r, rn, b, nb);
      q[i >> 5] |= 1u << (i & 31);
    }
  }
  *qp = q; *nq = na;
  *rp = r; *nr = rn;
}

/* The phrasing matches 'oath eval' - "division by zero" and "modulo by zero" -
   because the CONDITION is a fact about the language and not about this host.
   The framing around it differs, and by less than it did: the interpreter
   reports an error and exits 1; both COMPILED backends now write one line and
   exit 70, this one because it always did and the Go one since the #167 work gave
   its emitted runtime a single refusal door instead of panicking out of
   big.Int at status 2. All three fail and all three name the same thing.

   The remaining asymmetry is prose, not status: this runtime spells out why a
   zero divisor has no answer and the Go backend's line stops at the condition.
   That is #164-shaped work on the message, deliberately not folded in here. */
static void o_int_divzero(const char *what) {
  fprintf(stderr, "oath: %s: the divisor evaluated to 0, which has no quotient in Z. "
                  "Refusing rather than answering.\n", what);
  o_refused();
}

/* Sign, and it is the whole of what distinguishes these from the magnitudes.

   TRUNCATED toward zero, remainder taking the DIVIDEND's sign - which is what
   'oath eval' does, measured rather than assumed: (/ -7 2) is -3 and (% -7 2)
   is -1, so the pair rounds toward zero rather than toward negative infinity.
   Magnitude division truncates by construction, so the quotient needs only the
   product of the signs and the remainder needs only the dividend's; there is no
   correction step, which is precisely why floor division would need one. */
OVal *o_int_div(OVal *a, OVal *b) {
  if (o_int_arg(b)->isign == 0) o_int_divzero("division by zero");
  if (o_int_arg(a)->isign == 0) return o_int_wrap(0, 0, 0);
  o_u32 *q, *r; int nq, nr;
  o_magdivmod(a->imag, a->ilen, b->imag, b->ilen, &q, &nq, &r, &nr);
  return o_int_wrap(a->isign * b->isign, q, nq);
}

OVal *o_int_mod(OVal *a, OVal *b) {
  if (o_int_arg(b)->isign == 0) o_int_divzero("modulo by zero");
  if (o_int_arg(a)->isign == 0) return o_int_wrap(0, 0, 0);
  o_u32 *q, *r; int nq, nr;
  o_magdivmod(a->imag, a->ilen, b->imag, b->ilen, &q, &nq, &r, &nr);
  return o_int_wrap(a->isign, r, nr);
}

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
  if (!v || v->tag != T_CTOR || i >= v->n) o_bug("bad field access");
  return v->f[i];
}

/* ---------- the crypto boundary (#78), HAND-WRITTEN ----------

   THE TWO BACKENDS SATISFY ONE CONTRACT BY OPPOSITE MEANS, AND THAT IS THE
   ARRANGEMENT RATHER THAN AN INCONSISTENCY. compile.go lowers hmac-sha256 to
   crypto/hmac and bytes-eq-ct to crypto/subtle, commented "the trusted crypto
   boundary, compiled to the host's library" - the Go backend TRUSTS the host.
   This backend cannot: it emits textual IR, hands it to clang, and links nothing
   but libc. That zero-dependency property is what makes "two independent
   backends" a fact about the artifacts instead of a packaging detail, so SHA-256,
   HMAC and the constant-time compare are written out here. A primitive names an
   OPERATION and a backend lowers it; nothing obliges two backends to lower it the
   same way, exactly as nothing obliges them to supply a capability KIND the same
   way (#114).

   CORRECTNESS IS CHECKED AGAINST PUBLISHED ANSWERS, NOT AGAINST THE OTHER
   BACKEND. llvm_crypto_test.go runs the FIPS 180-2 SHA-256 examples against this
   compress function directly and the RFC 4231 HMAC-SHA256 vectors through the
   language. Two implementations of a fixed algorithm agreeing with each other is
   weaker evidence than either agreeing with the number a standards body printed,
   and the printed number is free. */

typedef struct OSha OSha;
struct OSha { o_u32 h[8]; unsigned char blk[64]; int used; o_u64 total; };

static o_u32 o_rotr32(o_u32 x, int n) { return (x >> n) | (x << (32 - n)); }

/* SHA-256's compression function, FIPS 180-4 6.2.2.

   THE ROUND CONSTANTS ARE AN AUTOMATIC LOCAL, NOT A FILE-SCOPE TABLE, and that
   is #165's no-static-storage rule rather than a style choice: the emitted
   runtime declares no object of static storage duration outside three argued
   exemptions, and llvm_runtime_state_test.go fails on a fourth whether or not it
   is const. Asking for an exemption would cost that argument to save a table.

   AND NOTHING HERE DEPENDS ON WHICH WAY A COMPILER TAKES IT. The constants are
   read-only and their address does not escape, so a compiler may keep them in
   .rodata and reference them in place - clang -O1 does - and even a per-call
   copy of 256 bytes would be immaterial beside the 64 rounds below. Stated as a
   cost bound rather than as a claim about an implementation, because a claim
   about an implementation would need a gate and would rot without one. */
static void o_sha_compress(o_u32 *h, const unsigned char *p) {
  const o_u32 k[64] = {
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u,
    0x3956c25bu, 0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u,
    0xd807aa98u, 0x12835b01u, 0x243185beu, 0x550c7dc3u,
    0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u, 0xc19bf174u,
    0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
    0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau,
    0x983e5152u, 0xa831c66du, 0xb00327c8u, 0xbf597fc7u,
    0xc6e00bf3u, 0xd5a79147u, 0x06ca6351u, 0x14292967u,
    0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu, 0x53380d13u,
    0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
    0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u,
    0xd192e819u, 0xd6990624u, 0xf40e3585u, 0x106aa070u,
    0x19a4c116u, 0x1e376c08u, 0x2748774cu, 0x34b0bcb5u,
    0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu, 0x682e6ff3u,
    0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u };
  o_u32 w[64];
  for (int i = 0; i < 16; i++)
    w[i] = ((o_u32)p[4 * i] << 24) | ((o_u32)p[4 * i + 1] << 16) |
           ((o_u32)p[4 * i + 2] << 8) | (o_u32)p[4 * i + 3];
  for (int i = 16; i < 64; i++) {
    o_u32 s0 = o_rotr32(w[i - 15], 7) ^ o_rotr32(w[i - 15], 18) ^ (w[i - 15] >> 3);
    o_u32 s1 = o_rotr32(w[i - 2], 17) ^ o_rotr32(w[i - 2], 19) ^ (w[i - 2] >> 10);
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }
  o_u32 a = h[0], b = h[1], c = h[2], d = h[3];
  o_u32 e = h[4], f = h[5], g = h[6], hh = h[7];
  for (int i = 0; i < 64; i++) {
    o_u32 S1 = o_rotr32(e, 6) ^ o_rotr32(e, 11) ^ o_rotr32(e, 25);
    o_u32 ch = (e & f) ^ ((~e) & g);
    o_u32 t1 = hh + S1 + ch + k[i] + w[i];
    o_u32 S0 = o_rotr32(a, 2) ^ o_rotr32(a, 13) ^ o_rotr32(a, 22);
    o_u32 maj = (a & b) ^ (a & c) ^ (b & c);
    o_u32 t2 = S0 + maj;
    hh = g; g = f; f = e; e = d + t1;
    d = c; c = b; b = a; a = t1 + t2;
  }
  h[0] += a; h[1] += b; h[2] += c; h[3] += d;
  h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
}

static void o_sha_init(OSha *s) {
  const o_u32 iv[8] = { 0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
                        0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u };
  for (int i = 0; i < 8; i++) s->h[i] = iv[i];
  s->used = 0;
  s->total = 0;
}

/* STREAMING, BECAUSE HMAC NEEDS IT. The inner hash is over ipad || msg with the
   two in different buffers, and concatenating them would mean allocating a copy
   of every message this runtime ever authenticates. */
static void o_sha_update(OSha *s, const unsigned char *p, size_t n) {
  s->total += (o_u64)n;
  while (n > 0) {
    size_t take = (size_t)(64 - s->used);
    if (take > n) take = n;
    memcpy(s->blk + s->used, p, take);
    s->used += (int)take;
    p += take;
    n -= take;
    if (s->used == 64) { o_sha_compress(s->h, s->blk); s->used = 0; }
  }
}

/* FIPS 180-4 5.1.1: append 0x80, then zeros until the block has 56 bytes, then
   the length in BITS as a 64-bit big-endian word.

   The bit count is captured BEFORE any padding is appended, because the padding
   goes through the same update path and would otherwise be counted as message.
   That is the whole of the 55/56/64-byte boundary this function is tested at:
   at 55 the length still fits the block, at 56 it does not and a second block is
   compressed, and at 64 the message ends exactly on a block edge and the padding
   block is entirely padding. */
static void o_sha_final(OSha *s, unsigned char *out) {
  o_u64 bits = s->total * (o_u64)8;
  unsigned char pad = 0x80, zero = 0, len[8];
  o_sha_update(s, &pad, 1);
  while (s->used != 56) o_sha_update(s, &zero, 1);
  for (int i = 0; i < 8; i++) len[i] = (unsigned char)(bits >> (56 - 8 * i));
  o_sha_update(s, len, 8);
  for (int i = 0; i < 8; i++) {
    out[4 * i]     = (unsigned char)(s->h[i] >> 24);
    out[4 * i + 1] = (unsigned char)(s->h[i] >> 16);
    out[4 * i + 2] = (unsigned char)(s->h[i] >> 8);
    out[4 * i + 3] = (unsigned char)(s->h[i]);
  }
}

static void o_sha256(const unsigned char *p, size_t n, unsigned char *out) {
  OSha s;
  o_sha_init(&s);
  o_sha_update(&s, p, n);
  o_sha_final(&s, out);
}

/* HMAC-SHA256, RFC 2104 with SHA-256 as the hash - which is what SPEC 1 names.

   A key LONGER than the 64-byte block is replaced by its own digest and then
   zero-padded, which is the clause RFC 4231's test cases 6 and 7 exist to
   exercise and the one an implementation is most likely to get wrong, because
   every shorter key takes the other path. */
static void o_hmac_sha256_raw(const unsigned char *key, size_t klen,
                              const unsigned char *msg, size_t mlen,
                              unsigned char *out) {
  unsigned char k[64], ipad[64], opad[64], inner[32];
  OSha s;
  memset(k, 0, sizeof k);
  if (klen > 64) o_sha256(key, klen, k);
  else if (klen > 0) memcpy(k, key, klen);
  for (int i = 0; i < 64; i++) {
    ipad[i] = (unsigned char)(k[i] ^ 0x36);
    opad[i] = (unsigned char)(k[i] ^ 0x5c);
  }
  o_sha_init(&s);
  o_sha_update(&s, ipad, 64);
  o_sha_update(&s, msg, mlen);
  o_sha_final(&s, inner);
  o_sha_init(&s);
  o_sha_update(&s, opad, 64);
  o_sha_update(&s, inner, 32);
  o_sha_final(&s, out);
}

/* ---------- (List Int) as octets ----------

   SPEC 1: an element outside 0..255 is a RUNTIME ERROR, and a kernel MUST NOT
   truncate or reduce modulo 256 - a digest over silently altered input would
   verify against a message nobody sent. So this refuses, with the message the Go
   backend's oBytes prints, and it refuses BEFORE any length is compared: the
   reference converts argument 0 in full, then argument 1 in full, and only then
   compares, so a bad element in either operand errors even when the lengths
   already differ. Measured against oath eval rather than assumed. */
static void o_byte_range_refuse(void) {
  fputs("oath: byte list element out of range 0..255\n", stderr);
  o_refused();
}

static int o_byte_list_len(OVal *v, int cons_idx) {
  int n = 0;
  for (OVal *cur = v; cur && cur->tag == T_CTOR && cur->idx == cons_idx; cur = cur->f[1]) {
    if (cur->n != 2) o_bug("a byte list Cons does not carry two fields");
    n++;
  }
  return n;
}

/* TWO WALKS, one to size the buffer and one to fill it. Values are immutable, so
   the second walk sees exactly what the first counted, and the arena carves
   forward with no realloc to grow into. */
static unsigned char *o_byte_list(OVal *v, int cons_idx, int *nout) {
  int n = o_byte_list_len(v, cons_idx);
  unsigned char *out = (unsigned char *)xalloc((size_t)(n > 0 ? n : 1));
  int i = 0;
  for (OVal *cur = v; cur && cur->tag == T_CTOR && cur->idx == cons_idx; cur = cur->f[1]) {
    OVal *el = cur->f[0];
    /* TWO CONDITIONS, TWO CLASSES, exactly as the Go backend separates them: a
       (List Int) whose element is not an Int is a broken representation and so a
       compiler bug, while the RANGE is something a well-typed program reaches. */
    if (!el || el->tag != T_INT) o_bug("a byte list element is not an Int");
    if (el->isign < 0 || el->ilen > 1 || (el->ilen == 1 && el->imag[0] > 255u))
      o_byte_range_refuse();
    out[i++] = (unsigned char)(el->ilen == 0 ? 0u : el->imag[0]);
  }
  *nout = n;
  return out;
}

/* bytes-eq-ct: EQUALITY THAT EXAMINES EVERY BYTE (SPEC 1, #78).

   THE EVIDENCE FOR THIS FUNCTION IS AN ARGUMENT ABOUT THE CODE, NOT A TEST, and
   the difference is the reason the comment is this long. For every input, this
   returns what memcmp returns; only the TIMING differs. So no value test
   discriminates a leaking lowering from this one - the three-way gate, the
   differential ratchet and every property in the corpus would all stay green
   over a memcmp. Timing here is ARGUED, and it has not been measured.

   WHAT THE CODE DOES

     LENGTH IS NOT SECRET, so a mismatch returns at once without reading a byte.
     That is the reference's behaviour, derived rather than assumed: oath eval
     routes this through subtle.ConstantTimeCompare, which answers 0 for
     differing lengths, and SPEC 1 makes differing lengths compare unequal rather
     than error. The corpus depends on it - malformed hex decodes to the empty
     list, which must simply not match a 32-byte digest.

     For equal lengths every byte is read, in index order, and the difference is
     OR-ed into one accumulator. No early exit, no data-dependent branch, and no
     memory access indexed by a byte's VALUE, so neither the branch predictor nor
     the cache is told anything about the secret.

     The reduction is ARITHMETIC, not a comparison: for an accumulator d in
     0..255, (d - 1) >> 8 has bit 0 set exactly when d is zero. Spelled d == 0 a
     compiler is free to emit a branch; spelled this way there is nothing to
     branch on.

   WHY THE ACCUMULATOR IS volatile. Without it a compiler may recognise the loop
   as memcmp and call it - LLVM does exactly this class of idiom rewrite - and
   libc memcmp stops at the first differing byte. A volatile object's reads and
   writes are side effects the abstract machine requires to happen, in order, so
   the loop cannot be replaced or exited early. The cost is one store per byte,
   over a 32-byte digest.

   WHAT WOULD INVALIDATE IT

     Dropping volatile, or a later edit that accumulates into a plain local and
     reduces it with != 0.

     VECTORISING IS FINE and an EARLY EXIT IS NOT: the work must stay
     input-independent, not stay scalar. volatile is what forbids the exit, so a
     toolchain or flag combination that weakens volatile semantics reopens this.

     THE CONVERSION ABOVE IS NOT COVERED AND IS NOT CONSTANT TIME. o_byte_list
     walks a cons list and allocates, so the call as a whole takes time
     proportional to each operand's LENGTH. Length is public and the walk's cost
     does not depend on the byte VALUES, so the secret is still not leaked - but
     the honest claim is about the comparison, not about the call.

     A SECRET-DEPENDENT LENGTH would break the first premise. In the corpus's use
     the operands are a 32-byte digest and a candidate decoded from a request
     header, and neither length is secret.

     THIS IS A CLAIM ABOUT EMITTED CODE, NOT ABOUT A MACHINE. A core with
     data-dependent memory or arithmetic timing can leak from an instruction
     stream that is constant-time by construction. The code is the layer a
     compiler backend can answer for. */
int o_bytes_eq_ct(OVal *av, OVal *bv, int cons_idx) {
  int na = 0, nb = 0;
  const unsigned char *a = o_byte_list(av, cons_idx, &na);
  const unsigned char *b = o_byte_list(bv, cons_idx, &nb);
  if (na != nb) return 0;
  volatile unsigned char acc = 0;
  for (int i = 0; i < na; i++) acc = (unsigned char)(acc | (unsigned char)(a[i] ^ b[i]));
  o_u32 d = (o_u32)acc;
  return (int)(1u & ((d - 1u) >> 8));
}

/* hmac-sha256: the digest as a (List Int), built tail-first.

   The constructor indices are passed in for the reason o_argv gives: they are a
   fact about the List datatype in THIS store, the compiler knows them, and a
   hardcoded 0 and 1 would be silently wrong for a store whose declaration order
   differs. SPEC 1 types both arguments and the result at the same (List Int), so
   the indices that walk the operands are the ones that build the answer. */
OVal *o_hmac_sha256(OVal *kv, OVal *mv, int nil_idx, int cons_idx) {
  int nk = 0, nm = 0;
  const unsigned char *k = o_byte_list(kv, cons_idx, &nk);
  const unsigned char *m = o_byte_list(mv, cons_idx, &nm);
  unsigned char mac[32];
  o_hmac_sha256_raw(k, (size_t)nk, m, (size_t)nm, mac);
  OVal *acc = o_ctor(nil_idx, 0, o_fields(0));
  for (int i = 31; i >= 0; i--) {
    OVal **f = o_fields(2);
    f[0] = o_int((long long)mac[i]);
    f[1] = acc;
    acc = o_ctor(cons_idx, 2, f);
  }
  return acc;
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
  if (i < 0 || i >= env_len(e)) o_bug("unbound variable");
  return e[i + 1];
}

OVal *o_closure(OCode code, OVal **env) {
  OVal *v = val(T_CLOS); v->code = code; v->env = env; return v;
}
OVal *o_apply(OVal *f, OVal *a) {
  if (!f || f->tag != T_CLOS) o_bug("applied a non-function");
  return f->code(f->env, a);
}

/* argv becomes (List Str), built tail-first. The constructor indices are passed
   in rather than assumed: they are a fact about the List datatype in THIS store,
   and the compiler knows them. */
OVal *o_argv(int argc, char **argv, int nil_idx, int cons_idx) {
  OVal *acc = o_ctor(nil_idx, 0, o_fields(0));
  for (int i = argc - 1; i >= 1; i--) {
    OVal **f = o_fields(2);
    char what[64];
    snprintf(what, sizeof what, "command-line argument %d", i);
    f[0] = o_str_host(argv[i], what);
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

/* ---------- capabilities ----------

   Each provider returns the capability value, or NULL with *err set to why THIS
   host cannot supply it. Provision failure is not an Oath value: o_require exits
   before any Oath code runs. A capability CALL that fails returns the empty
   string, which is the protocol's ordinary failure value. */

static OVal *cap_env_code(OVal **env, OVal *arg) {
  (void)env;
  if (o_has_nul(arg)) return o_str(CAP_FAIL);
  const char *k = o_cstr(arg);
  const char *v = getenv(k);
  if (!v) return o_str(CAP_FAIL);
  char what[160];
  snprintf(what, sizeof what, "environment variable %.120s", k);
  return o_str_host(v, what);
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
  char what[160];
  snprintf(what, sizeof what, "the contents of %.120s", o_cstr(arg));
  return o_strn_host(buf, (int)len, what);
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
/* A REQUIRED VALUE (#126). Not an authority: it grants nothing, and the program
   cannot observe its absence as a value because the program does not start.

   MISSING AND EMPTY ARE DISTINGUISHED and both exit 70. They are different
   operator mistakes — never set, versus set to nothing — and collapsing them is
   the same defect as answering "" for an absent capability. */
OVal *o_require_value(const char *field, const char *kind, const char *envvar) {
  const char *v = getenv(envvar);
  if (!v) {
    fprintf(stderr, "oath: this host cannot provide required capability %s (%s): "
                    "required value %s is not provided; set %s\n",
            field, kind, field, envvar);
    o_refused();
  }
  if (!*v) {
    fprintf(stderr, "oath: this host cannot provide required capability %s (%s): "
                    "required value %s is provided but empty (%s)\n",
            field, kind, field, envvar);
    o_refused();
  }
  /* A required value is ADMITted like any other external octets (#133), and
     refuses through the PROVISION channel: this runs before the entry point, so
     a malformed value stops the program from starting rather than aborting it
     later. Missing, empty and malformed are three ways the host failed to supply
     it, and they read the same way to an operator. */
  if (!o_utf8_valid(v, (int)strlen(v))) {
    fprintf(stderr, "oath: this host cannot provide required capability %s (%s): "
                    "required value %s is not valid UTF-8, so it has no Str value (%s); "
                    "refusing rather than substituting U+FFFD\n",
            field, kind, field, envvar);
    o_refused();
  }
  return o_str(v);
}

OVal *o_require(OVal *(*provide)(char **), const char *field, const char *kind) {
  char *err = NULL;
  OVal *v = provide(&err);
  if (!v) {
    fprintf(stderr, "oath: this host cannot provide required capability %s (%s): %s\n",
            field, kind, err ? err : "unavailable");
    o_refused();
  }
  return v;
}

#if !defined(_WIN32)

/* ---------- SPEC 14: the handler protocol ----------

   WHAT THIS IS. A handler entry is (-> Request Response): a PURE FUNCTION from
   a request VALUE to a response value. Everything between a socket and that
   value is this adapter, and SPEC 14.2's table is what it must produce - one
   total transformation in which every distinction the transport can supply has
   exactly one disposition.

   DEPENDENCY-FREE, like the rest of this runtime. IR is emitted as text and
   clang is invoked; nothing links a server library, so HTTP/1.1 is spoken here
   in about the space the Go backend spends configuring net/http. The cost is
   real and is stated rather than hidden: this speaks HTTP/1.1 ONLY, serves one
   connection at a time, and answers Connection: close to every request.

   WHY THE LOOP IS HERE AND NOT IN THE EMITTED IR. Everywhere else in this
   backend the emitter decides the shape of main, and the CLI entry places its
   own arena release for exactly that reason. A handler cannot: SPEC 14.2
   requires a runtime refusal to become a 500 rather than an exit, which in C
   means the refusal must unwind to a frame that is STILL LIVE while the handler
   runs - and a setjmp whose frame has returned is undefined behaviour, so that
   frame cannot be a function the emitter calls and returns from. The loop is
   the frame. What the emitter still owns is the neutral description: the entry
   to call and the constructor indices to build with.

   WHERE THE ARENA IS RELEASED, WHICH IS THE SAME ARGUMENT ONE PROTOCOL OVER.
   The CLI entry releases after o_print, because that is where the answer
   becomes octets. A request's answer becomes octets in o_http_respond, so the
   release is the statement after it - never before, or the response is
   serialised from freed memory. Every path out of a request reaches exactly one
   release: the answered path, the refused-with-400 path, and the
   refusal-unwound-to-500 path. */

#define O_HTTP_HDRMAX  65536
/* THE BODY LIMIT IS A LIMIT ON THE VALUE, NOT ON THE WIRE, and it is DERIVED
   rather than picked. A body becomes (List Int): every octet costs a boxed Int
   (an OVal plus a magnitude), a two-pointer field array, and a boxed Cons -
   about 190 arena bytes per octet on a 64-bit target. So the wire limit and the
   memory it commits differ by more than two orders of magnitude, and a limit
   written against the wire is not a limit on anything that matters: 8 MiB of
   octets is roughly 1.5 GiB of arena, which a small host answers by killing the
   process, which is a remote party ending the server with one legal request.
   1 MiB is about 190 MiB of arena. Raising it is a REPRESENTATION change - a
   compact body would move this by the same factor - and not a constant to
   nudge. */
#define O_HTTP_BODYMAX (1024 * 1024)
#define O_HTTP_LINEMAX 8192
#define O_HTTP_IOSECS  15
#define O_HTTP_REQSECS 30
#define O_HTTP_BACKLOG 64
#define O_HTTP_BUFMAX  (1024 * 1024 * 1024)

/* A growable byte buffer in the REQUEST ARENA. Nothing here frees; the whole
   region goes at the release point, which is what makes an unwound refusal
   leak-free without any cleanup on the unwinding path. */
typedef struct OBuf OBuf;
struct OBuf {
  char *p;
  int n;
  int cap;
};

static void o_buf_put(OBuf *b, const char *s, int n) {
  if (n <= 0) return;
  /* AN OVERFLOW GUARD, NOT A POLICY LIMIT, and the distinction is what stops a
     remote party from ending the server. o_oom EXITS - it is a host-resource
     failure with nothing left to serve - so a size POLICY enforced here would
     turn an oversized request into a process exit rather than a refusal. The
     policy limits live at the call sites, where each has a status: a header
     section answers 431 and a body answers 413, both per request. What remains
     here is the arithmetic: a length that wrapped would under-allocate, which
     is a heap overflow wearing an out-of-memory costume. */
  if (n > O_HTTP_BUFMAX - b->n) o_oom();
  if (b->n + n > b->cap) {
    int nc = b->cap ? b->cap : 256;
    while (nc < b->n + n) nc *= 2;
    char *np = (char *)xalloc((size_t)nc);
    if (b->n) memcpy(np, b->p, (size_t)b->n);
    b->p = np;
    b->cap = nc;
  }
  memcpy(b->p + b->n, s, (size_t)n);
  b->n += n;
}

/* One field line, AFTER row 8 has lowercased the name and rows 11-12 have
   resolved folding and stripped the surrounding OWS. Values are not
   NUL-terminated: a field value may legally contain no NUL, but the length is
   what this code carries everywhere else and one representation is fewer places
   to disagree. */
typedef struct OField OField;
struct OField {
  char *name;
  int nlen;
  char *value;
  int vlen;
};

/* The PARSER BOUNDARY of SPEC 14.2a, as a value: the six components the
   transformation is a function of, and nothing else. */
typedef struct OReq OReq;
struct OReq {
  char *method;
  int mlen;
  char *target;
  int tlen;
  OField *fs;
  int nf;
  char *body;
  int blen;
  long long at;
};

static int o_ascii_lower(int c) { return (c >= 'A' && c <= 'Z') ? c + 32 : c; }
static int o_ows(int c) { return c == ' ' || c == '\t'; }

/* An RFC 9110 5.6.2 token character. Written as byte values for the
   punctuation, which is the same reason the Go backend spells it that way:
   the set is  ! # $ % & ' * + - . ^ _ backtick | ~  and several of those are
   hazards inside the template this source lives in. */
static int o_is_tchar(int c) {
  if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) return 1;
  switch (c) {
    case 0x21: case 0x23: case 0x24: case 0x25: case 0x26: case 0x27:
    case 0x2A: case 0x2B: case 0x2D: case 0x2E: case 0x5E: case 0x5F:
    case 0x60: case 0x7C: case 0x7E:
      return 1;
  }
  return 0;
}

static int o_is_token(const char *s, int n) {
  if (n <= 0) return 0;
  for (int i = 0; i < n; i++) if (!o_is_tchar((unsigned char)s[i])) return 0;
  return 1;
}

/* SPEC 14.2 row 9, REQ-TEXT-OCTETS-ARE-ASCII. Any octet outside 0x20-0x7E -
   other than HTAB, permitted inside a field VALUE and a lifted authority and
   nowhere else - gives 400 with the handler NOT invoked. Str is a sequence of
   Unicode scalars; for printable US-ASCII the scalar IS the octet, and outside
   it the type cannot represent what arrived. Refusing rather than transcoding
   is the whole point: a repaired value would make a handler verify a signature
   over bytes that never arrived. */
static int o_txt_ok(const char *s, int n, int htab_ok) {
  for (int i = 0; i < n; i++) {
    unsigned char c = (unsigned char)s[i];
    if (c >= 0x20 && c <= 0x7E) continue;
    if (htab_ok && c == 0x09) continue;
    return 0;
  }
  return 1;
}

static int o_field_named(const OField *f, const char *lit) {
  int n = (int)strlen(lit);
  return f->nlen == n && memcmp(f->name, lit, (size_t)n) == 0;
}

/* SPEC 14.2 row 15, REQ-FRAMING-FIELDS-EXCLUDED. These describe the
   TRANSMISSION and are discharged once the body is octets. content-length in
   particular: body is authoritative on how many octets arrived, and trusting a
   field that disagrees with it is what request smuggling is built on. */
static int o_framing_field(const OField *f) {
  return o_field_named(f, "connection") || o_field_named(f, "keep-alive") ||
         o_field_named(f, "proxy-authenticate") || o_field_named(f, "proxy-authorization") ||
         o_field_named(f, "te") || o_field_named(f, "trailer") ||
         o_field_named(f, "transfer-encoding") || o_field_named(f, "upgrade") ||
         o_field_named(f, "content-length");
}

/* -2 IS THE DEADLINE, and it is a different answer from -1 because the two are
   different facts about the peer: one stopped talking, the other is still
   talking and has been doing so for too long.

   AN INACTIVITY TIMEOUT IS NOT A DEADLINE. SO_RCVTIMEO applies to each recv
   INDEPENDENTLY, so a client sending one octet every fourteen seconds resets it
   forever and holds this serial server for as long as it likes - reaching the
   size limits after days, having accepted nothing else in the meantime. The
   socket option stays because it ends a peer that says nothing at all; what
   bounds a peer that says something is this. */
/* A DURATION IS MEASURED WITH A MONOTONIC CLOCK, NOT WITH CIVIL TIME. This
   bounds how long one peer may hold a serial server, and a civil clock is
   ADJUSTABLE: a step backwards during a request extends that hold by the step,
   and a step forwards refuses a request that was inside its budget. Neither is
   a decision anyone made about this server.

   The receipt-time observation is the OPPOSITE case and stays on time(): SPEC
   14.2 row 20 asks for seconds since the Unix epoch, which is civil time by
   definition. Two clocks, two questions - when did this arrive, and how long
   has this been going on.

   MILLISECONDS, because the deadline is compared against itself. Whole seconds
   put a one-second budget and a read that expires after one second on the SAME
   value, so "has the deadline passed" is false at the moment it has - which
   reports an expired read as a dead peer and answers 400 where 408 is true. */
static long long o_now_ms(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (long long)ts.tv_sec * 1000 + (long long)ts.tv_nsec / 1000000;
}

/* ONE PLACE THAT SAYS WHAT A FIELD LINE IS. The header section and the trailer
   section are the same grammar (RFC 9110 6.5), and a second copy is where the
   two would drift - a trailer accepted by rules the headers refuse is a message
   two parsers disagree about. A name is a token, a colon ends it, and the value
   is field-content: VCHAR, SP, HTAB and obs-text, with no other control. */
static int o_http_field_line_ok(const char *ln, int ll) {
  int colon = -1;
  for (int i = 0; i < ll; i++) {
    if (ln[i] == ':') { colon = i; break; }
  }
  if (colon <= 0 || !o_is_token(ln, colon)) return 0;
  for (int i = colon + 1; i < ll; i++) {
    unsigned char c = (unsigned char)ln[i];
    if ((c < 0x20 && c != 0x09) || c == 0x7F) return 0;
  }
  return 1;
}

static int o_recv_some(int fd, long long deadline, char *p, int n) {
  for (;;) {
    long long now = o_now_ms();
    if (deadline && now > deadline) return -2;
    /* THE SOCKET TIMEOUT IS NARROWED TO WHAT IS LEFT, because a check before
       the call does not bound the call. With a budget shorter than the
       inactivity timeout, a peer that sends one octet and then stops would sit
       inside this recv for the full O_HTTP_IOSECS - past the deadline that was
       just checked, and holding a serial server while it does. */
    if (deadline) {
      long long left = deadline - now;
      if (left < (long long)O_HTTP_IOSECS * 1000) {
        if (left < 20) left = 20;
        struct timeval tv;
        tv.tv_sec = (time_t)(left / 1000);
        tv.tv_usec = (left % 1000) * 1000;
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
      }
    }
    ssize_t k = recv(fd, p, (size_t)n, 0);
    if (k < 0 && errno == EINTR) continue;
    /* AN EXPIRED READ IS A TIMEOUT, NOT A DEAD PEER, AND NOT ONLY WHEN THE
       BUDGET HAS RUN OUT. Reporting it as -1 answers 400 - a claim that the
       message was malformed - when what happened is that this backend stopped
       waiting. Both clocks say the same thing about the peer: it stopped
       talking mid-message. The default budget is longer than the inactivity
       timeout, so an unconditional classification is the ordinary case rather
       than the corner one, and making it conditional on the deadline is what
       produced a 400 for it. */
    if (k < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) return -2;
    return (int)k;
  }
}

/* THE WRITE IS BOUNDED FOR THE REASON THE READ IS. SO_SNDTIMEO applies to each
   send INDEPENDENTLY, so a client that reads a few octets and stalls resets it
   every time - each call makes progress, no call ever times out, and this
   serial server is held for as long as the client cares to keep trickling. The
   deadline is absolute and covers the whole response. */
static int o_send_all(int fd, long long deadline, const char *p, int n) {
  int sent = 0;
  while (sent < n) {
    long long now = o_now_ms();
    if (deadline && now > deadline) return 0;
    if (deadline) {
      long long left = deadline - now;
      if (left < (long long)O_HTTP_IOSECS * 1000) {
        if (left < 20) left = 20;
        struct timeval tv;
        tv.tv_sec = (time_t)(left / 1000);
        tv.tv_usec = (left % 1000) * 1000;
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
      }
    }
    ssize_t k = send(fd, p + sent, (size_t)(n - sent), 0);
    if (k < 0 && errno == EINTR) continue;
    if (k <= 0) return 0;
    sent += (int)k;
  }
  return 1;
}

/* Ensure n octets are available from pos. A stream that ends first is SPEC 14.2
   row 24's "a body shorter than its declared length": the message cannot be
   framed, so it is refused rather than delivered in part. Constructing a
   Request from a partial body would hand the handler a message that was never
   fully sent - which a signature over that body would then verify against the
   wrong octets. */
static int o_http_need(int fd, long long deadline, OBuf *b, int pos, int n) {
  char chunk[4096];
  while (b->n - pos < n) {
    int k = o_recv_some(fd, deadline, chunk, (int)sizeof chunk);
    if (k == -2) return 408;
    if (k <= 0) return 400;
    o_buf_put(b, chunk, k);
  }
  return 0;
}

static int o_http_line(int fd, long long deadline, OBuf *b, int *pos, char **line, int *llen) {
  for (;;) {
    for (int i = *pos; i + 1 < b->n; i++) {
      if (b->p[i] == '\r' && b->p[i + 1] == '\n') {
        /* THE LIMIT IS CHECKED WHERE THE LINE IS ACCEPTED, not only where more
           octets are requested. A read returns up to a whole chunk, so a line
           just under the bound can come back with its CRLF well past it - and
           the loop below would never run again, because the terminator is
           already buffered. A bound that only guards the growth path is not a
           bound on the result. */
        if (i - *pos > O_HTTP_LINEMAX) return 400;
        *line = b->p + *pos;
        *llen = i - *pos;
        *pos = i + 2;
        return 0;
      }
    }
    if (b->n - *pos > O_HTTP_LINEMAX) return 400;
    char chunk[2048];
    int k = o_recv_some(fd, deadline, chunk, (int)sizeof chunk);
    if (k == -2) return 408;
    if (k <= 0) return 400;
    o_buf_put(b, chunk, k);
  }
}

/* SPEC 14.2 row 19: a transfer coding is DISCARDED, discharged once body is
   octets - which requires actually decoding it. Refusing a chunked request
   would refuse a legal message, and answering 400 to a message this backend
   simply cannot read would report a fact about the tool as a fact about the
   request. Row 17's trailer section is read and dropped. */
static int o_http_chunked(int fd, long long deadline, OBuf *b, int *pos, OBuf *out) {
  int start = *pos;
  for (;;) {
    char *ln;
    int ll;
    /* THE FRAMING IS BOUNDED, not just the DATA. Only the decoded octets land
       in out, so a limit on the body alone leaves the chunk-size lines and
       their CRLFs unbounded - and every one of them is buffered here. The
       receive deadline does not close that: it is per read, and a peer that
       keeps sending resets it forever. */
    if (*pos - start > O_HTTP_BODYMAX + O_HTTP_HDRMAX) return 413;
    int rc = o_http_line(fd, deadline, b, pos, &ln, &ll);
    if (rc) return rc;
    long long sz = 0;
    int digits = 0;
    for (int i = 0; i < ll; i++) {
      int c = (unsigned char)ln[i];
      int v;
      if (c == ';') break;
      if (c >= '0' && c <= '9') v = c - '0';
      else if (c >= 'a' && c <= 'f') v = c - 'a' + 10;
      else if (c >= 'A' && c <= 'F') v = c - 'A' + 10;
      else return 400;
      /* THE BOUND IS TESTED BEFORE THE MULTIPLICATION, not after it. A
         chunk-size line is remote input of unbounded length, and enough hex
         digits overflow a signed long long - which is undefined behaviour, and
         the value that comes out of it can be NEGATIVE, sail past a later
         "greater than the maximum" test and reach the indexing below. A limit
         checked after the arithmetic that breaks it is not a limit. */
      if (sz > (long long)O_HTTP_BODYMAX / 16) return 413;
      sz = sz * 16 + v;
      digits++;
      if (sz > (long long)O_HTTP_BODYMAX) return 413;
    }
    if (digits == 0) return 400;
    if (sz == 0) break;
    if ((long long)out->n + sz > (long long)O_HTTP_BODYMAX) return 413;
    rc = o_http_need(fd, deadline, b, *pos, (int)sz + 2);
    if (rc) return rc;
    if (b->p[*pos + (int)sz] != '\r' || b->p[*pos + (int)sz + 1] != '\n') return 400;
    o_buf_put(out, b->p + *pos, (int)sz);
    *pos += (int)sz + 2;
  }
  /* The trailer section is DISCARDED (row 17), and discarding is not the same
     as ignoring: these octets are still read, still buffered, and a peer can
     send them without ever sending the empty line that ends them. It is
     bounded like the header section it resembles, and refused with the same
     status. */
  int tstart = *pos;
  int tfields = 0;
  for (;;) {
    char *ln;
    int ll;
    if (*pos - tstart > O_HTTP_HDRMAX) return 431;
    int rc = o_http_line(fd, deadline, b, pos, &ln, &ll);
    if (rc) return rc;
    if (ll == 0) return 0;
    /* A FOLD IS LEGAL HERE FOR THE SAME REASON IT IS LEGAL IN THE HEADER
       SECTION, and refusing it was a SPEC 14.0 divergence - the largest family
       in the differential ratchet, thirteen of seventeen. The trailer section
       is field lines, so row 12's obsolete line folding applies to it, and
       net/http reads trailers with the same reader it reads headers with:
       measured, a chunked message ending "0 CRLF X-T: a CRLF SP b CRLF CRLF"
       is served there and was refused here.

       DISCARDED, NOT RECONSTITUTED. Row 17 drops the trailer section entirely,
       so there is no value to continue and nothing to append to - the fold is
       checked for legality and its octets are dropped. The two conditions are
       the header section's, character for character, because agreement is the
       obligation and a second fold rule could only drift from the first: a
       fold with no field line to continue is malformed, and a control octet is
       malformed. Both were measured to be net/http's boundary too, and obs-text
       is accepted by both. */
    if (o_ows((unsigned char)ln[0])) {
      if (tfields == 0) return 400;
      for (int i = 0; i < ll; i++) {
        unsigned char c = (unsigned char)ln[i];
        if ((c < 0x20 && c != 0x09) || c == 0x7F) return 400;
      }
      continue;
    }
    /* DISCARDED IS NOT UNPARSED. A trailer that is not a field line makes the
       MESSAGE malformed. Row 17 says these fields do not become headers; it
       does not say the octets after the body need not be HTTP. */
    if (!o_http_field_line_ok(ln, ll)) return 400;
    tfields++;
    /* AND IT MAY NOT BE A FRAMING FIELD. RFC 9110 6.5.1 forbids these in a
       trailer section - a Content-Length that arrives AFTER the body would be
       describing a message already framed, which is the shape smuggling is
       built on. Row 17 discards trailers; it does not admit ones HTTP does not
       permit.

       AND THIS BACKEND IS ALONE IN REFUSING THEM, WHICH IS A KNOWN 14.0
       DIVERGENCE RATHER THAN A SETTLED RULE. An earlier version of this comment
       claimed "Go refuses it before its handler runs", and that was measured
       and is FALSE: net/http serves a chunked message whose trailer section
       carries Content-Length, Transfer-Encoding, Connection or Host, and serves
       a trailer whose name is not a token. It reads trailers into a map and
       does not police them. So four shapes are refused here and served there.
       The refusal is KEPT because relaxing it is a decision about smuggling
       exposure and not a comment fix, and it is recorded here so the next
       reader meets the measurement instead of the claim that was wrong.
       This family is outside the differential generator's grammar, so the
       ratchet does not see it. */
    OField tf;
    int tcolon = 0;
    while (ln[tcolon] != ':') tcolon++;
    char tname[64];
    if (tcolon < (int)sizeof tname) {
      for (int k = 0; k < tcolon; k++) tname[k] = (char)o_ascii_lower((unsigned char)ln[k]);
      tf.name = tname;
      tf.nlen = tcolon;
      tf.value = ln;
      tf.vlen = 0;
      if (o_framing_field(&tf) || o_field_named(&tf, "host") ||
          o_field_named(&tf, "expect") || o_field_named(&tf, "content-type")) {
        return 400;
      }
    }
  }
}

/* THE PARSER. Produces SPEC 14.2a's six components, or an HTTP status to answer
   with. A negative return means the peer sent nothing at all, which is not a
   request and gets no response.

   The rows discharged HERE are exactly the ones 14.2 marks 'parser': 11 (OWS
   around a value), 12 (obsolete line folding), 17 (trailers), 19 (transfer
   coding), 20 (the receipt time), 21 (the version), 24 (unframeable) and 27 (an
   HTTP/1.1 request with no Host). Everything else is the adapter's, below. */
static int o_target_authority(char *t, int n, char **out, int *olen);
static int o_authority_ok(const char *a, int n);
static int o_target_authority_ok(const char *a, int n);

static int o_http_parse(int fd, long long deadline, OReq *r) {
  OBuf b;
  memset(&b, 0, sizeof b);
  int pos = 0;

  char *ln;
  int ll;
  {
    char chunk[2048];
    int k = o_recv_some(fd, deadline, chunk, (int)sizeof chunk);
    if (k == -2) return 408;
    if (k <= 0) return -1;
    o_buf_put(&b, chunk, k);
  }
  int rc = o_http_line(fd, deadline, &b, &pos, &ln, &ll);
  if (rc) return rc;

  /* THE REQUEST LINE. Exactly two SP, per RFC 9112 3: a target containing a
     space is a malformed request line and not a target with a space in it. */
  int sp1 = -1, sp2 = -1, sps = 0;
  for (int i = 0; i < ll; i++) {
    if (ln[i] != ' ') continue;
    sps++;
    if (sp1 < 0) sp1 = i;
    else if (sp2 < 0) sp2 = i;
  }
  if (sps != 2 || sp1 <= 0 || sp2 <= sp1 + 1 || sp2 + 1 >= ll) return 400;
  if (!o_is_token(ln, sp1)) return 400;
  int vlen = ll - sp2 - 1;
  const char *ver = ln + sp2 + 1;
  /* THE METHOD IS RECORDED BEFORE THE VERSION IS JUDGED, so a 505 for HEAD is
     still bodiless. o_http_status suppresses the body for HEAD by reading
     r->method, and returning 505 before this assignment left it unset — the
     error path then sent a diagnostic body on a HEAD response, breaking the
     rule the same function states about itself. An early return that skips a
     fact the error path needs is the shape to watch for. */
  r->method = ln;
  r->mlen = sp1;
  if (vlen != 8 || memcmp(ver, "HTTP/1.1", 8) != 0) {
    /* SPEC 14.2 row 21 DISCARDS the version, which presumes a version this
       backend speaks. It speaks one. Answering 505 rather than 400 keeps "I do
       not implement this" distinct from "this message is malformed" - the
       tool-versus-world distinction, in a status code. */
    if (vlen > 5 && memcmp(ver, "HTTP/", 5) == 0) return 505;
    return 400;
  }
  r->target = ln + sp1 + 1;
  r->tlen = sp2 - sp1 - 1;
  /* RFC 9112 3.2 gives a request target FOUR forms and no others: origin,
     absolute, authority (CONNECT only) and asterisk. A target that is none of
     them is a malformed request line, which is row 24. Row 2 keeps the target
     VERBATIM; it does not make every octet sequence a target.

     THE ASTERISK FORM IS NOT RESTRICTED TO OPTIONS HERE, AND THAT IS NOT A
     CONCESSION TO THE OTHER BACKEND. RFC 9112 3.2.4 is read as forbidding
     "PUT *" because of its opening sentence - the asterisk form "is only used
     for a server-wide OPTIONS request" - but that sentence DESCRIBES sender
     usage. Every normative MUST in the section binds the CLIENT that sends the
     target or the PROXY that forwards it. None binds a recipient to reject one.

     WE ARE THE RECIPIENT. A constraint on what a client may SEND is not a
     licence for a server to REFUSE what arrives; treating it as one adds
     recipient strictness, which is the exact divergence class this parser keeps
     producing - a rule read off a grammar coming out stricter than the
     reference. There is no server-side conformance cost in either direction, so
     nothing here trades HTTP conformance for agreement, and SPEC 14.0 decides
     it unopposed: net/http parses "PUT *" into a Request with RequestURI "*"
     and runs the handler on it, measured, so this backend must build the same
     Request from the same octets.

     THE RESTRICTION SURVIVED THIS LONG BEHIND AN AGREEMENT FOR THE WRONG
     REASON. The earlier comment justified it with "the Go backend's parser
     refuses it". What actually answered 400 there was net/http's ServeMux
     REDIRECTING an uncleanable path - a routing decision from a layer SPEC 14
     does not describe. Removing the mux (the other backend is now the whole
     server) made the real parser behaviour visible and this branch wrong.

     THE FORM IS STILL EXACTLY ONE ASTERISK. "**", "*x" and "x*" are refused by
     both backends and llvm_http_agreement_test.go holds them there, because
     admitting the asterisk FORM is not admitting anything asterisk-shaped. What
     a handler does with a target of "*" is the handler's decision, and it
     receives the octets verbatim either way. */
  {
    char *ta;
    int tal;
    int form_ok = 0;
    int abs_form = o_target_authority(r->target, r->tlen, &ta, &tal);
    if (abs_form < 0) return 400;
    if (r->target[0] == '/') form_ok = 1;
    else if (abs_form > 0) form_ok = 1;
    else if (r->mlen == 7 && memcmp(ln, "CONNECT", 7) == 0) {
      form_ok = o_target_authority_ok(r->target, r->tlen);
    }
    else if (r->tlen == 1 && r->target[0] == '*') {
      form_ok = 1;
    }
    if (!form_ok) return 400;
    /* ROW 2 KEEPS THE TARGET RAW; IT DOES NOT MAKE EVERY OCTET SEQUENCE A
       TARGET. A percent that is not followed by two hex digits is not an escape
       and the target is not a URI - Go's parser refuses it, so admitting it
       would have the two backends disagree about whether a Request exists, on
       an input that reaches routing and signatures. Not DECODED here: checking
       the escape is well-formed and leaving the octets alone are different
       acts, and row 2 forbids only the second. */
    for (int i = 0; i < r->tlen; i++) {
      if (r->target[i] != '%') continue;
      if (i + 2 >= r->tlen) return 400;
      for (int k = 1; k <= 2; k++) {
        char c = r->target[i + k];
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'))) {
          return 400;
        }
      }
    }
  }

  /* THE FIELD LINES. */
  /* THE ONLY BOUND IS THE BYTE BOUND. A separate cap on the field COUNT
     refused a request the Go backend serves - 257 small field lines, far under
     the header-section limit - and two backends answering differently for one
     message is the single thing SPEC 14.0 asks them not to do. The header
     limit already bounds the count: every field line costs at least a name, a
     colon and a CRLF, so the arrays cannot grow without the octets that pay
     for them. */
  int fcap = 64;
  r->fs = (OField *)xalloc(sizeof(OField) * fcap);
  r->nf = 0;
  OBuf *vals = (OBuf *)xalloc(sizeof(OBuf) * fcap);
  memset(vals, 0, sizeof(OBuf) * fcap);
  /* Where the LAST appended line's content starts, per field. A fold's trailing
     run is the MESSAGE's whitespace, and the SP a previous fold contributed is
     this code's - so the right-trim below must stop here rather than eat it. */
  int *vmark = (int *)xalloc(sizeof(int) * fcap);
  memset(vmark, 0, sizeof(int) * fcap);
  for (;;) {
    rc = o_http_line(fd, deadline, &b, &pos, &ln, &ll);
    if (rc) return rc;
    /* THE HEADER SECTION IS BOUNDED, and it is bounded by OCTETS rather than by
       field count: a continuation line adds no field, so a count alone leaves a
       message that folds forever unbounded - and unbounded here means the
       allocator's exit, which is a remote party ending the server. pos is the
       octets consumed so far, so this is the whole section including folds. */
    if (pos > O_HTTP_HDRMAX) return 431;
    if (ll == 0) break;
    if (o_ows((unsigned char)ln[0])) {
      /* SPEC 14.2 row 12, PROTO-OBS-FOLD-IS-ONE-SPACE. Each fold becomes
         EXACTLY ONE SP, and the whitespace consumed is the run on BOTH sides -
         the trailing run before the CRLF and the leading run after it. A fold
         with no field to continue is a malformed message, not a field. */
      if (r->nf == 0) return 400;
      for (int i = 0; i < ll; i++) {
        unsigned char c = (unsigned char)ln[i];
        if ((c < 0x20 && c != 0x09) || c == 0x7F) return 400;
      }
      OBuf *v = &vals[r->nf - 1];
      /* EACH FOLD BECOMES EXACTLY ONE SP, and CONSECUTIVE folds therefore
         become one SP EACH. The trim stops at the last line's content mark, so
         a fold whose continuation line is empty still contributes its own
         space: a value continued twice, the first continuation carrying nothing
         but whitespace, is TWO folds and yields two spaces - which is what the
         Go backend produces from the same octets. Trimming
         the whole buffer instead ate the previous fold's space and made the two
         backends build different Str values from one message. */
      int floor = vmark[r->nf - 1];
      while (v->n > floor && o_ows((unsigned char)v->p[v->n - 1])) v->n--;
      int i = 0;
      while (i < ll && o_ows((unsigned char)ln[i])) i++;
      o_buf_put(v, " ", 1);
      vmark[r->nf - 1] = v->n;
      o_buf_put(v, ln + i, ll - i);
      continue;
    }
    if (r->nf == fcap) {
      /* Grown together, because the three arrays are one record split three
         ways and an index into any of them means the same field. */
      int nc = fcap * 2;
      OField *nf2 = (OField *)xalloc(sizeof(OField) * nc);
      OBuf *nv = (OBuf *)xalloc(sizeof(OBuf) * nc);
      int *nm = (int *)xalloc(sizeof(int) * nc);
      memset(nv, 0, sizeof(OBuf) * nc);
      memset(nm, 0, sizeof(int) * nc);
      for (int k = 0; k < fcap; k++) {
        nf2[k] = r->fs[k];
        nv[k] = vals[k];
        nm[k] = vmark[k];
      }
      r->fs = nf2;
      vals = nv;
      vmark = nm;
      fcap = nc;
    }
    int colon = -1;
    for (int i = 0; i < ll; i++) {
      if (ln[i] == ':') { colon = i; break; }
    }
    /* SPEC 14.2 row 24, PROTO-UNFRAMEABLE-IS-REFUSED: no colon in a field
       line, and SP or HTAB before the colon - a name ends at the colon and may
       not contain either (RFC 9112 5.1), so 'X-A : v' is malformed rather than
       a field named 'x-a ' with a trailing space. */
    if (colon <= 0) return 400;
    /* THE WHOLE LINE IS HTTP BEFORE ANY OF IT IS OATH, and the two questions
       are different rows. A name is a token (RFC 9112 5.1), which subsumes row
       24's named instance about SP and HTAB before the colon; a value is
       field-content, so a control octet other than HTAB makes the MESSAGE
       malformed - row 24, not row 9.
       THE DISTINCTION DECIDES A CASE SPEC 14.3.2 PINS. Row 9 asks what a Str
       can carry and is applied AFTER exclusions, so an EXCLUDED field's
       obs-text must NOT refuse the request. HTTP syntax is not conditional on
       the value surviving into the Oath value: a control octet is malformed
       whether or not anybody delivers the field, and the Go backend's parser
       refuses the same message. So obs-text passes here and meets row 9 later,
       while a control octet stops here. */
    if (!o_http_field_line_ok(ln, ll)) return 400;
    /* SPEC 14.2 row 8, REQ-HEADER-NAMES-LOWERCASE: ASCII only, A-Z to a-z,
       every other octet unchanged. NOT Unicode case folding, which can change
       a name's LENGTH. */
    char *nm = (char *)xalloc((size_t)colon);
    for (int i = 0; i < colon; i++) nm[i] = (char)o_ascii_lower((unsigned char)ln[i]);
    r->fs[r->nf].name = nm;
    r->fs[r->nf].nlen = colon;
    o_buf_put(&vals[r->nf], ln + colon + 1, ll - colon - 1);
    r->nf++;
  }
  /* SPEC 14.2 row 11: surrounding OWS is removed by the PARSER, on the
     reconstituted value - so it is applied AFTER row 12 rather than to each
     folded line. */
  for (int i = 0; i < r->nf; i++) {
    OBuf *v = &vals[i];
    int a = 0, z = v->n;
    while (a < z && o_ows((unsigned char)v->p[a])) a++;
    while (z > a && o_ows((unsigned char)v->p[z - 1])) z--;
    r->fs[i].value = v->p ? v->p + a : (char *)"";
    r->fs[i].vlen = z - a;
  }

  /* SPEC 14.2 row 20, REQ-TIME-IS-DATA and PROTO-TIME-IS-INTEGRAL. Whole
     seconds since the Unix epoch, taken BEFORE the body is consumed: reading
     first and stamping after would record body-COMPLETION time, which for a
     slow upload differs from receipt by seconds or more. */
  r->at = (long long)time(NULL);

  /* SPEC 14.2 row 27: an HTTP/1.1 request with NO Host field is refused.
     Discharged here, where the version is known, which is the same reason rows
     22-23 sit at the parser and row 21 can discard the version at all. Row 22:
     more than one Host field line is refused, and it is refused BEFORE the
     adapter resolves precedence, because resolving DESTROYS the losing
     candidates. */
  int hosts = 0;
  for (int i = 0; i < r->nf; i++) {
    if (!o_field_named(&r->fs[i], "host")) continue;
    hosts++;
    /* AND ITS VALUE MUST BE AN AUTHORITY. Row 5 lifts this field into the
       value's host entry, so an unvalidated one is nonsense delivered as the
       authority - and Go refuses the same message, so the two backends would
       disagree on exactly the input a handler is most likely to trust. Empty
       is allowed here and handled by row 26, which says present-empty and
       absent are the same outcome. */
    if (r->fs[i].vlen > 0 && !o_authority_ok(r->fs[i].value, r->fs[i].vlen)) return 400;
  }
  if (hosts == 0) return 400;
  if (hosts > 1) return 400;

  /* THE BODY. Transfer-Encoding OVERRIDES Content-Length when both are present
     (RFC 9112 6.3 rule 3), and the message is processed rather than refused.

     6.1 permits either: "A server MAY reject a request that contains both
     Content-Length and Transfer-Encoding or process such a request in
     accordance with the Transfer-Encoding alone." This backend refused, the Go
     backend processes, and BOTH were conformant — but SPEC 14.0 says two
     backends produce the SAME Request from the same octets, and a handler's
     properties are proven against that value, so a disagreement here stops a
     proof transferring between artifacts. Processing was chosen because the
     other backend inherits it from net/http and is the one people arrive with.

     WHAT IS NOT INHERITED IS THE CONNECTION HANDLING. 6.1 continues:
     "Regardless, the server MUST close the connection after responding to such
     a request to avoid the potential attacks." Reuse is the smuggling vector —
     a desynchronised connection carrying a second, attacker-framed request —
     so processing the shape is the MAY and closing after it is the MUST.
     Measured: net/http does neither signal nor close, and the Go backend
     inherited that — a real RFC violation (golang/go#80942). Both backends now
     hold the SAME per-request posture: the Go backend disables keep-alive (#171)
     and this one calls o_http_close on every path, and each answers Connection:
     close, so a connection is per-request by construction and cannot be reused
     after any response — smuggling-shaped or not. This per-request model IS the
     design, not a deferral: keep-alive is an optimization with no measured need
     (#179, closed). Anyone adding it must reproduce the §6.1 close explicitly,
     which needs the request framing net/http hid from a wire sniffer — a conn
     sniff was prototyped and has a confirmed pipelining bypass. That landmine is
     why this guarantee is stated here rather than left implicit. */
  int has_cl = 0, chunked = 0, has_te = 0, cl_too_big = 0;
  int te_codings = 0, te_last_chunked = 0, te_other = 0;
  long long clen = 0;
  const char *cltext = NULL;
  int cltext_len = 0;
  for (int i = 0; i < r->nf; i++) {
    OField *f = &r->fs[i];
    if (o_field_named(f, "content-length")) {
      /* REPEATED AND IDENTICAL IS ONE LENGTH; repeated and DIFFERENT is the
         smuggling shape. RFC 9110 8.6 permits the list form, and net/http
         accepts it - so refusing outright made the Go backend serve a request
         this one refused, which is exactly the disagreement SPEC 14.0 forbids.
         The comparison is on the parsed VALUE, so 5 and 005 agree, as they
         must: they are the same length written twice. */
      if (has_cl) {
        /* IDENTICAL SPELLINGS, not merely equal values. Comparing the parsed
           numbers would accept 5 beside 005, and net/http refuses that pair -
           so the looser rule is the one that breaks backend agreement, which
           is the only reason this case is admitted at all. RFC 9110 8.6's list
           form is about a value repeated, and a repeated value is written the
           same way twice. */
        if (f->vlen != cltext_len || memcmp(f->value, cltext, (size_t)f->vlen) != 0) return 400;
        continue;
      }
      has_cl = 1;
      cltext = f->value;
      cltext_len = f->vlen;
      /* ANY NON-EMPTY RUN OF DIGITS (RFC 9110 8.6), which is not the same as a
         short one: nineteen leading zeroes followed by a 1 is a valid
         Content-Length of 1, and a limit on the TEXT length refuses a frame the
         Go backend accepts. The bound belongs on the VALUE, and it is applied
         before each multiplication so an absurd run of digits cannot overflow
         its way past the test that was supposed to stop it. */
      if (f->vlen == 0) return 400;
      /* THE VERDICT IS DEFERRED, NOT THE MEASUREMENT. A Transfer-Encoding
         discards this field entirely (RFC 9112 6.3 rule 3), so answering 413
         here refused a request the other backend frames from its chunks and
         serves — a 14.0 divergence created by judging a value that was about to
         be thrown away. The overflow guard still runs per digit, because an
         absurd run of digits must not multiply its way past the test; it now
         records the excess instead of returning on it. */
      for (int k = 0; k < f->vlen; k++) {
        if (f->value[k] < '0' || f->value[k] > '9') return 400;
        if (clen > (long long)O_HTTP_BODYMAX) { cl_too_big = 1; break; }
        clen = clen * 10 + (f->value[k] - '0');
      }
      if (clen > (long long)O_HTTP_BODYMAX) cl_too_big = 1;
    } else if (o_field_named(f, "transfer-encoding")) {
      /* SPEC 14.2 row 19 discards the transfer coding, which requires reading
         the WHOLE ordered list rather than looking for one name in it. Two
         separate readings were wrong here and each was wrong in a different
         direction: a substring test made "gzip, chunked" decode as chunked and
         hand the handler compressed octets as its body, and a case-sensitive
         one answered 501 to a legal "Chunked". The list is comma-separated,
         each element is trimmed of OWS, an empty element is ignored, and the
         comparison is after ASCII lowercasing - the same element rules row 16
         applies to a connection option. */
      has_te = 1;
      int a = 0;
      for (int k = 0; k <= f->vlen; k++) {
        if (k < f->vlen && f->value[k] != ',') continue;
        int s0 = a, e0 = k;
        while (s0 < e0 && o_ows((unsigned char)f->value[s0])) s0++;
        while (e0 > s0 && o_ows((unsigned char)f->value[e0 - 1])) e0--;
        a = k + 1;
        /* AN EMPTY ELEMENT IS REFUSED HERE AND IGNORED IN A Connection VALUE,
           and the two loops looking identical is exactly why this is written
           down. Row 16 makes ignoring NORMATIVE for connection options; nothing
           says that about a transfer coding, so the governing rule is HTTP's -
           and net/http refuses these spellings, so ignoring one would decode a
           body the Go backend never delivers. Same shape, different question,
           different disposition. */
        if (e0 <= s0) return 400;
        te_codings++;
        int isch = (e0 - s0 == 7);
        for (int j = 0; isch && j < 7; j++) {
          if (o_ascii_lower((unsigned char)f->value[s0 + j]) != "chunked"[j]) isch = 0;
        }
        te_last_chunked = isch;
        if (!isch) te_other = 1;
      }
    }
  }
  /* TE WINS AND THE CONTENT-LENGTH IS DISCARDED, rather than the two being
     reconciled: 6.3 rule 3 makes the override unconditional, and row 15 already
     says body is authoritative on how many octets arrived. */
  if (has_te && has_cl) {
    has_cl = 0;
    clen = 0;
    cltext = NULL;
    cltext_len = 0;
    cl_too_big = 0; /* discarded, so its size is not a reason to refuse */
  }
  if (cl_too_big) return 413;
  if (has_te) {
    /* RFC 9112 6.1: if chunked is not the FINAL coding of a request, the body
       length cannot be determined reliably and the server MUST answer 400 -
       which is row 24's unframeable message under another name. A coding this
       backend cannot decode is a different answer: the message is framed, the
       tool cannot read it, and 501 says so rather than blaming the request. */
    if (te_codings == 0 || !te_last_chunked) return 400;
    if (te_other) return 501;
    /* RFC 9112 6.1: a sender MUST NOT apply the chunked coding more than once.
       A doubly chunked value passes both tests above - every coding IS chunked
       and the last one is - and decoding it once would hand the handler the
       inner framing as body octets. Refused, not partly decoded. */
    if (te_codings != 1) return 400;
    chunked = 1;
  }
  /* THE EXPECTATION IS THE ADAPTER'S, AND IT IS ANSWERED BEFORE THE BODY IS
     READ. A client sending Expect: 100-continue does not send its body until it
     is told to, so reading first is a deadlock that ends in this server's own
     timeout - a legal request answered 408 while both sides wait for the other.

     THIS IS ALSO THE ONE INTERIM RESPONSE THIS BOUNDARY EMITS, and it is not a
     contradiction of refusing a 1xx from the HANDLER. The two are different
     parties: the adapter knows a final response is still to come because it is
     the one that will send it, and a Response value cannot say that about
     itself. Any OTHER expectation is 417 (RFC 9110 10.1.1) - unknown, so
     refused by name rather than ignored, since ignoring it would have the
     client believe a condition was honoured. */
  int expects = 0;
  for (int i = 0; i < r->nf; i++) {
    OField *f = &r->fs[i];
    if (!o_field_named(f, "expect")) continue;
    int is100 = (f->vlen == 12);
    for (int j = 0; is100 && j < 12; j++) {
      if (o_ascii_lower((unsigned char)f->value[j]) != "100-continue"[j]) is100 = 0;
    }
    /* EVERY Expect FIELD IS INSPECTED BEFORE ANY INTERIM IS SENT. Stopping at
       the first recognised one would send 100 and then proceed as though a
       LATER expectation this backend does not support had been honoured -
       which is the one thing 417 exists to prevent it believing. */
    if (!is100) return 417;
    expects = 1;
  }
  if (expects && !o_send_all(fd, deadline, "HTTP/1.1 100 Continue\r\n\r\n", 25)) return -1;

  OBuf body;
  memset(&body, 0, sizeof body);
  if (chunked) {
    rc = o_http_chunked(fd, deadline, &b, &pos, &body);
    if (rc) return rc;
  } else if (clen > 0) {
    rc = o_http_need(fd, deadline, &b, pos, (int)clen);
    if (rc) return rc;
    o_buf_put(&body, b.p + pos, (int)clen);
    pos += (int)clen;
  }
  r->body = body.p;
  r->blen = body.n;
  return 0;
}

/* SPEC 14.2 row 3, PROTO-TARGET-AUTHORITY. The octets after the scheme's
   colon-slash-slash separator, up to the first slash, query mark, hash, or end
   of target, with any userinfo prefix EXCLUDED.
   Asterisk-form and authority-form lift nothing. The scheme must be a real
   scheme (RFC 3986 3.1) or a network-path reference in an origin-form target
   would be read as an
   authority. */
/* THE IPv6 GRAMMAR, STRUCTURALLY. A character whitelist admits things that are
   not addresses at all - four colons in a row, nine groups, a five-digit group -
   and Go's parser refuses those, so the whitelist was a backend disagreement
   dressed as a validation. The shape is eight groups of one to four hex digits,
   at most ONE elision, and an optional dotted-quad tail standing for the last
   two groups (RFC 3986 3.2.2, RFC 4291 2.2).

   THE WHITELIST WAS NOT TOO PERMISSIVE BY ACCIDENT; IT WAS THE WRONG KIND OF
   CHECK. Each round of narrowing it by character class would have admitted the
   next malformed spelling, because the property is about STRUCTURE and no set
   of legal characters can express it. */
static int o_ipv4_ok(const char *a, int n) {
  int parts = 0, i = 0;
  while (i < n) {
    int v = 0, d = 0;
    while (i < n && a[i] >= '0' && a[i] <= '9') {
      v = v * 10 + (a[i] - '0');
      d++;
      i++;
      if (d > 3) return 0;
    }
    if (d == 0 || v > 255) return 0;
    parts++;
    if (i == n) break;
    if (a[i] != '.') return 0;
    i++;
    if (i == n) return 0;
  }
  return parts == 4;
}

static int o_ipv6_ok(const char *a, int n) {
  if (n == 0) return 0;
  int groups = 0, elision = 0, i = 0;
  if (a[0] == ':') {
    if (n < 2 || a[1] != ':') return 0;
    elision = 1;
    i = 2;
    if (i == n) return 1;
  }
  for (;;) {
    int j = i, dotted = 0;
    while (j < n && a[j] != ':') {
      if (a[j] == '.') dotted = 1;
      j++;
    }
    if (j == i) return 0;
    if (dotted) {
      /* A dotted quad stands for the last two groups and must END the address. */
      if (j != n || !o_ipv4_ok(a + i, j - i)) return 0;
      groups += 2;
      i = j;
      break;
    }
    if (j - i > 4) return 0;
    for (int k = i; k < j; k++) {
      char c = a[k];
      if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'))) return 0;
    }
    groups++;
    i = j;
    if (i == n) break;
    i++;
    if (i < n && a[i] == ':') {
      if (elision) return 0;
      elision = 1;
      i++;
      if (i == n) break;
    } else if (i == n) {
      return 0;
    }
  }
  if (elision) return groups <= 7;
  return groups == 8;
}

/* AN AUTHORITY IS host [ ":" port ], with an optional userinfo already removed
   by the caller (RFC 3986 3.2). Validated rather than assumed, because a target
   that merely BEGINS with a scheme and slashes is not thereby absolute-form -
   and Go's parser refuses the malformed ones, so admitting them here would have
   the two backends disagree about whether a Request exists. */
/* THE TARGET'S AUTHORITY IS VALIDATED STRUCTURALLY, AND THE HOST FIELD IS NOT,
   because that is what the other backend does and SPEC 14.0 asks the two to
   agree. Measured against net/http: an absolute-form target with a non-numeric
   port is refused 400 with the handler never invoked, while a Host field of
   [1::2::3] or [v1.] is delivered unchanged. One validator served both sites
   and made the header case diverge. */
static int o_target_authority_ok(const char *a, int n) {
  if (n <= 0) return 0;
  int i = 0;
  if (a[0] == '[') {
    int close = -1;
    for (int k = 1; k < n; k++) {
      if (a[k] == ']') { close = k; break; }
    }
    if (close < 2) return 0;
    /* AN IP-LITERAL IS IPv6address OR IPvFuture (RFC 3986 3.2.2), and a
       validator that knew only the first refused a host net/http accepts -
       different outcomes for one request, which is the one thing SPEC 14.0
       asks two backends not to do. IPvFuture is v <hex> . <at least one
       unreserved / sub-delim / colon>, so both the version digits and the
       address part must be non-empty. */
    if (a[1] == 'v' || a[1] == 'V') {
      int k = 2, ver = 0;
      while (k < close && ((a[k] >= '0' && a[k] <= '9') || (a[k] >= 'a' && a[k] <= 'f') ||
                           (a[k] >= 'A' && a[k] <= 'F'))) {
        k++;
        ver++;
      }
      if (ver == 0 || k >= close || a[k] != '.') return 0;
      k++;
      if (k >= close) return 0;
      for (; k < close; k++) {
        unsigned char c = (unsigned char)a[k];
        if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
              c == '-' || c == '.' || c == '_' || c == '~' || c == '!' || c == '$' ||
              c == '&' || c == 0x27 || c == '(' || c == ')' || c == '*' || c == '+' ||
              c == ',' || c == ';' || c == '=' || c == ':')) {
        return 0;
        }
      }
    } else if (!o_ipv6_ok(a + 1, close - 1)) {
      return 0;
    }
    i = close + 1;
  } else {
    while (i < n && a[i] != ':') {
      unsigned char c = (unsigned char)a[i];
      if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
            c == '-' || c == '.' || c == '_' || c == '~' || c == '%' || c == '!' ||
            c == '$' || c == '&' || c == 0x27 || c == '(' || c == ')' || c == '*' ||
            c == '+' || c == ',' || c == ';' || c == '=')) {
        return 0;
      }
      i++;
    }
    if (i == 0) return 0;
  }
  if (i == n) return 1;
  if (a[i] != ':') return 0;
  /* An empty port is legal; anything in it must be a digit. */
  for (i++; i < n; i++) {
    if (a[i] < '0' || a[i] > '9') return 0;
  }
  return 1;
}

static int o_authority_ok(const char *a, int n) {
  /* CHARACTER-BASED, BECAUSE THAT IS WHAT THE OTHER BACKEND DOES — and SPEC
     14.0 asks the two to agree on every request, not to be individually
     defensible. Row 5 only LIFTS this field into the value's host entry; it
     asks for no structural validation, so validating structure here is a
     rejection the specification never requested.

     An earlier version parsed the authority per RFC 3986 — IPv6address and
     IPvFuture — and refused hosts net/http accepts, a divergence on exactly
     the field a handler is most likely to trust. Its comment had already been
     corrected once for IPvFuture alone; the class outlived the instance.

     The permitted set was MEASURED against net/http rather than read off a
     grammar: it accepts every printable ASCII byte except these thirteen and
     refuses DEL and above. [1::2::3] and [::::] are nonsense as addresses
     and both backends now deliver them unchanged, which is the agreement the
     three-way gate exists to keep. */
  if (n <= 0) return 0;
  for (int i = 0; i < n; i++) {
    unsigned char c = (unsigned char)a[i];
    if (c <= 0x20 || c >= 0x7f) return 0;
    if (c == 0x22 || c == '#' || c == '/' || c == '<' || c == '>' || c == '?' ||
        c == '@' || c == 0x5c || c == '^' || c == 0x60 || c == '{' || c == '|' ||
        c == '}') {
      return 0;
    }
  }
  return 1;
}

/* Returns 1 for absolute-form with a well-formed authority, -1 for absolute-form
   whose authority is malformed, and 0 for a target that is not absolute-form at
   all. The three are different answers: the middle one is a refusal, and
   collapsing it into "not absolute-form" would let a CONNECT smuggle one
   through the authority-form branch. */
static int o_target_authority(char *t, int n, char **out, int *olen) {
  int i = 0;
  if (n == 0) return 0;
  if (!((t[0] >= 'a' && t[0] <= 'z') || (t[0] >= 'A' && t[0] <= 'Z'))) return 0;
  while (i < n && ((t[i] >= 'a' && t[i] <= 'z') || (t[i] >= 'A' && t[i] <= 'Z') ||
                   (t[i] >= '0' && t[i] <= '9') || t[i] == '+' || t[i] == '-' || t[i] == '.')) i++;
  if (i + 2 >= n || t[i] != ':' || t[i + 1] != '/' || t[i + 2] != '/') return 0;
  int a = i + 3, z = a;
  while (z < n && t[z] != '/' && t[z] != '?' && t[z] != '#') z++;
  /* THE LAST @ ENDS THE USERINFO, not the first. RFC 3986 3.2 makes userinfo
     everything before the final delimiter, and net/http agrees: measured,
     http://u@v@host/path is served with host "host". Stopping at the first left
     "v@host" as the authority, which then failed validation and answered 400 to
     a request the other backend serves — a 14.0 divergence produced by being
     stricter than the reference rather than by disagreeing with the RFC. */
  for (int k = z - 1; k >= a; k--) {
    if (t[k] == '@') { a = k + 1; break; }
  }
  if (!o_target_authority_ok(t + a, z - a)) return -1;
  *out = t + a;
  *olen = z - a;
  return 1;
}

/* THE ADAPTER: SPEC 14.2's transformation, in the order the section makes
   normative.

	 canonicalize names             (row 8, at the parser above)
	 discard transport-only facts   (rows 15-17)
	 refuse conflicting authority   (rows 22-23, at the parser above)
	 lift what arrives elsewhere    (rows 3-5)
	 validate representability      (row 9)
	 canonicalize the remainder     (rows 13-14)
	 construct Request

   Two orderings in that list are load-bearing and each was got wrong in a draft
   of the section itself: DISCARD precedes VALIDATE, or an unrepresentable octet
   in a field nobody delivers refuses a request that could have been served; and
   LIFT precedes VALIDATE, or row 9 cannot inspect the authority it is required
   to cover. */
/* SPEC 14.2 row 16, PROTO-NOMINATION-BY-PRESENCE. The value is a
   comma-separated list of connection OPTIONS: split on the comma, strip SP and
   HTAB from both ends, ignore an empty element, ignore an element that is not a
   token. Compared AFTER row 8's lowercasing, so a nomination of x-hop reaches a
   field line spelled X-Hop. Parsed as OCTETS: HTTP OWS is SP and HTAB only, so a
   value of NBSP + "X-Hop" + NBSP yields no valid nomination and cannot silently
   suppress a real header the handler was meant to see.

   NO BOUND ON THE OPTION COUNT. The first version built an array of nominations
   with a fixed capacity, which silently DROPPED later options once it filled -
   and row 16 says the union across every Connection line, with no bound. A
   capacity is not a disposition: a request under the header limit could carry
   more options than the array held, and the field the last one named would
   reach the handler. The index below is grown from the message instead, so it
   is exactly the union and cannot be short by one.

   IT IS AN INDEX RATHER THAN A RESCAN BECAUSE THE FIX HAD TO BE FASTER, NOT
   STRICTER (issue 172). Asking a rescanning predicate once per surviving field
   costs fields x option-octets, and BOTH are bounded only by the 64 KiB header
   allowance, so an attacker picks the split that maximises the product - about
   2.6 x 10^8 octet steps for one well-formed request, uninterruptible, on a
   serial serve loop. Refusing a request carrying an absurd number of options
   would be the cheap repair and is forbidden: net/http accepts them, so a
   strictness this backend adds alone is a SPEC 14.0 disagreement about whether
   a Request exists. Building the union once and binary-searching it makes the
   same answer cost option-octets + fields x log(options), which no split of the
   budget turns back into a quadratic.

   THE INDEX IS NOT A SEMANTIC OBJECT. It carries exactly what the predicate
   read: an element that is empty or not a token is dropped while building, so
   membership in the index and satisfaction of the old predicate are the same
   relation. The lowercasing is done ONCE per option here rather than once per
   comparison, which is also why the stored token is a copy rather than a view.

   ITS SIZE IS BOUNDED BY THE SAME OCTETS THE OPTIONS ARE - an option costs at
   least two of them, so the index, its doubling waste and the sort scratch are
   all linear in a header section that is already capped, and the worst case is
   under a megabyte of arena for a 64 KiB request. A repair for a resource
   exhaustion must not introduce an allocation the same party controls. */
typedef struct ONom ONom;
struct ONom {
  char *tok;
  int len;
};

/* The order the index is sorted and searched in: unsigned octets, and a shorter
   name that is a prefix of a longer one first. The same total order the row-13
   field sort uses - one comparison rule for both, rather than two that could
   disagree about which of x-hop and x-hop-2 comes first. */
static int o_nom_order(const char *a, int alen, const char *b, int blen) {
  int m = alen < blen ? alen : blen;
  int c = m > 0 ? memcmp(a, b, (size_t)m) : 0;
  if (c != 0) return c < 0 ? -1 : 1;
  return alen < blen ? -1 : (alen > blen ? 1 : 0);
}

/* Every valid option in the message, lowercased and sorted.
   A BOTTOM-UP MERGE SORT, AND THE CHOICE IS PART OF THE FIX. qsort would be
   shorter and is quicksort in several libcs, which is quadratic on an input the
   attacker writes - the defect this function exists to remove, reintroduced one
   layer down and chosen by the same party. Merge sort is O(n log n) on every
   input; it is iterative, so it adds no recursion depth to a request path; and
   its scratch comes from the request arena, so a refusal unwinds it with
   everything else. */
static ONom *o_nominations(OReq *r, int *count) {
  ONom *ix = 0;
  int n = 0, cap = 0;
  for (int i = 0; i < r->nf; i++) {
    OField *f = &r->fs[i];
    if (!o_field_named(f, "connection")) continue;
    int a = 0;
    for (int k = 0; k <= f->vlen; k++) {
      if (k < f->vlen && f->value[k] != ',') continue;
      int s0 = a, e0 = k;
      while (s0 < e0 && o_ows((unsigned char)f->value[s0])) s0++;
      while (e0 > s0 && o_ows((unsigned char)f->value[e0 - 1])) e0--;
      a = k + 1;
      /* An empty element is not a token, so one test discharges both
         dispositions row 16 gives them. */
      if (!o_is_token(f->value + s0, e0 - s0)) continue;
      if (n == cap) {
        int nc = cap > 0 ? cap * 2 : 16;
        ONom *nx = (ONom *)xalloc(sizeof(ONom) * (size_t)nc);
        if (n > 0) memcpy(nx, ix, sizeof(ONom) * (size_t)n);
        ix = nx;
        cap = nc;
      }
      int len = e0 - s0;
      char *t = (char *)xalloc((size_t)len);
      for (int q = 0; q < len; q++) {
        t[q] = (char)o_ascii_lower((unsigned char)f->value[s0 + q]);
      }
      ix[n].tok = t;
      ix[n].len = len;
      n++;
    }
  }
  if (n > 1) {
    ONom *tmp = (ONom *)xalloc(sizeof(ONom) * (size_t)n);
    for (int w = 1; w < n; w *= 2) {
      for (int lo = 0; lo < n; lo += 2 * w) {
        int mid = lo + w, hi = lo + 2 * w;
        if (mid > n) mid = n;
        if (hi > n) hi = n;
        int i = lo, j = mid, o = lo;
        while (i < mid && j < hi) {
          if (o_nom_order(ix[j].tok, ix[j].len, ix[i].tok, ix[i].len) < 0) tmp[o++] = ix[j++];
          else tmp[o++] = ix[i++];
        }
        while (i < mid) tmp[o++] = ix[i++];
        while (j < hi) tmp[o++] = ix[j++];
      }
      memcpy(ix, tmp, sizeof(ONom) * (size_t)n);
    }
  }
  *count = n;
  return ix;
}

/* lname is a field name, so it is already lowercase - row 8 canonicalized it at
   the parser, and the index holds the option lowercased for the same reason. */
static int o_nominated(const ONom *ix, int n, const char *lname, int lnlen) {
  int lo = 0, hi = n - 1;
  while (lo <= hi) {
    int mid = lo + (hi - lo) / 2;
    int c = o_nom_order(ix[mid].tok, ix[mid].len, lname, lnlen);
    if (c == 0) return 1;
    if (c < 0) lo = mid + 1;
    else hi = mid - 1;
  }
  return 0;
}

static int o_http_adapt(OReq *r, OField *out, int *nout) {
  int n = 0;
  /* ONCE PER REQUEST, BEFORE THE FIELD LOOP - which is what makes the loop's
     lookup logarithmic instead of a second pass over the message. Building it
     here rather than at the parser keeps the transformation a function of the
     parser boundary alone: OReq stays the six components SPEC 14.2a names, and
     nothing derived from them is smuggled into it. */
  int nnom = 0;
  ONom *nom = o_nominations(r, &nnom);
  for (int i = 0; i < r->nf; i++) {
    OField *f = &r->fs[i];
    if (o_framing_field(f)) continue;
    /* SPEC 14.2 row 5 is UNCONDITIONAL, so a nomination cannot reach the host entry:
       honouring one would let a client delete a mandatory field from the Oath
       value and change how the handler behaves. The nomination is IGNORED, not
       refused. The host field line is dropped here and re-enters below as the lifted
       authority. */
    if (o_field_named(f, "host")) continue;
    if (o_nominated(nom, nnom, f->name, f->nlen)) continue;
    out[n++] = *f;
  }

  /* SPEC 14.2 rows 4 and 5, PROTO-AUTHORITY-SOURCE and REQ-HOST-IS-A-HEADER.
     Precedence over HTTP/1.1 is: absolute-form target, then the Host field
     line - there is no authority pseudo-header here, since this backend speaks 1.1 only.
     Row 26: a present but EMPTY authority supplies nothing and produces NO
     host entry, because an authority of zero length identifies nothing.
     AT MOST ONE host entry, and never synthesized. */
  char *auth = NULL;
  int alen = 0;
  if (o_target_authority(r->target, r->tlen, &auth, &alen) != 1) {
    for (int i = 0; i < r->nf; i++) {
      if (!o_field_named(&r->fs[i], "host")) continue;
      auth = r->fs[i].value;
      alen = r->fs[i].vlen;
      break;
    }
  }
  if (alen > 0) {
    out[n].name = (char *)"host";
    out[n].nlen = 4;
    out[n].value = auth;
    out[n].vlen = alen;
    n++;
  }

  /* SPEC 14.2 row 9, applied AFTER rows 15-17 and after the lift, over
     everything that becomes a Str: the method, the raw target, every surviving
     field name and value, and the lifted authority - which is covered by
     construction, since it is now an entry value. HTAB is permitted inside a
     field VALUE and a lifted authority and nowhere else. */
  if (!o_txt_ok(r->method, r->mlen, 0) || !o_txt_ok(r->target, r->tlen, 0)) return 400;
  for (int i = 0; i < n; i++) {
    if (!o_txt_ok(out[i].name, out[i].nlen, 0)) return 400;
    if (!o_txt_ok(out[i].value, out[i].vlen, 1)) return 400;
  }

  /* SPEC 14.2 rows 13 and 14. Ascending by lowercase name as UNSIGNED octets,
     a shorter name that is a prefix sorting first; repeats under one name keep
     their ARRIVAL order, are never reordered, deduplicated, dropped or
     comma-joined. An insertion sort, because it is stable by construction and
     the input is bounded - a stability bug here would silently reorder repeats,
     which is exactly what row 14 forbids and what no type would catch. */
  for (int i = 1; i < n; i++) {
    OField key = out[i];
    int j = i - 1;
    while (j >= 0) {
      int m = out[j].nlen < key.nlen ? out[j].nlen : key.nlen;
      int c = memcmp(out[j].name, key.name, (size_t)m);
      if (c == 0) c = out[j].nlen < key.nlen ? -1 : (out[j].nlen > key.nlen ? 1 : 0);
      if (c <= 0) break;
      out[j + 1] = out[j];
      j--;
    }
    out[j + 1] = key;
  }
  *nout = n;
  return 0;
}

/* The Request VALUE. Constructor indices are passed in rather than assumed:
   they are facts about the protocol types in THIS store and the compiler knows
   them. */
static OVal *o_http_value(OReq *r, OField *hs, int nh,
                          int nil_, int cons_, int pair_, int req_) {
  OVal *body = o_ctor(nil_, 0, o_fields(0));
  for (int i = r->blen - 1; i >= 0; i--) {
    OVal **f = o_fields(2);
    f[0] = o_int((long long)(unsigned char)r->body[i]);
    f[1] = body;
    body = o_ctor(cons_, 2, f);
  }
  OVal *hdrs = o_ctor(nil_, 0, o_fields(0));
  for (int i = nh - 1; i >= 0; i--) {
    OVal **pf = o_fields(2);
    pf[0] = o_strn(hs[i].name, hs[i].nlen);
    pf[1] = o_strn(hs[i].value, hs[i].vlen);
    OVal **f = o_fields(2);
    f[0] = o_ctor(pair_, 2, pf);
    f[1] = hdrs;
    hdrs = o_ctor(cons_, 2, f);
  }
  OVal **f = o_fields(5);
  f[0] = o_strn(r->method, r->mlen);
  f[1] = o_strn(r->target, r->tlen);
  f[2] = hdrs;
  f[3] = body;
  f[4] = o_int(r->at);
  return o_ctor(req_, 5, f);
}

static const char *o_http_reason(int status) {
  switch (status) {
    case 200: return "OK";
    case 400: return "Bad Request";
    case 413: return "Content Too Large";
    case 431: return "Request Header Fields Too Large";
    case 500: return "Internal Server Error";
    case 501: return "Not Implemented";
    case 408: return "Request Timeout";
    case 417: return "Expectation Failed";
    case 505: return "HTTP Version Not Supported";
    case 201: return "Created";
    case 202: return "Accepted";
    case 204: return "No Content";
    case 304: return "Not Modified";
    case 401: return "Unauthorized";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 409: return "Conflict";
    case 422: return "Unprocessable Content";
    case 429: return "Too Many Requests";
    case 503: return "Service Unavailable";
  }
  /* RFC 9110 15: the reason phrase carries no meaning and a client MUST NOT act
     on it, so an unlisted status gets a placeholder rather than an invented
     name that could read as authoritative. */
  return "Status";
}

/* The adapter's own answers: a refusal the handler never saw, and the 500 a
   refusal unwound to. Plain text, one line, no body framing surprises. */
static void o_http_status(int fd, long long deadline, int head_only, int status, const char *why) {
  /* A HEAD RESPONSE CARRIES NO CONTENT WHATEVER ITS STATUS. The rule is about
     the request's method, not about success, so a diagnostic body here would be
     invalid framing on exactly the paths that are already reporting a problem -
     the header still states the length the body would have had. */
  char head[512];
  int n = head_only
              ? snprintf(head, sizeof head,
                         "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\n"
                         "Content-Length: %d\r\nConnection: close\r\n\r\n",
                         status, o_http_reason(status), (int)strlen(why) + 1)
              : snprintf(head, sizeof head,
                         "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\n"
                         "Content-Length: %d\r\nConnection: close\r\n\r\n%s\n",
                         status, o_http_reason(status), (int)strlen(why) + 1, why);
  if (n > 0) o_send_all(fd, deadline, head, n < (int)sizeof head ? n : (int)sizeof head - 1);
}

/* CLOSING ON A REFUSAL WITHOUT LOSING THE ANSWER. A close() with octets still
   unread in the receive buffer sends RST on Linux, and an RST can discard the
   response this code just wrote - so the client sees a connection error where a
   400 was actually served, and the refusal becomes indistinguishable from a
   crash. A bounded drain with a short deadline retires that: it reads what the
   peer already sent, it cannot be held open by a peer that keeps sending, and
   the bound is what stops the drain from becoming the denial the timeout above
   exists to prevent. */
static void o_http_close(int fd) {
  /* HALF-CLOSE FIRST, then drain. The peer sees EOF and stops waiting on us, so
     the drain below ends at its close rather than at the deadline - which is
     what keeps a lingering close from costing every successful request the full
     timeout. */
  shutdown(fd, SHUT_WR);
  struct timeval tv;
  tv.tv_sec = 0;
  tv.tv_usec = 250000;
  setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
  /* THE DRAIN'S CLOCK IS ITS OWN, and it is derived from the socket timeout
     just set rather than handed in. A wider deadline passed from the caller
     would be used by o_recv_some to WIDEN that timeout again - the linger would
     then take the caller's second instead of this quarter of one, on every
     connection whose peer keeps its write side open. */
  long long linger = o_now_ms() + 250;
  char sink[4096];
  int drained = 0;
  while (drained < O_HTTP_HDRMAX) {
    int k = o_recv_some(fd, linger, sink, (int)sizeof sink);
    if (k <= 0) break;
    drained += k;
  }
  close(fd);
}

/* AN OUTBOUND HEADER MUST BE A HEADER, and this is checked rather than hoped
   for. Unlike everything above, these are octets the HANDLER chose - and SPEC
   14.4 deliberately does not constrain a Response, so nothing upstream has
   looked at them.

   CR, LF and NUL are the injection case: a value carrying one would SPLIT this
   response and let a handler inject a message of its own. They are not the only
   case, and a first version of this checked only them - a name of "X Bad" or
   "X: Bad", or a value carrying 0x01, produced a line no HTTP parser can read,
   which is a malformed response emitted rather than the malformed-Response 500
   this code promises. So the rule is the grammar's: a name is a token (RFC 9110
   5.6.2) and a value is printable US-ASCII with HTAB (RFC 9110 5.5).

   REFUSED AND NAMED, NEVER SANITIZED. A repaired header is a header the handler
   did not write, which is the substitution this backend declines everywhere
   else. */
static int o_hdr_name_out_ok(OVal *v) {
  return v && v->tag == T_STR && o_is_token(v->s, v->slen);
}

static int o_hdr_value_out_ok(OVal *v) {
  if (!v || v->tag != T_STR) return 0;
  /* FIELD-CONTENT, not row 9's ASCII. Row 9 governs what a Str can carry INTO
     the language, where the answer must be the same on every backend; this is
     the way OUT, where HTTP permits obs-text - so a handler answering with a
     non-ASCII header value is emitting a legal field value, and the Go backend
     emits it. Refusing it here would make the two backends disagree about a
     Response neither specification constrains. */
  for (int i = 0; i < v->slen; i++) {
    unsigned char c = (unsigned char)v->s[i];
    if ((c < 0x20 && c != 0x09) || c == 0x7F) return 0;
  }
  return 1;
}

/* THE ADAPTER OWNS FRAMING, so a handler's own copy of one of these is dropped
   rather than emitted a second time beside the one this code states. Compared
   case-insensitively, because these names are HTTP's rather than the handler's
   and HTTP does not distinguish their case. */
static int o_hdr_is_framing(OVal *k) {
  const char *own[3];
  own[0] = "content-length";
  own[1] = "connection";
  own[2] = "transfer-encoding";
  for (int j = 0; j < 3; j++) {
    int ol = (int)strlen(own[j]);
    if (k->slen != ol) continue;
    int same = 1;
    for (int q = 0; q < ol; q++) {
      if (o_ascii_lower((unsigned char)k->s[q]) != own[j][q]) { same = 0; break; }
    }
    if (same) return 1;
  }
  return 0;
}

/* SERIALISATION. This is where a Response becomes octets, and the arena release
   is the statement after the call to it.

   SPEC 14.4: the RESPONSE is deliberately not constrained - a handler's header
   names are its own choice and HTTP compares them case-insensitively - so
   nothing here canonicalizes them. What this DOES own is framing:
   content-length, connection and transfer-encoding are the adapter's to state,
   so a handler's own copy of one is dropped rather than emitted twice.

   A MALFORMED Response is a 500, not a crash and not a guess. It is
   unreachable for a well-typed program, which is why the class exists at all:
   the checker admits only (Resp Int (List (Pair Str Str)) (List Int)), so
   anything else here means this compiler is wrong - and answering the client
   while saying so is better than either serving nonsense or ending the
   process. */
static int o_http_respond(int fd, long long deadline, OReq *r, OVal *resp, int nil_, int cons_, int pair_, int resp_) {
  int head_only = r && r->mlen == 4 && memcmp(r->method, "HEAD", 4) == 0;
  int is_connect = r && r->mlen == 7 && memcmp(r->method, "CONNECT", 7) == 0;
  if (!resp || resp->tag != T_CTOR || resp->idx != resp_ || resp->n != 3) {
    o_http_status(fd, deadline, head_only, 500, "the handler returned a malformed Response");
    return 0;
  }
  OVal *sv = resp->f[0];
  if (!sv || sv->tag != T_INT || sv->isign < 0 || sv->ilen > 1) {
    o_http_status(fd, deadline, head_only, 500, "the handler returned a status that is not a whole number in 200..599");
    return 0;
  }
  long long status = sv->ilen == 0 ? 0 : (long long)sv->imag[0];
  /* 200 AND NOT 100 IS THE FLOOR, and that is a statement about the protocol
     rather than a tighter range for its own sake. A 1xx is an INTERIM response:
     it is followed by a final one on the same connection, and Response has no
     way to say what that final one is. Emitting it and closing would leave a
     client reading EOF where a completed exchange was promised, so it is
     refused and named - the same disposition as any other Response this
     boundary cannot deliver. */
  if (status < 200 || status > 599) {
    o_http_status(fd, deadline, head_only, 500, "the handler returned a status this boundary cannot deliver "
                           "(1xx is interim, and a Response cannot say what follows it)");
    return 0;
  }

  OBuf body;
  memset(&body, 0, sizeof body);
  OVal *c = resp->f[2];
  for (; c && c->tag == T_CTOR && c->idx == cons_ && c->n == 2; c = c->f[1]) {
    OVal *e = c->f[0];
    if (!e || e->tag != T_INT || e->isign < 0 || e->ilen > 1 ||
        (e->ilen == 1 && e->imag[0] > 255u)) {
      /* NAMED, not truncated. Reducing 300 to 44 would make two distinct
         response bodies identical, which is the U+FFFD defect this runtime
         refuses at every other boundary. */
      o_http_status(fd, deadline, head_only, 500, "the handler returned a body element outside 0..255");
      return 0;
    }
    char b0 = (char)(e->ilen == 0 ? 0 : (int)e->imag[0]);
    o_buf_put(&body, &b0, 1);
  }
  /* THE TERMINATOR IS CHECKED, not assumed. A walk that stops at "not a Cons"
     accepts a truncated list as a complete one, so a body this code could not
     read would be served as a SHORTER body rather than reported. */
  if (!c || c->tag != T_CTOR || c->idx != nil_) {
    o_http_status(fd, deadline, head_only, 500, "the handler returned a malformed Response body");
    return 0;
  }

  OBuf head;
  memset(&head, 0, sizeof head);
  char line[128];
  int n = snprintf(line, sizeof line, "HTTP/1.1 %d %s\r\n", (int)status, o_http_reason((int)status));
  o_buf_put(&head, line, n);
  OVal *hc = resp->f[1];
  for (; hc && hc->tag == T_CTOR && hc->idx == cons_ && hc->n == 2; hc = hc->f[1]) {
    OVal *p = hc->f[0];
    if (!p || p->tag != T_CTOR || p->idx != pair_ || p->n != 2) {
      o_http_status(fd, deadline, head_only, 500, "the handler returned a malformed Response header");
      return 0;
    }
    OVal *k = p->f[0], *v = p->f[1];
    if (!o_hdr_name_out_ok(k)) {
      o_http_status(fd, deadline, head_only, 500, "a Response header name is not an HTTP token");
      return 0;
    }
    if (!o_hdr_value_out_ok(v)) {
      o_http_status(fd, deadline, head_only, 500, "a Response header value carries an octet no HTTP field value may hold");
      return 0;
    }
    if (o_hdr_is_framing(k)) continue;
    o_buf_put(&head, k->s, k->slen);
    o_buf_put(&head, ": ", 2);
    o_buf_put(&head, v->s, v->slen);
    o_buf_put(&head, "\r\n", 2);
  }
  if (!hc || hc->tag != T_CTOR || hc->idx != nil_) {
    o_http_status(fd, deadline, head_only, 500, "the handler returned a malformed Response header list");
    return 0;
  }
  /* THE FRAMING RULES ARE HTTP'S, AND THIS CODE OWNS FRAMING. RFC 9110 6.4.1:
     a 1xx, 204 or 304 response has no content and carries no Content-Length,
     and a response to HEAD has the same header fields as the GET would and no
     body.

     THE TWO CASES ARE NOT THE SAME KIND OF THING, so they are answered
     differently. HEAD is a property of the REQUEST: the handler answered the
     resource correctly and this method simply does not carry the body, so the
     length is stated and the octets are withheld - suppressing it is required
     rather than a repair. A body under a bodiless STATUS is a contradiction
     inside the Response itself, and it is refused and named for the same reason
     a body element outside 0..255 is: emitting a message HTTP forbids, or
     silently dropping octets the handler produced, are both substitutions. */
  /* RFC 9110 6.4.1 and 15.3.6: 204, 205 and 304 carry no content. 1xx is not
     in this list because it never reaches here - it is refused above. */
  /* A SUCCESSFUL CONNECT SWITCHES THE CONNECTION TO A TUNNEL (RFC 9110 9.3.6),
     which this runtime has no way to provide - so it is refused rather than
     answered with ordinary framing a client would then try to tunnel through.
     Refusing a method/status combination is the same fail-closed answer this
     boundary gives every other Response it cannot deliver. */
  if (is_connect && status >= 200 && status < 300) {
    o_http_status(fd, deadline, head_only, 500,
                  "a successful CONNECT needs a tunnel this backend cannot provide");
    return 0;
  }
  int bodiless = status == 204 || status == 205 || status == 304;
  if (bodiless && body.n > 0) {
    o_http_status(fd, deadline, head_only, 500, "the handler returned content under a status that carries none");
    return 0;
  }
  if (bodiless) {
    n = snprintf(line, sizeof line, "Connection: close\r\n\r\n");
  } else {
    n = snprintf(line, sizeof line, "Content-Length: %d\r\nConnection: close\r\n\r\n", body.n);
  }
  o_buf_put(&head, line, n);
  if (!o_send_all(fd, deadline, head.p, head.n)) return 0;
  if (body.n > 0 && !head_only) o_send_all(fd, deadline, body.p, body.n);
  return 1;
}

static int o_http_listen(void) {
  const char *addr = getenv("OATH_HTTP_ADDR");
  if (!addr || !*addr) addr = ":8080";
  const char *colon = strrchr(addr, ':');
  if (!colon) {
    fprintf(stderr, "oath: OATH_HTTP_ADDR (%s) has no port; use host:port or :port\n", addr);
    exit(1);
  }
  /* EVERY OCTET OF THE SUFFIX IS A DIGIT, checked BEFORE the conversion. atoi
     stopped at the first non-digit and reported nothing, so a trailing typo
     bound the digits before it; strtol reported the tail but still ACCEPTED a
     leading sign and leading whitespace of its own accord, so a plus or a space
     was normalised away and the same wrong endpoint was bound by a validator
     that believed it had rejected it. A conversion that repairs its input
     cannot also be the thing that validates it. */
  const char *pd = colon + 1;
  int digits = 0;
  for (const char *q = pd; *q; q++) {
    if (*q < '0' || *q > '9') { digits = -1; break; }
    digits++;
  }
  char *pend = NULL;
  long port = digits > 0 ? strtol(pd, &pend, 10) : -1;
  if (digits <= 0 || !pend || *pend != 0 || port <= 0 || port > 65535) {
    fprintf(stderr, "oath: OATH_HTTP_ADDR (%s) does not name a port in 1..65535\n", addr);
    exit(1);
  }
  char host[128];
  size_t hl = (size_t)(colon - addr);
  if (hl >= sizeof host) {
    fprintf(stderr, "oath: OATH_HTTP_ADDR (%s) has an unreasonably long host\n", addr);
    exit(1);
  }
  memcpy(host, addr, hl);
  host[hl] = 0;
  struct sockaddr_in sa;
  memset(&sa, 0, sizeof sa);
  sa.sin_family = AF_INET;
  sa.sin_port = htons((unsigned short)port);
  if (hl == 0) sa.sin_addr.s_addr = htonl(INADDR_ANY);
  else if (strcmp(host, "localhost") == 0) sa.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  else if (inet_pton(AF_INET, host, &sa.sin_addr) != 1) {
    /* NAMED AND REFUSED rather than resolved. A name lookup would make where
       this artifact listens depend on the host's resolver, and this backend's
       whole position is that what it does not implement it declines by name. */
    fprintf(stderr, "oath: OATH_HTTP_ADDR host %s is not an IPv4 literal; this backend "
                    "binds IPv4 literals and localhost only\n", host);
    exit(1);
  }
  int fd = socket(AF_INET, SOCK_STREAM, 0);
  if (fd < 0) {
    fprintf(stderr, "oath: cannot create a socket: %s\n", strerror(errno));
    exit(1);
  }
  int one = 1;
  setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
  if (bind(fd, (struct sockaddr *)&sa, sizeof sa) != 0) {
    fprintf(stderr, "oath: cannot bind %s: %s\n", addr, strerror(errno));
    exit(1);
  }
  if (listen(fd, O_HTTP_BACKLOG) != 0) {
    fprintf(stderr, "oath: cannot listen on %s: %s\n", addr, strerror(errno));
    exit(1);
  }
  /* AFTER the bind, so the line is a statement about OBSERVABLE STARTUP rather
     than about an intention. A supervisor that waits for it is then waiting for
     something true. */
  fprintf(stderr, "oath handler listening on %s\n", addr);
  fflush(stderr);
  return fd;
}

/* THE SERVE LOOP. One connection at a time, one request per connection.
   Returns only if the listening socket fails; a handler is a long-lived server
   and there is no other way out that is not a bug or a signal.

   IT APPLIES A HANDLER VALUE, NOT A CODE POINTER, AND BOTH ENTRY SHAPES REACH
   IT THAT WAY. A capability-first handler's handler IS a closure - the result of
   applying the entry to the resolved record - and a plain handler's is that same
   closure over a null environment. Carrying an OCode and an OVal and choosing
   between them per request would make "which one is live" a state this loop had
   to be right about on every iteration; there is one value and one call instead.

   THE HANDLER IS PROCESS-LIFETIME MEMORY. It is built before this is entered,
   while the perm region is open, and this loop runs only after it is sealed - so
   the arena release below cannot reach it however many requests are served. */
static int o_serve_loop(OVal *handler, int nil_, int cons_, int pair_, int req_, int resp_) {
  if (o_perm_state != 2) o_bug("the serve loop was entered before the process-lifetime region was sealed");
  /* A client that vanishes mid-write must not take the process with it. This is
     the same class as a refusal: a remote party cannot be permitted to end a
     server, so the failed write is reported by send() and answered by closing
     the connection. */
  signal(SIGPIPE, SIG_IGN);
  int lfd = o_http_listen();
  for (;;) {
    int fd = accept(lfd, NULL, NULL);
    if (fd < 0) {
      if (errno == EINTR || errno == ECONNABORTED || errno == EAGAIN) continue;
      fprintf(stderr, "oath: the listening socket failed: %s\n", strerror(errno));
      return 70;
    }
    /* A SERIAL SERVER MUST NOT BE HOLDABLE. Without a receive deadline one peer
       that connects and says nothing blocks every other client forever, which
       is a remote party ending the service by other means. */
    /* ONE DEADLINE FOR THE WHOLE REQUEST, taken at accept. It is configurable
       for the same reason the listen address is: a bound nobody can observe
       firing is a hypothesis, and a thirty-second default cannot be witnessed
       inside a test suite. The floor of one second is what stops a
       configuration from disabling it. */
    long long budget = O_HTTP_REQSECS;
    const char *bs = getenv("OATH_HTTP_REQUEST_TIMEOUT");
    if (bs && *bs) {
      char *bend = NULL;
      long v = strtol(bs, &bend, 10);
      if (bend && *bend == 0 && v >= 1 && v <= 86400) budget = v;
    }
    long long deadline = o_now_ms() + budget * 1000;
    struct timeval tv;
    tv.tv_sec = O_HTTP_IOSECS;
    tv.tv_usec = 0;
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);

    /* THE REFUSAL BOUNDARY FOR ONE REQUEST, established BEFORE anything is
       allocated for it - so the jump target is a frame that stays live for the
       whole of the handler's evaluation, and so nothing this iteration
       allocated is live at the moment the target is saved. */
    volatile int cfd = fd;
    /* VOLATILE, like the descriptor, and for the same reason: it is assigned
       between the setjmp and the longjmp, so a non-volatile local's value is
       indeterminate on the way back. A HEAD request whose handler refused must
       still get a bodiless 500. */
    volatile int chead = 0;
    if (setjmp(o_request_jump) != 0) {
      /* A refusal travelled here. Its line is already on stderr - the operator
         needs to see WHICH operand refused, and a 500 naming nothing is the
         silent failure this repo keeps finding. SPEC 14.2 settles the
         disposition for a malformed request field (400, handler not invoked);
         this is the same principle one step later, when the handler WAS invoked
         and could not complete. */
      o_http_status((int)cfd, o_now_ms() + budget * 1000, chead, 500,
                    "the handler could not complete this request");
      o_http_close((int)cfd);
      o_arena_release();
      continue;
    }
    o_request_live = 1;

    OReq r;
    memset(&r, 0, sizeof r);
    int status = o_http_parse(fd, deadline, &r);
    chead = r.mlen == 4 && memcmp(r.method, "HEAD", 4) == 0;
    OField *hs = NULL;
    int nh = 0;
    if (status == 0) {
      /* One row per surviving field plus the lifted host entry. Derived from
         the request rather than from a constant, so it cannot be short. */
      hs = (OField *)xalloc(sizeof(OField) * (r.nf + 1));
      status = o_http_adapt(&r, hs, &nh);
    }
    if (status != 0) {
      /* THE HANDLER IS NOT INVOKED. That is row 9's words and row 24's, and it
         is the half that matters: a refused request must not reach Oath code at
         all, so no partial or repaired value can be observed by it. */
      o_request_live = 0;
      if (status > 0) {
        o_http_status(fd, o_now_ms() + budget * 1000,
                      r.mlen == 4 && memcmp(r.method, "HEAD", 4) == 0, status,
                      "this request was refused at the SPEC 14 boundary");
      }
      o_http_close(fd);
      o_arena_release();
      continue;
    }

    OVal *value = o_http_value(&r, hs, nh, nil_, cons_, pair_, req_);
    OVal *out = o_apply(handler, value);
    /* The handler has returned, so a refusal can no longer arrive from Oath
       code; clearing here means serialisation cannot re-enter the boundary and
       write a second response onto the same connection. */
    o_request_live = 0;
    /* A FRESH WINDOW FOR THE ANSWER, not the remainder of the read budget. The
       request deadline may already have expired - that is exactly how a 408 is
       reached - and a write bounded by an expired deadline could not send the
       refusal it was called to send. What each bound is FOR differs: one stops
       a peer from holding this server while it talks, the other while it
       listens. */
    o_http_respond(fd, o_now_ms() + budget * 1000, &r,
                   out, nil_, cons_, pair_, resp_);
    /* THE SAME LINGERING CLOSE THE REFUSAL PATHS USE. A client that pipelined a
       second request, or sent a body this answer did not read, leaves octets
       unread - and a close with unread octets sends RST on Linux, which can
       discard the response already written. A successful answer is exactly as
       losable that way as a refusal. */
    o_http_close(fd);
    /* THE RELEASE POINT. After serialisation, and the order is the whole
       argument: the response is written from Str buffers and byte lists that
       live in this region, so moving this above o_http_respond would serialise
       freed memory. */
    o_arena_release();
  }
}

/* THE PLAIN HANDLER'S ENTRY POINT. Its handler needs no authority, so the only
   thing provisioning does is box the entry's code as a closure over an empty
   environment - in the perm region, because that box is applied by every
   request and the arena would free it after the first. */
int o_serve(OCode entry, int nil_, int cons_, int pair_, int req_, int resp_) {
  o_perm_open();
  OVal *handler = o_closure(entry, NULL);
  o_perm_seal();
  return o_serve_loop(handler, nil_, cons_, pair_, req_, resp_);
}

/* THE CAPABILITY-FIRST HANDLER'S ENTRY POINT (#173), AND THE ORDER IS THE WHOLE
   OF IT.

   Resolution runs HERE, before o_serve_loop and therefore before o_http_listen:
   a host that cannot supply a declared requirement exits 70 from o_require or
   o_require_value with no socket ever bound, so "provisioned" and "listening"
   cannot come apart. That is the launch invariant #114 states, and putting the
   resolve call inside the loop's caller rather than inside the loop is what
   makes it structural rather than a comment.

   THE ENTRY IS APPLIED ONCE, not per request. Applying (-> {caps} (-> Request
   Response)) to the record yields the handler closure, and everything that
   application allocates - the record, its capability closures, the environment
   the returned closure captures - is perm memory because the region is still
   open. Sealing before the loop is what makes every LATER allocation a request
   allocation.

   The resolver is a function POINTER rather than a call the emitter inlines
   here, for the reason the loop is in C at all: what to resolve is the
   emitter's business and WHEN is this runtime's, and a sequence split across
   the two is one nobody owns. */
int o_serve_caps(OVal *(*resolve)(void), OCode entry, int nil_, int cons_, int pair_, int req_, int resp_) {
  o_perm_open();
  OVal *caps = resolve();
  OVal *handler = entry(NULL, caps);
  if (!handler || handler->tag != T_CLOS) o_bug("a capability-first entry did not return a handler");
  o_perm_seal();
  return o_serve_loop(handler, nil_, cons_, pair_, req_, resp_);
}

#else

/* THE HANDLER PROTOCOL ON A HOST THIS SLICE HAS NO SOCKET LAYER FOR. Refused by
   name, at run time rather than at compile time, because the refusal is a
   property of the HOST and not of the program: the same artifact's source
   compiles here and serves on a POSIX host. Exit 70, the status every other
   host refusal in this runtime uses, so a supervisor reads it the same way. */
static int o_no_socket_layer(void) {
  fputs("oath: this host has no socket layer in the LLVM backend, so the handler protocol "
        "cannot be served here; build this entry with the Go backend\n", stderr);
  return 70;
}

int o_serve(OCode entry, int nil_, int cons_, int pair_, int req_, int resp_) {
  (void)entry;
  (void)nil_;
  (void)cons_;
  (void)pair_;
  (void)req_;
  (void)resp_;
  return o_no_socket_layer();
}

/* REFUSED BEFORE RESOLVING, deliberately: this host cannot serve whatever the
   record contains, and provisioning first would report a missing capability as
   the reason a program that could never have served did not start. */
int o_serve_caps(OVal *(*resolve)(void), OCode entry, int nil_, int cons_, int pair_, int req_, int resp_) {
  (void)resolve;
  (void)entry;
  (void)nil_;
  (void)cons_;
  (void)pair_;
  (void)req_;
  (void)resp_;
  return o_no_socket_layer();
}

#endif
`

// ---------- program assembly ----------

// llvmDeclarations is the runtime's interface as LLVM sees it.
const llvmDeclarations = `
@o_stack_floor = external global i64
declare ptr @llvm.frameaddress.p0(i32)
declare void @o_stack_exhausted() noreturn
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
declare i32 @o_serve(ptr, i32, i32, i32, i32, i32)
declare i32 @o_serve_caps(ptr, ptr, i32, i32, i32, i32, i32)
declare void @o_print(ptr)
declare void @o_arena_release()
declare ptr @o_require(ptr, ptr, ptr)
declare ptr @o_require_value(ptr, ptr, ptr)
declare ptr @o_int(i64)
declare i32 @o_str_idx(ptr)
declare ptr @o_str_head(ptr)
declare ptr @o_str_tail(ptr)
declare ptr @o_str_cons(ptr, ptr)
declare i32 @o_str_eq(ptr, ptr)
declare ptr @o_int_dec(ptr)
declare ptr @o_int_add(ptr, ptr)
declare ptr @o_int_sub(ptr, ptr)
declare ptr @o_int_mul(ptr, ptr)
declare ptr @o_int_neg(ptr)
declare ptr @o_int_div(ptr, ptr)
declare ptr @o_int_mod(ptr, ptr)
declare i32 @o_bytes_eq_ct(ptr, ptr, i32)
declare ptr @o_hmac_sha256(ptr, ptr, i32, i32)
declare i32 @o_int_eq(ptr, ptr)
declare i32 @o_int_lt(ptr, ptr)
declare i32 @o_int_le(ptr, ptr)
declare ptr @o_cap_env(ptr)
declare ptr @o_cap_readfile(ptr)
declare ptr @o_cap_emit(ptr)
`

// ---------- this backend's answer for each entry shape ----------

// llvmEntry is the LLVM backend's DECISION for one EntryShape.
//
// A DECISION IS A ROW. This backend answers every shape from this table rather
// than from an early `if` in emitLLVM, which is what makes every consumer read
// one fact: listCtorIndices and handlerCtorIndices both ask the table where the
// entry's input is, so they cannot disagree about it.
//
// IT NO LONGER CARRIES A REFUSAL, and the field was removed rather than left
// matching nothing (#173): every shape the variant defines is now lowered here.
// A shape this build does not define is still an error — entryShapeCase reports
// it — so the "ask the table first" discipline that kept a declined shape from
// being walked as a type survives the last refusal being retired.
type llvmEntry struct {
	// shape is the case this row decides, and it must equal the row's own index.
	// The array's LENGTH is checked by the compiler; this is what makes a row
	// left at its zero value in the middle of the table detectable. It matters
	// more here than in the Go backend, because llvmEntry's zero value is a
	// LEGITIMATE row — it is exactly the plain CLI answer — so a hole would
	// otherwise be indistinguishable from a decision.
	shape EntryShape

	// caps: resolve a capability record and apply it before the entry's own
	// input.
	caps bool
	// inputDepth is how many leading arrows to step past in the entry's TYPE
	// before reaching the parameter the entry's INPUT is built for. A
	// capability-first entry is (-> {caps} (-> in out)), so its input parameter
	// is one arrow in, whichever protocol `in` belongs to.
	//
	// ONE NUMBER FOR BOTH PROTOCOLS, deliberately. It was `argvDepth` while only
	// the CLI shapes needed it, and the handler derivation then hardcoded a walk
	// of its own — which was correct for exactly as long as no handler took a
	// capability record. Where the input is and which protocol it belongs to are
	// different questions with different answers, and this is the first.
	inputDepth int

	// handler: the entry's input is a Request VALUE built by the runtime's
	// SPEC §14 adapter rather than an argv list, and main is a serve loop
	// rather than one call and a print.
	//
	// It is a SEPARATE field from inputDepth rather than a sentinel value of it,
	// because the two answer different questions — where the input is, and which
	// protocol it belongs to — and a shape that answered both with one number
	// would make "no argv" and "argv at depth 0" the same state.
	handler bool
}

var llvmEntries = [...]llvmEntry{
	shapeCLI:         {shape: shapeCLI, caps: false, inputDepth: 0},
	shapeCLICaps:     {shape: shapeCLICaps, caps: true, inputDepth: 1},
	shapeHandler:     {shape: shapeHandler, handler: true, inputDepth: 0},
	shapeHandlerCaps: {shape: shapeHandlerCaps, caps: true, handler: true, inputDepth: 1},
}

// EXHAUSTIVENESS, at compile time. Adding a shape to the variant makes this a
// type error here, in the LLVM backend, independently of the Go backend's own
// assertion — two backends, two failures, neither derived from the other.
var _ entryShapeTable = [len(llvmEntries)]struct{}{}

// llvmEntryFor is the ONE way this backend learns an entry's shape. A shape this
// build does not define is an error — a defect in this backend rather than a
// subset boundary — and it is reported here so that no consumer walks the type
// of an entry the table has no row for.
func llvmEntryFor(prog *CompiledProgram) (llvmEntry, error) {
	return entryShapeCase("the LLVM backend", &llvmEntries, prog.Shape)
}

// entryInputTy walks an entry's type to the ARROW WHOSE ARGUMENT IS THE ENTRY'S
// OWN INPUT, stepping past the capability record when the shape says the entry
// takes one.
//
// ONE WALK FOR BOTH PROTOCOLS. The CLI derivation and the handler derivation
// each need this and each used to do it their own way — the CLI one by the
// shape's depth, the handler one by assuming the entry's type was already the
// (-> in out) arrow, which was true only while no handler could take a record.
// Two answers to one question is where they drifted apart, so there is one.
func entryInputTy(ty *Ty, ent llvmEntry, shape EntryShape) (*Ty, error) {
	for i := 0; i < ent.inputDepth; i++ {
		if ty == nil || ty.K != "fun" {
			return nil, fmt.Errorf("entry is not %s: it has fewer than %d leading arrows", shape, ent.inputDepth+1)
		}
		ty = ty.B
	}
	return ty, nil
}

// byteListCtors reports the empty and cons constructor indices of the (List Int)
// the crypto primitives (#78) are typed at.
//
// DERIVED BY ARITY AND FIELD TYPE, NOT BY NAME. listCtorIndices reads the names
// "Nil" and "Cons" because argv's list is the one the entry protocol names; here
// nothing names anything — SPEC §1 says only that both operands are `(List Int)`,
// a byte list, and the checker admits whatever datatype is bound to List in this
// store. What the declaration makes unambiguous instead is the SHAPE: one nullary
// constructor and one binary constructor carrying (Int, the list itself). That is
// the same move handlerCtorIndices makes for the protocol types, and for the same
// reason — a lookup keyed on a spelling fails on a store that spells it
// differently, while the shape is what the operation actually needs.
//
// THE REFERENCE IS LESS CAREFUL HERE AND THIS DOES NOT COPY IT. `bytesOfList` in
// eval.go walks while the constructor index is 1, i.e. it assumes Cons is
// declared second. For the corpus's List — (Nil) then (Cons a (List a)) — that is
// this function's answer too, so the two agree on every program in it. A store
// declaring the constructors the other way round would make `oath eval` read
// every byte list as empty while this backend read it correctly, which is a
// divergence in the REFERENCE's favour by construction and not something a
// backend should reproduce.
func (e *llvmEmitter) byteListCtors(ty *Ty) (nil_, cons int, err error) {
	if ty == nil || ty.K != "data" || len(ty.Args) != 1 || ty.Args[0].K != "int" {
		return 0, 0, fmt.Errorf("a crypto primitive's operand is %s, not a byte list", debugTy(ty))
	}
	ad, err := e.st.GetDef(ty.Hash)
	if err != nil {
		return 0, 0, err
	}
	nil_, cons = -1, -1
	for i, fields := range ad.Ctors {
		switch len(fields) {
		case 0:
			if nil_ >= 0 {
				return 0, 0, fmt.Errorf("the byte list's datatype declares more than one nullary constructor, so its empty case is ambiguous")
			}
			nil_ = i
		case 2:
			if cons >= 0 {
				return 0, 0, fmt.Errorf("the byte list's datatype declares more than one binary constructor, so its cons case is ambiguous")
			}
			cons = i
		}
	}
	if nil_ < 0 || cons < 0 || len(ad.Ctors) != 2 {
		return 0, 0, fmt.Errorf("the byte list's datatype is not one nullary and one binary constructor, so this backend cannot walk it as a list")
	}
	// AND THE FIELD TYPES, not just their count — the same check listCtorIndices
	// makes about argv. A datatype shaped `Cons Bool (List Bool)` has the right
	// arities and would have this runtime read a Bool as an octet.
	fields := instCtorFields(ad, ty.Hash, ty.Args, cons)
	// THE TAIL IS COMPARED AS A WHOLE TYPE, NOT BY DATATYPE HASH. A hash-only
	// test answers "is the tail the same DATATYPE", and the question here is "is
	// the tail THIS TYPE" — which differ exactly when the arguments differ. A
	// legal declaration like `(data List [a] (Nil) (Cons a (List Bool)))`
	// instantiated at Int has a cons tail of `(List Bool)`: same hash, same
	// arities, and octets that are not octets. It would pass emission and die
	// inside the runtime at o_bug, which is the one disposition this backend
	// does not permit — unsupported shapes are refused BY NAME, here, never
	// discovered by the emitted program.
	//
	// tyEq is the checker's own equality and compares Args recursively, so this
	// stays aligned with what the kernel calls the same type instead of being a
	// second opinion about it.
	if len(fields) != 2 || fields[0] == nil || fields[0].K != "int" || !tyEq(fields[1], ty) {
		return 0, 0, fmt.Errorf("the byte list's cons constructor is not (Int, itself), so this backend cannot read it as octets")
	}
	return nil_, cons, nil
}

// listCtorIndices reports the constructor indices of Nil and Cons for the List
// datatype the entry's argument uses.
//
// DERIVED rather than assumed. Constructor order is a fact about the datatype in
// THIS store; hardcoding 0 and 1 would be right today and silently wrong for a
// store whose List declares its constructors the other way round — and the
// failure would be a program that reverses its own arguments, which no type error
// catches.
func listCtorIndices(st *Store, prog *CompiledProgram) (nil_, cons int, err error) {
	// The shape decides where the entry's input is, and whether this backend has
	// a row for the entry at all — asked BEFORE the store, so an undecided shape
	// is reported as one rather than as whatever the type walk happens to trip
	// on.
	//
	// The depth comes from the shape rather than from the capability record's
	// nil-ness so that this walk and the capability construction below cannot
	// disagree about where argv is: they read one fact.
	ent, err := llvmEntryFor(prog)
	if err != nil {
		return 0, 0, err
	}
	// A HANDLER HAS NO ARGV: a consumer that answers for a protocol it was not
	// written for answers wrongly rather than not at all. Asked here, before the
	// store, so the diagnostic names the mismatch instead of reporting whatever
	// the type walk trips on first — for a handler that would be "Request does
	// not declare Nil and Cons", which describes a symptom and not the error.
	if ent.handler {
		return 0, 0, fmt.Errorf("%s takes a Request, not an argv list; its constructor "+
			"indices come from handlerCtorIndices", prog.Shape)
	}
	d, err := st.GetDef(prog.EntryHash)
	if err != nil {
		return 0, 0, err
	}
	// Walk to the argv parameter, by the DEPTH THE SHAPE STATES — past the
	// capability record when the entry takes one. Resolving the NAME "List"
	// instead would read the CURRENT binding, and a store that has since rebound
	// List would hand this program constructor indices for a different datatype —
	// argv silently reversed, or arms selected wrongly, with no type error
	// anywhere.
	ty, err := entryInputTy(d.Ty, ent, prog.Shape)
	if err != nil {
		return 0, 0, err
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

// handlerCtors is what the SPEC §14 adapter needs in order to BUILD the values
// the protocol is defined over: one constructor index per protocol type.
//
// The runtime is handed these rather than assuming 0 and 1, for the reason
// listCtorIndices gives about argv — a hardcoded index is right today and
// silently wrong for any store whose declaration order differs, and the failure
// is a handler reading its own path as its method with no type error anywhere.
type handlerCtors struct{ nilIdx, consIdx, pairIdx, reqIdx, respIdx int }

// handlerCtorIndices derives those indices from the ENTRY'S OWN TYPE.
//
// DERIVED BY ARITY, NOT BY NAME, and that is forced rather than preferred:
// SPEC §14.1a's PROTO-TYPES-BY-IDENTITY says a store MAY bind these types to
// any name or to NONE, so a lookup keyed on "List" or "Nil" would fail on a
// perfectly conformant store. What identity pins instead is the declaration,
// and the declaration makes each constructor unambiguous by field count: List
// has one nullary and one binary constructor, and each of Pair, Request and
// Response has exactly one.
//
// The arity checks are therefore also the validation. They cannot fail for an
// entry classifyEntry accepted — it compares HASHES against the §14.1a
// declarations — which is exactly why they are cheap to keep: they turn a
// future change in that classification into an error here rather than into a
// value built with an index from a different datatype.
func handlerCtorIndices(st *Store, prog *CompiledProgram) (handlerCtors, error) {
	var hc handlerCtors
	// The shape decides whether this backend lowers the entry at all, and it is
	// asked FIRST for the same reason listCtorIndices asks first: a declined
	// shape must refuse as a subset boundary rather than as whatever the type
	// walk trips on.
	ent, err := llvmEntryFor(prog)
	if err != nil {
		return hc, err
	}
	if !ent.handler {
		return hc, fmt.Errorf("%s is not a handler, so it has no protocol types to build", prog.Shape)
	}
	d, err := st.GetDef(prog.EntryHash)
	if err != nil {
		return hc, err
	}
	// Past the capability record when the shape says there is one, by the same
	// walk the CLI derivation uses. A capability-first handler is
	// (-> {caps} (-> Request Response)), and reading its FIRST arrow would find a
	// record where the protocol types are expected.
	ty, err := entryInputTy(d.Ty, ent, prog.Shape)
	if err != nil {
		return hc, err
	}
	if ty == nil || ty.K != "fun" || ty.A == nil || ty.A.K != "data" || ty.B == nil || ty.B.K != "data" {
		return hc, fmt.Errorf("entry is not %s: it is not a function between two datatypes", prog.Shape)
	}
	sole := func(h string, fields int, what string) (int, []Ty, error) {
		def, err := st.GetDef(h)
		if err != nil {
			return 0, nil, err
		}
		if len(def.Ctors) != 1 || len(def.Ctors[0]) != fields {
			return 0, nil, fmt.Errorf("%s does not have the single %d-field constructor SPEC §14.1a declares", what, fields)
		}
		return 0, def.Ctors[0], nil
	}
	reqFields := []Ty(nil)
	if hc.reqIdx, reqFields, err = sole(ty.A.Hash, 5, "the entry's Request type"); err != nil {
		return hc, err
	}
	if hc.respIdx, _, err = sole(ty.B.Hash, 3, "the entry's Response type"); err != nil {
		return hc, err
	}
	// The header list carries BOTH remaining types: `(List (Pair Str Str))` is
	// Request's third field, so reading it is how this reaches List and Pair
	// without resolving a name or hardcoding a hash.
	hdrs := reqFields[2]
	if hdrs.K != "data" || len(hdrs.Args) != 1 {
		return hc, fmt.Errorf("Request's headers field is not a list of pairs")
	}
	if hc.pairIdx, _, err = sole(hdrs.Args[0].Hash, 2, "the header entry type"); err != nil {
		return hc, err
	}
	listDef, err := st.GetDef(hdrs.Hash)
	if err != nil {
		return hc, err
	}
	hc.nilIdx, hc.consIdx = -1, -1
	if len(listDef.Ctors) == 2 {
		for i, fields := range listDef.Ctors {
			switch len(fields) {
			case 0:
				hc.nilIdx = i
			case 2:
				hc.consIdx = i
			}
		}
	}
	if hc.nilIdx < 0 || hc.consIdx < 0 {
		return hc, fmt.Errorf("the header list type does not have the empty and two-field " +
			"constructors SPEC §14.1a declares")
	}
	return hc, nil
}

// emitLLVM lowers a compiled program to textual LLVM IR.
func emitLLVM(st *Store, prog *CompiledProgram) (string, error) {
	// The shape decides everything about the entry's interface. Asked first, so
	// an undecided shape is reported before the artifact is stamped.
	ent, err := llvmEntryFor(prog)
	if err != nil {
		return "", err
	}
	if err := prog.stampBackend(llvmBackendVersion); err != nil {
		return "", err
	}
	if err := llvmCheckValueBindings(prog); err != nil {
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
	// TWO ENTRY PROTOCOLS, TWO DERIVATIONS, and the shape chooses. Neither is a
	// special case of the other: a CLI entry's argument is a (List Str) and a
	// handler's is a Request, so asking one derivation for the other's indices
	// is a category error rather than a missing branch.
	var nilIdx, consIdx int
	var hc handlerCtors
	if ent.handler {
		hc, err = handlerCtorIndices(st, prog)
	} else {
		nilIdx, consIdx, err = listCtorIndices(st, prog)
	}
	if err != nil {
		return "", err
	}

	e := &llvmEmitter{st: st, fname: map[string]string{}, strHash: strTypeHash(st)}
	// Constructor indices are DERIVED, never assumed. A store whose Str declares
	// SCons first would otherwise have every non-empty literal folded to "" —
	// a silently wrong program, which is the one outcome this backend refuses to
	// risk.
	if e.strHash != "" {
		if err := e.resolveStrCtors(); err != nil {
			return "", err
		}
	}
	if err := e.plan(prog.EntryHash); err != nil {
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
	// Keyed on the SHAPE, not the requirement count. An entry typed
	// (-> {} (-> (List Str) Str)) requires nothing and still TAKES a capability
	// argument: its arity comes from its type. The Go backend had this exact bug
	// and it is no less wrong here — argv would be passed where the record belongs
	// and the program would print a closure.
	var caps string
	if ent.caps {
		e.label("entry")
		arr := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_fields(i32 %d)\n", arr, len(prog.Requirements))
		for i, r := range prog.Requirements {
			fn := e.strConst(r.Field)
			kn := e.strConst(string(r.Kind))
			v := e.next()
			if r.Kind == capRequiredValue {
				// The host binding is computed HERE and passed as a literal —
				// the C runtime does no string manipulation and cannot disagree
				// with the Go backend about what it is looking for.
				ev := e.strConst(llvmValueEnvVar(r.Field))
				fmt.Fprintf(&e.b, "  %s = call ptr @o_require_value(ptr %s, ptr %s, ptr %s)\n",
					v, fn, kn, ev)
			} else {
				fmt.Fprintf(&e.b, "  %s = call ptr @o_require(ptr @%s, ptr %s, ptr %s)\n",
					v, providers[i].Fn, fn, kn)
			}
			fmt.Fprintf(&e.b, "  call void @o_set(ptr %s, i32 %d, ptr %s)\n", arr, i, v)
		}
		rec := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_ctor(i32 -1, i32 %d, ptr %s)\n", rec, len(prog.Requirements), arr)
		fmt.Fprintf(&e.b, "  ret ptr %s\n}\n\n", rec)
		caps = "define ptr @o_resolve_caps() {\n" + e.b.String()
		e.b = strings.Builder{}
	}

	// main.
	//
	// A HANDLER'S main IS A HANDOFF, not a loop written here, and the reason is
	// on o_serve: SPEC §14.2 requires a runtime refusal to become a 500 rather
	// than an exit, the C that unwinds it needs a frame that stays live across
	// the handler call, and a frame this function called and returned from is
	// not one. What the emitter keeps is what the emitter owns — WHICH entry to
	// call, and the constructor indices derived from this store.
	//
	// A CAPABILITY-FIRST HANDLER HANDS OVER ONE THING MORE (#173): its resolver,
	// so the runtime can provision before it binds and place the record in the
	// process-lifetime region. The two handoffs differ only in that argument,
	// which is why they are one branch and not two mains.
	if ent.handler {
		e.label("entry")
		entry := e.fname[prog.EntryHash]
		out := e.next()
		idx := fmt.Sprintf("i32 %d, i32 %d, i32 %d, i32 %d, i32 %d", hc.nilIdx, hc.consIdx, hc.pairIdx, hc.reqIdx, hc.respIdx)
		if ent.caps {
			// THE RESOLVER IS HANDED OVER, NOT CALLED HERE (#173). A
			// capability-first handler's record must be resolved before the
			// listener binds AND must outlive every request, and both are
			// properties of the SEQUENCE rather than of the resolution — so the
			// runtime owns the sequence and is given the two pieces it cannot
			// know: what to resolve, and which entry to apply it to. Calling
			// @o_resolve_caps here instead would allocate the record in whichever
			// region happened to be open, which is exactly the mistake the region
			// exists to make impossible.
			fmt.Fprintf(&e.b, "  %s = call i32 @o_serve_caps(ptr @o_resolve_caps, ptr %s, %s)\n",
				out, entry, idx)
		} else {
			fmt.Fprintf(&e.b, "  %s = call i32 @o_serve(ptr %s, %s)\n", out, entry, idx)
		}
		fmt.Fprintf(&e.b, "  ret i32 %s\n}\n", out)
		return llvmAssemble(prog, e, &body, caps, "define i32 @main(i32 %argc, ptr %argv) {\n"+e.b.String())
	}
	e.label("entry")
	args := e.next()
	fmt.Fprintf(&e.b, "  %s = call ptr @o_argv(i32 %%argc, ptr %%argv, i32 %d, i32 %d)\n", args, nilIdx, consIdx)
	entry := e.fname[prog.EntryHash]
	out := e.next()
	if ent.caps {
		c := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr @o_resolve_caps()\n", c)
		inner := e.next()
		fmt.Fprintf(&e.b, "  %s = call ptr %s(ptr null, ptr %s)\n", inner, entry, c)
		fmt.Fprintf(&e.b, "  %s = call ptr @o_apply(ptr %s, ptr %s)\n", out, inner, args)
	} else {
		fmt.Fprintf(&e.b, "  %s = call ptr %s(ptr null, ptr %s)\n", out, entry, args)
	}
	// THE REQUEST ARENA IS RELEASED HERE, AND THE ORDER IS THE WHOLE ARGUMENT
	// (#165). `o_print` is this entry shape's SERIALISATION: it turns the answer
	// into octets. Only after that is nothing live, which is why the release is
	// sound without any escape analysis over values — and why moving this call
	// above the print would hand `o_print` freed memory. A handler entry will put
	// its release at the same place relative to its own octet boundary, not at
	// the same line number.
	fmt.Fprintf(&e.b, "  call void @o_print(ptr %s)\n", out)
	fmt.Fprintf(&e.b, "  call void @o_arena_release()\n  ret i32 0\n}\n")
	mainFn := "define i32 @main(i32 %argc, ptr %argv) {\n" + e.b.String()
	return llvmAssemble(prog, e, &body, caps, mainFn)
}

// llvmAssemble writes the module around whatever main the entry protocol
// produced. Shared by both protocols DELIBERATELY: provenance, the linker
// anchor, the constant pool and the emission order are facts about the
// ARTIFACT, and a second copy for the handler is where an artifact that carries
// no manifest would come from.
func llvmAssemble(prog *CompiledProgram, e *llvmEmitter, body *strings.Builder, caps, mainFn string) (string, error) {
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
	// ANCHORED BY DIRECTIVE, NOT BY A RUNTIME SLOT (#165). The manifest must
	// survive linking, and it used to be held by `o_keep` writing a file-scope
	// pointer. That slot was program-lifetime and MUTABLE, so it was somewhere a
	// capability could park request memory past the point an arena would release
	// it — and seven rounds of scanning for ways in kept finding new ones,
	// because the population is defined by C, not by the shapes a regex knows.
	// @llvm.used tells the linker directly and allocates nothing, so the class is
	// removed rather than guarded.
	fmt.Fprintf(&src, "@llvm.used = appending global [1 x ptr] [ptr @oath_provenance], section \"llvm.metadata\"\n\n")
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
