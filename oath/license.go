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
	"os"
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
	// Proposed marks a PREVIEW subject whose term has not been signed. Display-only
	// and outside the §12.4 digest (which binds artifact, publication and license),
	// so it cannot forge an identity.
	Proposed bool `json:"proposed,omitempty"`
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
	Unmodeled int  // inputs the model could not interpret
	Preview   bool `json:"preview,omitempty"` // a pre-publication preview: the subject term is proposed, not signed
}

// evaluateLicensing derives what an artifact's closure permits, from the terms each
// publication asserted.
// licensingClosure returns the TRANSITIVE dependency closure of a subject
// definition — every artifact its composition rests on, not merely the ones it
// names directly. A composition verdict must cover the whole closure: a permissive
// direct dependency that itself pulls in an unlicensed or restrictive one would
// otherwise be reported as clean, and a consumer would ship believing they were
// permitted. The subject itself is excluded (it is evaluated separately with its
// own — or, in a preview, proposed — terms). It takes the def object rather than a
// hash so a PREVIEW can pass an elaborated-but-unpublished subject whose deps are in
// the store.
//
// This is §12.2's composition — "the artifact together with its exact transitive
// dependency closure", identified BY HASH — so it is a SET of hashes, exactly as
// programClosure and every other closure in the kernel builds. The `seen` set is
// therefore not a dedup that could hide terms: a hash is one artifact whose terms
// are a function of the hash (nameOfHash → assertedLicense), so reaching it by two
// branches yields the SAME license, and the permission fold is idempotent, so the
// VERDICT is closure-multiplicity-invariant. LICENSE-INPUT-COMPLETE's "a member
// appearing twice contributes twice" is honored where multiplicity can actually
// arise — the EVALUATION loop in evaluateLicensingSubject consumes its input list
// without deduping, so a caller (or a conformance vector) that supplies the same
// (artifact, publication) triple twice gets it counted twice.
func licensingClosure(st *Store, subject *Def) []string {
	seen := map[string]bool{}
	var walk func(h string)
	walk = func(h string) {
		if seen[h] {
			return
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			return // a dep not in the store contributes no assertion; the caller's
			// elaboration already required the closure to resolve, so this is defensive.
		}
		for dep := range collectDeps(d) {
			walk(dep)
		}
	}
	for dep := range collectDeps(subject) {
		walk(dep)
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// evaluateLicensing derives the composition verdict for a PUBLISHED name: the
// subject's asserted terms come from its own publications, the dependencies' from
// theirs.
func evaluateLicensing(st *Store, name string, deps []string) licenseEvaluation {
	art := st.Names()[name]
	return evaluateLicensingSubject(st, name, art, assertedLicense(st, name), publicationOf(st, name, art), deps)
}

// cmdLicensePreview answers the one question a publisher choosing terms has —
// "what will my users actually be permitted to do?" — BEFORE the permanent act of
// publishing (license-consumer friction, demand 2). It elaborates a single
// unpublished definition against the local store, computes its dependency closure
// exactly as `oath license <name>` does, and evaluates the composition with the
// PROPOSED assertion standing in for the subject's (not-yet-existing) publication.
// Nothing is signed and nothing is written; it mirrors how `publish --dry-run`
// makes the signed BYTES checkable, one layer up at the derived verdict.
// shellQuote renders s as a single shell argument, so a copy-pasted suggestion
// reproduces the exact value. A compound SPDX expression like `MIT OR Apache-2.0`
// unquoted would be parsed as `MIT` plus stray positional words — publishing,
// permanently, under terms other than the ones previewed.
func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '.' || r == '_' || r == '+' || r == '/') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cmdLicensePreview(st *Store, file, assert string) {
	// Reject a proposed term the PUBLICATION would refuse — a newline or control
	// character fails pubEnvelope.validate at publish time, so previewing it would
	// promise a verdict for a term that can never be asserted, and print a `--license`
	// suggestion that always fails. Same check the envelope uses.
	if !envelopeSafe(assert) {
		fail(fmt.Errorf("the proposed term contains a newline or control character; it could not be published (envelope §8.6.1), so there is nothing to preview"))
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fail(err)
	}
	forms, err := parseForms(string(src))
	if err != nil {
		fail(err)
	}
	if len(forms) != 1 {
		// A preview needs ONE subject whose terms are being chosen. A closure has no
		// single subject — its members may carry different terms — so the honest answer
		// is to publish it and license by name, where each member's assertion is real.
		fail(fmt.Errorf("license --assert previews a single definition (the one whose terms you are choosing); this file has %d — split it, or publish the closure and `oath license <name>`", len(forms)))
	}
	def, meta, err := elabForm(st, forms[0])
	if err != nil {
		fail(fmt.Errorf("this definition does not elaborate against the local store (its dependencies must be present to evaluate the composition): %w", err))
	}
	// Typecheck before hashing, exactly as buildPublishPlan does, so the previewed
	// artifact identity is the one a publication would carry (#101).
	if err := checkDef(st, def); err != nil {
		fail(err)
	}
	h := hashDef(def)
	deps := licensingClosure(st, def) // TRANSITIVE, matching the published path

	// The proposed subject has no publication — pass the empty sentinel so its digest
	// is not bound to some existing publication that asserted different terms.
	ev := evaluateLicensingSubject(st, meta.Name, h, assert, "", deps)
	ev.Preview = true
	for i := range ev.Inputs {
		if ev.Inputs[i].Artifact == h {
			ev.Inputs[i].Proposed = true
		}
	}
	fmt.Print(ev.render())
	fmt.Printf("\nPREVIEW — nothing was signed or published. The subject term %q is the one you\n"+
		"are PROPOSING; the dependencies' terms are read from THIS store's publication\n"+
		"records. A dependency whose signed publication record is not local — e.g. one\n"+
		"published to a remote registry and only put or resolved here — carries no terms\n"+
		"in this store and reads UNSTATED, so this verdict matches the eventual\n"+
		"publication only where the store already holds the dependencies' assertions\n"+
		"(a fully-local publish flow). Previewing against a remote registry's assertions\n"+
		"is not yet wired. Publish with `--license %s` to assert it.\n",
		assert, shellQuote(assert))
}

// evaluateLicensingSubject is the same derivation with the subject's ARTIFACT and
// asserted LICENSE supplied explicitly rather than read from the store. It backs
// both the published path (evaluateLicensing) and the pre-publication PREVIEW
// (`oath license <file> --assert <SPDX>`), where the subject has no publication yet
// and its terms are the ones a publisher is CONSIDERING. Dependencies are always
// read from the store — they are already published, and their terms are facts, not
// proposals.
func evaluateLicensingSubject(st *Store, name, subjectArtifact, subjectLicense, subjectPublication string, deps []string) licenseEvaluation {
	ev := licenseEvaluation{Policy: licensePolicyComposition, Engine: licenseEngine,
		Model: licenseModelVersion, ModelDigest: licenseModelDigest(),
		Subject: subjectArtifact}

	// add takes the ARTIFACT as the member identity, the name as provenance
	// (§12.4 LICENSE-IDENTITY-ARTIFACT), the asserted expression — which belongs to a
	// PUBLICATION rather than to the code, so one artifact published twice under
	// different terms is two input pairs and correctly two evaluations — and the
	// PUBLICATION digest that carried it. The publication is supplied rather than
	// looked up so a PREVIEW can pass the empty sentinel: a proposed assertion has no
	// publication, and binding the proposal to an existing one (e.g. previewing a
	// relicense of unchanged source) would forge a digest for terms that publication
	// never asserted.
	add := func(artifact, n, lic, pub string) {
		g, reason := modelLookup(lic)
		if lic == "" {
			lic = "(none)"
		}
		in := licenseInput{Artifact: artifact, Publication: pub,
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

	add(subjectArtifact, name, subjectLicense, subjectPublication)
	for _, d := range deps {
		// Dependencies arrive BY HASH, which is exactly the member identity. A name
		// is resolved only so the evaluation reports something a reader can act on;
		// an unnamed dependency is still a fully identified member, and no longer
		// poisons the composition merely for lacking a name. Their terms are always
		// REAL (already published), so their publications are looked up.
		//
		// KNOWN LIMIT (pre-existing, shared by both the published and preview paths):
		// when one hash is bound under several names whose publications assert
		// DIFFERENT terms, nameOfHash selects one alias, so a single publication's
		// terms govern. §12.2's model is that each publication of an artifact is its
		// own input; realising that means carrying which alias each dependency edge
		// resolved through, which the by-hash closure does not track. Recorded rather
		// than papered over — the preview inherits exactly what `oath license <name>`
		// does, which is what keeps them faithful.
		dn := nameOfHash(st, d)
		add(d, dn, assertedLicense(st, dn), publicationOf(st, dn, d))
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
		if i.Proposed {
			note = "  — PROPOSED (not yet signed)" + note
		}
		fmt.Fprintf(&b, "    %-22s %s%s\n", trunc(i.Name, 22), i.License, note)
	}
	if ev.Unmodeled > 0 {
		fmt.Fprintf(&b, "\n  %d assertion(s) the model could not interpret. UNSTATED is CONTAGIOUS:\n", ev.Unmodeled)
		fmt.Fprintf(&b, "  one unknown input makes the composition unknown, because absence of a\n")
		fmt.Fprintf(&b, "  prohibition is not a grant. Adopt your own policy — treat UNSTATED as deny,\n")
		fmt.Fprintf(&b, "  or require explicit grants — the registry must not choose that for you.\n")
	}
	if ev.Preview {
		fmt.Fprintf(&b, "\n  This was COMPUTED by the named engine from the named model. The subject term\n")
		fmt.Fprintf(&b, "  above is a PROPOSAL and is not signed; the dependencies' terms are their real\n")
		fmt.Fprintf(&b, "  signed assertions. The model is Oath's own and is fallible; SPDX supplies\n")
		fmt.Fprintf(&b, "  identifiers, not semantics. It is not advice, and it is not a proof.\n")
	} else {
		fmt.Fprintf(&b, "\n  This was COMPUTED by the named engine from the named model, over the signed\n")
		fmt.Fprintf(&b, "  assertions listed above. The model is Oath's own and is fallible; SPDX supplies\n")
		fmt.Fprintf(&b, "  identifiers, not semantics. It is not advice, and it is not a proof.\n")
	}
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
