package main

// The signing client (#83): build a canonical publication envelope, show the
// author exactly what they are about to sign, sign it locally, and send those
// bytes unchanged.
//
// The private key never leaves this process and is never sent. What crosses the
// wire is the artifact source, the exact envelope bytes, and a signature.
//
// WHY THE ARTIFACT HASH IS COMPUTED LOCALLY. The envelope binds the hash, so the
// client must derive it before signing — which means elaborating against the LOCAL
// store. That is a feature: if the registry's elaboration disagrees (a dependency
// resolves differently there), the hashes differ and the publication is REJECTED
// rather than silently binding something the author did not sign. The cross-kernel
// determinism guarantee becomes an enforced precondition instead of an assumption.
//
// WHY CONFLICTS ARE NEVER AUTO-RESOLVED. The parent and revision are read before
// signing, so the name can move in between. The tempting behaviour is to re-read
// and re-sign transparently — and it is wrong: it would sign DIFFERENT bytes and
// authorize a DIFFERENT transition than the one displayed, turning "here is what
// you are signing" into approval of a request that changes underneath. So a
// conflict stops, reports what moved, and requires a fresh signing action.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// publishPlan is everything about a publication that can be shown BEFORE a
// signature exists — the readable summary and the exact bytes side by side, so the
// author can check that the summary describes the bytes.
type publishPlan struct {
	Name      string `json:"name"`
	Artifact  string `json:"artifact"`
	Parent    string `json:"parent"`
	ParentRev string `json:"parent_rev"`
	Author    string `json:"author"`
	Op        string `json:"op"`
	License   string `json:"license"`
	// Bytes is the exact canonical envelope that will be signed and transmitted.
	Bytes string `json:"bytes"`
}

func (p publishPlan) render() string {
	parent := p.Parent
	if parent == noParent {
		parent = "(none — first publication of this name)"
	}
	return fmt.Sprintf(`ABOUT TO SIGN
  operation   %s
  name        %s
  artifact    %s
  replacing   %s
  revision    %s
  as          %s
  license     %s  (your ASSERTION; nothing evaluates it)

EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):
%s`, p.Op, p.Name, p.Artifact, parent, p.ParentRev, p.Author, p.License, indentBytes(p.Bytes))
}

func indentBytes(s string) string {
	out := ""
	for _, l := range splitLines(s) {
		out += "  | " + l + "\n"
	}
	return out
}

// buildPublishPlan derives the envelope for publishing src, resolving parent and
// revision from the REMOTE registry's journal (the authority on what is currently
// bound) while hashing against the LOCAL store.
//
// NAMESPACE IS A PUBLICATION-TIME DECISION, applied as a PREFIX to the name the
// source declares. `reverse` published under `alice` becomes `alice/reverse`, and
// the source stays clean — a definition that had to declare its own prefix would
// carry it through every self-reference, which is how writing
// `(sandbox/transfer-example x)` inside its own property came about.
//
// A PREFIX RATHER THAN AN OVERRIDE. `--name` was the obvious flag and is the wrong
// one: it creates two sources of truth for the name, the source saying one thing
// and the flag another, with nothing to say which wins. A prefix COMPOSES — there
// is exactly one declared name, and the namespace says where it goes.
//
// Identity is untouched either way: the name is metadata, and the artifact hash is
// the same definition wherever it is bound.
func buildPublishPlan(local *Store, endpoint, pubHex, src, license, namespace string) (publishPlan, pubEnvelope, error) {
	var zero publishPlan
	forms, err := parseForms(src)
	if err != nil {
		return zero, pubEnvelope{}, err
	}
	if len(forms) != 1 {
		return zero, pubEnvelope{}, fmt.Errorf("signed publication takes exactly one definition per envelope, found %d: a single signature must not cover several independent name transitions", len(forms))
	}
	def, meta, err := elabForm(local, forms[0])
	if err != nil {
		return zero, pubEnvelope{}, err
	}
	qualified, nerr := applyNamespace(meta.Name, namespace)
	if nerr != nil {
		return zero, pubEnvelope{}, nerr
	}
	meta.Name = qualified

	// TYPECHECK BEFORE HASHING. checkDef MUTATES the definition — it resolves and
	// normalises types in place — so identity is the hash of the TYPECHECKED AST,
	// not the merely elaborated one. apiPut runs checkDef before storing, so a
	// client that hashes first signs an artifact the server will never produce.
	//
	// That is #101: `singleton` hashed 4378f986b2dc before checkDef and
	// 14b8a3dd9719 after, and the registry correctly refused the publication
	// because the signed artifact did not describe the submitted content.
	//
	// The comment on elabForm claimed the client "derives byte-identical identity
	// to the server for the same source". It shared the elaboration functions and
	// not the step after them, and nothing compared the two — which is why this
	// survived until a definition happened to be mutated by typechecking.
	if err := checkDef(local, def); err != nil {
		return zero, pubEnvelope{}, err
	}
	h := hashDef(def)

	parent, rev, err := remoteNameRevision(endpoint, meta.Name)
	if err != nil {
		return zero, pubEnvelope{}, err
	}
	if license == "" {
		license = noLicense
	}
	env := pubEnvelope{Op: "put", Name: meta.Name, Artifact: h,
		Parent: parent, ParentRev: revOf(rev), Author: pubHex, License: license}
	if err := env.validate(); err != nil {
		return zero, pubEnvelope{}, err
	}
	raw := string(envelopeEncode(env))
	return publishPlan{Name: env.Name, Artifact: env.Artifact, Parent: env.Parent,
		ParentRev: env.ParentRev.String(), Author: env.Author, Op: env.Op,
		License: env.License, Bytes: raw}, env, nil
}

// elabForm dispatches a top-level form the same way apiPut does, so the client
// derives byte-identical identity to the server for the same source.
func elabForm(st *Store, f sx) (*Def, *Meta, error) {
	if f.K != "list" || len(f.Kids) == 0 || f.Kids[0].K != "sym" {
		return nil, nil, fmt.Errorf("line %d: top-level forms must be (data ...) or (defn ...)", f.Line)
	}
	switch f.Kids[0].Sym {
	case "data":
		return elabData(st, f)
	case "defn":
		return elabFunc(st, f)
	}
	return nil, nil, fmt.Errorf("line %d: unknown top-level form %q", f.Line, f.Kids[0].Sym)
}

// multiDefinition reports whether src declares more than one top-level form. It is
// the signal that a bare `publish` (no namespace) should take the batch path
// rather than the one-definition-per-envelope path (friction item 5). A source
// that does not parse is NOT treated as a batch — the direct path re-parses it and
// reports the error in context.
func multiDefinition(src string) bool {
	forms, err := parseForms(src)
	return err == nil && len(forms) > 1
}

// cmdPublish is the signing publication path.
//
// dryRun stops after showing the plan, so an author (or a script) can inspect the
// exact bytes without a key ever being used. jsonOut emits the plan
// machine-readably for a caller that wants to check the bytes programmatically.
func cmdPublish(local *Store, endpoint, keyPath, kmsKey, file, license, namespace string, dryRun, jsonOut, assumeYes bool) {
	src, err := os.ReadFile(file)
	if err != nil {
		fail(err)
	}
	// Signer selection happens HERE, in CLI parsing, and nowhere below. Publication
	// never inspects the signer's type, never reads a key file, and has no fallback
	// path — it asks the interface for a public key and for a signature over bytes
	// it constructed once.
	signer, serr := resolveSigner(keyPath, kmsKey)
	if serr != nil {
		fail(serr)
	}
	ctx := context.Background()
	pubRaw, perr := signer.PublicKey(ctx)
	if perr != nil {
		fail(perr)
	}
	pubHex := hex.EncodeToString(pubRaw)
	clientSigner, clientPub = signer, pubHex

	// A --namespace publish is NOT a display prefix: binding X/name is a source
	// TRANSFORMATION the server must see, because it re-elaborates the source and
	// derives the name from it. So the namespaced path qualifies the source (the
	// declared name AND every intra-batch reference), publishes each definition as
	// its own signed envelope in dependency order, and seeds client elaboration
	// from the definitions already processed. #185.
	if namespace != "" {
		cmdPublishClosure(ctx, signer, pubHex, local, endpoint, string(src), license, namespace, dryRun, jsonOut, assumeYes)
		return
	}

	// A multi-definition file with NO namespace is a closure of new BARE names.
	// Route it through the same batch path the namespaced case uses — topo order,
	// one signed envelope per name (the one-signature-per-transition rule is
	// preserved, not bypassed), confirm-and-bind before each dependent — with an
	// identity transformation, so nothing is qualified. A single definition keeps
	// the direct path below. (friction item 5)
	if multiDefinition(string(src)) {
		cmdPublishClosure(ctx, signer, pubHex, local, endpoint, string(src), license, "", dryRun, jsonOut, assumeYes)
		return
	}

	plan, _, err := buildPublishPlan(local, endpoint, pubHex, string(src), license, namespace)
	if err != nil {
		fail(err)
	}

	if jsonOut {
		b, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Println(plan.render())
	}
	if dryRun {
		// The signer is part of what a reviewer must see. A plan that shows WHAT
		// would be signed but not WHO would sign it cannot be checked against the
		// authority a namespace is bound to.
		fmt.Printf("\nSIGNER: %s\n", signer.Description())
		fmt.Println("\n--dry-run: nothing signed, nothing sent.")
		return
	}
	if !assumeYes && !jsonOut {
		if !confirm("Sign and publish these exact bytes?") {
			fail(fmt.Errorf("aborted before signing"))
		}
	}

	if err := finalizePublish(ctx, signer, string(src), plan, endpoint, jsonOut); err != nil {
		fail(err)
	}
}

// finalizePublish signs the plan's EXACT bytes, transmits them unchanged, and
// verifies the registry persisted the same bytes. `source` is the definition text
// the server re-elaborates. It returns an error rather than calling fail() so a
// closure publish can stop cleanly mid-batch; the single-def path turns that error
// into fail(). The bytes signed are plan.Bytes, not a re-encoding of the envelope:
// a re-encoding that differed by one byte would sign something the registry never
// sees.
func finalizePublish(ctx context.Context, signer Signer, source string, plan publishPlan, endpoint string, jsonOut bool) error {
	sig, err := signStatement(ctx, signer, []byte(plan.Bytes), plan.Author, jsonOut)
	if err != nil {
		return err
	}
	out, err := remotePutSigned(endpoint, source, plan.Bytes, sig, plan.Author)
	if err != nil {
		// A stale parent/revision is the one failure with a specific remedy, and the
		// remedy is deliberately NOT automatic — see the file comment.
		if newParent, newRev, rerr := remoteNameRevision(endpoint, plan.Name); rerr == nil &&
			(newParent != plan.Parent || fmt.Sprintf("%d", newRev) != plan.ParentRev) {
			if !jsonOut {
				fmt.Printf("\nCONFLICT: %q moved while this envelope was being prepared.\n", plan.Name)
				fmt.Printf("  signed against  parent %s (revision %s)\n", plan.Parent, plan.ParentRev)
				fmt.Printf("  now at          parent %s (revision %d)\n", newParent, newRev)
				fmt.Printf("\nThe signature is still valid for the transition it describes, which is no\n")
				fmt.Printf("longer the transition available. It has NOT been re-signed automatically:\n")
				fmt.Printf("that would sign different bytes and authorize a different state change than\n")
				fmt.Printf("the one shown above. Re-run to review the new transition and sign it.\n")
			}
			return fmt.Errorf("stale parent: publication not applied")
		}
		return err
	}
	if !jsonOut {
		fmt.Print(out)
	}

	// A BLOCKED publication still stores the object and journals the refusal — the
	// name simply does not move (docs/teamstore.md). So the persistence check below
	// would fetch the envelope of whoever legitimately holds that artifact and
	// report a mismatch, turning a clean policy refusal into what reads like
	// tampering. Surface the refusal instead: the reason the server gave is the
	// answer, and inventing an integrity alarm on top of it trains the reader to
	// distrust the one check that matters.
	if strings.Contains(out, "BLOCKED:") {
		return fmt.Errorf("publication refused by the registry; the name was not moved")
	}

	// Verify the registry persisted the exact bytes signed. Without this the client
	// takes the registry's word that the record it accepted is the statement made.
	if got, err := remoteEnvelopeOf(endpoint, plan.Artifact); err == nil {
		if got == "" {
			if !jsonOut {
				fmt.Printf("\nWARNING: the registry accepted the publication but records no author\n")
				fmt.Printf("envelope for %s. The attribution is not independently verifiable.\n", shortHash(plan.Artifact))
			}
		} else if octets, derr := decodeEnvelopeB64(got); derr != nil {
			// The registry stores envelope OCTETS base64-encoded (SPEC §8.6.3). Comparing
			// the encoded text against the signed octets always differs, which is what
			// this check did after storage moved to base64 — reporting a mismatch on
			// every honest publication. A verification that cries wolf is worse than
			// none: it trains the reader to ignore the one case that matters.
			if !jsonOut {
				fmt.Printf("\nWARNING: the registry's stored envelope does not decode: %v\n", derr)
			}
			return fmt.Errorf("persisted envelope is not canonical base64")
		} else if string(octets) != plan.Bytes {
			if !jsonOut {
				fmt.Printf("\nWARNING: the registry persisted DIFFERENT envelope bytes than were signed.\n")
				fmt.Printf("  signed:    %q\n  persisted: %q\n", plan.Bytes, string(octets))
				fmt.Printf("The signature will not verify against the stored record.\n")
			}
			return fmt.Errorf("persisted envelope differs from the signed bytes")
		} else if !jsonOut {
			fmt.Printf("\nverified: the registry persisted the exact %d bytes that were signed.\n", len(plan.Bytes))
		}
	}
	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var s string
	if _, err := fmt.Scanln(&s); err != nil {
		return false
	}
	return s == "y" || s == "Y" || s == "yes"
}

// applyNamespace prefixes a declared name with a publication-time namespace.
//
// Separated from buildPublishPlan so the rule is testable without a registry:
// the plan resolves parent and revision remotely, so exercising it needs a live
// endpoint, and a naming rule that can only be checked against production is a
// rule nobody checks.
//
// `alice` and `alice/*` mean the same thing. People type the reservation spelling
// from muscle memory, and two spellings that silently publish to different places
// is the sort of difference that is only noticed after the name is permanent.
func applyNamespace(declared, namespace string) (string, error) {
	if namespace == "" {
		return declared, nil
	}
	ns := strings.TrimSuffix(namespace, "/*")
	if strings.Contains(declared, "/") {
		return "", fmt.Errorf("the definition already declares a namespace (%q) and --namespace says %q: "+
			"a name has ONE source of truth. Remove the prefix from the source, or drop --namespace", declared, ns)
	}
	if err := validNamespacePattern(ns + "/*"); err != nil {
		return "", fmt.Errorf("--namespace %q: %w", ns, err)
	}
	return ns + "/" + declared, nil
}
