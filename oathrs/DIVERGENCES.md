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

# DIVERGENCES — Stage 2 (dynamic conformance: eval, generation, analyses)

Checks 4-5. The evaluator, deterministic generation, the value printer, and the
§6 analyses had never been implemented independently, so the harvest is richer
than stage 1. **UNTESTED** marks a choice that no fixture actually exercises —
these are the real cross-kernel risk, because a passing conformance run does not
constrain them.

## Value printer / report format (§3.2, §5)

18. **The per-property line format is `{mark} prop {name:<24} {status}`** — mark
    is `✓` (U+2713) or `✗` (U+2717), then a literal ` prop `, the property name
    left-justified in a **24-column** field, a single separator space, then
    `passed 200 cases` or `FALSIFIED after N cases`. The 24 was reverse-engineered
    from a hexdump (status text begins at byte/col 32 = 1 mark + 6 `" prop "` +
    24 + 1). Nothing in the spec states this layout. *Disambiguated:* all 48
    `verify/*.txt` reproduce, including a name longer than 24
    (`antidistributes-over-append`), which shows the field does not truncate and
    the single separator space remains.

19. **`FALSIFIED after N cases` counts cases that PASSED before the failure**
    (0-indexed failing case): spin fails on case 0 → `after 0 cases`; bad-reverse
    on case 4 → `after 4 cases`. *Disambiguated* by both falsified fixtures.

20. **The counterexample line is `    counterexample: <inputs>` (4-space indent),
    inputs joined by `", "` in binder-declaration order, with an optional
    `  (runtime error: MSG)` (two leading spaces) appended** (§3.2). Inputs are
    printed from the *generated* values, before evaluation. *Disambiguated:*
    bad-reverse (two list inputs) and spin (the runtime-error suffix).

21. **Only two runtime-error message strings are observable.** `recursion too
    deep (likely non-termination)` (depth bound) is pinned by spin. The fuel
    message, `division by zero`, and `modulo by zero` are **UNTESTED** — every
    corpus function guards its divisors and nothing reaches the fuel bound, so my
    wording for those is a guess and could diverge.

22. **Value-printer branches beyond int/data are UNTESTED.** Only `Int` and data
    values (`(Cons -1 Nil)`, nullary `Nil` bare) appear in a counterexample. `Str`
    (Go `strconv.Quote`), `Record` (`{name value ...}`), `Closure` (`<fn>`), and
    the four `Native` renderings (`<fn x. x>`, `<fn x. A*x + B>`, `<fn _. V>`,
    `<fn {K→V ...} else D>`) are implemented from prose only — no fixture produces
    them, so their exact bytes are unverified.

## Evaluator (§3, §3.1)

23. **The §3.1 depth bound (100,000) assumes a growable host stack.** A direct
    recursive evaluator overflows Rust's 8 MiB main stack long before depth
    100,000, so the whole program runs on a worker thread with a 2 GiB stack (Go,
    the reference host, grows goroutine stacks automatically). Without this the
    kernel aborts instead of reporting the depth error. *Disambiguated:* spin's
    `recursion too deep` only reproduces once the host can actually reach the
    bound.

24. **Fuel accounting is barely constrained.** I charge 1 unit per term-node
    evaluation and 1 per application (§3.1). The *only* resource-bound fixture is
    spin, which hits the **depth** bound (~100k levels, well under the 2,000,000
    fuel), so the exact fuel count is never observed. A definition sitting exactly
    at the fuel boundary could reveal an off-by-some in my per-node vs
    per-application charging. **UNTESTED.**

25. **`ref`/`self` re-evaluate the definition body on every use** (§3), producing
    a fresh closure each time; `self` resolves against the *enclosing* def hash
    threaded through evaluation. Correct for the corpus, but the only stress case
    (spin) is a single self-call; mutual or nested self/ref interplay is lightly
    exercised.

## Deterministic generation (§4)

26. **Whether a single-candidate data choice consumes a draw is UNTESTED but
    assumed YES.** At size ≤ 0 the generator picks "uniformly among constructors
    with no recursive field"; when there is exactly one (e.g. `Nil` for a list at
    size 0) I still call `below(1)` (always 0, but it advances splitmix). This
    matters only when more values are generated *after* the size-0 sub-draw within
    the same case. In bad-reverse's reproduced counterexample the forced `Nil`
    happens to be the very last draw, so the fixtures do NOT distinguish
    draw-vs-no-draw here. A def whose counterexample depends on a size-0 sub-draw
    ordering could diverge.

27. **Passing properties do not constrain generation at all.** A property that is
    true for every input reports `passed 200 cases` under *any* generator, so of
    the whole §4 machinery only two things are actually pinned: integer generation
    (spin's `-1`) and `List Int` generation (bad-reverse's two lists). Everything
    else — the `Str` alphabet/length, `Bool`, `Int -> Int` identity/affine/table,
    general table functions (`n = 1 + below(3)`, key-then-value order, trailing
    default), record generation, and the exact recursion/`below` draw order for
    data — is implemented to spec but **UNTESTED** for draw-exactness. This is the
    single largest unverified surface in stage 2.

28. **Seed derivation and splitmix64 are exact and validated.** `base` = BE u64 of
    the first 8 hash bytes; `s = base ^ (pi<<32) ^ (c * 0xD1B54A32D192ED03)`;
    `size = c mod 8`; each case reseeds from its own `c` (draws do not carry across
    cases). Reproducing both counterexamples byte-for-byte pins these.

## Termination (§6.1)

29. **The lexicographic multi-position descent is exercised only by `merge`.** My
    implementation backtracks over head positions, discharges `lt` sites, and
    recurses `eq` sites on the remaining positions; merge (descends xs on one
    branch, ys on the other) verifies `structural`. Single-position descent
    (length, map, sum, …) and the `unknown` cases (spin) also reproduce. The
    "self without a full application spine is conservative" clause is **UNTESTED**
    (every self-call in the corpus is fully applied).

## Confinement (§6.2)

30. **The blessed-closure rule and the whole `inLam` machinery are UNTESTED.** No
    example body contains a `(fn ...)` literal or any inner lambda, so `inLam` is
    always false throughout, and the "lambda passed to a confined callee position
    is a blessed closure" path (the `(map (fn [u] ((. net fetch) u)) urls)` idiom
    from the spec) never fires. Implemented from prose; unverified. The five
    higher-order params that *are* tested (greet/greet-or-guest `net` → confined,
    map `f` → confined, leak/stash `net` → escapes) all reproduce, including
    "a type variable in result position counts as data" (map's `f : a -> b`).

## Mutation (§6.3)

31. **Mutation scores are STORE METADATA, recorded selectively — a conforming
    kernel cannot predict WHICH definitions carry one.** My engine reproduces all
    **41** fixture-recorded scores exactly (abs 3/5, length 7/7, merge 8/11, rot
    20/22, e-mod 17/20, t-member 4/5, …). But four `tested`/`proven` definitions
    with well-defined, non-empty mutation sets — `count`, `rot-h2`, `rot-h3`,
    `rot-hl` — carry **no** score in the fixtures (I compute 3/9, 9/9, 10/12,
    10/12). §8 makes mutation a per-hash verdict field populated by an explicit
    `oath mutate`; it is not a pure function of the `Def`. Consequence: check 5
    can only assert "every score the reference recorded, we reproduce" — my
    harness compares mutation *only where the fixture has it*.

32. **The self-chain "forgot to recurse" mutation fires only at the MAXIMAL
    application**, not at inner sub-applications of the same chain. I gate it on
    "this App is not the function-child of another App". Firing it at every
    sub-app would inflate totals. *Disambiguated* by matching counts (e.g. abs 5,
    count 9, merge 11).

33. **Match-arm-body swaps cover ALL pairs `(i,j)` with `i<j`, not just
    adjacent**, and cross-arity swaps are left in the candidate set to be rejected
    by the typecheck gate (de Bruijn misalignment). Int-literal mutations are
    `{old+1, old-1, 0}` minus the unchanged value, with exact duplicates collapsed
    by the by-hash dedup (original hash pre-seeded, typecheck-failing mutants
    dropped before counting). All *disambiguated* by matching the 41 totals.

## Guarantee / analyses coupling

34. **Check-5 files depend on check-6 (SMT) outputs, so stage 2 cannot fully
    satisfy them.** The `analyses/*.json` guarantee block carries a proof-derived
    `level: "proven"` and a `proven: N` count (§7.3) for 30 definitions. These
    come from the SMT prover, which the brief defers to stage 3. I compute the
    testing-derived base level (`asserted`/`tested`/`falsified`) and omit
    `proven`; my harness compares `level` modulo the proof upgrade
    (`proven` ≡ `tested`) and skips `proven`. The MANIFEST lists analyses (5) and
    proof (6) as separate checks, but the analyses *level* is not reconstructible
    without the proof outcomes — worth flagging as a spec/fixture coupling.

35. **Field presence in the analyses record is conditional and had to be
    inferred:** `termination`/`confinement` for every func; `cases: 200` iff
    `level ∈ {tested, proven}`; `mutants_*` only when a score exists AND is
    non-zero (`abs-small` has a body with no mutable node → 0 mutants → the fields
    are omitted, like a zero-value omission); `proven` iff `> 0`. Data defs carry
    only `name/hash/kind/level:"asserted"`.

## Suspected bug / fixture issue (stage 2)

- **The check-5 fixture set is internally inconsistent for mutation.** It presents
  `analyses/*.json` as the ground truth for "mutation ... match", yet four
  `tested`/`proven` definitions omit their (well-defined) mutation scores because
  `oath mutate` was never run on them in the reference store. A second
  implementation doing a naive byte/field comparison of the whole file will fail
  on `count`, `rot-h2`, `rot-h3`, `rot-hl` through no fault of its analysis
  engine. Either those scores should be materialized, or the manifest should state
  that mutation presence is store state, not a derived verdict.
