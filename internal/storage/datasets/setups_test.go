package datasets_test

import (
	"encoding/json/v2"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// irWithSetups is the report example whose reviewer changes over (D-055,
// T-050): its default changeover samples dataset:review_duration
// (transition false), or the default is a constant and the changeover from
// change to hotfix samples it (transition true; hotfix is an entity type
// added for it). The review step is a constant, so only the changeover uses
// the dataset.
func irWithSetups(t *testing.T, transition bool) *model.ModelIR {
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
	s := map[string]any{"default": empirical}
	if transition {
		m["entity_types"] = append(m["entity_types"].([]any), map[string]any{"id": "hotfix", "name": "Hotfix"})
		s = map[string]any{"default": map[string]any{"type": "constant", "value": 30},
			"transitions": []any{map[string]any{"from": "change", "to": "hotfix", "distribution": empirical}}}
	}
	m["resources"].([]any)[0].(map[string]any)["setups"] = s
	data, _ := json.Marshal(m)
	v, err := validate.IR(data)
	if err != nil || !v.Valid() {
		t.Fatalf("report example with setups: %v %v", err, v.Issues)
	}
	return v.IR
}

// A changeover's empirical distribution needs its binding like a node's or
// a breakdown's, reported at the distribution's own path: the default is
// the distribution itself (setups/default), a transition's is under it
// (setups/transitions/<k>/distribution). A 0-tick sample may be a
// changeover (D-055: a changeover of 0 ticks is still one, D-097 (a)).
func TestSetupDatasets(t *testing.T) {
	good := root(t, "testdata/contracts/valid-dataset-snapshot.json")
	onlyIssue(t, resolve(t, single(good), irWithSetups(t, false)), validate.DatasetBindingInvalid,
		"/ir/resources/0/setups/default/samples_ref")
	onlyIssue(t, resolve(t, single(good), irWithSetups(t, true)), validate.DatasetBindingInvalid,
		"/ir/resources/0/setups/transitions/0/distribution/samples_ref")
	for _, transition := range []bool{false, true} {
		if r := resolve(t, single(good), irWithSetups(t, transition), bindingFor(good)); !r.Valid() || r.Datasets[ref].Binding != bindingFor(good) {
			t.Errorf("a bound changeover (transition %v): %v", transition, r.Issues)
		}
	}
	zero := snapshotBytes(t, func(m map[string]any) { m["samples"] = []any{0.0, 240.0, 360.0} })
	if r := resolve(t, single(zero), irWithSetups(t, false), bindingFor(zero)); !r.Valid() {
		t.Errorf("a 0-tick changeover sample: %v", r.Issues)
	}
}
