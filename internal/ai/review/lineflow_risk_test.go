package review

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057's server risk for the line-flow parameters (TWIN_IR_SPEC §6):
// shrinking a buffer or making it finite, and any breakdown, changeover,
// calendar, release or batch parameter, is at least medium; a larger or an
// unlimited buffer is low. ServerRisk reads each member from the task that
// lifts its primitive's gate (T-047 buffers, T-048 breakdowns, T-049
// calendars and release schedules; changeovers and batches with T-050).
// Until then the primitive gate refuses the candidate and the review never
// computes its risk (T-046 review round 1, L2).
//
// lineFlowRiskCases is where that task registers its cases: each changes
// valid-line-flow-ir.json into a candidate and states the least risk
// ServerRisk must give.
type lineFlowRiskCase struct {
	name    string
	edit    func(candidate *model.ModelIR)
	atLeast Risk
}

var lineFlowRiskCases = map[validate.Primitive][]lineFlowRiskCase{
	// T-047 (D-057): a queue's capacity. In valid-line-flow-ir.json nodes 3
	// (q_burn_in) and 4 (q_line) are finite buffers of 80 and 20.
	validate.FiniteBuffer: {
		{"a smaller buffer", func(c *model.ModelIR) { c.Nodes[4].Capacity = 5 }, Medium},
		{"a buffer of one", func(c *model.ModelIR) { c.Nodes[3].Capacity = 1 }, Medium},
		{"a larger buffer", func(c *model.ModelIR) { c.Nodes[4].Capacity = 40 }, Low},
		{"an unlimited buffer", func(c *model.ModelIR) { c.Nodes[3].Capacity = 0 }, Low},
	},
	// T-048 (D-057): any breakdown parameter is at least medium. Resource 1
	// (line) breaks down: TTF exponential 600, TTR lognormal 25, cv 0.8.
	validate.Breakdowns: {
		{"a longer time to failure", func(c *model.ModelIR) {
			mean := 900.0
			c.Resources[1].Breakdowns.TimeToFailure.Distribution.Mean = &mean
		}, Medium},
		{"a faster repair", func(c *model.ModelIR) {
			mean := 10.0
			c.Resources[1].Breakdowns.TimeToRepair.Distribution.Mean = &mean
		}, Medium},
		{"breakdowns removed", func(c *model.ModelIR) { c.Resources[1].Breakdowns = nil }, Medium},
		{"breakdowns added", func(c *model.ModelIR) {
			b := *c.Resources[1].Breakdowns
			c.Resources[0].Breakdowns = &b
		}, Medium},
	},
	// Lognormal and Weibull take the risk of the member they are in.
	validate.Lognormal: {
		{"a lognormal repair with another cv", func(c *model.ModelIR) {
			cv := 0.3
			c.Resources[1].Breakdowns.TimeToRepair.Distribution.CV = &cv
		}, Medium},
		{"a lognormal service time", func(c *model.ModelIR) {
			mean, cv := 240.0, 0.2
			c.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistLognormal, Mean: &mean, CV: &cv}
		}, Low},
	},
	validate.Weibull: {
		{"a Weibull time to failure", func(c *model.ModelIR) {
			shape, scale := 1.2, 640.0
			c.Resources[1].Breakdowns.TimeToFailure.Distribution = model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale}
		}, Medium},
		{"a Weibull service time", func(c *model.ModelIR) {
			shape, scale := 3.0, 250.0
			c.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale}
		}, Low},
	},
	// T-049 (D-057): any calendar parameter, either way. Calendar two_shift
	// has day [360, 840) and evening [840, 1320) and two exceptions;
	// resources 1 (line: day 2, evening 1) and 2 (operator) are on it.
	validate.Calendars: {
		{"a longer evening shift", func(c *model.ModelIR) { c.Calendars[0].Shifts[1].End = 1380 }, Medium},
		{"a shift relabelled", func(c *model.ModelIR) { c.Calendars[0].Shifts[0].Label = "early" }, Medium},
		{"an exception dropped", func(c *model.ModelIR) { c.Calendars[0].Exceptions = c.Calendars[0].Exceptions[:1] }, Medium},
		{"another period", func(c *model.ModelIR) { c.Calendars[0].Period = 2880 }, Medium},
		{"more units in the evening", func(c *model.ModelIR) { c.Resources[1].ShiftCapacity = map[string]int{"day": 2, "evening": 2} }, Medium},
		{"a resource off its calendar", func(c *model.ModelIR) { c.Resources[2].CalendarID = "" }, Medium},
		{"a resource put on a calendar", func(c *model.ModelIR) { c.Resources[0].CalendarID = "two_shift" }, Medium},
		{"a calendar added", func(c *model.ModelIR) {
			c.Calendars = append(c.Calendars, model.Calendar{ID: "night", Period: 1440, Shifts: []model.Shift{{Start: 0, End: 360}}})
		}, Medium},
		{"the calendar renamed", func(c *model.ModelIR) { c.Calendars[0].Name = "Day and evening" }, Low},
	},
	// T-049 (D-057): any release-schedule parameter, either way. Nodes 5
	// (release_a, deterministic 0.5 on [360, 1320)), 6 (release_b, a table
	// of 20 at 360 and 840) and 7 (rush_b, poisson 0.05).
	validate.ReleaseSchedule: {
		{"a higher rate", func(c *model.ModelIR) { c.Nodes[5].Arrival.Schedule.Segments[1].Rate = 0.6 }, Medium},
		{"a lower poisson rate", func(c *model.ModelIR) { c.Nodes[7].Arrival.Schedule.Segments[0].Rate = 0.04 }, Medium},
		{"a larger batch released", func(c *model.ModelIR) { c.Nodes[6].Arrival.Schedule.Releases[0].Quantity = 25 }, Medium},
		{"a release moved", func(c *model.ModelIR) { c.Nodes[6].Arrival.Schedule.Releases[1].At = 900 }, Medium},
		{"a schedule replaced by an interarrival time", func(c *model.ModelIR) {
			mean := 30.0
			a := *c.Nodes[7].Arrival
			a.Schedule, a.Distribution = nil, model.Distribution{Type: model.DistExponential, Mean: &mean}
			c.Nodes[7].Arrival = &a
		}, Medium},
	},
	// T-050 (D-057): any batch parameter, either way. Node 1 (burn_in) is a
	// batch process: min_size 4, max_size 8, max_wait 60.
	validate.Batch: {
		{"a larger batch", func(c *model.ModelIR) { b := *c.Nodes[1].Batch; b.MaxSize = 10; c.Nodes[1].Batch = &b }, Medium},
		{"a smaller minimum", func(c *model.ModelIR) { b := *c.Nodes[1].Batch; b.MinSize = 2; c.Nodes[1].Batch = &b }, Medium},
		{"a longer wait limit", func(c *model.ModelIR) {
			b, w := *c.Nodes[1].Batch, 90.0
			b.MaxWait = &w
			c.Nodes[1].Batch = &b
		}, Medium},
		{"a shorter wait limit", func(c *model.ModelIR) {
			b, w := *c.Nodes[1].Batch, 30.0
			b.MaxWait = &w
			c.Nodes[1].Batch = &b
		}, Medium},
		{"no batch any more", func(c *model.ModelIR) { c.Nodes[1].Batch = nil }, Medium},
		{"a batch added", func(c *model.ModelIR) { c.Nodes[0].Batch = &model.Batch{MinSize: 2, MaxSize: 2} }, Medium},
		{"the same batch", func(c *model.ModelIR) { b := *c.Nodes[1].Batch; c.Nodes[1].Batch = &b }, Low},
	},
}

// A primitive the validator checks (its gate lifted) must have server-risk
// cases, and every registered case must hold.
func TestLiftedPrimitivesHaveRiskCases(t *testing.T) {
	for p := range lineFlowRiskCases {
		if !slices.Contains(validate.Primitives, p) {
			t.Errorf("risk cases for %q, which is not a primitive", p)
		}
	}
	decode := func() *model.ModelIR {
		var ir model.ModelIR
		if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-line-flow-ir.json")), &ir); err != nil {
			t.Fatal(err)
		}
		return &ir
	}
	for _, p := range validate.Primitives {
		cases := lineFlowRiskCases[p]
		if validate.Checked(p) && len(cases) == 0 {
			t.Errorf("%s is checked by the validator, but no D-057 server-risk case is registered for it", p)
		}
		for _, c := range cases {
			base, candidate := decode(), decode()
			c.edit(candidate)
			if got := ServerRisk(base, candidate); Max(got, c.atLeast) != got {
				t.Errorf("%s, %s: server risk %s, want at least %s", p, c.name, got, c.atLeast)
			}
		}
	}
}
