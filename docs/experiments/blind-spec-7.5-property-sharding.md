# §7.5 sharded verification — bringing `oathrs/` onto the property-level assignment rule

Worked from `docs/SPEC.md` §7.5 alone. Nothing outside this directory was read.

`cargo build --release --manifest-path oathrs/Cargo.toml` succeeds;
`cargo test --manifest-path oathrs/Cargo.toml` passes (93 lib + 9 cost + 8 sharding-CLI,
no warnings).

---

## 1. What changed

The kernel assigned by DEFINITION (`first_64_bits(definition_hash) mod n`, no
SHA-256 involved). Everything below follows from the unit becoming a PROPERTY.

**`oathrs/src/prove.rs`**

| before | after |
| --- | --- |
| `shard_of(def_hash, n)` = leading 16 hex digits of the hash, `mod n` | `shard_of_prop(def_hash, prop_index, n)` = `first_64_bits(SHA-256(h ++ "#" ++ decimal(p))) mod n` |
| `shardable_defs()` -> set of definition hashes | `shardable_props()` -> set of `(hash, prop_index)` |
| `ShardOutcome { shard, defs, verdicts }` | `ShardOutcome { shard, verdicts }` — the attempted set IS `verdicts.keys()` |
| `prove_shard_with` tested `assign(hash)` OUTSIDE the property loop | tests `assign(hash, pi)` INSIDE it |
| merge `owners: def -> [shard]`, coverage per definition | `owners: (def, prop) -> [shard]`, coverage per property |
| merge provenance check `shard_of(h, n) == out.shard` | `shard_of_prop(h, pi, n) == out.shard`, per property |
| `campaign_identity(S, hints, solver, rlimit)` | `campaign_identity(S, hints, solver, rlimit, n)`, encoding gains a `partition<TAB><granularity><TAB><n>` line; `campaign_identity_encode` split out so a test can inspect what is bound |
| wire banner `oath-sharded-verification/v1` | `.../v2`; the LINES are unchanged, the version moved because what `shard i of n` MEANS did |

Two smaller consequences:

- `shard_of_prop` needs no malformed-input fallback. The old function parsed hex
  and fell back to `0`; SHA-256 is total over bytes, so a malformed hash now gets
  a defined (meaningless) shard and the merge's canonical-assignment check
  catches it.
- The coverage failure and the "no verdict for this property" failure collapsed
  into one diagnostic. With the unit a property, "this shard dropped a verdict"
  and "no shard attempted this property" are the same fact; reporting both would
  report one defect twice. The `None` arm survives as the structural owner of the
  claim but is unreachable through `merge_and_check`.

**`oathrs/src/main.rs`** — both `campaign_identity` call sites pass `n`; comments
and the merge's rejection message updated. No CLI surface changed.

**`oathrs/src/cost.rs`** — unchanged. Its records were already keyed `(hash,
prop)`, and its claim "in sharded mode each property is attempted exactly once"
is still true (more obviously so).

---

## 2. Tests

### Updated (each said something true of the OLD rule)

| test | what it asserted before | why the replacement is not a weakening |
| --- | --- | --- |
| `assignment_is_deterministic_and_partitions_every_definition` -> `..._every_property` | assignment is deterministic, in range, and every DEFINITION is attempted by exactly one shard | The definition-level claim is now FALSE BY DESIGN — §7.5 says "A definition's properties therefore spread across shards". The property partition is strictly finer: it implies the old claim wherever a definition's properties happen to co-locate, and the old claim is unstatable otherwise. |
| `a_definition_attempted_by_two_shards_is_caught` -> `a_property_attempted_by_two_shards_is_caught` | a definition contributed by two outcomes fails the partition check | §7.5: read per definition this "would be the ordinary case and would fire on every correct run". The new test additionally asserts a CONTROL (an honest, possibly-split campaign passes) and pins the property INDEX in the reported message — strictly more than the original. |
| `a_foreign_verdict_from_an_unowning_outcome_fails_the_merge` | a verdict smuggled into an outcome that does not list the definition in `defs` is rejected | Ownership was decided against the outcome's SELF-REPORTED `defs`; it is now decided against `shard_of_prop`. Strictly stronger: the old check would have admitted this exact verdict whenever the smuggling shard held any property of `quad`. |
| `an_out_of_range_property_index_in_an_owned_outcome_fails` | `quad` prop 5 in the outcome owning `quad` is rejected on range, with the assignment check passing | Same isolation, reached differently: the verdict is inserted into `shard_of_prop(quad, 5, n)`, which is defined even for an index no definition has. Identical strength. |
| `an_outcome_with_a_mislabelled_shard_fails` | a flipped shard label makes its definitions fail the canonical check | Same, per property. Identical strength. |
| `a_missing_verdict_for_an_owned_property_fails_even_when_not_in_s` | required the message to contain the def name and "NOT attempted" | Now requires the def name, the exact property INDEX, and "attempted by NO shard" — and that the defect is reported EXACTLY ONCE. Stronger. |
| `campaign_identity_binds_s_hints_solver_and_rlimit` -> `..._rlimit_and_the_partition` | S / hints / solver / rlimit each move the digest | Every one of those assertions is kept verbatim; `n` and the granularity are added. Purely additive. |
| `alt_shard` (the second, non-normative assignment used to show the union is partition-independent) | read the trailing 16 hex digits of the hash | Now also mixes the property index, so it is a genuine second PROPERTY-level partition. It has to change: a definition-level `alt_shard` could no longer produce the shapes the invariant is about. |

No assertion was dropped without a replacement at least as strong. Nothing was
loosened to make a test pass.

### New

| test | fails if |
| --- | --- |
| `the_assignment_key_matches_an_external_sha256_oracle` | the separator is not `#`; `decimal(p)` is zero-padded or otherwise re-spelled; the digest is taken over decoded hash BYTES instead of the ASCII spelling; `first_64_bits` reads little-endian or reads the trailing 8 bytes; or the pre-change rule (the hash's own leading bytes, no SHA-256) is used. Its expected values were produced by `shasum -a 256` / `openssl dgst` — implementations outside this kernel — and pasted in as constants; nothing in the test recomputes them. |
| `a_definitions_properties_split_across_shards_and_no_sibling_is_attempted` | the selection test is hoisted out of the property loop; or the key ignores the property index, in which case no `n` splits `quad` and the search that makes the test non-vacuous finds nothing. |
| `a_sibling_property_from_the_wrong_shard_fails_the_merge` | the merge's canonical-assignment check is per definition. This is the case the old rule could not see at all: under definition-level assignment, a shard holding `quad/0` claiming `quad/1` is indistinguishable from honest work. |
| `a_definitions_properties_split_across_shards_end_to_end` (CLI) | assignment is per definition (no `n` splits `quad`); or the merge's coverage is per definition (every correct split run is then reported as a double attempt). The existing CLI corpus gives every definition ONE property, so a per-definition and a per-property partition are indistinguishable through it — this test uses a two-property definition, the only shape that can tell them apart end to end. |
| `campaign_identity_binds_..._and_the_partition` | the `partition` line is dropped, or `n` is dropped from it. Granularity cannot be shown by varying it (this kernel has one value), so the ENCODING is inspected directly, and the test also asserts the digest is the hash of exactly those inspected bytes. |

Each was verified by MUTATION, not by reading. Eight mutations were applied to a
green tree, one at a time, and reverted: hoisting the selection test to the
definition level; `#` -> `:`; big-endian -> little-endian; the pre-change
definition-hash key with no SHA-256; the merge's canonical check made
per-definition; `owners` keyed by definition; the `partition` line dropped; `n`
dropped from the `partition` line. Every one was caught, by the test named above
for it.

Note which mutation the partition test does NOT catch: hoisting the selection
test to the definition level leaves every property with an owner count of
exactly one and full coverage — it is just the WRONG shard. Only the split test
and the merge's canonical-assignment check see it. That asymmetry is why the
split test exists as a separate test rather than as an assertion inside the
partition one.

An end-to-end check of the real binary, against an external SHA-256: on a corpus
whose `quad` has two properties, `--shard i/3` put `quad/0` in shard 0, `quad/1`
in shard 1, `twice/0` in shard 1, shard 2 empty, and the merge PASSed.
`shasum -a 256` over the same three keys gives 0, 1, 1.

---

## 3. Where the specification did not determine the answer

### 3.1 GUESS — the shard-index range: `0..n` half-open or inclusive?

> "`n >= 1`, shard index `i` in `0..n`"
> "The merge MUST also require exactly one emission per shard index `0..n`"

Both are written `0..n`, which reads as half-open in some notations (`{0,...,n-1}`,
`n` values) and inclusive in others (`{0,...,n}`, `n+1` values). §7.5 never
disambiguates and never gives a count.

**Chose half-open, `{0,...,n-1}`.** The assignment rule is `... mod n`, whose image
is exactly `{0,...,n-1}`, so under the inclusive reading a merge would demand an
emission for a shard `n` to which no property can ever be assigned — an always-
empty emission whose only function is to exist. That is a strong argument, not a
statement in the text; the text is genuinely two-valued here. A kernel that read
it inclusively would produce `n+1` emissions and this kernel's merge would reject
them as out of range.

### 3.2 INVENTED — the campaign identity's encoding

> "An emitted shard result MUST carry a CAMPAIGN IDENTITY binding the FULL
> determinism context — the proven set `S`, the author hints, the solver version,
> the effective rlimit ..., and THE PARTITION — both the assignment granularity and
> the shard count `n`."

§7.5 says what the identity must BIND and never says how it is computed: no
domain separator, no field order, no serialization, no digest function. It is
therefore a purely internal value here.

**Chose** a versioned line-based encoding hashed with SHA-256:

```
oath-sharded-campaign/v2
solver<TAB>{version}
rlimit<TAB>{rlimit}
partition<TAB>property<TAB>{n}
seed
{hash}<TAB>{pi}          (S, sorted)
hints
{hash}<TAB>{pi}<TAB>{target}:{pi}...   (sorted, each target list sorted)
```

Consequence: **two conformant kernels will not agree on a campaign identity**, so
emissions cannot be merged ACROSS kernels. §7.5 does not appear to intend
cross-kernel merging (it pins no encoding for a shard result either — see 5.1
below), but nothing in the text says so, and a reader who assumed portability
would be wrong.

The header string was deliberately moved away from `oath-campaign/v1`, which the
previous code used: §11.2's canonical mutation-campaign encoding opens with the
literal `oath-campaign/1`, and the two are unrelated concepts. That near-collision
was pre-existing and is now gone.

### 3.3 INVENTED — how to spell "assignment granularity"

§7.5 names the concept and gives no vocabulary for it. **Chose the literal string
`property`** (`PARTITION_GRANULARITY`). Any string would satisfy the requirement
within one kernel; none of them interoperate.

A related judgement: since this kernel has exactly ONE granularity, writing it
into the identity changes no observable behaviour today. It is written anyway,
because §7.5 requires it and because an emission from a definition-sharding build
must not merge with one from this build — which is the whole point of binding a
value that never varies.

### 3.4 INFERENCE (low risk) — what `h` contributes to the digest

> "where `h` is the 64-character lowercase hex hash as it appears in the store,
> `++` is byte concatenation of the US-ASCII spellings"

Read as: the digest is over the 64 ASCII characters of the hex spelling, not over
the 32 decoded bytes. "the US-ASCII spellings" (plural) covers `h`, `"#"` and
`decimal(p)` alike, and "as it appears in the store" points at the hex form. An
alternative reading — that the spelling clause governs only `decimal(p)`, and `h`
enters as decoded bytes — is available if one reads the plural loosely, and would
produce completely different shard assignments. I consider it clearly the weaker
reading and note it only because the two are not distinguishable by testing
against anything supplied here: **no fixture in this tree pins a single shard
assignment.** The vectors in my test are computed by an external SHA-256, which
proves the code implements the reading I chose, and cannot prove the reading is
right.

### 3.5 INFERENCE — falsified definitions are outside the partition

> "Every property of every function definition belongs to exactly one shard. A
> definition with no properties has no proof work and lies outside the partition
> entirely (it can contribute nothing to `S`)."

Taken literally, "every function definition" includes a FALSIFIED one. This
kernel excludes falsified definitions from the partition (inherited from the
pre-change code, kept), on the ground that §7.3 makes a falsified definition
never proved, so its properties can contribute nothing to `S` — the very reason
§7.5 gives for excluding property-free definitions. But §7.5 names only the
property-free exclusion, and a kernel that read the sentence literally would
assign falsified properties to shards, attempt them, and report `unproven`. On a
seed that (correctly) omits them, both readings PASS; they diverge only in what
the coverage condition quantifies over, and in this kernel a shard contributing a
verdict for a falsified definition is a loud PROVENANCE failure. **A second kernel
could disagree here and both could believe they conform.**

### 3.6 INFERENCE — what "ATTEMPTED" means to a merge

> "the run MUST FAIL LOUDLY on any mismatch: a valid verdict differing from `S`,
> a PROPERTY ATTEMPTED by no shard, or a PROPERTY attempted by more than one."

The merge has to decide, per property, HOW MANY shards attempted it — but §7.5
pins no artifact carrying that fact. **Chose: a property was attempted by a shard
iff that shard's emission carries a verdict for it.** This is consistent with the
carry-forward paragraph (an aborted attempt is an attempt, and it does carry a
verdict slot), and it is the only choice available given that every attempt in
this kernel yields one of proven / unproven / aborted. It is also what let me
delete `ShardOutcome::defs`: a separately-carried membership list is a second
account of the same fact that can disagree with the verdicts it describes.

The residual risk is real: a kernel whose attempt can END WITHOUT A VERDICT (not
even an abort) would need a membership list, and §7.5 gives it nowhere to put one.

### 3.7 BEYOND THE SPEC (kept) — the merge recomputes assignment

§7.5 requires a merge to (a) reject a foreign campaign identity, (b) require one
emission per shard index, and (c) self-check coverage and verdicts. It does NOT
require the merge to recompute `shard_of_prop` and check each verdict came from
its canonical shard. This kernel does anyway (pre-existing behaviour, now
per-property). It cannot reject anything a correct campaign produces, and it
closes the sibling-smuggling case in §2's table. Recorded because it is a strength
of this kernel, not of the specification.

### 3.8 CHOICE — bumping the wire-format label to `v2`

§7.5 pins nothing about the shard result's format, so the label is mine either
way. The lines are byte-identical in shape to before; only what "shard i of n"
means changed. Bumped so a human reading a stale file is not misled. The campaign
identity independently rejects a cross-version merge, so nothing depends on it.

---

## 4. What I could not determine at all

1. **Whether a shard result is supposed to be portable between kernels.** §7.5
   requires an emission to CARRY a campaign identity and requires a merge to
   COMPARE identities, while explicitly declining to pin the shard result's
   destination or encoding. So the obligation is stated over an artifact whose
   form is undefined, and there is no way to tell from the text whether merging
   another kernel's emissions is in scope. (See 5.1.)

2. **The cost figures.** "the heaviest DEFINITION is 22.1% of the campaign's
   solver cost... observed runs did sit at 23.5%... The heaviest PROPERTY is 6.9%."
   Nothing in this tree carries per-attempt cost data, so these motivating
   measurements are unverifiable from the supplied inputs. They impose no
   obligation, so this is an observation rather than a gap.

3. **Whether `decimal(p)` can ever be negative.** "no leading zeros or sign"
   implies a sign is conceivable; §7.2's property indices are positional and
   non-negative everywhere I can see, and this kernel's are `usize`. If some
   kernel had a signed index the spelling of `-1` would be undetermined ("no
   sign" would forbid writing it at all).

---

## 5. Internal inconsistencies and tensions

### 5.1 An obligation stated over an artifact the section declines to define

> "An emitted shard result MUST carry a CAMPAIGN IDENTITY binding the FULL
> determinism context..."

against, three paragraphs later:

> "That is stated as a property of the emission rather than as a relation to the
> shard result, because this section pins no destination or encoding for the
> latter — a requirement phrased against an unpinned artifact could not be
> checked."

The second sentence states the principle that the first sentence violates. The
`MUST carry` requirement is phrased against exactly the unpinned artifact, and by
the section's own reasoning it cannot be checked — not by §10 (which does not
mention §7.5 at all), and not by any second implementation. In practice each
kernel invents a format, meets the requirement internally, and no two agree. This
is the sharpest thing I found in §7.5.

The same applies, more weakly, to "The merge MUST also require exactly one
emission per shard index `0..n`": what an "emission" is, and how a merge learns
its index, are undefined.

### 5.2 `0..n` versus `mod n`

Covered in 3.1. Under the inclusive reading, the assignment rule can never
produce shard `n` while the merge requires an emission for it — the two sentences
are consistent only under the half-open reading, which the notation does not
force.

### 5.3 "Every property of every function definition" versus §7.3

Covered in 3.5. §7.5's partition sentence admits no exclusion for falsified
definitions; §7.3 makes their properties unprovable. Both cannot be taken at face
value at once, and §7.5 explicitly carves out only the property-free case — the
reader is left to notice that the falsified case needs the same treatment for the
same reason.

### 5.4 The self-check "is the mode", but only a merge can run it

§7.5 says the self-check IS the mode ("A sharded run that merely completes is not
a pass — the equality is the pass"), and simultaneously that the throughput comes
from running the shards as SEPARATE parallel jobs whose results are merged. A
single shard job therefore cannot self-check, and the section says nothing about
what such a job's completion means or how it should signal that it produced a
contribution rather than a verification. This kernel exits 0 and labels the
emission `CONTRIBUTION ONLY ... NOT verified until merged` in the bytes
themselves. That is a gap rather than a contradiction, but it is the kind of gap
where two kernels will differ in a user-visible way.

### 5.5 A minor one: nothing in §10 references §7.5

§7.5 opens "like §7.4 and `prove/attempts.txt`, this is an OPTIONAL capability,
**pinned once offered**". "Pinned" reads as a cross-kernel commitment, but §10
never mentions sharded verification and no fixture in this tree exercises it —
so the pinning has no witness. Consistent with it being optional; worth flagging
because "pinned once offered" is the phrase §7.4 uses for its manifest, whose
bytes ARE fully specified. §7.5's are not.

---
---

# Part II — against the REVISED §7.5

Everything above is the original report, unchanged. This part was written after
re-reading the revised `docs/SPEC.md` §7.5 and bringing the kernel onto it.

`cargo build --release --manifest-path oathrs/Cargo.toml` succeeds;
`cargo test --manifest-path oathrs/Cargo.toml` passes (94 lib + 9 cost + 8
sharding-CLI, no warnings). The lib count is 93 + 1: one new test, described in
§9.

---

## 6. Did each revision resolve what I raised?

| # | what I raised | resolved? |
| --- | --- | --- |
| 1 | 3.1 / 5.2 — `0..n` is two-valued | **YES**, at the assignment site. One leftover, §7.3 below. |
| 2 | 3.6 — what ATTEMPTED means to a merge | **YES**, and it went further than I did: it made my choice an obligation, closed the residual risk I flagged, and exposed a real defect in my merge. §6.2. |
| 3 | (not raised by me) outcome vs attempt result | Applied; it found genuine conflations in my code. §6.3. |
| 4 | 3.2, 3.3, 4.1, 5.1 — the campaign identity stated over an unpinned artifact | **YES**, completely. §6.4. |
| — | 3.4 — whether `h` enters as ASCII or as decoded bytes | untouched; still an inference. §8. |
| — | 3.5 / 5.3 — falsified definitions in "every function definition" | untouched; still an inference, and two kernels can still differ. §8. |
| — | 5.4 — a single shard cannot self-check, and §7.5 says nothing about what its completion means | untouched; still a gap. |
| — | 5.5 — "pinned once offered" has no witness in §10 | partly addressed, and partly complicated. §7.4. |
| — | 4.2 — the cost figures are unverifiable from this tree | untouched, and still imposes no obligation. |

### 6.1 The shard index bound — resolved

> "`n ≥ 1` and the shard index satisfies `0 ≤ i < n` (half-open, which `mod n`
> can never leave)"

This is exactly the reading I guessed, and it is now derivable in one sentence
rather than argued from `mod n`. The kernel already implemented it, so no
behaviour changed; what changed is the justification. Comments and messages that
said "read HALF-OPEN, since `mod n` produces exactly `{0,…,n-1}`" now cite the
sentence, because an inference recorded next to code outlives the reason it was
needed. The two range messages were reworded to spell the bound (`0 <= i < n`)
rather than write `0..{n}`, which reproduced the ambiguity in the kernel's own
output.

### 6.2 ATTEMPTED — resolved, and it found a defect in my merge

> "A property counts as ATTEMPTED by a shard exactly when that shard's emission
> carries an ATTEMPT RESULT for it — either a valid verdict or an abort … an
> emission MUST NOT carry a separate membership list alongside its attempt
> results"

**My emission matches this as written.** It carries one line per attempt result
(`proven` / `unproven` / `aborted<TAB>reason`) plus two control lines (`shard`,
`campaign`) and a `#` banner. No membership list, and now none is permitted.

**My reasoning for removing the membership list survives, but it is no longer
the operative reason.** I removed it because a second account of the same fact
can disagree with the first; §7.5 now gives the same argument and makes it a
`MUST NOT`. I checked rather than assumed that the residual risk I recorded in
3.6 — "a kernel whose attempt can END WITHOUT A VERDICT (not even an abort)
would need a membership list, and §7.5 gives it nowhere to put one" — is closed:
an ATTEMPT RESULT is a valid verdict or an abort, §7.2's abort covers *any*
environmental invalidating condition, and §7.2's composition of a property
attempt yields exactly proven / unproven / aborted. There is no fourth state, so
there is nothing a membership list would have to carry. (I confirmed the one case
that looked like a fourth: a property whose translation bails and never reaches
the solver. §7.2 composes it as an untainted negative — a valid `unproven` — so
the emission carries a result for it. See §7.2 below for why that case is worth
naming.)

**My coverage check did NOT match as written, and I fixed it.** This is the one
thing in this exercise I would not have found by re-reading my own code, because
it looked exactly like a strengthening. `merge_and_check` validates every
contributed result against three canonical facts, and it used to `continue` past
*all three* failures — including the one that is **beyond §7.5**, the check that
a result came from the shard `shard_of_prop` assigns it to. So a result that
failed only that check was dropped from the ownership count, and coverage was
computed over *canonically-assigned* results rather than over *carried* ones.
Two consequences, both wrong under the definition:

- a property whose only attempt result came from the wrong shard was reported as
  `attempted by NO shard` — a false statement about the emissions, since one of
  them plainly carried a result for it;
- a property its rightful owner attempted **and** another shard also carried a
  result for was reported as attempted by one shard, when "attempted by more than
  one" is precisely the condition §7.5 names.

Neither could turn a FAIL into a PASS — the provenance mismatch fires either way
— so this is a diagnostic defect, not a soundness one. But the merge was saying
something untrue about which shard attempted what, and it was untrue *because*
I had let a check §7.5 does not require decide a predicate §7.5 does define.
Ownership and the verdict union are now decided by what an emission carries, and
by nothing else; the canonical-assignment check still reports loudly and no
longer drops the result. The two checks that DO decide partition membership —
index in range, definition shardable — still drop, because a key failing them
names no property of the partition at all and cannot be an attempt of something
that does not exist.

### 6.3 "Outcome" vs "attempt result" — my code did conflate them

I had not raised this and the revision is right that it was there. `ShardOutcome`
named the thing §7.5 calls an EMISSION, and its `verdicts` field held aborts,
which are not verdicts. Renamed: `ShardOutcome` → `ShardEmission`, its field →
`attempts`, `ParsedShard.outcome` → `.emission`, `merge_and_check`'s parameter →
`emissions`, and — the one that mattered most — `PropVerdict` → `AttemptResult`,
whose three variants are exactly "a valid verdict or an abort". Messages
followed: `contributed a verdict for` → `contributed an attempt result for`, and
`was NOT attempted — no shard produced a verdict for this assigned property` →
`… no emission carries an attempt result for this assigned property`, which was
the message that most directly restated the wrong definition.

`Outcome` (the z3 `unsat`/`sat`/`unknown` enum) is untouched: that IS a valid
verdict, so the word is correct there, and so is `cost.rs`'s use throughout.

Three test names changed with the types. Since §2's table above names the old
ones and I am not editing it, the mapping is:

    a_foreign_verdict_from_an_unowning_outcome_fails_the_merge
      -> a_foreign_result_from_an_unowning_emission_fails_the_merge
    an_out_of_range_property_index_in_an_owned_outcome_fails
      -> an_out_of_range_property_index_in_an_owned_emission_fails
    an_outcome_with_a_mislabelled_shard_fails
      -> an_emission_with_a_mislabelled_shard_fails

No assertion in any of them changed.

### 6.4 Campaign identity as a kernel-local interchange — fully resolved

This dissolves 5.1, which I called the sharpest thing I found, and it dissolves
it the right way: not by pinning an encoding nobody could have derived, but by
narrowing the obligation to what it can bind. 3.2 and 3.3 were filed as INVENTED
— the encoding and the spelling of the granularity — and they are now explicitly
the kernel's to choose. 4.1 ("whether a shard result is supposed to be portable
between kernels") is answered outright: no.

Confirmed rather than assumed, since the report asked: **nothing in the code or
the tests claims cross-kernel interoperability.** I checked every use of
"portable", "cross-kernel", "interoperate" and "two kernels" in the §7.5 and cost
code. The only such claims are in `cost.rs` and `tests/cost_emission.rs`, and
they are about the COST emission, whose framing §7.5 does still make portable
(encoding, line discipline, member names, types) while keeping `strategy` and
`detail` kernel-local — which is what those comments say. The campaign identity
and the shard-result wire format now carry explicit doc comments stating they are
this kernel's own and that §10 compares none of it. `campaign_identity_binds_…`
gained a paragraph saying what it does NOT assert: that any other kernel computes
this value.

One thing I deliberately did NOT change: the granularity is still written into
the identity even though this kernel has exactly one value for it. §7.5 requires
the partition to be bound, and an emission from a definition-sharding build must
not merge with one from this build. That argument is unaffected by the scoping —
it was always about one kernel's own builds.

---

## 7. What the revision introduced, left, or complicated

### 7.1 The self-check cannot detect a violation of the assignment rule, and the new definition makes that sharp

This is not introduced by the revision — it is made *visible* by it, which is
worth more. With ATTEMPTED defined over emissions, the self-check's stated
failure list is exactly: a valid verdict differing from `S`, a property attempted
by no shard, a property attempted by more than one, plus the merge's campaign-
identity and one-emission-per-index conditions. Now consider two shards that
swap their assignments wholesale. Every property is attempted exactly once. Every
verdict equals `S`. Every campaign identity matches. Every index is present
exactly once. **A conformant merge PASSES it**, while "Shard `i` attempts each
property ASSIGNED TO IT" — normative, in the same section — was violated for
every property involved.

My kernel rejects this, and §3.7 above already recorded that as beyond the
specification. I am not asking for it to be required: a merge that recomputes
`shard_of_prop` is cheap here but is a real obligation to impose, and the
assignment rule's actual purpose (reproducibility by an independent runner) does
not depend on the merge policing it. But a reader who takes "The self-check IS
the mode" at face value will believe the mode verifies the partition it
describes, and it verifies a strictly weaker property: that *some* partition of
the universe held.

### 7.2 "The union of all shards' verdicts" — the same conflation, one paragraph earlier

> "The union of all shards' verdicts MUST be compared to `S` property by
> property"

The paragraph immediately below this one introduces ATTEMPT RESULT precisely
because "outcome" is reserved for a valid verdict — and this sentence then calls
the union a union of *verdicts*, when an aborted property contributes an abort,
which is not one. The failure list in the same sentence is precise ("a valid
verdict differing from `S`"), and the carry-forward paragraph handles aborts
correctly, so nothing is unimplementable. But a reader implementing "union of all
shards' verdicts" literally will drop the aborts from the union and then have
nothing for carry-forward to act on. The fix the revision applied to the coverage
condition has not been applied to the sentence one paragraph up.

The same two-level use of "verdict" appears in the cost emission: "a property
attempt that never reaches the solver — §7.2's translation bails — has no budget,
no counter and no verdict to report". That is true at the SOLVER-attempt level
(the `verdict` member) and false at the property level: §7.2 composes such a
property as an untainted negative, a valid `unproven`, and it had better, because
the emission must carry an attempt result for it or coverage fails. Both readings
of "verdict" are live in one section, and only the newly-defined ATTEMPT RESULT
is unambiguous.

### 7.3 `0..n` survives at the site a merge implementer reads

The assignment paragraph now says `0 ≤ i < n` explicitly. The merge requirement
still says:

> "The merge MUST also require exactly one emission per shard index `0..n`"

This is now *derivable* — the section fixes the index range once, and `0..n` here
can only denote that range — so it is no longer the guess it was in 3.1. I record
it because the notation that caused the guess is still present at exactly the
sentence a merge implementer reads, and the disambiguating sentence is a hundred
lines above it under a different heading.

### 7.4 "Pinned once offered" now spans two kinds of obligation

§7.5 opens "OPTIONAL capability, pinned once offered" and applies that phrase
uniformly. After the scoping, the section contains both:

- **cross-kernel normative**: the assignment key (a byte-exact digest an
  independent runner must reproduce), the seed rule, elaboration being global,
  carry-forward, the self-check's conditions, and the cost emission's FRAMING;
- **kernel-local**: the shard result's encoding and destination, the campaign
  identity's encoding and digest, and the spelling of the granularity.

Both are introduced by `MUST`, and which half a given `MUST` belongs to is
settled only by the scoping paragraph in the middle of the section. That is a
real improvement over the previous state (where the two were not distinguished at
all), and it leaves the opening sentence promising one thing about a section that
now does two. My 5.5 observation stands unchanged underneath it: §10 still
references §7.5 nowhere, so neither half has a conformance witness.

### 7.5 Unchanged and still gaps

- **5.4** — the self-check IS the mode, but only a merge can run one, and §7.5
  still says nothing about what a single `--shard` job's exit means. My kernel
  exits 0 and writes `CONTRIBUTION ONLY … NOT verified until merged` into the
  bytes. Two kernels can still differ visibly here.
- **5.3 / 3.5** — "Every property of every function definition belongs to exactly
  one shard" still carves out only property-free definitions. A falsified
  definition's properties can contribute nothing to `S` for the same reason, and
  the sentence still does not say so. My kernel excludes them; a kernel reading
  the sentence literally would include them, attempt them, and report `unproven`.
  Both pass on a correct seed. Unchanged and still a place two kernels can
  disagree while both believing they conform.

---

## 8. Where I still had to infer rather than derive

1. **`h` enters the digest as its 64-character ASCII spelling, not as 32 decoded
   bytes** (3.4). Untouched by the revision. Still the clearly better reading of
   "the 64-character lowercase hex hash as it appears in the store" plus "byte
   concatenation of the US-ASCII spellings", still not decidable from anything in
   this tree, and still the inference with the largest blast radius in the
   section: the alternative reading produces a completely different partition.
   **No fixture here pins a single shard assignment**, so my test vectors
   (computed with an external `shasum`) prove only that the code implements the
   reading I chose.
2. **Falsified definitions are outside the partition** (3.5). Untouched.
3. **Whether a shard may emit an attempt result for a property not assigned to
   it.** New, and a direct consequence of the ATTEMPTED definition: coverage is
   now decided by what an emission carries, and the self-check's failure list
   does not include "carried a result it was not assigned". I treat it as a loud
   failure (beyond §7.5, §7.1 above); a kernel that did not would still conform.
4. **Whether `decimal(p)` can ever be negative** (4.3). Untouched.

---

## 9. What changed in the implementation

Behaviour:

- `merge_and_check` computes ownership and the verdict union from **every attempt
  result an emission carries** for an in-partition property. The canonical-
  assignment check (beyond §7.5) reports and no longer drops. §6.2.
- Two shard-index messages spell the bound instead of writing `0..{n}`.
- Nothing else. The wire-format bytes, the campaign identity's bytes, the
  assignment key, the CLI surface and every exit code are unchanged.

Terminology (§6.3): `ShardEmission`, `.attempts`, `AttemptResult`,
`ParsedShard.emission`, `emissions`, and the messages and comments that stated
the wrong definition. Doc comments now cite the revised text where they used to
record an inference (the half-open range, the membership-list prohibition, the
kernel-local scoping).

### The new test, and its mutation

`coverage_counts_every_carried_attempt_result_including_a_misassigned_one` builds
an honest split campaign (asserted PASSING as a control), then:

- **(a)** MOVES `quad/1` out of its canonical shard into another — exactly one
  emission carries a result, and it is the wrong one. Asserts the misassignment
  is reported AND that the merge does **not** say `attempted by NO shard`.
- **(b)** DUPLICATES `quad/1` into a second shard, leaving the owner's own result
  in place. Asserts it is reported as `attempted by 2 shards`.

**The mutation that makes it fail is the exact code I removed**: putting the
`continue` back after the canonical-assignment check, so coverage is computed
over canonically-assigned results again. Applied to a green tree, (a) fails with
`PARTITION: quad prop 1 was attempted by NO shard` present in the mismatches.
Disabling (a)'s assertion and re-running the same mutation, (b) fails
independently, reporting one owner instead of two. Both halves catch it alone;
both were verified by applying and reverting, not by reading.

Neither campaign PASSES under either version — the point of the test is that the
merge says something TRUE about which shard attempted what, not that it fails.
That is why the test asserts on message content and why the control assertion is
there: without the control, a mutation that broke the honest path would satisfy
both failure assertions vacuously.

Two of the eight mutations from §2 were re-applied after the rename, to check the
rename did not quietly weaken anything: the assignment key's `#` → `:` (caught by
`the_assignment_key_matches_an_external_sha256_oracle` alone), and disabling the
canonical-assignment check entirely (caught by four tests, including the new
one). No test was weakened, and no assertion was dropped.
