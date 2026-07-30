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
	"encoding/json"
	"fmt"
	"os"
)

// publishPlan is everything about a publication that can be shown BEFORE a
// signature exists — the readable summary and the exact bytes side by side, so the
// author can check that the summary describes the bytes.
type publishPlan struct {
	Name      string `json:"name"`
	Artifact  string `json:"artifact"`
	Parent    string `json:"parent"`
	ParentRev int    `json:"parent_rev"`
	Author    string `json:"author"`
	Op        string `json:"op"`
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
  revision    %d
  as          %s

EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):
%s`, p.Op, p.Name, p.Artifact, parent, p.ParentRev, p.Author, indentBytes(p.Bytes))
}

func indentBytes(s string) string {
	out := ""
	for _, l := range splitLines(s) {
		out += "  | " + l + "\n"
	}
	return out
}

// buildPublishPlan derives the envelope for publishing src under its own declared
// name, resolving parent and revision from the REMOTE registry's journal (the
// authority on what is currently bound) while hashing against the LOCAL store.
func buildPublishPlan(local *Store, endpoint, pubHex, src string) (publishPlan, pubEnvelope, error) {
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
	h := hashDef(def)

	parent, rev, err := remoteNameRevision(endpoint, meta.Name)
	if err != nil {
		return zero, pubEnvelope{}, err
	}
	env := pubEnvelope{Op: "put", Name: meta.Name, Artifact: h,
		Parent: parent, ParentRev: rev, Author: pubHex}
	if err := env.validate(); err != nil {
		return zero, pubEnvelope{}, err
	}
	raw := string(envelopeEncode(env))
	return publishPlan{Name: env.Name, Artifact: env.Artifact, Parent: env.Parent,
		ParentRev: env.ParentRev, Author: env.Author, Op: env.Op, Bytes: raw}, env, nil
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

// cmdPublish is the signing publication path.
//
// dryRun stops after showing the plan, so an author (or a script) can inspect the
// exact bytes without a key ever being used. jsonOut emits the plan
// machine-readably for a caller that wants to check the bytes programmatically.
func cmdPublish(local *Store, endpoint, keyPath, file string, dryRun, jsonOut, assumeYes bool) {
	src, err := os.ReadFile(file)
	if err != nil {
		fail(err)
	}
	priv, pubHex := loadSigningKey(keyPath)
	clientPriv, clientPub = priv, pubHex

	plan, env, err := buildPublishPlan(local, endpoint, pubHex, string(src))
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
		fmt.Println("--dry-run: nothing signed, nothing sent.")
		return
	}
	if !assumeYes && !jsonOut {
		if !confirm("Sign and publish these exact bytes?") {
			fail(fmt.Errorf("aborted before signing"))
		}
	}

	sig, err := envelopeSign(priv, env)
	if err != nil {
		fail(err)
	}
	// The bytes signed are the bytes sent: plan.Bytes, not a re-encoding of env.
	out, err := remotePutSigned(endpoint, string(src), plan.Bytes, sig, pubHex)
	if err != nil {
		// A stale parent/revision is the one failure with a specific remedy, and the
		// remedy is deliberately NOT automatic — see the file comment.
		if newParent, newRev, rerr := remoteNameRevision(endpoint, plan.Name); rerr == nil &&
			(newParent != plan.Parent || newRev != plan.ParentRev) {
			fmt.Printf("\nCONFLICT: %q moved while this envelope was being prepared.\n", plan.Name)
			fmt.Printf("  signed against  parent %s (revision %d)\n", plan.Parent, plan.ParentRev)
			fmt.Printf("  now at          parent %s (revision %d)\n", newParent, newRev)
			fmt.Printf("\nThe signature is still valid for the transition it describes, which is no\n")
			fmt.Printf("longer the transition available. It has NOT been re-signed automatically:\n")
			fmt.Printf("that would sign different bytes and authorize a different state change than\n")
			fmt.Printf("the one shown above. Re-run to review the new transition and sign it.\n")
			fail(fmt.Errorf("stale parent: publication not applied"))
		}
		fail(err)
	}
	fmt.Print(out)

	// Verify the registry persisted the exact bytes signed. Without this the client
	// takes the registry's word that the record it accepted is the statement made.
	if got, err := remoteEnvelopeOf(endpoint, plan.Artifact); err == nil {
		if got == "" {
			fmt.Printf("\nWARNING: the registry accepted the publication but records no author\n")
			fmt.Printf("envelope for %s. The attribution is not independently verifiable.\n", shortHash(plan.Artifact))
		} else if got != plan.Bytes {
			fmt.Printf("\nWARNING: the registry persisted DIFFERENT envelope bytes than were signed.\n")
			fmt.Printf("  signed:    %q\n  persisted: %q\n", plan.Bytes, got)
			fmt.Printf("The signature will not verify against the stored record.\n")
			fail(fmt.Errorf("persisted envelope differs from the signed bytes"))
		} else {
			fmt.Printf("\nverified: the registry persisted the exact %d bytes that were signed.\n", len(plan.Bytes))
		}
	}
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var s string
	if _, err := fmt.Scanln(&s); err != nil {
		return false
	}
	return s == "y" || s == "Y" || s == "yes"
}
