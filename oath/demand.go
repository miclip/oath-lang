package main

// Demand telemetry (#75): what agents looked for and did not find.
//
// The coverage programme is gated on this deliberately. A proof system left to
// its own momentum grows along the axis where proving SUCCEEDS — pure algorithms
// — while demand sits on the axis where it is hard. Without a demand signal,
// "coverage" means building what is buildable, which looks like progress and is
// the exact failure the applicability inversion names. So: coverage grows when
// someone tried and failed to find something, not when someone had an idea.
//
// WHAT IS RECORDED, and why so little:
//
//   - the PROPERTY CONTENT HASH of the unmatched query — a structural
//     fingerprint, already the key the corpus is indexed by, containing no names
//     and no prose;
//   - the GENERALIZED TYPE SIGNATURE of the sought function, for reading the
//     record back as a coverage request;
//   - a count, and day-resolution first/last seen.
//
// WHAT IS NOT RECORDED, and this is the load-bearing part:
//
//   - the query SOURCE. It is spec-space, but it is text an agent authored.
//   - the property NAME. Agents choose these, and an agent projecting a task
//     writes what the task is about: `verify-webhook-signature`, not `p1`. A
//     name is prose wearing a schema, so it is dropped.
//   - the PRINCIPAL. No signal is ever associated with who sent it.
//   - exact timestamps. Day resolution only, so a record cannot be correlated
//     against a request log to re-identify a session.
//
// The privacy guarantee this preserves is structural rather than promised: the
// registry receives spec shapes because that is all a client has reason to send
// (DESIGN.md), and now it retains strictly less than it receives.

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// demandKey is a reserved (non-hash) meta key, like proofFixpointKey. It never
// collides with a real object.
const demandKey = "discovery-demand"

// demandFloor is the frequency below which a record is never REPORTED. One
// unusual query from one agent is not a demand signal, and surfacing it would
// turn a coverage instrument into a record of what a single caller wanted —
// which is the surveillance shape this design exists to avoid. Entries below the
// floor are still counted, because you cannot know a count without counting.
const demandFloor = 3

type demandRecord struct {
	PropHash  string `json:"prop_hash"` // generalized property content hash
	Signature string `json:"signature"` // generalized type signature of the sought function
	Count     int    `json:"count"`
	FirstSeen string `json:"first_seen"` // YYYY-MM-DD
	LastSeen  string `json:"last_seen"`
}

func loadDemand(st *Store) map[string]*demandRecord {
	out := map[string]*demandRecord{}
	if b, ok, err := st.be.getMeta(demandKey); err == nil && ok {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// recordMiss notes that a spec query found nothing. Called only on a genuine
// no-match: a query that matched, or that failed to elaborate, is not a demand
// signal — the first found what it wanted and the second never asked.
func recordMiss(st *Store, propHash, signature string, now time.Time) {
	if propHash == "" {
		return
	}
	day := now.UTC().Format("2006-01-02")
	d := loadDemand(st)
	r, ok := d[propHash]
	if !ok {
		r = &demandRecord{PropHash: propHash, Signature: signature, FirstSeen: day}
		d[propHash] = r
	}
	r.Count++
	r.LastSeen = day
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return
	}
	_ = st.be.putMeta(demandKey, b)
}

// cmdDemand reports the coverage requests that have crossed the floor, commonest
// first. This is the input #75 needs to be demand-led rather than a guess.
func cmdDemand(st *Store, showAll bool) {
	d := loadDemand(st)
	recs := make([]*demandRecord, 0, len(d))
	suppressed := 0
	for _, r := range d {
		if r.Count < demandFloor && !showAll {
			suppressed++
			continue
		}
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Count != recs[j].Count {
			return recs[i].Count > recs[j].Count
		}
		return recs[i].PropHash < recs[j].PropHash
	})
	if len(recs) == 0 {
		fmt.Printf("no coverage requests at or above the floor (%d)\n", demandFloor)
	}
	for _, r := range recs {
		fmt.Printf("%4d×  #%s  %s   (%s → %s)\n",
			r.Count, shortHash(r.PropHash), r.Signature, r.FirstSeen, r.LastSeen)
	}
	if suppressed > 0 {
		fmt.Printf("\n%d record(s) below the reporting floor of %d, counted but not shown:\n", suppressed, demandFloor)
		fmt.Printf("a single unusual query is not a demand signal, and surfacing it would make\n")
		fmt.Printf("this a record of what one caller wanted. Use --all to see them anyway.\n")
	}
}
