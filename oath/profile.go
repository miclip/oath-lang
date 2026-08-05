package main

import "fmt"

// THE PORTABLE RESOURCE PROFILE (#149).
//
// Oath's accepted structural domain is BACKEND-INDEPENDENT. The same artifact
// must not be a program natively and a refusal in the playground because the two
// borrow different stacks. An implementation may refuse for an explicit,
// documented resource limit — that is this file — but a limit must never emerge
// accidentally from host call-stack depth. #147 made the same correction one
// layer down, for a bound derived in one deployment environment and silently
// inherited by a second.
//
// SO THESE NUMBERS ARE A POLICY, NOT A MEASUREMENT OF ANY HOST. They were chosen
// with measured headroom above real use, not reverse-engineered from a crash
// threshold. The floor, from `go test -run TestMeasure` (oath/resource_profile_test.go):
//
//	                     corpus max   profile   headroom
//	syntax nesting               17       512        30x
//	canonical nodes/def       1,293    65,536        50x
//
// The deepest real canonical structure is 68 (`hmac-kat-rfc4231-2`, whose depth
// is the SCons spine of a hex literal) against a syntax nesting of far less —
// which is the distinction the whole profile exists to preserve.
//
// THREE QUANTITIES, KEPT SEPARATE ON PURPOSE:
//
//	source bytes     size entering the kernel        (not yet bounded — see below)
//	syntax nesting   genuinely nested source forms   maxSyntaxNesting
//	canonical nodes  size of the constructed value   maxCanonicalNodes
//
// A 5,000-rune literal is ~5,000 canonical nodes and almost no syntax nesting.
// 20,000 nested parens consume both. 20,000 record fields consume many nodes and
// almost no nesting. Collapsing these into one "depth" budget would let the
// canonical REPRESENTATION of linear data decide which strings are legal
// programs — an implementation detail becoming language semantics.
//
// Source-byte size is deliberately NOT bounded here yet: it needs measuring
// against real application payloads first, and an unmeasured limit is the thing
// this file exists to avoid.
const (
	// maxCanonicalNodes bounds the size of an ADMITTED structure — the Term/Ty
	// graph as constructed — and thereby every later full-structure pass.
	//
	// IT IS A MAXIMUM ADMITTED SIZE, NOT CUMULATIVE FUEL SHARED ACROSS
	// TRAVERSALS. Fuel would make acceptance depend on which analyses happened
	// to run and in what order, so the same definition could be admitted or
	// refused according to whether a prover pass fired — acceptance would stop
	// being a property of the artifact.
	maxCanonicalNodes = 65536

	// maxSyntaxNesting bounds genuinely nested source forms. It is enforced in
	// the reader, which is iterative, so the limit is reached and reported
	// rather than approached until a host stack gives out — and, because the
	// elaborator descends the tree the reader produced, this is what currently
	// keeps that still-recursive descent far from any host ceiling.
	maxSyntaxNesting = 512
)

// resourceLimitErr is the TYPED refusal. It must not masquerade as malformed
// syntax, a type error, or a host exception: those say the input was wrong,
// while this says the input was well-formed and larger than this profile
// admits. Conflating them would tell an author to fix a program that has
// nothing wrong with it.
type resourceLimitErr struct {
	what  string // the quantity exceeded
	limit int
	got   int // 0 when the count was abandoned at the limit rather than completed
}

func (e *resourceLimitErr) Error() string {
	if e.got > 0 {
		return fmt.Sprintf("RESOURCE_LIMIT: %s exceeds portable profile limit (%d, got %d)", e.what, e.limit, e.got)
	}
	return fmt.Sprintf("RESOURCE_LIMIT: %s exceeds portable profile limit (%d)", e.what, e.limit)
}

func errTooManyNodes() error {
	return &resourceLimitErr{what: "canonical structure", limit: maxCanonicalNodes}
}

func errTooDeeplyNested(line int) error {
	return fmt.Errorf("line %d: %w", line, &resourceLimitErr{what: "syntax nesting", limit: maxSyntaxNesting})
}

// --- the traversal substrate (seed) ---
//
// Structural algorithms must not recurse directly over Term/Ty on the host
// stack. This is the shared mechanism they consume instead: an explicit work
// stack, so depth costs heap rather than an unobservable host resource.
//
// It starts here rather than as a big-bang refactor because the NODE COUNTER
// IS ITSELF A TRAVERSAL, and a recursive counter would overflow while measuring
// whether a structure is too deep to walk — the check crashing on exactly the
// inputs it exists to refuse.

// structItem is one pending node: exactly one of ty/term is non-nil.
type structItem struct {
	ty   *Ty
	term *Term
}

// countCanonicalNodes counts the nodes in a constructed Def, iteratively, and
// stops as soon as the limit is exceeded.
//
// EARLY EXIT IS PART OF THE CONTRACT: a structure far over the limit must cost
// at most limit+1 work to refuse, or the check becomes its own denial of
// service. So the returned count is exact only when ok is true.
//
// It counts nodes in the CONSTRUCTED CANONICAL OBJECT, never allocations made
// while constructing it. Temporary parser and elaborator objects are
// implementation-dependent, and counting them would make the portable profile
// backend-relative — precisely what it exists to prevent.
func countCanonicalNodes(d *Def, limit int) (n int, ok bool) {
	if d == nil {
		return 0, true
	}
	stack := make([]structItem, 0, 64)
	push := func(it structItem) { stack = append(stack, it) }

	if d.Ty != nil {
		push(structItem{ty: d.Ty})
	}
	if d.Body != nil {
		push(structItem{term: d.Body})
	}
	for i := range d.Props {
		push(structItem{term: &d.Props[i].Body})
		for j := range d.Props[i].Binders {
			push(structItem{ty: &d.Props[i].Binders[j]})
		}
	}
	for _, ctor := range d.Ctors {
		for i := range ctor {
			push(structItem{ty: &ctor[i]})
		}
	}

	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n++
		if n > limit {
			return 0, false
		}
		switch {
		case it.ty != nil:
			t := it.ty
			if t.A != nil {
				push(structItem{ty: t.A})
			}
			if t.B != nil {
				push(structItem{ty: t.B})
			}
			for i := range t.Args {
				push(structItem{ty: &t.Args[i]})
			}
		case it.term != nil:
			t := it.term
			for _, c := range []*Term{t.A, t.B, t.C} {
				if c != nil {
					push(structItem{term: c})
				}
			}
			if t.Ty != nil {
				push(structItem{ty: t.Ty})
			}
			for i := range t.Args {
				push(structItem{term: &t.Args[i]})
			}
			for i := range t.Arms {
				push(structItem{term: &t.Arms[i]})
			}
			for i := range t.TyArgs {
				push(structItem{ty: &t.TyArgs[i]})
			}
		}
	}
	return n, true
}

// admitDef is the ADMISSION BOUNDARY. Every path that constructs a Def from
// external input must pass through it — elaboration and decoding today, and
// anything added later. It is one function rather than a check at each call
// site so that "what does this profile admit?" has a single answer.
func admitDef(d *Def) error {
	if _, ok := countCanonicalNodes(d, maxCanonicalNodes); !ok {
		return errTooManyNodes()
	}
	return nil
}
