package main

// The DECISION PACKAGE (#74, discovery v0). `oath find` answers "which
// definitions satisfy this?"; this answers the question an agent asks next:
// "should I use this one, and what am I trusting if I do?"
//
// A conventional registry ranks by popularity, which is a proxy for other
// people's judgement. Oath can rank by EVIDENCE and hand over the evidence
// itself — proof status per property, spec strength, provenance, the exact
// dependency closure, and, most importantly, the LIMITATIONS. An agent choosing
// between artifacts needs the honest failure modes more than it needs the
// claims: `tested` is not `proven`, a waived mutant is a judgement call someone
// made, and a low mutation score means the specification pins little even when
// every property passes.
//
// Everything here is derived from recorded state. Nothing is inferred, nothing
// is scored heuristically, and where a fact is absent it is reported as absent
// rather than as a zero.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type explainProp struct {
	Name   string `json:"name"`
	Hash   string `json:"hash"`   // the property's own content hash (spec identity)
	Status string `json:"status"` // proven | tested | falsified
}

type explainWaiver struct {
	Mutant string `json:"mutant"`
	Desc   string `json:"desc"`
	Reason string `json:"reason"`
	By     string `json:"by"`
}

type explainPkg struct {
	Name         string          `json:"name"`
	Hash         string          `json:"hash"`
	Guarantee    string          `json:"guarantee"`
	Termination  string          `json:"termination,omitempty"`
	Confinement  []string        `json:"confinement,omitempty"`
	Properties   []explainProp   `json:"properties"`
	SpecStrength *specStrength   `json:"spec_strength,omitempty"`
	Waivers      []explainWaiver `json:"waivers,omitempty"`
	Provenance   explainProv     `json:"provenance"`
	Dependencies []string        `json:"dependencies"`
	Limitations  []string        `json:"limitations"`
}

type specStrength struct {
	Killed int     `json:"killed"`
	Total  int     `json:"total"`
	Score  float64 `json:"score"`
	// Campaign identifies the measurement that produced this score, and State
	// tells a consumer whether to believe it: MEASURED means the score comes
	// from the engine currently in use, STALE means it was produced by a
	// superseded one and describes a mutant set that no longer exists. A score
	// without that distinction is a number whose provenance cannot be checked.
	Campaign string `json:"campaign,omitempty"`
	// CurrentCampaign is what a fresh measurement of this artifact WOULD be
	// identified by. A consumer compares the two hashes rather than reasoning
	// about versions and dates.
	CurrentCampaign string `json:"current_campaign,omitempty"`
	State           string `json:"state"` // MEASURED | STALE
}

// Authorship evidence ladder. A boolean `separated` conflated two different
// claims: that two distinct private keys produced valid signatures, and that two
// independently controlled authors produced the spec and body. Only the first is
// re-derivable from the record, so only the first may be reported as fact.
//
// The rungs are deliberately named for what is ESTABLISHED, not for the process
// that produced it — a name like "separated" invites a reader to assume the
// stronger property. Note the ceiling: even attested separate custody proves
// CONTROL separation, never independent thought. Two agents can hold
// uncompromisable separate keys and still receive the same hidden context, or be
// orchestrated toward the same mistake. No signing arrangement can close that
// gap, so no rung claims to.
const (
	// authSamePrincipal: spec and body signed by the same key.
	authSamePrincipal = "SAME_PRINCIPAL"
	// authDistinctKeys: different keys signed, but the registry has no evidence
	// that CONTROL was separated — one process holding both key files produces
	// exactly this record. This is the honest ceiling until custody is attestable.
	authDistinctKeys = "DISTINCT_KEYS_CUSTODY_UNVERIFIED"
	// authSeparateCustody: the signing arrangement provides independently
	// checkable evidence that one process could not use both keys. Not yet
	// reachable — no mechanism here yet earns it, so nothing emits it.
	authSeparateCustody = "SEPARATE_CUSTODY_ATTESTED"
	// authUnattributed: no authorship recorded at all.
	authUnattributed = "UNATTRIBUTED"
)

type explainProv struct {
	Author     string `json:"author,omitempty"`
	SpecAuthor string `json:"spec_author,omitempty"`
	BodyAuthor string `json:"body_author,omitempty"`
	// Authorship is the ladder rung this artifact's record actually supports.
	Authorship string `json:"authorship"`
	// Owner is the principal that FIRST published this name (#84) — who may repoint
	// it, where trust-on-first-publish is enabled. OwnerSource says where that
	// authority came from, which is as decision-relevant as its strength: a key
	// named in the CURRENT policy file is editable by whoever holds the store and
	// must never read as historical cryptographic evidence.
	Owner       string `json:"owner,omitempty"`
	OwnerSource string `json:"owner_source,omitempty"`
	// License is the terms the PUBLISHER asserted in the signed publication envelope.
	// It is an assertion, never a derivation: the registry can later evaluate
	// compatibility across a dependency closure, and reporting the two as one claim is
	// the conflation this project exists to avoid (DESIGN.md, "What belongs inside
	// identity"). Empty means no publication carried terms.
	License string `json:"license,omitempty"`
}

// authorshipLevel places an artifact on the ladder from recorded state alone.
//
// It deliberately does NOT consult whether the journal entries were signed —
// that is a separate axis (is the attribution evidence or the registry's word?)
// reported as its own limitation. Folding the two together would let a signed
// same-key artifact outrank an unsigned distinct-key one on a scale that is
// supposed to measure only control separation.
func authorshipLevel(specAuthor, bodyAuthor string) string {
	if specAuthor == "" || bodyAuthor == "" {
		return authUnattributed
	}
	if specAuthor == bodyAuthor {
		return authSamePrincipal
	}
	return authDistinctKeys
}

// unsignedAttribution reports whether this artifact's recorded authorship rests
// on unsigned journal entries — i.e. whether an auditor must take the registry's
// word for who authored it.
//
// It walks the whole NAME LINEAGE backwards through Prev, not just the entry for
// this hash, because spec_author is INHERITED: when a put leaves the props
// unchanged, the spec author carries over from the object the name previously
// pointed at. So "which key signed the spec" is only answerable if the earlier
// entry that introduced those props was itself signed. One unsigned link makes
// the inherited half of the attribution unverifiable, however well-signed the
// most recent put was.
//
// Conservative by construction: an object with no accepted entry at all, or a
// lineage that runs out, counts as unverifiable rather than clean. Absence of a
// record is not evidence of authorship.
func unsignedAttribution(st *Store, h string) bool {
	accepted := map[string][]LogEntry{}
	for _, e := range st.ReadLog() {
		if e.Status == "accepted" && e.Hash != "" {
			accepted[e.Hash] = append(accepted[e.Hash], e)
		}
	}
	seen := map[string]bool{}
	for cur := h; cur != ""; {
		if seen[cur] { // a repoint cycle (A→B→A) is finite evidence, not an error
			return false
		}
		seen[cur] = true
		es, ok := accepted[cur]
		if !ok {
			return true
		}
		signed, prev := false, ""
		for _, e := range es {
			// The AUTHOR's envelope is what makes attribution verifiable: it binds a
			// key to this exact publication. The registry's own Pubkey/Sig pair proves
			// CUSTODY (the entry has not been altered since it was written) and says
			// nothing about who authored it — a registry could sign an entry naming
			// anyone. Either is accepted here because both were once the only
			// available form, but they are not equivalent, and the envelope is the one
			// a third party can check without trusting the registry.
			if e.EnvelopeB64 != "" && e.AuthorSig != "" && e.AuthorPubkey != "" {
				signed = true
			}
			if e.Sig != "" && e.Pubkey != "" {
				signed = true
			}
			if e.Prev != "" {
				prev = e.Prev
			}
		}
		if !signed {
			return true
		}
		cur = prev
	}
	return false
}

// buildExplain assembles the decision package for one definition.
func buildExplain(st *Store, name string) (*explainPkg, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return nil, fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return nil, err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return nil, err
	}

	proven := map[int]bool{}
	for _, pi := range m.ProvenProps {
		proven[pi] = true
	}
	falsified := map[string]bool{}
	for _, fn := range m.Guarantee.Falsified {
		falsified[fn] = true
	}

	pkg := &explainPkg{
		Name: name, Hash: h,
		Guarantee:   guaranteeString(m.Guarantee),
		Termination: m.Termination,
		Confinement: m.Confinement,
		Provenance: explainProv{
			Author: m.Author, SpecAuthor: m.SpecAuthor, BodyAuthor: m.BodyAuthor,
			// The split-agent result made structural: spec and body written by
			// different principals is a stronger artifact than one author's
			// self-assessment, and a consumer should be able to see which it is —
			// but only as far up the ladder as the record actually reaches.
			Authorship: authorshipLevel(m.SpecAuthor, m.BodyAuthor),
		},
	}

	pkg.Provenance.Owner, pkg.Provenance.OwnerSource = nameOwner(st, name)
	pkg.Provenance.License = assertedLicense(st, name)

	for pi := range d.Props {
		pn := metaPropName(m, pi)
		status := "tested"
		switch {
		case falsified[pn]:
			status = "falsified"
		case proven[pi]:
			status = "proven"
		}
		pkg.Properties = append(pkg.Properties, explainProp{
			Name: pn, Hash: propHash(&d.Props[pi]), Status: status,
		})
	}

	if m.MutantsTotal > 0 {
		// Waivers count toward the score because they carry recorded
		// justification — but they are listed separately so a consumer can
		// judge the justification rather than take the number on trust.
		killed := m.MutantsKilled + len(m.WaivedMutants)
		state := "MEASURED"
		current := campaignHash(h, m.WaivedMutants)
		if m.MutationCampaign != current {
			state = "STALE"
		}
		pkg.SpecStrength = &specStrength{
			Killed: killed, Total: m.MutantsTotal,
			Score:    float64(killed) / float64(m.MutantsTotal),
			Campaign: m.MutationCampaign, CurrentCampaign: current, State: state,
		}
	}
	for _, w := range m.WaivedMutants {
		pkg.Waivers = append(pkg.Waivers, explainWaiver{
			Mutant: shortHash(w.Hash), Desc: w.Desc, Reason: w.Reason, By: w.By,
		})
	}

	for dep := range collectDeps(d) {
		if n := st.NameOf(dep); n != "" {
			pkg.Dependencies = append(pkg.Dependencies, fmt.Sprintf("%s #%s", n, shortHash(dep)))
		} else {
			pkg.Dependencies = append(pkg.Dependencies, "#"+shortHash(dep))
		}
	}
	sort.Strings(pkg.Dependencies)

	pkg.Limitations = explainLimitations(st, pkg, m)
	return pkg, nil
}

// explainLimitations is the part that matters most: the honest reasons NOT to
// pick this artifact. Everything here is derived from recorded state, so a
// definition cannot look better than its evidence.
func explainLimitations(st *Store, p *explainPkg, m *Meta) []string {
	var out []string
	var unproven []string
	for _, pr := range p.Properties {
		switch pr.Status {
		case "falsified":
			out = append(out, fmt.Sprintf("property %q is FALSIFIED — a counterexample exists", pr.Name))
		case "tested":
			unproven = append(unproven, pr.Name)
		}
	}
	if len(unproven) > 0 {
		out = append(out, fmt.Sprintf("%d of %d properties are TESTED, not proven (%s) — they hold on generated cases, not for all inputs",
			len(unproven), len(p.Properties), strings.Join(unproven, ", ")))
	}
	if len(p.Properties) == 0 {
		out = append(out, "no properties: nothing about this definition has been verified")
	}
	if p.Termination == "unknown" {
		out = append(out, "termination is UNPROVEN — this definition may not halt, and its defining equation is not asserted in proofs")
	}
	for i, c := range p.Confinement {
		if c == "escapes" {
			out = append(out, fmt.Sprintf("capability parameter %d ESCAPES (stored or returned) — it cannot receive real authority via `oath build`", i))
		}
	}
	switch {
	case p.SpecStrength == nil:
		out = append(out, "spec strength UNMEASURED — no mutation score recorded, so how much the properties actually pin is unknown")
	case p.SpecStrength.State == "STALE":
		out = append(out, fmt.Sprintf("spec strength is STALE — %d/%d was measured by campaign %q, superseded by %q; the mutant set it describes no longer exists",
			p.SpecStrength.Killed, p.SpecStrength.Total, shortHash(orNone(p.SpecStrength.Campaign)), shortHash(p.SpecStrength.CurrentCampaign)))
	case p.SpecStrength.Score < 0.5:
		out = append(out, fmt.Sprintf("spec strength is LOW (%d/%d mutants caught): the properties pass but constrain little, so passing them is weak evidence",
			p.SpecStrength.Killed, p.SpecStrength.Total))
	}
	if len(m.WaivedMutants) > 0 {
		out = append(out, fmt.Sprintf("%d surviving mutant(s) WAIVED as equivalent — judgement calls, listed with their justifications", len(m.WaivedMutants)))
	}
	// Authorship limitations, one per rung. The DISTINCT_KEYS case still carries a
	// limitation: two key files on one machine, used by one process, produce
	// exactly that record, and dropping the caveat there would let the registry
	// vouch for control separation it cannot observe.
	switch p.Provenance.Authorship {
	case authUnattributed:
		out = append(out, "authorship is UNATTRIBUTED — no principal is recorded for the spec or the body, so there is nothing to hold accountable for either")
	case authSamePrincipal:
		out = append(out, "spec and body share an author — no authorship separation, so the specification was not written independently of the code")
	case authDistinctKeys:
		out = append(out, "spec and body were signed by DISTINCT KEYS, but key custody and independent control were NOT verified — one process holding both keys produces this same record, so this is not evidence of independent authorship")
	}
	// Licensing. The publisher's terms are an assertion; nothing here has evaluated
	// them against the dependency closure, and saying so is the point — a consumer who
	// reads a licence off an artifact and acts on it is making a legal decision, and
	// this system has derived none of it.
	switch p.Provenance.License {
	case "":
		out = append(out, "no publication of this name asserted licensing terms — reuse rights are UNSTATED, not permissive")
	case noLicense:
		out = append(out, "the publisher explicitly asserted NO licensing terms — reuse rights are unstated, which is not the same as granted")
	default:
		out = append(out, fmt.Sprintf("licensing is the publisher's ASSERTION (%s), not a derived fact: nothing here has evaluated it against the %d dependency(ies), and compatibility of a composition is a separate question this does not answer",
			p.Provenance.License, len(p.Dependencies)))
	}
	// Control of the NAME, separate from authorship of the code. A label owner is
	// still enforced where trust-on-first-publish is on, but it is not cryptographic
	// ownership, and a consumer deciding whether to depend on this name should know
	// which of the two is protecting it.
	if p.Provenance.Owner != "" && !ownerIsCryptographic(p.Provenance.OwnerSource) {
		out = append(out, fmt.Sprintf("the name is owned by %q via %s — %s, so who may repoint this name is NOT independently checkable from the journal",
			p.Provenance.Owner, p.Provenance.OwnerSource, ownerSourceMeaning(p.Provenance.OwnerSource)))
	}
	// A separate axis from the ladder: is the attribution EVIDENCE, or the
	// registry's word for it? An unsigned journal entry records a pubkey the
	// registry chose to write down. It may well have verified a signature at
	// request time, but that verification left no artifact, so no third party can
	// re-derive who authored this — and unverifiable attribution is exactly what
	// the rest of this system refuses to report as fact.
	if p.Provenance.Authorship != authUnattributed && unsignedAttribution(st, p.Hash) {
		out = append(out, "the authorship above is NOT independently verifiable — the journal entries recording it carry no signature, so the recorded principals are the registry's assertion rather than evidence an auditor can check")
	}
	if len(out) == 0 {
		out = append(out, "none recorded")
	}
	return out
}

func cmdExplain(st *Store, name string, asJSON bool) {
	pkg, err := buildExplain(st, name)
	if err != nil {
		fail(err)
	}
	if asJSON {
		b, _ := json.MarshalIndent(pkg, "", "  ")
		fmt.Println(string(b))
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  #%s\n  %s\n", pkg.Name, shortHash(pkg.Hash), pkg.Guarantee)
	if pkg.Termination != "" {
		fmt.Fprintf(&b, "  termination: %s\n", pkg.Termination)
	}
	b.WriteString("\nSPEC (properties, by content hash — the identity of the claim):\n")
	for _, pr := range pkg.Properties {
		fmt.Fprintf(&b, "  %-10s %-28s #%s\n", pr.Status, pr.Name, shortHash(pr.Hash))
	}
	if pkg.SpecStrength != nil {
		fmt.Fprintf(&b, "\nSPEC STRENGTH: %d/%d mutants caught (%.0f%%)\n",
			pkg.SpecStrength.Killed, pkg.SpecStrength.Total, pkg.SpecStrength.Score*100)
	}
	for _, w := range pkg.Waivers {
		fmt.Fprintf(&b, "  waived %s (%s): %s — %s\n", w.Mutant, w.Desc, w.Reason, w.By)
	}
	fmt.Fprintf(&b, "\nPROVENANCE: author=%s spec=%s body=%s\n            authorship: %s\n",
		orNone(pkg.Provenance.Author), orNone(pkg.Provenance.SpecAuthor),
		orNone(pkg.Provenance.BodyAuthor), pkg.Provenance.Authorship)
	if l := pkg.Provenance.License; l != "" && l != noLicense {
		fmt.Fprintf(&b, "            license: %s (ASSERTED by the publisher, signed; NOT evaluated)\n", l)
	}
	if pkg.Provenance.Owner != "" {
		fmt.Fprintf(&b, "            name owner: %s\n              via %s (%s)\n",
			pkg.Provenance.Owner, pkg.Provenance.OwnerSource,
			ownerSourceMeaning(pkg.Provenance.OwnerSource))
	}
	fmt.Fprintf(&b, "\nDEPENDENCIES (%d, exact by hash):\n", len(pkg.Dependencies))
	for _, dep := range pkg.Dependencies {
		fmt.Fprintf(&b, "  %s\n", dep)
	}
	b.WriteString("\nLIMITATIONS:\n")
	for _, l := range pkg.Limitations {
		fmt.Fprintf(&b, "  · %s\n", l)
	}
	fmt.Print(b.String())
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// ownerSourceMeaning spells out what an ownership source does and does not
// establish. Written out at every call site rather than left to the reader,
// because "owner" reads as authoritative regardless of where it came from.
func ownerSourceMeaning(source string) string {
	switch source {
	case ownerSignedFirstPublish:
		return "historical and cryptographic: re-verifiable from the journal alone"
	case ownerSignedAdoption:
		return "authority adopted by a signed operation at a recorded point, not retroactive"
	case ownerLegacyLabel:
		return "historical but NOT cryptographic: a principal string the registry recorded on an unsigned entry"
	case ownerConfiguredPolicy:
		return "present configuration, NOT history: editable by whoever holds the store"
	}
	return "unrecorded"
}

// assertedLicense reports the terms the most recent APPLIED publication of a name
// asserted, read from its signed envelope.
//
// Read from the envelope rather than stored separately, so it is the author's signed
// statement and not a field the registry could have written. Returns "" when no
// publication carried terms — distinct from noLicense, which is a publisher choosing to
// assert none.
func assertedLicense(st *Store, name string) string {
	lic := ""
	for _, t := range nameTransitions(st.ReadLog(), name) {
		if t.Transition != transitionApplied || t.Entry.EnvelopeB64 == "" {
			continue
		}
		octets, err := decodeEnvelopeB64(t.Entry.EnvelopeB64)
		if err != nil {
			continue
		}
		if env, perr := envelopeParse(octets); perr == nil {
			lic = env.License
		}
	}
	return lic
}
