//go:build js && wasm

package main

// The browser playground (#34). This exposes the REAL kernel to a web page: the
// same parse → elaborate → typecheck gate → deterministic verification →
// termination/confinement analyses that `oath put` runs, compiled to wasm and
// called from JavaScript. Nothing is reimplemented and nothing is mocked; the
// page is a front-end for this binary.
//
// Two things are NOT available here and the result says so rather than
// pretending otherwise:
//   - PROOF. `oath prove` drives z3 as a subprocess, which does not exist in a
//     browser. A pasted definition whose content hash matches one the corpus has
//     already proven can still report that RECORDED verdict — content addressing
//     is exactly what makes that honest — but a novel definition gets tested,
//     not proven, and is labelled that way.
//   - A real filesystem. The store is served from an in-memory filesystem
//     provided by the host page, so the unmodified kernel's os.ReadFile calls
//     resolve against the embedded corpus snapshot.

import (
	"encoding/json"
	"syscall/js"
)

// isWasm makes main() park instead of running the CLI argument parser: the
// registration below has already published the entry point, and the Go runtime
// must stay alive for JavaScript to call it.
var isWasm = true

// playgroundResult is the JSON handed back to the page. It mirrors the CLI's
// putReport rather than inventing a second vocabulary, so what a visitor sees
// is what `oath put` prints.
type playgroundResult struct {
	OK      bool        `json:"ok"`
	Error   string      `json:"error,omitempty"`
	Reports []putReport `json:"reports,omitempty"`
	Notes   []string    `json:"notes,omitempty"`
}

func init() {
	js.Global().Set("oathCheck", js.FuncOf(oathCheck))
	js.Global().Set("oathKernelVersion", js.ValueOf(kernelVersion))
}

// oathCheck(storeRoot, source) runs the gate over `source` against the corpus
// mounted at `storeRoot`, returning JSON. Errors are returned as data — a
// rejected definition is an ordinary outcome of the gate, not a crash.
func oathCheck(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return mustJSON(playgroundResult{Error: "oathCheck(storeRoot, source) requires two arguments"})
	}
	root, src := args[0].String(), args[1].String()

	st, err := OpenStore(root)
	if err != nil {
		return mustJSON(playgroundResult{Error: "could not open corpus: " + err.Error()})
	}

	// The real gate. A parse/elaboration failure returns whatever reports were
	// produced before it plus the error, exactly as the CLI does — a visitor
	// pasting a broken definition should see the kernel's own message.
	reports, err := apiPut(st, src, "playground", "")
	res := playgroundResult{OK: err == nil, Reports: reports}
	if err != nil {
		res.Error = err.Error()
	}
	res.Notes = append(res.Notes,
		"Proof requires z3 and is not available in the browser: novel definitions report `tested` (200 deterministic, hash-seeded cases per property), never `proven`.")
	return mustJSON(res)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"could not encode result"}`
	}
	return string(b)
}
