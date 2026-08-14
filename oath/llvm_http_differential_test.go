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
	// Requests whose rendering did not end in a plausible received-at. Not
	// divergences — see the note at the check below.
	var badTime []string

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

		lo := time.Now().Unix()
		llStatus, llBody := sendFinal(t, llAddr, raw)
		goStatus, goBody := sendFinal(t, goAddr, raw)
		hi := time.Now().Unix()

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
		// AND received-at IS EXCLUDED FROM THAT COMPARISON, because it is the one
		// component of the value that is NOT a function of the octets. Row 20
		// makes it an OBSERVATION: each server reads its own clock as the request
		// arrives, and these two requests are sent one after the other, so a pair
		// straddling a second boundary would be reported as a framing divergence.
		// At a ceiling of 17 that surfaced as an eighteenth; at zero it is a
		// failing gate on a green tree. Found in review before it fired.
		//
		// SPLIT, NOT TRIMMED. splitReceivedAt requires the tail to be a non-empty
		// digit run, so a rendering that lost its timestamp still fails — and the
		// two observations are compared for PROXIMITY below, which is the real
		// obligation: they must be two readings of one arrival, not equal.
		disagree := llStatus != goStatus
		llInv, goInv := invoked(llBody), invoked(goBody)
		if llInv != goInv {
			// One built a Request and the other did not, which is the sharpest
			// form of the disagreement §14.0 forbids.
			disagree = true
		}
		if llInv && goInv {
			llHead, llAt, llOK := splitReceivedAt(llBody)
			goHead, goAt, goOK := splitReceivedAt(goBody)
			// A MISSING OR IMPLAUSIBLE TIMESTAMP IS AN INSTRUMENT FAILURE, NOT A
			// DIVERGENCE, and it must not be folded into the count. Both
			// renderings losing it would make the heads agree and the two zeroes
			// agree — a green gate measuring something other than its claim.
			// Reported after the loop instead, where it says what actually
			// happened. Two review findings landed here in turn: the first
			// version tested llOK != goOK, which cannot see BOTH going wrong
			// together; the second checked the two observations only against
			// EACH OTHER, which cannot tell a receipt time from any constant
			// both renderings end in. The window below is the external clock
			// that closes it.
			timely := func(at int64) bool { return at >= lo-1 && at <= hi+1 }
			if !llOK || !goOK || !timely(llAt) || !timely(goAt) {
				badTime = append(badTime, raw)
			}
			disagree = disagree || llHead != goHead
			// Two READINGS of one arrival, not two equal integers. Far apart
			// means one of them is not a receipt time, which is a real
			// divergence; one second apart is two clocks read in sequence.
			if d := llAt - goAt; d > 5 || d < -5 {
				disagree = true
			}
		}
		if disagree {
			found = append(found, mismatch{
				req: raw,
				ll:  fmt.Sprintf("%d %q", llStatus, llBody),
				gg:  fmt.Sprintf("%d %q", goStatus, goBody),
			})
		}
	}

	// THE INSTRUMENT BEFORE THE MEASUREMENT. If a rendering stopped ending in a
	// received-at — or ended in an integer that is not one — the comparison
	// above was stripping part of the Request value and the count below means
	// nothing. So this is fatal rather than counted, and it names a request
	// instead of a total.
	if len(badTime) > 0 {
		t.Fatalf("%d of %d renderings did not end in a received-at inside the window their "+
			"exchange happened in (first: %q). The handler fixture's output shape changed, so "+
			"the divergence count below is not a measurement of anything",
			len(badTime), cases, badTime[0])
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

	// A RATCHET. It is now at ZERO over these 300 requests, and that is a
	// MEASUREMENT, not a target that was aimed at.
	//
	// It was 17, and the recorded diagnosis was wrong in a way worth keeping:
	// the dominant family was said to be an absolute-form authority carrying an
	// explicit port. Re-measuring by forcing the constant to zero and reading
	// every mismatch found TWO families and neither was that one — 13 chunked
	// messages whose discarded trailer section carried an obs-fold continuation,
	// and 4 `OPTIONS *` requests net/http answered itself. The ports were
	// incidental: every one of those requests also carried the folded trailer,
	// and the authority validator already accepted them. A count cannot say
	// WHY, so a prose diagnosis attached to one goes stale silently — which is
	// the argument for the three named witnesses in llvm_http_agreement_test.go
	// rather than for a smaller number here.
	//
	// A THIRD FAMILY WAS EXPOSED BY REPAIRING THE SECOND, and it had been hiding
	// behind an agreement for the wrong reason. The Go handler was a route on
	// DefaultServeMux; taking it off the mux (which is what lets the general
	// OPTIONS interception be disabled) revealed that net/http parses `PUT *`
	// into a Request and serves it, while this backend refused the asterisk form
	// for any method but OPTIONS. Before, the mux answered 400 for an
	// uncleanable path and the two backends agreed by coincidence. TWO WRONG
	// ANSWERS THAT MATCH ARE INDISTINGUISHABLE FROM AGREEMENT HERE, and only
	// changing one of them shows it.
	//
	// THE FIRST MEASUREMENT SAID 40, AND 23 OF THOSE WERE THE INSTRUMENT. The
	// sender returned the interim 100 for `Expect: 100-continue` requests, so
	// this backend's provisional response was compared against the other's final
	// one. Consuming interims first removed them. Recorded because the number is
	// meant to be trusted by whoever changes it next, and a ceiling inflated by
	// a client bug would have made 23 phantom divergences look like debt.
	//
	// ZERO HERE IS NOT "THE BACKENDS AGREE". It is a CEILING on a fixed seed and
	// a fixed grammar, so it measures those 300 requests and nothing more — a
	// different seed finds different requests, and a wider grammar would almost
	// certainly find more families. One is known already: the trailer section is
	// still stricter here than in net/http for a framing field or a non-token
	// trailer name (measured), and the generator does not produce those.
	//
	// The gate keeps working unchanged at zero: a change that widens the gap
	// fails immediately, and the constant must come down with any further fix.
	//
	// FOUR FAMILIES ARE KNOWN AND DELIBERATELY OUT OF THIS GRAMMAR, so the
	// ceiling is not mistaken for coverage:
	//
	//   TRAILER   a trailer section carrying a framing field (Content-Length,
	//             Transfer-Encoding, Connection, Host) or a non-token name is
	//             refused here and SERVED by net/http — measured, and the
	//             opposite of what the comment at that code used to claim. It is
	//             kept refused because relaxing it is a decision about request
	//             smuggling, not a comment fix. The generator's bodies carry
	//             only `X-T` trailers, so this never reaches the count.
	//   SIZE      this backend caps headers at 64 KiB and bodies at 1 MiB; the
	//             Go backend inherits net/http's allowance and an unbounded
	//             io.ReadAll. A ~70 KiB header request is 431 here and served
	//             there. Generating requests that large would make this test
	//             slow for one family already understood.
	//             THE TRAILER SECTION IS THE SAME FAMILY, and it is recorded
	//             because review read it as new: a trailer over roughly 4 KiB
	//             is 200 here and 400 there, net/http's reader giving up before
	//             it finds the terminating CRLF. It is driven by SIZE and not by
	//             folding — measured with the control that decides it, an
	//             UNFOLDED 5 KiB trailer this backend accepted before the fold
	//             repair and accepts now, which diverges identically. Chasing it
	//             would mean writing another implementation's buffer size into
	//             this parser: undocumented, non-normative, and free to change
	//             between Go releases.
	//   CONNECT   a 2xx answer TO a CONNECT is refused 500 here — a successful
	//             CONNECT switches the connection to a tunnel this runtime
	//             cannot provide — and delivered by net/http. Measured, and
	//             measured to be a RESPONSE difference and not a Request one:
	//             `CONNECT *` and `CONNECT h:443` reach the entry on both
	//             backends and both answer 333 to a handler that returns one,
	//             so §14.0's obligation is met and the disagreement is about
	//             what can be DELIVERED. Named here because a reviewer read it
	//             as an asterisk-form parsing bug; `*` is already a valid
	//             reg-name authority, so the CONNECT branch accepts it and no
	//             ordering change would alter anything. CONNECT is not in the
	//             method list above, so this never reaches the count.
	//   COST      a cost disagreement is not a Request disagreement, so it would
	//             not appear here however wide the grammar. Issue 172's
	//             quadratic nomination scan was one, and the bound that replaced
	//             it is witnessed by timing in llvm_nomination_cost_test.go —
	//             which is a separate instrument for a reason: this one compares
	//             VALUES, and two backends can agree on every value while one of
	//             them takes a second to say so.
	//
	// TRAILER and SIZE remain §14.0-relevant and unfixed. Naming them here is
	// the difference between a gate with a stated scope and one that looks
	// total — and at zero that difference is the whole of what stops this
	// reading as "the two backends agree".
	const known = 0
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
