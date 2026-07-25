package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The repoint policy (#3): content addressing makes STORAGE unconditional —
// any well-typed object may enter the store, addressable by hash forever.
// What policy governs is the only mutable thing in the system: which object
// a NAME points at. A submission that fails policy is stored, verified, and
// journaled, but the name does not move; the previous version stays live.
//
// Policy lives in <store>/policy.json, absent by default. On a local stdio
// store it is advisory discipline; on the hosted store — where principals
// are authenticated rather than self-reported — it becomes enforcement.
//
// Authorship attribution (the substance of separation): when a name is
// repointed, the new object's spec/body authorship derives from a diff
// against the PREVIOUS object under that name. Unchanged props inherit the
// spec author; unchanged body inherits the body author; changes assign the
// submitting principal. A brand-new name assigns both to the submitter.

type PolicyRule struct {
	Names                       []string `json:"names"` // exact names; "*" matches all
	RequireAuthorshipSeparation bool     `json:"require_authorship_separation,omitempty"`
	RequireTotal                bool     `json:"require_total,omitempty"`
	ForbidFalsified             bool     `json:"forbid_falsified,omitempty"`
	MinMutationScore            float64  `json:"min_mutation_score,omitempty"` // 0..1; runs the mutation engine if the object is unscored
	RequireProven               bool     `json:"require_proven,omitempty"`     // name only binds once EVERY property is SMT-proven (#14)
}

type Policy struct {
	Rules []PolicyRule `json:"rules"`
}

func LoadPolicy(root string) (*Policy, error) {
	b, err := os.ReadFile(filepath.Join(root, "policy.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("corrupt policy.json: %w", err)
	}
	return &p, nil
}

func (p *Policy) ruleFor(name string) *PolicyRule {
	if p == nil {
		return nil
	}
	for i := range p.Rules {
		for _, n := range p.Rules[i].Names {
			if n == name || n == "*" {
				return &p.Rules[i]
			}
		}
	}
	return nil
}

func jsonEq(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// attributeAuthorship computes the spec/body lineage for repointing `name`
// to a new def submitted by `submitter`, diffing against the object the
// name currently points at.
func attributeAuthorship(st *Store, name string, newDef *Def, submitter string) (specAuthor, bodyAuthor string) {
	specAuthor, bodyAuthor = submitter, submitter
	prevH, ok := st.Resolve(name)
	if !ok {
		return
	}
	prevDef, err := st.GetDef(prevH)
	if err != nil {
		return
	}
	prevMeta, err := st.GetMeta(prevH)
	if err != nil {
		return
	}
	inherit := func(field, fallback string) string {
		if field != "" {
			return field
		}
		if fallback != "" {
			return fallback
		}
		return "unattributed"
	}
	if jsonEq(newDef.Props, prevDef.Props) {
		specAuthor = inherit(prevMeta.SpecAuthor, prevMeta.Author)
	}
	if jsonEq(newDef.Body, prevDef.Body) && jsonEq(newDef.Ctors, prevDef.Ctors) {
		bodyAuthor = inherit(prevMeta.BodyAuthor, prevMeta.Author)
	}
	return
}

// evalPolicy decides whether `name` may be repointed to hash h. Returns
// ok=false with a human-readable reason on refusal. May run the mutation
// engine (and record its score) when a rule demands a score the object
// does not yet carry.
func evalPolicy(st *Store, pol *Policy, name, h string, def *Def, specAuthor, bodyAuthor string) (bool, string) {
	rule := pol.ruleFor(name)
	if rule == nil {
		return true, ""
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return false, "policy: metadata unavailable: " + err.Error()
	}
	if rule.ForbidFalsified && m.Guarantee.Level == "falsified" {
		return false, "policy: falsified definitions may not hold this name"
	}
	if rule.RequireTotal && def.K == "func" && !isTotal(m.Termination) {
		return false, "policy: this name requires proven termination (got " + orWord(m.Termination, "unknown") + ")"
	}
	if rule.RequireAuthorshipSeparation && specAuthor == bodyAuthor {
		return false, fmt.Sprintf("policy: this name requires spec/body authorship separation (both would be %s); have a different principal author the specs or the body", specAuthor)
	}
	if rule.MinMutationScore > 0 && def.K == "func" && len(def.Props) > 0 {
		if m.MutantsTotal == 0 {
			// Score on demand: the object is verified but unscored.
			if _, err := apiMutateHash(st, h); err != nil {
				return false, "policy: mutation scoring failed: " + err.Error()
			}
			m, _ = st.GetMeta(h)
		}
		if m.MutantsTotal > 0 {
			effective := m.MutantsKilled + len(m.WaivedMutants)
			score := float64(effective) / float64(m.MutantsTotal)
			if score < rule.MinMutationScore {
				return false, fmt.Sprintf("policy: spec strength %d/%d (%.2f incl. %d waived) below required %.2f",
					effective, m.MutantsTotal, score, len(m.WaivedMutants), rule.MinMutationScore)
			}
		}
	}
	return true, ""
}

// isFullyProven reports whether every property of def carries an SMT proof. The
// prover sets Guarantee.Level to "proven" iff all properties proved (prove.go),
// but we also confirm the proof set is complete as defense in depth.
func isFullyProven(m *Meta, def *Def) bool {
	return m.Guarantee.Level == "proven" && len(m.ProvenProps) == len(def.Props) && len(def.Props) > 0
}

// provenGate is the asynchronous half of the repoint decision (#14). Because
// proving is too heavy to run inside a put, a require_proven name cannot be
// decided synchronously: the object is stored and its verdicts (tested,
// termination, mutation) are known, but the PROOF is not. This returns one of:
//
//	"pass"    — no proof gate, or the object is already fully proven: bind now.
//	"pending" — proof required and not yet earned, but attainable: defer the
//	            bind, enqueue the object, let a worker prove and bind it later.
//	"blocked" — proof required but unattainable: a falsified def can never prove,
//	            and a def that swears no properties has nothing to prove.
//
// It runs AFTER evalPolicy's synchronous checks pass, so a pending job is only
// ever enqueued for an object that already clears forbid_falsified /
// require_total / min_mutation_score — no point proving something a synchronous
// rule already rejects.
func provenGate(pol *Policy, name string, m *Meta, def *Def) (state, reason string) {
	rule := pol.ruleFor(name)
	if rule == nil || !rule.RequireProven || def.K != "func" {
		return "pass", ""
	}
	if isFullyProven(m, def) {
		return "pass", ""
	}
	if m.Guarantee.Level == "falsified" {
		return "blocked", "policy: this name requires proven properties; the definition is falsified and can never be proven"
	}
	if len(def.Props) == 0 {
		return "blocked", "policy: this name requires proven properties; the definition swears none"
	}
	return "pending", "policy: this name requires proven properties; queued for the verification worker (name unchanged until proof lands)"
}

func orWord(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
