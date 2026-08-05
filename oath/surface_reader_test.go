package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reader was made iterative for #149. These tests witness two DIFFERENT
// claims, and neither implies the other:
//
//	semantics unchanged  -> differential against the original recursive reader
//	host stack unused    -> depths that overflowed the recursive one now parse
//
// A depth test alone would pass over a reader that returned wrong forms; a
// differential alone would pass over one that still recursed.

// readRecursive is the reader EXACTLY as it stood before the iterative rewrite.
// It is the oracle, kept here rather than deleted so the claim "this refactor
// changed nothing observable" has a witness that can actually fail.
func readRecursive(r *reader) (sx, error) {
	if r.pos >= len(r.toks) {
		return sx{}, fmt.Errorf("unexpected end of input")
	}
	t := r.toks[r.pos]
	r.pos++
	switch t.kind {
	case "int":
		return sx{K: "int", Int: t.i, Line: t.line}, nil
	case "rat":
		return sx{K: "rat", Rat: t.r, Line: t.line}, nil
	case "float":
		return sx{K: "float", Float: t.f, Line: t.line}, nil
	case "str":
		return sx{K: "str", Str: t.s, Line: t.line}, nil
	case "sym":
		return sx{K: "sym", Sym: t.sym, Line: t.line}, nil
	case "(", "[", "{":
		closer := ")"
		kind := "list"
		if t.kind == "[" {
			closer = "]"
			kind = "brack"
		}
		if t.kind == "{" {
			closer = "}"
			kind = "brace"
		}
		var kids []sx
		for {
			if r.pos >= len(r.toks) {
				return sx{}, fmt.Errorf("line %d: unclosed %q", t.line, t.kind)
			}
			if r.toks[r.pos].kind == closer {
				r.pos++
				return sx{K: kind, Kids: kids, Line: t.line}, nil
			}
			k, err := readRecursive(r)
			if err != nil {
				return sx{}, err
			}
			kids = append(kids, k)
		}
	}
	return sx{}, fmt.Errorf("line %d: unexpected %q", t.line, t.kind)
}

// parseFormsRecursive mirrors parseForms over the oracle reader.
func parseFormsRecursive(src string) ([]sx, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	r := &reader{toks: toks}
	var out []sx
	for r.pos < len(r.toks) {
		x, err := readRecursive(r)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// sxString renders a form structurally, including Line, so the differential
// compares the whole tree rather than a shape summary that could hide a
// misplaced child or a dropped line number.
func sxString(x sx) string {
	var b strings.Builder
	var walk func(sx)
	walk = func(v sx) {
		fmt.Fprintf(&b, "(%s@%d", v.K, v.Line)
		switch v.K {
		case "int":
			fmt.Fprintf(&b, " %v", v.Int)
		case "rat":
			fmt.Fprintf(&b, " %v", v.Rat)
		case "float":
			fmt.Fprintf(&b, " %v", v.Float)
		case "str":
			fmt.Fprintf(&b, " %q", v.Str)
		case "sym":
			fmt.Fprintf(&b, " %s", v.Sym)
		}
		for _, k := range v.Kids {
			b.WriteByte(' ')
			walk(k)
		}
		b.WriteByte(')')
	}
	walk(x)
	return b.String()
}

func formsString(xs []sx, err error) string {
	if err != nil {
		return "ERR: " + err.Error()
	}
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = sxString(x)
	}
	return strings.Join(parts, "\n")
}

// readerDifferentialCases are the inputs both readers must agree on. The error
// cases matter more than the happy ones: an iterative rewrite is most likely to
// diverge on WHICH delimiter it blames and on stray/mismatched closers.
var readerDifferentialCases = []string{
	``,
	`x`,
	`()`,
	`(a b c)`,
	`[a b]`,
	`{a b}`,
	`(a [b {c d}] e)`,
	`(defn f [] [(x Int)] Int (+ x 1))`,
	`1 2/3 4.5f "s" sym`,
	`((((()))))`,
	`(a (b (c (d))))`,
	// error paths
	`(`,
	`[`,
	`{`,
	`(a`,
	`(a (b`,
	`)`,
	`]`,
	`(]`,
	`(a]`,
	`[)`,
	`{)`,
	`((a)`,
	`(a) )`,
	`((((`,
	// several top-level forms, the second broken
	"(a)\n(b",
	// line numbers must survive: the unclosed report names the OPEN line
	"(\n\n\n",
	"(a\n(b\n(c",
}

func TestReaderMatchesRecursiveOracle(t *testing.T) {
	cases := append([]string{}, readerDifferentialCases...)

	// THE UNIVERSE IS THE CORPUS TOO, not only hand-written cases: the corpus is
	// what the kernel actually reads, and a hand-written list reflects the cases
	// its author thought of. examples/ PLUS apps/ (CLAUDE.md).
	for _, dir := range []string{"../examples", "../apps"} {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".oath") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err == nil {
				cases = append(cases, string(b))
			}
			return nil
		})
	}
	if len(cases) < len(readerDifferentialCases)+20 {
		t.Fatalf("corpus did not load: only %d cases, expected the hand-written %d plus the corpus. "+
			"A differential over hand-written cases only is a weaker test than it looks",
			len(cases), len(readerDifferentialCases))
	}

	for i, src := range cases {
		got := formsString(parseForms(src))
		want := formsString(parseFormsRecursive(src))
		if got != want {
			label := src
			if len(label) > 60 {
				label = label[:60] + "..."
			}
			t.Errorf("case %d (%q) diverged:\n iterative: %s\n recursive: %s", i, label, got, want)
		}
	}
	t.Logf("iterative reader agrees with the recursive oracle on %d inputs (%d hand-written + corpus)",
		len(cases), len(readerDifferentialCases))
}

// TestReaderDifferentialDiscriminates is the CONTROL. A differential test is
// worthless if the oracle and the subject cannot disagree — and this repo has
// repeatedly found checks that passed while measuring nothing. Here the oracle
// is deliberately perturbed; the comparison MUST notice.
func TestReaderDifferentialDiscriminates(t *testing.T) {
	// A form whose rendering depends on structure, order, and line numbers.
	const src = "(a [b {c 1}]\n d)"
	base := formsString(parseForms(src))

	// Perturbation 1: a dropped child.
	x, err := parseForms(src)
	if err != nil || len(x) != 1 || len(x[0].Kids) < 2 {
		t.Fatalf("setup failed: %v", err)
	}
	trimmed := x[0]
	trimmed.Kids = trimmed.Kids[:len(trimmed.Kids)-1]
	if formsString([]sx{trimmed}, nil) == base {
		t.Error("comparison did not notice a dropped child — it is not measuring structure")
	}

	// Perturbation 2: a changed line number.
	moved := x[0]
	moved.Line += 1
	if formsString([]sx{moved}, nil) == base {
		t.Error("comparison did not notice a changed line number")
	}

	// Perturbation 3: an error is distinguishable from a success.
	if formsString(parseForms("(")) == base {
		t.Error("comparison did not distinguish an error from a parse")
	}
}

// TestReaderDepthDoesNotUseHostStack witnesses the #149 claim directly: nesting
// depth is bounded by memory, not by an unobservable host resource. 200,000
// exceeds every depth measured to overflow the recursive reader on wasm (~20,000)
// by an order of magnitude.
//
// It runs natively, where Go's growable stacks meant the RECURSIVE reader also
// survived — so on its own this test would pass over the unfixed code, and that
// is exactly the trap this repo keeps finding. The witness that discriminates on
// the failing target is scripts/check-playground-wasm.mjs, which runs the same
// input through the SERVED wasm artifact. Both are required; neither is
// sufficient. See #149.
func TestReaderDepthDoesNotUseHostStack(t *testing.T) {
	// AT the limit: admitted, and actually that deep rather than truncated.
	atLimit := strings.Repeat("(", maxSyntaxNesting) + strings.Repeat(")", maxSyntaxNesting)
	forms, err := parseForms(atLimit)
	if err != nil {
		t.Fatalf("nesting %d is the limit and must be admitted, got %v", maxSyntaxNesting, err)
	}
	if len(forms) != 1 {
		t.Fatalf("expected one form, got %d", len(forms))
	}
	n, cur := 0, forms[0]
	for {
		n++
		if len(cur.Kids) == 0 {
			break
		}
		cur = cur.Kids[0]
	}
	if n != maxSyntaxNesting {
		t.Fatalf("expected nesting depth %d, measured %d — the reader truncated", maxSyntaxNesting, n)
	}

	// ONE OVER: refused, and refused as a RESOURCE limit rather than as
	// malformed syntax. The input is well-formed; only its size is at issue.
	over := strings.Repeat("(", maxSyntaxNesting+1) + strings.Repeat(")", maxSyntaxNesting+1)
	_, err = parseForms(over)
	if err == nil {
		t.Fatalf("nesting %d is over the limit and must be refused", maxSyntaxNesting+1)
	}
	var rl *resourceLimitErr
	if !errors.As(err, &rl) {
		t.Fatalf("refusal must be a typed resource limit, got %T: %v", err, err)
	}
	if rl.what != "syntax nesting" {
		t.Fatalf("wrong quantity blamed: %q", rl.what)
	}

	// FAR over: still a clean refusal, and CHEAP. This is the #149 claim proper
	// — a limit must be reached and reported, never approached until a host
	// stack gives out. 200,000 is ten times the depth measured to throw a host
	// exception out of oathCheck on wasm before this existed.
	huge := strings.Repeat("(", 200000)
	if _, err := parseForms(huge); !errors.As(err, &rl) {
		t.Fatalf("200,000 deep must refuse as a resource limit, got %v", err)
	}
}

// TestReaderUnclosedBlamesInnermost pins the error-message behaviour the
// differential also covers, as a named regression: the "unclosed" report must
// name the innermost open delimiter and its line.
func TestReaderUnclosedBlamesInnermost(t *testing.T) {
	_, err := parseForms("(a\n[b\n{c")
	if err == nil {
		t.Fatal("expected an unclosed error")
	}
	if got, want := err.Error(), `line 3: unclosed "{"`; got != want {
		t.Fatalf("unclosed error = %q, want %q", got, want)
	}
}
