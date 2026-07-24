package main

import (
	"fmt"
	"math"
	"math/big"
)

// Value is a runtime value. Types are fully erased at runtime.
type Value struct {
	K      string // int | rat | float | bool | str | closure | data | record | native
	Int    *big.Int
	Rat    *big.Rat
	Float  float64
	Bool   bool
	Str    string   // str: value
	Names  []string // record: field names, sorted, parallel to Fields
	Env    []Value  // closure: captured environment
	Body   *Term    // closure: the lam term
	Slf    string   // closure: hash of the def it belongs to (for self-references)
	Hash   string   // data: ADT hash
	Idx    int      // data: constructor index
	Fields []Value  // data: constructor arguments
	Native string   // native (test-generated function): id | affine | const | table
	NA, NB int64    // native affine: NA*x + NB
	NVal   *Value   // native const: the returned value; table: the default
	TVals  []Value  // native table: outputs, parallel to Fields (the inputs)
}

// evaluator interprets kernel terms with a fuel budget. Termination is not
// proven in v0, so every execution is bounded: running out of fuel is an
// error, and a property that exhausts fuel counts as a failure, not a pass.
type evaluator struct {
	st    *Store
	fuel  int64
	depth int64
}

// maxEvalDepth bounds recursion depth separately from fuel: fuel limits total
// work, but the interpreter borrows the host stack for Oath recursion, so an
// unproductive loop can exhaust the stack long before it exhausts fuel.
// (Found the hard way: examples/nontotal.oath overflowed a 1GB goroutine
// stack with plenty of fuel left.)
const maxEvalDepth = 100_000

func (e *evaluator) eval(env []Value, slf string, t *Term) (Value, error) {
	e.depth++
	defer func() { e.depth-- }()
	if e.depth > maxEvalDepth {
		return Value{}, fmt.Errorf("recursion too deep (likely non-termination)")
	}
	return e.evalInner(env, slf, t)
}

func (e *evaluator) evalInner(env []Value, slf string, t *Term) (Value, error) {
	e.fuel--
	if e.fuel < 0 {
		return Value{}, fmt.Errorf("out of fuel (likely non-termination)")
	}
	switch t.K {
	case "var":
		if t.Idx < 0 || t.Idx >= len(env) {
			return Value{}, fmt.Errorf("variable index %d out of scope at runtime", t.Idx)
		}
		return env[len(env)-1-t.Idx], nil
	case "int":
		return Value{K: "int", Int: t.Int}, nil
	case "rat":
		return Value{K: "rat", Rat: t.Rat}, nil
	case "float":
		return Value{K: "float", Float: canonFloat(t.Float)}, nil
	case "bool":
		return Value{K: "bool", Bool: t.Bool}, nil
	case "record":
		fields := make([]Value, len(t.Args))
		for i := range t.Args {
			v, err := e.eval(env, slf, &t.Args[i])
			if err != nil {
				return Value{}, err
			}
			fields[i] = v
		}
		return Value{K: "record", Names: t.Names, Fields: fields}, nil
	case "field":
		rv, err := e.eval(env, slf, t.A)
		if err != nil {
			return Value{}, err
		}
		for i, n := range rv.Names {
			if n == t.Op {
				return rv.Fields[i], nil
			}
		}
		return Value{}, fmt.Errorf("record has no field %q at runtime", t.Op)
	case "lam":
		cenv := make([]Value, len(env))
		copy(cenv, env)
		return Value{K: "closure", Env: cenv, Body: t, Slf: slf}, nil
	case "app":
		f, err := e.eval(env, slf, t.A)
		if err != nil {
			return Value{}, err
		}
		a, err := e.eval(env, slf, t.B)
		if err != nil {
			return Value{}, err
		}
		return e.apply(f, a)
	case "let":
		v, err := e.eval(env, slf, t.A)
		if err != nil {
			return Value{}, err
		}
		return e.eval(pushEnv(env, v), slf, t.B)
	case "if":
		c, err := e.eval(env, slf, t.A)
		if err != nil {
			return Value{}, err
		}
		if c.Bool {
			return e.eval(env, slf, t.B)
		}
		return e.eval(env, slf, t.C)
	case "prim":
		return e.evalPrim(env, slf, t)
	case "ref":
		d, err := e.st.GetDef(t.Hash)
		if err != nil {
			return Value{}, err
		}
		return e.eval(nil, t.Hash, d.Body)
	case "self":
		if slf == "" {
			return Value{}, fmt.Errorf("self-reference with no enclosing definition")
		}
		d, err := e.st.GetDef(slf)
		if err != nil {
			return Value{}, err
		}
		return e.eval(nil, slf, d.Body)
	case "ctor":
		fields := make([]Value, len(t.Args))
		for i := range t.Args {
			v, err := e.eval(env, slf, &t.Args[i])
			if err != nil {
				return Value{}, err
			}
			fields[i] = v
		}
		return Value{K: "data", Hash: t.Hash, Idx: t.Idx, Fields: fields}, nil
	case "match":
		sv, err := e.eval(env, slf, t.A)
		if err != nil {
			return Value{}, err
		}
		if sv.K != "data" {
			return Value{}, fmt.Errorf("match on non-data value at runtime")
		}
		if sv.Idx < 0 || sv.Idx >= len(t.Arms) {
			return Value{}, fmt.Errorf("match arm %d missing at runtime", sv.Idx)
		}
		armEnv := env
		for _, f := range sv.Fields {
			armEnv = pushEnv(armEnv, f)
		}
		return e.eval(armEnv, slf, &t.Arms[sv.Idx])
	}
	return Value{}, fmt.Errorf("unknown term form %q at runtime", t.K)
}

func (e *evaluator) apply(f, a Value) (Value, error) {
	e.fuel--
	if e.fuel < 0 {
		return Value{}, fmt.Errorf("out of fuel (likely non-termination)")
	}
	switch f.K {
	case "closure":
		return e.eval(pushEnv(f.Env, a), f.Slf, f.Body.A)
	case "native":
		switch f.Native {
		case "id":
			return a, nil
		case "const":
			return *f.NVal, nil
		case "affine":
			if a.K != "int" {
				return Value{}, fmt.Errorf("native affine function applied to non-Int")
			}
			r := new(big.Int).Mul(big.NewInt(f.NA), a.Int)
			return Value{K: "int", Int: r.Add(r, big.NewInt(f.NB))}, nil
		case "table":
			for i := range f.Fields {
				if eq, err := structEq(f.Fields[i], a); err == nil && eq {
					return f.TVals[i], nil
				}
			}
			return *f.NVal, nil
		}
	}
	return Value{}, fmt.Errorf("applied a non-function value at runtime")
}

func (e *evaluator) evalPrim(env []Value, slf string, t *Term) (Value, error) {
	args := make([]Value, len(t.Args))
	for i := range t.Args {
		v, err := e.eval(env, slf, &t.Args[i])
		if err != nil {
			return Value{}, err
		}
		args[i] = v
	}
	vInt := func(x *big.Int) Value { return Value{K: "int", Int: x} }
	vBool := func(x bool) Value { return Value{K: "bool", Bool: x} }
	// Rational arithmetic — dispatched on operand kind. Rat is ℚ (exact),
	// so `/` is true division and never truncates.
	if len(args) > 0 && args[0].K == "rat" {
		vRat := func(x *big.Rat) Value { return Value{K: "rat", Rat: x} }
		switch t.Op {
		case "+":
			return vRat(new(big.Rat).Add(args[0].Rat, args[1].Rat)), nil
		case "-":
			return vRat(new(big.Rat).Sub(args[0].Rat, args[1].Rat)), nil
		case "*":
			return vRat(new(big.Rat).Mul(args[0].Rat, args[1].Rat)), nil
		case "/":
			if args[1].Rat.Sign() == 0 {
				return Value{}, fmt.Errorf("division by zero")
			}
			return vRat(new(big.Rat).Quo(args[0].Rat, args[1].Rat)), nil
		case "neg":
			return vRat(new(big.Rat).Neg(args[0].Rat)), nil
		case "<":
			return vBool(args[0].Rat.Cmp(args[1].Rat) < 0), nil
		case "<=":
			return vBool(args[0].Rat.Cmp(args[1].Rat) <= 0), nil
		case "==":
			return vBool(args[0].Rat.Cmp(args[1].Rat) == 0), nil
		}
	}
	// IEEE-754 float arithmetic — dispatched on operand kind. Totally defined:
	// division by zero yields ±inf (0/0 yields NaN), never an error. Every result
	// is canonicalized (NaN → the one canonical NaN). `<`/`<=` are IEEE ordered
	// comparisons (NaN is unordered ⇒ false), matching fp.lt/fp.leq. `==` is NOT
	// handled here: it falls through to structEq, which is bitwise (SMT `=`), so
	// NaN == NaN is true and +0.0 == -0.0 is false — the kernel's identity model.
	if len(args) > 0 && args[0].K == "float" {
		vF := func(x float64) Value { return Value{K: "float", Float: canonFloat(x)} }
		switch t.Op {
		case "+":
			return vF(args[0].Float + args[1].Float), nil
		case "-":
			return vF(args[0].Float - args[1].Float), nil
		case "*":
			return vF(args[0].Float * args[1].Float), nil
		case "/":
			return vF(args[0].Float / args[1].Float), nil
		case "neg":
			return vF(-args[0].Float), nil
		case "<":
			return vBool(args[0].Float < args[1].Float), nil
		case "<=":
			return vBool(args[0].Float <= args[1].Float), nil
		case "fp-eq":
			// IEEE equality (fp.eq): NaN ≠ NaN, +0.0 == -0.0. The opt-in escape
			// hatch, distinct from structural ==.
			return vBool(args[0].Float == args[1].Float), nil
		}
	}
	// Numeric conversions (unary). Widening (Int→Rat/Float, Rat→Float) and the
	// exact narrowing Float→Rat / floor toward −∞. Float→{Rat,Int} error on
	// NaN/inf, like division by zero.
	switch t.Op {
	case "to-rat":
		if args[0].K == "int" {
			return Value{K: "rat", Rat: new(big.Rat).SetInt(args[0].Int)}, nil
		}
		f := args[0].Float
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return Value{}, fmt.Errorf("to-rat of non-finite float")
		}
		return Value{K: "rat", Rat: new(big.Rat).SetFloat64(f)}, nil // exact for finite binary64
	case "to-float":
		if args[0].K == "int" {
			f, _ := new(big.Float).SetInt(args[0].Int).Float64()
			return Value{K: "float", Float: canonFloat(f)}, nil
		}
		f, _ := args[0].Rat.Float64() // nearest binary64, round-nearest-even
		return Value{K: "float", Float: canonFloat(f)}, nil
	case "floor":
		if args[0].K == "rat" {
			// floor(num/den) with den > 0 is big.Int Div (rounds toward −∞).
			return vInt(new(big.Int).Div(args[0].Rat.Num(), args[0].Rat.Denom())), nil
		}
		f := args[0].Float
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return Value{}, fmt.Errorf("floor of non-finite float")
		}
		i, _ := big.NewFloat(math.Floor(f)).Int(nil)
		return vInt(i), nil
	}
	switch t.Op {
	case "+":
		return vInt(new(big.Int).Add(args[0].Int, args[1].Int)), nil
	case "-":
		return vInt(new(big.Int).Sub(args[0].Int, args[1].Int)), nil
	case "*":
		return vInt(new(big.Int).Mul(args[0].Int, args[1].Int)), nil
	case "/":
		if args[1].Int.Sign() == 0 {
			return Value{}, fmt.Errorf("division by zero")
		}
		// Quo truncates toward zero, matching the SPEC (not Euclidean Div).
		return vInt(new(big.Int).Quo(args[0].Int, args[1].Int)), nil
	case "%":
		if args[1].Int.Sign() == 0 {
			return Value{}, fmt.Errorf("modulo by zero")
		}
		// Rem takes the dividend's sign, matching the SPEC.
		return vInt(new(big.Int).Rem(args[0].Int, args[1].Int)), nil
	case "neg":
		return vInt(new(big.Int).Neg(args[0].Int)), nil
	case "<":
		return vBool(args[0].Int.Cmp(args[1].Int) < 0), nil
	case "<=":
		return vBool(args[0].Int.Cmp(args[1].Int) <= 0), nil
	case "and":
		return vBool(args[0].Bool && args[1].Bool), nil
	case "or":
		return vBool(args[0].Bool || args[1].Bool), nil
	case "not":
		return vBool(!args[0].Bool), nil
	case "==":
		eq, err := structEq(args[0], args[1])
		if err != nil {
			return Value{}, err
		}
		return vBool(eq), nil
	}
	return Value{}, fmt.Errorf("unknown primitive %q at runtime", t.Op)
}

// structEq is structural equality on first-order values. Functions are not
// comparable; the typechecker rules this out statically, so hitting it at
// runtime indicates a kernel bug.
func structEq(a, b Value) (bool, error) {
	if a.K != b.K {
		return false, nil
	}
	switch a.K {
	case "int":
		return a.Int.Cmp(b.Int) == 0, nil
	case "rat":
		return a.Rat.Cmp(b.Rat) == 0, nil
	case "float":
		// Bitwise (Leibniz / SMT `=`), on canonicalized values: NaN == NaN
		// (one canonical pattern) and +0.0 != -0.0. This is the identity model,
		// deliberately NOT IEEE fp.eq (that is the `fp-eq` primitive).
		return math.Float64bits(canonFloat(a.Float)) == math.Float64bits(canonFloat(b.Float)), nil
	case "bool":
		return a.Bool == b.Bool, nil
	case "record":
		if len(a.Fields) != len(b.Fields) {
			return false, nil
		}
		for i := range a.Fields {
			eq, err := structEq(a.Fields[i], b.Fields[i])
			if err != nil || !eq {
				return eq, err
			}
		}
		return true, nil
	case "data":
		if a.Hash != b.Hash || a.Idx != b.Idx || len(a.Fields) != len(b.Fields) {
			return false, nil
		}
		for i := range a.Fields {
			eq, err := structEq(a.Fields[i], b.Fields[i])
			if err != nil || !eq {
				return eq, err
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("equality is not defined on function values")
}

func pushEnv(env []Value, v Value) []Value {
	out := make([]Value, len(env)+1)
	copy(out, env)
	out[len(env)] = v
	return out
}
