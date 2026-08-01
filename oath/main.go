package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
  oath migrate-store                  copy the OATH_STORE filesystem store into the cloud backend
                                      (GCS objects + Postgres index; cloud build only; docs/store-drivers.md)
  oath transfer <ns>/* --to <pubkey> --recipient-key <file> [--key <file>] [--remote <url>] [--dry-run]
  oath plugin install [--codex] [--user] [--dir <path>] [--registry <url>] [--dry-run]
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
  oath scorable                       list every definition the mutation engine can score
  oath demand [--all]                 coverage requests: spec shapes agents sought and did not find
  oath explain <name> [--json]        decision package: spec, evidence, provenance, deps, LIMITATIONS
  oath waive <name> <mutant> "<why>"  record a surviving mutant as judged-equivalent, with justification
  oath cross <nameA> <nameB> [--record]  N-version misalignment check: run each spec against the other's body
  oath prove <name>                   SMT-prove properties for ALL inputs (non-recursive Int/Bool fragment)
  oath hint <name> <prop> <lemma>[.<prop>]  admit a proven property as a lemma for a goal (#67)
  oath hint <name>                    list a definition's proof hints
  oath hint --clear <name> [<prop>]   remove hints (all, or one property's)
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
	if fn, ok := harnessCommands[args[0]]; ok {
		fn(args[1:])
		return
	}
	// A LOCATION FLAG THAT IS ACCEPTED AND IGNORED IS A QUERY-INTEGRITY BUG (#104).
	//
	// Only a few commands have a remote path. The rest parsed no --remote, read the
	// LOCAL store, and exited 0 — so `oath ls --remote <registry>` printed a
	// well-formed list of real definitions from a completely different store, with
	// no warning.
	//
	// This is worse than a missing feature because the output is PLAUSIBLE. It was
	// observed for real while checking which live names sat under newly reserved
	// prefixes: the local store held 187 names and the registry held 376, and the
	// local answer was read as the registry having LOST a migration. It had not.
	//
	// A CLI that accepts a flag it does not implement is asserting something false
	// about where its answer came from. Refusing is the only honest behaviour until
	// every read command can actually reach a registry.
	if !remoteCapable[args[0]] {
		for _, a := range args[1:] {
			if a == "--remote" {
				fail(fmt.Errorf("`oath %s` has no remote path: it can only read the LOCAL store, "+
					"so --remote would be silently ignored and the answer would come from "+
					"somewhere other than the registry you named. Use a command that supports "+
					"--remote (%s), or drop the flag to query locally on purpose", args[0], remoteCapableList()))
			}
		}
	}
	switch args[0] {
	case "put":
		jsonMode := false
		author := os.Getenv("OATH_AUTHOR")
		ctxHash := ""
		keyFile := os.Getenv("OATH_KEY")
		remote := os.Getenv("OATH_REGISTRY")
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
			case rest[i] == "--remote" && i+1 < len(rest):
				remote = rest[i+1]
				i++
			default:
				files = append(files, rest[i])
			}
		}
		// --remote publishes to a REGISTRY instead of the local store, signed. It
		// takes several files because publication order is load-bearing on a cold
		// registry: a definition cannot elaborate before its dependencies exist
		// (#83).
		if remote != "" {
			if len(files) == 0 {
				fail(fmt.Errorf("usage: oath put --remote <url> --key <file> <file.oath>..."))
			}
			cmdRemotePut(remote, keyFile, files, ctxHash)
			return
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
			fail(fmt.Errorf("usage: oath put [--json] [--author <id>] [--context <hash>] [--key <file>] [--remote <url>] <file.oath>"))
		}
		cmdPut(st, files[0], jsonMode, author, ctxHash)
	case "config":
		cmdConfig(args[1:])
	case "new":
		ns, registry := "", ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--remote" && i+1 < len(rest) {
				registry = rest[i+1]
				i++
			} else {
				ns = rest[i]
			}
		}
		cmdNew(ns, registry)
	case "transfer":
		// Bilateral: the holder authorizes surrender, the recipient accepts
		// custody, both over the SAME bytes.
		endpoint := os.Getenv("OATH_REGISTRY")
		keyFile := os.Getenv("OATH_KEY")
		kmsKey := os.Getenv("OATH_KMS_KEY")
		recipientKey := ""
		dryRun, yes := false, false
		namespace, toPub := "", ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--remote" && i+1 < len(rest):
				endpoint = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			case rest[i] == "--kms-key" && i+1 < len(rest):
				kmsKey = rest[i+1]
				i++
			case rest[i] == "--recipient-key" && i+1 < len(rest):
				recipientKey = rest[i+1]
				i++
			case rest[i] == "--to" && i+1 < len(rest):
				toPub = rest[i+1]
				i++
			case rest[i] == "--dry-run":
				dryRun = true
			case rest[i] == "--yes" || rest[i] == "-y":
				yes = true
			default:
				namespace = rest[i]
			}
		}
		if cfg, _, cerr := loadClientConfig(); cerr == nil && cfg != nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" {
				keyFile = cfg.Key
			}
		}
		cmdTransfer(st, endpoint, keyFile, kmsKey, recipientKey, namespace, toPub, dryRun, yes)
	case "plugin":
		// Wire a coding assistant to the substrate. The tools already existed; what
		// did not was any way for an assistant to find them.
		if len(args) < 2 || args[1] != "install" {
			fail(fmt.Errorf("usage: oath plugin install [--codex] [--user] [--dir <path>] [--registry <url>] [--dry-run]"))
		}
		cmdPluginInstall(args[2:])
	case "keygen":
		// Default OUTSIDE the working directory. The previous default wrote ./oath.key,
		// which is how a signing key ended up in a git repository — the ignore rule
		// catches the symptom, this removes the hazard. An explicit path still wins.
		prefix := ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if (rest[i] == "--out" || rest[i] == "-o") && i+1 < len(rest) {
				prefix = rest[i+1]
				i++
			} else {
				prefix = rest[i]
			}
		}
		if prefix == "" {
			if err := os.MkdirAll(filepath.Join(oathHome(), "keys"), 0o700); err != nil {
				fail(err)
			}
			prefix = filepath.Join(oathHome(), "keys", "default")
		}
		cmdKeygen(prefix)
	case "reserve":
		endpoint := os.Getenv("OATH_REGISTRY")
		keyFile := os.Getenv("OATH_KEY")
		dryRun, yes := false, false
		kmsKey := os.Getenv("OATH_KMS_KEY")
		namespace := ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--remote" && i+1 < len(rest):
				endpoint = rest[i+1]
				i++
			case rest[i] == "--kms-key" && i+1 < len(rest):
				kmsKey = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			case rest[i] == "--dry-run":
				dryRun = true
			case rest[i] == "--yes" || rest[i] == "-y":
				yes = true
			default:
				namespace = rest[i]
			}
		}
		// Same fallback publish uses, so `oath new` configures both.
		if cfg, _, err := loadClientConfig(); err == nil && cfg != nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" {
				keyFile = cfg.Key
			}
		}
		cmdReserve(st, endpoint, keyFile, kmsKey, namespace, dryRun, yes)
	case "authority":
		// Read-only: who holds a prefix, and who may publish under it. The
		// counterpart to `reserve` — a permanent, irreversible act deserves a way
		// to check the ground first, and telling someone to verify without giving
		// them a command is advice they cannot follow.
		//
		// The view is resolved EXACTLY as `reserve` resolves its target: same env,
		// same flags, same config fallback. That is not tidiness, it is the whole
		// correctness property (#104) — advice about an irreversible act must come
		// from the state that act will be evaluated against, so the two resolutions
		// must be the same resolution.
		endpoint := os.Getenv("OATH_REGISTRY")
		keyFile := os.Getenv("OATH_KEY")
		kmsKey := os.Getenv("OATH_KMS_KEY")
		query := ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--remote" && i+1 < len(rest):
				endpoint = rest[i+1]
				i++
			case rest[i] == "--kms-key" && i+1 < len(rest):
				kmsKey = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			default:
				query = rest[i]
			}
		}
		if cfg, _, err := loadClientConfig(); err == nil && cfg != nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" {
				keyFile = cfg.Key
			}
		}
		if rc := cmdAuthority(st, endpoint, keyFile, kmsKey, query); rc != 0 {
			os.Exit(rc)
		}
	case "delegate", "revoke":
		endpoint := os.Getenv("OATH_REGISTRY")
		keyFile := os.Getenv("OATH_KEY")
		kmsKey := os.Getenv("OATH_KMS_KEY")
		dryRun, yes := false, false
		namespace, subject := "", ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--remote" && i+1 < len(rest):
				endpoint = rest[i+1]
				i++
			case rest[i] == "--kms-key" && i+1 < len(rest):
				kmsKey = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			case (rest[i] == "--to" || rest[i] == "--from") && i+1 < len(rest):
				subject = rest[i+1]
				i++
			case rest[i] == "--dry-run":
				dryRun = true
			case rest[i] == "--yes" || rest[i] == "-y":
				yes = true
			default:
				namespace = rest[i]
			}
		}
		if cfg, _, cerr := loadClientConfig(); cerr == nil && cfg != nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" && kmsKey == "" {
				keyFile = cfg.Key
			}
		}
		if kmsKey != "" {
			keyFile = ""
		}
		op := opDelegate
		if args[0] == "revoke" {
			op = opRevoke
		}
		cmdDelegate(st, endpoint, keyFile, kmsKey, namespace, subject, op, dryRun, yes)
	case "publish":
		// Signed publication (#83): show the exact bytes, sign locally, send them
		// unchanged, then confirm the registry persisted the same bytes.
		endpoint := os.Getenv("OATH_REGISTRY")
		keyFile := os.Getenv("OATH_KEY")
		dryRun, jsonOut, yes := false, false, false
		license := ""
		kmsKey := os.Getenv("OATH_KMS_KEY")
		var file string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--remote" && i+1 < len(rest):
				endpoint = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			case rest[i] == "--kms-key" && i+1 < len(rest):
				kmsKey = rest[i+1]
				i++
			case rest[i] == "--license" && i+1 < len(rest):
				license = rest[i+1]
				i++
			case rest[i] == "--dry-run":
				dryRun = true
			case rest[i] == "--json":
				jsonOut = true
			case rest[i] == "--yes" || rest[i] == "-y":
				yes = true
			default:
				file = rest[i]
			}
		}
		// Fall back to ~/.oath/config (#86) so the defaults are load-bearing rather
		// than decorative. Precedence is flag > env > config, and `oath config` reports
		// which one won — a publication going somewhere unexpected is exactly when the
		// SOURCE of a setting matters more than its value.
		if cfg, _, cerr := loadClientConfig(); cerr == nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" {
				keyFile = cfg.Key
			}
		}
		// A KMS key satisfies the signing requirement exactly as a file does; the
		// guard must not privilege one signer over the other, or --kms-key would be
		// accepted and then refused for the absence of the flag it replaces.
		if endpoint == "" || (keyFile == "" && kmsKey == "") || file == "" {
			fail(fmt.Errorf("usage: oath publish [--remote <url>] [--key <file> | --kms-key <resource/cryptoKeyVersions/N>] [--license <SPDX>] [--dry-run] [--json] [-y] <file.oath>\n" +
				"       --remote and --key may come from ~/.oath/config (see `oath config`, set up with `oath new`)"))
		}
		// A config-file key must not silently join an explicit --kms-key and make the
		// invocation ambiguous. Explicit beats configured.
		if kmsKey != "" {
			keyFile = ""
		}
		cmdPublish(st, endpoint, keyFile, kmsKey, file, license, dryRun, jsonOut, yes)
	case "store-check":
		// SPEC-adjacent, #100: every committed metadata record must be exactly what
		// the CANONICAL encoder produces. Uses the kernel's own encodeMeta rather
		// than a lookalike — Go escapes HTML in JSON strings and other encoders do
		// not, so a check written in another language agrees only by luck.
		//
		// Artifact hashes protect SEMANTIC identity; this protects reproducibility
		// and reviewability, which are different properties. A representation does
		// not need to participate in identity to require canonical bytes.
		files, _ := filepath.Glob(filepath.Join(st.Root, "meta", "*.json"))
		bad := 0
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				fail(err)
			}
			var m Meta
			if err := json.Unmarshal(raw, &m); err != nil {
				fmt.Printf("  UNREADABLE %s: %v\n", filepath.Base(f), err)
				bad++
				continue
			}
			if got := encodeMeta(&m); !bytes.Equal(got, raw) {
				fmt.Printf("  NON-CANONICAL %s (%d bytes committed, %d re-encoded)\n",
					filepath.Base(f)[:12], len(raw), len(got))
				bad++
			}
		}
		fmt.Printf("STORE CANONICAL: %s — %d metadata record(s), %d non-canonical\n",
			map[bool]string{true: "PASS", false: "FAIL"}[bad == 0], len(files), bad)
		if bad > 0 {
			fmt.Println("  A no-op update rewrites these, so the store cannot be reproduced")
			fmt.Println("  by the kernel shipping with it and every touch produces a diff.")
			os.Exit(1)
		}
	case "ownership":
		// The pre-enforcement census (#84): what the registry believes about who
		// controls every name, and what enabling enforcement would do.
		verbose := true
		for _, x := range args[1:] {
			if x == "--summary" {
				verbose = false
			}
		}
		cmdOwnership(st, verbose)
	case "vectors":
		path := "fixtures/envelope/vectors.jsonl"
		if len(args) > 1 {
			path = args[1]
		}
		cmdVectors(path)
	case "license":
		if len(args) < 2 {
			fail(fmt.Errorf("usage: oath license <name> — evaluates the asserted terms across the dependency closure"))
		}
		pkg, err := buildExplain(st, args[1])
		if err != nil {
			fail(err)
		}
		fmt.Print(evaluateLicensing(st, args[1], pkg.depHashes).render())
	case "audit":
		mode, ref := "", ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--entry" && i+1 < len(rest):
				mode, ref = "entry", rest[i+1]
				i++
			case rest[i] == "--position" && i+1 < len(rest):
				mode, ref = "position", rest[i+1]
				i++
			default:
				ref = rest[i]
			}
		}
		if ref == "" {
			cmdAudit(st)
		} else {
			cmdAuditEntry(st, ref, mode)
		}
	case "migrate-store":
		cmdMigrateStore(storeDir)
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
	case "log", "ls":
		// Read-only listings, and the SAME view discipline as `authority` (#104):
		// a command must read the store the caller asked for, or say it cannot.
		// These silently dropped --remote and listed the local store, which is
		// undetectable from the output.
		endpoint := os.Getenv("OATH_REGISTRY")
		keyFile := os.Getenv("OATH_KEY")
		kmsKey := os.Getenv("OATH_KMS_KEY")
		forceLocal := false
		filter := ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--remote" && i+1 < len(rest):
				endpoint = rest[i+1]
				i++
			case rest[i] == "--kms-key" && i+1 < len(rest):
				kmsKey = rest[i+1]
				i++
			case rest[i] == "--key" && i+1 < len(rest):
				keyFile = rest[i+1]
				i++
			case rest[i] == "--local":
				forceLocal = true
			default:
				filter = rest[i]
			}
		}
		if cfg, _, cerr := loadClientConfig(); cerr == nil && cfg != nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" {
				keyFile = cfg.Key
			}
		}
		view, verr := resolveStoreView(endpoint, keyFile, kmsKey, forceLocal)
		if verr != nil {
			fail(verr)
		}
		if view.Endpoint != "" {
			tool, toolArgs := "ls", map[string]any{}
			if args[0] == "log" {
				tool, toolArgs = "log", map[string]any{"name": filter}
			}
			out, rerr := remoteText(context.Background(), view.Endpoint, view.Signer, tool, toolArgs)
			if rerr != nil {
				fail(fmt.Errorf("reading %s from %s: %w", tool, view.Endpoint, rerr))
			}
			fmt.Print(out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Println()
			}
			fmt.Printf("  view: %s\n", view.Label)
			return
		}
		if args[0] == "log" {
			cmdLog(st, filter)
		} else {
			cmdLs(st)
		}
		fmt.Printf("  view: %s\n", view.Label)
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
	case "get":
		if len(args) < 2 {
			fail(fmt.Errorf("usage: oath get <name> | oath get stdlib/<member> [--where]"))
		}
		// stdlib/<member> is a CURATED reference, not a registry name. It resolves
		// through the index to wherever the member actually lives, so a consumer
		// never has to know whether it was republished by the project or selected
		// from someone else's publication.
		name := args[1]
		if strings.HasPrefix(name, stdlibPrefix) {
			r, rerr := resolveStdlibIn(st, name)
			if rerr != nil {
				fail(rerr)
			}
			if len(args) > 2 && args[2] == "--where" {
				fmt.Print(renderResolution(r))
				return
			}
			name = r.Target
		}
		cmdGet(st, name)
	case "stdlib":
		// The index as a consumer sees it: what the library offers and where each
		// member lives. Membership is shown because it is a real difference in what
		// the project is claiming — but it is shown, not required to be understood.
		if len(args) > 1 && args[1] != "ls" {
			r, rerr := resolveStdlibIn(st, stdlibPrefix+strings.TrimPrefix(args[1], stdlibPrefix))
			if rerr != nil {
				fail(rerr)
			}
			fmt.Print(renderResolution(r))
			return
		}
		ix, path, ierr := loadStdlibIndex()
		if ierr != nil {
			fail(ierr)
		}
		fmt.Printf("THE OATH STANDARD LIBRARY  (index: %s)\n\n", path)
		for _, d := range ix.Definitions {
			if !d.Export {
				continue
			}
			r, rerr := resolveStdlibIn(st, stdlibPrefix+d.Name)
			where := r.Target
			if rerr != nil {
				where = "(unresolvable here)"
			}
			mode := "referenced"
			if d.Membership == "project-publication" {
				mode = "published"
			}
			fmt.Printf("  stdlib/%-22s %-10s → %s\n", d.Name, mode, where)
		}
		fmt.Printf("\n  `published` members carry the project's licence assertion.\n")
		fmt.Printf("  `referenced` members are RECOMMENDED, not republished — their terms\n")
		fmt.Printf("  are their publisher's. `oath stdlib <name>` shows the provenance.\n")
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
	case "explain":
		// The decision package (#74): everything an agent needs to CHOOSE,
		// including the reasons not to.
		rest := args[1:]
		asJSON := false
		var nm string
		for _, a := range rest {
			if a == "--json" {
				asJSON = true
			} else {
				nm = a
			}
		}
		if nm == "" {
			fail(fmt.Errorf("usage: oath explain <name> [--json]"))
		}
		cmdExplain(st, nm, asJSON)
	case "demand":
		// What agents looked for and did not find (#75).
		cmdDemand(st, len(args) > 1 && args[1] == "--all")
	case "scorable":
		// Every definition the mutation engine can score: a func with at least
		// one property. Exists so `make mutate` can be driven from the STORE
		// rather than a hand-maintained list. Hand lists of corpus members rot
		// silently — the same drift left rat/convert/circle out of `make verify`
		// entirely, and left 40 definitions unscored here — and a list that must
		// be kept in sync with content by discipline eventually will not be.
		cmdScorable(st)
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
	case "hint":
		rest := args[1:]
		var out string
		var err error
		switch {
		case len(rest) >= 1 && rest[0] == "--clear":
			if len(rest) < 2 || len(rest) > 3 {
				fail(fmt.Errorf("usage: oath hint --clear <name> [<prop>]"))
			}
			propSpec := ""
			if len(rest) == 3 {
				propSpec = rest[2]
			}
			out, err = apiHintClear(st, rest[1], propSpec)
		case len(rest) == 1:
			out, err = apiHintList(st, rest[0])
		case len(rest) == 3:
			out, err = apiHint(st, rest[0], rest[1], rest[2])
		default:
			fail(fmt.Errorf("usage: oath hint <name> <prop> <lemma>[.<prop>]  |  oath hint <name>  |  oath hint --clear <name> [<prop>]"))
		}
		if err != nil {
			fail(err)
		}
		fmt.Print(out)
	case "serve":
		httpAddr, tokensPath, authKeysPath := "", "", os.Getenv("OATH_AUTHORIZED_KEYS")
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--http" && i+1 < len(rest):
				httpAddr = rest[i+1]
				i++
			case rest[i] == "--tokens" && i+1 < len(rest):
				tokensPath = rest[i+1]
				i++
			case rest[i] == "--authorized-keys" && i+1 < len(rest):
				authKeysPath = rest[i+1]
				i++
			default:
				fail(fmt.Errorf("usage: oath serve [--http addr --tokens file --authorized-keys file]"))
			}
		}
		if httpAddr != "" {
			cmdServeHTTP(st, httpAddr, tokensPath, authKeysPath)
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
	// The identity of THIS PUBLICATION, distinct from the artifact's identity above.
	// An artifact hash says what was published; these say which publication of it.
	// JournalEntry is the durable address (a digest over the exact canonical journal
	// line, so it survives copying); JournalPosition is the human-facing ordinal.
	JournalPosition int    `json:"journal_position,omitempty"`
	JournalEntry    string `json:"journal_entry,omitempty"`
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

// cmdMigrateStore copies the filesystem store at OATH_STORE into the cloud
// backend (GCS objects + Postgres index), byte-preserving the journal so its
// chain still verifies, then verifies the destination. Run with the fs store as
// OATH_STORE, the destination as OATH_OBJECT_BUCKET + OATH_DB_DSN, and
// OATH_BACKEND UNSET (the source is always the filesystem). Requires the cloud
// build (`-tags cloud`). See docs/store-drivers.md.
func cmdMigrateStore(srcRoot string) {
	if cloudBackendOpener == nil {
		fail(fmt.Errorf("migrate-store needs the cloud driver: rebuild with -tags cloud"))
	}
	src, err := openFSBackend(srcRoot)
	if err != nil {
		fail(err)
	}
	dst, label, err := cloudBackendOpener()
	if err != nil {
		fail(err)
	}
	fmt.Printf("migrating filesystem store %s → %s\n", srcRoot, label)
	n, err := migrateBackend(src, dst)
	if err != nil {
		fail(fmt.Errorf("migration failed: %w", err))
	}
	// The migrated journal must reconstruct with an intact hash chain, or the
	// audit trail did not survive — refuse to call it done otherwise.
	dstStore, err := newStoreWithBackend(dst, label)
	if err != nil {
		fail(err)
	}
	if err := dstStore.VerifyLog(); err != nil {
		fail(fmt.Errorf("migrated journal FAILS verification (audit trail not intact): %w", err))
	}
	fmt.Printf("migrated %d objects; destination journal verified ✓\n", n)
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

// remoteCapable lists the commands that actually reach a registry. Maintained by
// hand rather than discovered, because adding a remote path to a command should
// be a deliberate edit here — a command silently gaining or losing the ability to
// answer from elsewhere is exactly the drift #104 is about.
var remoteCapable = map[string]bool{
	"put":      true,
	"publish":  true,
	"license":  true,
	"reserve":  true,
	"delegate": true,
	"revoke":   true,
	// Read commands that now genuinely reach a registry rather than being refused
	// for lacking a path to one. The guard below and this table must always agree
	// with reality: a command listed here without a remote path re-creates the
	// exact defect the guard exists to prevent.
	"ls":        true,
	"log":       true,
	"authority": true,
}

func remoteCapableList() string {
	names := make([]string, 0, len(remoteCapable))
	for n := range remoteCapable {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
