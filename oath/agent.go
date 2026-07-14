package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// The agent interface, v0: instead of reading files, an AI author asks the
// store for exactly the slice of the codebase it needs, sized to a token
// budget. Because Oath definitions are local, the spec of a dependency is a
// sufficient interface — bodies are never included, and never needed.

func estTokens(s string) int { return len(s)/4 + 1 }

// cmdContext prints spec-only projections of the named definitions and their
// transitive dependencies, in breadth-first order, greedily skipping any
// definition that would exceed the token budget. Anything omitted is named
// explicitly: a context slice must never silently pretend to be complete.
func cmdContext(st *Store, names []string, budget int) {
	out, err := apiContext(st, names, budget)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func apiContext(st *Store, names []string, budget int) (string, error) {
	var queue []string
	seen := map[string]bool{}
	for _, n := range names {
		h, ok := st.Resolve(n)
		if !ok {
			return "", fmt.Errorf("no definition named %q", n)
		}
		if !seen[h] {
			seen[h] = true
			queue = append(queue, h)
		}
	}
	for i := 0; i < len(queue); i++ {
		d, err := st.GetDef(queue[i])
		if err != nil {
			continue
		}
		deps := collectDeps(d)
		var ds []string
		for dh := range deps {
			ds = append(ds, dh)
		}
		sort.Strings(ds)
		for _, dh := range ds {
			if !seen[dh] {
				seen[dh] = true
				queue = append(queue, dh)
			}
		}
	}

	total := 0
	var sections []string
	var dropped []string
	var included []string
	for _, h := range queue {
		s, err := printSpec(st, h)
		if err != nil {
			continue
		}
		t := estTokens(s)
		if budget > 0 && total+t > budget {
			dropped = append(dropped, st.NameOf(h))
			continue
		}
		total += t
		sections = append(sections, s)
		included = append(included, h)
	}
	var b strings.Builder
	b.WriteString(strings.Join(sections, "\n"))
	fmt.Fprintf(&b, "\n-- context: %d definitions, ~%d tokens", len(sections), total)
	if len(dropped) > 0 {
		fmt.Fprintf(&b, "; OMITTED (over budget): %s", strings.Join(dropped, ", "))
	}
	fmt.Fprintf(&b, "\n-- context-hash: %s\n", contextHash(included))
	return b.String(), nil
}

// contextHash identifies WHAT an agent built against: the SHA-256 of the
// sorted definition hashes actually served in a context slice (issue #4).
// It hashes the identity set, not the rendered text, so a pretty-printer
// change cannot alter what "built against these specs" means. An agent
// passes it back via `put --context`, and the journal records it — making
// implemented-against-stale-specs detectable after the fact by comparing
// against the dependency hashes current at submission time.
func contextHash(included []string) string {
	hs := append([]string{}, included...)
	sort.Strings(hs)
	sum := sha256.Sum256([]byte(strings.Join(hs, "\n")))
	return hex.EncodeToString(sum[:])
}

// cmdDependents answers the reverse question: who builds on this definition?
// Superseded objects show up as name@hash — immutability means history stays
// visible.
func cmdDependents(st *Store, name string) {
	out, err := apiDependents(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func apiDependents(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	var b strings.Builder
	for _, ah := range st.AllHashes() {
		if ah == h {
			continue
		}
		d, err := st.GetDef(ah)
		if err != nil {
			continue
		}
		if collectDeps(d)[h] {
			fmt.Fprintln(&b, st.NameOf(ah))
		}
	}
	if b.Len() == 0 {
		return fmt.Sprintf("nothing references %s\n", name), nil
	}
	return b.String(), nil
}
