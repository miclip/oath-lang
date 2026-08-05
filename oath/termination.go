package main

// Structural termination checking — Foetus-lite, after Agda's checker.
//
// A function is provably total if some LEXICOGRAPHIC order of parameter
// positions (p1, p2, ...) exists such that every self-call strictly shrinks
// the tuple (param p1, param p2, ...): at each call site there is a first
// position in the order where the passed argument is a strict subterm of
// that parameter (obtained by matching on it, transitively), and at every
// earlier position in the order the argument IS the corresponding parameter
// unchanged. The subterm relation is well-founded, so the tuple cannot
// shrink forever and no infinite call chain exists. A single always-
// descending position is the length-1 special case; merge-style functions
// that alternate descent between arguments need length 2. Additionally
// every function the body references must already be total — and since
// references are content hashes, dependency cycles are impossible, so
// totality composes bottom-up for free.
//
// The analysis is conservative: anything it cannot see (descent through
// let-bindings, self passed as a value, non-variable scrutinees) yields
// "unknown", never a false "total". Verdicts:
//
//   "structural"    total: recursion descends structurally, deps total
//   "measure"       total: a Z3-verified integer ranking function bounds the
//                   recursion (an integer counter guarded below); see ranking.go
//   "nonrecursive"  total: no self-calls, deps total
//   "unknown"       not proven total; fuel remains the only bound

// rel records how a bound variable relates in size to each top-level
// parameter: "eq" (is that parameter) or "lt" (strict subterm of it).
type rel map[int]string

type callSite struct{ args []rel }

type termWalker struct {
	st    *Store
	sites []callSite
	bad   bool
}

func pushRel(env []rel, r rel) []rel {
	out := make([]rel, len(env)+1)
	copy(out, env)
	out[len(env)] = r
	return out
}

func argRel(t *Term, env []rel) rel {
	if t.K == "var" && t.Idx < len(env) {
		return env[len(env)-1-t.Idx]
	}
	return nil
}

func ltOf(r rel) rel {
	if r == nil {
		return nil
	}
	out := rel{}
	for k := range r {
		out[k] = "lt"
	}
	return out
}

// walk visits a term. spine carries the relations of arguments applied so
// far, so that when an application chain bottoms out at "self" the full
// argument list is in hand. Only App propagates the spine: a self-call
// reached any other way (through let, if, match, or passed as a value)
// arrives with an empty spine and conservatively fails the check.
// walkItem is one pending node for the iterative walker, carrying the per-node
// state the recursive version passed as arguments.
type walkItem struct {
	t     *Term
	env   []rel
	spine []rel
}

// walk collects self-call sites, ITERATIVELY (#149).
//
// Converted because a definition that clears the gate is then analysed for
// termination, and this descended the same linear spine the typechecker had
// just stopped descending — so `oathCheck` still borrowed the host stack on a
// 5,000-rune string literal.
//
// CHILDREN ARE PUSHED IN REVERSE so they pop in source order. That is not
// cosmetic: w.sites is append-only and lexDescends reads it positionally, so a
// reordered traversal can change the termination VERDICT.
func (w *termWalker) walk(root *Term, rootEnv []rel, rootSpine []rel) {
	stack := []walkItem{{t: root, env: rootEnv, spine: rootSpine}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		t, env := it.t, it.env
		if t == nil {
			continue
		}
		push := func(w walkItem) { stack = append(stack, w) }
		switch t.K {
		case "self":
			w.sites = append(w.sites, callSite{args: it.spine})
		case "app":
			push(walkItem{t: t.B, env: env})
			push(walkItem{t: t.A, env: env, spine: append([]rel{argRel(t.B, env)}, it.spine...)})
		case "lam":
			push(walkItem{t: t.A, env: pushRel(env, nil)})
		case "let":
			push(walkItem{t: t.B, env: pushRel(env, nil)})
			push(walkItem{t: t.A, env: env})
		case "match":
			var brel rel
			if t.A.K == "var" {
				brel = ltOf(argRel(t.A, env))
			}
			d, err := w.st.GetDef(t.Hash)
			if err != nil {
				// The recursive version returned from THIS invocation only, so
				// the node's children are skipped and siblings still run.
				w.bad = true
				continue
			}
			for i := len(t.Arms) - 1; i >= 0; i-- {
				env2 := env
				for range d.Ctors[i] {
					env2 = pushRel(env2, brel)
				}
				push(walkItem{t: &t.Arms[i], env: env2})
			}
			push(walkItem{t: t.A, env: env})
		default:
			for i := len(t.Args) - 1; i >= 0; i-- {
				push(walkItem{t: &t.Args[i], env: env})
			}
			push(walkItem{t: t.C, env: env})
			push(walkItem{t: t.B, env: env})
			push(walkItem{t: t.A, env: env})
		}
	}
}

func isTotal(term string) bool {
	return term == "structural" || term == "nonrecursive" || term == "measure"
}

func bodyFuncRefs(t *Term) map[string]bool {
	// Iterative for the same reason as termWalker.walk (#149): this runs over
	// every admitted definition. The result is a SET, so traversal order does
	// not affect it — but stack depth does.
	out := map[string]bool{}
	stack := []*Term{t}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == nil {
			continue
		}
		if cur.K == "ref" {
			out[cur.Hash] = true
		}
		stack = append(stack, cur.A, cur.B, cur.C)
		for i := range cur.Args {
			stack = append(stack, &cur.Args[i])
		}
		for i := range cur.Arms {
			stack = append(stack, &cur.Arms[i])
		}
	}
	return out
}

// terminationOf classifies a function definition. Empty string for data defs.
// h is d's content hash — needed to resolve self-calls when the ranking-function
// fallback (ranking.go) translates recursive arguments to SMT.
func terminationOf(st *Store, d *Def, h string) string {
	if d.K != "func" {
		return ""
	}
	// Totality is only as good as what the body calls (props don't execute
	// in production positions, so only body refs count).
	for h := range bodyFuncRefs(d.Body) {
		m, err := st.GetMeta(h)
		if err != nil || !isTotal(m.Termination) {
			return "unknown"
		}
	}
	nparams := 0
	cur := d.Body
	for cur.K == "lam" {
		nparams++
		cur = cur.A
	}
	var env []rel
	for i := 0; i < nparams; i++ {
		env = pushRel(env, rel{i: "eq"})
	}
	w := &termWalker{st: st}
	w.walk(cur, env, nil)
	if w.bad {
		return "unknown"
	}
	if len(w.sites) == 0 {
		return "nonrecursive"
	}
	positions := make([]int, nparams)
	for j := range positions {
		positions[j] = j
	}
	if lexDescends(w.sites, positions) {
		return "structural"
	}
	// Structural descent failed: try a Z3-verified integer ranking function
	// (an integer counter bounded below by its guards, e.g. range/replicate).
	if ranksTotal(st, d, h) {
		return "measure"
	}
	return "unknown"
}

// relAt is the diagonal of a call site: how the argument passed at position j
// relates to parameter j itself ("lt" strict subterm, "eq" the parameter
// unchanged, "" unknown).
func relAt(cs callSite, j int) string {
	if j >= len(cs.args) || cs.args[j] == nil {
		return ""
	}
	return cs.args[j][j]
}

// lexDescends searches (with backtracking) for a lexicographic order over the
// given positions that discharges every call site. A position heads a valid
// order iff every remaining site is "lt" or "eq" there — one "" would mean
// the tuple could grow at that position — and at least one site is "lt"
// (otherwise the position discharges nothing). Sites that are "lt" are
// discharged; sites that are "eq" must be discharged by the rest of the
// order, over the remaining positions. Positions are few, so the exponential
// worst case is irrelevant in practice.
func lexDescends(sites []callSite, positions []int) bool {
	if len(sites) == 0 {
		return true
	}
	for i, j := range positions {
		usable, anyLt := true, false
		for _, cs := range sites {
			switch relAt(cs, j) {
			case "lt":
				anyLt = true
			case "eq":
			default:
				usable = false
			}
			if !usable {
				break
			}
		}
		if !usable || !anyLt {
			continue
		}
		var remaining []callSite
		for _, cs := range sites {
			if relAt(cs, j) == "eq" {
				remaining = append(remaining, cs)
			}
		}
		rest := append(append([]int{}, positions[:i]...), positions[i+1:]...)
		if lexDescends(remaining, rest) {
			return true
		}
	}
	return false
}
