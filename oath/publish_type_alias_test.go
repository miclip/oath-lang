package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// `oath publish` accepts (type ...) aliases. An alias is identity-transparent surface
// sugar with NO published identity: it takes no rename entry, no duplicate-name slot,
// no topology node, no plan and no envelope. What it does carry is a DEPENDENCY, and
// that is the whole difficulty — a definition writing `(p Env)` names nothing the
// dependency scan can see, even when Env's body names a datatype the definition cannot
// elaborate without.

const (
	// Payload is reachable ONLY through the alias body: `cost` projects an Int.
	pubAliasExtDeps = `(data Payload [] (Pay Int))`
	pubAliasSrc     = "(type Env {decode (-> Payload Int) size Int})\n" +
		`(defn cost [] [(e Env)] Int (. e size))`
	pubInlineSrc = `(defn cost [] [(e {decode (-> Payload Int) size Int})] Int (. e size))`
)

// headStub is a registry that answers `head` with an empty record — no parent, first
// revision — which is all buildPublishPlan needs to construct an envelope.
//
// The listen probe is not ceremony. httptest.NewServer PANICS when it cannot bind, and a
// panic in one test takes the whole package down — so in a sandbox without loopback
// networking these tests would destroy the run of every other test rather than reporting
// their own unavailability. Observed doing exactly that in an external reviewer's
// sandbox. Skipping is the honest report: the claim is untested here, not false.
func headStub(t *testing.T) string {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a loopback port here, so the plan builder's registry stub is unavailable: %v", err)
	}
	probe.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonRPC(w, "{}", false)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The defining property, at the publication layer: the alias spelling and the inline
// spelling produce the SAME ENVELOPE — same artifact, same name, same bytes. Anything
// weaker (equal hashes but a different envelope) would still be a different signed
// statement.
func TestPublishAliasProducesTheSameEnvelopeAsInline(t *testing.T) {
	st := newStore(t)
	put(t, st, pubAliasExtDeps)
	endpoint := headStub(t)
	kHex, _ := newKey(t)

	inline, _, _, err := buildPublishPlan(st, endpoint, kHex, pubInlineSrc, "Apache-2.0", "")
	if err != nil {
		t.Fatalf("inline: %v", err)
	}
	aliased, _, _, err := buildPublishPlan(st, endpoint, kHex, pubAliasSrc, "Apache-2.0", "")
	if err != nil {
		t.Fatalf("publish must accept a (type ...) alias: %v", err)
	}
	if aliased.Name != "cost" {
		t.Errorf("the alias declares no published name; the plan must be for cost, got %q", aliased.Name)
	}
	if inline.Artifact != aliased.Artifact {
		t.Errorf("alias must be identity-transparent:\n inline %s\n alias  %s", inline.Artifact, aliased.Artifact)
	}
	if inline.Bytes != aliased.Bytes {
		t.Errorf("the two spellings must produce the same signed statement:\n inline %q\n alias  %q", inline.Bytes, aliased.Bytes)
	}
	// An alias is not a definition, so a single definition written against one stays
	// on the single-envelope path rather than being routed through the batch.
	if multiDefinition(pubAliasSrc) {
		t.Error("one definition plus an alias is still ONE definition; it must not route to the batch path")
	}
}

// Two definitions and one alias: the alias must not become a third plan, a third
// envelope, or a duplicate name.
func TestPublishBatchExcludesAliasesFromPlans(t *testing.T) {
	st := newStore(t)
	put(t, st, pubAliasExtDeps)
	src := "(type Env {decode (-> Payload Int) size Int})\n" +
		"(defn cost [] [(e Env)] Int (. e size))\n" +
		"(defn twice [] [(e Env)] Int (+ (cost e) (cost e)))"
	batch, err := buildPublishBatch(st, src, "team/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.ordered) != 2 || len(batch.planText) != 2 {
		t.Fatalf("2 definitions and 1 alias must yield 2 plans, got %d ordered / %d texts",
			len(batch.ordered), len(batch.planText))
	}
	if _, ok := batch.rename["Env"]; ok {
		t.Errorf("the alias must take no rename entry (it would be published as a name): %v", batch.rename)
	}
	if batch.ordered[0].name != "team/cost" {
		t.Errorf("cost must precede its caller twice; got %s first", batch.ordered[0].name)
	}
	// Each plan carries the alias it needs, so each is independently elaborable.
	for i, text := range batch.planText {
		if !strings.Contains(text, "(type Env") {
			t.Errorf("plan %d does not carry the alias it needs:\n%s", i, text)
		}
	}
	// ...but the whole-batch layout registers it ONCE — registerTypeAlias refuses a
	// duplicate within a batch, so a concatenation of the plans would not elaborate.
	if n := strings.Count(strings.Join(batch.batchQual, "\n"), "(type Env"); n != 1 {
		t.Errorf("the batch layout must register the alias exactly once, got %d", n)
	}
}

// A file of nothing but aliases publishes nothing, and says so.
func TestPublishRefusesAnAliasOnlyFile(t *testing.T) {
	st := newStore(t)
	put(t, st, pubAliasExtDeps)
	_, err := buildPublishBatch(st, `(type Env {size Int})`, "")
	if err == nil || !strings.Contains(err.Error(), "only (type ...) aliases") {
		t.Fatalf("a file with no definitions must be refused with a reason; got %v", err)
	}
}

// NAMESPACE QUALIFICATION reaches inside an alias body: a same-batch type named there
// must be rewritten with the rest, or the published alias would resolve to whatever
// the bare name means on the registry.
func TestPublishQualifiesInsideAliasBodies(t *testing.T) {
	st := newStore(t)
	src := "(data Point [] (Pt Int))\n" +
		"(type P Point)\n" +
		"(defn cost [] [(p P) (n Int)] Int n)"
	batch, err := buildPublishBatch(st, src, "team/*")
	if err != nil {
		t.Fatal(err)
	}
	costPlan := ""
	for i, qf := range batch.ordered {
		if qf.name == "team/cost" {
			costPlan = batch.planText[i]
		}
	}
	if costPlan == "" {
		t.Fatal("no plan for team/cost")
	}
	if !strings.Contains(costPlan, "(type P team/Point)") {
		t.Errorf("the alias body's batch reference must be qualified with the rest:\n%s", costPlan)
	}
	// The alias's own NAME is batch-local and must stay unqualified — qualifying it
	// would imply a published name that does not exist. (Checked as the declaration,
	// not as a substring: "team/P" is also a prefix of "team/Point".)
	if !strings.Contains(costPlan, "(type P ") || strings.Contains(costPlan, "(type team/P ") {
		t.Errorf("the alias's own name must not be qualified; it publishes nothing:\n%s", costPlan)
	}
	if batch.ordered[0].name != "team/Point" {
		t.Errorf("the datatype the alias expands to must publish first; got %s", batch.ordered[0].name)
	}
}

// ORDERING: the dependency runs only through the alias, and the source is written in
// the order publish exists to fix — the user first, its datatype after. Without the
// alias's dependency being propagated, `cost` sorts first and is published against a
// datatype the registry does not yet hold.
func TestPublishOrdersByAliasOnlyDependency(t *testing.T) {
	st := newStore(t)
	// Three things make this discriminating, and it took a surviving mutant to get all
	// three right:
	//
	//  1. cost mentions Point NOWHERE — not as a type, not through a constructor. The
	//     parameter is typed with the alias and never used, so the alias's recorded
	//     dependency is the only thing that can order them. (A constructor in the body
	//     supplies the edge by itself, and the test would pass either way.)
	//  2. Point is DELAYED by a forward reference of its own (to Inner, declared last),
	//     which publication legitimately reorders. Without that, Point precedes cost by
	//     declaration order alone and Kahn's index tie-break already gets it right.
	//  3. cost is otherwise dependency-free, so it is READY immediately and overtakes
	//     the delayed Point unless the alias edge holds it back.
	src := "(data Point [] (Pt Inner))\n" +
		"(type P Point)\n" +
		"(defn cost [] [(p P) (n Int)] Int n)\n" +
		"(data Inner [] (MkI Int))"
	batch, err := buildPublishBatch(st, src, "team/*")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, qf := range batch.ordered {
		got = append(got, qf.name)
	}
	if len(got) != 3 || got[2] != "team/cost" {
		t.Fatalf("cost reaches Point only through the alias P and must follow it; got %v", got)
	}
	if got[0] != "team/Inner" || got[1] != "team/Point" {
		t.Fatalf("Point must follow its own forward dependency Inner; got %v", got)
	}
	// And the whole-batch layout must elaborate, which it can only do if the alias is
	// emitted after the datatype it expands to.
	if err := putBatch(newStore(t), batch.batchBare, "tester"); err != nil {
		t.Errorf("the bare batch layout must elaborate in the order it lays out: %v", err)
	}
}

// SEEDING: an alias-only EXTERNAL dependency is pinned by the hash it resolved to, and
// the seed store it produces elaborates the batch.
//
// What this witnesses precisely: the RECORD (drop it and the pin is empty) and the
// end state (the seed binds it and the qualified batch elaborates). It does NOT
// witness the by-hash seeding loop as the sole cause — seedResolutionStore's datatype
// sweep binds the same object independently, and the comment there says so rather than
// claiming a protection this cannot show.
func TestPublishSeedsAliasOnlyExternalDependency(t *testing.T) {
	st := newStore(t)
	put(t, st, pubAliasExtDeps)
	payload, _ := st.Resolve("Payload")

	batch, err := buildPublishBatch(st, pubAliasSrc, "team/*")
	if err != nil {
		t.Fatal(err)
	}
	if batch.aliasExt["Payload"] != payload {
		t.Errorf("Payload is referenced only by the alias body and must be pinned to %s; got %v",
			payload, batch.aliasExt)
	}
	seed := newStore(t)
	if err := seedResolutionStore(st, seed, batch.rename, batch.forms, batch.aliasExt); err != nil {
		t.Fatal(err)
	}
	if h, ok := seed.Resolve("Payload"); !ok || h != payload {
		t.Fatalf("the seed store must bind the alias's dependency (%v, %s)", ok, h)
	}
	if err := putBatch(seed, batch.batchQual, "tester"); err != nil {
		t.Errorf("the seeded store must elaborate the qualified batch: %v", err)
	}
}

// HASH-SAFE SHADOWING. `put` reads this source unambiguously: the alias binds the
// AMBIENT Point, and the batch's own Point binds afterwards. Publication cannot
// reproduce that — token qualification rewrites the name inside the alias body, and
// dependency ordering may bind the batch's Point first — so it is REFUSED rather than
// silently retargeted at a different type.
func TestPublishRefusesAliasShadowedByALaterDeclaration(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Point [] (Amb Int))`) // the AMBIENT Point the alias resolves
	src := "(type P Point)\n" +
		"(data Point [] (Pt Int))\n" +
		"(defn cost [] [(p P)] Int 0)"
	_, err := buildPublishBatch(st, src, "team/*")
	if err == nil || !strings.Contains(err.Error(), "retarget") {
		t.Fatalf("an alias resolving outside the file, whose name a later form redeclares, must be refused; got %v", err)
	}

	// CONTROL 1: with the declaration moved ABOVE the alias there is no ambiguity —
	// the alias means the batch's Point, and that is what publication carries.
	fixed := "(data Point [] (Pt Int))\n" +
		"(type P Point)\n" +
		"(defn cost [] [(p P) (n Int)] Int n)"
	batch, ferr := buildPublishBatch(st, fixed, "team/*")
	if ferr != nil {
		t.Fatalf("the unambiguous order must be accepted: %v", ferr)
	}
	if batch.ordered[0].name != "team/Point" {
		t.Errorf("the batch datatype must still be ordered first; got %s", batch.ordered[0].name)
	}

	// CONTROL 2: with NO ambient Point there is nothing to shadow, so this refusal must
	// not fire — the source is simply mis-ordered, and elaboration says so in its own
	// words rather than this one's.
	clean := newStore(t)
	if _, cerr := buildPublishBatch(clean, src, "team/*"); cerr != nil && strings.Contains(cerr.Error(), "retarget") {
		t.Errorf("with no external Point there is no shadowing to refuse: %v", cerr)
	}
}

// THE SERVER-SIDE WITNESS, and the point of requirement "one published definition":
// the EXACT bytes each plan sends re-elaborate at the registry, under the same
// publication gate a real put goes through, and bind exactly one name. apiPutSigned IS
// the server path — no network, no stub.
func TestPublishedAliasSourceIsAcceptedByTheServer(t *testing.T) {
	st := newMemStoreForTest(t)
	kHex, k := newKey(t)
	resOct, resSig := signRes(t, k, "team/*", noAuthority, 0)
	if _, err := apiReserve(st, resOct, resSig, kHex); err != nil {
		t.Fatalf("reserve team/*: %v", err)
	}
	// The registry holds the alias's external dependency, as it would for any dep.
	if _, err := apiPut(st, pubAliasExtDeps, "setup", ""); err != nil {
		t.Fatal(err)
	}

	// Declared in the order publish must fix: the caller first, then the definition it
	// calls, with the alias last — nothing here is in dependency order.
	src := "(type Env {decode (-> Payload Int) size Int})\n" +
		"(defn twice [] [(e Env)] Int (+ (cost e) (cost e)))\n" +
		"(defn cost [] [(e Env)] Int (. e size))"
	batch, err := buildPublishBatch(st, src, "team/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.planText) != 2 {
		t.Fatalf("want 2 plans (the alias publishes nothing), got %d", len(batch.planText))
	}
	if batch.ordered[0].name != "team/cost" {
		t.Fatalf("cost must publish before twice; got %s first", batch.ordered[0].name)
	}

	for i, text := range batch.planText {
		name := batch.ordered[i].name
		def, meta, eerr := elabAliasPlan(t, st, text)
		if eerr != nil {
			t.Fatalf("%s: the exact published bytes must elaborate against the registry: %v", name, eerr)
		}
		if meta.Name != name {
			t.Fatalf("plan %d declares %q but is published as %q", i, meta.Name, name)
		}
		if err := checkDef(st, def); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		h := hashDef(def)
		env := pubEnvelope{Op: "put", Name: name, Artifact: h,
			Parent: noParent, ParentRev: firstRev(), Author: kHex, License: "Apache-2.0"}
		sig, serr := envelopeSign(k, env)
		if serr != nil {
			t.Fatal(serr)
		}
		reps, perr := apiPutSigned(st, text, kHex, "",
			&pubAuth{Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: kHex})
		if perr != nil {
			t.Fatalf("%s rejected: %v", name, perr)
		}
		// EXACTLY ONE published definition per envelope — the alias must contribute no
		// report, or a single signature would be covering two name transitions.
		if len(reps) != 1 {
			t.Fatalf("%s: the published source must yield exactly one definition, got %d: %+v", name, len(reps), reps)
		}
		if reps[0].Status != "accepted" || reps[0].Name != name {
			t.Fatalf("%s: %+v", name, reps[0])
		}
	}
	for _, n := range []string{"team/cost", "team/twice"} {
		if _, ok := st.Resolve(n); !ok {
			t.Errorf("%s did not bind", n)
		}
	}
}

// elabAliasPlan elaborates a plan's exact source the way the client does: aliases
// registered in order, then the single definition.
func elabAliasPlan(t *testing.T, st *Store, src string) (*Def, *Meta, error) {
	t.Helper()
	forms, err := parseForms(src)
	if err != nil {
		return nil, nil, err
	}
	aliases := map[string]*aliasDef{}
	var defs []sx
	for _, f := range forms {
		if isTypeAliasForm(f) {
			if err := registerTypeAlias(st, f, aliases); err != nil {
				return nil, nil, err
			}
			continue
		}
		defs = append(defs, f)
	}
	if len(defs) != 1 {
		return nil, nil, fmt.Errorf("want exactly one definition in a plan's source, got %d", len(defs))
	}
	return elabFormWith(st, defs[0], aliases)
}

// EVERY alias is validated, including one no definition uses — found by external
// review. The batch's validation puts are the ONLY place a closure publication
// elaborates its aliases, so an alias omitted from that layout is an alias never
// checked: the publication would drop it silently and publish the rest, while `put`
// refuses the same file.
//
// The control is `put` itself: each source below must be refused there too, or the
// assertion is measuring a rule publish invented rather than one it shares.
func TestPublishValidatesEveryAliasIncludingUnusedOnes(t *testing.T) {
	cases := []struct {
		label, src, want string
	}{
		{"unused alias over an unknown type",
			"(type Bad Missing)\n(defn one [] [] Int 1)\n(defn two [] [] Int (one))",
			"Missing"},
		{"unused duplicate alias name",
			"(type A Int)\n(type A Int)\n(defn one [] [] Int 1)\n(defn two [] [] Int (one))",
			"already defined"},
		{"unused malformed alias",
			"(type OnlyName)\n(defn one [] [] Int 1)\n(defn two [] [] Int (one))",
			"must be (type Name ty)"},
		{"unused alias shadowing a builtin",
			"(type Int {x Int})\n(defn one [] [] Int 1)\n(defn two [] [] Int (one))",
			"builtin"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			// The control: `put` refuses it. Without this the test could be asserting a
			// restriction publish made up.
			if _, err := apiPut(newStore(t), c.src, "t", ""); err == nil {
				t.Fatalf("control dead: put ACCEPTED %q, so publish refusing it would be a divergence", c.src)
			}
			st := newStore(t)
			batch, err := buildPublishBatch(st, c.src, "team/*")
			if err != nil {
				if !strings.Contains(err.Error(), c.want) {
					t.Fatalf("refused for the wrong reason: got %v, want something naming %q", err, c.want)
				}
				return // refused up front — also correct
			}
			// Otherwise the batch layout must carry the alias, so the validation put
			// catches it. A layout that omitted it would elaborate cleanly.
			perr := putBatch(newStore(t), batch.batchQual, "t")
			if perr == nil {
				t.Fatalf("publish accepted a file put refuses; the alias was dropped from the batch: %q", batch.batchQual)
			}
			if !strings.Contains(perr.Error(), c.want) {
				t.Errorf("refused for the wrong reason: got %v, want something naming %q", perr, c.want)
			}
		})
	}
}

// An alias referencing a batch name declared BELOW it is refused, matching `put` —
// publication reorders definitions, so accepting it would mean publishing a file `put`
// rejects, on the strength of a reordering the author never wrote.
func TestPublishRefusesAliasForwardReference(t *testing.T) {
	src := "(type P Point)\n" +
		"(data Point [] (Pt Int))\n" +
		"(defn cost [] [(p P) (n Int)] Int n)"
	if _, err := apiPut(newStore(t), src, "t", ""); err == nil {
		t.Fatal("control dead: put accepted an alias referencing a later declaration")
	}
	_, err := buildPublishBatch(newStore(t), src, "team/*")
	if err == nil || !strings.Contains(err.Error(), "declares LATER") {
		t.Fatalf("a forward reference from an alias body must be refused; got %v", err)
	}
}

// ALIAS SCOPE IS BOUNDED BY SOURCE POSITION, in both publish paths — found by external
// review. An alias declared BELOW its use is not in scope for it, because the registry
// re-elaborates the published bytes IN ORDER and would reject the definition. Gathering
// every alias before elaborating made the client hash and sign an artifact the server
// can never produce; hoisting one above its use made the batch accept a file `put`
// rejects. `put` is the control for both: it must report the same thing.
func TestPublishRefusesAliasDeclaredAfterItsUse(t *testing.T) {
	const want = `unknown type "A"`
	src := "(defn f [] [(x A)] Int x)\n(type A Int)"

	// The control. If put ever accepts this, both assertions below become divergences.
	_, perr := apiPut(newStore(t), src, "t", "")
	if perr == nil || !strings.Contains(perr.Error(), want) {
		t.Fatalf("control dead: put must reject a forward alias use with %s; got %v", want, perr)
	}

	// DIRECT path: the plan is built from the same bytes the server reads, so it must
	// fail here rather than produce an artifact hash the server will not reproduce.
	kHex, _ := newKey(t)
	plan, _, _, err := buildPublishPlan(newStore(t), headStub(t), kHex, src, "Apache-2.0", "")
	if err == nil {
		t.Errorf("the direct path planned %s at %s, but the registry rejects these exact bytes",
			plan.Name, shortHash(plan.Artifact))
	} else if !strings.Contains(err.Error(), want) {
		t.Errorf("the direct path must report what the server reports (%s); got %v", want, err)
	}

	// BATCH path: the alias must NOT be hoisted above its use. It is still emitted (so
	// it is still validated), just where it was written — so the closure fails to
	// elaborate exactly as put says it does.
	batch, berr := buildPublishBatch(newStore(t), src+"\n(defn g [] [] Int 1)", "team/*")
	if berr != nil {
		return // refused up front is also correct
	}
	if e := putBatch(newStore(t), batch.batchBare, "t"); e == nil {
		t.Errorf("the batch path hoisted the alias above its use, accepting a file put rejects: %q", batch.batchBare)
	} else if !strings.Contains(e.Error(), want) {
		t.Errorf("the batch path must report what put reports (%s); got %v", want, e)
	}
}

// A parametric alias's BOUND TYPE VARIABLES are binders, not type references — parseTy
// resolves a tyvar before it consults aliases or the store. Reading one as a reference
// made a valid bare batch look like a forward reference and refused it. Found by
// external review, on this source. `put` is again the control: it accepts this.
func TestPublishDoesNotReadAliasTyvarsAsTypeReferences(t *testing.T) {
	// `Point` here is the alias's PARAMETER; the datatype of the same name is declared
	// later and is a different thing entirely.
	src := "(type A [Point] Point)\n" +
		"(defn f [] [(x (A Int))] Int x)\n" +
		"(data Point [] (Pt Int))"
	if _, perr := apiPut(newStore(t), src, "t", ""); perr != nil {
		t.Fatalf("control dead: put must accept this — Point is a bound tyvar: %v", perr)
	}
	// The BARE path rewrites no tokens, so there is nothing to refuse.
	if _, err := buildPublishBatch(newStore(t), src, ""); err != nil {
		t.Errorf("a bare batch whose alias parameter shares a datatype's spelling is valid: %v", err)
	}
	// Under a NAMESPACE it is still refused, and correctly so for an unrelated reason:
	// token qualification would rewrite the type variable along with the datatype. That
	// refusal predates this work and must keep naming the collision, not a forward
	// reference.
	_, nerr := buildPublishBatch(newStore(t), src, "team/*")
	if nerr == nil || !strings.Contains(nerr.Error(), "batch-local name") {
		t.Errorf("qualifying a tyvar that shares a published name must be refused as a collision; got %v", nerr)
	}
}

// A TRAILING alias — declared below the sole definition — is valid, because the
// registry binds that definition before it reaches the alias. Validating it against the
// unmodified local store instead rejects a source the server accepts, making it
// unpublishable. Found by external review as a regression from scoping aliases to
// source position: the first fix made the client stricter than the server in the
// direction that costs the user a publication.
func TestPublishAcceptsAnAliasBelowItsDefinition(t *testing.T) {
	src := "(data Point [] (Pt Int))\n(type P Point)"
	// Controls: put accepts it, and it routes to the DIRECT path (an alias is not a
	// definition), which is where the defect lived.
	if _, err := apiPut(newStore(t), src, "t", ""); err != nil {
		t.Fatalf("control dead: put must accept a trailing alias over a preceding datatype: %v", err)
	}
	if multiDefinition(src) {
		t.Fatal("control dead: one definition plus an alias must take the direct path")
	}
	kHex, _ := newKey(t)
	plan, _, _, err := buildPublishPlan(newStore(t), headStub(t), kHex, src, "Apache-2.0", "")
	if err != nil {
		t.Fatalf("the direct path rejects a source the registry accepts: %v", err)
	}
	if plan.Name != "Point" {
		t.Errorf("the definition is what publishes, not the alias; got %q", plan.Name)
	}

	// ...and a trailing alias is still VALIDATED, not merely skipped: the same batch
	// rules apply to it, including duplication against an alias declared earlier.
	for _, bad := range []struct{ src, want string }{
		{"(data Point [] (Pt Int))\n(type P Missing)", "Missing"},
		{"(type P Int)\n(data Point [] (Pt Int))\n(type P Int)", "already defined"},
		{"(data Point [] (Pt Int))\n(type Int {x Int})", "builtin"},
	} {
		if _, err := apiPut(newStore(t), bad.src, "t", ""); err == nil {
			t.Fatalf("control dead: put accepted %q", bad.src)
		}
		_, _, _, perr := buildPublishPlan(newStore(t), headStub(t), kHex, bad.src, "Apache-2.0", "")
		if perr == nil || !strings.Contains(perr.Error(), bad.want) {
			t.Errorf("a trailing alias must still be validated (%s): got %v", bad.want, perr)
		}
	}
}

// ATOMICITY. A trailing alias must NOT be transmitted. It publishes nothing and is not
// in scope for the definition above it, so dropping it cannot change what the registry
// derives — but SENDING it can destroy the publication's atomicity: apiPutSigned stores
// and REPOINTS the definition before it reaches the alias, so an alias valid on the
// client and not on the registry fails after the name has irreversibly moved, and the
// command reports failure over a publication that happened. Found by external review.
func TestPublishDoesNotTransmitTrailingAliases(t *testing.T) {
	// The hazard, demonstrated against the real server path — this is why the trim
	// exists, and it is asserted so the reason cannot quietly stop being true.
	server := newMemStoreForTest(t)
	hazard := "(data Point [] (Pt Int))\n(type P LocalOnly)" // LocalOnly is absent there
	reps, serr := apiPutSigned(server, hazard, "author", "", nil)
	if serr == nil {
		t.Fatal("control dead: the server accepted an alias over a type it does not hold")
	}
	if len(reps) == 0 || reps[0].Status != "accepted" {
		t.Fatalf("control dead: the server must COMMIT the definition before failing: %+v", reps)
	}
	if _, moved := server.Resolve("Point"); !moved {
		t.Fatal("control dead: the name did not move, so transmitting the alias would be harmless")
	}

	// The client therefore sends the definition and its LEADING aliases only.
	local := newStore(t)
	put(t, local, `(data LocalOnly [] (L Int))`)
	kHex, _ := newKey(t)
	_, _, send, err := buildPublishPlan(local, headStub(t), kHex, hazard, "Apache-2.0", "")
	if err != nil {
		t.Fatalf("the source is valid locally and must plan: %v", err)
	}
	if strings.Contains(send, "(type P") {
		t.Errorf("the trailing alias must not be transmitted; it can only fail after the name moves:\n%s", send)
	}
	if !strings.Contains(send, "(data Point") {
		t.Errorf("the definition itself must still be transmitted:\n%s", send)
	}
	// And the trimmed bytes are what the registry accepts, cleanly.
	fresh := newMemStoreForTest(t)
	reps2, err2 := apiPutSigned(fresh, send, "author", "", nil)
	if err2 != nil || len(reps2) != 1 || reps2[0].Status != "accepted" {
		t.Fatalf("the transmitted bytes must publish exactly one definition cleanly: err=%v reps=%+v", err2, reps2)
	}

	// A LEADING alias is still sent — the definition cannot elaborate without it.
	st := newStore(t)
	put(t, st, pubAliasExtDeps)
	_, _, leadSend, lerr := buildPublishPlan(st, headStub(t), kHex, pubAliasSrc, "Apache-2.0", "")
	if lerr != nil {
		t.Fatal(lerr)
	}
	if !strings.Contains(leadSend, "(type Env") {
		t.Errorf("a leading alias is required to elaborate the definition and must be sent:\n%s", leadSend)
	}

	// With no trailing alias the author's exact bytes go, comments and spacing included.
	verbatim := "(data Point [] (Pt Int))   ; a comment the author wrote\n"
	_, _, vSend, verr := buildPublishPlan(newStore(t), headStub(t), kHex, verbatim, "Apache-2.0", "")
	if verr != nil {
		t.Fatal(verr)
	}
	if vSend != verbatim {
		t.Errorf("an ordinary publication must transmit the source unchanged:\n got %q\nwant %q", vSend, verbatim)
	}
}

// Alias dependencies are a question about TYPES, and are read off type POSITIONS rather
// than off every symbol in the form. A flat symbol walk answers a different question
// confidently: it reads record FIELD LABELS and value BINDERS as type references. Both
// shapes below were found by external review; `put` accepts each, so a publish-side
// refusal or a spurious transmission is a divergence, not a stricter rule.
func TestPublishClassifiesOnlyTypePositions(t *testing.T) {
	// A record field LABEL is not a type reference. `Later` is a field of the alias's
	// record AND an unrelated definition below it — no forward reference exists.
	t.Run("record label is not a type reference", func(t *testing.T) {
		src := "(type Box {Later Int})\n" +
			"(defn use [] [(b Box)] Int (. b Later))\n" +
			"(defn Later [] [] Int 1)"
		if _, err := apiPut(newStore(t), src, "t", ""); err != nil {
			t.Fatalf("control dead: put must accept this — Later is a field label: %v", err)
		}
		if _, err := buildPublishBatch(newStore(t), src, ""); err != nil {
			t.Errorf("a record field label was read as a type reference: %v", err)
		}
	})

	// A value BINDER spelled like an alias does not make the definition need the alias.
	// Transmitting it anyway can fail remotely — the registry may hold a datatype under
	// that name where the local store does not — for a definition that never uses it.
	t.Run("value binder does not pull in an alias", func(t *testing.T) {
		st := newStore(t)
		put(t, st, pubAliasExtDeps)
		src := "(type Env {decode (-> Payload Int) size Int})\n" +
			"(defn cost [] [(Env Int)] Int Env)\n" +
			"(defn other [] [] Int 1)"
		ctl := newStore(t)
		put(t, ctl, pubAliasExtDeps)
		if _, err := apiPut(ctl, src, "t", ""); err != nil {
			t.Fatalf("control dead: %v", err)
		}
		batch, err := buildPublishBatch(st, src, "")
		if err != nil {
			t.Fatal(err)
		}
		for i, txt := range batch.planText {
			if batch.ordered[i].name == "cost" && strings.Contains(txt, "(type Env") {
				t.Errorf("cost uses Env only as a value binder; the alias must not travel with it:\n%s", txt)
			}
		}
	})

	// The other direction, so the precision is not bought by under-collecting: an alias
	// used in each genuine type position IS found. A miss here would surface as an
	// unknown-type failure in the validation put, which is what the second half asserts.
	t.Run("every genuine type position is found", func(t *testing.T) {
		st := newStore(t)
		put(t, st, pubAliasExtDeps)
		cases := []struct{ label, defn string }{
			{"parameter type", `(defn u [] [(e Env)] Int (. e size))`},
			{"return type", `(defn u [] [(n Int)] Env (mk n))`},
			{"let binder type", `(defn u [] [(e Env)] Int (let (q Env e) (. q size)))`},
			{"fn parameter type", `(defn u [] [(e Env)] Int ((fn [(q Env)] Int (. q size)) e))`},
			{"property binder type", `(defn u [] [(n Int)] Int n (prop p [(e Env)] (== (u (. e size)) (. e size))))`},
			{"nested inside a type application", `(defn u [] [(l (List Env))] Int 0)`},
		}
		checked := 0
		for _, c := range cases {
			src := "(type Env {decode (-> Payload Int) size Int})\n" + c.defn + "\n(defn other [] [] Int 1)"
			batch, err := buildPublishBatch(st, src, "")
			if err != nil {
				t.Errorf("%s: fixture must build, or this row checks nothing: %v", c.label, err)
				continue
			}
			checked++
			found := false
			for i, txt := range batch.planText {
				if batch.ordered[i].name == "u" && strings.Contains(txt, "(type Env") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: the alias is used there and must travel with the definition", c.label)
			}
		}
		// The guard that keeps this row from passing vacuously: every fixture must have
		// reached the assertion, not been skipped by a setup failure.
		if checked != len(cases) {
			t.Errorf("only %d of %d type positions were actually checked", checked, len(cases))
		}
	})
}

// SCOPE, in both directions. The type-position walker has to agree with the elaborator
// about where a name means the alias, and both errors it can make are costly: missing a
// use rejects a valid source in the validation put; inventing one prepends an alias that
// a registry holding a datatype of that name will refuse. Both shapes found by review.
func TestPublishAliasScopeMatchesTheElaborator(t *testing.T) {
	planFor := func(t *testing.T, src, name string) string {
		t.Helper()
		if _, err := apiPut(newStore(t), src, "t", ""); err != nil {
			t.Fatalf("control dead: put must accept this source: %v", err)
		}
		b, err := buildPublishBatch(newStore(t), src, "")
		if err != nil {
			t.Fatalf("buildPublishBatch: %v", err)
		}
		// The batch layout must elaborate too — that is where a missed dependency shows.
		if e := putBatch(newStore(t), b.batchBare, "t"); e != nil {
			t.Fatalf("the batch layout rejects a source put accepts: %v", e)
		}
		for i := range b.planText {
			if b.ordered[i].name == name {
				return b.planText[i]
			}
		}
		t.Fatalf("no plan for %s", name)
		return ""
	}

	// A record LITERAL's values are TERMS and can carry types. Skipping the brace missed
	// an alias used only there.
	t.Run("alias inside a record literal value", func(t *testing.T) {
		got := planFor(t, "(type A Int)\n"+
			"(defn use [] [] Int (. {x (let (v A 1) v)} x))\n"+
			"(defn other [] [] Int 1)", "use")
		if !strings.Contains(got, "(type A") {
			t.Errorf("the alias is used inside the record literal and must travel with it:\n%s", got)
		}
	})

	// A form's own [tyvars] SHADOW aliases — parseTy resolves a bound type variable
	// before consulting the alias map — so `id` does not use the alias at all.
	t.Run("a definition's type variable is not the alias", func(t *testing.T) {
		got := planFor(t, "(type A Int)\n(defn id [A] [(x A)] A x)\n(defn other [] [] Int 1)", "id")
		if strings.Contains(got, "(type A") {
			t.Errorf("A is id's own type variable, not the alias; it must not travel with it:\n%s", got)
		}
	})

	// ...and the same for a datatype's parameters.
	t.Run("a datatype's type variable is not the alias", func(t *testing.T) {
		got := planFor(t, "(type A Int)\n(data Box [A] (Mk A))\n(defn other [] [] Int 1)", "Box")
		if strings.Contains(got, "(type A") {
			t.Errorf("A is Box's own type variable:\n%s", got)
		}
	})

	// THE ASYMMETRY, verified against the elaborator rather than assumed: elabFuncRaw
	// builds a FRESH elab per property WITHOUT the function's tyvars, so inside a
	// property the same spelling resolves to the ALIAS. Subtracting tyvars there would
	// drop a real dependency — which is why the walker scopes them per position.
	t.Run("a property sees the alias, not the enclosing type variable", func(t *testing.T) {
		// The control that establishes the asymmetry: with no alias, the property's `A`
		// is an unknown type even though the function declares a tyvar A.
		if _, err := apiPut(newStore(t), `(defn id [A] [(x A)] A x (prop p [(y A)] (== (id [Int] y) y)))`, "t", ""); err == nil {
			t.Fatal("control dead: a property must NOT see the function's type variables")
		}
		got := planFor(t, "(type A Int)\n"+
			`(defn id [A] [(x A)] A x (prop p [(y A)] (== (id [Int] y) y)))`+"\n"+
			"(defn other [] [] Int 1)", "id")
		if !strings.Contains(got, "(type A") {
			t.Errorf("the property's binder resolves to the ALIAS, so it must travel with the definition:\n%s", got)
		}
	})
}

// An alias's own NAME is a batch-local declaration, not a reference. Under a namespace
// qualifyNames is token-based, so it would rewrite `(type f Int)` to `(type team/f Int)`
// — retargeting a declaration that publishes nothing. Where `team/f` is already bound to
// a datatype, alias registration then refuses the generated source and blocks a
// legitimate replacement publication. Found by external review; measured below, because
// the consequence is what justifies refusing rather than tolerating it.
func TestPublishRefusesAliasNameCollidingWithAPublishedName(t *testing.T) {
	src := "(type f Int)\n(defn f [] [(x f)] f x)\n(defn g [] [] Int 1)"
	// The control: put accepts this — an alias and a function live in different
	// namespaces — so the refusal below is a publication-specific restriction and must
	// stay confined to the qualifying path.
	if _, err := apiPut(newStore(t), src, "t", ""); err != nil {
		t.Fatalf("control dead: put must accept an alias sharing a definition's spelling: %v", err)
	}

	// BARE path: nothing is rewritten, so nothing collides. It must still be accepted.
	bare, berr := buildPublishBatch(newStore(t), src, "")
	if berr != nil {
		t.Fatalf("the bare path rewrites no tokens and must accept this: %v", berr)
	}
	if e := putBatch(newStore(t), bare.batchBare, "t"); e != nil {
		t.Errorf("the bare batch layout must elaborate: %v", e)
	}

	// NAMESPACED path: refused, and named as a collision rather than silently retargeted.
	_, nerr := buildPublishBatch(newStore(t), src, "team/*")
	if nerr == nil {
		t.Fatal("qualification would rewrite the alias's own declaration; it must be refused")
	}
	if !strings.Contains(nerr.Error(), "batch-local name") || !strings.Contains(nerr.Error(), `"f"`) {
		t.Errorf("the refusal must name the colliding symbol as batch-local: %v", nerr)
	}
}
