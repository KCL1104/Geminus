package aicontext

import (
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/productionlogistics"
)

// H-045: each production and logistics template, in both locales, through
// the proposal context the modeler and scenario prompts get: the whole
// model fits the default budget (only the server-bound model ID and the 2D
// layout are left out, as for every model), its synthetic labelling
// reaches the provider, the pack's rules are the domain rules, and nothing
// is redacted or cut.
func TestProductionLogisticsTemplateContext(t *testing.T) {
	intent := map[string]string{
		productionlogistics.ShoeFactory: "Add eight stitchers to the closing room.",
		productionlogistics.FoodPlant:   "隧道爐增加到 20 個位置，其他不要動。",
		productionlogistics.CartonPlant: "Cut the makeready on the flexo folder-gluer to 8 minutes.",
		productionlogistics.Warehouse:   "Add one picker to the late shift.",
	}
	for _, name := range productionlogistics.Templates() {
		for _, locale := range domainpack.Locales {
			base, err := productionlogistics.Template(name, mdlA, "Plant", locale)
			if err != nil {
				t.Fatal(err)
			}
			pool, err := idpool.New(base)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []provider.Mode{provider.ModeScenario, provider.ModeEdit} {
				in := ProposalInput{
					Scope:  Scope{OrganizationID: orgA, ModelID: mdlA},
					Mode:   mode,
					Base:   Version{OrganizationID: orgA, ModelID: mdlA, VersionID: mvA, IR: base},
					Domain: productionlogistics.Pack{}.PromptContext(),
					Intent: intent[name],
					Pool:   Pool{OrganizationID: orgA, ProposalID: aipA, IDs: pool},
					Budget: provider.DefaultBudget(),
				}
				ctx, err := Proposal(in)
				if err != nil {
					t.Fatalf("%s in %s, %s: %v", name, locale, mode, err)
				}
				p := ctx.Prompt
				if p.Verify() != nil {
					t.Fatalf("%s in %s, %s: the prompt does not verify", name, locale, mode)
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
					t.Errorf("%s in %s, %s: omitted %v, redacted %v", name, locale, mode, what, om.Redacted)
				}
				model := string(blocks(t, p)["CURRENT_MODEL_IR"].Content)
				for _, s := range []string{`"synthetic":true`, `"breakdowns"`, `"calendar_id"`, `"production_logistics"`, base.Nodes[len(base.Nodes)-1].Name} {
					if !strings.Contains(model, s) {
						t.Errorf("%s in %s, %s: the model in context has no %s", name, locale, mode, s)
					}
				}
				for _, s := range []string{"Domain: production_logistics.", "never by product", "pick paths are not modelled", "synthetic, for demonstration"} {
					if !strings.Contains(p.System, s) {
						t.Errorf("%s in %s, %s: the domain rules do not say %q", name, locale, mode, s)
					}
				}
			}
		}
	}
}
