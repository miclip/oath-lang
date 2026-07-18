# Oath development rituals, encoded. `make check` = full re-verification.

# Dependency order matters: later files reference earlier definitions.
# bad_reverse/nontotal/undertested exit nonzero BY DESIGN (falsified /
# unproven exhibits) — the leading dash tolerates them.
# rot_hl/rot_f/rot_h2/rot_h3 are the flywheel-experiment arms (#15): four
# independently-authored green bodies for one oath; `rot` aliases the winner.
EXAMPLES = list sort generic merge tree interval queue rle ediv rot_hl rot_f rot_h2 \
           rot_h3 rot records extras ints service leaky stateful cli netcli set map
EXHIBITS = undertested nontotal bad_reverse
PROVABLE = length append sum count reverse map filter foldr foldl \
           reverse-onto flatten all any snoc find last init \
           product maximum minimum take-while drop-while count-matching zip zip-with \
           contains is-sorted insert \
           merge t-flatten t-insert t-member t-size \
           i-contains i-overlaps i-intersect i-hull \
           q-to-list q-push q-peek q-drop rle-encode \
           sort count-by list-eq-by min-by max-by insert-by sort-by \
           take drop max2 abs sign clamp or-else shout full-name \
           greet greet-or-guest initials-or \
           map-option flat-map-option is-some is-none \
           map-result map-err unwrap-or \
           kv-get kv-put rename-key safe-get \
           join-with lengths main-echo main-fetch \
           set-add set-union set-inter \
           map-size map-keys map-values map-insert map-lookup map-has map-merge
# Props exist but sit outside the provable fragment (Int-recursion fuel
# bounds, or / and % in bodies): mutation-scored, never proven. merge
# graduated to PROVABLE when lexicographic induction landed (#17).
TESTED_ONLY = rle-expand rle-decode e-mod e-div rot

OATH = ./oath/oath
AUTHOR ?= claude-main

.PHONY: build verify prove mutate check fixtures

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

# Everything with properties gets a spec-strength score. Known-equivalent
# survivors (t-member 4/5, i-intersect 5/7, i-hull 12/15: < vs <= inside
# min/max or behind an equality-first check) are honest denominators, not
# spec gaps.
mutate: build
	@for n in $(PROVABLE) $(TESTED_ONLY); do \
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
