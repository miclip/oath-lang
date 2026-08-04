package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// The claim is not "some malformed requests are refused" but that NO accepted
// request can decode to a string the wire bytes did not denote. So the table
// asserts BOTH directions on the same function, and the accept rows are the
// half that catches an over-broad check.
func TestRejectLossyJSON(t *testing.T) {
	for _, tc := range []struct {
		why    string
		raw    string
		reject bool
	}{
		// --- must be accepted ---
		{"plain ascii", `{"source":"(defn f [] [] Int 1)"}`, false},
		{"multibyte UTF-8 direct", `{"source":"café ✓ 🔒"}`, false},
		{"a well-formed surrogate PAIR is lossless", `{"source":"A🔒B"}`, false},
		{"an uppercase-hex surrogate pair", `{"source":"A🔒B"}`, false},
		{"two pairs in a row", `{"source":"🔒🔒"}`, false},
		{"a legitimate escaped U+FFFD", `{"source":"A�B"}`, false},
		{"a BMP escape near the surrogate range", `{"source":"A퟿BC"}`, false},
		// The vector a substring search for `\u` gets wrong: this is an escaped
		// BACKSLASH followed by the ordinary letters uD800, not an escape at all.
		{"an escaped backslash then literal uD800", `{"source":"A\\uD800B"}`, false},
		{"a quote inside the string before an escape", `{"source":"say \" then 🔒"}`, false},
		{"empty arguments object", `{}`, false},

		// --- must be refused ---
		{"a lone high surrogate", `{"source":"A\ud800B"}`, true},
		{"a different lone high surrogate", `{"source":"A\ud801B"}`, true},
		{"a lone low surrogate", `{"source":"A\udc00B"}`, true},
		{"a high surrogate followed by a NON-low escape", `{"source":"A\ud800AB"}`, true},
		{"a high surrogate at end of string", `{"source":"A\ud800"}`, true},
		{"a lone surrogate in the envelope field", `{"envelope":"x\udfffy"}`, true},
		{"raw invalid start byte", "{\"source\":\"A\xffB\"}", true},
		{"raw lone continuation byte", "{\"source\":\"A\x80B\"}", true},
		{"raw truncated 3-byte sequence", "{\"source\":\"A\xe2\x9cB\"}", true},
	} {
		err := rejectLossyJSON([]byte(tc.raw))
		if tc.reject && err == nil {
			t.Errorf("%s: accepted, but decoding it substitutes U+FFFD", tc.why)
		}
		if !tc.reject && err != nil {
			t.Errorf("%s: refused a lossless request, so the check is over-broad: %v", tc.why, err)
		}
	}
}

// The check and the decoder must agree about which inputs are lossy — asserted
// against encoding/json itself rather than against my reading of it, so a change
// in Go's behaviour shows up here instead of silently widening the gap.
//
// Accepted documents may still contain U+FFFD, but only where the wire bytes
// actually said so; the test therefore compares against the RAW text rather than
// merely checking for the rune's absence.
func TestAcceptedJSONNeverGainsAReplacementCharacter(t *testing.T) {
	for _, raw := range []string{
		`{"source":"café ✓ 🔒"}`,
		`{"source":"A🔒B"}`,
		`{"source":"A\\uD800B"}`,
		`{"source":"A�B"}`, // legitimately asks for U+FFFD
	} {
		if err := rejectLossyJSON([]byte(raw)); err != nil {
			t.Fatalf("control refused, so this test proves nothing: %q: %v", raw, err)
		}
		var a struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			t.Fatalf("unmarshal %q: %v", raw, err)
		}
		gained := strings.ContainsRune(a.Source, utf8.RuneError)
		asked := strings.Contains(strings.ToLower(raw), `�`) ||
			strings.ContainsRune(raw, utf8.RuneError)
		if gained && !asked {
			t.Errorf("%q decoded to %q, which contains U+FFFD the request never asked for",
				raw, a.Source)
		}
	}

	// And the converse: every rejected vector must be one the decoder really
	// would have mangled. A rule refusing inputs json handles correctly would be
	// a compatibility break dressed as a safety check.
	for _, raw := range []string{
		`{"source":"A\ud800B"}`,
		`{"source":"A\udc00B"}`,
		"{\"source\":\"A\xffB\"}",
	} {
		if err := rejectLossyJSON([]byte(raw)); err == nil {
			t.Fatalf("%q was accepted; the rejection half of this test is vacuous", raw)
		}
		var a struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			continue // refused by the decoder too: also fine
		}
		if !strings.ContainsRune(a.Source, utf8.RuneError) {
			t.Errorf("%q was refused but encoding/json decodes it losslessly to %q — "+
				"the check is refusing something that is not actually lossy", raw, a.Source)
		}
	}
}

// End to end through the surface that publishes, because the unit tests above
// only establish that the checker works on bytes handed to it — not that the
// bytes reaching mcpCallTool are still the wire bytes. Both transports pass
// arguments as json.RawMessage, which is a claim about encoding/json worth
// witnessing rather than reading.
//
// The CONTROL is the same request with a valid astral character written as a
// surrogate pair: it must still publish, or this would be evidence that `put`
// is broken rather than that lossy input is refused.
func TestMCPPutRefusesLossySourceBeforeItCanBePublished(t *testing.T) {
	st := newStore(t)

	control := []byte(`{"source":"(data Str [] (SNil) (SCons Int Str))"}`)
	if _, err := mcpCallTool(st, "put", control, "bob", true, false, false); err != nil {
		t.Fatalf("control put failed, so the refusals below prove nothing: %v", err)
	}

	for _, tc := range []struct{ why, args string }{
		{"a lone high surrogate escape", `{"source":"(defn q [] [] Str \"A\ud800B\")"}`},
		{"a different lone high surrogate", `{"source":"(defn q [] [] Str \"A\ud801B\")"}`},
		{"raw malformed bytes", "{\"source\":\"(defn q [] [] Str \\\"A\xffB\\\")\"}"},
	} {
		out, err := mcpCallTool(st, "put", []byte(tc.args), "bob", true, false, false)
		if err == nil {
			t.Errorf("%s: put succeeded (%s); the substitution would be in the hash", tc.why, out)
			continue
		}
		if !strings.Contains(err.Error(), "U+FFFD") {
			t.Errorf("%s: refused for some other reason: %v", tc.why, err)
		}
	}

	// Nothing was published by the refused calls.
	if _, ok := st.Resolve("q"); ok {
		t.Error("a definition was published from a request that had to be refused")
	}
}

// The collapse, stated as the property rather than as its instances: distinct
// request bodies must not produce one accepted source string.
func TestDistinctLossyRequestsCannotCollapseToOneSource(t *testing.T) {
	bodies := []string{
		"{\"source\":\"A\xffB\"}",
		"{\"source\":\"A\xfeB\"}",
		`{"source":"A\ud800B"}`,
		`{"source":"A\ud801B"}`,
	}
	decoded := map[string][]string{}
	for _, raw := range bodies {
		var a struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			continue
		}
		decoded[a.Source] = append(decoded[a.Source], raw)
	}
	// Establish the hazard is real before asserting it is closed: if these did
	// NOT collide, the guard below would pass for the wrong reason.
	collided := false
	for _, srcs := range decoded {
		if len(srcs) > 1 {
			collided = true
		}
	}
	if !collided {
		t.Fatal("encoding/json no longer collapses these bodies, so this test no longer " +
			"witnesses the hazard it was written for — re-derive the vectors")
	}
	for _, raw := range bodies {
		if err := rejectLossyJSON([]byte(raw)); err == nil {
			t.Errorf("%q accepted; it collapses onto the same source as another body", raw)
		}
	}
}
