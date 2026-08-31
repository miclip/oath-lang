package main

// The BACKEND-NEUTRAL compiled program (#114 / effects stage 4).
//
// WHY THIS FILE EXISTS AT ALL. `compile.go` emits Go. Before this file, it also
// DEFINED what a capability is: `capWiring` switched on a field name and returned
// Go source, so "which authority does this program require" and "how does Go
// supply it" were the same string. That makes the Go runtime model the implicit
// specification of compiled Oath — the exact failure the architectural constraint
// on #114 forbids:
//
//	No new language or capability semantics may be defined in terms of Go
//	constructs.
//
// So the dependency direction is pinned:
//
//	Oath semantics -> neutral requirements -> backend provider
//
// and never
//
//	Go provider switch -> implied Oath semantics
//
// Nothing in this file may emit, mention, or assume Go. It says WHAT a compiled
// artifact requires; `compile.go` (the Go backend) says HOW one host satisfies it,
// and a later backend (#115) supplies a different provider table against these
// same requirements.
//
// WHAT IS DELIBERATELY NOT HERE. There is no expression IR. The neutral
// representation of a program BODY already exists and is the thing this whole
// system is built on: the verified `Def` closure in canonical de Bruijn form.
// Inventing a `CoreExpr` that mirrors `Term` would add a second description of
// the same thing without answering any question capability wiring asks. Typed IR,
// monomorphisation and layout are #115's scope, and they are compiler concerns
// rather than language ones. What was genuinely missing — and is here — is
// everything AROUND the body: the entry shape as data, the required authority as
// data, and the provenance manifest.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// provenanceSchema versions the manifest embedded in every compiled artifact.
// It is a published surface — something reading a binary's provenance is reading
// this shape — so it is versioned like the other protocol envelopes.
const provenanceSchema = "oath-provenance/1"

// ---------- entry protocols (LANGUAGE-LEVEL) ----------
//
// What counts as an entry point, and which protocol it speaks, is a fact about
// Oath types — it lives here rather than next to the emitter that consumes it.
// These moved out of compile.go after #114: their CONTENT was already neutral
// (they classify a *Ty and emit nothing), but a neutral layer reaching into the
// file named "the Go backend" leaves the boundary true in substance and soft in
// structure, which is exactly the shape a second backend would trip over.

// EntryShape is the WHOLE of what a backend must know about an entry point's
// interface: which PROTOCOL it speaks and whether it takes a leading capability
// record. It is one variant rather than two independent facts because the two
// were previously read from two different places — `Protocol` for the protocol
// and `CapTy != nil` for the arity — and a *Ty consulted for its nil-ness is a
// TYPE being used as a flag. That works exactly until something else wants to
// know the arity and reads the requirement count instead, which is the bug both
// backends carry a comment about: an entry typed (-> {} (-> (List Str) Str))
// requires nothing and still takes a capability argument.
//
// Ingress is deliberately a protocol rather than a capability. A capability is
// outbound authority the program HOLDS and may misuse — hence the confinement
// checker. Being called is not authority: the host owns the socket, decides when
// to invoke, and the artifact stays a pure function of the value it is handed.
// So a handler needs no new capability, and inherits confinement checking,
// verification against every generated request, and the refusal-to-wire gates
// exactly as the CLI protocols do.
//
// THE POINT OF NAMING THE COMBINATIONS rather than carrying a protocol and a
// bool: a backend cannot answer for a shape by accident. Every shape-keyed
// decision is a TABLE INDEXED BY THE VARIANT whose length the compiler checks
// against numEntryShapes, so adding a fifth shape here fails to COMPILE in every
// backend until each one says what the new shape means — including saying it
// refuses it. Neither backend can be extended by inference.
type EntryShape int

const (
	shapeCLI EntryShape = iota
	shapeCLICaps
	shapeHandler
	shapeHandlerCaps
	shapeCLIResult
	shapeCLICapsResult

	// numEntryShapes is the TERMINAL SENTINEL and the single authority on the
	// variant's size. It must stay last: a shape declared after it would not be
	// counted, and every coverage check here is measured against this number.
	//
	// It exists because the alternative — a hand-written list of the shapes —
	// is itself a place a new shape can be omitted, and omitting it there leaves
	// every "does this table decide every shape?" check passing while measuring
	// a universe smaller than the variant. A count the compiler derives from the
	// iota block cannot be forgotten, which a list can.
	numEntryShapes
)

// entryShapes is the variant's universe, DERIVED from the sentinel rather than
// written out. Used where iteration reads better than an index loop; it is not
// an independent authority and cannot disagree with numEntryShapes.
func entryShapes() []EntryShape {
	out := make([]EntryShape, 0, numEntryShapes)
	for s := EntryShape(0); s < numEntryShapes; s++ {
		out = append(out, s)
	}
	return out
}

// shapeNames renders the variant for messages. Like every shape-keyed table it
// is indexed by the variant and length-checked below, so a shape added without a
// name fails to compile rather than printing as a bare integer at the moment
// someone is trying to read an error about it.
var shapeNames = [...]string{
	shapeCLI:           "cli",
	shapeCLICaps:       "cli-with-capabilities",
	shapeHandler:       "handler",
	shapeHandlerCaps:   "handler-with-capabilities",
	shapeCLIResult:     "cli-result",
	shapeCLICapsResult: "cli-result-with-capabilities",
}

// entryShapeTable is the COMPILE-TIME exhaustiveness assertion every shape-keyed
// table declares against its own length:
//
//	var _ entryShapeTable = [len(myTable)]struct{}{}
//
// Two array types are identical only when their lengths are, so a table with a
// row missing — or a stale table carrying one too many — is a TYPE ERROR at the
// declaration, in the file that owns the decision. No test has to run, no build
// has to reach the missing case, and nothing has to remember to look. The
// assertion names this type, so the compiler's message names the obligation.
//
// This is why the tables are `[...]T{shapeX: …}` arrays rather than maps. A map's
// totality is a run-time property at best: a map missing a row answers with the
// zero value, which for a backend means silently doing whatever its zero row
// happens to say, at the first build that reaches the missing case.
//
// THE RESIDUAL GAP, stated because a length check is not a coverage check: a
// table can reach the right LENGTH while leaving a MIDDLE index at its zero
// value, and for at least one row the zero value is a legitimate answer, so it
// cannot be rejected on sight. Each table therefore also names the shape each row
// decides, checked position-by-position — see TestShapeTablesAreIndexedByShape.
// The length is the compile-time half and the index check is the run-time half;
// neither subsumes the other.
type entryShapeTable = [numEntryShapes]struct{}

var _ entryShapeTable = [len(shapeNames)]struct{}{}

// String never fails, because a renderer that can fail is unusable inside the
// error messages that report the failure. Coverage of shapeNames is a compile
// error; this fallback is for a value outside the variant, which is a runtime
// possibility because EntryShape is an integer type.
func (s EntryShape) String() string {
	if s < 0 || int(s) >= len(shapeNames) {
		return fmt.Sprintf("entry-shape-%d", int(s))
	}
	return shapeNames[s]
}

// entryShapeCase selects one shape's case from a table that is TOTAL BY
// CONSTRUCTION — the compiler has already established that, at the table's
// declaration, so nothing is checked here about coverage.
//
// What remains is the one thing the compiler cannot establish: EntryShape is an
// integer type, so a value outside the variant can be constructed and reach an
// index. That is rejected rather than allowed to index out of range, because a
// panic in a build reads as a compiler crash rather than as a program this
// backend has no case for.
func entryShapeCase[T any](owner string, table *[numEntryShapes]T, s EntryShape) (T, error) {
	if s < 0 || s >= numEntryShapes {
		var zero T
		return zero, fmt.Errorf("%s was asked for %s, which is not an entry shape this build defines", owner, s)
	}
	return table[s], nil
}

// classifyEntry classifies an entry type, returning the capability record (nil if
// the entry takes none), its shape, and whether it is an entry at all.
// Recognized, one per shape:
//
//	(-> (List Str) Str)               shapeCLI
//	(-> {caps} (-> (List Str) Str))   shapeCLICaps
//	(-> Request Response)             shapeHandler
//	(-> {caps} (-> Request Response)) shapeHandlerCaps
//
// The record type is returned for entryRequirements to read; it is deliberately
// NOT carried on CompiledProgram, because a backend holding it would read its
// nil-ness for the arity and the shape would stop being the single answer.
func classifyEntry(st *Store, t *Ty) (*Ty, EntryShape, bool) {
	if isPureEntry(st, t) {
		return nil, shapeCLI, true
	}
	if isResultEntry(st, t) {
		return nil, shapeCLIResult, true
	}
	if isHandlerEntry(st, t) {
		return nil, shapeHandler, true
	}
	if t != nil && t.K == "fun" && t.A != nil && t.A.K == "record" {
		if isPureEntry(st, t.B) {
			return t.A, shapeCLICaps, true
		}
		if isResultEntry(st, t.B) {
			return t.A, shapeCLICapsResult, true
		}
		if isHandlerEntry(st, t.B) {
			return t.A, shapeHandlerCaps, true
		}
	}
	return nil, shapeCLI, false
}

// isHandlerEntry recognizes (-> Request Response) by the STRUCTURE of the two
// types, per SPEC §14.1a: a store may bind them to any name or to none, and
// binding the name `Request` to an unrelated type must not produce a handler.
//
// This replaced a by-name resolution (`isNamedData(st, "Request", …)`), which
// failed in both directions: a valid handler whose types were bound under other
// names was refused, and an unrelated type bound to that name was compiled as a
// handler and then handed the adapter's five-field Request value — with, through
// `oath build`, the real world attached.
func isHandlerEntry(st *Store, t *Ty) bool {
	if t == nil || t.K != "fun" {
		return false
	}
	protoInit()
	return isProtoData(t.A, protoRequest) && isProtoData(t.B, protoResponse)
}

func isNamedData(st *Store, name string, t *Ty) bool {
	h, ok := st.Resolve(name)
	return ok && t != nil && t.K == "data" && t.Hash == h && len(t.Args) == 0
}

// strTypeHash is the hash of this store's string type — the identity both backends
// lower as a native string, so recognition (entry shapes, str-map keys) and lowering
// share ONE hash and cannot diverge.
//
// If the store NAMES a string type `Str`, that binding is authoritative: a store may
// deliberately repoint `Str` to another shape, and the family keyed on the superseded
// one is then no longer the active string (#184). But a store need NOT bind the bare
// name: when nothing is called `Str`, fall back to the CANONICAL Str CONTENT HASH
// (`(data Str [] (SNil) (SCons Int Str))`, == protoStr). That is what lets a program
// typed entirely against a namespaced `michael/Str` — byte-identical, same hash —
// build without ALSO binding a redundant bare `Str`, which was friction item 4. In
// the committed corpus `Str` resolves to exactly protoStr, so the two agree and
// nothing changes; the fallback only fires for a store that names no string type at
// all. This mirrors how the entry's `List` is recognised structurally (protoList),
// which likewise needs no bare `List` binding.
func strTypeHash(st *Store) string {
	if h, ok := st.Resolve("Str"); ok {
		return h
	}
	protoInit()
	return protoStr
}

func isStrTy(strHash string, t *Ty) bool {
	return strHash != "" && t != nil && t.K == "data" && t.Hash == strHash
}

func isPureEntry(st *Store, t *Ty) bool {
	sh := strTypeHash(st)
	return t != nil && t.K == "fun" && isStrTy(sh, t.B) && isListStrArg(st, t.A)
}

// isListStrArg reports whether a is (List Str) — the argument every CLI entry
// shape takes. Extracted so the CLI, capability, and exit-result shapes share
// ONE definition of "the argument is the command line" rather than three copies
// that could drift.
func isListStrArg(st *Store, a *Ty) bool {
	protoInit()
	sh := strTypeHash(st)
	// a.Hash == protoList requires the CANONICAL List structure — Nil at index 0,
	// Cons at index 1 — which is exactly what the emitted argv construction
	// assumes (both the CLI and the exit-result mains build Nil=0/Cons=1
	// directly, while the LLVM backend derives the indices). A store that
	// reordered List's constructors, or bound the name `List` to some other
	// datatype, hashes differently and is REJECTED here rather than lowered into
	// an argv the two backends would build differently. This is the same
	// structural-over-by-name recognition the handler uses (isProtoData), for the
	// same reason: a by-name check accepted a wrong shape and a right shape under
	// another name, both defects.
	return a != nil && a.K == "data" && a.Hash == protoList &&
		len(a.Args) == 1 && isStrTy(sh, &a.Args[0])
}

// isResultTy recognises the CLI exit-result datatype (data _ [] (Ok Str)
// (Fail Int Str)) in EITHER constructor order. Str is strTypeHash — this store's
// string type, which is exactly what both backends lower as a native string, so
// recognition and lowering key on ONE hash and cannot diverge. (strTypeHash is the
// bound `Str` when the store names one, else the canonical prototype; see its
// comment.) The arms differ in shape, so declaration order does not change their
// meaning.
func isResultTy(st *Store, t *Ty) bool {
	sh := strTypeHash(st)
	if sh == "" {
		return false
	}
	str := Ty{K: "data", Hash: sh}
	okFirst := hashDef(&Def{K: "data", Ctors: [][]Ty{{str}, {*tInt(), str}}})
	failFirst := hashDef(&Def{K: "data", Ctors: [][]Ty{{*tInt(), str}, {str}}})
	return isProtoData(t, okFirst) || isProtoData(t, failFirst)
}

// isResultEntry recognises (-> (List Str) Result): the CLI entry that MAY exit
// non-zero with its own diagnostic (#120 second-app friction). Same argument as
// isPureEntry; the result is the exit-result type instead of Str.
func isResultEntry(st *Store, t *Ty) bool {
	return t != nil && t.K == "fun" && isResultTy(st, t.B) && isListStrArg(st, t.A)
}

// ---------- capability vocabulary (LANGUAGE-LEVEL) ----------

// capabilityKind is Oath's own name for a unit of outbound authority.
//
// A KIND is not a Go function, an interface, or a syscall. It is the answer to
// "what authority does this program hold", stated so that two backends can
// disagree completely about how to provide it and still agree about what was
// required. `http_request` means the program may cause an outbound HTTP request;
// whether that is `net/http`, an imported wasm function, or an ABI table entry is
// a backend's business and appears nowhere in this file.
type capabilityKind string

const (
	capHTTPRequest capabilityKind = "http_request"
	capProcessEnv  capabilityKind = "process_env"
	capFileRead    capabilityKind = "file_read"
	capRecordSink  capabilityKind = "record_sink"

	// capRequiredValue is NOT an authority. Every kind above answers "what may
	// this program DO"; this one answers "what must the host have SUPPLIED for
	// this program to start at all". It restricts nothing and grants nothing.
	//
	// It exists because #120 found that the two were being conflated: a program
	// holding `env` may read the environment, which is true whether or not the
	// variable it needs exists — so the launch gate reported success while the
	// artifact accepted a delivery forged under an empty key. Narrowing the
	// authority (#117) does not fix that and is a different, harder question.
	capRequiredValue capabilityKind = "required_value"
)

// capabilityDecl binds one capability-record FIELD NAME to a kind and the Oath
// type a field must have to denote it.
//
// The field name carries the meaning, which is the existing convention (a program
// asks for `{fetch : (-> Str Str)}` and means HTTP) and is kept deliberately: it
// is a statement in Oath's own vocabulary, not a Go construct. The type check is
// what stops the name from being the ONLY thing consulted — a field called `fetch`
// with the wrong type is a type error, not a silently different capability.
//
// LIMITATION, stated rather than hidden: the vocabulary is global and unscoped, so
// a program cannot yet require `http_request` NARROWED to one host, or `file_read`
// rooted at one path. #114 lists narrowing as later work; the requirement struct
// leaves room for it and nothing here has to change shape to add it.
type capabilityDecl struct {
	Kind capabilityKind
	// Sig reports whether an Oath type may denote this kind. Expressed as a
	// predicate over *Ty so the constraint stays in Oath's type language.
	Sig func(st *Store, t *Ty) bool
	Doc string
}

// capabilityVocabulary is the complete set of capabilities Oath defines. A field
// name outside it is not an unwired capability — it is an UNKNOWN REQUIREMENT, and
// the difference matters: one is a host that cannot help, the other is a program
// asking for authority this language has no meaning for. Both refuse the build;
// they refuse it with different messages, because the repairs are different.
var capabilityVocabulary = map[string]capabilityDecl{
	"fetch": {
		Kind: capHTTPRequest,
		Sig:  sigStrToStr,
		Doc:  "one outbound HTTP GET; the response body, or the failure value",
	},
	"env": {
		Kind: capProcessEnv,
		Sig:  sigStrToStr,
		Doc:  "read one host environment variable by name",
	},
	"readfile": {
		Kind: capFileRead,
		Sig:  sigStrToStr,
		Doc:  "read one file by path",
	},
	"emit": {
		Kind: capRecordSink,
		Sig:  sigStrToStr,
		Doc:  "append one record to the host's sink",
	},
}

// sigStrToStr is the shape every capability in the first slice shares: a total
// function from a Str request to a Str result. The protocol for CALL failure is
// the empty string — see capabilityFailureValue.
func sigStrToStr(st *Store, t *Ty) bool {
	sh := strTypeHash(st)
	return t != nil && t.K == "fun" && isStrTy(sh, t.A) && isStrTy(sh, t.B)
}

// capabilityFailureValue documents the CALL-failure protocol, which is a
// different thing from provision failure and must not be confused with it:
//
//   - a capability CALL that fails (host unreachable, file absent) returns the
//     empty string. That is an ordinary Oath value the program branches on, and
//     it is part of the capability's contract.
//   - a capability that cannot be PROVIDED AT ALL is not a value. The program
//     never starts. See the invariant on CompiledProgram.
//
// Before #114 these were the same state: an unrecognised capability field was
// simply never wired, so a program whose host could not supply its authority ran
// anyway with every call returning "". "The fetch succeeded and the body was
// empty" and "this host has no network at all" were indistinguishable — which
// makes the capability system a naming convention rather than a guarantee.
const capabilityFailureValue = ""

// ---------- requirements ----------

// CapabilityRequirement is one unit of authority a compiled entry point demands,
// stated neutrally. Slot is its position in the entry's capability record, which
// is how the value is delivered — records compile to positional field access, so
// the provider set and the record MUST agree on order.
type CapabilityRequirement struct {
	Field string         `json:"field"`
	Kind  capabilityKind `json:"kind"`
	Slot  int            `json:"slot"`
}

// entryRequirements derives the requirement list from an entry's capability
// record type. nil record (an entry taking no capabilities) yields no
// requirements, which is a legitimate answer and not an error.
//
// This is the whole of "what does this program require". It is DERIVED from the
// verified type — never stored, never asserted by the builder — which is what
// makes the manifest checkable rather than merely claimed: anyone holding the
// entry hash can recompute it.
// The empty result is an empty SLICE, never nil, so the manifest serializes
// `"requirements": []` rather than `null`. "This artifact requires no authority"
// is a claim the record makes; `null` reads as a field nobody filled in, and the
// difference between an answer and a gap is the whole value of the record.
// capabilityKinds is every kind an Oath capability record can denote, and it is
// the single source of truth for that set.
//
// THE VOCABULARY IS TWO-PART, which is why this cannot just range over
// capabilityVocabulary: function-typed fields name AUTHORITIES from a closed,
// name-keyed table, and value-typed fields name REQUIRED VALUES recognized by
// shape, with the field name as the value's own identifier rather than a lookup.
// A backend must implement all of these and nothing outside them.
func capabilityKinds() map[capabilityKind]bool {
	out := map[capabilityKind]bool{capRequiredValue: true}
	for _, decl := range capabilityVocabulary {
		out[decl.Kind] = true
	}
	return out
}

func entryRequirements(st *Store, capTy *Ty) ([]CapabilityRequirement, error) {
	out := []CapabilityRequirement{}
	if capTy == nil {
		return out, nil
	}
	for i, name := range capTy.Names {
		fieldTy := &capTy.Args[i]

		// A VALUE-typed field is a required value, and the FIELD NAME is its
		// identifier rather than a lookup into the authority vocabulary.
		//
		// The rule is by SHAPE, not by name: function-typed fields are named
		// authorities from a closed vocabulary; value-typed fields are named
		// requirements from an open one. That is why this needs no new type
		// machinery — a record's field set and types are already part of the
		// def's identity, so a program requiring a secret is a DIFFERENT
		// ARTIFACT from one that does not, and no deployer can strip the
		// requirement by editing metadata.
		//
		// The host's source for the value — an environment variable, a secret
		// manager, a mounted file — is deployment configuration and is
		// deliberately NOT in the type, exactly as `emit` does not name a path.
		// The artifact declares WHAT it requires; the host decides WHERE it
		// comes from, and two deployments sourcing it differently are the same
		// artifact.
		if isStrTy(strTypeHash(st), fieldTy) {
			if _, clash := capabilityVocabulary[name]; clash {
				return nil, fmt.Errorf("capability %q : Str reuses the name of an authority\n"+
					"  %s names an authority and must be (-> Str Str). A required VALUE must not\n"+
					"  reuse an authority's name, or a reader could not tell which one a record\n"+
					"  field means without consulting its type.", name, name)
			}
			out = append(out, CapabilityRequirement{Field: name, Kind: capRequiredValue, Slot: i})
			continue
		}

		decl, known := capabilityVocabulary[name]
		if !known {
			return nil, fmt.Errorf("unknown capability requirement %q : %s\n"+
				"  Oath defines these capabilities: %s\n"+
				"  A function-typed capability field names the AUTHORITY it wants, and a name\n"+
				"  outside that vocabulary has no meaning to give a host. A field of type Str is\n"+
				"  a REQUIRED VALUE instead, named by the field and provisioned before launch.",
				name, debugTy(fieldTy), strings.Join(knownCapabilityFields(), ", "))
		}
		if !decl.Sig(st, fieldTy) {
			return nil, fmt.Errorf("capability %q : %s has the wrong type\n"+
				"  %s denotes %s (%s) and must be (-> Str Str).",
				name, debugTy(fieldTy), name, decl.Kind, decl.Doc)
		}
		out = append(out, CapabilityRequirement{Field: name, Kind: decl.Kind, Slot: i})
	}
	return out, nil
}

// knownCapabilityFields lists the vocabulary in a stable order, for messages.
func knownCapabilityFields() []string {
	var names []string
	for n := range capabilityVocabulary {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ---------- the compiled program ----------

// entryProtocolNames maps each shape to the word the MANIFEST uses. The manifest
// is read by things that are not this program, so the protocol is a word rather
// than an integer whose meaning lives in a Go const block.
//
// TWO SHAPES SHARE ONE WORD, AND THE ARITY IS THEREFORE NOT IN THE MANIFEST AT
// ALL. An earlier version of this comment said the reader recovers the arity
// from `requirements`; that is FALSE, and an entry typed
// (-> {} (-> (List Str) Str)) is the counterexample — it requires nothing, so
// `requirements` is `[]`, and it still takes a capability record. The record's
// presence and its CONTENTS are independent facts, which is the same confusion
// this variant exists to remove, reappearing one layer up in the record that
// describes the build.
//
// It is left out rather than added because the manifest describes the ARTIFACT,
// and the arity is a fact about how a backend calls it; `entry_type` already
// carries the entry's full type, from which the arity is genuinely recoverable.
// Making the protocol word a table rather than an `if` is what stops a new
// protocol from silently being recorded as "cli".
var entryProtocolNames = [...]string{
	shapeCLI:           "cli",
	shapeCLICaps:       "cli",
	shapeHandler:       "handler",
	shapeHandlerCaps:   "handler",
	shapeCLIResult:     "cli",
	shapeCLICapsResult: "cli",
}

var _ entryShapeTable = [len(entryProtocolNames)]struct{}{}

func entryProtocolName(s EntryShape) (string, error) {
	return entryShapeCase("the provenance manifest", &entryProtocolNames, s)
}

// CompiledProgram is the backend-neutral description of one artifact: what it is,
// what authority it requires, what it was built from, and — as the verified def
// closure rather than a re-encoding of it — what it computes.
//
// THE INVARIANT this exists to make enforceable:
//
//	Every requirement declared by the compiled entry point is resolved exactly
//	once before launch, or the executable does not start.
//
// "Exactly once" is structural: requirements are derived from the entry type,
// each is resolved into exactly one record slot, and the record is applied to the
// entry a single time at the boundary. "Or it does not start" is the part that was
// missing — see the resolution preamble the Go backend emits.
// Shape is the ONLY statement of the entry's interface. The capability record
// TYPE is deliberately absent: it was here, and every backend read it as
// `CapTy != nil` to decide arity — a type consulted as a flag, alongside a
// separate `Protocol` field for the protocol, so one interface was two facts a
// backend could combine wrongly. Requirements answers what authority is demanded
// and answers nothing about shape; len(Requirements) == 0 is true of an entry
// typed (-> {} (-> (List Str) Str)), which still takes the record.
type CompiledProgram struct {
	Entry        string                  // resolved name, as asked for
	EntryHash    string                  // the def hash — the artifact's identity
	Shape        EntryShape              // the entry's protocol AND arity, as one fact
	Requirements []CapabilityRequirement // derived from the capability record; the authority demand
	Closure      []string                // every def hash the artifact depends on, sorted
	Provenance   ProvenanceManifest
}

// ProvenanceManifest is what a compiled artifact says about itself. #114's rule:
//
//	Provenance records the exact entry artifact, dependency closure, compiler
//	version, and capability manifest.
//
// and, from the next-pass spec:
//
//	provenance records the NEUTRAL requirements, not Go implementation names.
//
// That second clause is why this type lives here and not next to the emitter. A
// manifest naming `net/http` would describe one build of one backend; a manifest
// naming `http_request` describes the artifact, and stays true when the backend
// changes underneath it.
//
// Every field is either an identity (a hash), a derivation from identity (the
// closure, the requirements), or a fact about the tool that ran. Nothing here is
// an assertion a builder could make up: given the store and the entry hash, all
// of it recomputes.
type ProvenanceManifest struct {
	Schema       string                  `json:"schema"`
	Entry        string                  `json:"entry"`
	EntryHash    string                  `json:"entry_hash"`
	EntryType    string                  `json:"entry_type"`
	Protocol     string                  `json:"protocol"`
	Guarantee    string                  `json:"guarantee"`
	Requirements []CapabilityRequirement `json:"requirements"`
	Closure      []string                `json:"closure"`
	Kernel       string                  `json:"kernel"`
	Backend      string                  `json:"backend"`
}

// manifestJSON renders the manifest deterministically. Key order is struct order
// and every list is already sorted or positionally fixed, so the same program
// yields the same bytes — which is what makes the digest meaningful.
// HTML escaping is off: a manifest is read by people and by tools, and an entry
// type rendered as (-> {fetch ...}) is a worse record of what was built than
// the arrow the type actually has.
func (m ProvenanceManifest) manifestJSON() []byte {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		// The manifest is composed of strings and ints from this package; a
		// marshal failure would be a programming error, not a user error.
		panic("provenance manifest is not marshalable: " + err.Error())
	}
	return []byte(b.String())
}

// digest identifies the manifest itself, so two parties can compare what a binary
// claims without diffing text. It is NOT the artifact's identity — that is
// EntryHash, and it is what a verifier resolves against the store.
func (m ProvenanceManifest) digest() string {
	s := sha256.Sum256(m.manifestJSON())
	return hex.EncodeToString(s[:])
}

// validate reports whether a manifest is a COMPLETE record rather than merely
// well-formed JSON that mentions the schema.
//
// The distinction matters because parsing is not validation: unmarshalling
// `{"schema":"oath-provenance/1"}` succeeds and leaves every other field at its
// zero value, so an empty record would be reported as provenance while naming no
// artifact, no closure, and no authority. A record that says nothing is worse
// than no record — one is an obvious absence, the other reads as a passing check.
//
// Requirements may legitimately be empty: most programs need no authority, and
// "requires nothing" is a claim, not a gap.
func (m ProvenanceManifest) validate() error {
	if m.Schema != provenanceSchema {
		return fmt.Errorf("schema is %q, want %q", m.Schema, provenanceSchema)
	}
	for _, f := range []struct {
		name, val string
	}{
		{"entry", m.Entry}, {"entry_hash", m.EntryHash}, {"entry_type", m.EntryType},
		{"guarantee", m.Guarantee}, {"kernel", m.Kernel}, {"backend", m.Backend},
	} {
		if f.val == "" {
			return fmt.Errorf("%s is empty", f.name)
		}
	}
	if m.Protocol != "cli" && m.Protocol != "handler" {
		return fmt.Errorf("protocol is %q, want cli or handler", m.Protocol)
	}
	if !isDefHash(m.EntryHash) {
		return fmt.Errorf("entry_hash %q is not a definition hash", m.EntryHash)
	}
	// The closure is what the artifact was built FROM, so it must contain the
	// artifact. A closure omitting its own entry is not a smaller closure — it is
	// a record of something else.
	//
	// It is also sorted and duplicate-free, because programClosure derives it that
	// way. Checking the shape a producer must have produced is the difference
	// between "this parses" and "this could have come from a build".
	if !slicesContains(m.Closure, m.EntryHash) {
		return fmt.Errorf("closure does not contain the entry hash")
	}
	for i, h := range m.Closure {
		if !isDefHash(h) {
			return fmt.Errorf("closure entry %d (%q) is not a definition hash", i, h)
		}
		if i > 0 && m.Closure[i-1] >= h {
			return fmt.Errorf("closure is not sorted and unique at entry %d (%q after %q)", i, h, m.Closure[i-1])
		}
	}
	// An OMITTED authority list is not an empty one. `null` and `[]` would
	// otherwise both read as "requires nothing", giving the same content two
	// canonical forms and letting a record that never stated its authority pass
	// as one that stated it holds none.
	if m.Requirements == nil {
		return fmt.Errorf("requirements is absent; an artifact requiring nothing says so as []")
	}
	for i, r := range m.Requirements {
		if r.Field == "" {
			return fmt.Errorf("requirement %d has no field", i)
		}
		// Record field order is canonicalized away by the kernel — `{b … a …}`
		// and `{a … b …}` are the same type — so a capability record's fields are
		// sorted and unique, and requirements derived from one inherit that. A
		// record naming the same capability twice is not a program this compiler
		// could have built.
		if i > 0 && m.Requirements[i-1].Field >= r.Field {
			return fmt.Errorf("requirements are not sorted and unique at %d (%q after %q)",
				i, r.Field, m.Requirements[i-1].Field)
		}
		if r.Kind == "" {
			return fmt.Errorf("requirement %s names no kind", r.Field)
		}
		if r.Slot != i {
			return fmt.Errorf("requirement %s claims slot %d at position %d — slots are how authority is delivered", r.Field, r.Slot, i)
		}
		// A field DETERMINES its kind: entryRequirements derives one from the
		// other, so a record claiming `fetch` denotes process_env is not a
		// capability this compiler could have produced. Reporting it as authority
		// would misdescribe what the artifact holds, which is the one thing this
		// record exists to get right.
		if decl, known := capabilityVocabulary[r.Field]; known && decl.Kind != r.Kind {
			return fmt.Errorf("requirement %s claims kind %q, but %s denotes %q",
				r.Field, r.Kind, r.Field, decl.Kind)
		}
	}
	return nil
}

// isDefHash reports whether a string has the shape of a definition hash: 64
// lowercase hex digits, which is what SHA-256 over the canonical encoding
// produces and the only thing that can be resolved against a store.
func isDefHash(h string) bool {
	return len(h) == 64 && strings.TrimLeft(h, "0123456789abcdef") == ""
}

// unknownRequirements lists requirements naming capabilities THIS build does not
// define. They are not an error: the vocabulary grows, and an older reader
// meeting a newer artifact should say so rather than declare the record invalid
// and report "no provenance" about a file that plainly has some.
//
// The distinction is between a record that CONTRADICTS itself — rejected above,
// since no version of this compiler could produce it — and one that says more
// than this reader knows. Only the first is a defect.
func (m ProvenanceManifest) unknownRequirements() []string {
	var out []string
	for _, r := range m.Requirements {
		// A required value is known by its KIND, not by its name: the name is
		// the program's own identifier for the value and is deliberately open,
		// so looking it up in the authority vocabulary would report every one
		// of them as unrecognized.
		if r.Kind == capRequiredValue {
			continue
		}
		if _, known := capabilityVocabulary[r.Field]; !known {
			out = append(out, fmt.Sprintf("%s (%s)", r.Field, r.Kind))
		}
	}
	return out
}

// The manifest is carried inside the executable between these markers.
//
// PROVENANCE IS DATA, NOT A MODE. A compiled program has no provenance flag and
// reads no provenance environment variable, because both channels already belong
// to the program:
//
//   - argv IS a CLI entry point's input — its type is literally (List Str), so
//     every argument is a value the verified logic is entitled to receive, and a
//     reserved flag would silently shadow one.
//   - the environment belongs to any program holding `env`. A reserved variable
//     name would mean a program reading it got a process exit instead of the value
//     its `process_env` capability promises — and environments are inherited, so
//     one exported variable would change every Oath artifact beneath it.
//
// The first version of this shipped the environment variable. It was wrong for
// exactly the reason the capability model exists to notice: the compiler helped
// itself to authority over a channel it had already granted to the program.
//
// Reading provenance therefore never runs the artifact, which is also the right
// answer on its own terms — executing an unknown binary to find out what it is
// has the order backwards.
//
// LIMITATION, AND IT IS THE IMPORTANT ONE: the record is a SELF-DESCRIPTION, and
// nothing binds it to the machine code around it. It is unsigned, and an
// executable cannot carry a hash of itself. So any binary can embed a copied
// manifest, and `oath provenance` will report it faithfully — faithfully being
// the whole of the claim. This is evidence about a COOPERATIVE artifact: it tells
// you what a build recorded, and it detects a manifest that is malformed,
// ambiguous, self-contradictory or truncated. It is NOT proof about an
// ADVERSARIAL one, and must not be read as attestation.
//
// Closing that gap needs a signature over (artifact digest, manifest) by a
// principal, which is the model the publication envelope already uses (SPEC §8.4)
// and is deliberately separate work rather than something bolted on here.
// Verifying a signature is a different act from reading a record, and conflating
// them would make the weaker one look like the stronger.
const (
	provenanceBegin = "\x00oath-provenance-begin\x00"
	provenanceEnd   = "\x00oath-provenance-end\x00"
)

// embeddedManifest is the blob planted in a compiled artifact: the markers make
// it findable in a file nobody is willing to execute. NUL bytes keep the markers
// out of any JSON and out of ordinary text.
func (m ProvenanceManifest) embeddedManifest() string {
	return provenanceBegin + string(m.manifestJSON()) + provenanceEnd
}

// extractProvenance recovers the manifest from the raw bytes of an artifact.
// Reports the JSON as found rather than re-rendering it, so what a reader sees is
// what the file actually carries.
//
// EVERY candidate is examined, not the first. A marker is just a byte string, and
// nothing stops other data in the file from containing one — a compiled program's
// own string literals live in the same image, and the linker chooses their order.
// Taking the first match would let unrelated bytes decide what an artifact claims
// about itself, which is the opposite of the job.
//
// So a candidate must PARSE as a manifest of this schema, and an artifact
// carrying two DIFFERENT valid manifests is reported as ambiguous rather than
// resolved by picking one. Ambiguity here is a real fact about the file: choosing
// silently would be the compiler guessing on a reader's behalf about provenance,
// which is the one thing provenance may not do.
func extractProvenance(b []byte) (string, error) {
	s := string(b)
	var found []string
	// nextEnd tracks the closing marker for the current opening one. Opening
	// markers are visited left to right, so the first close after each is
	// non-decreasing: the pointer only ever moves FORWARD, and the searches
	// together cover the file once. Re-searching the remaining suffix per marker
	// would be quadratic on a file chosen by whoever handed you the artifact,
	// which is precisely the input this command exists to accept.
	nextEnd := -1
	for off := 0; ; {
		i := strings.Index(s[off:], provenanceBegin)
		if i < 0 {
			break
		}
		start := off + i + len(provenanceBegin)
		off = start
		if nextEnd < start {
			j := strings.Index(s[start:], provenanceEnd)
			if j < 0 {
				// No close at or after this point means none after any LATER
				// opening marker either, so the scan is finished.
				break
			}
			nextEnd = start + j
		}
		cand := s[start:nextEnd]
		// A record can contain no NUL byte: encoding/json escapes control
		// characters, so a genuine manifest never carries one — while every marker
		// does. This rejects any candidate that spans a marker, which is both the
		// decoy shape and the expensive one, at the first marker rather than after
		// parsing the whole span.
		if strings.IndexByte(cand, 0) >= 0 {
			continue
		}
		// A record is a JSON object. Checking the first byte is not a heuristic —
		// it is the same predicate Unmarshal applies — and it discards the bulk of
		// look-alike candidates without parsing them.
		if trimmed := strings.TrimLeft(cand, " \t\r\n"); trimmed == "" || trimmed[0] != '{' {
			continue
		}
		var m ProvenanceManifest
		if err := json.Unmarshal([]byte(cand), &m); err != nil || m.validate() != nil {
			continue // bytes that merely look like a record
		}
		// CANONICAL FORM, or it is not this schema's record. Parsing alone leaves
		// a record open to readings this one does not control: encoding/json
		// silently takes the LAST of two `entry_hash` keys, and another consumer
		// may take the first — so the same bytes would describe two different
		// artifacts depending on who read them. A record whose meaning depends on
		// the reader is not a record.
		//
		// Round-tripping through the canonical rendering settles it: duplicate
		// keys, reordering, unknown fields and whitespace variants all fail at
		// once. Within a schema version the encoding is exact, which is the same
		// discipline identity and the journal already follow — and adding a field
		// is what the version is for.
		if string(m.manifestJSON()) != cand {
			continue
		}
		if !slicesContains(found, cand) {
			found = append(found, cand)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no Oath provenance found\n" +
			"  Every artifact `oath build` produces carries one. A file without it was\n" +
			"  either not built by this compiler or was built before provenance existed —\n" +
			"  in both cases nothing here vouches for what it contains.")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("this file carries %d DIFFERENT provenance records\n"+
			"  An artifact must make one claim about what it is. Two mean either the file\n"+
			"  was assembled from more than one build, or something in it is imitating the\n"+
			"  record. Neither is answered by choosing one of them.", len(found))
	}
}

func slicesContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// programClosure returns every definition hash the entry transitively depends on,
// sorted. Datatypes are included: they are part of what the artifact was built
// from even though the Go backend erases them to constructor indices.
//
// This is NOT the emitter's emission order — see emissionOrder below. The two
// answer different questions and must be allowed to differ: the provenance
// closure says what the artifact was built FROM, and its answer must not shift
// when a backend changes which definitions it lowers natively.
func programClosure(st *Store, entry string) ([]string, error) {
	seen := map[string]bool{}
	var walk func(h string) error
	walk = func(h string) error {
		if seen[h] {
			return nil
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			return err
		}
		deps := make([]string, 0, len(collectDeps(d)))
		for dep := range collectDeps(d) {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			if err := walk(dep); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(entry); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// emissionOrder returns the functions a backend must emit for `entry`, each
// preceded by everything it depends on, in an order that is a function of the
// definitions alone.
//
// THIS IS THE ONLY DEPENDENCY-ORDERING WALK A BACKEND MAY USE. Both backends
// previously carried their own copy, each recursing over collectDepsBody in Go
// MAP ITERATION ORDER, so siblings with no edge between them came out in a
// different order run to run and one build of an unchanged tree did not match
// the next (#168, measured at 27/3 over 30 Go emissions and 25/5 over 30 LLVM).
// Sorting inside each copy would have fixed today's symptom and left a third
// copy free to appear; ownership is exclusive here instead, and the backends
// have no ordering code left to drift from.
//
// Determinism comes from visiting each definition's dependencies in HASH order.
// Hashes are canonical identity, so the order is a property of what is being
// compiled rather than of the run, the store's layout, or the names bound to it.
//
// `native` names definitions the calling backend lowers to its own primitives.
// They are PRUNED, not filtered afterwards: a backend that never emits an
// operation must not emit that operation's structural helpers either, and those
// helpers are usually reachable only through it. Filtering a single fixed order
// would emit them as dead code — or, if the filter also dropped their names,
// leave the emitted code referring to functions that were never written. Which
// definitions are native is the one thing here that is a backend decision; the
// ORDER is not, which is why the argument comes in and the walk does not go out.
// Native containers (#13, #178): `Set` and `Map` are ordinary Oath datatypes
// proven over the structural model, but a backend MAY refine them at compile
// time into a native hash representation — the prover never sees the host type.
// Which definitions are the container types and their operations is a NEUTRAL
// fact: it is the same for every backend, derived from the store by name. What
// each recognized operation LOWERS TO is backend-specific, so this layer names
// the operation (its canonical name) and its saturated arity, and each backend
// maps that name to its own helper. Lifting this here keeps the two backends
// from maintaining two copies of "which hash is set-member" — the same
// consolidation as capabilityKinds().
//
// nativeOp is a recognized container operation: its canonical name (the key a
// backend maps to a helper) and the argument count at which a call is saturated
// and may be intercepted.
type nativeOp struct {
	Name  string
	Arity int
}

// ncKind is the expected kind of a recognized operation's parameter or result.
// It is what lets recognition VALIDATE that a name is the canonical container
// operation and not an unrelated function that happens to share the name: a
// store may define its own `set-member : Int -> Int -> Bool`, and lowering that
// call to the native helper would misread its second argument as a Set.
type ncKind uint8

const (
	ncInt ncKind = iota
	ncBool
	ncSet
	ncMap
	ncStr
	ncStrMap
	ncListInt
	ncListStr
	ncOptInt
)

// nativeOpShape is the canonical signature of each recognized operation. An
// operation is admitted only if its declared type matches this shape AND every
// Set/Map argument resolves to ONE datatype consistent across its whole family.
var nativeOpShape = map[string]struct {
	params []ncKind
	result ncKind
}{
	"set-empty":  {nil, ncSet},
	"set-member": {[]ncKind{ncInt, ncSet}, ncBool},
	"set-add":    {[]ncKind{ncInt, ncSet}, ncSet},
	"set-union":  {[]ncKind{ncSet, ncSet}, ncSet},
	"set-inter":  {[]ncKind{ncSet, ncSet}, ncSet},
	"set-size":   {[]ncKind{ncSet}, ncInt},
	"set-elems":  {[]ncKind{ncSet}, ncListInt},
	"map-empty":  {nil, ncMap},
	"map-insert": {[]ncKind{ncInt, ncInt, ncMap}, ncMap},
	"map-lookup": {[]ncKind{ncInt, ncMap}, ncOptInt},
	"map-has":    {[]ncKind{ncInt, ncMap}, ncBool},
	"map-keys":   {[]ncKind{ncMap}, ncListInt},
	"map-values": {[]ncKind{ncMap}, ncListInt},
	"map-size":   {[]ncKind{ncMap}, ncInt},
	"map-merge":  {[]ncKind{ncMap, ncMap}, ncMap},
	// The Str-keyed map is a SEPARATE family under DISTINCT names, not a
	// polymorphic reading of map-*. A store resolves one hash per name, so
	// `map-insert` is either the Int-keyed operation or the Str-keyed one and
	// cannot be both; distinct names are what lets the two families coexist in
	// one program. Values stay Int, so map-values and str-map-values share a
	// result kind while the KEY kind is what distinguishes the families.
	"str-map-empty":  {nil, ncStrMap},
	"str-map-insert": {[]ncKind{ncStr, ncInt, ncStrMap}, ncStrMap},
	"str-map-lookup": {[]ncKind{ncStr, ncStrMap}, ncOptInt},
	"str-map-has":    {[]ncKind{ncStr, ncStrMap}, ncBool},
	"str-map-keys":   {[]ncKind{ncStrMap}, ncListStr},
	"str-map-values": {[]ncKind{ncStrMap}, ncListInt},
	"str-map-size":   {[]ncKind{ncStrMap}, ncInt},
	"str-map-merge":  {[]ncKind{ncStrMap, ncStrMap}, ncStrMap},
}

var setOpNames = []string{"set-empty", "set-member", "set-add", "set-union", "set-inter", "set-size", "set-elems"}
var mapOpNames = []string{"map-empty", "map-insert", "map-lookup", "map-has", "map-keys", "map-values", "map-size", "map-merge"}
var strMapOpNames = []string{"str-map-empty", "str-map-insert", "str-map-lookup", "str-map-has", "str-map-keys", "str-map-values", "str-map-size", "str-map-merge"}

// nativeOpNames is every operation the vocabulary defines — the set a backend
// that lowers all of them passes as `supported`.
func nativeOpNames() map[string]bool {
	out := make(map[string]bool, len(nativeOpShape))
	for name := range nativeOpShape {
		out[name] = true
	}
	return out
}

// nativeContainers is the store-resolved, VALIDATED recognition. Every hash is
// derived from the operations' canonical types, never from a mutable name, and a
// family is admitted only as a coherent whole (see validateFamily). A backend
// consults Ops at a call site and the datatype/list hashes at a constructor or
// match; the Go backend ignores the list/option/pair hashes (its runtime carries
// them), the LLVM backend resolves constructor indices from them.
type nativeContainers struct {
	SetHash     string
	MapHash     string
	SetListHash string // the List datatype MkSet wraps and set-elems builds
	MapListHash string // the List datatype map-keys/values/pairs build
	OptionHash  string // the Option map-lookup returns
	PairHash    string // the Pair a Map match materializes
	// The Str-keyed map family, recorded separately because it is a separate
	// datatype under separate names and may be present when the Int-keyed one is
	// not (and vice versa). StrMapKeyHash is the Str identity its keys carry —
	// the store's ACTIVE Str, the one both backends lower as a native string —
	// so a backend comparing keys knows which datatype it is handling and does
	// not have to re-resolve a name the recognizer already pinned.
	StrMapHash     string
	StrMapListHash string // the List datatype MkStrMap wraps and str-map-keys/values build
	StrMapPairHash string // the Pair a StrMap match materializes
	StrMapOptHash  string // the Option str-map-lookup returns
	StrMapKeyHash  string // the Str datatype the keys are
	Ops            map[string]nativeOp
}

// TWO ASSUMPTIONS THIS RECOGNITION INHERITS FROM THE #13 NATIVE-CONTAINER
// REFINEMENT, shared by every backend that lowers containers (the Go backend
// included), and latent because the corpus only ever builds a Set/Map through
// the operations:
//
//   - NAME-AND-SHAPE TRUST. A definition is admitted from its canonical signature
//     and datatype shape, which does not prove its BODY computes the operation.
//     A conforming-but-wrong `set-member` that always returns false would be
//     replaced by the native helper and diverge from `oath eval`. The stdlib
//     operations are held to the contract by the differential gate; an arbitrary
//     definition under the same name is trusted, not verified.
//   - CANONICALIZING CONSTRUCTION. The native representation is a sorted,
//     duplicate-free array, so a DIRECT `MkSet [2,1,1]` materializes as `[1,2]`,
//     matching the type's own invariant and the Go backend but differing from the
//     structural evaluation of a non-canonical constructor. Programs that build
//     sets through the operations never observe this.
//
// Both are properties of the refinement, not of this recognizer, and narrowing
// them is a #13-scope question about the trust model, not a backend change.
//
// resolveNativeContainers derives the recognition for a backend that lowers the
// operations named in `supported`. It validates each family in ncFamilyTable
// INDEPENDENTLY and FAIL-CLOSED: a family's operations are admitted only if every
// one present matches its canonical signature over one consistent datatype. A
// single mismatch — an unrelated function under a recognized name, or operations
// tied to two different datatype versions — drops the whole family to the
// structural path, because a native value and a structurally-built one of the
// same type cannot coexist. A backend supporting no operations recognizes none.
func resolveNativeContainers(st *Store, supported map[string]bool) nativeContainers {
	nc := nativeContainers{Ops: map[string]nativeOp{}}
	idx := opNameIndex(st)
	for i := range ncFamilyTable {
		nc.validateFamily(st, supported, &ncFamilyTable[i], idx)
	}
	return nc
}

// opNameIndex maps the FINAL path segment of every bound name to the distinct
// object hashes bound under it. Discovery consults this instead of resolving a
// bare name, because the recognized container vocabulary is reserved by its last
// segment, not by one unversioned spelling. The emitter already recognizes an
// operation by its object HASH (recognizeSetOp keys on head.Hash), so a namespaced
// alias — michael/oath/str-map-insert, the shape a registry hands a consumer — must
// be DISCOVERED exactly as the bare name is; otherwise a program depending on the
// published copy silently loses native lowering and falls back to the O(N)
// structural body (#186).
func opNameIndex(st *Store) map[string][]string {
	bySeg := map[string]map[string]bool{}
	for full, h := range st.Names() {
		seg := full
		if i := strings.LastIndex(full, "/"); i >= 0 {
			seg = full[i+1:]
		}
		if bySeg[seg] == nil {
			bySeg[seg] = map[string]bool{}
		}
		bySeg[seg][h] = true
	}
	out := make(map[string][]string, len(bySeg))
	for seg, hs := range bySeg {
		for h := range hs {
			out[seg] = append(out[seg], h)
		}
		sort.Strings(out[seg]) // deterministic; every valid candidate shares the canonical container
	}
	return out
}

// resolveOp finds the object that PROVIDES a native operation, preferring the bare
// name and falling back to a namespaced alias. The bare preference keeps the
// corpus and every existing behaviour byte-identical (bare present -> bare wins),
// and the fallback is the whole fix: when a consumer's store carries str-map only
// as michael/oath/str-map-insert, that alias resolves the SAME object the bare
// name would, and the family is discovered. Distinct implementations of one
// operation is not a case the vocabulary supports; the sorted index makes the pick
// deterministic rather than correct in that case, and a broken op still rejects
// the whole family through validateFamily's shape checks, falling back to the
// verified structural body.
func resolveOp(st *Store, idx map[string][]string, name string) (string, bool) {
	if h, ok := st.Resolve(name); ok {
		return h, true
	}
	if hs := idx[name]; len(hs) > 0 {
		return hs[0], true
	}
	return "", false
}

// ncFamily describes one container family AS DATA: the operations it admits, the
// single constructor its datatype must declare, the element shape the wrapped
// List must carry, and where the validated hashes are recorded. Adding a family
// is a table entry rather than a new branch in validateFamily — the same reason
// nativeOpShape is a table and not a switch.
type ncFamily struct {
	names    []string
	ctorName string
	// elem reports whether the wrapped List's ELEMENT type is the one this
	// family's runtime assumes: Int for a Set, (Pair Int Int) for a Map,
	// (Pair Str Int) for a Str-keyed map.
	elem func(st *Store, el *Ty) bool
	// record writes the validated hashes into nativeContainers.
	record func(nc *nativeContainers, st *Store, ctx *ncCtx)
}

var ncFamilyTable = []ncFamily{
	{
		names:    setOpNames,
		ctorName: "MkSet",
		elem:     func(st *Store, el *Ty) bool { return el != nil && el.K == "int" },
		record: func(nc *nativeContainers, st *Store, ctx *ncCtx) {
			nc.SetHash = ctx.container
			nc.SetListHash = containerListHash(st, ctx.container)
		},
	},
	{
		names:    mapOpNames,
		ctorName: "MkMap",
		elem: func(st *Store, el *Ty) bool {
			return ncPairElem(st, el, func(k *Ty) bool { return k.K == "int" })
		},
		record: func(nc *nativeContainers, st *Store, ctx *ncCtx) {
			nc.MapHash = ctx.container
			nc.MapListHash = containerListHash(st, ctx.container)
			nc.OptionHash = ctx.option
			nc.PairHash = containerPairHash(st, ctx.container)
		},
	},
	{
		names:    strMapOpNames,
		ctorName: "MkStrMap",
		elem: func(st *Store, el *Ty) bool {
			return ncPairElem(st, el, func(k *Ty) bool { return ncIsActiveStr(st, k) })
		},
		record: func(nc *nativeContainers, st *Store, ctx *ncCtx) {
			nc.StrMapHash = ctx.container
			nc.StrMapListHash = containerListHash(st, ctx.container)
			nc.StrMapOptHash = ctx.option
			nc.StrMapPairHash = containerPairHash(st, ctx.container)
			nc.StrMapKeyHash = containerKeyHash(st, ctx.container)
		},
	},
}

// ncPairElem reports whether el is (Pair K Int) for a key type accepted by
// keyOK — the element every map family's wrapped List carries. The VALUE is Int
// in both families; only the key differs.
func ncPairElem(st *Store, el *Ty, keyOK func(*Ty) bool) bool {
	return el != nil && el.K == "data" && isCanonicalPair(st, el.Hash) &&
		len(el.Args) == 2 && keyOK(&el.Args[0]) && el.Args[1].K == "int"
}

// ncIsActiveStr reports whether t is the store's ACTIVE Str — the datatype the
// name `Str` resolves to AND whose shape is (SNil) (SCons Int Str). That
// identity, not a structurally-equal alias, is what both backends lower as a
// native host string, so it is the only key type a native Str comparison would
// receive. A store whose `Str` names some other shape admits no Str-keyed
// family: the keys would reach the backend as ordinary constructors.
func ncIsActiveStr(st *Store, t *Ty) bool {
	sh := strTypeHash(st)
	return isStrTy(sh, t) && len(t.Args) == 0 && isCanonicalStrData(st, sh)
}

// isCanonicalStrData checks (data Str [] (SNil) (SCons Int Str)): no type
// parameters, SNil with no fields, SCons carrying a codepoint and the recursive
// tail (encoded as the self-ADT marker "rec", as in isCanonicalList).
func isCanonicalStrData(st *Store, hash string) bool {
	if hash == "" {
		return false
	}
	d, err := st.GetDef(hash)
	if err != nil || d.K != "data" || d.TyVars != 0 || len(d.Ctors) != 2 {
		return false
	}
	ni, ci := ctorIdxOf(st, hash, "SNil"), ctorIdxOf(st, hash, "SCons")
	if ni < 0 || ci < 0 || len(d.Ctors[ni]) != 0 || len(d.Ctors[ci]) != 2 {
		return false
	}
	c := d.Ctors[ci]
	return c[0].K == "int" && c[1].K == "rec" && len(c[1].Args) == 0
}

// validateFamily admits a container family only if every present, supported
// operation matches its canonical signature over one consistent datatype. The
// operations are processed in a fixed order, so the outcome is deterministic
// regardless of map iteration. On any mismatch nothing in the family is admitted.
func (nc *nativeContainers) validateFamily(st *Store, supported map[string]bool, fam *ncFamily, idx map[string][]string) {
	ctx := ncCtx{}
	type admitted struct{ name, hash string }
	var admit []admitted
	for _, name := range fam.names {
		if !supported[name] {
			continue
		}
		h, ok := resolveOp(st, idx, name)
		if !ok {
			continue // an absent operation is fine; a family may be partial
		}
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || d.Ty == nil {
			return
		}
		shape := nativeOpShape[name]
		params, result := unfoldFun(d.Ty)
		if len(params) != len(shape.params) {
			return
		}
		for i, k := range shape.params {
			if !ncMatch(st, params[i], k, &ctx) {
				return
			}
		}
		if !ncMatch(st, result, shape.result, &ctx) {
			return
		}
		admit = append(admit, admitted{name, h})
	}
	if len(admit) == 0 {
		return
	}
	// The runtime represents a container as its single wrapped List; a datatype
	// with an extra constructor or a non-List field would make constructor lowering
	// index a missing argument or a match refuse. So admit the family only if the
	// container's COMPLETE shape matches that assumption, not merely that it has a
	// constructor of the right name.
	if !containerShapeOK(st, ctx.container, fam) {
		return
	}
	// A List-returning operation (set-elems, map-keys, map-values) must return the
	// SAME List datatype the container wraps — the lowering materializes its result
	// with that List's constructor indices, so a different declared result List
	// would be encoded under the wrong type. An empty ctx.list means no such
	// operation is present, which is fine.
	if ctx.list != "" && ctx.list != containerListHash(st, ctx.container) {
		return
	}
	for _, a := range admit {
		nc.Ops[a.hash] = nativeOp{Name: a.name, Arity: len(nativeOpShape[a.name].params)}
	}
	fam.record(nc, st, &ctx)
}

// ncCtx threads the datatype hashes a family must agree on: the container every
// operation shares, the Option a lookup returns, the List a List-returning
// operation builds, and (for a Str-keyed family) the Str its keys are.
type ncCtx struct {
	container string
	option    string
	list      string // the List datatype every List-returning operation shares
}

// ncMatch reports whether t is a type of the expected kind, recording and
// enforcing datatype consistency in ctx. A Set/Map must be the SAME datatype
// across the family; an Int/Bool must be exactly that primitive; a List/Option
// must have the right constructors and Int element.
func ncMatch(st *Store, t *Ty, k ncKind, ctx *ncCtx) bool {
	switch k {
	case ncInt:
		return t != nil && t.K == "int"
	case ncBool:
		return t != nil && t.K == "bool"
	case ncSet:
		return ncData(st, t, "MkSet") && ncPin(&ctx.container, t.Hash)
	case ncMap:
		return ncData(st, t, "MkMap") && ncPin(&ctx.container, t.Hash)
	case ncStr:
		// No pin: ncIsActiveStr already forces every key position to the ONE
		// active Str, so no two positions can disagree and a pin here could never
		// fire — a mutation control confirmed that. The key hash a backend needs
		// is derived from the admitted CONTAINER (containerKeyHash), which is
		// present even for a family whose operations never mention the key type.
		return ncIsActiveStr(st, t)
	case ncStrMap:
		return ncData(st, t, "MkStrMap") && ncPin(&ctx.container, t.Hash)
	case ncListInt:
		return t != nil && t.K == "data" && isCanonicalList(st, t.Hash) &&
			len(t.Args) == 1 && t.Args[0].K == "int" && ncPin(&ctx.list, t.Hash)
	case ncListStr:
		return t != nil && t.K == "data" && isCanonicalList(st, t.Hash) &&
			len(t.Args) == 1 && ncIsActiveStr(st, &t.Args[0]) && ncPin(&ctx.list, t.Hash)
	case ncOptInt:
		return t != nil && t.K == "data" && isCanonicalOption(st, t.Hash) &&
			len(t.Args) == 1 && t.Args[0].K == "int" && ncPin(&ctx.option, t.Hash)
	}
	return false
}

// ncPin records a datatype hash the first time and requires equality thereafter,
// so a family whose operations disagree on their container is rejected.
func ncPin(slot *string, h string) bool {
	if *slot == "" {
		*slot = h
		return true
	}
	return *slot == h
}

func ncData(st *Store, t *Ty, ctor string) bool {
	return t != nil && t.K == "data" && t.Hash != "" && ncHasCtor(st, t.Hash, ctor)
}

func ncHasCtor(st *Store, hash, ctor string) bool {
	m, err := st.GetMeta(hash)
	if err != nil {
		return false
	}
	for _, n := range m.CtorNames {
		if n == ctor {
			return true
		}
	}
	return false
}

// unfoldFun splits a curried function type into its parameter types and result.
// A nullary operation's type is the result itself (no fun wrapper).
func unfoldFun(t *Ty) (params []*Ty, result *Ty) {
	cur := t
	for cur != nil && cur.K == "fun" {
		params = append(params, cur.A)
		cur = cur.B
	}
	return params, cur
}

// containerShapeOK reports whether a container datatype has exactly the single
// representation the native runtime assumes: one constructor of the family's
// name, with one field that is a List whose element type the family accepts —
// Int (Set), (Pair Int Int) (Map), (Pair Str Int) (StrMap). Anything
// else — a second constructor, a non-List field, a wrong element type — is
// rejected, because the lowering indexes that one field and materializes that one
// List and would otherwise miscompile or fail to build.
func containerShapeOK(st *Store, hash string, fam *ncFamily) bool {
	if hash == "" {
		return false
	}
	d, err := st.GetDef(hash)
	if err != nil || d.K != "data" || len(d.Ctors) != 1 || len(d.Ctors[0]) != 1 {
		return false
	}
	m, err := st.GetMeta(hash)
	if err != nil || len(m.CtorNames) != 1 || m.CtorNames[0] != fam.ctorName {
		return false
	}
	field := d.Ctors[0][0]
	if field.K != "data" || len(field.Args) != 1 || !isCanonicalList(st, field.Hash) {
		return false
	}
	return fam.elem(st, &field.Args[0])
}

// ctorIdxOf returns the index of constructor `ctor` in datatype `hash`, or -1.
func ctorIdxOf(st *Store, hash, ctor string) int {
	m, err := st.GetMeta(hash)
	if err != nil {
		return -1
	}
	for i, n := range m.CtorNames {
		if n == ctor {
			return i
		}
	}
	return -1
}

// isTyVar reports whether t is exactly the type variable with index n.
func isTyVar(t Ty, n int) bool { return t.K == "var" && t.Var == n }

// isCanonicalList checks that `hash` is (data List [a] (Nil) (Cons a (List a))):
// one type parameter, exactly Nil (no fields) and Cons (the parameter, then the
// recursive tail typed at the same parameter). Field TYPES matter, not only
// arities — a List whose Cons head were Bool would place Int values into a Bool
// binder. The recursion is pinned to `hash` itself, so only the genuine List is
// accepted.
func isCanonicalList(st *Store, hash string) bool {
	d, err := st.GetDef(hash)
	if err != nil || d.K != "data" || d.TyVars != 1 || len(d.Ctors) != 2 {
		return false
	}
	// Require the CANONICAL constructor ORDER (Nil at 0, Cons at 1), for the same
	// reason as isCanonicalOption: the backends materialize lists with
	// `&ctorV{idx:0}` for Nil and `idx:1` for Cons directly, so a reordered List
	// would misencode keys/values/elems. A reordered List is not lowered natively.
	ni, ci := ctorIdxOf(st, hash, "Nil"), ctorIdxOf(st, hash, "Cons")
	if ni != 0 || ci != 1 || len(d.Ctors[0]) != 0 || len(d.Ctors[1]) != 2 {
		return false
	}
	// The recursive tail (List a) is encoded as the self-ADT marker "rec" (the
	// hash is not knowable while the type is being defined), applied to the same
	// parameter.
	c := d.Ctors[1]
	return isTyVar(c[0], 0) && c[1].K == "rec" && len(c[1].Args) == 1 && isTyVar(c[1].Args[0], 0)
}

// isCanonicalOption checks (data Option [a] (None) (Some a)).
func isCanonicalOption(st *Store, hash string) bool {
	d, err := st.GetDef(hash)
	if err != nil || d.K != "data" || d.TyVars != 1 || len(d.Ctors) != 2 {
		return false
	}
	// Require the CANONICAL constructor ORDER (None at 0, Some at 1), not merely
	// the names: the backends' omap/smap lowerings emit `&ctorV{idx:0}` for None
	// and `idx:1` for Some directly, so a reordered `(data Option [a] (Some a)
	// (None))` — which passes a names-only check — would misencode a lookup
	// result. Requiring the order makes the recognizer's admission match what the
	// codegen assumes; a reordered Option is simply not lowered natively.
	ni, si := ctorIdxOf(st, hash, "None"), ctorIdxOf(st, hash, "Some")
	if ni != 0 || si != 1 || len(d.Ctors[0]) != 0 || len(d.Ctors[1]) != 1 {
		return false
	}
	return isTyVar(d.Ctors[1][0], 0)
}

// isCanonicalPair checks (data Pair [a b] (Pair a b)).
func isCanonicalPair(st *Store, hash string) bool {
	d, err := st.GetDef(hash)
	if err != nil || d.K != "data" || d.TyVars != 2 || len(d.Ctors) != 1 {
		return false
	}
	pi := ctorIdxOf(st, hash, "Pair")
	if pi < 0 || len(d.Ctors[pi]) != 2 {
		return false
	}
	return isTyVar(d.Ctors[pi][0], 0) && isTyVar(d.Ctors[pi][1], 1)
}

// ctorArityIs reports whether datatype `hash` declares a constructor `ctor` with
// exactly `n` fields. The native runtime constructs and destructures these
// constructors at fixed arities (Nil/0, Cons/2, Pair/2, None/0, Some/1), so a
// datatype whose constructor has a different field count would read or write out
// of bounds and must not be lowered natively.
func ctorArityIs(st *Store, hash, ctor string, n int) bool {
	if hash == "" {
		return false
	}
	d, err := st.GetDef(hash)
	if err != nil || d.K != "data" {
		return false
	}
	m, err := st.GetMeta(hash)
	if err != nil {
		return false
	}
	for i, name := range m.CtorNames {
		if name == ctor {
			return i < len(d.Ctors) && len(d.Ctors[i]) == n
		}
	}
	return false
}

// containerListHash returns the hash of the List datatype a container's single
// field wraps (MkSet's (List Int), MkMap's (List (Pair Int Int))), derived from
// the container's CANONICAL constructor rather than the name "List".
func containerListHash(st *Store, containerHash string) string {
	if containerHash == "" {
		return ""
	}
	d, err := st.GetDef(containerHash)
	if err != nil {
		return ""
	}
	for _, fields := range d.Ctors {
		if len(fields) == 1 && fields[0].K == "data" {
			return fields[0].Hash
		}
	}
	return ""
}

// containerKeyHash returns the hash of the KEY datatype in a map container's
// element type (List (Pair K Int)), or "" when the key is a primitive and has no
// datatype hash. It is derived from the CONTAINER's own validated shape rather
// than from whichever operation happened to mention the key type, because a
// family may be PARTIAL: one holding only str-map-empty/size/merge names no key
// anywhere in its signatures, and a backend lowering merge still has to compare
// keys. Reading it off the container makes the recorded identity available for
// every admitted family rather than for the ones that happen to spell it out.
func containerKeyHash(st *Store, containerHash string) string {
	if containerHash == "" {
		return ""
	}
	d, err := st.GetDef(containerHash)
	if err != nil {
		return ""
	}
	for _, fields := range d.Ctors {
		if len(fields) == 1 && fields[0].K == "data" && len(fields[0].Args) == 1 {
			el := fields[0].Args[0]
			if el.K == "data" && len(el.Args) == 2 {
				return el.Args[0].Hash
			}
		}
	}
	return ""
}

// containerPairHash returns the hash of the Pair datatype inside a Map's field
// type (List (Pair Int Int)), for the Map match bridge.
func containerPairHash(st *Store, mapHash string) string {
	if mapHash == "" {
		return ""
	}
	d, err := st.GetDef(mapHash)
	if err != nil {
		return ""
	}
	for _, fields := range d.Ctors {
		if len(fields) == 1 {
			return dataHashWithCtor(st, &fields[0], "Pair")
		}
	}
	return ""
}

// dataHashWithCtor returns the hash of the first datatype appearing in ty whose
// constructors include ctorName, or "" if none. It recovers a datatype hash from
// a CANONICAL type — a field type here — rather than from a name.
func dataHashWithCtor(st *Store, ty *Ty, ctorName string) string {
	seen := map[string]bool{}
	var walk func(t *Ty) string
	walk = func(t *Ty) string {
		if t == nil {
			return ""
		}
		if t.K == "data" && t.Hash != "" && !seen[t.Hash] {
			seen[t.Hash] = true
			if ncHasCtor(st, t.Hash, ctorName) {
				return t.Hash
			}
		}
		if r := walk(t.A); r != "" {
			return r
		}
		if r := walk(t.B); r != "" {
			return r
		}
		for i := range t.Args {
			if r := walk(&t.Args[i]); r != "" {
				return r
			}
		}
		return ""
	}
	return walk(ty)
}

// FIRST-CLASS USE FALLS BACK TO THE STRUCTURAL DEFINITION, DELIBERATELY. A
// saturated call is intercepted and lowered to the iterative native helper; an
// operation passed as a value or applied partially keeps its structural
// definition (see below) and runs the ordinary recursive body. That body has the
// same guarded stack behavior as any structural recursion on the LLVM backend —
// it is not a new failure mode, it is the #178 ceiling that native lowering
// removes for the COMMON (saturated) case and leaves for the rare higher-order
// one. Synthesizing a native closure per operation would close that too; it is
// not worth the complexity for a pattern the corpus never uses, and the fallback
// is correct.
//
// prunableOps returns the recognized operations that may be pruned from
// structural emission: those EVERY reference to which, across the entry's
// closure, is a SATURATED application — the only form recognizeSetOp /
// recognizeContainerOp intercepts. An operation used as a first-class value or
// applied partially is NOT pruned, so its structural definition stays available
// for that use; its saturated calls are still lowered natively, and its
// structural body still produces native values through the intercepted MkSet/
// MkMap constructor and match, so the two paths agree. SetHash/MapHash are never
// pruned — a constructor or match still needs the datatype's shape.
func (nc nativeContainers) prunableOps(st *Store, entryHash string) (map[string]bool, error) {
	if len(nc.Ops) == 0 {
		return nil, nil
	}
	closure, err := programClosure(st, entryHash)
	if err != nil {
		return nil, err
	}
	total := map[string]int{}
	sat := map[string]int{}
	var scan func(t *Term)
	scan = func(t *Term) {
		if t == nil {
			return
		}
		if t.K == "ref" {
			if _, ok := nc.Ops[t.Hash]; ok {
				total[t.Hash]++
			}
		}
		if t.K == "app" {
			if head, args := unwindApp(t); head.K == "ref" {
				if op, ok := nc.Ops[head.Hash]; ok && len(args) == op.Arity {
					sat[head.Hash]++
				}
			}
		}
		scan(t.A)
		scan(t.B)
		scan(t.C)
		for i := range t.Args {
			scan(&t.Args[i])
		}
		for i := range t.Arms {
			scan(&t.Arms[i])
		}
	}
	for _, h := range closure {
		d, err := st.GetDef(h)
		if err != nil {
			return nil, err
		}
		if d.K == "func" && d.Body != nil {
			scan(d.Body)
		}
	}
	// An operation is prunable iff every reference to it was the head of a
	// saturated call: each such call contributes one to both counts (the ref via
	// the walk, the saturation via unwindApp), while a partial or first-class
	// reference contributes to `total` alone.
	prune := map[string]bool{}
	for h := range nc.Ops {
		if total[h] == sat[h] {
			prune[h] = true
		}
	}
	return prune, nil
}

func emissionOrder(st *Store, entry string, native map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	var order []string
	var walk func(h string) error
	walk = func(h string) error {
		if seen[h] {
			return nil
		}
		seen[h] = true
		if native[h] {
			return nil
		}
		d, err := st.GetDef(h)
		if err != nil {
			return err
		}
		if d.K != "func" {
			return nil // datatypes are erased to constructor indices
		}
		deps := make([]string, 0, len(collectDepsBody(d)))
		for dep := range collectDepsBody(d) {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			if err := walk(dep); err != nil {
				return err
			}
		}
		order = append(order, h) // after its dependencies
		return nil
	}
	if err := walk(entry); err != nil {
		return nil, err
	}
	return order, nil
}

// planProgram is the whole front half of `oath build`: resolve the name, apply
// every gate, classify the entry, derive the requirements, and build the
// manifest. It produces a neutral description and touches no backend.
//
// The gates are here rather than in the emitter because they are facts about the
// ARTIFACT, not about how it is lowered. An LLVM backend must refuse a falsified
// entry for exactly the same reason the Go one does, and neither should have to
// remember to.
func planProgram(st *Store, name string) (*CompiledProgram, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return nil, fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return nil, err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return nil, err
	}
	if d.K != "func" {
		return nil, fmt.Errorf("%s is a data definition; entry points are functions", name)
	}
	// Provenance gate: executables are proof-carrying artifacts.
	switch m.Guarantee.Level {
	case "falsified":
		return nil, fmt.Errorf("%s is FALSIFIED (%s) — refusing to build an executable from a broken oath",
			name, strings.Join(m.Guarantee.Falsified, ", "))
	case "asserted":
		return nil, fmt.Errorf("%s has no verified properties — swear and verify an oath before building", name)
	}

	capTy, shape, ok := classifyEntry(st, d.Ty)
	if !ok {
		return nil, fmt.Errorf("%s : %s — entry protocol requires (-> (List Str) Str), (-> (List Str) Result) with Result = (Ok Str | Fail Int Str), (-> Request Response), or any of these with a leading {caps} record",
			name, debugTy(d.Ty))
	}
	protocol, err := entryProtocolName(shape)
	if err != nil {
		return nil, err
	}
	reqs, err := entryRequirements(st, capTy)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(reqs) > 0 {
		// Confinement gate: an entry point that STORES or RETURNS its
		// capability has no business receiving the real one.
		if len(m.Confinement) > 0 && m.Confinement[0] == "escapes" {
			return nil, fmt.Errorf("%s's capability parameter ESCAPES (stored or returned) — refusing to hand it the real world", name)
		}
	}
	closure, err := programClosure(st, h)
	if err != nil {
		return nil, err
	}
	prog := &CompiledProgram{
		Entry:        name,
		EntryHash:    h,
		Shape:        shape,
		Requirements: reqs,
		Closure:      closure,
		Provenance: ProvenanceManifest{
			Schema:       provenanceSchema,
			Entry:        name,
			EntryHash:    h,
			EntryType:    printTy(st, d.Ty, m.TyVarNames),
			Protocol:     protocol,
			Guarantee:    guaranteeString(m.Guarantee),
			Requirements: reqs,
			Closure:      closure,
			Kernel:       kernelVersion,
			// Backend is deliberately UNSET here. Which lowering produced an
			// artifact is not something the neutral planner can know — naming the
			// Go emitter at this layer would make every future backend's manifest
			// claim to be Go, and would have the plan depend on the backend, which
			// is the exact direction this split forbids. The backend stamps it when
			// it emits; see stampBackend.
		},
	}
	return prog, nil
}

// stampBackend records which lowering produced this artifact and checks the
// finished record. A plan is not an artifact until a backend claims it, so this
// is where the manifest becomes complete and where completeness is enforced.
//
// A record a reader would reject must never be written: producer and reader agree
// on one definition of complete, and a gap between them surfaces at the build that
// introduced it rather than at whoever later tries to find out what the artifact is.
func (p *CompiledProgram) stampBackend(backend string) error {
	p.Provenance.Backend = backend
	if err := p.Provenance.validate(); err != nil {
		return fmt.Errorf("refusing to build %s: the provenance record would be incomplete (%v)", p.Entry, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// SPEC §14.1a — the handler protocol's types, recognized by IDENTITY.
//
// §14.1a declares `List`, `Pair`, `Request` and `Response`, and requires that an
// entry point be a handler because of the IDENTITY of its argument and result
// types, never because of the names a store binds them to.
//
// THE DECLARATIONS ARE CONSTRUCTED HERE AND HASHED, rather than resolved. That
// is not a shortcut around structural matching — under content addressing the
// two are the same thing: a datatype's hash IS the canonical encoding of its
// declaration, so two structurally identical declarations have one hash by
// construction. It is also STRICTER than walking the tree, because a walk
// compares the fields it occurs to the author to compare, while a hash compares
// every byte of the canonical form. An earlier structural walker was replaced
// for exactly that reason: it tested `K == "int"` and would have accepted a
// node carrying surplus serialized fields whose canonical identity differs.
//
// Constructor names are absent by the same mechanism: they are per-alias
// metadata and not in the hashed `Def`, so `Req` and `Envelope` are vocabulary,
// not identity.
//
// TestProtocolTypeHashesMatchTheCorpus pins these against the committed store —
// if §1's encoding changes, they move with it and that test says so rather than
// the protocol drifting in silence.

var (
	protoOnce                      sync.Once
	protoStr, protoList, protoPair string
	protoRequest, protoResponse    string
)

func protoInit() {
	protoOnce.Do(func() {
		// (data Str [] (SNil) (SCons Int Str))
		protoStr = hashDef(&Def{K: "data", Ctors: [][]Ty{{}, {*tInt(), {K: "rec"}}}})
		// (data List [a] (Nil) (Cons a (List a)))
		protoList = hashDef(&Def{K: "data", TyVars: 1,
			Ctors: [][]Ty{{}, {*tVar(0), {K: "rec", Args: []Ty{*tVar(0)}}}}})
		// (data Pair [a b] (Pair a b))
		protoPair = hashDef(&Def{K: "data", TyVars: 2, Ctors: [][]Ty{{*tVar(0), *tVar(1)}}})

		str := Ty{K: "data", Hash: protoStr}
		hdrs := Ty{K: "data", Hash: protoList,
			Args: []Ty{{K: "data", Hash: protoPair, Args: []Ty{str, str}}}}
		octets := Ty{K: "data", Hash: protoList, Args: []Ty{*tInt()}}
		// (data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))
		protoRequest = hashDef(&Def{K: "data",
			Ctors: [][]Ty{{str, str, hdrs, octets, *tInt()}}})
		// (data Response [] (Resp Int (List (Pair Str Str)) (List Int)))
		protoResponse = hashDef(&Def{K: "data",
			Ctors: [][]Ty{{*tInt(), hdrs, octets}}})
	})
}

// isProtoData reports whether t is exactly the ground datatype with that hash.
// `len(t.Args) != 0` matters: the protocol types take no type arguments, and a
// node carrying them is a different type however its hash reads.
func isProtoData(t *Ty, hash string) bool {
	return t != nil && t.K == "data" && t.Hash == hash && len(t.Args) == 0
}

// --- typed backend refusals (#134) -------------------------------------
//
// A refusal's REASON is a contract; its wording is presentation. That
// separation exists because the two were conflated once and a test skipped
// forever while looking deliberate: it keyed on
// `strings.Contains(err, "match on Str")`, and the help paragraph LISTS
// unsupported features — so the substring stayed true after `match` on `Str`
// was implemented. A witness that reads implementation prose infers semantic
// state instead of exercising a capability.
//
// So callers branch on Reason, which cannot be satisfied by boilerplate, and
// Detail stays free to be rewritten without silently changing what any gate
// measures.
type refusalReason string

// The vocabulary. NEUTRAL where two backends mean the same thing —
// reasonCapability is emitted by both, because "this backend has no
// implementation for that capability kind" is one fact about the neutral
// requirement vocabulary, not two coincidentally similar sentences.
const (
	// Shared by every backend.
	reasonCapability refusalReason = "capability-unsupported"

	// Backend-specific lowering limits.
	//
	// RETIRED, AND SAID SO RATHER THAN QUIETLY LEFT: no backend emits
	// reasonIntRange (#166 made Int arbitrary-precision), reasonDynamicStr
	// (#164 lowered runtime Str construction) or reasonHandlerProtocol (#173
	// lowered the capability-first handler, which was the last entry shape any
	// backend declined). They stay in the vocabulary
	// because a reason is a published contract term and removing one silently
	// changes what a caller's `==` means — but a caller branching on either
	// today is writing a branch that can never be taken, which is the same
	// forever-skipping guard this type was introduced to prevent. Delete them
	// together, deliberately, if the vocabulary is ever versioned.
	reasonDynamicStr      refusalReason = "dynamic-str"
	reasonStrElementRange refusalReason = "non-scalar-str-element"
	reasonIntMissing      refusalReason = "int-missing-value"
	reasonIntRange        refusalReason = "int-range"
	reasonRatFloat        refusalReason = "rat-float"
	reasonPrim            refusalReason = "prim"
	reasonTermKind        refusalReason = "term-kind"
	reasonMatchOnStrArms  refusalReason = "match-on-str-arms"
	reasonHandlerProtocol refusalReason = "handler-protocol"
	reasonValueBinding    refusalReason = "value-binding-collision"
)

// backendRefusal is a backend declining to lower something it understands.
//
// It is NOT an error in the program: the artifact is valid Oath and this
// backend covers a subset. Callers that need to know WHICH subset boundary was
// hit test Reason; callers showing a human the problem print the error.
type backendRefusal struct {
	Reason  refusalReason
	Backend string
	Detail  string // the human sentence; free to change
	Help    string // optional trailing guidance; free to change
}

func (e *backendRefusal) Error() string {
	if e.Reason == reasonCapability {
		msg := e.Detail + " has no implementation in the " + e.Backend + " backend"
		if e.Help != "" {
			msg += "\n" + e.Help
		}
		return msg
	}
	msg := "the " + e.Backend + " backend cannot lower " + e.Detail
	if e.Help != "" {
		msg += "\n" + e.Help
	}
	return msg
}

// newCapabilityRefusal is the ONE construction for "this backend has no
// implementation for that capability kind", shared by every backend.
//
// It is shared because the two backends previously spelled the same fact as two
// unrelated fmt.Errorf strings, so a caller could match one and silently miss
// the other. The neutral requirement vocabulary is a single fact about the
// program; which backends can provide a kind is a fact about the backends, and
// both belong to the same relation (#134, #114).
//
// `supported` is optional guidance and lives in Help, which is presentation.
func newCapabilityRefusal(backend, field string, kind capabilityKind, supported []string) error {
	r := &backendRefusal{
		Reason:  reasonCapability,
		Backend: backend,
		Detail:  "capability " + field + " (" + string(kind) + ")",
	}
	if len(supported) > 0 {
		r.Help = "  This backend supports: " + strings.Join(supported, ", ") + ".\n" +
			"  The Go backend (`oath build` with no --backend) covers the full vocabulary."
	}
	return r
}

// refusedFor reports the refusal reason, if the error is one. Callers use this
// rather than matching prose:
//
//	if r, ok := refusedFor(err); ok && r == reasonStrElementRange { ... }
func refusedFor(err error) (refusalReason, bool) {
	var r *backendRefusal
	if errors.As(err, &r) {
		return r.Reason, true
	}
	return "", false
}
