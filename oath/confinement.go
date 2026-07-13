package main

// Capability confinement — the no-escape checker (docs/effects.md, stage 2).
//
// A higher-order parameter (one whose type contains a function: a bare
// callback or a capability record) is CONFINED if the function only ever
// exercises it, never keeps it. Allowed uses of the parameter variable:
//
//   - applied directly:               (f x)
//   - projected and applied:          ((. net fetch) x)
//   - passed to itself at the SAME parameter position (recursive plumbing)
//   - passed, whole, to a callee parameter that is itself confined
//
// Everything else escapes: returned bare, stored in a constructor, record,
// or let-binding, projected without application, or captured inside an
// inner lambda (conservative: the closure might outlive the call).
//
// Like totality, verdicts compose bottom-up with no extra machinery:
// callees are content hashes, so cycles are impossible and their verdicts
// are already in metadata. The verdict is a label, never a rejection —
// leaky code is admitted and branded, matching the guarantee philosophy.

// confinementOf returns one verdict per parameter: "confined" | "escapes"
// for higher-order params, "" for first-order params (nothing to leak).
func confinementOf(st *Store, d *Def) []string {
	if d.K != "func" {
		return nil
	}
	var paramTys []*Ty
	cur := d.Body
	for cur.K == "lam" {
		paramTys = append(paramTys, cur.Ty)
		cur = cur.A
	}
	out := make([]string, len(paramTys))
	for i, pt := range paramTys {
		if !tyHasFun(pt) {
			continue
		}
		// The param bound by lam i sits at de Bruijn index nparams-1-i
		// inside the innermost body.
		w := &escapeWalker{st: st, selfPos: i}
		if w.confined(cur, len(paramTys)-1-i, false) {
			out[i] = "confined"
		} else {
			out[i] = "escapes"
		}
	}
	return out
}

type escapeWalker struct {
	st      *Store
	selfPos int // the parameter position under analysis, for self-calls
}

// confined reports whether the variable at de Bruijn index idx is confined
// within t. inLam is true once we are under a binder introduced by an inner
// lambda — capability use inside a closure is conservatively an escape.
func (w *escapeWalker) confined(t *Term, idx int, inLam bool) bool {
	if t == nil {
		return true
	}
	switch t.K {
	case "var":
		// A bare occurrence that no allowed pattern intercepted.
		return t.Idx != idx
	case "app":
		head, args := unwindApp(t)
		headOK := false
		switch {
		case head.K == "var" && head.Idx == idx:
			// (f x ...) — direct application of the parameter.
			headOK = !inLam
		case head.K == "field" && head.A != nil && head.A.K == "var" && head.A.Idx == idx:
			// ((. net fetch) x ...) — project-and-apply.
			headOK = !inLam
		}
		if !headOK {
			if head.K == "ref" || head.K == "self" {
				// Whole-parameter pass-through: allowed only into positions
				// known to confine.
				for j, a := range args {
					if a.K == "var" && a.Idx == idx {
						if inLam || !w.calleeConfines(head, j) {
							return false
						}
					} else if !w.confined(a, idx, inLam) {
						return false
					}
				}
				return true
			}
			if !w.confined(head, idx, inLam) {
				return false
			}
		}
		for _, a := range args {
			if !w.confined(a, idx, inLam) {
				return false
			}
		}
		return true
	case "lam":
		return w.confined(t.A, idx+1, true)
	case "let":
		return w.confined(t.A, idx, inLam) && w.confined(t.B, idx+1, inLam)
	case "match":
		if !w.confined(t.A, idx, inLam) {
			return false
		}
		d, err := w.st.GetDef(t.Hash)
		if err != nil {
			return false
		}
		for i := range t.Arms {
			if !w.confined(&t.Arms[i], idx+len(d.Ctors[i]), inLam) {
				return false
			}
		}
		return true
	default:
		if !w.confined(t.A, idx, inLam) || !w.confined(t.B, idx, inLam) || !w.confined(t.C, idx, inLam) {
			return false
		}
		for i := range t.Args {
			if !w.confined(&t.Args[i], idx, inLam) {
				return false
			}
		}
		return true
	}
}

// calleeConfines reports whether passing the whole parameter into argument
// position j of the given ref/self head is safe.
func (w *escapeWalker) calleeConfines(head *Term, j int) bool {
	if head.K == "self" {
		// Recursive plumbing: same position is the fixed point we are
		// currently establishing; any other position is not yet known.
		return j == w.selfPos
	}
	m, err := w.st.GetMeta(head.Hash)
	if err != nil || j >= len(m.Confinement) {
		return false
	}
	return m.Confinement[j] == "confined"
}

// unwindApp flattens an application chain into head and in-order arguments.
func unwindApp(t *Term) (*Term, []*Term) {
	var args []*Term
	cur := t
	for cur.K == "app" {
		args = append([]*Term{cur.B}, args...)
		cur = cur.A
	}
	return cur, args
}
