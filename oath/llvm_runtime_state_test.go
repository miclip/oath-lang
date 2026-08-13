package main

import (
	"regexp"
	"strings"
	"testing"
)

// THE EMITTED RUNTIME DECLARES NO STORAGE THAT COULD HOLD A REQUEST POINTER (#165).
//
// The memory design for this backend is arena-shaped: everything a request
// allocates is released once its answer has been serialised. That is sound only
// while nothing survives the call that produced it — and the load-bearing case
// is a CAPABILITY, because a capability is the only thing an Oath program hands
// to the host. `o_cap_emit` opens, writes and closes inside the call today; a
// later batching sink that buffered its argument would break the arena silently,
// with no test failing and no comment contradicted.
//
// WHAT THIS ESTABLISHES, AND WHAT IT DOES NOT. It checks DECLARATIONS: no object
// of static storage duration exists that could hold a pointer. It does NOT
// establish that no capability retains request memory, and the difference is not
// pedantic — a provider could call `putenv(o_cstr(arg))`, which keeps the buffer
// it is handed, or pass `arg` to a worker thread, declaring nothing. Neither is
// visible here.
//
// So this closes the storage route and names the other one rather than implying
// it is covered. Retention through a LIBC CALL or a thread handoff needs an
// audit at the capability boundary, and `docs/experiments/issue-165-memory.md`
// records it as open.
//
// THE UNIVERSE IS STATIC STORAGE DURATION, NOT COLUMN ZERO. A first version of
// this test scanned file-scope lines only, so `static OVal *stash;` written
// INSIDE `cap_emit_code` — which is how a batching sink would actually be
// written, and which has static storage duration all the same — passed it. The
// claim is about what outlives a call, so the scan is about `static`, at any
// indentation.
func TestEmittedRuntimeDeclaresNoStaticStorageForValues(t *testing.T) {
	rt := llvmRuntimeC
	if len(rt) < 2000 {
		t.Fatalf("the runtime source is %d bytes, so this test is not reading what it "+
			"believes it is", len(rt))
	}

	// CONTROLS, written in the forms the defect would ARRIVE in rather than the
	// form the scanner finds easiest. Both defeated an earlier version.
	for _, probe := range []struct{ name, line string }{
		{"file-scope with a trailing comment", "static OVal *o_stash;   /* a batching sink */"},
		{"function-local static", "  static OVal *stash = 0;"},
	} {
		got := staticVars(rt + "\n" + probe.line + "\n")
		if len(got) == 0 || got[len(got)-1].name == "" {
			t.Fatalf("the scanner does not see an injected %s, so its silence on the real "+
				"runtime is not evidence", probe.name)
		}
	}

	for _, v := range staticVars(rt) {
		if v.name == "" {
			t.Errorf("a `static` declaration this scanner cannot parse: %s\n"+
				"An UNRECOGNISED form is a FAILURE, not a pass. The regex reads a "+
				"single-word type, so `static unsigned char *stash;` or `static struct "+
				"Holder stash;` would otherwise vanish — and vanishing is indistinguishable "+
				"from absent, which is the whole defect this test exists to prevent.", v.decl)
			continue
		}
		t.Errorf("the emitted runtime declares static storage %q (%s). A capability could "+
			"retain a request's memory there, which the arena design forbids. If this is "+
			"deliberate it needs a lifetime argument, not just a declaration", v.name, v.decl)
	}
}

type staticVar struct{ name, decl string }

// staticVars finds declarations with STATIC STORAGE DURATION in C source, at any
// indentation: `static <type> <name>` that is not a function. A scanner rather
// than a parser, which is why the callers inject before trusting its silence.
func staticVars(src string) []staticVar {
	decl := regexp.MustCompile(`^static\s+(?:const\s+)?[A-Za-z_][A-Za-z0-9_]*[\s*]+(?:volatile\s+)?\**([A-Za-z_][A-Za-z0-9_]*)\s*(?:=[^;]*)?(?:\[[^\]]*\])?\s*;`)
	strip := func(l string) string {
		if i := strings.Index(l, "/*"); i >= 0 {
			l = l[:i]
		}
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		return strings.TrimRight(l, " \t")
	}
	var out []staticVar
	// BRACE DEPTH, NOT INDENTATION. C scope is not whitespace: a file-scope
	// declaration written with a leading space still has static storage duration,
	// and inferring scope from the left margin missed it.
	inComment := false
	for _, raw := range strings.Split(src, "\n") {
		// BLOCK COMMENTS SPAN LINES, and their interiors are ordinary prose that
		// no declaration regex can read. Without this, every continuation line of
		// a multi-line comment was reported as an unparsable declaration — the
		// fail-closed default turning into noise, which is its own way of being
		// ignored.
		line := raw
		if inComment {
			if i := strings.Index(line, "*/"); i >= 0 {
				line, inComment = line[i+2:], false
			} else {
				continue
			}
		}
		for {
			i := strings.Index(line, "/*")
			if i < 0 {
				break
			}
			if j := strings.Index(line[i:], "*/"); j >= 0 {
				line = line[:i] + line[i+j+2:]
				continue
			}
			line, inComment = line[:i], true
			break
		}
		stripped := strip(line)
		l := strings.TrimLeft(stripped, " \t")
		// A FUNCTION HAS ITS PARENTHESIS BEFORE ANY `=`; AN OBJECT MAY HAVE ONE
		// AFTER. Skipping every parenthesised line let `static OVal *stash =
		// (OVal *)0;` through — a cast in the initialiser, which is ordinary C
		// and is storage all the same. Only the declarator is tested.
		declarator := l
		if i := strings.Index(l, "="); i >= 0 {
			declarator = l[:i]
		}
		// A FUNCTION'S PARENTHESIS FOLLOWS AN IDENTIFIER; AN OBJECT'S DOES NOT.
		// `cap_env_code(OVal **env` is a function. `static OVal *(stash);` is a
		// parenthesised declarator — valid C, an ordinary pointer object, and a
		// place to retain — and treating every parenthesis as a function skipped
		// it. This is a scanner, not a C parser, and that is why an unrecognised
		// `static` line FAILS rather than passing: the forms it cannot read are
		// reported instead of vanishing.
		isFunc := false
		// An ARRAY BOUND may contain a parenthesised expression —
		// `static OVal *stash[sizeof(OVal *)];` — and `sizeof` is an identifier,
		// so the identifier-before-paren rule alone called it a function. A `[`
		// ahead of the first `(` settles it: that is a declarator, not a
		// parameter list.
		if b := strings.Index(declarator, "["); b >= 0 {
			if pi := strings.Index(declarator, "("); pi < 0 || b < pi {
				declarator = declarator[:b]
			}
		}
		if i := strings.Index(declarator, "("); i > 0 {
			c := declarator[i-1]
			isFunc = c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		}
		if l == "" || isFunc || strings.HasPrefix(l, "typedef") ||
			strings.HasPrefix(l, "#") || strings.HasPrefix(l, "}") {
			continue
		}
		// A TYPE DEFINITION IS NOT STORAGE. `enum { ... };` and `struct OVal {`
		// declare types and allocate nothing; `struct Holder stash;` declares an
		// OBJECT and does. The brace is what separates them, so it is the test —
		// not the leading keyword, which both share.
		if strings.Contains(l, "{") &&
			(strings.HasPrefix(l, "enum") || strings.HasPrefix(l, "struct") || strings.HasPrefix(l, "union")) {
			continue
		}
		// STATIC STORAGE DURATION, not the `static` KEYWORD. A file-scope object
		// gets it without the keyword — `OVal *stash;` at column 0 is persistent
		// mutable storage — so the universe is: anything with the keyword at any
		// indentation, plus anything declared at file scope at all.
		// SCOPE BY INDENTATION, WITH THE LIMIT STATED. Brace counting was tried
		// and made this worse: multi-line function signatures and block comments
		// both look like file-scope declarations to a line scanner, so the
		// fail-closed default turned into noise. A KNOWN, NARROW gap beats an
		// unbounded one — a file-scope declaration written with leading
		// whitespace is missed here, and the emitted runtime writes none.
		atFileScope := stripped == strings.TrimLeft(stripped, " \t")
		// `static` ANYWHERE IN THE QUALIFIERS, not only as a prefix.
		// `_Thread_local static OVal *stash;` is block-scoped, outlives the call
		// exactly as a plain function-local static does, and does not begin with
		// the keyword.
		hasStatic := strings.HasPrefix(l, "static ") || strings.Contains(l, " static ")
		if !hasStatic && !atFileScope {
			continue
		}
		// A NON-POINTER OBJECT CANNOT HOLD ARENA MEMORY. A future `static const
		// o_u32 masks[]` lookup table is storage, but nothing about it can retain
		// a request pointer, and failing it would make this test obstruct
		// unrelated work while defending nothing. The line is the pointer, not
		// the qualifier: `static const char *volatile o_kept` WAS const and was
		// exactly the hazard.
		if !strings.Contains(declarator, "*") && strings.Contains(l, "const") {
			continue
		}
		// EVERY non-function `static` line produces an entry. A line the regex
		// cannot read yields name "" so the caller REPORTS it — skipping it here
		// is what made `static unsigned char *stash;` vanish, and a scanner that
		// drops what it does not understand reports a clean runtime forever.
		if m := decl.FindStringSubmatch(l); m != nil {
			out = append(out, staticVar{name: m[1], decl: strings.TrimSpace(raw)})
		} else {
			out = append(out, staticVar{name: "", decl: strings.TrimSpace(raw)})
		}
	}
	return out
}
