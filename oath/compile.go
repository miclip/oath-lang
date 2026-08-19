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
		fmt.Printf("  every one is resolved before the entry point runs, or the program exits %d.\n", exitHostRefusal)
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

// exitHostRefusal is the status a compiled artifact exits with when the HOST
// refuses: it cannot supply a required capability, or it cannot carry out
// something the program asked of it. 70 is sysexits.h EX_UNAVAILABLE — "a
// service the program needs is not available" — chosen so a supervisor can tell
// a refusal from an ordinary program failure without parsing stderr.
//
// ONE constant, covering BOTH clocks, because a supervisor reads one number:
// launch-time provisioning failure (the program never starts) and run-time host
// refusal (the program stops) are both "this host could not complete the
// artifact", and the LLVM backend has always used 70 for both. It was named
// exitCapabilityUnavailable while only the launch half went through it, which
// made the runtime half look like a different contract instead of an unwired
// one (#167).
//
// It is the sole authority for the number in this backend: the emitted runtime
// gets `oathExitRefusal` formatted FROM this constant rather than a second 70
// written down beside it.
const exitHostRefusal = 70

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
		Provide: `func() (any, error) {
	return capFn(func(k string) string {
		return oathStrFromHost("environment variable "+k, os.Getenv(k))
	}), nil
}`,
	},

	capFileRead: {
		// Provision is the authority to read files at all, which this host has.
		// Whether one PATH exists is a call-time question and stays one.
		Provide: `func() (any, error) {
	return capFn(func(s string) string {
		b, err := os.ReadFile(s)
		if err != nil { return oathCapFailure }
		return oathStrFromHost("the contents of "+s, string(b))
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
		return oathStrFromHost("the response body from "+s, string(b))
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
// valueEnvVar is this backend's default binding from a capability field to a
// host source: `secret` <- OATH_VALUE_SECRET. It is a Go-backend decision, not
// an Oath one — the LLVM backend makes the same choice independently, and a
// third backend could source values from somewhere else entirely without
// changing any artifact.
func valueEnvVar(field string) string {
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
			// `-` is legal in an Oath identifier and not in an environment
			// variable name on most shells.
			out = append(out, '_')
		}
	}
	return string(out)
}

func goProviderFor(r CapabilityRequirement) (goProvider, error) {
	if r.Kind == capRequiredValue {
		// Both names are computed HERE, at compile time, and embedded as
		// literals: the emitted program needs no string manipulation and no
		// `strings` import to know what it is looking for.
		return goProvider{Provide: fmt.Sprintf(
			"func() (any, error) { return oathProvideValue(%q, %q) }",
			r.Field, valueEnvVar(r.Field))}, nil
	}
	p, ok := goProviders[r.Kind]
	if !ok {
		return goProvider{}, newCapabilityRefusal(goBackendVersion, r.Field, r.Kind, nil)
	}
	return p, nil
}

// ---------- emitter ----------

type emitter struct {
	st      *Store
	b       strings.Builder
	fname   map[string]string // def hash → emitted Go function name
	order   []string          // emission order, from the neutral emissionOrder
	strHash string            // hash of the Str datatype; its values compile to Go strings
	nc      nativeContainers  // neutral Set/Map recognition; ops lower to oset/omap helpers
	// Type tracking for record field resolution: the kernel's own checker,
	// threaded alongside compilation. ctx mirrors the de Bruijn env.
	chk *checkerMachine
	ctx []*Ty
}

// goSetHelpers maps each neutral Set/Map operation name (program.go's
// nativeOpArity) to the Go runtime helper it lowers to. A Set flows at runtime
// as an oset and a Map as an omap; these helpers keep values in that form.
var goSetHelpers = map[string]string{
	"set-empty": "osetEmpty", "set-member": "osetMember", "set-add": "osetAdd",
	"set-union": "osetUnion", "set-inter": "osetInter", "set-size": "osetSize",
	"set-elems": "osetElems",
	"map-empty": "omapEmpty", "map-insert": "omapInsert", "map-lookup": "omapLookup",
	"map-has": "omapHas", "map-keys": "omapKeys", "map-values": "omapValues",
	"map-size": "omapSize", "map-merge": "omapMerge",
}

// goSetHelperNames is the set of container operations THIS backend lowers — the
// keys of goSetHelpers. resolveNativeContainers recognizes only these, so the
// Go backend cannot prune an operation it has no helper for. Every operation in
// nativeOpArity currently has a helper; the filter keeps that a checked fact
// rather than an assumption, and stays correct if either table changes.
func goSetHelperNames() map[string]bool {
	out := make(map[string]bool, len(goSetHelpers))
	for name := range goSetHelpers {
		out[name] = true
	}
	return out
}

// checkValueBindings refuses a program whose required values would collide in
// this backend's host bindings.
//
// The mapping is lossy — it uppercases letters and replaces everything else
// with `_` — so `api-key` and `api_key` both become OATH_VALUE_API_KEY. Two
// distinct requirements would then read one variable and silently receive the
// same value, which is unconfigurable rather than merely surprising.
//
// It is a BUILD failure, not a launch failure: no environment can repair it,
// and the program is asking for something this backend cannot express. A
// backend keying a secret manager by exact field name would have no collision
// and no need for this check, which is why it lives here and not in the
// neutral layer.
func checkValueBindings(prog *CompiledProgram) error {
	seen := map[string]string{}
	for _, r := range prog.Requirements {
		if r.Kind != capRequiredValue {
			continue
		}
		env := valueEnvVar(r.Field)
		if prev, dup := seen[env]; dup {
			return &backendRefusal{
				Reason:  reasonValueBinding,
				Backend: goBackendVersion,
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

// ---------- this backend's answer for each entry shape ----------

// goEntry is the Go backend's DECISION for one EntryShape: what main binds the
// entry's own input to, whether authority is resolved and applied first, which
// main is emitted, and what the PROTOCOL alone costs in imports.
//
// It is a table rather than a chain of conditionals so that the decision is
// per-shape and total. Adding a shape to the variant does not silently inherit
// the CLI answers here; it fails to build until this table says what the new
// shape means to Go.
type goEntry struct {
	// shape is the case this row decides, and it must equal the row's own index.
	// The array's LENGTH is checked by the compiler; this is what makes a row
	// left at its zero value in the middle of the table detectable, which length
	// alone cannot see. See TestShapeTablesAreIndexedByShape.
	shape EntryShape

	arg     string   // what main binds the entry's own input to
	caps    bool     // resolve the capability record and apply it before the input
	handler bool     // emit the HTTP adapter rather than the argv main
	imports []string // imports the PROTOCOL needs; capability imports are separate
}

// goHandlerImports is what serving costs. It is protocol-driven and NOT
// authority: the host owns the socket, so net/http appears here for a handler
// and never implies the outbound client — see goImportKeepalive.
var goHandlerImports = []string{"net/http", "io", "time"}

var goEntries = [...]goEntry{
	shapeCLI:         {shape: shapeCLI, arg: "args", caps: false, handler: false},
	shapeCLICaps:     {shape: shapeCLICaps, arg: "args", caps: true, handler: false},
	shapeHandler:     {shape: shapeHandler, arg: "req", caps: false, handler: true, imports: goHandlerImports},
	shapeHandlerCaps: {shape: shapeHandlerCaps, arg: "req", caps: true, handler: true, imports: goHandlerImports},
}

// EXHAUSTIVENESS, at compile time. Adding a shape to the variant makes this a
// type error here, in the Go backend, before any test runs — which is the whole
// claim: a new entry protocol cannot be introduced without this backend saying
// what it means.
var _ entryShapeTable = [len(goEntries)]struct{}{}

func goEntryFor(prog *CompiledProgram) (goEntry, error) {
	return entryShapeCase("the Go backend", &goEntries, prog.Shape)
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
//
// It resolves the shape itself rather than being handed the answer, so the
// import set is a consumer of the variant in its own right: a shape this backend
// has no case for cannot produce an import list at all.
func goImports(prog *CompiledProgram) ([]string, error) {
	// Always present: the type-erased value representation and the numeric,
	// string and container helpers every emitted program carries.
	need := map[string]bool{
		"fmt": true, "math": true, "math/big": true,
		"crypto/hmac": true, "crypto/sha256": true, "crypto/subtle": true,
		"os": true, "sort": true, "unicode/utf8": true,
	}
	ent, err := goEntryFor(prog)
	if err != nil {
		return nil, err
	}
	for _, imp := range ent.imports {
		need[imp] = true
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
	return out, nil
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
	if err := checkValueBindings(prog); err != nil {
		return "", err
	}
	// The entry's shape, decided once and consulted wherever the shape matters.
	// Resolved BEFORE anything is emitted: a shape this backend has no case for
	// must stop the build rather than reach the point where some conditional
	// quietly takes its else branch.
	ent, err := goEntryFor(prog)
	if err != nil {
		return "", err
	}
	// This backend claims the artifact. Done before anything is emitted so an
	// incomplete record stops the build rather than reaching a binary.
	if err := prog.stampBackend(goBackendVersion); err != nil {
		return "", err
	}
	e := &emitter{st: st, fname: map[string]string{}, strHash: strTypeHash(st)}
	e.nc = resolveNativeContainers(e.st, goSetHelperNames())
	if err := e.plan(prog.EntryHash); err != nil {
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
	imports, err := goImports(prog)
	if err != nil {
		return "", err
	}
	for _, imp := range imports {
		fmt.Fprintf(&e.b, "\t%q\n", imp)
	}
	e.b.WriteString(")\n\n")
	for _, imp := range imports {
		if sym, ok := goImportKeepalive[imp]; ok {
			fmt.Fprintf(&e.b, "var _ = %s\n", sym)
		}
	}
	// THE ARTIFACT'S REFUSAL MECHANISM, EMITTED ONCE (#167).
	//
	// Every host-side refusal in this runtime goes through oathRefuse, and the
	// number it exits with is FORMATTED FROM exitHostRefusal rather than written
	// down again here — one authority for the status a supervisor reads.
	fmt.Fprintf(&e.b, `
// oathExitRefusal is what this artifact exits with when the HOST refuses. It is
// the same status the launch gate uses for an unprovidable capability and the
// same one the LLVM backend uses for every refusal in its runtime: two artifacts
// compiled from ONE definition must not tell a supervisor different stories
// about why they stopped.
const oathExitRefusal = %d

// THE ARTIFACT ENDS IN EXACTLY THREE WAYS, and each has a named authority so
// that the classification is structural rather than a convention.
//
//	oathDone            an ordinary successful return, status 0
//	oathListenFailed    the handler could not bind, status 1
//	oathRefuse -> a boundary   a HOST-SIDE REFUSAL, disposed of below
//
// A listen failure is NOT the refusal status, on purpose: it happens after
// provisioning succeeded, and apps/github-webhook's acceptance suite uses
// exactly that difference as its control that capabilities resolve BEFORE the
// port is bound. Folding it in would destroy a live discriminator.
//
// Leaving these as bare os.Exit calls in main would make "which exits are
// legitimate" a claim about VALUES (0 and 1 are fine, 70 is not) rather than
// about SITES, and a host refusal added to main later would satisfy that check
// while bypassing the refusal path entirely.
func oathDone() {
	os.Exit(0)
}

func oathListenFailed(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// oathRefusal is a refusal IN FLIGHT: raised where the condition is detected,
// disposed of by the BOUNDARY it unwinds to. It is not an Oath value, and no
// Oath code can observe it, branch on it or continue past it.
//
// WHY IT TRAVELS INSTEAD OF EXITING WHERE IT IS RAISED. The CONDITION is a fact
// about the language — a zero divisor, a codepoint outside the scalars — but
// what should HAPPEN is a fact about the boundary the artifact runs under, and
// only the boundary knows it. A standalone program has nothing left to do, so
// it prints one line and exits %d. A handler is a long-lived server whose input
// comes from a remote party, and SPEC 14.2 is explicit that a remote party must
// never be able to end the process: there the disposition is a 500 and a served
// connection, exactly as it already is for a malformed Response.
//
// The first version of this exited at the raise site. It read as one clean door
// and handed any handler dividing by a request byte a remote process-kill. One
// door with two dispositions is not a weakening of that design; it is what the
// design needed, and it is the shape this project keeps arriving at — the
// BOUNDARY owns the transformation, not the place that detected the condition.
type oathRefusal struct{ msg string }

// WHAT IS A REFUSAL, and it is a classification rather than a style: conditions
// a WELL-TYPED Oath program can reach at run time — a codepoint outside the
// scalars, a zero divisor, octets from outside the language that are not text.
// Those are the artifact's contract with its host.
//
// WHAT IS NOT: conditions unreachable unless this compiler or its checker is
// WRONG — structEq on a function value, a non-exhaustive match, a byte list
// whose element is not an Int. Those stay panics, on purpose. They are not
// refusals the artifact offers its supervisor; they are bug reports, and the
// stack trace is the useful part. A compiler defect reported as an orderly
// refusal would be indistinguishable from the host declining to do something,
// which is the collapse this change exists to undo, one layer down.
//
// ONE LINE IS A PROPERTY OF THIS FUNCTION, NOT A HABIT OF ITS CALLERS. A
// refusal names what it refused, and some of those names are chosen by whoever
// runs the artifact — a file path, a URL, an environment value, an error from
// the host. A newline inside one would split a refusal into two lines, which
// breaks the contract and lets a caller forge a line in someone's log.
//
// The encoding is INJECTIVE: a backslash is escaped BEFORE the line breaks, so
// a name containing a real newline and one containing the two characters
// backslash-n stay two distinct diagnostics. Escaping only the line breaks
// would collapse them — distinct inputs, one output, which is the U+FFFD defect
// relocated into the message.
func oathRefuse(msg string) {
	b := make([]byte, 0, len(msg))
	for i := 0; i < len(msg); i++ {
		switch msg[i] {
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			b = append(b, msg[i])
		}
	}
	panic(oathRefusal{string(b)})
}

// oathRefusalOf is where the two classes are TOLD APART, and it is one function
// so that every boundary tells them apart the same way.
//
// recover() cannot be selective: catching a refusal means catching a
// compiler-bug panic too. So the only way to leave a bug alone is to re-raise
// what is not a refusal — it keeps its stack trace and its status 2, and the
// two classes stay as far apart as they were when one of them was the only
// mechanism. Every boundary calls this; none of them repeats the type test.
func oathRefusalOf(r any) (string, bool) {
	if r == nil {
		return "", false
	}
	if ref, ok := r.(oathRefusal); ok {
		return ref.msg, true
	}
	panic(r)
}

// oathExitOnRefusal is the STANDALONE disposition, deferred by main: one stderr
// line, then the refusal status. It covers a refusal raised anywhere in the
// program, capability provisioning included — that runs in this goroutine
// before anything is served.
func oathExitOnRefusal() {
	if msg, ok := oathRefusalOf(recover()); ok {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(oathExitRefusal)
	}
}
`, exitHostRefusal, exitHostRefusal)

	e.b.WriteString(`
// oathCapFailure is the CALL-failure value in Oath's capability protocol: a
// capability that was provided, was invoked, and could not complete. It is an
// ordinary Str the program may branch on.
//
// It is NOT how an unavailable capability is reported. A capability this host
// cannot supply is refused before any Oath code runs, so that case never reaches
// this program as a value at all.
const oathCapFailure = ""

// oathStrCons packs one Str element, or REFUSES — it never substitutes.
//
// A backend subset boundary, NOT a claim that the value is illegal Oath (#133
// asks that question and is open). This backend packs Str as UTF-8, and UTF-8
// encodes exactly the Unicode scalar values. Everything outside them has no
// injective encoding here.
//
// What it replaces was string(rune(n)), which silently yields U+FFFD for a
// negative value, a surrogate, or anything above 0x10FFFF — so SCons -1 SNil,
// SCons 55296 SNil and SCons 1114112 SNil all printed the same three bytes.
// Three distinct values, one output, no constructor inverse. Both native
// backends did it, so they agreed with each other and disagreed with the
// reference, and the three-way gate stayed green.
func oathStrCons(cp *big.Int, rest string) string {
	// SIGN BEFORE MAGNITUDE, so a value too large for int64 still gets a CLASS.
	// Testing IsInt64 first left both huge positives and huge negatives with a
	// bare number and no reason, while the LLVM backend named them — and the two
	// backends refuse the same values, so they should say the same thing about
	// why. A magnitude outside int64 is above 0x10FFFF with certainty once the
	// sign is known.
	if cp.Sign() < 0 {
		oathRefuseStrElement(cp.String() + " (negative)")
	}
	if !cp.IsInt64() {
		oathRefuseStrElement(cp.String() + " (above the maximum scalar 0x10FFFF)")
	}
	n := cp.Int64()
	switch {
	case n > 0x10FFFF:
		oathRefuseStrElement(cp.String() + " (above the maximum scalar 0x10FFFF)")
	case n >= 0xD800 && n <= 0xDFFF:
		oathRefuseStrElement(cp.String() + " (a surrogate, 0xD800..0xDFFF)")
	}
	return string(rune(n)) + rest
}

func oathRefuseStrElement(what string) {
	oathRefuse("oath: this backend cannot encode Str element " + what +
		": Str is packed as UTF-8, which encodes only Unicode scalar values. " +
		"Refusing rather than substituting U+FFFD, which would make distinct Str values identical.")
}

// oathStrFromHost admits bytes from OUTSIDE the language as a Str, or refuses.
//
// This is the boundary where the property first becomes observable, and so the
// only place it can honestly be enforced: the kernel never sees "bytes
// pretending to be text", it only sees a Str. Once these gates hold, malformed
// storage cannot reach oathStrHead through a capability at all — that refusal
// stays as the backstop for a path this list forgot, which is exactly the kind
// of list that acquires a new entry later.
//
// Refusal is oathRefuse, NOT oathCapFailure. The failure value is "" (see above),
// so routing malformed bytes there would make invalid input indistinguishable
// from an unset variable or an empty file — the same collapse of distinct inputs
// this is removing, relocated one layer up. And 70 is already what both backends
// use when the host cannot supply what the program requires; a supervisor should
// not need to know which backend compiled the artifact.
//
// The per-request HTTP boundary is deliberately NOT here: SPEC 14.2 row 9
// (REQ-TEXT-OCTETS-ARE-ASCII) governs every Str built from a request and answers
// 400 with the handler not invoked. A remote party must never be able to end the
// process, so that disposition is a response, not an exit.
func oathStrFromHost(what, s string) string {
	if !utf8.ValidString(s) {
		oathRefuse(fmt.Sprintf("oath: %s is not valid UTF-8, so it has no Str value; "+
			"refusing rather than substituting U+FFFD, which would make distinct inputs identical. "+
			"Arbitrary octets need a bytes-typed channel, not Str.", what))
	}
	return s
}

// oathStrHead is oathStrCons's inverse, and refuses on the SAME principle.
//
// utf8.DecodeRuneInString answers (RuneError, 1) for malformed storage, which
// is a SUBSTITUTION: it invents a codepoint the bytes do not denote, and two
// different malformed buffers then decode to the same Str. Packing stopped
// doing that in #132; decoding kept doing it, so the two native backends
// disagreed on the same program — llvm-ir/1 refused the buffer while go-emit/2
// printed a value.
//
// The size test is the whole check: a VALID U+FFFD decodes with size 3, so
// comparing the rune alone would refuse legitimate text.
func oathStrHead(s string) (rune, int) {
	r, sz := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && sz <= 1 {
		oathRefuse("oath: a Str holds bytes that are not valid UTF-8; this backend packs Str as UTF-8 " +
			"and refuses to decode malformed storage rather than replace or guess it")
	}
	return r, sz
}
`)
	e.b.WriteString(`
// oBytes reads a (List Int) value as raw bytes; oList rebuilds one. Elements
// outside 0..255 REFUSE rather than truncate: a digest over silently truncated
// input would verify against a message nobody sent.
func oBytes(v any) []byte {
	var out []byte
	for cur, _ := v.(*ctorV); cur != nil && cur.idx == 1; cur, _ = cur.fields[1].(*ctorV) {
		n, ok := cur.fields[0].(*big.Int)
		// TWO CONDITIONS, TWO CLASSES, and merging them said the wrong thing
		// about one of them. A (List Int) whose element is not an Int is a
		// broken representation — the checker types this argument — so it is a
		// compiler bug and keeps its stack. Only the RANGE is something a
		// well-typed program can reach, and reporting a broken representation as
		// "out of range 0..255" described the value as merely too large, which
		// it is not.
		if !ok {
			panic("byte list element is not an Int")
		}
		if !n.IsInt64() || n.Int64() < 0 || n.Int64() > 255 {
			oathRefuse("oath: byte list element out of range 0..255")
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

// iquo / irem are Int division and modulo, and they exist ONLY to name the
// condition. big.Int.Quo and big.Int.Rem both panic with "division by zero",
// so a modulo fault reported a division — the operation the program did not
// perform. The interpreter distinguishes the two, so an artifact compiled from
// the same definition must not tell a different story about why it stopped.
func iquo(a, b *big.Int) *big.Int {
	if b.Sign() == 0 {
		oathRefuse("oath: division by zero")
	}
	return new(big.Int).Quo(a, b)
}
func irem(a, b *big.Int) *big.Int {
	if b.Sign() == 0 {
		oathRefuse("oath: modulo by zero")
	}
	return new(big.Int).Rem(a, b)
}

// canonF canonicalizes a float64: every NaN becomes the one canonical NaN, so
// runtime identity matches the kernel (and prover). -0.0 and ±inf are kept.
func canonF(f float64) float64 {
	if math.IsNaN(f) {
		return math.Float64frombits(0x7FF8000000000000)
	}
	return f
}

// Numeric conversions (mirror the interpreter). Widening is total; the
// Float→{Rat,Int} narrowings REFUSE non-finite input, naming the same condition
// eval's error names.
func i2r(x *big.Int) *big.Rat { return new(big.Rat).SetInt(x) }
func i2f(x *big.Int) float64  { f, _ := new(big.Float).SetInt(x).Float64(); return canonF(f) }
func r2f(x *big.Rat) float64  { f, _ := x.Float64(); return canonF(f) }
func f2r(x float64) *big.Rat {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		oathRefuse("oath: to-rat of non-finite float")
	}
	return new(big.Rat).SetFloat64(x)
}
func rfloor(x *big.Rat) *big.Int { return new(big.Int).Div(x.Num(), x.Denom()) }
func ffloor(x float64) *big.Int {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		oathRefuse("oath: floor of non-finite float")
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
	// A BUG PANIC, NOT A REFUSAL (see oathRefuse). Equality is only ever emitted
	// at a type the checker admitted for it, and no such type contains a
	// function, so reaching this means the checker let one through. Exiting 70
	// here would report a compiler defect in the vocabulary the artifact uses to
	// tell its supervisor the HOST declined something — and the stack trace,
	// useless for a refusal, is the whole value of a bug report.
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
	entryArg := ent.arg
	entryCall := fmt.Sprintf("%s(nil, %s)", e.fname[prog.EntryHash], entryArg)
	// Keyed on the SHAPE, not on the requirement count. An entry typed
	// (-> {} (-> (List Str) Str)) requires nothing and still takes a capability
	// argument: its arity comes from its type, and skipping the record would pass
	// argv where the record belongs and hand the caller a closure to assert a
	// string on. "Requires nothing" is a statement about authority; "takes no
	// argument" is a statement about shape, and they are not the same statement.
	if ent.caps {
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
// oathProvideValue supplies a REQUIRED VALUE, or reports why this host cannot.
//
// MISSING AND EMPTY ARE DISTINGUISHED, and both refuse to launch. They are
// different operator mistakes — the variable was never set, versus it was set
// to nothing — and collapsing them is the same defect as answering "" for an
// absent capability: two distinguishable states presented identically.
func oathProvideValue(field, envVar string) (any, error) {
	v, ok := os.LookupEnv(envVar)
	if !ok {
		return nil, fmt.Errorf("required value %%s is not provided; set %%s", field, envVar)
	}
	if v == "" {
		return nil, fmt.Errorf("required value %%s is provided but empty (%%s)", field, envVar)
	}
	// A required value is ADMITted like any other external octets (#133). It
	// refuses through the PROVISION channel rather than through oathStrFromHost,
	// because that channel is strictly better here: this runs before the entry
	// point, so the program never starts, and the operator gets the same
	// "this host cannot provide" framing as a missing or empty value — which is
	// what a malformed one is, a third way for the host to fail to supply it.
	if !utf8.ValidString(v) {
		return nil, fmt.Errorf("required value %%s is not valid UTF-8, so it has no Str value (%%s); "+
			"refusing rather than substituting U+FFFD", field, envVar)
	}
	return v, nil
}

// The LAUNCH half of the refusal contract, and it goes through the SAME door as
// the runtime half (#167). It always exited 70; what it did not do was share a
// path with the conditions the program can reach after it starts, so the two
// halves could drift apart while each stayed internally right.
func oathResolveCapabilities() any {
	fields := make([]any, len(oathRequired))
	for i, c := range oathRequired {
		v, err := c.provide()
		if err != nil {
			oathRefuse(fmt.Sprintf("oath: this host cannot provide required capability %%s (%%s): %%v", c.field, c.kind, err))
		}
		if v == nil {
			oathRefuse(fmt.Sprintf("oath: provider for required capability %%s (%%s) supplied nothing", c.field, c.kind))
		}
		fields[i] = v
	}
	return &ctorV{idx: -1, fields: fields}
}
`, strings.Join(slots, "\n"))
		caps = "\tvar realWorld any = oathResolveCapabilities()\n"
		entryCall = fmt.Sprintf("apply(%s(nil, realWorld), %s)", e.fname[prog.EntryHash], entryArg)
	}
	if ent.handler {
		// HANDLER protocol (#78). The host owns the socket, TLS, routing and
		// process lifecycle; the artifact is a pure function from a Request
		// VALUE to a Response value. This adapter is the whole irreversible
		// translation, and SPEC §14 is what it must produce.
		//
		// The governing rule is §14.1: PRESERVE every distinction the transport
		// supplies, CANONICALIZE the ones it does not define. The earlier rule
		// here was "normalize as little as possible", and it produced the defect
		// #122 was filed about — it read as a prohibition on transformation, so
		// net/http's key canonicalization was recorded as an unavoidable wart
		// rather than as a decision, and an application ended up encoding one
		// backend's spelling (`X-Github-Event`) in its Oath source.
		//
		// Field-name case is NOT information HTTP carries: names are
		// case-insensitive (RFC 9110 §5.1) and HTTP/2 mandates lowercase on the
		// wire (RFC 9113 §8.2.1), so a rule preserving the sender's case is
		// unsatisfiable the moment a client negotiates h2. Lowercasing is
		// therefore a canonicalization, not a loss. Cross-key order is likewise
		// insignificant (§5.3) and already destroyed by net/http's map, so §14
		// pins it lexicographically rather than leaving each backend to pick —
		// "deterministic" alone would let two conformant backends build
		// different Oath values from the same request.
		//
		// What IS preserved, because HTTP means it: repeats under one name, in
		// arrival order and never comma-joined; the value octets; the method;
		// the raw path with its query; and the body bytes, which is what
		// signature schemes actually sign.
		//
		// This is the ONLY implementation of the model — there is deliberately
		// no second copy in the neutral layer for tests to check against, since
		// a model nothing runs is not evidence about the adapter. The gate is
		// end-to-end: TestHandlerRequestModelIsCanonical builds this program,
		// sends §14.3's vector, and asserts the resulting list exactly.
		// Capabilities are resolved before the listener binds, not per request:
		// a host that cannot supply the program's authority must fail to launch,
		// not accept traffic and fail each request individually.
		fmt.Fprintf(&e.b, `
// oathLowerASCII implements SPEC 14.2 REQ-HEADER-NAMES-LOWERCASE: ASCII only,
// never Unicode case folding. A field name is an HTTP token and so is ASCII by
// construction, but net/http passes through a name it could not canonicalize,
// and strings.ToLower would fold such a name under a Unicode table this program
// must not depend on — a table that can also change the name's LENGTH.
// oathIsToken reports whether s is a non-empty HTTP token (RFC 9110 5.6.2).
func oathIsToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		// tchar punctuation, in the order  ! # $ %% & ' * + - . ^ _ backtick | ~
		// written as byte values because a percent sign, a quote and a backtick
		// are each hazards inside the emitted template this function lives in.
		switch c {
		case 0x21, 0x23, 0x24, 0x25, 0x26, 0x27, 0x2A, 0x2B,
			0x2D, 0x2E, 0x5E, 0x5F, 0x60, 0x7C, 0x7E:
			continue
		}
		return false
	}
	return true
}

// oathHdrASCII reports whether every octet is one Str can carry exactly: the
// printable US-ASCII range, plus HTAB inside a field VALUE only (RFC 9110 5.5
// permits it there and nowhere in a field name).
func oathHdrASCII(s string, isValue bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c <= 0x7E {
			continue
		}
		if isValue && c == 0x09 {
			continue
		}
		return false
	}
	return true
}

func oathLowerASCII(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func main() {
	// Covers PROVISIONING, which happens in this goroutine before anything is
	// served — a launch refusal must end the process. It does NOT cover a
	// request: each one installs its own disposition below, because a remote
	// party must never be able to end this process (SPEC 14.2).
	defer oathExitOnRefusal()
	addr := os.Getenv("OATH_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
%s	// THE HANDLER IS THE WHOLE SERVER, NOT A ROUTE ON A MUX. SPEC 14.2 hands
	// every request to the entry and asks for one Request value from the octets;
	// a ServeMux sits in front of that and makes decisions of its own, and two
	// of them were measured as 14.0 divergences against the other backend.
	// Routing is not part of the protocol, so nothing here should be routing.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// THE REQUEST-SCOPED DISPOSITION OF A REFUSAL, and the reason a refusal
		// is a travelling value rather than an exit.
		//
		// A handler's input is chosen by a remote party, and a well-typed
		// handler can reach a refusal from it — dividing by a body byte that
		// arrived as zero is enough. Exiting there would hand that party the
		// process. SPEC 14.2 already settles the principle for a malformed
		// request field (400, handler not invoked); this is the same principle
		// one step later, when the handler WAS invoked and could not complete,
		// so the answer is 500 and the server keeps serving.
		//
		// The line still goes to stderr: the operator needs to see it, and a
		// 500 with no operand named is the silent failure this repo keeps
		// finding. A BUG panic is re-raised for net/http to handle as before.
		defer func() {
			if msg, ok := oathRefusalOf(recover()); ok {
				fmt.Fprintln(os.Stderr, msg)
				http.Error(w, "the handler could not complete this request", 500)
			}
		}()

		// SPEC 14.2 row 20: the receipt-time observation is taken BEFORE the body
		// is consumed. Reading first and stamping after records body-COMPLETION
		// time, which for a slow or large upload differs from receipt by seconds
		// or more — and received-at is an observation about when the request
		// arrived, not when the backend finished collecting it.
		receivedAt := time.Now().Unix()

		// SPEC 14.2a PROTO-UNFRAMEABLE-IS-REFUSED. The error is NOT discarded: a
		// connection that ends before its declared Content-Length yields
		// unexpected EOF here, and constructing a Request from the partial body
		// would hand the handler a message that was never fully sent — which a
		// signature over that body would then verify against the wrong octets.
		raw, rerr := io.ReadAll(r.Body)
		r.Body.Close()
		if rerr != nil {
			http.Error(w, "request body could not be read in full (SPEC 14.2a)", 400)
			return
		}

		// body: raw bytes, one Int per byte, built tail-first
		var body any = &ctorV{idx: 0}
		for i := len(raw) - 1; i >= 0; i-- {
			body = &ctorV{idx: 1, fields: []any{big.NewInt(int64(raw[i])), body}}
		}

		// headers: (List (Pair Str Str)) per SPEC 14.2 — names ASCII-lowercased,
		// entries ordered lexicographically by the lowered name, repeats kept in
		// arrival order and never comma-joined.
		// Built from a COPY rather than by editing r.Header: the host entry below
		// is one net/http deliberately removed, and writing it back would be a
		// visible side effect on a value the server still owns.
		type oathHdr struct{ name, value string }
		canon := make([]string, 0, len(r.Header))
		for k := range r.Header {
			canon = append(canon, k)
		}
		// Sort the canonical keys BEFORE lowering. Two distinct map keys can
		// lower to the same name (net/http leaves a non-token name untouched),
		// and Go randomizes map iteration, so the stable sort below needs a
		// deterministic input order to break that tie reproducibly.
		sort.Strings(canon)
		// SPEC 14.2 REQ-FRAMING-FIELDS-EXCLUDED — the names Connection NOMINATES
		// are connection-specific for this connection only, so a conformant
		// intermediary would have stripped them. Excluding them here is what
		// stops the Oath value from depending on the network path taken.
		// Parsed as OCTETS. HTTP OWS is SP and HTAB only, and each option must be
		// a token (RFC 9110 5.6.2). strings.TrimSpace would strip UNICODE
		// whitespace, so a value of NBSP + "X-Hop" + NBSP would yield the
		// valid-looking nomination x-hop and SILENTLY SUPPRESS a real header the
		// handler was meant to see — a way to hide a field behind a malformed
		// Connection value. A non-token option is ignored, not honoured.
		nominated := map[string]bool{}
		for _, cv := range r.Header["Connection"] {
			start := 0
			for i := 0; i <= len(cv); i++ {
				if i < len(cv) && cv[i] != ',' {
					continue
				}
				tok := cv[start:i]
				start = i + 1
				for len(tok) > 0 && (tok[0] == ' ' || tok[0] == '\t') {
					tok = tok[1:]
				}
				for len(tok) > 0 && (tok[len(tok)-1] == ' ' || tok[len(tok)-1] == '\t') {
					tok = tok[:len(tok)-1]
				}
				if oathIsToken(tok) {
					nominated[oathLowerASCII(tok)] = true
				}
			}
		}
		entries := make([]oathHdr, 0, len(canon)+1)
		for _, k := range canon {
			lk := oathLowerASCII(k)
			// Dropped by NAME, not by asking the library what it kept: net/http
			// deletes some of these into dedicated struct fields and
			// deduplicates others, so a backend forwarding its library's
			// collection would agree with another backend only by luck.
			switch lk {
			case "connection", "keep-alive", "proxy-authenticate",
				"proxy-authorization", "te", "trailer", "transfer-encoding",
				"upgrade", "content-length":
				continue
			}
			if nominated[lk] {
				continue
			}
			for _, v := range r.Header[k] {
				entries = append(entries, oathHdr{lk, v})
			}
		}
		// SPEC 14.2 REQ-HOST-IS-A-HEADER. net/http promotes the Host field to
		// r.Host and REMOVES it from the map, so passing the map through drops a
		// mandatory header. Restoring it also gives HTTP/2 the same shape, where
		// the authority arrives as :authority rather than as a field line. No
		// entry is invented: no authority in, no host entry out.
		//
		// Deliberately NOT subject to the nomination filter. That rule is
		// unconditional, and honouring a Connection nomination of host would let
		// a client delete a mandatory field from the Oath value and change how
		// the handler behaves. The nomination is ignored, not refused.
		if r.Host != "" {
			entries = append(entries, oathHdr{"host", r.Host})
		}
		sort.SliceStable(entries, func(a, b int) bool { return entries[a].name < entries[b].name })

		// SPEC 14.2 REQ-TEXT-OCTETS-ARE-ASCII. Str is codepoints; for
		// printable US-ASCII the codepoint IS the octet, and outside it the type
		// cannot represent what arrived. RFC 9110 still permits obs-text in a
		// value, so REFUSE rather than transcode: a repaired value would make
		// the handler verify a signature over bytes that never arrived, and no
		// test of the artifact could catch it because the artifact never sees
		// the original.
		//
		// Checked AFTER the exclusions, which §14.2 makes normative: this is a
		// question about the value being constructed, not about the message, so
		// a field that never enters headers never enters Str and must not cause
		// a refusal.
		for _, e := range entries {
			if !oathHdrASCII(e.name, false) || !oathHdrASCII(e.value, true) {
				http.Error(w, "header field is not US-ASCII (SPEC 14.2)", 400)
				return
			}
		}

		var flatK, flatV []string
		for _, e := range entries {
			flatK = append(flatK, e.name)
			flatV = append(flatV, e.value)
		}
		var hs any = &ctorV{idx: 0}
		for i := len(flatK) - 1; i >= 0; i-- {
			pair := &ctorV{idx: 0, fields: []any{flatK[i], flatV[i]}}
			hs = &ctorV{idx: 1, fields: []any{pair, hs}}
		}

		// SPEC 14.2 REQ-PATH-IS-RAW. r.URL.Path is already PERCENT-DECODED by
		// net/http, so building from it turns an escaped slash in the request
		// target into a real path separator — a different path, and a different
		// signature input. RequestURI is the unmodified target as sent.
		path := r.RequestURI

		// SPEC 14.2 REQ-TEXT-OCTETS-ARE-ASCII reaches METHOD and PATH too, not
		// just headers. Both are Str built from request octets, and an earlier
		// version of the rule covered only headers — blind round 9 found the
		// asymmetry. HTAB is permitted inside a header VALUE and nowhere else,
		// so both are checked with isValue=false.
		if !oathHdrASCII(r.Method, false) || !oathHdrASCII(path, false) {
			http.Error(w, "method or request target is not US-ASCII (SPEC 14.2)", 400)
			return
		}

		var req any = &ctorV{idx: 0, fields: []any{
			r.Method, path, hs, body, big.NewInt(receivedAt),
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
	// DisableGeneralOptionsHandler: SPEC 14.0 BINDS THIS BACKEND TO THE OTHER,
	// AND net/http ANSWERS "OPTIONS *" ITSELF. Its general handler replies 200
	// with an empty body without ever calling the registered handler, so an
	// Oath entry that would have built a Request from those octets never runs —
	// while the LLVM backend, which has no such interception, invokes it and
	// answers whatever the handler returns. Measured: four of seventeen
	// recorded divergences were exactly this.
	//
	// The repair is to REMOVE the interception rather than to reproduce it in
	// the other backend. "OPTIONS *" is a legal request with a Request value;
	// the alternative would be a second transport-only response path in the
	// emitted C, which is a rule about HTTP added to a backend rather than a
	// rule of the language, and 14.2 gives that request no such disposition.
	srv := &http.Server{Addr: addr, Handler: handler, DisableGeneralOptionsHandler: true}
	// RFC 9112 §6.1: a request carrying both Transfer-Encoding and Content-Length
	// MUST have its connection closed after the response — a desynchronised
	// connection is the request-smuggling vector. net/http processes such a
	// request and REUSES the connection (golang/go#80942), and STRIPS both
	// headers before the handler, so the conflict is invisible from the parsed
	// Request; detecting it precisely means reimplementing net/http's request
	// framing in a conn wrapper (across pipelining, obs-fold and bare-LF), which
	// is exactly what this backend delegates to net/http to avoid. So this backend
	// takes the complete, robust disposition instead: NO keep-alive. Every
	// response closes its connection and answers Connection: close, which makes
	// the §6.1 conflict — and every other reuse — impossible by construction, the
	// same posture the LLVM backend has always held. It is a per-request
	// connection model; keep-alive is a future optimization for both backends
	// (#179), where the §6.1 conflict would have to be handled explicitly.
	srv.SetKeepAlivesEnabled(false)
	fmt.Fprintf(os.Stderr, "oath handler listening on %%s\n", addr)
	if err := srv.ListenAndServe(); err != nil {
		oathListenFailed(err)
	}
}
`, caps, entryCall)
		return e.b.String(), nil
	}
	fmt.Fprintf(&e.b, `
func main() {
	// FIRST STATEMENT, so it covers capability provisioning and argv admission
	// as well as the entry point. A refusal raised before this is installed
	// would reach the runtime's default panic printer instead of the contract.
	defer oathExitOnRefusal()
	var args any = &ctorV{idx: 0} // Nil
	for i := len(os.Args) - 1; i >= 1; i-- {
		args = &ctorV{idx: 1, fields: []any{
			oathStrFromHost(fmt.Sprintf("command-line argument %%d", i), os.Args[i]), args}} // Cons
	}
%s	out := %s
	fmt.Println(out.(string))
	oathDone()
}
`, caps, entryCall)
	return e.b.String(), nil
}

// plan fixes what this backend emits and in what order, by asking the neutral
// layer for the entry's dependency-first order.
//
// The set this backend lowers natively is the recognized Set/Map operations:
// they become oset/omap helpers at their call sites, so neither the operation
// nor the sorted-list helpers it is defined in terms of are emitted. Passing
// them to emissionOrder prunes them; nothing here walks the closure itself.
func (e *emitter) plan(entry string) error {
	native, err := e.nc.prunableOps(e.st, entry)
	if err != nil {
		return err
	}
	order, err := emissionOrder(e.st, entry, native)
	if err != nil {
		return err
	}
	e.order = order
	for _, h := range order {
		e.fname[h] = "f_" + smtName(e.st.NameOf(h)) + "_" + h[:8]
	}
	return nil
}

// emitDef compiles one function to a Go function of shape
// func(env []any, arg any) any, uncurried across its leading lams by
// chaining closures exactly like the evaluator does.
func (e *emitter) emitDef(h string) error {
	d, _ := e.st.GetDef(h)
	name := e.fname[h]
	e.chk = &checkerMachine{st: e.st, selfTyVars: d.TyVars, selfTy: d.Ty}
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
	op, ok := e.nc.Ops[head.Hash]
	if !ok || len(args) != op.Arity {
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
	return fmt.Sprintf("%s(%s)", goSetHelpers[op.Name], strings.Join(parts, ", ")), true, nil
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
			return fmt.Sprintf("any(oathStrCons(%s.(*big.Int), %s.(string)))", parts[0], parts[1]), nil
		}
		// Set/Map values are native osets/omaps: the MkSet/MkMap constructor wraps
		// a list, so build the native map from it (keeping the representation
		// uniform even when constructed directly rather than via an operation).
		if t.Hash == e.nc.SetHash && e.nc.SetHash != "" {
			return fmt.Sprintf("osetFromList(%s)", parts[0]), nil
		}
		if t.Hash == e.nc.MapHash && e.nc.MapHash != "" {
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
		if t.Hash == e.nc.SetHash && e.nc.SetHash != "" {
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
		if t.Hash == e.nc.MapHash && e.nc.MapHash != "" {
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
		// A BUG PANIC, NOT A REFUSAL (#167): the checker requires one arm per
		// constructor, so a scrutinee whose idx no case matches means the
		// emitted switch and the datatype disagree. That is this compiler
		// being wrong, not the host declining something, and it stays a panic
		// for the reason recorded at oathRefuse — a refusal is a contract with
		// the supervisor, and a compiler defect must not be reported in it.
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
		r, sz := oathStrHead(scrut)
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
	bigOp := map[string]string{"+": "Add", "-": "Sub", "*": "Mul"}
	cmp := func(op string) string {
		return fmt.Sprintf("any(%s.(*big.Int).Cmp(%s.(*big.Int)) %s 0)", args[0], args[1], op)
	}
	switch t.Op {
	case "+", "-", "*":
		return fmt.Sprintf("any(new(big.Int).%s(%s.(*big.Int), %s.(*big.Int)))", bigOp[t.Op], args[0], args[1]), nil
	case "/", "%":
		// Quo/Rem truncate toward zero / take the dividend's sign (SPEC). They
		// go through iquo/irem rather than being emitted inline, because
		// big.Int.Rem panics with "division by zero" — naming an operation the
		// program never performed. The helper names the condition instead.
		helper := map[string]string{"/": "iquo", "%": "irem"}[t.Op]
		return fmt.Sprintf("any(%s(%s.(*big.Int), %s.(*big.Int)))", helper, args[0], args[1]), nil
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
