package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	capTy, shape, ok := classifyEntry(st, &Ty{K: "fun", A: &req, B: &resp})
	if !ok || shape != shapeHandler || capTy != nil {
		t.Fatalf("plain handler: (cap=%v shape=%v ok=%v), want (nil, handler, true)", capTy, shape, ok)
	}

	// (-> {caps} (-> Request Response)) is a capability-first handler, and the
	// record must be returned so the existing wiring/confinement gates run.
	capRec := Ty{K: "record", Names: []string{"fetch"},
		Args: []Ty{{K: "fun", A: &Ty{K: "data", Hash: mustResolve(t, st, "Str")}, B: &Ty{K: "data", Hash: mustResolve(t, st, "Str")}}}}
	capTy2, shape2, ok2 := classifyEntry(st, &Ty{K: "fun", A: &capRec, B: &Ty{K: "fun", A: &req, B: &resp}})
	if !ok2 || shape2 != shapeHandlerCaps || capTy2 == nil {
		t.Fatalf("cap-first handler: (cap=%v shape=%v ok=%v), want (record, handler-with-capabilities, true)", capTy2, shape2, ok2)
	}

	// Reversed is not an entry: a Response cannot be an input.
	if _, _, ok := classifyEntry(st, &Ty{K: "fun", A: &resp, B: &req}); ok {
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
// WHAT MUTATION MAKES IT FAIL. Thirteen, each failing distinguishably rather
// than merely failing, all re-run after #141 changed how a handler is recognized
// and again after §14.3's vector gained a date-bearing field:
//
//	drop the ASCII lowercasing      X-Github-Event returns
//	drop the sort                   arrival order returns
//	drop the host entry             host vanishes (net/http hides it in r.Host)
//	build path from r.URL.Path      the escaped slash becomes a real separator
//	drop the framing filter         connection/content-length reappear
//	drop Connection-nomination      x-hop reappears
//	make the ASCII check pass       obs-text answers 200 with 0xFF as U+FFFD
//	check ASCII BEFORE exclusions   400 on a request that could be served
//	honour `Connection: host`       the authority disappears from the value
//	trim Unicode space, not OWS     a real header is hidden by NBSP padding
//	skip the method/path check      a raw 0xFF target answers 200 as U+FFFD
//	discard the body read error     a truncated message is delivered as 200
//	read the time from `Date`       received-at is the message's, not the observation
//
// The seventh is the one worth reading: the mangled byte in the failure output
// IS the information loss the rule exists to prevent. An earlier attempt at that
// mutation deleted the check outright and broke the BUILD instead — which would
// have witnessed the Go compiler, not the rule, so it was redone semantically.
//
// Four came from review rather than from running the model, and none is visible
// from any single obligation: two are RULE INTERACTIONS (the ordering of two
// correct rules, and one rule's scope against another's), one is a stdlib call
// whose contract is Unicode where the protocol is octets, and one is an error
// return that was being discarded.
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
	// Renders `received-at` as a decimal line after the headers, so the adapter
	// has a witness for REQ-TIME-IS-DATA: a backend reading the time out of a
	// Date field would produce a visibly different number.
	put(t, st, `(defn hp-digits [] [(n Int)] (List Int)
		(if (< n 10)
		    (Cons [Int] (+ 48 n) (Nil [Int]))
		    (hp-cat (hp-digits (/ n 10)) (Cons [Int] (+ 48 (% n 10)) (Nil [Int])))))`)
	put(t, st, `(defn hp-line [] [(m Str) (p Str) (h (List (Pair Str Str))) (t Int)] (List Int)
		(hp-cat (hp-bytes m)
		  (Cons [Int] 32
		    (hp-cat (hp-bytes p)
		      (Cons [Int] 10
		        (hp-cat (hp-render h) (hp-digits t)))))))`)
	put(t, st, `(defn hp-entry [] [(r Request)] Response
		(Resp 200 (Nil [(Pair Str Str)])
		  (match r ((Req m p h b t) (hp-line m p h t)))))`)

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

	// One raw exchange per call: dial, write the bytes verbatim, read back.
	// A fresh connection each time is what lets every case send its own
	// `Connection` field without one case's framing affecting the next.
	send := func(t *testing.T, wire string) (int, string) {
		t.Helper()
		var conn net.Conn
		var derr error
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if conn, derr = net.DialTimeout("tcp", addr, time.Second); derr == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if conn == nil {
			t.Fatalf("handler never accepted a connection on %s within 15s (%v)", addr, derr)
		}
		defer conn.Close()
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
		return resp.StatusCode, string(body)
	}

	// SPEC §14.3's vector, byte for byte. Arrival order is deliberately not
	// lexicographic and `X-GitHub-Event` carries the case GitHub documents.
	// The target carries %2F: net/http's r.URL.Path would deliver it DECODED as
	// `/hook/admin`, a different path and a different signature input, so this
	// escape is what distinguishes REQ-PATH-IS-RAW from "close enough".
	//
	// `X-Hop` is NOMINATED by Connection, so a conformant intermediary would
	// have stripped it and §14 requires this backend to agree — its absence
	// below is what witnesses that, and it is invisible without the nomination.
	status, body := send(t, "POST /hook%2Fadmin?attempt=2&x=%2f HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-GitHub-Event: push\r\n"+
		"Accept: */*\r\n"+
		"X-Example: first\r\n"+
		"X-Example: second\r\n"+
		"X-Hop: dropped-by-nomination\r\n"+
		"Date: Wed, 01 Jan 2025 00:00:00 GMT\r\n"+
		"Content-Type: application/json\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close, X-Hop\r\n\r\n")
	if status != 200 {
		t.Fatalf("canonical vector: status %d, want 200 (body %q)", status, body)
	}

	// `host` is asserted rather than filtered out: it participates in the
	// ordering, and net/http removes it from the header map, so its presence is
	// exactly REQ-HOST-IS-A-HEADER. `connection` and `content-length` were sent
	// on the wire above and MUST be absent here — REQ-FRAMING-FIELDS-EXCLUDED
	// is the only reason they are missing, so their absence is an assertion too.
	want := "POST /hook%2Fadmin?attempt=2&x=%2f\n" +
		"accept=*/*\n" +
		"content-type=application/json\n" +
		"date=Wed, 01 Jan 2025 00:00:00 GMT\n" +
		"host=" + addr + "\n" +
		"x-example=first\n" +
		"x-example=second\n" +
		"x-github-event=push\n"
	// The body is the header rendering followed by received-at in decimal.
	if !strings.HasPrefix(body, want) {
		t.Fatalf("Request model diverges from SPEC §14.\n got:\n%s\nwant prefix:\n%s", body, want)
	}

	// REQ-TIME-IS-DATA, witnessed rather than assumed. The request carries
	// `Date: Wed, 01 Jan 2025 00:00:00 GMT` = Unix 1735689600, so a backend
	// reading the time OUT OF THE MESSAGE reports that number; the value must
	// instead be the backend's own observation. Without a date-bearing field
	// nothing distinguishes the two — which is why §14.3's vector now carries
	// one, after blind round 10 found the obligation unwitnessed.
	digits := strings.TrimPrefix(body, want)
	got, perr := strconv.ParseInt(digits, 10, 64)
	if perr != nil {
		t.Fatalf("received-at did not render as digits: %q", digits)
	}
	if got == 1735689600 {
		t.Fatal("received-at is the Date field's value — the time was read from the message")
	}
	if d := time.Since(time.Unix(got, 0)); d < -time.Minute || d > time.Minute {
		t.Fatalf("received-at %d is %v away from now — not an observation of receipt", got, d)
	}

	// REQ-HEADER-OCTETS-ARE-ASCII. A raw 0xFF is legal obs-text (RFC 9110 §5.5)
	// and Str cannot carry it: the codepoint that would come back out is not the
	// octet that arrived. §14 requires a REFUSAL rather than a repair, because a
	// transcoded value would have the handler verify a signature over bytes that
	// never arrived — undetectable from inside the artifact.
	//
	// The 400 alone is weak evidence, since the handler could have produced it.
	// This entry point answers 200 unconditionally, so a 400 can only come from
	// the adapter; the empty body confirms the renderer never ran.
	status, body = send(t, "POST /hook HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-Obs-Text: caf\xff\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close\r\n\r\n")
	if status != 400 {
		t.Fatalf("obs-text header: status %d, want 400 (body %q)", status, body)
	}
	if strings.Contains(body, "x-obs-text") {
		t.Fatalf("obs-text header reached the handler: %q", body)
	}

	// The CONTROL for that refusal: the same shape with an ASCII value is
	// delivered. Without it, a backend that refused every request would pass the
	// check above — the measurement has to discriminate, not just fire.
	status, body = send(t, "POST /hook HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-Obs-Text: cafe\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close\r\n\r\n")
	if status != 200 || !strings.Contains(body, "x-obs-text=cafe\n") {
		t.Fatalf("ASCII control: status %d body %q, want 200 carrying x-obs-text=cafe", status, body)
	}

	// The two rules INTERACT, and neither interaction is visible from the cases
	// above. Both were found by review rather than by running the model.
	//
	// (a) Exclusion is applied BEFORE the ASCII check. obs-text inside a
	// Connection-nominated field never enters `headers`, so it never enters Str
	// and must not cause a refusal — checking first would reject a request that
	// could have been served faithfully.
	status, body = send(t, "POST /hook HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-Hop: caf\xff\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close, X-Hop\r\n\r\n")
	if status != 200 {
		t.Fatalf("obs-text in an EXCLUDED field: status %d, want 200 (body %q)", status, body)
	}
	if strings.Contains(body, "x-hop") {
		t.Fatalf("nominated field was delivered: %q", body)
	}

	// (b) Nomination cannot delete `host`. REQ-HOST-IS-A-HEADER is
	// unconditional, so `Connection: host` is ignored rather than honoured —
	// otherwise a client could remove a mandatory field from the Oath value and
	// change how the handler behaves.
	status, body = send(t, "POST /hook HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close, Host\r\n\r\n")
	if status != 200 || !strings.Contains(body, "host="+addr+"\n") {
		t.Fatalf("`Connection: host` removed the authority: status %d body %q", status, body)
	}

	// SPEC §14.2 REQ-TEXT-OCTETS-ARE-ASCII reaches the request target, not just
	// headers. A raw 0xFF in the target cannot be carried by Str.
	status, body = send(t, "POST /caf\xff HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close\r\n\r\n")
	if status != 400 {
		t.Fatalf("non-ASCII request target: status %d, want 400 (body %q)", status, body)
	}

	// PROTO-OBS-FOLD-IS-ONE-SPACE. Two leading spaces on the continuation line
	// collapse to exactly one SP. This rule was written as a REFUSAL first and
	// changed after measuring: Go's net/textproto unfolds during parsing, so the
	// adapter never sees that a fold occurred and cannot refuse it. The rule now
	// pins the count the RFC leaves open, which is the part that keeps two
	// backends agreeing.
	status, body = send(t, "POST /hook HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-Fold: one\r\n  two\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close\r\n\r\n")
	if status != 200 || !strings.Contains(body, "x-fold=one two\n") {
		t.Fatalf("obs-fold: status %d body %q, want 200 carrying `x-fold=one two`", status, body)
	}

	// PROTO-UNFRAMEABLE-IS-REFUSED: a body shorter than its declared length. The
	// connection closes after 2 of the promised 10 octets, so io.ReadAll returns
	// unexpected EOF — an error the adapter used to discard, handing the handler
	// a message that was never fully sent.
	func() {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprintf(c, "POST /hook HTTP/1.1\r\nHost: %s\r\nContent-Length: 10\r\n"+
			"Connection: close\r\n\r\nhi", addr)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			c.Close()
			return // the server may simply drop it; that is also a refusal
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		c.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("truncated body was DELIVERED: %q", b)
		}
	}()

	// (c) A nomination that is not an HTTP token is IGNORED. HTTP OWS is SP and
	// HTAB only, so NBSP around a name does not make it an option — but Go's
	// strings.TrimSpace trims Unicode whitespace and would have turned this into
	// a valid-looking `x-hop`, silently hiding a real header from the handler.
	// The header must survive.
	status, body = send(t, "POST /hook HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-Hop: visible\r\n"+
		"Content-Length: 0\r\n"+
		"Connection: close, \xc2\xa0X-Hop\xc2\xa0\r\n\r\n")
	if status != 200 || !strings.Contains(body, "x-hop=visible\n") {
		t.Fatalf("a non-token nomination suppressed a real header: status %d body %q", status, body)
	}
}

// SPEC §14.1a PROTO-TYPES-BY-IDENTITY (#141): an entry point is a handler because
// of the IDENTITY of its argument and result types, never because of the names a
// store binds them to.
//
// MUTATED ON BOTH SIDES OF THE REAL-WORLD BOUNDARY, deliberately. The easy half
// is that a qualifying entry is still recognized. The dangerous half is the other
// one: recognition is what decides whether `oath build` hands a program the real
// world, so a near-match that slipped through would be compiled as a handler and
// handed the adapter's five-field Request value it is not shaped to hold. A test
// asserting only the positive direction would pass for a matcher that accepts
// everything.
func TestHandlerRecognizedByIdentityNotByName(t *testing.T) {
	base := func() *Store {
		st := newStore(t)
		put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
		put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
		put(t, st, `(data Pair [a b] (Pair a b))`)
		return st
	}
	handler := func(st *Store, reqName, respName string) (*Ty, EntryShape, bool) {
		t.Helper()
		req := Ty{K: "data", Hash: mustResolve(t, st, reqName)}
		resp := Ty{K: "data", Hash: mustResolve(t, st, respName)}
		return classifyEntry(st, &Ty{K: "fun", A: &req, B: &resp})
	}

	// POSITIVE — the canonical declarations under names §14 never mentions, and
	// with constructors named nothing like `Req`/`Resp`. Constructor names are
	// per-alias metadata rather than identity, so they must not matter.
	t.Run("canonical types under foreign names", func(t *testing.T) {
		st := base()
		put(t, st, `(data Inbound [] (Envelope Str Str (List (Pair Str Str)) (List Int) Int))`)
		put(t, st, `(data Outbound [] (Reply Int (List (Pair Str Str)) (List Int)))`)
		if _, shape, ok := handler(st, "Inbound", "Outbound"); !ok || shape != shapeHandler {
			t.Fatalf("renamed canonical types were NOT recognized: shape=%v ok=%v", shape, ok)
		}
	})

	// NEGATIVE — each is one field, one element type, one constructor or one type
	// parameter away from the declaration, and each MUST be refused. A near-match
	// that is accepted is strictly worse than one that is rejected loudly.
	for _, tc := range []struct{ name, req, resp string }{
		{"four fields, not five",
			`(data R [] (X Str Str (List (Pair Str Str)) (List Int)))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"SIX fields, not five — the extra one is silently ignorable",
			`(data R [] (X Str Str (List (Pair Str Str)) (List Int) Int Int))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"header values are Int, not Str",
			`(data R [] (X Str Str (List (Pair Str Int)) (List Int) Int))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"body is a list of Str, not octets",
			`(data R [] (X Str Str (List (Pair Str Str)) (List Str) Int))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"headers are a bare list, not of pairs",
			`(data R [] (X Str Str (List Str) (List Int) Int))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"fields transposed: path before method is fine, but Int leads",
			`(data R [] (X Int Str (List (Pair Str Str)) (List Int) Str))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"two constructors, not one",
			`(data R [] (X Str Str (List (Pair Str Str)) (List Int) Int) (Z))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"parameterized, so not the ground type",
			`(data R [a] (X Str Str (List (Pair Str Str)) (List Int) Int))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int)))`},
		{"response has four fields",
			`(data R [] (X Str Str (List (Pair Str Str)) (List Int) Int))`,
			`(data P [] (Y Int (List (Pair Str Str)) (List Int) Int))`},
	} {
		t.Run("near-match refused: "+tc.name, func(t *testing.T) {
			st := base()
			put(t, st, tc.req)
			put(t, st, tc.resp)
			if _, kind, ok := handler(st, "R", "P"); ok {
				t.Fatalf("NEAR-MATCH ACCEPTED as %v — it would receive the real world", kind)
			}
		})
	}

	// THE NAME IS NOT THE THING. Binding the names §14 uses to a type that is not
	// the declaration must not manufacture a handler — this is the direction the
	// old by-name resolution got wrong, and the only one that hands capabilities
	// to something unprepared for them.
	t.Run("the names Request and Response bound to unrelated types", func(t *testing.T) {
		st := base()
		put(t, st, `(data Request [] (Req Str))`)
		put(t, st, `(data Response [] (Resp Int))`)
		if _, kind, ok := handler(st, "Request", "Response"); ok {
			t.Fatalf("a type NAMED Request was accepted as %v — recognition is still name-based", kind)
		}
	})

	// And the canonical declarations bound to NO name at all are still the
	// protocol types: identity does not depend on a binding existing.
	t.Run("canonical types with no name binding", func(t *testing.T) {
		st := base()
		put(t, st, `(data Inbound [] (Envelope Str Str (List (Pair Str Str)) (List Int) Int))`)
		put(t, st, `(data Outbound [] (Reply Int (List (Pair Str Str)) (List Int)))`)
		protoInit()
		req := Ty{K: "data", Hash: mustResolve(t, st, "Inbound")}
		if !isProtoData(&req, protoRequest) {
			t.Fatal("the canonical Request declaration was not recognized by identity")
		}
	})
}

// The identities SPEC §14.1a's declarations denote, pinned against the committed
// store. Computing them from constructed Defs is only sound if the declarations
// in §14.1a are the ones the corpus actually contains — otherwise the compiler
// would be recognizing a protocol nobody writes.
//
// This is also the alarm for an encoding change: §1 says encoding changes fork
// reality, and if that happens these five move. The test then fails HERE, naming
// the protocol, rather than the handler protocol quietly ceasing to match
// anything.
func TestProtocolTypeHashesMatchTheCorpus(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)

	protoInit()
	for _, tc := range []struct{ name, computed string }{
		{"Str", protoStr}, {"List", protoList}, {"Pair", protoPair},
		{"Request", protoRequest}, {"Response", protoResponse},
	} {
		got := mustResolve(t, st, tc.name)
		if got != tc.computed {
			t.Errorf("%s: the store elaborates %s, §14.1a's declaration hashes to %s",
				tc.name, got[:16], tc.computed[:16])
		}
	}
}
