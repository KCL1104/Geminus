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
// testdata/simulation/blocking-goldens.json, run by this engine, as
// per-replication values. With GEMINUS_SIMPY_REFERENCE_OUT set it writes
// them there for the script, which compares them with its own SimPy runs
// within the fixture's tolerance. The engine never calls the script or
// SimPy (AGENTS.md §7).
func TestSimPyReferenceExport(t *testing.T) {
	var doc struct {
		Reference struct {
			Cases []struct {
				ID     string               `json:"id"`
				Model  jsontext.Value       `json:"model"`
				Config simulation.RunConfig `json:"config"`
			} `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	type replication struct {
		CompletedCount     int64              `json:"completed_count"`
		MeanLeadTime       *float64           `json:"mean_lead_time_minutes"`
		Reworks            int64              `json:"reworks"`
		Utilization        map[string]float64 `json:"utilization"`
		BlockedTicks       map[string]int64   `json:"blocked_ticks"`
		FullTicks          map[string]int64   `json:"full_ticks"`
		BlocksStarted      map[string]int64   `json:"blocks_started"`
		BlockedEntityTicks map[string]int64   `json:"blocked_entity_ticks"`
	}
	out := map[string][]replication{}
	if len(doc.Reference.Cases) < 4 {
		t.Fatalf("%d reference cases", len(doc.Reference.Cases))
	}
	for _, c := range doc.Reference.Cases {
		var ir model.ModelIR
		if err := model.Decode(c.Model, &ir); err != nil {
			t.Fatal(err)
		}
		in := input(t, ir, 0, 0, 1, simulation.TraceNone)
		in.Config = c.Config
		res := run(t, &Engine{}, in).Result
		validResult(t, res)
		for _, s := range res.ReplicationSummaries {
			r := replication{CompletedCount: s.CompletedCount, MeanLeadTime: s.MeanLeadTimeMinutes, Reworks: s.Reworks,
				Utilization: s.ResourceUtilization, BlockedTicks: map[string]int64{}, FullTicks: map[string]int64{},
				BlocksStarted: map[string]int64{}, BlockedEntityTicks: map[string]int64{}}
			if s.ResourceStates == nil || s.Blocking == nil || s.Buffers == nil {
				t.Fatalf("%s: a model with finite buffers lacks their members", c.ID)
			}
			for id, st := range s.ResourceStates {
				r.BlockedTicks[id] = *st.BlockedTicks
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
