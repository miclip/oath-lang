package main

// The LEGACY-UNOWNED set: names that exist, are historically valid, and are owned
// by nobody in any sense a verifier can check.
//
// 179 of the original bare corpus names were first bound by an UNSIGNED entry
// under the label `admin`. Exact-name ownership is inferred from a name's FIRST
// binding, so what those names have is a label the registry recorded about
// itself — an OBSERVATION, authoritative about the observer and checkable by no
// one. What protects them operationally is policy.json, which is operator
// configuration and precisely what namespace reservation moved authority away
// from.
//
// FREEZE, DO NOT REWRITE. Retro-assigning a key to a name first bound by a label
// would have the registry assert a signature that was never made — manufacturing
// evidence so a checker passes, which is the single thing this system exists not
// to do. A later signed republication proves authorship OF THAT PUBLICATION, not
// ownership of the original name. So the set stays exactly what it is:
//
//   - historically valid registry entries
//   - owned only by an unverifiable label
//   - protected operationally by policy.json
//   - superseded for real use by the owned michael/* mirrors
//
// WHY A PINNED BOUNDARY RATHER THAN A QUERY. "Every name that is currently
// unowned" is not a frozen set — it is a description that grows every time
// something unowned is admitted, and a category that expands while calling itself
// frozen is worse than no category at all. The boundary is a fixed point in an
// append-only journal, so membership is DERIVED (never stored, per DESIGN.md's
// rule for derived facts) from immutable history and cannot grow: entries after
// the boundary are not eligible, whatever they look like.
//
// The set is therefore closed by construction, not by discipline.

// legacyUnownedBoundary is the journal sequence at which the set was frozen:
// 2026-08-01, at 1266 entries, immediately after the delegation_rev migration.
//
// PINNED, VERSIONED, AND LOAD-BEARING. Raising it would admit names into the
// legacy category that were created after the freeze — which is exactly the
// silent expansion the boundary exists to prevent. It may be lowered only if the
// journal were shown to be wrong before it, which cannot happen: the chain is
// tamper-evident.
const legacyUnownedBoundary = 1266

// legacyUnowned derives the frozen set: names whose FIRST accepted binding is at
// or before the boundary AND carried no signature.
//
// Both conditions matter. The sequence bound closes the set; the signature test is
// what makes membership meaningful, since a name first bound by a signed
// publication has a real cryptographic owner and belongs to nothing here.
func legacyUnowned(st *Store) map[string]bool {
	return legacyUnownedAt(st, legacyUnownedBoundary)
}

// legacyUnownedAt is the derivation with the boundary given explicitly. Production
// callers pass the pinned constant and nothing else; it is parameterised so the
// BOUNDARY BEHAVIOUR ITSELF can be tested — that a name first bound after the
// freeze is excluded is the property the whole design rests on, and a test that
// cannot move the boundary cannot check it.
func legacyUnownedAt(st *Store, boundary int) map[string]bool {
	out := map[string]bool{}
	seen := map[string]bool{}
	for _, e := range st.ReadLog() {
		if e.Name == "" || seen[e.Name] {
			continue
		}
		if e.Status != "" && e.Status != "accepted" {
			continue
		}
		seen[e.Name] = true
		if e.Seq > boundary {
			continue
		}
		if e.AuthorPubkey == "" {
			out[e.Name] = true
		}
	}
	return out
}

// isLegacyUnowned reports whether a name is in the frozen set.
func isLegacyUnowned(st *Store, name string) bool {
	return legacyUnowned(st)[name]
}

// nameExists reports whether a name is already bound — the difference between
// CREATING authority state and updating it, which is the line the freeze draws.
func nameExists(st *Store, name string) bool {
	_, ok := st.Names()[name]
	return ok
}

// bearerRefusal is the message a token-only client gets when it tries to create a
// name. It states the boundary and the repair path, because a generic refusal
// leaves an agent with no way forward and this one has an obvious one.
const bearerRefusal = "new names require a signed principal. Bearer authorization grants SERVICE ACCESS, not NAME OWNERSHIP.\n" +
	"  A token authorizes use of this registry; a key establishes who you are and what you may govern.\n" +
	"  To publish %q: generate a key (`oath keygen`) and publish with `--key`, or use a key delegated\n" +
	"  to you under a reserved namespace (`oath delegate`). Everything short of creating a name — search,\n" +
	"  evaluate, prove, and preparing the publication itself — still works with the token alone."

// sourceNames reports the names a put source would bind, WITHOUT elaborating it.
//
// The freeze must be decided before anything is stored, and elaboration is where
// storage begins — content addressing writes the object unconditionally past the
// type gate. Reading the top-level form heads is enough: a name is what a
// `(data <name> …)` or `(defn <name> …)` binds, and a source whose forms cannot
// be read at all is left to the real parser to reject with a better message.
func sourceNames(src string) []string {
	forms, err := parseForms(src)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range forms {
		if f.K != "list" || len(f.Kids) < 2 || f.Kids[0].K != "sym" || f.Kids[1].K != "sym" {
			continue
		}
		if f.Kids[0].Sym == "data" || f.Kids[0].Sym == "defn" {
			out = append(out, f.Kids[1].Sym)
		}
	}
	return out
}
