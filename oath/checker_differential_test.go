package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// THE CHECKER DIFFERENTIAL FIXTURE (#149, step 1 of the port).
//
// The bidirectional checker is about to be defunctionalized onto an explicit
// stack. This freezes its observable behaviour FIRST, so the new machine has
// something to reproduce that is not the old machine's source code.
//
// WHY A FIXTURE AND NOT A RETAINED ORACLE. The iterative reader kept its
// recursive predecessor in the test file, because it is 45 lines. The checker is
// a five-way mutually recursive group across ~700 lines with inference state and
// mid-flight mutation; a duplicate would rot. So the FIXTURE is the durable
// oracle and the old checker is scaffolding, which is why this exists before any
// machine does.
//
// WHAT IS CAPTURED, and why each is separately necessary:
//
//	outcome        accepted / refused / elaboration failure
//	category       a STABLE diagnostic class, not the message text
//	hash_before    canonical hash of the elaborated Def, pre-check
//	hash_after     canonical hash AFTER checking
//	infer_entries  entries into constructor type-argument inference
//
// hash_after is the whole term comparison, for free and exactly. checkDef
// backfills every solved type argument into the AST before it is hashed, so an
// inferred call is byte-identical to the explicit one — which means the pair
// (before, after) records the surviving mutation of the checked term precisely.
// Writing a structural comparator instead would compare the fields its author
// remembered; canonical identity compares every byte, and stays aligned with
// artifact identity rather than drifting from it.
//
// infer_entries is the COMPLEXITY witness, and its necessity is ARGUED rather
// than demonstrated — stated that way deliberately, because the sharper claim
// was made first and did not survive checking.
//
// What IS measured. Two mutants were constructed and both are caught:
// propagating pass-1 synthesis errors moves 25 fields, and rolling TyArgs back
// at the end of synthCtor moves 81 — INCLUDING hash_after, because a wiped
// TyArgs is visible in the canonical bytes. So the earlier claim that such a
// mutant "produces correct canonical output and slips past a result-bytes
// fixture" is false for every mutant reachable by a local edit.
//
// What is NOT measured. The dangerous shape is a port that loses the MEMO while
// producing the correct final term — a functional machine that threads solved
// arguments and writes them back at the end. That cannot be produced by editing
// this checker: dropping the len(tyargs)==0 guard also re-infers over EXPLICIT
// type arguments, which moves 88 outcomes and is a semantic change, not an
// isolated memo loss. The shape only becomes constructible during the port
// itself, which is exactly when the counter has to already exist.
//
// Its demonstrated value is being EARLIER and SHARPER: the rollback mutant is
// visible at n=1 as 2 entries against 1, where a stopwatch sees nothing, and it
// reports in the vocabulary of the defect rather than as a wall of moved hashes.

type checkOutcome struct {
	Name         string `json:"name"`
	Outcome      string `json:"outcome"`
	Category     string `json:"category,omitempty"`
	HashBefore   string `json:"hash_before,omitempty"`
	HashAfter    string `json:"hash_after,omitempty"`
	InferEntries int    `json:"infer_entries"`
}

// diagnosticCategory maps an error to a STABLE class. The port is allowed to
// reword a message; it is not allowed to change which KIND of thing went wrong,
// nor which diagnostic wins when several could.
//
// Deliberately ordered most-specific first, and ending in "other" rather than
// panicking: an uncategorised error still records SOMETHING, and a case whose
// category is "other" is a signal to add a class, not a test failure.
func diagnosticCategory(err error) string {
	if err == nil {
		return ""
	}
	m := strings.ToLower(err.Error())
	for _, c := range []struct{ needle, class string }{
		{"cannot infer type argument", "inference-incomplete"},
		{"constructor takes", "ctor-arity"},
		{"constructor given", "tyargs-arity"},
		{"constructor index", "ctor-index"},
		{"is not a data definition", "not-a-datatype"},
		{"binders must be concrete", "binder-not-concrete"},
		{"needs a type and a body", "malformed-def"},
		{"expected", "type-mismatch"},
		{"unbound", "unbound"},
		{"primitive", "prim-misuse"},
	} {
		if strings.Contains(m, c.needle) {
			return c.class
		}
	}
	return "other"
}

// runCheckCase elaborates one form and runs the checker over it exactly as
// checkDef's func branch does, capturing everything the port must reproduce.
// It performs NO store writes.
func runCheckCase(st *Store, name, src string) checkOutcome {
	out := checkOutcome{Name: name}
	forms, err := parseForms(src)
	if err != nil || len(forms) == 0 {
		out.Outcome = "parse-error"
		out.Category = "parse"
		return out
	}
	var def *Def
	switch {
	case len(forms[0].Kids) > 0 && forms[0].Kids[0].K == "sym" && forms[0].Kids[0].Sym == "data":
		def, _, err = elabData(st, forms[0])
	default:
		def, _, err = elabFunc(st, forms[0])
	}
	if err != nil {
		out.Outcome = "elab-error"
		out.Category = diagnosticCategory(err)
		return out
	}
	out.HashBefore = hashDef(def)

	if def.K != "func" || def.Ty == nil || def.Body == nil {
		out.Outcome = "accepted"
		out.HashAfter = out.HashBefore
		return out
	}
	c := &checker{st: st, selfTyVars: def.TyVars, selfTy: def.Ty}
	cerr := c.check(nil, def.Body, def.Ty)
	out.InferEntries = c.inferEntries
	if cerr != nil {
		out.Outcome = "refused"
		out.Category = diagnosticCategory(cerr)
		return out
	}
	out.Outcome = "accepted"
	out.HashAfter = hashDef(def)
	return out
}

// adversarialCases is the population the CORPUS CANNOT PROVIDE. The corpus is
// curated and almost entirely successful, so a differential built from it alone
// would witness the happy path and nothing else. Each entry below targets a
// specific transition in synthCtor's protocol.
var adversarialCases = []struct{ name, src string }{
	// --- polymorphic inference, single parameter ---
	{"infer-one-param", `(defn t [] [] (Option Int) (Some 1))`},
	{"explicit-one-param", `(defn t [] [] (Option Int) (Some [Int] 1))`},
	{"nullary-needs-expected", `(defn t [] [] (Option Int) (None))`},
	{"nullary-explicit", `(defn t [] [] (Option Int) (None [Int]))`},

	// --- MULTIPLE type parameters: both solved from arguments ---
	{"infer-two-params", `(defn t [] [] (Pair Int Bool) (Pair 1 true))`},
	{"explicit-two-params", `(defn t [] [] (Pair Int Bool) (Pair [Int Bool] 1 true))`},

	// --- PASS-1 SUPPRESSION, the behaviour a "cleaner" port would break.
	//     Arguments are SYNTHESIZED in pass 1 with no expected type, so a nested
	//     constructor whose parameters only the expected type determines MUST
	//     fail synthesis there, be skipped for inference, and then succeed in
	//     pass 2 when it is checked against the substituted field type.
	//     An implementation that propagates the first pass-1 error rejects these.
	{"pass1-fails-pass2-succeeds", `(defn t [] [] (Option (Result Int Bool)) (Some (Ok 1)))`},
	{"pass1-fails-err-side", `(defn t [] [] (Option (Result Int Bool)) (Some (Err true)))`},
	{"pass1-fails-nested-twice", `(defn t [] [] (Option (Option (Result Int Bool))) (Some (Some (Ok 1))))`},
	{"pass1-fails-then-mismatch", `(defn t [] [] (Option Int) (Some (Ok 1)))`},

	// --- RENAMED after measurement: both of these were written as
	//     "underdetermined" and both are ACCEPTED, because the declared return
	//     type reaches matchTy(exp) and determines every parameter. Keeping the
	//     original names would have left two cases advertising a behaviour they
	//     do not exhibit. The genuine inference-incomplete refusal is
	//     pass1-fails-then-mismatch below. ---
	{"nested-ctor-in-pair", `(defn t [] [] (Pair (Result Int Bool) Int) (Pair (Ok 1) 2))`},
	{"ctor-under-tyvar-return", `(defn t [a] [(x a)] (Result Int a) (Ok 1))`},

	// --- spines: the two witnesses that cannot substitute for each other ---
	{"poly-spine-3", `(defn t [] [] (List Int) (Cons 1 (Cons 2 (Cons 3 (Nil [Int])))))`},
	{"mono-spine-str", `(defn t [] [] Str "abc")`},

	// --- arity and type-argument-count failures ---
	{"ctor-too-few-args", `(defn t [] [] (Pair Int Bool) (Pair 1))`},
	{"ctor-too-many-args", `(defn t [] [] (Pair Int Bool) (Pair 1 true 2))`},
	{"tyargs-wrong-count", `(defn t [] [] (Option Int) (Some [Int Bool] 1))`},

	// --- expected-type mismatch, both directions ---
	{"expected-mismatch-simple", `(defn t [] [] Int (Some 1))`},
	{"expected-mismatch-param", `(defn t [] [] (Option Bool) (Some 1))`},
	{"expected-mismatch-nested", `(defn t [] [] (Option (Option Int)) (Some 1))`},

	// --- traversal order could decide which diagnostic wins: TWO bad
	//     arguments, so a machine that reports the second has changed
	//     observable behaviour even though both are errors. ---
	{"two-bad-args", `(defn t [] [] (Pair Int Bool) (Pair true 1))`},
	{"bad-arg-then-arity", `(defn t [] [] (Pair Int Bool) (Pair true))`},

	// --- non-constructor paths, so the fixture is not purely about synthCtor ---
	{"prim-arith", `(defn t [] [(x Int)] Int (+ x 1))`},
	{"prim-mismatch", `(defn t [] [(x Int)] Int (+ x true))`},
	{"if-branches-agree", `(defn t [] [(x Int)] Int (if (< x 0) 0 x))`},
	{"if-branches-differ", `(defn t [] [(x Int)] Int (if (< x 0) 0 true))`},
	{"let-binding", `(defn t [] [(x Int)] Int (let (y Int (+ x 1)) (+ y y)))`},
	{"match-on-option", `(defn t [] [(o (Option Int))] Int (match o ((None) 0) ((Some v) v)))`},
	{"match-arm-mismatch", `(defn t [] [(o (Option Int))] Int (match o ((None) 0) ((Some v) true)))`},
	{"match-ctor-in-arm", `(defn t [] [(o (Option Int))] (Option Int) (match o ((None) (Some 0)) ((Some v) (Some v))))`},
}

const fixturePath = "testdata/checker-differential.json"

// TestCheckerDifferential is the oracle. Regenerate with:
//
//	OATH_UPDATE_CHECKER_FIXTURE=1 go test -run TestCheckerDifferential
//
// and READ THE DIFF: a change here is a change to what the checker accepts, what
// it infers, or which diagnostic wins. During the port this file must not move
// at all.
func TestCheckerDifferential(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(filepath.Join(wd, "..", "codebase"))
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}

	got := map[string]checkOutcome{}
	for _, c := range adversarialCases {
		if _, dup := got[c.name]; dup {
			t.Fatalf("duplicate case name %q — one would silently shadow the other", c.name)
		}
		got[c.name] = runCheckCase(st, c.name, c.src)
	}

	// THE CORPUS TOO: necessary, not sufficient. It is what the kernel actually
	// reads, and it catches regressions the hand-written cases never imagined —
	// but it is curated and almost entirely successful, which is why the
	// adversarial population above exists at all.
	corpusN := 0
	for _, dir := range []string{"../examples", "../apps"} {
		_ = filepath.Walk(filepath.Join(wd, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".oath") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			forms, perr := parseForms(string(b))
			if perr != nil {
				return nil
			}
			rel, _ := filepath.Rel(wd, p)
			for i, f := range forms {
				if len(f.Kids) < 2 || f.Kids[1].K != "sym" {
					continue
				}
				name := fmt.Sprintf("corpus:%s#%d:%s", filepath.Base(rel), i, f.Kids[1].Sym)
				got[name] = runCheckCase(st, name, sxSource(b, f, forms, i))
				corpusN++
			}
			return nil
		})
	}
	if corpusN < 100 {
		t.Fatalf("only %d corpus definitions swept; the walk did not find the corpus, "+
			"so this fixture would freeze almost nothing", corpusN)
	}

	if os.Getenv("OATH_UPDATE_CHECKER_FIXTURE") != "" {
		writeFixture(t, got)
		t.Logf("regenerated %s: %d cases (%d adversarial + %d corpus)",
			fixturePath, len(got), len(adversarialCases), corpusN)
		return
	}

	want := readFixture(t)
	compareOutcomes(t, want, got)
}

// sxSource re-serializes one top-level form back to source. The corpus sweep
// needs each definition INDIVIDUALLY so a single failure does not mask the rest,
// and forms carry no byte offsets, so the whole file is passed and the index
// selects the form. Simpler and exact: hand the file to the elaborator and let
// runCheckCase take forms[0]... which would only ever check the first. So this
// slices the file by re-reading it per form instead.
func sxSource(file []byte, f sx, all []sx, idx int) string {
	// Forms are top-level and newline-separated in this corpus; reconstructing
	// exact bytes is unnecessary because the checker consumes the ELABORATED
	// def. Re-emitting the source is avoided entirely by passing the whole file
	// and letting the caller index — but runCheckCase takes forms[0], so the
	// simplest correct thing is to hand back a source containing only this form.
	// The corpus is s-expression source with balanced parens, so a form's text
	// can be recovered by counting delimiters from its start line.
	lines := strings.Split(string(file), "\n")
	start := f.Line - 1
	if start < 0 || start >= len(lines) {
		return ""
	}
	depth, out := 0, []string{}
	for i := start; i < len(lines); i++ {
		out = append(out, lines[i])
		for _, r := range lines[i] {
			switch r {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			}
		}
		if depth <= 0 && len(out) > 0 {
			break
		}
	}
	return strings.Join(out, "\n")
}

func writeFixture(t *testing.T, m map[string]checkOutcome) {
	t.Helper()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]checkOutcome, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, m[k])
	}
	b, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T) map[string]checkOutcome {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("fixture missing (%v)\n  regenerate: OATH_UPDATE_CHECKER_FIXTURE=1 go test -run TestCheckerDifferential", err)
	}
	var rows []checkOutcome
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("fixture unreadable: %v", err)
	}
	m := make(map[string]checkOutcome, len(rows))
	for _, r := range rows {
		m[r.Name] = r
	}
	return m
}

// compareOutcomes reports every field that moved, per case. It does NOT stop at
// the first difference: during a port the SHAPE of the divergence is the
// diagnosis, and one line of output would hide it.
func compareOutcomes(t *testing.T, want, got map[string]checkOutcome) {
	t.Helper()
	if len(want) == 0 {
		t.Fatal("fixture is empty — it would certify anything")
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("%s: present in the fixture, MISSING from this run", name)
			continue
		}
		for _, f := range []struct{ field, w, g string }{
			{"outcome", w.Outcome, g.Outcome},
			{"category", w.Category, g.Category},
			{"hash_before", w.HashBefore, g.HashBefore},
			{"hash_after", w.HashAfter, g.HashAfter},
			{"infer_entries", fmt.Sprint(w.InferEntries), fmt.Sprint(g.InferEntries)},
		} {
			if f.w != f.g {
				t.Errorf("%s: %s changed\n  fixture: %s\n  now:     %s", name, f.field, f.w, f.g)
			}
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: new case not in the fixture — regenerate deliberately, do not ignore", name)
		}
	}
}
