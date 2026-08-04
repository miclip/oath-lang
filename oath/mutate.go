package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Mutation testing: the kernel's answer to "who verifies the specs?"
// If the same author writes both implementation and properties, a lazy or
// tautological spec passes trivially. So the kernel measures spec STRENGTH:
// generate small semantic mutations of the implementation and check whether
// the properties notice. A mutant that survives is a change in behavior the
// oath is blind to — printed loudly, with the surviving body, so the weak
// spot is visible. The score (killed/total) is recorded in metadata next to
// the guarantee: "tested" tells you the promises held; spec strength tells
// you whether the promises say anything.
//
// WITH ONE PRECISION THE ABOVE LACKED, and it is not a caveat but a different
// question (#130): the check runs GENERATED EXECUTIONS. "Do the properties
// notice?" and "did the generator reach an input where they notice?" coincide
// only when the draw can reach the distinguishing input, and on this corpus they
// often do not — `hex-nibble` is PROVEN for all inputs and scores 11/53, because
// `genValue` draws Int from [-20,20] and its guards sit at 48/97/65. So the
// score answers REACH, and adjudicate.go answers EXCLUSION separately. The two
// are reported side by side and never averaged: a proof-refuted survivor is
// still a real survivor, and it is real evidence — about the harness.

const mutantCases = 60
const mutantFuel = 500_000

type mutantDef struct {
	desc string
	def  *Def
	hash string
}

func deepCopyDef(d *Def) *Def {
	b, _ := json.Marshal(d)
	var out Def
	_ = json.Unmarshal(b, &out)
	return &out
}

func deepCopyTerm(t *Term) *Term {
	b, _ := json.Marshal(t)
	var out Term
	_ = json.Unmarshal(b, &out)
	return &out
}

func collectNodes(t *Term, out *[]*Term) {
	if t == nil {
		return
	}
	*out = append(*out, t)
	collectNodes(t.A, out)
	collectNodes(t.B, out)
	collectNodes(t.C, out)
	for i := range t.Args {
		collectNodes(&t.Args[i], out)
	}
	for i := range t.Arms {
		collectNodes(&t.Arms[i], out)
	}
}

// Type-preserving operator substitutions, so every mutant still typechecks.
var opMutations = map[string][]string{
	"+": {"-"}, "-": {"+"}, "*": {"+"}, "/": {"*"}, "%": {"/"},
	"<": {"<="}, "<=": {"<"}, "and": {"or"}, "or": {"and"},
}

// Non-commutative binary primitives where swapping operands changes meaning.
var swappablePrims = map[string]bool{
	"-": true, "/": true, "%": true, "<": true, "<=": true,
}

// genMutants produces every single-node mutation of the definition's body
// that still typechecks. Props are copied unchanged: the question is whether
// THEY notice the body changed.
func genMutants(st *Store, d *Def) []mutantDef {
	work := deepCopyDef(d)
	var nodes []*Term
	collectNodes(work.Body, &nodes)
	seen := map[string]bool{hashDef(d): true}
	var out []mutantDef
	add := func(desc string) {
		md := deepCopyDef(work)
		if checkDef(st, md) != nil {
			return
		}
		h := hashDef(md)
		if seen[h] {
			return
		}
		seen[h] = true
		out = append(out, mutantDef{desc: desc, def: md, hash: h})
	}
	for _, n := range nodes {
		switch n.K {
		case "prim":
			for _, op := range opMutations[n.Op] {
				old := n.Op
				n.Op = op
				add(fmt.Sprintf("%s → %s", old, op))
				n.Op = old
			}
			if swappablePrims[n.Op] && len(n.Args) == 2 {
				n.Args[0], n.Args[1] = n.Args[1], n.Args[0]
				add(fmt.Sprintf("swapped operands of %s", n.Op))
				n.Args[0], n.Args[1] = n.Args[1], n.Args[0]
			}
		case "int":
			old := n.Int
			for _, nv := range []*big.Int{
				new(big.Int).Add(old, big.NewInt(1)),
				new(big.Int).Sub(old, big.NewInt(1)),
				big.NewInt(0),
			} {
				if nv.Cmp(old) == 0 {
					continue
				}
				n.Int = nv
				add(fmt.Sprintf("literal %s → %s", old, nv))
				n.Int = old
			}
		case "if":
			n.B, n.C = n.C, n.B
			add("swapped if branches")
			n.B, n.C = n.C, n.B
		case "app":
			// Swap adjacent call arguments: (f a b) → (f b a). Only mutants
			// that still typecheck survive the add() gate, so same-type
			// argument pairs are the ones that make it through.
			if n.A != nil && n.A.K == "app" {
				n.B, n.A.B = n.A.B, n.B
				add("swapped call arguments")
				n.B, n.A.B = n.A.B, n.B
			}
			// Replace a recursive call with one of its own arguments:
			// (self ... x ...) → x. This is the "forgot to recurse" bug —
			// structurally the smallest way to delete a recursion while
			// keeping an expression of (possibly) the right type.
			if head, args := unwindApp(n); head.K == "self" {
				// Shallow-save: the restore must reinstate the ORIGINAL child
				// pointers (nodes still aliases them), not copies — a deep-copy
				// restore would orphan the children and silently drop every
				// later mutation under this subtree.
				saved := *n
				for ai, a := range args {
					*n = *deepCopyTerm(a)
					add(fmt.Sprintf("recursive call → its argument %d", ai))
					*n = saved
				}
			}
		case "ctor":
			// Swap adjacent constructor arguments (tree children, record-ish
			// payloads); the typecheck gate keeps only same-type pairs.
			for ai := 0; ai+1 < len(n.Args); ai++ {
				n.Args[ai], n.Args[ai+1] = n.Args[ai+1], n.Args[ai]
				add(fmt.Sprintf("swapped constructor arguments %d,%d", ai, ai+1))
				n.Args[ai], n.Args[ai+1] = n.Args[ai+1], n.Args[ai]
			}
		case "match":
			// Swap two arm bodies. De Bruijn does the type policing: a body
			// referring to binders the other arm does not have fails the gate.
			for i := 0; i < len(n.Arms); i++ {
				for j := i + 1; j < len(n.Arms); j++ {
					n.Arms[i], n.Arms[j] = n.Arms[j], n.Arms[i]
					add(fmt.Sprintf("swapped match arms %d,%d", i, j))
					n.Arms[i], n.Arms[j] = n.Arms[j], n.Arms[i]
				}
			}
			// Collapse the match to a base arm: the whole match is replaced
			// by the body of an arm that binds nothing. This is the "always
			// take the base case" bug — the classic way recursion silently
			// disappears (length → 0, reverse → Nil).
			if md, err := st.GetDef(n.Hash); err == nil && md.K == "data" {
				saved := *n // shallow, for the same aliasing reason as above
				for ci := range saved.Arms {
					if ci < len(md.Ctors) && len(md.Ctors[ci]) == 0 {
						*n = *deepCopyTerm(&saved.Arms[ci])
						add(fmt.Sprintf("match collapsed to arm %d", ci))
						*n = saved
					}
				}
			}
		}
	}
	return out
}

// mutantSeed derives a mutant's case seed from its hash. Extracted so a test
// asking "is this mutant a survivor?" runs the ENGINE's derivation rather than
// a second copy of it — a hand-repeated seed would drift and the test would
// then measure a different draw than the score it is checking.
func mutantSeed(hash string) uint64 {
	seedB, _ := hex.DecodeString(hash[:16])
	return binary.BigEndian.Uint64(seedB)
}

func metaPropName(m *Meta, pi int) string {
	if pi < len(m.PropNames) {
		return m.PropNames[pi]
	}
	return fmt.Sprintf("prop%d", pi)
}

func cmdMutate(st *Store, name string, adjudicate bool) {
	out, err := apiMutateOpt(st, name, adjudicate)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func apiMutate(st *Store, name string) (string, error) { return apiMutateOpt(st, name, false) }

func apiMutateOpt(st *Store, name string, adjudicate bool) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	return apiMutateHashOpt(st, h, adjudicate)
}

// apiMutateHash mutation-scores an object directly by hash — used by the
// repoint policy, which must be able to score a candidate BEFORE any name
// points at it.
// apiMutateHash scores without survivor adjudication. The repoint policy calls
// this path, and it must stay prover-free: a policy decision that shells out to
// z3 would make admission latency depend on solver search, and `min_mutation_score`
// is defined over the generated score (SPEC §6.3), not over adjudicated survivors.
func apiMutateHash(st *Store, h string) (string, error) { return apiMutateHashOpt(st, h, false) }

func apiMutateHashOpt(st *Store, h string, adjudicate bool) (string, error) {
	name := st.NameOf(h)
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	if d.K != "func" {
		return "", fmt.Errorf("only function definitions can be mutation-tested")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if len(d.Props) == 0 {
		return fmt.Sprintf("%s swears no properties — every mutant survives; spec strength is zero.\n", name), nil
	}
	muts := genMutants(st, d)
	if len(muts) == 0 {
		return fmt.Sprintf("no mutation points in %s (body has no mutable operators, literals, or branches)\n", name), nil
	}
	waived := map[string]*WaivedMutant{}
	for i := range m.WaivedMutants {
		waived[m.WaivedMutants[i].Hash] = &m.WaivedMutants[i]
	}
	var b strings.Builder
	killed, waivedSeen := 0, 0
	// Survivors are collected rather than only printed, so their DISPOSITION can
	// be reported beneath the score (#130). A survivor is any mutant generated
	// execution failed to distinguish — waived ones included, since a waiver is
	// a judgement recorded ABOUT a survivor, not a different outcome.
	var survivors []mutantDef
	var survivorDescs []string
	var survivorVerdicts []survivorVerdict
	for _, mu := range muts {
		// Mutants are evaluated from the in-memory cache only — they are
		// candidates under interrogation, never admitted to the codebase.
		st.CacheDef(mu.hash, mu.def)
		base := mutantSeed(mu.hash)
		killer := ""
		for pi := range mu.def.Props {
			rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi), base, pi, mutantCases, mutantFuel)
			if rep.Failed || rep.Err != "" {
				killer = rep.Name
				break
			}
		}
		switch {
		case killer != "":
			killed++
			fmt.Fprintf(&b, "✓ killed    %-22s by %s\n", mu.desc, killer)
		case waived[mu.hash] != nil:
			// A waiver is an annotation with a justification on record —
			// reported distinctly, never counted as a kill.
			waivedSeen++
			w := waived[mu.hash]
			fmt.Fprintf(&b, "○ waived    %-22s — %s (by %s)\n", mu.desc, w.Reason, w.By)
			survivors = append(survivors, mu)
			survivorDescs = append(survivorDescs, mu.desc)
			survivorVerdicts = append(survivorVerdicts, survivorVerdict{kind: "equivalent", reason: w.Reason})
		default:
			pr := &printer{st: st, tvs: m.TyVarNames}
			fmt.Fprintf(&b, "✗ SURVIVED  %-22s — generated cases did not distinguish this change\n", mu.desc)
			fmt.Fprintf(&b, "    mutant: %s  (waive with: oath waive %s %s \"reason\")\n", pr.term(mu.def.Body, m.Name), name, shortHash(mu.hash))
			survivors = append(survivors, mu)
			survivorDescs = append(survivorDescs, mu.desc)
			survivorVerdicts = append(survivorVerdicts, survivorVerdict{kind: "unadjudicated", reason: "not adjudicated against proven properties"})
		}
	}
	// "generated mutation score", not "spec strength". The old label invited the
	// reading "the specification rules this mutant out", which is a claim about
	// the SPEC; the number is a claim about what generated executions REACHED.
	// On `hex-nibble` those diverge completely — 11/53 on a definition proven for
	// all inputs. The score is unchanged and still correct; only the claim
	// attached to it was wrong. (#130)
	fmt.Fprintf(&b, "generated mutation score: %d/%d mutants killed", killed, len(muts))
	if waivedSeen > 0 {
		fmt.Fprintf(&b, " (+%d waived as equivalent, justification on record)", waivedSeen)
	}
	b.WriteString("\n")
	if adjudicate {
		for i := range survivors {
			// The solver requirement is decided inside, at the point one is
			// actually run: a survivor that is non-total, or a definition with
			// nothing proven, is dispositioned without z3 at all.
			v, err := adjudicateSurvivorErr(st, m, survivors[i].hash, survivors[i].def)
			if err != nil {
				return "", err
			}
			// A waiver asserts the mutant is equivalent. A refutation proves it
			// is not. That contradiction is surfaced loudly rather than resolved
			// silently in either direction: the waiver stays on record, and the
			// proof that contradicts it is stated next to it.
			if survivorVerdicts[i].kind == "equivalent" && v.kind == "proof-refuted" {
				fmt.Fprintf(&b, "⚠ WAIVER CONTRADICTED  %s — waived as equivalent, but proven property %s refutes it\n",
					survivorDescs[i], v.prop)
			}
			if survivorVerdicts[i].kind != "equivalent" {
				survivorVerdicts[i] = v
			}
		}
	}
	renderAdjudication(&b, survivorVerdicts, survivorDescs, adjudicate)
	m.MutantsKilled, m.MutantsTotal = killed, len(muts)
	m.MutationCampaign = campaignHash(h, m.WaivedMutants)
	if err := st.SetMeta(h, m); err != nil {
		return "", err
	}
	return b.String(), nil
}

// apiWaive records a surviving mutant as judged-equivalent. The mutant is
// re-derived from the current definition so the waiver can only name a
// mutant that actually exists, and the full mutant hash is resolved from a
// short prefix. Waiving a mutant that a property kills is refused: waivers
// document unkillable survivors, they do not overrule the referee.
func apiWaive(st *Store, name, mutantPrefix, reason, by string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if reason == "" {
		return "", fmt.Errorf("a waiver requires a justification")
	}
	for _, mu := range genMutants(st, d) {
		if !strings.HasPrefix(mu.hash, mutantPrefix) {
			continue
		}
		st.CacheDef(mu.hash, mu.def)
		seedB, _ := hex.DecodeString(mu.hash[:16])
		base := binary.BigEndian.Uint64(seedB)
		for pi := range mu.def.Props {
			rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi), base, pi, mutantCases, mutantFuel)
			if rep.Failed || rep.Err != "" {
				return "", fmt.Errorf("mutant %s is killed by %s — nothing to waive", shortHash(mu.hash), rep.Name)
			}
		}
		for _, w := range m.WaivedMutants {
			if w.Hash == mu.hash {
				return fmt.Sprintf("mutant %s already waived: %s\n", shortHash(mu.hash), w.Reason), nil
			}
		}
		m.WaivedMutants = append(m.WaivedMutants, WaivedMutant{Hash: mu.hash, Desc: mu.desc, Reason: reason, By: by})
		if err := st.SetMeta(h, m); err != nil {
			return "", err
		}
		return fmt.Sprintf("○ waived %s (%s): %s\n", shortHash(mu.hash), mu.desc, reason), nil
	}
	return "", fmt.Errorf("no surviving mutant of %s matches %q (run oath mutate %s to list)", name, mutantPrefix, name)
}

// cmdScorable prints every definition that mutation scoring applies to — a func
// with at least one property — one name per line, sorted. Machine-readable on
// purpose: it is what drives `make mutate`, so the set scored is derived from
// the store instead of from a list someone has to remember to update.
func cmdScorable(st *Store) {
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, n := range keys {
		d, err := st.GetDef(names[n])
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		fmt.Println(n)
	}
}

// mutationEngine names the generator revision. Bump it whenever the mutant set
// changes shape — a new mutation kind means an old score describes a campaign
// that no longer exists.
const mutationEngine = "mutants-1"

// campaignHash is the reproducible identity of a MEASUREMENT (SPEC §11).
//
// A bare killed/total answers "how many" and never "out of which mutants, under
// which policy" — evidence without reproducible campaign identity is an
// assertion with numbers attached. Because MEASURED and STALE are decided by
// comparing this value, and consumers make trust decisions from it, its
// construction cannot be private registry behaviour: an auditor would then have
// to take the registry's word that a digest is correct, which is the
// publisher-asserts shape this was introduced to remove, moved one layer in.
//
// The encoding is therefore normative and deliberately dull: a domain separator,
// then one `key=value` line per field in fixed order, each LF-terminated,
// SHA-256, lowercase hex. Newline framing rather than delimiters inside a single
// line, so no field value can be confused with a separator.
//
// Note what the kernel owes here: only campaignHash, a PURE function of the
// description. Running mutation, scheduling, and storage stay registry concerns
// and are NOT specified — an auditor must be able to reproduce the identity of
// the computation being claimed, not the computation itself.
func campaignHash(artifact string, waived []WaivedMutant) string {
	ws := make([]string, 0, len(waived))
	for _, w := range waived {
		ws = append(ws, w.Hash)
	}
	sort.Strings(ws) // set identity, not recording order
	desc := campaignDescription{
		Artifact: artifact, Kernel: kernelVersion, Engine: mutationEngine,
		Cases: mutantCases, Fuel: mutantFuel,
		WaiverPolicy: waiverPolicy, Waivers: ws,
	}
	return campaignDigest(desc)
}

// waiverPolicy names how waivers affect the score. It is part of the identity
// because waivers count toward it: a waiver added later changes the number
// without changing the code, which is the drift the digest exists to expose.
const waiverPolicy = "waived-count-as-killed"

// campaignDescription is the normative, canonically-encodable statement of what
// a measurement WAS. Every field can change an outcome.
type campaignDescription struct {
	Artifact     string   // the object measured — a score is about one object
	Kernel       string   // evaluation semantics decide whether a mutant is caught
	Engine       string   // mutant generator revision
	Cases        int      // a survivor at 60 cases may be a kill at 600
	Fuel         int      // evaluation budget per case
	WaiverPolicy string   // how waivers affect the score
	Waivers      []string // waived mutant hashes, ascending; the SET, not the order
}

// campaignEncode renders the canonical bytes. Exported shape is fixed by SPEC
// §11; changing it changes every campaign identity, which is a fork of what
// "current evidence" means and must be treated as one.
// campaignFieldSafe reports whether a free-form value can appear in the encoding
// without making it ambiguous to DECODE. LF and CR are forbidden because the
// format is line-framed: a value containing LF adds lines the schema never
// emitted, and although that cannot forge a DIGEST — the encoder always writes
// exactly the same seven keys in the same order, so an injected line changes the
// byte count rather than replacing a field — it does defeat the audit step.
// Auditing means reconstructing a description from bytes and re-encoding it, and
// a byte stream that parses more than one way cannot be reconstructed with
// confidence. §11's original rationale named `=` as the risk; `=` is harmless
// under first-match splitting, and LF was the real one.
func campaignFieldSafe(v string) bool { return !strings.ContainsAny(v, "\n\r") }

func campaignEncode(d campaignDescription) []byte {
	// CANONICAL BY DEFINITION: campaignEncode emits exactly one byte
	// representation for every semantically identical description, so
	// campaignHash can be boring — hash the bytes, interpret nothing. Leaving
	// normalization in the hash wrapper would give two contracts (encode
	// serializes a sequence; hash reinterprets it as a set) and let a conforming
	// implementation hash the encoded bytes directly and get a different answer
	// while believing it followed the spec.
	//
	// `waivers` is a genuine SET: order and multiplicity are both non-semantic.
	// Waivers are keyed by mutant hash and a mutant can be waived once, so a
	// repeat carries no information — duplicates COLLAPSE rather than being
	// rejected. Sorted bytewise ascending.
	//
	// Note the discipline this implies: a field may only be normalized this way
	// when order and multiplicity are non-semantic. An ordered execution phase or
	// a precedence rule is a SEQUENCE and must never be sorted, or the encoding
	// would erase meaning rather than canonicalize it.
	seen := map[string]bool{}
	ws := make([]string, 0, len(d.Waivers))
	for _, w := range d.Waivers {
		if !seen[w] {
			seen[w] = true
			ws = append(ws, w)
		}
	}
	sort.Strings(ws)
	var b strings.Builder
	b.WriteString("oath-campaign/1\n")
	fmt.Fprintf(&b, "artifact=%s\n", d.Artifact)
	fmt.Fprintf(&b, "kernel=%s\n", d.Kernel)
	fmt.Fprintf(&b, "engine=%s\n", d.Engine)
	fmt.Fprintf(&b, "cases=%d\n", d.Cases)
	fmt.Fprintf(&b, "fuel=%d\n", d.Fuel)
	fmt.Fprintf(&b, "waiver-policy=%s\n", d.WaiverPolicy)
	fmt.Fprintf(&b, "waivers=%s\n", strings.Join(ws, ","))
	return []byte(b.String())
}

func campaignDigest(d campaignDescription) string {
	s := sha256.Sum256(campaignEncode(d))
	return hex.EncodeToString(s[:])
}
