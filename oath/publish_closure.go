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

// publishBatch is the SOURCE TRANSFORMATION a closure publication performs, computed
// before anything is signed or sent. It is a pure function of (source, namespace,
// local store) so it can be driven — and falsified — without a registry, a signer, or
// a process exit.
type publishBatch struct {
	forms    []sx              // every top-level form, aliases included
	rename   map[string]string // bare declared name -> qualified name (definitions only)
	aliasExt map[string]string // type-alias external dependency: name -> the hash it resolved to
	ordered  []qform           // the PUBLISHED definitions, in dependency order
	// planText[i] is the EXACT source published for ordered[i]: the type aliases that
	// definition needs, then the definition itself. It is what gets hashed, signed and
	// re-elaborated by the registry — one text, so those cannot drift apart. It always
	// declares exactly ONE definition, because one signature authorises one transition.
	planText []string
	// batchBare/batchQual are the whole closure laid out as ONE elaboration unit for
	// the local identity control. They are NOT planText concatenated: an alias may be
	// registered only once per batch, so here each appears once, at its first use.
	batchBare []string
	batchQual []string
}

// buildPublishBatch qualifies the source, classifies its type aliases, and orders the
// definitions so each follows what it depends on — including what it depends on only
// through an alias.
func buildPublishBatch(local *Store, src, namespace string) (publishBatch, error) {
	var zero publishBatch
	forms, err := parseForms(src)
	if err != nil {
		return zero, err
	}
	if len(forms) == 0 {
		return zero, fmt.Errorf("no definitions to publish")
	}
	parts := splitTopLevelForms(src)
	if len(parts) != len(forms) {
		return zero, fmt.Errorf("internal: split %d source forms from %d parsed definitions", len(parts), len(forms))
	}

	// The rename table (each declared name -> its namespaced form) and the batch's
	// constructors (a bare ctor is a dependency on its datatype even though it is
	// not itself qualified). applyNamespace validates the namespace pattern and
	// refuses a source that already carries a prefix, so a name keeps ONE source of
	// truth.
	//
	// (type ...) forms are skipped throughout: an alias binds no published name, so it
	// takes no rename entry, occupies no duplicate-name slot, is no topology node, and
	// becomes no plan and no envelope. It is carried alongside the definitions that
	// need it instead.
	rename := map[string]string{}
	qualifiedSet := map[string]bool{}
	ctorOwner := map[string]string{} // qualified ctor-owner: ctor name -> qualified datatype
	declaredAt := map[string]int{}   // bare declared name -> its index in `forms`
	var defIdx []int                 // indices of the forms that ARE published
	for i, f := range forms {
		if isTypeAliasForm(f) {
			continue
		}
		name, err := declaredName(f)
		if err != nil {
			return zero, err
		}
		if _, dup := rename[name]; dup {
			return zero, fmt.Errorf("%q is declared twice in this file", name)
		}
		q, err := applyNamespace(name, namespace)
		if err != nil {
			return zero, err
		}
		rename[name] = q
		qualifiedSet[q] = true
		declaredAt[name] = i
		defIdx = append(defIdx, i)
		for _, c := range constructorNames(f) {
			ctorOwner[c] = q
		}
	}
	if len(defIdx) == 0 {
		return zero, fmt.Errorf("no definitions to publish: this file declares only (type ...) aliases, which are batch-local sugar with no published identity")
	}

	if err := collisionUnderQualification(forms, rename, namespace); err != nil {
		return zero, err
	}

	qsrc := qualifyNames(src, rename)
	qparts := splitTopLevelForms(qsrc)
	if len(qparts) != len(parts) {
		return zero, fmt.Errorf("internal: qualified %d forms from %d definitions", len(qparts), len(parts))
	}

	// The type aliases and their dependencies, each classified at its OWN source
	// position — the position where its body is elaborated, and so the only one at
	// which its references mean what the published object will carry.
	aliases, aerr := collectBatchAliases(forms, parts, qparts, declaredAt, local)
	if aerr != nil {
		return zero, aerr
	}
	bareAliasText := make([]string, len(aliases))
	qualAliasText := make([]string, len(aliases))
	aliasExt := map[string]string{}
	for i, a := range aliases {
		bareAliasText[i], qualAliasText[i] = a.bare, a.qual
		for n, h := range a.ext {
			aliasExt[n] = h
		}
	}

	// Per published definition: which aliases it needs in scope, and the extra
	// dependency edges those aliases contribute. A definition writing `(p Env)`
	// mentions no batch name even when Env expands to one, so without these edges it
	// could be ordered — and published — ahead of the datatype it actually depends on.
	defBare := make([]string, len(defIdx))
	defQual := make([]string, len(defIdx))
	needs := make([][]int, len(defIdx))
	extra := make([]map[string]bool, len(defIdx))
	for k, i := range defIdx {
		defBare[k], defQual[k] = parts[i], qparts[i]
		needs[k] = aliasesNeededBy(forms[i], i, aliases)
		extra[k] = map[string]bool{}
		for _, ai := range needs[k] {
			for b := range aliases[ai].batch {
				if q, ok := rename[b]; ok {
					extra[k][q] = true
				}
			}
		}
	}

	// Dependency order, derived from which qualified names (and which batch
	// constructors) each definition references. A dependent must be published
	// after its deps.
	ordered, err := topoOrderForms(defQual, qualifiedSet, ctorOwner, extra)
	if err != nil {
		return zero, err
	}

	out := publishBatch{forms: forms, rename: rename, aliasExt: aliasExt, ordered: ordered}
	qualText := make([]string, len(ordered))
	bareText := make([]string, len(ordered))
	orderedNeeds := make([][]int, len(ordered))
	out.planText = make([]string, len(ordered))
	for i, qf := range ordered {
		orderedNeeds[i] = needs[qf.idx]
		qualText[i] = qf.text
		bareText[i] = defBare[qf.idx]
		out.planText[i] = planSource(qf.text, orderedNeeds[i], qualAliasText)
	}
	out.batchBare = batchSource(bareText, orderedNeeds, bareAliasText)
	out.batchQual = batchSource(qualText, orderedNeeds, qualAliasText)
	return out, nil
}

// cmdPublishClosure publishes every definition in src under the namespace, in
// dependency order.
func cmdPublishClosure(ctx context.Context, signer Signer, pubHex string, local *Store, endpoint, src, license, namespace string, dryRun, jsonOut, assumeYes bool) {
	batch, err := buildPublishBatch(local, src, namespace)
	if err != nil {
		fail(err)
	}
	ordered := batch.ordered

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
	if err := seedResolutionStore(local, seed, batch.rename, batch.forms, batch.aliasExt); err != nil {
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
	// The bare closure must elaborate: a failure here is the closure's own defect,
	// not the namespace's.
	if err := putBatch(seed, batch.batchBare, pubHex); err != nil {
		die(fmt.Errorf("this closure does not elaborate: %w", err))
	}
	// The qualified closure must ALSO elaborate; a failure means qualification
	// changed a definition's meaning.
	if err := putBatch(seed, batch.batchQual, pubHex); err != nil {
		die(fmt.Errorf("qualifying this closure under %s changed a definition's meaning — a name may collide with a reserved word, a binder, or a record field; rename it: %w",
			strings.TrimSuffix(namespace, "/*"), err))
	}
	for bare, qual := range batch.rename {
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
	sendText := make([]string, len(ordered))
	for i, qf := range ordered {
		plan, _, send, err := buildPublishPlan(seed, endpoint, pubHex, batch.planText[i], license, "")
		if err != nil {
			die(fmt.Errorf("%s: %w", qf.name, err))
		}
		// planSource puts every alias ABOVE the definition, so there is nothing to trim
		// and this is the plan text unchanged. Taken from the plan builder regardless, so
		// the bytes validated and the bytes sent have one owner.
		sendText[i] = send
		if plan.Name != qf.name {
			die(fmt.Errorf("internal: publishing %s produced a plan for %s — the alias-prefixed source declares more than the intended definition", qf.name, plan.Name))
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
		if err := finalizePublish(ctx, signer, sendText[i], plans[i], endpoint, jsonOut); err != nil {
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
			return fmt.Errorf("%q is both a published name and a batch-local name in this closure — a type alias, binder, constructor, property, or record-field name; rename the local use so the published name is unambiguous", bare)
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

// --- batch-local type aliases in a publish batch ---
//
// A (type Name ty) form is identity-transparent surface sugar (SPEC §1.4): it stores
// no object, appends no journal entry, and has no published name. So it is NOT a unit
// of publication — it takes no rename entry, no duplicate-name slot, no topology node,
// no plan and no envelope. What it DOES carry is a dependency: its body names types,
// and a definition using the alias depends on them just as if it had spelled the type
// inline.
//
// Those dependencies are recorded by RESOLVED HASH, not by name, and that distinction
// is the whole reason this type exists. An alias body is elaborated ONCE, at its own
// position in the source, against whatever the names meant THERE. A later form
// declaring the same name does not reach back and change it. Recording the name alone
// would lose that: the batch-name filter in seeding would ERASE an external dependency
// whose name a later batch form happens to reuse, and token qualification would
// RETARGET the alias body at the batch's definition instead of the external one it
// actually resolved to.
type batchAlias struct {
	formIdx int               // index in the source's top-level form list
	name    string            // the alias's own name (batch-local; never published)
	bare    string            // exact bare source text of the (type ...) form
	qual    string            // exact qualified source text of the same form
	uses    []int             // earlier aliases this body expands, ascending
	batch   map[string]bool   // BARE batch names the body binds to (declared EARLIER)
	ext     map[string]string // external name -> the hash it resolved to, pinned here
}

// isTypeAliasForm reports whether a top-level form is a (type ...) alias.
func isTypeAliasForm(f sx) bool {
	return f.K == "list" && len(f.Kids) > 0 && f.Kids[0].K == "sym" && f.Kids[0].Sym == "type"
}

// aliasFormName returns an alias form's declared name, or "?" if it is malformed.
// registerTypeAlias does the real validation; this is only for messages.
func aliasFormName(f sx) string {
	if len(f.Kids) > 1 && f.Kids[1].K == "sym" {
		return f.Kids[1].Sym
	}
	return "?"
}

// --- type-position analysis ---
//
// Alias dependencies are a question about TYPES, so they are read off the type
// positions rather than off every symbol in the form. A flat symbol walk answers a
// different question and answers it confidently: it reads record FIELD LABELS and value
// BINDERS as type references, which refuses valid batches and transmits aliases a
// definition never uses. (Both found by external review, on exactly those shapes.)
//
// The positions below are derived from the elaborator's own parseTy call sites, not
// from the shapes that came to mind — that is the only way the set can be complete.
// Under-collection is not silent: a missing dependency makes the batch fail to
// elaborate in the validation put, with the same unknown-type message `put` gives.

// collectTypeRefs walks TYPE syntax and records the symbols in type-NAME position.
// parseTy accepts exactly: a record brace (odd kids are types, EVEN kids are labels), a
// bare symbol, an arrow (every kid after `->`), or an application whose head is itself a
// type name.
func collectTypeRefs(x sx, out map[string]bool) {
	switch x.K {
	case "sym":
		out[x.Sym] = true
	case "brace":
		for i := 1; i < len(x.Kids); i += 2 { // labels sit at the even indices
			collectTypeRefs(x.Kids[i], out)
		}
	case "list":
		if len(x.Kids) == 0 {
			return
		}
		if x.Kids[0].isSym("->") {
			for _, k := range x.Kids[1:] {
				collectTypeRefs(k, out)
			}
			return
		}
		for _, k := range x.Kids { // (Name arg ...) — the head is a type name too
			collectTypeRefs(k, out)
		}
	}
}

// collectParamTypeRefs reads the types out of a [(name ty) ...] binder list.
func collectParamTypeRefs(brack sx, out map[string]bool) {
	if brack.K != "brack" {
		return
	}
	for _, p := range brack.Kids {
		if p.K == "list" && len(p.Kids) == 2 {
			collectTypeRefs(p.Kids[1], out)
		}
	}
}

// collectTermTypeRefs walks a TERM and records only what the elaborator parses as a
// type there: a let's binder type, an fn's parameter types, and a call's [tyargs].
func collectTermTypeRefs(x sx, out map[string]bool) {
	// A record LITERAL {label expr ...} in a term position: the odd kids are TERMS and
	// may carry types of their own (a let binder, an fn parameter, a call's tyargs).
	// Returning early here missed an alias used only inside one, which then landed
	// after its use in the batch layout and failed the validation put. Found by review.
	if x.K == "brace" {
		for i := 1; i < len(x.Kids); i += 2 {
			collectTermTypeRefs(x.Kids[i], out)
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
	case "let": // (let (x ty expr) body)
		if len(x.Kids) > 1 && x.Kids[1].K == "list" && len(x.Kids[1].Kids) == 3 {
			collectTypeRefs(x.Kids[1].Kids[1], out)
			collectTermTypeRefs(x.Kids[1].Kids[2], out)
		}
		for _, k := range x.Kids[minInt(2, len(x.Kids)):] {
			collectTermTypeRefs(k, out)
		}
	case "fn": // (fn [(p ty) ...] body)
		if len(x.Kids) > 1 {
			collectParamTypeRefs(x.Kids[1], out)
		}
		for _, k := range x.Kids[minInt(2, len(x.Kids)):] {
			collectTermTypeRefs(k, out)
		}
	default:
		// A named application may carry type arguments: (name [ty ...] arg ...).
		if len(x.Kids) > 1 && x.Kids[0].K == "sym" && x.Kids[1].K == "brack" {
			for _, k := range x.Kids[1].Kids {
				collectTypeRefs(k, out)
			}
			for _, k := range x.Kids[2:] {
				collectTermTypeRefs(k, out)
			}
			return
		}
		for _, k := range x.Kids {
			collectTermTypeRefs(k, out)
		}
	}
}

// formTypeRefs returns every type a top-level form REFERENCES — the set that decides
// which aliases it needs in scope. Binder names, property names, constructor names and
// record labels are all excluded by construction, because none of them is a type.
func formTypeRefs(f sx) map[string]bool {
	out := map[string]bool{}
	if f.K != "list" || len(f.Kids) == 0 || f.Kids[0].K != "sym" {
		return out
	}
	// The form's own [tyvars] SHADOW aliases: parseTy resolves a bound type variable
	// before it consults the alias map, so `(defn id [A] [(x A)] A x)` does not use an
	// alias named A at all. Recording it prepends that alias to the plan, which can be
	// refused at a registry holding a datatype of the same name. Found by review.
	bound := map[string]bool{}
	if len(f.Kids) > 2 {
		collectTyvarBinders(f.Kids[2], bound)
	}
	// scoped adds refs that the form's type variables shadow; unscoped adds refs from a
	// position where they do NOT. The distinction is load-bearing and was VERIFIED
	// against the elaborator rather than read off it: elabFuncRaw builds a FRESH elab
	// for each property WITHOUT the function's tyvars, so inside a property the same
	// spelling resolves to the ALIAS. Subtracting the tyvars there would drop a genuine
	// dependency and fail the validation put.
	scoped := func(add func(map[string]bool)) {
		got := map[string]bool{}
		add(got)
		for sym := range got {
			if !bound[sym] {
				out[sym] = true
			}
		}
	}
	switch f.Kids[0].Sym {
	case "defn": // (defn n [tyvars] [(p ty)...] retTy body prop...)
		if len(f.Kids) > 3 {
			scoped(func(m map[string]bool) { collectParamTypeRefs(f.Kids[3], m) })
		}
		if len(f.Kids) > 4 {
			scoped(func(m map[string]bool) { collectTypeRefs(f.Kids[4], m) })
		}
		if len(f.Kids) > 5 {
			scoped(func(m map[string]bool) { collectTermTypeRefs(f.Kids[5], m) })
		}
		for _, p := range f.Kids[minInt(6, len(f.Kids)):] { // (prop n [(x ty)...] body)
			if p.K != "list" {
				continue
			}
			if len(p.Kids) > 2 { // NOT scoped — a property sees no tyvars
				collectParamTypeRefs(p.Kids[2], out)
			}
			if len(p.Kids) > 3 {
				collectTermTypeRefs(p.Kids[3], out)
			}
		}
	case "data": // (data N [tyvars] (Ctor fieldTy...)...)
		for _, c := range f.Kids[minInt(3, len(f.Kids)):] {
			if c.K == "list" {
				for _, ft := range c.Kids[1:] { // Kids[0] is the constructor NAME
					scoped(func(m map[string]bool) { collectTypeRefs(ft, m) })
				}
			}
		}
	case "type":
		for _, sym := range aliasBodySyms(f) {
			out[sym] = true
		}
	}
	return out
}

// aliasBodySyms returns the symbols in an alias's BODY that are genuine type
// REFERENCES: the alias's own name and its bound type variables are removed.
//
// The tyvars are not cosmetic. parseTy resolves a bound type variable before it
// consults aliases or the store, so in `(type A [Point] Point)` the body's `Point` IS
// the parameter and names no type at all — reading it as a reference makes a valid
// batch that later declares a datatype `Point` look like a forward reference and
// refuses it. Found by external review, on exactly that source.
func aliasBodySyms(f sx) []string {
	if len(f.Kids) < 3 {
		return nil
	}
	bound := map[string]bool{}
	if f.Kids[2].K == "brack" { // the parametric form's [tyvars]
		for _, k := range f.Kids[2].Kids {
			if k.K == "sym" {
				bound[k.Sym] = true
			}
		}
	}
	refs := map[string]bool{}
	collectTypeRefs(f.Kids[len(f.Kids)-1], refs)
	out := make([]string, 0, len(refs))
	for sym := range refs {
		if !bound[sym] {
			out = append(out, sym)
		}
	}
	sort.Strings(out) // deterministic, so a refusal names the same symbol every run
	return out
}

// collectBatchAliases records every (type ...) form with its dependencies, classified
// AT ITS OWN SOURCE POSITION — which is where its body is elaborated, and therefore the
// only position at which "what does this name mean?" has the answer the published
// object will carry.
//
// A body reference is: an EARLIER alias (expanded in place), an EARLIER batch
// declaration (a same-batch dependency, which the using definition must follow), or an
// external name, pinned to the hash it resolves to in `local` right now.
//
// The one case with no faithful publication is refused rather than approximated: a body
// reference that resolves EXTERNALLY and whose name a LATER form in the same batch also
// declares. `put` reads that source unambiguously (the alias binds the external object,
// the later declaration binds afterwards), but publication cannot reproduce it —
// qualification rewrites the token inside the alias body to the batch's qualified name,
// and dependency ordering may place the batch declaration before the alias registers.
// Either way the alias would silently mean a different type than the author wrote, and
// a silently different type is exactly what a content-addressed publication must not
// produce.
func collectBatchAliases(forms []sx, parts, qparts []string, declaredAt map[string]int, local *Store) ([]batchAlias, error) {
	var out []batchAlias
	aliasIdx := map[string]int{} // alias name -> position in `out`
	for i, f := range forms {
		if !isTypeAliasForm(f) {
			continue
		}
		a := batchAlias{
			formIdx: i,
			name:    aliasFormName(f),
			bare:    parts[i],
			qual:    qparts[i],
			batch:   map[string]bool{},
			ext:     map[string]string{},
		}
		seen := map[string]bool{}
		for _, sym := range aliasBodySyms(f) {
			if seen[sym] {
				continue
			}
			seen[sym] = true
			declIdx, isBatch := declaredAt[sym]
			prior, isAlias := aliasIdx[sym] // only EARLIER aliases are in the map yet
			switch {
			case isAlias:
				// an earlier alias: its own dependencies are already recorded
				a.uses = append(a.uses, prior)
			case isBatch && declIdx < i:
				a.batch[sym] = true
			case isBatch:
				// Declared LATER in this batch — the two sub-cases fail for different
				// reasons and both are refused, because publication REORDERS
				// definitions and so cannot preserve either reading.
				if _, ok := local.Resolve(sym); ok {
					return nil, fmt.Errorf("line %d: the alias %q resolves %q to a definition outside this file, "+
						"but %q is also declared later in the same file. Publication reorders definitions "+
						"and (under a namespace) rewrites the name inside the alias body, either of which "+
						"would silently retarget %q at the local declaration. Move that declaration above "+
						"the alias, or rename one of them",
						f.Line, a.name, sym, sym, a.name)
				}
				// Nothing resolves it here, so `put` reads this source as an unknown
				// type. Publishing must not quietly accept it by reordering the
				// declaration ahead of the alias.
				return nil, fmt.Errorf("line %d: the alias %q references %q, which this file declares LATER "+
					"and nothing outside it defines. Move the declaration of %q above the alias",
					f.Line, a.name, sym, sym)
			default:
				if h, ok := local.Resolve(sym); ok {
					a.ext[sym] = h
				}
				// otherwise a builtin, a type variable, or genuinely unknown — the
				// elaborator is the authority on which, and reports it in context.
			}
		}
		sort.Ints(a.uses)
		aliasIdx[a.name] = len(out)
		out = append(out, a)
	}
	return out, nil
}

// aliasesNeededBy returns the aliases a definition must have in scope to elaborate:
// those it names, plus everything those expand, transitively. Ascending, so a chained
// alias always follows the alias it expands. Over-collection (a binder spelled like an
// alias) only prepends sugar the definition does not use, which elaborates to nothing.
//
// SCOPE IS BOUNDED BY SOURCE POSITION: only aliases declared ABOVE the definition (at
// form index < defIdx) count. An alias declared below is not in scope for it — that is
// what `put` does, and publication must not manufacture scope by hoisting the alias
// above its use, which would let publish accept a file `put` rejects. Excluding it here
// rather than refusing outright keeps the report identical to `put`'s: the definition
// simply fails to elaborate with an unknown type, and a binder that merely SHARES a
// later alias's spelling still costs nothing.
func aliasesNeededBy(f sx, defIdx int, aliases []batchAlias) []int {
	if len(aliases) == 0 {
		return nil
	}
	byName := make(map[string]int, len(aliases))
	for i, a := range aliases {
		if a.formIdx < defIdx {
			byName[a.name] = i
		}
	}
	need := map[int]bool{}
	var mark func(int)
	mark = func(i int) {
		if need[i] {
			return
		}
		need[i] = true
		for _, u := range aliases[i].uses {
			mark(u)
		}
	}
	for sym := range formTypeRefs(f) {
		if i, ok := byName[sym]; ok {
			mark(i)
		}
	}
	idxs := make([]int, 0, len(need))
	for i := range need {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	return idxs
}

// planSource is the EXACT source published for one definition: the aliases it needs,
// then the definition. It must be self-contained, because the registry re-elaborates
// these bytes and has no other way to learn what the alias means — and it must contain
// exactly ONE definition, because one signature authorises one name transition.
func planSource(defText string, need []int, aliasTexts []string) string {
	if len(need) == 0 {
		return defText
	}
	parts := make([]string, 0, len(need)+1)
	for _, i := range need {
		parts = append(parts, aliasTexts[i])
	}
	return strings.Join(append(parts, defText), "\n\n")
}

// batchSource lays the whole ordered batch out as ONE elaboration unit, for the local
// validation puts. Each alias appears EXACTLY ONCE — registerTypeAlias refuses a
// duplicate within a batch, so the per-plan texts (which each repeat the aliases they
// need) cannot simply be concatenated.
//
// EVERY alias is emitted, including one no definition uses. That is not tidiness: the
// validation puts are the only place a `publish` batch elaborates its aliases, so an
// alias left out is an alias never checked — and a malformed one, a duplicate, or one
// naming an unknown type would be silently DROPPED from the publication while `put`
// refuses the same file. (Found by review, with exactly that source.)
//
// Placement: at the first definition that needs it, or — for an unused one — after
// every definition, where any batch name it references is certainly bound. Both are in
// scope, because an alias's same-batch dependencies are ordered ahead of every
// definition needing it, and collectBatchAliases refuses a body that references a batch
// name declared after the alias. Ties break by declaration order, so an alias expanding
// an earlier alias always follows it.
func batchSource(defTexts []string, needs [][]int, aliasTexts []string) []string {
	emitted := make([]bool, len(aliasTexts))
	out := make([]string, 0, len(defTexts)+len(aliasTexts))
	for k, text := range defTexts {
		for _, i := range needs[k] {
			if !emitted[i] {
				emitted[i] = true
				out = append(out, aliasTexts[i])
			}
		}
		out = append(out, text)
	}
	for i, text := range aliasTexts {
		if !emitted[i] {
			out = append(out, text)
		}
	}
	return out
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
	case "type":
		// A (type ...) alias declares no PUBLISHED name — it is batch-local elaboration
		// sugar with no object, no journal entry and no envelope. Callers must filter it
		// out with isTypeAliasForm before asking for a declared name; reaching here means
		// an alias leaked into the rename table, the topology, or the plan list, and
		// inventing a name for it would publish sugar as if it were a definition.
		return "", fmt.Errorf("line %d: internal: (type %s ...) has no published name; "+
			"alias forms must be filtered before this point", f.Line, aliasFormName(f))
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

// collectPatternBinders records the binders introduced by one match-pattern position.
// A position is either a name (`n`) or a nested constructor pattern (`(MkRun n x)`),
// whose head is a constructor reference and whose remaining positions are themselves
// binder positions — so it must recurse to reach binders nested to any depth.
func collectPatternBinders(b sx, out map[string]bool) {
	switch {
	case b.K == "sym":
		out[b.Sym] = true
	case b.K == "list" && len(b.Kids) > 0 && b.Kids[0].K == "sym":
		for _, sub := range b.Kids[1:] {
			collectPatternBinders(sub, out)
		}
	}
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
	case "type": // (type Name [tyvars] ty)
		// The alias NAME is a batch-local declaration, not a reference — the same class
		// as a binder or a field label, and it belongs in this set for the same reason:
		// qualifyNames is token-based, so under a namespace it rewrites `(type f Int)` to
		// `(type team/f Int)`, retargeting a declaration that publishes nothing. Where
		// `team/f` is already a datatype, alias registration then refuses the generated
		// source and blocks a legitimate replacement publication. Measured, not reasoned
		// about; found by external review.
		//
		// It is REFUSED rather than repaired because token qualification cannot tell a
		// type-position `f` (the alias) from a term-position `f` (the function), which is
		// exactly why binders and field labels are refused here too.
		if len(x.Kids) > 1 && x.Kids[1].K == "sym" {
			out[x.Kids[1].Sym] = true
		}
		if len(x.Kids) > 2 {
			collectTyvarBinders(x.Kids[2], out) // no-op unless the parametric form
		}
		for _, k := range x.Kids[minInt(2, len(x.Kids)):] {
			collectDeclared(k, out) // the body may carry record-field labels
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
				for _, b := range arm.Kids[0].Kids[1:] { // Ctor is a ref; the rest are binders
					collectPatternBinders(b, out)
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
	text string // qualified source text (the definition alone; aliases are prefixed later)
	idx  int    // index in the caller's input order, so per-definition state stays attached
}

// topoOrderForms orders the qualified forms so every definition follows the batch
// members it references. batch is the set of qualified names being published;
// ctorOwner maps a batch constructor to its (qualified) datatype, so a definition
// that uses a datatype only through a bare constructor still follows the datatype.
// References outside the batch impose no ordering, and a cycle is reported rather
// than silently broken.
//
// extra carries edges the definition's own text does not show: the same-batch types
// its type ALIASES depend on. An alias is expanded, not referenced, so `(p Env)`
// mentions no batch name even when Env's body does — without these the definition
// could be ordered before the datatype its alias expands to. Qualified names, index-
// aligned with qforms; nil for a batch with no aliases.
func topoOrderForms(qforms []string, batch map[string]bool, ctorOwner map[string]string, extra []map[string]bool) ([]qform, error) {
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
		if extra != nil {
			for r := range extra[i] {
				if r != name {
					refs[r] = true
				}
			}
		}
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
		out = append(out, qform{name: names[i], text: qforms[i], idx: i})
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
//
// aliasExt is the union of every type alias's external dependencies, pinned to the HASH
// each resolved to at its own position in the source, and it is seeded FIRST so an
// alias's binding derives from what that alias ACTUALLY resolved rather than from a
// second lookup of the same name.
//
// Stated honestly, because a guard whose reach is overclaimed is worse than none: this
// is redundant TODAY. The name walk below already visits the alias forms, and the
// datatype sweep already binds every datatype in `local`, so the one input on which the
// two would disagree — an alias body resolving an external name that a LATER form in
// the same batch also declares, which the walk's batch filter would drop — never
// reaches here: collectBatchAliases refuses it. What this buys is that the refusal and
// the seeding are not the SAME assumption written twice; if the refusal is ever
// narrowed, the seeding is already derived from the recorded resolution.
func seedResolutionStore(local, seed *Store, batch map[string]string, forms []sx, aliasExt map[string]string) error {
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

	// The type aliases' external dependencies, by the hash each actually resolved to.
	for name, h := range aliasExt {
		if err := copyClosure(h); err != nil {
			return fmt.Errorf("copying type-alias dependency %q (#%s): %w", name, shortHash(h), err)
		}
		if _, err := seed.Repoint(name, h); err != nil {
			return fmt.Errorf("binding type-alias dependency %q: %w", name, err)
		}
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
