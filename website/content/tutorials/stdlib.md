# Tutorial: writing and proving a library function

This walks the full authoring loop — define a function, state its laws, watch the
kernel prove them, then check the spec is actually tight — using string
concatenation, which is an ordinary definition here (strings are structural, not
primitive).

## Strings are a datatype

A `Str` is a sequence of Unicode codepoints, built like any other inductive type.
Its constructors are `SNil` (empty) and `SCons` (a codepoint on the front):

```lisp
(data Str [] (SNil) (SCons Int Str))
```

A `"…"` literal is just sugar for the corresponding `SCons` chain, so there is no
string primitive and no magic — every string operation is a definition you could
have written yourself.

## Define the function, and *state its laws*

Concatenation is structural recursion on the first string. The interesting part is
that you attach the **properties it should satisfy** right in the definition — the
spec and the code live together:

```lisp
(defn str-append [] [(a Str) (b Str)] Str
  (match a
    ((SNil) b)
    ((SCons c rest) (SCons c (str-append rest b))))
  (prop left-unit [(b Str)] (== (str-append (SNil) b) b))
  (prop length-adds [(a Str) (b Str)]
    (== (str-len (str-append a b)) (+ (str-len a) (str-len b)))))
```

`left-unit` says appending onto the empty string is the identity; `length-adds`
says lengths add. These aren't comments — they're checked.

## Prove the laws

`put` runs the gate and tests the properties (200 hash-seeded cases each); `prove`
tries to discharge them for *all* inputs with Z3, using structural induction:

```console
$ oath get str-append
  prop left-unit:   forall [(x0 Str)]. (== (str-append SNil x0) x0)
  prop length-adds: forall [(x0 Str) (x1 Str)]. (== (str-len (str-append x0 x1)) (+ (str-len x0) (str-len x1)))
guarantee: PROVEN (all 2 properties, Z3 over unbounded ints) · spec strength 3/4 mutants killed
```

`PROVEN` — both laws hold for every string, by induction over the `Str` datatype.
(The split/join round-trip that other string libraries prove is here too, and it's
one Z3's sequence theory can't reach directly — which is exactly why strings are a
*structural* datatype rather than a primitive.)

## Check the spec is actually tight

`PROVEN` says the laws hold; **mutation** asks whether the laws actually pin the
implementation down. `spec strength 3/4 mutants killed` (above) means one
type-preserving mutant of the body slipped past the properties — a hint that the
spec could be tighter:

```console
$ oath mutate str-append
spec strength: 3/4 mutants killed
# the surviving mutant is printed with its body — either strengthen a property to
# catch it, or `oath waive` it with a justification if it's genuinely equivalent.
```

That honesty is the point: a proof tells you the stated laws hold; the mutation
score tells you how much the stated laws are *worth*.

## From here

Everything you built is now first-class: other definitions reference `str-append`
by hash (see [names aren't identity](names.md)), it compiles to a native Go string
at runtime (`oath build`, see [the circle tutorial](circle.md)), and it's
discoverable by its laws (`oath find`, see [discovery](discovery.md)). You wrote a
function, said what it means, and the kernel held you to it — that's the whole
loop.
