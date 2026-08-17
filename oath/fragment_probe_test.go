package main

// A READ-ONLY probe of which corpus goals the prover can TRANSLATE at all —
// the "provable fragment" boundary, measured rather than inferred.
//
// WHY THIS EXISTS (#177). `oath find --implies` appends the CALLER'S query
// property to a candidate and proves that, with `self` bound to the candidate
// (api.go:1195-1256). A goal the translator cannot build never reaches a solver,
// so the mode returns NO VERDICT — indistinguishable, to the caller, from the
// artifact not existing. #177 asks how big that blind spot is. Answering needs
// the SET, and the set has to come from the prover, not from a reading of the
// source.
//
// TWO MEASUREMENTS, and they are not the same set. Per PROPERTY: can the prover
// build a goal from this definition's own law at all — the quantity
// docs/experiments/issue-68.md §6 counts. Per DEFINITION: can ANY query
// mentioning this definition be translated — the `--implies` criterion, measured
// by selfMentionProps below. A definition with an untranslatable law can still be
// reachable, and one with no laws at all can be unreachable.
//
// THE INSTRUMENT IS THE PROVER'S OWN ENUMERATION SEAM, not a parallel analysis
// of term kinds. `smtCtx.enumerate` walks the real strategy sequence and records
// every script it can emit without invoking z3 (TestEnumerationRunsNoSolver is
// the control on that). A goal that emits ZERO scripts bailed in translation
// before the first solver call. That is the measurement; the bail REASON is
// `proveOneInner`'s own returned detail, which is the translator's error string.
//
// WHY NOT `fixtures/prove/attempts.txt`. The committed fixture records a row
// only where a script exists, so "no rows" spells both "the goal bailed" and
// "nobody looked" — scripts/prove-reasons.py says so at the place where its
// fourth control would have gone, and names the enumerating producer as the
// thing that closes it. This is that producer, run live. The fixture is still
// read here, but as a RECONCILIATION target: a disagreement is a fact about the
// fixture's freshness, reported by the consumer rather than swallowed.
//
// THE UNIVERSE IS THE LIVE STORE'S. `codebase/names.json` is the authority on
// what the corpus OFFERS; `codebase/meta/` is a walk of the store's HISTORY and
// would answer a different question while looking identical. Translation is a
// fact about the DEF, hence about the HASH, so the probe is keyed by hash and
// the per-name view is derived by expanding aliases — never the other way round.
//
// NOTHING HERE WRITES. The store is opened, read and closed; no solver runs; no
// metadata is set. `git status codebase/` is clean before and after.
//
// Running it:
//
//	cd oath && OATH_FRAGMENT_OUT=/tmp/fragment.json go test -run TestFragmentReach -count=1
//
// It SKIPS when OATH_FRAGMENT_OUT is unset. It is a measurement, not an
// invariant, and enumerating the whole corpus twice (once through the kernel's
// own scriptAttempts, once through the outcome-carrying path this file needs)
// is not a cost every `go test ./...` should pay.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
)

// fragmentProp is one property of one OBJECT. Translatability is a fact about
// the hash, so this is recorded per-object and the per-name view is derived.
type fragmentProp struct {
	Index int `json:"index"`
	// Name as the object's canonical metadata spells it. A DISPLAY label:
	// aliases spell property names differently and the join must not key on it.
	Name string `json:"name"`
	// Proven is the store's recorded verdict. It is carried so a consumer can
	// run the proven-implies-translatable control without a second artefact.
	Proven bool `json:"proven"`
	// Scripts is how many scripts the strategy sequence can emit for this goal
	// under the recorded lemma state. Zero means the translator bailed.
	Scripts int `json:"scripts"`
	// Bail is true iff Scripts == 0.
	Bail bool `json:"bail"`
	// BailReason is proveOneInner's own returned detail for a bailing goal —
	// the translator's error text, verbatim, so a consumer can tag the cause
	// (an unsupported term form, an opaque primitive, a partial application, a
	// carrier the translator cannot encode) rather than guess at it. Empty for
	// a goal that emitted at least one script.
	BailReason string `json:"bail_reason,omitempty"`
	// BailStatus is that outcome's status field, recorded so a consumer can see
	// whether the reason really came from a translation failure ("unknown")
	// rather than from some other early return.
	BailStatus string `json:"bail_status,omitempty"`
}

// fragmentObject is one live object: one hash, every live name that resolves to
// it, the per-property translatability verdicts, and the SYNTHETIC-QUERY probe
// described at selfMentionProps below.
type fragmentObject struct {
	Hash          string         `json:"hash"`
	CanonicalName string         `json:"canonical_name"`
	LiveNames     []string       `json:"live_names"`
	DefKind       string         `json:"def_kind"` // Def.K as stored: func | data | ...
	PropCount     int            `json:"prop_count"`
	Level         string         `json:"level"`
	Props         []fragmentProp `json:"props"`

	// Polymorphic records TyVars > 0. It qualifies a query-probe BAIL and
	// nothing else: the probe instantiates type parameters concretely, so a bail
	// under that instantiation does not establish that every instantiation bails.
	Polymorphic bool `json:"polymorphic"`
	// Query is the synthetic-query verdict: reachable | bail | not-probed.
	Query string `json:"query"`
	// QueryScripts is how many scripts the sequence emits for the synthetic goal.
	QueryScripts int `json:"query_scripts"`
	// QueryReason is the translator's refusal when Query is `bail`, or the
	// reason the probe could not be built when Query is `not-probed`. A
	// not-probed object is REPORTED, never silently dropped: it is an object the
	// instrument did not measure, which is a different thing from a reachable
	// one and must not be counted as either.
	QueryReason string `json:"query_reason,omitempty"`
}

type fragmentReport struct {
	// Store is the path the measurement was taken against, so a consumer cannot
	// silently join a probe of one corpus onto a census of another.
	Store string `json:"store"`
	// LiveNames and LiveObjects are OUTPUT, never expectation. Pinning either
	// here would duplicate an authority that codebase/names.json already holds.
	LiveNames   int              `json:"live_names"`
	LiveObjects int              `json:"live_objects"`
	Objects     []fragmentObject `json:"objects"`
}

// enumerateWithOutcome runs the prover's strategy sequence under enumeration and
// returns BOTH the scripts it emitted and the outcome it returned.
//
// scriptAttempts (prove.go) is the kernel's own seam and returns only the
// scripts; the bail REASON lives in the outcome it discards. Rather than change
// the kernel's signature for a diagnostic, this repeats the four lines and the
// caller asserts, for EVERY property in the corpus, that the two agree on the
// script count. A drift between them therefore fails the probe instead of
// silently producing a different population — which is the only reason a second
// copy of a code path is admissible here.
func enumerateWithOutcome(st *Store, h string, pi int) (int, propOutcome, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return 0, propOutcome{}, err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return 0, propOutcome{}, err
	}
	if pi < 0 || pi >= len(d.Props) {
		return 0, propOutcome{}, fmt.Errorf("no property %d", pi)
	}
	c := newSmtCtx(st, d, h)
	c.enumerate = true
	loadLemmaLibrary(c, st, d, h, m, pi)
	o := c.proveOneInner(d, h, m, &d.Props[pi], pi)
	return len(c.attempts), o, nil
}

// selfMentionProp builds the minimal query property that MENTIONS a candidate.
//
// WHY A SYNTHETIC PROPERTY AND NOT THE CANDIDATE'S OWN. `apiFindImplies` does
// not prove a candidate's stored properties. It APPENDS the caller's query
// property to the candidate and proves THAT — `aug.Props = append(d.Props, qp)`,
// then `proveOne(&aug, h, m, &aug.Props[pi], pi)` (api.go:1195-1256) — with
// `self` bound to the candidate. So a candidate whose own property is
// untranslatable is not thereby unreachable: a different query might translate.
// Deriving the blind spot from the stored properties would be measuring the
// implementation's decomposition instead of the claim.
//
// What DOES put a candidate beyond every query is its own BODY. Any query
// property that mentions the candidate inlines that body through `call`, so if
// the body cannot be translated, no mentioning query can be. The property built
// here is the minimal mentioning query — `(== (self b…) (self b…))` over the
// candidate's own parameter types. It is trivially TRUE, and that is the point:
// it carries no information except whether a goal can be BUILT at all.
//
// A query that does NOT mention the candidate would translate for anything and
// would also discriminate nothing, so it is not the relevant existential.
//
// POLYMORPHIC CANDIDATES ARE INSTANTIATED AT `Int`, and the asymmetry is
// recorded rather than smoothed over: the claim is existential ("some query
// reaches it"), so a translating instantiation is a POSITIVE WITNESS, while a
// bail under one instantiation does not establish that all of them bail. The
// caller marks such objects and the consumer qualifies them.
// TWO FORMS, because the obvious one is not total. `(== (self …) (self …))`
// requires the candidate's RETURN TYPE to be equatable, and three definitions in
// this corpus return a capability record, where `==` is not defined — so an
// equality-only probe would leave them unmeasured while looking complete.
//
// The primary form is therefore `(let [x (self b…)] true)`, which mentions the
// candidate at ANY return type. It works because `tr` translates a `let`'s bound
// value EAGERLY, before the body and regardless of whether the body uses it
// (prove.go:763-768) — so the self-call is forced even though the property is
// `true`. That is a fact about the translator, checkable at that line, and it is
// the whole load-bearing assumption of this probe.
//
// The equality form is kept as a CROSS-CHECK wherever it typechecks: the two
// must agree, or one of them is not measuring what it claims. That covers all
// but the three records.
func selfMentionProps(d *Def) (letForm Prop, eqForm *Prop, why string) {
	if d.K != "func" || d.Ty == nil {
		return Prop{}, nil, "not a function definition"
	}
	args := make([]Ty, d.TyVars)
	for i := range args {
		args[i] = *tInt()
	}
	ty := substTy(d.Ty, args)
	var params []Ty
	for ty.K == "fun" {
		params = append(params, *ty.A)
		ty = ty.B
	}
	head := &Term{K: "self"}
	if d.TyVars > 0 {
		head.TyArgs = args
	}
	// Curried application, and the de Bruijn direction is the part to get right:
	// formulaWith pushes binders in order and `tr` reads them as
	// env[len(env)-1-Idx], so binder i is Idx len(params)-1-i.
	call := head
	for i := range params {
		call = &Term{K: "app", A: call, B: &Term{K: "var", Idx: len(params) - 1 - i}}
	}
	letForm = Prop{Binders: params, Body: Term{
		K: "let", Ty: ty, A: call, B: &Term{K: "bool", Bool: true}}}
	eq := Prop{Binders: params, Body: Term{K: "prim", Op: "==", Args: []Term{*call, *call}}}
	return letForm, &eq, ""
}

// mentionsSelf reports whether a term reaches the enclosing definition. Only a
// property that does is evidence about the definition's BODY: one that never
// mentions it translates without the body being reached.
//
// IT WALKS EVERY CHILD FIELD BY REFLECTION, NOT BY NAME. The hand-written
// version listed A, B, C and Args — the fields its author remembered — and
// silently missed `Arms`, so a `self` reachable only through a `match` arm read
// as absent and the control that depends on this silently did not run. A walker
// derived from the fields in mind encodes the shapes in mind; one derived from
// the TYPE covers a field added later without anyone remembering to come back.
// TestMentionsSelfCoversEveryChildField pins that.
func mentionsSelf(t *Term) bool {
	if t == nil {
		return false
	}
	if t.K == "self" {
		return true
	}
	found := false
	forEachChildTerm(t, func(k *Term) {
		if !found && mentionsSelf(k) {
			found = true
		}
	})
	return found
}

// forEachChildTerm visits every *Term and every element of every []Term field of
// t, whatever those fields are called.
func forEachChildTerm(t *Term, f func(*Term)) {
	v := reflect.ValueOf(t).Elem()
	for i := 0; i < v.NumField(); i++ {
		switch fv := v.Field(i); fv.Type() {
		case reflect.TypeOf((*Term)(nil)):
			if !fv.IsNil() {
				f(fv.Interface().(*Term))
			}
		case reflect.TypeOf([]Term(nil)):
			for j := 0; j < fv.Len(); j++ {
				f(fv.Index(j).Addr().Interface().(*Term))
			}
		}
	}
}

// TestMentionsSelfCoversEveryChildField is the control on the walker, and it is
// NOT gated on OATH_FRAGMENT_OUT: it is an invariant about the AST, cheap, and
// the thing that must not rot when Term gains a field.
//
// It plants a `self` in EVERY child-bearing field, one at a time, and requires
// each to be found — so a walker that skips a field fails here rather than
// silently disabling a control elsewhere.
func TestMentionsSelfCoversEveryChildField(t *testing.T) {
	if mentionsSelf(nil) || mentionsSelf(&Term{K: "var"}) {
		t.Fatal("a term with no self must report false")
	}
	tt := reflect.TypeOf(Term{})
	planted := 0
	for i := 0; i < tt.NumField(); i++ {
		f := tt.Field(i)
		probe := &Term{K: "app"}
		v := reflect.ValueOf(probe).Elem().Field(i)
		switch f.Type {
		case reflect.TypeOf((*Term)(nil)):
			v.Set(reflect.ValueOf(&Term{K: "self"}))
		case reflect.TypeOf([]Term(nil)):
			v.Set(reflect.ValueOf([]Term{{K: "self"}}))
		default:
			continue
		}
		planted++
		if !mentionsSelf(probe) {
			t.Fatalf("a `self` planted in Term.%s is not found: the walker skips that field, "+
				"so any control resting on it silently does not run", f.Name)
		}
	}
	// The walk must have had something to walk. A Term that stopped carrying
	// child terms would make every assertion above vacuous.
	if planted == 0 {
		t.Fatal("no child-term field found on Term: this control measured nothing")
	}
	t.Logf("mentionsSelf covers %d child-bearing fields of Term", planted)
}

// runQuery augments the candidate with one synthetic property and enumerates,
// through the same augmentation, gate and context construction `apiFindImplies`
// uses (api.go:1195-1256) — stopping short of the solver.
func runQuery(st *Store, h string, d *Def, m *Meta, qp Prop) (string, int, string) {
	aug := *d
	aug.Props = append(append([]Prop{}, d.Props...), qp)
	// The SAME unconditional gate apiFindImplies applies (api.go:1198). A
	// candidate the checker rejects never reaches the prover there, so an
	// augmentation this instrument cannot typecheck is one it has not measured —
	// reported as not-probed rather than counted on either side.
	if err := checkDef(st, &aug); err != nil {
		return "not-probed", 0, "the synthetic query does not typecheck against the candidate: " + err.Error()
	}
	pi := len(d.Props)
	c := newSmtCtx(st, &aug, h)
	c.enumerate = true
	// -1, as apiFindImplies passes: the goal is a synthetic property past the
	// definition's own props, so author hints keyed by real prop index must not
	// reach it.
	loadLemmaLibrary(c, st, &aug, h, m, -1)
	o := c.proveOneInner(&aug, h, m, &aug.Props[pi], pi)
	if len(c.attempts) == 0 {
		// Same discrimination as the per-property path: only `unknown` is a
		// translation bail. Anything else is an early return this probe has no
		// reading for, and it is surfaced as not-probed rather than folded into
		// the blind spot.
		if o.status != "unknown" {
			return "not-probed", 0, fmt.Sprintf(
				"the synthetic query emitted no script with status %q (%q), which is not a translation bail",
				o.status, o.detail)
		}
		return "bail", 0, o.detail
	}
	return "reachable", len(c.attempts), ""
}

// queryProbe returns the primary verdict and, separately, the cross-check
// verdict from the equality form (empty when that form does not typecheck).
func queryProbe(st *Store, h string, d *Def, m *Meta) (verdict string, scripts int, reason, cross string) {
	letForm, eqForm, why := selfMentionProps(d)
	if why != "" {
		return "not-probed", 0, why, ""
	}
	verdict, scripts, reason = runQuery(st, h, d, m, letForm)
	if eqForm != nil {
		cross, _, _ = runQuery(st, h, d, m, *eqForm)
	}
	return verdict, scripts, reason, cross
}

func TestFragmentReach(t *testing.T) {
	out := os.Getenv("OATH_FRAGMENT_OUT")
	if out == "" {
		t.Skip("set OATH_FRAGMENT_OUT to a path to emit the fragment-reach probe")
	}

	// THE FAULT-INJECTION SEAMS MUST BE OFF, and this refuses rather than warns.
	// `proveOneInner` honours OATH_PROVE_FORCE_ABORT / _ONCE by returning BEFORE
	// translation (prove.go:1489-1496), which produces zero attempts — exactly
	// the shape this probe reads as "outside the provable fragment". A run under
	// either variable would silently move candidates into the blind spot and
	// still report PASS. The status assertion below catches the same thing from
	// the other side; this closes it at the source, which is where the whole
	// class dies rather than the one symptom.
	for _, v := range []string{"OATH_PROVE_FORCE_ABORT", "OATH_PROVE_FORCE_ABORT_ONCE"} {
		if os.Getenv(v) != "" {
			t.Fatalf("%s is set: it makes the prover return before translation, which this "+
				"probe cannot distinguish from a translation bail. Unset it and re-run.", v)
		}
	}

	// The FILESYSTEM backend explicitly, for the reason corpus_census_test.go
	// gives: OpenStore consults OATH_BACKEND and, when it is `cloud`, ignores
	// the path it was handed. The claim is about the committed store at this
	// path, so the authority is pinned to the claim rather than to the
	// environment.
	const storePath = "../codebase"
	be, err := openFSBackend(storePath)
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, storePath)
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}

	// --- the universe, derived from codebase/names.json ----------------------
	live := st.Names()
	if len(live) == 0 {
		t.Fatal("codebase/names.json resolved to no live names: this probe measured nothing")
	}
	byHash := map[string][]string{}
	for n, h := range live {
		byHash[h] = append(byHash[h], n)
	}
	hashes := make([]string, 0, len(byHash))
	for h := range byHash {
		sort.Strings(byHash[h])
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	rep := fragmentReport{Store: storePath, LiveNames: len(live), LiveObjects: len(hashes)}

	// CONTROL — the probe must observe at least one goal on each side of the
	// boundary. An instrument that finds every goal translatable, or none, is
	// far more likely to be broken than to be reporting a corpus that changed
	// that much; either way the number below would be uninterpretable.
	totalProps, bails, translated := 0, 0, 0

	for _, h := range hashes {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s (%v): reading the def: %v", byHash[h][0], h[:8], err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s (%v): reading the meta: %v", byHash[h][0], h[:8], err)
		}
		obj := fragmentObject{
			Hash:          h,
			CanonicalName: m.Name,
			LiveNames:     byHash[h],
			DefKind:       d.K,
			PropCount:     len(d.Props),
			Level:         m.Guarantee.Level,
		}
		// ProvenProps is the per-index record; Guarantee.Proven is a stored
		// COUNT of the same fact. Read the indices — the count cannot say WHICH
		// property is proven, and the control below is per property.
		provenSet := map[int]bool{}
		for _, i := range m.ProvenProps {
			provenSet[i] = true
		}
		for pi := range d.Props {
			totalProps++

			// The kernel's own seam, first — it is the authority on how many
			// scripts the sequence emits.
			ats, err := scriptAttempts(st, h, pi)
			if err != nil {
				t.Fatalf("%s[%d]: scriptAttempts: %v", m.Name, pi, err)
			}
			// The outcome-carrying path, second. Its script count must match,
			// or the two have drifted and neither number means what it says.
			n, o, err := enumerateWithOutcome(st, h, pi)
			if err != nil {
				t.Fatalf("%s[%d]: enumerateWithOutcome: %v", m.Name, pi, err)
			}
			if n != len(ats) {
				t.Fatalf("%s[%d]: the probe's enumeration emitted %d scripts and the kernel's "+
					"scriptAttempts emitted %d — the two paths have drifted, so the bail set "+
					"this probe reports is not the prover's", m.Name, pi, n, len(ats))
			}

			p := fragmentProp{
				Index:   pi,
				Name:    metaPropName(m, pi),
				Proven:  provenSet[pi],
				Scripts: n,
				Bail:    n == 0,
			}
			if p.Bail {
				bails++
				p.BailReason = o.detail
				p.BailStatus = o.status
				// A bail with no reason is an instrument failure, not a
				// finding: the whole point of taking the outcome is that the
				// translator names its own refusal. Reporting an empty cause
				// would hand the consumer a row it cannot classify.
				if o.detail == "" {
					t.Fatalf("%s[%d] emitted no script yet the prover returned no reason "+
						"(status %q) — the probe cannot say why this goal is outside the "+
						"fragment", m.Name, pi, o.status)
				}
				// A TRANSLATION BAIL IS `unknown`, and nothing else is.
				// proveOneInner converts a translator error to
				// propOutcome{status:"unknown", detail: err.Error()}
				// (prove.go:1515, :1525); its other zero-attempt exits report
				// `invalidated`. Accepting any status would let a
				// non-translation early return be counted as a fragment fact.
				if o.status != "unknown" {
					t.Fatalf("%s[%d] emitted no script with status %q (%q) — a translation "+
						"bail is reported as `unknown`, so this exit is not one and must "+
						"not be counted as being outside the fragment", m.Name, pi, o.status, o.detail)
				}
			} else {
				translated++
			}

			// CONTROL — a PROVEN property necessarily reached the solver, so a
			// proven property that emits no script means this probe is
			// measuring something other than translatability. This is the live
			// counterpart of scripts/prove-reasons.py's control 3, and it is
			// stronger here: that one can only check the fixture against
			// itself, while this checks the enumerator against the store's
			// recorded verdicts.
			if p.Proven && p.Bail {
				t.Fatalf("%s[%d] is PROVEN yet the strategy sequence emits no script — "+
					"'no script' cannot mean 'outside the provable fragment' (reason: %q)",
					m.Name, pi, o.detail)
			}
			obj.Props = append(obj.Props, p)
		}

		// --- the synthetic-query probe: what `--implies` actually proves -----
		obj.Polymorphic = d.TyVars > 0
		var cross string
		obj.Query, obj.QueryScripts, obj.QueryReason, cross = queryProbe(st, h, d, m)
		// CONTROL — the two synthetic forms must agree wherever both typecheck.
		// They mention the candidate through different term shapes (a `let`
		// bound value and an equality operand), so an agreement is evidence the
		// verdict is about the CANDIDATE and not about the probe's own syntax.
		// A disagreement means one form is not forcing the self-call it appears
		// to, which would silently move objects between the two classes.
		if cross != "" && cross != "not-probed" && cross != obj.Query {
			t.Fatalf("%s: the `let` form says %q and the `==` form says %q — the two "+
				"synthetic queries disagree, so at least one is not forcing the self-call",
				m.Name, obj.Query, cross)
		}
		switch obj.Query {
		case "reachable", "bail", "not-probed":
		default:
			t.Fatalf("%s: queryProbe returned an unknown verdict %q", m.Name, obj.Query)
		}
		if obj.Query != "reachable" && obj.QueryReason == "" {
			t.Fatalf("%s: query probe says %q with no reason — an unmeasured object must "+
				"say why it was not measured", m.Name, obj.Query)
		}
		// CONTROL — a stored property that MENTIONS `self` and translates did so
		// by translating this same body, so the synthetic mentioning query must
		// translate too. The reverse implication does NOT hold: a stored
		// property can use primitives the body does not. If this fires, the
		// synthetic property is built wrong — which is the only way this
		// instrument can be wrong without being obviously so.
		//
		// THE `self` QUALIFIER IS LOAD-BEARING AND WAS MISSING AT FIRST. A
		// property that never mentions the definition — a constant law, or one
		// relating only its binders — translates without the body being reached
		// at all, so it says nothing about the body. Without this gate, adding
		// such a law to an untranslatable definition would abort the whole probe
		// and blame the query. No corpus member has that shape today, which is
		// exactly why the unsound version passed.
		if obj.Query == "bail" {
			for i, p := range obj.Props {
				if !p.Bail && mentionsSelf(&d.Props[i].Body) {
					t.Fatalf("%s: property %d mentions `self` and translates, yet the synthetic "+
						"self-mentioning query bails (%q) — the probe's query is malformed, "+
						"not the corpus", m.Name, p.Index, obj.QueryReason)
				}
			}
		}
		rep.Objects = append(rep.Objects, obj)
	}

	if totalProps == 0 {
		t.Fatal("the live corpus carries no properties: this probe measured nothing")
	}
	if bails == 0 {
		t.Fatal("no goal in the live corpus bailed in translation — either the corpus is " +
			"entirely inside the provable fragment (check by hand before believing it) or " +
			"the enumeration seam is not being exercised")
	}
	if translated == 0 {
		t.Fatal("every goal in the live corpus bailed in translation — the enumerator is " +
			"almost certainly not running, since the corpus has proven properties")
	}

	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("encoding the report: %v", err)
	}
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("writing %s: %v", out, err)
	}
	t.Logf("fragment probe: %d live names, %d live objects, %d properties, "+
		"%d translated, %d bailed -> %s",
		rep.LiveNames, rep.LiveObjects, totalProps, translated, bails, out)
}
