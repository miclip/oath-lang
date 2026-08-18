package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestSignatureNeighboursReportsWhatItDropped is a NO-SILENT-CAP control.
//
// The neighbour list is the fallback `--spec` prints when a law matches nothing,
// and it is the whole mechanism behind the signature-probe technique
// docs/discovery.md now recommends: a deliberately unmatchable law, run to see
// what the corpus HAS at a shape. It is printed with a cap.
//
// Capping is fine. Capping SILENTLY is not: a caller reading eight names cannot
// tell a complete answer from a truncated one, and the definition they want may
// be the ninth. An empty-or-short discovery result must never be allowed to
// imply "that is all there is" — the same defect as a search that reports
// nothing when it merely stopped looking.
func TestSignatureNeighboursReportsWhatItDropped(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)

	// Twelve definitions at one signature, each with a property of its own —
	// property-less definitions are deliberately omitted from the list, so a
	// fixture built without laws would exercise nothing.
	const n = 12
	for i := 0; i < n; i++ {
		put(t, st, fmt.Sprintf(`(defn cap-%02d [] [(x Int)] Int (+ x %d)
		  (prop shifts [(x Int)] (== (cap-%02d x) (+ x %d))))`, i, i, i, i))
	}

	// The query is a def of the shape under test; signatureNeighbours takes the
	// DEF, generalizes its type, and compares.
	qd := mustDef(t, st, "cap-00")
	h, _ := st.Resolve("cap-00")
	got, total := signatureNeighboursN(st, qd, h)

	// THE COUNT IS NOT len(rows), and a caller rendering a header must use the
	// total. Adding the notice row made `--spec` print "9 definition(s)" for
	// every truncated result regardless of the real population — the silent cap
	// reappearing one layer up, in the number a reader would quote.
	if total != n-1 {
		t.Errorf("matched count is %d, want %d (cap-00 is the query and is excluded)", total, n-1)
	}
	if len(got) == 0 {
		t.Fatal("no neighbours at all, so this test is not measuring what it claims")
	}

	last := got[len(got)-1]
	if !strings.Contains(last, "more at this signature") {
		t.Fatalf("the list was truncated without saying so. %d definitions share "+
			"the signature and %d were printed, with no line reporting the "+
			"remainder — a caller cannot tell this from a complete answer:\n  %s",
			n, len(got), strings.Join(got, "\n  "))
	}
	// The COUNT must be right, not merely present: an off-by-one here is a
	// number a reader would trust and act on.
	// cap-00 is excluded as the query itself, so n-1 are candidates.
	wantDropped := total - (len(got) - 1)
	if !strings.Contains(last, fmt.Sprintf("and %d more", wantDropped)) {
		t.Errorf("the truncation line reports the wrong remainder; %d were "+
			"printed of %d, so %d were dropped:\n  %s",
			len(got)-1, n, wantDropped, last)
	}

	// THE CONTROL: below the cap there must be no such line, or the test above
	// would pass on a list that always claims truncation.
	st2 := newStore(t)
	put(t, st2, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st2, `(defn only-one [] [(x Int)] Int (+ x 1)
	  (prop shifts [(x Int)] (== (only-one x) (+ x 1))))`)
	qd2 := mustDef(t, st2, "only-one")
	small := signatureNeighbours(st2, qd2, "")
	for _, l := range small {
		if strings.Contains(l, "more at this signature") {
			t.Errorf("an uncapped list claims a remainder it does not have: %s", l)
		}
	}
}
