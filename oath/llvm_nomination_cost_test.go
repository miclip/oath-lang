package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// THE COST OF ONE LEGAL REQUEST, BOUNDED (#172).
//
// SPEC §14.2 row 16 nominates fields by presence in a Connection value, and the
// adapter must decide the question once per surviving field. The first
// implementation answered it by RESCANNING the message, so the work before the
// handler ran was fields × options — both bounded only by the 64 KiB header
// allowance, and an attacker chooses the split between them. Nothing in such a
// request is malformed, the read deadline is consulted only during socket I/O,
// and the serve loop is serial: one well-formed request could hold the server
// for as long as the scan took.
//
// THE CLAIM IS ABOUT THE WORST LEGAL REQUEST, NOT ABOUT A TYPICAL ONE, so the
// vector maximises the product under the header limit rather than exercising a
// plausible shape. With minimal field lines at four octets and minimal options
// at two, the product peaks when the two halves of the budget are equal.
//
// THE CONTROL IS THE SAME REQUEST WITH THE OPTION OCTETS MOVED, and it is what
// makes the number a measurement rather than a stopwatch reading: identical
// total size, identical field count, identical body, and the octets land in a
// field that is discarded by the same row 15 rule — so the only difference is
// whether they are PARSED AS OPTIONS. A transport, parser or allocator cost
// would move both. Only the nomination lookup moves one.
//
// WHY A RATIO AND A CEILING, and not either alone. The ratio is what the claim
// is actually about (lookup must not scale with the option count) and it
// survives a slow or loaded machine; the ceiling catches a regression that
// slows BOTH shapes, which a ratio cannot see — the rescanning version was
// quadratic in the field count as well, and the control paid for that too.
//
// BOTH BOUNDS WERE SET FROM MEASUREMENT, NOT CHOSEN. On the machine that wrote
// them, the rescanning implementation served this vector in 685 ms against a
// 39 ms control (ratio 17); the indexed one serves it in 2.9 ms against 1.0 ms
// (ratio 3.0). Each bound sits between the two with two orders of magnitude of
// headroom over the passing side, and BOTH were verified to fire by restoring
// the rescanning implementation and re-running.
func TestNominationLookupSurvivesTheWorstLegalRequest(t *testing.T) {
	requireClang(t)
	st := llvmHandlerStore(t)
	markVerified(t, st, "lh-div")
	bin := buildLLVM(t, st, "lh-div")
	addr, _ := llvmServe(t, bin)

	adv, ctl := worstLegalNominationRequest(addr)
	if len(adv) != len(ctl) {
		t.Fatalf("the control is not the same size as the vector: %d vs %d", len(adv), len(ctl))
	}
	t.Logf("vector: %d octets of header section", len(adv))

	// A WARM PASS FIRST. The first request a freshly-execed binary serves pays
	// for page faults and the first arena block, and attributing that to the
	// adapter would overstate every number below.
	if st, _ := timedSend(t, addr, ctl); st != 204 {
		t.Fatalf("the warm-up control answered %d, so the vector is not being served at all", st)
	}

	// The MINIMUM of a few runs, because the hazard is a cost that is always
	// there and scheduler noise only ever adds. A median would hide a fast run
	// under a slow one; the minimum cannot make a slow implementation look fast.
	const runs = 3
	var advT, ctlT time.Duration
	for i := 0; i < runs; i++ {
		status, d := timedSend(t, addr, ctl)
		if status != 204 {
			t.Fatalf("control answered %d", status)
		}
		if ctlT == 0 || d < ctlT {
			ctlT = d
		}
		status, d = timedSend(t, addr, adv)
		if status != 204 {
			t.Fatalf("the adversarial vector answered %d, so it was refused rather than served "+
				"— a refusal would make this measurement meaningless", status)
		}
		if advT == 0 || d < advT {
			advT = d
		}
	}
	t.Logf("worst legal nomination request: %v; same-size control with the option octets "+
		"moved out of Connection: %v (ratio %.2f)", advT, ctlT, float64(advT)/float64(ctlT))

	const ceiling = 250 * time.Millisecond
	if advT > ceiling {
		t.Errorf("one legal request occupied the server for %v (bound %v): the nomination "+
			"lookup is scaling with the option count again (#172)", advT, ceiling)
	}
	if advT > 8*ctlT+50*time.Millisecond {
		t.Errorf("the adversarial split cost %v against %v for the same octets outside a "+
			"Connection value: nomination lookup is not sublinear in the option count (#172)",
			advT, ctlT)
	}
}

// worstLegalNominationRequest builds the vector and its control.
//
// THE SPLIT IS DERIVED, NOT PICKED. A field line costs at least four octets
// ("a:" CRLF) and an option at least two ("b" comma), so with the header budget
// spent as 4F + V the product F×V is largest when the two terms are equal.
// Every option token matches NO field name, because a match ends the rescan
// early — the worst case is the one where it never does.
//
// MANY Connection LINES, NOT ONE, and that is the whole reason the vector is
// built here rather than written out. A single line is capped at 8 KiB, so a
// vector using one line measures a bound a quarter the size of the real one.
// SPEC §14.2 row 16 takes the UNION across every Connection line and puts no
// bound on how many arrive, so the option budget is spread across as many lines
// as the line limit requires — which is what a request under the header
// allowance can actually carry.
func worstLegalNominationRequest(addr string) (adversarial, control string) {
	const (
		budget   = 64000 // under O_HTTP_HDRMAX with room for the fixed lines
		perLine  = 8000  // under O_HTTP_LINEMAX, with room for the name and colon
		connName = "connection"
		// THE SAME LENGTH AND THE SAME DISPOSITION. Equal length keeps the two
		// messages octet for octet identical in size; keep-alive is a framing
		// field, so its value is DISCARDED by row 15 exactly as a Connection
		// value is. An ordinary field name here would have been the wrong
		// control: its 32 KiB of values would survive into the Request and be
		// allocated there, so the control would pay a cost the vector does not
		// and quietly understate the ratio. Found in review.
		fillName = "keep-alive"
	)
	head := "POST / HTTP/1.1\r\nHost: " + addr + "\r\nContent-Length: 0\r\n"
	spare := budget - len(head) - 2 // the empty line ending the header section

	// Half the remaining octets to option values, half to field lines. The
	// per-line overhead (name, colon, space, CRLF) is charged to the option half
	// so the two halves stay comparable.
	optionOctets := spare / 2
	fields := (spare - optionOctets) / 4

	var opts, fill strings.Builder
	for left := optionOctets; left > 0; {
		n := left
		if n > perLine {
			n = perLine
		}
		left -= n
		body := n - len(connName) - 4 // "name" ":" " " CRLF
		if body <= 0 {
			break
		}
		// An odd number of octets cannot be an exact run of "b," pairs, so the
		// run is built to length and any leftover octet padded onto the last
		// token — which stays a token and stays unequal to every field name.
		v := strings.Repeat("b,", body/2)
		if body%2 == 1 {
			v += "b"
		}
		v = strings.TrimSuffix(v, ",")
		opts.WriteString(connName + ": " + v + "\r\n")
		fill.WriteString(fillName + ": " + v + "\r\n")
	}

	fs := strings.Repeat("a:\r\n", fields)
	adversarial = head + opts.String() + fs + "\r\n"
	control = head + fill.String() + fs + "\r\n"
	return adversarial, control
}

func timedSend(t *testing.T, addr, raw string) (int, time.Duration) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(120 * time.Second))
	start := time.Now()
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// THROUGH THE FIRST CRLF, not one Read. A segmented response can deliver
	// "HTTP/1." and then the rest, and a single Read would report an
	// unparseable status for a response that was perfectly well formed — a
	// timing test that fails for a transport reason says nothing about the cost
	// it exists to bound.
	head, err := bufio.NewReader(conn).ReadString('\n')
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("read: %v (after %v)", err, elapsed)
	}
	sp := strings.SplitN(head, " ", 3)
	if len(sp) < 2 {
		t.Fatalf("unparseable status line %q", head)
	}
	var code int
	if _, err := fmt.Sscanf(sp[1], "%d", &code); err != nil {
		t.Fatalf("unparseable status %q", head)
	}
	return code, elapsed
}
