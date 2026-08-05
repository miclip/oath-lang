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
// appArgFrame sequences one application node. Applications are CURRIED — one
// argument per node — so there is no arity check here: applying too many
// arguments surfaces as "applied a non-function" at the node where the callee
// has stopped being one.
//
// THE ARGUMENT IS SYNTHESIZED AND COMPARED, NOT CHECKED against the parameter
// type. That is not the same thing: a term that only CHECKS — a bare
// constructor whose type arguments the parameter would determine — fails here
// today. A port that "improves" this to a check would accept programs the
// current checker rejects.
type appArgFrame struct {
	ctx  []*Ty
	term *Term
	fnTy *Ty    // the callee's type, once synthesized
	part string // fn | arg
}

func (f *appArgFrame) describe() string { return "app:" + f.part }

// ifBranchFrame sequences an `if`: condition, then-branch, else-branch, in that
// order. One frame advances through the three parts rather than three frame
// types, because the ORDER is the invariant and splitting it across types would
// let a later edit reorder them without anything reading as wrong.
//
// The two modes differ in a way that is easy to collapse and must not be:
//
//	synth   cond must be Bool; SYNTHESIZE both branches; they must agree; the
//	        type is the then-branch's.
//	check   cond must be Bool; CHECK BOTH BRANCHES AGAINST exp — never the
//	        else-branch against the then-branch's inferred type.
//
// A machine that checks the else against the then's type accepts programs this
// checker rejects: `(if c 1 2)` against Bool fails on the then-branch today,
// but would pass if the else were merely made to agree with the then.
type ifBranchFrame struct {
	ctx    []*Ty
	term   *Term
	exp    *Ty    // nil in synth mode
	part   string // cond | then | else
	thenTy *Ty    // synth mode only: carried from the then-branch to the else
}

func (f *ifBranchFrame) describe() string { return "if:" + f.part }

// letBodyFrame sequences a `let`: the bound expression, then the body under an
// EXTENDED context.
//
// CONTEXT RESTORATION IS STRUCTURAL, NOT AN ACTION. The frame stores the
// ORIGINAL ctx and derives the body's as pushCtx(f.ctx, t.Ty); nothing is ever
// popped, because nothing was ever mutated. A sibling that runs after this let
// resumes from its own frame's ctx and cannot see the binding — there is no
// machine-level context to leak.
//
// That is a deliberate design property and it is still TESTED, because a later
// refactor could move ctx onto the machine and the leak would be silent: the
// witness is an `if` whose then-branch contains a let and whose else-branch
// resolves the SAME de Bruijn index to a different type.
//
// The two modes differ in the bound expression AND in the diagnostic:
//
//	synth   SYNTHESIZE the bound value, compare to the annotation
//	        -> "let annotation mismatch: declared X, got Y"
//	check   CHECK the bound value against the annotation
//	        -> "expected X, got Y"
type letBodyFrame struct {
	ctx  []*Ty
	term *Term
	exp  *Ty    // nil in synth mode
	part string // bound | body
}

func (f *letBodyFrame) describe() string { return "let:" + f.part }

type matchArmFrame struct { // match: scrutinee or arm i done
	ctx      []*Ty
	term     *Term
	exp      *Ty
	scrutTy  *Ty
	idx      int
	scrutine bool // true while the scrutinee itself is outstanding
}

func (f *matchArmFrame) describe() string { return "match:arm" }

// primArgFrame is INDEXED TRAVERSAL: synthesize every argument left to right,
// accumulate their types, then apply the operator's rules once.
//
// The loop state is explicit — index, accumulated types, the original argument
// list — so the ways this family goes wrong are all visible as frame state
// rather than as control flow: skipping an argument, visiting one twice,
// advancing the index before the result is consumed, or pairing argument i with
// slot i+1.
//
// THE FIRST FAILURE ABORTS. Unlike the constructor inference pass, nothing here
// is suppressed, so the diagnostic belongs to the LEFTMOST failing argument
// however many others would also fail.
//
// The operator rules themselves are NOT reimplemented here: primResultTy is the
// same function the recursive checker calls. Eleven operator cases copied into
// a second implementation would compare the cases their author remembered.
type primArgFrame struct {
	ctx    []*Ty
	term   *Term
	idx    int
	argTys []*Ty
}

func (f *primArgFrame) describe() string { return "prim:arg" }

// eqFrame is `==`, which is a RECOVERY machine rather than a traversal — the
// reason it was excluded from prim rather than folded into it.
//
// `==` is polymorphic in its operand type, so an operand may be a bare
// constructor whose type arguments only the OTHER operand determines. Four
// paths, and they are not symmetric:
//
//	left synthesizes            -> check RIGHT against left's type
//	left fails, right succeeds  -> check LEFT against right's type
//	both fail                   -> a FIXED message; BOTH original errors are
//	                               DISCARDED, so there is no precedence to
//	                               preserve — only the discarding itself
//	either way, a function type -> "== is not defined on function types"
//
// THE FAILURE OF THE LEFT OPERAND IS SUPPRESSED, not propagated. A machine that
// propagates it turns `==` into ordinary prim and erases the recovery: programs
// whose left operand cannot be synthesized in isolation, but whose right
// operand determines the type, would start being rejected.
type eqFrame struct {
	ctx  []*Ty
	term *Term
	part string // left | right-recover | compare
}

func (f *eqFrame) describe() string { return "eq:" + f.part }

// lamBodyFrame waits on a lambda's body. The parameter type extends the context
// for the body ONLY, by the same structural rule as `let`: the frame stores the
// original ctx and derives the body's, so nothing is popped.
//
// The two modes differ in what comes back:
//
//	synth   the body is SYNTHESIZED; the result is (-> paramTy bodyTy)
//	check   against a FUNCTION type: the parameter must equal exp.A and the
//	        body is CHECKED against exp.B
//	check   against anything else: check.go's `case "lam"` does not return —
//	        it FALLS THROUGH to synthesize-then-compare, so the diagnostic is
//	        "expected Int, got (-> ...)" rather than a lambda-specific message.
//	        A port that returns a tailored error there is observably different.
type lamBodyFrame struct {
	paramTy   *Ty
	synthMode bool
}

func (f *lamBodyFrame) describe() string { return "lam:body" }

func (f *lamBodyFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	if r.err != nil {
		return frameOutcome{done: &checkResult{err: r.err}}, nil
	}
	if f.synthMode {
		return frameOutcome{done: &checkResult{ty: tFun(f.paramTy, r.ty)}}, nil
	}
	return frameOutcome{done: &checkResult{}}, nil
}

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
func (f *matchArmFrame) resume(*checkerMachine, checkResult) (frameOutcome, error) {
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

// dispatch decides what happens to the current step. Exactly one of three
// things: it produces a RESULT (the step was a leaf), it pushes a frame and
// rewrites *s to the first child (the step has sub-judgements), or it refuses
// because the family is not ported.
//
// The check/synth split lives here rather than in the run loop because the two
// modes DIVERGE PER FAMILY. Most forms are checked by synthesize-then-compare,
// but `if` consumes the expected type directly and pushes both branches against
// it — collapsing that into one path would silently change which programs
// typecheck.
func (m *checkerMachine) dispatch(s *checkerStep) (checkResult, bool, error) {
	if s.term == nil {
		return checkResult{err: fmt.Errorf("missing term")}, true, nil
	}
	if s.mode == modeCheck {
		switch s.term.K {
		case "app":
			if head, _ := spine(s.term); head.K == "ref" || head.K == "self" {
				return checkResult{}, false, errFamilyNotPorted{"app with a ref/self head", modeCheck}
			}
			// check.go's `case "app"` only handles the ref/self spine; anything
			// else FALLS THROUGH to synthesize-then-compare.
			m.stack = append(m.stack, &checkCompareFrame{exp: s.exp})
			*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term}
			return checkResult{}, false, nil
		case "lam":
			if s.exp != nil && s.exp.K == "fun" {
				if err := checkTyWF(m.st, s.term.Ty, m.selfTyVars, false); err != nil {
					return checkResult{err: err}, true, nil
				}
				if !tyEq(s.term.Ty, s.exp.A) {
					return checkResult{err: fmt.Errorf("lambda parameter %s does not match expected %s",
						debugTy(s.term.Ty), debugTy(s.exp.A))}, true, nil
				}
				m.stack = append(m.stack, &lamBodyFrame{paramTy: s.term.Ty})
				*s = checkerStep{mode: modeCheck, ctx: pushCtx(s.ctx, s.term.Ty), term: s.term.A, exp: s.exp.B}
				return checkResult{}, false, nil
			}
			// FALL THROUGH, exactly as check.go does: no lambda-specific
			// message when the expected type is not a function.
			m.stack = append(m.stack, &checkCompareFrame{exp: s.exp})
			*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term}
			return checkResult{}, false, nil
		case "let":
			// The annotation is well-formedness-checked BEFORE any child runs,
			// so a bad annotation is reported even when the bound expression
			// would also fail.
			if err := checkTyWF(m.st, s.term.Ty, m.selfTyVars, false); err != nil {
				return checkResult{err: err}, true, nil
			}
			m.stack = append(m.stack, &letBodyFrame{ctx: s.ctx, term: s.term, exp: s.exp, part: "bound"})
			// CHECK MODE: the bound value is CHECKED against the annotation.
			*s = checkerStep{mode: modeCheck, ctx: s.ctx, term: s.term.A, exp: s.term.Ty}
			return checkResult{}, false, nil
		case "if":
			// CHECK MODE: the expected type flows into BOTH branches.
			m.stack = append(m.stack, &ifBranchFrame{ctx: s.ctx, term: s.term, exp: s.exp, part: "cond"})
			*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term.A}
			return checkResult{}, false, nil
		case "var", "int", "rat", "float", "bool", "prim":
			// The DEFAULT path: synthesize, then compare. check.go has no
			// `prim` case either — it falls through to exactly this.
			m.stack = append(m.stack, &checkCompareFrame{exp: s.exp})
			*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term}
			return checkResult{}, false, nil
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
	case "let":
		if err := checkTyWF(m.st, s.term.Ty, m.selfTyVars, false); err != nil {
			return checkResult{err: err}, true, nil
		}
		m.stack = append(m.stack, &letBodyFrame{ctx: s.ctx, term: s.term, part: "bound"})
		// SYNTH MODE: the bound value is SYNTHESIZED, then compared.
		*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term.A}
		return checkResult{}, false, nil
	case "app":
		// Spine inference over a ref/self head belongs to THAT family: it
		// resolves definitions through the store and infers type arguments
		// across the whole spine. Refused by name so app cannot quietly absorb
		// two semantic families and blur where a divergence came from.
		if head, _ := spine(s.term); head.K == "ref" || head.K == "self" {
			return checkResult{}, false, errFamilyNotPorted{"app with a ref/self head", modeSynth}
		}
		m.stack = append(m.stack, &appArgFrame{ctx: s.ctx, term: s.term, part: "fn"})
		*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term.A}
		return checkResult{}, false, nil
	case "lam":
		if err := checkTyWF(m.st, s.term.Ty, m.selfTyVars, false); err != nil {
			return checkResult{err: err}, true, nil
		}
		m.stack = append(m.stack, &lamBodyFrame{paramTy: s.term.Ty, synthMode: true})
		*s = checkerStep{mode: modeSynth, ctx: pushCtx(s.ctx, s.term.Ty), term: s.term.A}
		return checkResult{}, false, nil
	case "prim":
		// `==` is NOT this family. It recovers from a failed synthesis of its
		// first operand by retrying the second — a suppressed failure, closer
		// to the constructor inference pass than to indexed traversal — so it
		// is refused BY NAME rather than approximated here.
		if s.term.Op == "==" {
			// Arity is checked BEFORE any operand runs, matching check.go: a
			// wrong-arity `==` reports arity even if an operand would also fail.
			if len(s.term.Args) != 2 {
				return checkResult{err: fmt.Errorf("primitive == takes 2 arguments, got %d", len(s.term.Args))}, true, nil
			}
			m.stack = append(m.stack, &eqFrame{ctx: s.ctx, term: s.term, part: "left"})
			*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: &s.term.Args[0]}
			return checkResult{}, false, nil
		}
		if len(s.term.Args) == 0 {
			// No children: the operator rules apply immediately. Loop machinery
			// most often gets its termination condition wrong at this boundary.
			ty, err := primResultTy(m.st, s.term.Op, nil)
			return checkResult{ty: ty, err: err}, true, nil
		}
		m.stack = append(m.stack, &primArgFrame{
			ctx: s.ctx, term: s.term, argTys: make([]*Ty, len(s.term.Args))})
		*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: &s.term.Args[0]}
		return checkResult{}, false, nil
	case "if":
		// SYNTH MODE: both branches are synthesized and must agree.
		m.stack = append(m.stack, &ifBranchFrame{ctx: s.ctx, term: s.term, part: "cond"})
		*s = checkerStep{mode: modeSynth, ctx: s.ctx, term: s.term.A}
		return checkResult{}, false, nil
	}
	return checkResult{}, false, errFamilyNotPorted{s.term.K, modeSynth}
}

// run drives the machine to a result. The explicit stack is the whole point:
// depth costs heap here, not an embedder resource Go can neither measure nor
// grow.
func (m *checkerMachine) run(start checkerStep) (*Ty, error) {
	s := start
	for {
		r, produced, err := m.dispatch(&s)
		if err != nil {
			return nil, err
		}
		if !produced {
			continue // a frame was pushed; s now names its first child
		}
		// Unwind: hand the result to each waiting frame until one asks for
		// another child or the stack empties.
		descend := false
		for !descend {
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
				descend = true
				break
			}
			r = *out.done
		}
	}
}

// resume advances the if through its three parts. Errors propagate immediately
// in every part: unlike the constructor inference pass, nothing here is
// suppressed, and the FIRST failure is the one reported.
func (f *ifBranchFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	if r.err != nil {
		return frameOutcome{done: &checkResult{err: r.err}}, nil
	}
	synthMode := f.exp == nil
	switch f.part {
	case "cond":
		if r.ty == nil || r.ty.K != "bool" {
			return frameOutcome{done: &checkResult{
				err: fmt.Errorf("if condition must be Bool, got %s", debugTy(r.ty))}}, nil
		}
		f.part = "then"
		next := checkerStep{mode: modeSynth, ctx: f.ctx, term: f.term.B}
		if !synthMode {
			next = checkerStep{mode: modeCheck, ctx: f.ctx, term: f.term.B, exp: f.exp}
		}
		return frameOutcome{next: &next}, nil
	case "then":
		f.part = "else"
		if synthMode {
			f.thenTy = r.ty
			next := checkerStep{mode: modeSynth, ctx: f.ctx, term: f.term.C}
			return frameOutcome{next: &next}, nil
		}
		// CHECK MODE: the else-branch is checked against exp, NOT against
		// whatever the then-branch turned out to be.
		next := checkerStep{mode: modeCheck, ctx: f.ctx, term: f.term.C, exp: f.exp}
		return frameOutcome{next: &next}, nil
	default: // else
		if !synthMode {
			return frameOutcome{done: &checkResult{}}, nil
		}
		if !tyEq(f.thenTy, r.ty) {
			return frameOutcome{done: &checkResult{
				err: fmt.Errorf("if branches disagree: %s vs %s", debugTy(f.thenTy), debugTy(r.ty))}}, nil
		}
		return frameOutcome{done: &checkResult{ty: f.thenTy}}, nil
	}
}

func (f *letBodyFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	if r.err != nil {
		return frameOutcome{done: &checkResult{err: r.err}}, nil
	}
	synthMode := f.exp == nil
	if f.part == "bound" {
		if synthMode && !tyEq(r.ty, f.term.Ty) {
			return frameOutcome{done: &checkResult{
				err: fmt.Errorf("let annotation mismatch: declared %s, got %s",
					debugTy(f.term.Ty), debugTy(r.ty))}}, nil
		}
		f.part = "body"
		// The body's context is derived from the frame's OWN ctx. The original
		// is untouched, so whatever runs after this let sees it unchanged.
		body := pushCtx(f.ctx, f.term.Ty)
		next := checkerStep{mode: modeSynth, ctx: body, term: f.term.B}
		if !synthMode {
			next = checkerStep{mode: modeCheck, ctx: body, term: f.term.B, exp: f.exp}
		}
		return frameOutcome{next: &next}, nil
	}
	return frameOutcome{done: &checkResult{ty: r.ty}}, nil
}

func (f *primArgFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	if r.err != nil {
		// Leftmost failure wins; the remaining arguments are not visited.
		return frameOutcome{done: &checkResult{err: r.err}}, nil
	}
	// The result is consumed BEFORE the index advances. Advancing first would
	// store argument i's type in slot i+1 and leave slot 0 nil.
	f.argTys[f.idx] = r.ty
	f.idx++
	if f.idx < len(f.term.Args) {
		next := checkerStep{mode: modeSynth, ctx: f.ctx, term: &f.term.Args[f.idx]}
		return frameOutcome{next: &next}, nil
	}
	ty, err := primResultTy(m.st, f.term.Op, f.argTys)
	return frameOutcome{done: &checkResult{ty: ty, err: err}}, nil
}

func (f *eqFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	// known becomes the operand type both sides must share; other is the
	// operand still to be checked against it.
	settle := func(known *Ty, other *Term) (frameOutcome, error) {
		if tyHasFun(known) {
			return frameOutcome{done: &checkResult{
				err: fmt.Errorf("== is not defined on function types")}}, nil
		}
		f.part = "compare"
		next := checkerStep{mode: modeCheck, ctx: f.ctx, term: other, exp: known}
		return frameOutcome{next: &next}, nil
	}
	switch f.part {
	case "left":
		if r.err != nil {
			// SUPPRESSED. The left operand's error is discarded and the right
			// is tried instead; propagating here would erase the recovery.
			f.part = "right-recover"
			next := checkerStep{mode: modeSynth, ctx: f.ctx, term: &f.term.Args[1]}
			return frameOutcome{next: &next}, nil
		}
		return settle(r.ty, &f.term.Args[1])
	case "right-recover":
		if r.err != nil {
			// BOTH failed. check.go discards both original errors and reports
			// this fixed message; reproducing either operand's error would be a
			// visible change even though it looks more informative.
			return frameOutcome{done: &checkResult{
				err: fmt.Errorf("cannot infer the type of == operands — annotate one side")}}, nil
		}
		return settle(r.ty, &f.term.Args[0])
	default: // compare
		if r.err != nil {
			return frameOutcome{done: &checkResult{err: r.err}}, nil
		}
		return frameOutcome{done: &checkResult{ty: tBool()}}, nil
	}
}

func (f *appArgFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	if r.err != nil {
		return frameOutcome{done: &checkResult{err: r.err}}, nil
	}
	if f.part == "fn" {
		if r.ty == nil || r.ty.K != "fun" {
			return frameOutcome{done: &checkResult{
				err: fmt.Errorf("applied a non-function (type %s)", debugTy(r.ty))}}, nil
		}
		f.fnTy = r.ty
		f.part = "arg"
		next := checkerStep{mode: modeSynth, ctx: f.ctx, term: f.term.B}
		return frameOutcome{next: &next}, nil
	}
	if !tyEq(r.ty, f.fnTy.A) {
		return frameOutcome{done: &checkResult{
			err: fmt.Errorf("argument type mismatch: expected %s, got %s",
				debugTy(f.fnTy.A), debugTy(r.ty))}}, nil
	}
	return frameOutcome{done: &checkResult{ty: f.fnTy.B}}, nil
}
