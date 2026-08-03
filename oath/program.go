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

// entryKind distinguishes the two entry PROTOCOLS. A CLI entry is invoked once
// with argv and returns stdout; a HANDLER is invoked per request by the host and
// returns a response (#78).
//
// Ingress is deliberately a protocol rather than a capability. A capability is
// outbound authority the program HOLDS and may misuse — hence the confinement
// checker. Being called is not authority: the host owns the socket, decides when
// to invoke, and the artifact stays a pure function of the value it is handed.
// So a handler needs no new capability, and inherits confinement checking,
// verification against every generated request, and the refusal-to-wire gates
// exactly as the CLI protocols do.
type entryKind int

const (
	entryCLI entryKind = iota
	entryHandler
)

// entryShape classifies an entry type, returning the capability record (nil if
// the entry takes none), which protocol it speaks, and whether it is an entry at
// all. Recognized:
//
//	(-> (List Str) Str)             CLI
//	(-> {caps} (-> (List Str) Str)) CLI, capability-first
//	(-> Request Response)           handler
//	(-> {caps} (-> Request Response)) handler, capability-first
func entryShape(st *Store, t *Ty) (*Ty, entryKind, bool) {
	if isPureEntry(st, t) {
		return nil, entryCLI, true
	}
	if isHandlerEntry(st, t) {
		return nil, entryHandler, true
	}
	if t != nil && t.K == "fun" && t.A != nil && t.A.K == "record" {
		if isPureEntry(st, t.B) {
			return t.A, entryCLI, true
		}
		if isHandlerEntry(st, t.B) {
			return t.A, entryHandler, true
		}
	}
	return nil, entryCLI, false
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

// strTypeHash is the hash of the `Str` datatype in this store (the string type
// by convention), or "" if none is defined. isStrTy recognizes a type as that
// datatype — the compiler represents such values as native Go strings.
func strTypeHash(st *Store) string { h, _ := st.Resolve("Str"); return h }

func isStrTy(strHash string, t *Ty) bool {
	return strHash != "" && t != nil && t.K == "data" && t.Hash == strHash
}

func isPureEntry(st *Store, t *Ty) bool {
	sh := strTypeHash(st)
	if t == nil || t.K != "fun" || !isStrTy(sh, t.B) {
		return false
	}
	a := t.A
	if a == nil || a.K != "data" || len(a.Args) != 1 || !isStrTy(sh, &a.Args[0]) {
		return false
	}
	if m, err := st.GetMeta(a.Hash); err == nil {
		return m.Name == "List"
	}
	return false
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

// entryProtocolName renders an entryKind for the manifest. The manifest is read
// by things that are not this program, so the protocol is a word rather than an
// integer whose meaning lives in a Go const block.
func entryProtocolName(k entryKind) string {
	if k == entryHandler {
		return "handler"
	}
	return "cli"
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
type CompiledProgram struct {
	Entry        string                  // resolved name, as asked for
	EntryHash    string                  // the def hash — the artifact's identity
	Protocol     entryKind               // CLI or handler ingress
	CapTy        *Ty                     // the capability record type, or nil
	Requirements []CapabilityRequirement // derived from CapTy; the authority demand
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
// This is deliberately NOT the emitter's emission order. Emission order is a
// backend decision — the Go backend skips definitions it lowers to native
// container operations, and another backend will skip different ones. The
// provenance closure answers a different question ("what does this artifact
// depend on") whose answer must not shift when a backend changes its mind.
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

	capTy, kind, ok := entryShape(st, d.Ty)
	if !ok {
		return nil, fmt.Errorf("%s : %s — entry protocol requires (-> (List Str) Str), (-> Request Response), or either with a leading {caps} record",
			name, debugTy(d.Ty))
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
		Protocol:     kind,
		CapTy:        capTy,
		Requirements: reqs,
		Closure:      closure,
		Provenance: ProvenanceManifest{
			Schema:       provenanceSchema,
			Entry:        name,
			EntryHash:    h,
			EntryType:    printTy(st, d.Ty, m.TyVarNames),
			Protocol:     entryProtocolName(kind),
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
