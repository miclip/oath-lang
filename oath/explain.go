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
}

type explainProv struct {
	Author     string `json:"author,omitempty"`
	SpecAuthor string `json:"spec_author,omitempty"`
	BodyAuthor string `json:"body_author,omitempty"`
	Separated  bool   `json:"authorship_separated"`
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
			// self-assessment, and a consumer should be able to see which it is.
			Separated: m.SpecAuthor != "" && m.BodyAuthor != "" && m.SpecAuthor != m.BodyAuthor,
		},
	}

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
		pkg.SpecStrength = &specStrength{
			Killed: killed, Total: m.MutantsTotal,
			Score: float64(killed) / float64(m.MutantsTotal),
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

	pkg.Limitations = explainLimitations(pkg, m)
	return pkg, nil
}

// explainLimitations is the part that matters most: the honest reasons NOT to
// pick this artifact. Everything here is derived from recorded state, so a
// definition cannot look better than its evidence.
func explainLimitations(p *explainPkg, m *Meta) []string {
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
	case p.SpecStrength.Score < 0.5:
		out = append(out, fmt.Sprintf("spec strength is LOW (%d/%d mutants caught): the properties pass but constrain little, so passing them is weak evidence",
			p.SpecStrength.Killed, p.SpecStrength.Total))
	}
	if len(m.WaivedMutants) > 0 {
		out = append(out, fmt.Sprintf("%d surviving mutant(s) WAIVED as equivalent — judgement calls, listed with their justifications", len(m.WaivedMutants)))
	}
	if !p.Provenance.Separated {
		out = append(out, "spec and body share an author — no authorship separation, so the specification was not written independently of the code")
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
	fmt.Fprintf(&b, "\nPROVENANCE: author=%s spec=%s body=%s (separated: %v)\n",
		orNone(pkg.Provenance.Author), orNone(pkg.Provenance.SpecAuthor),
		orNone(pkg.Provenance.BodyAuthor), pkg.Provenance.Separated)
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
