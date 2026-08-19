package main

import (
	"strings"
	"testing"
)

// The spec-query surface matches definitions by the CONTENT HASH of their
// properties, not by name. Two differently-implemented functions that satisfy
// the same-shaped law converge on the same propHash (the law references `self`
// and de Bruijn binders, so it is def-independent) and are surfaced together; a
// function with a different law is not.
func TestFindMatchesByPropContentHash(t *testing.T) {
	st := newStore(t)
	// Two DIFFERENT functions (+ vs *) carrying the SAME commutativity law.
	put(t, st, `(defn op-a [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (op-a a b) (op-a b a))))`)
	put(t, st, `(defn op-b [] [(a Int) (b Int)] Int (* a b)
		(prop comm [(a Int) (b Int)] (== (op-b a b) (op-b b a))))`)
	// A third with a DIFFERENT property (trivial self-equality, not commutativity).
	put(t, st, `(defn op-c [] [(a Int)] Int (+ a 1)
		(prop refl [(a Int)] (== (op-c a) (op-c a))))`)

	// propHash is genuinely equal across the two commutative ops...
	if propHash(&mustDef(t, st, "op-a").Props[0]) != propHash(&mustDef(t, st, "op-b").Props[0]) {
		t.Fatal("commutativity should hash identically for op-a and op-b")
	}
	// ...and different from op-c's law.
	if propHash(&mustDef(t, st, "op-a").Props[0]) == propHash(&mustDef(t, st, "op-c").Props[0]) {
		t.Fatal("distinct laws must hash differently")
	}

	out, err := apiFind(st, "op-a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "op-b") {
		t.Fatalf("find op-a should surface op-b (shared commutativity):\n%s", out)
	}
	if strings.Contains(out, "op-c") {
		t.Fatalf("find op-a should NOT surface op-c (different law):\n%s", out)
	}
}

// Cross-type matching: a law that is polymorphic in its operand type (e.g.
// commutativity) matches across the types it ranges over. Commutativity over
// Int and over Rat share a generalized property hash and are surfaced together.
func TestFindCrossTypeMatch(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn plus-i [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (plus-i a b) (plus-i b a))))`)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop comm [(a Rat) (b Rat)] (== (plus-r a b) (plus-r b a))))`)

	// EXACT hashes differ (Int vs Rat binders)...
	if propHash(&mustDef(t, st, "plus-i").Props[0]) == propHash(&mustDef(t, st, "plus-r").Props[0]) {
		t.Fatal("exact propHash should differ across Int and Rat binders")
	}
	// ...but the GENERALIZED hashes match (both [t0,t0]).
	if propHashGeneral(&mustDef(t, st, "plus-i").Props[0]) != propHashGeneral(&mustDef(t, st, "plus-r").Props[0]) {
		t.Fatal("generalized propHash should match commutativity across Int and Rat")
	}
	out, err := apiFind(st, "plus-i")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plus-r") {
		t.Fatalf("find plus-i should cross-type match plus-r:\n%s", out)
	}
}

// Fresh-spec query: write a specification (a defn whose props are the query),
// and find proven implementations — no example, no name of the target used.
func TestFindSpecFreshQuery(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop comm [(a Rat) (b Rat)] (== (plus-r a b) (plus-r b a))))`)

	// The query is a fresh Int-commutativity spec; plus-r is Rat and never named.
	out, err := apiFindSpec(st, `(defn wanted [] [(a Int) (b Int)] Int (+ a b)
		(prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plus-r") {
		t.Fatalf("fresh spec (Int commutativity) should find plus-r (Rat, cross-type):\n%s", out)
	}
	// (plus-r is only `tested` here — no Z3 in unit tests — so the "proven
	// implementation" flag is exercised by the live demo against the real corpus,
	// where rat-add/rat-mul are proven.)

	// A spec nobody satisfies returns cleanly (no false matches).
	out2, err := apiFindSpec(st, `(defn odd [] [(a Int)] Int (+ a 1)
		(prop weird [(a Int)] (== (odd (odd a)) a)))`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "plus-r") {
		t.Fatalf("an unrelated spec must not match plus-r:\n%s", out2)
	}
}

// Proof-implication finds definitions that PROVABLY satisfy a spec, even when
// the spec is written differently from any law they state. Commutativity written
// `(== (self b a) (self a b))` has a different AST from the usual form (so the
// content-hash surface misses it), but `+` still provably satisfies it.
func TestFindImplies(t *testing.T) {
	if z3Available() != nil {
		t.Skip("z3 not available")
	}
	st := newStore(t)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b))`)

	// The exact-hash surface misses this flipped form...
	exact, err := apiFindSpec(st, `(defn q [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop fc [(a Rat) (b Rat)] (== (q b a) (q a b))))`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exact, "plus-r") {
		t.Fatalf("exact-hash find should MISS the flipped form (that's the point):\n%s", exact)
	}
	// ...but proof-implication proves it.
	impl, err := apiFindImplies(st, `(defn q [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop fc [(a Rat) (b Rat)] (== (q b a) (q a b))))`, findImpliesSummary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(impl, "plus-r") {
		t.Fatalf("proof-implication should find plus-r (+ provably satisfies flipped comm):\n%s", impl)
	}

	// A FALSE SPEC IS A REFUTATION, NOT AN ABSENCE (#156). Left-projection is not
	// true of +, and that is something ESTABLISHED about plus-r rather than
	// something the search failed to find. The assertion this replaces read
	// `!Contains(no, "plus-r")` over the summary output, which is satisfied both
	// by "plus-r was refuted and reported as such" and by "plus-r was silently
	// dropped" — the conflation #156 exists to remove, sitting inside the test
	// that was supposed to pin the behaviour.
	//
	// WHAT MUTATION MAKES IT FAIL: the pre-change behaviour itself — `continue`ing
	// past a concretely refuted candidate without recording it (equivalently,
	// dropping the `evidence:`/`byEval:` fields, or rendering detail as summary).
	// Then the REFUTED section never appears and no countermodel is reported.
	// Verified by reverting.
	const falseSpec = `(defn q [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop proj [(a Rat) (b Rat)] (== (q a b) a)))`
	detailed, err := apiFindImplies(st, falseSpec, findImpliesDetailed)
	if err != nil {
		t.Fatal(err)
	}
	// Detailed output must place plus-r under REFUTED, with the countermodel.
	//
	// THE EVIDENCE IS ASSERTED ON plus-r's OWN LINE, not on the section. That
	// section's heading reads "REFUTED — proved NOT to satisfy it (a countermodel
	// exists)", so a Contains for "countermodel" over the section is satisfied by
	// the heading alone and stays green with every candidate's evidence stripped.
	// The candidate NAME is safe to look for section-wide because no heading
	// contains it; the evidence word is not.
	sect := implyDetailSection(t, detailed, "REFUTED")
	cand := ""
	for _, line := range strings.Split(sect, "\n") {
		if strings.Contains(line, "plus-r") {
			cand = line
		}
	}
	if cand == "" {
		t.Errorf("a refuted candidate must be reported under REFUTED, not dropped:\n%s", detailed)
	} else if !strings.Contains(cand, "countermodel") {
		t.Errorf("the refutation must carry its countermodel — a refutation without evidence is an "+
			"assertion:\n%s", cand)
	}
	// ...and must NOT be reported as satisfying the spec. The name appearing is
	// only correct because of WHERE it appears, so both halves are asserted.
	if hit := satisfyingLineFor(detailed, "plus-r"); hit != "" {
		t.Errorf("a false spec must not report plus-r as satisfying it:\n%s", hit)
	}
	// The world-claim must be suppressed: something WAS established here, so
	// "no definition provably satisfies this" is not the report.
	if strings.Contains(detailed, "no definition provably satisfies this") {
		t.Errorf("the no-match fallback fired despite a refutation on record:\n%s", detailed)
	}

	// SUMMARY MODE, the same query: the refutation is COUNTED, and the candidate
	// is not named. This is what keeps the old assertion's meaning — plus-r is
	// not presented as a hit — while making the count the thing that says so.
	no, err := apiFindImplies(st, falseSpec, findImpliesSummary)
	if err != nil {
		t.Fatal(err)
	}
	// SUMMARY MODE NAMES THE REFUTATION TOO. The count alone is not actionable —
	// "1 REFUTED" does not tell you WHICH definition was disproved, and that is
	// the finding. Both the count and the name are asserted, because either
	// alone passes for a renderer that drops the other.
	if !strings.Contains(no, "1 REFUTED") {
		t.Fatalf("summary mode must report the refutation count:\n%s", no)
	}
	if !strings.Contains(no, "plus-r") {
		t.Fatalf("summary mode must NAME the refuted candidate — a refutation is a result, and "+
			"a count without a name cannot be acted on:\n%s", no)
	}
}

// satisfyingLineFor returns the line reporting `name` as a HIT, or "" if there
// is none. It is how a test asks "was this candidate returned as satisfying the
// query?" without conflating that with "is this name mentioned anywhere in the
// report" — two different questions that were the same question until refutations
// began being reported by name.
//
// It matches on implySatisfiesMarker, the same constant the renderer writes, so a
// reworded hit line breaks the writer and the reader together instead of leaving
// every caller here silently matching nothing.
func satisfyingLineFor(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name) && strings.Contains(line, implySatisfiesMarker) {
			return line
		}
	}
	return ""
}

// implyDetailSection returns the lines of `out` belonging to the status row
// whose label contains `label` — the row line itself plus the indented candidate
// lines under it, stopping at the next row or the next query property.
//
// Asserting on a SECTION rather than on the whole output is what makes "reported
// as refuted" different from "the name appears somewhere". A Contains over the
// full text is satisfied by a candidate listed under any heading at all, which is
// exactly the imprecision that let a silent drop and a reported refutation look
// alike.
func implyDetailSection(t *testing.T, out, label string) string {
	t.Helper()
	var sect []string
	in := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		switch {
		case in && indent > 6:
			sect = append(sect, line)
		case in:
			in = false
		}
		if indent == 6 && strings.Contains(line, label) {
			in = true
			sect = append(sect, line)
		}
	}
	if len(sect) == 0 {
		t.Fatalf("no %q section in output:\n%s", label, out)
	}
	return strings.Join(sect, "\n")
}

// TestFindImpliesCrossType is the regression witness for the CROSS-TYPE
// admission: a query stated over Int finds definitions whose signature differs
// only in its primitive leaves, with the query's binders re-typed to theirs.
//
// It fails on exact-signature-only code, and each assertion names the mutation
// that breaks it:
//   - REVERT THE ADMISSION (skip candidates whose signature is not byte-equal)
//     and the Rat and Bool hits disappear.
//   - REVERT THE RE-TYPING (append the Int-bindered property verbatim to a Rat
//     definition) and checkDef rejects every cross-type candidate, so the hits
//     disappear the same way — which is why the second assertion checks the
//     RE-TYPED SIGNATURE is reported, not merely that a name appears.
//   - DROP THE checkDef GATE and the last section reports a candidate for a
//     property whose body still says Int while its binders say Rat. That is
//     measured, not assumed: with the gate removed the prover returns `proven`
//     for the ill-typed augmentation rather than failing, because nothing
//     downstream re-typechecks what it is handed.
//
// The negative assertions are not decoration. Admission is deliberately wider
// than truth — (Rat,Rat)->Rat and (Bool,Bool)->Bool both generalize to
// (t0,t0)->t0 — so the PROOF is the filter, and a non-commutative candidate of a
// compatible signature must be admitted and then rejected.
//
// THE TWO CONTROLS ASSERT DIFFERENT THINGS AND MUST BE DISTINGUISHABLE, which is
// why the first query runs in DETAIL mode. `minus-r` is admitted and then
// refuted; `add3-r` is never admitted at all. Under summary output neither is
// named, so a single `!Contains(out, ...)` was true of both for two unrelated
// reasons — and would have gone on passing if admission had silently stopped
// reaching `minus-r`, which is the exact regression this test exists to catch.
// The contract is therefore stated positively for one and negatively for the
// other:
//
//	minus-r   PRESENT, in the REFUTED section, with a countermodel and its
//	          re-typed signature — proof of admission AND of rejection
//	add3-r    ABSENT from the whole report — never a candidate, so there is
//	          nothing to classify
//
// The signature assertions are read off the SATISFYING line for each hit rather
// than off the whole report, for the same reason: in detail mode a refuted
// cross-type candidate also prints `at (-> Rat Rat Rat)`, so a whole-output
// match would no longer establish that the HIT reported its type.
func TestFindImpliesCrossType(t *testing.T) {
	if z3Available() != nil {
		t.Skip("z3 not available")
	}
	st := newStore(t)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b))`)
	put(t, st, `(defn and-b [] [(a Bool) (b Bool)] Bool (and a b))`)
	// CONTROL: signature-compatible with the query, and NOT commutative.
	put(t, st, `(defn minus-r [] [(a Rat) (b Rat)] Rat (- a b))`)
	// CONTROL: a different signature SHAPE (three arguments), which the
	// compatibility relation must not admit at all.
	put(t, st, `(defn add3-r [] [(a Rat) (b Rat) (c Rat)] Rat (+ a (+ b c)))`)

	out, err := apiFindImplies(st, `(defn q [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (q a b) (q b a))))`, findImpliesDetailed)
	if err != nil {
		t.Fatal(err)
	}
	// THE CLAIM: an Int query reaches Rat and Bool definitions, and each hit
	// reports the signature it was proved AT, so a reader can see it was a
	// different type rather than assume an exact match. Both facts are read off
	// the candidate's own SATISFYING line — a whole-output match would accept the
	// signature appearing on somebody else's line.
	for _, c := range []struct{ name, sig string }{
		{"plus-r", "(-> Rat Rat Rat)"},
		{"and-b", "(-> Bool Bool Bool)"},
	} {
		hit := satisfyingLineFor(out, c.name)
		if hit == "" {
			t.Fatalf("cross-type proof-implication should find %s for an Int commutativity query:\n%s", c.name, out)
		}
		if !strings.Contains(hit, c.sig) || !strings.Contains(hit, "cross-type") {
			t.Fatalf("%s's hit must report %s and be labelled cross-type:\n%s", c.name, c.sig, hit)
		}
	}
	// CONTROL: ADMITTED, proved against, and REJECTED by the prover — and both
	// halves are asserted, because only the pair separates this from add3-r
	// below. Its presence in the REFUTED section witnesses admission; the
	// countermodel witnesses that a verdict was actually reached rather than the
	// candidate being set aside; the re-typed signature witnesses that admission
	// went through the cross-type path.
	refuted := implyDetailSection(t, out, "REFUTED")
	minus := ""
	for _, line := range strings.Split(refuted, "\n") {
		if strings.Contains(line, "minus-r") {
			minus = line
		}
	}
	if minus == "" {
		t.Fatalf("subtraction is not commutative, so minus-r must be reported as REFUTED — its absence "+
			"here means it was never admitted, which is a different failure:\n%s", out)
	}
	// ASSERTED ON minus-r's OWN LINE, not on the section. The section's heading is
	// "REFUTED — proved NOT to satisfy it (a countermodel exists)", so a Contains
	// for "countermodel" over the section is satisfied by the heading and would
	// pass with every candidate's evidence stripped. Found by mutation, not by
	// reading: dropping the retained witness left the section assertion green.
	for _, want := range []string{"countermodel", "(-> Rat Rat Rat)"} {
		if !strings.Contains(minus, want) {
			t.Fatalf("minus-r's refutation must carry %q — otherwise this witnesses a name in a section "+
				"and not a verdict reached about a cross-type candidate:\n%s", want, minus)
		}
	}
	if hit := satisfyingLineFor(out, "minus-r"); hit != "" {
		t.Fatalf("subtraction is not commutative and must not be reported as satisfying the query:\n%s", hit)
	}
	// CONTROL: not signature-compatible in the first place, so it is not a
	// candidate and appears NOWHERE — not as a hit, not as a refutation, not as
	// an unsettled goal. This is the assertion that must stay a whole-report one.
	if strings.Contains(out, "add3-r") {
		t.Fatalf("a 3-argument signature must not be admitted against a 2-argument query:\n%s", out)
	}

	// CONTROL, the other direction: exact-signature behaviour is unchanged and
	// reports NO cross-type label, so widening did not relabel the old path.
	put(t, st, `(defn plus-i [] [(a Int) (b Int)] Int (+ a b))`)
	exact, err := apiFindImplies(st, `(defn q [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (q a b) (q b a))))`, findImpliesSummary)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(exact, "\n") {
		if strings.Contains(line, "plus-i") && strings.Contains(line, "cross-type") {
			t.Fatalf("an exact-signature hit must not be labelled cross-type:\n%s", line)
		}
	}
	if !strings.Contains(exact, "plus-i") {
		t.Fatalf("the exact-signature path must still find plus-i:\n%s", exact)
	}

	// THE checkDef GATE, witnessed. Only BINDERS are re-typed, so a property
	// whose BODY carries its own type annotation still says Int after the
	// binders say Rat. That augmentation is ill-typed and must be dropped —
	// dropping the gate instead makes this same query report plus-r as
	// `provably satisfies it`, because the prover does not re-typecheck what it
	// is handed. This is the rung-3 residue: rejected, not silently approximated.
	//
	// THE OLD ASSERTION WAS `!Contains(residue, "plus-r2")` OVER SUMMARY OUTPUT,
	// AND IT NAMED A CAUSE IT COULD NOT SEE. Summary mode counts the residue
	// without naming it (renderImplyResults skips the non-refuted rows), so the
	// absence of the name was equally consistent with the intended drop, with
	// plus-r2 being classified NO VERDICT, and with it being ruled out for
	// signature incompatibility that has nothing to do with the gate. Three
	// causes, one silence, and the failure message asserted a specific one of
	// them. The contract is therefore split into the two claims it was making:
	//
	//	the DECISION   crossTypeCompatible admits the signature, and checkDef
	//	               then rejects the augmentation — "dropped BECAUSE
	//	               ill-typed", asserted where the decision is made
	//	the REPORT     plus-r2 is in NONE of the four dispositions, asserted in
	//	               DETAIL mode, where every classification is named
	//
	// WHAT MUTATIONS MAKE THIS BLOCK FAIL — each applied, run, and reverted. The
	// third is the one that establishes the strengthening, and the last two are
	// the ones that keep the guards from being decoration:
	//
	//   - DROP THE checkDef GATE. plus-r2 is reported as `provably satisfies it at
	//     (-> Rat Rat Rat)` — a false proof — and the satisfying check fires.
	//   - RECORD THE ILL-TYPED CANDIDATE AS implyRefuted instead of dropping it.
	//     The whole-report check fires. The old assertion also caught this one,
	//     because summary mode names refutations.
	//   - RECORD IT AS implyUnknown (NO VERDICT). The whole-report check fires.
	//     THE OLD ASSERTION PASSED THIS UNCHANGED: summary mode counts the residue
	//     without naming it, so "plus-r2 does not appear" was true of a candidate
	//     that had been classified. This is why the report half moved to detail.
	//   - CLASSIFY IT AS implyUnknown *AND* STOP NAMING THE RESIDUE IN DETAIL MODE.
	//     The name is then absent from a report that classified the candidate
	//     anyway, so every name-based check passes and only the FALLBACK assertion
	//     fires. That is the whole reason the fallback is asserted: a negative is
	//     satisfied by a renderer that drops the name, a fired fallback is not.
	//   - MAKE checkDef ACCEPT the augmentation. The decision half fires — and
	//     nothing else does, which is the point of asserting the gate directly
	//     rather than only through what the report omits.
	//   - GIVE plus-r2 A THIRD Rat ARGUMENT. The signature stops being cross-type
	//     compatible, so the gate is never reached and the VACUITY GUARD fires —
	//     before checkErr is looked at, which is the ordering that makes the guard
	//     mean anything.
	//   - MAKE crossTypeCompatible ARITY-BLIND. The add3-r control fires, and it
	//     is the only assertion here that can catch it.
	// RUNG 3 (#65): a property whose BODY carries a type is now matched cross-type
	// too. The law commutes q, wrapped in an Int-annotated identity lambda that
	// lives in the BODY, and the candidate is plus-r2 : (Rat,Rat)->Rat. Before
	// rung 3 only the binders were re-typed, so the body's `(fn [(x Int)] x)`
	// kept its Int annotation, the augmentation was ill-typed against Rat binders,
	// and checkDef dropped it — a documented ASYMMETRY (binders generalized,
	// bodies did not). Rung 3 threads the same Int->Rat substitution through the
	// body, so the augmentation typechecks and plus-r2 is PROVED — a TRUE
	// cross-type proof, because commutativity holds at Rat.
	//
	// THE CONTRACT INVERTED, AND THE NEW ONE IS PINNED AT LEAST AS TIGHTLY (#156):
	// the old test asserted checkDef REJECTED the augmentation and plus-r2 was in
	// NO classification; this asserts checkDef ACCEPTS it and plus-r2 is in the
	// SATISFIED classification. Soundness is unchanged: checkDef still gates
	// well-typedness and Z3 still gates truth, so a re-typed law FALSE at the new
	// type would be refuted, never proved — the same filter the binder-only path
	// has always relied on.
	const annotatedQuery = `(defn q [] [(a Int) (b Int)] Int (+ a b)
		(prop annotated [(a Int) (b Int)] (== ((fn [(x Int)] x) (q a b)) (q b a))))`
	st2 := newStore(t)
	put(t, st2, `(defn plus-r2 [] [(a Rat) (b Rat)] Rat (+ a b))`)

	// The augmentation now TYPECHECKS — direct evidence that the body's Int
	// annotation was re-typed to Rat. The vacuity guard is unchanged: a
	// signature that was never compatible would make this witness nothing.
	compatible, checkErr := crossTypeAugmentForTest(t, st2, annotatedQuery, "plus-r2")
	if !compatible {
		t.Fatalf("plus-r2's signature is no longer cross-type compatible with the query, so the " +
			"body-retyping path is never reached and this control witnesses nothing about it")
	}
	if checkErr != nil {
		t.Fatalf("checkDef REJECTED the re-typed augmentation: rung 3 re-types the body's own Int "+
			"annotation to Rat, so the augmentation must typecheck. If it does not, body retyping "+
			"is not being applied: %v", checkErr)
	}

	// TWO-WAY CONTROL ON THE COMPATIBILITY GUARD. A 3-argument candidate must NOT
	// be called compatible with the 2-argument query — otherwise the guard above
	// is dead and could pass on an arity-blind relation no fixture change reveals.
	if compat, _ := crossTypeAugmentForTest(t, st, `(defn q [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (q a b) (q b a))))`, "add3-r"); compat {
		t.Fatal("crossTypeAugmentForTest calls a 3-argument candidate signature-compatible with a " +
			"2-argument query, so its compatibility result cannot distinguish anything and the " +
			"vacuity guard above is dead")
	}

	// THE REPORT, in DETAIL mode. plus-r2 must now appear in the SATISFIED
	// classification — the body-carrying law is provably satisfied cross-type —
	// and the empty-answer fallback must NOT fire, since the classified set is
	// non-empty.
	residue, err := apiFindImplies(st2, annotatedQuery, findImpliesDetailed)
	if err != nil {
		t.Fatal(err)
	}
	if hit := satisfyingLineFor(residue, "plus-r2"); hit == "" {
		t.Fatalf("plus-r2 was NOT reported as provably satisfying the body-carrying law; rung 3 "+
			"should prove it cross-type (commutativity holds at Rat):\n%s", residue)
	}
	if strings.Contains(residue, "no definition provably satisfies this") {
		t.Fatalf("the empty-answer fallback fired even though plus-r2 satisfies the query — the "+
			"classified set was wrongly empty:\n%s", residue)
	}
}

// crossTypeAugmentForTest rebuilds, for ONE named candidate, exactly the
// augmentation apiFindImplies would hand the prover — the query's property
// re-typed to the candidate's signature and appended past the candidate's own
// props — and reports the two facts that decide whether it is admitted:
//
//	compatible  did crossTypeCompatible admit the SIGNATURE at all?
//	err         what did the checkDef gate say about the augmentation?
//
// It exists so a test can assert WHY a candidate is missing from a report rather
// than only THAT it is missing. Absence has several causes — an incompatible
// signature, an unreadable object, an ill-typed augmentation — and a report
// cannot tell them apart, because all three produce the same silence. Splitting
// them is what turns "plus-r2 does not appear" into "plus-r2 is compatible and
// was rejected by checkDef", which is the claim the residue control is making.
//
// It uses the SHIPPED predicates (crossTypeCompatible, crossTypeRetypeBinders,
// crossTypeRetypeBody, checkDef) rather than re-implementing the decision, so it
// cannot pass on a re-implementation that has drifted from the path under test.
func crossTypeAugmentForTest(t *testing.T, st *Store, query, name string) (bool, error) {
	t.Helper()
	forms, err := parseForms(query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	qd, _, err := elabFunc(st, forms[0])
	if err != nil {
		t.Fatalf("elaborate query: %v", err)
	}
	h, ok := st.Names()[name]
	if !ok {
		t.Fatalf("%s is not in the store, so nothing here witnesses its admission", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	qp := qd.Props[0]
	if string(tyBytes(d.Ty)) != string(tyBytes(qd.Ty)) {
		sub, ok := crossTypeCompatible(qd.Ty, d.Ty)
		if !ok {
			return false, nil
		}
		qp = Prop{Binders: crossTypeRetypeBinders(qp.Binders, sub),
			Body: *crossTypeRetypeBody(&qp.Body, sub)}
	}
	aug := *d
	aug.Props = append(append([]Prop{}, d.Props...), qp)
	return true, checkDef(st, &aug)
}

// TestCrossTypeCompatibilityShape pins the RELATION independently of the prover:
// compatibility is up to PRIMITIVE LEAVES, so the leaf-sharing pattern is
// preserved even though the leaf types are not.
func TestCrossTypeCompatibilityShape(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn c-ii [] [(a Int) (b Int)] Int (+ a b))`)
	put(t, st, `(defn c-bb [] [(a Bool) (b Bool)] Bool (and a b))`)
	put(t, st, `(defn c-ib [] [(a Int) (b Int)] Bool (== a b))`)
	ii, bb, ib := mustDef(t, st, "c-ii").Ty, mustDef(t, st, "c-bb").Ty, mustDef(t, st, "c-ib").Ty
	if _, ok := crossTypeCompatible(ii, bb); !ok {
		t.Fatal("(Int,Int)->Int and (Bool,Bool)->Bool generalize alike and must be compatible")
	}
	// (Int,Int)->Int has ONE distinct leaf kind; (Int,Int)->Bool has two. Their
	// generalizations differ ([t0,t0]->t0 vs [t0,t0]->t1), so the SHARING
	// pattern — not just the arity — is what compatibility preserves.
	if _, ok := crossTypeCompatible(ii, ib); ok {
		t.Fatal("(Int,Int)->Int must not be compatible with (Int,Int)->Bool: leaf sharing differs")
	}
	// Re-typing rewrites every occurrence of the mapped kind, and only those.
	sub, ok := crossTypeCompatible(ii, bb)
	if !ok {
		t.Fatal("expected compatibility")
	}
	got := crossTypeRetypeBinders([]Ty{*tInt(), {K: "str"}}, sub)
	if got[0].K != "bool" {
		t.Fatalf("an Int binder must be re-typed to Bool, got %q", got[0].K)
	}
	if got[1].K != "str" {
		t.Fatalf("an unmapped binder kind must be left alone, got %q", got[1].K)
	}
}

// The e-graph rung: two implementations equal up to the rewrite rules
// (commutativity) share an eHash and are found equivalent — but keep DISTINCT
// identities (the layer draws an edge, it never merges objects). A
// non-commutative op (subtraction) is correctly NOT collapsed.
func TestFindEquiv(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn sab [] [(a Int) (b Int)] Int (+ a b))`)
	put(t, st, `(defn sba [] [(a Int) (b Int)] Int (+ b a))`) // commutative variant
	put(t, st, `(defn dab [] [(a Int) (b Int)] Int (- a b))`) // genuinely different (- doesn't commute)

	sab, sba, dab := mustDef(t, st, "sab"), mustDef(t, st, "sba"), mustDef(t, st, "dab")

	// Distinct IDENTITIES (different ASTs)...
	if hashDef(sab) == hashDef(sba) {
		t.Fatal("commutative variants must keep distinct identities")
	}
	// ...but the same EQUIVALENCE key.
	if eHash(st, sab) != eHash(st, sba) {
		t.Fatal("commutative variants should share an eHash")
	}
	// A non-commutative body is not collapsed.
	if eHash(st, sab) == eHash(st, dab) {
		t.Fatal("subtraction is not commutative — must not share an eHash with +")
	}

	out, err := apiFindEquiv(st, "sab")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sba") {
		t.Fatalf("find --equiv sab should surface sba:\n%s", out)
	}
	if strings.Contains(out, "dab") {
		t.Fatalf("find --equiv sab must NOT surface dab:\n%s", out)
	}
}

// Type-directed associativity: over Int, a+b+c collapses to one form regardless
// of association/order; over Float it must NOT (float addition is not
// associative), so associativity is applied only where the operand type admits.
func TestFindEquivTypeDirectedAssoc(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn il [] [(a Int) (b Int) (c Int)] Int (+ (+ a b) c))`)
	put(t, st, `(defn ir [] [(a Int) (b Int) (c Int)] Int (+ a (+ b c)))`)
	put(t, st, `(defn fl [] [(a Float) (b Float) (c Float)] Float (+ (+ a b) c))`)
	put(t, st, `(defn fr [] [(a Float) (b Float) (c Float)] Float (+ a (+ b c)))`)

	il, ir := mustDef(t, st, "il"), mustDef(t, st, "ir")
	fl, fr := mustDef(t, st, "fl"), mustDef(t, st, "fr")

	if eHash(st, il) != eHash(st, ir) {
		t.Fatal("Int associativity variants should share an eHash")
	}
	if eHash(st, fl) == eHash(st, fr) {
		t.Fatal("Float associativity is unsound — must NOT collapse")
	}
	out, err := apiFindEquiv(st, "il")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ir") {
		t.Fatalf("find --equiv il should surface ir (Int assoc):\n%s", out)
	}
	if out2, _ := apiFindEquiv(st, "fl"); strings.Contains(out2, "fr") {
		t.Fatalf("find --equiv fl must NOT surface fr (float doesn't associate):\n%s", out2)
	}
}

// UNIT, IDEMPOTENCE AND INVOLUTION RULES. A body carrying an identity element
// (`+ 0`, `* 1`, `and true`, `or false`), a duplicated Bool operand, or a
// doubled `neg` belongs in the same equivalence class as the body it is equal
// to, and this pins both halves of that: which bodies collapse, and which
// deliberately do not.
//
// THE MUTATION THIS KILLS — two of them, in opposite directions, which is why
// the table carries controls rather than only collapses:
//
//   - DISABLED REWRITE: delete the unit/idempotence/involution pass from
//     eNormalize, or gate its entry on a condition that is never true (e.g.
//     `if false`, an empty identity-element table, or a `switch` whose default
//     returns the term untouched). Every collapse row goes red.
//   - TYPE-BLIND OR CONSTANT-BLIND REWRITE: apply `+ 0` to Float, or reduce
//     `and x c` / `* x c` for ANY constant c rather than only the identity
//     element. Both mutants pass every collapse row and are caught only by the
//     `wantEquiv: false` controls.
//
// The soundness side-conditions, since two of these are easy to get wrong:
// Float `* 1.0` IS sound (x*1.0 is x for every IEEE x, including ±0.0, ±inf,
// and — under the kernel's single canonical NaN — NaN), while Float `+ 0.0` is
// NOT: -0.0 + 0.0 is +0.0, a distinct value under the kernel's Leibniz `==`.
// Double `neg` is sound over Float for the same canonical-NaN reason.
func TestFindEquivUnitAndIdempotenceRules(t *testing.T) {
	cases := []struct {
		name      string
		lhs, rhs  string // two defn bodies with the SAME signature
		wantEquiv bool
		why       string
	}{
		// --- additive/multiplicative units -------------------------------
		{"int-add-zero", `[(a Int)] Int (+ a 0)`, `[(a Int)] Int a`, true,
			"0 is the additive identity over Int"},
		{"int-mul-one", `[(a Int)] Int (* a 1)`, `[(a Int)] Int a`, true,
			"1 is the multiplicative identity over Int"},
		{"rat-add-zero", `[(a Rat)] Rat (+ a 0/1)`, `[(a Rat)] Rat a`, true,
			"0 is the additive identity over Rat"},
		{"rat-mul-one", `[(a Rat)] Rat (* a 1/1)`, `[(a Rat)] Rat a`, true,
			"1 is the multiplicative identity over Rat"},
		{"float-mul-one", `[(a Float)] Float (* a 1.0f)`, `[(a Float)] Float a`, true,
			"x * 1.0 is x for every IEEE value, canonical NaN included"},

		// --- Bool units and idempotence ----------------------------------
		{"bool-and-true", `[(a Bool)] Bool (and a true)`, `[(a Bool)] Bool a`, true,
			"true is the identity of non-short-circuiting and"},
		{"bool-or-false", `[(a Bool)] Bool (or a false)`, `[(a Bool)] Bool a`, true,
			"false is the identity of non-short-circuiting or"},
		{"bool-and-idem", `[(a Bool)] Bool (and a a)`, `[(a Bool)] Bool a`, true,
			"and is idempotent"},
		{"bool-or-idem", `[(a Bool)] Bool (or a a)`, `[(a Bool)] Bool a`, true,
			"or is idempotent"},

		// --- involution --------------------------------------------------
		{"int-double-neg", `[(a Int)] Int (neg (neg a))`, `[(a Int)] Int a`, true,
			"neg is an involution over Int"},
		{"rat-double-neg", `[(a Rat)] Rat (neg (neg a))`, `[(a Rat)] Rat a`, true,
			"neg is an involution over Rat"},
		{"float-double-neg", `[(a Float)] Float (neg (neg a))`, `[(a Float)] Float a`, true,
			"sign flipped twice is the same bits; NaN is canonical"},

		// --- CONTROLS: unsound collapses that must NOT happen ------------
		{"float-add-zero-control", `[(a Float)] Float (+ a 0.0f)`, `[(a Float)] Float a`, false,
			"-0.0 + 0.0 is +0.0, so 0.0 is NOT a Float additive identity"},
		{"int-add-idem-control", `[(a Int)] Int (+ a a)`, `[(a Int)] Int a`, false,
			"+ is not idempotent — a+a is 2a"},
		{"int-mul-idem-control", `[(a Int)] Int (* a a)`, `[(a Int)] Int a`, false,
			"* is not idempotent — a*a is a squared"},
		{"rat-mul-idem-control", `[(a Rat)] Rat (* a a)`, `[(a Rat)] Rat a`, false,
			"* is not idempotent over Rat either"},
		{"bool-and-false-control", `[(a Bool)] Bool (and a false)`, `[(a Bool)] Bool a`, false,
			"false is and's ANNIHILATOR, not its identity — a wrong-constant mutant"},
		{"int-mul-zero-control", `[(a Int)] Int (* a 0)`, `[(a Int)] Int a`, false,
			"0 is *'s annihilator, not its identity — a wrong-constant mutant"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			put(t, st, "(defn lhs [] "+tc.lhs+")")
			put(t, st, "(defn rhs [] "+tc.rhs+")")
			l, r := mustDef(t, st, "lhs"), mustDef(t, st, "rhs")

			// CONTROL FOR THE MEASUREMENT ITSELF: the two bodies must have
			// distinct IDENTITIES, or an eHash comparison is vacuous — it
			// would pass for a collapse row even with every rewrite deleted.
			if hashDef(l) == hashDef(r) {
				t.Fatalf("%s: the two bodies already share an identity — the eHash assertion below proves nothing", tc.name)
			}

			got := eHash(st, l) == eHash(st, r)
			if got != tc.wantEquiv {
				verb := "should collapse"
				if !tc.wantEquiv {
					verb = "must NOT collapse"
				}
				t.Fatalf("%s: eHash equal = %v, want %v — %s (%s)\n  lhs: %s\n  rhs: %s",
					tc.name, got, tc.wantEquiv, verb, tc.why, tc.lhs, tc.rhs)
			}
		})
	}
}

// CONTROL FLOW AND BINDING RULES: identical `if` branches, a constant `if`
// condition, and eta.
//
// THESE SIDE CONDITIONS ARE OF A DIFFERENT KIND FROM THE UNIT RULES ABOVE, and
// saying so is the point of putting them in a separate table. `+ 0` is excluded
// for Float because of what Float ARITHMETIC does — the restriction is a fact
// about one type's algebra. Nothing below is. Each rule here is restricted by
// Oath's STRICT EVALUATION ORDER (eval.go's `if` evaluates its condition before
// selecting a branch; an application evaluates its head) and by BINDING
// STRUCTURE (de Bruijn indices shift when a binder is removed). Both hold at
// EVERY well-formed type, so the Float rows below are representative rather than
// special, and a future numeric type would need no new carve-out.
//
// THE MUTATION THIS KILLS — again in two directions:
//
//   - DISABLED REWRITE: remove the `if` or `lam` exit phase from eNormalize.
//     Every collapse row goes red.
//   - UNRESTRICTED REWRITE: `(if c x x)` → `x` for ANY condition, or eta for any
//     head, or eta without shifting the free indices of the head. Each passes
//     every collapse row and is caught only by a control.
//
// WHY EACH RESTRICTION IS REAL, since "the two branches are the same" and "the
// argument is applied straight through" both look unconditional:
//
//   - `(if c x x)` may drop `c` only when `c` is ALREADY A VALUE — a `var`
//     (bound to a value under call-by-value) or a Bool literal. If `c` is a
//     computation, evaluating it is part of what the term does: dropping a
//     divergent condition turns a non-terminating term into a terminating one,
//     which is REMOVING divergence, not preserving meaning. The branches
//     themselves are unconstrained — exactly one is ever evaluated — which is
//     why one collapse row below has divergent branches on purpose.
//
//   - `fn [(x A)] (f x)` → `f` needs `f` to be a SYNTACTIC VALUE, because the
//     left side is a value whatever `f` is, while the right side evaluates `f`
//     immediately. An `f` that diverges would MOVE divergence from application
//     time to construction time. A bare `ref` is NOT a value by kind —
//     eval.go's `ref` case evaluates the referenced definition's body — but it
//     IS one when a store lookup shows that body begins with `lam`, because
//     evaluating it then just builds a closure. `boom` below is the control for
//     the other case: nullary, body `self`, so its ref diverges.
//
//     THE ADMITTED HEADS ARE `var` AND `ref`, AND THE BOUNDARY IS A COST ONE AS
//     MUCH AS A SEMANTIC ONE. A `lam` head is equally sound and is DELIBERATELY
//     DEFERRED: the binder-shift below must walk the whole head, and a lam head
//     contains arbitrarily much of the term, so a nested tower
//     `fn x. ((fn y. …) x)` costs O(n²) — measured at 11ms/26ms/109ms/459ms for
//     n = 500/1000/2000/4000, which extrapolates to minutes at the profile's
//     admitted node count, reachable from `find --equiv`. `var` and `ref` heads
//     are single nodes, so their check and shift are O(1) and no tower shape can
//     accumulate. RE-ADMITTING `lam` REQUIRES MAKING THE FREE-OCCURRENCE TEST
//     AND THE SHIFT O(1) FIRST (a min-free-index attribute computed during
//     normalization, and shifts accumulated through a chain rather than applied
//     per level) — not merely deciding the rewrite is sound, which it already
//     is.
//
//   - and `x` must not be free in `f`, which is not about evaluation at all: the
//     rewrite deletes a binder, so every free index in `f` shifts. If `x` occurs
//     free there is no index to shift it to, and the naive implementation —
//     leave every index alone — silently repoints that occurrence at whatever
//     encloses the lambda. The control below pins exactly that output.
//     ONE VARIANT IS DELIBERATELY NOT PINNED, because it is not wrong: shifting
//     the occurrence DOWN as well lands it on the head's own outermost binder,
//     which is beta-reduction at a value argument and agrees with the original
//     on every input. Mutating the implementation that way kills nothing here,
//     and should not — a control that forbade it would be pinning an
//     incompleteness rather than a defect.
func TestFindEquivControlFlowAndEtaRules(t *testing.T) {
	cases := []struct {
		name      string
		lhs, rhs  string
		wantEquiv bool
		why       string
	}{
		// --- identical branches, condition already a value ----------------
		{"if-same-var-int", `[(c Bool) (x Int)] Int (if c x x)`, `[(c Bool) (x Int)] Int x`, true,
			"a var is a value under call-by-value, so dropping the condition drops no work"},
		{"if-same-var-bool", `[(c Bool) (x Bool)] Bool (if c x x)`, `[(c Bool) (x Bool)] Bool x`, true,
			"the result type is irrelevant to this rule"},
		{"if-same-var-float", `[(c Bool) (x Float)] Float (if c x x)`, `[(c Bool) (x Float)] Float x`, true,
			"Float is not a special case here — no arithmetic is involved"},
		{"if-same-var-rat", `[(c Bool) (x Rat)] Rat (if c x x)`, `[(c Bool) (x Rat)] Rat x`, true,
			"nor is Rat"},
		{"if-same-var-fun", `[(c Bool) (x (-> Int Int))] (-> Int Int) (if c x x)`,
			`[(c Bool) (x (-> Int Int))] (-> Int Int) x`, true,
			"a function-typed result too: the rule quantifies over every well-formed type"},
		{"if-same-lit-false", `[(x Int)] Int (if false x x)`, `[(x Int)] Int x`, true,
			"a Bool literal is a value"},

		// --- constant condition -------------------------------------------
		{"if-const-true", `[(x Int) (y Int)] Int (if true x y)`, `[(x Int) (y Int)] Int x`, true,
			"true selects the then-branch; the else-branch was never going to be evaluated"},
		{"if-const-false", `[(x Int) (y Int)] Int (if false x y)`, `[(x Int) (y Int)] Int y`, true,
			"false selects the else-branch"},
		{"if-const-divergent-branches", `[(n Int)] Int (if true (spin-b-i n) (dbl n))`,
			`[(n Int)] Int (spin-b-i n)`, true,
			"THE BRANCHES NEED NOT BE VALUES: exactly one is evaluated either way, so a " +
				"non-total live branch is preserved and a dead branch is discarded unevaluated"},

		// --- eta ----------------------------------------------------------
		{"eta-outer-var-head", `[(g (-> Int Int))] (-> Int Int) (fn [(x Int)] (g x))`,
			`[(g (-> Int Int))] (-> Int Int) g`, true,
			"THE REINDEXING WITNESS: g is var1 inside the lambda and must become var0 once " +
				"the lambda is removed — an implementation that forgets the shift produces " +
				"different bytes and this row stays red"},
		{"eta-ref-head-total", `[] (-> Int Int) (fn [(x Int)] (dbl x))`, `[] (-> Int Int) dbl`, true,
			"dbl's stored body BEGINS WITH lam, so evaluating the ref immediately produces a " +
				"closure and does no work — the store lookup is what makes this head a value. " +
				"A ref also carries no term de Bruijn index, so the shift is vacuous here"},

		// --- CONTROLS: rewrites that would remove or move divergence -------
		{"eta-free-x-control", `[(n Int)] (-> Int Int) (fn [(x Int)] ((fn [(y Int)] (+ y x)) x))`,
			`[(n Int)] (-> Int Int) (fn [(y Int)] (+ y n))`, false,
			"x IS free in the head, and the rhs is precisely what an unshifted rewrite emits — " +
				"the x occurrence silently repointed at the enclosing parameter n"},
		{"eta-nontotal-app-head-control", `[(n Int)] (-> Int Int) (fn [(x Int)] ((pick n) x))`,
			`[(n Int)] (-> Int Int) (pick n)`, false,
			"the head is an APPLICATION of a non-total definition: the lhs is a value, the rhs " +
				"diverges on construction — eta would MOVE divergence"},
		{"eta-nontotal-ref-head-control", `[] (-> Int Int) (fn [(x Int)] (boom x))`,
			`[] (-> Int Int) boom`, false,
			"a bare ref is not a value: boom is nullary, so evaluating the ref evaluates its " +
				"body and diverges"},
		{"if-nontotal-cond-control", `[(n Int) (x Int)] Int (if (spin-b n) x x)`,
			`[(n Int) (x Int)] Int x`, false,
			"identical branches, but the condition is a non-total call — dropping it would " +
				"REMOVE divergence"},
	}

	// One store for the whole table: the setup definitions the controls need are
	// stored objects, and `boom` in particular has to be a real store entry for
	// "evaluating this ref runs a non-total body" to be a fact about the store
	// rather than about the test's imagination.
	st := newStore(t)
	put(t, st, `(defn dbl [] [(k Int)] Int (+ k k))`)
	put(t, st, `(defn spin-b [] [(n Int)] Bool (spin-b n))`)    // non-total, Bool-valued
	put(t, st, `(defn spin-b-i [] [(n Int)] Int (spin-b-i n))`) // non-total, Int-valued
	put(t, st, `(defn pick [] [(n Int)] (-> Int Int) (pick n))`)
	put(t, st, `(defn boom [] [] (-> Int Int) (boom))`) // nullary: its body is `self`, not a lam

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, rn := "l-"+tc.name, "r-"+tc.name
			put(t, st, "(defn "+ln+" [] "+tc.lhs+")")
			put(t, st, "(defn "+rn+" [] "+tc.rhs+")")
			l, r := mustDef(t, st, ln), mustDef(t, st, rn)

			if hashDef(l) == hashDef(r) {
				t.Fatalf("%s: the two bodies already share an identity — the eHash assertion below proves nothing", tc.name)
			}
			got := eHash(st, l) == eHash(st, r)
			if got != tc.wantEquiv {
				verb := "should collapse"
				if !tc.wantEquiv {
					verb = "must NOT collapse"
				}
				t.Fatalf("%s: eHash equal = %v, want %v — %s (%s)\n  lhs: %s\n  rhs: %s",
					tc.name, got, tc.wantEquiv, verb, tc.why, tc.lhs, tc.rhs)
			}
		})
	}
}

func mustDef(t *testing.T, st *Store, name string) *Def {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Fatalf("%s not in store", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
