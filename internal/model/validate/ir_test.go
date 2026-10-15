package validate_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func run(t *testing.T, doc []byte) validate.Result {
	t.Helper()
	r, err := validate.IR(doc)
	if err != nil {
		t.Fatalf("internal error: %v", err)
	}
	return r
}

func codesAt(r validate.Result) map[string][]string {
	out := map[string][]string{}
	for _, i := range r.Issues {
		out[i.Path] = append(out[i.Path], i.Code)
	}
	return out
}

// The Go validator and the Python spec checks must agree on every shared
// fixture: positives are valid, schema negatives fail at their pinned paths,
// semantic negatives report their expected codes (TEST-001, TEST-002).
func TestAgreesWithSharedFixtures(t *testing.T) {
	root := contracttest.Root(t)
	var idx struct {
		Cases []struct {
			ID, Instance, Schema, Layer string
			Valid                       bool
			ExpectedErrorPaths          []string `json:"expected_error_paths"`
			ExpectedCodes               []string `json:"expected_codes"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/contracts/cases.json")), &idx,
		json.MatchCaseInsensitiveNames(true)); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range idx.Cases {
		if c.Schema != "schemas/model-ir.schema.json" {
			continue
		}
		n++
		r := run(t, contracttest.ReadFile(t, filepath.Join(root, c.Instance)))
		switch {
		case c.Valid:
			if !r.Valid() {
				t.Errorf("%s: want valid, got %v", c.ID, r.Issues)
			}
		case c.Layer == "schema":
			at := codesAt(r)
			if r.Valid() {
				t.Errorf("%s: schema negative accepted", c.ID)
			}
			for _, p := range c.ExpectedErrorPaths {
				if !slices.Contains(at[p], validate.IRSchemaInvalid) {
					t.Errorf("%s: no IR_SCHEMA_INVALID at %q; got %v", c.ID, p, r.Issues)
				}
			}
		default:
			var got []string
			for _, i := range r.Issues {
				got = append(got, i.Code)
			}
			for _, want := range c.ExpectedCodes {
				if !slices.Contains(got, want) {
					t.Errorf("%s: want %s, got %v", c.ID, want, r.Issues)
				}
			}
		}
	}
	golden, _ := filepath.Glob(filepath.Join(root, "testdata/golden-models/*.json"))
	for _, p := range golden {
		n++
		if r := run(t, contracttest.ReadFile(t, p)); !r.Valid() || r.IR == nil {
			t.Errorf("%s: want valid, got %v", filepath.Base(p), r.Issues)
		}
	}
	if n < 20 {
		t.Fatalf("only %d IR fixtures exercised", n)
	}
}

// edit returns the minimal valid IR changed by f.
func edit(t *testing.T, f func(ir map[string]any)) []byte {
	t.Helper()
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t),
		"testdata/contracts/valid-minimal-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	f(ir)
	out, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func nodes(ir map[string]any) []any { return ir["nodes"].([]any) }
func flows(ir map[string]any) []any { return ir["flows"].([]any) }
func obj(v any) map[string]any      { return v.(map[string]any) }
func flow(id, from, to string, p float64) map[string]any {
	return map[string]any{"id": id, "from": from, "to": to, "routing": map[string]any{"type": "probability", "probability": p}}
}

// minimal IR: start(source) -> work(process, worker) -> end(sink)
func TestRules(t *testing.T) {
	cases := []struct {
		name  string
		doc   func(t *testing.T) []byte
		code  string
		path  string
		valid bool // the issue is a warning and the IR stays valid
	}{
		{"duplicate member", func(*testing.T) []byte {
			return []byte(`{"name":"a","name":"b"}`)
		}, validate.JSONInvalid, "/name", false},
		{"invalid UTF-8", func(*testing.T) []byte { return []byte("{\"name\":\"\xff\"}") }, validate.JSONInvalid, "/name", false},
		{"trailing document", func(t *testing.T) []byte { return append(edit(t, func(map[string]any) {}), []byte("{}")...) },
			validate.JSONInvalid, "", false},
		{"document too large", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { ir["name"] = strings.Repeat("x", validate.MaxIRBytes) })
		}, validate.LimitExceeded, "", false},
		{"unknown member", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { obj(nodes(ir)[1])["script"] = "x" })
		}, validate.IRSchemaInvalid, "/nodes/1", false},
		{"metadata too deep", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				var v any = "leaf"
				for range validate.MaxContainerDepth + 1 {
					v = map[string]any{"k": v}
				}
				ir["metadata"] = v
			})
		}, validate.LimitExceeded, "/metadata", false},
		{"metadata too big", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["metadata"] = map[string]any{"k": strings.Repeat("x", validate.MaxContainerBytes)}
			})
		}, validate.LimitExceeded, "/metadata", false},
		{"duration over 365 days", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["time_unit"] = "day"
				obj(obj(obj(nodes(ir)[1])["service_time"])["distribution"])["value"] = 365.001
			})
		}, validate.LimitExceeded, "/nodes/1/service_time/distribution/value", false},
		{"duplicate node ID", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { obj(nodes(ir)[2])["id"] = "work" })
		}, validate.DuplicateID, "/nodes/2/id", false},
		{"node and resource share an ID", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				obj(ir["resources"].([]any)[0])["id"] = "work"
				obj(obj(nodes(ir)[1])["requires"].([]any)[0])["resource_id"] = "work"
			})
		}, validate.DuplicateID, "/resources/0/id", false},
		{"unknown flow target", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { obj(flows(ir)[1])["to"] = "nowhere" })
		}, validate.UnknownReference, "/flows/1/to", false},
		{"unknown resource", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { obj(obj(nodes(ir)[1])["requires"].([]any)[0])["resource_id"] = "ghost" })
		}, validate.UnknownReference, "/nodes/1/requires/0/resource_id", false},
		{"unknown metric sink", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["metrics"] = []any{map[string]any{"id": "tp", "type": "sink_throughput", "sink_id": "ghost"}}
			})
		}, validate.UnknownReference, "/metrics/0/sink_id", false},
		{"view position of a missing node", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["views"] = map[string]any{"2d": map[string]any{"positions": map[string]any{"ghost": map[string]any{"x": 0, "y": 0}}}, "3d": map[string]any{}}
			})
		}, validate.ViewReferenceInvalid, "/views/2d/positions/ghost", false},
		{"sink with an outgoing flow", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { ir["flows"] = append(flows(ir), flow("loop", "end", "work", 1)) })
		}, validate.RoutingInvalid, "/flows/2/from", false},
		{"process with two outgoing flows", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { ir["flows"] = append(flows(ir), flow("extra", "work", "end", 1)) })
		}, validate.RoutingInvalid, "/nodes/1", false},
		{"single flow below probability 1", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { obj(obj(flows(ir)[1])["routing"])["probability"] = 0.5 })
		}, validate.RoutingInvalid, "/flows/1/routing/probability", false},
		{"decision probabilities do not sum to 1", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
				obj(flows(ir)[1])["to"] = "d"
				ir["flows"] = append(flows(ir), flow("d_end", "d", "end", 0.6), flow("d_work", "d", "work", 0.3))
			})
		}, validate.RoutingInvalid, "/nodes/3", false},
		{"flow into a source", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
				obj(flows(ir)[1])["to"] = "d"
				ir["flows"] = append(flows(ir), flow("d_end", "d", "end", 0.5), flow("d_start", "d", "start", 0.5))
			})
		}, validate.RoutingInvalid, "/flows/3/to", false},
		{"queue feeding a sink", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["nodes"] = append(nodes(ir), map[string]any{"id": "q", "type": "queue"})
				obj(flows(ir)[1])["to"] = "q"
				ir["flows"] = append(flows(ir), flow("q_end", "q", "end", 1))
			})
		}, validate.RoutingInvalid, "/flows/2/to", false},
		{"no source", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				n := nodes(ir)
				ir["nodes"] = []any{map[string]any{"id": "start", "type": "queue"}, n[1], n[2]}
			})
		}, validate.NoReachableSink, "/nodes", false},
		{"entities trapped in a loop", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
				obj(flows(ir)[1])["to"] = "d"
				ir["flows"] = append(flows(ir), flow("d_work", "d", "work", 1), flow("d_end", "d", "end", 0))
			})
		}, validate.NoReachableSink, "/nodes/1", false},
		{"dormant node is only a warning", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["nodes"] = append(nodes(ir), map[string]any{"id": "idle", "type": "rework"})
				ir["flows"] = append(flows(ir), flow("idle_end", "idle", "end", 1))
			})
		}, validate.UnreachableNode, "/nodes/3", true},
		{"zero-time cycle", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				obj(obj(obj(nodes(ir)[1])["service_time"])["distribution"])["value"] = 0
				ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
				obj(flows(ir)[1])["to"] = "d"
				ir["flows"] = append(flows(ir), flow("d_work", "d", "work", 0.5), flow("d_end", "d", "end", 0.5))
			})
		}, validate.ZeroTimeCycle, "/nodes/1", false},
		{"triangular out of order", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				obj(obj(nodes(ir)[1])["service_time"])["distribution"] = map[string]any{"type": "triangular", "min": 3, "mode": 2, "max": 4}
			})
		}, validate.DistributionInvalid, "/nodes/1/service_time/distribution", false},
		{"zero interarrival", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				obj(obj(obj(nodes(ir)[0])["arrival"])["distribution"])["value"] = 0
			})
		}, validate.DistributionInvalid, "/nodes/0/arrival/distribution", false},
		{"demand above capacity", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) { obj(obj(nodes(ir)[1])["requires"].([]any)[0])["units"] = 2 })
		}, validate.ResourceRequirementInvalid, "/nodes/1/requires/0/units", false},
		{"resource required twice", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				n := obj(nodes(ir)[1])
				n["requires"] = append(n["requires"].([]any), map[string]any{"resource_id": "worker", "units": 1})
			})
		}, validate.ResourceRequirementInvalid, "/nodes/1/requires/1/resource_id", false},
		{"constraint on a sink", func(t *testing.T) []byte {
			return edit(t, func(ir map[string]any) {
				ir["constraints"] = []any{map[string]any{"type": "outgoing_probability_sum", "node_id": "end", "equals": 1}}
			})
		}, validate.ConstraintFailed, "/constraints/0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, c.doc(t))
			if !slices.Contains(codesAt(r)[c.path], c.code) {
				t.Fatalf("want %s at %q, got %v", c.code, c.path, r.Issues)
			}
			if r.Valid() != c.valid {
				t.Fatalf("Valid() = %v, want %v: %v", r.Valid(), c.valid, r.Issues)
			}
		})
	}
}

// Non-zero service or a 0% back edge is not a zero-time cycle.
func TestNotZeroTimeCycle(t *testing.T) {
	for name, service := range map[string]map[string]any{
		"positive constant": {"type": "constant", "value": 0.001},
		"exponential":       {"type": "exponential", "mean": 1},
	} {
		doc := edit(t, func(ir map[string]any) {
			obj(obj(nodes(ir)[1])["service_time"])["distribution"] = service
			ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
			obj(flows(ir)[1])["to"] = "d"
			ir["flows"] = append(flows(ir), flow("d_work", "d", "work", 0.5), flow("d_end", "d", "end", 0.5))
		})
		if r := run(t, doc); !r.Valid() {
			t.Errorf("%s: %v", name, r.Issues)
		}
	}
}

// The schema checks "mean > 0" on the exact decimal, but a mean such as
// 1e-400 decodes to binary64 0: it must not pass as a zero interarrival or
// hide a zero-time cycle (review of T-004/T-005, D-033).
func TestExponentialMeanUnderflow(t *testing.T) {
	const marker = 7777.25
	underflow := func(doc []byte) []byte {
		out := bytes.ReplaceAll(doc, []byte("7777.25"), []byte("1e-400"))
		if bytes.Equal(out, doc) {
			t.Fatal("marker not found")
		}
		return out
	}
	arrival := underflow(edit(t, func(ir map[string]any) {
		obj(obj(nodes(ir)[0])["arrival"])["distribution"] = map[string]any{"type": "exponential", "mean": marker}
	}))
	r := run(t, arrival)
	if r.Valid() || !slices.Contains(codesAt(r)["/nodes/0/arrival/distribution"], validate.DistributionInvalid) {
		t.Errorf("underflowing interarrival mean: %v", r.Issues)
	}
	service := underflow(edit(t, func(ir map[string]any) {
		obj(obj(nodes(ir)[1])["service_time"])["distribution"] = map[string]any{"type": "exponential", "mean": marker}
	}))
	r = run(t, service)
	if r.Valid() || !slices.Contains(codesAt(r)["/nodes/1/service_time/distribution"], validate.DistributionInvalid) {
		t.Errorf("underflowing service mean: %v", r.Issues)
	}
	// In a rework loop the earlier cycle stage already reports it (D-023).
	loop := underflow(edit(t, func(ir map[string]any) {
		obj(obj(nodes(ir)[1])["service_time"])["distribution"] = map[string]any{"type": "exponential", "mean": marker}
		ir["nodes"] = append(nodes(ir), map[string]any{"id": "d", "type": "decision"})
		obj(flows(ir)[1])["to"] = "d"
		ir["flows"] = append(flows(ir), flow("d_work", "d", "work", 0.5), flow("d_end", "d", "end", 0.5))
	}))
	r = run(t, loop)
	if r.Valid() || !slices.Contains(codesAt(r)["/nodes/1"], validate.ZeroTimeCycle) {
		t.Errorf("underflowing service mean in a loop: %v", r.Issues)
	}
	// The smallest positive binary64 is still positive and stays valid here;
	// the engine guarantees an exponential sample is at least one tick.
	tiny := bytes.ReplaceAll(edit(t, func(ir map[string]any) {
		obj(obj(nodes(ir)[0])["arrival"])["distribution"] = map[string]any{"type": "exponential", "mean": marker}
	}), []byte("7777.25"), []byte("5e-324"))
	if r := run(t, tiny); !r.Valid() {
		t.Errorf("subnormal mean: %v", r.Issues)
	}
}

// The metadata size limit applies to the canonical (RFC 8785) form, so
// whitespace never decides validity and two documents with the same model
// hash always get the same answer (review of T-004).
func TestContainerLimitIgnoresWhitespace(t *testing.T) {
	entries := map[string]any{}
	for i := range 60 { // the schema allows 64 members
		entries[strconv.Itoa(i)] = strings.Repeat("x", 256) // ≈ 15.9 KB compact
	}
	compact := edit(t, func(ir map[string]any) { ir["metadata"] = entries })
	if r := run(t, compact); !r.Valid() {
		t.Fatalf("compact metadata under the limit: %v", r.Issues)
	}
	var indented bytes.Buffer
	v := jsontext.Value(slices.Clone(compact))
	if err := v.Indent(jsontext.WithIndent("        ")); err != nil {
		t.Fatal(err)
	}
	indented.Write(v)
	var members map[string]jsontext.Value
	if err := json.Unmarshal(indented.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	if n := len(members["metadata"]); n <= validate.MaxContainerBytes {
		t.Fatalf("indentation did not push the raw metadata over the limit (%d bytes)", n)
	}
	if r := run(t, indented.Bytes()); !r.Valid() {
		t.Errorf("the same metadata, indented: %v", r.Issues)
	}
}

// A later stage does not run while an earlier one has errors (D-023).
func TestStagesStopAtFirstFailingStage(t *testing.T) {
	r := run(t, edit(t, func(ir map[string]any) {
		obj(flows(ir)[1])["to"] = "nowhere" // references stage
		obj(obj(nodes(ir)[1])["service_time"])["distribution"] = map[string]any{"type": "triangular", "min": 3, "mode": 2, "max": 4}
	}))
	for _, i := range r.Issues {
		if i.Code != validate.UnknownReference {
			t.Errorf("later-stage issue reported: %v", i)
		}
	}
	if len(r.Issues) == 0 {
		t.Fatal("no issues")
	}
}

// Issues sort by pointer with numeric indices, then code (D-023).
func TestIssueOrder(t *testing.T) {
	got := validate.Sort([]model.ValidationIssue{
		{Code: "B", Path: "/nodes/10/id"}, {Code: "A", Path: "/nodes/2/id"}, {Code: "Z", Path: "/flows/0"},
		{Code: "A", Path: "/nodes/2"}, {Code: "A", Path: "/nodes/2/id"},
	})
	var paths []string
	for _, i := range got {
		paths = append(paths, i.Path+" "+i.Code)
	}
	want := []string{"/flows/0 Z", "/nodes/2 A", "/nodes/2/id A", "/nodes/10/id B"}
	if !slices.Equal(paths, want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
}

// The compiled schemas are shared; validation must be safe in parallel (run with -race).
func TestConcurrentValidation(t *testing.T) {
	docs := [][]byte{
		edit(t, func(map[string]any) {}),
		edit(t, func(ir map[string]any) { obj(flows(ir)[1])["to"] = "nowhere" }),
		contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models/default-delivery.json")),
	}
	want := make([]validate.Result, len(docs))
	for i, d := range docs {
		want[i] = run(t, d)
	}
	var wg sync.WaitGroup
	for range 8 {
		for i, d := range docs {
			wg.Go(func() {
				r, err := validate.IR(d)
				if err != nil || !slices.Equal(r.Issues, want[i].Issues) {
					t.Errorf("doc %d: concurrent result differs: %v %v", i, err, r.Issues)
				}
			})
		}
	}
	wg.Wait()
}

// Arbitrary input never panics and always yields the same, sorted issues.
func FuzzIR(f *testing.F) {
	root := contracttest.Root(f)
	paths, _ := filepath.Glob(filepath.Join(root, "testdata/contracts/*ir*.json"))
	more, _ := filepath.Glob(filepath.Join(root, "testdata/golden-models/*.json"))
	for _, p := range append(paths, more...) {
		f.Add(contracttest.ReadFile(f, p))
	}
	f.Add([]byte(`{"schema_version":"0.1"`))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := validate.IR(data)
		if err != nil {
			t.Fatalf("internal error: %v", err)
		}
		b, _ := validate.IR(data)
		if !slices.Equal(a.Issues, b.Issues) {
			t.Fatal("non-deterministic issues")
		}
		if !slices.Equal(a.Issues, validate.Sort(a.Issues)) {
			t.Fatal("issues not sorted")
		}
		if a.Valid() && a.IR == nil {
			t.Fatal("valid result without IR")
		}
		_ = jsontext.Value(data)
	})
}

// A data point for T-037, not a performance gate: 500 nodes, ~750 flows.
func BenchmarkLargeIR(b *testing.B) {
	t := &testing.T{}
	doc := edit(t, func(ir map[string]any) {
		ns := []any{nodes(ir)[0]}
		var fs []any
		prev := "start"
		for i := range 249 {
			d, p := "d"+idx(i), "p"+idx(i)
			ns = append(ns, map[string]any{"id": d, "type": "decision"}, map[string]any{"id": p, "type": "process",
				"service_time": map[string]any{"distribution": map[string]any{"type": "exponential", "mean": 5}},
				"requires":     []any{map[string]any{"resource_id": "worker", "units": 1}}})
			fs = append(fs, flow(prev+"_"+d, prev, d, 1))
			if i > 0 {
				fs = fs[:len(fs)-1]
				fs = append(fs, flow("d"+idx(i-1)+"_"+d, "d"+idx(i-1), d, 0.5))
			}
			fs = append(fs, flow(d+"_"+p, d, p, map[bool]float64{true: 1, false: 0.5}[i == 248]), flow(p+"_end", p, "end", 1))
			prev = d
		}
		ir["nodes"] = append(ns, map[string]any{"id": "end", "type": "sink"})
		ir["flows"] = fs
	})
	if r := run(t, doc); !r.Valid() {
		b.Fatalf("benchmark model invalid: %v", r.Issues[:min(3, len(r.Issues))])
	}
	b.ReportMetric(float64(len(doc)), "doc-bytes")
	for b.Loop() {
		if _, err := validate.IR(doc); err != nil {
			b.Fatal(err)
		}
	}
}

func idx(i int) string { return strconv.Itoa(i) }
