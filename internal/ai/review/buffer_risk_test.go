package review

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057 (T-047): a queue's capacity in a scenario, through the whole
// review. A smaller buffer, or a finite one where the base had none, is at
// least medium; a larger or an unlimited one is low (like a resource's
// capacity, D-016); a buffer that closes a blocking cycle is invalid
// (D-050 R1), so no scenario can make a model that can deadlock.
//
// The bases are default-delivery.json after a person's edit (a draft
// publish): an explicit queue q_test (node 9) in front of test, and q_ci
// (node 10) in front of ci, which the CI retry loop enters directly.
func bufferBase(t *testing.T, testCapacity, ciCapacity int) []byte {
	t.Helper()
	q := func(id string, capacity int) string {
		if capacity == 0 {
			return `{"id":"` + id + `","type":"queue"}`
		}
		return `{"id":"` + id + `","type":"queue","capacity":` + itoa(capacity) + `}`
	}
	edit := `[{"op":"add","path":"/nodes/-","value":` + q("q_test", testCapacity) + `},
		{"op":"add","path":"/nodes/-","value":` + q("q_ci", ciCapacity) + `},
		{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/to","value":"q_test"},
		{"op":"test","path":"/flows/1/id","value":"f2"},{"op":"replace","path":"/flows/1/to","value":"q_ci"},
		{"op":"add","path":"/flows/-","value":{"id":"f_q_test","from":"q_test","to":"test","routing":{"type":"probability","probability":1}}},
		{"op":"add","path":"/flows/-","value":{"id":"f_q_ci","from":"q_ci","to":"ci","routing":{"type":"probability","probability":1}}},
		{"op":"add","path":"/views/2d/positions/q_test","value":{"x":0,"y":0}},
		{"op":"add","path":"/views/2d/positions/q_ci","value":{"x":0,"y":0}}]`
	res, err := patch.Apply(base(t), []byte(edit), patch.ManualDraft)
	if err != nil || !res.Applied() {
		t.Fatalf("base edit: %v %v", err, res.PatchIssues)
	}
	return res.Candidate
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestBufferCapacityRisk(t *testing.T) {
	unlimited := bufferBase(t, 0, 0)
	finite := bufferBase(t, 4, 0)
	for _, b := range [][]byte{unlimited, finite} {
		if r := validateDoc(t, b); !r.Valid() {
			t.Fatalf("base: %v", r.Issues)
		}
	}
	testQ := `{"op":"test","path":"/nodes/9/id","value":"q_test"}`
	for _, c := range []struct {
		name    string
		base    []byte
		changes string
		want    Risk
	}{
		{"an unlimited buffer made finite", unlimited, `[` + testQ + `,{"op":"add","path":"/nodes/9/capacity","value":3}]`, Medium},
		{"a smaller buffer", finite, `[` + testQ + `,{"op":"replace","path":"/nodes/9/capacity","value":2}]`, Medium},
		{"a larger buffer", finite, `[` + testQ + `,{"op":"replace","path":"/nodes/9/capacity","value":8}]`, Low},
		{"the buffer made unlimited", finite, `[` + testQ + `,{"op":"remove","path":"/nodes/9/capacity"}]`, Low},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Mode: provider.ModeScenario, Base: c.base, Pool: pool(t, c.base), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack,
				Output: proposed(ops(t, c.changes), "low")}
			o := run(t, in)
			if o.Status != Ready || o.ServerRisk != c.want || o.Risk != c.want {
				t.Fatalf("status %q, server risk %q, risk %q, want ready %q (%v)", o.Status, o.ServerRisk, o.Risk, c.want, codes(o))
			}
		})
	}
	// q_ci feeds ci, which the CI retry loop (ci_result -> ci_failure ->
	// ci_rework -> ci) enters directly: a finite q_ci closes a blocking
	// cycle of ci with itself.
	o := run(t, Input{Mode: provider.ModeScenario, Base: unlimited, Pool: pool(t, unlimited), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack,
		Output: proposed(ops(t, `[{"op":"test","path":"/nodes/10/id","value":"q_ci"},{"op":"add","path":"/nodes/10/capacity","value":2}]`), "low")})
	wantInvalid(t, o, validate.BlockingCycle, "/nodes/10/capacity")
}

func validateDoc(t *testing.T, doc []byte) validate.Result {
	t.Helper()
	r, err := validate.IR(doc)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
