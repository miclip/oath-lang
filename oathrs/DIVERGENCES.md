# DIVERGENCES — independent Rust kernel, Stage 1

Every place SPEC.md was ambiguous, silent, contradictory, or surprising while
implementing identity + the gate blind (no access to the Go reference). Each
entry: the spec section, the question, the choice I made, and whether a fixture
settled it. Entries marked **UNTESTED** are choices no fixture exercises — the
highest-risk spots for a hidden cross-kernel disagreement.

## Encoding / identity

1. **§1.2 — the two `var`s.** A `Ty` variable and a `Term` variable both have
   `k:"var"`, but a `Ty` carries its index in the field `var` while a `Term`
   carries it in `idx`. The struct field-order tables say so, but it is a trap:
   emitting a term variable's index under `"var"` (or vice versa) hashes
   everything downstream differently. Choice: `Ty::Var` → `"var"`, `Term::Var`
   → `"idx"`, both omitted when 0. *Disambiguated:* `map.json` shows
   `{"k":"var","var":1}` (type) alongside `{"k":"var","idx":1}` (term).

2. **§1.2 — record encodes `args` before `names`.** The Ty field order is
   `…hash, args, names`, so a record type/term emits its parallel value array
   *before* its (sorted) name array. Alphabetical intuition would flip them.
   Choice: follow field order literally. *Disambiguated:* `full-name.json`
   (`{"k":"record","args":[…],"names":["first","last"]}`).

3. **§1.1/§1.3 — operator names are HTML-escaped too.** The string-escape rule
   is stated for string *literals*, but `op` is also a JSON string, so the
   comparison operators `<` and `<=` serialize as `<` / `<=`. Easy to
   forget that the escape rule reaches operator names. Choice: run every emitted
   string (op, hash, field name, str literal) through the same escaper.
   *Disambiguated:* `length.json` (`"op":"<="`).

4. **§1.2 — `ctors` is always emitted for a `data` Def.** `ctors` is not in the
   "always present" list, yet the omit-when-empty rule would never fire (a data
   always has ≥1 constructor) and a zero-field constructor must still appear as
   `[]`. Choice: emit `ctors` whenever the Def is `data`; emit each empty
   constructor as `[]`. *Disambiguated:* `List.json` (`"ctors":[[],[…]]}` — the
   `Nil` arm is `[]`).

5. **Canonical bytes carry no trailing newline.** Never stated, but hash =
   `SHA-256(canonical bytes)` and each `canonical/<name>.json` must be
   byte-identical to what we emit. A trailing `\n` would break both. Choice:
   emit exactly the compact object, no newline. *Disambiguated:* the fixture
   files' own SHA-256 equals their `hashes.txt` value (verified: no trailing
   byte).

## Surface elaboration

6. **§1.4 — a 0-parameter `defn` gets no lambda.** "The body is wrapped in one
   `lam` per parameter" leaves the 0-parameter case implicit: then `Def.ty` is
   just the return type and `Def.body` is the bare term. Choice: 0 params → no
   `lam`, `ty = ret`, body checked directly against `ret`. *Disambiguated:*
   reject fixture `body_type_mismatch.oath` = `(defn bad [] [] Int true)` — only
   rejects correctly if the bare `true` body is synthesized as `Bool ≠ Int`.

7. **§1.4 — how to recognize the `[tyargs]` bracket in an application.** Grammar
   is `(name [tyargs] arg ...)` but nothing says how tyargs are told apart from a
   value argument. Choice: a leading square-bracket form immediately after the
   head is the type-argument list; there are no bracket-valued *terms* in the
   language, so this is unambiguous, and a 0-tyvar callee simply has no bracket.
   *Disambiguated:* corpus — `(length [Int] xs)` vs `(count x xs)`.

8. **§1.4 — primitives are recognized before name resolution.** The name-
   resolution order (local, self, constructor, stored fn) omits primitives
   entirely. Choice: if the application head is one of the literal primitive
   strings (`+ - * / % neg == < <= and or not ++ str-len`), elaborate a `prim`
   before attempting variable/ctor/fn resolution. **UNTESTED** directly (no
   fixture shadows a primitive name), but consistent with every hash.

9. **§1.4 — constructor saturation vs. curried application.** "A constructor
   term is saturated by all remaining arguments; other applications elaborate to
   left-associated `app` chains." So a `ctor` collects its arguments into one
   flat `args` array, while a `self`/`ref`/local-var call nests `app`. Choice:
   exactly that. *Disambiguated:* `append.json` (`Cons` args are a flat array;
   `self`/`ref` calls are nested `app`).

10. **§1.2/§1.4 — "first field outermost" in a match arm.** Constructor fields
    become consecutive binders with the first field *outermost*, i.e. the
    highest de Bruijn index, and the last field is `var 0`. "Outermost" could be
    misread as index 0. Choice: push fields left-to-right so the last-declared
    field is innermost (`var 0`). *Disambiguated:* `append.json` `Cons h t`
    arm → `h` is `{"k":"var","idx":1}`, `t` is `{"k":"var"}`.

11. **§1.4/§9 — resolving a constructor to its ADT by name, when two ADTs share
    a hash.** `Interval` and `Run` are both `(data _ [] (_ Int Int))` and
    content-address to the *same* hash; only names distinguish them. Ctor
    resolution "scans the current name index in ascending name order and chooses
    the first ADT whose metadata contains the constructor name." Choice:
    implement that ascending-name scan over resolved data definitions. Works
    because constructor names are unique across the corpus. **UNTESTED edge:**
    no fixture has the same constructor name in two different ADTs, so the
    tie-break (ascending name wins) is unverified.

12. **§1.4 — cross-file, definition-before-use resolution.** Files reference
    each other (e.g. `sort.oath` uses `length`/`append`/`reverse` from
    `list.oath`). The task allowed either dependency ordering or lazy whole-
    corpus resolution; a referenced def's hash is the hash *I* computed. Choice:
    parse all files, then elaborate to a fixpoint, deferring any form that hits
    an unresolved name and retrying until no progress. *Disambiguated:* all 56
    hashes reproduce.

## The gate (static semantics)

13. **§2 — `rec` must be applied to exactly the ADT's parameters.** For a
    1-parameter ADT (`List`) the self-reference carries `args:[var0]`; for a
    0-parameter ADT (`Tree`) it carries no args. Choice: require `rec.args ==
    [Var0 … Var(n-1)]`, arity = ADT tyvars. *Disambiguated:* `List.json`
    (`{"k":"rec","args":[{"k":"var"}]}`) and `Tree.json` (`{"k":"rec"}`).

14. **§2 — instantiating constructor fields resolves `rec` to a concrete
    `data`.** When typechecking a `ctor`/`match`, each field type is instantiated
    by substituting the ADT's type arguments for `var` *and* rewriting `rec{args}`
    to `data{selfHash, args}`. Choice: a single `inst_field` pass does both.
    *Disambiguated indirectly:* the whole corpus type-checks (accept side of
    check 3) and every recursive-list/tree body relies on it.

15. **§2 — `==` "must not contain a function type."** I read this as: the shared
    operand type must contain no `fun` *anywhere* (including nested inside a
    data/record), not merely at the top level. Choice: recursive `contains_fun`.
    *Disambiguated (top level only):* `eq_on_function.oath`. **UNTESTED:** the
    nested case (e.g. `==` on a record whose field is a function) is not in any
    fixture.

16. **§2 — strict positivity through containers.** The polarity rule ("a `rec`
    argument to an arrow-free datatype keeps polarity, otherwise negative; the
    check conservatively over-rejects the covariant-through-an-arrow container")
    is fully specified but the *only* corpus/fixture case is `rec` directly to
    the left of an arrow (`negative_datatype.oath`). Choice: implemented polarity
    flipping plus a transitive `arrow-free` test on referenced datatypes; a
    `rec` inside the type-arguments of a non-arrow-free container is treated as
    negative. **UNTESTED** beyond the one direct-arrow reject — no fixture nests
    `rec` inside another datatype's arguments, so my arrow-free/over-rejection
    logic could disagree with the reference on an exotic datatype and nothing
    here would catch it.

17. **§2 — prop binders are concrete; the function's type variables are not in
    scope for props.** "Prop binders must be concrete (no `var`/`rec`)." I
    checked binders contain no `Var`/`Rec` and well-formed them with zero type
    variables in scope, and elaborate/check prop bodies with `nvars = 0` (only
    `self`'s tyarg count is tied to the def's tyvars). Choice as stated.
    **UNTESTED:** no prop in the corpus mentions a type variable, so "are the
    def's tyvars visible inside a prop?" is answered only by my reading of
    "concrete" + "a separate term scope containing only their binders."

## Suspected spec/fixture bugs (not just ambiguity)

- **§1.5 — the `zero_ctor_idx_omitted` golden does not contain a constructor.**
  Its note says "constructor index 0 omitted; bool false omitted", but the
  actual Def is `{"k":"func","tyvars":0,"ty":{"k":"bool"},"body":{"k":"bool"}}`
  — there is no `ctor` node at all; it only exercises `bool false` omission. The
  constructor-index-0 omission *is* exercised elsewhere (e.g. `Nil` in
  `length.json`), so identity is fine, but the dedicated golden is mislabeled and
  does not test what its name/note claim. Low severity; worth fixing the fixture
  so §1.5's "constructor index 0" clause has a real witness.

- **§1.5 vs §1.2 — "empty function `props`" is listed as a zero-value-omission
  case, which is correct, but "always-present" `Prop.binders`/`Prop.body` and
  the omitted-when-empty `props` array live at different levels (a `Prop`'s
  fields vs. the `Def`'s `props` list).** Not a contradiction once you separate
  the levels, but the bullet list in §1.5 reads as if they are peers. Minor
  wording hazard; both goldens (`empty_props_omitted`, `prop_body_always_present`)
  reproduce with my reading (omit the `props` array when empty; always emit a
  present `Prop`'s `binders` even when `[]`).
