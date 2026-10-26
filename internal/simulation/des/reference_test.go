package des

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// arrivalCounter counts a replication's arrivals in [W, H] per source with
// a release schedule and segment (or table row) of its period (T-049).
type arrivalCounter struct {
	p      *engine.Program
	ir     *model.ModelIR
	w, h   int64
	counts map[string]int64
}

func (a *arrivalCounter) Observe(o engine.Observation) {
	if o.Kind != engine.ObsArrival || o.Tick < a.w || o.Tick > a.h {
		return
	}
	id := a.p.NodeID(o.Node)
	for _, n := range a.ir.Nodes {
		if n.ID != id || n.Arrival.Schedule == nil {
			continue
		}
		s := n.Arrival.Schedule
		unit := model.TimeUnit(a.ir.TimeUnit)
		ticks := func(v float64) int64 { t, _ := model.Ticks(v, unit); return t }
		off := o.Tick % ticks(s.Period)
		part := 0
		if s.Process == model.ReleaseTable {
			part = slices.IndexFunc(s.Releases, func(r model.Release) bool { return ticks(r.At) == off })
		} else {
			for j, seg := range s.Segments {
				if ticks(seg.Start) <= off {
					part = j
				}
			}
		}
		a.counts[fmt.Sprintf("%s/%d", id, part)]++
	}
}

// The Go side of the SimPy reference (TEST-034; scripts/simpy_reference.py,
// `make simulation-reference-test`): the stochastic reference cases of
// testdata/simulation/blocking-goldens.json (finite buffers, T-047), of
// testdata/simulation/breakdown-goldens.json (breakdowns, T-048), of
// testdata/simulation/calendar-goldens.json (calendars and release
// schedules, T-049) and of testdata/simulation/batch-goldens.json and
// setup-goldens.json (batch processes and changeovers, T-050;
// scripts/simpy_batch_reference.py), run by this engine, as
// per-replication values. With GEMINUS_SIMPY_REFERENCE_OUT set it writes
// them there for the scripts, which compare them with their own SimPy runs
// within the fixtures' tolerance. The engine never calls the scripts or
// SimPy (AGENTS.md §7).
func TestSimPyReferenceExport(t *testing.T) {
	type replication struct {
		CompletedCount     int64              `json:"completed_count"`
		MeanLeadTime       *float64           `json:"mean_lead_time_minutes"`
		Reworks            int64              `json:"reworks"`
		Utilization        map[string]float64 `json:"utilization"`
		BlockedTicks       map[string]int64   `json:"blocked_ticks"`
		FullTicks          map[string]int64   `json:"full_ticks"`
		BlocksStarted      map[string]int64   `json:"blocks_started"`
		BlockedEntityTicks map[string]int64   `json:"blocked_entity_ticks"`
		// Breakdowns (T-048): per resource that breaks down.
		DownTicks         map[string]int64 `json:"down_ticks"`
		AvailableTicks    map[string]int64 `json:"available_ticks"`
		IdleTicks         map[string]int64 `json:"idle_ticks"`
		BreakdownsStarted map[string]int64 `json:"breakdowns_started"`
		// Calendars and release schedules (T-049): per resource on a
		// calendar (available and idle above, too), and the arrivals per
		// source with a schedule and segment or row.
		ScheduledTicks      map[string]int64 `json:"scheduled_ticks"`
		ScheduledInUseTicks map[string]int64 `json:"scheduled_in_use_ticks"`
		OvertimeTicks       map[string]int64 `json:"overtime_ticks"`
		Arrivals            map[string]int64 `json:"arrivals"`
		// Batch processes (per batch process) and changeovers (per
		// resource with setups; ticks), T-050.
		BatchesStarted map[string]int64 `json:"batches_started"`
		MembersStarted map[string]int64 `json:"members_started"`
		SetupTicks     map[string]int64 `json:"setup_ticks"`
		SetupsStarted  map[string]int64 `json:"setups_started"`
	}
	out := map[string][]replication{}
	for _, fixture := range []struct {
		file                                   string
		breakdowns, calendars, batches, setups bool
	}{{"testdata/simulation/blocking-goldens.json", false, false, false, false}, {"testdata/simulation/breakdown-goldens.json", true, false, false, false},
		{"testdata/simulation/calendar-goldens.json", false, true, false, false},
		{"testdata/simulation/batch-goldens.json", false, false, true, false}, {"testdata/simulation/setup-goldens.json", false, false, false, true}} {
		var doc struct {
			Reference struct {
				Cases []struct {
					ID     string               `json:"id"`
					Model  jsontext.Value       `json:"model"`
					Config simulation.RunConfig `json:"config"`
				} `json:"cases"`
			} `json:"reference"`
		}
		if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), fixture.file)), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Reference.Cases) < 4 {
			t.Fatalf("%s: %d reference cases", fixture.file, len(doc.Reference.Cases))
		}
		for _, c := range doc.Reference.Cases {
			if _, dup := out[c.ID]; dup {
				t.Fatalf("reference case %s twice", c.ID)
			}
			var ir model.ModelIR
			if err := model.Decode(c.Model, &ir); err != nil {
				t.Fatal(err)
			}
			finite, breaks, calendared, scheduled := false, map[string]bool{}, map[string]bool{}, false
			batched, changes := map[string]bool{}, map[string]bool{}
			for _, n := range ir.Nodes {
				finite = finite || n.Capacity > 0
				scheduled = scheduled || (n.Arrival != nil && n.Arrival.Schedule != nil)
				if n.Batch != nil {
					batched[n.ID] = true
				}
			}
			for _, r := range ir.Resources {
				if r.Breakdowns != nil {
					breaks[r.ID] = true
				}
				if r.CalendarID != "" {
					calendared[r.ID] = true
				}
				if r.Setups != nil {
					changes[r.ID] = true
				}
			}
			switch {
			case fixture.batches || fixture.setups:
				// T-050: the batch and changeover reference has no finite
				// buffers, calendars or rate schedules.
				if fixture.batches != (len(batched) > 0) || fixture.setups != (len(changes) > 0) || finite || len(calendared) > 0 || scheduled {
					t.Fatalf("%s: %s is not a case of its fixture", fixture.file, c.ID)
				}
			case len(batched) > 0 || len(changes) > 0:
				t.Fatalf("%s: %s has batches or changeovers", fixture.file, c.ID)
			case fixture.calendars && len(calendared) > 0:
			case fixture.calendars || len(calendared) > 0 || scheduled:
				t.Fatalf("%s: %s is not a case of its fixture", fixture.file, c.ID)
			case fixture.breakdowns != (len(breaks) > 0) || (!fixture.breakdowns && !finite):
				t.Fatalf("%s: %s is not a case of its fixture", fixture.file, c.ID)
			}
			in := input(t, ir, 0, 0, 1, simulation.TraceNone)
			in.Config = c.Config
			res := run(t, &Engine{}, in).Result
			validResult(t, res)
			p, err := engine.Compile(in)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range res.ReplicationSummaries {
				r := replication{CompletedCount: s.CompletedCount, MeanLeadTime: s.MeanLeadTimeMinutes, Reworks: s.Reworks,
					Utilization: s.ResourceUtilization, BlockedTicks: map[string]int64{}, FullTicks: map[string]int64{},
					BlocksStarted: map[string]int64{}, BlockedEntityTicks: map[string]int64{},
					DownTicks: map[string]int64{}, AvailableTicks: map[string]int64{}, IdleTicks: map[string]int64{}, BreakdownsStarted: map[string]int64{},
					ScheduledTicks: map[string]int64{}, ScheduledInUseTicks: map[string]int64{}, OvertimeTicks: map[string]int64{}, Arrivals: map[string]int64{},
					BatchesStarted: map[string]int64{}, MembersStarted: map[string]int64{}, SetupTicks: map[string]int64{}, SetupsStarted: map[string]int64{}}
				if scheduled {
					counter := &arrivalCounter{p: p, ir: &ir, w: res.MeasurementWindow.WarmupTicks, h: res.MeasurementWindow.HorizonTicks, counts: r.Arrivals}
					if _, err := engine.RunReplication(context.Background(), p, uint32(s.ReplicationIndex), engine.NewBudget(in.Limits.MaxEvents), counter); err != nil {
						t.Fatal(err)
					}
				}
				if finite && (s.ResourceStates == nil || s.Blocking == nil || s.Buffers == nil) {
					t.Fatalf("%s: a model with finite buffers lacks their members", c.ID)
				}
				for id, st := range s.ResourceStates {
					if finite {
						r.BlockedTicks[id] = *st.BlockedTicks
					}
					if breaks[id] != (st.DownTicks != nil) {
						t.Fatalf("%s: resource %s, breakdowns %v, down_ticks %v", c.ID, id, breaks[id], st.DownTicks)
					}
					if st.DownTicks != nil {
						r.DownTicks[id], r.AvailableTicks[id], r.IdleTicks[id], r.BreakdownsStarted[id] = *st.DownTicks, *st.AvailableTicks, *st.IdleTicks, *st.BreakdownsStarted
					}
					if calendared[id] != (st.ScheduledTicks != nil) {
						t.Fatalf("%s: resource %s, calendar %v, scheduled_ticks %v", c.ID, id, calendared[id], st.ScheduledTicks)
					}
					if st.ScheduledTicks != nil {
						r.ScheduledTicks[id], r.ScheduledInUseTicks[id], r.OvertimeTicks[id] = *st.ScheduledTicks, *st.ScheduledInUseTicks, *st.OvertimeTicks
						r.AvailableTicks[id], r.IdleTicks[id] = *st.AvailableTicks, *st.IdleTicks
					}
					if changes[id] != (st.SetupTicks != nil) {
						t.Fatalf("%s: resource %s, setups %v, setup_ticks %v", c.ID, id, changes[id], st.SetupTicks)
					}
					if st.SetupTicks != nil {
						r.SetupTicks[id], r.SetupsStarted[id] = *st.SetupTicks, *st.SetupsStarted
					}
				}
				for id, b := range s.Batches {
					r.BatchesStarted[id], r.MembersStarted[id] = b.BatchesStarted, b.MembersStarted
				}
				if len(r.SetupTicks) != len(changes) || len(r.BatchesStarted) != len(batched) {
					t.Fatalf("%s: setup_ticks of %v, setups on %v; batches of %v, batch processes %v", c.ID, r.SetupTicks, changes, r.BatchesStarted, batched)
				}
				if len(r.DownTicks) != len(breaks) || len(r.ScheduledTicks) != len(calendared) {
					t.Fatalf("%s: down_ticks of %v, breakdowns on %v; scheduled_ticks of %v, calendars on %v", c.ID, r.DownTicks, breaks, r.ScheduledTicks, calendared)
				}
				for id, b := range s.Buffers {
					r.FullTicks[id] = b.FullTicks
				}
				for id, b := range s.Blocking {
					r.BlocksStarted[id], r.BlockedEntityTicks[id] = b.BlocksStarted, b.BlockedEntityTicks
				}
				out[c.ID] = append(out[c.ID], r)
			}
		}
	}
	path := os.Getenv("GEMINUS_SIMPY_REFERENCE_OUT")
	if path == "" {
		return
	}
	data, err := json.Marshal(out, jsontext.WithIndent("  "))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote the Go side of %d reference cases to %s", len(out), path)
}
