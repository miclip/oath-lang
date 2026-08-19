package main

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHandlerClosesEveryConnection is the #171 gate. RFC 9112 §6.1 makes closing
// the connection a MUST after a Transfer-Encoding + Content-Length request;
// net/http instead REUSES it (golang/go#80942) and strips both headers before
// the handler, so a precise per-conflict close would have to reimplement
// net/http's request framing (across pipelining, obs-fold and bare-LF). This
// backend takes the complete disposition instead: no keep-alive. Every response
// closes its connection, which makes the §6.1 conflict — and every other reuse —
// impossible by construction, matching the LLVM backend.
//
// WHAT THIS ASSERTS, and why each half is load-bearing:
//   - the source pins SetKeepAlivesEnabled(false) — a fix that dropped it would
//     silently restore the reuse this closes;
//   - end to end, no connection is reused, INCLUDING a pipelined TE+CL, which is
//     the case a handler-side close cannot reach (net/http serves the pipelined
//     request from its own buffer);
//   - every response carries Connection: close, so no client is surprised.
func TestHandlerClosesEveryConnection(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)
	put(t, st, `(defn hf-len [] [(b (List Int))] Int
		(match b ((Nil) 0) ((Cons x r) (+ 1 (hf-len r)))))`)
	put(t, st, `(defn hf-entry [] [(r Request)] Response
		(match r ((Req m p h b t)
		  (Resp (if (< (hf-len b) 0) 500 204) (Nil [(Pair Str Str)]) (Nil [Int])))))`)

	src, err := emitProgram(st, codegenPlan(t, st, "hf-entry"))
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	if !strings.Contains(src, "srv.SetKeepAlivesEnabled(false)") {
		t.Fatal("the handler's server does not disable keep-alive; RFC 9112 §6.1 " +
			"is held by the per-request connection model, and dropping it would " +
			"restore the reuse this gate exists to prevent")
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
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	dial := func() net.Conn {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
				return c
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("handler never accepted on %s", addr)
		return nil
	}

	// reusedAndClose sends one write, reads the first response, and reports
	// whether the connection is still open for a trailing probe and whether the
	// first response advertised close.
	check := func(name, wire string) {
		c := dial()
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		br := bufio.NewReader(c)
		if _, err := c.Write([]byte(wire)); err != nil {
			t.Fatalf("%s: write: %v", name, err)
		}
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("%s: first response: %v", name, err)
		}
		// http.ReadResponse consumes `Connection: close` into resp.Close (and
		// drops it from Header), so the parsed field is what to assert, not the
		// raw map.
		closed := resp.Close
		resp.Body.Close()
		if !closed {
			t.Errorf("%s: response did not advertise Connection: close", name)
		}
		if _, err := c.Write([]byte("GET /_probe HTTP/1.1\r\nHost: h\r\n\r\n")); err == nil {
			if _, err := http.ReadResponse(br, nil); err == nil {
				t.Errorf("%s: connection was reused; every connection must be per-request", name)
			}
		}
	}

	check("plain Content-Length", "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n\r\nhello")
	check("TE+CL (§6.1)", "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nContent-Length: 5\r\n\r\n5\r\nhello\r\n0\r\n\r\n")
	check("GET", "GET / HTTP/1.1\r\nHost: h\r\n\r\n")
	// Pipelined clean + TE+CL: the case a handler-side close cannot reach. With
	// no keep-alive, net/http closes after the first response and never serves
	// the pipelined second request on this connection.
	check("pipelined clean + TE+CL",
		"GET / HTTP/1.1\r\nHost: h\r\n\r\nPOST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nContent-Length: 5\r\n\r\n5\r\nhello\r\n0\r\n\r\n")
}
