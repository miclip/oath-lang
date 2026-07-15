package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

// The registry layer (#14), stage 1: TRUST BY REPRODUCTION.
//
// A public registry of verified code has two classic problems — verifier
// trust (why believe a stored verdict?) and proof portability (whose
// solver?). Oath's determinism dissolves the first: verdicts are
// reproducible from the object bytes alone, so the importer re-derives
// them and needs to trust nobody. Content addressing dissolves the
// distribution problem: a bundle is a plain file of hash-named objects,
// publishable on any dumb host — the registry needs no server logic at all.
//
// `oath export <name>` packs the definition and its transitive closure:
// canonical object bytes (identity), naming metadata (usability: without
// constructor names an imported ADT is unusable in surface syntax), and
// the publisher's guarantees as UNVERIFIED HINTS, clearly so.
//
// `oath import <path|url>` refuses anything whose bytes do not hash to
// their name, strict-decodes, gate-checks in dependency order, RE-VERIFIES
// every function locally (properties re-run; termination and confinement
// recomputed; proofs left to `oath prove`, which re-earns them), journals
// each admission with the bundle source as its context, and binds names —
// through the ordinary repoint policy, so a store's rules govern foreign
// code exactly like local code.

type bundleNaming struct {
	Name       string   `json:"name,omitempty"`
	TyVarNames []string `json:"tyvar_names,omitempty"`
	CtorNames  []string `json:"ctor_names,omitempty"`
	PropNames  []string `json:"prop_names,omitempty"`
	ParamNames []string `json:"param_names,omitempty"`
	// Publisher claims, imported as display-only hints, never as verdicts.
	ClaimedGuarantee string `json:"claimed_guarantee,omitempty"`
}

type bundleRoot struct {
	Root   string `json:"root"` // hash of the exported definition
	Name   string `json:"name"`
	Kernel string `json:"kernel"`
}

func cmdExport(st *Store, name, out string) {
	h, ok := st.Resolve(name)
	if !ok {
		fail(fmt.Errorf("no definition named %q", name))
	}
	// Transitive closure over full deps (props included — they are the def).
	closure := map[string]bool{}
	var visit func(string)
	visit = func(x string) {
		if closure[x] {
			return
		}
		closure[x] = true
		d, err := st.GetDef(x)
		if err != nil {
			fail(err)
		}
		for dep := range collectDeps(d) {
			visit(dep)
		}
	}
	visit(h)

	if out == "" {
		out = name + ".oathpkg"
	}
	f, err := os.Create(out)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	write := func(path string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data))}); err != nil {
			fail(err)
		}
		if _, err := tw.Write(data); err != nil {
			fail(err)
		}
	}

	hashes := make([]string, 0, len(closure))
	for x := range closure {
		hashes = append(hashes, x)
	}
	sort.Strings(hashes)
	naming := map[string]bundleNaming{}
	for _, x := range hashes {
		b, err := os.ReadFile(st.Root + "/objects/" + x + ".bin")
		if err != nil {
			fail(err)
		}
		write("objects/"+x+".bin", b)
		if m, err := st.GetMeta(x); err == nil {
			naming[x] = bundleNaming{
				Name: m.Name, TyVarNames: m.TyVarNames, CtorNames: m.CtorNames,
				PropNames: m.PropNames, ParamNames: m.ParamNames,
				ClaimedGuarantee: guaranteeString(m.Guarantee),
			}
		}
	}
	nb, _ := json.MarshalIndent(naming, "", "  ")
	write("naming.json", nb)
	rb, _ := json.MarshalIndent(bundleRoot{Root: h, Name: name, Kernel: kernelVersion}, "", "  ")
	write("root.json", rb)
	if err := tw.Close(); err != nil {
		fail(err)
	}
	fmt.Printf("exported %s (%d objects, transitive closure) → %s\n", name, len(hashes), out)
}

func cmdImport(st *Store, src, asName, author string) {
	var raw []byte
	var err error
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		resp, err2 := http.Get(src)
		if err2 != nil {
			fail(err2)
		}
		defer resp.Body.Close()
		raw, err = io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	} else {
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		fail(err)
	}

	objects := map[string][]byte{}
	naming := map[string]bundleNaming{}
	var root bundleRoot
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(err)
		}
		data, err := io.ReadAll(io.LimitReader(tr, 16<<20))
		if err != nil {
			fail(err)
		}
		switch {
		case strings.HasPrefix(hdr.Name, "objects/") && strings.HasSuffix(hdr.Name, ".bin"):
			h := strings.TrimSuffix(strings.TrimPrefix(hdr.Name, "objects/"), ".bin")
			objects[h] = data
		case hdr.Name == "naming.json":
			if err := json.Unmarshal(data, &naming); err != nil {
				fail(fmt.Errorf("corrupt naming.json: %w", err))
			}
		case hdr.Name == "root.json":
			if err := json.Unmarshal(data, &root); err != nil {
				fail(fmt.Errorf("corrupt root.json: %w", err))
			}
		}
	}
	if root.Root == "" || len(objects) == 0 {
		fail(fmt.Errorf("not an oath bundle (missing root.json or objects/)"))
	}

	// TRUST GATE 1 — identity: every object's bytes must hash to its name.
	defs := map[string]*Def{}
	for h, b := range objects {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != h {
			fail(fmt.Errorf("REFUSED: object %s does not hash to its name — bundle is corrupt or tampered", shortHash(h)))
		}
		d, err := decodeDef(b)
		if err != nil {
			fail(fmt.Errorf("REFUSED: object %s: %v", shortHash(h), err))
		}
		defs[h] = d
	}

	// Topological order (deps first) for gate-checking and admission.
	var order []string
	state := map[string]int{}
	var visit func(string)
	visit = func(h string) {
		if state[h] != 0 {
			return
		}
		state[h] = 1
		deps := collectDeps(defs[h])
		sorted := make([]string, 0, len(deps))
		for dh := range deps {
			sorted = append(sorted, dh)
		}
		sort.Strings(sorted)
		for _, dh := range sorted {
			if _, inBundle := defs[dh]; inBundle {
				visit(dh)
			} else if _, err := st.GetDef(dh); err != nil {
				fail(fmt.Errorf("REFUSED: %s references %s, which is neither in the bundle nor in this store", shortHash(h), shortHash(dh)))
			}
		}
		state[h] = 2
		order = append(order, h)
	}
	keys := make([]string, 0, len(defs))
	for h := range defs {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	for _, h := range keys {
		visit(h)
	}

	// Admission: gate-check, store, RE-VERIFY. Publisher verdicts are shown
	// as claims and then ignored — every guarantee is re-earned here.
	imported, renamed, collided := 0, 0, []string{}
	for _, h := range order {
		if _, err := st.GetDef(h); err == nil {
			continue // already have this exact object
		}
		d := defs[h]
		if err := checkDef(st, d); err != nil {
			fail(fmt.Errorf("REFUSED: object %s fails the gate: %v", shortHash(h), err))
		}
		n := naming[h]
		meta := &Meta{
			Name: n.Name, TyVarNames: n.TyVarNames, CtorNames: n.CtorNames,
			PropNames: n.PropNames, ParamNames: n.ParamNames,
			Guarantee: Guarantee{Level: "asserted"}, Author: author,
		}
		if _, err := st.StoreObject(d, meta); err != nil {
			fail(err)
		}
		if d.K == "func" {
			if _, err := verifyDef(st, h); err != nil {
				fail(err)
			}
			m, _ := st.GetMeta(h)
			m.Termination = terminationOf(st, d)
			m.Confinement = confinementOf(st, d)
			_ = st.SetMeta(h, m)
			if m.Guarantee.Level == "falsified" {
				fmt.Printf("⚠ %-16s FALSIFIED on re-verification (publisher claimed: %s) — stored, will not be named\n",
					n.Name, orWord(n.ClaimedGuarantee, "nothing"))
			}
		}
		_ = st.AppendLog(&LogEntry{Author: author, Name: n.Name, Kind: d.K, Status: "accepted", Hash: h, Context: "import:" + src})
		imported++

		// Bind names for usability (ctor lookup is name-index driven), via
		// the ordinary policy gate; collisions keep the local pointer.
		bindName := n.Name
		if h == root.Root && asName != "" {
			bindName = asName
		}
		if bindName == "" {
			continue
		}
		m, _ := st.GetMeta(h)
		if m.Guarantee.Level == "falsified" {
			continue
		}
		if existing, ok := st.Resolve(bindName); ok && existing != h {
			collided = append(collided, bindName)
			continue
		}
		specA, bodyA := attributeAuthorship(st, bindName, d, author)
		pol, err := LoadPolicy(st.Root)
		if err != nil {
			fail(err)
		}
		if ok, reason := evalPolicy(st, pol, bindName, h, d, specA, bodyA); !ok {
			fmt.Printf("⛔ %-16s name not bound: %s\n", bindName, reason)
			continue
		}
		if _, err := st.Repoint(bindName, h); err != nil {
			fail(err)
		}
		renamed++
	}
	fmt.Printf("imported %d objects (%d names bound) from %s — every verdict re-earned locally; publisher claims were hints\n", imported, renamed, src)
	if len(collided) > 0 {
		fmt.Printf("  name collisions kept local: %s\n", strings.Join(collided, ", "))
	}
	fmt.Printf("  proofs are not imported: run `oath prove <name>` to re-earn PROVEN locally\n")
}
