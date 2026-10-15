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
