package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// http_request ON THE LLVM BACKEND, AGAINST THE GO BACKEND ON THE SAME SERVER.
//
// This capability was refused by the LLVM backend until libcurl was linked, and
// the refusal was honest: the capability's URL is a RUNTIME value, so a
// plain-HTTP client could not refuse `https` at build time and would have
// returned the failure value for it at run time — indistinguishable from an
// unreachable host, and a SILENT divergence from the Go backend. That is exactly
// what this backend's refuse-and-name discipline exists to prevent, which is why
// the dependency is the honest option and a hand-rolled client is not.
//
// WHAT THE COMPARISON HAS TO COVER. The contract is "the response body, or the
// failure value" — so agreement on a 200 proves very little on its own. The
// cases that discriminate are the ones where a reasonable implementer would
// choose differently:
//
//	404          the Go provider returns the BODY, not an error. A backend that
//	             treated non-2xx as failure would pass every success case and be
//	             wrong here.
//	empty body   a 200 with zero bytes must be the empty string, which is also
//	             the failure value — so this pins that the two are NOT
//	             distinguishable by the protocol, rather than accidentally equal.
//	redirect     the Go provider follows (http.Get does). A client without
//	             FOLLOWLOCATION returns the redirect's own empty body and agrees
//	             with nothing.
//	binary       a body with NUL and high bytes must survive as Str codepoints;
//	             a client using strlen truncates at the first NUL.
//	unreachable  both must produce the failure value, not a crash or a hang.
//
// skipUnlessHTTPProvisions gates the differential tests on what the PROVIDER
// actually decides, not on a second copy of its requirements.
//
// o_cap_http refuses at launch on a libcurl lacking HTTPS, gzip, or thread-safe
// initialisation — all of which link perfectly well, so "curl-config exists" and
// "a probe compiles" both say yes on a host where every one of these tests would
// then fail with the Go binary succeeding and the LLVM binary exiting 70. Asking
// the built artifact is the only gate that cannot drift from the rule it guards:
// re-listing the versions and features here would be a duplicate to maintain,
// and it would be wrong on exactly the hosts it exists for.
// requireUsableLibcurl asks the FUNCTION THE BUILD ASKS. "curl-config exists" is
// not the same question: it can report a stale prefix whose headers clang cannot
// use, in which case llvmCurlFlags rejects its output AND the -lcurl fallback,
// and buildLLVM fatals on a host that should simply have skipped.
func requireUsableLibcurl(t *testing.T) {
	t.Helper()
	if _, err := llvmCurlFlags(); err != nil {
		t.Skipf("libcurl not usable by the llvm backend: %v", err)
	}
}

func skipUnlessHTTPProvisions(t *testing.T, bin, url string) {
	t.Helper()
	out, err := exec.Command(bin, url).CombinedOutput()
	// ANY provisioning diagnostic mentioning libcurl counts. Matching only the
	// "http_request cannot be provided" wording misses two real refusals —
	// "libcurl did not report its version information" and "libcurl could not be
	// initialised" — and on a host emitting either, every assertion below would
	// compare the reference against a launch error.
	if err != nil && strings.Contains(string(out), "libcurl") {
		t.Skipf("this host's libcurl does not satisfy the provider: %s", strings.TrimSpace(string(out)))
	}
}

func TestHTTPRequestAgreesAcrossBackends(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	mux := http.NewServeMux()
	// Declared ahead of the handlers because one of them echoes a header whose
	// value contains the server's own (per-run) base URL.
	var srv *httptest.Server
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello-from-http"))
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not-here-body"))
	})
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok", http.StatusFound)
	})
	mux.HandleFunc("/binary", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte{'a', 0x00, 'b', 0xC3, 0xA9, 'c'})
	})
	// A GZIPPED BODY. net/http advertises gzip and decodes it transparently;
	// libcurl returns the compressed bytes unless CURLOPT_ACCEPT_ENCODING asks
	// for them decoded. Without that option the two backends return DIFFERENT
	// bytes for the same URL — a silent divergence, and the reason this is a
	// case rather than a comment.
	mux.HandleFunc("/gzipped", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte("gzipped-payload-that-must-be-decoded"))
		_ = zw.Close()
	})
	// A REDIRECT CHAIN LONGER THAN TEN HOPS. net/http gives up after 10;
	// libcurl with FOLLOWLOCATION and no MAXREDIRS follows without limit, so the
	// Go backend returns the failure value where the LLVM backend would return
	// the final body. The chain terminates so a regression is a wrong ANSWER
	// rather than a hanging test.
	// THE REQUEST HEADERS ARE OBSERVABLE. A server may vary its response on
	// User-Agent or Accept, so two backends that send different requests agree
	// only for servers that happen not to care. Echoing them back turns the
	// request into part of the response, which is what makes this checkable at
	// all by the same byte comparison as every other case.
	mux.HandleFunc("/echo-headers", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "ua=%q accept=%q accept-encoding=%q",
			r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Accept-Encoding"))
	})
	// THE REFERER IS SET ON A REDIRECT HOP, and only on one — so the echo has to
	// be reached THROUGH a redirect for the header to exist at all.
	mux.HandleFunc("/to-echo", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/echo-referer", http.StatusFound)
	})
	// CREDENTIALS MUST NOT TRAVEL IN THE REFERER. A URL may carry userinfo, and
	// a redirect would otherwise hand user:pass to the next server. The
	// reference drops it; echoing the header back is what makes that checkable.
	mux.HandleFunc("/creds-to-echo", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/echo-referer", http.StatusFound)
	})
	mux.HandleFunc("/echo-referer", func(w http.ResponseWriter, r *http.Request) {
		ref := r.Header.Get("Referer")
		// The port varies per run, so the ORIGIN is stripped before comparing —
		// but everything else is echoed verbatim, which is what lets a leaked
		// user:pass show up here instead of being trimmed away with the host.
		_, _ = fmt.Fprintf(w, "referer=%q", strings.TrimPrefix(ref, srv.URL))
	})
	// A Location WITH A RAW SPACE IN ITS QUERY. Whether the redirect target is
	// normalized on the way is observable to any server that reads the raw
	// request target, so the echo reports it verbatim.
	mux.HandleFunc("/raw-query-redirect", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/echo-raw-query?q=a b&r=caf\u00e9")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/echo-raw-query", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "raw=%q", r.URL.RawQuery)
	})
	// A SCHEME-BEARING Location IS ABSOLUTE, even without "//". The reference
	// parses foo:bar as an absolute URI and rejects the scheme; merging it into
	// the current origin instead would fetch a path the SERVER chose.
	mux.HandleFunc("/opaque-scheme", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "foo:bar")
		w.WriteHeader(http.StatusFound)
	})
	// A Location SUPPLIED ONLY AS A TRAILER. The reference decides redirects
	// from the initial header block, before trailers exist, so this response's
	// body is the result and no second request is made.
	mux.HandleFunc("/trailer-location", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "Location")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("body-with-trailer-location"))
		w.Header().Set("Location", "/binary")
	})
	// AN EMPTY FIRST Location, THEN A NON-EMPTY ONE. Header.Get returns the
	// first, which is empty, so the reference does not redirect — a backend
	// that skipped the empty one would follow the second, to a destination the
	// server, not the caller, chose.
	mux.HandleFunc("/empty-then-real-location", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Location", "")
		w.Header().Add("Location", "/binary")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("body-not-a-redirect"))
	})
	// TWO Location HEADERS: the reference reads the FIRST. A backend taking the
	// last would follow a different destination for the same response.
	mux.HandleFunc("/two-locations", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Location", "/ok")
		w.Header().Add("Location", "/binary")
		w.WriteHeader(http.StatusFound)
	})
	// A LONG Location IS STILL A REDIRECT. Its length is chosen by the server,
	// so any fixed cap here is a boundary a remote party can reach; the
	// reference's limit is its whole header budget, far above this.
	mux.HandleFunc("/long-location", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok?pad="+strings.Repeat("p", 8000), http.StatusFound)
	})
	mux.HandleFunc("/ok-padded", func(w http.ResponseWriter, _ *http.Request) {})
	// AN EMPTY Location IS NOT A REDIRECT. The reference returns the response
	// body rather than following anything, so treating the header's PRESENCE as
	// a redirect would discard a body it keeps.
	mux.HandleFunc("/empty-location", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("body-with-empty-location"))
	})
	// 300 Multiple Choices CARRIES A Location AND IS NOT A REDIRECT. The
	// reference follows only 301/302/303/307/308, so this body is the RESULT.
	// Treating every 3xx as a redirect would discard it and follow a hop the
	// reference never takes.
	mux.HandleFunc("/multiple-choices", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/ok")
		w.WriteHeader(http.StatusMultipleChoices)
		_, _ = w.Write([]byte("body-of-300-not-a-redirect"))
	})
	// A BODY THAT IS NOT VALID UTF-8. Str is a codepoint datatype, so both
	// backends must REFUSE this rather than admit the bytes — and on the LLVM
	// side the refusal unwinds, so it is also the path where the host response
	// buffer has to be released before the refusal is raised.
	mux.HandleFunc("/invalid-utf8", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte{'o', 'k', 0xFF, 0xFE, 'z'})
	})
	// A CHAIN OF EXACTLY N REDIRECTS, so the 10-hop boundary can be probed on
	// both sides rather than only far past it. Go errors when len(via) >= 10 and
	// libcurl counts MAXREDIRS differently, so the OFF-BY-ONE is where the
	// backends part company — a case a 25-hop chain agrees on by accident.
	mux.HandleFunc("/chain/", func(w http.ResponseWriter, r *http.Request) {
		var total, n int
		_, _ = fmt.Sscanf(r.URL.Path, "/chain/%d/%d", &total, &n)
		if n >= total {
			_, _ = fmt.Fprintf(w, "chain-of-%d-complete", total)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/chain/%d/%d", total, n+1), http.StatusFound)
	})
	mux.HandleFunc("/hop/", func(w http.ResponseWriter, r *http.Request) {
		var n int
		_, _ = fmt.Sscanf(r.URL.Path, "/hop/%d", &n)
		if n >= 25 {
			_, _ = w.Write([]byte("end-of-a-very-long-chain"))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n+1), http.StatusFound)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-at [] [(args (List Str)) (n Int)] Str
		(match args
			((Nil) "")
			((Cons h rest) (if (== n 0) h (arg-at rest (- n 1))))))`)
	// `(. net fetch)` — a capability record field is PROJECTED, not in scope as
	// a bare name. Written the way examples/netcli.oath writes it.
	put(t, st, `(defn fetch-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-at args 0))
		(prop is-the-fetch [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (fetch-main net args) ((. net fetch) (arg-at args 0)))))`)

	goBin, _ := buildProgram(t, st, "fetch-main")
	llvmBin := buildLLVM(t, st, "fetch-main")
	skipUnlessHTTPProvisions(t, llvmBin, srv.URL+"/ok")

	for _, tc := range []struct{ name, url string }{
		{"200 with a body", srv.URL + "/ok"},
		{"200 with an EMPTY body", srv.URL + "/empty"},
		{"404 returns the BODY, not a failure", srv.URL + "/missing"},
		{"a redirect is FOLLOWED", srv.URL + "/redir"},
		{"a body with NUL and high bytes", srv.URL + "/binary"},
		{"a gzipped body is DECODED", srv.URL + "/gzipped"},
		{"300 with a Location is NOT followed", srv.URL + "/multiple-choices"},
		{"an EMPTY Location is not a redirect", srv.URL + "/empty-location"},
		{"a Location longer than 4 KiB is followed", srv.URL + "/long-location"},
		{"the FIRST of two Location headers wins", srv.URL + "/two-locations"},
		{"an empty FIRST Location is still the one that counts",
			srv.URL + "/empty-then-real-location"},
		{"a Location in a TRAILER is not a redirect", srv.URL + "/trailer-location"},
		{"a scheme-bearing Location without // is absolute", srv.URL + "/opaque-scheme"},
		{"a body that is not valid UTF-8 is REFUSED", srv.URL + "/invalid-utf8"},
		{"the REQUEST headers match the reference", srv.URL + "/echo-headers"},
		{"a redirect hop carries the same Referer", srv.URL + "/to-echo"},
		{"a Referer carries no userinfo",
			strings.Replace(srv.URL, "http://", "http://user:pass@", 1) + "/creds-to-echo"},
		{"a redirect chain past the 10-hop limit", srv.URL + "/hop/0"},
		{"a chain of 8 redirects", srv.URL + "/chain/8/0"},
		{"a chain of 9 redirects", srv.URL + "/chain/9/0"},
		{"a chain of 10 redirects (the boundary)", srv.URL + "/chain/10/0"},
		{"a chain of 11 redirects", srv.URL + "/chain/11/0"},
		{"a SCHEMELESS url", strings.TrimPrefix(srv.URL, "http://") + "/ok"},
		{"an unreachable host is the failure value", "http://127.0.0.1:1/nope"},
		{"a malformed URL is the failure value", "not-a-url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, gerr := exec.Command(goBin, tc.url).CombinedOutput()
			l, lerr := exec.Command(llvmBin, tc.url).CombinedOutput()
			if (gerr == nil) != (lerr == nil) {
				t.Fatalf("exit disagreement: go err=%v, llvm err=%v", gerr, lerr)
			}
			if string(g) != string(l) {
				t.Fatalf("backends disagree for %s\n  go   = %q\n  llvm = %q", tc.url, g, l)
			}
		})
	}
}

// THE DEPENDENCY IS PAID ONLY BY PROGRAMS THAT USE THE CAPABILITY.
//
// libcurl is linked from the PROGRAM'S REQUIREMENTS, not from a build flag, so a
// program that never fetches must compile and link exactly as it did before this
// capability existed. Without this, adding one capability would have made every
// LLVM build depend on a library — the cost this design exists to avoid, and the
// kind of regression nothing else here would notice.
func TestLibcurlIsLinkedOnlyWhenTheCapabilityIsRequired(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn plain-main [] [(args (List Str))] Str "no capabilities here"
		(prop constant [(args (List Str))]
			(== (plain-main args) "no capabilities here")))`)
	// THE DISCRIMINATING CASE: a program with a capability that is NOT
	// http_request. `plain-main` has no requirements at all, so it cannot tell
	// "needs curl" from "has any provider" — a mutation replacing the NeedsCurl
	// test with a bare table lookup passes against it and fails here.
	put(t, st, `(defn env-main [] [(host {env (-> Str Str)}) (args (List Str))] Str
		((. host env) "HOME")
		(prop is-the-env [(host {env (-> Str Str)}) (args (List Str))]
			(== (env-main host args) ((. host env) "HOME"))))`)
	put(t, st, `(defn arg-at2 [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-at2 rest (- n 1))))))`)
	put(t, st, `(defn fetch-main2 [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-at2 args 0))
		(prop is-the-fetch2 [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (fetch-main2 net args) ((. net fetch) (arg-at2 args 0)))))`)

	for _, tc := range []struct {
		name string
		want bool
	}{
		{"plain-main", false},
		{"env-main", false},
		{"fetch-main2", true},
	} {
		prog, err := planProgram(st, tc.name)
		if err != nil {
			t.Fatalf("planProgram(%s): %v", tc.name, err)
		}
		if got := llvmNeedsCurl(prog); got != tc.want {
			t.Errorf("llvmNeedsCurl(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A HOST WITHOUT libcurl MUST BE REFUSED BY NAME, not left to clang.
//
// The refusal has to name the CAPABILITY and point at the Go backend. Letting
// the build reach clang would surface as an undefined symbol nobody asked
// about — the diagnostic would be about `o_cap_http`, not about http_request,
// and the reader would have no way to know a supported backend exists.
func TestMissingLibcurlRefusalNamesTheCapability(t *testing.T) {
	_, err := llvmCurlFlags()
	if err == nil {
		return // libcurl present: the refusal path is not reachable here
	}
	for _, want := range []string{"http_request", "libcurl", "Go backend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

// http_request IS AUTHORITY OVER HTTP, AND libcurl SPEAKS MORE THAN HTTP.
//
// The capability model's claim is that undeclared authority is ABSENT from the
// binary. Linking libcurl to serve `http_request` quietly hands the program
// file://, ftp://, scp:// and smb:// as well — so a program declaring only
// http_request could read a local file, authority the artifact never declared,
// `oath provenance` never reports, and the Go backend does not grant.
//
// TWO DOORS, AND CLOSING ONE IS NOT CLOSING THE CLASS. The direct URL is the
// obvious one. The second is a REDIRECT: FOLLOWLOCATION is on, so a server the
// program legitimately fetches can answer 302 to file:// and escalate through a
// hop the caller never wrote — reachable by an ATTACKER rather than only by the
// program's own author, which makes it the more serious of the two.
//
// The sentinel is a temp file rather than /etc/passwd so the assertion is about
// bytes this test placed, and cannot pass because a hardened machine happened to
// deny the read.
//
// WHAT THIS WITNESSES, AND WHAT IT DOES NOT. Removing CURLOPT_PROTOCOLS leaks the
// sentinel through BOTH cases, so that setopt is under test. Removing only
// CURLOPT_REDIR_PROTOCOLS leaks nothing here: in libcurl 8.7.1 the transfer-level
// restriction already covers redirect targets, so the redirect setopt is
// belt-and-braces against the documented default (which admits ftp/ftps) rather
// than a line this test observes failing. Recorded rather than asserted, because
// an unobserved line called a control is how a suite starts overstating itself.
func TestHTTPRequestGrantsNoAuthorityBeyondHTTP(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	const sentinel = "SENTINEL-a3f9c1-local-file-contents"
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	fileURL := "file://" + secret

	mux := http.NewServeMux()
	mux.HandleFunc("/escalate", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fileURL, http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-at3 [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-at3 rest (- n 1))))))`)
	put(t, st, `(defn fetch-main3 [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-at3 args 0))
		(prop is-the-fetch3 [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (fetch-main3 net args) ((. net fetch) (arg-at3 args 0)))))`)

	goBin, _ := buildProgram(t, st, "fetch-main3")
	llvmBin := buildLLVM(t, st, "fetch-main3")
	skipUnlessHTTPProvisions(t, llvmBin, srv.URL+"/escalate")

	for _, tc := range []struct{ name, url string }{
		{"a file:// URL directly", fileURL},
		{"a redirect INTO file://", srv.URL + "/escalate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := exec.Command(llvmBin, tc.url).CombinedOutput()
			if strings.Contains(string(l), sentinel) {
				t.Fatalf("http_request read a local file: %q", l)
			}
			// The Go backend is the reference, and it grants no such authority
			// either — so the two must still agree, not merely both refuse.
			g, _ := exec.Command(goBin, tc.url).CombinedOutput()
			if strings.Contains(string(g), sentinel) {
				t.Fatalf("the GO backend read a local file: %q", g)
			}
			if string(g) != string(l) {
				t.Fatalf("backends disagree for %s\n  go   = %q\n  llvm = %q", tc.url, g, l)
			}
		})
	}
}

// A LARGE BODY UNDER A DECLARED HEAP BUDGET IS REFUSED, NOT LEAKED OR OOM-KILLED.
//
// The response buffer grows inside a libcurl WRITE CALLBACK, which is the one
// place the arena's ordinary refusal cannot be used: o_heap_exhausted longjmps
// to the server loop, and unwinding out of a callback abandons libcurl's live
// transfer state along with the easy handle. So the sink checks the budget
// BEFORE allocating and reports failure the way libcurl expects — a short
// return — leaving the library to unwind its own frames.
//
// This asserts the OBSERVABLE consequence: a body past the budget yields the
// capability's failure value and a clean exit, rather than a crash, a hang, or a
// process that grows until the host kills it.
func TestHTTPBodyBeyondTheHeapBudgetIsRefusedCleanly(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	big := bytes.Repeat([]byte("x"), 4<<20)
	mux := http.NewServeMux()
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(big) })
	mux.HandleFunc("/small", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("small-body"))
	})
	mux.HandleFunc("/fat-redirect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/small")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write(big) // a redirect body far larger than the budget
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-atb [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-atb rest (- n 1))))))`)
	put(t, st, `(defn budget-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-atb args 0))
		(prop is-the-fetchb [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (budget-main net args) ((. net fetch) (arg-atb args 0)))))`)
	llvmBin := buildLLVM(t, st, "budget-main")
	skipUnlessHTTPProvisions(t, llvmBin, srv.URL+"/small")

	// THE CONTROL: same binary, same budget, a body that FITS. Without it a
	// refusal would prove only that the budget stops everything.
	small := exec.Command(llvmBin, srv.URL+"/small")
	small.Env = append(os.Environ(), "OATH_HEAP_BUDGET=1048576")
	out, err := small.CombinedOutput()
	if err != nil {
		t.Fatalf("a body within the budget should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "small-body") {
		t.Fatalf("a body within the budget should be returned, got %q", out)
	}

	// A REDIRECT CARRYING A HUGE BODY STILL FOLLOWS. The intermediate body is
	// not the result and must not be buffered: charging it to the declared
	// budget would fail a request the reference completes, and the body is
	// attacker-controlled — a 302 with a megabyte of padding would become a
	// denial of service against a budgeted handler.
	fat := exec.Command(llvmBin, srv.URL+"/fat-redirect")
	fat.Env = append(os.Environ(), "OATH_HEAP_BUDGET=1048576")
	fatOut, fatErr := fat.CombinedOutput()
	if fatErr != nil {
		t.Fatalf("a redirect with a large body should still follow: %v\n%s", fatErr, fatOut)
	}
	if !strings.Contains(string(fatOut), "small-body") {
		t.Fatalf("the redirect target's body should be returned, got %q", fatOut)
	}

	cmd := exec.Command(llvmBin, srv.URL+"/big")
	cmd.Env = append(os.Environ(), "OATH_HEAP_BUDGET=1048576")
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a body past the budget should be REFUSED, not a crash: %v\n%s", err, got)
	}
	if strings.Contains(string(got), "xxxxxxxxxx") {
		t.Fatalf("the body was returned despite exceeding the declared budget")
	}
}

// UPPERCASE HTTP_PROXY IS HONOURED, BECAUSE THE REFERENCE HONOURS IT.
//
// libcurl ignores an uppercase HTTP_PROXY by design — under CGI it is
// attacker-controlled, arriving from the Proxy: request header — while net/http
// reads both spellings. A deployment that sets only HTTP_PROXY would therefore
// route the Go backend through its egress proxy and send the LLVM artifact
// straight out, which is both a different answer and a bypassed network control.
//
// The witness is a real proxy rather than an assertion about environment
// plumbing: the fake proxy answers every absolute-URI request with a body the
// ORIGIN never serves, so returning it is proof the request went through the
// proxy, and returning the origin's body is proof it did not.
func TestUppercaseHTTPProxyIsHonouredLikeTheReference(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ORIGIN-DIRECT"))
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("VIA-PROXY"))
	}))
	defer proxy.Close()

	// A LOOPBACK ORIGIN THAT REDIRECTS SOMEWHERE EXTERNAL. The first hop is
	// exempt from proxying; the second is not, and the reference re-decides per
	// hop. A transfer-wide proxy setting carries the first hop's exemption out
	// to the internet — the deployment's egress control bypassed by a redirect.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://oath-proxy-probe.invalid/x", http.StatusFound)
	}))
	defer redir.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-atp [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-atp rest (- n 1))))))`)
	put(t, st, `(defn proxy-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-atp args 0))
		(prop is-the-fetchp [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (proxy-main net args) ((. net fetch) (arg-atp args 0)))))`)
	goBin, _ := buildProgram(t, st, "proxy-main")
	llvmBin := buildLLVM(t, st, "proxy-main")
	skipUnlessHTTPProvisions(t, llvmBin, origin.URL)

	// TEST-NET-1 (RFC 5737) is guaranteed unroutable, so a request that reaches
	// it at all cannot succeed — which is what makes VIA-PROXY proof of the
	// proxy rather than a coincidence. It is never actually contacted: when the
	// proxy is honoured, the local proxy answers instead.
	const unroutable = "http://192.0.2.1/x"
	// A name that fails DNS immediately when the proxy is NOT used, so each
	// NO_PROXY case costs nothing and the two outcomes are far apart.
	const probeHost = "http://oath-proxy-probe.invalid/x"

	for _, tc := range []struct {
		name, url, env, want, absent string
	}{
		// The uppercase spelling is the one libcurl drops; the lowercase is the
		// CONTROL showing the proxy plumbing works at all, so a failure on the
		// uppercase case cannot be blamed on the harness.
		{"uppercase HTTP_PROXY", unroutable, "HTTP_PROXY=" + proxy.URL, "VIA-PROXY", ""},
		{"lowercase http_proxy", unroutable, "http_proxy=" + proxy.URL, "VIA-PROXY", ""},
		// A LOOPBACK DESTINATION IS NEVER PROXIED, whatever the environment says.
		// libcurl has no such rule, so without the exclusion list this case sends
		// the LLVM artifact through the proxy and the reference straight out.
		{"a proxy is set but the target is loopback", origin.URL, "HTTP_PROXY=" + proxy.URL, "ORIGIN-DIRECT", ""},
		{"no proxy configured", origin.URL, "OATH_UNUSED=1", "ORIGIN-DIRECT", ""},
		// httpoxy (CVE-2016-5385): under CGI the request's own Proxy: header
		// arrives as HTTP_PROXY, so the reference refuses an http-scheme proxy
		// whenever REQUEST_METHOD is set. The .invalid target fails at DNS
		// immediately when the proxy is refused, so the case costs nothing;
		// using the proxy would instead visibly succeed with the proxy's body.
		// The origin is REACHABLE here on purpose. The reference does not fall
		// back to a direct request under CGI, it returns an error — so a target
		// that cannot be reached anyway would let a direct-connect
		// implementation pass. It is also loopback, which the reference exempts
		// from proxying only AFTER raising this refusal, so the ordering of the
		// two rules is under test as well.
		{"HTTP_PROXY under CGI is REFUSED, not sent direct",
			origin.URL, "REQUEST_METHOD=GET", "", "ORIGIN-DIRECT"},
		// The redirect leaves loopback, so the SECOND hop must be proxied even
		// though the first was exempt.
		{"a loopback origin redirecting out is proxied on the next hop",
			redir.URL, "HTTP_PROXY=" + proxy.URL, "VIA-PROXY", ""},
		// NO_PROXY SEMANTICS, WITH THE REFERENCE AS THE ORACLE. The assertion
		// that matters in each of these is the backends AGREEING; the expected
		// value records what the reference actually does, measured rather than
		// read off a manual page. A leading dot is the one that bites: it means
		// subdomains ONLY, so the exact host is still proxied.
		{"NO_PROXY names the exact host", probeHost,
			"NO_PROXY=oath-proxy-probe.invalid", "", "VIA-PROXY"},
		{"NO_PROXY with a leading dot does not cover the exact host", probeHost,
			"NO_PROXY=.oath-proxy-probe.invalid", "VIA-PROXY", ""},
		{"NO_PROXY is a wildcard", probeHost, "NO_PROXY=*", "", "VIA-PROXY"},
		// NOT a parent domain: "probe.invalid" is a suffix of the STRING but not
		// at a label boundary, and the reference matches labels. Measured — the
		// first version of this case asserted the opposite and the reference
		// disagreed.
		{"a suffix that is not a label boundary does not match", probeHost,
			"NO_PROXY=probe.invalid", "VIA-PROXY", ""},
		{"a leading dot DOES cover a subdomain",
			"http://sub.oath-proxy-probe.invalid/x",
			"NO_PROXY=.oath-proxy-probe.invalid", "", "VIA-PROXY"},
		{"a bare entry covers its subdomains too",
			"http://sub.oath-proxy-probe.invalid/x",
			"NO_PROXY=oath-proxy-probe.invalid", "", "VIA-PROXY"},
		{"NO_PROXY names an unrelated host", probeHost,
			"NO_PROXY=elsewhere.invalid", "VIA-PROXY", ""},
		// The URL carries no port, so matching this entry requires the scheme's
		// default — which the reference supplies and a naive matcher does not.
		{"a port-qualified NO_PROXY entry matches the scheme default", probeHost,
			"NO_PROXY=oath-proxy-probe.invalid:80", "", "VIA-PROXY"},
		{"a port-qualified entry with the WRONG port does not match", probeHost,
			"NO_PROXY=oath-proxy-probe.invalid:8080", "VIA-PROXY", ""},
		// AN IP LITERAL IS NOT A DOMAIN. "2.1" is a suffix of "192.0.2.1" at a dot
		// boundary, so a domain matcher drops the proxy for it — the reference
		// does not apply domain matching to a parsed IP, and keeps the proxy.
		{"a numeric NO_PROXY suffix does not match an IP literal", unroutable,
			"NO_PROXY=2.1", "VIA-PROXY", ""},
		// AN UPPERCASE HOST IS NOT THE REFERENCE'S "localhost". Its built-in
		// exemption compares the literal lowercase name, so LOCALHOST keeps the
		// proxy there; lowercasing before that check would exempt it here and
		// hand a caller a way around the configured egress.
		{"an uppercase LOCALHOST is not the built-in exemption",
			strings.Replace(origin.URL, "127.0.0.1", "LOCALHOST", 1),
			"HTTP_PROXY=" + proxy.URL, "VIA-PROXY", ""},
		// 0.0.0.0 is NOT loopback to the reference, so the ordinary proxy rules
		// apply — and a direct connection to it is REFUSED at once rather than
		// waiting out a route that does not exist, which is what makes an
		// exclusion case affordable to assert.
		{"an exact IP literal in NO_PROXY does match", "http://0.0.0.0:1/x",
			"NO_PROXY=0.0.0.0", "", "VIA-PROXY"},
		// 127.1 IS NOT A LOOPBACK ADDRESS TO THE REFERENCE — net.ParseIP wants
		// four octets — so the proxy must still be used for it. Treating any
		// digits-and-dots host as loopback would drop the proxy here while
		// libcurl resolved the name to 127.0.0.1 anyway.
		{"an abbreviated 127.1 is not a parsed loopback IP",
			"http://127.1/x", "HTTP_PROXY=" + proxy.URL, "VIA-PROXY", ""},
		// A '@' IN THE QUERY IS NOT USERINFO. Read as userinfo, the host parses
		// as localhost, the loopback rule drops the proxy, and the request goes
		// direct to a host the deployment meant to route — so this asserts the
		// proxy is still used for a non-loopback target whose query contains one.
		{"an @ in the query does not make a host loopback",
			"http://oath-proxy-probe.invalid?next=@localhost",
			"HTTP_PROXY=" + proxy.URL, "VIA-PROXY", ""},
		// ALL_PROXY is libcurl's, not net/http's. The target is a reserved
		// .invalid name so the direct attempt fails immediately at DNS rather
		// than waiting out a TCP timeout; what is asserted is that the proxy was
		// NOT used, which the proxy's own body would reveal.
		{"ALL_PROXY is IGNORED, as the reference ignores it",
			"http://oath-proxy-probe.invalid/x", "ALL_PROXY=" + proxy.URL, "", "VIA-PROXY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// EVERY proxy variable is REMOVED from the inherited environment
			// before the case sets its own — appending an empty assignment is
			// not enough for ALL_PROXY, which libcurl consults on its own, and
			// a developer machine with any of these exported would otherwise
			// decide the result instead of the code under test.
			var env []string
			for _, kv := range os.Environ() {
				k := strings.ToUpper(kv[:strings.IndexByte(kv, '=')+1])
				switch k {
				case "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=":
				default:
					env = append(env, kv)
				}
			}
			env = append(env, tc.env)
			// The CGI case needs the proxy variable PRESENT and refused, so it
			// carries a second assignment rather than replacing the first.
			if strings.HasPrefix(tc.env, "REQUEST_METHOD=") ||
				strings.HasPrefix(tc.env, "NO_PROXY=") {
				env = append(env, "HTTP_PROXY="+proxy.URL)
			}
			g := exec.Command(goBin, tc.url)
			g.Env = env
			l := exec.Command(llvmBin, tc.url)
			l.Env = env
			gout, _ := g.CombinedOutput()
			lout, _ := l.CombinedOutput()
			if string(gout) != string(lout) {
				t.Fatalf("backends disagree under %s\n  go   = %q\n  llvm = %q", tc.env, gout, lout)
			}
			if tc.want != "" && !strings.Contains(string(lout), tc.want) {
				t.Fatalf("under %s expected %s, got %q", tc.env, tc.want, lout)
			}
			if tc.absent != "" && strings.Contains(string(lout), tc.absent) {
				t.Fatalf("under %s the response must NOT contain %s, got %q", tc.env, tc.absent, lout)
			}
		})
	}
}

// AN EXPANDED IPv6 LOOPBACK LITERAL IS STILL LOOPBACK.
//
// The reference parses the literal, so http://[0:0:0:0:0:0:0:1]/ is loopback to
// it and is never proxied. An exact-string test for "::1" misses that spelling
// and sends the request through the proxy — localhost traffic exposed to an
// egress hop the reference keeps local, and a different response from it.
func TestExpandedIPv6LoopbackIsNotProxied(t *testing.T) {
	requireClang(t)
	// THE LIBCURL GATE BELONGS ON EVERY TEST THAT BUILDS AN http_request PROGRAM,
	// and this one was written without it — so on a runner with clang but no
	// libcurl development files it reached buildLLVM and FATALED on the
	// provider's own refusal instead of skipping. That is what broke CI; the
	// refusal was correct and the test was not gated to expect it.
	requireUsableLibcurl(t)
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback on this host")
	}
	origin := &httptest.Server{
		Listener: ln,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ORIGIN-DIRECT"))
		})},
	}
	origin.Start()
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("VIA-PROXY"))
	}))
	defer proxy.Close()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	st := llvmStore(t)
	put(t, st, `(defn arg-at6 [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-at6 rest (- n 1))))))`)
	put(t, st, `(defn v6-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-at6 args 0))
		(prop is-the-fetch6 [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (v6-main net args) ((. net fetch) (arg-at6 args 0)))))`)
	goBin, _ := buildProgram(t, st, "v6-main")
	llvmBin := buildLLVM(t, st, "v6-main")
	skipUnlessHTTPProvisions(t, llvmBin, "http://[::1]:"+port+"/")

	for _, spelling := range []string{"[::1]", "[0:0:0:0:0:0:0:1]", "[0000:0000:0000:0000:0000:0000:0000:0001]"} {
		t.Run(spelling, func(t *testing.T) {
			url := "http://" + spelling + ":" + port + "/"
			env := append(os.Environ(), "HTTP_PROXY="+proxy.URL, "NO_PROXY=", "no_proxy=", "ALL_PROXY=")
			g := exec.Command(goBin, url)
			g.Env = env
			l := exec.Command(llvmBin, url)
			l.Env = env
			gout, _ := g.CombinedOutput()
			lout, _ := l.CombinedOutput()
			if string(gout) != string(lout) {
				t.Fatalf("backends disagree for %s\n  go   = %q\n  llvm = %q", url, gout, lout)
			}
			if strings.Contains(string(lout), "VIA-PROXY") {
				t.Fatalf("a loopback literal was proxied: %s", url)
			}
		})
	}
}

// A REDIRECT WHOSE BODY NEVER ARRIVES IS STILL FOLLOWED, PROMPTLY.
//
// The reference closes a redirect response whose advertised length exceeds its
// slurp limit without reading any of it, so a body that is large, slow, or
// simply never sent costs it nothing. Waiting for those bytes here would hang
// the program — there is no request timeout to end it — so the abort has to
// happen when the HEADERS end, before the body is awaited. The write callback
// cannot do it: for a body that never comes, it is never called.
//
// The assertion is a DEADLINE, and the margin is wide on purpose: the server
// holds the body far longer than the deadline allows, so a regression is a
// timeout rather than a close call.
func TestARedirectBodyThatNeverArrivesIsNotAwaited(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	release := make(chan struct{})
	defer close(release)
	mux := http.NewServeMux()
	mux.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("reached-the-target"))
	})
	// EXACTLY 2048 BYTES, THEN NOTHING — with no Content-Length, so the header
	// abort cannot fire and the decision falls to the write callback. The
	// reference stops as soon as it has read the limit; waiting for one more
	// byte hangs here forever.
	mux.HandleFunc("/stall-at-limit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/target")
		w.WriteHeader(http.StatusFound)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write(bytes.Repeat([]byte("z"), 2048))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})
	mux.HandleFunc("/slow-redirect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/target")
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusFound)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select { // the body is withheld until the test is over
		case <-release:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-ats [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-ats rest (- n 1))))))`)
	put(t, st, `(defn slow-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-ats args 0))
		(prop is-the-fetchs [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (slow-main net args) ((. net fetch) (arg-ats args 0)))))`)
	llvmBin := buildLLVM(t, st, "slow-main")
	skipUnlessHTTPProvisions(t, llvmBin, srv.URL+"/target")

	for _, path := range []string{"/slow-redirect", "/stall-at-limit"} {
		t.Run(path, func(t *testing.T) {
			done := make(chan struct{})
			var out []byte
			var err error
			go func() {
				out, err = exec.Command(llvmBin, srv.URL+path).CombinedOutput()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the program waited for a redirect body the reference never reads")
			}
			if err != nil {
				t.Fatalf("the redirect should have been followed: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "reached-the-target") {
				t.Fatalf("expected the redirect target's body, got %q", out)
			}
		})
	}
}

// A REDIRECT TARGET libcurl WILL NOT SEND IS REFUSED, NOT REWRITTEN.
//
// THIS IS A DOCUMENTED NON-AGREEMENT, and the only one in this file. A Location
// carrying a raw space or a bare non-ASCII byte is not a legal request target.
// The reference sends it anyway and surfaces whatever the server answers — for
// Go's own server, a 400. libcurl refuses to emit it at all.
//
// The two available behaviours were: normalize the target so the request
// succeeds (which is what curl_url did, silently sending a DIFFERENT request
// than the reference and reaching a handler the reference never reaches), or
// refuse. Refusing is the one this backend takes, for the same reason it refuses
// every construct it cannot lower faithfully: a backend that quietly rewrites a
// request is a semantic divergence wearing the costume of a success.
//
// So this asserts the residual rather than agreement — the LLVM artifact must
// return the capability failure value, and must NOT reach the echo handler.
func TestARedirectTargetThatCannotBeSentIsRefused(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/raw-query-redirect", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/echo-raw-query?q=a b")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/echo-raw-query", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "REACHED raw=%q", r.URL.RawQuery)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-atr [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-atr rest (- n 1))))))`)
	put(t, st, `(defn raw-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-atr args 0))
		(prop is-the-fetchr [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (raw-main net args) ((. net fetch) (arg-atr args 0)))))`)
	llvmBin := buildLLVM(t, st, "raw-main")
	skipUnlessHTTPProvisions(t, llvmBin, srv.URL+"/echo-raw-query")

	out, err := exec.Command(llvmBin, srv.URL+"/raw-query-redirect").CombinedOutput()
	if err != nil {
		t.Fatalf("the refusal is the capability failure value, not an exit: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "REACHED") {
		t.Fatalf("the target was rewritten and sent; it must be refused: %q", out)
	}
}

// DOT SEGMENTS IN A RELATIVE Location ARE RESOLVED BEFORE THE REQUEST IS SENT.
//
// A BARE HANDLER, NOT A ServeMux, AND THAT IS THE WHOLE POINT: ServeMux cleans
// the request path and redirects to the tidied form, so a backend that sent
// "/nest/deep/../../ok" would still end up at "/ok" and the two would agree by
// the SERVER's doing rather than the resolver's. Routed by hand, the path
// arrives exactly as it was sent and the difference is visible.
func TestDotSegmentsAreResolvedBeforeSending(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nest/deep/dots" {
			w.Header().Set("Location", "../../ok")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = fmt.Fprintf(w, "path=%q", r.URL.Path)
	}))
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-atd [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-atd rest (- n 1))))))`)
	put(t, st, `(defn dots-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-atd args 0))
		(prop is-the-fetchd [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (dots-main net args) ((. net fetch) (arg-atd args 0)))))`)
	goBin, _ := buildProgram(t, st, "dots-main")
	llvmBin := buildLLVM(t, st, "dots-main")
	skipUnlessHTTPProvisions(t, llvmBin, srv.URL+"/ok")

	url := srv.URL + "/nest/deep/dots"
	g, _ := exec.Command(goBin, url).CombinedOutput()
	l, _ := exec.Command(llvmBin, url).CombinedOutput()
	if string(g) != string(l) {
		t.Fatalf("backends disagree\n  go   = %q\n  llvm = %q", g, l)
	}
	if !strings.Contains(string(l), `path="/ok"`) {
		t.Fatalf("the dot segments were not resolved before sending: %q", l)
	}
}

// A FRAGMENT-ONLY Location KEEPS THE BASE QUERY.
//
// ResolveReference preserves ?token=x across Location: #next, so the second hop
// carries it. Dropping the query would send a different request — and can turn
// a redirect loop into a success, which is the sort of difference that only
// shows up against a server that reads its query.
//
// THIS NEEDS ITS OWN TEST BECAUSE THE SERVER MUST BE STATEFUL. The fragment is
// never sent, so the two hops are identical from the server's side and only a
// counter can tell them apart — and that counter has to be RESET between the two
// backends, or the second one starts mid-sequence and never sees the redirect at
// all. Shared with the table-driven cases, it silently compared two different
// journeys and passed no matter what the resolver did.
func TestAFragmentOnlyRedirectKeepsTheQuery(t *testing.T) {
	requireClang(t)
	requireUsableLibcurl(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Location", "#next")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = fmt.Fprintf(w, "second-hop-query=%q", r.URL.RawQuery)
	}))
	defer srv.Close()

	st := llvmStore(t)
	put(t, st, `(defn arg-atf [] [(args (List Str)) (n Int)] Str
		(match args ((Nil) "") ((Cons h rest) (if (== n 0) h (arg-atf rest (- n 1))))))`)
	put(t, st, `(defn frag-main [] [(net {fetch (-> Str Str)}) (args (List Str))] Str
		((. net fetch) (arg-atf args 0))
		(prop is-the-fetchf [(net {fetch (-> Str Str)}) (args (List Str))]
			(== (frag-main net args) ((. net fetch) (arg-atf args 0)))))`)
	goBin, _ := buildProgram(t, st, "frag-main")
	llvmBin := buildLLVM(t, st, "frag-main")

	url := srv.URL + "/frag-start?token=x"
	atomic.StoreInt32(&hits, 0)
	g, _ := exec.Command(goBin, url).CombinedOutput()
	atomic.StoreInt32(&hits, 0)
	l, _ := exec.Command(llvmBin, url).CombinedOutput()

	if strings.Contains(string(l), "http_request cannot be provided") ||
		strings.Contains(string(l), "libcurl") {
		t.Skipf("this host's libcurl does not satisfy the provider: %s", l)
	}
	if string(g) != string(l) {
		t.Fatalf("backends disagree\n  go   = %q\n  llvm = %q", g, l)
	}
	if !strings.Contains(string(l), `second-hop-query="token=x"`) {
		t.Fatalf("the base query was not carried across the fragment redirect: %q", l)
	}
}
