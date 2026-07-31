package main

// LICENSE EVALUATION — the registry's half of DESIGN.md "What belongs inside identity".
//
// The publisher ASSERTS terms in a signed envelope. This derives what those assertions
// imply across a dependency closure. The two are different claims and are never
// reported as one: an assertion is signed and historical, an evaluation is computed and
// will be recomputed differently as the model improves.
//
// THE EVALUATION HAS AN IDENTITY, for the reason campaign identity exists (SPEC §11). A
// timeless "compatible" badge is a number whose method can change invisibly: alter the
// lattice next year and every historical verdict silently means something else. So an
// evaluation records the engine, the engine version, the model version, the policy, and
// a digest over the exact assertions consumed — and a consumer compares digests rather
// than reasoning about dates.
//
// UNSTATED IS CONTAGIOUS, and this is the load-bearing decision. A dependency that
// asserted nothing does not contribute "yes"; it contributes "unknown", and unknown
// propagates to the result. Deriving "commercial use: YES" from missing data would be
// the silent overclaim this whole system refuses — absence of a prohibition is not a
// grant. A consumer may adopt "treat UNSTATED as deny" or "require explicit grants";
// the registry must not choose that for them.
//
// THE MODEL IS FALLIBLE AND SAYS SO. SPDX supplies identifiers, not semantics; the
// mapping below is Oath's own and carries Oath's own errors. A wrong verdict here costs
// a reader a lawsuit rather than a bug, which is why every surface reports the model
// version and why an unrecognised expression yields UNSTATED rather than a guess.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Engine and model identity. Both are part of an evaluation's digest, so a change to
// either makes historical verdicts distinguishable rather than silently reinterpreted.
const (
	licenseEngine       = "oath-license/1"
	licenseModelVersion = "spdx-lattice/1"
	// SPEC §12.3 LICENSE-POLICY-DEFINED. The ONLY policy this specification
	// defines. It names how the input set was SELECTED, so an unrecognised value
	// changes the verdict as well as the identity and must never be silently
	// treated as this one.
	licensePolicyComposition = "composition"
)

// tri is a three-valued grant. UNSTATED is not a synonym for NO: it means the model has
// no basis to answer, and the difference is what stops absence becoming permission.
type tri int

const (
	triUnstated tri = iota
	triYes
	triNo
)

func (t tri) String() string {
	switch t {
	case triYes:
		return "YES"
	case triNo:
		return "NO"
	}
	return "UNSTATED"
}

// grants is what the model believes an expression permits. Every field defaults to
// UNSTATED, so a licence the model does not know contributes nothing rather than
// something optimistic.
type grants struct {
	Commercial   tri
	Redistribute tri
	Modify       tri
	PatentGrant  tri
	ShareAlike   tri // reciprocal obligation: derivatives must carry the same terms
}

// licenseModel maps SPDX identifiers to grants.
//
// DELIBERATELY SMALL AND EXPLICITLY INCOMPLETE. Every entry here is a claim about a
// legal text, and a wrong one is worse than a missing one: a missing entry yields
// UNSTATED, which is safe, while a wrong entry yields a confident answer that is false.
// Expressions this table does not know — including every OR/AND/WITH combination — fall
// through to all-UNSTATED by construction.
var licenseModel = map[string]grants{
	"MIT":           {triYes, triYes, triYes, triUnstated, triNo},
	"Apache-2.0":    {triYes, triYes, triYes, triYes, triNo},
	"BSD-2-Clause":  {triYes, triYes, triYes, triUnstated, triNo},
	"BSD-3-Clause":  {triYes, triYes, triYes, triUnstated, triNo},
	"ISC":           {triYes, triYes, triYes, triUnstated, triNo},
	"GPL-3.0-only":  {triYes, triYes, triYes, triYes, triYes},
	"GPL-2.0-only":  {triYes, triYes, triYes, triUnstated, triYes},
	"AGPL-3.0-only": {triYes, triYes, triYes, triYes, triYes},
	"MPL-2.0":       {triYes, triYes, triYes, triYes, triYes},
	"Unlicense":     {triYes, triYes, triYes, triUnstated, triNo},
	// A licence that PROHIBITS. Without at least one, the permission combiner's
	// NO-dominance branch is unreachable from the model and no vector can witness
	// LICENSE-PERMISSION-NO — the rule would be stated, implemented, and dead.
	"CC-BY-NC-4.0": {triNo, triYes, triYes, triUnstated, triNo},
}

// modelLookup resolves an asserted expression. Compound expressions are NOT parsed:
// "MIT OR Apache-2.0" requires choosing a disjunct, which is a decision with legal
// consequence that belongs to the consumer, not to the registry.
func isCompound(expr string) bool {
	return strings.Contains(expr, " OR ") || strings.Contains(expr, " AND ") ||
		strings.Contains(expr, " WITH ")
}

func compoundResult(expr string) (grants, string) {
	if strings.Contains(expr, " OR ") {
		return grants{}, "compound expression: choosing a disjunct is the consumer's decision, not the registry's"
	}
	return grants{}, "compound expression the model does not evaluate"
}

func modelLookupIn(model map[string]grants, expr string) (grants, string) {
	if expr == "" || expr == noLicense {
		return grants{}, "no terms asserted"
	}
	// SPEC §12.3 LICENSE-LOOKUP-PRECEDENCE: the compound test comes FIRST. A model
	// MAY contain any set of identifiers, so with the lookup first a model carrying
	// a compound key would RESOLVE it — which LICENSE-LOOKUP-COMPOUND forbids.
	// Unordered, the two rules contradict each other on a permitted input.
	// PRECEDENCE governs WHERE the compound test runs; LICENSE-LOOKUP-COMPOUND
	// governs WHETHER compounds are rejected at all. Gating this branch on both is
	// what keeps them independently measurable: with precedence rejecting compounds
	// unconditionally, disabling LOOKUP-COMPOUND changed nothing and the two rules
	// hid each other's removal.
	if ruleOn("LICENSE-LOOKUP-COMPOUND") && ruleOn("LICENSE-LOOKUP-PRECEDENCE") && isCompound(expr) {
		return compoundResult(expr)
	}
	// LICENSE-LOOKUP-EXACT: exact octet equality.
	if g, ok := model[expr]; ok {
		return g, ""
	}
	if !ruleOn("LICENSE-LOOKUP-EXACT") {
		// MUTATION: "helpful" normalisation, and the most dangerous mutation in
		// this section — it turns an expression the publisher never wrote into a
		// full grant, one layer BELOW the fold that would otherwise catch it.
		norm := strings.ToUpper(strings.Trim(strings.TrimSpace(expr), "()"))
		for k, g := range model {
			if strings.ToUpper(k) == norm {
				return g, ""
			}
		}
	}
	if ruleOn("LICENSE-LOOKUP-COMPOUND") {
		if isCompound(expr) {
			return compoundResult(expr)
		}
	} else if i := strings.Index(expr, " OR "); i > 0 {
		// MUTATION: resolve to the first disjunct. Plausible and wrong — it picks
		// terms on the consumer's behalf.
		if g, ok := model[expr[:i]]; ok {
			return g, ""
		}
	}
	if !ruleOn("LICENSE-LOOKUP-UNKNOWN") {
		// MUTATION: the dangerous direction. An unmodelled identifier becomes a
		// full grant, so an unknown composition reads as permitted.
		return grants{Commercial: triYes, Redistribute: triYes, Modify: triYes,
			PatentGrant: triYes, ShareAlike: triNo}, ""
	}
	return grants{}, "identifier not in the model"
}

func modelLookup(expr string) (grants, string) {
	return modelLookupIn(licenseModel, expr)
}

// combine folds a PERMISSION across a composition. UNSTATED wins over YES — once any
// input is unknown the answer is unknown, however many others granted. NO wins over
// everything, since a prohibition anywhere binds the whole.
//
// PERMISSIONS AND OBLIGATIONS HAVE OPPOSITE POLARITY, and conflating them was a real
// bug this fixture family caught. "May I redistribute?" is a permission: one NO stops
// the composition. "Must derivatives carry the same terms?" is an OBLIGATION: one YES
// binds the composition. Folding an obligation with the permission rule produced
// share_alike=NO for a closure containing an UNKNOWN licence — reporting "no reciprocal
// obligation" about terms nobody had read, which is precisely the false-permission
// direction that matters most here.
func combine(acc, next tri) tri {
	if ruleOn("LICENSE-PERMISSION-NO") && (acc == triNo || next == triNo) {
		return triNo
	}
	if ruleOn("LICENSE-PERMISSION-UNKNOWN") && (acc == triUnstated || next == triUnstated) {
		return triUnstated
	}
	return triYes
}

// combineObligation folds an OBLIGATION. YES dominates, because a reciprocal
// requirement anywhere binds the whole composition. UNSTATED remains contagious over
// NO: not knowing whether an obligation exists is not the same as knowing there is
// none, and only the second is safe to report.
func combineObligation(acc, next tri) tri {
	// Its OWN rule identifiers, not the permission combiner's. Sharing them would make
	// the two dimensions indistinguishable to the scorer: disabling one rule would
	// perturb both combiners, and a mutation could not be attributed to either.
	if ruleOn("LICENSE-OBLIGATION-YES") && (acc == triYes || next == triYes) {
		return triYes
	}
	if ruleOn("LICENSE-OBLIGATION-UNKNOWN") && (acc == triUnstated || next == triUnstated) {
		return triUnstated
	}
	return triNo
}

type licenseInput struct {
	// Artifact is what the pair is IDENTIFIED by (§12.4 LICENSE-IDENTITY-ARTIFACT).
	// Name is PROVENANCE — reported so a reader can see how the closure was located,
	// never hashed, because §9 makes names mutable without changing identity.
	Artifact string `json:"artifact"`
	// Publication is the §8.2.2 entry digest of the publication whose assertion
	// was consumed — WHOSE grant this is. The same artifact can carry different
	// assertions under different publications, and the same expression asserted by
	// two principals is two different grants over the same bytes.
	Publication string `json:"publication"`
	Name        string `json:"name"`
	License     string `json:"license"`
	Reason      string `json:"reason,omitempty"` // why the model could not answer, when it could not
}

type licenseEvaluation struct {
	Policy      string
	Engine      string
	Model       string
	ModelDigest string
	// Subject is the artifact the evaluation is ABOUT (§12.4
	// LICENSE-IDENTITY-SUBJECT). Without it two entry points into one component
	// share a digest, and every empty closure collapses onto one identity.
	Subject   string
	Digest    string
	Result    grants
	Inputs    []licenseInput
	Unmodeled int // inputs the model could not interpret
}

// evaluateLicensing derives what an artifact's closure permits, from the terms each
// publication asserted.
func evaluateLicensing(st *Store, name string, deps []string) licenseEvaluation {
	ev := licenseEvaluation{Policy: licensePolicyComposition, Engine: licenseEngine,
		Model: licenseModelVersion, ModelDigest: licenseModelDigest(),
		Subject: st.Names()[name]}

	// add takes the ARTIFACT as the member identity and the name as provenance
	// (§12.4 LICENSE-IDENTITY-ARTIFACT). The asserted expression still travels with
	// the artifact, because a licence assertion belongs to a PUBLICATION rather
	// than to the code — one artifact published twice under different terms is two
	// input pairs, and correctly two evaluations.
	add := func(artifact, n string) {
		lic := assertedLicense(st, n)
		g, reason := modelLookup(lic)
		if lic == "" {
			lic = "(none)"
		}
		in := licenseInput{Artifact: artifact, Publication: publicationOf(st, n, artifact),
			Name: n, License: lic, Reason: reason}
		// SPEC §12.4 LICENSE-IDENTITY-UNAMBIGUOUS. A value that cannot be encoded
		// on a digest line is not evaluated: encoding it anyway would let one
		// assertion forge another composition's digest. Fail CLOSED. Silently
		// stripping the offending octets would be worse than refusing, because the
		// digest would then attest to a string nobody asserted.
		if !digestSafe(in.member()) || !digestSafe(lic) {
			ev.Inputs = append(ev.Inputs, licenseInput{Artifact: artifact, Name: shortHash(n),
				License: "(unencodable)",
				Reason:  "member or expression contains an octet §12.4 forbids in a digest line"})
			ev.Unmodeled++
			ev.Result = grants{}
			return
		}
		ev.Inputs = append(ev.Inputs, in)
		if reason != "" {
			ev.Unmodeled++
		}
		if len(ev.Inputs) == 1 {
			ev.Result = g
			return
		}
		ev.Result = grants{
			Commercial:   combine(ev.Result.Commercial, g.Commercial),
			Redistribute: combine(ev.Result.Redistribute, g.Redistribute),
			Modify:       combine(ev.Result.Modify, g.Modify),
			PatentGrant:  combine(ev.Result.PatentGrant, g.PatentGrant),
			ShareAlike:   combineObligation(ev.Result.ShareAlike, g.ShareAlike),
		}
	}

	add(st.Names()[name], name)
	for _, d := range deps {
		// Dependencies arrive BY HASH, which is exactly the member identity. A name
		// is resolved only so the evaluation reports something a reader can act on;
		// an unnamed dependency is still a fully identified member, and no longer
		// poisons the composition merely for lacking a name.
		add(d, nameOfHash(st, d))
	}
	ev.Digest = evaluationDigest(ev)
	return ev
}

// publicationOf returns the §8.2.2 entry digest of the publication that most
// recently BOUND name to hash — the publication whose licence assertion an
// evaluation consumes. Searching backwards finds the transition currently in
// force rather than the first one that ever matched.
func publicationOf(st *Store, name, hash string) string {
	entries := st.ReadLog()
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Name != name || e.Hash != hash {
			continue
		}
		if d, derr := entryDigest(&entries[i]); derr == nil {
			return d
		}
		return ""
	}
	return ""
}

func nameOfHash(st *Store, h string) string {
	for n, hh := range st.Names() {
		if hh == h {
			return n
		}
	}
	return ""
}

// evaluationDigest identifies WHAT was evaluated and BY WHAT. Canonical by definition,
// the same discipline as campaignEncode: a domain separator, then fixed keys, then the
// consumed assertions sorted by name. Changing the engine, the model, the policy, or any
// input assertion produces a different digest, so a stale verdict is detectable rather
// than merely old.
// digestSafe reports whether v may appear in a §12.4 digest line. The character
// rule is not cosmetic: an LF inside a value injects a line, letting a single
// assertion forge the digest of a two-input composition. §8.6.1 established the
// same rule for the same reason; §12.4 originally inherited none of it.
// member is the value an input pair is identified by in the digest: its artifact
// hash. LICENSE-IDENTITY-ARTIFACT — binding the NAME instead would move an
// evaluation's identity on a rename, when nothing about the evaluated software
// changed, which is the same defect as making artifact identity depend on a
// repository path.
func (i licenseInput) member() string {
	if !ruleOn("LICENSE-IDENTITY-ARTIFACT") {
		// MUTATION: identify members by NAME. Plausible — names are what a reader
		// sees — and wrong: §9 makes names mutable without changing identity, so an
		// evaluation would move on a rename while nothing about the evaluated
		// software changed.
		return i.Name
	}
	if i.Artifact != "" {
		return i.Artifact
	}
	// A member with no resolvable artifact still has to be distinguishable and
	// MUST NOT silently borrow a name.
	return noLicense
}

// pub is the publication identity bound by a triple. An input with none is
// marked rather than omitted: a member whose publication cannot be identified is
// a DIFFERENT fact from one published anonymously, and collapsing them would let
// an unidentifiable grant borrow an identifiable one's digest.
func (i licenseInput) pub() string {
	if i.Publication != "" {
		return i.Publication
	}
	return noLicense
}

// subjectOr returns the evaluated artifact, or the §8.6.1 sentinel when there is
// none. Omitting the line would make a subjectless evaluation encode as a SHORTER
// record, which is the collapse LICENSE-IDENTITY-SUBJECT prevents.
func subjectOr(h string) string {
	if h == "" {
		return noLicense
	}
	return h
}

func digestSafe(v string) bool {
	for _, c := range []byte(v) {
		if c < 0x20 || c == 0x7F {
			return false
		}
	}
	// U+2028/U+2029 are NOT control octets, so the loop above admits them — and a
	// Unicode-aware line splitter then reads a one-member evaluation as a
	// multi-member composition carrying a grant nobody published. §8.2.1 escapes
	// both by name for this hazard.
	return !strings.ContainsRune(v, '\u2028') && !strings.ContainsRune(v, '\u2029')
}

// licenseModelBytes is the canonical published form of the model — the exact bytes
// fixtures/license/model.json carries (SPEC §12.3). Shared with the fixture writer so
// the digest can never bind a serialisation the corpus does not publish.
func licenseModelBytes() []byte {
	type row struct {
		Commercial   string `json:"commercial"`
		Redistribute string `json:"redistribute"`
		Modify       string `json:"modify"`
		PatentGrant  string `json:"patent_grant"`
		ShareAlike   string `json:"share_alike"`
	}
	ids := make([]string, 0, len(licenseModel))
	for k := range licenseModel {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	rows := make(map[string]row, len(ids))
	for _, k := range ids {
		g := licenseModel[k]
		rows[k] = row{g.Commercial.String(), g.Redistribute.String(), g.Modify.String(),
			g.PatentGrant.String(), g.ShareAlike.String()}
	}
	// No `engine` member: §12.3 LICENSE-MODEL-SCHEMA. The engine is the evaluator,
	// the model is the lattice it consults — carrying one inside the model's bytes
	// made engine= and model-digest= non-independent components of one identity.
	b, err := json.MarshalIndent(map[string]any{
		"model": licenseModelVersion,
		"note": "Permissions fold as MINIMUM and obligations as MAXIMUM over NO < UNSTATED < YES (SPEC §12.2). " +
			"share_alike is the only OBLIGATION dimension here; the rest are permissions. " +
			"An identifier absent from this table yields all-UNSTATED, which is safe; a wrong entry is not.",
		"licenses": rows,
	}, "", "  ")
	if err != nil {
		panic("license model is not serialisable: " + err.Error())
	}
	return append(b, '\n')
}

func licenseModelDigest() string {
	sum := sha256.Sum256(licenseModelBytes())
	return hex.EncodeToString(sum[:])
}

// evaluationDigest returns "" — REFUSAL — when any input cannot be safely encoded
// (SPEC §12.4). Refusal rather than a digest is the whole point: the previous guard
// lived in evaluateLicensing, so the store path was safe while any direct caller of
// this function could still forge a two-input identity from one assertion. A safety
// rule enforced at one call site is enforced nowhere.
func evaluationDigest(ev licenseEvaluation) string {
	if ruleOn("LICENSE-IDENTITY-UNAMBIGUOUS") {
		for _, i := range ev.Inputs {
			if !digestSafe(i.member()) || !digestSafe(i.pub()) || !digestSafe(i.License) {
				return ""
			}
		}
	}
	var b strings.Builder
	b.WriteString("oath-license-eval/1\n")
	if ruleOn("LICENSE-IDENTITY-INPUT") {
		eng := ev.Engine
		if !ruleOn("LICENSE-ENGINE-DEFINED") {
			// MUTATION: treat the engine as a property of the MODEL. Plausible,
			// because a published model file carries one — and wrong: the model is
			// the lattice, the engine is what consults it, so this makes two
			// components of the digest non-independent.
			eng = licenseEngine
		}
		b.WriteString("engine=" + eng + "\n")
	}
	if ruleOn("LICENSE-MODEL-VERSIONED") {
		b.WriteString("model=" + ev.Model + "\n")
	}
	if ruleOn("LICENSE-IDENTITY-MODEL-CONTENT") {
		// The version string is an ASSERTION by whoever edits the lattice; the
		// content digest is not. Binding only the string lets a table be changed
		// while every historical evaluation still verifies and now means the
		// opposite — the exact harm this section opens by naming. §11.2 hashes the
		// waiver SET rather than a version, for the same reason.
		b.WriteString("model-digest=" + ev.ModelDigest + "\n")
	}
	if ruleOn("LICENSE-IDENTITY-INPUT") {
		pol := ev.Policy
		if !ruleOn("LICENSE-POLICY-DEFINED") && pol != licensePolicyComposition {
			// MUTATION: treat an unrecognised policy as `composition`. The verdict
			// is then reported as agreement with an evaluation that selected its
			// inputs by a DIFFERENT rule.
			pol = licensePolicyComposition
		}
		b.WriteString("policy=" + pol + "\n")
	}
	if ruleOn("LICENSE-IDENTITY-SUBJECT") {
		b.WriteString("subject=" + subjectOr(ev.Subject) + "\n")
	}
	in := append([]licenseInput(nil), ev.Inputs...)
	if ruleOn("LICENSE-ORDER-INDEPENDENT") {
		// Name alone is NOT a total order, so it does not determine a digest.
		// Artifact hash alone is not a total order: the same artifact can appear
		// twice in a closure under different asserted terms.
		sort.Slice(in, func(i, j int) bool {
			if in[i].member() != in[j].member() {
				return in[i].member() < in[j].member()
			}
			if in[i].pub() != in[j].pub() {
				return in[i].pub() < in[j].pub()
			}
			return in[i].License < in[j].License
		})
	}
	if ruleOn("LICENSE-IDENTITY-INPUT") {
		for _, i := range in {
			if !ruleOn("LICENSE-INPUT-COMPLETE") && (i.License == "(none)" || i.License == noLicense) {
				// MUTATION: skip members that assert nothing. This is the reading
				// that COLLIDES — {a:MIT, b:(none)} then encodes exactly as {a:MIT}
				// while their verdicts differ.
				continue
			}
			// SPEC §12.4 LICENSE-IDENTITY-UNAMBIGUOUS: name and expression on
			// SEPARATE lines. A single `input=<name>=<expr>` line has no sound
			// split — §8.6.1 permits `=` inside a name — so `a=b`/`MIT` and
			// `a`/`b=MIT` encoded identically, violating LICENSE-IDENTITY-INPUT
			// two paragraphs below.
			if ruleOn("LICENSE-IDENTITY-UNAMBIGUOUS") {
				b.WriteString("input-artifact=" + i.member() + "\n")
				if ruleOn("LICENSE-IDENTITY-PUBLICATION") {
					if i.Publication == "" && !ruleOn("LICENSE-PUBLICATION-SENTINEL") {
						// MUTATION: omit the line instead of encoding the sentinel.
						// This collapses a triple into a pair, letting an
						// unidentifiable member borrow an identifiable one's bytes.
					} else {
						b.WriteString("input-publication=" + i.pub() + "\n")
					}
				}
				b.WriteString("input-license=" + i.License + "\n")
			} else {
				// The superseded encoding, retained ONLY so the scorer can disable
				// the rule and confirm a vector notices. It is not injective.
				b.WriteString("input=" + i.member() + "=" + i.License + "\n")
			}
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// render prints an evaluation so its fallibility is visible before its result is.
func (ev licenseEvaluation) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nLICENSE EVALUATION (DERIVED — not a legal opinion, and not PROVEN)\n")
	fmt.Fprintf(&b, "  policy  %s\n  engine  %s\n  model   %s\n  digest  %s\n\n",
		ev.Policy, ev.Engine, ev.Model, shortHash(ev.Digest))
	for _, r := range []struct {
		label string
		v     tri
	}{
		{"commercial use", ev.Result.Commercial},
		{"redistribution", ev.Result.Redistribute},
		{"modification", ev.Result.Modify},
		{"patent grant", ev.Result.PatentGrant},
		{"share-alike obligation", ev.Result.ShareAlike},
	} {
		fmt.Fprintf(&b, "  %-24s %s\n", r.label, r.v)
	}
	fmt.Fprintf(&b, "\n  assertions consumed (%d):\n", len(ev.Inputs))
	for _, i := range ev.Inputs {
		note := ""
		if i.Reason != "" {
			note = "  — " + i.Reason
		}
		fmt.Fprintf(&b, "    %-22s %s%s\n", trunc(i.Name, 22), i.License, note)
	}
	if ev.Unmodeled > 0 {
		fmt.Fprintf(&b, "\n  %d assertion(s) the model could not interpret. UNSTATED is CONTAGIOUS:\n", ev.Unmodeled)
		fmt.Fprintf(&b, "  one unknown input makes the composition unknown, because absence of a\n")
		fmt.Fprintf(&b, "  prohibition is not a grant. Adopt your own policy — treat UNSTATED as deny,\n")
		fmt.Fprintf(&b, "  or require explicit grants — the registry must not choose that for you.\n")
	}
	fmt.Fprintf(&b, "\n  This was COMPUTED by the named engine from the named model, over the signed\n")
	fmt.Fprintf(&b, "  assertions listed above. The model is Oath's own and is fallible; SPDX supplies\n")
	fmt.Fprintf(&b, "  identifiers, not semantics. It is not advice, and it is not a proof.\n")
	return b.String()
}

// --- conformance surface (SPEC §10) ------------------------------------------------

type licensePair struct {
	// Artifact is what the digest binds; Name is provenance only (§12.4).
	Artifact    string `json:"artifact,omitempty"`
	Publication string `json:"publication,omitempty"`
	Name        string `json:"name,omitempty"`
	License     string `json:"license"`
}

type licenseVector struct {
	Kind       string   `json:"kind"`
	Label      string   `json:"label"`
	Witnesses  string   `json:"witnesses,omitempty"`
	Policy     string   `json:"policy"`
	Engine     string   `json:"engine"`
	Model      string   `json:"model"`
	Assertions []string `json:"assertions"`
	// Pairs carries name and expression as SEPARATE values. `assertions` encodes
	// them as "<name>=<expr>" and so cannot express a name containing `=` — the
	// very ambiguity §12.4 forbids. A vector witnessing
	// LICENSE-IDENTITY-UNAMBIGUOUS therefore CANNOT use `assertions`: the fixture
	// format has to be able to state what the rule is about.
	Pairs       []licensePair     `json:"pairs,omitempty"`
	Expect      map[string]string `json:"expect,omitempty"`
	Digest      string            `json:"digest,omitempty"`
	Subject     string            `json:"subject,omitempty"`
	ModelDigest string            `json:"model_digest,omitempty"`
	// ModelLicenses lets a vector supply its OWN model. §12.3 permits a model to
	// contain any set of identifiers, and LICENSE-LOOKUP-PRECEDENCE is load-bearing
	// precisely when one carries a COMPOUND key — a case the published model does
	// not contain and should not, since polluting a policy artifact to make a test
	// reachable would corrupt the thing under test.
	ModelLicenses map[string]map[string]string `json:"model_licenses,omitempty"`
	// ExpectRejected marks a vector whose inputs a conformant kernel MUST REFUSE
	// to encode (§12.4's character rule). Its digest is published so the value an
	// unsafe implementation would produce is on the record — the point is that a
	// conformant one never reaches it.
	ExpectRejected bool `json:"expect_rejected,omitempty"`
}

// runLicenseVectors executes the family and returns each FAILURE. Empty means pass.
func runLicenseVectors(vs []licenseVector) []string {
	var fail []string
	for _, v := range vs {
		switch v.Kind {
		case "evaluation":
			// §12.3 LICENSE-POLICY-DEFINED has a second clause the digest cannot
			// witness: an unrecognised policy MUST NOT be evaluated under
			// `composition` and reported as agreement. Refusing is the obligation;
			// producing a different digest is LICENSE-IDENTITY-INPUT's clause.
			var g grants
			if ruleOn("LICENSE-POLICY-DEFINED") && v.Policy != licensePolicyComposition {
				g = grants{}
			} else {
				g = evalFromAssertionsIn(vectorModel(v), v.Assertions)
			}
			got := map[string]string{"commercial": g.Commercial.String(), "redistribute": g.Redistribute.String(),
				"modify": g.Modify.String(), "patent_grant": g.PatentGrant.String(), "share_alike": g.ShareAlike.String()}
			for k, want := range v.Expect {
				if got[k] != want {
					fail = append(fail, fmt.Sprintf("evaluation %q: %s = %s, want %s", v.Label, k, got[k], want))
				}
			}
		case "identity":
			var in []licenseInput
			if len(v.Pairs) > 0 {
				for _, pr := range v.Pairs {
					in = append(in, licenseInput{Artifact: pr.Artifact, Publication: pr.Publication,
						Name: pr.Name, License: pr.License})
				}
			} else {
				for _, a := range v.Assertions {
					// Lossy by construction; only sound because these vectors use
					// names without `=`. See licenseVector.Pairs.
					n, l, _ := strings.Cut(a, "=")
					in = append(in, licenseInput{Name: n, License: l})
				}
			}
			if v.ExpectRejected {
				// The obligation is REFUSAL, not a matching digest.
				for _, i := range in {
					if !digestSafe(i.Name) || !digestSafe(i.License) {
						continue
					}
					fail = append(fail, fmt.Sprintf("identity %q: input %q is encodable, but §12.4's character rule must reject it — this kernel can forge a two-input digest from one assertion", v.Label, i.Name))
				}
				continue
			}
			ev := licenseEvaluation{Policy: v.Policy, Engine: v.Engine, Model: v.Model,
				ModelDigest: v.ModelDigest, Subject: v.Subject, Inputs: in}
			if d := evaluationDigest(ev); d != v.Digest {
				fail = append(fail, fmt.Sprintf("identity %q: digest %s, want %s", v.Label, shortHash(d), shortHash(v.Digest)))
			}
		}
	}
	return fail
}

// vectorModel returns the model a vector is evaluated against: its own if it
// supplies one, otherwise the published model.
func vectorModel(v licenseVector) map[string]grants {
	if len(v.ModelLicenses) == 0 {
		return licenseModel
	}
	m := make(map[string]grants, len(v.ModelLicenses))
	for k, row := range v.ModelLicenses {
		m[k] = grants{triFrom(row["commercial"]), triFrom(row["redistribute"]),
			triFrom(row["modify"]), triFrom(row["patent_grant"]), triFrom(row["share_alike"])}
	}
	return m
}

// triFrom reads one model cell. SPEC §12.3 LICENSE-MODEL-SCHEMA: an absent member
// or a value outside {YES, NO, UNSTATED} — including one differing only in case —
// reads UNSTATED. Both permissive alternatives are the NATURAL ones, which is why
// both had to be stated: defaulting an absent share_alike to NO reports "no
// reciprocal obligation" about a row that never said so, and case-folding "yes"
// grants terms nobody wrote.
func triFrom(s string) tri {
	switch s {
	case "YES":
		return triYes
	case "NO":
		return triNo
	case "UNSTATED":
		return triUnstated
	}
	if !ruleOn("LICENSE-MODEL-SCHEMA") {
		// MUTATION: be lenient. Case-fold, and read anything unrecognised as a grant.
		switch strings.ToUpper(strings.TrimSpace(s)) {
		case "YES", "":
			return triYes
		case "NO":
			return triNo
		}
		return triYes
	}
	return triUnstated
}
