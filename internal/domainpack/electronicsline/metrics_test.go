package electronicsline_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// handLine is a small electronics line whose metrics are derived by hand
// below: an SMT line (calendar, breakdowns, changeovers, a finite buffer
// after it), an FCT station with a test result, a burn-in batch and a
// packing station, the last two without a calendar.
const handLine = `{
  "schema_version": "0.1", "model_id": "mdl_01M4YDQK00E1ECTR0N1CS00001", "domain": "electronics_line", "name": "Hand line", "time_unit": "minute",
  "entity_types": [{"id": "board", "name": "Board"}],
  "resources": [
    {"id": "line", "type": "machine", "role": "smt_line", "name": "Line", "capacity": 1, "queue_discipline": "fifo", "calendar_id": "cal",
     "breakdowns": {"time_to_failure": {"distribution": {"type": "constant", "value": 30}}, "time_to_repair": {"distribution": {"type": "constant", "value": 5}}},
     "setups": {"default": {"type": "constant", "value": 5}}},
    {"id": "tester", "type": "machine", "role": "fct_tester", "name": "Tester", "capacity": 2, "queue_discipline": "fifo", "calendar_id": "cal"},
    {"id": "oven", "type": "machine", "role": "burn_in_chamber", "name": "Oven", "capacity": 2, "queue_discipline": "fifo"},
    {"id": "packer", "type": "human", "role": "pack_station", "name": "Packer", "capacity": 1, "queue_discipline": "fifo"}
  ],
  "nodes": [
    {"id": "src", "type": "source", "entity_type": "board", "arrival": {"distribution": {"type": "constant", "value": 2}}},
    {"id": "qs", "type": "queue"},
    {"id": "smt", "type": "process", "domain_attributes": {"station": "smt", "pass": "first"}, "service_time": {"distribution": {"type": "constant", "value": 1}}, "requires": [{"resource_id": "line", "units": 1}]},
    {"id": "q1", "type": "queue", "capacity": 5},
    {"id": "fct", "type": "process", "domain_attributes": {"station": "fct", "pass": "first"}, "service_time": {"distribution": {"type": "constant", "value": 1}}, "requires": [{"resource_id": "tester", "units": 1}]},
    {"id": "fct_result", "type": "decision", "domain_attributes": {"result_of": "fct"}},
    {"id": "fct_fail", "type": "failure"},
    {"id": "scrap", "type": "sink", "outcome": "abandoned"},
    {"id": "burn", "type": "process", "domain_attributes": {"station": "burn_in", "pass": "first"}, "service_time": {"distribution": {"type": "constant", "value": 10}}, "requires": [{"resource_id": "oven", "units": 1}], "batch": {"min_size": 2, "max_size": 4, "max_wait": 10}},
    {"id": "pack", "type": "process", "domain_attributes": {"station": "pack", "pass": "first"}, "service_time": {"distribution": {"type": "constant", "value": 1}}, "requires": [{"resource_id": "packer", "units": 1}]},
    {"id": "done", "type": "sink", "outcome": "completed"}
  ],
  "flows": [
    {"id": "f1", "from": "src", "to": "qs", "routing": {"type": "probability", "probability": 1}},
    {"id": "f2", "from": "qs", "to": "smt", "routing": {"type": "probability", "probability": 1}},
    {"id": "f3", "from": "smt", "to": "q1", "routing": {"type": "probability", "probability": 1}},
    {"id": "f4", "from": "q1", "to": "fct", "routing": {"type": "probability", "probability": 1}},
    {"id": "f5", "from": "fct", "to": "fct_result", "routing": {"type": "probability", "probability": 1}},
    {"id": "f6", "from": "fct_result", "to": "burn", "routing": {"type": "probability", "probability": 0.9}},
    {"id": "f7", "from": "fct_result", "to": "fct_fail", "routing": {"type": "probability", "probability": 0.1}},
    {"id": "f8", "from": "fct_fail", "to": "scrap", "routing": {"type": "probability", "probability": 1}},
    {"id": "f9", "from": "burn", "to": "pack", "routing": {"type": "probability", "probability": 1}},
    {"id": "f10", "from": "pack", "to": "done", "routing": {"type": "probability", "probability": 1}}
  ],
  "constraints": [{"type": "outgoing_probability_sum", "node_id": "fct_result", "equals": 1}],
  "metrics": [],
  "calendars": [{"id": "cal", "period": 100, "shifts": [{"start": 0, "end": 50}]}],
  "views": {"2d": {}, "3d": {}}
}`

func handIR(t *testing.T) *model.ModelIR {
	t.Helper()
	res, err := validate.IR([]byte(handLine))
	if err != nil || !res.Valid() {
		t.Fatalf("the hand line is not a valid model: %v %v", err, res.Issues)
	}
	return res.IR
}

func p(v int64) *int64 { return &v }

// The window: W = 1,000,000 ticks, H = 13,000,000 ticks, so T = 12e6
// ticks (200 minutes). The calendar is on in [0, 50) of every 100 minutes
// (6e6 ticks), 3e6 ticks a period, so the line's on-shift time in [W, H)
// is [1e6, 3e6) + [6e6, 9e6) + [12e6, 13e6) = 2e6 + 3e6 + 1e6 = 6e6 ticks.
const handW, handH = 1_000_000, 13_000_000

// handRep is one replication's Result members, chosen by hand (they need
// not be a run the engine could produce; the pack only combines them):
//
//	line (C 1, C·T 12e6):   U 6e6, K 1e6, Z 0.5e6, D 1e6, S 6e6, min(U,S) 5e6, overtime 1e6, A 5.5e6, idle 0.5e6, min(P,S) 4e6, 3 changeovers
//	tester (C 2, C·T 24e6): U 8e6, K 2e6, S 12e6, min(U,S) 7e6, overtime 1e6, A 12e6, idle 5e6, min(P,S) 5e6
//	oven (C 2, C·T 24e6):   U 6e6, K 0, min(P,S) 6e6 (no calendar: S = C)
//	packer (C 1, C·T 12e6): U 3e6, K 0, min(P,S) 3e6
//	smt: 2 completed blocks of 600,000 ticks in all; burn: 4 batches of 12 boards; 30 boards completed
func handRep(i int) simulation.ReplicationSummary {
	return simulation.ReplicationSummary{
		ReplicationIndex: i, CompletedCount: 30,
		ResourceUtilization: map[string]float64{"line": 0.5, "tester": 8.0 / 24, "oven": 0.25, "packer": 0.25},
		ResourceStates: map[string]simulation.ResourceState{
			"line": {InUseTicks: 6_000_000, BlockedTicks: p(1_000_000), SetupTicks: p(500_000), SetupsStarted: p(3), SetupsCompleted: p(3), CompletedSetupTicks: p(500_000),
				DownTicks: p(1_000_000), BreakdownsStarted: p(2), RepairsCompleted: p(2), CompletedRepairTicks: p(1_000_000),
				ScheduledTicks: p(6_000_000), ScheduledInUseTicks: p(5_000_000), OvertimeTicks: p(1_000_000), AvailableTicks: p(5_500_000), IdleTicks: p(500_000),
				ScheduledProcessingTicks: 4_000_000},
			"tester": {InUseTicks: 8_000_000, BlockedTicks: p(2_000_000), ScheduledTicks: p(12_000_000), ScheduledInUseTicks: p(7_000_000), OvertimeTicks: p(1_000_000),
				AvailableTicks: p(12_000_000), IdleTicks: p(5_000_000), ScheduledProcessingTicks: 5_000_000},
			"oven":   {InUseTicks: 6_000_000, BlockedTicks: p(0), ScheduledProcessingTicks: 6_000_000},
			"packer": {InUseTicks: 3_000_000, BlockedTicks: p(0), ScheduledProcessingTicks: 3_000_000},
		},
		Blocking: map[string]simulation.NodeBlocking{"smt": {BlocksStarted: 2, BlocksCompleted: 2, CompletedBlockTicks: 600_000, BlockedEntityTicks: 1_000_000}},
		Buffers:  map[string]simulation.BufferState{"q1": {FullTicks: 100_000}},
		Batches:  map[string]simulation.BatchCounts{"burn": {BatchesStarted: 4, MembersStarted: 12}},
	}
}

func handResult(reps ...simulation.ReplicationSummary) *simulation.Result {
	return &simulation.Result{Status: "completed", EngineVersion: "des-go-0.5.0", ReplicationSummaries: reps,
		MeasurementWindow: simulation.MeasurementWindow{TickUnit: "millisecond", WarmupTicks: handW, HorizonTicks: handH, TimeBoundary: "inclusive_events_window_integrals"}}
}

type expectation struct {
	values  map[string]float64
	reasons map[string]string
}

// expect checks every key of e in got; with exact, got has no other key.
func expect(t *testing.T, name string, got map[string]simulation.MetricSummary, e expectation, exact bool) {
	t.Helper()
	units := map[string]string{}
	for _, d := range electronicsline.Metrics() {
		units[d.Name] = d.Unit
	}
	for k, v := range e.values {
		m, ok := got[k]
		if !ok || m.Status != simulation.Available || m.Mean == nil || m.Reason != nil || m.Population != simulation.PopulationReplication {
			t.Errorf("%s: %s = %+v, want available %v", name, k, m, v)
			continue
		}
		if math.Abs(*m.Mean-v) > 1e-12*math.Max(1, math.Abs(v)) {
			t.Errorf("%s: %s = %v, want %v", name, k, *m.Mean, v)
		}
	}
	for k, r := range e.reasons {
		m, ok := got[k]
		if !ok || m.Status != simulation.Unavailable || m.Mean != nil || m.P50 != nil || m.P95 != nil || m.Reason == nil || *m.Reason != r {
			t.Errorf("%s: %s = %+v, want unavailable %s", name, k, m, r)
		}
	}
	for k, m := range got {
		metric, _, _ := strings.Cut(k, ".")
		if u, ok := units[metric]; !ok || m.Unit != u {
			t.Errorf("%s: %s has unit %q; the catalogue says %q (%v)", name, k, m.Unit, u, ok)
		}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(data, "simulation.schema.json#/$defs/MetricSummary", "SCHEMA"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %s is not a MetricSummary: %v %v", name, k, issues, err)
		}
		if _, v := e.values[k]; exact && !v {
			if _, r := e.reasons[k]; !r {
				t.Errorf("%s: unexpected metric %s = %+v", name, k, m)
			}
		}
	}
}

// ELECTRONICS_LINE §5 by hand: every formula of the pack on the hand line.
func TestModelMetricsHandDerived(t *testing.T) {
	ir := handIR(t)
	res := handResult(handRep(0))
	before, _ := json.Marshal(res, json.Deterministic(true))
	irBefore, _ := model.Encode(ir)
	got := pack.ComputeModelMetrics(ir, res)
	after, _ := json.Marshal(res, json.Deterministic(true))
	irAfter, _ := model.Encode(ir)
	if string(before) != string(after) || string(irBefore) != string(irAfter) {
		t.Error("ComputeModelMetrics changed its input")
	}
	const CT1, CT2 = 12e6, 24e6
	expect(t, "hand line", got, expectation{
		values: map[string]float64{
			// UPH: N / (T / 3.6e6) = 30 / (12e6 / 3.6e6) = 9; scheduled: 30 / (6e6 / 3.6e6) = 18.
			"uph": 9, "uph_scheduled": 18,
			// w = 4/6, 5/12, 6/24, 3/12; mean 19/48 over max 2/3 = 57/96.
			"line_balance": 57.0 / 96,
			// The six states of the SMT line add up to 1.
			"processing_ratio.smt": (6e6 - 1e6 - 0.5e6) / CT1, "setup_ratio.smt": 0.5e6 / CT1, "blocking_ratio.smt": 1e6 / CT1,
			"down_ratio.smt": 1e6 / CT1, "idle_ratio.smt": 0.5e6 / CT1, "off_shift_ratio.smt": (CT1 - 6e6 - 1e6 - 0.5e6) / CT1,
			"starved_ratio.smt": 0.5e6 / CT1, "availability.smt": 5.5e6 / 6e6, "scheduled_utilization.smt": 5e6 / 6e6,
			"overtime_minutes.smt": 1e6 / 60000, "work_ratio.smt": 4e6 / 6e6, "mean_blocking_minutes.smt": 600000.0 / 2 / 60000,
			"setups_started.smt": 3,
			// FCT: two testers, no breakdowns or changeovers.
			"processing_ratio.fct": (8e6 - 2e6) / CT2, "blocking_ratio.fct": 2e6 / CT2, "idle_ratio.fct": 5e6 / CT2,
			"off_shift_ratio.fct": (CT2 - 8e6 - 5e6) / CT2, "starved_ratio.fct": 5e6 / CT2, "scheduled_utilization.fct": 7e6 / 12e6,
			"overtime_minutes.fct": 1e6 / 60000, "work_ratio.fct": 5e6 / 12e6, "fpy_input.fct": 0.9,
			// Burn-in: no calendar (S = C), a batch process.
			"processing_ratio.burn": 6e6 / CT2, "blocking_ratio.burn": 0, "idle_ratio.burn": 18e6 / CT2, "off_shift_ratio.burn": 0,
			"work_ratio.burn": 6e6 / CT2, "batches_started.burn": 4, "mean_batch_size.burn": 3, "batch_fill_ratio.burn": 0.75,
			// Packing: no calendar, no breakdowns.
			"processing_ratio.pack": 3e6 / CT1, "blocking_ratio.pack": 0, "idle_ratio.pack": 9e6 / CT1, "off_shift_ratio.pack": 0,
			"starved_ratio.pack": 9e6 / CT1, "work_ratio.pack": 3e6 / CT1,
		},
		reasons: map[string]string{
			"line_wip": "WIP_NOT_DECLARED", "scrap_rate": "SINK_COUNT_NOT_DECLARED",
			// The burn-in station has no test result decision.
			"rolled_throughput_yield_input": "TEST_RESULT_NOT_IDENTIFIED", "fpy_input.burn": "TEST_RESULT_NOT_IDENTIFIED",
			"simulated_first_pass_ratio.fct": "FLOW_COUNT_NOT_DECLARED", "simulated_first_pass_ratio.burn": "FLOW_COUNT_NOT_DECLARED",
			"batches_started.smt": "NO_BATCH_MODEL", "mean_batch_size.smt": "NO_BATCH_MODEL", "batch_fill_ratio.smt": "NO_BATCH_MODEL",
			"setup_ratio.fct": "NO_SETUP_MODEL", "setups_started.fct": "NO_SETUP_MODEL", "down_ratio.fct": "NO_BREAKDOWN_MODEL",
			"availability.fct": "NO_BREAKDOWN_MODEL", "mean_blocking_minutes.fct": "NO_COMPLETED_BLOCK_IN_WINDOW",
			"batches_started.fct": "NO_BATCH_MODEL", "mean_batch_size.fct": "NO_BATCH_MODEL", "batch_fill_ratio.fct": "NO_BATCH_MODEL",
			"setup_ratio.burn": "NO_SETUP_MODEL", "setups_started.burn": "NO_SETUP_MODEL", "down_ratio.burn": "NO_BREAKDOWN_MODEL",
			"availability.burn": "NO_BREAKDOWN_MODEL", "starved_ratio.burn": "STARVATION_UNDEFINED_BATCH",
			"mean_blocking_minutes.burn": "NO_COMPLETED_BLOCK_IN_WINDOW",
			"setup_ratio.pack":           "NO_SETUP_MODEL", "setups_started.pack": "NO_SETUP_MODEL", "down_ratio.pack": "NO_BREAKDOWN_MODEL",
			"availability.pack": "NO_BREAKDOWN_MODEL", "mean_blocking_minutes.pack": "NO_COMPLETED_BLOCK_IN_WINDOW",
			"batches_started.pack": "NO_BATCH_MODEL", "mean_batch_size.pack": "NO_BATCH_MODEL", "batch_fill_ratio.pack": "NO_BATCH_MODEL",
		},
	}, true)
	// Only stations with a calendar list scheduled utilization and
	// overtime; a test station has a first-pass yield, a station with a
	// retest a retest ratio (none here).
	for _, k := range []string{"scheduled_utilization.burn", "overtime_minutes.pack", "fpy_input.smt", "fpy_input.pack", "retest_ratio.fct"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s is listed", k)
		}
	}
	if again := pack.ComputeModelMetrics(ir, res); !maps.EqualFunc(got, again, func(a, b simulation.MetricSummary) bool {
		x, _ := json.Marshal(a, json.Deterministic(true))
		y, _ := json.Marshal(b, json.Deterministic(true))
		return string(x) == string(y)
	}) {
		t.Error("two calls disagree")
	}
}

// Across replications a metric is the replications' summary (population
// replication, mean, Type 7 quantiles and the Student-t interval).
func TestModelMetricsAcrossReplications(t *testing.T) {
	reps := []simulation.ReplicationSummary{handRep(0), handRep(1), handRep(2)}
	reps[1].CompletedCount, reps[2].CompletedCount = 36, 24 // UPH 10.8 and 7.2
	got := pack.ComputeModelMetrics(handIR(t), handResult(reps...))["uph"]
	if got.Status != simulation.Available || got.SampleCount != 3 || got.Mean == nil || math.Abs(*got.Mean-9) > 1e-12 ||
		math.Abs(*got.P50-9) > 1e-12 || math.Abs(*got.P95-(9+0.9*1.8)) > 1e-12 || got.ConfidenceIntervalMean == nil ||
		got.ConfidenceIntervalMean.Method != "student_t_replication_means" {
		t.Errorf("uph across replications %+v", got)
	}
	// A value some replication lacks makes the metric unavailable.
	reps[1].Batches = map[string]simulation.BatchCounts{"burn": {}}
	got2 := pack.ComputeModelMetrics(handIR(t), handResult(reps...))
	expect(t, "a replication without a batch", got2, expectation{
		values:  map[string]float64{"batches_started.burn": 8.0 / 3},
		reasons: map[string]string{"mean_batch_size.burn": "NO_BATCH_IN_WINDOW", "batch_fill_ratio.burn": "NO_BATCH_IN_WINDOW"},
	}, false)
}

// Each unavailable reason of ELECTRONICS_LINE §5 the pack can meet.
func TestModelMetricsUnavailable(t *testing.T) {
	type edit func(ir *model.ModelIR, rep *simulation.ReplicationSummary, res *simulation.Result)
	byID := func(ir *model.ModelIR, id string) *model.Node {
		return &ir.Nodes[slices.IndexFunc(ir.Nodes, func(n model.Node) bool { return n.ID == id })]
	}
	res := func(ir *model.ModelIR, id string) *model.Resource {
		return &ir.Resources[slices.IndexFunc(ir.Resources, func(r model.Resource) bool { return r.ID == id })]
	}
	for _, c := range []struct {
		name string
		edit edit
		e    expectation
	}{
		{"no finite buffer", func(ir *model.ModelIR, rep *simulation.ReplicationSummary, _ *simulation.Result) {
			byID(ir, "q1").Capacity = 0
			for id, s := range rep.ResourceStates {
				s.BlockedTicks = nil
				rep.ResourceStates[id] = s
			}
			rep.Blocking, rep.Buffers = nil, nil
		}, expectation{reasons: map[string]string{"blocking_ratio.smt": "NO_FINITE_BUFFERS", "mean_blocking_minutes.smt": "NO_FINITE_BUFFERS",
			"blocking_ratio.pack": "NO_FINITE_BUFFERS"}, values: map[string]float64{"processing_ratio.smt": 5.5e6 / 12e6}}},
		{"no line calendar", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			res(ir, "line").CalendarID, res(ir, "tester").CalendarID = "", ""
		}, expectation{reasons: map[string]string{"uph_scheduled": "NO_COMMON_LINE_CALENDAR"}, values: map[string]float64{"uph": 9}}},
		{"two line calendars", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			second := ir.Calendars[0]
			second.ID = "cal2"
			ir.Calendars = append(ir.Calendars, second)
			res(ir, "tester").CalendarID = "cal2"
		}, expectation{reasons: map[string]string{"uph_scheduled": "NO_COMMON_LINE_CALENDAR"}}},
		{"a window off shift", func(_ *model.ModelIR, rep *simulation.ReplicationSummary, r *simulation.Result) {
			r.MeasurementWindow.WarmupTicks, r.MeasurementWindow.HorizonTicks = 3_000_000, 6_000_000
			s := rep.ResourceStates["line"]
			s.ScheduledTicks = p(0)
			rep.ResourceStates["line"] = s
		}, expectation{reasons: map[string]string{"uph_scheduled": "NO_SCHEDULED_TIME_IN_WINDOW", "availability.smt": "NO_SCHEDULED_TIME_IN_WINDOW",
			"scheduled_utilization.smt": "NO_SCHEDULED_TIME_IN_WINDOW", "work_ratio.smt": "NO_SCHEDULED_TIME_IN_WINDOW"}}},
		{"no completed sink", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			byID(ir, "done").Outcome = model.SinkAbandoned
		}, expectation{reasons: map[string]string{"uph": "NO_COMPLETED_SINK", "uph_scheduled": "NO_COMPLETED_SINK"}}},
		{"a shared tester", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			n := *byID(ir, "pack")
			n.ID, n.DomainAttributes, n.Requires = "debug", nil, []model.ResourceRequirement{{ResourceID: "tester", Units: 1}}
			ir.Nodes = append(ir.Nodes, n)
		}, expectation{reasons: map[string]string{"starved_ratio.fct": "STARVATION_UNDEFINED_SHARED_RESOURCE"}, values: map[string]float64{"idle_ratio.fct": 5e6 / 24e6}}},
		{"a two-resource bundle", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			byID(ir, "fct").Requires = append(byID(ir, "fct").Requires, model.ResourceRequirement{ResourceID: "packer", Units: 1})
		}, expectation{reasons: map[string]string{"starved_ratio.fct": "STARVATION_UNDEFINED_SHARED_RESOURCE", "starved_ratio.pack": "STARVATION_UNDEFINED_SHARED_RESOURCE"}}},
		{"two units a request", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			byID(ir, "fct").Requires[0].Units = 2
		}, expectation{reasons: map[string]string{"starved_ratio.fct": "STARVATION_UNDEFINED_MULTI_UNIT"}}},
		// No test station either: no factor of the rolled throughput yield,
		// which is unavailable, not 1 (rolledYield counts the test station
		// types it multiplies, review r1 M1).
		{"one station", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			for _, id := range []string{"fct", "burn", "pack"} {
				byID(ir, id).DomainAttributes = nil
			}
		}, expectation{reasons: map[string]string{"line_balance": "TOO_FEW_STATIONS", "rolled_throughput_yield_input": "TEST_RESULT_NOT_IDENTIFIED"}}},
		{"no processing", func(_ *model.ModelIR, rep *simulation.ReplicationSummary, _ *simulation.Result) {
			for id, s := range rep.ResourceStates {
				s.ScheduledProcessingTicks = 0
				rep.ResourceStates[id] = s
			}
		}, expectation{reasons: map[string]string{"line_balance": "NO_PROCESSING_IN_WINDOW"}, values: map[string]float64{"work_ratio.smt": 0}}},
		{"a station without its role", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			res(ir, "tester").Role = "generic"
		}, expectation{reasons: map[string]string{"line_balance": "STATION_ROLE_MISSING", "processing_ratio.fct": "STATION_ROLE_MISSING",
			"work_ratio.fct": "STATION_ROLE_MISSING", "blocking_ratio.fct": "STATION_ROLE_MISSING"}, values: map[string]float64{"fpy_input.fct": 0.9}}},
		{"a test result for burn-in", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			ir.Nodes = append(ir.Nodes, model.Node{ID: "burn_result", Type: model.NodeDecision, DomainAttributes: []byte(`{"result_of":"burn"}`)},
				model.Node{ID: "burn_fail", Type: model.NodeFailure})
			for i, f := range ir.Flows {
				if f.From == "burn" {
					ir.Flows[i].To = "burn_result"
				}
			}
			ir.Flows = append(ir.Flows, model.Flow{ID: "f11", From: "burn_result", To: "pack", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.99}},
				model.Flow{ID: "f12", From: "burn_result", To: "burn_fail", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.01}},
				model.Flow{ID: "f13", From: "burn_fail", To: "scrap", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
		}, expectation{values: map[string]float64{"fpy_input.burn": 0.99, "rolled_throughput_yield_input": 0.9 * 0.99}}},
		{"a test result with two fail flows", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			for i, f := range ir.Flows {
				if f.From == "fct_result" && f.To == "burn" {
					ir.Flows[i].To = "fct_fail"
				}
			}
		}, expectation{reasons: map[string]string{"fpy_input.fct": "TEST_RESULT_NOT_IDENTIFIED", "rolled_throughput_yield_input": "TEST_RESULT_NOT_IDENTIFIED"}}},
		{"a retest", func(ir *model.ModelIR, _ *simulation.ReplicationSummary, _ *simulation.Result) {
			n := *byID(ir, "fct")
			n.ID, n.DomainAttributes = "fct_retest", []byte(`{"station":"fct","pass":"retest"}`)
			ir.Nodes = append(ir.Nodes, n)
		}, expectation{reasons: map[string]string{"retest_ratio.fct": "FLOW_COUNT_NOT_DECLARED"}, values: map[string]float64{"starved_ratio.fct": 5e6 / 24e6}}},
		{"a result without its member", func(_ *model.ModelIR, rep *simulation.ReplicationSummary, _ *simulation.Result) {
			delete(rep.ResourceStates, "line")
			rep.Batches = nil
		}, expectation{reasons: map[string]string{"processing_ratio.smt": "RESULT_MEMBER_MISSING", "work_ratio.smt": "RESULT_MEMBER_MISSING",
			"line_balance": "RESULT_MEMBER_MISSING", "batches_started.burn": "RESULT_MEMBER_MISSING"}}},
	} {
		ir := handIR(t)
		rep := handRep(0)
		r := handResult()
		c.edit(ir, &rep, r)
		r.ReplicationSummaries = []simulation.ReplicationSummary{rep}
		expect(t, c.name, pack.ComputeModelMetrics(ir, r), c.e, false)
	}
}

// flowTo is one flow of a test result: its target and probability.
type flowTo struct {
	to string
	p  float64
}

// probability is a probability-routed flow.
func probability(id, from, to string, p float64) model.Flow {
	return model.Flow{ID: id, From: from, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: p}}
}

// parallelFCT is the template with a second FCT cell in parallel (review
// r1 M1): the flows into q_fct (ICT's passes from its first test and its
// retest) go to a decision fct_split, which sends half the boards to
// q_fct → fct and half to a new q_fct_b → fct_b. fct_b is a first FCT
// test on the same FCT testers with its own test result fct_b_result,
// whose flows are result; fct_fail is the FCT debug loop the template's
// fct uses.
func parallelFCT(t *testing.T, result ...flowTo) *model.ModelIR {
	t.Helper()
	ir := tmpl(t)
	fct, res, q := ir.Nodes[nodeIndex(t, ir, "fct")], ir.Nodes[nodeIndex(t, ir, "fct_result")], ir.Nodes[nodeIndex(t, ir, "q_fct")]
	q.ID, fct.ID, res.ID = "q_fct_b", "fct_b", "fct_b_result"
	res.DomainAttributes = []byte(`{"result_of":"fct_b"}`)
	ir.Nodes = append(ir.Nodes, model.Node{ID: "fct_split", Type: model.NodeDecision}, q, fct, res)
	for i, f := range ir.Flows {
		if f.To == "q_fct" {
			ir.Flows[i].To = "fct_split"
		}
	}
	ir.Flows = append(ir.Flows, probability("fct_split__q_fct", "fct_split", "q_fct", 0.5), probability("fct_split__q_fct_b", "fct_split", "q_fct_b", 0.5),
		probability("q_fct_b__fct_b", "q_fct_b", "fct_b", 1), probability("fct_b__fct_b_result", "fct_b", "fct_b_result", 1))
	for i, f := range result {
		ir.Flows = append(ir.Flows, probability(fmt.Sprintf("fct_b_result__%d", i), "fct_b_result", f.to, f.p))
	}
	ir.Constraints = append(ir.Constraints, model.Constraint{Type: model.ConstraintOutgoingProbabilitySum, NodeID: "fct_split", Equals: 1},
		model.Constraint{Type: model.ConstraintOutgoingProbabilitySum, NodeID: "fct_b_result", Equals: 1})
	return ir
}

// revalidated is ir through the generic validator (valid, or the test
// fails) with the pack's issue codes, sorted.
func revalidated(t *testing.T, ir *model.ModelIR) (*model.ModelIR, []string) {
	t.Helper()
	data, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := validate.IR(data)
	if err != nil || !v.Valid() {
		t.Fatalf("not a valid model: %v %v", err, v.Issues)
	}
	var codes []string
	for _, issue := range pack.Validate(v.IR) {
		codes = append(codes, issue.Code)
	}
	slices.Sort(codes)
	return v.IR, codes
}

// ELECTRONICS_LINE §5: the rolled throughput yield is the product of the
// main-line test stations' first-pass yields in series, each test station
// type once (review r1 M1). Two FCT cells in parallel are one FCT step of
// the series: with FPY 0.93 in both, the template's line has
// 0.97 · 0.95 · 0.93 · 0.995 (AOI, ICT, FCT, burn-in) = 0.9215 · 0.93 ·
// 0.995 = 0.856995 · 0.995 = 0.852710025, not 0.97 · 0.95 · 0.93 · 0.93 ·
// 0.995 = 0.79302032325 (FCT once a cell). FPYs within the IR's routing
// tolerance (1e-9) are the same, and the type's is its first cell's in
// node order. Cells of one type whose FPYs differ by more have no one FPY
// for the type without their routing shares: the metric is unavailable
// with PARALLEL_TEST_YIELDS_DIFFER. A cell without a usable test result
// leaves the type without an FPY: TEST_RESULT_NOT_IDENTIFIED, as for one
// cell. Each cell's fpy_input is its own throughout.
func TestRolledYieldTakesEachTestTypeOnce(t *testing.T) {
	const serial = 0.852710025 // 0.97 · 0.95 · 0.93 · 0.995, by hand above
	others := map[string]float64{"fpy_input.aoi": 0.97, "fpy_input.ict": 0.95, "fpy_input.fct": 0.93, "fpy_input.burn_in": 0.995}
	with := func(m map[string]float64) map[string]float64 {
		out := maps.Clone(others)
		maps.Copy(out, m)
		return out
	}
	for _, c := range []struct {
		name   string
		result []flowTo
		issues []string // the pack's rules on the model
		e      expectation
	}{
		{"the same FPY", []flowTo{{"q_burnin", 0.93}, {"fct_fail", 0.07}}, nil,
			expectation{values: with(map[string]float64{"fpy_input.fct_b": 0.93, "rolled_throughput_yield_input": serial})}},
		// 0.6 + 0.33 is 0.9299999999999999 in float64: the same FPY within
		// the routing tolerance; the type's FPY is its first cell's (fct).
		{"the same FPY in two pass flows", []flowTo{{"q_burnin", 0.6}, {"q_burnin", 0.33}, {"fct_fail", 0.07}}, nil,
			expectation{values: with(map[string]float64{"fpy_input.fct_b": 0.93, "rolled_throughput_yield_input": serial})}},
		// 5e-10 away, inside the tolerance: still fct's 0.93, not fct_b's
		// (which would make the product 0.852710025 · (1 + 5.4e-10)).
		{"an FPY within the routing tolerance", []flowTo{{"q_burnin", 0.9300000005}, {"fct_fail", 0.0699999995}}, nil,
			expectation{values: with(map[string]float64{"fpy_input.fct_b": 0.9300000005, "rolled_throughput_yield_input": serial})}},
		{"another FPY", []flowTo{{"q_burnin", 0.92}, {"fct_fail", 0.08}}, nil,
			expectation{values: with(map[string]float64{"fpy_input.fct_b": 0.92}),
				reasons: map[string]string{"rolled_throughput_yield_input": "PARALLEL_TEST_YIELDS_DIFFER"}}},
		{"an FPY 1e-6 away", []flowTo{{"q_burnin", 0.930001}, {"fct_fail", 0.069999}}, nil,
			expectation{values: with(map[string]float64{"fpy_input.fct_b": 0.930001}),
				reasons: map[string]string{"rolled_throughput_yield_input": "PARALLEL_TEST_YIELDS_DIFFER"}}},
		// The fail branch bypasses the failure node (rule 2's warning): the
		// cell has no FPY, so neither has the type; its yields are not
		// compared.
		{"a cell without a usable test result", []flowTo{{"q_burnin", 0.92}, {"q_fct_debug", 0.08}}, []string{"TEST_RESULT_NOT_IDENTIFIED"},
			expectation{values: others,
				reasons: map[string]string{"fpy_input.fct_b": "TEST_RESULT_NOT_IDENTIFIED", "rolled_throughput_yield_input": "TEST_RESULT_NOT_IDENTIFIED"}}},
	} {
		ir, issues := revalidated(t, parallelFCT(t, c.result...))
		if !slices.Equal(issues, c.issues) {
			t.Errorf("%s: the pack's rules give %v, want %v", c.name, issues, c.issues)
		}
		res, err := engine().Run(context.Background(), runInput(t, ir, 7, 1, 1, 3), nil)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		d := res.Metrics.Domain
		expect(t, c.name, d, c.e, false)
		if c.name != "the same FPY" {
			continue
		}
		// Line balance counts each primary resource once (review r1 L4):
		// fct and fct_b share the FCT testers, one term of six (one
		// replication, so the metric is that replication's value).
		var w []float64
		for _, s := range []string{"smt", "aoi", "ict", "fct", "burn_in", "pack_ship"} {
			w = append(w, *d["work_ratio."+s].Mean)
		}
		if a, b := d["work_ratio.fct"], d["work_ratio.fct_b"]; a.Mean == nil || b.Mean == nil || *a.Mean != *b.Mean {
			t.Errorf("the two FCT cells' work ratios %+v %+v are not the FCT testers'", a, b)
		}
		sum := 0.0
		for _, v := range w {
			sum += v
		}
		expect(t, c.name+": line balance", d, expectation{values: map[string]float64{"line_balance": sum / 6 / slices.Max(w)}}, false)
		// The retest (on the FCT testers) goes with the first FCT test on
		// them in node order: fct, not fct_b.
		if _, listed := d["retest_ratio.fct_b"]; listed || d["retest_ratio.fct"].Reason == nil {
			t.Errorf("%s: retest_ratio.fct %+v, retest_ratio.fct_b listed %v", c.name, d["retest_ratio.fct"], listed)
		}
	}
}

// handParallel is the hand line with a second FCT cell (review r1 L4): the
// SMT line feeds a decision fct_split, half the boards to q1 → fct and
// half to q1b (also 5 places) → fct_b, a first FCT test whose result
// fct_b_result passes 0.9 to burn and fails 0.1 to fct_b_fail →
// fct_b_retest → burn. With own, fct_b and its retest use a tester of
// their own, tester_b (role fct_tester, one unit, the line calendar);
// otherwise the FCT testers fct uses.
func handParallel(t *testing.T, own bool) *model.ModelIR {
	t.Helper()
	ir := handIR(t)
	tester := "tester"
	if own {
		tester = "tester_b"
		r := ir.Resources[slices.IndexFunc(ir.Resources, func(r model.Resource) bool { return r.ID == "tester" })]
		r.ID, r.Name, r.Capacity = "tester_b", "Tester B", 1
		ir.Resources = append(ir.Resources, r)
	}
	node := func(id string) model.Node {
		return ir.Nodes[slices.IndexFunc(ir.Nodes, func(n model.Node) bool { return n.ID == id })]
	}
	q, first, retest := node("q1"), node("fct"), node("fct")
	q.ID = "q1b"
	first.ID, first.Requires = "fct_b", []model.ResourceRequirement{{ResourceID: tester, Units: 1}}
	retest.ID, retest.Requires = "fct_b_retest", []model.ResourceRequirement{{ResourceID: tester, Units: 1}}
	retest.DomainAttributes = []byte(`{"station":"fct","pass":"retest"}`)
	ir.Nodes = append(ir.Nodes, model.Node{ID: "fct_split", Type: model.NodeDecision}, q, first,
		model.Node{ID: "fct_b_result", Type: model.NodeDecision, DomainAttributes: []byte(`{"result_of":"fct_b"}`)},
		model.Node{ID: "fct_b_fail", Type: model.NodeFailure}, retest)
	for i, f := range ir.Flows {
		if f.To == "q1" {
			ir.Flows[i].To = "fct_split"
		}
	}
	ir.Flows = append(ir.Flows, probability("s1", "fct_split", "q1", 0.5), probability("s2", "fct_split", "q1b", 0.5), probability("s3", "q1b", "fct_b", 1),
		probability("b1", "fct_b", "fct_b_result", 1), probability("b2", "fct_b_result", "burn", 0.9), probability("b3", "fct_b_result", "fct_b_fail", 0.1),
		probability("b4", "fct_b_fail", "fct_b_retest", 1), probability("b5", "fct_b_retest", "burn", 1))
	ir.Constraints = append(ir.Constraints, model.Constraint{Type: model.ConstraintOutgoingProbabilitySum, NodeID: "fct_split", Equals: 1},
		model.Constraint{Type: model.ConstraintOutgoingProbabilitySum, NodeID: "fct_b_result", Equals: 1})
	ir, issues := revalidated(t, ir)
	if len(issues) > 0 {
		t.Fatalf("the pack's rules: %v", issues)
	}
	return ir
}

// Several first tests of one station type, by hand (review r1 L4): line
// balance counts each primary resource once, and a retest goes with the
// first test whose primary resource it shares (the first such in node
// order), whatever other first tests of its type come before it.
func TestParallelFirstTestsHandDerived(t *testing.T) {
	// Blocks of fct_b (one of 120,000 ticks) and of fct_b_retest (one of
	// 240,000): the station of the retest adds them.
	blocks := func(rep *simulation.ReplicationSummary) {
		rep.Blocking["fct_b"] = simulation.NodeBlocking{BlocksStarted: 1, BlocksCompleted: 1, CompletedBlockTicks: 120_000, BlockedEntityTicks: 120_000}
		rep.Blocking["fct_b_retest"] = simulation.NodeBlocking{BlocksStarted: 1, BlocksCompleted: 1, CompletedBlockTicks: 240_000, BlockedEntityTicks: 240_000}
	}

	// On the FCT testers: the testers once in line balance, w = 4/6, 5/12,
	// 6/24, 3/12 as on the hand line, 57/96 (counting them twice would give
	// mean(4/6, 5/12, 5/12, 6/24, 3/12) / (4/6) = (2/5) / (2/3) = 3/5). The
	// retest goes with fct, the first FCT test on the testers: its block
	// (4 minutes) is fct's, fct_b has its own (2 minutes). The testers serve
	// two stations, so idle is not starvation at either.
	rep := handRep(0)
	blocks(&rep)
	shared := pack.ComputeModelMetrics(handParallel(t, false), handResult(rep))
	expect(t, "a second cell on the same testers", shared, expectation{
		values: map[string]float64{"line_balance": 57.0 / 96, "fpy_input.fct": 0.9, "fpy_input.fct_b": 0.9,
			"mean_blocking_minutes.fct": 4, "mean_blocking_minutes.fct_b": 2, "work_ratio.fct_b": 5e6 / 12e6, "idle_ratio.fct_b": 5e6 / 24e6},
		reasons: map[string]string{"retest_ratio.fct": "FLOW_COUNT_NOT_DECLARED",
			"starved_ratio.fct": "STARVATION_UNDEFINED_SHARED_RESOURCE", "starved_ratio.fct_b": "STARVATION_UNDEFINED_SHARED_RESOURCE"},
	}, false)
	if _, listed := shared["retest_ratio.fct_b"]; listed {
		t.Error("a second cell on the same testers: fct_b has the retest")
	}

	// On a tester of its own (C 1, C·T 12e6): U 3.5e6, K 0.5e6, S 6e6,
	// min(U,S) 3.2e6, overtime 0.3e6, A 6e6, idle 2.8e6, min(P,S) 3e6.
	// Line balance has five resources, w = 4/6, 5/12, 3/6, 6/24, 3/12:
	// mean 25/60 over max 2/3 = 5/8. The retest goes with fct_b, whose
	// tester it shares, although fct is the first FCT test in node order:
	// fct_b adds its block, (120,000 + 240,000) / 2 ticks = 3 minutes, and
	// fct has none; tester_b serves fct_b's station only, so its idle is
	// starvation.
	rep = handRep(0)
	blocks(&rep)
	rep.ResourceUtilization["tester_b"] = 3.5e6 / 12e6
	rep.ResourceStates["tester_b"] = simulation.ResourceState{InUseTicks: 3_500_000, BlockedTicks: p(500_000), ScheduledTicks: p(6_000_000),
		ScheduledInUseTicks: p(3_200_000), OvertimeTicks: p(300_000), AvailableTicks: p(6_000_000), IdleTicks: p(2_800_000), ScheduledProcessingTicks: 3_000_000}
	own := pack.ComputeModelMetrics(handParallel(t, true), handResult(rep))
	expect(t, "a second cell with a tester of its own", own, expectation{
		values: map[string]float64{"line_balance": 5.0 / 8, "fpy_input.fct": 0.9, "fpy_input.fct_b": 0.9,
			"mean_blocking_minutes.fct_b": 3, "work_ratio.fct_b": 0.5, "work_ratio.fct": 5e6 / 12e6,
			"processing_ratio.fct_b": 3e6 / 12e6, "blocking_ratio.fct_b": 0.5e6 / 12e6, "idle_ratio.fct_b": 2.8e6 / 12e6,
			"off_shift_ratio.fct_b": (12e6 - 3.5e6 - 2.8e6) / 12e6, "starved_ratio.fct_b": 2.8e6 / 12e6, "starved_ratio.fct": 5e6 / 24e6,
			"scheduled_utilization.fct_b": 3.2e6 / 6e6, "overtime_minutes.fct_b": 5},
		reasons: map[string]string{"retest_ratio.fct_b": "FLOW_COUNT_NOT_DECLARED", "mean_blocking_minutes.fct": "NO_COMPLETED_BLOCK_IN_WINDOW"},
	}, false)
	if _, listed := own["retest_ratio.fct"]; listed {
		t.Error("a second cell with a tester of its own: fct has fct_b's retest")
	}
}

// No model, no result or no replication: no metrics, and no panic.
func TestModelMetricsWithoutInput(t *testing.T) {
	if got := pack.ComputeModelMetrics(nil, handResult(handRep(0))); len(got) != 0 {
		t.Errorf("nil IR: %v", got)
	}
	if got := pack.ComputeModelMetrics(handIR(t), nil); len(got) != 0 {
		t.Errorf("nil result: %v", got)
	}
	if got := pack.ComputeModelMetrics(handIR(t), handResult()); len(got) != 0 {
		t.Errorf("no replication: %v", got)
	}
	// A result whose window is empty or inverted names nothing either.
	r := handResult(handRep(0))
	r.MeasurementWindow.HorizonTicks = r.MeasurementWindow.WarmupTicks
	if got := pack.ComputeModelMetrics(handIR(t), r); len(got) != 0 {
		t.Errorf("empty window: %v", got)
	}
}

// The metric catalogue: each name once, line or station scope, a unit;
// the names follow ELECTRONICS_LINE §5 and the UI has a label for each in
// both languages (REQ-022), as for every node and resource kind; the
// English labels of the kinds are the pack's own.
func TestCataloguesHaveUILabels(t *testing.T) {
	read := func(name string) map[string]string {
		src := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "apps/web/messages", name)))
		out := map[string]string{}
		for _, m := range regexp.MustCompile(`(?m)^\s*"(domain\.electronics_line\.[a-z_.]+)":\s*"([^"]*)",`).FindAllStringSubmatch(src, -1) {
			out[m[1]] = m[2]
		}
		return out
	}
	en, zh := read("en.ts"), read("zh-Hant.ts")
	want := map[string]string{}
	for _, n := range pack.NodeCatalog() {
		want["domain.electronics_line.node."+n.Kind] = n.Label
	}
	for _, r := range pack.ResourceCatalog() {
		want["domain.electronics_line.resource."+r.Kind] = r.Label
	}
	var names []string
	for _, d := range electronicsline.Metrics() {
		if d.Name == "" || strings.Contains(d.Name, ".") || d.Unit == "" || (d.Scope != electronicsline.LineScope && d.Scope != electronicsline.StationScope) {
			t.Errorf("metric %+v", d)
		}
		names = append(names, d.Name)
		want["domain.electronics_line.metric."+d.Name] = ""
	}
	if len(slices.Compact(slices.Sorted(slices.Values(names)))) != len(names) || len(names) < 20 {
		t.Errorf("metric names %v", names)
	}
	for k, label := range want {
		if en[k] == "" || zh[k] == "" || en[k] == zh[k] || (label != "" && en[k] != label) {
			t.Errorf("%s: en %q, zh-Hant %q, pack label %q", k, en[k], zh[k], label)
		}
	}
	for k := range en {
		if _, ok := want[k]; !ok {
			t.Errorf("the UI labels %s, which the pack does not have", k)
		}
	}
	if len(zh) != len(en) {
		t.Errorf("%d zh-Hant labels for %d en", len(zh), len(en))
	}
	var _ domainpack.ModelMetricsProvider = electronicsline.Pack{}
}
