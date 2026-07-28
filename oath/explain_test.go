package main

import (
	"strings"
	"testing"
)

// #74 discovery v0: the decision package must report the reasons NOT to use an
// artifact, derived from recorded state — never inferred, never flattering. An
// agent choosing between candidates needs the honest failure modes more than the
// claims, and a definition must not be able to look better than its evidence.
func TestExplainReportsLimitationsHonestly(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn weak [] [(x Int)] Int (if (< x 0) (neg x) x)
		(prop trivial [(x Int)] (== (weak x) (weak x))))`)

	pkg, err := buildExplain(st, "weak")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(pkg.Limitations, " | ")

	// An unproven property must be declared unproven, not silently counted.
	if !strings.Contains(joined, "TESTED, not proven") {
		t.Fatalf("tested property not disclosed: %q", joined)
	}
	// No mutation score means UNMEASURED — reported as absent, never as zero or
	// as a pass, since "unknown" and "weak" are different claims.
	if !strings.Contains(joined, "UNMEASURED") {
		t.Fatalf("missing spec strength not disclosed: %q", joined)
	}
	// Same author for spec and body is a real weakness in the evidence.
	if !strings.Contains(joined, "share an author") {
		t.Fatalf("absent authorship separation not disclosed: %q", joined)
	}
	// The spec is reported by property CONTENT HASH: the identity of the claim,
	// so a consumer can compare specs across differently-named definitions.
	if len(pkg.Properties) != 1 || pkg.Properties[0].Hash == "" {
		t.Fatalf("property hash missing: %+v", pkg.Properties)
	}
	if pkg.Properties[0].Status != "tested" {
		t.Fatalf("status = %q, want tested", pkg.Properties[0].Status)
	}
}

// A falsified property is the strongest possible reason not to use something and
// must be surfaced first-class rather than folded into a guarantee string.
func TestExplainSurfacesFalsified(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn broken [] [(x Int)] Int (+ x 1)
		(prop wrong [(x Int)] (== (broken x) x)))`)
	pkg, err := buildExplain(st, "broken")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Properties[0].Status != "falsified" {
		t.Fatalf("status = %q, want falsified", pkg.Properties[0].Status)
	}
	if !strings.Contains(strings.Join(pkg.Limitations, " "), "FALSIFIED") {
		t.Fatalf("falsification not in limitations: %v", pkg.Limitations)
	}
}
