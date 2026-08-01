package main

// The standard-library index: `stdlib/<name>` resolution (#107).
//
// The library is a CURATED VIEW, not a namespace. Membership is decided by the
// manifest; where a member physically lives is a separate fact:
//
//	project-publication   the project republished it → oath/<name>
//	referenced            the project SELECTED someone else's publication →
//	                      it stays at their name, under their key, on their terms
//
// USING THE LIBRARY MUST NOT REQUIRE KNOWING WHICH. `oath get stdlib/map` follows
// the member wherever it is. If a consumer had to know, `referenced` members
// would be second-class in practice however the manifest describes them, and
// contributors would correctly infer that republication is the "real" membership
// — which is the outcome the mode exists to avoid.
//
// The distinction stays VISIBLE ON REQUEST rather than hidden: resolution reports
// what it followed, so provenance is one flag away and never a surprise.
//
// SCOPE, stated because it is a real limit. The index is read from a LOCAL
// manifest file. It is not itself content-addressed, published, or signed, so
// resolving through it is trusting whoever supplied the file — unlike everything
// the resolution lands on, which is signed and verifiable. Publishing the index
// as an artifact so a consumer can verify the curation itself is the obvious next
// step and is deliberately not done here.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const stdlibPrefix = "stdlib/"

// stdlibMember is one entry of the curated index.
type stdlibMember struct {
	Name        string `json:"name"`
	Artifact    string `json:"artifact"`
	Membership  string `json:"membership"`
	Publication string `json:"publication"`
	License     string `json:"license"`
	Export      bool   `json:"export"`
}

type stdlibIndex struct {
	Namespace   string         `json:"namespace"`
	Definitions []stdlibMember `json:"definitions"`
}

// stdlibManifestPath finds the index: OATH_STDLIB, then ./stdlib/oath-stdlib.json,
// then ~/.oath/oath-stdlib.json. Explicit beats conventional, and a missing index
// is reported rather than silently treated as an empty library — "no such member"
// and "I could not find the index" are different answers and only one of them
// means the member does not exist.
func stdlibManifestPath() (string, error) {
	if p := os.Getenv("OATH_STDLIB"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("OATH_STDLIB=%s: %w", p, err)
		}
		return p, nil
	}
	for _, p := range []string{
		filepath.Join("stdlib", "oath-stdlib.json"),
		filepath.Join(oathHome(), "oath-stdlib.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no standard-library index found: set OATH_STDLIB, or place it at " +
		"stdlib/oath-stdlib.json or ~/.oath/oath-stdlib.json")
}

func loadStdlibIndex() (*stdlibIndex, string, error) {
	p, err := stdlibManifestPath()
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	var ix stdlibIndex
	if err := json.Unmarshal(b, &ix); err != nil {
		return nil, "", fmt.Errorf("standard-library index %s is not valid JSON: %w", p, err)
	}
	return &ix, p, nil
}

// stdlibResolution is what a `stdlib/<name>` reference resolves to.
type stdlibResolution struct {
	Member      string // the library name asked for
	Target      string // the registry name it lives under
	Artifact    string
	Membership  string
	Publication string // the pinned publication, for a referenced member
	IndexPath   string
}

// resolveStdlib maps stdlib/<name> to the registry name holding it.
func resolveStdlib(ref string) (stdlibResolution, error) {
	name := strings.TrimPrefix(ref, stdlibPrefix)
	if name == "" || strings.Contains(name, "/") {
		return stdlibResolution{}, fmt.Errorf("%q is not a standard-library reference: "+
			"expected stdlib/<name> with a single bare name", ref)
	}
	ix, path, err := loadStdlibIndex()
	if err != nil {
		return stdlibResolution{}, err
	}
	for _, d := range ix.Definitions {
		if d.Name != name {
			continue
		}
		if !d.Export {
			// Present but not exported: a deliberate exclusion, and saying so is
			// more useful than "not found", which would suggest a typo.
			return stdlibResolution{}, fmt.Errorf("stdlib/%s is listed in the index but is NOT a "+
				"library member (export: false) — it is recorded there to explain what it is, "+
				"not to offer it", name)
		}
		r := stdlibResolution{Member: name, Artifact: d.Artifact,
			Membership: d.Membership, Publication: d.Publication, IndexPath: path}
		switch d.Membership {
		case "project-publication":
			r.Target = ix.Namespace + "/" + name
		case "referenced":
			// The member lives at the publisher's name, which the index does not
			// record — only the publication digest does. Resolving it needs the
			// journal, so the caller looks it up; this reports what to look for.
			r.Target = ""
		default:
			return stdlibResolution{}, fmt.Errorf("stdlib/%s has unknown membership %q", name, d.Membership)
		}
		return r, nil
	}
	return stdlibResolution{}, fmt.Errorf("stdlib/%s is not a standard-library member (index: %s)", name, path)
}

// resolveStdlibIn completes a resolution against a store, finding the registry
// name of a referenced member by its pinned publication.
func resolveStdlibIn(st *Store, ref string) (stdlibResolution, error) {
	r, err := resolveStdlib(ref)
	if err != nil {
		return r, err
	}
	if r.Target != "" {
		return r, nil
	}
	name, ok := nameOfPublication(st, r.Publication)
	if !ok {
		return r, fmt.Errorf("stdlib/%s selects publication %s…, which this store does not hold — "+
			"the index names a publication the registry cannot show you",
			r.Member, shortHash(r.Publication))
	}
	r.Target = name
	return r, nil
}

// nameOfPublication finds the name bound by the publication with this digest.
func nameOfPublication(st *Store, digest string) (string, bool) {
	if digest == "" {
		return "", false
	}
	for _, e := range st.ReadLog() {
		if e.EnvelopeB64 == "" {
			continue
		}
		octets, err := decodeEnvelopeB64(e.EnvelopeB64)
		if err != nil || authorStatementKind(octets) != "publication" {
			continue
		}
		if hex.EncodeToString(sha256Sum(octets)) != digest {
			continue
		}
		env, perr := envelopeParse(octets)
		if perr != nil {
			return "", false
		}
		return env.Name, true
	}
	return "", false
}

// renderResolution is the provenance a consumer can ask for. It is not printed by
// default — the point is that using the library does not require reading it — but
// it must always be available, because "which grant am I relying on" is a
// question a consumer is entitled to answer without leaving the tool.
func renderResolution(r stdlibResolution) string {
	var b strings.Builder
	fmt.Fprintf(&b, "stdlib/%s\n", r.Member)
	fmt.Fprintf(&b, "  resolves to:  %s\n", r.Target)
	fmt.Fprintf(&b, "  artifact:     %s\n", r.Artifact)
	fmt.Fprintf(&b, "  membership:   %s\n", r.Membership)
	if r.Membership == "referenced" {
		fmt.Fprintf(&b, "  publication:  %s\n", r.Publication)
		fmt.Fprintf(&b, "\n  SELECTED, not republished. The project recommends this exact\n")
		fmt.Fprintf(&b, "  publication; the terms are its publisher's, not the project's.\n")
	} else {
		fmt.Fprintf(&b, "\n  PUBLISHED BY THE PROJECT under its own licence assertion.\n")
	}
	fmt.Fprintf(&b, "  index: %s (a local file — not itself signed or verified)\n", r.IndexPath)
	return b.String()
}

// sha256Sum is the publication-digest primitive: a publication is pinned by the
// hash of its EXACT canonical octets, so the digest identifies one statement
// rather than the artifact it happens to name.
func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
