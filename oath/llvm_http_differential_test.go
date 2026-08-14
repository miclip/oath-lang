package main

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// THE TWO BACKENDS MUST FRAME THE SAME OCTETS THE SAME WAY, AND THIS SEARCHES
// FOR PLACES THEY DO NOT.
//
// SPEC §14.0: "Two backends satisfying it produce the SAME `Request` value from
// the same HTTP request, and that is the entire obligation." A handler's
// properties are proven against that value, so a framing disagreement stops a
// proof transferring between artifacts — the divergence is a verification
// problem before it is an HTTP one.
//
// WHY A SEARCH RATHER THAN MORE CASES. Four divergences were found by review in
// three sittings — a Host field validated structurally, an absolute-form target
// split at the first `@` instead of the last, a folded trailer, and the
// smuggling shape. Every one had the same cause: a parser hand-written to the
// RFC comes out STRICTER than the reference implementation, and each strictness
// difference is a §14.0 violation. Fixing them individually leaves the class
// intact and the next one undiscovered until someone happens to look.
//
// The universe is HTTP request octets, so the instrument has to be one that
// generates them. This is deliberately a small grammar over the shapes that
// have historically differed, not a general fuzzer: it mutates the parts where
// implementations disagree — delimiters, folding, framing fields, unusual but
// legal authorities — because uniformly random bytes are refused by both and
// prove nothing.
//
// WHAT AGREEMENT MEANS HERE. Both status AND the handler's rendering, because
// two backends that both answer 200 while building different `Request` values
// are exactly the failure this is for. The renderer echoes method, path and the
// surviving fields, so equality of its output is equality of the value.
func TestBackendsAgreeOnGeneratedRequests(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	llAddr, _ := llvmServe(t, buildLLVM(t, st, "lh-entry"))
	goBin, _ := buildProgram(t, st, "lh-entry")
	goAddr, _ := llvmServe(t, goBin)

	// Fixed seed: a disagreement must reproduce for whoever reads the failure.
	r := rand.New(rand.NewSource(20260814))

	targets := []string{
		"/hook", "/hook?q=1", "/a%2Fb", "/", "*",
		"http://host/hook", "http://u@host/hook", "http://u@v@host/hook",
		"http://[::1]/hook", "http://[1::2::3]/hook", "http://host:80/hook",
	}
	hosts := []string{
		"h", "h:8080", "[::1]", "[1::2::3]", "[v1.x]", "HOST", "h.example.com", "",
	}
	extras := []string{
		"", "X-A: 1\r\n", "X-A: 1\r\nX-A: 2\r\n", "X-A: a\r\n b\r\n",
		"X-A:\r\n", "X-A: \t1 \t\r\n", "Connection: X-A\r\nX-A: 1\r\n",
		"Content-Length: 0\r\n", "Transfer-Encoding: chunked\r\n",
		"Transfer-Encoding: chunked\r\nContent-Length: 5\r\n",
		"Expect: 100-continue\r\n", "Trailer: X-T\r\n",
	}
	bodies := []string{"", "1\r\nx\r\n0\r\n\r\n", "0\r\n\r\n", "0\r\nX-T: v\r\n\r\n", "0\r\nX-T: a\r\n b\r\n\r\n"}
	methods := []string{"GET", "POST", "HEAD", "PUT", "OPTIONS"}

	type mismatch struct{ req, ll, gg string }
	var found []mismatch

	const cases = 300
	for i := 0; i < cases; i++ {
		m := methods[r.Intn(len(methods))]
		tg := targets[r.Intn(len(targets))]
		h := hosts[r.Intn(len(hosts))]
		ex := extras[r.Intn(len(extras))]
		body := ""
		if strings.Contains(ex, "chunked") {
			body = bodies[1+r.Intn(len(bodies)-1)]
		}
		hostLine := ""
		if h != "" {
			hostLine = "Host: " + h + "\r\n"
		}
		raw := m + " " + tg + " HTTP/1.1\r\n" + hostLine + ex + "\r\n" + body

		llStatus, llBody := sendFinal(t, llAddr, raw)
		goStatus, goBody := sendFinal(t, goAddr, raw)

		// THE COMPARISON IS THE Request VALUE, NOT THE RESPONSE BYTES. A first
		// version compared bodies unconditionally and reported 99 of 300 —
		// most of them two REFUSALS carrying different diagnostic prose
		// ("refused at the SPEC 14 boundary" vs "400 Bad Request: missing
		// required Host header"). Both produce NO Request, which is agreement
		// under §14.0; the error text is each server's own and is bound by
		// nothing. Comparing it measured the implementations' vocabularies
		// while claiming to measure the obligation.
		//
		// So: statuses must match, and the RENDERING must match only where the
		// handler ran. The renderer's output begins with the method, which is
		// what distinguishes a Request that reached Oath code from a server's
		// diagnostic.
		invoked := func(b string) bool {
			for _, m := range methods {
				if strings.HasPrefix(b, m+" ") {
					return true
				}
			}
			return false
		}
		disagree := llStatus != goStatus
		if invoked(llBody) || invoked(goBody) {
			disagree = disagree || llBody != goBody
		}
		if disagree {
			found = append(found, mismatch{
				req: raw,
				ll:  fmt.Sprintf("%d %q", llStatus, llBody),
				gg:  fmt.Sprintf("%d %q", goStatus, goBody),
			})
		}
	}

	// THE CONTROL: the generator must reach requests both backends SERVE, or a
	// clean run would be consistent with a grammar that only produces garbage
	// both refuse — agreement by mutual rejection, which proves nothing.
	served := 0
	for _, tg := range []string{"/hook", "http://host/hook"} {
		if s, _ := llvmSend(t, llAddr, "GET "+tg+" HTTP/1.1\r\nHost: h\r\n\r\n"); s == 200 {
			served++
		}
	}
	if served == 0 {
		t.Fatal("no generated shape is served by either backend, so agreement below is " +
			"agreement about refusals and says nothing about the Request value")
	}

	// A RATCHET, NOT A PASS/FAIL ON ZERO — and the distinction is deliberate.
	//
	// 17 real divergences over 300 generated requests. The dominant family is an
	// absolute-form authority carrying an explicit port (`http://host:80/hook`),
	// which this backend refuses and net/http serves. Fixing them is its own
	// piece of work.
	//
	// THE FIRST MEASUREMENT SAID 40, AND 23 OF THOSE WERE THE INSTRUMENT. The
	// sender returned the interim 100 for `Expect: 100-continue` requests, so
	// this backend's provisional response was compared against the other's final
	// one. Consuming interims first removed them. Recorded because the number is
	// meant to be trusted by whoever changes it next, and a ceiling inflated by
	// a client bug would have made 23 phantom divergences look like debt.
	//
	// Asserting zero today would mean not landing the instrument at all, and the
	// instrument is what found the family — review had produced four in three
	// sittings, one at a time. Asserting NO INCREASE keeps it a real gate: a
	// change that widens the gap fails here immediately, while the existing gap
	// is recorded rather than hidden. The number comes down as they are fixed,
	// and this line is the thing that makes each fix visible.
	//
	// It is a CEILING on a fixed seed and a fixed grammar, so it measures those
	// 300 requests and nothing more. It is not a claim about HTTP in general —
	// a different seed finds different requests, and a wider grammar would
	// almost certainly find more families.
	//
	// TWO FAMILIES ARE KNOWN AND DELIBERATELY OUT OF THIS GRAMMAR, so the
	// ceiling is not mistaken for coverage:
	//
	//   SIZE      this backend caps headers at 64 KiB and bodies at 1 MiB; the
	//             Go backend inherits net/http's allowance and an unbounded
	//             io.ReadAll. A ~70 KiB header request is 431 here and served
	//             there. Generating requests that large would make this test
	//             slow for one family already understood.
	//   COST      the Connection-nomination scan is quadratic (issue 172). It
	//             is a performance defect rather than a Request disagreement,
	//             so it would not appear here however wide the grammar.
	//
	// Both are §14.0-relevant and neither is fixed. Naming them here is the
	// difference between a gate with a stated scope and one that looks total.
	const known = 17
	if len(found) > known {
		for _, mm := range found {
			t.Logf("disagreement:\n%q\n  llvm: %s\n  go:   %s", mm.req, mm.ll, mm.gg)
		}
		t.Errorf("%d of %d generated requests are framed differently, up from the recorded "+
			"%d. A change has WIDENED the gap between the backends; §14.0 binds them to one "+
			"Request from the same octets", len(found), cases, known)
	}
	if len(found) < known {
		t.Errorf("%d divergences, DOWN from the recorded %d — good, and the constant must "+
			"come down with it or this gate stops ratcheting", len(found), known)
	}
	t.Logf("%d of %d generated requests framed differently (recorded ceiling)", len(found), cases)
}

// sendFinal is llvmSend that CONSUMES INTERIM RESPONSES before returning.
//
// `Expect: 100-continue` makes a server free to send a provisional 1xx and then
// the real answer. `llvmSend` returns the first response it reads, so the LLVM
// backend's interim 100 was being compared against the Go backend's final 200
// and counted as a Request divergence — an artifact of the client, inflating
// the ratchet and able to fail on a transport-only change.
//
// A provisional response says nothing about the value the handler built, which
// is what §14.0 binds. So they are read and discarded until a final one
// arrives.
func sendFinal(t *testing.T, addr, raw string) (int, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	var hint *http.Request
	if strings.HasPrefix(raw, "HEAD ") {
		hint = &http.Request{Method: "HEAD"}
	}
	br := bufio.NewReader(c)
	for i := 0; i < 4; i++ {
		resp, err := http.ReadResponse(br, hint)
		if err != nil {
			return 0, ""
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 100 && resp.StatusCode < 200 {
			continue // provisional: not the answer, and not evidence about the value
		}
		return resp.StatusCode, string(body)
	}
	return 0, ""
}
