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
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// fixtureFilename encodes a definition name into a filename that cannot collide
// on a CASE-INSENSITIVE filesystem. macOS folds `map` and `Map` onto one inode,
// so the corpus silently shipped 186 canonical fixtures for 187 definitions —
// one definition's bytes absent entirely, whichever was written second having
// overwritten the first — and nothing noticed, because coverage was defined by
// what was in the DIRECTORY rather than by what was in the CORPUS (#95).
//
// The encoding is a bijection and stays readable:
//
//	'_'         -> "__"
//	uppercase X -> "_x"
//	otherwise   -> unchanged
//
// So `Map` -> `_map`, `map` -> `map`, `_map` -> `__map`. Names differing only by
// case now differ by more than case, which is what a case-folding filesystem
// needs. It also makes the tree reproducible across platforms: the same
// generator emitted 186 files on macOS and 187 on Linux.
// countFixtures counts what actually landed on disk, so the caller can compare it
// against the corpus. Reading the FILESYSTEM rather than a counter is deliberate:
// a counter increments once per write and cannot see one file overwriting
// another, which is exactly how the collision stayed invisible.
func countFixtures(outdir, sub, ext string) (int, error) {
	ents, err := os.ReadDir(filepath.Join(outdir, sub))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			n++
		}
	}
	return n, nil
}

func fixtureFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '_':
			b.WriteString("__")
		case r >= 'A' && r <= 'Z':
			b.WriteByte('_')
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

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
		b, err := os.ReadFile(filepath.Join(st.Root, "objects", h+".bin"))
		if err != nil {
			return "", fmt.Errorf("read object for %s: %w", name, err)
		}
		if err := write(filepath.Join("canonical", fixtureFilename(name)+".bin"), b); err != nil {
			return "", err
		}
	}
	if err := write("hashes.txt", []byte(hashes.String())); err != nil {
		return "", err
	}
	// COVERAGE IS ASSERTED AGAINST THE CORPUS, not counted from the directory.
	// The old report said "186 definitions" and was true and useless: it described
	// what had been written, so a definition whose fixture was overwritten by a
	// case-colliding sibling could not be noticed, let alone fail (#95).
	if n, err := countFixtures(outdir, "canonical", ".bin"); err != nil {
		return "", err
	} else if n != len(keys) {
		return "", fmt.Errorf("canonical/: wrote %d fixtures for %d definitions — %d "+
			"definition(s) have no canonical bytes in the corpus, so nothing would "+
			"compare them", n, len(keys), len(keys)-n)
	}
	fmt.Fprintf(&log, "hashes.txt + canonical/: %d definitions\n", len(keys))

	// verify/<name>.txt and analyses/<name>.json and prove outcomes
	type propOut struct {
		Name   string `json:"name"`
		Proven bool   `json:"proven"`
		// Author hints for this goal (#67). Hints live in STORE METADATA, not in
		// .oath source, so a kernel reading only source could never reproduce a
		// hinted goal's script. They ride here because outcomes.json is already
		// the channel by which the byte-oracle hands recorded proof state to an
		// independent kernel — hints are proof state in exactly the same sense.
		// Targets are DEFINITION HASHES, which are portable across kernels (each
		// computes them itself), never names. omitempty keeps a hint-free corpus
		// byte-identical to before the feature existed.
		Hints []HintRef `json:"hints,omitempty"`
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
		// The campaign the score was obtained under. Without it a score is
		// detached from its conditions, and "is this current?" is unanswerable
		// from the fixture alone (SPEC §11).
		MutationCampaign string `json:"mutation_campaign,omitempty"`
		Level            string `json:"level"`
		Cases            int    `json:"cases,omitempty"`
		Proven           int    `json:"proven,omitempty"`
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
			MutationCampaign: m.MutationCampaign,
			Level:            m.Guarantee.Level, Cases: m.Guarantee.Cases, Proven: m.Guarantee.Proven,
		}
		ab, _ := json.MarshalIndent(a, "", "  ")
		if err := write(filepath.Join("analyses", fixtureFilename(name)+".json"), ab); err != nil {
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
		if err := write(filepath.Join("verify", fixtureFilename(name)+".txt"), []byte(renderVerifyReports(reports))); err != nil {
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
			e.Props = append(e.Props, propOut{Name: metaPropName(m, pi), Proven: provenSet[pi], Hints: m.Hints[pi]})
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
	// envelope/vectors.jsonl — the signed-publication conformance vectors (SPEC §8.6).
	//
	// JSONL with every octet string carried as canonical base64. That choice is the
	// point: the previous format used Go's %q quoting, so reading it required
	// implementing another language's string-literal rules — inside a corpus whose
	// entire purpose is being readable by an INDEPENDENT kernel. A fixture format that
	// presumes the reference language is not a cross-kernel fixture.
	//
	// Base64 also removes the representation ambiguity that made the old file lie: an
	// octet sequence containing LFs has no unambiguous one-line text spelling, which is
	// exactly why envelope_b64 exists in the journal.
	//
	// Three record kinds:
	//   canonical  structured envelope -> the exact octets it MUST encode to
	//   reject     octets a conformant parser MUST refuse, with the reason
	//   signature  the WHOLE path: envelope, octets, envelope_b64, key, signature,
	//              canonical journal line, and the expected verdict
	//   store      §8.6.4's store-side MUSTs: a STATE (what the store believes) plus a
	//              REQUEST (signed octets, signature, authenticated principal, and the
	//              artifact the store recomputed), with the expected verdict
	if err := writeEnvelopeVectors(write); err != nil {
		return "", err
	}

	// gate/bytes/ — HOSTILE OBJECT BYTES (#91).
	//
	// The existing reject corpus tests SOURCE-level rejects: text the elaborator must
	// refuse. It cannot reach the decoder's canonicality rules at all, because every
	// object the kernel produces is canonical by construction. That gap let a
	// negative-zero encoding — sign=1 with an empty magnitude, a second byte spelling
	// of 0 — decode happily for as long as it existed, at a DISTINCT content hash. The
	// store's load path validates sha256(bytes)==name and typechecks, deliberately not
	// encode(decode(b))==b, so a crafted object loaded under exactly the hosted-store
	// threat model that check exists to defend.
	//
	// Each vector is a REAL encoding with ONE byte-level defect. That construction is
	// the point: generation asserts the unperturbed original DECODES and the perturbed
	// one does NOT, so each vector witnesses one obligation rather than merely failing.
	// A hand-written blob that is malformed in several ways at once would be rejected
	// by whichever check runs first and would prove nothing about the rule it names.
	if err := writeHostileBytes(write); err != nil {
		return "", err
	}

	// license/vectors.jsonl — LICENSE EVALUATION (DESIGN.md "What belongs inside
	// identity"). These witness a consumer-visible DERIVED claim with legal
	// consequence, so the priority is the dangerous direction: any mutation turning an
	// unknown or prohibited composition into YES must be caught. A false UNSTATED is
	// inconvenient; a false YES is harmful.
	//
	// Language-neutral by construction — a case is (policy, ordered assertions) and an
	// expected verdict per dimension, so an independent evaluator can reproduce the
	// three-valued combination, the model lookup, the canonical input ordering and the
	// evaluation identity without reading this kernel.
	if err := writeLicenseVectors(write); err != nil {
		return "", err
	}
	if err := writeLicenseModel(write); err != nil {
		return "", err
	}

	// campaign/vectors.txt — canonical campaign descriptions and their digests
	// (SPEC §11). These are the AUDIT vectors: a kernel reproduces the identity
	// of a measurement without running one, and without consulting another
	// implementation. Cases chosen for what they pin: the empty-waiver line must
	// still be present, and two waivers in either recording order must give the
	// SAME digest (the identity is of the SET).
	var camp strings.Builder
	camp.WriteString("# canonical campaign description (SPEC §11) -> sha256, one blank-line-separated block each\n")
	for _, d := range []campaignDescription{
		{Artifact: strings.Repeat("00", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy},
		{Artifact: strings.Repeat("ab", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy,
			Waivers: []string{strings.Repeat("11", 32)}},
		{Artifact: strings.Repeat("ab", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy,
			Waivers: []string{strings.Repeat("11", 32), strings.Repeat("22", 32)}},
		// SAME set, reversed recording order — must give the SAME digest.
		{Artifact: strings.Repeat("ab", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy,
			Waivers: []string{strings.Repeat("22", 32), strings.Repeat("11", 32)}},
		// DUPLICATE member: collapses, so this equals the single-waiver vector.
		{Artifact: strings.Repeat("ab", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy,
			Waivers: []string{strings.Repeat("11", 32), strings.Repeat("11", 32)}},
		// One set MEMBER changed: must change encoding and digest.
		{Artifact: strings.Repeat("ab", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy,
			Waivers: []string{strings.Repeat("33", 32)}},
		// UPPERCASE hex is a DIFFERENT description, not the same one encoded
		// differently — no case folding, so canonicality holds literally.
		{Artifact: strings.Repeat("AB", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy},
		// Single-digit and zero integers: pins shortest bare decimal, no padding.
		{Artifact: strings.Repeat("ef", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: 1, Fuel: 0, WaiverPolicy: waiverPolicy},
		// Three members given out of order: sorting must be a real sort, not a
		// pairwise swap that happens to work on two elements.
		{Artifact: strings.Repeat("ab", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy,
			Waivers: []string{strings.Repeat("33", 32), strings.Repeat("11", 32), strings.Repeat("22", 32)}},
		// A value containing `=`: harmless under first-match splitting, and the
		// vector says so rather than leaving it to be argued about.
		{Artifact: strings.Repeat("ab", 32), Kernel: "kernel=with=equals", Engine: mutationEngine,
			Cases: mutantCases, Fuel: mutantFuel, WaiverPolicy: waiverPolicy},
		// Only the case budget differs from the first vector: a different claim.
		{Artifact: strings.Repeat("cd", 32), Kernel: kernelVersion, Engine: mutationEngine,
			Cases: 600, Fuel: mutantFuel, WaiverPolicy: waiverPolicy},
	} {
		camp.Write(campaignEncode(d))
		fmt.Fprintf(&camp, "digest=%s\n\n", campaignDigest(d))
	}
	if err := write(filepath.Join("campaign", "vectors.txt"), []byte(camp.String())); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "campaign/vectors.txt: 11 identity vectors\n")
	// prove/scripts.txt — sha256 of every property's direct-attempt script
	// under the recorded lemma state (SPEC §7.2 script stability). This is
	// the byte oracle that pins the naming scheme, lemma order, and
	// translation across independent kernels without prose ambiguity.
	var scripts strings.Builder
	scripts.WriteString("# name\tprop\tsha256(direct-attempt script)\n")
	scriptCount := 0
	for _, name := range keys {
		hh := names[name]
		dd, err := st.GetDef(hh)
		if err != nil || dd.K != "func" || len(dd.Props) == 0 {
			continue
		}
		for pi := range dd.Props {
			sc, err := directAttemptScript(st, hh, pi)
			if err != nil {
				continue // outside the provable fragment: no script exists
			}
			sum := sha256.Sum256([]byte(sc))
			fmt.Fprintf(&scripts, "%s\t%d\t%s\n", name, pi, hex.EncodeToString(sum[:]))
			scriptCount++
		}
	}
	if err := write(filepath.Join("prove", "scripts.txt"), []byte(scripts.String())); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "prove/scripts.txt: %d direct-attempt script hashes\n", scriptCount)

	// prove/scripts/ — full golden script TEXTS for a curated set, one per
	// structural feature of the translation. scripts.txt pins all 161 by
	// hash; these make a divergence debuggable (a hash tells you THAT you
	// differ, a golden tells you WHERE). Chosen: a recursive function with
	// defining axiom and own-lemma library (length:0), a non-total callee
	// left uninterpreted (spin:0), the lemma-heavy interleaved-declaration
	// stress case (q-drop:2), and lexicographic-fragment recursion over two
	// arguments (merge:0).
	goldenScripts := []struct {
		name string
		pi   int
	}{{"length", 0}, {"spin", 0}, {"q-drop", 2}, {"merge", 0},
		// Second wave, driven by cross-kernel divergence debugging: a
		// multi-recursive-field datatype (t-size), records + strings
		// (full-name), a capability record with an array-encoded function
		// field (greet), and a record-carrying datatype (rle-encode).
		{"t-size", 0}, {"full-name", 0}, {"greet", 0}, {"rle-encode", 0}}
	goldenCount := 0
	for _, g := range goldenScripts {
		hh, ok := names[g.name]
		if !ok {
			continue
		}
		sc, err := directAttemptScript(st, hh, g.pi)
		if err != nil {
			continue
		}
		p := filepath.Join("prove", "scripts", fmt.Sprintf("%s-%d.smt2", g.name, g.pi))
		if err := write(p, []byte(sc)); err != nil {
			return "", err
		}
		goldenCount++
	}
	fmt.Fprintf(&log, "prove/scripts/: %d golden script texts\n", goldenCount)

	// §1.5 golden encoding fixtures: hand-built Defs demonstrating the encoding
	// rules a second kernel must reproduce byte-for-byte. These are ENCODING
	// demonstrations, not necessarily well-typed terms.
	golden := []struct {
		name string
		note string
		def  *Def
	}{
		{"negative_int", "i64 is 8-byte big-endian two's complement",
			&Def{K: "func", Ty: tInt(), Body: &Term{K: "int", Int: big.NewInt(-401)}}},
		{"bool_bytes", "bool encodes as a single 0x00/0x01 byte",
			&Def{K: "func", Ty: tBool(), Body: &Term{K: "bool", Bool: false}}},
		{"hash_reference", "hash references are 32 raw bytes, not hex text",
			&Def{K: "func", Ty: tBool(), Body: &Term{K: "ctor",
				Hash: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
				Idx:  0, Args: []Term{{K: "bool"}}}}},
		{"empty_lists", "counts are u32; empty lists are a bare zero count (props here)",
			&Def{K: "func", Ty: tInt(), Body: &Term{K: "int", Int: big.NewInt(0)},
				Props: []Prop{{Binders: []Ty{}, Body: Term{K: "bool", Bool: true}}}}},
		{"negative_rat", "rat encodes as a reduced bigint pair (numerator, denominator); sign on the numerator",
			&Def{K: "func", Ty: tRat(), Body: &Term{K: "rat", Rat: big.NewRat(-7, 4)}}},
		{"negative_float", "float encodes as 8 big-endian IEEE-754 bytes (here -2.5); NaN would be canonical",
			&Def{K: "func", Ty: tFloat(), Body: &Term{K: "float", Float: -2.5}}},
		{"record_order", "record fields encode name-then-value pairs in strictly ascending name order",
			&Def{K: "func", Ty: &Ty{K: "record", Names: []string{"a", "b"}, Args: []Ty{{K: "int"}, {K: "bool"}}},
				Body: &Term{K: "record", Names: []string{"a", "b"},
					Args: []Term{{K: "int", Int: big.NewInt(1)}, {K: "bool", Bool: true}}}}},
	}
	var gman strings.Builder
	gman.WriteString("# §1.5 golden encoding fixtures (O1 binary)\n# case\thash\tnote\n")
	sort.Slice(golden, func(i, j int) bool { return golden[i].name < golden[j].name })
	for _, g := range golden {
		b := encodeDef(g.def)
		if err := write(filepath.Join("encoding", g.name+".bin"), b); err != nil {
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
		// Coverage gaps flagged by the blind implementation (#22): these are
		// exactly the spots its DIVERGENCES marked UNTESTED.
		{"negative_through_container", "strict positivity: rec nested inside another datatype's type argument, left of an arrow",
			"(data W [a] (Wrap a))\n(data D [] (C (-> (W D) Int)))"},
		{"eq_on_record_with_function", "== rejected when a function hides inside a record type",
			"(defn bad [] [(r {f (-> Int Int)})] Bool (== r r))"},
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
		reps, putErr := apiPut(tst, r.src, "fixtures", "")
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

	// gate accept corpus — edge cases that MUST elaborate and pass the gate,
	// beyond what the examples corpus happens to exercise. Self-validating
	// like the rejects, in reverse.
	accepts := []struct {
		name string
		why  string
		src  string
	}{
		{"positive_through_container", "strict positivity: rec nested in a container type argument in positive position (rose-tree shape) is legal",
			"(data W [a] (Wrap a))\n(data Rose [] (Node Int (W Rose)))"},
		{"primitive_head_wins", "a list head naming a primitive is the primitive, even when a local variable shadows the name",
			"(defn shadow [] [(not Bool)] Bool (not not))"},
		{"def_named_like_primitive", "defining a name that collides with a primitive is legal; call heads still resolve to the primitive",
			"(defn + [] [(a Int)] Int a)\n(defn uses-prim [] [(x Int)] Int (+ x x))"},
	}
	sort.Slice(accepts, func(i, j int) bool { return accepts[i].name < accepts[j].name })
	for _, a := range accepts {
		tmp, err := os.MkdirTemp("", "oath-fixture-")
		if err != nil {
			return "", err
		}
		tst, err := OpenStore(tmp)
		if err != nil {
			return "", err
		}
		reps, putErr := apiPut(tst, a.src, "fixtures", "")
		os.RemoveAll(tmp)
		accepted := putErr == nil
		for _, rep := range reps {
			if rep.Status == "rejected" || rep.Status == "blocked" {
				accepted = false
			}
		}
		if !accepted {
			return "", fmt.Errorf("accept fixture %q was NOT accepted by the current kernel — fixture is wrong (putErr=%v)", a.name, putErr)
		}
		if err := write(filepath.Join("gate", "accept", a.name+".oath"), []byte(a.src+"\n")); err != nil {
			return "", err
		}
		fmt.Fprintf(&expected, "gate/accept/%s.oath\taccept\t%s\n", a.name, a.why)
	}

	if err := write(filepath.Join("gate", "expected.txt"), []byte(expected.String())); err != nil {
		return "", err
	}
	fmt.Fprintf(&log, "gate/reject/: %d self-validated reject cases; gate/accept/: %d self-validated accept cases\n", len(rejects), len(accepts))

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
7. campaign/vectors.txt digests reproduce (SPEC §11) — measurement identity,
   derivable without running a measurement.
8. gate/bytes/ (SPEC §1): every *.bin except baseline.bin MUST be refused by the
   decoder, and baseline.bin MUST decode. These are hostile OBJECT bytes — the
   canonicality rules are unreachable from source, since every object a kernel
   produces is canonical by construction.
9. license/vectors.jsonl (DESIGN.md, "What belongs inside identity"): every
   evaluation record's expected verdict per dimension must be reproduced, and every
   identity record's evaluation digest must match. These witness a consumer-visible
   DERIVED claim with legal consequence, so the direction that matters is FALSE
   PERMISSION: any mutation turning an unknown or prohibited composition into YES must
   be caught. A false UNSTATED is inconvenient; a false YES is harmful.
   license/model.json (SPEC §12.3 LICENSE-MODEL-PUBLISHED) is the NAMED model those
   verdicts are relative to. It is an INPUT, not an expectation: the specification
   deliberately does not fix the table, so without this file the vectors would be the
   only description of it and every row no vector exercises would be unconstrained.
10. envelope/vectors.jsonl (SPEC §8.6): every "canonical" record's octets reproduce
   EXACTLY, every "reject" record is refused, and every "signature" record verifies
   or fails as its verdict says. These octets are what a publication signature is
   computed over, so one differing byte makes signatures from that kernel
   unverifiable elsewhere. JSONL with base64 octets so reading the fixtures needs no
   knowledge of the reference language.

Files: hashes.txt, canonical/, encoding/, gate/, verify/, analyses/,
prove/outcomes.json, campaign/vectors.txt, envelope/vectors.jsonl,
gate/bytes/, license/vectors.jsonl.
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

// writeEnvelopeVectors emits fixtures/envelope/vectors.jsonl. Every vector is
// SELF-VALIDATED before it is written: a reject case this kernel accepts, or an
// accept case it cannot reproduce, fails generation rather than shipping a false
// obligation to an implementer who has no other source to check it against.
func writeEnvelopeVectors(write func(string, []byte) error) error {
	var out strings.Builder
	emit := func(v map[string]any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out.Write(b)
		out.WriteByte('\n')
		return nil
	}

	// envJSON renders the STRUCTURED half of a vector. Two defects lived here, both
	// found by an independent implementation that could not reproduce the octets:
	//
	//  - `license` was absent, so the structured half carried oath-publish/1's key
	//    set while octets_b64 carried a /2 envelope with seven lines. The record
	//    could not produce its own octets — the exact defect §8.6.1's own note
	//    congratulates itself for catching IN THE PROSE, fixed there and left here.
	//  - `parent_rev` was emitted as a JSON NUMBER. The vector labelled "arbitrary
	//    precision, not a machine word" carries 2^128+1, and a float64 JSON reader
	//    (Go into interface{}, JavaScript JSON.parse) decodes it OFF BY ONE. The
	//    witness for the bignum rule was defeated by its own carrier, and the file
	//    was internally inconsistent: `store` records already used a string.
	envJSON := func(e pubEnvelope) map[string]any {
		return map[string]any{"op": e.Op, "name": e.Name, "artifact": e.Artifact,
			"parent": e.Parent, "parent_rev": e.ParentRev.String(), "author": e.Author,
			"license": e.License}
	}

	// --- canonical: structured envelope -> exact octets -------------------------
	canon := []struct {
		label string
		env   pubEnvelope
	}{
		{"first publication (parent sentinel, revision 0)", pubEnvelope{Op: "put", Name: "double",
			Artifact: strings.Repeat("11", 32), Parent: noParent, ParentRev: firstRev(), Author: strings.Repeat("aa", 32), License: "MIT"}},
		{"repoint with a real parent", pubEnvelope{Op: "put", Name: "double",
			Artifact: strings.Repeat("22", 32), Parent: strings.Repeat("11", 32), ParentRev: revOf(1), Author: strings.Repeat("bb", 32), License: "MIT OR Apache-2.0"}},
		{"namespaced name, multi-digit revision", pubEnvelope{Op: "put", Name: "michael/service1/verify-webhook",
			Artifact: strings.Repeat("33", 32), Parent: strings.Repeat("44", 32), ParentRev: revOf(1042), Author: strings.Repeat("cc", 32), License: "GPL-3.0 WITH Classpath-exception-2.0"}},
		// The three rules the §8 amendment newly specified were exactly the three no
		// vector exercised — prose added in response to an audit is especially likely
		// to need a fixture immediately, because nothing has ever tested it.
		{"name containing '=' (lines split at the FIRST '=')", pubEnvelope{Op: "put", Name: "eq=name",
			Artifact: strings.Repeat("55", 32), Parent: noParent, ParentRev: firstRev(), Author: strings.Repeat("dd", 32), License: noLicense}},
		{"non-ASCII name (octets are UTF-8, emitted literally)", pubEnvelope{Op: "put", Name: "café/λ-fold",
			Artifact: strings.Repeat("66", 32), Parent: noParent, ParentRev: firstRev(), Author: strings.Repeat("ee", 32), License: "Apache-2.0"}},
		{"parent_rev beyond 2^64 (arbitrary precision, not a machine word)", pubEnvelope{Op: "put", Name: "deep",
			Artifact: strings.Repeat("77", 32), Parent: strings.Repeat("88", 32),
			ParentRev: bigRev("340282366920938463463374607431768211457"), Author: strings.Repeat("ff", 32), License: noLicense}},
	}
	for _, c := range canon {
		octets := envelopeEncode(c.env)
		back, err := envelopeParse(octets)
		if err != nil || !back.equal(c.env) {
			return fmt.Errorf("canonical envelope vector %q does not round-trip through this kernel: %v", c.label, err)
		}
		if err := emit(map[string]any{"kind": "canonical", "label": c.label,
			"envelope": envJSON(c.env), "octets_b64": encodeEnvelopeB64(octets)}); err != nil {
			return err
		}
	}

	// --- reject: octets a conformant parser MUST refuse -------------------------
	hex64, key64 := strings.Repeat("ab", 32), strings.Repeat("aa", 32)
	valid := "oath-publish/1\nop=put\nname=n\nartifact=" + hex64 + "\nparent=-\nparent_rev=0\nauthor=" + key64 + "\n"
	rejects := []struct{ label, witnesses, octets, reason string }{
		{"uppercase hex artifact", "ENV-HEX-LOWERCASE", strings.Replace(valid, hex64, strings.ToUpper(hex64), 1),
			"hashes are compared as bytes, so ABAB… and abab… would be two statements about one artifact"},
		{"non-canonical revision (leading zero)", "ENV-REV-CANONICAL",
			"oath-publish/1\nop=put\nname=n\nartifact=" + hex64 + "\nparent=" + strings.Repeat("cd", 32) + "\nparent_rev=01\nauthor=" + key64 + "\n",
			"\"01\" and \"1\" would be different bytes for one revision"},
		{"parent sentinel with nonzero revision", "ENV-PARENT-CONSISTENT", strings.Replace(valid, "parent_rev=0", "parent_rev=2", 1),
			"a first publication has both the sentinel and revision 0, or neither"},
		{"parent hash with revision 0", "ENV-PARENT-CONSISTENT", strings.Replace(valid, "parent=-", "parent="+strings.Repeat("cd", 32), 1),
			"same consistency rule from the other side"},
		{"LF in a value breaks line framing", "ENV-FIELD-COUNT", strings.Replace(valid, "name=n", "name=a\nb", 1),
			"an injected LF becomes a line break, so this is caught as 8 lines rather than 7; the value rule proper is an ENCODER obligation and is unreachable from the parse side"},
		{"reordered fields", "ENV-FIELD-ORDER", "oath-publish/1\nname=n\nop=put\nartifact=" + hex64 + "\nparent=-\nparent_rev=0\nauthor=" + key64 + "\n",
			"field order is part of the canonical encoding"},
		{"missing trailing newline", "ENV-LINE-LF", strings.TrimSuffix(valid, "\n"),
			"framing is part of the encoding; a truncated envelope is not a shorter valid one"},
		{"unknown extra field", "ENV-FIELD-COUNT", valid + "extra=1\n", "an unknown key is a second spelling of the same statement"},
		{"wrong format version", "ENV-TAG", strings.Replace(valid, "oath-publish/1", "oath-publish/2", 1),
			"the version is inside the signed octets, so a signature under one format cannot be read under another"},
	}
	for _, r := range rejects {
		if _, err := envelopeParse([]byte(r.octets)); err == nil {
			return fmt.Errorf("envelope reject vector %q is ACCEPTED by this kernel: the fixture would assert an obligation the reference implementation does not meet", r.label)
		}
		if r.witnesses != "" && !ruleKnown(r.witnesses) {
			return fmt.Errorf("reject vector %q declares witnesses=%q, which is not a known normative rule", r.label, r.witnesses)
		}
		if err := emit(map[string]any{"kind": "reject", "label": r.label, "witnesses": r.witnesses,
			"octets_b64": encodeEnvelopeB64([]byte(r.octets)), "reason": r.reason}); err != nil {
			return err
		}
	}

	// --- signature: the whole path, end to end ---------------------------------
	//
	// Pins every step a kernel must reproduce: structured envelope -> canonical
	// octets -> envelope_b64 as stored -> signature over the DECODED octets ->
	// canonical journal line. Plus one tamper vector per signed field, and byte-level
	// alternate spellings that must not be accepted.
	//
	// The key is derived from a FIXED seed so the vectors are reproducible; Ed25519
	// signing is deterministic, so the signature is a function of (seed, octets) with
	// no randomness to pin separately.
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pubHex := hex.EncodeToString(priv.Public().(ed25519.PublicKey))

	env := pubEnvelope{Op: "put", Name: "double", Artifact: strings.Repeat("55", 32),
		Parent: strings.Repeat("66", 32), ParentRev: revOf(7), Author: pubHex, License: noLicense}
	octets := envelopeEncode(env)
	sig, err := envelopeSign(priv, env)
	if err != nil {
		return err
	}
	if err := envelopeVerify(env, sig); err != nil {
		return fmt.Errorf("signature vector does not verify in this kernel: %w", err)
	}
	entry := &LogEntry{Author: pubHex, Name: env.Name, Kind: "func", Status: "accepted",
		Hash: env.Artifact, Prev: env.Parent, Guarantee: "tested (200 cases per property)",
		Termination: "nonrecursive", EnvelopeB64: encodeEnvelopeB64(octets),
		AuthorPubkey: pubHex, AuthorSig: sig, NameTransition: transitionApplied}
	line, err := canonicalJournalLine(entry)
	if err != nil {
		return err
	}
	if err := emit(map[string]any{"kind": "signature", "label": "honest publication",
		"seed_b64": encodeEnvelopeB64(seed), "envelope": envJSON(env),
		"octets_b64": encodeEnvelopeB64(octets), "envelope_b64": entry.EnvelopeB64,
		"author_pubkey": pubHex, "author_sig": sig,
		"journal_line_b64": encodeEnvelopeB64(line), "verdict": "accept"}); err != nil {
		return err
	}

	// One tamper vector per SIGNED field: each must fail verification.
	for _, t := range []struct {
		field string
		mut   func(*pubEnvelope)
	}{
		{"op", func(e *pubEnvelope) { e.Op = "put " }},
		{"name", func(e *pubEnvelope) { e.Name = "other" }},
		{"artifact", func(e *pubEnvelope) { e.Artifact = strings.Repeat("99", 32) }},
		{"parent", func(e *pubEnvelope) { e.Parent = strings.Repeat("77", 32) }},
		{"parent_rev", func(e *pubEnvelope) { e.ParentRev = revOf(8) }},
		{"author", func(e *pubEnvelope) { e.Author = strings.Repeat("dd", 32) }},
	} {
		bad := env
		t.mut(&bad)
		// `op` and `author` tampering may make the envelope invalid outright, which is
		// also a rejection — record the octets either way so a kernel can check that it
		// refuses, whether at validation or at signature verification.
		var badOct []byte
		if bad.validate() == nil {
			badOct = envelopeEncode(bad)
			if envelopeVerify(bad, sig) == nil {
				return fmt.Errorf("tamper vector for %q still verifies: the field is not bound by the signature", t.field)
			}
		} else {
			badOct = []byte(strings.Replace(string(octets), "op=put\n", "op=put \n", 1))
		}
		if err := emit(map[string]any{"kind": "signature", "label": "tampered " + t.field,
			"tampered_field": t.field, "octets_b64": encodeEnvelopeB64(badOct),
			"author_pubkey": pubHex, "author_sig": sig, "verdict": "reject",
			"reason": "the signature is over the original octets; every field is bound"}); err != nil {
			return err
		}
	}

	// §8.6.4a vectors. This section previously had NONE, which is how a blind
	// implementation could pass every vector while using a permissive Ed25519 library
	// that ignored the convention entirely. Each is self-validated below.
	//
	// MALLEABILITY: S+L is a second encoding of the same signature. Accepting it would
	// mean two distinct byte strings both "the author's signature" over one statement,
	// which contradicts the envelope being a singular statement.
	sigBytes, _ := hex.DecodeString(sig)
	malleable := append([]byte(nil), sigBytes...)
	// L = 2^252 + 27742317777372353535851937790883648493, little-endian.
	order := []byte{0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2,
		0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x10}
	var carry uint16
	for i := 0; i < 32; i++ {
		v := uint16(malleable[32+i]) + uint16(order[i]) + carry
		malleable[32+i] = byte(v & 0xff)
		carry = v >> 8
	}
	malHex := hex.EncodeToString(malleable)
	if envelopeVerify(env, malHex) == nil {
		return fmt.Errorf("malleability vector VERIFIES: a non-canonical S is being accepted, so one statement has two valid signatures")
	}
	if err := emit(map[string]any{"kind": "signature", "label": "non-canonical S (S+L malleability)",
		"octets_b64": encodeEnvelopeB64(octets), "author_pubkey": pubHex, "author_sig": malHex,
		"verdict": "reject",
		"reason":  "S MUST be canonical (< L); S+L is a second encoding of one signature (SPEC §8.6.4a)"}); err != nil {
		return err
	}

	// SMALL-ORDER KEY. This vector previously used the all-zero encoding, described it
	// as the identity point, and paired it with a signature made under a REAL key —
	// three mistakes compounding into a vector that constrained nothing:
	//
	//   - all-zeros is y=0, a point of ORDER 4. The identity is 0100…00. The
	//     derivation script prints the orders and I mislabelled it anyway;
	//   - a real key's signature fails the curve equation under any other key, so a
	//     verifier with NO small-order rule at all returns `reject` and passes;
	//   - and the order-4 key does not admit the forgery the rule exists to stop.
	//
	// The witnessing vector is a closed form needing no private key and no curve code:
	// A = identity, R = identity, S = 0 satisfies [S]B = R + [k]A for ANY message,
	// because [0]B is the identity and identity + [k]identity is the identity. Go's
	// crypto/ed25519 accepts it. Only the small-order rule stops it, so a kernel
	// lacking that rule now FAILS this vector instead of passing it.
	identKey := make([]byte, ed25519.PublicKeySize)
	identKey[0] = 0x01
	identHex := hex.EncodeToString(identKey)
	forged := make([]byte, ed25519.SignatureSize)
	copy(forged[:32], identKey) // R = identity, S = 0
	forgedHex := hex.EncodeToString(forged)
	identEnv := env
	identEnv.Author = identHex
	identOct := envelopeEncode(identEnv)
	if envelopeVerify(identEnv, forgedHex) == nil {
		return fmt.Errorf("universal-forgery vector VERIFIES: the identity key is accepted as an author, so any party can forge under it")
	}
	// Self-check that the vector is not vacuous: the raw primitive MUST accept this,
	// or the vector would pass for reasons unrelated to the rule.
	if !ed25519.Verify(ed25519.PublicKey(identKey), []byte("anything"), forged) {
		return fmt.Errorf("universal-forgery vector is vacuous: the primitive rejects it for unrelated reasons, so it does not witness the small-order rule")
	}
	if err := emit(map[string]any{"kind": "signature", "label": "small-order author key (identity) with a universal forgery",
		"octets_b64": encodeEnvelopeB64(identOct), "author_pubkey": identHex, "author_sig": forgedHex,
		"verdict": "reject",
		"reason":  "A = identity with R = identity and S = 0 satisfies the verification equation for ANY message with no private key; ONLY the §8.6.4a small-order rule refuses it, so a kernel without that rule fails this vector"}); err != nil {
		return err
	}
	// And an order-4 small-order key, so the rule is witnessed beyond the identity.
	order4 := strings.Repeat("00", 32)
	o4Env := env
	o4Env.Author = order4
	if envelopeVerify(o4Env, sig) == nil {
		return fmt.Errorf("order-4 key vector VERIFIES")
	}
	if err := emit(map[string]any{"kind": "signature", "label": "small-order author key (order 4, y=0)",
		"octets_b64": encodeEnvelopeB64(envelopeEncode(o4Env)), "author_pubkey": order4, "author_sig": sig,
		"verdict": "reject",
		"reason":  "y=0 is a point of order 4 — small-order regardless of whether it admits a forgery (SPEC §8.6.4a)"}); err != nil {
		return err
	}

	// Byte-level alternate spellings of the STORED field: same octets, different text.
	for _, alt := range []struct{ label, b64, reason string }{
		{"envelope_b64 with leading whitespace", " " + entry.EnvelopeB64, "not standard padded base64"},
		{"envelope_b64 unpadded", strings.TrimRight(entry.EnvelopeB64, "="), "padding is required by the pinned dialect"},
	} {
		if _, derr := decodeEnvelopeB64(alt.b64); derr == nil {
			return fmt.Errorf("alternate base64 spelling %q is accepted by this kernel", alt.label)
		}
		if err := emit(map[string]any{"kind": "reject", "label": alt.label,
			"envelope_b64": alt.b64, "reason": alt.reason}); err != nil {
			return err
		}
	}

	// --- store: the three §8.6.4 store-side MUSTs ------------------------------
	//
	// These had NO fixture shape, which meant half of §8.6's normative weight was
	// unwitnessed: a kernel could pass every other vector without implementing any of
	// the acceptance preconditions. Expressing them needs a STATE plus a REQUEST, not
	// a single record — what the store currently believes, and what is being asked of
	// it.
	//
	// Every verdict below is self-validated against checkPublication, the same
	// function the put path uses. That matters: a generator that re-implements the
	// rule it witnesses can agree with itself while both are wrong.
	storeName := "double"
	storeArtifact := strings.Repeat("55", 32)
	curParent, curRev := strings.Repeat("66", 32), 7
	type storeCase struct {
		label, reason string
		env           pubEnvelope
		sig           string
		principal     string
		artifact      string // what the store recomputes from the submitted content
		parent        string
		rev           int
		accept        bool
	}
	good := pubEnvelope{Op: "put", Name: storeName, Artifact: storeArtifact,
		Parent: curParent, ParentRev: revOf(curRev), Author: pubHex, License: noLicense}
	goodSig, err := envelopeSign(priv, good)
	if err != nil {
		return err
	}
	// A SECOND key from a fixed seed. GenerateKey(nil) is random, which makes the
	// emitted fixture differ between runs — fixtures must be reproducible or they
	// cannot be a conformance target, and the determinism test rightly rejects them.
	otherSeed := make([]byte, ed25519.SeedSize)
	for i := range otherSeed {
		otherSeed[i] = byte(0xa0 + i)
	}
	other := ed25519.NewKeyFromSeed(otherSeed).Public().(ed25519.PublicKey)
	cases := []storeCase{
		{label: "current statement authorises the transition",
			reason: "signer is the authenticated principal, artifact recomputes, parent and revision are current",
			env:    good, sig: goodSig, principal: pubHex, artifact: storeArtifact, parent: curParent, rev: curRev, accept: true},
		{label: "signer is not the authenticated principal",
			reason: "the signing key MUST be the authenticated principal; verifying against the envelope's own author would always succeed, so any caller could replay anyone's statement",
			env:    good, sig: goodSig, principal: hex.EncodeToString(other), artifact: storeArtifact, parent: curParent, rev: curRev},
		{label: "submitted content hashes to a different artifact",
			reason: "the artifact hash MUST be recomputed from submitted content, making client/store agreement on identity an enforced precondition",
			env:    good, sig: goodSig, principal: pubHex, artifact: strings.Repeat("99", 32), parent: curParent, rev: curRev},
		{label: "signed parent is stale (name moved)",
			reason: "the signed parent MUST be the name's current binding; otherwise a captured envelope replays against a newer state",
			env:    good, sig: goodSig, principal: pubHex, artifact: storeArtifact, parent: strings.Repeat("77", 32), rev: curRev},
		{label: "signed revision is stale though the parent hash matches (ABA)",
			reason: "the revision MUST also be current; a hash can return to an earlier value, a revision cannot",
			env:    good, sig: goodSig, principal: pubHex, artifact: storeArtifact, parent: curParent, rev: curRev + 2},
	}
	for _, c := range cases {
		gotErr := checkPublication(c.env, c.sig, c.principal, storeName, c.artifact, c.parent, c.rev)
		if c.accept && gotErr != nil {
			return fmt.Errorf("store vector %q should ACCEPT but this kernel refuses it: %v", c.label, gotErr)
		}
		if !c.accept && gotErr == nil {
			return fmt.Errorf("store vector %q should REJECT and this kernel accepts it: the precondition is not enforced", c.label)
		}
		verdict := "reject"
		if c.accept {
			verdict = "accept"
		}
		if err := emit(map[string]any{"kind": "store", "label": c.label,
			"state":   map[string]any{"name": storeName, "bound": c.parent, "parent_rev": fmt.Sprintf("%d", c.rev)},
			"request": map[string]any{"octets_b64": encodeEnvelopeB64(envelopeEncode(c.env)), "author_sig": c.sig, "authenticated_principal": c.principal, "recomputed_artifact": c.artifact},
			"verdict": verdict,
			"reason":  c.reason}); err != nil {
			return err
		}
	}

	return write(filepath.Join("envelope", "vectors.jsonl"), []byte(out.String()))
}

// bigRev parses a decimal revision for fixture construction, panicking on a literal
// this file got wrong — a malformed constant must fail generation loudly rather than
// emit a vector nobody can reproduce.
func bigRev(dec string) *big.Int {
	v, ok := new(big.Int).SetString(dec, 10)
	if !ok {
		panic("fixture revision literal is not decimal: " + dec)
	}
	return v
}

// writeHostileBytes emits fixtures/gate/bytes/: object encodings a conformant DECODER
// must refuse, each differing from a valid encoding in exactly one place.
func writeHostileBytes(write func(string, []byte) error) error {
	// A minimal definition carrying a literal 0, so the integer-canonicality rules are
	// reachable. Built in memory: `put` would write to whatever store is configured.
	zero := &Def{K: "func", Ty: tFun(tInt(), tInt()), Body: &Term{K: "int", Int: big.NewInt(0)}}
	valid := encodeDef(zero)
	if _, err := decodeDef(valid); err != nil {
		return fmt.Errorf("the baseline encoding does not decode, so no perturbation of it witnesses anything: %w", err)
	}

	// Locate the encoded zero: sign 0x00 followed by a u32 length of 0.
	idx := -1
	for i := 0; i+4 < len(valid); i++ {
		if valid[i] == 0x00 && valid[i+1] == 0 && valid[i+2] == 0 && valid[i+3] == 0 && valid[i+4] == 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("could not locate an encoded zero to perturb; the fixture would be vacuous")
	}

	type hostile struct {
		name, witnesses, why string
		mutate               func([]byte) []byte
	}
	cases := []hostile{
		{"negative_zero", "1/negative-zero",
			"sign=0x01 with a zero-length magnitude is a SECOND encoding of 0, so one value would have two content-addressed identities",
			func(b []byte) []byte { c := append([]byte(nil), b...); c[idx] = 0x01; return c }},
		{"bad_sign_byte", "1/integer-sign",
			"the sign byte is 0x00 or 0x01; any other value is malformed",
			func(b []byte) []byte { c := append([]byte(nil), b...); c[idx] = 0x07; return c }},
		{"trailing_bytes", "1/no-trailing-bytes",
			"a decoder must consume exactly the object; trailing bytes would let two byte strings denote one definition",
			func(b []byte) []byte { return append(append([]byte(nil), b...), 0x00) }},
		{"truncated", "1/complete-object",
			"a truncated object must be refused rather than decoded as a shorter one",
			func(b []byte) []byte { return append([]byte(nil), b[:len(b)-1]...) }},
	}

	var man strings.Builder
	man.WriteString("# Hostile OBJECT BYTES (SPEC §1). Each file is a real encoding with ONE\n")
	man.WriteString("# byte-level defect; a conformant decoder MUST refuse every one.\n")
	man.WriteString("# The unperturbed baseline decodes, so each vector isolates its own rule.\n")
	man.WriteString("# file\twitnesses\twhy\n")

	if err := write(filepath.Join("gate", "bytes", "baseline.bin"), valid); err != nil {
		return err
	}
	man.WriteString("baseline.bin\t(must ACCEPT)\tthe unperturbed encoding; if this is refused the vectors below prove nothing\n")

	for _, c := range cases {
		bad := c.mutate(valid)
		if _, err := decodeDef(bad); err == nil {
			return fmt.Errorf("hostile-bytes vector %q is ACCEPTED by this kernel: the fixture would assert an obligation the reference does not meet", c.name)
		}
		if len(bad) == len(valid) && bytesEqual(bad, valid) {
			return fmt.Errorf("hostile-bytes vector %q did not change the encoding", c.name)
		}
		if err := write(filepath.Join("gate", "bytes", c.name+".bin"), bad); err != nil {
			return err
		}
		fmt.Fprintf(&man, "%s.bin\t%s\t%s\n", c.name, c.witnesses, c.why)
	}
	return write(filepath.Join("gate", "bytes", "manifest.txt"), []byte(man.String()))
}

// writeLicenseVectors emits fixtures/license/vectors.jsonl.
// writeLicenseModel emits fixtures/license/model.json — SPEC §12.3
// LICENSE-MODEL-PUBLISHED. The model is deliberately NOT normative text (it is a
// policy artifact expected to be corrected), but it must be an input the
// specification points at. Without this file a reader with §12 and no fixtures
// cannot derive a single verdict, and an independent implementation is forced to
// reverse-engineer rows from the vectors — which quietly promotes the vectors to
// normative text and leaves every unexercised row unconstrained.
func writeLicenseModel(write func(string, []byte) error) error {
	// The bytes come from licenseModelBytes() so the model-digest bound into every
	// evaluation identity (§12.4) is a digest of exactly what this file publishes.
	// Serialising it twice would let the two drift apart silently.
	return write(filepath.Join("license", "model.json"), licenseModelBytes())
}

func writeLicenseVectors(write func(string, []byte) error) error {
	var out strings.Builder
	emit := func(v map[string]any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out.Write(b)
		out.WriteByte('\n')
		return nil
	}

	// Each case is a closure of asserted terms, root first.
	type lcase struct {
		label, witnesses string
		inputs           []string
		want             map[string]string
	}
	all := func(c, r, m, p, s string) map[string]string {
		return map[string]string{"commercial": c, "redistribute": r, "modify": m,
			"patent_grant": p, "share_alike": s}
	}
	cases := []lcase{
		{"all modelled and permissive", "", []string{"MIT", "MIT", "BSD-3-Clause"},
			all("YES", "YES", "YES", "UNSTATED", "NO")},
		{"one ABSENT assertion among permissive dependencies", "LICENSE-PERMISSION-UNKNOWN",
			[]string{"MIT", "-", "MIT"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		{"one UNKNOWN identifier among permissive dependencies", "LICENSE-LOOKUP-UNKNOWN",
			[]string{"MIT", "NotARealLicense-1.0"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		{"compound expression is not resolved", "LICENSE-LOOKUP-COMPOUND",
			[]string{"MIT OR Apache-2.0"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		{"a share-alike obligation propagates to the root", "LICENSE-OBLIGATION-YES",
			[]string{"MIT", "GPL-3.0-only"}, all("YES", "YES", "YES", "UNSTATED", "YES")},
		// The obligation dimension combined with an UNKNOWN input. Distinct from the
		// permission case: not knowing whether an obligation exists is not knowing there
		// is none, so it must read UNSTATED rather than NO.
		{"an obligation dimension with an unknown input", "LICENSE-OBLIGATION-UNKNOWN",
			[]string{"MIT", "NotModelled-2.0"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		// A licence that PROHIBITS. Until the model contained one, the permission
		// combiner's NO-dominance branch was unreachable and this rule was stated,
		// implemented and dead.
		{"a known prohibition binds the composition", "LICENSE-PERMISSION-NO",
			[]string{"MIT", "CC-BY-NC-4.0"}, all("NO", "YES", "YES", "UNSTATED", "NO")},
		// The EMPTY composition. Reachable only by mis-assembling a closure, but
		// the literal §12.2 tables grant everything on it, so the conservative
		// answer has to be witnessed rather than assumed.
		{"an empty composition grants nothing", "LICENSE-FOLD-NONEMPTY",
			[]string{}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		// EXACT MATCHING. The largest unconstrained surface in §12: with matching
		// undefined, a "helpfully normalising" registry turns an expression the
		// publisher never wrote into a full commercial grant, one layer BELOW the
		// fold that would otherwise have caught it.
		{"a case-variant identifier is not the identifier", "LICENSE-LOOKUP-EXACT",
			[]string{"MIT", "mit"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		// A TRAILING SPACE is the hazard §12.3 names whose lenient reading yields a
		// FULL COMMERCIAL GRANT rather than another UNSTATED. The other two
		// case-variants fail safe; this one does not, and it was the only one of
		// the three with no vector.
		{"a trailing space is not trimmed before lookup", "LICENSE-LOOKUP-EXACT",
			[]string{"MIT", "MIT "}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		{"surrounding punctuation is not stripped before lookup", "LICENSE-LOOKUP-EXACT",
			[]string{"MIT", "(MIT)"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		// PRECEDENCE. A compound whose left operand IS in the model: a lookup-first
		// implementation resolves it, which LICENSE-LOOKUP-COMPOUND forbids.

		// POLICY, the CONSTRAINT clause. An unrecognised policy selected the input
		// set by an unknown rule, so its verdict is not reproducible and must not
		// be reported as agreement. Distinct from the digest clause, which only
		// says the identity differs.
		{"an unrecognised policy is not evaluated as composition", "LICENSE-POLICY-DEFINED",
			[]string{"MIT", "MIT"}, all("UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED", "UNSTATED")},
		{"a transitive dependency changes the root result", "",
			[]string{"MIT", "MIT", "GPL-3.0-only"}, all("YES", "YES", "YES", "UNSTATED", "YES")},
	}

	for _, c := range cases {
		pol := licensePolicyComposition
		if c.witnesses == "LICENSE-POLICY-DEFINED" {
			pol = "not-a-policy"
		}
		if c.witnesses != "" && !ruleKnown(c.witnesses) {
			return fmt.Errorf("license vector %q declares unknown rule %q", c.label, c.witnesses)
		}
		got := evalFromAssertions(c.inputs)
		if pol != licensePolicyComposition {
			got = grants{} // §12.3: not evaluated under composition
		}
		want := c.want
		actual := map[string]string{"commercial": got.Commercial.String(), "redistribute": got.Redistribute.String(),
			"modify": got.Modify.String(), "patent_grant": got.PatentGrant.String(), "share_alike": got.ShareAlike.String()}
		for k, v := range want {
			if actual[k] != v {
				return fmt.Errorf("license vector %q expects %s=%s but this kernel derives %s — the fixture would assert a verdict the reference does not produce",
					c.label, k, v, actual[k])
			}
		}
		if err := emit(map[string]any{"kind": "evaluation", "label": c.label, "witnesses": c.witnesses,
			"policy": pol, "engine": licenseEngine, "model": licenseModelVersion,
			"assertions": c.inputs, "expect": want}); err != nil {
			return err
		}
	}

	// PRECEDENCE, with a model that CONTAINS the compound key. This is the only
	// shape that separates LICENSE-LOOKUP-PRECEDENCE from LICENSE-LOOKUP-COMPOUND:
	// when the model lacks the key both rules reject it and each hides the other's
	// removal. §12.3 permits any set of identifiers, so this model is legal — and
	// a lookup-first implementation RESOLVES it, granting terms the consumer never
	// chose between.
	compoundModel := map[string]map[string]string{
		"MIT":                {"commercial": "YES", "redistribute": "YES", "modify": "YES", "patent_grant": "UNSTATED", "share_alike": "NO"},
		"MIT AND GPL-3.0-only": {"commercial": "YES", "redistribute": "YES", "modify": "YES", "patent_grant": "YES", "share_alike": "YES"},
	}
	cm := map[string]grants{}
	for k, r := range compoundModel {
		cm[k] = grants{triFrom(r["commercial"]), triFrom(r["redistribute"]), triFrom(r["modify"]),
			triFrom(r["patent_grant"]), triFrom(r["share_alike"])}
	}
	if g := evalFromAssertionsIn(cm, []string{"MIT AND GPL-3.0-only"}); g.Commercial != triUnstated {
		return fmt.Errorf("a compound key present in the model was RESOLVED (commercial=%s); the compound test must precede the lookup", g.Commercial)
	}
	if err := emit(map[string]any{"kind": "evaluation",
		"label": "a compound present in the model is still not resolved",
		"witnesses": "LICENSE-LOOKUP-PRECEDENCE", "policy": licensePolicyComposition,
		"engine": licenseEngine, "model": licenseModelVersion,
		"model_licenses": compoundModel, "assertions": []string{"MIT AND GPL-3.0-only"},
		"expect": map[string]string{"commercial": "UNSTATED", "redistribute": "UNSTATED",
			"modify": "UNSTATED", "patent_grant": "UNSTATED", "share_alike": "UNSTATED"}}); err != nil {
		return err
	}

	// MODEL SCHEMA. A row with a MISSING dimension and an out-of-vocabulary value.
	// Both read UNSTATED; the lenient readings (absent obligation = NO, "yes" =
	// YES) are the natural ones, and both grant terms nobody wrote.
	sloppyModel := map[string]map[string]string{
		"Sloppy-1.0": {"commercial": "yes", "redistribute": "YES", "modify": "YES",
			"patent_grant": "UNSTATED"}, // share_alike absent; commercial wrong case
	}
	sm := map[string]grants{}
	for k, r := range sloppyModel {
		sm[k] = grants{triFrom(r["commercial"]), triFrom(r["redistribute"]), triFrom(r["modify"]),
			triFrom(r["patent_grant"]), triFrom(r["share_alike"])}
	}
	if g := evalFromAssertionsIn(sm, []string{"Sloppy-1.0"}); g.Commercial != triUnstated || g.ShareAlike != triUnstated {
		return fmt.Errorf("a malformed model row was read leniently (commercial=%s share_alike=%s); an absent or out-of-vocabulary cell must read UNSTATED",
			g.Commercial, g.ShareAlike)
	}
	if err := emit(map[string]any{"kind": "evaluation",
		"label": "a malformed model row grants nothing",
		"witnesses": "LICENSE-MODEL-SCHEMA", "policy": licensePolicyComposition,
		"engine": licenseEngine, "model": licenseModelVersion,
		"model_licenses": sloppyModel, "assertions": []string{"Sloppy-1.0"},
		"expect": map[string]string{"commercial": "UNSTATED", "redistribute": "YES",
			"modify": "YES", "patent_grant": "UNSTATED", "share_alike": "UNSTATED"}}); err != nil {
		return err
	}

	// IDENTITY vectors: the digest must bind method and inputs, and must not depend on
	// order. Without these, changing the lattice next year would silently reinterpret
	// every historical verdict.
	// Members are ARTIFACT HASHES (§12.4 LICENSE-IDENTITY-ARTIFACT). Fixed literals
	// rather than corpus hashes, so the vector family stays a statement about the
	// RULES rather than about this store.
	const artA = "1111111111111111111111111111111111111111111111111111111111111111"
	const artB = "2222222222222222222222222222222222222222222222222222222222222222"
	const pubA = "aaaa000000000000000000000000000000000000000000000000000000000000"
	const pubB = "bbbb000000000000000000000000000000000000000000000000000000000000"
	baseInputs := []licenseInput{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"},
		{Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}
	base := licenseEvaluation{Policy: licensePolicyComposition, Engine: licenseEngine,
		Model: licenseModelVersion, ModelDigest: licenseModelDigest(),
		Subject: artA, Inputs: baseInputs}
	baseDigest := evaluationDigest(base)
	if err := emit(map[string]any{"kind": "identity", "label": "evaluation digest over method and sorted inputs",
		"witnesses": "LICENSE-IDENTITY-INPUT", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "pairs": []licensePair{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"}, {Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}, "assertions": []string{}, "digest": baseDigest}); err != nil {
		return err
	}
	rev := base
	rev.Inputs = []licenseInput{baseInputs[1], baseInputs[0]}
	if evaluationDigest(rev) != baseDigest {
		return fmt.Errorf("input order changed the evaluation digest; the same evaluation would appear to be two")
	}
	// The model version must be bound, or changing the lattice would silently
	// reinterpret every historical verdict rather than producing a new one.
	modelChanged := base
	modelChanged.Model = "spdx-lattice/99"
	if evaluationDigest(modelChanged) == baseDigest {
		return fmt.Errorf("changing the model version did not change the digest; historical verdicts would be reinterpreted rather than superseded")
	}
	if err := emit(map[string]any{"kind": "identity", "label": "changing the model changes the evaluation",
		"witnesses": "LICENSE-MODEL-VERSIONED", "policy": base.Policy, "engine": base.Engine,
		"model": modelChanged.Model, "model_digest": modelChanged.ModelDigest, "subject": modelChanged.Subject, "pairs": []licensePair{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"}, {Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}, "assertions": []string{},
		"digest": evaluationDigest(modelChanged)}); err != nil {
		return err
	}
	// ENGINE and POLICY vary independently. LICENSE-IDENTITY-INPUT names all three
	// of engine, policy and inputs, and disabling the WHOLE rule is caught — but a
	// kernel that hardcoded engine or policy while computing inputs correctly still
	// passed every vector, because only `model` had a varying one. A rule covering
	// three dimensions needs a vector per dimension; whole-rule disablement measures
	// the coarsest possible mutation and quietly certifies the rest.
	for _, d := range []struct{ field, label, engine, policy string }{
		{"engine", "changing the engine changes the evaluation", "oath-license/99", base.Policy},
		{"policy", "changing the policy changes the evaluation", base.Engine, "closure-only"},
	} {
		v := base
		v.Engine, v.Policy = d.engine, d.policy
		dig := evaluationDigest(v)
		if dig == baseDigest {
			return fmt.Errorf("changing the %s did not change the digest; two different evaluation methods would be indistinguishable", d.field)
		}
		if err := emit(map[string]any{"kind": "identity", "label": d.label,
			"witnesses": "LICENSE-IDENTITY-INPUT", "policy": v.Policy, "engine": v.Engine,
			"model": v.Model, "model_digest": v.ModelDigest, "subject": v.Subject, "pairs": []licensePair{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"}, {Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}, "assertions": []string{}, "digest": dig}); err != nil {
			return err
		}
	}

	// The CHARACTER RULE clause. §12.4's LICENSE-IDENTITY-UNAMBIGUOUS has TWO
	// clauses and only the two-line split was witnessed; the clause the prose calls
	// "a forgery surface" had no vector at all. It is reachable against a PUBLISHED
	// digest: a single assertion whose expression embeds LF plus two input lines
	// reproduces the two-member digest above exactly. A conformant-looking kernel
	// that omits the character rule therefore forges a real evaluation identity.
	forgery := licenseEvaluation{Policy: base.Policy, Engine: base.Engine, Model: base.Model,
		ModelDigest: base.ModelDigest,
		Inputs: []licenseInput{{Artifact: artA, Publication: pubA,
			License: "MIT\ninput-artifact=" + artB + "\ninput-publication=" + pubB + "\ninput-license=Apache-2.0"}}}
	if d := evaluationDigest(forgery); d != "" {
		return fmt.Errorf("the character rule did not refuse an assertion embedding LF (got digest %s); one assertion can forge another composition's identity", shortHash(d))
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "an embedded newline cannot forge a two-input digest",
		"witnesses": "LICENSE-IDENTITY-UNAMBIGUOUS", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{},
		"pairs": []licensePair{{Artifact: artA, Publication: pubA, License: "MIT\ninput-artifact=" + artB + "\ninput-publication=" + pubB + "\ninput-license=Apache-2.0"}},
		"expect_rejected": true}); err != nil {
		return err
	}

	// INPUT COMPLETENESS. Under the reading that skips members asserting nothing,
	// {a:MIT, b:(none)} encodes exactly as {a:MIT} — one identity covering two
	// compositions whose VERDICTS DIFFER (all-UNSTATED against a commercial grant).
	// Both readings passed every vector until this one existed.
	withNone := licenseEvaluation{Policy: base.Policy, Engine: base.Engine, Model: base.Model,
		ModelDigest: base.ModelDigest, Subject: base.Subject,
		Inputs:      []licenseInput{{Artifact: artA, Publication: pubA, License: "MIT"}, {Artifact: artB, Publication: pubB, License: "-"}}}
	alone := withNone
	alone.Inputs = []licenseInput{{Artifact: artA, Publication: pubA, License: "MIT"}}
	if evaluationDigest(withNone) == evaluationDigest(alone) {
		return fmt.Errorf("a member asserting nothing was dropped from the digest: {a:MIT, b:(none)} and {a:MIT} share an identity while their verdicts differ")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "a member asserting nothing still contributes an input pair",
		"witnesses": "LICENSE-INPUT-COMPLETE", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject,
		"assertions": []string{}, "pairs": []licensePair{{Artifact: artA, Publication: pubA, License: "MIT"}, {Artifact: artB, Publication: pubB, License: "-"}},
		"digest": evaluationDigest(withNone)}); err != nil {
		return err
	}

	// POLICY. An unrecognised policy is not reproducible; it must never be
	// evaluated as `composition` and reported as agreement.
	unk := base
	unk.Policy = "not-a-policy"
	if evaluationDigest(unk) == baseDigest {
		return fmt.Errorf("an unrecognised policy produced the composition digest; a different selection rule would be reported as the same evaluation")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "an unrecognised policy is a different evaluation",
		"witnesses": "LICENSE-POLICY-DEFINED", "policy": unk.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject,
		"pairs": []licensePair{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"}, {Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}, "assertions": []string{}, "digest": evaluationDigest(unk)}); err != nil {
		return err
	}

	// ARTIFACT IDENTITY. The same artifacts reached by DIFFERENT names are the same
	// evaluation. Binding names would make this pair differ, and that is the defect:
	// nothing about the evaluated software changed, only the route by which the
	// closure was located.
	renamed := base
	renamed.Inputs = []licenseInput{
		{Artifact: artA, Publication: pubA, Name: "service", License: "MIT"},
		{Artifact: artB, Publication: pubB, Name: "vendored-lib", License: "Apache-2.0"}}
	if evaluationDigest(renamed) != baseDigest {
		return fmt.Errorf("renaming a member changed the evaluation digest; identity is following the discovery path rather than the artifacts")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "renaming members does not change the evaluation",
		"witnesses": "LICENSE-IDENTITY-ARTIFACT", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{},
		"pairs": []licensePair{
			{Artifact: artA, Publication: pubA, Name: "service", License: "MIT"},
			{Artifact: artB, Publication: pubB, Name: "vendored-lib", License: "Apache-2.0"}},
		"digest": baseDigest}); err != nil {
		return err
	}

	// PUBLICATION IDENTITY. Same artifact, same asserted terms, DIFFERENT publisher.
	// Two grants over the same bytes, which must not share an identity — otherwise
	// an evaluation cannot say whose grant it relied on.
	otherPub := base
	otherPub.Inputs = []licenseInput{
		{Artifact: artA, Publication: pubB, Name: "a", License: "MIT"},
		{Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}
	if evaluationDigest(otherPub) == baseDigest {
		return fmt.Errorf("the same terms asserted by a different publication produced the same digest; the evaluation cannot say whose grant it consumed")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "the same terms from a different publication is a different evaluation",
		"witnesses": "LICENSE-IDENTITY-PUBLICATION", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{},
		"pairs": []licensePair{
			{Artifact: artA, Publication: pubB, Name: "a", License: "MIT"},
			{Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}},
		"digest": evaluationDigest(otherPub)}); err != nil {
		return err
	}

	// INPUT-COMPLETE clause 2: a member appearing TWICE contributes twice. No
	// identity vector had a duplicated member, and the fold is idempotent, so
	// deduplicating passed every vector while changing what a composition is.
	dup := base
	dup.Inputs = []licenseInput{
		{Artifact: artA, Publication: pubA, License: "MIT"},
		{Artifact: artA, Publication: pubA, License: "MIT"}}
	single := dup
	single.Inputs = dup.Inputs[:1]
	if evaluationDigest(dup) == evaluationDigest(single) {
		return fmt.Errorf("a member appearing twice was deduplicated; two different compositions share an identity")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "a member appearing twice contributes twice",
		"witnesses": "LICENSE-INPUT-COMPLETE", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{},
		"pairs": []licensePair{{Artifact: artA, Publication: pubA, License: "MIT"},
			{Artifact: artA, Publication: pubA, License: "MIT"}},
		"digest": evaluationDigest(dup)}); err != nil {
		return err
	}

	// ORDER clause 2: artifact hash alone is NOT a total order. The same artifact
	// under two publications, presented both ways, must hash identically — the
	// tie-break is what makes that true, and nothing exercised it.
	tie := base
	tie.Inputs = []licenseInput{
		{Artifact: artA, Publication: pubA, License: "MIT"},
		{Artifact: artA, Publication: pubB, License: "Apache-2.0"}}
	tieRev := tie
	tieRev.Inputs = []licenseInput{tie.Inputs[1], tie.Inputs[0]}
	if evaluationDigest(tie) != evaluationDigest(tieRev) {
		return fmt.Errorf("two publications of ONE artifact hashed differently by presentation order; sorting on artifact alone is not a total order")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "one artifact under two publications sorts deterministically",
		"witnesses": "LICENSE-ORDER-INDEPENDENT", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{},
		"pairs": []licensePair{{Artifact: artA, Publication: pubB, License: "Apache-2.0"},
			{Artifact: artA, Publication: pubA, License: "MIT"}},
		"digest": evaluationDigest(tie)}); err != nil {
		return err
	}

	// The publication SENTINEL. A member whose publication cannot be determined
	// encodes `-`; the reference previously emitted an undocumented `unpublished`,
	// making two published vectors irreproducible from the normative text.
	nopub := base
	nopub.Inputs = []licenseInput{{Artifact: artA, License: "MIT"}}
	explicit := base
	explicit.Inputs = []licenseInput{{Artifact: artA, Publication: noLicense, License: "MIT"}}
	if evaluationDigest(nopub) != evaluationDigest(explicit) {
		return fmt.Errorf("an absent publication did not encode as the sentinel; the encoding uses a value this specification does not define")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "an undeterminable publication encodes the sentinel",
		"witnesses": "LICENSE-PUBLICATION-SENTINEL", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{},
		"pairs": []licensePair{{Artifact: artA, Publication: "-", License: "MIT"}},
		"digest": evaluationDigest(nopub)}); err != nil {
		return err
	}

	// U+2028. The clause was stated in response to an audit and had NO vector: a
	// stated-but-unwitnessed forgery surface is the worst combination, because an
	// implementation excluding only control octets passes everything.
	uniForge := base
	uniForge.Inputs = []licenseInput{{Artifact: artA, Publication: pubA,
		License: "MIT\u2028input-artifact=" + artB + "\u2028input-publication=" + pubB + "\u2028input-license=Apache-2.0"}}
	if evaluationDigest(uniForge) != "" {
		return fmt.Errorf("U+2028 was accepted in an asserted expression; a Unicode-aware reader would see a multi-member composition carrying a grant nobody published")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "U+2028 cannot forge additional input lines",
		"witnesses": "LICENSE-IDENTITY-UNAMBIGUOUS", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject,
		"assertions": []string{},
		"pairs": []licensePair{{Artifact: artA, Publication: pubA, License: uniForge.Inputs[0].License}},
		"expect_rejected": true}); err != nil {
		return err
	}

	// SUBJECT. The same closure evaluated ABOUT a different artifact is a
	// different evaluation — otherwise two entry points into one component, and
	// every empty closure, share an identity.
	otherSubj := base
	otherSubj.Subject = artB
	if evaluationDigest(otherSubj) == baseDigest {
		return fmt.Errorf("changing the evaluated artifact did not change the digest; the identity does not name what it is an evaluation of")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "the same closure about a different artifact is a different evaluation",
		"witnesses": "LICENSE-IDENTITY-SUBJECT", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": artB,
		"assertions": []string{},
		"pairs": []licensePair{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"},
			{Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}},
		"digest": evaluationDigest(otherSubj)}); err != nil {
		return err
	}

	// The MODEL CONTENT binding. Changing the lattice while holding the version
	// string fixed must change every identity, or historical verdicts are
	// reinterpreted rather than superseded.
	swapped := base
	swapped.ModelDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	if evaluationDigest(swapped) == baseDigest {
		return fmt.Errorf("editing the lattice under a fixed version string left the digest unchanged; every historical evaluation would still verify while meaning the opposite")
	}
	if err := emit(map[string]any{"kind": "identity",
		"label": "editing the lattice changes the digest even under a fixed version string",
		"witnesses": "LICENSE-IDENTITY-MODEL-CONTENT", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": swapped.ModelDigest, "subject": swapped.Subject,
		"pairs": []licensePair{{Artifact: artA, Publication: pubA, Name: "a", License: "MIT"}, {Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}}, "assertions": []string{}, "digest": evaluationDigest(swapped)}); err != nil {
		return err
	}

	if err := emit(map[string]any{"kind": "identity", "label": "input order does not change the digest",
		"witnesses": "LICENSE-ORDER-INDEPENDENT", "policy": base.Policy, "engine": base.Engine,
		"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{}, "pairs": []licensePair{{Artifact: artB, Publication: pubB, Name: "b", License: "Apache-2.0"}, {Artifact: artA, Publication: pubA, Name: "a", License: "MIT"}}, "digest": baseDigest}); err != nil {
		return err
	}

	// The COLLISION PAIR. Under the previous one-line `input=<name>=<expr>`
	// encoding these two DISTINCT compositions hashed identically, because
	// §8.6.1 permits `=` inside a name and the line had no sound split. The pair
	// is emitted as a vector so the property is witnessed rather than asserted in
	// prose — and generation FAILS if a kernel ever collides them again.
	amb := []struct {
		label string
		pair  licensePair
	}{
		{"a member containing = does not collide with a shifted split", licensePair{Artifact: "a=b", License: "MIT"}},
		{"the shifted split is a different evaluation", licensePair{Artifact: "a", License: "b=MIT"}},
	}
	var ambDigests []string
	for _, a := range amb {
		ev := licenseEvaluation{Policy: base.Policy, Engine: base.Engine, Model: base.Model,
			ModelDigest: base.ModelDigest, Subject: base.Subject,
			Inputs:      []licenseInput{{Artifact: a.pair.Artifact, License: a.pair.License}}}
		d := evaluationDigest(ev)
		ambDigests = append(ambDigests, d)
		if err := emit(map[string]any{"kind": "identity", "label": a.label,
			"witnesses": "LICENSE-IDENTITY-UNAMBIGUOUS", "policy": base.Policy, "engine": base.Engine,
			"model": base.Model, "model_digest": base.ModelDigest, "subject": base.Subject, "assertions": []string{}, "pairs": []licensePair{a.pair},
			"digest": d}); err != nil {
			return err
		}
	}
	if ambDigests[0] == ambDigests[1] {
		return fmt.Errorf("name=%q/expr=%q and name=%q/expr=%q share a digest: the encoding is not injective, so one assertion can forge another composition's identity",
			amb[0].pair.Name, amb[0].pair.License, amb[1].pair.Name, amb[1].pair.License)
	}

	return write(filepath.Join("license", "vectors.jsonl"), []byte(out.String()))
}

// evalFromAssertions folds a list of asserted expressions the way evaluateLicensing
// folds a closure, without needing a store. Kept separate so the vectors describe the
// RULES rather than a particular corpus.
func evalFromAssertions(exprs []string) grants {
	return evalFromAssertionsIn(licenseModel, exprs)
}

func evalFromAssertionsIn(model map[string]grants, exprs []string) grants {
	// SPEC §12.2 LICENSE-FOLD-NONEMPTY. The zero value is UNSTATED in every
	// dimension, so an empty fold is already conservative here. Read literally,
	// the §12.2 tables fall through both rows on zero inputs and yield YES — the
	// operator's identity — which grants everything on the strength of nothing.
	var acc grants
	if len(exprs) == 0 && !ruleOn("LICENSE-FOLD-NONEMPTY") {
		return grants{Commercial: triYes, Redistribute: triYes, Modify: triYes,
			PatentGrant: triYes, ShareAlike: triNo} // the identity: a false YES
	}
	for i, e := range exprs {
		g, _ := modelLookupIn(model, e)
		if i == 0 {
			acc = g
			continue
		}
		acc = grants{
			Commercial:   combine(acc.Commercial, g.Commercial),
			Redistribute: combine(acc.Redistribute, g.Redistribute),
			Modify:       combine(acc.Modify, g.Modify),
			PatentGrant:  combine(acc.PatentGrant, g.PatentGrant),
			ShareAlike:   combineObligation(acc.ShareAlike, g.ShareAlike),
		}
	}
	return acc
}
