package main

import (
	"sort"
	"strings"
	"testing"
)

// strMapPrelude puts the datatypes and the Int-keyed map family every Str-keyed
// test needs, so each test differs ONLY in the str-map operations under
// examination. The Int-keyed family is present in every case deliberately: the
// claim being witnessed is that the two families coexist, and a test store
// holding only one of them could not observe a str-map admission that damaged
// the Int-keyed one.
func strMapPrelude(t *testing.T, st *Store) {
	t.Helper()
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Option [a] (None) (Some a))`)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(defn length [a] [(xs (List a))] Int
		(match xs ((Nil) 0) ((Cons h t) (+ 1 (length [a] t)))))`)

	// The Int-keyed map family, in full.
	put(t, st, `(data Map [] (MkMap (List (Pair Int Int))))`)
	put(t, st, `(defn mi-lookup [] [(k Int) (m (List (Pair Int Int)))] (Option Int)
		(match m ((Nil) (None [Int]))
			((Cons p t) (match p ((Pair pk pv)
				(if (== k pk) (Some [Int] pv) (if (< k pk) (None [Int]) (mi-lookup k t))))))))`)
	put(t, st, `(defn mi-insert [] [(k Int) (v Int) (m (List (Pair Int Int)))] (List (Pair Int Int))
		(match m ((Nil) (Cons [(Pair Int Int)] (Pair [Int Int] k v) (Nil [(Pair Int Int)])))
			((Cons p t) (match p ((Pair pk pv)
				(if (< k pk) (Cons [(Pair Int Int)] (Pair [Int Int] k v) m)
					(if (== k pk) (Cons [(Pair Int Int)] (Pair [Int Int] k v) t)
						(Cons [(Pair Int Int)] p (mi-insert k v t)))))))))`)
	put(t, st, `(defn map-empty [] [] Map (MkMap (Nil [(Pair Int Int)])))`)
	put(t, st, `(defn map-insert [] [(k Int) (v Int) (m Map)] Map (match m ((MkMap ps) (MkMap (mi-insert k v ps)))))`)
	put(t, st, `(defn map-lookup [] [(k Int) (m Map)] (Option Int) (match m ((MkMap ps) (mi-lookup k ps))))`)
	put(t, st, `(defn map-size [] [(m Map)] Int (match m ((MkMap ps) (length [(Pair Int Int)] ps))))`)
}

// strMapModel puts the STRUCTURAL model of the Str-keyed map: the lexicographic
// order str-lt, the sorted-association-list operations over it, and the thin
// StrMap wrappers the recognizer is expected to admit.
func strMapModel(t *testing.T, st *Store) {
	t.Helper()
	put(t, st, `(defn str-lt [] [(a Str) (b Str)] Bool
		(match a
			((SNil) (match b ((SNil) false) ((SCons cb rb) true)))
			((SCons ca ra) (match b ((SNil) false)
				((SCons cb rb) (if (< ca cb) true (if (== ca cb) (str-lt ra rb) false)))))))`)
	put(t, st, `(defn str-eq [] [(a Str) (b Str)] Bool
		(if (str-lt a b) false (if (str-lt b a) false true)))`)
	put(t, st, `(data StrMap [] (MkStrMap (List (Pair Str Int))))`)
	put(t, st, `(defn smi-lookup [] [(k Str) (m (List (Pair Str Int)))] (Option Int)
		(match m ((Nil) (None [Int]))
			((Cons p t) (match p ((Pair pk pv)
				(if (str-eq k pk) (Some [Int] pv) (if (str-lt k pk) (None [Int]) (smi-lookup k t))))))))`)
	put(t, st, `(defn smi-insert [] [(k Str) (v Int) (m (List (Pair Str Int)))] (List (Pair Str Int))
		(match m ((Nil) (Cons [(Pair Str Int)] (Pair [Str Int] k v) (Nil [(Pair Str Int)])))
			((Cons p t) (match p ((Pair pk pv)
				(if (str-lt k pk) (Cons [(Pair Str Int)] (Pair [Str Int] k v) m)
					(if (str-eq k pk) (Cons [(Pair Str Int)] (Pair [Str Int] k v) t)
						(Cons [(Pair Str Int)] p (smi-insert k v t)))))))))`)
	put(t, st, `(defn smi-keys [] [(m (List (Pair Str Int)))] (List Str)
		(match m ((Nil) (Nil [Str])) ((Cons p t) (match p ((Pair pk pv) (Cons [Str] pk (smi-keys t)))))))`)
	put(t, st, `(defn smi-values [] [(m (List (Pair Str Int)))] (List Int)
		(match m ((Nil) (Nil [Int])) ((Cons p t) (match p ((Pair pk pv) (Cons [Int] pv (smi-values t)))))))`)
	put(t, st, `(defn smi-merge [] [(xs (List (Pair Str Int))) (ys (List (Pair Str Int)))] (List (Pair Str Int))
		(match xs ((Nil) ys) ((Cons p t) (match p ((Pair k v) (smi-insert k v (smi-merge t ys)))))))`)
	put(t, st, `(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Str Int)])))`)
	put(t, st, `(defn str-map-insert [] [(k Str) (v Int) (m StrMap)] StrMap
		(match m ((MkStrMap ps) (MkStrMap (smi-insert k v ps)))))`)
	put(t, st, `(defn str-map-lookup [] [(k Str) (m StrMap)] (Option Int)
		(match m ((MkStrMap ps) (smi-lookup k ps))))`)
	put(t, st, `(defn str-map-has [] [(k Str) (m StrMap)] Bool
		(match m ((MkStrMap ps) (match (smi-lookup k ps) ((None) false) ((Some v) true)))))`)
	put(t, st, `(defn str-map-keys [] [(m StrMap)] (List Str) (match m ((MkStrMap ps) (smi-keys ps))))`)
	put(t, st, `(defn str-map-values [] [(m StrMap)] (List Int) (match m ((MkStrMap ps) (smi-values ps))))`)
	put(t, st, `(defn str-map-size [] [(m StrMap)] Int (match m ((MkStrMap ps) (length [(Pair Str Int)] ps))))`)
	put(t, st, `(defn str-map-merge [] [(a StrMap) (b StrMap)] StrMap
		(match a ((MkStrMap xs) (match b ((MkStrMap ys) (MkStrMap (smi-merge xs ys)))))))`)
}

// opNames returns the recognized operation names, sorted, so a test can compare
// the ADMITTED SET rather than a count — a count cannot tell "the str-map family
// was admitted" from "the Int-keyed family was admitted twice".
func opNames(nc nativeContainers) []string {
	var out []string
	for _, op := range nc.Ops {
		out = append(out, op.Name)
	}
	sort.Strings(out)
	return out
}

func hasOp(nc nativeContainers, name string) bool {
	for _, op := range nc.Ops {
		if op.Name == name {
			return true
		}
	}
	return false
}

func strMapOpsAdmitted(nc nativeContainers) []string {
	var out []string
	for _, n := range opNames(nc) {
		if strings.HasPrefix(n, "str-map-") {
			out = append(out, n)
		}
	}
	return out
}

// TestStrMapFamilyRecognized is the POSITIVE control for every rejection test
// below: a store carrying the canonical Str-keyed family has all eight
// operations admitted, with the container/List/Pair/Option/key hashes derived
// from the operations' canonical types — and the Int-keyed family admitted
// ALONGSIDE it, untouched. Distinct names are what makes that coexistence
// possible: a store resolves one hash per name, so a single `map-insert` could
// not be both Int-keyed and Str-keyed.
func TestStrMapFamilyRecognized(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	strMapModel(t, st)

	nc := resolveNativeContainers(st, nativeOpNames())

	want := []string{"map-empty", "map-insert", "map-lookup", "map-size",
		"str-map-empty", "str-map-has", "str-map-insert", "str-map-keys",
		"str-map-lookup", "str-map-merge", "str-map-size", "str-map-values"}
	got := opNames(nc)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("admitted ops = %v, want %v", got, want)
	}

	strMap, _ := st.Resolve("StrMap")
	if nc.StrMapHash != strMap || strMap == "" {
		t.Errorf("StrMapHash = %q, want the StrMap datatype %q", nc.StrMapHash, strMap)
	}
	list, _ := st.Resolve("List")
	if nc.StrMapListHash != list {
		t.Errorf("StrMapListHash = %q, want the List datatype %q", nc.StrMapListHash, list)
	}
	pair, _ := st.Resolve("Pair")
	if nc.StrMapPairHash != pair {
		t.Errorf("StrMapPairHash = %q, want the Pair datatype %q", nc.StrMapPairHash, pair)
	}
	opt, _ := st.Resolve("Option")
	if nc.StrMapOptHash != opt {
		t.Errorf("StrMapOptHash = %q, want the Option datatype %q", nc.StrMapOptHash, opt)
	}
	str, _ := st.Resolve("Str")
	if nc.StrMapKeyHash != str {
		t.Errorf("StrMapKeyHash = %q, want the active Str %q", nc.StrMapKeyHash, str)
	}

	// The Int-keyed family's own hashes are unchanged by the new family sharing
	// its List, Pair and Option datatypes.
	if m, _ := st.Resolve("Map"); nc.MapHash != m {
		t.Errorf("MapHash = %q, want %q — admitting the Str-keyed family disturbed the Int-keyed one", nc.MapHash, m)
	}
	if nc.MapHash == nc.StrMapHash {
		t.Error("MapHash == StrMapHash; the two families collapsed onto one datatype")
	}
}

// TestStrMapRejectsIntKey witnesses that the KEY KIND is what the recognizer
// checks: a str-map-lookup taking an Int key is not the Str-keyed operation, and
// lowering it would hand a native integer to a string comparison. The family is
// fail-closed as a whole, and the Int-keyed family must survive — a rejection
// that took the other family with it would be a different defect wearing the
// same green.
func TestStrMapRejectsIntKey(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	strMapModel(t, st)
	// Repoint str-map-lookup to an Int-keyed function of the right ARITY and
	// result: only the key type differs.
	put(t, st, `(defn str-map-lookup [] [(k Int) (m StrMap)] (Option Int)
		(match m ((MkStrMap ps) (None [Int]))))`)

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 0 {
		t.Errorf("an Int-keyed str-map-lookup was admitted: %v", ops)
	}
	if nc.StrMapHash != "" || nc.StrMapKeyHash != "" {
		t.Errorf("StrMap hashes recorded for a rejected family: hash=%q key=%q", nc.StrMapHash, nc.StrMapKeyHash)
	}
	if !hasOp(nc, "map-insert") {
		t.Error("the Int-keyed family was dropped along with the rejected Str-keyed one")
	}
}

// TestStrMapRejectsMixedKeys witnesses the CONSISTENCY requirement across a
// family, distinct from the per-operation shape check above: every operation's
// key must be the same Str. Here each operation is individually well-shaped —
// str-map-has is a perfectly good Int-keyed predicate — and only the family read
// as a whole is incoherent.
func TestStrMapRejectsMixedKeys(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	strMapModel(t, st)
	put(t, st, `(defn str-map-has [] [(k Int) (m StrMap)] Bool
		(match m ((MkStrMap ps) false)))`)

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 0 {
		t.Errorf("a family mixing Str and Int keys was admitted: %v", ops)
	}
}

// TestStrMapRejectsNonStrKeyList witnesses that the CONTAINER's own shape is
// checked, not only the operations' signatures: a StrMap wrapping
// (List (Pair Str Str)) has a non-Int value, which the native representation
// cannot hold.
func TestStrMapRejectsNonStrKeyList(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	put(t, st, `(data StrMap [] (MkStrMap (List (Pair Str Str))))`)
	put(t, st, `(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Str Str)])))`)
	put(t, st, `(defn str-map-size [] [(m StrMap)] Int (match m ((MkStrMap ps) (length [(Pair Str Str)] ps))))`)

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 0 {
		t.Errorf("a StrMap over (List (Pair Str Str)) was admitted: %v", ops)
	}
}

// TestStrMapRejectsIntKeyList is the same check from the other side: the
// operations are Str-keyed and well-formed, but the datatype they share wraps
// (List (Pair Int Int)). The operations alone cannot reveal this — their key
// types are correct — so it witnesses containerShapeOK rather than ncMatch.
func TestStrMapRejectsIntKeyList(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	put(t, st, `(data StrMap [] (MkStrMap (List (Pair Int Int))))`)
	put(t, st, `(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Int Int)])))`)
	put(t, st, `(defn str-map-has [] [(k Str) (m StrMap)] Bool (match m ((MkStrMap ps) false)))`)

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 0 {
		t.Errorf("a Str-keyed family over an Int-keyed container was admitted: %v", ops)
	}
}

// TestStrMapRejectsIntKeyResult witnesses the RESULT side of the key check:
// str-map-keys returning (List Int) would have the backend materialize integers
// where the program expects strings.
func TestStrMapRejectsIntKeyResult(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	strMapModel(t, st)
	put(t, st, `(defn str-map-keys [] [(m StrMap)] (List Int) (match m ((MkStrMap ps) (smi-values ps))))`)

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 0 {
		t.Errorf("a str-map-keys returning (List Int) was admitted: %v", ops)
	}
}

// TestStrMapRequiresActiveStr witnesses that the key must be the store's ACTIVE
// Str — the datatype both backends lower as a native host string. A store whose
// `Str` names a structurally different datatype has no native string to compare,
// so the family must drop to the structural path rather than hand the runtime a
// key it cannot read. Without this the recognizer would admit keys the backend
// lowers as ordinary constructors.
func TestStrMapRequiresActiveStr(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	strMapModel(t, st)
	before := resolveNativeContainers(st, nativeOpNames())
	if len(strMapOpsAdmitted(before)) == 0 {
		t.Fatal("control failed: the family was not admitted before Str was repointed")
	}
	origStr, _ := st.Resolve("Str")

	// Repoint the NAME Str to a datatype of a different shape. The existing
	// str-map operations still reference the ORIGINAL Str by hash.
	put(t, st, `(data Str [] (SNil) (SCons Int Str) (Tombstone))`)
	if drifted, _ := st.Resolve("Str"); drifted == origStr {
		t.Fatal("repointing Str did not change its hash; the test cannot discriminate")
	}

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 0 {
		t.Errorf("a family keyed on a superseded Str was admitted: %v", ops)
	}
}

// TestStrMapClaimedByGoBackendNotLLVM pins the per-backend SCOPE (#184): the
// neutral recognizer knows the Str-keyed family, the GO backend lowers it
// natively (smap helpers) and so claims + recognizes it, while the LLVM backend
// has no str-map helper yet and so must NOT claim it — recognition is filtered by
// that claim, so a backend claiming an operation it cannot emit would prune the
// structural definition and have nothing to emit, whereas NOT claiming leaves
// str-map STRUCTURAL for LLVM (correct, O(N)). When LLVM gains a native str-map
// tree, this test is the one that must be updated deliberately.
func TestStrMapClaimedByGoBackendNotLLVM(t *testing.T) {
	for _, b := range []struct {
		name       string
		supported  map[string]bool
		wantStrMap bool // does THIS backend lower str-map NATIVELY? (#184)
	}{
		// Go lowers str-map natively (smap helpers); LLVM has no str-map helper
		// yet, so it must NOT claim it — the recognizer then leaves str-map
		// STRUCTURAL for LLVM (correct, O(N)) rather than pruning it to a helper
		// that does not exist.
		{"go", goSetHelperNames(), true},
		{"llvm", llvmSetHelperNames(), false},
	} {
		for _, n := range strMapOpNames {
			if got := b.supported[n]; got != b.wantStrMap {
				t.Errorf("%s backend claims to lower %s = %v, want %v", b.name, n, got, b.wantStrMap)
			}
		}
		// Control: both claim the Int-keyed operations, so the check above is
		// observing a real filter and not an empty map.
		if !b.supported["map-insert"] {
			t.Errorf("%s backend does not claim map-insert; the control is broken", b.name)
		}
	}

	st := newStore(t)
	strMapPrelude(t, st)
	strMapModel(t, st)
	for _, b := range []struct {
		name       string
		supported  map[string]bool
		wantStrMap bool
	}{
		{"go", goSetHelperNames(), true},
		{"llvm", llvmSetHelperNames(), false},
	} {
		nc := resolveNativeContainers(st, b.supported)
		ops := strMapOpsAdmitted(nc)
		if admitted := len(ops) != 0; admitted != b.wantStrMap {
			t.Errorf("%s backend str-map recognized = %v, want %v (ops %v)", b.name, admitted, b.wantStrMap, ops)
		}
		if !hasOp(nc, "map-insert") {
			t.Errorf("%s backend recognized no Int-keyed map operation; the control is broken", b.name)
		}
	}
}

// TestLLVMContainerHelperNamesMatchEmitter checks the two directions of the
// claim llvmContainerHelperNames makes: every name it lists is one
// emitContainerCall has a case for, and every neutral operation it does NOT
// list is one emitContainerCall refuses by name. Without this the list is a
// comment — and it replaced `nativeOpNames()`, which was exactly such an
// assumption holding only while the two tables happened to coincide.
func TestLLVMContainerHelperNamesMatchEmitter(t *testing.T) {
	claimed := llvmSetHelperNames()
	for name := range nativeOpShape {
		e := &llvmEmitter{}
		// Constructor indices are all valid, so the only refusal that can fire is
		// the unrecognized-operation default.
		e.setListNil, e.setListCons = 0, 1
		e.mapListNil, e.mapListCons = 0, 1
		e.optNone, e.optSome = 0, 1
		e.pairCtor = 0
		args := make([]string, len(nativeOpShape[name].params))
		for i := range args {
			args[i] = "%a"
		}
		_, err := e.emitContainerCall(name, args)
		refused := err != nil && strings.Contains(err.Error(), "unrecognized native container operation")
		if claimed[name] && refused {
			t.Errorf("%s is claimed as lowered but emitContainerCall refuses it", name)
		}
		if !claimed[name] && !refused {
			t.Errorf("%s is not claimed as lowered but emitContainerCall emits it (err=%v)", name, err)
		}
	}
}

// TestStrMapRejectsForeignKeyDatatype is the case ONLY the key-kind check
// catches, and it exists because mutation controls showed the other rejection
// tests were each carried by a NEIGHBOURING check rather than by the key kind:
// an Int key disagrees with the Str hash an earlier operation pinned; a
// superseded Str is caught by the container's own shape; and a foreign key is
// caught by str-map-keys' (List Str) result — whenever str-map-keys is present.
// A family may be PARTIAL, so it need not be. Here every operation agrees on a
// key that is simply not a Str, the container is the canonical
// (List (Pair Str Int)), and str-map-keys is absent: nothing but the key kind
// can see it, and a backend would hand a foreign constructor to a native string
// comparison as if it were a string.
func TestStrMapRejectsForeignKeyDatatype(t *testing.T) {
	// The rejected case: a partial family, consistently keyed on Tag.
	bad := newStore(t)
	strMapPrelude(t, bad)
	put(t, bad, `(data StrMap [] (MkStrMap (List (Pair Str Int))))`)
	put(t, bad, `(data Tag [] (MkTag Int))`)
	put(t, bad, `(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Str Int)])))`)
	put(t, bad, `(defn str-map-insert [] [(k Tag) (v Int) (m StrMap)] StrMap m)`)
	put(t, bad, `(defn str-map-lookup [] [(k Tag) (m StrMap)] (Option Int)
		(match m ((MkStrMap ps) (None [Int]))))`)
	put(t, bad, `(defn str-map-has [] [(k Tag) (m StrMap)] Bool (match m ((MkStrMap ps) false)))`)
	if ops := strMapOpsAdmitted(resolveNativeContainers(bad, nativeOpNames())); len(ops) != 0 {
		t.Errorf("a family keyed on a foreign datatype was admitted: %v", ops)
	}

	// CONTROL: the SAME partial family, differing only in the key type, IS
	// admitted — so the rejection above is the key kind and not the family being
	// partial or the store missing something.
	good := newStore(t)
	strMapPrelude(t, good)
	put(t, good, `(data StrMap [] (MkStrMap (List (Pair Str Int))))`)
	put(t, good, `(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Str Int)])))`)
	put(t, good, `(defn str-map-insert [] [(k Str) (v Int) (m StrMap)] StrMap m)`)
	put(t, good, `(defn str-map-lookup [] [(k Str) (m StrMap)] (Option Int)
		(match m ((MkStrMap ps) (None [Int]))))`)
	put(t, good, `(defn str-map-has [] [(k Str) (m StrMap)] Bool (match m ((MkStrMap ps) false)))`)
	nc := resolveNativeContainers(good, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 4 {
		t.Errorf("the Str-keyed control family was not fully admitted: %v", ops)
	}
	if str, _ := good.Resolve("Str"); nc.StrMapKeyHash != str {
		t.Errorf("StrMapKeyHash = %q, want the active Str %q", nc.StrMapKeyHash, str)
	}
}

// TestStrMapPartialFamilyRecordsKeyHash witnesses that the recorded key
// identity comes from the CONTAINER, not from an operation's signature. A
// family may be partial, and one holding only str-map-empty, str-map-size and
// str-map-merge names no key type anywhere — yet a merge helper still has to
// compare keys, so recording "" for it would hand the backend an admitted
// family with no key identity. Reading it off the validated container makes the
// hash available for every admitted family. (Found by review, not by the tests
// above, all of which use families that mention Str somewhere.)
func TestStrMapPartialFamilyRecordsKeyHash(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	put(t, st, `(data StrMap [] (MkStrMap (List (Pair Str Int))))`)
	put(t, st, `(defn str-map-empty [] [] StrMap (MkStrMap (Nil [(Pair Str Int)])))`)
	put(t, st, `(defn str-map-size [] [(m StrMap)] Int (match m ((MkStrMap ps) (length [(Pair Str Int)] ps))))`)
	put(t, st, `(defn str-map-merge [] [(a StrMap) (b StrMap)] StrMap a)`)

	nc := resolveNativeContainers(st, nativeOpNames())
	if ops := strMapOpsAdmitted(nc); len(ops) != 3 {
		t.Fatalf("the partial family was not admitted: %v", ops)
	}
	str, _ := st.Resolve("Str")
	if nc.StrMapKeyHash != str {
		t.Errorf("StrMapKeyHash = %q, want the active Str %q — a family that names no key type "+
			"was admitted without a key identity", nc.StrMapKeyHash, str)
	}
}

// TestIntMapKeyHashIsNotAStrHash is the discriminating control for
// containerKeyHash: the Int-keyed family shares the container SHAPE (a List of
// Pairs) with the Str-keyed one, so a key derivation that ignored the key type
// would happily report a hash for it too. Int is a primitive and carries no
// datatype hash, and the Str-keyed field must stay empty for a store that has
// no Str-keyed family at all.
func TestIntMapKeyHashIsNotAStrHash(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st) // Int-keyed family only; no StrMap defined
	nc := resolveNativeContainers(st, nativeOpNames())
	if !hasOp(nc, "map-insert") {
		t.Fatal("the Int-keyed family was not admitted; the control is broken")
	}
	if nc.StrMapHash != "" || nc.StrMapKeyHash != "" {
		t.Errorf("a store with no Str-keyed family recorded StrMap hashes: hash=%q key=%q",
			nc.StrMapHash, nc.StrMapKeyHash)
	}
	if got := containerKeyHash(st, nc.MapHash); got != "" {
		t.Errorf("containerKeyHash on the Int-keyed Map = %q, want \"\" (Int is a primitive "+
			"and has no datatype hash)", got)
	}
}
