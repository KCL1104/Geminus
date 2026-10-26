package electronicsline_test

import (
	"context"
	"encoding/json/v2"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/observed"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The synthetic data set's window (its ground-truth.json): model time 0 is
// Monday 2026-09-28 00:00 at +08:00; the log is the second week.
var (
	plus8    = time.FixedZone("", 8*3600)
	anchor   = time.Date(2026, 9, 28, 0, 0, 0, 0, plus8)
	dataWeek = electronicsline.ObservedWindow{Anchor: anchor, From: anchor.AddDate(0, 0, 7), To: anchor.AddDate(0, 0, 14)}
)

// truthRun runs the template (the data set's model) for two weeks, the
// first warm-up, with seed and reps.
func truthRun(t *testing.T, reps int, seed int64) (*model.ModelIR, *simulation.Result) {
	t.Helper()
	ir := tmpl(t)
	res, err := engine().Run(context.Background(), runInput(t, ir, 14, 7, reps, seed), nil)
	if err != nil {
		t.Fatal(err)
	}
	return ir, res
}

func compare(t *testing.T, ir *model.ModelIR, res *simulation.Result, evs []domainpack.CanonicalEvent, w electronicsline.ObservedWindow) map[string]observed.Comparison {
	t.Helper()
	rows, err := electronicsline.CompareObserved(ir, res, evs, w, observed.HoldOut)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]observed.Comparison{}
	for _, r := range rows {
		if _, dup := out[r.Key]; dup {
			t.Fatalf("%s twice", r.Key)
		}
		out[r.Key] = r.Comparison
		if r.Sample != observed.HoldOut {
			t.Errorf("%s: sample %q", r.Key, r.Sample)
		}
	}
	return out
}

func minutes(t *testing.T, a, b string) float64 {
	t.Helper()
	s, err1 := time.Parse(time.RFC3339, a)
	e, err2 := time.Parse(time.RFC3339, b)
	if err1 != nil || err2 != nil {
		t.Fatal(a, b)
	}
	return e.Sub(s).Minutes()
}

// D-060 on the synthetic data (T-052), against independent counts of the
// log: the line's output per week and per day (an output: what the run's
// replications ship over the same window), each test station's first-pass
// yield (input consistency: boards that entered the line in the week, the
// result of their first test there, against the model's input) and each
// station's mean cycle time (input consistency: the log's end − start
// against the mean of the model's service time). With the generator's seed
// and one replication the run is the log, so the output's error is 0.
func TestCompareObservedOnTheSyntheticLog(t *testing.T) {
	evs := events(t, datasetFile(t, "station-log-synthetic.csv"))
	ir, truth := truthRun(t, 1, 52)
	got := compare(t, ir, truth, evs, dataWeek)

	// Independent counts of the log.
	shipped := 0
	entered := map[string]bool{}
	first := map[string]map[string]domainpack.CanonicalEvent{} // station -> serial -> first operation
	cycle := map[string][]float64{}
	for _, e := range evs {
		if e.EventType != "operation" {
			continue
		}
		if e.Stage == "pack" {
			shipped++
		}
		if e.Stage == "smt" {
			entered[e.CaseID] = true
		}
		cycle[e.Stage] = append(cycle[e.Stage], minutes(t, e.Timestamp, e.EndTimestamp))
		if first[e.Stage] == nil {
			first[e.Stage] = map[string]domainpack.CanonicalEvent{}
		}
		if _, seen := first[e.Stage][e.CaseID]; !seen {
			first[e.Stage][e.CaseID] = e // the log is in start order
		}
	}
	if shipped != 3588 {
		t.Fatalf("the log ships %d", shipped)
	}
	week := got["line_output_per_week"]
	if week.Kind != observed.Output || week.Status != simulation.Available || *week.Observed != 3588 || *week.Simulated != 3588 ||
		*week.AbsoluteError != 0 || *week.RelativeError != 0 || !*week.WithinRange || week.Replications != 1 || week.Unit != "entity" || week.Approximate {
		t.Errorf("per week %+v", week)
	}
	day := got["line_output_per_day"]
	if day.Status != simulation.Available || *day.Observed != 3588.0/7 || *day.Simulated != 3588.0/7 || day.Unit != "entity" {
		t.Errorf("per day %+v", day)
	}
	for _, st := range []struct {
		node, stage string
		input       float64
	}{{"aoi", "aoi", 0.97}, {"ict", "ict", 0.95}, {"fct", "fct", 0.93}, {"burn_in", "burn_in", 0.995}} {
		pass, boards := 0, 0
		for serial, e := range first[st.stage] {
			if entered[serial] {
				boards++
				if e.Outcome == "pass" {
					pass++
				}
			}
		}
		c := got["first_pass_yield."+st.node]
		if want := float64(pass) / float64(boards); c.Kind != observed.InputConsistency || c.Status != simulation.Available ||
			*c.Observed != want || *c.Simulated != st.input || c.RangeLow != nil || c.Unit != "ratio" || boards < 3000 {
			t.Errorf("FPY %s: %+v, want %v of %d boards against %v", st.node, c, want, boards, st.input)
		}
	}
	for _, st := range []struct {
		node, stage string
		mean        float64
	}{{"smt", "smt", (0.9 + 1.0 + 1.4) / 3}, {"aoi", "aoi", (0.8 + 0.9 + 1.1) / 3}, {"ict", "ict", (1.8 + 2.0 + 2.6) / 3},
		{"fct", "fct", (2.8 + 3.2 + 4.0) / 3}, {"burn_in", "burn_in", 240}, {"pack_ship", "pack", (0.8 + 1.0 + 1.5) / 3}} {
		xs := cycle[st.stage]
		sum := 0.0
		for _, x := range xs {
			sum += x
		}
		c := got["mean_cycle_minutes."+st.node]
		// The log's times are whole seconds: its mean is within a second of
		// the engine's, and within 2% of the model's mean.
		if c.Kind != observed.InputConsistency || c.Status != simulation.Available || math.Abs(*c.Observed-sum/float64(len(xs))) > 1e-9 ||
			math.Abs(*c.Simulated-st.mean) > 1e-12 || math.Abs(*c.Observed-st.mean) > 0.02*st.mean || c.Unit != "minute" || c.Approximate {
			t.Errorf("cycle %s: %+v, want the mean of %d steps against %v", st.node, c, len(xs), st.mean)
		}
	}
	// Exactly these keys, in this order: the line's, then each station's
	// in node order (D-117 (d)'s key form).
	rows, err := electronicsline.CompareObserved(ir, truth, evs, dataWeek, observed.HoldOut)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	want := []string{"line_output_per_week", "line_output_per_day", "mean_cycle_minutes.smt", "first_pass_yield.aoi", "mean_cycle_minutes.aoi",
		"first_pass_yield.ict", "mean_cycle_minutes.ict", "first_pass_yield.fct", "mean_cycle_minutes.fct", "first_pass_yield.burn_in",
		"mean_cycle_minutes.burn_in", "mean_cycle_minutes.pack_ship"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys %v", keys)
	}
}

// Another seed is another week of the same line: the output's simulated
// side is the replications' completed boards over the window, their mean,
// range and errors as D-060 says; the observed side and the inputs do not
// depend on the run.
func TestCompareObservedAgainstAnotherRun(t *testing.T) {
	evs := events(t, datasetFile(t, "station-log-synthetic.csv"))
	ir, res := truthRun(t, 5, 2026)
	got := compare(t, ir, res, evs, dataWeek)
	xs := make([]float64, len(res.ReplicationSummaries))
	for i, r := range res.ReplicationSummaries {
		xs[i] = float64(r.CompletedCount)
	}
	want := observed.CompareOutput(3588, xs)
	if c := got["line_output_per_week"]; c.Replications != 5 || *c.Simulated != *want.Simulated || *c.RangeLow != slices.Min(xs) ||
		*c.RangeHigh != slices.Max(xs) || *c.AbsoluteError != *want.AbsoluteError || c.RangeMethod != observed.RangeMinMax {
		t.Errorf("per week %+v, replications %v", c, xs)
	}
	_, truth := truthRun(t, 1, 52)
	same := compare(t, ir, truth, evs, dataWeek)
	for k, c := range got {
		if c.Kind == observed.InputConsistency && (*c.Observed != *same[k].Observed || *c.Simulated != *same[k].Simulated) {
			t.Errorf("%s depends on the run", k)
		}
	}
}

// Never a number without its data (REQ-015, §7's reasons): no events; a
// window shorter than a week (the days still compare), not whole periods
// or not at the model's phase (here an hour late), or across two UTC
// offsets; a result without replications; a test without results or a
// shipment without its end in the log; a model without a usable test
// result or with a service time whose mean the numerical profile cannot
// give (Weibull). The inputs are not changed.
func TestCompareObservedUnavailable(t *testing.T) {
	evs := events(t, datasetFile(t, "station-log-synthetic.csv"))
	ir, truth := truthRun(t, 1, 52)
	reason := func(c observed.Comparison) string {
		if c.Status != simulation.Unavailable || c.Reason == nil || c.Observed != nil || c.Simulated != nil {
			return "available"
		}
		return *c.Reason
	}
	for k, c := range compare(t, ir, truth, nil, dataWeek) {
		if reason(c) != observed.ReasonNoObservedData {
			t.Errorf("no events, %s: %+v", k, c)
		}
	}
	// The model repeats weekly (its calendar and releases), so both windows
	// are whole weeks at the model's phase, per day as per week: three days
	// would compare Monday to Wednesday with a whole week.
	three := dataWeek
	three.To = three.From.AddDate(0, 0, 3)
	got := compare(t, ir, truth, evs, three)
	if reason(got["line_output_per_week"]) != observed.ReasonWindowTooShort || reason(got["line_output_per_day"]) != observed.ReasonWindowTooShort {
		t.Errorf("three days: %+v %+v", got["line_output_per_week"], got["line_output_per_day"])
	}
	late := dataWeek
	late.From, late.To = late.From.Add(time.Hour), late.To.Add(time.Hour)
	got = compare(t, ir, truth, evs, late)
	if reason(got["line_output_per_week"]) != observed.ReasonWindowNotAligned || reason(got["line_output_per_day"]) != observed.ReasonWindowNotAligned {
		t.Errorf("an hour late: %+v %+v", got["line_output_per_week"], got["line_output_per_day"])
	}
	odd := dataWeek
	odd.To = odd.To.Add(12 * time.Hour)
	if c := compare(t, ir, truth, evs, odd)["line_output_per_day"]; reason(c) != observed.ReasonWindowNotAligned {
		t.Errorf("seven and a half days: %+v", c)
	}
	mixed := slices.Clone(evs)
	mixed[100].Timestamp = "2026-10-05T07:00:00+09:00" // another offset in the window
	if c := compare(t, ir, truth, mixed, dataWeek)["line_output_per_week"]; reason(c) != observed.ReasonWindowNotAligned {
		t.Errorf("two offsets: %+v", c)
	}
	none := *truth
	none.ReplicationSummaries = nil
	if c := compare(t, ir, &none, evs, dataWeek)["line_output_per_week"]; reason(c) != observed.ReasonSimulatedUnavailable {
		t.Errorf("no replications: %+v", c)
	}
	// No run at all: the outputs have no simulated side (not a window that
	// does not align); the inputs need none.
	got = compare(t, ir, nil, evs, dataWeek)
	if reason(got["line_output_per_week"]) != observed.ReasonSimulatedUnavailable || got["first_pass_yield.fct"].Status != simulation.Available {
		t.Errorf("no run: %+v %+v", got["line_output_per_week"], got["first_pass_yield.fct"])
	}
	noResults := slices.Clone(evs)
	noEnd := slices.Clone(evs)
	for i := range noResults {
		if noResults[i].Stage == "ict" {
			noResults[i].Outcome = ""
		}
		if noEnd[i].Stage == "pack" && i%2 == 0 {
			noEnd[i].EndTimestamp = ""
		}
	}
	if c := compare(t, ir, truth, noResults, dataWeek)["first_pass_yield.ict"]; reason(c) != observed.ReasonFieldMissing {
		t.Errorf("no ICT results: %+v", c)
	}
	got = compare(t, ir, truth, noEnd, dataWeek)
	if reason(got["line_output_per_week"]) != observed.ReasonFieldMissing || reason(got["mean_cycle_minutes.pack_ship"]) != observed.ReasonFieldMissing {
		t.Errorf("shipments without an end: %+v %+v", got["line_output_per_week"], got["mean_cycle_minutes.pack_ship"])
	}
	changed := tmpl(t)
	for i := range changed.Flows {
		if changed.Flows[i].ID == "fct_result__fct_fail" {
			changed.Flows[i].To = "q_burnin" // the FCT result has no fail flow
		}
	}
	for i := range changed.Nodes {
		if changed.Nodes[i].ID == "aoi" {
			shape, scale := 2.0, 1.0
			changed.Nodes[i].ServiceTime = &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale}}
		}
	}
	got = compare(t, changed, truth, evs, dataWeek)
	if reason(got["first_pass_yield.fct"]) != observed.ReasonSimulatedUnavailable || reason(got["mean_cycle_minutes.aoi"]) != observed.ReasonSimulatedUnavailable {
		t.Errorf("no FCT fail flow, a Weibull AOI: %+v %+v", got["first_pass_yield.fct"], got["mean_cycle_minutes.aoi"])
	}
	before, err := json.Marshal(evs, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	irBefore, err := json.Marshal(ir, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	compare(t, ir, truth, evs, dataWeek)
	after, _ := json.Marshal(evs, json.Deterministic(true))
	irAfter, _ := json.Marshal(ir, json.Deterministic(true))
	if string(before) != string(after) || string(irBefore) != string(irAfter) {
		t.Error("the inputs were changed")
	}
	if _, err := electronicsline.CompareObserved(nil, truth, evs, dataWeek, observed.HoldOut); err == nil {
		t.Error("no model")
	}
	bad := slices.Clone(evs)
	bad[0].Timestamp = "yesterday"
	if _, err := electronicsline.CompareObserved(ir, truth, bad, dataWeek, observed.HoldOut); err == nil {
		t.Error("an event that is not canonical")
	}
}
