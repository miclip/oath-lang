package main

// A READ-ONLY census of the committed corpus, and the assertions that make it a
// measurement rather than a listing.
//
// WHY THIS EXISTS. Every question of the form "how much of the corpus is X?"
// needs a universe before it needs a number, and this repo has already paid for
// getting that wrong twice: a walk of `codebase/meta/` answers a question about
// the STORE'S HISTORY (every object ever stored, superseded ones included) while
// looking exactly like a question about the CORPUS, and a set-keyed join
// silently collapses aliases, because names alias and objects do not.
//
// So the universe here is derived from the CLAIM, in two halves that are
// asserted to coincide:
//
//	the SOURCE half  every top-level form the kernel's own reader finds in
//	                 examples/*.oath plus apps/*/*.oath — parseForms, not a
//	                 regex, because the parser is the authority on what a
//	                 declaration is
//	the STORE half   every name in codebase/names.json — the store's authority
//	                 on what is LIVE, which is what a meta/ walk is not
//
// The test asserts set equality between them. It deliberately hardcodes NO
// counts: a pinned 210 here would be exactly the duplicated-authority defect the
// census is being built to avoid, and it would go stale the first time the
// corpus grows. The numbers are OUTPUT, not expectation.
//
// It also asserts the two hand-maintained corpus lists agree with the glob,
// because `make verify`, oathrs/conformance.sh and the Makefile's APPS must stay
// in step or divergence reports become confidently wrong.
//
// WHAT IT DOES NOT DO. It does not re-derive hashes from source; that is check 1
// of oathrs/conformance.sh and `make verify`, and the latter APPENDS TO THE
// JOURNAL. Nothing here writes: the store is opened, read, and closed.
//
// Emitting the census: set OATH_CENSUS_OUT to a path and the full per-object and
// per-name enumeration is written there as JSON.
//
//	cd oath && OATH_CENSUS_OUT=/tmp/census.json go test -run TestCorpusCensus -count=1

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// censusPropVerdict is one property of one OBJECT. `Proven` is a fact about the
// hash (SPEC: verdicts are hash-keyed), so it is recorded per-object; only the
// NAME of the property varies per alias, which is why the per-name view below
// carries its own prop names and reuses these verdicts.
type censusPropVerdict struct {
	Index  int    `json:"index"`
	Name   string `json:"name"` // as spelled by the object's canonical name
	Proven bool   `json:"proven"`
}

// censusObject is one live object: something at least one live name resolves to.
type censusObject struct {
	Hash          string   `json:"hash"`
	CanonicalName string   `json:"canonical_name"` // meta.Name
	LiveNames     []string `json:"live_names"`     // every live name resolving here
	Kind          string   `json:"kind"`           // data | defn, from the parsed source form
	DefKind       string   `json:"def_kind"`       // Def.K, as stored
	// Sites is EVERY declaration that produced this object, not one of them. An
	// aliased object has two, in two different files, and reporting a single
	// site would quietly pick one and look complete.
	Sites         []censusSite        `json:"declaration_sites"`
	Level         string              `json:"level"`
	Cases         int                 `json:"cases,omitempty"`
	ProvenCount   int                 `json:"proven_count"`
	PropCount     int                 `json:"prop_count"`
	Falsified     []string            `json:"falsified,omitempty"`
	Termination   string              `json:"termination,omitempty"`
	Confinement   []string            `json:"confinement,omitempty"`
	MutantsKilled int                 `json:"mutants_killed"`
	MutantsTotal  int                 `json:"mutants_total"`
	WaivedMutants int                 `json:"waived_mutants"`
	Campaign      string              `json:"mutation_campaign,omitempty"`
	Hinted        []int               `json:"hinted_props,omitempty"`
	Props         []censusPropVerdict `json:"props"`
}

// censusName is one live NAME. Two names sharing an object appear twice here and
// once above; that duplication is the point, and collapsing it is the defect.
type censusName struct {
	Name       string              `json:"name"`
	Hash       string              `json:"hash"`
	IsAlias    bool                `json:"is_alias"` // name != meta.Name
	Kind       string              `json:"kind"`
	SourceFile string              `json:"source_file"`
	SourceLine int                 `json:"source_line"`
	Level      string              `json:"level"`
	Props      []censusPropVerdict `json:"props"` // prop names AS THIS NAME SPELLS THEM
}

// censusSite is one (data ...) or (defn ...) form in the corpus source.
type censusSite struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type censusReport struct {
	SourceDecls   int            `json:"source_declarations"`
	LiveNames     int            `json:"live_names"`
	LiveObjects   int            `json:"live_objects"`
	CorpusFiles   []string       `json:"corpus_files"`
	AliasedObject map[string]any `json:"aliased_objects"` // hash -> names, for the objects with >1 live name
	Objects       []censusObject `json:"objects"`
	Names         []censusName   `json:"names"`
}

type sourceDecl struct {
	kind string // "data" | "defn"
	name string
	file string
	line int
}

// corpusSources is the corpus as every other tool in this repo defines it:
// examples/*.oath plus apps/*/*.oath, apps last because they depend on the
// examples.
func corpusSources(t *testing.T) []string {
	t.Helper()
	ex, err := filepath.Glob("../examples/*.oath")
	if err != nil || len(ex) == 0 {
		t.Fatalf("no example sources found (err=%v): this test must run from oath/", err)
	}
	sort.Strings(ex)
	ap, err := filepath.Glob("../apps/*/*.oath")
	if err != nil {
		t.Fatalf("globbing apps: %v", err)
	}
	sort.Strings(ap)
	if len(ap) == 0 {
		t.Fatal("no app sources found; the corpus is examples/ PLUS apps/, and an empty apps glob means this check is measuring the wrong universe")
	}
	return append(ex, ap...)
}

func TestCorpusCensus(t *testing.T) {
	files := corpusSources(t)

	// --- the SOURCE half -----------------------------------------------------
	var decls []sourceDecl
	byName := map[string]sourceDecl{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		forms, err := parseForms(string(src))
		if err != nil {
			t.Fatalf("parsing %s with the kernel reader: %v", f, err)
		}
		for _, form := range forms {
			// apiPut accepts exactly (data ...) and (defn ...) at top level. A
			// third kind here would mean this census is enumerating something
			// the kernel would refuse, so fail rather than skip.
			if form.K != "list" || len(form.Kids) < 2 || form.Kids[0].K != "sym" || form.Kids[1].K != "sym" {
				t.Fatalf("%s:%d: top-level form is not (<kind> <name> ...)", f, form.Line)
			}
			kind, name := form.Kids[0].Sym, form.Kids[1].Sym
			if kind != "data" && kind != "defn" {
				t.Fatalf("%s:%d: unknown top-level form %q (the kernel accepts only data and defn)", f, form.Line, kind)
			}
			d := sourceDecl{kind: kind, name: name, file: f, line: form.Line}
			if prev, dup := byName[name]; dup {
				t.Fatalf("%q declared twice in the corpus: %s:%d and %s:%d",
					name, prev.file, prev.line, d.file, d.line)
			}
			byName[name] = d
			decls = append(decls, d)
		}
	}

	// --- the STORE half ------------------------------------------------------
	// The FILESYSTEM backend explicitly, not OpenStore. `OpenStore` consults
	// OATH_BACKEND and, when it is `cloud`, ignores the path it was handed and
	// opens the remote registry instead — so the census would describe a
	// different corpus while every assertion below still passed. The claim is
	// about the committed store at this path; the authority has to be pinned to
	// the claim, not left to the environment.
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	live := st.Names()

	// --- the assertion the census rests on -----------------------------------
	// Set equality, reported as differences rather than as a count mismatch: a
	// bare "210 != 209" says nothing about which side moved.
	var declaredNotLive, liveNotDeclared []string
	for n := range byName {
		if _, ok := live[n]; !ok {
			declaredNotLive = append(declaredNotLive, n)
		}
	}
	for n := range live {
		if _, ok := byName[n]; !ok {
			liveNotDeclared = append(liveNotDeclared, n)
		}
	}
	sort.Strings(declaredNotLive)
	sort.Strings(liveNotDeclared)
	if len(declaredNotLive) > 0 {
		t.Errorf("declared in corpus source but NOT live in codebase/names.json (%d): %v",
			len(declaredNotLive), declaredNotLive)
	}
	if len(liveNotDeclared) > 0 {
		t.Errorf("live in codebase/names.json but NOT declared in the corpus source (%d): %v",
			len(liveNotDeclared), liveNotDeclared)
	}
	if len(decls) != len(byName) {
		t.Fatalf("internal: %d declarations but %d distinct names", len(decls), len(byName))
	}

	// --- the per-object and per-name views -----------------------------------
	namesOf := map[string][]string{}
	for n, h := range live {
		namesOf[h] = append(namesOf[h], n)
	}
	hashes := make([]string, 0, len(namesOf))
	for h := range namesOf {
		sort.Strings(namesOf[h])
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	rep := censusReport{
		SourceDecls:   len(decls),
		LiveNames:     len(live),
		LiveObjects:   len(namesOf),
		CorpusFiles:   files,
		AliasedObject: map[string]any{},
	}

	objProps := map[string][]censusPropVerdict{}
	for _, h := range hashes {
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("meta for %s (live names %v): %v", h, namesOf[h], err)
		}
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("def for %s (live names %v): %v", h, namesOf[h], err)
		}
		// THE PROPERTY UNIVERSE IS THE DEFINITION, NOT THE NAMING METADATA.
		// `Prop` carries no name — properties live in the hashed `Def` and are
		// part of identity, while `PropNames` is metadata supplying spellings.
		// So `d.Props` decides HOW MANY properties exist and `m.PropNames` only
		// decides what they are called; taking the count from the names would
		// let a stale or short naming record delete a real property from every
		// figure below without a single assertion firing.
		if len(m.PropNames) != len(d.Props) {
			t.Errorf("%s (%v): the definition carries %d properties but prop_names has %d entries",
				h[:12], namesOf[h], len(d.Props), len(m.PropNames))
		}
		provenSet := map[int]bool{}
		for _, i := range m.ProvenProps {
			// A stray index would mean the lemma library points at nothing, and
			// the proven COUNT would then exceed the enumerated verdicts with
			// nothing noticing.
			if i < 0 || i >= len(d.Props) {
				t.Errorf("%s (%v): proven_props index %d out of range for %d properties",
					h[:12], namesOf[h], i, len(d.Props))
				continue
			}
			// A repeated index inflates ProvenProps (and guarantee.proven with
			// it) while marking one property proven, so the two agree with each
			// other and disagree with the enumeration.
			if provenSet[i] {
				t.Errorf("%s (%v): proven_props lists index %d more than once",
					h[:12], namesOf[h], i)
			}
			provenSet[i] = true
		}
		props := make([]censusPropVerdict, 0, len(d.Props))
		for i := range d.Props {
			pn := ""
			if i < len(m.PropNames) {
				pn = m.PropNames[i]
			}
			props = append(props, censusPropVerdict{Index: i, Name: pn, Proven: provenSet[i]})
		}
		objProps[h] = props

		// Guarantee.Proven is a stored summary of the same fact. If the two
		// disagree, one of them is stale and every count built on either is
		// suspect. Compared against the DISTINCT indices, so a duplicate cannot
		// make an inflated summary look corroborated.
		if m.Guarantee.Proven != len(provenSet) {
			t.Errorf("%s (%v): guarantee.proven=%d but proven_props names %d distinct properties",
				h[:12], namesOf[h], m.Guarantee.Proven, len(provenSet))
		}

		// Kind: taken from the SOURCE forms, and cross-checked against what the
		// store holds. Every declaration site of one object must agree on kind —
		// they produced the same canonical bytes, so a disagreement here would
		// mean the source and the store describe different things.
		var sites []censusSite
		sd := byName[namesOf[h][0]]
		for _, nm := range namesOf[h] {
			s := byName[nm]
			if s.kind != sd.kind {
				t.Errorf("%s: names %v resolve to one object but declare different kinds (%q vs %q)",
					h[:12], namesOf[h], sd.kind, s.kind)
			}
			sites = append(sites, censusSite{Name: nm, File: s.file, Line: s.line})
		}
		// Compared EXACTLY, both directions. `!= "data"` would accept an empty or
		// unknown stored kind for every `(defn …)` in the corpus, so the
		// cross-check would pass over precisely the malformed data it exists to
		// catch.
		defKind := d.K
		wantKind := map[string]string{"data": "data", "defn": "func"}[sd.kind]
		if defKind != wantKind {
			t.Errorf("%s (%v): source form is %q so Def.K should be %q, but the store holds %q",
				h[:12], namesOf[h], sd.kind, wantKind, defKind)
		}

		var hinted []int
		for i := range m.Hints {
			hinted = append(hinted, i)
		}
		sort.Ints(hinted)

		rep.Objects = append(rep.Objects, censusObject{
			Hash: h, CanonicalName: m.Name, LiveNames: namesOf[h],
			Kind: sd.kind, DefKind: defKind, Sites: sites,
			Level: m.Guarantee.Level, Cases: m.Guarantee.Cases,
			ProvenCount: len(provenSet), PropCount: len(d.Props),
			Falsified: m.Guarantee.Falsified, Termination: m.Termination,
			Confinement:   m.Confinement,
			MutantsKilled: m.MutantsKilled, MutantsTotal: m.MutantsTotal,
			WaivedMutants: len(m.WaivedMutants), Campaign: m.MutationCampaign,
			Hinted: hinted, Props: props,
		})
		if len(namesOf[h]) > 1 {
			rep.AliasedObject[h] = namesOf[h]
		}
	}

	allNames := make([]string, 0, len(live))
	for n := range live {
		allNames = append(allNames, n)
	}
	sort.Strings(allNames)
	for _, n := range allNames {
		h := live[n]
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("meta for name %q: %v", n, err)
		}
		// Per-NAME prop names: an alias carries its own naming record, and the
		// verdicts stay hash-keyed. Reading m.PropNames for an alias would report
		// the canonical name's spelling under the alias's row.
		propNames := m.PropNames
		isAlias := n != m.Name
		if isAlias {
			if an, ok := m.Aliases[n]; ok && an != nil {
				propNames = an.PropNames
			} else {
				t.Errorf("%q resolves to %s whose canonical name is %q, but there is no alias naming record for it",
					n, h[:12], m.Name)
			}
		}
		if len(propNames) != len(objProps[h]) {
			t.Errorf("%q: %d prop names but the object carries %d properties", n, len(propNames), len(objProps[h]))
			continue
		}
		props := make([]censusPropVerdict, len(objProps[h]))
		for i := range objProps[h] {
			props[i] = censusPropVerdict{Index: i, Name: propNames[i], Proven: objProps[h][i].Proven}
		}
		rep.Names = append(rep.Names, censusName{
			Name: n, Hash: h, IsAlias: isAlias, Kind: byName[n].kind,
			SourceFile: byName[n].file, SourceLine: byName[n].line,
			Level: m.Guarantee.Level, Props: props,
		})
	}

	t.Logf("corpus census: %d source declarations, %d live names, %d live objects (%d aliased)",
		rep.SourceDecls, rep.LiveNames, rep.LiveObjects, len(rep.AliasedObject))

	// A census is only emitted by a measurement that PASSED. Writing one after an
	// assertion failed would hand the renderer — and anything quoting it — a
	// partial or internally inconsistent report that looks exactly like a good
	// one, which is this repo's most familiar defect wearing a new costume.
	if t.Failed() {
		if out := os.Getenv("OATH_CENSUS_OUT"); out != "" {
			// Remove any earlier report so a stale file cannot be mistaken for
			// this run's output.
			_ = os.Remove(out)
			t.Logf("assertions failed: no census written, and %s removed if it existed", out)
		}
		return
	}

	if out := os.Getenv("OATH_CENSUS_OUT"); out != "" {
		b, err := json.MarshalIndent(rep, "", " ")
		if err != nil {
			t.Fatalf("marshalling census: %v", err)
		}
		if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
			t.Fatalf("writing census to %s: %v", out, err)
		}
		fmt.Fprintf(os.Stderr, "census written to %s\n", out)
	}
}

// TestCorpusListsAgree checks the hand-maintained corpus lists against the glob
// the census uses. CLAUDE.md requires `make verify`, oathrs/conformance.sh and
// the Makefile's APPS to stay in step "or divergence reports become confidently
// wrong" — and the only one of the three that is a hand-written LIST is APPS.
// A list is exactly the enumerable proxy that this repo keeps finding under its
// wrong-universe defects, so it gets an assertion rather than a comment.
func TestCorpusListsAgree(t *testing.T) {
	mk, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	// `make verify` iterates the hand-written EXAMPLES and EXHIBITS lists by
	// BASENAME and APPS by path, while the census and oathrs/conformance.sh
	// derive their corpus from globs. A source that exists on disk but is
	// dropped from a list is then verified by nobody and measured by everybody,
	// which is precisely the silent divergence CLAUDE.md requires preventing —
	// and it is invisible from either side.
	examples := makeVarFields(t, string(mk), "EXAMPLES")
	exhibits := makeVarFields(t, string(mk), "EXHIBITS")
	listed := map[string]bool{}
	for _, n := range append(append([]string{}, examples...), exhibits...) {
		if listed[n] {
			t.Errorf("%q appears in EXAMPLES and EXHIBITS, or twice in one of them", n)
		}
		listed[n] = true
	}
	compareToGlob(t, "../examples/*.oath", listed, "EXAMPLES+EXHIBITS", func(path string) string {
		return strings.TrimSuffix(filepath.Base(path), ".oath")
	})

	appsListed := map[string]bool{}
	for _, f := range makeVarFields(t, string(mk), "APPS") {
		appsListed[f] = true
	}
	compareToGlob(t, "../apps/*/*.oath", appsListed, "APPS", func(path string) string {
		return filepath.ToSlash(strings.TrimPrefix(path, "../"))
	})
}

// makeVarFields reads one `VAR = ...` assignment out of a Makefile, following
// backslash continuations. It FAILS rather than returning empty if the variable
// is absent: a check whose input silently parsed to nothing would pass over
// everything, which is worse than no check at all.
func makeVarFields(t *testing.T, mk, name string) []string {
	t.Helper()
	lines := strings.Split(mk, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(ln, name+" ") && !strings.HasPrefix(ln, name+"=") {
			continue
		}
		acc := ln[strings.Index(ln, "=")+1:]
		for strings.HasSuffix(strings.TrimRight(acc, " \t"), "\\") {
			i++
			if i >= len(lines) {
				break
			}
			acc = strings.TrimSuffix(strings.TrimRight(acc, " \t"), "\\") + " " + lines[i]
		}
		f := strings.Fields(acc)
		if len(f) == 0 {
			t.Fatalf("%s parsed to zero entries; this check did NOT run", name)
		}
		return f
	}
	t.Fatalf("no %s assignment found in the Makefile; this check did NOT run", name)
	return nil
}

// compareToGlob asserts a hand-written list and a glob describe the same set,
// reporting each direction separately so the output says which side moved.
func compareToGlob(t *testing.T, pattern string, listed map[string]bool, label string, key func(string) string) {
	t.Helper()
	found, err := filepath.Glob(pattern)
	if err != nil || len(found) == 0 {
		t.Fatalf("globbing %s (err=%v, n=%d); this check did NOT run", pattern, err, len(found))
	}
	onDisk := map[string]bool{}
	for _, f := range found {
		onDisk[key(f)] = true
	}
	for f := range onDisk {
		if !listed[f] {
			t.Errorf("%s contains %s but the Makefile's %s does not list it", pattern, f, label)
		}
	}
	for f := range listed {
		if !onDisk[f] {
			t.Errorf("the Makefile's %s lists %s but %s does not find it", label, f, pattern)
		}
	}
}
