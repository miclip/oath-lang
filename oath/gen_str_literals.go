package main

// Literal-weighted Str generation — SPEC §4's `Str` entry.
//
// Generated Str codepoints used to come from the generic Int arm alone, so the
// probability that a generated string contained the one delimiter a definition
// branches on was the probability of drawing one specific codepoint at one
// specific position — and for the range that arm draws from, exactly zero for
// every printable character. §4 now weights the head of an `SCons` toward the
// Int literals of the definition under test and its transitive dependency
// closure.
//
// THE SPEC IS THE AUTHORITY FOR THE RULE; THIS FILE IMPLEMENTS IT. Draw order,
// the 1-in-4 weight, the numeric sort, the reflexive transitive closure and the
// empty-set no-extra-draw path are all §4's, and `oathrs` implements the same
// text independently. Anything here that disagrees with §4 is a defect here.
//
// TWO DESIGN POINTS §4 STATES AND THIS FILE MUST NOT QUIETLY RELAX:
//
//   - Str is identified by its CANONICAL DECLARATION HASH, never by the name
//     "Str". A name is metadata and can be repointed, and a generator keyed on
//     one would produce different cases for identical canonical bytes.
//   - Literals are ℤ. They are sorted and deduplicated by NUMERIC VALUE with no
//     machine-word narrowing anywhere, because a definition mentioning a large
//     constant must contribute that constant and not a truncation of it.

import (
	"fmt"
	"math/big"
	"sort"
)

// canonicalStrHash is the Str datatype's identity, DERIVED rather than looked
// up: the canonical hash of `(data Str [] (SNil) (SCons Int Str))`.
//
// TestCanonicalStrHashMatchesTheCorpus pins it against the corpus binding, so
// the derivation cannot drift into naming a datatype nothing uses.
func canonicalStrHash() string {
	return hashDef(&Def{K: "data", TyVars: 0, Ctors: [][]Ty{
		{},                       // SNil
		{{K: "int"}, {K: "rec"}}, // SCons Int Str
	}})
}

// strWeights is the literal set a generation run draws from, together with the
// identity of the Str ADT it applies to. It is never constructed empty:
// strLiterals returns nil for an empty set, so "the closure has no literals"
// and "no weighting" are one state rather than two that can disagree — which is
// also what makes §4's "consume no extra draw" path unmissable at the call
// site.
type strWeights struct {
	strHash string     // Str's canonical declaration hash
	lits    []*big.Int // unique, ascending by numeric value, non-empty
}

// genStrCodepoint is §4's weighted head draw. The below(4) is taken FIRST and
// unconditionally, so the number of draws the choice costs does not depend on
// which branch it takes.
func genStrCodepoint(r *rng) Value {
	if r.below(4) == 0 {
		return Value{K: "int", Int: new(big.Int).Set(r.lits.lits[r.below(len(r.lits.lits))])}
	}
	return genInt(r)
}

// strLiterals is §4's literal set L(D) for the definition under test, or nil
// when that set is empty.
//
// THE CLOSURE IS §7.2'S, NAMED RATHER THAN REBUILT. §4 defines L(D) over "the
// transitive dependency closure §7.2 already defines" — everything a
// definition's body AND properties reference, at every step — so this walks
// `sortedDepHashes`, which is the same relation the hash already rests on.
// A second traversal written to §4's own words would be a duplicate free to
// drift from the one identity uses.
func strLiterals(st *Store, ownerHash string) (*strWeights, error) {
	owner, err := st.GetDef(ownerHash)
	if err != nil {
		return nil, err
	}
	return strLiteralsOf(st, ownerHash, owner)
}

// strLiteralsOf is strLiterals for a definition whose canonical bytes are in
// hand rather than in the store — §6.3 mutation scoring, where §4 makes the
// MUTANT the definition under test.
func strLiteralsOf(st *Store, ownerHash string, owner *Def) (*strWeights, error) {
	seen := map[string]*big.Int{}
	collectDefLiterals(owner, seen)

	// The reference graph is acyclic by construction — a definition cannot name
	// a hash that does not exist yet — but the visited set bounds the walk on a
	// malformed store and keeps a diamond linear rather than exponential.
	visited := map[string]bool{ownerHash: true}
	queue := sortedDepHashes(owner)
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if visited[h] {
			continue
		}
		visited[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			// AN UNREADABLE DEPENDENCY IS FATAL, not skipped. Dropping one
			// SHRINKS the set, and a smaller set raises every survivor's share
			// of the 1-in-4 arm — so a partial read is a DIFFERENT
			// distribution, not a smaller one, and silently returning it would
			// let two kernels generate different cases from the same bytes.
			return nil, fmt.Errorf("literal set for %s: dependency %s is unreadable (%w); "+
				"an incomplete closure is a DIFFERENT distribution, not a smaller one",
				shortHash(ownerHash), shortHash(h), err)
		}
		collectDefLiterals(d, seen)
		queue = append(queue, sortedDepHashes(d)...)
	}
	if len(seen) == 0 {
		return nil, nil
	}
	lits := make([]*big.Int, 0, len(seen))
	for _, v := range seen {
		lits = append(lits, v)
	}
	// NUMERIC order, per §4 — not the order of the decimal strings the map is
	// keyed by, which would put 10 before 9 and make the set a function of an
	// encoding rather than of the values.
	sort.Slice(lits, func(i, j int) bool { return lits[i].Cmp(lits[j]) < 0 })
	return &strWeights{strHash: canonicalStrHash(), lits: lits}, nil
}

// collectDefLiterals accumulates one definition's Int literals, keyed by
// decimal string so deduplication is by NUMERIC VALUE across arbitrary
// precision rather than by pointer.
//
// Body AND property bodies, per §4: a property is part of the definition, so
// its literals are the definition's.
//
// THERE IS NO `str` CASE, and its absence is deliberate. §4 says surface Str
// literals contribute THROUGH this rule rather than beside it, because they
// elaborate to `SCons` chains of Int literals before canonicalization — so a
// separate case would either double-count or diverge. TestNoStrTermsSurvive
// canonicalization checks that premise against the corpus instead of trusting
// it.
func collectDefLiterals(d *Def, out map[string]*big.Int) {
	if d == nil {
		return
	}
	var walk func(t *Term)
	walk = func(t *Term) {
		if t == nil {
			return
		}
		if t.K == "int" && t.Int != nil {
			out[t.Int.String()] = new(big.Int).Set(t.Int)
		}
		walk(t.A)
		walk(t.B)
		walk(t.C)
		for i := range t.Args {
			walk(&t.Args[i])
		}
		for i := range t.Arms {
			walk(&t.Arms[i])
		}
	}
	walk(d.Body)
	for pi := range d.Props {
		walk(&d.Props[pi].Body)
	}
}

// mustSchedule resolves a generation schedule or fails the run. Test-facing
// convenience over newGenSchedule, so a measurement cannot silently proceed on
// a schedule that failed to resolve.
func mustSchedule(st *Store, owner string) *genSchedule {
	sch, err := newGenSchedule(st, owner)
	if err != nil {
		panic(fmt.Sprintf("resolving the generation schedule for %s: %v", shortHash(owner), err))
	}
	return sch
}

// unweightedSchedule is the schedule a definition would have had before SPEC
// §4's literal rule: the same seed, an EMPTY literal set.
//
// It exists for MEASUREMENT and for two-way controls — a comparison that cannot
// produce a difference cannot witness that the rule does anything — and for
// nothing else. No kernel path builds one: §4 makes L(D) a function of the
// definition, not a mode.
func unweightedSchedule(owner string) *genSchedule {
	return &genSchedule{owner: owner, base: caseSeedBase(owner)}
}

// zeroOwnerHash is the all-zero definition hash used by measurements that
// generate a Str with no owner in view — the seed base is then 0, matching what
// those measurements pinned before the schedule became an object.
const zeroOwnerHash = "0000000000000000000000000000000000000000000000000000000000000000"
