package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// THE LOAD-BEARING INVARIANT OF THE WHOLE DISCOVERY LAYER: it never touches
// identity (docs/discovery.md, docs/egraph.md). A definition's identity is the
// SHA-256 of the O1 encoding of its ACTUAL AST (SPEC §1); `eHash` is a separate
// key computed over a REWRITTEN form, and `find --equiv` draws an edge between
// existing objects rather than merging them.
//
// UNTIL NOW THAT WAS AN ARGUMENT, NOT A REGRESSION. Every other test in this
// repository asks what discovery REPORTS; none asks what discovery LEAVES
// BEHIND, and the two are different questions with different failure modes.
//
// WHY THE HAZARD IS REAL AND NOT HYPOTHETICAL, which is what makes this worth a
// gate rather than a comment:
//
//   - `Store.GetDef` CACHES: every call for one hash returns the SAME `*Def`
//     pointer. A mutation made while hashing is therefore not confined to the
//     caller — it is published to every later consumer in the process.
//   - `eNormalize` calls `chk.synth` on the ORIGINAL subterm, and synth writes
//     inferred type arguments INTO the term it is given. So the normalizer has
//     a live write path into the very structure whose bytes are the identity.
//
// So the claim under test is not "the code looks read-only". It is: after
// hashing and querying, the encoding, the identity, every property's content
// address, and the store's name resolution are byte-identical — in the
// in-memory definitions that were handed to discovery AND on disk.
//
// THE UNIVERSE IS THE COMMITTED CORPUS PLUS A CONSTRUCTED STORE, because they
// fail differently: the corpus is what `find --equiv` actually runs over, and
// the constructed store carries the polymorphic and arithmetic shapes the
// corpus may or may not contain — a survey of what is on disk cannot establish
// a claim about shapes nobody has committed.

// idSnapshot is everything about one definition that must not move.
type idSnapshot struct {
	enc      []byte   // the canonical bytes — the identity's preimage
	id       string   // hashDef
	props    []string // propHash per property, in order
	propsGen []string // propHashGeneral per property, in order
}

func takeIdSnapshot(d *Def) idSnapshot {
	s := idSnapshot{enc: append([]byte(nil), encodeDef(d)...), id: hashDef(d)}
	for i := range d.Props {
		s.props = append(s.props, propHash(&d.Props[i]))
		s.propsGen = append(s.propsGen, propHashGeneral(&d.Props[i]))
	}
	return s
}

func (a idSnapshot) diff(b idSnapshot) string {
	if !bytes.Equal(a.enc, b.enc) {
		return fmt.Sprintf("canonical ENCODING moved (%d bytes → %d bytes)", len(a.enc), len(b.enc))
	}
	if a.id != b.id {
		return "IDENTITY moved: " + a.id + " → " + b.id
	}
	if len(a.props) != len(b.props) {
		return "the number of PROPERTIES changed"
	}
	for i := range a.props {
		if a.props[i] != b.props[i] {
			return fmt.Sprintf("propHash of property %d moved: %s → %s", i, a.props[i], b.props[i])
		}
		if a.propsGen[i] != b.propsGen[i] {
			return fmt.Sprintf("propHashGeneral of property %d moved: %s → %s", i, a.propsGen[i], b.propsGen[i])
		}
	}
	return ""
}

// exerciseDiscovery runs the discovery layer hard enough that anything it
// writes has happened: every definition hashed REPEATEDLY, and the full
// `find --equiv` query, which is the command that hashes the entire store per
// call.
//
// IT RE-CHECKS AFTER EVERY CALL, NOT ONLY AT THE END, and that is not
// belt-and-braces — it is what makes the measurement discriminate. An
// end-to-end comparison sees only the two endpoints, so a write that UNDOES
// ITSELF over an even number of calls is invisible to it. Measured, not
// imagined: a mutant that swapped a commutative primitive's operands on every
// eHash passed the endpoint form of this test, because the exercise happened to
// call eHash an even number of times per definition. A per-call check kills it
// on the first call.
func exerciseDiscovery(t *testing.T, st *Store, before map[string]idSnapshot, queryNames []string) {
	t.Helper()
	names := st.Names()
	hashes := make([]string, 0, len(names))
	for _, h := range names {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	verifyAll := func(after string) {
		for _, h := range hashes {
			d, err := st.GetDef(h)
			if err != nil {
				t.Fatalf("%s: %v", shortHash(h), err)
			}
			if msg := before[h].diff(takeIdSnapshot(d)); msg != "" {
				t.Fatalf("#%s: identity moved during %s — %s", shortHash(h), after, msg)
			}
		}
	}
	for round := 0; round < 3; round++ {
		for _, h := range hashes {
			d, err := st.GetDef(h)
			if err != nil || d.K != "func" {
				continue
			}
			_ = eHash(st, d)
			if msg := before[h].diff(takeIdSnapshot(d)); msg != "" {
				t.Fatalf("#%s: eHash moved the definition it was given — %s", shortHash(h), msg)
			}
		}
		for _, n := range queryNames {
			if _, err := apiFindEquiv(st, n); err != nil {
				t.Fatalf("find --equiv %s: %v", n, err)
			}
			verifyAll("find --equiv " + n)
		}
	}
}

func TestDiscoveryNeverTouchesIdentityOnTheCorpus(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skip("no committed store")
	}
	namesBefore := st.Names()
	before := map[string]idSnapshot{}
	var funcNames []string
	for n, h := range namesBefore {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		// CONTROL ON THE STORE ITSELF: an object's name IS its hash, so a
		// snapshot that did not already agree with the store would make every
		// comparison below meaningless.
		if got := hashDef(d); got != h {
			t.Fatalf("%s: stored under %s but hashes to %s before anything ran", n, h, got)
		}
		before[h] = takeIdSnapshot(d)
		if d.K == "func" {
			funcNames = append(funcNames, n)
		}
	}
	if len(before) == 0 {
		t.Fatal("the store is empty — this test measured nothing")
	}
	sort.Strings(funcNames)
	queries := funcNames
	if len(queries) > 5 {
		queries = queries[:5] // apiFindEquiv is O(store) per call
	}

	exerciseDiscovery(t, st, before, queries)

	// (1) THE IN-MEMORY DEFINITIONS discovery was handed. GetDef caches, so
	// these are the same pointers eHash normalized — the only place a
	// synth-time write would be observable.
	for n, h := range namesBefore {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if msg := before[h].diff(takeIdSnapshot(d)); msg != "" {
			t.Fatalf("%s (#%s): discovery moved identity — %s", n, shortHash(h), msg)
		}
	}

	// (2) NAME RESOLUTION. `find --equiv` walks every name; a query must not
	// bind, rebind or drop one.
	namesAfter := st.Names()
	if len(namesAfter) != len(namesBefore) {
		t.Fatalf("the store held %d names before discovery and %d after", len(namesBefore), len(namesAfter))
	}
	for n, h := range namesBefore {
		got, ok := st.Resolve(n)
		if !ok {
			t.Fatalf("%s no longer resolves after discovery", n)
		}
		if got != h {
			t.Fatalf("%s resolved to %s before discovery and %s after", n, shortHash(h), shortHash(got))
		}
	}

	// (3) THE BYTES ON DISK, read through a store opened after the fact. The
	// checks above share one process and one cache with the code under test;
	// this one does not, so it is the only one that can see a write that
	// reached the filesystem.
	fresh, err := OpenStore("../codebase")
	if err != nil {
		t.Fatal(err)
	}
	freshNames := fresh.Names()
	if len(freshNames) != len(namesBefore) {
		t.Fatalf("a freshly opened store holds %d names, not %d", len(freshNames), len(namesBefore))
	}
	for n, h := range namesBefore {
		fh, ok := freshNames[n]
		if !ok || fh != h {
			t.Fatalf("%s resolves differently in a freshly opened store", n)
		}
		fd, err := fresh.GetDef(fh)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if msg := before[h].diff(takeIdSnapshot(fd)); msg != "" {
			t.Fatalf("%s (#%s): the stored object changed on disk — %s", n, shortHash(h), msg)
		}
	}
}

// The corpus cannot witness what it does not contain. This store is built to
// hold exactly the shapes that make the hazard reachable:
//
//   - definitions the e-graph REWRITES (a factored and an expanded product),
//     so saturation and extraction run rather than being skipped;
//   - a POLYMORPHIC call, because synth's write path is the publication of
//     inferred type arguments and a monomorphic body never exercises it;
//   - definitions carrying PROPERTIES, so the property content addresses have
//     something to move.
func TestDiscoveryNeverTouchesIdentityOnRewrittenDefinitions(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn idf [t] [(v t)] t v
  (prop id-is-identity [(x Int)] (== (idf [Int] x) x)))`)
	put(t, st, `(defn poly-call [] [(x Int) (y Int) (z Int)] Int (idf (* x (+ y z)))
  (prop matches-product [(x Int) (y Int) (z Int)] (== (poly-call x y z) (* x (+ y z)))))`)
	put(t, st, `(defn di-factored [] [(a Int) (b Int) (c Int)] Int (* a (+ b c))
  (prop is-a-product [(a Int) (b Int) (c Int)] (== (di-factored a b c) (* a (+ b c)))))`)
	put(t, st, `(defn di-expanded [] [(a Int) (b Int) (c Int)] Int (+ (* a b) (* a c))
  (prop is-a-sum [(a Int) (b Int) (c Int)] (== (di-expanded a b c) (+ (* a b) (* a c)))))`)

	names := st.Names()
	before := map[string]idSnapshot{}
	for n, h := range names {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		before[h] = takeIdSnapshot(d)
	}

	// CONTROL ON THE EXERCISE: the query must actually find the rewrite, or
	// this test is a no-op dressed as a regression — the e-graph would never
	// have run and nothing could have been mutated by it.
	out, err := apiFindEquiv(st, "di-factored")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "di-expanded") {
		t.Fatalf("the e-graph did not connect the two products, so this test exercises nothing:\n%s", out)
	}

	exerciseDiscovery(t, st, before, []string{"di-factored", "di-expanded", "poly-call", "idf"})

	for n, h := range names {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if msg := before[h].diff(takeIdSnapshot(d)); msg != "" {
			t.Fatalf("%s (#%s): discovery moved identity — %s", n, shortHash(h), msg)
		}
		if got, ok := st.Resolve(n); !ok || got != h {
			t.Fatalf("%s no longer resolves to its own object after discovery", n)
		}
	}

	// AND THE EDGE IS STILL AN EDGE: equivalent bodies keep DISTINCT
	// identities. Without this row the test above would pass on an
	// implementation that merged the two objects outright, which is the one
	// outcome the invariant exists to forbid.
	f, e := mustDef(t, st, "di-factored"), mustDef(t, st, "di-expanded")
	if hashDef(f) == hashDef(e) {
		t.Fatal("equivalent definitions must keep distinct identities")
	}
	if eHash(st, f) != eHash(st, e) {
		t.Fatal("the two definitions stopped being equivalent")
	}
}

// THE WRITE PATH ITSELF, aimed at directly rather than hoped to be absent.
//
// The two tests above run discovery over definitions that came out of `put` and
// out of the committed store — and every one of those has ALREADY been through
// the checker, so its inferred type arguments are already populated and there is
// nothing left for a synth to publish. That is a fact about how those
// definitions were made, not about discovery, and a regression that depends on
// it is measuring the wrong thing.
//
// This constructs the one shape where the hazard is live: a `ref` to a
// POLYMORPHIC definition with its type arguments OMITTED, sitting where
// eNormalize will synthesize it (an operand of a commutative primitive). The
// checker's finishInferApp writes the solved arguments into that head. If eHash
// normalized the stored body, that write would land on a definition whose bytes
// are its identity — and `Store.GetDef` caches, so it would be published to
// every later consumer in the process.
//
// THE CONTROL IS NOT OPTIONAL: the test first proves the checker really does
// fill this shape in. Without that, an eHash that stopped synthesizing
// altogether — or a shape the checker happens to ignore — would pass this as
// comfortably as a correct implementation.
func TestDiscoveryCannotMutateAnUnsynthesizedPolymorphicReference(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn idf [t] [(v t)] t v)`)
	idfHash, ok := st.Resolve("idf")
	if !ok {
		t.Fatal("idf did not resolve")
	}

	// fn (x Int) -> (+ (idf x) x), with idf's type argument DELIBERATELY absent.
	mkBody := func() *Term {
		call := Term{K: "app",
			A: &Term{K: "ref", Hash: idfHash},
			B: &Term{K: "var", Idx: 0}}
		sum := Term{K: "prim", Op: "+", Args: []Term{call, {K: "var", Idx: 0}}}
		return &Term{K: "lam", Ty: tInt(), A: &sum}
	}
	refIn := func(t *Term) *Term { // the ref node inside a body built above
		return t.A.Args[0].A
	}
	if r := refIn(mkBody()); r.K != "ref" || len(r.TyArgs) != 0 {
		t.Fatalf("setup: expected a ref with no type arguments, got %q with %d", r.K, len(r.TyArgs))
	}

	d := &Def{K: "func", Ty: tFun(tInt(), tInt()), Body: mkBody()}

	// CONTROL: the checker DOES publish into this shape, so there is a write to
	// be prevented.
	probe := mkBody()
	chk := &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
	if _, err := chk.synth(nil, probe); err != nil {
		t.Fatalf("setup: the probe body does not typecheck (%v) — synth would never reach the ref", err)
	}
	if n := len(refIn(probe).TyArgs); n == 0 {
		t.Fatal("setup: synth published no type arguments into the ref, so this shape carries no " +
			"write for eHash to avoid and the assertions below would pass vacuously")
	}

	// THE CLAIM.
	before := takeIdSnapshot(d)
	for round := 0; round < 3; round++ {
		_ = eHash(st, d)
		if msg := before.diff(takeIdSnapshot(d)); msg != "" {
			t.Fatalf("round %d: eHash moved the definition it was given — %s", round, msg)
		}
		if n := len(refIn(d.Body).TyArgs); n != 0 {
			t.Fatalf("round %d: eHash published %d inferred type argument(s) into the stored body — "+
				"discovery wrote into the structure whose bytes are its identity", round, n)
		}
	}

	// AND THE HASH IS STILL COMPUTED, not skipped: a body the normalizer refused
	// would also never mutate anything.
	if h := eHash(st, d); h == "" {
		t.Fatal("eHash returned nothing for a well-typed body")
	}
}
