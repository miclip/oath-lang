package main

import (
	"encoding/json"
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
// Entry protocol: main : (-> (List Str) Str). argv (after the program name)
// becomes the list; the result is written to stdout with a trailing newline.
// Exit code 0.
//
// THIS FILE IS THE GO BACKEND, AND ONLY THAT. What a capability is, which ones a
// program requires, and what the provenance manifest records live in program.go
// and name no Go construct; here they become imports, providers and emitted
// source. The rule the split enforces (#114): no new language or capability
// semantics may be defined in terms of Go constructs. When adding to this file,
// ask whether you are describing Oath or describing Go — the first belongs next
// door.
//
// Compilation model: type-erased. Values are Go `any` (int64, bool, string,
// *closure, *ctorV); each Oath function becomes one Go function taking and
// returning `any`, generics erased, matches by constructor index, direct
// recursion. Not fast-path native — but genuinely compiled, and the
// differential gate (`compiled output == oath eval output`) keeps it honest.

func cmdBuild(st *Store, name, out, backend string) {
	// The front half is backend-neutral: gates, entry classification, required
	// authority, provenance. See program.go — nothing there knows Go exists, and
	// both backends below consume exactly this.
	prog, err := planProgram(st, name)
	if err != nil {
		fail(err)
	}
	if out == "" {
		out = name
	}
	if backend == "llvm" {
		if err := llvmBuild(st, prog, out); err != nil {
			fail(err)
		}
		reportBuild(prog, out)
		return
	}
	src, err := emitProgram(st, prog)
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
	abs, err := filepath.Abs(out)
	if err != nil {
		fail(err)
	}
	cmd := exec.Command("go", "build", "-o", abs, ".")
	cmd.Dir = tmp
	if b, err := cmd.CombinedOutput(); err != nil {
		fail(fmt.Errorf("go build failed:\n%s", string(b)))
	}
	reportBuild(prog, out)
}

// reportBuild says what was built and what authority it will demand. Shared by
// both backends: the facts are properties of the artifact, so they must not read
// differently depending on which lowering produced it.
func reportBuild(prog *CompiledProgram, out string) {
	p := prog.Provenance
	fmt.Printf("built %s → %s  (entry %s : %s, guarantee: %s, backend: %s)\n",
		prog.Entry, out, prog.Entry, p.EntryType, p.Guarantee, p.Backend)
	// What authority this executable will demand, named neutrally. Printed even
	// when empty: "requires: nothing" is the interesting answer for most programs,
	// and silence would leave a reader unable to tell it from an unchecked build.
	if len(prog.Requirements) == 0 {
		fmt.Printf("  requires: no capabilities\n")
	} else {
		var rs []string
		for _, r := range prog.Requirements {
			rs = append(rs, fmt.Sprintf("%s (%s)", r.Field, r.Kind))
		}
		fmt.Printf("  requires: %s\n", strings.Join(rs, ", "))
		fmt.Printf("  every one is resolved before the entry point runs, or the program exits %d.\n", exitCapabilityUnavailable)
	}
	// The sidecar is a convenience for tooling that would rather read a file than
	// scan a binary. It is NOT the record — the executable carries that, and a
	// sidecar can be lost, edited, or paired with the wrong artifact. `oath
	// provenance <binary>` reads what the artifact itself says.
	//
	// Which is why failing to write it is a warning, not a build failure: the
	// executable exists and already carries the authoritative manifest, and
	// reporting a successful build as failed over a convenience file would leave a
	// caller believing it has no artifact when it has one.
	abs, err := filepath.Abs(out)
	if err != nil {
		abs = out
	}
	sidecar := abs + ".provenance.json"
	if err := os.WriteFile(sidecar, p.manifestJSON(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write %s (%v)\n"+
			"  The executable was built and carries its provenance; read it with `oath provenance %s`.\n",
			sidecar, err, out)
	}
	fmt.Printf("  provenance: %s  (oath provenance %s)\n", p.digest(), out)
}

// cmdProvenance reads the manifest an artifact carries, WITHOUT executing it.
// That ordering is the point: running an unknown binary to discover what it is
// asks you to trust it before you have any grounds to.
func cmdProvenance(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	raw, err := extractProvenance(b)
	if err != nil {
		fail(fmt.Errorf("%s: %w", path, err))
	}
	// Say plainly when the record names authority this build cannot interpret.
	// Printing it without comment would let a reader conclude they understood what
	// the artifact holds when they only understood part of it.
	var m ProvenanceManifest
	if json.Unmarshal([]byte(raw), &m) == nil {
		if unknown := m.unknownRequirements(); len(unknown) > 0 {
			fmt.Fprintf(os.Stderr, "warning: this artifact requires capabilities %s does not define: %s\n"+
				"  It was built by a newer compiler. The record is shown as found; this build\n"+
				"  cannot tell you what that authority means.\n",
				kernelVersion, strings.Join(unknown, ", "))
		}
	}
	fmt.Println(raw)
}

// ---------- the Go backend's capability providers ----------
//
// This is where authority becomes Go, and it is the ONLY place that is allowed
// to. Everything above it — what a capability is, which ones a program requires,
// what the manifest records — lives in program.go and names no Go construct.
// The table is keyed by capabilityKind, not by field name: a backend answers
// "how do I supply http_request", never "what does `fetch` mean".
//
// A later backend (#115) adds a different table against the same keys. That is
// the entire purpose of the split, and the reason the key is a kind.

// goBackendVersion identifies this backend in the provenance manifest, so an
// artifact says which lowering produced it without the manifest describing Go.
const goBackendVersion = "go-emit/2"

// exitCapabilityUnavailable is the status a compiled program exits with when a
// required capability cannot be provided. 70 is sysexits.h EX_UNAVAILABLE — "a
// service the program needs is not available" — chosen so a supervisor can tell
// a launch refusal from an ordinary program failure without parsing stderr.
const exitCapabilityUnavailable = 70

// goProvider is the Go backend's implementation of one capability kind.
type goProvider struct {
	// Provide is a Go expression of type func() (any, error). It runs ONCE, at
	// launch, before any Oath code: it returns the capability value, or an error
	// saying why THIS host cannot supply it.
	//
	// The (any, error) shape is the fix for the defect this pass exists to close.
	// The old wiring returned a capability that answered "" forever when the host
	// could not help, which made "the call succeeded and the result was empty"
	// and "this host has no such authority" the same observable state. Provision
	// failure is now a distinct channel that cannot be mistaken for a value —
	// because it is not a value, and the program does not start.
	Provide string
	// Imports are the Go packages Provide needs beyond the always-present set.
	// Requirement-driven imports are not tidiness: a program that does not
	// require http_request must not LINK an HTTP client, which is the static half
	// of the confinement claim.
	Imports []string
}

var goProviders = map[capabilityKind]goProvider{
	capProcessEnv: {
		// Reading the environment cannot fail at provision time, and a missing
		// variable is an ordinary empty result — the host has the authority
		// whether or not the variable exists.
		Provide: `func() (any, error) { return capFn(os.Getenv), nil }`,
	},

	capFileRead: {
		// Provision is the authority to read files at all, which this host has.
		// Whether one PATH exists is a call-time question and stays one.
		Provide: `func() (any, error) {
	return capFn(func(s string) string {
		b, err := os.ReadFile(s)
		if err != nil { return oathCapFailure }
		return string(b)
	}), nil
}`,
	},

	capHTTPRequest: {
		Provide: `func() (any, error) {
	return capFn(func(s string) string {
		resp, err := http.Get(s)
		if err != nil { return oathCapFailure }
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil { return oathCapFailure }
		return string(b)
	}), nil
}`,
		Imports: []string{"io", "net/http"},
	},

	capRecordSink: {
		// #78 step 3: the minimum WRITE capability — append one record to a sink
		// and say whether it landed. Sink is OATH_EMIT_PATH, or stdout when unset.
		//
		// This is the provider with a REAL precondition, and it is why the launch
		// gate is not ceremony: a path the host cannot write is a provision
		// failure and the program never starts. Previously an unwritable path
		// meant every emit silently returned the failure value for the life of the
		// process — a program reporting success while writing nowhere.
		//
		// PROVISION AND WRITING ARE SEPARATE, deliberately. The launch check opens
		// the sink and closes it again; each emit reopens by PATH. Holding the
		// descriptor would have been simpler and would have quietly changed what a
		// sink means: a rotated log (renamed, replaced) leaves a held descriptor
		// writing to the old inode forever, so records stop reaching the path the
		// host configured while the program still reports success. Following the
		// path on every write is the behaviour a sink is expected to have; the
		// launch check exists to answer a different question, once.
		Provide: `func() (any, error) {
	path := os.Getenv("OATH_EMIT_PATH")
	if path == "" {
		return capFn(func(s string) string { fmt.Println(s); return "ok" }), nil
	}
	probe, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("sink %s cannot be opened for append: %v", path, err)
	}
	probe.Close()
	return capFn(func(s string) string {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil { return oathCapFailure }
		defer f.Close()
		if _, err := f.WriteString(s + "\n"); err != nil { return oathCapFailure }
		return "ok"
	}), nil
}`,
	},
}

// goProviderFor resolves a requirement to this backend's implementation.
//
// A miss is a COMPILE failure, which is strictly stronger than the launch failure
// #114 asks for: the requirement is known to Oath but this backend cannot lower
// it, and no amount of runtime environment will change that. Unreachable while
// every vocabulary kind has a provider — capabilityVocabularyIsProvidable in the
// tests holds that — and kept because the failure it guards is a backend added
// without one, which is a mistake a future contributor makes, not a user.
func goProviderFor(r CapabilityRequirement) (goProvider, error) {
	p, ok := goProviders[r.Kind]
	if !ok {
		return goProvider{}, fmt.Errorf("capability %s (%s) has no implementation in the %s backend",
			r.Field, r.Kind, goBackendVersion)
	}
	return p, nil
}

// ---------- emitter ----------

type emitter struct {
	st      *Store
	b       strings.Builder
	fname   map[string]string // def hash → emitted Go function name
	order   []string          // emission order (deps first)
	seen    map[string]bool
	strHash string           // hash of the Str datatype; its values compile to Go strings
	setHash string           // hash of the Set datatype; its values compile to native osets
	mapHash string           // hash of the Map datatype; its values compile to native omaps
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

// goImports computes the import set for one program: the fixed runtime support,
// plus whatever the ingress protocol and the DECLARED REQUIREMENTS need.
//
// The requirement-driven part is load-bearing rather than cosmetic. #114's
// acceptance test asks for a program verified to receive only capability A,
// launched in a host that possesses A and B, that cannot observe or invoke B. The
// strongest available answer is that B is not in the binary: a program requiring
// only `env` links no HTTP client, so there is no reachable code path to an
// outbound request, whatever the host is capable of. A fixed import list made that
// claim untrue for every program ever built by this compiler.
func goImports(prog *CompiledProgram) []string {
	// Always present: the type-erased value representation and the numeric,
	// string and container helpers every emitted program carries.
	need := map[string]bool{
		"fmt": true, "math": true, "math/big": true,
		"crypto/hmac": true, "crypto/sha256": true, "crypto/subtle": true,
		"os": true, "sort": true, "unicode/utf8": true,
	}
	if prog.Protocol == entryHandler {
		// The host owns the socket: serving is the ingress protocol, not a
		// capability the program holds. See entryKind.
		need["net/http"], need["io"], need["time"] = true, true, true
	}
	for _, r := range prog.Requirements {
		if p, ok := goProviders[r.Kind]; ok {
			for _, imp := range p.Imports {
				need[imp] = true
			}
		}
	}
	out := make([]string, 0, len(need))
	for imp := range need {
		out = append(out, imp)
	}
	sort.Strings(out)
	return out
}

// goImportKeepalive names one symbol per ALWAYS-PRESENT import that the fixed
// preamble might not otherwise reference, so an unused import never breaks the
// build.
//
// The conditional imports — io, net/http, time — are deliberately absent, and
// this is a confinement matter rather than tidiness. They are included only when
// something emitted actually uses them (the handler adapter, or the http_request
// provider), so they need no keepalive; and naming `http.Get` here would have
// planted a reference to the OUTBOUND CLIENT in every handler, which links
// net/http to serve. A server-only handler declares no http_request and must not
// carry one: "the authority is absent from the artifact" has to survive contact
// with the artifact's own import list.
//
// Every symbol below is arithmetic, sorting, hashing or text — never authority.
var goImportKeepalive = map[string]string{
	"unicode/utf8":  "utf8.DecodeRuneInString",
	"math":          "math.Float64bits",
	"sort":          "sort.Slice",
	"crypto/subtle": "subtle.ConstantTimeCompare",
	"crypto/hmac":   "hmac.New",
	"crypto/sha256": "sha256.New",
}

func emitProgram(st *Store, prog *CompiledProgram) (string, error) {
	// This backend claims the artifact. Done before anything is emitted so an
	// incomplete record stops the build rather than reaching a binary.
	if err := prog.stampBackend(goBackendVersion); err != nil {
		return "", err
	}
	e := &emitter{st: st, fname: map[string]string{}, seen: map[string]bool{}, strHash: strTypeHash(st)}
	e.resolveNativeContainers()
	if err := e.closure(prog.EntryHash); err != nil {
		return "", err
	}
	// Resolve every requirement to a provider BEFORE emitting anything. A
	// requirement this backend cannot lower must stop the build, not produce a
	// program with a hole in it.
	providers := make([]goProvider, len(prog.Requirements))
	for i, r := range prog.Requirements {
		p, err := goProviderFor(r)
		if err != nil {
			return "", err
		}
		providers[i] = p
	}

	e.b.WriteString("// Generated by oath build — do not edit.\n")
	e.b.WriteString("// Values: int64 | bool | string | *closure | *ctorV (type-erased).\n")
	e.b.WriteString("package main\n\nimport (\n")
	imports := goImports(prog)
	for _, imp := range imports {
		fmt.Fprintf(&e.b, "\t%q\n", imp)
	}
	e.b.WriteString(")\n\n")
	for _, imp := range imports {
		if sym, ok := goImportKeepalive[imp]; ok {
			fmt.Fprintf(&e.b, "var _ = %s\n", sym)
		}
	}
	e.b.WriteString(`
// oathCapFailure is the CALL-failure value in Oath's capability protocol: a
// capability that was provided, was invoked, and could not complete. It is an
// ordinary Str the program may branch on.
//
// It is NOT how an unavailable capability is reported. A capability this host
// cannot supply is refused before any Oath code runs, so that case never reaches
// this program as a value at all.
const oathCapFailure = ""
`)
	e.b.WriteString(`
// oBytes reads a (List Int) value as raw bytes; oList rebuilds one. Elements
// outside 0..255 PANIC rather than truncate: a digest over silently truncated
// input would verify against a message nobody sent.
func oBytes(v any) []byte {
	var out []byte
	for cur, _ := v.(*ctorV); cur != nil && cur.idx == 1; cur, _ = cur.fields[1].(*ctorV) {
		n, ok := cur.fields[0].(*big.Int)
		if !ok || !n.IsInt64() || n.Int64() < 0 || n.Int64() > 255 {
			panic("byte list element out of range 0..255")
		}
		out = append(out, byte(n.Int64()))
	}
	return out
}

func oList(b []byte) any {
	var out any = &ctorV{idx: 0}
	for i := len(b) - 1; i >= 0; i-- {
		out = &ctorV{idx: 1, fields: []any{big.NewInt(int64(b[i])), out}}
	}
	return out
}

func oHmac(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

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
	// The provenance manifest, embedded so the artifact describes itself. Quoted
	// rather than written as a raw literal: the manifest carries type strings and
	// names from the store, and a raw literal would be one backtick away from
	// emitting a source file that does not compile.
	//
	// It is inert. The program has no provenance flag and reads no provenance
	// environment variable — see the markers in program.go for why both would take
	// authority over a channel already granted to the program. `oath provenance`
	// reads it out of the file without running anything.
	fmt.Fprintf(&e.b, `
// oathProvenance is what this executable was built from, recorded at build time.
// It names NEUTRAL capability kinds, never the Go implementations below: it
// describes the artifact, and stays true when the backend changes.
//
// Read it with: oath provenance <this file>
var oathProvenance = %s

// oathProvenanceKeep exists only so the linker keeps the manifest. Go drops
// string data nothing references, and an artifact that cannot say what it was
// built from is not carrying provenance — so the reference has to be real. It is
// placed in an init rather than on a path main could reach, because provenance
// must never be able to change what the program does.
var oathProvenanceKeep []string

func init() { oathProvenanceKeep = append(oathProvenanceKeep, oathProvenance) }
`, strconv.Quote(prog.Provenance.embeddedManifest()))

	// The capability boundary. Authority enters the program exactly once, here —
	// everything below received it as an ordinary argument and was verified
	// against all simulated worlds before the real one arrived.
	caps := ""
	entryArg := "args"
	if prog.Protocol == entryHandler {
		entryArg = "req"
	}
	entryCall := fmt.Sprintf("%s(nil, %s)", e.fname[prog.EntryHash], entryArg)
	// Keyed on the RECORD, not on the requirement count. An entry typed
	// (-> {} (-> (List Str) Str)) requires nothing and still takes a capability
	// argument: its arity comes from its type, and skipping the record would pass
	// argv where the record belongs and hand the caller a closure to assert a
	// string on. "Requires nothing" is a statement about authority; "takes no
	// argument" is a statement about shape, and they are not the same statement.
	if prog.CapTy != nil {
		var slots []string
		for i, r := range prog.Requirements {
			slots = append(slots, fmt.Sprintf("\t{field: %q, kind: %q, provide: %s},",
				r.Field, string(r.Kind), providers[i].Provide))
		}
		// THE INVARIANT, made executable: every declared requirement is resolved
		// exactly once, before launch, or the process exits.
		//
		// Exactly once is structural — one slot per requirement, in the record's
		// own field order, built once and applied once. "Or it does not start" is
		// the loop below, and it is the difference between a capability system and
		// a naming convention: an unprovidable capability is not handed to Oath
		// code as an inert value that answers "" forever.
		//
		// Extra host authority is not rejected here because it cannot arrive
		// here: the record has exactly len(oathRequired) fields, the emitted
		// program reaches authority only by projecting that record, and a
		// capability this program did not declare has no provider compiled in and
		// often no import either.
		fmt.Fprintf(&e.b, `
// oathCapability is one required unit of authority and this host's means of
// supplying it. The kind is Oath's word for the authority; the provider is Go's
// answer to it, and nothing above this line depends on that answer.
type oathCapability struct {
	field   string
	kind    string
	provide func() (any, error)
}

// oathRequired is derived from the entry point's type — the same derivation
// recorded in oathProvenance. Order is the capability record's field order,
// because that is how the value is delivered.
var oathRequired = []oathCapability{
%s
}

// oathResolveCapabilities resolves every requirement before a line of Oath runs,
// and refuses to launch if any cannot be met. A provision failure is not an Oath
// value and never becomes one.
func oathResolveCapabilities() any {
	fields := make([]any, len(oathRequired))
	for i, c := range oathRequired {
		v, err := c.provide()
		if err != nil {
			fmt.Fprintf(os.Stderr, "oath: this host cannot provide required capability %%s (%%s): %%v\n", c.field, c.kind, err)
			os.Exit(%d)
		}
		if v == nil {
			fmt.Fprintf(os.Stderr, "oath: provider for required capability %%s (%%s) supplied nothing\n", c.field, c.kind)
			os.Exit(%d)
		}
		fields[i] = v
	}
	return &ctorV{idx: -1, fields: fields}
}
`, strings.Join(slots, "\n"), exitCapabilityUnavailable, exitCapabilityUnavailable)
		caps = "\tvar realWorld any = oathResolveCapabilities()\n"
		entryCall = fmt.Sprintf("apply(%s(nil, realWorld), %s)", e.fname[prog.EntryHash], entryArg)
	}
	if prog.Protocol == entryHandler {
		// HANDLER protocol (#78). The host owns the socket, TLS, routing and
		// process lifecycle; the artifact is a pure function from a Request
		// VALUE to a Response value. This adapter is the whole irreversible
		// translation, and it deliberately normalizes as little as it can:
		// the body crosses as raw BYTES, the path keeps its query, and
		// received-at is stamped once and handed over as data.
		//
		// One normalization is NOT avoidable at this layer and is therefore
		// documented rather than hidden: Go's net/http canonicalizes header
		// KEYS (content-type → Content-Type) and stores them in a map, so
		// cross-key order is lost. Repeats within a key keep their order, and
		// keys are emitted in sorted order so the value is deterministic.
		// Body bytes — what signature schemes actually sign — are exact.
		// Recovering byte-exact header casing and interleaving needs a raw
		// connection reader; see the limitation note on #78.
		// Capabilities are resolved before the listener binds, not per request:
		// a host that cannot supply the program's authority must fail to launch,
		// not accept traffic and fail each request individually.
		fmt.Fprintf(&e.b, `
func main() {
	addr := os.Getenv("OATH_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
%s	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body.Close()

		// body: raw bytes, one Int per byte, built tail-first
		var body any = &ctorV{idx: 0}
		for i := len(raw) - 1; i >= 0; i-- {
			body = &ctorV{idx: 1, fields: []any{big.NewInt(int64(raw[i])), body}}
		}

		// headers: (List (Pair Str Str)), sorted by key, repeats in order
		keys := make([]string, 0, len(r.Header))
		for k := range r.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var flatK, flatV []string
		for _, k := range keys {
			for _, v := range r.Header[k] {
				flatK = append(flatK, k)
				flatV = append(flatV, v)
			}
		}
		var hs any = &ctorV{idx: 0}
		for i := len(flatK) - 1; i >= 0; i-- {
			pair := &ctorV{idx: 0, fields: []any{flatK[i], flatV[i]}}
			hs = &ctorV{idx: 1, fields: []any{pair, hs}}
		}

		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path = path + "?" + r.URL.RawQuery
		}
		var req any = &ctorV{idx: 0, fields: []any{
			r.Method, path, hs, body, big.NewInt(time.Now().Unix()),
		}}

		resp, ok := (%s).(*ctorV)
		if !ok || len(resp.fields) != 3 {
			http.Error(w, "handler returned a malformed Response", 500)
			return
		}
		status := 500
		if n, ok := resp.fields[0].(*big.Int); ok && n.IsInt64() {
			status = int(n.Int64())
		}
		for cur, _ := resp.fields[1].(*ctorV); cur != nil && cur.idx == 1; cur, _ = cur.fields[1].(*ctorV) {
			if p, ok := cur.fields[0].(*ctorV); ok && len(p.fields) == 2 {
				hk, _ := p.fields[0].(string)
				hv, _ := p.fields[1].(string)
				w.Header().Add(hk, hv)
			}
		}
		var out []byte
		for cur, _ := resp.fields[2].(*ctorV); cur != nil && cur.idx == 1; cur, _ = cur.fields[1].(*ctorV) {
			if n, ok := cur.fields[0].(*big.Int); ok {
				out = append(out, byte(n.Int64()))
			}
		}
		w.WriteHeader(status)
		w.Write(out)
	})
	fmt.Fprintf(os.Stderr, "oath handler listening on %%s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`, caps, entryCall)
		return e.b.String(), nil
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
		// any(...) first: the scrutinee expression may already have the CONCRETE
		// type *ctorV — a directly-constructed value, as in (match (Box x) ...) —
		// and Go forbids a type assertion on a non-interface. Found by the LLVM
		// backend, which compiles such a program correctly; the Go backend refused
		// to build it at all.
		b.WriteString("\t\t}\n\t\tpanic(\"non-exhaustive\")\n\t})(any(" + s + ").(*ctorV), env)")
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
	case "hmac-sha256", "bytes-eq-ct":
		// #78: the trusted crypto boundary, compiled to the host's library.
		if t.Op == "bytes-eq-ct" {
			return fmt.Sprintf("any(subtle.ConstantTimeCompare(oBytes(%s), oBytes(%s)) == 1)", args[0], args[1]), nil
		}
		return fmt.Sprintf("oList(oHmac(oBytes(%s), oBytes(%s)))", args[0], args[1]), nil
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
