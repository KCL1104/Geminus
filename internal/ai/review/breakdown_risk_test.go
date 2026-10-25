package review

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057 (T-048): the breakdowns' TTF and TTR distributions in a scenario,
// through the whole review. Any change to them is at least medium, in
// whichever direction (a faster repair as much as a slower one), and so is
// adding or removing breakdowns in an edit; any distribution a scenario may
// change can be lognormal or Weibull, whose risk is that of the member
// (a service time stays low). D-016's rules are unchanged.
//
// The base is default-delivery.json after a person's edit (a draft
// publish) that gives ci_runner (resource 1) breakdowns: TTF exponential
// 600, TTR constant 30.
func breakdownBase(t *testing.T) []byte {
	t.Helper()
	edit := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},
		{"op":"add","path":"/resources/1/breakdowns","value":{"time_to_failure":{"distribution":{"type":"exponential","mean":600}},
			"time_to_repair":{"distribution":{"type":"constant","value":30}}}}]`
	res, err := patch.Apply(base(t), []byte(edit), patch.ManualDraft)
	if err != nil || !res.Applied() {
		t.Fatalf("base edit: %v %v", err, res.PatchIssues)
	}
	if r := validateDoc(t, res.Candidate); !r.Valid() {
		t.Fatalf("base: %v", r.Issues)
	}
	return res.Candidate
}

func TestBreakdownRisk(t *testing.T) {
	b := breakdownBase(t)
	ci := `{"op":"test","path":"/resources/1/id","value":"ci_runner"}`
	review := `{"op":"test","path":"/nodes/1/id","value":"code_review"}`
	ttf, ttr := `/resources/1/breakdowns/time_to_failure/distribution`, `/resources/1/breakdowns/time_to_repair/distribution`
	for _, c := range []struct {
		name    string
		mode    provider.Mode
		changes string
		want    Risk
	}{
		{"a longer time to failure", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"` + ttf + `","value":{"type":"exponential","mean":900}}]`, Medium},
		{"a shorter time to failure", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"` + ttf + `","value":{"type":"exponential","mean":300}}]`, Medium},
		{"a faster repair", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"` + ttr + `","value":{"type":"constant","value":10}}]`, Medium},
		{"a lognormal repair", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"` + ttr + `","value":{"type":"lognormal","mean":30,"cv":0.5}}]`, Medium},
		{"a Weibull time to failure", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"` + ttf + `","value":{"type":"weibull","shape":1.5,"scale":650}}]`, Medium},
		// The risk is the models', not the patch's: a replace that writes
		// the same repair time, beside a higher capacity, changes no
		// breakdowns (alone it would be a no-op, which is refused).
		{"the same breakdowns rewritten", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"` + ttr + `","value":{"type":"constant","value":30}},` +
			`{"op":"replace","path":"/resources/1/capacity","value":4}]`, Low},
		{"a lognormal service time", provider.ModeScenario, `[` + review + `,{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"lognormal","mean":240,"cv":0.25}}]`, Low},
		{"a Weibull service time", provider.ModeScenario, `[` + review + `,{"op":"replace","path":"/nodes/1/service_time/distribution","value":{"type":"weibull","shape":2,"scale":270}}]`, Low},
		{"breakdowns removed", provider.ModeEdit, `[` + ci + `,{"op":"remove","path":"/resources/1/breakdowns"}]`, Medium},
		{"breakdowns added", provider.ModeEdit, `[{"op":"test","path":"/resources/2/id","value":"test_environment"},{"op":"add","path":"/resources/2/breakdowns","value":` +
			`{"time_to_failure":{"distribution":{"type":"constant","value":480}},"time_to_repair":{"distribution":{"type":"constant","value":15}}}}]`, Medium},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := run(t, Input{Mode: c.mode, Base: b, Pool: pool(t, b), Pack: input(t, c.mode, proposed(nil, "low")).Pack,
				Output: proposed(ops(t, c.changes), "low")})
			if o.Status != Ready || o.ServerRisk != c.want || o.Risk != c.want {
				t.Fatalf("status %q, server risk %q, risk %q, want ready %q (%v)", o.Status, o.ServerRisk, o.Risk, c.want, codes(o))
			}
		})
	}
	// A cv out of range is refused by the candidate's validation; a
	// scenario cannot add breakdowns (D-057: draft and publish).
	o := run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack,
		Output: proposed(ops(t, `[`+ci+`,{"op":"replace","path":"`+ttr+`","value":{"type":"lognormal","mean":30,"cv":12}}]`), "low")})
	wantInvalid(t, o, validate.DistributionInvalid, ttr)
	o = run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack,
		Output: proposed(ops(t, `[{"op":"test","path":"/resources/2/id","value":"test_environment"},{"op":"add","path":"/resources/2/breakdowns","value":`+
			`{"time_to_failure":{"distribution":{"type":"constant","value":480}},"time_to_repair":{"distribution":{"type":"constant","value":15}}}}]`), "low")})
	wantInvalid(t, o, patch.PathForbidden, "/changes/1/path")
	// R3 in a candidate: a second unit of a resource that breaks down.
	o = run(t, Input{Mode: provider.ModeEdit, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeEdit, proposed(nil, "low")).Pack,
		Output: proposed(ops(t, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":4},`+
			`{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/requires/0/units","value":2}]`), "low")})
	wantInvalid(t, o, validate.ResourceRequirementInvalid, "/nodes/2/requires/0/units")
}

// The numbers a proposal's expected effects may quote include the
// breakdown parameters and the lognormal and Weibull ones (values): an
// effect that names the new repair time is no invented number.
func TestBreakdownValuesInEffects(t *testing.T) {
	b := breakdownBase(t)
	out := proposed(ops(t, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},`+
		`{"op":"replace","path":"/resources/1/breakdowns/time_to_repair/distribution","value":{"type":"lognormal","mean":20,"cv":0.4}}]`), "low")
	out.ExpectedEffects = []string{"With a mean repair of 20 minutes (cv 0.4) instead of 30, CI may wait less; a simulation will tell."}
	o := run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack, Output: out})
	if o.Status != Ready {
		t.Fatalf("status %q (%v)", o.Status, codes(o))
	}
	out.ExpectedEffects = []string{"With a mean repair of 20 minutes, CI waits 37% less."}
	o = run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack, Output: out})
	wantInvalid(t, o, OutputInvalid, "/expected_effects/0")
}
