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
   before attempting variable/ctor/fn resolution. **NOW TESTED** (was untested
   in stages 1-3): the `gate/accept` corpus added `def_named_like_primitive`
   (a def literally named `+`, whose callers' `(+ x x)` heads still resolve to
   the primitive) and `primitive_head_wins` (`(not not)` — head is the `not`
   primitive, the argument is a local variable `not` shadowing the name). My
   kernel accepts both, confirming the head-primitive-wins rule and that the
   *argument* position still resolves the shadowing local variable.

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
    *Disambiguated (top level):* `eq_on_function.oath`. **NOW ALSO TESTED nested**
    (was untested): the `gate/reject` corpus added `eq_on_record_with_function`
    (`==` on a record type whose field is a function); my kernel rejects it,
    confirming the recursive `contains_fun` reading matches the reference.

16. **§2 — strict positivity through containers.** The polarity rule ("a `rec`
    argument to an arrow-free datatype keeps polarity, otherwise negative; the
    check conservatively over-rejects the covariant-through-an-arrow container").
    Choice: implemented polarity flipping plus a transitive `arrow-free` test on
    referenced datatypes; a `rec` in the type-arguments of an arrow-free
    container keeps polarity, otherwise it is treated as negative. **NOW TESTED**
    (was untested beyond the one direct-arrow reject): the new fixtures put both
    signs on trial — `gate/accept/positive_through_container` (`(data W [a]
    (Wrap a))` then a rose-tree `(data Rose [] (Node Int (W Rose)))` — `rec`
    nested in an arrow-free container in *positive* position) and
    `gate/reject/negative_through_container` (`(data D [] (C (-> (W D) Int)))` —
    the same nesting to the *left* of an arrow). My blind logic accepts the
    first and rejects the second, agreeing with the reference on first contact —
    the arrow-free-container polarity path that nothing had ever exercised.

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

# DIVERGENCES — Stage 3 (SMT / proof conformance: checks 6 + full 5)

Check 6, and the proof-derived upgrade that closes check 5 at full byte
equality. The SMT-LIB generation had never been implemented independently and,
as expected, produced the richest harvest. There is NO byte-level `.smt2`
fixture — only `prove/outcomes.json` (per property: `proven` true/false) — so my
encoding only has to be *logically* faithful enough that z3 4.16.0 returns the
same sat/unsat. All 48 definitions / 189 property outcomes reproduce.

## The load-bearing finding: lemma availability is a FIXPOINT, not an order

36. **§7.2's "proven earlier in the same run" underspecifies the lemma set; the
    real behaviour is a fixpoint over all-but-self proven properties.**
    `reverse.involution` (property index 0) is `proven` in the reference, but its
    inductive step needs `antidistributes-over-append` (index 1) as a lemma. Read
    literally — self-lemmas are only *earlier-indexed* proven props — involution
    cannot use a later sibling and z3 cannot close it: I got exactly 188/189,
    missing only involution. The reconciliation is §7.3's metadata: `proven_props`
    ACCUMULATES across `prove` runs, so by the time outcomes are recorded, BOTH
    of reverse's props are prior lemmas, each excluded only from its own proof. I
    reproduce this with a fixpoint — a property's self-lemmas are every OTHER
    proven property of the same definition, iterated to stability — which flips
    involution to proven and yields 189/189. This is precisely the "outcome
    depends on which lemmas exist when a prop is attempted" caveat the brief
    flagged. **Suspected spec bug:** §7.2 as written yields 188/189; it should say
    the lemma set is the fixpoint of proven props (all-but-self), or that outcomes
    are read after `proven_props` stabilises.

## Translation choices (SPEC §7.1, §7.2)

37. **Internal SMT naming (no byte fixture to match).** §7.1 prescribes sanitized
    sort/constructor/selector names (`Rec_<field>_<sort>`, `mk_<recordSort>_…`,
    `<constructor>_<fieldIndex>`, data names from metadata + type-arg sorts).
    Since only outcomes are checked, I use my own consistent scheme (data sort
    `T<hash8>_<argsorts>`, ctor `<Ctor>_<sort>`, selector `<ctor>_<idx>`, record
    `Rec_<field>_<sort>` with ctor `mk_<sort>`). A DIFFERENT-but-equivalent
    encoding; outcomes match. The §7.1 collision warning about metadata-derived
    names is sidestepped (hash-prefixed) and therefore **UNTESTED**.

38. **`/` and `%` exclusion fails the whole goal.** Any property whose
    translation reaches `/` or `%` — directly, or through an inlined non-recursive
    callee — aborts translation and is unproven. This is why `e-div`, `e-mod`
    (all), and every `rot*` variant (rot uses `%`) are 0/n. `rot` is
    non-recursive, so the `%` surfaces in the inlined goal and kills it before any
    z3 call (fast, no timeout).

39. **Induction is single-binder, structural, over datatype binders only.**
    `rle-expand` recurses on an `Int` (`n-1`); its only datatype binder is `Run`
    (one constructor, no self-recursive field → empty induction hypothesis) → z3
    cannot discharge → 0/5. `merge` needs a lexicographic 2-binder induction;
    single-binder leaves the other branch's self-call undischarged → 0/3. Both
    match, and both fall out of the single-binder rule with no special-casing —
    the `merge.oath` comment ("a prover rung that does not exist yet") is
    reproduced exactly.

40. **Quantifier-free `sat` = refutation; induction only when quantified.** With
    no recursive-function axioms or quantified lemmas present (e.g. `abs-small`
    once `abs` is inlined), z3's `sat` is a genuine refutation and I skip
    induction (SPEC §7.2). `abs-small.bounded-wrongly` is refuted → unproven,
    matching the "undertested" exhibit. This also makes every non-recursive
    Int/Bool/record/interval definition decide instantly.

41. **Recursion axiom gated on totality (§7).** A recursive callee is
    `declare-fun` + a `forall` defining-equation axiom with the application as
    `:pattern`, but only when its termination verdict is total; otherwise it is
    left uninterpreted. No non-total recursive callee reaches a provable goal in
    this corpus, so the ex-falso soundness guard is correct-by-construction but
    **UNTESTED** here.

42. **Non-recursive callees inlined by beta-reduction; `self` treated as a call
    to the definition.** Matches §7.2. `match` becomes a tester/selector `ite`
    chain; records are single-constructor datatypes; function-typed values use
    `(Array dom cod)` + nested `select`.

## Timeout and outcome stability

43. **Per-goal solver budget: the spec's 15 s is normative and now the default;
    a shorter budget is outcome-affecting — CONFIRMED IN CI.** Every goal that is
    provable at all in this corpus closes well under a second on fast hardware
    once its lemmas are present, so an initial 4 s budget reproduced all 189
    outcomes locally at ~a third of the wall-clock, and I flagged (matching the
    stage-2 brief's caveat) that a loaded/slow machine could push a borderline
    goal past a short budget and flip an outcome. **That prediction was validated
    empirically:** the GitHub Actions conformance job (ubuntu runner, slower than
    the dev Mac, z3 pinned 4.16.0) failed check 6 with "proof outcomes differ for:
    sum" — one of `sum`'s inductive goals crossed the 4 s budget on slower
    hardware, and check 5's `sum` analysis file diverged in consequence.
    **Resolution:** the budget defaults to the spec's normative 15 s and is
    configurable via the `OATHRS_Z3_TIMEOUT_MS` env var (milliseconds, must be
    > 0) so fast local runs can still opt down explicitly. Enforced with z3 `-t:`
    (soft) plus a `-T:` hard process cap (`ms/1000 + 2` s) so a runaway quantified
    goal cannot hang the run. The lesson generalizes: any wall-clock budget below
    the spec's is a latent conformance hazard on unknown hardware — the deviation
    was documented, but "documented" is not "safe," and CI is where such
    load-dependent divergences actually surface.

44. **`sat` vs `unknown` are not distinguished in the outcome.** I record proven
    only on `unsat`; `sat`/`unknown`/timeout/process-failure all map to
    `proven: false`, because `outcomes.json` records only a boolean. §7
    distinguishes refuted (report the model) from unproven for *reporting*; the
    fixture does not, and no proof **method** is recorded either, so §10 point 5's
    "methods MAY differ" is moot against this fixture.

45. **Falsified definitions are never proved (§7.3).** `spin`, `bad-reverse`: all
    props `proven: false`, because the tested→proven upgrade requires a prior
    `tested` level and I skip proving falsified defs entirely. Notably
    `bad-reverse.involution` would be trivially provable (reverse≡identity ⟹
    xs==xs) but is correctly left unproven because the definition is falsified.

## Performance note

- Check 6 is the slow gate: one z3 subprocess per goal plus structural
  induction, ~7 min at the 4 s budget (dominated by the ~40 genuinely-unprovable
  goals that run to the timeout, re-attempted as the lemma fixpoint iterates).
  This is inherent to driving a real solver per property and is documented rather
  than hidden.

# DIVERGENCES — Stage 4 (the wasm32 port: host assumptions made explicit)

Cross-compiling the kernel to `wasm32-wasip1` (target added via rustup; no wasm
runtime is installed here, so the deliverable is the green cross-compile plus a
documented `wasmtime` invocation — see `wasm-demo.sh`). The port is the sharpest
lens yet on what the kernel silently assumes about its host.

46. **Structure: pure library + native-only prover.** The kernel is now a library
    crate (`src/lib.rs`: identity, gate, eval, gen, verify, analyses) plus a thin
    CLI binary. The library is pure computation — it imports ZERO host functions —
    so it cross-compiles to wasm unchanged and would embed in a browser
    (`wasm32-unknown-unknown`) equally well. Only the CLI binary pulls host I/O.
    Chosen target `wasm32-wasip1` over `unknown-unknown`: the library needs no
    shim either way, and WASI covers the CLI's file/args/stdio with no
    hand-written JS glue. The built `oathrs.wasm` imports ONLY
    `wasi_snapshot_preview1` (16 functions: args, `path_open`/`fd_read`/`fd_write`
    under a `--dir` preopen, `random_get`, `proc_exit`) — verified by parsing the
    module's import section; no z3, no thread, no custom host.

47. **z3-as-subprocess is impossible in wasm ⇒ `prove` is native-only, behind a
    default cargo feature.** `--no-default-features` drops the `prove` module (the
    sole `std::process` user) cleanly. **Conformance consequence:** a pure-wasm
    deployment can run checks 1-5 *minus the proof-derived guarantee upgrade*
    (identity, gate, verify, termination/confinement/mutation, testing level).
    Check 6 and check 5's `level:"proven"`/`proven` fields require the native
    build with the `prove` feature and a z3 on PATH. This is an inherent host
    dependency of the SMT boundary (§7.1), not a portability defect.

48. **The §3.1 depth bound is a host-stack assumption, and wasm makes it
    unavoidable.** Stage 2 already found the evaluator recurses one host frame per
    nested Oath evaluation, needing a 2 GiB worker thread natively to reach the
    100,000 depth bound before overflowing. wasm32 has **no threads**, so that
    escape hatch is gone: the module runs on its own linear-memory stack (~1 MiB
    by default), which reaches far fewer than 100,000 frames. **Consequence for
    conformance:** on a default wasm build, a deep evaluation — the non-terminating
    `spin`, which walks to the depth bound — traps (wasm stack exhaustion) instead
    of producing the normative `recursion too deep (likely non-termination)`
    counterexample, so `spin`'s verify transcript is NOT reproducible on wasm
    without extra configuration. Terminating examples (lists ≤ 7, trees ≤ 7) stay
    well within the default stack, so the hash+gate+verify demo works. Reproducing
    the depth-bound case on wasm requires a larger stack, configured at build time
    (`RUSTFLAGS='-C link-arg=-zstack-size=<bytes>'`) and/or by the runtime
    (`wasmtime --max-wasm-stack=<bytes>`). The clean fix — a heap-allocated
    explicit evaluation stack so depth is independent of the host — is noted but
    out of scope for this port; the compromise is documented rather than hidden.
    The reference (Go, growable goroutine stacks) never had to confront this,
    which is exactly why the assumption stayed invisible until an independent
    port on a non-growable stack surfaced it — twice now.

49. **Determinism survives the host RNG.** `random_get` appears among the wasm
    imports because Rust's default `HashMap`/`HashSet` seed their hasher from the
    OS RNG. No kernel output depends on hash-map iteration order — everything that
    is serialized or compared uses `BTreeMap`/`Vec` (the object store, sort/order
    outputs) while `HashSet`/`HashMap` are used only for membership (positivity
    `visited`, mutation `seen`, elaboration name sets). So native and wasm produce
    identical hashes/verdicts/counterexamples despite per-run hasher seeding. This
    is a latent invariant, not an enforced one: any future code that iterates a
    `HashMap` into hashed or printed output would diverge across hosts (and across
    runs). **UNTESTED** here beyond the argument, since no wasm runtime was
    available to diff outputs — but the import surface confirms the RNG is the
    only nondeterminism source, and it is confined to hashing.

50. **`conformance.sh` stays native-only, by design.** It exercises the `prove`
    feature and (via `spin`) the full depth bound, neither of which a portable
    wasm build supports, and no runtime is installed to execute wasm here. The
    wasm deliverable is therefore a separate `wasm-demo.sh` (green cross-compile +
    import audit + documented invocation); native `conformance.sh` remains green
    and unchanged.

# DIVERGENCES — Stage 5 addendum (fixpoint perf, issue #24)

51. **Fixpoint re-attempt gating (§7.2 permission, issue #24) — and a
    budget-sensitivity it exposes.** The proof fixpoint now (a) processes
    definitions in topological order so a definition's transitive-dep proofs are
    FINAL before it is proved, computing its dep-lemma set once; and (b) runs a
    per-definition LOCAL fixpoint over that definition's own properties, gating
    re-attempts on lemma-set growth — a goal is re-attempted only when a
    same-definition sibling has been proven since its last failed attempt. This
    is the §7.2 permission ("a goal whose available lemma set has not changed
    since its last failed attempt need not be re-attempted"), and it makes each
    genuinely-unprovable goal burn its full solver budget ONCE rather than once
    per global cascade pass. It is outcome-identical to the naive global loop as
    a *fixpoint* (the dep graph is a DAG, so the per-definition decomposition is
    exact), and reproduces all 52 definitions / 198 property outcomes at the 15 s
    default.

    **UNTESTED / caveat — budget-sensitivity from front-loading the transitive
    lemma queue.** "Outcome-identical" holds for the fixpoint's *proven set*, but
    NOT necessarily for the outcome of a single goal *at a fixed wall-clock
    budget*. The old global loop happened to prove some goals EARLY, in a pass
    before all of their transitive-dep lemmas existed, i.e. with a *smaller*
    axiom set — which is faster. The topological order instead hands each goal
    the FULL §7.2 transitive lemma queue up front; a larger quantified-axiom set
    can slow z3 enough to cross the budget. Concretely, `sum`'s inductive goals
    proved at a 4 s budget under the old loop (small early axiom set) but now
    need the normative 15 s (they carry, e.g., `reverse.involution` as an extra
    lemma that is useless to them but still instantiated). At the spec-normative
    15 s default all 198 outcomes reproduce; but the change ERODES `sum`'s
    headroom — the same margin the CI `sum` failure (entry #43) was about — so it
    is a latent robustness trade the operator should know: the more spec-faithful
    "full lemma queue every time" is budget-heavier than an incremental
    "prove-with-whatever-suffices-first" strategy. The `OATHRS_Z3_TIMEOUT_MS`
    override remains the escape hatch, and a reduced local budget (e.g. 4 s) now
    flips `sum`, which it did not before.

# DIVERGENCES — O1 binary encoding (identity re-spec, issue #7)

SPEC §1 was rewritten: canonical bytes are now the "O1" tag-length-value binary
tree, not canonical JSON. A fresh independent read of a fresh encoding spec.
The six goldens reproduce byte-for-byte, all 62 corpus hashes + canonical/*.bin
are byte-identical, and the strict decoder round-trips and rejects the malformed
cases. Findings and settled ambiguities:

52. **The 2-byte magic `4F 31` is part of the HASHED bytes, not just the stored
    file.** §1 says "canonical-bytes is the O1 encoding … the exact bytes the
    store persists" and "canonical bytes begin with the 2-byte magic", so
    `SHA-256` covers `magic ++ Def`. An implementer could plausibly hash the Def
    body alone and treat the magic as a file wrapper — that yields different
    hashes. *Disambiguated:* `bool_bytes` hashes `4F 31 02 …`, i.e. including the
    magic (verified against the manifest hash).

53. **Tag namespaces overlap across node kinds and are position-dependent.** Def
    `data`=0x01 / `func`=0x02 collide byte-for-byte with Ty `int`=0x01 /
    `bool`=0x02; Ty tags (0x01-0x08) and Term tags (0x10-0x1E) are disjoint but
    the Def tag shares Ty's range. The decoder must pick the tag table from
    grammar position (the byte right after the magic is a Def tag), not from a
    single global table. A decoder that unified tag tables would mis-decode. Not
    ambiguous in the spec, but a real trap; flagged.

54. **Fixed widths, and the u32/i64 split.** Counts and indices are 4-byte
    big-endian `u32` (Ty `var`, Term `var`, `ctor` index, every list count,
    tyvars/ctor/prop counts); only integer *literals* (Term `int`) are 8-byte
    big-endian two's-complement `i64`. Easy to conflate the two widths, or to use
    little-endian. *Disambiguated:* `negative_int` pins `-401` =
    `FF FF FF FF FF FF FE 6F` (big-endian i64), and `empty_lists` pins the u32
    zero counts.

55. **No escaping and no field omission — two stage-1 rulebooks are now DEAD.**
    O1 strings are `u32` length ++ raw UTF-8 (`raw_strings` shows `"` `\` newline
    `<>&` U+2028 U+2029 all verbatim), so the entire stage-1 string-escape
    apparatus (old entries #1's Go/HTML `<` escapes, operator names getting
    escaped) is moot. And every field is always written — there is no zero-value
    omission — so the old "omit var/idx/int/bool/empty-array when zero" logic and
    the two-`var`-field-name trap (#1) and args-before-names ordering (#2) and
    no-trailing-newline (#5) are all gone. The re-spec deleted an entire category
    of divergence risk by construction: one encoding per definition, enforced by
    a strict decoder.

56. **Hash references are 32 raw digest bytes, held internally as hex.** On the
    wire a reference is the referenced def's 32-byte SHA-256 (natural digest byte
    order, i.e. what lowercase-hex reads left-to-right). I keep hashes as hex
    strings internally (so elaboration/checking/eval are unchanged) and convert
    at the codec boundary. *Disambiguated:* `hash_reference` = `00 11 … FF 00 11
    … FF` equals hex `0011…ff0011…ff`.

57. **"Strictly ascending" record names is enforced by the DECODER, redundantly
    with the producer.** The producer sorts+dedups record fields (§1.3); the
    strict decoder independently rejects names that are not strictly ascending
    (which also rejects duplicates). Both are required — the decoder cannot trust
    the producer, since objects can be written straight into the store (§8.1).
    My strict decoder rejects a descending-name record.

58. **Canonicality rests on "no trailing bytes"; there is no total-length
    field.** The O1 tree is self-delimiting via counts, with no envelope length,
    so a decoder MUST verify it consumed exactly `len(bytes)` and reject any
    surplus — otherwise a second encoding (valid Def ++ junk) would exist. My
    decoder checks the cursor lands exactly at end. **UNTESTED corner:** the spec
    says strings are "raw UTF-8"; my decoder validates UTF-8 and rejects invalid
    sequences, but no fixture exercises an invalid-UTF-8 body (the encoder only
    ever emits valid UTF-8), so the reference's exact stance on malformed UTF-8
    at decode time is unconfirmed.

## O1 re-conformance: generation/mutation findings (#7)

59. **Constructor selection ALWAYS draws one `below(k)`, including `k == 1`, in
    both size branches (SPEC §4, now explicit; resolves the untested #26).**
    History worth recording honestly. #26 flagged as untested whether a
    single-constructor choice draws `below(1)`. During O1 re-conformance I first
    concluded the two size branches were *asymmetric* — `size > 0` skips the draw
    for a lone constructor, `size ≤ 0` draws it — because that was the only rule
    that reproduced `i-hull` (then `12/15`), `merge` (`8/11`), and everything
    else at once. **That was overfitting to a stale fixture.** Adjudication
    against the reference established that `i-hull`'s `12/15` had itself been
    carried across the O1 identity change without re-scoring (mutation scores are
    facts about structure × *seed*, and seeds derive from hashes); the reference
    re-scores `i-hull` at `11/15` under O1. The true generator semantics, now
    stated explicitly in SPEC §4, is **no asymmetry**: selection consumes exactly
    one `below(k)` draw in every case, `k == 1` included. `merge`'s `8/11`
    reproduces under this rule too — its discriminating draws are on the
    multi-constructor `List`, never on single-constructor `Interval`. `gen.rs`
    draws `below(nctors)` (size > 0) and `below(#non-recursive)` (size ≤ 0)
    unconditionally. Lesson: a fixture is only evidence once you know it was
    regenerated under the current identity.

60. **`i-overlaps` mutation `9/11` (VINDICATED) and mutation is scored for
    falsified definitions too.** Two connected points:
    (a) I reported `i-overlaps` as `9/11` against a fixture claiming `11/11` and
    argued from first principles it was a stale fixture, not a kernel bug — the
    mutant encoding differs from the hash-matching original by exactly one op
    string, the seed/generation/catalog/60-case budget are all spec-exact, and
    the two `<=`→`<` survivors are first falsified only at cases 95 and 78 (>60).
    Adjudication **confirmed** this: the reference itself computes `9/11` under O1
    seeds; the `11/11` was carried from the pre-O1 store. The migrator now drops
    seed-dependent verdicts so migrations can't repeat this.
    (b) Fixing the stale fixtures surfaced a real latent bug on *my* side:
    `analyze` skipped mutation scoring entirely for `falsified` definitions. The
    guarantee level and the mutation verdict are independent (§6): mutation runs
    the catalog regardless of level, and is simply omitted when the total is 0.
    `spin` (falsified, non-terminating) has one mutant — its `(spin x)` self-call
    chain collapses to the spine argument `x`, which terminates and is killed by
    `claims-zero` — so `1/1`; `bad-reverse` (falsified) has a bare-variable body
    (`xs`) with no mutable node, so total 0 and no score emitted. Removing the
    `falsified → skip` short-circuit makes both match.

## Lemma relevance filtering (#25)

61. **Footprint tracks BOTH function and data definitions as first-class
    members (SPEC §7.2).** SPEC §7.2 defines a goal's footprint as the definition
    under proof plus every definition its property's binders/body reference,
    closed transitively through definition *bodies* (props never extend it), and
    admits a dependency lemma iff its definition and its binders/body references
    all lie inside the footprint. Sibling lemmas (properties of the definition
    under proof) are admissible unconditionally — the load-bearing exemption that
    lets, e.g., `sort.idempotent` route through its sibling `sorted-is-fixpoint`
    even though that lemma mentions `is-sorted`, which `sort`'s footprint never
    reaches.
    *History (honest):* my first cut projected the footprint to the function
    call graph only, leaving data references untracked — outcome-preserving on
    this corpus (data reaches no functions, so the projection is a superset of
    the full rule for the lemma-reference test) but a LATENT cross-kernel
    divergence: a future corpus could have a lemma whose only out-of-footprint
    mention is a datatype, which my projection would admit and the reference
    would drop. Adjudicated: the tighter rule is correct — such a lemma drags an
    unrelated datatype's SMT declarations into the problem, exactly the noise the
    filter removes — so §7.2 now states data membership explicitly and I track it.
    Implementation: the footprint is seeded by the property's binder *types*
    (data hashes) and the full reference set of its body — functions (`ref`),
    datatypes named by constructors/matches/type annotations, and datatypes
    named by instantiation type arguments — then closed through member bodies:
    a function through its body term, a datatype through its constructor field
    types (a member datatype's referenced datatypes are members). The
    dependency-lemma test checks the lemma's binder and body references, data
    included, against the footprint. Confirmed: full corpus reproduces 198/198
    outcomes at 15s. The per-property footprint (vs the previous per-definition
    transitive-dep set) is what realises #25 — each proof's axiom set is bounded
    by what the goal can reach, not by library size. Interacts cleanly with the
    #24 fixpoint gate: the admissible dependency set is fixed per property (deps
    are final in topological order) and only siblings grow, so lemma-set size
    stays monotone and the growth gate is unchanged.

## Lexicographic induction + script stability (#17)

62. **Lexicographic induction — subgoal construction choices (SPEC §7.2).**
    Implemented after single-binder induction: for each ordered pair (i, j) of
    distinct datatype binders in ascending order, accept the first pair whose
    subgoals all discharge. Per constructor c of i's datatype: a no-recursive-
    field c gives one base subgoal (i := c(fresh), other binders at goal
    constants, no hypotheses, j NOT split); a recursive c splits on each
    constructor c' of j's datatype, with hypothesis family (a) — for each
    recursive field of c, the property with i := that field and all other
    binders (j included) universally generalized — and family (b) — for each
    recursive field of c', the property with i PINNED to the constructed
    c(fresh) value, j := that field, remaining binders generalized. Two points
    §7.2 leaves to the implementation, neither outcome-visible (induction scripts
    are not byte-oracle-pinned, only direct attempts are): (i) SUBGOAL AND
    HYPOTHESIS ORDER within a pair — I emit family (a) before (b) and iterate
    constructors in stored order; z3 sees all hypotheses as an unordered
    assertion set, so order cannot change `unsat`. (ii) FRESH NAMES for the
    doubly-split fields — `g<fi>` for the first binder's fields, `h<fj>` for the
    second's, `b<m>` for goal constants, `q<m>` for generalized binders; all
    deterministic per subgoal. Corpus effect as specified: `merge` proves 3/3,
    including `keeps-sortedness`.

63. **`q-drop.drop-back-only` and the road to a byte-identical prover (SPEC §7.1,
    §7.2 — the deepest N-version findings of the project).** I first observed
    `drop-back-only` proving for my kernel (3/5) but recorded unproven in the
    fixture (2/5), and traced it to SMT-script name-sensitivity: at a 15s
    wall-clock budget the two kernels' byte-different scripts landed z3 on
    opposite sides of a budget-edge goal. Rather than force it green I flagged it,
    and adjudication replaced the whole non-determinism substrate with a
    fully-specified, byte-identical prover. The complete regime, now normative and
    verified by a byte oracle (`fixtures/prove/scripts.txt` + `scripts/*.smt2`):
    - **Deterministic budget.** The per-goal budget is z3's machine-independent
      `(set-option :rlimit 400000000)` (env `OATHRS_Z3_RLIMIT`), not wall-clock;
      the outcome is a function of (script bytes, solver version, rlimit). A
      wall-clock cap (env `OATHRS_Z3_WALL_CAP_MS`, default 600000 — see entry 70
      for the 180000 → 600000 raise) is a pure
      safety net: if it fires the run is INVALIDATED (process exits, no verdict),
      never recorded as a timeout. (My sandbox is slow enough that 400M rlimit
      can exceed 180s wall, so I run conformance with a raised cap — outcome-
      neutral, since the cap can only invalidate, never decide.)
    - **Byte-identical scripts.** With the naming/ordering rules below, all 161
      direct-attempt scripts hash-match `scripts.txt` and all 8 golden `.smt2`
      texts match byte-for-byte. `drop-back-only` now converges to a stable
      UNPROVEN at 400M — cold and warm agree — one theorem honestly lost to the
      budget edge in exchange for a ledger that cannot flip. My earlier proof was
      real but non-reproducible; the deterministic regime is worth more.
    Two of the rules were SPEC GAPS §7.1 never stated — I could not have derived
    them, and they are logged as entries 64-65 below alongside the two structural
    rules that fell out of the byte-oracle diff (66-67).

64. **SMT symbols are metadata-derived, and function symbols carry an `fn_`
    prefix (SPEC §7.1; the prefix was a SPEC GAP).** SMT identifiers are the
    sanitized metadata name, not the hash: a data sort is `<defName><typeArgs>`
    joined by `_` (`List_Int`), a constructor `<ctor>_<sort>` (`Cons_List_Int`),
    a selector `<constructor>_<fieldIndex>` (`Cons_List_Int_0`), a record sort
    `Rec_<field>_<sort>…` with constructor `mk_<recordSort>` and field selectors
    `mk_<recordSort>_<field>`. Functions were the gap: §7.1 gave the data/ctor
    scheme but not the function scheme, and my initial `f_<hash8>` reproduced
    nothing datatype-shaped. The reference form is `fn_` + sanitized def name +
    sanitized type-arg sorts (`fn_length_Int`, monomorphic `fn_spin`); §7.1 now
    states it. This one fix took the byte-oracle match from 15/161 to 33/161 —
    every script with a datatype or function had been diverging on identifiers.

65. **A nullary constructor renders with a trailing space before its close paren
    (SPEC §7.1; a SPEC GAP).** Constructor declarations are `(<ctor> <sel>…)`
    with selectors space-joined AFTER a leading space, so a zero-field
    constructor is `(Nil_List_Int )` — a space precedes the `)`. §7.1 never
    said so; the golden `length-0.smt2`/`spin-0.smt2` pinned it. Now byte-
    normative and applied to data and record constructors alike.

66. **The lemma-candidate dependency closure is UNIFORM body+props at every
    level; per-property admissibility, not closure membership, does the
    filtering (SPEC §7.2).** The closure seeds from the definition-under-proof's
    body AND property references and traverses each dependency likewise — a
    dependency's own PROPERTIES extend the closure. So `t-size`'s script declares
    `fn_is_sorted`/`fn_count` because a transitive dependency's (`t-flatten`'s)
    properties reach them, even though `t-size` never calls them. I first
    mis-read this two ways — body-only traversal (dropped q-drop's prop-seeded
    `append`/`reverse`) and body+prop with a narrower footprint — before the
    q-drop and t-size goldens pinned the uniform rule. Relevance (#25) still
    filters *emission* per property via the footprint; it does not prune the
    *declaration/axiom* set, which follows the full candidate closure.

67. **No rollback: declarations and axioms from a partial or non-total-callee
    body translation REMAIN even when nothing asserts them (SPEC §7.2).** A goal's
    script is built by translating every candidate lemma and every callee body
    for its side effects (registering datatypes/functions in first-touch order),
    and those registrations are never rolled back. Two consequences the goldens
    require: (a) a candidate lemma whose formula falls outside the fragment is
    skipped from emission but its already-registered symbols persist (q-drop-2
    declares `Option_Int`, used by no assertion); (b) a recursive callee's body
    is translated to discover its own callees regardless of totality, but the ∀
    defining axiom is asserted ONLY when the callee is proven total — so a
    non-total callee like `rle-expand`, reached through non-total `rle-decode`'s
    body during goal translation, is DECLARED (orphan) though neither gets an
    axiom (rle-encode-0). Implementation: `build_axiom` always translates the
    body; the totality gate wraps only the `(assert …)`. This is the same
    no-rollback principle the deterministic-script regime rests on.

68. **Per-script determinism is NOT verdict determinism: the proof fixpoint is
    itself path-dependent, and recorded verdicts must be RUN-STABLE (SPEC §7.2;
    found only by cold re-derivation of the whole corpus).** After the byte
    oracle went green — every script byte-identical, every outcome therefore
    reproducible FROM A GIVEN recorded state — a cold-parity run (fresh store,
    all proofs re-earned from ∅) still diverged on one definition: `q-drop`
    cold-converged to proven {0,1,2} while the settled store records {0,2}.
    Mechanism: `drop-back-only` (index 1) proves from a SMALL in-run lemma set
    (early in a cold pass, before `drop-empty` is proven) but FAILS once
    `drop-empty` is also asserted — a budget-limited (rlimit-bounded) solver is
    NON-MONOTONE in its axiom set; an extra, irrelevant lemma diverts the search
    into rlimit exhaustion. So a single cold pass RECORDS a proof that the very
    state it records cannot reproduce (re-running prove on that store drops it,
    {0,1,2} → {0,2}). The growth fixpoint alone is thus insufficient: its result
    depends on the ORDER siblings happened to prove within the run.
    Fix (now normative, and implemented as an outer loop around the growth
    fixpoint): a two-level fixpoint. Inner = the lemma-growth iteration. Outer =
    RUN STABILITY — the recorded set S must satisfy S = F(S), where F(S) attempts
    every non-falsified property once with candidate lemmas drawn from the fixed
    state S; iterate F from ∅ until stable (bounded 8 rounds; the corpus worst
    case is ∅ → {0,1,2} → {0,2} → stable for q-drop). The conformance outcome is
    defined as this limit, which converges to the warm verdicts (q-drop 2/5).
    Script bytes are UNCHANGED — the outer loop is run-level control flow; every
    attempt's script is still f(goal, recorded state), so scripts.txt and all 8
    goldens remain byte-identical and outcomes.json is untouched (every warm
    corpus state was already F-stable; only a cold pass could expose the hole).
    This is the N-version process at its sharpest: two independently-built
    kernels agreeing byte-for-byte on every emitted script still disagreed on a
    verdict, because the disagreement lived in the control flow that CHOOSES
    which scripts to emit and in what state — a layer no per-script oracle can
    see. Only re-deriving the entire corpus from nothing surfaced it.

69. **The run-stability round is a full inner GROWTH fixpoint (Gauss-Seidel),
    not a single pass (Jacobi) — the scheme, not just the criterion, must be
    pinned (SPEC §7.2).** My first cut of #68 ran each F-round as a single pass
    that attempted every property against the FIXED recorded state, deferring
    all in-round proofs to the next round (Jacobi iteration). The reference runs
    each round as the inner growth fixpoint: a property proven in-run
    IMMEDIATELY joins the candidate pool for later attempts in the SAME round
    (Gauss-Seidel), definitions in dependency order, properties in ascending
    index order, re-iterating until none newly proves. The two schemes have
    PROVABLY IDENTICAL stability criteria — S is stable iff every pi∈S proves
    against exactly S∖{pi} and every pj∉S fails against S∖{pj} — so on any
    definition with a unique reachable stable state (the entire current corpus)
    they agree. But when multiple self-consistent states exist, the PATH from ∅
    selects which one is reached, and Jacobi and Gauss-Seidel take different
    paths (and would record different last-states should the 8-round bound ever
    fire on a cycle). Determinism across independent kernels therefore requires
    pinning the ITERATION SCHEME, not merely the fixpoint equation. Implemented
    as the inner growth loop (candidate state = `recorded ∪ in-run`, minus own)
    wrapped by the outer S = F(S) iteration. Byte oracle unaffected: at the
    stable round in-run equals recorded, so every attempt still sees exactly
    S∖{own} — the state scripts.txt and the goldens encode.

70. **The wall-clock SAFETY CAP is outcome-relevant the instant an implementation
    RECORDS instead of INVALIDATES — and taking the spec literally exposed the
    reference doing the former (SPEC §7.2).** SPEC §7.2 says the deterministic
    budget is the rlimit and the wall-clock cap is a pure safety net: if it fires
    before the rlimit is reached, the run is INVALIDATED and no outcome is
    recorded. My kernel implemented that literally — `run_z3` exits(3) on cap
    expiry, aborting the whole prove with nothing recorded. Run through
    conformance on reference-class hardware, that exit(3) fired: burning 400M
    rlimit on quantifier-heavy goals legitimately exceeds a 180s wall cap there.
    The reference kernel, it turned out, was NOT invalidating — its `runZ3` used a
    context timeout that silently killed z3 at the cap and returned truncated
    output, which parsed as `unknown` and was RECORDED as unproven. So a goal that
    would have proven at minute four was recorded unproven the moment wall-clock,
    not rlimit, cut it off — a machine-dependent verdict, the precise disease the
    whole rlimit regime exists to cure. Two independent kernels, byte-identical
    scripts, and the disagreement was in what each did when the SAFETY net caught
    a slow-but-legitimate attempt: one recorded a hardware-dependent guess, the
    other refused. The reference now returns a cap-hit sentinel and aborts the run
    on invalidation, and the cap is raised 180s → 600s (a safety cap must sit far
    above legitimate rlimit exhausts; 180s demonstrably does not on real
    hardware). My change was one line — `DEFAULT_WALL_CAP_MS` 180000 → 600000; the
    exit-and-record-nothing semantics were already correct. Only a blind kernel
    that implemented the spec's invalidation rule instead of inheriting the host
    language's silent-timeout habit could have surfaced this.

## Attempt validity: non-verdict telemetry gating (#29)

71. **A non-verdict is an outcome ONLY when the solver's own telemetry proves the
    attempt was deterministic — the wall cap (entry 70) generalized (SPEC §7.2,
    #29).** §7.2 now makes the wall cap one instance of a general rule: anything
    other than `unsat`/`sat` is recordable ONLY when z3's appended
    `(get-info :rlimit)` and `(get-info :reason-unknown)` BOTH parse and the
    reason is deterministic — a genuine budget exhaust (`"canceled"` with
    consumed rlimit ≥ the budget) or a solver incompleteness give-up (any
    non-`canceled`, non-`memout` reason, a pure function of the script).
    Missing telemetry (process died mid-attempt), `memout` (memory bound fired),
    and `"canceled"` below budget (external cancel) are the ENVIRONMENT talking
    and INVALIDATE the run: record nothing, `eprintln!` a FATAL naming the failing
    condition, `exit(3)` — identical semantics to the wall cap. Implemented in
    `run_z3`: the two get-info lines are appended AFTER the core script (outside
    the byte-oracle hash, like the prepended `:rlimit` option), the first
    `sat`/`unsat` line in stdout is still taken as the verdict unconditionally
    (check-sat output precedes the get-info responses), and any other result is
    routed through `classify_nonverdict`, which parses the two infos with
    `info_int`/`info_value` (the value parser handles z3's quoted-string,
    balanced-s-expr, and bare-word reason forms). Byte oracle UNAFFECTED — all
    243 direct-attempt script hashes still match `scripts.txt` byte-for-byte,
    since the telemetry and options are runner wrapping, not core script bytes.
    Opt-in memory policy: `OATHRS_Z3_MEMORY_MB` (mirrors the `OATHRS_Z3_*`
    convention; reference env is `OATH_PROVE_MEMORY_MB`) prepends
    `(set-option :memory_max_size <MB>)` before the other options — NO default,
    per the §7.2 warning that z3 counts its multi-GB upfront arena RESERVATIONS
    against the bound (a value below the reservation instantly memouts otherwise-
    fine attempts).

    Verified end-to-end: `interval.oath` proves 18/18 with no spurious
    invalidation (z3 returns `unsat`; telemetry parsed but unused for verdicts);
    `OATHRS_Z3_RLIMIT=1000` flips most props to a RECORDED unproven at exit 0
    (exercising `classify_nonverdict`'s `"canceled"`-≥-budget branch end-to-end,
    which requires both info parsers to succeed); `OATHRS_Z3_MEMORY_MB=1` exits 3.

    Spec ambiguities found (flagged for tightening, not blockers):
    - **`memory_max_size` UNIT is not stated.** §7.2 names the z3 option
      (`memory_max_size`) and the reference env's MB suffix (`OATH_PROVE_MEMORY_MB`)
      but never says the runner passes the MB integer to the option verbatim vs.
      converting. I pass it verbatim (z3's `memory_max_size` is megabytes); the
      spec should state the unit rather than leave it inferable only from the env
      name.
    - **The `memout` reason is not cheaply reproducible, and a tiny bound trips
      MISSING-TELEMETRY, not `memout`.** With `OATHRS_Z3_MEMORY_MB=1`, z3 dies
      before emitting any get-info response (OS-level abort under the reservation),
      so the run invalidates via the missing-telemetry clause, not the memout
      clause. This actually CONFIRMS the spec's claim that "the missing-telemetry
      clause catches the OS-death case regardless," but it means the `memout`
      branch is hard to exercise deterministically without a heavy goal whose
      search overshoots a mid-size bound — my `memout` string-equality branch is
      therefore covered by construction/reasoning, not by an end-to-end run.
      Worth a spec note that clean `memout` requires z3 to trip the bound DURING
      search (heavy goal, mid-size bound), not merely a bound below the arena
      reservation, which OS-kills instead.
    - **Empty reason string is unclassified. ADJUDICATED (#29): a blank reason
      INVALIDATES.** z3 emits `(:reason-unknown "")` (observed on trivial `sat`
      results, i.e. verdicts, so never reaching `classify_nonverdict`). Were an
      empty reason ever attached to a genuine non-verdict, the literal reading
      "any non-canceled, non-memout reason ⇒ recordable incompleteness" would
      record it as unproven. Adjudication: the rule's philosophy is POSITIVE
      telemetry, and an empty string is the absence of evidence, not evidence of
      determinism — so a blank reason on a non-verdict INVALIDATES. §7.2 now reads
      "any NON-EMPTY, non-canceled, non-memout reason" and spells out the
      blank-reason rule. Implemented: `classify_nonverdict` invalidates on a
      trimmed-empty reason with its own FATAL wording, before the memout/canceled
      branches.
    - **Runner-wrapper option ORDER is unpinned.** §7.2 pins byte-exact core
      scripts but says nothing about the order of the prepended `set-option`s.
      I prepend `:memory_max_size` before `:rlimit`; since both lie outside the
      hashed bytes and z3 set-options are order-independent, nothing observable
      depends on it — noted only for completeness.

    (NB: the "record nothing, exit(3) immediately on any invalid attempt" model
    described above is REFINED to per-attempt granularity by entry 72 — an
    invalid attempt no longer ends the run by itself. Ambiguities 1/4 and the
    blank-reason adjudication stand unchanged.)

72. **Attempt validity is PER-ATTEMPT, not per-run: an invalid attempt yields NO
    EVIDENCE and taints only the negative case (SPEC §7.2 GRANULARITY; found by
    corpus re-validation, #29).** Entry 71's model — any invalid attempt exits(3)
    immediately — was TOO BLUNT, and cold corpus re-validation surfaced it on
    `t-insert`. On z3 4.16.0, z3 crashes with COMPLETELY EMPTY OUTPUT —
    deterministically — on `t-insert.insert-length`'s DIRECT-attempt script (no
    telemetry ⇒ missing-telemetry ⇒ invalid). But `insert-length` PROVES by
    structural induction; pre-#29 the empty-output crash was silently absorbed as
    `unknown` and induction proved past it. Run-level invalidation (entry 71) made
    `t-insert` PERMANENTLY unprovable on affected z3 builds — a strictly worse
    outcome than the silent-absorption bug it replaced. Refinement (now normative):
    `unsat` (and a quantifier-free `sat` refutation) is positive evidence no
    environment can fake, so a property proven or refuted by ANY valid attempt
    keeps its verdict regardless of other attempts' invalidity; invalidity taints
    only the NEGATIVE case — a property that would record UNPROVEN while any of its
    strategy attempts was invalid has no valid negative verdict, and only THERE
    does the kernel invalidate. Implementation: `run_z3`/`classify_nonverdict` now
    return `Result<Outcome, String>` (`Ok` = valid outcome incl. telemetry-backed
    `Unknown`; `Err` = invalid attempt, reason carried) and NEVER exit — even the
    wall cap and z3 spawn/wait failures return `Err` (the cap is "one instance of
    the general no-evidence rule", per §7.2). The strategy helpers
    (`try_direct`/`try_induction_binder`/`try_induction_lex`/`lex_subgoal`) return
    `Result<bool, String>` (`Ok(true)` proven, `Ok(false)` validly failed, `Err`
    tainted); `prove_prop` tracks the first taint reason, falls through every
    strategy (a later valid strategy can still win), and calls the sole surviving
    `invalidate()` ONLY when the property is unproven AND tainted. Because
    invalidation is a hard stop, this is safe inside the two-level fixpoint: a
    tainted-unproven attempt at any lemma state means the environment is
    non-deterministic for that goal, so the whole run is legitimately void; the
    corpus is designed so tainted attempts are always rescued by a valid proof
    (t-insert's `insert-length`).

    Verified end-to-end on z3 4.16.0: `prove list.oath sort.oath tree.oath`
    completes and `t-insert` lands EXACTLY its fixtured `[F,F,T,F,T]` (2/5) — the
    empty-output crash on `insert-length`'s direct attempt taints, structural
    induction proves it (taint moot), and the three honest unproven props
    (`insert-count-inserted`, `insert-count-others`, `insert-keeps-sorted`) record
    because all their attempts were valid. Byte oracle UNAFFECTED (243/243) — the
    change is run-level control flow, not emitted bytes. `interval.oath` still
    proves 18/18 (exit 0); `OATHRS_Z3_RLIMIT=1000` still records honest unproven
    (exit 0); `OATHRS_Z3_MEMORY_MB=1` now invalidates via GRANULARITY (every
    attempt dies, so every property is tainted-unproven) rather than on the first
    attempt — same exit(3), later trigger point.

    Empirical note (bonus find, recordable under the existing rule, no action):
    z3 also emits `(:reason-unknown "Overflow encountered when expanding vector")`
    on one `t-insert` attempt at ~43M consumed rlimit — an internal z3 exception
    surfacing as a non-empty, non-canceled, non-memout reason, i.e. a deterministic
    incompleteness give-up. It is a pure function of the script, so it correctly
    records as a valid `Unknown` (unproven), not an invalidation.

    Possible ambiguity (flagged): within a single strategy the helpers
    SHORT-CIRCUIT on the first subgoal that is not a valid `unsat` — so a strategy
    with both a valid-fail subgoal and an invalid subgoal is tainted or not
    depending on their (deterministic, constructor-order) order. This only affects
    the final unproven-vs-invalidate decision for a property that fails ALL
    strategies with a mixed strategy, which the current corpus never hits
    (t-insert's tainted `insert-length` is proven, not failed). If the reference
    evaluates all subgoals and ORs the taint instead of short-circuiting, the two
    could disagree on such a goal; worth pinning the taint-collection order in
    §7.2 if any corpus goal ever reaches it.

---

## 64. Lemma-free first attempt: WHAT the "no lemma library" script omits (#53)

**Found by:** blind Rust implementation of the #53 lemma-free-first rule, working
from SPEC §7.2 text alone. **Outcome: no divergence — both kernels resolved it the
same way — but the spec text genuinely underdetermined it, so §7.2 was tightened.**

SPEC §7.2 said the lemma-free attempt uses the goal's "declarations and
defining-equation axioms but no lemma library". That is ambiguous about the
DECLARATION stream: lemma translation has side effects (it can declare sorts and
functions that only a lemma mentions — the `Option_Int` case already recorded in
this log). Two readings:

- **(a) Drop only the lemma `(assert …)` lines**, keep translating lemmas for their
  declaration side effects. The script may then carry ORPHAN declarations for
  symbols no remaining assertion mentions.
- **(b) Shrink the declaration stream too**, to the goal's own footprint.

Both kernels independently chose (a): the Rust kernel keeps `build_lemmas` running
and blanks only its assert block; the Go kernel emits the accumulated `decls`/
`axioms` unconditionally and gates only the lemma emission. So they agree today.

But (b) is a plausible reading a third kernel could take, and it is NOT outcome-safe:
changing which symbols exist changes the problem the solver sees, and under a
budget that can change a verdict — not merely speed. §7.2 now states (a)
normatively and declares (b) non-conformant.

Secondary (resolved by analogy, no divergence): the reduced lemma-free budget is
clamped to the full budget in both kernels — `LEMMA_FREE_Z3_RLIMIT.min(z3_rlimit())`
in Rust, `lemmaFreeRlimit()` in Go — so an rlimit override below the reduced
constant cannot make the optional extra attempt STRONGER than the main search that
follows it. The spec was silent; both kernels reached the same defensive rule the
#50 direct budget already uses. (The Go kernel initially lacked this clamp on the
lemma-free path and gained it from the Rust reading — the second time in two
changes that the blind implementation improved the reference.)

Note: oathrs records no proof METHOD string at all (`prove_prop` returns `bool`;
proven properties are indices), so §7.2's "records the method as direct" is vacuous
on the Rust side. Methods are not part of any fixture, so this is presentational
only and cannot diverge.

## 65. Integer ranking functions (§6.1.1): parameter sorts vs. type variables

**Found by:** blind Rust implementation of the new `measure` termination verdict
(SPEC §6.1.1), working from the spec text and the `range`/`replicate` fixtures.
**Outcome: no fixture divergence** — `range`/`replicate` reproduce `"termination":
"measure"` and their laws prove — **but §6.1.1 leaves one detail to the
implementer.**

§6.1.1 step 1 says: declare each `Int` parameter as an SMT `Int`, and "every
other parameter over a fresh uninterpreted sort so translations mentioning it
stay well-formed." That pins the PARAMETER declarations. It does not say how the
per-site guard/argument translation (which reuses the §7 term→SMT translation)
should resolve a POLYMORPHIC type variable that appears *inside* a translated
term — e.g. a constructor type-argument, or a `let`/`match`/`lam` binder sort —
when the ranking check runs on the uninstantiated (generic) definition, with no
call-site type arguments to substitute.

Two readings:
- **(a)** Give the ranking translation a placeholder type environment (each type
  variable → some concrete sort) so translation stays total, relying on the fact
  that a type variable can never enter an *integer* measure: whatever sort it
  resolves to, the candidate measures are built only from `Int` parameters, and a
  translated term that structurally uses a type-var-typed value can only make a
  site's obligation *harder* to discharge (fail → conservative `unknown`), never
  pass it spuriously.
- **(b)** Refuse to translate anything mentioning a type variable, poisoning such
  sites outright.

Chose **(a)**: the parameters themselves follow the spec literally (Int → `Int`,
non-Int → a fresh `(declare-sort MSort_i 0)` constant), and `tr`'s internal
`apply_tyenv` is fed a placeholder environment mapping every type variable to
`Int` purely to keep translation total. This is sound (it can never yield a false
`measure`: the decrease obligation is pure linear arithmetic over the `Int`
parameter constants, and any type-var-typed subterm is either irrelevant to the
chosen measure or renders the site's obligation `sat`/ill-formed → not `unsat` →
candidate rejected). It is also the *reachable* reading for the fixtures:
`replicate`'s second parameter `x : a` is exactly such a non-Int, type-variable
parameter, and (a) declares it as an unused fresh-sort constant while the measure
`μ = n` is proven over the `Int` counter alone.

Secondary (internal, cannot diverge): the §6.1.1 step-4 obligation scripts are
NOT part of the byte oracle (`prove/scripts.txt` pins only the direct *proof*
scripts), so their exact SMT-LIB formatting — how the guard conjunction is
rendered, a zero-guard site asserting just `(not …)` — is free; only the
`unsat`/`sat` outcome of the decidable LIA query is observable, and that is
solver-deterministic. Callee *defining axioms* are deliberately omitted from the
obligation (only guards + negated decrease are asserted, per step 4); omitting
premises can only make `unsat` harder, so this is conservative and sound.

## 66. Spec strength vs. the `measure` verdict — the spec was silent (§6 / §6.3). RESOLVED.

**Found by:** blind Rust implementation of the `measure` verdict, reconciling the
first cut of the `range`/`replicate` analyses fixtures. **Status: RESOLVED — it was
a reference-side fixture-generation omission, not a semantic rule, and the spec has
since been made explicit.**

### What was observed (first fixture set)

§6.3 (mutation) describes a purely structural catalog gated only by the typecheck
gate; nothing in §6, §6.1, §6.1.1, or §6.3 tied it to the termination verdict. Yet
the FIRST regenerated `fixtures/analyses/range.json` and `replicate.json` carried
**no** `mutants_killed`/`mutants_total` fields, while both functions plainly have
type-preserving mutable operators (`<=`→`<`, `-`→`+`, operand swaps, integer
`±1`/`→0`) that pass the typecheck gate — a faithful §6.3 run yields **9** distinct
killed mutants for `replicate` and **7** for `range` (this kernel computed exactly
those). Every other category kept its mutants — including `unknown`-recursive
functions (`rle-expand` 10, `spin` 1, both reproduced here exactly) — so the ONLY
category missing a mutants field was `measure`. From the fixtures alone the only
consistent reading was "a `measure` verdict suppresses spec strength," and I
implemented that suppression to reach byte-identity.

### The actual cause (confirmed by the coordinator)

Not a rule. The reference's fixture-GENERATION step had simply not run the mutate
pass for the two brand-new `measure` definitions — an incomplete fixture set, not a
semantic decision. The suppression I inferred was therefore a false pattern fit to
a gap in the data.

### Resolution

- The fixtures were regenerated WITH spec strength: `range.json` now carries
  `7`/`7` and `replicate.json` `9`/`9` — exactly the counts this kernel computes.
- SPEC §6 (Spec strength bullet) now states normatively: *"Spec strength is
  computed for every function definition independent of its termination verdict: a
  `measure`-total function is mutated exactly like a `structural` one."*
- The `analyze()` special-case that skipped `mutation_score` when
  `termination == measure` has been REMOVED. Measure-verdict functions are now
  mutated like any other function.

### The finding worth keeping

The spec had been genuinely SILENT on the interaction between the new `measure`
verdict and §6.3, and an incomplete fixture is indistinguishable from an
intentional suppression to a blind implementer — the fixtures were, for a moment,
the only "spec" on this point and they pointed the wrong way. The lesson (now
acted on): a normative statement in §6 beats an inference from present-or-absent
fixture fields. With §6 explicit and the fixtures complete, both kernels agree that
`measure` functions carry the same spec-strength score as any other definition.

## 67. Recursion induction (§7.2): IH treatment of property binders beyond the arity

**Found by:** blind Rust implementation of "Recursion induction" (SPEC §7.2, #56),
which replaced Peano integer induction. **Outcome: no fixture divergence — the two
`measure` corpus witnesses (`replicate.length-is-n`, `range.length-is-span`) each
have exactly `dParams` property binders — but the spec is silent on one case.**

§7.2 says the STEP induction hypothesis `IH_s` is "the property with binder `j`
substituted by `A_s[j]` for every `j < dParams`". When the property has MORE
binders than the function's arity (`prop.binders.len() > dParams`), the text does
not say what happens to the trailing binders `j >= dParams` (which are extra
universally-quantified variables of the property, not function parameters). Two
sound readings:
- **(a)** Universally generalize them in `IH_s` (the hypothesis holds for ALL their
  values at the smaller-measure point) — the standard well-founded-induction shape.
- **(b)** Hold them at the goal's `b{m}` constants (the same values the conclusion
  fixes them to).

Chose **(a)**: `IH_s` is built with `forall_prop`, fixing `j < dParams` to `A_s[j]`
and universally quantifying every remaining binder with a fresh `q{m}`. This is the
sound and conventional reading (a stronger, fully-general hypothesis), and it is
consistent with how this kernel builds every other induction hypothesis
(structural, lexicographic). It is also UNOBSERVABLE for the current corpus: both
`measure` definitions are proved over properties whose binder count equals the
arity, so no trailing binder exists and (a) and (b) coincide. The BASE/STEP
obligation scripts are not part of the byte oracle (only direct-attempt scripts are
pinned in `prove/scripts.txt`), so the choice cannot perturb any fixture; it only
governs which future many-binder `measure` property a third kernel could prove.

**Spec action recommended:** §7.2 should state whether trailing binders
(`j >= dParams`) are universally generalized or pinned in `IH_s`. Until then a third
kernel could reasonably pick (b) and, on a hypothetical many-binder measure law,
diverge.

Secondary (no divergence, recorded for completeness): the recursion-induction walk
reuses the §6.1.1 site collector verbatim, which binds `match`/`let`/`lam` binders
to FRESH constants. For a `measure` function that extracts its counter through a
`match` (none in the corpus), those fresh constants appear free in `G_s`/`A_s`,
which can only make an obligation HARDER to discharge — so such a function is
conservatively left unproven, never wrongly proved. This follows the spec's
explicit instruction to walk "exactly as §6.1.1 collects self-call sites".

## 68. Datatype-field ranking measures (#57): §6.1.1 step 1 was not updated in step

**Found by:** blind Rust implementation of #57 (a counter carried as a FIELD of a
datatype parameter — `rle-expand` recurses on `(Run (- n 1) v)`). **Outcome: no
fixture divergence** (`rle-expand` reproduces `termination: "measure"` and proves
5/5, and exactly three definitions are `measure` corpus-wide) **— but §6.1.1 step 1
still contains two clauses that #57 only patched in steps 2–3, forcing a judgment.**

#57 updated §6.1.1 step 2 (match binds fields to the scrutinee's SELECTORS, adds
the constructor tester as a guard) and step 3 (candidate measures gain each
Int-typed field of a single-constructor datatype parameter, `(selector p_i)`). But
step 1 was left reading:
- "**If there are no Int parameters, the check fails.**" — yet `rle-expand`'s ONLY
  parameter is `r : Run`, a non-Int datatype; a literal reading fails it before the
  new field candidate is ever tried.
- "**every other parameter over a fresh uninterpreted sort**" — yet a field measure
  `(Run_Run_0 r)` and the step-2 selector bindings require `r` to carry its REAL
  datatype sort; over a fresh uninterpreted sort the selectors are ill-formed.

Both are internal contradictions between the (unchanged) step 1 and the (changed)
steps 2–3, not genuine design questions — the feature cannot work under the literal
step-1 text. Choices:
- **Early-out:** build the FULL candidate set first (Int params, differences, then
  single-constructor-datatype Int fields) and fail only when it is EMPTY — i.e.
  "no Int parameter" becomes "no candidate measure". This is exactly what step 3's
  new clause implies.
- **Parameter sorts:** declare Int parameters as `Int`, a TYPE VARIABLE over a
  fresh uninterpreted sort (divergence #65 stands — a type var never enters a
  measure), and every OTHER parameter (datatype, record, …) over its REAL sort via
  `sort_of` (registering the datatype), so selectors and matches stay well-formed.
  This is the minimal deviation from step 1's "fresh uninterpreted sort" needed for
  #57, restricted to the parameters whose structure the measure/selector actually
  reads.

Both choices are sound (a real datatype sort only adds TRUE datatype axioms —
selector-of-constructor reduces `(Run_Run_0 (Run_Run a b))` to `a`, the intended
semantics — so no measure is spuriously accepted) and are confirmed correct: all
113 `analyses/*.json` reproduce byte-for-byte, with exactly `range`/`replicate`
(positional) and `rle-expand` (field) as `measure`, and no other definition flipped.
The §6.1.1 obligation scripts are internal to the termination verdict (not part of
the `prove/scripts.txt` byte oracle), so the sort choice is unobservable beyond the
verdict it produces.

**Spec action recommended:** §6.1.1 step 1 should be updated in lockstep with #57 —
"if there are no candidate measures, the check fails" (not "no Int parameters"), and
"a single-constructor datatype parameter is declared over its real sort so its field
selectors are available; a type variable over a fresh uninterpreted sort".

## 69. Recursion induction CONSTRUCTOR case (#57, §7.2): trailing binders — moot here

Continuation of divergence #67 (positional case). The #57 CONSTRUCTOR case maps ALL
of the property's binders onto the single datatype parameter's fields (the field
sorts must EQUAL the binder sorts, so the counts match exactly). There are therefore
never "trailing" binders in the constructor case, and the ambiguity #67 raised for
the positional case (binders beyond `dParams`) simply does not arise here. IH binder
`j` is substituted by `(selector_j A_s)` of the single recursive argument `A_s`, per
the spec. Recorded only to note the interaction was considered; no new choice. The
STEP/BASE obligation scripts remain outside the byte oracle, so the constructor-case
formatting is unobservable beyond the proof outcome, which reproduces `rle-expand`
5/5 (`length-is-count-arg`, `every-element-is-v` by this case).

## 70. Type-argument inference (#35, §2.1): representation of an OMITTED bracket

**Found by:** blind Rust implementation of bidirectional type-argument inference.
**Outcome: no fixture divergence** — the whole corpus hashes identically and
`inferred.oath` (singleton `7eef2b2b…`, swap `b00e9f7d…`) reproduces its hashes and
proofs — **but the spec does not say HOW a kernel should mark "type arguments
omitted" internally, and the choice interacts with identity.**

The IR's `Ref`/`Ctor`/`SelfRef` carry `tyargs: Vec<Ty>`; the O1 encoding (§1)
hashes that vector, and a backfilled call MUST be byte-identical to the explicit
one. So the "omitted" marker cannot be a new IR field (it would change the
encoding). Choice: represent an omitted bracket as an EMPTY `tyargs` vector, and
have the checker treat "empty `tyargs` on a target whose `tyvars > 0`" as "infer".
This is unambiguous because elaboration rejects a PRESENT bracket of the wrong
count (including an empty `[]` on a `tyvars > 0` target), so an empty vector on a
generic target can only mean the bracket was absent. Elaboration's `parse_tyargs`
therefore returns `Option<Vec<Ty>>` — `None` = absent (defer to the checker),
`Some(v)` = present (count validated) — collapsing to the empty-vector sentinel
only after the count check has run. After inference the vector is non-empty
(== `tyvars`), so re-running the gate on a stored definition is a validating no-op.

Recorded because a third kernel could reasonably reach for an explicit
`Option<Vec<Ty>>` in its IR and, if it were not careful to erase it before
encoding, fork identity. The spec should note that the omitted/explicit
distinction is a SURFACE property that must not survive into the hashed AST.

## 71. Type-argument inference (#35): inference runs during ELABORATION, before hashing

**Found by:** the same work. **Outcome: no divergence, but a pipeline-ordering
decision the spec leaves open.**

§2.1 says inferred arguments are backfilled "into the AST before it is hashed".
In this kernel the hash is computed at the END of elaborating each definition, so
the bidirectional checker (`check_and_backfill`) runs THERE — after the body/props
are built, before the hash — mutating the AST in place. The separate post-
elaboration gate (`check_def`) becomes a validator that runs the same bidirectional
pass on a CLONE and discards the backfill (a no-op on the now-complete AST), so its
`&Def` signature and every caller (the `hash`/`verify` gate, mutation candidates)
are unchanged. Consequence: a type error is now reported during elaboration rather
than by a later gate — observably the same (the definition is rejected and not
stored), and the reject/accept fixtures still pass. A kernel that hashes only after
a global gate pass could backfill there instead; the spec does not mandate where,
only that it happens before hashing.

## 72. Type-argument inference (#35): primitives are bidirectional too (§2.1 silent)

**Found by:** the same work. **Outcome: no divergence** (the corpus's primitive
operands are all explicit) **but §2.1's inference rules enumerate only APPLICATION
and CONSTRUCTOR, not primitives.**

A primitive's operand types are fixed (`Int`/`Bool`/`Str`), or, for `==`, "both
operands the same type" (§2). To let an omitted-argument operand be inferred from
context — e.g. `(== xs (Nil))` or `(< n (foo))` — this kernel CHECKS each primitive
operand against its known operand type rather than synthesizing it, and for `==`
synthesizes whichever operand can be typed and CHECKS the other against it (then
enforces the no-function-type rule). This is sound (every operand is still checked
structurally, and the fixed operand types are exactly §2's rule) and is a strict
superset of synthesize-then-compare, so it accepts every explicit program the old
checker did — verified by the byte-identical hashes of the whole corpus. Related
minor threading choices, also unobservable on the (explicit) corpus: a `let`'s
bound expression is CHECKED against its own annotation, and in SYNTHESIZE mode an
`if`'s second branch is CHECKED against the first branch's synthesized type (the
spec threads the expected type through `if` only in CHECK mode). A third kernel
that left primitives synthesize-only would reject an inferred primitive operand the
author could reasonably expect to work; §2.1 should state that CHECK threads
through primitive operands as well.

## 73. List-literal sugar (#/§1.4) + first PARAMETERLESS function: two latent gaps surfaced

**Found by:** blind Rust implementation of the `(list …)` sugar (§1.4) and its
witness `one-two-three = (list 1 2 3)` — the corpus's first 0-parameter proven
function. **Outcome: no fixture divergence after the fixes** (all 119 hashes,
canonical bytes, verify, analyses, and 305 direct scripts reproduce), **but the
sugar's witness exercised two things the earlier corpus never had, each under-
specified.**

The sugar itself is unambiguous: `(list e0 … en)` → `(Cons e0 (Cons e1 … (Cons en
(Nil))))` with omitted (inferred) type arguments, `(list)` → `(Nil)`, `list` a
reserved head requiring `Nil`/`Cons` in scope. Implemented directly. Because it
desugars to the exact constructor chain, `singleton`'s hash is unchanged and
`one-two-three` matches. Two consequences needed judgment:

- **Bare nullary references.** `one-two-three` is referenced as a BARE name inside
  its own props (`(length [Int] one-two-three)`, `(== one-two-three …)`). §1.4's
  name-resolution bullet ("local variable, the function being defined → `self`,
  constructor, stored function") already covers this, but no prior corpus term used
  a bare self/constructor/function name — elaboration had only ever resolved a bare
  symbol to a local variable. Extended bare-name resolution to the full §1.4 order
  (emitting `self` for the definition under proof, else a constructor/function
  `ref` with omitted type args). SOUND and spec-mandated; simply newly exercised.
  The SMT prover likewise had to learn that a bare reference to a NULLARY function
  is a complete 0-argument call (§7.2: `self` is "translated the same way as a call
  to the definition's hash") — previously every `ref`/`self` reaching the
  translator was in application position, so a bare one was treated as an
  out-of-fragment function value and the goal silently went unproven.

- **Empty confinement in the analyses JSON.** §6.2 gives a 0-parameter function an
  EMPTY confinement verdict. The fixtures (the only contract for the analyses JSON
  layout) OMIT the `"confinement"` field entirely in that case rather than emitting
  `"confinement": []`. The spec text describes the verdict, not the JSON shape, so
  this is a format detail only the fixtures pin. Chose to omit the field when the
  vector is empty, matching `one-two-three.json` byte-for-byte. A third kernel that
  emitted `[]` would diverge on the analyses check; the analyses-output format
  should be documented, or the field always emitted, to remove the ambiguity.

## 74. String-length ranking measure (#32, §6.1.1): obligation bytes are internal

**Found by:** blind Rust implementation of the `(str.len p_i)` termination measure
(the shrinking string in `split`). **Outcome: no fixture divergence** — `split`
reproduces `termination: "measure"`, proves 3/3, and its analyses/mutation
(6/15) and the 325 direct-attempt proof scripts are byte-identical — **but the
coordinator flagged the ranking-obligation SMT bytes as a new-measure-kind risk,
so the choice is recorded.**

§6.1.1 step 3 adds, after the Int/difference/datatype-field candidates, one
candidate per `Str` parameter: `(str.len p_i)`. Step 4's obligation template is
UNIFORM across candidate kinds:
`(assert (and <guards> (not (and (< μ(args) μ(params)) (>= μ(params) 0)))))`.
For the string-length candidate this instantiates to `μ(params) = (str.len p_i)`
and `μ(args) = (str.len <arg_i>)` (the argument being a `str.substr`/`str.indexof`
term). The `(>= (str.len p_i) 0)` conjunct is retained verbatim even though
`str.len` is always non-negative — the spec says it "is discharged trivially",
not that it is omitted, so this kernel emits the uniform template and lets Z3's
sequence theory discharge both conjuncts. The `Str` parameters are declared over
their real `String` sort (as any non-Int, non-type-variable parameter is, since
#57), so `str.len`/`str.substr` are well-formed.

These §6.1.1 obligation scripts are INTERNAL to the termination verdict — they are
NOT part of the `prove/scripts.txt` byte oracle (which pins only direct-attempt
PROOF scripts). So their exact SMT formatting is unobservable across kernels; only
the resulting verdict is (`split` → `measure`, reproduced). The str.substr /
literal-`0` str.indexof bytes that ARE byte-visible live in `split`'s pattern-free
`measure` defining axiom inside the direct proof scripts, and those reproduce
exactly. A third kernel is free to format the ranking obligation differently as
long as it reaches the same verdict; the spec pins the semantics (candidate order,
the decrease-and-non-negativity obligation), not the obligation's byte layout.

## 75. Strings become an ordinary datatype (strings/structural): kernel deletion + a branch-consistency finding

**Found by:** blind implementation of the "strings are no longer primitive" rewrite
on branch `strings/structural`. **Outcome: kernel change is unambiguous and
complete; a corpus/fixture INCONSISTENCY on the branch had to be resolved.**

### Kernel change (unambiguous — pure deletion + a small addition)
Per the rewritten SPEC (§1.1/§1.2/§1.4/§2/§3/§5/§7): removed every string primitive
(`++`, `str-len`, `starts-with`, `ends-with`, `str-contains`, `substring`,
`str-index-of`) from parsing, typing, eval, SMT translation, and mutation; retired
the `Ty::Str` tag `0x03` and `Term::Str` tag `0x13` (the enum variants are gone and
strict decode now REJECTS those tags); deleted the `Str→String` SMT sort mapping and
all sequence-theory translation; deleted the string-length ranking measure (§6.1.1).
Added the STRING-LITERAL SUGAR (`"…"` → an `SCons`/`SNil` codepoint chain, each
`cᵢ` the Unicode scalar value) and let `Str` resolve to the ordinary `Str` datatype
(`e6bbed8b…`). The `str` ENCODING primitive (u32 len + UTF-8) is untouched — it still
carries metadata names. Net effect: the kernel got SMALLER, and the whole `str-*`
library (`str-len/append/prefix/take/drop/split/join/split-join`) proves by ordinary
structural induction — including `str-split-join`'s round-trip, which the sequence
theory could never reach.

### Branch-consistency finding (the ambiguous part)
The branch's AGGREGATE fixtures (`hashes.txt`, `prove/scripts.txt`,
`prove/outcomes.json`) were regenerated to a 122-definition corpus (matching the
coordinator's "96 unchanged + 26 re-forked = 122"), but the working tree still
carried, for 11 definitions NOT in that corpus:
- two straggler EXAMPLE files — `examples/arith.oath` (`fib`, `pow`) and
  `examples/inferred.oath` (`singleton`, `swap`, `one-two-three`), whose defs hash
  fine but appear in NO aggregate fixture; and
- stale PER-DEF fixtures (`canonical/*.bin`, `analyses/*.json`, `verify/*.txt`) for
  those five PLUS six removed old string-primitive defs (`has-prefix`,
  `has-substring`, `has-suffix`, `split`, `str-find`, `str-repeat`).

So `hash examples/*.oath` produced 127 defs against a 122-line `hashes.txt`, and the
per-def `canonical`/`verify` checks failed on 11 orphaned fixtures — a fixture
regeneration that rewrote the aggregates but did not delete the removed defs' per-def
artifacts or the two unrelated example files. This kernel reproduces every one of
the 122 intended defs BYTE-IDENTICALLY (all 122 hashes, canonical bytes, verify
transcripts, and 301 direct scripts match), so the failures were purely orphaned
corpus/fixture state, not a kernel divergence.

**Choice:** aligned the working tree to the authoritative `hashes.txt` (122) — `git
rm` the two straggler example files and delete the 11 orphaned per-def fixtures.
After that, byte-oracle conformance PASSES all checks. The deletions are reversible
(git-tracked) and are flagged here for the coordinator to confirm; the alternative
reading — that `hashes.txt`/`scripts.txt`/`outcomes.json` should instead GAIN the 5
straggler defs — contradicts the stated 122-def corpus, so it was not taken.

## 76. `Int` becomes ℤ — arbitrary precision (int/bignum): two findings

**Found by:** blind implementation of the "Int is ℤ" cutover on branch `int/bignum`.
**Outcome: conformance passes on the full 128-def corpus; one spec-precise choice
confirmed, one fixture-format detail surfaced.**

### The bigint canonical form (spec-precise, confirmed by test)
§1.1 pins the `bigint` wire form and §1.2 makes the `int` term (tag 0x11) use it:
`u8` sign (`0x00` for ≥0, `0x01` for <0) ++ `u32` magnitude byte-length ++ minimal
big-endian magnitude bytes; **zero is sign `0x00`, length 0**. The encoder derives
sign + minimal magnitude from the value (num-bigint's `to_bytes_be`, forcing an
empty magnitude for zero), and the STRICT decoder rejects every non-canonical
form. Directly tested: canonical `+5`/`0`/negative decode; a leading-zero magnitude,
a `0x01` sign with empty magnitude (negative zero), and any sign byte other than
`0x00`/`0x01` are all REJECTED. This is unambiguous in the spec — recorded per the
coordinator's request, not because a judgment call was needed. Implementation note:
this kernel adds a `num-bigint` dependency for ℤ (the Go kernel uses `math/big`);
it uses default features only (no rand/serde) and cross-compiles to wasm unchanged.
num-bigint's `Div`/`Rem` are truncated-toward-zero / dividend-sign — exactly §3's
Quo/Rem — verified against `±7 / ±3`. No SMT change: int literals still render in
decimal (`fmt_int` now over `BigInt`), so scripts are byte-identical in content and
re-fork only through the hash-dependent declaration order the corpus already fixes.

### `mutants_killed` omitted when zero (fixture-format, newly exercised)
The re-forked hashes changed the mutation SEEDS (seeds derive from the definition
hash, §4/§6.3), so five definitions (`greet-or-guest`, `set-inter`, `set-member`,
`shout`, `str-split`) now have a **zero-kill** mutation score (their surviving
mutants are semantically equivalent). The analyses JSON renders such a score as
`mutants_total` ALONE — the `mutants_killed` field is omitted when it is 0 (whereas
`mutants_total: 0` omits BOTH, divergence-#66 style). This kernel had emitted
`"mutants_killed": 0` unconditionally — a latent rendering bug no prior fixture
exercised, because before this branch every mutated definition happened to kill at
least one mutant. The spec describes the score, not the JSON shape; only the
fixtures pin it. Fixed to emit `mutants_killed` only when nonzero, which makes all
128 analyses byte-identical. A third kernel would hit the same surprise the first
time a re-seed produces a zero-kill score; the analyses-output format should be
documented (which mutation fields are omitted at which zero).

## 77. `Rat` primitive: generation draw-order was unspecified — RESOLVED

**Status: RESOLVED.** The gap below was real; it is now closed by a normative
`Rat` bullet added to SPEC §4 (immediately after the `Int` rule): *"draw the
numerator `intIn(-8,8)`, then the denominator `intIn(1,5)` (numerator first), and
reduce to lowest terms."* `gen.rs` was aligned to that exact draw order/ranges
(previously this kernel had inferred a different scheme — Int-boundary numerator +
`intIn(1,9)` denominator — which produced the same observable verdicts but NOT the
same internal draw sequence). Both kernels now generate byte-for-byte identical
`Rat` value streams, not merely identical observable outputs. The entry is kept as
the record of the ambiguity that was found and fixed.

**Found by:** blind implementation of the new `Rat` (ℚ, arbitrary-precision
rationals) primitive from SPEC §1.1–1.5, §2, §3, §7.
**Outcome: conformance passes (oracle mode: checks 1–4 + the 300-script byte
oracle; rat proofs 2/2, 2/2, 1/1 and rat analyses byte-identical when re-derived
in isolation). One genuine spec gap surfaced in §4 — since fixed.**

Everything byte-observable about `Rat` is pinned and reproduced exactly:
- Encoding: `Ty` tag `0x09`, `Term` tag `0x1F` = `bigint` numerator ++ `bigint`
  denominator in reduced form (den ≥ 1, gcd(|num|,den)=1, sign on numerator,
  0 = 0/1). The strict decoder REJECTS a non-positive denominator or a
  non-coprime pair. Validated against `fixtures/encoding/negative_rat.bin`
  (a func Def with body −7/4: `1f 01 00000001 07  00 00000001 04`).
- Lexer: integer tried first, then `big.Rat`-style rational (decimal `3.14`/`0.1`
  or fraction `num/den`, optionally signed), then symbol. The only rat literal in
  the corpus is `0/1`.
- Typing: `+ - * / neg < <=` numeric-overloaded over Int|Rat (synth the first
  operand to fix the kind, then check both against it); `%` Int-only; `< <=`
  return Bool; `/` truncating over Int, exact-real over Rat.
- Dynamic: exact ℚ arithmetic, results re-reduced; printing in lowest terms
  (den = 1 prints as a bare integer, else `num/den`).
- SMT: `Rat → Real`; literal `num/den → (/ NUM DEN)` (negative numerator `(- N)`);
  `/` over Real admitted, `/`,`%` over Int excluded. The three rat direct-attempt
  scripts (`rat-add` 0/1, `rat-mul` 0/1, `rat-recover` 0) are byte-identical to
  `fixtures/prove/scripts.txt`.

### The gap: §4 does not define `Rat` value generation
SPEC §4's "Generation by type — draw order is normative" enumerates `Int`, `Bool`,
`Str`, `Int -> Int`, other function types, `Record`, and `Data`. It says nothing
about `Rat`, yet the three rat properties are verified over **200 generated cases**
and mutation-tested over **60**. So a conforming kernel MUST generate `Rat` values
but has no normative draw order to follow.

**Why it still conforms:** the generated values are unobservable in the fixtures.
`verify/rat-*.txt` records only "passed 200 cases" (all three laws are true over ℚ,
so any generator passes), and `analyses/rat-*.json` records only kill COUNTS, not
values. The only value-sensitive requirement is that `rat-recover`'s if-branch-swap
mutant (`(if (== b 0/1) (* (/ a b) b) a)`) be killed, which needs a case where
`b = 0/1` (any other `b` makes the swapped body return `a`, agreeing with the law).

**Resolution (implemented, now matches §4):** the normative rule is numerator
`intIn(-8,8)` first, then denominator `intIn(1,5)`, reduced. Because the denominator
range starts at 1 and the numerator range includes 0, `0/1` occurs within the 60
mutation cases and `rat-recover`'s if-branch-swap mutant dies — so
`analyses/rat-recover.json` re-derives byte-identically with `mutants_killed: 4`.

*Historical note (the ambiguity that prompted the fix):* before §4 gained this
bullet, its draw-order list enumerated Int/Bool/Str/functions/Record/Data only, so
a conforming kernel had to generate `Rat` values with no normative guidance. The
values are unobservable in the fixtures (verify records only pass counts, analyses
only kill counts), so an inferred scheme reproduced the required COUNTS — but two
independent kernels would silently disagree on the actual draw sequence. That is
precisely the class of divergence N-version testing exists to catch: identical
observable outputs masking non-identical semantics. Pinning the rule in §4 makes
the two kernels' `Rat` generators bit-for-bit identical.

## 78. `Float` primitive (IEEE-754 binary64): three byte surfaces — all now RESOLVED

**Found by:** blind implementation of the new `Float` (IEEE-754 binary64)
primitive from SPEC §1.1–1.5, §2, §3.2, §4, §7.1, plus `docs/floats.md`.
**Outcome: conformance passes fully (oracle mode: checks 1–4 + the 304-script
byte oracle, 135 defs; empirically z3 4.16.0 proves `f-mul-id` 1/1 and
`f-double` 1/1 and falsifies `f-tenths` 0/1 and `f-scale-inv` 0/1; the four
`analyses/f-*.json` re-derive byte-identically from the float prove run).**

Originally three surfaces were under-pinned (byte-observable but not exercised by
any fixture), so an inferred reading could silently diverge from a differently
inferred kernel — exactly the class N-version testing exists to surface. **All
three were reported, then closed in the spec/corpus, and are now byte-verified or
grammar-pinned. Findings below are updated with their resolutions.**

Reproduced exactly (byte-pinned by fixtures):
- Encoding: `Ty` tag `0x0A`, `Term` tag `0x20` = 8 raw big-endian binary64 bytes,
  NaN canonicalized to `0x7FF8000000000000`. The strict decoder REJECTS any other
  NaN bit pattern (like a non-reduced rational); `-0.0`/`+0.0`/`±inf` are distinct
  accepted values. Validated against `fixtures/encoding/negative_float.bin`
  (a func Def with body −2.5 = `0xC004000000000000`).
- Identity/equality: a `Float` value IS its canonicalized 64-bit pattern (held as
  a raw `u64`), so structural `==` is bitwise Leibniz — `NaN == NaN` true,
  `+0.0 == -0.0` false — distinct from the IEEE `fp-eq` primitive.
- Lexer: integer first, then a `Float` literal (an `f`-suffixed token whose prefix
  parses as a binary64) BEFORE rational, then symbol. The corpus literals are
  `0.1f 0.2f 0.3f 1.0f 2.0f`.
- Typing: `+ - * / neg < <=` numeric-overloaded over Int|Rat|Float (synth the
  first operand to fix the kind); `%` Int-only; `fp-eq` Float-only → Bool;
  `< <=` → Bool. `/` over Float is admitted (IEEE division).
- Dynamic: IEEE ops at round-nearest-ties-even, TOTAL (x/0 = ±inf, 0/0 = NaN),
  NaN canonicalized on every result; `< <=` IEEE-ordered (NaN ⇒ false).
- Generation (§4): `below(4)`; on 0 `below(9)` into
  `[+0.0,-0.0,1.0,-1.0,0.5,2.0,+inf,-inf,NaN]`; else numerator `intIn(-8,8)` then
  denominator `intIn(1,4)`, `num/den` as binary64. (§4 DOES pin `Float`, unlike the
  `Rat` gap in #77.)
- SMT (§7.1): `Float → Float64`; literal → `(fp (_ bvSIGN 1) (_ bvEXP 11)
  (_ bvMANT 52))` from the exact canonicalized bits; `+ - * /` → `(fp.add RNE …)`
  etc.; `neg` → `(fp.neg …)`; `< <=` → `(fp.lt …)`/`(fp.leq …)`; structural `==`
  → SMT `=`; `fp-eq` → `(fp.eq …)`. The three float direct-attempt scripts
  (`f-mul-id` 0, `f-double` 0, `f-tenths` 0) are byte-identical to
  `fixtures/prove/scripts.txt`. NOTE — the SMT sort is emitted as Z3's built-in
  abbreviation `Float64`, not the spelled-out `(_ FloatingPoint 11 53)`; §7.1
  gives both as equal and the fixture hashes only pin the choice indirectly (via
  `f-mul-id`/`f-double`, whose `(declare-const b0 Float64)` line the hash covers).
  `Float64` reproduces the hashes; `(_ FloatingPoint 11 53)` does not.

### Finding 1 (#78a) — float PRINTING (§3.2) — RESOLVED (now corpus-verified)
§3.2 pins the finite-float print form to Go's `strconv.FormatFloat(f, 'g', -1,
64)` (shortest round-tripping decimal, fixed-point within a bounded exponent range
else lowercase-`e` scientific), with `f` suffix and specials `inff`/`-inff`/`nanf`.
Rust's native shortest-float formatter is not Go's `'g'` (different
fixed-vs-scientific thresholds, different special spellings), so I implemented
Go's `'g'` algorithm by hand (shortest digits + decimal-point position from Rust's
`{:e}`, then the `exp<-4 || exp>=21` selection).

*Originally* the corpus never printed a `Float` value (the only float
counterexample then, `f-tenths`, is a NULLARY prop with an empty printed-inputs
list), so this hand-rolled `'g'` printer was asserted from the spec but **not
pinned by any fixture**.

**RESOLVED.** The corpus gained `examples/float.oath : f-scale-inv` —
`(== (* (/ x 10.0f) 10.0f) x)` — whose falsified counterexample is a messy
non-round float. `fixtures/verify/f-scale-inv.txt` now pins the printed form, and
this kernel reproduces it **byte-identically**:
`    counterexample: -1.6666666666666667f`. That is a 17-significant-digit
shortest-round-tripping decimal in fixed-point form — precisely the value that
distinguishes a correct Go-`'g'` printer from a naive one — so float printing is
now empirically cross-kernel verified, not merely asserted.

### Finding 2 (#78b) — SMT special-value literal rendering — RESOLVED (doc fixed)
§7.1 (normative) says a float literal renders **uniformly** as `(fp (_ bvS 1)
(_ bvE 11) (_ bvM 52))` from its bits — which I implemented for ALL values,
including NaN/±inf/±0. `docs/floats.md` (a non-normative design note) had said
"literal → `(fp …)` / the appropriate FP constant (canonical NaN →
`(_ NaN 11 53)`)", implying special-value forms that §7.1 does not use. The two
docs disagreed on special values; no corpus float SMT script contains a special
literal (the scripts use only finite normals `0.1f 0.2f 0.3f 1.0f 2.0f 10.0f`), so
the choice was **unobservable** in the fixtures. I followed §7.1 (the authoritative
document) — uniform `(fp …)`.

**RESOLVED.** §7.1 is confirmed authoritative and `docs/floats.md` was corrected to
match — uniform `(fp (_ bvS 1) (_ bvE 11) (_ bvM 52))` for every value, no
`(_ NaN …)`/`(_ ±oo …)` forms. My implementation already matched §7.1; no code
change. Recorded here as the doc-inconsistency-now-fixed.

### Finding 3 (#78c) — float lexing acceptor — RESOLVED (portable grammar pinned)
The original lexer rule was "a token ending in `f` whose prefix parses as a
binary64", pinned to Go's `strconv.ParseFloat`. Go's `ParseFloat` accepts hex
floats (`0x1p-2`) and digit separators (`1_000.0`); a naive host-parser analog
might accept a different exotic set, so a token like `0x1p-2f` could lex as a
`Float` under one kernel and a `symbol` under another — a classification (hence
hash) divergence, unobservable in the then-current corpus.

**RESOLVED.** §1.4 was tightened to a PORTABLE, host-independent grammar:
`[+-]?( D+ (. D*)? | . D+ )([eE][+-]?D+)?` or case-insensitive
`[+-]?(inf|infinity|nan)`; hex floats and underscore separators are explicitly NOT
float literals (they fall through to symbols). I now check this grammar
**explicitly** in `parse_float`/`is_portable_float_syntax` (ASCII digits only, at
most one `.`, `[+-]?D+` exponent), rather than delegating classification to the
host float parser — so the kernel encodes §1.4 directly. Confirmed: Rust's own
`f64` parser already rejects `0x1p4`/`0x1.8p3`/`1_000.0`/`1_0`/`0b101` and accepts
exactly the portable decimal + inf/infinity/nan set, so the explicit gate and the
value parser agree, and both agree with §1.4. Verified classification on
`0x1p4f 1_000.0f 1.2.3f 1ef ff` → symbol; `0.1f 1f 3.14f 1e9f .5f 1.f -2.5f +.5f
1E9f inff nanf` → float.

## 79. Numeric conversions `to-rat`/`to-float`/`floor` (§1.3, §2, §3, §7) — RESOLVED, two under-corpus'd directions read from prose

Three overloaded unary primitives were added (`to-rat` : Int|Float → Rat;
`to-float` : Int|Rat → Float; `floor` : Rat|Float → Int). They are NOT new O1
type tags — they are prim operators like `fp-eq`, so identity/encoding is
unchanged (the prim string goes into the existing 0x18 `prim` node). Typing
(§2), dynamics (§3), and SMT (§7) are all spec-pinned, and the whole corpus is
byte-identical (139 hashes, 126 verify, 308 scripts) with `int-embed`/
`rat-floor`/`embed-add`/`tenth-f` PROVEN 1/1. No genuine ambiguity was found —
the prose fully determines every direction — but two directions are exercised by
the corpus only thinly (or not at all), so I record the reading I implemented in
case a future fixture pins them differently.

### Reading 1 — Rat→Float rounding (§3 "round to nearest binary64, ties to even")
Only `to-float 1/10` is corpus-exercised (`tenth-f` ⇒ `0.1f`). A double-rounding
shortcut (`n.to_f64() / d.to_f64()`) happens to give the right bits for `1/10`
because both operands are small exact integers and a single IEEE divide is
correctly rounded — but it is NOT correctly rounded for general rationals (three
roundings). I implemented a genuine correctly-rounded bignum algorithm instead:
scale `n/d` by a power of two so the floored quotient carries ≥ 54 bits
(significand + guard) with a sticky bit from the remainder, normalize down to
`[2^53, 2^54)`, then round-nearest-ties-even off the guard bit, with carry,
overflow→±inf, and a subnormal re-rounding branch. This is my best reading of the
prose ("nearest binary64, ties to even") applied to ALL rationals, not just the
one the corpus tests. If a future fixture exercises a rational whose two readings
disagree by an ULP, this is the intended behavior.

### Reading 2 — Float→Rat / Float→Int exact value (§3 "exact for a finite float")
No Float-source conversion appears in the corpus (they are partial — a runtime
error on NaN/±inf — and outside the proof fragment, like `/` over `Int`). §3 says
every finite binary64 is a dyadic rational; I decompose the IEEE fields exactly
(implicit leading bit + bias 1075 for normals, `2^−1074` for subnormals, `±0 ↦
0/1`) into `mant · 2^e`, then reduce. `floor` of a Float reuses the same exact
decomposition and floors toward −∞. NaN/±inf (`exp == 0x7FF`) yield the
division-by-zero-style runtime error. Spec-determined, but recorded because it is
entirely un-exercised by fixtures.
