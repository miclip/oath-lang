# Ledger app friction log

The second "depend on Oath, don't improve it" exercise (after
`webhook-friction.md`, #120). `docs/experiments/ledger-app/` is a double-entry ledger checker:
read a file of `account amount` postings, accumulate per-account balances in
exact ℤ minor units, print the balances, and REFUSE (a failing exit) any ledger
whose postings do not net to zero. It was built to surface the next demand list,
which is below. It compiles on BOTH backends and they agree byte-for-byte —
balanced → exit 0 + report, imbalanced → `net = 100` on stderr + exit 1, no args
→ usage + exit 2.

**The friction is the deliverable, not the app.** Ranked by how much a real
consumer would feel it.

## Demands (ranked)

1. **A native string-keyed container, or a way to recover a `str-code`d key.**
   The obvious data structure for a ledger is `account → balance`, and the
   native `Set`/`Map` (the #180 persistent trees) are exactly the right
   performance shape — but they are keyed on `Int`, and the app keys accounts
   through `str-code` (an injective `Str → Int`). `str-code` has no inverse
   available to a program, so a `Map` could accumulate the balances but could
   never carry the account NAME back out to the report. The app is therefore
   written over an association list `(List (Pair Str Int))` — O(N) per lookup,
   the exact O(N²) build the native containers exist to avoid. This is the same
   wall `webhook-friction.md` hit from the other side, now with a concrete cost:
   **the native containers are unreachable for the single most common shape,
   a string-keyed aggregate that must be reported.** DEMAND: a native
   `Str`-keyed map, or a `str-decode` inverse so a program can key on `Int` and
   still recover the string. **Filed as #184**, with a structural model for the
   `Str`-keyed map so it clears the language-capability guard.

2. **`parse-nat` has no signed sibling.** Money is signed; `parse-nat` is
   `Str → Int` over non-negative values only, so `parse-amount` peels a leading
   `-` by hand (`match ((SCons 45 rest) …)`). Every numeric-input program will
   rewrite this. DEMAND: `parse-int : Str → Option Int` in the standard library
   (and an `Option` result, since `parse-nat` on non-numeric input has no
   defined failure today either).

3. **A simple conservation lemma does not auto-prove — so "PROVEN balanced" is
   aspirational for this shape.** The payoff property is
   `sum(balances ps) == total(ps)` — nothing created or lost in the fold — and
   its one-step lemma `sum(bal-add a n bs) == n + sum(bs)`. Both are ordinary
   structural inductions, but z3 burned the full budget (>400 s, no verdict) and
   they stay `tested`, not `proven`. The cause is the `str-code` guard in
   `bal-add`: the solver churns on the recursive function in the `if` condition
   even though BOTH branches change the sum by exactly `n` regardless of it.
   DEMAND: the induction heuristic should discharge an assoc-list fold whose only
   obstacle is a recursive function appearing in a guard (the guard's VALUE is
   irrelevant to the goal). This is also a clean example for `scripts/prove-
   reasons.py` (#68) to classify — `budget-exhausted`, not `needs-a-lemma`.

4. **No forward references within a file.** `put` processes definitions
   top-to-bottom and fails on the first forward reference (`bal-add` used
   `sum-amounts`, defined below it: `error: unknown name "sum-amounts"`), so a
   file must be hand-ordered by dependency. For a small app this is a minor
   reshuffle; for a large one it is real bookkeeping. DEMAND: resolve a file's
   definitions in dependency order within a single `put`, or at least name the
   ordering fix in the error.

## What worked, and is worth keeping

These are not friction — they are the parts the exercise confirms are load-
bearing and correct, recorded so a later change does not quietly regress them.

- **The failing-exit protocol (#120) is exactly right for this domain and works
  identically on both backends.** A ledger that does not balance is the canonical
  "computed a refusal, must REPORT it to the caller" case, and `Emit`/`Refuse`
  carried it cleanly to a real exit code.
- **Exact ℤ arithmetic is the whole reason to write this in Oath.** No overflow,
  no float rounding — an imbalance cannot hide in the low bits. This is a real
  "why Oath" story that the webhook did not exercise.
- **Capability confinement worked with no effort.** `ledger-main`'s `readfile`
  capability was analysed `confined` automatically; the program cannot read a
  file it was not handed the capability for.
- **Constructor renaming did not change identity.** Renaming `Res`/`Ok`/`Fail`
  to `CliResult`/`Emit`/`Refuse` (to avoid permanently binding a second `Ok`
  beside the corpus's `Result.Ok`) left every hash unchanged — names are
  metadata, identity is the canonical AST, exactly as promised.
- **The exit protocol is recognised STRUCTURALLY, by field count, not by
  constructor name** — which is what made the rename free, and is the right
  design: it does not force a naming convention onto every program.
- **Unknown CLI flags are refused, not ignored.** `oath build -o` is the flag;
  a mistaken `--out` was rejected with a message naming the known flags rather
  than silently doing the wrong thing. Fail-closed, as the rest of the system is.

## The one-line summary

The language and the exit/capability/identity machinery held up on a second, very
different app. The sharpest missing piece is the same one the webhook implied and
this app makes concrete: **the native containers are Int-keyed, and the most
common real aggregate is string-keyed with a name to report — so today that
aggregate cannot use them.** That is the demand to weigh first.
