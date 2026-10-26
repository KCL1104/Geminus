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

// The Result members of changeovers (D-055, D-058; SIMULATION_SPEC §5)
// against testdata/simulation/setup-goldens.json, hand-derived; the
// observation-level facts of the same cases are in internal/simulation/engine.

func setupDoc(t *testing.T) []goldenCase {
	t.Helper()
	var doc struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/setup-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

// Every setup case with an expected result: resource_states with the
// setup group (∫Z, changeovers started in [W, H], complete ones and their
// ticks) and scheduled_processing_ticks = ∫min(U − K − Z, S), the other
// groups where their features apply, blocking and buffers exactly, and the
// existing members: lead time, queue wait up to the grant (D-055),
// utilization ∫U/(C·T) with the changeover inside U.
func TestSetupGoldenResults(t *testing.T) {
	checked := 0
	for _, c := range setupDoc(t) {
		if c.Expected.Result == nil {
			continue
		}
		checked++
		t.Run(c.ID, func(t *testing.T) {
			checkGoldenResult(t, run(t, &Engine{}, caseInput(t, c)).Result, *c.Expected.Result)
		})
	}
	if checked != 8 {
		t.Fatalf("%d cases with an expected result, want 8", checked)
	}
}

// The sampled trace of changeovers (D-055, SIMULATION_SPEC §9): each
// changeover is a setup_start at the process with the resource changing
// over, in the grant's event, right before the entity's service_start; no
// other event has a resource_id. On G-SU1's model over 3000 minutes.
func TestSetupTrace(t *testing.T) {
	var c goldenCase
	for _, x := range setupDoc(t) {
		if x.ID == "G-SU1" {
			c = x
		}
	}
	in := input(t, caseInput(t, c).Model, 3000, 0, 1, simulation.TraceSampled)
	in.Limits.MaxTraceEvents = 100_000
	out := run(t, &Engine{}, in)
	if out.Trace == nil {
		t.Fatal("no trace")
	}
	evs := out.Trace.Events
	setups := 0
	last := map[string]int{} // entity -> the index of its last event
	for i, ev := range evs {
		if ev.Type != simulation.TraceSetupStart {
			if ev.ResourceID != "" {
				t.Errorf("%+v has a resource ID", ev)
			}
			if ev.Type == simulation.TraceServiceStart {
				if j, ok := last[ev.EntityID]; ok && evs[j].Type == simulation.TraceSetupStart &&
					(evs[j].Sequence != ev.Sequence || evs[j].TimeTick != ev.TimeTick || evs[j].NodeID != ev.NodeID) {
					t.Errorf("setup_start %+v and service_start %+v are not in one grant", evs[j], ev)
				}
			}
			last[ev.EntityID] = i
			continue
		}
		setups++
		if ev.ResourceID != "smt" || ev.NodeID != "place" {
			t.Errorf("setup_start %+v", ev)
		}
		// The next event of the same entity is its service start.
		next := slices.IndexFunc(evs[i+1:], func(x simulation.TraceEvent) bool { return x.EntityID == ev.EntityID })
		if next < 0 || evs[i+1+next].Type != simulation.TraceServiceStart {
			t.Errorf("setup_start %+v is not followed by the entity's service_start", ev)
		}
		// A changeover on G-SU1 is only ever to a piece whose type differs
		// from the one before it on the unit; board_b pieces start every
		// 20 minutes, each with a changeover both ways around it.
		last[ev.EntityID] = i
	}
	schemaValid(t, out.Trace, "simulation.schema.json#/$defs/TraceArtifact")
	t.Logf("%d sampled changeovers", setups)
	if setups < 20 {
		t.Fatalf("only %d sampled changeovers", setups)
	}
}

// neutralSetups gives every resource of ir changeovers that never take
// place (D-047 step 2, D-055 G-S4): the golden models have one entity
// type, every process needs one unit of one resource, so setups without
// initial, a default and transitions to and from a type no source makes
// never change a unit over.
func neutralSetups(t *testing.T, ir model.ModelIR) model.ModelIR {
	t.Helper()
	if len(ir.EntityTypes) != 1 {
		t.Fatalf("%d entity types", len(ir.EntityTypes))
	}
	only := ir.EntityTypes[0].ID
	ir.EntityTypes = append(slices.Clone(ir.EntityTypes), model.EntityType{ID: "spare_type", Name: "Spare"})
	ir.Resources = slices.Clone(ir.Resources)
	five, one := 5.0, 1.0
	for i := range ir.Resources {
		ir.Resources[i].Setups = &model.Setups{Default: &model.Distribution{Type: model.DistConstant, Value: &five},
			Transitions: []model.SetupTransition{{From: only, To: "spare_type", Distribution: model.Distribution{Type: model.DistConstant, Value: &one}}}}
	}
	return ir
}

// D-047 for changeovers: a model without setups has no setup member; with
// neutral setups every existing member and the trace are as without them,
// and every resource gains a resource_states entry with the setup group at
// 0 and scheduled processing = in use (no finite buffer, no calendar).
func TestNeutralSetupsKeepTheResult(t *testing.T) {
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
		for _, member := range []string{"setup_ticks", "resource_states"} {
			if slices.Contains(jsonKeys(t, data), member) {
				t.Errorf("%s: the Result has %s", name, member)
			}
		}
		nin := in
		nin.Model = neutralSetups(t, in.Model)
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
			t.Errorf("%s: neutral setups changed the metrics or the trace", name)
		}
		for i, s := range neutral.Result.ReplicationSummaries {
			b := base.Result.ReplicationSummaries[i]
			if len(s.ResourceStates) != len(nin.Model.Resources) {
				t.Errorf("%s r%d: resource states %+v", name, i, s.ResourceStates)
			}
			for id, st := range s.ResourceStates {
				if st.SetupTicks == nil || *st.SetupTicks != 0 || *st.SetupsStarted != 0 || *st.SetupsCompleted != 0 || *st.CompletedSetupTicks != 0 ||
					st.ScheduledProcessingTicks != st.InUseTicks || st.BlockedTicks != nil || st.DownTicks != nil || st.ScheduledTicks != nil {
					t.Errorf("%s r%d: %s %+v", name, i, id, st)
				}
			}
			s.ResourceStates = nil
			if !reflect.DeepEqual(s, b) {
				t.Errorf("%s r%d: %+v\n without setups %+v", name, i, s, b)
			}
		}
	}
}

// D-058's identities for batches and changeovers on stochastic runs (the
// eight reference cases of batch-goldens.json and setup-goldens.json, 30
// replications each): a batch has min_size to max_size members, so
// min_size·batches <= members <= max_size·batches, and every batch
// process is listed; a complete changeover started in [W, H], so
// setups_completed <= setups_started and completed_setup_ticks <=
// setup_ticks; a unit changing over is in use and not blocked (Z + K <=
// U), and without a calendar scheduled processing = in use - blocked -
// changing over (P = U - K - Z), with one at most that.
func TestBatchAndSetupIdentities(t *testing.T) {
	var cases []goldenCase
	for _, file := range []string{"batch-goldens.json", "setup-goldens.json"} {
		var doc struct {
			Reference struct {
				Cases []goldenCase `json:"cases"`
			} `json:"reference"`
		}
		if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation", file)), &doc); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, doc.Reference.Cases...)
	}
	if len(cases) != 8 {
		t.Fatalf("%d reference cases, want 8", len(cases))
	}
	var batches, partial, setupTicks, completed int64
	for _, c := range cases {
		in := caseInput(t, c)
		res := run(t, &Engine{}, in).Result
		validResult(t, res)
		sizes := map[string][2]int64{}
		for _, n := range in.Model.Nodes {
			if n.Batch != nil {
				sizes[n.ID] = [2]int64{int64(n.Batch.MinSize), int64(n.Batch.MaxSize)}
			}
		}
		setups, calendared := map[string]bool{}, map[string]bool{}
		for _, r := range in.Model.Resources {
			setups[r.ID], calendared[r.ID] = r.Setups != nil, r.CalendarID != ""
		}
		for _, s := range res.ReplicationSummaries {
			if len(s.Batches) != len(sizes) {
				t.Errorf("%s r%d: batches %+v for the batch processes %v", c.ID, s.ReplicationIndex, s.Batches, sizes)
			}
			for id, b := range s.Batches {
				size, ok := sizes[id]
				if !ok || b.MembersStarted < size[0]*b.BatchesStarted || b.MembersStarted > size[1]*b.BatchesStarted {
					t.Errorf("%s r%d %s: %+v, sizes %v", c.ID, s.ReplicationIndex, id, b, size)
				}
				batches += b.BatchesStarted
				if b.MembersStarted < size[1]*b.BatchesStarted {
					partial++
				}
			}
			for id, st := range s.ResourceStates {
				if !setups[id] {
					if st.SetupTicks != nil || st.SetupsStarted != nil || st.SetupsCompleted != nil || st.CompletedSetupTicks != nil {
						t.Errorf("%s r%d %s: setup members without setups: %+v", c.ID, s.ReplicationIndex, id, st)
					}
					continue
				}
				if st.SetupTicks == nil || st.SetupsStarted == nil || st.SetupsCompleted == nil || st.CompletedSetupTicks == nil {
					t.Fatalf("%s r%d %s: %+v", c.ID, s.ReplicationIndex, id, st)
				}
				z, k := *st.SetupTicks, int64(0)
				if st.BlockedTicks != nil {
					k = *st.BlockedTicks
				}
				ok := *st.SetupsCompleted <= *st.SetupsStarted && *st.CompletedSetupTicks <= z && z+k <= st.InUseTicks &&
					st.ScheduledProcessingTicks <= st.InUseTicks-k-z
				if !calendared[id] {
					ok = ok && st.ScheduledProcessingTicks == st.InUseTicks-k-z
				}
				if !ok {
					t.Errorf("%s r%d %s: %+v", c.ID, s.ReplicationIndex, id, st)
				}
				setupTicks += z
				completed += *st.SetupsCompleted
			}
			for id := range setups {
				if _, ok := s.ResourceStates[id]; setups[id] && !ok {
					t.Errorf("%s r%d: %s has setups but no resource_states", c.ID, s.ReplicationIndex, id)
				}
			}
		}
	}
	if batches == 0 || partial == 0 || setupTicks == 0 || completed == 0 {
		t.Fatalf("the reference cases have %d batches (%d replications with one below max_size), %d ticks changing over, %d complete changeovers",
			batches, partial, setupTicks, completed)
	}
}
