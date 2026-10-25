package datasets_test

import (
	"encoding/json/v2"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// irWithBreakdowns is the report example with breakdowns on its first
// resource whose time to repair (member "time_to_repair") or time to
// failure samples dataset:review_duration, and the review step at a
// constant, so that only the resource uses the dataset (T-048: the
// binding stage reads the distributions of resources too).
func irWithBreakdowns(t *testing.T, member string) *model.ModelIR {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(root(t, "testdata/golden-models/report-example.json"), &m); err != nil {
		t.Fatal(err)
	}
	for _, n := range m["nodes"].([]any) {
		n := n.(map[string]any)
		if st, ok := n["service_time"].(map[string]any); ok && st["distribution"].(map[string]any)["type"] == "empirical" {
			st["distribution"] = map[string]any{"type": "constant", "value": 240}
		}
	}
	empirical := map[string]any{"type": "empirical", "samples_ref": ref}
	other := map[string]any{"type": "constant", "value": 600}
	b := map[string]any{"time_to_failure": map[string]any{"distribution": other}, "time_to_repair": map[string]any{"distribution": empirical}}
	if member == "time_to_failure" {
		b = map[string]any{"time_to_failure": map[string]any{"distribution": empirical}, "time_to_repair": map[string]any{"distribution": map[string]any{"type": "constant", "value": 5}}}
	}
	m["resources"].([]any)[0].(map[string]any)["breakdowns"] = b
	data, _ := json.Marshal(m)
	v, err := validate.IR(data)
	if err != nil || !v.Valid() {
		t.Fatalf("report example with breakdowns: %v %v", err, v.Issues)
	}
	return v.IR
}

func TestBreakdownDatasets(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	// A resource's empirical distribution needs its binding like a node's.
	onlyIssue(t, resolve(t, single(good), irWithBreakdowns(t, "time_to_repair")), validate.DatasetBindingInvalid,
		"/ir/resources/0/breakdowns/time_to_repair/distribution/samples_ref")
	if r := resolve(t, single(good), irWithBreakdowns(t, "time_to_repair"), bindingFor(good)); !r.Valid() || r.Datasets[ref].Binding != bindingFor(good) {
		t.Errorf("a bound repair time: %v", r.Issues)
	}
	// D-033 (D-051): a 0-tick sample may be a repair time, never a time to failure.
	zero := snapshotBytes(t, func(m map[string]any) { m["samples"] = []any{0.0, 240.0, 360.0} })
	if r := resolve(t, single(zero), irWithBreakdowns(t, "time_to_repair"), bindingFor(zero)); !r.Valid() {
		t.Errorf("a 0-tick repair sample: %v", r.Issues)
	}
	onlyIssue(t, resolve(t, single(zero), irWithBreakdowns(t, "time_to_failure"), bindingFor(zero)), validate.DistributionInvalid,
		"/ir/resources/0/breakdowns/time_to_failure/distribution")
	if r := resolve(t, single(good), irWithBreakdowns(t, "time_to_failure"), bindingFor(good)); !r.Valid() {
		t.Errorf("positive failure samples: %v", r.Issues)
	}
}
