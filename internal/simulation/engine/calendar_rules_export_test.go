package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/calendar"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The Go side of ADR-026 D-050's planned cross-check (T-049 review round 1
// L2): an independent Python implementation of R2, R3, R4, B2's next_fit and
// X_end and TTRmax (scripts/calendar_reference.py, run by `make
// simulation-reference-test`) must agree with this validator and compiler on
// random models. This test always builds the models and runs the validator
// and Compile on them (so `make backend-test` keeps it compiling and
// running); with GEMINUS_CALENDAR_REFERENCE_OUT set it writes, per model,
// the IR, the horizon in ticks, every validation issue (code and path), and
// for a valid model each process's X_end, TTRmax and next_fit (with the
// exceptions, to the horizon) from several ticks, for the script. The
// engine never calls the script (AGENTS.md §7).

// randomRulesModel is a random line start -> p0 -> ... -> end with two to
// four resources (capacity 1-3), one to three calendars, one to three
// processes needing one to three of the resources, and breakdowns on some
// resources with no attempt to keep R3: the validator decides. Most models
// have periods of a few whole minutes and one to three shifts (so conflicts
// that appear only past the larger of two periods are common, review round
// 1 M2); one in five has periods of days, among them 5, 73 and 365 days,
// whose least common multiple is exactly 365 days or over it (review round
// 1 L1). Exceptions, open or closed, fall within the horizon.
func randomRulesModel(base model.ModelIR, seed uint64) (model.ModelIR, float64) {
	rng := rand.New(rand.NewPCG(seed, 4905))
	ir := base
	horizon := float64(60 * (2 + rng.IntN(11)))
	days := rng.IntN(5) == 0
	cals := make([]model.Calendar, 1+rng.IntN(3))
	for k := range cals {
		c := model.Calendar{ID: fmt.Sprintf("c%d", k)}
		if days {
			d := []int{1, 5, 7, 10, 73, 146, 365}[rng.IntN(7)]
			c.Period = float64(d * 1440)
			start := rng.IntN(1380)
			end := start + 30 + rng.IntN(1440-start-29)
			c.Shifts = append(c.Shifts, model.Shift{Start: float64(start), End: float64(end)})
			if d > 1 && rng.IntN(2) == 0 {
				off := 1440 * (1 + rng.IntN(d-1))
				s2 := off + rng.IntN(1380)
				c.Shifts = append(c.Shifts, model.Shift{Start: float64(s2), End: float64(s2 + 30 + rng.IntN(1440-(s2-off)-29))})
			}
		} else {
			period := []int{2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 30, 60}[rng.IntN(12)]
			c.Period = float64(period)
			at := 0
			for range 1 + rng.IntN(3) {
				if at >= period {
					break
				}
				start := at + rng.IntN(period-at)
				end := start + 1 + rng.IntN(period-start)
				c.Shifts = append(c.Shifts, model.Shift{Start: float64(start), End: float64(end)})
				at = end
			}
		}
		for s := range c.Shifts {
			if rng.IntN(2) == 0 {
				c.Shifts[s].Label = fmt.Sprintf("s%d", s)
			}
		}
		at := 0
		for range rng.IntN(3) {
			start := at + rng.IntN(int(horizon)/2+1)
			end := start + 1 + rng.IntN(120)
			c.Exceptions = append(c.Exceptions, model.CalendarException{Start: float64(start), End: float64(end), Open: rng.IntN(3) == 0})
			at = end
		}
		cals[k] = c
	}
	ir.Calendars = cals
	ir.Resources = nil
	for i := range 2 + rng.IntN(3) {
		r := model.Resource{ID: fmt.Sprintf("r%d", i), Type: model.ResourceMachine, Name: fmt.Sprintf("r%d", i), Capacity: 1 + rng.IntN(3),
			QueueDiscipline: base.Resources[0].QueueDiscipline}
		if rng.IntN(3) > 0 {
			c := cals[rng.IntN(len(cals))]
			r.CalendarID = c.ID
			for _, s := range c.Shifts {
				if s.Label != "" && rng.IntN(2) == 0 {
					if r.ShiftCapacity == nil {
						r.ShiftCapacity = map[string]int{}
					}
					r.ShiftCapacity[s.Label] = rng.IntN(r.Capacity + 1)
				}
			}
		}
		if rng.IntN(3) == 0 {
			mean := float64(30 + rng.IntN(600))
			ttf := model.Distribution{Type: model.DistExponential, Mean: &mean}
			var ttr model.Distribution
			switch rng.IntN(3) {
			case 0:
				v := 0.25 * float64(rng.IntN(240))
				ttr = model.Distribution{Type: model.DistConstant, Value: &v}
			case 1:
				lo := float64(1 + rng.IntN(10))
				mode, hi := lo+float64(rng.IntN(10)), lo+10+float64(rng.IntN(50))
				ttr = model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &mode, Max: &hi}
			default:
				m := float64(5 + rng.IntN(60))
				ttr = model.Distribution{Type: model.DistExponential, Mean: &m}
			}
			r.Breakdowns = &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: ttf}, TimeToRepair: model.TimedDistribution{Distribution: ttr}}
		}
		ir.Resources = append(ir.Resources, r)
	}
	src, work, sink := base.Nodes[0], base.Nodes[1], base.Nodes[2]
	ir.Nodes = []model.Node{src}
	ir.Flows = nil
	prev := src.ID
	for p := range 1 + rng.IntN(3) {
		n := work
		n.ID = fmt.Sprintf("p%d", p)
		n.Requires = nil
		for _, i := range rng.Perm(len(ir.Resources))[:1+rng.IntN(min(3, len(ir.Resources)))] {
			units := 1
			if c := ir.Resources[i].Capacity; c > 1 && rng.IntN(4) == 0 {
				units = 1 + rng.IntN(c)
			}
			n.Requires = append(n.Requires, model.ResourceRequirement{ResourceID: ir.Resources[i].ID, Units: units})
		}
		ir.Nodes = append(ir.Nodes, n)
		ir.Flows = append(ir.Flows, model.Flow{ID: prev + "_" + n.ID, From: prev, To: n.ID, Routing: base.Flows[0].Routing})
		prev = n.ID
	}
	ir.Nodes = append(ir.Nodes, sink)
	ir.Flows = append(ir.Flows, model.Flow{ID: prev + "_" + sink.ID, From: prev, To: sink.ID, Routing: base.Flows[0].Routing})
	return ir, horizon
}

func TestCalendarRulesExport(t *testing.T) {
	const n = 5000
	type nodeOut struct {
		XEnd    int64      `json:"x_end"`
		TTRMax  int64      `json:"ttr_max"`
		NextFit [][2]int64 `json:"next_fit"`
	}
	type caseOut struct {
		Seed    uint64             `json:"seed"`
		Model   jsontext.Value     `json:"model"`
		Horizon int64              `json:"horizon_ticks"`
		Issues  [][2]string        `json:"issues"`
		Nodes   map[string]nodeOut `json:"nodes,omitempty"`
	}
	base := minimal(t)
	var cases []caseOut
	valid, fits := 0, 0
	for seed := range uint64(n) {
		ir, horizon := randomRulesModel(base, seed)
		doc, err := model.Encode(ir)
		if err != nil {
			t.Fatal(err)
		}
		res, err := validate.IR(doc)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		c := caseOut{Seed: seed, Model: doc, Horizon: int64(horizon * 60_000), Issues: [][2]string{}}
		invalid := false
		for _, is := range res.Issues {
			c.Issues = append(c.Issues, [2]string{is.Code, is.Path})
			invalid = invalid || is.Severity == model.SeverityError
		}
		if !invalid {
			in := input(ir)
			in.Config = config(horizon, model.Minute)
			p, err := Compile(in)
			if err != nil {
				t.Fatalf("seed %d: the validator accepts it, Compile refuses it: %v", seed, err)
			}
			valid++
			rng := rand.New(rand.NewPCG(seed, 4906))
			c.Nodes = map[string]nodeOut{}
			for i := range p.nodes {
				node := &p.nodes[i]
				if node.typ != model.NodeProcess {
					continue
				}
				out := nodeOut{XEnd: node.xEnd, TTRMax: node.ttrMax, NextFit: [][2]int64{}}
				if node.need != nil {
					from := []int64{0, node.xEnd, p.horizon / 3, 2 * p.horizon / 3, rng.Int64N(p.horizon + 1), rng.Int64N(p.horizon + 1)}
					if node.ttrMax >= 0 {
						from = append(from, node.xEnd+node.ttrMax)
					}
					slices.Sort(from)
					for _, f := range slices.Compact(from) {
						at, ok := calendar.NextFit(node.need, f, p.horizon, true)
						if !ok {
							at = -1
						} else {
							fits++
						}
						out.NextFit = append(out.NextFit, [2]int64{f, at})
					}
				}
				c.Nodes[node.id] = out
			}
		}
		cases = append(cases, c)
	}
	if valid < n/5 || fits < 1000 {
		t.Fatalf("too weak: %d of %d models valid, %d next_fit found", valid, n, fits)
	}
	t.Logf("%d random models, %d valid, %d next_fit found", n, valid, fits)
	path := os.Getenv("GEMINUS_CALENDAR_REFERENCE_OUT")
	if path == "" {
		return
	}
	data, err := json.Marshal(map[string]any{"cases": cases}, jsontext.WithIndent(" "))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
