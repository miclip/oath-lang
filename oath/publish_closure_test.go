package main

import (
	"strings"
	"testing"
)

// #185. `oath publish --namespace X` must perform a SOURCE transformation, not a
// display prefix: the registry re-elaborates the source it receives and derives
// the bound name from it, so the qualified name has to be in the source or the
// server rejects the mismatch (and cannot resolve bare intra-closure references
// under the namespace).

func TestQualifyNamesRewritesWholeSymbolsOnly(t *testing.T) {
	rename := map[string]string{"str-lt": "michael/oath/str-lt", "StrMap": "michael/oath/StrMap"}
	in := "(data StrMap [] (MkStrMap (List Int)))  ; StrMap in a comment\n" +
		`(defn use [] [] Bool (str-lt "str-lt" "x"))`
	got := qualifyNames(in, rename)

	// MkStrMap ends in "StrMap" but is not a whole token, so it must be untouched;
	// a substring rewrite would corrupt the constructor.
	if !strings.Contains(got, "(MkStrMap ") || strings.Contains(got, "michael/oath/MkStrMap") {
		t.Errorf("constructor MkStrMap was corrupted by a substring match:\n%s", got)
	}
	// The declared type and the function reference are both qualified.
	if !strings.Contains(got, "(data michael/oath/StrMap ") {
		t.Errorf("declared type not qualified:\n%s", got)
	}
	if !strings.Contains(got, "(michael/oath/str-lt \"str-lt\" \"x\")") {
		t.Errorf("function reference not qualified (or the string literal was touched):\n%s", got)
	}
	// A name appearing as comment text or as a string literal is data, not a
	// reference, and must be left exactly as written.
	if !strings.Contains(got, "; StrMap in a comment") {
		t.Errorf("comment text was altered:\n%s", got)
	}
	if !strings.Contains(got, "\"str-lt\"") {
		t.Errorf("string literal was altered:\n%s", got)
	}
}

func TestSplitTopLevelFormsIgnoresParensInCommentsAndStrings(t *testing.T) {
	src := "(defn a [] [] Int 1) ; a stray ) in a comment\n" +
		`(defn b [] [] Bool (== "a)((" "a)(("))` + "\n(data C [] (MkC))"
	forms := splitTopLevelForms(src)
	if len(forms) != 3 {
		t.Fatalf("want 3 forms, got %d: %#v", len(forms), forms)
	}
	if !strings.HasPrefix(forms[0], "(defn a") || !strings.HasPrefix(forms[2], "(data C") {
		t.Errorf("forms not split at top-level boundaries: %#v", forms)
	}
}

func TestTopoOrderPublishesDependenciesFirst(t *testing.T) {
	// Declared in REVERSE dependency order: g (which calls f) before f. The order
	// must be corrected to f, then g.
	forms := []string{
		`(defn team/g [] [(x Int)] Int (team/f x))`,
		`(defn team/f [] [(x Int)] Int x)`,
	}
	batch := map[string]bool{"team/f": true, "team/g": true}
	ordered, err := topoOrderForms(forms, batch, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].name != "team/f" || ordered[1].name != "team/g" {
		t.Errorf("dependency order wrong: %s before %s", ordered[0].name, ordered[1].name)
	}
}

// A batch name reused as ONE definition's binder, with no mutual reference, still
// orders — the false edge (the binder read as a reference) only over-constrains,
// it does not invent a cycle. This is the realistic bare-batch collision (friction
// item 5): `bar` takes a parameter named `foo`, and `foo` is an independent
// definition. `put` accepts it; the bare batch path must too.
func TestTopoOrderToleratesOneSidedBinderCollision(t *testing.T) {
	// Bare batch names, as the bare path uses. `bar` takes a parameter named `foo`
	// and its body mentions `foo`; `foo` is an independent batch member. walkSyms
	// reads that `foo` as a reference (a false edge), which only over-constrains —
	// foo has no deps, so it orders first and both appear. `put` accepts this.
	forms := []string{
		`(defn bar [] [(foo Int)] Int foo)`,
		`(defn foo [] [] Int 1)`,
	}
	batch := map[string]bool{"foo": true, "bar": true}
	ordered, err := topoOrderForms(forms, batch, nil, nil)
	if err != nil {
		t.Fatalf("a one-sided binder collision must not be read as a cycle: %v", err)
	}
	if len(ordered) != 2 || ordered[0].name != "foo" {
		t.Errorf("expected foo ordered first, got %v", []string{ordered[0].name, ordered[1].name})
	}

	// Genuinely mutual references — real mutual recursion, or an adversarial pair of
	// parameters each named after the other — are a cycle, and correctly so: neither
	// can be published as a separate one-name envelope before the other exists.
	mutual := []string{
		`(defn a [] [(b Int)] Int b)`,
		`(defn b [] [(a Int)] Int a)`,
	}
	if _, err := topoOrderForms(mutual, map[string]bool{"a": true, "b": true}, nil, nil); err == nil {
		t.Error("a mutual reference must be reported as a cycle — it has no separate-envelope order")
	}
}

// A definition that uses a batch datatype ONLY through its (bare, unqualified)
// constructor still depends on the datatype, so the datatype must be ordered
// first even though the function never names it. #185 review, P2.
func TestConstructorUseOrdersAfterItsDatatype(t *testing.T) {
	forms := []string{
		`(defn team/h [] [] Int (match (MkC) ((MkC) 1)))`,
		`(data team/C [] (MkC))`,
	}
	batch := map[string]bool{"team/h": true, "team/C": true}
	ctorOwner := map[string]string{"MkC": "team/C"}
	ordered, err := topoOrderForms(forms, batch, ctorOwner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].name != "team/C" {
		t.Errorf("datatype must be published before the function that constructs it; got %s first", ordered[0].name)
	}
}

// collectDeclared must find every non-reference symbol, because the caller refuses
// a batch name that collides with one — a silent rewrite of a binder or a property
// name would corrupt the published definition. #185 review, P1(c).
// collectDeclared must reach non-reference symbols in EVERY syntactic position:
// binders, record-field labels in nested type positions, and binders inside a
// match scrutinee. A missed one lets qualifyNames rewrite a local name, corrupting
// naming metadata or inventing a false dependency cycle. #185 review.
func TestCollectDeclaredReachesEveryPosition(t *testing.T) {
	keys := func(m map[string]bool) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		return out
	}
	f, err := parseForms(`(defn d [a] [(p (List a)) (r {label Int})] Int
		(let (q Int (+ p 1)) (match (let (b Int 1) b) ((Nil) 0) ((Cons hd tl) hd)))
		(prop nonneg [(z Int)] (== z z)))`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	collectDeclared(f[0], got)
	for _, want := range []string{"a", "p", "r", "label", "q", "b", "hd", "tl", "z", "nonneg"} {
		if !got[want] {
			t.Errorf("non-reference symbol %q not collected: %v", want, keys(got))
		}
	}
	// References — a type name and the definition's own name — must NOT be swept in.
	if got["List"] || got["d"] {
		t.Errorf("a reference was misclassified as a binder: %v", keys(got))
	}
}

// A binder introduced inside a NESTED constructor pattern is still a binder, so
// collectDeclared must recurse into it. Missing one lets qualifyNames rewrite that
// local as a published reference — inventing a false dependency edge, or rejecting a
// valid batch as cyclic when the binder name matches a top-level definition's.
func TestCollectDeclaredReachesNestedPatternBinders(t *testing.T) {
	f, err := parseForms(`(defn d [a] [(rs (List (Run a)))] Int
		(match rs ((Nil) 0) ((Cons (MkRun n x) t) n)))`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	collectDeclared(f[0], got)
	for _, want := range []string{"n", "x", "t"} {
		if !got[want] {
			t.Errorf("nested-pattern binder %q not collected", want)
		}
	}
	// The nested constructor head is a reference, not a binder.
	if got["MkRun"] {
		t.Error("nested constructor head MkRun was misclassified as a binder")
	}
}

// The transform must preserve identity: qualifying a name may only remap
// references. For a valid closure, each definition's bare and qualified forms are
// the SAME object (same hash) — only the name differs. This backstops the collision
// check for anything that changes STRUCTURE, e.g. a reserved word. #185.
func TestQualificationPreservesIdentity(t *testing.T) {
	st := newStore(t)
	src := "(defn g [] [(x Int)] Int (f (f x)))\n(defn f [] [(x Int)] Int (+ x 1))"
	rename := map[string]string{"f": "team/f", "g": "team/g"}

	if err := putBatch(st, []string{"(defn f [] [(x Int)] Int (+ x 1))", "(defn g [] [(x Int)] Int (f (f x)))"}, "tester"); err != nil {
		t.Fatalf("bare closure should elaborate: %v", err)
	}
	q := qualifyNames(src, rename)
	qforms := splitTopLevelForms(q)
	// qforms is source order (g, f); publish/elaborate deps first.
	if err := putBatch(st, []string{qforms[1], qforms[0]}, "tester"); err != nil {
		t.Fatalf("valid closure failed to elaborate once qualified: %v", err)
	}
	for bare, qual := range rename {
		hb, okb := st.Resolve(bare)
		hq, okq := st.Resolve(qual)
		if !okb || !okq || hb != hq {
			t.Errorf("%s and %s must be one object; got %s vs %s", bare, qual, shortHash(hb), shortHash(hq))
		}
	}
}

// A batch name that collides with a reserved word breaks once qualified — the
// `let` special form in the body becomes a call to the (0-arg) function, which
// cannot elaborate. The identity check catches this generically, where a
// position-by-position collision scan did not. #185 review.
func TestQualificationRejectsReservedWordCollision(t *testing.T) {
	st := newStore(t)
	src := `(defn let [] [] Int (let (x Int 1) x))`
	if err := putBatch(st, []string{src}, "tester"); err != nil {
		t.Fatalf("the bare closure should elaborate: %v", err)
	}
	q := qualifyNames(src, map[string]string{"let": "team/let"})
	err := putBatch(st, []string{q}, "tester")
	if err == nil {
		// If it somehow elaborated, its identity must differ from the bare form.
		hb, _ := st.Resolve("let")
		hq, _ := st.Resolve("team/let")
		if hb == hq {
			t.Errorf("qualifying a function named `let` produced an identical object; the collision went undetected")
		}
	}
}

func TestTopoOrderRefusesACycle(t *testing.T) {
	forms := []string{
		`(defn team/a [] [] Int (team/b))`,
		`(defn team/b [] [] Int (team/a))`,
	}
	batch := map[string]bool{"team/a": true, "team/b": true}
	if _, err := topoOrderForms(forms, batch, nil, nil); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("a dependency cycle should be reported, got: %v", err)
	}
}

// The end-to-end witness: a closure whose cross-reference is bare in the author's
// source is published one definition at a time, under a namespace, and the SERVER
// ACCEPTS it — because the transform qualified the declared names and the
// reference, and the definitions go up in dependency order so the dependent
// resolves its qualified dep. This is the whole fix, run against the real
// publication gate (no network: apiPutSigned is the server path).
func TestNamespacedClosureIsAcceptedByTheServer(t *testing.T) {
	st := newMemStoreForTest(t)
	kHex, k := newKey(t)
	resOct, resSig := signRes(t, k, "team/*", noAuthority, 0)
	if _, err := apiReserve(st, resOct, resSig, kHex); err != nil {
		t.Fatalf("reserve team/*: %v", err)
	}

	// As an author writes it: bare names, a cross-reference (g calls f), declared
	// dependent-first to prove the ordering is derived and not assumed.
	src := "(defn g [] [(x Int)] Int (f (f x)))\n(defn f [] [(x Int)] Int (+ x 1))"
	rename := map[string]string{"f": "team/f", "g": "team/g"}

	qsrc := qualifyNames(src, rename)
	forms := splitTopLevelForms(qsrc)
	ordered, err := topoOrderForms(forms, map[string]bool{"team/f": true, "team/g": true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].name != "team/f" {
		t.Fatalf("f must publish before g; got %s first", ordered[0].name)
	}

	for _, qf := range ordered {
		parsed, err := parseForms(qf.text)
		if err != nil {
			t.Fatal(err)
		}
		name, err := declaredName(parsed[0])
		if err != nil {
			t.Fatal(err)
		}
		h := artifactHashOf(t, st, qf.text) // resolves team/f for g, since f is bound first
		env := pubEnvelope{Op: "put", Name: name, Artifact: h,
			Parent: noParent, ParentRev: firstRev(), Author: kHex, License: "Apache-2.0"}
		sig, err := envelopeSign(k, env)
		if err != nil {
			t.Fatal(err)
		}
		reps, err := apiPutSigned(st, qf.text, kHex, "",
			&pubAuth{Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: kHex})
		if err != nil || len(reps) == 0 || reps[0].Status != "accepted" {
			t.Fatalf("%s rejected under its own namespace: err=%v rep=%+v", name, err, reps)
		}
	}

	if _, ok := st.Resolve("team/g"); !ok {
		t.Fatalf("team/g did not bind")
	}
	if _, ok := st.Resolve("team/f"); !ok {
		t.Fatalf("team/f did not bind")
	}
}

// The control that makes the test above mean something: the OLD --namespace
// behaviour — a prefixed envelope name over a BARE-named source — is REJECTED by
// the same gate, with the name-mismatch this fix removes. Without this, the test
// above could pass for a reason unrelated to the qualification.
func TestBareSourceWithPrefixedEnvelopeIsRejected(t *testing.T) {
	st := newMemStoreForTest(t)
	kHex, k := newKey(t)
	resOct, resSig := signRes(t, k, "team/*", noAuthority, 0)
	if _, err := apiReserve(st, resOct, resSig, kHex); err != nil {
		t.Fatalf("reserve team/*: %v", err)
	}

	src := `(defn f [] [(x Int)] Int (+ x 1))`
	h := artifactHashOf(t, st, src) // bare "f" hashes fine
	env := pubEnvelope{Op: "put", Name: "team/f", Artifact: h,
		Parent: noParent, ParentRev: firstRev(), Author: kHex, License: "Apache-2.0"}
	sig, err := envelopeSign(k, env)
	if err != nil {
		t.Fatal(err)
	}
	reps, _ := apiPutSigned(st, src, kHex, "",
		&pubAuth{Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: kHex})
	if len(reps) == 0 || reps[0].Status == "accepted" {
		t.Fatalf("a prefixed envelope over bare source was accepted; the name mismatch went unchecked: %+v", reps)
	}
	if !strings.Contains(reps[0].Error, "does not match the requested transition") {
		t.Errorf("rejection is not the name mismatch #185 addresses: %q", reps[0].Error)
	}
}
