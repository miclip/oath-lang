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
           rot_h2 rot_h3 rot ints rat convert service leaky stateful cli netcli set map circle http webhook exclusion
# float belongs here, not in EXAMPLES: it carries deliberate FALSIFIED exhibits
# (f-tenths — 0.1+0.2 ≠ 0.3 — and f-scale-inv, float scaling not being
# invertible), so `oath put` exits nonzero by design and the `|| exit 1` in the
# EXAMPLES loop would fail the build. It is a dependency leaf, so running it last
# with the other exhibits is safe.
EXHIBITS = undertested nontotal bad_reverse float
PROVABLE = length append sum count reverse map filter foldr foldl \
           reverse-onto flatten all any snoc find last init \
           product maximum minimum take-while drop-while count-matching zip zip-with \
           contains index-of is-sorted insert \
           merge t-flatten t-insert t-member t-size \
           i-contains i-overlaps i-intersect i-hull \
           q-to-list q-push q-peek q-drop rle-encode \
           sort count-append count-by list-eq-by min-by max-by insert-by sort-by \
           take drop max2 abs sign clamp or-else shout full-name \
           greet greet-or-guest initials-or \
           map-option flat-map-option is-some is-none \
           map-result map-err unwrap-or \
           kv-get kv-put rename-key safe-get \
           join-with lengths main-echo main-fetch \
           set-member set-add set-union set-inter \
           map-size map-keys map-values map-insert map-lookup map-has map-merge \
           str-len str-append str-prefix str-take str-drop str-split str-join str-split-join \
           req-method req-path req-headers req-body req-received-at header-first echo-handler \
           bytes-ok str-bytes hex-nibble hex-decode within-window
# Props exist but sit outside the provable fragment (Int-recursion fuel
# bounds, or / and % in bodies): mutation-scored, never proven. merge
# graduated to PROVABLE when lexicographic induction landed (#17).
TESTED_ONLY = rle-expand rle-decode e-mod e-div rot

OATH = ./oath/oath
AUTHOR ?= claude-main

.PHONY: build verify prove mutate check fixtures tutorials check-web-tutorials print-order

build:
	cd oath && go build -o oath .

verify: build
	@for f in $(EXAMPLES); do \
		OATH_AUTHOR=$(AUTHOR) $(OATH) put examples/$$f.oath || exit 1; \
	done
	@for f in $(EXHIBITS); do \
		OATH_AUTHOR=$(AUTHOR) $(OATH) put examples/$$f.oath || true; \
	done

# Two passes: pass 2 lets a definition's own pass-1 proofs serve as lemmas
# (reverse-involution depends on its own antidistribution law).
# Single pass: apiProve reaches the SPEC 7.2 self-lemma fixpoint internally
# (with lemma-growth gating, #24), so the historical two-pass ritual is gone.
prove: build
	@for n in $(PROVABLE); do \
		$(OATH) prove $$n | tail -1 | sed "s/^/  $$n: /"; \
	done

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
fixtures: build
	@$(OATH) fixtures fixtures
	@cp fixtures/prove/outcomes.json website/lib/outcomes.json

# Guard: the website's proof ledger must match the canonical fixtures ledger
# byte-for-byte. The site claims its numbers are "read live from the machine's
# own ledger"; this fails CI if that claim ever goes stale (#30).
check-web-ledger:
	@diff -q fixtures/prove/outcomes.json website/lib/outcomes.json >/dev/null \
		&& echo "web ledger in sync ✓" \
		|| { echo "ERROR: website/lib/outcomes.json drifted from fixtures/prove/outcomes.json — run 'make fixtures'"; exit 1; }

# The website renders the tutorials from docs/tutorial/*.md (the single source),
# copied verbatim into website/content/tutorials/ so the Vercel build (rooted at
# website/) can read them. Regenerate after editing a tutorial; the guard fails
# CI if the committed copies drift.
tutorials:
	@cp docs/tutorial/*.md website/content/tutorials/

check-web-tutorials:
	@for f in docs/tutorial/*.md; do \
		diff -q "$$f" "website/content/tutorials/$$(basename $$f)" >/dev/null || \
		{ echo "ERROR: website/content/tutorials/ drifted from docs/tutorial/ — run 'make tutorials'"; exit 1; }; \
	done
	@echo "web tutorials in sync ✓"

# Playground compute engine (#34): compile the kernel to browser wasm, ship
# Go's loader, and snapshot the committed store — the three derived assets the
# web playground serves. Regenerate after any kernel or corpus change.
playground-assets: build
	@mkdir -p website/public/pgrt
	@GOOS=js GOARCH=wasm go -C oath build -o ../website/public/pgrt/oath.wasm .
	@cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" website/public/pgrt/wasm_exec.js
	@cp website/lib/playground/memfs.js website/public/pgrt/memfs.js
	@node website/lib/playground/gen-snapshot.mjs .
	@echo "playground assets assembled in website/public/pgrt/"
# The corpus in dependency order, one source of truth for both `make verify` and
# scripts/push-corpus.sh — so a registry push can never disagree with the local
# put order (that disagreement is how the registry ended up missing rat/convert).
print-order:
	@echo $(EXAMPLES) $(EXHIBITS)
