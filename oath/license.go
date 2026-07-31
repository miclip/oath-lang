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
	"fmt"
	"sort"
	"strings"
)

// Engine and model identity. Both are part of an evaluation's digest, so a change to
// either makes historical verdicts distinguishable rather than silently reinterpreted.
const (
	licenseEngine       = "oath-license/1"
	licenseModelVersion = "spdx-lattice/1"
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
}

// modelLookup resolves an asserted expression. Compound expressions are NOT parsed:
// "MIT OR Apache-2.0" requires choosing a disjunct, which is a decision with legal
// consequence that belongs to the consumer, not to the registry.
func modelLookup(expr string) (grants, string) {
	if expr == "" || expr == noLicense {
		return grants{}, "no terms asserted"
	}
	if g, ok := licenseModel[expr]; ok {
		return g, ""
	}
	if ruleOn("license/compound-unstated") {
		if strings.Contains(expr, " OR ") {
			return grants{}, "compound expression: choosing a disjunct is the consumer's decision, not the registry's"
		}
		if strings.Contains(expr, " AND ") || strings.Contains(expr, " WITH ") {
			return grants{}, "compound expression the model does not evaluate"
		}
	} else if i := strings.Index(expr, " OR "); i > 0 {
		// MUTATION: resolve to the first disjunct. Plausible and wrong — it silently
		// picks terms on the consumer's behalf.
		if g, ok := licenseModel[expr[:i]]; ok {
			return g, ""
		}
	}
	if !ruleOn("license/unknown-unstated") {
		// MUTATION: the dangerous direction. An unmodelled identifier becomes a full
		// grant, so an unknown composition reads as permitted.
		return grants{Commercial: triYes, Redistribute: triYes, Modify: triYes,
			PatentGrant: triYes, ShareAlike: triNo}, ""
	}
	return grants{}, "identifier not in the model"
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
	if ruleOn("license/prohibition-dominates") && (acc == triNo || next == triNo) {
		return triNo
	}
	if ruleOn("license/unstated-contagion") && (acc == triUnstated || next == triUnstated) {
		return triUnstated
	}
	return triYes
}

// combineObligation folds an OBLIGATION. YES dominates, because a reciprocal
// requirement anywhere binds the whole composition. UNSTATED remains contagious over
// NO: not knowing whether an obligation exists is not the same as knowing there is
// none, and only the second is safe to report.
func combineObligation(acc, next tri) tri {
	if ruleOn("license/prohibition-dominates") && (acc == triYes || next == triYes) {
		return triYes
	}
	if ruleOn("license/unstated-contagion") && (acc == triUnstated || next == triUnstated) {
		return triUnstated
	}
	return triNo
}

type licenseInput struct {
	Name    string
	License string
	Reason  string // why the model could not answer, when it could not
}

type licenseEvaluation struct {
	Policy    string
	Engine    string
	Model     string
	Digest    string
	Result    grants
	Inputs    []licenseInput
	Unmodeled int // inputs the model could not interpret
}

// evaluateLicensing derives what an artifact's closure permits, from the terms each
// publication asserted.
func evaluateLicensing(st *Store, name string, deps []string) licenseEvaluation {
	ev := licenseEvaluation{Policy: "composition", Engine: licenseEngine, Model: licenseModelVersion}

	add := func(n string) {
		lic := assertedLicense(st, n)
		g, reason := modelLookup(lic)
		if lic == "" {
			lic = "(none)"
		}
		ev.Inputs = append(ev.Inputs, licenseInput{Name: n, License: lic, Reason: reason})
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

	add(name)
	for _, d := range deps {
		// Dependencies are listed by hash in the decision package; resolve to a name so
		// the evaluation reports something a reader can act on.
		if n := nameOfHash(st, d); n != "" {
			add(n)
		} else {
			ev.Inputs = append(ev.Inputs, licenseInput{Name: shortHash(d), License: "(unresolved)",
				Reason: "dependency has no bound name, so no publication asserted terms for it"})
			ev.Unmodeled++
			ev.Result = grants{} // unknown input makes the whole composition unknown
		}
	}
	ev.Digest = evaluationDigest(ev)
	return ev
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
func evaluationDigest(ev licenseEvaluation) string {
	var b strings.Builder
	b.WriteString("oath-license-eval/1\n")
	if ruleOn("license/digest-binds-method") {
		b.WriteString("engine=" + ev.Engine + "\n")
		b.WriteString("model=" + ev.Model + "\n")
		b.WriteString("policy=" + ev.Policy + "\n")
	}
	in := append([]licenseInput(nil), ev.Inputs...)
	if ruleOn("license/digest-order-invariant") {
		sort.Slice(in, func(i, j int) bool { return in[i].Name < in[j].Name })
	}
	if ruleOn("license/digest-binds-inputs") {
		for _, i := range in {
			b.WriteString("input=" + i.Name + "=" + i.License + "\n")
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

type licenseVector struct {
	Kind       string            `json:"kind"`
	Label      string            `json:"label"`
	Witnesses  string            `json:"witnesses,omitempty"`
	Policy     string            `json:"policy"`
	Engine     string            `json:"engine"`
	Model      string            `json:"model"`
	Assertions []string          `json:"assertions"`
	Expect     map[string]string `json:"expect,omitempty"`
	Digest     string            `json:"digest,omitempty"`
}

// runLicenseVectors executes the family and returns each FAILURE. Empty means pass.
func runLicenseVectors(vs []licenseVector) []string {
	var fail []string
	for _, v := range vs {
		switch v.Kind {
		case "evaluation":
			g := evalFromAssertions(v.Assertions)
			got := map[string]string{"commercial": g.Commercial.String(), "redistribute": g.Redistribute.String(),
				"modify": g.Modify.String(), "patent_grant": g.PatentGrant.String(), "share_alike": g.ShareAlike.String()}
			for k, want := range v.Expect {
				if got[k] != want {
					fail = append(fail, fmt.Sprintf("evaluation %q: %s = %s, want %s", v.Label, k, got[k], want))
				}
			}
		case "identity":
			in := make([]licenseInput, 0, len(v.Assertions))
			for _, a := range v.Assertions {
				n, l, _ := strings.Cut(a, "=")
				in = append(in, licenseInput{Name: n, License: l})
			}
			ev := licenseEvaluation{Policy: v.Policy, Engine: v.Engine, Model: v.Model, Inputs: in}
			if d := evaluationDigest(ev); d != v.Digest {
				fail = append(fail, fmt.Sprintf("identity %q: digest %s, want %s", v.Label, shortHash(d), shortHash(v.Digest)))
			}
		}
	}
	return fail
}
