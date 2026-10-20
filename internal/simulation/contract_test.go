package simulation_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/simulation"
)

func TestSchemaParity(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	for typ, ref := range map[reflect.Type]string{
		reflect.TypeFor[simulation.RunConfig]():       "simulation.schema.json#/$defs/RunConfig",
		reflect.TypeFor[simulation.ExecutionLimits](): "simulation.schema.json#/$defs/ExecutionLimits",
		reflect.TypeFor[simulation.MetricSummary]():   "simulation.schema.json#/$defs/MetricSummary",
		reflect.TypeFor[simulation.Result]():          "simulation.schema.json#/$defs/Result",
		reflect.TypeFor[simulation.TraceArtifact]():   "simulation.schema.json#/$defs/TraceArtifact",
		reflect.TypeFor[simulation.Duration]():        "common.schema.json#/$defs/Duration",
	} {
		s.Parity(t, typ, ref)
	}
}

// The trace event types are the schema's enum, in order (ADR-026 added
// blocked, unblocked and setup_start).
func TestTraceEventTypesMatchSchema(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	var want []string
	for _, v := range s.At("simulation.schema.json#/$defs/TraceEvent").Value["properties"].(map[string]any)["type"].(map[string]any)["enum"].([]any) {
		want = append(want, v.(string))
	}
	var got []string
	for _, v := range simulation.TraceEventTypes {
		got = append(got, string(v))
	}
	if !slices.Equal(got, want) {
		t.Errorf("Go %v, schema %v", got, want)
	}
}

// D-047: a Result without line-flow members encodes none of them, so the
// digest of every older result is unchanged; one with them keeps them all.
func TestLineFlowResultMembersOnlyWhenPresent(t *testing.T) {
	root := contracttest.Root(t)
	for name, want := range map[string]bool{"valid-result.json": false, "valid-result-line-flow.json": true} {
		var r simulation.Result
		if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata", "contracts", name)), &r); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range []string{`"domain"`, `"resource_states"`, `"blocking"`, `"buffers"`, `"batches"`, `"declared"`} {
			if strings.Contains(string(out), member) != want {
				t.Errorf("%s: %s present = %v", name, member, !want)
			}
		}
	}
}

func TestFixturesRoundTrip(t *testing.T) {
	root := contracttest.Root(t)
	var n int
	for _, c := range contracttest.Cases(t) {
		if !c.Valid || c.Layer != "schema" {
			continue
		}
		data := contracttest.ReadFile(t, filepath.Join(root, c.Instance))
		switch c.Schema {
		case "schemas/simulation.schema.json#/$defs/Result":
			contracttest.RoundTrip[simulation.Result](t, c.ID, data)
		case "schemas/simulation.schema.json#/$defs/TraceArtifact":
			contracttest.RoundTrip[simulation.TraceArtifact](t, c.ID, data)
		case "schemas/simulation.schema.json": // a RunRequest; its config is a RunConfig
			var req struct {
				Config jsontext.Value `json:"config"`
			}
			if err := json.Unmarshal(data, &req); err != nil {
				t.Fatal(err)
			}
			contracttest.RoundTrip[simulation.RunConfig](t, c.ID+".config", req.Config)
		default:
			continue
		}
		n++
	}
	if n < 4 {
		t.Fatalf("only %d fixtures exercised", n)
	}
}

// "unavailable" must stay null, never become 0 (TEST-022, invalid-unavailable-is-zero).
func TestNullStaysNull(t *testing.T) {
	var r simulation.Result
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "valid-result.json"))
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	for name, m := range r.Metrics.DORA {
		if m.Status == simulation.Unavailable && (m.Mean != nil || m.P50 != nil || m.P95 != nil) {
			t.Errorf("dora %s: unavailable metric decoded with a value", name)
		}
	}
	contracttest.RoundTrip[simulation.Result](t, "valid-result", data)
}

// The engine package may depend on the IR types and nothing else outside the
// standard library: no software-delivery pack, HTTP, DB driver or LLM SDK.
func TestNoForbiddenDependencies(t *testing.T) {
	contracttest.OnlyDeps(t, "github.com/kcl1104/geminus/internal/model")
}
