package main

import (
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
	var queue []string
	seen := map[string]bool{}
	for _, n := range names {
		h, ok := st.Resolve(n)
		if !ok {
			fail(fmt.Errorf("no definition named %q", n))
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
	}
	fmt.Print(strings.Join(sections, "\n"))
	fmt.Printf("\n-- context: %d definitions, ~%d tokens", len(sections), total)
	if len(dropped) > 0 {
		fmt.Printf("; OMITTED (over budget): %s", strings.Join(dropped, ", "))
	}
	fmt.Println()
}

// cmdDependents answers the reverse question: who builds on this definition?
// Superseded objects show up as name@hash — immutability means history stays
// visible.
func cmdDependents(st *Store, name string) {
	h, ok := st.Resolve(name)
	if !ok {
		fail(fmt.Errorf("no definition named %q", name))
	}
	found := false
	for _, ah := range st.AllHashes() {
		if ah == h {
			continue
		}
		d, err := st.GetDef(ah)
		if err != nil {
			continue
		}
		if collectDeps(d)[h] {
			fmt.Println(st.NameOf(ah))
			found = true
		}
	}
	if !found {
		fmt.Printf("nothing references %s\n", name)
	}
}
