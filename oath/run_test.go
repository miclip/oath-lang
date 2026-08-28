package main

import "testing"

// `oath run` interprets a pure (-> (List Str) Str) program on its arguments and
// returns its output, with no build and no toolchain. The arguments must reach the
// program as the same (List Str) a compiled binary would receive — including exact
// escaping of quotes/backslashes and faithful Unicode — so run and a build agree.
func TestRunProgram(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st) // List, Str, ...
	// A tiny CLI entry: join the arguments with a space, so the output is a
	// function of every argument and their order.
	put(t, st, `(defn str-append [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c r) (SCons c (str-append r b)))))`)
	put(t, st, `(defn join-sp [] [(xs (List Str))] Str
		(match xs
			((Nil) (SNil))
			((Cons h t) (match t
				((Nil) h)
				((Cons h2 t2) (str-append h (SCons 32 (join-sp t))))))))`)
	put(t, st, `(defn cat-args [] [(args (List Str))] Str (join-sp args))`)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"hello"}, "hello"},
		{[]string{"the", "cat"}, "the cat"},
		{[]string{`a"b`, `c\d`}, `a"b c\d`},     // quote + backslash survive escaping
		{[]string{"café", "é"}, "café é"},        // Unicode codepoints, not bytes
		{[]string{"tab\there", "x"}, "tab\there x"}, // a real tab byte round-trips
	} {
		got, err := runProgram(st, "cat-args", tc.args)
		if err != nil {
			t.Fatalf("run cat-args %v: %v", tc.args, err)
		}
		if got != tc.want {
			t.Errorf("run cat-args %v = %q, want %q", tc.args, got, tc.want)
		}
	}

	// A non-program entry is refused, not run.
	if _, err := runProgram(st, "str-append", []string{"x"}); err == nil {
		t.Error("expected str-append (not a CLI entry) to be refused")
	}
	if _, err := runProgram(st, "does-not-exist", nil); err == nil {
		t.Error("expected an unknown name to be refused")
	}

	// An entry legally named `true` must resolve to its DEFINITION, not reparse as
	// a Bool literal — runProgram evaluates the resolved def, not the name string.
	put(t, st, `(defn true [] [(args (List Str))] Str "ok")`)
	if got, err := runProgram(st, "true", nil); err != nil || got != "ok" {
		t.Errorf("run entry named `true` = %q, %v; want \"ok\", nil", got, err)
	}
}
