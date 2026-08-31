package main

import "testing"

// friction item 5: a bare `publish` of a multi-definition file must take the batch
// path (one signed envelope per name, in dependency order) rather than the
// one-definition-per-envelope path. multiDefinition is the routing predicate.
func TestMultiDefinitionRouting(t *testing.T) {
	cases := []struct {
		label string
		src   string
		want  bool
	}{
		{"single datatype", `(data Str [] (SNil) (SCons Int Str))`, false},
		{"single defn with a property",
			`(defn foo [] [(n Int)] Int (+ n 1) (prop inc [(n Int)] (== (foo n) (+ n 1))))`, false},
		{"two datatypes — the item-5 case",
			"(data List [a] (Nil) (Cons a (List a)))\n(data Str [] (SNil) (SCons Int Str))", true},
		{"a datatype and a dependent function",
			"(data Str [] (SNil) (SCons Int Str))\n(defn len [] [(s Str)] Int (match s ((SNil) 0) ((SCons c r) (+ 1 (len r)))))", true},
		{"unparseable source is not a batch",
			`(defn oops [`, false},
		{"empty source is not a batch", ``, false},
	}
	for _, c := range cases {
		if got := multiDefinition(c.src); got != c.want {
			t.Errorf("%s: multiDefinition = %v, want %v", c.label, got, c.want)
		}
	}
}

// A batch name that also appears as another definition's binder is a collision
// that QUALIFICATION would corrupt — so it is refused when a namespace is given,
// and allowed on the bare path, where nothing is rewritten (and `put` accepts it).
func TestCollisionUnderQualification(t *testing.T) {
	// `foo` is a batch name AND `bar`'s parameter.
	forms, err := parseForms("(defn foo [] [] Int 1)\n(defn bar [] [(foo Int)] Int foo)")
	if err != nil {
		t.Fatal(err)
	}
	qualified := map[string]string{"foo": "ns/foo", "bar": "ns/bar"}
	if err := collisionUnderQualification(forms, qualified, "ns"); err == nil {
		t.Error("qualifying a closure whose name collides with a binder must be refused")
	}
	// The bare path rewrites nothing, so the same closure is admitted.
	identity := map[string]string{"foo": "foo", "bar": "bar"}
	if err := collisionUnderQualification(forms, identity, ""); err != nil {
		t.Errorf("a bare batch with a name/binder collision must be admitted: %v", err)
	}

	// A closure with no such collision qualifies cleanly.
	clean, err := parseForms("(data Str [] (SNil) (SCons Int Str))\n(defn size [] [(s Str)] Int 0)")
	if err != nil {
		t.Fatal(err)
	}
	if err := collisionUnderQualification(clean, map[string]string{"Str": "ns/Str", "size": "ns/size"}, "ns"); err != nil {
		t.Errorf("a collision-free closure must qualify cleanly: %v", err)
	}
}
