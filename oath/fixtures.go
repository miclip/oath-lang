package main

// `oath fixtures <dir>` materializes the conformance suite described in
// SPEC.md §10 as byte-level artifacts, so a second kernel (Rust/WASM, #5) can
// be checked against a frozen target instead of prose. Everything here is
// deterministic: sorted output, no wall-clock, no RNG. Proof OUTCOMES are read
// from the reference store's metadata (populated by `make check`), so the
// solver is not re-invoked — only its recorded verdicts are frozen.
//
// Layout (a subset of the SPEC §10 sketch, plus the §1.5 golden encodings):
//
//   hashes.txt            name<TAB>hash for every current definition
//   canonical/<name>.json exact canonical Def bytes (identity fixtures)
//   encoding/             §1.5 golden byte fixtures + manifest (hand-built Defs)
//   verify/<name>.txt     property verdicts and counterexamples (deterministic)
//   analyses/<name>.json  termination, confinement, mutation, guarantee
//   prove/outcomes.json   per-property proof outcomes + solver version
//   gate/reject/<case>.oath + gate/expected.txt  self-validating reject corpus
//   MANIFEST.md           what this tree is and how to regenerate it

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func apiFixtures(st *Store, outdir string) (string, error) {
	var log strings.Builder
	write := func(rel string, data []byte) error {
		p := filepath.Join(outdir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	}

	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// hashes.txt + canonical/<name>.json
	var hashes strings.Builder
	for _, name := range keys {
		h := names[name]
		fmt.Fprintf(&hashes, "%s\t%s\n", name, h)
		b, err := os.ReadFile(filepath.Join(st.Root, "objects", h+".json"))
		if err != nil {
			return "", fmt.Errorf("read object for %s: %w", name, err)
		}
		if err := write(filepath.Join("canonical", name+".json"), b); err != nil {
			return "", err
		}
	}
	if err := write("hashes.txt", []byte(hashes.String())); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "hashes.txt + canonical/: %d definitions\n", len(keys))

	// verify/<name>.txt and analyses/<name>.json and prove outcomes
	type propOut struct {
		Name   string `json:"name"`
		Proven bool   `json:"proven"`
	}
	type proveEntry struct {
		Name        string    `json:"name"`
		Hash        string    `json:"hash"`
		Level       string    `json:"level"`
		ProvenCount int       `json:"proven_count"`
		PropCount   int       `json:"prop_count"`
		Props       []propOut `json:"props"`
	}
	type analysis struct {
		Name          string   `json:"name"`
		Hash          string   `json:"hash"`
		Kind          string   `json:"kind"`
		Termination   string   `json:"termination,omitempty"`
		Confinement   []string `json:"confinement,omitempty"`
		MutantsKilled int      `json:"mutants_killed,omitempty"`
		MutantsTotal  int      `json:"mutants_total,omitempty"`
		Level         string   `json:"level"`
		Cases         int      `json:"cases,omitempty"`
		Proven        int      `json:"proven,omitempty"`
	}
	var outcomes []proveEntry
	var verifyCount int
	for _, name := range keys {
		h := names[name]
		d, err := st.GetDef(h)
		if err != nil {
			return "", err
		}
		m, err := st.GetMeta(h)
		if err != nil {
			return "", err
		}
		// analyses
		a := analysis{
			Name: name, Hash: h, Kind: d.K,
			Termination: m.Termination, Confinement: m.Confinement,
			MutantsKilled: m.MutantsKilled, MutantsTotal: m.MutantsTotal,
			Level: m.Guarantee.Level, Cases: m.Guarantee.Cases, Proven: m.Guarantee.Proven,
		}
		ab, _ := json.MarshalIndent(a, "", "  ")
		if err := write(filepath.Join("analyses", name+".json"), ab); err != nil {
			return "", err
		}
		if d.K != "func" || len(d.Props) == 0 {
			continue
		}
		// verify/<name>.txt — deterministic verdicts and counterexamples,
		// computed read-only so fixture generation never mutates the store.
		reports, _, _, err := verifyReports(st, h)
		if err != nil {
			return "", err
		}
		if err := write(filepath.Join("verify", name+".txt"), []byte(renderVerifyReports(reports))); err != nil {
			return "", err
		}
		verifyCount++
		// prove outcomes from recorded metadata.
		provenSet := map[int]bool{}
		for _, pi := range m.ProvenProps {
			provenSet[pi] = true
		}
		e := proveEntry{Name: name, Hash: h, Level: m.Guarantee.Level, ProvenCount: len(m.ProvenProps), PropCount: len(d.Props)}
		for pi := range d.Props {
			e.Props = append(e.Props, propOut{Name: metaPropName(m, pi), Proven: provenSet[pi]})
		}
		outcomes = append(outcomes, e)
	}
	fmt.Fprintf(&log, "verify/: %d definitions with properties\n", verifyCount)

	solver := "unknown"
	if out, err := exec.Command("z3", "--version").Output(); err == nil {
		solver = strings.TrimSpace(string(out))
	}
	proveDoc := map[string]any{"kernel": kernelVersion, "solver": solver, "definitions": outcomes}
	pb, _ := json.MarshalIndent(proveDoc, "", "  ")
	if err := write(filepath.Join("prove", "outcomes.json"), pb); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "prove/outcomes.json: %d definitions (solver: %s)\n", len(outcomes), solver)

	// §1.5 golden encoding fixtures: hand-built Defs demonstrating the encoding
	// rules a second kernel must reproduce byte-for-byte. These are ENCODING
	// demonstrations, not necessarily well-typed terms.
	golden := []struct {
		name string
		note string
		def  *Def
	}{
		{"string_escapes", `string with " \ newline < > & U+2028 U+2029 — HTML-safety escapes are normative`,
			&Def{K: "func", Ty: tStr(), Body: &Term{K: "str", Str: "\"\\\n<>&  "}}},
		{"zero_var_omitted", "var 0 encodes as {\"k\":\"var\"} — idx zero omitted",
			&Def{K: "func", Ty: tInt(), Body: &Term{K: "var"}}},
		{"zero_ctor_idx_omitted", "constructor index 0 omitted; bool false omitted",
			&Def{K: "func", Ty: tBool(), Body: &Term{K: "bool"}}},
		{"empty_props_omitted", "a func with no props omits the props field entirely",
			&Def{K: "func", Ty: tInt(), Body: &Term{K: "int", Int: 0}}},
		{"prop_body_always_present", "Prop.binders and Prop.body are always present even when empty/zero",
			&Def{K: "func", Ty: tBool(), Body: &Term{K: "bool", Bool: true},
				Props: []Prop{{Binders: []Ty{}, Body: Term{K: "bool", Bool: true}}}}},
	}
	var gman strings.Builder
	gman.WriteString("# §1.5 golden encoding fixtures\n# case\thash\tnote\n")
	sort.Slice(golden, func(i, j int) bool { return golden[i].name < golden[j].name })
	for _, g := range golden {
		b, err := json.Marshal(g.def)
		if err != nil {
			return "", err
		}
		if err := write(filepath.Join("encoding", g.name+".json"), b); err != nil {
			return "", err
		}
		fmt.Fprintf(&gman, "%s\t%s\t%s\n", g.name, hashDef(g.def), g.note)
	}
	if err := write(filepath.Join("encoding", "manifest.txt"), []byte(gman.String())); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "encoding/: %d golden fixtures\n", len(golden))

	// gate reject corpus — self-validating: each source is run through a fresh
	// throwaway store and MUST reject, or fixture generation fails loudly.
	rejects := []struct {
		name string
		why  string
		src  string
	}{
		{"negative_datatype", "strict positivity: rec left of an arrow",
			"(data D [] (C (-> D D)))"},
		{"body_type_mismatch", "body is Bool, declared Int",
			"(defn bad [] [] Int true)"},
		{"eq_on_function", "== is not defined on function types",
			"(defn bad [] [(f (-> Int Int))] Bool (== f f))"},
		{"nonexhaustive_match", "match omits a constructor arm",
			"(data C2 [] (A) (B))\n(defn bad [] [(x C2)] Int (match x ((A) 0)))"},
		{"ctor_arity", "constructor applied to the wrong number of arguments",
			"(data Box [] (Mk Int))\n(defn bad [] [] Box (Mk))"},
	}
	sort.Slice(rejects, func(i, j int) bool { return rejects[i].name < rejects[j].name })
	var expected strings.Builder
	expected.WriteString("# gate conformance manifest\n")
	expected.WriteString("# accept corpus: examples/*.oath (every def currently in the store)\n")
	expected.WriteString("# reject corpus: gate/reject/*.oath — each MUST be rejected at the gate\n#\n")
	expected.WriteString("# file\texpected\treason\n")
	for _, r := range rejects {
		tmp, err := os.MkdirTemp("", "oath-fixture-")
		if err != nil {
			return "", err
		}
		tst, err := OpenStore(tmp)
		if err != nil {
			return "", err
		}
		reps, putErr := apiPut(tst, r.src, "fixtures")
		os.RemoveAll(tmp)
		// Rejection surfaces two ways: an elaboration error (returned as err),
		// or a gate rejection (a report with Status "rejected").
		rejected := putErr != nil
		for _, rep := range reps {
			if rep.Status == "rejected" {
				rejected = true
			}
		}
		if !rejected {
			return "", fmt.Errorf("reject fixture %q was NOT rejected by the current kernel — fixture is wrong", r.name)
		}
		if err := write(filepath.Join("gate", "reject", r.name+".oath"), []byte(r.src+"\n")); err != nil {
			return "", err
		}
		fmt.Fprintf(&expected, "gate/reject/%s.oath\treject\t%s\n", r.name, r.why)
	}
	if err := write(filepath.Join("gate", "expected.txt"), []byte(expected.String())); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "gate/reject/: %d self-validated reject cases\n", len(rejects))

	manifest := fmt.Sprintf(`# Oath conformance fixtures

Generated by `+"`oath fixtures <dir>`"+` from the reference store (kernel %s).
Regenerate with `+"`make fixtures`"+`. Everything here is deterministic.

A candidate kernel conforms (SPEC §10) if, against this tree:

1. Re-elaborating examples/*.oath reproduces every hash in hashes.txt, and each
   canonical/<name>.json is byte-identical to what it emits.
2. encoding/*.json hash to the values in encoding/manifest.txt (SPEC §1.5).
3. Its gate rejects every gate/reject/*.oath and accepts the examples corpus.
4. verify/<name>.txt reproduces byte-for-byte (verdicts + counterexamples).
5. analyses/<name>.json match (termination, confinement, mutation, guarantee).
6. prove/outcomes.json match, given the same solver version.

Files: hashes.txt, canonical/, encoding/, gate/, verify/, analyses/,
prove/outcomes.json.
`, kernelVersion)
	if err := write("MANIFEST.md", []byte(manifest)); err != nil {
		return "", err
	}

	fmt.Fprintf(&log, "\nfixtures written to %s\n", outdir)
	return log.String(), nil
}

func cmdFixtures(st *Store, outdir string) {
	out, err := apiFixtures(st, outdir)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}
