# Oath development rituals, encoded. `make check` = full re-verification.

# Dependency order matters: later files reference earlier definitions.
# bad_reverse/nontotal/undertested exit nonzero BY DESIGN (falsified /
# unproven exhibits) — the leading dash tolerates them.
EXAMPLES = list sort merge tree interval queue rle ediv records extras ints \
           service leaky
EXHIBITS = undertested nontotal bad_reverse
PROVABLE = length append sum count reverse map contains is-sorted insert \
           t-flatten t-insert t-member t-size \
           i-contains i-overlaps i-intersect i-hull \
           q-to-list q-push q-peek q-drop rle-encode \
           sort take drop max2 abs sign clamp or-else shout full-name \
           greet greet-or-guest initials-or
# Props exist but sit outside the provable fragment (Int-recursion fuel
# bounds, or / and % in bodies): mutation-scored, never proven.
TESTED_ONLY = merge rle-expand rle-decode e-mod e-div

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
prove: build
	@for pass in 1 2; do \
		echo "== prove pass $$pass"; \
		for n in $(PROVABLE); do \
			$(OATH) prove $$n | tail -1 | sed "s/^/  $$n: /"; \
		done; \
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
# `make check` so proof outcomes reflect the latest verdicts.
fixtures: build
	@$(OATH) fixtures fixtures
