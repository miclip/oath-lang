package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Names selects which definitions this rule governs. Three pattern forms:
	// an exact name, a namespace prefix "michael/*" (matching names UNDER
	// michael/), or "*" for all. The MOST SPECIFIC matching pattern wins,
	// independent of the order rules appear in the file (see ruleFor).
	Names                       []string `json:"names"`
	RequireAuthorshipSeparation bool     `json:"require_authorship_separation,omitempty"`
	RequireTotal                bool     `json:"require_total,omitempty"`
	ForbidFalsified             bool     `json:"forbid_falsified,omitempty"`
	MinMutationScore            float64  `json:"min_mutation_score,omitempty"` // 0..1; runs the mutation engine if the object is unscored
	RequireProven               bool     `json:"require_proven,omitempty"`     // name only binds once EVERY property is SMT-proven (#14)
	OwnerPubkey                 string   `json:"owner_pubkey,omitempty"`       // hex Ed25519 key; only this principal may repoint the name (#14)
	// TrustOnFirstPublish derives ownership from the journal when OwnerPubkey is
	// unset: the first principal to publish a name owns it, and only that principal
	// may repoint it (#84). OPT-IN, and deliberately so — switching it on is a
	// migration, not a default. A store whose existing names were published under a
	// principal that can no longer authenticate would find every repoint blocked,
	// so the operator must face adoption explicitly rather than discover it.
	TrustOnFirstPublish bool `json:"trust_on_first_publish,omitempty"`
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
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("invalid policy.json: %w", err)
	}
	return &p, nil
}

// validate rejects a policy that cannot be resolved unambiguously, rather than
// letting ruleFor pick a winner. An ambiguous policy that silently resolves is the
// worst outcome: it enforces SOMETHING, the operator believes it enforces what
// they wrote, and nothing surfaces the difference.
//
// WHY DUPLICATE PATTERNS ARE THE ONLY AMBIGUITY. For a given name, the patterns
// that can match are: the exact name (unique, score len+1); prefixes ending at a
// segment boundary (necessarily nested, so all of different length and therefore
// different score); and "*" (score 0). Distinct patterns matching one name
// therefore always have distinct specificity — so a tie is possible ONLY when the
// same pattern appears in two rules. Detecting that statically at load is
// equivalent to a runtime conflict check, and it fails at configuration time
// instead of on some later publication.
//
// The one exception is the degenerate prefix "/*", whose empty prefix scores 0 and
// would tie with "*" for names beginning with "/". It is rejected outright: a
// namespace with an empty name is not a thing anyone means.
func (p *Policy) validate() error {
	if p == nil {
		return nil
	}
	seen := map[string]int{}
	for i := range p.Rules {
		if len(p.Rules[i].Names) == 0 {
			return fmt.Errorf("rule %d matches nothing: `names` is empty", i)
		}
		for _, n := range p.Rules[i].Names {
			if n == "" {
				return fmt.Errorf("rule %d contains an empty pattern", i)
			}
			if n == "/*" {
				return fmt.Errorf("rule %d uses the degenerate pattern \"/*\": an empty namespace prefix is ambiguous with \"*\"", i)
			}
			if strings.HasSuffix(n, "/*") && strings.Contains(strings.TrimSuffix(n, "/*"), "*") {
				return fmt.Errorf("rule %d pattern %q: \"*\" is only meaningful as a whole pattern or as a trailing \"/*\"", i, n)
			}
			if n != "*" && !strings.HasSuffix(n, "/*") && strings.Contains(n, "*") {
				return fmt.Errorf("rule %d pattern %q: \"*\" is only meaningful as a whole pattern or as a trailing \"/*\" — it is not a glob", i, n)
			}
			if prior, dup := seen[n]; dup {
				return fmt.Errorf("pattern %q appears in both rule %d and rule %d: the scope is governed twice and which rule wins would depend on file order", n, prior, i)
			}
			seen[n] = i
		}
	}
	return nil
}

// patternSpecificity scores how narrowly a policy pattern selects names. Higher
// wins. Returns -1 when the pattern does not match at all.
//
//	exact name        len(name)+1   most specific: names precisely one thing
//	"prefix/*"        len(prefix)   longer prefixes beat shorter ones
//	"*"               0             least specific: the catch-all
//
// A prefix pattern "michael/*" matches names UNDER michael/, and deliberately not
// the bare name "michael". A namespace and a definition that happens to share its
// first segment are different things, and silently folding them together would let
// a prefix claim capture a name its owner never reasoned about. Owning the bare
// name too is expressible by listing it.
func patternSpecificity(pattern, name string) int {
	if pattern == name {
		return len(name) + 1
	}
	if pattern == "*" {
		return 0
	}
	if prefix, ok := strings.CutSuffix(pattern, "/*"); ok {
		if strings.HasPrefix(name, prefix+"/") {
			return len(prefix)
		}
	}
	return -1
}

// ruleFor returns the policy rule governing `name`: the MOST SPECIFIC match, not
// the first one in the file.
//
// It used to return the first rule listing the name or "*", which made behaviour
// depend on document order — a rule with names ["*"] placed above a specific rule
// silently shadowed it, so a policy could be correct as written and wrong as
// ordered. That is an invisible failure: the shadowed rule looks present.
// Specificity ordering makes the file's meaning independent of its layout.
//
// Ties cannot occur in a VALIDATED policy: distinct patterns matching one name
// always have distinct specificity, so the only possible tie is a duplicated
// pattern, which Policy.validate rejects at load (see its comment for the
// argument). This function therefore never has to break a tie, and must not start
// doing so silently — if a tie ever appears, the policy skipped validation.
func (p *Policy) ruleFor(name string) *PolicyRule {
	if p == nil {
		return nil
	}
	best, bestScore := -1, -1
	for i := range p.Rules {
		for _, n := range p.Rules[i].Names {
			if sc := patternSpecificity(n, name); sc > bestScore {
				best, bestScore = i, sc
			}
		}
	}
	if best < 0 {
		return nil
	}
	return &p.Rules[best]
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
	// NAMESPACE AUTHORITY (#66) is checked FIRST, and deliberately above the
	// no-rule early return below.
	//
	// A reservation is journal-derived authority, so it must bind whether or not
	// an operator has written a policy rule covering the prefix. Checking it after
	// `rule == nil` would make a reserved namespace unenforced in exactly the case
	// the whole operation exists for — a developer who reserved a namespace on a
	// registry whose operator never edited anything on their behalf.
	if res, ok := governingReservation(st, name); ok {
		m, err := st.GetMeta(h)
		if err != nil {
			return false, "policy: metadata unavailable: " + err.Error()
		}
		// MOST-SPECIFIC WINS. An exact-name owner beneath the prefix is more
		// specific than the prefix, so they keep their name — the retention promised
		// by RES-NO-CAPTURE, enforced here rather than merely reported at
		// reservation time. Falling through leaves them to the ordinary owner rules
		// below; the reservation neither grants nor removes anything for that name.
		owner, _ := nameOwner(st, name)
		if owner == "" || owner == res.Pubkey {
			if m.Author != res.Pubkey {
				return false, fmt.Sprintf("policy: %q lies under namespace %q, reserved to key %s… by a signed authority record; submitter %q may not bind names there",
					name, res.Namespace, shortHash(res.Pubkey), m.Author)
			}
		}
	}
	rule := pol.ruleFor(name)
	if rule == nil {
		return true, ""
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return false, "policy: metadata unavailable: " + err.Error()
	}
	// Scope-owned-by-key (#14): a name reserved to a pubkey may only be repointed
	// by that principal. Authorship is the authenticated key (signature auth sets
	// the author to the caller's pubkey), so an impostor with a different key —
	// or a bearer principal — cannot move the name. Unforgeable ownership without
	// the server being a trust root.
	// Trust on first publish (#84), consulted only when no explicit owner is
	// configured: an explicit OwnerPubkey is an operator decision and outranks
	// anything derived from history.
	if rule.OwnerPubkey == "" && rule.TrustOnFirstPublish {
		if owner, source := nameOwner(st, name); owner != "" && owner != m.Author {
			if ownerIsCryptographic(source) {
				return false, fmt.Sprintf("policy: %q is owned by key %s… (first publisher, signed); submitter %q may not repoint it",
					name, shortHash(owner), m.Author)
			}
			// The owner is a LABEL, so say so. Someone debugging this needs to know
			// they are being refused by an unverifiable claim, not by a signature.
			return false, fmt.Sprintf("policy: %q is owned by %q (first publisher) and submitter is %q. NOTE: that ownership rests on an UNSIGNED journal entry, so the owner is a recorded label rather than a verified key — it is enforced, but it is not cryptographic ownership",
				name, owner, m.Author)
		}
	}
	if rule.OwnerPubkey != "" && m.Author != rule.OwnerPubkey {
		return false, fmt.Sprintf("policy: name is owned by key %s…; submitter %q may not repoint it", shortHash(rule.OwnerPubkey), m.Author)
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

// nameOwner derives who owns `name` from the journal: the principal on the FIRST
// entry that applied a transition to it (trust on first publish).
//
// Derived rather than stored, so there is no ownership table to drift from the
// history it is supposed to summarise, and any party holding the journal computes
// the same answer. Returns ("", false) for a name that has never been published.
//
// The second return is the part that must not be dropped: whether that first
// publication carried an author ENVELOPE, i.e. whether the owner is a KEY or a
// bare LABEL. `claude-main` is a string the registry wrote down; a pubkey with an
// envelope behind it is a fact a third party can check. Enforcing owner-only
// repoint against a label still has value (it stops a DIFFERENT authenticated
// principal moving the name) but it is not cryptographic ownership, and reporting
// it as though it were would be the authorship-boolean mistake again.
// Ownership SOURCES. Strength alone (key vs label) is not enough: an operator can
// edit policy.json at any moment, so a rule in a current config file must never be
// presentable as historical cryptographic evidence. Where the authority came from
// is as decision-relevant as how strong it is.
const (
	// ownerNone: the name has never had a transition applied.
	ownerNone = ""
	// ownerSignedFirstPublish: derived from a signed first publication. Historical
	// and cryptographic — a third party can re-verify it from the journal alone.
	ownerSignedFirstPublish = "signed-first-publication"
	// ownerLegacyLabel: derived from an UNSIGNED first publication. Historical but
	// not cryptographic: the principal is a string the registry recorded.
	ownerLegacyLabel = "legacy-label"
	// ownerConfiguredPolicy: an explicit owner_pubkey in the CURRENT policy file.
	// Strong (it names a key) but not historical — it reflects present
	// configuration, is editable by whoever holds the store, and says nothing about
	// who published anything.
	ownerConfiguredPolicy = "registry-configured-policy"
	// ownerSignedAdoption: authority adopted by an explicit signed operation.
	// Reserved; adoption is not implemented yet, so nothing emits this.
	ownerSignedAdoption = "signed-adoption"
)

// nameOwner derives who owns `name`, and from WHERE.
//
// TOFU establishes ownership of an EXACT NAME only. Publishing
// michael/service1/foo does not make anyone the owner of michael/service1/*, or
// publishing one child would silently capture a whole namespace. Prefix authority
// comes only from an explicit rule, reservation, or adoption — never from
// inference (#84).
//
// A legacy label owner is reported as such and MUST NOT be promoted to enforceable
// key ownership by inference. Signed adoption, when it exists, changes authority
// PROSPECTIVELY; it will not rewrite the authorship of earlier entries.
//
// CAVEAT under the filesystem store: two concurrent FIRST publications can race to
// become the TOFU owner, because establishing the first owner is not atomic. This
// prevents ordinary unauthorized repoints immediately; atomic first-owner
// establishment is pending the transactional store.
// signedPublicationBy reports whether the journal records a publication of `name`
// signed by `pubkey`. This is the CORROBORATION half of adoption: policy naming a
// key is a present-tense operator statement, and this is the historical evidence
// that the named key actually acted.
func signedPublicationBy(st *Store, name, pubkey string) bool {
	if pubkey == "" {
		return false
	}
	for _, e := range st.ReadLog() {
		if e.Name != name {
			continue
		}
		// Any ACCEPTED publication corroborates, not only one that MOVED the name.
		// This deliberately does NOT use repointedName(), which is `applied`-only:
		// re-publishing identical content signs an `unchanged` transition, and that
		// is exactly what signing an existing corpus produces — so an
		// applied-only test would make adoption impossible for the very campaign
		// that creates the evidence.
		//
		// The question corroboration asks is "did this key publish this name?", not
		// "did this key change what the name points at". Third instance of the same
		// mistake in this codebase (see §8.6.4 clause 5 and
		// LICENSE-ASSERTED-BY-PUBLICATION): an `unchanged` transition is still a
		// publication.
		if e.nameTransitionOf() == transitionNone {
			continue
		}
		if e.AuthorPubkey == pubkey && e.EnvelopeB64 != "" && e.AuthorSig != "" {
			return true
		}
	}
	return false
}

// nameOwnerUnderPolicy resolves ownership with policy precedence applied, and is
// the single place the ADOPTION upgrade happens.
//
// A configured OwnerPubkey outranks history — it is an operator decision — but on
// its own it is present-tense CONFIGURATION and a third party cannot re-derive it
// from the journal. When the journal ALSO shows that key signing a publication of
// the name, the claim stops being merely configured: it is corroborated by
// evidence, which is ownerSignedAdoption ("authority adopted by a signed operation
// at a recorded point, not retroactive").
//
// WHY ADOPTION IS NOT AUTOMATIC. Letting the first signed publisher of a
// legacy-label name simply become its cryptographic owner was the obvious
// implementation and is a LAND GRAB: trust-on-first-publish is off by default, so
// any key could sign over any legacy name and would then own it the moment an
// operator enabled enforcement. Requiring an operator statement first means
// adoption grants no authority that was not already declared — it only converts a
// declaration into checkable history.
func nameOwnerUnderPolicy(st *Store, pol *Policy, name string) (owner, source string) {
	derived, derivedSrc := nameOwner(st, name)
	if pol == nil {
		return derived, derivedSrc
	}
	rule := pol.ruleFor(name)
	if rule == nil || rule.OwnerPubkey == "" {
		return derived, derivedSrc
	}
	if signedPublicationBy(st, name, rule.OwnerPubkey) {
		return rule.OwnerPubkey, ownerSignedAdoption
	}
	return rule.OwnerPubkey, ownerConfiguredPolicy
}

func nameOwner(st *Store, name string) (owner, source string) {
	for _, e := range st.ReadLog() {
		// repointedName, not Status: a FALSIFIED but applied first publication does
		// establish the name; rejected, blocked and pending do not, or a failed
		// submission would squat a name for free.
		if e.Name != name || !e.repointedName() {
			continue
		}
		if e.EnvelopeB64 != "" && e.AuthorPubkey != "" && e.AuthorSig != "" {
			return e.AuthorPubkey, ownerSignedFirstPublish
		}
		if e.Author != "" {
			return e.Author, ownerLegacyLabel
		}
		return "unattributed", ownerLegacyLabel
	}
	return "", ownerNone
}

// ownerIsCryptographic reports whether a source constitutes evidence a third party
// can re-derive from the journal. Configured policy is deliberately excluded: it
// names a key, but it is a present-tense operator statement, not history.
func ownerIsCryptographic(source string) bool {
	return source == ownerSignedFirstPublish || source == ownerSignedAdoption
}
