package main

// THE PRODUCER for scripts/prove-reasons.py's JSONL modes (#68).
//
// WHAT IT MEASURES, AND WHY THAT IS NOT THE NINE-HOUR SWEEP. `prove-reasons.py`
// specifies a record format and states that no producer is committed: the one
// that existed drove the full-budget re-derivation and was deleted, because a
// nine-hour instrument is not one this repository can offer. This is the bounded
// producer that file names — the same seams, at a budget the caller pins, so a
// single command answers "why is this property not proven" and every figure it
// feeds carries the budget it was measured at.
//
// A BUDGET IS PART OF THE CLAIM. OATH_PROVE_RLIMIT sets effectiveRlimit(), and
// directRlimit()/lemmaFreeRlimit() clamp to it, so one variable puts EVERY
// strategy at one budget and the record's `rlimit` describes all of them. An
// `unknown` here says the solver did not converge within that budget and says
// nothing about what it would do with more — which is why the report refuses to
// render without one.
//
// THE UNIVERSE IS DERIVED, NEVER LISTED, AND IT IS THE CENSUS'S WHOLE
// NON-PROVEN SET. It comes from the committed store's own metadata: every
// property no live object records as proven. That is exactly the population
// `prove-reasons.py`'s check() reconciles against, and anything narrower is
// refused there — a report that cannot state its universe does not render.
//
// A large minority of that set emits NO candidate script: the strategy sequence
// bails before its first solver call, so those properties cost no solver time
// and land in `translation-bail` with an empty attempt list, which is the one
// status the format declares legal with zero telemetry. THAT SPLIT IS A FACT
// ABOUT THE GOALS, NOT ABOUT THIS SWEEP'S COVERAGE — the sweep covers all of
// them, and the ones with no attempts were not skipped, not truncated and not
// budget-limited; their goals were untranslatable. Reporting the split as
// coverage would turn a property of the corpus into an apology for the
// instrument.
//
// Whether a property emits a script is reported per record and comes from
// scriptAttempts — the prover's own enumeration seam, the same code path that
// produces fixtures/prove/attempts.txt — rather than from a reading of the
// fixture, so the derived artefact never stands between the claim and its
// population.
//
// THE CONTROL IS THE SCRIPT HASH. Each property's direct-attempt script is
// sha256'd AS SENT TO THE SOLVER and compared to that property's row in
// fixtures/prove/scripts.txt. That match is what makes a record a measurement of
// the PINNED script rather than of some other script that happened to be built:
// §7.2 makes a script a function of (goal, recorded lemma state), so a mismatch
// is a lemma-state difference and the record is describing a different
// experiment. Mismatches are counted, reported, and their records marked — never
// averaged in as noise.
//
// IT WRITES NOTHING. The store is opened read-only through the filesystem
// backend, proofs run through proveOne directly rather than apiProve, and no
// verdict is stored. `oath fixtures` and `make verify` both mutate codebase/;
// this must not, or the corpus would move underneath the measurement.
//
//	cd oath && OATH_CENSUS_OUT=/tmp/census.json go test -run TestCorpusCensus -count=1
//	cd oath && OATH_PROVE_RLIMIT=4000000 OATH_SWEEP_OUT=/tmp/sweep.jsonl \
//	    go test -run TestProveReasonsSweep -count=1 -timeout 24h
//
// OATH_SWEEP_LIMIT=N stops after N properties (a pilot). The output is appended
// and flushed per property, so an aborted run leaves a STATED SUBSET rather than
// nothing — the file is valid JSONL at every instant, and the log says how far
// it got.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sweepAttempt is one solver attempt, in prove-reasons.py's required shape.
// `budget` is THAT ATTEMPT's rlimit and is deliberately not the record's:
// the lemma-free probe and an induction-eligible direct attempt run at their own
// reduced budgets, and reporting a cheap bail against the sweep's nominal figure
// inflates it into a spent full budget.
type sweepAttempt struct {
	Strategy string `json:"strategy"`
	Detail   string `json:"detail"`
	Verdict  string `json:"verdict"`
	Consumed int64  `json:"consumed"`
	Reason   string `json:"reason"`
	Budget   int64  `json:"budget"`
}

// sweepRecord is one property. Field names and their meanings are fixed by
// scripts/prove-reasons.py's load(); this struct is the Go side of that
// contract and every field it validates must be present.
type sweepRecord struct {
	Hash      string `json:"hash"`
	PropIndex int    `json:"prop_index"`
	Name      string `json:"name"`
	PropName  string `json:"prop_name"`
	Status    string `json:"status"`
	Method    string `json:"method,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Rlimit    int64  `json:"rlimit"`
	// HasDatatypeBinder is a POINTER because absent and false mean different
	// things to the cross-tab: absent says the direct phase never ran, false
	// says it ran and the goal had no datatype binder. A non-pointer bool would
	// report the first as the second.
	HasDatatypeBinder *bool          `json:"has_datatype_binder,omitempty"`
	Attempts          []sweepAttempt `json:"attempts"`

	// Control fields. Not read by prove-reasons.py; they are what makes the
	// record auditable after the fact.
	DirectSHA string `json:"direct_script_sha256,omitempty"`
	PinnedSHA string `json:"pinned_script_sha256,omitempty"`
	SHAMatch  *bool  `json:"pinned_script_match,omitempty"`
	// ControlRoute says HOW the compared bytes were obtained, because the two
	// routes are not equally strong evidence and merging them would overstate
	// the weaker one:
	//
	//	as-sent      the direct attempt RAN and these are the bytes handed to
	//	             z3. This witnesses the sweep itself.
	//	regenerated  the sweep short-circuited before the direct attempt (the
	//	             lemma-free probe discharged the goal), so no direct bytes
	//	             were sent. The script is rebuilt under the SAME store and
	//	             lemma state the sweep ran at. This witnesses the LEMMA
	//	             STATE — which is what a mismatch would indict — but not the
	//	             sweep's own traffic, because there was none to witness.
	//	absent       scripts.txt pins no row for this property; nothing was
	//	             compared and the control did not run.
	ControlRoute string  `json:"script_control_route,omitempty"`
	WallSeconds  float64 `json:"wall_seconds"`
}

// sweepObserver collects one property's attempts off the prover's own solve
// seam. It is deliberately dumb: it records what it is handed and refuses what
// it cannot vouch for, rather than reconstructing anything.
type sweepObserver struct {
	attempts   []sweepAttempt
	scripts    []string // parallel to attempts; the bytes actually sent
	hasDT      *bool
	staleSeq   int      // attempts whose telemetry could not be vouched for
	strategies []string // emission order, for the log
}

func (o *sweepObserver) onAttempt(strategy, detail, script, out string, capHit bool, t z3Telemetry, seqDelta int64) {
	// A seqDelta other than 1 means another goroutine published telemetry
	// between this attempt starting and finishing, so `t` may describe a
	// different goal. Counted and recorded with an empty reason rather than
	// silently attributed — a wrong budget on a `canceled` is exactly the
	// substitution the format exists to prevent.
	if seqDelta != 1 {
		o.staleSeq++
	}
	verdict := t.Verdict
	if seqDelta != 1 {
		// Fall back to what THIS attempt's own return value proves, which is
		// independent of the shared cell.
		switch {
		case capHit:
			verdict = "capHit"
		case strings.HasPrefix(out, "unsat"):
			verdict = "unsat"
		case strings.HasPrefix(out, "sat"):
			verdict = "sat"
		default:
			verdict = "unknown"
		}
	}
	o.attempts = append(o.attempts, sweepAttempt{
		Strategy: strategy, Detail: detail, Verdict: verdict,
		Consumed: t.Consumed, Reason: t.Reason, Budget: t.Budget,
	})
	o.scripts = append(o.scripts, script)
	o.strategies = append(o.strategies, strategy)
}

func (o *sweepObserver) onDatatypeBinder(v bool) { o.hasDT = &v }

// readPinnedScripts reads fixtures/prove/scripts.txt into (name, prop) -> sha.
// FAILS on a missing or empty fixture: without it every comparison below would
// report "no pinned row" and the control would pass by being absent.
func readPinnedScripts(t *testing.T, path string) map[[2]string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v — without it the script control cannot run", path, err)
	}
	out := map[[2]string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) != 3 {
			t.Fatalf("malformed row in %s: %q", path, ln)
		}
		out[[2]string{f[0], f[1]}] = f[2]
	}
	if len(out) == 0 {
		t.Fatalf("%s pinned no scripts; the control would pass vacuously", path)
	}
	return out
}

type sweepTarget struct {
	hash     string
	pi       int
	name     string // canonical name: always a live name of the object
	propName string
	nAttempt int // how many scripts the sequence can emit (from the enumerator)
}

func TestProveReasonsSweep(t *testing.T) {
	out := os.Getenv("OATH_SWEEP_OUT")
	if out == "" {
		t.Skip("set OATH_SWEEP_OUT=<path.jsonl> to run the sweep (it runs z3 and costs real time)")
	}
	// THE BUDGET MUST BE PINNED EXPLICITLY. Defaulting to proveRlimit would run
	// the nine-hour sweep from a command that looks bounded, and every figure
	// downstream would name a budget the caller never chose.
	raw := os.Getenv("OATH_PROVE_RLIMIT")
	if raw == "" {
		t.Fatal("OATH_PROVE_RLIMIT must be set: the record format requires every figure to name " +
			"its budget, and an unset budget silently means the full 400M per-goal sweep")
	}
	if err := z3Available(); err != nil {
		t.Fatalf("z3: %v", err)
	}
	rlimit := effectiveRlimit()
	// PRESENCE IS NOT ACCEPTANCE, and the difference is the whole guard.
	// effectiveRlimit() ignores a value it cannot parse or that is <= 0 and
	// falls back to proveRlimit, so `0` or `4000000x` passes a set/unset check
	// and launches the very nine-hour sweep this bound exists to prevent — a
	// typo defeating the only thing between a bounded run and a pegged machine.
	// The check is DERIVED from the authority rather than restating its parse
	// rule: ask effectiveRlimit() what it actually took, and refuse unless it
	// took THIS value. If its acceptance rule ever changes, this follows.
	if want, err := strconv.ParseInt(raw, 10, 64); err != nil || want <= 0 || rlimit != want {
		t.Fatalf("OATH_PROVE_RLIMIT=%q was NOT honoured: effectiveRlimit() returned %d. "+
			"It silently ignores an unparseable or non-positive value and falls back to the "+
			"full %d-unit budget, so this run would have swept at the normative rlimit while "+
			"every figure claimed the budget you typed.", raw, rlimit, int64(proveRlimit))
	}

	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	pinned := readPinnedScripts(t, "../fixtures/prove/scripts.txt")

	// --- the universe, derived ------------------------------------------------
	live := st.Names()
	byHash := map[string][]string{}
	for n, h := range live {
		byHash[h] = append(byHash[h], n)
	}
	hashes := make([]string, 0, len(byHash))
	for h := range byHash {
		sort.Strings(byHash[h])
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	var targets []sweepTarget
	nonProven := 0
	for _, h := range hashes {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("def %s: %v", h[:12], err)
		}
		if d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("meta %s: %v", h[:12], err)
		}
		proven := map[int]bool{}
		for _, i := range m.ProvenProps {
			proven[i] = true
		}
		for pi := range d.Props {
			if proven[pi] {
				continue
			}
			nonProven++
			// "Emits a candidate script" comes from the prover's OWN enumerator,
			// not from a reading of the fixture: the fixture is derived from this
			// function, and deriving the universe from the derived artefact would
			// put a stale copy between the claim and its population. It is
			// RECORDED, never used to exclude — see the header.
			ats, _ := scriptAttempts(st, h, pi)
			targets = append(targets, sweepTarget{
				hash: h, pi: pi, name: m.Name,
				propName: metaPropName(m, pi), nAttempt: len(ats),
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].name != targets[j].name {
			return targets[i].name < targets[j].name
		}
		return targets[i].pi < targets[j].pi
	})
	withScript := 0
	for _, tg := range targets {
		if tg.nAttempt > 0 {
			withScript++
		}
	}
	// Both numbers are reported, and the second is deliberately NOT called
	// coverage: every one of the %d is swept. The split says how many goals the
	// strategy sequence could translate at all.
	t.Logf("universe: %d non-proven properties (per-object), ALL swept. %d emit at least one "+
		"candidate script; %d emit none and reach no solver — a fact about those goals, "+
		"not a gap in this sweep", nonProven, withScript, nonProven-withScript)
	if len(targets) != nonProven {
		t.Fatalf("internal: %d targets for %d non-proven properties", len(targets), nonProven)
	}
	if len(targets) == 0 {
		t.Fatal("empty universe; a sweep over nothing is not a sweep")
	}

	// OATH_SWEEP_ONLY=<name>:<prop_index> narrows to one property. It exists to
	// COST a specific goal before committing to the whole sweep — an average over
	// a sample that excludes the heaviest goals projects a total that cannot
	// happen. Like OATH_SWEEP_LIMIT it produces a subset that does not reconcile.
	if v := os.Getenv("OATH_SWEEP_ONLY"); v != "" {
		nm, idx, ok := strings.Cut(v, ":")
		if !ok {
			t.Fatalf("OATH_SWEEP_ONLY=%q must be <name>:<prop_index>", v)
		}
		pi, err := strconv.Atoi(idx)
		if err != nil {
			t.Fatalf("OATH_SWEEP_ONLY=%q: %v", v, err)
		}
		var keep []sweepTarget
		for _, tg := range targets {
			if tg.name == nm && tg.pi == pi {
				keep = append(keep, tg)
			}
		}
		if len(keep) == 0 {
			t.Fatalf("OATH_SWEEP_ONLY=%q matched no non-proven property in the universe", v)
		}
		targets = keep
		t.Logf("PROBE: %s[%d] alone (%d enumerated attempts). A SUBSET — it does not reconcile.",
			nm, pi, keep[0].nAttempt)
	}

	limit := len(targets)
	if v := os.Getenv("OATH_SWEEP_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("OATH_SWEEP_LIMIT=%q is not a positive integer", v)
		}
		if n < limit {
			limit = n
		}
		t.Logf("PILOT: %d of %d properties. This is a SUBSET — its counts do not "+
			"reconcile against the census and must not be reported as corpus figures.", limit, len(targets))
	}

	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("creating %s: %v", out, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	matched, regenerated, mismatched, noPin, stale := 0, 0, 0, 0, 0
	start := time.Now()
	for i := 0; i < limit; i++ {
		tg := targets[i]
		d, err := st.GetDef(tg.hash)
		if err != nil {
			t.Fatalf("def %s: %v", tg.hash[:12], err)
		}
		m, err := st.GetMeta(tg.hash)
		if err != nil {
			t.Fatalf("meta %s: %v", tg.hash[:12], err)
		}

		// The SETTLED lemma state, which is the state scriptAttempts and
		// fixtures/prove/scripts.txt are both written against. proveOne is called
		// directly rather than through apiProve because apiProve runs the whole
		// two-level fixpoint over every property of the object AND writes the
		// verdicts back — this measurement must not move the corpus underneath
		// itself.
		obs := &sweepObserver{}
		c := newSmtCtx(st, d, tg.hash)
		c.observer = obs
		loadLemmaLibrary(c, st, d, tg.hash, m, tg.pi)
		// SERIALITY, ASSERTED RATHER THAN ASSUMED. The telemetry seam is a
		// package-level cell, so it is only sound while this is the ONLY prover
		// running; solve's per-attempt delta is a cheap alarm and explicitly not a
		// proof. This is the property-level version and it is strictly stronger:
		// every publication between the first and last attempt of this goal is
		// counted, so ANY foreign attempt — from a second sweep, a stray
		// goroutine, a future parallel scan — inflates the total above the number
		// of attempts the observer saw. Equality is the witness that nothing else
		// published while this property was being proved.
		seqBefore := z3Seq.Load()
		t0 := time.Now()
		o := c.proveOne(d, tg.hash, m, &d.Props[tg.pi], tg.pi)
		wall := time.Since(t0)
		if got, want := z3Seq.Load()-seqBefore, int64(len(obs.attempts)); got != want {
			t.Fatalf("%s[%d]: %d solver attempt(s) published while the observer recorded %d — "+
				"another prover is running, so every budget and reason in this sweep may belong "+
				"to a different goal; the seam requires a SERIAL producer", tg.name, tg.pi, got, want)
		}

		rec := sweepRecord{
			Hash: tg.hash, PropIndex: tg.pi, Name: tg.name, PropName: tg.propName,
			Status: o.status, Method: o.method, Detail: o.detail,
			Rlimit: rlimit, HasDatatypeBinder: obs.hasDT,
			Attempts: obs.attempts, WallSeconds: wall.Seconds(),
		}
		if rec.Attempts == nil {
			rec.Attempts = []sweepAttempt{}
		}
		stale += obs.staleSeq

		// --- the control ------------------------------------------------------
		// Prefer the bytes AS SENT. A property whose lemma-free probe discharges
		// the goal never builds a direct script, so there are no sent bytes to
		// hash — and reporting that as "no pinned row" (which an earlier version
		// of this did) states the wrong fact twice over: scripts.txt pins those
		// rows, and what is missing is the sweep's traffic, not the pin. The two
		// are separated, and the short-circuit case is controlled by REBUILDING
		// the direct script under the same lemma state.
		for j, s := range obs.strategies {
			if s != "direct" {
				continue
			}
			sum := sha256.Sum256([]byte(obs.scripts[j]))
			rec.DirectSHA = hex.EncodeToString(sum[:])
			rec.ControlRoute = "as-sent"
			break
		}
		if rec.DirectSHA == "" {
			if sc, err := directAttemptScript(st, tg.hash, tg.pi); err == nil {
				sum := sha256.Sum256([]byte(sc))
				rec.DirectSHA = hex.EncodeToString(sum[:])
				rec.ControlRoute = "regenerated"
			}
		}
		want, havePin := pinned[[2]string{tg.name, strconv.Itoa(tg.pi)}]
		switch {
		case !havePin && rec.DirectSHA == "":
			// BOTH absent is the ONLY legitimate absence, and it is exactly the
			// translation-bail shape: no direct script exists, so scripts.txt
			// pins no row for it. Nothing to compare, and nothing is claimed.
			noPin++
			rec.ControlRoute = "absent"
			t.Logf("script control did not apply to %s[%d]: no direct script and no pinned row "+
				"— the translation-bail shape", tg.name, tg.pi)
		case havePin != (rec.DirectSHA != ""):
			// ONE-SIDED IS A FAILED CONTROL, NOT AN ABSENT ONE, and folding it in
			// with the line above let the sweep exit successfully while the
			// script-state control never ran for this property. A pin with no
			// obtainable script means the goal no longer builds the script it was
			// pinned from; a script with no pin means it is not the pinned one.
			// Either way the record's budget and verdict describe a script nothing
			// here compared, which is the measurement this control exists to make.
			t.Errorf("script control is ONE-SIDED for %s[%d]: pinned row present=%v, direct "+
				"script obtainable=%v. Only BOTH-absent is legitimate (a translation bail); "+
				"one side alone means this property's script state was never checked, and its "+
				"record would report a verdict for a script nothing compared to the pin",
				tg.name, tg.pi, havePin, rec.DirectSHA != "")
			noPin++
			rec.ControlRoute = "one-sided"
		default:
			rec.PinnedSHA = want
			hit := want == rec.DirectSHA
			rec.SHAMatch = &hit
			if hit {
				matched++
				if rec.ControlRoute == "regenerated" {
					regenerated++
				}
			} else {
				mismatched++
				t.Errorf("SCRIPT MISMATCH %s[%d] (%s, route %s): got %s but scripts.txt pins %s — "+
					"§7.2 makes the script a function of (goal, recorded lemma state), so this record "+
					"describes a DIFFERENT experiment from the pinned one and is not evidence about it",
					tg.name, tg.pi, tg.propName, rec.ControlRoute, rec.DirectSHA[:16], want[:16])
			}
		}

		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshalling %s[%d]: %v", tg.name, tg.pi, err)
		}
		// FLUSHED PER PROPERTY. An aborted sweep then leaves a prefix that is
		// valid JSONL and a stated subset, rather than a truncated final line or
		// nothing at all.
		if _, err := w.Write(append(b, '\n')); err != nil {
			t.Fatalf("writing record: %v", err)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("flushing: %v", err)
		}
		if err := f.Sync(); err != nil {
			t.Fatalf("syncing: %v", err)
		}
		t.Logf("[%d/%d] %s.%s %s in %.1fs (%d attempts, enumerator said %d)",
			i+1, limit, tg.name, tg.propName, o.status, wall.Seconds(),
			len(obs.attempts), tg.nAttempt)
	}
	elapsed := time.Since(start)

	t.Logf("wrote %d record(s) to %s in %s (rlimit %d)", limit, out, elapsed.Round(time.Millisecond), rlimit)
	t.Logf("script control: %d matched (%d of them via a REGENERATED script, because the goal "+
		"proved before the direct attempt ran), %d MISMATCHED, %d not controlled",
		matched, regenerated, mismatched, noPin)
	if stale > 0 {
		t.Errorf("%d attempt(s) could not have their telemetry vouched for (seq delta != 1); "+
			"the sweep must run serially or the budgets and reasons may belong to another goal", stale)
	}
	// A control that never fired is not a passed control. If nothing matched, the
	// lookup key is wrong and every record is unvouched — which would otherwise
	// read as a clean run with an empty complaint.
	if matched == 0 {
		t.Errorf("NO property matched its pinned script; the control did not discriminate, "+
			"so nothing here is evidence about the pinned scripts (checked %d)", limit)
	}
	// Only a run that was neither narrowed NOR truncated has swept the universe.
	// Comparing limit to len(targets) alone reported "FULL universe swept" after
	// OATH_SWEEP_ONLY had already cut targets down to one — the subset redefining
	// the total it is then compared against.
	if limit == len(targets) && limit == nonProven {
		t.Logf("FULL universe swept. Reconcile with: python3 scripts/prove-reasons.py %s check <census.json>", out)
	}
	fmt.Fprintf(os.Stderr, "sweep written to %s\n", out)
}
