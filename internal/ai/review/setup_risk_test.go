package review

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057 (T-050): changeovers through the whole review. A scenario may
// replace a resource's setups default and each transition's
// distribution; any change to a changeover parameter, either way, is at
// least medium, and a candidate whose setups break D-055 (SETUP_INVALID,
// UNKNOWN_REFERENCE, a malformed distribution) is invalid. Adding or
// removing setups, a transition or the default, or changing initial or a
// transition's types, is an edit.
//
// The base is default-delivery.json after a person's edit (a draft
// publish): a second entity type, hotfix, and setups on reviewer
// (resource 0, one unit of it at code_review): initial change, default
// constant 5, change -> hotfix constant 2.
func setupBase(t *testing.T) []byte {
	t.Helper()
	edit := `[{"op":"add","path":"/entity_types/-","value":{"id":"hotfix","name":"Hotfix"}},
		{"op":"test","path":"/resources/0/id","value":"reviewer"},
		{"op":"add","path":"/resources/0/setups","value":{"initial":"change","default":{"type":"constant","value":5},
			"transitions":[{"from":"change","to":"hotfix","distribution":{"type":"constant","value":2}}]}}]`
	res, err := patch.Apply(base(t), []byte(edit), patch.ManualDraft)
	if err != nil || !res.Applied() {
		t.Fatalf("base edit: %v %v", err, res.PatchIssues)
	}
	if r := validateDoc(t, res.Candidate); !r.Valid() {
		t.Fatalf("base: %v", r.Issues)
	}
	return res.Candidate
}

func TestSetupRisk(t *testing.T) {
	b := setupBase(t)
	test := `{"op":"test","path":"/resources/0/id","value":"reviewer"}`
	for _, c := range []struct {
		name    string
		mode    provider.Mode
		changes string
		want    Risk
	}{
		{"a shorter default", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/default","value":{"type":"constant","value":3}}]`, Medium},
		{"a longer transition", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/transitions/0/distribution","value":{"type":"constant","value":4}}]`, Medium},
		{"a lognormal changeover", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/default","value":{"type":"lognormal","mean":5,"cv":0.5}}]`, Medium},
		// The risk is the models': rewriting the same default beside a
		// higher capacity changes no changeover.
		{"the same default rewritten", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/default","value":{"type":"constant","value":5}},` +
			`{"op":"replace","path":"/resources/0/capacity","value":3}]`, Low},
		{"the setups removed", provider.ModeEdit, `[` + test + `,{"op":"remove","path":"/resources/0/setups"}]`, Medium},
		{"setups added", provider.ModeEdit, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"add","path":"/resources/1/setups","value":{"default":{"type":"constant","value":1}}}]`, Medium},
		{"another initial", provider.ModeEdit, `[` + test + `,{"op":"replace","path":"/resources/0/setups/initial","value":"hotfix"}]`, Medium},
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
		// D-055 in a candidate: a malformed changeover time, a transition
		// listed twice, an initial that names no entity type.
		{"a malformed default", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/default","value":{"type":"triangular","min":5,"mode":2,"max":9}}]`,
			validate.DistributionInvalid, "/resources/0/setups/default"},
		{"a transition listed twice", provider.ModeEdit, `[` + test + `,{"op":"add","path":"/resources/0/setups/transitions/-","value":{"from":"change","to":"hotfix","distribution":{"type":"constant","value":1}}}]`,
			validate.SetupInvalid, "/resources/0/setups/transitions/1"},
		{"an unknown initial", provider.ModeEdit, `[` + test + `,{"op":"replace","path":"/resources/0/setups/initial","value":"nope"}]`,
			validate.UnknownReference, "/resources/0/setups/initial"},
		// A scenario changes only the times: never initial, a transition's
		// types, a new transition, or the setups themselves.
		{"a scenario changes initial", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/initial","value":"hotfix"}]`, patch.PathForbidden, "/changes/1/path"},
		{"a scenario changes a transition's types", provider.ModeScenario, `[` + test + `,{"op":"replace","path":"/resources/0/setups/transitions/0/to","value":"change"}]`, patch.PathForbidden, "/changes/1/path"},
		{"a scenario adds a transition", provider.ModeScenario, `[` + test + `,{"op":"add","path":"/resources/0/setups/transitions/-","value":{"from":"hotfix","to":"change","distribution":{"type":"constant","value":1}}}]`,
			patch.PathForbidden, "/changes/1/path"},
		{"a scenario removes the setups", provider.ModeScenario, `[` + test + `,{"op":"remove","path":"/resources/0/setups"}]`, patch.PathForbidden, "/changes/1/path"},
		{"a scenario adds setups", provider.ModeScenario, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"add","path":"/resources/1/setups","value":{"default":{"type":"constant","value":1}}}]`,
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
// changeover times (values, through the resource's distributions): an
// effect that names the new default is no invented number.
func TestSetupValuesInEffects(t *testing.T) {
	b := setupBase(t)
	scenario := func(changes string, effect string) Outcome {
		out := proposed(ops(t, changes), "low")
		out.ExpectedEffects = []string{effect}
		return run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack, Output: out})
	}
	shorter := `[{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/setups/default","value":{"type":"constant","value":3}}]`
	if o := scenario(shorter, "Changeovers of 3 minutes instead of 5 may free the reviewer; a simulation will tell."); o.Status != Ready {
		t.Errorf("the changeover's own numbers: %q (%v)", o.Status, codes(o))
	}
	wantInvalid(t, scenario(shorter, "Changeovers of 3 minutes cut the lead time by 12%."), OutputInvalid, "/expected_effects/0")
}
