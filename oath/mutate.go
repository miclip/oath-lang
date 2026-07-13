package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
		case "int":
			old := n.Int
			for _, nv := range []int64{old + 1, old - 1, 0} {
				if nv == old {
					continue
				}
				n.Int = nv
				add(fmt.Sprintf("literal %d → %d", old, nv))
				n.Int = old
			}
		case "if":
			n.B, n.C = n.C, n.B
			add("swapped if branches")
			n.B, n.C = n.C, n.B
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
	h, ok := st.Resolve(name)
	if !ok {
		fail(fmt.Errorf("no definition named %q", name))
	}
	d, err := st.GetDef(h)
	if err != nil {
		fail(err)
	}
	if d.K != "func" {
		fail(fmt.Errorf("only function definitions can be mutation-tested"))
	}
	m, err := st.GetMeta(h)
	if err != nil {
		fail(err)
	}
	if len(d.Props) == 0 {
		fmt.Printf("%s swears no properties — every mutant survives; spec strength is zero.\n", name)
		return
	}
	muts := genMutants(st, d)
	if len(muts) == 0 {
		fmt.Printf("no mutation points in %s (body has no mutable operators, literals, or branches)\n", name)
		return
	}
	killed := 0
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
		if killer != "" {
			killed++
			fmt.Printf("✓ killed    %-22s by %s\n", mu.desc, killer)
		} else {
			pr := &printer{st: st, tvs: m.TyVarNames}
			fmt.Printf("✗ SURVIVED  %-22s — no property notices this change\n", mu.desc)
			fmt.Printf("    mutant: %s\n", pr.term(mu.def.Body, m.Name))
		}
	}
	fmt.Printf("spec strength: %d/%d mutants killed\n", killed, len(muts))
	m.MutantsKilled, m.MutantsTotal = killed, len(muts)
	if err := st.SetMeta(h, m); err != nil {
		fail(err)
	}
}
