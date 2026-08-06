package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// MEASUREMENT FOR THE PORTABLE RESOURCE PROFILE (#149).
//
// The profile's budgets must be chosen from what real use needs, with a
// generous envelope above it — NOT from whatever a browser tab or a node
// process happened to survive. A limit reverse-engineered from one host's stack
// is the defect this whole issue exists to remove, so measuring the floor is a
// prerequisite for setting the ceiling, not documentation of it.
//
// THREE QUANTITIES, AND THEY ARE DELIBERATELY NOT ONE NUMBER:
//
//	source bytes      what an ingestion boundary accepts
//	syntax nesting    genuinely nested source forms — a string literal is ONE
//	                  node here regardless of length
//	canonical nodes   Term/Ty nodes in the elaborated definition, where a
//	                  5,000-rune string IS a 5,000-long SCons spine
//
// The 5,000-rune witness is exactly the case that separates the last two: it is
// work, not nesting. Collapsing them into a single "depth" budget would let the
// canonical REPRESENTATION of linear data decide which strings are legal
// programs, which is the thing the backend-independence invariant forbids.

// sxDepth is genuine syntactic nesting: one level per bracketed form. A string
// literal is a leaf here however long it is — that is the point.
func sxDepth(x sx) int {
	best := 0
	for _, k := range x.Kids {
		if d := sxDepth(k); d > best {
			best = d
		}
	}
	return best + 1
}

func sxNodes(x sx) int {
	n := 1
	for _, k := range x.Kids {
		n += sxNodes(k)
	}
	return n
}

// termNodes / tyNodes count canonical structure. These are measurement helpers
// over trusted committed data, so ordinary recursion is fine here; they are not
// on the oathCheck path.
func tyNodes(t *Ty) int {
	if t == nil {
		return 0
	}
	n := 1 + tyNodes(t.A) + tyNodes(t.B)
	for i := range t.Args {
		n += tyNodes(&t.Args[i])
	}
	return n
}

func tyDepth(t *Ty) int {
	if t == nil {
		return 0
	}
	best := 0
	for _, c := range []*Ty{t.A, t.B} {
		if d := tyDepth(c); d > best {
			best = d
		}
	}
	for i := range t.Args {
		if d := tyDepth(&t.Args[i]); d > best {
			best = d
		}
	}
	return best + 1
}

func termNodes(t *Term) int {
	if t == nil {
		return 0
	}
	n := 1 + termNodes(t.A) + termNodes(t.B) + termNodes(t.C) + tyNodes(t.Ty)
	for i := range t.Args {
		n += termNodes(&t.Args[i])
	}
	for i := range t.Arms {
		n += termNodes(&t.Arms[i])
	}
	for i := range t.TyArgs {
		n += tyNodes(&t.TyArgs[i])
	}
	return n
}

func termDepth(t *Term) int {
	if t == nil {
		return 0
	}
	best := 0
	for _, c := range []*Term{t.A, t.B, t.C} {
		if d := termDepth(c); d > best {
			best = d
		}
	}
	for i := range t.Args {
		if d := termDepth(&t.Args[i]); d > best {
			best = d
		}
	}
	for i := range t.Arms {
		if d := termDepth(&t.Arms[i]); d > best {
			best = d
		}
	}
	if d := tyDepth(t.Ty); d > best {
		best = d
	}
	return best + 1
}

func defNodes(d *Def) (nodes, depth int) {
	nodes, depth = tyNodes(d.Ty), tyDepth(d.Ty)
	if n, dp := termNodes(d.Body), termDepth(d.Body); true {
		nodes += n
		if dp > depth {
			depth = dp
		}
	}
	for i := range d.Props {
		nodes += termNodes(&d.Props[i].Body)
		if dp := termDepth(&d.Props[i].Body); dp > depth {
			depth = dp
		}
		for j := range d.Props[i].Binders {
			nodes += tyNodes(&d.Props[i].Binders[j])
		}
	}
	for _, ctor := range d.Ctors {
		for i := range ctor {
			nodes += tyNodes(&ctor[i])
			if dp := tyDepth(&ctor[i]); dp > depth {
				depth = dp
			}
		}
	}
	return nodes, depth
}

// TestMeasureCorpusResourceFloor reports what the committed corpus and the
// applications actually require. It ASSERTS ONLY that the measurement ran —
// the numbers are input to a policy decision, and pinning them here would turn
// an observation into a gate nobody chose.
func TestMeasureCorpusResourceFloor(t *testing.T) {
	type row struct {
		file              string
		bytes, depth, nds int
	}
	var rows []row

	// The corpus is examples/ PLUS apps/ (CLAUDE.md). Globbed, not listed.
	for _, dir := range []string{"../examples", "../apps"} {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".oath") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			forms, err := parseForms(string(b))
			if err != nil {
				t.Errorf("%s did not parse: %v", p, err)
				return nil
			}
			r := row{file: p, bytes: len(b)}
			for _, f := range forms {
				if d := sxDepth(f); d > r.depth {
					r.depth = d
				}
				r.nds += sxNodes(f)
			}
			rows = append(rows, r)
			return nil
		})
	}
	if len(rows) < 20 {
		t.Fatalf("only %d corpus files measured — the walk did not find the corpus, "+
			"so any envelope derived from this run would be derived from nothing", len(rows))
	}

	maxBytes, maxDepth, maxNodes := 0, 0, 0
	var bFile, dFile, nFile string
	totalBytes := 0
	for _, r := range rows {
		totalBytes += r.bytes
		if r.bytes > maxBytes {
			maxBytes, bFile = r.bytes, r.file
		}
		if r.depth > maxDepth {
			maxDepth, dFile = r.depth, r.file
		}
		if r.nds > maxNodes {
			maxNodes, nFile = r.nds, r.file
		}
	}

	t.Logf("SOURCE-LEVEL FLOOR over %d corpus files (%d KB total)", len(rows), totalBytes/1024)
	t.Logf("  max source bytes  : %7d  (%s)", maxBytes, bFile)
	t.Logf("  max syntax nesting: %7d  (%s)   <- string literals count as 1", maxDepth, dFile)
	t.Logf("  max sx nodes/file : %7d  (%s)", maxNodes, nFile)

	// Deepest few, so the shape of the tail is visible rather than just its max.
	sort.Slice(rows, func(i, j int) bool { return rows[i].depth > rows[j].depth })
	for i := 0; i < 5 && i < len(rows); i++ {
		t.Logf("    nesting %3d  %s", rows[i].depth, rows[i].file)
	}
}

// TestMeasureCanonicalNodeFloor reports the canonical side: what the elaborated
// definitions in the committed store actually contain. This is where a string
// literal stops being one node — the quantity a work budget must cover.
func TestMeasureCanonicalNodeFloor(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	names := st.Names()
	if len(names) < 100 {
		t.Fatalf("store resolved only %d names; the measurement did not run", len(names))
	}

	// PER-OBJECT, over what live names reach — not a walk of meta/, which also
	// holds superseded objects (CLAUDE.md). Duplicate resolutions collapse here
	// deliberately: this asks about objects, not about what the corpus offers.
	seen := map[string]bool{}
	maxNodes, maxDepth := 0, 0
	var nName, dName string
	total := 0
	for name, h := range names {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		n, dp := defNodes(d)
		total += n
		if n > maxNodes {
			maxNodes, nName = n, name
		}
		if dp > maxDepth {
			maxDepth, dName = dp, name
		}
	}

	t.Logf("CANONICAL FLOOR over %d distinct objects reached by live names", len(seen))
	t.Logf("  max nodes/def : %6d  (%s)", maxNodes, nName)
	t.Logf("  max depth/def : %6d  (%s)   <- an SCons spine WOULD show here", maxDepth, dName)
	t.Logf("  mean nodes/def: %6d", total/len(seen))
}

// TestProfileCommentMatchesMeasurement. profile.go's header quotes the corpus
// figures the budgets were chosen against:
//
//	syntax nesting        17
//	canonical nodes    1,293
//
// Those are COPIES of a measurement, and a copy is correct exactly once.
// TestCorpusFitsPortableProfile guards the CLAIM (headroom stays above 4x), but
// it fires only at 16,384 nodes — so between 1,293 and there the comment could
// drift silently, and the numbers a reader calibrates against would be wrong
// while every gate stayed green.
//
// So the figures are DERIVED from the source of truth and compared to the text,
// the same relationship check-doc-numbers has with fixtures/outcomes.json.
func TestProfileCommentMatchesMeasurement(t *testing.T) {
	src, err := os.ReadFile("profile.go")
	if err != nil {
		t.Fatalf("reading profile.go: %v", err)
	}
	quoted := map[string]int{}
	for _, line := range strings.Split(string(src), "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "//"))
		// "syntax nesting  17  512  30x" / "canonical nodes/def  1,293  65,536  50x"
		if len(f) >= 3 && (f[0] == "syntax" || f[0] == "canonical") {
			n, err := strconv.Atoi(strings.ReplaceAll(f[2], ",", ""))
			if err == nil {
				quoted[f[0]] = n
			}
		}
	}
	if len(quoted) != 2 {
		t.Fatalf("could not find both figures in profile.go's header (found %v) — "+
			"if the comment was reworded, update this check WITH it", quoted)
	}

	// Measure, from the corpus itself.
	maxNesting := 0
	for _, dir := range []string{"../examples", "../apps"} {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".oath") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			forms, perr := parseForms(string(b))
			if perr != nil {
				return nil
			}
			for _, f := range forms {
				if d := sxDepth(f); d > maxNesting {
					maxNesting = d
				}
			}
			return nil
		})
	}
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the corpus: %v", err)
	}
	seen, maxNodes := map[string]bool{}, 0
	for _, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		if n, _ := defNodes(d); n > maxNodes {
			maxNodes = n
		}
	}
	if maxNesting == 0 || maxNodes == 0 {
		t.Fatalf("measurement produced nothing (nesting=%d nodes=%d)", maxNesting, maxNodes)
	}

	if quoted["syntax"] != maxNesting {
		t.Errorf("profile.go says max syntax nesting is %d; the corpus measures %d",
			quoted["syntax"], maxNesting)
	}
	if quoted["canonical"] != maxNodes {
		t.Errorf("profile.go says max canonical nodes is %d; the corpus measures %d",
			quoted["canonical"], maxNodes)
	}
}
