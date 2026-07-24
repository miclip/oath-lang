# Tutorial: numbers you can trust

Most languages give you one number type and a pile of caveats — silent overflow,
`0.1 + 0.2 != 0.3`, rounding you have to remember. Oath has three numeric
primitives, each chosen so the prover is always on solid ground, and the verdicts
say exactly what holds.

## `Int` is ℤ — no overflow, ever

`Int` is arbitrary precision. There is no wraparound to reason around:

```console
$ oath eval '(* 1000000000000 1000000000000)'
1000000000000000000000000 : Int
```

The proof model already reasons over unbounded integers, so a proof about `Int`
carries no "valid modulo overflow" asterisk — the runtime simply matches the math.

## `Rat` is ℚ — exact, and the laws *prove*

A bare decimal like `0.1` is an exact rational, so the classic floating-point trap
doesn't exist — and because Z3's real arithmetic is complete, the clean algebraic
laws are *proven*, not hoped:

```console
$ oath eval '(== (+ 0.1 0.2) 0.3)'
true : Bool
```

`0.1 + 0.2` is *exactly* `3/10`. `examples/rat.oath` proves commutativity,
distributivity, and exact division-inverse `(a/b)*b == a` for all rationals.

## `Float` is IEEE-754 — and it's honest about it

When you need bit-level interop, `Float` (opt in with an `f` suffix) gives you
real IEEE-754 — including its warts, stated plainly:

```console
$ oath eval '(+ 0.1f 0.2f)'
0.30000000000000004f : Float

$ oath eval '(== (+ 0.1f 0.2f) 0.3f)'
false : Bool
```

The *same* property — `0.1 + 0.2 == 0.3` — is **proven** for `Rat` and
**falsified** for `Float`, side by side. That's not a bug in the tutorial; it's
the point. Floats still reach `proven` for their *true* laws (`x*1.0 == x`,
`x+x == x*2` for every float), and their false ones (associativity,
`0.1f+0.2f==0.3f`) are correctly falsified. The prover being *right*, not weak.

This is the completeness lens: `Str` is a structural datatype because Z3's
sequence theory is incomplete; `Rat` and `Float` are primitives because Z3's real
and float theories are complete. Structure where the solver is weak, primitive
where it's strong.

## Converting between them — explicitly

The three interconvert, and the conversions are honest about what can fail:

```console
$ oath eval '(to-rat 0.5f)'          # exact: 0.5 is dyadic
1/2 : Rat
$ oath eval '(floor -2.5f)'          # round toward -inf
-3 : Int
$ oath eval '(== (to-float 1/10) 0.1f)'   # 1/10 rounds to exactly the 0.1f literal
true : Bool
```

`to-rat`/`to-float`/`floor` bridge `Int`/`Rat`/`Float`; the widening directions
are proven, and the narrowing ones fault on `NaN`/`inf` (they have no rational or
integer) exactly the way division by zero does. The last line is the nicest: the
exact rational `1/10` converts to *precisely* the `0.1f` float literal — the ℚ and
IEEE worlds meet where you'd want them to.

## The takeaway

You never get a silently-wrong number. `Int` can't overflow; `Rat` is exact and
its algebra proves; `Float` is real IEEE-754 and the kernel *tells you* which of
its laws hold and which don't. See the whole story in
[docs/floats.md](../floats.md), or watch it drive a real program in
[the circle tutorial](circle.md).
