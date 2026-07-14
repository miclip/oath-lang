package main

import (
	"fmt"
	"sort"
	"strings"
)

// String-returning implementations of every verb, shared by the CLI (which
// prints them) and the MCP server (which returns them as tool results).
// None of these exit the process; errors come back as errors.

// apiPut elaborates, gates, stores, verifies, and journals every form in
// src. It stops at the first rejection or elaboration error; results
// accumulated so far are returned alongside any error.
func apiPut(st *Store, src string, author string) ([]putReport, error) {
	if author == "" {
		author = "unattributed"
	}
	forms, err := parseForms(src)
	if err != nil {
		return nil, err
	}
	var results []putReport
	for _, f := range forms {
		if f.K != "list" || len(f.Kids) == 0 || f.Kids[0].K != "sym" {
			return results, fmt.Errorf("line %d: top-level forms must be (data ...) or (defn ...)", f.Line)
		}
		formName := "?"
		if len(f.Kids) >= 2 && f.Kids[1].K == "sym" {
			formName = f.Kids[1].Sym
		}
		var def *Def
		var meta *Meta
		switch f.Kids[0].Sym {
		case "data":
			def, meta, err = elabData(st, f)
		case "defn":
			def, meta, err = elabFunc(st, f)
		default:
			err = fmt.Errorf("line %d: unknown top-level form %q", f.Line, f.Kids[0].Sym)
		}
		if err != nil {
			_ = st.AppendLog(&LogEntry{Author: author, Name: formName, Status: "rejected", Error: err.Error()})
			return results, err
		}
		meta.Author = author

		// The kernel gate: nothing enters the codebase without typechecking.
		// Rejections store no object, but the journal retains the attempt.
		if err := checkDef(st, def); err != nil {
			_ = st.AppendLog(&LogEntry{Author: author, Name: meta.Name, Kind: def.K, Status: "rejected", Error: err.Error()})
			results = append(results, putReport{Name: meta.Name, Kind: def.K, Status: "rejected", Error: err.Error()})
			return results, nil
		}

		h, prev, err := st.Put(def, meta)
		if err != nil {
			return results, err
		}

		rep := putReport{Name: meta.Name, Hash: h, Kind: def.K, Status: "accepted", Prev: prev, Ctors: len(def.Ctors)}
		if def.K == "func" {
			reports, err := verifyDef(st, h)
			if err != nil {
				return results, err
			}
			m, _ := st.GetMeta(h)
			m.Termination = terminationOf(st, def)
			m.Confinement = confinementOf(st, def)
			if err := st.SetMeta(h, m); err != nil {
				return results, err
			}
			rep.Guarantee = guaranteeString(m.Guarantee)
			rep.Termination = m.Termination
			rep.Confinement = confinementString(m)
			if m.Guarantee.Level == "falsified" {
				rep.Status = "falsified"
			}
			for _, r := range reports {
				rep.Props = append(rep.Props, propJSON{
					Name: r.Name, Passed: r.Passed, Failed: r.Failed,
					Counterexample: r.Counter, Error: r.Err,
				})
			}
		}
		_ = st.AppendLog(&LogEntry{
			Author: author, Name: meta.Name, Kind: def.K, Status: rep.Status,
			Hash: h, Prev: prev, Guarantee: rep.Guarantee, Termination: rep.Termination,
		})
		results = append(results, rep)
	}
	return results, nil
}

// renderPutReports formats put results the way the CLI prints them.
func renderPutReports(results []putReport) string {
	var b strings.Builder
	for _, rep := range results {
		status := ""
		if rep.Prev != "" {
			status = fmt.Sprintf("  (name repointed; old version %s remains immutable)", shortHash(rep.Prev))
		}
		switch {
		case rep.Status == "rejected":
			fmt.Fprintf(&b, "✗ %-16s REJECTED: %s\n", rep.Name, rep.Error)
		case rep.Kind == "data":
			fmt.Fprintf(&b, "✓ %-16s #%s  data (%d constructors)%s\n", rep.Name, shortHash(rep.Hash), rep.Ctors, status)
		default:
			mark := "✓"
			if rep.Status == "falsified" {
				mark = "✗"
			}
			suffix := ""
			switch rep.Termination {
			case "structural", "nonrecursive":
				suffix = " · total"
			case "unknown":
				suffix = " · termination unproven"
			}
			fmt.Fprintf(&b, "%s %-16s #%s  %s%s%s\n", mark, rep.Name, shortHash(rep.Hash), rep.Guarantee, suffix, status)
			if rep.Confinement != "" {
				fmt.Fprintf(&b, "    capabilities: %s\n", rep.Confinement)
			}
			for _, r := range rep.Props {
				if r.Failed {
					fmt.Fprintf(&b, "    prop %-24s FALSIFIED after %d cases\n", r.Name, r.Passed)
					fmt.Fprintf(&b, "      counterexample: %s\n", r.Counterexample)
				} else if r.Error != "" {
					fmt.Fprintf(&b, "    prop %-24s ERROR: %s\n", r.Name, r.Error)
				} else {
					fmt.Fprintf(&b, "    prop %-24s passed %d cases\n", r.Name, r.Passed)
				}
			}
		}
	}
	return b.String()
}

func apiLs(st *Store) string {
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		h := names[k]
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil {
			continue
		}
		kind := "func"
		g := guaranteeString(m.Guarantee) + termSuffix(m)
		if d.K == "data" {
			kind = "data"
			g = fmt.Sprintf("%d constructors", len(d.Ctors))
		}
		fmt.Fprintf(&b, "%-16s #%s  %-5s %s\n", k, shortHash(h), kind, g)
	}
	return b.String()
}

func apiGet(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	return printDef(st, h)
}

func apiEval(st *Store, src string) (string, error) {
	forms, err := parseForms(src)
	if err != nil {
		return "", err
	}
	if len(forms) != 1 {
		return "", fmt.Errorf("eval expects exactly one expression")
	}
	e := &elab{st: st}
	term, err := e.elabTerm(forms[0])
	if err != nil {
		return "", err
	}
	c := &checker{st: st}
	ty, err := c.synth(nil, term)
	if err != nil {
		return "", err
	}
	ev := &evaluator{st: st, fuel: propFuel}
	v, err := ev.eval(nil, "", term)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s : %s", printValue(st, v), printTy(st, ty, nil)), nil
}

func apiVerify(st *Store, name string) (string, error) {
	h, ok := st.Resolve(name)
	if !ok {
		return "", fmt.Errorf("no definition named %q", name)
	}
	reports, err := verifyDef(st, h)
	if err != nil {
		return "", err
	}
	if len(reports) == 0 {
		return fmt.Sprintf("%s has no properties; guarantee remains: asserted", name), nil
	}
	return renderVerifyReports(reports), nil
}

// renderVerifyReports formats property verdicts identically for the CLI and for
// conformance fixtures.
func renderVerifyReports(reports []PropReport) string {
	var b strings.Builder
	for _, r := range reports {
		if r.Failed {
			fmt.Fprintf(&b, "✗ prop %-24s FALSIFIED after %d cases\n    counterexample: %s\n", r.Name, r.Passed, r.Counter)
		} else if r.Err != "" {
			fmt.Fprintf(&b, "✗ prop %-24s ERROR: %s\n", r.Name, r.Err)
		} else {
			fmt.Fprintf(&b, "✓ prop %-24s passed %d cases\n", r.Name, r.Passed)
		}
	}
	return b.String()
}

func apiLog(st *Store, filter string) string {
	entries := st.ReadLog()
	if len(entries) == 0 {
		return "journal is empty"
	}
	var b strings.Builder
	for _, e := range entries {
		if filter != "" && e.Name != filter {
			continue
		}
		mark := "✓"
		detail := e.Guarantee
		if e.Termination == "structural" || e.Termination == "nonrecursive" {
			detail += " · total"
		}
		switch e.Status {
		case "rejected":
			mark = "✗"
			detail = e.Error
		case "falsified":
			mark = "✗"
		}
		h := ""
		if e.Hash != "" {
			h = "#" + shortHash(e.Hash)
		}
		if e.Prev != "" {
			h += " (was #" + shortHash(e.Prev) + ")"
		}
		fmt.Fprintf(&b, "%-4d %s  %-20s %s %-10s %-16s %s  %s\n",
			e.Seq, e.Time, e.Author, mark, e.Status, e.Name, h, detail)
	}
	return b.String()
}
