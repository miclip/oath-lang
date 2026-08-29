package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
		// --timeout: a --implies-only, value-taking flag. Each refusal names its rule.
		{"timeout on --spec", []string{"--spec", "q.oath", "--timeout", "5s"}, "--implies form only"},
		{"timeout on a name lookup", []string{"rat-add", "--timeout", "5s"}, "--implies form only"},
		{"timeout with no value", []string{"--implies", "q.oath", "--timeout"}, "needs a duration"},
		{"timeout given a flag as its value", []string{"--implies", "q.oath", "--timeout", "--details"}, "is a flag"},
		{"timeout not a duration", []string{"--implies", "q.oath", "--timeout", "soon"}, "is not a duration"},
		{"timeout non-positive", []string{"--implies", "q.oath", "--timeout", "0s"}, "positive"},
		{"timeout twice", []string{"--implies", "q.oath", "--timeout", "1s", "--timeout", "2s"}, "twice"},
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
	for _, f := range []string{findDetailsFlag, findTimeoutFlag} {
		if !table[f] {
			t.Errorf("%s is absent from knownFlags, so it is refused before parseFindArgs sees it", f)
		}
	}
	// And the table has nothing else in it: a surplus entry is a flag the guard
	// accepts and the parser then refuses with an unrelated message.
	if want := len(findSelectors) + 2; len(table) != want {
		t.Errorf("knownFlags[\"find\"] has %d entries, want %d (%d selectors + %s + %s) — a surplus entry is "+
			"catalogued but unparsed", len(table), want, len(findSelectors), findDetailsFlag, findTimeoutFlag)
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

// --timeout parses to a wall-clock budget, defaults to unbounded, and composes
// with --details. The budget is what bounds the proof search in apiFindImpliesOpts.
func TestParseFindArgsTimeout(t *testing.T) {
	got, err := parseFindArgs([]string{"--implies", "q.oath", "--timeout", "30s"})
	if err != nil {
		t.Fatal(err)
	}
	if got.budget != 30*time.Second {
		t.Errorf("--timeout 30s → budget %v, want 30s", got.budget)
	}
	// Unbounded is the default: 0 is what apiFindImpliesOpts reads as "no ceiling",
	// the fully deterministic mode conformance and every scripted run take.
	if def, _ := parseFindArgs([]string{"--implies", "q.oath"}); def.budget != 0 {
		t.Errorf("without --timeout the budget must be 0 (unbounded), got %v", def.budget)
	}
	// Composes with --details, order-independent.
	both, err := parseFindArgs([]string{"--implies", "q.oath", "--details", "--timeout", "1m"})
	if err != nil {
		t.Fatal(err)
	}
	if both.budget != time.Minute || both.mode != findImpliesDetailed {
		t.Errorf("--details + --timeout must both apply: budget=%v mode=%d", both.budget, both.mode)
	}
}

// The help text is the only place a user learns --timeout exists.
func TestUsageDocumentsFindTimeout(t *testing.T) {
	if !strings.Contains(usage, findTimeoutFlag) {
		t.Errorf("`oath find --implies` gained %s and the usage text does not mention it:\n%s", findTimeoutFlag, usage)
	}
}

// An elapsed budget must report the abort AS the report, framed NO VERDICT — the
// unreached candidates were never examined, which is a fact about the budget, not
// about them. A 1ns budget elapses before the first candidate, so the scan stops
// immediately and nothing is misreported as "no definition satisfies".
func TestFindImpliesTimeoutReportsAbort(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn a2 [] [(x Int) (y Int)] Int (+ x y)
		(prop c [(x Int) (y Int)] (== (a2 x y) (a2 y x))))`)
	out, err := apiFindImpliesOpts(st, `(defn wanted [] [(x Int) (y Int)] Int (+ x y)
		(prop c [(x Int) (y Int)] (== (wanted x y) (wanted y x))))`, findImpliesSummary, 1*time.Nanosecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SEARCH INCOMPLETE") || !strings.Contains(out, "NO VERDICT") {
		t.Errorf("an elapsed budget must report the abort as NO VERDICT:\n%s", out)
	}
	// And it must NOT claim the corpus is empty — the whole point of suppressing
	// the fallback when the search did not finish.
	if strings.Contains(out, "no definition provably satisfies this") {
		t.Errorf("an aborted search must NOT claim nothing satisfies the query:\n%s", out)
	}
}

// A non-nil progress writer receives a per-candidate line; an unbounded run
// finishes with no abort banner. Nil progress (the default, and MCP) writes
// nothing — asserted by the abort test above running with nil and not panicking.
func TestFindImpliesProgressIsWritten(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn a2 [] [(x Int) (y Int)] Int (+ x y)
		(prop c [(x Int) (y Int)] (== (a2 x y) (a2 y x))))`)
	var prog strings.Builder
	out, err := apiFindImpliesOpts(st, `(defn wanted [] [(x Int) (y Int)] Int (+ x y)
		(prop c [(x Int) (y Int)] (== (wanted x y) (wanted y x))))`, findImpliesSummary, 0, &prog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prog.String(), "proving") {
		t.Errorf("a non-nil progress writer must receive a per-candidate line, got %q", prog.String())
	}
	if strings.Contains(out, "SEARCH INCOMPLETE") {
		t.Errorf("an unbounded (budget 0) run must not report an abort:\n%s", out)
	}
}

// The fallback "nothing satisfies" is a claim about the corpus, valid only when
// the search FINISHED. renderImplyResults must suppress it when complete=false.
func TestRenderImplyFallbackSuppressedWhenIncomplete(t *testing.T) {
	var b strings.Builder
	renderImplyResults(&b, nil, findImpliesSummary, false)
	if strings.Contains(b.String(), "no definition provably satisfies this") {
		t.Errorf("an unfinished search must not claim the corpus is empty:\n%s", b.String())
	}
	// Control: the same empty results WITH complete=true does make the claim.
	var b2 strings.Builder
	renderImplyResults(&b2, nil, findImpliesSummary, true)
	if !strings.Contains(b2.String(), "no definition provably satisfies this") {
		t.Errorf("a finished empty search must still report that nothing satisfies it:\n%s", b2.String())
	}
}

// A candidate-less store, given a budget large enough to FINISH scanning, must
// complete and fire the "nothing satisfies" fallback — not report an abort. (A
// budget too small to finish honestly reports INCOMPLETE, since the scan did not
// establish the corpus is empty; that is the separate immediate-abort case.)
func TestFindImpliesTimeoutFinishesWithoutCandidates(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(data Color [] (Red) (Green))`) // no function candidates at all
	out, err := apiFindImpliesOpts(st, `(defn wanted [] [(x Int) (y Int)] Int (+ x y)
		(prop c [(x Int) (y Int)] (== (wanted x y) (wanted y x))))`, findImpliesSummary, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SEARCH INCOMPLETE") {
		t.Errorf("a candidate-less store with an ample budget must finish, not abort:\n%s", out)
	}
	if !strings.Contains(out, "no definition provably satisfies this") {
		t.Errorf("with no candidates the completed-search fallback must fire:\n%s", out)
	}
}

// The running progress count accumulates ACROSS properties (it is a running
// number, not a per-property one that resets), and the property counter names
// which property is being searched.
func TestFindImpliesProgressRunsAcrossProperties(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn a2 [] [(x Int) (y Int)] Int (+ x y)
		(prop c1 [(x Int) (y Int)] (== (a2 x y) (a2 y x)))
		(prop c2 [(x Int) (y Int)] (== (a2 x y) (a2 x y))))`)
	var prog strings.Builder
	_, err := apiFindImpliesOpts(st, `(defn wanted [] [(x Int) (y Int)] Int (+ x y)
		(prop c1 [(x Int) (y Int)] (== (wanted x y) (wanted y x)))
		(prop c2 [(x Int) (y Int)] (== (wanted x y) (wanted x y))))`, findImpliesSummary, 0, &prog)
	if err != nil {
		t.Fatal(err)
	}
	p := prog.String()
	if !strings.Contains(p, "proving 2") { // 1 candidate × 2 properties = a 2nd check
		t.Errorf("the running count must accumulate across properties (want a 'proving 2'):\n%q", p)
	}
	if !strings.Contains(p, "property 2/2") {
		t.Errorf("progress must name which property is being searched:\n%q", p)
	}
}

// truncateForProgress operates on a byte-budgeted display column but must never
// emit invalid UTF-8: a multibyte definition name cut mid-rune would corrupt the
// terminal stream. It slices runes, not bytes.
func TestTruncateForProgressKeepsValidUTF8(t *testing.T) {
	name := strings.Repeat("λ", 40) // 40 two-byte runes = 80 bytes, over the budget
	got := truncateForProgress(name, 28)
	if !utf8.ValidString(got) {
		t.Errorf("truncated progress name is not valid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 28 {
		t.Errorf("truncation exceeded the rune budget: %d runes in %q", n, got)
	}
	// An all-ASCII name under budget is returned unchanged.
	if got := truncateForProgress("reverse", 28); got != "reverse" {
		t.Errorf("a short name must be unchanged, got %q", got)
	}
}

// attemptWallCap shortens the z3 per-attempt cap to an active search budget so a
// single slow proof cannot overrun --timeout. Unset, it must equal the host cap
// exactly (the default/conformance path is byte-identical); past the deadline it
// must stay positive (fail fast, never disable the cap).
func TestAttemptWallCap(t *testing.T) {
	clearSearchWallDeadline()
	if got := attemptWallCap(); got != proveWallCap() {
		t.Errorf("with no search deadline the cap must equal proveWallCap (%v), got %v", proveWallCap(), got)
	}
	setSearchWallDeadline(time.Now().Add(2 * time.Second))
	defer clearSearchWallDeadline()
	if got := attemptWallCap(); got <= 0 || got > proveWallCap() {
		t.Errorf("a near deadline must shorten the cap into (0, proveWallCap], got %v", got)
	}
	setSearchWallDeadline(time.Now().Add(-time.Hour))
	if got := attemptWallCap(); got <= 0 {
		t.Errorf("a past deadline must still yield a positive cap, got %v", got)
	}
}
