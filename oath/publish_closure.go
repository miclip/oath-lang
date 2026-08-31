package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Namespaced publication (#185).
//
// `oath publish --namespace X` cannot be a display prefix, because the registry
// re-elaborates the SOURCE it receives and derives the bound name from it. If the
// client stamps the envelope name X/foo but sends bare-named source, the server
// derives "foo" and refuses the mismatch — and even past that, an intra-closure
// reference (foo -> bar) stays bare and cannot resolve under X.
//
// So binding X/name is a SOURCE TRANSFORMATION. This path performs it: it
// qualifies the declared name AND every reference to a name in the same batch
// (external dependencies and constructors stay bare), then publishes each
// definition as its own signed envelope — one signature per name transition, the
// protocol rule — in dependency order, seeding client elaboration from the
// definitions already processed so a dependent can resolve its qualified deps.
//
// Identity is name-independent (names resolve to hashes), so nothing here changes
// a hash, a verdict, or the spec.

// cmdPublishClosure publishes every definition in src under the namespace, in
// dependency order.
func cmdPublishClosure(ctx context.Context, signer Signer, pubHex string, local *Store, endpoint, src, license, namespace string, dryRun, jsonOut, assumeYes bool) {
	forms, err := parseForms(src)
	if err != nil {
		fail(err)
	}
	if len(forms) == 0 {
		fail(fmt.Errorf("no definitions to publish"))
	}

	// The rename table (each declared name -> its namespaced form) and the batch's
	// constructors (a bare ctor is a dependency on its datatype even though it is
	// not itself qualified). applyNamespace validates the namespace pattern and
	// refuses a source that already carries a prefix, so a name keeps ONE source of
	// truth.
	rename := map[string]string{}
	qualifiedSet := map[string]bool{}
	ctorOwner := map[string]string{} // qualified ctor-owner: ctor name -> qualified datatype
	for _, f := range forms {
		name, err := declaredName(f)
		if err != nil {
			fail(err)
		}
		if _, dup := rename[name]; dup {
			fail(fmt.Errorf("%q is declared twice in this file", name))
		}
		q, err := applyNamespace(name, namespace)
		if err != nil {
			fail(err)
		}
		rename[name] = q
		qualifiedSet[q] = true
		for _, c := range constructorNames(f) {
			ctorOwner[c] = q
		}
	}

	if err := collisionUnderQualification(forms, rename, namespace); err != nil {
		fail(err)
	}

	qsrc := qualifyNames(src, rename)
	qforms := splitTopLevelForms(qsrc)
	if len(qforms) != len(forms) {
		fail(fmt.Errorf("internal: qualified %d forms from %d definitions", len(qforms), len(forms)))
	}

	// Dependency order, derived from which qualified names (and which batch
	// constructors) each definition references. A dependent must be published
	// after its deps.
	ordered, err := topoOrderForms(qforms, qualifiedSet, ctorOwner)
	if err != nil {
		fail(err)
	}

	// Seed store: a throwaway FILESYSTEM store holding the batch's external
	// dependency closure, copied from the ACTIVE store `local` by hash — so it is
	// correct whatever backend `local` uses (a filesystem path would be wrong, or
	// absent, under OATH_BACKEND=cloud). It is deliberately never the real store:
	// the unsigned apiPut below STORES AND REPOINTS, and doing that against the
	// live cloud backend would mutate production during a local validation,
	// dry-run included. Client elaboration of a dependent resolves its qualified
	// siblings from here, and the server resolves them from the registry, which is
	// why publication order is dependency order.
	tmp, err := os.MkdirTemp("", "oath-publish-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(tmp)
	// die removes the seed store before exiting. fail() calls os.Exit, which does
	// NOT run deferred functions, so a bare fail() after the temp store exists
	// would leak the copied dependency closure under the temp directory.
	die := func(e error) {
		os.RemoveAll(tmp)
		fail(e)
	}
	seedDir := filepath.Join(tmp, "store")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		die(err)
	}
	be, err := openFSBackend(seedDir)
	if err != nil {
		die(err)
	}
	seed, err := newStoreWithBackend(be, seedDir)
	if err != nil {
		die(err)
	}
	if err := seedResolutionStore(local, seed, rename, forms); err != nil {
		die(fmt.Errorf("seeding the batch's dependencies from the active store: %w", err))
	}

	// The transform must preserve every definition's IDENTITY: qualifying a name
	// may only REMAP references, never change what a definition means. So elaborate
	// the closure BOTH bare and qualified into the seed and require each definition
	// to keep the same hash. This is the one check that does not depend on
	// enumerating every syntactic position a name can occupy — a batch name that
	// collides with a reserved word (a function named `let`), a binder, a type
	// variable, or a record-field label either fails to elaborate once qualified or
	// hashes to a different object, and both are caught here rather than signing a
	// definition the author never wrote.
	bareByName := map[string]string{}
	for _, bt := range splitTopLevelForms(src) {
		pf, perr := parseForms(bt)
		if perr != nil || len(pf) != 1 {
			die(fmt.Errorf("re-parsing a definition: %v", perr))
		}
		n, nerr := declaredName(pf[0])
		if nerr != nil {
			die(nerr)
		}
		bareByName[n] = bt
	}
	qualToBare := map[string]string{}
	for b, q := range rename {
		qualToBare[q] = b
	}
	bareOrdered := make([]string, len(ordered))
	qualOrdered := make([]string, len(ordered))
	for i, qf := range ordered {
		qualOrdered[i] = qf.text
		bareOrdered[i] = bareByName[qualToBare[qf.name]]
	}
	// The bare closure must elaborate: a failure here is the closure's own defect,
	// not the namespace's.
	if err := putBatch(seed, bareOrdered, pubHex); err != nil {
		die(fmt.Errorf("this closure does not elaborate: %w", err))
	}
	// The qualified closure must ALSO elaborate; a failure means qualification
	// changed a definition's meaning.
	if err := putBatch(seed, qualOrdered, pubHex); err != nil {
		die(fmt.Errorf("qualifying this closure under %s changed a definition's meaning — a name may collide with a reserved word, a binder, or a record field; rename it: %w",
			strings.TrimSuffix(namespace, "/*"), err))
	}
	for bare, qual := range rename {
		hb, okb := seed.Resolve(bare)
		hq, okq := seed.Resolve(qual)
		if !okb || !okq || hb != hq {
			die(fmt.Errorf("qualifying %q as %q changed its identity — it collides with a reserved word, a binder, or a record field; rename it", bare, qual))
		}
	}

	// Build every plan first, so the whole batch can be shown before anything is
	// signed. Each envelope is a separate permanent authorization; the author (or a
	// --dry-run reader) must be able to inspect the artifact, parent, revision,
	// license and exact bytes of each.
	plans := make([]publishPlan, len(ordered))
	for i, qf := range ordered {
		plan, _, err := buildPublishPlan(seed, endpoint, pubHex, qf.text, license, "")
		if err != nil {
			die(fmt.Errorf("%s: %w", qf.name, err))
		}
		plans[i] = plan
	}

	// "under <ns>" only when there IS a namespace; a bare batch (friction item 5)
	// publishes new names as themselves, so the qualifier would be a lie.
	where := ""
	if namespace != "" {
		where = " under " + strings.TrimSuffix(namespace, "/*")
	}
	if jsonOut {
		// One valid JSON value: an array of the plans, machine-decodable. No
		// human-only status text on stdout in JSON mode.
		b, _ := json.MarshalIndent(plans, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("Publishing %d definitions%s, in dependency order:\n", len(ordered), where)
		for i, plan := range plans {
			fmt.Printf("\n=== [%d/%d] %s ===\n", i+1, len(plans), plan.Name)
			fmt.Println(plan.render())
		}
	}

	if dryRun {
		if !jsonOut {
			fmt.Printf("\nSIGNER: %s\n", signer.Description())
			fmt.Println("\n--dry-run: nothing signed, nothing sent.")
		}
		return
	}

	var adopted []string
	for i := range plans {
		if !assumeYes && !jsonOut {
			if !confirm(fmt.Sprintf("Sign and publish %s?", plans[i].Name)) {
				die(fmt.Errorf("aborted before signing %s (%d already published)", plans[i].Name, i))
			}
		}
		if err := finalizePublish(ctx, signer, ordered[i].text, plans[i], endpoint, jsonOut); err != nil {
			die(fmt.Errorf("%s: %w", plans[i].Name, err))
		}
		// Confirm the definition actually bound before publishing anything that
		// depends on it. finalizePublish verifies the persisted bytes when it can
		// reach the envelope, but if that read failed it returns without confirming
		// the name MOVED — and a later dependent would then fail server-side with a
		// confusing "unknown name". Stop here, at the prerequisite, instead.
		if cur, _, rerr := remoteNameRevision(endpoint, plans[i].Name); rerr != nil || cur != plans[i].Artifact {
			die(fmt.Errorf("%s did not bind on the registry (points at %s, expected %s); stopping before its dependents",
				plans[i].Name, shortHash(cur), shortHash(plans[i].Artifact)))
		}
		// Adopt the published QUALIFIED name into the LOCAL store, so the publisher
		// can build a dependent on what they just published without a round trip to
		// fetch a name mapping they authored (friction item 3). The object is already
		// present — it is exactly what was elaborated and published — so this binds a
		// name and stores no new identity; --namespace is a purely local source
		// transformation, so the bare->qualified mapping is fully known here. A failure
		// is non-fatal: the registry publication is the authoritative act, and
		// `oath resolve --remote` remains the fallback.
		if newlyBound, err := adoptPublished(local, seed, plans[i].Name, plans[i].Artifact); err != nil {
			fmt.Fprintf(os.Stderr, "warning: published %s but could not bind it in the local store (%v); `oath resolve --remote` will fetch it\n", plans[i].Name, err)
		} else if newlyBound {
			adopted = append(adopted, plans[i].Name)
		}
	}

	if !jsonOut {
		fmt.Printf("\nall %d definitions published%s.\n", len(ordered), where)
		if len(adopted) > 0 {
			fmt.Printf("bound %d published name(s) into the local store so dependents resolve without a fetch: %s\n",
				len(adopted), strings.Join(adopted, ", "))
		}
	}
}

// adoptPublished binds a just-published qualified name to its object in the local
// store, so a dependent published next from the same store resolves it locally. It
// reports whether it created a NEW binding (false when the name already pointed at
// the same object, i.e. an idempotent re-publish). The object is copied from the
// seed store only if the local store lacks it — in the common case the local store
// already holds it under a bare name, and storing it again at the same hash is a
// no-op.
func adoptPublished(local, seed *Store, name, hash string) (bool, error) {
	if cur, ok := local.Resolve(name); ok && cur == hash {
		return false, nil
	}
	if _, err := local.GetDef(hash); err != nil {
		d, derr := seed.GetDef(hash)
		if derr != nil {
			return false, derr
		}
		m, merr := seed.GetMeta(hash)
		if merr != nil {
			m = &Meta{}
		}
		if _, err := local.StoreObject(d, m); err != nil {
			return false, err
		}
	}
	if _, err := local.Repoint(name, hash); err != nil {
		return false, err
	}
	return true, nil
}

// collisionUnderQualification refuses a batch name that also appears as a
// NON-reference symbol — a binder, a type variable, a property name, a
// constructor, or a record-field label. Token qualification would rewrite that
// occurrence too, which does not change a definition's IDENTITY (binder and field
// names are metadata, so the identity check cannot see it) but DOES corrupt its
// naming metadata and can make two independent definitions look mutually recursive
// to topoOrderForms.
//
// It applies ONLY when qualifying. A bare batch (namespace == "", friction item 5)
// rewrites no tokens, so such a collision corrupts nothing and `put` accepts it —
// applying the check there would reject a valid closure. The worst a residual
// collision can do on the bare path is add a FALSE dependency edge in
// topoOrderForms (walkSyms over-collects, so a binder named like a batch member
// reads as a reference); a false edge can only over-constrain the order or, on a
// contrived mutual collision, surface as a cycle — never a wrong order and never
// corruption.
func collisionUnderQualification(forms []sx, names map[string]string, namespace string) error {
	if namespace == "" {
		return nil
	}
	nonRef := map[string]bool{}
	for _, f := range forms {
		collectDeclared(f, nonRef)
	}
	for bare := range names {
		if nonRef[bare] {
			return fmt.Errorf("%q is both a published name and a local binder, constructor, property, or record-field name in this closure; rename the local use so the published name is unambiguous", bare)
		}
	}
	return nil
}

// putBatch elaborates and stores a whole dependency-ordered batch into the store,
// returning the first rejection or hard error. It is the client-side elaboration
// used to derive definition identities and to validate a closure.
func putBatch(st *Store, defs []string, author string) error {
	reports, err := apiPut(st, strings.Join(defs, "\n\n"), author, "")
	if err != nil {
		return err
	}
	for _, r := range reports {
		if r.Status == "rejected" {
			return fmt.Errorf("%s: %s", r.Name, r.Error)
		}
	}
	return nil
}

// declaredName returns the name a top-level (defn ...) or (data ...) form binds.
func declaredName(f sx) (string, error) {
	if f.K != "list" || len(f.Kids) < 2 || f.Kids[0].K != "sym" {
		return "", fmt.Errorf("line %d: top-level forms must be (data ...) or (defn ...)", f.Line)
	}
	switch f.Kids[0].Sym {
	case "defn", "data":
		if f.Kids[1].K != "sym" {
			return "", fmt.Errorf("line %d: %s needs a name", f.Line, f.Kids[0].Sym)
		}
		return f.Kids[1].Sym, nil
	default:
		return "", fmt.Errorf("line %d: unknown top-level form %q", f.Line, f.Kids[0].Sym)
	}
}

// constructorNames returns the constructor names a (data ...) form declares
// (empty for a defn). Shape: (data Name [tyvars] (Ctor fieldTy...)...).
func constructorNames(f sx) []string {
	if f.K != "list" || len(f.Kids) < 3 || f.Kids[0].K != "sym" || f.Kids[0].Sym != "data" {
		return nil
	}
	var out []string
	for _, c := range f.Kids[3:] {
		if c.K == "list" && len(c.Kids) > 0 && c.Kids[0].K == "sym" {
			out = append(out, c.Kids[0].Sym)
		}
	}
	return out
}

// collectDeclared gathers every symbol that is NOT a free reference: binders
// (parameters, type variables, let/fn/match binders), property names, record
// field labels, and constructor names. The traversal is exhaustive — it visits
// every subexpression — so no such position is skipped; a missed one would let a
// batch name in that position be rewritten by qualifyNames.
func collectDeclared(x sx, out map[string]bool) {
	if x.K == "brace" {
		// A record type {name ty ...} or literal {name value ...}: the even-indexed
		// kids are field NAMES (labels, not references), the odd-indexed kids are
		// types or value expressions.
		for i, k := range x.Kids {
			if i%2 == 0 {
				if k.K == "sym" {
					out[k.Sym] = true
				}
			} else {
				collectDeclared(k, out)
			}
		}
		return
	}
	if x.K == "brack" {
		// A bracket in a reference position — a call's [tyargs] — is not itself a
		// binder, but a type argument can be a record type carrying field labels.
		// Traverse it so those labels are seen. (Binder brackets — a defn/fn/data's
		// parameter and type-variable lists — are handled by their own collectors,
		// not reached here.)
		for _, k := range x.Kids {
			collectDeclared(k, out)
		}
		return
	}
	if x.K != "list" || len(x.Kids) == 0 {
		return
	}
	head := ""
	if x.Kids[0].K == "sym" {
		head = x.Kids[0].Sym
	}
	switch head {
	case "defn": // (defn name [tyvars] [(p ty)...] retTy body props...)
		if len(x.Kids) > 2 {
			collectTyvarBinders(x.Kids[2], out)
		}
		if len(x.Kids) > 3 {
			collectParamBinders(x.Kids[3], out)
		}
		for _, k := range x.Kids[minInt(4, len(x.Kids)):] {
			collectDeclared(k, out)
		}
	case "data": // (data Name [tyvars] (Ctor fieldTy...)...)
		if len(x.Kids) > 2 {
			collectTyvarBinders(x.Kids[2], out)
		}
		for _, c := range x.Kids[minInt(3, len(x.Kids)):] {
			if c.K == "list" && len(c.Kids) > 0 && c.Kids[0].K == "sym" {
				out[c.Kids[0].Sym] = true // constructor name
				for _, ft := range c.Kids[1:] {
					collectDeclared(ft, out) // field types may contain record labels
				}
			}
		}
	case "prop": // (prop pname [(x ty)...] body)
		if len(x.Kids) > 1 && x.Kids[1].K == "sym" {
			out[x.Kids[1].Sym] = true // property name
		}
		if len(x.Kids) > 2 {
			collectParamBinders(x.Kids[2], out)
		}
		for _, k := range x.Kids[minInt(3, len(x.Kids)):] {
			collectDeclared(k, out)
		}
	case "fn": // (fn [(p ty)...] body)
		if len(x.Kids) > 1 {
			collectParamBinders(x.Kids[1], out)
		}
		for _, k := range x.Kids[minInt(2, len(x.Kids)):] {
			collectDeclared(k, out)
		}
	case "let": // (let (x ty expr) body)
		if len(x.Kids) > 1 && x.Kids[1].K == "list" && len(x.Kids[1].Kids) > 0 && x.Kids[1].Kids[0].K == "sym" {
			out[x.Kids[1].Kids[0].Sym] = true
		}
		for _, k := range x.Kids[1:] {
			collectDeclared(k, out)
		}
	case ".": // (. record field) — the field is a label, not a reference
		if len(x.Kids) > 1 {
			collectDeclared(x.Kids[1], out)
		}
		if len(x.Kids) > 2 && x.Kids[2].K == "sym" {
			out[x.Kids[2].Sym] = true
		}
	case "match": // (match scrut ((Ctor a b) arm)...)
		if len(x.Kids) > 1 {
			collectDeclared(x.Kids[1], out) // the scrutinee may nest binders
		}
		for _, arm := range x.Kids[minInt(2, len(x.Kids)):] {
			if arm.K == "list" && len(arm.Kids) >= 1 && arm.Kids[0].K == "list" {
				for _, b := range arm.Kids[0].Kids[1:] { // Ctor is a ref; a,b are binders
					if b.K == "sym" {
						out[b.Sym] = true
					}
				}
			}
			for _, k := range arm.Kids {
				collectDeclared(k, out)
			}
		}
	default:
		for _, k := range x.Kids {
			collectDeclared(k, out)
		}
	}
}

// collectTyvarBinders reads a [a b ...] type-variable list.
func collectTyvarBinders(brack sx, out map[string]bool) {
	if brack.K != "brack" {
		return
	}
	for _, k := range brack.Kids {
		if k.K == "sym" {
			out[k.Sym] = true
		}
	}
}

// collectParamBinders reads a [(name ty) ...] parameter list, collecting each
// parameter name AND traversing its type — a record type there carries field
// labels that must not be qualified.
func collectParamBinders(brack sx, out map[string]bool) {
	if brack.K != "brack" {
		return
	}
	for _, p := range brack.Kids {
		if p.K == "list" && len(p.Kids) > 0 {
			if p.Kids[0].K == "sym" {
				out[p.Kids[0].Sym] = true
			}
			for _, ty := range p.Kids[1:] {
				collectDeclared(ty, out)
			}
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isPublishDelim reports whether c ends a symbol token, matching the lexer's
// notion (whitespace and brackets). `;` and `"` are handled separately because
// they open a comment and a string, not merely a boundary.
func isPublishDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '(', ')', '[', ']', '{', '}':
		return true
	}
	return false
}

// qualifyNames rewrites every whole-symbol occurrence of a batch name to its
// qualified form. It skips `;` comments and "..." string literals so a name that
// appears as text is never touched, and it matches maximal symbol runs so `StrMap`
// inside `MkStrMap` is not a match. Bare Oath names never contain a delimiter, so
// the token boundaries here are exact; the caller has already refused any batch
// name that also appears as a non-reference symbol, so every match is a
// declaration or a reference.
func qualifyNames(src string, rename map[string]string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ';': // comment to end of line
			for i < len(src) && src[i] != '\n' {
				b.WriteByte(src[i])
				i++
			}
		case c == '"': // string literal, honouring backslash escapes
			b.WriteByte(c)
			i++
			for i < len(src) {
				ch := src[i]
				b.WriteByte(ch)
				i++
				if ch == '\\' && i < len(src) {
					b.WriteByte(src[i])
					i++
					continue
				}
				if ch == '"' {
					break
				}
			}
		case isPublishDelim(c):
			b.WriteByte(c)
			i++
		default: // a maximal symbol run
			j := i
			for j < len(src) && !isPublishDelim(src[j]) && src[j] != ';' && src[j] != '"' {
				j++
			}
			tok := src[i:j]
			if q, ok := rename[tok]; ok {
				b.WriteString(q)
			} else {
				b.WriteString(tok)
			}
			i = j
		}
	}
	return b.String()
}

// splitTopLevelForms returns the source text of each top-level parenthesised form,
// in order, skipping `;` comments and "..." strings so a paren inside either does
// not shift the depth. It preserves each form's exact bytes.
func splitTopLevelForms(src string) []string {
	var forms []string
	depth := 0
	start := -1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ';':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '"':
			i++
			for i < len(src) {
				ch := src[i]
				i++
				if ch == '\\' && i < len(src) {
					i++
					continue
				}
				if ch == '"' {
					break
				}
			}
		case c == '(' || c == '[' || c == '{':
			if depth == 0 && c == '(' {
				start = i
			}
			depth++
			i++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 && start >= 0 {
				forms = append(forms, strings.TrimSpace(src[start:i+1]))
				start = -1
			}
			i++
		default:
			i++
		}
	}
	return forms
}

type qform struct {
	name string // qualified declared name
	text string // qualified source text
}

// topoOrderForms orders the qualified forms so every definition follows the batch
// members it references. batch is the set of qualified names being published;
// ctorOwner maps a batch constructor to its (qualified) datatype, so a definition
// that uses a datatype only through a bare constructor still follows the datatype.
// References outside the batch impose no ordering, and a cycle is reported rather
// than silently broken.
func topoOrderForms(qforms []string, batch map[string]bool, ctorOwner map[string]string) ([]qform, error) {
	n := len(qforms)
	names := make([]string, n)
	deps := make([]map[string]bool, n)
	for i, text := range qforms {
		f, err := parseForms(text)
		if err != nil || len(f) != 1 {
			return nil, fmt.Errorf("could not re-parse a qualified definition: %v", err)
		}
		name, err := declaredName(f[0])
		if err != nil {
			return nil, err
		}
		names[i] = name
		// walkSyms OVER-collects deliberately: it is flat, not scope-aware, so a batch
		// member's name reused as a binder reads as a reference. That is the safe
		// direction. Under-collecting (subtracting this form's binders) would DROP a
		// genuine free reference whenever the same name is also shadowed in a nested
		// scope, mis-ordering a closure `put` accepts. Over-collecting can only ADD an
		// edge, which at worst reports a cycle — and the one input that produces a
		// FALSE cycle here (two defs each naming a parameter after the other) is
		// indistinguishable from genuine mutual recursion, which cannot be published as
		// separate one-name envelopes at all. So a cycle here is the right answer for
		// the case that matters and an acceptable refusal for an adversarial one.
		refs := map[string]bool{}
		for _, s := range walkSyms(f[0]) {
			if s == name {
				continue
			}
			if batch[s] {
				refs[s] = true // direct reference to a batch name
			} else if owner, ok := ctorOwner[s]; ok && owner != name {
				refs[owner] = true // a batch constructor depends on its datatype
			}
		}
		deps[i] = refs
	}

	// Kahn's algorithm over the intra-batch dependency edges. Ties break by
	// declaration order so the output is deterministic.
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		indeg[i] = len(deps[i])
	}
	var ready []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	var out []qform
	placed := make([]bool, n)
	for len(ready) > 0 {
		sort.Ints(ready)
		i := ready[0]
		ready = ready[1:]
		placed[i] = true
		out = append(out, qform{name: names[i], text: qforms[i]})
		for j := 0; j < n; j++ {
			if placed[j] || !deps[j][names[i]] {
				continue
			}
			indeg[j]--
			if indeg[j] == 0 {
				ready = append(ready, j)
			}
		}
	}
	if len(out) != n {
		var stuck []string
		for i := 0; i < n; i++ {
			if !placed[i] {
				stuck = append(stuck, names[i])
			}
		}
		return nil, fmt.Errorf("the definitions form a dependency cycle and cannot be ordered: %s", strings.Join(stuck, ", "))
	}
	return out, nil
}

// walkSyms collects every symbol appearing anywhere in a form. Over-collection is
// harmless for dependency analysis: an edge to a batch name the definition does
// not truly use only orders it later, never wrong.
func walkSyms(x sx) []string {
	var out []string
	var rec func(sx)
	rec = func(n sx) {
		if n.K == "sym" {
			out = append(out, n.Sym)
		}
		for _, k := range n.Kids {
			rec(k)
		}
	}
	rec(x)
	return out
}

// seedResolutionStore populates the throwaway seed store with the batch's
// external dependency closure, read from the ACTIVE store `local` by hash. It
// copies whatever backend `local` uses (filesystem, cloud, memory) and mutates
// only `seed`, never `local`.
//
// batch maps each bare declared name to its qualified form; those are the names
// the batch defines and must NOT be copied (apiPut binds them). Every other name
// a form references and that resolves in `local` is an external dependency: its
// object, and the transitive closure of objects it references, are copied by
// hash (content addressing makes the recomputed hash match), and the name is
// bound so the qualified batch elaborates. Constructors of an external datatype
// resolve through that datatype's object, so copying the datatype is enough.
func seedResolutionStore(local, seed *Store, batch map[string]string, forms []sx) error {
	copied := map[string]bool{}
	var copyClosure func(h string) error
	copyClosure = func(h string) error {
		if copied[h] {
			return nil
		}
		copied[h] = true
		d, err := local.GetDef(h)
		if err != nil {
			return err
		}
		for dep := range collectDeps(d) {
			if err := copyClosure(dep); err != nil {
				return err
			}
		}
		m, merr := local.GetMeta(h)
		if merr != nil {
			m = &Meta{}
		}
		_, err = seed.StoreObject(d, m)
		return err
	}
	// bind copies a name's object closure into the seed and binds the name. A name
	// that does not resolve (a builtin, a bare constructor, a type variable) is a
	// no-op — those need no object.
	bind := func(name string) error {
		h, ok := local.Resolve(name)
		if !ok {
			return nil
		}
		if err := copyClosure(h); err != nil {
			return fmt.Errorf("copying dependency %q: %w", name, err)
		}
		if _, err := seed.Repoint(name, h); err != nil {
			return fmt.Errorf("binding dependency %q: %w", name, err)
		}
		return nil
	}

	// Every datatype in the active store. A constructor name is metadata, not a
	// resolvable name, so a closure that uses an external datatype ONLY through a
	// bare constructor — constructing and matching a value with no type annotation
	// — never names the datatype anywhere the reference scan below could see it.
	// Datatypes are few; seeding them all makes any constructor resolve.
	for name, h := range local.Names() {
		d, err := local.GetDef(h)
		if err != nil {
			continue
		}
		if d.K == "data" {
			if err := bind(name); err != nil {
				return err
			}
		}
	}

	// Every other name the batch actually references (functions), and their
	// transitive closures.
	seen := map[string]bool{}
	for _, f := range forms {
		for _, s := range walkSyms(f) {
			if _, isBatch := batch[s]; isBatch || seen[s] {
				continue
			}
			seen[s] = true
			if err := bind(s); err != nil {
				return err
			}
		}
	}
	return nil
}
