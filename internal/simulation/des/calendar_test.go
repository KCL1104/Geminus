package des

import (
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The Result members of resource calendars and release schedules (D-052,
// D-053, D-058; SIMULATION_SPEC §5) against testdata/simulation/
// calendar-goldens.json, hand-derived; the observation-level facts of the
// same cases are in internal/simulation/engine.

func calendarDoc(t *testing.T) (cases, reference []goldenCase) {
	t.Helper()
	var doc struct {
		Cases     []goldenCase `json:"cases"`
		Reference struct {
			Cases []goldenCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/calendar-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases, doc.Reference.Cases
}

// Every calendar case with an expected result: resource_states (the
// scheduled, available and processing groups, the downtime group with
// breakdowns and blocked_ticks with a finite buffer) exactly, blocking and
// buffers exactly, and the existing members the case states. Utilization
// keeps C·T as its denominator (D-052 (i), D-084): a resource on a
// calendar is not "more utilized" for being off shift; the scheduled
// utilization is scheduled_in_use_ticks / scheduled_ticks.
func TestCalendarGoldenResults(t *testing.T) {
	cases, _ := calendarDoc(t)
	checked := 0
	for _, c := range cases {
		if c.Expected.Result == nil {
			continue
		}
		checked++
		t.Run(c.ID, func(t *testing.T) {
			w := *c.Expected.Result
			res := run(t, &Engine{}, caseInput(t, c)).Result
			validResult(t, res)
			s := res.ReplicationSummaries[0]
			if s.CompletedCount != w.CompletedCount || s.CensoredEntities != w.Censored {
				t.Errorf("completed %d, censored %d; hand-derived %d, %d", s.CompletedCount, s.CensoredEntities, w.CompletedCount, w.Censored)
			}
			if !reflect.DeepEqual(s.ResourceStates, w.ResourceStates) {
				got, _ := json.Marshal(s.ResourceStates, json.Deterministic(true))
				want, _ := json.Marshal(w.ResourceStates, json.Deterministic(true))
				t.Errorf("resource states %s\n hand-derived %s", got, want)
			}
			if !reflect.DeepEqual(s.Blocking, w.Blocking) || !reflect.DeepEqual(s.Buffers, w.Buffers) {
				t.Errorf("blocking %+v buffers %+v\n hand-derived %+v %+v", s.Blocking, s.Buffers, w.Blocking, w.Buffers)
			}
			for r, u := range w.ResourceUtilization {
				if !near(s.ResourceUtilization[r], u) {
					t.Errorf("utilization of %s %v, hand-derived %v", r, s.ResourceUtilization[r], u)
				}
			}
			if len(s.ResourceUtilization) != len(w.ResourceUtilization) {
				t.Errorf("utilization %v, hand-derived %v", s.ResourceUtilization, w.ResourceUtilization)
			}
			lt := res.Metrics.LeadTimeMinutes
			switch {
			case len(w.LeadTimes) == 0:
				if lt.SampleCount != 0 || lt.Status != simulation.Unavailable || lt.Mean != nil || s.MeanLeadTimeMinutes != nil {
					t.Errorf("lead time %+v, hand-derived: none", lt)
				}
			case lt.SampleCount != int64(len(w.LeadTimes)) || !near(*lt.Mean, average(w.LeadTimes)) || !near(*lt.P95, quantile7(w.LeadTimes, 0.95)):
				t.Errorf("lead time %+v, hand-derived %v", lt, w.LeadTimes)
			}
			for q, waits := range w.QueueWaits {
				st := res.Metrics.Queues[q]
				if st.SampleCount != int64(len(waits)) || !near(*st.WaitMeanMinutes, average(waits)) || !near(*st.WaitP95Minutes, quantile7(waits, 0.95)) {
					t.Errorf("queue %s %+v, hand-derived waits %v", q, st, waits)
				}
			}
			for id, st := range s.ResourceStates {
				if want := float64(st.InUseTicks) / (float64(res.Metrics.Resources[id].Capacity) * float64(res.MeasurementWindow.HorizonTicks-res.MeasurementWindow.WarmupTicks)); !near(s.ResourceUtilization[id], want) {
					t.Errorf("%s: utilization %v, in_use_ticks over C·T says %v", id, s.ResourceUtilization[id], want)
				}
			}
			cp := *res
			cp.Reproducibility.ResultDigest = ""
			if d, err := hashing.Result(cp); err != nil || d != res.Reproducibility.ResultDigest {
				t.Errorf("digest %s, recomputed %s %v", res.Reproducibility.ResultDigest, d, err)
			}
		})
	}
	if checked != 20 {
		t.Fatalf("%d cases with an expected result, want 20", checked)
	}
}

// D-058's identities for a resource on a calendar, on stochastic runs (the
// reference cases' 30 replications): scheduled_in_use + overtime = in use
// (min(U, S) + max(U - S, 0) = U at every tick); available <= scheduled
// (A = min(S, C - D) <= S), equal without a downtime; idle = available -
// scheduled_in_use without breakdowns (A = S); processing <= scheduled in
// use, equal without a finite buffer (P = U); scheduled <= C·T.
func TestCalendarStateIdentities(t *testing.T) {
	_, reference := calendarDoc(t)
	if len(reference) < 4 {
		t.Fatalf("%d reference cases", len(reference))
	}
	overtime, offShift := int64(0), int64(0)
	for _, c := range reference {
		res := run(t, &Engine{}, caseInput(t, c)).Result
		validResult(t, res)
		span := res.MeasurementWindow.HorizonTicks - res.MeasurementWindow.WarmupTicks
		calendared := map[string]bool{}
		for _, r := range caseInput(t, c).Model.Resources {
			calendared[r.ID] = r.CalendarID != ""
		}
		for _, s := range res.ReplicationSummaries {
			for id, st := range s.ResourceStates {
				if !calendared[id] {
					if st.ScheduledTicks != nil {
						t.Errorf("%s r%d %s: scheduled members without a calendar", c.ID, s.ReplicationIndex, id)
					}
					continue
				}
				capTicks := int64(res.Metrics.Resources[id].Capacity) * span
				if st.ScheduledTicks == nil || st.ScheduledInUseTicks == nil || st.OvertimeTicks == nil || st.AvailableTicks == nil || st.IdleTicks == nil {
					t.Fatalf("%s r%d %s: %+v", c.ID, s.ReplicationIndex, id, st)
				}
				sched, inUse, over, avail, idle := *st.ScheduledTicks, *st.ScheduledInUseTicks, *st.OvertimeTicks, *st.AvailableTicks, *st.IdleTicks
				overtime += over
				offShift += capTicks - sched
				ok := inUse+over == st.InUseTicks && avail <= sched && sched <= capTicks && st.ScheduledProcessingTicks <= inUse && idle >= 0
				if st.DownTicks == nil {
					ok = ok && avail == sched && idle == avail-inUse
				}
				if st.BlockedTicks == nil {
					ok = ok && st.ScheduledProcessingTicks == inUse
				}
				if !ok {
					t.Errorf("%s r%d %s: %+v (C·T %d)", c.ID, s.ReplicationIndex, id, st, capTicks)
				}
			}
		}
	}
	if overtime == 0 || offShift == 0 {
		t.Fatalf("the reference cases have no overtime (%d) or no off-shift time (%d)", overtime, offShift)
	}
}

// neutralCalendar puts resource 0 of ir on a calendar that is on shift at
// every tick (one shift over the whole period, its label at the whole
// capacity), with an open exception, which changes nothing either.
func neutralCalendar(ir model.ModelIR) model.ModelIR {
	ir.Resources = slices.Clone(ir.Resources)
	ir.Calendars = []model.Calendar{{ID: "always", Period: 480, Shifts: []model.Shift{{Start: 0, End: 300, Label: "a"}, {Start: 300, End: 480}},
		Exceptions: []model.CalendarException{{Start: 100, End: 200, Open: true}}}}
	ir.Resources[0].CalendarID = "always"
	ir.Resources[0].ShiftCapacity = map[string]int{"a": ir.Resources[0].Capacity}
	return ir
}

// D-047 for calendars: a model without one has no scheduled member; with
// a neutral calendar (S = C at every tick) every existing member and the
// trace are as without it, and only the resource on it gains a
// resource_states entry, scheduled = available = C·T, no overtime, idle =
// C·T - in use.
func TestNeutralCalendarKeepsTheResult(t *testing.T) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		in := input(t, golden(t, name), 600, 60, 2, simulation.TraceSampled)
		review := simulation.ResolvedDataset{
			Binding: model.DatasetBinding{LogicalRef: "dataset:review_duration", DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40",
				ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3},
			Samples: []float64{120, 240, 360},
		}
		in.Datasets = map[string]simulation.ResolvedDataset{review.Binding.LogicalRef: review}
		in.Manifest = []model.DatasetBinding{review.Binding}
		base := run(t, &Engine{}, in)
		data, _ := json.Marshal(base.Result)
		for _, member := range []string{"scheduled_ticks", "overtime_ticks", "available_ticks", "resource_states"} {
			if slices.Contains(jsonKeys(t, data), member) {
				t.Errorf("%s: the Result has %s", name, member)
			}
		}
		nin := in
		nin.Model = neutralCalendar(in.Model)
		ndata, err := model.Encode(&nin.Model)
		if err != nil {
			t.Fatal(err)
		}
		if nin.EffectiveModelHash, err = hashing.Model(ndata); err != nil {
			t.Fatal(err)
		}
		neutral := run(t, &Engine{}, nin)
		validResult(t, neutral.Result)
		if !reflect.DeepEqual(neutral.Result.Metrics, base.Result.Metrics) || !reflect.DeepEqual(neutral.Trace.Events, base.Trace.Events) {
			t.Errorf("%s: a neutral calendar changed the metrics or the trace", name)
		}
		id := nin.Model.Resources[0].ID
		capTicks := int64(nin.Model.Resources[0].Capacity) * (neutral.Result.MeasurementWindow.HorizonTicks - neutral.Result.MeasurementWindow.WarmupTicks)
		for i, s := range neutral.Result.ReplicationSummaries {
			b := base.Result.ReplicationSummaries[i]
			st, ok := s.ResourceStates[id]
			if len(s.ResourceStates) != 1 || !ok || *st.ScheduledTicks != capTicks || *st.AvailableTicks != capTicks || *st.OvertimeTicks != 0 ||
				*st.ScheduledInUseTicks != st.InUseTicks || *st.IdleTicks != capTicks-st.InUseTicks || st.ScheduledProcessingTicks != st.InUseTicks {
				t.Errorf("%s r%d: resource states %+v", name, i, s.ResourceStates)
			}
			s.ResourceStates = nil
			if !reflect.DeepEqual(s, b) {
				t.Errorf("%s r%d: %+v\n without a calendar %+v", name, i, s, b)
			}
		}
	}
}

// D-047 for release schedules (D-053 G-R4): a deterministic schedule of one
// segment whose rate 1/c is exact in binary64 releases at the same ticks
// as the constant interarrival c; the Result is the same but for the model
// hashes.
func TestNeutralScheduleKeepsTheResult(t *testing.T) {
	cases, _ := calendarDoc(t)
	var c goldenCase
	for _, x := range cases {
		if x.ID == "G-R7" {
			c = x
		}
	}
	for _, every := range []float64{0.5, 2, 8, 16} {
		in := caseInput(t, c)
		in.Config.Replications = 2
		in.Model.Nodes = slices.Clone(in.Model.Nodes)
		var src *model.Node
		for i := range in.Model.Nodes {
			if in.Model.Nodes[i].Type == model.NodeSource {
				src = &in.Model.Nodes[i]
			}
		}
		arrival := *src.Arrival
		v := every
		arrival.Schedule, arrival.Distribution, arrival.MaxEntities = nil, model.Distribution{Type: model.DistConstant, Value: &v}, 0
		src.Arrival = &arrival
		base := run(t, &Engine{}, rehash(t, in))
		sched := arrival
		sched.Distribution = model.Distribution{}
		sched.Schedule = &model.ReleaseSchedule{Process: model.ReleaseDeterministic, Period: every * 7, Segments: []model.RateSegment{{Start: 0, Rate: 1 / every}}}
		src.Arrival = &sched
		neutral := run(t, &Engine{}, rehash(t, in))
		validResult(t, neutral.Result)
		a, b := *base.Result, *neutral.Result
		if a.ReplicationSummaries[0].CompletedCount < 5 {
			t.Fatalf("every %v: %d completed", every, a.ReplicationSummaries[0].CompletedCount)
		}
		if a.Reproducibility.EffectiveModelHash == b.Reproducibility.EffectiveModelHash {
			t.Fatalf("every %v: the same model", every)
		}
		a.Reproducibility, b.Reproducibility = simulation.Reproducibility{}, simulation.Reproducibility{}
		if !reflect.DeepEqual(a, b) {
			ga, _ := json.Marshal(a.ReplicationSummaries)
			gb, _ := json.Marshal(b.ReplicationSummaries)
			t.Errorf("every %v: the schedule changed the Result\n%s\n%s", every, gb, ga)
		}
	}
}

func rehash(t *testing.T, in simulation.RunInput) simulation.RunInput {
	t.Helper()
	data, err := model.Encode(&in.Model)
	if err != nil {
		t.Fatal(err)
	}
	if in.EffectiveModelHash, err = hashing.Model(data); err != nil {
		t.Fatal(err)
	}
	return in
}

// TEST-019 with calendars and release schedules: the reference cases (30
// replications: two shifts and a closed day, units on shift 3/2/1 with a
// poisson schedule, a calendar with breakdowns behind a finite buffer, a
// deterministic schedule and a table into two calendars) give the same
// Result and trace bit for bit whatever the worker count; another seed
// gives another digest.
func TestCalendarDeterminism(t *testing.T) {
	_, reference := calendarDoc(t)
	for _, c := range reference {
		in := caseInput(t, c)
		in.Config.TraceLevel = simulation.TraceSampled
		var digests []string
		var traces [][]byte
		for _, workers := range []int{1, 3, 8, 0} {
			out := run(t, &Engine{Workers: workers}, in)
			digests = append(digests, out.Result.Reproducibility.ResultDigest)
			data, _ := json.Marshal(out.Trace)
			traces = append(traces, data)
		}
		for i := range digests {
			if digests[i] != digests[0] || string(traces[i]) != string(traces[0]) {
				t.Errorf("%s: worker counts give different results: %v", c.ID, digests)
			}
		}
		other := in
		other.Config.Seed++
		if d := run(t, &Engine{}, other).Result.Reproducibility.ResultDigest; d == digests[0] {
			t.Errorf("%s: another seed gave the same digest", c.ID)
		}
	}
}

// des-go-0.4.0's own regression lock (engine output, not an oracle; ADR-026
// D-047 "新版本另立自己的鎖"; testdata/simulation/engine-golden-digests-
// 0.4.0.json): the calendar goldens with an expected result and the four
// stochastic reference cases C-S1–C-S4. The values are the hand-derived
// ones' (TestCalendarGoldenResults) and the SimPy reference's; the lock
// only makes a later numerical change visible. The cases of the older
// locks are checked against them with engine_version set back
// (TestGoldenDigests, TestGoldenDigests020, TestGoldenDigests030).
func TestGoldenDigests040(t *testing.T) {
	var lock struct {
		EngineVersion string            `json:"engine_version"`
		Digests       map[string]string `json:"digests"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-golden-digests-0.4.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.EngineVersion != Version {
		t.Fatalf("lock for %s, engine %s", lock.EngineVersion, Version)
	}
	cases, reference := calendarDoc(t)
	got := map[string]string{}
	for _, c := range append(cases, reference...) {
		if c.Expected.Result != nil || strings.HasPrefix(c.ID, "C-S") {
			got[c.ID] = run(t, &Engine{}, caseInput(t, c)).Result.Reproducibility.ResultDigest
		}
	}
	if len(got) != 24 || !reflect.DeepEqual(got, lock.Digests) {
		data, _ := json.Marshal(got, json.Deterministic(true))
		t.Errorf("digests %s\n locked %v", data, lock.Digests)
	}
}
