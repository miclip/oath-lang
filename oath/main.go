package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const usage = `oath — a content-addressed, spec-carrying language kernel

usage:
  oath put [--json] [--author <id>] [--context <hash>] [--key <file>] <file.oath>
                                      elaborate, typecheck, store, verify; every attempt is journaled
                                      (--context: the context-hash the code was authored against;
                                       --key: Ed25519 private key — signs the journal entry, pubkey = principal)
  oath keygen [--out <prefix>]        generate an Ed25519 signing keypair (<prefix>.key + <prefix>.pub)
  oath prove-worker [--scan] [--once] [--key <file>] [--interval D] [--lease D]
                                      drain the proof queue: SMT-prove queued objects out of band,
                                      bind require_proven names once proven (#14). --scan seeds the
                                      queue from every tested-but-unproven def; --key signs verdicts
  oath log [name]                     append-only submission journal (all attempts, incl. rejections)
  oath ls                             list named definitions and their guarantees
  oath get <name>                     print the human projection of a definition
  oath find <name>                    find definitions that satisfy the same PROPERTY (by content hash, not name)
  oath find --spec <file>             find definitions that satisfy a FRESH spec (a defn whose props are the query)
  oath find --implies <file>          find definitions that PROVABLY satisfy a spec (Z3 proof, catches semantic matches)
  oath find --equiv <name>            find definitions that are the SAME FUNCTION up to rewrite rules (the e-graph)
  oath context <name...> [--budget N] spec-only slice of the named defs + transitive deps (no bodies)
  oath dependents <name>              list definitions that reference a definition
  oath verify <name>                  re-run a definition's properties
  oath mutate <name>                  score spec strength: do the properties notice mutations?
  oath waive <name> <mutant> "<why>"  record a surviving mutant as judged-equivalent, with justification
  oath cross <nameA> <nameB> [--record]  N-version misalignment check: run each spec against the other's body
  oath prove <name>                   SMT-prove properties for ALL inputs (non-recursive Int/Bool fragment)
  oath eval "<expr>"                  typecheck and evaluate an expression
  oath serve                          MCP server over stdio (tools for agent sessions)
  oath serve --http <addr> --tokens <file>
                                      team store: MCP over HTTP with authenticated principals;
                                      repoint policy in <store>/policy.json (docs/teamstore.md)
  oath build <name> [-o out]          compile a verified definition to a native executable
                                      (entry protocol: (-> (List Str) Str); refuses falsified names)
  oath export <name> [-o pkg]         bundle a definition + transitive closure for publication
  oath import <path|url> [--as name]  admit a bundle: hash-checked, gate-checked, RE-VERIFIED locally
  oath fixtures <dir>                 materialize the SPEC §10 conformance suite as byte fixtures

the codebase lives in ./codebase (override with OATH_STORE)`

func main() {
	// The wasm build has already published its entry point from an init(); the
	// runtime must stay alive for JavaScript to call it, and there are no
	// command-line arguments to parse (#34).
	if isWasm {
		select {}
	}
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
		ctxHash := ""
		keyFile := os.Getenv("OATH_KEY")
		var files []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--json":
				jsonMode = true
			case rest[i] == "--author" && i+1 < len(rest):
				author = rest[i+1]
				i++
			case rest[i] == "--context" && i+1 < len(rest):
				ctxHash = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			default:
				files = append(files, rest[i])
			}
		}
		// A signing key makes the put attributed by SIGNATURE, not by an
		// unverified label: the pubkey is the principal. Default the author label
		// to the key's own pubkey so `oath log` shows who signed without needing
		// a separate --author (docs/registry-auth.md).
		if keyFile != "" {
			priv, pub := loadSigningKey(keyFile)
			st.SetSigner(priv)
			if author == "" {
				author = pub
			}
		}
		if author == "" {
			author = "unattributed"
		}
		if len(files) != 1 {
			fail(fmt.Errorf("usage: oath put [--json] [--author <id>] [--context <hash>] [--key <file>] <file.oath>"))
		}
		cmdPut(st, files[0], jsonMode, author, ctxHash)
	case "keygen":
		prefix := "oath"
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if (rest[i] == "--out" || rest[i] == "-o") && i+1 < len(rest) {
				prefix = rest[i+1]
				i++
			} else {
				prefix = rest[i]
			}
		}
		cmdKeygen(prefix)
	case "prove-worker":
		o := proveWorkerOpts{author: os.Getenv("OATH_AUTHOR")}
		keyFile := os.Getenv("OATH_KEY")
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--scan":
				o.scan = true
			case rest[i] == "--once":
				o.once = true
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			case rest[i] == "--author" && i+1 < len(rest):
				o.author = rest[i+1]
				i++
			case rest[i] == "--interval" && i+1 < len(rest):
				d, derr := time.ParseDuration(rest[i+1])
				if derr != nil {
					fail(fmt.Errorf("--interval: %w", derr))
				}
				o.interval = d
				i++
			case rest[i] == "--lease" && i+1 < len(rest):
				d, derr := time.ParseDuration(rest[i+1])
				if derr != nil {
					fail(fmt.Errorf("--lease: %w", derr))
				}
				o.leaseTTL = d
				i++
			default:
				fail(fmt.Errorf("prove-worker: unexpected argument %q", rest[i]))
			}
		}
		if keyFile != "" {
			priv, pub := loadSigningKey(keyFile)
			st.SetSigner(priv)
			if o.author == "" {
				o.author = pub
			}
		}
		cmdProveWorker(st, o)
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
	case "find":
		if len(args) == 3 && args[1] == "--spec" {
			cmdFindSpec(st, args[2])
		} else if len(args) == 3 && args[1] == "--implies" {
			cmdFindImplies(st, args[2])
		} else if len(args) == 3 && args[1] == "--equiv" {
			cmdFindEquiv(st, args[2])
		} else if len(args) == 2 {
			cmdFind(st, args[1])
		} else {
			fail(fmt.Errorf("usage: oath find <name> | --spec <file> | --implies <file> | --equiv <name>"))
		}
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
	case "cross":
		record := false
		rest := args[1:]
		var names []string
		for _, a := range rest {
			if a == "--record" {
				record = true
			} else {
				names = append(names, a)
			}
		}
		if len(names) != 2 {
			fail(fmt.Errorf("usage: oath cross <nameA> <nameB> [--record]"))
		}
		author := os.Getenv("OATH_AUTHOR")
		if author == "" {
			author = "unattributed"
		}
		out, err := apiCross(st, names[0], names[1], record, author)
		if err != nil {
			fail(err)
		}
		fmt.Print(out)
	case "waive":
		if len(args) != 4 {
			fail(fmt.Errorf("usage: oath waive <name> <mutant-hash-prefix> \"<reason>\""))
		}
		by := os.Getenv("OATH_AUTHOR")
		if by == "" {
			by = "unattributed"
		}
		out, err := apiWaive(st, args[1], args[2], args[3], by)
		if err != nil {
			fail(err)
		}
		fmt.Print(out)
	case "prove":
		if len(args) != 2 {
			fail(fmt.Errorf("usage: oath prove <name>"))
		}
		cmdProve(st, args[1])
	case "serve":
		httpAddr, tokensPath := "", ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--http" && i+1 < len(rest):
				httpAddr = rest[i+1]
				i++
			case rest[i] == "--tokens" && i+1 < len(rest):
				tokensPath = rest[i+1]
				i++
			default:
				fail(fmt.Errorf("usage: oath serve [--http addr --tokens file]"))
			}
		}
		if httpAddr != "" {
			cmdServeHTTP(st, httpAddr, tokensPath)
		} else {
			cmdServe(st)
		}
	case "build":
		outPath := ""
		var names []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "-o" && i+1 < len(rest) {
				outPath = rest[i+1]
				i++
			} else {
				names = append(names, rest[i])
			}
		}
		if len(names) != 1 {
			fail(fmt.Errorf("usage: oath build <name> [-o out]"))
		}
		cmdBuild(st, names[0], outPath)
	case "export":
		outPath := ""
		var names []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "-o" && i+1 < len(rest) {
				outPath = rest[i+1]
				i++
			} else {
				names = append(names, rest[i])
			}
		}
		if len(names) != 1 {
			fail(fmt.Errorf("usage: oath export <name> [-o bundle.oathpkg]"))
		}
		cmdExport(st, names[0], outPath)
	case "import":
		asName := ""
		author := os.Getenv("OATH_AUTHOR")
		if author == "" {
			author = "unattributed"
		}
		var srcs []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--as" && i+1 < len(rest) {
				asName = rest[i+1]
				i++
			} else {
				srcs = append(srcs, rest[i])
			}
		}
		if len(srcs) != 1 {
			fail(fmt.Errorf("usage: oath import <path|url> [--as name]"))
		}
		cmdImport(st, srcs[0], asName, author)
	case "migrate-encoding":
		cmdMigrateEncoding(st)
	case "fixtures":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			// The guard exists because `oath fixtures --help` once generated a
			// fixture tree into a directory literally named "--help" (#23).
			fail(fmt.Errorf("usage: oath fixtures <dir>"))
		}
		cmdFixtures(st, args[1])
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
	Status      string     `json:"status"` // accepted | falsified | rejected | blocked | pending
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

func cmdPut(st *Store, path string, jsonMode bool, author string, ctxHash string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	results, perr := apiPut(st, string(src), author, ctxHash)
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

// cmdKeygen writes a fresh Ed25519 keypair. The private key (<prefix>.key, mode
// 0600, hex of the 64-byte key) is the principal's secret; the public key
// (<prefix>.pub, hex of 32 bytes) IS the principal's identity — share it freely.
// Authorship is the signature over the object; the host stores signed bytes and
// never holds a secret (docs/registry-auth.md).
func cmdKeygen(prefix string) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		fail(err)
	}
	keyPath := prefix + ".key"
	pubPath := prefix + ".pub"
	if _, err := os.Stat(keyPath); err == nil {
		fail(fmt.Errorf("%s already exists; refusing to overwrite a signing key", keyPath))
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		fail(err)
	}
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s (private, keep secret) and %s\n", keyPath, pubPath)
	fmt.Printf("principal (pubkey): %s\n", hex.EncodeToString(pub))
}

// loadSigningKey reads a hex-encoded Ed25519 private key and returns it with its
// hex pubkey. Accepts either the 64-byte key or a 32-byte seed.
func loadSigningKey(path string) (ed25519.PrivateKey, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(fmt.Errorf("reading signing key %s: %w", path, err))
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		fail(fmt.Errorf("signing key %s is not valid hex: %w", path, err))
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	default:
		fail(fmt.Errorf("signing key %s has wrong length %d (want %d-byte key or %d-byte seed)", path, len(raw), ed25519.PrivateKeySize, ed25519.SeedSize))
	}
	return priv, hex.EncodeToString(priv.Public().(ed25519.PublicKey))
}

func cmdLog(st *Store, filter string) {
	fmt.Print(apiLog(st, filter))
	// The journal is the audit trail; reading it silently past tampering
	// would defeat the point. Entries are still printed above so the damage
	// can be inspected.
	if err := st.VerifyLog(); err != nil {
		fail(fmt.Errorf("journal integrity: %w", err))
	}
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

func cmdFind(st *Store, name string) {
	out, err := apiFind(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdFindSpec(st *Store, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	out, err := apiFindSpec(st, string(src))
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdFindImplies(st *Store, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	out, err := apiFindImplies(st, string(src))
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdFindEquiv(st *Store, name string) {
	out, err := apiFindEquiv(st, name)
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
