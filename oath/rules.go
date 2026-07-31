package main

// NORMATIVE RULE INSTRUMENTATION, and the conformance mutation score.
//
// The project already measures how much of an IMPLEMENTATION's behaviour a
// specification constrains: mutate the code, see whether the properties notice, and
// report the score as evidence rather than as a claim (SPEC §11). A blind
// implementation pointed out that the same question applies one level up, and that
// nobody had asked it:
//
//   implementation mutation score — how much of the implementation is constrained
//                                   by its specification
//   conformance mutation score    — how much of the NORMATIVE SPECIFICATION is
//                                   actually witnessed by the conformance vectors
//
// They are analogous and answer different questions. The second was measured by hand
// once, by disabling each normative rule in turn and re-running the vectors: 15 of 17
// rules could be deleted with the suite still passing. That measurement should not
// depend on someone repeating it by hand, so it is automated here — a coverage figure
// that is DERIVED, like every other verdict in this system, rather than asserted by
// whoever wrote the vectors.
//
// THE MUTATION MACHINERY IS NOT IN PRODUCTION BUILDS. It can turn off signature
// verification, so package privacy is not a sufficient boundary: the code paths would
// still exist in a shipped binary, reachable by any future caller inside the package
// and present for anyone inspecting it.
//
// The switch therefore lives behind the `conformance_mutation` build tag (rule_disable_mutation.go). A
// normal build compiles rule_disable_prod.go instead, where ruleOn is a function returning a
// constant — the compiler folds every call site, so there is no branch to disable a
// rule and no state that could hold one. The true claim moves from "unreachable
// through configuration and exported APIs" to "production binaries do not contain
// code paths capable of disabling verification rules".
//
// Build the scorer with:  go build -tags conformance_mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// normativeRule is one obligation the specification states and the kernel enforces.
type normativeRule struct {
	ID   string // stable identifier, "<section>/<slug>"
	Sec  string // the section that states it
	What string // what it obliges, in one line
	// Family is the fixture family that can execute this rule. The denominator of a
	// score must contain only rules the suite being measured is CAPABLE of witnessing;
	// counting a rule that needs integration fixtures against the envelope suite would
	// make a low score read as "the rule is untested" when it means "this family cannot
	// reach it". A low score must mean "this suite does not witness the rule" and
	// nothing else.
	Family string
}

const (
	familyEnvelope    = "envelope"    // reachable through envelope/vectors.jsonl
	familyLicense     = "license"     // reachable through license/vectors.jsonl
	familyIntegration = "integration" // needs a journal or a live store; no fixture family yet
)

// normativeRules is the population any conformance score is measured over. A rule
// belongs here when the spec states it as a MUST, the kernel has a single place that
// enforces it, and it can be disabled without changing unrelated semantics — the
// third condition matters, because a switch that perturbs two obligations at once
// cannot attribute a failure to either.
var normativeRules = []normativeRule{
	{"8.6.1/version-tag", "§8.6.1", "the first line must be exactly the format tag", familyEnvelope},
	{"8.6.1/lowercase-hex", "§8.6.1", "hashes and keys must be lowercase hex", familyEnvelope},
	{"8.6.1/canonical-decimal", "§8.6.1", "parent_rev must be canonical decimal", familyEnvelope},
	{"8.6.1/parent-consistency", "§8.6.1", "the parent sentinel and revision 0 hold iff each other", familyEnvelope},
	{"8.6.1/value-characters", "§8.6.1", "values must exclude LF, CR and control characters", familyEnvelope},
	{"8.6.1/field-order", "§8.6.1", "envelope members appear in a fixed order", familyEnvelope},
	{"8.6.1/field-count", "§8.6.1", "the envelope has exactly six members", familyEnvelope},
	{"8.6.1/trailing-lf", "§8.6.1", "the envelope ends with a newline", familyEnvelope},
	{"8.6.1/reencode", "§8.6.1", "parsed octets must re-encode to themselves", familyEnvelope},
	{"8.6.3/base64-dialect", "§8.6.3", "envelope_b64 is standard padded base64", familyEnvelope},
	{"8.6.3/base64-canonical", "§8.6.3", "envelope_b64 must re-encode to itself", familyEnvelope},
	{"8.6.4/principal-binding", "§8.6.4", "the signer must be the authenticated principal", familyEnvelope},
	{"8.6.4/artifact-recompute", "§8.6.4", "the signed artifact must equal the recomputed hash", familyEnvelope},
	{"8.6.4/name-match", "§8.6.4", "the signed name must be the name being published", familyEnvelope},
	{"8.6.4/parent-current", "§8.6.4", "the signed parent must be the current binding", familyEnvelope},
	{"8.6.4/revision-current", "§8.6.4", "the signed revision must be current (ABA)", familyEnvelope},
	{"8.6.4a/signature-valid", "§8.6.4a", "the signature must verify over the decoded octets", familyEnvelope},
	{"8.6.4a/small-order", "§8.6.4a", "a small-order author key must be rejected", familyEnvelope},

	// LICENSE EVALUATION (DESIGN.md "What belongs inside identity"). These govern a
	// consumer-visible derived claim with legal consequence, so the dangerous direction
	// is a FALSE PERMISSION: a mutation turning an unknown or prohibited composition
	// into YES must be caught. A false UNSTATED is inconvenient; a false YES is harmful.
	{"license/unstated-contagion", "DESIGN", "UNSTATED propagates: one unknown input makes the composition unknown", familyLicense},
	{"license/prohibition-dominates", "DESIGN", "a known prohibition binds the whole composition", familyLicense},
	{"license/unknown-unstated", "DESIGN", "an unmodelled identifier yields UNSTATED, never a grant", familyLicense},
	{"license/compound-unstated", "DESIGN", "compound expressions are not resolved by the registry", familyLicense},
	{"license/digest-binds-method", "DESIGN", "engine, model and policy are bound by the evaluation digest", familyLicense},
	{"license/digest-binds-inputs", "DESIGN", "every consumed assertion is bound by the evaluation digest", familyLicense},
	{"license/digest-order-invariant", "DESIGN", "input order does not change the evaluation digest", familyLicense},

	// Reachable only with a journal or a live store. Listed so the inventory is
	// complete and the gap is visible, but EXCLUDED from the envelope suite's
	// denominator — see Family.
	{"8.6.2/revision-fold", "§8.6.2", "the legacy transition derivation folds a name's history", familyIntegration},
	{"8.6.2/no-op-revision", "§8.6.2", "a same-hash re-publication does not advance the revision", familyIntegration},
	{"8.2.1/member-order", "§8.2.1", "journal members appear in the normative order", familyIntegration},
	{"8.2.1/escaping", "§8.2.1", "journal strings use minimal escaping", familyIntegration},
	{"8.2.2/entry-digest", "§8.2.2", "an entry digest is SHA-256 over the canonical line", familyIntegration},
}

// rulesInFamily returns the denominator for one suite.
func rulesInFamily(fam string) []normativeRule {
	var out []normativeRule
	for _, r := range normativeRules {
		if r.Family == fam {
			out = append(out, r)
		}
	}
	return out
}

// ruleInventoryDigest identifies the POPULATION a score was measured over.
//
// Campaign identity taught this lesson once already (SPEC §11): a bare score whose
// denominator or method can change invisibly is a number that cannot be compared with
// itself. "2 of 17" means nothing without knowing which 17 — and a rule added to the
// inventory silently lowers every historical score while looking like a regression.
func ruleInventoryDigest(fam string) string {
	var b strings.Builder
	b.WriteString("oath-rule-inventory/1\n")
	b.WriteString("family=" + fam + "\n")
	rs := rulesInFamily(fam)
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	for _, r := range rs {
		b.WriteString("rule=" + r.ID + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ruleKnown guards against the score silently measuring a rule that no enforcement
// site consults — a typo in an ID would otherwise show up as an "unwitnessed" rule
// and be indistinguishable from a genuine coverage gap.
func ruleKnown(id string) bool {
	for _, r := range normativeRules {
		if r.ID == id {
			return true
		}
	}
	return false
}
