package main

import (
	"bytes"
	"sort"
	"strconv"
)

// A REAL E-GRAPH, BOUNDED (#65 rung 1).
//
// `eNormalize` (canon.go) is a CONFLUENT rewriter: every rule it applies has an
// obvious direction, so applying them to exhaustion lands on a normal form and
// no search is needed. Distributivity is the rule that breaks that property.
// `a*(b+c)` and `a*b + a*c` are equal, neither is canonically "smaller" in the
// sense a rewriter needs, and expanding one form can enable a factoring that
// re-creates the other. There is no confluent orientation, so the class cannot
// be decided by rewriting a term in place.
//
// The standard answer (egg) is to stop rewriting a TERM and start growing a
// SET of equal terms:
//
//	e-node    an operator symbol plus the e-CLASSES of its children — never
//	          child terms, which is what lets one node stand for exponentially
//	          many terms.
//	e-class   a set of e-nodes asserted equal. Union-find owns the relation.
//	congruence  if two nodes have the same symbol and their children are now in
//	          the same classes, the nodes are equal too — the closure that makes
//	          a union propagate upward, restored by `rebuild`.
//	saturation  apply every rule everywhere, adding nodes and unioning classes,
//	          until nothing changes or a BUDGET stops it.
//	extraction  pick one representative per class by a cost function. That
//	          representative is what `eHash` hashes.
//
// THE INVARIANT IS UNCHANGED (docs/egraph.md): identity is still the O1
// encoding of the definition's ACTUAL AST. Everything here computes a separate
// discovery key.
//
// WHAT IS DELIBERATELY NOT HERE. The rules fire ONLY on `+` and `*` over `Int`
// and `Rat`. Distributivity over `Float` is unsound for the same reason
// associativity is — `(a*(b+c))` and `a*b + a*c` differ in rounding — and the
// type direction is taken from `isACPrim`, which is the existing authority for
// "may this operator be re-associated at this operand type", rather than from a
// second list that could disagree with it.
//
// EVERY DECISION HERE IS DETERMINISTIC. No rule application, union, or
// extraction choice reads a Go map in iteration order: nodes are visited by
// ascending id, classes by ascending canonical id, and extraction ties break on
// canonical BYTES rather than on class numbering — the numbering depends on
// insertion order, so a tie broken on it would make `eHash` depend on how a
// definition happened to be written rather than on what it means.

// egClass is an e-class id. It indexes the union-find, not the node table: a
// class holds any number of nodes.
type egClass int32

// egNode is a hash-consed e-node.
//
// `sym` is the node's own identity WITHOUT its children — for an ordinary term
// node it is the canonical encoding of the term with every child slot replaced
// by one fixed placeholder, so it distinguishes exactly what the canonical
// encoder distinguishes (operator, literal payload, type annotation, reference
// hash, field names, arity) and nothing else. Deriving it from the real encoder
// rather than from a hand-written field comparison is the rule this repository
// has already paid to learn: a hand-written key compares the fields its author
// remembered.
//
// `tmpl` is that same placeholder-filled term, kept so extraction can rebuild a
// real term by dropping the extracted children back into their slots.
type egNode struct {
	sym  string
	tmpl *Term // nil for an AC node
	ac   bool
	acOp string // "+" or "*", AC nodes only
	args []egClass
}

// egHole is the placeholder that stands in for a child inside a node's `sym`.
// Any closed term would do; a Bool literal is the shortest one the encoder
// admits. It is read-only.
var egHole = Term{K: "bool"}

// egBudget bounds one e-graph. Both halves are needed and they bound different
// things: `nodes` bounds the SIZE the graph may reach, `iters` bounds how many
// saturation rounds may run even if each round is small.
//
// Exceeding either is not an error. Saturation stops, extraction runs on the
// graph as it stands, and the result is still SOUND — every node in a class is
// equal to every other, so any representative is a correct one. What a budget
// costs is COMPLETENESS: two definitions that would have been found equivalent
// under a bigger budget may not be. That direction is the safe one.
type egBudget struct {
	nodes int
	iters int
}

// egDefaultBudget is what eHash uses. The numbers are chosen against the cost
// of extraction rather than against the cost of saturation: extraction
// materialises canonical bytes per candidate node, so the graph has to stay
// small enough that doing so repeatedly is cheap.
var egDefaultBudget = egBudget{nodes: 8192, iters: 12}

// egMaxTermNodes is the largest normalized body the e-graph will look at. The
// portable profile admits terms with tens of thousands of nodes and `eHash` is
// reachable from `find --equiv`, so a term above this bound skips the e-graph
// entirely and hashes its e-normalized form exactly as before. Skipping loses
// equivalences; it cannot produce a wrong one.
const egMaxTermNodes = 2048

// ---------- structural child slots ----------
//
// ONE ORDERING, USED BY EVERY CONSUMER. The insertion walk, the shape template
// and the extraction rebuild all enumerate a term's children through egSlots,
// so none of them can disagree with the others about which children a node has
// or what order they are in. Written as data rather than as three switches.

type egSlot struct {
	field byte // 'A' | 'B' | 'C' | 'g' → Args[idx] | 'm' → Arms[idx]
	idx   int
}

func egSlots(t *Term) []egSlot {
	switch t.K {
	case "lam", "field":
		return []egSlot{{'A', 0}}
	case "app", "let":
		return []egSlot{{'A', 0}, {'B', 0}}
	case "if":
		return []egSlot{{'A', 0}, {'B', 0}, {'C', 0}}
	case "prim", "ctor", "record":
		out := make([]egSlot, len(t.Args))
		for i := range t.Args {
			out[i] = egSlot{'g', i}
		}
		return out
	case "match":
		out := make([]egSlot, 0, 1+len(t.Arms))
		out = append(out, egSlot{'A', 0})
		for i := range t.Arms {
			out = append(out, egSlot{'m', i})
		}
		return out
	}
	return nil
}

func egSlotGet(t *Term, s egSlot) *Term {
	switch s.field {
	case 'A':
		return t.A
	case 'B':
		return t.B
	case 'C':
		return t.C
	case 'g':
		return &t.Args[s.idx]
	case 'm':
		return &t.Arms[s.idx]
	}
	return nil
}

func egSlotSet(t *Term, s egSlot, v *Term) {
	switch s.field {
	case 'A':
		t.A = v
	case 'B':
		t.B = v
	case 'C':
		t.C = v
	case 'g':
		t.Args[s.idx] = *v
	case 'm':
		t.Arms[s.idx] = *v
	}
}

// egShape returns the node's own identity: a copy with every child slot filled
// by the placeholder, and fresh Args/Arms so the original is never touched.
func egShape(t *Term) *Term {
	sc := *t
	if len(t.Args) > 0 {
		sc.Args = make([]Term, len(t.Args))
	}
	if len(t.Arms) > 0 {
		sc.Arms = make([]Term, len(t.Arms))
	}
	for _, s := range egSlots(&sc) {
		egSlotSet(&sc, s, &egHole)
	}
	return &sc
}

// cloneTerm is a structural deep copy, ITERATIVELY.
//
// IT EXISTS BECAUSE THE CHECKER WRITES INTO THE TERM IT IS GIVEN. `chk.synth`
// publishes inferred type arguments into `ctor` and `ref`/`self` heads
// (check_machine.go's finishInference and finishInferApp), and `eNormalize`
// calls synth on the ORIGINAL subterm. `Store.GetDef` caches, so the definition
// discovery is handed is the same pointer every later consumer sees — and its
// bytes ARE its identity. Hashing must therefore normalize a COPY.
//
// NOT `deepCopyTerm` (mutate.go), which round-trips through encoding/json: that
// recurses over the term, and `eHash` is reachable from `find --equiv` on terms
// the portable profile admits at tens of thousands of nodes deep.
//
// TyArgs are COPIED rather than shared, because they are exactly what the
// checker overwrites. Both publication sites assign a fresh slice, so sharing
// the header would in fact be safe today — copying does not depend on that
// remaining true. `Ty`, `Int` and `Rat` pointers are shared: nothing in the
// kernel mutates one in place, and copying them would make this walk pay for
// the whole type structure of every node.
func cloneTerm(t *Term) *Term {
	if t == nil {
		return nil
	}
	out := new(Term)
	type item struct{ src, dst *Term }
	stack := []item{{t, out}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s, d := it.src, it.dst
		*d = *s
		if len(s.TyArgs) > 0 {
			d.TyArgs = append([]Ty(nil), s.TyArgs...)
		}
		if len(s.Args) > 0 {
			d.Args = make([]Term, len(s.Args))
		}
		if len(s.Arms) > 0 {
			d.Arms = make([]Term, len(s.Arms))
		}
		for _, sl := range egSlots(s) {
			c := egSlotGet(s, sl)
			if c == nil {
				continue
			}
			switch sl.field {
			case 'A', 'B', 'C':
				n := new(Term)
				egSlotSet(d, sl, n)
				stack = append(stack, item{c, n})
			default:
				stack = append(stack, item{c, egSlotGet(d, sl)})
			}
		}
	}
	return out
}

// egFlatten collects the leaves of an `op` chain as POINTERS into the term,
// which is the one thing acFlatten cannot give (it returns copied values, so a
// leaf cannot be mapped back to the node whose operand type was synthesized).
//
// THE DESCENT RULE IS acFlatten's, and it is pinned to it by a test rather than
// restated in prose: a leaf is anything that is not a two-argument `prim` of
// the same operator. Iterative, because a normalized chain is as deep as the
// term that produced it.
func egFlatten(op string, t *Term) []*Term {
	var out []*Term
	stack := []*Term{t}
	for len(stack) > 0 {
		a := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if a.K == "prim" && a.Op == op && len(a.Args) == 2 {
			stack = append(stack, &a.Args[1], &a.Args[0])
			continue
		}
		out = append(out, a)
	}
	return out
}

// ---------- the graph ----------

type egraph struct {
	parent  []egClass
	nodes   []egNode
	nodeCls []egClass
	cons    map[string]egClass

	budget    egBudget
	exhausted bool

	// roundIndex is the class→nodes index the rules read, rebuilt once per
	// saturation round. A rule reads it as it stood at the START of the round,
	// so a match does not depend on how far through the round it is.
	roundIndex map[egClass][]int
	// fired counts SATURATION ROUNDS in which some rule changed the graph —
	// not individual matches. It is what tells "the e-graph found nothing to
	// do" from "the e-graph rewrote something", which a test needs and a caller
	// does not.
	fired int
}

func newEgraph(b egBudget) *egraph {
	return &egraph{cons: map[string]egClass{}, budget: b}
}

func (g *egraph) find(c egClass) egClass {
	for g.parent[c] != c {
		g.parent[c] = g.parent[g.parent[c]]
		c = g.parent[c]
	}
	return c
}

// union merges two classes and reports whether anything changed. The SMALLER id
// always survives, so the representative of a class is a function of which
// classes were created, never of the order they were merged in.
func (g *egraph) union(a, b egClass) bool {
	ra, rb := g.find(a), g.find(b)
	if ra == rb {
		return false
	}
	if rb < ra {
		ra, rb = rb, ra
	}
	g.parent[rb] = ra
	return true
}

func (g *egraph) canon(n egNode) egNode {
	args := make([]egClass, len(n.args))
	for i, a := range n.args {
		args[i] = g.find(a)
	}
	if n.ac {
		// An AC node's children are a MULTISET: sorted, never deduplicated.
		// `a + a` is not `a`, and `*`/`+` are not idempotent.
		sort.Slice(args, func(i, j int) bool { return args[i] < args[j] })
	}
	n.args = args
	return n
}

func (g *egraph) key(n egNode) string {
	var b bytes.Buffer
	b.WriteString(n.sym)
	for _, a := range n.args {
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(int(a)))
	}
	return b.String()
}

// add hash-conses a node and returns its class. `ok` is false only when the
// node budget is spent — the caller must then abandon the rewrite it was in the
// middle of, so that a partially built rewrite never gets unioned into anything.
func (g *egraph) add(n egNode) (egClass, bool) {
	n = g.canon(n)
	// A one-argument AC node is its argument: `+(x)` is `x`. Factoring produces
	// these whenever a quotient or a remainder has a single term, and leaving
	// them in the graph would make the same value reachable under two symbols.
	if n.ac && len(n.args) == 1 {
		return n.args[0], true
	}
	k := g.key(n)
	if c, ok := g.cons[k]; ok {
		return g.find(c), true
	}
	if len(g.nodes) >= g.budget.nodes {
		g.exhausted = true
		return 0, false
	}
	cls := egClass(len(g.parent))
	g.parent = append(g.parent, cls)
	g.nodes = append(g.nodes, n)
	g.nodeCls = append(g.nodeCls, cls)
	g.cons[k] = cls
	return cls, true
}

// rebuild restores CONGRUENCE: after a union, two nodes whose children are now
// in the same classes denote the same value and their classes must merge too.
// Repeated until a pass changes nothing, because a merge can make a further
// pair congruent one level up.
func (g *egraph) rebuild() {
	for {
		changed := false
		seen := make(map[string]egClass, len(g.nodes))
		for i := range g.nodes {
			g.nodes[i] = g.canon(g.nodes[i])
			c := g.find(g.nodeCls[i])
			g.nodeCls[i] = c
			k := g.key(g.nodes[i])
			if prev, ok := seen[k]; ok {
				if g.union(prev, c) {
					changed = true
				}
				seen[k] = g.find(prev)
			} else {
				seen[k] = c
			}
		}
		if !changed {
			g.cons = seen
			return
		}
	}
}

// egMembers is one class and the ids of its nodes, both in ascending order —
// the deterministic view every rule and the extractor iterate over.
type egMembers struct {
	cls   egClass
	nodes []int
}

func (g *egraph) members() []egMembers {
	byCls := map[egClass][]int{}
	for i := range g.nodes {
		c := g.find(g.nodeCls[i])
		byCls[c] = append(byCls[c], i)
	}
	out := make([]egMembers, 0, len(byCls))
	for c, ns := range byCls {
		out = append(out, egMembers{cls: c, nodes: ns})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].cls < out[j].cls })
	return out
}

func egACSym(op, tag string) string { return "a|" + op + "|" + tag }

// ---------- the rules ----------
//
// Three rules, all confined to `+`/`*` over Int/Rat, all sound for every value
// of those types because both are exact (`Int` is ℤ and `Rat` is ℚ — there is
// no overflow and no rounding to make a re-association observable).
//
// ASSOCIATIVITY IS A RULE HERE EVEN THOUGH THE AC REPRESENTATION ALREADY
// FLATTENS. Flattening happens at INSERTION, on syntax. Once a class has been
// merged with another by a rewrite, a child class can come to CONTAIN a same-op
// node without any term ever having been written that way, and the flattened
// form of that is a node nothing else would create.

// ruleFlatten: `op(x, op(y, z), …)` ≡ `op(x, y, z, …)`.
func (g *egraph) ruleFlatten(nid int, cls egClass) bool {
	n := g.nodes[nid]
	byCls := g.membersOf()
	changed := false
	for i, a := range n.args {
		for _, mid := range byCls[g.find(a)] {
			m := g.nodes[mid]
			if m.sym != n.sym {
				continue
			}
			args := make([]egClass, 0, len(n.args)+len(m.args)-1)
			args = append(args, n.args[:i]...)
			args = append(args, n.args[i+1:]...)
			args = append(args, m.args...)
			c, ok := g.add(egNode{sym: n.sym, ac: true, acOp: n.acOp, args: args})
			if !ok {
				return changed
			}
			if g.union(c, cls) {
				changed = true
			}
		}
	}
	return changed
}

// ruleDistribute: `x * (y + z)` ≡ `x*y + x*z`, for a product of any arity and a
// sum of any arity.
func (g *egraph) ruleDistribute(nid int, cls egClass, tag string) bool {
	n := g.nodes[nid]
	sumSym := egACSym("+", tag)
	byCls := g.membersOf()
	changed := false
	for i, a := range n.args {
		for _, sid := range byCls[g.find(a)] {
			s := g.nodes[sid]
			if s.sym != sumSym {
				continue
			}
			rest := make([]egClass, 0, len(n.args)-1)
			rest = append(rest, n.args[:i]...)
			rest = append(rest, n.args[i+1:]...)
			addends := make([]egClass, 0, len(s.args))
			abort := false
			for _, q := range s.args {
				prodArgs := append(append([]egClass{}, rest...), q)
				pc, ok := g.add(egNode{sym: n.sym, ac: true, acOp: "*", args: prodArgs})
				if !ok {
					abort = true
					break
				}
				addends = append(addends, pc)
			}
			if abort {
				return changed
			}
			sc, ok := g.add(egNode{sym: sumSym, ac: true, acOp: "+", args: addends})
			if !ok {
				return changed
			}
			if g.union(sc, cls) {
				changed = true
			}
		}
	}
	return changed
}

// ruleFactor: `x*y + x*z + w` ≡ `x*(y + z) + w`.
//
// The factor is taken out of EVERY addend that can supply it, which is one
// deterministic choice among many possible partitions. Choosing a subset would
// be exponential; choosing the maximal support is the move that inverts
// distribution, which is what makes the two forms meet.
func (g *egraph) ruleFactor(nid int, cls egClass, tag string) bool {
	n := g.nodes[nid]
	prodSym := egACSym("*", tag)
	byCls := g.membersOf()

	// support, in first-encounter order so the iteration below is deterministic
	// without sorting classes into an order that means nothing.
	type cand struct {
		factor  egClass
		addends []int    // indices into n.args
		product []egNode // the chosen product node per addend, parallel
	}
	var cands []cand
	index := map[egClass]int{}
	for i, a := range n.args {
		seenHere := map[egClass]bool{}
		for _, pid := range byCls[g.find(a)] {
			p := g.nodes[pid]
			if p.sym != prodSym {
				continue
			}
			for _, f := range p.args {
				if seenHere[f] {
					continue // this addend already offers f, via this or an earlier product
				}
				seenHere[f] = true
				ci, ok := index[f]
				if !ok {
					cands = append(cands, cand{factor: f})
					ci = len(cands) - 1
					index[f] = ci
				}
				cands[ci].addends = append(cands[ci].addends, i)
				cands[ci].product = append(cands[ci].product, p)
			}
		}
	}

	changed := false
	for _, c := range cands {
		if len(c.addends) < 2 {
			continue
		}
		inSupport := map[int]bool{}
		quotients := make([]egClass, 0, len(c.addends))
		abort := false
		for k, ai := range c.addends {
			p := c.product[k]
			rem := make([]egClass, 0, len(p.args)-1)
			dropped := false
			for _, f := range p.args {
				if !dropped && f == c.factor {
					dropped = true
					continue
				}
				rem = append(rem, f)
			}
			if len(rem) == 0 {
				// The product IS the factor. Cancelling it would need a literal
				// 1 of the right numeric type, which is a different rule; skip
				// this addend rather than invent one.
				abort = true
				break
			}
			qc, ok := g.add(egNode{sym: prodSym, ac: true, acOp: "*", args: rem})
			if !ok {
				return changed
			}
			quotients = append(quotients, qc)
			inSupport[ai] = true
		}
		if abort || len(quotients) < 2 {
			continue
		}
		sum, ok := g.add(egNode{sym: egACSym("+", tag), ac: true, acOp: "+", args: quotients})
		if !ok {
			return changed
		}
		prod, ok := g.add(egNode{sym: prodSym, ac: true, acOp: "*", args: []egClass{c.factor, sum}})
		if !ok {
			return changed
		}
		outArgs := []egClass{prod}
		for i, a := range n.args {
			if !inSupport[i] {
				outArgs = append(outArgs, a)
			}
		}
		res, ok := g.add(egNode{sym: n.sym, ac: true, acOp: "+", args: outArgs})
		if !ok {
			return changed
		}
		if g.union(res, cls) {
			changed = true
		}
	}
	return changed
}

// membersOf is the per-round index (see egraph.roundIndex).
func (g *egraph) membersOf() map[egClass][]int { return g.roundIndex }

// saturate runs rounds until nothing changes or a budget stops it.
func (g *egraph) saturate(acTag map[string]string) {
	for it := 0; it < g.budget.iters; it++ {
		g.roundIndex = map[egClass][]int{}
		for i := range g.nodes {
			c := g.find(g.nodeCls[i])
			g.roundIndex[c] = append(g.roundIndex[c], i)
		}
		changed := false
		for _, m := range g.members() {
			for _, nid := range m.nodes {
				n := g.nodes[nid]
				if !n.ac {
					continue
				}
				tag := acTag[n.sym]
				if tag == "" {
					continue
				}
				if g.ruleFlatten(nid, m.cls) {
					changed = true
				}
				switch n.acOp {
				case "*":
					if g.ruleDistribute(nid, m.cls, tag) {
						changed = true
					}
				case "+":
					if g.ruleFactor(nid, m.cls, tag) {
						changed = true
					}
				}
				if g.exhausted {
					g.rebuild()
					return
				}
			}
		}
		g.rebuild()
		if changed {
			g.fired++
		} else {
			return
		}
	}
}

// ---------- extraction ----------

// egBest is a class's chosen representative: the cheapest term in it, ties
// broken on canonical bytes.
type egBest struct {
	cost  int
	term  *Term
	bytes []byte
}

// build materialises a node's term from its children's chosen terms.
//
// The result is a DAG — a child term is SHARED by every parent that chose it,
// never copied — and that is safe because nothing here mutates a built term:
// it is encoded and discarded. Cost is still counted as TREE size, which is
// what makes the extractor prefer `a*(b+c)` (5 nodes) to `a*b + a*c` (7).
func (g *egraph) build(n egNode, kids []*Term) *Term {
	if n.ac {
		// AN AC NODE IS REBUILT BY acRebuild, NOT BY A SECOND SORT-AND-NEST.
		// That function is the authority for what a canonical chain of one AC
		// operator looks like — the ordering, the right-nesting, and the unit
		// and idempotence rules all live there — and a copy here would be a
		// second notion of the same thing, free to drift from the one
		// eNormalize uses. It is deliberately NOT given acFlatten: flattening a
		// chain that reaches a node through a merged CLASS is a graph
		// operation, and it is ruleFlatten's job.
		vals := make([]Term, len(kids))
		for i, k := range kids {
			vals[i] = *k
		}
		return acRebuild(n.acOp, vals)
	}
	out := *n.tmpl
	if len(n.tmpl.Args) > 0 {
		out.Args = make([]Term, len(n.tmpl.Args))
	}
	if len(n.tmpl.Arms) > 0 {
		out.Arms = make([]Term, len(n.tmpl.Arms))
	}
	for i, s := range egSlots(&out) {
		egSlotSet(&out, s, kids[i])
	}
	return &out
}

// extract chooses one term per class by minimum tree cost, ties broken on
// canonical bytes.
//
// TIES MUST NOT BREAK ON CLASS IDS. Ids are assigned in insertion order, so two
// definitions that saturate to the SAME graph up to numbering would extract
// different representatives and `find --equiv` would miss them — the exact
// failure the e-graph exists to remove. Bytes are a property of the term.
//
// Relaxation rather than a topological walk, because a class can be cyclic. It
// is monotone (a class's best only ever improves), so it terminates; the cap is
// a guard, and failing to converge under it returns nil so the caller falls
// back to the e-normalized term.
func (g *egraph) extract(root egClass) *Term {
	best := map[egClass]*egBest{}
	classes := g.members()
	maxPasses := len(classes) + 2
	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		for _, m := range classes {
			for _, nid := range m.nodes {
				n := g.nodes[nid]
				kids := make([]*Term, len(n.args))
				// AN AC NODE IS NOT ONE NODE IN THE REBUILT TERM. It rebuilds
				// as a right-nested binary chain, so an n-ary sum contributes
				// n-1 `prim` nodes, not one. Charging it as one made the cost
				// function disagree with the AST it claims to measure — and
				// since extraction MINIMISES that cost, the disagreement is not
				// cosmetic: it is which representative a class settles on.
				// (An AC node with a single argument collapses to that argument
				// in `add`, so len(args) is always at least 2 here.)
				self := 1
				if n.ac {
					self = len(n.args) - 1
				}
				cost, ok := self, true
				for j, a := range n.args {
					b := best[g.find(a)]
					if b == nil {
						ok = false
						break
					}
					cost += b.cost
					kids[j] = b.term
				}
				if !ok {
					continue
				}
				cur := best[m.cls]
				if cur != nil && cost > cur.cost {
					continue
				}
				t := g.build(n, kids)
				by := termBytes(t)
				if cur == nil || cost < cur.cost || bytes.Compare(by, cur.bytes) < 0 {
					best[m.cls] = &egBest{cost: cost, term: t, bytes: by}
					changed = true
				}
			}
		}
		if !changed {
			b := best[g.find(root)]
			if b == nil {
				return nil
			}
			return b.term
		}
	}
	return nil
}

// ---------- entry point ----------

// eCanonicalArith is the e-graph pass `eHash` runs AFTER `eNormalize`.
//
// It returns the e-normalized term UNCHANGED whenever the e-graph cannot help
// or cannot be afforded — no arithmetic redex, a term over the size bound, a
// spent budget, an extraction that did not converge. Every one of those is a
// loss of COMPLETENESS and none is a loss of soundness, which is the direction
// this whole layer is allowed to fail in.
func eCanonicalArith(chk *checkerMachine, normalized *Term) *Term {
	out, _ := eCanonicalArithBudget(chk, normalized, egDefaultBudget, false)
	return out
}

// eCanonicalArithBudget is the testable form: an explicit budget, and `force`
// to build the graph even where the syntactic pre-check says no rule can fire —
// which is what lets a test assert the pass is INERT on the corpus rather than
// merely unreached.
func eCanonicalArithBudget(chk *checkerMachine, normalized *Term, b egBudget, force bool) (*Term, *egraph) {
	if normalized == nil {
		return nil, nil
	}
	nodes, arith, wellFormed := egSurvey(normalized)
	if !wellFormed || nodes > egMaxTermNodes {
		return normalized, nil
	}
	if !arith && !force {
		return normalized, nil
	}

	// TYPE DIRECTION IS DECIDED ON A THROWAWAY COPY. chk.synth publishes
	// inferred type arguments INTO the term it is given, so synthesizing
	// against the term that is about to be hashed could move `eHash` for a
	// definition with omitted type arguments — a change in discovery, arriving
	// through a type-inference side effect. The clone absorbs it.
	clone := cloneTerm(normalized)
	tags := egAnnotate(chk, normalized, clone)
	if len(tags) == 0 {
		return normalized, nil
	}

	g := newEgraph(b)
	root, ok := g.insert(normalized, tags)
	if !ok {
		return normalized, g
	}
	g.rebuild()

	acTag := map[string]string{}
	for _, tag := range tags {
		acTag[egACSym("+", tag)] = tag
		acTag[egACSym("*", tag)] = tag
	}
	g.saturate(acTag)

	res := g.extract(root)
	if res == nil {
		return normalized, g
	}
	return res, g
}

// egSurvey reports a normalized term's node count, whether any distributivity
// or factoring rule COULD fire on it syntactically, and whether every
// structural child slot is populated.
//
// THE PRE-CHECK IS CONSERVATIVE, AND THE DIRECTION MATTERS MORE THAN THE WORD.
//
//	NO FALSE NEGATIVES  it never skips a term some rule could fire on, which is
//	                    the only direction that could change an answer. At
//	                    insertion every class is a singleton, so a rule can
//	                    only match a shape the term already HAS; and the only
//	                    thing that creates new matches is a rule firing. If
//	                    nothing matches here, saturation would add nothing.
//	FALSE POSITIVES     it admits terms where no rule turns out to fire, and
//	                    those cost a wasted e-graph. Two reasons, both
//	                    deliberate: the factoring test asks whether a sum has
//	                    two PRODUCTS under it, not whether those products SHARE
//	                    A FACTOR — deciding that here would be a second copy of
//	                    ruleFactor's matching, free to disagree with it — and
//	                    operand types are not consulted, so a Float chain
//	                    passes here and is stopped by the annotation, which
//	                    keeps the one authority on operand types in one place.
//
// Measured on the committed corpus: 34 bodies are admitted and 0 fire a rule.
// So this is a cheap filter in front of an expensive engine, not a decision
// procedure for "is there a redex".
//
// A nil child slot means the term cannot be encoded at all (enc.term
// dereferences it), so there is nothing to canonicalize and the pass declines.
//
// TWO PASSES, AND THE SPLIT IS THE POINT. Sizing is cheap and bounded; matching
// walks AC chains. Doing them together meant the expensive half ran on terms the
// cheap half was about to reject — so a 65,536-node arithmetic chain, which the
// portable profile admits and `find --equiv` reaches, was fully surveyed before
// anything looked at the 2048-node cap. Found by external review. The size pass
// now ABORTS at the cap, so the matching pass only ever sees a term small enough
// to be worth matching.
func egSurvey(t *Term) (nodes int, arith bool, wellFormed bool) {
	wellFormed = true
	stack := []*Term{t}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++
		if nodes > egMaxTermNodes {
			// Over the cap: the caller skips on size alone, so `arith` is not
			// consulted and the remaining nodes need not be counted exactly.
			return nodes, false, wellFormed
		}
		for _, s := range egSlots(n) {
			c := egSlotGet(n, s)
			if c == nil {
				wellFormed = false
				continue
			}
			stack = append(stack, c)
		}
	}

	// ONE FLATTEN PER MAXIMAL CHAIN, the same discipline eNormalize's chainOp
	// carries. A node whose parent already flattens through it is an INTERIOR
	// node, and its leaves are a subset of the root's — so surveying it again
	// can find nothing new and costs another walk of the chain. Flattening at
	// every level is what made this quadratic.
	type item struct {
		t       *Term
		chainOp string
	}
	work := []item{{t: t}}
	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		n := it.t
		childChain := ""
		if n.K == "prim" && len(n.Args) == 2 && (n.Op == "+" || n.Op == "*") {
			childChain = n.Op
			if it.chainOp != n.Op {
				leaves := egFlatten(n.Op, n)
				hits := 0
				for _, l := range leaves {
					if l.K != "prim" || len(l.Args) != 2 {
						continue
					}
					if n.Op == "*" && l.Op == "+" {
						arith = true // a product over a sum: distribute
					}
					if n.Op == "+" && l.Op == "*" {
						hits++
					}
				}
				if hits >= 2 {
					arith = true // two products under one sum: a factor may be common
				}
			}
		}
		for _, s := range egSlots(n) {
			if c := egSlotGet(n, s); c != nil {
				work = append(work, item{t: c, chainOp: childChain})
			}
		}
	}
	return nodes, arith, wellFormed
}

// egAnnotate decides, for every `+`/`*` node of the term, whether that node is
// associative-commutative at its operand type AND that type is exact (Int or
// Rat). It returns the type tag keyed by the node in the ORIGINAL term, having
// done every synth against the parallel node in the clone.
//
// isACPrim is the authority for "may this operator be re-associated here"; this
// narrows its answer to the exact numeric types, because `and`/`or` over Bool
// are AC but have no distributive rule here.
func egAnnotate(chk *checkerMachine, orig, clone *Term) map[*Term]string {
	tags := map[*Term]string{}
	type item struct {
		o, c *Term
		ctx  *ctxList
	}
	stack := []item{{orig, clone, nil}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		o, c := it.o, it.c
		if o == nil || c == nil {
			continue
		}
		if o.K == "prim" && len(o.Args) == 2 && (o.Op == "+" || o.Op == "*") {
			if argTy, err := chk.synth(ctxSlice(it.ctx), &c.Args[0]); err == nil &&
				isACPrim(o.Op, argTy) && (argTy.K == "int" || argTy.K == "rat") {
				tags[o] = argTy.K
			}
		}
		slots := egSlots(o)
		for i := len(slots) - 1; i >= 0; i-- {
			s := slots[i]
			oc, cc := egSlotGet(o, s), egSlotGet(c, s)
			if oc == nil || cc == nil {
				continue
			}
			stack = append(stack, item{oc, cc, egChildCtx(chk, c, it.ctx, s)})
		}
	}
	return tags
}

// egChildCtx extends the de Bruijn context the way the binder at this slot
// does. It mirrors eNormalize's context threading — a lambda's body sees its
// parameter, a let's body sees its bound type, and a match arm sees the
// constructor's fields — and reads them through the same helpers, so a change
// to how a binder's type is computed cannot be applied to one and not the
// other.
func egChildCtx(chk *checkerMachine, t *Term, ctx *ctxList, s egSlot) *ctxList {
	switch t.K {
	case "lam":
		return ctxExtend(ctx, t.Ty)
	case "let":
		if s.field == 'B' {
			return ctxExtend(ctx, t.Ty)
		}
	case "match":
		if s.field == 'm' && chk.st != nil {
			scrutTy, terr := chk.synth(ctxSlice(ctx), t.A)
			md, derr := chk.st.GetDef(t.Hash)
			if terr == nil && derr == nil && scrutTy.K == "data" && s.idx < len(md.Ctors) {
				armCtx := ctx
				for _, f := range instCtorFields(md, scrutTy.Hash, scrutTy.Args, s.idx) {
					armCtx = ctxExtend(armCtx, f)
				}
				return armCtx
			}
		}
	}
	return ctx
}

// egFrame is one pending insertion: a node whose children are being inserted.
type egFrame struct {
	sym  string
	tmpl *Term
	ac   bool
	acOp string
	kids []*Term
	out  []egClass
	next int
	dst  *egClass
}

// insert builds the initial graph from a term. ITERATIVE: `eHash` is reachable
// from `find --equiv` on terms the portable profile admits, and a normalized
// chain is as deep as the term that produced it.
func (g *egraph) insert(root *Term, tags map[*Term]string) (egClass, bool) {
	var out egClass
	frames := []*egFrame{g.frame(root, tags, &out)}
	for len(frames) > 0 {
		f := frames[len(frames)-1]
		if f.next < len(f.kids) {
			i := f.next
			f.next++
			frames = append(frames, g.frame(f.kids[i], tags, &f.out[i]))
			continue
		}
		frames = frames[:len(frames)-1]
		c, ok := g.add(egNode{sym: f.sym, tmpl: f.tmpl, ac: f.ac, acOp: f.acOp, args: f.out})
		if !ok {
			return 0, false
		}
		*f.dst = c
	}
	return out, true
}

func (g *egraph) frame(t *Term, tags map[*Term]string, dst *egClass) *egFrame {
	if tag, ok := tags[t]; ok {
		// An AC chain enters as ONE n-ary node over its leaves. The interior
		// nodes of the chain are not inserted at all: they are an artefact of
		// writing an associative operator with a binary syntax, and keeping
		// them would make `(a+b)+c` and `a+(b+c)` different graphs.
		leaves := egFlatten(t.Op, t)
		return &egFrame{
			sym:  egACSym(t.Op, tag),
			ac:   true,
			acOp: t.Op,
			kids: leaves,
			out:  make([]egClass, len(leaves)),
			dst:  dst,
		}
	}
	shape := egShape(t)
	slots := egSlots(t)
	kids := make([]*Term, len(slots))
	for i, s := range slots {
		kids[i] = egSlotGet(t, s)
	}
	return &egFrame{
		sym:  "t|" + string(termBytes(shape)),
		tmpl: shape,
		kids: kids,
		out:  make([]egClass, len(kids)),
		dst:  dst,
	}
}
