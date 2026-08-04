package main

import (
	"fmt"
	"unicode/utf8"
)

// REFUSING A JSON DOCUMENT THAT CANNOT BE READ WITHOUT SUBSTITUTION (#133).
//
// Go's encoding/json answers U+FFFD for two different inputs — bytes that are
// not valid UTF-8, and a `\uXXXX` escape naming an unpaired surrogate — and
// reports no error for either. Both are lossy in the sense this project cannot
// tolerate: distinct documents decode to one string, and downstream that string
// is elaborated into a definition whose HASH is its identity.
//
// The check runs on the wire bytes because after decoding the evidence is gone:
// the substituted string is valid UTF-8 and indistinguishable from a document
// that legitimately contained U+FFFD.
//
// It is deliberately not "reject all surrogate escapes". A PAIR is lossless —
// `🔒` denotes U+1F512 exactly — and ASCII-safe encoders emit pairs
// routinely, so refusing them would reject correct clients for no gain. Only the
// unpaired case loses information, so only it is refused.

// hex4 reads exactly four hex digits.
func hex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var v rune
	for _, c := range b[:4] {
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = rune(c - '0')
		case c >= 'a' && c <= 'f':
			d = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = rune(c-'A') + 10
		default:
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

func isHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func isLowSurrogate(r rune) bool  { return r >= 0xDC00 && r <= 0xDFFF }

// loneSurrogateEscapeAt reports the offset of the first `\uXXXX` escape naming an
// unpaired surrogate, or -1. Escapes only mean anything inside a string, and a
// preceding `\\` is a literal backslash rather than the start of an escape, so
// the scan tracks both rather than searching for the substring `\u` — which
// would report a false positive on the perfectly ordinary source text `"\\u..."`.
func loneSurrogateEscapeAt(raw []byte) int {
	inString := false
	for i := 0; i < len(raw); i++ {
		if !inString {
			if raw[i] == '"' {
				inString = true
			}
			continue
		}
		switch raw[i] {
		case '"':
			inString = false
			continue
		case '\\':
		default:
			continue
		}
		// raw[i] == '\\': consume the escape.
		if i+1 >= len(raw) {
			return -1 // truncated; json.Unmarshal will report it
		}
		if raw[i+1] != 'u' {
			i++ // \" \\ \/ \b \f \n \r \t — skip the escaped byte
			continue
		}
		hi, ok := hex4(raw[i+2:])
		if !ok {
			i++ // malformed \u escape; json.Unmarshal will report it
			continue
		}
		if isLowSurrogate(hi) {
			return i // a low surrogate with no high before it
		}
		if isHighSurrogate(hi) {
			if i+7 < len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' {
				if lo, ok2 := hex4(raw[i+8:]); ok2 && isLowSurrogate(lo) {
					i += 11 // a well-formed pair: consume both escapes
					continue
				}
			}
			return i
		}
		i += 5
	}
	return -1
}

// rejectLossyJSON refuses a document whose decoding would invent codepoints.
func rejectLossyJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("request is not valid UTF-8: decoding it would substitute U+FFFD, " +
			"and distinct requests would then produce one definition")
	}
	if at := loneSurrogateEscapeAt(raw); at >= 0 {
		return fmt.Errorf("request contains an unpaired surrogate escape at byte %d: "+
			"decoding it would substitute U+FFFD, and distinct requests would then "+
			"produce one definition. A surrogate PAIR is fine; a lone half denotes nothing", at)
	}
	return nil
}
