package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

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

// SPEC §14 — the Request model (#122). The claim is that a backend builds ONE
// canonical Request value from an HTTP request: names ASCII-lowercased, entries
// lexicographic by lowered name, repeats preserved in arrival order and never
// joined, method and raw path verbatim.
//
// WHY A RAW SOCKET. Go's http.Client canonicalizes header keys on send and
// offers no control over cross-key order, so a client-driven test could only
// ever observe what Go chose to transmit — it would measure the IMPLEMENTATION's
// idea of a request instead of the transport's. The universe this claim
// quantifies over is "requests the wire can deliver", so the test writes the
// bytes itself: `X-GitHub-Event` in the case GitHub documents, arrival order
// deliberately NOT lexicographic, and one name repeated.
//
// WHAT MUTATION MAKES IT FAIL. Dropping the lowercasing yields `X-GitHub-Event`;
// dropping the sort yields arrival order (x-github-event first); comma-joining
// collapses the two x-example lines to one; an unstable sort or reversed repeat
// handling swaps first/second; percent-decoding or dropping the query breaks the
// path line. Each is a distinguishable failure against the vector below.
func TestHandlerRequestModelIsCanonical(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)
	// A Str is SCons over CODEPOINTS, so for the ASCII of a header field name
	// and value the codepoint sequence IS the byte sequence — this renders the
	// Request's header list into the response body without needing str-bytes.
	put(t, st, `(defn hp-bytes [] [(s Str)] (List Int)
		(match s ((SNil) (Nil [Int])) ((SCons c t) (Cons [Int] c (hp-bytes t)))))`)
	put(t, st, `(defn hp-cat [] [(a (List Int)) (b (List Int))] (List Int)
		(match a ((Nil) b) ((Cons x r) (Cons [Int] x (hp-cat r b)))))`)
	put(t, st, `(defn hp-render [] [(hs (List (Pair Str Str)))] (List Int)
		(match hs
		  ((Nil) (Nil [Int]))
		  ((Cons h rest)
		    (match h ((Pair k v)
		      (hp-cat (hp-bytes k)
		        (Cons [Int] 61 (hp-cat (hp-bytes v) (Cons [Int] 10 (hp-render rest))))))))))`)
	// method SP path LF, then one `name=value` LF per header entry.
	put(t, st, `(defn hp-entry [] [(r Request)] Response
		(Resp 200 (Nil [(Pair Str Str)])
		  (match r ((Req m p h b t)
		    (hp-cat (hp-bytes m)
		      (Cons [Int] 32 (hp-cat (hp-bytes p) (Cons [Int] 10 (hp-render h)))))))))`)

	src, err := emitProgram(st, codegenPlan(t, st, "hp-entry"))
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}

	// Claim a free port, release it, hand it to the program. A race is possible
	// and would surface as a launch failure below rather than as a wrong answer.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var conn net.Conn
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err = net.DialTimeout("tcp", addr, time.Second); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("handler never accepted a connection on %s within 15s", addr)
	}
	defer conn.Close()

	// SPEC §14.3's vector, byte for byte. Arrival order is deliberately not
	// lexicographic and `X-GitHub-Event` carries the case GitHub documents.
	// The target carries %2F: net/http's r.URL.Path would deliver it DECODED as
	// `/hook/admin`, a different path and a different signature input, so this
	// escape is what distinguishes REQ-PATH-IS-RAW from "close enough".
	wire := "POST /hook%2Fadmin?attempt=2&x=%2f HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"X-GitHub-Event: push\r\n" +
		"Accept: */*\r\n" +
		"X-Example: first\r\n" +
		"X-Example: second\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n\r\n"
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := conn.Write([]byte(wire)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// `host` is asserted rather than filtered out: it participates in the
	// ordering, and net/http removes it from the header map, so its presence is
	// exactly REQ-HOST-IS-A-HEADER. `connection` and `content-length` were sent
	// on the wire above and MUST be absent here — REQ-FRAMING-FIELDS-EXCLUDED
	// is the only reason they are missing, so their absence is an assertion too.
	want := "POST /hook%2Fadmin?attempt=2&x=%2f\n" +
		"accept=*/*\n" +
		"content-type=application/json\n" +
		"host=" + addr + "\n" +
		"x-example=first\n" +
		"x-example=second\n" +
		"x-github-event=push\n"
	if string(body) != want {
		t.Fatalf("Request model diverges from SPEC §14.\n got:\n%s\nwant:\n%s", body, want)
	}
}
