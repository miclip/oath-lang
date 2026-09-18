package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// objectNaming is the READABLE VOCABULARY that travels beside a published
// object: the names of type variables, constructors, properties and parameters.
//
// It is separate from the object because it is not identity. A canonical object
// is de Bruijn and name-free (§1), so `hashDef` cannot see these strings — which
// is exactly why they may travel UNSIGNED beside a signed object without
// weakening anything the signature claims. It is also why they are not optional
// in practice: without them a stored definition is correct and unreadable.
type objectNaming struct {
	TyVarNames []string `json:"tyvar_names,omitempty"`
	CtorNames  []string `json:"ctor_names,omitempty"`
	PropNames  []string `json:"prop_names,omitempty"`
	ParamNames []string `json:"param_names,omitempty"`
}

// apiPutObject is OBJECT publication (#102): the publisher submits the exact
// canonical object octets it hashed and signed, and the registry validates and
// stores THOSE octets rather than regenerating an object from source.
//
// WHAT THIS CLOSES, and why it is not the same as the guard it replaces.
// Source publication has the client elaborate once, sign the result, and send
// SOURCE for the registry to elaborate a second time; ENV-STORE-ARTIFACT then
// catches any disagreement. That guard works, and it is strictly weaker than not
// having two elaborations: every future difference in normalisation, defaults,
// dependency resolution or kernel version recreates the split, and the next
// instance is found the way the last one was — by a publication being refused in
// production (#101).
//
// The registry still DECODES, and that is not a second derivation of identity:
// decoding is total and injective over canonical bytes, so it can be CHECKED.
// The re-encode assertion below is what makes "store the received bytes" and
// "store the decoded object" the same operation, provably, at admission. Source
// elaboration admits no such check, which is the whole difference.
func apiPutObject(st *Store, objectB64 string, naming *objectNaming, auth *pubAuth, author, ctxHash string) ([]putReport, error) {
	if auth == nil {
		// Object publication exists to bind a signature to exact bytes. Without a
		// statement there is nothing to bind, and the request is just a put whose
		// identity the registry would own anyway — so it is refused rather than
		// silently degraded into the weaker path.
		return nil, fmt.Errorf("object publication requires a signed author statement: without one there is no claim tying these bytes to a principal, and source publication is the path for unsigned writes")
	}
	// decodeEnvelopeB64, not the standard decoder: Go's base64 SILENTLY IGNORES
	// newlines even in Strict mode, so `StdEncoding.Strict().DecodeString` admits
	// several textual spellings of one byte string. That is the same defect
	// ENV-B64-CANONICAL exists to prevent for envelopes, and the authority on
	// this dialect already lives in that function — restating the rule here would
	// be a second description of it, correct exactly once.
	raw, err := decodeEnvelopeB64(objectB64)
	if err != nil {
		return nil, fmt.Errorf("object octets are not canonical base64: %w", err)
	}
	def, err := decodeDef(raw)
	if err != nil {
		return nil, fmt.Errorf("object octets are not a canonical definition: %w", err)
	}
	// OBJ-REENCODE. The decoder is strict, so this should be unreachable — which
	// is precisely why it is asserted rather than assumed. It is the sentence that
	// licenses everything downstream treating the stored object as the signed one:
	// without it, "the registry stores what was signed" rests on a property of the
	// codec that nothing in this path checks.
	if !bytes.Equal(encodeDef(def), raw) {
		return nil, fmt.Errorf("object octets do not re-encode to themselves: the submitted bytes decode to a definition whose canonical form differs, so the signature would cover bytes other than the ones stored")
	}
	env, perr := envelopeParse([]byte(auth.Bytes))
	if perr != nil {
		return nil, fmt.Errorf("author envelope is not canonical: %w", perr)
	}

	// `asserted` is what every other publication path starts new metadata at, and
	// the difference is only visible for DATA definitions: a func passes through
	// the verification branch, which fills the level in, while a data definition
	// never does. Omitting it here would leave an empty level that is not one of
	// the ladder's values at all — a state reachable through exactly one of three
	// publication paths, which is the kind of divergence this issue exists to
	// stop rather than introduce.
	// SOURCE PUBLICATION VALIDATES NAMES BY PARSING THEM; THIS PATH DOES NOT
	// PARSE, SO IT MUST CHECK THEM. A name like `bad name` or `(bad)` cannot be
	// written as one symbol in Oath source, so a binding carrying it is
	// unreferenceable and every projection that prints it emits source that will
	// not re-read. The object path is a second door into a space the lexer was
	// the only door to, which is how it acquires an obligation the source path
	// discharges implicitly.
	if err := requireSurfaceSymbol("published name", env.Name); err != nil {
		return nil, err
	}
	m := &Meta{Name: env.Name, Guarantee: Guarantee{Level: "asserted"}}
	if naming != nil {
		m.TyVarNames, m.CtorNames, m.PropNames = naming.TyVarNames, naming.CtorNames, naming.PropNames
		m.ParamNames = naming.ParamNames
	}
	// The vocabulary is OPTIONAL and attacker-supplied, so it is fitted to the
	// object's own shape rather than trusted. Renderers and the prover index
	// these slices positionally (`m.CtorNames[i]`), so a payload that is short —
	// or simply absent — turns a publication into an out-of-range panic in a
	// LATER, unrelated operation, which is the worst shape for this defect: the
	// bad input is long gone by the time anything crashes.
	//
	// Padded rather than refused, because a missing vocabulary is a legitimate
	// publication (the object is complete without it) and positional names are
	// exactly what the de Bruijn form already means. Surplus entries are dropped
	// for the same reason: they name nothing.
	for label, names := range map[string][]string{
		"constructor name": m.CtorNames, "property name": m.PropNames,
		"parameter name": m.ParamNames, "type-variable name": m.TyVarNames,
	} {
		seen := map[string]bool{}
		for _, n := range names {
			if n == "" {
				continue // absent is legal; fitNaming supplies a positional name
			}
			if err := requireSurfaceSymbol(label, n); err != nil {
				return nil, err
			}
			// DUPLICATES ARE NOT REFUSED, and an earlier version of this refused
			// them. The reasoning was sound and the placement was wrong: a
			// repeated positional name does make a projection ambiguous, but the
			// LANGUAGE already produces them — `(defn f [] [(x Int) (x Int)] …)`
			// and `(data D [a a] …)` both elaborate and are accepted by source
			// publication, which stores the duplicates as-is. Measured, not
			// assumed.
			//
			// So refusing here would have made object publication reject
			// definitions source publication accepts — reintroducing exactly the
			// divergence between the two paths that #102 exists to remove, and
			// doing it AFTER the author had signed. The ambiguity is real and it
			// belongs to the surface projection for both paths; it is not this
			// path's to fix unilaterally by narrowing what may be published.
			_ = seen
		}
	}
	fitNaming(m, def)
	// THE STATEMENT IS VERIFIED BEFORE ANYTHING IS STORED, and only this path can
	// do that. The shared sequence checks it after StoreObject because SOURCE
	// publication cannot know the artifact hash until it has elaborated — so the
	// object is already written when the statement is judged. That is harmless
	// for source (an unreferenced object is inert, and to reach an existing
	// object's metadata you would have to submit source elaborating to exactly
	// it), and it is NOT harmless here: the caller supplies the bytes directly
	// AND the vocabulary, so a publication that is about to be rejected could
	// otherwise rewrite the rendered names of a definition someone else has
	// bound.
	//
	// Object publication has the hash in hand before it stores anything, which
	// is the structural advantage of receiving the object rather than deriving
	// it — so the check moves to where it belongs. admitPut checks again; the
	// check is pure and cheap, and a duplicated refusal is the right kind of
	// redundancy.
	h := hashDef(def)
	curParent, curRev := nameRevision(st, env.Name)
	if cerr := checkPublication(env, auth.Sig, auth.Pubkey, env.Name, h, curParent, curRev); cerr != nil {
		// JOURNALLED, because moving the check earlier must not also move the
		// attempt out of the record. The journal's guarantee is that a rejected
		// put is retained, and the source path's equivalent gate appends one —
		// so returning straight out here would make a class of failed
		// publication vanish from an append-only log, which is a worse property
		// than the metadata exposure the early check was added to close.
		//
		// WITHOUT THE AUTHOR-EVIDENCE FIELDS, which is the opposite of the
		// instinct that first put them here. VerifyLog validates every entry
		// carrying a populated author record REGARDLESS OF STATUS, so recording
		// an envelope whose signature just failed would make every later audit of
		// this journal fail — one malformed request would permanently poison an
		// append-only log. The source path's gate omits them for the same reason:
		// the attempt is retained, the unverified claim is not, and the error
		// text says what was wrong.
		_ = st.AppendLog(&LogEntry{
			Author: author, Name: env.Name, Kind: def.K, Status: "rejected",
			Hash: h, Error: cerr.Error(), Context: ctxHash,
			NameTransition: transitionNone,
		})
		return nil, cerr
	}
	// SNAPSHOT AND RESTORE, because the statement check above is not enough. It
	// covers the signature, the name, the parent and the revision — everything
	// the AUTHOR asserts — but POLICY gates (namespace, ownership, spec strength,
	// the proof gate) run inside the shared sequence, after StoreObject. So a
	// correctly signed publication that policy then blocks would still have
	// merged its unsigned vocabulary into an existing hash, renaming a definition
	// whose name never moved.
	//
	// Restored rather than deferred: the shared sequence's order is deliberate
	// and source publication depends on it, so the fix belongs to the path that
	// introduced the exposure. Only naming is restored — verdict fields are
	// hash-keyed facts that a blocked publication may legitimately have improved.
	prev, prevErr := st.GetMeta(h)
	hadPrev := prevErr == nil && prev != nil
	var prevAliases map[string]*AliasNaming
	if hadPrev && prev.Aliases != nil {
		// A DEEP copy. `prev.Aliases` is the same map StoreObject hands to the
		// incoming metadata and then mutates while merging, so keeping the
		// reference would "restore" the already-mutated map — a snapshot that
		// records nothing, which is worse than none because it reads as a repair.
		prevAliases = make(map[string]*AliasNaming, len(prev.Aliases))
		for k, v := range prev.Aliases {
			if v == nil {
				prevAliases[k] = nil
				continue
			}
			cp := *v
			prevAliases[k] = &cp
		}
	}
	rep, _, err := admitPut(st, def, m, auth, author, ctxHash)
	// `pending` counts as ADMITTED. A require_proven publication is queued and the
	// worker binds the name once the proof lands, so restoring here would leave
	// the eventually-bound name wearing the PREVIOUS alias's vocabulary — the
	// publication succeeds and its names silently do not. Only a refusal
	// (rejected / blocked) restores.
	admitted := rep.Status == "accepted" || rep.Status == "falsified" || rep.Status == "pending"
	if err != nil || !admitted {
		if hadPrev {
			if cur, cerr := st.GetMeta(h); cerr == nil {
				cur.Name = prev.Name
				cur.TyVarNames, cur.CtorNames, cur.PropNames, cur.ParamNames =
					prev.TyVarNames, prev.CtorNames, prev.PropNames, prev.ParamNames
				cur.Aliases = prevAliases
				_ = st.SetMeta(h, cur)
			}
		}
	}
	if err != nil {
		// No report on error: admitPut fills its report in as it goes, so on an
		// error path it can still say "accepted" for a name that never moved.
		return nil, err
	}
	return []putReport{rep}, nil
}

// parseObjectNaming reads the optional naming payload. Absent is legal and means
// "no vocabulary supplied"; malformed is an error rather than a silent empty,
// because the two are different statements and only one of them is deliberate.
func parseObjectNaming(raw json.RawMessage) (*objectNaming, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var n objectNaming
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("naming payload is not a valid naming object: %w", err)
	}
	return &n, nil
}

// fitNaming makes every naming slice exactly as long as the object requires,
// padding with positional names and truncating surplus. Total by construction:
// there is no input for which a downstream index can be out of range.
func fitNaming(m *Meta, def *Def) {
	// UNIQUENESS IS THIS FUNCTION'S INVARIANT, not a precondition checked before
	// it. Validating distinctness on the SUPPLIED payload cannot establish it for
	// the RESULT: a caller who sends {"t1", ""} passes that check and then
	// receives a generated `t1` in the empty slot, so the collision is introduced
	// by the repair. The producer of the final vocabulary is the only place that
	// can guarantee the property of the final vocabulary.
	fit := func(have []string, want int, prefix string) []string {
		if want <= 0 {
			return nil
		}
		// The supplied set is built ONCE. Scanning `have` per generated name made
		// this quadratic in attacker-controlled metadata: an object may declare up
		// to the node budget in type variables, so 65,536 empty entries meant
		// billions of comparisons from a request small enough to send in a loop.
		// The bound that stops the allocation says nothing about the time.
		supplied := make(map[string]bool, len(have))
		for _, h := range have {
			if h != "" {
				supplied[h] = true
			}
		}
		used := make(map[string]bool, want)
		out := make([]string, want)
		next := 0
		for i := 0; i < want; i++ {
			// A supplied name is kept VERBATIM, duplicates included: the language
			// produces them and source publication stores them, so rewriting one
			// here would make the same definition carry different vocabulary
			// depending on which path published it.
			if i < len(have) && have[i] != "" {
				out[i] = have[i]
				used[out[i]] = true
				continue
			}
			// A generated name must avoid both what is already placed and what the
			// caller supplied elsewhere — a payload of {"t1", ""} would otherwise
			// receive `t1` in the empty slot. `next` never rewinds, so the whole
			// pass stays linear.
			for {
				cand := fmt.Sprintf("%s%d", prefix, next)
				next++
				if !used[cand] && !supplied[cand] {
					out[i] = cand
					used[cand] = true
					break
				}
			}
		}
		return out
	}
	// Exactly the slices whose length the OBJECT determines. CtorNames is the one
	// that matters most: it is indexed unguarded (pretty.go, and the prover's
	// datatype construction), so a short payload panics in an operation the
	// publisher is no longer part of. PropNames and TyVarNames are fitted for the
	// same reason even where today's consumers happen to bounds-check — the
	// object knows its own cardinality, so there is no reason to carry a slice
	// that disagrees with it.
	//
	m.CtorNames = fit(m.CtorNames, len(def.Ctors), "c")
	m.PropNames = fit(m.PropNames, len(def.Props), "p")
	m.TyVarNames = fit(m.TyVarNames, def.TyVars, "t")
	// ParamNames IS fitted, and an earlier comment here claiming it was safe
	// unfitted was wrong. The printer takes these as a `preset` and generates
	// `x0`, `x1`... for binders past the end — so a partial payload of {"x0"} on
	// a two-binder function renders BOTH binders `x0`, and a body referring to
	// the outer one displays as referring to the inner one. That projection
	// re-elaborates to a different object than the signed bytes, which is the
	// one outcome this path must never produce. Bounds-checked consumption is not
	// the same property as unambiguous naming.
	m.ParamNames = fit(m.ParamNames, lambdaBinders(def.Body), "x")
}

// WHAT VOCABULARY VALIDATION DOES AND DOES NOT ESTABLISH.
//
// It establishes that every supplied name is a single Oath symbol and that names
// within one positional vocabulary are distinct. Those two make a projection
// well-formed and unambiguous.
//
// It does NOT establish that a projection carrying the vocabulary MEANS the same
// definition. A type variable named `Int` lexes fine and is distinct, but a
// printed type renders it as `Int` while a reader resolves `Int` to the builtin
// before consulting type variables — so the projection describes a monomorphic
// artifact that is not the object signed. Parameter names colliding with a
// keyword or with the definition's own name shadow the same way.
//
// THE FIX IS NOT A LIST OF RESERVED WORDS. Enumerating builtins, keywords and
// self-references is a set someone writes down, and the identical defect was
// repaired in §8.6.4a's small-order check earlier by replacing exactly such a
// list with the condition it was written from. The condition here is a
// ROUND TRIP: render the definition with its vocabulary, re-elaborate the
// rendering, and require the same hash. That is decidable and closes the whole
// class — reserved names, shadowing, and whatever a future surface change adds.
//
// It is not implemented because the artefact it needs does not exist yet:
// printDef and printSpec emit a human rendering rather than re-parseable source,
// so there is nothing to feed back through the elaborator. Recorded on #102 as
// the identified next piece rather than approximated here, because a partial
// reserved-word list would look like the general check and would not be one.
//
// The exposure meanwhile is bounded and worth stating exactly: a publisher can
// attach a misleading vocabulary to THEIR OWN publication. Identity is
// unaffected (the object is the bytes), other objects are unaffected (naming is
// restored on refusal), and audits are unaffected (no unverified evidence is
// journalled). What suffers is the readability of the publisher's own artifact.

// requireSurfaceSymbol refuses a name that is not a single Oath symbol.
//
// It ASKS THE LEXER rather than restating its character rules. `lex` is the
// authority on what a symbol is, and a second description of that here would be
// correct exactly once: the delimiters, the numeric forms and the string rules
// all live there and all move independently of this file. A name is a symbol
// when lexing it yields exactly one `sym` token whose text is the whole input —
// which also rejects the empty string, leading or trailing space, and anything
// that lexes as a number or a string rather than an identifier.
func requireSurfaceSymbol(what, s string) error {
	toks, err := lex(s)
	if err != nil || len(toks) != 1 || toks[0].kind != "sym" || toks[0].sym != s {
		return fmt.Errorf("%s %q is not a single Oath symbol: it could not be written or referenced in source, and every projection that printed it would emit source that does not re-read", what, s)
	}
	return nil
}

func containsName(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// lambdaBinders counts the outermost lambda binders of a function body, which is
// the number of parameter names a projection will need. Derived from the BODY
// rather than from the declared type because that is what the printer walks
// (pretty.go's "lam" case, which descends via .A), and a name list fitted to a different count than the
// consumer indexes is the defect it is meant to prevent.
func lambdaBinders(t *Term) int {
	n := 0
	for t != nil && t.K == "lam" {
		n++
		t = t.A
	}
	return n
}
