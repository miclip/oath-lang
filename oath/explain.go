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
	Status string `json:"status"` // proven | tested | indeterminate | falsified
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
	// depHashes is the same closure as Dependencies but as RAW 64-hex artifact
	// hashes. Dependencies is a DISPLAY form ("append #78d23e27" — name plus a
	// SHORT hash) and passing it to a consumer that expects hashes silently
	// produced garbage: licence evaluation resolved every dependency to an empty
	// name, reported them all as unmodelled, and bound display strings into the
	// §12.4 digest where the spec requires 64 lowercase hex. Unexported so it
	// cannot leak into the JSON payload as a second, confusable dependency list.
	depHashes   []string
	Limitations []string `json:"limitations"`
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
	// authSamePrincipal: spec and body attributed to the same principal.
	authSamePrincipal = "SAME_PRINCIPAL"
	// authDistinctPrincipals: different principals, and the journal does NOT
	// show both lineages established by valid author signatures from those
	// principals' keys — so whether any key was held is unknown. Two
	// write-scoped bearer tokens, which involve no key at all, produce exactly
	// this record.
	authDistinctPrincipals = "DISTINCT_PRINCIPALS_CUSTODY_UNVERIFIED"
	// authDistinctKeys: different principals, and each lineage's establishing
	// write carries a valid author envelope signed by that principal's key
	// (lineageEvidence) — so two distinct keys demonstrably signed. CONTROL
	// separation is still unobserved: one process holding both key files
	// produces exactly this record. That is the gap the rung above names.
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
	// SpecLineage and BodyLineage are the journal-derived evidence for who
	// ESTABLISHED each lineage (#82): the entry where it last changed, and
	// whether a valid author signature covers that exact transition. Exposed
	// whatever the rung, so a mixed or unrecoverable history stays observable
	// rather than being summarised away. Derived on every call, never stored.
	SpecLineage lineageTier `json:"spec_lineage"`
	BodyLineage lineageTier `json:"body_lineage"`
	// Owner is the principal that FIRST published this name (#84) — who may repoint
	// it, where trust-on-first-publish is enabled. OwnerSource says where that
	// authority came from, which is as decision-relevant as its strength: a key
	// named in the CURRENT policy file is editable by whoever holds the store and
	// must never read as historical cryptographic evidence.
	Owner       string `json:"owner,omitempty"`
	OwnerSource string `json:"owner_source,omitempty"`
	// Namespace and NamespaceHolder record PREFIX AUTHORITY (§8.7), which is a
	// different question from Owner: Owner says who may repoint THIS name,
	// Namespace says who governs the prefix it sits under and may create names
	// there. A consumer evaluating a dependency needs both — a name can be owned
	// by one key inside a namespace governed by another, which is exactly what
	// RES-NO-CAPTURE preserves.
	Namespace       string `json:"namespace,omitempty"`
	NamespaceHolder string `json:"namespace_holder,omitempty"`
	// NamespaceDelegates are keys the HOLDER has permitted to publish under the
	// prefix. They are listed separately from the holder and MUST NOT be rendered
	// as owners: a delegate holds revocable permission, never authority.
	NamespaceDelegates []string `json:"namespace_delegates,omitempty"`
	// AppliedVia is the store's DECLARATION of how it applied the write that bound
	// this name (SPEC §8.6.3). Empty means the journal does not say.
	//
	// THERE IS DELIBERATELY NO "SIGNED" COMPANION FIELD. §8.4 excludes this member
	// from the entry signature because the STORE assigns it, so no signature ever
	// covers it — for any entry, signed or not. A flag pairing it with the entry's
	// signedness would invite exactly the reading it must never receive: that a
	// signed entry's mechanism is attested. The journal CHAIN does seal it (the
	// chain is computed over the whole entry), which is a weaker and different
	// claim, and one `oath audit` is what verifies.
	AppliedVia string `json:"applied_via,omitempty"`
	// License is the terms the PUBLISHER asserted in the signed publication envelope.
	// It is an assertion, never a derivation: the registry can later evaluate
	// compatibility across a dependency closure, and reporting the two as one claim is
	// the conflation this project exists to avoid (DESIGN.md, "What belongs inside
	// identity"). Empty means no publication carried terms.
	License string `json:"license,omitempty"`
}

// authorshipLevel places an artifact on the ladder.
//
// Signedness raises a rung only where it changes what separation means: two
// DISTINCT labels backed by two valid author signatures are two keys, which is
// strictly more than two labels. It never lifts SAME_PRINCIPAL — one key
// signing both lineages is still one author — so a signed same-key artifact
// cannot outrank an unsigned distinct-principal one.
//
// Each lineage must be signed by the key its OWN label names — both the
// per-hash label in meta and the per-NAME label the establishing entry records.
// A signature by some other key establishes that key, not the recorded
// principal. The name-scoped check matters because meta is shared by every name
// bound to the object: an alias publication can rewrite those labels to match
// another name's signing keys.
func authorshipLevel(specAuthor, bodyAuthor string, spec, body lineageTier) string {
	if specAuthor == "" || bodyAuthor == "" {
		return authUnattributed
	}
	if specAuthor == bodyAuthor {
		return authSamePrincipal
	}
	if spec.Tier == lineageKeySigned && spec.Pubkey == specAuthor && spec.Principal == specAuthor &&
		body.Tier == lineageKeySigned && body.Pubkey == bodyAuthor && body.Principal == bodyAuthor {
		return authDistinctKeys
	}
	return authDistinctPrincipals
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
	// Properties that reached NO VERDICT (SPEC §4.1). Without this they fall
	// through to the "tested" default below and `explain` reports that they
	// held on generated cases — a claim nothing observed.
	indeterminate := map[string]bool{}
	for _, in := range m.Guarantee.Indeterminate {
		indeterminate[in] = true
	}

	pkg := &explainPkg{
		Name: name, Hash: h,
		Guarantee:   guaranteeString(m.Guarantee),
		Termination: m.Termination,
		Confinement: m.Confinement,
		Provenance: explainProv{
			Author: m.Author, SpecAuthor: m.SpecAuthor, BodyAuthor: m.BodyAuthor,
		},
	}
	// The split-agent result made structural: spec and body written by
	// different principals is a stronger artifact than one author's
	// self-assessment, and a consumer should be able to see which it is — but
	// only as far up the ladder as the record actually reaches.
	pkg.Provenance.SpecLineage, pkg.Provenance.BodyLineage = lineageEvidenceAt(st, name, h)
	// NAME-SCOPED principals when the derivation supplies them. `m.SpecAuthor`
	// and `m.BodyAuthor` live in metadata keyed by HASH, and structurally
	// identical definitions are ONE object with several names — so an unrelated
	// alias first-publishing this object can rewrite them, and a definition whose
	// lineages really were established by distinct keys would read SAME_PRINCIPAL
	// because of a write under another name. The lineage derivation replays THIS
	// name's journal, so its principals are the ones this name's ladder must use;
	// the metadata fields remain the fallback for a lineage that reports none.
	specPrincipal, bodyPrincipal := m.SpecAuthor, m.BodyAuthor
	if p := pkg.Provenance.SpecLineage.Principal; p != "" {
		specPrincipal = p
	}
	if p := pkg.Provenance.BodyLineage.Principal; p != "" {
		bodyPrincipal = p
	}
	pkg.Provenance.Authorship = authorshipLevel(specPrincipal, bodyPrincipal,
		pkg.Provenance.SpecLineage, pkg.Provenance.BodyLineage)

	pkg.Provenance.AppliedVia = bindingAppliedVia(st, name, h)
	pkg.Provenance.Owner, pkg.Provenance.OwnerSource = nameOwner(st, name)
	if r, ok := governingReservation(st, name); ok {
		pkg.Provenance.Namespace, pkg.Provenance.NamespaceHolder = r.Namespace, r.Pubkey
		// Only delegates whose SCOPE covers THIS name can publish it — a delegate
		// scoped to a different name under the prefix has no permission here, and
		// listing it as one who "may bind names here" would misreport authority.
		for k, scope := range delegates(st)[r.Namespace] {
			if patternSpecificity(scope, name) >= 0 {
				pkg.Provenance.NamespaceDelegates = append(pkg.Provenance.NamespaceDelegates, k)
			}
		}
		sortStringsInPlace(pkg.Provenance.NamespaceDelegates)
	}
	pkg.Provenance.License = assertedLicense(st, name)

	for pi := range d.Props {
		pn := metaPropName(m, pi)
		status := "tested"
		switch {
		case falsified[pn]:
			status = "falsified"
		case proven[pi]:
			// A proof outranks an indeterminate test run: it establishes the
			// property for ALL inputs, which is strictly more than any case
			// could have shown.
			status = "proven"
		case indeterminate[pn]:
			status = "indeterminate"
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
		pkg.depHashes = append(pkg.depHashes, dep)
		if n := st.NameOf(dep); n != "" {
			pkg.Dependencies = append(pkg.Dependencies, fmt.Sprintf("%s #%s", n, shortHash(dep)))
		} else {
			pkg.Dependencies = append(pkg.Dependencies, "#"+shortHash(dep))
		}
	}
	sort.Strings(pkg.Dependencies)
	sort.Strings(pkg.depHashes)

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
		case "indeterminate":
			// "at least one", not "no": a property is indeterminate as soon as
			// ONE case is unevaluable, and cases may well have passed alongside
			// it. Saying none could be evaluated would overstate the gap and
			// contradict the verify transcript, which reports both counts.
			out = append(out, fmt.Sprintf(
				"property %q reached NO VERDICT — at least one generated case could not be "+
					"evaluated, so it is neither tested nor refuted", pr.Name))
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
	case p.SpecStrength.Score < 0.5 && len(m.ProvenProps) > 0:
		// The old text said "the properties pass but constrain little, so passing
		// them is weak evidence". On a PROVEN definition that is false: the
		// properties do not merely pass, they hold for every input. A low score
		// there measures the GENERATOR's reach, not the specification's strength
		// — mutation scoring evaluates properties on generated cases and never
		// consults the prover. Measured: 15 of the corpus's 112 proven
		// definitions score under 50%, eight of them at zero, and 11 of
		// `hex-nibble`'s 42 survivors are refuted outright by its proven
		// properties. Stating the limitation this way keeps the number honest
		// without letting it libel the specification. (#130)
		out = append(out, fmt.Sprintf("generated mutation score is LOW (%d/%d mutants caught) but %d propert%s PROVEN for all inputs: the score measures which mutants generated executions distinguished, not which the specification excludes — run `oath mutate --prove %s` to classify the survivors",
			p.SpecStrength.Killed, p.SpecStrength.Total, len(m.ProvenProps), pluralize(len(m.ProvenProps), "y is", "ies are"), m.Name))
	case p.SpecStrength.Score < 0.5:
		out = append(out, fmt.Sprintf("spec strength is LOW (%d/%d mutants caught): the properties pass but constrain little, so passing them is weak evidence",
			p.SpecStrength.Killed, p.SpecStrength.Total))
	}
	if len(m.WaivedMutants) > 0 {
		out = append(out, fmt.Sprintf("%d surviving mutant(s) WAIVED as equivalent — judgement calls, listed with their justifications", len(m.WaivedMutants)))
	}
	// Authorship limitations, one per rung. Neither distinct rung drops its
	// caveat: two key files on one machine, used by one process, produce the
	// DISTINCT_KEYS record, and dropping the caveat there would let the registry
	// vouch for control separation it cannot observe. DISTINCT_PRINCIPALS says
	// key possession is UNKNOWN, not absent — the journal cannot tell a bearer
	// write from a key-holder's write that left no envelope.
	switch p.Provenance.Authorship {
	case authUnattributed:
		out = append(out, "authorship is UNATTRIBUTED — no principal is recorded for the spec or the body, so there is nothing to hold accountable for either")
	case authSamePrincipal:
		out = append(out, "spec and body share an author — no authorship separation, so the specification was not written independently of the code")
	case authDistinctPrincipals:
		// Say only what the lineage evidence leaves unestablished: when one
		// lineage IS key-signed, "whether either held a key is unknown" would
		// discard evidence the journal does carry.
		possession := "whether either held a signing key is UNKNOWN from this record — two bearer tokens holding no key at all produce it"
		if p.Provenance.SpecLineage.Tier == lineageKeySigned || p.Provenance.BodyLineage.Tier == lineageKeySigned {
			possession = "the journal does NOT show each lineage signed by its own principal's key (see the per-lineage evidence below)"
		}
		out = append(out, "spec and body are attributed to DISTINCT PRINCIPALS, but "+possession+" — and custody and independent control were NOT verified, so this is not evidence of independent authorship")
	case authDistinctKeys:
		out = append(out, "spec and body lineages were each established by a valid author signature from a DISTINCT KEY, but custody and independent control were NOT verified — one process holding both key files produces this same record, so this is not evidence of independent authorship")
	}
	// Per-lineage evidence, disclosed wherever it falls short. UNKNOWN is not
	// "unsigned": the establishing write may predate author envelopes, or be a
	// worker's repoint of a signed submission, and the journal does not say.
	if p.Provenance.Authorship != authUnattributed {
		for _, l := range []struct {
			what, meta string
			ev         lineageTier
		}{
			{"spec", p.Provenance.SpecAuthor, p.Provenance.SpecLineage},
			{"body", p.Provenance.BodyAuthor, p.Provenance.BodyLineage},
		} {
			// The NAME-scoped principal is what the establishing entry recorded.
			// Meta's label is per-hash and an alias can rewrite it, so it is only
			// the fallback when no establishing entry was identified.
			label := l.ev.Principal
			if label == "" {
				label = l.meta
			}
			switch {
			case l.ev.Tier == lineageUnknown:
				out = append(out, fmt.Sprintf("key possession for the %s lineage is UNKNOWN (%s) — the recorded principal %q is the registry's attribution, not something an auditor can tie to a key from the journal; this does not mean it was unsigned",
					l.what, lineageWhere(l.ev), label))
			case l.ev.Pubkey != label:
				out = append(out, fmt.Sprintf("the %s lineage was established by a valid signature from key %s… (journal seq %d), but that entry attributes it to %q — the signature evidences that key, not the recorded principal",
					l.what, shortHash(l.ev.Pubkey), l.ev.Seq, label))
			}
			if l.ev.Principal != "" && l.meta != l.ev.Principal {
				out = append(out, fmt.Sprintf("the %s author shown above (%q) is the object's SHARED attribution, rewritten by another name bound to the same object; under THIS name the establishing entry (journal seq %d) records %q",
					l.what, l.meta, l.ev.Seq, l.ev.Principal))
			}
		}
	}
	// The store's own declaration, rendered in its own right and never folded into
	// the verified findings above (SPEC §8.6.5). The wording tracks what is actually
	// present: calling an UNSIGNED entry's field attested would be the overclaim this
	// member exists to prevent, one layer further in.
	if av := p.Provenance.AppliedVia; av != "" {
		// Two things this must NOT say, both of which an earlier draft did. Not
		// "signed": §8.4 excludes the member from the entry signature, so it is
		// unsigned even on a signed entry. Not "chain-sealed" as an established
		// fact: this command does not run VerifyLog, so the seal is a property of
		// the format here, not a verification that has happened.
		out = append(out, "registry-RECORDED: the store declares this name was bound via "+av+
			" — its own statement about how the write happened. No signature covers it (§8.4 excludes it), "+
			"it cannot be reconstructed from the artifact, and it is NOT verified here; `oath audit` checks the chain that seals it")
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
	// PREFIX AUTHORITY, and specifically the case a consumer would otherwise have
	// to work out for themselves: this name is governed by a namespace whose holder
	// is NOT its owner. That is legitimate — the name predates the reservation and
	// was retained by its owner — but "who controls this" then has two answers
	// depending on which question is being asked, and only saying one is misleading.
	if p.Provenance.NamespaceHolder != "" && p.Provenance.Owner != "" &&
		p.Provenance.NamespaceHolder != p.Provenance.Owner {
		out = append(out, fmt.Sprintf("this name sits under namespace %s, governed by key %s…, but the NAME itself is owned by %s — the namespace holder may not repoint it, and the owner may not create other names in that namespace",
			p.Provenance.Namespace, shortHash(p.Provenance.NamespaceHolder), shortHash(p.Provenance.Owner)))
	}
	// The authority CHAIN, rendered so the two roles cannot be confused. Anyone
	// reading a delegate as the namespace owner would conclude that revoking it
	// changes who governs the prefix, which is exactly backwards.
	if len(p.Provenance.NamespaceDelegates) > 0 {
		out = append(out, fmt.Sprintf("namespace %s is HELD by key %s…; publication under it is also DELEGATED to %d key(s) (%s) — a delegate may bind names here and may not reserve, delegate onward, or revoke, and the holder may withdraw them at any time",
			p.Provenance.Namespace, shortHash(p.Provenance.NamespaceHolder),
			len(p.Provenance.NamespaceDelegates), shortHash(p.Provenance.NamespaceDelegates[0])))
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
	fmt.Fprintf(&b, "            spec lineage: %s\n            body lineage: %s\n",
		lineageLine(pkg.Provenance.SpecLineage), lineageLine(pkg.Provenance.BodyLineage))
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
		return "historical but NOT cryptographic: a principal string the registry recorded on an unsigned " +
			"entry, protected by operator policy rather than by a key. This name is in the CLOSED legacy set " +
			"— it is preserved as it is, no ownership claim is upgraded, and no new name can be created this way"
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
	// SPEC §12.3 LICENSE-ASSERTED-BY-PUBLICATION. A licence is asserted by a
	// PUBLICATION, not by a name transition, so any ACCEPTED publication carries
	// its author's terms — including one whose transition is `unchanged`.
	//
	// This was scoped to `applied`, which silently discarded the assertion on a
	// re-publication of identical content. That is exactly how relicensing works:
	// the code does not change, the terms do. Found by the first real signed
	// publication, after fixtures had reported 22/22 obligations witnessed — no
	// vector had an `unchanged` transition carrying an envelope, because vectors
	// supply assertions directly and never go through a transition at all.
	lic := ""
	for _, t := range nameTransitions(st.ReadLog(), name) {
		if t.Transition == transitionNone || t.Entry.EnvelopeB64 == "" {
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

// bindingAppliedVia finds the store's declared application mechanism for the
// write that actually bound this name to this hash.
//
// It selects the entry by the DERIVED transition (§8.6.4
// ENV-VERIFY-DERIVED-TRANSITION), not by the stored `name_transition` member.
// That distinction is the whole correctness of this function: the member is
// absent on every entry predating it and on paths that never set it, so testing
// it directly treats "the entry did not say" as "this entry may have bound the
// name" — and a later `prove` write, which moves no name, then lends its
// mechanism to a binding it had nothing to do with. Both entries are honest
// about themselves, so the misattribution is invisible downstream.
//
// Deriving requires knowing what the name was bound to immediately before each
// entry, so this walks FORWARD maintaining that state rather than backward from
// the end. Returns "" when the journal does not say, which is the common case
// for anything published before the member existed and MUST NOT be rendered as
// a mechanism.
func bindingAppliedVia(st *Store, name, hash string) string {
	bound, found := "", ""
	for _, e := range st.ReadLog() {
		if e.Name != name {
			continue
		}
		if deriveTransition(&e, bound) != transitionApplied {
			continue
		}
		bound = e.Hash
		if e.Hash == hash {
			found = e.AppliedVia // keep the LAST binding to this hash
		}
	}
	return found
}

// lineageWhere renders an UNKNOWN lineage's reason with its establishing entry
// when one was identified.
func lineageWhere(ev lineageTier) string {
	if ev.Seq > 0 {
		return fmt.Sprintf("%s; journal seq %d", ev.Reason, ev.Seq)
	}
	return ev.Reason
}

// lineageLine is the text rendering of one lineage's evidence.
func lineageLine(ev lineageTier) string {
	who := ""
	if ev.Principal != "" {
		who = fmt.Sprintf(", recorded principal %q", ev.Principal)
	}
	if ev.Tier == lineageKeySigned {
		return fmt.Sprintf("%s by key %s… (journal seq %d%s)", lineageKeySigned, shortHash(ev.Pubkey), ev.Seq, who)
	}
	return fmt.Sprintf("%s (%s%s)", ev.Tier, lineageWhere(ev), who)
}
