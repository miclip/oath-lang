package main

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SMT-backed proof with structural induction: the top rung of the guarantee
// ladder. Three layers:
//
//  1. Translation. Monomorphic data instances become SMT algebraic
//     datatypes; pattern matches become tester/selector ite-chains; strings
//     map to SMT strings; non-recursive callees are inlined; RECURSIVE
//     functions are declared uninterpreted with their defining equations
//     asserted as quantified axioms.
//  2. Proof search. Each property is attempted directly (negate, check-sat),
//     then by structural induction on each datatype-typed binder: one
//     subgoal per constructor, with induction hypotheses for recursive
//     fields GENERALIZED over the remaining binders.
//  3. The lemma library. Proven properties of referenced definitions — and
//     earlier proven properties of the same definition — are asserted as
//     axioms. Proof power composes bottom-up through the hash graph exactly
//     like totality and confinement verdicts do.
//
// Honest bail-outs remain: division/modulo (kernel truncates, SMT-LIB is
// Euclidean — translating would prove the wrong theorem), records, partial
// application. And the standing caveat on every proof: Z3 reasons over
// unbounded integers; the evaluator uses int64.

// Deterministic proof budget (#17 adjudication): outcomes must be a pure
// function of (script bytes, solver version, budget). Wall-clock budgets
// are machine-dependent — the sum/CI episode and the q-drop borderline
// flip both trace to them — so the budget is z3's rlimit, a deterministic
// work counter: same script + same rlimit ⇒ same outcome on any machine.
// The wall clock survives only as a non-outcome-determining safety cap; a
// kernel whose wall cap fires before rlimit exhausts is running on
// pathological hardware and MUST report the run as non-conformant.
const proveRlimit = 400_000_000 // 3.5x the heaviest successful proof (calibrated)
// proveDirectRlimit is the reduced budget for the DIRECT attempt on a goal
// that has a datatype-typed binder — i.e. one where structural induction is a
// possible strategy (SPEC §7.2, #50). Such a direct attempt is almost always
// futile (the goal needs induction) yet, at the full budget, burns minutes
// before failing. Every direct proof that SUCCEEDS in the corpus consumes
// under ~3K rlimit (measured); this budget clears that by >1000x while making
// a futile direct attempt fail at 1/100th of the full budget. A goal that
// proves ONLY by full-budget direct is caught by the fallback direct attempt
// at proveRlimit after induction, so the outcome is unchanged — this is a
// pure performance refinement, budget-part-of-identity notwithstanding.
const proveDirectRlimit = 4_000_000

// proveLemmaFreeRlimit is the budget for the LEMMA-FREE first attempt (SPEC
// §7.2, #53): the goal with its declarations and defining-equation axioms but
// NO lemma library. A budget-limited solver is non-monotone in its axiom set,
// so admitting a goal's legitimately-relevant lemmas can strangle a goal that
// discharges trivially without them (measured: q-peek.peek-is-head takes 2,294
// rlimit lemma-free and does not terminate within 400M with its 12 lemmas).
// Lemma-free successes are milliseconds, so a small budget catches them while
// failing fast for goals that genuinely need their lemma chain — those fall
// through to the unchanged strategies below, which is why the recorded outcome
// is the UNION and nothing can regress. Only `unsat` is accepted: proving from
// FEWER premises is strictly sound, while a lemma-free `sat` is left to the
// canonical attempt rather than reasoned about here.
const proveLemmaFreeRlimit = 4_000_000

// proveInstantiatedRlimit is the budget for the deterministic-instantiation
// attempt on one structural-induction constructor subgoal (see
// prove_instantiate.go). Measured: the composed-recursion subgoals that exhaust
// 15,000,000 rlimit in their quantified form return `unsat` at 1,847
// (swap.preserves) and 5,183 (opt.preserves) once the defining equations are
// ground. A goal whose shape the instance schema does not fit fails here at
// 1/100th of the full budget and falls through to the unchanged quantified
// attempt.
const proveInstantiatedRlimit = 4_000_000

// proveWallCapDefault is the wall-clock safety cap on a single z3 attempt. It is
// NOT part of any recorded verdict — a cap hit is an environmental abort, never
// an outcome (SPEC §7.2; execZ3 is explicitly host-specific) — so the value is a
// property of the HOST, not of identity: a slower machine needs a larger cap to
// let z3 finish consuming the SAME rlimit budget and return the SAME verdict a
// fast machine gets under the default. proveWallCap reads OATH_PROVE_WALLCAP_SEC
// so a slow deployment (e.g. the registry's Cloud Run worker) can raise it
// WITHOUT a code change; the default keeps local/CI/conformance byte-identical.
const proveWallCapDefault = 600 * time.Second

func proveWallCap() time.Duration {
	if v := os.Getenv("OATH_PROVE_WALLCAP_SEC"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return proveWallCapDefault
}

// searchWallDeadlineNano is a process-wide wall-clock deadline (UnixNano; 0 =
// unset) that CAPS each z3 attempt for the duration of a budgeted `find --implies`
// search, so a single slow proof cannot overrun the user's --timeout — the
// between-candidates check alone would let one in-flight proof run for minutes.
// It is set only by apiFindImpliesOpts (a single-goroutine CLI path) and read by
// attemptWallCap. UNSET, attemptWallCap == proveWallCap exactly, so the default,
// conformance, prove/verify and MCP paths are byte-identical. Atomic to match the
// concurrency the rest of this file already assumes around runZ3Budget.
var searchWallDeadlineNano atomic.Int64

func setSearchWallDeadline(t time.Time) { searchWallDeadlineNano.Store(t.UnixNano()) }
func clearSearchWallDeadline()          { searchWallDeadlineNano.Store(0) }

// searchDeadlinePassed reports whether an active search budget has elapsed. It
// lets the non-solver stages of a candidate (the concrete-probe sampling loop)
// stop early, so `--timeout` bounds the whole search and not only its z3 calls.
// Unset (the default), it is always false, so nothing outside a budgeted search
// changes behaviour.
func searchDeadlinePassed() bool {
	dl := searchWallDeadlineNano.Load()
	return dl != 0 && time.Now().UnixNano() > dl
}

// attemptWallCap is execZ3's effective per-attempt wall-clock cap: the host safety
// cap, shortened to whatever remains of an active search budget so an in-flight
// proof is bounded by the user's --timeout rather than by the 10-minute safety
// net. A cap hit is an ENVIRONMENTAL ABORT (NO VERDICT), never a verdict, and
// `find` records nothing to the store, so shrinking this cap never changes what
// PROVES or any stored identity — only whether a slow attempt is cut short.
func attemptWallCap() time.Duration {
	cap := proveWallCap()
	if dl := searchWallDeadlineNano.Load(); dl != 0 {
		if remaining := time.Until(time.Unix(0, dl)); remaining < cap {
			cap = remaining
		}
	}
	if cap <= 0 {
		cap = time.Millisecond // already past the deadline: fail the attempt fast, never disable the cap
	}
	return cap
}

var smtNameRe = regexp.MustCompile(`[^A-Za-z0-9]`)

func smtName(s string) string { return smtNameRe.ReplaceAllString(s, "_") }

type dtInfo struct {
	name    string
	ctors   []string
	sels    [][]string
	fields  [][]string        // field sorts
	recSel  map[string]string // records: field name → selector
	recSort map[string]string // records: field name → sort
}

type smtVal struct {
	expr     string
	sort     string
	fn       string
	argSorts []string
	ret      string
}

type lemma struct {
	ownIdx   int // index of the prop this lemma came from in the def under proof; -1 for dependency lemmas
	propIdx  int // index of the prop within defHash (its OWN def), for ALL lemmas — the hint-match key (#67)
	text     string
	defHash  string          // the definition the lemma belongs to
	mentions map[string]bool // every definition hash the lemma's binders/body reference
}

// hintedLemma reports whether property pi of the goal def names this lemma as an
// author-supplied admission (#67): a match on (defHash, propIdx). A hinted lemma
// bypasses the footprint relevance filter — sound because it is itself proven.
func hintedLemma(m *Meta, pi int, l *lemma) bool {
	for _, hr := range m.Hints[pi] {
		if hr.Def == l.defHash && hr.Prop == l.propIdx {
			return true
		}
	}
	return false
}

type smtCtx struct {
	st         *Store
	selfDef    *Def
	selfHash   string
	decls      []string
	axioms     []string           // defining equations of axiomatized recursive functions
	lemmas     []lemma            // proven properties usable as axioms (filtered per goal)
	dts        map[string]*dtInfo // by instance key
	dtBySort   map[string]*dtInfo
	fns        map[string]smtVal    // by instance key
	arrows     map[string][2]string // array sort → (domain, codomain)

	// fnEqns holds the substitution-ready defining equations of the functions
	// that HAVE a quantified axiom, so deterministic instantiation can emit
	// ground instances of them (prove_instantiate.go). Recorded on the totality
	// branch of ensureFn, which is what makes the soundness gate inherited
	// rather than restated.
	fnEqns     []fnEqn
	fnEqnByKey map[string]int
	quantified bool
	depth      int
	intDivDefs bool // truncating-division bridge emitted? (SPEC §7.1, #71)

	// metaOverride supplies metadata for objects the STORE does not hold.
	// Survivor adjudication (#130) proves against MUTANTS, which are cached
	// definitions with no metadata — and `GetMeta` failing there means the
	// defining-equation gate below sees no termination classification and
	// leaves the function UNINTERPRETED. That is not a weaker proof, it is a
	// corrupt one: an unconstrained function makes almost any property
	// "refutable". Supplied per-context rather than written into the store's
	// cache, so a candidate under interrogation can never be mistaken for a
	// store member by anything else running in this process.
	metaOverride map[string]*Meta

	// rlimitCap bounds EVERY solver attempt this context makes, clamping each
	// strategy's nominal budget (SPEC §7.2 leaves the runner's budget outside
	// the hashed script, so a clamp changes outcomes and never bytes). Zero
	// means no clamp, which is what every normative proof path uses.
	//
	// It exists for survivor adjudication (#146), which is a DIAGNOSTIC and not
	// a proof of record: sweeping the corpus at the full budget is an endurance
	// test, because a survivor that will never reach a verdict burns the entire
	// rlimit through every strategy before saying so.
	//
	// SOUND AT ANY CAP, and that is the whole licence for it: "refuted" requires
	// z3 to answer `sat` and "proven" requires `unsat`, while exhausting the
	// rlimit yields `unknown`, which is neither. So lowering the budget can only
	// turn a terminal verdict into "no verdict" — it can NEVER invert one, and
	// it can never manufacture a refutation. A capped sweep therefore reports
	// exact counts for what it did settle and a residue it must state.
	//
	// Threaded through the context rather than folded into effectiveRlimit()
	// deliberately: that function feeds prove-worker's proof-state fingerprint
	// (prove_worker.go), so capping there would make a diagnostic re-prove the
	// corpus.
	rlimitCap int64

	// ENUMERATION MODE (#139). When set, no solver runs: every script the
	// strategy sequence builds is RECORDED and answered `unsat`, and each
	// strategy's success return is bypassed, so the sequence walks to the end
	// and yields every script it could emit rather than stopping at the first
	// one that proves. This exists so `prove/attempts.txt` can pin the
	// induction, lexicographic and recursion-induction scripts — which §7.2
	// already makes normative (it names their constants `f<fi>`, `g<fi>`,
	// `h<fj>` and specifies the lexicographic subgoal structure) but which no
	// fixture witnessed. Only `prove/scripts.txt`'s direct attempt was pinned,
	// while `conformance.sh` in oracle mode claimed outcomes were determined
	// by pinned bytes — true only for goals that never reach induction.
	//
	// The recording seam is deliberately the SAME code path the prover runs, not
	// a parallel enumerator: a second script builder would compare the bytes its
	// author remembered to construct, and would drift from the emission it claims
	// to pin. `enumerate` is set by scriptAttempts alone and never by proveOne.
	enumerate bool
	attempts  []scriptAttempt

	// observer, when set, receives per-attempt telemetry. Diagnostic only; see
	// proveObserver below. nil on every normative path.
	observer proveObserver
}

// scriptAttempt is one script the strategy sequence can emit for a property.
// `detail` locates it — which binder, which constructor, which ordered pair —
// so a cross-kernel hash divergence names the subgoal that differs instead of
// only the property.
type scriptAttempt struct {
	strategy string
	detail   string
	text     string
}

// solve runs one script, or — under enumeration — records it and answers
// `unsat` so the sequence keeps building every remaining script.
//
// `unsat` is the correct enumeration answer at every site: within a strategy it
// is what continues to the next constructor or subgoal, and the strategy's
// success return is separately bypassed on c.enumerate, so the walk continues
// into the strategies that follow. Answering `unknown` would stop each strategy
// at its first constructor; answering `unsat` without bypassing the returns
// would stop the walk at the first strategy.
func (c *smtCtx) solve(strategy, detail, sc string, run func(string) (string, bool)) (string, bool) {
	if c.enumerate {
		c.attempts = append(c.attempts, scriptAttempt{strategy, detail, sc})
		return "unsat", false
	}
	if c.observer == nil {
		return run(sc)
	}
	// A HEURISTIC, AND SAID SO. lastZ3 is package-level, so telemetry read after
	// run() is this attempt's only if nothing else published meanwhile. The delta
	// catches the common interleavings and it is NOT a guarantee: z3Seq.Add and
	// lastZ3.Store are separate operations, so a concurrent prover can leave the
	// counter one higher than we started while the cell holds ANOTHER goal's
	// telemetry — delta 1, accepted, wrong goal. Tightening it would mean a lock
	// on the normative path, which a diagnostic may not buy.
	//
	// WHAT ACTUALLY MAKES THIS SOUND IS THE CALLER: the seam is valid only for a
	// SERIAL producer, and no normative path installs an observer, so nothing in
	// the kernel — scanBulkProve included — is ever observed. The delta is a
	// cheap alarm for the case where that precondition has been broken, not the
	// thing the correctness rests on. The producer asserts its own seriality.
	before := z3Seq.Load()
	out, capHit := run(sc)
	t, _ := lastZ3.Load().(z3Telemetry)
	c.observer.onAttempt(strategy, detail, sc, out, capHit, t, t.Seq-before)
	return out, capHit
}

// proveObserver watches the strategy sequence run. It is a DIAGNOSTIC seam: no
// proof outcome may depend on it, and nothing in the kernel installs one. It
// exists so a producer can record per-attempt telemetry through the same code
// path the prover actually runs, rather than through a parallel enumerator that
// would drift from it.
//
// `seqDelta` is 1 whenever no other publication was observed while the attempt
// ran. That is NECESSARY for the telemetry to be this attempt's and not
// sufficient (see solve); an observer must refuse anything else, and must not
// read a delta of 1 as proof that the seam was used serially.
type proveObserver interface {
	onAttempt(strategy, detail, script, out string, capHit bool, t z3Telemetry, seqDelta int64)
	// onDatatypeBinder reports the prover's own hasDT flag, and is called only
	// when the direct phase is reached — which is why an absent value is
	// meaningful and must not be defaulted to false.
	onDatatypeBinder(bool)
}

// scriptAttempts returns every script the strategy sequence can emit for
// property pi, in emission order, WITHOUT running a solver.
//
// The universe is the claim's, not the prover's: a script that a short-circuit
// happens to skip on this corpus still determines the outcome on a corpus where
// an earlier strategy fails, so enumeration reports what the sequence CAN emit
// rather than what one run did emit. Pinning a superset is sound; pinning only
// the executed subset would make the fixture depend on the very outcomes it
// exists to determine.
//
// SCOPED TO ONE LEMMA STATE, and the boundary is the point. §7.2 makes a script
// a pure function of (goal, RECORDED LEMMA STATE), and this walks the strategies
// under the state loadLemmaLibrary reads — the settled one. A cold fixpoint run
// attempts a goal under smaller intermediate sets as sibling properties become
// proven, and those scripts have different bytes. So this closes the STRATEGY
// dimension of the emitted set and leaves the LEMMA-STATE dimension open; the
// residue is named in SPEC §7.2 and in conformance.sh rather than left for a
// reader to discover, because the version of this comment that said "every
// script the sequence can emit" was wrong in exactly the way #139 exists to fix.
func scriptAttempts(st *Store, h string, pi int) ([]scriptAttempt, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return nil, err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return nil, err
	}
	if pi < 0 || pi >= len(d.Props) {
		return nil, fmt.Errorf("no property %d", pi)
	}
	c := newSmtCtx(st, d, h)
	c.enumerate = true
	loadLemmaLibrary(c, st, d, h, m, pi)
	c.proveOneInner(d, h, m, &d.Props[pi], pi)
	return c.attempts, nil
}

// propMentions collects every definition hash a property references
// (its binders and body), plus the hash of the definition it belongs to.
func propMentions(defHash string, p *Prop) map[string]bool {
	out := map[string]bool{defHash: true}
	shell := &Def{K: "func", TyVars: 0, Ty: tBool(), Body: &Term{K: "bool"},
		Props: []Prop{*p}}
	for h := range collectDeps(shell) {
		out[h] = true
	}
	return out
}

// goalFootprint is SPEC §7.2's relevance set: the definition under proof,
// every definition the property's body references, closed transitively
// through function bodies (their defining equations enter the SMT problem,
// so lemmas about them are usable; anything else is noise that slows the
// solver — the lemma library's scaling wall, #25).
func goalFootprint(st *Store, defHash string, d *Def, p *Prop) map[string]bool {
	fp := map[string]bool{}
	var add func(h string, def *Def)
	add = func(h string, def *Def) {
		if fp[h] {
			return
		}
		fp[h] = true
		for dh := range collectDepsBody(def) {
			dd, err := st.GetDef(dh)
			if err != nil {
				continue
			}
			add(dh, dd)
		}
	}
	add(defHash, d)
	for h := range propMentions(defHash, p) {
		if dd, err := st.GetDef(h); err == nil {
			add(h, dd)
		}
	}
	return fp
}

// collectDepsBody is collectDeps restricted to the definition's type, body
// and constructors — excluding its props, which are not part of the SMT
// problem for goals about OTHER definitions.
func collectDepsBody(d *Def) map[string]bool {
	shell := *d
	shell.Props = nil
	return collectDeps(&shell)
}

// lemmaAdmissible: same-definition sibling lemmas are admissible
// unconditionally — they are the §7.2 self-lemma fixpoint's foundation, and
// a sibling's proof chain may legitimately route through symbols the goal
// itself never mentions (sort.idempotent proves via sorted-is-fixpoint,
// which mentions is-sorted; the goal mentions only sort). The footprint
// test applies to DEPENDENCY lemmas, where the library's growth lives.
func lemmaAdmissible(l *lemma, goalDef string, fp map[string]bool) bool {
	if l.defHash == goalDef {
		return true
	}
	if l.mentions == nil {
		return true // pre-filter lemmas (shouldn't occur); fail open
	}
	for h := range l.mentions {
		if !fp[h] {
			return false
		}
	}
	return true
}

// ensureIntDivDefs emits the truncating-division bridge (SPEC §7.1, #71) once,
// on first use. The kernel's `/` truncates toward zero and `%` takes the
// dividend's sign (Go big.Int Quo/Rem); SMT-LIB's div/mod are Euclidean, so
// emitting them directly would prove a DIFFERENT theorem for negative dividends.
// These definitions are exactly the kernel's semantics wherever b ≠ 0 — z3
// itself verifies there is no (a,b) with b≠0 where they violate the defining
// characterisation (a = b·q + r, |r| < |b|, r signed like a or zero).
//
// Division by zero stays UNCONSTRAINED, inheriting SMT-LIB's unspecified
// div/mod there. That is deliberate and is the sound direction: in the kernel
// these operations ERROR on a zero divisor, so there is no value to model, and
// leaving it arbitrary means a property that depends on it simply fails to
// prove. Pinning it (say to 0) would let the prover establish properties the
// evaluator cannot even run.
//
// Emitted lazily so that goals touching neither operator produce byte-identical
// scripts to those from a kernel predating this bridge.
func (c *smtCtx) ensureIntDivDefs() {
	if c.intDivDefs {
		return
	}
	c.intDivDefs = true
	c.decls = append(c.decls,
		"(define-fun oath_tquo ((a Int) (b Int)) Int\n"+
			"  (ite (>= a 0) (div a b)\n"+
			"    (ite (= (mod a b) 0) (div a b)\n"+
			"      (+ (div a b) (ite (> b 0) 1 (- 1))))))",
		"(define-fun oath_trem ((a Int) (b Int)) Int\n"+
			"  (ite (>= a 0) (mod a b)\n"+
			"    (ite (= (mod a b) 0) 0\n"+
			"      (- (mod a b) (ite (> b 0) b (- b))))))")
}

func newSmtCtx(st *Store, d *Def, h string) *smtCtx {
	return &smtCtx{st: st, selfDef: d, selfHash: h,
		dts: map[string]*dtInfo{}, dtBySort: map[string]*dtInfo{}, fns: map[string]smtVal{},
		fnEqnByKey: map[string]int{}}
}

// metaOf is the ONLY way this file should read metadata: every consumer must see
// an overridden object identically to a stored one, or the override is a second
// source of truth that disagrees with the first. Returns nil when nothing knows
// the object — the caller's existing nil handling then applies unchanged.
func (c *smtCtx) metaOf(h string) *Meta {
	if m, ok := c.metaOverride[h]; ok {
		return m
	}
	m, _ := c.st.GetMeta(h)
	return m
}

func tyKey(h string, args []Ty) string {
	parts := []string{h}
	for i := range args {
		parts = append(parts, debugTy(&args[i]))
	}
	return strings.Join(parts, "|")
}

func (c *smtCtx) sortOf(t *Ty) (string, error) {
	switch t.K {
	case "int":
		return "Int", nil
	case "rat":
		return "Real", nil
	case "float":
		// IEEE-754 binary64 → Z3's FloatingPoint theory (FPA is decidable).
		return "Float64", nil
	case "bool":
		return "Bool", nil
	case "fun":
		// Function values are SMT arrays, applied via select. This makes
		// them first-class: quantifiable in induction hypotheses and legal
		// as datatype fields (capability records).
		dom, err := c.sortOf(t.A)
		if err != nil {
			return "", err
		}
		cod, err := c.sortOf(t.B)
		if err != nil {
			return "", err
		}
		s := fmt.Sprintf("(Array %s %s)", dom, cod)
		if c.arrows == nil {
			c.arrows = map[string][2]string{}
		}
		c.arrows[s] = [2]string{dom, cod}
		return s, nil
	case "data":
		dt, err := c.ensureDT(t.Hash, t.Args)
		if err != nil {
			return "", err
		}
		return dt.name, nil
	case "record":
		dt, err := c.ensureRecordDT(t)
		if err != nil {
			return "", err
		}
		return dt.name, nil
	}
	return "", fmt.Errorf("type %s is outside the provable fragment", debugTy(t))
}

// ensureRecordDT declares a structural record as a single-constructor
// datatype. Records with function-typed fields (capabilities) stay outside
// the fragment — SMT datatype fields must be first-order.
func (c *smtCtx) ensureRecordDT(t *Ty) (*dtInfo, error) {
	var sorts []string
	for i := range t.Args {
		s, err := c.sortOf(&t.Args[i])
		if err != nil {
			return nil, err
		}
		sorts = append(sorts, s)
	}
	name := "Rec"
	for i, n := range t.Names {
		name += "_" + smtName(n) + "_" + smtName(sorts[i])
	}
	if dt, ok := c.dtBySort[name]; ok {
		return dt, nil
	}
	mk := "mk_" + name
	dt := &dtInfo{name: name, ctors: []string{mk}, recSel: map[string]string{}, recSort: map[string]string{}}
	var parts, sels []string
	for i, n := range t.Names {
		sel := mk + "_" + smtName(n)
		dt.recSel[n] = sel
		dt.recSort[n] = sorts[i]
		sels = append(sels, sel)
		parts = append(parts, fmt.Sprintf("(%s %s)", sel, sorts[i]))
	}
	dt.sels = [][]string{sels}
	dt.fields = [][]string{sorts}
	c.dts[name] = dt
	c.dtBySort[name] = dt
	c.decls = append(c.decls, fmt.Sprintf("(declare-datatypes ((%s 0)) (((%s %s))))", name, mk, strings.Join(parts, " ")))
	return dt, nil
}

// ensureDT declares the monomorphic datatype instance for (hash, args).
func (c *smtCtx) ensureDT(h string, args []Ty) (*dtInfo, error) {
	key := tyKey(h, args)
	if dt, ok := c.dts[key]; ok {
		return dt, nil
	}
	d, err := c.st.GetDef(h)
	if err != nil {
		return nil, err
	}
	m, err := c.st.GetMeta(h)
	if err != nil {
		return nil, err
	}
	name := smtName(m.Name)
	for i := range args {
		s, err := c.sortOf(&args[i])
		if err != nil {
			return nil, err
		}
		name += "_" + smtName(s)
	}
	dt := &dtInfo{name: name}
	c.dts[key] = dt // pre-register: recursive fields resolve to this name
	c.dtBySort[name] = dt
	var ctorDecls []string
	for ci := range d.Ctors {
		cn := smtName(m.CtorNames[ci]) + "_" + name
		dt.ctors = append(dt.ctors, cn)
		fields := instCtorFields(d, h, args, ci)
		var sels, sorts, parts []string
		for fi, f := range fields {
			s, err := c.sortOf(f)
			if err != nil {
				return nil, err
			}
			sel := fmt.Sprintf("%s_%d", cn, fi)
			sels = append(sels, sel)
			sorts = append(sorts, s)
			parts = append(parts, fmt.Sprintf("(%s %s)", sel, s))
		}
		dt.sels = append(dt.sels, sels)
		dt.fields = append(dt.fields, sorts)
		ctorDecls = append(ctorDecls, fmt.Sprintf("(%s %s)", cn, strings.Join(parts, " ")))
	}
	c.decls = append(c.decls, fmt.Sprintf("(declare-datatypes ((%s 0)) ((%s)))", name, strings.Join(ctorDecls, " ")))
	return dt, nil
}

// ensureFn declares a recursive function instance and asserts its defining
// equation as a quantified axiom.
func (c *smtCtx) ensureFn(h string, d *Def, args []Ty) (smtVal, error) {
	key := tyKey(h, args)
	if v, ok := c.fns[key]; ok {
		return v, nil
	}
	name := "fn_" + smtName(c.st.NameOf(h))
	ty := substTy(d.Ty, args)
	var argSorts []string
	cur := ty
	for cur.K == "fun" {
		s, err := c.sortOf(cur.A)
		if err != nil {
			return smtVal{}, err
		}
		argSorts = append(argSorts, s)
		cur = cur.B
	}
	ret, err := c.sortOf(cur)
	if err != nil {
		return smtVal{}, err
	}
	for i := range args {
		s, _ := c.sortOf(&args[i])
		name += "_" + smtName(s)
	}
	v := smtVal{fn: name, argSorts: argSorts, ret: ret}
	c.fns[key] = v // pre-register: recursive self-calls resolve here
	c.decls = append(c.decls, fmt.Sprintf("(declare-fun %s (%s) %s)", name, strings.Join(argSorts, " "), ret))

	body := termSubstTy(d.Body, args)
	var env []smtVal
	var params []string
	for i := 0; body.K == "lam"; i++ {
		p := fmt.Sprintf("p%d", i)
		s, err := c.sortOf(body.Ty)
		if err != nil {
			return smtVal{}, err
		}
		params = append(params, fmt.Sprintf("(%s %s)", p, s))
		env = append(env, smtVal{expr: p, sort: s})
		body = body.A
	}
	// Translate the body in the callee's own frame: self is (h, d).
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = d, h
	bexpr, _, err := c.tr(body, env)
	c.selfDef, c.selfHash = saveDef, saveHash
	if err != nil {
		return smtVal{}, err
	}
	// Assert the defining equation ONLY for functions proven total. For a
	// function whose termination is unproven, `f(x) = body` can be an
	// inconsistent constraint (e.g. f x = f x + 1 ⟹ ∀x. f(x)=f(x)+1, UNSAT),
	// and an inconsistent axiom lets Z3 discharge ANY goal by ex falso —
	// "proving" false properties, and poisoning every dependent through the
	// lemma library. The soundness of the top rung depends on this gate.
	// A non-total callee is left uninterpreted (declared, no equation): sound,
	// merely weaker (proofs that needed its definition come back `unknown`).
	tm := c.metaOf(h)
	if tm != nil && isTotal(tm.Termination) {
		// Deterministic instantiation (prove_instantiate.go) may emit GROUND
		// instances of this equation. Its soundness gate is exactly the totality
		// test guarding this branch — a ground instance of `f(x) = body` is a
		// theorem only where f is defined — so the metadata is recorded HERE and
		// nowhere else: a function that gets no quantified axiom gets no
		// substitution-ready data either, and cannot be instantiated at all.
		c.fnEqnByKey[key] = len(c.fnEqns)
		c.fnEqns = append(c.fnEqns, fnEqn{
			hash: h, def: d, name: name, argSorts: argSorts, ret: ret,
			bodyTerm: body, axiomIdx: len(c.axioms),
		})
		app := name
		if len(env) > 0 {
			var syms []string
			for _, e := range env {
				syms = append(syms, e.expr)
			}
			app = fmt.Sprintf("(%s %s)", name, strings.Join(syms, " "))
			if tm.Termination == "measure" {
				// Integer-counter recursion (#56): a `:pattern` would E-match
				// without bound — rep(n) instantiates rep(n-1) instantiates
				// rep(n-2)…, with no datatype acyclicity to stop the descent, so
				// any goal mentioning the function diverges. Emit the axiom with NO
				// pattern; Z3 falls back to model-based instantiation, which
				// terminates and still discharges both the direct laws and the
				// integer-induction obligations below.
				c.axioms = append(c.axioms, fmt.Sprintf("(assert (forall (%s) (= %s %s)))",
					strings.Join(params, " "), app, bexpr))
			} else {
				c.axioms = append(c.axioms, fmt.Sprintf("(assert (forall (%s) (! (= %s %s) :pattern (%s))))",
					strings.Join(params, " "), app, bexpr, app))
			}
			c.quantified = true
		} else {
			c.axioms = append(c.axioms, fmt.Sprintf("(assert (= %s %s))", app, bexpr))
		}
	}
	return v, nil
}

var smtPrimOps = map[string]string{
	"+": "+", "-": "-", "*": "*", "neg": "-", "/": "/",
	"<": "<", "<=": "<=", "and": "and", "or": "or", "not": "not",
	"==": "=",
}

var smtPrimSorts = map[string]string{
	"+": "Int", "-": "Int", "*": "Int", "neg": "Int", "/": "Int",
	"<": "Bool", "<=": "Bool", "and": "Bool", "or": "Bool", "not": "Bool",
	"==": "Bool",
}

// arithPrims preserve their operand's numeric sort (Int or Real) as the result.
var arithPrims = map[string]bool{"+": true, "-": true, "*": true, "/": true, "neg": true}

// floatLit renders a float64 as an SMT-LIB (fp sign exp mantissa) literal from
// its exact IEEE-754 bits (NaN canonicalized). One value → one literal, no
// rounding — Float64 is (_ FloatingPoint 11 53).
func floatLit(f float64) string {
	bits := math.Float64bits(canonFloat(f))
	sign := bits >> 63
	exp := (bits >> 52) & 0x7FF
	mant := bits & 0xFFFFFFFFFFFFF
	return fmt.Sprintf("(fp (_ bv%d 1) (_ bv%d 11) (_ bv%d 52))", sign, exp, mant)
}

// floatPrim translates a primitive over Float operands into Z3 FPA. Arithmetic
// rounds nearest-ties-even (RNE); `<`/`<=` are IEEE-ordered (fp.lt/fp.leq);
// `==` is SMT `=` (structural/Leibniz, the kernel's identity); `fp-eq` is the
// IEEE equality fp.eq.
func floatPrim(op string, parts []string) (string, string, error) {
	bin := func(f string) string { return fmt.Sprintf("(%s %s %s)", f, parts[0], parts[1]) }
	rnd := func(f string) string { return fmt.Sprintf("(%s RNE %s %s)", f, parts[0], parts[1]) }
	switch op {
	case "+":
		return rnd("fp.add"), "Float64", nil
	case "-":
		return rnd("fp.sub"), "Float64", nil
	case "*":
		return rnd("fp.mul"), "Float64", nil
	case "/":
		return rnd("fp.div"), "Float64", nil
	case "neg":
		return fmt.Sprintf("(fp.neg %s)", parts[0]), "Float64", nil
	case "<":
		return bin("fp.lt"), "Bool", nil
	case "<=":
		return bin("fp.leq"), "Bool", nil
	case "==":
		return bin("="), "Bool", nil
	case "fp-eq":
		return bin("fp.eq"), "Bool", nil
	}
	return "", "", fmt.Errorf("primitive %s is outside the provable fragment for Float", op)
}

func (c *smtCtx) tr(t *Term, env []smtVal) (string, string, error) {
	c.depth++
	defer func() { c.depth-- }()
	if c.depth > 512 {
		return "", "", fmt.Errorf("inlining too deep")
	}
	switch t.K {
	case "var":
		v := env[len(env)-1-t.Idx]
		return v.expr, v.sort, nil
	case "int":
		if t.Int.Sign() < 0 {
			return fmt.Sprintf("(- %s)", new(big.Int).Neg(t.Int).String()), "Int", nil
		}
		return t.Int.String(), "Int", nil
	case "rat":
		// A rational renders as (/ num den) over Real — Z3's rational theory is
		// exact, so 0.1 + 0.2 proves == 3/10 with no rounding. num carries the
		// sign; den > 0.
		num, den := t.Rat.Num(), t.Rat.Denom()
		numStr := num.String()
		if num.Sign() < 0 {
			numStr = fmt.Sprintf("(- %s)", new(big.Int).Neg(num).String())
		}
		return fmt.Sprintf("(/ %s %s)", numStr, den.String()), "Real", nil
	case "float":
		// An IEEE Float renders as an (fp sign exp mantissa) literal from its
		// exact (canonicalized) bits — no rounding, one representation per value.
		return floatLit(t.Float), "Float64", nil
	case "bool":
		return fmt.Sprintf("%v", t.Bool), "Bool", nil
	case "if":
		cnd, _, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		th, s1, err := c.tr(t.B, env)
		if err != nil {
			return "", "", err
		}
		el, _, err := c.tr(t.C, env)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("(ite %s %s %s)", cnd, th, el), s1, nil
	case "let":
		bound, s, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		return c.tr(t.B, append(append([]smtVal{}, env...), smtVal{expr: bound, sort: s}))
	case "prim":
		var parts []string
		argSort := ""
		for i := range t.Args {
			a, s, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", "", err
			}
			if i == 0 {
				argSort = s
			}
			parts = append(parts, a)
		}
		// Float operands translate to Z3's FloatingPoint theory (FPA): arithmetic
		// carries the round-nearest-even mode, comparisons are IEEE-ordered, and
		// structural == is SMT `=` (Leibniz — NaN=NaN, +0≠−0), matching the
		// kernel's identity model. `fp-eq` is the separate IEEE fp.eq.
		if argSort == "Float64" {
			// Includes the PARTIAL narrowings to-rat/floor from Float, which
			// floatPrim rejects — non-finite floats have no rational/integer, so
			// they stay outside the provable fragment (like Int division).
			return floatPrim(t.Op, parts)
		}
		// Numeric conversions with a total, exact SMT image: Int→Rat (to_real),
		// Int/Rat→Float (to_fp at RNE), Rat→Int (to_int = floor). The Float-source
		// conversions are partial and handled by floatPrim above.
		switch t.Op {
		case "to-rat":
			return fmt.Sprintf("(to_real %s)", parts[0]), "Real", nil
		case "to-float":
			src := parts[0]
			if argSort == "Int" {
				src = fmt.Sprintf("(to_real %s)", src)
			}
			return fmt.Sprintf("((_ to_fp 11 53) RNE %s)", src), "Float64", nil
		case "floor":
			return fmt.Sprintf("(to_int %s)", parts[0]), "Int", nil
		}
		// Int `/` and `%` are DEFINED in terms of SMT-LIB's Euclidean div/mod
		// rather than emitted as them (SPEC §7.1, #71): the kernel truncates
		// toward zero and its remainder takes the dividend's sign, which differs
		// from Euclidean whenever the dividend is negative. Over Real, `/` is
		// exact and needs no bridge.
		if (t.Op == "/" || t.Op == "%") && argSort == "Int" {
			c.ensureIntDivDefs()
			fn := "oath_tquo"
			if t.Op == "%" {
				fn = "oath_trem"
			}
			return fmt.Sprintf("(%s %s %s)", fn, parts[0], parts[1]), "Int", nil
		}
		// #78: the crypto primitives are the artifact's TRUSTED boundary and are
		// deliberately outside the provable fragment. Modelling SHA-256 in SMT is
		// not useful even where it is possible, and an axiomatization we invented
		// would be a proof about our axioms rather than about the algorithm. So a
		// property whose goal reaches the digest stays TESTED — which is the
		// honest guarantee level for a trusted primitive. What the prover DOES
		// verify is the handler logic around it (§7, the no-script rule applies).
		if t.Op == "hmac-sha256" || t.Op == "bytes-eq-ct" {
			return "", "", fmt.Errorf("%s is outside the provable fragment (trusted crypto primitive)", t.Op)
		}
		if t.Op == "%" || (t.Op == "/" && argSort != "Real") {
			return "", "", fmt.Errorf("%s is untranslatable over %s", t.Op, argSort)
		}
		op, ok := smtPrimOps[t.Op]
		if !ok {
			return "", "", fmt.Errorf("primitive %s is outside the provable fragment", t.Op)
		}
		// Arithmetic preserves the operand's numeric sort (Int or Real).
		sort := smtPrimSorts[t.Op]
		if arithPrims[t.Op] {
			sort = argSort
		}
		return "(" + op + " " + strings.Join(parts, " ") + ")", sort, nil
	case "ctor":
		dt, err := c.ensureDT(t.Hash, t.TyArgs)
		if err != nil {
			return "", "", err
		}
		if len(t.Args) == 0 {
			return dt.ctors[t.Idx], dt.name, nil
		}
		var parts []string
		for i := range t.Args {
			a, _, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", "", err
			}
			parts = append(parts, a)
		}
		return fmt.Sprintf("(%s %s)", dt.ctors[t.Idx], strings.Join(parts, " ")), dt.name, nil
	case "record":
		// Reconstruct the record's type from field sorts to locate its
		// datatype; fields are canonically sorted so names align.
		var sorts []string
		var exprs []string
		for i := range t.Args {
			e, s, err := c.tr(&t.Args[i], env)
			if err != nil {
				return "", "", err
			}
			exprs = append(exprs, e)
			sorts = append(sorts, s)
		}
		name := "Rec"
		for i, n := range t.Names {
			name += "_" + smtName(n) + "_" + smtName(sorts[i])
		}
		dt, ok := c.dtBySort[name]
		if !ok {
			// Declare on first sight, mirroring ensureRecordDT.
			mk := "mk_" + name
			dt = &dtInfo{name: name, ctors: []string{mk}, recSel: map[string]string{}, recSort: map[string]string{}}
			var parts []string
			for i, n := range t.Names {
				sel := mk + "_" + smtName(n)
				dt.recSel[n] = sel
				dt.recSort[n] = sorts[i]
				parts = append(parts, fmt.Sprintf("(%s %s)", sel, sorts[i]))
			}
			c.dts[name] = dt
			c.dtBySort[name] = dt
			c.decls = append(c.decls, fmt.Sprintf("(declare-datatypes ((%s 0)) (((%s %s))))", name, mk, strings.Join(parts, " ")))
		}
		return fmt.Sprintf("(%s %s)", dt.ctors[0], strings.Join(exprs, " ")), name, nil
	case "field":
		re, rs, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		dt, ok := c.dtBySort[rs]
		if !ok || dt.recSel == nil {
			return "", "", fmt.Errorf("field access on non-record sort %s", rs)
		}
		sel, ok := dt.recSel[t.Op]
		if !ok {
			return "", "", fmt.Errorf("record sort %s has no field %q", rs, t.Op)
		}
		return fmt.Sprintf("(%s %s)", sel, re), dt.recSort[t.Op], nil
	case "match":
		s, ssort, err := c.tr(t.A, env)
		if err != nil {
			return "", "", err
		}
		dt, ok := c.dtBySort[ssort]
		if !ok {
			return "", "", fmt.Errorf("match on non-datatype sort %s", ssort)
		}
		var armExprs []string
		var resSort string
		for i := range t.Arms {
			env2 := append([]smtVal{}, env...)
			for fi, sel := range dt.sels[i] {
				env2 = append(env2, smtVal{expr: fmt.Sprintf("(%s %s)", sel, s), sort: dt.fields[i][fi]})
			}
			a, as, err := c.tr(&t.Arms[i], env2)
			if err != nil {
				return "", "", err
			}
			armExprs = append(armExprs, a)
			resSort = as
		}
		out := armExprs[len(armExprs)-1]
		for i := len(armExprs) - 2; i >= 0; i-- {
			out = fmt.Sprintf("(ite ((_ is %s) %s) %s %s)", dt.ctors[i], s, armExprs[i], out)
		}
		return out, resSort, nil
	case "app":
		head, args := unwindApp(t)
		switch head.K {
		case "var":
			// Array-encoded function value: apply via nested select.
			v := env[len(env)-1-head.Idx]
			expr, sort := v.expr, v.sort
			for _, a := range args {
				arrow, ok := c.arrows[sort]
				if !ok {
					return "", "", fmt.Errorf("application of a non-function value (sort %s)", sort)
				}
				s, _, err := c.tr(a, env)
				if err != nil {
					return "", "", err
				}
				expr = fmt.Sprintf("(select %s %s)", expr, s)
				sort = arrow[1]
			}
			return expr, sort, nil
		case "field":
			// ((. cap fetch) x): project then select.
			fe, fs, err := c.tr(head, env)
			if err != nil {
				return "", "", err
			}
			expr, sort := fe, fs
			for _, a := range args {
				arrow, ok := c.arrows[sort]
				if !ok {
					return "", "", fmt.Errorf("application of a non-function field (sort %s)", sort)
				}
				s, _, err := c.tr(a, env)
				if err != nil {
					return "", "", err
				}
				expr = fmt.Sprintf("(select %s %s)", expr, s)
				sort = arrow[1]
			}
			return expr, sort, nil
		case "ref":
			d, err := c.st.GetDef(head.Hash)
			if err != nil {
				return "", "", err
			}
			return c.call(head.Hash, d, head.TyArgs, args, env)
		case "self":
			return c.call(c.selfHash, c.selfDef, head.TyArgs, args, env)
		case "lam":
			cur := head
			env2 := append([]smtVal{}, env...)
			consumed := 0
			for cur.K == "lam" && consumed < len(args) {
				s, srt, err := c.tr(args[consumed], env)
				if err != nil {
					return "", "", err
				}
				env2 = append(env2, smtVal{expr: s, sort: srt})
				cur = cur.A
				consumed++
			}
			if consumed != len(args) || cur.K == "lam" {
				return "", "", fmt.Errorf("partial application is outside the provable fragment")
			}
			return c.tr(cur, env2)
		}
		return "", "", fmt.Errorf("application head %q is outside the provable fragment", head.K)
	case "ref":
		d, err := c.st.GetDef(t.Hash)
		if err != nil {
			return "", "", err
		}
		return c.call(t.Hash, d, t.TyArgs, nil, env)
	case "self":
		return c.call(c.selfHash, c.selfDef, t.TyArgs, nil, env)
	}
	return "", "", fmt.Errorf("%q terms are outside the provable fragment", t.K)
}

// call translates a fully-applied reference: recursive callees become
// axiomatized uninterpreted functions, non-recursive callees are inlined.
func (c *smtCtx) call(h string, d *Def, tyargs []Ty, args []*Term, env []smtVal) (string, string, error) {
	if d.K != "func" {
		return "", "", fmt.Errorf("data reference is outside the provable fragment")
	}
	if hasSelfRef(d.Body) {
		v, err := c.ensureFn(h, d, tyargs)
		if err != nil {
			return "", "", err
		}
		if len(args) != len(v.argSorts) {
			return "", "", fmt.Errorf("%s must be fully applied", c.st.NameOf(h))
		}
		if len(args) == 0 {
			return v.fn, v.ret, nil
		}
		var parts []string
		for _, a := range args {
			s, _, err := c.tr(a, env)
			if err != nil {
				return "", "", err
			}
			parts = append(parts, s)
		}
		return fmt.Sprintf("(%s %s)", v.fn, strings.Join(parts, " ")), v.ret, nil
	}
	// Non-recursive: inline.
	body := termSubstTy(d.Body, tyargs)
	var callee []smtVal
	for i := 0; body.K == "lam"; i++ {
		if i >= len(args) {
			return "", "", fmt.Errorf("%s must be fully applied to inline", c.st.NameOf(h))
		}
		s, srt, err := c.tr(args[i], env)
		if err != nil {
			return "", "", err
		}
		callee = append(callee, smtVal{expr: s, sort: srt})
		body = body.A
	}
	if len(callee) != len(args) {
		return "", "", fmt.Errorf("%s is over-applied; cannot inline", c.st.NameOf(h))
	}
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = d, h
	e, s, err := c.tr(body, callee)
	c.selfDef, c.selfHash = saveDef, saveHash
	return e, s, err
}

// termSubstTy instantiates every embedded type in a term.
func termSubstTy(t *Term, args []Ty) *Term {
	if t == nil || len(args) == 0 {
		return t
	}
	out := *t
	if t.Ty != nil {
		out.Ty = substTy(t.Ty, args)
	}
	if len(t.TyArgs) > 0 {
		tys := make([]Ty, len(t.TyArgs))
		for i := range t.TyArgs {
			tys[i] = *substTy(&t.TyArgs[i], args)
		}
		out.TyArgs = tys
	}
	out.A = termSubstTy(t.A, args)
	out.B = termSubstTy(t.B, args)
	out.C = termSubstTy(t.C, args)
	if len(t.Args) > 0 {
		as := make([]Term, len(t.Args))
		for i := range t.Args {
			as[i] = *termSubstTy(&t.Args[i], args)
		}
		out.Args = as
	}
	if len(t.Arms) > 0 {
		as := make([]Term, len(t.Arms))
		for i := range t.Arms {
			as[i] = *termSubstTy(&t.Arms[i], args)
		}
		out.Arms = as
	}
	return &out
}

func hasSelfRef(t *Term) bool {
	if t == nil {
		return false
	}
	if t.K == "self" {
		return true
	}
	if hasSelfRef(t.A) || hasSelfRef(t.B) || hasSelfRef(t.C) {
		return true
	}
	for i := range t.Args {
		if hasSelfRef(&t.Args[i]) {
			return true
		}
	}
	for i := range t.Arms {
		if hasSelfRef(&t.Arms[i]) {
			return true
		}
	}
	return false
}

// formulaWith renders a property as a formula. assign maps binder index →
// SMT expression; unassigned binders become universally quantified. Function
// binders cannot be quantified in FOL, so they fail unless assigned.
func (c *smtCtx) formulaWith(d *Def, h string, p *Prop, assign map[int]string) (string, error) {
	var env []smtVal
	var foralls []string
	for i := range p.Binders {
		b := &p.Binders[i]
		if e, ok := assign[i]; ok {
			s, err := c.sortOf(b)
			if err != nil {
				return "", err
			}
			env = append(env, smtVal{expr: e, sort: s})
			continue
		}
		s, err := c.sortOf(b)
		if err != nil {
			return "", fmt.Errorf("cannot quantify binder of type %s", debugTy(b))
		}
		q := fmt.Sprintf("q%d", i)
		foralls = append(foralls, fmt.Sprintf("(%s %s)", q, s))
		env = append(env, smtVal{expr: q, sort: s})
	}
	saveDef, saveHash := c.selfDef, c.selfHash
	c.selfDef, c.selfHash = d, h
	body, _, err := c.tr(&p.Body, env)
	c.selfDef, c.selfHash = saveDef, saveHash
	if err != nil {
		return "", err
	}
	if len(foralls) > 0 {
		c.quantified = true
		return fmt.Sprintf("(forall (%s) %s)", strings.Join(foralls, " "), body), nil
	}
	return body, nil
}

// calibLastConsumed records the rlimit the last runZ3 call spent. It backs the
// OATH_PROVE_SPLIT per-phase diagnostic (a durable debugging hook alongside
// OATH_PROVE_CALIBRATE); nothing in the proof outcome depends on it.
// atomic because parallel scanBulkProve runs runZ3Budget from many goroutines;
// this is a diagnostic side-channel (OATH_PROVE_SPLIT), never a proof input, so
// a racy value would be harmless — but keep it race-clean.
var calibLastConsumed atomic.Int64

// THERE IS DELIBERATELY NO UNCAPPED `runZ3` HELPER. Every solver attempt in the
// strategy chain goes through a context, so removing the context-free runner is
// what makes the cap below unbypassable BY CONSTRUCTION rather than by everyone
// remembering to thread it. `c.runFull` is the full-budget runner; it is the
// same thing with the clamp applied, and the clamp is inert (zero cap) on every
// normative path.

// budget clamps a strategy's nominal per-attempt budget to this context's cap.
//
// EVERY solver attempt a context makes routes through here, which is what makes
// the cap a property of the context rather than of the strategies that remember
// to honour it. A strategy added later inherits the clamp by construction; one
// that called runZ3Budget directly would not, and TestEnumerationRunsNoSolver
// already fails any such call.
func (c *smtCtx) budget(nominal int64) int64 {
	if c.rlimitCap > 0 && c.rlimitCap < nominal {
		return c.rlimitCap
	}
	return nominal
}

// runFull is the default runner: the full per-goal budget, clamped by the cap.
func (c *smtCtx) runFull(script string) (string, bool) {
	return runZ3Budget(script, c.budget(effectiveRlimit()))
}

// effectiveRlimit is the full per-goal budget: the normative proveRlimit, or the
// OATH_PROVE_RLIMIT testing override when set.
func effectiveRlimit() int64 {
	rl := int64(proveRlimit)
	if v := os.Getenv("OATH_PROVE_RLIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			rl = n
		}
	}
	return rl
}

// lemmaFreeRlimit is the budget for the lemma-free first attempt, clamped to
// the full budget for the same reason as directRlimit: an optional extra
// attempt must never be STRONGER than the main search it precedes.
func lemmaFreeRlimit() int64 {
	full := effectiveRlimit()
	if proveLemmaFreeRlimit < full {
		return proveLemmaFreeRlimit
	}
	return full
}

// instantiatedRlimit is the budget for the deterministic-instantiation preview
// of a structural-induction subgoal. It is clamped to the full budget for the
// same reason as directRlimit and lemmaFreeRlimit: an optional extra attempt
// must never be STRONGER than the search it precedes, or lowering the full
// budget under test would leave the preview outrunning the fallback that is
// supposed to subsume it. The measured instantiated subgoals discharge at 1,847
// and 5,183 rlimit, so this clears them by ~800x while failing fast on a goal
// the schema does not fit.
func instantiatedRlimit() int64 {
	full := effectiveRlimit()
	if proveInstantiatedRlimit < full {
		return proveInstantiatedRlimit
	}
	return full
}

// instantiationEnabled reports whether the deterministic-instantiation preview
// is active. It is OFF by default in Phase 1 (see the gate at its call site) and
// enabled by setting OATH_PROVE_INSTANTIATE to any non-empty value. Phase 2 makes
// it the default alongside the SPEC text and fixture regeneration.
func instantiationEnabled() bool {
	return os.Getenv("OATH_PROVE_INSTANTIATE") != ""
}

// directRlimit is the budget for the direct attempt on an inductive-eligible
// goal. It is clamped to the full budget so the reduced attempt can never be
// STRONGER than the full-budget fallback — otherwise lowering the full budget
// (the testing override) would leave the direct attempt running longer than the
// fallback that is supposed to subsume it, and the two kernels would diverge.
func directRlimit() int64 {
	full := effectiveRlimit()
	if proveDirectRlimit < full {
		return proveDirectRlimit
	}
	return full
}

// runZ3Budget runs a goal at an explicit rlimit. The budget is a runner option
// prepended OUTSIDE the hashed core script (SPEC §7.2), so the same goal at any
// budget emits byte-identical script bytes — only the outcome may differ. The
// direct attempt on a datatype-binder goal uses the reduced proveDirectRlimit
// here (see proveOne); everything else uses proveRlimit.
func runZ3Budget(script string, rl int64) (string, bool) {
	// Runner options are prepended outside the hashed core script (SPEC
	// §7.2) and the trailing get-info lines are the attempt-validity
	// telemetry, likewise outside the hashed bytes. An OPT-IN memory bound
	// (OATH_PROVE_MEMORY_MB) converts an OS-level death into a clean
	// memout abort — but it cannot be a default: z3 counts its upfront
	// arena RESERVATIONS (~21GB virtual on quantified goals) against
	// memory_max_size, so any bound below the reservation instantly kills
	// attempts that would have run fine, and any bound above it bounds
	// nothing. Environments that prefer memout-invalidation over OS death
	// set it explicitly; the validity rule below catches both.
	header := fmt.Sprintf("(set-option :rlimit %d)\n", rl)
	if v := os.Getenv("OATH_PROVE_MEMORY_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			header = fmt.Sprintf("(set-option :memory_max_size %d)\n", n) + header
		}
	}
	full := header + script + "\n(get-info :rlimit)\n(get-info :reason-unknown)\n"
	// execZ3 is the host-specific run step (SPEC §7.2 is agnostic to it): a z3
	// subprocess natively, the browser z3-solver bridge under js/wasm. capHit
	// means the wall-clock safety cap fired — an INVALID attempt, never an
	// outcome (the reference once recorded cap hits as unknown; the blind Rust
	// kernel caught it, #17 epilogue).
	res, capHit := execZ3(full)
	if capHit {
		publishZ3Telemetry(rl, -1, "", "", true)
		return "", true
	}
	consumed, haveConsumed := int64(-1), false
	if i := strings.Index(res, "(:rlimit "); i >= 0 {
		s := strings.TrimRight(strings.TrimSpace(res[i+9:]), ")\n `")
		if j := strings.IndexAny(s, ")\n"); j >= 0 {
			s = s[:j]
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			consumed, haveConsumed = n, true
		}
	}
	reason, haveReason := "", false
	if i := strings.Index(res, `(:reason-unknown "`); i >= 0 {
		rest := res[i+len(`(:reason-unknown "`):]
		if j := strings.LastIndex(rest, `"`); j >= 0 {
			reason, haveReason = rest[:j], true
		}
	}
	if os.Getenv("OATH_PROVE_CALIBRATE") != "" {
		verdict := "unknown"
		if strings.HasPrefix(res, "unsat") {
			verdict = "unsat"
		} else if strings.HasPrefix(res, "sat") {
			verdict = "sat"
		}
		fmt.Fprintf(os.Stderr, "CALIB %s rlimit=%d reason=%q\n", verdict, consumed, reason)
		if !haveConsumed {
			raw := res
			if len(raw) > 400 {
				raw = raw[:400]
			}
			fmt.Fprintf(os.Stderr, "CALIB-RAW %q\n", raw)
		}
	}
	calibLastConsumed.Store(consumed)
	// ATTEMPT VALIDITY (SPEC §7.2, #29): a non-verdict is an OUTCOME only
	// when z3's own telemetry proves the attempt was deterministic —
	// either the budget was genuinely spent (reason "canceled" with
	// consumed >= rlimit; z3 overshoots by a few units), or the solver
	// gave up for a reason that is a pure function of the script
	// (incompleteness). Everything else is the ENVIRONMENT talking:
	// memout (the memory bound fired — bound size is machine policy),
	// "canceled" below budget (an external cancel), or missing telemetry
	// (the process died mid-attempt). Recording any of those as unproven
	// would make verdicts depend on RAM and signals, which is the
	// machine-dependence this design exists to abolish.
	//
	// The cases are ORDERED exactly as the sequence of early returns they
	// replaced: a terminal verdict wins first, and the invalidating
	// conditions are only consulted for a non-verdict. Collapsing them into
	// one switch is what gives the function a SINGLE exit, which is what
	// makes the telemetry publication below unmissable — a future case added
	// with its own `return` would silently stop reporting the attempt.
	out, invalid := res, false
	switch {
	case strings.HasPrefix(res, "unsat") || strings.HasPrefix(res, "sat"):
		// a terminal verdict; validity does not arise
	case !haveConsumed || !haveReason,
		// A blank reason on a non-verdict is absence of evidence, not
		// evidence of determinism — the rule demands POSITIVE telemetry
		// (found by the blind kernel, DIVERGENCES 71 ambiguity 3).
		reason == "",
		strings.Contains(reason, "memout") || strings.Contains(reason, "memory"),
		reason == "canceled" && consumed < rl:
		out, invalid = "", true
	}
	publishZ3Telemetry(rl, consumed, reason, out, invalid)
	return out, invalid
}

// z3Telemetry is one solver attempt as the runner saw it: the budget it was
// GIVEN, what it spent, z3's own reason string, and the verdict after attempt
// validity has been applied. `Verdict` is `capHit` for every INVALID attempt —
// wall-clock cap, memout, an external cancel below budget, or missing telemetry
// — because the kernel treats those identically (they are not outcomes), and a
// consumer that split them would be inventing a distinction §7.2 does not make.
type z3Telemetry struct {
	Seq      int64 // increments once per attempt; see solve's freshness check
	Budget   int64
	Consumed int64
	Reason   string
	Verdict  string // unsat | sat | unknown | capHit
}

// lastZ3 holds the most recent attempt's telemetry, and z3Seq counts attempts.
//
// A SIDE-CHANNEL, exactly like calibLastConsumed above and subject to the same
// rule: nothing in a proof outcome may depend on it. It exists so a diagnostic
// producer can report WHY a property is not proven — which needs the budget and
// the reason string, neither of which survives runZ3Budget's (string, bool)
// return. The alternative was a second script builder outside the prover, which
// this repo has already learned re-derives the bytes its author remembered.
//
// It is written unconditionally and read only by an installed observer, so the
// parallel scanBulkProve path is unaffected: with no observer, nobody reads it.
// The seq counter is what makes a read SAFE rather than merely usual — see
// solve, which refuses telemetry that another goroutine could have overwritten.
var (
	lastZ3 atomic.Value // z3Telemetry
	z3Seq  atomic.Int64
)

func publishZ3Telemetry(budget, consumed int64, reason, out string, invalid bool) {
	v := "unknown"
	switch {
	case invalid:
		v = "capHit"
	case strings.HasPrefix(out, "unsat"):
		v = "unsat"
	case strings.HasPrefix(out, "sat"):
		v = "sat"
	}
	lastZ3.Store(z3Telemetry{Seq: z3Seq.Add(1), Budget: budget,
		Consumed: consumed, Reason: reason, Verdict: v})
}

func sortedDepHashes(d *Def) []string {
	deps := collectDeps(d)
	out := make([]string, 0, len(deps))
	for h := range deps {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

type propOutcome struct {
	status string // proven | refuted | unknown
	method string // direct | induction on <binder>
	detail string // bail reason or countermodel
}

// proveOne attempts a single property: direct, then structural induction on
// each datatype-typed binder with IHs generalized over the other binders.
// pi is the property's own index — its own lemma (from a prior run) is
// excluded so a property can never prove itself.
func (c *smtCtx) proveOne(d *Def, h string, m *Meta, p *Prop, pi int) propOutcome {
	o := c.proveOneInner(d, h, m, p, pi)
	// Annotate "(hinted)" when the proof succeeded with an author-supplied lemma
	// actually in scope (#67): a legible signal that this guarantee needed a
	// nudge. The lemma-free strategy admits no lemmas, so it is never hinted.
	if o.status == "proven" && !strings.Contains(o.method, "lemma-free") && c.hintsInScope(m, h, pi) {
		o.method += " (hinted)"
	}
	return o
}

// hintsInScope reports whether an author hint for property pi named a lemma the
// library actually collected AND admitted (proven, translatable, not the goal's
// own property) — the condition under which the hint could have helped.
func (c *smtCtx) hintsInScope(m *Meta, h string, pi int) bool {
	for _, hr := range m.Hints[pi] {
		for i := range c.lemmas {
			if c.lemmas[i].ownIdx != pi && c.lemmas[i].defHash == hr.Def && c.lemmas[i].propIdx == hr.Prop {
				return true
			}
		}
	}
	return false
}

// forceAbortProps lists property names that OATH_PROVE_FORCE_ABORT asks the
// prover to treat as environmentally aborted. This is TEST-ONLY fault injection
// for the §7.2 per-property validity path (#72), which is otherwise reachable
// only by winning a race against the wall clock — a timing-dependent test would
// be flaky, and this rule is too close to soundness to leave untested. It is NOT
// part of the spec and a conforming kernel need not implement it; unset (the
// only production state) it does nothing. It can only ever SUPPRESS a verdict,
// never fabricate one, so the blast radius is the safe direction.
func forceAbortProps() map[string]bool {
	v := os.Getenv("OATH_PROVE_FORCE_ABORT")
	if v == "" {
		return nil
	}
	out := map[string]bool{}
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}

// onceAborted counts how many times each property has already been force-aborted
// under OATH_PROVE_FORCE_ABORT_ONCE. Guarded because the worker proves a
// dependency level concurrently.
var onceAborted struct {
	sync.Mutex
	seen map[string]bool
}

// forceAbortOnce aborts a property's FIRST attempt only, letting a later attempt
// in the SAME round succeed. That sequence — abort, then a valid `unsat` on the
// same property — is the only way to reach the rule that a proof SUPERSEDES an
// earlier abort, and it cannot be produced by the plain always-abort hook. Test
// scaffolding, like OATH_PROVE_FORCE_ABORT, and equally unable to fabricate a
// verdict: it only ever suppresses one attempt.
func forceAbortOnce(key string) bool {
	v := os.Getenv("OATH_PROVE_FORCE_ABORT_ONCE")
	if v == "" {
		return false
	}
	match := false
	for _, n := range strings.Split(v, ",") {
		if strings.TrimSpace(n) == key {
			match = true
			break
		}
	}
	if !match {
		return false
	}
	onceAborted.Lock()
	defer onceAborted.Unlock()
	if onceAborted.seen == nil {
		onceAborted.seen = map[string]bool{}
	}
	if onceAborted.seen[key] {
		return false // already spent: this attempt proceeds normally
	}
	onceAborted.seen[key] = true
	return true
}

func (c *smtCtx) proveOneInner(d *Def, h string, m *Meta, p *Prop, pi int) propOutcome {
	pname := metaPropName(m, pi)
	if fa := forceAbortProps(); fa != nil && fa[pname] {
		return propOutcome{status: "invalidated", detail: "forced abort (OATH_PROVE_FORCE_ABORT)"}
	}
	if forceAbortOnce(pname) {
		return propOutcome{status: "invalidated", detail: "forced abort, first attempt only (OATH_PROVE_FORCE_ABORT_ONCE)"}
	}
	// Script stability: all emitted symbols are STRUCTURALLY named (binder
	// index, constructor-field index, function-parameter index) — no
	// counters exist, so a goal's script is canonical by construction:
	// byte-identical across attempt histories, warm/cold runs, and
	// independent kernel implementations. Names + assertion order steer
	// solver search; canonicality makes outcomes a pure function of
	// (goal, lemma set, solver version, rlimit).
	// SPEC §7.2 relevance (#25): only lemmas whose every mention lies inside
	// this goal's footprint enter the problem.
	footprint := goalFootprint(c.st, h, d, p)
	// Declare the goal's binders as fresh constants. Function-typed binders
	// are array-sorted constants like any other — uniformly quantifiable.
	var binderDecls []string
	consts := map[int]string{}
	binderSorts := make([]string, len(p.Binders))
	for i := range p.Binders {
		s, err := c.sortOf(&p.Binders[i])
		if err != nil {
			return propOutcome{status: "unknown", detail: err.Error()}
		}
		name := fmt.Sprintf("b%d", i)
		binderDecls = append(binderDecls, fmt.Sprintf("(declare-const %s %s)", name, s))
		consts[i] = name
		binderSorts[i] = s
	}

	goal, err := c.formulaWith(d, h, p, consts)
	if err != nil {
		return propOutcome{status: "unknown", detail: err.Error()}
	}

	// buildScript emits the goal script. withLemmas=false omits the admissible
	// lemma library entirely — the LEMMA-FREE attempt (SPEC §7.2, #53). Every
	// other component (declarations, defining-equation axioms, binder
	// declarations, the negated goal) is byte-identical either way, so the
	// canonical with-lemmas script that prove/scripts.txt pins is unchanged.
	// omitAxioms names quantified defining equations to LEAVE OUT — used only by
	// the deterministic-instantiation attempt, which replaces the equations of
	// the functions it instantiates with ground instances of those same
	// equations. Every existing caller passes nil, so every script the rest of
	// the strategy sequence emits is byte-identical to before; prove/scripts.txt
	// and the induction bytes prove/attempts.txt pins are untouched.
	buildScript := func(extraDecls, extraAsserts []string, negated string, model bool, withLemmas bool, omitAxioms map[int]bool) string {
		var b strings.Builder
		for _, x := range c.decls {
			b.WriteString(x + "\n")
		}
		for i, x := range c.axioms {
			if omitAxioms[i] {
				continue
			}
			b.WriteString(x + "\n")
		}
		// Canonical lemma order (script stability, part 2): emission sorted
		// by (definition hash, property index), so the script depends only
		// on the admissible lemma SET — never on whether a lemma arrived
		// from prior-run metadata or was proven moments ago. Assert order
		// steers solver search; acquisition history must not.
		var adm []*lemma
		if withLemmas {
			for i := range c.lemmas {
				if c.lemmas[i].ownIdx != pi && (lemmaAdmissible(&c.lemmas[i], h, footprint) || hintedLemma(m, pi, &c.lemmas[i])) {
					adm = append(adm, &c.lemmas[i])
				}
			}
		}
		sort.Slice(adm, func(a, b2 int) bool {
			if adm[a].defHash != adm[b2].defHash {
				return adm[a].defHash < adm[b2].defHash
			}
			return adm[a].ownIdx < adm[b2].ownIdx
		})
		for _, l := range adm {
			b.WriteString(l.text + "\n")
		}
		for _, x := range binderDecls {
			b.WriteString(x + "\n")
		}
		for _, x := range extraDecls {
			b.WriteString(x + "\n")
		}
		for _, x := range extraAsserts {
			b.WriteString(x + "\n")
		}
		fmt.Fprintf(&b, "(assert (not %s))\n(check-sat)\n", negated)
		if model {
			b.WriteString("(get-model)\n")
		}
		return b.String()
	}
	// script is the canonical (with-lemmas) emission every existing strategy
	// uses; prove/scripts.txt pins its direct-attempt form.
	script := func(extraDecls, extraAsserts []string, negated string, model bool) string {
		return buildScript(extraDecls, extraAsserts, negated, model, true, nil)
	}

	// LEMMA-FREE FIRST ATTEMPT (SPEC §7.2, #53). A budget-limited solver is
	// non-monotone in its axiom set: admitting the goal's (legitimately
	// relevant) lemmas can convert a goal that discharges in ~2K rlimit into
	// one that does not terminate within the full budget. Measured on
	// q-peek.peek-is-head: 2,294 rlimit lemma-free versus unprovable at 400M
	// with its 12 admissible lemmas. So try the goal with NO lemmas first, at
	// the reduced budget — the successes are milliseconds, and a failure costs
	// a fraction of one full attempt. Everything below is unchanged, so the
	// recorded outcome is the UNION: anything provable before is still proven
	// by a later strategy, plus the goals the library was strangling.
	if lf, lfCap := c.solve("lemma-free", "", buildScript(nil, nil, goal, !c.quantified, false, nil),
		func(sc string) (string, bool) { return runZ3Budget(sc, c.budget(lemmaFreeRlimit())) }); !lfCap {
		if os.Getenv("OATH_PROVE_SPLIT") != "" {
			pname := storedPropName(m, pi)
			v := "unknown"
			if strings.HasPrefix(lf, "unsat") {
				v = "unsat"
			} else if strings.HasPrefix(lf, "sat") {
				v = "sat"
			}
			fmt.Fprintf(os.Stderr, "SPLIT\t%s.%s\tphase=lemma-free\tconsumed=%d\tverdict=%s\n",
				m.Name, pname, calibLastConsumed.Load(), v)
		}
		if strings.HasPrefix(lf, "unsat") && !c.enumerate {
			return propOutcome{status: "proven", method: "direct (lemma-free)"}
		}
	}

	// Direct attempt. An INVALID attempt (environmental abort) yields NO
	// EVIDENCE (SPEC §7.2): it must not end the run — a later strategy can
	// still prove the goal, and unsat is positive evidence no environment
	// can fake. Invalidity taints only the NEGATIVE case: recording
	// "unproven" with an invalid attempt among the strategies would
	// launder an environmental abort into a verdict, so that combination
	// invalidates at the end of proveOne instead. (Found in the wild:
	// z3 crashes with empty output on t-insert.insert-length's direct
	// script — deterministically — while induction proves the goal fine.)
	// A goal with a datatype-typed binder can be discharged by induction, so
	// its direct attempt is almost always futile — yet at the full budget it
	// burns minutes before failing (#50). Run it at the reduced
	// proveDirectRlimit; if that under-runs, induction is tried next, and a
	// full-budget direct FALLBACK below catches the rare goal that proves only
	// by heavy direct. The emitted script is byte-identical either way (the
	// budget is a runner option outside the hashed bytes, SPEC §7.2), so this
	// changes speed, not outcomes or script hashes.
	// A goal is INDUCTION-ELIGIBLE if it has a datatype binder (structural /
	// lexicographic induction) or an Int binder (Peano integer induction, #56).
	// For such goals the direct attempt is usually futile yet burns the full
	// budget (#50), so run it reduced and let the full-budget fallback below catch
	// the rare goal that proves only by heavy direct — outcomes unchanged.
	hasDTBinder := false
	inductionEligible := false
	for i := range binderSorts {
		if _, ok := c.dtBySort[binderSorts[i]]; ok {
			hasDTBinder = true
			inductionEligible = true
		}
		if binderSorts[i] == "Int" {
			inductionEligible = true
		}
	}
	if c.observer != nil {
		// Reported HERE, immediately before the direct phase, so that "absent"
		// means the direct phase was never reached rather than "false".
		c.observer.onDatatypeBinder(hasDTBinder)
	}
	directScript := script(nil, nil, goal, !c.quantified)
	sawInvalid := false
	directStart := time.Now()
	var out string
	var capHit bool
	if inductionEligible {
		out, capHit = c.solve("direct", "", directScript,
			func(sc string) (string, bool) { return runZ3Budget(sc, c.budget(directRlimit())) })
	} else {
		out, capHit = c.solve("direct", "", directScript, c.runFull)
	}
	if os.Getenv("OATH_PROVE_SPLIT") != "" {
		pname := storedPropName(m, pi)
		v := "unknown"
		switch {
		case capHit:
			v = "capHit"
		case strings.HasPrefix(out, "unsat"):
			v = "unsat"
		case strings.HasPrefix(out, "sat"):
			v = "sat"
		}
		fmt.Fprintf(os.Stderr, "SPLIT\t%s.%s\tphase=direct\thasDT=%v\twall=%s\tconsumed=%d\tverdict=%s\n",
			m.Name, pname, hasDTBinder, time.Since(directStart), calibLastConsumed.Load(), v)
	}
	if capHit {
		sawInvalid = true
		out = ""
	}
	switch {
	case strings.HasPrefix(out, "unsat") && !c.enumerate:
		return propOutcome{status: "proven", method: "direct"}
	case strings.HasPrefix(out, "sat") && !c.quantified:
		return propOutcome{status: "refuted", detail: strings.TrimSpace(strings.TrimPrefix(out, "sat"))}
	}

	// Induction on each datatype binder.
	for i := range p.Binders {
		dt, ok := c.dtBySort[binderSorts[i]]
		if !ok {
			continue
		}
		allUnsat := true
		for ci := range dt.ctors {
			var extraDecls, extraAsserts []string
			var fieldConsts []string
			for fi, fs := range dt.fields[ci] {
				fc := fmt.Sprintf("f%d", fi)
				extraDecls = append(extraDecls, fmt.Sprintf("(declare-const %s %s)", fc, fs))
				fieldConsts = append(fieldConsts, fc)
				if fs == dt.name {
					// Induction hypothesis: the induction binder becomes the
					// recursive field; every other binder is generalized
					// (∀-quantified — array-encoded functions included).
					ih, err := c.formulaWith(d, h, p, map[int]string{i: fc})
					if err != nil {
						allUnsat = false
						break
					}
					extraAsserts = append(extraAsserts, "(assert "+ih+")")
				}
			}
			if !allUnsat {
				break
			}
			ctorExpr := dt.ctors[ci]
			if len(fieldConsts) > 0 {
				ctorExpr = fmt.Sprintf("(%s %s)", dt.ctors[ci], strings.Join(fieldConsts, " "))
			}
			assign := map[int]string{}
			for k, v := range consts {
				assign[k] = v
			}
			assign[i] = ctorExpr
			subgoal, err := c.formulaWith(d, h, p, assign)
			if err != nil {
				allUnsat = false
				break
			}
			// DETERMINISTIC INSTANTIATION, tried FIRST (docs/deterministic-
			// instantiation.md). The quantified defining-equation axioms do not
			// e-match to a proof on composed-recursion subgoals; ground instances
			// of those same equations, at the terms this subgoal mentions,
			// discharge them. Only `unsat` is taken — a `sat` under a case split
			// is not a refutation, and an exhausted budget is not evidence — so
			// the unchanged quantified attempt below runs on every other result
			// and NO goal that proves today can regress. A wall-cap hit here is
			// deliberately NOT recorded as an invalid attempt: this attempt is
			// optional, the fallback runs regardless, and that fallback's own
			// validity is what governs the negative case (SPEC §7.2). Treating
			// the preview's environmental abort as invalidating would let a busy
			// machine suppress verdicts the search still reached.
			//
			// NOT RECORDED BY `scriptAttempts`. Enumeration (#139) pins the
			// candidate scripts of the strategies SPEC §7.2 names, and §7.2 does
			// not yet name this one: its normative text and the fixture
			// regeneration are the next phase. Emitting it into the walk now
			// would rewrite prove/attempts.txt ahead of the specification that
			// licenses the rows. The consequence is stated rather than hidden: a
			// verdict this preview attempt decides is, for now, determined by a
			// script no fixture pins — the same gap #139 closed for induction,
			// reopened narrowly and deliberately until the SPEC text lands.
			// PHASE 1 GATE (default OFF). The preview is opt-in behind
			// OATH_PROVE_INSTANTIATE until Phase 2 lands the SPEC §7.2 text,
			// prove/attempts.txt regeneration, and the oathrs blind
			// re-derivation together. Rationale: default-ON the preview can only
			// prove MORE-or-equal, so at the normative 400M budget it could
			// discharge a committed property currently recorded `unproven` —
			// a GAIN that would move outcomes.json and fork conformance against a
			// quantified-only second kernel. Gated OFF, emission is unchanged and
			// the committed corpus reproduces byte-identically; the strategy still
			// runs under the flag for the witnessing tests and for anyone opting
			// in. Flipping the default is a deliberate Phase 2 act, not this one.
			if !c.enumerate && instantiationEnabled() {
				if inst, omit := c.instantiatedSubgoal(d, h, p, i, dt, ci, fieldConsts); len(inst) > 0 {
					iout, _ := c.solve("induction-instantiated",
						fmt.Sprintf("binder %d ctor %d", i, ci),
						buildScript(extraDecls, append(append([]string{}, inst...), extraAsserts...),
							subgoal, false, false, omit),
						func(sc string) (string, bool) {
							return runZ3Budget(sc, c.budget(instantiatedRlimit()))
						})
					if strings.HasPrefix(iout, "unsat") {
						continue
					}
				}
			}
			out, capHit := c.solve("induction", fmt.Sprintf("binder %d ctor %d", i, ci),
				script(extraDecls, extraAsserts, subgoal, false), c.runFull)
			if capHit {
				sawInvalid = true
				allUnsat = false
				break
			}
			if !strings.HasPrefix(out, "unsat") {
				allUnsat = false
				break
			}
		}
		if allUnsat && !c.enumerate {
			bname := fmt.Sprintf("binder %d", i)
			if i < len(m.ParamNames) { // best effort; prop binders are unnamed
				bname = fmt.Sprintf("binder %d", i)
			}
			return propOutcome{status: "proven", method: "induction on " + bname}
		}
	}
	// Lexicographic induction on ordered pairs of datatype binders (#17,
	// SPEC §7.2): for functions like merge that shrink EITHER argument.
	// Sound by the lexicographic subterm order: IH1 shrinks binder i with
	// every other binder generalized; IH2 pins binder i to the SAME
	// constructed value and shrinks binder j.
	for i := range p.Binders {
		dti, ok := c.dtBySort[binderSorts[i]]
		if !ok {
			continue
		}
		for j := range p.Binders {
			if j == i {
				continue
			}
			dtj, ok := c.dtBySort[binderSorts[j]]
			if !ok {
				continue
			}
			allUnsat := true
		lexCtorI:
			for ci := range dti.ctors {
				var declsI []string
				var fieldsI []string
				recI := []string{}
				for fi, fs := range dti.fields[ci] {
					fc := fmt.Sprintf("g%d", fi)
					declsI = append(declsI, fmt.Sprintf("(declare-const %s %s)", fc, fs))
					fieldsI = append(fieldsI, fc)
					if fs == dti.name {
						recI = append(recI, fc)
					}
				}
				ctorI := dti.ctors[ci]
				if len(fieldsI) > 0 {
					ctorI = fmt.Sprintf("(%s %s)", dti.ctors[ci], strings.Join(fieldsI, " "))
				}
				if len(recI) == 0 {
					// Base case in the first component: j and the rest stay
					// as their declared constants; no hypotheses.
					assign := map[int]string{}
					for k, v := range consts {
						assign[k] = v
					}
					assign[i] = ctorI
					subgoal, err := c.formulaWith(d, h, p, assign)
					if err != nil {
						allUnsat = false
						break
					}
					out, capHit := c.solve("lexicographic",
						fmt.Sprintf("binders %d,%d ctor %d base", i, j, ci),
						script(declsI, nil, subgoal, false), c.runFull)
					if capHit {
						sawInvalid = true
						allUnsat = false
						break
					}
					if !strings.HasPrefix(out, "unsat") {
						allUnsat = false
						break
					}
					continue
				}
				// Recursive first component: case-split the second.
				for cj := range dtj.ctors {
					extraDecls := append([]string{}, declsI...)
					var extraAsserts []string
					var fieldsJ []string
					recJ := []string{}
					for fj, fs := range dtj.fields[cj] {
						fc := fmt.Sprintf("h%d", fj)
						extraDecls = append(extraDecls, fmt.Sprintf("(declare-const %s %s)", fc, fs))
						fieldsJ = append(fieldsJ, fc)
						if fs == dtj.name {
							recJ = append(recJ, fc)
						}
					}
					ctorJ := dtj.ctors[cj]
					if len(fieldsJ) > 0 {
						ctorJ = fmt.Sprintf("(%s %s)", dtj.ctors[cj], strings.Join(fieldsJ, " "))
					}
					// IH1: first component shrank; everything else (j
					// included) universally generalized.
					for _, fc := range recI {
						ih, err := c.formulaWith(d, h, p, map[int]string{i: fc})
						if err != nil {
							allUnsat = false
							break lexCtorI
						}
						extraAsserts = append(extraAsserts, "(assert "+ih+")")
					}
					// IH2: first component pinned to the SAME constructed
					// value; second shrank; the rest generalized.
					for _, fc := range recJ {
						ih, err := c.formulaWith(d, h, p, map[int]string{i: ctorI, j: fc})
						if err != nil {
							allUnsat = false
							break lexCtorI
						}
						extraAsserts = append(extraAsserts, "(assert "+ih+")")
					}
					assign := map[int]string{}
					for k, v := range consts {
						assign[k] = v
					}
					assign[i] = ctorI
					assign[j] = ctorJ
					subgoal, err := c.formulaWith(d, h, p, assign)
					if err != nil {
						allUnsat = false
						break lexCtorI
					}
					out, capHit := c.solve("lexicographic",
						fmt.Sprintf("binders %d,%d ctor %d,%d", i, j, ci, cj),
						script(extraDecls, extraAsserts, subgoal, false), c.runFull)
					if capHit {
						sawInvalid = true
						allUnsat = false
						break lexCtorI
					}
					if !strings.HasPrefix(out, "unsat") {
						allUnsat = false
						break lexCtorI
					}
				}
			}
			if allUnsat && !c.enumerate {
				return propOutcome{status: "proven", method: fmt.Sprintf("lexicographic induction on binders %d,%d", i, j)}
			}
		}
	}
	// Recursion induction on a measure-total self function (#56). A
	// measure-carrying law like `length (replicate n x) = n` or
	// `length (range lo hi) = hi − lo` needs induction over the well-founded
	// order the FUNCTION'S OWN recursion follows — which no datatype-binder
	// scheme reaches, and which single-binder Peano cannot express when the
	// counter increases toward a bound (range's measure is hi − lo). Because the
	// measure function's axiom is emitted without a pattern (ensureFn, above),
	// Z3's model-based instantiation discharges the cases without diverging.
	//
	// The scheme: map the goal's leading binders to the self function's
	// parameters and walk its body (reusing the ranking walker, ranking.go) to
	// recover every self-call SITE — its path guard and its recursive arguments.
	// Then, over those binders:
	//   BASE — prove the goal where NO recursive path fires: the conjunction of
	//          the negations of all site guards (the complement of the recursive
	//          region).
	//   STEP — for each site, prove the goal under that site's guard, assuming
	//          the goal at the site's recursive arguments (the induction
	//          hypothesis at a point of strictly smaller measure).
	// Sound by well-founded induction on the measure the function is total by:
	// the recursive arguments strictly decrease it, so a false property would
	// fail either the base (it is false off the recursion) or some step (false
	// at a point whose smaller-measure hypothesis holds). Obligations run at the
	// reduced induction budget — a legitimate case discharges quickly, and a
	// non-inductive goal fails fast instead of burning the full budget.
	if dm := c.metaOf(h); dm != nil && dm.Termination == "measure" {
		dParams := 0
		bodyCur := d.Body
		var param0Ty *Ty
		for bodyCur.K == "lam" {
			if dParams == 0 {
				param0Ty = bodyCur.Ty
			}
			dParams++
			bodyCur = bodyCur.A
		}

		// Map the property's binders to the function's inputs. `env` is what the
		// walker treats as the parameters; `ihArg` gives, per site, the recursive-
		// call expression for property binder j (ok=false leaves it generalized).
		var env []smtVal
		var ihArg func(site rankSite, j int) (string, bool)

		// Constructor case (#57): a single datatype parameter built from the
		// property's binders — the law applies f to `(ctor b0 b1 …)` (rle-expand's
		// `(Run n v)`). Bind the parameter to that construction; a site's recursive
		// argument is then a datatype value, deconstructed by the selectors.
		if dParams == 1 && param0Ty != nil {
			if s, err := c.sortOf(param0Ty); err == nil {
				if dt := c.dtBySort[s]; dt != nil && len(dt.ctors) == 1 &&
					len(dt.fields[0]) == len(p.Binders) && len(dt.sels[0]) == len(p.Binders) {
					match := true
					for j := range p.Binders {
						if dt.fields[0][j] != binderSorts[j] {
							match = false
							break
						}
					}
					if match {
						parts := []string{dt.ctors[0]}
						for j := range p.Binders {
							parts = append(parts, consts[j])
						}
						env = []smtVal{{expr: "(" + strings.Join(parts, " ") + ")", sort: s}}
						sels := dt.sels[0]
						ihArg = func(site rankSite, j int) (string, bool) {
							return fmt.Sprintf("(%s %s)", sels[j], site.args[0]), true
						}
					}
				}
			}
		}
		// Positional case: the property's leading binders ARE the parameters.
		if env == nil && dParams > 0 && len(p.Binders) >= dParams {
			env = make([]smtVal, dParams)
			for i := 0; i < dParams; i++ {
				env[i] = smtVal{expr: consts[i], sort: binderSorts[i]}
			}
			ihArg = func(site rankSite, j int) (string, bool) {
				if j < dParams {
					return site.args[j], true
				}
				return "", false
			}
		}

		if env != nil {
			w := &rankWalker{c: c, nparams: dParams}
			w.walk(bodyCur, env, nil, false)
			if !w.bad && len(w.sites) > 0 {
				guardConj := func(gs []string) string {
					if len(gs) == 0 {
						return "true"
					}
					return "(and " + strings.Join(gs, " ") + ")"
				}
				discharge := func(detail string, extraAsserts []string, negated string) (bool, bool) {
					out, cap := c.solve("recursion-induction", detail,
						script(nil, extraAsserts, negated, false),
						func(sc string) (string, bool) { return runZ3Budget(sc, c.budget(directRlimit())) })
					return strings.HasPrefix(out, "unsat"), cap
				}
				// BASE: the goal off the recursive region.
				var baseAsserts []string
				for _, site := range w.sites {
					baseAsserts = append(baseAsserts, "(assert (not "+guardConj(site.guards)+"))")
				}
				proved, capHit := discharge("base", baseAsserts, goal)
				allOK := !capHit && proved
				if capHit {
					sawInvalid = true
				}
				// STEP: group the self-call sites by their path guard, and for each
				// path prove the goal under that guard assuming the property at
				// EVERY recursive call reachable on it. A function with several
				// recursive calls on one path (fib's fib(n-1) and fib(n-2)) needs
				// all their hypotheses together; discharging one site at a time
				// would be sound but too weak to combine them. Single-recursive-call
				// functions have one site per guard, so their obligation — and its
				// script bytes — are unchanged.
				groups := map[string][]rankSite{}
				var order []string
				for _, site := range w.sites {
					g := guardConj(site.guards)
					if _, seen := groups[g]; !seen {
						order = append(order, g)
					}
					groups[g] = append(groups[g], site)
				}
				for gi, g := range order {
					if !allOK {
						break
					}
					asserts := []string{"(assert " + g + ")"}
					built := true
					for _, site := range groups[g] {
						ihAssign := map[int]string{}
						for j := range p.Binders {
							if expr, ok := ihArg(site, j); ok {
								ihAssign[j] = expr
							}
						}
						ih, err := c.formulaWith(d, h, p, ihAssign)
						if err != nil {
							built = false
							break
						}
						asserts = append(asserts, "(assert "+ih+")")
					}
					if !built {
						allOK = false
						break
					}
					// The GUARD INDEX, not the guard text: `order` is the sites'
					// first-touch order, which §7.2's structural naming already
					// makes deterministic, and a guard expression is unbounded
					// prose that would put solver syntax inside a fixture label.
					sp, spCap := discharge(fmt.Sprintf("step %d", gi), asserts, goal)
					if spCap {
						sawInvalid = true
						allOK = false
						break
					}
					if !sp {
						allOK = false
						break
					}
				}
				if allOK && !c.enumerate {
					return propOutcome{status: "proven", method: "recursion induction on " + smtName(c.st.NameOf(h))}
				}
			}
		}
	}

	// Full-budget direct FALLBACK (#50). The datatype-binder direct attempt
	// above ran at the reduced budget; induction and lexicographic induction
	// have now failed. Retry the SAME direct script at the full budget so a
	// goal provable only by heavy direct search is not lost — this keeps the
	// outcome a pure function of (script, solver, full budget), identical to
	// the pre-#50 kernel, while the common inductive case already returned
	// fast above. The script is byte-identical to the reduced attempt, so no
	// new direct-attempt script hash is introduced (SPEC §7.2).
	// WHEN A CAP COLLAPSES THE TWO BUDGETS, THIS FALLBACK *IS* THE ATTEMPT ABOVE.
	// The reduced direct attempt and this one run byte-identical scripts, so at
	// equal budgets the solver is deterministic and the retry cannot return
	// anything new — it just spends the budget twice, on the diagnostic path the
	// cap exists to make cheap.
	//
	// OUTCOME-PRESERVING, which is why it is safe rather than merely faster: the
	// only way to reach here with the budgets equal is a clamp (adjudication's
	// cap, or OATH_PROVE_RLIMIT below the reduced budget). On every normative
	// path the nominal budgets are 4M and 400M and this never fires.
	//
	// Two conditions are NOT folded in, deliberately. An ENVIRONMENTALLY ABORTED
	// first attempt (`capHit`) is retried, because a wall-cap abort is not a
	// verdict and the second run may complete. And ENUMERATION still emits it, or
	// `prove/attempts.txt` would lose a script it is supposed to pin.
	redundantFallback := !c.enumerate && !capHit &&
		c.budget(directRlimit()) == c.budget(effectiveRlimit())
	if inductionEligible && !redundantFallback {
		// Recorded under its own label even though the bytes are the direct
		// attempt's. §7.2 asserts in prose that the fallback introduces no new
		// script hash; recording it makes that checkable rather than believed —
		// TestFallbackReusesTheDirectScriptBytes reads the two attempts and
		// compares them.
		fb, fbCap := c.solve("direct-fallback", "", directScript, c.runFull)
		if os.Getenv("OATH_PROVE_SPLIT") != "" {
			fmt.Fprintf(os.Stderr, "SPLIT\t%s\tphase=direct-fallback\tconsumed=%d\n", m.Name, calibLastConsumed.Load())
		}
		if fbCap {
			sawInvalid = true
		} else {
			switch {
			case strings.HasPrefix(fb, "unsat") && !c.enumerate:
				return propOutcome{status: "proven", method: "direct"}
			case strings.HasPrefix(fb, "sat") && !c.quantified:
				return propOutcome{status: "refuted", detail: strings.TrimSpace(strings.TrimPrefix(fb, "sat"))}
			}
		}
	}
	if sawInvalid {
		return propOutcome{status: "invalidated", detail: "a strategy attempt was environmentally aborted and no remaining strategy proved the goal — no valid negative verdict exists (SPEC §7.2)"}
	}
	return propOutcome{status: "unknown", detail: "no direct proof; induction did not discharge"}
}

func apiProve(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	return apiProveHash(st, h, name)
}

// apiProveHash proves a specific object by hash, independent of any name. The
// verification worker pool (#14) needs this: a `require_proven` name defers its
// repoint, so while a job is pending the NAME still resolves to the previous
// object — the proof must target the stored candidate hash, not the name.
// `display` is used only for the human-readable report.

// promotableToProven reports whether a guarantee may be lifted to `proven` once
// every property carries a proof (SPEC §5). Named so the rule has one home: a
// test that re-spelled it could agree with itself while disagreeing with the
// prover.
//
// The third arm is the one the three-way outcome added. An `asserted` level
// reached through INDETERMINACY is promotable — its properties exist and none
// was refuted, the tester merely could not evaluate them, and a proof does not
// care whether a generated case ran out of fuel. Excluding it would make such
// definitions unprovable in principle, since testing can never lift them to
// `tested` for a proof to land on. A bare `asserted` has no properties at all,
// so there is nothing to prove and nothing to promote.
func promotableToProven(g Guarantee) bool {
	switch g.Level {
	case "tested", "proven":
		return true
	case "asserted":
		return len(g.Indeterminate) > 0
	}
	return false
}

func apiProveHash(st *Store, h string, display string) (string, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	if d.K != "func" {
		return "", fmt.Errorf("only function definitions have properties to prove")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if len(d.Props) == 0 {
		return fmt.Sprintf("%s swears no properties — nothing to prove.\n", display), nil
	}
	if err := z3Available(); err != nil {
		return "", err
	}

	var b strings.Builder
	// A property the deterministic tester already refuted has a concrete
	// counterexample; an SMT "proof" of it would be a contradiction we must
	// not record (defense in depth behind the totality gate above).
	testFalsified := map[string]bool{}
	for _, fn := range m.Guarantee.Falsified {
		testFalsified[fn] = true
	}
	// SPEC §7.2: a TWO-LEVEL fixpoint.
	//
	// Inner (self-lemma availability): iterate until no new property proves.
	// The lemma-growth gate (#24) makes it cheap: a goal is only re-attempted
	// when the same-def lemma set has actually grown since its last attempt —
	// outcome-identical to naive iteration, but full-budget burns on genuinely
	// unprovable goals happen once instead of once per pass.
	//
	// Outer (run stability): the recorded state must be a fixpoint of the
	// whole-RUN map F(S) = proven set produced by proving with S recorded.
	// A property proven EARLY in a cold run (small lemma set) is not
	// necessarily re-provable from the FINAL state: a budget-limited search
	// is non-monotone in its axiom set — extra lemmas can divert it. The
	// corpus witness is q-drop cold: drop-back-only proves from
	// {drop-front-nonempty} alone but NOT once drop-empty is also asserted,
	// so a single cold pass records a proof the store's own state cannot
	// reproduce, and the next warm run silently drops it. Iterating F until
	// S == F(S) makes every recorded proof re-derivable from exactly the
	// state the store records — warm and cold runs converge to the same
	// self-consistent verdicts.
	var outcomes []propOutcome
	var provenSet, withheld, aborted []bool
	var lemmaCount, ownProven int
	// The STANDING verdict set for the aborted-property carry-forward: what the
	// store records for this object right now. A property whose attempt is
	// environmentally aborted has no valid verdict, so its standing verdict must
	// survive untouched — dropping it would demote a proof on environmental
	// grounds, precisely what §7.2 forbids.
	//
	// This is REFRESHED at the top of every round (below), not snapshotted before
	// the loop. m.ProvenProps advances as rounds settle, so a property proven in
	// an earlier round of THIS run has a standing verdict too; reading only the
	// pre-run store state would discard a proof this very run derived — the same
	// loss #72 exists to prevent. (The blind Rust kernel read the rule this way
	// and was right; the reference did not. SPEC §7.2 now pins the referent.)
	origProven := map[int]bool{}
	// #181: the loop advances m.ProvenProps IN PLACE, and m aliases the store's
	// cached Meta (GetMeta returns the cached pointer). If the run fails to
	// converge below and returns without SetMeta, the cache would still hold the
	// last provisional (non-fixpoint) set for a later scan to consume as lemmas.
	// Snapshot the pre-run set so the failure path can restore it.
	provenPropsAtRunStart := append([]int(nil), m.ProvenProps...)
	for round := 0; ; round++ {
		c := newSmtCtx(st, d, h)
		lemmaCount, ownProven = loadLemmaLibrary(c, st, d, h, m, -1)
		outcomes = make([]propOutcome, len(d.Props))
		attempted := make([]bool, len(d.Props))
		provenSet = make([]bool, len(d.Props))
		lastEpoch := make([]int, len(d.Props))
		withheld = make([]bool, len(d.Props))
		aborted = make([]bool, len(d.Props))
		origProven = map[int]bool{}
		for _, pi := range m.ProvenProps {
			origProven[pi] = true
		}
		epoch := 1 // bumps whenever a new own-lemma lands
		for {
			progress := false
			for pi := range d.Props {
				if provenSet[pi] || withheld[pi] {
					continue
				}
				if attempted[pi] && lastEpoch[pi] == epoch {
					continue // no relevant lemma has appeared since the last attempt
				}
				pn := metaPropName(m, pi)
				// Fresh translation context per attempt: the script must be a
				// function of (goal, lemma set) alone — a shared context would
				// leak axioms accrued by earlier attempts into later scripts,
				// the acquisition-history dependence this design eliminates.
				ac := newSmtCtx(st, d, h)
				loadLemmaLibrary(ac, st, d, h, m, pi)
				already := map[int]bool{}
				for _, mp := range m.ProvenProps {
					already[mp] = true
				}
				for pj := range d.Props {
					if provenSet[pj] && !already[pj] {
						if f, err := ac.formulaWith(d, h, &d.Props[pj], nil); err == nil {
							ac.lemmas = append(ac.lemmas, lemma{ownIdx: pj, defHash: h, mentions: propMentions(h, &d.Props[pj]),
								text: "(assert " + f + ")"})
						}
					}
				}
				o := ac.proveOne(d, h, m, &d.Props[pi], pi)
				if o.status == "invalidated" {
					// SPEC §7.2, per PROPERTY (#72): an environmental abort is not
					// evidence, so this goal gets NO verdict this run — but that is a
					// fact about THIS property, not about its siblings. Mark it
					// aborted, leave its recorded state alone, and carry on; a
					// sibling whose own attempts completed cleanly still earns its
					// verdict. Invalidating the whole run instead made one
					// intractable property hide every other property's status.
					aborted[pi] = true
					attempted[pi] = true
					lastEpoch[pi] = epoch
					outcomes[pi] = o
					continue
				}
				attempted[pi] = true
				lastEpoch[pi] = epoch
				outcomes[pi] = o
				if o.status == "proven" {
					if testFalsified[pn] {
						withheld[pi] = true
						continue
					}
					provenSet[pi] = true
					// A valid `unsat` SUPERSEDES an earlier abort on this property
					// within the same round: positive evidence no environment can
					// fake beats an attempt that produced none. Without this the
					// report would call a proven property "aborted", and — because
					// the abort branch is checked first — a NEWLY proven one would
					// be dropped from the recorded set entirely.
					// Discriminated by TestProofSupersedesEarlierAbort (mutation-
					// checked: deleting this line fails it). Reaching it needs the
					// abort AND a later success on one property inside the round
					// that ends up LAST, which the always-abort hook cannot produce
					// — hence OATH_PROVE_FORCE_ABORT_ONCE plus a pre-proved store so
					// the fixpoint settles in a single round.
					aborted[pi] = false
					progress = true
					epoch++
				}
			}
			if !progress {
				break
			}
		}
		var newIdx []int
		for pi := range d.Props {
			// An aborted property keeps whatever the store already recorded: this
			// run produced no valid evidence either way about it.
			if provenSet[pi] || (aborted[pi] && origProven[pi]) {
				newIdx = append(newIdx, pi)
			}
		}
		prev := append([]int{}, m.ProvenProps...)
		sort.Ints(prev)
		if len(prev) == len(newIdx) {
			stable := true
			for i := range prev {
				if prev[i] != newIdx[i] {
					stable = false
					break
				}
			}
			if stable {
				break
			}
		}
		if round >= 7 {
			// #181: SPEC §7.2 requires the recorded proven set to BE a fixpoint of
			// F. Reaching the bound with S still changing means S != F(S), and
			// recording it would publish a non-fixpoint indistinguishable
			// downstream from a genuine one (the earlier "record the last result"
			// warning did exactly that, violating the MUST). Fail closed instead —
			// this matches oathrs and can only fire on a corpus that genuinely
			// needs more rounds than the bound (the committed corpus converges by
			// round 3), so raising the bound is the response, never recording.
			// Restore the pre-run set first: m aliases the store's cache, and a
			// failed run must leave no non-fixpoint state behind (see snapshot above).
			m.ProvenProps = provenPropsAtRunStart
			return "", fmt.Errorf(
				"SPEC §7.2 run-stability fixpoint did not converge within %d rounds for %s: "+
					"the recorded proven set is not F(S), so there is no valid conformance outcome "+
					"(raise the bound or investigate a deepening/oscillating corpus)",
				round+1, display)
		}
		m.ProvenProps = newIdx
	}
	if lemmaCount+ownProven > 0 {
		fmt.Fprintf(&b, "lemma library: %d from dependencies, %d from prior runs\n", lemmaCount, ownProven)
	}

	proven := 0
	var provenIdx []int
	anyRefuted := false
	var unprovenPIs []int
	for pi := range d.Props {
		pn := metaPropName(m, pi)
		o := outcomes[pi]
		switch {
		case aborted[pi]:
			// Deliberately NOT "unproven": no valid verdict exists for this goal,
			// which is a different claim. Whatever the store recorded stands — so a
			// prior proof is CARRIED FORWARD into the recorded set here, not just
			// described in the report. Demoting it would convert an environmental
			// abort into a verdict, which is what §7.2 forbids.
			state := "no prior proof to retain"
			if origProven[pi] {
				state = "prior PROVEN retained"
				proven++
				provenIdx = append(provenIdx, pi)
			}
			fmt.Fprintf(&b, "⚠ aborted   %-28s environmental abort, no valid verdict (%s)\n", pn, state)
		case withheld[pi]:
			// Deliberately NOT fed to the dependency-lemma note below: a withheld goal
			// has a concrete test counterexample, so it is FALSE. No dependency lemma
			// can discharge a false property, and advising the reader to go prove one
			// would be actively misleading.
			fmt.Fprintf(&b, "· unproven  %-28s SMT claim contradicts a test counterexample; withheld\n", pn)
		case provenSet[pi]:
			proven++
			provenIdx = append(provenIdx, pi)
			fmt.Fprintf(&b, "∎ PROVEN    %-28s %s (Z3, unbounded ints)\n", pn, o.method)
		case o.status == "refuted":
			anyRefuted = true
			fmt.Fprintf(&b, "✗ REFUTED   %-28s counterexample over unbounded ints:\n", pn)
			for _, line := range strings.Split(o.detail, "\n") {
				fmt.Fprintf(&b, "      %s\n", line)
			}
		default:
			unprovenPIs = append(unprovenPIs, pi)
			fmt.Fprintf(&b, "· unproven  %-28s %s\n", pn, o.detail)
		}
	}
	fmt.Fprintf(&b, "proven: %d/%d properties\n", proven, len(d.Props))

	// When a goal was left unproven, name any reused dependency whose own laws are
	// not proven and so are ABSENT from the lemma library above. Proving them may be
	// what a stuck goal needs — a distinction the per-goal detail cannot draw,
	// because "induction did not discharge" looks the same whether the goal is hard
	// or a dependency's lemma is simply missing (stats-consumer, demand 1).
	//
	// Restricted to the unproven goals' OWN vocabulary, which is what makes the note
	// honest: once the dependencies a stuck goal reaches are all proven, the note
	// falls silent even though the goal is still unproven — correctly reporting that
	// what remains is an intrinsic wall (e.g. a nonlinear obligation), not a missing
	// lemma. It is also silent when every dependency is proven, so it never fires as
	// noise in the settled corpus.
	if len(unprovenPIs) > 0 {
		footprints := make([]map[string]bool, 0, len(unprovenPIs))
		hinted := map[string]bool{} // "depHash#propIdx" a stuck goal hints in — admitted regardless of footprint
		for _, pi := range unprovenPIs {
			footprints = append(footprints, goalFootprint(st, h, d, &d.Props[pi]))
			for _, hr := range m.Hints[pi] {
				hinted[fmt.Sprintf("%s#%d", hr.Def, hr.Prop)] = true
			}
		}
		if deps := unprovenDepLemmas(st, d, footprints, hinted); len(deps) > 0 {
			one := len(deps) == 1
			fmt.Fprintf(&b, "note: %d reused dependenc%s not fully proven, so %s laws are absent from the lemma\n"+
				"      library above — proving %s may let a stuck goal here discharge: %s\n",
				len(deps), map[bool]string{true: "y is", false: "ies are"}[one],
				map[bool]string{true: "its", false: "their"}[one],
				map[bool]string{true: "it", false: "them"}[one],
				strings.Join(deps, ", "))
		}
	}

	m.Guarantee.Proven = proven
	m.ProvenProps = provenIdx
	// Set the level consistently from the CURRENT result — promote and demote.
	// A `proven` level must never outlive the proofs that justified it: an
	// existing `proven` from a prior kernel (e.g. before the non-total axiom
	// gate) that now proves fewer than all properties must fall back to its
	// underlying tested level, or the store would advertise `proven` with an
	// incomplete ProvenProps set.
	allProven := len(d.Props) > 0 && proven == len(d.Props) && !anyRefuted
	// An `asserted` level reached through INDETERMINACY (SPEC §4.1) is
	// promotable. Its properties exist and none was refuted — the tester simply
	// could not evaluate them — and a proof is exactly the instrument that does
	// not care whether a generated case ran out of fuel. Excluding it would make
	// `spin`-shaped definitions unprovable in principle: the tester can never
	// lift them to `tested`, so a complete proof would have nowhere to land.
	// A bare `asserted` (no properties at all) is NOT promotable, and cannot be:
	// allProven requires len(d.Props) > 0.
	switch {
	case allProven && promotableToProven(m.Guarantee):
		m.Guarantee.Level = "proven"
	case !allProven && m.Guarantee.Level == "proven":
		// Demote to whatever the TESTER could actually establish. A definition
		// promoted from indeterminacy must fall back to `asserted`, not to
		// `tested`: no case ever passed, so claiming a case count on the way
		// down would invent evidence the demotion itself is admitting is absent.
		if len(m.Guarantee.Indeterminate) > 0 {
			m.Guarantee.Level = "asserted"
			m.Guarantee.Cases = 0
		} else {
			m.Guarantee.Level = "tested"
			if m.Guarantee.Cases == 0 {
				m.Guarantee.Cases = propCases
			}
		}
	}
	if err := st.SetMeta(h, m); err != nil {
		return "", err
	}
	return b.String(), nil
}

func cmdProve(st *Store, name string) {
	out, err := apiProve(st, name)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

// loadLemmaLibrary populates c.lemmas exactly as apiProve does: proven
// properties of transitive dependencies, then the definition's own
// previously-proven properties (tagged by index). Shared by apiProve and
// the script-hash fixture generator so both see identical libraries.
// goalPI is the property this library is being loaded FOR: author hints are
// admitted only for that goal, so hinting one property leaves its siblings'
// scripts byte-identical. Pass -1 when no goal is in scope (bookkeeping counts),
// which admits no hints. Scoping matters beyond tidiness: candidate translation
// accumulates declarations and defining-equation axioms into the context, so an
// unscoped hint would leak its target's declarations into every sibling's
// script — precisely the acquisition-history dependence the fresh-context-per-
// attempt rule exists to eliminate (the script must be a function of (goal,
// lemma set) alone).
// unprovenDepLemmas walks the same transitive dependency closure loadLemmaLibrary
// draws lemmas from, and names the dependency definitions carrying properties that
// are NOT proven — laws that WOULD be admitted as lemmas if those dependencies were
// proven, and are silently absent because they are not.
//
// This closes the diagnostic gap the stats-consumer round measured (demand 1): the
// prover's "induction did not discharge" reads identically whether a goal is
// intrinsically hard or whether the consumer reused a `tested` dependency and so
// lost its laws. Only the second case is repairable by the reader, and only this
// tells them so. It returns descriptors in the same canonical (dep-hash) order the
// lemma walk uses, so the note is deterministic; empty when every reused dependency
// is fully proven — which is the corpus's normal state, so the note never fires as
// noise there.
//
// `footprints` are the relevance sets (goalFootprint) of the UNPROVEN goals. A
// dependency law is a candidate only if, WERE it proven, the prover would actually
// admit it for one of those goals — i.e. its mentions lie within some stuck goal's
// footprint (lemmaAdmissible, exactly as the real lemma walk applies it). This is
// what keeps the note honest in both directions: it does not name a tested
// definition no stuck goal reaches, and once the laws a goal COULD use are all
// proven it falls silent even though the goal stays unproven — correctly reporting
// an intrinsic wall (a nonlinear obligation) rather than a missing lemma. It is
// therefore silent whenever every admissible dependency law is proven, which is the
// settled corpus's normal state.
func unprovenDepLemmas(st *Store, d *Def, footprints []map[string]bool, hinted map[string]bool) []string {
	if len(footprints) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var queue []string
	enqueue := func(hh string) {
		if !seen[hh] {
			seen[hh] = true
			queue = append(queue, hh)
		}
	}
	for _, dep := range sortedDepHashes(d) {
		enqueue(dep)
	}
	// A hint can admit a lemma from a definition OUTSIDE the goal's dependency
	// closure (that is the whole point of a hint), so a hinted-but-regressed law
	// would never be reached by the closure walk alone. Seed the traversal with the
	// hinted definitions too, matching loadLemmaLibrary's out-of-closure hint route.
	// Sorted first: map iteration order is randomized, and it would otherwise leak
	// into the queue and the report order — this diagnostic must be deterministic.
	hintedHashes := make([]string, 0, len(hinted))
	for key := range hinted {
		if dh, _, ok := strings.Cut(key, "#"); ok {
			hintedHashes = append(hintedHashes, dh)
		}
	}
	sort.Strings(hintedHashes)
	for _, dh := range hintedHashes {
		enqueue(dh)
	}
	var out []string
	for qi := 0; qi < len(queue); qi++ {
		dh := queue[qi]
		dd, err := st.GetDef(dh)
		if err != nil {
			continue
		}
		for _, dep := range sortedDepHashes(dd) {
			enqueue(dep)
		}
		if dd.K != "func" || len(dd.Props) == 0 {
			continue
		}
		dm, err := st.GetMeta(dh)
		if err != nil {
			continue
		}
		proven := map[int]bool{}
		for _, pi := range dm.ProvenProps {
			proven[pi] = true
		}
		latent := 0
		for pi := range dd.Props {
			// A falsified property is not a latent lemma — proving it is impossible, so
			// naming it would send the reader after a law that cannot exist. refutedContains
			// excludes TESTER refutations, the only per-property refutation the store
			// persists, and it does so per index without over-excluding an unproven sibling
			// that merely SHARES a name with a falsified one.
			//
			// Two refutations it cannot exclude, both left deliberately because the miss is
			// SELF-CORRECTING for a hint: (a) a property the tester's cases missed but SMT
			// would refute, and (b) one of two same-NAMED siblings being falsified, where the
			// name alone cannot say which — refutedContains conservatively reports neither, so
			// the false one may still be counted. In both a reader who follows the hint and
			// runs `oath prove` on the dependency meets the refutation directly, learning the
			// reused law is broken. Over-including a self-revealing dead end is the right side
			// to err on for a "may help" suggestion; under-reporting a genuinely provable law
			// would not be.
			if proven[pi] || refutedContains(dm, pi, len(dd.Props)) {
				continue
			}
			// Would this law, once proven, actually be ADMITTED for a stuck goal? The
			// prover admits a dependency lemma either when its mentions lie within the
			// goal's footprint (lemmaAdmissible) or when the goal explicitly HINTS it
			// (hintedLemma, which bypasses the footprint filter). A law that satisfies
			// neither would never enter any stuck goal's script, so proving it cannot
			// help and the note must not claim it might. Both admission routes are
			// mirrored here so the note names exactly the dependencies whose proving
			// could matter.
			//
			// One approximation, deliberately left: loadLemmaLibrary also DROPS a
			// candidate whose formula does not translate to SMT (formulaWith errors),
			// which this does not re-check — attempting translation needs a full solver
			// context per candidate, disproportionate for a hint. The effect is bounded
			// and self-correcting: at worst the count includes an untranslatable law of a
			// dependency that has other useful ones, and a reader who runs `oath prove`
			// on the named dependency sees that law stay unproven directly.
			cand := lemma{defHash: dh, mentions: propMentions(dh, &dd.Props[pi])}
			admissible := hinted[fmt.Sprintf("%s#%d", dh, pi)]
			for _, fp := range footprints {
				if admissible {
					break
				}
				admissible = lemmaAdmissible(&cand, "", fp)
			}
			if admissible {
				latent++
			}
		}
		if latent == 0 {
			continue
		}
		// The reader is going to type `oath prove <name>`, which resolves through the
		// name index — so the note may only name a dependency by a name that RESOLVES to
		// THIS exact object now. NameOf gives the best live name; if it resolves back to
		// dh, it is actionable. If it does not (the name was repointed away and this
		// object is superseded), there is no name the reader could prove, so the entry is
		// omitted rather than printed as a reference `oath prove` would reject. This also
		// subsumes the earlier hazard of naming a repointed object's stale name — that
		// name resolves to a DIFFERENT object and is likewise rejected here.
		name := st.NameOf(dh)
		if resolved, ok := st.Resolve(name); !ok || resolved != dh {
			continue
		}
		suffix := "s"
		if latent == 1 {
			suffix = ""
		}
		out = append(out, fmt.Sprintf("%s (%d unproven law%s)", name, latent, suffix))
	}
	return out
}

func loadLemmaLibrary(c *smtCtx, st *Store, d *Def, h string, m *Meta, goalPI int) (int, int) {
	// Collect every candidate lemma (transitive-dependency proven props plus
	// the definition's own recorded proven props), then TRANSLATE in
	// canonical ascending (definition-hash, property-index) order — the same
	// order they are emitted in. Translation order determines first-touch
	// declaration/axiom accumulation, so it is part of script identity and
	// must be canonical, not traversal-dependent.
	type cand struct {
		dh string
		pi int
	}
	var cands []cand
	seen := map[string]bool{h: true}
	queue := []string{}
	for _, dep := range sortedDepHashes(d) {
		if !seen[dep] {
			seen[dep] = true
			queue = append(queue, dep)
		}
	}
	for qi := 0; qi < len(queue); qi++ {
		dh := queue[qi]
		dd, err := st.GetDef(dh)
		if err != nil {
			continue
		}
		for _, dep := range sortedDepHashes(dd) {
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
		if dd.K != "func" {
			continue
		}
		dm, err := st.GetMeta(dh)
		if err != nil {
			continue
		}
		for _, pi := range dm.ProvenProps {
			if pi >= 0 && pi < len(dd.Props) {
				cands = append(cands, cand{dh, pi})
			}
		}
	}
	ownStart := len(cands)
	for _, pi := range m.ProvenProps {
		if pi >= 0 && pi < len(d.Props) {
			cands = append(cands, cand{h, pi})
		}
	}
	ownCount := len(cands) - ownStart
	// Author hints (#67): admit named proven properties even when they lie
	// OUTSIDE the goal's dependency closure (the relevance filter would never
	// surface them). Only a currently-PROVEN target is admitted — an inert hint
	// (unproven, falsified, or missing target) is silently skipped, so the
	// admission stays sound: the prover only ever asserts already-proven facts.
	if goalPI >= 0 && len(m.Hints[goalPI]) > 0 {
		inCands := map[string]bool{}
		for _, cd := range cands {
			inCands[fmt.Sprintf("%s#%d", cd.dh, cd.pi)] = true
		}
		{
			refs := m.Hints[goalPI]
			for _, hr := range refs {
				key := fmt.Sprintf("%s#%d", hr.Def, hr.Prop)
				if inCands[key] {
					continue
				}
				hd, err := st.GetDef(hr.Def)
				if err != nil || hd.K != "func" || hr.Prop < 0 || hr.Prop >= len(hd.Props) {
					continue
				}
				hm, err := st.GetMeta(hr.Def)
				if err != nil {
					continue
				}
				proven := false
				for _, ppi := range hm.ProvenProps {
					if ppi == hr.Prop {
						proven = true
						break
					}
				}
				if !proven {
					continue
				}
				inCands[key] = true
				cands = append(cands, cand{hr.Def, hr.Prop})
			}
		}
	}
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].dh != cands[b].dh {
			return cands[a].dh < cands[b].dh
		}
		return cands[a].pi < cands[b].pi
	})
	added := 0
	for _, cd := range cands {
		dd, err := st.GetDef(cd.dh)
		if err != nil {
			continue
		}
		own := -1
		if cd.dh == h {
			own = cd.pi
		}
		f, err := c.formulaWith(dd, cd.dh, &dd.Props[cd.pi], nil)
		if err != nil {
			continue
		}
		c.lemmas = append(c.lemmas, lemma{ownIdx: own, propIdx: cd.pi, defHash: cd.dh, mentions: propMentions(cd.dh, &dd.Props[cd.pi]),
			text: "(assert " + f + ")"})
		added++
	}
	return added - ownCount, ownCount
}

// directAttemptScript reproduces, byte-for-byte, the direct-attempt script
// proveOne emits for property pi given the store's recorded lemma state.
// Fixtured as sha256 per property (SPEC §7.2 script stability): a conforming
// kernel must emit these exact bytes, which pins the naming scheme, lemma
// order, and translation without prose ambiguity.
func directAttemptScript(st *Store, h string, pi int) (string, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if pi < 0 || pi >= len(d.Props) {
		return "", fmt.Errorf("no property %d", pi)
	}
	c := newSmtCtx(st, d, h)
	loadLemmaLibrary(c, st, d, h, m, pi)
	p := &d.Props[pi]
	footprint := goalFootprint(c.st, h, d, p)
	var binderDecls []string
	consts := map[int]string{}
	for i := range p.Binders {
		srt, err := c.sortOf(&p.Binders[i])
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("b%d", i)
		binderDecls = append(binderDecls, fmt.Sprintf("(declare-const %s %s)", name, srt))
		consts[i] = name
	}
	goal, err := c.formulaWith(d, h, p, consts)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, x := range c.decls {
		b.WriteString(x + "\n")
	}
	for _, x := range c.axioms {
		b.WriteString(x + "\n")
	}
	var adm []*lemma
	for i := range c.lemmas {
		if c.lemmas[i].ownIdx != pi && (lemmaAdmissible(&c.lemmas[i], h, footprint) || hintedLemma(m, pi, &c.lemmas[i])) {
			adm = append(adm, &c.lemmas[i])
		}
	}
	sort.Slice(adm, func(a, b2 int) bool {
		if adm[a].defHash != adm[b2].defHash {
			return adm[a].defHash < adm[b2].defHash
		}
		return adm[a].ownIdx < adm[b2].ownIdx
	})
	for _, l := range adm {
		b.WriteString(l.text + "\n")
	}
	for _, x := range binderDecls {
		b.WriteString(x + "\n")
	}
	fmt.Fprintf(&b, "(assert (not %s))\n(check-sat)\n", goal)
	if !c.quantified {
		b.WriteString("(get-model)\n")
	}
	return b.String(), nil
}

// resolvePropIdx maps a property spec — a name or a 0-based index — to its index
// within a definition's props. Names resolve through the metadata's PropNames.
func resolvePropIdx(m *Meta, nprops int, spec string) (int, error) {
	for i := 0; i < nprops && i < len(m.PropNames); i++ {
		if m.PropNames[i] == spec {
			return i, nil
		}
	}
	if n, err := strconv.Atoi(spec); err == nil {
		if n < 0 || n >= nprops {
			return 0, fmt.Errorf("property index %d out of range (0..%d)", n, nprops-1)
		}
		return n, nil
	}
	return 0, fmt.Errorf("no property named %q", spec)
}

// apiHint records an author-supplied proof hint (#67): for property `propSpec` of
// `name`, admit proven properties of `lemmaSpec` as lemmas, bypassing the §7.2
// relevance filter (a performance gate, never a soundness gate). Only PROVEN
// targets are ever admitted, so a hint can help discharge a goal but never make
// a false one provable. `lemmaSpec` is "<lemma>" (all its currently-proven props)
// or "<lemma>.<prop>" (one property, by name or index). Identity-neutral: hints
// live in metadata keyed to this object's hash, never in its AST.
func apiHint(st *Store, name, propSpec, lemmaSpec string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	pi, err := resolvePropIdx(m, len(d.Props), propSpec)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}

	lname, lpropSpec := lemmaSpec, ""
	if i := strings.LastIndex(lemmaSpec, "."); i >= 0 {
		lname, lpropSpec = lemmaSpec[:i], lemmaSpec[i+1:]
	}
	lh, ok := st.Resolve(lname)
	if !ok {
		return "", fmt.Errorf("no definition named %q", lname)
	}
	if lh == h {
		return "", fmt.Errorf("a definition cannot hint its own properties — siblings are already admissible")
	}
	ld, err := st.GetDef(lh)
	if err != nil {
		return "", err
	}
	lm, err := st.GetMeta(lh)
	if err != nil {
		return "", err
	}
	provenSet := map[int]bool{}
	for _, ppi := range lm.ProvenProps {
		provenSet[ppi] = true
	}
	var targets []int
	if lpropSpec == "" {
		targets = append(targets, lm.ProvenProps...)
		sort.Ints(targets)
		if len(targets) == 0 {
			return "", fmt.Errorf("%s has no proven properties to admit — prove it first", lname)
		}
	} else {
		lpi, err := resolvePropIdx(lm, len(ld.Props), lpropSpec)
		if err != nil {
			return "", fmt.Errorf("%s: %w", lname, err)
		}
		if !provenSet[lpi] {
			return "", fmt.Errorf("%s.%s is not proven — only proven properties may be admitted as hints", lname, metaPropName(lm, lpi))
		}
		targets = []int{lpi}
	}

	if m.Hints == nil {
		m.Hints = map[int][]HintRef{}
	}
	added := 0
	for _, lpi := range targets {
		dup := false
		for _, hr := range m.Hints[pi] {
			if hr.Def == lh && hr.Prop == lpi {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		m.Hints[pi] = append(m.Hints[pi], HintRef{Def: lh, Prop: lpi})
		added++
	}
	if added == 0 {
		return fmt.Sprintf("no new hints for %s.%s (already present)\n", name, metaPropName(m, pi)), nil
	}
	sort.Slice(m.Hints[pi], func(a, b int) bool {
		if m.Hints[pi][a].Def != m.Hints[pi][b].Def {
			return m.Hints[pi][a].Def < m.Hints[pi][b].Def
		}
		return m.Hints[pi][a].Prop < m.Hints[pi][b].Prop
	})
	if err := st.SetMeta(h, m); err != nil {
		return "", err
	}
	return fmt.Sprintf("+ hint  %s.%s ← %s  (%d lemma%s admitted); re-run `oath prove %s`\n",
		name, metaPropName(m, pi), lemmaSpec, added, plural(added), name), nil
}

// apiHintList reports the hints recorded on a definition, resolving lemma hashes
// back to names, and flags any that reference a now-unproven target (inert).
func apiHintList(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if len(m.Hints) == 0 {
		return fmt.Sprintf("%s: no hints\n", name), nil
	}
	pis := make([]int, 0, len(m.Hints))
	for pi := range m.Hints {
		pis = append(pis, pi)
	}
	sort.Ints(pis)
	var b strings.Builder
	fmt.Fprintf(&b, "hints for %s:\n", name)
	for _, pi := range pis {
		for _, hr := range m.Hints[pi] {
			lname := st.NameOf(hr.Def)
			if lname == "" {
				lname = shortHash(hr.Def)
			}
			status := ""
			if lm, err := st.GetMeta(hr.Def); err == nil {
				proven := false
				for _, ppi := range lm.ProvenProps {
					if ppi == hr.Prop {
						proven = true
						break
					}
				}
				if !proven {
					status = "  ⚠ target not proven — inert"
				}
			} else {
				status = "  ⚠ target missing — inert"
			}
			fmt.Fprintf(&b, "  %s ← %s.%d%s\n", metaPropName(m, pi), lname, hr.Prop, status)
		}
	}
	return b.String(), nil
}

// apiHintClear removes hints. propSpec == "" clears every hint on the definition;
// otherwise it clears only the named/indexed property's hints.
func apiHintClear(st *Store, name, propSpec string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		return "", err
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return "", err
	}
	if len(m.Hints) == 0 {
		return fmt.Sprintf("%s: no hints to clear\n", name), nil
	}
	if propSpec == "" {
		m.Hints = nil
		if err := st.SetMeta(h, m); err != nil {
			return "", err
		}
		return fmt.Sprintf("cleared all hints on %s; re-run `oath prove %s`\n", name, name), nil
	}
	pi, err := resolvePropIdx(m, len(d.Props), propSpec)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if _, ok := m.Hints[pi]; !ok {
		return fmt.Sprintf("%s.%s: no hints\n", name, metaPropName(m, pi)), nil
	}
	delete(m.Hints, pi)
	if len(m.Hints) == 0 {
		m.Hints = nil
	}
	if err := st.SetMeta(h, m); err != nil {
		return "", err
	}
	return fmt.Sprintf("cleared hints on %s.%s; re-run `oath prove %s`\n", name, metaPropName(m, pi), name), nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
