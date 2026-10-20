package model_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
)

// Go type <-> schema location. Every exported contract type is listed so a new
// schema property, or a renamed Go field, fails TestSchemaParity.
var parity = []struct {
	typ reflect.Type
	ref string
}{
	{reflect.TypeFor[model.ModelIR](), "model-ir.schema.json#"},
	{reflect.TypeFor[model.Patch](), "json-patch.schema.json#"},
	{reflect.TypeFor[model.DatasetBinding](), "common.schema.json#/$defs/DatasetBinding"},
	{reflect.TypeFor[model.ValidationIssue](), "common.schema.json#/$defs/ValidationIssue"},
}

func TestSchemaParity(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	for _, p := range parity {
		s.Parity(t, p.typ, p.ref)
	}
}

func TestDiscriminatorsMatchSchema(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	checks := []struct {
		name string
		got  []string
		ref  string
		key  string
	}{
		{"node types", strs(model.NodeTypes), "model-ir.schema.json#/properties/nodes/items", "type"},
		{"distributions", strs(model.DistributionTypes), "model-ir.schema.json#/$defs/Distribution", "type"},
		{"metric types", strs(model.MetricTypes), "model-ir.schema.json#/properties/metrics/items", "type"},
		{"release processes", strs(model.ReleaseProcesses), "model-ir.schema.json#/$defs/ReleaseSchedule", "process"},
		{"patch ops", strs(model.PatchOpKinds), "json-patch.schema.json#/items", "op"},
	}
	for _, c := range checks {
		if want := s.Consts(c.ref, c.key); !slices.Equal(c.got, want) {
			t.Errorf("%s: Go %v, schema %v", c.name, c.got, want)
		}
	}
}

func strs[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// TEST-001 / TEST-003 (shape): every positive IR and patch fixture decodes
// strictly and round-trips without losing or inventing members.
func TestPositiveFixturesRoundTrip(t *testing.T) {
	root := contracttest.Root(t)
	var n int
	for _, c := range contracttest.Cases(t) {
		if !c.Valid || c.Layer != "schema" {
			continue
		}
		data := contracttest.ReadFile(t, filepath.Join(root, c.Instance))
		switch c.Schema {
		case "schemas/model-ir.schema.json":
			contracttest.RoundTrip[model.ModelIR](t, c.ID, data)
		case "schemas/json-patch.schema.json":
			contracttest.RoundTrip[model.Patch](t, c.ID, data)
		default:
			continue
		}
		n++
	}
	golden, _ := filepath.Glob(filepath.Join(root, "testdata", "golden-models", "*.json"))
	for _, path := range golden {
		contracttest.RoundTrip[model.ModelIR](t, filepath.Base(path), contracttest.ReadFile(t, path))
		n++
	}
	// The IR and dataset bindings embedded in a stored model version.
	var version struct {
		IR       jsontext.Value `json:"ir"`
		Bindings jsontext.Value `json:"dataset_bindings"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata", "contracts",
		"valid-model-version.json")), &version); err != nil {
		t.Fatal(err)
	}
	contracttest.RoundTrip[model.ModelIR](t, "valid-model-version.ir", version.IR)
	contracttest.RoundTrip[[]model.DatasetBinding](t, "valid-model-version.dataset_bindings", version.Bindings)
	if n < 10 {
		t.Fatalf("only %d fixtures exercised; cases.json or golden-models moved?", n)
	}
}

// D-047: the Go types write none of the optional line-flow members a model
// does not have, and keep every one it has, so decoding and re-encoding any
// IR fixture or golden model leaves its model_hash unchanged. The engine and
// the run service hash the re-encoded form (T-022), so an older model's
// stored hash and results stay valid.
func TestReencodingKeepsTheModelHash(t *testing.T) {
	root := contracttest.Root(t)
	paths, _ := filepath.Glob(filepath.Join(root, "testdata", "golden-models", "*.json"))
	for _, c := range contracttest.Cases(t) {
		if c.Schema == "schemas/model-ir.schema.json" && (c.Valid || c.Layer != "schema") {
			paths = append(paths, filepath.Join(root, c.Instance))
		}
	}
	lineFlow := 0
	for _, path := range paths {
		data := contracttest.ReadFile(t, path)
		var ir model.ModelIR
		if err := model.Decode(data, &ir); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		again, err := model.Encode(&ir)
		if err != nil {
			t.Fatal(err)
		}
		want, err1 := hashing.Model(data)
		got, err2 := hashing.Model(again)
		if err1 != nil || err2 != nil || got != want {
			t.Errorf("%s: model_hash %s after re-encoding, %s before (%v, %v)", filepath.Base(path), got, want, err1, err2)
		}
		if usesLineFlow(&ir) {
			lineFlow++
		}
	}
	if len(paths) < 30 || lineFlow < 10 {
		t.Fatalf("%d models, %d with line-flow members; cases.json or golden-models moved?", len(paths), lineFlow)
	}
}

func usesLineFlow(ir *model.ModelIR) bool {
	if len(ir.Calendars) > 0 {
		return true
	}
	for _, r := range ir.Resources {
		if r.CalendarID != "" || r.ShiftCapacity != nil || r.Breakdowns != nil || r.Setups != nil {
			return true
		}
	}
	for i, n := range ir.Nodes {
		if n.Capacity > 0 || n.Batch != nil || n.Arrival != nil && n.Arrival.Schedule != nil {
			return true
		}
		for _, d := range ir.Nodes[i].Distributions() {
			if d.Distribution.Type == model.DistLognormal || d.Distribution.Type == model.DistWeibull {
				return true
			}
		}
	}
	for _, m := range ir.Metrics {
		if slices.Contains(model.MetricTypes[6:], m.Type) {
			return true
		}
	}
	return false
}

// A source with a release schedule has no arrival distribution to visit,
// and its zero Distribution is not written back (D-053).
func TestScheduleSourceHasNoArrivalDistribution(t *testing.T) {
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts",
		"valid-line-flow-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := range ir.Nodes {
		node := &ir.Nodes[i]
		if node.Arrival == nil || node.Arrival.Schedule == nil {
			continue
		}
		n++
		if d := node.Distributions(); len(d) != 0 || node.Arrival.Distribution != (model.Distribution{}) {
			t.Errorf("%s: distributions %v", node.ID, d)
		}
		out, err := model.Encode(node.Arrival)
		if err != nil || strings.Contains(string(out), "distribution") {
			t.Errorf("%s: arrival encodes as %s (%v)", node.ID, out, err)
		}
	}
	if n != 3 {
		t.Fatalf("%d sources with a schedule, want 3", n)
	}
}

// Decoding rejects what the IR profile forbids before any validator runs.
func TestStrictDecodeRejects(t *testing.T) {
	base := string(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts",
		"valid-minimal-ir.json")))
	cases := map[string]string{
		"unknown root member":   strings.Replace(base, `"schema_version"`, `"script": "rm -rf /", "schema_version"`, 1),
		"unknown nested member": strings.Replace(base, `"id": "work"`, `"id": "work", "expression": "1+1"`, 1),
		"duplicate member":      strings.Replace(base, `"name": "Minimal valid model"`, `"name": "a", "name": "b"`, 1),
		"member case mismatch":  strings.Replace(base, `"schema_version"`, `"Schema_Version"`, 1),
		"invalid UTF-8":         strings.Replace(base, "Minimal valid model", "bad \xff byte", 1),
		"trailing document":     base + "{}",
	}
	for name, doc := range cases {
		if doc == base {
			t.Fatalf("%s: fixture edit did not apply", name)
		}
		var ir model.ModelIR
		if err := model.Decode([]byte(doc), &ir); err == nil {
			t.Errorf("%s: decoded without error", name)
		}
	}
	var ok model.ModelIR
	if err := model.Decode([]byte(base), &ok); err != nil {
		t.Fatalf("control: %v", err)
	}
}

// A patch value of null/false/0 is data, not "absent" (RFC 6902 test/replace).
func TestPatchValueKeepsFalsyJSON(t *testing.T) {
	for _, v := range []string{"null", "false", "0", `""`, "[]", "{}"} {
		doc := `[{"op":"test","path":"/name","value":` + v + `},{"op":"remove","path":"/metadata"}]`
		contracttest.RoundTrip[model.Patch](t, "value "+v, []byte(doc))
	}
}

// Zero values that carry meaning must survive: requires [], a constant of 0,
// probability 0 and empty positions are all distinct from "absent".
func TestMeaningfulZeroValuesSurvive(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata",
		"contracts", "valid-minimal-ir.json")), &doc); err != nil {
		t.Fatal(err)
	}
	work := doc["nodes"].([]any)[1].(map[string]any)
	work["requires"] = []any{}
	work["service_time"].(map[string]any)["distribution"].(map[string]any)["value"] = 0
	doc["flows"].([]any)[0].(map[string]any)["routing"].(map[string]any)["probability"] = 0
	doc["views"].(map[string]any)["2d"] = map[string]any{"positions": map[string]any{}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	contracttest.RoundTrip[model.ModelIR](t, "zero values", data)
}

func TestNoForbiddenDependencies(t *testing.T) {
	contracttest.OnlyDeps(t)
}
