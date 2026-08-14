package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// THREE FAMILIES THE GENERATED SEARCH FOUND, EACH WITH ITS OWN WITNESS.
//
// TestBackendsAgreeOnGeneratedRequests is a SEARCH: it reports a count over one
// seed and one grammar, and a count is not a witness. When it fell from 17 to 0
// it stopped being able to say WHY, and a later regression would come back as a
// number with no name attached. These three name the shapes, and each fails on
// the reversal of exactly one repair — which the search cannot do, because every
// repair moves the same number.
//
// Each test asserts the RENDERING, not just the status. Two backends can both
// answer 200 while building different Request values, and that is the failure
// §14.0 exists to prevent; the handler echoes method, target and the surviving
// fields, so equality of its output is equality of the value.

// agreementServers builds both backends over the shared handler fixture.
func agreementServers(t *testing.T) (llAddr, goAddr string) {
	t.Helper()
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-entry")
	llAddr, _ = llvmServe(t, buildLLVM(t, st, "lh-entry"))
	goBin, _ := buildProgram(t, st, "lh-entry")
	goAddr, _ = llvmServe(t, goBin)
	return llAddr, goAddr
}

// splitReceivedAt separates a rendering from the received-at it ends with.
//
// TWO BACKENDS MUST NOT BE ASKED TO AGREE ON received-at, AND THIS IS NOT A
// CONVENIENCE. SPEC §14.2 row 20 makes it an OBSERVATION: each server reads its
// own clock at the moment the request arrives, and the two requests here are
// sent one after the other. Asserting equality asserts that two independent
// observers saw the same second, which is false whenever the pair straddles a
// boundary and is an obligation §14.0 never states — every other component of
// the value is a function of the octets, and this one is a function of when
// they showed up. Found in review, before it flaked.
//
// IT STILL HAS TO BE THERE AND HAS TO BE A TIME, AND THE SECOND HALF NEEDS AN
// EXTERNAL CLOCK. The tail must be a non-empty digit run, and callers check it
// against THE TEST'S OWN wall-clock window around the exchange — not only
// against the other backend. Comparing the two observations to each other
// cannot tell a receipt time from any constant both renderings happen to end
// in: a fixture that dropped received-at and left a trailing `0` would satisfy
// a proximity check perfectly, and the split would then be discarding a real
// component of the value while claiming to discard an observation. Review
// found exactly that hole in the first version of this comment, which asserted
// "has to be a time" while checking only agreement.
//
// So the two conditions are independent: agreement is checked between the
// backends, and TIMELINESS is checked against a clock neither of them supplied.
// WATCHED FIRING, not assumed to: rendering a constant instead of received-at
// in lh-line makes both backends agree perfectly and fails here anyway — in the
// witnesses by naming the window, and in the differential gate as 197 of 300
// renderings outside it.
//
// It REPORTS ok rather than failing, because its other caller sweeps 300
// generated requests whose bodies are mostly refusal diagnostics with no
// rendering in them at all — a helper that failed on everything that is not a
// rendering could not be used where the question is whether one exists.
// mustSplitReceivedAt below is the assertive form, for the witnesses, where
// every response IS expected to be a rendering.
func splitReceivedAt(body string) (head string, at int64, ok bool) {
	i := strings.LastIndexByte(body, '\n')
	if i < 0 {
		return body, 0, false
	}
	tail := body[i+1:]
	if tail == "" {
		return body[:i+1], 0, false
	}
	for _, c := range tail {
		if c < '0' || c > '9' {
			return body[:i+1], 0, false
		}
	}
	var n int64
	fmt.Sscanf(tail, "%d", &n)
	return body[:i+1], n, true
}

func mustSplitReceivedAt(t *testing.T, what, body string) (string, int64) {
	t.Helper()
	head, at, ok := splitReceivedAt(body)
	if !ok {
		t.Errorf("%s: rendering does not end in a received-at integer: %q", what, body)
	}
	return head, at
}

// bothServe asserts that the octets reach the Oath entry on BOTH backends and
// build the same value. The rendering begins with the method, which is what
// distinguishes a Request that reached Oath code from a server's own answer —
// the distinction this file exists for, since net/http's general OPTIONS
// handler replies 200 with an empty body without invoking anything.
func bothServe(t *testing.T, llAddr, goAddr, raw, wantPrefix string) {
	t.Helper()
	lo := time.Now().Unix()
	ls, lb := sendFinal(t, llAddr, raw)
	gs, gb := sendFinal(t, goAddr, raw)
	hi := time.Now().Unix()
	if ls != 200 || gs != 200 {
		t.Errorf("%q answered llvm=%d go=%d, want 200 from both", raw, ls, gs)
	}
	lh, lat := mustSplitReceivedAt(t, "llvm "+raw, lb)
	gh, gat := mustSplitReceivedAt(t, "go "+raw, gb)
	if lh != gh {
		t.Errorf("%q built different Request values:\n  llvm: %q\n  go:   %q", raw, lb, gb)
	}
	if d := lat - gat; d > 5 || d < -5 {
		t.Errorf("%q: the two received-at observations are %ds apart, which is not two clocks "+
			"straddling a second — one of them is not a receipt time", raw, d)
	}
	// THE EXTERNAL ANCHOR. Each server stamped its own arrival, which lies
	// inside the window this test bracketed the exchange with; a second of slack
	// covers the truncation to whole seconds. A constant that is not a receipt
	// time fails here however well the two backends agree on it.
	for _, o := range []struct {
		who string
		at  int64
	}{{"llvm", lat}, {"go", gat}} {
		if o.at < lo-1 || o.at > hi+1 {
			t.Errorf("%q: %s reported received-at %d, outside the [%d, %d] window this exchange "+
				"happened in — that is not a receipt time, and a split that trims it is "+
				"discarding part of the Request value", raw, o.who, o.at, lo, hi)
		}
	}
	if !strings.HasPrefix(lb, wantPrefix) || !strings.HasPrefix(gb, wantPrefix) {
		t.Errorf("%q did not reach the handler on both backends (want a rendering starting "+
			"%q):\n  llvm: %q\n  go:   %q", raw, wantPrefix, lb, gb)
	}
}

// bothRefuse is the CONTROL half, and without it every test here would pass on
// a backend that accepted everything. A repair that removes a strictness must
// be shown to have removed THAT strictness and not the surrounding rule.
func bothRefuse(t *testing.T, llAddr, goAddr, raw string) {
	t.Helper()
	ls, _ := sendFinal(t, llAddr, raw)
	gs, _ := sendFinal(t, goAddr, raw)
	if ls != 400 || gs != 400 {
		t.Errorf("%q answered llvm=%d go=%d, want 400 from both — the repair widened past "+
			"the shape it was for", raw, ls, gs)
	}
}

// SPEC §14.2 row 17 DISCARDS the trailer section, and row 12's obsolete line
// folding applies to the field lines in it. Refusing a folded trailer was the
// largest divergence family in the search: net/http reads trailers with the
// reader it reads headers with, so a continuation is legal there and was a 400
// here.
//
// THE TWO REFUSALS ARE THE POINT OF THE TEST. Accepting every line that starts
// with whitespace would satisfy the first assertion and destroy the rule; both
// boundaries were measured on net/http before they were asserted here.
func TestFoldedTrailersAreAcceptedAndDiscardedByBothBackends(t *testing.T) {
	llAddr, goAddr := agreementServers(t)
	const head = "POST /t HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n"

	for _, body := range []string{
		"0\r\nX-T: a\r\n b\r\n\r\n",         // a fold with SP
		"0\r\nX-T: a\r\n\tb\r\n\r\n",        // a fold with HTAB
		"0\r\nX-T: a\r\n b\r\n c\r\n\r\n",   // two folds
		"0\r\nX-T: a\r\n \r\n\r\n",          // a fold whose continuation is empty
		"0\r\nX-T:\r\n b\r\n\r\n",           // a fold continuing an empty value
		"0\r\nX-T: a\r\n b\xc3\xa9\r\n\r\n", // obs-text in a fold: legal in both
	} {
		bothServe(t, llAddr, goAddr, head+body, "POST /t\n")
	}

	// A fold with no field line to continue is malformed, and so is a control
	// octet inside one. Both are the HEADER section's conditions, character for
	// character, and both were confirmed to be net/http's boundary too.
	bothRefuse(t, llAddr, goAddr, head+"0\r\n b\r\n\r\n")
	bothRefuse(t, llAddr, goAddr, head+"0\r\nX-T: a\r\n b\x01c\r\n\r\n")

	// AND THE DISCARD STILL HOLDS: no trailer becomes a header entry. The
	// renderer echoes every surviving field, so a trailer that leaked would
	// appear in the body.
	_, lb := sendFinal(t, llAddr, head+"0\r\nX-T: a\r\n b\r\n\r\n")
	if strings.Contains(lb, "x-t=") {
		t.Errorf("a trailer reached the Request value: %q — row 17 discards the section, and "+
			"accepting a fold must not turn it into a header", lb)
	}
}

// net/http answers `OPTIONS *` ITSELF, from Server.DisableGeneralOptionsHandler
// being false: 200, empty body, handler never called. The LLVM backend has no
// such interception and invokes the entry, so one backend built a Request and
// the other did not — a §14.0 disagreement about whether a Request EXISTS.
//
// The repair is on the Go side, because the alternative is a transport-only
// response path in the emitted C: a rule about HTTP added to a backend, for a
// request §14.2 gives a perfectly ordinary disposition.
func TestGeneralOptionsInterceptionDoesNotSwallowTheRequest(t *testing.T) {
	llAddr, goAddr := agreementServers(t)
	bothServe(t, llAddr, goAddr, "OPTIONS * HTTP/1.1\r\nHost: h\r\nX-A: 1\r\n\r\n", "OPTIONS *\n")

	// A CONTROL FOR THE CONTROL: an ordinary target still reaches the handler.
	// Without it, a Go backend that had stopped serving entirely would pass the
	// assertion above by failing everywhere equally.
	bothServe(t, llAddr, goAddr, "OPTIONS /hook HTTP/1.1\r\nHost: h\r\n\r\n", "OPTIONS /hook\n")
}

// RFC 9112 §3.2.4 is READ as reserving the asterisk form for OPTIONS, and it
// does not do that to a RECIPIENT. Its normative MUSTs bind the client that
// sends the target and the proxy that forwards it; the sentence everyone quotes
// — the form "is only used for a server-wide OPTIONS request" — describes
// sender usage. So there is no server-side conformance cost either way, and
// nothing is being traded for agreement here.
//
// net/http parses `PUT *` into a Request with RequestURI "*" and runs the
// handler. The LLVM parser refused it, which is the project's recurring shape —
// a rule read off a grammar coming out stricter than the reference — and §14.0
// makes each such difference a divergence.
//
// This was INVISIBLE until the Go handler stopped being a route on
// DefaultServeMux: the mux answered 400 for an uncleanable path, which agreed
// with the LLVM refusal by coincidence and for an unrelated reason.
func TestAsteriskFormIsNotRestrictedToOptions(t *testing.T) {
	llAddr, goAddr := agreementServers(t)
	for _, m := range []string{"GET", "PUT", "POST", "OPTIONS"} {
		bothServe(t, llAddr, goAddr, m+" * HTTP/1.1\r\nHost: h\r\n\r\n", m+" *\n")
	}

	// THE OTHER THREE FORMS ARE UNCHANGED, and a target that is none of the
	// four is still refused by both. Relaxing the asterisk branch must not have
	// relaxed row 24.
	bothServe(t, llAddr, goAddr, "GET /hook HTTP/1.1\r\nHost: h\r\n\r\n", "GET /hook\n")
	bothServe(t, llAddr, goAddr, "GET http://host/hook HTTP/1.1\r\nHost: h\r\n\r\n",
		"GET http://host/hook\n")
	for _, bad := range []string{"**", "*x", "x*", "hook"} {
		bothRefuse(t, llAddr, goAddr, "GET "+bad+" HTTP/1.1\r\nHost: h\r\n\r\n")
	}
}
