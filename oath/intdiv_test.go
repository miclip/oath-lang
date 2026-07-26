package main

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// #71: the SMT bridge for Int `/` and `%` must agree with the EVALUATOR on every
// sign combination — that agreement is the whole point, since emitting SMT-LIB's
// Euclidean div/mod directly would prove a different theorem for negative
// dividends. Differential, not asserted: ask z3 what the bridge computes and
// compare against big.Int Quo/Rem, the kernel's actual semantics.
func TestIntDivBridgeMatchesEvaluator(t *testing.T) {
	requireZ3(t)
	c := &smtCtx{}
	c.ensureIntDivDefs()
	defs := strings.Join(c.decls, "\n")

	vals := []int64{-97, -12, -7, -3, -1, 0, 1, 3, 7, 12, 97}
	var script strings.Builder
	script.WriteString(defs + "\n")
	type probe struct{ a, b int64 }
	var probes []probe
	for _, a := range vals {
		for _, b := range vals {
			if b == 0 {
				continue // partial in the kernel; deliberately unconstrained in SMT
			}
			probes = append(probes, probe{a, b})
			fmt.Fprintf(&script, "(simplify (oath_tquo %d %d))\n(simplify (oath_trem %d %d))\n", a, b, a, b)
		}
	}
	f, err := os.CreateTemp(t.TempDir(), "*.smt2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script.String()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command("z3", f.Name()).CombinedOutput()
	if err != nil {
		t.Fatalf("z3: %v\n%s", err, out)
	}
	lines := strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " "))
	// z3 prints negatives as (- n); rejoin those into single tokens.
	var got []string
	for i := 0; i < len(lines); i++ {
		if lines[i] == "(-" && i+1 < len(lines) {
			got = append(got, "-"+strings.TrimSuffix(lines[i+1], ")"))
			i++
			continue
		}
		got = append(got, lines[i])
	}
	if len(got) != 2*len(probes) {
		t.Fatalf("expected %d results, got %d: %s", 2*len(probes), len(got), out)
	}
	for i, p := range probes {
		wantQ := new(big.Int).Quo(big.NewInt(p.a), big.NewInt(p.b)).String()
		wantR := new(big.Int).Rem(big.NewInt(p.a), big.NewInt(p.b)).String()
		if got[2*i] != wantQ {
			t.Errorf("%d / %d: SMT=%s evaluator=%s", p.a, p.b, got[2*i], wantQ)
		}
		if got[2*i+1] != wantR {
			t.Errorf("%d %% %d: SMT=%s evaluator=%s", p.a, p.b, got[2*i+1], wantR)
		}
	}
}

// The bridge is emitted lazily: a goal touching neither operator must produce a
// script byte-identical to one from a kernel predating #71.
func TestIntDivDefsOnlyWhenUsed(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn nodiv [] [(x Int)] Int (+ x 1)
		(prop inc [(x Int)] (== (nodiv x) (+ x 1))))`)
	h, _ := st.Resolve("nodiv")
	sc, err := directAttemptScript(st, h, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sc, "oath_tquo") || strings.Contains(sc, "oath_trem") {
		t.Fatalf("division bridge leaked into a script that uses no division:\n%s", sc)
	}
}
