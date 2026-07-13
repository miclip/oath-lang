package main

import (
	"fmt"
	"os"
	"sort"
)

const usage = `oath — a content-addressed, spec-carrying language kernel

usage:
  oath put <file.oath>     elaborate, typecheck, store, and verify definitions
  oath ls                  list named definitions and their guarantees
  oath get <name>          print the human projection of a definition
  oath verify <name>       re-run a definition's properties
  oath eval "<expr>"       typecheck and evaluate an expression

the codebase lives in ./codebase (override with OATH_STORE)`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Println(usage)
		os.Exit(1)
	}
	storeDir := os.Getenv("OATH_STORE")
	if storeDir == "" {
		storeDir = "codebase"
	}
	st, err := OpenStore(storeDir)
	if err != nil {
		fail(err)
	}
	switch args[0] {
	case "put":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath put <file.oath>"))
		}
		cmdPut(st, args[1])
	case "ls":
		cmdLs(st)
	case "get":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath get <name>"))
		}
		cmdGet(st, args[1])
	case "verify":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath verify <name>"))
		}
		cmdVerify(st, args[1])
	case "eval":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath eval \"<expr>\""))
		}
		cmdEval(st, args[1])
	default:
		fmt.Println(usage)
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func cmdPut(st *Store, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	forms, err := parseForms(string(src))
	if err != nil {
		fail(err)
	}
	anyFalsified := false
	for _, f := range forms {
		if f.K != "list" || len(f.Kids) == 0 || f.Kids[0].K != "sym" {
			fail(fmt.Errorf("line %d: top-level forms must be (data ...) or (defn ...)", f.Line))
		}
		var def *Def
		var meta *Meta
		switch f.Kids[0].Sym {
		case "data":
			def, meta, err = elabData(st, f)
		case "defn":
			def, meta, err = elabFunc(st, f)
		default:
			err = fmt.Errorf("line %d: unknown top-level form %q", f.Line, f.Kids[0].Sym)
		}
		if err != nil {
			fail(err)
		}

		// The kernel gate: nothing enters the codebase without typechecking.
		if err := checkDef(st, def); err != nil {
			fmt.Printf("✗ %-16s REJECTED: %v\n", meta.Name, err)
			os.Exit(1)
		}

		h, prev, err := st.Put(def, meta)
		if err != nil {
			fail(err)
		}

		status := ""
		if prev != "" {
			status = fmt.Sprintf("  (name repointed; old version %s remains immutable)", shortHash(prev))
		}

		if def.K == "func" {
			reports, err := verifyDef(st, h)
			if err != nil {
				fail(err)
			}
			m, _ := st.GetMeta(h)
			mark := "✓"
			if m.Guarantee.Level == "falsified" {
				mark = "✗"
				anyFalsified = true
			}
			fmt.Printf("%s %-16s #%s  %s%s\n", mark, meta.Name, shortHash(h), guaranteeString(m.Guarantee), status)
			for _, r := range reports {
				if r.Failed {
					fmt.Printf("    prop %-24s FALSIFIED after %d cases\n", r.Name, r.Passed)
					fmt.Printf("      counterexample: %s\n", r.Counter)
				} else if r.Err != "" {
					fmt.Printf("    prop %-24s ERROR: %s\n", r.Name, r.Err)
				} else {
					fmt.Printf("    prop %-24s passed %d cases\n", r.Name, r.Passed)
				}
			}
		} else {
			fmt.Printf("✓ %-16s #%s  data (%d constructors)%s\n", meta.Name, shortHash(h), len(def.Ctors), status)
		}
	}
	if anyFalsified {
		os.Exit(2)
	}
}

func cmdLs(st *Store) {
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := names[k]
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			continue
		}
		kind := "func"
		g := guaranteeString(m.Guarantee)
		if d.K == "data" {
			kind = "data"
			g = fmt.Sprintf("%d constructors", len(d.Ctors))
		}
		fmt.Printf("%-16s #%s  %-5s %s\n", k, shortHash(h), kind, g)
	}
}

func cmdGet(st *Store, name string) {
	h, ok := st.Resolve(name)
	if !ok {
		fail(fmt.Errorf("no definition named %q", name))
	}
	out, err := printDef(st, h)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdVerify(st *Store, name string) {
	h, ok := st.Resolve(name)
	if !ok {
		fail(fmt.Errorf("no definition named %q", name))
	}
	reports, err := verifyDef(st, h)
	if err != nil {
		fail(err)
	}
	if len(reports) == 0 {
		fmt.Printf("%s has no properties; guarantee remains: asserted\n", name)
		return
	}
	for _, r := range reports {
		if r.Failed {
			fmt.Printf("✗ prop %-24s FALSIFIED after %d cases\n", r.Name, r.Passed)
			fmt.Printf("    counterexample: %s\n", r.Counter)
		} else if r.Err != "" {
			fmt.Printf("✗ prop %-24s ERROR: %s\n", r.Name, r.Err)
		} else {
			fmt.Printf("✓ prop %-24s passed %d cases\n", r.Name, r.Passed)
		}
	}
}

func cmdEval(st *Store, src string) {
	forms, err := parseForms(src)
	if err != nil {
		fail(err)
	}
	if len(forms) != 1 {
		fail(fmt.Errorf("eval expects exactly one expression"))
	}
	e := &elab{st: st}
	term, err := e.elabTerm(forms[0])
	if err != nil {
		fail(err)
	}
	c := &checker{st: st}
	ty, err := c.synth(nil, term)
	if err != nil {
		fail(err)
	}
	ev := &evaluator{st: st, fuel: propFuel}
	v, err := ev.eval(nil, "", term)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%s : %s\n", printValue(st, v), printTy(st, ty, nil))
}
