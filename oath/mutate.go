package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Mutation testing: the kernel's answer to "who verifies the specs?"
// If the same author writes both implementation and properties, a lazy or
// tautological spec passes trivially. So the kernel measures spec STRENGTH:
// generate small semantic mutations of the implementation and check whether
// the properties notice. A mutant that survives is a change in behavior the
// oath is blind to — printed loudly, with the surviving body, so the weak
// spot is visible. The score (killed/total) is recorded in metadata next to
// the guarantee: "tested" tells you the promises held; spec strength tells
// you whether the promises say anything.

const mutantCases = 60
const mutantFuel = 500_000

type mutantDef struct {
	desc string
	def  *Def
	hash string
}

func deepCopyDef(d *Def) *Def {
	b, _ := json.Marshal(d)
	var out Def
	_ = json.Unmarshal(b, &out)
	return &out
}

func deepCopyTerm(t *Term) *Term {
	b, _ := json.Marshal(t)
	var out Term
	_ = json.Unmarshal(b, &out)
	return &out
}

func collectNodes(t *Term, out *[]*Term) {
	if t == nil {
		return
	}
	*out = append(*out, t)
	collectNodes(t.A, out)
	collectNodes(t.B, out)
	collectNodes(t.C, out)
	for i := range t.Args {
		collectNodes(&t.Args[i], out)
	}
	for i := range t.Arms {
		collectNodes(&t.Arms[i], out)
	}
}

// Type-preserving operator substitutions, so every mutant still typechecks.
var opMutations = map[string][]string{
	"+": {"-"}, "-": {"+"}, "*": {"+"}, "/": {"*"}, "%": {"/"},
	"<": {"<="}, "<=": {"<"}, "and": {"or"}, "or": {"and"},
}

// Non-commutative binary primitives where swapping operands changes meaning.
var swappablePrims = map[string]bool{
	"-": true, "/": true, "%": true, "<": true, "<=": true,
}

// genMutants produces every single-node mutation of the definition's body
// that still typechecks. Props are copied unchanged: the question is whether
// THEY notice the body changed.
func genMutants(st *Store, d *Def) []mutantDef {
	work := deepCopyDef(d)
	var nodes []*Term
	collectNodes(work.Body, &nodes)
	seen := map[string]bool{hashDef(d): true}
	var out []mutantDef
	add := func(desc string) {
		md := deepCopyDef(work)
		if checkDef(st, md) != nil {
			return
		}
		h := hashDef(md)
		if seen[h] {
			return
		}
		seen[h] = true
		out = append(out, mutantDef{desc: desc, def: md, hash: h})
	}
	for _, n := range nodes {
		switch n.K {
		case "prim":
			for _, op := range opMutations[n.Op] {
				old := n.Op
				n.Op = op
				add(fmt.Sprintf("%s → %s", old, op))
				n.Op = old
			}
			if swappablePrims[n.Op] && len(n.Args) == 2 {
				n.Args[0], n.Args[1] = n.Args[1], n.Args[0]
				add(fmt.Sprintf("swapped operands of %s", n.Op))
				n.Args[0], n.Args[1] = n.Args[1], n.Args[0]
			}
		case "int":
			old := n.Int
			for _, nv := range []*big.Int{
				new(big.Int).Add(old, big.NewInt(1)),
				new(big.Int).Sub(old, big.NewInt(1)),
				big.NewInt(0),
			} {
				if nv.Cmp(old) == 0 {
					continue
				}
				n.Int = nv
				add(fmt.Sprintf("literal %s → %s", old, nv))
				n.Int = old
			}
		case "if":
			n.B, n.C = n.C, n.B
			add("swapped if branches")
			n.B, n.C = n.C, n.B
		case "app":
			// Swap adjacent call arguments: (f a b) → (f b a). Only mutants
			// that still typecheck survive the add() gate, so same-type
			// argument pairs are the ones that make it through.
			if n.A != nil && n.A.K == "app" {
				n.B, n.A.B = n.A.B, n.B
				add("swapped call arguments")
				n.B, n.A.B = n.A.B, n.B
			}
			// Replace a recursive call with one of its own arguments:
			// (self ... x ...) → x. This is the "forgot to recurse" bug —
			// structurally the smallest way to delete a recursion while
			// keeping an expression of (possibly) the right type.
			if head, args := unwindApp(n); head.K == "self" {
				// Shallow-save: the restore must reinstate the ORIGINAL child
				// pointers (nodes still aliases them), not copies — a deep-copy
				// restore would orphan the children and silently drop every
				// later mutation under this subtree.
				saved := *n
				for ai, a := range args {
					*n = *deepCopyTerm(a)
					add(fmt.Sprintf("recursive call → its argument %d", ai))
					*n = saved
				}
			}
		case "ctor":
			// Swap adjacent constructor arguments (tree children, record-ish
			// payloads); the typecheck gate keeps only same-type pairs.
			for ai := 0; ai+1 < len(n.Args); ai++ {
				n.Args[ai], n.Args[ai+1] = n.Args[ai+1], n.Args[ai]
				add(fmt.Sprintf("swapped constructor arguments %d,%d", ai, ai+1))
				n.Args[ai], n.Args[ai+1] = n.Args[ai+1], n.Args[ai]
			}
		case "match":
			// Swap two arm bodies. De Bruijn does the type policing: a body
			// referring to binders the other arm does not have fails the gate.
			for i := 0; i < len(n.Arms); i++ {
				for j := i + 1; j < len(n.Arms); j++ {
					n.Arms[i], n.Arms[j] = n.Arms[j], n.Arms[i]
					add(fmt.Sprintf("swapped match arms %d,%d", i, j))
					n.Arms[i], n.Arms[j] = n.Arms[j], n.Arms[i]
				}
			}
			// Collapse the match to a base arm: the whole match is replaced
			// by the body of an arm that binds nothing. This is the "always
			// take the base case" bug — the classic way recursion silently
			// disappears (length → 0, reverse → Nil).
			if md, err := st.GetDef(n.Hash); err == nil && md.K == "data" {
				saved := *n // shallow, for the same aliasing reason as above
				for ci := range saved.Arms {
					if ci < len(md.Ctors) && len(md.Ctors[ci]) == 0 {
						*n = *deepCopyTerm(&saved.Arms[ci])
						add(fmt.Sprintf("match collapsed to arm %d", ci))
						*n = saved
					}
				}
			}
		}
	}
	return out
}

func metaPropName(m *Meta, pi int) string {
	if pi < len(m.PropNames) {
		return m.PropNames[pi]
	}
	return fmt.Sprintf("prop%d", pi)
}

func cmdMutate(st *Store, name string) {
	out, err := apiMutate(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func apiMutate(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	return apiMutateHash(st, h)
}

// apiMutateHash mutation-scores an object directly by hash — used by the
// repoint policy, which must be able to score a candidate BEFORE any name
// points at it.
func apiMutateHash(st *Store, h string) (string, error) {
	name := st.NameOf(h)
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	if d.K != "func" {
		return "", fmt.Errorf("only function definitions can be mutation-tested")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if len(d.Props) == 0 {
		return fmt.Sprintf("%s swears no properties — every mutant survives; spec strength is zero.\n", name), nil
	}
	muts := genMutants(st, d)
	if len(muts) == 0 {
		return fmt.Sprintf("no mutation points in %s (body has no mutable operators, literals, or branches)\n", name), nil
	}
	waived := map[string]*WaivedMutant{}
	for i := range m.WaivedMutants {
		waived[m.WaivedMutants[i].Hash] = &m.WaivedMutants[i]
	}
	var b strings.Builder
	killed, waivedSeen := 0, 0
	for _, mu := range muts {
		// Mutants are evaluated from the in-memory cache only — they are
		// candidates under interrogation, never admitted to the codebase.
		st.CacheDef(mu.hash, mu.def)
		seedB, _ := hex.DecodeString(mu.hash[:16])
		base := binary.BigEndian.Uint64(seedB)
		killer := ""
		for pi := range mu.def.Props {
			rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi), base, pi, mutantCases, mutantFuel)
			if rep.Failed || rep.Err != "" {
				killer = rep.Name
				break
			}
		}
		switch {
		case killer != "":
			killed++
			fmt.Fprintf(&b, "✓ killed    %-22s by %s\n", mu.desc, killer)
		case waived[mu.hash] != nil:
			// A waiver is an annotation with a justification on record —
			// reported distinctly, never counted as a kill.
			waivedSeen++
			w := waived[mu.hash]
			fmt.Fprintf(&b, "○ waived    %-22s — %s (by %s)\n", mu.desc, w.Reason, w.By)
		default:
			pr := &printer{st: st, tvs: m.TyVarNames}
			fmt.Fprintf(&b, "✗ SURVIVED  %-22s — no property notices this change\n", mu.desc)
			fmt.Fprintf(&b, "    mutant: %s  (waive with: oath waive %s %s \"reason\")\n", pr.term(mu.def.Body, m.Name), name, shortHash(mu.hash))
		}
	}
	fmt.Fprintf(&b, "spec strength: %d/%d mutants killed", killed, len(muts))
	if waivedSeen > 0 {
		fmt.Fprintf(&b, " (+%d waived as equivalent, justification on record)", waivedSeen)
	}
	b.WriteString("\n")
	m.MutantsKilled, m.MutantsTotal = killed, len(muts)
	if err := st.SetMeta(h, m); err != nil {
		return "", err
	}
	return b.String(), nil
}

// apiWaive records a surviving mutant as judged-equivalent. The mutant is
// re-derived from the current definition so the waiver can only name a
// mutant that actually exists, and the full mutant hash is resolved from a
// short prefix. Waiving a mutant that a property kills is refused: waivers
// document unkillable survivors, they do not overrule the referee.
func apiWaive(st *Store, name, mutantPrefix, reason, by string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if reason == "" {
		return "", fmt.Errorf("a waiver requires a justification")
	}
	for _, mu := range genMutants(st, d) {
		if !strings.HasPrefix(mu.hash, mutantPrefix) {
			continue
		}
		st.CacheDef(mu.hash, mu.def)
		seedB, _ := hex.DecodeString(mu.hash[:16])
		base := binary.BigEndian.Uint64(seedB)
		for pi := range mu.def.Props {
			rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi), base, pi, mutantCases, mutantFuel)
			if rep.Failed || rep.Err != "" {
				return "", fmt.Errorf("mutant %s is killed by %s — nothing to waive", shortHash(mu.hash), rep.Name)
			}
		}
		for _, w := range m.WaivedMutants {
			if w.Hash == mu.hash {
				return fmt.Sprintf("mutant %s already waived: %s\n", shortHash(mu.hash), w.Reason), nil
			}
		}
		m.WaivedMutants = append(m.WaivedMutants, WaivedMutant{Hash: mu.hash, Desc: mu.desc, Reason: reason, By: by})
		if err := st.SetMeta(h, m); err != nil {
			return "", err
		}
		return fmt.Sprintf("○ waived %s (%s): %s\n", shortHash(mu.hash), mu.desc, reason), nil
	}
	return "", fmt.Errorf("no surviving mutant of %s matches %q (run oath mutate %s to list)", name, mutantPrefix, name)
}
