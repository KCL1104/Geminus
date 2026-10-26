package des

import (
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The Result members of batch processes (D-054, D-058; SIMULATION_SPEC §5)
// against testdata/simulation/batch-goldens.json, hand-derived; the
// observation-level facts of the same cases are in internal/simulation/engine.

func batchDoc(t *testing.T) []goldenCase {
	t.Helper()
	var doc struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/batch-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

// checkGoldenResult compares replication 0 of res with a hand-derived
// result: the line-flow members exactly (absent where the case says null),
// the existing members the case states, utilization against in_use_ticks
// over C·T, and the digest recomputed.
func checkGoldenResult(t *testing.T, res *simulation.Result, w goldenResult) {
	t.Helper()
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
	if !reflect.DeepEqual(s.Batches, w.Batches) {
		t.Errorf("batches %+v, hand-derived %+v", s.Batches, w.Batches)
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
		if lt.SampleCount != 0 || lt.Status != simulation.Unavailable {
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
}

// Every batch case with an expected result: batches[<process>] (D-054,
// D-058: batches started in [W, H] and their members), resource_states
// (a batch's bundle counted once in blocked_ticks however many members it
// has blocked), blocking and buffers exactly, and the existing members:
// lead time and queue wait per member, the forming wait included in the
// queue wait (D-054), utilization the chamber's busy time.
func TestBatchGoldenResults(t *testing.T) {
	checked := 0
	for _, c := range batchDoc(t) {
		if c.Expected.Result == nil {
			continue
		}
		checked++
		t.Run(c.ID, func(t *testing.T) {
			checkGoldenResult(t, run(t, &Engine{}, caseInput(t, c)).Result, *c.Expected.Result)
		})
	}
	if checked != 12 {
		t.Fatalf("%d cases with an expected result, want 12", checked)
	}
}

// The sampled trace of a batch (D-054, SIMULATION_SPEC §9): each member's
// service_start and service_end carry the batch ID r<rep>/<process>/b<n>,
// the members of one batch the same one, and no other event has one. On
// G-BT1's model with 400 entities: entity k is in batch ceil(k/4).
func TestBatchTrace(t *testing.T) {
	var c goldenCase
	for _, x := range batchDoc(t) {
		if x.ID == "G-BT1" {
			c = x
		}
	}
	ir := caseInput(t, c).Model
	ir.Nodes[0].Arrival.MaxEntities = 400 // nodes in document order: src, work, done
	in := input(t, ir, 4100, 0, 1, simulation.TraceSampled)
	in.Limits.MaxTraceEvents = 100_000
	out := run(t, &Engine{}, in)
	if out.Trace == nil {
		t.Fatal("no trace")
	}
	starts, ends := map[string]string{}, map[string]string{}
	for _, ev := range out.Trace.Events {
		switch ev.Type {
		case simulation.TraceServiceStart:
			starts[ev.EntityID] = ev.BatchID
		case simulation.TraceServiceEnd:
			ends[ev.EntityID] = ev.BatchID
		default:
			if ev.BatchID != "" {
				t.Errorf("%+v has a batch ID", ev)
			}
		}
	}
	for id, b := range starts {
		k, err := strconv.Atoi(id[strings.LastIndex(id, "/")+1:])
		if err != nil {
			t.Fatal(err)
		}
		if want := "r0/work/b" + strconv.Itoa((k+3)/4); b != want || ends[id] != b {
			t.Errorf("%s: service_start batch %q, service_end %q, want %s", id, b, ends[id], want)
		}
	}
	schemaValid(t, out.Trace, "simulation.schema.json#/$defs/TraceArtifact")
	t.Logf("%d sampled members", len(starts))
	if len(starts) < 20 {
		t.Fatalf("only %d sampled members", len(starts))
	}
}
