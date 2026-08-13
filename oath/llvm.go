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
// numeric tower, native containers, the handler protocol, and the http_request
// capability. Refusals are explicit and name what is missing. A backend that
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
			"  Str (literals, matching, and construction from values computed at\n" +
			"  runtime), Bool, the CLI entry protocol, and Int — arbitrary-precision,\n" +
			"  literals of any magnitude, with the binary operations `+ - * / %` and\n" +
			"  `== < <=`. Division truncates toward zero and a zero divisor fails at\n" +
			"  runtime, matching `oath eval`.\n" +
			"  `neg` is refused by name (use `(- 0 x)`), as are Rat and Float,\n" +
			"  Set/Map and the handler protocol. Build with the Go backend for full\n" +
			"  coverage.",
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
		// `neg` stays refused BY NAME. It is unary, so it does not fit either
		// shape here, and `(- 0 x)` already reaches it — which makes it a
		// LOWERING gap rather than a semantic one, and worth saying so instead
		// of quietly adding a third shape for one operation. A backend subset is
		// an honest claim only while what is outside it is named.
		intArith := map[string]string{
			"+": "o_int_add", "-": "o_int_sub", "*": "o_int_mul",
			"/": "o_int_div", "%": "o_int_mod",
		}
		intOrder := map[string]string{"==": "o_int_eq", "<": "o_int_lt", "<=": "o_int_le"}
		arith, isArith := intArith[t.Op]
		order, isOrder := intOrder[t.Op]
		if len(t.Args) == 2 && (isArith || isOrder) {
			// THE TYPE GUARD IS LOAD-BEARING, not a formality. `+ - * < <=` are
			// numeric-OVERLOADED in the language — the same op spells Rat and
			// Float arithmetic — and `==` is polymorphic over everything. So the
			// operand types decide whether this is the Int lowering at all, and
			// a Rat addition must fall through to the refusal below rather than
			// be handed to an integer runtime.
			at, aerr := e.chk.synth(e.ctx, &t.Args[0])
			bt, berr := e.chk.synth(e.ctx, &t.Args[1])
			if aerr == nil && berr == nil && at != nil && bt != nil && at.K == "int" && bt.K == "int" {
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
		}
		return "", llvmUnsupported(reasonPrim, fmt.Sprintf("the primitive operation %q", t.Op))
	}
	return "", llvmUnsupported(reasonTermKind, fmt.Sprintf("%q terms", t.K))
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
// What is new is that the release point EXISTS and is placed by the emitter, so
// the handler protocol — refused below, and refused BECAUSE of the memory model
// rather than the other way round — has somewhere to put per-request release.
// The release is sound for the reason that selected this design over reference
// counting, tracing and region inference: it happens after serialisation, so the
// value that could have escaped is already bytes and no escape analysis over
// VALUES is required.
const llvmRuntimeC = `
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <stddef.h>

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

/* THE ARENA'S SOLE OWNERSHIP ROOT, and the only object of static storage
   duration this runtime declares.

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
     - every OTHER static pointer stays forbidden, because nothing clears one -
       a capability that parked a value in a second slot would hold it past the
       release the whole design turns on.

   The declaration scan in llvm_runtime_state_test.go permits this NAME AT THIS
   TYPE and nothing else, and the clearing half is witnessed separately: an
   exemption for a root that is never cleared would be an exemption for exactly
   the retention this forbids. */
static OBlock *o_arena_blocks;

static void o_oom(void) { fputs("oath: out of memory\n", stderr); exit(70); }

static OBlock *o_block_new(size_t cap) {
  OBlock *nb = (OBlock *)calloc(1, sizeof(OBlock));
  if (!nb) o_oom();
  nb->base = (char *)calloc(1, cap);
  if (!nb->base) { free(nb); o_oom(); }
  nb->cap = cap;
  return nb;
}

static void *xalloc(size_t n) {
  /* The rounding must not wrap: a wrapped size would under-allocate silently,
     which is a heap overflow rather than the out-of-memory exit it looks like. */
  if (n > (size_t)-1 - (O_ARENA_ALIGN - 1)) o_oom();
  size_t need = (n + (O_ARENA_ALIGN - 1)) & ~(O_ARENA_ALIGN - 1);
  OBlock *b = o_arena_blocks;
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
      else { o_arena_blocks = big; }
      return big->base;
    }
    OBlock *nb = o_block_new(O_ARENA_BLOCK);
    nb->next = o_arena_blocks;
    o_arena_blocks = nb;
    b = nb;
  }
  void *p = b->base + b->used;
  b->used += need;
  return p;
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
   an arena existed. */
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
  if (!*p) {
    fputs("oath: the compiler emitted an Int literal with no digits\n", stderr);
    exit(70);
  }
  int cap = 4, n = 0;
  o_u32 *d = o_magalloc(cap);
  for (; *p; p++) {
    if (*p < '0' || *p > '9') {
      fprintf(stderr, "oath: the compiler emitted a malformed Int literal '%s'\n", s);
      exit(70);
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
  exit(70);
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
  exit(70);
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
  if (!v || v->tag != T_STR) { fputs("oath: not a Str\n", stderr); exit(70); }
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
  if (n == 0) { fputs("oath: head of an empty Str\n", stderr); exit(70); }
  return o_int(cp);
}

OVal *o_str_tail(OVal *v) {
  long long cp; int n = o_str_step(v, &cp);
  if (n == 0) { fputs("oath: tail of an empty Str\n", stderr); exit(70); }
  return o_strn(v->s + n, v->slen - n);
}
static OVal *o_int_arg(OVal *v) {
  if (!v || v->tag != T_INT) { fputs("oath: not an Int\n", stderr); exit(70); }
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
  exit(70);
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
  if (!rest || rest->tag != T_STR) {
    fputs("oath: the tail of a Str constructor is not a Str\n", stderr);
    exit(70);
  }
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
  exit(70);
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
    exit(70);
  }
  if (!*v) {
    fprintf(stderr, "oath: this host cannot provide required capability %s (%s): "
                    "required value %s is provided but empty (%s)\n",
            field, kind, field, envvar);
    exit(70);
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
    exit(70);
  }
  return o_str(v);
}

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
declare void @o_arena_release()
declare ptr @o_require(ptr, ptr, ptr)
declare ptr @o_require_value(ptr, ptr, ptr)
declare ptr @o_int(i64)
declare i32 @o_str_idx(ptr)
declare ptr @o_str_head(ptr)
declare ptr @o_str_tail(ptr)
declare ptr @o_str_cons(ptr, ptr)
declare ptr @o_int_dec(ptr)
declare ptr @o_int_add(ptr, ptr)
declare ptr @o_int_sub(ptr, ptr)
declare ptr @o_int_mul(ptr, ptr)
declare ptr @o_int_div(ptr, ptr)
declare ptr @o_int_mod(ptr, ptr)
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
// A REFUSAL IS A CASE. This backend covers a subset, and the shapes it declines
// are written into the same table as the ones it lowers — so declining is
// something the table says rather than something an early `if` in emitLLVM
// remembers to do. That is what makes every consumer of the shape refuse
// consistently: listCtorIndices cannot walk an entry type this backend never
// agreed to compile, because asking for the shape is what surfaces the refusal.
type llvmEntry struct {
	// shape is the case this row decides, and it must equal the row's own index.
	// The array's LENGTH is checked by the compiler; this is what makes a row
	// left at its zero value in the middle of the table detectable. It matters
	// more here than in the Go backend, because llvmEntry's zero value is a
	// LEGITIMATE row — it is exactly the plain CLI answer — so a hole would
	// otherwise be indistinguishable from a decision.
	shape EntryShape

	// refuse, when non-nil, CONSTRUCTS this backend's refusal of the shape. The
	// remaining fields are then unreachable, because llvmEntryFor turns the row
	// into an error rather than a case.
	//
	// It is a constructor rather than a stored refusalReason so that the reason
	// constant appears literally at the construction site: the refusal
	// vocabulary is closed by tracing assignments to declared constants
	// (TestEveryRefusalUsesADeclaredReason), and a reason carried through a
	// struct field is not traceable — correctly, since a computed reason is not
	// vocabulary.
	refuse func() error

	// caps: resolve a capability record and apply it before the argv argument.
	caps bool
	// argvDepth is how many leading arrows to step past in the entry's TYPE
	// before reaching the parameter argv is built for. A capability-first entry
	// is (-> {caps} (-> (List Str) Str)), so its argv parameter is one arrow in.
	argvDepth int
}

// llvmRefuseHandler is this backend's refusal of both handler shapes. Shared,
// because the reason is the same fact about this runtime in both cases — the
// capability record a handler may take changes nothing about it.
func llvmRefuseHandler() error {
	return llvmUnsupported(reasonHandlerProtocol, "the handler protocol\n"+
		"  Handler lowering is not implemented in this backend: it emits no request\n"+
		"  loop and no response-serialization boundary. The old reason was a LIFETIME\n"+
		"  objection — the runtime never freed, so a long-running server would leak\n"+
		"  per request — and the request arena retired it (#165). What remains is\n"+
		"  unwritten lowering, which is a property of this slice's backend and not of\n"+
		"  the protocol")
}

var llvmEntries = [...]llvmEntry{
	shapeCLI:         {shape: shapeCLI, caps: false, argvDepth: 0},
	shapeCLICaps:     {shape: shapeCLICaps, caps: true, argvDepth: 1},
	shapeHandler:     {shape: shapeHandler, refuse: llvmRefuseHandler},
	shapeHandlerCaps: {shape: shapeHandlerCaps, refuse: llvmRefuseHandler},
}

// EXHAUSTIVENESS, at compile time. Adding a shape to the variant makes this a
// type error here, in the LLVM backend, independently of the Go backend's own
// assertion — two backends, two failures, neither derived from the other.
var _ entryShapeTable = [len(llvmEntries)]struct{}{}

// llvmEntryFor is the ONE way this backend learns an entry's shape. It reports
// an undecided shape and a declined one as errors of the appropriate kinds — an
// undecided shape is a defect in this backend, a declined one is this backend's
// subset boundary and carries a typed refusal reason (#134).
func llvmEntryFor(prog *CompiledProgram) (llvmEntry, error) {
	ent, err := entryShapeCase("the LLVM backend", &llvmEntries, prog.Shape)
	if err != nil {
		return llvmEntry{}, err
	}
	if ent.refuse != nil {
		return llvmEntry{}, ent.refuse()
	}
	return ent, nil
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
	// The shape decides where argv is, and whether this backend lowers the entry
	// at all — asked BEFORE the store, so a declined shape is refused as a subset
	// boundary rather than reported as whatever the type walk happens to trip on.
	//
	// The depth comes from the shape rather than from the capability record's
	// nil-ness so that this walk and the capability construction below cannot
	// disagree about where argv is: they read one fact.
	ent, err := llvmEntryFor(prog)
	if err != nil {
		return 0, 0, err
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
	ty := d.Ty
	for i := 0; i < ent.argvDepth; i++ {
		if ty == nil || ty.K != "fun" {
			return 0, 0, fmt.Errorf("entry is not %s: it has fewer than %d leading arrows", prog.Shape, ent.argvDepth+1)
		}
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
	// The shape decides everything about the entry's interface, including whether
	// this backend lowers it at all. Asked first, so a declined shape refuses
	// before the artifact is stamped.
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
	nilIdx, consIdx, err := listCtorIndices(st, prog)
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
