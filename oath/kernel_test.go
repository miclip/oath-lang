package main

// Regression tests for kernel soundness and totality. Each test pins a
// specific defect found in review: a false PROVEN from a non-total function, a
// false `total` from a negative datatype, a divide-by-zero DoS in generation, a
// panic on a malformed stored object, and a silent proof demotion by verify.
//
// Tests that need the solver skip cleanly when z3 is absent.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return st
}

func put(t *testing.T, st *Store, src string) []putReport {
	t.Helper()
	reports, err := apiPut(st, src, "test", "")
	if err != nil {
		t.Fatalf("apiPut(%q): %v", src, err)
	}
	return reports
}

func requireZ3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH")
	}
}

// A Str field inside an ADT is reached by generation at size 0 and its field at
// size -1; before the size clamp this divided by zero and crashed `put`.
func TestStrInADTDoesNotPanic(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Box [] (Mk Str))`)
	reps := put(t, st, `(defn unbox [] [(b Box)] Str
		(match b ((Mk s) s))
		(prop trivial [(b Box)] (== (unbox b) (unbox b))))`)
	last := reps[len(reps)-1]
	if last.Status != "accepted" {
		t.Fatalf("unbox: status=%q, want accepted", last.Status)
	}
	if !strings.HasPrefix(last.Guarantee, "tested") {
		t.Fatalf("unbox: guarantee=%q, want tested", last.Guarantee)
	}
}

// `data D = C (D -> D)` is not strictly positive: it encodes nontermination
// with no self-recursion and would otherwise be blessed `total`. It must be
// rejected at the gate.
func TestNegativeDatatypeRejected(t *testing.T) {
	st := newStore(t)
	reps := put(t, st, `(data D [] (C (-> D D)))`)
	if len(reps) != 1 || reps[0].Status != "rejected" {
		t.Fatalf("negative datatype: reports=%+v, want one rejected", reps)
	}
	if !strings.Contains(reps[0].Error, "strictly positive") {
		t.Fatalf("negative datatype: error=%q, want strict-positivity message", reps[0].Error)
	}
}

// A positive recursive datatype (a list) must still be accepted.
func TestPositiveDatatypeAccepted(t *testing.T) {
	st := newStore(t)
	reps := put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	if reps[0].Status != "accepted" {
		t.Fatalf("List: status=%q err=%q, want accepted", reps[0].Status, reps[0].Error)
	}
}

// An object written straight into the store (never gate-checked) that is
// structurally incomplete must be rejected on load, not fault the checker or
// evaluator.
func TestMalformedStoredObjectRejected(t *testing.T) {
	st := newStore(t)
	bad := &Def{K: "func", TyVars: 0} // no Ty, no Body
	b, _ := json.Marshal(bad)
	h := hashDef(bad)
	if err := os.WriteFile(filepath.Join(st.Root, "objects", h+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDef(h); err == nil {
		t.Fatalf("GetDef on malformed object returned no error; want rejection")
	}
}

// Nested malformed shapes in a directly-written object must be rejected on
// load, not fault the checker. Each payload hashes to its own filename and
// omits a required child.
func TestMalformedNestedObjectsRejected(t *testing.T) {
	cases := map[string]*Def{
		"fun type missing domain/codomain": {K: "func", TyVars: 0, Ty: &Ty{K: "fun"}, Body: &Term{K: "int"}},
		"lam missing param type":           {K: "func", TyVars: 0, Ty: tFun(tInt(), tInt()), Body: &Term{K: "lam", A: &Term{K: "int"}}},
		"app missing function":             {K: "func", TyVars: 0, Ty: tInt(), Body: &Term{K: "app", B: &Term{K: "int"}}},
		"if missing else":                  {K: "func", TyVars: 0, Ty: tInt(), Body: &Term{K: "if", A: &Term{K: "bool", Bool: true}, B: &Term{K: "int"}}},
		"field on missing record":          {K: "func", TyVars: 0, Ty: tInt(), Body: &Term{K: "field", Op: "x"}},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			b, _ := json.Marshal(bad)
			h := hashDef(bad)
			if err := os.WriteFile(filepath.Join(st.Root, "objects", h+".json"), b, 0o644); err != nil {
				t.Fatal(err)
			}
			// Must return an error, and must not panic.
			if _, err := st.GetDef(h); err == nil {
				t.Fatalf("GetDef accepted a malformed object (%s)", name)
			}
		})
	}
}

// A stale `proven` level (e.g. carried over from a kernel before the non-total
// axiom gate) must be demoted when a rerun proves fewer than all properties.
// The second property uses `/`, which is outside the SMT fragment, so it is
// withheld — leaving the def genuinely proven on 1 of 2 props.
func TestStaleProvenDemoted(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn halfish [] [(x Int)] Int
		(if (< x 0) (neg x) x)
		(prop non-negative [(x Int)] (<= 0 (halfish x)))
		(prop div-bound [(x Int)] (== (/ x 1) (/ x 1))))`)
	h, _ := st.Resolve("halfish")
	// Seed corrupt/stale metadata: claim fully proven.
	m, _ := st.GetMeta(h)
	m.Guarantee.Level = "proven"
	m.Guarantee.Proven = 2
	m.ProvenProps = []int{0, 1}
	if err := st.SetMeta(h, m); err != nil {
		t.Fatal(err)
	}
	if _, err := apiProve(st, "halfish"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	m, _ = st.GetMeta(h)
	if m.Guarantee.Level == "proven" {
		t.Fatalf("stale proven not demoted: level=%q proven_props=%v (want not proven; / is outside the fragment)", m.Guarantee.Level, m.ProvenProps)
	}
	if len(m.ProvenProps) >= len(d(st, h).Props) {
		t.Fatalf("expected fewer than all props proven, got proven_props=%v", m.ProvenProps)
	}
}

// Fixture generation must be read-only (never mutate the store) and
// deterministic (byte-identical across runs) — both regressed during
// development.
func TestFixturesReadOnlyAndDeterministic(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn abs [] [(x Int)] Int
		(if (< x 0) (neg x) x)
		(prop non-negative [(x Int)] (<= 0 (abs x))))`)
	h, _ := st.Resolve("abs")
	before, _ := os.ReadFile(filepath.Join(st.Root, "meta", h+".json"))

	dirA, dirB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	if _, err := apiFixtures(st, dirA); err != nil {
		t.Fatalf("apiFixtures: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(st.Root, "meta", h+".json"))
	if string(before) != string(after) {
		t.Fatalf("fixtures mutated the store meta:\n before=%s\n after =%s", before, after)
	}
	if _, err := apiFixtures(st, dirB); err != nil {
		t.Fatalf("apiFixtures (2nd): %v", err)
	}
	readTree := func(root string) map[string]string {
		out := map[string]string{}
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				b, _ := os.ReadFile(p)
				rel, _ := filepath.Rel(root, p)
				out[rel] = string(b)
			}
			return nil
		})
		return out
	}
	a, b := readTree(dirA), readTree(dirB)
	if len(a) != len(b) {
		t.Fatalf("fixture file count differs: %d vs %d", len(a), len(b))
	}
	for k, va := range a {
		if b[k] != va {
			t.Fatalf("fixture %q differs between runs (non-deterministic)", k)
		}
	}
}

// The journal hash chain must verify after normal appends, and detect an
// edited or deleted line.
func TestJournalChainDetectsTampering(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn one [] [] Int 1)`)
	put(t, st, `(defn two [] [] Int 2)`)
	put(t, st, `(defn three [] [] Int 3)`)
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("clean journal failed verification: %v", err)
	}
	logPath := filepath.Join(st.Root, "log.jsonl")
	pristine, _ := os.ReadFile(logPath)

	// Edit a middle line: change its author but keep it valid JSON.
	lines := strings.Split(strings.TrimRight(string(pristine), "\n"), "\n")
	var e LogEntry
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatal(err)
	}
	e.Author = "mallory"
	edited, _ := json.Marshal(e)
	lines[1] = string(edited)
	os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	if err := st.VerifyLog(); err == nil {
		t.Fatal("edited journal line went undetected")
	}

	// Delete a middle line.
	lines = strings.Split(strings.TrimRight(string(pristine), "\n"), "\n")
	os.WriteFile(logPath, []byte(lines[0]+"\n"+lines[2]+"\n"), 0o644)
	if err := st.VerifyLog(); err == nil {
		t.Fatal("deleted journal line went undetected")
	}
}

// A journal written before chaining existed is sealed retroactively: the first
// chained entry anchors to the hash of the whole legacy prefix, so edits to
// legacy lines are detected too.
func TestJournalLegacyPrefixSealed(t *testing.T) {
	st := newStore(t)
	logPath := filepath.Join(st.Root, "log.jsonl")
	legacy := `{"seq":1,"time":"2026-07-01T00:00:00Z","author":"alice","verifier":"oath-kernel/0.5","name":"old","status":"accepted"}` + "\n" +
		`{"seq":2,"time":"2026-07-02T00:00:00Z","author":"bob","verifier":"oath-kernel/0.5","name":"older","status":"rejected"}` + "\n"
	if err := os.WriteFile(logPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendLog(&LogEntry{Author: "carol", Name: "new", Status: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("legacy journal with chained tail failed verification: %v", err)
	}
	tampered := strings.Replace(legacy, "alice", "eve  ", 1)
	rest, _ := os.ReadFile(logPath)
	os.WriteFile(logPath, append([]byte(tampered), rest[len(legacy):]...), 0o644)
	if err := st.VerifyLog(); err == nil {
		t.Fatal("edit to a legacy journal line went undetected")
	}
}

// A corrupt names.json must refuse to open, not silently become an empty
// index (the index is not derivable from objects/).
func TestCorruptNamesIndexRejected(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	put(t, st, `(defn one [] [] Int 1)`)
	// Simulate a crash-truncated index.
	names := filepath.Join(dir, "names.json")
	b, _ := os.ReadFile(names)
	os.WriteFile(names, b[:len(b)/2], 0o644)
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("OpenStore accepted a corrupt names.json")
	}
}

// A context slice carries a hash of the definition versions served, and put
// stamps it into the journal (#4): implemented-against-stale-specs becomes
// detectable after the fact.
func TestContextHashJournaled(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn base [] [] Int 7)`)
	out, err := apiContext(st, []string{"base"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ctx string
	for _, line := range strings.Split(out, "\n") {
		if h, ok := strings.CutPrefix(line, "-- context-hash: "); ok {
			ctx = h
		}
	}
	if len(ctx) != 64 {
		t.Fatalf("context output carries no context-hash line:\n%s", out)
	}
	// The hash is over the served identity set, so it must equal the hash of
	// the single included definition's hash.
	h, _ := st.Resolve("base")
	if want := contextHash([]string{h}); ctx != want {
		t.Fatalf("context-hash = %s, want %s (sha256 of served hash set)", ctx, want)
	}
	if _, err := apiPut(st, `(defn built [] [] Int (+ (base) 1))`, "test", ctx); err != nil {
		t.Fatal(err)
	}
	entries := st.ReadLog()
	last := entries[len(entries)-1]
	if last.Name != "built" || last.Context != ctx {
		t.Fatalf("journal entry for built has context %q, want %q", last.Context, ctx)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal with context entries failed chain verification: %v", err)
	}
}

// d fetches a def by hash for a test assertion.
func d(st *Store, h string) *Def {
	def, err := st.GetDef(h)
	if err != nil {
		panic(err)
	}
	return def
}

// A non-total recursive function whose defining equation is inconsistent
// (evil x = evil x + 1) must NOT yield a proof of a false property.
func TestNonTotalFunctionNotProven(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn evil [] [(x Int)] Int
		(+ (evil x) 1)
		(prop false-claim [(x Int)] (< (evil x) (evil x))))`)
	out, err := apiProve(st, "evil")
	if err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	if strings.Contains(out, "∎ PROVEN") {
		t.Fatalf("evil: a false property was PROVEN:\n%s", out)
	}
	h, _ := st.Resolve("evil")
	m, _ := st.GetMeta(h)
	if len(m.ProvenProps) != 0 {
		t.Fatalf("evil: ProvenProps=%v, want empty", m.ProvenProps)
	}
}

// The inconsistency must not leak through the lemma library: a clean function
// that merely references evil must not be able to prove an absurdity.
func TestInconsistencyNotContagious(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn evil [] [(x Int)] Int
		(+ (evil x) 1)
		(prop false-claim [(x Int)] (< (evil x) (evil x))))`)
	apiProve(st, "evil")
	put(t, st, `(defn g [] [(x Int)] Int
		(+ x (evil x))
		(prop absurd [(x Int)] (== 1 2)))`)
	out, err := apiProve(st, "g")
	if err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	if strings.Contains(out, "∎ PROVEN") {
		t.Fatalf("g: an absurdity was PROVEN via a dependency:\n%s", out)
	}
}

// A total function's genuine proofs must survive re-verification: `verify` must
// not silently demote PROVEN back to tested.
func TestVerifyPreservesProven(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn abs [] [(x Int)] Int
		(if (< x 0) (neg x) x)
		(prop non-negative [(x Int)] (<= 0 (abs x))))`)
	if _, err := apiProve(st, "abs"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, _ := st.Resolve("abs")
	if m, _ := st.GetMeta(h); m.Guarantee.Level != "proven" {
		t.Fatalf("abs: level=%q after prove, want proven", m.Guarantee.Level)
	}
	if _, err := verifyDef(st, h); err != nil {
		t.Fatalf("verifyDef: %v", err)
	}
	if m, _ := st.GetMeta(h); m.Guarantee.Level != "proven" {
		t.Fatalf("abs: level=%q after re-verify, want proven (silent demotion)", m.Guarantee.Level)
	}
}
