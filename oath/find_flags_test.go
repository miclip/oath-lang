package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for `oath find`'s argument grammar, and for `--details` selecting the
// detailed proof-implication report (#156).
//
// The grammar is tested through parseFindArgs rather than through a process,
// because a refusal that is only observable as a non-zero exit tells you a
// command line was rejected and nothing about WHICH rule rejected it — and the
// rules here differ in what they protect: one stops an ambiguous query, one
// stops a filename being read from a flag, one stops a switch being given a
// value it would ignore.

// TestParseFindArgsAcceptsEveryForm is the "does it still RUN" half, and it is
// the reason `find` was allowed into knownFlags at all: the serve outage came
// from a table that was checked while the invocations were not.
//
// WHAT MUTATION MAKES IT FAIL: dropping any selector from findSelectors, which
// is exactly the edit that would leave a working invocation refused.
func TestParseFindArgsAcceptsEveryForm(t *testing.T) {
	for _, c := range []struct {
		args   []string
		form   findForm
		target string
		mode   findImpliesMode
	}{
		{[]string{"rat-add"}, findFormName, "rat-add", findImpliesSummary},
		{[]string{"--spec", "q.oath"}, findFormSpec, "q.oath", findImpliesSummary},
		{[]string{"--implies", "q.oath"}, findFormImplies, "q.oath", findImpliesSummary},
		{[]string{"--equiv", "sum-ab"}, findFormEquiv, "sum-ab", findImpliesSummary},
		{[]string{"--implies", "q.oath", "--details"}, findFormImplies, "q.oath", findImpliesDetailed},
		// Order must not matter: the flag is a switch, not a suffix.
		{[]string{"--details", "--implies", "q.oath"}, findFormImplies, "q.oath", findImpliesDetailed},
	} {
		got, err := parseFindArgs(c.args)
		if err != nil {
			t.Errorf("oath find %s: %v", strings.Join(c.args, " "), err)
			continue
		}
		if got.form != c.form || got.target != c.target {
			t.Errorf("oath find %s → form %d target %q, want form %d target %q",
				strings.Join(c.args, " "), got.form, got.target, c.form, c.target)
		}
		if got.mode != c.mode {
			t.Errorf("oath find %s → mode %d, want %d", strings.Join(c.args, " "), got.mode, c.mode)
		}
	}
}

// TestParseFindArgsSummaryIsTheDefault is stated separately from the table above
// because it is the DEFAULT that must not drift, and a table entry asserting
// findImpliesSummary reads like one more case rather than like a policy.
//
// WHAT MUTATION MAKES IT FAIL: initialising findArgs.mode to findImpliesDetailed,
// which every other assertion in this file survives.
func TestParseFindArgsSummaryIsTheDefault(t *testing.T) {
	got, err := parseFindArgs([]string{"--implies", "q.oath"})
	if err != nil {
		t.Fatal(err)
	}
	if got.mode != findImpliesSummary {
		t.Errorf("--implies without --details selected mode %d; the default must be the summary (%d), "+
			"because the answer is what PROVED and a large corpus buries it under the misses",
			got.mode, findImpliesSummary)
	}
}

// TestParseFindArgsRefusals pins each refusal to its own reason. Asserting only
// "an error was returned" would pass for a parser that rejected every one of
// these for a single wrong reason — and three of them are cases where the
// tempting alternative is to accept and quietly do the wrong thing.
func TestParseFindArgsRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string // a phrase the refusal must contain, naming the actual rule
	}{
		{"no arguments", []string{}, "usage"},
		{"two selectors", []string{"--spec", "a.oath", "--implies", "b.oath"}, "ONE query at a time"},
		{"selector with no value", []string{"--implies"}, "needs a value"},
		// The reason this is refused rather than accepted: `--details` would
		// otherwise be READ AS THE FILENAME.
		{"selector followed by a flag", []string{"--implies", "--details"}, "is a flag"},
		{"details twice", []string{"--implies", "q.oath", "--details", "--details"}, "twice"},
		// --details is boolean, so a value beside it is a stray positional. Absorbing
		// it would let `--details false` turn detail ON.
		{"details given a value", []string{"--implies", "q.oath", "--details", "false"}, "takes no value"},
		{"details with --spec", []string{"--spec", "q.oath", "--details"}, "--implies form only"},
		{"details with --equiv", []string{"--equiv", "sum-ab", "--details"}, "--implies form only"},
		{"details with a name lookup", []string{"rat-add", "--details"}, "--implies form only"},
		{"two names", []string{"rat-add", "rat-mul"}, "usage"},
		{"an invented flag", []string{"--implies", "q.oath", "--verbose"}, "has no flag"},
	} {
		got, err := parseFindArgs(c.args)
		if err == nil {
			t.Errorf("%s: `oath find %s` was ACCEPTED as form %d target %q, mode %d",
				c.name, strings.Join(c.args, " "), got.form, got.target, got.mode)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal does not name its rule (want a mention of %q):\n%s", c.name, c.want, err)
		}
	}
}

// TestFindFlagTableIsDerivedFromTheParser is the mechanical form of the
// obligation TestUncataloguedCommandsAreNotFlagChecked states in prose: a
// catalogued command's table must list every flag it parses, and nothing else.
//
// The pre-dispatch guard refuses any `--` token absent from knownFlags, so a
// table MISSING a flag refuses a working invocation (the serve outage), and a
// table with a SURPLUS flag catalogues one the parser will then reject with a
// different message. Both directions are checked.
//
// WHAT MUTATION MAKES IT FAIL: writing knownFlags["find"] out by hand — add or
// drop one entry and the corresponding direction fires. Verified by replacing
// findKnownFlags() with a literal set missing --details.
func TestFindFlagTableIsDerivedFromTheParser(t *testing.T) {
	table := knownFlags["find"]
	if len(table) == 0 {
		t.Fatal("`find` is not catalogued, so the unknown-flag guard is inert for it")
	}
	// Every catalogued flag must be one the parser recognises. A flag the guard
	// waves through and the parser rejects is catalogued in name only.
	for f := range table {
		if _, err := parseFindArgs([]string{"--implies", "q.oath", f}); err != nil &&
			strings.Contains(err.Error(), "has no flag") {
			t.Errorf("knownFlags lists %q but parseFindArgs does not recognise it", f)
		}
	}
	// Every flag the parser recognises must be catalogued, or the guard refuses
	// the invocation before the parser ever sees it.
	for f := range findSelectors {
		if !table[f] {
			t.Errorf("parseFindArgs handles %q but it is absent from knownFlags, so `oath find %s ...` "+
				"is now refused before dispatch", f, f)
		}
	}
	if !table[findDetailsFlag] {
		t.Errorf("%s is absent from knownFlags, so it is refused before parseFindArgs sees it", findDetailsFlag)
	}
	// And the table has nothing else in it: a surplus entry is a flag the guard
	// accepts and the parser then refuses with an unrelated message.
	if want := len(findSelectors) + 1; len(table) != want {
		t.Errorf("knownFlags[\"find\"] has %d entries, want %d (%d selectors + %s) — a surplus entry is "+
			"catalogued but unparsed", len(table), want, len(findSelectors), findDetailsFlag)
	}
}

// TestFindImpliesDetailsReachesTheDetailedReport drives the WHOLE command —
// argument strings in, rendered report out — rather than calling apiFindImplies
// with a mode a test chose. A test that parses the flag and then passes the
// parsed mode to the API by hand witnesses the parser and NOT the wiring: it
// passes unchanged over a dispatch that drops fa.mode on the floor.
//
// WHAT MUTATION MAKES IT FAIL, and all three were verified by making the edit:
// passing findImpliesSummary literally at the dispatch site; ignoring the mode
// argument inside cmdFindImplies; and setting fa.mode unconditionally, which the
// CONTROL below catches and the positive assertions do not.
func TestFindImpliesDetailsReachesTheDetailedReport(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, reportDiffInt)
	query := filepath.Join(t.TempDir(), "flipped.oath")
	if err := os.WriteFile(query, []byte(reportFlipped), 0o644); err != nil {
		t.Fatal(err)
	}

	detailed := captureStdout(t, func() { runFind(st, []string{"--implies", query, "--details"}) })
	for _, want := range []string{"1 REFUTED", "diff-int", "countermodel (by evaluation)"} {
		if !strings.Contains(detailed, want) {
			t.Errorf("--details did not reach the detailed report (missing %q):\n%s", want, detailed)
		}
	}

	// THE CONTROL: the same query without the flag names nothing. Without it the
	// assertions above pass for a build that ignores --details and reports detail
	// unconditionally — the opposite defect, equally wrong, and invisible to any
	// assertion that only looks for the evidence being present.
	summary := captureStdout(t, func() { runFind(st, []string{"--implies", query}) })
	if !strings.Contains(summary, "1 REFUTED") {
		t.Fatalf("the refutation is not counted in summary mode, so the comparison below is vacuous:\n%s", summary)
	}
	// A REFUTED candidate IS named without --details: it is a result, not residue.
	// What --details adds for it is nothing, and that is deliberate — so this
	// arm's discrimination has to come from an UNRESOLVED candidate, which is the
	// status the flag actually gates. Asserting absence of `diff-int` here would
	// now pin the opposite of the intended contract.
	if !strings.Contains(summary, "diff-int") {
		t.Errorf("a refuted candidate must be named even without --details — a refutation is a "+
			"result and its name is the finding:\n%s", summary)
	}
	if strings.Contains(summary, "unsettled") {
		t.Errorf("without --details an UNRESOLVED candidate must not be named — that is the "+
			"status the flag gates:\n%s", summary)
	}
}

// The help text is the only place a user learns --details exists. A flag that is
// parsed, catalogued and undocumented is one nobody will type.
func TestUsageDocumentsFindDetails(t *testing.T) {
	if !strings.Contains(usage, "--implies") {
		t.Fatal("the usage text no longer mentions find --implies; this test is measuring the wrong string")
	}
	if !strings.Contains(usage, findDetailsFlag) {
		t.Errorf("`oath find --implies` gained %s and the usage text does not mention it:\n%s",
			findDetailsFlag, usage)
	}
}
