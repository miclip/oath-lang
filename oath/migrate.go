package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// One-shot migration from the v0 canonical-JSON identity (kernel ≤0.6) to
// the O1 binary identity (#7). Identity changes for every object, so this
// is the one legitimate mass-rehash in the store's life:
//
//   1. Load every objects/*.json via the LEGACY decoding.
//   2. Topologically (dependencies first — the hash graph is a DAG),
//      rewrite each definition's EMBEDDED hash references old→new,
//      re-encode as O1, and store under the new hash.
//   3. Structure-pure metadata moves to the new hash (proofs, termination,
//      confinement, guarantee); SEED-dependent verdicts (mutation scores,
//      waivers) are dropped and must be re-earned — seeds derive from hashes.
//   4. names.json values are remapped. The journal is append-only history:
//      old entries keep their old hashes, and each migrated object gets a
//      "migrated" entry (hash=new, prev=old) so the mapping is durable.
//
// The migration refuses to run twice (no .json objects → nothing to do).

func cmdMigrateEncoding(st *Store) {
	objDir := filepath.Join(st.Root, "objects")
	entries, err := os.ReadDir(objDir)
	if err != nil {
		fail(err)
	}
	old := map[string]*Def{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(objDir, e.Name()))
		if err != nil {
			fail(err)
		}
		var d Def
		if err := json.Unmarshal(b, &d); err != nil {
			fail(fmt.Errorf("legacy object %s: %w", shortHash(name), err))
		}
		if got := hashDefV0(&d); got != name {
			fail(fmt.Errorf("legacy object %s fails its v0 identity check (%s)", shortHash(name), shortHash(got)))
		}
		old[name] = &d
	}
	if len(old) == 0 {
		fmt.Println("no legacy .json objects found — store is already O1")
		return
	}

	// Topological order over the old hash DAG, deterministic (sorted).
	order := make([]string, 0, len(old))
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(h string)
	visit = func(h string) {
		if state[h] != 0 {
			return
		}
		state[h] = 1
		deps := collectDeps(old[h])
		sorted := make([]string, 0, len(deps))
		for dh := range deps {
			sorted = append(sorted, dh)
		}
		sort.Strings(sorted)
		for _, dh := range sorted {
			if _, ok := old[dh]; !ok {
				fail(fmt.Errorf("object %s references %s, which is not in the store", shortHash(h), shortHash(dh)))
			}
			visit(dh)
		}
		state[h] = 2
		order = append(order, h)
	}
	keys := make([]string, 0, len(old))
	for h := range old {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	for _, h := range keys {
		visit(h)
	}

	// Rewrite embedded references and re-store under the new identity.
	newHash := map[string]string{}
	for _, oldH := range order {
		d := rewriteHashes(old[oldH], newHash)
		nh := hashDef(d)
		if err := writeFileAtomic(filepath.Join(objDir, nh+".bin"), encodeDef(d), 0o644); err != nil {
			fail(err)
		}
		// Metadata travels — EXCEPT seed-dependent verdicts. Mutation scores
		// and waivers are facts about structure × seed, and seeds derive from
		// hashes, which just changed: carrying them forward bakes stale
		// numbers into fixtures (learned the hard way — the blind Rust kernel
		// caught a stale i-overlaps score the first migration carried).
		// Structure-pure verdicts (proofs, termination, confinement,
		// guarantee) survive; scores must be re-earned under the new seeds.
		metaOld := filepath.Join(st.Root, "meta", oldH+".json")
		if mb, err := os.ReadFile(metaOld); err == nil {
			var mm Meta
			if err := json.Unmarshal(mb, &mm); err != nil {
				fail(fmt.Errorf("meta for %s: %w", shortHash(oldH), err))
			}
			mm.MutantsKilled, mm.MutantsTotal = 0, 0
			mm.WaivedMutants = nil
			nb, _ := json.MarshalIndent(&mm, "", "  ")
			if err := writeFileAtomic(filepath.Join(st.Root, "meta", nh+".json"), nb, 0o644); err != nil {
				fail(err)
			}
			_ = os.Remove(metaOld)
		}
		_ = os.Remove(filepath.Join(objDir, oldH+".json"))
		newHash[oldH] = nh
		_ = st.AppendLog(&LogEntry{Author: "migration", Name: st.NameOf(nh), Status: "migrated", Hash: nh, Prev: oldH})
	}

	// Repoint every name.
	names := st.Names()
	for n, h := range names {
		nh, ok := newHash[h]
		if !ok {
			fail(fmt.Errorf("name %q points at %s, which was not migrated", n, shortHash(h)))
		}
		names[n] = nh
	}
	if err := st.writeNames(names); err != nil {
		fail(err)
	}
	fmt.Printf("migrated %d objects to O1 binary identity; %d names repointed\n", len(order), len(names))
}

// rewriteHashes deep-copies a definition with every embedded hash reference
// remapped through m. Dependencies were migrated first, so every reference
// must already have a mapping.
func rewriteHashes(d *Def, m map[string]string) *Def {
	out := deepCopyDef(d)
	var walkTy func(t *Ty)
	walkTy = func(t *Ty) {
		if t == nil {
			return
		}
		if t.K == "data" {
			nh, ok := m[t.Hash]
			if !ok {
				fail(fmt.Errorf("unmigrated type reference %s", shortHash(t.Hash)))
			}
			t.Hash = nh
		}
		walkTy(t.A)
		walkTy(t.B)
		for i := range t.Args {
			walkTy(&t.Args[i])
		}
	}
	var walkTerm func(t *Term)
	walkTerm = func(t *Term) {
		if t == nil {
			return
		}
		if t.Hash != "" {
			nh, ok := m[t.Hash]
			if !ok {
				fail(fmt.Errorf("unmigrated term reference %s", shortHash(t.Hash)))
			}
			t.Hash = nh
		}
		walkTy(t.Ty)
		for i := range t.TyArgs {
			walkTy(&t.TyArgs[i])
		}
		walkTerm(t.A)
		walkTerm(t.B)
		walkTerm(t.C)
		for i := range t.Args {
			walkTerm(&t.Args[i])
		}
		for i := range t.Arms {
			walkTerm(&t.Arms[i])
		}
	}
	for ci := range out.Ctors {
		for fi := range out.Ctors[ci] {
			walkTy(&out.Ctors[ci][fi])
		}
	}
	walkTy(out.Ty)
	walkTerm(out.Body)
	for pi := range out.Props {
		for bi := range out.Props[pi].Binders {
			walkTy(&out.Props[pi].Binders[bi])
		}
		walkTerm(&out.Props[pi].Body)
	}
	return out
}
