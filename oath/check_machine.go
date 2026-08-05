package main

import "fmt"

// THE EXPLICIT CHECKER MACHINE (#149, step 2 — types only, NOT routed).
//
// Nothing in this file is reachable from production yet. It exists so the port
// can proceed one expression family at a time against the frozen differential
// fixture (oath/testdata/checker-differential.json) rather than as one leap
// across 36 mutually recursive call sites.
//
// WHY A MACHINE AT ALL. The recursive checker maps a Term's REPRESENTATION
// DEPTH onto the host call stack, so an admitted structure — inside the portable
// profile, shallow in syntax — can still exhaust it. A 5,000-rune string literal
// is one syntax node and a 5,000-long SCons spine, and it takes the checker
// down with it on wasm. The resource profile bounds WORK; only an explicit stack
// bounds STACK.
//
// WHY EXPLICIT CONTINUATIONS AND NOT A GENERIC WALKER. Several consumers need
// post-order results, path state, early refusal, or reconstruction; a
// "visit every node" callback cannot express them. Each frame variant below
// corresponds to a continuation ALREADY IMPLICIT at a call site in check.go —
// "left child synthesized, now check the right", "constructor argument i
// completed", "substitution solved, publish it". That correspondence is what
// makes the migration auditable: one old recursive call becomes one explicit
// transition, rather than disappearing into a universal abstraction.

// checkMode is the direction of the bidirectional judgement.
type checkMode uint8

const (
	modeSynth checkMode = iota // infer a type for the term
	modeCheck                  // check the term against an expected type
)

func (m checkMode) String() string {
	if m == modeSynth {
		return "synth"
	}
	return "check"
}

// checkResult is what a completed sub-judgement hands back to its continuation.
//
// A SYNTHESIS FAILURE IS A VALUE HERE, NOT A CONTROL TRANSFER, and that is
// load-bearing rather than stylistic: synthCtor's inference pass deliberately
// SUPPRESSES failures — an argument that cannot be synthesized is skipped for
// inference and left for the validation pass to diagnose. A machine that
// unwound on the first error would reject programs the current checker accepts,
// and would change which diagnostic wins for programs it rejects. Frames decide
// what an error means; the machine does not decide for them.
type checkResult struct {
	ty  *Ty   // the synthesized type, when mode was modeSynth and err is nil
	err error // the sub-judgement's failure, for the FRAME to interpret
}

// checkerStep is one pending judgement: what to do next, and who resumes when
// it finishes.
type checkerStep struct {
	mode checkMode
	ctx  []*Ty
	term *Term
	exp  *Ty // modeCheck only
	cont checkerFrame
}

// frameOutcome is what a resumed frame decides to do: run another sub-judgement,
// or finish and hand a result to its own parent. Both are needed and step 2's
// shape could only express the first — a frame that returned "the next step"
// had no way to say "I am done", which is most of what a continuation does.
type frameOutcome struct {
	next *checkerStep // run this child next; the frame stays on the stack
	done *checkResult // the frame is finished; pop it and hand this upward
}

// checkerFrame is a suspended computation waiting on one sub-judgement.
type checkerFrame interface {
	resume(m *checkerMachine, r checkResult) (frameOutcome, error)
	// describe names the continuation for diagnostics and for the port's own
	// tracing. Frames are the port's audit trail; an unnamed one is a step
	// nobody can locate when the differential moves.
	describe() string
}

// checkerMachine owns the explicit stack and the accounting that must survive
// the port.
type checkerMachine struct {
	st         *Store
	selfTyVars int
	selfTy     *Ty

	// stack replaces the host call stack. Depth costs heap, which is
	// observable and boundable, rather than an embedder resource Go can
	// neither measure nor grow.
	stack []checkerFrame

	// inferEntries counts entries into constructor type-argument inference and
	// MUST reproduce the recursive checker's count exactly, per case, in the
	// differential fixture. It is the complexity witness: a machine that loses
	// the memo effect of publishing TyArgs mid-flight while reconstructing the
	// same final term is semantically correct and exponential, and output
	// equality cannot see that. Visible at n=1 as 2 entries against 1.
	inferEntries int
}

// --- the constructor routes ---------------------------------------------
//
// synthCtor has TWO routes and the port must not collapse them. Keeping them
// named makes it structurally harder to apply the three-stage protocol to Str,
// or to skip it for a polymorphic constructor.

type ctorRoute uint8

const (
	// routeValidateOnly — the MONOMORPHIC path. A datatype with no type
	// parameters, or a constructor whose type arguments were written
	// explicitly, has nothing to infer: there is ONE pass over the arguments,
	// checking each against its field type.
	//
	// This is the route a string literal takes. `Str` is (data Str [] ...), so
	// tyvars = 0 and inference never runs however long the literal is — which
	// is why the 5,000-rune witness is a pure STACK-SAFETY case with no
	// inference involved, and cannot witness anything about the memo.
	routeValidateOnly ctorRoute = iota

	// routeInferSolveValidate — the POLYMORPHIC path, three stages, every
	// boundary semantic:
	//
	//	1. INFER     synthesize every argument; collect successes; SUPPRESS
	//	             failures; preserve traversal order.
	//	2. SOLVE     require a complete substitution; publish it to t.TyArgs
	//	             IMMEDIATELY.
	//	3. VALIDATE  check every argument against the substituted field type;
	//	             re-entry into a child observes its populated TyArgs and
	//	             skips inference.
	//
	// The publication in stage 2 is not cached output, it is CONTROL STATE: it
	// is what makes stage 3's re-entry skip inference. Without it the
	// recurrence is T(n) = 2*T(n-1), and a spine capped at 512 by
	// maxSyntaxNesting makes 2^512 a hang rather than a slow path.
	routeInferSolveValidate
)

func (r ctorRoute) String() string {
	if r == routeValidateOnly {
		return "validate-only"
	}
	return "infer-solve-validate"
}

// ctorRouteFor decides which route a constructor application takes.
//
// IT DERIVES THE DECISION FROM inferReady RATHER THAN RESTATING IT. A second
// copy of `tyvars > 0 && len(tyargs) == 0` would be correct exactly once: the
// machine and the recursive checker could then disagree about which route a
// constructor takes, and the differential would report a moved hash with no
// indication that the ROUTE was the thing that diverged.
func ctorRouteFor(d *Def, t *Term) ctorRoute {
	if inferReady(d.TyVars, t.TyArgs) {
		return routeInferSolveValidate
	}
	return routeValidateOnly
}

// --- frames -------------------------------------------------------------
//
// One variant per continuation that exists in check.go today. resume bodies
// arrive with step 3, family by family; the shapes are fixed here so the
// fixture can be run against each family as it lands.

// ctorInferFrame is stage 1: argument idx has been synthesized.
//
// It holds subst because the substitution accumulates ACROSS arguments — a type
// parameter may be determined by argument 3 after arguments 1 and 2 left it
// open, so the frame carries partial state rather than recomputing.
type ctorInferFrame struct {
	def       *Def
	term      *Term
	exp       *Ty
	ctx       []*Ty
	rawFields []*Ty
	subst     []*Ty
	idx       int
}

func (f *ctorInferFrame) describe() string { return "ctor:infer" }

// ctorSolveFrame is stage 2: every argument has been visited and the
// substitution is complete or it is not. Separate from stage 1 so that
// PUBLICATION has its own step and cannot drift to a different moment.
type ctorSolveFrame struct {
	def   *Def
	term  *Term
	ctx   []*Ty
	subst []*Ty
}

func (f *ctorSolveFrame) describe() string { return "ctor:solve+publish" }

// ctorValidateFrame is stage 3, and is also the WHOLE of routeValidateOnly.
// Shared deliberately: the monomorphic route is exactly "validation with no
// inference before it", and giving it a private copy would invite the two to
// drift apart.
type ctorValidateFrame struct {
	def    *Def
	term   *Term
	ctx    []*Ty
	fields []*Ty
	idx    int
	route  ctorRoute // recorded so a trace says which path produced this pass
}

func (f *ctorValidateFrame) describe() string { return "ctor:validate(" + f.route.String() + ")" }

// The remaining families, named now so the port has a checklist rather than a
// search. Each corresponds to a recursive call in check.go.
type appArgFrame struct { // application: head done, argument pending
	ctx  []*Ty
	head *Term
	args []*Term
	exp  *Ty
	idx  int
}

func (f *appArgFrame) describe() string { return "app:arg" }

type ifBranchFrame struct { // if: condition or a branch done
	ctx  []*Ty
	term *Term
	exp  *Ty
	part string // cond | then | else
}

func (f *ifBranchFrame) describe() string { return "if:" + f.part }

type letBodyFrame struct { // let: bound expression done, body pending
	ctx  []*Ty
	term *Term
	exp  *Ty
}

func (f *letBodyFrame) describe() string { return "let:body" }

type matchArmFrame struct { // match: scrutinee or arm i done
	ctx      []*Ty
	term     *Term
	exp      *Ty
	scrutTy  *Ty
	idx      int
	scrutine bool // true while the scrutinee itself is outstanding
}

func (f *matchArmFrame) describe() string { return "match:arm" }

type primArgFrame struct { // primitive: argument i done
	ctx  []*Ty
	term *Term
	idx  int
}

func (f *primArgFrame) describe() string { return "prim:arg" }

type recordFieldFrame struct { // record literal: field i done
	ctx  []*Ty
	term *Term
	exp  *Ty
	idx  int
}

func (f *recordFieldFrame) describe() string { return "record:field" }

// --- resume stubs -------------------------------------------------------
//
// Step 2 is INERT: the shapes are fixed, the transitions arrive with step 3.
// These return an error rather than panicking so that if anything ever routes
// here before its family is ported, the failure is a legible message in the
// differential rather than a crash with a stack trace pointing at the machine
// that was supposed to remove stack traces.

func notPorted(f checkerFrame) (frameOutcome, error) {
	return frameOutcome{}, errFramePending{f.describe()}
}

type errFramePending struct{ frame string }

func (e errFramePending) Error() string {
	return "checker machine: continuation " + e.frame + " is declared but not yet ported (#149 step 3)"
}

func (f *ctorInferFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *ctorSolveFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *ctorValidateFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *appArgFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) { return notPorted(f) }
func (f *ifBranchFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *letBodyFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *matchArmFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *primArgFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}
func (f *recordFieldFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
	return notPorted(f)
}

// --- step 3: the run loop, and the first ported family -------------------
//
// PORTED SO FAR: synthesis of leaves (var, int, rat, float, bool) and check's
// DEFAULT path — synthesize, then compare against the expected type.
//
// Chosen as the smallest family that exercises continuation plumbing at all.
// Leaves alone would not: they have no children, so nothing suspends. The
// default check path is a genuine continuation — check suspends, synth runs, a
// frame resumes and compares — while touching neither inference nor mutation,
// which is what makes a divergence here attributable to the machinery rather
// than to the constructor protocol.
//
// Everything else refuses with errFamilyNotPorted. It does NOT fall back to the
// recursive checker: a silent fallback would let the differential pass while
// measuring the old machine, which is the failure this whole port is guarding
// against.

type errFamilyNotPorted struct {
	kind string
	mode checkMode
}

func (e errFamilyNotPorted) Error() string {
	return "checker machine: " + e.mode.String() + " of `" + e.kind + "` is not yet ported (#149 step 3)"
}

// checkCompareFrame is check's default path: the term was synthesized, and its
// type must now equal the expected one.
//
// It is a FRAME rather than an inline comparison because the synthesis it waits
// on may itself suspend arbitrarily deep once further families land.
type checkCompareFrame struct{ exp *Ty }

func (f *checkCompareFrame) describe() string { return "check:compare" }

func (f *checkCompareFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	if r.err != nil {
		return frameOutcome{done: &checkResult{err: r.err}}, nil
	}
	if !tyEq(r.ty, f.exp) {
		// Byte-identical to check.go's message: the differential compares a
		// diagnostic CATEGORY, but a needless rewording is still a change
		// nobody asked for.
		return frameOutcome{done: &checkResult{
			err: fmt.Errorf("expected %s, got %s", debugTy(f.exp), debugTy(r.ty))}}, nil
	}
	return frameOutcome{done: &checkResult{}}, nil
}

// leaf produces a result for a step that needs no children, or reports that the
// step is not yet portable. ok=false with a nil error means "this step has
// children" — which no ported family reaches yet.
func (m *checkerMachine) leaf(s checkerStep) (checkResult, bool, error) {
	if s.term == nil {
		return checkResult{err: fmt.Errorf("missing term")}, true, nil
	}
	if s.mode == modeCheck {
		// Only the DEFAULT path is ported. Forms that consume an expected type
		// directly (ctor, lam, if, let, match, app-with-inference) are not, and
		// must not be approximated by synthesize-then-compare — that would
		// change which programs typecheck, not merely how.
		switch s.term.K {
		case "var", "int", "rat", "float", "bool":
			return checkResult{}, false, nil // handled by the caller pushing a compare frame
		default:
			return checkResult{}, false, errFamilyNotPorted{s.term.K, modeCheck}
		}
	}
	switch s.term.K {
	case "var":
		if s.term.Idx < 0 || s.term.Idx >= len(s.ctx) {
			return checkResult{err: fmt.Errorf("variable index %d out of scope", s.term.Idx)}, true, nil
		}
		return checkResult{ty: s.ctx[len(s.ctx)-1-s.term.Idx]}, true, nil
	case "int":
		return checkResult{ty: tInt()}, true, nil
	case "rat":
		return checkResult{ty: tRat()}, true, nil
	case "float":
		return checkResult{ty: tFloat()}, true, nil
	case "bool":
		return checkResult{ty: tBool()}, true, nil
	}
	return checkResult{}, false, errFamilyNotPorted{s.term.K, modeSynth}
}

// run drives the machine to a result. The explicit stack is the whole point:
// depth costs heap here, not an embedder resource Go can neither measure nor
// grow.
func (m *checkerMachine) run(start checkerStep) (*Ty, error) {
	s := start
	for {
		// A check of a ported leaf becomes: synth it, then compare.
		if s.mode == modeCheck {
			if _, _, err := m.leaf(s); err != nil {
				return nil, err
			}
			m.stack = append(m.stack, &checkCompareFrame{exp: s.exp})
			s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term}
			continue
		}
		r, done, err := m.leaf(s)
		if err != nil {
			return nil, err
		}
		if !done {
			return nil, errFamilyNotPorted{s.term.K, s.mode}
		}
		// Unwind: hand the result to each waiting frame until one asks for
		// another child or the stack empties.
		for {
			if len(m.stack) == 0 {
				return r.ty, r.err
			}
			f := m.stack[len(m.stack)-1]
			m.stack = m.stack[:len(m.stack)-1]
			out, ferr := f.resume(m, r)
			if ferr != nil {
				return nil, ferr
			}
			if out.next != nil {
				m.stack = append(m.stack, f) // the frame is not finished
				s = *out.next
				break
			}
			r = *out.done
		}
	}
}
