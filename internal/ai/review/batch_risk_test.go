package review

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057 (T-050): batch processes through the whole review. A scenario may
// replace a batch's min_size, max_size and max_wait; any change to them,
// either way, is at least medium, and a candidate whose batch cannot form
// (BATCH_INVALID) is invalid. Adding or removing a batch is an edit.
//
// The base is default-delivery.json after a person's edit (a draft
// publish): test (node 6) is a batch process with min_size 2, max_size 4
// and max_wait 30.
func batchBase(t *testing.T) []byte {
	t.Helper()
	edit := `[{"op":"test","path":"/nodes/6/id","value":"test"},
		{"op":"add","path":"/nodes/6/batch","value":{"min_size":2,"max_size":4,"max_wait":30}}]`
	res, err := patch.Apply(base(t), []byte(edit), patch.ManualDraft)
	if err != nil || !res.Applied() {
		t.Fatalf("base edit: %v %v", err, res.PatchIssues)
	}
	if r := validateDoc(t, res.Candidate); !r.Valid() {
		t.Fatalf("base: %v", r.Issues)
	}
	return res.Candidate
}

func TestBatchRisk(t *testing.T) {
	b := batchBase(t)
	test := `{"op":"test","path":"/nodes/6/id","value":"test"}`
	for _, c := range []struct {
		name    string
		mode    provider.Mode
		changes string
		want    Risk
	}{
		{"a larger batch", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/nodes/6/batch/max_size","value":6}]`, Medium},
		{"a smaller minimum", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/nodes/6/batch/min_size","value":1}]`, Medium},
		{"a shorter wait limit", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/nodes/6/batch/max_wait","value":15}]`, Medium},
		{"a longer wait limit", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/nodes/6/batch/max_wait","value":60}]`, Medium},
		// The risk is the models': rewriting the same size beside a higher
		// capacity changes no batch.
		{"the same size rewritten", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/nodes/6/batch/max_size","value":4},` +
			`{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/capacity","value":3}]`, Low},
		{"the batch removed", provider.ModeEdit, `[` + test + `,{"op":"remove","path":"/nodes/6/batch"}]`, Medium},
		{"a batch added", provider.ModeEdit, `[{"op":"test","path":"/nodes/7/id","value":"deploy"},{"op":"add","path":"/nodes/7/batch","value":{"min_size":3,"max_size":3}}]`, Medium},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := run(t, Input{Mode: c.mode, Base: b, Pool: pool(t, b), Pack: input(t, c.mode, proposed(nil, "low")).Pack,
				Output: proposed(ops(t, c.changes), "low")})
			if o.Status != Ready || o.ServerRisk != c.want || o.Risk != c.want {
				t.Fatalf("status %q, server risk %q, risk %q, want ready %q (%v)", o.Status, o.ServerRisk, o.Risk, c.want, codes(o))
			}
		})
	}
	for _, c := range []struct {
		name       string
		mode       provider.Mode
		changes    string
		code, path string
	}{
		// D-054 in a candidate: min_size above max_size, or below it with
		// no wait limit.
		{"min above max", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/nodes/6/batch/min_size","value":5}]`, validate.BatchInvalid, "/nodes/6/batch"},
		{"a scenario removes the wait limit", provider.ModeScenario, `[` + test + `,{"op":"remove","path":"/nodes/6/batch/max_wait"}]`, patch.PathForbidden, "/changes/1/path"},
		// A scenario never adds or removes a batch.
		{"a scenario removes the batch", provider.ModeScenario, `[` + test + `,{"op":"remove","path":"/nodes/6/batch"}]`, patch.PathForbidden, "/changes/1/path"},
		{"a scenario adds a batch", provider.ModeScenario, `[{"op":"test","path":"/nodes/7/id","value":"deploy"},{"op":"add","path":"/nodes/7/batch","value":{"min_size":2,"max_size":2}}]`,
			patch.PathForbidden, "/changes/1/path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := run(t, Input{Mode: c.mode, Base: b, Pool: pool(t, b), Pack: input(t, c.mode, proposed(nil, "low")).Pack,
				Output: proposed(ops(t, c.changes), "low")})
			wantInvalid(t, o, c.code, c.path)
		})
	}
}

// The numbers a proposal's expected effects may quote include the
// batches' sizes and wait limits (values): an effect that names the new
// max_size is no invented number.
func TestBatchValuesInEffects(t *testing.T) {
	b := batchBase(t)
	scenario := func(changes string, effect string) Outcome {
		out := proposed(ops(t, changes), "low")
		out.ExpectedEffects = []string{effect}
		return run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack, Output: out})
	}
	larger := `[{"op":"test","path":"/nodes/6/id","value":"test"},{"op":"replace","path":"/nodes/6/batch/max_size","value":6}]`
	if o := scenario(larger, "Batches of up to 6 instead of 4, started after at most 30 minutes, may use the environment better; a simulation will tell."); o.Status != Ready {
		t.Errorf("the batch's own numbers: %q (%v)", o.Status, codes(o))
	}
	wantInvalid(t, scenario(larger, "Batches of up to 6 cut the waiting by 25%."), OutputInvalid, "/expected_effects/0")
}
