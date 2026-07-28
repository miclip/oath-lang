package main

import "testing"

// #78: ingress is an entry PROTOCOL, not a capability. A capability is outbound
// authority the program holds and could misuse (hence confinement checking);
// being called is not authority — the host owns the socket and decides when to
// invoke. So a handler needs no new capability and inherits every existing gate.
func TestHandlerEntryProtocol(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)

	reqH, _ := st.Resolve("Request")
	respH, _ := st.Resolve("Response")
	req := Ty{K: "data", Hash: reqH}
	resp := Ty{K: "data", Hash: respH}

	// (-> Request Response) is a handler, with no capability record.
	capTy, kind, ok := entryShape(st, &Ty{K: "fun", A: &req, B: &resp})
	if !ok || kind != entryHandler || capTy != nil {
		t.Fatalf("plain handler: (cap=%v kind=%v ok=%v), want (nil, handler, true)", capTy, kind, ok)
	}

	// (-> {caps} (-> Request Response)) is a capability-first handler, and the
	// record must be returned so the existing wiring/confinement gates run.
	capRec := Ty{K: "record", Names: []string{"fetch"},
		Args: []Ty{{K: "fun", A: &Ty{K: "data", Hash: mustResolve(t, st, "Str")}, B: &Ty{K: "data", Hash: mustResolve(t, st, "Str")}}}}
	capTy2, kind2, ok2 := entryShape(st, &Ty{K: "fun", A: &capRec, B: &Ty{K: "fun", A: &req, B: &resp}})
	if !ok2 || kind2 != entryHandler || capTy2 == nil {
		t.Fatalf("cap-first handler: (cap=%v kind=%v ok=%v), want (record, handler, true)", capTy2, kind2, ok2)
	}

	// Reversed is not an entry: a Response cannot be an input.
	if _, _, ok := entryShape(st, &Ty{K: "fun", A: &resp, B: &req}); ok {
		t.Fatal("(-> Response Request) was accepted as an entry protocol")
	}
}

func mustResolve(t *testing.T, st *Store, name string) string {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Fatalf("no %s in store", name)
	}
	return h
}
