#!/usr/bin/env python3
"""Tests for the §7.5 exclusion-scoped campaign harness (#98).

THE PIN IS THE GUARD, AND HERE IS EXACTLY WHAT IT DOES AND DOES NOT ACHIEVE.
`test_pin_current_exclusions` restates the entire exclusion set as a literal in
this file. Adding an entry to `scripts/campaign-exclusions.json` and
nothing else FAILS this test — demonstrated by
`test_adding_an_entry_to_the_artefact_alone_fails_the_pin`, which does the edit
and asserts the failure rather than asserting that a rule exists.

What that buys: an exclusion cannot be added as a side effect of some other
change, or slipped in with a plausible-looking artefact edit. Someone has to type
the property into an assertion whose only purpose is to make them do so — the
same shape as `oath put --new`, which does not judge a name and only makes
creating one deliberate.

What it does NOT buy, said plainly: it cannot stop a change that edits BOTH. No
mechanical guard can, because "is this exclusion justified?" is judgment. The
pin makes growth VISIBLE and DELIBERATE; it does not make it hard.
"""

import copy
import hashlib
import importlib.util
import json
import os
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
_spec = importlib.util.spec_from_file_location("campaign_subset",
                                               os.path.join(HERE, "campaign-subset.py"))
cs = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cs)


# ---------------------------------------------------------------- the pin ---

# THE PINNED ARTEFACT, TWO WAYS.
#
# PINNED_SHA256 pins the COMPLETE BYTES — every field, including `reason` and
# `return_condition`. A justification that can be rewritten without any check
# noticing is not a justification the repo is keeping; and the reason and the
# return condition are the whole difference between a narrowed claim and a
# quietly weakened one.
#
# PINNED (below) pins the STRUCTURE separately, and is not redundant: a digest
# says only THAT the bytes changed, which makes a legitimate edit and a smuggled
# entry look identical in the failure. The structural pin says WHAT changed, so
# the reviewer reads the diff rather than a hex mismatch.
PINNED_SHA256 = "7316eeea6ca6597377499df7cc987e82371cd0524fdf95851666759e35bb56e1"

PINNED_N = 177
# EMPTY, AND THAT IS A PINNED VALUE LIKE ANY OTHER. The campaign covers the FULL
# corpus; an exclusion cannot be added without editing this list, which is the
# point of pinning it. gh-counts prop 1 was excluded and is not any more: shard
# 140 alone, under the campaign's own per-attempt wall cap, finished in 218
# minutes with the property recorded `unproven` — a verdict matching S, not an
# abort. See the artefact's _doc for the measurement and for the earlier,
# WRONG test that used the default cap.
PINNED = []


def _pin_check(n, exclusions):
    """The pin, factored so the negative control can run it on a mutated artefact."""
    got = [{k: e[k] for k in ("name", "hash", "prop", "prop_name", "shard")} for e in exclusions]
    return n == PINNED_N and got == PINNED


def _artefact_sha256(path=None):
    with open(path or cs.ARTEFACT, "rb") as fh:
        return hashlib.sha256(fh.read()).hexdigest()


class Pin(unittest.TestCase):
    def test_pin_complete_artefact_bytes(self):
        self.assertEqual(
            _artefact_sha256(), PINNED_SHA256,
            "scripts/campaign-exclusions.json changed. If the change is intended — an "
            "exclusion added, removed, or its reason/return-condition revised — update "
            "PINNED_SHA256 (and PINNED, if the structure moved) in the same change, and say "
            "in the commit what the campaign now does or does not cover.")

    def test_pin_current_exclusions(self):
        n, ex, _ = cs.load_exclusions()
        self.assertTrue(
            _pin_check(n, ex),
            "the exclusion artefact does not match the pin in this file.\n"
            "If you are ADDING an exclusion, add it to PINNED above in the same change "
            "and say in the commit why the campaign may stop covering it.\n"
            "If you are REMOVING one because its return condition is met, delete it here too.\n"
            f"artefact n={n} exclusions={json.dumps(ex, indent=2)}",
        )

    def test_adding_an_entry_to_the_artefact_alone_fails_the_pin(self):
        """The guard must BITE, not merely exist."""
        with open(cs.ARTEFACT, encoding="utf-8") as fh:
            doc = json.loads(fh.read())
        before = len(doc["exclusions"])
        extra = copy.deepcopy(SYNTHETIC)
        extra.update(name="append", prop=0, prop_name="length-adds",
                     hash="0" * 64, shard=0, reason="r", return_condition="c")
        doc["exclusions"].append(extra)
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "campaign-exclusions.json")
            with open(p, "w", encoding="utf-8") as fh:
                fh.write(json.dumps(doc))
            n, ex, _ = cs.load_exclusions(p)
            # Inside the temp dir: the byte pin is read from the mutated file.
            self.assertNotEqual(_artefact_sha256(p), PINNED_SHA256,
                                "adding an entry to the artefact alone did NOT fail the byte "
                                "pin — the guard is decorative")
        self.assertEqual(len(ex), before + 1,
                         "the mutated artefact should carry one more entry than the live one")
        self.assertFalse(_pin_check(n, ex),
                         "adding an entry to the artefact alone did NOT fail the structural "
                         "pin — the guard is decorative")

    def test_revising_only_a_reason_fails_the_byte_pin(self):
        """A justification must not be rewritable without a check noticing."""
        with open(cs.ARTEFACT, encoding="utf-8") as fh:
            doc = json.loads(fh.read())
        doc["exclusions"] = [copy.deepcopy(SYNTHETIC)]
        with tempfile.TemporaryDirectory() as d:
            a = os.path.join(d, "a.json")
            with open(a, "w", encoding="utf-8") as fh:
                fh.write(json.dumps(doc))
            first = _artefact_sha256(a)
            doc["exclusions"][0]["reason"] = "because it is slow"
            b = os.path.join(d, "b.json")
            with open(b, "w", encoding="utf-8") as fh:
                fh.write(json.dumps(doc))
            self.assertNotEqual(_artefact_sha256(b), first,
                                "revising ONLY a reason did not change the digest")


# ------------------------------------------------------- the partition rule ---

class Rule(unittest.TestCase):
    def test_rule_reproduces_every_pinned_value(self):
        rows, pinned = cs.load_universe()
        self.assertGreater(cs.verify_rule(rows, pinned), 4000)

    def test_a_broken_rule_is_refused(self):
        rows, pinned = cs.load_universe()
        orig = cs.shard_of
        try:
            cs.shard_of = lambda h, p, n: (orig(h, p, n) + 1) % n
            with self.assertRaises(cs.Bad):
                cs.verify_rule(rows, pinned)
        finally:
            cs.shard_of = orig

    def test_no_shard_is_excluded_and_the_isolation_rule_still_holds(self):
        """With nothing excluded the campaign declines NO shard. The isolation
        RULE is still exercised, against the synthetic entry, because a future
        exclusion depends on it and an empty list must not retire it."""
        rows, _ = cs.load_universe()
        n, ex, _ = cs.load_exclusions()
        shards, _ = cs.resolve(n, ex, rows)
        self.assertEqual(shards, set(), "no exclusions, so no shard may be declined")
        self.assertEqual(len(ex), 0, "the live artefact is expected to be empty")
        shards2, members2 = cs.resolve(n, [copy.deepcopy(SYNTHETIC)], rows)
        self.assertEqual(shards2, {140})
        self.assertEqual([(m[0], m[2]) for m in members2[140]], [("gh-counts", 1)])


# ------------------------------------------------------ artefact validation ---

# A SYNTHETIC ENTRY, SO THE MACHINERY IS TESTED WHETHER OR NOT ANYTHING IS
# EXCLUDED TODAY. These tests validate the VALIDATOR — stale entries, duplicates,
# malformed hashes, missing reasons, shard isolation. Building their fixtures by
# mutating the live artefact's first entry silently disables every one of them
# the moment the exclusion list goes empty, which is exactly when the validator
# most needs to work: the next person to add an exclusion is relying on it. A
# REAL corpus property, so the stale-entry and shard-rule checks compare against
# something true.
SYNTHETIC = {
    "name": "gh-counts",
    "hash": "ae09e70ae58547c85e425a9633f803e24f364356970e7c357820d527fae20fa5",
    "prop": 1,
    "prop_name": "every-group-is-present",
    "shard": 140,
    "reason": "synthetic entry used only by these tests",
    "return_condition": "never — this entry exists only in a temporary fixture",
}


def _artefact(**over):
    with open(cs.ARTEFACT, encoding="utf-8") as fh:
        doc = json.loads(fh.read())
    if not doc["exclusions"]:
        doc["exclusions"] = [copy.deepcopy(SYNTHETIC)]
    doc.update(over)
    return doc


def _write(doc):
    d = tempfile.mkdtemp()
    p = os.path.join(d, "a.json")
    with open(p, "w", encoding="utf-8") as fh:
        fh.write(json.dumps(doc))
    return p


class Validation(unittest.TestCase):
    def test_duplicate_entry_rejected(self):
        doc = _artefact()
        doc["exclusions"].append(copy.deepcopy(doc["exclusions"][0]))
        with self.assertRaises(cs.Bad):
            cs.load_exclusions(_write(doc))

    def test_malformed_hash_rejected(self):
        doc = _artefact()
        doc["exclusions"][0]["hash"] = "NOTAHASH"
        with self.assertRaises(cs.Bad):
            cs.load_exclusions(_write(doc))

    def test_missing_reason_rejected(self):
        doc = _artefact()
        del doc["exclusions"][0]["reason"]
        with self.assertRaises(cs.Bad):
            cs.load_exclusions(_write(doc))

    def test_missing_return_condition_rejected(self):
        doc = _artefact()
        del doc["exclusions"][0]["return_condition"]
        with self.assertRaises(cs.Bad):
            cs.load_exclusions(_write(doc))

    def test_empty_reason_rejected(self):
        doc = _artefact()
        doc["exclusions"][0]["reason"] = "   "
        with self.assertRaises(cs.Bad):
            cs.load_exclusions(_write(doc))

    def test_wrong_format_rejected(self):
        with self.assertRaises(cs.Bad):
            cs.load_exclusions(_write(_artefact(format="something-else/v9")))

    def test_stale_entry_rejected(self):
        rows, _ = cs.load_universe()
        doc = _artefact()
        doc["exclusions"][0]["hash"] = "f" * 64
        doc["exclusions"][0]["shard"] = cs.shard_of("f" * 64, 1, doc["n"])
        n, ex, _ = cs.load_exclusions(_write(doc))
        with self.assertRaises(cs.Bad) as cm:
            cs.resolve(n, ex, rows)
        self.assertIn("STALE", str(cm.exception))

    def test_wrong_name_for_hash_rejected(self):
        rows, _ = cs.load_universe()
        doc = _artefact()
        doc["exclusions"][0]["name"] = "append"
        n, ex, _ = cs.load_exclusions(_write(doc))
        with self.assertRaises(cs.Bad) as cm:
            cs.resolve(n, ex, rows)
        self.assertIn("STALE", str(cm.exception))

    def test_recorded_shard_must_match_the_rule(self):
        rows, _ = cs.load_universe()
        doc = _artefact()
        doc["exclusions"][0]["shard"] = 141
        n, ex, _ = cs.load_exclusions(_write(doc))
        with self.assertRaises(cs.Bad):
            cs.resolve(n, ex, rows)

    def test_excluded_shard_holding_an_in_scope_property_is_refused(self):
        """At n=128 the same property shares its shard — the narrowing is unsound there."""
        rows, _ = cs.load_universe()
        doc = _artefact(n=128)
        doc["exclusions"][0]["shard"] = cs.shard_of(doc["exclusions"][0]["hash"], 1, 128)
        n, ex, _ = cs.load_exclusions(_write(doc))
        with self.assertRaises(cs.Bad) as cm:
            cs.resolve(n, ex, rows)
        self.assertIn("UNSOUND", str(cm.exception))


# ----------------------------------------------------------- envelope shape ---

class Envelope(unittest.TestCase):
    def test_empty_envelope_carries_no_attempt_results(self):
        text = cs.synthesize(140, 177, "abc123")
        lines = [l for l in text.splitlines() if l and not l.startswith("#")]
        self.assertEqual(lines, ["shard\t140\t177", "campaign\tabc123"])

    def test_campaign_id_is_copied_from_a_real_emission(self):
        d = tempfile.mkdtemp()
        p = os.path.join(d, "shard-0.txt")
        with open(p, "w", encoding="utf-8") as fh:
            fh.write(cs.BANNER.format(i=0, n=177) + "shard\t0\t177\ncampaign\tDEADBEEF\n"
                     + "a" * 64 + "\t0\tproven\n")
        self.assertEqual(cs.campaign_of(p), "DEADBEEF")

    def test_a_file_without_a_campaign_line_is_refused(self):
        d = tempfile.mkdtemp()
        p = os.path.join(d, "x.txt")
        with open(p, "w", encoding="utf-8") as fh:
            fh.write("shard\t0\t177\n")
        with self.assertRaises(cs.Bad):
            cs.campaign_of(p)


# ------------------------------------------------------------ adjudication ---

EX = [{"name": "gh-counts",
       "hash": "ae09e70ae58547c85e425a9633f803e24f364356970e7c357820d527fae20fa5",
       "prop": 1, "prop_name": "every-group-is-present"}]

HDR = "FAIL: the union of the shards' attempt results does NOT equal the seed S (%d mismatch(es)):\n"

# The kernel prints an 8-character short hash; the artefact carries all 64. The
# adjudicator matches by prefix AND by name, so both are exercised here.
MISS = HDR % 1 + "  PARTITION: gh-counts (ae09e70a) prop 1 was attempted by NO shard\n"
MISS12 = HDR % 1 + "  PARTITION: gh-counts (ae09e70ae585) prop 1 was attempted by NO shard\n"


class Adjudicate(unittest.TestCase):
    def test_accepts_exactly_the_named_exclusions(self):
        ok, why = cs.adjudicate(1, MISS, EX)
        self.assertTrue(ok, why)

    def test_accepts_a_wider_short_hash_too(self):
        ok, why = cs.adjudicate(1, MISS12, EX)
        self.assertTrue(ok, why)

    def test_rejects_a_named_exclusion_that_was_covered_after_all(self):
        two = EX + [{"name": "append", "hash": "b" * 64, "prop": 0, "prop_name": "x"}]
        ok, why = cs.adjudicate(1, MISS, two)
        self.assertFalse(ok)
        self.assertIn("named but NOT reported", why)

    def test_rejects_a_foreign_mismatch_alongside(self):
        extra = (HDR % 2
                 + "  PARTITION: gh-counts (ae09e70a) prop 1 was attempted by NO shard\n"
                 + "  VERDICT: append (aaaaaaaa) prop 0 is proven in S but unproven here\n")
        ok, why = cs.adjudicate(1, extra, EX)
        self.assertFalse(ok)
        self.assertIn("NOT the named exclusions", why)

    def test_rejects_an_uncovered_property_that_is_not_excluded(self):
        other = HDR % 1 + "  PARTITION: append (aaaaaaaa) prop 0 was attempted by NO shard\n"
        ok, why = cs.adjudicate(1, other, EX)
        self.assertFalse(ok)
        self.assertIn("NOT the named exclusions", why)
        self.assertIn("append", why)

    # ---- mutations that a prefix-only, count-blind or rc-blind check would pass

    def test_rejects_a_different_property_sharing_the_hash_PREFIX(self):
        """Same short hash, same index, DIFFERENT definition name."""
        forged = HDR % 1 + "  PARTITION: gh-counts-v2 (ae09e70a) prop 1 was attempted by NO shard\n"
        ok, why = cs.adjudicate(1, forged, EX)
        self.assertFalse(ok, "a prefix-only match accepted another definition's diagnostic")
        self.assertIn("NOT the named exclusions", why)

    def test_rejects_an_understated_mismatch_count(self):
        """Two diagnostics reported, one declared — a hidden mismatch."""
        understated = (HDR % 1
                       + "  PARTITION: gh-counts (ae09e70a) prop 1 was attempted by NO shard\n"
                       + "  VERDICT: append (aaaaaaaa) prop 0 differs from S\n")
        ok, why = cs.adjudicate(1, understated, EX)
        self.assertFalse(ok)
        self.assertIn("declared 1", why)

    def test_rejects_an_overstated_mismatch_count(self):
        ok, why = cs.adjudicate(1, HDR % 5 + "  PARTITION: gh-counts (ae09e70a) prop 1 "
                                "was attempted by NO shard\n", EX)
        self.assertFalse(ok)
        self.assertIn("declared 5", why)

    def test_rejects_a_failure_with_no_mismatch_count_at_all(self):
        """A campaign-identity rejection or missing shard is NOT a coverage result."""
        ok, why = cs.adjudicate(1, "FAIL: shard emission x ran under campaign A, but this "
                                   "merge's campaign is B\n", EX)
        self.assertFalse(ok)
        self.assertIn("BEFORE the self-check", why)

    def test_rejects_signal_like_return_codes(self):
        for rc in (-9, -6, 2, 101, 137, 139):
            ok, why = cs.adjudicate(rc, MISS, EX)
            self.assertFalse(ok, f"rc={rc} was adjudicated instead of refused")
            self.assertIn("did not reach a verdict", why)

    def test_rejects_a_pass_while_exclusions_are_named(self):
        ok, why = cs.adjudicate(0, "", EX)
        self.assertFalse(ok)
        self.assertIn("stale", why)

    def test_accepts_a_full_corpus_pass_when_no_exclusions(self):
        ok, why = cs.adjudicate(0, "", [])
        self.assertTrue(ok, why)

    def test_subset_identity_is_full_width_and_binds_both_inputs(self):
        a = cs.subset_identity("base", "digest")
        self.assertEqual(len(a), 64, "a truncated identity is a smaller space than what it names")
        self.assertTrue(all(c in "0123456789abcdef" for c in a))
        self.assertNotEqual(a, cs.subset_identity("base2", "digest"))
        self.assertNotEqual(a, cs.subset_identity("base", "digest2"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
