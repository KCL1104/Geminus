package aicontext

import (
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
)

// T-051 (H-029): the electronics line template and its pack's vocabulary
// through the proposal context of the demo's scenario ("burn-in in lots of
// 48"): the whole model fits the default budget (only the server-bound
// model ID and the 2D layout are left out, as for every model), its
// station attributes and synthetic labelling reach the provider, the
// pack's rules are the domain rules, and nothing is redacted or cut.
func TestElectronicsLineTemplateContext(t *testing.T) {
	base, err := electronicsline.Template(electronicsline.DefaultTemplate, mdlA, "電子產線", domainpack.TraditionalChinese)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := idpool.New(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []provider.Mode{provider.ModeScenario, provider.ModeEdit} {
		in := ProposalInput{
			Scope:             Scope{OrganizationID: orgA, ModelID: mdlA},
			Mode:              mode,
			Base:              Version{OrganizationID: orgA, ModelID: mdlA, VersionID: mvA, IR: base},
			Domain:            electronicsline.Pack{}.PromptContext(),
			Intent:            "燒機改成每批 48 片，其他不要動。",
			SelectedEntityIDs: []string{"burn_in"},
			Pool:              Pool{OrganizationID: orgA, ProposalID: aipA, IDs: pool},
			Budget:            provider.DefaultBudget(),
		}
		ctx, err := Proposal(in)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		p := ctx.Prompt
		if p.Verify() != nil {
			t.Fatalf("%s: the prompt does not verify", mode)
		}
		var om struct {
			Omitted  []Omission     `json:"omitted"`
			Redacted map[string]int `json:"redacted"`
		}
		decode(t, blocks(t, p)["CONTEXT_OMISSIONS"].Content, &om)
		var what []string
		for _, o := range om.Omitted {
			what = append(what, o.What+":"+string(o.Reason))
		}
		if !slices.Equal(what, []string{"model_ir.model_id:server_bound", "model_ir.views:presentation"}) || len(om.Redacted) != 0 {
			t.Errorf("%s: omitted %v, redacted %v", mode, what, om.Redacted)
		}
		model := string(blocks(t, p)["CURRENT_MODEL_IR"].Content)
		for _, s := range []string{`"station":"burn_in"`, `"result_of":"fct"`, `"max_size":40`, `"synthetic":true`, `"shift_capacity"`, "燒機"} {
			if !strings.Contains(model, s) {
				t.Errorf("%s: the model in context has no %s", mode, s)
			}
		}
		for _, s := range []string{"Domain: electronics_line.", "domain_attributes station", "result_of", "not OEE", "synthetic, for demonstration"} {
			if !strings.Contains(p.System, s) {
				t.Errorf("%s: the domain rules do not say %q", mode, s)
			}
		}
	}
}
