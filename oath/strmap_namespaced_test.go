package main

import (
	"strings"
	"testing"
)

// #186: a consumer who depends on the registry's PUBLISHED str-map has it bound
// only as michael/oath/str-map-* — the bare names are absent from their store. The
// emitter recognizes a native operation by object HASH, so a namespaced alias is
// the same object the bare name would be; discovery must therefore find the family
// through the namespaced names too, or native lowering silently degrades to the
// O(N) structural body. This is the falsifier: str-map bound ONLY under a
// namespace must still be recognized.
func TestStrMapDiscoveredThroughNamespacedNamesOnly(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st) // Str, List, Option, Pair, length, the Int-keyed family

	// The str-map family as one source, then qualified under a namespace and put
	// so that ONLY the namespaced names bind — exactly the shape `oath publish
	// --namespace` produces and the live registry serves.
	bare := strMapFamilySource()
	rename := map[string]string{}
	for _, n := range []string{
		"str-lt", "str-eq", "StrMap",
		"smi-lookup", "smi-insert", "smi-keys", "smi-values", "smi-merge",
		"str-map-empty", "str-map-insert", "str-map-lookup", "str-map-has",
		"str-map-keys", "str-map-values", "str-map-size", "str-map-merge",
	} {
		rename[n] = "acme/oath/" + n
	}
	if _, err := apiPut(st, qualifyNames(bare, rename), "test", ""); err != nil {
		t.Fatalf("qualified str-map failed to elaborate: %v", err)
	}

	// The premise: the bare names are ABSENT — otherwise the test proves nothing.
	if _, ok := st.Resolve("str-map-insert"); ok {
		t.Fatal("bare str-map-insert resolved; the namespaced-only premise is void")
	}
	if _, ok := st.Resolve("acme/oath/str-map-insert"); !ok {
		t.Fatal("namespaced str-map-insert did not bind; setup is broken")
	}

	// Discovery must find the family through the namespaced names.
	nc := resolveNativeContainers(st, goSetHelperNames())
	if nc.StrMapHash == "" {
		t.Fatal("str-map family NOT discovered through namespaced names (#186 regression)")
	}
	// The specific operations the emitter will meet must be registered by hash.
	for _, op := range []string{"str-map-insert", "str-map-lookup", "str-map-keys"} {
		h, ok := st.Resolve("acme/oath/" + op)
		if !ok {
			t.Fatalf("acme/oath/%s missing", op)
		}
		nop, ok := nc.Ops[h]
		if !ok {
			t.Errorf("acme/oath/%s (hash %s) not registered as a native op", op, shortHash(h))
			continue
		}
		if nop.Name != op {
			t.Errorf("acme/oath/%s registered under the wrong op name %q", op, nop.Name)
		}
	}

	// Control: the bare corpus behaviour is unchanged. A fresh store with the bare
	// family is discovered exactly as before — the fallback never fires when the
	// bare name is present.
	st2 := newStore(t)
	strMapPrelude(t, st2)
	if _, err := apiPut(st2, bare, "test", ""); err != nil {
		t.Fatalf("bare str-map failed to elaborate: %v", err)
	}
	nc2 := resolveNativeContainers(st2, goSetHelperNames())
	if nc2.StrMapHash == "" {
		t.Fatal("control: bare str-map family not discovered")
	}
	if hb, _ := st2.Resolve("str-map-insert"); nc2.Ops[hb].Name != "str-map-insert" {
		t.Error("control: bare str-map-insert not registered")
	}
}

// strMapFamilySource is the str-map model as a single source string, so a test can
// qualify it and put it under a namespace. Bodies match strMapModel; properties
// are omitted because recognition is a function of signatures, not proofs.
func strMapFamilySource() string {
	return strings.Join([]string{
		`(defn str-lt [] [(a Str) (b Str)] Bool
			(match a
				((SNil) (match b ((SNil) false) ((SCons cb rb) true)))
				((SCons ca ra) (match b ((SNil) false)
					((SCons cb rb) (if (< ca cb) true (if (== ca cb) (str-lt ra rb) false)))))))`,
		`(defn str-eq [] [(a Str) (b Str)] Bool
			(if (str-lt a b) false (if (str-lt b a) false true)))`,
		`(data StrMap [] (MkStrMap (List (Pair Str Int))))`,
		`(defn smi-lookup [] [(k Str) (m (List (Pair Str Int)))] (Option Int)
			(match m ((Nil) (None [Int]))
				((Cons p t) (match p ((Pair pk pv)
					(if (str-eq k pk) (Some [Int] pv) (if (str-lt k pk) (None [Int]) (smi-lookup k t))))))))`,
		`(defn smi-insert [] [(k Str) (v Int) (m (List (Pair Str Int)))] (List (Pair Str Int))
			(match m ((Nil) (Cons [(Pair Str Int)] (Pair [Str Int] k v) (Nil [(Pair Str Int)])))
				((Cons p t) (match p ((Pair pk pv)
					(if (str-lt k pk) (Cons [(Pair Str Int)] (Pair [Str Int] k v) m)
						(if (str-eq k pk) (Cons [(Pair Str Int)] (Pair [Str Int] k v) t)
							(Cons [(Pair Str Int)] p (smi-insert k v t)))))))))`,
		`(defn smi-keys [] [(m (List (Pair Str Int)))] (List Str)
			(match m ((Nil) (Nil [Str])) ((Cons p t) (match p ((Pair pk pv) (Cons [Str] pk (smi-keys t)))))))`,
		`(defn smi-values [] [(m (List (Pair Str Int)))] (List Int)
			(match m ((Nil) (Nil [Int])) ((Cons p t) (match p ((Pair pk pv) (Cons [Int] pv (smi-values t)))))))`,
		`(defn smi-merge [] [(xs (List (Pair Str Int))) (ys (List (Pair Str Int)))] (List (Pair Str Int))
			(match xs ((Nil) ys) ((Cons p t) (match p ((Pair k v) (smi-insert k v (smi-merge t ys)))))))`,
		`(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Str Int)])))`,
		`(defn str-map-insert [] [(k Str) (v Int) (m StrMap)] StrMap
			(match m ((MkStrMap ps) (MkStrMap (smi-insert k v ps)))))`,
		`(defn str-map-lookup [] [(k Str) (m StrMap)] (Option Int)
			(match m ((MkStrMap ps) (smi-lookup k ps))))`,
		`(defn str-map-has [] [(k Str) (m StrMap)] Bool
			(match m ((MkStrMap ps) (match (smi-lookup k ps) ((None) false) ((Some v) true)))))`,
		`(defn str-map-keys [] [(m StrMap)] (List Str) (match m ((MkStrMap ps) (smi-keys ps))))`,
		`(defn str-map-values [] [(m StrMap)] (List Int) (match m ((MkStrMap ps) (smi-values ps))))`,
		`(defn str-map-size [] [(m StrMap)] Int (match m ((MkStrMap ps) (length [(Pair Str Int)] ps))))`,
		`(defn str-map-merge [] [(a StrMap) (b StrMap)] StrMap
			(match a ((MkStrMap xs) (match b ((MkStrMap ys) (MkStrMap (smi-merge xs ys)))))))`,
	}, "\n\n")
}
