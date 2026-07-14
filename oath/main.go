package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

const usage = `oath — a content-addressed, spec-carrying language kernel

usage:
  oath put [--json] [--author <id>] <file.oath>
                                      elaborate, typecheck, store, verify; every attempt is journaled
  oath log [name]                     append-only submission journal (all attempts, incl. rejections)
  oath ls                             list named definitions and their guarantees
  oath get <name>                     print the human projection of a definition
  oath context <name...> [--budget N] spec-only slice of the named defs + transitive deps (no bodies)
  oath dependents <name>              list definitions that reference a definition
  oath verify <name>                  re-run a definition's properties
  oath mutate <name>                  score spec strength: do the properties notice mutations?
  oath prove <name>                   SMT-prove properties for ALL inputs (non-recursive Int/Bool fragment)
  oath eval "<expr>"                  typecheck and evaluate an expression
  oath serve                          MCP server over stdio (tools for agent sessions)

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
		author := os.Getenv("OATH_AUTHOR")
		var files []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--json":
				jsonMode = true
			case rest[i] == "--author" && i+1 < len(rest):
				author = rest[i+1]
				i++
			default:
				files = append(files, rest[i])
			}
		}
		if author == "" {
			author = "unattributed"
		}
		if len(files) != 1 {
			fail(fmt.Errorf("usage: oath put [--json] [--author <id>] <file.oath>"))
		}
		cmdPut(st, files[0], jsonMode, author)
	case "log":
		filter := ""
		if len(args) > 1 {
			filter = args[1]
		}
		cmdLog(st, filter)
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
	case "prove":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath prove <name>"))
		}
		cmdProve(st, args[1])
	case "serve":
		cmdServe(st)
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
	Name        string     `json:"name"`
	Hash        string     `json:"hash,omitempty"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"` // accepted | falsified | rejected
	Guarantee   string     `json:"guarantee,omitempty"`
	Termination string     `json:"termination,omitempty"`
	Confinement string     `json:"confinement,omitempty"`
	Prev        string     `json:"prev,omitempty"`
	Ctors       int        `json:"ctors,omitempty"`
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

func cmdPut(st *Store, path string, jsonMode bool, author string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	results, perr := apiPut(st, string(src), author)
	if jsonMode {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Print(renderPutReports(results))
	}
	if perr != nil {
		fail(perr)
	}
	code := 0
	for _, rep := range results {
		switch rep.Status {
		case "rejected":
			code = 1
		case "falsified":
			if code == 0 {
				code = 2
			}
		}
	}
	if code != 0 {
		os.Exit(code)
	}
}

func cmdLog(st *Store, filter string) {
	fmt.Print(apiLog(st, filter))
}

func cmdLs(st *Store) {
	fmt.Print(apiLs(st))
}

func cmdGet(st *Store, name string) {
	out, err := apiGet(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdVerify(st *Store, name string) {
	out, err := apiVerify(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdEval(st *Store, src string) {
	out, err := apiEval(st, src)
	if err != nil {
		fail(err)
	}
	fmt.Println(out)
}
