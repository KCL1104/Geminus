package des

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The Go side of the SimPy reference (TEST-034; scripts/simpy_reference.py,
// `make simulation-reference-test`): the stochastic reference cases of
// testdata/simulation/blocking-goldens.json (finite buffers, T-047) and of
// testdata/simulation/breakdown-goldens.json (breakdowns, T-048), run by
// this engine, as per-replication values. With GEMINUS_SIMPY_REFERENCE_OUT
// set it writes them there for the script, which compares them with its
// own SimPy runs within the fixtures' tolerance. The engine never calls the
// script or SimPy (AGENTS.md §7).
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
	}
	out := map[string][]replication{}
	for _, fixture := range []struct {
		file       string
		breakdowns bool
	}{{"testdata/simulation/blocking-goldens.json", false}, {"testdata/simulation/breakdown-goldens.json", true}} {
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
			finite, breaks := false, map[string]bool{}
			for _, n := range ir.Nodes {
				finite = finite || n.Capacity > 0
			}
			for _, r := range ir.Resources {
				if r.Breakdowns != nil {
					breaks[r.ID] = true
				}
			}
			if fixture.breakdowns != (len(breaks) > 0) || (!fixture.breakdowns && !finite) {
				t.Fatalf("%s: %s is not a case of its fixture", fixture.file, c.ID)
			}
			in := input(t, ir, 0, 0, 1, simulation.TraceNone)
			in.Config = c.Config
			res := run(t, &Engine{}, in).Result
			validResult(t, res)
			for _, s := range res.ReplicationSummaries {
				r := replication{CompletedCount: s.CompletedCount, MeanLeadTime: s.MeanLeadTimeMinutes, Reworks: s.Reworks,
					Utilization: s.ResourceUtilization, BlockedTicks: map[string]int64{}, FullTicks: map[string]int64{},
					BlocksStarted: map[string]int64{}, BlockedEntityTicks: map[string]int64{},
					DownTicks: map[string]int64{}, AvailableTicks: map[string]int64{}, IdleTicks: map[string]int64{}, BreakdownsStarted: map[string]int64{}}
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
				}
				if len(r.DownTicks) != len(breaks) {
					t.Fatalf("%s: down_ticks of %v, breakdowns on %v", c.ID, r.DownTicks, breaks)
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
