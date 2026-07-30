package main

// The Go-side runner for fixtures/envelope/vectors.jsonl (SPEC §10.1).
//
// The reference kernel GENERATED these vectors and never ran them, which is a gap
// worth naming: generation self-validates each record as it is emitted, but that
// checks the record against the kernel at the moment of writing. It cannot detect a
// later change that breaks a rule the vectors were meant to pin, and it gives no way
// to ask what the suite would catch — which is exactly what the conformance mutation
// score needs.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type vectorRecord struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	OctetsB64 string `json:"octets_b64"`
	// signature records
	AuthorPubkey string `json:"author_pubkey"`
	AuthorSig    string `json:"author_sig"`
	Verdict      string `json:"verdict"`
	// reject records may carry either form
	EnvelopeB64 string `json:"envelope_b64"`
	// store records
	State   map[string]any `json:"state"`
	Request map[string]any `json:"request"`
	// Witnesses names the normative rule this record claims to demonstrate. Without
	// it a negative vector earns credit for being rejected AT ALL, which is how a
	// fixture labelled "newline injected" passed while failing on unrelated hex rules
	// and testing nothing it claimed.
	Witnesses string `json:"witnesses,omitempty"`
}

func loadVectors(path string) ([]vectorRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []vectorRecord
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v vectorRecord
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// runVectors executes every record and returns a description of each FAILURE. An
// empty result means the suite passed.
func runVectors(vs []vectorRecord) []string {
	var failures []string
	note := func(v vectorRecord, msg string) {
		failures = append(failures, fmt.Sprintf("%s %q: %s", v.Kind, v.Label, msg))
	}
	for _, v := range vs {
		switch v.Kind {
		case "canonical":
			// Round-trip: the octets must parse and re-encode to themselves.
			oct, err := base64.StdEncoding.DecodeString(v.OctetsB64)
			if err != nil {
				note(v, "octets_b64 does not decode")
				continue
			}
			env, perr := envelopeParse(oct)
			if perr != nil {
				note(v, "canonical octets rejected: "+perr.Error())
				continue
			}
			if string(envelopeEncode(env)) != string(oct) {
				note(v, "re-encoding the parsed envelope does not reproduce the octets")
			}

		case "reject":
			// Either an envelope octet string or a stored-field spelling.
			if v.EnvelopeB64 != "" {
				if _, err := decodeEnvelopeB64(v.EnvelopeB64); err == nil {
					note(v, "non-canonical envelope_b64 was ACCEPTED")
				}
				continue
			}
			oct, err := base64.StdEncoding.DecodeString(v.OctetsB64)
			if err != nil {
				note(v, "octets_b64 does not decode")
				continue
			}
			if _, perr := envelopeParse(oct); perr == nil {
				note(v, "octets that MUST be rejected were accepted")
			}

		case "signature":
			oct, err := base64.StdEncoding.DecodeString(v.OctetsB64)
			if err != nil {
				note(v, "octets_b64 does not decode")
				continue
			}
			var verr error
			env, perr := envelopeParse(oct)
			if perr != nil {
				verr = perr
			} else {
				verr = envelopeVerify(env, v.AuthorSig)
			}
			if v.Verdict == "accept" && verr != nil {
				note(v, "MUST verify but did not: "+verr.Error())
			}
			if v.Verdict == "reject" && verr == nil {
				note(v, "MUST fail verification but verified")
			}

		case "store":
			oct, err := base64.StdEncoding.DecodeString(str(v.Request, "octets_b64"))
			if err != nil {
				note(v, "request octets do not decode")
				continue
			}
			env, perr := envelopeParse(oct)
			if perr != nil {
				if v.Verdict == "accept" {
					note(v, "MUST be accepted but the envelope did not parse: "+perr.Error())
				}
				continue
			}
			rev := 0
			fmt.Sscanf(str(v.State, "parent_rev"), "%d", &rev)
			cerr := checkPublication(env, str(v.Request, "author_sig"),
				str(v.Request, "authenticated_principal"), str(v.State, "name"),
				str(v.Request, "recomputed_artifact"), str(v.State, "bound"), rev)
			if v.Verdict == "accept" && cerr != nil {
				note(v, "MUST be accepted but was refused: "+cerr.Error())
			}
			if v.Verdict == "reject" && cerr == nil {
				note(v, "MUST be refused but was accepted")
			}
		}
	}
	return failures
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

// cmdVectors runs the suite and reports, so the reference kernel checks the fixtures
// it emits rather than only producing them.
func cmdVectors(path string) {
	vs, err := loadVectors(path)
	if err != nil {
		fail(err)
	}
	failures := runVectors(vs)
	kinds := map[string]int{}
	for _, v := range vs {
		kinds[v.Kind]++
	}
	// Claim-checking requires disabling rules, so it lives in the harness build only
	// (rule_disable_mutation.go). A production `oath vectors` runs the suite; it cannot ask
	// what the suite would CATCH, because answering that means weakening verification.
	reportVectorClaims(vs)
	if len(failures) == 0 {
		fmt.Printf("VECTORS: PASS — %d records (%d canonical, %d reject, %d signature, %d store)\n",
			len(vs), kinds["canonical"], kinds["reject"], kinds["signature"], kinds["store"])
		return
	}
	fmt.Printf("VECTORS: FAIL — %d of %d records\n", len(failures), len(vs))
	for _, f := range failures {
		fmt.Printf("  %s\n", f)
	}
	fail(fmt.Errorf("vector suite failed"))
}
