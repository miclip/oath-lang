# Oath development rituals, encoded. `make check` = full re-verification.

# Dependency order matters: later files reference earlier definitions.
# bad_reverse/nontotal/undertested exit nonzero BY DESIGN (falsified /
# unproven exhibits) — the leading dash tolerates them.
# rot_hl/rot_f/rot_h2/rot_h3 are the flywheel-experiment arms (#15): four
# independently-authored green bodies for one oath; `rot` aliases the winner.
# Topological order: list → str (needs List) → records (defines Option/Pair/
# Result, needs str) → everything else. The committed store always has every
# dependency, so this order matters only for a from-scratch rebuild — which is
# exactly why it silently rotted. Two fixes, both verified by putting this list
# into an EMPTY store and getting all 162 definitions with no errors:
#   - extras moved BEFORE the rot* files: they call drop/take, which extras
#     defines. The old order only worked because the committed store already
#     held drop from an earlier run.
#   - rat, float, convert, circle ADDED. They are real corpus members (present
#     in codebase/ and pinned in fixtures/hashes.txt) but were missing from this
#     list, so `make verify` never re-put them. That is how the live registry
#     ended up with no rational family at all — a corpus push driven by this
#     list cannot push what the list omits.
EXAMPLES = list str records arith inferred sort generic merge tree interval queue rle ediv extras rot_hl rot_f \
           rot_h2 rot_h3 rot ints rat convert service leaky stateful cli netcli set map strmap circle http webhook config exclusion optimizer
# float belongs here, not in EXAMPLES: it carries deliberate FALSIFIED exhibits
# (f-tenths — 0.1+0.2 ≠ 0.3 — and f-scale-inv, float scaling not being
# invertible), so `oath put` exits nonzero by design and the `|| exit 1` in the
# EXAMPLES loop would fail the build. It is a dependency leaf, so running it last
# with the other exhibits is safe.
EXHIBITS = undertested nontotal bad_reverse float
# THE APPLICATION (#120). It is a corpus member like any other — `oath fixtures`
# derives fixtures from the STORE, so its definitions are hashed, verified,
# analysed and cross-checked against the Rust kernel exactly as the examples are.
# It must therefore be re-puttable from source: a from-scratch rebuild that
# omitted it would produce a store the committed fixtures no longer describe,
# which is precisely how rat/convert/circle rotted out of this list once before.
# Listed AFTER the examples, whose definitions it depends on.
APPS = apps/github-webhook/webhook.oath apps/github-webhook/hdr-probe.oath \
       apps/github-webhook/report.oath
# THE PROVABLE / TESTED_ONLY LISTS ARE GONE, and their absence is the point.
# They were hand-written enumerations of what `make prove` should attempt, and
# they drifted: 112 of 236 corpus definitions ended up in NEITHER, so nothing
# ever attempted them, and `union == S` (SPEC 7.5) measured the result as 109
# properties that PROVE under re-derivation and were missing from
# outcomes.json (#192). `make prove` and `make mutate` now both DERIVE their
# set from the store, so there is no list left to drift — and a dead list that
# still looks authoritative is worse than none, because the next reader
# believes it.
#
# What the lists knew that the store does not, kept because it is still true:
# some definitions have properties that sit OUTSIDE the provable fragment
# (Int-recursion fuel bounds, `/` and `%` in bodies) and are mutation-scored
# but never proven. That is a fact about those goals, not a set to maintain —
# attempting one costs a budget and records nothing, which is why deriving the
# set is affordable rather than free. `merge` graduated out of that group when
# lexicographic induction landed (#17).

OATH = ./oath/oath
AUTHOR ?= claude-main

.PHONY: build verify prove mutate check fixtures tutorials check-web-tutorials webdocs check-web-docs check-playground-guard check-playground-snapshot check-playground-wasm playground-manifest verify-playground-artifact print-order

build:
	cd oath && go build -o oath .

verify: build
	@for f in $(EXAMPLES); do \
		OATH_AUTHOR=$(AUTHOR) $(OATH) put examples/$$f.oath || exit 1; \
	done
	@for f in $(EXHIBITS); do \
		OATH_AUTHOR=$(AUTHOR) $(OATH) put examples/$$f.oath || true; \
	done
	@for f in $(APPS); do \
		OATH_AUTHOR=$(AUTHOR) $(OATH) put $$f || exit 1; \
	done

# Two passes: pass 2 lets a definition's own pass-1 proofs serve as lemmas
# (reverse-involution depends on its own antidistribution law).
# Single pass: apiProve reaches the SPEC 7.2 self-lemma fixpoint internally
# (with lemma-growth gating, #24), so the historical two-pass ritual is gone.
# THE SET COMES FROM THE STORE, NOT FROM A HAND-WRITTEN LIST — the same repair
# `mutate` already carries, four lines down, and for the same reason it records
# there: "A list kept in sync with content by discipline is a list that
# eventually is not." PROVABLE had drifted exactly as that comment predicts.
# `union == S` (SPEC 7.5) measured the drift: 109 properties across 43
# definitions PROVE under a seeded re-derivation and are absent from
# outcomes.json, because 39 of those definitions are in neither PROVABLE nor
# TESTED_ONLY and were never attempted, and 4 are in PROVABLE but no committed
# run had recorded them (#192). A list is not the authority on what the corpus
# contains; the corpus is.
#
# COST, MEASURED, so nobody discovers it: `scorable` is 236 definitions against
# PROVABLE's 119, and the added half is where the goals that never prove live. A
# from-cold pass over the whole set is ~38 HOURS serially (summed shard time from
# the 177-way campaign, run 34285817138). Subsequent passes are cheap — the
# lemma-growth gate (#24) and the attempt-reuse rule (7.2) skip settled goals —
# but the FIRST pass over a definition is not, and `make check` runs this.
# PROVE_SET overrides the set for a bounded run: `make prove PROVE_SET="a b c"`.
# IT ITERATES TO A FIXPOINT, because a derived set has no dependency ORDER and a
# single pass therefore cannot reach what a seeded run reached. The old PROVABLE
# list carried that order as data — hand-sorted so a definition came after the
# laws it consumes — and deriving the set from the store necessarily throws it
# away (`scorable` is alphabetical). Measured instance: `str-pad-left` sorts
# before its dependency `str-spaces`, proves 4/5 on the first pass, and a
# one-pass target would record that and never retry it; iterating reaches 5/5.
# This is the SAME rule SPEC 7.2 puts on the kernel's own lemma fixpoint, applied
# one level up, including its refusal: a bound reached while the store is still
# moving is NOT a fixpoint and MUST NOT be reported as success.
PROVE_PASSES ?= 8
PROVE_SET ?=
prove: build
	@set -e; \
	names=$$(printf '%s' "$(PROVE_SET)" | tr -s '[:space:]' ' ' | sed 's/^ //; s/ $$//'); \
	if [ -z "$$names" ]; then names=$$($(OATH) scorable) || { \
		echo "prove: 'oath scorable' FAILED — refusing to run, because an empty set" >&2; \
		echo "       would let this target prove NOTHING and still exit 0." >&2; exit 1; }; fi; \
	if [ -z "$$names" ]; then \
		echo "prove: the proof set is EMPTY. A verification target that verifies" >&2; \
		echo "       nothing must not report success." >&2; exit 1; fi; \
	prev=""; \
	for pass in $$(seq 1 $(PROVE_PASSES)); do \
		for n in $$names; do \
			out=$$($(OATH) prove $$n) || { \
				echo "prove: '$(OATH) prove $$n' FAILED — stopping." >&2; \
				echo "       A pipeline through tail/sed would have hidden this, and the" >&2; \
				echo "       next pass would then report CONVERGENCE because nothing moved." >&2; \
				exit 1; }; \
			printf '%s\n' "$$out" | tail -1 | sed "s/^/  $$n: /"; \
		done; \
		now=$$(find codebase/meta -type f -exec shasum -a 256 {} + 2>/dev/null | shasum -a 256); \
		if [ "$$now" = "$$prev" ]; then \
			echo "prove: converged after pass $$pass (the store stopped moving)"; \
			exit 0; \
		fi; \
		prev="$$now"; \
	done; \
	echo "prove: NOT CONVERGED — the store was still moving at the $(PROVE_PASSES)-pass bound." >&2; \
	echo "       A truncated fixpoint must not be reported as success (SPEC 7.2)." >&2; \
	echo "       Raise PROVE_PASSES rather than accepting this state." >&2; \
	exit 1

# Everything with properties gets a spec-strength score — and that is now TRUE
# rather than aspirational: the set comes from `oath scorable`, which reads the
# STORE, not from PROVABLE/TESTED_ONLY. Those lists had drifted badly enough to
# leave 40 definitions unscored, including every definition added for #78, and
# the same rot had already left rat/convert/circle out of `make verify`. A list
# kept in sync with content by discipline is a list that eventually is not.
#
# Known-equivalent survivors (t-member 4/5, i-intersect 5/7, i-hull 12/15: < vs
# <= inside min/max or behind an equality-first check) are honest denominators,
# not spec gaps.
mutate: build
	@for n in $$($(OATH) scorable); do \
		$(OATH) mutate $$n | tail -1 | sed "s/^/  $$n: /"; \
	done

check: verify prove
	@$(OATH) ls

# Freeze the conformance suite (SPEC §10) from the current store. Run after
# `make check` so proof outcomes reflect the latest verdicts. Also refresh the
# website's rendered copy of the proof ledger so it cannot drift from canon
# (contract: website/lib/outcomes.json is a verbatim copy — see #30).
# Re-derive the Ed25519 small-order blocklist (SPEC §8.6.4a) and check it still
# matches what the kernel embeds. The constants are DERIVED from p, d and L and
# self-verified ([8]P = identity, on-curve, base point excluded) — never transcribed,
# because a recalled constant guarding a signature check is an unverified claim.
# The conformance-coverage harness. Built with -tags harness because the mutation
# machinery can disable verification rules, so it must not exist in a shipped binary:
# a production build compiles a ruleOn that returns a constant, leaving no branch to
# switch off and no state to hold one.
.PHONY: conformance-score
conformance-score:
	@cd oath && go build -tags conformance_mutation -o oath-vector-score .
	@./oath/oath-vector-score conformance-score

# RELEASE GATE. The property is behavioural — no invocation of the production binary,
# under any arguments or environment, can disable a normative verification rule — so it
# is checked against the built artifact rather than the source. Run before any release
# or deploy; the mutation machinery is exactly the capability an attacker wants, and
# shipping it inside the artifact whose guarantees it switches off would make the
# measurement tooling the vulnerability.
# Prose carrying numbers is prose making claims. This asserts the documented figures
# against fixtures/prove/outcomes.json and codebase/names.json, so README/DESIGN counts
# cannot drift the way "99 fully proven (299 properties)" did while the real figures
# were 123 and 348/427. A reworded sentence FAILS rather than silently dropping out of
# coverage.
# FIXTURE INTEGRITY. Every committed fixture must have one reproducible producer, one
# unambiguous identity, and this check that the tree is exactly the producers' output.
# Generation goes to a CLEAN temp dir and the COMPLETE tree is compared both ways —
# comparing only known paths finds files that changed but never files the generator
# stopped emitting, which stay committed forever and misrepresent the corpus.
.PHONY: check-fixtures
check-fixtures:
	@python3 scripts/check-fixture-integrity.py

# THE APPLICATION (#120). apps/github-webhook is a real dependent of the kernel:
# it puts definitions, compiles a binary, runs it, and drives it over a socket
# with an INDEPENDENT sender (openssl computes the HMAC, curl speaks HTTP). A
# kernel change that breaks compilation, the capability launch gate, the handler
# adapter or hmac-sha256 fails here rather than in a review.
#
# It runs against a COPY of codebase/ — `oath put` is the only way to check a
# source file and it always appends to the journal, so a gate driven off the
# committed store would dirty the thing it measures on every run.
.PHONY: check-app
check-app: build
	@apps/github-webhook/acceptance.sh

.PHONY: check-doc-numbers
check-doc-numbers:
	@python3 scripts/check-doc-numbers.py

# The spec is the conformance target, and nothing compared it to the BYTES. §8.6.1
# documented a six-line oath-publish/1 envelope for several commits after the kernel
# began emitting seven-line /2 — every other gate passed throughout, because the
# kernel, the fixtures and their agreement were all self-consistent. Only an
# independent implementation reading the prose would have noticed, and one did.
# SPEC §7.5 exclusion-scoped campaign (#98). READ-ONLY and prover-free: it
# validates the exclusion artefact, re-derives the partition, and refuses an
# exclusion whose shard also holds in-scope work. The unit tests carry the PIN
# that makes adding an exclusion a deliberate act.
.PHONY: check-campaign-exclusions
check-campaign-exclusions:
	@python3 scripts/campaign-subset.py check
	@python3 -m unittest discover -s scripts -p 'test_campaign_subset.py' -q

# The same harness END TO END against the real oathrs binary, on a two-file
# corpus. ~17 MINUTES, because it actually proves — which is why it is NOT in the
# per-push gate above and must not be moved there. It needs a release oathrs and
# z3 on PATH, and SKIPS with the reason when either is missing.
.PHONY: check-campaign-integration
check-campaign-integration:
	@python3 scripts/test_campaign_subset_integration.py

.PHONY: check-spec-bytes
check-spec-bytes:
	@python3 scripts/check-spec-vs-fixtures.py

# Materialize an ISOLATED dispatch root for a blind implementation, and verify the
# produced directory rather than the instructions. Round one ran in a worktree with
# an instruction not to inspect history, and two commit subjects still reached the
# agent's terminal through ordinary setup commands. A tree with no .git cannot leak
# history however the setup is worded.
#   make blind-export SHA=<commit> DEST=<dir>
.PHONY: blind-export
blind-export:
	@python3 scripts/blind-export.py $(SHA) $(DEST)

# SPEC §13. A DIFFERENT question from conformance: not "does this implementation
# agree with the vectors" but "could an independent implementer build this from the
# published surface without hidden knowledge". Three blind runs have now passed the
# vectors and independently reported they could not.
.PHONY: check-transformation-table
check-transformation-table:
	@python3 scripts/check-transformation-table.py

.PHONY: check-coaching-leak
check-coaching-leak:
	@python3 scripts/check-coaching-leak.py

.PHONY: check-spec-member-lists
check-spec-member-lists:
	@python3 scripts/check-spec-member-lists.py

.PHONY: check-structural-numbers
check-structural-numbers:
	@python3 scripts/test_check_structural_numbers.py
	@python3 scripts/check-structural-numbers.py

.PHONY: check-implementability
check-implementability:
	@python3 scripts/check-implementability.py

# SPEC §13 IMPL-NORMATIVE-SOURCE. Every literal in every identity encoder must be
# findable in the spec. The kernel once encoded an undeterminable publication as
# `unpublished` — a value in no document — making two published vectors
# irreproducible from the normative text. The word was incidental; the defect was
# the implementation knowing something the specification did not.
# Also asks IMPL-IDENTITY-SUBJECT of every identity encoder: not "are the fields
# bound?" but "identity of WHAT?". §12.4 bound a method and a set of members with
# no subject for months, and every field in it was correct — which is why
# field-level review could not see it.
# Verify a journal SNAPSHOT before/after a registry cutover. Not a CI gate — it
# takes a live snapshot path. See docs/deploy-delta.md.
#   make cutover-check SNAP=<file> [BASE=<file>]
.PHONY: cutover-check
cutover-check:
	@python3 scripts/cutover-check.py $(SNAP) $(BASE)

# #100: every committed metadata record must be exactly what the canonical
# encoder produces, so a no-op update leaves no diff and the store is
# reproducible by the kernel shipping with it. Uses the kernel's own encoder —
# Go escapes HTML in JSON strings and a check written elsewhere agrees by luck.
.PHONY: check-store
check-store:
	@./oath/oath store-check

.PHONY: check-normative-source
check-normative-source:
	@python3 scripts/check-normative-source.py

# SPEC §7.4 pins the BYTES of the #68 bridge obligations so a second kernel can
# reproduce them without reading this one's source. That only means something if
# the reference kernel is held to the document rather than the document being a
# description of the kernel, which is what this compares.
# The read-only subset of what CI runs, DERIVED from the workflow rather than
# remembered. There is no other local command that runs the CI set, so before this
# existed every pre-commit sweep was a sample assembled by hand — enumerated wrongly
# four times in one week, twice reddening main. `--list` shows the plan without
# running anything. It REFUSES on a CI step it has never seen rather than guessing.
# Depends on `build` because several gates invoke `oath/oath` directly, and CI
# gets it via `make verify`'s prerequisite — which this deliberately skips. On a
# clean checkout the binary is absent and those gates would fail for a reason
# that has nothing to do with what they check.
.PHONY: ci-local
ci-local: build
	@python3 scripts/ci-local.py

.PHONY: check-bridge-bytes
check-bridge-bytes:
	@python3 scripts/check-bridge-bytes.py

# The corpus/registry reconciliation RATCHET, exercised against a synthetic tree.
#
# The live check (scripts/check-registry-reconciliation.py --fetch) needs read
# access to the production store and so cannot be an ordinary gate — a check that
# fails for want of credentials gets ignored, which is worse than not having it.
# The LOGIC can be gated, and this is what does it: the decisive case is an
# arrival that exactly cancels a departure, where the total is unchanged and a
# count-based check sees nothing.
# Plugin assets: plugin/ is the single source, oath/plugin_assets.go the compiled
# copy. Same arrangement as the website docs, and gated the same way.
.PHONY: plugin-assets
plugin-assets:
	@python3 scripts/gen-plugin-assets.py

.PHONY: check-plugin-assets
check-plugin-assets:
	@python3 scripts/gen-plugin-assets.py --check

.PHONY: check-stdlib-type-closure
check-stdlib-type-closure:
	@python3 scripts/test-stdlib-type-closure.py

.PHONY: check-reconciliation-ratchet
check-reconciliation-ratchet:
	@python3 scripts/test-reconciliation-ratchet.py

# License evaluation coverage, measured the same way: disable each rule, re-run.
# Rule-to-vector matrix: does every NORMATIVE rule have a witnessing vector? The
# opposite check from license-score, which asks whether disabling an implementation
# rule breaks a vector. Run BEFORE dispatching a blind implementation — without it, an
# unwitnessed rule and an unstated one produce the same symptom.
# Section-parameterised: `make rule-matrix SECTION=8.6`. Not yet a CI gate for
# §8.6 — 22 of its 29 obligations have no vector, which is the honest measured
# state and the next work rather than something to hide behind a passing check.
.PHONY: rule-matrix
rule-matrix:
	@python3 scripts/rule-matrix.py $(or $(SECTION),12)

.PHONY: license-matrix
license-matrix:
	@python3 scripts/rule-matrix.py 12

.PHONY: license-score
license-score:
	@cd oath && go build -tags conformance_mutation -o oath-vector-score .
	@./oath/oath-vector-score license-score

.PHONY: mutation-boundary
mutation-boundary:
	@./scripts/verify-no-mutation-in-release.sh

.PHONY: small-order
small-order:
	@python3 scripts/derive-small-order.py > /tmp/oath-soe.txt || { echo "derivation FAILED its own checks"; exit 1; }
	@grep '^	{0x' /tmp/oath-soe.txt | sed 's/ \/\/ order [0-9]*//' | sort > /tmp/oath-soe-a.txt
	@grep -A 20 '^var smallOrderEncodings' oath/envelope.go | grep '^	{0x' | sed 's/ \/\/ order [0-9]*//' | sort > /tmp/oath-soe-b.txt
	@diff /tmp/oath-soe-a.txt /tmp/oath-soe-b.txt && echo "small-order blocklist reproduces from the derivation"

fixtures: build
	@$(OATH) fixtures fixtures
	@cp fixtures/prove/outcomes.json website/lib/outcomes.json

# Guard: the website's proof ledger must match the canonical fixtures ledger
# byte-for-byte. The site claims its numbers are "read live from the machine's
# own ledger"; this fails CI if that claim ever goes stale (#30).
#
# The byte diff covers the DATA the /corpus page reads. It says nothing about
# the numbers hardcoded into essay and docs prose, which is how #93 shipped an
# essay contradicting the evidence it cited. check-essay-claims.py closes that
# half: derived claims are recomputed from the fixtures, historical and
# captured ones are pinned with provenance. Both must pass.
check-web-ledger:
	@diff -q fixtures/prove/outcomes.json website/lib/outcomes.json >/dev/null \
		&& echo "web ledger in sync ✓" \
		|| { echo "ERROR: website/lib/outcomes.json drifted from fixtures/prove/outcomes.json — run 'make fixtures'"; exit 1; }
	@python3 scripts/test_check_essay_claims.py
	@python3 scripts/check-essay-claims.py

# The website renders the tutorials from docs/tutorial/*.md (the single source),
# copied verbatim into website/content/tutorials/ so the Vercel build (rooted at
# website/) can read them. Regenerate after editing a tutorial; the guard fails
# CI if the committed copies drift.
tutorials:
	@cp docs/tutorial/*.md website/content/tutorials/

webdocs:
	@mkdir -p website/content/docs
	@python3 -c "import sys;sys.path.insert(0,'scripts')" 2>/dev/null || true
	@for f in $$(python3 scripts/webdocs-list.py); do cp "docs/$$f" website/content/docs/; done
	@echo "reference docs copied into website/content/docs/"

# The JS half of ADMIT (#133). Needs no build artifacts, unlike the end-to-end
# witness in website/lib/playground/kernel.test.mjs, which requires
# `make playground-assets` and is therefore a local gate rather than a CI one.
check-playground-guard:
	@node scripts/check-playground-guard.mjs

# BEHAVIOURAL conformance for the served wasm — deliberately NOT a freshness
# check, and the distinction is the whole point (#145). It boots the artifact the
# browser downloads and re-elaborates the corpus through it, catching a
# divergence class nothing else covers: the wasm is a THIRD compilation of the
# kernel, with its own build tags and syscall/js boundary, and oathrs's
# cross-kernel gate says nothing about it. It is GREEN over a stale-but-agreeing
# binary; artifact freshness is tracked separately.
check-playground-wasm:
	@node scripts/check-playground-wasm.mjs

# #148: emit the immutable commit manifest for the built artifact, and verify a
# downloaded one against it. The UPLOAD between them is CI's job and is not
# stubbed here — a stub would look like coverage for a path nothing has run.
playground-manifest:
	@node scripts/playground-artifact.mjs manifest

verify-playground-artifact:
	@node scripts/playground-artifact.mjs verify $(MANIFEST) $(ARTIFACT)

# The served corpus must BE the committed store (#145). It sat 2026-07-23 stale
# for months because `playground-assets` is manual and nothing checked it, and a
# stale corpus still works — so nobody notices. Regenerating the snapshot also
# rebuilds oath.wasm, so this transitively keeps the served kernel fresh
# whenever the corpus moves; a kernel-only change still needs a manual rebuild.
check-playground-snapshot:
	@node scripts/check-playground-snapshot.mjs

check-web-docs:
	@for f in $$(python3 scripts/webdocs-list.py); do \
		diff -q "docs/$$f" "website/content/docs/$$f" >/dev/null || \
		{ echo "ERROR: website/content/docs/$$f drifted from docs/$$f — run 'make webdocs'"; exit 1; }; \
	done
	@python3 scripts/webdocs-list.py --check
	@echo "web reference docs in sync ✓"

check-web-tutorials:
	@for f in docs/tutorial/*.md; do \
		diff -q "$$f" "website/content/tutorials/$$(basename $$f)" >/dev/null || \
		{ echo "ERROR: website/content/tutorials/ drifted from docs/tutorial/ — run 'make tutorials'"; exit 1; }; \
	done
	@echo "web tutorials in sync ✓"

# Playground compute engine (#34): compile the kernel to browser wasm, ship
# Go's loader, and snapshot the committed store — the three derived assets the
# web playground serves. Regenerate after any kernel or corpus change.
#
# `-buildvcs=false -trimpath` is not cosmetic (#148). Without them the shipped
# 15MB public download embedded the BUILDER'S HOME DIRECTORY 54 times and the
# GOROOT path 640, plus a `vcs.modified=true` stamp — a build-provenance claim
# that is unsound by its own admission, since it says the tree was dirty and
# therefore cannot identify the source that produced the binary. Stripping them
# also makes the build BYTE-REPRODUCIBLE, which is the prerequisite for
# delivering this artifact by content digest instead of versioning it in Git.
playground-assets: build
	@mkdir -p website/public/pgrt
	@GOOS=js GOARCH=wasm go -C oath build -buildvcs=false -trimpath -o ../website/public/pgrt/oath.wasm .
	@cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" website/public/pgrt/wasm_exec.js
	@cp website/lib/playground/memfs.js website/public/pgrt/memfs.js
	@cp website/lib/playground/lossless.js website/public/pgrt/lossless.js
	@node website/lib/playground/gen-snapshot.mjs .
	@echo "playground assets assembled in website/public/pgrt/"
# The corpus in dependency order, one source of truth for both `make verify` and
# scripts/push-corpus.sh — so a registry push can never disagree with the local
# put order (that disagreement is how the registry ended up missing rat/convert).
print-order:
	@echo $(EXAMPLES) $(EXHIBITS)
