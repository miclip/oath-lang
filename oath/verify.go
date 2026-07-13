package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const propCases = 200
const propFuel = 2_000_000

// PropReport is the outcome of checking one property.
type PropReport struct {
	Name    string
	Passed  int    // cases that passed
	Failed  bool
	Counter string // rendered counterexample inputs, if falsified
	Err     string // generation/setup error, if any
}

// verifyDef runs every property of a function definition and records the
// resulting guarantee in its metadata. The guarantee is honest by
// construction: `tested` only ever means "these exact deterministic cases
// passed", and a falsified property downgrades the definition loudly rather
// than hiding it.
func verifyDef(st *Store, h string) ([]PropReport, error) {
	d, err := st.GetDef(h)
	if err != nil {
		return nil, err
	}
	if d.K != "func" {
		return nil, fmt.Errorf("only function definitions have properties to verify")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		return nil, err
	}

	seedBytes, _ := hex.DecodeString(h[:16])
	base := binary.BigEndian.Uint64(seedBytes)

	var reports []PropReport
	for pi := range d.Props {
		name := fmt.Sprintf("prop%d", pi)
		if pi < len(m.PropNames) {
			name = m.PropNames[pi]
		}
		reports = append(reports, runProp(st, h, &d.Props[pi], name, base, pi))
	}

	g := Guarantee{Level: "asserted"}
	if len(d.Props) > 0 {
		var falsified []string
		for _, r := range reports {
			if r.Failed || r.Err != "" {
				falsified = append(falsified, r.Name)
			}
		}
		if len(falsified) > 0 {
			g = Guarantee{Level: "falsified", Falsified: falsified}
		} else {
			g = Guarantee{Level: "tested", Cases: propCases}
		}
	}
	m.Guarantee = g
	if err := st.SetMeta(h, m); err != nil {
		return nil, err
	}
	return reports, nil
}

func runProp(st *Store, h string, p *Prop, name string, base uint64, pi int) PropReport {
	rep := PropReport{Name: name}
	for c := 0; c < propCases; c++ {
		r := &rng{s: base ^ (uint64(pi) << 32) ^ uint64(c)*0xD1B54A32D192ED03}
		size := c % 8

		var env []Value
		var inputs []string
		genFailed := false
		for bi := range p.Binders {
			v, err := genValue(st, &p.Binders[bi], size, r)
			if err != nil {
				rep.Err = err.Error()
				genFailed = true
				break
			}
			env = append(env, v)
			inputs = append(inputs, printValue(st, v))
		}
		if genFailed {
			return rep
		}

		ev := &evaluator{st: st, fuel: propFuel}
		out, err := ev.eval(env, h, &p.Body)
		if err != nil {
			rep.Failed = true
			rep.Counter = strings.Join(inputs, ", ") + "  (runtime error: " + err.Error() + ")"
			return rep
		}
		if out.K != "bool" || !out.Bool {
			rep.Failed = true
			rep.Counter = strings.Join(inputs, ", ")
			return rep
		}
		rep.Passed++
	}
	return rep
}
