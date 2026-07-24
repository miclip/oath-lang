# Tutorial: a circle-area calculator, compiled to a native binary

This is a complete, runnable program — a command-line calculator that reads a
radius and prints the area of a circle — assembled entirely from proof-carrying
Oath definitions and lowered to a standalone native executable by `oath build`.
It's a small program that happens to touch the whole substrate: structural
strings, the numeric tower (`Int`, exact `Rat`), the numeric conversions, and
the native compiler.

Source: [`circle.oath`](circle.oath).

## Run it

```console
$ oath put docs/tutorial/circle.oath
$ oath build circle -o /tmp/circle
$ /tmp/circle 5
area(r=5) ~ 78
$ /tmp/circle 10
area(r=10) ~ 314
$ /tmp/circle 100
area(r=100) ~ 31415
```

That last line is worth a pause: `π·100² ≈ 31415.9`, floored to `31415`, over an
`Int` that is arbitrary precision — nothing overflows, and the intermediate
arithmetic is *exact* (see below).

## How it works

### π without floating point

There is no `Float` anywhere in this program. `π` is approximated by the rational
`355/113` (correct to six decimals), and the area is computed over exact `Rat`:

```lisp
(show-int (floor (* 355/113 (to-rat (* r r)))))
```

`(* r r)` is an `Int`; `to-rat` lifts it into ℚ; `*` multiplies by the rational
`355/113` *exactly* (no rounding, no drift); and `floor` rounds the exact result
down to a whole number of square units. For `r = 5`, the intermediate area is
*exactly* `8875/113` — the kernel never approximates it, it just floors it for
display. (If you wanted the fractional part, it's all still there in the `Rat`.)

### Reading a number is total; writing one is a frontier

The two halves of "talk to the outside world" have very different proof
characters, and the program is honest about it.

**Reading** (`parse-nat`) recurses on the *structure of the string* — a `Str` is
an inductive datatype of codepoints, so peeling one character at a time is
structural recursion, and the kernel proves it **total**:

```lisp
(defn parse-nat-go [] [(acc Int) (s Str)] Int
  (match s
    ((SNil) acc)
    ((SCons c rest) (parse-nat-go (+ (* 10 acc) (- c 48)) rest))))
```

**Writing** (`show-int`) has no such structure to lean on. To print a number in
decimal you divide by ten and recurse on the quotient:

```lisp
(defn show-nat [] [(n Int)] Str
  (if (< n 10)
      (SCons (+ 48 n) (SNil))
      (str-append (show-nat (/ n 10)) (SCons (+ 48 (% n 10)) (SNil)))))
```

`n / 10` genuinely decreases, but the termination checker can't see it: integer
division and modulo are outside its measure fragment (`/` truncates while
SMT-LIB is Euclidean, so the kernel excludes both from translation, §7). So
`show-nat`/`show-int` are labeled **`termination unproven`** — they run
correctly, and the round-trip law `parse-nat (show-int n) == n` is *tested* over
200 cases, but the kernel refuses to *claim* termination it can't certify. That
honesty is the point: the verdict tells you exactly how far the guarantee goes.

`oath build` still compiles it — the build gate requires verified *properties*
(here `circle` is `tested`, with concrete `r=5`/`r=10` checks), not proven
termination.

### The whole entry

```lisp
(defn circle [] [(args (List Str))] Str
  (let (r Int (parse-nat (first-or "0" args)))
    (str-append
      (str-append (str-append "area(r=" (show-int r)) ") ~ ")
      (show-int (floor (* 355/113 (to-rat (* r r)))))))
  (prop r5  [(x Int)] (== (circle (Cons [Str] "5"  (Nil [Str]))) "area(r=5) ~ 78"))
  (prop r10 [(x Int)] (== (circle (Cons [Str] "10" (Nil [Str]))) "area(r=10) ~ 314")))
```

`args` arrives as a `(List Str)` (the process argv); `first-or "0"` takes the
first argument or defaults to `"0"`; and the result `Str` is printed. At runtime
every `Str` is a native Go string and every `Rat` a `big.Rat`, so the compiled
binary is ordinary fast native code — kept honest by the compiler's differential
gate (compiled output must equal what `oath eval` produces).

## Why this lives in `docs/tutorial/`, not `examples/`

The curated `examples/` corpus doubles as the cross-kernel conformance suite,
and this program deliberately steps onto a frontier that corpus avoids: it has
proof properties (`circle`'s `r5`/`r10`) that transitively reference a
`termination unproven`, division-using function (`show-int`). That combination
surfaced a real ambiguity in how the two kernels generate proof *scripts* for
such a goal — the reference (Go) kernel emits no direct-attempt script when
eager body-translation reaches an excluded operator, while the independent
(Rust) kernel emits one with the callee left uninterpreted. Both reach the same
verdict (unprovable → the property stays `tested`), but the script bytes differ,
which the byte-oracle conformance check flags.

That's the N-version methodology doing its job — it's a finding, not a bug in
this program — and it's tracked in issue #64 for a proper spec clarification.
Until then, this tutorial stays out of the conformance corpus so the finding
doesn't gate CI, while remaining a fully working, buildable example.
