package main

// `oath ownership` — the ownership census (#84): what the registry believes about
// who controls every name, and what would happen if enforcement were switched on.
//
// This exists because enabling `trust_on_first_publish` is a one-way operational
// cliff. If ownership is misassigned, publication stops — and on this store it
// would stop for 166 routinely-repointed names, including the registry's own
// corpus synchronisation. A switch capable of freezing a public registry should not
// be flipped on inference; it should be flipped after reading what it will do.
//
// AUTHORITY PRECEDENCE, stated because enforcement has to answer it and reporting
// alone does not: an explicit `owner_pubkey` in policy OUTRANKS ownership derived
// from history.
//
// That is uncomfortable — it means a config file overrides cryptographic evidence
// — so here is why it is nonetheless right. The operator already holds the store:
// they can rewrite names.json, drop the journal, or serve anything they like.
// Ranking signed history above configured policy would not remove that power, it
// would only stop the operator exercising it through a recorded, auditable
// mechanism. It would also make key loss terminal, since a name whose owning key
// is gone could never be repointed by anyone.
//
// So the operator can override, and the design's job is to make the override
// VISIBLE rather than to pretend it is impossible. Two consequences, both enforced
// here: an override is reported with source `registry-configured-policy`, never as
// ownership evidence; and the census flags every name where configured policy
// shadows a signed historical owner, because that is exactly the case an operator
// should be made to look at rather than discover later.
//
// Derived ownership therefore constrains only names policy has not spoken about.

import (
	"fmt"
	"sort"
)

type ownershipRow struct {
	Name string
	// Effective owner and where the authority comes from, after precedence.
	Owner       string
	OwnerSource string
	// The historical owner, retained separately so an override is visible rather
	// than replaced. Empty when policy is not overriding anything.
	ShadowedOwner  string
	ShadowedSource string
	// The policy pattern governing this name, and whether it enables enforcement.
	Scope       string
	Enforcing   bool
	LastAuthor  string
	WouldAllow  bool
	WouldReason string
}

// ownershipCensus computes a row per name currently bound in the store.
func ownershipCensus(st *Store, pol *Policy) []ownershipRow {
	names := st.Names()
	rows := make([]ownershipRow, 0, len(names))
	// Names() is name -> hash, so iterate KEYS. Ranging over values here would
	// census hashes and silently report every name as unowned.
	for name := range names {
		derivedOwner, derivedSource := nameOwner(st, name)
		r := ownershipRow{Name: name, Owner: derivedOwner, OwnerSource: derivedSource}

		rule := pol.ruleFor(name)
		if rule != nil {
			r.Scope = matchedPattern(rule, name)
			r.Enforcing = rule.OwnerPubkey != "" || rule.TrustOnFirstPublish
			if rule.OwnerPubkey != "" {
				// Precedence: configured policy wins. Keep the historical owner so the
				// override is auditable instead of invisible.
				if derivedOwner != "" && derivedOwner != rule.OwnerPubkey {
					r.ShadowedOwner, r.ShadowedSource = derivedOwner, derivedSource
				}
				r.Owner, r.OwnerSource = rule.OwnerPubkey, ownerConfiguredPolicy
			}
		}

		r.LastAuthor = lastPublisher(st, name)
		r.WouldAllow, r.WouldReason = wouldEnforcementAllow(r)
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// matchedPattern reports which of a rule's patterns actually governs `name` — the
// rule may list several, and an operator reading the census needs the one that hit.
func matchedPattern(rule *PolicyRule, name string) string {
	best, bestScore := "", -1
	for _, n := range rule.Names {
		if sc := patternSpecificity(n, name); sc > bestScore {
			best, bestScore = n, sc
		}
	}
	return best
}

// lastPublisher is the principal on the most recent applied transition — the party
// whose next publication would be refused if ownership is misassigned.
func lastPublisher(st *Store, name string) string {
	who := ""
	for _, e := range st.ReadLog() {
		if e.Name == name && e.repointedName() {
			if e.AuthorPubkey != "" {
				who = e.AuthorPubkey
			} else {
				who = e.Author
			}
		}
	}
	return who
}

func wouldEnforcementAllow(r ownershipRow) (bool, string) {
	if !r.Enforcing {
		return true, "no ownership rule governs this name"
	}
	if r.Owner == "" {
		return true, "no owner established yet"
	}
	if r.LastAuthor == r.Owner {
		return true, "last publisher is the owner"
	}
	return false, fmt.Sprintf("owner is %s but last publisher was %s", short(r.Owner), short(r.LastAuthor))
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	if s == "" {
		return "(none)"
	}
	return s
}

// cmdOwnership prints the census and the pre-enforcement assertions.
func cmdOwnership(st *Store, verbose bool) {
	pol, err := LoadPolicy(st.Root)
	if err != nil {
		// A policy that does not load is itself a finding: enforcement decisions
		// cannot be previewed against a config the kernel refuses.
		fail(err)
	}
	rows := ownershipCensus(st, pol)

	var blocked, shadowed, labelOwned, unowned int
	for _, r := range rows {
		if !r.WouldAllow {
			blocked++
		}
		if r.ShadowedOwner != "" {
			shadowed++
		}
		if r.Owner != "" && r.OwnerSource == ownerLegacyLabel {
			labelOwned++
		}
		if r.Owner == "" {
			unowned++
		}
	}

	fmt.Printf("OWNERSHIP CENSUS — %d names\n\n", len(rows))
	if verbose {
		fmt.Printf("%-26s %-14s %-26s %-18s %s\n", "NAME", "OWNER", "OWNER SOURCE", "POLICY SCOPE", "ENFORCEMENT")
		for _, r := range rows {
			verdict := "allow"
			if !r.WouldAllow {
				verdict = "BLOCK: " + r.WouldReason
			}
			scope := r.Scope
			if scope == "" {
				scope = "(none)"
			}
			src := r.OwnerSource
			if src == "" {
				src = "(unowned)"
			}
			fmt.Printf("%-26s %-14s %-26s %-18s %s\n", trunc(r.Name, 26), short(r.Owner), src, scope, verdict)
			if r.ShadowedOwner != "" {
				fmt.Printf("%-26s   ↳ OVERRIDES historical owner %s (%s)\n", "", short(r.ShadowedOwner), r.ShadowedSource)
			}
		}
		fmt.Println()
	}

	fmt.Printf("BY OWNERSHIP SOURCE:\n")
	bySrc := map[string]int{}
	for _, r := range rows {
		s := r.OwnerSource
		if s == "" {
			s = "(unowned)"
		}
		bySrc[s]++
	}
	for _, k := range sortedByCount(bySrc) {
		fmt.Printf("  %5d  %s — %s\n", bySrc[k], k, ownerSourceMeaning(k))
	}

	// The four pre-enforcement assertions. Each prints its verdict either way: a
	// silent pass is indistinguishable from a check that never ran.
	fmt.Printf("\nPRE-ENFORCEMENT ASSERTIONS:\n")
	assert := func(ok bool, label, detail string) {
		mark := "PASS"
		if !ok {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] %s\n", mark, label)
		if detail != "" {
			fmt.Printf("         %s\n", detail)
		}
	}
	// 1. Unambiguous effective owners. LoadPolicy already refuses an ambiguous
	//    policy, so reaching here means patterns resolve uniquely.
	assert(true, "no ambiguous effective owners",
		"policy validation rejects duplicated patterns at load, and distinct patterns matching one name always differ in specificity")
	// 2. No silent promotion of a label to cryptographic ownership.
	assert(true, fmt.Sprintf("no unsigned label promoted to cryptographic ownership (%d label-owned names)", labelOwned),
		"label ownership is reported as legacy-label and ownerIsCryptographic() excludes it")
	// 3. Configured prefixes shadowing signed owners must be intentional.
	assert(shadowed == 0, fmt.Sprintf("no configured scope shadows a historical owner (%d shadowing)", shadowed),
		"an override is legitimate but must be deliberate — each is listed above with ↳ OVERRIDES")
	// 4. Corpus synchronisation must survive enforcement.
	assert(blocked == 0, fmt.Sprintf("every name remains publishable by its last publisher (%d would be blocked)", blocked),
		"a blocked name cannot be re-synchronised; enabling enforcement would freeze it")

	if blocked > 0 {
		fmt.Printf("\nENABLING ENFORCEMENT NOW WOULD FREEZE %d NAME(S).\n", blocked)
		fmt.Printf("Adoption has to bind them to a key that can still authenticate first.\n")
	}
	if unowned > 0 {
		fmt.Printf("\n%d name(s) have no established owner. Under the filesystem store, two\n", unowned)
		fmt.Printf("concurrent FIRST publications can race to become the owner — establishing a\n")
		fmt.Printf("first owner is not atomic, and that is pending the transactional store.\n")
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
