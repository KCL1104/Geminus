package review

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057's server risk for the line-flow parameters (TWIN_IR_SPEC §6):
// shrinking a buffer or making it finite, and any breakdown, changeover,
// calendar, release or batch parameter, is at least medium; a larger or an
// unlimited buffer is low. ServerRisk does not read those members yet: the
// rules come with the task that lifts each primitive's gate (T-047–T-051).
// Until then the primitive gate refuses the candidate and the review never
// computes its risk (T-046 review round 1, L2).
//
// lineFlowRiskCases is where that task registers its cases: each changes
// valid-line-flow-ir.json into a candidate and states the least risk
// ServerRisk must give.
type lineFlowRiskCase struct {
	name    string
	edit    func(candidate *model.ModelIR)
	atLeast Risk
}

var lineFlowRiskCases = map[validate.Primitive][]lineFlowRiskCase{
	// T-047 (D-057): a queue's capacity. In valid-line-flow-ir.json nodes 3
	// (q_burn_in) and 4 (q_line) are finite buffers of 80 and 20.
	validate.FiniteBuffer: {
		{"a smaller buffer", func(c *model.ModelIR) { c.Nodes[4].Capacity = 5 }, Medium},
		{"a buffer of one", func(c *model.ModelIR) { c.Nodes[3].Capacity = 1 }, Medium},
		{"a larger buffer", func(c *model.ModelIR) { c.Nodes[4].Capacity = 40 }, Low},
		{"an unlimited buffer", func(c *model.ModelIR) { c.Nodes[3].Capacity = 0 }, Low},
	},
}

// A primitive the validator checks (its gate lifted) must have server-risk
// cases, and every registered case must hold.
func TestLiftedPrimitivesHaveRiskCases(t *testing.T) {
	for p := range lineFlowRiskCases {
		if !slices.Contains(validate.Primitives, p) {
			t.Errorf("risk cases for %q, which is not a primitive", p)
		}
	}
	decode := func() *model.ModelIR {
		var ir model.ModelIR
		if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-line-flow-ir.json")), &ir); err != nil {
			t.Fatal(err)
		}
		return &ir
	}
	for _, p := range validate.Primitives {
		cases := lineFlowRiskCases[p]
		if validate.Checked(p) && len(cases) == 0 {
			t.Errorf("%s is checked by the validator, but no D-057 server-risk case is registered for it", p)
		}
		for _, c := range cases {
			base, candidate := decode(), decode()
			c.edit(candidate)
			if got := ServerRisk(base, candidate); Max(got, c.atLeast) != got {
				t.Errorf("%s, %s: server risk %s, want at least %s", p, c.name, got, c.atLeast)
			}
		}
	}
}
