package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
)

const usage = `oath — a content-addressed, spec-carrying language kernel

usage:
  oath put [--json] <file.oath>       elaborate, typecheck, store, verify; --json for machine-readable verdicts
  oath ls                             list named definitions and their guarantees
  oath get <name>                     print the human projection of a definition
  oath context <name...> [--budget N] spec-only slice of the named defs + transitive deps (no bodies)
  oath dependents <name>              list definitions that reference a definition
  oath verify <name>                  re-run a definition's properties
  oath mutate <name>                  score spec strength: do the properties notice mutations?
  oath eval "<expr>"                  typecheck and evaluate an expression

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
		jsonMode := false
		var files []string
		for _, a := range args[1:] {
			if a == "--json" {
				jsonMode = true
			} else {
				files = append(files, a)
			}
		}
		if len(files) != 1 {
			fail(fmt.Errorf("usage: oath put [--json] <file.oath>"))
		}
		cmdPut(st, files[0], jsonMode)
	case "context":
		budget := 0
		var names []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--budget" && i+1 < len(rest) {
				budget, err = strconv.Atoi(rest[i+1])
				if err != nil {
					fail(fmt.Errorf("--budget needs a number"))
				}
				i++
			} else {
				names = append(names, rest[i])
			}
		}
		if len(names) == 0 {
			fail(fmt.Errorf("usage: oath context <name...> [--budget N]"))
		}
		cmdContext(st, names, budget)
	case "dependents":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath dependents <name>"))
		}
		cmdDependents(st, args[1])
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
	case "mutate":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath mutate <name>"))
		}
		cmdMutate(st, args[1])
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

// putReport is the machine-readable verdict for one definition — the exact
// feedback an AI author needs to regenerate: what failed, and on which inputs.
type putReport struct {
	Name      string     `json:"name"`
	Hash      string     `json:"hash,omitempty"`
	Kind      string     `json:"kind"`
	Status    string     `json:"status"` // accepted | falsified | rejected
	Guarantee   string     `json:"guarantee,omitempty"`
	Termination string     `json:"termination,omitempty"`
	Error       string     `json:"error,omitempty"`
	Props       []propJSON `json:"props,omitempty"`
}

type propJSON struct {
	Name           string `json:"name"`
	Passed         int    `json:"passed"`
	Failed         bool   `json:"failed"`
	Counterexample string `json:"counterexample,omitempty"`
	Error          string `json:"error,omitempty"`
}

func cmdPut(st *Store, path string, jsonMode bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	forms, err := parseForms(string(src))
	if err != nil {
		fail(err)
	}
	var results []putReport
	finish := func(code int) {
		if jsonMode {
			b, _ := json.MarshalIndent(results, "", "  ")
			fmt.Println(string(b))
		}
		if code != 0 {
			os.Exit(code)
		}
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
			results = append(results, putReport{Name: meta.Name, Kind: def.K, Status: "rejected", Error: err.Error()})
			if !jsonMode {
				fmt.Printf("✗ %-16s REJECTED: %v\n", meta.Name, err)
			}
			finish(1)
		}

		h, prev, err := st.Put(def, meta)
		if err != nil {
			fail(err)
		}

		status := ""
		if prev != "" {
			status = fmt.Sprintf("  (name repointed; old version %s remains immutable)", shortHash(prev))
		}

		rep := putReport{Name: meta.Name, Hash: h, Kind: def.K, Status: "accepted"}
		if def.K == "func" {
			reports, err := verifyDef(st, h)
			if err != nil {
				fail(err)
			}
			m, _ := st.GetMeta(h)
			m.Termination = terminationOf(st, def)
			if err := st.SetMeta(h, m); err != nil {
				fail(err)
			}
			rep.Guarantee = guaranteeString(m.Guarantee)
			rep.Termination = m.Termination
			if m.Guarantee.Level == "falsified" {
				rep.Status = "falsified"
				anyFalsified = true
			}
			for _, r := range reports {
				rep.Props = append(rep.Props, propJSON{
					Name: r.Name, Passed: r.Passed, Failed: r.Failed,
					Counterexample: r.Counter, Error: r.Err,
				})
			}
			if !jsonMode {
				mark := "✓"
				if rep.Status == "falsified" {
					mark = "✗"
				}
				fmt.Printf("%s %-16s #%s  %s%s%s\n", mark, meta.Name, shortHash(h), rep.Guarantee, termSuffix(m), status)
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
			}
		} else if !jsonMode {
			fmt.Printf("✓ %-16s #%s  data (%d constructors)%s\n", meta.Name, shortHash(h), len(def.Ctors), status)
		}
		results = append(results, rep)
	}
	if anyFalsified {
		finish(2)
	}
	finish(0)
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
		g := guaranteeString(m.Guarantee) + termSuffix(m)
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
