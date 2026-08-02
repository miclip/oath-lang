package main

// Tests for #114 / effects stage 4: a compiled artifact's RUNTIME authority
// matches the authority the verifier modelled.
//
// The invariant under test, stated once:
//
//	Every requirement declared by the compiled entry point is resolved exactly
//	once before launch, or the executable does not start.
//
// and the acceptance test that makes it mean something, which is deliberately
// adversarial: compile a program verified to receive only capability A, launch it
// in a host that possesses A and B, and show it cannot observe or invoke B.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capStore builds a store with the base datatypes plus whatever the caller adds,
// and marks the named entry verified so the guarantee gate is not what is under
// test here.
func capStore(t *testing.T) *Store {
	t.Helper()
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	return st
}

// markVerified seeds a `tested` verdict so planProgram's guarantee gate passes.
// The gate itself is tested in TestPlanProgramGates; here it would only be noise.
func markVerified(t *testing.T, st *Store, name string) {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Fatalf("%s not in store", name)
	}
	m, err := st.GetMeta(h)
	if err != nil {
		t.Fatal(err)
	}
	m.Guarantee.Level = "tested"
	if err := st.SetMeta(h, m); err != nil {
		t.Fatal(err)
	}
}

// buildProgram compiles a name to a real executable and returns its path.
func buildProgram(t *testing.T, st *Store, name string) (bin, src string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	prog, err := planProgram(st, name)
	if err != nil {
		t.Fatalf("planProgram(%s): %v", name, err)
	}
	source, err := emitProgram(st, prog)
	if err != nil {
		t.Fatalf("emitProgram(%s): %v", name, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := runIn(dir, "go", "build", "-o", out, "."); err != nil {
		t.Fatalf("go build %s failed:\n%s\n--- source ---\n%s", name, o, source)
	}
	return out, source
}

// readProvenance recovers the manifest from a built artifact by reading the FILE.
// The artifact is never executed: provenance is data an artifact carries, not a
// mode it can be put into, and finding out what a binary is must not require
// running it.
func readProvenance(t *testing.T, bin string) []byte {
	t.Helper()
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	m, err := extractProvenance(b)
	if err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	return []byte(m)
}

// ---------- the vocabulary and the backend agree ----------

// Every capability Oath defines must be providable by this backend. A kind in the
// vocabulary with no provider would be a requirement the compiler accepts and
// then cannot honour — caught at build time by goProviderFor, but that refusal is
// a backstop for a contributor's mistake, and this is where the mistake is found.
func TestCapabilityVocabularyIsProvidable(t *testing.T) {
	for field, decl := range capabilityVocabulary {
		if _, ok := goProviders[decl.Kind]; !ok {
			t.Errorf("capability %q denotes kind %q, which the %s backend cannot provide",
				field, decl.Kind, goBackendVersion)
		}
	}
}

// The provider table must not grow keys the vocabulary never produces: a provider
// for a kind no program can require is dead authority sitting in the backend, and
// the direction of the dependency (Oath semantics -> requirements -> provider) is
// what forbids it. A backend does not get to invent capabilities.
func TestNoProviderWithoutVocabulary(t *testing.T) {
	declared := map[capabilityKind]bool{}
	for _, decl := range capabilityVocabulary {
		declared[decl.Kind] = true
	}
	for kind := range goProviders {
		if !declared[kind] {
			t.Errorf("backend provides kind %q, which no capability in Oath's vocabulary denotes", kind)
		}
	}
}

// ---------- requirements are derived, and refusals are distinguishable ----------

// A capability field outside Oath's vocabulary is an UNKNOWN REQUIREMENT, not an
// unwired one. The build must refuse, and must say which of the two it is: one is
// a host that cannot help, the other is a program asking for authority the
// language has no meaning for, and the repairs differ.
func TestUnknownCapabilityRefusesBuild(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn main-frob [] [(w {frobnicate (-> Str Str)}) (args (List Str))] Str
		((. w frobnicate) "x"))`)
	markVerified(t, st, "main-frob")

	_, err := planProgram(st, "main-frob")
	if err == nil {
		t.Fatal("built a program requiring a capability Oath does not define")
	}
	if !strings.Contains(err.Error(), "unknown capability requirement") {
		t.Fatalf("refusal does not name the problem as an unknown requirement: %v", err)
	}
	// The message must be actionable: it lists what Oath does define.
	for _, known := range []string{"env", "fetch", "readfile", "emit"} {
		if !strings.Contains(err.Error(), known) {
			t.Errorf("refusal does not offer the vocabulary (missing %q): %v", known, err)
		}
	}
}

// A field whose NAME is in the vocabulary but whose type is wrong is a type
// error, not a differently-named capability. This is what stops the field name
// from being the only thing consulted.
func TestWrongTypedCapabilityRefusesBuild(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn main-badfetch [] [(w {fetch (-> Str Int)}) (args (List Str))] Str
		(if (== ((. w fetch) "x") 0) "a" "b"))`)
	markVerified(t, st, "main-badfetch")

	_, err := planProgram(st, "main-badfetch")
	if err == nil {
		t.Fatal("built a program whose capability field has the wrong type")
	}
	if !strings.Contains(err.Error(), "wrong type") {
		t.Fatalf("refusal does not name the problem as a type error: %v", err)
	}
	if strings.Contains(err.Error(), "unknown capability") {
		t.Fatalf("a known capability with a bad type must not be reported as unknown: %v", err)
	}
}

// Requirements are DERIVED from the verified type, in the capability record's own
// field order — which is how the value is delivered, so a slot that disagreed
// with the record would hand the program the wrong authority under the right name.
func TestRequirementsFollowRecordOrder(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn main-two [] [(w {env (-> Str Str) fetch (-> Str Str)}) (args (List Str))] Str
		((. w env) ((. w fetch) "u")))`)

	h, _ := st.Resolve("main-two")
	d, _ := st.GetDef(h)
	capTy, _, ok := entryShape(st, d.Ty)
	if !ok {
		t.Fatal("main-two is not an entry point")
	}
	reqs, err := entryRequirements(st, capTy)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d requirements, want 2", len(reqs))
	}
	for i, r := range reqs {
		if r.Slot != i {
			t.Errorf("requirement %s has slot %d, want %d", r.Field, r.Slot, i)
		}
		if r.Field != capTy.Names[i] {
			t.Errorf("requirement %d is %q, but record field %d is %q", i, r.Field, i, capTy.Names[i])
		}
	}
	kinds := map[string]capabilityKind{}
	for _, r := range reqs {
		kinds[r.Field] = r.Kind
	}
	if kinds["env"] != capProcessEnv || kinds["fetch"] != capHTTPRequest {
		t.Fatalf("derived kinds are wrong: %v", kinds)
	}
}

// ---------- the gates are facts about the artifact ----------

func TestPlanProgramGates(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn greet [] [(args (List Str))] Str "hi")`)

	// asserted: no verified properties at all.
	if _, err := planProgram(st, "greet"); err == nil {
		t.Fatal("built an executable from an entry with no verified properties")
	} else if !strings.Contains(err.Error(), "no verified properties") {
		t.Fatalf("unexpected refusal: %v", err)
	}

	// falsified: a broken oath must never become an executable.
	h, _ := st.Resolve("greet")
	m, _ := st.GetMeta(h)
	m.Guarantee.Level = "falsified"
	m.Guarantee.Falsified = []string{"some-prop"}
	if err := st.SetMeta(h, m); err != nil {
		t.Fatal(err)
	}
	if _, err := planProgram(st, "greet"); err == nil {
		t.Fatal("built an executable from a FALSIFIED entry")
	} else if !strings.Contains(err.Error(), "FALSIFIED") {
		t.Fatalf("unexpected refusal: %v", err)
	}

	// tested: builds, and requires nothing.
	m.Guarantee.Level = "tested"
	m.Guarantee.Falsified = nil
	if err := st.SetMeta(h, m); err != nil {
		t.Fatal(err)
	}
	prog, err := planProgram(st, "greet")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	if len(prog.Requirements) != 0 {
		t.Fatalf("a pure entry requires %v, want nothing", prog.Requirements)
	}
	if len(prog.Closure) == 0 {
		t.Fatal("provenance closure is empty")
	}
}

// ---------- THE ACCEPTANCE TEST ----------

// Compile a program verified to receive only `env`. Launch it in a host that
// possesses env AND network. Demonstrate it cannot observe or invoke HTTP.
//
// The demonstration is static and total rather than behavioural and sampled: the
// authority is not in the artifact. A program that does not require http_request
// has no provider compiled in and does not link an HTTP client at all, so there
// is no reachable path to an outbound request regardless of what the host can do.
// The control in the second half is what makes that a property of the DECLARATION
// rather than of the compiler: the same store, the same host, one extra field in
// the capability record, and the client appears.
func TestCompiledProgramCannotReachUndeclaredCapability(t *testing.T) {
	st := capStore(t)
	// Requires env only. `fetch` is never mentioned.
	put(t, st, `(defn main-env [] [(w {env (-> Str Str)}) (args (List Str))] Str
		((. w env) "OATH_TEST_VAR"))`)
	// The control: same shape, one more declared capability.
	put(t, st, `(defn main-envfetch [] [(w {env (-> Str Str) fetch (-> Str Str)}) (args (List Str))] Str
		((. w env) ((. w fetch) "http://example.invalid/")))`)
	markVerified(t, st, "main-env")
	markVerified(t, st, "main-envfetch")

	confined, confinedSrc := buildProgram(t, st, "main-env")
	control, controlSrc := buildProgram(t, st, "main-envfetch")

	// 1. The emitted source of the confined program contains no HTTP authority.
	for _, forbidden := range []string{`"net/http"`, "http.Get"} {
		if strings.Contains(confinedSrc, forbidden) {
			t.Errorf("a program requiring only env emitted %s — undeclared authority reached the artifact", forbidden)
		}
	}
	// The control proves the absence is caused by the declaration, not by the
	// compiler being unable to emit HTTP at all.
	for _, expected := range []string{`"net/http"`, "http.Get"} {
		if !strings.Contains(controlSrc, expected) {
			t.Errorf("a program requiring fetch did not emit %s — the control is not controlling anything", expected)
		}
	}

	// 2. The BINARY does not link an HTTP client. Source inspection could be
	//    fooled by a helper reached indirectly; the symbol table cannot.
	//
	//    Measured by the PRESENCE OF net/http SYMBOLS AT ALL, and the control's
	//    count is asserted too. An earlier version of this test looked for the
	//    symbol `net/http.Get`, which the Go linker never emits under that name —
	//    so it passed for both programs and proved nothing. A confinement test
	//    that cannot fail is worse than none, because it reads as evidence.
	confinedSyms := httpSymbolCount(t, confined)
	controlSyms := httpSymbolCount(t, control)
	if confinedSyms != 0 {
		t.Errorf("the confined binary links %d net/http symbols — the capability is present, merely unused", confinedSyms)
	}
	if controlSyms == 0 {
		t.Error("the control binary links no net/http symbols either, so the measurement does not discriminate")
	}

	// 3. It runs, in a host that has both authorities, and does the env work.
	cmd := exec.Command(confined)
	cmd.Env = append(os.Environ(), "OATH_TEST_VAR=granted")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "granted" {
		t.Fatalf("confined program printed %q, want %q", got, "granted")
	}
}

// An entry typed (-> {} (-> (List Str) Str)) requires no authority and still
// TAKES a capability argument — its arity comes from its type. Emitting the
// record only when there are requirements confuses "requires nothing" with "takes
// no argument": argv would be passed where the record belongs, and the program
// would try to print a closure.
func TestEmptyCapabilityRecordIsStillApplied(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn no-caps [] [(w {}) (args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "no-caps")

	prog, err := planProgram(st, "no-caps")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	if prog.CapTy == nil {
		t.Fatal("an empty capability record must still be a capability record")
	}
	if len(prog.Requirements) != 0 {
		t.Fatalf("requirements = %v, want none", prog.Requirements)
	}

	bin, _ := buildProgram(t, st, "no-caps")
	out, err := exec.Command(bin, "hello").Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "hello" {
		t.Fatalf("printed %q, want %q", got, "hello")
	}
}

// A handler links net/http because SERVING is its ingress protocol — the host
// owns the socket. That must not smuggle in the outbound CLIENT: being called is
// not authority, and a handler that declares no `fetch` holds none.
//
// The leak this guards was exactly one line: a keepalive table mapping the
// net/http import to `http.Get`, which planted a reference to the outbound client
// in every handler ever compiled. "Absent from the artifact" has to survive
// contact with the artifact's own import list.
func TestHandlerDoesNotCarryTheOutboundClient(t *testing.T) {
	st := capStore(t)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Request Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Response Int (List (Pair Str Str)) (List Int)))`)
	// A handler holding a write capability and no network capability.
	put(t, st, `(defn hnd [] [(caps {emit (-> Str Str)}) (r Request)] Response
		(match r ((Request m p hs b t)
			(Response 200 (Nil [(Pair Str Str)]) (Nil [Int])))))`)
	markVerified(t, st, "hnd")

	prog, err := planProgram(st, "hnd")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	if prog.Protocol != entryHandler {
		t.Fatalf("hnd was classified as %s, not a handler", entryProtocolName(prog.Protocol))
	}
	src, err := emitProgram(st, prog)
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	if !strings.Contains(src, "http.ListenAndServe") {
		t.Fatal("a handler must serve; this one does not, so the test proves nothing")
	}
	if strings.Contains(src, "http.Get") {
		t.Error("a handler declaring no fetch references the outbound HTTP client")
	}
}

// httpSymbolCount reports how many net/http symbols a binary links. Zero means
// the package is not in the artifact at all — not that it is present and unused.
func httpSymbolCount(t *testing.T, bin string) int {
	t.Helper()
	out, err := runIn(t.TempDir(), "go", "tool", "nm", bin)
	if err != nil {
		t.Skipf("go tool nm unavailable: %v", err)
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "net/http") {
			n++
		}
	}
	return n
}

// ---------- the launch gate ----------

// A required capability this host cannot provide must stop the program before it
// runs. The record sink is the case with a real precondition: a sink path that
// cannot be opened for append is a provision failure.
//
// The defect this closes: previously the sink was reopened per call and an
// unwritable path meant every emit returned the failure value forever, so a
// program reported success while writing nowhere — "the call returned an empty
// result" and "this host never supplied the capability" were the same state.
func TestUnprovidableCapabilityRefusesLaunch(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn main-emit [] [(w {emit (-> Str Str)}) (args (List Str))] Str
		((. w emit) "record"))`)
	markVerified(t, st, "main-emit")
	bin, _ := buildProgram(t, st, "main-emit")

	// A path under a file (not a directory) cannot be opened for append.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_EMIT_PATH="+filepath.Join(blocker, "sink.log"))
	stdout, err := cmd.Output()
	if err == nil {
		t.Fatalf("program launched with an unprovidable capability and printed %q", stdout)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected failure: %v", err)
	}
	if ee.ExitCode() != exitCapabilityUnavailable {
		t.Fatalf("exit code %d, want %d (EX_UNAVAILABLE)", ee.ExitCode(), exitCapabilityUnavailable)
	}
	// The entry point must not have run: an unprovidable capability is not a
	// value the program gets to observe.
	if len(stdout) != 0 {
		t.Fatalf("the entry point produced output (%q) despite a failed capability resolution", stdout)
	}
	msg := string(ee.Stderr)
	if !strings.Contains(msg, "emit") || !strings.Contains(msg, string(capRecordSink)) {
		t.Fatalf("the refusal does not name the capability or its kind: %q", msg)
	}

	// The same binary, a host that CAN provide the sink: it launches and writes.
	sink := filepath.Join(dir, "sink.log")
	cmd = exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_EMIT_PATH="+sink)
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("run with a writable sink: %v", err)
	}
	b, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("sink was not written: %v", err)
	}
	if !strings.Contains(string(b), "record") {
		t.Fatalf("sink holds %q, want the emitted record", b)
	}

	// The sink FOLLOWS ITS PATH. The launch check opens and closes; each write
	// reopens. Holding the descriptor would leave a rotated sink — renamed and
	// replaced — receiving records at the old inode forever while the program
	// still reported success.
	rotated := filepath.Join(dir, "sink.log.1")
	if err := os.Rename(sink, rotated); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_EMIT_PATH="+sink)
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("run after rotation: %v", err)
	}
	after, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("the rotated sink was not recreated at its path: %v", err)
	}
	if !strings.Contains(string(after), "record") {
		t.Fatalf("post-rotation sink holds %q; the write followed the old inode", after)
	}
}

// ---------- provenance ----------

// The manifest describes the ARTIFACT, in Oath's vocabulary. A manifest naming Go
// packages would describe one build of one backend and would stop being true the
// moment the backend changed; naming kinds keeps it true.
func TestProvenanceRecordsNeutralRequirements(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn main-net [] [(w {fetch (-> Str Str)}) (args (List Str))] Str
		((. w fetch) "http://example.invalid/"))`)
	markVerified(t, st, "main-net")
	bin, _ := buildProgram(t, st, "main-net")

	out := readProvenance(t, bin)
	var m ProvenanceManifest
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v\n%s", err, out)
	}
	if m.Schema != provenanceSchema {
		t.Errorf("schema %q, want %q", m.Schema, provenanceSchema)
	}
	if m.Entry != "main-net" {
		t.Errorf("entry %q, want main-net", m.Entry)
	}
	h, _ := st.Resolve("main-net")
	if m.EntryHash != h {
		t.Errorf("entry_hash %q, want %q — the manifest must identify the artifact", m.EntryHash, h)
	}
	if len(m.Closure) == 0 {
		t.Error("closure is empty; provenance must record what the artifact was built from")
	}
	if len(m.Requirements) != 1 || m.Requirements[0].Kind != capHTTPRequest {
		t.Fatalf("requirements = %v, want one http_request", m.Requirements)
	}
	if m.Kernel != kernelVersion || m.Backend != goBackendVersion {
		t.Errorf("kernel/backend = %q/%q, want %q/%q", m.Kernel, m.Backend, kernelVersion, goBackendVersion)
	}
	// Nothing Go-shaped may appear in it.
	for _, leak := range []string{"net/http", "capFn", "func(", "os.Getenv"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("manifest leaks the backend implementation (%q): %s", leak, out)
		}
	}
}

// A program requiring nothing still carries provenance, and says so honestly
// rather than omitting the field — "requires nothing" is a claim worth recording.
func TestProvenanceForPureEntry(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn pure-entry [] [(args (List Str))] Str "ok")`)
	markVerified(t, st, "pure-entry")
	bin, src := buildProgram(t, st, "pure-entry")

	// No requirements means no resolution machinery at all, not an empty loop.
	if strings.Contains(src, "func oathResolveCapabilities") {
		t.Error("a program requiring no capabilities emitted the capability resolver")
	}
	out := readProvenance(t, bin)
	var m ProvenanceManifest
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v\n%s", err, out)
	}
	if len(m.Requirements) != 0 {
		t.Errorf("requirements = %v, want none", m.Requirements)
	}
	// "Requires nothing" is a claim the record makes, so it must be an empty
	// array. `null` reads as a field nobody filled in.
	if !strings.Contains(string(out), `"requirements": []`) {
		t.Errorf("an artifact requiring nothing must say so as []:\n%s", out)
	}
	if m.Protocol != "cli" {
		t.Errorf("protocol %q, want cli", m.Protocol)
	}

	// And it still behaves as a CLI program when not asked for provenance.
	plain, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(plain), "\n"); got != "ok" {
		t.Fatalf("printed %q, want ok", got)
	}
}

// Provenance must not take authority over a channel already granted to the
// program. A program holding `env` may read ANY variable name — so a compiler
// that reserved one for provenance would give that program a process exit where
// its `process_env` capability promised a value.
//
// This is a regression test for a real defect in the first version of this pass,
// which shipped exactly that: OATH_PROVENANCE printed the manifest and exited.
// The failure mode is not hypothetical — environments are inherited, so one
// exported variable would have silently changed every Oath artifact beneath it.
func TestProvenanceDoesNotShadowTheEnvCapability(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn read-var [] [(w {env (-> Str Str)}) (args (List Str))] Str
		((. w env) "OATH_PROVENANCE"))`)
	markVerified(t, st, "read-var")
	bin, _ := buildProgram(t, st, "read-var")

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_PROVENANCE=a-value-the-program-is-entitled-to")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the program exited instead of reading its environment: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "a-value-the-program-is-entitled-to" {
		t.Fatalf("program read %q; provenance is shadowing a variable the env capability owns", got)
	}

	// Reading provenance from the file still works — the two never interact.
	if len(readProvenance(t, bin)) == 0 {
		t.Fatal("the artifact carries no provenance")
	}
}

// argv belongs to a CLI entry point in the same way: its type is (List Str), so
// every argument is a value the verified logic is entitled to receive. A reserved
// flag would shadow one, and the program must see whatever it was handed.
func TestProvenanceDoesNotShadowArgv(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn first-arg [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "first-arg")
	bin, _ := buildProgram(t, st, "first-arg")

	for _, arg := range []string{"--provenance", "-provenance", "provenance", "--oath-provenance"} {
		out, err := exec.Command(bin, arg).Output()
		if err != nil {
			t.Fatalf("%s: the program exited instead of receiving its argument: %v", arg, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != arg {
			t.Errorf("passed %q, program saw %q — argv is being intercepted", arg, got)
		}
	}
}

// A marker is a byte string, and an artifact's own data lives in the same image.
// Extraction must therefore not let unrelated bytes decide what an artifact says
// about itself — the first match is not the answer, a valid manifest is.
//
// The decoy case here is not hypothetical: a build placing a program string that
// opens with the marker BEFORE the real manifest made a first-match reader return
// the decoy's bytes joined to the real record's close.
func TestProvenanceExtractionRejectsDecoys(t *testing.T) {
	const realHash = "1111111111111111111111111111111111111111111111111111111111111111"
	real := ProvenanceManifest{
		Schema: provenanceSchema, Entry: "real", EntryHash: realHash,
		EntryType: "(-> (List Str) Str)", Protocol: "cli", Guarantee: "tested",
		Requirements: []CapabilityRequirement{},
		Closure:      []string{realHash}, Kernel: kernelVersion, Backend: goBackendVersion,
	}
	if err := real.validate(); err != nil {
		t.Fatalf("the fixture is not a valid manifest, so this test proves nothing: %v", err)
	}
	realBlob := real.embeddedManifest()

	// A record from a newer compiler: a capability this build does not define.
	future := real
	future.Requirements = []CapabilityRequirement{{Field: "clock", Kind: "wall_time", Slot: 0}}

	// depHash sorts before realHash, so a two-entry closure is canonically
	// [depHash, realHash]; the unsorted fixture swaps them.
	const depHash = "0000000000000000000000000000000000000000000000000000000000000000"
	twoDep := real
	twoDep.Closure = []string{depHash, realHash}

	// The same capability twice — impossible from a canonicalized record type.
	twoCaps := real
	twoCaps.Requirements = []CapabilityRequirement{
		{Field: "env", Kind: capProcessEnv, Slot: 0},
		{Field: "env", Kind: capProcessEnv, Slot: 1},
	}

	other := real
	other.Entry = "other"

	for _, tc := range []struct {
		name    string
		bytes   string
		want    string // expected entry name, or "" when extraction must fail
		wantErr string
	}{
		{
			name:  "decoy opening marker before the record",
			bytes: "junk" + provenanceBegin + "not json at all" + realBlob + "tail",
			want:  "real",
		},
		{
			name:  "decoy holding valid JSON of the wrong schema",
			bytes: provenanceBegin + `{"schema":"something-else"}` + provenanceEnd + realBlob,
			want:  "real",
		},
		{
			name:  "record after unrelated binary noise",
			bytes: "\x00\x01\x02binary noise\x00" + realBlob,
			want:  "real",
		},
		{
			// Parsing is not validation: an empty record mentions the schema and
			// names no artifact, no closure, and no authority.
			name:    "record with only a schema",
			bytes:   provenanceBegin + `{"schema":"oath-provenance/1"}` + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			name:    "record whose closure omits its own entry",
			bytes:   provenanceBegin + `{"schema":"oath-provenance/1","entry":"x","entry_hash":"` + realHash + `","entry_type":"t","protocol":"cli","guarantee":"tested","closure":["` + strings.Repeat("2", 64) + `"],"kernel":"k","backend":"b"}` + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			// A field DETERMINES its kind, so this record contradicts itself and no
			// version of this compiler could have produced it.
			name:    "record whose field and kind disagree",
			bytes:   provenanceBegin + `{"schema":"oath-provenance/1","entry":"x","entry_hash":"` + realHash + `","entry_type":"t","protocol":"cli","guarantee":"tested","closure":["` + realHash + `"],"requirements":[{"field":"fetch","kind":"process_env","slot":0}],"kernel":"k","backend":"b"}` + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			// A capability this build has never heard of is NOT a contradiction —
			// the vocabulary grows, and an older reader meeting a newer artifact
			// should report what it found rather than deny the record exists.
			// cmdProvenance warns; extraction accepts.
			name:  "record naming a capability this build does not know",
			bytes: future.embeddedManifest(),
			want:  "real",
		},
		{
			// `null` is not `[]`. An omitted authority list would otherwise read as
			// a stated empty one, and the same content would have two canonical
			// forms.
			name:    "record whose requirements are null",
			bytes:   provenanceBegin + strings.Replace(string(real.manifestJSON()), `"requirements": []`, `"requirements": null`, 1) + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			// programClosure derives a sorted, duplicate-free list of definition
			// hashes. Checking the shape a producer must have produced is the
			// difference between "this parses" and "this could have come from a
			// build".
			name: "record whose closure holds something that is not a hash",
			bytes: provenanceBegin + strings.Replace(string(real.manifestJSON()),
				`"`+realHash+`"`+"\n  ]", `"`+realHash+`",`+"\n    \"not-a-hash\"\n  ]", 1) + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			name: "record whose closure is unsorted",
			bytes: provenanceBegin + strings.Replace(string(twoDep.manifestJSON()),
				`"`+depHash+`",`+"\n    \""+realHash+`"`, `"`+realHash+`",`+"\n    \""+depHash+`"`, 1) + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			// Record field order is canonicalized away by the kernel, so a
			// capability record's fields are sorted and unique. A record naming the
			// same capability twice is not a program this compiler could build.
			name:    "record naming the same capability twice",
			bytes:   twoCaps.embeddedManifest(),
			wantErr: "no Oath provenance found",
		},
		{
			// Two `entry_hash` keys: encoding/json takes the last, another reader
			// may take the first, and the same bytes would then name two different
			// artifacts. Canonical form is what settles it.
			name:    "record with a duplicate key",
			bytes:   provenanceBegin + `{"schema":"oath-provenance/1","entry":"x","entry_hash":"` + realHash + `","entry_hash":"` + strings.Repeat("4", 64) + `","entry_type":"t","protocol":"cli","guarantee":"tested","closure":["` + strings.Repeat("4", 64) + `"],"requirements":[],"kernel":"k","backend":"b"}` + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			// Same fields, non-canonical rendering. Within a schema version the
			// encoding is exact; a new field is what the version is for.
			name:    "canonical fields in a non-canonical encoding",
			bytes:   provenanceBegin + strings.ReplaceAll(string(real.manifestJSON()), "\n", "") + provenanceEnd,
			wantErr: "no Oath provenance found",
		},
		{
			name:    "opening marker with no close",
			bytes:   "prefix" + provenanceBegin + `{"schema":"oath-provenance/1"}`,
			wantErr: "no Oath provenance found",
		},
		{
			name:    "two different valid records",
			bytes:   realBlob + "middle" + other.embeddedManifest(),
			wantErr: "DIFFERENT provenance records",
		},
		{
			name:  "the same record twice is not ambiguous",
			bytes: realBlob + realBlob,
			want:  "real",
		},
		{
			name:    "nothing at all",
			bytes:   "an ordinary file",
			wantErr: "no Oath provenance found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractProvenance([]byte(tc.bytes))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("extracted %q, want refusal containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractProvenance: %v", err)
			}
			var m ProvenanceManifest
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("extracted bytes are not a manifest: %v\n%q", err, got)
			}
			if m.Entry != tc.want {
				t.Fatalf("extracted entry %q, want %q", m.Entry, tc.want)
			}
		})
	}
}

// Extraction reads files chosen by whoever handed you the artifact, so a
// malformed one must not be expensive. A file of opening markers with no close
// used to re-search the whole remaining suffix once per marker.
func TestProvenanceExtractionIsNotQuadraticOnGarbage(t *testing.T) {
	var unterminated strings.Builder
	for i := 0; i < 200_000; i++ {
		unterminated.WriteString(provenanceBegin)
		unterminated.WriteString("{padding padding padding padding}")
	}
	// The harder shape: many opening markers and ONE closing marker at the very
	// end, so every candidate would otherwise be scanned and parsed to the far end.
	var oneFarEnd strings.Builder
	for i := 0; i < 200_000; i++ {
		oneFarEnd.WriteString(provenanceBegin)
		oneFarEnd.WriteString("{padding padding padding padding}")
	}
	oneFarEnd.WriteString(provenanceEnd)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, garbage := range []string{unterminated.String(), oneFarEnd.String()} {
			if _, err := extractProvenance([]byte(garbage)); err == nil {
				t.Error("garbage was accepted as provenance")
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("extraction did not finish on a file of unterminated markers")
	}
}

// The manifest digest is a function of the manifest and nothing else: two builds
// of the same program agree, and any change to what the artifact requires or was
// built from changes it.
func TestProvenanceDigestIsStable(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn stable [] [(args (List Str))] Str "x")`)
	markVerified(t, st, "stable")

	a, err := planProgram(st, "stable")
	if err != nil {
		t.Fatal(err)
	}
	b, err := planProgram(st, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if a.Provenance.digest() != b.Provenance.digest() {
		t.Fatal("two plans of the same program disagree on their provenance digest")
	}
	// The planner must not name a backend. Doing so would make every future
	// lowering's manifest claim to be the Go emitter, and would point the
	// dependency backwards — the neutral layer describing the backend rather than
	// the backend claiming the artifact.
	if a.Provenance.Backend != "" {
		t.Fatalf("planProgram stamped backend %q; only a backend may claim an artifact", a.Provenance.Backend)
	}
	if err := a.stampBackend("some-other-backend/1"); err != nil {
		t.Fatalf("stampBackend: %v", err)
	}
	if a.Provenance.Backend != "some-other-backend/1" {
		t.Fatalf("backend is %q after stamping", a.Provenance.Backend)
	}
	changed := a.Provenance
	changed.Requirements = []CapabilityRequirement{{Field: "env", Kind: capProcessEnv}}
	if changed.digest() == a.Provenance.digest() {
		t.Fatal("changing the required authority did not change the provenance digest")
	}
}
