package main

import (
	"fmt"
	"math/big"
)

// Deterministic input generation for property testing. Seeds are derived from
// the definition's hash, so verification is reproducible: the same definition
// always sees the same test inputs, on any machine, forever. (This is also
// why wall clocks and OS entropy appear nowhere in the kernel.)

type rng struct{ s uint64 }

// next is splitmix64: tiny, well-distributed, dependency-free.
func (r *rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *rng) below(n int) int {
	return int(r.next() % uint64(n))
}

func (r *rng) intIn(lo, hi int64) int64 {
	return lo + int64(r.next()%uint64(hi-lo+1))
}

// genValue produces a random value of a concrete type, with recursion bounded
// by size. Function-typed inputs are drawn from a small family of native
// functions (identity, affine, constant) — enough to falsify most wrong
// higher-order code without needing arbitrary term synthesis.
func genValue(st *Store, ty *Ty, size int, r *rng) (Value, error) {
	// Size is a recursion budget, never a draw parameter that may go negative:
	// data fields recurse at size-1, so a Str nested inside a data value can be
	// reached at size 0 and then generated at size -1. below(size+1) would then
	// divide by zero. The budget floors at 0 (empty/short leaves), which is
	// also the base-case regime the data branch already selects for.
	if size < 0 {
		size = 0
	}
	switch ty.K {
	case "int":
		// Bias toward boundary values: off-by-one and base-case mutants live
		// at small magnitudes that uniform sampling rarely witnesses. (Found
		// by the split-agent experiment: a correct relational property let
		// six take/drop mutants survive purely because 60 uniform draws from
		// [-20,20] seldom produce the distinguishing n ∈ {0,1,2}.)
		if r.below(4) == 0 {
			boundary := []int64{-2, -1, 0, 1, 2}
			return Value{K: "int", Int: big.NewInt(boundary[r.below(len(boundary))])}, nil
		}
		return Value{K: "int", Int: big.NewInt(r.intIn(-20, 20))}, nil
	case "bool":
		return Value{K: "bool", Bool: r.below(2) == 0}, nil
	case "record":
		fields := make([]Value, len(ty.Args))
		for i := range ty.Args {
			v, err := genValue(st, &ty.Args[i], size, r)
			if err != nil {
				return Value{}, err
			}
			fields[i] = v
		}
		return Value{K: "record", Names: ty.Names, Fields: fields}, nil
	case "fun":
		if ty.A.K == "int" && ty.B.K == "int" {
			switch r.below(4) {
			case 0:
				return Value{K: "native", Native: "id"}, nil
			case 1, 2:
				return Value{K: "native", Native: "affine", NA: r.intIn(-3, 3), NB: r.intIn(-10, 10)}, nil
			}
			// fall through to a table
		}
		// Generated functions are finite tables (input → output pairs plus a
		// default). Rich enough to simulate a world behind a capability
		// record — the same machinery tests pure higher-order code and
		// effectful-style code against deterministic fake environments.
		n := 1 + r.below(3)
		var keys, vals []Value
		for i := 0; i < n; i++ {
			k, err := genValue(st, ty.A, size, r)
			if err != nil {
				return Value{}, err
			}
			v, err := genValue(st, ty.B, size, r)
			if err != nil {
				return Value{}, err
			}
			keys = append(keys, k)
			vals = append(vals, v)
		}
		d, err := genValue(st, ty.B, size, r)
		if err != nil {
			return Value{}, err
		}
		return Value{K: "native", Native: "table", Fields: keys, TVals: vals, NVal: &d}, nil
	case "data":
		d, err := st.GetDef(ty.Hash)
		if err != nil {
			return Value{}, err
		}
		var candidates []int
		if size <= 0 {
			for i := range d.Ctors {
				if !ctorRecursive(d, ty.Hash, ty.Args, i) {
					candidates = append(candidates, i)
				}
			}
			if len(candidates) == 0 {
				return Value{}, fmt.Errorf("no base-case constructor for %s; cannot generate finite values", shortHash(ty.Hash))
			}
		} else {
			for i := range d.Ctors {
				candidates = append(candidates, i)
			}
		}
		idx := candidates[r.below(len(candidates))]
		fields := instCtorFields(d, ty.Hash, ty.Args, idx)
		fv := make([]Value, len(fields))
		for i, f := range fields {
			v, err := genValue(st, f, size-1, r)
			if err != nil {
				return Value{}, err
			}
			fv[i] = v
		}
		return Value{K: "data", Hash: ty.Hash, Idx: idx, Fields: fv}, nil
	}
	return Value{}, fmt.Errorf("cannot generate a value of type %s", debugTy(ty))
}

// ctorRecursive reports whether constructor idx mentions its own ADT —
// used to find base cases when the size budget runs out.
func ctorRecursive(d *Def, h string, tyargs []Ty, idx int) bool {
	for _, f := range instCtorFields(d, h, tyargs, idx) {
		if tyMentionsHash(f, h) {
			return true
		}
	}
	return false
}

func tyMentionsHash(t *Ty, h string) bool {
	if t == nil {
		return false
	}
	if t.K == "data" && t.Hash == h {
		return true
	}
	if tyMentionsHash(t.A, h) || tyMentionsHash(t.B, h) {
		return true
	}
	for i := range t.Args {
		if tyMentionsHash(&t.Args[i], h) {
			return true
		}
	}
	return false
}
