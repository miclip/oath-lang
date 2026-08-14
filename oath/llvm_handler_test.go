package main

// THE HANDLER PROTOCOL ON THE SECOND BACKEND (#115, SPEC §14).
//
// The LLVM backend refused `(-> Request Response)` outright until now, so every
// obligation in §14 was carried by exactly one implementation. Two backends
// producing the SAME Request value from the same HTTP request is §14.0's entire
// obligation, and it is not evaluable from one — which is why the vector below
// is the one handler_test.go already sends to the Go backend rather than a
// convenient variation of it.
//
// WHAT EACH TEST HERE IS FOR, since several look like restatements of one
// another and are not:
//
//	the IR shape          main HANDS OFF to the serve loop and builds no argv
//	the caps refusal      the subset boundary that did NOT move
//	the request model     §14.2's transformation, end to end over a socket
//	invocation suppressed a refused request never reaches Oath code
//	refusal disposition   500, a named line, and a server that keeps serving
//	the release order     the arena outlives serialisation and not one line more
//	the refusal boundary  the continuation is armed per request and cleared
//	                      before it jumps
//
// The last three assert over the emitted C rather than over behaviour, and each
// carries its own CONTROL MUTANT: the checker is run against a source with the
// defect injected and must report it. A structural check nobody has watched
// fail is a hypothesis, and for these three the behavioural version cannot
// discriminate — reading freed arena memory usually returns the old bytes, so a
// released-too-early response would serve correctly on most runs and the test
// would be measuring luck.

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

// llvmHandlerSrc is the protocol's types plus two entry points: a RENDERER,
// which writes the Request back out so §14.2's transformation can be read off
// the response, and a DIVIDER, whose status is computed from a body byte so a
// remote party can drive it into a runtime refusal.
//
// The renderer answers 200 UNCONDITIONALLY. That is what makes every 400 below
// evidence about the adapter: an implementation that invoked the handler on a
// refused request would answer 200, whatever else it got right.
const llvmHandlerSrc = `
(data Str [] (SNil) (SCons Int Str))
(data List [a] (Nil) (Cons a (List a)))
(data Pair [a b] (Pair a b))
(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))
(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))

(defn lh-bytes [] [(s Str)] (List Int)
  (match s ((SNil) (Nil [Int])) ((SCons c t) (Cons [Int] c (lh-bytes t)))))

(defn lh-cat [] [(a (List Int)) (b (List Int))] (List Int)
  (match a ((Nil) b) ((Cons x r) (Cons [Int] x (lh-cat r b)))))

(defn lh-render [] [(hs (List (Pair Str Str)))] (List Int)
  (match hs
    ((Nil) (Nil [Int]))
    ((Cons h rest)
      (match h ((Pair k v)
        (lh-cat (lh-bytes k)
          (Cons [Int] 61 (lh-cat (lh-bytes v) (Cons [Int] 10 (lh-render rest))))))))))

(defn lh-digits [] [(n Int)] (List Int)
  (if (< n 10)
      (Cons [Int] (+ 48 n) (Nil [Int]))
      (lh-cat (lh-digits (/ n 10)) (Cons [Int] (+ 48 (% n 10)) (Nil [Int])))))

(defn lh-line [] [(m Str) (p Str) (h (List (Pair Str Str))) (t Int)] (List Int)
  (lh-cat (lh-bytes m)
    (Cons [Int] 32
      (lh-cat (lh-bytes p)
        (Cons [Int] 10
          (lh-cat (lh-render h) (lh-digits t)))))))

(defn lh-entry [] [(r Request)] Response
  (Resp 200 (Nil [(Pair Str Str)])
    (match r ((Req m p h b t) (lh-line m p h t))))
  (prop always-200 [(r Request)]
    (== (match (lh-entry r) ((Resp s h b) s)) 200)))

(defn lh-badname [] [(r Request)] Response
  (Resp 200 (Cons [(Pair Str Str)] (Pair [Str Str] "X Bad" "v") (Nil [(Pair Str Str)])) (Nil [Int]))
  (prop always-200 [(r Request)]
    (== (match (lh-badname r) ((Resp s h b) s)) 200)))

(defn lh-badvalue [] [(r Request)] Response
  (Resp 200 (Cons [(Pair Str Str)] (Pair [Str Str] "X-Ctl" (SCons 1 (SNil))) (Nil [(Pair Str Str)])) (Nil [Int]))
  (prop always-200 [(r Request)]
    (== (match (lh-badvalue r) ((Resp s h b) s)) 200)))

(defn lh-bigbyte [] [(r Request)] Response
  (Resp 200 (Nil [(Pair Str Str)]) (Cons [Int] 300 (Nil [Int])))
  (prop always-200 [(r Request)]
    (== (match (lh-bigbyte r) ((Resp s h b) s)) 200)))

(defn lh-204body [] [(r Request)] Response
  (Resp 204 (Nil [(Pair Str Str)]) (Cons [Int] 65 (Nil [Int])))
  (prop always-204 [(r Request)]
    (== (match (lh-204body r) ((Resp s h b) s)) 204)))

(defn lh-205body [] [(r Request)] Response
  (Resp 205 (Nil [(Pair Str Str)]) (Cons [Int] 65 (Nil [Int])))
  (prop always-205 [(r Request)]
    (== (match (lh-205body r) ((Resp s h b) s)) 205)))

(defn lh-interim [] [(r Request)] Response
  (Resp 100 (Nil [(Pair Str Str)]) (Nil [Int]))
  (prop always-100 [(r Request)]
    (== (match (lh-interim r) ((Resp s h b) s)) 100)))

(defn lh-utf8hdr [] [(r Request)] Response
  (Resp 200 (Cons [(Pair Str Str)] (Pair [Str Str] "X-Caf" "café") (Nil [(Pair Str Str)])) (Nil [Int]))
  (prop always-200 [(r Request)]
    (== (match (lh-utf8hdr r) ((Resp s h b) s)) 200)))

(defn lh-goodhdr [] [(r Request)] Response
  (Resp 200 (Cons [(Pair Str Str)] (Pair [Str Str] "X-Good" "v") (Nil [(Pair Str Str)])) (Nil [Int]))
  (prop always-200 [(r Request)]
    (== (match (lh-goodhdr r) ((Resp s h b) s)) 200)))

(defn lh-div [] [(r Request)] Response
  (match r ((Req m p h b t)
    (match b
      ((Nil) (Resp 204 (Nil [(Pair Str Str)]) (Nil [Int])))
      ((Cons x xs) (Resp (/ 1000 x) (Nil [(Pair Str Str)]) (Nil [Int]))))))
  (prop empty-body-is-204 [(m Str) (p Str) (h (List (Pair Str Str))) (t Int)]
    (== (match (lh-div (Req m p h (Nil [Int]) t)) ((Resp s hh b) s)) 204)))
`

// llvmHandlerCapsSrc is the shape that stays refused.
//
// It carries a property because the refusal must be reached THROUGH the build
// gate rather than instead of it: an entry with no verified property is refused
// by planProgram before any backend is asked, and a test reading that as the
// subset boundary would stay green with the boundary deleted.
const llvmHandlerCapsSrc = `
(defn lh-caps [] [(w {env (-> Str Str)}) (r Request)] Response
  (Resp 200 (Nil [(Pair Str Str)]) (match r ((Req m p h b t) b)))
  (prop always-200 [(w {env (-> Str Str)}) (r Request)]
    (== (match (lh-caps w r) ((Resp s h b) s)) 200)))
`

func llvmHandlerStore(t *testing.T) *Store {
	t.Helper()
	st := newStore(t)
	put(t, st, llvmHandlerSrc)
	return st
}

// ---------- the emitted module ----------

// A handler's main is a HANDOFF: it calls the serve loop with the entry and the
// constructor indices this store determines, and nothing else. The two negative
// halves matter as much as the call — an argv build or a print in a handler's
// main would mean the CLI protocol had leaked into a program that has neither
// an argument vector nor a single answer to print.
func TestLLVMHandlerMainIsAServeLoopHandoff(t *testing.T) {
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	prog, err := planProgram(st, "lh-entry")
	if err != nil {
		t.Fatal(err)
	}
	if prog.Shape != shapeHandler {
		t.Fatalf("lh-entry classified as %s", prog.Shape)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("emitLLVM: %v", err)
	}
	i := strings.Index(ir, "define i32 @main(")
	if i < 0 {
		t.Fatal("the emitted module has no main")
	}
	main := ir[i:]

	// The indices are DERIVED, and the assertion is against the derivation
	// rather than against 0 and 1 — writing the literals here would make this
	// test agree with a hardcoded emitter for exactly the store where both are
	// wrong.
	hc, err := handlerCtorIndices(st, prog)
	if err != nil {
		t.Fatalf("handlerCtorIndices: %v", err)
	}
	call := fmt.Sprintf("i32 %d, i32 %d, i32 %d, i32 %d, i32 %d)", hc.nilIdx, hc.consIdx, hc.pairIdx, hc.reqIdx, hc.respIdx)
	if !strings.Contains(main, "call i32 @o_serve(ptr @") || !strings.Contains(main, call) {
		t.Errorf("main does not hand off to the serve loop with the derived indices.\nwant a call ending %q\ngot:\n%s", call, main)
	}
	if !strings.Contains(ir, "declare i32 @o_serve(") {
		t.Error("the module calls @o_serve without declaring it, so it is not assemblable")
	}
	if !strings.Contains(llvmRuntimeC, "int o_serve(OCode entry,") {
		t.Error("the runtime does not define o_serve, so a module calling it fails at link")
	}
	if strings.Contains(main, "@o_argv(") {
		t.Error("a handler's main builds argv; a handler has no argument vector, and building " +
			"one would hand the entry a value of the wrong type")
	}
	if strings.Contains(main, "@o_print(") {
		t.Error("a handler's main prints; a handler answers a connection per request and has " +
			"no single answer to serialise to stdout")
	}
	// The provenance record is a property of the ARTIFACT, not of the CLI
	// protocol, so a handler carries it too. This is the half a second copy of
	// the module assembly would have quietly dropped.
	if !strings.Contains(ir, "@oath_provenance = constant") || !strings.Contains(ir, "@llvm.used") {
		t.Error("a handler module carries no anchored provenance manifest")
	}
}

// The subset boundary that did NOT move, and it is refused for a stated reason
// rather than for want of code: a capability record is resolved once before the
// listener binds, and this runtime has only the per-request arena to put it in.
func TestLLVMRefusesACapabilityFirstHandler(t *testing.T) {
	st := llvmHandlerStore(t)
	put(t, st, llvmHandlerCapsSrc)
	markVerified(t, st, "lh-caps")
	prog, err := planProgram(st, "lh-caps")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	if prog.Shape != shapeHandlerCaps {
		t.Fatalf("lh-caps classified as %s", prog.Shape)
	}
	_, err = emitLLVM(st, prog)
	if err == nil {
		t.Fatal("the LLVM backend compiled a capability-first handler")
	}
	if r, ok := refusedFor(err); !ok || r != reasonHandlerProtocol {
		t.Errorf("refused as %v, want %s", r, reasonHandlerProtocol)
	}
	// THE REASON MUST BE TRUE, not merely typed. The old refusal said handler
	// lowering was unwritten, and that sentence became false the moment the
	// plain handler compiled — a refusal that describes a state the backend has
	// left is worse than a terse one, because it is confidently followed.
	msg := err.Error()
	if strings.Contains(msg, "not implemented") || strings.Contains(msg, "emits no request") {
		t.Errorf("the refusal still claims handler lowering is unwritten: %q", msg)
	}
	for _, want := range []string{"arena", "before the listener binds"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say why: %q does not mention %q", msg, want)
		}
	}

	// CONTROL: the same program without the record compiles, so the refusal is
	// about the capability record and not about handlers.
	markVerified(t, st, "lh-entry")
	plain, err := planProgram(st, "lh-entry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emitLLVM(st, plain); err != nil {
		t.Fatalf("the plain handler was refused too, so the check above is not about caps: %v", err)
	}
}

// ---------- the request model, end to end ----------

// syncBuf, the stderr sink these tests read while the child is still writing to
// it, is refusal_exit_test.go's — one definition, for the reason its comment
// gives: reading a bare bytes.Buffer there is a data race, and an assertion
// ABOUT stderr that races can pass by missing the line rather than by the line
// being absent.

// llvmServe starts a compiled handler on a free port and returns its address
// and its stderr. The port is claimed and released rather than guessed: a fixed
// port makes the test fail for whatever else is listening, which reads exactly
// like the backend being broken.
func llvmServe(t *testing.T, bin string) (string, *syncBuf) {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	errs := &syncBuf{}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr)
	cmd.Stderr = errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// Wait for OBSERVABLE STARTUP rather than for a duration. The runtime
	// prints its line after bind and listen have both succeeded, so a dial that
	// connects is a server that is actually accepting.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return addr, errs
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the handler never accepted a connection on %s.\nstderr:\n%s", addr, errs.String())
	return "", nil
}

// llvmSend writes raw wire bytes and reads the response.
//
// A RAW SOCKET, DELIBERATELY, for the reason handler_test.go gives: an
// http.Client canonicalizes header names on send and gives no control over the
// order of distinct names, so a client-driven test would measure the client's
// idea of a request rather than the wire's — and header-name case and cross-key
// order are two of the things §14.2 is about.
func llvmSend(t *testing.T, addr, raw string) (int, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := io.WriteString(conn, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The response to a HEAD carries a Content-Length and no body, and a reader
	// that does not know the method would block waiting for octets HTTP forbids
	// — so the method is handed to it rather than inferred from the framing,
	// which is the very thing under test here.
	var hint *http.Request
	if strings.HasPrefix(raw, "HEAD ") {
		hint = &http.Request{Method: "HEAD"}
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), hint)
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

// awaitStderr waits for a line to reach the child's stderr, and it exists
// because A RESPONSE AND A STDERR LINE ARE NOT ORDERED FOR THE READER.
//
// The child writes the diagnostic before it writes the 500 — stderr is unbuffered
// in C — but the line then travels child → pipe → os/exec's copy goroutine →
// syncBuf, and that last hop is asynchronous. A test holding the 500 in its hand
// has no guarantee the copier has run, so a single read is a coin flip decided
// by the scheduler. Under six busy cores this failed 2 runs in 40; on an idle
// machine it passed 25 consecutive times, which is exactly how a race of this
// shape hides until CI is loaded.
//
// IT STILL FAILS WHEN THE LINE IS ABSENT — bounded, not indefinite — and the
// control below proves that rather than assuming it. A waiter that could only
// pass would be the worse defect: this assertion's siblings elsewhere in the
// repo assert a line is ABSENT, and there the same race passes by missing the
// line rather than by the line being missing.
func awaitStderr(errs *syncBuf, want string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if strings.Contains(errs.String(), want) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// SPEC §14.2, over a real socket. This is the same vector handler_test.go sends
// to the Go backend, and the same expected header list — which is the point:
// §14.0's obligation is that two backends produce the SAME Request value, and a
// vector chosen to suit this backend could not witness it.
func TestLLVMHandlerRequestModelIsCanonical(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")
	addr, _ := llvmServe(t, bin)

	status, body := llvmSend(t, addr, "POST /hook%2Fadmin?attempt=2&x=%2f HTTP/1.1\r\n"+
		"Host: "+addr+"\r\n"+
		"X-GitHub-Event: push\r\n"+
		"Accept:   text/html, */*\r\n"+
		"X-Example: first\r\n"+
		"X-Example: second\r\n"+
		"X-Ab: two\r\n"+
		"X-A: one\r\n"+
		"X-Hop: dropped-by-nomination\r\n"+
		"Date: Wed, 01 Jan 2025 00:00:00 GMT\r\n"+
		"Content-Type: application/json\r\n"+
		"Content-Length: 2\r\n"+
		"Connection: close, X-Hop\r\n\r\nhi")
	if status != 200 {
		t.Fatalf("the delivered vector answered %d, not 200: %q", status, body)
	}
	want := "POST /hook%2Fadmin?attempt=2&x=%2f\n" +
		"accept=text/html, */*\n" +
		"content-type=application/json\n" +
		"date=Wed, 01 Jan 2025 00:00:00 GMT\n" +
		"host=" + addr + "\n" +
		"x-a=one\n" +
		"x-ab=two\n" +
		"x-example=first\n" +
		"x-example=second\n" +
		"x-github-event=push\n"
	if !strings.HasPrefix(body, want) {
		t.Fatalf("the Request value is not §14.2's.\nwant prefix:\n%s\ngot:\n%s", want, body)
	}

	// REQ-TIME-IS-DATA. The message carries Date: 1 Jan 2025, which is Unix
	// 1735689600; received-at must be the backend's own OBSERVATION, so a
	// backend reading the time out of the message is caught rather than merely
	// unlikely.
	digits := strings.TrimPrefix(body, want)
	at, err := strconv.ParseInt(strings.TrimSpace(digits), 10, 64)
	if err != nil {
		t.Fatalf("received-at is not a decimal: %q", digits)
	}
	if at == 1735689600 {
		t.Error("received-at is the Date field's value, so the time came from the message")
	}
	if d := time.Since(time.Unix(at, 0)); d > time.Minute || d < -time.Minute {
		t.Errorf("received-at is %v away from now, so it is not an observation of receipt", d)
	}

	// The body is octets, one Int per BYTE. Asserted through the divider below
	// rather than here, where the renderer does not print it.
}

// EVERY REFUSAL ROW THAT A WIRE VECTOR CAN REACH, and the pair that shows the
// ORDER of the rules rather than each rule alone.
//
// The handler answers 200 for every request it is given, so each 400 here is a
// claim about the adapter — and the exclusion case is the witness §14.3.2 asks
// for: a backend validating field values BEFORE applying exclusions refuses
// both, and fails the second.
func TestLLVMHandlerRefusesWithoutInvokingTheHandler(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")
	addr, _ := llvmServe(t, bin)

	base := "Host: " + addr + "\r\nContent-Length: 0\r\n"
	for _, tc := range []struct {
		name, raw string
		want      int
	}{
		{"row 9: an obs-text octet in a field value",
			"POST /hook HTTP/1.1\r\n" + base + "X-Note: caf\xff\r\n\r\n", 400},
		{"row 9: the request target",
			"POST /caf\xff HTTP/1.1\r\n" + base + "\r\n", 400},
		{"row 22: more than one Host field line",
			"POST /hook HTTP/1.1\r\nHost: " + addr + "\r\nHost: elsewhere\r\nContent-Length: 0\r\n\r\n", 400},
		{"row 27: an HTTP/1.1 request with no Host",
			"POST /hook HTTP/1.1\r\nContent-Length: 0\r\n\r\n", 400},
		{"row 24: no colon in a field line",
			"POST /hook HTTP/1.1\r\n" + base + "X-Broken\r\n\r\n", 400},
		{"row 24: SP before a field line's colon",
			"POST /hook HTTP/1.1\r\n" + base + "X-A : v\r\n\r\n", 400},
		{"row 24: a field name that is not a token",
			"POST /hook HTTP/1.1\r\n" + base + "X(Bad: v\r\n\r\n", 400},
		{"row 24: a malformed request line",
			"POST /hook\r\n" + base + "\r\n", 400},
		// A LINE LIMIT MUST BOUND THE LINE, not only the request for more
		// octets. This one arrives with its terminator inside a single read, so
		// a bound checked on the growth path alone never sees it.
		{"row 24: a request target in none of HTTP's four forms",
			"GET relative HTTP/1.1\r\n" + base + "\r\n", 400},
		// An absolute-form target whose AUTHORITY is malformed is a malformed
		// request line, not a target this backend happens to dislike — Go's
		// parser refuses the same one, and row 3 would otherwise LIFT the
		// nonsense into the value as the host entry.
		{"row 24: an absolute-form target with a non-numeric port",
			"GET http://host:bad/path HTTP/1.1\r\n" + base + "\r\n", 400},
		{"row 24: a percent that is not an escape",
			"GET /%zz HTTP/1.1\r\n" + base + "\r\n", 400},
		{"row 24: a truncated percent escape",
			"GET /path%2 HTTP/1.1\r\n" + base + "\r\n", 400},
		{"row 5: a Host value that is not an authority",
			"GET /hook HTTP/1.1\r\nHost: a b\r\nContent-Length: 0\r\n\r\n", 400},
		// An IP-literal is IPv6address OR IPvFuture, and the malformed ones are
		// THE STRUCTURAL HOST CASES MOVED OUT OF THIS LIST, and the reason is a
		// measurement rather than a preference. They asserted 400 for values like
		// [1::2::3] and [v1.] on the argument that "the property is structural, so
		// no set of legal characters can express it — which is why the validator
		// parses rather than filters." That argument is internally sound and
		// externally wrong: SPEC 14.2 row 5's disposition is LIFT, it asks for no
		// validation at all, and net/http DELIVERS every one of these to the
		// handler. Refusing them here made the two backends disagree on exactly
		// the field a handler is most likely to trust, which is what 14.0 exists
		// to prevent. They are now asserted POSITIVELY, and against the other
		// backend, in TestHostFieldIsLiftedNotValidated — a strictly tighter
		// contract than a refusal, because it pins agreement rather than one
		// backend's opinion.
		{"row 24: an absolute-form target with a malformed IPv6 authority",
			"GET http://[::::]/x HTTP/1.1\r\n" + base + "\r\n", 400},
		{"row 24: an absolute-form target with an unterminated IPv6 literal",
			"GET http://[::1/path HTTP/1.1\r\n" + base + "\r\n", 400},
		{"an overlong field line arriving whole",
			"POST /hook HTTP/1.1\r\n" + base + "X-Long: " + strings.Repeat("v", 9000) + "\r\n\r\n", 400},
	} {
		status, body := llvmSend(t, addr, tc.raw)
		if status != tc.want {
			t.Errorf("%s: answered %d, want %d (%q)", tc.name, status, tc.want, body)
		}
		// THE HANDLER WAS NOT INVOKED, which is the half the status alone does
		// not carry. The renderer's answer always begins with the method, so
		// its absence is what says no Oath code ran.
		if strings.HasPrefix(body, "POST ") {
			t.Errorf("%s: the response carries the handler's rendering, so the handler was "+
				"invoked on a request §14.2 refuses", tc.name)
		}
	}

	// CONTROL: the same request WITHOUT the offending octet is served, so the
	// refusals above are about the octet and not about the shape of the vector.
	if status, body := llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"X-Note: cafe\r\n\r\n"); status != 200 ||
		!strings.Contains(body, "x-note=cafe\n") {
		t.Errorf("the control request answered %d without the field: %q", status, body)
	}

	// A Content-Length is ANY non-empty run of digits, so a value with leading
	// zeroes is a legal frame — and a limit on the TEXT length would refuse one
	// the Go backend accepts. The body arrives and the request is served.
	if status, body := llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0000000000000000000\r\n\r\n"); status != 200 {
		t.Errorf("a Content-Length with nineteen leading zeroes answered %d, want 200: %q", status, body)
	}

	// EXCLUSION PRECEDES THE OCTET CHECK (§14.3.2's pair). The unrepresentable
	// field is nominated by Connection, so it never enters a Str and the
	// request succeeds with no x-note entry.
	status, body := llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"X-Note: caf\xff\r\nConnection: close, X-Note\r\n\r\n")
	if status != 200 {
		t.Errorf("an EXCLUDED field's octets refused the request: %d %q", status, body)
	}
	if strings.Contains(body, "x-note") {
		t.Errorf("a nominated field survived into the value: %q", body)
	}

	// HTTP SYNTAX IS NOT CONDITIONAL ON DELIVERY, and this is the case that
	// separates it from row 9. A control octet in a field value makes the
	// MESSAGE malformed (RFC 9110 5.5), so it is refused even when the field is
	// excluded — while the pair above shows obs-text in an excluded field being
	// served, because that is a question about what a Str can carry and is asked
	// after the exclusions. Two octets, two dispositions, and a backend that
	// treated them alike would fail one of these two cases whichever way it
	// chose.
	if status, body := llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"X-Hop: bad\x01\r\nConnection: close, X-Hop\r\n\r\n"); status != 400 {
		t.Errorf("a control octet in an excluded field answered %d, want 400: %q", status, body)
	}

	// A nomination cannot reach `host` (row 5 is unconditional), and a
	// non-token option is ignored rather than honoured — the NBSP-padded
	// nomination must NOT suppress the field it names.
	status, body = llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"Connection: close, Host\r\n\r\n")
	if status != 200 || !strings.Contains(body, "host="+addr+"\n") {
		t.Errorf("a Connection nomination deleted the mandatory host entry: %d %q", status, body)
	}
	status, body = llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"X-Hop: visible\r\nConnection: close, \xc2\xa0X-Hop\xc2\xa0\r\n\r\n")
	if status != 200 || !strings.Contains(body, "x-hop=visible\n") {
		t.Errorf("a non-token nomination suppressed a real header: %d %q", status, body)
	}

	// Row 12, PROTO-OBS-FOLD-IS-ONE-SPACE: the runs on BOTH sides of the fold
	// are consumed and exactly one SP remains.
	if status, body := llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"X-Fold: one  \r\n  two\r\n\r\n"); status != 200 ||
		!strings.Contains(body, "x-fold=one two\n") {
		t.Errorf("obs-fold did not become exactly one SP: %d %q", status, body)
	}

	// REPEATED AND IDENTICAL Content-Length IS ONE LENGTH (RFC 9110 8.6), and
	// net/http accepts it — so refusing outright made the Go backend serve a
	// request this one refused. Repeated and DIFFERENT stays the smuggling shape.
	if status, body := llvmSend(t, addr, "POST /hook HTTP/1.1\r\nHost: "+addr+
		"\r\nContent-Length: 0\r\nContent-Length: 0\r\n\r\n"); status != 200 {
		t.Errorf("repeated identical Content-Length answered %d, want 200: %q", status, body)
	}
	if status, _ := llvmSend(t, addr, "POST /hook HTTP/1.1\r\nHost: "+addr+
		"\r\nContent-Length: 0\r\nContent-Length: 1\r\n\r\n"); status != 400 {
		t.Errorf("conflicting Content-Length answered %d, want 400", status)
	}
	// IDENTICAL SPELLINGS, not merely equal values: net/http refuses 5 beside
	// 005, so accepting it is the reading that breaks backend agreement.
	if status, _ := llvmSend(t, addr, "POST /hook HTTP/1.1\r\nHost: "+addr+
		"\r\nContent-Length: 0\r\nContent-Length: 00\r\n\r\n"); status != 400 {
		t.Errorf("Content-Length 0 beside 00 answered %d, want 400", status)
	}

	// MANY SMALL FIELDS ARE STILL ONE REQUEST. The only bound is the byte bound;
	// a separate cap on the field COUNT refused a request the Go backend serves,
	// and the last field is the one a cap would have dropped — so it is the one
	// asserted.
	var many strings.Builder
	many.WriteString("GET /many HTTP/1.1\r\nHost: " + addr + "\r\nContent-Length: 0\r\n")
	for i := 0; i < 400; i++ {
		many.WriteString(fmt.Sprintf("X-N%03d: v\r\n", i))
	}
	many.WriteString("\r\n")
	if status, body := llvmSend(t, addr, many.String()); status != 200 ||
		!strings.Contains(body, "x-n399=v\n") || !strings.Contains(body, "x-n000=v\n") {
		t.Errorf("400 small field lines answered %d without carrying the last one: %d octets",
			status, len(body))
	}

	// AND AN ABSOLUTE-FORM TARGET WITH A GOOD AUTHORITY IS STILL LIFTED, so the
	// refusals above are about the authority being malformed rather than about
	// absolute-form targets being refused. Row 4: the target's authority beats
	// the Host field line.
	if status, body := llvmSend(t, addr,
		"GET http://lifted.example:9/hook HTTP/1.1\r\n"+base+"\r\n"); status != 200 ||
		!strings.Contains(body, "host=lifted.example:9\n") {
		t.Errorf("an absolute-form authority was not lifted over the Host line: %d %q", status, body)
	}

	// BOTH IP-LITERAL FORMS ARE AUTHORITIES. net/http accepts them, so a
	// validator that knew only IPv6 refused a host the Go backend serves —
	// different outcomes for one request, which is the one thing §14.0 asks two
	// backends not to do. Lifted verbatim into the host entry either way.
	for _, host := range []string{
		"[::1]", "[::]", "[fe80::1]", "[2001:db8::8a2e:370:7334]",
		"[1:2:3:4:5:6:7:8]", "[::ffff:192.168.0.1]",
		"[v1.fe]", "[V7.a:b]",
	} {
		if status, body := llvmSend(t, addr, "GET /hook HTTP/1.1\r\nHost: "+host+
			"\r\nContent-Length: 0\r\n\r\n"); status != 200 ||
			!strings.Contains(body, "host="+host+"\n") {
			t.Errorf("the IP-literal host %s answered %d without becoming the host entry: %q",
				host, status, body)
		}
	}

	// CONSECUTIVE FOLDS ARE CONSECUTIVE FOLDS. Row 12 says each becomes exactly
	// one SP, so two of them become two — and the second one's continuation line
	// being empty does not make it disappear. The Go backend produces the same
	// two spaces from these octets, which is the whole of §14.0's obligation.
	if status, body := llvmSend(t, addr,
		"POST /hook HTTP/1.1\r\n"+base+"X-Two: one\r\n \r\n two\r\n\r\n"); status != 200 ||
		!strings.Contains(body, "x-two=one  two\n") {
		t.Errorf("consecutive folds did not each become one SP: %d %q", status, body)
	}

	// Row 24's remaining named instance: a body shorter than its declared
	// length. The connection is half-closed after two of the ten octets, so the
	// message can never be framed.
	func() {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(20 * time.Second))
		io.WriteString(conn, "POST /hook HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 10\r\n\r\nhi")
		conn.(*net.TCPConn).CloseWrite()
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			return // a dropped connection is also a refusal
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Error("a body shorter than its declared length was delivered to the handler")
		}
	}()
}

// A REMOTE PARTY MUST NEVER BE ABLE TO END THE PROCESS (SPEC §14.2), and this
// is where that stops being a sentence. The divider's status is 200 divided by
// the first body byte, so a body of one NUL is a well-typed program reaching a
// zero divisor on input a client chose.
//
// THREE REQUESTS, AND THE ORDER IS THE ASSERTION. The first proves the server
// answers at all, so the 500 is not a server that was already broken; the
// second is the refusal; the third is the whole claim — it can only be answered
// by a process that survived the second.
func TestLLVMHandlerRefusalBecomesA500AndKeepsServing(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-div")
	bin := buildLLVM(t, st, "lh-div")
	addr, errs := llvmServe(t, bin)

	req := func(body string) (int, string) {
		return llvmSend(t, addr, "POST /d HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: "+
			strconv.Itoa(len(body))+"\r\n\r\n"+body)
	}

	if status, body := req(""); status != 204 {
		t.Fatalf("the control request answered %d, not 204: %q — the 500 below would then "+
			"say nothing about refusals", status, body)
	}
	if status, _ := req("\x00"); status != 500 {
		t.Errorf("a zero divisor reached from a request body answered %d, want 500", status)
	}
	// AND THE OPERATOR IS TOLD WHICH OPERAND REFUSED. A 500 naming nothing is
	// the silent failure this repo keeps finding; the line still goes to stderr
	// exactly as it would for a CLI program.
	if !awaitStderr(errs, "division by zero", 10*time.Second) {
		t.Errorf("the refusal was not reported on stderr; got:\n%s", errs.String())
	}
	// THE CONTROL FOR THE WAITER ITSELF. A helper that polls until a deadline
	// is worthless if it cannot report absence, and this is the one assertion
	// here whose failure mode would be silent: it would turn every stderr claim
	// into a claim about how long the test was willing to wait.
	if awaitStderr(errs, "a line this runtime never writes", 200*time.Millisecond) {
		t.Error("the stderr waiter reports a line the runtime never writes, so it cannot fail " +
			"and the assertion above is not evidence")
	}
	// THE CLAIM. A backend that exited on the refusal cannot answer this.
	if status, _ := req("\x05"); status != 200 {
		t.Errorf("the request after a refusal answered %d, want 200 (1000/5) — the refusal "+
			"ended the server, which hands a remote party the process", status)
	}
	// AND AGAIN, so "it survived once" is not mistaken for "it survives".
	if status, _ := req("\x00"); status != 500 {
		t.Errorf("a second refusal answered %d, want 500", status)
	}
	if status, _ := req("\x04"); status != 250 {
		t.Errorf("after two refusals the divider answered %d, want 250 (1000/4)", status)
	}
}

// ---------- the structural claims, each with its control mutant ----------

// serveBody returns o_serve's body from the emitted runtime.
func serveBody(src string) (string, bool) {
	i := strings.Index(src, "int o_serve(OCode entry,")
	if i < 0 {
		return "", false
	}
	j := strings.Index(src[i:], "\n}")
	if j < 0 {
		return "", false
	}
	return src[i : i+j], true
}

// releaseFollowsSerialisation is the CLAIM, as a pure function so that it can be
// run against a mutant. The universe is o_serve's body, because that is where
// the handler protocol's release points are; the CLI protocol's is in the
// emitted IR and is checked by TestEmittedMainReleasesTheArenaAfterSerialising.
func releaseFollowsSerialisation(src string) error {
	body, ok := serveBody(src)
	if !ok {
		return fmt.Errorf("o_serve is not in this source, so nothing was checked")
	}
	inv := strings.Index(body, "entry(NULL, value)")
	resp := strings.Index(body, "o_http_respond(")
	if inv < 0 || resp < 0 {
		return fmt.Errorf("o_serve neither invokes the handler nor serialises its answer")
	}
	if resp < inv {
		return fmt.Errorf("o_serve serialises before it invokes the handler")
	}
	if k := strings.Index(body[inv:resp], "o_arena_release()"); k >= 0 {
		return fmt.Errorf("o_serve releases the request arena between invoking the handler and " +
			"serialising its answer, so the response is written from freed memory")
	}
	if !strings.Contains(body[resp:], "o_arena_release()") {
		return fmt.Errorf("o_serve never releases the request arena after serialising, so the " +
			"region outlives the request and a long-running server grows per request")
	}
	return nil
}

func TestLLVMHandlerReleasesTheArenaAfterSerialising(t *testing.T) {
	if err := releaseFollowsSerialisation(llvmRuntimeC); err != nil {
		t.Errorf("the emitted runtime: %v", err)
	}
	// THE CONTROLS. Two mutants, one per failure direction, because a checker
	// that could only see one would be green for the other — and the early
	// release is precisely the one behaviour cannot catch, since freed arena
	// memory usually still reads as the answer.
	early := strings.Replace(llvmRuntimeC,
		"    o_http_respond(fd, o_now_ms()",
		"    o_arena_release();\n    o_http_respond(fd, o_now_ms()", 1)
	if early == llvmRuntimeC {
		t.Fatal("the early-release mutant could not be built, so this control did not run")
	}
	if err := releaseFollowsSerialisation(early); err == nil {
		t.Error("the check passes on a runtime that releases BEFORE serialising, so it is not " +
			"witnessing the order it asserts")
	}
	never := strings.Replace(llvmRuntimeC, "    o_arena_release();\n  }\n}", "  }\n}", 1)
	if never == llvmRuntimeC {
		t.Fatal("the never-release mutant could not be built, so this control did not run")
	}
	if err := releaseFollowsSerialisation(never); err == nil {
		t.Error("the check passes on a runtime that never releases after serialising")
	}
}

// refusalBoundaryIsWellFormed is the CLAIM behind the two static slots the
// memory-model test now permits: there is exactly one continuation, it is armed
// per request BEFORE any Oath code runs, and it is cleared before it is used.
func refusalBoundaryIsWellFormed(src string) error {
	if n := strings.Count(src, "setjmp(o_request_jump)"); n != 1 {
		return fmt.Errorf("the runtime establishes the refusal continuation %d times, want 1: "+
			"a second setjmp is a second boundary and the door cannot say which it reaches", n)
	}
	if n := strings.Count(src, "longjmp(o_request_jump"); n != 1 {
		return fmt.Errorf("the runtime raises the refusal continuation %d times, want 1: every "+
			"refusal must leave through one door or the two classes drift apart", n)
	}
	door := strings.Index(src, "static void o_refused(void) {")
	if door < 0 {
		return fmt.Errorf("o_refused is not in this source, so nothing was checked")
	}
	end := strings.Index(src[door:], "\n}")
	if end < 0 {
		return fmt.Errorf("o_refused has no end, so nothing was checked")
	}
	body := src[door : door+end]
	clear, jump := strings.Index(body, "o_request_live = 0"), strings.Index(body, "longjmp(")
	if clear < 0 || jump < 0 {
		return fmt.Errorf("o_refused neither clears the in-flight flag nor jumps")
	}
	if clear > jump {
		return fmt.Errorf("o_refused jumps before clearing the in-flight flag, so the loop can " +
			"re-enter this door while it is already disposing of a refusal")
	}
	if !strings.Contains(body, "exit(70)") {
		return fmt.Errorf("o_refused has no standalone disposition, so a refusal outside a " +
			"request would fall through instead of exiting")
	}
	// A READ THAT EXPIRED IS A TIMEOUT WHICHEVER CLOCK SAID SO, and the
	// classification must not consult the deadline: the default budget is
	// longer than the inactivity timeout, so a conditional test answers 400 —
	// "your message was malformed" — for a peer that simply stopped talking.
	if !strings.Contains(src, "if (k < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) return -2;") {
		return fmt.Errorf("an expired receive is not classified as a timeout, so a peer that " +
			"stops mid-message is told its message was malformed")
	}

	sb, ok := serveBody(src)
	if !ok {
		return fmt.Errorf("o_serve is not in this source, so nothing was checked")
	}
	arm := strings.Index(sb, "setjmp(o_request_jump)")
	live := strings.Index(sb, "o_request_live = 1")
	inv := strings.Index(sb, "entry(NULL, value)")
	resp := strings.Index(sb, "o_http_respond(")
	if arm < 0 || live < 0 || inv < 0 || resp < 0 {
		return fmt.Errorf("o_serve does not carry the whole boundary")
	}
	if !(arm < live && live < inv) {
		return fmt.Errorf("o_serve does not arm the continuation before the handler runs, so a " +
			"refusal raised by Oath code would exit rather than answer 500")
	}
	// BETWEEN THE INVOCATION AND THE SERIALISATION, not merely somewhere in the
	// body: the 400 path clears the flag too, so a scan for the first clearing
	// would be satisfied by a refusal path and say nothing about this one.
	if inv > resp || !strings.Contains(sb[inv:resp], "o_request_live = 0") {
		return fmt.Errorf("o_serve leaves the request in flight through serialisation, so a " +
			"refusal there would write a second response onto the same connection")
	}
	return nil
}

func TestRefusalBoundaryIsArmedPerRequest(t *testing.T) {
	if err := refusalBoundaryIsWellFormed(llvmRuntimeC); err != nil {
		t.Errorf("the emitted runtime: %v", err)
	}
	for _, m := range []struct {
		name, from, to string
	}{
		{"the door jumps before clearing the flag",
			"if (o_request_live) { o_request_live = 0; longjmp(o_request_jump, 1); }",
			"if (o_request_live) { longjmp(o_request_jump, 1); o_request_live = 0; }"},
		{"the loop arms nothing",
			"if (setjmp(o_request_jump) != 0) {",
			"if (0) {"},
		{"the loop stays in flight through serialisation",
			"    o_request_live = 0;\n    /* A FRESH WINDOW",
			"    /* A FRESH WINDOW"},
		{"an expired read is reported as a dead peer",
			"if (k < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) return -2;",
			"if (k < 0 && errno == 0) return -2;"},
		{"the door has no standalone disposition",
			"  exit(70);\n}\n\nstatic void o_bug(",
			"\n}\n\nstatic void o_bug("},
	} {
		mut := strings.Replace(llvmRuntimeC, m.from, m.to, 1)
		if mut == llvmRuntimeC {
			t.Errorf("the mutant %q could not be built, so this control did not run", m.name)
			continue
		}
		if err := refusalBoundaryIsWellFormed(mut); err == nil {
			t.Errorf("the check passes when %s, so it is not witnessing what it asserts", m.name)
		}
	}
}

// cFuncSpan returns the source range of a C function, from its signature to the
// closing brace in column zero. The runtime writes no other column-zero brace
// inside a function body, which is what makes this readable without a parser.
func cFuncSpan(src, sig string) (int, int, bool) {
	i := strings.Index(src, sig)
	if i < 0 {
		return 0, 0, false
	}
	j := strings.Index(src[i:], "\n}")
	if j < 0 {
		return 0, 0, false
	}
	return i, i + j + 2, true
}

// exitsAreConfinedToTheDoors is the CLAIM, as a pure function so it can be run
// against a mutant: every exit(70) in the runtime lies inside one of the three
// functions permitted to end the process.
//
// THE UNIVERSE IS THE SITE, NOT THE SPELLING. A check on the LINE — "is this
// text exactly `exit(70);`" — is satisfied by that text anywhere, which is the
// same population error this repo keeps finding: it measures what the exits
// LOOK like instead of where they LIVE, and a new refusal written as a bare
// exit is indistinguishable from the ones inside the doors.
func exitsAreConfinedToTheDoors(src string) error {
	type span struct {
		name     string
		from, to int
	}
	var doors []span
	for _, d := range []struct{ name, sig string }{
		{"o_refused", "static void o_refused(void) {"},
		{"o_bug", "static void o_bug(const char *what) {"},
		{"o_oom", "static void o_oom(void) {"},
	} {
		from, to, ok := cFuncSpan(src, d.sig)
		if !ok {
			return fmt.Errorf("%s is not in this source, so nothing was checked", d.name)
		}
		doors = append(doors, span{d.name, from, to})
	}
	for at := 0; ; {
		k := strings.Index(src[at:], "exit(70)")
		if k < 0 {
			return nil
		}
		k += at
		at = k + 1
		// Prose mentions the status; the line filter names them rather than
		// letting a comment fail the check.
		lineStart := strings.LastIndex(src[:k], "\n") + 1
		lineEnd := lineStart + strings.Index(src[lineStart:], "\n")
		line := src[lineStart:lineEnd]
		if strings.Contains(line, "the exit(70)") || strings.Contains(line, "to exit(70)") {
			continue
		}
		inside := false
		for _, d := range doors {
			if k >= d.from && k < d.to {
				inside = true
				break
			}
		}
		if !inside {
			return fmt.Errorf("an exit(70) lives outside the three doors, at %q. Route it through "+
				"o_refused (a condition a well-typed program can reach) or o_bug (a condition "+
				"that means this compiler is wrong); a bare exit at a refusal site hands a "+
				"remote party the process", strings.TrimSpace(line))
		}
	}
}

// EVERY REFUSAL LEAVES THROUGH ONE DOOR, AND A BUG LEAVES THROUGH ANOTHER. The
// classification is what has to be central: exit(70) written at a site rather
// than at a door is a refusal that cannot become a 500, and it would be a
// refusal a remote party can turn into a process exit.
func TestRuntimeExitsOnlyThroughItsTwoDoors(t *testing.T) {
	if err := exitsAreConfinedToTheDoors(llvmRuntimeC); err != nil {
		t.Errorf("the emitted runtime: %v", err)
	}
	// THE CONTROL, and it is what the first version of this check lacked. That
	// version accepted any line whose trimmed text was exactly `exit(70);`
	// WHEREVER it appeared, so a new refusal site written as a bare exit passed
	// the test that exists to forbid it — a request-triggered refusal ending the
	// server, waved through by its own guard. The check now asks WHERE, and
	// these mutants are how that is known rather than believed.
	for _, m := range []struct {
		name, from, to string
	}{
		{"a bare exit at a new refusal site",
			"static void o_str_malformed(void) {",
			"static void o_str_malformed(void) {\n  exit(70);"},
		{"a bare exit inside the serve loop",
			"    o_request_live = 1;",
			"    exit(70);\n    o_request_live = 1;"},
	} {
		mut := strings.Replace(llvmRuntimeC, m.from, m.to, 1)
		if mut == llvmRuntimeC {
			t.Errorf("the mutant %q could not be built, so this control did not run", m.name)
			continue
		}
		if err := exitsAreConfinedToTheDoors(mut); err == nil {
			t.Errorf("the check passes with %s, so it is not witnessing where an exit lives", m.name)
		}
	}
	// AND BOTH DOORS EXIST, or the check above is satisfied by a runtime that
	// simply never refuses.
	for _, door := range []string{"static void o_refused(void) {", "static void o_bug(const char *what) {"} {
		if !strings.Contains(llvmRuntimeC, door) {
			t.Errorf("the runtime has no %q", door)
		}
	}
	// A BUG IS NOT CAUGHT, which is the half that keeps the split meaningful:
	// converting a compiler defect into an orderly 500 would make it
	// indistinguishable from the host declining to do something.
	i := strings.Index(llvmRuntimeC, "static void o_bug(const char *what) {")
	j := strings.Index(llvmRuntimeC[i:], "\n}")
	if i < 0 || j < 0 {
		t.Fatal("o_bug is not readable, so this check did not run")
	}
	if strings.Contains(llvmRuntimeC[i:i+j], "longjmp") || strings.Contains(llvmRuntimeC[i:i+j], "o_request_live") {
		t.Error("o_bug consults the request boundary, so a compiler defect can be served as a 500")
	}
}

// ---------- through the real CLI ----------

// Everything above exercises the backend IN PROCESS, which cannot witness the
// claim a person actually makes: that they can write a handler, `oath put` it,
// and `oath build --backend llvm` it. This does that through the CLI into a
// SCRATCH store — the repository's own `codebase/` is never touched, and the
// binary is built from this checkout rather than reused, because a stale one
// would let this pass while exercising CLI code nobody has changed.
func TestLLVMHandlerThroughTheRealCLI(t *testing.T) {
	requireClang(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	oath := filepath.Join(dir, "oath")
	if out, err := exec.Command("go", "build", "-o", oath, ".").CombinedOutput(); err != nil {
		t.Fatalf("building the CLI from this checkout: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "handler.oath")
	if err := os.WriteFile(src, []byte(llvmHandlerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, "store")

	run := func(args ...string) (string, error) {
		cmd := exec.Command(oath, args...)
		// A SCRATCH STORE, typed per command. The repository's canonical store
		// is git-tracked and append-only, and `oath put` defaults to it.
		cmd.Env = append(os.Environ(), "OATH_STORE="+store, "OATH_AUTHOR=claude-main")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("put", src, "--new"); err != nil {
		t.Fatalf("oath put: %v\n%s", err, out)
	}
	bin := filepath.Join(dir, "handler")
	out, err := run("build", "lh-entry", "--backend", "llvm", "-o", bin)
	if err != nil {
		t.Fatalf("oath build --backend llvm refused a handler: %v\n%s", err, out)
	}
	if !strings.Contains(out, "backend: llvm-ir/1") {
		t.Errorf("the build does not name this lowering:\n%s", out)
	}
	// The manifest must call it a handler, or the artifact describes itself as
	// something a supervisor would run differently.
	if !strings.Contains(out, "handler") {
		t.Errorf("the build does not name the entry protocol:\n%s", out)
	}

	addr, _ := llvmServe(t, bin)
	status, body := llvmSend(t, addr,
		"GET /through-the-cli HTTP/1.1\r\nHost: "+addr+"\r\nX-Via: cli\r\nContent-Length: 0\r\n\r\n")
	if status != 200 {
		t.Fatalf("the CLI-built handler answered %d: %q", status, body)
	}
	for _, want := range []string{"GET /through-the-cli\n", "host=" + addr + "\n", "x-via=cli\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("the CLI-built handler did not deliver %q; got:\n%s", want, body)
		}
	}

	// AND THE REFUSED SHAPE IS STILL REFUSED THROUGH THE CLI, by name. Without
	// this the build above is equally consistent with a backend that stopped
	// refusing anything.
	capsSrc := filepath.Join(dir, "caps.oath")
	if err := os.WriteFile(capsSrc, []byte(llvmHandlerCapsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run("put", capsSrc, "--new"); err != nil {
		t.Fatalf("oath put of the capability-first handler: %v\n%s", err, out)
	}
	capsBin := filepath.Join(dir, "caps")
	out, err = run("build", "lh-caps", "--backend", "llvm", "-o", capsBin)
	if err == nil {
		t.Fatalf("the CLI compiled a capability-first handler through the LLVM backend:\n%s", out)
	}
	if !strings.Contains(out, "capability record") {
		t.Errorf("the refusal does not name what it declined:\n%s", out)
	}
	if _, err := os.Stat(capsBin); err == nil {
		t.Error("a refused build left an executable behind")
	}
	// CONTROL: the Go backend compiles the same entry, so this is a BACKEND
	// subset boundary rather than a broken program.
	if out, err := run("build", "lh-caps", "-o", filepath.Join(dir, "caps-go")); err != nil {
		t.Errorf("the Go backend refused it too, so the refusal is not about this backend: %v\n%s", err, out)
	}
}

// SPEC §14.2 row 19 discards the transfer coding, which means DECODING it — a
// backend that refused chunked would refuse a legal message, and one that
// looked for the word "chunked" anywhere in the field would hand the handler
// octets it never decoded.
//
// The divider is the witness because its STATUS is computed from the first body
// byte, so "the body arrived" and "the body arrived correct" are the same
// assertion rather than two.
func TestLLVMHandlerDecodesChunkedRequests(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-div")
	bin := buildLLVM(t, st, "lh-div")
	addr, _ := llvmServe(t, bin)

	send := func(te, framed string) (int, string) {
		return llvmSend(t, addr, "POST /d HTTP/1.1\r\nHost: "+addr+"\r\nTransfer-Encoding: "+te+
			"\r\n\r\n"+framed)
	}
	one := "1\r\n\x04\r\n0\r\n\r\n" // one octet, 0x04, then the last-chunk and an empty trailer section

	// 1000/4 is 250, so this says the octet arrived AND arrived as itself.
	if status, body := send("chunked", one); status != 250 {
		t.Errorf("a chunked body answered %d, want 250 (1000/4): %q", status, body)
	}
	// A coding name is case-insensitive (RFC 9110 5.6.2 tokens), so a legal
	// spelling must not be refused.
	if status, _ := send("Chunked", one); status != 250 {
		t.Errorf("Transfer-Encoding: Chunked answered %d, want 250 — a coding name is a token "+
			"and tokens are compared case-insensitively", status)
	}
	// A coding this backend cannot decode is 501 — a fact about the TOOL. The
	// dangerous wrong answer is 100: that would mean the compressed octets were
	// handed to the handler as its body.
	if status, _ := send("gzip, chunked", one); status != 501 {
		t.Errorf("Transfer-Encoding: gzip, chunked answered %d, want 501; a 250 would mean "+
			"undecoded octets reached the handler as its body", status)
	}
	// chunked not final: the body length cannot be determined, which is row
	// 24's unframeable message (RFC 9112 6.1).
	if status, _ := send("chunked, gzip", one); status != 400 {
		t.Errorf("Transfer-Encoding: chunked, gzip answered %d, want 400", status)
	}
	// Doubly chunked passes every test stated as "is it chunked?" — every coding
	// IS chunked and the last one is — while decoding it once would hand the
	// handler the inner framing as body octets (RFC 9112 6.1 forbids applying
	// the coding twice).
	if status, _ := send("chunked, chunked", one); status != 400 {
		t.Errorf("Transfer-Encoding: chunked, chunked answered %d, want 400", status)
	}
	// AN EMPTY ELEMENT IS REFUSED HERE AND IGNORED IN A Connection VALUE. Row 16
	// makes ignoring normative for connection OPTIONS; nothing says that about a
	// transfer coding, and net/http refuses these — so ignoring one would decode
	// a body the Go backend never delivers.
	for _, te := range []string{", chunked", "chunked,", "chunked, , chunked"} {
		if status, _ := send(te, one); status != 400 {
			t.Errorf("Transfer-Encoding: %q answered %d, want 400", te, status)
		}
	}
	// BOTH FRAMINGS AT ONCE: Transfer-Encoding OVERRIDES Content-Length and the
	// message is PROCESSED, per RFC 9112 6.3 rule 3. This asserted 400 while the
	// backend refused, and 6.1 permits either — "A server MAY reject a request
	// that contains both Content-Length and Transfer-Encoding or process such a
	// request in accordance with the Transfer-Encoding alone." What is not
	// optional is that the two backends agree: SPEC 14.0 binds them to produce
	// the same Request from the same octets, a handler's properties are proven
	// against that value, and net/http processes. So this backend processes too.
	//
	// THE REPLACEMENT PINS MORE THAN THE REFUSAL DID. 400 said only "rejected".
	// 250 is 1000/4, so it says the CHUNKED body was decoded and the octet
	// arrived as itself — the Content-Length of 1 is discarded rather than used
	// to frame a one-byte body, which is precisely the override under test.
	if status, _ := llvmSend(t, addr, "POST /d HTTP/1.1\r\nHost: "+addr+
		"\r\nTransfer-Encoding: chunked\r\nContent-Length: 1\r\n\r\n"+one); status != 250 {
		t.Errorf("Content-Length beside Transfer-Encoding answered %d, want 250 — the "+
			"Transfer-Encoding must override and the chunked body must decode", status)
	}
	// A chunk-size line long enough to overflow a signed accumulator. The
	// assertion is not the status so much as the NEXT request: an overflow that
	// produced a negative size would index outside the buffer, and what a test
	// can see of that is a server that is no longer there.
	if status, _ := send("chunked", "FFFFFFFFFFFFFFFFFFFFFFFF\r\n"); status != 413 {
		t.Errorf("an overflowing chunk size answered %d, want 413", status)
	}
	if status, _ := send("chunked", one); status != 250 {
		t.Errorf("the request after an overflowing chunk size answered %d — the server did not "+
			"survive input it was supposed to refuse", status)
	}

	// A TRAILER SECTION THAT NEVER ENDS. Only the DECODED octets are bounded by
	// the body limit; the framing and the trailers are buffered too, and a peer
	// that keeps sending resets the per-read deadline forever. The 431 is the
	// lesser half of the assertion — the request after it is the claim.
	var trailers strings.Builder
	trailers.WriteString(one[:len(one)-2]) // the last chunk, without the empty line that ends the trailers
	for i := 0; i < 1200; i++ {
		trailers.WriteString("X-Trailer: padding that is never terminated by an empty line\r\n")
	}
	if status, _ := send("chunked", trailers.String()); status != 431 {
		t.Errorf("an unbounded trailer section answered %d, want 431", status)
	}
	if status, _ := send("chunked", one); status != 250 {
		t.Errorf("the request after an unbounded trailer section answered %d — the server did "+
			"not survive it", status)
	}
}

// Row 17: the trailer section is DISCARDED. A trailer that reached the headers
// list would be a field the sender placed after the body appearing as though it
// had arrived before it — and a handler verifying a signature over the headers
// would then verify over fields the message never framed as headers.
func TestLLVMHandlerDiscardsTrailers(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")
	addr, _ := llvmServe(t, bin)

	status, body := llvmSend(t, addr, "POST /t HTTP/1.1\r\nHost: "+addr+
		"\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n0\r\nX-Trailer: late\r\n\r\n")
	if status != 200 {
		t.Fatalf("a chunked request with a trailer section answered %d: %q", status, body)
	}
	if strings.Contains(body, "x-trailer") {
		t.Errorf("a trailer entered the headers list: %q", body)
	}

	// DISCARDED IS NOT UNPARSED. A trailer that is not a field line makes the
	// message malformed, and the Go backend refuses it — so accepting it would
	// have the two backends disagree about whether a Request exists at all.
	if status, _ := llvmSend(t, addr, "POST /t HTTP/1.1\r\nHost: "+addr+
		"\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n0\r\nBadTrailer\r\n\r\n"); status != 400 {
		t.Errorf("a malformed trailer answered %d, want 400", status)
	}
	// A FRAMING FIELD IN A TRAILER describes a message that is already framed
	// (RFC 9110 6.5.1), which is the shape smuggling is built on. Go refuses it
	// before its handler runs, and row 17's discard does not admit fields HTTP
	// forbids there.
	if status, _ := llvmSend(t, addr, "POST /t HTTP/1.1\r\nHost: "+addr+
		"\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n0\r\nContent-Length: 99\r\n\r\n"); status != 400 {
		t.Errorf("a Content-Length trailer answered %d, want 400", status)
	}
}

// THE OUTBOUND SERIALISER FAILS CLOSED. §14.4 leaves a Response unconstrained by
// the specification, so these octets have been looked at by nothing before they
// reach the wire — and each of these handlers is well-typed and verified, so a
// type error is not what stands between them and an invalid HTTP message.
//
// The control is the same shape with a legal header, so a 500 here is evidence
// about the octets rather than about handlers that return headers at all.
func TestLLVMHandlerRefusesAMalformedResponse(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	for _, tc := range []struct {
		entry, why string
		want       int
	}{
		{"lh-goodhdr", "CONTROL: a legal header name and value", 200},
		// obs-text is LEGAL in a field value (RFC 9110 5.5), and the Go backend
		// emits it. Row 9's ASCII rule governs what a Str carries INTO the
		// language, where two backends must agree; the way out is HTTP's own
		// grammar, and refusing here would make the backends disagree about a
		// Response neither specification constrains.
		{"lh-utf8hdr", "a non-ASCII header value is obs-text, not a malformed one", 200},
		{"lh-badname", "a header name with a space is not a token", 500},
		{"lh-badvalue", "a header value carrying 0x01", 500},
		{"lh-bigbyte", "a body element outside 0..255", 500},
	} {
		markVerified(t, st, tc.entry)
		bin := buildLLVM(t, st, tc.entry)
		addr, _ := llvmServe(t, bin)
		status, body := llvmSend(t, addr, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0\r\n\r\n")
		if status != tc.want {
			t.Errorf("%s: %s answered %d, want %d (%q)", tc.entry, tc.why, status, tc.want, body)
		}
	}
}

// A REMOTE PARTY MUST NOT BE ABLE TO END THE PROCESS BY SENDING TOO MUCH, which
// is the availability half of the same rule the 500 disposition covers. The
// allocator's own limit cannot serve as the header limit: o_oom EXITS, so a
// policy enforced there turns an oversized request into a process exit.
//
// FOLDS, NOT FIELDS, because a continuation line adds no field — a bound stated
// in field count is satisfied by a message that folds forever, and that is
// exactly the input this test sends.
func TestLLVMHandlerRefusesAnOversizedHeaderSection(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")
	addr, _ := llvmServe(t, bin)

	var big strings.Builder
	big.WriteString("POST /big HTTP/1.1\r\nHost: " + addr + "\r\nContent-Length: 0\r\nX-Fold: a\r\n")
	for i := 0; i < 4000; i++ {
		big.WriteString(" continuation line padding to grow one field without adding another\r\n")
	}
	big.WriteString("\r\n")
	if status, body := llvmSend(t, addr, big.String()); status != 431 {
		t.Errorf("an oversized header section answered %d, want 431: %q", status, body)
	}
	// THE CLAIM. A server that exited on the oversized request cannot answer
	// this one, and the refusal above would then have been a process death
	// wearing a status code.
	if status, _ := llvmSend(t, addr,
		"GET /after HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0\r\n\r\n"); status != 200 {
		t.Errorf("the request after an oversized one answered %d — the server did not survive "+
			"input it was supposed to refuse", status)
	}
}

// THE ADAPTER OWNS FRAMING, so it owns the two rules a handler cannot state:
// a response to HEAD carries the headers and none of the body (RFC 9110 9.3.2),
// and a 1xx, 204 or 304 carries no content and no Content-Length (RFC 9110
// 6.4.1). The two are answered differently on purpose — one is a property of
// the request and is required, the other is a contradiction inside the Response
// and is refused.
func TestLLVMHandlerAppliesHTTPFramingRules(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")
	addr, _ := llvmServe(t, bin)

	// The GET first, so the length below is known to be the length of something.
	getStatus, getBody := llvmSend(t, addr,
		"GET /f HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0\r\n\r\n")
	if getStatus != 200 || len(getBody) == 0 {
		t.Fatalf("the GET control answered %d with %d octets", getStatus, len(getBody))
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	io.WriteString(conn, "HEAD /f HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "HEAD"})
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("HEAD answered %d, want 200", resp.StatusCode)
	}
	// The LENGTH is stated and the OCTETS are withheld. A backend that dropped
	// the header instead would be answering a different question.
	//
	// NOT COMPARED TO THE GET'S LENGTH, and the reason is a property of this
	// witness rather than of HTTP: the renderer echoes the request METHOD into
	// its body, so the two responses legitimately differ by the one octet
	// between GET and HEAD. Asserting equality would have pinned the handler's
	// output shape while claiming to pin the framing rule.
	cl, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	if err != nil || cl <= 0 {
		t.Errorf("HEAD reports Content-Length %q, want the length the body would have had",
			resp.Header.Get("Content-Length"))
	}
	if b, _ := io.ReadAll(resp.Body); len(b) != 0 {
		t.Errorf("HEAD returned %d octets of content, which HTTP forbids", len(b))
	}

	// AND ON THE ERROR PATHS TOO. The rule is about the METHOD, not about
	// success, so the diagnostic body the adapter would otherwise write is
	// exactly as forbidden as the handler's — and these are the paths that are
	// already reporting a problem, where invalid framing is hardest to notice.
	hc, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer hc.Close()
	hc.SetDeadline(time.Now().Add(20 * time.Second))
	io.WriteString(hc, "HEAD /f HTTP/1.1\r\nHost: "+addr+"\r\nX-Bad: caf\xff\r\nContent-Length: 0\r\n\r\n")
	hr, err := http.ReadResponse(bufio.NewReader(hc), &http.Request{Method: "HEAD"})
	if err != nil {
		t.Fatalf("HEAD refusal: %v", err)
	}
	defer hr.Body.Close()
	if hr.StatusCode != 400 {
		t.Errorf("the refused HEAD answered %d, want 400", hr.StatusCode)
	}
	if b, _ := io.ReadAll(hr.Body); len(b) != 0 {
		t.Errorf("a refused HEAD returned %d octets of content", len(b))
	}

	// A bodiless status with content is a contradiction the handler wrote, so it
	// is refused and named rather than emitted or silently truncated.
	markVerified(t, st, "lh-204body")
	bad := buildLLVM(t, st, "lh-204body")
	badAddr, _ := llvmServe(t, bad)
	if status, _ := llvmSend(t, badAddr,
		"GET / HTTP/1.1\r\nHost: "+badAddr+"\r\nContent-Length: 0\r\n\r\n"); status != 500 {
		t.Errorf("a 204 carrying content answered %d, want 500", status)
	}

	// 205 is the row a hand-written list forgets: it carries no content (RFC 9110
	// 15.3.6) and it is not adjacent to 204 in any obvious way.
	markVerified(t, st, "lh-205body")
	bad205 := buildLLVM(t, st, "lh-205body")
	a205, _ := llvmServe(t, bad205)
	if status, _ := llvmSend(t, a205,
		"GET / HTTP/1.1\r\nHost: "+a205+"\r\nContent-Length: 0\r\n\r\n"); status != 500 {
		t.Errorf("a 205 carrying content answered %d, want 500", status)
	}

	// A 1xx IS INTERIM, and this protocol has no way to say what follows it. A
	// backend that emitted it and closed would leave the client reading EOF
	// where a completed exchange was promised.
	markVerified(t, st, "lh-interim")
	interim := buildLLVM(t, st, "lh-interim")
	aInterim, _ := llvmServe(t, interim)
	if status, _ := llvmSend(t, aInterim,
		"GET / HTTP/1.1\r\nHost: "+aInterim+"\r\nContent-Length: 0\r\n\r\n"); status != 500 {
		t.Errorf("an interim 1xx status answered %d, want 500", status)
	}

	// CONTROL: an EMPTY body under the same status is legal, and must be served
	// without a Content-Length — otherwise the check above is satisfied by a
	// backend that simply refuses 204.
	markVerified(t, st, "lh-div")
	div := buildLLVM(t, st, "lh-div")
	divAddr, _ := llvmServe(t, div)
	c2, err := net.DialTimeout("tcp", divAddr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.SetDeadline(time.Now().Add(20 * time.Second))
	io.WriteString(c2, "POST /d HTTP/1.1\r\nHost: "+divAddr+"\r\nContent-Length: 0\r\n\r\n")
	r2, err := http.ReadResponse(bufio.NewReader(c2), nil)
	if err != nil {
		t.Fatalf("204 control: %v", err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != 204 {
		t.Errorf("the 204 control answered %d", r2.StatusCode)
	}
	if _, ok := r2.Header["Content-Length"]; ok {
		t.Error("a 204 carries a Content-Length, which RFC 9110 6.4.1 forbids")
	}
}

// Row 16 says the UNION of the options across every Connection line, and a union
// has no capacity. A first version collected them into a fixed array, so a
// request under the header limit could carry more options than the array held
// and the field the last one named would reach the handler — a hop-by-hop field
// delivered because a buffer filled.
func TestLLVMHandlerHonoursEveryConnectionNomination(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")
	addr, _ := llvmServe(t, bin)

	// The real nomination goes LAST, behind enough filler to overflow any
	// plausible fixed capacity, and the filler names nothing that is present so
	// it changes the value in no other way.
	var opts strings.Builder
	opts.WriteString("close")
	for i := 0; i < 400; i++ {
		opts.WriteString(fmt.Sprintf(", x-absent-%d", i))
	}
	opts.WriteString(", x-hop")
	status, body := llvmSend(t, addr, "POST /n HTTP/1.1\r\nHost: "+addr+
		"\r\nX-Hop: hop-by-hop\r\nContent-Length: 0\r\nConnection: "+opts.String()+"\r\n\r\n")
	if status != 200 {
		t.Fatalf("the nomination vector answered %d: %q", status, body)
	}
	if strings.Contains(body, "x-hop") {
		t.Errorf("a nomination past the first few was dropped, so a hop-by-hop field reached "+
			"the handler: %q", body)
	}
	// CONTROL: the same field with no nomination for it DOES reach the handler,
	// so the absence above is the nomination working rather than the field never
	// arriving.
	if status, body := llvmSend(t, addr, "POST /n HTTP/1.1\r\nHost: "+addr+
		"\r\nX-Hop: hop-by-hop\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"); status != 200 ||
		!strings.Contains(body, "x-hop=hop-by-hop\n") {
		t.Errorf("the control answered %d without the field: %q", status, body)
	}
}

// A LISTEN ADDRESS IS CONFIGURATION, AND A TYPO IN IT MUST NOT SILENTLY CHOOSE
// AN ENDPOINT. The failure exits 1 rather than 70: provisioning succeeded and
// the port did not bind, which is the distinction the webhook acceptance suite
// uses as its control that capabilities resolve BEFORE the port is bound.
func TestLLVMHandlerRefusesAMalformedListenAddress(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")

	for _, tc := range []struct{ addr, want string }{
		{"127.0.0.1:8080junk", "port"},
		// strtol repairs its own input — a leading sign or space is consumed and
		// the digits after it bound — so a validator built on it accepts
		// spellings it believes it rejected.
		{"127.0.0.1:+8080", "port"},
		{"127.0.0.1: 8080", "port"},
		{"127.0.0.1:-1", "port"},
		{"127.0.0.1:0", "port"},
		{"127.0.0.1:70000", "port"},
		{"127.0.0.1", "port"},
		{"not-a-host:8080", "IPv4"},
	} {
		// BOUNDED, because the failure this test hunts is a binary that STARTS.
		// CombinedOutput waits for exit, and a server does not exit — so the
		// unbounded form turns a regression in the address validation into a
		// suite that hangs rather than one that fails, which is the one outcome
		// a failure-path test must never have. Found by reverting the fix and
		// watching the harness stop instead of report.
		sink := &syncBuf{}
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+tc.addr)
		cmd.Stdout, cmd.Stderr = sink, sink
		if err := cmd.Start(); err != nil {
			t.Fatalf("launch: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		var err error
		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Errorf("OATH_HTTP_ADDR=%q did not exit, so it bound a listener for an address "+
				"this backend states it refuses", tc.addr)
			continue
		}
		out := []byte(sink.String())
		if err == nil {
			t.Errorf("OATH_HTTP_ADDR=%q started a server", tc.addr)
			continue
		}
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 {
			t.Errorf("OATH_HTTP_ADDR=%q exited %v, want 1 — a listen failure is not the "+
				"host-refusal status, and the difference is a live discriminator", tc.addr, err)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("OATH_HTTP_ADDR=%q did not say what was wrong (%q): %s", tc.addr, tc.want, out)
		}
	}
}

// SLOWLORIS. A per-read inactivity timeout is not a bound on a request: a client
// that sends one octet just inside it resets the clock forever, and this server
// is serial, so one such client is the whole service. The claim is that a
// request has a WALL-CLOCK budget, and it is witnessed by dribbling past one.
//
// The budget is read from the environment so that it can be witnessed at all —
// a thirty-second default cannot fire inside a test suite, and a bound nobody
// has watched fire is a hypothesis.
func TestLLVMHandlerBoundsTheWholeRequest(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	errs := &syncBuf{}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr, "OATH_HTTP_REQUEST_TIMEOUT=1")
	cmd.Stderr = errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// One octet at a time, well inside any per-read timeout and well past the
	// per-REQUEST budget. The request would be legal if it ever finished.
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	drip := "GET /slow HTTP/1.1\r\nHost: " + addr + "\r\nContent-Length: 0\r\n\r\n"
	var status int
	for i := 0; i < len(drip); i++ {
		if _, err := io.WriteString(conn, drip[i:i+1]); err != nil {
			break
		}
		time.Sleep(120 * time.Millisecond)
		if i%8 == 0 {
			// Peek for an early answer: once the budget passes, the server
			// refuses without waiting for the rest.
			conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
			if resp, err := http.ReadResponse(bufio.NewReader(conn), nil); err == nil {
				status = resp.StatusCode
				resp.Body.Close()
				break
			}
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		}
	}
	if status == 0 {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if resp, err := http.ReadResponse(bufio.NewReader(conn), nil); err == nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
	}
	if status != 408 {
		t.Errorf("a request dribbled past its budget answered %d, want 408 — a per-read "+
			"timeout that a slow peer keeps resetting is not a bound on the request", status)
	}
	// THE CLAIM: the server is still there for everyone else. Without this the
	// 408 above is equally consistent with a server that died on the slow client.
	if s, _ := llvmSend(t, addr,
		"GET /after HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0\r\n\r\n"); s != 200 {
		t.Errorf("the request after a slow client answered %d — one slow peer took the "+
			"service with it", s)
	}
}

// The deadline must bound the blocking RECEIVE, not merely the loop around it.
// A peer that sends one octet and stops is inside a recv when its budget
// expires, and a check made before the call cannot end a call already in
// progress — so the socket timeout has to be narrowed to what is left.
//
// The assertion is the ELAPSED TIME as much as the status: with the budget at
// one second and the inactivity timeout at fifteen, a backend that only checked
// before the call answers the same 408 nine seconds later.
func TestLLVMHandlerDeadlineBoundsABlockingRead(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	bin := buildLLVM(t, st, "lh-entry")

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr, "OATH_HTTP_REQUEST_TIMEOUT=1")
	cmd.Stderr = &syncBuf{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	up := time.Now().Add(15 * time.Second)
	for time.Now().Before(up) {
		if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	start := time.Now()
	io.WriteString(conn, "G") // one octet, then silence
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("no answer to a stalled request after %v: %v", elapsed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 408 {
		t.Errorf("a stalled request answered %d, want 408", resp.StatusCode)
	}
	// The inactivity timeout is 15s and the budget is 1s. Anything near the
	// former means the deadline did not reach inside the blocking call.
	if elapsed > 8*time.Second {
		t.Errorf("the stalled request was answered after %v — the budget bounds the loop but "+
			"not the recv inside it", elapsed)
	}
	if s, _ := llvmSend(t, addr,
		"GET /after HTTP/1.1\r\nHost: "+addr+"\r\nContent-Length: 0\r\n\r\n"); s != 200 {
		t.Errorf("the request after a stalled one answered %d", s)
	}
}

// EVERY PROGRAM THIS BACKEND EMITS LINKS ONE RUNTIME, CLI ENTRIES INCLUDED, so
// the socket layer is a portability question for programs that never open a
// socket. This project publishes a Windows binary of the kernel, and an
// unconditional POSIX include would have turned every LLVM build there into a
// compile failure — a regression in a program that has nothing to do with
// handlers.
//
// THE INSTRUMENT IS VERIFIED FIRST. -D_WIN32 on a POSIX host is a probe, not a
// Windows build, and on some hosts the system headers refuse it outright — in
// which case a failure here would say nothing about this runtime. The control
// compiles a trivial file the same way, and the check is skipped rather than
// reported when the control cannot pass.
func TestEmittedRuntimeCompilesWithoutTheSocketLayer(t *testing.T) {
	requireClang(t)
	dir := t.TempDir()
	trivial := filepath.Join(dir, "control.c")
	if err := os.WriteFile(trivial, []byte("#include <stdio.h>\nint main(void){return 0;}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("clang", "-D_WIN32", "-fsyntax-only", trivial).CombinedOutput(); err != nil {
		t.Skipf("this host's headers reject -D_WIN32, so the probe cannot distinguish a "+
			"portable runtime from an unusable flag: %s", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	drv := filepath.Join(dir, "drv.c")
	if err := os.WriteFile(drv, []byte("#include \"rt.c\"\nint main(void){return 0;}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("clang", "-D_WIN32", "-fsyntax-only", "drv.c")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("the emitted runtime does not compile with the socket layer excluded, so an "+
			"LLVM build of a CLI program breaks on a host without POSIX sockets:\n%s", out)
	}
	// AND THE REFUSAL IS STILL THERE. Excluding the layer must leave o_serve
	// DEFINED and refusing by name — a build that simply dropped the symbol
	// would fail at link, which is a compiler error where an honest host
	// refusal belongs.
	if !strings.Contains(llvmRuntimeC, "cannot be served here") {
		t.Error("the socket-free build has no named refusal for the handler protocol")
	}
}

// Expect: 100-continue is a HANDSHAKE, and a server that reads the body first
// deadlocks with a client that is waiting to be told to send it — a legal
// request answered 408 while both sides wait for the other. The Go backend's
// net/http performs it, so a handler behaves differently on the two backends
// unless this one does too.
//
// It is also the one INTERIM response this boundary emits, which is not a
// contradiction of refusing a 1xx from the handler: the adapter knows a final
// response is still to come because it is the party that will send it.
func TestLLVMHandlerAnswersTheContinueExpectation(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-div")
	bin := buildLLVM(t, st, "lh-div")
	addr, _ := llvmServe(t, bin)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	// The headers only. A client honouring the expectation holds its body back.
	io.WriteString(conn, "POST /d HTTP/1.1\r\nHost: "+addr+
		"\r\nExpect: 100-continue\r\nContent-Length: 1\r\n\r\n")
	br := bufio.NewReader(conn)
	interim := make([]byte, 0, 64)
	for !strings.HasSuffix(string(interim), "\r\n\r\n") {
		b, err := br.ReadByte()
		if err != nil {
			t.Fatalf("no interim response, so the server was waiting for a body the client "+
				"was waiting to be asked for: %v", err)
		}
		interim = append(interim, b)
		if len(interim) > 512 {
			break
		}
	}
	if !strings.Contains(string(interim), "100 Continue") {
		t.Fatalf("the expectation was not answered with an interim 100: %q", interim)
	}
	// NOW the body, and the final response must be the handler's.
	io.WriteString(conn, "\x04")
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("final response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 250 {
		t.Errorf("the final response was %d, want 250 (1000/4)", resp.StatusCode)
	}

	// EVERY Expect FIELD IS INSPECTED BEFORE ANY INTERIM IS SENT. Stopping at the
	// first recognised one would send 100 and then proceed as though a later
	// expectation this backend does not support had been honoured.
	if status, _ := llvmSend(t, addr, "POST /d HTTP/1.1\r\nHost: "+addr+
		"\r\nExpect: 100-continue\r\nExpect: something-else\r\nContent-Length: 0\r\n\r\n"); status != 417 {
		t.Errorf("an unsupported expectation behind a supported one answered %d, want 417", status)
	}

	// AN UNKNOWN EXPECTATION IS REFUSED BY NAME rather than ignored: ignoring it
	// would have the client believe a condition it stated was honoured.
	if status, _ := llvmSend(t, addr, "POST /d HTTP/1.1\r\nHost: "+addr+
		"\r\nExpect: something-else\r\nContent-Length: 0\r\n\r\n"); status != 417 {
		t.Errorf("an unknown expectation answered %d, want 417", status)
	}

	// CONTROL: the same request with no expectation is served normally, so the
	// two answers above are about the expectation rather than about the vector.
	if status, _ := llvmSend(t, addr, "POST /d HTTP/1.1\r\nHost: "+addr+
		"\r\nContent-Length: 0\r\n\r\n"); status != 204 {
		t.Errorf("the control answered %d, want 204", status)
	}
}

// THE HOST FIELD IS LIFTED, NOT VALIDATED — AND BOTH BACKENDS LIFT IT THE SAME.
//
// SPEC §14.2 row 5's disposition is LIFT: the field "becomes the `host` entry;
// never remains a field line". It asks for no structural validation, and §14.0
// requires the two backends to agree about whether a Request exists and what it
// contains.
//
// This replaces a list of 400 assertions that refused malformed IP-literals on
// their own grammar. Those were a rejection the specification never requested,
// and they DIVERGED: net/http delivers every value below to the handler. The
// replacement is tighter than what it replaces, because a refusal pins one
// backend's opinion while this pins the AGREEMENT that is the actual guarantee.
//
// Measured against net/http before it was written — the character set it does
// enforce is thirteen printable ASCII bytes plus DEL and above, which is why
// `a b` still refuses in the list above and none of these do.
func TestHostFieldIsLiftedNotValidated(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	llBin := buildLLVM(t, st, "lh-entry")
	goBin, _ := buildProgram(t, st, "lh-entry")
	llAddr, _ := llvmServe(t, llBin)
	goAddr, _ := llvmServe(t, goBin)

	for _, host := range []string{
		"[1::2::3]",           // two elisions
		"[::::]",              // only colons
		"[v1.]",               // IPvFuture, empty address part
		"[v.fe]",              // IPvFuture, no version digits
		"[1:2:3:4:5:6:7:8:9]", // nine groups
		"[12345::1]",          // five-digit group
		"[1:2:3]",             // too few groups, no elision
		"[::ffff:999.1.1.1]",  // out-of-range dotted tail
	} {
		t.Run(host, func(t *testing.T) {
			raw := "GET /hook HTTP/1.1\r\nHost: " + host + "\r\nContent-Length: 0\r\n\r\n"
			llStatus, llBody := llvmSend(t, llAddr, raw)
			goStatus, goBody := llvmSend(t, goAddr, raw)

			if llStatus != goStatus {
				t.Errorf("the backends disagree on the STATUS for Host %q: llvm=%d go=%d — "+
					"§14.0 asks them to agree about whether a Request exists", host, llStatus, goStatus)
			}
			if llBody != goBody {
				t.Errorf("the backends disagree on the LIFTED VALUE for Host %q:\n  llvm=%q\n  go=%q\n"+
					"row 5 says the field becomes the host entry, so a difference here is a "+
					"difference in the Request itself", host, llBody, goBody)
			}
			// AND IT REACHED THE HANDLER. Without this, two backends that both
			// REFUSED would "agree" and this test would pass while the defect it
			// exists to catch was present in both.
			if llStatus == 400 {
				t.Errorf("Host %q was refused rather than lifted; row 5's disposition is LIFT "+
					"and it requests no validation", host)
			}
			if !strings.Contains(llBody, "host="+host+"\n") {
				t.Errorf("Host %q was not lifted unchanged; the handler saw %q", host, llBody)
			}
		})
	}
}

// A HEAD RESPONSE IS BODILESS ON EVERY PATH, INCLUDING THE ERROR PATHS.
//
// `o_http_status` suppresses the body for HEAD, and the serve loop decides that
// by reading `r.method` AFTER the parser returns. The version check used to
// return 505 before assigning it, so on that path `mlen` was 0, HEAD was not
// recognised, and a diagnostic body followed the headers.
//
// THIS READS THE SOCKET RATHER THAN USING llvmSend, and that is the whole
// reason it can see anything. `llvmSend` hands `http.ReadResponse` a HEAD hint
// — correctly, since a reader that did not know the method would block — and a
// hinted reader NEVER reads a body for HEAD. So the client-side body is empty
// whatever the server wrote, and an assertion built on it cannot distinguish a
// suppressed body from an unread one. A witness for something not happening has
// to prove it was in a position to see it.
//
// Verified by reverting: moving the assignment back below the version check
// makes the 505 case here fail, and no other test in the suite notices.
func TestHeadResponsesAreBodilessOnErrorPaths(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	addr, _ := llvmServe(t, buildLLVM(t, st, "lh-entry"))

	// rawExchange returns every octet the server wrote, so anything after the
	// header terminator is visible as the body it is.
	rawExchange := func(t *testing.T, req string) string {
		t.Helper()
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(c, req); err != nil {
			t.Fatalf("write: %v", err)
		}
		b, _ := io.ReadAll(c) // the server closes, or the deadline ends it
		return string(b)
	}
	bodyOf := func(raw string) string {
		if i := strings.Index(raw, "\r\n\r\n"); i >= 0 {
			return raw[i+4:]
		}
		return ""
	}

	for _, tc := range []struct{ name, raw string }{
		{"HEAD with an unsupported version", "HEAD /hook HTTP/2.0\r\nHost: h\r\nConnection: close\r\n\r\n"},
		{"HEAD with no Host", "HEAD /hook HTTP/1.1\r\nConnection: close\r\n\r\n"},
	} {
		raw := rawExchange(t, tc.raw)
		if body := bodyOf(raw); body != "" {
			t.Errorf("%s: the response carried %d octets after the headers (%q). A HEAD "+
				"response carries no body on any path, and an error path is where that is "+
				"easiest to forget", tc.name, len(body), body)
		}
	}

	// CONTROL: the same failing request as a GET DOES carry the diagnostic, so
	// the assertions above distinguish HEAD from "this path never has a body".
	if body := bodyOf(rawExchange(t, "GET /hook HTTP/2.0\r\nHost: h\r\nConnection: close\r\n\r\n")); body == "" {
		t.Error("a GET with an unsupported version carried no body either, so the HEAD " +
			"assertions above distinguish nothing")
	}
}

// THE SMUGGLING SHAPE IS FRAMED THE SAME WAY BY BOTH BACKENDS.
//
// RFC 9112 §6.1 permits either disposition — "A server MAY reject a request
// that contains both Content-Length and Transfer-Encoding or process such a
// request in accordance with the Transfer-Encoding alone." Both were therefore
// conformant while they disagreed, which is why no RFC-shaped test caught it.
//
// SPEC §14.0 is what binds them: "Two backends satisfying it produce the SAME
// `Request` value from the same HTTP request, and that is the entire
// obligation." A handler's properties are proven against that value, so a
// disagreement here stops a proof transferring from one artifact to the other —
// the divergence is a verification problem before it is an HTTP one.
//
// Measured when this was written: LLVM answered 400 and Go answered 200 on the
// same octets, and neither test suite noticed, because each backend's tests
// asserted its own behaviour. This asserts the RELATION instead.
func TestBothBackendsFrameTheSmugglingShapeAlike(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-div")
	llAddr, _ := llvmServe(t, buildLLVM(t, st, "lh-div"))
	goBin, _ := buildProgram(t, st, "lh-div")
	goAddr, _ := llvmServe(t, goBin)

	// 0x04 through the chunked framing; the divider's status is 1000/first-byte,
	// so agreement on the STATUS is agreement on the decoded body.
	raw := "POST /d HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n" +
		"Content-Length: 1\r\n\r\n1\r\n\x04\r\n0\r\n\r\n"

	llStatus, llBody := llvmSend(t, llAddr, raw)
	goStatus, goBody := llvmSend(t, goAddr, raw)

	if llStatus != goStatus || llBody != goBody {
		t.Errorf("the backends disagree on the smuggling shape: llvm=%d/%q go=%d/%q — "+
			"§14.0 binds them to one Request from these octets", llStatus, llBody, goStatus, goBody)
	}
	// AND THEY AGREE ON THE RIGHT ANSWER, not merely with each other. Two
	// backends that both refused would satisfy the comparison above while
	// neither delivered the body §6.3 says the Transfer-Encoding frames.
	if llStatus != 250 {
		t.Errorf("answered %d, want 250: the Transfer-Encoding must override the "+
			"Content-Length and the chunked octet must arrive as itself", llStatus)
	}
}
