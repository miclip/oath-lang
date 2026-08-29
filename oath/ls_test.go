package main

import (
	"strings"
	"testing"
)

// `oath ls` prints each function's SIGNATURE (finding #3's secondary half), so a
// consumer can ask "what does this corpus hold at this shape?" with a grep rather
// than by writing a probe query. Data definitions carry no `::` signature — their
// shape is their name — and a polymorphic type renders with its own variable
// names, not positional t0.
func TestLsShowsFunctionSignatures(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn add2 [] [(a Int) (b Int)] Int (+ a b))`)
	put(t, st, `(data Color [] (Red) (Green))`)
	put(t, st, `(defn idd [a] [(x a)] a x)`)
	out := apiLs(st)

	if !strings.Contains(out, "::  (-> Int Int Int)") {
		t.Errorf("ls must render a function's signature after `::`:\n%s", out)
	}
	if !strings.Contains(out, "::  (-> a a)") {
		t.Errorf("a polymorphic signature must use its type-variable name, not t0:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Color") && strings.Contains(line, "::") {
			t.Errorf("a data definition must not carry a `::` signature:\n%s", line)
		}
	}
	// The existing name/hash/kind/guarantee layout is unchanged — the signature
	// only trails it — so a `func <guarantee>` reader is not disturbed.
	if !strings.Contains(out, "func  tested") && !strings.Contains(out, "func  asserted") {
		t.Errorf("the kind/guarantee columns must be preserved ahead of the signature:\n%s", out)
	}
}

// Per-alias vocabulary: a polymorphic object published under two names with
// different type-variable spellings must render each row with ITS OWN name's
// spelling (#19). Without selecting the alias naming block, every row would show
// the newest alias's type variables.
func TestLsSignatureUsesPerAliasTyVarNames(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn poly-a [a] [(x a)] a x)`)
	put(t, st, `(defn poly-b [b] [(x b)] b x)`) // structurally identical → one object, two aliases
	out := apiLs(st)
	if !strings.Contains(out, "poly-a") || !strings.Contains(out, "poly-b") {
		t.Fatalf("both aliases should be listed:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "poly-a ") && !strings.Contains(line, "(-> a a)") {
			t.Errorf("poly-a must render with its OWN tyvar name `a`:\n%s", line)
		}
		if strings.HasPrefix(line, "poly-b ") && !strings.Contains(line, "(-> b b)") {
			t.Errorf("poly-b must render with its OWN tyvar name `b`:\n%s", line)
		}
	}
}
