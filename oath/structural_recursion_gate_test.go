package main

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE STRUCTURAL RECURSION GATE (#149).
//
// POLICY, stated before the detector so the detector can be judged against it:
//
//	FORBIDDEN  a function in a REPAIRED component invokes itself — directly or
//	           through a cycle — with a Term or Ty argument derived from a
//	           structural CHILD of its own Term/Ty parameter.
//
//	PERMITTED  the checker machine scheduling child terms through frames;
//	           the encoder's and walker's explicit work stacks;
//	           recursion over unrelated data structures;
//	           numeric recursion on an explicit decreasing scalar;
//	           the retained recursive checker ORACLE, which lives in test files.
//
// The point is not to ban recursion. It is to ban the ONE shape that maps a
// term's representation depth onto the host call stack, in the components that
// were repaired precisely because they did that.
//
// SCOPE IS DELIBERATELY NARROW AND NAMED. Components that still recurse
// structurally are listed in `stillRecursive` with the reason, so the gate never
// implies more than it checks — an allowlist that silently grows is how a gate
// stops meaning anything.
var guardedFiles = map[string]string{
	"check_machine.go": "the explicit checker machine",
	"termination.go":   "the termination walker",
}

// stillRecursive records what is NOT covered, with why. Each entry is a claim
// that can be checked, not a waiver.
var stillRecursive = map[string]string{
	"enc.ty": "Ty depth is bounded by maxSyntaxNesting (512) for elaborated defs",
	"dec.ty": "same bound, but see dec.term — the decoder is NOT protected by the reader",
	"dec.term": "UNREPAIRED: decodeDef runs BEFORE admitDef, so a crafted stored object " +
		"overflows before its node count is checked. Reachable on a hosted store; not on " +
		"the playground, whose corpus snapshot is fixed and committed.",
	"eNormalize": "UNREPAIRED, and FOUND BY THIS GATE on its first run rather than by " +
		"reading. The e-graph normalizer descends Terms structurally. Reached only by " +
		"`find --equiv` (eHash), so it is OUTSIDE the oathCheck boundary #149 closed — " +
		"but it is a real exposure on that entry point, not a bounded exception.",
}

// NOTE ON THE TWO "UNREPAIRED" ENTRIES. They are recorded, not waived: each says
// what is reachable and from where. A bounded exception (enc.ty, dec.ty) states
// its BOUND; an unrepaired one states its EXPOSURE. Collapsing the two
// categories is how an allowlist stops being evidence — the entries would all
// read as "known and fine" when half of them mean "known and not fine".

// structuralChildArg reports whether an expression is a structural child of one
// of the named Term/Ty parameters — `t.A`, `&t.Args[i]`, `t.Ty`, and so on.
func structuralChildArg(e ast.Expr, params map[string]bool) bool {
	switch v := e.(type) {
	case *ast.UnaryExpr:
		return structuralChildArg(v.X, params)
	case *ast.IndexExpr:
		return structuralChildArg(v.X, params)
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return params[id.Name] // t.A, t.Args, t.Ty ...
		}
		return structuralChildArg(v.X, params)
	}
	return false
}

// termOrTyParams collects the parameters of Term/Ty type, which are the only
// ones a structural descent can be rooted at.
func termOrTyParams(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	add := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			t := f.Type
			if s, ok := t.(*ast.StarExpr); ok {
				t = s.X
			}
			id, ok := t.(*ast.Ident)
			if !ok || (id.Name != "Term" && id.Name != "Ty") {
				continue
			}
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	add(fn.Type.Params)
	if fn.Recv != nil {
		add(fn.Recv)
	}
	return out
}

func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		t := fn.Recv.List[0].Type
		if s, ok := t.(*ast.StarExpr); ok {
			t = s.X
		}
		if id, ok := t.(*ast.Ident); ok {
			return id.Name + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

// callTargets returns the package-level names this call could reach: `f(...)`
// and `x.f(...)` both reduce to a trailing identifier.
func callTarget(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

type fnInfo struct {
	key    string
	file   string
	params map[string]bool
	calls  map[string][]ast.Expr // callee short name -> flattened argument lists
}

// findStructuralRecursion returns violations: (function, callee) pairs where a
// guarded function passes a structural child into a call that can return to it.
func findStructuralRecursion(t *testing.T, files map[string]string) []string {
	t.Helper()
	infos := map[string]*fnInfo{}
	byShort := map[string][]string{}

	for f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		file, err := parser.ParseFile(gotoken.NewFileSet(), f, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			info := &fnInfo{key: funcKey(fn), file: f,
				params: termOrTyParams(fn), calls: map[string][]ast.Expr{}}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if tgt := callTarget(c); tgt != "" {
					info.calls[tgt] = append(info.calls[tgt], c.Args...)
				}
				return true
			})
			infos[info.key] = info
			short := info.key
			if i := strings.Index(short, "."); i >= 0 {
				short = short[i+1:]
			}
			byShort[short] = append(byShort[short], info.key)
		}
	}

	var bad []string
	for key, info := range infos {
		if len(info.params) == 0 {
			continue
		}
		short := key
		if i := strings.Index(short, "."); i >= 0 {
			short = short[i+1:]
		}
		for callee, args := range info.calls {
			// "THE CALLEE CAN REACH ME AGAIN", computed over the TRANSITIVE
			// call graph rather than one hop.
			//
			// An earlier version checked only direct recursion and two-function
			// cycles, which is narrower than the policy this gate states. A
			// three-function cycle — walkA(t.A) -> walkB(t) -> walkC(t) ->
			// walkA(t) — passed it, so a future structural descent could
			// reintroduce host-stack exhaustion without failing. The gate's
			// universe has to be the one its claim quantifies over.
			if !reachesBack(infos, byShort, callee, short) {
				continue
			}
			for _, a := range args {
				if structuralChildArg(a, info.params) {
					bad = append(bad, info.file+": "+key+" -> "+callee)
					break
				}
			}
		}
	}
	return bad
}

// reachesBack reports whether `from` can reach `target` through any chain of
// calls, so a cycle of ANY length is recognised.
func reachesBack(infos map[string]*fnInfo, byShort map[string][]string, from, target string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(cur string) bool {
		if cur == target {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		for _, key := range byShort[cur] {
			info := infos[key]
			if info == nil {
				continue
			}
			for callee := range info.calls {
				if walk(callee) {
					return true
				}
			}
		}
		return false
	}
	return walk(from)
}

func TestNoStructuralRecursionInRepairedComponents(t *testing.T) {
	files := map[string]string{}
	for f, why := range guardedFiles {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("guarded file %s is missing (%s); the gate did not run", f, why)
		}
		files[f] = why
	}
	// canon.go is guarded for `term` only; `ty` is a recorded exception, so the
	// file is scanned but the finding is filtered below.
	files["canon.go"] = "the canonical encoder (term only)"

	bad := findStructuralRecursion(t, files)
	var unexpected []string
	for _, b := range bad {
		exempt := false
		for name := range stillRecursive {
			if strings.Contains(b, name) {
				exempt = true
			}
		}
		if !exempt {
			unexpected = append(unexpected, b)
		}
	}
	for _, u := range unexpected {
		t.Errorf("STRUCTURAL RECURSION in a repaired component: %s\n"+
			"  These components were rewritten because descending a Term on the host\n"+
			"  stack made an admitted 5,000-rune string crash the kernel (#149).\n"+
			"  Schedule the child on the explicit stack instead. If this recursion is\n"+
			"  genuinely bounded, add it to stillRecursive WITH THE BOUND.", u)
	}
	if len(unexpected) == 0 {
		t.Logf("no structural recursion across %d guarded files (%d recorded exceptions)",
			len(files), len(stillRecursive))
	}
}

// TestStructuralGateDiscriminates is the control, in BOTH directions: the gate
// must fire on the shapes it forbids and stay silent on the shapes it permits.
// A detector that only ever passes is indistinguishable from one that reads
// nothing.
func TestStructuralGateDiscriminates(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("package main\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name string
		src  string
		want bool // want a violation
	}{
		{"direct-child-recursion", `
func walkX(t *Term) { walkX(t.A) }`, true},
		{"child-through-index", `
func walkY(t *Term) { for i := range t.Args { walkY(&t.Args[i]) } }`, true},
		{"indirect-two-function-cycle", `
func walkP(t *Term) { walkQ(t.A) }
func walkQ(t *Term) { walkP(t) }`, true},
		// THREE functions: the shape an earlier version of this gate missed,
		// because it only looked one hop for a call back.
		{"indirect-three-function-cycle", `
func walkA(t *Term) { walkB(t.A) }
func walkB(t *Term) { walkC(t) }
func walkC(t *Term) { walkA(t) }`, true},
		{"indirect-four-function-cycle", `
func walkE(t *Term) { walkF(t.A) }
func walkF(t *Term) { walkG(t) }
func walkG(t *Term) { walkH(t) }
func walkH(t *Term) { walkE(t) }`, true},
		// A chain that does NOT come back must stay silent.
		{"non-cyclic-chain-permitted", `
func walkI(t *Term) { walkJ(t.A) }
func walkJ(t *Term) { walkK(t) }
func walkK(t *Term) { _ = t }`, false},
		{"ty-child-recursion", `
func walkT(t *Ty) { walkT(t.A) }`, true},
		{"iterative-scheduling-permitted", `
func walkZ(t *Term) {
	stack := []*Term{t}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		stack = append(stack, cur.A, cur.B)
	}
}`, false},
		{"numeric-measure-recursion-permitted", `
func countdown(t *Term, n int) int { if n == 0 { return 0 }; return countdown(t, n-1) }`, false},
		{"unrelated-structure-permitted", `
type node struct{ next *node }
func walkList(t *Term, n *node) { if n != nil { walkList(t, n.next) } }`, false},
		{"self-call-without-child-permitted", `
func walkS(t *Term) { walkS(t) }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(tc.name+".go", tc.src)
			got := findStructuralRecursion(t, map[string]string{p: "probe"})
			if (len(got) > 0) != tc.want {
				t.Errorf("want violation=%v, got %v", tc.want, got)
			}
		})
	}
}

// TestEveryContinuationGoesThroughPush makes the frame counter's invariant
// ENFORCEABLE rather than a comment.
//
// framesPushed is documented as counting every continuation, and
// TestPortedFamilyUsesTheExplicitStack uses it as evidence that a family
// actually suspends. A frame installed by a direct `m.stack = append(...)` is
// invisible to it, so the counter would silently understate — and the test
// resting on it would silently weaken.
//
// That is not hypothetical: `primArgFrame` was installed directly for exactly
// as long as this check did not exist, because the mechanical conversion to
// m.push matched only single-line composite literals. Found by external review,
// not by any local gate.
func TestEveryContinuationGoesThroughPush(t *testing.T) {
	src, err := os.ReadFile("check_machine.go")
	if err != nil {
		t.Fatalf("reading check_machine.go: %v", err)
	}
	file, err := parser.ParseFile(gotoken.NewFileSet(), "check_machine.go", src, 0)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	direct := 0
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		if fn.Name.Name == "push" {
			return false // push IS the one permitted append
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 {
				return true
			}
			sel, ok := as.Lhs[0].(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "stack" {
				return true
			}
			// Popping (`m.stack = m.stack[:n]`) is fine; APPENDING is not.
			for _, r := range as.Rhs {
				if c, ok := r.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "append" {
						t.Errorf("%s installs a continuation via a direct append; use m.push "+
							"so framesPushed counts it", fn.Name.Name)
						direct++
					}
				}
			}
			return true
		})
		return false
	})
	if direct == 0 {
		t.Log("every continuation is installed through m.push")
	}
}
