package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
  oath find --implies <f> --details   ...and NAME the candidates refuted or left without a verdict, with the evidence
  oath find --implies <f> --timeout D  ...bounded by wall-clock D (e.g. 30s); the unreached candidates report NO VERDICT
  oath find --equiv <name>            find definitions that are the SAME FUNCTION up to rewrite rules (the e-graph)
  oath context <name...> [--budget N] spec-only slice of the named defs + transitive deps (no bodies)
  oath dependents <name>              list definitions that reference a definition
  oath verify <name>                  re-run a definition's properties
  oath mutate [--prove] <name>        generated mutation score: do generated executions notice
                                      mutations? --prove additionally classifies SURVIVORS against
                                      the definition's proven properties (execution measures reach,
                                      proof measures exclusion; never averaged into one number)
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
  oath run <name> [-- args...]        interpret a (-> (List Str) Str) program on args
                                      and print its output — no build, no toolchain
  oath serve                          MCP server over stdio (tools for agent sessions)
  oath serve --http <addr> --tokens <file>
                                      team store: MCP over HTTP with authenticated principals;
                                      repoint policy in <store>/policy.json (docs/teamstore.md)
  oath build <name> [-o out] [--backend go|llvm]
                                      compile a verified definition to a native executable
                                      (entry protocol: (-> (List Str) Str); refuses falsified names)
  oath provenance <file>              read what a compiled artifact was built from, WITHOUT running it
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
	// `provenance` reads a FILE and consults no codebase, so it runs before the
	// store is opened. Opening one would make inspecting an artifact depend on —
	// and, by creating ./codebase, modify — the directory it is inspected from,
	// which is wrong for the one command meant to answer "what is this thing?"
	// about an artifact you have no context for and may not want to execute.
	if args[0] == "provenance" {
		// Dispatching early means this command never reaches the shared
		// unknown-flag guard, so it makes the same check itself rather than
		// silently reading a mistyped flag as a filename.
		if len(args) != 2 || strings.HasPrefix(args[1], "--") {
			fail(fmt.Errorf("usage: oath provenance <file>   (no flags)"))
		}
		cmdProvenance(args[1])
		return
	}
	// bridge-obligation emits SPEC §7.4's fixed scripts and never touches the
	// store. Dispatched early for the same reason as provenance: opening the
	// store CREATES ./codebase/{objects,meta} relative to the caller, so a
	// gate that merely asks the kernel for its bytes would leave directories
	// behind in whatever tree it ran from — and would fail outright on an
	// unreadable or cloud-configured store it has no business consulting.
	if args[0] == "bridge-obligation" {
		cmdBridgeObligation(args[1:])
		return
	}
	storeDir := os.Getenv("OATH_STORE")
	if storeDir == "" {
		storeDir = defaultStoreDir
	}
	st, err := OpenStore(storeDir)
	if err != nil {
		fail(err)
	}
	if fn, ok := harnessCommands[args[0]]; ok {
		fn(args[1:])
		return
	}
	// AN UNKNOWN FLAG IS NEVER IGNORED. Every command below parses the flags it
	// knows and treats anything else as a positional argument, so a mistyped or
	// imagined flag is silently absorbed. That is not hypothetical: during the
	// v0.10.0 transfer exercise `oath publish --license X --name sandbox/thing f.oath`
	// was run against the LIVE registry. `publish` has no --name; the flag and its
	// value were swallowed and the definition bound its own bare name at the top
	// level of a public, append-only namespace. The name is permanent.
	//
	// The general rule the earlier --remote guard was a special case of: a CLI that
	// accepts a flag it does not implement is asserting something false about what
	// it did.
	// ONLY for commands whose flags are catalogued. An absent entry means "not yet
	// listed", NOT "takes no flags", and conflating the two is how this guard broke
	// `serve` on its first deploy: the entrypoint runs `oath serve --http … --tokens
	// …`, no `serve` entry existed, every flag read as unknown, and the container
	// exited before binding a port. Cloud Run refused to shift traffic and the
	// registry stayed up, which is the only reason that was cheap.
	//
	// The failure mode to avoid is not "an uncatalogued command accepts a bad flag"
	// — it is a guard that breaks working commands to enforce a table nobody has
	// finished writing.
	for _, a := range args[1:] {
		if _, catalogued := knownFlags[args[0]]; !catalogued {
			break
		}
		if !strings.HasPrefix(a, "--") || knownFlags[args[0]][a] || a == "--" {
			continue
		}
		fail(fmt.Errorf("`oath %s` has no flag %q, and silently ignoring it would let this command do "+
			"something other than what you asked. Known flags for %s: %s",
			args[0], a, args[0], knownFlagList(args[0])))
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
			if args[0] == "run" && a == "--" {
				break // ONLY `run` treats `--` as a separator: everything after it is a
				// program argument, not a flag. For any other command `--remote` after a
				// `--` is still a misused location flag, and the guard must catch it.
			}
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
		allowNew := false
		var files []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--json":
				jsonMode = true
			case rest[i] == "--new":
				allowNew = true
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
		cmdPut(st, files[0], jsonMode, author, ctxHash, allowNew)
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
		// OATH_NAMESPACE, then --namespace, then the client config. The config has
		// documented `namespace` as "a local DEFAULT for names this client
		// publishes" since it was written, and nothing read it — a setting that
		// silently does nothing is worse than an absent one, because it is
		// indistinguishable from a setting that works.
		namespace := os.Getenv("OATH_NAMESPACE")
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
			case rest[i] == "--namespace" && i+1 < len(rest):
				namespace = rest[i+1]
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
		if cfg, _, cerr := loadClientConfig(); cerr == nil && cfg != nil {
			if endpoint == "" {
				endpoint = cfg.Registry
			}
			if keyFile == "" {
				keyFile = cfg.Key
			}
			if namespace == "" {
				namespace = cfg.Namespace
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
		cmdPublish(st, endpoint, keyFile, kmsKey, file, license, namespace, dryRun, jsonOut, yes)
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
		runFind(st, args[1:])
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
		adjudicate := false
		var names []string
		for _, a := range args[1:] {
			switch {
			case a == "--prove":
				adjudicate = true
			case strings.HasPrefix(a, "-"):
				// An unknown flag must not be silently accepted as a definition
				// name: `oath mutate --prov x` would otherwise score whichever
				// argument landed last and report success for a run the user did
				// not ask for.
				fail(fmt.Errorf("unknown option %q; usage: oath mutate [--prove] <name>", a))
			default:
				names = append(names, a)
			}
		}
		if len(names) != 1 {
			fail(fmt.Errorf("usage: oath mutate [--prove] <name>"))
		}
		cmdMutate(st, names[0], adjudicate)
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
		outPath, backend := "", "go"
		emitSource := false
		var names []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "-o" && i+1 < len(rest):
				outPath = rest[i+1]
				i++
			case rest[i] == "--backend" && i+1 < len(rest):
				backend = rest[i+1]
				i++
			case rest[i] == "--emit-source":
				emitSource = true
			default:
				names = append(names, rest[i])
			}
		}
		if len(names) != 1 {
			fail(fmt.Errorf("usage: oath build <name> [-o out] [--backend go|llvm] [--emit-source]"))
		}
		if backend != "go" && backend != "llvm" {
			fail(fmt.Errorf("unknown backend %q (go, llvm)", backend))
		}
		cmdBuild(st, names[0], outPath, backend, emitSource)
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
		text := false
		var exprs []string
		for _, a := range args[1:] {
			if a == "--text" {
				text = true
			} else {
				exprs = append(exprs, a)
			}
		}
		if len(exprs) != 1 {
			fail(fmt.Errorf("usage: oath eval [--text] \"<expr>\""))
		}
		cmdEval(st, exprs[0], text)
	case "run":
		rest := args[1:]
		if len(rest) == 0 {
			fail(fmt.Errorf("usage: oath run <name> [-- args...]"))
		}
		name := rest[0]
		// Split on the first `--`: everything after it is a program argument, even
		// if it begins with '-'. BEFORE it, a `--`-prefixed token is a mistyped oath
		// option, not a silently-swallowed program argument — reject it so a typo
		// cannot quietly change what runs. (`oath run` has no flags of its own.)
		var progArgs []string
		seenSep := false
		for _, a := range rest[1:] {
			if !seenSep && a == "--" {
				seenSep = true
				continue
			}
			if !seenSep && strings.HasPrefix(a, "--") {
				fail(fmt.Errorf("`oath run` has no flag %q; pass a program argument that begins with '-' after `--` (oath run %s -- %s ...)", a, name, a))
			}
			progArgs = append(progArgs, a)
		}
		cmdRun(st, name, progArgs)
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

// propJSON is a WIRE FORMAT — `put --json`, the WASM playground, and any
// machine consumer read it.
//
// `outcome` is the authority: passed | falsified | indeterminate. `failed` is
// RETAINED and NARROWED — it is now true only for a refutation, where before it
// was also true for a case that could not be evaluated. That is a deliberate
// semantic change and the reason `outcome` exists: a consumer reading only
// `failed` now sees an indeterminate property as not-failed, which is correct
// but less informative, so anything making a decision should read `outcome`.
type propJSON struct {
	Name           string `json:"name"`
	Passed         int    `json:"passed"`
	Indeterminate  int    `json:"indeterminate,omitempty"` // cases that could not be evaluated
	Outcome        string `json:"outcome"`
	Failed         bool   `json:"failed"` // refuted — NOT merely unevaluated
	Counterexample string `json:"counterexample,omitempty"`
	Error          string `json:"error,omitempty"`
	// Rendering, carried so every surface prints one vocabulary. HasDetail is
	// separate from Detail being non-empty: a nullary property refuted by the
	// empty binding has an empty counterexample and still prints its line.
	Headline    string `json:"-"`
	DetailLabel string `json:"-"`
	Detail      string `json:"-"`
	HasDetail   bool   `json:"-"`
}

// readSourceFile reads a .oath file and REFUSES bytes that are not valid UTF-8
// (SPEC §1.4, #133).
//
// The validation is here, and not only in `lex`, because not every reader
// elaborates locally: `put --remote` ships the source to a registry, and
// json.Marshal substitutes U+FFFD on the way OUT — so the server receives
// well-formed JSON and no downstream check can see that anything was replaced.
// The lossy step is `[]byte -> string`, which is why the guard belongs at the
// read rather than at any one consumer.
//
// Stated as the thing that kept being wrong: `lex` is the single entry to the
// LANGUAGE, but this is the single entry to a source FILE, and they are not the
// same boundary. Three separate paths reached identity through the second one.
func readSourceFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	if !utf8.Valid(b) {
		fail(fmt.Errorf("%s is not valid UTF-8: a string literal cannot hold arbitrary bytes, "+
			"and substituting U+FFFD would make distinct sources publish as one definition", path))
	}
	return string(b)
}

func cmdPut(st *Store, path string, jsonMode bool, author string, ctxHash string, allowNew bool) {
	src := readSourceFile(path)
	if err := guardNewNames(st, src, allowNew); err != nil {
		fail(err)
	}
	results, perr := apiPut(st, src, author, ctxHash)
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
	src := readSourceFile(path)
	out, err := apiFindSpec(st, src)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func cmdFindImplies(st *Store, path string, mode findImpliesMode, budget time.Duration) {
	src := readSourceFile(path)
	// Progress goes to stderr, and ONLY when stderr is a terminal: a piped or
	// captured run (a test, a script, `2>&1` into a file) gets the report on
	// stdout and nothing else, so the carriage-return line never lands in output.
	var progress io.Writer
	if isTerminalFile(os.Stderr) {
		progress = os.Stderr
	}
	out, err := apiFindImpliesOpts(st, src, mode, budget, progress)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

// isTerminalFile reports whether f is a character device (a terminal), without a
// dependency: the default build is zero-deps, so this reads the file mode rather
// than calling into golang.org/x/term.
func isTerminalFile(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ---------------------------------------------------------------------------
// `oath find`'s argument grammar (#156).
// ---------------------------------------------------------------------------

// runFind is the whole of `oath find`: parse, then dispatch to the form's
// command. It lives outside main's switch so the path a user actually takes —
// argument text in, rendered report out — is exercisable by a test. Left inline,
// the only thing standing between `--details` and being parsed-but-ignored would
// be a one-line expression no test could reach without running the binary.
func runFind(st *Store, args []string) {
	fa, err := parseFindArgs(args)
	if err != nil {
		fail(err)
	}
	switch fa.form {
	case findFormSpec:
		cmdFindSpec(st, fa.target)
	case findFormImplies:
		cmdFindImplies(st, fa.target, fa.mode, fa.budget)
	case findFormEquiv:
		cmdFindEquiv(st, fa.target)
	default:
		cmdFind(st, fa.target)
	}
}

// findForm is which of the four queries `oath find` was asked for. They are
// mutually exclusive: each searches a different surface, and a command line
// naming two has not asked for both, it has asked ambiguously.
type findForm int

const (
	findFormName findForm = iota // oath find <name>
	findFormSpec
	findFormImplies
	findFormEquiv
)

// findSelectors maps each selector flag to the form it selects, and it is the
// ONLY place a form's spelling is written down: parseFindArgs recognises
// selectors from it, and knownFlags["find"] is DERIVED from it. Two hand-written
// lists could disagree about which flags `find` accepts, and the disagreement
// would be invisible from inside either — the pre-dispatch guard would refuse a
// flag the parser handles, or catalogue one it does not.
var findSelectors = map[string]findForm{
	"--spec":    findFormSpec,
	"--implies": findFormImplies,
	"--equiv":   findFormEquiv,
}

// findDetailsFlag selects the detailed report from apiFindImplies: the
// candidates that were REFUTED or left without a verdict, named, with their
// evidence. Summary stays the default because the ANSWER is what proved, and on
// a large store naming every miss buries it.
const findDetailsFlag = "--details"

// findTimeoutFlag bounds --implies by WALL-CLOCK. It is a --implies-only control
// (like --details): the other three searches attempt no proof, so a budget on
// them would be a flag that does nothing. Its value is a Go duration ("30s",
// "2m"). See apiFindImpliesOpts for why a budget truncates the candidate set and
// never a proof.
const findTimeoutFlag = "--timeout"

const findUsage = "usage: oath find <name> | --spec <file> | --implies <file> [--details] [--timeout <dur>] | --equiv <name>"

// findArgs is a parsed `oath find` command line.
type findArgs struct {
	form   findForm
	target string
	mode   findImpliesMode
	budget time.Duration // --implies wall-clock ceiling; 0 = unbounded
}

// parseFindArgs is `oath find`'s grammar as a TOTAL FUNCTION returning an error
// rather than exiting, so every refusal is assertable directly instead of only
// through a process that died.
//
// WHAT IT REFUSES, and why each is a refusal rather than a default:
//
//   - two selectors. `--spec f --implies g` is not a request for both; picking
//     either silently answers a question that was not asked.
//   - a selector with no value, or a value that is itself a flag. `find
//     --implies --details q.oath` would otherwise read `--details` as the
//     FILENAME and fail later with a confusing I/O error.
//   - a stray positional beside a selector — which is what `--details true`
//     degrades to, since --details is boolean. Absorbing the `true` would let
//     `--details false` turn detail ON.
//   - --details twice. A repeated boolean is harmless to obey and evidence that
//     the caller believes something the flag does not do.
//   - --details on any form but --implies. The other three have no unproven
//     residue to report, so accepting it there would be accepting a flag that
//     does nothing, which is the class of defect knownFlags exists to prevent.
//
// The unknown-flag branch duplicates the pre-dispatch knownFlags guard, which
// normally fires first. It is kept because this function is also the parser the
// table is derived from, and a parser that silently accepted an uncatalogued
// flag would make the derivation a lie the day the two drifted.
func parseFindArgs(args []string) (findArgs, error) {
	fa := findArgs{mode: findImpliesSummary}
	selector, details, timeoutSet := "", false, false
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case findSelectorOf(a) != nil:
			if selector != "" {
				return fa, fmt.Errorf("`oath find` answers ONE query at a time, and %s and %s are two different "+
					"searches. Run them separately. %s", selector, a, findUsage)
			}
			if i+1 >= len(args) {
				return fa, fmt.Errorf("%s needs a value and none followed it. %s", a, findUsage)
			}
			if strings.HasPrefix(args[i+1], "--") {
				return fa, fmt.Errorf("%s needs a value, but %q is a flag — as written, %s would be read as the "+
					"thing to search for. %s", a, args[i+1], args[i+1], findUsage)
			}
			selector, fa.form, fa.target = a, *findSelectorOf(a), args[i+1]
			i++
		case a == findDetailsFlag:
			if details {
				return fa, fmt.Errorf("%s was given twice. It is a switch, so the repeat asks for something it "+
					"cannot do. %s", findDetailsFlag, findUsage)
			}
			details = true
		case a == findTimeoutFlag:
			if timeoutSet {
				return fa, fmt.Errorf("%s was given twice. %s", findTimeoutFlag, findUsage)
			}
			if i+1 >= len(args) {
				return fa, fmt.Errorf("%s needs a duration and none followed it (e.g. %s 30s). %s",
					findTimeoutFlag, findTimeoutFlag, findUsage)
			}
			if strings.HasPrefix(args[i+1], "--") {
				return fa, fmt.Errorf("%s needs a duration, but %q is a flag. %s", findTimeoutFlag, args[i+1], findUsage)
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fa, fmt.Errorf("%s %q is not a duration (try 30s, 2m): %v. %s", findTimeoutFlag, args[i+1], err, findUsage)
			}
			if d <= 0 {
				return fa, fmt.Errorf("%s must be a positive duration, got %q. %s", findTimeoutFlag, args[i+1], findUsage)
			}
			timeoutSet, fa.budget = true, d
			i++
		case strings.HasPrefix(a, "--"):
			return fa, fmt.Errorf("`oath find` has no flag %q, and silently ignoring it would let this command "+
				"do something other than what you asked. %s", a, findUsage)
		default:
			positionals = append(positionals, a)
		}
	}
	if selector == "" {
		if len(positionals) != 1 {
			return fa, fmt.Errorf(findUsage)
		}
		fa.form, fa.target = findFormName, positionals[0]
	} else if len(positionals) != 0 {
		return fa, fmt.Errorf("`oath find %s %s` takes no further arguments, and %q was left over — "+
			"%s is a switch and takes no value. %s", selector, fa.target, positionals[0], findDetailsFlag, findUsage)
	}
	if details {
		if fa.form != findFormImplies {
			return fa, fmt.Errorf("%s belongs to the --implies form only: it reports the candidates a PROOF "+
				"refuted or left unsettled, and the other searches never attempt one. %s",
				findDetailsFlag, findUsage)
		}
		fa.mode = findImpliesDetailed
	}
	if timeoutSet && fa.form != findFormImplies {
		return fa, fmt.Errorf("%s belongs to the --implies form only: it bounds the PROOF search, and the "+
			"other searches never run one. %s", findTimeoutFlag, findUsage)
	}
	return fa, nil
}

// findSelectorOf returns the form a token selects, or nil if it is not a
// selector. A pointer so "not a selector" is distinguishable from findFormName,
// which is the zero value.
func findSelectorOf(a string) *findForm {
	if f, ok := findSelectors[a]; ok {
		return &f
	}
	return nil
}

// findKnownFlags is knownFlags["find"], derived from the grammar above rather
// than written out beside it.
func findKnownFlags() map[string]bool {
	m := map[string]bool{findDetailsFlag: true, findTimeoutFlag: true}
	for f := range findSelectors {
		m[f] = true
	}
	return m
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

// cmdRun interprets a pure (-> (List Str) Str) program on its arguments and prints
// the output as text, with no build and no toolchain — the interpreted counterpart
// of `oath build` + run. It reuses the eval pipeline: the arguments become a
// (List Str) literal exactly as a compiled program would receive them (each a Str,
// UTF-8 required), the entry is applied to that list, and the resulting Str is
// printed raw — the same disposition the compiled shapeCLI main uses (fmt.Println).
// Programs that take capabilities, a request, or return a Result are refused: those
// need the host provisioning only a real build gives them.
func cmdRun(st *Store, name string, progArgs []string) {
	out, err := runProgram(st, name, progArgs)
	if err != nil {
		fail(err)
	}
	fmt.Println(out) // the compiled shapeCLI main prints its Str result with Println too
}

// runProgram is cmdRun without the process effects: it returns the program's
// output rather than printing it, so it is testable and so cmdRun stays a thin
// shell. See cmdRun for the semantics.
func runProgram(st *Store, name string, progArgs []string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	if d.K != "func" {
		return "", fmt.Errorf("%s is not a function; `oath run` needs a program entry (-> (List Str) Str)", name)
	}
	_, shape, isEntry := classifyEntry(st, d.Ty)
	if !isEntry {
		return "", fmt.Errorf("%s : %s is not a program entry", name, debugTy(d.Ty))
	}
	if shape != shapeCLI {
		return "", fmt.Errorf("`oath run` interprets a pure (-> (List Str) Str) program; %s takes capabilities, a request, or returns a Result — build and run it with `oath build`", name)
	}
	// Take the List and Str types FROM the entry (-> (List Str) Str), so the
	// argument value is built with the exact datatypes the program expects.
	if d.Ty == nil || d.Ty.A == nil || d.Ty.A.K != "data" || len(d.Ty.A.Args) != 1 {
		return "", fmt.Errorf("internal: %s does not have a (List Str) argument", name)
	}
	listHash, strHash := d.Ty.A.Hash, d.Ty.A.Args[0].Hash
	// Build the (List Str) exactly as a compiled program would: a non-UTF-8
	// argument has no Str value (oathStrFromHost refuses it rather than substitute
	// U+FFFD), so refuse here too instead of running something the binary rejects.
	for i, a := range progArgs {
		if !utf8.ValidString(a) {
			return "", fmt.Errorf("command-line argument %d is not valid UTF-8, so it has no Str value; a compiled program would refuse it too", i+1)
		}
	}
	sd := newStrDecoder(st)
	if sd == nil || sd.hash != strHash {
		return "", fmt.Errorf("the store's active Str is not %s's argument type; cannot build its arguments", name)
	}
	listVal, err := buildArgList(st, sd, listHash, progArgs)
	if err != nil {
		return "", err
	}
	// Evaluate the RESOLVED definition to its closure — the same way the evaluator's
	// "ref" case does — and apply it to the argument list. Evaluating d.Body rather
	// than re-parsing `name` matters: an entry legally named `true` or `false` would
	// reparse as a Bool literal. Building the value and applying directly (rather
	// than re-lexing a source literal) also keeps run independent of constructor
	// NAMES, so it works even when List/Str are bound under a same-hash alias.
	// execFuel, not propFuel: this is EXECUTION, not verification, so the budget is
	// an execution budget. The interpreter is still bounded (fuel + recursion
	// depth) where a compiled program is not, so a resource limit points at build.
	ev := &evaluator{st: st, fuel: execFuel}
	fnVal, err := ev.eval(nil, h, d.Body)
	if err != nil {
		return "", err
	}
	out, err := ev.apply(fnVal, listVal)
	if err != nil {
		if strings.Contains(err.Error(), "non-termination") {
			return "", fmt.Errorf("%s: %w — `oath run` interprets within a bound; if the program is correct but long-running or deeply recursive, `oath build` runs it natively without that bound", name, err)
		}
		return "", err
	}
	s, ok := sd.text(out)
	if !ok {
		return "", fmt.Errorf("%s produced a Str this run cannot render (a non-scalar codepoint?); a compiled program would refuse it too", name)
	}
	return s, nil
}

// buildArgList constructs a (List Str) value from the command-line arguments,
// resolving the list's Nil/Cons constructor INDICES by shape (arity 0 and 2) so it
// does not depend on their names. Each argument becomes a Str via the decoder's
// encode.
func buildArgList(st *Store, sd *strDecoder, listHash string, args []string) (Value, error) {
	d, err := st.GetDef(listHash)
	if err != nil || d.K != "data" || len(d.Ctors) != 2 {
		return Value{}, fmt.Errorf("the entry's argument is not a two-constructor list")
	}
	nilIdx, consIdx := -1, -1
	for i, f := range d.Ctors {
		switch len(f) {
		case 0:
			nilIdx = i
		case 2:
			consIdx = i
		default:
			return Value{}, fmt.Errorf("the entry's list has an unexpected constructor shape")
		}
	}
	if nilIdx < 0 || consIdx < 0 {
		return Value{}, fmt.Errorf("the entry's list is not Nil/Cons-shaped")
	}
	out := Value{K: "data", Hash: listHash, Idx: nilIdx}
	for i := len(args) - 1; i >= 0; i-- {
		out = Value{K: "data", Hash: listHash, Idx: consIdx, Fields: []Value{sd.encode(args[i]), out}}
	}
	return out, nil
}

func cmdEval(st *Store, src string, text bool) {
	// The DEFAULT output is structural — a Str as its SCons/SNil tower — because it
	// is a parsed contract: the differential-test oracle, the LLVM acceptance
	// script's decoder, and the MCP tool all read it. --text opts into the
	// human-readable rendering (a Str as "cat"; see evalDisplay/printValueEval),
	// which is not machine-parsed.
	render := apiEval
	if text {
		render = evalDisplay
	}
	out, err := render(st, src)
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

// knownFlags is what each command actually parses. An unknown flag is refused
// rather than absorbed as a positional argument (see the guard in main).
//
// A TABLE RATHER THAN INTROSPECTION, for the same reason remoteCapable is one: the
// parsers are hand-written switches, so there is nothing to introspect, and a
// table plus a test is what makes gaining or losing a flag a deliberate edit.
// defaultStoreDir is the repository's canonical corpus: git-tracked, with an
// append-only journal. Named rather than inlined because guardNewNames must
// recognise it by identity however the caller spelled it.
const defaultStoreDir = "codebase"

var knownFlags = map[string]map[string]bool{
	"publish":   set("--remote", "--key", "--kms-key", "--license", "--namespace", "--dry-run", "--json", "--yes", "-y"),
	"put":       set("--remote", "--key", "--author", "--context", "--json", "--new"),
	"reserve":   set("--remote", "--key", "--kms-key", "--dry-run", "--yes", "-y"),
	"delegate":  set("--remote", "--key", "--kms-key", "--to", "--dry-run", "--yes", "-y"),
	"revoke":    set("--remote", "--key", "--kms-key", "--from", "--dry-run", "--yes", "-y"),
	"transfer":  set("--remote", "--key", "--kms-key", "--recipient-key", "--to", "--dry-run", "--yes", "-y"),
	"authority": set("--remote", "--key", "--kms-key"),
	"ls":        set("--remote", "--key", "--kms-key", "--local"),
	"log":       set("--remote", "--key", "--kms-key", "--local"),
	"plugin":    set("--codex", "--claude-code", "--user", "--dir", "--registry", "--dry-run"),
	"build":     set("--backend", "--emit-source"),
	"eval":      set("--text"),
	// DERIVED from the grammar in parseFindArgs, not restated beside it: the
	// guard above and the parser must agree about which flags `find` accepts, and
	// a hand-copied list is correct exactly once.
	"find": findKnownFlags(),
}

func set(flags ...string) map[string]bool {
	m := make(map[string]bool, len(flags))
	for _, f := range flags {
		m[f] = true
	}
	return m
}

func knownFlagList(cmd string) string {
	fs := knownFlags[cmd]
	if len(fs) == 0 {
		return "(none)"
	}
	out := make([]string, 0, len(fs))
	for f := range fs {
		out = append(out, f)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// guardNewNames refuses to BIND A NEW NAME unless the caller asked for it.
//
// A name is permanent. The journal is append-only, there is no unbind, and a
// repoint supersedes a binding without removing it — so creating a name is the
// only irreversible thing `put` does. Re-putting an EXISTING name is not
// guarded: it is what `make verify` does on every run, and it cannot introduce
// a name that was not already chosen deliberately.
//
// WHY THIS EXISTS AS CODE AND NOT AS A RULE. The naming rules in CLAUDE.md were
// written after two exercise artifacts were published into `oath/*`, and they
// were written as ADVICE because the only correction available for an
// already-bound name is documentation. Advice does not fire while you are
// concentrating on something else: this guard was built after a session bound
// four throwaway definitions into the committed store while carefully debugging
// an unrelated typechecker question. `storeDir` defaults to `codebase`, which is
// git-tracked and journal-append-only, so a bare `oath put` on a scratch file
// reaches the canonical corpus with no flag, warning, or confirmation.
//
// It guards the ACT, not the SPELLING. A check on name shape would have caught
// `polyspine50` and waved through `spine` — identical pollution, identical
// journal entries, no warning. What distinguishes a probe from a publication is
// not what it is called; it is whether anyone decided to create it.
//
// CLI-ONLY BY DESIGN. apiPut is also reached by the MCP server, the registry and
// the playground, which have their own authority models; publication policy
// belongs at the human interface where the mistake happens, not in the kernel.
func guardNewNames(st *Store, src string, allowNew bool) error {
	if allowNew {
		return nil
	}
	// ONLY THE CANONICAL STORE IS GUARDED, and that is the principle rather than
	// a concession: the hazard is a CASUAL write reaching the canonical corpus.
	// Guarding every store instead broke CI immediately, because publishing into
	// a FRESH store makes every name new by definition —
	// scripts/check-stdlib-manifest.py republishes 52 artifacts into a temp dir
	// and `oathrs/conformance.sh` rebuilds the corpus from empty. In an empty
	// store a new name is a RECONSTRUCTION, not a publication decision, so the
	// premise this guard rests on does not hold there.
	//
	// KEYED ON THE STORE'S IDENTITY, NOT ON HOW IT WAS SELECTED. An earlier
	// version exempted any run with OATH_STORE set, treating the variable as
	// evidence of intent. That is sound while it is typed per-command and false
	// the moment it is exported: `export OATH_STORE=codebase` in a shell profile
	// would have silently disabled this guard, permanently and invisibly, for
	// the exact store it exists to protect. Comparing resolved paths closes
	// that, because an explicitly-named canonical store is still the canonical
	// store.
	//
	// ITS WIDTH, STATED SO IT IS NOT OVERREAD: this prevents accidental creation
	// in the repository's canonical store. It does NOT claim to detect whether
	// some other explicitly configured store was chosen thoughtfully — a wrapper
	// pointing OATH_STORE at a second long-lived corpus gets no protection here.
	// That is a real limit, and still worth far more than inferring publication
	// intent from a name.
	if !isCanonicalStore(st.Root) {
		return nil
	}
	forms, err := parseForms(src)
	if err != nil {
		return nil // let apiPut report the real parse error, with its line number
	}
	var fresh []string
	for _, f := range forms {
		if len(f.Kids) < 2 || f.Kids[0].K != "sym" || f.Kids[1].K != "sym" {
			continue
		}
		switch f.Kids[0].Sym {
		case "data", "defn":
			if name := f.Kids[1].Sym; name != "" {
				if _, exists := st.Resolve(name); !exists {
					fresh = append(fresh, name)
				}
			}
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to bind %d new name(s) without --new: %s\n"+
		"  A name is PERMANENT: the journal is append-only, there is no unbind, and\n"+
		"  repointing supersedes a binding rather than removing it.\n"+
		"  If you meant to publish, pass --new.\n"+
		"  If this is exploratory, use a disposable store:\n"+
		"      OATH_STORE=$(mktemp -d) oath put %s --new",
		len(fresh), strings.Join(fresh, ", "), "<file>")
}

// isCanonicalStore reports whether root IS the repository's canonical corpus:
// the git-tracked, append-only-journal store `oath` writes to by default.
//
// IDENTITY IS ANCHORED TO THE REPOSITORY, NOT TO THE PROCESS CWD, and the
// difference is a real bypass rather than a nicety. An earlier version compared
// against filepath.Abs(defaultStoreDir), which resolves "codebase" relative to
// wherever the command happened to run — so `cd docs && OATH_STORE=../codebase
// oath put file.oath` did NOT recognise the real store and bound a permanent
// name with no warning, while an unrelated `codebase` directory elsewhere WAS
// treated as canonical. Reproduced before this fix. Found by external review.
//
// The owning artifact is the repository: a store is canonical when it is named
// `codebase` at the root of a git working tree. That is true from any working
// directory and by any spelling — relative, absolute, symlinked, or reached
// through a parent — because it is a property of the store rather than of how
// the caller referred to it.
//
// Fails CLOSED on an unreadable path (returns false) but note the asymmetry: a
// false negative here skips a warning, while a false positive only demands an
// explicit --new. Recognising one extra git-tracked `codebase` is the cheaper
// error, so the check does not additionally require the path to be TRACKED.
func isCanonicalStore(root string) bool {
	if root == "" {
		return false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if filepath.Base(abs) != defaultStoreDir {
		return false
	}
	// `.git` is a directory in a normal clone and a FILE in a worktree, so
	// Stat rather than a directory check — the port's own worktrees would
	// otherwise not be recognised.
	if _, err := os.Stat(filepath.Join(filepath.Dir(abs), ".git")); err != nil {
		return false
	}
	return true
}
