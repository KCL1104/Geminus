package des

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/trace"
)

// The Result members of finite buffers (D-049, D-058; SIMULATION_SPEC §5)
// against testdata/simulation/blocking-goldens.json, hand-derived; the
// observation-level facts of the same cases are in internal/simulation/engine.

type goldenResult struct {
	CompletedCount      int64                               `json:"completed_count"`
	Censored            int64                               `json:"censored_entities"`
	LeadTimes           []float64                           `json:"lead_times_minutes"`
	QueueWaits          map[string][]float64                `json:"queue_waits_minutes"`
	ResourceUtilization map[string]float64                  `json:"resource_utilization"`
	Blocking            map[string]simulation.NodeBlocking  `json:"blocking"`
	Buffers             map[string]simulation.BufferState   `json:"buffers"`
	ResourceStates      map[string]simulation.ResourceState `json:"resource_states"`
}

type goldenCase struct {
	ID       string               `json:"id"`
	Model    jsontext.Value       `json:"model"`
	Config   simulation.RunConfig `json:"config"`
	Expected struct {
		Result *goldenResult `json:"result"`
	} `json:"expected"`
}

func blockingCases(t *testing.T) []goldenCase {
	t.Helper()
	var doc struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

func caseInput(t *testing.T, c goldenCase) simulation.RunInput {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(c.Model, &ir); err != nil {
		t.Fatal(err)
	}
	in := input(t, ir, 0, 0, 1, simulation.TraceNone)
	in.Config = c.Config
	return in
}

func quantile7(x []float64, p float64) float64 {
	s := slices.Clone(x)
	slices.Sort(s)
	h := float64(len(s)-1) * p
	lo, hi := math.Floor(h), math.Ceil(h)
	return s[int(lo)] + (h-lo)*(s[int(hi)]-s[int(lo)])
}

func average(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b)) }

// Every case with an expected result: the blocking, buffer and
// resource-state members exactly (absent when the case says null), and
// the existing members the case states (completed count, censored,
// utilization, lead times, queue waits).
func TestBlockingGoldenResults(t *testing.T) {
	checked := 0
	for _, c := range blockingCases(t) {
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
			if !reflect.DeepEqual(s.Blocking, w.Blocking) {
				t.Errorf("blocking %+v\n hand-derived %+v", s.Blocking, w.Blocking)
			}
			if !reflect.DeepEqual(s.Buffers, w.Buffers) {
				t.Errorf("buffers %+v\n hand-derived %+v", s.Buffers, w.Buffers)
			}
			if !reflect.DeepEqual(s.ResourceStates, w.ResourceStates) {
				t.Errorf("resource states %v\n hand-derived %v", states(s.ResourceStates), states(w.ResourceStates))
			}
			for r, u := range w.ResourceUtilization {
				if !near(s.ResourceUtilization[r], u) {
					t.Errorf("utilization of %s %v, hand-derived %v", r, s.ResourceUtilization[r], u)
				}
			}
			if len(w.LeadTimes) > 0 {
				lt := res.Metrics.LeadTimeMinutes
				if lt.SampleCount != int64(len(w.LeadTimes)) || !near(*lt.Mean, average(w.LeadTimes)) || !near(*lt.P95, quantile7(w.LeadTimes, 0.95)) ||
					!near(*s.MeanLeadTimeMinutes, average(w.LeadTimes)) {
					t.Errorf("lead time %+v, hand-derived %v", lt, w.LeadTimes)
				}
			}
			for q, waits := range w.QueueWaits {
				st := res.Metrics.Queues[q]
				if st.SampleCount != int64(len(waits)) || !near(*st.WaitMeanMinutes, average(waits)) || !near(*st.WaitP95Minutes, quantile7(waits, 0.95)) {
					t.Errorf("queue %s %+v, hand-derived waits %v", q, st, waits)
				}
			}
			// The members are part of the digest, which recomputes.
			c := *res
			c.Reproducibility.ResultDigest = ""
			if d, err := hashing.Result(c); err != nil || d != res.Reproducibility.ResultDigest {
				t.Errorf("digest %s, recomputed %s %v", res.Reproducibility.ResultDigest, d, err)
			}
		})
	}
	if checked < 8 {
		t.Fatalf("%d cases with an expected result", checked)
	}
}

func states(m map[string]simulation.ResourceState) map[string][3]int64 {
	out := map[string][3]int64{}
	for k, v := range m {
		b := int64(-1)
		if v.BlockedTicks != nil {
			b = *v.BlockedTicks
		}
		out[k] = [3]int64{v.InUseTicks, b, v.ScheduledProcessingTicks}
	}
	return out
}

// D-047: a model without a finite buffer has none of the new members, and
// its Result encodes without them.
func TestNoFiniteBufferNoNewMembers(t *testing.T) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		in := input(t, golden(t, name), 600, 60, 2, simulation.TraceSampled)
		review := simulation.ResolvedDataset{
			Binding: model.DatasetBinding{LogicalRef: "dataset:review_duration", DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40",
				ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3},
			Samples: []float64{120, 240, 360},
		}
		in.Datasets = map[string]simulation.ResolvedDataset{review.Binding.LogicalRef: review}
		in.Manifest = []model.DatasetBinding{review.Binding}
		res := run(t, &Engine{}, in).Result
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range res.ReplicationSummaries {
			if s.ResourceStates != nil || s.Blocking != nil || s.Buffers != nil {
				t.Errorf("%s: line-flow members %+v", name, s)
			}
		}
		for _, member := range []string{"resource_states", "blocking", "buffers", "blocked_ticks"} {
			if slices.Contains(jsonKeys(t, data), member) {
				t.Errorf("%s: the Result has %s", name, member)
			}
		}
	}
}

func jsonKeys(t *testing.T, data []byte) []string {
	t.Helper()
	var out []string
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return out
		}
		if tok.Kind() == '"' {
			if kind, n := dec.StackIndex(dec.StackDepth()); kind == '{' && n%2 == 1 {
				out = append(out, tok.String())
			}
		}
	}
}

// Trace: blocked and unblocked at the node the entity is blocked at, in
// engine order between its other events (D-049, SIMULATION_SPEC §9).
func TestBlockedEntitiesInTheTrace(t *testing.T) {
	var c goldenCase
	for _, x := range blockingCases(t) {
		if x.ID == "G-B4" {
			c = x
		}
	}
	in := caseInput(t, c)
	in.Config.TraceLevel = simulation.TraceSampled
	out := run(t, &Engine{}, in)
	if out.Trace == nil {
		t.Fatal("no trace")
	}
	schemaValid(t, out.Trace, "simulation.schema.json#/$defs/TraceArtifact")
	// G-B4: e_k (k >= 3) is blocked at src over [k, 2k-3] (still blocked at
	// the horizon from k = 12), then enters qw.
	blocked := 0
	for k := 3; k <= 20; k++ {
		id := engineEntityID("src", k)
		if !trace.Selected(id) {
			continue
		}
		var types []simulation.TraceEventType
		var at []int64
		for _, e := range out.Trace.Events {
			if e.EntityID == id {
				types = append(types, e.Type)
				at = append(at, e.TimeTick/60_000)
				if (e.Type == simulation.TraceBlocked || e.Type == simulation.TraceUnblocked) && e.NodeID != "src" {
					t.Errorf("%s: %s at %s", id, e.Type, e.NodeID)
				}
			}
		}
		want := []simulation.TraceEventType{simulation.TraceArrival, simulation.TraceBlocked}
		wantAt := []int64{int64(k), int64(k)}
		if 2*k-3 <= 20 {
			want = append(want, simulation.TraceUnblocked, simulation.TraceQueueEnter)
			wantAt = append(wantAt, int64(2*k-3), int64(2*k-3))
			if 2*k-1 <= 20 {
				want = append(want, simulation.TraceServiceStart)
				wantAt = append(wantAt, int64(2*k-1))
			}
			if 2*k+1 <= 20 {
				want = append(want, simulation.TraceServiceEnd, simulation.TraceComplete)
				wantAt = append(wantAt, int64(2*k+1), int64(2*k+1))
			}
		}
		blocked++
		if !slices.Equal(types, want) || !slices.Equal(at, wantAt) {
			t.Errorf("%s: %v at %v, want %v at %v", id, types, at, want, wantAt)
		}
	}
	if blocked == 0 {
		t.Fatal("no traced entity of G-B4 is blocked")
	}
}

func engineEntityID(source string, n int) string {
	return "r0/" + source + "/" + strconv.Itoa(n)
}

// G-B5: which finite-buffer members a Result has does not depend on the
// routing draws: every resource, the nodes whose route can reach a finite
// buffer (a, through the decision; not src, before a's unlimited buffer)
// and the finite buffers (hand-derived in blocking-goldens.json).
func TestBlockingMemberKeys(t *testing.T) {
	var doc struct {
		Cases []struct {
			ID       string               `json:"id"`
			Model    jsontext.Value       `json:"model"`
			Config   simulation.RunConfig `json:"config"`
			Expected struct {
				Keys map[string][]string `json:"member_keys"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range doc.Cases {
		if c.Expected.Keys == nil {
			continue
		}
		checked++
		res := run(t, &Engine{}, caseInput(t, goldenCase{ID: c.ID, Model: c.Model, Config: c.Config})).Result
		validResult(t, res)
		s := res.ReplicationSummaries[0]
		for member, keys := range map[string][]string{
			"blocking": slices.Sorted(maps.Keys(s.Blocking)), "buffers": slices.Sorted(maps.Keys(s.Buffers)),
			"resource_states": slices.Sorted(maps.Keys(s.ResourceStates)),
		} {
			if !slices.Equal(keys, c.Expected.Keys[member]) {
				t.Errorf("%s: %s keys %v, hand-derived %v", c.ID, member, keys, c.Expected.Keys[member])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no case lists member keys")
	}
}
